package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestW2E5AGovernedKnowledgeBackupDisableAndGrantNarrowing(
	t *testing.T,
) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	fixture := newModuleApplyKnowledgeFixtureVersionV1(t, "1.1.0")
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-governed-knowledge.json"),
		newEnabledModuleApplyKnowledgePlanWithLimitsV1(
			t,
			fixture,
			1,
			moduleApplyKnowledgeInstanceIDV1,
			1,
			moduleapi.KnowledgeScopeRuleV1{
				TenantID:     defaultTenantID,
				WorkspaceID:  defaultWorkspaceID,
				AgentID:      defaultAgentID,
				TaskInputRef: "*",
			},
			4,
			4096,
			2,
			2048,
		),
	)
	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || applied.Status != moduleApplyStatusApplied ||
		applied.PointerRevision != 2 {
		t.Fatalf("apply governed Knowledge=%+v error=%v", applied, err)
	}

	message := "How does FreeAgent shared knowledge stay independent from " +
		"Agent and Workspace definitions?"
	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open governed Knowledge composition: %v", err)
	}
	first, chatErr := composition.chat.Chat(
		ctx,
		moduleApplyChatInputV1("e5a-governed-before-backup", message),
	)
	if chatErr != nil || first.TerminalResult == nil || first.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("governed Knowledge Chat=%+v error=%v", first, chatErr)
	}
	firstSnapshot := inspectProductionRAGDispatch(
		t,
		composition.store,
		first.RunID,
		first.TerminalResult.AttemptID,
		"e5a-before-backup",
		message,
	)
	assertW2E5AKnowledgeRequestLimits(
		t,
		composition.store,
		first.TerminalResult.AttemptID,
		message,
		2,
		2048,
	)
	if err := composition.Close(); err != nil {
		t.Fatalf("close governed Knowledge composition: %v", err)
	}

	enabledBundle := filepath.Join(root, "governed-enabled.bundle")
	createAndVerifyModuleApplyBundleV1(
		t,
		databasePath,
		artifactRoot,
		enabledBundle,
	)
	restoredDatabase, restoredArtifacts := restoreModuleApplyBundleV1(
		t,
		enabledBundle,
		filepath.Join(root, "enabled-restored"),
	)
	restored, err := openProductionComposition(
		ctx,
		restoredDatabase,
		restoredArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open restored governed Knowledge composition: %v", err)
	}
	afterRestore, chatErr := restored.chat.Chat(
		ctx,
		moduleApplyChatInputV1("e5a-governed-after-restore", message),
	)
	if chatErr != nil || afterRestore.TerminalResult == nil ||
		afterRestore.FailureCode != "" {
		_ = restored.Close()
		t.Fatalf("restored governed Knowledge Chat=%+v error=%v", afterRestore, chatErr)
	}
	inspectProductionRAGDispatch(
		t,
		restored.store,
		afterRestore.RunID,
		afterRestore.TerminalResult.AttemptID,
		"e5a-after-restore",
		message,
	)
	restoredHistorical := inspectProductionRAGDispatch(
		t,
		restored.store,
		first.RunID,
		first.TerminalResult.AttemptID,
		"e5a-restored-history",
		message,
	)
	assertProductionRAGSnapshotEqual(
		t,
		firstSnapshot,
		restoredHistorical,
		"E5-A enabled backup/restore history",
	)
	if err := restored.Close(); err != nil {
		t.Fatalf("close restored governed Knowledge composition: %v", err)
	}

	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-governed-knowledge.json"),
		newDisabledDeclarativeModuleApplyPlanV1(
			t,
			moduleApplyKnowledgeInstanceIDV1,
			2,
		),
	)
	disabled, err := runModuleApplyFixtureV1(
		restoredDatabase,
		restoredArtifacts,
		disablePath,
		"",
		"",
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied ||
		disabled.PointerRevision != 3 {
		t.Fatalf("disable governed Knowledge=%+v error=%v", disabled, err)
	}
	disabledComposition, err := openProductionComposition(
		ctx,
		restoredDatabase,
		restoredArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open disabled governed Knowledge composition: %v", err)
	}
	afterDisable, chatErr := disabledComposition.chat.Chat(
		ctx,
		moduleApplyChatInputV1("e5a-governed-after-disable", message),
	)
	if chatErr != nil || afterDisable.TerminalResult == nil ||
		afterDisable.FailureCode != "" {
		_ = disabledComposition.Close()
		t.Fatalf("disabled governed Knowledge Chat=%+v error=%v", afterDisable, chatErr)
	}
	assertW2E5ANoKnowledgeCompilation(
		t,
		disabledComposition.store,
		afterDisable.TerminalResult.AttemptID,
	)
	disabledHistorical := inspectProductionRAGDispatch(
		t,
		disabledComposition.store,
		first.RunID,
		first.TerminalResult.AttemptID,
		"e5a-disabled-history",
		message,
	)
	assertProductionRAGSnapshotEqual(
		t,
		firstSnapshot,
		disabledHistorical,
		"E5-A Disable history",
	)
	if err := disabledComposition.Close(); err != nil {
		t.Fatalf("close disabled governed Knowledge composition: %v", err)
	}

	disabledBundle := filepath.Join(root, "governed-disabled.bundle")
	createAndVerifyModuleApplyBundleV1(
		t,
		restoredDatabase,
		restoredArtifacts,
		disabledBundle,
	)
	disabledDatabase, disabledArtifacts := restoreModuleApplyBundleV1(
		t,
		disabledBundle,
		filepath.Join(root, "disabled-restored"),
	)
	reopened, err := openProductionComposition(
		ctx,
		disabledDatabase,
		disabledArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("reopen disabled governed Knowledge backup: %v", err)
	}
	afterDisabledRestore, chatErr := reopened.chat.Chat(
		ctx,
		moduleApplyChatInputV1("e5a-governed-disabled-restored", message),
	)
	if chatErr != nil || afterDisabledRestore.TerminalResult == nil ||
		afterDisabledRestore.FailureCode != "" {
		_ = reopened.Close()
		t.Fatalf(
			"disabled-restored governed Knowledge Chat=%+v error=%v",
			afterDisabledRestore,
			chatErr,
		)
	}
	assertW2E5ANoKnowledgeCompilation(
		t,
		reopened.store,
		afterDisabledRestore.TerminalResult.AttemptID,
	)
	finalHistorical := inspectProductionRAGDispatch(
		t,
		reopened.store,
		first.RunID,
		first.TerminalResult.AttemptID,
		"e5a-disabled-restored-history",
		message,
	)
	assertProductionRAGSnapshotEqual(
		t,
		firstSnapshot,
		finalHistorical,
		"E5-A disabled backup/restore history",
	)
	if err := reopened.Close(); err != nil {
		t.Fatalf("close disabled-restored governed Knowledge composition: %v", err)
	}
}

