package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/harness"
)

func invoke(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func initCLI(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	data := filepath.Join(base, "fresh data")
	workspace := filepath.Join(base, "project")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "init", "--data-dir", data, "--workspace", workspace); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "default", "--default", "--provider", "echo"); err != nil {
		t.Fatal(err)
	}
	return data, workspace
}

func TestCLIInitChatRestartAndJSONDeduplication(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json", "--request", "first", "hello")
	if err != nil {
		t.Fatal(err)
	}
	var first harness.RunResult
	if err := json.Unmarshal([]byte(raw), &first); err != nil || first.Answer != "Echo: hello" {
		t.Fatalf("output=%q error=%v", raw, err)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--session", first.Run.ConversationID, "--request", "first", "--json", "hello")
	if err != nil {
		t.Fatal(err)
	}
	var repeated harness.RunResult
	if err := json.Unmarshal([]byte(raw), &repeated); err != nil || !repeated.Duplicate || repeated.Run.ID != first.Run.ID {
		t.Fatalf("duplicate=%+v error=%v", repeated, err)
	}
	raw, _, err = invoke(t, "second message\n/history\n/exit\n", "chat", "--data-dir", data, "--resume", first.Run.ConversationID)
	if err != nil || !strings.Contains(raw, "Echo: second message") || !strings.Contains(raw, "user: hello") {
		t.Fatalf("chat output=%q error=%v", raw, err)
	}
	raw, _, err = invoke(t, "", "sessions", "--data-dir", data)
	if err != nil || !strings.Contains(raw, first.Run.ConversationID) {
		t.Fatalf("sessions output=%q error=%v", raw, err)
	}
}

func TestCLIToolsAndDoctorAreOfflineWithoutCredential(t *testing.T) {
	data, workspace := initCLI(t)
	key := fixtureKey(t, "https://127.0.0.1:1", harness.KeySourceEnv, "ABSENT_HARNESS_KEY", "")
	config := cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "fixture"})
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"doctor", "--data-dir", data},
		{"tools", "list", "--data-dir", data},
		{"sessions", "--data-dir", data},
	} {
		_, _, err := invoke(t, "", args...)
		if err != nil {
			t.Fatalf("offline command=%v error=%v", args, err)
		}
	}
}

func TestCLIWriteWaitsThenShowsDiffAndContinues(t *testing.T) {
	data, workspace := initCLI(t)
	original := []byte("before")
	_ = os.WriteFile(filepath.Join(workspace, "notes.txt"), original, 0o600)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"edit","type":"function","function":{"name":"file_edit","arguments":"{\"path\":\"notes.txt\",\"expected_sha256\":\"6db7d803e74f1ffa7d8f5adc0bf95b3e15bf4c8373fffadf546227cc6c6742cb\",\"old_text\":\"before\",\"new_text\":\"after\"}"}}]},"finish_reason":"tool_calls"}]}`))
		} else {
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Changed."},"finish_reason":"stop"}]}`))
		}
	}))
	defer server.Close()
	key := fixtureKey(t, server.URL, harness.KeySourceSaved, "", "fixture-key")
	config := cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "fixture"})
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	raw, stderr, err := invoke(t, "", "run", "--data-dir", data, "--json", "change notes.txt")
	if exitCode(err) != exitWaiting {
		t.Fatalf("waiting run exit=%d err=%v", exitCode(err), err)
	}
	var pending harness.RunResult
	if err := json.Unmarshal([]byte(raw), &pending); err != nil || pending.Run.Status != "waiting_confirmation" {
		t.Fatalf("run=%+v stderr=%q error=%v", pending, stderr, err)
	}
	before, _ := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if string(before) != "before" || calls != 1 {
		t.Fatal("file modified before confirmation")
	}
	configOutput, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--id", "next", "--default", "--provider", "echo")
	if err != nil || configOutput == "" {
		t.Fatalf("registering a future model was blocked: output=%q error=%v", configOutput, err)
	}
	configAfter, err := harness.LoadConfig(data)
	if err != nil || configAfter.DefaultModel != "next" || configAfter.Models["default"] != config.Models["default"] || calls != 1 {
		t.Fatal("future model registration changed the bound model or made a model call")
	}
	raw, stderr, err = invoke(t, "y\n", "continue", "--data-dir", data, "--run", pending.Run.ID, "--interactive", "--json")
	if err != nil || !strings.Contains(stderr, "before") || !strings.Contains(stderr, "after") {
		t.Fatalf("continue output=%q stderr=%q error=%v", raw, stderr, err)
	}
	after, _ := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if string(after) != "after" || calls != 2 {
		t.Fatalf("edit result=%q calls=%d", after, calls)
	}
	var finished harness.RunResult
	if err := json.Unmarshal([]byte(raw), &finished); err != nil || finished.Run.ModelID != "default" {
		t.Fatalf("pending run was redirected: output=%q error=%v", raw, err)
	}
	raw, _, err = invoke(t, "", "run", "--data-dir", data, "--session", pending.Run.ConversationID, "--json", "next task")
	if err != nil || !strings.Contains(raw, "Echo: next task") || calls != 2 {
		t.Fatalf("next task did not use the new default: output=%q error=%v calls=%d", raw, err, calls)
	}
	if _, _, err := invoke(t, "", "config", "model", "--data-dir", data, "--provider", "echo"); err != nil {
		t.Fatalf("completed edit still blocked model change: %v", err)
	}
}

