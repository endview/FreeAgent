package wasmaction

import (
	"bytes"
	"context"
	"fmt"
	"unicode/utf8"
)

type moduleSectionFactsV1 struct {
	functionCount               uint32
	codeCount                   uint32
	aggregateLocals             uint64
	aggregateBranchTableTargets uint64
	customSectionCount          uint32
	standardSections            [12]bool
	lastStandardSection         byte
}

// inspectModuleSectionsV1 is the allocation-safe parser in front of wazero.
// It rejects declaration counts and byte ranges before wazero can size a Go
// slice or map from untrusted input. wazero remains authoritative for full
// Core v1 type and instruction validation.
func inspectModuleSectionsV1(ctx context.Context, wasm []byte) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidModule)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(wasm) < len(wasmMagicAndVersionV1) ||
		len(wasm) > MaxModuleBytesV1 ||
		!bytes.Equal(wasm[:len(wasmMagicAndVersionV1)], wasmMagicAndVersionV1) {
		return fmt.Errorf("%w: magic, version or size is invalid", ErrInvalidModule)
	}

	facts := moduleSectionFactsV1{}
	offset := len(wasmMagicAndVersionV1)
	for offset < len(wasm) {
		if err := ctx.Err(); err != nil {
			return err
		}
		sectionID := wasm[offset]
		offset++
		if sectionID > 11 {
			return fmt.Errorf(
				"%w: section is not part of Core v1",
				ErrInvalidModule,
			)
		}
		sectionSize, next, ok := readVarUint32V1(wasm, offset)
		if !ok {
			return fmt.Errorf("%w: section size is invalid", ErrInvalidModule)
		}
		offset = next
		if uint64(sectionSize) > uint64(len(wasm)-offset) {
			return fmt.Errorf("%w: section is truncated", ErrInvalidModule)
		}
		end := offset + int(sectionSize)
		payload := wasm[offset:end]

		if sectionID == 0 {
			facts.customSectionCount++
			if facts.customSectionCount > MaxCustomSectionsV1 {
				return fmt.Errorf(
					"%w: custom section count exceeds the Host ceiling",
					ErrInvalidModule,
				)
			}
			if err := inspectCustomSectionV1(payload); err != nil {
				return err
			}
			offset = end
			continue
		}
		if facts.standardSections[sectionID] {
			return fmt.Errorf(
				"%w: duplicate standard section",
				ErrInvalidModule,
			)
		}
		if sectionID <= facts.lastStandardSection {
			return fmt.Errorf(
				"%w: standard sections are out of order",
				ErrInvalidModule,
			)
		}
		facts.standardSections[sectionID] = true
		facts.lastStandardSection = sectionID

		var err error
		switch sectionID {
		case 1:
			err = inspectTypesV1(ctx, payload)
		case 2:
			err = inspectImportsV1(payload)
		case 3:
			facts.functionCount, err = inspectFunctionsV1(ctx, payload)
		case 4:
			err = inspectTableV1(payload)
		case 5:
			err = inspectMemoryV1(payload)
		case 6:
			err = inspectGlobalsV1(ctx, payload)
		case 7:
			err = inspectExportsV1(payload)
		case 8:
			err = fmt.Errorf(
				"%w: start section is forbidden",
				ErrInvalidModule,
			)
		case 9:
			err = inspectElementsV1(ctx, payload)
		case 10:
			facts.codeCount, err = inspectCodeV1(ctx, payload, &facts)
		case 11:
			err = inspectDataV1(ctx, payload)
		}
		if err != nil {
			return err
		}
		offset = end
	}

	if !facts.standardSections[7] {
		return fmt.Errorf("%w: export section is absent", ErrInvalidModule)
	}
	if !facts.standardSections[5] {
		return fmt.Errorf("%w: memory section is absent", ErrInvalidModule)
	}
	if facts.functionCount != facts.codeCount {
		return fmt.Errorf(
			"%w: function and code counts differ",
			ErrInvalidModule,
		)
	}
	return nil
}

func inspectCustomSectionV1(payload []byte) error {
	nameLength, offset, ok := readVarUint32V1(payload, 0)
	if !ok || nameLength > MaxCustomSectionNameBytesV1 ||
		uint64(nameLength) > uint64(len(payload)-offset) {
		return fmt.Errorf(
			"%w: custom section name is invalid or oversized",
			ErrInvalidModule,
		)
	}
	name := payload[offset : offset+int(nameLength)]
	if !utf8.Valid(name) {
		return fmt.Errorf(
			"%w: custom section name is not UTF-8",
			ErrInvalidModule,
		)
	}
	// wazero decodes the standardized name section even when arbitrary custom
	// section retention is disabled. Reject it so no nested untrusted count can
	// reach that decoder.
	if bytes.Equal(name, []byte("name")) {
		return fmt.Errorf(
			"%w: name custom section is forbidden",
			ErrInvalidModule,
		)
	}
	return nil
}

