package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/channelwire"
)

var errChannelPreparing = errors.New("channel: result generated; transport preparation is pending")

func (b *ChannelBridge) RegisterCapabilities(id string, capabilities channelwire.Capabilities) error {
	if capabilities.Version != channelwire.Version || capabilities.Format == "" || len(capabilities.Format) > 128 || hasControl(capabilities.Format) || len(capabilities.Actions) > 16 || len(capabilities.Reactions) > 128 {
		return ErrRequestKey
	}
	seen := map[string]bool{}
	for _, action := range capabilities.Actions {
		if seen[action] || !slices.Contains([]string{"prepare_text", "prepare_resource", "text", "file", "download", "sticker_set", "sticker", "reaction"}, action) {
			return ErrRequestKey
		}
		seen[action] = true
	}
	if !seen["prepare_text"] || !seen["text"] {
		return ErrRequestKey
	}
	for _, reaction := range capabilities.Reactions {
		if reaction == "" || len(reaction) > 64 || !utf8.ValidString(reaction) || hasControl(reaction) {
			return ErrRequestKey
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.connections[id]
	if c == nil {
		return ErrPermissionDenied
	}
	if c.capabilities.Format != "" && string(mustJSON(c.capabilities)) != string(mustJSON(capabilities)) {
		return ErrApprovalChanged
	}
	c.capabilities = capabilities
	c.lastSeen = time.Now()
	return nil
}

func (b *ChannelBridge) capabilitiesFor(target ChannelTarget) channelwire.Capabilities {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.connections {
		if c.config.Source == target.Source && c.config.ApplicationID == target.ApplicationID {
			return c.capabilities
		}
	}
	return channelwire.Capabilities{}
}

func (b *ChannelBridge) validReaction(target ChannelTarget, emoji string) bool {
	caps := b.capabilitiesFor(target)
	return slices.Contains(caps.Actions, "reaction") && (emoji == "" || slices.Contains(caps.Reactions, emoji))
}

func validatePrepared(op channelwire.Operation, raw []byte) (channelwire.Prepared, error) {
	var prepared channelwire.Prepared
	if len(raw) > 1<<20 || json.Unmarshal(raw, &prepared) != nil || prepared.SourceHash != op.SourceHash {
		return prepared, ErrStoreFormat
	}
	if op.Kind == "prepare_resource" {
		if !validPreparedResource(prepared.Resource) || len(prepared.Parts) > 0 {
			return prepared, ErrStoreFormat
		}
		return prepared, nil
	}
	if prepared.Format != op.FormatVersion || len(prepared.Parts) < 1 || len(prepared.Parts) > 256 {
		return prepared, ErrStoreFormat
	}
	for _, part := range prepared.Parts {
		if part.Text == "" || !utf8.ValidString(part.Text) || len(part.Text) > 64<<10 || len(part.Entities) > 512 {
			return prepared, ErrStoreFormat
		}
		size := channelwire.UTF16Len(part.Text)
		for _, entity := range part.Entities {
			if entity.Offset < 0 || entity.Length < 1 || entity.Offset > size-entity.Length || len(entity.Type) > 64 || len(entity.URL) > 8192 || len(entity.Language) > 128 {
				return prepared, ErrStoreFormat
			}
		}
	}
	return prepared, nil
}

func (b *ChannelBridge) prepareText(ctx context.Context, id string, target ChannelTarget, text string) ([]channelwire.TextPart, string, error) {
	caps := b.capabilitiesFor(target)
	if caps.Format == "" {
		return nil, "", errChannelPreparing
	}
	op, err := b.perform(ctx, id, channelwire.Operation{Kind: "prepare_text", Target: channelwire.Target(target), Text: text, SourceHash: contentHash([]byte(text)), FormatVersion: caps.Format, Limit: 1 << 20})
	if isChannelQueued(err) {
		return nil, "", errChannelPreparing
	}
	if err != nil {
		return nil, "", err
	}
	prepared, err := validatePrepared(op, op.Data)
	return prepared.Parts, prepared.Format, err
}

func (b *ChannelBridge) prepareResource(ctx context.Context, target ChannelTarget, raw string) (string, error) {
	c, ok := b.connectionFor(target)
	if !ok {
		return "", ErrPermissionDenied
	}
	if !slices.Contains(b.capabilitiesFor(target).Actions, "prepare_resource") {
		return "", ErrToolDisabled
	}
	if _, ok := ctx.Value(telegramFileAuthorityKey{}).(telegramFileAuthority); !ok {
		grant, err := b.tasks.telegramDeliveryGrant(ctx, target, "", true)
		if err != nil {
			return "", err
		}
		ctx = context.WithValue(ctx, telegramFileAuthorityKey{}, telegramFileAuthority{Target: target, Revision: grant.Revision, LocalOwner: true})
	}
	// Resource normalization is pure. A user/Tool request may wait for its
	// prepared reply, while text delivery itself never blocks an outbox pull.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		op, err := b.perform(ctx, c.ID, channelwire.Operation{Kind: "prepare_resource", Target: channelwire.Target(target), Name: "sticker_set", Text: raw, SourceHash: contentHash([]byte(raw)), Limit: 1 << 20})
		if err == nil {
			prepared, err := validatePrepared(op, op.Data)
			return prepared.Resource, err
		}
		if !isChannelQueued(err) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", errChannelPreparing
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func validPreparedResource(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && !hasControl(value) && !strings.ContainsAny(value, "/\\")
}

type pendingChannelReply struct {
	CreatedAt   string `json:"created_at"`
	RunID       string `json:"run_id"`
	Answer      string `json:"answer"`
	Status      string `json:"status"`
	Correlation string `json:"correlation,omitempty"`
	Maintenance bool   `json:"maintenance,omitempty"`
}

func (s *channelSession) pendingReply(ctx context.Context, group string, result RunResult, status string) (string, error) {
	key := "channel/pending-reply/" + s.source + "/" + s.application + "/" + contentHash([]byte(group))
	correlation, _ := ctx.Value(channelEventKey{}).(string)
	if len(result.Answer) > 1<<20 {
		return "", ErrContextLimit
	}
	record := pendingChannelReply{CreatedAt: result.Run.UpdatedAt, RunID: result.Run.ID, Answer: result.Answer, Status: status, Correlation: correlation, Maintenance: result.Maintenance}
	_, err := s.tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, key, string(mustJSON(record)))
	return key, err
}

func (b *ChannelBridge) recoverPreparedReplies(ctx context.Context, id string) error {
	b.mu.Lock()
	c := b.connections[id]
	ready := c != nil && c.capabilities.Format != ""
	b.mu.Unlock()
	if c == nil {
		return ErrPermissionDenied
	}
	if !ready {
		return nil
	}
	prefix := "channel/pending-reply/" + c.config.Source + "/" + c.config.ApplicationID + "/"
	rows, err := b.tasks.core.Store.db.QueryContext(ctx, `SELECT key,value FROM fa.meta WHERE key >= $1 AND key < $2 AND COALESCE(document->>'state','pending')='pending' ORDER BY key LIMIT 16`, prefix, prefix+"\uffff")
	if err != nil {
		return err
	}
	type entry struct{ key, raw string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.key, &e.raw); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, e := range entries {
		var saved pendingChannelReply
		if len(e.raw) > 2<<20 || json.Unmarshal([]byte(e.raw), &saved) != nil {
			return ErrStoreFormat
		}
		run, err := b.tasks.core.Store.GetRun(ctx, saved.RunID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return err
		}
		var cancelled bool
		if err := b.tasks.core.Store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key=$1)`, "channel/run-cancelled/"+run.ID).Scan(&cancelled); err != nil {
			return err
		}
		if cancelled || run.Status != saved.Status {
			if _, err := b.tasks.core.Store.db.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1 AND value=$2`, e.key, e.raw); err != nil {
				return err
			}
			continue
		}
		callCtx := context.WithValue(ctx, channelEventKey{}, saved.Correlation)
		if err := b.Deliver(callCtx, RunResult{Run: run, Answer: saved.Answer, Maintenance: saved.Maintenance}); err != nil {
			state := "preparation_failed"
			if errors.Is(err, ErrPermissionDenied) || errors.Is(err, ErrPermissionRevoked) {
				state = "cancelled"
			}
			if _, saveErr := b.tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{state}',to_jsonb($1::text),true)::text WHERE key=$2 AND value=$3`, state, e.key, e.raw); saveErr != nil {
				return saveErr
			}
		}
	}
	return nil
}
