package chatui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func seedContextUI(t *testing.T, ui *testUI) string {
	t.Helper()
	chat := ui.create(t)
	for i := range 3 {
		if _, err := ui.tasks.Submit(t.Context(), chat, string(rune('a'+i)), strings.Repeat("source facts; ", 40)); err != nil {
			t.Fatal(err)
		}
	}
	return chat
}

func contextClient(t *testing.T, ui *testUI, interactive bool) *handoff.Client {
	t.Helper()
	info := handoff.EngineInfo{Origin: ui.http.URL, Generation: ui.server.generation, OwnerKind: "interactive"}
	var client *handoff.Client
	var err error
	if interactive {
		client, err = handoff.NewInteractiveClient(info, ui.server.token)
	} else {
		client, err = handoff.NewClient(info, ui.server.attachToken)
	}
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestContextAPIUsesSameSummaryAndRejectsObservationMutation(t *testing.T) {
	ui := setupUI(t, fixtureModel(func(context.Context, harness.ModelRequest, func(string)) (harness.ModelResponse, error) {
		return harness.ModelResponse{Content: "Retained source facts, completed work and next user action."}, nil
	}))
	chat := seedContextUI(t, ui)
	client, observer := contextClient(t, ui, true), contextClient(t, ui, false)
	if _, err := observer.ManageContext(t.Context(), chat, harness.ContextCommand{Action: "compact"}); err == nil {
		t.Fatal("observation credential mutated selection")
	}
	state, err := observer.ContextState(t.Context(), chat)
	if err != nil || state.Status != "none" {
		t.Fatalf("state: %+v %v", state, err)
	}
	result, err := client.ManageContext(t.Context(), chat, harness.ContextCommand{Action: "compact", ExpectedRevision: state.Revision})
	if err != nil || !result.Changed || result.ModelCalls != 1 {
		t.Fatalf("compact: %+v %v", result, err)
	}
	code, raw := ui.request(t, http.MethodGet, "/api/attach/context?conversation_id="+chat, nil)
	var gui harness.ContextState
	if code != 200 || json.Unmarshal(raw, &gui) != nil || gui.Revision != result.State.Revision || gui.Summary.SourceDigest != result.State.Summary.SourceDigest {
		t.Fatalf("GUI and CLI disagree: %d %s", code, raw)
	}
	if _, err := client.ManageContext(t.Context(), chat, harness.ContextCommand{Action: "clear", ExpectedRevision: state.Revision}); err == nil {
		t.Fatal("stale revision cleared new summary")
	}
	cleared, err := client.ManageContext(t.Context(), chat, harness.ContextCommand{Action: "clear", ExpectedRevision: gui.Revision})
	if err != nil || !cleared.Changed || cleared.State.Status != "none" {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
}

func TestContextCancellationRetainsSummaryAndOwner(t *testing.T) {
	for _, mode := range []string{"gui-stop", "client-cancel"} {
		t.Run(mode, func(t *testing.T) { testContextCancellation(t, mode == "client-cancel") })
	}
}

func testContextCancellation(t *testing.T, cancelClient bool) {
	entered, block := make(chan struct{}, 1), make(chan struct{})
	ui := setupUI(t, fixtureModel(func(ctx context.Context, request harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		if request.Purpose == "compaction" {
			select {
			case <-block:
				entered <- struct{}{}
				<-ctx.Done()
				return harness.ModelResponse{}, ctx.Err()
			default:
			}
		}
		return harness.ModelResponse{Content: "Goals and source facts retained. Await next action."}, nil
	}))
	chat := seedContextUI(t, ui)
	client := contextClient(t, ui, true)
	first, err := client.ManageContext(t.Context(), chat, harness.ContextCommand{Action: "compact"})
	if err != nil {
		t.Fatal(err)
	}
	close(block)
	done := make(chan error, 1)
	operation, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		_, err := client.ManageContext(operation, chat, harness.ContextCommand{Action: "refresh"})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("compaction did not enter Provider")
	}
	state, err := client.State(t.Context())
	if err != nil || state.ActiveBinding == "" || state.ActiveChat != chat {
		t.Fatalf("active state: %+v %v", state, err)
	}
	if _, err := client.ManageContext(t.Context(), chat, harness.ContextCommand{Action: "clear"}); err == nil {
		t.Fatal("competing mutation accepted")
	}
	if cancelClient {
		cancel()
	} else {
		code, _ := ui.request(t, http.MethodPost, "/api/chats/"+chat+"/stop", map[string]any{})
		if code != 200 {
			t.Fatalf("stop: %d", code)
		}
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel accepted as success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not settle")
	}
	ui.settled(t, chat)
	after, err := client.ContextState(t.Context(), chat)
	if err != nil || after.Revision != first.State.Revision || after.Blocked == "" {
		t.Fatalf("cancel lost previous summary/UNKNOWN: %+v %v", after, err)
	}
	view, err := client.Chat(t.Context(), chat)
	if err != nil || len(view.Turns) != 4 || !view.Turns[3].Maintenance || view.Turns[3].State != "unknown" || len(view.Turns[3].Messages) != 0 {
		t.Fatalf("UNKNOWN maintenance hidden or fabricated messages: %+v %v", view, err)
	}
	select {
	case <-ui.server.StopRequested():
		t.Fatal("ordinary Stop requested owner exit")
	default:
	}
	state, err = client.State(t.Context())
	if err != nil || state.Stopping || state.ActiveChat != "" {
		t.Fatalf("owner unusable: %+v %v", state, err)
	}
	if _, err := client.NewConversation(t.Context(), "owner remains usable"); err != nil {
		t.Fatal(err)
	}
}

func TestContextToolConfirmationRequiresInteractiveCredential(t *testing.T) {
	propose := make(chan struct{})
	ui := setupUI(t, fixtureModel(func(_ context.Context, request harness.ModelRequest, _ func(string)) (harness.ModelResponse, error) {
		select {
		case <-propose:
			if request.Purpose == "" {
				for _, message := range request.Messages {
					if message.Role == "tool" && message.Name == "context_manage" {
						return harness.ModelResponse{Content: "user decision observed"}, nil
					}
				}
				return harness.ModelResponse{ToolCalls: []harness.ToolCall{{ID: "proposal", Name: "context_manage", Arguments: json.RawMessage(`{"action":"compact"}`)}}}, nil
			}
		default:
		}
		return harness.ModelResponse{Content: "Retained source facts. Await user action."}, nil
	}))
	chat := seedContextUI(t, ui)
	close(propose)
	run, err := ui.tasks.Submit(t.Context(), chat, "proposal", "compact this conversation")
	if err != nil || run.Run.Status != "waiting_confirmation" {
		t.Fatalf("proposal: %+v %v", run, err)
	}
	confirmation, err := ui.tasks.Preview(t.Context(), run.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	observer := contextClient(t, ui, false)
	if err := observer.Confirm(t.Context(), confirmation, true); err == nil {
		t.Fatal("observation credential approved persistent summary")
	}
	state, err := ui.tasks.ContextState(t.Context(), chat)
	if err != nil || state.Status != "none" {
		t.Fatalf("observation changed summary: %+v %v", state, err)
	}
}
