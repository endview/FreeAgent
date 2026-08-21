package moduleapi

import (
	"bytes"
	"strings"
	"testing"
)

func TestModelAuthorityCeilingV1CanonicalRoundTrip(t *testing.T) {
	t.Parallel()

	input := validModelAuthorityCeilingV1()
	frozen, canonical, err := NewModelAuthorityCeilingV1(input)
	if err != nil {
		t.Fatalf("NewModelAuthorityCeilingV1: %v", err)
	}
	wantCanonical := []byte(
		`{"allow_official_provider_endpoint":true,"provider":"deepseek","schema_version":"model-authority-ceiling/v1","secret_ref":"placeholder","tenant_id":"tenant-1"}`,
	)
	if !bytes.Equal(canonical, wantCanonical) {
		t.Fatalf("canonical = %s, want %s", canonical, wantCanonical)
	}
	if frozen != input {
		t.Fatalf("frozen = %+v, want %+v", frozen, input)
	}
	if err := frozen.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	restored, err := RestoreModelAuthorityCeilingV1(canonical)
	if err != nil {
		t.Fatalf("RestoreModelAuthorityCeilingV1: %v", err)
	}
	if restored != frozen {
		t.Fatalf("restored = %+v, want %+v", restored, frozen)
	}
	rebuilt, rebuiltCanonical, err := NewModelAuthorityCeilingV1(restored)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if rebuilt != restored || !bytes.Equal(rebuiltCanonical, canonical) {
		t.Fatal("model authority ceiling round trip is not stable")
	}
}

func TestNewModelAuthorityCeilingV1RejectsInvalidFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ModelAuthorityCeilingV1)
	}{
		{
			name: "wrong schema",
			mutate: func(value *ModelAuthorityCeilingV1) {
				value.SchemaVersion = "model-authority-ceiling/v2"
			},
		},
		{
			name: "empty tenant",
			mutate: func(value *ModelAuthorityCeilingV1) {
				value.TenantID = ""
			},
		},
		{
			name: "trimmed provider",
			mutate: func(value *ModelAuthorityCeilingV1) {
				value.Provider = " deepseek"
			},
		},
		{
			name: "secret ref control character",
			mutate: func(value *ModelAuthorityCeilingV1) {
				invalidReference := "placeholder\n"
				value.SecretRef = invalidReference
			},
		},
		{
			name: "secret ref non NFC",
			mutate: func(value *ModelAuthorityCeilingV1) {
				invalidReference := "e\u0301"
				value.SecretRef = invalidReference
			},
		},
		{
			name: "secret ref too long",
			mutate: func(value *ModelAuthorityCeilingV1) {
				invalidReference := strings.Repeat("x", MaxOpaqueIDBytes+1)
				value.SecretRef = invalidReference
			},
		},
		{
			name: "official endpoint permission false",
			mutate: func(value *ModelAuthorityCeilingV1) {
				value.AllowOfficialProviderEndpoint = false
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validModelAuthorityCeilingV1()
			test.mutate(&input)
			if _, _, err := NewModelAuthorityCeilingV1(input); err == nil {
				t.Fatal("invalid model authority ceiling was accepted")
			}
			if err := input.Validate(); err == nil {
				t.Fatal("Validate accepted invalid model authority ceiling")
			}
		})
	}
}

func TestRestoreModelAuthorityCeilingV1RejectsNonExactWire(t *testing.T) {
	t.Parallel()

	_, canonical, err := NewModelAuthorityCeilingV1(
		validModelAuthorityCeilingV1(),
	)
	if err != nil {
		t.Fatalf("NewModelAuthorityCeilingV1: %v", err)
	}

	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "empty", payload: nil},
		{name: "whitespace", payload: append([]byte(" "), canonical...)},
		{
			name: "non canonical key order",
			payload: []byte(
				`{"schema_version":"model-authority-ceiling/v1","tenant_id":"tenant-1","provider":"deepseek","secret_ref":"placeholder","allow_official_provider_endpoint":true}`,
			),
		},
		{
			name: "missing schema",
			payload: []byte(
				`{"allow_official_provider_endpoint":true,"provider":"deepseek","secret_ref":"placeholder","tenant_id":"tenant-1"}`,
			),
		},
		{
			name: "missing tenant",
			payload: []byte(
				`{"allow_official_provider_endpoint":true,"provider":"deepseek","schema_version":"model-authority-ceiling/v1","secret_ref":"placeholder"}`,
			),
		},
		{
			name: "missing provider",
			payload: []byte(
				`{"allow_official_provider_endpoint":true,"schema_version":"model-authority-ceiling/v1","secret_ref":"placeholder","tenant_id":"tenant-1"}`,
			),
		},
		{
			name: "missing official endpoint permission",
			payload: []byte(
				`{"provider":"deepseek","schema_version":"model-authority-ceiling/v1","secret_ref":"placeholder","tenant_id":"tenant-1"}`,
			),
		},
		{
			name: "false official endpoint permission",
			payload: []byte(
				`{"allow_official_provider_endpoint":false,"provider":"deepseek","schema_version":"model-authority-ceiling/v1","secret_ref":"placeholder","tenant_id":"tenant-1"}`,
			),
		},
		{
			name: "missing secret ref",
			payload: []byte(
				`{"allow_official_provider_endpoint":true,"provider":"deepseek","schema_version":"model-authority-ceiling/v1","tenant_id":"tenant-1"}`,
			),
		},
		{
			name: "secret value field",
			payload: []byte(
				`{"allow_official_provider_endpoint":true,"api_key":"placeholder","provider":"deepseek","schema_version":"model-authority-ceiling/v1","secret_ref":"placeholder","tenant_id":"tenant-1"}`,
			),
		},
		{
			name: "literal secret field",
			payload: []byte(
				`{"allow_official_provider_endpoint":true,"provider":"deepseek","schema_version":"model-authority-ceiling/v1","secret":"placeholder","secret_ref":"placeholder","tenant_id":"tenant-1"}`,
			),
		},
		{
			name: "extra endpoint field",
			payload: []byte(
				`{"allow_official_provider_endpoint":true,"endpoint":"https://example.invalid","provider":"deepseek","schema_version":"model-authority-ceiling/v1","secret_ref":"placeholder","tenant_id":"tenant-1"}`,
			),
		},
		{
			name: "unknown field",
			payload: []byte(
				`{"allow_official_provider_endpoint":true,"provider":"deepseek","schema_version":"model-authority-ceiling/v1","secret_ref":"placeholder","tenant_id":"tenant-1","x":1}`,
			),
		},
		{
			name:    "oversized",
			payload: bytes.Repeat([]byte{' '}, MaxConfigBytes+1),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := RestoreModelAuthorityCeilingV1(test.payload); err == nil {
				t.Fatal("non-exact model authority wire was accepted")
			}
		})
	}
}

func validModelAuthorityCeilingV1() ModelAuthorityCeilingV1 {
	return ModelAuthorityCeilingV1{
		SchemaVersion:                 ModelAuthorityCeilingSchemaV1,
		TenantID:                      "tenant-1",
		Provider:                      "deepseek",
		SecretRef:                     "placeholder",
		AllowOfficialProviderEndpoint: true,
	}
}