func inspectImportsV1(payload []byte) error {
	count, offset, ok := readVarUint32V1(payload, 0)
	if !ok || count != 0 || offset != len(payload) {
		return fmt.Errorf("%w: imports are forbidden", ErrInvalidModule)
	}
	return nil
}

func inspectTypesV1(ctx context.Context, payload []byte) error {
	count, offset, ok := readVarUint32V1(payload, 0)
	if !ok || count > MaxTypesV1 {
		return fmt.Errorf(
			"%w: type count exceeds the Host ceiling",
			ErrInvalidModule,
		)
	}
	for index := uint32(0); index < count; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if offset >= len(payload) || payload[offset] != 0x60 {
			return fmt.Errorf("%w: function type is invalid", ErrInvalidModule)
		}
		offset++
		parameterCount, next, valid := readVarUint32V1(payload, offset)
		if !valid || parameterCount > MaxFunctionParametersV1 {
			return fmt.Errorf(
				"%w: function parameter count exceeds the Host ceiling",
				ErrInvalidModule,
			)
		}
		offset = next
		for parameter := uint32(0); parameter < parameterCount; parameter++ {
			if offset >= len(payload) || !isNumericValueTypeV1(payload[offset]) {
				return fmt.Errorf(
					"%w: function parameter type is invalid",
					ErrInvalidModule,
				)
			}
			offset++
		}
		resultCount, next, valid := readVarUint32V1(payload, offset)
		if !valid || resultCount > MaxFunctionResultsV1 {
			return fmt.Errorf(
				"%w: function result count exceeds the Host ceiling",
				ErrInvalidModule,
			)
		}
		offset = next
		for result := uint32(0); result < resultCount; result++ {
			if offset >= len(payload) || !isNumericValueTypeV1(payload[offset]) {
				return fmt.Errorf(
					"%w: function result type is invalid",
					ErrInvalidModule,
				)
			}
			offset++
		}
	}
	if offset != len(payload) {
		return fmt.Errorf("%w: type section has trailing bytes", ErrInvalidModule)
	}
	return nil
}

func inspectFunctionsV1(ctx context.Context, payload []byte) (uint32, error) {
	count, offset, ok := readVarUint32V1(payload, 0)
	if !ok || count > MaxFunctionsV1 {
		return 0, fmt.Errorf(
			"%w: function count exceeds the Host ceiling",
			ErrInvalidModule,
		)
	}
	for index := uint32(0); index < count; index++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		_, next, valid := readVarUint32V1(payload, offset)
		if !valid {
			return 0, fmt.Errorf(
				"%w: function type index is invalid",
				ErrInvalidModule,
			)
		}
		offset = next
	}
	if offset != len(payload) {
		return 0, fmt.Errorf(
			"%w: function section has trailing bytes",
			ErrInvalidModule,
		)
	}
	return count, nil
}

func inspectGlobalsV1(ctx context.Context, payload []byte) error {
	count, offset, ok := readVarUint32V1(payload, 0)
	if !ok || count > MaxGlobalsV1 {
		return fmt.Errorf(
			"%w: global count exceeds the Host ceiling",
			ErrInvalidModule,
		)
	}
	for index := uint32(0); index < count; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if offset+2 > len(payload) || !isNumericValueTypeV1(payload[offset]) ||
			(payload[offset+1] != 0 && payload[offset+1] != 1) {
			return fmt.Errorf("%w: global type is invalid", ErrInvalidModule)
		}
		offset += 2
		var valid bool
		offset, valid = skipGlobalInitializerV1(payload, offset)
		if !valid {
			return fmt.Errorf(
				"%w: global initializer is invalid",
				ErrInvalidModule,
			)
		}
	}
	if offset != len(payload) {
		return fmt.Errorf("%w: global section has trailing bytes", ErrInvalidModule)
	}
	return nil
}

