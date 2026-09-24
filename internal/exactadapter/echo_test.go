package exactadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestDeterministicEchoReturnsCanonicalOutputWithUnknownUsage(t *testing.T) {
	provider := echoProvider()
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatalf("NewDeterministicEcho: %v", err)
	}
	prepared := echoPreparedInvocation(t, provider)

	result, err := echo.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if result.Provider != provider ||
		result.Outcome != modulehost.InvocationSucceeded {
		t.Fatalf("result identity/outcome=%+v", result)
	}
	const legacyNoActionOutput = `{"assistant_text":"hello from the user","schema_version":"model-generate-output/v1"}`
	if string(result.Output) != legacyNoActionOutput {
		t.Fatalf("no-Action Echo output bytes changed: %s", result.Output)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(result.Output)
	if err != nil {
		t.Fatalf("RestoreModelGenerateOutputV1: %v", err)
	}
	if output.AssistantText != "hello from the user" ||
		output.ProviderRequestID != "" {
		t.Fatalf("output=%+v", output)
	}
	for _, forbidden := range []string{
		prepared.Invocation.InvocationID,
		prepared.Invocation.RunID,
		prepared.Invocation.MemberID,
		prepared.Invocation.Deadline.Format(time.RFC3339Nano),
	} {
		if bytes.Contains(result.Output, []byte(forbidden)) {
			t.Fatalf("output contains invocation metadata %q: %s", forbidden, result.Output)
		}
	}

	usage, err := moduleapi.RestoreModelUsageReceiptV2(result.UsageReceipt)
	if err != nil {
		t.Fatalf("RestoreModelUsageReceiptV2: %v", err)
	}
	if usage.InputTokens != nil ||
		usage.CachedInputTokens != nil ||
		usage.UncachedInputTokens != nil ||
		usage.OutputTokens != nil ||
		usage.ReasoningTokens != nil ||
		string(usage.RawReceipt) != "null" {
		t.Fatalf("Echo fabricated Usage: %+v", usage)
	}
}

