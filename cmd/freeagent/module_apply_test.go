package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleApplyTestModuleID   = "freeagent.test.mcp.text_stats"
	moduleApplyTestVersion    = "1.0.0"
	moduleApplyTestInstanceID = "mcp-text-stats"
	moduleApplyTestProfileID  = "pure-chat"
)

func moduleApplyProfileBindingTargetTestValue(profileID string) map[string]any {
	return map[string]any{
		"kind":       string(moduleApplyBindingTargetProfileV1),
		"profile_id": profileID,
	}
}

type moduleApplyMCPFixtureV1 struct {
	ModuleID          string
	ArtifactDirectory string
	ArtifactDigest    string
	ArtifactSizeBytes uint64
}

type moduleApplyObservedStateV1 struct {
	PointerRevision     uint64
	ControlSnapshotID   string
	CatalogGenerationID string
	ArtifactEntries     []string
	MutationRowCounts   map[string]int64
	InstallationExists  bool
}

func TestModuleApplyEnableRetryChatDisableLifecycle(t *testing.T) {
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
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	enablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-v1.json"),
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
		t.Fatalf("ENABLE module: %v", err)
	}
	assertModuleApplyResultV1(
		t,
		enabled,
		moduleApplyStatusApplied,
		moduleApplyEnabledV1,
		2,
	)
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	retried, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil {
		t.Fatalf("exact ENABLE retry: %v", err)
	}
	assertModuleApplyResultV1(
		t,
		retried,
		moduleApplyStatusAlreadyApplied,
		moduleApplyEnabledV1,
		2,
	)
	if retried.PlanDigest != enabled.PlanDigest ||
		retried.ControlSnapshotID != enabled.ControlSnapshotID ||
		retried.CatalogGenerationID != enabled.CatalogGenerationID {
		t.Fatalf("exact retry changed publication identity: first=%+v retry=%+v", enabled, retried)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	noChangePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-no-change.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 2, 0),
	)
	noChange, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		noChangePlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil {
		t.Fatalf("exact desired state at current pointer: %v", err)
	}
	assertModuleApplyResultV1(
		t,
		noChange,
		moduleApplyStatusNoChange,
		moduleApplyEnabledV1,
		2,
	)
	if noChange.ControlSnapshotID != enabled.ControlSnapshotID ||
		noChange.CatalogGenerationID != enabled.CatalogGenerationID {
		t.Fatalf("NO_CHANGE published new state: enabled=%+v no-change=%+v", enabled, noChange)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open enabled composition: %v", err)
	}
	actionInput := moduleApplyChatInputV1(
		"module-apply-enabled-action-1",
		"one MCP tool call after module apply",
	)
	actionResult, chatErr := composition.chat.Chat(ctx, actionInput)
	if chatErr != nil {
		_ = composition.Close()
		t.Fatalf("chat through enabled MCP: %v", chatErr)
	}
	if !actionResult.AdmissionCreated || actionResult.TerminalResult == nil ||
		actionResult.LoopResult.Disposition != loopapi.DispositionTerminated ||
		actionResult.Reply != "text.stats completed." || actionResult.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("enabled Action chat result=%+v", actionResult)
	}
	actionTerminal := *actionResult.TerminalResult
	if actionTerminal.AttemptKind != corecontract.AttemptKindModel ||
		actionTerminal.State != corecontract.ModelAttemptSucceeded {
		_ = composition.Close()
		t.Fatalf("enabled Action terminal=%+v", actionTerminal)
	}
	actionModel, readErr := composition.store.GetModelDispatchRecord(
		ctx,
		actionTerminal.AttemptID,
	)
	if readErr != nil || actionModel.Attempt.SourceDispatchAttemptID == "" {
		_ = composition.Close()
		t.Fatalf("enabled Action final model=%+v, %v", actionModel, readErr)
	}
	if closeErr := composition.Close(); closeErr != nil {
		t.Fatalf("close enabled composition: %v", closeErr)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)

	disablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-v1.json"),
		newDisabledModuleApplyPlanFixtureV1(t, 2),
	)
	disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePlan,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("DISABLE module: %v", err)
	}
	assertModuleApplyResultV1(
		t,
		disabled,
		moduleApplyStatusApplied,
		moduleApplyDisabledV1,
		3,
	)
	assertMCPProductionEvents(t, eventPath, 2, 1)

	disabledComposition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open disabled composition: %v", err)
	}
	pureInput := moduleApplyChatInputV1(
		"module-apply-disabled-pure-chat-1",
		"pure chat after disabling the MCP module",
	)
	pureResult, pureErr := disabledComposition.chat.Chat(ctx, pureInput)
	if pureErr != nil {
		_ = disabledComposition.Close()
		t.Fatalf("chat after DISABLE: %v", pureErr)
	}
	if !pureResult.AdmissionCreated || pureResult.TerminalResult == nil ||
		pureResult.LoopResult.Disposition != loopapi.DispositionTerminated ||
		pureResult.Reply != pureInput.Message || pureResult.FailureCode != "" {
		_ = disabledComposition.Close()
		t.Fatalf("disabled Pure Chat result=%+v", pureResult)
	}
	pureTerminal := *pureResult.TerminalResult
	pureModel, readErr := disabledComposition.store.GetModelDispatchRecord(
		ctx,
		pureTerminal.AttemptID,
	)
	if readErr != nil || pureModel.Attempt.SourceDispatchAttemptID != "" {
		_ = disabledComposition.Close()
		t.Fatalf("DISABLE left an Action execution in the new chat: model=%+v, %v", pureModel, readErr)
	}
	if closeErr := disabledComposition.Close(); closeErr != nil {
		t.Fatalf("close disabled composition: %v", closeErr)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)
}