func assertW2E5AKnowledgeRequestLimits(
	t *testing.T,
	store *currentstore.Store,
	attemptID string,
	queryText string,
	wantMaxHits uint32,
	wantMaxTextBytes uint32,
) {
	t.Helper()
	record, err := store.GetModelDispatchRecord(context.Background(), attemptID)
	if err != nil || record.Attempt.ContextCompilation == nil {
		t.Fatalf("read E5-A Model dispatch=%+v error=%v", record.Attempt, err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		record.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil || len(compilation.KnowledgeRetrievals) != 1 {
		t.Fatalf("restore E5-A Context Compilation=%+v error=%v", compilation, err)
	}
	retrieval := compilation.KnowledgeRetrievals[0]
	_, _, expectedDigest, err := moduleapi.NewKnowledgeContextRequestV1(
		moduleapi.KnowledgeContextRequestV1{
			SchemaVersion:     moduleapi.KnowledgeContextRequestSchemaV1,
			Source:            retrieval.Source,
			Scope:             retrieval.Scope,
			QueryText:         queryText,
			MaxHits:           wantMaxHits,
			MaxTotalTextBytes: wantMaxTextBytes,
		},
	)
	if err != nil || retrieval.RequestDigest != expectedDigest {
		t.Fatalf(
			"E5-A effective grant limits did not narrow request: digest=%s want=%s error=%v",
			retrieval.RequestDigest,
			expectedDigest,
			err,
		)
	}
}

func assertW2E5ANoKnowledgeCompilation(
	t *testing.T,
	store *currentstore.Store,
	attemptID string,
) {
	t.Helper()
	record, err := store.GetModelDispatchRecord(context.Background(), attemptID)
	if err != nil || record.Attempt.ContextCompilation != nil {
		t.Fatalf(
			"future Run retained governed Knowledge after Disable: compilation=%+v error=%v",
			record.Attempt.ContextCompilation,
			err,
		)
	}
}

func newEnabledModuleApplyKnowledgePlanWithLimitsV1(
	t *testing.T,
	fixture moduleApplyKnowledgeFixtureV1,
	expectedPointer uint64,
	instanceID string,
	portIndex uint32,
	rule moduleapi.KnowledgeScopeRuleV1,
	configMaxHits uint32,
	configMaxTextBytes uint32,
	authorityMaxHits uint32,
	authorityMaxTextBytes uint32,
) []byte {
	t.Helper()
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            fixture.Source,
			MaxHits:           configMaxHits,
			MaxTotalTextBytes: configMaxTextBytes,
		},
	)
	if err != nil {
		t.Fatalf("freeze E5-A Knowledge parameters: %v", err)
	}
	_, config, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementUntrustedData,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    json.RawMessage(parameters),
		},
	)
	if err != nil {
		t.Fatalf("freeze E5-A Context config: %v", err)
	}
	_, authority, err := moduleapi.NewKnowledgeAuthorityCeilingV1(
		moduleapi.KnowledgeAuthorityCeilingV1{
			SchemaVersion:     moduleapi.KnowledgeAuthorityCeilingSchemaV1,
			Source:            fixture.Source,
			AllowedScopes:     []moduleapi.KnowledgeScopeRuleV1{rule},
			MaxHits:           authorityMaxHits,
			MaxTotalTextBytes: authorityMaxTextBytes,
		},
	)
	if err != nil {
		t.Fatalf("freeze E5-A Knowledge authority: %v", err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target": moduleApplyProfileBindingTargetTestValue(
			moduleApplyTestProfileID,
		),
		"instance_id": instanceID,
		"port": map[string]any{
			"name":          productionContextPort.Name,
			"exact_version": productionContextPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  fixture.ModuleID,
			"exact_version":       fixture.ExactVersion,
			"artifact_digest":     fixture.ArtifactDigest,
			"artifact_size_bytes": fixture.ArtifactSizeBytes,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestTrustedInProcess),
				"protocol": moduleapi.RuntimeProtocolGoInProcessV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": portIndex,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
	})
}
