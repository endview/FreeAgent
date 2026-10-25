package main

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

func TestCLICommandHelper(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--cli-command-helper" {
			fmt.Println("CLI_READY")
			time.Sleep(20 * time.Second)
			os.Exit(0)
		}
	}
}

func commandCLIConfig(t *testing.T, full bool) string {
	t.Helper()
	data, work := initCLI(t)
	config := harness.DefaultConfig(work)
	config.Models = map[string]harness.ModelConfig{"fixture": {Provider: "echo"}}
	config.DefaultModel = "fixture"
	program, _ := os.Executable()
	config.Commands.Enabled = true
	config.Tools.CommandRun = true
	config.Commands.Rules = []harness.CommandRule{{Program: program, Decision: harness.CommandRuleAllow}}
	config.Commands.Services.AllowLocalListen = true
	if !full {
		config.Commands.Rules[0].Decision = harness.CommandRuleAsk
	}
	if full {
		config.Execution.PermissionMode = harness.PermissionFull
	}
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	return data
}
func TestCommandsCLIUsesInteractiveOwnerAndExactExtension(t *testing.T) {
	data := commandCLIConfig(t, true)
	_, _, _ = startTestWeb(t, data)
	client, err := interactiveOwner(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := client.NewConversation(t.Context(), "CLI service")
	if err != nil {
		t.Fatal(err)
	}
	program, _ := os.Executable()
	arguments := fmt.Sprintf(`{"command":%q,"args":["-test.run=TestCLICommandHelper","--","--cli-command-helper"],"cwd":".","mode":"service","duration_seconds":15}`, program)
	raw, diagnostic, err := invoke(t, "", "services", "start", "--data-dir", data, "--session", chat.ID, "--request", "start", "--arguments", arguments, "--json")
	var result harness.RunResult
	if err != nil || json.Unmarshal([]byte(raw), &result) != nil || result.Run.Status != "completed" {
		t.Fatalf("start %s %s %v", raw, diagnostic, err)
	}
	var record harness.CommandRecord
	if json.Unmarshal([]byte(result.Answer), &record) != nil {
		t.Fatal("no command")
	}
	expires, _ := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	raw, _, err = invoke(t, "", "services", "extend", "--data-dir", data, "--id", record.ID, "--revision", fmt.Sprint(record.Revision), "--expires-at", expires.Add(time.Second).Format(time.RFC3339Nano), "--request", "extend", "--json")
	if err != nil {
		t.Fatalf("extend %s %v", raw, err)
	}
	if _, err := client.StopCommand(t.Context(), record.ID); err != nil {
		t.Fatal(err)
	}
	raw, _, err = invoke(t, "", "commands", "poll", "--data-dir", data, "--id", record.ID, "--json")
	if err != nil || json.Unmarshal([]byte(raw), &record) != nil || record.State != "stopped" {
		t.Fatalf("same owner Stop missing %s %v", raw, err)
	}
}
func TestCommandsCLIJSONPendingKeepsWaitingExitCode(t *testing.T) {
	data := commandCLIConfig(t, false)
	_, _, _ = startTestWeb(t, data)
	client, err := interactiveOwner(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := client.NewConversation(t.Context(), "Ask command")
	if err != nil {
		t.Fatal(err)
	}
	program, _ := os.Executable()
	arguments := fmt.Sprintf(`{"command":%q,"args":["-test.run=TestCLICommandHelper","--","--cli-command-helper"],"cwd":".","background":true}`, program)
	raw, _, err := invoke(t, "", "commands", "start", "--data-dir", data, "--session", chat.ID, "--request", "ask", "--arguments", arguments, "--json")
	var result harness.RunResult
	if json.Unmarshal([]byte(raw), &result) != nil || result.Run.Status != "waiting_confirmation" || exitCode(err) != exitWaiting {
		t.Fatalf("waiting exit %s %v", raw, err)
	}
}
