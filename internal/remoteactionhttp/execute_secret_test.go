package remoteactionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestResolvedBearerCredentialGrammar(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "single payload byte", value: "A", valid: true},
		{name: "all payload characters", value: "Az09-._~+/", valid: true},
		{name: "trailing padding", value: "Az09+/==", valid: true},
		{name: "empty"},
		{name: "padding only", value: "="},
		{name: "leading padding", value: "=Az09"},
		{name: "embedded padding", value: "Az=09"},
		{name: "payload after padding", value: "Az09=A"},
		{name: "quote", value: "Az\"09"},
		{name: "backslash", value: `Az\09`},
		{name: "colon", value: "Az:09"},
		{name: "space", value: "Az 09"},
		{name: "non ASCII", value: "Az中09"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validResolvedSecret([]byte(test.value)); got != test.valid {
				t.Fatalf("validResolvedSecret()=%t want=%t", got, test.valid)
			}
		})
	}
}

func TestExecutePreparedRejectsInvalidBearerCredentialBeforeDispatch(t *testing.T) {
	descriptor, _ := testDescriptor(t)
	tests := []struct {
		name  string
		value string
	}{
		{name: "quote", value: "Az\"09"},
		{name: "backslash", value: `Az\09`},
		{name: "leading padding", value: "=Az09"},
		{name: "embedded padding", value: "Az=09"},
		{name: "payload after padding", value: "Az09=A"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			adapter, err := newAdapterWithRoundTripper(
				testProvider(strings.Repeat("a", 64), "instance", 1),
				descriptor,
				&recordingResolver{values: map[string]string{"credential.ref": test.value}},
				roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls.Add(1)
					return nil, errors.New("dispatch must not occur")
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
					"attempt-invalid-credential",
					"https://api.example.com/action",
					"credential.ref",
				),
			)
			if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
				result.ErrorClassification != failedResolutionUnavailable {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if calls.Load() != 0 {
				t.Fatalf("RoundTrip calls=%d want=0", calls.Load())
			}
		})
	}
}

func TestExecutePreparedAcceptsRFC6750BearerCredential(t *testing.T) {
	descriptor, _ := testDescriptor(t)
	const value = "Az09-._~+/=="
	var calls atomic.Int64
	adapter, err := newAdapterWithRoundTripper(
		testProvider(strings.Repeat("a", 64), "instance", 1),
		descriptor,
		&recordingResolver{values: map[string]string{"credential.ref": value}},
		roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			if request.Header.Get("Authorization") != "Bearer "+value {
				return nil, errors.New("authorization value drifted")
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			frozen, err := moduleapi.RestoreActionExecutionRequestV1(body)
			if err != nil {
				return nil, err
			}
			_, canonical, err := moduleapi.NewActionExecutionResultV1(
				moduleapi.ActionExecutionResultV1{
					SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
					AttemptID:           frozen.AttemptID,
					Outcome:             moduleapi.ActionExecutionSucceeded,
					CanonicalResult:     json.RawMessage(`{"ok":true}`),
					ExternalOperationID: "operation-valid-credential",
				},
			)
			if err != nil {
				return nil, err
			}
			return jsonResponse(http.StatusOK, canonical), nil
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
			"attempt-valid-credential",
			"https://api.example.com/action",
			"credential.ref",
		),
	)
	if err != nil || result.Outcome != moduleapi.ActionExecutionSucceeded {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("RoundTrip calls=%d want=1", calls.Load())
	}
}
