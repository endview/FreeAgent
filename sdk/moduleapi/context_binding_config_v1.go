package moduleapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const ContextBindingConfigSchemaV1 = "context-binding-config/v1"

// ContextPlacementV1 is the consumer-granted placement of context.provide/v1
// content. A provider response cannot change this value.
type ContextPlacementV1 string

const (
	ContextPlacementTrustedInstruction ContextPlacementV1 = "TRUSTED_INSTRUCTION"
	ContextPlacementUntrustedData      ContextPlacementV1 = "UNTRUSTED_DATA"
)

func (placement ContextPlacementV1) Validate() error {
	switch placement {
	case ContextPlacementTrustedInstruction,
		ContextPlacementUntrustedData:
		return nil
	default:
		return fmt.Errorf("unsupported context placement %q", placement)
	}
}

// ContextBindingParametersSchemaVersionV1 returns the sole protocol dispatch
// key from an already consumer-owned context Binding config. It deliberately
// does not interpret the remaining fields: the selected exact wire restorer
// must still reject unknown fields and validate the complete contract.
func ContextBindingParametersSchemaVersionV1(
	config ContextBindingConfigV1,
) (string, error) {
	restored, _, err := NewContextBindingConfigV1(config)
	if err != nil {
		return "", err
	}
	var envelope struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(restored.Parameters, &envelope); err != nil {
		return "", fmt.Errorf(
			"decode context binding parameters schema_version: %w",
			err,
		)
	}
	if envelope.SchemaVersion == "" ||
		envelope.SchemaVersion != strings.TrimSpace(envelope.SchemaVersion) ||
		len(envelope.SchemaVersion) > MaxIdentifierBytes ||
		!utf8.ValidString(envelope.SchemaVersion) {
		return "", fmt.Errorf(
			"context binding parameters schema_version must be a non-empty canonical identifier",
		)
	}
	return envelope.SchemaVersion, nil
}

// ContextBindingConfigV1 is the exact consumer-owned configuration referenced
// by a context.provide/v1 Binding. Parameters remain inert provider-specific
// configuration; they cannot override placement or retention rights.
type ContextBindingConfigV1 struct {
	SchemaVersion string             `json:"schema_version"`
	Placement     ContextPlacementV1 `json:"placement"`
	AllowSummary  bool               `json:"allow_summary"`
	AllowDrop     bool               `json:"allow_drop"`
	Parameters    json.RawMessage    `json:"parameters"`
}

// NewContextBindingConfigV1 validates, canonicalizes and defensively copies
// one context.provide/v1 Binding configuration.
func NewContextBindingConfigV1(
	input ContextBindingConfigV1,
) (ContextBindingConfigV1, []byte, error) {
	if input.SchemaVersion != ContextBindingConfigSchemaV1 {
		return ContextBindingConfigV1{}, nil, fmt.Errorf(
			"context binding config schema_version must be %q",
			ContextBindingConfigSchemaV1,
		)
	}
	if err := input.Placement.Validate(); err != nil {
		return ContextBindingConfigV1{}, nil, err
	}
	parameters, err := canonicalConfig(input.Parameters)
	if err != nil {
		return ContextBindingConfigV1{}, nil, fmt.Errorf(
			"context binding config parameters: %w",
			err,
		)
	}
	frozen := input
	frozen.Parameters = bytes.Clone(parameters)
	encoded, err := json.Marshal(frozen)
	if err != nil {
		return ContextBindingConfigV1{}, nil, fmt.Errorf(
			"marshal context binding config: %w",
			err,
		)
	}
	canonical, err := CanonicalJSONWithLimits(
		encoded,
		CanonicalJSONLimits{
			MaxBytes: MaxConfigBytes,
			MaxDepth: 128,
			MaxNodes: MaxConfigBytes,
		},
	)
	if err != nil {
		return ContextBindingConfigV1{}, nil, fmt.Errorf(
			"canonicalize context binding config: %w",
			err,
		)
	}
	if len(canonical) > MaxConfigBytes {
		return ContextBindingConfigV1{}, nil, fmt.Errorf(
			"canonical context binding config exceeds %d bytes",
			MaxConfigBytes,
		)
	}
	return frozen, bytes.Clone(canonical), nil
}

// RestoreContextBindingConfigV1 accepts only the exact canonical form emitted
// by NewContextBindingConfigV1 and rejects unknown fields.
func RestoreContextBindingConfigV1(
	canonical []byte,
) (ContextBindingConfigV1, error) {
	if len(canonical) == 0 || len(canonical) > MaxConfigBytes {
		return ContextBindingConfigV1{}, fmt.Errorf(
			"context binding config must contain between 1 and %d canonical bytes",
			MaxConfigBytes,
		)
	}
	checked, err := CanonicalJSONWithLimits(
		canonical,
		CanonicalJSONLimits{
			MaxBytes: MaxConfigBytes,
			MaxDepth: 128,
			MaxNodes: MaxConfigBytes,
		},
	)
	if err != nil || !bytes.Equal(checked, canonical) {
		return ContextBindingConfigV1{}, fmt.Errorf(
			"context binding config is not canonical JSON",
		)
	}
	var decoded ContextBindingConfigV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return ContextBindingConfigV1{}, fmt.Errorf(
			"decode context binding config: %w",
			err,
		)
	}
	restored, rebuilt, err := NewContextBindingConfigV1(decoded)
	if err != nil {
		return ContextBindingConfigV1{}, err
	}
	if !bytes.Equal(rebuilt, canonical) {
		return ContextBindingConfigV1{}, fmt.Errorf(
			"context binding config is not frozen canonically",
		)
	}
	restored.Parameters = bytes.Clone(restored.Parameters)
	return restored, nil
}
