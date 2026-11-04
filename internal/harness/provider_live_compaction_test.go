package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLiveProviderAutomaticCompactionRetainsConstraint(t *testing.T) {
	config, keys, _ := liveProviderConfig(t)
	var summaries, ordinary, reused int
	live := keyedClient(t, config, keys, nil)
	service, _, _ := testHarness(t, modelFunc(func(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
		if request.Purpose == "compaction" {
			summaries++
		} else {
			ordinary++
			for _, m := range request.Messages {
				if strings.Contains(m.Content, "Conversation summary") && strings.Contains(m.Content, "AMBER-7429") {
					reused++
				}
			}
		}
		return live.Generate(ctx, request, emit)
	}))
	service.Keys = keys
	service.Config.Models[service.Config.DefaultModel] = config
	enableTestProviders(&service.Config)
	service.Config.HistoryPageSize = 6
	service.Config.MaxContextBytes = 10000
	service.Config.TaskSummaryBytes = 2048
	service.Config.MaxOutputTokens = 1024
	tasks := NewTaskService(service)
	if err := tasks.SetSpacePermission(t.Context(), LocalSpaceID, PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	chat := conversation(t, service)
	for i := range 4 {
		prompt := fmt.Sprintf("Conversation sample %d. %s Reply only ACK. Retain the earlier exact project constraint for later questions.", i, strings.Repeat("This is background context about a small Harness. ", 28))
		if i == 0 {
			prompt = "Project constraint: the project label is AMBER-7429 and delivery must be a downloadable Markdown attachment, with no hashes in the prose. Retain this exact label and delivery rule. " + prompt
		}
		result, err := tasks.Submit(t.Context(), chat, fmt.Sprintf("long-%d", i), prompt)
		if err != nil || result.Run.Status != "completed" {
			t.Fatalf("live long conversation %d failed: status=%s err=%v", i, result.Run.Status, err)
		}
	}
	answer, err := tasks.Submit(t.Context(), chat, "recall", "State the exact project label and the delivery rule from earlier in one English sentence of at most 30 words. Do not explain, and do not use tools.")
	if err != nil || !strings.Contains(answer.Answer, "AMBER-7429") || !strings.Contains(strings.ToLower(answer.Answer), "markdown") {
		attempts, _ := service.Store.ListAttempts(t.Context(), answer.Run.ID)
		for _, a := range attempts {
			if a.Kind == "model" {
				var saved ModelResponse
				if json.Unmarshal([]byte(a.ResponseJSON), &saved) == nil {
					t.Logf("actual provider finish=%v usage=%+v", saved.Provider, saved.Usage)
				}
			}
		}
		t.Fatalf("live constraint recall not accepted: status=%s err=%v", answer.Run.Status, err)
	}
	state, err := tasks.ContextState(t.Context(), chat)
	if err != nil || state.Summary == nil || summaries < 1 || reused < 1 || state.Summary.Scope != "public_text" {
		t.Fatalf("summary not actually produced/reused: calls=%d reused=%d state=%s err=%v", summaries, reused, state.Status, err)
	}
	messages, err := tasks.Messages(t.Context(), chat)
	if err != nil || len(messages) != 10 {
		t.Fatal("source history changed", len(messages), err)
	}
	t.Logf("real Provider automatic compaction accepted: summaries=%d ordinary=%d requests_reusing_summary=%d source_messages=%d covered=%d revision=%d None=true", summaries, ordinary, reused, len(messages), state.Summary.SourceCount, state.Summary.Revision)
}
