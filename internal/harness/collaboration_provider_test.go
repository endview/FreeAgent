package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollaborationProviderWireFixtures(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			const secret = "collaboration-fixture-credential"
			t.Setenv("HARNESS_COLLAB_KEY", secret)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				var body openAIRequest
				if json.NewDecoder(request.Body).Decode(&body) != nil || len(body.Messages) == 0 ||
					len(body.Tools) != 1 || body.Tools[0].Function.Name != "file_read" || !body.Stream ||
					body.Model != "fixture" || body.MaxTokens == 0 || request.Header.Get("Authorization") != "Bearer "+secret {
					t.Error("child request lost its selected model, tool, budget or credential")
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
				rawRequest, _ := json.Marshal(body)
				if strings.Contains(string(rawRequest), "PARENT_PRIVATE") {
					t.Error("parent context leaked into provider request")
				}
				response := ModelResponse{Content: `{"summary":"Read authorized facts.","findings":[]}`}
				finish := "stop"
				if body.Messages[len(body.Messages)-1].Role == "user" {
					response.Content = ""
					response.ToolCalls = []ToolCall{makeCall("read", "file_read", map[string]string{"path": "facts.txt"})}
					finish = "tool_calls"
				}
				field := "message"
				if streaming {
					field = "delta"
					writer.Header().Set("Content-Type", "text/event-stream")
				} else {
					writer.Header().Set("Content-Type", "application/json")
				}
				message := openAIMessage{Role: "assistant", Content: response.Content, ToolCalls: toOpenAIToolCalls(response.ToolCalls)}
				raw, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, field: message, "finish_reason": finish}}})
				if err != nil {
					t.Error(err)
					writer.WriteHeader(http.StatusInternalServerError)
					return
				}
				if streaming {
					writeSSE(writer, string(raw))
					_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
				} else {
					_, _ = writer.Write(raw)
				}
			}))
			defer server.Close()
			keys := testKeyStore(t)
			config := ModelConfig{Provider: "openai-compatible", Key: addTestProfile(t, keys, server.URL, KeySourceEnv, "HARNESS_COLLAB_KEY", ""), Model: "fixture"}
			collab, core, plan := collaborationFixture(t, keyedClient(t, config, keys, nil))
			core.Keys = keys
			core.Config.Models[core.Config.DefaultModel] = config
			plan.Parallel = true
			id := createCollaboration(t, collab, plan)
			report, err := collab.Continue(t.Context(), id)
			if err != nil || report.Parent.Status != "completed" || report.Usage.ModelCalls != 4 ||
				report.Usage.ToolCalls != 2 || calls.Load() != 4 {
				t.Fatalf("wire collaboration not accepted: usage=%+v calls=%d err=%v", report.Usage, calls.Load(), err)
			}
			for _, child := range report.Children {
				if len(child.Observed) != 1 || child.Observed[0].Path != "facts.txt" {
					t.Fatalf("missing actual authorized file observation: %s", child.Spec.Name)
				}
				attempts, err := core.Store.ListAttempts(t.Context(), child.Run.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, attempt := range attempts {
					if attempt.Status != "finished" || strings.Contains(attempt.RequestJSON+attempt.ResponseJSON, secret) {
						t.Fatal("unresolved attempt or credential value retained in execution records")
					}
				}
			}
			if _, err := collab.Continue(t.Context(), id); err != nil || calls.Load() != 4 {
				t.Fatalf("completed wire task replayed: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestLiveProviderCollaboration(t *testing.T) {
	config, keys, _ := liveProviderConfig(t)
	collab, core, plan := collaborationFixture(t, keyedClient(t, config, keys, nil))
	core.Keys = keys
	core.Config.Models[core.Config.DefaultModel] = config
	core.Config.instructionText = ""
	role := plan.Roles["reviewer"]
	role.Budget = ChildBudget{MaxSteps: 8, MaxSeconds: 120, MaxOutputBytes: 65536, MaxCallOutputBytes: 16384}
	role.Tools = []string{"file_read"}
	plan.Roles["reviewer"] = role
	plan.Budget = ParentBudget{MaxChildren: 2, MaxModelCalls: 16, MaxToolCalls: 16, MaxOutputBytes: 1 << 20, MaxConcurrentChildren: 2, MaxActiveSeconds: 60}
	plan.Parallel = true
	for index := range plan.Children {
		plan.Children[index].Objective = "Use file_read on facts.txt. Then return exactly the declared JSON summary and findings. Do not state that a check or edit ran."
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	id, err := collab.Create(ctx, "", "live-collaboration", plan)
	if err != nil {
		t.Fatal(err)
	}
	report, err := collab.Continue(ctx, id)
	if err != nil || report.Parent.Status != "completed" || len(report.Children) != 2 || report.Usage.ToolCalls < 2 {
		t.Fatalf("live collaboration NOT ACCEPTED: status=%s usage=%+v err=%v", report.Parent.Status, report.Usage, err)
	}
	for _, child := range report.Children {
		if len(child.Observed) == 0 || child.Run.Status != "completed" || child.Observed[0].Path != "facts.txt" {
			t.Fatalf("child %s lacks the required actual file observation", child.Spec.Name)
		}
	}
}

func TestCollaborationChildBindingHasBaseURL(t *testing.T) {
	const base = "http://127.0.0.1:1"
	keys := testKeyStore(t)
	id := addTestProfile(t, keys, base, KeySourceNone, "", "")
	collab, core, plan := collaborationFixture(t, modelFunc(readingChild))
	core.Keys = keys
	core.Config.Models[core.Config.DefaultModel] = ModelConfig{Provider: "openai-compatible", Key: id, Model: "fixture"}
	report, err := collab.Continue(t.Context(), createCollaboration(t, collab, plan))
	if err != nil || report.Parent.Status != "completed" || len(report.Children) != 2 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	for _, child := range report.Children {
		run, err := core.Store.GetRun(t.Context(), child.Run.ID)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := decodeRunSnapshot(run.ConfigJSON)
		if err != nil {
			t.Fatal(err)
		}
		keyed := 0
		for _, binding := range snapshot.Bindings {
			if binding.Config.Key == id {
				keyed++
				if binding.BaseURL != base {
					t.Fatalf("child %s binding base=%q", child.Spec.Name, binding.BaseURL)
				}
			}
		}
		if keyed == 0 {
			t.Fatalf("child %s has no keyed binding: %+v", child.Spec.Name, snapshot.Bindings)
		}
	}
}
