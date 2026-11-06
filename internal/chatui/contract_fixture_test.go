package chatui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

// This is one small real projection fixture, not a second protocol generator.
func TestFrontendContractFixture(t *testing.T) {
	view := harness.ChatView{Conversation: harness.Conversation{ID: "synthetic-chat", Title: "合成对话", Workspace: "/synthetic", SpaceID: "local"},
		ModelID: "echo", Permission: harness.PermissionSelection{Mode: harness.PermissionAsk},
		Turns: []harness.ChatTurn{{Key: "synthetic-run", State: "completed", Messages: []harness.ChatMessage{{Key: "1", Role: "user", Raw: "合成输入"}, {Key: "2", Role: "assistant", Raw: "合成输出"}}, Activity: []harness.ChatActivity{}}}}
	server := &Server{tasks: harness.NewTaskService(&harness.Service{}), running: map[string]*ownerExecution{}, streams: map[string]Stream{}, generation: "synthetic-generation", eventSeq: 7}
	snapshot, err := server.readChatSnapshot(t.Context(), "synthetic-chat", "", func(context.Context, string, string) (harness.ChatView, error) { return view, nil })
	if err != nil {
		t.Fatal(err)
	}
	snapshot["server_version"] = "contract-sample"
	sample := map[string]any{"snapshot": snapshot, "event_kinds": harness.EventKinds(), "control_operation": harness.ControlOperation{ID: "synthetic-operation", Kind: harness.ControlSetAutoPolicy, Preview: "合成控制变更", Status: harness.ControlPending}, "event": harness.Event{Kind: harness.EventModelText, Seq: 8, Generation: "synthetic-generation", ConversationID: "synthetic-chat", RunID: "synthetic-run", AttemptID: "synthetic-attempt", Message: "合成片段"}}
	want, err := json.MarshalIndent(sample, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	path := filepath.Join("test", "fixtures", "core-contract.json")
	if os.Getenv("FREEAGENT_UPDATE_FRONTEND_CONTRACT") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, want, 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), want) {
		t.Fatal("Core projection fixture is stale; regenerate it explicitly, then update frontend DTO/event coverage")
	}
}
