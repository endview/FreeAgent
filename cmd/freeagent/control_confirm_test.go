package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

var shownControlCode = regexp.MustCompile(`Confirmation code: ([0-9A-Z]{5}-[0-9A-Z]{5})`)

// controlProposalCLI prepares a data directory whose model proposes switching
// to workspace access once and answers plainly on any later call.
func controlProposalCLI(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	data, workspace := initCLI(t)
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"control","type":"function","function":{"name":"control_plan_change","arguments":"{\"kind\":\"execution.set_permission\",\"arguments\":{\"mode\":\"workspace\"}}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Kept the current permission."},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)
	key := fixtureKey(t, server.URL, harness.KeySourceNone, "", "")
	if err := harness.WriteConfig(data, cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "fixture"})); err != nil {
		t.Fatal(err)
	}
	if permissionMode(t, data) == harness.PermissionWorkspace {
		t.Fatal("the fixture already uses workspace access")
	}
	return data, calls
}

func permissionMode(t *testing.T, data string) harness.PermissionMode {
	t.Helper()
	config, err := harness.LoadConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	return config.Execution.PermissionMode
}

// waitingControl leaves one Run waiting on the fixture's proposal.
func waitingControl(t *testing.T, data string) (harness.RunResult, harness.Confirmation) {
	t.Helper()
	raw, stderr, err := invoke(t, "", "run", "--data-dir", data, "--json", "use workspace access")
	var pending harness.RunResult
	if exitCode(err) != exitWaiting || json.Unmarshal([]byte(raw), &pending) != nil || pending.Run.Status != "waiting_confirmation" {
		t.Fatalf("run exit=%d raw=%q stderr=%q err=%v", exitCode(err), raw, stderr, err)
	}
	if !strings.Contains(stderr, "Setting change waiting: freeagent preview --run "+pending.Run.ID) {
		t.Fatalf("missing setting-change next step: %q", stderr)
	}
	confirmation := previewJSON(t, data, pending.Run.ID)
	if confirmation.ControlOperationID == "" || confirmation.Tool != "control_plan_change" {
		t.Fatalf("confirmation=%+v", confirmation)
	}
	return pending, confirmation
}

func decodeRun(t *testing.T, raw, stderr string, err error) harness.RunResult {
	t.Helper()
	var result harness.RunResult
	if err != nil || json.Unmarshal([]byte(raw), &result) != nil {
		t.Fatalf("raw=%q stderr=%q err=%v", raw, stderr, err)
	}
	return result
}

// lockedBuffer lets a test reader watch prompts while the command writes.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (locked *lockedBuffer) Write(raw []byte) (int, error) {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.buffer.Write(raw)
}

func (locked *lockedBuffer) String() string {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.buffer.String()
}

// codeTyper answers the CLI prompt with each line in before, then types the
// code the command showed, in lowercase with its separator.
type codeTyper struct {
	prompts *lockedBuffer
	before  []string
	typed   bool
}

func (typer *codeTyper) Read(buffer []byte) (int, error) {
	if len(typer.before) != 0 {
		line := typer.before[0] + "\n"
		typer.before = typer.before[1:]
		return copy(buffer, line), nil
	}
	if typer.typed {
		return 0, io.EOF
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if match := shownControlCode.FindStringSubmatch(typer.prompts.String()); match != nil {
			typer.typed = true
			return copy(buffer, strings.ToLower(match[1])+"\n"), nil
		}
	}
	return 0, errors.New("the command never showed a confirmation code")
}

func assertCodeNotIn(t *testing.T, output, code string) {
	t.Helper()
	if strings.Contains(output, code) || strings.Contains(output, strings.ReplaceAll(code, "-", "")) {
		t.Fatal("the confirmation code reached the Run output")
	}
}

