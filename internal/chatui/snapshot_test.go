package chatui

import (
	"context"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func TestSnapshotRefreshesWhenConfirmationCompletesDuringRead(t *testing.T) {
	server := &Server{tasks: harness.NewTaskService(&harness.Service{}), running: map[string]*ownerExecution{"chat": {}}, streams: map[string]Stream{}, generation: "snapshot-test"}
	reads := 0
	payload, err := server.readChatSnapshot(t.Context(), "chat", "", func(context.Context, string, string) (harness.ChatView, error) {
		reads++
		if reads == 1 {
			server.mu.Lock()
			delete(server.running, "chat")
			server.eventSeq++
			server.mu.Unlock()
			return harness.ChatView{Turns: []harness.ChatTurn{{State: "awaiting_approval"}}}, nil
		}
		return harness.ChatView{Turns: []harness.ChatTurn{{State: "completed"}}}, nil
	})
	if err != nil || reads != 2 || payload["busy"] != false || payload["snapshot_stable"] != true {
		t.Fatalf("reads=%d snapshot=%v err=%v", reads, payload, err)
	}
	if payload["view"].(harness.ChatView).Turns[0].State != "completed" {
		t.Fatal("old confirmation returned with idle slot")
	}
}

func TestSnapshotHasFiniteRetryAndMarksAnUnstableRead(t *testing.T) {
	server := &Server{tasks: harness.NewTaskService(&harness.Service{}), running: map[string]*ownerExecution{}, streams: map[string]Stream{}}
	reads := 0
	payload, err := server.readChatSnapshot(t.Context(), "chat", "", func(context.Context, string, string) (harness.ChatView, error) {
		reads++
		server.mu.Lock()
		server.eventSeq++
		server.stateSeq++
		server.mu.Unlock()
		return harness.ChatView{}, nil
	})
	if err != nil || reads != 3 || payload["snapshot_stable"] != false || payload["busy"] != true {
		t.Fatalf("reads=%d snapshot=%v err=%v", reads, payload, err)
	}
}

func TestSnapshotIncludesConcurrentTextWithoutStarvingOnStreaming(t *testing.T) {
	server := &Server{tasks: harness.NewTaskService(&harness.Service{}), running: map[string]*ownerExecution{"chat": {}}, streams: map[string]Stream{"attempt": {Attempt: "attempt", Run: "run"}}, listeners: map[chan []byte]bool{}, generation: "streaming"}
	reads := 0
	payload, err := server.readChatSnapshot(t.Context(), "chat", "", func(context.Context, string, string) (harness.ChatView, error) {
		reads++
		server.onEvent(harness.Event{Kind: harness.EventModelText, RunID: "run", ConversationID: "chat", AttemptID: "attempt", Message: "new text"})
		return harness.ChatView{Turns: []harness.ChatTurn{{Key: "run", State: "generating"}}}, nil
	})
	if err != nil || reads != 1 || payload["snapshot_stable"] != true || payload["snapshot_seq"] != uint64(1) {
		t.Fatalf("reads=%d snapshot=%v err=%v", reads, payload, err)
	}
	if streams := payload["streams"].([]Stream); len(streams) != 1 || streams[0].Raw != "new text" {
		t.Fatalf("stream projection=%+v", streams)
	}
}
