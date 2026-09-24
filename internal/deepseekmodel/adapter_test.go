package deepseekmodel

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

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	testFlashBuild = "deepseek-v4-flash/public-alias-observed-2026-08-04"
	testProBuild   = "deepseek-v4-pro/public-alias-observed-2026-08-04"
	testAPIKey     = "not-a-secret"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTrip(request)
}

type testAPIKeyResolver struct {
	key      []byte
	err      error
	identity APIKeyIdentity
}

func (resolver *testAPIKeyResolver) ResolveAPIKey(
	_ context.Context,
	identity APIKeyIdentity,
) ([]byte, error) {
	resolver.identity = identity
	return append([]byte(nil), resolver.key...), resolver.err
}

func TestAdapterSuccessMapsCacheReasoningAndExcludesPrivateContent(t *testing.T) {
	parameters := json.RawMessage(
		`{"max_tokens":512,"reasoning_effort":"high","thinking":{"type":"enabled"},"user_id":"workspace_stable"}`,
	)
	prepared := testPreparedInvocation(
		t,
		testProvider(),
		ModelV4Pro,
		testProBuild,
		parameters,
		parameters,
	)
	resolver := &testAPIKeyResolver{key: []byte(testAPIKey)}
	var requestBody []byte
	client := &http.Client{Transport: roundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPost ||
				request.URL.String() != officialChatCompletionsURL {
				t.Fatalf("unexpected request target: %s %s", request.Method, request.URL)
			}
			if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
				t.Fatal("missing bearer credential")
			}
			if request.Header.Get("Content-Type") != "application/json" ||
				request.Header.Get("Accept") != "application/json" {
				t.Fatalf("unexpected headers: %v", request.Header)
			}
			var err error
			requestBody, err = io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}
			return jsonResponse(request, http.StatusOK, successfulResponseJSON()), nil
		},
	)}
	adapter := testAdapter(t, prepared.Binding.Provider, resolver, client)

	result, err := adapter.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result.Outcome != modulehost.InvocationSucceeded ||
		result.Provider != prepared.Binding.Provider {
		t.Fatalf("result=%+v", result)
	}
	const expectedRequest = `{"max_tokens":512,"messages":[{"content":"You are concise.","role":"system"},{"content":"Answer the task.","role":"user"}],"model":"deepseek-v4-pro","reasoning_effort":"high","stream":false,"thinking":{"type":"enabled"},"user_id":"workspace_stable"}`
	if string(requestBody) != expectedRequest {
		t.Fatalf("request body=%s", requestBody)
	}
	if resolver.identity != (APIKeyIdentity{
		Provider:     ProviderNameV1,
		Model:        ModelV4Pro,
		ModelBuildID: testProBuild,
	}) {
		t.Fatalf("resolver identity=%+v", resolver.identity)
	}

	output, err := moduleapi.RestoreModelGenerateOutputV1(result.Output)
	if err != nil {
		t.Fatalf("RestoreModelGenerateOutputV1: %v", err)
	}
	if output.AssistantText != "safe final answer" ||
		output.ProviderRequestID != "request-123" {
		t.Fatalf("output=%+v", output)
	}
	usage, err := moduleapi.RestoreModelUsageReceiptV2(result.UsageReceipt)
	if err != nil {
		t.Fatalf("RestoreModelUsageReceiptV2: %v", err)
	}
	assertUint64Pointer(t, "input", usage.InputTokens, 100)
	assertUint64Pointer(t, "cached", usage.CachedInputTokens, 80)
	assertUint64Pointer(t, "uncached", usage.UncachedInputTokens, 20)
	assertUint64Pointer(t, "output", usage.OutputTokens, 25)
	assertUint64Pointer(t, "reasoning", usage.ReasoningTokens, 12)
	for _, forbidden := range []string{
		testAPIKey,
		"private reasoning must never persist",
		"reasoning_content",
	} {
		if bytes.Contains(result.Output, []byte(forbidden)) ||
			bytes.Contains(result.UsageReceipt, []byte(forbidden)) {
			t.Fatalf("persisted result contains forbidden value %q", forbidden)
		}
	}
	if !bytes.Contains(usage.RawReceipt, []byte(`"system_fingerprint":"fp_public_1"`)) ||
		!bytes.Contains(usage.RawReceipt, []byte(`"prompt_cache_hit_tokens":80`)) {
		t.Fatalf("safe raw receipt=%s", usage.RawReceipt)
	}
}

