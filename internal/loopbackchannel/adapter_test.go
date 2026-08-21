package loopbackchannel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const testSecret = "not-a-secret"

type testSecretResolver struct {
	secret []byte
	err    error
	calls  atomic.Int32
}

func newTestSecretResolver() *testSecretResolver {
	material := []byte(testSecret)
	return &testSecretResolver{secret: material}
}

func (resolver *testSecretResolver) ResolveSecret(
	context.Context,
	string,
) ([]byte, error) {
	resolver.calls.Add(1)
	if resolver.err != nil {
		return nil, resolver.err
	}
	return bytes.Clone(resolver.secret), nil
}

func TestNewRequiresLiteralLoopbackAndDoesNotResolveSecret(t *testing.T) {
	t.Parallel()
	invalid := []string{
		"http://localhost:8080/send",
		"http://192.0.2.1:8080/send",
		"https://127.0.0.1:8080/send",
		"http://127.0.0.1:8080/send?route=other",
		"http://" + "user@127.0.0.1:8080/send",
		"http://127.0.0.1:8080/a/../send",
	}
	for _, endpoint := range invalid {
		endpoint := endpoint
		t.Run(endpoint, func(t *testing.T) {
			resolver := newTestSecretResolver()
			if _, err := New(
				testProvider(),
				"endpoint-1",
				"account-1",
				"conversation-1",
				testConfig(t, endpoint, 1000),
				resolver,
			); err == nil {
				t.Fatal("New() accepted a non-strict loopback endpoint")
			}
			if got := resolver.calls.Load(); got != 0 {
				t.Fatalf("secret resolver calls during construction = %d, want 0", got)
			}
		})
	}

	resolver := newTestSecretResolver()
	adapter, err := New(
		testProvider(),
		"endpoint-1",
		"account-1",
		"conversation-1",
		testConfig(t, "http://127.0.0.1:8080/send", 1000),
		resolver,
	)
	if err != nil {
		t.Fatalf("New(valid loopback) error = %v", err)
	}
	transport, ok := adapter.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", adapter.client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("loopback transport configured a proxy callback")
	}
	if adapter.InboundPath() != "/inbound" {
		t.Fatalf("InboundPath() = %q, want /inbound", adapter.InboundPath())
	}
	if got := resolver.calls.Load(); got != 0 {
		t.Fatalf("secret resolver calls during construction = %d, want 0", got)
	}
}

func TestDecodeInboundAuthenticatesAndNormalizesBoundedWire(t *testing.T) {
	t.Parallel()
	resolver := newTestSecretResolver()
	adapter := testAdapter(t, "http://127.0.0.1:8080/send", 1000, resolver)
	body := testInboundBody(t, nil)
	request := testInboundRequest(body, testSecret)
	envelope, err := adapter.DecodeInbound(context.Background(), request)
	if err != nil {
		t.Fatalf("DecodeInbound() error = %v", err)
	}
	if envelope.EndpointID != "endpoint-1" || envelope.ProviderEventID != "event-1" ||
		envelope.ExternalUserID != "user-1" || envelope.Message != "hello" {
		t.Fatalf("DecodeInbound() = %+v", envelope)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("secret resolver calls = %d, want 1", got)
	}

	for name, mutate := range map[string]func(*http.Request){
		"wrong method":       func(request *http.Request) { request.Method = http.MethodGet },
		"remote public":      func(request *http.Request) { request.RemoteAddr = "192.0.2.1:1234" },
		"hostname authority": func(request *http.Request) { request.Host = "localhost:8080" },
		"forwarded":          func(request *http.Request) { request.Header.Set("X-Forwarded-For", "127.0.0.1") },
		"query":              func(request *http.Request) { request.URL.RawQuery = "route=other" },
		"encoded":            func(request *http.Request) { request.Header.Set("Content-Encoding", "gzip") },
	} {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			request := testInboundRequest(body, testSecret)
			mutate(request)
			if _, err := adapter.DecodeInbound(context.Background(), request); err == nil {
				t.Fatal("DecodeInbound() accepted forbidden HTTP metadata")
			}
		})
	}
}

