package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
)

func TestModuleDryRunReenableInactiveActivationDoesNotReserveRevision(
	t *testing.T,
) {
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
		filepath.Join(root, "enable-role-v1.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	if enabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePath,
		fixture.ArtifactDirectory,
		"",
	); err != nil || enabled.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare active Activation: result=%+v err=%v", enabled, err)
	}
	firstActivation := readLatestNamedModuleActivationV1(
		t,
		databasePath,
		fixture.InstanceID,
	)
	if firstActivation.ActivationRevision != 1 {
		t.Fatalf("first Activation=%+v", firstActivation)
	}

	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-role.json"),
		newDisabledDeclarativeModuleApplyPlanV1(t, fixture.InstanceID, 2),
	)
	if disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePath,
		"",
		"",
	); err != nil || disabled.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare inactive Activation: result=%+v err=%v", disabled, err)
	}
	prepareClosedModuleDryRunStoreV1(t, databasePath)
	observed, catalog, inactiveActivation := readModuleDryRunActivationFactsV1(
		t,
		databasePath,
		fixture.InstanceID,
	)
	if inactiveActivation != firstActivation {
		t.Fatalf(
			"disable changed immutable Activation: got=%+v want=%+v",
			inactiveActivation,
			firstActivation,
		)
	}
	if _, active := catalog.FindInstance(fixture.InstanceID); active {
		t.Fatalf("disabled instance %q remains active in Catalog", fixture.InstanceID)
	}
	databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)

	reenablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role-v2.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 3, 0),
	)
	dryRun, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		reenablePath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil {
		t.Fatalf("dry-run re-enable inactive Activation: %v", err)
	}
	if dryRun.Status != moduleApplyStatusWouldApply ||
		dryRun.ObservedBasis != observed ||
		dryRun.Changes.Installation != moduleDryRunInstallationReuseV1 ||
		dryRun.Changes.Activation != moduleDryRunActivationCreateV1 ||
		dryRun.Changes.Binding.Change != moduleDryRunBindingInsertV1 ||
		dryRun.Changes.Catalog != moduleDryRunCatalogAddInstanceV1 {
		t.Fatalf("inactive Activation dry-run=%+v", dryRun)
	}
	projected := publishedModuleDryRunCandidateBasisV1(t, dryRun.CandidateBasis)
	if projected.PointerRevision != 4 {
		t.Fatalf("projected publication=%+v", projected)
	}
	databaseAfterDryRun := snapshotModuleDryRunDatabaseV1(t, databasePath)
	if !reflect.DeepEqual(databaseAfterDryRun, databaseBefore) {
		t.Fatalf(
			"dry-run reserved durable state: before=%v after=%v",
			databaseBefore,
			databaseAfterDryRun,
		)
	}
	if latest := readLatestNamedModuleActivationV1(
		t,
		databasePath,
		fixture.InstanceID,
	); latest != firstActivation {
		t.Fatalf("dry-run reserved an Activation revision: got=%+v want=%+v", latest, firstActivation)
	}

	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		reenablePath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || applied.Status != moduleApplyStatusApplied {
		t.Fatalf("apply projected re-enable: result=%+v err=%v", applied, err)
	}
	appliedBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	if appliedBasis != projected {
		t.Fatalf("projected basis=%+v applied basis=%+v", projected, appliedBasis)
	}
	secondActivation := readLatestNamedModuleActivationV1(
		t,
		databasePath,
		fixture.InstanceID,
	)
	if secondActivation.ActivationRevision != 2 ||
		secondActivation.ActivationID == firstActivation.ActivationID ||
		secondActivation.InstallationID != firstActivation.InstallationID {
		t.Fatalf(
			"subsequent Apply Activation=%+v first=%+v",
			secondActivation,
			firstActivation,
		)
	}
}

func TestModuleDryRunMCPProjectedBasisExactlyMatchesApply(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-mcp.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)

	dryRun, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || dryRun.Status != moduleApplyStatusWouldApply {
		t.Fatalf("MCP dry-run: result=%+v err=%v", dryRun, err)
	}
	projected := publishedModuleDryRunCandidateBasisV1(t, dryRun.CandidateBasis)
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || applied.Status != moduleApplyStatusApplied {
		t.Fatalf("MCP Apply after dry-run: result=%+v err=%v", applied, err)
	}
	appliedBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	if appliedBasis != projected {
		t.Fatalf("MCP projected basis=%+v applied basis=%+v", projected, appliedBasis)
	}
	if dryRun.PlanDigest != applied.PlanDigest ||
		dryRun.CandidateBasis.Control.SnapshotID != applied.ControlSnapshotID ||
		dryRun.CandidateBasis.Catalog.GenerationID != applied.CatalogGenerationID {
		t.Fatalf("MCP dry-run=%+v Apply=%+v", dryRun, applied)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)
}

