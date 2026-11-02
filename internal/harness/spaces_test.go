package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoneAndExternalContextExcludeLocalToolBodiesAndInstructions(t *testing.T) {
	service, _, work := testHarness(t, EchoModel{})
	if err := os.WriteFile(filepath.Join(work, "private.txt"), []byte("LOCAL_TOOL_SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	calls := 0
	service.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("read", "file_read", map[string]string{"path": "private.txt"})}}, nil
		}
		return ModelResponse{Content: "finished reading"}, nil
	})
	if result, err := tasks.Submit(t.Context(), chat, "read", "read this private file"); err != nil || result.Run.Status != "completed" {
		t.Fatalf("initial %s %v", result.Run.Status, err)
	}
	space, err := tasks.NewSpace(t.Context(), "External", SpaceSourceTelegram, "group", "telegram:bot:1:chat:2")
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.MoveConversation(t.Context(), chat, space.ID); err != nil {
		t.Fatal(err)
	}
	service.Config.SystemPrompt = "LOCAL_SYSTEM_SECRET"
	service.Config.instructionText = "LOCAL_INSTRUCTION_SECRET"
	var requests []ModelRequest
	service.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, r)
		if r.Purpose == "compaction" {
			return ModelResponse{Content: "public summary"}, nil
		}
		return ModelResponse{Content: "public reply"}, nil
	})
	for i := 0; i < 3; i++ {
		if result, err := tasks.Submit(t.Context(), chat, fmt.Sprint(i), strings.Repeat("public background ", 35)); err != nil || result.Run.Status != "completed" {
			t.Fatalf("None %s %v", result.Run.Status, err)
		}
	}
	if _, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	var compact bool
	for _, r := range requests {
		compact = compact || r.Purpose == "compaction"
		raw, _ := json.Marshal(r)
		for _, secret := range []string{"LOCAL_TOOL_SECRET", "LOCAL_SYSTEM_SECRET", "LOCAL_INSTRUCTION_SECRET"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("%s leaked to %s request", secret, r.Purpose)
			}
		}
		if !r.DenyOperations || len(r.Tools) != 0 {
			t.Fatal("None request regained operations")
		}
	}
	if !compact {
		t.Fatal("manual compaction was not exercised")
	}
}

func noneHarness(t *testing.T, model ModelClient) (*Service, *FileTools, string) {
	service, files, work := testHarness(t, model)
	if err := NewTaskService(service).SetSpacePermission(t.Context(), LocalSpaceID, PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	return service, files, work
}

func TestSpacesShareConversationIdentityAndSurviveReopen(t *testing.T) {
	service, _, work := noneHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	ctx := t.Context()
	spaces, err := tasks.Spaces(ctx)
	if err != nil || len(spaces) != 1 || spaces[0].ID != LocalSpaceID {
		t.Fatalf("spaces=%+v err=%v", spaces, err)
	}
	p, err := tasks.SpacePermission(ctx, LocalSpaceID)
	if err != nil || p.Mode != PermissionNone {
		t.Fatalf("permission=%+v %v", p, err)
	}
	space, err := tasks.NewSpace(ctx, "Project", SpaceSourceLocal, "", "")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := tasks.NewConversationInSpace(ctx, space.ID, "One")
	if err != nil || chat.SpaceID != space.ID || chat.Workspace != work {
		t.Fatalf("chat=%+v %v", chat, err)
	}
	other, err := tasks.NewConversationInSpace(ctx, space.ID, "Two")
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpaceDefaultConversation(ctx, space.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpaceActiveConversation(ctx, space.ID, chat.ID); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpaceDefaultConversation(ctx, LocalSpaceID, chat.ID); !errors.Is(err, ErrConversationSpace) {
		t.Fatalf("cross-space default=%v", err)
	}
	if err := tasks.MoveConversation(ctx, other.ID, LocalSpaceID); err != nil {
		t.Fatal(err)
	}
	updated, _ := tasks.Space(ctx, space.ID)
	if updated.DefaultConversationID != chat.ID || updated.ActiveConversationID != chat.ID {
		t.Fatalf("dangling pointers=%+v", updated)
	}
	if err := tasks.DeleteSpace(ctx, space.ID); err != nil {
		t.Fatal(err)
	}
	if err := tasks.DeleteSpace(ctx, LocalSpaceID); !errors.Is(err, ErrLocalSpace) {
		t.Fatalf("delete local=%v", err)
	}
	data := service.Store.DataDir()
	if err := service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.GetConversation(ctx, chat.ID)
	if err != nil || got.SpaceID != LocalSpaceID || got.Workspace != work {
		t.Fatalf("restored=%+v %v", got, err)
	}
}

func TestNoneDoesNotInheritGlobalFullAndRejectsForgedTools(t *testing.T) {
	var request ModelRequest
	service, _, work := noneHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		request = r
		return ModelResponse{ToolCalls: []ToolCall{makeCall("forged", "file_create", map[string]string{"path": "forged.txt", "content": "wrong"})}}, nil
	}))
	service.Config.Execution.PermissionMode = PermissionFull
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	result, err := tasks.Submit(t.Context(), chat, "none", "create a file")
	if err == nil || result.Run.Status != "unknown" {
		t.Fatalf("forged call status=%s %v", result.Run.Status, err)
	}
	if len(request.Tools) != 0 || !request.DenyOperations {
		t.Fatalf("request tools=%v deny=%t", request.Tools, request.DenyOperations)
	}
	if _, err := os.Stat(filepath.Join(work, "forged.txt")); !os.IsNotExist(err) {
		t.Fatalf("side effect=%v", err)
	}
	bound, e := service.forRun(result.Run)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := bound.runtimeTools(result.Run.ID, "").Prepare(t.Context(), makeCall("direct", "file_list", map[string]string{"path": "."})); !errors.Is(e, ErrPermissionDenied) {
		t.Fatalf("direct execution=%v", e)
	}
}

