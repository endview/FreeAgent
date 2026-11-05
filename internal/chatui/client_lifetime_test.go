package chatui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

func TestExplicitClientDetachRetainsOtherClientAndWork(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	for _, id := range []string{"closing-desktop", "other-web"} {
		r := httptest.NewRequest(http.MethodGet, ui.http.URL+"/api/environment", nil)
		r.Header.Set("X-FreeAgent-Client", id)
		leave, ok := ui.server.enterClientRequest(r)
		if !ok {
			t.Fatal("client not admitted")
		}
		leave()
	}
	r := httptest.NewRequest(http.MethodPost, ui.http.URL+"/api/client/detach", nil)
	r.Header.Set("X-FreeAgent-Key", ui.server.token)
	r.Header.Set("X-FreeAgent-Client", "closing-desktop")
	w := httptest.NewRecorder()
	ui.server.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	ui.server.mu.Lock()
	_, closing := ui.server.clients["closing-desktop"]
	_, other := ui.server.clients["other-web"]
	ui.server.mu.Unlock()
	if closing || !other || ui.server.tryIdleShutdown(time.Now()) {
		t.Fatal("detach removed the wrong client or stopped the remaining Web")
	}
	ui.server.ForgetClient("other-web")
	if !ui.server.tryIdleShutdown(time.Now()) {
		t.Fatal("normal close retained a stale client lease")
	}
}

func TestDesktopIdleShutdownKeepsWebAndRejectsNewAdmission(t *testing.T) {
	ui := setupUI(t, harness.EchoModel{})
	now := time.Now()
	r := httptest.NewRequest("GET", ui.http.URL+"/api/environment", nil)
	r.Header.Set("X-FreeAgent-Client", "web-only")
	leave, ok := ui.server.enterClientRequest(r)
	if !ok {
		t.Fatal("not admitted")
	}
	leave()
	if ui.server.tryIdleShutdown(now) {
		t.Fatal("closed while authenticated Web client remained")
	}
	if !ui.server.tryIdleShutdown(now.Add(time.Minute)) {
		t.Fatal("idle last client did not release Owner")
	}
	r = httptest.NewRequest("POST", ui.http.URL+"/api/chats", nil)
	if leave, ok := ui.server.enterClientRequest(r); ok {
		leave()
		t.Fatal("new input raced the stop decision")
	}
}

func TestDesktopIdleShutdownKeepsWaitingConfirmation(t *testing.T) {
	ui := setupUI(t, fixtureModel(func(_ context.Context, r harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		return harness.ModelResponse{ToolCalls: []harness.ToolCall{{ID: "edit", Name: "file_create", Arguments: json.RawMessage(`{"path":"confirm.txt","content":"pending"}`)}}}, nil
	}))
	chat := ui.create(t)
	result, err := ui.tasks.Submit(t.Context(), chat, "pending", "create a file")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatal(result, err)
	}
	if ui.server.tryIdleShutdown(time.Now().Add(time.Minute)) {
		t.Fatal("exited with a waiting confirmation")
	}
}
