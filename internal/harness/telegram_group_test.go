package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/endview/freeagent/internal/channelwire"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func groupBridge(t *testing.T, model ModelClient) (*ChannelBridge, *TaskService, ChannelTarget) {
	b, tasks, target := expressionBridge(t, model)
	target.ChatID = "-100"
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "group", Enabled: true, Members: []string{"telegram:user:7", "telegram:user:8"}})
	return b, tasks, target
}

func TestTelegramGroupQueuePreservesMemberAndAdoptionOrder(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var requests []ModelRequest
	var modelMu sync.Mutex
	b, tasks, target := groupBridge(t, modelFunc(func(ctx context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		modelMu.Lock()
		requests = append(requests, r)
		first := len(requests) == 1
		modelMu.Unlock()
		if first {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ModelResponse{}, ctx.Err()
			}
		}
		return ModelResponse{Content: "done"}, nil
	}))
	target.ThreadID = "11"
	var mu sync.Mutex
	busy := false
	done := make(chan RunResult, 1)
	fail := make(chan error, 1)
	b.connections["tg"].session.setDispatch(func(_ string, call func(context.Context) (RunResult, error)) error {
		mu.Lock()
		if busy {
			mu.Unlock()
			return ErrActiveRun
		}
		busy = true
		mu.Unlock()
		go func() {
			r, e := call(t.Context())
			mu.Lock()
			busy = false
			mu.Unlock()
			done <- r
			fail <- e
		}()
		return nil
	})
	if err := b.Inbound(t.Context(), "tg", groupEvent(target, 1, "member-seven-request")); err != nil {
		t.Fatal(err)
	}
	<-entered
	other := groupEvent(target, 2, "member-eight-request")
	other.Person = "telegram:user:8"
	if err := b.Inbound(t.Context(), "tg", other); err != nil {
		t.Fatal(err)
	}
	for n := 3; n <= 21; n++ {
		item := groupEvent(target, n, fmt.Sprintf("queued-eight-%d", n))
		item.Person = "telegram:user:8"
		if err := b.Inbound(t.Context(), "tg", item); err != nil {
			t.Fatal(err)
		}
	}
	overflow := groupEvent(target, 22, "OVERFLOW_NEVER_ADOPTED")
	overflow.Person = "telegram:user:8"
	if err := b.Inbound(t.Context(), "tg", overflow); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	full := b.connections["tg"].diagnostic.Counts["queue_full"]
	b.mu.Unlock()
	if full != 1 {
		t.Fatal("queue overflow not diagnosed", full)
	}
	close(release)
	first := <-done
	if err := <-fail; err != nil {
		t.Fatal(err)
	}
	state, err := tasks.Inputs(t.Context(), first.Run.ConversationID)
	if err != nil || len(state.Entries) != 20 || state.Entries[0].Person != "telegram:user:8" || state.Entries[0].Action != "queue" {
		t.Fatal(state, err)
	}
	modelMu.Lock()
	if strings.Contains(string(mustJSON(requests[0].Messages)), "member-eight-request") {
		t.Error("unadopted member input reached first Run")
	}
	modelMu.Unlock()
	second, err := tasks.RunQueued(t.Context(), first.Run.ConversationID)
	if err != nil || second.Run.Permission == nil || second.Run.Permission.PersonID != "telegram:user:8" {
		t.Fatal(second.Run, err)
	}
	_, origin, err := tasks.ChannelRun(t.Context(), second.Run.ID)
	if err != nil || origin == nil || origin.ChannelTarget != target {
		t.Fatal(origin, err)
	}
	firstOrigin := channelOriginFromJSON(first.Run.ConfigJSON)
	if firstOrigin == nil || origin.InputMessageID <= firstOrigin.HistoryThrough {
		t.Fatal("queued adoption did not advance projection")
	}
}
func groupEvent(target ChannelTarget, n int, text string) channelwire.Event {
	return channelwire.Event{ID: fmt.Sprint(n), Target: channelwire.Target(target), Person: "telegram:user:7", Message: fmt.Sprint(n), Kind: "group", Text: text, Trigger: true}
}

