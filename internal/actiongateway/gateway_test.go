package actiongateway

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestNormalizeExecutorResultPreservesKnownSuccessAndRejectsOnlyResult(t *testing.T) {
	request := gatewayTestRequest(t, 16)
	provider := gatewayTestProvider()

	valid := moduleapi.ActionExecutionResultV1{
		SchemaVersion:   moduleapi.ActionExecutionResultSchemaV1,
		AttemptID:       request.AttemptID,
		Outcome:         moduleapi.ActionExecutionSucceeded,
		CanonicalResult: json.RawMessage(`{"ok":true}`),
	}
	got := normalizeExecutorResult(request, provider, valid, nil)
	if got.Outcome != moduleapi.ActionExecutionSucceeded ||
		got.ResultRejectionClassification != "" ||
		string(got.CanonicalResult) != `{"ok":true}` {
		t.Fatalf("valid success = %+v", got)
	}

	oversizeRequest := gatewayTestRequest(t, 4)
	oversize := valid
	oversize.AttemptID = oversizeRequest.AttemptID
	got = normalizeExecutorResult(oversizeRequest, provider, oversize, nil)
	if got.Outcome != moduleapi.ActionExecutionSucceeded ||
		got.ResultRejectionClassification != classificationResultRejected ||
		len(got.CanonicalResult) != 0 || got.UnknownReason != "" {
		t.Fatalf("known Effect with rejected result = %+v", got)
	}

	nonCanonical := valid
	nonCanonical.CanonicalResult = json.RawMessage(`{"z":1,"a":2}`)
	got = normalizeExecutorResult(request, provider, nonCanonical, nil)
	if got.Outcome != moduleapi.ActionExecutionSucceeded ||
		got.ResultRejectionClassification != classificationResultRejected {
		t.Fatalf("non-canonical known success = %+v", got)
	}
}

func TestNormalizeExecutorResultUsesUnknownOnlyForAmbiguousExecution(t *testing.T) {
	request := gatewayTestRequest(t, 64)
	provider := gatewayTestProvider()
	got := normalizeExecutorResult(
		request,
		provider,
		moduleapi.ActionExecutionResultV1{},
		errors.New("transport broke after call"),
	)
	if got.Outcome != moduleapi.ActionExecutionUnknown ||
		got.UnknownReason != unknownExecutorError {
		t.Fatalf("executor error = %+v", got)
	}

	mismatched := moduleapi.ActionExecutionResultV1{
		SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
		AttemptID:           "other-attempt",
		Outcome:             moduleapi.ActionExecutionFailed,
		ErrorClassification: "PROVIDER_REJECTED",
	}
	got = normalizeExecutorResult(request, provider, mismatched, nil)
	if got.Outcome != moduleapi.ActionExecutionUnknown ||
		got.UnknownReason != unknownInvalidExecutorResult {
		t.Fatalf("identity mismatch = %+v", got)
	}

	invalidSuccesses := []struct {
		name   string
		mutate func(*moduleapi.ActionExecutionResultV1)
	}{
		{
			name: "wrong schema",
			mutate: func(result *moduleapi.ActionExecutionResultV1) {
				result.SchemaVersion = "action-execution-result/v999"
			},
		},
		{
			name: "missing result",
			mutate: func(result *moduleapi.ActionExecutionResultV1) {
				result.CanonicalResult = nil
			},
		},
		{
			name: "success with error",
			mutate: func(result *moduleapi.ActionExecutionResultV1) {
				result.ErrorClassification = "IMPOSSIBLE_SUCCESS_ERROR"
			},
		},
		{
			name: "non canonical receipt",
			mutate: func(result *moduleapi.ActionExecutionResultV1) {
				result.ProviderReceipt = json.RawMessage(`{"z":1,"a":2}`)
			},
		},
		{
			name: "invalid external operation",
			mutate: func(result *moduleapi.ActionExecutionResultV1) {
				result.ExternalOperationID = " bad "
			},
		},
	}
	for _, test := range invalidSuccesses {
		t.Run(test.name, func(t *testing.T) {
			executed := moduleapi.ActionExecutionResultV1{
				SchemaVersion:   moduleapi.ActionExecutionResultSchemaV1,
				AttemptID:       request.AttemptID,
				Outcome:         moduleapi.ActionExecutionSucceeded,
				CanonicalResult: json.RawMessage(`{"ok":true}`),
			}
			test.mutate(&executed)
			got := normalizeExecutorResult(request, provider, executed, nil)
			if got.Outcome != moduleapi.ActionExecutionUnknown ||
				got.UnknownReason != unknownInvalidExecutorResult ||
				got.ResultRejectionClassification != "" {
				t.Fatalf("invalid success = %+v", got)
			}
		})
	}
}

func TestNormalizeExecutorResultPreservesKnownSuccessMetadataWhenOnlyResultIsRejected(
	t *testing.T,
) {
	request := gatewayTestRequest(t, 4)
	provider := gatewayTestProvider()
	executed := moduleapi.ActionExecutionResultV1{
		SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
		AttemptID:           request.AttemptID,
		Outcome:             moduleapi.ActionExecutionSucceeded,
		CanonicalResult:     json.RawMessage(`{"too":"large"}`),
		ProviderReceipt:     json.RawMessage(`{"receipt":"stable"}`),
		ExternalOperationID: "external-operation-1",
	}
	got := normalizeExecutorResult(request, provider, executed, nil)
	if got.Outcome != moduleapi.ActionExecutionSucceeded ||
		got.ResultRejectionClassification != classificationResultRejected ||
		string(got.ProviderReceipt) != string(executed.ProviderReceipt) ||
		got.ExternalOperationID != executed.ExternalOperationID ||
		len(got.CanonicalResult) != 0 || got.UnknownReason != "" {
		t.Fatalf("known success metadata = %+v", got)
	}
}

func gatewayTestRequest(
	t *testing.T,
	maximum uint32,
) moduleapi.ActionExecutionRequestV1 {
	t.Helper()
	request, _, err := moduleapi.NewActionExecutionRequestV1(
		moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        "action-attempt-test",
			PublicActionID:   "text.stats",
			ProviderActionID: "builtin.text.stats",
			DefinitionDigest: gatewayTestDigest("d"),
			MaxResultBytes:   maximum,
			PreparedPayload:  json.RawMessage(`{"text":"hello"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func gatewayTestProvider() moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           "builtin.action.text",
		Version:            "1.0.0",
		ArtifactDigest:     gatewayTestDigest("a"),
		InstanceID:         "builtin-text-action",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "builtin.action.text.v1",
		ActivationRevision: 1,
	}
}

func gatewayTestDigest(character string) string {
	value := ""
	for len(value) < 64 {
		value += character
	}
	return value[:64]
}
