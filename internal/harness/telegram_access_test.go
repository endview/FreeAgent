package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/endview/freeagent/internal/channels"
	"strings"
	"sync"
	"testing"
	"time"
)

func grantTelegramFixture(t *testing.T, tasks *TaskService, change TelegramAccessChange) TelegramAccess {
	t.Helper()
	tasks.core.Store.telegramIdentity(change.Target.ApplicationID)
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	op, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlTelegramAccess, Arguments: mustJSON(change), Authorization: ControlUserDirect})
	if err != nil {
		t.Fatal(err)
	}
	cap, err := control.IssueInteractiveCapability(t.Context(), "", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(t.Context(), op.ID, "", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	result, err := control.ConfirmControlChange(t.Context(), op.ID, ControlAuthorization{Source: ControlUserDirect, Principal: "fixture", Code: confirmation.Code, Capability: cap})
	if err != nil {
		t.Fatal(err)
	}
	var grant TelegramAccess
	if json.Unmarshal(result.Data, &grant) != nil {
		t.Fatal("missing access result")
	}
	return grant
}

func admitTelegramFixture(t *testing.T, r *TelegramRuntime, chat, user int64, kind string) {
	t.Helper()
	update := telegramMessageUpdate(1, 10, chat, user, "/start", kind)
	if kind != "private" {
		update.Message.Text = "/start@fixture_bot"
		update.Message.Entities = []channels.TelegramEntity{{Type: "bot_command", Offset: 0, Length: 18}}
	}
	if err := r.HandleUpdate(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	requests := r.tasks.TelegramAccessRequests()
	if len(requests) != 1 {
		t.Fatalf("requests=%+v", requests)
	}
	grantTelegramFixture(t, r.tasks, TelegramAccessChange{RequestID: requests[0].ID, Target: requests[0].Target, Kind: requests[0].Kind, Enabled: true})
}

func TestTelegramAccessIdentityScopeExpiryAndRevocation(t *testing.T) {
	r, tasks, store, _, sent := telegramRuntimeFixture(t, EchoModel{})
	ctx := t.Context()
	if err := r.HandleUpdate(ctx, telegramMessageUpdate(1, 1, 90, 7, "unadmitted text", "private")); err != nil {
		t.Fatal(err)
	}
	spaces, _ := tasks.Spaces(ctx)
	if len(spaces) != 1 {
		t.Fatal("stranger created space")
	}
	admitTelegramFixture(t, r, 90, 7, "private")
	target := ChannelTarget{SpaceSourceTelegram, "1", "90"}
	if _, err := tasks.CheckTelegramAccess(ctx, target, "telegram:user:8"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("same name/other person admitted", err)
	}
	if err := r.HandleUpdate(ctx, telegramMessageUpdate(4, 4, 90, 7, "hello", "private")); err != nil {
		t.Fatal(err)
	}
	space, _ := tasks.ChannelSpace(ctx, target)
	run, _ := tasks.Latest(ctx, space.DefaultConversationID)
	before := len(*sent)
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Members: []string{"telegram:user:7"}, Enabled: false})
	if err := r.Deliver(ctx, RunResult{Run: run, Answer: "do not send"}); err == nil || len(*sent) != before {
		t.Fatal("revoked output sent", err)
	}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Members: []string{"telegram:user:7"}, Enabled: true})
	if err := tasks.checkTelegramRun(ctx, run); err == nil {
		t.Fatal("old Run revived after regrant")
	}
	store.telegramIdentity("2")
	if err := tasks.checkTelegramRun(ctx, run); err == nil {
		t.Fatal("other Bot inherited Run")
	}
}

