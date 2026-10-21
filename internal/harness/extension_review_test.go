package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// turn.finished must always see the settled output and status; needs only
// select the optional context around them.
func TestExtensionTurnFinishedAlwaysReceivesOutcome(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	// The shared helper process registers as com.example.test.
	extensionID := "com.example.test"
	extensionRoot := filepath.Join(service.Store.DataDir(), "extensions", extensionID)
	if err := os.MkdirAll(extensionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"com.example.test",
		"version":"1.0.0",
		"name":"Finished outcome",
		"description":"Receives the outcome without declaring output or status needs",
		"run":{"command":"` + strings.ReplaceAll(os.Args[0], `\`, `\\`) + `","args":["--freeagent-extension-helper"]},
		"needs":["message"],
		"context":{"phases":["turn"],"purposes":["chat"],"placement":["tail"]},
		"events":["turn.finished"]
	}`
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	service.Config.Extensions.Enabled = map[string]bool{extensionID: true}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })

	activity, err := service.extensions.finish(t.Context(), extensionID,
		Run{ID: "run-finished", Prompt: "hello", Output: "finished output", Status: "completed"},
		"completed", []ModelMessage{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("turn.finished without output/status needs: %v", err)
	}
	if activity["summary"] != "extension activity recorded" {
		t.Fatalf("activity=%+v", activity)
	}
}

func reviewBudgetRun(t *testing.T, service *Service, key string) Run {
	t.Helper()
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := service.Store.CreateRun(t.Context(), conversation(t, service), key, "budget", snapshot)
	if err != nil || !created {
		t.Fatalf("run=%+v created=%v err=%v", run, created, err)
	}
	return run
}

func reviewGenerate(t *testing.T, host *extensionHost, service *Service, run Run, manifest ExtensionManifest) error {
	t.Helper()
	invocation, err := host.beginInvocation(t.Context(), manifest.ID, service, run, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer host.endInvocation(invocation)
	request := &jsonrpc.Request{Method: "model.generate"}
	request.Params, _ = json.Marshal(map[string]any{
		"invocation": invocation.id,
		"messages":   []map[string]any{{"role": "user", "content": "hello"}},
	})
	_, err = host.handleHostRequest(t.Context(), manifest.ID, request)
	return err
}

// Every hook opens a new invocation, so a per-invocation counter would let an
// extension spend MaxModelCallsPerTurn once per hook instead of once per Run.
func TestExtensionModelBudgetSpansInvocationsOfOneRun(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	service.Config.Extensions.MaxModelCallsPerTurn = 1
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	manifest := ExtensionManifest{ID: "com.example.budget", Version: "1.0.0"}
	first := reviewBudgetRun(t, service, "budget-first")

	if err := reviewGenerate(t, host, service, first, manifest); err != nil {
		t.Fatalf("first model.generate: %v", err)
	}
	err := reviewGenerate(t, host, service, first, manifest)
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != errExtensionBudget.Code {
		t.Fatalf("second invocation in the same run spent a fresh budget: err=%v", err)
	}
	second := reviewBudgetRun(t, service, "budget-second")
	if err := reviewGenerate(t, host, service, second, manifest); err != nil {
		t.Fatalf("another run must start with its own budget: %v", err)
	}
	// A settled Run releases its counters; the other Run keeps its own.
	host.releaseRun(first.ID)
	host.mu.Lock()
	_, firstKept := host.usage[extensionUsageKey(first.ID, manifest.ID)]
	_, secondKept := host.usage[extensionUsageKey(second.ID, manifest.ID)]
	host.mu.Unlock()
	if firstKept || !secondKept {
		t.Fatalf("after release first=%v second=%v", firstKept, secondKept)
	}
}

type reviewToolRunner struct{ names []string }

func (runner reviewToolRunner) Definitions() []ToolDefinition {
	definitions := make([]ToolDefinition, 0, len(runner.names))
	for _, name := range runner.names {
		definitions = append(definitions, ToolDefinition{Name: name, Description: name,
			InputSchema: json.RawMessage(`{"type":"object"}`)})
	}
	return definitions
}

func (reviewToolRunner) Prepare(context.Context, ToolCall) (PreparedTool, error) {
	return PreparedTool{}, errors.New("not used")
}

// Only extension aliases can re-enter an extension. Ordinary tool names that
// merely contain "agent" or "delegate" are not recursion.
func TestExtensionRecursionGuardMatchesExtensionAliasesOnly(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	service.Tools = reviewToolRunner{names: []string{"file_read", "ext_0123abcd", "user_agent_lookup", "delegate_notes"}}
	runner, err := delegationTools(service, "run")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, definition := range runner.Definitions() {
		names = append(names, definition.Name)
	}
	if slices.Contains(names, "ext_0123abcd") ||
		!slices.Contains(names, "user_agent_lookup") || !slices.Contains(names, "delegate_notes") {
		t.Fatalf("delegated tool names=%v", names)
	}

	service, _, workspace := testHarness(t, EchoModel{})
	run := reviewBudgetRun(t, service, "recursion")
	host := newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	manifest := ExtensionManifest{ID: "com.example.recursion", Version: "1.0.0"}
	invocation, err := host.beginInvocation(t.Context(), manifest.ID, service, run, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer host.endInvocation(invocation)
	call := func(name string) error {
		request := &jsonrpc.Request{Method: "tool.call"}
		request.Params, _ = json.Marshal(map[string]any{"invocation": invocation.id, "name": name,
			"arguments": map[string]any{}})
		_, err := host.handleHostRequest(t.Context(), manifest.ID, request)
		return err
	}
	if err := call("ext_0123abcd"); err == nil || !strings.Contains(err.Error(), "recursion") {
		t.Fatalf("extension alias error=%v", err)
	}
	if err := call("user_agent_lookup"); err != nil && strings.Contains(err.Error(), "recursion") {
		t.Fatalf("ordinary tool name was treated as recursion: %v", err)
	}
}

func writeReviewExtensions(tb testing.TB, dataDir string, count int) {
	tb.Helper()
	for index := range count {
		id := fmt.Sprintf("com.example.ext%d", index)
		root := filepath.Join(dataDir, "extensions", id)
		if err := os.MkdirAll(root, 0o700); err != nil {
			tb.Fatal(err)
		}
		writeReviewManifest(tb, root, id, "1.0.0")
	}
}

func writeReviewManifest(tb testing.TB, root, id, version string) {
	tb.Helper()
	manifest := `{
		"protocol":"freeagent-ext/1",
		"id":"` + id + `",
		"version":"` + version + `",
		"name":"Review fixture",
		"description":"Manifest used to measure registry lookups",
		"run":{"command":"fixture.exe"},
		"needs":["message","history:4"],
		"settings_schema":{"type":"object","properties":{"mode":{"type":"string","enum":["a","b"]},"limit":{"type":"integer","minimum":1}},"additionalProperties":false},
		"context":{"phases":["turn","step"],"purposes":["chat"],"placement":["tail"]},
		"events":["turn.finished","operation.check"],
		"workflows":[{"name":"triage","description":"Triage input","input_schema":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}}]
	}`
	if err := os.WriteFile(filepath.Join(root, "extension.json"), []byte(manifest), 0o600); err != nil {
		tb.Fatal(err)
	}
}

// Hooks look up the definition several times per turn. The lookup must stay
// cheap and must still see manifest edits and newly imported extensions.
func BenchmarkExtensionDefinitionLookup(b *testing.B) {
	dataDir, workspace := b.TempDir(), b.TempDir()
	writeReviewExtensions(b, dataDir, 5)
	config := DefaultConfig(workspace)
	host := newExtensionHost(dataDir, workspace, config, nil)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := host.definitionFor("com.example.ext3", config); err != nil {
			b.Fatal(err)
		}
	}
}

func TestExtensionDefinitionLookupSeesManifestChanges(t *testing.T) {
	dataDir, workspace := t.TempDir(), t.TempDir()
	writeReviewExtensions(t, dataDir, 2)
	config := DefaultConfig(workspace)
	host := newExtensionHost(dataDir, workspace, config, nil)
	for range 3 {
		if definition, err := host.definitionFor("com.example.ext1", config); err != nil ||
			definition.Manifest.Version != "1.0.0" {
			t.Fatalf("definition=%+v err=%v", definition.Manifest, err)
		}
	}
	// Same length and an immediate rewrite: a size or coarse-clock check alone
	// would keep serving the old manifest.
	writeReviewManifest(t, filepath.Join(dataDir, "extensions", "com.example.ext1"), "com.example.ext1", "1.0.1")
	if definition, err := host.definitionFor("com.example.ext1", config); err != nil ||
		definition.Manifest.Version != "1.0.1" {
		t.Fatalf("edited definition=%+v err=%v", definition.Manifest, err)
	}
	root := filepath.Join(dataDir, "extensions", "com.example.added")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeReviewManifest(t, root, "com.example.added", "2.0.0")
	if definition, err := host.definitionFor("com.example.added", config); err != nil ||
		definition.Manifest.Version != "2.0.0" {
		t.Fatalf("added definition=%+v err=%v", definition.Manifest, err)
	}
	if err := os.WriteFile(filepath.Join(root, "extension.json"), []byte(`{"broken"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := host.definitionFor("com.example.added", config); err == nil {
		t.Fatal("broken manifest was served from the cache")
	}
	if err := os.RemoveAll(filepath.Join(dataDir, "extensions", "com.example.ext0")); err != nil {
		t.Fatal(err)
	}
	if _, err := host.definitionFor("com.example.ext0", config); err == nil {
		t.Fatal("removed extension was served from the cache")
	}
}

