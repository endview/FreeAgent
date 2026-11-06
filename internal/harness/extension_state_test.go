package harness

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This child deliberately commits before dropping its RPC response. Its file
// is test-only effect evidence; production extensions use a transaction ledger.
func TestExtensionStateProcessHelper(t *testing.T) {
	root := os.Getenv("FREEAGENT_STATE_TEST_ROOT")
	if root == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(3)
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{"id": "com.example.state-test", "version": "1.0.0", "provides": []string{"state"}}
		case "state.preview":
			var p ExtensionStateChange
			_ = json.Unmarshal(request.Params, &p)
			result = map[string]any{"revision": p.ExpectedRevision, "before": map[string]int{"trust": 1}, "after": map[string]int{"trust": 2}, "summary": "exact test state change"}
		case "state.apply":
			var p struct {
				OperationID string `json:"operation_id"`
				Digest      string `json:"digest"`
			}
			_ = json.Unmarshal(request.Params, &p)
			file, err := os.OpenFile(filepath.Join(root, "writes"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(4)
			}
			_, _ = file.WriteString("applied\n")
			_ = file.Close()
			raw := mustJSON(map[string]any{"found": true, "operation_id": p.OperationID, "digest": p.Digest, "revision": 2})
			_ = os.WriteFile(filepath.Join(root, "result.json"), raw, 0600)
			os.Exit(0)
		case "state.result":
			raw, err := os.ReadFile(filepath.Join(root, "result.json"))
			if err != nil {
				result = map[string]bool{"found": false}
			} else {
				result = json.RawMessage(raw)
			}
		case "shutdown":
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]bool{"ok": true}})
			os.Exit(0)
		default:
			result = map[string]any{}
		}
		if len(request.ID) > 0 {
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}
	os.Exit(0)
}
func installStateTest(t *testing.T, core *Service) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("FREEAGENT_STATE_TEST_ROOT", root)
	id := "com.example.state-test"
	directory := filepath.Join(core.Store.DataDir(), "extensions", id)
	_ = os.MkdirAll(directory, 0700)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	manifest := ExtensionManifest{Protocol: extensionProtocol, ID: id, Name: "state test", Version: "1.0.0", State: true, Run: ExtensionRun{Command: binary, Args: []string{"-test.run=^TestExtensionStateProcessHelper$"}}, Env: []string{"FREEAGENT_STATE_TEST_ROOT"}}
	if err = os.WriteFile(filepath.Join(directory, "extension.json"), mustJSON(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	core.Config.Extensions.Enabled = map[string]bool{id: true}
	if err = WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	core.extensions = newExtensionHost(core.Store.DataDir(), core.Config.Workspace, core.Config, nil)
	t.Cleanup(func() { _ = core.extensions.close() })
	core.extensions.configure(core.Config)
	return root
}
func TestExtensionStateLostACKReconcilesWithoutReplay(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	root := installStateTest(t, core)
	control, err := core.Control()
	if err != nil {
		t.Fatal(err)
	}
	operation, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlChangeExtensionState, Authorization: ControlUserDirect, Arguments: mustJSON(ExtensionStateChange{ID: "com.example.state-test", Scope: ExtensionStateScope{SpaceID: "local", RoleID: "maya", ActorID: "owner"}, Action: "apply_proposal", ExpectedRevision: 1, SourceID: "input-1", Proposal: json.RawMessage(`{"relationship_delta":{"trust":0.05}}`)})})
	if err != nil {
		t.Fatal(err)
	}
	cap, err := control.IssueInteractiveCapability(t.Context(), "", "user")
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ConfirmControlChange(t.Context(), operation.ID, controlAuth(cap, "", "user", ""))
	if !errors.Is(err, ErrUnknownResult) || result.Operation.Status != ControlPartiallyApplied {
		t.Fatalf("lost ACK=%+v %v", result, err)
	}
	if _, err = control.ConfirmControlChange(t.Context(), operation.ID, controlAuth(cap, "", "user", "")); !errors.Is(err, ErrControlAlreadyUsed) {
		t.Fatal("replayed uncertain apply", err)
	}
	// Recreate the control service from its durable journal, as after restart.
	restarted := NewService(core.Store, core.Config, Adapters{Model: EchoModel{}, Tools: core.Tools})
	t.Cleanup(func() { _ = restarted.extensions.close() })
	reopened, err := NewControlService(NewTaskService(restarted))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := reopened.ReconcileExtensionChange(t.Context(), operation.ID)
	if err != nil || !verified.Applied || verified.Operation.Status != ControlApplied {
		t.Fatalf("reconcile=%+v %v", verified, err)
	}
	if _, err = reopened.ReconcileExtensionChange(t.Context(), operation.ID); err != nil {
		t.Fatal(err)
	}
	writes, err := os.ReadFile(filepath.Join(root, "writes"))
	if err != nil || strings.Count(string(writes), "applied") != 1 {
		t.Fatalf("write count=%q %v", writes, err)
	}
}
func TestExtensionIdentityReadsOnlyAdoptedMessages(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	result, err := tasks.Submit(t.Context(), chat, "input-id-test", "real user input")
	if err != nil {
		t.Fatal(err)
	}
	before, err := core.extensionIdentity(t.Context(), result.Run)
	if err != nil || len(before.Inputs) != 1 || before.Inputs[0].ActorID != "owner" {
		t.Fatal(before, err)
	}
	_, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.messages(conversation_id,run_id,body_json) VALUES($1,$2,$3)`, chat, result.Run.ID, string(mustJSON(ModelMessage{Role: "user", Content: "queued unadopted", Deferred: true})))
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 140; n++ {
		_, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.messages(conversation_id,run_id,body_json) VALUES($1,$2,$3)`, chat, result.Run.ID, string(mustJSON(ModelMessage{Role: "tool", Content: "tool output"})))
		if err != nil {
			t.Fatal(err)
		}
	}
	after, err := core.extensionIdentity(t.Context(), result.Run)
	if err != nil || string(mustJSON(before)) != string(mustJSON(after)) {
		t.Fatal("queued/tool records changed identity", after, err)
	}
}
func TestExtensionVariantsRequireSelectedEnabledCapability(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	c.Extensions.Enabled = map[string]bool{"cc.endview.affectweave": true}
	selection := CapabilitySelection{Extensions: []string{"cc.endview.affectweave"}, Variants: map[string]string{"cc.endview.affectweave": "maya"}}
	if err := selection.validate(c); err != nil {
		t.Fatal(err)
	}
	copy := selection.normalize()
	copy.Variants["cc.endview.affectweave"] = "other"
	if selection.Variants["cc.endview.affectweave"] != "maya" {
		t.Fatal("variant map aliased")
	}
	selection.Extensions = nil
	if selection.validate(c) == nil {
		t.Fatal("unselected variant accepted")
	}
}

func TestPassiveContextRejectsHostPrivileges(t *testing.T) {
	manifest := ExtensionManifest{Protocol: extensionProtocol, ID: "com.example.passive", Name: "passive", Version: "1.0.0", Run: ExtensionRun{Command: "program"}, State: true, PassiveContext: true}
	if err := validateExtensionManifest(manifest); err != nil {
		t.Fatal(err)
	}
	tools := manifest
	tools.Tools = true
	if validateExtensionManifest(tools) == nil {
		t.Fatal("passive tools accepted")
	}
	model := manifest
	model.Budget.ModelCalls = 1
	if validateExtensionManifest(model) == nil {
		t.Fatal("passive model budget accepted")
	}
	history := manifest
	history.Needs = []string{"history:10"}
	if validateExtensionManifest(history) == nil {
		t.Fatal("passive private history accepted")
	}
}
