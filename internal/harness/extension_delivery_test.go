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
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const deliveryTestExtension = "com.example.delivery"
const deliveryTestReview = "delivery review contract verified"

// Run by the existing --freeagent-extension-host-helper TestMain entrypoint.
// All calls below travel over the actual bidirectional NDJSON subprocess pipe.
type extensionDeliveryTestHelper struct {
	mode, finishedInvocation, runID, conversationID, id string
	candidate                                           ReplyCandidate
	candidates                                          map[string]ReplyCandidate
}

func (helper *extensionDeliveryTestHelper) handle(request *jsonrpc.Request, hostRequest func(string, map[string]any) (*jsonrpc.Response, bool)) (any, *jsonrpc.Error) {
	failure := func(format string, args ...any) (any, *jsonrpc.Error) {
		return nil, &jsonrpc.Error{Code: -32603, Message: fmt.Sprintf(format, args...)}
	}
	var params map[string]json.RawMessage
	if json.Unmarshal(request.Params, &params) != nil && request.Method != "shutdown" {
		return failure("invalid fixture callback params")
	}
	var invocation string
	_ = json.Unmarshal(params["invocation"], &invocation)
	call := func(method string, fields map[string]any, code int) (map[string]any, bool) {
		if fields == nil {
			fields = map[string]any{}
		}
		if _, exists := fields["invocation"]; !exists {
			fields["invocation"] = invocation
		}
		response, ok := hostRequest(method, fields)
		if !ok {
			return nil, false
		}
		if code != 0 {
			var rpcErr *jsonrpc.Error
			return nil, errors.As(response.Error, &rpcErr) && rpcErr.Code == int64(code)
		}
		var result map[string]any
		if response.Error != nil || json.Unmarshal(response.Result, &result) != nil {
			return nil, false
		}
		return result, true
	}
	if strings.HasPrefix(helper.mode, "parts-") {
		return helper.handleReplyParts(request)
	}
	switch request.Method {
	case "initialize":
		provides, events := []string{"delivery"}, []string{"turn.finished", "delivery.check"}
		if helper.mode == "unregistered-capability" {
			provides = []string{}
		}
		if helper.mode == "unregistered-event" {
			events = []string{"turn.finished"}
		}
		return map[string]any{"id": deliveryTestExtension, "version": "1.0.0", "provides": provides, "events": events}, nil
	case "turn.finished":
		helper.finishedInvocation = invocation
		_ = json.Unmarshal(params["run_id"], &helper.runID)
		_ = json.Unmarshal(params["conversation_id"], &helper.conversationID)
		var delivery ExtensionDeliveryContext
		if json.Unmarshal(params["delivery"], &delivery) != nil || !delivery.Reply || len(delivery.RuleIDs) != 1 || delivery.MaxTextParts != 3 || delivery.MaxStickers != 1 {
			return failure("missing scoped delivery context: %s", params["delivery"])
		}
		now := time.Now().UTC()
		helper.candidate = ReplyCandidate{Key: "one-followup", RuleID: delivery.RuleIDs[0], Text: "Frozen follow-up text.", After: now.Add(20 * time.Minute).Format(time.RFC3339Nano), Expires: now.Add(time.Hour).Format(time.RFC3339Nano)}
		receipt, ok := call("delivery.propose", map[string]any{"candidate": helper.candidate}, 0)
		if !ok || receipt["state"] != "scheduled" || receipt["sent"] != false {
			return failure("proposal did not return an unsent scheduled receipt: %v", receipt)
		}
		helper.id, _ = receipt["id"].(string)
		helper.candidates = map[string]ReplyCandidate{helper.id: helper.candidate}
		again, ok := call("delivery.propose", map[string]any{"candidate": helper.candidate}, 0)
		if !ok || again["id"] != helper.id {
			return failure("duplicate source candidate was not idempotent")
		}
		changed := helper.candidate
		changed.Text = "Do not replace frozen content."
		if _, ok := call("delivery.propose", map[string]any{"candidate": changed}, -32020); !ok {
			return failure("same source key accepted different content")
		}
		if _, ok := call("delivery.propose", map[string]any{"candidate": helper.candidate, "target": "another-chat"}, -32602); !ok {
			return failure("delivery accepted an arbitrary target")
		}
		if _, ok := call("delivery.resources", nil, 0); !ok {
			return failure("scoped resources unavailable")
		}
		if helper.mode == "poll-budget" {
			for index := 2; index <= 4; index++ {
				candidate := helper.candidate
				candidate.Key = fmt.Sprintf("one-followup-%d", index)
				receipt, ok := call("delivery.propose", map[string]any{"candidate": candidate}, 0)
				id, _ := receipt["id"].(string)
				if !ok || id == "" || receipt["state"] != "scheduled" || receipt["sent"] != false {
					return failure("additional budget candidate was not scheduled: %v", receipt)
				}
				helper.candidates[id] = candidate
			}
		}
		return map[string]any{}, nil
	case "delivery.check":
		var identity ExtensionIdentity
		var candidate struct {
			ID, Key, Text, Kind string
			RuleID              string `json:"rule_id"`
			ExpiresMS           int64  `json:"expires_ms"`
		}
		if json.Unmarshal(params["candidate"], &candidate) != nil {
			return failure("invalid review candidate")
		}
		original, exists := helper.candidates[candidate.ID]
		if json.Unmarshal(params["identity"], &identity) != nil || identity.RunID != helper.runID || identity.ConversationID != helper.conversationID || len(identity.Inputs) != 1 || identity.Inputs[0].ActorID != "telegram:user:7" ||
			!exists || candidate.Key != original.Key || candidate.Text != original.Text || candidate.Kind != "text" || candidate.ExpiresMS <= time.Now().UnixMilli() || candidate.RuleID != original.RuleID || invocation == "" || invocation == helper.finishedInvocation {
			return failure("review did not preserve original identity and frozen candidate")
		}
		if _, ok := call("delivery.read", map[string]any{"invocation": helper.finishedInvocation, "id": helper.id}, -32005); !ok {
			return failure("completed hook invocation remained usable")
		}
		if receipt, ok := call("delivery.read", map[string]any{"id": candidate.ID}, 0); !ok || receipt["state"] != "reviewing" || receipt["sent"] != false {
			return failure("review cannot read its unsent candidate: %v", receipt)
		}
		for _, blocked := range []struct {
			method string
			fields map[string]any
		}{
			{"model.generate", map[string]any{"messages": []map[string]string{{"role": "user", "content": "unauthorized generation"}}}},
			{"model.embed", map[string]any{"inputs": []string{"unauthorized embedding"}}},
			{"tool.call", map[string]any{"name": "file_external", "arguments": map[string]any{}}},
			{"task.delegate", map[string]any{"plan": map[string]any{}}},
			{"delivery.propose", map[string]any{"candidate": helper.candidate}},
			{"delivery.resources", nil},
			{"reply.suggest", map[string]any{"mode": "single"}},
		} {
			if _, ok := call(blocked.method, blocked.fields, -32004); !ok {
				return failure("delivery.check did not reject %s as unavailable", blocked.method)
			}
		}
		if helper.mode == "withdraw" {
			if receipt, ok := call("delivery.withdraw", map[string]any{"id": helper.id}, 0); !ok || receipt["state"] != "cancelled" || receipt["suppressed_reason"] != "extension_withdrawn" {
				return failure("review could not withdraw its unattempted candidate")
			}
		}
		// Parent tests require this marker even for deny/error cases, so a
		// fixture assertion failure cannot masquerade as a successful denial.
		if _, ok := call("activity.report", map[string]any{"summary": deliveryTestReview}, 0); !ok {
			return failure("review could not report bounded activity")
		}
		switch helper.mode {
		case "deny", "poll-budget":
			return map[string]any{"allow": false}, nil
		case "malformed":
			return map[string]any{"allow": true, "replacement_text": "not permitted"}, nil
		case "wrong-type":
			return map[string]any{"allow": "true"}, nil
		case "rpc-error":
			return failure("fixture review unavailable")
		case "exit":
			os.Exit(0)
		case "timeout":
			time.Sleep(6 * time.Second)
		}
		return map[string]any{"allow": true}, nil
	case "shutdown":
		return nil, nil
	default:
		return failure("unexpected delivery fixture method %s", request.Method)
	}
}

