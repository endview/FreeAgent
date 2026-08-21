// Package wasmaction implements the Core-owned freeagent-action-wasm/v1
// Action Host. Describe and Prepare are native, deterministic and offline;
// only the private Action Gateway executor may run guest instructions.
package wasmaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	AdapterIdentityV1  = "freeagent.adapter.action.wasm/v1"
	DescriptorSchemaV1 = "freeagent-action-wasm-descriptor/v1"
	ABIVersionV1       = "freeagent-action-wasm-abi/v1"

	MaxDescriptorBytesV1 = moduleapi.MaxActionDefinitionAggregateBytesV1 +
		moduleapi.MaxConfigBytes
)

var ErrInvalidDescriptor = errors.New(
	"wasmaction: invalid offline descriptor",
)

// DescriptorV1 freezes the executable path and static Action definitions
// covered by one module ArtifactDigest. It carries no host capability,
// credential, endpoint or mutable runtime policy.
type DescriptorV1 struct {
	SchemaVersion string                         `json:"schema_version"`
	ABIVersion    string                         `json:"abi_version"`
	ModulePath    string                         `json:"module_path"`
	Actions       []moduleapi.ActionDefinitionV1 `json:"actions"`
}

func NewDescriptorV1(input DescriptorV1) (DescriptorV1, []byte, error) {
	if input.SchemaVersion != DescriptorSchemaV1 {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: schema_version must be %q",
			ErrInvalidDescriptor,
			DescriptorSchemaV1,
		)
	}
	if input.ABIVersion != ABIVersionV1 {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: abi_version must be %q",
			ErrInvalidDescriptor,
			ABIVersionV1,
		)
	}
	normalized, err := moduleapi.NormalizeArtifactPath(input.ModulePath)
	if err != nil || normalized != input.ModulePath ||
		!strings.HasPrefix(normalized, "content/") ||
		!strings.HasSuffix(normalized, ".wasm") {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: module_path must be a canonical content/*.wasm path",
			ErrInvalidDescriptor,
		)
	}
	actions, err := moduleapi.FreezeActionDefinitionsV1(input.Actions)
	if err != nil {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: actions: %v",
			ErrInvalidDescriptor,
			err,
		)
	}
	aggregate := 0
	for _, action := range actions {
		if action.RequestedEffectClass != moduleapi.EffectNone {
			return DescriptorV1{}, nil, fmt.Errorf(
				"%w: every Action must request effect none",
				ErrInvalidDescriptor,
			)
		}
		aggregate += len(action.Description) + len(action.InputSchema)
		if aggregate > moduleapi.MaxActionDefinitionAggregateBytesV1 {
			return DescriptorV1{}, nil, fmt.Errorf(
				"%w: Action definition aggregate exceeds %d bytes",
				ErrInvalidDescriptor,
				moduleapi.MaxActionDefinitionAggregateBytesV1,
			)
		}
	}
	frozen := DescriptorV1{
		SchemaVersion: input.SchemaVersion,
		ABIVersion:    input.ABIVersion,
		ModulePath:    input.ModulePath,
		Actions:       cloneDefinitions(actions),
	}
	encoded, err := json.Marshal(frozen)
	if err != nil {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: encode",
			ErrInvalidDescriptor,
		)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaxDescriptorBytesV1,
			MaxDepth: 128,
			MaxNodes: MaxDescriptorBytesV1,
		},
	)
	if err != nil {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: canonicalize",
			ErrInvalidDescriptor,
		)
	}
	return frozen, bytes.Clone(canonical), nil
}

func RestoreDescriptorV1(canonical []byte) (DescriptorV1, error) {
	owned := bytes.Clone(canonical)
	if len(owned) == 0 || len(owned) > MaxDescriptorBytesV1 {
		return DescriptorV1{}, fmt.Errorf(
			"%w: wire must contain between 1 and %d bytes",
			ErrInvalidDescriptor,
			MaxDescriptorBytesV1,
		)
	}
	normalized, err := moduleapi.CanonicalJSONWithLimits(
		owned,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaxDescriptorBytesV1,
			MaxDepth: 128,
			MaxNodes: MaxDescriptorBytesV1,
		},
	)
	if err != nil || len(normalized) == 0 || normalized[0] != '{' ||
		!bytes.Equal(owned, normalized) {
		return DescriptorV1{}, fmt.Errorf(
			"%w: wire must be an exact canonical JSON object",
			ErrInvalidDescriptor,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(owned))
	decoder.DisallowUnknownFields()
	var decoded DescriptorV1
	if err := decoder.Decode(&decoded); err != nil {
		return DescriptorV1{}, fmt.Errorf(
			"%w: decode",
			ErrInvalidDescriptor,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return DescriptorV1{}, fmt.Errorf(
			"%w: trailing JSON",
			ErrInvalidDescriptor,
		)
	}
	frozen, rebuilt, err := NewDescriptorV1(decoded)
	if err != nil {
		return DescriptorV1{}, err
	}
	if !bytes.Equal(owned, rebuilt) {
		return DescriptorV1{}, fmt.Errorf(
			"%w: wire is not frozen canonically",
			ErrInvalidDescriptor,
		)
	}
	return frozen, nil
}

func cloneDescriptor(input DescriptorV1) DescriptorV1 {
	return DescriptorV1{
		SchemaVersion: input.SchemaVersion,
		ABIVersion:    input.ABIVersion,
		ModulePath:    input.ModulePath,
		Actions:       cloneDefinitions(input.Actions),
	}
}

func cloneDefinitions(input []moduleapi.ActionDefinitionV1) []moduleapi.ActionDefinitionV1 {
	output := make([]moduleapi.ActionDefinitionV1, len(input))
	for index, definition := range input {
		definition.InputSchema = bytes.Clone(definition.InputSchema)
		output[index] = definition
	}
	return output
}
