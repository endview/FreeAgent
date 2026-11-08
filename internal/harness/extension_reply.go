package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// Extensions choose logical parts; the Host only enforces a finite transport
// envelope and lossless reconstruction. It never interprets expression modes.
const maxExtensionReplyParts = 3

type extensionReplyParts struct {
	Extension string   `json:"extension"`
	Version   string   `json:"version"`
	Hash      string   `json:"source_hash"`
	Parts     []string `json:"parts"`
}

func validExtensionReplyParts(parts []string, source string) bool {
	if len(parts) == 0 || len(parts) > maxExtensionReplyParts || len(source) > maxExtensionFinishedOutput || !utf8.ValidString(source) {
		return false
	}
	offset := 0
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || !utf8.ValidString(part) || len(part) > len(source)-offset || source[offset:offset+len(part)] != part {
			return false
		}
		offset += len(part)
	}
	return offset == len(source)
}

func (service *Service) saveExtensionReplyParts(ctx context.Context, run Run, manifest ExtensionManifest, parts []string) error {
	if run.Status != "completed" || !manifest.Delivery || !service.replyExpressionsEnabled() ||
		!service.currentConfig().Extensions.Enabled[manifest.ID] || !validExtensionReplyParts(parts, run.Output) {
		return errors.New("harness: extension reply parts require a completed reply and must preserve its exact output")
	}
	plan := extensionReplyParts{Extension: manifest.ID, Version: manifest.Version, Hash: contentHash([]byte(run.Output)), Parts: parts}
	key := "channel/reply-parts/" + run.ID
	raw := string(mustJSON(plan))
	if _, err := service.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO NOTHING`, key, raw); err != nil {
		return err
	}
	var saved string
	if err := service.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, key).Scan(&saved); err != nil {
		return err
	}
	if saved != raw {
		return errors.New("harness: another extension reply plan is already frozen")
	}
	return nil
}

// Revocation before the first enqueue restores the original single logical
// reply. Once rendering/enqueue is frozen, existing per-part receipts remain
// authoritative and a settings change cannot rewrite or replay that group.
func (s *channelSession) extensionReplyParts(ctx context.Context, group, text string) ([]string, error) {
	fallback := []string{text}
	identity := strings.Split(group, "/")
	if len(identity) < 2 || identity[0] != "reply" {
		return fallback, nil
	}
	var raw string
	err := s.tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "channel/reply-parts/"+identity[1]).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return nil, err
	}
	var plan extensionReplyParts
	if len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &plan) != nil || !validExtensionID(plan.Extension) {
		return nil, ErrStoreFormat
	}
	// A status/error continuation uses the same Run identity but different
	// text. A plan never applies to anything except its original full answer.
	if plan.Hash != contentHash([]byte(text)) {
		return fallback, nil
	}
	if !validExtensionReplyParts(plan.Parts, text) {
		return nil, ErrStoreFormat
	}
	config := s.tasks.Config()
	if !config.Extensions.Enabled[plan.Extension] {
		return fallback, nil
	}
	registry, err := scanExtensions(s.tasks.core.Store.DataDir(), config.Workspace, config)
	if err != nil {
		return nil, err
	}
	definition, installed := registry.Extensions[plan.Extension]
	if !installed || registry.Errors[plan.Extension] != nil || !definition.Manifest.Delivery || definition.Manifest.Version != plan.Version {
		return fallback, nil
	}
	run, err := s.tasks.core.Store.GetRun(ctx, identity[1])
	if err != nil {
		return nil, err
	}
	snapshot, err := decodeRunSnapshot(run.ConfigJSON)
	if err != nil {
		return nil, err
	}
	if snapshot.Capabilities == nil || !contains(snapshot.Capabilities.Extensions, plan.Extension) {
		return fallback, nil
	}
	return plan.Parts, nil
}
