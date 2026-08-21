package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const moduleApplyMemoryInstanceIDV1 = "memory-deterministic-module-apply"

type moduleApplyMemoryFixtureV1 struct {
	ModuleID          string
	ExactVersion      string
	ArtifactDirectory string
	ArtifactDigest    string
	ArtifactSizeBytes uint64
}

func TestModuleApplyMemoryDryRunApplyRetryDisableLifecycle(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyMemoryFixtureV1(t)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-memory.json"),
		newEnabledModuleApplyMemoryPlanV1(
			t,
			fixture,
			1,
			defaultAgentID,
			[]string{defaultWorkspaceID},
		),
	)

	before := observeModuleApplyStateV1(t, databasePath, artifactRoot)
	assertModuleApplyMemoryAbsentV1(t, databasePath)
	dryRun, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || dryRun.Status != moduleApplyStatusWouldApply ||
		dryRun.CandidateBasis.PointerRevision != 2 ||
		dryRun.Changes.Binding.Change != moduleDryRunBindingInsertV1 ||
		dryRun.Changes.Binding.PortBindingIndex == nil ||
		*dryRun.Changes.Binding.PortBindingIndex != 1 ||
		dryRun.Changes.Catalog != moduleDryRunCatalogAddInstanceV1 {
		t.Fatalf("Memory dry-run=%+v err=%v", dryRun, err)
	}
	afterDryRun := observeModuleApplyStateV1(t, databasePath, artifactRoot)
	if !reflect.DeepEqual(before, afterDryRun) {
		t.Fatalf("Memory dry-run mutated durable state:\nbefore=%+v\nafter=%+v", before, afterDryRun)
	}
	assertModuleApplyMemoryAbsentV1(t, databasePath)
	if _, err := os.Lstat(filepath.Join(artifactRoot, fixture.ArtifactDigest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Memory dry-run published an artifact: %v", err)
	}

	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || applied.Status != moduleApplyStatusApplied ||
		applied.PointerRevision != 2 || applied.Module == nil ||
		applied.Module.ArtifactDigest != fixture.ArtifactDigest {
		t.Fatalf("Memory apply=%+v err=%v", applied, err)
	}
	genesis := loadModuleApplyMemoryHeadV1(t, databasePath)
	wantGenesis, err := newModuleApplyMemoryGenesisV1(
		defaultTenantID,
		defaultAgentID,
	)
	if err != nil || genesis.SnapshotRef.Revision != 1 ||
		genesis.SourceAttemptID != "" || len(genesis.Snapshot.Entries) != 0 ||
		!bytes.Equal(genesis.CanonicalBytes, wantGenesis) {
		t.Fatalf("Memory genesis=%+v err=%v", genesis, err)
	}

	retried, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || retried.Status != moduleApplyStatusAlreadyApplied ||
		retried.PointerRevision != 2 {
		t.Fatalf("Memory exact retry=%+v err=%v", retried, err)
	}
	afterRetry := loadModuleApplyMemoryHeadV1(t, databasePath)
	assertModuleApplyMemoryHeadEqualV1(t, genesis, afterRetry, "exact retry")

	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-memory.json"),
		newDisabledDeclarativeModuleApplyPlanV1(
			t,
			moduleApplyMemoryInstanceIDV1,
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
		disabled.PointerRevision != 3 {
		t.Fatalf("Memory disable=%+v err=%v", disabled, err)
	}
	afterDisable := loadModuleApplyMemoryHeadV1(t, databasePath)
	assertModuleApplyMemoryHeadEqualV1(t, genesis, afterDisable, "disable")
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		context.Background(),
		filepath.Join(artifactRoot, fixture.ArtifactDigest),
		moduleapi.ArtifactMetadataPaths{},
		fixture.ArtifactDigest,
		fixture.ArtifactSizeBytes,
	); err != nil {
		t.Fatalf("Memory disable removed historical artifact: %v", err)
	}
	_, control, catalog := loadModuleApplyPublishedStateV1(t, databasePath)
	profile, found := control.FindProfile(moduleApplyTestProfileID)
	if !found {
		t.Fatal("Memory disable removed target Profile")
	}
	for _, binding := range profile.Bindings {
		if binding.InstanceID == moduleApplyMemoryInstanceIDV1 {
			t.Fatalf("Memory disable retained future Binding: %+v", binding)
		}
	}
	if _, found := catalog.FindInstance(moduleApplyMemoryInstanceIDV1); found {
		t.Fatal("Memory disable retained unreferenced current Catalog instance")
	}
}