func TestNoneCanChatAndSpacePermissionAppliesToEveryConversation(t *testing.T) {
	service, _, _ := noneHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	first := conversation(t, service)
	second := conversation(t, service)
	result, err := tasks.Submit(t.Context(), first, "hello", "hello")
	if err != nil || result.Answer != "Echo: hello" {
		t.Fatalf("None chat=%+v %v", result, err)
	}
	if err := tasks.SelectPermission(t.Context(), first, PermissionWorkspace, false); err != nil {
		t.Fatal(err)
	}
	p, err := tasks.SelectedPermission(t.Context(), second)
	if err != nil || p.Mode != PermissionWorkspace || p.Source != "space" {
		t.Fatalf("shared grant=%+v %v", p, err)
	}
	if err := tasks.SelectPermission(t.Context(), first, PermissionFull, false); !errors.Is(err, ErrFullAccessConfirmation) {
		t.Fatalf("unconfirmed Full=%v", err)
	}
}

func TestSpaceAndPersonPermissionsAllCombinations(t *testing.T) {
	service, _, _ := noneHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	person := "telegram:user:12345"
	levels := []PermissionMode{PermissionNone, PermissionAsk, PermissionWorkspace, PermissionFull}
	for i, space := range levels {
		for j, p := range levels {
			if err := tasks.SetSpacePermission(t.Context(), LocalSpaceID, space, true); err != nil {
				t.Fatal(err)
			}
			if err := tasks.SetPersonPermission(t.Context(), person, "User", p, true); err != nil {
				t.Fatal(err)
			}
			bound := service.runCopy()
			if err := bound.resolveRequestPermission(t.Context(), chat, person, "telegram"); err != nil {
				t.Fatal(err)
			}
			want := levels[min(i, j)]
			if bound.permission.Mode != want || bound.permission.SpaceLimit != space || bound.permission.PersonLimit != p {
				t.Fatalf("%s/%s => %+v want=%s", space, p, bound.permission, want)
			}
		}
	}
}

