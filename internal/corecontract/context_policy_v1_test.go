package corecontract

import (
	"bytes"
	"testing"
)

func validContextPolicyV1() ContextPolicyV1 {
	return ContextPolicyV1{
		SchemaVersion:        ContextPolicySchemaVersionV1,
		ContextWindowTokens:  128000,
		ReservedOutputTokens: 8000,
		RecentHistoryTurns:   2,
		EstimatorVersion:     ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
	}
}

func TestContextPolicyV1CanonicalRoundTripAndBudget(t *testing.T) {
	policy := validContextPolicyV1()
	frozen, canonical, err := NewContextPolicyV1(policy)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreContextPolicyV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if restored != frozen {
		t.Fatalf("restored = %+v, want %+v", restored, frozen)
	}
	budget, err := restored.InputBudgetTokens()
	if err != nil || budget != 120000 {
		t.Fatalf("budget = %d, error=%v", budget, err)
	}
	watermark, err := restored.RestoreWatermarkTokens()
	if err != nil || watermark != 102000 {
		t.Fatalf("watermark = %d, error=%v", watermark, err)
	}
	if !bytes.Equal(canonical, []byte(
		`{"context_window_tokens":128000,"estimator_version":`+
			`"canonical-json-utf8-byte-upper-bound/v1",`+
			`"recent_history_turns":2,`+
			`"reserved_output_tokens":8000,"schema_version":"context-policy/v1"}`,
	)) {
		t.Fatalf("canonical = %s", canonical)
	}
}

func TestContextPolicyV1WatermarkDoesNotOverflow(t *testing.T) {
	policy := validContextPolicyV1()
	policy.ContextWindowTokens = maximumJSONSafeIntegerV1
	policy.ReservedOutputTokens = 0
	frozen, canonical, err := NewContextPolicyV1(policy)
	if err != nil {
		t.Fatalf("NewContextPolicyV1(max safe integer): %v", err)
	}
	restored, err := RestoreContextPolicyV1(canonical)
	if err != nil {
		t.Fatalf("RestoreContextPolicyV1(max safe integer): %v", err)
	}
	if restored != frozen || restored.ContextWindowTokens != maximumJSONSafeIntegerV1 {
		t.Fatalf("context policy changed across canonical round trip: %+v", restored)
	}
	watermark, err := policy.RestoreWatermarkTokens()
	if err != nil {
		t.Fatal(err)
	}
	want := (maximumJSONSafeIntegerV1/10000)*8500 +
		(maximumJSONSafeIntegerV1%10000)*8500/10000
	if watermark != want {
		t.Fatalf("watermark = %d, want %d", watermark, want)
	}
	if _, err := ContextRestoreWatermarkTokensV1(
		maximumJSONSafeIntegerV1 + 1,
	); err == nil {
		t.Fatal("watermark input above the JSON safe integer boundary was accepted")
	}
}

func TestContextPolicyV1RejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ContextPolicyV1)
	}{
		{
			name: "schema",
			mutate: func(value *ContextPolicyV1) {
				value.SchemaVersion = "context-policy/v2"
			},
		},
		{
			name: "zero window",
			mutate: func(value *ContextPolicyV1) {
				value.ContextWindowTokens = 0
			},
		},
		{
			name: "window outside JSON safe integer",
			mutate: func(value *ContextPolicyV1) {
				value.ContextWindowTokens = maximumJSONSafeIntegerV1 + 1
			},
		},
		{
			name: "reserve equals window",
			mutate: func(value *ContextPolicyV1) {
				value.ReservedOutputTokens = value.ContextWindowTokens
			},
		},
		{
			name: "too many recent turns",
			mutate: func(value *ContextPolicyV1) {
				value.RecentHistoryTurns = 257
			},
		},
		{
			name: "unknown estimator",
			mutate: func(value *ContextPolicyV1) {
				value.EstimatorVersion = "provider-tokenizer/latest"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validContextPolicyV1()
			test.mutate(&value)
			if _, _, err := NewContextPolicyV1(value); err == nil {
				t.Fatal("accepted invalid context policy")
			}
		})
	}
}

func TestRestoreContextPolicyV1RejectsUnknownField(t *testing.T) {
	canonical := []byte(
		`{"context_window_tokens":128000,"estimator_version":` +
			`"canonical-json-utf8-byte-upper-bound/v1",` +
			`"optional_repository_reads":false,` +
			`"recent_history_turns":2,` +
			`"reserved_output_tokens":8000,"schema_version":"context-policy/v1"}`,
	)
	if _, err := RestoreContextPolicyV1(canonical); err == nil {
		t.Fatal("accepted unknown context policy field")
	}
}