func TestModuleApplyMemoryPreservesExistingHeadByteForByte(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	seeded := seedNonEmptyModuleApplyMemoryGenesisV1(t, databasePath)
	fixture := newModuleApplyMemoryFixtureV1(t)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-memory.json"),
		newEnabledModuleApplyMemoryPlanV1(
			t,
			fixture,
			1,
			defaultAgentID,
			[]string{defaultWorkspaceID},
		),
	)
	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || applied.Status != moduleApplyStatusApplied {
		t.Fatalf("Memory apply over existing head=%+v err=%v", applied, err)
	}
	after := loadModuleApplyMemoryHeadV1(t, databasePath)
	assertModuleApplyMemoryHeadEqualV1(t, seeded, after, "existing head apply")
}

func TestModuleApplyMemoryRejectsSecondProfileBindingWithoutWrites(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyMemoryFixtureV1(t)
	firstPlanPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-first-memory.json"),
		newEnabledModuleApplyMemoryPlanV1(
			t,
			fixture,
			1,
			defaultAgentID,
			[]string{defaultWorkspaceID},
		),
	)
	if result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		firstPlanPath,
		fixture.ArtifactDirectory,
		"",
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("apply first Memory Binding=%+v err=%v", result, err)
	}
	secondPlanPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-second-memory.json"),
		newEnabledModuleApplyMemoryPlanForInstanceV1(
			t,
			fixture,
			2,
			"memory-deterministic-module-apply-second",
			2,
			defaultAgentID,
			[]string{defaultWorkspaceID},
		),
	)

	databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
	artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
	sourceBefore := snapshotModuleDryRunTreeV1(t, fixture.ArtifactDirectory)
	_, dryRunErr := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		secondPlanPath,
		fixture.ArtifactDirectory,
		"",
	)
	if dryRunErr == nil || dryRunErr.Error() !=
		"freeagent module-dry-run: failed (TARGET_CONFLICT)" {
		t.Fatalf("second Memory dry-run error=%v", dryRunErr)
	}
	assertModuleDryRunInputsUnchangedV1(
		t,
		databasePath,
		artifactRoot,
		fixture.ArtifactDirectory,
		databaseBefore,
		artifactsBefore,
		sourceBefore,
	)

	_, applyErr := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		secondPlanPath,
		fixture.ArtifactDirectory,
		"",
	)
	if applyErr == nil || applyErr.Error() !=
		"freeagent module-apply: failed (TARGET_CONFLICT)" {
		t.Fatalf("second Memory apply error=%v", applyErr)
	}
	assertModuleDryRunInputsUnchangedV1(
		t,
		databasePath,
		artifactRoot,
		fixture.ArtifactDirectory,
		databaseBefore,
		artifactsBefore,
		sourceBefore,
	)
	head := loadModuleApplyMemoryHeadV1(t, databasePath)
	wantGenesis, freezeErr := newModuleApplyMemoryGenesisV1(
		defaultTenantID,
		defaultAgentID,
	)
	if freezeErr != nil || !bytes.Equal(head.CanonicalBytes, wantGenesis) {
		t.Fatalf("rejected second binding changed Memory head=%+v err=%v", head, freezeErr)
	}
	assertNoModuleApplyStageResidueV1(t, root)
}