// reviewStepStore fills a Run with model attempts whose stored requests carry
// a realistic history, interleaved with tool attempts.
func reviewStepStore(tb testing.TB, models, tools int) (*Store, string) {
	tb.Helper()
	ctx := context.Background()
	data := filepath.Join(tb.TempDir(), "state")
	if err := InitStore(ctx, data); err != nil {
		tb.Fatal(err)
	}
	workspace := tb.TempDir()
	if err := WriteConfig(data, DefaultConfig(workspace)); err != nil {
		tb.Fatal(err)
	}
	store, err := OpenStore(ctx, data)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = store.Close() })
	item, err := store.CreateConversation(ctx, "steps", workspace)
	if err != nil {
		tb.Fatal(err)
	}
	run, _, err := store.CreateRun(ctx, item.ID, "steps", "steps", "{}")
	if err != nil {
		tb.Fatal(err)
	}
	history := make([]ModelMessage, 0, 2*models)
	for index := range models {
		history = append(history,
			ModelMessage{Role: "user", Content: fmt.Sprintf("step %d %s", index, strings.Repeat("u", 512))},
			ModelMessage{Role: "assistant", Content: strings.Repeat("a", 512)})
		raw, err := json.Marshal(ModelRequest{ModelID: "echo", Messages: history})
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := store.db.ExecContext(ctx, `INSERT INTO attempts VALUES(?,?,'model','','finished',?,'')`,
			newID(), run.ID, string(raw)); err != nil {
			tb.Fatal(err)
		}
		if index < tools {
			if _, err := store.db.ExecContext(ctx, `INSERT INTO attempts VALUES(?,?,'tool',?,'finished','{}','')`,
				newID(), run.ID, newID()); err != nil {
				tb.Fatal(err)
			}
		}
	}
	return store, run.ID
}

