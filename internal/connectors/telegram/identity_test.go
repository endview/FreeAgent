package telegram

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
)

type probeTransportFunc func(*http.Request) (*http.Response, error)

func (f probeTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProbeBotReturnsSettingsIdentity(t *testing.T) {
	const token = "4500000123:fixture_only"
	calls := 0
	identity, err := ProbeBot(t.Context(), token, probeTransportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.String() != APIOrigin+"/bot"+token+"/getMe" {
			t.Fatal("identity check did not use only Telegram getMe")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
			`{"ok":true,"result":{"id":4500000123,"is_bot":true,"username":"fixture_bot","first_name":"Fixture"}}`,
		))}, nil
	}))
	if err != nil || calls != 1 {
		t.Fatalf("valid Bot identity check: calls=%d err=%v", calls, err)
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"application_id": "4500000123", "username": "fixture_bot"}
	var fields map[string]string
	if err := json.Unmarshal(raw, &fields); err != nil || !maps.Equal(fields, want) {
		t.Fatalf("settings response = %s, want only application_id and username", raw)
	}
}

func TestProbeBotRejectsUnverifiedIdentity(t *testing.T) {
	const token = "7:fixture_only"
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"not a bot", http.StatusOK, `{"ok":true,"result":{"id":7,"is_bot":false}}`},
		{"wrong identity", http.StatusOK, `{"ok":true,"result":{"id":8,"is_bot":true}}`},
		{"unauthorized", http.StatusUnauthorized, `{"ok":false}`},
		{"redirect", http.StatusFound, ""},
		{"transport error", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			identity, err := ProbeBot(t.Context(), token, probeTransportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if tc.status == 0 {
					return nil, errors.New(r.URL.String())
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {"https://redirect.example.test"}},
					Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}))
			if err == nil || strings.Contains(err.Error(), token) || identity != (BotIdentity{}) || calls != 1 {
				t.Fatal("unverified probe must fail without leaking its token or making another request")
			}
		})
	}
}
