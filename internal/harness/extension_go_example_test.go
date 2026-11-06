package harness

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGoContextExampleThroughActualCore(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	core, _, workspace := testHarness(t, EchoModel{})
	id := "com.example.go-context"
	root := filepath.Join(core.Store.DataDir(), "extensions", id)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	executable := filepath.Join(root, "go-context"+suffix)
	command := exec.CommandContext(t.Context(), filepath.Join(runtime.GOROOT(), "bin", "go"+suffix), "build", "-trimpath", "-ldflags=-s -w", "-o", executable, ".")
	command.Dir = filepath.Join(repo, "extension-kit", "examples", "go-context")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build Go example: %s %v", output, err)
	}
	raw, err := os.ReadFile(filepath.Join(command.Dir, "extension.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if json.Unmarshal(raw, &manifest) != nil {
		t.Fatal("invalid sample manifest")
	}
	manifest["run"] = map[string]any{"command": "./go-context" + suffix}
	if err := os.WriteFile(filepath.Join(root, "extension.json"), mustJSON(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	core.Config.Extensions.Enabled = map[string]bool{id: true}
	core.capabilities = &CapabilitySelection{Extensions: []string{id}}
	core.extensions = newExtensionHost(core.Store.DataDir(), workspace, core.Config, nil)
	t.Cleanup(func() { _ = core.extensions.close() })
	core.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "GO_CONTEXT_READY") {
				return ModelResponse{Content: "Go extension accepted"}, nil
			}
		}
		t.Error("Go block did not reach the real model request")
		return ModelResponse{}, nil
	})
	result, err := NewTaskService(core).Submit(t.Context(), conversation(t, core), "go-example", "Check the Go context example")
	if err != nil || result.Run.Status != "completed" || result.Answer != "Go extension accepted" {
		t.Fatalf("actual Go extension: status=%s err=%v", result.Run.Status, err)
	}
}
