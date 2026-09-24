package zhipumodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	testModelBuild = "glm-4.5/public-alias-observed-2026-09-22"
	testAPIKey     = "not-a-secret"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type testAPIKeyResolver struct {
	key      []byte
	err      error
	identity APIKeyIdentity
}

func (resolver *testAPIKeyResolver) ResolveAPIKey(_ context.Context, identity APIKeyIdentity) ([]byte, error) {
	resolver.identity = identity
	return append([]byte(nil), resolver.key...), resolver.err
}

func TestAdapterSuccessNormalizesZhipuResponse(t *testing.T) {
	prepared := testPreparedInvocation(t, json.RawMessage(`{"max_tokens":128,"temperature":0,"thinking":{"type":"disabled"}}`), json.RawMessage(`{"max_tokens":128,"temperature":0,"thinking":{"type":"disabled"}}`))
	resolver := &testAPIKeyResolver{key: []byte(testAPIKey)}
	var requestBody []byte
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != officialChatCompletionsURL {
			t.Fatalf("unexpected request target: %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Fatal("missing bearer credential")
		}
		requestBody, _ = io.ReadAll(request.Body)
		return jsonResponse(request, http.StatusOK, successfulResponseJSON()), nil
	})}
	adapter := testAdapter(t, resolver, client)
	result, err := adapter.Invoke(context.Background(), prepared)
	if err != nil || result.Outcome != modulehost.InvocationSucceeded {
		t.Fatalf("Invoke result=%+v error=%v", result, err)
	}
	if string(requestBody) != `{"max_tokens":128,"messages":[{"content":"Answer concisely.","role":"user"}],"model":"glm-4.5","stream":false,"temperature":0,"thinking":{"type":"disabled"}}` {
		t.Fatalf("request body=%s", requestBody)
	}
	if resolver.identity.Provider != ProviderNameV1 || resolver.identity.Model != ModelGLM45 || resolver.identity.ModelBuildID != testModelBuild {
		t.Fatalf("resolver identity=%+v", resolver.identity)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(result.Output)
	if err != nil || output.AssistantText != "FREEAGENT_P3_OK" || output.ProviderRequestID != "zhipu-request-1" {
		t.Fatalf("output=%+v err=%v", output, err)
	}
	usage, err := moduleapi.RestoreModelUsageReceiptV2(result.UsageReceipt)
	if err != nil {
		t.Fatalf("usage=%v", err)
	}
	assertUint64Pointer(t, usage.InputTokens, 14)
	assertUint64Pointer(t, usage.OutputTokens, 6)
	assertUint64Pointer(t, usage.CachedInputTokens, 0)
	for _, forbidden := range []string{testAPIKey, "private reasoning"} {
		if bytes.Contains(result.Output, []byte(forbidden)) || bytes.Contains(result.UsageReceipt, []byte(forbidden)) {
			t.Fatalf("persisted result contains %q", forbidden)
		}
	}
}