func TestModuleApplyEmergencyDisableAllowsMissingLastReferencedArtifact(t *testing.T) {
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
	if err != nil || enabled.Status != moduleApplyStatusApplied ||
		enabled.PointerRevision != 2 {
		t.Fatalf("prepare emergency DISABLE fixture=%+v, %v", enabled, err)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	publishedArtifact := filepath.Join(artifactRoot, fixture.ArtifactDigest)
	if err := os.RemoveAll(publishedArtifact); err != nil {
		t.Fatalf("remove referenced test artifact: %v", err)
	}
	if _, err := os.Lstat(publishedArtifact); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("referenced artifact still exists: %v", err)
	}

	disablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "emergency-disable.json"),
		newDisabledModuleApplyPlanFixtureV1(t, 2),
	)
	disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePlan,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("emergency DISABLE with missing final referenced artifact: %v", err)
	}
	assertModuleApplyResultV1(
		t,
		disabled,
		moduleApplyStatusApplied,
		moduleApplyDisabledV1,
		3,
	)
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Store after emergency DISABLE: %v", err)
	}
	installation, installErr := store.GetModuleInstallationByIdentity(
		ctx,
		moduleApplyTestModuleID,
		moduleApplyTestVersion,
	)
	closeErr := store.Close()
	if installErr != nil || closeErr != nil ||
		installation.ArtifactDigest != fixture.ArtifactDigest {
		t.Fatalf(
			"emergency DISABLE did not retain immutable installation=%+v, %v",
			installation,
			errors.Join(installErr, closeErr),
		)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)
}

