package harness

import (
	"context"
	"errors"
)

// CollaborationCommand is the bounded owner-API representation of one CLI
// collaboration action. Plans and edits remain ordinary typed values; no new
// persisted operation object is introduced.
type CollaborationCommand struct {
	Action   string             `json:"action"`
	Parent   string             `json:"parent,omitempty"`
	Child    string             `json:"child,omitempty"`
	Session  string             `json:"session,omitempty"`
	Request  string             `json:"request,omitempty"`
	Control  string             `json:"control,omitempty"`
	Plan     *CollaborationPlan `json:"plan,omitempty"`
	Spec     *ChildSpec         `json:"spec,omitempty"`
	Edit     *CollaborationEdit `json:"edit,omitempty"`
	Limits   *ParentBudget      `json:"limits,omitempty"`
	Digest   string             `json:"digest,omitempty"`
	Decision string             `json:"decision,omitempty"`
	Approved bool               `json:"approved,omitempty"`
}

// ExecuteCollaborationCommand is the shared owner-side adapter for CLI
// collaboration controls. It deliberately returns existing reports, runs and
// confirmations rather than adding a second collaboration persistence model.
func ExecuteCollaborationCommand(ctx context.Context, tasks *TaskService, command CollaborationCommand) (any, error) {
	if ctx == nil || tasks == nil {
		return nil, errors.New("harness: collaboration command requires a task service and context")
	}
	collab := NewCollaboration(tasks)
	switch command.Action {
	case "create":
		if command.Plan == nil {
			return nil, errors.New("collab create: plan is required")
		}
		id, err := collab.Create(ctx, command.Session, command.Request, *command.Plan)
		if err != nil {
			return nil, err
		}
		return map[string]any{"parent_run": id, "continued": false}, nil
	case "continue":
		return collab.Continue(ctx, command.Parent)
	case "inspect":
		return collab.Inspect(ctx, command.Parent)
	case "control":
		if err := collab.Control(ctx, command.Parent, command.Child, command.Control); err != nil {
			return nil, err
		}
	case "adjust":
		if command.Spec == nil {
			return nil, errors.New("collab adjust: spec is required")
		}
		if err := collab.Adjust(ctx, command.Parent, command.Child, *command.Spec); err != nil {
			return nil, err
		}
	case "revise":
		if command.Edit == nil {
			return nil, errors.New("collab revise: edit is required")
		}
		if err := collab.Revise(ctx, command.Parent, *command.Edit); err != nil {
			return nil, err
		}
	case "revisions":
		return collab.Revisions(ctx, command.Parent)
	case "increase-limits":
		if command.Limits == nil {
			return nil, errors.New("collab increase-limits: limits are required")
		}
		if err := collab.IncreaseLimits(ctx, command.Parent, *command.Limits); err != nil {
			return nil, err
		}
	case "resolve":
		if err := collab.Resolve(ctx, command.Parent, command.Digest, command.Decision); err != nil {
			return nil, err
		}
	case "preview":
		return collab.ChildPreview(ctx, command.Parent, command.Child)
	case "confirm":
		preview, err := collab.ChildPreview(ctx, command.Parent, command.Child)
		if err != nil {
			return nil, err
		}
		if preview.Digest != command.Digest {
			return nil, ErrApprovalChanged
		}
		return collab.ChildConfirm(ctx, command.Parent, command.Child, preview, command.Approved)
	default:
		return nil, errors.New("harness: unknown collaboration command")
	}
	return map[string]any{"ok": true, "parent": command.Parent, "child": command.Child, "action": command.Action}, nil
}
