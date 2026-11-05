package harness

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type firstLiveResponseInterrupted struct {
	base   http.RoundTripper
	calls  atomic.Int32
	opened atomic.Bool
}

func (t *firstLiveResponseInterrupted) RoundTrip(r *http.Request) (*http.Response, error) {
	n := t.calls.Add(1)
	response, err := t.base.RoundTrip(r)
	if n == 1 && err == nil && response.StatusCode == 200 {
		t.opened.Store(true)
		response.Body = &liveFaultBody{ReadCloser: response.Body, mode: "disconnect", ctx: r.Context()}
	}
	return response, err
}

func TestLiveProviderModelRecoveryRetainsOriginalUnknown(t *testing.T) {
	config, keys, _ := liveProviderConfig(t)
	config.Capabilities.Streaming = "supported"
	config.Capabilities.Usage = "required"
	base := http.DefaultTransport.(*http.Transport).Clone()
	defer base.CloseIdleConnections()
	fault := &firstLiveResponseInterrupted{base: base}
	client := keyedClient(t, config, keys, &http.Client{Transport: fault})
	core := recoveryFixture(t, client)
	core.Keys = keys
	core.Config.Models[core.Config.DefaultModel] = config
	enableTestProviders(&core.Config)
	core.Config.MaxOutputTokens = 1024
	if err := WriteConfig(core.Store.DataDir(), core.Config); err != nil {
		t.Fatal(err)
	}
	result, err := core.Run(t.Context(), conversation(t, core), "live-recovery", "Reply exactly HARNESS_RECOVERY_OK. Do not call tools.")
	if err != nil || result.Run.Status != "completed" || !strings.Contains(result.Answer, "HARNESS_RECOVERY_OK") || fault.calls.Load() != 2 || !fault.opened.Load() {
		t.Fatalf("recovery not accepted: status=%s http_calls=%d real_200=%v error=%v", result.Run.Status, fault.calls.Load(), fault.opened.Load(), err)
	}
	attempts, e := core.Store.ListAttempts(context.Background(), result.Run.ID)
	if e != nil || len(attempts) != 2 || attempts[0].Status != "unknown" || attempts[0].Replacement != attempts[1].ID || attempts[1].Status != "finished" {
		t.Fatalf("recovery facts not retained; count=%d error=%v", len(attempts), e)
	}
	t.Log("real HTTP 200 interrupted; one replacement completed, original UNKNOWN retained; no tool dispatched")
}
