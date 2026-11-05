package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

var errChannelInputRejected = errors.New("channel: input retained but not accepted for execution")

type channelReplayKey struct{}
type channelInputIntent struct {
	Event          channelwire.Event `json:"event"`
	ConversationID string            `json:"conversation_id,omitempty"`
	Generation     string            `json:"generation"`
	CommandStarted bool              `json:"command_started,omitempty"`
	Fingerprint    string            `json:"fingerprint"`
	State          string            `json:"state"`
	TargetRun      string            `json:"target_run,omitempty"`
	Revision       uint64            `json:"revision,omitempty"`
	RunID          string            `json:"run_id,omitempty"`
}

func channelInboundKey(connection, event string) string {
	return "channel/inbound/" + connection + "/" + contentHash([]byte(event))
}

func (b *ChannelBridge) acceptInbound(ctx context.Context, c *channelConnection, id string, event channelwire.Event, e channelEvent) (returnErr error) {
	key := channelInboundKey(id, event.ID)
	fingerprint := contentHash(mustJSON(event))
	state := channelInputIntent{Event: event, Generation: b.generation, Fingerprint: fingerprint, State: "pending"}
	var raw string
	err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if raw != "" {
		if json.Unmarshal([]byte(raw), &state) != nil || state.Fingerprint != fingerprint {
			return ErrRequestKey
		}
		if state.State == "scheduled" && state.Generation != b.generation {
			state.State = "pending"
		}
		if state.State != "pending" {
			return nil
		}
		// A Run association is the durable adoption fact even if the receipt
		// or the final journal update was lost. Never dispatch it again.
		var adopted string
		source := ChannelMessageSource{ChannelTarget: e.Target, MessageID: e.Message}
		if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT id FROM fa.runs WHERE request_key=$1`, source.requestKey()).Scan(&adopted); err == nil {
			state.State, state.RunID = "accepted", adopted
			_, err = b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(state)), key)
			return err
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if state.CommandStarted {
			if state.Generation == b.generation {
				return ErrActiveRun
			}
			// A control action can have committed before the old Owner died.
			// Keep the whole intent and expose the interruption; a transport
			// retry cannot authorize replaying it under a new Owner.
			state.State = "interrupted"
			if err := c.session.plain(ctx, e, "上次控制操作在重启时中断；操作内容已保留，请先 /status 核对后重新操作。未重复执行。"); err != nil {
				return err
			}
			_, err = b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(state)), key)
			return err
		}
	} else {
		if auth, ok := ctx.Value(telegramFileAuthorityKey{}).(telegramFileAuthority); ok {
			state.Revision = auth.Revision
		}
		space, err := b.tasks.ChannelSpace(ctx, e.Target)
		if err == nil {
			state.ConversationID = space.ActiveConversationID
			if !space.MultiConversation {
				state.ConversationID = space.DefaultConversationID
			}
			run, err := scanRun(b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM fa.runs WHERE conversation_id=$1 AND `+activeRunPredicate+` LIMIT 1`, space.ActiveConversationID))
			if err == nil {
				state.TargetRun = run.ID
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := b.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, key, string(mustJSON(state))); err != nil {
			return err
		}
	}
	if event.Callback != "" || strings.HasPrefix(strings.TrimSpace(event.Text), "/") {
		state.CommandStarted = true
		if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(state)), key); err != nil {
			return err
		}
	}
	if auth, ok := ctx.Value(telegramFileAuthorityKey{}).(telegramFileAuthority); ok && state.Revision != 0 && state.Revision != auth.Revision {
		state.State = "rejected"
		_, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(state)), key)
		return err
	}
	ctx = context.WithValue(ctx, channelReplayKey{}, state)
	c.flightMu.Lock()
	inFlight := c.flights[event.ID]
	c.flightMu.Unlock()
	if !inFlight {
		err = c.session.handle(ctx, e)
	}
	if state.ConversationID == "" {
		source := ChannelMessageSource{ChannelTarget: e.Target, MessageID: e.Message}
		var chat string
		if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT conversation_id FROM fa.messages WHERE request_key=$1`, source.requestKey()).Scan(&chat); err == nil {
			state.ConversationID = chat
			if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(state)), key); err != nil {
				return err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if isChannelQueued(err) {
		err = nil
	}
	if err != nil {
		if errors.Is(err, ErrActiveRun) || errors.Is(err, ErrPermissionDenied) || errors.Is(err, ErrPermissionRevoked) || errors.Is(err, ErrInvalidSpace) || errors.Is(err, errChannelInputRejected) || errors.Is(err, ErrInputTargetEnded) || errors.Is(err, ErrApprovalChanged) {
			state.State = "rejected"
		} else {
			return err
		}
	} else if !event.Trigger || event.Callback != "" || strings.HasPrefix(strings.TrimSpace(event.Text), "/") {
		state.State = "accepted"
		c.flightMu.Lock()
		if c.flights[event.ID] {
			state.State = "scheduled"
		}
		c.flightMu.Unlock()
	} else {
		source := ChannelMessageSource{ChannelTarget: e.Target, MessageID: e.Message}
		// Admission is not a volatile goroutine reservation: wait for a Run or
		// an existing pending-input record before acknowledging a model input.
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for state.State == "pending" {
			var chat string
			err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT id,conversation_id FROM fa.runs WHERE request_key=$1`, source.requestKey()).Scan(&state.RunID, &chat)
			if err == nil {
				state.State = "accepted"
				break
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if state.ConversationID != "" {
				input, err := loadInput(ctx, b.tasks.core.Store.db, state.ConversationID, source.requestKey())
				if err == nil {
					state.State = input.Status
					if state.State == "pending" || state.State == "adopted" {
						state.State = "accepted"
					}
					break
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return ErrActiveRun
			case <-ticker.C:
			}
		}
	}
	_, err = b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND (document #>> '{state}') IN ('pending','scheduled')`, string(mustJSON(state)), key)
	return err
}

