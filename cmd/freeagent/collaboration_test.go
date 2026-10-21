package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func cliCollaborationPlan() harness.CollaborationPlan {
	return harness.CollaborationPlan{
		Objective: "Read selected facts",
		Budget:    harness.ParentBudget{MaxChildren: 4, MaxModelCalls: 12, MaxToolCalls: 8, MaxOutputBytes: 65536, MaxConcurrentChildren: 2, MaxActiveSeconds: 60},
		Roles: map[string]harness.RoleProfile{"reviewer": {
			Instructions: "Read only the explicit file.", Tools: []string{"file_read"}, Paths: []string{"facts.txt"}, OutputFormat: "json",
			Budget: harness.ChildBudget{MaxSteps: 6, MaxSeconds: 30, MaxOutputBytes: 16384, MaxCallOutputBytes: 4096},
		}},
		Children: []harness.ChildSpec{{Name: "one", Role: "reviewer", Objective: "Read facts"}, {Name: "two", Role: "reviewer", Objective: "Review facts"}},
	}
}

func TestCLICollaborationExplicitLifecycle(t *testing.T) {
	data, workspace := initCLI(t)
	if err := os.WriteFile(filepath.Join(workspace, "facts.txt"), []byte("facts"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil || len(body.Messages) == 0 {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		message := map[string]any{"role": "assistant", "content": `{"summary":"Read facts","findings":[]}`}
		finish := "stop"
		if body.Messages[len(body.Messages)-1].Role == "user" {
			message["content"] = ""
			message["tool_calls"] = []any{map[string]any{"id": "read", "type": "function", "function": map[string]any{"name": "file_read", "arguments": `{"path":"facts.txt"}`}}}
			finish = "tool_calls"
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}})
	}))
	defer server.Close()
	key := fixtureKey(t, server.URL, harness.KeySourceNone, "", "")
	config := cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "fixture"})
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	rawPlan, _ := json.Marshal(cliCollaborationPlan())
	planFile := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planFile, rawPlan, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "collab", "create", "--data-dir", data, "--plan", planFile); !errors.Is(err, harness.ErrCollaborationDisabled) {
		t.Fatalf("default enabled: %v", err)
	}
	if _, _, err := invoke(t, "", "config", "collaboration", "--data-dir", data, "--mode", "ask"); err != nil {
		t.Fatal(err)
	}
	raw, _, err := invoke(t, "", "collab", "create", "--data-dir", data, "--plan", planFile)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Parent    string `json:"parent_run"`
		Continued bool   `json:"continued"`
	}
	if json.Unmarshal([]byte(raw), &created) != nil || created.Parent == "" || created.Continued || requests.Load() != 0 {
		t.Fatalf("implicit dispatch=%s calls=%d", raw, requests.Load())
	}
	if _, _, err := invoke(t, "", "collab", "control", "--data-dir", data, "--parent", created.Parent, "--child", "one", "--action", "pause"); err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "collab", "continue", "--data-dir", data, "--parent", created.Parent)
	if err != nil {
		t.Fatal(err)
	}
	var report harness.CollaborationReport
	if json.Unmarshal([]byte(raw), &report) != nil || report.Parent.Status == "completed" || report.Children[0].Run.Steps != 0 || requests.Load() != 2 {
		t.Fatalf("paused=%s", raw)
	}
	if _, _, err := invoke(t, "", "collab", "control", "--data-dir", data, "--parent", created.Parent, "--child", "one", "--action", "resume"); err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "collab", "continue", "--data-dir", data, "--parent", created.Parent)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(raw), &report) != nil || report.Parent.Status != "completed" || report.Usage.ModelCalls != 4 || report.Usage.ToolCalls != 2 || requests.Load() != 4 {
		t.Fatalf("completed=%s calls=%d", raw, requests.Load())
	}
	if _, _, err := invoke(t, "", "collab", "continue", "--data-dir", data, "--parent", created.Parent); err != nil || requests.Load() != 4 {
		t.Fatalf("replay=%v calls=%d", err, requests.Load())
	}
	if _, _, err := invoke(t, "", "config", "collaboration", "--data-dir", data, "--mode", "off"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--provider", "echo"); err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--json", "single agent")
	if err != nil || !strings.Contains(raw, "Echo: single agent") {
		t.Fatalf("core after disable=%s %v", raw, err)
	}
}

