package harness

import (
	"errors"
	"strings"
	"testing"
)

func TestContextSummarySlotsDoNotReplaceOtherAudience(t *testing.T) {
	core, tasks, chat, _ := summaryFixture(t)
	full, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 1024})
	if err != nil || full.State.Scope != "full" {
		t.Fatal(full, err)
	}
	original, err := core.Store.summaryRaw(t.Context(), chat, "full")
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.SetSpacePermission(t.Context(), LocalSpaceID, PermissionNone, false); err != nil {
		t.Fatal(err)
	}
	core.permission = nil
	public, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 1024})
	if err != nil || !public.Changed || public.State.Scope != "public_text" || public.State.Summary == nil {
		t.Fatalf("public maintenance: %+v %v", public, err)
	}
	retained, err := core.Store.summaryRaw(t.Context(), chat, "full")
	if err != nil || retained != original {
		t.Fatal("restricted summary replaced full summary", err)
	}
	if err = tasks.SetSpacePermission(t.Context(), LocalSpaceID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	state, err := tasks.ContextState(t.Context(), chat)
	if err != nil || state.Scope != "full" || state.Summary == nil || state.Summary.Revision != full.State.Summary.Revision {
		t.Fatal("full checkpoint was lost", state, err)
	}
}

func TestClearedSummarySlotDoesNotReviveLegacySummary(t *testing.T) {
	core, tasks, chat, _ := summaryFixture(t)
	if _, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "compact", SummaryBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	raw, err := core.Store.summaryRaw(t.Context(), chat)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, summaryMetaPrefix+chat, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = core.Store.db.ExecContext(t.Context(), `DELETE FROM fa.meta WHERE key=$1`, summarySlotKey(chat, "full")); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.ManageContext(t.Context(), chat, ContextCommand{Action: "clear"})
	if err != nil || !result.Changed || result.State.Summary != nil || result.State.Status != "none" {
		t.Fatal("legacy summary revived", result, err)
	}
	if s, _, e := core.Store.contextSummary(t.Context(), chat, true); e != nil || s != nil {
		t.Fatal("restart read revived legacy", s, e)
	}
}

func TestInputBudgetChangeInvalidatesCompactionPause(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	before, err := core.compactionFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	core.Config.MaxContextBytes *= 2
	after, err := core.compactionFingerprint()
	if err != nil || before == after {
		t.Fatal("input budget is missing from pause fingerprint", err)
	}
}

func TestNewConversationRoutingFailureRollsBackCreation(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	original := conversation(t, core)
	if err := installPGFault(t, core.Store, "reject_create_route", "spaces", "UPDATE", "NEW.active_conversation_id IS DISTINCT FROM OLD.active_conversation_id"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Store.CreateActiveConversation(t.Context(), LocalSpaceID, "fixture", core.Config.Workspace, "rollback"); err == nil {
		t.Fatal("expected injected routing error")
	}
	var count int
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.conversations WHERE space_id=$1`, LocalSpaceID).Scan(&count); err != nil || count != 1 {
		t.Fatal("orphan conversation after routing failure", count, err)
	}
	space, err := core.Store.GetSpace(t.Context(), LocalSpaceID)
	if err != nil || space.ActiveConversationID != original {
		t.Fatal(space, err)
	}
}

func TestContextStopCodesSeparateProtocolAndCapacity(t *testing.T) {
	err := &ContextBudgetError{Code: "history_context_full", Usage: ContextUsage{Resource: "bytes"}}
	if runStopCode(err.Error()) != "context_payload_limit" {
		t.Fatal(err)
	}
	message := channelRunStatus(Run{Status: "limited", StopReason: err.Error()})
	if !strings.Contains(message, "字节保护") || strings.Contains(message, "思考额度") {
		t.Fatal(message)
	}
	if !strings.Contains(channelRunStatus(Run{Status: "failed", StopReason: ErrContextStructure.Error()}), "结构") {
		t.Fatal("protocol failure mislabeled")
	}
	if !errors.Is(err, ErrContextLimit) {
		t.Fatal("budget error lost compatibility")
	}
}
