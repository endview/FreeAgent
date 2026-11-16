package harness

import (
	"context"
	"encoding/json"
	"fmt"
)

const ControlManageConversations = "conversation.manage"

type managementControlArgs struct {
	Command   ManagementCommand `json:"command"`
	PreviewID string            `json:"preview_id,omitempty"`
	SelfRun   string            `json:"self_run,omitempty"`
	SelfStamp string            `json:"self_stamp,omitempty"`
}

func (service *ControlService) buildManagementControl(ctx context.Context, r *controlRecord, origin string) error {
	var args managementControlArgs
	if err := decodeControlJSON(r.Arguments, &args); err != nil {
		return err
	}
	if args.SelfRun != "" || args.SelfStamp != "" {
		return ErrPermissionDenied
	}
	if r.Operation.Authorization == ControlModelProposed && args.Command.Action == "purge" {
		return ErrPermissionDenied
	}
	tasks := NewTaskService(service.core)
	var p ManagementPreview
	if args.PreviewID != "" {
		var raw string
		if err := service.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, "conversation/management/"+args.PreviewID).Scan(&raw); err != nil {
			return err
		}
		if json.Unmarshal([]byte(raw), &p) != nil {
			return ErrStoreFormat
		}
		args.Command = p.Command
	} else {
		var err error
		p, err = tasks.PreviewConversationManagement(ctx, args.Command)
		if err != nil {
			return err
		}
		args.PreviewID = p.ID
	}
	if r.Operation.Authorization == ControlModelProposed {
		if args.Command.Action == "purge" {
			return ErrPermissionDenied
		}
		for _, id := range p.Command.IDs {
			if id == r.Operation.ConversationID && (args.Command.Action == "trash" || args.Command.Action == "move" || args.Command.Action == "delete_space") {
				if origin == "" {
					return ErrPermissionDenied
				}
				args.SelfRun = origin
				var err error
				args.SelfStamp, err = managementSelfStamp(ctx, service.core.Store.db, id, origin)
				if err != nil {
					return err
				}
			}
		}
	}
	resource := "conversation.management:" + p.ID
	r.Operation.ResourceIDs = []string{resource}
	r.Before = map[string]json.RawMessage{resource: mustJSON(map[string]any{"preview_id": p.ID})}
	r.After = map[string]json.RawMessage{resource: mustJSON(args)}
	r.Arguments = mustJSON(args)
	r.Operation.Preview = fmt.Sprintf("%s：%d 个会话。%s", args.Command.Action, len(p.Items), p.Warning)
	if args.SelfRun != "" {
		r.Operation.Preview += "\n确认后先结束提出建议的本轮，再整理当前会话；不会再调用模型或重新执行工具。"
	}
	for _, i := range p.Items {
		r.Operation.Preview += fmt.Sprintf("\n%s · %d 轮 · %d 字节附件 · %d 项未确认", i.Title, i.Runs, i.AttachmentBytes, i.Unresolved)
	}
	return nil
}
func (service *ControlService) applyManagementControl(ctx context.Context, r *controlRecord) error {
	var args managementControlArgs
	if json.Unmarshal(r.Arguments, &args) != nil {
		return ErrStoreFormat
	}
	if args.SelfRun != "" {
		stamp, err := managementSelfStamp(ctx, service.core.Store.db, r.Operation.ConversationID, args.SelfRun)
		if err != nil {
			return err
		}
		if stamp != args.SelfStamp {
			return ErrManagementStale
		}
		pending := deferredConversationManagement{Operation: r.Operation.ID, Preview: args.PreviewID, Run: args.SelfRun, Attempt: r.Operation.AttemptID, Conversation: r.Operation.ConversationID, Stamp: args.SelfStamp, State: "waiting_settlement"}
		_, err = service.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT DO NOTHING`, deferredManagementPrefix+args.SelfRun, string(mustJSON(pending)))
		r.ResultData = mustJSON(map[string]string{"state": "waiting_settlement", "preview_id": args.PreviewID})
		return err
	}
	result, err := NewTaskService(service.core).ApplyConversationManagement(ctx, args.PreviewID)
	if err != nil {
		return err
	}
	r.ResultData = mustJSON(result)
	return nil
}
