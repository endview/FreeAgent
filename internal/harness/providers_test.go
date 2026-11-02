package harness

import (
	"errors"
	"sync/atomic"
	"testing"
)

func TestProviderDisabledDoesNotResolveSecretOrCreateAdapter(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	keys := testKeyStore(t)
	id := addTestProfile(t, keys, "https://api.example.com/v1", KeySourceEnv, "FREEAGENT_ABSENT_PROVIDER_TEST", "")
	service.Keys = keys
	model := ModelConfig{Provider: "openai-compatible", Key: id, Model: "test"}
	service.Config.Models["remote"] = model
	var adapters atomic.Int32
	service.ModelFor = func(ModelConfig) (ModelClient, error) { adapters.Add(1); return EchoModel{}, nil }
	if _, err := service.newBinding(service.Config, "remote"); !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("disabled binding: %v", err)
	}
	binding, _ := modelBinding(service.Config, "remote")
	service.bindings = []ModelBinding{binding}
	if _, err := service.modelClient(); !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("disabled dispatch: %v", err)
	}
	if adapters.Load() != 0 {
		t.Fatal("disabled Provider reached adapter factory")
	}
}

func TestProviderToggleAppliesToFrozenRunWithoutChangingSnapshot(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	keys := testKeyStore(t)
	id := addTestProfile(t, keys, "http://127.0.0.1:19321/v1", KeySourceNone, "", "")
	core.Keys = keys
	core.Config.Models["remote"] = ModelConfig{Provider: "openai-compatible", Key: id, Model: "fixture"}
	core.Config.Providers = map[string]bool{id: true}
	tasks := NewTaskService(core)
	bound, err := core.forSelection("remote")
	if err != nil {
		t.Fatal(err)
	}
	before, err := bound.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = control.ApplyDirectControlChange(t.Context(), ControlSetProviderEnabled, map[string]any{"id": id, "enabled": false}, "", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err = bound.modelClient(); !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("frozen Run ignored live disable: %v", err)
	}
	after, err := bound.snapshot()
	if err != nil || before != after {
		t.Fatalf("toggle changed frozen task: %v", err)
	}
	if _, err = control.ApplyDirectControlChange(t.Context(), ControlSetProviderEnabled, map[string]any{"id": "k-deadbeef", "enabled": true}, "", "test"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("nonexistent Provider: %v", err)
	}
}
