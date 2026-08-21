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
	"unicode/utf8"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type w2e5bDocumentInsightRunEvidenceV1 struct {
	ActionChain w2e2ActionChainEvidenceV1
	RAG         productionRAGSnapshot
}

type w2e5bReadOnlyApplyStateV1 struct {
	PointerRevision     uint64
	ControlSnapshotID   string
	CatalogGenerationID string
	ArtifactEntries     []string
	MutationRowCounts   map[string]int64
}

// TestW2E5BDocumentInsightProductAcceptanceV1 is the network-free E5-B
// product proof. One installed Document Insight module owns both the real RAG
// Context provider and the real text.stats Action provider consumed by the
// production Universal Loop. The test starts and ends in Pure Chat and uses
// only the production module command, composition, Gateway and backup paths.
func TestW2E5BDocumentInsightProductAcceptanceV1(t *testing.T) {
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
	fixture := newModuleApplyDocumentInsightFixtureV1(t)

	// Establish the real zero-optional-module starting point before installing
	// or binding Document Insight.
	initialComposition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open initial Pure Chat composition: %v", err)
	}
	initialInput := moduleApplyChatInputV1(
		"w2e5b-initial-pure-chat",
		"Pure Chat starts without Document Insight",
	)
	initial, err := initialComposition.chat.Chat(ctx, initialInput)
	if err != nil || !initial.AdmissionCreated || initial.TerminalResult == nil ||
		initial.LoopResult.Disposition != loopapi.DispositionTerminated ||
		initial.Reply != initialInput.Message || initial.FailureCode != "" {
		_ = initialComposition.Close()
		t.Fatalf("initial Pure Chat=%+v error=%v", initial, err)
	}
	assertW2E5ANoKnowledgeCompilation(
		t,
		initialComposition.store,
		initial.TerminalResult.AttemptID,
	)
	if models, actions := readW2E2RunAttemptCountsV1(
		t,
		databasePath,
		initial.RunID,
	); models != 1 || actions != 0 {
		_ = initialComposition.Close()
		t.Fatalf("initial Pure Chat attempts model=%d action=%d want 1/0", models, actions)
	}
	if err := initialComposition.Close(); err != nil {
		t.Fatalf("close initial Pure Chat composition: %v", err)
	}
	assertW2E5BNoSQLiteSidecarsV1(t, databasePath, "initial Pure Chat Close")

	runtimeBeforeApply := readW2E2RuntimeCountsV1(t, databasePath)

	// The Action half cannot publish first. This top-level production command
	// must reject before artifact staging or any Current Store mutation.
	actionFirstPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "action-first.json"),
		newEnabledModuleApplyDocumentInsightActionPlanV1(t, fixture, 1, 0),
	)
	beforeActionFirst := readW2E5BApplyStateV1(t, databasePath, artifactRoot)
	if _, err := runW2E2TrustedModuleCommandV1[moduleApplyResultV1](
		ctx,
		runModuleApply,
		databasePath,
		artifactRoot,
		actionFirstPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	); !w2e5bCommandHasFailureCodeV1(err, moduleApplyFailureTarget) {
		t.Fatalf("Action-first Document Insight error=%v", err)
	}
	assertW2E5BNoSQLiteSidecarsV1(t, databasePath, "Action-first rejection")
	afterActionFirst := readW2E5BApplyStateV1(t, databasePath, artifactRoot)
	if !reflect.DeepEqual(afterActionFirst, beforeActionFirst) {
		t.Fatalf(
			"Action-first command mutated state:\nbefore=%+v\nafter=%+v",
			beforeActionFirst,
			afterActionFirst,
		)
	}
	assertNoModuleApplyStageResidueV1(t, root)

	contextPlanCanonical := newEnabledModuleApplyDocumentInsightContextPlanV1(
		t,
		fixture,
		1,
		1,
	)
	contextPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-document-insight-context.json"),
		contextPlanCanonical,
	)
	contextDryRun, err := runW2E2TrustedModuleCommandV1[moduleDryRunResultV1](
		ctx,
		runModuleDryRun,
		databasePath,
		artifactRoot,
		contextPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || contextDryRun.Status != moduleApplyStatusWouldApply ||
		contextDryRun.Changes.Installation != moduleDryRunInstallationCreateV1 ||
		contextDryRun.Changes.Activation != moduleDryRunActivationCreateV1 ||
		contextDryRun.Changes.Catalog != moduleDryRunCatalogAddInstanceV1 {
		directInput := newModuleApplyDocumentInsightInputV1(
			t,
			databasePath,
			artifactRoot,
			fixture,
			contextPlanCanonical,
		)
		_, directErr := dryRunModulePlanV1(ctx, directInput)
		t.Fatalf(
			"Document Insight Context dry-run=%+v error=%v direct_error=%v direct_cause=%T:%v",
			contextDryRun,
			err,
			directErr,
			errors.Unwrap(directErr),
			errors.Unwrap(directErr),
		)
	}
	contextApplied, err := runW2E2TrustedModuleCommandV1[moduleApplyResultV1](
		ctx,
		runModuleApply,
		databasePath,
		artifactRoot,
		contextPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || contextApplied.Status != moduleApplyStatusApplied ||
		contextApplied.PointerRevision != 2 || contextApplied.Port != productionContextPort {
		t.Fatalf("apply Document Insight Context=%+v error=%v", contextApplied, err)
	}

	actionPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-document-insight-action.json"),
		newEnabledModuleApplyDocumentInsightActionPlanV1(t, fixture, 2, 0),
	)
	actionDryRun, err := runW2E2TrustedModuleCommandV1[moduleDryRunResultV1](
		ctx,
		runModuleDryRun,
		databasePath,
		artifactRoot,
		actionPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || actionDryRun.Status != moduleApplyStatusWouldApply ||
		actionDryRun.Changes.Installation != moduleDryRunInstallationReuseV1 ||
		actionDryRun.Changes.Activation != moduleDryRunActivationReuseCurrentV1 ||
		actionDryRun.Changes.Catalog != moduleDryRunCatalogRetainInstanceV1 {
		t.Fatalf("Document Insight Action dry-run=%+v error=%v", actionDryRun, err)
	}
	actionApplied, err := runW2E2TrustedModuleCommandV1[moduleApplyResultV1](
		ctx,
		runModuleApply,
		databasePath,
		artifactRoot,
		actionPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || actionApplied.Status != moduleApplyStatusApplied ||
		actionApplied.PointerRevision != 3 || actionApplied.Port != productionActionPort {
		t.Fatalf("apply Document Insight Action=%+v error=%v", actionApplied, err)
	}
	provider, basicProvider := assertW2E5BDocumentInsightCurrentClosureV1(
		t,
		databasePath,
		artifactRoot,
		fixture,
		3,
		true,
		true,
		true,
	)
	if runtimeAfterApply := readW2E2RuntimeCountsV1(t, databasePath); runtimeAfterApply != runtimeBeforeApply {
		t.Fatalf(
			"Document Insight Apply created runtime rows: before=%+v after=%+v",
			runtimeBeforeApply,
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
		t.Fatalf("open Document Insight composition: %v", err)
	}
	registered, err := composition.registry.IsRegistered(
		ctx,
		fixture.ArtifactDigest,
		exactadapter.DocumentInsightAdapterIdentityV1,
	)
	if err != nil || registered {
		_ = composition.Close()
		t.Fatalf("Document Insight loaded before admission=%v error=%v", registered, err)
	}

	// A different Profile/Workspace has no Document Insight Binding and must
	// not cause the shared artifact to load.
	unboundInput := moduleApplyChatInputV1(
		"w2e5b-unbound-profile",
		"an unbound Profile remains ordinary Pure Chat",
	)
	unboundInput.ProfileID = moduleApplyRoleProfile
	unboundInput.WorkspaceID = moduleApplyRoleWorkspace
	unbound, err := composition.chat.Chat(ctx, unboundInput)
	if err != nil || !unbound.AdmissionCreated || unbound.TerminalResult == nil ||
		unbound.Reply != unboundInput.Message || unbound.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("unbound Document Insight chat=%+v error=%v", unbound, err)
	}
	assertW2E5ANoKnowledgeCompilation(
		t,
		composition.store,
		unbound.TerminalResult.AttemptID,
	)
	if models, actions := readW2E2RunAttemptCountsV1(
		t,
		databasePath,
		unbound.RunID,
	); models != 1 || actions != 0 {
		_ = composition.Close()
		t.Fatalf("unbound Run attempts model=%d action=%d want 1/0", models, actions)
	}
	registered, err = composition.registry.IsRegistered(
		ctx,
		fixture.ArtifactDigest,
		exactadapter.DocumentInsightAdapterIdentityV1,
	)
	if err != nil || registered {
		_ = composition.Close()
		t.Fatalf("unbound Profile loaded Document Insight=%v error=%v", registered, err)
	}

	boundInput := moduleApplyChatInputV1(
		"w2e5b-bound-rag-action",
		"How does FreeAgent shared knowledge stay independent from Agent and Workspace definitions?",
	)
	bound, err := composition.chat.Chat(ctx, boundInput)
	if err != nil || !bound.AdmissionCreated || bound.TerminalResult == nil ||
		bound.LoopResult.Disposition != loopapi.DispositionTerminated ||
		bound.Reply != exactadapter.TextStatsCompletedAssistantTextV1 ||
		bound.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("bound Document Insight chat=%+v error=%v", bound, err)
	}
	member, memberCanonical := inspectW2E5BDocumentInsightMemberV1(
		t,
		databasePath,
		bound.RunID,
		provider,
		basicProvider,
		true,
		true,
	)
	evidence := inspectW2E5BDocumentInsightRunV1(
		t,
		composition,
		bound.RunID,
		bound.TerminalResult.AttemptID,
		boundInput.Message,
		provider,
		member.Actions,
	)
	registered, err = composition.registry.IsRegistered(
		ctx,
		fixture.ArtifactDigest,
		exactadapter.DocumentInsightAdapterIdentityV1,
	)
	if err != nil || !registered {
		_ = composition.Close()
		t.Fatalf("bound Run did not load Document Insight=%v error=%v", registered, err)
	}

	countsBeforeRetry := readW2E2RuntimeCountsV1(t, databasePath)
	retry, err := composition.chat.Chat(ctx, boundInput)
	if err != nil || retry.AdmissionCreated || retry.RunID != bound.RunID ||
		retry.TerminalResult == nil ||
		retry.TerminalResult.AttemptID != bound.TerminalResult.AttemptID ||
		retry.Reply != bound.Reply {
		_ = composition.Close()
		t.Fatalf("Document Insight exact retry=%+v error=%v first=%+v", retry, err, bound)
	}
	if countsAfterRetry := readW2E2RuntimeCountsV1(t, databasePath); countsAfterRetry != countsBeforeRetry {
		_ = composition.Close()
		t.Fatalf("exact retry added Run/Attempt: before=%+v after=%+v", countsBeforeRetry, countsAfterRetry)
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close Document Insight composition: %v", err)
	}

	bundlePath := filepath.Join(root, "document-insight-enabled.bundle")
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
		t.Fatalf("open restored Document Insight composition: %v", err)
	}
	restoredMember, restoredMemberCanonical := inspectW2E5BDocumentInsightMemberV1(
		t,
		restoredDatabase,
		bound.RunID,
		provider,
		basicProvider,
		true,
		true,
	)
	restoredEvidence := inspectW2E5BDocumentInsightRunV1(
		t,
		restoredComposition,
		bound.RunID,
		bound.TerminalResult.AttemptID,
		boundInput.Message,
		provider,
		restoredMember.Actions,
	)
	if !reflect.DeepEqual(restoredEvidence.ActionChain, evidence.ActionChain) {
		_ = restoredComposition.Close()
		t.Fatalf(
			"Document Insight Action evidence changed across restore:\nbefore=%+v\nafter=%+v",
			evidence.ActionChain,
			restoredEvidence.ActionChain,
		)
	}
	assertProductionRAGSnapshotEqual(
		t,
		evidence.RAG,
		restoredEvidence.RAG,
		"W2-E5-B restore",
	)
	if !bytes.Equal(restoredMemberCanonical, memberCanonical) {
		_ = restoredComposition.Close()
		t.Fatal("Document Insight MemberExecutionSnapshot changed across restore")
	}

	restoredNewInput := moduleApplyChatInputV1(
		"w2e5b-restored-new-rag-action",
		"FreeAgent shared knowledge remains available after restore.",
	)
	restoredNew, err := restoredComposition.chat.Chat(ctx, restoredNewInput)
	if err != nil || !restoredNew.AdmissionCreated || restoredNew.TerminalResult == nil ||
		restoredNew.Reply != exactadapter.TextStatsCompletedAssistantTextV1 ||
		restoredNew.FailureCode != "" {
		_ = restoredComposition.Close()
		t.Fatalf("new Document Insight Run after restore=%+v error=%v", restoredNew, err)
	}
	restoredNewMember, _ := inspectW2E5BDocumentInsightMemberV1(
		t,
		restoredDatabase,
		restoredNew.RunID,
		provider,
		basicProvider,
		true,
		true,
	)
	_ = inspectW2E5BDocumentInsightRunV1(
		t,
		restoredComposition,
		restoredNew.RunID,
		restoredNew.TerminalResult.AttemptID,
		restoredNewInput.Message,
		provider,
		restoredNewMember.Actions,
	)
	if err := restoredComposition.Close(); err != nil {
		t.Fatalf("close restored Document Insight composition: %v", err)
	}

	// Context cannot be removed while the same module's Action Binding still
	// depends on its governed Knowledge prerequisite.
	contextFirstDisablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-context-first.json"),
		newDisabledDeclarativeModuleApplyPlanV1(
			t,
			moduleApplyDocumentInsightInstanceIDV1,
			3,
		),
	)
	beforeContextFirst := readW2E5BApplyStateV1(
		t,
		restoredDatabase,
		restoredArtifacts,
	)
	if _, err := runModuleApplyFixtureV1(
		restoredDatabase,
		restoredArtifacts,
		contextFirstDisablePath,
		"",
		"",
	); !w2e5bCommandHasFailureCodeV1(err, moduleApplyFailurePublication) {
		t.Fatalf("Context-first Disable error=%v", err)
	}
	afterContextFirst := readW2E5BApplyStateV1(
		t,
		restoredDatabase,
		restoredArtifacts,
	)
	if !reflect.DeepEqual(afterContextFirst, beforeContextFirst) {
		t.Fatalf(
			"Context-first Disable mutated state:\nbefore=%+v\nafter=%+v",
			beforeContextFirst,
			afterContextFirst,
		)
	}

	actionDisablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-action.json"),
		newNamedDisabledModuleApplyPlanV1(
			t,
			moduleApplyTestProfileID,
			moduleApplyDocumentInsightInstanceIDV1,
			3,
		),
	)
	actionDisabled, err := runModuleApplyFixtureV1(
		restoredDatabase,
		restoredArtifacts,
		actionDisablePath,
		"",
		"",
	)
	if err != nil || actionDisabled.Status != moduleApplyStatusApplied ||
		actionDisabled.PointerRevision != 4 || actionDisabled.Port != productionActionPort {
		t.Fatalf("disable Document Insight Action=%+v error=%v", actionDisabled, err)
	}
	provider, basicProvider = assertW2E5BDocumentInsightCurrentClosureV1(
		t,
		restoredDatabase,
		restoredArtifacts,
		fixture,
		4,
		true,
		false,
		true,
	)

	contextOnlyComposition, err := openProductionComposition(
		ctx,
		restoredDatabase,
		restoredArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open Context-only Document Insight composition: %v", err)
	}
	contextOnlyInput := moduleApplyChatInputV1(
		"w2e5b-context-only",
		"How does FreeAgent shared knowledge remain independent?",
	)
	contextOnly, err := contextOnlyComposition.chat.Chat(ctx, contextOnlyInput)
	if err != nil || !contextOnly.AdmissionCreated || contextOnly.TerminalResult == nil ||
		contextOnly.Reply != contextOnlyInput.Message || contextOnly.FailureCode != "" {
		_ = contextOnlyComposition.Close()
		t.Fatalf("Context-only Document Insight chat=%+v error=%v", contextOnly, err)
	}
	_ = inspectProductionRAGDispatch(
		t,
		contextOnlyComposition.store,
		contextOnly.RunID,
		contextOnly.TerminalResult.AttemptID,
		"w2e5b-context-only",
		contextOnlyInput.Message,
	)
	if models, actions := readW2E2RunAttemptCountsV1(
		t,
		restoredDatabase,
		contextOnly.RunID,
	); models != 1 || actions != 0 {
		_ = contextOnlyComposition.Close()
		t.Fatalf("Context-only attempts model=%d action=%d want 1/0", models, actions)
	}
	_, _ = inspectW2E5BDocumentInsightMemberV1(
		t,
		restoredDatabase,
		contextOnly.RunID,
		provider,
		basicProvider,
		true,
		false,
	)
	if err := contextOnlyComposition.Close(); err != nil {
		t.Fatalf("close Context-only composition: %v", err)
	}

	contextDisablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-context.json"),
		newDisabledDeclarativeModuleApplyPlanV1(
			t,
			moduleApplyDocumentInsightInstanceIDV1,
			4,
		),
	)
	contextDisabled, err := runModuleApplyFixtureV1(
		restoredDatabase,
		restoredArtifacts,
		contextDisablePath,
		"",
		"",
	)
	if err != nil || contextDisabled.Status != moduleApplyStatusApplied ||
		contextDisabled.PointerRevision != 5 || contextDisabled.Port != productionContextPort {
		t.Fatalf("disable Document Insight Context=%+v error=%v", contextDisabled, err)
	}
	_, _ = assertW2E5BDocumentInsightCurrentClosureV1(
		t,
		restoredDatabase,
		restoredArtifacts,
		fixture,
		5,
		false,
		false,
		false,
	)

	finalComposition, err := openProductionComposition(
		ctx,
		restoredDatabase,
		restoredArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open final Pure Chat composition: %v", err)
	}
	registered, err = finalComposition.registry.IsRegistered(
		ctx,
		fixture.ArtifactDigest,
		exactadapter.DocumentInsightAdapterIdentityV1,
	)
	if err != nil || registered {
		_ = finalComposition.Close()
		t.Fatalf("disabled Document Insight loaded=%v error=%v", registered, err)
	}
	finalInput := moduleApplyChatInputV1(
		"w2e5b-final-pure-chat",
		"FreeAgent shared knowledge is no longer bound",
	)
	finalResult, err := finalComposition.chat.Chat(ctx, finalInput)
	if err != nil || !finalResult.AdmissionCreated || finalResult.TerminalResult == nil ||
		finalResult.Reply != finalInput.Message || finalResult.FailureCode != "" {
		_ = finalComposition.Close()
		t.Fatalf("final Pure Chat=%+v error=%v", finalResult, err)
	}
	assertW2E5ANoKnowledgeCompilation(
		t,
		finalComposition.store,
		finalResult.TerminalResult.AttemptID,
	)
	if models, actions := readW2E2RunAttemptCountsV1(
		t,
		restoredDatabase,
		finalResult.RunID,
	); models != 1 || actions != 0 {
		_ = finalComposition.Close()
		t.Fatalf("final Pure Chat attempts model=%d action=%d want 1/0", models, actions)
	}
	assertW1PureChatOrdinaryRunV1(t, restoredDatabase, finalResult.RunID)
	if err := finalComposition.Close(); err != nil {
		t.Fatalf("close final Pure Chat composition: %v", err)
	}
}

