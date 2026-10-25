package chatui

import (
	"encoding/json"
	"fmt"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandOwnerAPIObservationCannotMutate(t *testing.T) {
	ui := setupUI(t, nil)
	client, observer := contextClient(t, ui, true), contextClient(t, ui, false)
	if records, err := observer.Commands(t.Context()); err != nil || len(records) != 0 {
		t.Fatalf("list %+v %v", records, err)
	}
	if _, err := observer.StopCommand(t.Context(), "missing"); err == nil {
		t.Fatal("observer allowed stop")
	}
	chat := ui.create(t)
	if _, err := observer.StartCommand(t.Context(), chat, "start", json.RawMessage(`{"command":"missing","cwd":".","background":true}`)); err == nil {
		t.Fatal("observer allowed start")
	}
	if _, err := client.PollCommand(t.Context(), "missing", 0, 16384); err == nil {
		t.Fatal("missing command reported success")
	}
	code, _ := ui.request(t, http.MethodGet, "/api/attach/commands?cursor=-1&id=missing", nil)
	if code == 200 {
		t.Fatal("invalid cursor accepted")
	}
}

func TestUICommandHelper(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--ui-command-helper" {
			fmt.Println("UI_READY")
			time.Sleep(20 * time.Second)
			os.Exit(0)
		}
	}
}

func TestCommandGUIAndAttachedClientShareServiceAndHandoff(t *testing.T) {
	root := t.TempDir()
	data, work := filepath.Join(root, "data"), filepath.Join(root, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	if err := harness.InitStore(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	program, _ := os.Executable()
	config := harness.DefaultConfig(work)
	config.Models = map[string]harness.ModelConfig{"fixture": {Provider: "echo"}}
	config.DefaultModel = "fixture"
	config.Commands.Enabled = true
	config.Tools.CommandRun = true
	config.Commands.Rules = []harness.CommandRule{{Program: program, Decision: harness.CommandRuleAllow}}
	config.Commands.Services.AllowLocalListen = true
	ui := setupUIWithConfig(t, harness.EchoModel{}, data, work, config)
	chat := ui.create(t)
	if err := ui.tasks.SelectPermission(t.Context(), chat, harness.PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	client, observer := contextClient(t, ui, true), contextClient(t, ui, false)
	args := json.RawMessage(fmt.Sprintf(`{"command":%q,"args":["-test.run=TestUICommandHelper","--","--ui-command-helper"],"cwd":".","mode":"service","duration_seconds":15}`, program))
	result, err := client.StartCommand(t.Context(), chat, "start-service", args)
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("start %+v %v", result, err)
	}
	var record harness.CommandRecord
	if json.Unmarshal([]byte(result.Answer), &record) != nil {
		t.Fatal("no startup projection")
	}
	code, raw := ui.request(t, http.MethodGet, "/api/attach/commands", nil)
	var records []harness.CommandRecord
	if code != 200 || json.Unmarshal(raw, &records) != nil || len(records) != 1 || records[0].ID != record.ID || records[0].Generation != ui.server.generation {
		t.Fatalf("GUI/CLI mismatch %d %s", code, raw)
	}
	expires, _ := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	request := harness.CommandExtension{ID: record.ID, Revision: record.Revision, ExpiresAt: expires.Add(time.Second).Format(time.RFC3339Nano), RequestKey: "extend"}
	if _, err := observer.ExtendCommand(t.Context(), request); err == nil {
		t.Fatal("observer extended service")
	}
	if _, err := client.ExtendCommand(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	code, raw = ui.request(t, http.MethodGet, "/api/chats/"+chat, nil)
	var snapshot struct {
		Busy       bool `json:"busy"`
		Background bool `json:"background_active"`
	}
	if code != 200 || json.Unmarshal(raw, &snapshot) != nil || snapshot.Busy || !snapshot.Background {
		t.Fatalf("background blocked conversation %d %s", code, raw)
	}
	code, _ = ui.request(t, http.MethodPost, "/api/attach/handoff", map[string]bool{"cancel_active": false})
	if code != 409 {
		t.Fatalf("handoff lost live service %d", code)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/attach/handoff", map[string]bool{"cancel_active": true})
	if code != 202 {
		t.Fatalf("handoff failed %d %s", code, raw)
	}
	final, err := client.PollCommand(t.Context(), record.ID, 0, 16384)
	if err != nil || final.State != "stopped" || final.Reason != "owner_handoff" {
		t.Fatalf("handoff did not stop service %+v %v", final, err)
	}
}
