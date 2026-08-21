package currentstore

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestLoadPublishedBasisReturnsExactCurrentContracts(t *testing.T) {
	fixture := newPublicationFixture(t)
	first := fixture.input(
		t,
		"basis-snapshot-1",
		1,
		"basis-catalog-1",
		1,
		0,
		1,
		nil,
	)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		first,
	); err != nil {
		t.Fatalf("publish first basis: %v", err)
	}
	second := fixture.input(
		t,
		"basis-snapshot-2",
		2,
		"basis-catalog-2",
		2,
		1,
		2,
		nil,
	)
	wantBasis, err := fixture.store.PublishControlCatalog(
		context.Background(),
		second,
	)
	if err != nil {
		t.Fatalf("publish second basis: %v", err)
	}
	before := publicationRowCounts(t, fixture.store)

	gotBasis, gotControl, gotCatalog, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.tenantID,
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis: %v", err)
	}
	if gotBasis != wantBasis {
		t.Fatalf("basis=%+v want %+v", gotBasis, wantBasis)
	}

	wantControl, err := controlcontract.RestoreControlSnapshot(
		second.ControlCanonical,
		second.ControlRef,
	)
	if err != nil {
		t.Fatalf("restore expected ControlSnapshot: %v", err)
	}
	wantCatalog, err := controlcontract.RestoreCatalogGeneration(
		second.CatalogCanonical,
		second.CatalogRef,
	)
	if err != nil {
		t.Fatalf("restore expected CatalogGeneration: %v", err)
	}
	if !reflect.DeepEqual(gotControl, wantControl) {
		t.Fatalf("ControlSnapshot=%+v want %+v", gotControl, wantControl)
	}
	if !reflect.DeepEqual(gotCatalog, wantCatalog) {
		t.Fatalf("CatalogGeneration=%+v want %+v", gotCatalog, wantCatalog)
	}

	_, rebuiltControlRef, rebuiltControlCanonical, err :=
		controlcontract.NewControlSnapshot(gotControl)
	if err != nil {
		t.Fatalf("rebuild loaded ControlSnapshot: %v", err)
	}
	if rebuiltControlRef != second.ControlRef ||
		!bytes.Equal(rebuiltControlCanonical, second.ControlCanonical) {
		t.Fatalf(
			"rebuilt ControlSnapshot ref/bytes differ: ref=%+v want %+v",
			rebuiltControlRef,
			second.ControlRef,
		)
	}
	_, rebuiltCatalogRef, rebuiltCatalogCanonical, err :=
		controlcontract.NewCatalogGeneration(gotCatalog)
	if err != nil {
		t.Fatalf("rebuild loaded CatalogGeneration: %v", err)
	}
	if rebuiltCatalogRef != second.CatalogRef ||
		!bytes.Equal(rebuiltCatalogCanonical, second.CatalogCanonical) {
		t.Fatalf(
			"rebuilt CatalogGeneration ref/bytes differ: ref=%+v want %+v",
			rebuiltCatalogRef,
			second.CatalogRef,
		)
	}
	if after := publicationRowCounts(t, fixture.store); after != before {
		t.Fatalf(
			"LoadPublishedBasis changed publication rows: before=%v after=%v",
			before,
			after,
		)
	}
}

func TestLoadControlCatalogRevisionReturnsExactHistoricalPair(t *testing.T) {
	fixture := newPublicationFixture(t)
	first := fixture.input(
		t,
		"historical-snapshot-1",
		1,
		"historical-catalog-1",
		1,
		0,
		1,
		nil,
	)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		first,
	); err != nil {
		t.Fatalf("publish first basis: %v", err)
	}
	second := fixture.input(
		t,
		"historical-snapshot-2",
		2,
		"historical-catalog-2",
		2,
		1,
		2,
		nil,
	)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		second,
	); err != nil {
		t.Fatalf("publish second basis: %v", err)
	}

	control, catalog, err := fixture.store.LoadControlCatalogRevision(
		context.Background(),
		fixture.tenantID,
		1,
		1,
	)
	if err != nil {
		t.Fatalf("LoadControlCatalogRevision: %v", err)
	}
	wantControl, err := controlcontract.RestoreControlSnapshot(
		first.ControlCanonical,
		first.ControlRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantCatalog, err := controlcontract.RestoreCatalogGeneration(
		first.CatalogCanonical,
		first.CatalogRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(control, wantControl) ||
		!reflect.DeepEqual(catalog, wantCatalog) {
		t.Fatalf(
			"historical pair differs: control=%+v catalog=%+v",
			control,
			catalog,
		)
	}
	if _, _, err := fixture.store.LoadControlCatalogRevision(
		context.Background(),
		fixture.tenantID,
		1,
		2,
	); !errors.Is(err, ErrPublishedBasisNotFound) {
		t.Fatalf("mismatched historical pair error=%v", err)
	}
}

func TestLoadPublishedBasisReturnsExplicitNotFound(t *testing.T) {
	store := openModuleTestStore(t)

	basis, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		"tenant-without-current",
	)
	if !errors.Is(err, ErrPublishedBasisNotFound) {
		t.Fatalf(
			"LoadPublishedBasis error=%v want ErrPublishedBasisNotFound",
			err,
		)
	}
	if basis != (controlcontract.PublishedBasis{}) ||
		!reflect.DeepEqual(control, controlcontract.ControlSnapshot{}) ||
		!reflect.DeepEqual(catalog, controlcontract.CatalogGeneration{}) {
		t.Fatalf(
			"not-found result is not empty: basis=%+v control=%+v catalog=%+v",
			basis,
			control,
			catalog,
		)
	}
}