func w2e5bCommandHasFailureCodeV1(
	err error,
	code moduleApplyFailureCodeV1,
) bool {
	return err != nil && strings.Contains(err.Error(), "("+string(code)+")")
}

func assertW2E5BNoSQLiteSidecarsV1(
	t *testing.T,
	databasePath string,
	phase string,
) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		path := databasePath + suffix
		if _, err := os.Lstat(path); err == nil {
			t.Fatalf("%s retained SQLite sidecar %s", phase, suffix)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s inspect SQLite sidecar %s: %v", phase, suffix, err)
		}
	}
}

func readW2E5BApplyStateV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
) w2e5bReadOnlyApplyStateV1 {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	state := w2e5bReadOnlyApplyStateV1{
		MutationRowCounts: make(map[string]int64),
	}
	if err := database.QueryRow(`
		SELECT pointer_revision, snapshot_id, catalog_generation_id
		FROM control_current WHERE tenant_id=?
	`, defaultTenantID).Scan(
		&state.PointerRevision,
		&state.ControlSnapshotID,
		&state.CatalogGenerationID,
	); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	for _, table := range []string{
		"content_records",
		"control_snapshots",
		"module_installations",
		"module_activations",
		"runtime_catalog_generations",
		"control_current",
	} {
		var count int64
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			_ = database.Close()
			t.Fatalf("count W2-E5-B mutation table %s: %v", table, err)
		}
		state.MutationRowCounts[table] = count
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	state.ArtifactEntries = make([]string, 0, len(entries))
	for _, entry := range entries {
		state.ArtifactEntries = append(state.ArtifactEntries, entry.Name())
	}
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatalf("reopen Current Store after read-only observation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("requiesce Current Store after read-only observation: %v", err)
	}
	assertW2E5BNoSQLiteSidecarsV1(t, databasePath, "read-only Apply observation")
	return state
}

