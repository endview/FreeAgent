package wasmaction

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	MaxModuleBytesV1                 = 16 << 20
	MaxInputBytesV1                  = 128 << 10
	MaxOutputBytesV1                 = 32 << 10
	MaxInitialPagesV1                = 32
	MaxMemoryPagesV1                 = 256
	MaxTableElementsV1               = 64 << 10
	MaxTypesV1                       = 1024
	MaxFunctionsV1                   = 4096
	MaxGlobalsV1                     = 1024
	MaxDataSegmentsV1                = 1024
	MaxElementSegmentsV1             = 1024
	MaxFunctionParametersV1          = 64
	MaxFunctionResultsV1             = 1
	MaxLocalDeclarationsV1           = 256
	MaxFunctionLocalsV1              = 4096
	MaxAggregateLocalsV1             = 64 << 10
	MaxFunctionBodyBytesV1           = 256 << 10
	MaxCodeSectionBytesV1            = 4 << 20
	MaxCustomSectionsV1              = 64
	MaxCustomSectionNameBytesV1      = 256
	MaxExportNameBytesV1             = 64
	MaxBranchTableTargetsV1          = 4096
	MaxAggregateBranchTableTargetsV1 = 64 << 10
	MaxDataBytesV1                   = MaxInitialPagesV1 << 16
	MaxValidationDurationV1          = 5 * time.Second

	MemoryExportV1   = "memory"
	AllocateExportV1 = "freeagent_alloc_v1"
	ExecuteExportV1  = "freeagent_execute_v1"
)

var (
	ErrInvalidModule       = errors.New("wasmaction: invalid Wasm module")
	ErrValidationTimeoutV1 = errors.New("wasmaction: validation timeout")
)

var wasmMagicAndVersionV1 = []byte{
	0x00, 0x61, 0x73, 0x6d,
	0x01, 0x00, 0x00, 0x00,
}

// ValidateModuleV1 performs the complete no-execution validation used by
// Apply and the production Adapter constructor. No WASI or host module is
// registered, and the compiled module is closed before this function returns.
func ValidateModuleV1(ctx context.Context, wasm []byte) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidModule)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(wasm) == 0 || len(wasm) > MaxModuleBytesV1 {
		return fmt.Errorf(
			"%w: binary must contain between 1 and %d bytes",
			ErrInvalidModule,
			MaxModuleBytesV1,
		)
	}
	validationContext, cancel := context.WithTimeout(
		ctx,
		MaxValidationDurationV1,
	)
	defer cancel()
	if err := inspectModuleSectionsV1(validationContext, wasm); err != nil {
		return normalizeValidationFailureV1(ctx, validationContext, err)
	}

	runtime := wazero.NewRuntimeWithConfig(
		validationContext,
		newRuntimeConfigV1(),
	)
	defer runtime.Close(context.Background())
	compiled, err := runtime.CompileModule(
		validationContext,
		bytes.Clone(wasm),
	)
	if err != nil {
		if contextErr := normalizeValidationFailureV1(
			ctx,
			validationContext,
			nil,
		); contextErr != nil {
			return contextErr
		}
		return fmt.Errorf("%w: compile rejected", ErrInvalidModule)
	}
	defer compiled.Close(context.Background())
	if contextErr := normalizeValidationFailureV1(
		ctx,
		validationContext,
		nil,
	); contextErr != nil {
		return contextErr
	}
	validationErr := validateCompiledModuleV1(compiled)
	if contextErr := normalizeValidationFailureV1(
		ctx,
		validationContext,
		nil,
	); contextErr != nil {
		return contextErr
	}
	return validationErr
}

func normalizeValidationFailureV1(
	callerContext context.Context,
	validationContext context.Context,
	fallback error,
) error {
	if err := callerContext.Err(); err != nil {
		return err
	}
	if errors.Is(validationContext.Err(), context.DeadlineExceeded) {
		return fmt.Errorf(
			"%w: %w",
			ErrInvalidModule,
			ErrValidationTimeoutV1,
		)
	}
	return fallback
}

func newRuntimeConfigV1() wazero.RuntimeConfig {
	return wazero.NewRuntimeConfigInterpreter().
		WithCoreFeatures(api.CoreFeaturesV1).
		WithMemoryLimitPages(MaxMemoryPagesV1).
		WithCloseOnContextDone(true).
		WithDebugInfoEnabled(false).
		WithCustomSections(false)
}

