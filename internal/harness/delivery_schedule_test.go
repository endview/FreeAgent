package harness

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

func TestScheduledDeliveryPersistsDueTimeDeduplicatesAndDoesNotReplayUnknown(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "remind later")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	candidate := ReplyCandidate{Key: "reminder", Text: "检查部署版本", After: now.Add(30 * time.Minute).UTC().Format(time.RFC3339Nano), Expires: now.Add(time.Hour).UTC().Format(time.RFC3339Nano), Timezone: "Asia/Shanghai"}
	op, err := b.queueCandidate(t.Context(), result.Run, "", candidate)
	if err != nil || op.State != "scheduled" {
		t.Fatal(op, err)
	}
	again, err := b.queueCandidate(t.Context(), result.Run, "", candidate)
	if err != nil || again.ID != op.ID {
		t.Fatal("candidate not idempotent", again, err)
	}
	changed := candidate
	changed.Text = "different"
	if _, err := b.queueCandidate(t.Context(), result.Run, "", changed); err != ErrRequestKey {
		t.Fatal("changed content reused source key", err)
	}
	if ops, err := b.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 {
		t.Fatal("sent before due", ops, err)
	}
	op.NotBeforeMS = now.Add(-time.Second).UnixMilli()
	if _, err := tasks.core.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(op)), channelOperationPrefix+"tg/"+op.ID); err != nil {
		t.Fatal(err)
	}
	ops, err := b.Outbox(t.Context(), "tg")
	if err != nil || len(ops) != 1 || ops[0].ID != op.ID {
		t.Fatal("due fixed delivery missing", ops, err)
	}
	if err := b.Receipt(t.Context(), "tg", channelwire.Receipt{ID: op.ID, Dispatch: ops[0].Dispatch, State: "unknown"}); err != nil {
		t.Fatal(err)
	}
	if ops, err := b.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 {
		t.Fatal("UNKNOWN reminder resent", ops, err)
	}
}

func TestScheduledDeliveryRejectsChangedConversationDestination(t *testing.T) {
	for _, identity := range []string{"requester", "local-owner"} {
		for _, destination := range []string{"local", "other-telegram", "other-workspace"} {
			t.Run(identity+"/"+destination, func(t *testing.T) {
				b, tasks, target := expressionBridge(t, EchoModel{})
				input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "remind later")
				if err != nil {
					t.Fatal(err)
				}
				var result RunResult
				if identity == "local-owner" {
					result, err = tasks.Submit(t.Context(), input.ConversationID, "owner-reminder", "remind later")
				} else {
					result, err = tasks.SubmitChannel(t.Context(), input)
				}
				if err != nil || result.Run.Status != "completed" {
					t.Fatalf("run status=%s err=%v", result.Run.Status, err)
				}
				now := time.Now()
				candidate := ReplyCandidate{Key: "before-move", Text: "fixed reminder", After: now.Add(time.Minute).UTC().Format(time.RFC3339Nano), Expires: now.Add(time.Hour).UTC().Format(time.RFC3339Nano), Timezone: "UTC"}
				op, err := b.queueCandidate(t.Context(), result.Run, "", candidate)
				if err != nil {
					t.Fatal(err)
				}
				grant, err := tasks.telegramDeliveryGrant(t.Context(), target, op.Person, op.LocalOwner)
				if err != nil {
					t.Fatal(err)
				}
				switch destination {
				case "local":
					err = tasks.MoveConversation(t.Context(), input.ConversationID, LocalSpaceID)
				case "other-telegram":
					other := target
					other.ChatID = "91"
					space := grantTelegramFixture(t, tasks, TelegramAccessChange{Target: other, Kind: "private", Enabled: true, Members: []string{"telegram:user:7"}})
					err = tasks.MoveConversation(t.Context(), input.ConversationID, space.SpaceID)
				case "other-workspace":
					_, err = tasks.core.Store.db.ExecContext(t.Context(), `UPDATE fa.conversations SET workspace=$1 WHERE id=$2`, t.TempDir(), input.ConversationID)
				}
				if err != nil {
					t.Fatal(err)
				}
				// The old recipient is still authorized: only the conversation's
				// current destination/scope has changed.
				current, err := tasks.telegramDeliveryGrant(t.Context(), target, op.Person, op.LocalOwner)
				if err != nil || current.Revision != grant.Revision {
					t.Fatalf("original grant changed: %+v err=%v", current, err)
				}
				if _, err := b.candidateOperation(t.Context(), result.Run, "", candidate, now); !errors.Is(err, ErrConversationSpace) {
					t.Errorf("accepted stale candidate preview: %v", err)
				}
				if _, err := b.queueCandidate(t.Context(), result.Run, "", candidate); !errors.Is(err, ErrConversationSpace) {
					t.Errorf("idempotent acceptance bypassed current scope: %v", err)
				}
				candidate.Key = "after-move"
				if _, err := b.queueCandidate(t.Context(), result.Run, "", candidate); !errors.Is(err, ErrConversationSpace) {
					t.Errorf("queued new candidate for stale destination: %v", err)
				}
				wait, reason, err := b.candidateEligibility(t.Context(), op, now.Add(2*time.Minute))
				if err != nil || wait != 0 || reason != "conversation_destination_changed" {
					t.Errorf("stale candidate remained eligible: wait=%d reason=%q err=%v", wait, reason, err)
				}
				if err := b.prepareScheduled(t.Context(), "tg", now.Add(2*time.Minute)); err != nil {
					t.Fatal(err)
				}
				var raw string
				var saved channelwire.Operation
				if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"tg/"+op.ID).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if json.Unmarshal([]byte(raw), &saved) != nil || saved.State != "cancelled" || saved.SuppressedReason != "conversation_destination_changed" || saved.Attempts != 0 || saved.Target != op.Target || saved.ConversationID != op.ConversationID {
					t.Fatalf("candidate was not cancelled at its original destination: %s", raw)
				}
				var count int
				if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/operation/%'`).Scan(&count); err != nil || count != 1 {
					t.Fatalf("stale acceptance created another delivery: count=%d err=%v", count, err)
				}
			})
		}
	}
}