func TestCLIInteractiveRunShowsAndAppliesControlCode(t *testing.T) {
	data, calls := controlProposalCLI(t)
	raw, stderr, err := invoke(t, "y\n", "run", "--data-dir", data, "--interactive", "--json", "use workspace access")
	result := decodeRun(t, raw, stderr, err)
	match := shownControlCode.FindStringSubmatch(stderr)
	if match == nil || !strings.Contains(stderr, "Type y or the code") {
		t.Fatalf("the prompt did not show a code: %q", stderr)
	}
	if result.Run.Status != "completed" || result.Run.StopReason != "control_change_applied_next_run" || calls.Load() != 1 {
		t.Fatalf("status=%s reason=%s calls=%d", result.Run.Status, result.Run.StopReason, calls.Load())
	}
	if permissionMode(t, data) != harness.PermissionWorkspace {
		t.Fatal("the confirmed change was not applied")
	}
	assertCodeNotIn(t, raw, match[1])
}

func TestCLIInteractiveRunDeclinesControlProposalOnEnter(t *testing.T) {
	data, calls := controlProposalCLI(t)
	before := permissionMode(t, data)
	raw, stderr, err := invoke(t, "\n", "run", "--data-dir", data, "--interactive", "--json", "use workspace access")
	result := decodeRun(t, raw, stderr, err)
	if !shownControlCode.MatchString(stderr) || result.Run.Status != "completed" || calls.Load() != 2 {
		t.Fatalf("status=%s calls=%d stderr=%q", result.Run.Status, calls.Load(), stderr)
	}
	if permissionMode(t, data) != before {
		t.Fatal("a declined proposal changed the permission")
	}
}

func TestCLIConfirmAsksForControlCodeAfterPreview(t *testing.T) {
	data, calls := controlProposalCLI(t)
	before := permissionMode(t, data)
	pending, confirmation := waitingControl(t, data)
	id, digest := pending.Run.ID, confirmation.Digest

	text, hint, err := invoke(t, "", "preview", "--data-dir", data, "--run", id)
	if err != nil || !strings.Contains(text, "Setting change: "+confirmation.ControlOperationID) ||
		!strings.Contains(hint, "--approve (it shows a confirmation code") || shownControlCode.MatchString(hint) {
		t.Fatalf("preview=%q hint=%q err=%v", text, hint, err)
	}
	// A code lives only in the process that showed it.
	if _, _, err := invoke(t, "", "confirm", "--data-dir", data, "--run", id, "--digest", digest, "--approve", "--code", "ABCDE-FGHJK"); exitCode(err) != exitUsage {
		t.Fatalf("stale code exit=%d err=%v", exitCode(err), err)
	}
	_, stderr, err := invoke(t, "", "confirm", "--data-dir", data, "--run", id, "--digest", digest, "--approve")
	if exitCode(err) != exitWaiting || !shownControlCode.MatchString(stderr) || !strings.Contains(stderr, "No answer was given") {
		t.Fatalf("unanswered exit=%d stderr=%q err=%v", exitCode(err), stderr, err)
	}
	if permissionMode(t, data) != before || previewJSON(t, data, id).Digest != digest {
		t.Fatal("an unanswered prompt changed the proposal")
	}

	prompts := &lockedBuffer{}
	var stdout bytes.Buffer
	err = run(t.Context(), []string{"confirm", "--data-dir", data, "--run", id, "--digest", digest, "--approve", "--json"},
		&codeTyper{prompts: prompts, before: []string{"BADBADBAD1"}}, &stdout, prompts)
	result := decodeRun(t, stdout.String(), prompts.String(), err)
	if !strings.Contains(prompts.String(), "That code does not match; try again.") {
		t.Fatalf("a wrong code was not asked again: %q", prompts.String())
	}
	if result.Run.Status != "completed" || permissionMode(t, data) != harness.PermissionWorkspace || calls.Load() != 1 {
		t.Fatalf("status=%s permission=%s calls=%d", result.Run.Status, permissionMode(t, data), calls.Load())
	}
}

func TestCLIConfirmWithoutApproveDeclinesControlProposal(t *testing.T) {
	data, calls := controlProposalCLI(t)
	before := permissionMode(t, data)
	pending, confirmation := waitingControl(t, data)
	raw, stderr, err := invoke(t, "", "confirm", "--data-dir", data, "--run", pending.Run.ID, "--digest", confirmation.Digest, "--json")
	result := decodeRun(t, raw, stderr, err)
	if result.Run.Status != "completed" || calls.Load() != 2 || permissionMode(t, data) != before {
		t.Fatalf("status=%s calls=%d permission=%s", result.Run.Status, calls.Load(), permissionMode(t, data))
	}
}

