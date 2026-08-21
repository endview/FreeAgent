package currentstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestW2E5AChannelManifestDeclarationsFailClosedAcrossSharedClosure(
	t *testing.T,
) {
	tests := []struct {
		name  string
		extra map[string]any
	}{
		{
			name: "Requires",
			extra: map[string]any{
				"requires": []moduleapi.PortRef{{
					Name:         moduleapi.PortNameModelGenerate,
					ExactVersion: moduleapi.PortVersionV1,
				}},
			},
		},
		{
			name: "requested permissions",
			extra: map[string]any{
				"requested_permissions": []moduleapi.Permission{
					moduleapi.PermissionKnowledgeReadV1,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdmissionCommitFixture(t)
			bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
			control, err := controlcontract.RestoreControlSnapshot(
				fixture.controlCanonical,
				fixture.basis.Control,
			)
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := controlcontract.RestoreCatalogGeneration(
				fixture.catalogCanonical,
				fixture.basis.Catalog,
			)
			if err != nil {
				t.Fatal(err)
			}

			replaceChannelManifestForE5A(t, fixture, test.extra)
			beforePublication := publicationRowCounts(t, fixture.store)
			beforeReceipts := countTableRows(
				t,
				fixture.store,
				"channel_ingress_receipts",
			)
			exactRetry := PublishControlCatalogInput{
				ExpectedPointerRevision: fixture.basis.PointerRevision - 1,
				NewPointerRevision:      fixture.basis.PointerRevision,
				ControlRef:              fixture.basis.Control,
				ControlCanonical:        fixture.controlCanonical,
				CatalogRef:              fixture.basis.Catalog,
				CatalogCanonical:        fixture.catalogCanonical,
			}
			if _, err := fixture.store.PublishControlCatalog(
				context.Background(),
				exactRetry,
			); !errors.Is(err, ErrPublicationConflict) ||
				!strings.Contains(err.Error(), "ChannelEndpoint Manifest") {
				t.Fatalf("exact retry declaration error=%v", err)
			}
			if err := VerifyPublishedControlCatalogClosureV1(
				context.Background(),
				fixture.store.db,
				control,
				catalog,
			); !errors.Is(err, ErrPublicationConflict) ||
				!strings.Contains(err.Error(), "ChannelEndpoint Manifest") {
				t.Fatalf("public Verify declaration error=%v", err)
			}
			atomicInput := atomicEnabledChannelPublicationInput(
				t,
				fixture,
				bindingDigest,
			)
			if _, _, err := fixture.store.PublishControlCatalogWithChannelCursorSeed(
				context.Background(),
				atomicInput,
			); !errors.Is(err, ErrPublicationConflict) ||
				!strings.Contains(err.Error(), "ChannelEndpoint Manifest") {
				t.Fatalf("atomic Channel publication declaration error=%v", err)
			}
			if after := publicationRowCounts(t, fixture.store); after != beforePublication {
				t.Fatalf("failed Channel closure wrote publication rows: before=%v after=%v", beforePublication, after)
			}
			if after := countTableRows(
				t,
				fixture.store,
				"channel_ingress_receipts",
			); after != beforeReceipts {
				t.Fatalf("failed Channel closure wrote receipts: before=%d after=%d", beforeReceipts, after)
			}
		})
	}
}

func replaceChannelManifestForE5A(
	t *testing.T,
	fixture *admissionCommitFixture,
	extra map[string]any,
) {
	t.Helper()
	port := moduleapi.PortRef{
		Name:         moduleapi.PortNameChannelTransport,
		ExactVersion: moduleapi.PortVersionV1,
	}
	declarations := make(map[string]any, len(extra)+1)
	declarations["provides"] = []moduleapi.PortRef{port}
	for key, value := range extra {
		declarations[key] = value
	}
	manifest := canonicalModuleManifest(
		t,
		"firstparty.loopback.channel",
		"v1",
		moduleapi.RuntimeModeRequestTrustedInProcess,
		declarations,
	)
	manifestRef := putPublicationJSON(
		t,
		fixture.store,
		ContentModuleManifest,
		manifest,
	)
	result, err := fixture.store.db.Exec(`
		UPDATE module_installations
		SET manifest_ref=?
		WHERE installation_id='installation-channel'
	`, manifestRef)
	if err != nil {
		t.Fatalf("replace Channel Manifest: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("replace Channel Manifest affected=%d error=%v", affected, err)
	}
}