func TestTelegramTopicManagementReplacesOnlyItsRouteAndSpaceDeletionClearsRoutes(t *testing.T) {
	_, tasks, target := groupBridge(t, EchoModel{})
	ctx := t.Context()
	target.ThreadID = "11"
	first, err := tasks.ChannelConversation(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	other := target
	other.ThreadID = "22"
	second, err := tasks.ChannelConversation(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	manageOnce(t, tasks, ManagementCommand{Action: "trash", IDs: []string{first}, Replacement: "new"})
	replacement, err := tasks.ChannelConversation(ctx, target)
	if err != nil || replacement == first {
		t.Fatal(replacement, err)
	}
	otherCurrent, _ := tasks.ChannelConversation(ctx, other)
	if otherCurrent != second {
		t.Fatal("management changed another topic")
	}
	space, _ := tasks.ChannelSpace(ctx, target)
	yes := true
	manageOnce(t, tasks, ManagementCommand{Action: "delete_space", SpaceID: space.ID, TrashConversations: &yes})
	var count int
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE starts_with(key,$1)`, "connector/telegram/"+target.key()+"/topic/").Scan(&count); err != nil || count != 0 {
		t.Fatal("space deletion retained routing", count, err)
	}
}

func TestTelegramMigrationClosesOnlyUndispatchedApprovalAndNewChatContinues(t *testing.T) {
	calls := 0
	b, tasks, target := groupBridge(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		if calls == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("write", "file_create", map[string]any{"path": "never-created.txt", "content": "test"})}}, nil
		}
		return ModelResponse{Content: "new group continues"}, nil
	}))
	ctx := t.Context()
	space, _ := tasks.ChannelSpace(ctx, target)
	if err := tasks.SetSpacePermission(ctx, space.ID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetPersonPermission(ctx, "telegram:user:7", "fixture", PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	if err := b.Inbound(ctx, "tg", groupEvent(target, 1, "create file")); err != nil {
		t.Fatal(err)
	}
	chat, _ := tasks.ChannelConversation(ctx, target)
	old, err := tasks.Latest(ctx, chat)
	if err != nil || old.Status != "waiting_confirmation" {
		t.Fatal(old.Status, err)
	}
	fact := groupEvent(target, 2, "")
	fact.Lifecycle = &channelwire.Lifecycle{Kind: "migration", NewChatID: "-100300"}
	if err = b.Inbound(ctx, "tg", fact); err != nil {
		t.Fatal(err)
	}
	target.ChatID = "-100300"
	if err = b.Inbound(ctx, "tg", groupEvent(target, 3, "different work")); err != nil {
		t.Fatal(err)
	}
	before, _ := tasks.core.Store.GetRun(ctx, old.ID)
	latest, _ := tasks.Latest(ctx, chat)
	if before.Status != "abandoned" || before.StopReason != "channel_authority_changed" || latest.ID == old.ID || latest.Status != "completed" {
		t.Fatal(before.Status, before.StopReason, latest.Status)
	}
	if _, err = os.Stat(filepath.Join(tasks.Config().Workspace, "never-created.txt")); !os.IsNotExist(err) {
		t.Fatal("unapproved action ran", err)
	}
}

func TestTelegramTopicRoutesSurviveNewSwitchAndCrossTopicIsRejected(t *testing.T) {
	var requests []ModelRequest
	b, tasks, target := groupBridge(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, r)
		return ModelResponse{Content: "reply"}, nil
	}))
	ctx := t.Context()
	for i, topic := range []string{"general", "11", "22", "11"} {
		target.ThreadID = topic
		if err := b.Inbound(ctx, "tg", groupEvent(target, i+1, "topic-"+topic)); err != nil {
			t.Fatal(err)
		}
	}
	if len(requests) != 4 {
		t.Fatal(len(requests))
	}
	for _, r := range requests {
		joined := string(mustJSON(r.Messages))
		if strings.Contains(joined, "topic-11") && strings.Contains(joined, "topic-22") {
			t.Fatal("cross-topic context")
		}
	}
	target.ThreadID = "11"
	first, err := tasks.ChannelConversation(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	space, _ := tasks.ChannelSpace(ctx, target)
	if err = tasks.SetSpaceMultiConversation(ctx, space.ID, true); err != nil {
		t.Fatal(err)
	}
	created, err := tasks.newChannelConversation(ctx, target, "", "stable-request")
	if err != nil {
		t.Fatal(err)
	}
	again, err := tasks.newChannelConversation(ctx, target, "", "stable-request")
	if err != nil || again.ID != created.ID {
		t.Fatal(again, err)
	}
	if err = tasks.setChannelConversation(ctx, target, first, false); err != nil {
		t.Fatal(err)
	}
	target.ThreadID = "22"
	if err = tasks.setChannelConversation(ctx, target, first, false); err == nil {
		t.Fatal("cross-topic switch accepted")
	}
	other, _ := tasks.ChannelConversation(ctx, target)
	if other == first || other == created.ID {
		t.Fatal("topic pointer changed")
	}
	for _, topic := range []string{"general", "11", "22"} {
		target.ThreadID = topic
		chat, _ := tasks.ChannelConversation(ctx, target)
		actual, e := channelConversationTarget(ctx, tasks.core.Store.db, chat, target)
		if e != nil || actual != target {
			t.Fatal(actual, e)
		}
	}
}

func TestTelegramBackgroundPolicyTTLTopicIsolationAndClear(t *testing.T) {
	var requests []ModelRequest
	b, tasks, target := groupBridge(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		requests = append(requests, r)
		return ModelResponse{Content: "reply"}, nil
	}))
	ctx := t.Context()
	target.ThreadID = "11"
	e := groupEvent(target, 1, "unsolicited-secret")
	e.Trigger = false
	if err := b.Inbound(ctx, "tg", e); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE value LIKE '%unsolicited-secret%'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	state, err := tasks.ApplyTelegramGroup(ctx, target.group(), false, true, 0, "opt-in")
	if err != nil {
		t.Fatal(err)
	}
	e = groupEvent(target, 2, "opted-in-context")
	e.Trigger = false
	if err = b.Inbound(ctx, "tg", e); err != nil {
		t.Fatal(err)
	}
	if err = b.Inbound(ctx, "tg", groupEvent(target, 3, "question")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustJSON(requests[len(requests)-1].Messages)), "opted-in-context") {
		t.Fatal("background not provided")
	}
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.attempts WHERE request_json LIKE '%opted-in-context%'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("ephemeral text retained in model attempt", count, err)
	}
	target.ThreadID = "22"
	if err = b.Inbound(ctx, "tg", groupEvent(target, 4, "other topic")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustJSON(requests[len(requests)-1].Messages)), "opted-in-context") {
		t.Fatal("background crossed topic")
	}
	prefix := telegramBackgroundPrefix(target)
	if _, err = tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=jsonb_set(document,'{expires}',to_jsonb($1::bigint))::text WHERE starts_with(key,$2)`, time.Now().Add(-time.Hour).UnixMilli(), prefix); err != nil {
		t.Fatal(err)
	}
	target.ThreadID = "11"
	if err = b.Inbound(ctx, "tg", groupEvent(target, 5, "after expiry")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustJSON(requests[len(requests)-1].Messages)), "opted-in-context") {
		t.Fatal("expired background provided")
	}
	if _, err = tasks.ApplyTelegramGroup(ctx, target.group(), false, false, state.Revision, "off"); err != nil {
		t.Fatal(err)
	}
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.meta WHERE starts_with(key,$1)`, prefix).Scan(&count); err != nil || count != 0 {
		t.Fatal("off retained text", count, err)
	}
}

func TestTelegramMigrationIdempotentAndMembershipStopsDispatch(t *testing.T) {
	b, tasks, target := groupBridge(t, EchoModel{})
	ctx := t.Context()
	chat, _ := tasks.ChannelConversation(ctx, target)
	op, err := b.perform(ctx, "tg", channelwire.Operation{Kind: "text", Target: channelwire.Target(target), Text: "not yet dispatched"})
	if !isChannelQueued(err) {
		t.Fatal(err)
	}
	fact := groupEvent(target, 20, "")
	fact.Lifecycle = &channelwire.Lifecycle{Kind: "migration", NewChatID: "-100200"}
	if err = b.Inbound(ctx, "tg", fact); err != nil {
		t.Fatal(err)
	}
	if err = b.Inbound(ctx, "tg", fact); err != nil {
		t.Fatal(err)
	}
	next := target
	next.ChatID = "-100200"
	got, _ := tasks.ChannelConversation(ctx, next)
	if got != chat {
		t.Fatal("migration lost conversation")
	}
	grant, err := tasks.CheckTelegramAccess(ctx, next, "telegram:user:7")
	if err != nil || grant.Revision != 2 {
		t.Fatal(grant, err)
	}
	if _, err = tasks.CheckTelegramAccess(ctx, target, "telegram:user:7"); err == nil {
		t.Fatal("old chat still admitted")
	}
	var raw string
	if err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"tg/"+op.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var moved channelwire.Operation
	if json.Unmarshal([]byte(raw), &moved) != nil || moved.Target.ChatID != next.ChatID {
		t.Fatal("safe pending output not migrated", raw)
	}
	member := groupEvent(next, 21, "")
	member.Lifecycle = &channelwire.Lifecycle{Kind: "membership", Status: "left", CanSend: false}
	if err = b.Inbound(ctx, "tg", member); err != nil {
		t.Fatal(err)
	}
	ready, err := b.Outbox(ctx, "tg")
	if err != nil || len(ready) != 0 {
		t.Fatal("sent after removal", ready, err)
	}
	member.ID = "22"
	member.Lifecycle = &channelwire.Lifecycle{Kind: "membership", Status: "member", CanSend: true}
	if err = b.Inbound(ctx, "tg", member); err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.CheckTelegramAccess(ctx, next, "telegram:user:7"); err != nil {
		t.Fatal("ordinary membership cannot recover", err)
	}
}
