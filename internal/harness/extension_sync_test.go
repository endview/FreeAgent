package harness

import (
	"testing"
)

func TestExtensionFactSyncCursorIsBoundAndDoesNotReplay(t *testing.T) {
	core, tasks, _ := affectHost(t)
	chat := conversation(t, core)
	for n := 0; n < 34; n++ {
		if _, err := tasks.Submit(t.Context(), chat, string(rune('a'+n)), "hello"); err != nil {
			t.Fatal(err)
		}
	}
	before := affectState(t, tasks, chat)
	first, err := tasks.SyncExtensionFacts(t.Context(), affectExtensionID, chat, "maya", "")
	if err != nil || first.Processed != 32 || first.Next == "" || first.Replayed {
		t.Fatal(first, err)
	}
	if _, err = tasks.SyncExtensionFacts(t.Context(), affectExtensionID, chat, "another", first.Next); err == nil {
		t.Fatal("cursor reused for a different role")
	}
	last, err := tasks.SyncExtensionFacts(t.Context(), affectExtensionID, chat, "maya", first.Next)
	if err != nil || last.Processed != 2 || last.Next != "" || last.Replayed {
		t.Fatal(last, err)
	}
	after := affectState(t, tasks, chat)
	if before.Revision != after.Revision {
		t.Fatal("sync re-adopted inputs", before, after)
	}
	var runs int
	err = core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.runs WHERE conversation_id=$1`, chat).Scan(&runs)
	if err != nil || runs != 34 {
		t.Fatal("sync replayed work", runs, err)
	}
}