func TestScheduledDeliveryRetainsDestinationAfterConversationRoutingChanges(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "remind later")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	candidate := ReplyCandidate{Key: "routing", Text: "fixed reminder", After: now.Add(time.Minute).UTC().Format(time.RFC3339Nano), Expires: now.Add(time.Hour).UTC().Format(time.RFC3339Nano), Timezone: "UTC"}
	op, err := b.queueCandidate(t.Context(), result.Run, "", candidate)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := tasks.core.Store.GetConversation(t.Context(), input.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := tasks.NewConversationInSpace(t.Context(), chat.SpaceID, "Other conversation")
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpaceMultiConversation(t.Context(), chat.SpaceID, true); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpaceDefaultConversation(t.Context(), chat.SpaceID, other.ID); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpaceActiveConversation(t.Context(), chat.SpaceID, other.ID); err != nil {
		t.Fatal(err)
	}
	again, err := b.queueCandidate(t.Context(), result.Run, "", candidate)
	if err != nil || again.ID != op.ID || again.ConversationID != input.ConversationID || again.Target != channelwire.Target(target) {
		t.Fatalf("routing changed the frozen destination: %+v err=%v", again, err)
	}
	if wait, reason, err := b.candidateEligibility(t.Context(), op, now.Add(2*time.Minute)); err != nil || wait != 0 || reason != "" {
		t.Fatalf("routing invalidated original destination: wait=%d reason=%q err=%v", wait, reason, err)
	}
}

