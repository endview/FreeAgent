package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const contextProjectionVersion = 1

func summaryScope(scopes []string) string {
	if len(scopes) == 0 || scopes[0] == "" {
		return "full"
	}
	return scopes[0]
}
func summarySlotKey(chat, scope string) string { return summaryMetaPrefix + chat + "/" + scope }
func (service *Service) contextScope() string {
	if service.scopedHistory() {
		return service.publicSummaryScope()
	}
	return "full"
}
func validContextScope(scope string) bool {
	return scope == "full" || scope == "public_text" || scope == "channel_public"
}

func readSummaryRaw(ctx context.Context, reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, chat, scope string) (string, error) {
	if !validContextScope(scope) {
		return "", ErrPermissionDenied
	}
	var raw string
	err := reader.QueryRowContext(ctx, `SELECT substr(value,1,$1) FROM fa.meta WHERE key=$2`, maxSummaryRecordBytes+1, summarySlotKey(chat, scope)).Scan(&raw)
	if err == nil && raw == "null" {
		return "", nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = reader.QueryRowContext(ctx, `SELECT substr(value,1,$1) FROM fa.meta WHERE key=$2`, maxSummaryRecordBytes+1, summaryMetaPrefix+chat).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		var saved ConversationSummary
		if json.Unmarshal([]byte(raw), &saved) != nil {
			if scope == "full" {
				return raw, nil
			}
			return "", nil
		}
		if summaryScope([]string{saved.Scope}) != scope {
			return "", nil
		}
	}
	if len(raw) > maxSummaryRecordBytes {
		return "", ErrSummaryStale
	}
	return raw, err
}

func (store *Store) contextVisibility(ctx context.Context, chat, scope string) (string, error) {
	c, err := store.GetConversation(ctx, chat)
	if err != nil {
		return "", err
	}
	values := []any{"context-visibility", contextProjectionVersion, chat, c.SpaceID, scope}
	var legacyBackground bool
	if err = store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fa.messages WHERE conversation_id=$1 AND body_json#>>'{Channel,purpose}'='background')`, chat).Scan(&legacyBackground); err != nil {
		return "", err
	}
	if legacyBackground {
		values = append(values, "exclude-legacy-background/v9")
	}
	return contentHash(mustJSON(values)), nil
}

func (tasks *TaskService) contextReader(ctx context.Context, chat string) (*Service, error) {
	if err := tasks.core.Store.checkLiveConversation(ctx, chat); err != nil {
		return nil, err
	}
	core := tasks.core.runCopy()
	core.Config = tasks.Config()
	// Model tools already have an exact actor/permission selection. Preserve it;
	// resolving a generic Owner selection here could reveal another scope.
	if core.permission == nil {
		if err := core.resolvePermission(ctx, chat, "", false); err != nil {
			return nil, err
		}
	}
	return &core, nil
}