func reviewListedModelSteps(ctx context.Context, store *Store, runID string) (int, error) {
	attempts, err := store.ListAttempts(ctx, runID)
	step := 0
	for _, attempt := range attempts {
		if attempt.Kind == "model" {
			step++
		}
	}
	return step, err
}

func TestExtensionStepCountMatchesModelAttempts(t *testing.T) {
	store, runID := reviewStepStore(t, 3, 2)
	listed, err := reviewListedModelSteps(t.Context(), store, runID)
	if err != nil {
		t.Fatal(err)
	}
	counted, err := store.countModelAttempts(t.Context(), runID)
	if err != nil || counted != listed || counted != 3 {
		t.Fatalf("counted=%d listed=%d err=%v", counted, listed, err)
	}
	if other, err := store.countModelAttempts(t.Context(), "missing-run"); err != nil || other != 0 {
		t.Fatalf("unrelated run counted=%d err=%v", other, err)
	}
}

// Every model step asks for the step number. The former path decoded every
// stored request, so its cost grew with the square of the Run length.
func BenchmarkExtensionStepCount(b *testing.B) {
	store, runID := reviewStepStore(b, 40, 40)
	b.Run("list_attempts", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := reviewListedModelSteps(b.Context(), store, runID); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("count", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := store.countModelAttempts(b.Context(), runID); err != nil {
				b.Fatal(err)
			}
		}
	})
}
