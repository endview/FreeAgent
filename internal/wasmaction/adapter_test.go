package wasmaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestDescribePrepareAreOfflineAndGenericInvokeIsForbidden(t *testing.T) {
	adapter, _ := testAdapter(t, testWASMModule(testWASMOptions{}))
	describe := moduleapi.ActionDescribeRequestV1{
		SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
		Parameters:    json.RawMessage(`{}`),
	}
	definitions, err := adapter.Describe(context.Background(), describe)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("Describe definitions=%+v error=%v", definitions, err)
	}
	definitions[0].InputSchema[0] = '['
	again, err := adapter.Describe(context.Background(), describe)
	if err != nil || len(again) != 1 || again[0].InputSchema[0] != '{' {
		t.Fatalf("Describe leaked caller mutation: definitions=%+v error=%v", again, err)
	}
	for _, parameters := range []json.RawMessage{
		nil,
		json.RawMessage(`{ }`),
		json.RawMessage(`{"extra":true}`),
	} {
		request := describe
		request.Parameters = parameters
		if _, err := adapter.Describe(
			context.Background(),
			request,
		); !errors.Is(err, ErrDescribe) {
			t.Fatalf("Describe parameters=%q error=%v, want ErrDescribe", parameters, err)
		}
	}

	prepared, err := adapter.Prepare(
		context.Background(),
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   "example.echo",
			ProviderActionID: "example.echo",
			DefinitionDigest: strings.Repeat("d", 64),
			CanonicalInput:   json.RawMessage(`{"value":"hello"}`),
		},
	)
	if err != nil || !bytes.Equal(prepared, []byte(`{"value":"hello"}`)) {
		t.Fatalf("Prepare payload=%s error=%v", prepared, err)
	}
	if _, err := adapter.Invoke(
		context.Background(),
		modulehost.PreparedInvocation{},
	); !errors.Is(err, ErrGenericInvocation) {
		t.Fatalf("generic Invoke error=%v, want ErrGenericInvocation", err)
	}
}

