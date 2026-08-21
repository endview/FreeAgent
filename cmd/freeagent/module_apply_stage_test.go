package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type moduleApplyLeaseWaitObservedContextV1 struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *moduleApplyLeaseWaitObservedContextV1) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

func TestModuleApplyStageIsHiddenDirectArtifactRootChild(t *testing.T) {
	artifactRoot := filepath.Join(t.TempDir(), "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatalf("create artifact root: %v", err)
	}
	filesystem := productionModuleApplyStageFilesystemV1()
	stageRoot, err := createModuleApplyStageRootV1(artifactRoot, filesystem)
	if err != nil {
		t.Fatalf("create module apply stage: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stageRoot) })

	if !samePath(filepath.Dir(stageRoot), artifactRoot) {
		t.Fatalf("stage root %q is not a direct child of artifact root %q", stageRoot, artifactRoot)
	}
	if moduleapi.ValidSHA256(filepath.Base(stageRoot)) {
		t.Fatalf("hidden stage %q is a reachable artifact identity", stageRoot)
	}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	stagedArtifact := filepath.Join(stageRoot, digest)
	if err := validateModuleApplyStagedArtifactPathV1(
		stagedArtifact,
		artifactRoot,
		digest,
	); err != nil {
		t.Fatalf("validate in-root staged artifact: %v", err)
	}
	outOfRootStage := filepath.Join(
		filepath.Dir(artifactRoot),
		moduleApplyStagePrefixV1+"outside",
		digest,
	)
	if err := validateModuleApplyStagedArtifactPathV1(
		outOfRootStage,
		artifactRoot,
		digest,
	); err == nil {
		t.Fatal("accepted a staged artifact outside artifact root")
	}
	if err := cleanupModuleApplyStageRootV1(
		stageRoot,
		artifactRoot,
		filesystem,
	); err != nil {
		t.Fatalf("cleanup module apply stage: %v", err)
	}
	if _, err := os.Lstat(stageRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stage root remains after cleanup: %v", err)
	}
}

func TestModuleApplyStageCleanupFailureIsFailClosedBeforeStoreWrites(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-started.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	beforeCounts := moduleApplyMutationRowCountsV1(t, databasePath)

	planBytes := newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0)
	plan, canonical, planDigest, err := restoreModuleApplyPlanV1(planBytes)
	if err != nil {
		t.Fatalf("restore enabled plan: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open Current Store: %v", err)
	}
	basis, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if err != nil {
		_ = store.Close()
		t.Fatalf("load published basis: %v", err)
	}

	production := productionModuleApplyStageFilesystemV1()
	var stageRoot string
	removeCalls := 0
	syncCalls := 0
	filesystem := moduleApplyStageFilesystemV1{
		mkdirTemp: func(directory string, pattern string) (string, error) {
			if !samePath(directory, artifactRoot) {
				t.Errorf("MkdirTemp directory=%q want artifact root %q", directory, artifactRoot)
			}
			if pattern != moduleApplyStagePrefixV1+"*" {
				t.Errorf("MkdirTemp pattern=%q", pattern)
			}
			created, createErr := production.mkdirTemp(directory, pattern)
			stageRoot = created
			return created, createErr
		},
		removeAll: func(path string) error {
			removeCalls++
			if stageRoot != "" && !samePath(path, stageRoot) {
				t.Errorf("remove path=%q want stage root %q", path, stageRoot)
			}
			return errors.New("injected stage cleanup failure")
		},
		syncDirectory: func(path string) error {
			syncCalls++
			if !samePath(path, artifactRoot) {
				t.Errorf("sync path=%q want artifact root %q", path, artifactRoot)
			}
			return production.syncDirectory(path)
		},
	}
	_, applyErr := prepareAndStageEnabledModuleV1(
		context.Background(),
		store,
		artifactRoot,
		filesystem,
		moduleApplyCommandInputV1{
			ArtifactRoot:          artifactRoot,
			ArtifactDirectory:     fixture.ArtifactDirectory,
			LocalMCPArtifactGrant: fixture.ArtifactDigest,
			Plan:                  plan,
			PlanCanonical:         canonical,
			PlanDigest:            planDigest,
		},
		basis,
		control,
		catalog,
	)
	if code := moduleApplyFailureCodeOfV1(applyErr); code != moduleApplyFailureArtifact {
		t.Fatalf("cleanup failure code=%q error=%v", code, applyErr)
	}
	if removeCalls < 2 || syncCalls < 2 {
		t.Fatalf("cleanup attempts remove=%d sync=%d; want explicit and deferred best effort", removeCalls, syncCalls)
	}
	if stageRoot == "" || !samePath(filepath.Dir(stageRoot), artifactRoot) {
		t.Fatalf("stage root=%q is not inside artifact root", stageRoot)
	}
	if _, err := os.Lstat(stageRoot); err != nil {
		t.Fatalf("injected failed cleanup did not leave observable stage for test: %v", err)
	}
	if _, err := store.GetModuleInstallationByIdentity(
		context.Background(),
		fixture.ModuleID,
		moduleApplyTestVersion,
	); !errors.Is(err, currentstore.ErrModuleInstallationNotFound) {
		t.Fatalf("cleanup failure crossed immutable Store boundary: %v", err)
	}
	afterBasis, _, _, err := store.LoadPublishedBasis(context.Background(), defaultTenantID)
	if err != nil {
		t.Fatalf("reload published basis: %v", err)
	}
	if afterBasis != basis {
		t.Fatalf("cleanup failure changed published basis: before=%+v after=%+v", basis, afterBasis)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close Current Store: %v", err)
	}
	if afterCounts := moduleApplyMutationRowCountsV1(t, databasePath); !reflect.DeepEqual(afterCounts, beforeCounts) {
		t.Fatalf("cleanup failure wrote immutable Store rows: before=%v after=%v", beforeCounts, afterCounts)
	}
	if _, err := os.Lstat(filepath.Join(artifactRoot, fixture.ArtifactDigest)); err != nil {
		t.Fatalf("verified unreferenced final artifact was not retained for exact retry: %v", err)
	}
	if err := os.RemoveAll(stageRoot); err != nil {
		t.Fatalf("remove injected test stage: %v", err)
	}
	if err := syncInitDirectory(artifactRoot); err != nil {
		t.Fatalf("sync artifact root after test cleanup: %v", err)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)
}

func TestModuleApplyWriteLeaseSerializesDifferentStoresSharingArtifactRoot(t *testing.T) {
	root := t.TempDir()
	sharedArtifactRoot := filepath.Join(root, "shared-artifacts")
	databaseA := filepath.Join(root, "a.sqlite")
	databaseB := filepath.Join(root, "b.sqlite")
	initializePureChatForModuleApplyV1(t, databaseA, sharedArtifactRoot)
	initializePureChatForModuleApplyV1(
		t,
		databaseB,
		filepath.Join(root, "b-bootstrap-artifacts"),
	)
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		filepath.Join(root, "mcp-started.log"),
	)
	planBytes := newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0)
	plan, canonical, planDigest, err := restoreModuleApplyPlanV1(planBytes)
	if err != nil {
		t.Fatal(err)
	}
	storeA, err := currentstore.OpenExistingCurrentStore(context.Background(), databaseA)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := currentstore.OpenExistingCurrentStore(context.Background(), databaseB)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	basisA, controlA, catalogA, err := storeA.LoadPublishedBasis(
		context.Background(), defaultTenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	basisB, controlB, catalogB, err := storeB.LoadPublishedBasis(
		context.Background(), defaultTenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	input := moduleApplyCommandInputV1{
		ArtifactRoot:          sharedArtifactRoot,
		ArtifactDirectory:     fixture.ArtifactDirectory,
		LocalMCPArtifactGrant: fixture.ArtifactDigest,
		Plan:                  plan,
		PlanCanonical:         canonical,
		PlanDigest:            planDigest,
	}

	production := productionModuleApplyStageFilesystemV1()
	leaseHeld := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseFirstOnce sync.Once
	releaseFirstLease := func() {
		releaseFirstOnce.Do(func() { close(releaseFirst) })
	}
	defer releaseFirstLease()
	firstFilesystem := production
	firstFilesystem.mkdirTemp = func(directory, pattern string) (string, error) {
		close(leaseHeld)
		<-releaseFirst
		return production.mkdirTemp(directory, pattern)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, firstErr := prepareAndStageEnabledModuleV1(
			context.Background(),
			storeA,
			sharedArtifactRoot,
			firstFilesystem,
			input,
			basisA,
			controlA,
			catalogA,
		)
		firstDone <- firstErr
	}()
	select {
	case <-leaseHeld:
	case <-time.After(10 * time.Second):
		releaseFirstLease()
		select {
		case <-firstDone:
		case <-time.After(30 * time.Second):
		}
		t.Fatal("first Apply did not enter the leased writer interval")
	}

	secondStageCalled := false
	secondFilesystem := production
	secondFilesystem.mkdirTemp = func(directory, pattern string) (string, error) {
		secondStageCalled = true
		return production.mkdirTemp(directory, pattern)
	}
	leaseWaitObserved := make(chan struct{})
	secondFilesystem.wrapArtifactRootWriteLeaseContext = func(ctx context.Context) context.Context {
		return &moduleApplyLeaseWaitObservedContextV1{
			Context:  ctx,
			observed: leaseWaitObserved,
		}
	}
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	secondDone := make(chan error, 1)
	go func() {
		_, secondErr := prepareAndStageEnabledModuleV1(
			secondCtx,
			storeB,
			sharedArtifactRoot,
			secondFilesystem,
			input,
			basisB,
			controlB,
			catalogB,
		)
		secondDone <- secondErr
	}()
	var secondErr error
	select {
	case <-leaseWaitObserved:
	case secondErr = <-secondDone:
		releaseFirstLease()
		select {
		case <-firstDone:
		case <-time.After(30 * time.Second):
		}
		t.Fatalf("second Apply returned before entering the busy write-lease wait: %v", secondErr)
	case <-time.After(10 * time.Second):
		cancelSecond()
		releaseFirstLease()
		select {
		case <-secondDone:
		case <-time.After(10 * time.Second):
		}
		select {
		case <-firstDone:
		case <-time.After(30 * time.Second):
		}
		t.Fatal("second Apply did not enter the busy write-lease wait")
	}
	cancelSecond()
	select {
	case secondErr = <-secondDone:
	case <-time.After(10 * time.Second):
		releaseFirstLease()
		select {
		case <-secondDone:
		case <-time.After(30 * time.Second):
		}
		select {
		case <-firstDone:
		case <-time.After(30 * time.Second):
		}
		t.Fatal("second Apply did not stop after write-lease cancellation")
	}
	secondSerialized := errors.Is(secondErr, context.Canceled) &&
		moduleApplyFailureCodeOfV1(secondErr) == moduleApplyFailureCancelled &&
		!secondStageCalled
	releaseFirstLease()
	var firstErr error
	select {
	case firstErr = <-firstDone:
	case <-time.After(30 * time.Second):
		t.Fatal("first Apply did not complete after lease release")
	}
	if !secondSerialized {
		t.Fatalf(
			"second Apply error=%v code=%q stage=%v",
			secondErr,
			moduleApplyFailureCodeOfV1(secondErr),
			secondStageCalled,
		)
	}
	if firstErr != nil {
		t.Fatalf("first Apply after releasing writer interval: %v", firstErr)
	}
	assertNoModuleApplyStageResidueV1(t, sharedArtifactRoot)
}

func TestModuleApplyPreReservationRejectsUnknownRootBeforeStage(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		filepath.Join(root, "mcp-started.log"),
	)
	plan, canonical, planDigest, err := restoreModuleApplyPlanV1(
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	basis, control, catalog, err := store.LoadPublishedBasis(
		context.Background(), defaultTenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(artifactRoot, "unknown-root-entry"),
		[]byte("not artifact data"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	stageCalls := 0
	filesystem := productionModuleApplyStageFilesystemV1()
	productionMkdirTemp := filesystem.mkdirTemp
	filesystem.mkdirTemp = func(directory, pattern string) (string, error) {
		stageCalls++
		return productionMkdirTemp(directory, pattern)
	}
	_, applyErr := prepareAndStageEnabledModuleV1(
		context.Background(),
		store,
		artifactRoot,
		filesystem,
		moduleApplyCommandInputV1{
			ArtifactRoot:          artifactRoot,
			ArtifactDirectory:     fixture.ArtifactDirectory,
			LocalMCPArtifactGrant: fixture.ArtifactDigest,
			Plan:                  plan,
			PlanCanonical:         canonical,
			PlanDigest:            planDigest,
		},
		basis,
		control,
		catalog,
	)
	if moduleApplyFailureCodeOfV1(applyErr) != moduleApplyFailureArtifact || stageCalls != 0 {
		t.Fatalf("pre-reservation error=%v code=%q stage calls=%d", applyErr, moduleApplyFailureCodeOfV1(applyErr), stageCalls)
	}
	if _, err := store.GetModuleInstallationByIdentity(
		context.Background(), fixture.ModuleID, moduleApplyTestVersion,
	); !errors.Is(err, currentstore.ErrModuleInstallationNotFound) {
		t.Fatalf("pre-reservation failure crossed Store boundary: %v", err)
	}
}

func TestModuleApplyPostClosureFailsBeforeStoreWrites(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		filepath.Join(root, "mcp-started.log"),
	)
	plan, canonical, planDigest, err := restoreModuleApplyPlanV1(
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	basis, control, catalog, err := store.LoadPublishedBasis(
		context.Background(), defaultTenantID,
	)
	if err != nil {
		t.Fatal(err)
	}

	unknown := filepath.Join(artifactRoot, "post-publish-unknown")
	filesystem := productionModuleApplyStageFilesystemV1()
	productionSync := filesystem.syncDirectory
	injected := false
	filesystem.syncDirectory = func(path string) error {
		if err := productionSync(path); err != nil {
			return err
		}
		if !injected {
			injected = true
			if err := os.WriteFile(unknown, []byte("drift"), 0o600); err != nil {
				return err
			}
			return productionSync(path)
		}
		return nil
	}
	_, applyErr := prepareAndStageEnabledModuleV1(
		context.Background(),
		store,
		artifactRoot,
		filesystem,
		moduleApplyCommandInputV1{
			ArtifactRoot:          artifactRoot,
			ArtifactDirectory:     fixture.ArtifactDirectory,
			LocalMCPArtifactGrant: fixture.ArtifactDigest,
			Plan:                  plan,
			PlanCanonical:         canonical,
			PlanDigest:            planDigest,
		},
		basis,
		control,
		catalog,
	)
	if !injected || moduleApplyFailureCodeOfV1(applyErr) != moduleApplyFailureArtifact {
		t.Fatalf("post-closure error=%v code=%q injected=%v", applyErr, moduleApplyFailureCodeOfV1(applyErr), injected)
	}
	if _, err := store.GetModuleInstallationByIdentity(
		context.Background(), fixture.ModuleID, moduleApplyTestVersion,
	); !errors.Is(err, currentstore.ErrModuleInstallationNotFound) {
		t.Fatalf("post-closure failure crossed Store boundary: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(artifactRoot, fixture.ArtifactDigest)); err != nil {
		t.Fatalf("post-closure failure lost verified unreferenced publication: %v", err)
	}
	assertNoModuleApplyStageResidueV1(t, artifactRoot)
}

func TestModuleApplyStageCleanupPreservesCommitBoundaryPriority(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	for _, test := range []struct {
		name string
		code moduleApplyFailureCodeV1
	}{
		{name: "unknown", code: moduleApplyFailureOutcomeUnknown},
		{name: "pointer", code: moduleApplyFailurePointer},
		{name: "cancelled", code: moduleApplyFailureCancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			primary := &moduleApplyFailureV1{
				code:  test.code,
				cause: context.Canceled,
			}
			joined := joinModuleApplyStageCleanupFailureV1(primary, cleanupErr)
			if got := moduleApplyFailureCodeOfV1(joined); got != test.code {
				t.Fatalf("joined cleanup failure code=%q want=%q", got, test.code)
			}
			if !errors.Is(joined, context.Canceled) || !errors.Is(joined, cleanupErr) {
				t.Fatalf("joined cleanup failure lost causes: %v", joined)
			}
		})
	}
}

func TestModuleApplyPreexistingArtifactSyncEnumeratesEveryOrdinaryFile(t *testing.T) {
	root := t.TempDir()
	fixture := newModuleApplyMCPFixtureV1(
		t,
		root,
		filepath.Join(root, "mcp-started.log"),
	)
	files, err := moduleapi.ScanArtifactDirectoryContext(
		context.Background(),
		fixture.ArtifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatalf("scan fixture artifact: %v", err)
	}
	want := []string{moduleapi.ArtifactManifestPath}
	for _, file := range files {
		want = append(want, filepath.ToSlash(file.Path))
	}
	sort.Strings(want)

	var got []string
	err = syncModuleApplyArtifactFilesWithV1(
		context.Background(),
		fixture.ArtifactDirectory,
		func(_ context.Context, path string) error {
			relative, relativeErr := filepath.Rel(fixture.ArtifactDirectory, path)
			if relativeErr != nil {
				return relativeErr
			}
			info, statErr := os.Lstat(path)
			if statErr != nil {
				return statErr
			}
			if !info.Mode().IsRegular() {
				return errors.New("sync target is not an ordinary file")
			}
			got = append(got, filepath.ToSlash(relative))
			return nil
		},
	)
	if err != nil {
		t.Fatalf("enumerate artifact durability sync: %v", err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("synced ordinary files=%v want=%v", got, want)
	}

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err = syncModuleApplyArtifactFilesWithV1(
		ctx,
		fixture.ArtifactDirectory,
		func(context.Context, string) error {
			calls++
			cancel()
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancelled file sync error=%v calls=%d; want context.Canceled after one file", err, calls)
	}
}

func TestModuleApplyReconcileClassifiesConflictByObservedPointer(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	plan, _, planDigest, err := restoreModuleApplyPlanV1(
		newDisabledModuleApplyPlanFixtureV1(t, 1),
	)
	if err != nil {
		t.Fatalf("restore disabled plan: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open Current Store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close Current Store: %v", err)
		}
	}()
	basis, _, _, err := store.LoadPublishedBasis(context.Background(), defaultTenantID)
	if err != nil {
		t.Fatalf("load published basis: %v", err)
	}

	_, err = reconcileModulePublicationV1(
		context.Background(),
		store,
		artifactRoot,
		plan,
		planDigest,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
		},
		currentstore.ErrPublicationConflict,
	)
	if code := moduleApplyFailureCodeOfV1(err); code != moduleApplyFailurePublication {
		t.Fatalf("unchanged pointer conflict code=%q error=%v", code, err)
	}

	_, err = reconcileModulePublicationV1(
		context.Background(),
		store,
		artifactRoot,
		plan,
		planDigest,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision - 1,
			NewPointerRevision:      basis.PointerRevision,
		},
		errors.Join(currentstore.ErrPublicationConflict, context.Canceled),
	)
	if code := moduleApplyFailureCodeOfV1(err); code != moduleApplyFailurePointer {
		t.Fatalf("moved pointer conflict code=%q error=%v", code, err)
	}
}
