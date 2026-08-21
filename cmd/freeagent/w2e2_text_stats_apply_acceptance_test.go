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
	"unicode/utf8"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const w2e2TextStatsInstanceV1 = "w2e2-text-stats"

type w2e2RuntimeCountsV1 struct {
	Runs             int64
	ModelAttempts    int64
	DispatchAttempts int64
}

type w2e2ActionChainEvidenceV1 struct {
	ModelOne currentstore.ModelDispatchRecord
	Action   currentstore.ActionDispatchRecord
	ModelTwo currentstore.ModelDispatchRecord
}

// TestW2E2TextStatsApplyAcceptanceV1 is the network-free W2-E2 acceptance
// proof. It starts from the existing Pure Chat seed and the official unpacked
// text.stats package, never from the Action bootstrap seed or an Action seed.
// The only installation path is the production module-dry-run/module-apply
// CLI surface with one exact Operator-owned TRUSTED_IN_PROCESS artifact grant.
func TestW2E2TextStatsApplyAcceptanceV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializeMultiProfileForModuleApplyV1(
		t,
		root,
		databasePath,
		artifactRoot,
	)

	artifactDirectory := textStatsExampleArtifactPath(t)
	artifactDigest, artifactSize, err := inspectArtifact(artifactDirectory)
	if err != nil {
		t.Fatalf("inspect official text.stats source artifact: %v", err)
	}
	if artifactDigest != localTextStatsDigest || artifactSize != 1936 {
		t.Fatalf(
			"official text.stats source lock=%s/%d want %s/1936",
			artifactDigest,
			artifactSize,
			localTextStatsDigest,
		)
	}

	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-text-stats.json"),
		newW2E2TextStatsEnabledPlanV1(t, artifactDigest, artifactSize, 1),
	)
	runtimeBefore := readW2E2RuntimeCountsV1(t, databasePath)
	dryRun, err := runW2E2TrustedModuleCommandV1[moduleDryRunResultV1](
		ctx,
		runModuleDryRun,
		databasePath,
		artifactRoot,
		planPath,
		artifactDirectory,
		artifactDigest,
	)
	if err != nil {
		t.Fatalf("production module-dry-run for trusted text.stats: %v", err)
	}
	if dryRun.SchemaVersion != moduleDryRunResultSchemaV1 ||
		dryRun.Status != moduleApplyStatusWouldApply ||
		dryRun.DesiredState != moduleApplyEnabledV1 ||
		dryRun.TenantID != defaultTenantID ||
		dryRun.BindingTarget.ProfileID != defaultProfileID ||
		dryRun.InstanceID != w2e2TextStatsInstanceV1 ||
		dryRun.Port != productionActionPort ||
		dryRun.StartupRecoveryRequired || dryRun.Module == nil ||
		dryRun.Module.ID != localTextStatsModuleID ||
		dryRun.Module.ExactVersion != localTextStatsVersion ||
		dryRun.Module.ArtifactDigest != artifactDigest ||
		dryRun.Changes.Installation != moduleDryRunInstallationCreateV1 ||
		dryRun.Changes.Activation != moduleDryRunActivationCreateV1 ||
		dryRun.Changes.Binding.Change != moduleDryRunBindingInsertV1 ||
		dryRun.Changes.Catalog != moduleDryRunCatalogAddInstanceV1 {
		t.Fatalf("trusted text.stats module-dry-run=%+v", dryRun)
	}
	if runtimeAfterDryRun := readW2E2RuntimeCountsV1(t, databasePath); runtimeAfterDryRun != runtimeBefore {
		t.Fatalf(
			"module-dry-run created Run/Attempt rows: before=%+v after=%+v",
			runtimeBefore,
			runtimeAfterDryRun,
		)
	}
	if _, err := os.Lstat(filepath.Join(artifactRoot, artifactDigest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("module-dry-run published or touched the text.stats target: %v", err)
	}

	applied, err := runW2E2TrustedModuleCommandV1[moduleApplyResultV1](
		ctx,
		runModuleApply,
		databasePath,
		artifactRoot,
		planPath,
		artifactDirectory,
		artifactDigest,
	)
	if err != nil {
		basis, control, catalog := loadModuleApplyPublishedStateV1(t, databasePath)
		plan, _, _, planErr := readModuleApplyPlanV1(planPath)
		var exact bool
		var conflict bool
		var inspectErr error
		var semanticErr error
		store, openErr := currentstore.OpenExistingCurrentStore(ctx, databasePath)
		if openErr == nil {
			exact, conflict, inspectErr = inspectCurrentModuleApplyStateV1(
				ctx,
				store,
				artifactRoot,
				plan,
				control,
				catalog,
			)
			semanticErr = verifyEnabledModuleApplySemanticsV1(
				ctx,
				store,
				artifactRoot,
				plan,
				control,
				catalog,
			)
			inspectErr = errors.Join(inspectErr, store.Close())
		} else {
			inspectErr = openErr
		}
		t.Fatalf(
			"production module-apply for trusted text.stats: %v; basis=%+v exact=%v conflict=%v plan=%v inspect=%v semantics=%v",
			err,
			basis,
			exact,
			conflict,
			planErr,
			inspectErr,
			semanticErr,
		)
	}
	if applied.SchemaVersion != moduleApplyResultSchemaV1 ||
		applied.Status != moduleApplyStatusApplied ||
		applied.DesiredState != moduleApplyEnabledV1 ||
		applied.TenantID != defaultTenantID ||
		applied.BindingTarget.ProfileID != defaultProfileID ||
		applied.InstanceID != w2e2TextStatsInstanceV1 ||
		applied.Port != productionActionPort || applied.PointerRevision != 2 ||
		applied.Module == nil || applied.Module.ID != localTextStatsModuleID ||
		applied.Module.ExactVersion != localTextStatsVersion ||
		applied.Module.ArtifactDigest != artifactDigest {
		t.Fatalf("trusted text.stats module-apply=%+v", applied)
	}
	if runtimeAfterApply := readW2E2RuntimeCountsV1(t, databasePath); runtimeAfterApply != runtimeBefore {
		t.Fatalf(
			"module-apply created Run/Attempt rows: before=%+v after=%+v",
			runtimeBefore,
			runtimeAfterApply,
		)
	}

	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open fresh composition after trusted Apply: %v", err)
	}
	registered, err := composition.registry.IsRegistered(
		ctx,
		artifactDigest,
		localTextStatsAdapterID,
	)
	if err != nil || registered {
		_ = composition.Close()
		t.Fatalf("text.stats loaded before a bound Profile admission=%v, %v", registered, err)
	}

	// A different Profile and Workspace have no Action Binding. Selecting them
	// must neither load the trusted adapter nor create an Action Attempt.
	unboundInput := moduleApplyChatInputV1(
		"w2e2-unbound-profile-workspace",
		"unbound Profile remains ordinary Pure Chat",
	)
	unboundInput.ProfileID = moduleApplyRoleProfile
	unboundInput.WorkspaceID = moduleApplyRoleWorkspace
	unbound, err := composition.chat.Chat(ctx, unboundInput)
	if err != nil || !unbound.AdmissionCreated || unbound.TerminalResult == nil ||
		unbound.LoopResult.Disposition != loopapi.DispositionTerminated ||
		unbound.Reply != unboundInput.Message || unbound.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("unbound Profile/Workspace chat=%+v error=%v", unbound, err)
	}
	if models, actions := readW2E2RunAttemptCountsV1(t, databasePath, unbound.RunID); models != 1 || actions != 0 {
		_ = composition.Close()
		t.Fatalf("unbound Run attempts model=%d action=%d want 1/0", models, actions)
	}
	registered, err = composition.registry.IsRegistered(
		ctx,
		artifactDigest,
		localTextStatsAdapterID,
	)
	if err != nil || registered {
		_ = composition.Close()
		t.Fatalf("unbound Profile/Workspace loaded text.stats=%v, %v", registered, err)
	}

	actionInput := moduleApplyChatInputV1(
		"w2e2-bound-action",
		"hello 世界\nsecond line",
	)
	actionResult, err := composition.chat.Chat(ctx, actionInput)
	if err != nil || !actionResult.AdmissionCreated || actionResult.TerminalResult == nil ||
		actionResult.LoopResult.Disposition != loopapi.DispositionTerminated ||
		actionResult.Reply != "text.stats completed." || actionResult.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("bound trusted Action chat=%+v error=%v", actionResult, err)
	}
	evidence := inspectW2E2TextStatsActionChainV1(
		t,
		composition,
		actionResult.RunID,
		actionResult.TerminalResult.AttemptID,
		actionInput.Message,
	)
	registered, err = composition.registry.IsRegistered(
		ctx,
		artifactDigest,
		localTextStatsAdapterID,
	)
	if err != nil || !registered {
		_ = composition.Close()
		t.Fatalf("bound Profile did not load compiled text.stats=%v, %v", registered, err)
	}

	countsBeforeRetry := readW2E2RuntimeCountsV1(t, databasePath)
	retry, err := composition.chat.Chat(ctx, actionInput)
	if err != nil || retry.AdmissionCreated || retry.RunID != actionResult.RunID ||
		retry.Reply != actionResult.Reply || retry.TerminalResult == nil ||
		retry.TerminalResult.AttemptID != actionResult.TerminalResult.AttemptID {
		_ = composition.Close()
		t.Fatalf("exact Action chat retry=%+v error=%v first=%+v", retry, err, actionResult)
	}
	if countsAfterRetry := readW2E2RuntimeCountsV1(t, databasePath); countsAfterRetry != countsBeforeRetry {
		_ = composition.Close()
		t.Fatalf("exact chat retry added Run/Attempt: before=%+v after=%+v", countsBeforeRetry, countsAfterRetry)
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close applied composition: %v", err)
	}

	bundlePath := filepath.Join(root, "w2e2-enabled.bundle")
	createAndVerifyModuleApplyBundleV1(t, databasePath, artifactRoot, bundlePath)
	restoredDatabase, restoredArtifacts := restoreModuleApplyBundleV1(
		t,
		bundlePath,
		filepath.Join(root, "restored"),
	)
	restoredComposition, err := openProductionComposition(
		ctx,
		restoredDatabase,
		restoredArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open restored trusted composition: %v", err)
	}
	restoredEvidence := readW2E2HistoricalActionEvidenceV1(
		t,
		restoredComposition,
		evidence,
	)
	if !reflect.DeepEqual(restoredEvidence, evidence) {
		_ = restoredComposition.Close()
		t.Fatalf("Action evidence changed across backup/restore:\nbefore=%+v\nafter=%+v", evidence, restoredEvidence)
	}
	restoredBeforeRetry := readW2E2RuntimeCountsV1(t, restoredDatabase)
	restoredRetry, err := restoredComposition.chat.Chat(ctx, actionInput)
	if err != nil || restoredRetry.AdmissionCreated ||
		restoredRetry.RunID != actionResult.RunID || restoredRetry.Reply != actionResult.Reply {
		_ = restoredComposition.Close()
		t.Fatalf("restored exact Action retry=%+v error=%v", restoredRetry, err)
	}
	if restoredAfterRetry := readW2E2RuntimeCountsV1(t, restoredDatabase); restoredAfterRetry != restoredBeforeRetry {
		_ = restoredComposition.Close()
		t.Fatalf("restored exact retry added Run/Attempt: before=%+v after=%+v", restoredBeforeRetry, restoredAfterRetry)
	}

	restoredNewInput := moduleApplyChatInputV1(
		"w2e2-restored-new-action",
		"restored action still reaches compiled text stats",
	)
	restoredNew, err := restoredComposition.chat.Chat(ctx, restoredNewInput)
	if err != nil || !restoredNew.AdmissionCreated || restoredNew.TerminalResult == nil ||
		restoredNew.Reply != "text.stats completed." || restoredNew.FailureCode != "" {
		_ = restoredComposition.Close()
		t.Fatalf("new Action after restore=%+v error=%v", restoredNew, err)
	}
	_ = inspectW2E2TextStatsActionChainV1(
		t,
		restoredComposition,
		restoredNew.RunID,
		restoredNew.TerminalResult.AttemptID,
		restoredNewInput.Message,
	)
	if err := restoredComposition.Close(); err != nil {
		t.Fatalf("close restored composition: %v", err)
	}

	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-text-stats.json"),
		newNamedDisabledModuleApplyPlanV1(
			t,
			defaultProfileID,
			w2e2TextStatsInstanceV1,
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
		disabled.DesiredState != moduleApplyDisabledV1 ||
		disabled.PointerRevision != 3 || disabled.Module != nil {
		t.Fatalf("disable trusted text.stats=%+v error=%v", disabled, err)
	}

	disabledComposition, err := openProductionComposition(
		ctx,
		restoredDatabase,
		restoredArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open disabled composition: %v", err)
	}
	disabledHistorical := readW2E2HistoricalActionEvidenceV1(
		t,
		disabledComposition,
		evidence,
	)
	if !reflect.DeepEqual(disabledHistorical, evidence) {
		_ = disabledComposition.Close()
		t.Fatal("Disable changed historical model/Action evidence")
	}
	disabledBefore := readW2E2RuntimeCountsV1(t, restoredDatabase)
	disabledInput := moduleApplyChatInputV1(
		"w2e2-new-run-after-disable",
		"new Run after Disable is Pure Chat",
	)
	disabledResult, err := disabledComposition.chat.Chat(ctx, disabledInput)
	if err != nil || !disabledResult.AdmissionCreated || disabledResult.TerminalResult == nil ||
		disabledResult.Reply != disabledInput.Message || disabledResult.FailureCode != "" {
		_ = disabledComposition.Close()
		t.Fatalf("new Run after Disable=%+v error=%v", disabledResult, err)
	}
	disabledAfter := readW2E2RuntimeCountsV1(t, restoredDatabase)
	if disabledAfter.Runs != disabledBefore.Runs+1 ||
		disabledAfter.ModelAttempts != disabledBefore.ModelAttempts+1 ||
		disabledAfter.DispatchAttempts != disabledBefore.DispatchAttempts {
		_ = disabledComposition.Close()
		t.Fatalf("Disable affected new Run incorrectly: before=%+v after=%+v", disabledBefore, disabledAfter)
	}
	if models, actions := readW2E2RunAttemptCountsV1(t, restoredDatabase, disabledResult.RunID); models != 1 || actions != 0 {
		_ = disabledComposition.Close()
		t.Fatalf("disabled new Run attempts model=%d action=%d want 1/0", models, actions)
	}
	if err := disabledComposition.Close(); err != nil {
		t.Fatalf("close disabled composition: %v", err)
	}
}

