package harness

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/channelwire"
	"github.com/endview/freeagent/internal/webaccess"
)

func TestTelegramGroupToolsShareCoreAndRequireOriginalApproval(t *testing.T) {
	var searches atomic.Int32
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"title":"Public fixture","url":"https://example.org/source","content":"Search result"}]}`)
	}))
	defer search.Close()
	calls := 0
	b, tasks, target := groupBridge(t, modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
		calls++
		for _, message := range request.Messages {
			if strings.Contains(message.Content, "OWNER_ONLY_TRANSCRIPT") {
				t.Fatal("private transcript reached group model")
			}
		}
		switch calls {
		case 1:
			return ModelResponse{ToolCalls: []ToolCall{makeCall("read", "file_read", map[string]string{"path": "public.txt"})}}, nil
		case 2:
			if !strings.Contains(string(mustJSON(request.Messages)), "GROUP_PUBLIC_FIXTURE") {
				t.Fatal("public read did not reach model")
			}
			return ModelResponse{ToolCalls: []ToolCall{makeCall("write", "file_create", map[string]string{"path": "group-approved.txt", "content": "saved once"})}}, nil
		case 3:
			return ModelResponse{ToolCalls: []ToolCall{makeCall("search", "web_search", map[string]string{"query": "public reference"})}}, nil
		default:
			if !strings.Contains(string(mustJSON(request.Messages)), "https://example.org/source") {
				t.Fatal("search result did not reach model")
			}
			return ModelResponse{Content: "group task completed"}, nil
		}
	}))
	ctx := t.Context()
	target.ThreadID = "11"
	core := tasks.core
	config := tasks.Config()
	workspace := config.Workspace
	if err := os.WriteFile(filepath.Join(workspace, "public.txt"), []byte("GROUP_PUBLIC_FIXTURE"), 0600); err != nil {
		t.Fatal(err)
	}
	keys := NewKeyStore(filepath.Join(t.TempDir(), "keys.json"))
	key, err := keys.Add(KeyInput{Name: "isolated search", Protocol: KeyProtocolSearch, BaseURL: search.URL, Source: KeySourceNone})
	if err != nil {
		t.Fatal(err)
	}
	policy := webaccess.DefaultConfig()
	policy.Mode, policy.DefaultConnection = "api", "fixture"
	policy.Connections = map[string]webaccess.Connection{"fixture": {Provider: "searxng", Key: key.ID}}
	config.WebSearch, config.Tools.Network = &policy, true
	runtime := NewRuntimeTools(core.Tools.(*FileTools))
	runtime.SetKeyStore(keys)
	core.Keys, core.Tools = keys, NewToolRuntimeManager(runtime, config)
	core.setConfig(config)
	chat, err := tasks.ChannelConversation(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	space, _ := tasks.ChannelSpace(ctx, target)
	if err = tasks.SetSpacePermission(ctx, space.ID, PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	if err = tasks.SetPersonPermission(ctx, "telegram:user:7", "Fixture A", PermissionAsk, false); err != nil {
		t.Fatal(err)
	}
	if _, err = core.Store.db.ExecContext(ctx, `INSERT INTO fa.messages(conversation_id,body_json) VALUES($1,$2)`, chat, string(mustJSON(ModelMessage{Kind: messageUser, Role: "user", Content: "OWNER_ONLY_TRANSCRIPT"}))); err != nil {
		t.Fatal(err)
	}
	if err = b.Inbound(ctx, "tg", groupEvent(target, 10, "Read public fixture, create approved file, and search")); err != nil {
		t.Fatal(err)
	}
	run, err := tasks.Latest(ctx, chat)
	if err != nil || run.Status != "waiting_confirmation" || calls != 2 {
		t.Fatal(run.Status, calls, err)
	}
	ready, err := b.Outbox(ctx, "tg")
	if err != nil {
		t.Fatal(err)
	}
	var card channelwire.Operation
	for _, op := range ready {
		if len(op.Buttons) == 2 {
			card = op
		}
	}
	if card.ID == "" || ChannelTarget(card.Target) != target {
		t.Fatal("wrong approval route", ready)
	}
	if err = b.Receipt(ctx, "tg", channelwire.Receipt{ID: card.ID, Dispatch: card.Dispatch, State: "sent", MessageID: "101"}); err != nil {
		t.Fatal(err)
	}
	callback := groupEvent(target, 11, "")
	callback.Person, callback.Callback, callback.CallbackMessage = "telegram:user:8", card.Buttons[0].Token, "101"
	if err = b.Inbound(ctx, "tg", callback); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(workspace, "group-approved.txt")); !os.IsNotExist(err) {
		t.Fatal("non-requester could approve write", err)
	}
	callback.ID, callback.Person = "12", "telegram:user:7"
	if err = b.Inbound(ctx, "tg", callback); err != nil {
		t.Fatal(err)
	}
	run, err = tasks.Latest(ctx, chat)
	if err != nil || run.Status != "completed" || searches.Load() != 1 {
		t.Fatal(run.Status, searches.Load(), err)
	}
	raw, err := os.ReadFile(filepath.Join(workspace, "group-approved.txt"))
	if err != nil || string(raw) != "saved once" {
		t.Fatal(string(raw), err)
	}
}
