package harness

import (
	"context"
	"github.com/endview/freeagent/internal/channelwire"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func affectWakeHarness(t *testing.T) (*ChannelBridge, *TaskService, ChannelTarget, FollowupRule) {
	t.Helper()
	core, tasks, _ := affectHost(t)
	key, err := tasks.ChannelKeys().Add(KeyInput{Name: "Test bot", Protocol: "telegram", BaseURL: "https://api.telegram.org", Source: KeySourceSaved, Secret: []byte("1:FIXTURE_TOKEN")})
	if err != nil {
		t.Fatal(err)
	}
	cfg := tasks.Config()
	cfg.Channels = ChannelsConfig{Connections: []channelwire.Connection{{ID: "tg", Source: "telegram", ApplicationID: "1", Credential: key.ID, Enabled: true, ReplyStickers: true, AutonomousReactions: true}}}
	core.setConfig(cfg)
	if err = WriteConfig(core.Store.DataDir(), cfg); err != nil {
		t.Fatal(err)
	}
	controlBaseline, _ := tasks.Control()
	if err = controlBaseline.ReviewExternalChange(t.Context()); err != nil {
		t.Fatal(err)
	}
	b, err := newTestChannelBridge(tasks, "http://127.0.0.1:1", "wake-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	target := ChannelTarget{Source: "telegram", ApplicationID: "1", ChatID: "90"}
	access := grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
	control, _ := tasks.Control()
	_, err = control.ApplyDirectControlChange(t.Context(), ControlChangeExtensionState, map[string]any{"id": affectExtensionID, "scope": map[string]string{"space_id": "local", "resource": "maya", "actor_id": "owner"}, "action": "edit_role", "expected_revision": 1, "payload": map[string]any{"persona": map[string]any{"id": "maya", "name": "Maya", "identity_summary": "An AI engineering partner", "preferences": []string{"Concrete engineering discussions"}, "version": 2, "frozen": true}, "spaces": []string{"local", access.SpaceID}}}, "", "test-owner")
	if err != nil {
		t.Fatal(err)
	}
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.SelectCapabilities(t.Context(), input.ConversationID, CapabilitySelection{Extensions: []string{affectExtensionID}, Variants: map[string]string{affectExtensionID: "maya"}}); err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil || result.Run.Status != "completed" {
		t.Fatal(err)
	}
	if _, err = core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "scheduled/input/"+input.ConversationID, string(mustJSON(map[string]any{"message_id": input.MessageID, "at": time.Now().Add(-5 * time.Hour).UTC().Format(time.RFC3339Nano)}))); err != nil {
		t.Fatal(err)
	}

	rule := FollowupRule{ID: "conversation-opener", Extension: affectExtensionID, ConversationID: input.ConversationID, Enabled: true, Timezone: "Asia/Shanghai", DailyMax: 1, CooldownHours: 6, CancelOnInput: true, MaxDelaySeconds: 86400, Generation: true, Resource: "maya", FreeTopics: true, SourceRefs: []string{"persona:interests"}}
	cfg = tasks.Config()
	cfg.Delivery = DeliveryConfig{Enabled: true, GenerationEnabled: map[string]bool{affectExtensionID: true}, Rules: []FollowupRule{rule}}
	core.setConfig(cfg)
	core.extensions.configure(cfg)
	if err = WriteConfig(core.Store.DataDir(), cfg); err != nil {
		t.Fatal(err)
	}
	if err = control.ReviewExternalChange(t.Context()); err != nil {
		t.Fatal(err)
	}
	return b, tasks, target, rule
}
func TestScheduledGenerationRealExtensionSpeakSkipAndUnknown(t *testing.T) {
	for _, mode := range []string{"speak", "skip", "unknown", "forged-source"} {
		t.Run(mode, func(t *testing.T) {
			b, tasks, _, rule := affectWakeHarness(t)
			var calls atomic.Int32
			tasks.core.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
				calls.Add(1)
				if !strings.HasSuffix(r.Purpose, "/wake.evaluate") || r.SupplementalKey == "" || len(r.Tools)+len(r.Memory)+len(r.Extensions) != 0 {
					t.Error("not an isolated scheduled generation")
				}
				if mode == "unknown" {
					return ModelResponse{}, context.DeadlineExceeded
				}
				if mode == "skip" {
					return ModelResponse{Content: `{"decision":"skip","text":"","reason_code":"no_topic","reason":"No useful topic","source_refs":[]}`}, nil
				}
				source := "persona:interests"
				if mode == "forged-source" {
					source = "other-space:secret"
				}
				return ModelResponse{Content: `{"decision":"speak","text":"想继续聊一下上次的工程设计吗？","reason_code":"relevant","reason":"A confirmed interest","source_refs":["` + source + `"]}`}, nil
			})
			now := time.Now()
			o, created, err := tasks.ScheduleWake(t.Context(), rule, "slot-1", rule.SourceRefs, now, false)
			if err != nil || !created {
				t.Fatal(created, err)
			}
			result, err := tasks.EvaluateWake(t.Context(), o.ID)
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("wrong real dispatch count", calls.Load())
			}
			var count int
			if err = tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.messages WHERE run_id=$1 AND body_json->>'Role'='user'`, result.Run.ID).Scan(&count); err != nil || count != 0 {
				t.Fatal("fabricated adopted user input", count, err)
			}
			if _, err = tasks.EvaluateWake(t.Context(), o.ID); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 {
				t.Fatal("opportunity replayed model")
			}
			ops, err := b.Outbox(t.Context(), "tg")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "speak" {
				if len(ops) != 1 || ops[0].WakeID != o.ID {
					t.Fatal("speak not queued", ops)
				}
				if err = b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "unknown"}); err != nil {
					t.Fatal(err)
				}
				if ops, err = b.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 {
					t.Fatal("unknown send replayed", ops, err)
				}
			} else if len(ops) != 0 {
				t.Fatal("invalid/skip result sent", ops)
			}
		})
	}
}
func TestScheduledGenerationDefaultOffAndPreemptsOnInput(t *testing.T) {
	b, tasks, target, rule := affectWakeHarness(t)
	cfg := tasks.Config()
	cfg.Delivery.GenerationEnabled[affectExtensionID] = false
	tasks.core.setConfig(cfg)
	if _, _, err := tasks.ScheduleWake(t.Context(), rule, "off", rule.SourceRefs, time.Now(), false); err == nil {
		t.Fatal("master off permitted opportunity")
	}
	cfg.Delivery.GenerationEnabled[affectExtensionID] = true
	tasks.core.setConfig(cfg)
	started := make(chan struct{})
	var calls atomic.Int32
	tasks.core.Model = modelFunc(func(ctx context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return ModelResponse{}, ctx.Err()
	})
	o, _, err := tasks.ScheduleWake(t.Context(), rule, "on", rule.SourceRefs, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := tasks.EvaluateWake(t.Context(), o.ID); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not dispatch")
	}
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "new", Purpose: "request"}, "new user input wins")
	if err != nil {
		t.Fatal(err)
	}
	tasks.cancelScheduledForInput(t.Context(), input.ConversationID)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("user input did not preempt generation")
	}
	if ops, err := b.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 {
		t.Fatal("stale opener sent", ops, err)
	}
	if calls.Load() != 1 {
		t.Fatal("interrupted generation retried")
	}
}

func TestScheduledOwnerDoesNotStartTimerWhenDisabled(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tasks.SetScheduledGenerationOwner(ctx, nil)
	if core.Store.scheduled.active {
		t.Fatal("plain Core started scheduled timer")
	}
}
