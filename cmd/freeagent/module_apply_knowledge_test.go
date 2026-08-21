package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const moduleApplyKnowledgeInstanceIDV1 = "knowledge-shared-module-apply"

type moduleApplyKnowledgeFixtureV1 struct {
	ModuleID          string
	ExactVersion      string
	ArtifactDirectory string
	ArtifactDigest    string
	ArtifactSizeBytes uint64
	Source            moduleapi.KnowledgeSourceRefV1
}

func TestModuleApplyKnowledgeDryRunApplyAndProductionChat(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	fixture := newModuleApplyKnowledgeFixtureV1(t)
	planCanonical := newEnabledModuleApplyKnowledgePlanV1(
		t,
		fixture,
		1,
		moduleapi.KnowledgeScopeRuleV1{
			TenantID:     defaultTenantID,
			WorkspaceID:  defaultWorkspaceID,
			AgentID:      defaultAgentID,
			TaskInputRef: "*",
		},
	)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-knowledge.json"),
		planCanonical,
	)

	dryRun, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil {
		t.Fatalf("dry-run Knowledge module: %v", err)
	}
	if dryRun.SchemaVersion != moduleDryRunResultSchemaV1 ||
		dryRun.Status != moduleApplyStatusWouldApply ||
		dryRun.DesiredState != moduleApplyEnabledV1 ||
		dryRun.TenantID != defaultTenantID ||
		dryRun.BindingTarget.ProfileID != moduleApplyTestProfileID ||
		dryRun.InstanceID != moduleApplyKnowledgeInstanceIDV1 ||
		dryRun.Port != productionContextPort ||
		dryRun.ObservedBasis.PointerRevision != 1 ||
		dryRun.CandidateBasis.PointerRevision != 2 ||
		dryRun.CandidateBasis.ProjectionStatus !=
			moduleDryRunProjectedNotReservedV1 ||
		dryRun.Changes.Installation != moduleDryRunInstallationCreateV1 ||
		dryRun.Changes.Activation != moduleDryRunActivationCreateV1 ||
		dryRun.Changes.Binding.Change != moduleDryRunBindingInsertV1 ||
		dryRun.Changes.Catalog != moduleDryRunCatalogAddInstanceV1 ||
		dryRun.Changes.Binding.PortBindingIndex == nil ||
		*dryRun.Changes.Binding.PortBindingIndex != 1 ||
		len(dryRun.Changes.Binding.StaticContextRefs) != 0 ||
		dryRun.Module == nil ||
		dryRun.Module.ID != fixture.ModuleID ||
		dryRun.Module.ExactVersion != fixture.ExactVersion ||
		dryRun.Module.ArtifactDigest != fixture.ArtifactDigest {
		t.Fatalf("Knowledge dry-run result=%+v", dryRun)
	}

	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil {
		t.Fatalf("apply Knowledge module: %v", err)
	}
	if applied.SchemaVersion != moduleApplyResultSchemaV1 ||
		applied.Status != moduleApplyStatusApplied ||
		applied.DesiredState != moduleApplyEnabledV1 ||
		applied.PointerRevision != 2 ||
		applied.ControlSnapshotID != dryRun.CandidateBasis.Control.SnapshotID ||
		applied.CatalogGenerationID !=
			dryRun.CandidateBasis.Catalog.GenerationID ||
		applied.PlanDigest != dryRun.PlanDigest ||
		applied.Module == nil ||
		applied.Module.ID != fixture.ModuleID ||
		applied.Module.ExactVersion != fixture.ExactVersion ||
		applied.Module.ArtifactDigest != fixture.ArtifactDigest {
		t.Fatalf("Knowledge apply result=%+v dry-run=%+v", applied, dryRun)
	}
	retried, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || retried.Status != moduleApplyStatusAlreadyApplied ||
		retried.PointerRevision != 2 ||
		retried.ControlSnapshotID != applied.ControlSnapshotID ||
		retried.CatalogGenerationID != applied.CatalogGenerationID ||
		retried.PlanDigest != applied.PlanDigest {
		t.Fatalf("exact Knowledge retry=%+v err=%v", retried, err)
	}
	noChangePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-knowledge-no-change.json"),
		newEnabledModuleApplyKnowledgePlanV1(
			t,
			fixture,
			2,
			moduleapi.KnowledgeScopeRuleV1{
				TenantID:     defaultTenantID,
				WorkspaceID:  defaultWorkspaceID,
				AgentID:      defaultAgentID,
				TaskInputRef: "*",
			},
		),
	)
	noChange, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		noChangePath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || noChange.Status != moduleApplyStatusNoChange ||
		noChange.PointerRevision != 2 ||
		noChange.ControlSnapshotID != applied.ControlSnapshotID ||
		noChange.CatalogGenerationID != applied.CatalogGenerationID ||
		noChange.PlanDigest == applied.PlanDigest {
		t.Fatalf("equivalent Knowledge apply=%+v err=%v", noChange, err)
	}

	basis, control, catalog := loadModuleApplyPublishedStateV1(t, databasePath)
	if basis.TenantID != dryRun.CandidateBasis.TenantID ||
		basis.PointerRevision != dryRun.CandidateBasis.PointerRevision ||
		basis.Control != dryRun.CandidateBasis.Control ||
		basis.Catalog != dryRun.CandidateBasis.Catalog {
		t.Fatalf("applied Basis=%+v projected=%+v", basis, dryRun.CandidateBasis)
	}
	profile, found := findModuleApplyProfileV1(control, moduleApplyTestProfileID)
	if !found {
		t.Fatal("Pure Chat Profile is absent after Knowledge apply")
	}
	var knowledgeBindingFound bool
	for _, binding := range profile.Bindings {
		if binding.Port != productionContextPort ||
			binding.InstanceID != moduleApplyKnowledgeInstanceIDV1 {
			continue
		}
		knowledgeBindingFound = true
		if binding.FailurePolicy != moduleapi.FailureRequired ||
			binding.ConfigRef == "" || binding.AuthorityCeilingRef == "" ||
			len(binding.StaticContextRefs) != 0 {
			t.Fatalf("Knowledge Binding=%+v", binding)
		}
	}
	if !knowledgeBindingFound {
		t.Fatal("Knowledge Binding is absent after apply")
	}
	entry, found := catalog.FindInstance(moduleApplyKnowledgeInstanceIDV1)
	if !found || entry.Activation.ModuleID != fixture.ModuleID ||
		entry.Activation.Version != fixture.ExactVersion ||
		entry.Activation.ArtifactDigest != fixture.ArtifactDigest ||
		entry.Activation.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		entry.Activation.AdapterIdentity != localKnowledgeAdapterID ||
		len(entry.Provides) != 1 || entry.Provides[0] != productionContextPort {
		t.Fatalf("Knowledge Catalog entry=%+v found=%v", entry, found)
	}

	message := "How does FreeAgent shared knowledge stay independent from " +
		"Agent and Workspace definitions?"
	ctx := context.Background()
	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open production composition after Knowledge apply: %v", err)
	}
	input := moduleApplyChatInputV1("module-apply-knowledge-chat", message)
	result, chatErr := composition.chat.Chat(ctx, input)
	if chatErr != nil || !result.AdmissionCreated ||
		result.TerminalResult == nil || result.Reply != message ||
		result.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("production Knowledge chat=%+v err=%v", result, chatErr)
	}
	enabledSnapshot := inspectProductionRAGDispatch(
		t,
		composition.store,
		result.RunID,
		result.TerminalResult.AttemptID,
		"module-apply",
		message,
	)
	if err := composition.Close(); err != nil {
		t.Fatalf("close production Knowledge composition: %v", err)
	}

	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-knowledge.json"),
		newDisabledDeclarativeModuleApplyPlanV1(
			t,
			moduleApplyKnowledgeInstanceIDV1,
			2,
		),
	)
	disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePath,
		"",
		"",
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied ||
		disabled.DesiredState != moduleApplyDisabledV1 ||
		disabled.PointerRevision != 3 || disabled.Module != nil {
		t.Fatalf("disable Knowledge binding=%+v err=%v", disabled, err)
	}

	afterDisable, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open production composition after Knowledge disable: %v", err)
	}
	afterInput := moduleApplyChatInputV1(
		"module-apply-knowledge-after-disable",
		message,
	)
	afterResult, chatErr := afterDisable.chat.Chat(ctx, afterInput)
	if chatErr != nil || !afterResult.AdmissionCreated ||
		afterResult.TerminalResult == nil || afterResult.Reply != message ||
		afterResult.FailureCode != "" || afterResult.RunID == result.RunID {
		_ = afterDisable.Close()
		t.Fatalf("production chat after Knowledge disable=%+v err=%v", afterResult, chatErr)
	}
	afterRecord, readErr := afterDisable.store.GetModelDispatchRecord(
		ctx,
		afterResult.TerminalResult.AttemptID,
	)
	if readErr != nil || afterRecord.Attempt.ContextCompilation != nil {
		_ = afterDisable.Close()
		t.Fatalf(
			"new Run after Knowledge disable has retrieval evidence=%+v err=%v",
			afterRecord.Attempt.ContextCompilation,
			readErr,
		)
	}
	historicalSnapshot := inspectProductionRAGDispatch(
		t,
		afterDisable.store,
		result.RunID,
		result.TerminalResult.AttemptID,
		"module-apply-disabled-history",
		message,
	)
	assertProductionRAGSnapshotEqual(
		t,
		enabledSnapshot,
		historicalSnapshot,
		"Knowledge disable historical Run",
	)
	if err := afterDisable.Close(); err != nil {
		t.Fatalf("close production composition after Knowledge disable: %v", err)
	}
}

