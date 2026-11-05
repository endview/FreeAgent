package harness

import (
	"context"
	"encoding/json"
	"fmt"
)

const ControlSetMemoryPin = "memory.set_pin"

type memoryPinArguments struct {
	ConversationID   string      `json:"conversation_id"`
	Scope            MemoryScope `json:"scope"`
	EntryID          string      `json:"entry_id"`
	ExpectedRevision int         `json:"expected_revision"`
	Pin              *MemoryPin  `json:"pin"` // null explicitly removes pinning
}

func (service *ControlService) buildMemoryPin(ctx context.Context, record *controlRecord) error {
	var args memoryPinArguments
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	if args.ConversationID == "" {
		args.ConversationID = record.Operation.ConversationID
	}
	entry, err := NewTaskService(service.core).ReadMemory(ctx, args.ConversationID, args.Scope, args.EntryID)
	if err != nil {
		return err
	}
	if entry.Revision != args.ExpectedRevision {
		return ErrMemoryConflict
	}
	if err := validatePinTarget(ctx, service.core.Store.db, args.Pin, service.core.Config.Workspace); err != nil {
		return err
	}
	if args.Pin != nil && (args.Pin.validate() != nil || entry.Scope.TaskID != "" || entry.State != "active" || len(entry.Body) > 2048) {
		return ErrMemoryDenied
	}
	resource := "memory.pin:" + entry.ID
	record.Operation.ResourceIDs = []string{resource}
	record.Operation.RevertClass = "none"
	record.Before = map[string]json.RawMessage{resource: mustJSON(entry)}
	record.After = map[string]json.RawMessage{resource: mustJSON(args)}
	record.Arguments = mustJSON(args)
	record.Operation.Preview = fmt.Sprintf("Change pinning for Memory %q, revision %d. Scope: %s. Content:\n%s\nThis does not grant tools or change prior requests.", entry.Title, entry.Revision, string(mustJSON(args.Pin)), entry.Body)
	return nil
}
func (service *ControlService) applyMemoryPin(ctx context.Context, record *controlRecord) error {
	var args memoryPinArguments
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	tasks := NewTaskService(service.core)
	entry, err := tasks.ReadMemory(ctx, args.ConversationID, args.Scope, args.EntryID)
	if err != nil {
		return err
	}
	if string(mustJSON(entry)) != string(record.Before[record.Operation.ResourceIDs[0]]) {
		return ErrMemoryConflict
	}
	action := "pin"
	if args.Pin == nil {
		action = "unpin"
	}
	result, err := tasks.SaveMemory(ctx, args.ConversationID, MemoryMutation{RequestKey: "control-" + record.Operation.ID, Action: action, Scope: args.Scope, EntryID: args.EntryID, ExpectedRevision: args.ExpectedRevision, Pinned: args.Pin})
	if err == nil {
		record.ResultData = mustJSON(result)
	}
	return err
}
