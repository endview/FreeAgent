package moduleapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestContextBindingConfigV1CanonicalRoundTrip(t *testing.T) {
	parameters := json.RawMessage(`{ "z": 1, "nested": {"b": 2, "a": 1} }`)
	frozen, canonical, err := NewContextBindingConfigV1(
		ContextBindingConfigV1{
			SchemaVersion: ContextBindingConfigSchemaV1,
			Placement:     ContextPlacementUntrustedData,
			AllowSummary:  true,
			AllowDrop:     true,
			Parameters:    parameters,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	parameters[2] = 'X'
	if string(frozen.Parameters) != `{"nested":{"a":1,"b":2},"z":1}` {
		t.Fatalf("parameters = %s", frozen.Parameters)
	}
	restored, err := RestoreContextBindingConfigV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if restored.SchemaVersion != ContextBindingConfigSchemaV1 ||
		restored.Placement != ContextPlacementUntrustedData ||
		!restored.AllowSummary || !restored.AllowDrop ||
		!bytes.Equal(restored.Parameters, frozen.Parameters) {
		t.Fatalf("restored = %+v", restored)
	}
	restored.Parameters[0] = '['
	if frozen.Parameters[0] != '{' {
		t.Fatal("RestoreContextBindingConfigV1 aliased parameters")
	}
}

func TestContextBindingConfigV1DefaultsEmptyParameters(t *testing.T) {
	frozen, _, err := NewContextBindingConfigV1(ContextBindingConfigV1{
		SchemaVersion: ContextBindingConfigSchemaV1,
		Placement:     ContextPlacementTrustedInstruction,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(frozen.Parameters) != `{}` {
		t.Fatalf("parameters = %s", frozen.Parameters)
	}
}

func TestContextBindingConfigV1RejectsInvalidValues(t *testing.T) {
	base := ContextBindingConfigV1{
		SchemaVersion: ContextBindingConfigSchemaV1,
		Placement:     ContextPlacementTrustedInstruction,
		Parameters:    json.RawMessage(`{}`),
	}
	tests := []struct {
		name   string
		mutate func(*ContextBindingConfigV1)
	}{
		{
			name: "schema",
			mutate: func(value *ContextBindingConfigV1) {
				value.SchemaVersion = "context-binding-config/v2"
			},
		},
		{
			name: "placement",
			mutate: func(value *ContextBindingConfigV1) {
				value.Placement = "SYSTEM"
			},
		},
		{
			name: "parameters array",
			mutate: func(value *ContextBindingConfigV1) {
				value.Parameters = json.RawMessage(`[]`)
			},
		},
		{
			name: "oversized parameters",
			mutate: func(value *ContextBindingConfigV1) {
				value.Parameters = json.RawMessage(
					`{"value":"` + strings.Repeat("x", MaxConfigBytes) + `"}`,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if _, _, err := NewContextBindingConfigV1(value); err == nil {
				t.Fatal("accepted invalid context binding config")
			}
		})
	}
}

func TestRestoreContextBindingConfigV1RejectsUnknownAndNonCanonical(t *testing.T) {
	unknown := []byte(
		`{"allow_drop":false,"allow_summary":false,"parameters":{},` +
			`"placement":"TRUSTED_INSTRUCTION","schema_version":` +
			`"context-binding-config/v1","trust":"self-granted"}`,
	)
	if _, err := RestoreContextBindingConfigV1(unknown); err == nil {
		t.Fatal("accepted unknown trust field")
	}
	nonCanonical := []byte(
		`{"schema_version":"context-binding-config/v1",` +
			`"placement":"TRUSTED_INSTRUCTION","allow_summary":false,` +
			`"allow_drop":false,"parameters":{}}`,
	)
	if _, err := RestoreContextBindingConfigV1(nonCanonical); err == nil {
		t.Fatal("accepted non-canonical field order")
	}
}

func TestContextBindingParametersSchemaVersionV1DispatchesOnlyTheEnvelope(t *testing.T) {
	config, _, err := NewContextBindingConfigV1(ContextBindingConfigV1{
		SchemaVersion: ContextBindingConfigSchemaV1,
		Placement:     ContextPlacementUntrustedData,
		Parameters: []byte(
			`{"schema_version":"memory-context-binding/v1","max_items":4}`,
		),
	})
	if err != nil {
		t.Fatalf("NewContextBindingConfigV1: %v", err)
	}
	got, err := ContextBindingParametersSchemaVersionV1(config)
	if err != nil {
		t.Fatalf("ContextBindingParametersSchemaVersionV1: %v", err)
	}
	if got != "memory-context-binding/v1" {
		t.Fatalf("schema version = %q", got)
	}

	config.Parameters = []byte(`{"max_items":4}`)
	if _, err := ContextBindingParametersSchemaVersionV1(config); err == nil {
		t.Fatal("missing schema_version unexpectedly dispatched")
	}
}