func TestLoadPublishedBasisRejectsDamagedStoredRows(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *publicationFixture, PublishControlCatalogInput)
	}{
		{
			name: "control canonical",
			mutate: func(
				t *testing.T,
				fixture *publicationFixture,
				input PublishControlCatalogInput,
			) {
				t.Helper()
				execClosedFileTamperV1(t, fixture.store,
					[]string{"control_snapshots_reject_update"},
					`
					UPDATE control_snapshots
					SET canonical_json=?
					WHERE snapshot_id=?
					`, []byte(`{"broken":true}`), input.ControlRef.SnapshotID,
				)
			},
		},
		{
			name: "catalog digest projection",
			mutate: func(
				t *testing.T,
				fixture *publicationFixture,
				input PublishControlCatalogInput,
			) {
				t.Helper()
				digest := strings.Repeat("f", 64)
				if digest == input.CatalogRef.Digest {
					digest = strings.Repeat("e", 64)
				}
				execClosedFileTamperV1(t, fixture.store,
					[]string{"runtime_catalog_generations_reject_update"},
					`
					UPDATE runtime_catalog_generations
					SET digest=?
					WHERE generation_id=?
					`, digest, input.CatalogRef.GenerationID,
				)
			},
		},
		{
			name: "catalog generation projection",
			mutate: func(
				t *testing.T,
				fixture *publicationFixture,
				input PublishControlCatalogInput,
			) {
				t.Helper()
				execClosedFileTamperV1(t, fixture.store,
					[]string{"runtime_catalog_generations_reject_update"},
					`
					UPDATE runtime_catalog_generations
					SET generation=generation+10
					WHERE generation_id=?
					`, input.CatalogRef.GenerationID,
				)
			},
		},
		{
			name: "control tenant projection",
			mutate: func(
				t *testing.T,
				fixture *publicationFixture,
				input PublishControlCatalogInput,
			) {
				t.Helper()
				execClosedFileTamperV1(t, fixture.store,
					[]string{"control_snapshots_reject_update"},
					`
					UPDATE control_snapshots
					SET tenant_id=?
					WHERE snapshot_id=?
				`,
					"another-tenant",
					input.ControlRef.SnapshotID,
				)
			},
		},
		{
			name: "missing catalog row",
			mutate: func(
				t *testing.T,
				fixture *publicationFixture,
				input PublishControlCatalogInput,
			) {
				t.Helper()
				execClosedFileTamperV1(t, fixture.store,
					[]string{"runtime_catalog_generations_reject_delete"},
					`
					DELETE FROM runtime_catalog_generations
					WHERE generation_id=?
					`, input.CatalogRef.GenerationID,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPublicationFixture(t)
			input := fixture.input(
				t,
				"damaged-basis-snapshot",
				1,
				"damaged-basis-catalog",
				1,
				0,
				1,
				nil,
			)
			if _, err := fixture.store.PublishControlCatalog(
				context.Background(),
				input,
			); err != nil {
				t.Fatalf("publish basis: %v", err)
			}
			test.mutate(t, fixture, input)

			_, _, _, err := fixture.store.LoadPublishedBasis(
				context.Background(),
				fixture.tenantID,
			)
			if !errors.Is(err, ErrPublishedBasisIntegrity) {
				t.Fatalf(
					"LoadPublishedBasis error=%v want ErrPublishedBasisIntegrity",
					err,
				)
			}
		})
	}
}

