package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleApplyBackupRestoreEnabledAndDisabled(t *testing.T) {
	ctx := context.Background()
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

	enablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	enabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil {
		t.Fatalf("ENABLE module before backup: %v", err)
	}
	assertModuleApplyResultV1(
		t,
		enabled,
		moduleApplyStatusApplied,
		moduleApplyEnabledV1,
		2,
	)
	assertModuleApplyMCPNotStartedV1(t, eventPath)
	enabledBasis, _, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	enabledHistory := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(
			databasePath,
			enabledBasis,
			moduleApplyTestProfileID,
		),
	)

	enabledBundle := filepath.Join(root, "enabled.bundle")
	createAndVerifyModuleApplyBundleV1(
		t,
		databasePath,
		artifactRoot,
		enabledBundle,
	)
	enabledRestoreRoot := filepath.Join(root, "enabled-restored")
	enabledDatabase, enabledArtifacts := restoreModuleApplyBundleV1(
		t,
		enabledBundle,
		enabledRestoreRoot,
	)
	restoredEnabledHistory := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(
			enabledDatabase,
			enabledBasis,
			moduleApplyTestProfileID,
		),
	)
	if !reflect.DeepEqual(restoredEnabledHistory, enabledHistory) {
		t.Fatal("enabled module history changed across backup/restore")
	}

	composition, err := openProductionComposition(
		ctx,
		enabledDatabase,
		enabledArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open enabled restored composition: %v", err)
	}
	actionInput := moduleApplyChatInputV1(
		"module-apply-enabled-restored-action",
		"one MCP tool call after restoring an enabled module",
	)
	actionResult, chatErr := composition.chat.Chat(ctx, actionInput)
	if chatErr != nil {
		_ = composition.Close()
		t.Fatalf("chat through restored enabled MCP: %v", chatErr)
	}
	if !actionResult.AdmissionCreated || actionResult.TerminalResult == nil ||
		actionResult.LoopResult.Disposition != loopapi.DispositionTerminated ||
		actionResult.Reply != "text.stats completed." || actionResult.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("restored enabled Action chat result=%+v", actionResult)
	}
	actionModel, readErr := composition.store.GetModelDispatchRecord(
		ctx,
		actionResult.TerminalResult.AttemptID,
	)
	if readErr != nil || actionModel.Attempt.SourceDispatchAttemptID == "" {
		_ = composition.Close()
		t.Fatalf("restored enabled final model=%+v, %v", actionModel, readErr)
	}
	if closeErr := composition.Close(); closeErr != nil {
		t.Fatalf("close enabled restored composition: %v", closeErr)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)

	disablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable.json"),
		newDisabledModuleApplyPlanFixtureV1(t, 2),
	)
	disabled, err := runModuleApplyFixtureV1(
		enabledDatabase,
		enabledArtifacts,
		disablePlan,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("DISABLE restored module: %v", err)
	}
	assertModuleApplyResultV1(
		t,
		disabled,
		moduleApplyStatusApplied,
		moduleApplyDisabledV1,
		3,
	)
	assertMCPProductionEvents(t, eventPath, 2, 1)
	disabledBasis, _, _ := loadModuleApplyPublishedStateV1(t, enabledDatabase)
	disabledHistory := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(
			enabledDatabase,
			disabledBasis,
			moduleApplyTestProfileID,
		),
	)
	historicalEnabledAfterDisable := runModuleOperatorCommandV1(
		t,
		moduleHistoryCommandV1(
			enabledDatabase,
			enabledBasis,
			moduleApplyTestProfileID,
		),
	)
	if !reflect.DeepEqual(historicalEnabledAfterDisable, enabledHistory) {
		t.Fatal("disable changed the exact enabled historical projection")
	}

	disabledBundle := filepath.Join(root, "disabled.bundle")
	createAndVerifyModuleApplyBundleV1(
		t,
		enabledDatabase,
		enabledArtifacts,
		disabledBundle,
	)
	disabledDatabase, disabledArtifacts := restoreModuleApplyBundleV1(
		t,
		disabledBundle,
		filepath.Join(root, "disabled-restored"),
	)
	for label, expected := range map[string][]byte{
		"enabled":  enabledHistory,
		"disabled": disabledHistory,
	} {
		basis := enabledBasis
		if label == "disabled" {
			basis = disabledBasis
		}
		actual := runModuleOperatorCommandV1(
			t,
			moduleHistoryCommandV1(
				disabledDatabase,
				basis,
				moduleApplyTestProfileID,
			),
		)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("%s module history changed across disabled backup/restore", label)
		}
	}
	assertRestoredModuleApplyInstallationV1(
		t,
		disabledDatabase,
		disabledArtifacts,
		fixture,
	)

	disabledComposition, err := openProductionComposition(
		ctx,
		disabledDatabase,
		disabledArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open disabled restored composition: %v", err)
	}
	pureInput := moduleApplyChatInputV1(
		"module-apply-disabled-restored-pure-chat",
		"pure chat after restoring a disabled module",
	)
	pureResult, pureErr := disabledComposition.chat.Chat(ctx, pureInput)
	if pureErr != nil {
		_ = disabledComposition.Close()
		t.Fatalf("chat through restored disabled composition: %v", pureErr)
	}
	if !pureResult.AdmissionCreated || pureResult.TerminalResult == nil ||
		pureResult.LoopResult.Disposition != loopapi.DispositionTerminated ||
		pureResult.Reply != pureInput.Message || pureResult.FailureCode != "" {
		_ = disabledComposition.Close()
		t.Fatalf("restored disabled Pure Chat result=%+v", pureResult)
	}
	pureModel, readErr := disabledComposition.store.GetModelDispatchRecord(
		ctx,
		pureResult.TerminalResult.AttemptID,
	)
	if readErr != nil || pureModel.Attempt.SourceDispatchAttemptID != "" {
		_ = disabledComposition.Close()
		t.Fatalf("restored DISABLE executed an Action: model=%+v, %v", pureModel, readErr)
	}
	if closeErr := disabledComposition.Close(); closeErr != nil {
		t.Fatalf("close disabled restored composition: %v", closeErr)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)
}

