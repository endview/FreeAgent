package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

// waitingEdit starts a run whose model asks to change notes.txt from
// "before" to "after", then answers "Changed.". It returns the waiting run,
// the workspace and the model call counter.
func waitingEdit(t *testing.T) (string, harness.RunResult, string, *atomic.Int32) {
	t.Helper()
	data, workspace := initCLI(t)
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"edit","type":"function","function":{"name":"file_edit","arguments":"{\"path\":\"notes.txt\",\"expected_sha256\":\"6db7d803e74f1ffa7d8f5adc0bf95b3e15bf4c8373fffadf546227cc6c6742cb\",\"old_text\":\"before\",\"new_text\":\"after\"}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Changed."},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(server.Close)
	key := fixtureKey(t, server.URL, harness.KeySourceNone, "", "")
	if err := harness.WriteConfig(data, cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "fixture"})); err != nil {
		t.Fatal(err)
	}
	raw, stderr, err := invoke(t, "", "run", "--data-dir", data, "--json", "change notes.txt")
	var pending harness.RunResult
	if exitCode(err) != exitWaiting || json.Unmarshal([]byte(raw), &pending) != nil || pending.Run.Status != "waiting_confirmation" {
		t.Fatalf("run exit=%d raw=%q err=%v", exitCode(err), raw, err)
	}
	if !strings.Contains(stderr, "freeagent preview --run "+pending.Run.ID) {
		t.Fatalf("missing next step: %q", stderr)
	}
	return data, pending, workspace, calls
}

func readNotes(t *testing.T, workspace string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(workspace, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func previewJSON(t *testing.T, data, runID string) harness.Confirmation {
	t.Helper()
	raw, _, err := invoke(t, "", "preview", "--data-dir", data, "--run", runID, "--json")
	var confirmation harness.Confirmation
	if err != nil || json.Unmarshal([]byte(raw), &confirmation) != nil {
		t.Fatalf("preview=%q err=%v", raw, err)
	}
	return confirmation
}

func TestPreviewConfirmDigestMismatch(t *testing.T) {
	data, pending, workspace, calls := waitingEdit(t)
	confirmation := previewJSON(t, data, pending.Run.ID)
	if confirmation.RunID != pending.Run.ID || confirmation.Tool != "file_edit" || confirmation.Digest == "" ||
		confirmation.CallID != "edit" || !strings.Contains(confirmation.Preview, "after") ||
		len(confirmation.Items) != 1 || confirmation.Items[0].Kind != "file_edit" ||
		confirmation.Items[0].Target != "notes.txt" || confirmation.Items[0].Old != "before" ||
		confirmation.Items[0].New != "after" {
		t.Fatalf("confirmation=%+v", confirmation)
	}
	raw, stderr, err := invoke(t, "", "preview", "--data-dir", data, "--run", pending.Run.ID)
	if err != nil || !strings.Contains(raw, "Digest: "+confirmation.Digest) || !strings.Contains(stderr, "--approve") {
		t.Fatalf("text preview=%q stderr=%q err=%v", raw, stderr, err)
	}
	wrongPrefix := byte('0')
	if confirmation.Digest[0] == wrongPrefix {
		wrongPrefix = '1'
	}
	wrongDigest := string(wrongPrefix) + confirmation.Digest[1:]
	_, stderr, err = invoke(t, "", "confirm", "--data-dir", data, "--run", pending.Run.ID, "--digest", wrongDigest, "--approve")
	if exitCode(err) != exitFailure || !strings.Contains(stderr, "freeagent preview --run") {
		t.Fatalf("mismatch exit=%d stderr=%q err=%v", exitCode(err), stderr, err)
	}
	if readNotes(t, workspace) != "before" || calls.Load() != 1 {
		t.Fatalf("a mismatched digest dispatched the call: calls=%d", calls.Load())
	}
	if again := previewJSON(t, data, pending.Run.ID); again.Digest != confirmation.Digest {
		t.Fatal("the rejected confirmation changed the pending call")
	}
	if _, _, err := invoke(t, "", "confirm", "--data-dir", data, "--run", pending.Run.ID, "--approve"); exitCode(err) != exitUsage {
		t.Fatalf("missing digest exit=%d err=%v", exitCode(err), err)
	}
	raw, _, err = invoke(t, "", "confirm", "--data-dir", data, "--run", pending.Run.ID, "--digest", confirmation.Digest, "--approve", "--json")
	var finished harness.RunResult
	if err != nil || json.Unmarshal([]byte(raw), &finished) != nil || finished.Run.Status != "completed" {
		t.Fatalf("approve=%q err=%v", raw, err)
	}
	if readNotes(t, workspace) != "after" || calls.Load() != 2 {
		t.Fatalf("approved edit result=%q calls=%d", readNotes(t, workspace), calls.Load())
	}
	if _, _, err := invoke(t, "", "confirm", "--data-dir", data, "--run", pending.Run.ID, "--digest", confirmation.Digest, "--approve"); err == nil {
		t.Fatal("a used confirmation was accepted again")
	}
	if readNotes(t, workspace) != "after" || calls.Load() != 2 {
		t.Fatal("a repeated confirmation dispatched again")
	}
}

func TestConfirmDefaultsToDecline(t *testing.T) {
	data, pending, workspace, calls := waitingEdit(t)
	confirmation := previewJSON(t, data, pending.Run.ID)
	raw, _, err := invoke(t, "", "confirm", "--data-dir", data, "--run", pending.Run.ID, "--digest", confirmation.Digest, "--json")
	var result harness.RunResult
	if json.Unmarshal([]byte(raw), &result) != nil || result.Run.ID != pending.Run.ID || exitCode(err) != statusExitCode(result.Run.Status) {
		t.Fatalf("decline=%q exit=%d err=%v", raw, exitCode(err), err)
	}
	if readNotes(t, workspace) != "before" {
		t.Fatal("confirm without --approve changed the file")
	}
	if result.Run.Status == "waiting_confirmation" || calls.Load() != 2 {
		t.Fatalf("declined run status=%s calls=%d", result.Run.Status, calls.Load())
	}
}
