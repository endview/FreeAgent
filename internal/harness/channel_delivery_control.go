package harness

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

type DeliveryPart struct {
	ID        string `json:"id"`
	Sequence  int    `json:"sequence"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	MessageID string `json:"message_id,omitempty"`
}
type DeliveryGroup struct {
	CreatedAt        string         `json:"created_at"`
	Cursor           string         `json:"cursor"`
	AfterMS          int64          `json:"after_ms,omitempty"`
	ExpiresMS        int64          `json:"expires_ms,omitempty"`
	Source           string         `json:"source,omitempty"`
	SuppressedReason string         `json:"suppressed_reason,omitempty"`
	ID               string         `json:"id"`
	RunID            string         `json:"run_id"`
	Digest           string         `json:"digest"`
	Status           string         `json:"status"`
	Parts            []DeliveryPart `json:"parts"`
}
type DeliveryCommand struct {
	Rule   string `json:"rule,omitempty"`
	Key    string `json:"key,omitempty"`
	Group  string `json:"group"`
	Digest string `json:"digest"`
	Action string `json:"action"`
}

type deliveryCursor struct {
	Chat string `json:"chat"`
	At   string `json:"at"`
	ID   string `json:"id"`
}

// ChannelDeliveries returns one bounded page of complete groups, newest first.
// The last group's cursor selects the next page; no group's approval digest is
// ever calculated from a row-limited subset of its parts.
func (tasks *TaskService) ChannelDeliveries(ctx context.Context, chat string, cursor ...string) ([]DeliveryGroup, error) {
	conversation, err := tasks.core.Store.GetConversation(ctx, chat)
	if err != nil {
		return nil, err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return nil, ErrConversationSpace
	}
	after := deliveryCursor{Chat: chat, At: "9999-12-31T00:00:00Z", ID: ""}
	if len(cursor) > 1 {
		return nil, ErrRequestKey
	}
	if len(cursor) == 1 && cursor[0] != "" {
		if len(cursor[0]) > 2048 {
			return nil, ErrRequestKey
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor[0])
		if err != nil || decodeControlJSON(raw, &after) != nil || after.Chat != chat || after.ID == "" || len(after.ID) > 256 || hasControl(after.ID) {
			return nil, ErrRequestKey
		}
		if _, err := time.Parse(time.RFC3339Nano, after.At); err != nil {
			return nil, ErrRequestKey
		}
	}
	rows, err := tasks.core.Store.db.QueryContext(ctx, `WITH scoped AS MATERIALIZED (
 SELECT value,document FROM fa.meta WHERE key >= 'channel/operation/' AND key < 'channel/operation0'
 AND document->>'conversation_id'=$1 AND document->>'run_id'<>'' AND document->>'kind' IN ('text','file','sticker','reaction')
 UNION ALL
 SELECT jsonb_build_object('id',m.key,'group',m.key,'run_id',r.id,'kind','preparation','state',CASE WHEN m.document->>'state'='preparation_failed' THEN 'preparation_failed' ELSE 'preparing' END,'created_at',r.updated_at,'sequence',1)::text,
 jsonb_build_object('id',m.key,'group',m.key,'run_id',r.id,'kind','preparation','state',CASE WHEN m.document->>'state'='preparation_failed' THEN 'preparation_failed' ELSE 'preparing' END,'created_at',r.updated_at,'sequence',1)
 FROM fa.meta m JOIN fa.runs r ON r.id=m.document->>'run_id' WHERE m.key >= 'channel/pending-reply/' AND m.key < 'channel/pending-reply0' AND r.conversation_id=$1 AND COALESCE(m.document->>'state','pending') IN ('pending','preparation_failed')),
 groups AS (SELECT document->>'group' AS id,min((document->>'created_at')::timestamptz) AS created FROM scoped GROUP BY 1),
 page AS (SELECT * FROM groups WHERE (created,id)<($2::timestamptz,$3::text) ORDER BY created DESC,id DESC LIMIT 50)
 SELECT o.value,p.created FROM scoped o JOIN page p ON o.document->>'group'=p.id
 ORDER BY p.created DESC,p.id DESC,(o.document->>'sequence')::int`, chat, after.At, after.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []DeliveryGroup{}
	indices := map[string]int{}
	raws := map[string][]string{}
	for rows.Next() {
		var raw string
		var created time.Time
		var op channelwire.Operation
		if err := rows.Scan(&raw, &created); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &op) != nil {
			return nil, ErrStoreFormat
		}
		idx, ok := indices[op.Group]
		if !ok {
			idx = len(groups)
			indices[op.Group] = idx
			at := created.UTC().Format(time.RFC3339Nano)
			groups = append(groups, DeliveryGroup{ID: op.Group, RunID: op.RunID, CreatedAt: at, Cursor: base64.RawURLEncoding.EncodeToString(mustJSON(deliveryCursor{Chat: chat, At: at, ID: op.Group})), Status: "sent", Parts: []DeliveryPart{}})
		}
		group := &groups[idx]
		if op.Proactive {
			group.AfterMS, group.ExpiresMS, group.Source, group.SuppressedReason = op.NotBeforeMS, op.ExpiresMS, op.Extension, op.SuppressedReason
		}
		group.Parts = append(group.Parts, DeliveryPart{ID: op.ID, Sequence: op.Sequence, Kind: op.Kind, State: op.State, MessageID: op.MessageID})
		raws[op.Group] = append(raws[op.Group], raw)
		if op.State != "sent" {
			if op.State == "unknown" || op.State == "failed" || op.State == "paused" {
				group.Status = "partial"
			} else if group.Status != "partial" {
				group.Status = op.State
			}
		}
	}
	for i := range groups {
		groups[i].Digest = contentHash(mustJSON(raws[groups[i].ID]))
	}
	return groups, rows.Err()
}

func (tasks *TaskService) ControlDelivery(ctx context.Context, chat string, command DeliveryCommand) error {
	if command.Action != "resume" && command.Action != "abandon" || command.Group == "" || len(command.Digest) != 64 {
		return ErrRequestKey
	}
	bridge := tasks.core.Store.channelBridge.Load()
	if bridge == nil {
		return ErrToolDisabled
	}
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	// Group controls and transport claims share this mutex; the transaction also
	// locks each durable row and retains the exact review digest.
	conversation, err := tasks.core.Store.GetConversation(ctx, chat)
	if err != nil {
		return err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return ErrConversationSpace
	}
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize the cancellation marker with whole-group enqueue, including
	// members that do not exist yet when the preview is confirmed.
	var connection string
	if err := tx.QueryRowContext(ctx, `SELECT document->>'connection' FROM fa.meta WHERE key >= 'channel/operation/' AND key < 'channel/operation0' AND document->>'conversation_id'=$1 AND document->>'group'=$2 LIMIT 1`, chat, command.Group).Scan(&connection); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, connection+"/"+command.Group); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'conversation_id'=$1 AND document->>'group'=$2 ORDER BY (document->>'sequence')::int FOR UPDATE`, chat, command.Group)
	if err != nil {
		return err
	}
	type entry struct {
		key, raw string
		op       channelwire.Operation
	}
	entries := []entry{}
	raws := []string{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.key, &e.raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal([]byte(e.raw), &e.op) != nil {
			rows.Close()
			return ErrStoreFormat
		}
		entries = append(entries, e)
		raws = append(raws, e.raw)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	reviewKey := "channel/delivery-review/" + contentHash(mustJSON([]string{chat, command.Group, command.Digest, command.Action}))
	var applied string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, reviewKey).Scan(&applied); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if len(entries) == 0 || command.Digest != contentHash(mustJSON(raws)) {
		return ErrApprovalChanged
	}
	if command.Action == "abandon" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,'true') ON CONFLICT(key) DO NOTHING`, channelGroupCancelKey(connection, command.Group)); err != nil {
			return err
		}
	}
	for _, e := range entries {
		op := e.op
		if command.Action == "resume" && op.Target.Source == SpaceSourceTelegram {
			// Query outside this transaction only after closing its row iterator.
			grant, err := tasks.telegramDeliveryGrant(ctx, ChannelTarget(op.Target), op.Person, op.LocalOwner)
			if err != nil || grant.Revision != op.AccessRevision {
				return ErrPermissionDenied
			}
		}
		switch op.State {
		case "scheduled", "reviewing", "held":
			if command.Action == "abandon" && op.Attempts == 0 {
				op.State = "cancelled"
			} else {
				continue
			}
		case "pending", "paused":
			if command.Action == "resume" && op.Attempts != 0 {
				continue
			}
			if command.Action == "abandon" {
				op.State = "cancelled"
			} else {
				op.State = "pending"
				op.ResumedMS = time.Now().UnixMilli()
			}
		case "unknown", "failed":
			if command.Action == "resume" {
				op.ReviewedUncertainty = true
			}
		default:
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(op)), e.key); err != nil {
			return err
		}
	}
	if command.Action == "resume" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1`, channelGroupPauseKey(entries[0].op.Connection, command.Group)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, reviewKey, string(mustJSON(command))); err != nil {
		return err
	}
	return tx.Commit()
}
