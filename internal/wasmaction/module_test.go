package wasmaction

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestValidateModuleV1AcceptsExactABI(t *testing.T) {
	if err := ValidateModuleV1(
		context.Background(),
		testWASMModule(testWASMOptions{}),
	); err != nil {
		t.Fatalf("ValidateModuleV1 rejected the exact ABI: %v", err)
	}
}

func TestValidateModuleV1AcceptsBoundedInternalTable(t *testing.T) {
	maximum := uint32(32)
	initializers := uint32(1)
	if err := ValidateModuleV1(
		context.Background(),
		testWASMModule(testWASMOptions{
			includeTable:            true,
			tableInitial:            1,
			tableMaximum:            &maximum,
			elementInitializerCount: &initializers,
		}),
	); err != nil {
		t.Fatalf("ValidateModuleV1 rejected a bounded internal table: %v", err)
	}
}

func TestValidateModuleV1AcceptsBoundedOpaqueCustomSectionAndGlobal(t *testing.T) {
	module := prependTestCustomSectionV1(
		testWASMModule(testWASMOptions{}),
		[]byte{0x04, 'm', 'e', 't', 'a', 0xde, 0xad},
	)
	module = insertTestSectionBeforeV1(
		t,
		module,
		7,
		6,
		[]byte{
			0x01,       // one global
			0x7f, 0x00, // immutable i32
			0x41, 0x00, 0x0b, // i32.const 0; end
		},
	)
	if err := ValidateModuleV1(context.Background(), module); err != nil {
		t.Fatalf("ValidateModuleV1 rejected bounded metadata/global: %v", err)
	}
}

func TestValidateModuleV1RejectsForbiddenSurfaceAndMemoryBounds(t *testing.T) {
	initialOne := uint32(1)
	initialTooLarge := uint32(MaxInitialPagesV1 + 1)
	maximumTooLarge := uint32(MaxMemoryPagesV1 + 1)
	tableMaximum := uint32(32)
	tableTooLarge := uint32(MaxTableElementsV1 + 1)
	elementsTooLarge := uint32(MaxTableElementsV1 + 1)
	tests := []struct {
		name    string
		options testWASMOptions
	}{
		{
			name:    "import",
			options: testWASMOptions{includeImport: true},
		},
		{
			name:    "start section",
			options: testWASMOptions{includeStart: true},
		},
		{
			name:    "extra export",
			options: testWASMOptions{includeExtraExport: true},
		},
		{
			name: "memory without maximum",
			options: testWASMOptions{
				initialPages: &initialOne,
				maximumPages: nil,
			},
		},
		{
			name: "memory initial exceeds ceiling",
			options: testWASMOptions{
				initialPages: &initialTooLarge,
				maximumPages: &initialTooLarge,
			},
		},
		{
			name: "memory maximum exceeds ceiling",
			options: testWASMOptions{
				initialPages: &initialOne,
				maximumPages: &maximumTooLarge,
			},
		},
		{
			name: "table without maximum",
			options: testWASMOptions{
				includeTable: true,
			},
		},
		{
			name: "table initial exceeds ceiling",
			options: testWASMOptions{
				includeTable: true,
				tableInitial: tableTooLarge,
				tableMaximum: &tableTooLarge,
			},
		},
		{
			name: "table maximum exceeds ceiling",
			options: testWASMOptions{
				includeTable: true,
				tableMaximum: &tableTooLarge,
			},
		},
		{
			name: "element initializers exceed ceiling",
			options: testWASMOptions{
				includeTable:            true,
				tableMaximum:            &tableMaximum,
				elementInitializerCount: &elementsTooLarge,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateModuleV1(
				context.Background(),
				testWASMModule(test.options),
			)
			if !errors.Is(err, ErrInvalidModule) {
				t.Fatalf("ValidateModuleV1 error = %v, want ErrInvalidModule", err)
			}
		})
	}
}

