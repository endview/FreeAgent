package harness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAutomaticCompactionTriggersBeforeFullAndReusesSummary(t *testing.T) {
	compacted, reused := 0, 0
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.Purpose == "compaction" {
			compacted++
			return ModelResponse{Content: "Keep project AMBER-7429 and downloadable Markdown delivery. Completed previous work; retain user constraints."}, nil
		}
		for _, m := range r.Messages {
			if strings.Contains(m.Content, "Conversation summary") && strings.Contains(m.Content, "AMBER-7429") {
				reused++
			}
		}
		return ModelResponse{Content: "ack"}, nil
	}))
	core.Config.TaskSummaryBytes = 512
	core.Config.MaxOutputTokens = 128
	core.Config.MaxContextBytes = 16000
	chat := conversation(t, core)
	for i := range 10 {
		result, err := core.Run(t.Context(), chat, fmt.Sprint(i), "AMBER-7429 downloadable Markdown. "+strings.Repeat("background context ", 70))
		if err != nil || result.Run.Status != "completed" {
			t.Fatalf("round=%d status=%s error=%v", i, result.Run.Status, err)
		}
	}
	if compacted < 1 || reused < 1 {
		t.Fatalf("automatic calls=%d reused=%d", compacted, reused)
	}
	messages, _ := core.Store.ListMessages(t.Context(), chat)
	if len(messages) != 20 {
		t.Fatal("source history changed")
	}
}

func TestCompletedLargeToolOutputIsSummarizedAsBoundedSourceFragments(t *testing.T) {
	parts := 0
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.Purpose == "compaction" {
			parts++
			if len(r.Tools) != 0 || completeMessageGroups(r.Messages) != nil {
				t.Error("source fragments became dangling executable tool protocol")
			}
			return ModelResponse{Content: "Completed tool data retained as a summary; no replay is authorized."}, nil
		}
		return ModelResponse{Content: "continued"}, nil
	}))
	chat := conversation(t, core)
	snapshot, _ := core.snapshot()
	prior, _, _ := core.Store.CreateRun(t.Context(), chat, "prior-large", "prior task", snapshot)
	a, _ := core.Store.startModel(t.Context(), prior, `{}`, 0)
	call := makeCall("large", "file_read", map[string]string{"path": "note"})
	if err := core.Store.saveModel(t.Context(), prior, a, ModelResponse{ToolCalls: []ToolCall{call}}); err != nil {
		t.Fatal(err)
	}
	attempts, _ := core.Store.ListAttempts(t.Context(), prior.ID)
	if err := core.Store.saveTool(t.Context(), prior, attempts[1], call, ToolResult{Content: strings.Repeat("completed observed data; ", 3000)}); err != nil {
		t.Fatal(err)
	}
	a, _ = core.Store.startModel(t.Context(), prior, `{}`, 0)
	if err := core.Store.saveModel(t.Context(), prior, a, ModelResponse{Content: "prior completed"}); err != nil {
		t.Fatal(err)
	}
	core.Config.MaxContextBytes = 16000
	core.Config.MaxOutputTokens = 128
	core.Config.TaskSummaryBytes = 512
	result, err := core.Run(t.Context(), chat, "after-large", "continue")
	if err != nil || result.Run.Status != "completed" || parts < 2 {
		t.Fatalf("status=%s fragments=%d error=%v", result.Run.Status, parts, err)
	}
	attempts, _ = core.Store.ListAttempts(t.Context(), prior.ID)
	if len(attempts) != 3 {
		t.Fatal("source tool was replayed")
	}
}

func TestDefiniteProviderContextRefusalGetsOneRecordedCompactionRetry(t *testing.T) {
	core, tasks, chat, _ := summaryFixture(t)
	core.Config.TaskSummaryBytes = 512
	ordinary, summaries := 0, 0
	core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.Purpose == "compaction" {
			summaries++
			return ModelResponse{Content: "Preserve goals, constraints and completed work."}, nil
		}
		ordinary++
		if ordinary == 1 {
			return ModelResponse{}, &CallError{Certain: true, Category: "context_limit", Message: "definite refusal before generation"}
		}
		return ModelResponse{Content: "recovered"}, nil
	})
	result, err := tasks.Submit(t.Context(), chat, "refused", "next task")
	if err != nil || result.Run.Status != "completed" || ordinary != 2 || summaries != 1 {
		t.Fatalf("status=%s ordinary=%d summaries=%d error=%v", result.Run.Status, ordinary, summaries, err)
	}
	_, attempts, _, _ := tasks.Inspect(t.Context(), result.Run.ID)
	if len(attempts) != 3 || !strings.Contains(attempts[0].ResponseJSON, "context_limit") {
		t.Fatal("original refusal attempt was not retained")
	}
	if _, err := tasks.Submit(t.Context(), chat, "refused", "next task"); err != nil || ordinary != 2 {
		t.Fatal("retrying input replayed work", err)
	}
	if errors.Is(err, ErrUnknownResult) {
		t.Fatal("definite refusal became UNKNOWN")
	}
}
