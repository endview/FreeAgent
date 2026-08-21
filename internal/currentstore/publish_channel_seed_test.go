package currentstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
)

func TestPublishControlCatalogWithChannelCursorSeedIsAtomicAndRetryReadsRevisionZero(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
	input := atomicEnabledChannelPublicationInput(t, fixture, bindingDigest)

	basis, seed, err := fixture.store.PublishControlCatalogWithChannelCursorSeed(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("atomic publication and seed: %v", err)
	}
	if basis != input.CursorSeed.PublishedBasis || !seed.Created ||
		seed.CursorRevision != 0 || seed.Disposition != ChannelCursorSeed {
		t.Fatalf("basis=%+v seed=%+v", basis, seed)
	}
	storedSeed, err := fixture.store.GetChannelCursorSeed(
		context.Background(),
		seed.TenantID,
		seed.EndpointID,
		seed.CursorScopeKey,
	)
	if err != nil || storedSeed.CursorAfterRef != seed.CursorAfterRef || storedSeed.Created {
		t.Fatalf("stored revision-zero seed=%+v error=%v", storedSeed, err)
	}

	// Advance the cursor, then prove an exact Apply retry still validates the
	// fixed revision-zero receipt rather than MAX(revision).
	fixture.basis = basis
	fixture.controlCanonical = input.Publication.ControlCanonical
	fixture.catalogCanonical = input.Publication.CatalogCanonical
	acceptedInput := channelAcceptedFixture(
		t,
		fixture,
		bindingDigest,
		seed,
		"atomic-seed-event",
		"atomic-seed-run",
	)
	if _, err := fixture.store.CommitChannelIngressAndRunAdmission(
		context.Background(),
		acceptedInput,
	); err != nil {
		t.Fatalf("advance seeded cursor: %v", err)
	}
	current, err := fixture.store.GetCurrentChannelCursor(
		context.Background(),
		seed.TenantID,
		seed.EndpointID,
		seed.CursorScopeKey,
	)
	if err != nil || current.CursorRevision != 1 {
		t.Fatalf("current cursor=%+v error=%v", current, err)
	}

	retryBasis, retrySeed, err :=
		fixture.store.PublishControlCatalogWithChannelCursorSeed(
			context.Background(),
			input,
		)
	if err != nil || retryBasis != basis || retrySeed.Created ||
		retrySeed.CursorRevision != 0 || retrySeed.CursorAfterRef != seed.CursorAfterRef {
		t.Fatalf("exact retry basis=%+v seed=%+v error=%v", retryBasis, retrySeed, err)
	}

	observer := openPreparedReadOnlyObserver(t, fixture.store)
	defer observer.Close()
	observed, err := observer.GetChannelCursorSeed(
		context.Background(),
		seed.TenantID,
		seed.EndpointID,
		seed.CursorScopeKey,
	)
	if err != nil || observed.CursorRevision != 0 ||
		observed.CursorAfterRef != seed.CursorAfterRef {
		t.Fatalf("observer revision-zero seed=%+v error=%v", observed, err)
	}
}

func TestPublishControlCatalogWithChannelCursorSeedRejectsConflictsWithoutWrites(
	t *testing.T,
) {
	tests := []struct {
		name    string
		wantErr error
		mutate  func(*PublishControlCatalogWithChannelCursorSeedInput)
	}{
		{
			name:    "workspace differs",
			wantErr: ErrChannelIngressConflict,
			mutate: func(input *PublishControlCatalogWithChannelCursorSeedInput) {
				input.CursorSeed.WorkspaceID = "workspace-other"
			},
		},
		{
			name:    "Endpoint differs",
			wantErr: ErrChannelIngressConflict,
			mutate: func(input *PublishControlCatalogWithChannelCursorSeedInput) {
				input.CursorSeed.EndpointID = "endpoint-other"
			},
		},
		{
			name:    "cursor scope differs",
			wantErr: ErrChannelIngressConflict,
			mutate: func(input *PublishControlCatalogWithChannelCursorSeedInput) {
				input.CursorSeed.CursorScopeKey = "conversation-other"
			},
		},
		{
			name:    "Binding digest differs",
			wantErr: ErrChannelIngressConflict,
			mutate: func(input *PublishControlCatalogWithChannelCursorSeedInput) {
				input.CursorSeed.EndpointBindingDigest = strings.Repeat("a", 64)
			},
		},
		{
			name:    "PublishedBasis differs",
			wantErr: ErrInvalidChannelIngress,
			mutate: func(input *PublishControlCatalogWithChannelCursorSeedInput) {
				input.CursorSeed.PublishedBasis.PointerRevision--
			},
		},
		{
			name:    "stale pointer CAS",
			wantErr: ErrPublicationConflict,
			mutate: func(input *PublishControlCatalogWithChannelCursorSeedInput) {
				input.Publication.ExpectedPointerRevision--
				input.Publication.NewPointerRevision--
				input.CursorSeed.PublishedBasis.PointerRevision--
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdmissionCommitFixture(t)
			bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
			input := atomicEnabledChannelPublicationInput(t, fixture, bindingDigest)
			test.mutate(&input)
			beforePublication := publicationRowCounts(t, fixture.store)
			beforeContent := countTableRows(t, fixture.store, "content_records")

			if _, _, err := fixture.store.PublishControlCatalogWithChannelCursorSeed(
				context.Background(),
				input,
			); !errors.Is(err, test.wantErr) {
				t.Fatalf("error=%v want %v", err, test.wantErr)
			}
			if after := publicationRowCounts(t, fixture.store); after != beforePublication {
				t.Fatalf("publication rows changed: before=%v after=%v", beforePublication, after)
			}
			if after := countTableRows(t, fixture.store, "content_records"); after != beforeContent {
				t.Fatalf("content rows changed: before=%d after=%d", beforeContent, after)
			}
			if got := countTableRows(t, fixture.store, "channel_ingress_receipts"); got != 0 {
				t.Fatalf("failed atomic operation left %d cursor receipts", got)
			}
			current, _, _, err := fixture.store.LoadPublishedBasis(
				context.Background(),
				fixture.basis.TenantID,
			)
			if err != nil || current != fixture.basis {
				t.Fatalf("current basis=%+v want %+v error=%v", current, fixture.basis, err)
			}
		})
	}
}