func TestModuleApplyPreservesMixedBindingOrder(t *testing.T) {
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

	initial := loadModuleApplyProfileBindingsV1(
		t,
		databasePath,
		moduleApplyTestProfileID,
	)
	if len(initial) < 2 || len(moduleApplyActionInstanceIDsV1(initial)) != 0 {
		t.Fatalf("Pure Chat mixed-binding prerequisite=%+v", initial)
	}

	firstPlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-order-a.json"),
		newNamedEnabledModuleApplyPlanV1(
			t,
			fixture,
			moduleApplyTestProfileID,
			"mcp-order-a",
			"order.action.a",
			1,
			0,
		),
	)
	first, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		firstPlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || first.Status != moduleApplyStatusApplied || first.PointerRevision != 2 {
		t.Fatalf("apply first ordered Action=%+v, %v", first, err)
	}
	afterFirst := loadModuleApplyProfileBindingsV1(
		t,
		databasePath,
		moduleApplyTestProfileID,
	)
	wantAfterFirst := appendModuleApplyBindingCopyV1(initial, afterFirst[len(afterFirst)-1])
	if !reflect.DeepEqual(afterFirst, wantAfterFirst) ||
		!reflect.DeepEqual(
			moduleApplyActionInstanceIDsV1(afterFirst),
			[]string{"mcp-order-a"},
		) {
		t.Fatalf("first Action changed global Binding order: before=%+v after=%+v", initial, afterFirst)
	}

	secondPlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-order-b.json"),
		newNamedEnabledModuleApplyPlanV1(
			t,
			fixture,
			moduleApplyTestProfileID,
			"mcp-order-b",
			"order.action.b",
			2,
			0,
		),
	)
	second, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		secondPlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || second.Status != moduleApplyStatusApplied || second.PointerRevision != 3 {
		t.Fatalf("insert second ordered Action=%+v, %v", second, err)
	}
	afterSecond := loadModuleApplyProfileBindingsV1(
		t,
		databasePath,
		moduleApplyTestProfileID,
	)
	if !reflect.DeepEqual(
		moduleApplyNonActionBindingsV1(afterSecond),
		moduleApplyNonActionBindingsV1(initial),
	) {
		t.Fatalf("Action insertion reordered non-Action Bindings: before=%+v after=%+v", initial, afterSecond)
	}
	if got := moduleApplyActionInstanceIDsV1(afterSecond); !reflect.DeepEqual(
		got,
		[]string{"mcp-order-b", "mcp-order-a"},
	) {
		t.Fatalf("Action subsequence=%v", got)
	}
	wantAfterSecond := make([]controlcontract.BindingSpec, 0, len(afterSecond))
	wantAfterSecond = append(wantAfterSecond, initial...)
	wantAfterSecond = append(
		wantAfterSecond,
		moduleApplyBindingByInstanceV1(t, afterSecond, "mcp-order-b"),
		moduleApplyBindingByInstanceV1(t, afterSecond, "mcp-order-a"),
	)
	if !reflect.DeepEqual(afterSecond, wantAfterSecond) {
		t.Fatalf("global Binding order changed: got=%+v want=%+v", afterSecond, wantAfterSecond)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)
}