func TestExecutePreparedSucceedsWithCanonicalHostUsageReceipt(t *testing.T) {
	output := []byte(`{"ok":true}`)
	adapter, provider := testAdapter(t, testWASMModule(testWASMOptions{
		output: output,
	}))
	execution := testPreparedExecution(t, provider, "attempt-success")
	_, requestCanonical, err := moduleapi.NewActionExecutionRequestV1(
		execution.Request,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.ExecutePrepared(context.Background(), execution)
	if err != nil {
		t.Fatalf("ExecutePrepared: %v", err)
	}
	if result.Outcome != moduleapi.ActionExecutionSucceeded ||
		result.AttemptID != execution.Request.AttemptID ||
		!bytes.Equal(result.CanonicalResult, output) ||
		result.ExternalOperationID != "" ||
		result.ErrorClassification != "" ||
		result.UnknownReason != "" {
		t.Fatalf("execution result = %+v", result)
	}
	canonical, err := moduleapi.CanonicalJSON(result.ProviderReceipt)
	if err != nil || !bytes.Equal(canonical, result.ProviderReceipt) {
		t.Fatalf("ProviderReceipt is not canonical: %s error=%v", result.ProviderReceipt, err)
	}
	var receipt UsageReceiptV1
	if err := json.Unmarshal(result.ProviderReceipt, &receipt); err != nil {
		t.Fatalf("decode ProviderReceipt: %v", err)
	}
	if receipt.SchemaVersion != UsageReceiptSchemaV1 ||
		receipt.EngineIdentity != EngineIdentityV1 ||
		receipt.ABIVersion != ABIVersionV1 ||
		receipt.InputBytes != uint32(len(requestCanonical)) ||
		receipt.OutputBytes != uint32(len(output)) ||
		receipt.ObservedMemoryPages != 1 ||
		receipt.InstructionMetering != "UNSUPPORTED" {
		t.Fatalf("usage receipt = %+v", receipt)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result.ProviderReceipt, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 8 || fields["token_count"] != nil ||
		fields["cost"] != nil || fields["price"] != nil {
		t.Fatalf("usage receipt fields = %v", fields)
	}
}

func TestAdapterReusesExactArtifactAcrossActivatedInstances(t *testing.T) {
	adapter, first := testAdapter(t, testWASMModule(testWASMOptions{
		output: []byte(`{"ok":true}`),
	}))
	second := first
	second.InstanceID = "instance-wasm-secondary"
	second.ActivationRevision = 7

	result, err := adapter.ExecutePrepared(
		context.Background(),
		testPreparedExecution(t, second, "attempt-secondary-instance"),
	)
	if err != nil || result.Outcome != moduleapi.ActionExecutionSucceeded {
		t.Fatalf("second Instance result=%+v error=%v", result, err)
	}

	tests := []struct {
		name   string
		mutate func(*moduleapi.ActivatedModuleRef)
	}{
		{name: "module", mutate: func(provider *moduleapi.ActivatedModuleRef) {
			provider.ModuleID = "other.wasm.action"
		}},
		{name: "version", mutate: func(provider *moduleapi.ActivatedModuleRef) {
			provider.Version = "2.0.0"
		}},
		{name: "artifact", mutate: func(provider *moduleapi.ActivatedModuleRef) {
			provider.ArtifactDigest = strings.Repeat("b", 64)
		}},
		{name: "execution class", mutate: func(provider *moduleapi.ActivatedModuleRef) {
			provider.ExecutionClass = moduleapi.ExecutionTrustedInProcess
		}},
		{name: "adapter", mutate: func(provider *moduleapi.ActivatedModuleRef) {
			provider.AdapterIdentity = "freeagent.adapter.action.other-wasm/v1"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mismatched := first
			test.mutate(&mismatched)
			result, err := adapter.ExecutePrepared(
				context.Background(),
				testPreparedExecution(
					t,
					mismatched,
					"attempt-mismatch-"+strings.ReplaceAll(test.name, " ", "-"),
				),
			)
			if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
				result.ErrorClassification != failedProviderMismatch {
				t.Fatalf("mismatch result=%+v error=%v", result, err)
			}
		})
	}
}

func TestExecutePreparedUsesFreshInstanceEveryTime(t *testing.T) {
	const outputPointer = uint32(2048)
	instructions := []byte{0x41}
	instructions = appendVarInt64(instructions, int64(outputPointer))
	instructions = append(instructions, 0x41)
	instructions = appendVarInt64(instructions, int64(outputPointer))
	instructions = append(instructions,
		0x2d, 0x00, 0x00, // i32.load8_u align=0 offset=0
		0x41, 0x01, // i32.const 1
		0x6a,             // i32.add
		0x3a, 0x00, 0x00, // i32.store8 align=0 offset=0
	)
	packed := uint64(outputPointer)<<32 | 1
	instructions = appendVarInt64(
		append(instructions, 0x42),
		int64(packed),
	)
	adapter, provider := testAdapter(t, testWASMModule(testWASMOptions{
		executeInstructions: instructions,
		outputPointer:       outputPointer,
		output:              []byte(`0`),
	}))
	for _, attemptID := range []string{"attempt-fresh-one", "attempt-fresh-two"} {
		result, err := adapter.ExecutePrepared(
			context.Background(),
			testPreparedExecution(t, provider, attemptID),
		)
		if err != nil || result.Outcome != moduleapi.ActionExecutionSucceeded ||
			!bytes.Equal(result.CanonicalResult, []byte(`1`)) {
			t.Fatalf("attempt=%s result=%+v error=%v", attemptID, result, err)
		}
	}
}

func TestInfiniteLoopCancellationReturnsFixedFailedResult(t *testing.T) {
	adapter, provider := testAdapter(t, testInfiniteLoopWASM())
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	started := time.Now()
	result, err := adapter.ExecutePrepared(
		ctx,
		testPreparedExecution(t, provider, "attempt-cancelled"),
	)
	if err != nil {
		t.Fatalf("ExecutePrepared returned Go error: %v", err)
	}
	if result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != failedCancelled ||
		len(result.CanonicalResult) != 0 {
		t.Fatalf("cancelled execution result = %+v", result)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("cancelled guest did not stop promptly: %s", time.Since(started))
	}
	if bytes.Contains(result.ProviderReceipt, []byte("loop")) ||
		bytes.Contains(result.ProviderReceipt, []byte("trap")) {
		t.Fatalf("ProviderReceipt persisted a guest diagnostic: %s", result.ProviderReceipt)
	}
}

func TestProcessWideGuestLimitIsSharedAcrossAdaptersAndWaitIsCancellable(t *testing.T) {
	if cap(guestInstanceSlotsV1) != MaxConcurrentInstancesV1 ||
		len(guestInstanceSlotsV1) != 0 {
		t.Fatalf(
			"guest instance gate capacity=%d occupancy=%d",
			cap(guestInstanceSlotsV1),
			len(guestInstanceSlotsV1),
		)
	}
	adapterOne, provider := testAdapter(t, testInfiniteLoopWASM())
	adapterTwo, _ := testAdapter(t, testInfiniteLoopWASM())
	adapters := []*Adapter{adapterOne, adapterTwo, adapterOne, adapterTwo}
	type observedResult struct {
		result moduleapi.ActionExecutionResultV1
		err    error
	}
	results := make(chan observedResult, MaxConcurrentInstancesV1)
	cancels := make([]context.CancelFunc, 0, MaxConcurrentInstancesV1)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for index := 0; index < MaxConcurrentInstancesV1; index++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		execution := testPreparedExecution(
			t,
			provider,
			fmt.Sprintf("attempt-slot-%d", index),
		)
		adapter := adapters[index]
		go func() {
			result, err := adapter.ExecutePrepared(ctx, execution)
			results <- observedResult{result: result, err: err}
		}()
	}
	if !waitForGuestSlotOccupancyV1(MaxConcurrentInstancesV1, time.Second) {
		for _, cancel := range cancels {
			cancel()
		}
		t.Fatalf("guest slots never reached %d", MaxConcurrentInstancesV1)
	}

	waitingContext, cancelWaiting := context.WithCancel(context.Background())
	defer cancelWaiting()
	waitingExecution := testPreparedExecution(t, provider, "attempt-slot-waiting")
	waitingResult := make(chan observedResult, 1)
	waitingStarted := make(chan struct{})
	go func() {
		close(waitingStarted)
		result, err := adapterOne.ExecutePrepared(waitingContext, waitingExecution)
		waitingResult <- observedResult{result: result, err: err}
	}()
	<-waitingStarted
	time.Sleep(20 * time.Millisecond)
	if got := len(guestInstanceSlotsV1); got != MaxConcurrentInstancesV1 {
		cancelWaiting()
		for _, cancel := range cancels {
			cancel()
		}
		t.Fatalf("waiting execution changed slot occupancy to %d", got)
	}
	cancelWaiting()
	select {
	case observed := <-waitingResult:
		if observed.err != nil ||
			observed.result.Outcome != moduleapi.ActionExecutionFailed ||
			observed.result.ErrorClassification != failedCancelled {
			t.Fatalf("waiting result=%+v error=%v", observed.result, observed.err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting execution did not honor cancellation")
	}
	if got := len(guestInstanceSlotsV1); got != MaxConcurrentInstancesV1 {
		t.Fatalf("cancelled waiter released another execution's slot: %d", got)
	}

	for _, cancel := range cancels {
		cancel()
	}
	for index := 0; index < MaxConcurrentInstancesV1; index++ {
		select {
		case observed := <-results:
			if observed.err != nil ||
				observed.result.Outcome != moduleapi.ActionExecutionFailed ||
				observed.result.ErrorClassification != failedCancelled {
				t.Fatalf("occupied result=%+v error=%v", observed.result, observed.err)
			}
		case <-time.After(time.Second):
			t.Fatal("occupied execution did not stop after cancellation")
		}
	}
	if !waitForGuestSlotOccupancyV1(0, time.Second) {
		t.Fatalf("guest slots leaked: %d", len(guestInstanceSlotsV1))
	}
}

func TestGuestTrapReturnsSanitizedFixedFailedResult(t *testing.T) {
	const outputPointer = uint32(2048)
	instructions := []byte{0x00, 0x42} // unreachable; i64.const
	packed := uint64(outputPointer)<<32 | 1
	instructions = appendVarInt64(instructions, int64(packed))
	adapter, provider := testAdapter(t, testWASMModule(testWASMOptions{
		executeInstructions: instructions,
		outputPointer:       outputPointer,
		output:              []byte(`0`),
	}))
	result, err := adapter.ExecutePrepared(
		context.Background(),
		testPreparedExecution(t, provider, "attempt-trap"),
	)
	if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != failedGuestTrap {
		t.Fatalf("trap result=%+v error=%v", result, err)
	}
	if bytes.Contains(result.ProviderReceipt, []byte("unreachable")) ||
		bytes.Contains(result.ProviderReceipt, []byte("trap")) {
		t.Fatalf("ProviderReceipt persisted a raw trap: %s", result.ProviderReceipt)
	}
}

func TestExecutePreparedMapsAlreadyUnavailableContextToFixedFailure(t *testing.T) {
	adapter, provider := testAdapter(t, testWASMModule(testWASMOptions{}))
	tests := []struct {
		name           string
		context        func() context.Context
		classification string
	}{
		{
			name: "cancelled",
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			classification: failedCancelled,
		},
		{
			name: "deadline",
			context: func() context.Context {
				ctx, cancel := context.WithDeadline(
					context.Background(),
					time.Now().Add(-time.Second),
				)
				cancel()
				return ctx
			},
			classification: failedDeadline,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := adapter.ExecutePrepared(
				test.context(),
				testPreparedExecution(t, provider, "attempt-context-"+test.name),
			)
			if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
				result.ErrorClassification != test.classification {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func TestContextFailureClassificationV1RecognizesWrappedDeadline(t *testing.T) {
	wrapped := fmt.Errorf("runtime stopped: %w", context.DeadlineExceeded)
	if got := contextFailureClassificationV1(wrapped); got != failedDeadline {
		t.Fatalf("classification=%q want=%q", got, failedDeadline)
	}
}

func testInfiniteLoopWASM() []byte {
	const outputPointer = uint32(2048)
	instructions := []byte{
		0x03, 0x40, // loop with empty block type
		0x0c, 0x00, // br 0
		0x0b, // end loop
		0x42, // i64.const, unreachable but required by the result type
	}
	packed := uint64(outputPointer)<<32 | 1
	instructions = appendVarInt64(instructions, int64(packed))
	return testWASMModule(testWASMOptions{
		executeInstructions: instructions,
		outputPointer:       outputPointer,
		output:              []byte(`0`),
	})
}

func waitForGuestSlotOccupancyV1(want int, maximum time.Duration) bool {
	deadline := time.Now().Add(maximum)
	for time.Now().Before(deadline) {
		if len(guestInstanceSlotsV1) == want {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return len(guestInstanceSlotsV1) == want
}

func testAdapter(
	t *testing.T,
	wasm []byte,
) (*Adapter, moduleapi.ActivatedModuleRef) {
	t.Helper()
	descriptor, _ := testDescriptor(t)
	provider := testProvider(strings.Repeat("a", 64))
	adapter, err := New(context.Background(), provider, descriptor, wasm)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return adapter, provider
}

func testDescriptor(t *testing.T) (DescriptorV1, []byte) {
	t.Helper()
	schema, err := moduleapi.CanonicalJSON([]byte(
		`{"additionalProperties":false,"properties":{"value":{"maxLength":4096,"type":"string"}},"required":["value"],"type":"object"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	descriptor, canonical, err := NewDescriptorV1(DescriptorV1{
		SchemaVersion: DescriptorSchemaV1,
		ABIVersion:    ABIVersionV1,
		ModulePath:    "content/action.wasm",
		Actions: []moduleapi.ActionDefinitionV1{{
			ProviderActionID:        "example.echo",
			Description:             "Return one deterministic local value.",
			InputSchema:             schema,
			RequestedEffectClass:    moduleapi.EffectNone,
			RequestedMaxResultBytes: 256,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return descriptor, canonical
}

func testProvider(digest string) moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           "example.wasm.action",
		Version:            "1.0.0",
		ArtifactDigest:     digest,
		InstanceID:         "instance-wasm",
		ExecutionClass:     moduleapi.ExecutionWASM,
		AdapterIdentity:    AdapterIdentityV1,
		ActivationRevision: 1,
	}
}

func testPreparedExecution(
	t *testing.T,
	provider moduleapi.ActivatedModuleRef,
	attemptID string,
) modulehost.PreparedActionExecutionV1 {
	t.Helper()
	_, configCanonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "example.echo",
				ProviderActionID: "example.echo",
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   256,
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
			TenantID:                 "tenant-one",
			AllowedWorkspaceIDs:      []string{"workspace-one"},
			AllowedProviderActionIDs: []string{"example.echo"},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           256,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := moduleapi.NewActionExecutionRequestV1(
		moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        attemptID,
			PublicActionID:   "example.echo",
			ProviderActionID: "example.echo",
			DefinitionDigest: strings.Repeat("d", 64),
			MaxResultBytes:   256,
			PreparedPayload:  json.RawMessage(`{"value":"hello"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return modulehost.PreparedActionExecutionV1{
		Request: request,
		Binding: moduleapi.PortBinding{
			Provider:            provider,
			ConfigRef:           testContentRef("CONFIG", configCanonical),
			AuthorityCeilingRef: testContentRef("AUTHORITY_CEILING", authorityCanonical),
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		},
		ConfigCanonical:    configCanonical,
		AuthorityCanonical: authorityCanonical,
	}
}

func testContentRef(kind string, canonical []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("freeagent.content-record/v1\x00"))
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte("application/json"))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonical)
	return hex.EncodeToString(digest.Sum(nil))
}