func TestApplyModulePlanCanonicalBytesAreTheSoleAuthority(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	before := observeModuleApplyStateV1(t, databasePath, artifactRoot)

	planCanonical := newDisabledModuleApplyPlanFixtureV1(t, 1)
	parsed, canonical, digest, err := restoreModuleApplyPlanV1(planCanonical)
	if err != nil {
		t.Fatalf("restore canonical authority fixture: %v", err)
	}
	parsed.TenantID = "untrusted-parsed-tenant"
	parsed.BindingTarget.ProfileID = "untrusted-parsed-profile"
	parsed.ExpectedPointerRevision = 99
	result, err := applyModulePlanV1(ctx, moduleApplyCommandInputV1{
		DatabasePath:  databasePath,
		ArtifactRoot:  artifactRoot,
		Plan:          parsed,
		PlanCanonical: canonical,
		PlanDigest:    digest,
	})
	if err != nil {
		t.Fatalf("canonical plan should override parsed copy: %v", err)
	}
	assertModuleApplyResultV1(
		t,
		result,
		moduleApplyStatusNoChange,
		moduleApplyDisabledV1,
		1,
	)
	if after := observeModuleApplyStateV1(t, databasePath, artifactRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("canonical NO_CHANGE changed state: before=%+v after=%+v", before, after)
	}

	_, err = applyModulePlanV1(ctx, moduleApplyCommandInputV1{
		DatabasePath:  databasePath,
		ArtifactRoot:  artifactRoot,
		Plan:          parsed,
		PlanCanonical: canonical,
		PlanDigest:    strings.Repeat("0", moduleapi.SHA256HexLength),
	})
	if err == nil || moduleApplyFailureCodeOfV1(err) != moduleApplyFailurePlanInvalid {
		t.Fatalf("digest mismatch error=%v code=%s", err, moduleApplyFailureCodeOfV1(err))
	}
	if after := observeModuleApplyStateV1(t, databasePath, artifactRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("digest mismatch changed state: before=%+v after=%+v", before, after)
	}
}

func TestModuleApplyDisableReenableCreatesNextActivationAndRuns(t *testing.T) {
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
		filepath.Join(root, "enable-first.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	first, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || first.Status != moduleApplyStatusApplied ||
		first.PointerRevision != 2 {
		t.Fatalf("first ENABLE=%+v, %v", first, err)
	}
	firstActivation := readLatestModuleApplyActivationV1(t, databasePath)
	if firstActivation.ActivationRevision != 1 {
		t.Fatalf("first Activation=%+v", firstActivation)
	}
	disablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable.json"),
		newDisabledModuleApplyPlanFixtureV1(t, 2),
	)
	disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePlan,
		"",
		"",
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied ||
		disabled.PointerRevision != 3 {
		t.Fatalf("DISABLE=%+v, %v", disabled, err)
	}

	reenablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-second.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 3, 0),
	)
	reenabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		reenablePlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil {
		t.Fatalf("second ENABLE: %v", err)
	}
	assertModuleApplyResultV1(
		t,
		reenabled,
		moduleApplyStatusApplied,
		moduleApplyEnabledV1,
		4,
	)
	secondActivation := readLatestModuleApplyActivationV1(t, databasePath)
	if secondActivation.ActivationRevision != firstActivation.ActivationRevision+1 ||
		secondActivation.ActivationRevision != 2 ||
		secondActivation.InstallationID != firstActivation.InstallationID ||
		secondActivation.ActivationID == firstActivation.ActivationID {
		t.Fatalf(
			"re-enable did not create historical max+1 Activation: first=%+v second=%+v",
			firstActivation,
			secondActivation,
		)
	}
	assertCurrentModuleApplyActivationV1(t, databasePath, secondActivation)
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	retried, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		reenablePlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil {
		t.Fatalf("re-enable exact retry: %v", err)
	}
	assertModuleApplyResultV1(
		t,
		retried,
		moduleApplyStatusAlreadyApplied,
		moduleApplyEnabledV1,
		4,
	)
	if latest := readLatestModuleApplyActivationV1(t, databasePath); latest.ActivationID != secondActivation.ActivationID ||
		latest.ActivationRevision != secondActivation.ActivationRevision {
		t.Fatalf("exact retry created another Activation: second=%+v latest=%+v", secondActivation, latest)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open re-enabled composition: %v", err)
	}
	input := moduleApplyChatInputV1(
		"module-apply-reenabled-action-1",
		"one MCP tool call after re-enable",
	)
	result, chatErr := composition.chat.Chat(ctx, input)
	closeErr := composition.Close()
	if chatErr != nil || closeErr != nil || !result.AdmissionCreated ||
		result.TerminalResult == nil ||
		result.LoopResult.Disposition != loopapi.DispositionTerminated ||
		result.Reply != "text.stats completed." || result.FailureCode != "" {
		t.Fatalf("re-enabled Action chat=%+v, %v", result, errors.Join(chatErr, closeErr))
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)
}