func inspectCodeV1(
	ctx context.Context,
	payload []byte,
	facts *moduleSectionFactsV1,
) (uint32, error) {
	if len(payload) > MaxCodeSectionBytesV1 {
		return 0, fmt.Errorf(
			"%w: code section exceeds the Host byte ceiling",
			ErrInvalidModule,
		)
	}
	count, offset, ok := readVarUint32V1(payload, 0)
	if !ok || count > MaxFunctionsV1 {
		return 0, fmt.Errorf(
			"%w: code count exceeds the Host ceiling",
			ErrInvalidModule,
		)
	}
	for function := uint32(0); function < count; function++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		bodySize, next, valid := readVarUint32V1(payload, offset)
		if !valid || bodySize == 0 || bodySize > MaxFunctionBodyBytesV1 {
			return 0, fmt.Errorf(
				"%w: function body size exceeds the Host ceiling",
				ErrInvalidModule,
			)
		}
		offset = next
		if uint64(bodySize) > uint64(len(payload)-offset) {
			return 0, fmt.Errorf("%w: function body is truncated", ErrInvalidModule)
		}
		bodyEnd := offset + int(bodySize)
		body := payload[offset:bodyEnd]
		localDeclarationCount, localOffset, valid := readVarUint32V1(body, 0)
		if !valid || localDeclarationCount > MaxLocalDeclarationsV1 {
			return 0, fmt.Errorf(
				"%w: local declaration count exceeds the Host ceiling",
				ErrInvalidModule,
			)
		}
		var functionLocals uint64
		for declaration := uint32(0); declaration < localDeclarationCount; declaration++ {
			localCount, nextLocal, localValid := readVarUint32V1(body, localOffset)
			if !localValid || nextLocal >= len(body) ||
				!isNumericValueTypeV1(body[nextLocal]) {
				return 0, fmt.Errorf(
					"%w: local declaration is invalid",
					ErrInvalidModule,
				)
			}
			localOffset = nextLocal + 1
			functionLocals += uint64(localCount)
			if functionLocals > MaxFunctionLocalsV1 {
				return 0, fmt.Errorf(
					"%w: function locals exceed the Host ceiling",
					ErrInvalidModule,
				)
			}
		}
		facts.aggregateLocals += functionLocals
		if facts.aggregateLocals > MaxAggregateLocalsV1 {
			return 0, fmt.Errorf(
				"%w: aggregate locals exceed the Host ceiling",
				ErrInvalidModule,
			)
		}
		instructions := body[localOffset:]
		if len(instructions) == 0 || instructions[len(instructions)-1] != 0x0b {
			return 0, fmt.Errorf(
				"%w: function expression has no final end",
				ErrInvalidModule,
			)
		}
		branchTargets, instructionErr := inspectCodeInstructionsV1(
			ctx,
			instructions,
		)
		if instructionErr != nil {
			return 0, instructionErr
		}
		facts.aggregateBranchTableTargets += branchTargets
		if facts.aggregateBranchTableTargets > MaxAggregateBranchTableTargetsV1 {
			return 0, fmt.Errorf(
				"%w: aggregate br_table targets exceed the Host ceiling",
				ErrInvalidModule,
			)
		}
		offset = bodyEnd
	}
	if offset != len(payload) {
		return 0, fmt.Errorf("%w: code section has trailing bytes", ErrInvalidModule)
	}
	return count, nil
}