func TestCLIHelpExcludesRetiredCommandsAndDependencyClosure(t *testing.T) {
	raw, _, err := invoke(t, "", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"config model --id", "[--default]", "[--model <id>]", "--max-steps", "--max-seconds", "/model", "/more"} {
		if !strings.Contains(raw, command) {
			t.Errorf("current command missing from help: %s", command)
		}
	}
	for _, command := range []string{"module-", "migrate", "serve", "learning-", "s3-"} {
		if strings.Contains(raw, "\n"+command) {
			t.Fatalf("retired command remains: %s", command)
		}
		if _, _, err := invoke(t, "", command); err == nil {
			t.Fatalf("retired command accepted: %s", command)
		}
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Skip("Go executable is not in PATH")
	}
	command := exec.Command(goPath, "list", "-deps", ".")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"/currentstore", "/coreloop", "/localchat", "/control", "/module", "/wasm", "/remoteaction", "/learning", "/sdk/"} {
		if bytes.Contains(output, []byte(retired)) {
			t.Errorf("production import closure still contains %s", retired)
		}
	}
}

func TestCLIBlockedTerminalReadCancels(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var stdout, stderr bytes.Buffer
	data, _ := initCLI(t)
	start := time.Now()
	if err := run(ctx, []string{"chat", "--data-dir", data}, reader, &stdout, &stderr); err == nil || time.Since(start) > time.Second {
		t.Fatalf("terminal input did not cancel: error=%v duration=%s", err, time.Since(start))
	}
}

func TestTerminalApprovalTimeoutKeepsSingleReader(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	lifetime, stop := context.WithCancel(context.Background())
	defer stop()
	input := newTerminalInput(lifetime, reader)
	ctx, cancel := context.WithTimeout(lifetime, 20*time.Millisecond)
	defer cancel()
	if _, err := input.read(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting read error=%v", err)
	}
	written := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, "first line\nsecond line\n")
		written <- err
	}()
	for _, expected := range []string{"first line\n", "second line\n"} {
		ctx, cancel := context.WithTimeout(lifetime, time.Second)
		line, err := input.read(ctx)
		cancel()
		if err != nil || line != expected {
			t.Fatalf("read after timeout=%q error=%v expected=%q", line, err, expected)
		}
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}

func TestInitWithoutModel(t *testing.T) {
	base := t.TempDir()
	data, workspace := filepath.Join(base, "data"), filepath.Join(base, "project")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, stderr, err := invoke(t, "", "init", "--data-dir", data, "--workspace", workspace)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if json.Unmarshal([]byte(raw), &output) != nil || len(output) != 2 || output["data_dir"] != data || output["workspace"] != workspace {
		t.Fatalf("init stdout=%q", raw)
	}
	if !strings.Contains(stderr, "key add") || !strings.Contains(stderr, "config model") {
		t.Fatalf("init stderr=%q", stderr)
	}
	config, err := harness.LoadConfig(data)
	if err != nil || len(config.Models) != 0 || config.DefaultModel != "" {
		t.Fatalf("config=%+v err=%v", config, err)
	}
}

