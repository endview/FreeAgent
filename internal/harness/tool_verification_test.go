package harness

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type uncertainCreateTools struct {
	base   ToolRunner
	writes *int
}

func (t uncertainCreateTools) Definitions() []ToolDefinition { return t.base.Definitions() }
func (t uncertainCreateTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	p, err := t.base.Prepare(ctx, call)
	if err != nil || call.Name != "file_create" {
		return p, err
	}
	execute := p.Execute
	p.ExecuteRecorded = nil
	p.Execute = func(ctx context.Context) (ToolResult, error) {
		*t.writes++
		r, err := execute(ctx)
		if err != nil {
			return r, err
		}
		return r, &CallError{Message: "receipt acknowledgement lost"}
	}
	return p, nil
}

func TestToolUnknownVerificationReadsAndNeverReplaysWrite(t *testing.T) {
	for _, mode := range []string{"verified", "attempt_write", "wrong_evidence", "partial", "exhausted"} {
		t.Run(mode, func(t *testing.T) {
			calls, writes := 0, 0
			var original, hash, evidence string
			service := recoveryFixture(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
				calls++
				if calls == 1 {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("create", "file_create", map[string]string{"path": "result.txt", "content": "done"})}}, nil
				}
				if r.Purpose != "verification" {
					return ModelResponse{Content: "done after inspection"}, nil
				}
				for _, d := range r.Tools {
					if !verificationToolAllowed(d) {
						t.Fatalf("unsafe verification tool %s", d.Name)
					}
				}
				var source struct {
					ID   string `json:"attempt_id"`
					Hash string `json:"fingerprint"`
				}
				if json.Unmarshal([]byte(r.Messages[1].Content), &source) != nil {
					t.Fatal("missing verification source")
				}
				original, hash = source.ID, source.Hash
				if mode == "attempt_write" {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("unsafe", "file_create", map[string]string{"path": "duplicate.txt", "content": "bad"})}}, nil
				}
				if len(r.Messages) == 2 || mode == "exhausted" {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("inspect", "file_read", map[string]string{"path": "result.txt"})}}, nil
				}
				var observation struct {
					ID string `json:"evidence_attempt"`
				}
				json.Unmarshal([]byte(r.Messages[len(r.Messages)-1].Content), &observation)
				evidence = observation.ID
				if mode == "wrong_evidence" {
					hash = "wrong"
				}
				outcome := "happened"
				if mode == "partial" {
					outcome = "partial"
				}
				return ModelResponse{Content: string(mustJSON(map[string]any{"attempt_id": original, "fingerprint": hash, "outcome": outcome, "evidence_attempts": []string{evidence}}))}, nil
			}))
			service.Tools = uncertainCreateTools{base: service.Tools, writes: &writes}
			service.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) { return true, nil }
			// Full still passes through the verification gate.
			if err := NewTaskService(service).SetSpacePermission(t.Context(), LocalSpaceID, PermissionFull, true); err != nil {
				t.Fatal(err)
			}
			result, err := service.Run(t.Context(), conversation(t, service), "unknown", "create once")
			if writes != 1 {
				t.Fatalf("write count=%d", writes)
			}
			if mode == "verified" {
				if err != nil || result.Run.Status != "completed" || calls != 4 {
					t.Fatalf("status=%s calls=%d err=%v", result.Run.Status, calls, err)
				}
			} else if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" {
				t.Fatalf("unsafe continuation status=%s calls=%d err=%v", result.Run.Status, calls, err)
			}
			attempts, e := service.Store.ListAttempts(t.Context(), result.Run.ID)
			if e != nil {
				t.Fatal(e)
			}
			if attempts[1].Status != "unknown" {
				t.Fatal("original fact changed", attempts[1])
			}
			if mode == "verified" && attempts[1].Resolution != "verified_effect" {
				t.Fatal("resolution missing")
			}
			if mode == "exhausted" {
				reopenHarness(t, service)
				state, e := service.Store.reserveToolVerification(t.Context(), result.Run, attempts[1], hash)
				if !errors.Is(e, ErrUnknownResult) || state.Rounds != 2 {
					t.Fatal("verification budget reset", state, e)
				}
			}
		})
	}
}