func inspectCodeInstructionsV1(ctx context.Context, body []byte) (uint64, error) {
	var branchTargets uint64
	for offset := 0; offset < len(body); {
		if offset&0x3ff == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		opcode := body[offset]
		offset++
		var ok bool
		switch {
		case opcode == 0x00 || opcode == 0x01 || opcode == 0x05 ||
			opcode == 0x0b || opcode == 0x0f || opcode == 0x1a ||
			opcode == 0x1b || (opcode >= 0x45 && opcode <= 0xbf):
			// No immediate.
		case opcode >= 0x02 && opcode <= 0x04:
			offset, ok = skipVarInt33V1(body, offset)
			if !ok {
				return 0, invalidInstructionImmediateV1()
			}
		case opcode == 0x0c || opcode == 0x0d || opcode == 0x10 ||
			(opcode >= 0x20 && opcode <= 0x24):
			_, offset, ok = readVarUint32V1(body, offset)
			if !ok {
				return 0, invalidInstructionImmediateV1()
			}
		case opcode == 0x0e:
			var targetCount uint32
			targetCount, offset, ok = readVarUint32V1(body, offset)
			if !ok || targetCount > MaxBranchTableTargetsV1 {
				return 0, fmt.Errorf(
					"%w: br_table target count exceeds the Host ceiling",
					ErrInvalidModule,
				)
			}
			branchTargets += uint64(targetCount) + 1 // Include the default target.
			for target := uint32(0); target <= targetCount; target++ {
				_, offset, ok = readVarUint32V1(body, offset)
				if !ok {
					return 0, invalidInstructionImmediateV1()
				}
			}
		case opcode == 0x11:
			_, offset, ok = readVarUint32V1(body, offset)
			if ok {
				_, offset, ok = readVarUint32V1(body, offset)
			}
			if !ok {
				return 0, invalidInstructionImmediateV1()
			}
		case opcode >= 0x28 && opcode <= 0x3e:
			_, offset, ok = readVarUint32V1(body, offset)
			if ok {
				_, offset, ok = readVarUint32V1(body, offset)
			}
			if !ok {
				return 0, invalidInstructionImmediateV1()
			}
		case opcode == 0x3f || opcode == 0x40:
			_, offset, ok = readVarUint32V1(body, offset)
			if !ok {
				return 0, invalidInstructionImmediateV1()
			}
		case opcode == 0x41:
			offset, ok = skipVarInt32V1(body, offset)
			if !ok {
				return 0, invalidInstructionImmediateV1()
			}
		case opcode == 0x42:
			offset, ok = skipVarInt64V1(body, offset)
			if !ok {
				return 0, invalidInstructionImmediateV1()
			}
		case opcode == 0x43:
			if len(body)-offset < 4 {
				return 0, invalidInstructionImmediateV1()
			}
			offset += 4
		case opcode == 0x44:
			if len(body)-offset < 8 {
				return 0, invalidInstructionImmediateV1()
			}
			offset += 8
		default:
			return 0, fmt.Errorf(
				"%w: instruction is outside Core v1",
				ErrInvalidModule,
			)
		}
	}
	return branchTargets, nil
}

func invalidInstructionImmediateV1() error {
	return fmt.Errorf(
		"%w: instruction immediate is invalid",
		ErrInvalidModule,
	)
}

func inspectDataV1(ctx context.Context, payload []byte) error {
	segmentCount, offset, ok := readVarUint32V1(payload, 0)
	if !ok || segmentCount > MaxDataSegmentsV1 {
		return fmt.Errorf(
			"%w: data segment count exceeds the Host ceiling",
			ErrInvalidModule,
		)
	}
	var totalDataBytes uint64
	for segment := uint32(0); segment < segmentCount; segment++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		prefix, next, valid := readVarUint32V1(payload, offset)
		if !valid || prefix != 0 {
			return fmt.Errorf(
				"%w: data segment is outside Core v1",
				ErrInvalidModule,
			)
		}
		offset = next
		offset, valid = skipElementOffsetExpressionV1(payload, offset)
		if !valid {
			return fmt.Errorf("%w: data offset is invalid", ErrInvalidModule)
		}
		dataSize, next, valid := readVarUint32V1(payload, offset)
		if !valid || dataSize > MaxDataBytesV1 {
			return fmt.Errorf(
				"%w: data bytes exceed the Host ceiling",
				ErrInvalidModule,
			)
		}
		offset = next
		totalDataBytes += uint64(dataSize)
		if totalDataBytes > MaxDataBytesV1 ||
			uint64(dataSize) > uint64(len(payload)-offset) {
			return fmt.Errorf(
				"%w: aggregate data bytes exceed the Host ceiling or are truncated",
				ErrInvalidModule,
			)
		}
		offset += int(dataSize)
	}
	if offset != len(payload) {
		return fmt.Errorf("%w: data section has trailing bytes", ErrInvalidModule)
	}
	return nil
}

func skipGlobalInitializerV1(input []byte, offset int) (int, bool) {
	if offset >= len(input) {
		return offset, false
	}
	opcode := input[offset]
	offset++
	var ok bool
	switch opcode {
	case 0x41:
		offset, ok = skipVarInt32V1(input, offset)
	case 0x42:
		offset, ok = skipVarInt64V1(input, offset)
	case 0x43:
		if len(input)-offset < 4 {
			return offset, false
		}
		offset += 4
		ok = true
	case 0x44:
		if len(input)-offset < 8 {
			return offset, false
		}
		offset += 8
		ok = true
	case 0x23:
		_, offset, ok = readVarUint32V1(input, offset)
	default:
		return offset, false
	}
	if !ok || offset >= len(input) || input[offset] != 0x0b {
		return offset, false
	}
	return offset + 1, true
}

func skipVarInt33V1(input []byte, offset int) (int, bool) {
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

func skipVarInt64V1(input []byte, offset int) (int, bool) {
	for index := 0; index < 10; index++ {
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

func isNumericValueTypeV1(value byte) bool {
	return value == 0x7f || value == 0x7e ||
		value == 0x7d || value == 0x7c
}