func TestModuleApplyMemoryPostCASGenesisFailureFailsClosedAndRetryHeals(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyMemoryFixtureV1(t)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-memory.json"),
		newEnabledModuleApplyMemoryPlanV1(
			t,
			fixture,
			1,
			defaultAgentID,
			[]string{defaultWorkspaceID},
		),
	)
	wantGenesis, freezeErr := newModuleApplyMemoryGenesisV1(
		defaultTenantID,
		defaultAgentID,
	)
	if freezeErr != nil {
		t.Fatalf("freeze expected Memory genesis: %v", freezeErr)
	}
	genesisContentRef, digestErr := currentstore.ComputeContentDigest(
		currentstore.ContentMemorySnapshot,
		"application/json",
		wantGenesis,
	)
	if digestErr != nil {
		t.Fatalf("compute expected Memory genesis ContentRef: %v", digestErr)
	}
	collisionCanonical := []byte(`{"collision":true}`)
	withModuleApplyFaultDatabaseV1(t, databasePath, func(database *sql.DB) {
		if _, err := database.Exec(`
			INSERT INTO content_records(
				content_digest, kind, media_type, canonical_bytes,
				size_bytes, created_at
			) VALUES(?, 'TASK_INPUT', 'application/json', ?, ?, 1)
		`, genesisContentRef, collisionCanonical, len(collisionCanonical)); err != nil {
			t.Fatalf("install Memory genesis content collision: %v", err)
		}
	})

	result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err == nil || result.Status != "" ||
		!strings.Contains(err.Error(), "(APPLY_OUTCOME_UNKNOWN)") {
		t.Fatalf("post-CAS/pre-genesis apply=%+v err=%v", result, err)
	}
	basis, _, catalog := loadModuleApplyPublishedStateV1(t, databasePath)
	if basis.PointerRevision != 2 {
		t.Fatalf("injected post-CAS basis=%+v", basis)
	}
	if _, found := catalog.FindInstance(moduleApplyMemoryInstanceIDV1); !found {
		t.Fatal("injected failure did not reach the post-CAS window")
	}
	assertModuleApplyMemoryAbsentV1(t, databasePath)
	// Remove only the injected unreferenced collision. The exact current
	// Catalog remains enabled and the missing-head window remains observable.
	execCmdClosedFileTamperV1(
		t,
		databasePath,
		[]string{"content_records_reject_delete"},
		`DELETE FROM content_records WHERE content_digest=?`,
		genesisContentRef,
	)

	composition, openErr := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if openErr != nil {
		t.Fatalf("open composition in post-CAS window: %v", openErr)
	}
	beforeAttempts := countModuleApplyRowsV1(t, databasePath, "model_dispatch_attempts")
	chatResult, chatErr := composition.chat.Chat(
		context.Background(),
		moduleApplyChatInputV1(
			"module-apply-memory-missing-head",
			"This must not reach the model without a Memory head.",
		),
	)
	closeErr := composition.Close()
	if chatErr == nil || !chatResult.AdmissionCreated || closeErr != nil {
		t.Fatalf(
			"missing-head chat did not fail closed: result=%+v errors=%v",
			chatResult,
			errors.Join(chatErr, closeErr),
		)
	}
	afterAttempts := countModuleApplyRowsV1(t, databasePath, "model_dispatch_attempts")
	if afterAttempts != beforeAttempts {
		t.Fatalf("missing-head chat dispatched model: before=%d after=%d", beforeAttempts, afterAttempts)
	}

	healed, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || healed.Status != moduleApplyStatusAlreadyApplied ||
		healed.PointerRevision != 2 {
		t.Fatalf("Memory exact retry heal=%+v err=%v", healed, err)
	}
	head := loadModuleApplyMemoryHeadV1(t, databasePath)
	if !bytes.Equal(head.CanonicalBytes, wantGenesis) {
		t.Fatalf("healed Memory genesis=%+v", head)
	}
}