func TestModuleApplyFailuresLeaveStoreAndArtifactsUnchanged(t *testing.T) {
	tests := []struct {
		name            string
		expectedPointer uint64
		actionIndex     uint32
		grant           func(moduleApplyMCPFixtureV1) string
		wantFailure     string
		compareRawStore bool
	}{
		{
			name:            "invalid grant",
			expectedPointer: 1,
			actionIndex:     0,
			grant: func(moduleApplyMCPFixtureV1) string {
				return strings.Repeat("0", moduleapi.SHA256HexLength)
			},
			wantFailure:     "(GRANT_REQUIRED)",
			compareRawStore: true,
		},
		{
			name:            "pointer conflict",
			expectedPointer: 2,
			actionIndex:     0,
			grant: func(fixture moduleApplyMCPFixtureV1) string {
				return fixture.ArtifactDigest
			},
			wantFailure: "(POINTER_CONFLICT)",
		},
		{
			name:            "Action index conflict",
			expectedPointer: 1,
			actionIndex:     1,
			grant: func(fixture moduleApplyMCPFixtureV1) string {
				return fixture.ArtifactDigest
			},
			wantFailure: "(TARGET_CONFLICT)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
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
			before := observeModuleApplyStateV1(t, databasePath, artifactRoot)
			var rawStoreBefore []byte
			if test.compareRawStore {
				var err error
				rawStoreBefore, err = os.ReadFile(databasePath)
				if err != nil {
					t.Fatalf("read Store before invalid grant: %v", err)
				}
			}
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "enable.json"),
				newEnabledModuleApplyPlanFixtureV1(
					t,
					fixture,
					test.expectedPointer,
					test.actionIndex,
				),
			)
			_, err := runModuleApplyFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				test.grant(fixture),
			)
			if err == nil || !strings.Contains(err.Error(), test.wantFailure) {
				t.Fatalf("failure=%v want %s", err, test.wantFailure)
			}
			after := observeModuleApplyStateV1(t, databasePath, artifactRoot)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("failed apply changed durable state:\nbefore=%+v\nafter=%+v", before, after)
			}
			if test.compareRawStore {
				rawStoreAfter, readErr := os.ReadFile(databasePath)
				if readErr != nil || !bytes.Equal(rawStoreAfter, rawStoreBefore) {
					t.Fatalf("invalid grant changed Store bytes: %v", readErr)
				}
			}
			if _, err := os.Lstat(filepath.Join(artifactRoot, fixture.ArtifactDigest)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed apply published target artifact: %v", err)
			}
			assertNoModuleApplyStageResidueV1(t, root)
			assertModuleApplyMCPNotStartedV1(t, eventPath)
		})
	}
}

func newModuleApplyMCPFixtureV1(
	t *testing.T,
	root string,
	eventPath string,
) moduleApplyMCPFixtureV1 {
	t.Helper()
	_, digest := newMCPProductionFixture(t, root, "success", eventPath)
	artifactDirectory := filepath.Join(
		root,
		"bootstrap-artifacts",
		moduleApplyTestModuleID,
		moduleApplyTestVersion,
	)
	manifest, err := os.ReadFile(
		filepath.Join(artifactDirectory, moduleapi.ArtifactManifestPath),
	)
	if err != nil {
		t.Fatalf("read MCP test manifest: %v", err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatalf("scan MCP test artifact: %v", err)
	}
	size := uint64(len(manifest))
	for _, file := range files {
		size += uint64(len(file.Content))
	}
	return moduleApplyMCPFixtureV1{
		ModuleID:          moduleApplyTestModuleID,
		ArtifactDirectory: artifactDirectory,
		ArtifactDigest:    digest,
		ArtifactSizeBytes: size,
	}
}

func initializePureChatForModuleApplyV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
) {
	t.Helper()
	initialized, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	})
	if err != nil {
		t.Fatalf("initialize Pure Chat module-apply fixture: %v", err)
	}
	if initialized.Defaults.ProfileID != moduleApplyTestProfileID ||
		len(initialized.ArtifactLocks) != 2 {
		t.Fatalf("Pure Chat initialization=%+v", initialized)
	}
}