func TestModuleDryRunReusesCurrentActivationAcrossProfiles(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"s3c-deepseek-v4-flash-reviewer-off.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize multi-Profile Store: %v", err)
	}
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)

	const firstProfile = "s3c.backend"
	const secondProfile = "s3c.frontend"
	firstPlanPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role-backend.json"),
		newNamedEnabledDeclarativeModuleApplyPlanV1(
			t,
			fixture,
			firstProfile,
			1,
			1,
		),
	)
	if applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		firstPlanPath,
		fixture.ArtifactDirectory,
		"",
	); err != nil || applied.Status != moduleApplyStatusApplied {
		t.Fatalf("activate module in first Profile: result=%+v err=%v", applied, err)
	}
	prepareClosedModuleDryRunStoreV1(t, databasePath)
	_, _, activationBefore := readModuleDryRunActivationFactsV1(
		t,
		databasePath,
		fixture.InstanceID,
	)
	databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)

	secondPlanPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role-frontend.json"),
		newNamedEnabledDeclarativeModuleApplyPlanV1(
			t,
			fixture,
			secondProfile,
			2,
			1,
		),
	)
	dryRun, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		secondPlanPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil {
		t.Fatalf("dry-run same Activation in second Profile: %v", err)
	}
	if dryRun.Status != moduleApplyStatusWouldApply ||
		dryRun.BindingTarget.ProfileID != secondProfile ||
		dryRun.Changes.Installation != moduleDryRunInstallationReuseV1 ||
		dryRun.Changes.Activation != moduleDryRunActivationReuseCurrentV1 ||
		dryRun.Changes.Binding.Change != moduleDryRunBindingInsertV1 ||
		dryRun.Changes.Catalog != moduleDryRunCatalogRetainInstanceV1 {
		t.Fatalf("cross-Profile reuse dry-run=%+v", dryRun)
	}
	if databaseAfter := snapshotModuleDryRunDatabaseV1(t, databasePath); !reflect.DeepEqual(databaseAfter, databaseBefore) {
		t.Fatalf("cross-Profile dry-run wrote Store: before=%v after=%v", databaseBefore, databaseAfter)
	}
	if activationAfter := readLatestNamedModuleActivationV1(
		t,
		databasePath,
		fixture.InstanceID,
	); activationAfter != activationBefore {
		t.Fatalf("cross-Profile dry-run changed Activation: before=%+v after=%+v", activationBefore, activationAfter)
	}
	projected := publishedModuleDryRunCandidateBasisV1(t, dryRun.CandidateBasis)

	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		secondPlanPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || applied.Status != moduleApplyStatusApplied {
		t.Fatalf("apply same Activation in second Profile: result=%+v err=%v", applied, err)
	}
	appliedBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	if appliedBasis != projected {
		t.Fatalf("cross-Profile projected=%+v applied=%+v", projected, appliedBasis)
	}
	if activationAfter := readLatestNamedModuleActivationV1(
		t,
		databasePath,
		fixture.InstanceID,
	); activationAfter != activationBefore {
		t.Fatalf("REUSE_CURRENT created a new Activation: before=%+v after=%+v", activationBefore, activationAfter)
	}
}

func readLatestNamedModuleActivationV1(
	t *testing.T,
	databasePath string,
	instanceID string,
) currentstore.ModuleActivation {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenReadOnlyObserver(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	activation, readErr := store.GetLatestModuleActivationForInstance(
		ctx,
		defaultTenantID,
		instanceID,
	)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read latest Activation for %q: %v", instanceID, errors.Join(readErr, closeErr))
	}
	return activation
}

func prepareClosedModuleDryRunStoreV1(t *testing.T, databasePath string) {
	t.Helper()
	if _, err := currentstore.PrepareClosedCurrentStoreForPublication(
		context.Background(),
		databasePath,
	); err != nil {
		t.Fatalf("prepare closed Store for dry-run: %v", err)
	}
}

func readModuleDryRunActivationFactsV1(
	t *testing.T,
	databasePath string,
	instanceID string,
) (
	controlcontract.PublishedBasis,
	controlcontract.CatalogGeneration,
	currentstore.ModuleActivation,
) {
	t.Helper()
	ctx := context.Background()
	observer, err := currentstore.OpenReadOnlyObserver(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, _, catalog, basisErr := observer.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	activation, activationErr := observer.GetLatestModuleActivationForInstance(
		ctx,
		defaultTenantID,
		instanceID,
	)
	closeErr := observer.Close()
	if basisErr != nil || activationErr != nil || closeErr != nil {
		t.Fatalf(
			"read dry-run Activation facts for %q: %v",
			instanceID,
			errors.Join(basisErr, activationErr, closeErr),
		)
	}
	return basis, catalog, activation
}
