package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

func TestConversationManagementPagesFreezeIDsAndKeepUnselectedItems(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	space, err := tasks.NewSpace(t.Context(), "分页整理", SpaceSourceLocal, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 265; i++ {
		if _, err = tasks.NewConversationInSpace(t.Context(), space.ID, fmt.Sprintf("会话 %03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	ids := []string{}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, e := tasks.ManageConversations(t.Context(), ManagementQuery{SpaceID: space.ID, Limit: 70, Cursor: cursor})
		if e != nil || page.Total != 265 {
			t.Fatal(page.Total, e)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("duplicate page ID")
			}
			seen[item.ID] = true
			ids = append(ids, item.ID)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(ids) != 265 {
		t.Fatal(len(ids))
	}
	p, err := tasks.PreviewConversationManagement(t.Context(), ManagementCommand{Action: "trash", IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	later, err := tasks.NewConversationInSpace(t.Context(), space.ID, "确认后新增，不得删除")
	if err != nil {
		t.Fatal(err)
	}
	r, err := tasks.ApplyConversationManagement(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range r.Items {
		if item.Status != "completed" {
			t.Fatal("same-space batch invalidated itself", item)
		}
	}
	page, err := tasks.ManageConversations(t.Context(), ManagementQuery{SpaceID: space.ID})
	if err != nil || page.Total != 1 || page.Items[0].ID != later.ID {
		t.Fatal(page, err)
	}
	// Report actual isolated database timings without machine-dependent pass thresholds.
	t.Logf("265 conversations, four pages + frozen preview + per-item receipts: %s", time.Since(start))
}

func TestConversationPurgeDiscardsReplyCachesButPreservesUnknownDeliveryBody(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	result, err := tasks.Submit(t.Context(), chat, "ordinary", "synthetic original")
	if err != nil {
		t.Fatal(err)
	}
	caches := []string{"channel/reply-parts/" + result.Run.ID, "channel/render/telegram/fixture/reply/" + result.Run.ID + "/completed", "context/omission/" + result.Run.ID, "context/repair/" + result.Run.ID + "/fixture", "branch/request/" + chat + "/fixture"}
	for _, key := range caches {
		if _, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, key, `{"parts":["disposable body"]}`); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"sent", "unknown"} {
		if _, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "channel/operation/fixture/"+state, string(mustJSON(map[string]any{"conversation_id": chat, "run_id": result.Run.ID, "state": state, "text": "original transport body", "target": map[string]string{"source": "telegram"}}))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES('channel/pending-reply/fixture/body',$1)`, string(mustJSON(map[string]string{"run_id": result.Run.ID, "conversation_id": chat, "answer": "disposable body"}))); err != nil {
		t.Fatal(err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "trash", IDs: []string{chat}})
	manageOnce(t, tasks, ManagementCommand{Action: "purge", IDs: []string{chat}})
	var pending int
	if err = core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE key='channel/pending-reply/fixture/body'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("pending preparation retained deleted answer", pending, err)
	}
	for _, key := range []string{"channel/reply-parts/late", "channel/render/fixture/late", "channel/pending-reply/late"} {
		if _, e := core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, key, string(mustJSON(map[string]string{"conversation_id": chat, "answer": "late body"}))); e == nil {
			t.Fatal("late cache write resurrected deleted body", key)
		}
	}
	for _, key := range caches {
		var n int
		if err = core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE key=$1`, key).Scan(&n); err != nil || n != 0 {
			t.Fatal("cache retained body", key, n, err)
		}
	}
	for _, state := range []string{"sent", "unknown"} {
		var raw string
		if err = core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key=$1`, "channel/operation/fixture/"+state).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(raw, "original transport body") != (state == "unknown") {
			t.Fatal("transport evidence retention", state, raw)
		}
	}
}

func TestConversationManagementActivityIgnoresAdministrativeTimestamps(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	result, err := tasks.Submit(t.Context(), chat, "input", "old activity")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-60 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err = core.Store.db.ExecContext(t.Context(), `UPDATE fa.conversations SET created_at=$1,updated_at=$2 WHERE id=$3`, past, nowText(), chat); err != nil {
		t.Fatal(err)
	}
	if _, err = core.Store.db.ExecContext(t.Context(), `UPDATE fa.runs SET created_at=$1,updated_at=$1 WHERE id=$2`, past, result.Run.ID); err != nil {
		t.Fatal(err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "rename", IDs: []string{chat}, Title: "整理过名称"})
	page, err := tasks.ManageConversations(t.Context(), ManagementQuery{Before: time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339Nano), Entry: "local"})
	if err != nil || page.Total != 1 || page.Items[0].ActivityAt != past {
		t.Fatal("administrative change hid an old conversation", page, err)
	}
	page, err = tasks.ManageConversations(t.Context(), ManagementQuery{Entry: "telegram"})
	if err != nil || page.Total != 0 {
		t.Fatal("guessed a channel entry", page, err)
	}
}

func TestConversationManagementDoesNotApplyAnOldWorkspacePreview(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	p, err := tasks.PreviewConversationManagement(t.Context(), ManagementCommand{Action: "trash", IDs: []string{chat}})
	if err != nil {
		t.Fatal(err)
	}
	previous := core.Config.Workspace
	core.Config.Workspace = filepath.Join(t.TempDir(), "other-workspace")
	result, err := tasks.ApplyConversationManagement(t.Context(), p.ID)
	core.Config.Workspace = previous
	if err != nil || len(result.Items) != 1 || result.Items[0].Status != "skipped" {
		t.Fatal(result, err)
	}
	if _, err = core.Store.GetConversation(t.Context(), chat); err != nil {
		t.Fatal("old workspace preview retired a live conversation", err)
	}
}

func TestConversationAttachmentCleanupDoesNotRaceQueuedDelivery(t *testing.T) {
	tasks, chat, _, _ := attachmentFixture(t, nil, EchoModel{})
	ref, err := tasks.UploadAttachment(t.Context(), chat, "delivery.txt", strings.NewReader("must remain available"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.SubmitWithOptions(t.Context(), chat, "attachment", "read", RunOptions{Attachments: []string{ref.ID}}); err != nil {
		t.Fatal(err)
	}
	key := "channel/operation/fixture/file"
	if _, err = tasks.core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, key, string(mustJSON(map[string]any{"conversation_id": chat, "attachment_id": ref.ID, "state": "dispatching"}))); err != nil {
		t.Fatal(err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"conversation_id": chat, "id": ref.ID, "sha256": ref.SHA256}
	if _, err = control.ApplyDirectControlChange(t.Context(), ControlDeleteAttachment, args, chat, "test-user"); err == nil {
		t.Fatal("deleted a file being delivered")
	}
	if _, raw, e := tasks.Attachment(t.Context(), chat, ref.ID); e != nil || string(raw) != "must remain available" {
		t.Fatal(e)
	}
	if _, err = tasks.core.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value=jsonb_set(document,'{state}','"cancelled"')::text WHERE key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if _, err = control.ApplyDirectControlChange(t.Context(), ControlDeleteAttachment, args, chat, "test-user"); err != nil {
		t.Fatal(err)
	}
}

func TestConversationRestoreAndExpiryShareOneLifecycleDecision(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	for i := 0; i < 8; i++ {
		chat := conversation(t, core)
		manageOnce(t, tasks, ManagementCommand{Action: "trash", IDs: []string{chat}})
		p, err := tasks.PreviewConversationManagement(t.Context(), ManagementCommand{Action: "restore", IDs: []string{chat}})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var restored bool
		var restoreErr, expireErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			result, e := tasks.ApplyConversationManagement(t.Context(), p.ID)
			restoreErr = e
			restored = e == nil && len(result.Items) == 1 && result.Items[0].Status == "completed"
		}()
		go func() {
			defer wg.Done()
			<-start
			expireErr = core.Store.expireConversations(t.Context(), time.Now().Add(169*time.Hour))
		}()
		close(start)
		wg.Wait()
		if expireErr != nil {
			t.Fatal(expireErr)
		}
		if restoreErr != nil && !errors.Is(restoreErr, ErrActiveRun) {
			t.Fatal(restoreErr)
		}
		if err = core.Store.expireConversations(t.Context(), time.Now().Add(169*time.Hour)); err != nil {
			t.Fatal(err)
		}
		life, err := readLifecycle(t.Context(), core.Store.db, chat)
		if err != nil {
			t.Fatal(err)
		}
		if restored && life.State != "active" {
			t.Fatal("restored conversation purged by stale sweep", life)
		}
		if !restored && life.State != "purged" {
			t.Fatal("neither restore nor expiry settled", life)
		}
	}
}

func TestConversationPurgeAttachmentRetryAndIndependentBranch(t *testing.T) {
	tasks, chat, data, _ := attachmentFixture(t, nil, EchoModel{})
	ctx := t.Context()
	ref, err := tasks.UploadAttachment(ctx, chat, "保留分支.txt", strings.NewReader("branch retains its independent bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.SubmitWithOptions(ctx, chat, "attachment", "read", RunOptions{Attachments: []string{ref.ID}}); err != nil {
		t.Fatal(err)
	}
	msgs, err := tasks.Messages(ctx, chat)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := tasks.BranchConversation(ctx, chat, BranchCommand{RequestKey: "copy", MessageID: msgs[0].ID, Mode: "rerun"})
	if err != nil {
		t.Fatal(err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "trash", IDs: []string{chat}})
	if _, raw, e := tasks.ManagedAttachment(ctx, chat, ref.ID); e != nil || string(raw) != "branch retains its independent bytes" {
		t.Fatal("readonly trash download", e)
	}
	if _, _, e := tasks.Attachment(ctx, chat, ref.ID); e == nil {
		t.Fatal("normal attachment path bypassed trash", e)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "restore", IDs: []string{chat}})
	file := filepath.Join(data, "attachments", ref.ID)
	held := file + ".held"
	if err = os.Rename(file, held); err != nil {
		t.Fatal(err)
	}
	// A nonempty directory forces file removal failure on Windows and Linux.
	if err = os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	// Cleanup deliberately chmods an attachment to 0600 before unlinking. A
	// directory used as this fault fixture needs execute permission restored on
	// Unix before its child can be removed, including on an early test failure.
	t.Cleanup(func() { _ = os.Chmod(file, 0700) })
	block := filepath.Join(file, "block")
	if err = os.WriteFile(block, []byte("locked"), 0600); err != nil {
		t.Fatal(err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "trash", IDs: []string{chat}})
	manageOnce(t, tasks, ManagementCommand{Action: "purge", IDs: []string{chat}})
	var count int
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key=$1`, "conversation/file-cleanup/"+ref.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed removal lost its durable retry", count, err)
	}
	retained, err := tasks.ManageConversations(ctx, ManagementQuery{View: "retained"})
	if err != nil || len(retained.Items) != 1 || retained.Items[0].CleanupBytes != ref.Bytes {
		t.Fatal(retained, err)
	}
	if _, _, err = tasks.ManagedAttachment(ctx, chat, ref.ID); err == nil {
		t.Fatal("purged download accessible")
	}
	if len(branch.Attachments) != 1 {
		t.Fatal(branch)
	}
	if _, raw, e := tasks.Attachment(ctx, branch.Conversation.ID, branch.Attachments[0].ID); e != nil || string(raw) != "branch retains its independent bytes" {
		t.Fatal("branch bytes damaged", e)
	}
	if err = os.Chmod(file, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(block); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(held, file); err != nil {
		t.Fatal(err)
	}
	if err = tasks.core.Store.expireConversations(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("retry did not remove bytes", err)
	}
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE key=$1`, "conversation/file-cleanup/"+ref.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func manageOnce(t *testing.T, tasks *TaskService, c ManagementCommand) ManagementResult {
	t.Helper()
	p, err := tasks.PreviewConversationManagement(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	r, err := tasks.ApplyConversationManagement(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range r.Items {
		if i.Status != "completed" {
			t.Fatalf("management %s: %+v", c.Action, i)
		}
	}
	return r
}

func TestConversationRecoveryCompletesOnlyConfirmedSelfManagement(t *testing.T) {
	var chat string
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		return ModelResponse{ToolCalls: []ToolCall{{ID: "manage", Name: controlPlanTool, Arguments: mustJSON(map[string]any{"kind": ControlManageConversations, "arguments": map[string]any{"command": ManagementCommand{Action: "trash", IDs: []string{chat}}}})}}}, nil
	}))
	model := core.Model
	data := core.Store.dataDir
	if err := core.Store.Close(); err != nil {
		t.Fatal(err)
	}
	tasks, closeInitial, err := OpenTasks(t.Context(), data, Adapters{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer closeInitial()
	core = tasks.core
	chat = conversation(t, core)
	result, err := tasks.Submit(t.Context(), chat, "confirmed-self", "trash this conversation")
	if err != nil && result.Run.Status != "waiting_confirmation" {
		t.Fatal(result, err)
	}
	decision, err := previewConfirmation(t.Context(), core, result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), chat, "test")
	if err != nil {
		t.Fatal(err)
	}
	card, err := control.RenderConfirmation(t.Context(), decision.ControlOperationID, chat, "test")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate power loss after confirmed control receipt, before Run settlement.
	if _, err = control.confirmRunProposal(t.Context(), decision.ControlOperationID, ControlAuthorization{Source: ControlModelProposed, ConversationID: chat, Principal: "test", Capability: capability, Code: card.Code}); err != nil {
		t.Fatal(err)
	}
	if err = core.Store.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, closeAll, err := OpenTasks(t.Context(), data, Adapters{Model: EchoModel{}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAll()
	trash, err := recovered.ManageConversations(t.Context(), ManagementQuery{View: "trash"})
	if err != nil || trash.Total != 1 {
		t.Fatal("confirmed handoff not recovered", trash, err)
	}
	if err = recovered.recoverDeferredManagement(t.Context()); err != nil {
		t.Fatal("idempotent recovery", err)
	}
}

func TestConversationPurgePreservesUnresolvedEvenWithoutBranch(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	result, err := tasks.Submit(t.Context(), chat, "ordinary", "hello")
	if err != nil {
		t.Fatal(err)
	}
	call := ToolCall{ID: "call", Name: "write_file", Arguments: json.RawMessage(`{"path":"a.txt","content":"x"}`)}
	a := Attempt{ID: newID(), RunID: result.Run.ID, Kind: "tool", CallID: call.ID, Status: "unknown", RequestJSON: string(mustJSON(call)), ResponseJSON: `{"Content":"unknown"}`}
	if _, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.attempts(id,run_id,kind,call_id,status,request_json,response_json) VALUES($1,$2,$3,$4,$5,$6,$7)`, a.ID, a.RunID, a.Kind, a.CallID, a.Status, a.RequestJSON, a.ResponseJSON); err != nil {
		t.Fatal(err)
	}
	tx, err := core.Store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	item, err := putUnresolved(t.Context(), tx, result.Run, a, call, "tool_response")
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = tasks.ResolveUnresolved(t.Context(), chat, item.ID, item.Revision, "ignored", "", "test-user"); err != nil {
		t.Fatal(err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "trash", IDs: []string{chat}})
	manageOnce(t, tasks, ManagementCommand{Action: "purge", IDs: []string{chat}})
	replay, err := core.replayUnresolved(t.Context(), result.Run, call)
	if err != nil || !replay {
		t.Fatal("purge cleared replay guard", replay, err)
	}
	saved, err := core.Store.GetAttempt(t.Context(), a.ID)
	if err != nil || saved.Status != "unknown" || saved.RequestJSON != a.RequestJSON {
		t.Fatal(saved, err)
	}
	items, err := tasks.ManagedUnresolved(t.Context(), chat, 0)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	if err = core.Store.recover(t.Context()); err != nil {
		t.Fatal("retained evidence broke recovery", err)
	}
	if err = tasks.ManagedResolveUnresolved(t.Context(), chat, items[0].ID, items[0].Revision, "user_confirmed_applied"); err != nil {
		t.Fatal("purged evidence could not be processed", err)
	}
}

func TestConversationChannelReplacementAndLateReceiptAfterPurge(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	ctx := t.Context()
	input, err := tasks.ReceiveChannelText(ctx, ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "lifecycle-first", Purpose: "request"}, "reply")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.SelectCapabilities(ctx, input.ConversationID, CapabilitySelection{}); err != nil {
		t.Fatal(err)
	}
	var originalSelection string
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, capabilityMetaPrefix+input.ConversationID).Scan(&originalSelection); err != nil {
		t.Fatal(err)
	}
	_, origin, err := tasks.ChannelRun(ctx, result.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = b.connections["tg"].session.queueReplyGroup(ctx, result.Run, origin, "lifecycle-group", "revision", "fixture", []channelwire.TextPart{{Text: "first"}, {Text: "tail"}}, nil, false); err != nil {
		t.Fatal(err)
	}
	ops, err := b.Outbox(ctx, "tg")
	if err != nil || len(ops) != 1 {
		t.Fatal(ops, err)
	}
	op := ops[0]
	if err = b.Receipt(ctx, "tg", channelwire.Receipt{ID: op.ID, Dispatch: op.Dispatch, State: "unknown"}); err != nil {
		t.Fatal(err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "trash", IDs: []string{input.ConversationID}, Replacement: "new", CancelPending: true})
	space, err := tasks.ChannelSpace(ctx, target)
	if err != nil || space.DefaultConversationID == input.ConversationID || space.ActiveConversationID != space.DefaultConversationID || space.MultiConversation {
		t.Fatal(space, err)
	}
	var replacementSelection string
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, capabilityMetaPrefix+space.DefaultConversationID).Scan(&replacementSelection); err != nil || replacementSelection != originalSelection {
		t.Fatal("replacement lost explicit extension exclusions", replacementSelection, err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "purge", IDs: []string{input.ConversationID}})
	groups, err := tasks.ManagedDeliveries(ctx, input.ConversationID, "")
	if err != nil || len(groups) != 1 || len(groups[0].Parts) != 2 || groups[0].Parts[0].State != "unknown" {
		t.Fatal("retained delivery evidence unavailable", groups, err)
	}
	if err = b.Receipt(ctx, "tg", channelwire.Receipt{ID: op.ID, Dispatch: op.Dispatch, State: "sent", MessageID: "late-success"}); err != nil {
		t.Fatal("late receipt rejected", err)
	}
	if ops, err = b.Outbox(ctx, "tg"); err != nil || len(ops) != 0 {
		t.Fatal("tail was re-sent", ops, err)
	}
	again, err := tasks.ReceiveChannelText(ctx, ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "lifecycle-first", Purpose: "request"}, "reply")
	if err == nil && !again.Duplicate {
		t.Fatal("old input became executable", again)
	}
	if _, err = tasks.Submit(ctx, input.ConversationID, "new", "must not run"); !errors.Is(err, ErrConversationRetired) {
		t.Fatal(err)
	}
}

func TestConversationRestoreAtPinLimitKeepsHistoryWithoutRePinning(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	if err := core.Store.PinConversation(t.Context(), chat, true); err != nil {
		t.Fatal(err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "trash", IDs: []string{chat}})
	for i := 0; i < 50; i++ {
		c := conversation(t, core)
		if err := core.Store.PinConversation(t.Context(), c, true); err != nil {
			t.Fatal(err)
		}
	}
	p, err := tasks.PreviewConversationManagement(t.Context(), ManagementCommand{Action: "restore", IDs: []string{chat}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := tasks.ApplyConversationManagement(t.Context(), p.ID)
	if err != nil || len(r.Items) != 1 || r.Items[0].Status != "completed" || !strings.Contains(r.Items[0].Message, "名额已满") {
		t.Fatal(r, err)
	}
	c, err := core.Store.GetConversation(t.Context(), chat)
	if err != nil || c.Pinned {
		t.Fatal(c, err)
	}
}
func TestConversationTrashRestoreExpiryAndOldRequest(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	if _, err := tasks.Submit(t.Context(), chat, "hello", "hello"); err != nil {
		t.Fatal(err)
	}
	p, err := tasks.PreviewConversationManagement(t.Context(), ManagementCommand{Action: "trash", IDs: []string{chat}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.ApplyConversationManagement(t.Context(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = core.Store.GetConversation(t.Context(), chat); !errors.Is(err, ErrConversationRetired) {
		t.Fatal(err)
	}
	if _, err = tasks.Submit(t.Context(), chat, "blocked", "must not dispatch"); !errors.Is(err, ErrConversationRetired) {
		t.Fatal(err)
	}
	list, err := tasks.Conversations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.ID == chat {
			t.Fatal("trashed chat leaked")
		}
	}
	trash, err := tasks.ManageConversations(t.Context(), ManagementQuery{View: "trash"})
	if err != nil || trash.Total != 1 {
		t.Fatal(trash, err)
	}
	if _, err = tasks.ManagedChat(t.Context(), chat, ""); err != nil {
		t.Fatal("readonly trash", err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "restore", IDs: []string{chat}})
	if _, err = tasks.ApplyConversationManagement(t.Context(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = core.Store.GetConversation(t.Context(), chat); err != nil {
		t.Fatal("old delete reapplied", err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "trash", IDs: []string{chat}})
	if err = core.Store.expireConversations(t.Context(), time.Now().Add(167*time.Hour)); err != nil {
		t.Fatal(err)
	}
	life, err := readLifecycle(t.Context(), core.Store.db, chat)
	if err != nil || life.State != "trashed" {
		t.Fatal(life, err)
	}
	if err = core.Store.expireConversations(t.Context(), time.Now().Add(169*time.Hour)); err != nil {
		t.Fatal(err)
	}
	life, err = readLifecycle(t.Context(), core.Store.db, chat)
	if err != nil || life.State != "purged" {
		t.Fatal(life, err)
	}
	var count int
	if err = core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.messages WHERE conversation_id=$1`, chat).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestConversationManagementStaleAndDatabaseWriteGuard(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	chat := conversation(t, core)
	p, err := tasks.PreviewConversationManagement(t.Context(), ManagementCommand{Action: "trash", IDs: []string{chat}})
	if err != nil {
		t.Fatal(err)
	}
	if err = core.Store.RenameConversation(t.Context(), chat, "changed"); err != nil {
		t.Fatal(err)
	}
	r, err := tasks.ApplyConversationManagement(t.Context(), p.ID)
	if err != nil || len(r.Items) != 1 || r.Items[0].Status != "skipped" {
		t.Fatal(r, err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "trash", IDs: []string{chat}})
	if _, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.messages(conversation_id,body_json) VALUES($1,'{"Role":"user","Content":"forbidden"}')`, chat); err == nil {
		t.Fatal("database allowed hidden write")
	}
}
func TestConversationManagedSpaceDeletionAndRestore(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	space, err := core.Store.CreateSpace(t.Context(), "test", "local", "", "")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := tasks.NewConversationInSpace(t.Context(), space.ID, "inside")
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	manageOnce(t, tasks, ManagementCommand{Action: "delete_space", SpaceID: space.ID, TrashConversations: &yes})
	if _, err = core.Store.GetSpace(t.Context(), space.ID); !errors.Is(err, ErrSpaceNotFound) {
		t.Fatal(err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "restore", IDs: []string{chat.ID}})
	restored, err := core.Store.GetConversation(t.Context(), chat.ID)
	if err != nil || restored.SpaceID != LocalSpaceID {
		t.Fatal(restored, err)
	}
}

func TestConversationDeleteSpaceKeepHonorsQueuedCancellation(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	space, err := tasks.NewSpace(t.Context(), "保留会话但取消排队", SpaceSourceLocal, "", "")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := tasks.NewConversationInSpace(t.Context(), space.ID, "排队中的会话")
	if err != nil {
		t.Fatal(err)
	}
	key := "input/" + chat.ID + "/queued"
	if _, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, key, string(mustJSON(map[string]string{"conversation_id": chat.ID, "status": "pending"}))); err != nil {
		t.Fatal(err)
	}
	keep := false
	manageOnce(t, tasks, ManagementCommand{Action: "delete_space", SpaceID: space.ID, TrashConversations: &keep, CancelPending: true})
	current, err := core.Store.GetConversation(t.Context(), chat.ID)
	if err != nil || current.SpaceID != LocalSpaceID {
		t.Fatal(current, err)
	}
	var state string
	if err = core.Store.db.QueryRowContext(t.Context(), `SELECT document->>'status' FROM fa.meta WHERE key=$1`, key).Scan(&state); err != nil || state != "cancelled" {
		t.Fatal("keep-conversations resumed queued input", state, err)
	}
}
