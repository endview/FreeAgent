package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func controlAuth(capability InteractiveControlCapability, conversationID, principal, code string) ControlAuthorization {
	return ControlAuthorization{Source: ControlModelProposed, ConversationID: conversationID,
		Principal: principal, Code: code, Capability: capability}
}

func TestControlPlanConfirmUsesShortOwnerCodeAndAppliesToFutureRuns(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	conversationID := conversation(t, service)
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := control.PlanControlChange(ctx, ControlPlanRequest{
		Kind: ControlSetPermission, Arguments: mustJSON(map[string]any{"mode": PermissionFull}),
		Authorization: ControlModelProposed, ConversationID: conversationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.Status != ControlPending || operation.Risk != ControlRiskAuthority || operation.Digest == "" {
		t.Fatalf("operation=%+v", operation)
	}
	if operation.OldValues[operation.ResourceIDs[0]] != `"ask"` {
		t.Fatalf("old value=%+v", operation.OldValues)
	}
	if operation.ProposedValues[operation.ResourceIDs[0]] != `"full"` {
		t.Fatalf("proposed value=%+v", operation.ProposedValues)
	}

	capability, err := control.IssueInteractiveCapability(ctx, conversationID, "local-user")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(ctx, operation.ID, conversationID, "local-user")
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmation.Code) != controlCodeLength || confirmation.Code == operation.Digest || confirmation.Code == operation.ProposedValues[operation.ResourceIDs[0]] {
		t.Fatalf("confirmation=%+v operation=%+v", confirmation, operation)
	}
	if _, err := control.ConfirmControlChange(ctx, operation.ID, controlAuth(capability, conversationID, "local-user", "0000000000")); !errors.Is(err, ErrControlWrongCode) {
		t.Fatalf("wrong code error=%v", err)
	}
	result, err := control.ConfirmControlChange(ctx, operation.ID, controlAuth(capability, conversationID, "local-user", confirmation.Code))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || !result.NextRunRequired || result.Result != "applied_to_future_runs" {
		t.Fatalf("result=%+v", result)
	}
	if got := service.Config.Execution.PermissionMode; got != PermissionFull {
		t.Fatalf("permission=%q", got)
	}
	state, err := control.ReadControlState(ctx)
	if err != nil || state.Config.PermissionMode != string(PermissionFull) {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	for _, value := range []string{confirmation.Code, "local-user", operation.Digest} {
		if value != "" && containsControlSecret(state, value) {
			t.Fatalf("control state leaked %q: %+v", value, state)
		}
	}
}

func TestKeyControlStoresOnlyMetadataAndClearsSecretAfterApply(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	keys := NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	service.Keys = keys
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const secret = "FA-control-secret-0001"
	operation, err := control.PlanKeyProfileChange(ctx, KeyControlRequest{Input: KeyInput{
		Name: "controlled", BaseURL: "https://provider.example/v1", Protocol: KeyProtocolOpenAICompatible,
		Source: KeySourceSaved, Secret: []byte(secret),
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(service.Store.DataDir(), controlJournalName))
	if err != nil || strings.Contains(string(raw), secret) || strings.Contains(operation.Preview, secret) {
		t.Fatalf("secret leaked into control record: raw=%s preview=%q err=%v", raw, operation.Preview, err)
	}
	capability, err := control.IssueInteractiveCapability(ctx, "", "user")
	if err != nil {
		t.Fatal(err)
	}
	code, err := control.ControlCodeForTests(ctx, operation.ID, "", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, operation.ID, ControlAuthorization{Source: ControlUserDirect,
		Principal: "user", Code: code, Capability: capability}); err != nil {
		t.Fatal(err)
	}
	view, err := keys.List()
	if err != nil || len(view) != 1 || view[0].Name != "controlled" {
		t.Fatalf("key list=%+v err=%v", view, err)
	}
	resolved, err := keys.Resolve(view[0].ID)
	if err != nil || resolved.secret != secret {
		t.Fatalf("resolved key=%v err=%v", resolved, err)
	}
	remove, err := control.PlanKeyProfileChange(ctx, KeyControlRequest{Ref: view[0].ID, Remove: true})
	if err != nil {
		t.Fatal(err)
	}
	capability, err = control.IssueInteractiveCapability(ctx, "", "user")
	if err != nil {
		t.Fatal(err)
	}
	code, err = control.ControlCodeForTests(ctx, remove.ID, "", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, remove.ID, ControlAuthorization{Source: ControlUserDirect,
		Principal: "user", Code: code, Capability: capability}); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Find(view[0].ID); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("removed key lookup=%v", err)
	}
}

func TestKeyProbeUsesTypedControlOperation(t *testing.T) {
	const secret = "FA-probe-control-secret-0001"
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" || request.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"id":"probe-a"},{"id":"probe-b"}]}`))
	}))
	defer provider.Close()
	service, _, _ := testHarness(t, EchoModel{})
	keys := NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	service.Keys = keys
	profile, err := keys.Add(KeyInput{Name: "probe", BaseURL: provider.URL + "/v1", Source: KeySourceSaved, Secret: []byte(secret)})
	if err != nil {
		t.Fatal(err)
	}
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ProbeKey(t.Context(), profile.ID, "test-user")
	if err != nil || result.Category != "ok" || len(result.Models) != 2 {
		t.Fatalf("probe=%+v err=%v", result, err)
	}
	view, err := keys.Find(profile.ID)
	if err != nil || view.LastCheck == nil || view.LastCheck.Result != "ok" {
		t.Fatalf("check=%+v err=%v", view, err)
	}
	operations, err := control.ListControlOperations(t.Context())
	if err != nil || len(operations) == 0 || operations[len(operations)-1].Kind != ControlProbeKey || operations[len(operations)-1].Status != ControlApplied {
		t.Fatalf("operations=%+v err=%v", operations, err)
	}
}

