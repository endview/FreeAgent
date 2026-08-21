package moduleapplyplan

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	profileContextDisableCanaryV1 = `{"binding_target":{"kind":"PROFILE","profile_id":"profile-1"},"desired_state":"DISABLED","expected_pointer_revision":7,"instance_id":"instance-1","port":{"exact_version":"v1","name":"context.provide"},"schema_version":"module-apply-plan/v1","tenant_id":"tenant-1"}`
	profileContextDisableDigestV1 = "6918ee2da16a405e6e5f2f9947c9c56435a5797e224fc7314d452d79124b29ab"
)

func validInputV1() ProfileContextDisableInputV1 {
	return ProfileContextDisableInputV1{
		TenantID:                "tenant-1",
		ExpectedPointerRevision: 7,
		ProfileID:               "profile-1",
		InstanceID:              "instance-1",
	}
}

func TestProfileContextDisableCanaryAndCandidateIDsV1(t *testing.T) {
	plan, canonical, digest, err := FreezeProfileContextDisableV1(validInputV1())
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical) != 264 || string(canonical) != profileContextDisableCanaryV1 {
		t.Fatalf("canonical (%d bytes) = %s", len(canonical), canonical)
	}
	if digest != profileContextDisableDigestV1 {
		t.Fatalf("digest = %q", digest)
	}
	if plan.SchemaVersion != SchemaVersionV1 ||
		plan.DesiredState != DesiredStateDisabledV1 ||
		plan.TenantID != "tenant-1" ||
		plan.ExpectedPointerRevision != 7 ||
		plan.BindingTarget != (ProfileContextDisableBindingTargetV1{
			Kind: BindingTargetProfileV1, ProfileID: "profile-1",
		}) ||
		plan.InstanceID != "instance-1" ||
		plan.Port != (moduleapi.PortRef{
			Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1,
		}) {
		t.Fatalf("plan = %+v", plan)
	}

	restored, err := RestoreProfileContextDisableV1(canonical, digest)
	if err != nil || restored != plan {
		t.Fatalf("restored = %+v, error = %v", restored, err)
	}
	computed, err := DigestCanonicalV1(canonical)
	if err != nil || computed != digest {
		t.Fatalf("computed digest = %q, error = %v", computed, err)
	}
	ids, err := DeriveCandidateIDsV1(digest)
	if err != nil {
		t.Fatal(err)
	}
	if ids.ControlSnapshotID != CandidateControlSnapshotIDPrefixV1+digest ||
		ids.CatalogGenerationID != CandidateCatalogGenerationIDPrefixV1+digest {
		t.Fatalf("candidate IDs = %+v", ids)
	}
}

