package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestDefaultEntryMultilineReferencesAndSessionSelection(t *testing.T) {
	data, workspace := initCLI(t)
	if err := os.WriteFile(filepath.Join(workspace, "context.txt"), []byte("explicit-source-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, diagnostics, err := invoke(t,
		"/file context.txt\n/begin\nfirst line\nsecond line\n/end\n/status\n/new\nsecond task\n/sessions\n/resume 2\n/history\n/exit\n",
		"--data-dir", data)
	if err != nil || !strings.Contains(output, "first line\nsecond line") ||
		!strings.Contains(output, "explicit-source-marker") || !strings.Contains(output, "1. Interactive chat") {
		t.Fatalf("output=%s diagnostics=%s err=%v", output, diagnostics, err)
	}
	store, err := harness.OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sessions, _ := store.ListConversations(t.Context())
	if len(sessions) != 2 {
		t.Fatalf("sessions=%+v", sessions)
	}
	messages, _ := store.ListMessages(t.Context(), sessions[1].ID)
	if len(messages) != 2 || !strings.Contains(messages[0].Content, `"sha256"`) {
		t.Fatalf("source not saved: %+v", messages)
	}
}

func TestChatCancellationCancelsTaskWithoutEndingSession(t *testing.T) {
	lifetime, stop := context.WithCancel(t.Context())
	defer stop()
	task, cancelTask := context.WithCancel(lifetime)
	controller := &chatCancellation{idle: stop}
	controller.set(cancelTask)
	controller.cancel()
	if task.Err() == nil || lifetime.Err() != nil {
		t.Fatal("task cancellation ended session or did not cancel")
	}
	controller.set(nil)
	controller.cancel()
	if lifetime.Err() == nil {
		t.Fatal("idle cancellation did not end session")
	}
}

func TestCLICommandPolicyConfiguredWithoutJSONEditing(t *testing.T) {
	data, _ := initCLI(t)
	output, _, err := invoke(t, "", "config", "commands", "--data-dir", data, "--enabled", "--timeout", "10", "--output-bytes", "4096")
	if err != nil || !strings.Contains(output, `"enabled": true`) {
		t.Fatalf("output=%s err=%v", output, err)
	}
	config, err := harness.LoadConfig(data)
	if err != nil || !config.Commands.Enabled || config.Commands.InheritEnv || config.Commands.TimeoutSecs != 10 {
		t.Fatalf("config=%+v err=%v", config.Commands, err)
	}
	if _, _, err := invoke(t, "", "config", "commands", "--data-dir", data); err != nil {
		t.Fatal(err)
	}
	config, _ = harness.LoadConfig(data)
	if config.Commands.Enabled {
		t.Fatal("command disable did not take effect")
	}
}
