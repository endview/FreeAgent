package channelplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channels"
	"github.com/endview/freeagent/internal/channelwire"
)

func TestTelegramActivityUsesLiveCoreLeaseAndStopsWithContext(t *testing.T) {
	var visible atomic.Bool
	visible.Store(true)
	var calls, polls atomic.Int32
	notified := make(chan struct{}, 1)
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls.Add(1)
		if !strings.HasSuffix(r.URL.Path, "/activities") {
			t.Error("activity used durable outbox")
		}
		var activities []channelwire.Activity
		if visible.Load() {
			activities = []channelwire.Activity{{Target: channelwire.Target{Source: "telegram", ApplicationID: "1", ChatID: "90"}, Action: "typing", UntilMS: time.Now().Add(8 * time.Second).UnixMilli()}}
		}
		_ = json.NewEncoder(w).Encode(activities)
	}))
	defer core.Close()
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendChatAction") {
			t.Error("activity sent a real message")
		}
		var input struct {
			Chat   int64  `json:"chat_id"`
			Action string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input.Chat != 90 || input.Action != "typing" {
			t.Error(input)
		}
		calls.Add(1)
		select {
		case notified <- struct{}{}:
		default:
		}
		fmt.Fprint(w, `{"ok":true,"result":true}`)
	}))
	defer platform.Close()
	api, _ := channels.NewTelegramAPI(platform.URL, "1:FIXTURE_TOKEN", platform.Client())
	client := NewClient(channelwire.Bootstrap{Origin: core.URL, CoreID: "core", Generation: "gen", Token: strings.Repeat("x", 64), Connection: channelwire.Connection{ApplicationID: "1"}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); runTelegramActivities(ctx, client, api) }()
	select {
	case <-notified:
	case <-time.After(3 * time.Second):
		t.Fatal("typing was not sent")
	}
	visible.Store(false)
	deadline := time.Now().Add(3 * time.Second)
	for polls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("activity loop survived cancellation")
	}
	if calls.Load() != 1 || polls.Load() < 2 {
		t.Fatal(calls.Load(), polls.Load())
	}
}