func TestLoadPublishedBasisRejectsMismatchedCurrentClosure(t *testing.T) {
	fixture := newPublicationFixture(t)
	first := fixture.input(
		t,
		"closure-snapshot-1",
		1,
		"closure-catalog-1",
		1,
		0,
		1,
		nil,
	)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		first,
	); err != nil {
		t.Fatalf("publish first basis: %v", err)
	}
	second := fixture.input(
		t,
		"closure-snapshot-2",
		2,
		"closure-catalog-2",
		2,
		1,
		2,
		nil,
	)
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		second,
	); err != nil {
		t.Fatalf("publish second basis: %v", err)
	}
	execClosedFileTamperV1(t, fixture.store, nil, `
		UPDATE control_current
		SET snapshot_id=?
		WHERE tenant_id=?
	`, first.ControlRef.SnapshotID, fixture.tenantID)

	_, _, _, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.tenantID,
	)
	if !errors.Is(err, ErrPublishedBasisIntegrity) {
		t.Fatalf(
			"LoadPublishedBasis error=%v want ErrPublishedBasisIntegrity",
			err,
		)
	}
}

func TestLoadPublishedBasisReturnsDefensiveCopies(t *testing.T) {
	fixture := newPublicationFixture(t)
	input := fixture.input(
		t,
		"copy-snapshot",
		1,
		"copy-catalog",
		1,
		0,
		1,
		nil,
	)
	wantBasis, err := fixture.store.PublishControlCatalog(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("publish basis: %v", err)
	}
	wantControl, err := controlcontract.RestoreControlSnapshot(
		input.ControlCanonical,
		input.ControlRef,
	)
	if err != nil {
		t.Fatalf("restore expected ControlSnapshot: %v", err)
	}
	wantCatalog, err := controlcontract.RestoreCatalogGeneration(
		input.CatalogCanonical,
		input.CatalogRef,
	)
	if err != nil {
		t.Fatalf("restore expected CatalogGeneration: %v", err)
	}

	basis, control, catalog, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.tenantID,
	)
	if err != nil {
		t.Fatalf("first LoadPublishedBasis: %v", err)
	}
	if len(control.Profiles) == 0 ||
		len(control.Profiles[0].Bindings) == 0 ||
		len(catalog.Entries) == 0 ||
		len(catalog.Entries[0].Provides) == 0 {
		t.Fatal("publication fixture lacks nested slices")
	}
	basis.TenantID = "mutated-tenant"
	control.Profiles[0].Profile.ID = "mutated-profile"
	control.Profiles[0].Bindings[0].StaticContextRefs = append(
		control.Profiles[0].Bindings[0].StaticContextRefs,
		strings.Repeat("a", 64),
	)
	catalog.Entries[0].Activation.AdapterIdentity = "mutated-adapter"
	catalog.Entries[0].Provides[0] = moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}

	secondBasis, secondControl, secondCatalog, err :=
		fixture.store.LoadPublishedBasis(
			context.Background(),
			fixture.tenantID,
		)
	if err != nil {
		t.Fatalf("second LoadPublishedBasis: %v", err)
	}
	if secondBasis != wantBasis {
		t.Fatalf("second basis=%+v want %+v", secondBasis, wantBasis)
	}
	if !reflect.DeepEqual(secondControl, wantControl) {
		t.Fatalf(
			"second ControlSnapshot was aliased: got=%+v want=%+v",
			secondControl,
			wantControl,
		)
	}
	if !reflect.DeepEqual(secondCatalog, wantCatalog) {
		t.Fatalf(
			"second CatalogGeneration was aliased: got=%+v want=%+v",
			secondCatalog,
			wantCatalog,
		)
	}
}

func TestLoadPublishedBasisValidatesInputAndStoreLifecycle(t *testing.T) {
	var nilStore *Store
	if _, _, _, err := nilStore.LoadPublishedBasis(
		context.Background(),
		"tenant",
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("nil Store error=%v want ErrStoreClosed", err)
	}

	zeroStore := &Store{}
	if _, _, _, err := zeroStore.LoadPublishedBasis(
		context.Background(),
		"tenant",
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("zero Store error=%v want ErrStoreClosed", err)
	}

	store := openModuleTestStore(t)
	if _, _, _, err := store.LoadPublishedBasis(
		nil,
		"tenant",
	); !errors.Is(err, ErrInvalidPublishedBasis) {
		t.Fatalf("nil context error=%v want ErrInvalidPublishedBasis", err)
	}
	for _, tenantID := range []string{"", " tenant", "tenant\n"} {
		if _, _, _, err := store.LoadPublishedBasis(
			context.Background(),
			tenantID,
		); !errors.Is(err, ErrInvalidPublishedBasis) {
			t.Fatalf(
				"tenant %q error=%v want ErrInvalidPublishedBasis",
				tenantID,
				err,
			)
		}
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, _, _, err := store.LoadPublishedBasis(
		context.Background(),
		"tenant",
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed Store error=%v want ErrStoreClosed", err)
	}
}