func TestMCPProfileControlJournalsOnlySafeMetadata(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	secret := "MCP-ENV-SECRET-SENTINEL"
	service.Config.MCPServers = map[string]MCPServerConfig{"fixture": {
		Enabled: false, Command: os.Args[0], Env: map[string]string{"TOKEN": secret},
	}}
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := control.PlanMCPProfileChange(t.Context(), MCPControlRequest{ID: "fixture", Config: service.Config.MCPServers["fixture"]})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(service.Store.DataDir(), controlJournalName))
	if err != nil || strings.Contains(string(raw), secret) {
		t.Fatalf("MCP secret leaked into journal=%s err=%v", raw, err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), "", "mcp-user")
	if err != nil {
		t.Fatal(err)
	}
	code, err := control.ControlCodeForTests(t.Context(), operation.ID, "", "mcp-user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(t.Context(), operation.ID, ControlAuthorization{Source: ControlUserDirect,
		Principal: "mcp-user", Code: code, Capability: capability}); err != nil {
		t.Fatal(err)
	}
	if got := service.Config.MCPServers["fixture"].Env["TOKEN"]; got != secret {
		t.Fatalf("MCP environment was not applied: %q", got)
	}
}

func TestMCPProfileControlRequiresReentryAfterOwnerRestart(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	secret := "MCP-REENTRY-SECRET-SENTINEL"
	profile := MCPServerConfig{Enabled: false, Command: os.Args[0], Env: map[string]string{"TOKEN": secret}}
	service.Config.MCPServers = map[string]MCPServerConfig{"restart-fixture": profile}
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := control.PlanMCPProfileChange(t.Context(), MCPControlRequest{ID: "restart-fixture", Config: profile})
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the owner-side control service from its journal. The full MCP
	// payload was intentionally not persisted, so the pending operation must
	// remain pending and require explicit re-entry.
	service.control = nil
	service.controlErr = nil
	restarted, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	capability, err := restarted.IssueInteractiveCapability(t.Context(), "", "mcp-user")
	if err != nil {
		t.Fatal(err)
	}
	code, err := restarted.ControlCodeForTests(t.Context(), operation.ID, "", "mcp-user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ConfirmControlChange(t.Context(), operation.ID, ControlAuthorization{Source: ControlUserDirect,
		Principal: "mcp-user", Code: code, Capability: capability}); !errors.Is(err, ErrControlMCPReentryRequired) {
		t.Fatalf("confirm without re-entry error=%v", err)
	}
	operations, err := restarted.ListControlOperations(t.Context())
	if err != nil || len(operations) == 0 || operations[len(operations)-1].Status != ControlPending {
		t.Fatalf("pending operation was not retained: %+v err=%v", operations, err)
	}
	if _, err := restarted.ReenterMCPProfile(t.Context(), operation.ID, MCPControlRequest{ID: "restart-fixture", Config: profile}); err != nil {
		t.Fatal(err)
	}
	code, err = restarted.ControlCodeForTests(t.Context(), operation.ID, "", "mcp-user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ConfirmControlChange(t.Context(), operation.ID, ControlAuthorization{Source: ControlUserDirect,
		Principal: "mcp-user", Code: code, Capability: capability}); err != nil {
		t.Fatal(err)
	}
	if got := service.Config.MCPServers["restart-fixture"].Env["TOKEN"]; got != secret {
		t.Fatalf("reentered MCP profile was not applied: %q", got)
	}
}

