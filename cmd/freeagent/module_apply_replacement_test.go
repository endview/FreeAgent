package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleApplyExactDeclarativeReplacementDryRunApplyAndAdjacentRetry(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	current := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		"replacement-current",
	)
	currentPlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "current.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, current, 1, 0),
	)
	if _, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		currentPlan,
		current.ArtifactDirectory,
		"",
	); err != nil {
		t.Fatalf("apply current: %v", err)
	}

	target := moduleApplyReplacementTargetFixtureV1(t, root, current)
	canonical := moduleApplyReplacementPlanV1(t, current, target, 2)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "replacement.json"),
		canonical,
	)
	dryRun, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		target.ArtifactDirectory,
		"",
	)
	if err != nil || dryRun.Status != moduleApplyStatusWouldApply ||
		dryRun.Changes.Binding.Change != moduleDryRunBindingReplaceV1 ||
		dryRun.Changes.Catalog != moduleDryRunCatalogReplaceInstanceV1 {
		t.Fatalf("replacement dry-run = %+v, %v", dryRun, err)
	}
	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		target.ArtifactDirectory,
		"",
	)
	if err != nil || applied.Status != moduleApplyStatusApplied ||
		applied.PointerRevision != 3 {
		t.Fatalf("replacement apply = %+v, %v", applied, err)
	}
	retried, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		target.ArtifactDirectory,
		"",
	)
	if err != nil || retried.Status != moduleApplyStatusAlreadyApplied ||
		retried.PointerRevision != 3 {
		t.Fatalf("replacement retry = %+v, %v", retried, err)
	}

	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	closeErr := store.Close()
	if err != nil || closeErr != nil || basis.PointerRevision != 3 {
		t.Fatalf("load replacement publication: %+v, %v, %v", basis, err, closeErr)
	}
	profile, found := control.FindProfile(moduleApplyTestProfileID)
	if !found {
		t.Fatal("replacement Profile is absent")
	}
	contextInstances := []string{}
	for _, binding := range profile.Bindings {
		if binding.Port == productionContextPort {
			contextInstances = append(contextInstances, binding.InstanceID)
		}
	}
	targetCount := 0
	currentCount := 0
	for _, instanceID := range contextInstances {
		if instanceID == target.InstanceID {
			targetCount++
		}
		if instanceID == current.InstanceID {
			currentCount++
		}
	}
	if targetCount != 1 || currentCount != 0 {
		t.Fatalf("replacement Context Bindings = %v", contextInstances)
	}
	if _, found := catalog.FindInstance(current.InstanceID); found {
		t.Fatal("replacement retained old Catalog Instance")
	}
	if entry, found := catalog.FindInstance(target.InstanceID); !found ||
		entry.Activation.ModuleID != target.ModuleID ||
		entry.Activation.Version != target.ExactVersion {
		t.Fatalf("replacement target Catalog entry = %+v, %v", entry, found)
	}
}

func TestModuleApplyExactReplacementRejectsSharedOldInstanceAndWrongOrdinal(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	current := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		"replacement-shared-current",
	)
	first := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "first.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, current, 1, 0),
	)
	if _, err := runModuleApplyFixtureV1(
		databasePath, artifactRoot, first, current.ArtifactDirectory, "",
	); err != nil {
		t.Fatal(err)
	}
	target := moduleApplyReplacementTargetFixtureV1(t, root, current)
	canonical := moduleApplyReplacementPlanForProfileV1(
		t,
		current,
		target,
		moduleApplyTestProfileID,
		2,
		0,
	)
	plan, _, _, err := restoreModuleApplyPlanV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, control, catalog, err := store.LoadPublishedBasis(context.Background(), defaultTenantID)
	closeErr := store.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("load replacement current: %v, %v", err, closeErr)
	}
	profileIndex := -1
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID == moduleApplyTestProfileID {
			profileIndex = index
			break
		}
	}
	if profileIndex < 0 {
		t.Fatal("test Profile is absent")
	}
	var oldBinding controlcontract.BindingSpec
	for _, binding := range control.Profiles[profileIndex].Bindings {
		if binding.Port == productionContextPort && binding.InstanceID == current.InstanceID {
			oldBinding = binding
			break
		}
	}
	control.Profiles[profileIndex].Bindings = append(
		control.Profiles[profileIndex].Bindings,
		oldBinding,
	)
	if _, err := inspectModuleApplyReplacementStateV1(plan, control, catalog); !errors.Is(err, errModuleApplyReplacementConflictV1) {
		t.Fatalf("shared old Instance replacement error = %v", err)
	}

	wrongOrdinal := moduleApplyReplacementPlanForProfileV1(
		t,
		current,
		target,
		moduleApplyTestProfileID,
		2,
		1,
	)
	wrongPlan, _, _, err := restoreModuleApplyPlanV1(wrongOrdinal)
	if err != nil {
		t.Fatal(err)
	}
	control.Profiles[profileIndex].Bindings = control.Profiles[profileIndex].Bindings[:len(control.Profiles[profileIndex].Bindings)-1]
	if _, err := inspectModuleApplyReplacementStateV1(wrongPlan, control, catalog); !errors.Is(err, errModuleApplyReplacementConflictV1) {
		t.Fatalf("wrong ordinal replacement error = %v", err)
	}
}