func TestValidateModuleV1RejectsOversizedBinary(t *testing.T) {
	err := ValidateModuleV1(
		context.Background(),
		bytes.Repeat([]byte{0}, MaxModuleBytesV1+1),
	)
	if !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("ValidateModuleV1 error = %v, want ErrInvalidModule", err)
	}
}

func TestValidateModuleV1RejectsDeclaredAllocationAmplification(t *testing.T) {
	base := testWASMModule(testWASMOptions{})
	tooManyTypes := appendVarUint32(nil, MaxTypesV1+1)
	tooManyParameters := []byte{0x01, 0x60}
	tooManyParameters = appendVarUint32(
		tooManyParameters,
		MaxFunctionParametersV1+1,
	)
	tooManyResults := []byte{0x01, 0x60, 0x00}
	tooManyResults = appendVarUint32(
		tooManyResults,
		MaxFunctionResultsV1+1,
	)
	tooManyFunctions := appendVarUint32(nil, MaxFunctionsV1+1)
	functionCodeMismatch := []byte{0x01, 0x00}
	tooManyGlobals := appendVarUint32(nil, MaxGlobalsV1+1)
	tooManyCodeBodies := appendVarUint32(nil, MaxFunctionsV1+1)
	oversizedBody := []byte{0x02}
	oversizedBody = appendVarUint32(
		oversizedBody,
		MaxFunctionBodyBytesV1+1,
	)
	tooManyLocalDeclarations := appendVarUint32(
		nil,
		MaxLocalDeclarationsV1+1,
	)
	tooManyFunctionLocals := []byte{0x01}
	tooManyFunctionLocals = appendVarUint32(
		tooManyFunctionLocals,
		MaxFunctionLocalsV1+1,
	)
	tooManyFunctionLocals = append(tooManyFunctionLocals, 0x7f, 0x0b)
	tooManyDataSegments := appendVarUint32(nil, MaxDataSegmentsV1+1)
	oversizedData := []byte{0x01, 0x00, 0x41, 0x00, 0x0b}
	oversizedData = appendVarUint32(oversizedData, MaxDataBytesV1+1)
	tooManyElementSegments := appendVarUint32(
		nil,
		MaxElementSegmentsV1+1,
	)
	oversizedExportName := []byte{0x03}
	oversizedExportName = appendVarUint32(
		oversizedExportName,
		MaxExportNameBytesV1+1,
	)
	oversizedCustomName := appendVarUint32(
		nil,
		MaxCustomSectionNameBytesV1+1,
	)
	nameCustomSection := appendVarUint32(nil, uint32(len("name")))
	nameCustomSection = append(nameCustomSection, "name"...)
	tooManyBranchTargets := []byte{0x0e}
	tooManyBranchTargets = appendVarUint32(
		tooManyBranchTargets,
		MaxBranchTableTargetsV1+1,
	)

	aggregateLocals := testModuleWithAggregateLocalsV1(
		base,
		(MaxAggregateLocalsV1/MaxFunctionLocalsV1)+1,
		MaxFunctionLocalsV1,
	)
	aggregateBranchTargets := testModuleWithAggregateBranchTargetsV1(
		base,
		(MaxAggregateBranchTableTargetsV1/MaxBranchTableTargetsV1)+1,
		MaxBranchTableTargetsV1,
	)
	aggregateData := testModuleWithAggregateDataV1(base)
	tooManyCustomSections := bytes.Clone(wasmMagicAndVersionV1)
	for index := 0; index <= MaxCustomSectionsV1; index++ {
		tooManyCustomSections = appendTestSection(
			tooManyCustomSections,
			0,
			[]byte{0x01, 'x'},
		)
	}
	tooManyCustomSections = append(
		tooManyCustomSections,
		base[len(wasmMagicAndVersionV1):]...,
	)

	tests := []struct {
		name   string
		module []byte
	}{
		{
			name:   "type count",
			module: replaceTestSectionV1(t, base, 1, tooManyTypes),
		},
		{
			name:   "function parameter count",
			module: replaceTestSectionV1(t, base, 1, tooManyParameters),
		},
		{
			name:   "function result count",
			module: replaceTestSectionV1(t, base, 1, tooManyResults),
		},
		{
			name:   "function count",
			module: replaceTestSectionV1(t, base, 3, tooManyFunctions),
		},
		{
			name: "function and code count mismatch",
			module: replaceTestSectionV1(
				t,
				base,
				3,
				functionCodeMismatch,
			),
		},
		{
			name: "global count",
			module: insertTestSectionBeforeV1(
				t,
				base,
				7,
				6,
				tooManyGlobals,
			),
		},
		{
			name:   "code count",
			module: replaceTestSectionV1(t, base, 10, tooManyCodeBodies),
		},
		{
			name:   "function body size",
			module: replaceTestSectionV1(t, base, 10, oversizedBody),
		},
		{
			name: "code section bytes",
			module: replaceTestSectionV1(
				t,
				base,
				10,
				bytes.Repeat([]byte{0}, MaxCodeSectionBytesV1+1),
			),
		},
		{
			name: "local declaration count",
			module: replaceTestSectionV1(
				t,
				base,
				10,
				testCodeSectionWithFirstBodyV1(tooManyLocalDeclarations),
			),
		},
		{
			name: "function locals",
			module: replaceTestSectionV1(
				t,
				base,
				10,
				testCodeSectionWithFirstBodyV1(tooManyFunctionLocals),
			),
		},
		{
			name:   "aggregate locals",
			module: aggregateLocals,
		},
		{
			name:   "data segment count",
			module: replaceTestSectionV1(t, base, 11, tooManyDataSegments),
		},
		{
			name:   "data bytes",
			module: replaceTestSectionV1(t, base, 11, oversizedData),
		},
		{
			name:   "aggregate data bytes",
			module: aggregateData,
		},
		{
			name: "element segment count",
			module: insertTestSectionBeforeV1(
				t,
				base,
				10,
				9,
				tooManyElementSegments,
			),
		},
		{
			name:   "export name bytes",
			module: replaceTestSectionV1(t, base, 7, oversizedExportName),
		},
		{
			name: "custom section name bytes",
			module: prependTestCustomSectionV1(
				base,
				oversizedCustomName,
			),
		},
		{
			name:   "name custom section",
			module: prependTestCustomSectionV1(base, nameCustomSection),
		},
		{
			name:   "custom section count",
			module: tooManyCustomSections,
		},
		{
			name: "br_table target count",
			module: testWASMModule(testWASMOptions{
				executeInstructions: tooManyBranchTargets,
			}),
		},
		{
			name:   "aggregate br_table targets",
			module: aggregateBranchTargets,
		},
		{
			name: "duplicate standard section",
			module: appendTestSection(
				bytes.Clone(base),
				11,
				[]byte{0x00},
			),
		},
		{
			name: "non Core v1 data count section",
			module: appendTestSection(
				bytes.Clone(base),
				12,
				[]byte{0x00},
			),
		},
		{
			name: "standard section order",
			module: appendTestSection(
				appendTestSection(
					bytes.Clone(wasmMagicAndVersionV1),
					5,
					[]byte{0x01, 0x01, 0x01, 0x02},
				),
				1,
				[]byte{0x00},
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateModuleV1(context.Background(), test.module)
			if !errors.Is(err, ErrInvalidModule) {
				t.Fatalf(
					"ValidateModuleV1 error = %v, want ErrInvalidModule",
					err,
				)
			}
		})
	}
}

