package main

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
)

func TestModuleDryRunDisableUsesSameEvaluatorOnLiveStoreV1(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	enablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	if result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePath,
		fixture.ArtifactDirectory,
		"",
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare live DISABLED dry-run: result=%+v err=%v", result, err)
	}
	disableCanonical := newDisabledDeclarativeModuleApplyPlanV1(
		t,
		fixture.InstanceID,
		2,
	)
	plan, canonical, digest, err := restoreModuleApplyPlanV1(disableCanonical)
	if err != nil {
		t.Fatalf("restore DISABLED plan: %v", err)
	}

	// First prove the stopped-process CLI wrapper and retain its exact result.
	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-role.json"),
		canonical,
	)
	offline, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		disablePath,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("offline DISABLED dry-run: %v", err)
	}

	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatalf("open live Current Store: %v", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close live Current Store: %v", closeErr)
		}
	}()
	live, err := dryRunDisabledModulePlanOnViewV1(
		context.Background(),
		store,
		artifactRoot,
		moduleApplyCommandInputV1{
			ArtifactRoot:  artifactRoot,
			Plan:          plan,
			PlanCanonical: canonical,
			PlanDigest:    digest,
		},
		false,
	)
	if err != nil {
		t.Fatalf("live DISABLED dry-run: %v", err)
	}
	if !reflect.DeepEqual(live, offline) {
		t.Fatalf("live/offline evaluator drift:\nlive=%+v\noffline=%+v", live, offline)
	}
}

func TestModuleDisableApplyAndDryRunUseByteExactSharedPublicationV1(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	enablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	if result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePath,
		fixture.ArtifactDirectory,
		"",
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare Disable: result=%+v err=%v", result, err)
	}
	disableCanonical := newDisabledDeclarativeModuleApplyPlanV1(
		t,
		fixture.InstanceID,
		2,
	)
	plan, canonical, digest, err := restoreModuleApplyPlanV1(disableCanonical)
	if err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := evaluateDisabledModulePlanOnViewV1(
		context.Background(),
		store,
		artifactRoot,
		plan,
		digest,
	)
	if err != nil {
		_ = store.Close()
		t.Fatalf("shared Disable evaluation: %v", err)
	}
	publication, err := moduleDisablePublicationV1(shared.Publication)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-role.json"),
		canonical,
	)
	dry, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		disablePath,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("offline Disable Dry-run: %v", err)
	}
	if dry.ObservedBasis != shared.ObservedBasis ||
		dry.CandidateBasis.Control != publication.ControlRef ||
		dry.CandidateBasis.Catalog != publication.CatalogRef ||
		dry.CandidateBasis.PointerRevision != publication.NewPointerRevision {
		t.Fatalf("Dry-run projection drift: shared=%+v dry=%+v", shared, dry)
	}
	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePath,
		"",
		"",
	)
	if err != nil || applied.Status != moduleApplyStatusApplied {
		t.Fatalf("Apply shared Disable: result=%+v err=%v", applied, err)
	}
	basis, control, catalog := loadModuleApplyPublishedStateV1(t, databasePath)
	if basis.PointerRevision != publication.NewPointerRevision ||
		basis.Control != publication.ControlRef ||
		basis.Catalog != publication.CatalogRef {
		t.Fatalf("Apply publication refs drift: got=%+v want=%+v", basis, publication)
	}
	_, _, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	_, _, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(controlCanonical, publication.ControlCanonical) ||
		!bytes.Equal(catalogCanonical, publication.CatalogCanonical) {
		t.Fatal("Apply did not publish the byte-exact shared Disable projection")
	}
}