func TestModuleApplyKnowledgeRejectsSourceAndTargetConflictsWithoutWrites(
	t *testing.T,
) {
	fixture := newModuleApplyKnowledgeFixtureV1(t)
	foreignSource := fixture.Source
	foreignSource.ID += ".foreign"
	foreignSource.Digest = strings.Repeat("a", moduleapi.SHA256HexLength)
	validRule := moduleapi.KnowledgeScopeRuleV1{
		TenantID:     defaultTenantID,
		WorkspaceID:  defaultWorkspaceID,
		AgentID:      defaultAgentID,
		TaskInputRef: "*",
	}
	tests := []struct {
		name            string
		configSource    moduleapi.KnowledgeSourceRefV1
		authoritySource moduleapi.KnowledgeSourceRefV1
		rule            moduleapi.KnowledgeScopeRuleV1
		portIndex       uint32
		failureCode     moduleApplyFailureCodeV1
	}{
		{
			name:            "config_authority_source_mismatch",
			configSource:    fixture.Source,
			authoritySource: foreignSource,
			rule:            validRule,
			portIndex:       1,
			failureCode:     moduleApplyFailurePlanInvalid,
		},
		{
			name:            "artifact_source_mismatch",
			configSource:    foreignSource,
			authoritySource: foreignSource,
			rule:            validRule,
			portIndex:       1,
			failureCode:     moduleApplyFailureArtifact,
		},
		{
			name:            "unknown_workspace",
			configSource:    fixture.Source,
			authoritySource: fixture.Source,
			rule: moduleapi.KnowledgeScopeRuleV1{
				TenantID:     defaultTenantID,
				WorkspaceID:  "unknown-workspace",
				AgentID:      "*",
				TaskInputRef: "*",
			},
			portIndex:   1,
			failureCode: moduleApplyFailureTarget,
		},
		{
			name:            "unknown_agent",
			configSource:    fixture.Source,
			authoritySource: fixture.Source,
			rule: moduleapi.KnowledgeScopeRuleV1{
				TenantID:     defaultTenantID,
				WorkspaceID:  "*",
				AgentID:      "unknown-agent",
				TaskInputRef: "*",
			},
			portIndex:   1,
			failureCode: moduleApplyFailureTarget,
		},
		{
			name:            "untrusted_before_trusted_context",
			configSource:    fixture.Source,
			authoritySource: fixture.Source,
			rule:            validRule,
			portIndex:       0,
			failureCode:     moduleApplyFailureTarget,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			before := observeModuleApplyStateV1(t, databasePath, artifactRoot)
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "rejected-knowledge.json"),
				newEnabledModuleApplyKnowledgePlanWithPolicyV1(
					t,
					fixture,
					1,
					test.configSource,
					test.authoritySource,
					test.rule,
					test.portIndex,
				),
			)
			_, err := runModuleApplyFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			)
			if err == nil || !strings.Contains(
				err.Error(),
				"("+string(test.failureCode)+")",
			) {
				t.Fatalf("rejected Knowledge apply error=%v want=%s", err, test.failureCode)
			}
			after := observeModuleApplyStateV1(t, databasePath, artifactRoot)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected Knowledge apply mutated state:\nbefore=%+v\nafter=%+v", before, after)
			}
			assertNoModuleApplyStageResidueV1(t, root)
		})
	}
}

