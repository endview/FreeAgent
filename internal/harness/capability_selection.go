package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const capabilityMetaPrefix = "chat/capabilities/"

func (store *Store) selectedCapabilities(ctx context.Context, conversationID string, config Config) (CapabilitySelection, error) {
	if _, err := store.GetConversation(ctx, conversationID); err != nil {
		return CapabilitySelection{}, err
	}
	var raw string
	err := store.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`,
		capabilityMetaPrefix+conversationID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultCapabilities(config), nil
	}
	if err != nil {
		return CapabilitySelection{}, err
	}
	var selection CapabilitySelection
	if json.Unmarshal([]byte(raw), &selection) != nil || selection.validate(config) != nil {
		return CapabilitySelection{}, ErrStoreFormat
	}
	return selection.normalize(), nil
}

func (tasks *TaskService) SelectedCapabilities(ctx context.Context, conversationID string) (CapabilitySelection, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return CapabilitySelection{}, errors.New("harness: task service and context are required")
	}
	return tasks.core.Store.selectedCapabilities(ctx, conversationID, tasks.Config())
}

// SelectCapabilities changes only the conversation default. A submitted turn
// may still pass an explicit selection, which is frozen in its run snapshot.
func (tasks *TaskService) SelectCapabilities(ctx context.Context, conversationID string, selection CapabilitySelection) error {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return errors.New("harness: task service and context are required")
	}
	selection = selection.normalize()
	if err := selection.validate(tasks.Config()); err != nil {
		return err
	}
	if _, err := tasks.core.Store.GetConversation(ctx, conversationID); err != nil {
		return err
	}
	raw, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	_, err = tasks.core.Store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		capabilityMetaPrefix+conversationID, string(raw))
	return err
}