func TestTelegramRequestsBoundedDeduplicatedAndNotBearer(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(service)
	service.Store.telegramIdentity("1")
	now := time.Now().UTC()
	e := channelEvent{Target: ChannelTarget{SpaceSourceTelegram, "1", "90"}, Person: "telegram:user:7", Kind: "private", Name: "same"}
	r, notify, err := tasks.requestTelegramAccess(e, now)
	if err != nil || !notify || len(r.Code) != 8 {
		t.Fatal(r, notify, err)
	}
	duplicate, notify, err := tasks.requestTelegramAccess(e, now.Add(time.Second))
	if err != nil || notify || duplicate.ID != r.ID || !duplicate.ExpiresAt.Equal(r.ExpiresAt) {
		t.Fatal("duplicate extended request", duplicate, err)
	}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			event := e
			event.Person = fmt.Sprintf("telegram:user:%d", i+100)
			_, _, _ = tasks.requestTelegramAccess(event, now)
		}(i)
	}
	wg.Wait()
	if len(tasks.TelegramAccessRequests()) != telegramRequestCapacity {
		t.Fatal("unbounded request set")
	}
	if _, err := tasks.CheckTelegramAccess(context.Background(), e.Target, e.Person); err == nil {
		t.Fatal("application granted access")
	}
	service.Store.telegramMu.Lock()
	service.Store.expireTelegramRequests(now.Add(time.Hour))
	service.Store.telegramMu.Unlock()
	if _, err := tasks.TelegramRequest(r.Code); err == nil {
		t.Fatal("expired code remained")
	}
	if _, _, err := tasks.requestTelegramAccess(e, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	service.Store.telegramIdentity("2")
	if len(tasks.TelegramAccessRequests()) != 0 {
		t.Fatal("pending survived bot change")
	}
}

func TestTelegramGroupAdmissionPrecedesHistoryMediaAndDoesNotGrantTools(t *testing.T) {
	r, tasks, store, _, _ := telegramRuntimeFixture(t, EchoModel{})
	admitTelegramFixture(t, r, -100, 7, "supergroup")
	target := ChannelTarget{SpaceSourceTelegram, "1", "-100"}
	space, _ := tasks.ChannelSpace(t.Context(), target)
	denied := telegramMessageUpdate(2, 11, -100, 8, "UNADMITTED_BODY", "supergroup")
	if err := r.HandleUpdate(t.Context(), denied); err != nil {
		t.Fatal(err)
	}
	messages, _ := tasks.Messages(t.Context(), space.DefaultConversationID)
	for _, m := range messages {
		if strings.Contains(m.Content, "UNADMITTED_BODY") {
			t.Fatal("unadmitted group body entered history")
		}
	}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "group", Enabled: true, WholeGroup: true, Members: []string{"telegram:user:7"}})
	if _, err := tasks.CheckTelegramAccess(t.Context(), target, "telegram:user:8"); err != nil {
		t.Fatal("whole-group member denied", err)
	}
	if _, err := tasks.CheckTelegramAccess(t.Context(), target, ""); err == nil {
		t.Fatal("anonymous sender admitted")
	}
	if err := tasks.SetSpacePermission(t.Context(), space.ID, PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	wake := telegramMessageUpdate(3, 12, -100, 8, "@fixture_bot say hello", "supergroup")
	wake.Message.Entities = []channels.TelegramEntity{{Type: "mention", Offset: 0, Length: 12}}
	if err := r.HandleUpdate(t.Context(), wake); err != nil {
		t.Fatal(err)
	}
	run, _ := store.GetLatestRun(t.Context(), space.DefaultConversationID)
	if run.Permission == nil || run.Permission.Mode != PermissionNone {
		t.Fatal("whole-group admission granted tools")
	}
}

func TestTelegramRevocationCancelsOriginalTaskServiceAndBlocksOutput(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	r, tasks, _, _, _ := telegramRuntimeFixture(t, modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return ModelResponse{}, ctx.Err()
	}))
	admitTelegramFixture(t, r, 90, 7, "private")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.HandleUpdate(t.Context(), telegramMessageUpdate(2, 11, 90, 7, "wait until cancelled", "private"))
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("model never started")
	}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: ChannelTarget{SpaceSourceTelegram, "1", "90"}, Kind: "private", Enabled: false, Members: []string{"telegram:user:7"}})
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("control helper did not cancel the actual Owner request")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("revoked work did not settle")
	}
}

