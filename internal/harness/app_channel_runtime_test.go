package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/endview/freeagent/internal/channels"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func appRuntimeFixture(t *testing.T, source string, model ModelClient) (*AppChannelRuntime, *TaskService, string, *atomic.Int32) {
	t.Helper()
	core, _, work := testHarness(t, model)
	tasks := NewTaskService(core)
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			fmt.Fprint(w, `{"code":0,"tenant_access_token":"FIXTURE_TOKEN","expire":7200}`)
		case "/app/getAppAccessToken":
			fmt.Fprint(w, `{"access_token":"FIXTURE_TOKEN","expires_in":"7200"}`)
		default:
			id := sends.Add(1)
			if source == SpaceSourceLark {
				fmt.Fprintf(w, `{"code":0,"data":{"message_id":"sent_%d"}}`, id)
			} else {
				fmt.Fprintf(w, `{"id":"sent_%d"}`, id)
			}
		}
	}))
	t.Cleanup(server.Close)
	r := newAppChannelRuntime(tasks, source)
	var err error
	if source == SpaceSourceLark {
		r.lark, err = channels.NewLarkAPI(server.URL, "app", "FIXTURE_SECRET", server.Client())
	} else {
		r.qq, err = channels.NewQQAPI(server.URL, server.URL, "app", "FIXTURE_SECRET", server.Client())
	}
	if err != nil {
		t.Fatal(err)
	}
	r.session.reset("app")
	r.running = true
	return r, tasks, work, &sends
}
func TestLarkRuntimeTenantPairingNoneAndWrongCardMessage(t *testing.T) {
	r, tasks, _, sends := appRuntimeFixture(t, SpaceSourceLark, EchoModel{})
	ctx := t.Context()
	code, _, _ := r.PairCode()
	event := channels.LarkEvent{App: "app", Tenant: "t1", Chat: "chat", Person: "lark:app:t1:open:u1", Message: "pair", Kind: "private", Text: "/pair " + code, Trigger: true}
	if err := r.HandleLark(ctx, event); err != nil {
		t.Fatal(err)
	}
	event.Message = "input"
	event.Text = "public chat"
	if err := r.HandleLark(ctx, event); err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 2 {
		t.Fatal(sends.Load())
	}
	if err := r.HandleLark(ctx, event); err != nil || sends.Load() != 2 {
		t.Fatalf("duplicate=%v %d", err, sends.Load())
	}
	target := ChannelTarget{SpaceSourceLark, "app:t1", "chat"}
	space, err := tasks.ChannelSpace(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := tasks.Latest(ctx, space.DefaultConversationID)
	if run.Permission.Mode != PermissionNone || run.Permission.PersonID != event.Person {
		t.Fatalf("permission=%+v", run.Permission)
	}
	if _, err := tasks.ChannelSpace(ctx, ChannelTarget{SpaceSourceLark, "app:t2", "chat"}); err == nil {
		t.Fatal("tenants merged")
	}
	if _, err := tasks.CheckChannelRunAction(ctx, target, event.Person, run.ID, "stop"); err != nil {
		t.Fatalf("original None cannot stop chat: %v", err)
	}
	view, err := tasks.Chat(ctx, space.DefaultConversationID, "")
	if err != nil || len(view.Turns) != 1 {
		t.Fatal(err)
	}
	event.Kind = "group"
	event.Message = "background"
	event.Text = "another fact"
	event.Trigger = false
	r.HandleLark(ctx, event)
	view, err = tasks.Chat(ctx, space.DefaultConversationID, "")
	if err != nil || len(view.Background) != 1 {
		t.Fatal(view.Background, err)
	}
}
func TestQQRuntimeLocalApprovalWindowAndSavedStatus(t *testing.T) {
	var calls atomic.Int32
	r, tasks, work, sends := appRuntimeFixture(t, SpaceSourceQQ, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		if calls.Add(1) == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("create", "file_create", map[string]string{"path": "qq.txt", "content": "approved"})}}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}))
	code, _, _ := r.PairCode()
	e := channels.QQEvent{App: "app", Chat: "c2c:user", Person: "qq:app:c2c:user", Message: "pair", Kind: "private", Text: "/pair " + code}
	if err := r.HandleQQ(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	target := ChannelTarget{SpaceSourceQQ, "app", e.Chat}
	space, _ := tasks.ChannelSpace(t.Context(), target)
	tasks.SetSpacePermission(t.Context(), space.ID, PermissionAsk, false)
	tasks.SetPersonPermission(t.Context(), e.Person, "user", PermissionAsk, false)
	e.Message = "request"
	e.Text = "create a file"
	if err := r.HandleQQ(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 2 {
		t.Fatal(sends.Load())
	}
	run, _ := tasks.Latest(t.Context(), space.DefaultConversationID)
	if run.Status != "waiting_confirmation" || len(r.session.callbacks) != 0 {
		t.Fatalf("QQ should require local approval: %s", run.Status)
	}
	confirm, err := tasks.Preview(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.Confirm(t.Context(), run.ID, confirm, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(work, "qq.txt"))
	if err != nil || string(raw) != "approved" {
		t.Fatal(string(raw), err)
	}
	r.mu.Lock()
	r.replies[e.Chat+"/request"].at = time.Now().Add(-3 * time.Minute)
	r.mu.Unlock()
	before := sends.Load()
	if err := r.Deliver(t.Context(), result); err == nil || sends.Load() != before {
		t.Fatal("expired passive window sent/replayed")
	}
	if err := r.Deliver(t.Context(), result); err != nil {
		t.Fatal("duplicate receipt should not retry", err)
	}
	e.Message = "status"
	e.Text = "/status"
	if err := r.HandleQQ(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if sends.Load() != before+1 || calls.Load() != 2 {
		t.Fatalf("status replayed model or did not send: %d %d", sends.Load(), calls.Load())
	}
	var uncertain int
	err = tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM meta WHERE key LIKE 'channel/send/%' AND json_extract(value,'$.state')='failed'`).Scan(&uncertain)
	if err != nil || uncertain != 1 {
		t.Fatalf("delivery uncertainty=%d %v", uncertain, err)
	}
}
func TestChannelApprovalWrongTargetRequesterMessageAndDuplicateAreDenied(t *testing.T) {
	var calls atomic.Int32
	r, tasks, work, _ := appRuntimeFixture(t, SpaceSourceLark, modelFunc(func(context.Context, ModelRequest, func(string)) (ModelResponse, error) {
		if calls.Add(1) == 1 {
			return ModelResponse{ToolCalls: []ToolCall{makeCall("write", "file_create", map[string]string{"path": "card.txt", "content": "yes"})}}, nil
		}
		return ModelResponse{Content: "done"}, nil
	}))
	code, _, _ := r.PairCode()
	e := channels.LarkEvent{App: "app", Tenant: "t", Chat: "chat", Person: "lark:app:t:open:user", Message: "pair", Kind: "private", Trigger: true, Text: "/pair " + code}
	r.HandleLark(t.Context(), e)
	space, _ := tasks.ChannelSpace(t.Context(), ChannelTarget{SpaceSourceLark, "app:t", "chat"})
	tasks.SetSpacePermission(t.Context(), space.ID, PermissionAsk, false)
	tasks.SetPersonPermission(t.Context(), e.Person, "User", PermissionAsk, false)
	e.Message = "request"
	e.Text = "write"
	if err := r.HandleLark(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	var token string
	var item channelApproval
	r.session.mu.Lock()
	for key, v := range r.session.callbacks {
		if v.Approved {
			token, item = key, v
		}
	}
	r.session.mu.Unlock()
	if token == "" || item.Message == "" {
		t.Fatal("missing bound approval")
	}
	callback := e
	callback.Callback = token
	callback.CallbackMessage = item.Message
	callback.Message = item.Message
	for _, mutation := range []func(*channels.LarkEvent){func(e *channels.LarkEvent) { e.Person = "lark:app:t:open:other" }, func(e *channels.LarkEvent) { e.CallbackMessage = "other" }, func(e *channels.LarkEvent) { e.Tenant = "other" }} {
		wrong := callback
		mutation(&wrong)
		if err := r.HandleLark(t.Context(), wrong); err == nil {
			t.Fatal("forged callback accepted")
		}
	}
	if err := r.HandleLark(t.Context(), callback); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(work, "card.txt")); err != nil {
		t.Fatal(err)
	}
	if err := r.HandleLark(t.Context(), callback); err == nil {
		t.Fatal("callback replay accepted")
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}
func TestChannelSendClaimSurvivesUncertainDeliveryWithoutReplay(t *testing.T) {
	core, _, _ := testHarness(t, EchoModel{})
	tasks := NewTaskService(core)
	target := ChannelTarget{SpaceSourceTelegram, "1", "90"}
	tasks.PairChannel(t.Context(), target, "private", "receipt")
	grantTelegramFixture(t, tasks, TelegramAccessChange{Target: target, Kind: "private", Members: []string{"telegram:user:7"}, Enabled: true})
	input, err := tasks.ReceiveChannelText(t.Context(), ChannelMessageSource{ChannelTarget: target, PersonID: "telegram:user:7", MessageID: "m1", Purpose: "request"}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	result, err := tasks.SubmitChannel(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	sends := 0
	s := newChannelSession(tasks, SpaceSourceTelegram, func(context.Context, ChannelTarget, string, string, []channelButton) (string, error) {
		sends++
		return "", fmt.Errorf("delivery unknown")
	})
	if err := s.Deliver(t.Context(), result); err == nil {
		t.Fatal("uncertainty lost")
	}
	second := newChannelSession(tasks, SpaceSourceTelegram, s.send)
	if err := second.Deliver(t.Context(), result); err != nil || sends != 1 {
		t.Fatalf("restart send=%d %v", sends, err)
	}
	evidence, err := tasks.core.Store.Evidence(t.Context(), result.Run.ID)
	if err != nil || len(evidence.Uncertainty) != 1 || !strings.Contains(evidence.Uncertainty[0], "channel delivery UNKNOWN") {
		t.Fatalf("delivery receipt not inspectable: %+v %v", evidence, err)
	}
	view, err := tasks.Chat(t.Context(), input.ConversationID, "")
	if err != nil || len(view.Turns) != 1 || len(view.Turns[0].Evidence.Uncertainty) != 1 {
		t.Fatalf("GUI lost delivery uncertainty: %+v %v", view, err)
	}
	messages, _ := tasks.Messages(t.Context(), input.ConversationID)
	for _, m := range messages {
		if m.Public && m.Role == "assistant" {
			t.Fatal("unconfirmed text became public")
		}
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "FIXTURE_SECRET") {
		t.Fatal("fixture secret entered result")
	}
}

func TestChannelNoOpCompactRepliesWithoutSyntheticRun(t *testing.T) {
	r, tasks, _, sends := appRuntimeFixture(t, SpaceSourceLark, EchoModel{})
	code, _, _ := r.PairCode()
	e := channels.LarkEvent{App: "app", Tenant: "t", Chat: "chat", Person: "lark:app:t:open:u", Message: "pair", Kind: "private", Text: "/pair " + code, Trigger: true}
	if err := r.HandleLark(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	space, err := tasks.ChannelSpace(t.Context(), ChannelTarget{SpaceSourceLark, "app:t", "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetSpacePermission(t.Context(), space.ID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	if err := tasks.SetPersonPermission(t.Context(), e.Person, "u", PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	e.Message = "compact"
	e.Text = "/compact"
	if err := r.HandleLark(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	if sends.Load() != 2 {
		t.Fatal("no-op compact silently disappeared", sends.Load())
	}
	var count int
	if err := tasks.core.Store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM runs WHERE conversation_id=?`, space.DefaultConversationID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("no-op invented run: %d %v", count, err)
	}
}
