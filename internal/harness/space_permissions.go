package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrPermissionDenied = errors.New("harness: operation is not allowed by the space and requester permissions")
var ErrPermissionRevoked = errors.New("harness: Run permission was reduced; start a new request with the current permission")

func permissionRank(mode PermissionMode) int {
	switch mode {
	case PermissionNone:
		return 0
	case PermissionAsk:
		return 1
	case PermissionWorkspace:
		return 2
	case PermissionFull:
		return 3
	default:
		return -1
	}
}

func lowerPermission(a, b PermissionMode) PermissionMode {
	if permissionRank(a) < 0 || permissionRank(b) < 0 {
		return PermissionNone
	}
	if permissionRank(a) < permissionRank(b) {
		return a
	}
	return b
}

func (store *Store) spacePermission(ctx context.Context, spaceID string) (PermissionSelection, error) {
	if _, err := store.GetSpace(ctx, spaceID); err != nil {
		return PermissionSelection{}, err
	}
	selection := PermissionSelection{SpaceID: spaceID, Source: "space", Mode: PermissionNone}
	var raw string
	err := store.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "space/permission/"+spaceID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return selection, nil
	}
	if err != nil {
		return PermissionSelection{}, err
	}
	if json.Unmarshal([]byte(raw), &selection) != nil || selection.validate() != nil ||
		selection.Source != "space" || selection.SpaceID != spaceID {
		return PermissionSelection{}, ErrStoreFormat
	}
	return selection, nil
}

func (tasks *TaskService) SpacePermission(ctx context.Context, spaceID string) (PermissionSelection, error) {
	if tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return PermissionSelection{}, ErrInvalidSpace
	}
	return tasks.core.Store.spacePermission(ctx, spaceID)
}

func (tasks *TaskService) SetSpacePermission(ctx context.Context, spaceID string, mode PermissionMode, confirmed bool) error {
	if err := mode.Validate(); err != nil {
		return err
	}
	previous, err := tasks.SpacePermission(ctx, spaceID)
	if err != nil {
		return err
	}
	if mode == PermissionFull && previous.Mode != PermissionFull && !confirmed {
		return ErrFullAccessConfirmation
	}
	previous.Mode = mode
	raw, err := json.Marshal(previous)
	if err != nil {
		return err
	}
	return tasks.persistSpacePermission(ctx, spaceID, raw)
}

func (tasks *TaskService) persistSpacePermission(ctx context.Context, spaceID string, raw []byte) error {
	before, err := tasks.SpacePermission(ctx, spaceID)
	if err != nil {
		return err
	}
	var next PermissionSelection
	if json.Unmarshal(raw, &next) != nil || next.validate() != nil || next.SpaceID != spaceID || next.Source != "space" {
		return ErrInvalidSpace
	}
	if _, err := tasks.core.Store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		"space/permission/"+spaceID, string(raw)); err != nil {
		return err
	}
	if permissionRank(next.Mode) < permissionRank(before.Mode) {
		return tasks.stopPermissionCommands(ctx, spaceID, "")
	}
	return nil
}

type PersonPermission struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name"`
	Mode        PermissionMode `json:"mode"`
	UpdatedAt   string         `json:"updated_at"`
}

func validPersonID(id string) bool {
	return len(id) > 0 && len(id) <= 512 && !strings.ContainsAny(id, "\x00\r\n") &&
		(strings.HasPrefix(id, "telegram:user:") || strings.HasPrefix(id, "lark:") || strings.HasPrefix(id, "qq:"))
}

func (tasks *TaskService) PersonPermission(ctx context.Context, id string) (PersonPermission, error) {
	if !validPersonID(id) {
		return PersonPermission{}, ErrPermissionDenied
	}
	result := PersonPermission{ID: id, Mode: PermissionNone}
	var raw string
	err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "person/"+id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return PersonPermission{}, err
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.ID != id || result.Mode.Validate() != nil {
		return PersonPermission{}, ErrStoreFormat
	}
	return result, nil
}

