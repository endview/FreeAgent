package currentstore

import (
	"context"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCheckCurrentActivationAllowsExactThenDeniesRevokedProvider(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatal(err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(
		fixture.input.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	port, provider := exactTestModelProvider(t, member)
	if err := fixture.store.CheckCurrentActivation(
		context.Background(),
		"run-admitted",
		port,
		provider,
	); err != nil {
		t.Fatalf("active Provider denied: %v", err)
	}

	publishEmptyCurrentCatalog(
		t,
		fixture.store,
		fixture.intent.TenantID,
		fixture.basis.PointerRevision,
	)
	if err := fixture.store.CheckCurrentActivation(
		context.Background(),
		"run-admitted",
		port,
		provider,
	); !errors.Is(err, ErrCurrentActivationDenied) {
		t.Fatalf("revoked Provider error = %v", err)
	}
}

func exactTestModelProvider(
	t *testing.T,
	member corecontract.MemberExecutionSnapshot,
) (moduleapi.PortRef, moduleapi.ActivatedModuleRef) {
	t.Helper()
	for _, plan := range member.PortPlans {
		if plan.Port.Name != moduleapi.PortNameModelGenerate ||
			plan.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		if len(plan.Bindings) != 1 {
			t.Fatalf("model PortPlan Bindings = %d", len(plan.Bindings))
		}
		return plan.Port, plan.Bindings[0].Provider
	}
	t.Fatal("model PortPlan is absent")
	return moduleapi.PortRef{}, moduleapi.ActivatedModuleRef{}
}

func publishEmptyCurrentCatalog(
	t *testing.T,
	store *Store,
	tenantID string,
	expectedPointerRevision uint64,
) {
	t.Helper()
	control, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(controlcontract.ControlSnapshot{
			SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV1,
			SnapshotID:    "control-revoked",
			TenantID:      tenantID,
			Revision:      expectedPointerRevision + 1,
			Agents:        []corecontract.AgentRef{},
			Workspaces:    []controlcontract.WorkspaceDefinition{},
			Profiles:      []controlcontract.ProfileDefinition{},
		})
	if err != nil {
		t.Fatal(err)
	}
	_ = control
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(
			controlcontract.CatalogGeneration{
				SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
				GenerationID:          "catalog-revoked",
				Generation:            expectedPointerRevision + 1,
				TenantID:              tenantID,
				ControlSnapshotID:     controlRef.SnapshotID,
				ControlSnapshotDigest: controlRef.Digest,
				Entries:               []controlcontract.CatalogEntry{},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: expectedPointerRevision,
			NewPointerRevision:      expectedPointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatalf("publish revoked current Catalog: %v", err)
	}
}
