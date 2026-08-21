package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
)

func TestModuleApplyPreStageCheckFailsBeforeHiddenStageAndPublication(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		"role-prestage-denied",
	)
	plan, canonical, digest, err := restoreModuleApplyPlanV1(
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	checkCalls := 0
	_, err = applyModulePlanV1(context.Background(), moduleApplyCommandInputV1{
		DatabasePath:      databasePath,
		ArtifactRoot:      artifactRoot,
		ArtifactDirectory: fixture.ArtifactDirectory,
		Plan:              plan,
		PlanCanonical:     canonical,
		PlanDigest:        digest,
		PreStageCheck: func(
			context.Context,
			*currentstore.Store,
			controlcontract.PublishedBasis,
		) error {
			checkCalls++
			return errors.New("approval became stale")
		},
	})
	if moduleApplyFailureCodeOfV1(err) != moduleApplyFailureStore {
		t.Fatalf("pre-stage failure=%v code=%q", err, moduleApplyFailureCodeOfV1(err))
	}
	if checkCalls != 1 {
		t.Fatalf("pre-stage calls=%d want=1", checkCalls)
	}
	assertNoModuleApplyStageResidueV1(t, artifactRoot)
	assertModuleApplyContextStateV1(t, databasePath, nil)
	store, openErr := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer store.Close()
	basis, _, catalog, loadErr := store.LoadPublishedBasis(context.Background(), defaultTenantID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	_, found := catalog.FindInstance(fixture.InstanceID)
	if basis.PointerRevision != 1 || found {
		t.Fatalf("pre-stage failure changed publication: basis=%+v catalog=%+v", basis, catalog)
	}
}

func TestModuleApplyExactRetrySkipsPreStageCheck(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		"role-prestage-retry",
	)
	plan, canonical, digest, err := restoreModuleApplyPlanV1(
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	input := moduleApplyCommandInputV1{
		DatabasePath:      databasePath,
		ArtifactRoot:      artifactRoot,
		ArtifactDirectory: fixture.ArtifactDirectory,
		Plan:              plan,
		PlanCanonical:     canonical,
		PlanDigest:        digest,
	}
	first, err := applyModulePlanV1(context.Background(), input)
	if err != nil || first.Status != moduleApplyStatusApplied {
		t.Fatalf("first Apply=%+v err=%v", first, err)
	}
	checkCalls := 0
	input.PreStageCheck = func(
		context.Context,
		*currentstore.Store,
		controlcontract.PublishedBasis,
	) error {
		checkCalls++
		return errors.New("must not run on exact retry")
	}
	retried, err := applyModulePlanV1(context.Background(), input)
	if err != nil || retried.Status != moduleApplyStatusAlreadyApplied {
		t.Fatalf("exact retry=%+v err=%v", retried, err)
	}
	if checkCalls != 0 {
		t.Fatalf("exact retry pre-stage calls=%d want=0", checkCalls)
	}
}
