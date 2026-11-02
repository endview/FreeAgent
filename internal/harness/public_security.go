package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type PublicSecurityEvent struct {
	At     string `json:"at"`
	Source string `json:"source"`
	Reason string `json:"reason"`
}

func (tasks *TaskService) ReadPublicSecurityEvents(ctx context.Context) ([]PublicSecurityEvent, error) {
	var raw string
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='public/security-events'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return []PublicSecurityEvent{}, nil
	}
	if err != nil {
		return nil, err
	}
	var events []PublicSecurityEvent
	if json.Unmarshal([]byte(raw), &events) != nil || len(events) > 256 {
		return nil, ErrStoreFormat
	}
	return events, nil
}
func (tasks *TaskService) AppendPublicSecurityEvent(ctx context.Context, event PublicSecurityEvent) error {
	if len(event.At) > 64 || len(event.Source) > 128 || len(event.Reason) > 128 || strings.ContainsAny(event.At+event.Source+event.Reason, "\x00\r\n") {
		return errors.New("harness: invalid bounded public security event")
	}
	store := tasks.core.Store
	store.securityMu.Lock()
	defer store.securityMu.Unlock()
	events, err := tasks.ReadPublicSecurityEvents(ctx)
	if err != nil {
		return err
	}
	events = append(events, event)
	if len(events) > 256 {
		events = events[len(events)-256:]
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('public/security-events',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(mustJSON(events)))
	return err
}
func (service *Service) recordPublicPolicyChange(ctx context.Context) {
	source := "local"
	if entry, remote := PublicEntryFrom(ctx); remote {
		source = entry.IP
	}
	_ = NewTaskService(service).AppendPublicSecurityEvent(ctx, PublicSecurityEvent{At: nowText(), Source: source, Reason: "public_policy_changed"})
}