func newModuleApplyKnowledgeFixtureV1(
	t *testing.T,
) moduleApplyKnowledgeFixtureV1 {
	t.Helper()
	return newModuleApplyKnowledgeFixtureVersionV1(t, "1.0.0")
}

func newModuleApplyKnowledgeFixtureVersionV1(
	t *testing.T,
	exactVersion string,
) moduleApplyKnowledgeFixtureV1 {
	t.Helper()
	directory := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"bootstrap-artifacts",
		"freeagent.example.knowledge.shared",
		exactVersion,
	)
	manifestPayload, err := os.ReadFile(filepath.Join(
		directory,
		moduleapi.ArtifactManifestPath,
	))
	if err != nil {
		t.Fatalf("read Knowledge manifest: %v", err)
	}
	manifest, manifestCanonical, err := moduleapi.ParseModuleManifestV1(
		manifestPayload,
	)
	if err != nil {
		t.Fatalf("parse Knowledge manifest: %v", err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		directory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatalf("scan Knowledge artifact: %v", err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		t.Fatalf("compute Knowledge artifact digest: %v", err)
	}
	size := uint64(len(manifestCanonical))
	for _, file := range files {
		size += uint64(len(file.Content))
	}
	sourceCanonical, err := os.ReadFile(filepath.Join(
		directory,
		filepath.FromSlash(manifest.Runtime.Entrypoint),
	))
	if err != nil {
		t.Fatalf("read Knowledge source: %v", err)
	}
	_, sourceRef, err := moduleapi.RestoreKnowledgeSourceV1(sourceCanonical)
	if err != nil {
		t.Fatalf("restore Knowledge source: %v", err)
	}
	return moduleApplyKnowledgeFixtureV1{
		ModuleID:          manifest.ID,
		ExactVersion:      manifest.Version,
		ArtifactDirectory: directory,
		ArtifactDigest:    digest,
		ArtifactSizeBytes: size,
		Source:            sourceRef,
	}
}

func newEnabledModuleApplyKnowledgePlanV1(
	t *testing.T,
	fixture moduleApplyKnowledgeFixtureV1,
	expectedPointer uint64,
	rule moduleapi.KnowledgeScopeRuleV1,
) []byte {
	t.Helper()
	return newEnabledModuleApplyKnowledgePlanWithPolicyV1(
		t,
		fixture,
		expectedPointer,
		fixture.Source,
		fixture.Source,
		rule,
		1,
	)
}

func newEnabledModuleApplyKnowledgePlanWithPolicyV1(
	t *testing.T,
	fixture moduleApplyKnowledgeFixtureV1,
	expectedPointer uint64,
	configSource moduleapi.KnowledgeSourceRefV1,
	authoritySource moduleapi.KnowledgeSourceRefV1,
	rule moduleapi.KnowledgeScopeRuleV1,
	portIndex uint32,
) []byte {
	t.Helper()
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            configSource,
			MaxHits:           4,
			MaxTotalTextBytes: 4096,
		},
	)
	if err != nil {
		t.Fatalf("freeze Knowledge binding parameters: %v", err)
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
		t.Fatalf("freeze Knowledge Context config: %v", err)
	}
	_, authority, err := moduleapi.NewKnowledgeAuthorityCeilingV1(
		moduleapi.KnowledgeAuthorityCeilingV1{
			SchemaVersion:     moduleapi.KnowledgeAuthorityCeilingSchemaV1,
			Source:            authoritySource,
			AllowedScopes:     []moduleapi.KnowledgeScopeRuleV1{rule},
			MaxHits:           8,
			MaxTotalTextBytes: 8192,
		},
	)
	if err != nil {
		t.Fatalf("freeze Knowledge authority: %v", err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(moduleApplyTestProfileID),
		"instance_id":               moduleApplyKnowledgeInstanceIDV1,
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