func validateCompiledModuleV1(compiled wazero.CompiledModule) error {
	if compiled == nil {
		return fmt.Errorf("%w: compiled module is nil", ErrInvalidModule)
	}
	if len(compiled.ImportedFunctions()) != 0 ||
		len(compiled.ImportedMemories()) != 0 {
		return fmt.Errorf("%w: imports are forbidden", ErrInvalidModule)
	}
	functions := compiled.ExportedFunctions()
	if len(functions) != 2 {
		return fmt.Errorf(
			"%w: exactly two function exports are required",
			ErrInvalidModule,
		)
	}
	allocate, found := functions[AllocateExportV1]
	if !found || !sameValueTypes(allocate.ParamTypes(), api.ValueTypeI32) ||
		!sameValueTypes(allocate.ResultTypes(), api.ValueTypeI32) {
		return fmt.Errorf(
			"%w: freeagent_alloc_v1 signature is invalid",
			ErrInvalidModule,
		)
	}
	execute, found := functions[ExecuteExportV1]
	if !found || !sameValueTypes(
		execute.ParamTypes(),
		api.ValueTypeI32,
		api.ValueTypeI32,
	) || !sameValueTypes(execute.ResultTypes(), api.ValueTypeI64) {
		return fmt.Errorf(
			"%w: freeagent_execute_v1 signature is invalid",
			ErrInvalidModule,
		)
	}

	memories := compiled.ExportedMemories()
	if len(memories) != 1 {
		return fmt.Errorf(
			"%w: exactly one exported memory is required",
			ErrInvalidModule,
		)
	}
	memory, found := memories[MemoryExportV1]
	if !found || memory.Min() > MaxInitialPagesV1 {
		return fmt.Errorf(
			"%w: memory initial pages exceed the ABI ceiling",
			ErrInvalidModule,
		)
	}
	maximum, bounded := memory.Max()
	if !bounded || maximum > MaxMemoryPagesV1 {
		return fmt.Errorf(
			"%w: memory requires an explicit maximum of at most %d pages",
			ErrInvalidModule,
			MaxMemoryPagesV1,
		)
	}
	return nil
}

func sameValueTypes(observed []api.ValueType, expected ...api.ValueType) bool {
	if len(observed) != len(expected) {
		return false
	}
	for index := range expected {
		if observed[index] != expected[index] {
			return false
		}
	}
	return true
}

func inspectTableV1(payload []byte) error {
	count, offset, ok := readVarUint32V1(payload, 0)
	if !ok || count != 1 || offset >= len(payload) || payload[offset] != 0x70 {
		return fmt.Errorf(
			"%w: table section must contain exactly one funcref table",
			ErrInvalidModule,
		)
	}
	offset++
	flags, next, ok := readVarUint32V1(payload, offset)
	if !ok || flags != 1 {
		return fmt.Errorf(
			"%w: table requires an explicit maximum",
			ErrInvalidModule,
		)
	}
	minimum, next, ok := readVarUint32V1(payload, next)
	if !ok {
		return fmt.Errorf("%w: table minimum is invalid", ErrInvalidModule)
	}
	maximum, next, ok := readVarUint32V1(payload, next)
	if !ok || next != len(payload) || minimum > maximum {
		return fmt.Errorf("%w: table limits are invalid", ErrInvalidModule)
	}
	if minimum > MaxTableElementsV1 || maximum > MaxTableElementsV1 {
		return fmt.Errorf(
			"%w: table elements exceed the ABI ceiling",
			ErrInvalidModule,
		)
	}
	return nil
}

func inspectElementsV1(ctx context.Context, payload []byte) error {
	segmentCount, offset, ok := readVarUint32V1(payload, 0)
	if !ok || segmentCount > MaxElementSegmentsV1 {
		return fmt.Errorf(
			"%w: element segment count exceeds the ABI ceiling",
			ErrInvalidModule,
		)
	}
	var totalElements uint64
	for segment := uint32(0); segment < segmentCount; segment++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		tableIndex, next, valid := readVarUint32V1(payload, offset)
		if !valid || tableIndex != 0 {
			return fmt.Errorf("%w: element table index is invalid", ErrInvalidModule)
		}
		offset = next
		offset, valid = skipElementOffsetExpressionV1(payload, offset)
		if !valid {
			return fmt.Errorf("%w: element offset is invalid", ErrInvalidModule)
		}
		initializerCount, next, valid := readVarUint32V1(payload, offset)
		if !valid {
			return fmt.Errorf("%w: element initializer count is invalid", ErrInvalidModule)
		}
		offset = next
		totalElements += uint64(initializerCount)
		if totalElements > MaxTableElementsV1 {
			return fmt.Errorf(
				"%w: element initializers exceed the ABI ceiling",
				ErrInvalidModule,
			)
		}
		for index := uint32(0); index < initializerCount; index++ {
			if index&0x3ff == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			_, next, valid = readVarUint32V1(payload, offset)
			if !valid {
				return fmt.Errorf("%w: element initializer is invalid", ErrInvalidModule)
			}
			offset = next
		}
	}
	if offset != len(payload) {
		return fmt.Errorf("%w: element section has trailing bytes", ErrInvalidModule)
	}
	return nil
}