func TestDeterministicEchoRequestsTextStatsOnceThenReturnsFinal(t *testing.T) {
	provider := echoProvider()
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatal(err)
	}
	actionAdapter, err := exactadapter.NewTextStatsAction(textStatsProvider())
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
	definitions, err := actionAdapter.Describe(
		context.Background(),
		describeRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	actions := []moduleapi.ModelActionDefinitionV1{
		{
			ActionID:    exactadapter.TextStatsActionIDV1,
			Description: definitions[0].Description,
			InputSchema: bytes.Clone(definitions[0].InputSchema),
		},
	}
	prepared := echoPreparedInvocation(t, provider)
	firstRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		prepared.Invocation.Input,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstRequest.Messages[len(firstRequest.Messages)-1].Content = "Count this text"
	firstRequest.Actions = actions
	_, firstRequestCanonical, err := moduleapi.NewModelGenerateRequestV1(
		firstRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Invocation.Input = firstRequestCanonical

	firstResult, err := echo.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	firstOutput, err := moduleapi.RestoreModelGenerateOutputV1(
		firstResult.Output,
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstOutput.AssistantText != "" || firstOutput.ActionRequest == nil ||
		firstOutput.ActionRequest.ActionID != exactadapter.TextStatsActionIDV1 ||
		string(firstOutput.ActionRequest.CanonicalInput) !=
			`{"text":"Count this text"}` {
		t.Fatalf("first output=%+v", firstOutput)
	}

	frozenDefinition, _, err := corecontract.NewFrozenActionDefinitionV1(
		corecontract.FrozenActionDefinitionV1{
			PublicActionID:   exactadapter.TextStatsActionIDV1,
			ProviderActionID: exactadapter.TextStatsActionIDV1,
			BindingIndex:     0,
			Description:      definitions[0].Description,
			InputSchema:      definitions[0].InputSchema,
			EffectClass:      moduleapi.EffectNone,
			MaxResultBytes:   exactadapter.TextStatsMaxResultBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	providerRequest, _, err := moduleapi.NewActionRequestV1(
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   frozenDefinition.PublicActionID,
			ProviderActionID: frozenDefinition.ProviderActionID,
			DefinitionDigest: frozenDefinition.DefinitionDigest,
			CanonicalInput:   firstOutput.ActionRequest.CanonicalInput,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	preparedPayload, err := actionAdapter.Prepare(
		context.Background(),
		providerRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	executionRequest, _, err := moduleapi.NewActionExecutionRequestV1(
		moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        "action-attempt-echo-chain",
			PublicActionID:   frozenDefinition.PublicActionID,
			ProviderActionID: frozenDefinition.ProviderActionID,
			DefinitionDigest: frozenDefinition.DefinitionDigest,
			MaxResultBytes:   frozenDefinition.MaxResultBytes,
			PreparedPayload:  preparedPayload,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executionResult, err := actionAdapter.ExecutePrepared(
		context.Background(),
		textStatsExecution(t, textStatsProvider(), executionRequest),
	)
	if err != nil {
		t.Fatal(err)
	}
	actionResult, _, _, err := corecontract.NewAvailableActionResultV1(
		frozenDefinition,
		executionResult.CanonicalResult,
	)
	if err != nil {
		t.Fatal(err)
	}
	resultMessage, err := corecontract.BuildUntrustedActionResultEnvelopeV1(
		actionResult,
		frozenDefinition,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondRequest := firstRequest
	secondRequest.Messages = append(
		append([]moduleapi.ModelMessageV1(nil), firstRequest.Messages...),
		resultMessage,
	)
	_, secondRequestCanonical, err := moduleapi.NewModelGenerateRequestV1(
		secondRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Invocation.Input = secondRequestCanonical
	secondResult, err := echo.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	secondOutput, err := moduleapi.RestoreModelGenerateOutputV1(
		secondResult.Output,
	)
	if err != nil {
		t.Fatal(err)
	}
	if secondOutput.ActionRequest != nil ||
		secondOutput.AssistantText !=
			exactadapter.TextStatsCompletedAssistantTextV1 {
		t.Fatalf("second output requested another Action: %+v", secondOutput)
	}
}

func TestDeterministicEchoRejectsInvalidOrUnsupportedProvider(t *testing.T) {
	tests := []moduleapi.ActivatedModuleRef{
		{},
		func() moduleapi.ActivatedModuleRef {
			provider := echoProvider()
			provider.ExecutionClass = moduleapi.ExecutionRemote
			return provider
		}(),
	}
	for _, provider := range tests {
		if _, err := exactadapter.NewDeterministicEcho(
			provider,
		); !errors.Is(err, exactadapter.ErrInvalidEchoProvider) {
			t.Fatalf("provider=%+v error=%v", provider, err)
		}
	}
}

func TestDeterministicEchoRejectsWrongPortProviderAndTamperedInput(
	t *testing.T,
) {
	provider := echoProvider()
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatal(err)
	}
	valid := echoPreparedInvocation(t, provider)

	tests := []struct {
		name   string
		mutate func(*modulehost.PreparedInvocation)
	}{
		{
			name: "wrong Port",
			mutate: func(value *modulehost.PreparedInvocation) {
				value.Invocation.Port = moduleapi.PortRef{
					Name:         moduleapi.PortNameContextProvide,
					ExactVersion: moduleapi.PortVersionV1,
				}
			},
		},
		{
			name: "different frozen Provider",
			mutate: func(value *modulehost.PreparedInvocation) {
				value.Binding.Provider.ModuleID = "model.other"
			},
		},
		{
			name: "non canonical request",
			mutate: func(value *modulehost.PreparedInvocation) {
				value.Invocation.Input = append(value.Invocation.Input, ' ')
			},
		},
		{
			name: "unknown request field",
			mutate: func(value *modulehost.PreparedInvocation) {
				var document map[string]any
				if err := json.Unmarshal(value.Invocation.Input, &document); err != nil {
					t.Fatal(err)
				}
				document["attempt_id"] = "attempt-must-not-enter-request"
				encoded, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				canonical, err := moduleapi.CanonicalJSON(encoded)
				if err != nil {
					t.Fatal(err)
				}
				value.Invocation.Input = canonical
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared := clonePrepared(valid)
			test.mutate(&prepared)
			if _, err := echo.Invoke(
				context.Background(),
				prepared,
			); !errors.Is(err, exactadapter.ErrEchoInvocation) {
				t.Fatalf("Invoke error=%v", err)
			}
		})
	}
}

func TestDeterministicEchoSharesArtifactAdapterAcrossInstances(t *testing.T) {
	first := echoProvider()
	echo, err := exactadapter.NewDeterministicEcho(first)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.InstanceID = "echo-secondary"
	second.ActivationRevision = 9

	for _, provider := range []moduleapi.ActivatedModuleRef{first, second} {
		result, err := echo.Invoke(
			context.Background(),
			echoPreparedInvocation(t, provider),
		)
		if err != nil {
			t.Fatalf("Invoke(%s) error = %v", provider.InstanceID, err)
		}
		if result.Provider != provider {
			t.Fatalf(
				"Invoke(%s) Provider = %+v",
				provider.InstanceID,
				result.Provider,
			)
		}
	}
}

func TestInvocationHostCallsRegisteredEchoExactlyOnce(t *testing.T) {
	provider := echoProvider()
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatal(err)
	}
	counter := &countingInvoker{delegate: echo}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         counter,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared := echoPreparedInvocation(t, provider)
	gate := &fixedGate{prepared: prepared}
	host, err := modulehost.NewInvocationHost(gate, registry)
	if err != nil {
		t.Fatal(err)
	}

	result, err := host.Invoke(
		context.Background(),
		prepared.Invocation,
	)
	if err != nil {
		t.Fatalf("InvocationHost.Invoke: %v", err)
	}
	if gate.calls != 1 || counter.calls != 1 {
		t.Fatalf("gate/Echo calls=%d/%d want 1/1", gate.calls, counter.calls)
	}
	if result.Provider != provider ||
		result.Outcome != modulehost.InvocationSucceeded {
		t.Fatalf("result=%+v", result)
	}
}

func TestDeterministicEchoReturnsDefensiveResultCopies(t *testing.T) {
	provider := echoProvider()
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatal(err)
	}
	prepared := echoPreparedInvocation(t, provider)
	first, err := echo.Invoke(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput := bytes.Clone(first.Output)
	wantUsage := bytes.Clone(first.UsageReceipt)
	first.Output[0] ^= 0xff
	first.UsageReceipt[0] ^= 0xff
	prepared.Invocation.Input[0] ^= 0xff

	secondPrepared := echoPreparedInvocation(t, provider)
	second, err := echo.Invoke(context.Background(), secondPrepared)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second.Output, wantOutput) ||
		!bytes.Equal(second.UsageReceipt, wantUsage) {
		t.Fatalf(
			"second result changed:\noutput=%s\nusage=%s",
			second.Output,
			second.UsageReceipt,
		)
	}
}

func echoProvider() moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           "model.echo",
		Version:            "v1",
		ArtifactDigest:     strings.Repeat("e", 64),
		InstanceID:         "echo-local",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "builtin.model.echo",
		ActivationRevision: 1,
	}
}

func echoPreparedInvocation(
	t *testing.T,
	provider moduleapi.ActivatedModuleRef,
) modulehost.PreparedInvocation {
	t.Helper()
	_, request, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{
				{
					Role:    moduleapi.ModelRoleSystem,
					Content: "stay deterministic",
				},
				{
					Role:    moduleapi.ModelRoleUser,
					Content: "hello from the user",
				},
			},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("NewModelGenerateRequestV1: %v", err)
	}
	invocation := modulehost.ModuleInvocation{
		InvocationID:         "invocation-secret-metadata",
		RunID:                "run-secret-metadata",
		MemberID:             "member-secret-metadata",
		MemberSnapshotDigest: strings.Repeat("a", 64),
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameModelGenerate,
			ExactVersion: moduleapi.PortVersionV2,
		},
		BindingIndex: 0,
		Input:        request,
		Deadline:     time.Now().Add(time.Hour).Round(0).UTC(),
	}
	return modulehost.PreparedInvocation{
		Invocation: invocation,
		Binding: moduleapi.PortBinding{
			Provider:            provider,
			ConfigRef:           strings.Repeat("b", 64),
			AuthorityCeilingRef: strings.Repeat("c", 64),
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		},
	}
}

func clonePrepared(
	prepared modulehost.PreparedInvocation,
) modulehost.PreparedInvocation {
	prepared.Invocation.Input = bytes.Clone(prepared.Invocation.Input)
	prepared.Binding.StaticContextRefs = append(
		[]string(nil),
		prepared.Binding.StaticContextRefs...,
	)
	return prepared
}

type fixedGate struct {
	calls    int
	prepared modulehost.PreparedInvocation
}

func (gate *fixedGate) ResolveAuthorized(
	_ context.Context,
	_ modulehost.ModuleInvocation,
) (modulehost.PreparedInvocation, error) {
	gate.calls++
	return clonePrepared(gate.prepared), nil
}

type countingInvoker struct {
	calls    int
	delegate modulehost.ModuleInvoker
}

func (invoker *countingInvoker) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	invoker.calls++
	return invoker.delegate.Invoke(ctx, prepared)
}
