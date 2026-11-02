package main

import (
	"encoding/json"
	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestOneShotOnlineAbandonAndBackup(t *testing.T) {
	data, _ := initCLI(t)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"create","type":"function","function":{"name":"file_create","arguments":"{\"path\":\"pending.txt\",\"content\":\"waiting\"}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer model.Close()
	config, err := harness.LoadConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	id := fixtureKey(t, model.URL, harness.KeySourceNone, "", "")
	config.Models["default"] = harness.ModelConfig{Provider: "openai-compatible", Key: id, Model: "fixture"}
	enableTestProviders(&config)
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	startTestWeb(t, data)
	client, err := interactiveOwner(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	var runs []harness.RunResult
	for range 2 {
		raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json", "create pending")
		var result harness.RunResult
		if exitCode(err) != exitWaiting || json.Unmarshal([]byte(raw), &result) != nil {
			t.Fatalf("waiting %s %v", raw, err)
		}
		runs = append(runs, result)
	}
	for range 2 {
		if _, _, err := invoke(t, "", "abandon", "--data-dir", data, "--run", runs[0].Run.ID); err != nil {
			t.Fatal(err)
		}
	}
	second, _, _, err := client.Inspect(t.Context(), runs[1].Run.ID)
	if err != nil || second.Status != "waiting_confirmation" {
		t.Fatalf("other conversation affected: %+v %v", second, err)
	}
	target := filepath.Join(t.TempDir(), "online backup")
	if _, _, err := invoke(t, "", "backup", "--data-dir", data, "--out", target); err != nil {
		t.Fatal(err)
	}
	if err := harness.VerifyBackup(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "backup", "--data-dir", data, "--out", target); err == nil {
		t.Fatal("existing target accepted")
	}
	info, secret, _, err := handoff.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := handoff.NewClient(info, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := observer.ChatCommand(t.Context(), "abandon", "", second.ID, nil, nil); err == nil || !strings.Contains(err.Error(), "user_interaction_required") {
		t.Fatalf("observer mutation: %v", err)
	}
}