func TestDecodeInboundRejectsMalformedOversizeAndNeverLeaksSecret(t *testing.T) {
	t.Parallel()
	resolver := &testSecretResolver{
		err: fmt.Errorf("resolver accidentally mentioned %s", testSecret),
	}
	adapter := testAdapter(t, "http://127.0.0.1:8080/send", 1000, resolver)
	_, err := adapter.DecodeInbound(
		context.Background(),
		testInboundRequest(testInboundBody(t, nil), testSecret),
	)
	if err == nil {
		t.Fatal("DecodeInbound() with resolver failure succeeded")
	}
	if strings.Contains(err.Error(), testSecret) {
		t.Fatalf("DecodeInbound() leaked secret in error: %v", err)
	}

	workingResolver := newTestSecretResolver()
	adapter = testAdapter(t, "http://127.0.0.1:8080/send", 1000, workingResolver)
	for name, body := range map[string][]byte{
		"malformed":     []byte(`{"schema_version":`),
		"unknown field": testInboundBody(t, map[string]any{"unexpected": true}),
		"oversize":      bytes.Repeat([]byte{'x'}, maximumInboundBytes+1),
	} {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			request := testInboundRequest(body, testSecret)
			if _, err := adapter.DecodeInbound(context.Background(), request); err == nil {
				t.Fatal("DecodeInbound() accepted malformed or oversize body")
			} else if strings.Contains(err.Error(), testSecret) {
				t.Fatalf("DecodeInbound() leaked secret: %v", err)
			}
		})
	}
}

func TestReplyTargetIsBoundAtInboundPrepareAndExecute(t *testing.T) {
	t.Parallel()

	for name, target := range map[string]any{
		"missing account": map[string]any{
			"conversation_id": "conversation-1",
		},
		"another conversation": map[string]any{
			"account_id": "account-1", "conversation_id": "conversation-other",
		},
		"another account": map[string]any{
			"account_id": "account-other", "conversation_id": "conversation-1",
		},
		"unknown field": map[string]any{
			"account_id": "account-1", "conversation_id": "conversation-1", "route": "other",
		},
	} {
		name, target := name, target
		t.Run("inbound "+name, func(t *testing.T) {
			resolver := newTestSecretResolver()
			adapter := testAdapter(
				t,
				"http://127.0.0.1:8080/send",
				1000,
				resolver,
			)
			body := testInboundBody(t, map[string]any{"reply_target": target})
			if _, err := adapter.DecodeInbound(
				context.Background(),
				testInboundRequest(body, testSecret),
			); err == nil {
				t.Fatal("DecodeInbound accepted a target outside the frozen Endpoint")
			}
		})
	}

	resolver := newTestSecretResolver()
	var outboundCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { outboundCalls.Add(1) },
	))
	defer server.Close()
	adapter := testAdapter(t, server.URL+"/send", 1000, resolver)
	if _, err := adapter.PrepareSend(
		context.Background(),
		moduleapi.ChannelPrepareSendRequestV1{
			SchemaVersion: moduleapi.ChannelPrepareSendRequestSchemaV1,
			EndpointID:    "endpoint-1",
			ReplyTarget:   json.RawMessage(`{"account_id":"account-1","conversation_id":"conversation-other"}`),
			AssistantText: "must not be prepared",
		},
	); !errors.Is(err, ErrPrepareSend) {
		t.Fatalf("PrepareSend target mismatch error=%v", err)
	}

	execution, _ := testExecutionRequest(t, adapter, "must not be sent")
	execution.ReplyTarget = json.RawMessage(
		`{"account_id":"account-other","conversation_id":"conversation-1"}`,
	)
	if err := execution.Validate(); err != nil {
		t.Fatal(err)
	}
	result, err := adapter.ExecutePrepared(context.Background(), execution)
	if err != nil || result.Outcome != moduleapi.ChannelExecutionFailed ||
		result.ErrorClassification != "CHANNEL_REPLY_TARGET_MISMATCH" {
		t.Fatalf("ExecutePrepared target mismatch result=%+v err=%v", result, err)
	}
	if got := outboundCalls.Load(); got != 0 {
		t.Fatalf("target mismatch reached outbound HTTP %d times", got)
	}
	if got := resolver.calls.Load(); got != 0 {
		t.Fatalf("target mismatch resolved secret %d times", got)
	}
}