func TestInspectCodeInstructionsV1ParsesImmediatesBeforeBrTable(t *testing.T) {
	// 0x0e is the immediate value of i32.const, not an opcode. This guards
	// against a byte-search implementation that would reject valid syntax.
	body := []byte{
		0x41, 0x0e, // i32.const 14
		0x80,       // i64.div_u
		0x80,       // i64.div_u
		0x04, 0x40, // if with empty block type
		0x0b, // end if
		0x0b, // end function
	}
	if _, err := inspectCodeInstructionsV1(context.Background(), body); err != nil {
		t.Fatalf("inspectCodeInstructionsV1 rejected instruction syntax: %v", err)
	}
}

func TestValidateModuleV1ValidationContextClassification(t *testing.T) {
	callerContext, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()
	validationContext, cancelValidation := context.WithCancel(callerContext)
	defer cancelValidation()
	callerErr := normalizeValidationFailureV1(
		callerContext,
		validationContext,
		errors.New("fallback"),
	)
	if !errors.Is(callerErr, context.Canceled) ||
		errors.Is(callerErr, ErrValidationTimeoutV1) {
		t.Fatalf("caller cancellation classification = %v", callerErr)
	}

	hostValidationContext, cancelHostValidation := context.WithDeadline(
		context.Background(),
		time.Now().Add(-time.Second),
	)
	defer cancelHostValidation()
	hostErr := normalizeValidationFailureV1(
		context.Background(),
		hostValidationContext,
		errors.New("fallback"),
	)
	if !errors.Is(hostErr, ErrInvalidModule) ||
		!errors.Is(hostErr, ErrValidationTimeoutV1) ||
		errors.Is(hostErr, context.DeadlineExceeded) {
		t.Fatalf("Host validation timeout classification = %v", hostErr)
	}
}