func TestAdapterAcceptsReusableArtifactAcrossInstances(t *testing.T) {
	provider := testProvider()
	adapter := testAdapter(
		t,
		provider,
		&testAPIKeyResolver{key: []byte(testAPIKey)},
		&http.Client{Transport: roundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				return jsonResponse(request, http.StatusOK, successfulResponseJSON()), nil
			},
		)},
	)
	second := provider
	second.InstanceID = "deepseek-second"
	second.ActivationRevision = 2
	prepared := testPreparedInvocation(
		t,
		second,
		ModelV4Pro,
		testProBuild,
		json.RawMessage(`{}`),
		json.RawMessage(`{}`),
	)
	result, err := adapter.Invoke(context.Background(), prepared)
	if err != nil || result.Outcome != modulehost.InvocationSucceeded {
		t.Fatalf("Invoke result=%+v error=%v", result, err)
	}
	if result.Provider != second {
		t.Fatalf("result provider=%+v want=%+v", result.Provider, second)
	}
}

func TestAdapterRejectsSuccessfulResponseFromDifferentModel(t *testing.T) {
	prepared := testPreparedInvocation(
		t,
		testProvider(),
		ModelV4Flash,
		testFlashBuild,
		json.RawMessage(`{}`),
		json.RawMessage(`{}`),
	)
	response := strings.Replace(
		successfulResponseJSON(),
		`"model":"deepseek-v4-pro"`,
		`"model":"deepseek-v4-flash-other"`,
		1,
	)
	adapter := testAdapter(
		t,
		prepared.Binding.Provider,
		&testAPIKeyResolver{key: []byte(testAPIKey)},
		&http.Client{Transport: roundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				return jsonResponse(request, http.StatusOK, response), nil
			},
		)},
	)

	result, err := adapter.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result.Outcome != modulehost.InvocationFailed ||
		len(result.Output) != 0 || len(result.UsageReceipt) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestAdapterExplicitHTTPFailureDoesNotExposeBodyOrCredential(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			prepared := testPreparedInvocation(
				t,
				testProvider(),
				ModelV4Flash,
				testFlashBuild,
				json.RawMessage(`{}`),
				json.RawMessage(`{}`),
			)
			adapter := testAdapter(
				t,
				prepared.Binding.Provider,
				&testAPIKeyResolver{key: []byte(testAPIKey)},
				&http.Client{Transport: roundTripFunc(
					func(request *http.Request) (*http.Response, error) {
						return jsonResponse(
							request,
							status,
							`{"error":"`+testAPIKey+`"}`,
						), nil
					},
				)},
			)
			result, err := adapter.Invoke(context.Background(), prepared)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if result.Outcome != modulehost.InvocationFailed ||
				len(result.Output) != 0 || len(result.UsageReceipt) != 0 {
				t.Fatalf("result=%+v", result)
			}
			if strings.Contains(fmt.Sprintf("%+v", result), testAPIKey) {
				t.Fatal("credential leaked into failed result")
			}
		})
	}
}

func TestAdapterAmbiguousTransportAndBodyReadAreUnknown(t *testing.T) {
	prepared := testPreparedInvocation(
		t,
		testProvider(),
		ModelV4Pro,
		testProBuild,
		json.RawMessage(`{}`),
		json.RawMessage(`{}`),
	)
	for _, test := range []struct {
		name         string
		transport    roundTripFunc
		unknownClass modulehost.InvocationUnknownClass
	}{
		{
			name:         "transport",
			unknownClass: modulehost.UnknownClassInvokeReturnedError,
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("ambiguous after send " + testAPIKey)
			},
		},
		{
			name:         "response body",
			unknownClass: modulehost.UnknownClassResponseBodyReadIncomplete,
			transport: func(request *http.Request) (*http.Response, error) {
				response := jsonResponse(request, http.StatusOK, "")
				response.Body = &failingReadCloser{}
				response.ContentLength = -1
				return response, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			adapter := testAdapter(
				t,
				prepared.Binding.Provider,
				&testAPIKeyResolver{key: []byte(testAPIKey)},
				&http.Client{Transport: roundTripFunc(
					func(request *http.Request) (*http.Response, error) {
						calls++
						return test.transport(request)
					},
				)},
			)
			result, err := adapter.Invoke(context.Background(), prepared)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if result.Outcome != modulehost.InvocationUnknown ||
				result.UnknownClass != test.unknownClass ||
				len(result.Output) != 0 || len(result.UsageReceipt) != 0 {
				t.Fatalf("result=%+v", result)
			}
			if calls != 1 {
				t.Fatalf("transport calls=%d want 1", calls)
			}
			if strings.Contains(fmt.Sprintf("%+v", result), testAPIKey) {
				t.Fatal("credential leaked into UNKNOWN result")
			}
		})
	}
}

