package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const ControlSpaceCapabilities = "space.set_capabilities"

func spaceCapabilitiesResource(space string) string { return "space.capabilities:" + space }

type SpaceCapabilities struct {
	Selection  CapabilitySelection `json:"selection"`
	Configured bool                `json:"configured"`
}

func (service *ControlService) SpaceCapabilitySnapshot(ctx context.Context, space string) (SpaceCapabilities, map[string]uint64, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	state, err := NewTaskService(service.core).SpaceCapabilities(ctx, space)
	resource := spaceCapabilitiesResource(space)
	return state, map[string]uint64{resource: service.revisionLocked(resource)}, err
}

func (tasks *TaskService) SpaceCapabilities(ctx context.Context, space string) (SpaceCapabilities, error) {
	if _, err := tasks.Space(ctx, space); err != nil {
		return SpaceCapabilities{}, err
	}
	var raw string
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "space/capabilities/"+space).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return SpaceCapabilities{}, nil
	}
	state := SpaceCapabilities{Configured: err == nil}
	if err == nil && json.Unmarshal([]byte(raw), &state.Selection) != nil {
		err = ErrStoreFormat
	}
	return state, err
}

func initializeSpaceCapabilitiesTx(ctx context.Context, tx *sql.Tx, chat, space string) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "space/capabilities/"+space).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		var source string
		if err = tx.QueryRowContext(ctx, `SELECT source FROM fa.spaces WHERE id=$1`, space).Scan(&source); err != nil {
			return err
		}
		if source == SpaceSourceLocal {
			return nil
		}
		raw = string(mustJSON(CapabilitySelection{}.normalize()))
	} else if err != nil {
		return err
	}
	var selected CapabilitySelection
	if json.Unmarshal([]byte(raw), &selected) != nil {
		return ErrStoreFormat
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, capabilityMetaPrefix+chat, string(mustJSON(selected.normalize())))
	return err
}

func (tasks *TaskService) validateSpaceCapabilities(ctx context.Context, space string, selection CapabilitySelection) error {
	if _, err := tasks.Space(ctx, space); err != nil {
		return err
	}
	if err := selection.validate(tasks.Config()); err != nil {
		return err
	}
	if err := tasks.core.validateCapabilitySettings(selection); err != nil {
		return err
	}
	for id, value := range selection.Variants {
		raw, err := tasks.ExtensionState(ctx, id, "", "", "owner", space)
		if err != nil {
			return err
		}
		var state struct {
			Choices []struct {
				ID string `json:"id"`
			} `json:"choices"`
		}
		if json.Unmarshal(raw, &state) != nil {
			return ErrStoreFormat
		}
		allowed := false
		for _, choice := range state.Choices {
			allowed = allowed || choice.ID == value
		}
		if !allowed {
			return ErrPermissionDenied
		}
	}
	return nil
}