func TestModuleApplySharedInstanceCatalogLastReference(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"s3c-deepseek-v4-flash-reviewer-off.bootstrap.seed.json",
	)
	initialized, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	})
	if err != nil {
		t.Fatalf("initialize shared-Profile fixture: %v", err)
	}
	if initialized.Defaults.ProfileID != "s3c.coordinator" {
		t.Fatalf("shared-Profile defaults=%+v", initialized.Defaults)
	}

	const sharedInstance = "mcp-shared-instance"
	for index, profileID := range []string{"s3c.coordinator", "s3c.backend"} {
		expectedPointer := uint64(index + 1)
		plan := writeModuleApplyPlanFixtureV1(
			t,
			filepath.Join(root, "enable-shared-"+profileID+".json"),
			newNamedEnabledModuleApplyPlanV1(
				t,
				fixture,
				profileID,
				sharedInstance,
				"shared.text.stats",
				expectedPointer,
				0,
			),
		)
		result, applyErr := runModuleApplyFixtureV1(
			databasePath,
			artifactRoot,
			plan,
			fixture.ArtifactDirectory,
			fixture.ArtifactDigest,
		)
		if applyErr != nil || result.Status != moduleApplyStatusApplied ||
			result.PointerRevision != expectedPointer+1 {
			t.Fatalf("enable shared Instance for %s=%+v, %v", profileID, result, applyErr)
		}
	}
	assertModuleApplyCatalogReferenceV1(t, databasePath, sharedInstance, true, 2)

	disableFirst := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-shared-coordinator.json"),
		newNamedDisabledModuleApplyPlanV1(t, "s3c.coordinator", sharedInstance, 3),
	)
	firstDisabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disableFirst,
		"",
		"",
	)
	if err != nil || firstDisabled.Status != moduleApplyStatusApplied ||
		firstDisabled.PointerRevision != 4 {
		t.Fatalf("disable first shared reference=%+v, %v", firstDisabled, err)
	}
	assertModuleApplyCatalogReferenceV1(t, databasePath, sharedInstance, true, 1)

	disableLast := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-shared-backend.json"),
		newNamedDisabledModuleApplyPlanV1(t, "s3c.backend", sharedInstance, 4),
	)
	lastDisabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disableLast,
		"",
		"",
	)
	if err != nil || lastDisabled.Status != moduleApplyStatusApplied ||
		lastDisabled.PointerRevision != 5 {
		t.Fatalf("disable last shared reference=%+v, %v", lastDisabled, err)
	}
	assertModuleApplyCatalogReferenceV1(t, databasePath, sharedInstance, false, 0)
	assertRestoredModuleApplyInstallationV1(t, databasePath, artifactRoot, fixture)
	assertModuleApplyMCPNotStartedV1(t, eventPath)
}

func createAndVerifyModuleApplyBundleV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	bundlePath string,
) {
	t.Helper()
	ctx := context.Background()
	created, err := currentbackup.CreateBundle(
		ctx,
		databasePath,
		artifactRoot,
		bundlePath,
		"freeagent-module-apply-backup-order-test/v1",
	)
	if err != nil {
		t.Fatalf("create Current backup %s: %v", filepath.Base(bundlePath), err)
	}
	verified, err := currentbackup.VerifyBundle(ctx, bundlePath)
	if err != nil || verified.ManifestDigest != created.ManifestDigest {
		t.Fatalf("verify Current backup %s=%+v, %v", filepath.Base(bundlePath), verified, err)
	}
}

func restoreModuleApplyBundleV1(
	t *testing.T,
	bundlePath string,
	restoreRoot string,
) (string, string) {
	t.Helper()
	if err := os.MkdirAll(restoreRoot, 0o700); err != nil {
		t.Fatalf("create restore root: %v", err)
	}
	databasePath := filepath.Join(restoreRoot, "current.sqlite")
	artifactRoot := filepath.Join(restoreRoot, "artifacts")
	if err := currentbackup.RestoreBundle(
		context.Background(),
		bundlePath,
		databasePath,
		artifactRoot,
	); err != nil {
		t.Fatalf("restore Current backup %s: %v", filepath.Base(bundlePath), err)
	}
	return databasePath, artifactRoot
}