func TestPublishControlCatalogWithChannelCursorSeedConflictRollsBackPublication(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
	input := atomicEnabledChannelPublicationInput(t, fixture, bindingDigest)
	if _, err := fixture.store.db.Exec(`
		CREATE TRIGGER fail_atomic_cursor_seed
		BEFORE INSERT ON channel_ingress_receipts
		WHEN NEW.disposition='CURSOR_SEED'
		BEGIN
			SELECT RAISE(ABORT, 'forced atomic cursor seed failure');
		END
	`); err != nil {
		t.Fatalf("create cursor seed fault trigger: %v", err)
	}
	beforePublication := publicationRowCounts(t, fixture.store)
	beforeContent := countTableRows(t, fixture.store, "content_records")

	if _, _, err := fixture.store.PublishControlCatalogWithChannelCursorSeed(
		context.Background(),
		input,
	); err == nil {
		t.Fatal("fault-injected atomic publication unexpectedly succeeded")
	}
	if after := publicationRowCounts(t, fixture.store); after != beforePublication {
		t.Fatalf("publication rows changed: before=%v after=%v", beforePublication, after)
	}
	if after := countTableRows(t, fixture.store, "content_records"); after != beforeContent {
		t.Fatalf("cursor content leaked: before=%d after=%d", beforeContent, after)
	}
	if got := countTableRows(t, fixture.store, "channel_ingress_receipts"); got != 0 {
		t.Fatalf("fault-injected operation left %d cursor receipts", got)
	}
}

func TestPublishControlCatalogWithChannelCursorSeedRejectsDifferentExactSeed(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	bindingDigest := installDisabledChannelEndpointFixture(t, fixture)
	input := atomicEnabledChannelPublicationInput(t, fixture, bindingDigest)
	if _, _, err := fixture.store.PublishControlCatalogWithChannelCursorSeed(
		context.Background(),
		input,
	); err != nil {
		t.Fatal(err)
	}
	beforePublication := publicationRowCounts(t, fixture.store)
	beforeContent := countTableRows(t, fixture.store, "content_records")
	input.CursorSeed.CursorAfter = channelCursorContent(t, `{"offset":99}`)

	if _, _, err := fixture.store.PublishControlCatalogWithChannelCursorSeed(
		context.Background(),
		input,
	); !errors.Is(err, ErrChannelIngressConflict) {
		t.Fatalf("different exact seed error=%v", err)
	}
	if after := publicationRowCounts(t, fixture.store); after != beforePublication {
		t.Fatalf("conflicting retry changed publication rows: before=%v after=%v", beforePublication, after)
	}
	if after := countTableRows(t, fixture.store, "content_records"); after != beforeContent {
		t.Fatalf("conflicting retry changed content rows: before=%d after=%d", beforeContent, after)
	}
}

func atomicEnabledChannelPublicationInput(
	t *testing.T,
	fixture *admissionCommitFixture,
	bindingDigest string,
) PublishControlCatalogWithChannelCursorSeedInput {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.controlCanonical,
		fixture.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	control.Revision++
	control.SnapshotID = fmt.Sprintf("control-channel-atomic-%d", control.Revision)
	control.Digest = ""
	control.Workspaces[0].ChannelEndpoints[0].Enabled = true
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
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
	catalog.Generation++
	catalog.GenerationID = fmt.Sprintf("catalog-channel-atomic-%d", catalog.Generation)
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis := controlcontract.PublishedBasis{
		TenantID:        fixture.basis.TenantID,
		PointerRevision: fixture.basis.PointerRevision + 1,
		Control:         controlRef,
		Catalog:         catalogRef,
	}
	seed := channelCursorSeedInput(t, fixture, bindingDigest)
	seed.PublishedBasis = basis
	return PublishControlCatalogWithChannelCursorSeedInput{
		Publication: PublishControlCatalogInput{
			ExpectedPointerRevision: fixture.basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
		CursorSeed: seed,
	}
}