func newW2E2TextStatsEnabledPlanV1(
	t *testing.T,
	artifactDigest string,
	artifactSize uint64,
	expectedPointer uint64,
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
		t.Fatalf("freeze W2-E2 Action config: %v", err)
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
		t.Fatalf("freeze W2-E2 Action authority: %v", err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(defaultProfileID),
		"instance_id":               w2e2TextStatsInstanceV1,
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  localTextStatsModuleID,
			"exact_version":       localTextStatsVersion,
			"artifact_digest":     artifactDigest,
			"artifact_size_bytes": artifactSize,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestTrustedInProcess),
				"protocol": moduleapi.RuntimeProtocolGoInProcessV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": 0,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
	})
}

type w2e2ModuleCommandRunnerV1 func(
	context.Context,
	[]string,
	io.Writer,
	io.Writer,
) error

func runW2E2TrustedModuleCommandV1[T any](
	ctx context.Context,
	runner w2e2ModuleCommandRunnerV1,
	databasePath string,
	artifactRoot string,
	planPath string,
	artifactDirectory string,
	artifactGrant string,
) (T, error) {
	var zero T
	args := []string{
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--plan", planPath,
		"--artifact", artifactDirectory,
		"--allow-trusted-in-process-artifact", artifactGrant,
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runner(ctx, args, &stdout, &stderr); err != nil {
		if stdout.Len() != 0 {
			err = errors.Join(err, errors.New("failed module command unexpectedly wrote stdout"))
		}
		if stderr.Len() != 0 {
			err = errors.Join(err, errors.New("failed module command wrote stderr: "+stderr.String()))
		}
		return zero, err
	}
	if stderr.Len() != 0 {
		return zero, errors.New("successful module command wrote stderr: " + stderr.String())
	}
	var result T
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return zero, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return zero, errors.New("module command result has trailing JSON")
	}
	return result, nil
}