func TestModuleApplyMemoryCASFailureCreatesNoGenesis(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyMemoryFixtureV1(t)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-memory.json"),
		newEnabledModuleApplyMemoryPlanV1(
			t,
			fixture,
			1,
			defaultAgentID,
			[]string{defaultWorkspaceID},
		),
	)
	_, _, planDigest, readErr := readModuleApplyPlanV1(planPath)
	if readErr != nil {
		t.Fatalf("read Memory apply plan: %v", readErr)
	}
	_, control, _ := loadModuleApplyPublishedStateV1(t, databasePath)
	collisionSnapshotID := moduleApplyControlIDPrefixV1 + planDigest
	fakeDigest := moduleapi.Digest(
		"freeagent.test.module-apply-memory-control-collision/v1",
		[]byte(t.Name()),
	)
	withModuleApplyFaultDatabaseV1(t, databasePath, func(database *sql.DB) {
		if _, err := database.Exec(`
			INSERT INTO control_snapshots(
				snapshot_id, tenant_id, revision, canonical_json,
				digest, published_at
			) VALUES(?, ?, ?, ?, ?, 1)
		`, collisionSnapshotID, defaultTenantID, control.Revision+1, []byte(`{}`), fakeDigest); err != nil {
			t.Fatalf("install pre-CAS Control collision: %v", err)
		}
	})
	_, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "(PUBLICATION_FAILED)") {
		t.Fatalf("Catalog CAS failure error=%v", err)
	}
	basis, _, catalog := loadModuleApplyPublishedStateV1(t, databasePath)
	if basis.PointerRevision != 1 {
		t.Fatalf("failed Catalog CAS moved pointer: %+v", basis)
	}
	if _, found := catalog.FindInstance(moduleApplyMemoryInstanceIDV1); found {
		t.Fatal("failed Catalog CAS made Memory reachable")
	}
	assertModuleApplyMemoryAbsentV1(t, databasePath)
}

func TestModuleApplyMemoryRejectsUnknownScopeAndAdapterMismatchWithoutWrites(
	t *testing.T,
) {
	tests := []struct {
		name       string
		agentID    string
		workspaces []string
		mutate     func(*testing.T, moduleApplyMemoryFixtureV1) moduleApplyMemoryFixtureV1
		wantCode   moduleApplyFailureCodeV1
	}{
		{
			name:       "unknown Agent",
			agentID:    "unknown-agent",
			workspaces: []string{defaultWorkspaceID},
			wantCode:   moduleApplyFailureTarget,
		},
		{
			name:       "unknown Workspace",
			agentID:    defaultAgentID,
			workspaces: []string{"unknown-workspace"},
			wantCode:   moduleApplyFailureTarget,
		},
		{
			name:       "artifact adapter source mismatch",
			agentID:    defaultAgentID,
			workspaces: []string{defaultWorkspaceID},
			mutate:     mismatchedModuleApplyMemoryFixtureV1,
			wantCode:   moduleApplyFailureArtifact,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			fixture := newModuleApplyMemoryFixtureV1(t)
			if test.mutate != nil {
				fixture = test.mutate(t, fixture)
			}
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "rejected-memory.json"),
				newEnabledModuleApplyMemoryPlanV1(
					t,
					fixture,
					1,
					test.agentID,
					test.workspaces,
				),
			)
			before := observeModuleApplyStateV1(t, databasePath, artifactRoot)
			_, err := runModuleApplyFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			)
			if err == nil || !strings.Contains(
				err.Error(),
				"("+string(test.wantCode)+")",
			) {
				t.Fatalf("rejected Memory apply error=%v want=%s", err, test.wantCode)
			}
			after := observeModuleApplyStateV1(t, databasePath, artifactRoot)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected Memory apply mutated state:\nbefore=%+v\nafter=%+v", before, after)
			}
			assertModuleApplyMemoryAbsentV1(t, databasePath)
			assertNoModuleApplyStageResidueV1(t, root)
		})
	}
}

func newModuleApplyMemoryFixtureV1(t *testing.T) moduleApplyMemoryFixtureV1 {
	t.Helper()
	directory := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"bootstrap-artifacts",
		"freeagent.example.memory.deterministic",
		"1.0.0",
	)
	return moduleApplyMemoryFixtureFromDirectoryV1(t, directory)
}