type testWASMOptions struct {
	initialPages            *uint32
	maximumPages            *uint32
	includeImport           bool
	includeStart            bool
	includeExtraExport      bool
	includeTable            bool
	tableInitial            uint32
	tableMaximum            *uint32
	elementInitializerCount *uint32
	executeInstructions     []byte
	outputPointer           uint32
	output                  []byte
}

func testWASMModule(options testWASMOptions) []byte {
	initialPages := uint32(1)
	maximumPages := uint32(2)
	if options.initialPages != nil {
		initialPages = *options.initialPages
	}
	if options.maximumPages == nil && options.initialPages == nil {
		options.maximumPages = &maximumPages
	}
	outputPointer := options.outputPointer
	if outputPointer == 0 {
		outputPointer = 2048
	}
	output := bytes.Clone(options.output)
	if len(output) == 0 {
		output = []byte(`{"ok":true}`)
	}
	if len(options.executeInstructions) == 0 {
		packed := uint64(outputPointer)<<32 | uint64(len(output))
		options.executeInstructions = appendVarInt64(
			[]byte{0x42}, // i64.const
			int64(packed),
		)
	}

	module := bytes.Clone(wasmMagicAndVersionV1)
	typeSection := []byte{
		0x02,
		0x60, 0x01, 0x7f, 0x01, 0x7f,
		0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7e,
	}
	module = appendTestSection(module, 1, typeSection)
	if options.includeImport {
		// The Host rejects a non-empty import section before compilation. The
		// intentionally incomplete entry proves that no import kind is trusted.
		module = appendTestSection(module, 2, []byte{0x01})
	}
	module = appendTestSection(module, 3, []byte{0x02, 0x00, 0x01})
	if options.includeTable {
		tableSection := []byte{0x01, 0x70}
		if options.tableMaximum == nil {
			tableSection = append(tableSection, 0x00)
			tableSection = appendVarUint32(tableSection, options.tableInitial)
		} else {
			tableSection = append(tableSection, 0x01)
			tableSection = appendVarUint32(tableSection, options.tableInitial)
			tableSection = appendVarUint32(tableSection, *options.tableMaximum)
		}
		module = appendTestSection(module, 4, tableSection)
	}

	memorySection := []byte{0x01}
	if options.maximumPages == nil {
		memorySection = append(memorySection, 0x00)
		memorySection = appendVarUint32(memorySection, initialPages)
	} else {
		memorySection = append(memorySection, 0x01)
		memorySection = appendVarUint32(memorySection, initialPages)
		memorySection = appendVarUint32(memorySection, *options.maximumPages)
	}
	module = appendTestSection(module, 5, memorySection)

	exportCount := uint32(3)
	if options.includeExtraExport {
		exportCount++
	}
	exportSection := appendVarUint32(nil, exportCount)
	exportSection = appendTestExport(exportSection, MemoryExportV1, 0x02, 0)
	exportSection = appendTestExport(exportSection, AllocateExportV1, 0x00, 0)
	exportSection = appendTestExport(exportSection, ExecuteExportV1, 0x00, 1)
	if options.includeExtraExport {
		exportSection = appendTestExport(exportSection, "extra", 0x00, 0)
	}
	module = appendTestSection(module, 7, exportSection)
	if options.includeStart {
		module = appendTestSection(module, 8, []byte{0x00})
	}
	if options.elementInitializerCount != nil {
		elementSection := []byte{
			0x01,       // one segment
			0x00,       // table index zero
			0x41, 0x00, // i32.const 0
			0x0b, // end offset expression
		}
		elementSection = appendVarUint32(
			elementSection,
			*options.elementInitializerCount,
		)
		for index := uint32(0); index < *options.elementInitializerCount; index++ {
			elementSection = appendVarUint32(elementSection, 0)
		}
		module = appendTestSection(module, 9, elementSection)
	}

	allocateBody := []byte{0x00, 0x41} // no locals; i32.const
	allocateBody = appendVarInt64(allocateBody, 1024)
	allocateBody = append(allocateBody, 0x0b)
	executeBody := append([]byte{0x00}, options.executeInstructions...)
	executeBody = append(executeBody, 0x0b)
	codeSection := []byte{0x02}
	codeSection = appendVarUint32(codeSection, uint32(len(allocateBody)))
	codeSection = append(codeSection, allocateBody...)
	codeSection = appendVarUint32(codeSection, uint32(len(executeBody)))
	codeSection = append(codeSection, executeBody...)
	module = appendTestSection(module, 10, codeSection)

	dataSection := []byte{0x01, 0x00, 0x41}
	dataSection = appendVarInt64(dataSection, int64(outputPointer))
	dataSection = append(dataSection, 0x0b)
	dataSection = appendVarUint32(dataSection, uint32(len(output)))
	dataSection = append(dataSection, output...)
	return appendTestSection(module, 11, dataSection)
}