func TestAdapterExplicitFailureAndAmbiguousTransport(t *testing.T) {
	prepared := testPreparedInvocation(t, json.RawMessage(`{}`), json.RawMessage(`{}`))
	for _, test := range []struct {
		name      string
		transport roundTripFunc
		outcome   modulehost.InvocationOutcome
		unknown   modulehost.InvocationUnknownClass
	}{
		{name: "http failure", transport: func(request *http.Request) (*http.Response, error) {
			return jsonResponse(request, http.StatusUnauthorized, `{"error":"`+testAPIKey+`"}`), nil
		}, outcome: modulehost.InvocationFailed},
		{name: "transport ambiguity", transport: func(*http.Request) (*http.Response, error) { return nil, errors.New("after dispatch") }, outcome: modulehost.InvocationUnknown, unknown: modulehost.UnknownClassInvokeReturnedError},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			adapter := testAdapter(t, &testAPIKeyResolver{key: []byte(testAPIKey)}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) { calls++; return test.transport(request) })})
			result, err := adapter.Invoke(context.Background(), prepared)
			if err != nil || result.Outcome != test.outcome || result.UnknownClass != test.unknown || calls != 1 {
				t.Fatalf("result=%+v error=%v calls=%d", result, err, calls)
			}
			if strings.Contains(fmt.Sprintf("%+v", result), testAPIKey) {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestInvokeStreamUsesSharedAccumulatorAndRejectsIncompleteStream(t *testing.T) {
	prepared := testPreparedInvocation(t, json.RawMessage(`{"max_tokens":128,"temperature":0,"thinking":{"type":"disabled"}}`), json.RawMessage(`{"max_tokens":128,"temperature":0,"thinking":{"type":"disabled"}}`))
	stream := "data: {\"id\":\"zhipu-stream-1\",\"model\":\"glm-4.5\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"zhipu-stream-1\",\"model\":\"glm-4.5\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"zhipu-stream-1\",\"model\":\"glm-4.5\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1,\"total_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	adapter := testAdapter(t, &testAPIKeyResolver{key: []byte(testAPIKey)}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) { return sseResponse(request, stream), nil })})
	result, err := adapter.InvokeStream(context.Background(), prepared, 64)
	if err != nil || result.Outcome != modulehost.InvocationSucceeded {
		t.Fatalf("stream result=%+v error=%v", result, err)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(result.Output)
	if err != nil || output.AssistantText != "hello" || output.ProviderRequestID != "zhipu-stream-1" {
		t.Fatalf("stream output=%+v err=%v", output, err)
	}

	partial := "data: {\"id\":\"zhipu-stream-2\",\"model\":\"glm-4.5\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"
	parsed, parseErr := parseChatCompletionsStream(http.Header{"Content-Type": []string{"text/event-stream"}}, strings.NewReader(partial), ModelGLM45, 64)
	if !errors.Is(parseErr, errStreamEOF) || parsed.Result.Terminal != corecontract.ModelStreamTerminalUnknownV1 || parsed.Result.AssistantText != "partial" {
		t.Fatalf("partial parse=%+v err=%v", parsed, parseErr)
	}
}

func TestStreamLengthIsNotSuccessfulText(t *testing.T) {
	prepared := testPreparedInvocation(t, json.RawMessage(`{}`), json.RawMessage(`{}`))
	body := "data: {\"id\":\"zhipu-stream-3\",\"model\":\"glm-4.5\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\n"
	adapter := testAdapter(t, &testAPIKeyResolver{key: []byte(testAPIKey)}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) { return sseResponse(request, body), nil })})
	result, err := adapter.InvokeStream(context.Background(), prepared, 64)
	if err != nil || result.Outcome != modulehost.InvocationFailed {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func testAdapter(t *testing.T, resolver APIKeyResolver, client *http.Client) *Adapter {
	t.Helper()
	adapter, err := New(Options{Provider: testProvider(), AllowedModelBuildIDs: map[string]string{ModelGLM45: testModelBuild}, APIKeyResolver: resolver, HTTPClient: client})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return adapter
}

func testProvider() moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{ModuleID: "freeagent.builtin.model.zhipu", Version: "2.0.0", ArtifactDigest: strings.Repeat("a", moduleapi.SHA256HexLength), InstanceID: "zhipu-main", ExecutionClass: moduleapi.ExecutionTrustedInProcess, AdapterIdentity: AdapterIdentityV1, ActivationRevision: 1}
}

func testPreparedInvocation(t *testing.T, configParameters, requestParameters json.RawMessage) modulehost.PreparedInvocation {
	t.Helper()
	return modulehost.PreparedInvocation{Invocation: modulehost.ModuleInvocation{InvocationID: "invocation-1", RunID: "run-1", MemberID: "member-1", MemberSnapshotDigest: strings.Repeat("d", moduleapi.SHA256HexLength), Port: modelGeneratePortV1, Input: testRequestCanonical(t, requestParameters), Deadline: time.Now().UTC().Add(time.Minute)}, Binding: moduleapi.PortBinding{Provider: testProvider(), ConfigRef: strings.Repeat("b", moduleapi.SHA256HexLength), AuthorityCeilingRef: strings.Repeat("c", moduleapi.SHA256HexLength), FailurePolicy: moduleapi.FailureRequired}, ConfigCanonical: testModelConfigCanonical(t, configParameters), AuthorityCanonical: append([]byte(nil), legacyDenyAllModelAuthorityV1...)}
}

func testModelConfigCanonical(t *testing.T, parameters json.RawMessage) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewModelBindingConfigV2(moduleapi.ModelBindingConfigV2{SchemaVersion: moduleapi.ModelBindingConfigSchemaV2, Provider: ProviderNameV1, Model: ModelGLM45, ModelBuildID: testModelBuild, Parameters: parameters})
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func testRequestCanonical(t *testing.T, parameters json.RawMessage) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewModelGenerateRequestV1(moduleapi.ModelGenerateRequestV1{SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1, Messages: []moduleapi.ModelMessageV1{{Role: moduleapi.ModelRoleUser, Content: "Answer concisely."}}, Parameters: parameters})
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func successfulResponseJSON() string {
	return `{"id":"zhipu-request-1","model":"glm-4.5","choices":[{"index":0,"message":{"role":"assistant","content":"FREEAGENT_P3_OK","reasoning_content":"private reasoning"},"finish_reason":"stop"}],"usage":{"prompt_tokens":14,"completion_tokens":6,"total_tokens":20,"prompt_tokens_details":{"cached_tokens":0}}}`
}

func jsonResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: request}
}

func sseResponse(request *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: request}
}

func assertUint64Pointer(t *testing.T, value *uint64, want uint64) {
	t.Helper()
	if value == nil || *value != want {
		t.Fatalf("value=%v want=%d", value, want)
	}
}