func TestConfirmCodeOnlyAppliesToSettingChanges(t *testing.T) {
	data, pending, workspace, calls := waitingEdit(t)
	confirmation := previewJSON(t, data, pending.Run.ID)
	_, _, err := invoke(t, "", "confirm", "--data-dir", data, "--run", pending.Run.ID, "--digest", confirmation.Digest, "--approve", "--code", "ABCDE-FGHJK")
	if exitCode(err) != exitUsage || readNotes(t, workspace) != "before" || calls.Load() != 1 {
		t.Fatalf("exit=%d calls=%d err=%v", exitCode(err), calls.Load(), err)
	}
}

func TestAttachedInteractiveRunAppliesOwnerControlCode(t *testing.T) {
	data, calls := controlProposalCLI(t)
	_, _, stop := startTestWeb(t, data)
	defer stop()
	raw, stderr, err := invoke(t, "y\n", "run", "--data-dir", data, "--interactive", "--json", "use workspace access")
	result := decodeRun(t, raw, stderr, err)
	match := shownControlCode.FindStringSubmatch(stderr)
	if match == nil || result.Run.Status != "completed" || result.Run.StopReason != "control_change_applied_next_run" || calls.Load() != 1 {
		t.Fatalf("status=%s reason=%s calls=%d stderr=%q", result.Run.Status, result.Run.StopReason, calls.Load(), stderr)
	}
	if permissionMode(t, data) != harness.PermissionWorkspace {
		t.Fatal("the owner did not apply the confirmed change")
	}
	assertCodeNotIn(t, raw, match[1])
}

func TestAttachedPreviewShowsCodeForConfirm(t *testing.T) {
	data, calls := controlProposalCLI(t)
	before := permissionMode(t, data)
	origin, token, stop := startTestWeb(t, data)
	defer stop()
	pending, confirmation := waitingControl(t, data)
	id, digest := pending.Run.ID, confirmation.Digest

	// The Chat UI card renders first; the CLI then sees the same code.
	status, body := webRequest(t, origin, token, "/api/control/confirmation?operation_id="+url.QueryEscape(confirmation.ControlOperationID)+
		"&conversation_id="+url.QueryEscape(pending.Run.ConversationID), "")
	var card harness.ControlConfirmation
	if status != http.StatusOK || json.Unmarshal(body, &card) != nil || card.Code == "" {
		t.Fatalf("card confirmation %d %s", status, body)
	}
	_, hint, err := invoke(t, "", "preview", "--data-dir", data, "--run", id)
	match := shownControlCode.FindStringSubmatch(hint)
	if err != nil || match == nil || !strings.Contains(hint, "--code "+match[1]) || formatControlCode(card.Code) != match[1] {
		t.Fatalf("attached preview hint=%q err=%v", hint, err)
	}
	if _, again, _ := invoke(t, "", "preview", "--data-dir", data, "--run", id); !strings.Contains(again, match[1]) {
		t.Fatal("previewing again issued a different code")
	}
	_, stderr, err := invoke(t, "", "confirm", "--data-dir", data, "--run", id, "--digest", digest, "--approve", "--code", "BADBADBAD1")
	if exitCode(err) != exitWaiting || !strings.Contains(stderr, "was not accepted") || permissionMode(t, data) != before {
		t.Fatalf("wrong code exit=%d stderr=%q err=%v", exitCode(err), stderr, err)
	}
	code := strings.ToLower(match[1])
	raw, stderr, err := invoke(t, "", "confirm", "--data-dir", data, "--run", id, "--digest", digest, "--approve", "--code", code, "--json")
	result := decodeRun(t, raw, stderr, err)
	if result.Run.Status != "completed" || permissionMode(t, data) != harness.PermissionWorkspace || calls.Load() != 1 {
		t.Fatalf("status=%s permission=%s calls=%d", result.Run.Status, permissionMode(t, data), calls.Load())
	}
}
