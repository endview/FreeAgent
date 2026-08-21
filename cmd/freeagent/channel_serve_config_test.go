package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeRequiresExplicitCompleteChannelEnablement(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "option without switch",
			args: []string{"serve", "--db", "missing.sqlite", "--channel-endpoint", "endpoint"},
			want: "require explicit --enable-channel",
		},
		{
			name: "incomplete explicit switch",
			args: []string{"serve", "--db", "missing.sqlite", "--enable-channel"},
			want: "requires --channel-workspace",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := run(
				context.Background(), test.args, &bytes.Buffer{}, &bytes.Buffer{},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestExactChannelHTTPMuxNeverCleansOrRedirectsPaths(t *testing.T) {
	channelCalls := 0
	fallbackCalls := 0
	mux := &exactChannelHTTPMux{
		path: "/channel/inbound",
		channel: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			channelCalls++
			response.WriteHeader(http.StatusNoContent)
		}),
		fallback: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			fallbackCalls++
			response.WriteHeader(http.StatusNotFound)
		}),
	}
	for _, path := range []string{
		"/channel//inbound",
		"/channel/../channel/inbound",
		"/channel/inbound/",
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path, nil))
		if response.Code != http.StatusNotFound || response.Header().Get("Location") != "" {
			t.Fatalf("path=%q status=%d headers=%v", path, response.Code, response.Header())
		}
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "http://127.0.0.1/channel/inbound", nil),
	)
	if response.Code != http.StatusNoContent || channelCalls != 1 || fallbackCalls != 3 {
		t.Fatalf("status=%d channel=%d fallback=%d", response.Code, channelCalls, fallbackCalls)
	}
}
