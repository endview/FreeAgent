package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const (
	ControlCreateSpace      = "space.create"
	ControlRenameSpace      = "space.rename"
	ControlDeleteSpace      = "space.delete"
	ControlMoveConversation = "space.move_conversation"
	ControlSpaceDefault     = "space.set_default"
	ControlSpaceActive      = "space.set_active"
	ControlSpaceMulti       = "space.set_multi"
	ControlPersonPermission = "person.set_permission"
)

type SpaceControlArguments struct {
	SpaceID        string               `json:"space_id,omitempty"`
	ConversationID string               `json:"conversation_id,omitempty"`
	Title          string               `json:"title,omitempty"`
	Mode           PermissionMode       `json:"mode,omitempty"`
	Selection      *PermissionSelection `json:"selection,omitempty"`
	Enabled        *bool                `json:"enabled,omitempty"`
	PersonID       string               `json:"person_id,omitempty"`
	DisplayName    string               `json:"display_name,omitempty"`
}

func isSpaceControl(kind string) bool {
	switch kind {
	case ControlCreateSpace, ControlRenameSpace, ControlDeleteSpace, ControlMoveConversation,
		ControlSpaceDefault, ControlSpaceActive, ControlSpaceMulti, ControlSetSpacePermission, ControlPersonPermission:
		return true
	}
	return false
}

// One existing journal resource serializes space organization and permission
// proposals. No new proposal store or execution subsystem is needed.
func (service *ControlService) spaceControlState(ctx context.Context, personID string) (json.RawMessage, error) {
	tasks := NewTaskService(service.core)
	spaces, err := tasks.Spaces(ctx)
	if err != nil {
		return nil, err
	}
	chats, err := tasks.Conversations(ctx)
	if err != nil {
		return nil, err
	}
	for i := range chats {
		chats[i].UpdatedAt = ""
		chats[i].CreatedAt = ""
		chats[i].ModelID = ""
		chats[i].Pinned = false
		chats[i].PinnedAt = ""
	}
	sort.Slice(chats, func(i, j int) bool { return chats[i].ID < chats[j].ID })
	permissions := map[string]PermissionSelection{}
	for _, space := range spaces {
		p, err := tasks.SpacePermission(ctx, space.ID)
		if err != nil {
			return nil, err
		}
		permissions[space.ID] = p
	}
	var person *PersonPermission
	if personID != "" {
		p, err := tasks.PersonPermission(ctx, personID)
		if err != nil {
			return nil, err
		}
		person = &p
	}
	attachmentHash := sha256.New()
	rows, err := service.core.Store.db.QueryContext(ctx, `SELECT key,value FROM meta WHERE key LIKE 'attachment/%' ORDER BY key`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return nil, err
		}
		attachmentHash.Write([]byte(k))
		attachmentHash.Write([]byte(v))
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Spaces        []Space                        `json:"spaces"`
		Conversations []Conversation                 `json:"conversations"`
		Permissions   map[string]PermissionSelection `json:"permissions"`
		Person        *PersonPermission              `json:"person,omitempty"`
		Attachments   string                         `json:"attachments_revision"`
	}{spaces, chats, permissions, person, hex.EncodeToString(attachmentHash.Sum(nil))})
}

