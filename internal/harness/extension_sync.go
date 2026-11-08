package harness

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

type ExtensionSyncPage struct {
	Processed int    `json:"processed"`
	Skipped   int    `json:"skipped"`
	Next      string `json:"next,omitempty"`
	Replayed  bool   `json:"model_or_tool_replayed"`
}
type extensionSyncCursor struct {
	Chat     string `json:"chat"`
	Sequence int64  `json:"sequence"`
	Binding  string `json:"binding"`
}

// SyncExtensionFacts copies at most 32 original Run projections per call. The
// stable cursor can resume after downtime; it never dispatches work to repair
// missing receipts. Only originally selected extensions receive their original frozen settings.
func (tasks *TaskService) SyncExtensionFacts(ctx context.Context, id, chat, variant, cursor string) (ExtensionSyncPage, error) {
	if !validExtensionID(id) || variant != "" && !validCapabilityID(variant) {
		return ExtensionSyncPage{}, ErrPermissionDenied
	}
	binding := contentHash(mustJSON([]string{id, chat, variant}))
	position := extensionSyncCursor{Binding: binding}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > 2048 || json.Unmarshal(raw, &position) != nil || position.Binding != binding {
			return ExtensionSyncPage{}, errors.New("harness: invalid extension fact cursor")
		}
	}
	rows, err := tasks.core.Store.db.QueryContext(ctx, `SELECT id,conversation_id,seq FROM fa.runs WHERE ($1='' OR conversation_id=$1) AND (conversation_id,seq)>($2,$3) ORDER BY conversation_id,seq LIMIT 33`, chat, position.Chat, position.Sequence)
	if err != nil {
		return ExtensionSyncPage{}, err
	}
	type selected struct {
		ID, Chat string
		Sequence int64
	}
	var records []selected
	for rows.Next() {
		var r selected
		if err = rows.Scan(&r.ID, &r.Chat, &r.Sequence); err != nil {
			rows.Close()
			return ExtensionSyncPage{}, err
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ExtensionSyncPage{}, err
	}
	page := ExtensionSyncPage{}
	for _, r := range records[:min(32, len(records))] {
		position.Chat, position.Sequence = r.Chat, r.Sequence
		run, err := tasks.core.Store.GetRun(ctx, r.ID)
		if err != nil {
			return page, err
		}
		var snapshot runSnapshot
		if json.Unmarshal([]byte(run.ConfigJSON), &snapshot) != nil {
			return page, ErrSnapshotIncomplete
		}
		if snapshot.Capabilities == nil || !contains(snapshot.Capabilities.Extensions, id) || variant != "" && extensionVariant(run, id, snapshot.Config) != variant {
			page.Skipped++
			continue
		}
		facts, err := tasks.ExtensionRunFacts(ctx, r.Chat, r.ID)
		if err != nil {
			return page, err
		}
		facts.Variant = extensionVariant(run, id, snapshot.Config)
		facts.Settings = extensionRunSettings(run, id, snapshot.Config)
		if _, err = tasks.extensionStateRPC(ctx, id, "facts.sync", map[string]any{"identity": facts.Identity, "variant": extensionVariant(run, id, snapshot.Config), "settings": extensionRunSettings(run, id, snapshot.Config), "facts": []ExtensionRunFacts{facts}}); err != nil {
			return page, fmt.Errorf("harness: original facts not synced; inspect extension scope authorization and storage: %w", err)
		}
		page.Processed++
	}
	if len(records) > 32 {
		page.Next = base64.RawURLEncoding.EncodeToString(mustJSON(position))
	}
	return page, nil
}