func TestProfileContextDisableFreezeRejectsInvalidInputV1(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProfileContextDisableInputV1)
	}{
		{"empty tenant", func(v *ProfileContextDisableInputV1) { v.TenantID = "" }},
		{"padded tenant", func(v *ProfileContextDisableInputV1) { v.TenantID = " tenant-1" }},
		{"control tenant", func(v *ProfileContextDisableInputV1) { v.TenantID = "tenant\n1" }},
		{"non NFC tenant", func(v *ProfileContextDisableInputV1) { v.TenantID = "te\u0301nant" }},
		{"long tenant", func(v *ProfileContextDisableInputV1) { v.TenantID = strings.Repeat("t", moduleapi.MaxOpaqueIDBytes+1) }},
		{"empty profile", func(v *ProfileContextDisableInputV1) { v.ProfileID = "" }},
		{"padded profile", func(v *ProfileContextDisableInputV1) { v.ProfileID = "profile-1 " }},
		{"empty instance", func(v *ProfileContextDisableInputV1) { v.InstanceID = "" }},
		{"padded instance", func(v *ProfileContextDisableInputV1) { v.InstanceID = " instance-1" }},
		{"zero revision", func(v *ProfileContextDisableInputV1) { v.ExpectedPointerRevision = 0 }},
		{"exhausted revision", func(v *ProfileContextDisableInputV1) { v.ExpectedPointerRevision = math.MaxInt64 }},
		{"above signed revision", func(v *ProfileContextDisableInputV1) { v.ExpectedPointerRevision = math.MaxUint64 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInputV1()
			test.mutate(&input)
			if _, _, _, err := FreezeProfileContextDisableV1(input); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestRestoreProfileContextDisableRejectsBroaderOrNonExactWireV1(t *testing.T) {
	_, canonical, digest, err := FreezeProfileContextDisableV1(validInputV1())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreProfileContextDisableV1(append([]byte(" "), canonical...), digest); err == nil {
		t.Fatal("non-canonical surrounding whitespace accepted")
	}
	if _, err := RestoreProfileContextDisableV1(canonical, strings.Repeat("0", moduleapi.SHA256HexLength)); err == nil {
		t.Fatal("wrong digest accepted")
	}
	for _, invalid := range []string{"", "ABC", strings.Repeat("A", moduleapi.SHA256HexLength)} {
		if _, err := RestoreProfileContextDisableV1(canonical, invalid); err == nil {
			t.Fatalf("invalid digest %q accepted", invalid)
		}
	}

	mutations := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown top field", func(v map[string]any) { v["module"] = map[string]any{} }},
		{"missing top field", func(v map[string]any) { delete(v, "tenant_id") }},
		{"null top field", func(v map[string]any) { v["instance_id"] = nil }},
		{"wrong schema", func(v map[string]any) { v["schema_version"] = "module-apply-plan/v2" }},
		{"enabled", func(v map[string]any) { v["desired_state"] = "ENABLED" }},
		{"zero revision", func(v map[string]any) { v["expected_pointer_revision"] = float64(0) }},
		{"wrong target kind", func(v map[string]any) { v["binding_target"].(map[string]any)["kind"] = "WORKSPACE_CHANNEL_ENDPOINT" }},
		{"unknown target field", func(v map[string]any) { v["binding_target"].(map[string]any)["workspace_id"] = "workspace-1" }},
		{"missing target field", func(v map[string]any) { delete(v["binding_target"].(map[string]any), "profile_id") }},
		{"null target field", func(v map[string]any) { v["binding_target"].(map[string]any)["profile_id"] = nil }},
		{"wrong port name", func(v map[string]any) { v["port"].(map[string]any)["name"] = "action.provider" }},
		{"wrong port version", func(v map[string]any) { v["port"].(map[string]any)["exact_version"] = "v2" }},
		{"unknown port field", func(v map[string]any) { v["port"].(map[string]any)["extra"] = "x" }},
		{"missing port field", func(v map[string]any) { delete(v["port"].(map[string]any), "name") }},
		{"null port field", func(v map[string]any) { v["port"].(map[string]any)["name"] = nil }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			mutated := canonicalMutationV1(t, canonical, test.mutate)
			mutatedDigest := moduleapi.Digest(DigestDomainV1, mutated)
			if _, err := RestoreProfileContextDisableV1(mutated, mutatedDigest); err == nil {
				t.Fatalf("broader wire accepted: %s", mutated)
			}
			if _, err := DigestCanonicalV1(mutated); err == nil {
				t.Fatalf("digest accepted broader wire: %s", mutated)
			}
		})
	}

	for _, malformed := range [][]byte{
		[]byte(`null`),
		[]byte(`[]`),
		[]byte(`{}`),
		[]byte(`{"schema_version":"module-apply-plan/v1","schema_version":"module-apply-plan/v1"}`),
		bytes.Repeat([]byte("x"), MaxCanonicalBytesV1+1),
	} {
		if _, err := DigestCanonicalV1(malformed); err == nil {
			t.Fatalf("malformed wire accepted: %.80q", malformed)
		}
	}
}

func TestProfileContextDisableCanonicalBytesAreDetachedV1(t *testing.T) {
	_, first, firstDigest, err := FreezeProfileContextDisableV1(validInputV1())
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'x'
	_, second, secondDigest, err := FreezeProfileContextDisableV1(validInputV1())
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != profileContextDisableCanaryV1 || secondDigest != firstDigest {
		t.Fatalf("returned bytes alias internal state: %s %s", second, secondDigest)
	}
}

func TestDeriveCandidateIDsRejectsInvalidDigestV1(t *testing.T) {
	for _, invalid := range []string{
		"", "abc", strings.Repeat("A", moduleapi.SHA256HexLength), strings.Repeat("0", moduleapi.SHA256HexLength-1),
	} {
		if _, err := DeriveCandidateIDsV1(invalid); err == nil {
			t.Fatalf("invalid digest %q accepted", invalid)
		}
	}
}

func canonicalMutationV1(
	t *testing.T,
	canonical []byte,
	mutate func(map[string]any),
) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	mutated, err := moduleapi.CanonicalJSONWithLimits(encoded, canonicalLimitsV1())
	if err != nil {
		t.Fatal(err)
	}
	return mutated
}
