package harness

import (
	"context"
	"fmt"
	"github.com/endview/freeagent/internal/channelwire"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduledMasterRevocationCancelsGenerationAndQueuedSend(t *testing.T) {
	for _, stage := range []string{"model", "send"} {
		t.Run(stage, func(t *testing.T) {
			b, tasks, _, rule := affectWakeHarness(t)
			started := make(chan struct{})
			var calls atomic.Int32
			tasks.core.Model = modelFunc(func(ctx context.Context, _ ModelRequest, _ func(string)) (ModelResponse, error) {
				calls.Add(1)
				close(started)
				if stage == "model" {
					<-ctx.Done()
					return ModelResponse{}, ctx.Err()
				}
				return ModelResponse{Content: `{"decision":"speak","text":"A granted opener.","reason_code":"relevant","reason":"Specific confirmed interest","source_refs":["persona:interests"]}`}, nil
			})
			o, created, err := tasks.ScheduleWake(t.Context(), rule, "revoke", rule.SourceRefs, time.Now(), false)
			if err != nil || !created {
				t.Fatal(created, err)
			}
			done := make(chan error, 1)
			go func() { _, err := tasks.EvaluateWake(t.Context(), o.ID); done <- err }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("no dispatch")
			}
			if stage == "send" {
				select {
				case err = <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("generation did not settle")
				}
			}
			policy := tasks.Config().Delivery
			policy.GenerationEnabled = map[string]bool{affectExtensionID: false}
			control, _ := tasks.Control()
			at := time.Now()
			if _, err = control.ApplyDirectControlChange(t.Context(), ControlSetDeliveryPolicy, policy, "", "test-owner"); err != nil {
				t.Fatal(err)
			}
			if stage == "model" {
				select {
				case err = <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("master switch failed to cancel in-flight evaluation")
				}
			}
			t.Logf("revocation stage=%s cancellation_ms=%d", stage, time.Since(at).Milliseconds())
			if ops, err := b.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 {
				t.Fatal("revoked opener dispatched", ops, err)
			}
			records, err := tasks.WakeRecords(t.Context(), rule.ConversationID)
			if err != nil || len(records) != 1 || records[0].State != "cancelled" || calls.Load() != 1 {
				t.Fatal(records, calls.Load(), err)
			}
			if _, err = tasks.EvaluateWake(t.Context(), o.ID); err != nil || calls.Load() != 1 {
				t.Fatal("revoked generation replayed", err)
			}
		})
	}
}

func TestScheduledPreviewNeverSendsAndQuotaSurvivesRuleChange(t *testing.T) {
	b, tasks, _, rule := affectWakeHarness(t)
	calls := 0
	tasks.core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		calls++
		// Deliberately no usage: the reservation and rolling quota must remain.
		return ModelResponse{Content: `{"decision":"speak","text":"Preview only.","reason_code":"relevant","reason":"Confirmed interest","source_refs":["persona:interests"]}`}, nil
	})
	for n := 0; n < 3; n++ {
		if n == 2 {
			cfg := tasks.Config()
			rule.ID = "renamed-rule"
			cfg.Delivery.Rules = []FollowupRule{rule}
			tasks.core.setConfig(cfg)
		}
		o, err := tasks.PreviewScheduledGeneration(t.Context(), rule.ID, fmt.Sprint("preview-", n))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tasks.EvaluateWake(t.Context(), o.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = tasks.EvaluateWake(t.Context(), o.ID); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("preview replay or quota reset", calls)
	}
	if ops, err := b.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 {
		t.Fatal("preview sent", ops, err)
	}
	records, err := tasks.WakeRecords(t.Context(), rule.ConversationID)
	if err != nil || len(records) != 3 {
		t.Fatal(records, err)
	}
	for _, o := range records {
		if o.State != "previewed" && o.State != "skipped" {
			t.Fatal(o)
		}
	}
}

func TestScheduledQuietHoursPreventModelAdmission(t *testing.T) {
	_, tasks, _, rule := affectWakeHarness(t)
	now := time.Now().UTC()
	rule.Timezone = "UTC"
	rule.QuietStart = now.Add(-time.Minute).Format("15:04")
	rule.QuietEnd = now.Add(time.Minute).Format("15:04")
	cfg := tasks.Config()
	cfg.Delivery.Rules = []FollowupRule{rule}
	tasks.core.setConfig(cfg)
	if _, _, err := tasks.ScheduleWake(t.Context(), rule, "quiet", rule.SourceRefs, now, false); err == nil {
		t.Fatal("quiet opportunity admitted")
	}
}

func TestScheduledOutboxAssociationAndReceiptCannotBeOverwritten(t *testing.T) {
	b, tasks, _, rule := affectWakeHarness(t)
	tasks.core.Model = modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		return ModelResponse{Content: `{"decision":"speak","text":"An authorized opener.","reason_code":"relevant","reason":"Confirmed interest","source_refs":["persona:interests"]}`}, nil
	})
	o, _, err := tasks.ScheduleWake(t.Context(), rule, "receipt-race", rule.SourceRefs, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tasks.EvaluateWake(t.Context(), o.ID); err != nil {
		t.Fatal(err)
	}
	records, err := tasks.WakeRecords(t.Context(), rule.ConversationID)
	if err != nil || len(records) != 1 || records[0].State != "delivery_queued" || records[0].DeliveryID == "" {
		t.Fatal("outbox association was not committed", records, err)
	}
	stale := records[0]
	ops, err := b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || ops[0].ID != stale.DeliveryID {
		t.Fatal(ops, err)
	}
	if err = b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "sent", MessageID: "101"}); err != nil {
		t.Fatal(err)
	}
	// Simulate a late Run cleanup/queue result and verify that it cannot erase
	// a fast transport's already committed receipt or reopen pending capacity.
	stale.State = "generated"
	if err = tasks.persistWake(t.Context(), stale); err == nil {
		t.Fatal("stale generation overwrote terminal receipt")
	}
	records, err = tasks.WakeRecords(t.Context(), rule.ConversationID)
	if err != nil || records[0].State != "delivery_sent" || records[0].DeliveryMessage != "101" {
		t.Fatal(records, err)
	}
	if _, err = tasks.EvaluateWake(t.Context(), o.ID); err != nil {
		t.Fatal(err)
	}
	if ops, err = b.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 {
		t.Fatal("sent opener replayed", ops, err)
	}
}

func TestScheduledUninstallDropsGenerationGrant(t *testing.T) {
	_, tasks, _, rule := affectWakeHarness(t)
	o, _, err := tasks.ScheduleWake(t.Context(), rule, "before-uninstall", rule.SourceRefs, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err = tasks.DeleteExtension(t.Context(), affectExtensionID); err != nil {
		t.Fatal(err)
	}
	if tasks.Config().Delivery.GenerationEnabled[affectExtensionID] {
		t.Fatal("uninstall retained automatic generation grant")
	}
	records, err := tasks.WakeRecords(t.Context(), rule.ConversationID)
	if err != nil || len(records) != 1 || records[0].ID != o.ID || records[0].State != "cancelled" {
		t.Fatal(records, err)
	}
}
