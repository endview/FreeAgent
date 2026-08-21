package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestExplicitChannelServeEndToEndAndDuplicateDoesNotResend(t *testing.T) {
	const secret = "not-a-secret"
	var outboundCalls atomic.Int32
	outbound := httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			outboundCalls.Add(1)
			if request.Method != http.MethodPost ||
				request.URL.Path != "/channel/outbound" ||
				request.Header.Get("Authorization") != "Bearer "+secret {
				t.Errorf("unexpected outbound request %s %s headers=%v", request.Method, request.URL.Path, request.Header)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read outbound request: %v", err)
				return
			}
			var delivery struct {
				SchemaVersion string `json:"schema_version"`
				AttemptID     string `json:"attempt_id"`
				EndpointID    string `json:"endpoint_id"`
				Message       string `json:"message"`
			}
			if err := json.Unmarshal(body, &delivery); err != nil ||
				delivery.SchemaVersion != "loopback-channel-delivery/v1" ||
				delivery.AttemptID == "" ||
				delivery.EndpointID != "endpoint-loopback" ||
				delivery.Message != "hello over channel" {
				t.Errorf("invalid outbound delivery=%+v err=%v body=%s", delivery, err, body)
				return
			}
			wire, err := json.Marshal(map[string]any{
				"schema_version":        "loopback-channel-delivery-result/v1",
				"attempt_id":            delivery.AttemptID,
				"outcome":               "SUCCEEDED",
				"external_operation_id": "external-message-e2e-1",
			})
			if err != nil {
				t.Errorf("marshal outbound response: %v", err)
				return
			}
			canonical, err := moduleapi.CanonicalJSON(wire)
			if err != nil {
				t.Errorf("canonical outbound response: %v", err)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write(canonical)
		},
	))
	defer outbound.Close()

	fixture := newProductionChannelFixture(t, productionChannelFixtureOptions{
		enabled:     true,
		outboundURL: outbound.URL + "/channel/outbound",
	})
	credentialPath := filepath.Join(t.TempDir(), "channel.secret")
	if err := os.WriteFile(credentialPath, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	serveContext, stop := context.WithCancel(context.Background())
	stdoutReader, stdoutWriter := io.Pipe()
	serveDone := make(chan error, 1)
	go func() {
		err := run(
			serveContext,
			[]string{
				"serve",
				"--db", fixture.databasePath,
				"--artifact-root", fixture.artifactRoot,
				"--tenant", defaultTenantID,
				"--listen", "127.0.0.1:0",
				"--enable-channel",
				"--channel-workspace", defaultWorkspaceID,
				"--channel-endpoint", "endpoint-loopback",
				"--channel-secret-file", credentialPath,
			},
			stdoutWriter,
			io.Discard,
		)
		_ = stdoutWriter.CloseWithError(err)
		serveDone <- err
	}()
	defer func() {
		if serveDone == nil {
			return
		}
		stop()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("Channel serve shutdown: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("Channel serve did not shut down")
		}
	}()

	var ready map[string]string
	if err := json.NewDecoder(stdoutReader).Decode(&ready); err != nil {
		t.Fatalf("decode serve readiness: %v", err)
	}
	if ready["status"] != "ready" || ready["listen"] == "" ||
		ready["channel_endpoint"] != "endpoint-loopback" ||
		ready["channel_path"] != "/channel/inbound" {
		t.Fatalf("serve readiness=%v", ready)
	}

	inboundRaw, err := json.Marshal(map[string]any{
		"schema_version":    "loopback-channel-inbound/v1",
		"endpoint_id":       "endpoint-loopback",
		"provider_event_id": "provider-event-e2e-1",
		"external_user_id":  "external-user",
		"message":           "hello over channel",
		"reply_target":      map[string]any{"account_id": "account-loopback", "conversation_id": "conversation-loopback"},
		"cursor_before":     map[string]any{"offset": 1},
		"cursor_after":      map[string]any{"offset": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	inbound, err := moduleapi.CanonicalJSON(inboundRaw)
	if err != nil {
		t.Fatal(err)
	}
	wrongTargetRaw, err := json.Marshal(map[string]any{
		"schema_version":    "loopback-channel-inbound/v1",
		"endpoint_id":       "endpoint-loopback",
		"provider_event_id": "provider-event-wrong-target",
		"external_user_id":  "external-user",
		"message":           "must not cross conversations",
		"reply_target":      map[string]any{"account_id": "account-loopback", "conversation_id": "conversation-other"},
		"cursor_before":     map[string]any{"offset": 0},
		"cursor_after":      map[string]any{"offset": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongTarget, err := moduleapi.CanonicalJSON(wrongTargetRaw)
	if err != nil {
		t.Fatal(err)
	}
	wrongRequest, err := http.NewRequest(
		http.MethodPost,
		"http://"+ready["listen"]+ready["channel_path"],
		bytes.NewReader(wrongTarget),
	)
	if err != nil {
		t.Fatal(err)
	}
	wrongRequest.Header.Set("Content-Type", "application/json")
	wrongRequest.Header.Set("Authorization", "Bearer "+secret)
	wrongResponse, err := (&http.Client{Timeout: 15 * time.Second}).Do(wrongRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.Copy(io.Discard, wrongResponse.Body)
	closeErr := wrongResponse.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if wrongResponse.StatusCode != http.StatusBadRequest || outboundCalls.Load() != 0 {
		t.Fatalf(
			"wrong target status=%d outbound calls=%d",
			wrongResponse.StatusCode,
			outboundCalls.Load(),
		)
	}
	post := func(payload []byte) map[string]any {
		request, err := http.NewRequest(
			http.MethodPost,
			"http://"+ready["listen"]+ready["channel_path"],
			bytes.NewReader(payload),
		)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+secret)
		response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
		if err != nil {
			t.Fatalf("post inbound Channel request: %v", err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("inbound status=%d body=%s", response.StatusCode, body)
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode inbound response: %v body=%s", err, body)
		}
		return decoded
	}
	poisonRaw, err := json.Marshal(map[string]any{
		"schema_version":    "loopback-channel-inbound/v1",
		"endpoint_id":       "endpoint-loopback",
		"provider_event_id": "provider-event-poison",
		"external_user_id":  "unknown-user",
		"message":           "authenticated poison event",
		"reply_target":      map[string]any{"account_id": "account-loopback", "conversation_id": "conversation-loopback"},
		"cursor_before":     map[string]any{"offset": 0},
		"cursor_after":      map[string]any{"offset": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	poison, err := moduleapi.CanonicalJSON(poisonRaw)
	if err != nil {
		t.Fatal(err)
	}
	rejected := post(poison)
	if rejected["status"] != "rejected" || rejected["ingress_key"] == "" ||
		rejected["run_id"] != nil || outboundCalls.Load() != 0 {
		t.Fatalf(
			"poison response=%v outbound calls=%d",
			rejected,
			outboundCalls.Load(),
		)
	}

	first := post(inbound)
	if first["status"] != "accepted" || first["run_id"] == "" ||
		outboundCalls.Load() != 1 {
		t.Fatalf("first response=%v outbound calls=%d", first, outboundCalls.Load())
	}
	second := post(inbound)
	if second["status"] != "duplicate" ||
		second["run_id"] != first["run_id"] || outboundCalls.Load() != 1 {
		t.Fatalf("duplicate response=%v outbound calls=%d", second, outboundCalls.Load())
	}

	stop()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Channel serve: %v", err)
		}
		serveDone = nil
	case <-time.After(15 * time.Second):
		t.Fatal("Channel serve did not stop")
	}

	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(), fixture.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	terminal, err := store.GetTerminalRunResult(
		context.Background(), first["run_id"].(string),
	)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.ChannelState != currentstore.DispatchSucceeded ||
		terminal.Output.AssistantText != "hello over channel" {
		t.Fatalf("terminal=%+v", terminal)
	}
	cursor, err := store.GetCurrentChannelCursor(
		context.Background(),
		defaultTenantID,
		"endpoint-loopback",
		"cursor/endpoint-loopback",
	)
	if err != nil || cursor.CursorRevision != 2 {
		t.Fatalf("Cursor after rejected+accepted events=%+v err=%v", cursor, err)
	}
}