func newEnabledModuleApplyPlanFixtureV1(
	t *testing.T,
	fixture moduleApplyMCPFixtureV1,
	expectedPointer uint64,
	actionIndex uint32,
) []byte {
	t.Helper()
	_, config, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "text.stats",
				ProviderActionID: "text.stats",
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   256,
			}},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("freeze module-apply Action config: %v", err)
	}
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 defaultTenantID,
			AllowedWorkspaceIDs:      []string{defaultWorkspaceID},
			AllowedProviderActionIDs: []string{"text.stats"},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           256,
		},
	)
	if err != nil {
		t.Fatalf("freeze module-apply Action authority: %v", err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(moduleApplyTestProfileID),
		"instance_id":               moduleApplyTestInstanceID,
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

func newDisabledModuleApplyPlanFixtureV1(
	t *testing.T,
	expectedPointer uint64,
) []byte {
	t.Helper()
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyDisabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(moduleApplyTestProfileID),
		"instance_id":               moduleApplyTestInstanceID,
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
	})
}

func writeModuleApplyPlanFixtureV1(
	t *testing.T,
	path string,
	canonical []byte,
) string {
	t.Helper()
	if err := os.WriteFile(path, canonical, 0o600); err != nil {
		t.Fatalf("write module-apply plan: %v", err)
	}
	return path
}

func runModuleApplyFixtureV1(
	databasePath string,
	artifactRoot string,
	planPath string,
	artifactDirectory string,
	grant string,
) (moduleApplyResultV1, error) {
	args := []string{
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--plan", planPath,
	}
	if artifactDirectory != "" {
		args = append(args, "--artifact", artifactDirectory)
	}
	if grant != "" {
		args = append(args, "--allow-local-mcp-artifact", grant)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runModuleApply(context.Background(), args, &stdout, &stderr)
	if stderr.Len() != 0 {
		return moduleApplyResultV1{}, errors.Join(
			err,
			errors.New("module-apply unexpectedly wrote stderr: "+stderr.String()),
		)
	}
	if err != nil {
		if stdout.Len() != 0 {
			return moduleApplyResultV1{}, errors.Join(
				err,
				errors.New("failed module-apply unexpectedly wrote stdout"),
			)
		}
		return moduleApplyResultV1{}, err
	}
	var result moduleApplyResultV1
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(&result); decodeErr != nil {
		return moduleApplyResultV1{}, decodeErr
	}
	var trailing any
	if decodeErr := decoder.Decode(&trailing); !errors.Is(decodeErr, io.EOF) {
		return moduleApplyResultV1{}, errors.New("module-apply result has trailing JSON")
	}
	return result, nil
}

func assertModuleApplyResultV1(
	t *testing.T,
	result moduleApplyResultV1,
	status moduleApplyStatusV1,
	desired moduleApplyDesiredStateV1,
	pointer uint64,
) {
	t.Helper()
	if result.SchemaVersion != moduleApplyResultSchemaV1 ||
		result.Status != status || result.DesiredState != desired ||
		result.TenantID != defaultTenantID ||
		result.BindingTarget.ProfileID != moduleApplyTestProfileID ||
		result.InstanceID != moduleApplyTestInstanceID ||
		result.Port != productionActionPort ||
		result.PointerRevision != pointer ||
		result.ControlSnapshotID == "" || result.CatalogGenerationID == "" ||
		!moduleapi.ValidSHA256(result.PlanDigest) {
		t.Fatalf("module-apply result=%+v", result)
	}
	if desired == moduleApplyEnabledV1 {
		if result.Module == nil || result.Module.ID != moduleApplyTestModuleID ||
			result.Module.ExactVersion != moduleApplyTestVersion ||
			!moduleapi.ValidSHA256(result.Module.ArtifactDigest) {
			t.Fatalf("enabled module-apply result=%+v", result)
		}
	} else if result.Module != nil {
		t.Fatalf("disabled module-apply result carries module=%+v", result)
	}
}

func moduleApplyChatInputV1(requestID, message string) localchat.ChatInput {
	return localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-module-apply-test",
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   moduleApplyTestProfileID,
		Message:     message,
		RequestID:   requestID,
		Deadline: time.Date(
			2099,
			time.January,
			2,
			3,
			4,
			5,
			0,
			time.UTC,
		),
	}
}

