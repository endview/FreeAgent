package harness

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

type telegramAdminFixture struct {
	tasks      *TaskService
	control    *ControlService
	target     ChannelTarget
	chat       string
	identity   AdministratorEpoch
	identityMu sync.Mutex
	calls      int
	kind       string
	args       map[string]any
}

func newTelegramAdminFixture(t *testing.T) *telegramAdminFixture {
	t.Helper()
	f := &telegramAdminFixture{target: ChannelTarget{Source: SpaceSourceTelegram, ApplicationID: "1", ChatID: "7"},
		identity: AdministratorEpoch{AccountID: "fixture-admin", Epoch: "fixture-epoch"},
		kind:     ControlSetRuntimeLimits, args: map[string]any{"auto_compact_percent": 75}}
	core, _, _ := testHarness(t, modelFunc(func(_ context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
		f.calls++
		if f.kind == "" {
			return ModelResponse{Content: "done"}, nil
		}
		return ModelResponse{ToolCalls: []ToolCall{makeCall("setting", controlPlanTool, map[string]any{"kind": f.kind, "arguments": f.args})}}, nil
	}))
	core.controlToolsActive = true
	core.Config.PublicWeb = DefaultPublicWebConfig()
	core.Config.PublicWeb.Enabled, core.Config.PublicWeb.Origin = true, "https://fixture.example.test"
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	f.tasks = NewTaskService(core)
	f.tasks.SetAdministratorGuard(func() (AdministratorEpoch, func(), error) {
		f.identityMu.Lock()
		return f.identity, f.identityMu.Unlock, nil
	})
	grantTelegramFixture(t, f.tasks, TelegramAccessChange{Target: f.target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
	verified, err := f.tasks.ReceiveChannelText(t.Context(), f.source("verify"), "verified private message")
	if err != nil {
		t.Fatal(err)
	}
	f.chat = verified.ConversationID
	f.control, err = f.tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *telegramAdminFixture) source(id string) ChannelMessageSource {
	return ChannelMessageSource{ChannelTarget: f.target, PersonID: "telegram:user:7", MessageID: id, Purpose: "request"}
}

func (f *telegramAdminFixture) submit(t *testing.T, id string) RunResult {
	t.Helper()
	in, err := f.tasks.ReceiveChannelText(t.Context(), f.source(id), "change settings")
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.tasks.SubmitChannel(t.Context(), in)
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("private request did not end: %s, %v", result.Run.Status, err)
	}
	return result
}

func (f *telegramAdminFixture) bind(t *testing.T, revision int64) {
	t.Helper()
	if err := f.tasks.ApplyTelegramAdminBinding(t.Context(), "1", "bind", "7", revision); err != nil {
		t.Fatal(err)
	}
}

func (f *telegramAdminFixture) operation(t *testing.T) ControlOperation {
	t.Helper()
	ops, err := f.control.ListControlOperations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := len(ops) - 1; i >= 0; i-- {
		if ops[i].RequestEntry == telegramAdminEntry {
			return ops[i]
		}
	}
	t.Fatal("no administrator proposal")
	return ControlOperation{}
}

func TestTelegramAdminPrivateCard(t *testing.T) {
	f := newTelegramAdminFixture(t)
	if _, err := f.tasks.ApplyTelegramConfirmers(t.Context(), "1", []string{"7"}, 0, "allow-ordinary"); err != nil {
		t.Fatal(err)
	}
	if result := f.submit(t, "unbound"); result.Run.StopReason == detachedProposalReason {
		t.Fatal("confirmation roster granted administrator delegation")
	}
	if _, err := f.tasks.ApplyTelegramConfirmers(t.Context(), "1", nil, 1, "remove-ordinary"); err != nil {
		t.Fatal(err)
	}
	f.bind(t, 0)
	count := f.calls
	result := f.submit(t, "bound")
	op := f.operation(t)
	if result.Run.StopReason != detachedProposalReason || !op.Detached || op.Status != ControlPending || op.ConversationID != f.chat || f.calls != count+1 {
		t.Fatal("incorrect handoff lifecycle", op.Status, f.calls)
	}
	var text string
	session := newChannelSession(f.tasks, SpaceSourceTelegram, func(_ context.Context, _ ChannelTarget, _ string, body string, _ []channelButton) (string, error) {
		text = body
		return "card", nil
	})
	session.reset("1")
	if err := session.Deliver(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	var token string
	for k, item := range session.callbacks {
		if item.Approved {
			token = k
		}
	}
	if token == "" || !strings.Contains(text, "75%") || !strings.Contains(text, "全局") {
		t.Fatal("private direct card missing", text)
	}
	event := channelEvent{Target: f.target, Person: "telegram:user:8", Callback: token, CallbackMessage: "card"}
	if err := session.callback(t.Context(), event); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("wrong person accepted", err)
	}
	event.Person = "telegram:user:7"
	for _, target := range []ChannelTarget{
		{Source: SpaceSourceTelegram, ApplicationID: "2", ChatID: "7"},
		{Source: SpaceSourceTelegram, ApplicationID: "1", ChatID: "-100", ThreadID: "3"},
	} {
		wrong := event
		wrong.Target = target
		if err := session.callback(t.Context(), wrong); !errors.Is(err, ErrPermissionDenied) {
			t.Fatal("wrong destination accepted", err)
		}
	}
	if err := session.callback(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	applied, err := f.control.ReadControlOperation(t.Context(), op.ID)
	if err != nil || applied.Status != ControlApplied || applied.DecisionEntry != telegramAdminEntry ||
		autoCompactPercent(f.tasks.Config()) != 75 || f.calls != count+1 {
		t.Fatal("not committed once by the host", applied.Status, f.calls, err)
	}
	var feedback string
	if err := session.callback(WithChannelFeedback(t.Context(), &feedback), event); err != nil || !strings.Contains(feedback, "不会重复") {
		t.Fatal("repeated callback did not report the original decision", err, feedback)
	}
	again, err := f.control.ReadControlOperation(t.Context(), op.ID)
	if err != nil || applied.NewRevisions["config.limits"] != again.NewRevisions["config.limits"] || f.calls != count+1 {
		t.Fatal("repeated decision changed revision or called the model", err)
	}
}

func TestTelegramAdminBridgeReceiptAndConcurrentDecision(t *testing.T) {
	f := newTelegramAdminFixture(t)
	ctx := t.Context()
	key, err := f.tasks.ChannelKeys().Add(KeyInput{Name: "Bot", Protocol: KeyProtocolTelegram, BaseURL: "https://api.telegram.org", Source: KeySourceSaved, Secret: []byte("1:fixture-only-token")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.tasks.ConfigureRuntimePolicy(ctx, "", func(c *Config) error {
		c.Channels.Connections = []channelwire.Connection{{ID: "tg", Source: SpaceSourceTelegram, ApplicationID: "1", Credential: key.ID, Enabled: true}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := newTestChannelBridge(f.tasks, "http://127.0.0.1:1", "fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	f.bind(t, 0)
	result := f.submit(t, "bridge")
	op := f.operation(t)
	if err := b.Deliver(ctx, result); err != nil {
		t.Fatal(err)
	}
	outbox, err := b.Outbox(ctx, "tg")
	if err != nil || len(outbox) != 1 || len(outbox[0].Buttons) != 2 {
		t.Fatalf("expected one decision card: %d, %v", len(outbox), err)
	}
	delivery := outbox[0]
	var token string
	for _, button := range delivery.Buttons {
		if button.Text == "保存全局设置" {
			token = button.Token
		}
	}
	if token == "" {
		t.Fatal("approve button missing")
	}
	s := b.connections["tg"].session
	event := channelEvent{Target: f.target, Callback: token, CallbackMessage: "91", Person: "telegram:user:7"}
	if err := s.callback(ctx, event); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("card without a delivery receipt was usable", err)
	}
	if err := b.Receipt(ctx, "tg", channelwire.Receipt{ID: delivery.ID, Dispatch: delivery.Dispatch, State: "sent", MessageID: "91"}); err != nil {
		t.Fatal(err)
	}
	web := WithPublicAdministrator(ctx, "public:fixture", "192.0.2.7", f.identity.AccountID, func(bool) (func(), error) { return func() {}, nil })
	card, err := f.control.RenderConfirmation(web, op.ID, f.chat, "public:fixture")
	if err != nil {
		t.Fatal(err)
	}
	cap, err := f.control.IssueInteractiveCapability(web, f.chat, "public:fixture")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := f.control.ConfirmControlChange(web, op.ID, controlAuth(cap, f.chat, "public:fixture", card.Code))
		results <- err
	}()
	go func() {
		<-start
		event := channelwire.Event{ID: "fixture-callback", Target: channelwire.Target(f.target), Person: "telegram:user:7",
			Kind: "private", Callback: token, CallbackMessage: "91"}
		results <- b.Inbound(ctx, "tg", event)
	}()
	close(start)
	for range 2 {
		select {
		case err := <-results:
			if err != nil && !errors.Is(err, ErrControlAlreadyUsed) {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent decisions did not complete")
		}
	}
	done, err := f.control.ReadControlOperation(ctx, op.ID)
	if err != nil || done.Status != ControlApplied || done.NewRevisions["config.limits"] != op.ExpectedRevisions["config.limits"]+1 || f.calls != 1 {
		t.Fatal("decisions did not converge to one write without rerunning the model", done.Status, f.calls, err)
	}
}

func TestTelegramAdminPauseRevokeAndExpiry(t *testing.T) {
	f := newTelegramAdminFixture(t)
	ctx := t.Context()
	if err := f.tasks.ApplyTelegramAdminBinding(ctx, "1", "bind", "8", 0); !errors.Is(err, ErrPermissionDenied) {
		t.Fatal("unverified or inadmitted user could be bound", err)
	}
	f.bind(t, 0)
	f.submit(t, "before-pause")
	old := f.operation(t)
	if err := f.tasks.ApplyTelegramAdminBinding(ctx, "1", "pause", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.ApplyTelegramAdminBinding(ctx, "1", "revoke", "", 1); !errors.Is(err, ErrControlStaleRevision) {
		t.Fatal("stale binding revision could overwrite a pause", err)
	}
	f.bind(t, 2)
	if _, err := f.control.RenderConfirmation(ctx, old.ID, f.chat, "cli"); !errors.Is(err, ErrTelegramAdminChanged) {
		t.Fatal("rebinding revived a paused proposal", err)
	}
	f.submit(t, "expire")
	expires := f.operation(t)
	f.control.mu.Lock()
	record := f.control.operations[expires.ID]
	record.Operation.ExpiresAt = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	f.control.operations[expires.ID] = record
	f.control.mu.Unlock()
	if _, err := f.control.RenderConfirmation(ctx, expires.ID, f.chat, "cli"); !errors.Is(err, ErrControlExpired) {
		t.Fatal("expired detached proposal was usable", err)
	}
	if err := f.tasks.ApplyTelegramAdminBinding(ctx, "1", "revoke", "", 3); err != nil {
		t.Fatal(err)
	}
	view, err := f.tasks.TelegramAdminBinding(ctx, "1")
	if err != nil || view.State != "unbound" || view.User != "" || view.Revision != 4 {
		t.Fatal("revocation lost binding revision or retained the user", view, err)
	}
}

func TestTelegramAdminWebHandoffAndRotation(t *testing.T) {
	f := newTelegramAdminFixture(t)
	f.bind(t, 0)
	f.args["max_output_tokens"] = 3000
	result := f.submit(t, "handoff")
	op := f.operation(t)
	if !strings.Contains(result.Run.Output, "operation="+op.ID) || strings.Contains(result.Run.Output, "3000") {
		t.Fatal("handoff leaked a diff or did not link the original", result.Run.Output)
	}
	preview, ok, err := f.tasks.detachedSettingConfirmation(t.Context(), result.Run)
	if err != nil || !ok {
		t.Fatal(err)
	}
	session := newChannelSession(f.tasks, SpaceSourceTelegram, nil)
	if text, buttons, err := session.settingCard(t.Context(), result.Run, preview); err != nil || len(buttons) != 0 || text != result.Run.Output {
		t.Fatal("a multi-field change had a Telegram decision", text, err)
	}
	f.kind = ""
	f.submit(t, "nonblocked")
	ctx := WithPublicAdministrator(t.Context(), "public:fixture", "192.0.2.7", f.identity.AccountID, func(bool) (func(), error) {
		return func() {}, nil
	})
	capability, err := f.control.IssueInteractiveCapability(ctx, f.chat, "public:fixture")
	if err != nil {
		t.Fatal(err)
	}
	card, err := f.control.RenderConfirmation(ctx, op.ID, f.chat, "public:fixture")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.control.ConfirmControlChange(ctx, op.ID, controlAuth(capability, f.chat, "public:fixture", card.Code))
	if err != nil || f.tasks.Config().MaxOutputTokens != 3000 || f.calls != 2 {
		t.Fatal("Web did not apply the original operation or reran the model", f.calls, err)
	}
	f.kind = ControlSetRuntimeLimits
	f.args = map[string]any{"auto_compact_percent": 82}
	f.submit(t, "before-rotation")
	old := f.operation(t)
	f.identityMu.Lock()
	f.identity.Epoch = "new-password-epoch"
	f.identityMu.Unlock()
	view, err := f.tasks.TelegramAdminBinding(t.Context(), "1")
	if err != nil || view.State != "paused" {
		t.Fatal(view, err)
	}
	f.bind(t, view.Revision)
	if _, err := f.control.RenderConfirmation(ctx, old.ID, f.chat, "public:fixture"); !errors.Is(err, ErrTelegramAdminChanged) {
		t.Fatal("rebinding revived an old proposal", err)
	}
}

func TestTelegramAdminStalePreviewCreatesNewOperation(t *testing.T) {
	f := newTelegramAdminFixture(t)
	f.bind(t, 0)
	f.args["max_output_tokens"] = 4000
	f.submit(t, "stale")
	old := f.operation(t)
	if _, err := f.control.ApplyDirectControlChange(t.Context(), ControlSetRuntimeLimits, map[string]int{"max_output_tokens": 3001}, "", "cli"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.control.RenderConfirmation(t.Context(), old.ID, f.chat, "cli"); !errors.Is(err, ErrControlStaleRevision) {
		t.Fatal(err)
	}
	next, err := f.control.RepreviewTelegramHandoff(t.Context(), old.ID)
	if err != nil || next.ID == old.ID || !strings.Contains(next.Preview, old.ID) || next.Status != ControlPending || f.tasks.Config().MaxOutputTokens != 3001 {
		t.Fatal("stale preview applied or intent was discarded", err)
	}
}

func TestTelegramAdminDefaultModelPinsProvider(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	keys := NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	core.Keys = keys
	profile, err := keys.Add(KeyInput{Name: "fixture", BaseURL: "https://provider.example.test/v1", Protocol: KeyProtocolOpenAICompatible, Source: KeySourceSaved, Secret: []byte("fixture-secret-only")})
	if err != nil {
		t.Fatal(err)
	}
	c := core.currentConfig()
	c.Models = map[string]ModelConfig{"before": {Provider: KeyProtocolOpenAICompatible, Key: profile.ID, Model: "one"}, "after": {Provider: KeyProtocolOpenAICompatible, Key: profile.ID, Model: "two"}}
	c.DefaultModel, c.DefaultSelection = "before", "before"
	enableTestProviders(&c)
	core.setConfig(c)
	control, err := NewTaskService(core).Control()
	if err != nil {
		t.Fatal(err)
	}
	next := cloneConfig(c)
	_ = SetDefaultModel(&next, "after")
	rec := controlRecord{Operation: ControlOperation{Kind: ControlSetDefaultModel}, Arguments: mustJSON(map[string]string{"model_id": "after"}),
		Before: map[string]json.RawMessage{"config.models": mustJSON(configResourceValue(c, "config.models"))},
		After:  map[string]json.RawMessage{"config.models": mustJSON(configResourceValue(next, "config.models"))}}
	ok, _, fingerprint := control.telegramAdminDirectDiff(rec)
	if !ok || fingerprint == "" {
		t.Fatal("same saved Provider not recognized")
	}
	c.DefaultSelection = "auto"
	rec.Before["config.models"] = mustJSON(configResourceValue(c, "config.models"))
	if ok, _, _ := control.telegramAdminDirectDiff(rec); ok {
		t.Fatal("auto route incorrectly treated as the same destination")
	}
}