func TestFollowupEligibilityQuietNewInputRevocationAndBudget(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "follow later")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	rule := FollowupRule{ID: "follow", Extension: "com.fixture.person", ConversationID: result.Run.ConversationID, Enabled: true, Timezone: "Asia/Shanghai", QuietStart: "23:00", QuietEnd: "08:00", DailyMax: 1, CooldownHours: 6, CancelOnInput: true, MaxDelaySeconds: 86400}
	config := tasks.Config()
	config.Delivery = DeliveryConfig{Enabled: true, Rules: []FollowupRule{rule}}
	if config.Extensions.Enabled == nil {
		config.Extensions.Enabled = map[string]bool{}
	}
	config.Extensions.Enabled[rule.Extension] = true
	tasks.core.setConfig(config)
	_, err = tasks.core.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, capabilityMetaPrefix+result.Run.ConversationID, string(mustJSON(CapabilitySelection{Extensions: []string{rule.Extension}})))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	candidate := ReplyCandidate{Key: "follow", RuleID: rule.ID, Text: "一次跟进", After: now.UTC().Format(time.RFC3339Nano), Expires: now.Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}
	op, err := b.queueCandidate(t.Context(), result.Run, rule.Extension, candidate)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation(rule.Timezone)
	quiet := time.Date(now.Year(), now.Month(), now.Day(), 23, 30, 0, 0, loc)
	op.NotBeforeMS = quiet.UnixMilli()
	op.ExpiresMS = quiet.Add(12 * time.Hour).UnixMilli()
	wait, reason, err := b.candidateEligibility(t.Context(), op, quiet)
	if err != nil || reason != "" || time.UnixMilli(wait).In(loc).Hour() != 8 {
		t.Fatal(wait, reason, err)
	}
	_, err = tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "2", Purpose: "background"}, "new input")
	if err != nil {
		t.Fatal(err)
	}
	_, reason, err = b.candidateEligibility(t.Context(), op, quiet)
	if err != nil || reason != "new_input" {
		t.Fatal(reason, err)
	}
	config.Delivery.Enabled = false
	tasks.core.setConfig(config)
	if err := tasks.cancelRevokedCandidates(t.Context()); err != nil {
		t.Fatal(err)
	}
	var raw string
	var saved channelwire.Operation
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"tg/"+op.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(raw), &saved) != nil || saved.State != "cancelled" {
		t.Fatal("disabled rule did not cancel unsent candidate", raw)
	}
}

func TestScheduledDeliveryControlRequiresExactUserConfirmation(t *testing.T) {
	_, tasks, target := expressionBridge(t, EchoModel{})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "later")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	control, err := tasks.Control()
	if err != nil {
		t.Fatal(err)
	}
	args := scheduleDeliveryArguments{RunID: result.Run.ID, Candidate: ReplyCandidate{Key: "confirmed", Text: "fixed notification", After: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), Expires: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Timezone: "UTC"}}
	operation, err := control.PlanControlChange(t.Context(), ControlPlanRequest{Kind: ControlScheduleDelivery, Arguments: mustJSON(args), ConversationID: result.Run.ConversationID, Authorization: ControlModelProposed})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/operation/%'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("proposal sent before user consent", count, err)
	}
	capability, err := control.IssueInteractiveCapability(t.Context(), result.Run.ConversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	confirmation, err := control.RenderConfirmation(t.Context(), operation.ID, result.Run.ConversationID, "user")
	if err != nil {
		t.Fatal(err)
	}
	applied, err := control.ConfirmControlChange(t.Context(), operation.ID, controlAuth(capability, result.Run.ConversationID, "user", confirmation.Code))
	if err != nil || !applied.Applied {
		t.Fatal(applied, err)
	}
}

func TestExtensionDeliveryRejectsPassiveUndeclaredAndExpiredInvocation(t *testing.T) {
	b, tasks, target := expressionBridge(t, EchoModel{})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	run, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	service, err := tasks.core.forRun(run.Run)
	if err != nil {
		t.Fatal(err)
	}
	host := &extensionHost{active: map[string]*extensionInvocation{}}
	inv := &extensionInvocation{id: "fixture", extensionID: "com.fixture.person", service: service, run: run.Run, ctx: context.Background()}
	if _, err := host.handleDeliveryRequest(inv, "delivery.resources", map[string]json.RawMessage{}); err == nil {
		t.Fatal("undeclared extension gained delivery")
	}
	inv.manifest.Delivery = true
	if _, err := host.handleDeliveryRequest(inv, "delivery.resources", map[string]json.RawMessage{}); err != nil {
		t.Fatal(err)
	}
	host.active[inv.id] = inv
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	inv.ctx = cancelled
	if _, err := host.invocation(inv.id, inv.extensionID); err == nil {
		t.Fatal("expired invocation accepted")
	}
	if b == nil {
		t.Fatal("missing bridge")
	}
}
