package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func TestLiveCLIInputQueuesUntilExplicitGuidance(t *testing.T) {
	type inputRequest struct {
		Chat      string               `json:"conversation_id"`
		Operation string               `json:"operation"`
		Key       string               `json:"key"`
		RunID     string               `json:"run_id"`
		Command   harness.InputCommand `json:"command"`
	}
	requests := make(chan inputRequest, 4)
	var queuedKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/attach/chat-command":
			_ = json.NewEncoder(w).Encode(harness.Run{ID: "current", ConversationID: "chat", Status: "running"})
		case "/api/attach/inputs":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(harness.InputState{Entries: []harness.PendingInput{{Key: queuedKey, Action: "queue", Status: "pending", MessageID: 1}}})
				return
			}
			var body inputRequest
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			requests <- body
			key := body.Key
			if key == "" {
				key = body.Command.RequestKey
				queuedKey = key
			}
			_ = json.NewEncoder(w).Encode(harness.PendingInput{Key: key, Status: "pending"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	remote := &remoteChat{Client: &handoff.Client{Origin: server.URL, Secret: strings.Repeat("s", 64)}, stderr: &output}
	if err := remote.liveInput(t.Context(), "chat", "first", "current", "a later request"); err != nil {
		t.Fatal(err)
	}
	queued := <-requests
	if queued.Operation != "accept" || queued.Command.Action != "queue" || queued.Command.RunID != "" || queued.Command.Prompt != "a later request" {
		t.Fatal("ordinary terminal input did not queue", queued)
	}
	if !strings.Contains(output.String(), "/guide") {
		t.Fatal("queued input did not expose its guidance action", output.String())
	}
	if err := remote.liveInput(t.Context(), "chat", "first", "current", "/guide"); err != nil {
		t.Fatal(err)
	}
	guided := <-requests
	if guided.Operation != "steer" || guided.Key != queued.Command.RequestKey || guided.RunID != "current" || guided.Command.Prompt != "" {
		t.Fatal("guidance resent the message instead of converting its saved entry", guided)
	}
}
