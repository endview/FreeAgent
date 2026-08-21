package exactadapter_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestTextStatsActionDescribePrepareAndExecuteDeterministically(
	t *testing.T,
) {
	adapter, err := exactadapter.NewTextStatsAction(textStatsProvider())
	if err != nil {
		t.Fatal(err)
	}
	describeRequest, _, err := moduleapi.NewActionDescribeRequestV1(
		moduleapi.ActionDescribeRequestV1{
			SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
			Parameters:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := adapter.Describe(context.Background(), describeRequest)
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 1 ||
		definitions[0].ProviderActionID != exactadapter.TextStatsActionIDV1 ||
		definitions[0].RequestedEffectClass != moduleapi.EffectNone ||
		definitions[0].RequestedMaxResultBytes !=
			exactadapter.TextStatsMaxResultBytesV1 {
		t.Fatalf("definitions=%+v", definitions)
	}
	originalSchema := bytes.Clone(definitions[0].InputSchema)
	definitions[0].InputSchema[0] = '['
	repeated, err := adapter.Describe(context.Background(), describeRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repeated[0].InputSchema, originalSchema) {
		t.Fatal("Describe result aliases prior caller-owned schema")
	}

	const inputText = "Hi 世界\nGo"
	canonicalInput, err := moduleapi.CanonicalizeAndValidateActionInputV1(
		originalSchema,
		json.RawMessage(`{"text":"Hi 世界\nGo"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	actionRequest, _, err := moduleapi.NewActionRequestV1(
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   exactadapter.TextStatsActionIDV1,
			ProviderActionID: exactadapter.TextStatsActionIDV1,
			DefinitionDigest: strings.Repeat("a", 64),
			CanonicalInput:   canonicalInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := adapter.Prepare(context.Background(), actionRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prepared, canonicalInput) {
		t.Fatalf("prepared=%s input=%s", prepared, canonicalInput)
	}

	executionRequest, _, err := moduleapi.NewActionExecutionRequestV1(
		moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        "action-attempt-1",
			PublicActionID:   exactadapter.TextStatsActionIDV1,
			ProviderActionID: exactadapter.TextStatsActionIDV1,
			DefinitionDigest: strings.Repeat("a", 64),
			MaxResultBytes:   exactadapter.TextStatsMaxResultBytesV1,
			PreparedPayload:  prepared,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.ExecutePrepared(
		context.Background(),
		textStatsExecution(t, textStatsProvider(), executionRequest),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != moduleapi.ActionExecutionSucceeded ||
		result.AttemptID != executionRequest.AttemptID ||
		len(result.ProviderReceipt) != 0 ||
		result.ExternalOperationID != "" ||
		result.ErrorClassification != "" || result.UnknownReason != "" {
		t.Fatalf("execution result=%+v", result)
	}
	const want = `{"bytes":12,"lines":2,"runes":8,"words":3}`
	if string(result.CanonicalResult) != want {
		t.Fatalf("stats=%s for %q", result.CanonicalResult, inputText)
	}
	if err := moduleapi.ValidateActionExecutionResultForRequestV1(
		executionRequest,
		result,
	); err != nil {
		t.Fatal(err)
	}

	result.CanonicalResult[0] = '['
	secondInstance := textStatsProvider()
	secondInstance.InstanceID = "text-stats-secondary"
	secondInstance.ActivationRevision = 9
	repeatedResult, err := adapter.ExecutePrepared(
		context.Background(),
		textStatsExecution(t, secondInstance, executionRequest),
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(repeatedResult.CanonicalResult) != want {
		t.Fatal("ExecutePrepared result aliases prior caller-owned bytes")
	}
}

func TestTextStatsActionGenericInvokeAlwaysRejectsWithoutExecution(
	t *testing.T,
) {
	adapter, err := exactadapter.NewTextStatsAction(textStatsProvider())
	if err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{nil, context.Background()} {
		result, err := adapter.Invoke(ctx, modulehost.PreparedInvocation{})
		if !errors.Is(err, exactadapter.ErrTextStatsGenericInvocation) {
			t.Fatalf("Invoke error=%v", err)
		}
		if result.InvocationID != "" ||
			result.Provider != (moduleapi.ActivatedModuleRef{}) ||
			result.Outcome != "" || len(result.Output) != 0 ||
			len(result.UsageReceipt) != 0 {
			t.Fatalf("Invoke result=%+v", result)
		}
	}
}

func TestTextStatsActionFailsClosedOnProviderAndActionDrift(t *testing.T) {
	invalid := textStatsProvider()
	invalid.ExecutionClass = moduleapi.ExecutionDeclarative
	if _, err := exactadapter.NewTextStatsAction(invalid); !errors.Is(
		err,
		exactadapter.ErrInvalidTextStatsProvider,
	) {
		t.Fatalf("constructor error=%v", err)
	}

	adapter, err := exactadapter.NewTextStatsAction(textStatsProvider())
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := moduleapi.NewActionRequestV1(
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   "other.action",
			ProviderActionID: "other.action",
			DefinitionDigest: strings.Repeat("b", 64),
			CanonicalInput:   json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Prepare(
		context.Background(),
		request,
	); !errors.Is(err, exactadapter.ErrTextStatsPrepare) {
		t.Fatalf("Prepare error=%v", err)
	}
}

func textStatsProvider() moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           "tool.text_stats",
		Version:            "v1",
		ArtifactDigest:     strings.Repeat("d", 64),
		InstanceID:         "text-stats-local",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "builtin.action.text_stats",
		ActivationRevision: 1,
	}
}

func textStatsExecution(
	t *testing.T,
	provider moduleapi.ActivatedModuleRef,
	request moduleapi.ActionExecutionRequestV1,
) modulehost.PreparedActionExecutionV1 {
	t.Helper()
	_, configCanonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   request.PublicActionID,
				ProviderActionID: request.ProviderActionID,
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   request.MaxResultBytes,
			}},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 "text-stats-test-tenant",
			AllowedWorkspaceIDs:      []string{"*"},
			AllowedProviderActionIDs: []string{request.ProviderActionID},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           request.MaxResultBytes,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := modulehost.NewPreparedActionExecutionV1(
		modulehost.PreparedActionExecutionV1{
			Request: request,
			Binding: moduleapi.PortBinding{
				Provider:            provider,
				ConfigRef:           textStatsContentDigest("CONFIG", configCanonical),
				AuthorityCeilingRef: textStatsContentDigest("AUTHORITY_CEILING", authorityCanonical),
				FailurePolicy:       moduleapi.FailureRequired,
			},
			ConfigCanonical:    configCanonical,
			AuthorityCanonical: authorityCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return execution
}

func textStatsContentDigest(kind string, canonical []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("freeagent.content-record/v1\x00"))
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte("application/json"))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonical)
	return hex.EncodeToString(digest.Sum(nil))
}
