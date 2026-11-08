package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const capabilityMetaPrefix = "chat/capabilities/"

func (store *Store) selectedCapabilities(ctx context.Context, conversationID string, config Config) (CapabilitySelection, error) {
	chat, err := store.GetConversation(ctx, conversationID)
	if err != nil {
		return CapabilitySelection{}, err
	}
	var raw string
	err = store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`,
		capabilityMetaPrefix+conversationID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		if chat.SpaceID != LocalSpaceID {
			policy, err := store.spacePermission(ctx, chat.SpaceID)
			if err != nil {
				return CapabilitySelection{}, err
			}
			if !policy.OwnerPrivate {
				return CapabilitySelection{}, nil
			}
		}
		return defaultCapabilities(config), nil
	}
	if err != nil {
		return CapabilitySelection{}, err
	}
	var selection CapabilitySelection
	if json.Unmarshal([]byte(raw), &selection) != nil {
		return CapabilitySelection{}, ErrStoreFormat
	}
	selection = selection.normalize()
	// Revoking an optional method removes future use without making its old
	// conversation or analysis results unreadable.
	activeSkills := selection.Skills[:0]
	for _, id := range selection.Skills {
		if config.Skills.Enabled[id] {
			activeSkills = append(activeSkills, id)
		}
	}
	selection.Skills = activeSkills
	// Disabling/uninstalling an extension revokes its defaults; it must not
	// make the conversation unreadable or prevent ordinary Core-only turns.
	enabled := selection.Extensions[:0]
	for _, id := range selection.Extensions {
		if config.Extensions.Enabled[id] {
			enabled = append(enabled, id)
		} else {
			delete(selection.Variants, id)
			delete(selection.Settings, id)
		}
	}
	selection.Extensions = enabled
	if selection.validate(config) != nil {
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
	if err := tasks.core.validateCapabilitySettings(selection); err != nil {
		return err
	}
	if _, err := tasks.core.Store.GetConversation(ctx, conversationID); err != nil {
		return err
	}
	raw, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	_, err = tasks.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		capabilityMetaPrefix+conversationID, string(raw))
	return err
}