func TestReadResponseBodyClassifiesNoUsableResponse(t *testing.T) {
	for _, response := range []*http.Response{nil, &http.Response{}} {
		body, outcome, class := readResponseBody(response)
		if len(body) != 0 || outcome != modulehost.InvocationUnknown ||
			class != modulehost.UnknownClassNoUsableResponse {
			t.Fatalf(
				"readResponseBody(%v) body=%q outcome=%q class=%q",
				response,
				body,
				outcome,
				class,
			)
		}
	}
}

func TestAdapterDeniesRedirectWithoutSecondRequest(t *testing.T) {
	prepared := testPreparedInvocation(
		t,
		testProvider(),
		ModelV4Flash,
		testFlashBuild,
		json.RawMessage(`{}`),
		json.RawMessage(`{}`),
	)
	calls := 0
	adapter := testAdapter(
		t,
		prepared.Binding.Provider,
		&testAPIKeyResolver{key: []byte(testAPIKey)},
		&http.Client{Transport: roundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				calls++
				response := jsonResponse(request, http.StatusTemporaryRedirect, `{}`)
				response.Header.Set("Location", "https://redirect.invalid/steal")
				return response, nil
			},
		)},
	)
	result, err := adapter.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result.Outcome != modulehost.InvocationFailed || calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
}

