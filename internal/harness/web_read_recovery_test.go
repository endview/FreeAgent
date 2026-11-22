package harness

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/webaccess"
)

func TestWebFetchFailureLetsIndependentSourcesContinue(t *testing.T) {
	for _, reread := range []bool{false, true} {
		name := "no_retry"
		if reread {
			name = "explicit_new_read"
		}
		t.Run(name, func(t *testing.T) {
			const a, b, c = "https://example.org/a", "https://example.org/b", "https://example.org/c"
			want := []string{a, b, c}
			if reread {
				want = []string{a, a, b, c}
			}
			var fetched []string
			core, _, _ := testHarness(t, modelFunc(func(_ context.Context, req ModelRequest, _ func(string)) (ModelResponse, error) {
				i := len(fetched)
				if i > 0 && !slices.ContainsFunc(req.Messages, func(msg ModelMessage) bool {
					return msg.Role == "tool" && strings.Contains(msg.Content, "Public page not read")
				}) {
					t.Fatal("model did not receive the failed read result")
				}
				if i == len(want) {
					return ModelResponse{Content: "Other sources read."}, nil
				}
				return ModelResponse{ToolCalls: []ToolCall{makeCall(string(rune('a'+i)), "web_fetch", map[string]string{"url": want[i]})}}, nil
			}))
			policy := webaccess.DefaultConfig()
			policy.Mode = "auto"
			core.Config.WebSearch, core.Config.Tools.Network = &policy, true
			core.Tools = &WebTools{Config: &policy, fetch: func(_ context.Context, url string, _ webaccess.Config) (webaccess.Page, error) {
				fetched = append(fetched, url)
				if len(fetched) == 1 {
					return webaccess.Page{URL: url}, &webaccess.FetchError{Unknown: true}
				}
				return webaccess.Page{URL: url, Content: "public fixture"}, nil
			}}
			tasks := NewTaskService(core)
			result, err := tasks.Submit(t.Context(), "", "sources", "Read independent sources")
			for i := range want {
				if err != nil || result.Run.Status != "waiting_confirmation" || len(fetched) != i {
					t.Fatal("unexpected dispatch or approval state", result.Run.Status, fetched, err)
				}
				preview, err := tasks.Preview(t.Context(), result.Run.ID)
				if err != nil || preview.Tool != "web_fetch" || !strings.Contains(preview.Preview, want[i]) {
					t.Fatal(preview, err)
				}
				result, err = tasks.Confirm(t.Context(), result.Run.ID, preview, true)
				if err != nil {
					t.Fatal(err)
				}
			}
			if result.Run.Status != "completed" || !slices.Equal(fetched, want) {
				t.Fatal(result.Run.Status, fetched)
			}
			attempts, err := core.Store.ListAttempts(t.Context(), result.Run.ID)
			if err != nil || len(attempts) != len(want)*2+1 {
				t.Fatal(len(attempts), err)
			}
			var failed ToolResult
			if attempts[1].Status != "finished" || json.Unmarshal([]byte(attempts[1].ResponseJSON), &failed) != nil || failed.Outcome == nil || failed.Outcome.Status != "failed" || len(failed.Receipts) != 1 || failed.Receipts[0].Status != "failed" {
				t.Fatal(attempts[1])
			}
		})
	}
}

func TestWebFetchRecoveryClosesOnlyRecordedPublicRead(t *testing.T) {
	for _, variant := range []struct{ name, tool, intent, runStatus string }{
		{"public_read", "web_fetch", "reading", "stopped"},
		{"legacy_read", "web_fetch", "unknown", "unknown"},
		{"other_http", "http_get", "reading", "unknown"},
	} {
		t.Run(variant.name, func(t *testing.T) {
			core, _, _ := testHarness(t, EchoModel{})
			ctx, store := t.Context(), core.Store
			snapshot, err := core.snapshot()
			if err != nil {
				t.Fatal(err)
			}
			run, _, err := store.CreateRun(ctx, conversation(t, core), "interrupt", "Read a source", snapshot)
			if err != nil {
				t.Fatal(err)
			}
			call := makeCall("read", variant.tool, map[string]string{"url": "https://example.org/"})
			model, err := store.startModel(ctx, run, "{}", 0)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.saveModel(ctx, run, model, ModelResponse{ToolCalls: []ToolCall{call}}); err != nil {
				t.Fatal(err)
			}
			attempt, err := store.startTool(ctx, run, call)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.dispatchTool(ctx, attempt, 0); err != nil {
				t.Fatal(err)
			}
			intent := string(mustJSON(ToolResult{Receipts: []ToolReceipt{{Kind: "http", URL: "https://example.org/", Status: variant.intent}}}))
			if _, err = store.db.ExecContext(ctx, `UPDATE fa.attempts SET response_json=$1 WHERE id=$2`, intent, attempt.ID); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err = store.recover(ctx); err != nil {
					t.Fatal(err)
				}
			}
			recovered, err := store.GetRun(ctx, run.ID)
			if err != nil || recovered.Status != variant.runStatus {
				t.Fatal(recovered.Status, err)
			}
			got, err := store.GetAttempt(ctx, attempt.ID)
			if err != nil {
				t.Fatal(err)
			}
			var messages int
			if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.messages WHERE run_id=$1 AND body_json::jsonb->>'Role'='tool'`, run.ID).Scan(&messages); err != nil {
				t.Fatal(err)
			}
			if variant.runStatus == "unknown" {
				if got.Status != "unknown" || got.ResponseJSON != intent || messages != 0 {
					t.Fatal(got, messages)
				}
			} else if got.Status != "finished" || messages != 1 || !strings.Contains(got.ResponseJSON, "No automatic retry") {
				t.Fatal(got, messages)
			}
		})
	}
}
