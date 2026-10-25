package harness

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestAllProviderProtocolsCancelBeforeAndAfterHeaders(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic"} {
		for _, headers := range []bool{false, true} {
			t.Run(protocol+map[bool]string{false: "/before-headers", true: "/after-headers"}[headers], func(t *testing.T) {
				var calls atomic.Int32
				handler := func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					if headers {
						w.Header().Set("Content-Type", "text/event-stream")
						w.WriteHeader(200)
						w.(http.Flusher).Flush()
					}
					<-r.Context().Done()
				}
				var client ModelClient
				if protocol == "anthropic" {
					value, server := newAnthropicFixture(t, handler)
					t.Cleanup(server.Close)
					client = value
				} else if protocol == "responses" {
					value, server := newResponsesFixture(t, ModelCapabilities{Protocol: "responses"}, handler)
					t.Cleanup(server.Close)
					client = value
				} else {
					value, _, _ := contractClient(t, ModelCapabilities{Protocol: protocol}, handler)
					client = value
				}
				cancelled, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := client.Generate(cancelled, contractRequest(), nil); err == nil || calls.Load() != 0 {
					t.Fatal("pre-cancel dispatched")
				}
				ctx, stop := context.WithTimeout(t.Context(), 150*time.Millisecond)
				defer stop()
				response, err := client.Generate(ctx, contractRequest(), nil)
				var failure *CallError
				if !errors.As(err, &failure) || failure.Certain || len(response.ToolCalls) != 0 || calls.Load() != 1 {
					t.Fatalf("interruption claimed safe completion %+v %v calls=%d", response, err, calls.Load())
				}
			})
		}
	}
}