func readW2E2RuntimeCountsV1(t *testing.T, databasePath string) w2e2RuntimeCountsV1 {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatalf("open W2-E2 read-only Store: %v", err)
	}
	defer database.Close()
	var counts w2e2RuntimeCountsV1
	if err := database.QueryRowContext(
		context.Background(),
		`SELECT
			(SELECT COUNT(*) FROM runs),
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM dispatch_attempts)`,
	).Scan(&counts.Runs, &counts.ModelAttempts, &counts.DispatchAttempts); err != nil {
		t.Fatalf("count W2-E2 runtime rows: %v", err)
	}
	return counts
}

func readW2E2RunAttemptCountsV1(
	t *testing.T,
	databasePath string,
	runID string,
) (int64, int64) {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatalf("open W2-E2 Run evidence Store: %v", err)
	}
	defer database.Close()
	var models int64
	var actions int64
	if err := database.QueryRowContext(
		context.Background(),
		`SELECT
			(SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
			(SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=? AND dispatch_kind='ACTION')`,
		runID,
		runID,
	).Scan(&models, &actions); err != nil {
		t.Fatalf("count W2-E2 Run attempts: %v", err)
	}
	return models, actions
}

func inspectW2E2TextStatsActionChainV1(
	t *testing.T,
	composition *productionComposition,
	runID string,
	terminalAttemptID string,
	inputText string,
) w2e2ActionChainEvidenceV1 {
	t.Helper()
	ctx := context.Background()
	modelTwo, err := composition.store.GetModelDispatchRecord(ctx, terminalAttemptID)
	if err != nil || modelTwo.Attempt.RunID != runID ||
		modelTwo.Attempt.LogicalStepID != corecontract.SecondModelLogicalStepIDV1 ||
		modelTwo.Attempt.State != corecontract.ModelAttemptSucceeded ||
		modelTwo.Attempt.SourceDispatchAttemptID == "" {
		t.Fatalf("W2-E2 model-2 dispatch=%+v error=%v", modelTwo, err)
	}
	modelTwoRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		modelTwo.Attempt.Request.CanonicalBytes,
	)
	if err != nil || len(modelTwoRequest.Messages) == 0 ||
		!strings.HasPrefix(
			modelTwoRequest.Messages[len(modelTwoRequest.Messages)-1].Content,
			corecontract.UntrustedActionResultPrefixV1,
		) {
		t.Fatalf("W2-E2 model-2 lacks isolated Action result: %v", err)
	}

	action, err := composition.store.GetActionDispatchRecord(
		ctx,
		modelTwo.Attempt.SourceDispatchAttemptID,
	)
	if err != nil || action.Attempt.RunID != runID ||
		action.Attempt.LogicalStepID != corecontract.FirstActionLogicalStepIDV1 ||
		action.Attempt.State != currentstore.ActionDispatchSucceeded ||
		action.Attempt.PublicActionID != "text.stats" ||
		action.Attempt.ProviderActionID != "text.stats" ||
		action.Attempt.SourceModelAttemptID == "" || action.Result == nil ||
		action.Proposal.Kind != currentstore.ContentActionProposal ||
		action.Result.Kind != currentstore.ContentActionResult ||
		action.Attempt.Binding.Provider.ModuleID != localTextStatsModuleID ||
		action.Attempt.Binding.Provider.Version != localTextStatsVersion ||
		action.Attempt.Binding.Provider.ArtifactDigest != localTextStatsDigest ||
		action.Attempt.Binding.Provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		action.Attempt.Binding.Provider.AdapterIdentity != localTextStatsAdapterID {
		t.Fatalf("W2-E2 Action DispatchAttempt=%+v error=%v", action, err)
	}

	modelOne, err := composition.store.GetModelDispatchRecord(
		ctx,
		action.Attempt.SourceModelAttemptID,
	)
	if err != nil || modelOne.Attempt.RunID != runID ||
		modelOne.Attempt.LogicalStepID != corecontract.FirstModelLogicalStepIDV1 ||
		modelOne.Attempt.State != corecontract.ModelAttemptSucceeded ||
		modelOne.Attempt.SourceDispatchAttemptID != "" || modelOne.Attempt.ResultRef == "" {
		t.Fatalf("W2-E2 model-1 dispatch=%+v error=%v", modelOne, err)
	}
	modelOneResult, err := composition.store.GetContent(ctx, modelOne.Attempt.ResultRef)
	if err != nil || modelOneResult.Kind != currentstore.ContentModelResult {
		t.Fatalf("W2-E2 model-1 result=%+v error=%v", modelOneResult, err)
	}
	modelOneOutput, err := moduleapi.RestoreModelGenerateOutputV1(
		modelOneResult.CanonicalBytes,
	)
	if err != nil || modelOneOutput.ActionRequest == nil ||
		modelOneOutput.ActionRequest.ActionID != "text.stats" ||
		modelOneOutput.AssistantText != "" {
		t.Fatalf("W2-E2 model-1 Action request=%+v error=%v", modelOneOutput, err)
	}
	expectedInput, err := moduleapi.CanonicalJSON([]byte(
		`{"text":` + string(mustW2E2JSONV1(t, inputText)) + `}`,
	))
	if err != nil {
		t.Fatalf("freeze expected text.stats input: %v", err)
	}
	if !bytes.Equal(modelOneOutput.ActionRequest.CanonicalInput, expectedInput) {
		t.Fatalf(
			"model-1 Action input=%s want %s",
			modelOneOutput.ActionRequest.CanonicalInput,
			expectedInput,
		)
	}
	var proposal corecontract.ActionProposalV1
	decoder := json.NewDecoder(bytes.NewReader(action.Proposal.CanonicalBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proposal); err != nil ||
		proposal.SchemaVersion != corecontract.ActionProposalSchemaVersionV1 ||
		proposal.MemberSnapshotDigest != action.Attempt.MemberSnapshotDigest ||
		proposal.PublicActionID != "text.stats" ||
		proposal.ProviderActionID != "text.stats" ||
		proposal.DefinitionDigest != action.Attempt.DefinitionDigest ||
		!bytes.Equal(proposal.CanonicalInput, expectedInput) ||
		!bytes.Equal(proposal.PreparedPayload, expectedInput) {
		t.Fatalf("W2-E2 ActionProposal=%+v error=%v", proposal, err)
	}

	var resultEnvelope struct {
		Status string `json:"status"`
		Result struct {
			Bytes uint64 `json:"bytes"`
			Runes uint64 `json:"runes"`
			Words uint64 `json:"words"`
			Lines uint64 `json:"lines"`
		} `json:"result"`
	}
	if err := json.Unmarshal(action.Result.CanonicalBytes, &resultEnvelope); err != nil ||
		resultEnvelope.Status != string(corecontract.ActionResultAvailable) ||
		resultEnvelope.Result.Bytes != uint64(len(inputText)) ||
		resultEnvelope.Result.Runes != uint64(utf8.RuneCountInString(inputText)) ||
		resultEnvelope.Result.Words != uint64(len(strings.Fields(inputText))) ||
		resultEnvelope.Result.Lines != w2e2TextLineCountV1(inputText) {
		t.Fatalf("W2-E2 compiled text.stats result=%+v error=%v", resultEnvelope, err)
	}
	if models, actions := readW2E2RunAttemptCountsV1(
		t,
		composition.store.Path(),
		runID,
	); models != 2 || actions != 1 {
		t.Fatalf("W2-E2 action chain attempts model=%d action=%d want 2/1", models, actions)
	}
	return w2e2ActionChainEvidenceV1{
		ModelOne: modelOne,
		Action:   action,
		ModelTwo: modelTwo,
	}
}