func assertRestoredModuleApplyInstallationV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	fixture moduleApplyMCPFixtureV1,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open restored Store for Installation: %v", err)
	}
	installation, readErr := store.GetModuleInstallationByIdentity(
		ctx,
		fixture.ModuleID,
		moduleApplyTestVersion,
	)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil ||
		installation.ArtifactDigest != fixture.ArtifactDigest {
		t.Fatalf("restored Installation=%+v, %v", installation, errors.Join(readErr, closeErr))
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		filepath.Join(artifactRoot, fixture.ArtifactDigest),
		moduleapi.ArtifactMetadataPaths{},
		fixture.ArtifactDigest,
		fixture.ArtifactSizeBytes,
	); err != nil {
		t.Fatalf("verify retained module artifact: %v", err)
	}
}

func newNamedEnabledModuleApplyPlanV1(
	t *testing.T,
	fixture moduleApplyMCPFixtureV1,
	profileID string,
	instanceID string,
	publicActionID string,
	expectedPointer uint64,
	actionIndex uint32,
) []byte {
	t.Helper()
	_, config, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   publicActionID,
				ProviderActionID: "text.stats",
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   256,
			}},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("freeze named Action config: %v", err)
	}
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 defaultTenantID,
			AllowedWorkspaceIDs:      []string{"*"},
			AllowedProviderActionIDs: []string{"text.stats"},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           256,
		},
	)
	if err != nil {
		t.Fatalf("freeze named Action authority: %v", err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(profileID),
		"instance_id":               instanceID,
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  fixture.ModuleID,
			"exact_version":       moduleApplyTestVersion,
			"artifact_digest":     fixture.ArtifactDigest,
			"artifact_size_bytes": fixture.ArtifactSizeBytes,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestLocalProcess),
				"protocol": moduleapi.RuntimeProtocolMCPStdio20251125,
			},
		},
		"binding": map[string]any{
			"port_binding_index": actionIndex,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
	})
}

func newNamedDisabledModuleApplyPlanV1(
	t *testing.T,
	profileID string,
	instanceID string,
	expectedPointer uint64,
) []byte {
	t.Helper()
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyDisabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(profileID),
		"instance_id":               instanceID,
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
	})
}

func loadModuleApplyProfileBindingsV1(
	t *testing.T,
	databasePath string,
	profileID string,
) []controlcontract.BindingSpec {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Store for Profile Bindings: %v", err)
	}
	_, control, _, readErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("load Profile Bindings: %v", errors.Join(readErr, closeErr))
	}
	profile, found := control.FindProfile(profileID)
	if !found {
		t.Fatalf("Profile %q is absent", profileID)
	}
	return append([]controlcontract.BindingSpec(nil), profile.Bindings...)
}

func moduleApplyActionInstanceIDsV1(bindings []controlcontract.BindingSpec) []string {
	result := make([]string, 0)
	for _, binding := range bindings {
		if binding.Port == productionActionPort {
			result = append(result, binding.InstanceID)
		}
	}
	return result
}

func moduleApplyNonActionBindingsV1(
	bindings []controlcontract.BindingSpec,
) []controlcontract.BindingSpec {
	result := make([]controlcontract.BindingSpec, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Port != productionActionPort {
			result = append(result, binding)
		}
	}
	return result
}

func moduleApplyBindingByInstanceV1(
	t *testing.T,
	bindings []controlcontract.BindingSpec,
	instanceID string,
) controlcontract.BindingSpec {
	t.Helper()
	for _, binding := range bindings {
		if binding.InstanceID == instanceID {
			return binding
		}
	}
	t.Fatalf("Binding for Instance %q is absent", instanceID)
	return controlcontract.BindingSpec{}
}

func appendModuleApplyBindingCopyV1(
	bindings []controlcontract.BindingSpec,
	binding controlcontract.BindingSpec,
) []controlcontract.BindingSpec {
	result := make([]controlcontract.BindingSpec, 0, len(bindings)+1)
	result = append(result, bindings...)
	return append(result, binding)
}

func assertModuleApplyCatalogReferenceV1(
	t *testing.T,
	databasePath string,
	instanceID string,
	wantCatalog bool,
	wantBindingReferences int,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Store for shared Instance: %v", err)
	}
	_, control, catalog, readErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("load shared Instance state: %v", errors.Join(readErr, closeErr))
	}
	_, catalogFound := catalog.FindInstance(instanceID)
	if catalogFound != wantCatalog {
		t.Fatalf("shared Instance Catalog presence=%v want %v", catalogFound, wantCatalog)
	}
	references := 0
	for _, profile := range control.Profiles {
		for _, binding := range profile.Bindings {
			if binding.InstanceID == instanceID {
				references++
			}
		}
	}
	if references != wantBindingReferences {
		t.Fatalf("shared Instance Binding references=%d want %d", references, wantBindingReferences)
	}
}
