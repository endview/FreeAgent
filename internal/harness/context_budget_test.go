package harness

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDeclaredContextWindowIsSeparateFromByteBoundAndReportedUsage(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	limits := configLimits(config)
	binding := ModelBinding{ID: "test", Config: ModelConfig{Capabilities: ModelCapabilities{ContextTokens: 8192, ContextSource: "manual"}}}
	request := ModelRequest{MaxOutputTokens: 512, Messages: []ModelMessage{{Role: "user", Content: strings.Repeat("abcd ", 2000)}}}
	u := measureContext(contextWindow(config, limits, binding, 512), request, Run{})
	if u.OverLimit || u.Window.InputBudget != 8192-512-409 || u.Window.Source != "manual" || u.ReservedBytes != 0 {
		t.Fatalf("declared window not applied: %+v", u)
	}
	binding.Config.Capabilities.ContextTokens = 2048
	u = measureContext(contextWindow(config, limits, binding, 512), request, Run{})
	if !u.OverLimit || u.Resource != "estimated_tokens" || !errors.Is(u.budgetError(), ErrContextLimit) {
		t.Fatal("small model window not enforced", u)
	}
	binding.Config.Capabilities.ContextTokens = 0
	u = measureContext(contextWindow(config, limits, binding, 512), request, Run{})
	if u.Window.Source != "local_byte_budget" || u.Window.Tokens != 0 || u.ReservedBytes != 2048 || u.EstimateMethod == "reported" {
		t.Fatal("byte fallback was represented as an actual model token window", u)
	}
}

func TestMoreThanFortyMessagesUsePagedHistoryWithinWindow(t *testing.T) {
	calls := atomic.Int32{}
	service, _, _ := testHarness(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls.Add(1)
		return ModelResponse{Content: "ack", Usage: &ModelUsage{InputTokens: 50, OutputTokens: 1, TotalTokens: 51}}, nil
	}))
	service.Config.HistoryPageSize = 3
	service.Config.TaskSummaryBytes = 0
	chat := conversation(t, service)
	for i := range 25 {
		result, err := service.Run(t.Context(), chat, string(rune('a'+i)), "small input")
		if err != nil || result.Run.Status != "completed" {
			t.Fatalf("small message history failed at %d: %v", i, err)
		}
	}
	before := calls.Load()
	state, err := NewTaskService(service).ContextState(t.Context(), chat)
	if err != nil || calls.Load() != before || state.LastRequest == nil || state.LastRequest.Messages <= 40 || state.LastUsage == nil || state.LastUsage.InputTokens != 50 {
		t.Fatalf("usage inspection lost current facts or ran a model: %+v err=%v", state, err)
	}
	if !state.LastRequest.Stale {
		t.Fatal("last prepared request was represented as including the newer final answer")
	}
}

func TestContextBudgetNamesOversizedFixedInputAndCountsImages(t *testing.T) {
	window := ContextWindow{ByteLimit: 4096, OutputReserve: 1, AutomaticPercent: 80}
	request := ModelRequest{MaxOutputTokens: 1, Messages: []ModelMessage{{Role: "system", Content: strings.Repeat("fixed", 1000)}}}
	u := measureContext(window, request, Run{})
	var budget *ContextBudgetError
	if !errors.As(u.budgetError(), &budget) || budget.Code != "fixed_context_too_large" {
		t.Fatal("fixed prompt was misdiagnosed as compressible history", u)
	}
	request.Messages = []ModelMessage{{Role: "user", Images: []ModelImage{{Base64: strings.Repeat("a", 5000), Width: 1024, Height: 1024}}}}
	u = measureContext(window, request, Run{})
	if !u.OverLimit || u.Bytes < 5000 || u.EstimatedTokens < 1024 {
		t.Fatal("image payload omitted from final budget", u)
	}
}