func (tasks *TaskService) SetPersonPermission(ctx context.Context, id, displayName string, mode PermissionMode, confirmed bool) error {
	if !validPersonID(id) || len([]rune(displayName)) > 128 || hasControl(displayName) {
		return ErrPermissionDenied
	}
	if err := mode.Validate(); err != nil {
		return err
	}
	previous, err := tasks.PersonPermission(ctx, id)
	if err != nil {
		return err
	}
	if mode == PermissionFull && previous.Mode != PermissionFull && !confirmed {
		return ErrFullAccessConfirmation
	}
	next := PersonPermission{ID: id, DisplayName: displayName, Mode: mode, UpdatedAt: nowText()}
	_, err = tasks.core.Store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "person/"+id, string(mustJSON(next)))
	if err == nil && permissionRank(mode) < permissionRank(previous.Mode) {
		return tasks.stopPermissionCommands(ctx, "", id)
	}
	return err
}

// resolveRequestPermission is only called with a platform-verified sender.
// Local control operations deliberately do not invent a platform identity.
func (service *Service) resolveRequestPermission(ctx context.Context, chat, personID, entry string) error {
	if err := service.resolvePermission(ctx, chat, "", false); err != nil {
		return err
	}
	person := PersonPermission{Mode: PermissionNone}
	if personID != "" {
		var err error
		person, err = NewTaskService(service).PersonPermission(ctx, personID)
		if err != nil {
			return err
		}
	}
	service.permission.PersonID, service.permission.Entry = personID, entry
	service.permission.PersonLimit = person.Mode
	service.permission.Mode = lowerPermission(service.permission.Mode, person.Mode)
	return nil
}

func (service *Service) checkCurrentPermission(ctx context.Context, run Run) error {
	selected := service.permission
	if selected == nil || selected.SpaceID == "" {
		return nil
	}
	if run.ConversationID != "" {
		chat, err := service.Store.GetConversation(ctx, run.ConversationID)
		if err != nil {
			return err
		}
		if chat.SpaceID != selected.SpaceID {
			return ErrPermissionRevoked
		}
	}
	space, err := service.Store.spacePermission(ctx, selected.SpaceID)
	if err != nil {
		return err
	}
	current := space.Mode
	if selected.PersonID != "" {
		person, err := NewTaskService(service).PersonPermission(ctx, selected.PersonID)
		if err != nil {
			return err
		}
		current = lowerPermission(current, person.Mode)
	}
	if permissionRank(current) < permissionRank(selected.Mode) {
		return ErrPermissionRevoked
	}
	return nil
}

func (tasks *TaskService) stopPermissionCommands(ctx context.Context, spaceID, personID string) error {
	if tasks.core.commands == nil {
		return nil
	}
	commands, err := tasks.Commands(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, command := range commands {
		if command.State != "running" && command.State != "stopping" {
			continue
		}
		run, err := tasks.core.Store.GetRun(ctx, command.RunID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		snapshot, err := decodeRunSnapshot(run.ConfigJSON)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		p := snapshot.Permission
		if p != nil && (spaceID != "" && p.SpaceID == spaceID || personID != "" && p.PersonID == personID) {
			_, err := tasks.core.commands.stop(ctx, command.ID, "permission_revoked")
			if err != nil {
				errs = append(errs, fmt.Errorf("command %s: %w", command.ID, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (service *Service) operationsDenied() bool {
	return service != nil && service.permission != nil && service.permission.Mode == PermissionNone
}

func (service *Service) externalContext() bool {
	return service != nil && service.permission != nil && service.permission.Entry != "" && service.permission.Entry != SpaceSourceLocal
}

func (service *Service) publicContext() bool {
	return service.operationsDenied() || service.externalContext()
}

func publicMessages(messages []Message) []Message {
	result := make([]Message, 0, len(messages))
	for _, item := range messages {
		if item.Role != "user" && item.Role != "assistant" {
			continue
		}
		if item.Content == "" {
			continue
		}
		item.ModelMessage = ModelMessage{Role: item.Role, Content: item.Content, Channel: item.Channel, Public: item.Public, Attachments: item.Attachments}
		result = append(result, item)
	}
	return result
}

type deniedTools struct{}

func (deniedTools) Definitions() []ToolDefinition { return nil }
func (deniedTools) Prepare(context.Context, ToolCall) (PreparedTool, error) {
	return PreparedTool{}, ErrPermissionDenied
}