func TestRevocationInvalidatesApprovalAndIncreaseDoesNotElevateOldRun(t *testing.T) {
	service, _, work := noneHarness(t, permissionCreateModel())
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	if err := tasks.SelectPermission(t.Context(), chat, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(t.Context(), chat, "pending", "create a file")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatalf("pending=%+v %v", result, err)
	}
	preview, err := tasks.Preview(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectPermission(t.Context(), chat, PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	bound, err := service.forRun(result.Run)
	if err != nil || bound.permission.Mode != PermissionAsk {
		t.Fatalf("old grant=%+v %v", bound, err)
	}
	if err := tasks.SelectPermission(t.Context(), chat, PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Confirm(t.Context(), result.Run.ID, preview, true); !errors.Is(err, ErrPermissionRevoked) {
		t.Fatalf("stale approval=%v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "permission.txt")); !os.IsNotExist(err) {
		t.Fatalf("revoked operation=%v", err)
	}
}

func TestSpaceMoveAndDeleteRefuseUnfinishedRuns(t *testing.T) {
	service, _, _ := noneHarness(t, permissionCreateModel())
	tasks := NewTaskService(service)
	space, err := tasks.NewSpace(t.Context(), "Pending", SpaceSourceLocal, "", "")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := tasks.NewConversationInSpace(t.Context(), space.ID, "Pending")
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpacePermission(t.Context(), space.ID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Submit(t.Context(), chat.ID, "pending", "create")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatalf("pending=%+v %v", result, err)
	}
	if err := tasks.MoveConversation(t.Context(), chat.ID, LocalSpaceID); !errors.Is(err, ErrActiveRun) {
		t.Fatalf("move=%v", err)
	}
	if err := tasks.DeleteSpace(t.Context(), space.ID); !errors.Is(err, ErrSpaceHasActiveRun) {
		t.Fatalf("delete=%v", err)
	}
}

func TestNoneResponsesDoesNotOfferNativeSearch(t *testing.T) {
	wire, err := responsesRequestFrom(ModelRequest{DenyOperations: true, Messages: []ModelMessage{{Role: "user", Content: "hello"}}, MaxOutputTokens: 100}, "fixture", false, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(wire)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	if tools, ok := decoded["tools"].([]any); ok && len(tools) != 0 {
		t.Fatalf("None native search: %s", raw)
	}
}

func TestAllPermissionPairsGateActualFileEffects(t *testing.T) {
	service, _, work := noneHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	levels := []PermissionMode{PermissionNone, PermissionAsk, PermissionWorkspace, PermissionFull}
	for i, space := range levels {
		for j, person := range levels {
			if err := tasks.SetSpacePermission(t.Context(), LocalSpaceID, space, true); err != nil {
				t.Fatal(err)
			}
			if err := tasks.SetPersonPermission(t.Context(), "telegram:user:90", "Member", person, true); err != nil {
				t.Fatal(err)
			}
			chat := conversation(t, service)
			bound := service.runCopy()
			path := fmt.Sprintf("effect-%d-%d.txt", i, j)
			calls := 0
			bound.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
				calls++
				if calls == 1 {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("write", "file_create", map[string]string{"path": path, "content": "actual"})}}, nil
				}
				return ModelResponse{Content: "done"}, nil
			})
			if err := bound.resolveRequestPermission(t.Context(), chat, "telegram:user:90", "telegram"); err != nil {
				t.Fatal(err)
			}
			result, err := bound.Run(t.Context(), chat, "pair", "write file")
			_, fileErr := os.Stat(filepath.Join(work, path))
			level := min(i, j)
			if level == 0 {
				if err == nil || !os.IsNotExist(fileErr) {
					t.Fatalf("%s/%s bypassed None: status=%s err=%v file=%v", space, person, result.Run.Status, err, fileErr)
				}
			} else if level == 1 {
				if err != nil || result.Run.Status != "waiting_confirmation" || !os.IsNotExist(fileErr) {
					t.Fatalf("%s/%s bypassed Ask: %s %v %v", space, person, result.Run.Status, err, fileErr)
				}
			} else if err != nil || result.Run.Status != "completed" || fileErr != nil {
				t.Fatalf("%s/%s failed enabled effect: %s %v %v", space, person, result.Run.Status, err, fileErr)
			}
		}
	}
}

func TestRevocationStopsOwnedServiceThroughControlEvenWhileHostIsActive(t *testing.T) {
	tasks, chat, work := serviceFixture(t, 30)
	_, initial := startService(t, tasks, chat, "revoke.addr", "", 20, "SERVICE_READY", 3)
	_ = waitReady(t, tasks, initial.ID)
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ApplyDirectControlChange(t.Context(), ControlSetSpacePermission,
		SpaceControlArguments{SpaceID: LocalSpaceID, Mode: PermissionNone}, "", "user")
	if err != nil || !result.Applied {
		t.Fatalf("revocation blocked by service: %+v %v", result, err)
	}
	final := finishService(t, tasks, initial.ID)
	if final.State != "stopped" || final.Reason != "permission_revoked" || final.Receipt.Cleanup != "confirmed" {
		t.Fatalf("termination evidence=%+v", final)
	}
	if _, err := os.Stat(filepath.Join(work, "revoke.addr")); err != nil {
		t.Fatalf("prior side effect was not retained: %v", err)
	}
	if _, err := tasks.StartCommand(t.Context(), chat, "revoke-new", helperCall(t, "ok").Arguments); err == nil {
		last, _ := tasks.Latest(t.Context(), chat)
		if last.Status == "completed" {
			t.Fatal("new command bypassed None")
		}
	}
}

func TestExternalSingleConversationRoutingIsExplicitAndLocalReferencesAreCleared(t *testing.T) {
	service, _, _ := noneHarness(t, EchoModel{})
	store := service.Store
	space, _, err := store.EnsureExternalSpace(t.Context(), "Group", SpaceSourceTelegram, "group", "telegram:bot:42:chat:90")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateConversationInSpace(t.Context(), space.ID, "First", service.Config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateConversationInSpace(t.Context(), space.ID, "Second", service.Config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSpaceActiveConversation(t.Context(), space.ID, second.ID); err == nil {
		t.Fatal("multiple conversations enabled implicitly")
	}
	if err := store.SetSpaceMultiConversation(t.Context(), space.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSpaceActiveConversation(t.Context(), space.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSpaceMultiConversation(t.Context(), space.ID, false); err != nil {
		t.Fatal(err)
	}
	updated, _ := store.GetSpace(t.Context(), space.ID)
	if updated.ActiveConversationID != first.ID {
		t.Fatal("single mode did not return to default")
	}
	chat := conversation(t, service)
	for _, prefix := range []string{"chat/memory/", capabilityMetaPrefix, summaryMetaPrefix} {
		if _, err := store.db.Exec(`INSERT INTO meta(key,value) VALUES(?,?)`, prefix+chat, `{"private":"local-only"}`); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MoveConversation(t.Context(), chat, space.ID); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"chat/memory/", capabilityMetaPrefix, summaryMetaPrefix} {
		var count int
		if err := store.db.QueryRow(`SELECT count(*) FROM meta WHERE key=?`, prefix+chat).Scan(&count); err != nil || count != 0 {
			t.Fatalf("local input reference retained: %s %v", prefix, err)
		}
	}
}