func appendTestSection(module []byte, sectionID byte, payload []byte) []byte {
	module = append(module, sectionID)
	module = appendVarUint32(module, uint32(len(payload)))
	return append(module, payload...)
}

func appendTestExport(
	payload []byte,
	name string,
	kind byte,
	index uint32,
) []byte {
	payload = appendVarUint32(payload, uint32(len(name)))
	payload = append(payload, name...)
	payload = append(payload, kind)
	return appendVarUint32(payload, index)
}

func appendVarUint32(output []byte, value uint32) []byte {
	for {
		current := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			current |= 0x80
		}
		output = append(output, current)
		if value == 0 {
			return output
		}
	}
}

func appendVarInt64(output []byte, value int64) []byte {
	for {
		current := byte(value & 0x7f)
		value >>= 7
		signSet := current&0x40 != 0
		done := (value == 0 && !signSet) || (value == -1 && signSet)
		if !done {
			current |= 0x80
		}
		output = append(output, current)
		if done {
			return output
		}
	}
}

func replaceTestSectionV1(
	t *testing.T,
	module []byte,
	sectionID byte,
	payload []byte,
) []byte {
	t.Helper()
	offset := len(wasmMagicAndVersionV1)
	for offset < len(module) {
		sectionStart := offset
		observedID := module[offset]
		offset++
		sectionSize, next, ok := readVarUint32V1(module, offset)
		if !ok || uint64(sectionSize) > uint64(len(module)-next) {
			t.Fatal("test module contains an invalid section")
		}
		sectionEnd := next + int(sectionSize)
		if observedID == sectionID {
			result := bytes.Clone(module[:sectionStart])
			result = appendTestSection(result, sectionID, payload)
			return append(result, module[sectionEnd:]...)
		}
		offset = sectionEnd
	}
	t.Fatalf("test module has no section %d", sectionID)
	return nil
}