func TestPrepareSendIsPureAndExecutePostsExactlyOnceWithoutSecretLeak(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/send" {
			t.Errorf("request = %s %s, want POST /send", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+testSecret {
			t.Errorf("Authorization = %q", got)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		requestBody = bytes.Clone(body)
		response := deliveryResponseWireV1{
			SchemaVersion:       deliveryResponseWireSchemaV1,
			AttemptID:           "attempt-1",
			Outcome:             string(moduleapi.ChannelExecutionSucceeded),
			ExternalOperationID: "message-1",
		}
		canonical, err := canonicalWire(response, maximumResponseBytes)
		if err != nil {
			t.Errorf("canonical response: %v", err)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(canonical)
	}))
	defer server.Close()

	resolver := newTestSecretResolver()
	adapter := testAdapter(t, server.URL+"/send", 1000, resolver)
	request, prepared := testExecutionRequest(t, adapter, "hello from assistant")
	if got := resolver.calls.Load(); got != 0 {
		t.Fatalf("PrepareSend resolved secret %d times, want 0", got)
	}
	if strings.Contains(string(prepared), testSecret) {
		t.Fatal("prepared payload leaked secret")
	}
	result, err := adapter.ExecutePrepared(context.Background(), request)
	if err != nil {
		t.Fatalf("ExecutePrepared() error = %v", err)
	}
	if result.Outcome != moduleapi.ChannelExecutionSucceeded || result.ExternalOperationID != "message-1" {
		t.Fatalf("ExecutePrepared() result = %+v", result)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("HTTP request count = %d, want exactly 1", got)
	}
	if got := resolver.calls.Load(); got != 1 {
		t.Fatalf("secret resolver calls = %d, want 1", got)
	}
	if strings.Contains(string(requestBody), testSecret) {
		t.Fatal("outbound request body leaked secret")
	}
	resultJSON, _ := json.Marshal(result)
	if strings.Contains(string(resultJSON), testSecret) {
		t.Fatal("execution result leaked secret")
	}
}

func TestExecutePreparedNeverFollowsRedirect(t *testing.T) {
	t.Parallel()
	var originCalls atomic.Int32
	var redirectedCalls atomic.Int32
	redirected := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectedCalls.Add(1)
	}))
	defer redirected.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		originCalls.Add(1)
		writer.Header().Set("Location", redirected.URL+"/must-not-run")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	adapter := testAdapter(
		t,
		origin.URL+"/send",
		1000,
		newTestSecretResolver(),
	)
	request, _ := testExecutionRequest(t, adapter, "redirect test")
	result, err := adapter.ExecutePrepared(context.Background(), request)
	if err != nil {
		t.Fatalf("ExecutePrepared() error = %v", err)
	}
	if result.Outcome != moduleapi.ChannelExecutionUnknown {
		t.Fatalf("redirect outcome = %s, want UNKNOWN", result.Outcome)
	}
	if got := originCalls.Load(); got != 1 {
		t.Fatalf("origin calls = %d, want 1", got)
	}
	if got := redirectedCalls.Load(); got != 0 {
		t.Fatalf("redirect target calls = %d, want 0", got)
	}
}