func w2e2TextLineCountV1(value string) uint64 {
	if value == "" {
		return 0
	}
	return uint64(strings.Count(value, "\n") + 1)
}

func readW2E2HistoricalActionEvidenceV1(
	t *testing.T,
	composition *productionComposition,
	want w2e2ActionChainEvidenceV1,
) w2e2ActionChainEvidenceV1 {
	t.Helper()
	ctx := context.Background()
	modelOne, modelOneErr := composition.store.GetModelDispatchRecord(
		ctx,
		want.ModelOne.Attempt.AttemptID,
	)
	action, actionErr := composition.store.GetActionDispatchRecord(
		ctx,
		want.Action.Attempt.AttemptID,
	)
	modelTwo, modelTwoErr := composition.store.GetModelDispatchRecord(
		ctx,
		want.ModelTwo.Attempt.AttemptID,
	)
	if err := errors.Join(modelOneErr, actionErr, modelTwoErr); err != nil {
		t.Fatalf("read historical W2-E2 evidence: %v", err)
	}
	return w2e2ActionChainEvidenceV1{
		ModelOne: modelOne,
		Action:   action,
		ModelTwo: modelTwo,
	}
}

func mustW2E2JSONV1(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal W2-E2 JSON: %v", err)
	}
	return encoded
}