func TestTelegramNoneManualCompactionUsesOnlyPublicHistoryAndIsNoopWhenRepeated(t *testing.T) {
	var summaries int
	r, tasks, store, _, _ := telegramRuntimeFixture(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		if request.Purpose == "compaction" {
			summaries++
			for _, m := range request.Messages {
				if strings.Contains(m.Content, "PRIVATE_ADMIN_") {
					t.Error("private administrator text reached channel summary")
				}
			}
			return ModelResponse{Content: "Public goal and confirmed chat facts retained. No tools were granted."}, nil
		}
		return ModelResponse{Content: strings.Repeat("public chat fact; ", 40)}, nil
	}))
	admitTelegramFixture(t, r, 90, 7, "private")
	space, _ := tasks.ChannelSpace(t.Context(), ChannelTarget{SpaceSourceTelegram, "1", "90"})
	snapshot, _ := tasks.core.snapshot()
	admin, _, err := store.CreateRun(t.Context(), space.DefaultConversationID, "admin-private", "PRIVATE_ADMIN_PROMPT", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.startModel(t.Context(), admin, `{}`, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.saveModel(t.Context(), admin, attempt, ModelResponse{Content: "PRIVATE_ADMIN_RESPONSE"}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := r.HandleUpdate(t.Context(), telegramMessageUpdate(int64(i+2), int64(i+11), 90, 7, strings.Repeat("public constraint; ", 40), "private")); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 2 {
		if err := r.HandleUpdate(t.Context(), telegramMessageUpdate(int64(20+i), int64(30+i), 90, 7, "/compact", "private")); err != nil {
			t.Fatal(err)
		}
	}
	if summaries != 1 {
		t.Fatalf("duplicate manual compaction calls=%d", summaries)
	}
	state, err := tasks.ContextState(t.Context(), space.DefaultConversationID)
	if err != nil || state.Summary == nil || state.Summary.Scope != "channel_public" {
		t.Fatal(state.Status, err)
	}
}

func TestTelegramCompactionResultRetainsTheActualGroupRequester(t *testing.T) {
	service, _, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.Purpose == "compaction" {
			return ModelResponse{Content: "Shared public goal and pending work retained."}, nil
		}
		return ModelResponse{Content: "public reply"}, nil
	}))
	tasks := NewTaskService(service)
	target := ChannelTarget{SpaceSourceTelegram, "1", "-100"}
	grant := grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "group", Enabled: true, Members: []string{"telegram:user:7", "telegram:user:8"}})
	for i := range 3 {
		input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: fmt.Sprint(i + 1), Purpose: "request"}, strings.Repeat("public group fact; ", 50))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tasks.SubmitChannel(t.Context(), input); err != nil {
			t.Fatal(err)
		}
	}
	space, _ := tasks.Space(t.Context(), grant.SpaceID)
	previous, _ := tasks.Latest(t.Context(), space.DefaultConversationID)
	result, err := tasks.CompactChannel(t.Context(), space.DefaultConversationID, "telegram:user:8", target)
	if err != nil || !result.Maintenance || result.Run.ID == previous.ID || result.Run.Permission == nil || result.Run.Permission.PersonID != "telegram:user:8" {
		t.Fatalf("wrong maintenance requester: maintenance=%t run=%s err=%v", result.Maintenance, result.Run.ID, err)
	}
}

func TestTelegramInvisibleCommandsDoNotConsumeEffectiveContextOrTriggerSummary(t *testing.T) {
	var summaries int
	service, _, _ := testHarness(t, modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		if r.Purpose == "compaction" {
			summaries++
		}
		return ModelResponse{Content: "public reply"}, nil
	}))
	service.Config.MaxMessages = 4
	tasks := NewTaskService(service)
	target := ChannelTarget{SpaceSourceTelegram, "1", "90"}
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
	for i := range 50 {
		_, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: fmt.Sprint(i + 1), Purpose: "command"}, "/status")
		if err != nil {
			t.Fatal(err)
		}
	}
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "51", Purpose: "request"}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil || result.Run.Status != "completed" || summaries != 0 {
		t.Fatalf("invisible commands consumed input: status=%s summaries=%d err=%v", result.Run.Status, summaries, err)
	}
}