func assertModuleApplyMCPNotStartedV1(t *testing.T, eventPath string) {
	t.Helper()
	if _, err := os.Lstat(eventPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("module apply started or accessed MCP process: %v", err)
	}
}

func readLatestModuleApplyActivationV1(
	t *testing.T,
	databasePath string,
) currentstore.ModuleActivation {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Store for latest Activation: %v", err)
	}
	activation, readErr := store.GetLatestModuleActivationForInstance(
		ctx,
		defaultTenantID,
		moduleApplyTestInstanceID,
	)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read latest module Activation: %v", errors.Join(readErr, closeErr))
	}
	return activation
}

func assertCurrentModuleApplyActivationV1(
	t *testing.T,
	databasePath string,
	want currentstore.ModuleActivation,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Store for current Catalog Activation: %v", err)
	}
	basis, _, catalog, readErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	entry, found := catalog.FindInstance(moduleApplyTestInstanceID)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil || !found {
		t.Fatalf(
			"read current Catalog Activation: found=%v, %v",
			found,
			errors.Join(readErr, closeErr),
		)
	}
	provider := entry.Activation
	if basis.PointerRevision != 4 ||
		provider.InstanceID != want.InstanceID ||
		provider.ActivationRevision != want.ActivationRevision ||
		provider.ModuleID != moduleApplyTestModuleID ||
		provider.Version != moduleApplyTestVersion {
		t.Fatalf(
			"current Catalog does not reference the new Activation: basis=%+v provider=%+v want=%+v",
			basis,
			provider,
			want,
		)
	}
}

func observeModuleApplyStateV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
) moduleApplyObservedStateV1 {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Store for module-apply observation: %v", err)
	}
	basis, _, _, basisErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	_, installErr := store.GetModuleInstallationByIdentity(
		ctx,
		moduleApplyTestModuleID,
		moduleApplyTestVersion,
	)
	closeErr := store.Close()
	if basisErr != nil || closeErr != nil ||
		(installErr != nil && !errors.Is(installErr, currentstore.ErrModuleInstallationNotFound)) {
		t.Fatalf(
			"observe module-apply Store: %v",
			errors.Join(basisErr, installErr, closeErr),
		)
	}
	entries, err := os.ReadDir(artifactRoot)
	if err != nil {
		t.Fatalf("read artifact root: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return moduleApplyObservedStateV1{
		PointerRevision:     basis.PointerRevision,
		ControlSnapshotID:   basis.Control.SnapshotID,
		CatalogGenerationID: basis.Catalog.GenerationID,
		ArtifactEntries:     names,
		MutationRowCounts:   moduleApplyMutationRowCountsV1(t, databasePath),
		InstallationExists:  installErr == nil,
	}
}

func moduleApplyMutationRowCountsV1(
	t *testing.T,
	databasePath string,
) map[string]int64 {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatalf("open read-only Store for row counts: %v", err)
	}
	defer database.Close()
	counts := make(map[string]int64)
	for _, table := range []string{
		"content_records",
		"control_snapshots",
		"module_installations",
		"module_activations",
		"runtime_catalog_generations",
		"control_current",
	} {
		var count int64
		if err := database.QueryRowContext(
			context.Background(),
			"SELECT COUNT(*) FROM "+table,
		).Scan(&count); err != nil {
			t.Fatalf("count module-apply mutation table %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}

func assertNoModuleApplyStageResidueV1(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read module-apply test root: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".freeagent-module-apply-") {
			t.Fatalf("failed module apply left staging residue %q", entry.Name())
		}
	}
}