func assertW2E5BDocumentInsightCurrentClosureV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	fixture moduleApplyDocumentInsightFixtureV1,
	wantPointer uint64,
	wantContext bool,
	wantAction bool,
	wantCatalog bool,
) (moduleapi.ActivatedModuleRef, moduleapi.ActivatedModuleRef) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, loadErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	installation, installationErr := store.GetModuleInstallationByIdentity(
		ctx,
		localDocumentInsightModuleID,
		localDocumentInsightVersion,
	)
	activation, activationErr := store.GetLatestModuleActivationForInstance(
		ctx,
		defaultTenantID,
		moduleApplyDocumentInsightInstanceIDV1,
	)
	closeErr := store.Close()
	if err := errors.Join(loadErr, installationErr, activationErr, closeErr); err != nil {
		t.Fatalf("load Document Insight current closure: %v", err)
	}
	if basis.PointerRevision != wantPointer {
		t.Fatalf("Document Insight pointer=%d want %d", basis.PointerRevision, wantPointer)
	}
	if installation.ArtifactDigest != fixture.ArtifactDigest ||
		activation.InstallationID != installation.InstallationID ||
		activation.ActivationRevision != 1 ||
		activation.InstanceID != moduleApplyDocumentInsightInstanceIDV1 {
		t.Fatalf(
			"Document Insight Installation/Activation mismatch: installation=%+v activation=%+v",
			installation,
			activation,
		)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}
	verified, err := validateDocumentInsightArtifactFromArtifact(
		ctx,
		fixture.ArtifactDigest,
		exactadapter.DocumentInsightAdapterIdentityV1,
		filepath.Join(artifactRoot, fixture.ArtifactDigest),
		fixture.ArtifactSizeBytes,
		&provider,
	)
	if err != nil || !bytes.Equal(verified.ManifestCanonical, installation.ManifestBytes) {
		t.Fatalf("verify installed Document Insight artifact=%+v error=%v", verified, err)
	}

	profile, found := control.FindProfile(moduleApplyTestProfileID)
	if !found {
		t.Fatalf("Document Insight target Profile %q is absent", moduleApplyTestProfileID)
	}
	contextBindings := 0
	actionBindings := 0
	for _, binding := range profile.Bindings {
		if binding.InstanceID != moduleApplyDocumentInsightInstanceIDV1 {
			continue
		}
		switch binding.Port {
		case productionContextPort:
			contextBindings++
		case productionActionPort:
			actionBindings++
		default:
			t.Fatalf("Document Insight has unexpected Binding %+v", binding)
		}
	}
	wantContextCount := 0
	if wantContext {
		wantContextCount = 1
	}
	wantActionCount := 0
	if wantAction {
		wantActionCount = 1
	}
	if contextBindings != wantContextCount || actionBindings != wantActionCount {
		t.Fatalf(
			"Document Insight Binding counts context/action=%d/%d want %d/%d",
			contextBindings,
			actionBindings,
			wantContextCount,
			wantActionCount,
		)
	}

	entry, catalogFound := catalog.FindInstance(moduleApplyDocumentInsightInstanceIDV1)
	if catalogFound != wantCatalog {
		t.Fatalf("Document Insight Catalog found=%v want %v", catalogFound, wantCatalog)
	}
	if wantCatalog && (entry.Activation != provider ||
		!reflect.DeepEqual(entry.Provides, moduleapi.ExactDocumentInsightProvidesV1())) {
		t.Fatalf("Document Insight Catalog entry=%+v activation=%+v", entry, activation)
	}
	basicEntry, basicFound := catalog.FindInstance(moduleApplyBasicContextInstance)
	if !basicFound {
		t.Fatalf("Document Insight current Catalog lost %q", moduleApplyBasicContextInstance)
	}

	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var installations, activations int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM module_installations
		WHERE module_id=? AND exact_version=? AND artifact_digest=?
	`, localDocumentInsightModuleID, localDocumentInsightVersion, fixture.ArtifactDigest).Scan(
		&installations,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM module_activations
		WHERE tenant_id=? AND instance_id=?
	`, defaultTenantID, moduleApplyDocumentInsightInstanceIDV1).Scan(&activations); err != nil {
		t.Fatal(err)
	}
	if installations != 1 || activations != 1 {
		t.Fatalf(
			"Document Insight rows installations/activations=%d/%d want 1/1",
			installations,
			activations,
		)
	}
	return provider, basicEntry.Activation
}

