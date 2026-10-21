package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

func chatSuggestionFixture(t *testing.T, data, workspace string) (*atomic.Int32, *atomic.Int32) {
	t.Helper()
	var planning, ordinary atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil || len(body.Messages) < 2 {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		content := "ordinary answer"
		if strings.Contains(body.Messages[0].Content, "Propose only necessary independent experts") {
			planning.Add(1)
			suggestion := harness.CollaborationSuggestion{Reason: "Independent analysis is useful.",
				Experts: []harness.ExpertSuggestion{{Name: "expert", Objective: "Initial analysis", ModelID: "default", Acceptance: "analysis"}}}
			raw, _ := json.Marshal(suggestion)
			content = string(raw)
		} else {
			ordinary.Add(1)
			for _, message := range body.Messages {
				if strings.Contains(message.Content, "Assigned objective:") {
					content = body.Model + ": expert answer"
				}
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(server.Close)
	key := fixtureKey(t, server.URL, harness.KeySourceNone, "", "")
	config := cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "initial"})
	config.Models["review"] = harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "reviewed"}
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	return &planning, &ordinary
}

func TestChatCollaborationOnceAcceptEditRejectThenOrdinary(t *testing.T) {
	for _, choice := range []string{"accept", "edit", "reject"} {
		t.Run(choice, func(t *testing.T) {
			data, workspace := initCLI(t)
			planning, ordinary := chatSuggestionFixture(t, data, workspace)
			before, _ := os.ReadFile(filepath.Join(data, "config.json"))
			review := "y\n"
			if choice == "edit" {
				review = "e\n1\nEdited analysis\nreview\ny\n"
			} else if choice == "reject" {
				review = "n\n"
			}
			output, diagnostics, err := invoke(t, "/collaboration once\ncollaborative task\n"+review+"ordinary task\n/exit\n",
				"chat", "--data-dir", data)
			if err != nil || planning.Load() != 1 || ordinary.Load() != 2 ||
				strings.Contains(diagnostics, "error:") || !strings.Contains(output, "ordinary answer") {
				t.Fatalf("output=%s diagnostics=%s planning=%d ordinary=%d err=%v",
					output, diagnostics, planning.Load(), ordinary.Load(), err)
			}
			if choice != "reject" && !strings.Contains(output, "Analysis conclusion") ||
				choice == "edit" && (!strings.Contains(output, "Edited analysis") || !strings.Contains(output, "reviewed: expert answer")) {
				t.Fatalf("review not honored: %s", output)
			}
			after, _ := os.ReadFile(filepath.Join(data, "config.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("one-task collaboration changed defaults")
			}
			tasks, closeAll, err := harness.OpenTasks(t.Context(), data, harness.Adapters{})
			if err != nil {
				t.Fatal(err)
			}
			defer closeAll()
			sessions, _ := tasks.Conversations(t.Context())
			for _, session := range sessions {
				if strings.Contains(output+diagnostics, session.ID) {
					t.Fatal("ordinary entry displayed an internal conversation ID")
				}
				run, _ := tasks.Latest(t.Context(), session.ID)
				if strings.Contains(output+diagnostics, run.ID) || strings.Contains(output, `"usage"`) {
					t.Fatal("ordinary entry displayed a ledger or run ID")
				}
				if session.Title == "Interactive chat" {
					settings, err := tasks.CollaborationSettings(t.Context(), session.ID)
					if err != nil || settings.Mode != "off" {
						t.Fatalf("once became persistent: %+v %v", settings, err)
					}
					messages, _ := tasks.Messages(t.Context(), session.ID)
					if len(messages) != 4 || run.Status != "completed" {
						t.Fatalf("same Chat did not return to single Agent: %+v %+v", messages, run)
					}
				}
			}
		})
	}
}

func TestChatAdvancedSettingsLayerAndNoHiddenModelCalls(t *testing.T) {
	for _, destination := range []string{"1", "2"} {
		t.Run(destination, func(t *testing.T) {
			data, workspace := initCLI(t)
			planning, ordinary := chatSuggestionFixture(t, data, workspace)
			script := "/settings\n3\n1\nreview\n" + destination + "\n/collaboration off\nhello\n/exit\n"
			output, diagnostics, err := invoke(t, script, "chat", "--data-dir", data)
			if err != nil || strings.Contains(diagnostics, "error:") ||
				!strings.Contains(diagnostics, "Settings saved.") || output == "" || planning.Load() != 0 || ordinary.Load() != 1 {
				t.Fatalf("advanced path=%s %s %v planning=%d ordinary=%d", output, diagnostics, err, planning.Load(), ordinary.Load())
			}
			config, _ := harness.LoadConfig(data)
			if destination == "2" && (len(config.Collaboration.Models) != 1 || config.Collaboration.Models[0] != "review") ||
				destination == "1" && len(config.Collaboration.Models) != 0 {
				t.Fatalf("wrong save scope: %+v", config.Collaboration)
			}
		})
	}
}

func TestCLIContinueReviewsSavedSuggestionWithoutPlanningReplay(t *testing.T) {
	data, workspace := initCLI(t)
	planning, ordinary := chatSuggestionFixture(t, data, workspace)
	output, diagnostics, err := invoke(t, "", "run", "--data-dir", data, "--collaboration", "ask", "--json", "goal")
	var waiting harness.RunResult
	if exitCode(err) != exitWaiting || json.Unmarshal([]byte(output), &waiting) != nil || waiting.Run.Status != "waiting_confirmation" ||
		planning.Load() != 1 || ordinary.Load() != 0 {
		t.Fatalf("saved suggestion=%s %s %v", output, diagnostics, err)
	}
	output, diagnostics, err = invoke(t, "y\n", "continue", "--data-dir", data, "--run", waiting.Run.ID, "--interactive")
	if err != nil || planning.Load() != 1 || ordinary.Load() != 1 ||
		!strings.Contains(output, "expert answer") || !strings.Contains(diagnostics, "Task: completed") ||
		strings.Contains(output+diagnostics, waiting.Run.ID) {
		t.Fatalf("continue review=%s %s %v planning=%d ordinary=%d", output, diagnostics, err, planning.Load(), ordinary.Load())
	}
}

func TestChatAuthorizedResultDeliverySynthesisThenSingle(t *testing.T) {
	data, workspace := initCLI(t)
	var planning, experts, synthesis, single atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools []any `json:"tools"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil || len(body.Messages) < 2 {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		content := "ordinary single answer"
		system := body.Messages[0].Content
		var input strings.Builder
		for _, message := range body.Messages[1:] {
			input.WriteString(message.Content)
			input.WriteByte('\n')
		}
		prompt := input.String()
		switch {
		case strings.Contains(system, "Propose only necessary independent experts"):
			planning.Add(1)
			suggestion := harness.CollaborationSuggestion{Reason: "Independent analyses followed by review.",
				Experts: []harness.ExpertSuggestion{
					{Name: "one", Objective: "Analyze FIRST", ModelID: "default", Acceptance: "analysis"},
					{Name: "two", Objective: "Analyze SECOND", ModelID: "default", Acceptance: "analysis"},
					{Name: "review", Objective: "Review BOTH", ModelID: "default", Acceptance: "analysis",
						DependsOn: []string{"one", "two"}, Results: []harness.ResultReference{
							{Source: "one", Scope: "summary", MaxBytes: 1024},
							{Source: "two", Scope: "summary", MaxBytes: 1024},
						}},
				}}
			raw, _ := json.Marshal(suggestion)
			content = string(raw)
		case strings.Contains(system, "Synthesize only the authorized saved public results"):
			synthesis.Add(1)
			if len(body.Tools) != 0 || !strings.Contains(prompt, "FIRST_PUBLIC") || !strings.Contains(prompt, "SECOND_PUBLIC") {
				t.Error("synthesis lost sources or acquired tools")
			}
			content = "COMBINED_VIEW"
		case strings.Contains(prompt, "Assigned objective:"):
			experts.Add(1)
			switch {
			case strings.Contains(prompt, "Analyze FIRST"):
				content = "FIRST_PUBLIC"
			case strings.Contains(prompt, "Analyze SECOND"):
				content = "SECOND_PUBLIC"
			default:
				if !strings.Contains(prompt, "FIRST_PUBLIC") || !strings.Contains(prompt, "SECOND_PUBLIC") {
					t.Error("review did not receive its authorized results")
				}
				content = "BOTH_REVIEWED"
			}
		default:
			single.Add(1)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	key := fixtureKey(t, server.URL, harness.KeySourceNone, "", "")
	config := cliConfig(workspace, harness.ModelConfig{Provider: "openai-compatible", Key: key, Model: "fixture"})
	if err := harness.WriteConfig(data, config); err != nil {
		t.Fatal(err)
	}
	script := "/settings advanced\n4\ny\n1\n/collaboration once\ncross-domain analysis\ny\nordinary task\n/exit\n"
	output, diagnostics, err := invoke(t, script, "chat", "--data-dir", data)
	if err != nil || strings.Contains(diagnostics, "error:") || planning.Load() != 1 || experts.Load() != 3 ||
		synthesis.Load() != 1 || single.Load() != 1 || !strings.Contains(output, "COMBINED_VIEW") ||
		!strings.Contains(output, "ordinary single answer") || !strings.Contains(output, "Runtime facts") {
		t.Fatalf("Chat flow=%s %s err=%v calls=%d/%d/%d/%d",
			output, diagnostics, err, planning.Load(), experts.Load(), synthesis.Load(), single.Load())
	}
}
