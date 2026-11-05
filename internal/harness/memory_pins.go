package harness

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
)

// Pinning is an attribute of a versioned Memory entry. It neither creates a
// second knowledge store nor grants tools or an extension permission.
type MemoryPin struct {
	Scope  string `json:"scope"`
	Target string `json:"target,omitempty"`
}

func (pin MemoryPin) validate() error {
	switch pin.Scope {
	case "conversation":
		if !memoryID(pin.Target) {
			return ErrMemoryDenied
		}
	case "workspace":
		if !filepath.IsAbs(pin.Target) || filepath.Clean(pin.Target) != pin.Target {
			return ErrMemoryDenied
		}
	case "global":
		if pin.Target != "" {
			return ErrMemoryDenied
		}
	default:
		return ErrMemoryDenied
	}
	return nil
}

func validatePinTarget(ctx context.Context, reader metaReader, pin *MemoryPin, project string) error {
	if pin == nil {
		return nil
	}
	if pin.validate() != nil {
		return ErrMemoryDenied
	}
	if pin.Scope == "workspace" && pin.Target != project {
		return ErrMemoryDenied
	}
	if pin.Scope == "conversation" {
		var workspace string
		if err := reader.QueryRowContext(ctx, `SELECT workspace FROM fa.conversations WHERE id=$1`, pin.Target).Scan(&workspace); err != nil || workspace != project {
			return ErrMemoryDenied
		}
	}
	return nil
}
func validateLivePin(ctx context.Context, reader metaReader, run Run, pin MemoryUse, project string) error {
	var spaceID, raw string
	if err := reader.QueryRowContext(ctx, `SELECT space_id FROM fa.conversations WHERE id=$1`, run.ConversationID).Scan(&spaceID); err != nil {
		return err
	}
	var permission PermissionSelection
	if err := reader.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "space/permission/"+spaceID).Scan(&raw); err != nil || strictDecode([]byte(raw), &permission) != nil {
		return ErrMemoryDenied
	}
	var selection MemorySelection
	err := reader.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "chat/memory/"+run.ConversationID).Scan(&raw)
	if err == nil {
		if strictDecode([]byte(raw), &selection) != nil {
			return ErrMemoryDenied
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if slices.Contains(selection.ExcludedPins, pin.EntryID) || pin.Pinned == nil {
		return ErrMemoryDenied
	}
	private := spaceID == LocalSpaceID || permission.OwnerPrivate
	allowed := pin.Pinned.Scope == "conversation" && pin.Pinned.Target == run.ConversationID || private && (pin.Pinned.Scope == "global" || pin.Pinned.Scope == "workspace" && pin.Pinned.Target == project) || slices.Contains(selection.PinnedGrants, pin.EntryID)
	if !allowed {
		return ErrMemoryDenied
	}
	return nil
}

func (service *Service) selectPinnedMemory(ctx context.Context, chat string, selection MemorySelection) ([]MemoryUse, error) {
	conversation, err := service.Store.GetConversation(ctx, chat)
	if err != nil {
		return nil, err
	}
	policy, err := service.Store.spacePermission(ctx, conversation.SpaceID)
	if err != nil {
		return nil, err
	}
	private := conversation.SpaceID == LocalSpaceID || policy.OwnerPrivate
	// Scope filters precede content retrieval. Explicit grants are saved by
	// the same local confirmation boundary as other conversation selections.
	rows, err := service.Store.db.QueryContext(ctx, `SELECT `+memoryColumns+` FROM fa.memories WHERE state='active' AND task_id='' AND (project='' OR project=$1) AND pin_json!='null' AND (((pin_json #>> '{scope}')='conversation' AND (pin_json #>> '{target}')=$2) OR ($3 AND ((pin_json #>> '{scope}')='global' OR ((pin_json #>> '{scope}')='workspace' AND (pin_json #>> '{target}')=$4))) OR id IN (SELECT value FROM jsonb_array_elements_text(COALESCE(NULLIF($5::jsonb,'null'::jsonb),'[]'::jsonb)))) ORDER BY id LIMIT 65`, service.Config.Workspace, chat, private, service.Config.Workspace, string(mustJSON(selection.PinnedGrants)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uses []MemoryUse
	total := 0
	for rows.Next() {
		entry, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		if slices.Contains(selection.ExcludedPins, entry.ID) {
			continue
		}
		if entry.Pinned == nil || entry.Pinned.validate() != nil {
			return nil, ErrStoreFormat
		}
		if entry.Scope.Project != "" && entry.Scope.Project != service.Config.Workspace {
			return nil, ErrMemoryDenied
		}
		if len(entry.Body) > 2048 {
			return nil, ErrContextLimit
		}
		total += len(entry.Body)
		if total > 8192 || len(uses) >= service.Config.Memory.MaxEntries {
			return nil, ErrContextLimit
		}
		uses = append(uses, memoryUse(entry))
	}
	return uses, rows.Err()
}

func (tasks *TaskService) PinnedMemory(ctx context.Context, chat string) ([]MemoryUse, error) {
	selection, err := tasks.SelectedMemory(ctx, chat)
	if err != nil {
		return nil, err
	}
	core := tasks.core.runCopy()
	return core.selectPinnedMemory(ctx, chat, selection)
}
func (tasks *TaskService) ExcludePinnedMemory(ctx context.Context, chat, id string) error {
	if !memoryID(id) {
		return ErrMemoryDenied
	}
	selection, err := tasks.SelectedMemory(ctx, chat)
	if err != nil {
		return err
	}
	selection.ExcludedPins = uniqueSorted(append(selection.ExcludedPins, id))
	return tasks.SelectMemory(ctx, chat, selection)
}