func moduleApplyMemoryFixtureFromDirectoryV1(
	t *testing.T,
	directory string,
) moduleApplyMemoryFixtureV1 {
	t.Helper()
	manifestPayload, err := os.ReadFile(filepath.Join(directory, moduleapi.ArtifactManifestPath))
	if err != nil {
		t.Fatalf("read Memory manifest: %v", err)
	}
	manifest, manifestCanonical, err := moduleapi.ParseModuleManifestV1(manifestPayload)
	if err != nil {
		t.Fatalf("parse Memory manifest: %v", err)
	}
	files, err := moduleapi.ScanArtifactDirectory(directory, moduleapi.ArtifactMetadataPaths{})
	if err != nil {
		t.Fatalf("scan Memory artifact: %v", err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		t.Fatalf("compute Memory artifact digest: %v", err)
	}
	size := uint64(len(manifestCanonical))
	for _, file := range files {
		size += uint64(len(file.Content))
	}
	return moduleApplyMemoryFixtureV1{
		ModuleID:          manifest.ID,
		ExactVersion:      manifest.Version,
		ArtifactDirectory: directory,
		ArtifactDigest:    digest,
		ArtifactSizeBytes: size,
	}
}

func mismatchedModuleApplyMemoryFixtureV1(
	t *testing.T,
	fixture moduleApplyMemoryFixtureV1,
) moduleApplyMemoryFixtureV1 {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "memory-artifact")
	copyModuleApplyTestTreeV1(t, fixture.ArtifactDirectory, directory)
	if err := os.WriteFile(
		filepath.Join(directory, "content", "adapter.json"),
		[]byte(`{"adapter_identity":"freeagent.adapter.memory.other/v1","schema_version":"memory-adapter-descriptor/v1"}`),
		0o600,
	); err != nil {
		t.Fatalf("write mismatched Memory descriptor: %v", err)
	}
	return moduleApplyMemoryFixtureFromDirectoryV1(t, directory)
}

func newEnabledModuleApplyMemoryPlanV1(
	t *testing.T,
	fixture moduleApplyMemoryFixtureV1,
	expectedPointer uint64,
	agentID string,
	allowedWorkspaceIDs []string,
) []byte {
	return newEnabledModuleApplyMemoryPlanForInstanceV1(
		t,
		fixture,
		expectedPointer,
		moduleApplyMemoryInstanceIDV1,
		1,
		agentID,
		allowedWorkspaceIDs,
	)
}

func newEnabledModuleApplyMemoryPlanForInstanceV1(
	t *testing.T,
	fixture moduleApplyMemoryFixtureV1,
	expectedPointer uint64,
	instanceID string,
	portBindingIndex uint32,
	agentID string,
	allowedWorkspaceIDs []string,
) []byte {
	t.Helper()
	kinds := []moduleapi.MemoryEntryKindV1{
		moduleapi.MemoryEntryTaskSummary,
		moduleapi.MemoryEntryCategoryCount,
		moduleapi.MemoryEntryRepeatedTermCount,
	}
	_, parameters, _, err := moduleapi.NewMemoryContextBindingV1(
		moduleapi.MemoryContextBindingV1{
			SchemaVersion:     moduleapi.MemoryContextBindingSchemaV1,
			Kinds:             kinds,
			MaxItems:          16,
			MaxTotalTextBytes: 8192,
			CategoryRules: []moduleapi.MemoryCategoryRuleV1{{
				Key: "programming", Terms: []string{"code", "go"},
			}},
			StopTerms:           []string{"the"},
			SummaryMaxTextBytes: 256,
			EntryTTLSeconds:     86400,
		},
	)
	if err != nil {
		t.Fatalf("freeze Memory binding: %v", err)
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
		t.Fatalf("freeze Memory Context config: %v", err)
	}
	_, authority, err := moduleapi.NewMemoryAuthorityCeilingV1(
		moduleapi.MemoryAuthorityCeilingV1{
			SchemaVersion:       moduleapi.MemoryAuthorityCeilingSchemaV1,
			TenantID:            defaultTenantID,
			AgentID:             agentID,
			AllowedWorkspaceIDs: allowedWorkspaceIDs,
			AllowedKinds:        kinds,
			MaxItems:            32,
			MaxTotalTextBytes:   16384,
		},
	)
	if err != nil {
		t.Fatalf("freeze Memory authority: %v", err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(moduleApplyTestProfileID),
		"instance_id":               instanceID,
		"port": map[string]any{
			"name": productionContextPort.Name, "exact_version": productionContextPort.ExactVersion,
		},
		"module": map[string]any{
			"id": fixture.ModuleID, "exact_version": fixture.ExactVersion,
			"artifact_digest": fixture.ArtifactDigest, "artifact_size_bytes": fixture.ArtifactSizeBytes,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestTrustedInProcess),
				"protocol": moduleapi.RuntimeProtocolGoInProcessV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": portBindingIndex,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
	})
}

