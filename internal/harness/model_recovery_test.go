package harness

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func recoveryFixture(t *testing.T, model ModelClient) *Service {
	t.Helper()
	service, _, _ := testHarness(t, model)
	service.Config.ModelRetries = 2
	service.retryWait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	if err := WriteConfig(service.Store.DataDir(), service.Config); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestModelRecoveryKeepsUnknownAndUsesOneCompleteAnswer(t *testing.T) {
	calls := 0
	service := recoveryFixture(t, modelFunc(func(ctx context.Context, r ModelRequest, emit func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{Content: "partial", ToolCalls: []ToolCall{{ID: "never", Name: "file_create", Arguments: json.RawMessage(`{"path":"never.txt","content":"bad"}`)}}}, &CallError{Category: "transport", Message: "connection lost"}
		}
		if r.Recovery == nil || r.Recovery.Number != 1 {
			t.Fatal("recovery association missing")
		}
		return ModelResponse{Content: "complete"}, nil
	}))
	chat := conversation(t, service)
	result, err := service.Run(t.Context(), chat, "retry", "read only")
	if err != nil || result.Run.Status != "completed" || result.Answer != "complete" || calls != 2 {
		t.Fatalf("%+v %v calls=%d", result, err, calls)
	}
	attempts, err := service.Store.ListAttempts(t.Context(), result.Run.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Status != "unknown" || attempts[1].Status != "finished" {
		t.Fatal(attempts, err)
	}
	data := service.Store.DataDir()
	service.Store.Close()
	store, err := OpenStore(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	run, err := store.GetRun(t.Context(), result.Run.ID)
	if err != nil || run.Status != "completed" {
		t.Fatal("reopen reblocked safely replaced model", run, err)
	}
	if again, err := store.ListAttempts(t.Context(), run.ID); err != nil || again[0].Status != "unknown" {
		t.Fatal("original UNKNOWN changed", err)
	}
}

func TestModelRecoveryBudgetQuotaAndCancellation(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		failure *CallError
		cancel  bool
		want    int
	}{
		{"transient", &CallError{Certain: true, Category: "unavailable", Message: "503", Retryable: true}, false, 3},
		{"quota", normalizeProviderError(402, nil, ""), false, 1},
		{"quota429", normalizeProviderError(429, &openAIError{Type: "insufficient_quota"}, ""), false, 1},
		{"auth", normalizeProviderError(401, nil, ""), false, 1},
		{"cancel", &CallError{Category: "transport", Message: "disconnect"}, true, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			calls := 0
			service := recoveryFixture(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
				calls++
				return ModelResponse{}, scenario.failure
			}))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario.cancel {
				service.retryWait = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
			}
			result, err := service.Run(ctx, conversation(t, service), "budget", "task")
			if err == nil || calls != scenario.want {
				t.Fatalf("%+v %v calls=%d", result, err, calls)
			}
			attempts, e := service.Store.ListAttempts(t.Context(), result.Run.ID)
			if e != nil || len(attempts) != scenario.want {
				t.Fatal(attempts, e)
			}
			if scenario.cancel && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestModelRecoveryLengthRegeneratesWithoutPartialTools(t *testing.T) {
	calls := 0
	service := recoveryFixture(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{Content: "unfinished", ToolCalls: []ToolCall{{ID: "never", Name: "file_create", Arguments: json.RawMessage(`{}`)}}}, &CallError{Certain: true, Category: "output_limit", Message: "length"}
		}
		if len(r.Tools) != 0 || !r.DenyOperations {
			t.Fatal("length recovery exposed operations")
		}
		return ModelResponse{Content: "concise answer; work remains"}, nil
	}))
	result, err := service.Run(t.Context(), conversation(t, service), "length", "task")
	if err != nil || result.Run.Status != "completed" || calls != 2 {
		t.Fatal(result, err, calls)
	}
	attempts, _ := service.Store.ListAttempts(t.Context(), result.Run.ID)
	for _, a := range attempts {
		if a.Kind != "model" {
			t.Fatal("partial tool dispatched", a)
		}
	}
}

func TestModelRetryAfterAndMCPTrustRequireExplicitConfiguration(t *testing.T) {
	err := withRetryAfter(&CallError{}, http.Header{"Retry-After": []string{"7"}})
	if err.RetryAfterMS != 7000 {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"command":"server"}`, `{"command":"server","trustReadOnlyHint":false}`} {
		var c MCPServerConfig
		if json.Unmarshal([]byte(raw), &c) != nil || c.TrustReadOnlyHint {
			t.Fatal("implicit MCP read-only trust", c)
		}
	}
}

func TestModelRecoveryReplacementDoesNotBlockExactToolApproval(t *testing.T) {
	calls := 0
	core := recoveryFixture(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{}, &CallError{Category: "transport", Message: "interrupted"}
		}
		if calls == 2 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("create-once", "file_create", map[string]string{"path": "approved.txt", "content": "done"})}}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}))
	tasks := NewTaskService(core)
	result, err := tasks.Submit(t.Context(), conversation(t, core), "approval-recovery", "create once")
	if err != nil || result.Run.Status != "waiting_confirmation" {
		t.Fatal(result.Run.Status, err)
	}
	confirmation, err := tasks.Preview(t.Context(), result.Run.ID)
	if err != nil {
		t.Fatal("replaced model UNKNOWN blocked exact approval", err)
	}
	result, err = tasks.Confirm(t.Context(), result.Run.ID, confirmation, true)
	if err != nil || result.Run.Status != "completed" || calls != 3 {
		t.Fatal(result.Run.Status, calls, err)
	}
}
