package harness

import (
	"strings"
	"testing"
)

func TestQueryIndexesUseHistoryLookupPaths(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tx, err := core.Store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `SET LOCAL enable_seqscan=off`); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct{ query, index string }{
		{`SELECT id FROM fa.attempts WHERE run_id='run' ORDER BY seq`, "attempts_run_id_seq_key"},
		{`SELECT run_id FROM fa.attempts WHERE status='unknown'`, "attempts_unknown"},
		{`SELECT id FROM fa.messages WHERE run_id='run' ORDER BY id`, "messages_run"},
		{`SELECT id FROM fa.runs WHERE conversation_id='chat' ORDER BY seq DESC LIMIT 1`, "runs_conversation_id_seq_key"},
	} {
		rows, err := tx.QueryContext(t.Context(), "EXPLAIN "+scenario.query)
		if err != nil {
			t.Fatal(err)
		}
		var plan string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan += line
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, scenario.index) {
			t.Fatalf("%s: %s", scenario.query, plan)
		}
	}
}