type extensionDeliveryFixture struct {
	bridge             *ChannelBridge
	tasks              *TaskService
	target             ChannelTarget
	run                Run
	op                 channelwire.Operation
	marker             string
	attempts           []Attempt
	modelCalls         atomic.Int64
	reviews            atomic.Int64
	reviewGate         func()
	expectedOperations int
}

func newExtensionDeliveryFixture(t *testing.T, mode string) *extensionDeliveryFixture {
	t.Helper()
	f := &extensionDeliveryFixture{expectedOperations: 1}
	if mode == "poll-budget" {
		f.expectedOperations = 4
	}
	f.bridge, f.tasks, f.target = expressionBridge(t, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		f.modelCalls.Add(1)
		return ModelResponse{Content: "Original reply."}, nil
	}))
	core := f.tasks.core
	f.marker = filepath.Join(core.Config.Workspace, "unauthorized-delivery-tool")
	core.Tools = permissionExternalTool{marker: f.marker}
	input, err := f.tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: f.target, PersonID: "telegram:user:7", MessageID: "1", Purpose: "request"}, "Follow up once later.")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := core.Store.GetConversation(t.Context(), input.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	// Full access makes the tool/model probes meaningful: the review-specific
	// gate must reject them before any ordinary execution or approval attempt.
	if err := f.tasks.SetSpacePermission(t.Context(), chat.SpaceID, PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.SetPersonPermission(t.Context(), "telegram:user:7", "Fixture", PermissionFull, true); err != nil {
		t.Fatal(err)
	}
	config := f.tasks.Config()
	config.Extensions.Enabled = map[string]bool{deliveryTestExtension: true}
	config.Delivery = DeliveryConfig{Enabled: true, Rules: []FollowupRule{{ID: "follow", Extension: deliveryTestExtension, ConversationID: input.ConversationID, Enabled: true, Timezone: "UTC", DailyMax: 1, CooldownHours: 6, CancelOnInput: true, MaxDelaySeconds: 86400}}}
	core.setConfig(config)
	root := filepath.Join(core.Store.DataDir(), "extensions", deliveryTestExtension)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := ExtensionManifest{Protocol: extensionProtocol, ID: deliveryTestExtension, Version: "1.0.0", Name: "Delivery contract fixture", Description: "Actual NDJSON delivery review", Run: ExtensionRun{Command: os.Args[0], Args: []string{"--freeagent-extension-host-helper", "--freeagent-delivery-check=" + mode}}, Delivery: true, Events: []string{"turn.finished", "delivery.check"}, TimeoutMS: 10000, Budget: ExtensionBudget{ModelCalls: 2, OutputTokens: 64}}
	if err := os.WriteFile(filepath.Join(root, "extension.json"), mustJSON(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.tasks.SelectCapabilities(t.Context(), input.ConversationID, CapabilitySelection{Extensions: []string{deliveryTestExtension}}); err != nil {
		t.Fatal(err)
	}
	core.extensions = newExtensionHost(core.Store.DataDir(), config.Workspace, config, nil)
	t.Cleanup(func() { _ = core.extensions.close() })
	core.Emit = func(event Event) {
		if event.Kind == EventExtensionError {
			t.Logf("extension fixture error: %s", event.Message)
		}
		if event.Kind == EventExtensionActivity && event.Message == deliveryTestReview {
			f.reviews.Add(1)
			if f.reviewGate != nil {
				f.reviewGate()
			}
		}
	}
	f.tasks.SetEvents(core.Emit)
	result, err := f.tasks.SubmitChannel(t.Context(), input)
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("original Run status=%s err=%v", result.Run.Status, err)
	}
	f.run = result.Run
	var raw string
	if err := core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key LIKE 'channel/operation/%' AND document->>'candidate_id'='one-followup'`).Scan(&raw); err != nil {
		t.Fatalf("turn.finished did not propose a candidate: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &f.op); err != nil || f.op.State != "scheduled" || f.op.Extension != deliveryTestExtension || f.op.RunID != f.run.ID || f.op.Target != channelwire.Target(f.target) {
		t.Fatalf("candidate=%s err=%v", raw, err)
	}
	f.attempts, err = core.Store.ListAttempts(t.Context(), f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ops, err := f.bridge.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 || f.reviews.Load() != 0 {
		t.Fatalf("candidate reviewed/dispatched before due: ops=%v reviews=%d err=%v", ops, f.reviews.Load(), err)
	}
	// Advance only this candidate's stored due time, without wall-clock sleeps.
	f.op.NotBeforeMS = time.Now().Add(-time.Second).UnixMilli()
	if _, err := core.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(f.op)), channelOperationPrefix+"tg/"+f.op.ID); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *extensionDeliveryFixture) saved(t *testing.T, ids ...string) channelwire.Operation {
	t.Helper()
	id := f.op.ID
	if len(ids) > 0 {
		id = ids[0]
	}
	var raw string
	var saved channelwire.Operation
	if err := f.tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT value FROM fa.meta WHERE key=$1`, channelOperationPrefix+"tg/"+id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func (f *extensionDeliveryFixture) assertNoExecution(t *testing.T) {
	t.Helper()
	if calls := f.modelCalls.Load(); calls != 1 {
		t.Errorf("delivery generated a model response: calls=%d", calls)
	}
	if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
		t.Errorf("delivery ran an unauthorized tool: stat=%v", err)
	}
	attempts, err := f.tasks.core.Store.ListAttempts(t.Context(), f.run.ID)
	if err != nil || string(mustJSON(attempts)) != string(mustJSON(f.attempts)) {
		t.Errorf("delivery created or changed execution attempts: attempts=%+v err=%v", attempts, err)
	}
	var operations, runs int
	if err := f.tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM fa.meta WHERE key LIKE 'channel/operation/%'),(SELECT count(*) FROM fa.runs)`).Scan(&operations, &runs); err != nil || operations != f.expectedOperations || runs != 1 {
		t.Errorf("delivery re-proposed/delegated: operations=%d runs=%d err=%v", operations, runs, err)
	}

}

func TestExtensionDeliveryNDJSONReviewContract(t *testing.T) {
	for _, mode := range []string{"allow", "deny", "withdraw", "malformed", "wrong-type", "rpc-error", "exit", "timeout", "unregistered-capability", "unregistered-event"} {
		t.Run(mode, func(t *testing.T) {
			f := newExtensionDeliveryFixture(t, mode)
			ops, err := f.bridge.Outbox(t.Context(), "tg")
			if err != nil {
				t.Fatal(err)
			}
			wantReviews := int64(1)
			if mode == "unregistered-capability" || mode == "unregistered-event" {
				wantReviews = 0
			}
			if f.reviews.Load() != wantReviews {
				t.Fatalf("review fixture assertions were not reached: reviews=%d want=%d", f.reviews.Load(), wantReviews)
			}
			saved := f.saved(t)
			if mode == "allow" {
				if len(ops) != 1 || ops[0].ID != f.op.ID || ops[0].Text != f.op.Text || ops[0].Target != f.op.Target || ops[0].RunID != f.run.ID || saved.Attempts != 1 || saved.State != "dispatching" {
					t.Fatalf("allowed candidate was not dispatched exactly once: ops=%+v saved=%+v", ops, saved)
				}
				if err := f.bridge.Receipt(t.Context(), "tg", channelwire.Receipt{ID: ops[0].ID, Dispatch: ops[0].Dispatch, State: "unknown"}); err != nil {
					t.Fatal(err)
				}
			} else {
				reason := "extension_review_unavailable_or_withdrawn"
				if mode == "withdraw" {
					reason = "extension_withdrawn"
				}
				if len(ops) != 0 || saved.State != "cancelled" || saved.SuppressedReason != reason || saved.Attempts != 0 {
					t.Fatalf("review failed open: ops=%+v saved=%+v", ops, saved)
				}
			}
			if again, err := f.bridge.Outbox(t.Context(), "tg"); err != nil || len(again) != 0 || f.reviews.Load() != wantReviews {
				t.Fatalf("settled/UNKNOWN candidate was reviewed or sent again: ops=%+v reviews=%d err=%v", again, f.reviews.Load(), err)
			}
			f.assertNoExecution(t)
		})
	}
}

func TestExtensionDeliveryNDJSONOneReviewPerPollStillExpiresOtherCandidates(t *testing.T) {
	f := newExtensionDeliveryFixture(t, "poll-budget")
	rows, err := f.tasks.core.Store.db.QueryContext(t.Context(), `SELECT value FROM fa.meta WHERE key LIKE 'channel/operation/%' ORDER BY document->>'candidate_id'`)
	if err != nil {
		t.Fatal(err)
	}
	var candidates []channelwire.Operation
	for rows.Next() {
		var raw string
		var op channelwire.Operation
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &op); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		candidates = append(candidates, op)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 4 {
		t.Fatalf("subprocess proposals=%d want=4", len(candidates))
	}
	now := time.Now()
	for index := range candidates {
		op := &candidates[index]
		op.NotBeforeMS = now.Add(time.Duration(index-4) * time.Second).UnixMilli()
		if index == 3 {
			op.ExpiresMS = now.Add(-time.Second).UnixMilli()
		}
		if _, err := f.tasks.core.Store.db.ExecContext(t.Context(), `UPDATE fa.meta SET value=$1 WHERE key=$2`, string(mustJSON(op)), channelOperationPrefix+"tg/"+op.ID); err != nil {
			t.Fatal(err)
		}
	}
	for poll := 1; poll <= 3; poll++ {
		if ops, err := f.bridge.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 || f.reviews.Load() != int64(poll) {
			t.Fatalf("poll %d exceeded/stalled its review budget: ops=%+v reviews=%d err=%v", poll, ops, f.reviews.Load(), err)
		}
		for index, op := range candidates {
			state, reason := "scheduled", ""
			if index == 3 {
				state, reason = "cancelled", "expired"
			} else if index < poll {
				state, reason = "cancelled", "extension_review_unavailable_or_withdrawn"
			}
			if saved := f.saved(t, op.ID); saved.State != state || saved.SuppressedReason != reason || saved.Attempts != 0 {
				t.Fatalf("poll %d candidate %d: state=%+v want=%s/%s", poll, index, saved, state, reason)
			}
		}
	}
	if ops, err := f.bridge.Outbox(t.Context(), "tg"); err != nil || len(ops) != 0 || f.reviews.Load() != 3 {
		t.Fatalf("settled candidates replayed: ops=%+v reviews=%d err=%v", ops, f.reviews.Load(), err)
	}
	f.assertNoExecution(t)
}

func TestExtensionDeliveryNDJSONInvalidationBeforeAndDuringReview(t *testing.T) {
	for _, phase := range []string{"before", "during"} {
		for _, change := range []string{"new-input", "rule-changed", "disabled", "deselected", "access-revoked"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				f := newExtensionDeliveryFixture(t, "allow")
				invalidate := func() string {
					switch change {
					case "new-input":
						if _, err := f.tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: f.target, PersonID: "telegram:user:7", MessageID: "2", Purpose: "background"}, "Already resolved."); err != nil {
							t.Fatal(err)
						}
						return "new_input"
					case "deselected":
						if err := f.tasks.SelectCapabilities(t.Context(), f.run.ConversationID, CapabilitySelection{}); err != nil {
							t.Fatal(err)
						}
						return "extension_deselected"
					case "access-revoked":
						grantTelegramFixture(t, f.tasks, TelegramAccessChange{Target: f.target, Kind: "private", Enabled: false, Members: []string{"telegram:user:7"}})
						// Admission control synchronously cancels unsent deliveries
						// with a durable reason before the next transport poll.
						var cancelled bool
						if err := f.tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM fa.meta WHERE key=$1)`, "channel/run-cancelled/"+f.run.ID).Scan(&cancelled); err != nil || !cancelled {
							t.Fatalf("access revocation did not persist delivery cancellation: cancelled=%v err=%v", cancelled, err)
						}
						return "authorization_revoked"
					default:
						config := f.tasks.Config()
						if change == "disabled" {
							config.Delivery.Enabled = false
							f.tasks.core.setConfig(config)
							return "disabled"
						}
						config.Delivery.Rules[0].DailyMax++
						f.tasks.core.setConfig(config)
						return "rule_changed_or_revoked"
					}
				}
				var ops []channelwire.Operation
				var err error
				var reason string
				if phase == "before" {
					reason = invalidate()
					ops, err = f.bridge.Outbox(t.Context(), "tg")
				} else {
					ready, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
					var release sync.Once
					defer release.Do(func() { close(resume) })
					f.reviewGate = func() { close(ready); <-resume }
					go func() { defer close(done); ops, err = f.bridge.Outbox(t.Context(), "tg") }()
					select {
					case <-ready:
					case <-done:
						t.Fatalf("review ended before its contract probes: ops=%v err=%v", ops, err)
					case <-time.After(10 * time.Second):
						t.Fatal("review never reached its NDJSON activity barrier")
					}
					reason = invalidate()
					release.Do(func() { close(resume) })
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Fatal("review did not settle after invalidation")
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				saved := f.saved(t)
				wantReviews := int64(0)
				if phase == "during" {
					wantReviews = 1
				}
				if len(ops) != 0 || saved.State != "cancelled" || saved.SuppressedReason != reason || saved.Attempts != 0 || f.reviews.Load() != wantReviews {
					t.Fatalf("invalidation did not suppress delivery: ops=%+v saved=%+v reviews=%d", ops, saved, f.reviews.Load())
				}
				f.assertNoExecution(t)
			})
		}
	}
}