func seedNonEmptyModuleApplyMemoryGenesisV1(
	t *testing.T,
	databasePath string,
) currentstore.AgentMemoryRevisionRecord {
	t.Helper()
	entry, _, err := moduleapi.NewMemoryEntryV1(moduleapi.MemoryEntryV1{
		EntryID:             "operator-seeded-fact",
		Kind:                moduleapi.MemoryEntryFact,
		Key:                 "preferred-language",
		Text:                "Go",
		VisibleWorkspaceIDs: []string{defaultWorkspaceID},
		SourceRefs: []string{moduleapi.Digest(
			"freeagent.test.module-apply-memory-source/v1",
			[]byte(t.Name()),
		)},
		CreatedAtUnixMS: 1,
	})
	if err != nil {
		t.Fatalf("freeze seeded Memory entry: %v", err)
	}
	_, canonical, err := moduleapi.NewAgentMemorySnapshotV1(
		moduleapi.AgentMemorySnapshotV1{
			SchemaVersion: moduleapi.MemorySnapshotSchemaV1,
			TenantID:      defaultTenantID,
			AgentID:       defaultAgentID,
			Revision:      1,
			Entries:       []moduleapi.MemoryEntryV1{entry},
		},
	)
	if err != nil {
		t.Fatalf("freeze seeded Memory genesis: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	put, putErr := store.PutAgentMemoryGenesis(context.Background(), canonical)
	closeErr := store.Close()
	if putErr != nil || closeErr != nil || !put.Applied {
		t.Fatalf("seed Memory genesis: result=%+v err=%v", put, errors.Join(putErr, closeErr))
	}
	return put.Record
}

func loadModuleApplyMemoryHeadV1(
	t *testing.T,
	databasePath string,
) currentstore.AgentMemoryRevisionRecord {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	head, readErr := store.GetCurrentAgentMemory(
		context.Background(),
		defaultTenantID,
		defaultAgentID,
	)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("load Memory head: %v", errors.Join(readErr, closeErr))
	}
	return head
}

func assertModuleApplyMemoryAbsentV1(t *testing.T, databasePath string) {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := store.GetCurrentAgentMemory(
		context.Background(),
		defaultTenantID,
		defaultAgentID,
	)
	closeErr := store.Close()
	if !errors.Is(readErr, currentstore.ErrAgentMemoryNotFound) || closeErr != nil {
		t.Fatalf("Agent Memory unexpectedly exists: %v", errors.Join(readErr, closeErr))
	}
}

func assertModuleApplyMemoryHeadEqualV1(
	t *testing.T,
	want currentstore.AgentMemoryRevisionRecord,
	got currentstore.AgentMemoryRevisionRecord,
	phase string,
) {
	t.Helper()
	if want.SnapshotRef != got.SnapshotRef ||
		want.SourceAttemptID != got.SourceAttemptID ||
		!bytes.Equal(want.CanonicalBytes, got.CanonicalBytes) ||
		!want.CreatedAt.Equal(got.CreatedAt) {
		t.Fatalf("%s rewrote Memory head:\nwant=%+v\ngot=%+v", phase, want, got)
	}
}

func countModuleApplyRowsV1(t *testing.T, databasePath string, table string) int64 {
	t.Helper()
	var count int64
	withModuleApplyFaultDatabaseV1(t, databasePath, func(database *sql.DB) {
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	})
	return count
}