func TestRunWithoutModelExit2(t *testing.T) {
	data, _ := initEmptyCLI(t)
	for _, args := range [][]string{
		{"run", "--data-dir", data, "hello"},
		{"continue", "--data-dir", data, "--run", "r-missing"},
		{"chat", "--data-dir", data},
	} {
		raw, stderr, err := invoke(t, "", args...)
		if exitCode(err) != exitUsage || raw != "" || !strings.Contains(stderr, "key add") {
			t.Fatalf("%v exit=%d stdout=%q stderr=%q err=%v", args, exitCode(err), raw, stderr, err)
		}
	}
	raw, _, err := invoke(t, "", "sessions", "--data-dir", data)
	if err != nil || strings.TrimSpace(raw) != "[]" {
		t.Fatalf("no model run left a session: %q err=%v", raw, err)
	}
}

func TestDoctorShowsKeysPath(t *testing.T) {
	data, _ := initCLI(t)
	name := "doctor " + t.Name()
	if _, _, err := invoke(t, "", "key", "add", "--name", name, "--url", "http://127.0.0.1:9/v1", "--no-key"); err != nil {
		t.Fatal(err)
	}
	raw, _, err := invoke(t, "", "doctor", "--data-dir", data)
	var report struct {
		OK        bool             `json:"ok"`
		KeysFile  string           `json:"keys_file"`
		Encrypted *bool            `json:"encrypted"`
		Keys      []map[string]any `json:"keys"`
	}
	if err != nil || json.Unmarshal([]byte(raw), &report) != nil {
		t.Fatalf("doctor=%q err=%v", raw, err)
	}
	path, _ := apppaths.KeysFile()
	if !report.OK || report.KeysFile != path || report.Encrypted == nil || *report.Encrypted != harness.KeysEncrypted() {
		t.Fatalf("doctor report=%+v", report)
	}
	found := false
	for _, key := range report.Keys {
		if key["name"] == name {
			found = key["source"] == harness.KeySourceNone && key["last_check"] == nil
		}
	}
	if !found {
		t.Fatalf("doctor keys=%v", report.Keys)
	}
}

// holdOwner keeps the data directory owned by this test, the way an open
// desktop app would.
func holdOwner(t *testing.T, data string) {
	t.Helper()
	store, err := harness.OpenStore(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
}

func TestSessionsReadOnlyWhileLocked(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json", "hello")
	var first harness.RunResult
	if err != nil || json.Unmarshal([]byte(raw), &first) != nil {
		t.Fatalf("run=%q err=%v", raw, err)
	}
	holdOwner(t, data)
	for _, check := range []struct {
		args []string
		want string
	}{
		{[]string{"sessions", "--data-dir", data}, first.Run.ConversationID},
		{[]string{"sessions", "--data-dir", data, "--show", first.Run.ConversationID}, "Echo: hello"},
		{[]string{"inspect", "--data-dir", data, "--run", first.Run.ID}, first.Run.ID},
	} {
		raw, stderr, err := invoke(t, "", check.args...)
		if err != nil || !strings.Contains(stderr, "showing a read-only view") {
			t.Fatalf("%v stderr=%q err=%v", check.args, stderr, err)
		}
		if !strings.Contains(raw, check.want) {
			t.Fatalf("%v output=%q", check.args, raw)
		}
	}
}

func TestWriteCommandLockMessage(t *testing.T) {
	data, _ := initCLI(t)
	raw, _, err := invoke(t, "", "run", "--data-dir", data, "--json", "hello")
	var first harness.RunResult
	if err != nil || json.Unmarshal([]byte(raw), &first) != nil {
		t.Fatalf("run=%q err=%v", raw, err)
	}
	holdOwner(t, data)
	for _, args := range [][]string{
		{"abandon", "--data-dir", data, "--run", first.Run.ID},
		{"run", "--data-dir", data, "hello again"},
		{"config", "model", "--data-dir", data, "--id", "other", "--provider", "echo"},
		{"backup", "--data-dir", data, "--out", filepath.Join(t.TempDir(), "backup")},
	} {
		_, _, err := invoke(t, "", args...)
		if exitCode(err) != exitFailure || !errors.Is(err, harness.ErrOwnerActive) {
			t.Fatalf("%v exit=%d err=%v", args, exitCode(err), err)
		}
		var stderr bytes.Buffer
		reportError(&stderr, err)
		if strings.TrimSpace(stderr.String()) != ownerActiveMessage {
			t.Fatalf("%v message=%q", args, stderr.String())
		}
	}
}
