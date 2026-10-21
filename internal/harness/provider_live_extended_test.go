package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveProviderChatJSONAndSSE(t *testing.T) {
	config, keys, _ := liveProviderConfig(t)
	for _, streaming := range []string{"none", "supported"} {
		t.Run(streaming, func(t *testing.T) {
			selected := config
			selected.Capabilities.Streaming = streaming
			selected.Capabilities.Usage = "required"
			client := keyedClient(t, selected, keys, nil)
			var text strings.Builder
			response, err := client.Generate(t.Context(), ModelRequest{
				Messages:        []ModelMessage{{Role: "user", Content: "Reply exactly HARNESS_LIVE_CHAT_OK. No other text."}},
				MaxOutputTokens: 1024,
			}, func(delta string) { text.WriteString(delta) })
			if err != nil || !strings.Contains(response.Content, "HARNESS_LIVE_CHAT_OK") ||
				text.String() != response.Content || len(response.ToolCalls) != 0 ||
				response.Provider == nil || response.Provider.ID == "" ||
				response.Provider.Streamed != (streaming == "supported") ||
				response.Usage == nil || response.Usage.TotalTokens <= 0 {
				t.Fatalf("live chat/stream/usage contract not accepted: stream=%s error=%v", streaming, err)
			}
			t.Logf("live mode=%s finish=%s input_tokens=%d output_tokens=%d", streaming,
				response.Provider.FinishReason, response.Usage.InputTokens, response.Usage.OutputTokens)
		})
	}
}

func TestLiveProviderToolErrorAndApprovalDecline(t *testing.T) {
	config, keys, _ := liveProviderConfig(t)
	core, _, workspace := testHarness(t, keyedClient(t, config, keys, nil))
	core.Keys = keys
	core.Config.Models[core.Config.DefaultModel] = config
	id := conversation(t, core)
	result, err := core.Run(t.Context(), id, "missing-file",
		"Use file_read to read deliberately-missing.txt. It deliberately does not exist; still call file_read once to observe the actual error. Then reply HARNESS_LIVE_TOOL_ERROR_OBSERVED. Do not create it.")
	if err != nil || result.Run.Status != "completed" {
		t.Fatalf("live tool-error continuation not accepted: status=%s error=%v", result.Run.Status, err)
	}
	found := false
	for _, attempt := range acceptanceAttempts(t, t.Context(), core, result.Run.ID) {
		if attempt.Kind != "tool" || attempt.Status != "finished" {
			continue
		}
		var call ToolCall
		var saved ToolResult
		if json.Unmarshal([]byte(attempt.RequestJSON), &call) == nil &&
			json.Unmarshal([]byte(attempt.ResponseJSON), &saved) == nil &&
			call.Name == "file_read" && saved.IsError {
			found = true
		}
	}
	if !found {
		t.Fatal("live model did not use the missing-file tool; no actual error evidence")
	}
	file := filepath.Join(workspace, "acceptance.txt")
	if err := os.WriteFile(file, []byte(acceptanceBefore), 0o600); err != nil {
		t.Fatal(err)
	}
	pending, err := core.Run(t.Context(), id, "decline-edit", acceptanceEdit)
	if err != nil || pending.Run.Status != "waiting_confirmation" {
		t.Fatalf("live decline preview not accepted: status=%s error=%v", pending.Run.Status, err)
	}
	declines := 0
	core.Approve = func(context.Context, ToolCall, PreparedTool) (bool, error) {
		declines++
		return false, nil
	}
	continued, err := core.Continue(t.Context(), pending.Run.ID)
	if err != nil || continued.Run.Status != "completed" || declines != 1 {
		t.Fatalf("live decline continuation not accepted: status=%s declines=%d error=%v",
			continued.Run.Status, declines, err)
	}
	requireAcceptanceFile(t, file, acceptanceBefore)
	t.Log("actual tool error observed; exact approval rejected and file unchanged")
}

type liveFaultTransport struct {
	base   http.RoundTripper
	mode   string
	cancel context.CancelFunc
	calls  atomic.Int32
	opened atomic.Bool
}

func (transport *liveFaultTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	response, err := transport.base.RoundTrip(request)
	if err == nil && response.StatusCode == http.StatusOK {
		transport.opened.Store(true)
		response.Body = &liveFaultBody{ReadCloser: response.Body, mode: transport.mode,
			ctx: request.Context(), cancel: transport.cancel}
	}
	return response, err
}

type liveFaultBody struct {
	io.ReadCloser
	mode   string
	ctx    context.Context
	cancel context.CancelFunc
	read   bool
}

func (body *liveFaultBody) Read(buffer []byte) (int, error) {
	if body.mode == "timeout" {
		<-body.ctx.Done()
		return 0, body.ctx.Err()
	}
	if body.read {
		return 0, io.ErrUnexpectedEOF
	}
	body.read = true
	// Interrupt a real response before any complete SSE event can authorize tools.
	n, err := body.ReadCloser.Read(buffer[:min(len(buffer), 32)])
	if n > 0 && body.mode == "cancel" {
		body.cancel()
	}
	return n, err
}

func TestLiveProviderControlledInterruptionsRetainUNKNOWN(t *testing.T) {
	config, keys, _ := liveProviderConfig(t)
	for _, mode := range []string{"disconnect", "cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			base := http.DefaultTransport.(*http.Transport).Clone()
			defer base.CloseIdleConnections()
			transport := &liveFaultTransport{base: base, mode: mode, cancel: cancel}
			client := keyedClient(t, config, keys, &http.Client{
				Transport: transport, Timeout: 8 * time.Second,
			})
			core, _, workspace := testHarness(t, client)
			core.Keys = keys
			core.Config.Models[core.Config.DefaultModel] = config
			id := conversation(t, core)
			result, err := core.Run(ctx, id, "interrupted",
				"Use file_create to create never-applied.txt containing HARNESS_LIVE_EFFECT. Then report success only after the tool result.")
			if !errors.Is(err, ErrUnknownResult) || result.Run.Status != "unknown" ||
				!transport.opened.Load() || transport.calls.Load() != 1 {
				t.Fatalf("live controlled interruption not accepted: mode=%s status=%s opened=%t calls=%d error=%v",
					mode, result.Run.Status, transport.opened.Load(), transport.calls.Load(), err)
			}
			if _, err := os.Stat(filepath.Join(workspace, "never-applied.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("interrupted live response authorized a tool")
			}
			reopenHarness(t, core)
			repeated, err := core.Run(t.Context(), id, "interrupted", result.Run.Prompt)
			if err != nil || !repeated.Duplicate || repeated.Run.Status != "unknown" ||
				transport.calls.Load() != 1 {
				t.Fatal("UNKNOWN was replayed after reopen")
			}
			if _, err := core.Continue(t.Context(), result.Run.ID); !errors.Is(err, ErrUnknownResult) ||
				transport.calls.Load() != 1 {
				t.Fatal("explicit continuation replayed UNKNOWN")
			}
			t.Logf("real HTTP 200 observed; controlled client %s retained UNKNOWN without tools or replay", mode)
		})
	}
}