func TestExecutePreparedAmbiguousHTTPFailuresAreUnknownAndNeverRetried(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		timeoutMS uint32
		handler   http.HandlerFunc
	}{
		"timeout": {
			timeoutMS: 25,
			handler: func(_ http.ResponseWriter, _ *http.Request) {
				time.Sleep(100 * time.Millisecond)
			},
		},
		"eof-reset": {
			timeoutMS: 1000,
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				hijacker, ok := writer.(http.Hijacker)
				if !ok {
					t.Error("ResponseWriter does not implement Hijacker")
					return
				}
				connection, _, err := hijacker.Hijack()
				if err != nil {
					t.Errorf("Hijack(): %v", err)
					return
				}
				_ = connection.Close()
			},
		},
		"malformed": {
			timeoutMS: 1000,
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{"detail":"` + testSecret + `"`))
			},
		},
		"invalid-success-wire": {
			timeoutMS: 1000,
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				canonical, err := canonicalWire(deliveryResponseWireV1{
					SchemaVersion: deliveryResponseWireSchemaV1,
					AttemptID:     "attempt-1",
					Outcome:       string(moduleapi.ChannelExecutionSucceeded),
				}, maximumResponseBytes)
				if err != nil {
					t.Errorf("canonical invalid response: %v", err)
					return
				}
				_, _ = writer.Write(canonical)
			},
		},
		"oversize": {
			timeoutMS: 1000,
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("Content-Length", fmt.Sprint(maximumResponseBytes+1))
				writer.WriteHeader(http.StatusOK)
			},
		},
	}
	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				test.handler(writer, request)
			}))
			defer server.Close()
			adapter := testAdapter(
				t,
				server.URL+"/send",
				test.timeoutMS,
				newTestSecretResolver(),
			)
			request, _ := testExecutionRequest(t, adapter, "ambiguous test")
			result, err := adapter.ExecutePrepared(context.Background(), request)
			if err != nil {
				t.Fatalf("ExecutePrepared() error = %v", err)
			}
			if result.Outcome != moduleapi.ChannelExecutionUnknown {
				t.Fatalf("outcome = %s, want UNKNOWN", result.Outcome)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("HTTP request count = %d, want exactly 1", got)
			}
			resultJSON, _ := json.Marshal(result)
			if strings.Contains(string(resultJSON), testSecret) {
				t.Fatal("UNKNOWN result leaked secret")
			}
		})
	}
}

func testAdapter(
	t *testing.T,
	outboundURL string,
	timeoutMS uint32,
	resolver SecretResolver,
) *Adapter {
	t.Helper()
	adapter, err := New(
		testProvider(),
		"endpoint-1",
		"account-1",
		"conversation-1",
		testConfig(t, outboundURL, timeoutMS),
		resolver,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return adapter
}

func testProvider() moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           "freeagent.builtin.channel.loopback-http",
		Version:            "1.0.0",
		ArtifactDigest:     strings.Repeat("a", moduleapi.SHA256HexLength),
		InstanceID:         "loopback-channel-1",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    AdapterIdentityV1,
		ActivationRevision: 1,
	}
}

func testConfig(t *testing.T, outboundURL string, timeoutMS uint32) moduleapi.ChannelBindingConfigV1 {
	t.Helper()
	parameters, err := canonicalWire(parametersV1{
		SchemaVersion:    parametersSchemaV1,
		InboundPath:      "/inbound",
		OutboundURL:      outboundURL,
		RequestTimeoutMS: timeoutMS,
	}, moduleapi.MaxConfigBytes)
	if err != nil {
		t.Fatalf("canonical parameters: %v", err)
	}
	return moduleapi.ChannelBindingConfigV1{
		SchemaVersion:   moduleapi.ChannelBindingConfigSchemaV1,
		AdapterProtocol: AdapterProtocolV1,
		SecretRef:       "placeholder",
		Parameters:      parameters,
	}
}

func testInboundBody(t *testing.T, extras map[string]any) []byte {
	t.Helper()
	value := map[string]any{
		"schema_version":    inboundWireSchemaV1,
		"endpoint_id":       "endpoint-1",
		"provider_event_id": "event-1",
		"external_user_id":  "user-1",
		"message":           "hello",
		"reply_target":      map[string]any{"account_id": "account-1", "conversation_id": "conversation-1"},
		"cursor_before":     map[string]any{"offset": 1},
		"cursor_after":      map[string]any{"offset": 2},
	}
	for key, extra := range extras {
		value[key] = extra
	}
	canonical, err := canonicalWire(value, maximumInboundBytes)
	if err != nil {
		t.Fatalf("canonical inbound wire: %v", err)
	}
	return canonical
}

func testInboundRequest(body []byte, secret string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/inbound", bytes.NewReader(body))
	request.Host = "127.0.0.1:8080"
	request.URL.Scheme = ""
	request.URL.Host = ""
	request.RequestURI = "/inbound"
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+secret)
	return request
}

func testExecutionRequest(
	t *testing.T,
	adapter *Adapter,
	message string,
) (moduleapi.ChannelExecutionRequestV1, json.RawMessage) {
	t.Helper()
	replyTarget := json.RawMessage(`{"account_id":"account-1","conversation_id":"conversation-1"}`)
	prepared, err := adapter.PrepareSend(
		context.Background(),
		moduleapi.ChannelPrepareSendRequestV1{
			SchemaVersion: moduleapi.ChannelPrepareSendRequestSchemaV1,
			EndpointID:    "endpoint-1",
			ReplyTarget:   replyTarget,
			AssistantText: message,
		},
	)
	if err != nil {
		t.Fatalf("PrepareSend() error = %v", err)
	}
	digest, err := moduleapi.ChannelAssistantTextDigestV1(message)
	if err != nil {
		t.Fatalf("ChannelAssistantTextDigestV1() error = %v", err)
	}
	request := moduleapi.ChannelExecutionRequestV1{
		SchemaVersion:       moduleapi.ChannelExecutionRequestSchemaV1,
		AttemptID:           "attempt-1",
		EndpointID:          "endpoint-1",
		IngressKey:          strings.Repeat("b", moduleapi.SHA256HexLength),
		ProposalDigest:      strings.Repeat("c", moduleapi.SHA256HexLength),
		AssistantTextDigest: digest,
		ReplyTarget:         replyTarget,
		PreparedPayload:     prepared,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("execution request Validate() error = %v", err)
	}
	return request, prepared
}
