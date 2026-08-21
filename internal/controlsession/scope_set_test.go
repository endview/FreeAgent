package controlsession

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
)

func TestAuthorizedScopeSetCanonicalCanary(t *testing.T) {
	input := []controlapicontract.ControlScopeV1{
		testWorkspaceScopeV1("tenant-b", "workspace-b"),
		testWorkspaceScopeV1("tenant-a", "workspace-z"),
		testTenantScopeV1("tenant-a"),
	}
	set, err := NewAuthorizedScopeSetV1(input)
	if err != nil {
		t.Fatal(err)
	}
	const wantWire = `{"schema_version":"control-scope-set/v1","scopes":[{"kind":"TENANT","schema_version":"control-scope/v1","tenant_id":"tenant-a"},{"kind":"WORKSPACE","schema_version":"control-scope/v1","tenant_id":"tenant-a","workspace_id":"workspace-z"},{"kind":"WORKSPACE","schema_version":"control-scope/v1","tenant_id":"tenant-b","workspace_id":"workspace-b"}]}`
	const wantDigest = "37a728ec2aa0b413e796bca9b355be8bacca74e04e2162d857c8f387380140fa"
	if got := string(set.Canonical()); got != wantWire {
		t.Fatalf("canonical ScopeSet changed\ngot:  %s\nwant: %s", got, wantWire)
	}
	if got := set.Digest(); got != wantDigest {
		t.Fatalf("ScopeSet digest = %s, want %s", got, wantDigest)
	}

	reordered, err := NewAuthorizedScopeSetV1([]controlapicontract.ControlScopeV1{
		input[2], input[0], input[1],
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reordered.Canonical(), set.Canonical()) ||
		reordered.Digest() != set.Digest() {
		t.Fatal("input ordering changed the canonical ScopeSet identity")
	}
}

func TestAuthorizedScopeSetRejectsInvalidCardinalityAndDuplicates(t *testing.T) {
	if _, err := NewAuthorizedScopeSetV1(nil); err == nil {
		t.Fatal("empty ScopeSet was accepted")
	}
	duplicate := testWorkspaceScopeV1("tenant-a", "workspace-a")
	if _, err := NewAuthorizedScopeSetV1([]controlapicontract.ControlScopeV1{
		duplicate, duplicate,
	}); err == nil {
		t.Fatal("duplicate ScopeSet entry was silently deduplicated")
	}
	invalid := duplicate
	invalid.SchemaVersion = "control-scope/v2"
	if _, err := NewAuthorizedScopeSetV1([]controlapicontract.ControlScopeV1{invalid}); err == nil {
		t.Fatal("invalid Control scope was accepted")
	}
	tooMany := make([]controlapicontract.ControlScopeV1, MaximumAuthorizedScopesV1+1)
	for index := range tooMany {
		tooMany[index] = testWorkspaceScopeV1(
			"tenant-a",
			fmt.Sprintf("workspace-%03d", index),
		)
	}
	if _, err := NewAuthorizedScopeSetV1(tooMany); err == nil {
		t.Fatal("oversized ScopeSet was accepted")
	}
}

func TestAuthorizedScopeSetCeilingAndDefensiveCopies(t *testing.T) {
	input := []controlapicontract.ControlScopeV1{
		testWorkspaceScopeV1("tenant-workspace", "workspace-a"),
		testTenantScopeV1("tenant-wide"),
	}
	set, err := NewAuthorizedScopeSetV1(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0].WorkspaceID = "mutated"
	canonical := set.Canonical()
	canonical[0] = 'x'
	scopes := set.Scopes()
	scopes[0].TenantID = "mutated"
	if strings.HasPrefix(string(set.Canonical()), "x") ||
		set.Scopes()[0].TenantID == "mutated" {
		t.Fatal("ScopeSet aliases caller or getter storage")
	}

	tests := []struct {
		name  string
		scope controlapicontract.ControlScopeV1
		want  bool
	}{
		{"tenant exact", testTenantScopeV1("tenant-wide"), true},
		{"tenant covers workspace", testWorkspaceScopeV1("tenant-wide", "any-workspace"), true},
		{"workspace exact", testWorkspaceScopeV1("tenant-workspace", "workspace-a"), true},
		{"workspace does not cover sibling", testWorkspaceScopeV1("tenant-workspace", "workspace-b"), false},
		{"workspace does not cover tenant", testTenantScopeV1("tenant-workspace"), false},
		{"cross tenant", testWorkspaceScopeV1("tenant-other", "workspace-a"), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := set.Allows(test.scope); got != test.want {
				t.Fatalf("Allows() = %v, want %v", got, test.want)
			}
		})
	}
	bad := testWorkspaceScopeV1("tenant-wide", "workspace-a")
	bad.Kind = "PREFIX"
	if set.Allows(bad) {
		t.Fatal("ScopeSet allowed an invalid scope")
	}
}

func testTenantScopeV1(tenant string) controlapicontract.ControlScopeV1 {
	return controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeTenantV1,
		TenantID:      tenant,
	}
}

func testWorkspaceScopeV1(
	tenant string,
	workspace string,
) controlapicontract.ControlScopeV1 {
	return controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeWorkspaceV1,
		TenantID:      tenant,
		WorkspaceID:   workspace,
	}
}
