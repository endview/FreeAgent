package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureModelPreservesHistoryAndOtherSettings(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	data := service.Store.DataDir()
	id := conversation(t, service)
	if _, err := service.Run(ctx, id, "first", "keep this conversation"); err != nil {
		t.Fatal(err)
	}
	config := service.Config
	config.Instructions = []string{"selected.txt"}
	config.MaxSteps = 17
	config.MCPServers = map[string]MCPServerConfig{
		"trusted": {Enabled: true, Command: filepath.Join(data, "must-not-start"), EnvFrom: map[string]string{"TOKEN": "MISSING_CONFIG_TEST_KEY"}},
	}
	if err := os.WriteFile(filepath.Join(data, "instructions", "selected.txt"), []byte("Keep these instructions."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	if err := service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	model := ModelConfig{Provider: "openai-compatible", Key: "k-0badc0de", Model: "selected-model"}
	updated, err := ConfigureModel(ctx, data, "new", model, true)
	if err != nil || updated.Models["new"] != model || updated.DefaultModel != "new" || updated.MaxSteps != 17 || len(updated.MCPServers) != 1 {
		t.Fatalf("configuration=%+v error=%v", updated, err)
	}
	loaded, err := LoadConfig(data)
	if err != nil || loaded.Models["new"] != model || loaded.DefaultModel != "new" || loaded.instructionText != "Keep these instructions.\n" {
		t.Fatalf("saved configuration=%+v error=%v", loaded, err)
	}
	store, err := OpenStore(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	messages, err := store.ListMessages(ctx, id)
	if err != nil || len(messages) != 2 || messages[0].Content != "keep this conversation" {
		t.Fatalf("history=%+v error=%v", messages, err)
	}
}

func TestConfigureModelRejectsOwnerAndInvalidChanges(t *testing.T) {
	for _, state := range []string{"owned", "invalid", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			service, _, _ := testHarness(t, EchoModel{})
			data := service.Store.DataDir()
			configPath := filepath.Join(data, "config.json")
			before, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			model := ModelConfig{Provider: "openai-compatible", Key: "k-0badc0de", Model: "next"}
			want := ErrActiveRun
			switch state {
			case "owned":
				want = ErrOwnerActive
			case "invalid":
				model.Key = "not-a-key-id"
				_ = service.Store.Close()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
				_ = service.Store.Close()
			default:
				snapshot, _ := service.snapshot()
				run, _, err := service.Store.CreateRun(ctx, conversation(t, service), "pending", "task", snapshot)
				if err != nil {
					t.Fatal(err)
				}
				if err := service.Store.FinishRun(ctx, run.ID, state, "test"); err != nil {
					t.Fatal(err)
				}
				_ = service.Store.Close()
			}
			_, err = ConfigureModel(ctx, data, "new", model, true)
			if err == nil || (state != "invalid" && !errors.Is(err, want)) {
				t.Fatalf("configuration error=%v want=%v", err, want)
			}
			after, err := os.ReadFile(configPath)
			if err != nil || string(before) != string(after) {
				t.Fatal("rejected update changed the configuration")
			}
		})
	}
}

func TestConfigureModelDoesNotRewriteUnfinishedRuns(t *testing.T) {
	for _, state := range []string{"running", "waiting_confirmation", "stopped", "unknown", "limited"} {
		t.Run(state, func(t *testing.T) {
			service, _, _ := testHarness(t, EchoModel{})
			data := service.Store.DataDir()
			snapshot, _ := service.snapshot()
			run, _, err := service.Store.CreateRun(t.Context(), conversation(t, service), "saved", "task", snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Store.FinishRun(t.Context(), run.ID, state, "saved"); err != nil {
				t.Fatal(err)
			}
			_ = service.Store.Close()
			_, err = ConfigureModel(t.Context(), data, "next", ModelConfig{Provider: "echo"}, true)
			if err != nil {
				t.Fatal(err)
			}
			store, err := OpenStore(t.Context(), data)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			saved, err := store.GetRun(t.Context(), run.ID)
			if err != nil || saved.ConfigJSON != snapshot || saved.InitialModel != "default" {
				t.Fatalf("binding was rewritten: %+v %v", saved, err)
			}
		})
	}
}

func TestConfigAllowsZeroModels(t *testing.T) {
	service, _, workspace := testHarness(t, EchoModel{})
	data := service.Store.DataDir()
	config := DefaultConfig(workspace)
	if len(config.Models) != 0 || config.DefaultModel != "" || config.DefaultSelection != "" || config.Auto.Enabled {
		t.Fatalf("default configuration ships a model: %+v", config)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, set := range map[string]func(*Config){
		"default model":     func(config *Config) { config.DefaultModel = "default" },
		"default selection": func(config *Config) { config.DefaultSelection = "default" },
		"auto":              func(config *Config) { config.Auto.Enabled = true },
	} {
		changed := DefaultConfig(workspace)
		set(&changed)
		if changed.Validate() == nil {
			t.Errorf("%s accepted without models", name)
		}
	}
	if err := WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(data)
	if err != nil || len(loaded.Models) != 0 || loaded.DefaultModel != "" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	service.Config = loaded
	if result, err := service.Run(context.Background(), conversation(t, service), "no-model", "hello"); err == nil {
		t.Fatalf("run without models succeeded: %+v", result)
	}
	var runs int
	if err := service.Store.db.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("runs=%d err=%v", runs, err)
	}
}

func TestSuggestModelID(t *testing.T) {
	config := testEchoConfig(t.TempDir())
	config.Models["a-very-long-model-name-that-is-already-configured"] = ModelConfig{Provider: "echo"}
	tests := []struct {
		name string
		want string
	}{
		{"deepseek-chat", "deepseek-chat"},
		{"deepseek/chat v3", "deepseek-chat-v3"},
		{"", "model"},
		{"中文模型", "----"},
	}
	for _, test := range tests {
		if got := SuggestModelID(test.name, config); got != test.want {
			t.Fatalf("SuggestModelID(%q)=%q, want %q", test.name, got, test.want)
		}
	}
	long := strings.Repeat("x", 80)
	if got := SuggestModelID(long, config); len(got) != 64 {
		t.Fatalf("long model id length=%d, want 64", len(got))
	}
	config.Models["same-model"] = ModelConfig{Provider: "echo"}
	config.Models["same-model-2"] = ModelConfig{Provider: "echo"}
	if got := SuggestModelID("same model", config); got != "same-model-3" {
		t.Fatalf("duplicate model id=%q, want same-model-3", got)
	}
	config.Models["auto"] = ModelConfig{Provider: "echo"}
	if got := SuggestModelID("auto", config); got == "auto" || !validModelID(got) {
		t.Fatalf("reserved model id=%q", got)
	}
}

func TestConfigureModelsReusesStore(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	data := service.Store.DataDir()
	tasks := NewTaskService(service)
	addModel := func(id, strength string) func(*Config) error {
		return func(config *Config) error {
			config.Models[id] = ModelConfig{Provider: "echo", Capabilities: ModelCapabilities{Strength: strength}}
			return nil
		}
	}
	if err := tasks.ConfigureModels(ctx, addModel("light", "light")); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(data)
	if err != nil || loaded.Models["light"].Capabilities.Strength != "light" || tasks.Config().Models["light"].Capabilities.Strength != "light" {
		t.Fatalf("model not saved: loaded=%+v err=%v", loaded.Models, err)
	}
	// The engine still owns the data directory; ConfigureModels never reopened it.
	if second, err := OpenStore(ctx, data); !errors.Is(err, ErrOwnerActive) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second owner error=%v", err)
	}
	service.Store.runMu.Lock()
	err = tasks.ConfigureModels(ctx, addModel("busy", "light"))
	service.Store.runMu.Unlock()
	if !errors.Is(err, ErrActiveRun) {
		t.Fatalf("active run error=%v", err)
	}

	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := service.Store.CreateRun(ctx, conversation(t, service), "unfinished", "task", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Store.FinishRun(ctx, run.ID, "stopped", "test"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.ConfigureModels(ctx, addModel("standard", "standard")); err != nil {
		t.Fatalf("models-only edit refused with an unfinished run: %v", err)
	}
	steps := tasks.Config().MaxSteps
	err = tasks.ConfigureModels(ctx, func(config *Config) error { config.MaxSteps = steps + 1; return nil })
	if !errors.Is(err, ErrActiveRun) {
		t.Fatalf("safety edit with an unfinished run error=%v", err)
	}
	if loaded, err := LoadConfig(data); err != nil || loaded.MaxSteps != steps || tasks.Config().MaxSteps != steps || loaded.Models["standard"].Provider != "echo" {
		t.Fatalf("configuration changed: loaded=%+v err=%v", loaded, err)
	}
}

func TestRemoveModelsPrunesReferences(t *testing.T) {
	workspace := t.TempDir()
	base := func() Config {
		config := DefaultConfig(workspace)
		for _, id := range []string{"a", "b", "c"} {
			if err := applyModel(&config, id, ModelConfig{Provider: "echo"}, false); err != nil {
				t.Fatal(err)
			}
		}
		config.Auto.Enabled = true
		config.Auto.Models = []string{"a", "b"}
		config.Auto.AssessmentModel = "b"
		config.DefaultSelection = "auto"
		config.Collaboration.Mode = "ask"
		config.Collaboration.Models = []string{"b"}
		if err := config.Validate(); err != nil {
			t.Fatal(err)
		}
		return config
	}
	same := func(got, want []string) bool {
		return strings.Join(got, "\n") == strings.Join(want, "\n")
	}

	config := base()
	changes, err := RemoveModels(&config, []string{"b", "b"}, "")
	want := []string{"models: removed b", "auto.models: removed b", "auto.assessment_model: cleared",
		"collaboration.models: removed b", "collaboration: off"}
	if err != nil || !same(changes, want) {
		t.Fatalf("changes=%q err=%v", changes, err)
	}
	if _, ok := config.Models["b"]; ok || !same(config.Auto.Models, []string{"a"}) || !config.Auto.Enabled ||
		config.Auto.AssessmentModel != "" || config.Collaboration.Mode != "off" || config.Collaboration.Models != nil ||
		config.DefaultModel != "a" || config.DefaultSelection != "auto" {
		t.Fatalf("after removing b: %+v", config)
	}

	// Emptying the explicit Auto list disables Auto instead of widening it.
	changes, err = RemoveModels(&config, []string{"a"}, "c")
	want = []string{"models: removed a", "auto.models: removed a", "auto: disabled", "default_model: c", "default_selection: c"}
	if err != nil || !same(changes, want) {
		t.Fatalf("changes=%q err=%v", changes, err)
	}
	if config.Auto.Enabled || config.Auto.Models != nil || config.DefaultModel != "c" || config.DefaultSelection != "c" {
		t.Fatalf("after removing a: %+v", config)
	}

	changes, err = RemoveModels(&config, []string{"c"}, "")
	if err != nil || !same(changes, []string{"models: removed c", "default_model: none"}) {
		t.Fatalf("changes=%q err=%v", changes, err)
	}
	if len(config.Models) != 0 || config.DefaultModel != "" || config.DefaultSelection != "" || config.Auto.Enabled {
		t.Fatalf("after removing every model: %+v", config)
	}

	for name, call := range map[string]func(*Config) error{
		"unknown":         func(c *Config) error { _, err := RemoveModels(c, []string{"missing"}, ""); return err },
		"empty":           func(c *Config) error { _, err := RemoveModels(c, nil, ""); return err },
		"default":         func(c *Config) error { _, err := RemoveModels(c, []string{"a"}, ""); return err },
		"removed default": func(c *Config) error { _, err := RemoveModels(c, []string{"a", "c"}, "c"); return err },
		"unknown default": func(c *Config) error { _, err := RemoveModels(c, []string{"a"}, "missing"); return err },
	} {
		config := base()
		if err := call(&config); err == nil {
			t.Errorf("%s: removal accepted", name)
		}
		if len(config.Models) != 3 || config.DefaultModel != "a" {
			t.Errorf("%s: rejected removal edited the configuration: %+v", name, config)
		}
	}
}

func TestOpenStoreReadOnlyWhileOwned(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	ctx := context.Background()
	data := service.Store.DataDir()
	id := conversation(t, service)
	if _, err := service.Run(ctx, id, "first", "visible while owned"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(ctx, data); !errors.Is(err, ErrOwnerActive) {
		t.Fatalf("second owner error=%v", err)
	}
	view, err := OpenStoreReadOnly(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	conversations, err := view.ListConversations(ctx)
	if err != nil || len(conversations) != 1 || conversations[0].ID != id {
		t.Fatalf("conversations=%+v err=%v", conversations, err)
	}
	messages, err := view.ListMessages(ctx, id)
	if err != nil || len(messages) != 2 || messages[0].Content != "visible while owned" {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if _, err := view.CreateConversation(ctx, "write", service.Config.Workspace); err == nil {
		t.Fatal("read-only store accepted a write")
	}
	if err := view.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing the view must leave the owner's lock in place.
	if _, err := OpenStore(ctx, data); !errors.Is(err, ErrOwnerActive) {
		t.Fatalf("owner lock after read-only close error=%v", err)
	}
	if _, err := service.Run(ctx, id, "second", "owner keeps writing"); err != nil {
		t.Fatal(err)
	}
}
