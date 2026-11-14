package harness

import (
	"encoding/json"
	"testing"
)

func TestGeneratedCandidateOnlyBecomesHistoryAfterConfirmedPublication(t *testing.T) {
	for _, state := range []string{"queued", "failed", "unknown"} {
		t.Run(state, func(t *testing.T) {
			s, _, _ := testHarness(t, EchoModel{})
			chat := conversation(t, s)
			snapshot, _ := s.snapshot()
			var config map[string]any
			_ = json.Unmarshal([]byte(snapshot), &config)
			config["extension_trigger"] = map[string]any{"kind": "scheduled", "extension": "com.example.fixture"}
			run, _, err := s.Store.CreateRun(t.Context(), chat, "generated", "automatic trigger, not a new human request", snapshot)
			if err != nil {
				t.Fatal(err)
			}
			run.ConfigJSON = string(mustJSON(config))
			if _, err = s.Store.db.ExecContext(t.Context(), `UPDATE fa.runs SET config_json=$1 WHERE id=$2`, run.ConfigJSON, run.ID); err != nil {
				t.Fatal(err)
			}
			attempt, err := s.Store.startModel(t.Context(), run, `{}`, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Store.saveModel(t.Context(), run, attempt, ModelResponse{Content: "generated original message"}); err != nil {
				t.Fatal(err)
			}
			// Simulate old rows lacking type tags: the one compatibility projection
			// uses their exact source Run, never their physical neighbor.
			if _, err = s.Store.db.ExecContext(t.Context(), `UPDATE fa.messages SET body_json=body_json-'kind' WHERE run_id=$1`, run.ID); err != nil {
				t.Fatal(err)
			}
			body := ModelMessage{Role: "assistant", Content: "generated original message", Public: true, Channel: &ChannelMessageSource{Purpose: "delivery"}}
			receipt := func(state string) {
				t.Helper()
				tx, err := s.Store.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if err = saveChannelReceipt(t.Context(), tx, "generated-delivery", chat, run.ID, state, &body); err != nil {
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			receipt(state)
			messages, err := s.Store.ListMessages(t.Context(), chat)
			if err != nil {
				t.Fatal(err)
			}
			projected, err := projectContextMessages(messages, "", "", false)
			if err != nil || len(projected) != 0 {
				t.Fatal("unsent candidate became spoken history", projected, err)
			}
			receipt("sent")
			receipt("sent")
			messages, err = s.Store.ListMessages(t.Context(), chat)
			if err != nil {
				t.Fatal(err)
			}
			projected, err = projectContextMessages(messages, "", "", false)
			if err != nil || len(projected) != 1 || projected[0].Kind != messagePublished || projected[0].Content != body.Content {
				t.Fatal("confirmed original was lost or duplicated", projected, err)
			}
		})
	}
}
