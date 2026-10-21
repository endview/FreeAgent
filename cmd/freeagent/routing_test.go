package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestCLIAutoOptInSelectionAndManualDefault(t *testing.T) {
	data, _ := initCLI(t)
	for _, args := range [][]string{
		{"config", "model", "--data-dir", data, "--provider", "echo", "--strength", "strong"},
		{"config", "model", "--data-dir", data, "--id", "light", "--provider", "echo", "--strength", "light"},
		{"config", "auto", "--data-dir", data, "--enabled", "--allow-model", "default", "--allow-model", "light"},
	} {
		raw, _, err := invoke(t, "", args...)
		if err != nil || !strings.Contains(raw, `"network_tested": false`) {
			t.Fatalf("offline configuration: %v output=%q err=%v", args, raw, err)
		}
	}
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--model", "auto", "--json", "hello")
	var automatic harness.RunResult
	if err != nil || json.Unmarshal([]byte(raw), &automatic) != nil || automatic.Run.ModelID != "light" ||
		len(automatic.Run.Routing) != 1 || automatic.Run.Routing[0].Mode != "auto" {
		t.Fatalf("Auto task=%q err=%v", raw, err)
	}
	raw, _, err = invoke(t, "/model auto\nhello again\n/model default\narchitecture\n/exit\n",
		"chat", "--data-dir", data, "--resume", automatic.Run.ConversationID)
	if err != nil || !strings.Contains(raw, "Echo: hello again") || !strings.Contains(raw, "Echo: architecture") {
		t.Fatalf("Chat Auto/manual=%q err=%v", raw, err)
	}
	if _, _, err := invoke(t, "", "config", "auto", "--data-dir", data, "--enabled", "--default"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "light", "--default", "--provider", "echo"); err != nil {
		t.Fatal(err)
	}
	config, err := harness.LoadConfig(data)
	if err != nil || config.DefaultSelection != "light" {
		t.Fatalf("manual default did not replace Auto: %+v err=%v", config, err)
	}
	if _, _, err := invoke(t, "", "config", "auto", "--data-dir", data, "--enabled=false"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "run", "--data-dir", data, "--model", "auto", "hello"); !errors.Is(err, harness.ErrAutoDisabled) {
		t.Fatalf("disabled Auto accepted: %v", err)
	}
}

func TestCLIAutoRejectsUnboundedAndUnauthorizedConfiguration(t *testing.T) {
	data, _ := initCLI(t)
	for _, flags := range [][]string{
		{"--enabled", "--max-candidates", "0"},
		{"--enabled", "--max-assessments", "2"},
		{"--enabled", "--max-upgrades", "3"},
		{"--enabled", "--allow-model", "absent"},
		{"--enabled", "--allow-model", "default", "--allow-model", "default"},
		{"--enabled", "--assessment-model", "absent"},
		{"--enabled=false", "--default"},
	} {
		args := append([]string{"config", "auto", "--data-dir", data}, flags...)
		raw, _, err := invoke(t, "", args...)
		if err == nil || raw != "" {
			t.Fatalf("invalid Auto configuration accepted: %v output=%q err=%v", flags, raw, err)
		}
		config, err := harness.LoadConfig(data)
		if err != nil || config.Auto.Enabled {
			t.Fatalf("invalid edit changed policy: %+v err=%v", config.Auto, err)
		}
	}
}