func insertTestSectionBeforeV1(
	t *testing.T,
	module []byte,
	beforeSectionID byte,
	sectionID byte,
	payload []byte,
) []byte {
	t.Helper()
	offset := len(wasmMagicAndVersionV1)
	for offset < len(module) {
		sectionStart := offset
		observedID := module[offset]
		offset++
		sectionSize, next, ok := readVarUint32V1(module, offset)
		if !ok || uint64(sectionSize) > uint64(len(module)-next) {
			t.Fatal("test module contains an invalid section")
		}
		if observedID == beforeSectionID {
			result := bytes.Clone(module[:sectionStart])
			result = appendTestSection(result, sectionID, payload)
			return append(result, module[sectionStart:]...)
		}
		offset = next + int(sectionSize)
	}
	t.Fatalf("test module has no section %d", beforeSectionID)
	return nil
}

func prependTestCustomSectionV1(module []byte, payload []byte) []byte {
	result := bytes.Clone(wasmMagicAndVersionV1)
	result = appendTestSection(result, 0, payload)
	return append(result, module[len(wasmMagicAndVersionV1):]...)
}

func testCodeSectionWithFirstBodyV1(body []byte) []byte {
	payload := []byte{0x02}
	payload = appendVarUint32(payload, uint32(len(body)))
	return append(payload, body...)
}

func testModuleWithAggregateLocalsV1(
	base []byte,
	functionCount int,
	localsPerFunction uint32,
) []byte {
	functionSection := appendVarUint32(nil, uint32(functionCount))
	codeSection := appendVarUint32(nil, uint32(functionCount))
	for function := 0; function < functionCount; function++ {
		functionSection = appendVarUint32(functionSection, 0)
		body := []byte{0x01}
		body = appendVarUint32(body, localsPerFunction)
		body = append(body, 0x7f, 0x0b)
		codeSection = appendVarUint32(codeSection, uint32(len(body)))
		codeSection = append(codeSection, body...)
	}
	result := replaceTestSectionForHelperV1(base, 3, functionSection)
	return replaceTestSectionForHelperV1(result, 10, codeSection)
}

func testModuleWithAggregateBranchTargetsV1(
	base []byte,
	functionCount int,
	targetsPerFunction uint32,
) []byte {
	functionSection := appendVarUint32(nil, uint32(functionCount))
	codeSection := appendVarUint32(nil, uint32(functionCount))
	for function := 0; function < functionCount; function++ {
		functionSection = appendVarUint32(functionSection, 0)
		body := []byte{0x00, 0x0e}
		body = appendVarUint32(body, targetsPerFunction)
		for target := uint32(0); target <= targetsPerFunction; target++ {
			body = append(body, 0x00)
		}
		body = append(body, 0x0b)
		codeSection = appendVarUint32(codeSection, uint32(len(body)))
		codeSection = append(codeSection, body...)
	}
	result := replaceTestSectionForHelperV1(base, 3, functionSection)
	return replaceTestSectionForHelperV1(result, 10, codeSection)
}

func testModuleWithAggregateDataV1(base []byte) []byte {
	payload := []byte{0x02, 0x00, 0x41, 0x00, 0x0b}
	payload = appendVarUint32(payload, MaxDataBytesV1)
	payload = append(payload, bytes.Repeat([]byte{'x'}, MaxDataBytesV1)...)
	payload = append(payload, 0x00, 0x41, 0x00, 0x0b, 0x01)
	return replaceTestSectionForHelperV1(base, 11, payload)
}

func replaceTestSectionForHelperV1(
	module []byte,
	sectionID byte,
	payload []byte,
) []byte {
	offset := len(wasmMagicAndVersionV1)
	for offset < len(module) {
		sectionStart := offset
		observedID := module[offset]
		offset++
		sectionSize, next, ok := readVarUint32V1(module, offset)
		if !ok || uint64(sectionSize) > uint64(len(module)-next) {
			panic("invalid test module")
		}
		sectionEnd := next + int(sectionSize)
		if observedID == sectionID {
			result := bytes.Clone(module[:sectionStart])
			result = appendTestSection(result, sectionID, payload)
			return append(result, module[sectionEnd:]...)
		}
		offset = sectionEnd
	}
	panic("missing test section")
}
