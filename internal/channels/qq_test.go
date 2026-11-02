package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestQQWebhookVerifiesSignatureBodyTimestampAndValidation(t *testing.T) {
	now := time.Now()
	timestamp := fmt.Sprint(now.Unix())
	secret := "QQ_FIXTURE_SECRET"
	var calls atomic.Int32
	handler := QQWebhook{Secret: secret, Now: func() time.Time { return now }, Handle: func(context.Context, []byte) error { calls.Add(1); return nil }}
	body := []byte(`{"op":0,"t":"C2C_MESSAGE_CREATE","d":{}}`)
	send := func(raw []byte, ts, sig string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1/qq/events", bytes.NewReader(raw))
		r.Header.Set("X-Signature-Timestamp", ts)
		r.Header.Set("X-Signature-Ed25519", sig)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := send(body, timestamp, QQSignature(secret, timestamp, body)); w.Code != 200 || !strings.Contains(w.Body.String(), `"op":12`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(append(body, ' '), timestamp, QQSignature(secret, timestamp, body)); w.Code != 401 {
		t.Fatal("tampered callback accepted")
	}
	old := fmt.Sprint(now.Add(-6 * time.Minute).Unix())
	if w := send(body, old, QQSignature(secret, old, body)); w.Code != 401 {
		t.Fatal("stale callback accepted")
	}
	challenge := []byte(`{"op":13,"d":{"plain_token":"hello","event_ts":"123"}}`)
	w := send(challenge, timestamp, QQSignature(secret, timestamp, challenge))
	var answer map[string]string
	if json.Unmarshal(w.Body.Bytes(), &answer) != nil || answer["signature"] != QQSignature(secret, "123", []byte("hello")) {
		t.Fatal(w.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}
func TestQQAppAndIdentityScopesAndUnsupportedTypes(t *testing.T) {
	raw := []byte(`{"t":"GROUP_AT_MESSAGE_CREATE","d":{"id":"msg","group_openid":"g1","content":"hello","author":{"member_openid":"member"}}}`)
	first, ok := DecodeQQEvent(raw, "app1")
	second, ok2 := DecodeQQEvent(raw, "app2")
	third, _ := DecodeQQEvent(bytes.ReplaceAll(raw, []byte("g1"), []byte("g2")), "app1")
	if !ok || !ok2 || first.Person == second.Person || first.Person == third.Person || first.Chat != "group:g1" {
		t.Fatalf("%+v %+v %+v", first, second, third)
	}
	for _, kind := range []string{"MESSAGE_CREATE", "AT_MESSAGE_CREATE", "GROUP_MESSAGE_CREATE"} {
		if _, ok := DecodeQQEvent(bytes.ReplaceAll(raw, []byte("GROUP_AT_MESSAGE_CREATE"), []byte(kind)), "app1"); ok {
			t.Fatal("unsupported channel type accepted", kind)
		}
	}
}
func TestQQPassiveReplyTokenAndNoReplay(t *testing.T) {
	var sends atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			w.Write([]byte(`{"access_token":"FIXTURE_TOKEN","expires_in":"7200"}`))
		case "/v2/groups/g/messages":
			sends.Add(1)
			if r.Header.Get("Authorization") != "QQBot FIXTURE_TOKEN" {
				t.Error("wrong authorization")
			}
			w.WriteHeader(403)
			w.Write([]byte(`{"code":304023,"message":"QQ_FIXTURE_SECRET"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	api, err := NewQQAPI(server.URL, server.URL, "app", "QQ_FIXTURE_SECRET", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.Send(t.Context(), "group", "g", "msg", "result", 1)
	if err == nil || strings.Contains(err.Error(), "QQ_FIXTURE_SECRET") || sends.Load() != 1 {
		t.Fatal(err, sends.Load())
	}
	if _, err := api.Send(t.Context(), "group", "g", "", "result", 2); err == nil {
		t.Fatal("unbound active send accepted")
	}
}
