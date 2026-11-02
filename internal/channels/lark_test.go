package channels

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLarkWireBoundsMultipartAndTenantIdentity(t *testing.T) {
	first := LarkFrame{Seq: 5, Log: 9, Service: 7, Method: 1, Headers: map[string]string{"type": "event", "message_id": "m1", "sum": "2", "seq": "0"}, Payload: []byte("one")}
	f, err := DecodeLarkFrame(first.Marshal())
	if err != nil || f.Seq != 5 || f.Headers["message_id"] != "m1" {
		t.Fatalf("frame=%+v err=%v", f, err)
	}
	a := NewLarkAssembler()
	if _, ok, err := a.Add(f); err != nil || ok {
		t.Fatal(ok, err)
	}
	f.Headers["seq"] = "1"
	f.Payload = []byte("two")
	got, ok, err := a.Add(f)
	if err != nil || !ok || string(got) != "onetwo" {
		t.Fatal(string(got), ok, err)
	}
	for _, raw := range [][]byte{{0x80}, {0, 0}, {0x42, 0xff, 0xff, 0x7f}, make([]byte, (1<<20)+1)} {
		if _, err := DecodeLarkFrame(raw); err == nil {
			t.Fatal("malformed frame accepted")
		}
	}
	first.Headers["sum"] = "2147483647"
	if _, _, err := a.Add(first); err == nil {
		t.Fatal("unbounded fragments accepted")
	}
	raw := func(tenant string) []byte {
		return []byte(`{"header":{"app_id":"app","tenant_key":"` + tenant + `","event_type":"im.message.receive_v1"},"event":{"sender":{"sender_type":"user","sender_id":{"open_id":"ou_1"}},"message":{"message_id":"m1","chat_id":"chat","chat_type":"group","message_type":"text","content":"{\"text\":\"@_user_1 hello\"}","mentions":[{"key":"@_user_1","id":{"open_id":"bot"}}]}}}`)
	}
	e1, ok := DecodeLarkEvent(raw("tenant1"), "app", "bot")
	e2, ok2 := DecodeLarkEvent(raw("tenant2"), "app", "bot")
	if !ok || !ok2 || !e1.Trigger || e1.Person == e2.Person {
		t.Fatalf("events=%+v %+v", e1, e2)
	}
	if _, ok := DecodeLarkEvent(raw("tenant1"), "other-app", "bot"); ok {
		t.Fatal("wrong app accepted")
	}
}
func TestLarkOfficialWireConnectionAcknowledgesAndCancels(t *testing.T) {
	var received atomic.Int32
	ack := make(chan LarkFrame, 1)
	var origin string
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/callback/ws/endpoint":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"URL": strings.Replace(origin, "http://", "ws://", 1) + "/ws?service_id=7", "ClientConfig": map[string]int{"PingInterval": 5}}})
		case "/ws":
			c, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			f := LarkFrame{Service: 7, Method: 1, Headers: map[string]string{"type": "event", "message_id": "wire"}, Payload: []byte(`{"fixture":true}`)}
			c.WriteMessage(websocket.BinaryMessage, f.Marshal())
			_, raw, err := c.ReadMessage()
			if err == nil {
				frame, e := DecodeLarkFrame(raw)
				if e == nil {
					ack <- frame
				}
			}
			for {
				if _, _, err := c.ReadMessage(); err != nil {
					return
				}
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	origin = server.URL
	api, err := NewLarkAPI(server.URL, "app", "FIXTURE_SECRET", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- api.Listen(ctx, func(raw []byte) (any, error) { received.Add(1); return map[string]string{"status": "ok"}, nil })
	}()
	select {
	case f := <-ack:
		if f.Service != 7 || !strings.Contains(string(f.Payload), `"code":200`) {
			t.Fatalf("ack=%+v", f)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no ack")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("websocket ignored cancellation")
	}
	if received.Load() != 1 {
		t.Fatal(received.Load())
	}
}
func TestLarkSendCachesTokenDoesNotLeakSecretOrRetry(t *testing.T) {
	var tokens, sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			tokens.Add(1)
			w.Write([]byte(`{"code":0,"tenant_access_token":"FIXTURE_TOKEN","expire":7200}`))
			return
		}
		sends.Add(1)
		if r.Header.Get("Authorization") != "Bearer FIXTURE_TOKEN" {
			t.Error("wrong authorization")
		}
		w.WriteHeader(500)
		w.Write([]byte(`{"code":1,"msg":"FIXTURE_SECRET"}`))
	}))
	defer server.Close()
	api, _ := NewLarkAPI(server.URL, "app", "FIXTURE_SECRET", server.Client())
	for range 2 {
		_, err := api.Send(t.Context(), "chat", "", "hello", nil)
		if err == nil || strings.Contains(err.Error(), "FIXTURE_SECRET") {
			t.Fatal(err)
		}
	}
	if tokens.Load() != 1 || sends.Load() != 2 {
		t.Fatal(tokens.Load(), sends.Load())
	}
}