func TestControlRejectsStaleRevisionAndExactRevert(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	conversationID := conversation(t, service)
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	beforeMode := service.Config.Execution.PermissionMode
	first, err := control.PlanControlChange(ctx, ControlPlanRequest{Kind: ControlSetPermission,
		Arguments: mustJSON(map[string]any{"mode": PermissionFull}), Authorization: ControlModelProposed, ConversationID: conversationID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := control.PlanControlChange(ctx, ControlPlanRequest{Kind: ControlSetPermission,
		Arguments: mustJSON(map[string]any{"mode": PermissionWorkspace}), Authorization: ControlModelProposed, ConversationID: conversationID})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(ctx, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	firstCode, err := control.ControlCodeForTests(ctx, first.ID, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, first.ID, controlAuth(capability, conversationID, "user", firstCode)); err != nil {
		t.Fatal(err)
	}
	secondCode, err := control.ControlCodeForTests(ctx, second.ID, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, second.ID, controlAuth(capability, conversationID, "user", secondCode)); !errors.Is(err, ErrControlStaleRevision) {
		t.Fatalf("stale error=%v", err)
	}
	if service.Config.Execution.PermissionMode != PermissionFull {
		t.Fatalf("stale operation changed config: %+v", service.Config.Execution)
	}

	revert, err := control.PlanRevertControlChange(ctx, first.ID, ControlModelProposed, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	revertCode, err := control.ControlCodeForTests(ctx, revert.ID, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, revert.ID, controlAuth(capability, conversationID, "user", revertCode)); err != nil {
		t.Fatal(err)
	}
	if service.Config.Execution.PermissionMode != beforeMode {
		t.Fatalf("revert did not restore previous mode: %+v", service.Config.Execution)
	}
}

func TestControlPendingOperationSurvivesOwnerRestartWithNewCodeEpoch(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	data := filepath.Join(base, "state")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := InitStore(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	config := testEchoConfig(workspace)
	if err := WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(context.Background(), "test", workspace)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store, Config: config}
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := control.PlanControlChange(context.Background(), ControlPlanRequest{Kind: ControlRenameConversation,
		Arguments: mustJSON(map[string]any{"title": "after restart"}), Authorization: ControlModelProposed, ConversationID: conversation.ID})
	if err != nil {
		t.Fatal(err)
	}
	oldCode, err := control.ControlCodeForTests(context.Background(), operation.ID, conversation.ID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStore(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := LoadConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	newService := &Service{Store: reopened, Config: loaded}
	newControl, err := NewControlService(NewTaskService(newService))
	if err != nil {
		t.Fatal(err)
	}
	operations, err := newControl.ListControlOperations(context.Background())
	if err != nil || len(operations) != 1 || operations[0].Status != ControlPending {
		t.Fatalf("operations=%+v err=%v", operations, err)
	}
	newCode, err := newControl.ControlCodeForTests(context.Background(), operation.ID, conversation.ID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if newCode == oldCode {
		t.Fatal("owner restart reused the old in-memory confirmation code")
	}
	capability, err := newControl.IssueInteractiveCapability(context.Background(), conversation.ID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newControl.ConfirmControlChange(context.Background(), operation.ID, controlAuth(capability, conversation.ID, "user", newCode)); err != nil {
		t.Fatal(err)
	}
	updated, err := reopened.GetConversation(context.Background(), conversation.ID)
	if err != nil || updated.Title != "after restart" {
		t.Fatalf("conversation=%+v err=%v", updated, err)
	}
}

func containsControlSecret(state ControlState, value string) bool {
	raw, _ := json.Marshal(state)
	return bytes.Contains(raw, []byte(value))
}

func TestModelControlProposalUsesWaitingConfirmationAndNextRunResult(t *testing.T) {
	var calls int
	service, _, _ := testHarness(t, modelFunc(func(ctx context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if calls != 1 {
			return ModelResponse{Content: "unexpected second model call"}, nil
		}
		found := false
		for _, definition := range request.Tools {
			if definition.Name == controlPlanTool {
				found = true
			}
		}
		if !found {
			return ModelResponse{}, errors.New("control proposal tool was not exposed")
		}
		response := ModelResponse{ToolCalls: []ToolCall{makeCall("control-1", controlPlanTool, map[string]any{
			"kind":      ControlSetPermission,
			"arguments": map[string]any{"mode": PermissionFull},
		})}}
		if validationErr := validateResponse(response); validationErr != nil {
			return ModelResponse{}, fmt.Errorf("validation: %w", validationErr)
		}
		if controlErr := controlResponseError(response, request.Tools); controlErr != nil {
			return ModelResponse{}, fmt.Errorf("control validation: %w", controlErr)
		}
		return response, nil
	}))
	service.controlToolsActive = true
	conversationID := conversation(t, service)
	result, err := service.Run(context.Background(), conversationID, "control-run", "enable full access")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		attempts, _ := service.Store.ListAttempts(context.Background(), result.Run.ID)
		t.Logf("result status=%s stop=%s err_type=%T attempts=%+v", result.Run.Status, result.Run.StopReason, err, attempts)
		t.Fatalf("control proposal did not wait for confirmation")
	}
	if calls != 1 {
		t.Fatalf("model calls=%d", calls)
	}
	tasks := NewTaskService(service)
	preview, err := tasks.Preview(context.Background(), result.Run.ID)
	if err != nil || preview.ControlOperationID == "" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(context.Background(), conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	code, err := control.ControlCodeForTests(context.Background(), preview.ControlOperationID, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := tasks.ConfirmControl(context.Background(), result.Run.ID, preview,
		controlAuth(capability, conversationID, "user", code), true)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Run.Status != "completed" || confirmed.Run.StopReason != "control_change_applied_next_run" {
		t.Fatalf("confirmed=%+v", confirmed)
	}
	if service.Config.Execution.PermissionMode != PermissionFull {
		t.Fatalf("permission=%q", service.Config.Execution.PermissionMode)
	}
	attempts, err := service.Store.ListAttempts(context.Background(), result.Run.ID)
	if err != nil || len(attempts) != 2 || attempts[1].Status != "finished" {
		t.Fatalf("attempts=%+v err=%v", attempts, err)
	}
}

func TestMixedControlAndToolResponseIsCertainFailureWithoutPendingTools(t *testing.T) {
	service, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ToolCall{
			makeCall("control-1", controlPlanTool, map[string]any{"kind": ControlSetPermission, "arguments": map[string]any{"mode": PermissionFull}}),
			makeCall("read-1", "file_list", map[string]any{"path": "."}),
		}}, nil
	}))
	service.controlToolsActive = true
	run, err := service.Run(context.Background(), conversation(t, service), "mixed-control", "change and list")
	if err == nil {
		t.Fatalf("mixed response unexpectedly succeeded: %+v", run)
	}
	if run.Run.Status != "failed" {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	var callErr *CallError
	if !errors.As(err, &callErr) || callErr.Code != "control_call_not_exclusive" || !callErr.Certain {
		t.Fatalf("error=%T %v", err, err)
	}
	attempts, listErr := service.Store.ListAttempts(context.Background(), run.Run.ID)
	if listErr != nil || len(attempts) != 1 || attempts[0].Kind != "model" {
		t.Fatalf("attempts=%+v err=%v", attempts, listErr)
	}
}

func TestExternalConfigChangeRequiresReviewBeforeControlOrRun(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	config := service.Config
	config.SystemPrompt = "external edit"
	if err := WriteConfig(service.Store.DataDir(), config); err != nil {
		t.Fatal(err)
	}
	if err := control.CheckExternalChanges(context.Background()); !errors.Is(err, ErrExternalControlChange) {
		t.Fatalf("external check=%v", err)
	}
	if _, err := control.PlanControlChange(context.Background(), ControlPlanRequest{Kind: ControlSetPermission,
		Arguments: mustJSON(map[string]any{"mode": PermissionWorkspace}), Authorization: ControlUserDirect}); !errors.Is(err, ErrExternalControlChange) {
		t.Fatalf("plan after external change=%v", err)
	}
	if err := control.ReviewExternalChange(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := control.PlanControlChange(context.Background(), ControlPlanRequest{Kind: ControlSetPermission,
		Arguments: mustJSON(map[string]any{"mode": PermissionWorkspace}), Authorization: ControlUserDirect}); err != nil {
		t.Fatal(err)
	}
}

func TestExtendedControlCatalogPlansAppliesAndRevertsConfigFamilies(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	ctx := context.Background()
	conversationID := conversation(t, service)
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	modelArgs := map[string]any{"id": "local-profile", "provider": "echo", "default": true}
	operations := []struct {
		kind  string
		args  any
		check func(Config) bool
	}{
		{ControlSetModelProfile, modelArgs, func(config Config) bool {
			return config.DefaultModel == "local-profile" && config.Models["local-profile"].Provider == "echo"
		}},
		{ControlSetAutoPolicy, map[string]any{"enabled": true, "models": []string{"local-profile"}, "max_candidates": 1, "max_assessments": 0, "max_upgrades": 0}, func(config Config) bool { return config.Auto.Enabled && len(config.Auto.Models) == 1 }},
		{ControlSetHTTPPolicy, map[string]any{"enabled": true, "allowed_origins": []string{"https://example.test"}, "timeout_seconds": 10, "max_output_bytes": 4096}, func(config Config) bool { return config.HTTP.Enabled && len(config.HTTP.AllowedOrigins) == 1 }},
		{ControlSetMemoryPolicy, map[string]any{"knowledge_enabled": true, "max_entries": 8, "max_context_bytes": 8192, "max_document_bytes": 16384}, func(config Config) bool { return config.Memory.KnowledgeEnabled && config.Memory.MaxEntries == 8 }},
		{ControlSetPromptPolicy, map[string]any{"system_prompt": "updated system prompt", "instructions": []string{}}, func(config Config) bool { return config.SystemPrompt == "updated system prompt" }},
		{ControlSetRuntimeLimits, map[string]any{"max_steps": 20, "max_context_bytes": 65536}, func(config Config) bool { return config.MaxSteps == 20 && config.MaxContextBytes == 65536 }},
		{ControlSetSkillPolicy, map[string]any{"id": "notes", "enabled": true, "trusted_workspace": workspace, "trusted": true}, func(config Config) bool {
			return config.Skills.Enabled["notes"] && config.Skills.TrustedWorkspaces[workspace]
		}},
	}
	var lastOperation ControlOperation
	for _, item := range operations {
		operation, planErr := control.PlanControlChange(ctx, ControlPlanRequest{Kind: item.kind, Arguments: mustJSON(item.args), Authorization: ControlUserDirect, ConversationID: conversationID})
		if planErr != nil {
			t.Fatalf("plan %s: %v", item.kind, planErr)
		}
		lastOperation = operation
		code, codeErr := control.ControlCodeForTests(ctx, operation.ID, conversationID, "user")
		if codeErr != nil {
			t.Fatal(codeErr)
		}
		capability, capErr := control.IssueInteractiveCapability(ctx, conversationID, "user")
		if capErr != nil {
			t.Fatal(capErr)
		}
		if _, applyErr := control.ConfirmControlChange(ctx, operation.ID, ControlAuthorization{Source: ControlUserDirect, ConversationID: conversationID, Principal: "user", Code: code, Capability: capability}); applyErr != nil {
			t.Fatalf("apply %s: %v", item.kind, applyErr)
		}
		if !item.check(service.Config) {
			t.Fatalf("config did not apply %s: %+v", item.kind, service.Config)
		}
	}
	revert, err := control.PlanRevertControlChange(ctx, lastOperation.ID, ControlUserDirect, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	revertCode, err := control.ControlCodeForTests(ctx, revert.ID, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(ctx, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, revert.ID, ControlAuthorization{Source: ControlUserDirect, ConversationID: conversationID, Principal: "user", Code: revertCode, Capability: capability}); err != nil {
		t.Fatal(err)
	}
	if service.Config.Skills.Enabled["notes"] {
		t.Fatal("skill policy revert did not restore exact prior state")
	}
}

func TestRuntimeLimitsRevertRestoresExactSnapshot(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	conversationID := conversation(t, service)
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	apply := func(kind string, args any) ControlOperation {
		t.Helper()
		operation, err := control.PlanControlChange(ctx, ControlPlanRequest{Kind: kind, Arguments: mustJSON(args),
			Authorization: ControlUserDirect, ConversationID: conversationID})
		if err != nil {
			t.Fatal(err)
		}
		code, err := control.ControlCodeForTests(ctx, operation.ID, conversationID, "user")
		if err != nil {
			t.Fatal(err)
		}
		capability, err := control.IssueInteractiveCapability(ctx, conversationID, "user")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := control.ConfirmControlChange(ctx, operation.ID, ControlAuthorization{Source: ControlUserDirect,
			ConversationID: conversationID, Principal: "user", Code: code, Capability: capability}); err != nil {
			t.Fatal(err)
		}
		return operation
	}
	apply(ControlSetRuntimeLimits, map[string]any{
		"max_steps": 20, "max_messages": 80, "max_context_bytes": 65536,
		"max_output_tokens": 2048, "max_run_seconds": 120, "max_tool_result_bytes": 32768,
		"task_summary_bytes": 4096,
	})
	before := service.Config
	changed := apply(ControlSetRuntimeLimits, map[string]any{"max_steps": 30})
	if service.Config.MaxSteps != 30 || service.Config.MaxMessages != before.MaxMessages ||
		service.Config.TaskSummaryBytes != before.TaskSummaryBytes {
		t.Fatalf("partial limits update changed the wrong fields: before=%+v after=%+v", before, service.Config)
	}
	revert, err := control.PlanRevertControlChange(ctx, changed.ID, ControlUserDirect, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := control.ControlCodeForTests(ctx, revert.ID, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(ctx, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, revert.ID, ControlAuthorization{Source: ControlUserDirect,
		ConversationID: conversationID, Principal: "user", Code: code, Capability: capability}); err != nil {
		t.Fatalf("exact limits revert failed: %v", err)
	}
	if service.Config.MaxSteps != before.MaxSteps || service.Config.MaxMessages != before.MaxMessages ||
		service.Config.MaxContextBytes != before.MaxContextBytes || service.Config.MaxOutputTokens != before.MaxOutputTokens ||
		service.Config.MaxRunSeconds != before.MaxRunSeconds || service.Config.MaxToolResult != before.MaxToolResult ||
		service.Config.TaskSummaryBytes != before.TaskSummaryBytes {
		t.Fatalf("limits revert did not restore exact snapshot: before=%+v after=%+v", before, service.Config)
	}
}

func TestConversationPermissionControlAppliesFullSelectionAndReverts(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	conversationID := conversation(t, service)
	root := t.TempDir()
	config := service.Config
	config.ComputerFiles = true
	config.ComputerRoots = []string{root}
	if err := WriteConfig(service.Store.DataDir(), config); err != nil {
		t.Fatal(err)
	}
	service.Config = config
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	if err := control.ReviewExternalChange(ctx); err != nil {
		t.Fatal(err)
	}
	selection := PermissionSelection{Mode: PermissionWorkspace, Source: "conversation", ComputerFiles: true,
		ComputerRoots: []string{root}, FileScope: FileScopeWorkspacePlusAllowedRoots,
		BlockedPaths: []string{filepath.Join(root, "blocked")}}
	operation, err := control.PlanControlChange(ctx, ControlPlanRequest{Kind: ControlSetConversationPermission,
		Arguments:     mustJSON(map[string]any{"conversation_id": conversationID, "selection": selection}),
		Authorization: ControlUserDirect, ConversationID: conversationID})
	if err != nil {
		t.Fatal(err)
	}
	code, err := control.ControlCodeForTests(ctx, operation.ID, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(ctx, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, operation.ID, ControlAuthorization{Source: ControlUserDirect,
		ConversationID: conversationID, Principal: "user", Code: code, Capability: capability}); err != nil {
		t.Fatal(err)
	}
	selected, err := NewTaskService(service).SelectedPermission(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Mode != PermissionWorkspace || selected.FileScope != FileScopeWorkspacePlusAllowedRoots ||
		len(selected.ComputerRoots) != 1 || selected.ComputerRoots[0] != root || len(selected.BlockedPaths) != 1 {
		t.Fatalf("conversation selection was not persisted through control: %+v", selected)
	}
	revert, err := control.PlanRevertControlChange(ctx, operation.ID, ControlUserDirect, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	code, err = control.ControlCodeForTests(ctx, revert.ID, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	capability, err = control.IssueInteractiveCapability(ctx, conversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, revert.ID, ControlAuthorization{Source: ControlUserDirect,
		ConversationID: conversationID, Principal: "user", Code: code, Capability: capability}); err != nil {
		t.Fatal(err)
	}
	selected, err = NewTaskService(service).SelectedPermission(ctx, conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Mode != PermissionAsk || selected.Source != "default" || selected.FileScope != "" || len(selected.ComputerRoots) != 0 {
		t.Fatalf("conversation selection revert did not restore default: %+v", selected)
	}
}

func TestControlCodeIsSharedAcrossSurfacesOfOneOwner(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	conversationID, otherConversation := conversation(t, service), conversation(t, service)
	control, err := NewControlService(NewTaskService(service))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := control.PlanControlChange(ctx, ControlPlanRequest{Kind: ControlSetPermission,
		Arguments: mustJSON(map[string]any{"mode": PermissionWorkspace}), Authorization: ControlModelProposed, ConversationID: conversationID})
	if err != nil {
		t.Fatal(err)
	}
	shown, err := control.RenderConfirmation(ctx, operation.ID, conversationID, "chat-ui")
	if err != nil {
		t.Fatal(err)
	}
	// Rendering on another surface of the same owner keeps the code alive.
	again, err := control.RenderConfirmation(ctx, operation.ID, conversationID, "cli")
	if err != nil || again.Code != shown.Code || again.ExpiresAt != shown.ExpiresAt {
		t.Fatalf("a second surface reissued the code: first=%+v second=%+v err=%v", shown, again, err)
	}
	// The code still belongs to its conversation.
	if _, err := control.RenderConfirmation(ctx, operation.ID, otherConversation, "cli"); !errors.Is(err, ErrControlUserInteractionRequired) {
		t.Fatalf("another conversation rendered the code: %v", err)
	}
	stranger, err := control.IssueInteractiveCapability(ctx, otherConversation, "cli")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, operation.ID, controlAuth(stranger, otherConversation, "cli", shown.Code)); !errors.Is(err, ErrControlUserInteractionRequired) {
		t.Fatalf("another conversation used the code: %v", err)
	}
	// A capability is still bound to the surface that holds it.
	gui, err := control.IssueInteractiveCapability(ctx, conversationID, "chat-ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ConfirmControlChange(ctx, operation.ID, controlAuth(gui, conversationID, "cli", shown.Code)); !errors.Is(err, ErrControlUserInteractionRequired) {
		t.Fatalf("a capability was accepted under another principal: %v", err)
	}
	// The code shown in the GUI is typed in the CLI.
	cli, err := control.IssueInteractiveCapability(ctx, conversationID, "cli")
	if err != nil {
		t.Fatal(err)
	}
	typed := strings.ToLower(shown.Code[:5] + "-" + shown.Code[5:])
	result, err := control.ConfirmControlChange(ctx, operation.ID, controlAuth(cli, conversationID, "cli", typed))
	if err != nil || !result.Applied || service.Config.Execution.PermissionMode != PermissionWorkspace {
		t.Fatalf("cross-surface confirm result=%+v permission=%s err=%v", result, service.Config.Execution.PermissionMode, err)
	}
}
