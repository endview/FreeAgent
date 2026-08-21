package currentstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestReadOnlyObserverListsCompositeRootsAndDoesNotChangeStore(t *testing.T) {
	fixture := newCommittedCompositeRuntimeFixture(t)
	path := fixture.store.Path()
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if _, err := PrepareClosedCurrentStoreForPublication(
		context.Background(),
		path,
	); err != nil {
		t.Fatalf("prepare self-contained Store: %v", err)
	}
	before := readOnlyObserverTestHash(t, path)

	observer, err := OpenReadOnlyObserver(context.Background(), path)
	if err != nil {
		t.Fatalf("open observer: %v", err)
	}
	roots, err := observer.ListCompositeRootRunIDs(context.Background())
	if err != nil {
		t.Fatalf("list roots: %v", err)
	}
	wantRoots := []string{fixture.compiled.Parent.RunManifest.RunID}
	if !reflect.DeepEqual(roots, wantRoots) {
		t.Fatalf("roots=%v want %v", roots, wantRoots)
	}
	projection, err := observer.GetCompositeFamilyUsageProjection(
		context.Background(),
		roots[0],
	)
	if err != nil {
		t.Fatalf("read projection: %v", err)
	}
	if projection.RootRunID != roots[0] ||
		len(projection.Runs) != len(fixture.compiled.Children)+1 {
		t.Fatalf("projection shape is incomplete")
	}
	basis, control, catalog, err := observer.LoadPublishedBasis(
		context.Background(),
		fixture.basis.TenantID,
	)
	if err != nil {
		t.Fatalf("load published basis: %v", err)
	}
	if !reflect.DeepEqual(basis, fixture.basis) ||
		control.SnapshotID != fixture.basis.Control.SnapshotID ||
		catalog.GenerationID != fixture.basis.Catalog.GenerationID ||
		len(catalog.Entries) == 0 {
		t.Fatalf("observed published basis is incomplete")
	}
	activation := catalog.Entries[0].Activation
	installation, err := observer.GetModuleInstallationByIdentity(
		context.Background(),
		activation.ModuleID,
		activation.Version,
	)
	if err != nil {
		t.Fatalf("get module installation by identity: %v", err)
	}
	if installation.ModuleID != activation.ModuleID ||
		installation.ExactVersion != activation.Version ||
		installation.ArtifactDigest != activation.ArtifactDigest ||
		len(installation.ManifestBytes) == 0 {
		t.Fatalf("observed installation does not close Catalog activation")
	}
	historicalControl, historicalCatalog, err :=
		observer.LoadControlCatalogRevision(
			context.Background(),
			fixture.basis.TenantID,
			fixture.basis.Control.Revision,
			fixture.basis.Catalog.Generation,
		)
	if err != nil {
		t.Fatalf("load Control/Catalog revision: %v", err)
	}
	if !reflect.DeepEqual(historicalControl, control) ||
		!reflect.DeepEqual(historicalCatalog, catalog) {
		t.Fatal("historical Control/Catalog wrapper returned a different closure")
	}
	exactActivation, err := observer.GetModuleActivationByIdentity(
		context.Background(),
		fixture.basis.TenantID,
		activation.InstanceID,
		activation.ActivationRevision,
	)
	if err != nil {
		t.Fatalf("get exact module activation: %v", err)
	}
	if exactActivation.InstanceID != activation.InstanceID ||
		exactActivation.InstallationID != installation.InstallationID ||
		exactActivation.ActivationRevision != activation.ActivationRevision {
		t.Fatalf("exact activation=%+v", exactActivation)
	}
	latestActivation, err := observer.GetLatestModuleActivationForInstance(
		context.Background(),
		fixture.basis.TenantID,
		activation.InstanceID,
	)
	if err != nil {
		t.Fatalf("get latest module activation: %v", err)
	}
	if latestActivation.InstanceID != activation.InstanceID ||
		latestActivation.InstallationID != installation.InstallationID ||
		latestActivation.ActivationRevision != activation.ActivationRevision {
		t.Fatalf("latest activation=%+v", latestActivation)
	}
	content, err := observer.GetContent(
		context.Background(),
		installation.ManifestRef,
	)
	if err != nil {
		t.Fatalf("get manifest content: %v", err)
	}
	if content.Digest != installation.ManifestRef ||
		content.Kind != ContentModuleManifest ||
		!bytes.Equal(content.CanonicalBytes, installation.ManifestBytes) {
		t.Fatalf("manifest content does not close installation")
	}
	content.CanonicalBytes[0] ^= 0xff
	contentAgain, err := observer.GetContent(
		context.Background(),
		installation.ManifestRef,
	)
	if err != nil {
		t.Fatalf("get manifest content again: %v", err)
	}
	if !bytes.Equal(contentAgain.CanonicalBytes, installation.ManifestBytes) {
		t.Fatal("observer ContentRecord bytes were not detached")
	}
	if err := observer.Close(); err != nil {
		t.Fatalf("close observer: %v", err)
	}
	if _, err := observer.ListCompositeRootRunIDs(context.Background()); err != ErrStoreClosed {
		t.Fatalf("closed observer error=%v", err)
	}
	after := readOnlyObserverTestHash(t, path)
	if before != after {
		t.Fatal("read-only observer changed Current Store bytes")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); err == nil || !os.IsNotExist(err) {
			t.Fatalf("read-only observer left SQLite sidecar %q: %v", suffix, err)
		}
	}
}

