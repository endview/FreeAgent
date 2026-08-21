package remoteactionhttp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestExecutePreparedAcceptsProviderFailedResponseExactlyOnce(t *testing.T) {
	descriptor, _ := testDescriptor(t)
	resolver := &recordingResolver{
		values: map[string]string{"reference.one": "example-material-1234"},
	}
	var calls atomic.Int64
	adapter, err := newAdapterWithRoundTripper(
		testProvider(strings.Repeat("a", 64), "instance", 1),
		descriptor,
		resolver,
		roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			frozen, err := moduleapi.RestoreActionExecutionRequestV1(body)
			if err != nil {
				return nil, err
			}
			return jsonResponse(
				http.StatusUnprocessableEntity,
				canonicalResult(t, moduleapi.ActionExecutionResultV1{
					SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
					AttemptID:           frozen.AttemptID,
					Outcome:             moduleapi.ActionExecutionFailed,
					ErrorClassification: "PROVIDER_INPUT_REJECTED",
				}),
			), nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.ExecutePrepared(
		context.Background(),
		testExecution(
			t,
			testProvider(strings.Repeat("a", 64), "instance", 1),
			"attempt-provider-failed",
			"https://api.example.com/action",
			"reference.one",
		),
	)
	if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != "PROVIDER_INPUT_REJECTED" ||
		len(result.CanonicalResult) != 0 || len(result.ProviderReceipt) != 0 ||
		result.ExternalOperationID != "" || result.UnknownReason != "" {
		t.Fatalf("provider FAILED result=%+v error=%v", result, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider FAILED RoundTrip calls=%d want=1", calls.Load())
	}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if len(resolver.requests) != 1 || resolver.requests[0].SecretRef != "reference.one" {
		t.Fatalf("provider FAILED resolver requests=%+v", resolver.requests)
	}
}