func TestAdapterRejectsInvalidClosureBeforeDispatch(t *testing.T) {
	validProvider := testProvider()
	validConfigParameters := json.RawMessage(`{"temperature":0}`)
	validRequestParameters := json.RawMessage(`{"temperature":0}`)
	valid := func(t *testing.T) modulehost.PreparedInvocation {
		return testPreparedInvocation(
			t,
			validProvider,
			ModelV4Pro,
			testProBuild,
			validConfigParameters,
			validRequestParameters,
		)
	}
	tests := []struct {
		name   string
		mutate func(*testing.T, *modulehost.PreparedInvocation)
	}{
		{
			name: "wrong provider name",
			mutate: func(t *testing.T, prepared *modulehost.PreparedInvocation) {
				prepared.ConfigCanonical = testModelConfigCanonical(
					t,
					"not-deepseek",
					ModelV4Pro,
					testProBuild,
					validConfigParameters,
				)
			},
		},
		{
			name: "untrusted build",
			mutate: func(t *testing.T, prepared *modulehost.PreparedInvocation) {
				prepared.ConfigCanonical = testModelConfigCanonical(
					t,
					ProviderNameV1,
					ModelV4Pro,
					"deepseek-v4-pro/untrusted",
					validConfigParameters,
				)
			},
		},
		{
			name: "unsupported parameter",
			mutate: func(t *testing.T, prepared *modulehost.PreparedInvocation) {
				unsupported := json.RawMessage(`{"unknown_parameter":true}`)
				prepared.ConfigCanonical = testModelConfigCanonical(
					t,
					ProviderNameV1,
					ModelV4Pro,
					testProBuild,
					unsupported,
				)
				prepared.Invocation.Input = testRequestCanonical(t, unsupported, nil)
			},
		},
		{
			name: "parameter escalation",
			mutate: func(t *testing.T, prepared *modulehost.PreparedInvocation) {
				prepared.ConfigCanonical = testModelConfigCanonical(
					t,
					ProviderNameV1,
					ModelV4Pro,
					testProBuild,
					json.RawMessage(`{"max_tokens":128}`),
				)
				prepared.Invocation.Input = testRequestCanonical(
					t,
					json.RawMessage(`{"max_tokens":512}`),
					nil,
				)
			},
		},
		{
			name: "Actions fail closed",
			mutate: func(t *testing.T, prepared *modulehost.PreparedInvocation) {
				actions := []moduleapi.ModelActionDefinitionV1{
					{
						ActionID:    "code.inspect",
						Description: "Inspect code.",
						InputSchema: json.RawMessage(`{"additionalProperties":false,"properties":{},"type":"object"}`),
					},
				}
				prepared.Invocation.Input = testRequestCanonical(
					t,
					validRequestParameters,
					actions,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared := valid(t)
			test.mutate(t, &prepared)
			calls := 0
			adapter := testAdapter(
				t,
				validProvider,
				&testAPIKeyResolver{key: []byte(testAPIKey)},
				&http.Client{Transport: roundTripFunc(
					func(*http.Request) (*http.Response, error) {
						calls++
						return nil, errors.New("must not dispatch")
					},
				)},
			)
			result, err := adapter.Invoke(context.Background(), prepared)
			if err == nil || !errors.Is(err, ErrInvocation) {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if calls != 0 {
				t.Fatalf("transport called %d times", calls)
			}
		})
	}
}

func TestAdapterAllowsReviewerMaxTokensInsertionAndTightening(t *testing.T) {
	for _, test := range []struct {
		name              string
		configParameters  json.RawMessage
		requestParameters json.RawMessage
	}{
		{
			name:              "insert",
			configParameters:  json.RawMessage(`{"temperature":0}`),
			requestParameters: json.RawMessage(`{"max_tokens":512,"temperature":0}`),
		},
		{
			name:              "tighten",
			configParameters:  json.RawMessage(`{"max_tokens":2048,"temperature":0}`),
			requestParameters: json.RawMessage(`{"max_tokens":512,"temperature":0}`),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared := testPreparedInvocation(
				t,
				testProvider(),
				ModelV4Pro,
				testProBuild,
				test.configParameters,
				test.requestParameters,
			)
			adapter := testAdapter(
				t,
				prepared.Binding.Provider,
				&testAPIKeyResolver{key: []byte(testAPIKey)},
				&http.Client{Transport: roundTripFunc(
					func(request *http.Request) (*http.Response, error) {
						return jsonResponse(request, http.StatusOK, successfulResponseJSON()), nil
					},
				)},
			)
			result, err := adapter.Invoke(context.Background(), prepared)
			if err != nil || result.Outcome != modulehost.InvocationSucceeded {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func TestAdapterResolverDiagnosticsAndInvalidMaterialDoNotLeak(t *testing.T) {
	prepared := testPreparedInvocation(
		t,
		testProvider(),
		ModelV4Pro,
		testProBuild,
		json.RawMessage(`{}`),
		json.RawMessage(`{}`),
	)
	for _, resolver := range []*testAPIKeyResolver{
		{err: errors.New("resolver accidentally logged " + testAPIKey)},
		{key: []byte("invalid key with spaces " + testAPIKey)},
	} {
		calls := 0
		adapter := testAdapter(
			t,
			prepared.Binding.Provider,
			resolver,
			&http.Client{Transport: roundTripFunc(
				func(*http.Request) (*http.Response, error) {
					calls++
					return nil, nil
				},
			)},
		)
		_, err := adapter.Invoke(context.Background(), prepared)
		if err == nil || !errors.Is(err, ErrAPIKeyResolve) {
			t.Fatalf("error=%v", err)
		}
		if strings.Contains(err.Error(), testAPIKey) {
			t.Fatalf("credential leaked in error: %v", err)
		}
		if calls != 0 {
			t.Fatalf("transport called %d times", calls)
		}
	}
}

func testAdapter(
	t *testing.T,
	provider moduleapi.ActivatedModuleRef,
	resolver APIKeyResolver,
	client *http.Client,
) *Adapter {
	t.Helper()
	adapter, err := New(Options{
		Provider: provider,
		AllowedModelBuildIDs: map[string]string{
			ModelV4Flash: testFlashBuild,
			ModelV4Pro:   testProBuild,
		},
		APIKeyResolver: resolver,
		HTTPClient:     client,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return adapter
}

func testProvider() moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           "freeagent.builtin.model.deepseek",
		Version:            "1.0.0",
		ArtifactDigest:     strings.Repeat("a", moduleapi.SHA256HexLength),
		InstanceID:         "deepseek-main",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    AdapterIdentityV1,
		ActivationRevision: 1,
	}
}

func testPreparedInvocation(
	t *testing.T,
	provider moduleapi.ActivatedModuleRef,
	model string,
	modelBuildID string,
	configParameters json.RawMessage,
	requestParameters json.RawMessage,
) modulehost.PreparedInvocation {
	t.Helper()
	return modulehost.PreparedInvocation{
		Invocation: modulehost.ModuleInvocation{
			InvocationID:         "invocation-1",
			RunID:                "run-1",
			MemberID:             "member-1",
			MemberSnapshotDigest: strings.Repeat("d", moduleapi.SHA256HexLength),
			Port:                 modelGeneratePortV1,
			Input: testRequestCanonical(
				t,
				requestParameters,
				nil,
			),
			Deadline: time.Now().UTC().Add(time.Minute),
		},
		Binding: moduleapi.PortBinding{
			Provider:            provider,
			ConfigRef:           strings.Repeat("b", moduleapi.SHA256HexLength),
			AuthorityCeilingRef: strings.Repeat("c", moduleapi.SHA256HexLength),
			FailurePolicy:       moduleapi.FailureRequired,
		},
		ConfigCanonical: testModelConfigCanonical(
			t,
			ProviderNameV1,
			model,
			modelBuildID,
			configParameters,
		),
		AuthorityCanonical: append([]byte(nil), legacyDenyAllModelAuthorityV1...),
	}
}

func testModelConfigCanonical(
	t *testing.T,
	provider string,
	model string,
	modelBuildID string,
	parameters json.RawMessage,
) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewModelBindingConfigV2(
		moduleapi.ModelBindingConfigV2{
			SchemaVersion: moduleapi.ModelBindingConfigSchemaV2,
			Provider:      provider,
			Model:         model,
			ModelBuildID:  modelBuildID,
			Parameters:    parameters,
		},
	)
	if err != nil {
		t.Fatalf("NewModelBindingConfigV2: %v", err)
	}
	return canonical
}

func testRequestCanonical(
	t *testing.T,
	parameters json.RawMessage,
	actions []moduleapi.ModelActionDefinitionV1,
) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{
				{Role: moduleapi.ModelRoleSystem, Content: "You are concise."},
				{Role: moduleapi.ModelRoleUser, Content: "Answer the task."},
			},
			Parameters: parameters,
			Actions:    actions,
		},
	)
	if err != nil {
		t.Fatalf("NewModelGenerateRequestV1: %v", err)
	}
	return canonical
}

func successfulResponseJSON() string {
	return `{
  "id":"request-123",
  "object":"chat.completion",
  "created":1785800000,
  "model":"deepseek-v4-pro",
  "system_fingerprint":"fp_public_1",
  "choices":[{
    "index":0,
    "message":{
      "role":"assistant",
      "content":"safe final answer",
      "reasoning_content":"private reasoning must never persist"
    },
    "finish_reason":"stop"
  }],
  "usage":{
    "prompt_tokens":100,
    "prompt_cache_hit_tokens":80,
    "prompt_cache_miss_tokens":20,
    "completion_tokens":25,
    "completion_tokens_details":{"reasoning_tokens":12},
    "total_tokens":125
  }
}`
}

func jsonResponse(
	request *http.Request,
	status int,
	body string,
) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}
}

type failingReadCloser struct{}

func (*failingReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("network read failed")
}

func (*failingReadCloser) Close() error { return nil }

func assertUint64Pointer(
	t *testing.T,
	name string,
	value *uint64,
	want uint64,
) {
	t.Helper()
	if value == nil || *value != want {
		t.Fatalf("%s=%v want=%d", name, value, want)
	}
}
