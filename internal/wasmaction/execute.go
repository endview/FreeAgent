package wasmaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tetratelabs/wazero"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	MaxExecutionDurationV1   = 5 * time.Second
	MaxConcurrentInstancesV1 = 4

	UsageReceiptSchemaV1 = "wasm-action-usage-receipt/v1"
	EngineIdentityV1     = "wazero-interpreter/v1.12.0"

	failedClosureRejected  = "WASM_ACTION_CLOSURE_REJECTED"
	failedProviderMismatch = "WASM_ACTION_PROVIDER_MISMATCH"
	failedPreparedRejected = "WASM_ACTION_PREPARED_PAYLOAD_REJECTED"
	failedModuleRejected   = "WASM_ACTION_MODULE_REJECTED"
	failedCompileRejected  = "WASM_ACTION_COMPILE_REJECTED"
	failedInstanceRejected = "WASM_ACTION_INSTANCE_REJECTED"
	failedABIRejected      = "WASM_ACTION_ABI_REJECTED"
	failedGuestTrap        = "WASM_ACTION_GUEST_TRAP"
	failedDeadline         = "WASM_ACTION_DEADLINE_EXCEEDED"
	failedCancelled        = "WASM_ACTION_CANCELLED"
	failedOutputRejected   = "WASM_ACTION_OUTPUT_REJECTED"
)

// guestInstanceSlotsV1 is the one process-wide execution gate shared by all
// Adapters. A slot is held until the runtime, compiled module and instance are
// all closed; it is deliberately not configurable by a module or Binding.
var guestInstanceSlotsV1 = make(chan struct{}, MaxConcurrentInstancesV1)

// UsageReceiptV1 contains only Host-observed local resource facts. In
// particular it contains no token, price, cost, guest diagnostic or raw trap.
type UsageReceiptV1 struct {
	SchemaVersion       string `json:"schema_version"`
	EngineIdentity      string `json:"engine_identity"`
	ABIVersion          string `json:"abi_version"`
	InputBytes          uint32 `json:"input_bytes"`
	OutputBytes         uint32 `json:"output_bytes"`
	ObservedMemoryPages uint32 `json:"observed_memory_pages"`
	ElapsedMS           uint64 `json:"elapsed_ms"`
	InstructionMetering string `json:"instruction_metering"`
}

type executionObservationV1 struct {
	inputBytes  uint32
	outputBytes uint32
	memoryPages uint32
	startedAt   time.Time
}

func newExecutionObservationV1(inputBytes int) executionObservationV1 {
	return executionObservationV1{
		inputBytes: uint32(inputBytes),
		startedAt:  time.Now(),
	}
}

// ExecutePrepared is the only guest execution entry point. The Action Gateway
// calls it after the original DispatchAttempt is durably PENDING and its
// one-shot permit has been consumed.
func (adapter *Adapter) ExecutePrepared(
	ctx context.Context,
	execution modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	if adapter == nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: Adapter is nil",
			ErrExecute,
		)
	}
	if ctx == nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: context is nil",
			ErrExecute,
		)
	}
	requestAttemptID := execution.Request.AttemptID
	request, requestCanonical, err := moduleapi.NewActionExecutionRequestV1(
		execution.Request,
	)
	if err != nil || len(requestCanonical) == 0 ||
		len(requestCanonical) > MaxInputBytesV1 {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: request identity is invalid",
			ErrExecute,
		)
	}
	observation := newExecutionObservationV1(len(requestCanonical))
	if err := ctx.Err(); err != nil {
		return failedExecutionV1(
			request.AttemptID,
			contextFailureClassificationV1(err),
			observation,
		)
	}
	frozenExecution, err := modulehost.NewPreparedActionExecutionV1(execution)
	if err != nil {
		return failedExecutionV1(
			requestAttemptID,
			failedClosureRejected,
			observation,
		)
	}
	provider := frozenExecution.Binding.Provider
	if !adapter.provider.matches(provider) {
		return failedExecutionV1(
			request.AttemptID,
			failedProviderMismatch,
			observation,
		)
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(
		frozenExecution.ConfigCanonical,
	)
	if err != nil || !bytes.Equal(config.Parameters, emptyParametersV1) ||
		!configClosesPureActionV1(config, request) {
		return failedExecutionV1(
			request.AttemptID,
			failedClosureRejected,
			observation,
		)
	}
	definition, found := adapter.definitionByProviderID[request.ProviderActionID]
	if !found || definition.RequestedEffectClass != moduleapi.EffectNone ||
		moduleapi.ValidateActionInputV1(
			definition.InputSchema,
			request.PreparedPayload,
		) != nil {
		return failedExecutionV1(
			request.AttemptID,
			failedPreparedRejected,
			observation,
		)
	}

	output, classification := adapter.executeGuestV1(
		ctx,
		requestCanonical,
		request.MaxResultBytes,
		&observation,
	)
	if classification != "" {
		return failedExecutionV1(
			request.AttemptID,
			classification,
			observation,
		)
	}
	receipt, err := canonicalUsageReceiptV1(observation)
	if err != nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: usage receipt",
			ErrExecute,
		)
	}
	result, _, err := moduleapi.NewActionExecutionResultV1(
		moduleapi.ActionExecutionResultV1{
			SchemaVersion:   moduleapi.ActionExecutionResultSchemaV1,
			AttemptID:       request.AttemptID,
			Outcome:         moduleapi.ActionExecutionSucceeded,
			CanonicalResult: output,
			ProviderReceipt: receipt,
		},
	)
	if err != nil {
		return failedExecutionV1(
			request.AttemptID,
			failedOutputRejected,
			observation,
		)
	}
	return result, nil
}