func inspectW2E5BDocumentInsightMemberV1(
	t *testing.T,
	databasePath string,
	runID string,
	wantProvider moduleapi.ActivatedModuleRef,
	wantBasicProvider moduleapi.ActivatedModuleRef,
	wantContext bool,
	wantAction bool,
) (corecontract.MemberExecutionSnapshot, []byte) {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var canonical []byte
	if err := database.QueryRow(`
		SELECT canonical_json FROM member_execution_snapshots WHERE run_id=?
	`, runID).Scan(&canonical); err != nil {
		t.Fatalf("read Document Insight MemberExecutionSnapshot: %v", err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(canonical)
	if err != nil {
		t.Fatalf("restore Document Insight MemberExecutionSnapshot: %v", err)
	}
	contextProviders := make([]moduleapi.ActivatedModuleRef, 0, 1)
	actionProviders := make([]moduleapi.ActivatedModuleRef, 0, 1)
	contextPlanCount := 0
	actionPlanCount := 0
	contextPlanBindings := 0
	actionPlanBindings := 0
	for _, plan := range member.PortPlans {
		switch plan.Port {
		case productionContextPort:
			contextPlanCount++
			contextPlanBindings = len(plan.Bindings)
			var actualBasicProvider moduleapi.ActivatedModuleRef
			if len(plan.Bindings) > 0 {
				actualBasicProvider = plan.Bindings[0].Provider
			}
			if actualBasicProvider != wantBasicProvider {
				t.Fatalf(
					"Document Insight Context PortPlan index 0 Provider=%+v want context.basic %+v: %+v",
					actualBasicProvider,
					wantBasicProvider,
					plan,
				)
			}
			if wantContext {
				if len(plan.Bindings) < 2 {
					t.Fatalf("Document Insight Context PortPlan has no index 1 Binding: %+v", plan)
				}
				if plan.Bindings[1].Provider != wantProvider {
					t.Fatalf(
						"Document Insight Context PortPlan index 1 Provider=%+v want %+v: %+v",
						plan.Bindings[1].Provider,
						wantProvider,
						plan,
					)
				}
			}
		case productionActionPort:
			actionPlanCount++
			actionPlanBindings = len(plan.Bindings)
		}
		for _, binding := range plan.Bindings {
			if binding.Provider.InstanceID != moduleApplyDocumentInsightInstanceIDV1 {
				continue
			}
			switch plan.Port {
			case productionContextPort:
				contextProviders = append(contextProviders, binding.Provider)
			case productionActionPort:
				actionProviders = append(actionProviders, binding.Provider)
			default:
				t.Fatalf("Document Insight froze unexpected PortPlan %+v", plan)
			}
		}
	}
	wantContextPlanBindings := 1 // the default context.basic Binding remains
	if wantContext {
		wantContextPlanBindings++
	}
	wantActionPlanCount := 0
	if wantAction {
		wantActionPlanCount = 1
	}
	if contextPlanCount != 1 ||
		contextPlanBindings != wantContextPlanBindings ||
		actionPlanCount != wantActionPlanCount ||
		actionPlanBindings != wantActionPlanCount {
		t.Fatalf(
			"Document Insight PortPlan shape context plans/bindings=%d/%d action=%d/%d want 1/%d %d/%d: %+v",
			contextPlanCount,
			contextPlanBindings,
			actionPlanCount,
			actionPlanBindings,
			wantContextPlanBindings,
			wantActionPlanCount,
			wantActionPlanCount,
			member,
		)
	}
	wantContextCount := 0
	if wantContext {
		wantContextCount = 1
	}
	wantActionCount := 0
	if wantAction {
		wantActionCount = 1
	}
	if len(contextProviders) != wantContextCount ||
		len(actionProviders) != wantActionCount {
		t.Fatalf(
			"Document Insight Member providers context/action=%d/%d want %d/%d: %+v",
			len(contextProviders),
			len(actionProviders),
			wantContextCount,
			wantActionCount,
			member,
		)
	}
	if wantContext && contextProviders[0] != wantProvider {
		t.Fatalf("Context Provider=%+v want %+v", contextProviders[0], wantProvider)
	}
	if wantAction && actionProviders[0] != wantProvider {
		t.Fatalf("Action Provider=%+v want %+v", actionProviders[0], wantProvider)
	}
	if wantContext && wantAction && contextProviders[0] != actionProviders[0] {
		t.Fatalf(
			"Document Insight PortPlans froze different providers: context=%+v action=%+v",
			contextProviders[0],
			actionProviders[0],
		)
	}
	wantActions := 0
	if wantAction {
		wantActions = 1
	}
	if len(member.Actions) != wantActions ||
		(wantAction && (member.Actions[0].PublicActionID != exactadapter.TextStatsActionIDV1 ||
			member.Actions[0].ProviderActionID != exactadapter.TextStatsActionIDV1 ||
			member.Actions[0].BindingIndex != 0)) {
		t.Fatalf("Document Insight frozen Actions=%+v", member.Actions)
	}
	return member, bytes.Clone(canonical)
}

func inspectW2E5BDocumentInsightRunV1(
	t *testing.T,
	composition *productionComposition,
	runID string,
	terminalAttemptID string,
	inputText string,
	wantProvider moduleapi.ActivatedModuleRef,
	actions []corecontract.FrozenActionDefinitionV1,
) w2e5bDocumentInsightRunEvidenceV1 {
	t.Helper()
	ctx := context.Background()
	modelTwo, err := composition.store.GetModelDispatchRecord(ctx, terminalAttemptID)
	if err != nil || modelTwo.Attempt.RunID != runID ||
		modelTwo.Attempt.LogicalStepID != corecontract.SecondModelLogicalStepIDV1 ||
		modelTwo.Attempt.State != corecontract.ModelAttemptSucceeded ||
		modelTwo.Attempt.SourceDispatchAttemptID == "" {
		t.Fatalf("W2-E5-B model-2 dispatch=%+v error=%v", modelTwo, err)
	}
	modelTwoRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		modelTwo.Attempt.Request.CanonicalBytes,
	)
	if err != nil || len(modelTwoRequest.Messages) == 0 ||
		!strings.HasPrefix(
			modelTwoRequest.Messages[len(modelTwoRequest.Messages)-1].Content,
			corecontract.UntrustedActionResultPrefixV1,
		) {
		t.Fatalf("W2-E5-B model-2 lacks isolated Action result: %v", err)
	}

	action, err := composition.store.GetActionDispatchRecord(
		ctx,
		modelTwo.Attempt.SourceDispatchAttemptID,
	)
	if err != nil || action.Attempt.RunID != runID ||
		action.Attempt.LogicalStepID != corecontract.FirstActionLogicalStepIDV1 ||
		action.Attempt.State != currentstore.ActionDispatchSucceeded ||
		action.Attempt.PublicActionID != exactadapter.TextStatsActionIDV1 ||
		action.Attempt.ProviderActionID != exactadapter.TextStatsActionIDV1 ||
		action.Attempt.SourceModelAttemptID == "" || action.Result == nil ||
		action.Proposal.Kind != currentstore.ContentActionProposal ||
		action.Result.Kind != currentstore.ContentActionResult ||
		action.Attempt.Binding.Provider != wantProvider {
		t.Fatalf("W2-E5-B Action DispatchAttempt=%+v error=%v", action, err)
	}

	modelOne, err := composition.store.GetModelDispatchRecord(
		ctx,
		action.Attempt.SourceModelAttemptID,
	)
	if err != nil || modelOne.Attempt.RunID != runID ||
		modelOne.Attempt.LogicalStepID != corecontract.FirstModelLogicalStepIDV1 ||
		modelOne.Attempt.State != corecontract.ModelAttemptSucceeded ||
		modelOne.Attempt.SourceDispatchAttemptID != "" || modelOne.Attempt.ResultRef == "" ||
		modelOne.Attempt.ContextCompilation == nil {
		t.Fatalf("W2-E5-B model-1 dispatch=%+v error=%v", modelOne, err)
	}

	compilation, err := corecontract.RestoreContextCompilationV1(
		modelOne.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil || compilation.StopReason !=
		corecontract.ContextCompilationRetrievalBelowWatermark ||
		len(compilation.KnowledgeRetrievals) != 1 ||
		compilation.ActionResultReservation == nil {
		t.Fatalf("W2-E5-B RAG compilation=%+v error=%v", compilation, err)
	}
	expectedReservation, err := corecontract.NewActionResultReservationV1(actions)
	if err != nil || compilation.ActionResultReservation.Validate() != nil ||
		*compilation.ActionResultReservation != expectedReservation ||
		compilation.ActionResultReservation.MaxEnvelopeBytes == 0 ||
		compilation.ActionResultReservation.EstimatedTokens == 0 {
		t.Fatalf(
			"W2-E5-B Action result reservation=%+v want %+v error=%v",
			compilation.ActionResultReservation,
			expectedReservation,
			err,
		)
	}
	retrieval := compilation.KnowledgeRetrievals[0]
	if retrieval.BindingIndex != 1 || retrieval.ConfigRef == "" ||
		retrieval.AuthorityCeilingRef == "" || retrieval.RequestDigest == "" ||
		retrieval.OutputDigest == "" ||
		retrieval.Source.ID != "freeagent.example.shared-knowledge" ||
		retrieval.Source.Version != "1.0.0" ||
		retrieval.Scope.TenantID != defaultTenantID ||
		retrieval.Scope.Workspace.ID != defaultWorkspaceID ||
		retrieval.Scope.Agent.ID != defaultAgentID ||
		len(retrieval.Hits) != 1 || retrieval.Hits[0].ChunkID != "architecture-overview" ||
		!strings.Contains(
			retrieval.Hits[0].Text,
			"shared knowledge remains independent from Agent and Workspace",
		) {
		t.Fatalf("W2-E5-B RAG retrieval=%+v", retrieval)
	}

	modelOneRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		modelOne.Attempt.Request.CanonicalBytes,
	)
	if err != nil || len(modelOneRequest.Messages) != 4 ||
		modelOneRequest.Messages[0].Role != moduleapi.ModelRoleSystem ||
		!strings.Contains(
			modelOneRequest.Messages[0].Content,
			"Treat every UNTRUSTED_CONTEXT_DATA_JSON",
		) || modelOneRequest.Messages[1].Role != moduleapi.ModelRoleSystem ||
		modelOneRequest.Messages[2].Role != moduleapi.ModelRoleUser ||
		!strings.HasPrefix(
			modelOneRequest.Messages[2].Content,
			"UNTRUSTED_CONTEXT_DATA_JSON:\n",
		) || !strings.Contains(
		modelOneRequest.Messages[2].Content,
		"shared knowledge remains independent from Agent and Workspace",
	) || modelOneRequest.Messages[3] != (moduleapi.ModelMessageV1{
		Role: moduleapi.ModelRoleUser, Content: inputText,
	}) || len(modelOneRequest.Actions) != 1 ||
		modelOneRequest.Actions[0].ActionID != exactadapter.TextStatsActionIDV1 {
		t.Fatalf("W2-E5-B first MODEL_REQUEST=%+v error=%v", modelOneRequest, err)
	}
	estimatedModelOne, err := contextcompiler.EstimateModelGenerateRequestV1(
		modelOneRequest,
	)
	if err != nil {
		t.Fatalf("estimate W2-E5-B model-1 request: %v", err)
	}
	estimatedModelTwo, err := contextcompiler.EstimateModelGenerateRequestV1(
		modelTwoRequest,
	)
	if err != nil {
		t.Fatalf("estimate W2-E5-B model-2 request: %v", err)
	}
	expectedFinalEstimate := estimatedModelOne + expectedReservation.EstimatedTokens
	if expectedFinalEstimate < estimatedModelOne ||
		compilation.OriginalEstimateTokens != expectedFinalEstimate ||
		compilation.FinalEstimateTokens != expectedFinalEstimate ||
		estimatedModelTwo > compilation.FinalEstimateTokens {
		t.Fatalf(
			"W2-E5-B budget original/final=%d/%d model-1=%d reservation=%d model-2=%d",
			compilation.OriginalEstimateTokens,
			compilation.FinalEstimateTokens,
			estimatedModelOne,
			expectedReservation.EstimatedTokens,
			estimatedModelTwo,
		)
	}

	modelOneResult, err := composition.store.GetContent(ctx, modelOne.Attempt.ResultRef)
	if err != nil || modelOneResult.Kind != currentstore.ContentModelResult {
		t.Fatalf("W2-E5-B model-1 result=%+v error=%v", modelOneResult, err)
	}
	modelOneOutput, err := moduleapi.RestoreModelGenerateOutputV1(
		modelOneResult.CanonicalBytes,
	)
	if err != nil || modelOneOutput.ActionRequest == nil ||
		modelOneOutput.ActionRequest.ActionID != exactadapter.TextStatsActionIDV1 ||
		modelOneOutput.AssistantText != "" {
		t.Fatalf("W2-E5-B model-1 Action request=%+v error=%v", modelOneOutput, err)
	}
	expectedInput, err := moduleapi.CanonicalJSON([]byte(
		`{"text":` + string(mustW2E2JSONV1(t, inputText)) + `}`,
	))
	if err != nil || !bytes.Equal(
		modelOneOutput.ActionRequest.CanonicalInput,
		expectedInput,
	) {
		t.Fatalf(
			"W2-E5-B Action input=%s want %s error=%v",
			modelOneOutput.ActionRequest.CanonicalInput,
			expectedInput,
			err,
		)
	}

	var proposal corecontract.ActionProposalV1
	decoder := json.NewDecoder(bytes.NewReader(action.Proposal.CanonicalBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proposal); err != nil ||
		proposal.SchemaVersion != corecontract.ActionProposalSchemaVersionV1 ||
		proposal.MemberSnapshotDigest != action.Attempt.MemberSnapshotDigest ||
		proposal.PublicActionID != exactadapter.TextStatsActionIDV1 ||
		proposal.ProviderActionID != exactadapter.TextStatsActionIDV1 ||
		proposal.DefinitionDigest != action.Attempt.DefinitionDigest ||
		!bytes.Equal(proposal.CanonicalInput, expectedInput) ||
		!bytes.Equal(proposal.PreparedPayload, expectedInput) {
		t.Fatalf("W2-E5-B ActionProposal=%+v error=%v", proposal, err)
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
		t.Fatalf("W2-E5-B text.stats result=%+v error=%v", resultEnvelope, err)
	}
	if models, actions := readW2E2RunAttemptCountsV1(
		t,
		composition.store.Path(),
		runID,
	); models != 2 || actions != 1 {
		t.Fatalf("W2-E5-B attempts model=%d action=%d want 2/1", models, actions)
	}
	return w2e5bDocumentInsightRunEvidenceV1{
		ActionChain: w2e2ActionChainEvidenceV1{
			ModelOne: modelOne,
			Action:   action,
			ModelTwo: modelTwo,
		},
		RAG: productionRAGSnapshot{
			attemptID:        modelOne.Attempt.AttemptID,
			requestCanonical: bytes.Clone(modelOne.Attempt.Request.CanonicalBytes),
			compilationCanonical: bytes.Clone(
				modelOne.Attempt.ContextCompilation.CanonicalBytes,
			),
		},
	}
}
