package chatui

import (
	"context"
	"encoding/json"
	"github.com/endview/freeagent/internal/harness"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOwnerInputsSteerQueueAndBranchUseSharedCore(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	ui := setupUI(t, fixtureModel(func(ctx context.Context, r harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return harness.ModelResponse{}, ctx.Err()
			}
		}
		return harness.ModelResponse{Content: "done"}, nil
	}))
	chat := ui.create(t)
	if code, raw := ui.request(t, "POST", "/api/chats/"+chat+"/submit", map[string]string{"request_key": "first", "prompt": "initial"}); code != 202 {
		t.Fatalf("submit %d %s", code, raw)
	}
	<-entered
	run, err := ui.tasks.Latest(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []harness.InputCommand{{Action: "queue", RequestKey: "after", Prompt: "queued input"}, {Action: "steer", RunID: run.ID, RequestKey: "direction", Prompt: "new direction"}} {
		if code, raw := ui.request(t, "POST", "/api/attach/inputs", map[string]any{"conversation_id": chat, "operation": "accept", "command": command}); code != 202 {
			t.Fatalf("input %d %s", code, raw)
		}
	}
	close(release)
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		latest, e := ui.tasks.Latest(t.Context(), chat)
		if e == nil && latest.RequestKey == "after" && latest.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	latest, _ := ui.tasks.Latest(t.Context(), chat)
	if latest.RequestKey != "after" || latest.Status != "completed" || calls.Load() != 3 {
		t.Fatalf("latest=%s %s calls=%d", latest.RequestKey, latest.Status, calls.Load())
	}
	code, raw := ui.request(t, "GET", "/api/attach/inputs?conversation_id="+chat, nil)
	if code != 200 || strings.Count(string(raw), `"status":"adopted"`) != 2 {
		t.Fatalf("state %d %s", code, raw)
	}
	messages, _ := ui.tasks.Messages(t.Context(), chat)
	code, raw = ui.request(t, "POST", "/api/attach/branch", map[string]any{"conversation_id": chat, "command": harness.BranchCommand{RequestKey: "branch", MessageID: messages[0].ID, Mode: "rerun"}})
	var branch harness.BranchResult
	if code != 201 || json.Unmarshal(raw, &branch) != nil || branch.Conversation.ID == chat {
		t.Fatalf("branch %d %s", code, raw)
	}
	code, raw = ui.request(t, http.MethodPost, "/api/models/context", map[string]any{"id": "default", "tokens": 65536})
	if code != 200 || ui.tasks.Config().Models["default"].Capabilities.ContextTokens != 65536 {
		t.Fatalf("model window %d %s", code, raw)
	}
}

func TestPublicInputsRequireAuthenticationAndSeparateLocalCapabilities(t *testing.T) {
	f := newPublicFixture(t, nil)
	for _, path := range []string{"/api/remote/inputs?conversation_id=x", "/api/remote/branch"} {
		if w := f.request(t, "GET", path, nil, nil); w.Code != 401 {
			t.Fatal("unauthenticated", path, w.Code)
		}
	}
	f.login(t)
	if w := f.request(t, "POST", "/api/remote/inputs", strings.NewReader(`{"conversation_id":"x","operation":"accept","command":{"action":"queue","request_key":"q","prompt":"hello"}}`), map[string]string{"X-FreeAgent-CSRF": ""}); w.Code != 403 {
		t.Fatal("queue accepted without CSRF", w.Code)
	}
	if w := f.request(t, "GET", "/api/attach/inputs?conversation_id=x", nil, nil); w.Code != 404 {
		t.Fatal("local attach route exposed", w.Code)
	}
}