func (adapter *Adapter) executeGuestV1(
	ctx context.Context,
	input []byte,
	maxResultBytes uint32,
	observation *executionObservationV1,
) (json.RawMessage, string) {
	callContext, cancel := context.WithTimeout(ctx, MaxExecutionDurationV1)
	defer cancel()
	if err := callContext.Err(); err != nil {
		return nil, contextFailureClassificationV1(err)
	}
	if !acquireGuestInstanceSlotV1(callContext) {
		return nil, contextFailureClassificationV1(callContext.Err())
	}
	defer releaseGuestInstanceSlotV1()
	if err := inspectModuleSectionsV1(callContext, adapter.wasm); err != nil {
		if callErr := callContext.Err(); callErr != nil {
			return nil, contextFailureClassificationV1(callErr)
		}
		return nil, failedModuleRejected
	}

	runtime := wazero.NewRuntimeWithConfig(
		callContext,
		newRuntimeConfigV1(),
	)
	defer runtime.Close(context.Background())
	compiled, err := runtime.CompileModule(callContext, bytes.Clone(adapter.wasm))
	if err != nil {
		if callErr := callContext.Err(); callErr != nil {
			return nil, contextFailureClassificationV1(callErr)
		}
		return nil, failedCompileRejected
	}
	defer compiled.Close(context.Background())
	if err := validateCompiledModuleV1(compiled); err != nil {
		return nil, failedModuleRejected
	}
	instance, err := runtime.InstantiateModule(
		callContext,
		compiled,
		wazero.NewModuleConfig().WithName("").WithStartFunctions(),
	)
	if err != nil {
		if callErr := callContext.Err(); callErr != nil {
			return nil, contextFailureClassificationV1(callErr)
		}
		return nil, failedInstanceRejected
	}
	defer instance.Close(context.Background())

	memory := instance.ExportedMemory(MemoryExportV1)
	allocate := instance.ExportedFunction(AllocateExportV1)
	execute := instance.ExportedFunction(ExecuteExportV1)
	if memory == nil || allocate == nil || execute == nil {
		return nil, failedABIRejected
	}
	if pages, ok := memory.Grow(0); ok {
		observation.memoryPages = pages
	}
	allocated, err := allocate.Call(callContext, uint64(len(input)))
	if err != nil {
		if callErr := callContext.Err(); callErr != nil {
			return nil, contextFailureClassificationV1(callErr)
		}
		return nil, failedGuestTrap
	}
	if len(allocated) != 1 {
		return nil, failedABIRejected
	}
	inputPointer := uint32(allocated[0])
	if inputPointer == 0 || !memory.Write(inputPointer, input) {
		return nil, failedABIRejected
	}
	executed, err := execute.Call(
		callContext,
		uint64(inputPointer),
		uint64(len(input)),
	)
	if err != nil {
		if callErr := callContext.Err(); callErr != nil {
			return nil, contextFailureClassificationV1(callErr)
		}
		return nil, failedGuestTrap
	}
	if len(executed) != 1 {
		return nil, failedABIRejected
	}
	packed := executed[0]
	outputPointer := uint32(packed >> 32)
	outputLength := uint32(packed)
	observation.outputBytes = outputLength
	if pages, ok := memory.Grow(0); ok {
		observation.memoryPages = pages
	}
	if outputPointer == 0 || outputLength == 0 ||
		outputLength > MaxOutputBytesV1 ||
		outputLength > maxResultBytes {
		return nil, failedOutputRejected
	}
	view, ok := memory.Read(outputPointer, outputLength)
	if !ok {
		return nil, failedABIRejected
	}
	return bytes.Clone(view), ""
}

func acquireGuestInstanceSlotV1(ctx context.Context) bool {
	select {
	case guestInstanceSlotsV1 <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func releaseGuestInstanceSlotV1() {
	<-guestInstanceSlotsV1
}

func configClosesPureActionV1(
	config moduleapi.ActionBindingConfigV1,
	request moduleapi.ActionExecutionRequestV1,
) bool {
	for _, mapping := range config.Actions {
		if mapping.PublicActionID == request.PublicActionID {
			return mapping.ProviderActionID == request.ProviderActionID &&
				mapping.LocalEffectClass == moduleapi.EffectNone &&
				request.MaxResultBytes <= mapping.MaxResultBytes
		}
	}
	return false
}

func contextFailureClassificationV1(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return failedDeadline
	}
	return failedCancelled
}

func failedExecutionV1(
	attemptID string,
	classification string,
	observation executionObservationV1,
) (moduleapi.ActionExecutionResultV1, error) {
	receipt, err := canonicalUsageReceiptV1(observation)
	if err != nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: usage receipt",
			ErrExecute,
		)
	}
	result, _, err := moduleapi.NewActionExecutionResultV1(
		moduleapi.ActionExecutionResultV1{
			SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
			AttemptID:           attemptID,
			Outcome:             moduleapi.ActionExecutionFailed,
			ProviderReceipt:     receipt,
			ErrorClassification: classification,
		},
	)
	return result, err
}

func canonicalUsageReceiptV1(
	observation executionObservationV1,
) (json.RawMessage, error) {
	elapsed := time.Since(observation.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	receipt := UsageReceiptV1{
		SchemaVersion:       UsageReceiptSchemaV1,
		EngineIdentity:      EngineIdentityV1,
		ABIVersion:          ABIVersionV1,
		InputBytes:          observation.inputBytes,
		OutputBytes:         observation.outputBytes,
		ObservedMemoryPages: observation.memoryPages,
		ElapsedMS:           uint64(elapsed / time.Millisecond),
		InstructionMetering: "UNSUPPORTED",
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: moduleapi.MaxActionReceiptBytesV1,
			MaxDepth: 16,
			MaxNodes: 64,
		},
	)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(canonical), nil
}