func TestCLICollaborationCancelAndInspectOffline(t *testing.T) {
	data, _ := initCLI(t)
	if _, _, err := invoke(t, "", "config", "collaboration", "--data-dir", data, "--mode", "ask"); err != nil {
		t.Fatal(err)
	}
	plan := cliCollaborationPlan()
	rawPlan, _ := json.Marshal(plan)
	planFile := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planFile, rawPlan, 0600); err != nil {
		t.Fatal(err)
	}
	raw, _, err := invoke(t, "", "collab", "create", "--data-dir", data, "--plan", planFile)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Parent string `json:"parent_run"`
	}
	if json.Unmarshal([]byte(raw), &created) != nil {
		t.Fatal(raw)
	}
	// Inspection/control must not resolve this missing credential or launch MCP.
	config, err := harness.LoadConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	missing := fixtureKey(t, "https://127.0.0.1:1", harness.KeySourceEnv, "MISSING_COLLAB_FIXTURE_KEY", "")
	config.Models[config.DefaultModel] = harness.ModelConfig{Provider: "openai-compatible", Key: missing, Model: "offline"}
	config.MCPServers = map[string]harness.MCPServerConfig{"never": {Enabled: true, Command: filepath.Join(t.TempDir(), "never.exe")}}
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "collab", "inspect", "--data-dir", data, "--parent", created.Parent); err != nil {
		t.Fatalf("offline inspect=%v", err)
	}
	if _, _, err := invoke(t, "", "collab", "control", "--data-dir", data, "--parent", created.Parent, "--action", "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "collab", "control", "--data-dir", data, "--parent", created.Parent, "--action", "cancel"); err != nil {
		t.Fatalf("cancel not idempotent: %v", err)
	}
	raw, _, err = invoke(t, "", "collab", "inspect", "--data-dir", data, "--parent", created.Parent)
	var report harness.CollaborationReport
	if err != nil || json.Unmarshal([]byte(raw), &report) != nil || report.Parent.Status != "abandoned" || report.Usage.ModelCalls != 0 {
		t.Fatalf("cancel=%s %v", raw, err)
	}
	if _, _, err := invoke(t, "", "config", "collaboration", "--data-dir", data, "--mode", "off"); err != nil {
		t.Fatalf("cancel left unfinished runs: %v", err)
	}
}

func TestCLICollaborationReviseHistoryAndExplicitIncrease(t *testing.T) {
	data, _ := initCLI(t)
	if _, _, err := invoke(t, "", "config", "collaboration", "--data-dir", data, "--mode", "ask", "--active-seconds", "30"); err != nil {
		t.Fatal(err)
	}
	config, err := harness.LoadConfig(data)
	if err != nil || config.Collaboration.Budget.MaxActiveSeconds != 30 {
		t.Fatalf("parent time setting=%+v %v", config.Collaboration, err)
	}
	plan := cliCollaborationPlan()
	dir := t.TempDir()
	write := func(name string, data any) string {
		t.Helper()
		raw, err := json.Marshal(data)
		path := filepath.Join(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	out, _, err := invoke(t, "", "collab", "create", "--data-dir", data, "--plan", write("plan.json", plan))
	var created struct {
		Parent string `json:"parent_run"`
	}
	if err != nil || json.Unmarshal([]byte(out), &created) != nil {
		t.Fatalf("create=%s %v", out, err)
	}
	edit := harness.CollaborationEdit{ExpectedRevision: 1, Reason: "split undispatched work", Retire: []string{"one"},
		Upsert: []harness.ChildSpec{{Name: "review", Role: "reviewer", Objective: "Review later", DependsOn: []string{"two"}}}}
	file := write("edit.json", edit)
	if _, _, err := invoke(t, "", "collab", "revise", "--data-dir", data, "--parent", created.Parent, "--edit", file); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "collab", "revise", "--data-dir", data, "--parent", created.Parent, "--edit", file); !errors.Is(err, harness.ErrPlanRevision) {
		t.Fatalf("stale CLI revision accepted: %v", err)
	}
	budget := plan.Budget
	budget.MaxChildren++
	budget.MaxActiveSeconds++
	if _, _, err := invoke(t, "", "collab", "increase-limits", "--data-dir", data, "--parent", created.Parent, "--limits", write("limits.json", budget)); err != nil {
		t.Fatal(err)
	}
	// Revision history and increased limits do not need credentials or adapters.
	missing := fixtureKey(t, "https://127.0.0.1:1", harness.KeySourceEnv, "MISSING_REVISION_KEY", "")
	config.Models[config.DefaultModel] = harness.ModelConfig{Provider: "openai-compatible", Key: missing, Model: "offline"}
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	out, _, err = invoke(t, "", "collab", "revisions", "--data-dir", data, "--parent", created.Parent)
	var revisions []harness.PlanRevision
	if err != nil || json.Unmarshal([]byte(out), &revisions) != nil || len(revisions) != 3 ||
		revisions[1].Revision != 2 || revisions[2].Plan.Budget.MaxChildren != budget.MaxChildren {
		t.Fatalf("history=%s %v", out, err)
	}
}