func skipElementOffsetExpressionV1(input []byte, offset int) (int, bool) {
	if offset >= len(input) {
		return offset, false
	}
	opcode := input[offset]
	offset++
	var ok bool
	switch opcode {
	case 0x41: // i32.const
		offset, ok = skipVarInt32V1(input, offset)
	case 0x23: // global.get
		_, offset, ok = readVarUint32V1(input, offset)
	default:
		return offset, false
	}
	if !ok || offset >= len(input) || input[offset] != 0x0b {
		return offset, false
	}
	return offset + 1, true
}

func skipVarInt32V1(input []byte, offset int) (int, bool) {
	for index := 0; index < 5; index++ {
		if offset >= len(input) {
			return offset, false
		}
		current := input[offset]
		offset++
		if current&0x80 == 0 {
			return offset, true
		}
	}
	return offset, false
}

func inspectMemoryV1(payload []byte) error {
	count, offset, ok := readVarUint32V1(payload, 0)
	if !ok || count != 1 {
		return fmt.Errorf(
			"%w: exactly one local memory is required",
			ErrInvalidModule,
		)
	}
	flags, next, ok := readVarUint32V1(payload, offset)
	if !ok || flags != 1 {
		return fmt.Errorf(
			"%w: memory requires an explicit 32-bit maximum",
			ErrInvalidModule,
		)
	}
	minimum, next, ok := readVarUint32V1(payload, next)
	if !ok {
		return fmt.Errorf("%w: memory minimum is invalid", ErrInvalidModule)
	}
	maximum, next, ok := readVarUint32V1(payload, next)
	if !ok || next != len(payload) || minimum > maximum {
		return fmt.Errorf("%w: memory limits are invalid", ErrInvalidModule)
	}
	if minimum > MaxInitialPagesV1 {
		return fmt.Errorf(
			"%w: memory initial pages exceed the ABI ceiling",
			ErrInvalidModule,
		)
	}
	if maximum > MaxMemoryPagesV1 {
		return fmt.Errorf(
			"%w: memory maximum pages exceed the ABI ceiling",
			ErrInvalidModule,
		)
	}
	return nil
}

func inspectExportsV1(payload []byte) error {
	count, offset, ok := readVarUint32V1(payload, 0)
	if !ok || count != 3 {
		return fmt.Errorf(
			"%w: exactly memory, allocator and executor must be exported",
			ErrInvalidModule,
		)
	}
	expected := map[string]byte{
		MemoryExportV1:   0x02,
		AllocateExportV1: 0x00,
		ExecuteExportV1:  0x00,
	}
	seen := make(map[string]struct{}, len(expected))
	for index := uint32(0); index < count; index++ {
		nameLength, next, valid := readVarUint32V1(payload, offset)
		if !valid || nameLength > MaxExportNameBytesV1 ||
			uint64(nameLength) > uint64(len(payload)-next) {
			return fmt.Errorf("%w: export name is invalid", ErrInvalidModule)
		}
		offset = next
		nameBytes := payload[offset : offset+int(nameLength)]
		if !utf8.Valid(nameBytes) {
			return fmt.Errorf("%w: export name is invalid", ErrInvalidModule)
		}
		name := string(nameBytes)
		offset += int(nameLength)
		if offset >= len(payload) {
			return fmt.Errorf("%w: export kind is absent", ErrInvalidModule)
		}
		kind := payload[offset]
		offset++
		_, next, valid = readVarUint32V1(payload, offset)
		if !valid {
			return fmt.Errorf("%w: export index is invalid", ErrInvalidModule)
		}
		offset = next
		wantedKind, allowed := expected[name]
		if !allowed || kind != wantedKind {
			return fmt.Errorf("%w: extra or mistyped export", ErrInvalidModule)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("%w: duplicate export", ErrInvalidModule)
		}
		seen[name] = struct{}{}
	}
	if offset != len(payload) || len(seen) != len(expected) {
		return fmt.Errorf("%w: export section is not exact", ErrInvalidModule)
	}
	return nil
}

func readVarUint32V1(input []byte, offset int) (uint32, int, bool) {
	var value uint32
	for index := 0; index < 5; index++ {
		if offset >= len(input) {
			return 0, offset, false
		}
		current := input[offset]
		offset++
		if index == 4 && current&0xf0 != 0 {
			return 0, offset, false
		}
		value |= uint32(current&0x7f) << (7 * index)
		if current&0x80 == 0 {
			return value, offset, true
		}
	}
	return 0, offset, false
}
