package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const ControlBranchConversation = "conversation.branch"

type branchControlArgs struct {
	ConversationID string  `json:"conversation_id"`
	MessageID      int64   `json:"message_id"`
	Mode           string  `json:"mode"`
	Prompt         *string `json:"prompt,omitempty"`
}
type branchControlSource struct {
	Args   branchControlArgs
	Prefix sourcePrefix
}
type branchAllowedRunKey struct{}

func (service *ControlService) buildBranchControl(ctx context.Context, record *controlRecord) error {
	var args branchControlArgs
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	if args.ConversationID == "" {
		args.ConversationID = record.Operation.ConversationID
	}
	if args.ConversationID == "" || args.MessageID < 1 || (args.Mode != "rerun" && args.Mode != "continue") {
		return ErrRequestKey
	}
	if args.Prompt != nil && len(*args.Prompt) > service.core.currentConfig().MaxContextBytes {
		return ErrContextLimit
	}
	conversation, err := service.core.Store.GetConversation(ctx, args.ConversationID)
	if err != nil {
		return err
	}
	if conversation.Workspace != service.core.currentConfig().Workspace {
		return ErrConversationSpace
	}
	var raw string
	if err := service.core.Store.db.QueryRowContext(ctx, `SELECT body_json FROM fa.messages WHERE id=$1 AND conversation_id=$2`, args.MessageID, args.ConversationID).Scan(&raw); err != nil {
		return err
	}
	var message ModelMessage
	if json.Unmarshal([]byte(raw), &message) != nil {
		return ErrStoreFormat
	}
	if message.Deferred || args.Mode == "rerun" && message.Role != "user" || args.Mode == "continue" && (message.Role != "assistant" || len(message.ToolCalls) != 0) {
		return ErrModelBoundary
	}
	prefix, err := readSourcePrefix(ctx, service.core.Store.db, args.ConversationID, args.MessageID, sourcePrefix{})
	if err != nil {
		return err
	}
	resource := "conversation.branch:" + args.ConversationID
	record.Operation.ConversationID = args.ConversationID
	record.Operation.ResourceIDs = []string{resource}
	record.Before = map[string]json.RawMessage{resource: mustJSON(prefix)}
	record.After = map[string]json.RawMessage{resource: mustJSON(branchControlSource{args, prefix})}
	record.Operation.Preview = fmt.Sprintf("Create a separate conversation branch from message %d (%s). Retain original conversation and current files; do not execute the new branch automatically.", args.MessageID, args.Mode)
	return nil
}

func (service *ControlService) applyBranchControl(ctx context.Context, record *controlRecord) error {
	var source branchControlSource
	if json.Unmarshal(record.After["conversation.branch:"+record.Operation.ConversationID], &source) != nil {
		return ErrStoreFormat
	}
	prefix, err := readSourcePrefix(ctx, service.core.Store.db, source.Args.ConversationID, source.Args.MessageID, sourcePrefix{})
	if err != nil {
		return err
	}
	if prefix != source.Prefix {
		return ErrApprovalChanged
	}
	if record.Operation.AttemptID != "" {
		attempt, err := service.core.Store.GetAttempt(ctx, record.Operation.AttemptID)
		if err != nil {
			return err
		}
		ctx = context.WithValue(ctx, branchAllowedRunKey{}, attempt.RunID)
	}
	result, err := NewTaskService(service.core).BranchConversation(ctx, source.Args.ConversationID, BranchCommand{RequestKey: "control-" + record.Operation.ID, MessageID: source.Args.MessageID, Mode: source.Args.Mode, Prompt: source.Args.Prompt})
	if err != nil {
		return err
	}
	if result.Conversation.ID == "" {
		return errors.New("harness: branch result has no conversation")
	}
	return nil
}