type moduleApplyReplacementFixtureV1 struct {
	ModuleID          string
	ExactVersion      string
	InstanceID        string
	ArtifactDirectory string
	ArtifactDigest    string
	ArtifactSizeBytes uint64
}

func moduleApplyReplacementTargetFixtureV1(
	t *testing.T,
	root string,
	current moduleApplyDeclarativeFixtureV1,
) moduleApplyReplacementFixtureV1 {
	t.Helper()
	directory := filepath.Join(root, "replacement-target")
	copyModuleApplyTestTreeV1(t, current.ArtifactDirectory, directory)
	manifestPath := filepath.Join(directory, moduleapi.ArtifactManifestPath)
	canonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		context.Background(),
		directory,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = "2.0.0"
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err = moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := moduleconformance.VerifyDirectory(
		context.Background(),
		directory,
	)
	if err != nil {
		t.Fatal(err)
	}
	return moduleApplyReplacementFixtureV1{
		ModuleID: report.Module.ID, ExactVersion: report.Module.ExactVersion,
		InstanceID: "replacement-target", ArtifactDirectory: directory,
		ArtifactDigest: report.ArtifactDigest, ArtifactSizeBytes: report.ArtifactSizeBytes,
	}
}

func moduleApplyReplacementPlanV1(
	t *testing.T,
	current moduleApplyDeclarativeFixtureV1,
	target moduleApplyReplacementFixtureV1,
	pointer uint64,
) []byte {
	return moduleApplyReplacementPlanForProfileV1(
		t, current, target, moduleApplyTestProfileID, pointer, 0,
	)
}

func moduleApplyReplacementPlanForProfileV1(
	t *testing.T,
	current moduleApplyDeclarativeFixtureV1,
	target moduleApplyReplacementFixtureV1,
	profileID string,
	pointer uint64,
	ordinal uint32,
) []byte {
	t.Helper()
	_, config, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementTrustedInstruction,
			Parameters:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":              moduleApplyPlanSchemaV1,
		"desired_state":               string(moduleApplyEnabledV1),
		"tenant_id":                   defaultTenantID,
		"expected_pointer_revision":   pointer,
		"binding_target":              moduleApplyProfileBindingTargetTestValue(profileID),
		"instance_id":                 target.InstanceID,
		"replace_current_instance_id": current.InstanceID,
		"port":                        map[string]any{"name": productionContextPort.Name, "exact_version": productionContextPort.ExactVersion},
		"module": map[string]any{
			"id": target.ModuleID, "exact_version": target.ExactVersion,
			"artifact_digest":          target.ArtifactDigest,
			"artifact_size_bytes":      target.ArtifactSizeBytes,
			"expected_runtime_request": map[string]any{"mode": string(moduleapi.RuntimeModeRequestDeclarative), "protocol": moduleapi.RuntimeProtocolStaticV1},
		},
		"binding": map[string]any{
			"port_binding_index": ordinal, "config": json.RawMessage(config),
			"authority_ceiling": json.RawMessage(moduleApplyDenyAllAuthorityCanonicalV1),
			"failure_policy":    string(moduleapi.FailureRequired),
		},
	})
}