func (b *ChannelBridge) InputReceipt(ctx context.Context, id, event string) string {
	var raw string
	if b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, channelInboundKey(id, event)).Scan(&raw) != nil {
		return "handled"
	}
	var state channelInputIntent
	if json.Unmarshal([]byte(raw), &state) != nil {
		return "unknown"
	}
	return state.State
}

func (b *ChannelBridge) completeInbound(connection, event string, result RunResult, callErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := channelInboundKey(connection, event)
	tx, err := b.tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var raw string
	if tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&raw) != nil {
		return
	}
	var intent channelInputIntent
	if json.Unmarshal([]byte(raw), &intent) != nil || intent.Generation != b.generation {
		return
	}
	if result.Run.ID != "" {
		intent.RunID = result.Run.ID
		intent.State = "accepted"
	} else if callErr != nil {
		intent.State = "rejected"
	} else {
		intent.State = "accepted"
	}
	_, err = tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(intent)), key, raw)
	if err == nil {
		_ = tx.Commit()
	}
}

// Scheduled control actions retain their full intent, but an Owner restart
// cannot authorize reapplying a potentially committed action. Report it once.
func (b *ChannelBridge) recoverInboundNotices(ctx context.Context, id string) error {
	b.mu.Lock()
	c := b.connections[id]
	b.mu.Unlock()
	if c == nil {
		return ErrPermissionDenied
	}
	if !c.inboundMu.TryLock() {
		return nil
	}
	defer c.inboundMu.Unlock()
	prefix := "channel/inbound/" + id + "/"
	rows, err := b.tasks.core.Store.db.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE key>=$1 AND key<$2 AND (document #>> '{state}')='scheduled' AND (document #>> '{generation}')!=$3 ORDER BY key LIMIT 8`, prefix, prefix+"\uffff", b.generation)
	if err != nil {
		return err
	}
	type item struct {
		k, raw string
		i      channelInputIntent
	}
	var items []item
	for rows.Next() {
		var p item
		if err := rows.Scan(&p.k, &p.raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal([]byte(p.raw), &p.i) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		items = append(items, p)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, p := range items {
		e := p.i.Event
		if e.Target.Source == "telegram" {
			grant, err := b.tasks.CheckTelegramAccess(ctx, ChannelTarget(e.Target), e.Person)
			if err != nil || grant.Revision != p.i.Revision {
				p.i.State = "rejected"
			}
		}
		if p.i.State != "rejected" {
			ctx = context.WithValue(ctx, channelEventKey{}, id+"/"+e.ID+"/restart")
			event := channelEvent{Target: ChannelTarget(e.Target), Person: e.Person, Message: e.Message}
			if err := c.session.plain(ctx, event, "上次控制操作在 Owner 重启时中断，原操作内容已保留；请先 /status 核对，再明确重新操作。未自动重复执行。"); err != nil {
				return err
			}
			p.i.State = "interrupted"
		}
		if _, err := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2 AND value=$3`, string(mustJSON(p.i)), p.k, p.raw); err != nil {
			return err
		}
	}
	return nil
}
