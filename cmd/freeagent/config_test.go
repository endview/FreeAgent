package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestCLIConfigShowIsOfflineAndOmitsSensitiveContent(t *testing.T) {
	data, workspace := initCLI(t)
	const secret = "fixture-secret-must-not-be-shown"
	t.Setenv("CLI_CONFIG_KEY", secret)
	key := fixtureKey(t, "https://127.0.0.1:1", harness.KeySourceSaved, "", secret)
	config := cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "configured-model"})
	config.SystemPrompt = "private-system-prompt"
	config.Instructions = []string{"selected.txt"}
	if err := os.WriteFile(filepath.Join(data, "instructions", "selected.txt"), []byte("private-instruction-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	config.MCPServers = map[string]harness.MCPServerConfig{
		"zeta": {
			Enabled: true, Command: filepath.Join(workspace, "never-start-this-program"),
			Args: []string{"private-mcp-argument"}, Env: map[string]string{"MODE": "private-env-value"},
			EnvFrom: map[string]string{"TOKEN": "CLI_CONFIG_KEY"},
		},
		"alpha":    {Enabled: true, Command: filepath.Join(workspace, "also-must-not-start")},
		"disabled": {Enabled: false},
	}
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(data, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Viewing config must also work while another process owns the runtime.
	store, err := harness.OpenStore(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw, stderr, err := invoke(t, "", "config", "show", "--data-dir", data)
	if err != nil {
		t.Fatal(err)
	}
	var shown struct {
		OK                  bool                           `json:"ok"`
		Models              map[string]harness.ModelConfig `json:"models"`
		DefaultModel        string                         `json:"default_model"`
		ConfigPath          string                         `json:"config_path"`
		Workspace           string                         `json:"workspace"`
		EnabledMCPServers   []string                       `json:"enabled_mcp_servers"`
		Instructions        []string                       `json:"instructions"`
		Limits              map[string]int                 `json:"limits"`
		Changed             bool                           `json:"changed"`
		NetworkTested       bool                           `json:"network_tested"`
		CredentialsResolved bool                           `json:"credentials_resolved"`
	}
	if err := json.Unmarshal([]byte(raw), &shown); err != nil {
		t.Fatal(err)
	}
	if !shown.OK || shown.Changed || shown.NetworkTested || shown.CredentialsResolved ||
		shown.Models[shown.DefaultModel] != config.DefaultModelConfig() || shown.ConfigPath != filepath.Join(data, "config.json") ||
		shown.Workspace != workspace || shown.Limits["max_steps"] != config.MaxSteps ||
		!reflect.DeepEqual(shown.EnabledMCPServers, []string{"alpha", "zeta"}) ||
		!reflect.DeepEqual(shown.Instructions, config.Instructions) {
		t.Fatalf("unexpected config summary: %+v", shown)
	}
	for _, omitted := range []string{
		secret, config.SystemPrompt, "private-instruction-content", "private-mcp-argument", "private-env-value",
	} {
		if strings.Contains(raw+stderr, omitted) {
			t.Fatal("config show exposed content outside its diagnostic summary")
		}
	}
	after, err := os.ReadFile(filepath.Join(data, "config.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal("config show changed the configuration")
	}
}

func TestCLIConfigMutationUsesControlServiceWithoutOwner(t *testing.T) {
	data, _ := initCLI(t)
	raw, stderr, err := invoke(t, "", "config", "model", "--data-dir", data,
		"--id", "next", "--provider", "echo", "--default")
	if err != nil {
		t.Fatalf("config mutation failed: output=%q stderr=%q err=%v", raw, stderr, err)
	}
	var result struct {
		Changed bool                           `json:"changed"`
		Models  map[string]harness.ModelConfig `json:"models"`
		Default string                         `json:"default_model"`
		Kind    string                         `json:"operation.kind"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("config output=%q err=%v", raw, err)
	}
	if !result.Changed || result.Default != "next" || result.Models["next"].Provider != "echo" {
		t.Fatalf("unexpected config result=%+v output=%q", result, raw)
	}
	journal, err := os.ReadFile(filepath.Join(data, "control-journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(journal), `"action":"planned"`) ||
		!strings.Contains(string(journal), `"action":"applied"`) ||
		!strings.Contains(string(journal), harness.ControlSetModelProfile) {
		t.Fatalf("config mutation bypassed the control journal: %s", journal)
	}
}

func TestCLIConfigModelSwitchPreservesAndUsesHistory(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json", "first turn")
	if err != nil {
		t.Fatal(err)
	}
	var first harness.RunResult
	if err := json.Unmarshal([]byte(raw), &first); err != nil {
		t.Fatal(err)
	}
	type modelRequest struct {
		Model    string                 `json:"model"`
		Messages []harness.ModelMessage `json:"messages"`
	}
	requests := make(chan modelRequest, 1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		var body modelRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case requests <- body:
		default:
			t.Error("unexpected extra model request")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Provider reply"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	const secret = "fixture-configuration-key"
	key := fixtureKey(t, server.URL, harness.KeySourceSaved, "", secret)
	raw, stderr, err := invoke(t, "", "config", "model", "--data-dir", data,
		"--provider", "openai-compatible", "--key", key, "--model", "selected-model")
	if err != nil || strings.Contains(raw+stderr, secret) || calls.Load() != 0 {
		t.Fatalf("configuration made a model call or failed: calls=%d error=%v", calls.Load(), err)
	}
	var changed struct {
		Changed      bool                           `json:"changed"`
		Models       map[string]harness.ModelConfig `json:"models"`
		DefaultModel string                         `json:"default_model"`
	}
	if err := json.Unmarshal([]byte(raw), &changed); err != nil || !changed.Changed ||
		changed.Models[changed.DefaultModel].Model != "selected-model" || changed.Models[changed.DefaultModel].Key != key {
		t.Fatalf("configuration result=%+v error=%v", changed, err)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--session", first.Run.ConversationID, "--json", "second turn")
	if err != nil {
		t.Fatal(err)
	}
	var second harness.RunResult
	if err := json.Unmarshal([]byte(raw), &second); err != nil || second.Answer != "Provider reply" || second.Run.Status != "completed" {
		t.Fatalf("second run=%+v error=%v", second, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("model calls=%d", calls.Load())
	}
	select {
	case request := <-requests:
		if request.Model != "selected-model" || len(request.Messages) != 4 ||
			request.Messages[1].Role != "user" || request.Messages[1].Content != "first turn" ||
			request.Messages[2].Role != "assistant" || request.Messages[2].Content != "Echo: first turn" ||
			request.Messages[3].Content != "second turn" {
			t.Fatalf("model did not receive the existing history: %+v", request)
		}
	default:
		t.Fatal("model request was not received")
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--provider", "echo"); err != nil {
		t.Fatal(err)
	}
	config, err := harness.LoadConfig(data)
	if err != nil || config.DefaultModelConfig() != (harness.ModelConfig{Provider: "echo"}) {
		t.Fatalf("switching back retained provider settings: %+v error=%v", config.DefaultModelConfig(), err)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--session", first.Run.ConversationID, "--json", "third turn")
	if err != nil || !strings.Contains(raw, "Echo: third turn") || calls.Load() != 1 {
		t.Fatalf("echo run failed: output=%q error=%v calls=%d", raw, err, calls.Load())
	}
	raw, _, err = invoke(t, "", "sessions", "--data-dir", data, "--show", first.Run.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	var messages []harness.Message
	if err := json.Unmarshal([]byte(raw), &messages); err != nil || len(messages) != 6 ||
		messages[0].Content != "first turn" || messages[3].Content != "Provider reply" {
		t.Fatalf("history changed after switching models: %+v error=%v", messages, err)
	}
}

func TestCLIConfigRejectsInvalidCommandsWithoutChangingConfig(t *testing.T) {
	data, _ := initCLI(t)
	before, err := os.ReadFile(filepath.Join(data, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Retired flags stay rejected; the names are assembled so that searches
	// for them find only real uses.
	retiredEndpoint, retiredKeyEnv := "--"+"endpoint", "--"+"api-key-env"
	for _, args := range [][]string{
		{"config"},
		{"config", "unknown"},
		{"config", "show", "--data-dir", data, "extra"},
		{"config", "show", "--data-dir", data, "--provider", "echo"},
		{"config", "model", "--data-dir", data},
		{"config", "model", "--data-dir", data, "--provider", "echo", "--model", "unused"},
		{"config", "model", "--data-dir", data, "--provider", "echo", "--key", "k-0badc0de"},
		{"config", "model", "--data-dir", data, "--provider", "openai-compatible", "--model", "missing-key"},
		{"config", "model", "--data-dir", data, "--provider", "openai-compatible", "--key", "k-0badc0de"},
		{"config", "model", "--data-dir", data, "--provider", "openai-compatible", "--model", "bad-key-id",
			"--key", "not a key id"},
		{"config", "model", "--data-dir", data, "--provider", "openai-compatible", "--model", "retired-flag",
			retiredEndpoint, "https://provider.example/v1/chat/completions"},
		{"config", "model", "--data-dir", data, "--model", "retired-flag", retiredKeyEnv, "FREEAGENT_RETIRED"},
		{"config", "model", "--data-dir", data, "--id", "missing", "--default"},
		{"config", "model", "--data-dir", data, "--id", "default", "--new-default", "default"},
		{"config", "workspace", "--data-dir", data},
		{"config", "workspace", "--data-dir", data, "--path", filepath.Join(data, "no-such-directory")},
		{"config", "http", "--data-dir", data, "--enabled"},
		{"config", "http", "--data-dir", data, "--enabled", "--origin", "http://remote.example"},
		{"config", "http", "--data-dir", data, "--enabled", "--origin", "https://docs.example/"},
		{"config", "http", "--data-dir", data, "--enabled", "--origin", "https://docs.example", "--timeout", "61"},
		{"config", "commands", "--data-dir", data, "--enabled", "--allow", "relative.exe"},
	} {
		raw, _, err := invoke(t, "", args...)
		if err == nil || raw != "" {
			t.Fatalf("invalid command returned success output: args=%v output=%q error=%v", args, raw, err)
		}
		after, err := os.ReadFile(filepath.Join(data, "config.json"))
		if err != nil || string(after) != string(before) {
			t.Fatalf("rejected command changed config: %v", args)
		}
	}
}

func TestCLIOptionalPoliciesAreOfflineAndRemovable(t *testing.T) {
	data, _ := initCLI(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "commands", "--data-dir", data, "--enabled", "--allow", executable},
		{"config", "http", "--data-dir", data, "--enabled", "--origin", "https://docs.example", "--origin", "http://127.0.0.1:1"},
	} {
		raw, _, err := invoke(t, "", args...)
		if err != nil || !strings.Contains(raw, `"network_tested": false`) {
			t.Fatalf("offline config=%q err=%v", raw, err)
		}
	}
	raw, _, err := invoke(t, "", "tools", "list", "--data-dir", data)
	if err != nil || !strings.Contains(raw, "http_get") || !strings.Contains(raw, "command_run") ||
		!strings.Contains(raw, `"network_tested": false`) {
		t.Fatalf("tools=%q err=%v", raw, err)
	}
	config, err := harness.LoadConfig(data)
	if err != nil || len(config.Commands.AllowedExecutables) != 1 || len(config.HTTP.AllowedOrigins) != 2 {
		t.Fatalf("saved config=%+v err=%v", config, err)
	}
	for _, tool := range []string{"http", "commands"} {
		if _, _, err := invoke(t, "", "config", tool, "--data-dir", data, "--enabled=false"); err != nil {
			t.Fatal(err)
		}
	}
	raw, _, err = invoke(t, "", "tools", "list", "--data-dir", data)
	if err != nil || strings.Contains(raw, "http_get") || strings.Contains(raw, "command_run") {
		t.Fatalf("optional tools remained advertised: %s err=%v", raw, err)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--json", "ordinary task after removal")
	if err != nil || !strings.Contains(raw, "Echo: ordinary task after removal") {
		t.Fatalf("optional removal broke core: %s err=%v", raw, err)
	}
}

func TestCLIHTTPExactApprovalAndEvidence(t *testing.T) {
	data, _ := initCLI(t)
	var requests, modelCalls atomic.Int32
	content := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("real local docs fixture"))
	}))
	defer content.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sequence := modelCalls.Add(1)
		message := map[string]any{"role": "assistant", "content": "Project docs read."}
		finish := "stop"
		if sequence == 1 {
			arguments, _ := json.Marshal(map[string]string{"url": content.URL + "/docs?exact=1"})
			message["content"] = ""
			message["tool_calls"] = []any{map[string]any{"id": "get", "type": "function",
				"function": map[string]any{"name": "http_get", "arguments": string(arguments)}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
	}))
	defer model.Close()
	if _, _, err := invoke(t, "", "config", "http", "--data-dir", data, "--enabled", "--origin", content.URL); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data,
		"--provider", "openai-compatible", "--key", fixtureKey(t, model.URL, harness.KeySourceNone, "", ""), "--model", "fixture"); err != nil {
		t.Fatal(err)
	}
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json", "read explicit project docs")
	var waiting harness.RunResult
	if exitCode(err) != exitWaiting || json.Unmarshal([]byte(raw), &waiting) != nil ||
		waiting.Run.Status != "waiting_confirmation" || requests.Load() != 0 {
		t.Fatalf("request before approval: %s err=%v", raw, err)
	}
	if _, _, err := invoke(t, "", "config", "http", "--data-dir", data, "--enabled=false"); err != nil {
		t.Fatalf("future HTTP policy change was blocked by a waiting approval: %v", err)
	}
	raw, stderr, err := invoke(t, "y\n", "continue", "--data-dir", data, "--run", waiting.Run.ID, "--interactive", "--json")
	var completed harness.RunResult
	if err != nil || json.Unmarshal([]byte(raw), &completed) != nil || completed.Run.Status != "completed" ||
		requests.Load() != 1 || len(completed.Evidence.HTTP) != 1 || completed.Evidence.HTTP[0].HTTPStatus != 200 ||
		!strings.Contains(stderr, "GET "+content.URL+"/docs?exact=1") {
		t.Fatalf("approved HTTP result=%s stderr=%s err=%v", raw, stderr, err)
	}
	raw, _, err = invoke(t, "", "inspect", "--data-dir", data, "--run", waiting.Run.ID)
	if err != nil || !strings.Contains(raw, `"http_requests"`) || requests.Load() != 1 {
		t.Fatalf("inspection lost evidence or replayed: %s err=%v", raw, err)
	}
}

// initEmptyCLI creates a data directory with no models.
func initEmptyCLI(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	data, workspace := filepath.Join(base, "data"), filepath.Join(base, "project")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "init", "--data-dir", data, "--workspace", workspace); err != nil {
		t.Fatal(err)
	}
	return data, workspace
}

func TestConfigModelKey(t *testing.T) {
	data, _ := initCLI(t)
	name := "config " + t.Name()
	raw, _, err := invoke(t, "", "key", "add", "--name", name, "--url", "http://127.0.0.1:9/v1", "--no-key")
	var added map[string]string
	if err != nil || json.Unmarshal([]byte(raw), &added) != nil {
		t.Fatalf("key add=%q err=%v", raw, err)
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "byname", "--key", name, "--model", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "byid", "--key", added["id"], "--model", "m2"); err != nil {
		t.Fatal(err)
	}
	config, err := harness.LoadConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"byname": "m1", "byid": "m2"} {
		model := config.Models[id]
		if model.Provider != "openai-compatible" || model.Key != added["id"] || model.Model != want {
			t.Fatalf("%s=%+v", id, model)
		}
	}
	if config.DefaultModel != "default" {
		t.Fatalf("adding models switched the default to %q", config.DefaultModel)
	}
	before, _ := os.ReadFile(filepath.Join(data, "config.json"))
	_, _, err = invoke(t, "", "config", "model", "--data-dir", data, "--id", "missing", "--key", "no such profile "+t.Name(), "--model", "m3")
	if exitCode(err) != exitUsage || !errors.Is(err, harness.ErrKeyNotFound) {
		t.Fatalf("missing key exit=%d err=%v", exitCode(err), err)
	}
	if after, _ := os.ReadFile(filepath.Join(data, "config.json")); string(after) != string(before) {
		t.Fatal("a missing key profile changed the configuration")
	}
}

func TestConfigModelFirstBecomesDefault(t *testing.T) {
	data, _ := initEmptyCLI(t)
	config, err := harness.LoadConfig(data)
	if err != nil || len(config.Models) != 0 || config.DefaultModel != "" {
		t.Fatalf("fresh config=%+v err=%v", config, err)
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "first", "--provider", "echo"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "second", "--provider", "echo"); err != nil {
		t.Fatal(err)
	}
	config, err = harness.LoadConfig(data)
	if err != nil || config.DefaultModel != "first" || config.DefaultSelection != "first" || len(config.Models) != 2 {
		t.Fatalf("config=%+v err=%v", config, err)
	}
}

func TestConfigModelDefaultAndRemove(t *testing.T) {
	data, _ := initEmptyCLI(t)
	for _, id := range []string{"first", "second"} {
		if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", id, "--provider", "echo"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "second", "--default"); err != nil {
		t.Fatal(err)
	}
	config, err := harness.LoadConfig(data)
	if err != nil || config.DefaultModel != "second" || config.DefaultSelection != "second" || config.Models["second"].Provider != "echo" {
		t.Fatalf("switch=%+v err=%v", config, err)
	}
	before, _ := os.ReadFile(filepath.Join(data, "config.json"))
	for _, args := range [][]string{
		{"--id", "missing", "--default"},
		{"--id", "second", "--remove"},
		{"--id", "missing", "--remove"},
		{"--id", "second", "--remove", "--new-default", "second"},
		{"--id", "second", "--remove", "--provider", "echo"},
	} {
		_, _, err := invoke(t, "", append([]string{"config", "model", "--data-dir", data}, args...)...)
		if exitCode(err) != exitUsage {
			t.Fatalf("%v exit=%d err=%v", args, exitCode(err), err)
		}
		if after, _ := os.ReadFile(filepath.Join(data, "config.json")); string(after) != string(before) {
			t.Fatalf("%v changed the configuration", args)
		}
	}
	_, stderr, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "second", "--remove", "--new-default", "first")
	if err != nil || !strings.Contains(stderr, "models: removed second") || !strings.Contains(stderr, "default_model: first") {
		t.Fatalf("remove stderr=%q err=%v", stderr, err)
	}
	config, err = harness.LoadConfig(data)
	if err != nil || config.DefaultModel != "first" || config.DefaultSelection != "first" || len(config.Models) != 1 {
		t.Fatalf("after remove=%+v err=%v", config, err)
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "first", "--remove"); err != nil {
		t.Fatal(err)
	}
	config, err = harness.LoadConfig(data)
	if err != nil || len(config.Models) != 0 || config.DefaultModel != "" {
		t.Fatalf("last model removal=%+v err=%v", config, err)
	}
}

func TestConfigWorkspaceUsesExecutionLeaseSemantics(t *testing.T) {
	data, _ := initCLI(t)
	next := t.TempDir()
	keyID := fixtureKey(t, "http://127.0.0.1:9/v1", harness.KeySourceEnv, "FREEAGENT_TEST_UNSET_WORKSPACE_KEY", "")
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "reentry", "--key", keyID, "--model", "m"); err != nil {
		t.Fatal(err)
	}
	raw, stderr, err := invoke(t, "", "run", "--data-dir", data, "--model", "reentry", "--json", "needs a key")
	var stopped harness.RunResult
	if exitCode(err) != exitStopped || json.Unmarshal([]byte(raw), &stopped) != nil || stopped.Run.Status != "stopped" ||
		!strings.Contains(stderr, "key edit") {
		t.Fatalf("stopped run exit=%d raw=%q stderr=%q err=%v", exitCode(err), raw, stderr, err)
	}
	assertWorkspaceChangeAllowed := func(status string, target string) {
		t.Helper()
		raw, _, err := invoke(t, "", "config", "workspace", "--data-dir", data, "--path", target)
		if err != nil || !strings.Contains(raw, `"changed": true`) {
			t.Fatalf("%s run: workspace change raw=%q err=%v", status, raw, err)
		}
		if config, err := harness.LoadConfig(data); err != nil || config.Workspace != target {
			t.Fatalf("%s run: workspace=%q err=%v", status, config.Workspace, err)
		}
	}
	assertWorkspaceChangeAllowed("stopped", next)
	// Model changes stay allowed; each run keeps the model it was bound to.
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "later", "--provider", "echo"); err != nil {
		t.Fatalf("model change blocked by an unfinished run: %v", err)
	}
	if _, _, err := invoke(t, "", "abandon", "--data-dir", data, "--run", stopped.Run.ID); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call := calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"list` + string(rune('0'+call)) +
			`","type":"function","function":{"name":"file_list","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer server.Close()
	config, err := harness.LoadConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxSteps = 1
	config.Models["looping"] = harness.ModelConfig{Provider: "openai-compatible", Model: "fixture",
		Key: fixtureKey(t, server.URL, harness.KeySourceNone, "", "")}
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--model", "looping", "--json", "list forever")
	var limited harness.RunResult
	if exitCode(err) != exitStopped || json.Unmarshal([]byte(raw), &limited) != nil || limited.Run.Status != "limited" {
		t.Fatalf("limited run exit=%d raw=%q err=%v", exitCode(err), raw, err)
	}
	limitedWorkspace := t.TempDir()
	assertWorkspaceChangeAllowed("limited", limitedWorkspace)
	if _, _, err := invoke(t, "", "abandon", "--data-dir", data, "--run", limited.Run.ID); err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "config", "workspace", "--data-dir", data, "--path", next)
	if err != nil || !strings.Contains(raw, `"changed": true`) {
		t.Fatalf("workspace change after abandon=%q err=%v", raw, err)
	}
	if config, err := harness.LoadConfig(data); err != nil || config.Workspace != next {
		t.Fatalf("workspace=%q err=%v", config.Workspace, err)
	}
}
