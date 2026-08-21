package moduleapi

import (
	"strings"
	"testing"
)

func TestCurrentModulePrimitivesValidateExactWireValues(t *testing.T) {
	ref := Ref{ID: "context.agent-role", Version: "2026.08+build_1"}
	key, err := ref.CanonicalKey()
	if err != nil {
		t.Fatal(err)
	}
	if key != "context.agent-role@2026.08+build_1" {
		t.Fatalf("canonical key = %q", key)
	}
	if err := (Permission("workspace.read")).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []FailurePolicy{FailureRequired, FailureOptional} {
		if err := policy.Validate(); err != nil {
			t.Fatalf("valid failure policy %q rejected: %v", policy, err)
		}
	}
	if !ValidSHA256(strings.Repeat("a", SHA256HexLength)) {
		t.Fatal("lowercase SHA-256 digest rejected")
	}
}

func TestCurrentModulePrimitivesRejectLegacyOrAmbiguousValues(t *testing.T) {
	for name, run := range map[string]func() error{
		"module id": func() error {
			return (Ref{ID: "Context.Role", Version: "1"}).Validate()
		},
		"module version": func() error {
			return (Ref{ID: "context.role", Version: "^1"}).Validate()
		},
		"permission": func() error {
			return (Permission("Workspace.Read")).Validate()
		},
		"failure policy": func() error {
			return FailurePolicy("FALLBACK").Validate()
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("invalid value was accepted")
			}
		})
	}
	if ValidSHA256(strings.Repeat("A", SHA256HexLength)) {
		t.Fatal("uppercase SHA-256 digest was accepted")
	}
}