func (service *ControlService) buildSpaceControl(ctx context.Context, record *controlRecord) error {
	var args SpaceControlArguments
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	tasks := NewTaskService(service.core)
	if args.SpaceID == "" && args.ConversationID != "" {
		chat, err := service.core.Store.GetConversation(ctx, args.ConversationID)
		if err != nil {
			return err
		}
		args.SpaceID = chat.SpaceID
	}
	var space Space
	if record.Operation.Kind != ControlCreateSpace && record.Operation.Kind != ControlPersonPermission {
		var err error
		space, err = tasks.Space(ctx, args.SpaceID)
		if err != nil {
			return err
		}
	}
	switch record.Operation.Kind {
	case ControlCreateSpace:
		if err := validateSpaceTitle(args.Title); err != nil {
			return err
		}
		record.Operation.Preview = fmt.Sprintf("Create local space %q with None permission.", args.Title)
	case ControlRenameSpace:
		if err := validateSpaceTitle(args.Title); err != nil {
			return err
		}
		record.Operation.Preview = fmt.Sprintf("Rename space %q to %q.", space.Title, args.Title)
	case ControlDeleteSpace:
		if space.ID == LocalSpaceID {
			return ErrLocalSpace
		}
		record.Operation.Preview = fmt.Sprintf("Delete space %q. Its conversations return to Local with Local's permission. External access is revoked and the binding removed; reauthorizing does not publish this old history.", space.Title)
	case ControlMoveConversation:
		chat, err := service.core.Store.GetConversation(ctx, args.ConversationID)
		if err != nil {
			return err
		}
		record.Operation.Preview = fmt.Sprintf("Move %q to space %q (%s). Workspace stays the same; future requests use the target permission. External destinations expose the conversation's public text to that chat.", chat.Title, space.Title, space.Source)
		refs, err := tasks.Attachments(ctx, chat.ID)
		if err != nil {
			return err
		}
		if len(refs) > 0 {
			record.Operation.Preview += fmt.Sprintf("\nThis also moves %d attachment references; attachments in published messages become visible to the external destination.", len(refs))
			for i, info := range refs {
				if i >= 16 {
					record.Operation.Preview += "\nAdditional references are bound by the attachment revision."
					break
				}
				record.Operation.Preview += fmt.Sprintf("\n%s %s %s", info.Ref.ID, info.Ref.Name, info.Ref.SHA256)
			}
		}
	case ControlSetSpacePermission:
		previous, err := tasks.SpacePermission(ctx, args.SpaceID)
		if err != nil {
			return err
		}
		next := previous
		if args.Selection != nil {
			next = *args.Selection
		} else {
			next.Mode = args.Mode
		}
		next, err = prepareExecutionProfileSelection(ctx, tasks.DataDir(), tasks.Config(), next)
		if err != nil {
			return err
		}
		next.SpaceID = args.SpaceID
		args.Selection = &next
		record.Operation.Preview = fmt.Sprintf("Change space %q from %s to %s for ALL its conversations. Existing Runs never gain authority; reductions stop new operations and request command termination.", space.Title, previous.Mode, next.Mode)
	case ControlPersonPermission:
		if !validPersonID(args.PersonID) || args.Mode.Validate() != nil || len([]rune(args.DisplayName)) > 128 || hasControl(args.DisplayName) {
			return ErrPermissionDenied
		}
		record.Operation.Preview = fmt.Sprintf("Set %s to %s across connected spaces. Each space remains an independent upper bound.", args.PersonID, args.Mode)
	case ControlSpaceDefault, ControlSpaceActive:
		chat, err := service.core.Store.GetConversation(ctx, args.ConversationID)
		if err != nil {
			return err
		}
		if chat.SpaceID != space.ID {
			return ErrConversationSpace
		}
		record.Operation.Preview = fmt.Sprintf("%s: choose %q in space %q.", record.Operation.Kind, chat.Title, space.Title)
	case ControlSpaceMulti:
		if args.Enabled == nil {
			return errors.New("harness: enabled is required")
		}
		record.Operation.Preview = fmt.Sprintf("Set multiple conversations in %q to %t. Disabling returns external routing to its default.", space.Title, *args.Enabled)
	}
	before, err := service.spaceControlState(ctx, args.PersonID)
	if err != nil {
		return err
	}
	record.Operation.ResourceIDs = []string{"spaces"}
	record.Before = map[string]json.RawMessage{"spaces": before}
	record.After = map[string]json.RawMessage{"spaces": mustJSON(args)}
	record.Arguments = mustJSON(args)
	return nil
}

func spacePermissionReduction(record controlRecord) bool {
	if record.Operation.Kind != ControlSetSpacePermission && record.Operation.Kind != ControlPersonPermission {
		return false
	}
	var args SpaceControlArguments
	var before struct {
		Permissions map[string]PermissionSelection `json:"permissions"`
		Person      *PersonPermission              `json:"person"`
	}
	if json.Unmarshal(record.Arguments, &args) != nil || json.Unmarshal(record.Before["spaces"], &before) != nil {
		return false
	}
	if record.Operation.Kind == ControlPersonPermission {
		return before.Person != nil && permissionRank(args.Mode) < permissionRank(before.Person.Mode)
	}
	return args.Selection != nil && permissionRank(args.Selection.Mode) < permissionRank(before.Permissions[args.SpaceID].Mode)
}

func (service *ControlService) applySpaceControl(ctx context.Context, record *controlRecord) error {
	var args SpaceControlArguments
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	current, err := service.spaceControlState(ctx, args.PersonID)
	if err != nil {
		return err
	}
	if string(current) != string(record.Before["spaces"]) {
		return ErrControlStaleRevision
	}
	tasks := NewTaskService(service.core)
	switch record.Operation.Kind {
	case ControlCreateSpace:
		space, err := tasks.NewSpace(ctx, args.Title, SpaceSourceLocal, "", "")
		if err == nil {
			record.ResultData = mustJSON(space)
		}
		return err
	case ControlRenameSpace:
		return tasks.RenameSpace(ctx, args.SpaceID, args.Title)
	case ControlDeleteSpace:
		return tasks.DeleteSpace(ctx, args.SpaceID)
	case ControlMoveConversation:
		return tasks.MoveConversation(ctx, args.ConversationID, args.SpaceID)
	case ControlSpaceDefault:
		return tasks.SetSpaceDefaultConversation(ctx, args.SpaceID, args.ConversationID)
	case ControlSpaceActive:
		return tasks.SetSpaceActiveConversation(ctx, args.SpaceID, args.ConversationID)
	case ControlSpaceMulti:
		return tasks.SetSpaceMultiConversation(ctx, args.SpaceID, *args.Enabled)
	case ControlPersonPermission:
		return tasks.SetPersonPermission(ctx, args.PersonID, args.DisplayName, args.Mode, true)
	case ControlSetSpacePermission:
		return tasks.persistSpacePermission(ctx, args.SpaceID, mustJSON(args.Selection))
	}
	return ErrInvalidSpace
}
