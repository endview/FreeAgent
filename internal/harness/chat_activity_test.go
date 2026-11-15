package harness

import (
	"context"
	"fmt"
	"testing"
)

func TestChatActivitiesPageScopeAndOldAttention(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	snapshot, err := service.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := service.Store.CreateRun(t.Context(), chat, "chain", "synthetic", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	request := string(mustJSON(ModelRequest{ModelID: "default"}))
	for i := range 260 {
		response := ModelResponse{Content: "synthetic result"}
		status := "finished"
		if i < 3 {
			response.Failure = &CallError{Certain: true, Category: "fixture", Message: "retained failure"}
		}
		if i == 3 {
			status = "unknown"
		}
		if _, err := service.Store.db.ExecContext(t.Context(), `INSERT INTO fa.attempts(id,run_id,kind,call_id,status,request_json,response_json) VALUES($1,$2,'model','',$3,$4,$5)`, fmt.Sprintf("chain-%03d", i), run.ID, status, request, string(mustJSON(response))); err != nil {
			t.Fatal(err)
		}
	}
	view, err := tasks.Chat(t.Context(), chat, "")
	if err != nil {
		t.Fatal(err)
	}
	turn := view.Turns[0]
	if len(turn.Activity) != 128 || !turn.ActivityEarlier || turn.AttentionCount != 4 || len(turn.Attention) != 3 || turn.Attention[0].Key != "chain-003" {
		t.Fatalf("bounded tail or old attention lost: tail=%d attention=%+v count=%d", len(turn.Activity), turn.Attention, turn.AttentionCount)
	}
	seen := map[string]bool{}
	cursor := ""
	for n := 0; n < 3; n++ {
		page, err := tasks.ChatActivities(t.Context(), chat, run.ID, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 260 || len(page.Items) > 128 {
			t.Fatal("unbounded or incorrect page")
		}
		var previous int64
		for _, item := range page.Items {
			if seen[item.Key] || item.Sequence <= previous {
				t.Fatal("duplicate or unstable chain order")
			}
			seen[item.Key] = true
			previous = item.Sequence
		}
		if n < 2 && !page.HasEarlier || n == 2 && page.HasEarlier {
			t.Fatal("wrong pagination boundary")
		}
		cursor = page.Before
	}
	if len(seen) != 260 {
		t.Fatalf("lost chain entries: %d", len(seen))
	}
	other := conversation(t, service)
	if _, err := tasks.ChatActivities(t.Context(), other, run.ID, ""); err == nil {
		t.Fatal("run crossed conversation boundary")
	}
	otherRun, _, err := service.Store.CreateRun(t.Context(), other, "other-chain", "synthetic", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.ChatActivities(t.Context(), other, otherRun.ID, "chain-003"); err == nil {
		t.Fatal("cursor crossed run boundary")
	}
}

func TestChatGenerationModelSurvivesSelectionAndReload(t *testing.T) {
	service, _, _ := testHarness(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
		return ModelResponse{Content: "answer from " + req.ModelID, Provider: &ProviderResponse{Protocol: "chat", UsageStatus: "missing", FinishReason: "stop", Model: "wire-" + req.ModelID}}, nil
	}))
	service.Config.Models = map[string]ModelConfig{"first": {Provider: "echo"}, "second": {Provider: "echo"}}
	service.Config.DefaultModel = "first"
	tasks := NewTaskService(service)
	chat := conversation(t, service)
	if _, err := tasks.Submit(t.Context(), chat, "one", "first answer"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SelectModel(t.Context(), chat, "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Submit(t.Context(), chat, "two", "second answer"); err != nil {
		t.Fatal(err)
	}
	for _, reload := range []bool{false, true} {
		if reload {
			reopenHarness(t, service)
		}
		view, err := tasks.Chat(t.Context(), chat, "")
		if err != nil {
			t.Fatal(err)
		}
		for i, id := range []string{"first", "second"} {
			m := view.Turns[i].Messages[1]
			if m.ModelID != id || m.ModelName != "wire-"+id {
				t.Fatalf("historical model changed: %+v", m)
			}
		}
	}
}
