package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func publicCoreFixture(t *testing.T, model ModelClient) (*TaskService, string, string) {
	t.Helper()
	tasks, chat, _, work := attachmentFixture(t, func(c *Config) {
		c.PublicWeb = DefaultPublicWebConfig()
		c.PublicWeb.Enabled = true
		c.PublicWeb.Origin = "https://agent.example.test"
	}, model)
	return tasks, chat, work
}
func TestPublicCeilingClampsNewRunAndCannotWidenDuringLocalContinuation(t *testing.T) {
	calls := 0
	tasks, chat, work := publicCoreFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if !controlOnlyDefinitions(request.Tools) || !request.DenyOperations {
			t.Error("None remote ceiling advertised execution tools")
		}
		return ModelResponse{Content: "done"}, nil
	}))
	if err := tasks.SetSpacePermission(t.Context(), LocalSpaceID, PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	c := tasks.Config()
	c.PublicWeb.MaxPermission = PermissionNone
	tasks.core.setConfig(c)
	ctx := WithPublicEntry(t.Context(), "public:first", "192.0.2.1")
	result, err := tasks.Submit(ctx, chat, "one", "do work")
	if err != nil || result.Run.Status != "completed" || calls != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if result.Run.Permission == nil || result.Run.Permission.Mode != PermissionNone || result.Run.Permission.RemoteLimit != PermissionNone {
		t.Fatal(result.Run.Permission)
	}
	if _, err := os.Stat(filepath.Join(work, "unexpected")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	// A local task's immutable Full grant cannot be continued through a lower
	// remote entrance by presenting the same Run id.
	local := tasks.core.runCopy()
	if err := local.resolvePermission(t.Context(), chat, "", false); err != nil {
		t.Fatal(err)
	}
	local.remoteEntry = &PublicEntry{Principal: "public:first", IP: "192.0.2.1"}
	if err := local.checkCurrentPermission(t.Context(), Run{ConversationID: chat}); !errors.Is(err, ErrPermissionRevoked) {
		t.Fatal("remote continuation widened a local grant", err)
	}
}

func TestPublicPolicyConfirmationRejectsRaiseSelfBlockAndOtherSession(t *testing.T) {
	tasks, chat, _ := publicCoreFixture(t, nil)
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithPublicAdministrator(t.Context(), "public:first", "192.0.2.1", "fixture-account", func(bool) (func(), error) {
		return func() {}, nil
	})
	next := tasks.Config().PublicWeb
	next.MaxPermission = PermissionAsk
	apply := func(ctx context.Context, operation ControlOperation) error {
		principal := InteractivePrincipal(ctx, "cli")
		cap, err := control.IssueInteractiveCapability(ctx, chat, principal)
		if err != nil {
			return err
		}
		confirmation, err := control.RenderConfirmation(ctx, operation.ID, chat, principal)
		if err != nil {
			return err
		}
		_, err = control.ConfirmControlChange(ctx, operation.ID, ControlAuthorization{Source: ControlUserDirect, Principal: principal, ConversationID: chat, Capability: cap, Code: confirmation.Code})
		return err
	}
	op, err := control.PlanControlChange(ctx, ControlPlanRequest{Kind: ControlSetPublicWeb, Arguments: mustJSON(next), Authorization: ControlUserDirect, ConversationID: chat})
	if err != nil {
		t.Fatal(err)
	}
	other := WithPublicEntry(t.Context(), "public:other", "192.0.2.1")
	if !strings.Contains(op.Preview, "max_permission: \"full\" -> \"ask\"") {
		t.Fatal("public policy preview hid the exact change", op.Preview)
	}
	if err := apply(other, op); !errors.Is(err, ErrControlUserInteractionRequired) {
		t.Fatal("other session accepted a bound proposal", err)
	}
	if err := apply(ctx, op); err != nil {
		t.Fatal("remote lowering failed", err)
	}
	if events, err := tasks.ReadPublicSecurityEvents(t.Context()); err != nil || len(events) == 0 || events[len(events)-1].Source != "192.0.2.1" {
		t.Fatal("policy change did not record authenticated source", events, err)
	}
	next.MaxPermission = PermissionFull
	op, err = control.PlanControlChange(ctx, ControlPlanRequest{Kind: ControlSetPublicWeb, Arguments: mustJSON(next), Authorization: ControlUserDirect, ConversationID: chat})
	if err != nil {
		t.Fatal(err)
	}
	if err := apply(ctx, op); err == nil {
		t.Fatal("remote ceiling raise accepted")
	}
	if got := tasks.Config().PublicWeb.MaxPermission; got != PermissionAsk {
		t.Fatal(got)
	}
	next.MaxPermission = PermissionAsk
	next.Security.AllowlistEnabled = true
	next.Security.Allowlist = []string{"192.0.2.99"}
	op, err = control.PlanControlChange(ctx, ControlPlanRequest{Kind: ControlSetPublicWeb, Arguments: mustJSON(next), Authorization: ControlUserDirect, ConversationID: chat})
	if err != nil {
		t.Fatal(err)
	}
	if err := apply(ctx, op); err == nil {
		t.Fatal("remote self-lock accepted")
	}
	if tasks.Config().PublicWeb.Security.AllowlistEnabled {
		t.Fatal("rejected self-lock changed persisted configuration")
	}
}

func TestPublicModelProposalCanFinishAndBeApprovedLocally(t *testing.T) {
	target := t.TempDir()
	calls := 0
	tasks, chat, _ := publicCoreFixture(t, modelFunc(func(_ context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{{ID: "workspace-handoff", Name: controlPlanTool, Arguments: mustJSON(map[string]any{
				"kind": ControlSetWorkspace, "arguments": map[string]string{"workspace": target},
			})}}}, nil
		}
		return ModelResponse{Content: "next message"}, nil
	}))
	ctx := WithPublicAdministrator(t.Context(), "public:fixture", "192.0.2.11", "fixture-account", func(bool) (func(), error) {
		return func() {}, nil
	})
	run, err := tasks.Submit(ctx, chat, "proposal", "propose workspace change")
	if err != nil || run.Run.Status != "completed" || run.Run.StopReason != detachedProposalReason || calls != 1 {
		t.Fatalf("proposal did not finish its Run: status=%s reason=%s calls=%d err=%v", run.Run.Status, run.Run.StopReason, calls, err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	operations, err := control.ListControlOperations(ctx)
	if err != nil || len(operations) == 0 {
		t.Fatal("proposal was not retained", err)
	}
	operation := operations[len(operations)-1]
	if !operation.Detached || !operation.LocalApproval || operation.Status != ControlPending || tasks.Config().Workspace == target {
		t.Fatal("proposal applied before local decision", operation.Status)
	}
	if _, err := tasks.Submit(ctx, chat, "next", "continue chatting"); err != nil {
		t.Fatal("waiting local proposal blocked the next user message", err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "cli")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(t.Context(), operation.ID, chat, "cli")
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ConfirmControlChange(t.Context(), operation.ID, controlAuth(capability, chat, "cli", confirmation.Code))
	if err != nil || !result.Applied || !samePath(tasks.Config().Workspace, target) || calls != 2 {
		t.Fatalf("original operation was not applied exactly once: status=%s calls=%d err=%v", result.Operation.Status, calls, err)
	}
}

func TestPublicToolConfirmationMACBindsOwnerSessionRunAndDigest(t *testing.T) {
	tasks, chat, _ := publicCoreFixture(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ToolCall{{ID: "create", Name: "file_create", Arguments: mustJSON(map[string]string{"path": "new.txt", "content": "result"})}}}, nil
	}))
	if err := tasks.SetSpacePermission(t.Context(), LocalSpaceID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	a := WithPublicEntry(t.Context(), "public:A", "192.0.2.1")
	result, err := tasks.Submit(a, chat, "approval", "create")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatalf("%+v %v", result, err)
	}
	decision, err := tasks.Preview(a, result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.core.validatePublicConfirmation(a, decision); err != nil {
		t.Fatal(err)
	}
	b := WithPublicEntry(t.Context(), "public:B", "192.0.2.1")
	if _, err := tasks.Confirm(b, result.Run.ID, decision, true); !errors.Is(err, ErrApprovalChanged) {
		t.Fatal("other session used approval", err)
	}
	decision.Digest = "modified"
	if _, err := tasks.Confirm(a, result.Run.ID, decision, true); !errors.Is(err, ErrApprovalChanged) {
		t.Fatal("modified approval accepted", err)
	}
	copy := tasks.core.runCopy()
	copy.approvalBinding = "new owner"
	if err := copy.validatePublicConfirmation(a, decision); !errors.Is(err, ErrApprovalChanged) {
		t.Fatal("owner restart retained confirmation", err)
	}
}