func TestReadOnlyObserverStartupRecoveryRequiredDistinguishesPendingAndUnknown(
	t *testing.T,
) {
	t.Run("model pending requires recovery", func(t *testing.T) {
		fixture, _, beginInput := newModelDispatchFixture(t)
		if _, err := fixture.store.BeginModelDispatch(
			context.Background(),
			beginInput,
		); err != nil {
			t.Fatalf("begin Model PENDING: %v", err)
		}
		observer := openPreparedReadOnlyObserver(t, fixture.store)
		defer observer.Close()

		required, err := observer.StartupRecoveryRequired(
			context.Background(),
		)
		if err != nil {
			t.Fatalf("check startup recovery: %v", err)
		}
		if !required {
			t.Fatal("Model PENDING did not require startup recovery")
		}
	})

	t.Run("action pending requires recovery", func(t *testing.T) {
		harness := newActionStoreHarness(t)
		harness.beginAction(t, "observer-action-pending")
		observer := openPreparedReadOnlyObserver(t, harness.store)
		defer observer.Close()

		required, err := observer.StartupRecoveryRequired(
			context.Background(),
		)
		if err != nil {
			t.Fatalf("check startup recovery: %v", err)
		}
		if !required {
			t.Fatal("Action PENDING did not require startup recovery")
		}
	})

	t.Run("channel pending requires recovery", func(t *testing.T) {
		harness := newChannelDispatchHarness(
			t,
			"observer-channel-pending",
			"observer-channel-event",
		)
		harness.mustBegin(t)
		observer := openPreparedReadOnlyObserver(t, harness.store)
		defer observer.Close()

		required, err := observer.StartupRecoveryRequired(
			context.Background(),
		)
		if err != nil {
			t.Fatalf("check startup recovery: %v", err)
		}
		if !required {
			t.Fatal("Channel PENDING did not require startup recovery")
		}
	})

	t.Run("model unknown is reconciliation only", func(t *testing.T) {
		fixture, _, beginInput := newModelDispatchFixture(t)
		pending, err := fixture.store.BeginModelDispatch(
			context.Background(),
			beginInput,
		)
		if err != nil {
			t.Fatalf("begin Model PENDING: %v", err)
		}
		if _, err := fixture.store.CommitModelDispatchOutcome(
			context.Background(),
			CommitModelDispatchOutcomeInput{
				Lease:                   pending.Lease,
				AttemptID:               pending.Attempt.AttemptID,
				InvocationID:            pending.Attempt.AttemptID,
				Provider:                pending.Attempt.Binding.Provider,
				ExpectedAttemptRevision: pending.Attempt.Revision,
				State:                   corecontract.ModelAttemptUnknown,
				UnknownReason:           "observer-test-unknown",
			},
		); err != nil {
			t.Fatalf("commit Model UNKNOWN: %v", err)
		}
		observer := openPreparedReadOnlyObserver(t, fixture.store)
		defer observer.Close()

		required, err := observer.StartupRecoveryRequired(
			context.Background(),
		)
		if err != nil {
			t.Fatalf("check startup recovery: %v", err)
		}
		if required {
			t.Fatal("Model UNKNOWN incorrectly required startup recovery")
		}
	})
}

func TestOpenReadOnlyObserverRejectsMissingStore(t *testing.T) {
	observer, err := OpenReadOnlyObserver(
		context.Background(),
		t.TempDir()+"/missing.sqlite",
	)
	if err == nil || observer != nil {
		t.Fatalf("missing Store observer=%v error=%v", observer, err)
	}
}

func TestOpenReadOnlyObserverRejectsSQLiteSidecars(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			fixture := newCommittedCompositeRuntimeFixture(t)
			path := fixture.store.Path()
			if err := fixture.store.Close(); err != nil {
				t.Fatalf("close writer: %v", err)
			}
			if _, err := PrepareClosedCurrentStoreForPublication(
				context.Background(),
				path,
			); err != nil {
				t.Fatalf("prepare self-contained Store: %v", err)
			}
			if err := os.WriteFile(path+suffix, []byte("sidecar"), 0o600); err != nil {
				t.Fatalf("write sidecar: %v", err)
			}
			observer, err := OpenReadOnlyObserver(context.Background(), path)
			if err == nil || observer != nil {
				t.Fatalf("sidecar observer=%v error=%v", observer, err)
			}
		})
	}
}

func readOnlyObserverTestHash(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(payload)
}

func openPreparedReadOnlyObserver(t *testing.T, store *Store) *ReadOnlyObserver {
	t.Helper()
	path := store.Path()
	if err := store.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if _, err := PrepareClosedCurrentStoreForPublication(
		context.Background(),
		path,
	); err != nil {
		t.Fatalf("prepare self-contained Store: %v", err)
	}
	observer, err := OpenReadOnlyObserver(context.Background(), path)
	if err != nil {
		t.Fatalf("open observer: %v", err)
	}
	return observer
}
