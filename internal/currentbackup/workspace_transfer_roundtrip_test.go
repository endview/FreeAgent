package currentbackup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestWorkspaceTransferDecisionRoundTrip(
	t *testing.T,
) {
	tests := []struct {
		name          string
		outcome       compositeBackupReviewerOutcome
		wantPayloads  int
		wantEnvelopes int
	}{
		{
			name:          "approve",
			outcome:       compositeBackupDecisionApprove,
			wantPayloads:  1,
			wantEnvelopes: 2,
		},
		{
			name:          "repair",
			outcome:       compositeBackupDecisionRepair,
			wantPayloads:  2,
			wantEnvelopes: 4,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCompositeBackupFixtureWithReviewerAndTransfer(
				t,
				compositeBackupComplete,
				false,
				test.outcome,
				true,
			)
			assertWorkspaceTransferContentCounts(
				t,
				fixture.base.databasePath,
				test.wantPayloads,
				test.wantEnvelopes,
			)
			restored := roundTripWorkspaceTransferFixture(t, fixture, test.name)
			assertWorkspaceTransferContentCounts(
				t,
				restored,
				test.wantPayloads,
				test.wantEnvelopes,
			)
		})
	}
}

func TestWorkspaceTransferUnknownHasRequestAndNoResultRoundTrip(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newCompositeBackupFixtureWithReviewerAndTransfer(
		t,
		compositeBackupDecisionAdmission,
		false,
		compositeBackupDecisionDormant,
		true,
	)
	store, err := currentstore.OpenExistingCurrentStore(
		ctx,
		fixture.base.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared := prepareProfiledExampleSeed(t, exampleSeedPath(t))
	registry := newCompositeBackupRegistryWithInvoker(
		t,
		prepared,
		&compositeBackupFixedInvoker{
			provider: compositeBackupProvider(prepared),
			outcome:  modulehost.InvocationUnknown,
		},
	)
	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	result, runErr := loop.Run(ctx, loopapi.RunInput{
		RunID:       fixture.childRunIDs[0],
		MaxSteps:    1,
		MaxDuration: time.Minute,
	})
	closeErr := store.Close()
	if err := errors.Join(runErr, closeErr); err != nil {
		t.Fatalf("run UNKNOWN transfer Child: result=%+v err=%v", result, err)
	}
	if result.Disposition != loopapi.DispositionWaitingReconciliation {
		t.Fatalf("UNKNOWN transfer Child result=%+v", result)
	}
	assertWorkspaceTransferContentCounts(
		t,
		fixture.base.databasePath,
		1,
		1,
	)
	assertWorkspaceTransferEnvelopeDirections(
		t,
		fixture.base.databasePath,
		1,
		0,
	)
	restored := roundTripWorkspaceTransferFixture(t, fixture, "unknown")
	assertWorkspaceTransferContentCounts(t, restored, 1, 1)
	assertWorkspaceTransferEnvelopeDirections(t, restored, 1, 0)
}

func TestWorkspaceTransferPendingBackupRestoreRecoveryNeverReplays(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newCompositeBackupFixtureWithReviewerAndTransfer(
		t,
		compositeBackupDecisionAdmission,
		false,
		compositeBackupDecisionDormant,
		true,
	)
	fixture.pendingChildID = fixture.childRunIDs[0]
	fixture.pendingAttemptID = "attempt-transfer-backup-pending"
	store, err := currentstore.OpenExistingCurrentStore(
		ctx,
		fixture.base.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	beginInput, begin := beginWorkspaceTransferPendingChild(
		t,
		store,
		fixture.pendingChildID,
		fixture.pendingAttemptID,
	)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if begin.Attempt.AttemptID != fixture.pendingAttemptID ||
		begin.Attempt.State != corecontract.ModelAttemptPending {
		t.Fatalf("cross-Workspace PENDING Attempt=%+v", begin.Attempt)
	}
	// Simulate the persisted lease after its wall-clock TTL elapsed so the
	// restored process can take a new fencing epoch without reusing the old
	// worker token.
	mustCompositeExec(t, fixture.base.databasePath, `
		UPDATE loop_frames SET lease_expiry=1 WHERE run_id=?
	`, fixture.pendingChildID)
	assertWorkspaceTransferContentCounts(
		t,
		fixture.base.databasePath,
		1,
		1,
	)
	assertWorkspaceTransferEnvelopeDirections(
		t,
		fixture.base.databasePath,
		1,
		0,
	)

	restored := roundTripWorkspaceTransferFixture(
		t,
		fixture,
		"pending-restart",
	)
	assertWorkspaceTransferContentCounts(t, restored, 1, 1)
	assertWorkspaceTransferEnvelopeDirections(t, restored, 1, 0)

	reopened, err := currentstore.OpenExistingCurrentStore(ctx, restored)
	if err != nil {
		t.Fatalf("reopen restored transfer PENDING Store: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = reopened.Close()
		}
	}()
	scan, err := reopened.ScanStartupRecovery(ctx)
	if err != nil {
		t.Fatalf("ScanStartupRecovery(restored transfer): %v", err)
	}
	pending, found := findStartupRecoveryRun(scan, fixture.pendingChildID)
	if !found || pending.UnsettledAttemptID != fixture.pendingAttemptID ||
		pending.UnsettledAttemptState != corecontract.ModelAttemptPending {
		t.Fatalf("restored transfer PENDING projection=%+v found=%v", pending, found)
	}
	lease, err := reopened.AcquireRunLease(
		ctx,
		currentstore.AcquireRunLeaseInput{
			RunID:                 fixture.pendingChildID,
			OwnerID:               "workspace-transfer-backup-recovery",
			ExpectedRunRevision:   readCompositeRunRevision(t, restored, fixture.pendingChildID),
			ExpectedFrameRevision: pending.FrameRevision,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("acquire restored transfer PENDING lease: %v", err)
	}
	recovered, err := reopened.RecoverStartupPending(
		ctx,
		currentstore.RecoverStartupPendingInput{
			Lease:         lease,
			AttemptKind:   corecontract.AttemptKindModel,
			AttemptID:     fixture.pendingAttemptID,
			UnknownReason: "CRASH_RECOVERY_TRANSFER",
		},
	)
	if err != nil {
		t.Fatalf("RecoverStartupPending(transfer Child): %v", err)
	}

	beginInput.Lease = recovered.Lease
	retry, err := reopened.BeginModelDispatch(ctx, beginInput)
	if err != nil || retry.Created || retry.InvokeAllowed ||
		retry.ConsumeModelInvocationPermit() ||
		retry.Attempt.AttemptID != fixture.pendingAttemptID ||
		retry.Attempt.State != corecontract.ModelAttemptUnknown {
		t.Fatalf("restored transfer exact Begin retry=%+v error=%v", retry, err)
	}
	if err := reopened.ReleaseRunLease(ctx, retry.Lease); err != nil {
		t.Fatalf("release recovered transfer lease: %v", err)
	}

	scan, err = reopened.ScanStartupRecovery(ctx)
	if err != nil {
		t.Fatalf("ScanStartupRecovery(after transfer recovery): %v", err)
	}
	unknown, found := findStartupRecoveryRun(scan, fixture.pendingChildID)
	if !found || unknown.UnsettledAttemptID != fixture.pendingAttemptID ||
		unknown.UnsettledAttemptState != corecontract.ModelAttemptUnknown {
		t.Fatalf("restored transfer UNKNOWN projection=%+v found=%v", unknown, found)
	}

	prepared := prepareProfiledExampleSeed(t, exampleSeedPath(t))
	guard := &workspaceTransferReplayGuardInvoker{}
	registry := newCompositeBackupRegistryWithInvoker(t, prepared, guard)
	loop, err := coreloop.NewUniversalLoop(reopened, registry)
	if err != nil {
		t.Fatal(err)
	}
	loopResult, runErr := loop.Run(ctx, loopapi.RunInput{
		RunID:       fixture.pendingChildID,
		MaxSteps:    1,
		MaxDuration: time.Minute,
	})
	if runErr != nil ||
		loopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		guard.calls != 0 {
		t.Fatalf(
			"restored transfer Loop replayed Provider: result=%+v calls=%d error=%v",
			loopResult,
			guard.calls,
			runErr,
		)
	}
	record, err := reopened.GetModelDispatchRecord(ctx, fixture.pendingAttemptID)
	if err != nil || record.Attempt.AttemptID != fixture.pendingAttemptID ||
		record.Attempt.State != corecontract.ModelAttemptUnknown ||
		record.Attempt.ResultRef != "" {
		t.Fatalf("recovered transfer Attempt=%+v error=%v", record, err)
	}
	assertWorkspaceTransferAttemptCount(
		t,
		restored,
		fixture.pendingChildID,
		1,
	)
	assertWorkspaceTransferContentCounts(t, restored, 1, 1)
	assertWorkspaceTransferEnvelopeDirections(t, restored, 1, 0)
	if got, want := countCompositeFamilyRuns(
		t,
		restored,
		fixture.rootRunID,
	), 2*len(fixture.childRunIDs)+3; got != want {
		t.Fatalf("restored Decision family Run count=%d want=%d", got, want)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	if err := VerifyCurrentStoreSemanticClosure(ctx, restored); err != nil {
		t.Fatalf("verify recovered transfer UNKNOWN Store: %v", err)
	}
}

func TestWorkspaceTransferConsumerRejectsReorderedResultEvidence(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newCompositeBackupFixtureWithReviewerAndTransfer(
		t,
		compositeBackupComplete,
		false,
		compositeBackupDecisionRepair,
		true,
	)
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	states := loadWorkspaceTransferSemanticStates(t, database)
	root := states[fixture.rootRunID]
	if root == nil {
		t.Fatalf("Workspace transfer root %q is absent", fixture.rootRunID)
	}
	authorities, err := inspectWorkspaceTransferFamilyAuthorityV1(
		ctx,
		database,
		states,
		root,
	)
	if err != nil {
		t.Fatalf("inspect Workspace transfer authority: %v", err)
	}

	initialCompilation := readWorkspaceTransferCompilation(
		t,
		fixture.base.databasePath,
		fixture.reviewerRunID,
	)
	repairCompilation := readWorkspaceTransferCompilation(
		t,
		fixture.base.databasePath,
		fixture.repairReviewerRunID,
	)
	initialResult := findWorkspaceTransferChildResultEvidence(
		t,
		initialCompilation,
		fixture.childRunIDs[0],
	)
	repairResult := findWorkspaceTransferChildResultEvidence(
		t,
		repairCompilation,
		fixture.repairChildRunIDs[0],
	)
	initialTransfer := singleWorkspaceTransferEvidence(t, initialCompilation)
	repairTransfer := singleWorkspaceTransferEvidence(t, repairCompilation)
	if initialTransfer.Direction != corecontract.WorkspaceTransferDirectionResultV1 ||
		repairTransfer.Direction != corecontract.WorkspaceTransferDirectionResultV1 {
		t.Fatalf(
			"consumer transfer directions initial/repair=%q/%q",
			initialTransfer.Direction,
			repairTransfer.Direction,
		)
	}

	// The Collaboration verifier keeps the two rounds separate. This focused
	// Transfer-gate test deliberately combines two authentic family RESULTs so
	// both consumer roles exercise the per-index evidence comparison.
	for _, role := range []corecontract.CompositeRunRoleV1{
		corecontract.CompositeRunRoleRootV1,
		corecontract.CompositeRunRoleReviewerV1,
	} {
		role := role
		t.Run(string(role), func(t *testing.T) {
			consumerFor := func(
				transfers []corecontract.WorkspaceTransferEvidenceV1,
			) *coreRunSemanticState {
				attempt := &coreModelAttemptState{
					attemptID: "attempt-order-" + string(role),
					compilation: &corecontract.ContextCompilationV1{
						Composite: &corecontract.CompositeContextEvidenceV1{
							Role: role,
							ChildResults: []corecontract.CompositeChildResultEvidenceV1{
								initialResult,
								repairResult,
							},
						},
						WorkspaceTransfers: transfers,
					},
				}
				return &coreRunSemanticState{
					row: coreRunRow{runID: "consumer-order-" + string(role)},
					attempts: map[string]*coreModelAttemptState{
						attempt.attemptID: attempt,
					},
				}
			}
			ordered := []corecontract.WorkspaceTransferEvidenceV1{
				initialTransfer,
				repairTransfer,
			}
			if err := inspectWorkspaceTransferConsumerV1(
				ctx,
				database,
				consumerFor(ordered),
				root,
				authorities,
				newWorkspaceTransferExpectedContentForTest(),
			); err != nil {
				t.Fatalf("ordered %s RESULT evidence: %v", role, err)
			}
			reordered := []corecontract.WorkspaceTransferEvidenceV1{
				repairTransfer,
				initialTransfer,
			}
			if err := inspectWorkspaceTransferConsumerV1(
				ctx,
				database,
				consumerFor(reordered),
				root,
				authorities,
				newWorkspaceTransferExpectedContentForTest(),
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf(
					"reordered %s RESULT evidence error=%v want ErrIntegrity",
					role,
					err,
				)
			}
		})
	}
}

func TestWorkspaceTransferSemanticClosureRejectsMissingAndTamperedRecords(
	t *testing.T,
) {
	t.Run("missing result envelope", func(t *testing.T) {
		fixture := newCompositeBackupFixtureWithReviewerAndTransfer(
			t,
			compositeBackupComplete,
			false,
			compositeBackupDecisionApprove,
			true,
		)
		resultRef := findWorkspaceTransferEnvelopeRef(
			t,
			fixture.base.databasePath,
			corecontract.WorkspaceTransferDirectionResultV1,
			fixture.childRunIDs[0],
		)
		mustCompositeClosedFileExec(
			t,
			fixture.base.databasePath,
			[]string{"content_records_reject_delete"},
			`DELETE FROM content_records WHERE content_digest=?`,
			resultRef,
		)
		if err := VerifyCurrentStoreSemanticClosure(
			context.Background(),
			fixture.base.databasePath,
		); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("missing RESULT envelope error=%v want ErrIntegrity", err)
		}
	})

	t.Run("tampered request evidence digest", func(t *testing.T) {
		fixture := newCompositeBackupFixtureWithReviewerAndTransfer(
			t,
			compositeBackupComplete,
			false,
			compositeBackupDecisionApprove,
			true,
		)
		tamperWorkspaceTransferEvidenceDigest(t, fixture)
		if err := VerifyCurrentStoreSemanticClosure(
			context.Background(),
			fixture.base.databasePath,
		); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("tampered REQUEST evidence error=%v want ErrIntegrity", err)
		}
	})

	t.Run("tampered repair request lineage", func(t *testing.T) {
		fixture := newCompositeBackupFixtureWithReviewerAndTransfer(
			t,
			compositeBackupComplete,
			false,
			compositeBackupDecisionRepair,
			true,
		)
		initial := readWorkspaceTransferEvidence(
			t,
			fixture.base.databasePath,
			fixture.childRunIDs[0],
		)
		repair := readWorkspaceTransferEvidence(
			t,
			fixture.base.databasePath,
			fixture.repairChildRunIDs[0],
		)
		initialSummary := readWorkspaceTransferTaskSummary(
			t,
			fixture.base.databasePath,
			initial.PayloadRef,
		)
		repairSummary := readWorkspaceTransferTaskSummary(
			t,
			fixture.base.databasePath,
			repair.PayloadRef,
		)
		if initialSummary.RepairRound != 0 ||
			initialSummary.PreviousSetDigest != "" ||
			initialSummary.VerdictRef != "" ||
			repairSummary.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
			repairSummary.PreviousSetDigest == "" || repairSummary.VerdictRef == "" {
			t.Fatalf(
				"unexpected initial/repair task-summary lineage: initial=%+v repair=%+v",
				initialSummary,
				repairSummary,
			)
		}
		rewriteWorkspaceTransferEvidence(
			t,
			fixture.base.databasePath,
			fixture.repairChildRunIDs[0],
			[]corecontract.WorkspaceTransferEvidenceV1{initial},
		)
		if err := VerifyCurrentStoreSemanticClosure(
			context.Background(),
			fixture.base.databasePath,
		); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("tampered repair REQUEST lineage error=%v want ErrIntegrity", err)
		}
	})

	t.Run("orphan payload", func(t *testing.T) {
		fixture := newCompositeBackupFixtureWithReviewerAndTransfer(
			t,
			compositeBackupComplete,
			false,
			compositeBackupDecisionApprove,
			true,
		)
		database, err := sql.Open("sqlite", fixture.base.databasePath)
		if err != nil {
			t.Fatal(err)
		}
		insertCompositeContent(
			t,
			database,
			currentstore.ContentWorkspaceTransferPayload,
			[]byte(`{"orphan":true}`),
		)
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if err := VerifyCurrentStoreSemanticClosure(
			context.Background(),
			fixture.base.databasePath,
		); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("orphan transfer payload error=%v want ErrIntegrity", err)
		}
	})

	t.Run("orphan envelope", func(t *testing.T) {
		fixture := newCompositeBackupFixtureWithReviewerAndTransfer(
			t,
			compositeBackupComplete,
			false,
			compositeBackupDecisionApprove,
			true,
		)
		database, err := sql.Open("sqlite", fixture.base.databasePath)
		if err != nil {
			t.Fatal(err)
		}
		insertCompositeContent(
			t,
			database,
			currentstore.ContentWorkspaceTransferEnvelope,
			[]byte(`{"orphan":true}`),
		)
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		if err := VerifyCurrentStoreSemanticClosure(
			context.Background(),
			fixture.base.databasePath,
		); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("orphan transfer envelope error=%v want ErrIntegrity", err)
		}
	})
}

func roundTripWorkspaceTransferFixture(
	t *testing.T,
	fixture compositeBackupFixture,
	label string,
) string {
	t.Helper()
	ctx := context.Background()
	if err := VerifyCurrentStoreSemanticClosure(
		ctx,
		fixture.base.databasePath,
	); err != nil {
		t.Fatalf("verify %s transfer source: %v", label, err)
	}
	bundle := filepath.Join(t.TempDir(), "workspace-transfer-"+label+".bundle")
	if _, err := CreateBundle(
		ctx,
		fixture.base.databasePath,
		fixture.base.artifactRoot,
		bundle,
		"currentbackup-workspace-transfer-"+label+"/v1",
	); err != nil {
		t.Fatalf("CreateBundle(%s transfer): %v", label, err)
	}
	if _, err := VerifyBundle(ctx, bundle); err != nil {
		t.Fatalf("VerifyBundle(%s transfer): %v", label, err)
	}
	restoreRoot := t.TempDir()
	restored := filepath.Join(restoreRoot, "restored.sqlite")
	if err := RestoreBundle(
		ctx,
		bundle,
		restored,
		filepath.Join(restoreRoot, "artifacts"),
	); err != nil {
		t.Fatalf("RestoreBundle(%s transfer): %v", label, err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restored); err != nil {
		t.Fatalf("verify %s transfer restore: %v", label, err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, restored)
	if err != nil {
		t.Fatalf("reopen %s transfer restore: %v", label, err)
	}
	resolved, found, resolveErr := store.ResolveCompositeRunFamily(
		ctx,
		fixture.tenantID,
		fixture.admissionKey,
		fixture.intentDigest,
	)
	closeErr := store.Close()
	if err := errors.Join(resolveErr, closeErr); err != nil {
		t.Fatalf("resolve %s restored transfer family: %v", label, err)
	}
	if !found || resolved.Created ||
		resolved.Parent.RunID != fixture.rootRunID ||
		len(resolved.Children) != len(fixture.childRunIDs) ||
		len(resolved.RepairChildren) != len(fixture.repairChildRunIDs) ||
		resolved.Reviewer == nil ||
		resolved.Reviewer.RunID != fixture.reviewerRunID ||
		resolved.RepairReviewer == nil ||
		resolved.RepairReviewer.RunID != fixture.repairReviewerRunID {
		t.Fatalf("restored %s transfer family does not reopen exactly: %+v", label, resolved)
	}
	for index := range fixture.childRunIDs {
		if resolved.Children[index].RunID != fixture.childRunIDs[index] {
			t.Fatalf(
				"restored %s transfer Child %d=%q want %q",
				label,
				index,
				resolved.Children[index].RunID,
				fixture.childRunIDs[index],
			)
		}
	}
	for index := range fixture.repairChildRunIDs {
		if resolved.RepairChildren[index].RunID !=
			fixture.repairChildRunIDs[index] {
			t.Fatalf(
				"restored %s repair transfer Child %d=%q want %q",
				label,
				index,
				resolved.RepairChildren[index].RunID,
				fixture.repairChildRunIDs[index],
			)
		}
	}
	return restored
}

func beginWorkspaceTransferPendingChild(
	t *testing.T,
	store *currentstore.Store,
	runID string,
	attemptID string,
) (currentstore.BeginModelDispatchInput, currentstore.BeginModelDispatchResult) {
	t.Helper()
	ctx := context.Background()
	lease, err := store.AcquireRunLease(ctx, currentstore.AcquireRunLeaseInput{
		RunID:                 runID,
		OwnerID:               "workspace-transfer-backup-pending",
		ExpectedRunRevision:   0,
		ExpectedFrameRevision: 0,
		TTL:                   time.Hour,
	})
	if err != nil {
		t.Fatalf("AcquireRunLease(pending transfer Child): %v", err)
	}
	run, err := store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop(pending transfer Child): %v", err)
	}
	if run.WorkspaceTransfer == nil || run.CompositeRoot == nil ||
		run.CompositeRoot.Composite == nil ||
		run.CompositeRoot.Composite.Plan == nil ||
		run.CompositeRoot.Composite.Plan.Decision == nil {
		t.Fatal("pending transfer Child lacks its Decision/Transfer root closure")
	}
	var modelBinding *moduleapi.PortBinding
	for _, plan := range run.Member.PortPlans {
		if plan.Port.Name == moduleapi.PortNameModelGenerate &&
			plan.Port.ExactVersion == moduleapi.PortVersionV1 &&
			len(plan.Bindings) == 1 {
			binding := plan.Bindings[0]
			modelBinding = &binding
			break
		}
	}
	if modelBinding == nil {
		t.Fatal("pending transfer Child has no exact model Binding")
	}
	modelConfigContent, found := run.FindContent(modelBinding.ConfigRef)
	if !found || modelConfigContent.Kind != currentstore.ContentConfig {
		t.Fatalf("pending transfer model config=%+v found=%v", modelConfigContent, found)
	}
	modelConfig, err := moduleapi.RestoreModelBindingConfigV1(
		modelConfigContent.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	contextPolicy, found := run.FindContent(run.Member.ContextPolicy.Digest)
	if !found || contextPolicy.Kind != currentstore.ContentPolicy {
		t.Fatalf("pending transfer ContextPolicy=%+v found=%v", contextPolicy, found)
	}
	var (
		modelProfileRef       *corecontract.ModelProfileRef
		modelProfileCanonical []byte
	)
	if run.Member.ModelProfile != nil {
		content, found := run.FindContent(run.Member.ModelProfile.Digest)
		if !found || content.Kind != currentstore.ContentConfig {
			t.Fatalf("pending transfer ModelProfile=%+v found=%v", content, found)
		}
		ref := *run.Member.ModelProfile
		modelProfileRef = &ref
		modelProfileCanonical = content.CanonicalBytes
	}
	transfer, err := currentstore.PrepareWorkspaceTransferRequestV1(run)
	if err != nil || transfer == nil {
		t.Fatalf("PrepareWorkspaceTransferRequestV1=%+v error=%v", transfer, err)
	}
	rootPlan := *run.CompositeRoot.Composite.Plan
	compiled, err := contextcompiler.CompileV1(contextcompiler.CompileInputV1{
		TenantID:                       run.Manifest.TenantID,
		WorkspaceScope:                 run.Member.Workspace,
		AgentScope:                     run.Member.Agent,
		ContextPolicyRef:               run.Member.ContextPolicy,
		ContextPolicyDocumentCanonical: contextPolicy.CanonicalBytes,
		ModelProfileRef:                modelProfileRef,
		ModelProfileCanonical:          modelProfileCanonical,
		ModelParameters:                modelConfig.Parameters,
		Actions:                        run.Member.Actions,
		TaskInputRef:                   run.Manifest.TaskInputRef,
		TaskInputCanonical: run.WorkspaceTransfer.RootTaskInput.
			CanonicalBytes,
		Composite: run.Manifest.Composite,
		CompositeCollaboration: &contextcompiler.CompositeCollaborationMaterialV1{
			FamilyDigest:     run.CompositeRoot.ManifestDigest,
			ParticipantRunID: run.RunID,
			RootPlan:         &rootPlan,
		},
		WorkspaceTransfers: []contextcompiler.WorkspaceTransferMaterialV1{
			transfer.ContextMaterialV1(),
		},
	})
	if err != nil {
		t.Fatalf("CompileV1(pending transfer Child): %v", err)
	}
	input := currentstore.BeginModelDispatchInput{
		Lease:                       lease,
		AttemptID:                   attemptID,
		LogicalStepID:               corecontract.PureChatModelLogicalStepIDV1,
		ContextCompilationCanonical: compiled.CompilationCanonical,
		RequestCanonical:            compiled.RequestCanonical,
		Deadline: run.Manifest.Deadline.
			Add(-time.Minute).
			Truncate(time.Microsecond),
	}
	begin, err := store.BeginModelDispatch(ctx, input)
	if err != nil || !begin.Created || !begin.InvokeAllowed ||
		begin.Attempt.State != corecontract.ModelAttemptPending {
		t.Fatalf("BeginModelDispatch(pending transfer Child)=%+v error=%v", begin, err)
	}
	return input, begin
}

type workspaceTransferReplayGuardInvoker struct {
	calls int
}

func (guard *workspaceTransferReplayGuardInvoker) Invoke(
	_ context.Context,
	_ modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	guard.calls++
	return modulehost.InvocationResult{}, errors.New(
		"workspace transfer UNKNOWN attempted semantic replay",
	)
}

func assertWorkspaceTransferAttemptCount(
	t *testing.T,
	databasePath string,
	runID string,
	want int,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	queryErr := database.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?
	`, runID).Scan(&count)
	closeErr := database.Close()
	if err := errors.Join(queryErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("transfer model Attempt count=%d want=%d", count, want)
	}
}

func assertWorkspaceTransferContentCounts(
	t *testing.T,
	databasePath string,
	wantPayloads int,
	wantEnvelopes int,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var payloads, envelopes int
	queryErr := database.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN kind='WORKSPACE_TRANSFER_PAYLOAD' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind='WORKSPACE_TRANSFER_ENVELOPE' THEN 1 ELSE 0 END), 0)
		FROM content_records
	`).Scan(&payloads, &envelopes)
	closeErr := database.Close()
	if err := errors.Join(queryErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if payloads != wantPayloads || envelopes != wantEnvelopes {
		t.Fatalf(
			"transfer content counts payload/envelope=%d/%d want %d/%d",
			payloads,
			envelopes,
			wantPayloads,
			wantEnvelopes,
		)
	}
}

func assertWorkspaceTransferEnvelopeDirections(
	t *testing.T,
	databasePath string,
	wantRequest int,
	wantResult int,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query(`
		SELECT canonical_bytes
		FROM content_records
		WHERE kind='WORKSPACE_TRANSFER_ENVELOPE'
	`)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	request, result := 0, 0
	for rows.Next() {
		var canonical []byte
		if err := rows.Scan(&canonical); err != nil {
			t.Fatal(err)
		}
		var envelope corecontract.WorkspaceTransferEnvelopeV1
		if err := json.Unmarshal(canonical, &envelope); err != nil {
			t.Fatal(err)
		}
		switch envelope.Direction {
		case corecontract.WorkspaceTransferDirectionRequestV1:
			request++
		case corecontract.WorkspaceTransferDirectionResultV1:
			result++
		}
	}
	closeErr := errors.Join(rows.Err(), rows.Close(), database.Close())
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if request != wantRequest || result != wantResult {
		t.Fatalf(
			"transfer directions request/result=%d/%d want %d/%d",
			request,
			result,
			wantRequest,
			wantResult,
		)
	}
}

func loadWorkspaceTransferSemanticStates(
	t *testing.T,
	database *sql.DB,
) map[string]*coreRunSemanticState {
	t.Helper()
	rows, err := database.Query(`
		SELECT
			run_id, tenant_id, workspace_id,
			conversation_id, conversation_turn_index,
			conversation_predecessor_run_id, admission_key,
			admission_intent_digest, parent_run_id,
			parent_manifest_digest, parent_slot_id,
			cancel_request_ref, state, disposition, revision
		FROM runs
		ORDER BY run_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	pending := make([]coreRunRow, 0)
	for rows.Next() {
		var row coreRunRow
		if err := rows.Scan(
			&row.runID,
			&row.tenantID,
			&row.workspaceID,
			&row.conversationID,
			&row.conversationTurnIndex,
			&row.conversationPrevious,
			&row.admissionKey,
			&row.admissionIntentDigest,
			&row.parentRunID,
			&row.parentManifestDigest,
			&row.parentSlotID,
			&row.cancelRequestRef,
			&row.state,
			&row.disposition,
			&row.revision,
		); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		pending = append(pending, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	states := make(map[string]*coreRunSemanticState, len(pending))
	for _, row := range pending {
		state, err := inspectOneCoreRun(
			context.Background(),
			database,
			row,
		)
		if err != nil {
			t.Fatalf("inspect Run %q for transfer test: %v", row.runID, err)
		}
		states[row.runID] = state
	}
	return states
}

func readWorkspaceTransferCompilation(
	t *testing.T,
	databasePath string,
	runID string,
) corecontract.ContextCompilationV1 {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var canonical []byte
	queryErr := database.QueryRow(`
		SELECT content.canonical_bytes
		FROM model_dispatch_attempts AS attempt
		JOIN content_records AS content
		  ON content.content_digest=attempt.context_compilation_ref
		WHERE attempt.run_id=?
	`, runID).Scan(&canonical)
	closeErr := database.Close()
	if err := errors.Join(queryErr, closeErr); err != nil {
		t.Fatal(err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatalf("restore transfer compilation for Run %q: %v", runID, err)
	}
	return compilation
}

func singleWorkspaceTransferEvidence(
	t *testing.T,
	compilation corecontract.ContextCompilationV1,
) corecontract.WorkspaceTransferEvidenceV1 {
	t.Helper()
	if len(compilation.WorkspaceTransfers) != 1 {
		t.Fatalf(
			"transfer evidence count=%d want 1",
			len(compilation.WorkspaceTransfers),
		)
	}
	return compilation.WorkspaceTransfers[0]
}

func findWorkspaceTransferChildResultEvidence(
	t *testing.T,
	compilation corecontract.ContextCompilationV1,
	runID string,
) corecontract.CompositeChildResultEvidenceV1 {
	t.Helper()
	if compilation.Composite == nil {
		t.Fatalf("consumer compilation for Child %q lacks Composite evidence", runID)
	}
	for _, result := range compilation.Composite.ChildResults {
		if result.RunID == runID {
			return result
		}
	}
	t.Fatalf("consumer compilation lacks Child result %q", runID)
	return corecontract.CompositeChildResultEvidenceV1{}
}

func newWorkspaceTransferExpectedContentForTest() *workspaceTransferExpectedContentV1 {
	return &workspaceTransferExpectedContentV1{
		envelopes: make(map[string]struct{}),
		payloads:  make(map[string]struct{}),
	}
}

func readWorkspaceTransferEvidence(
	t *testing.T,
	databasePath string,
	runID string,
) corecontract.WorkspaceTransferEvidenceV1 {
	t.Helper()
	return singleWorkspaceTransferEvidence(
		t,
		readWorkspaceTransferCompilation(t, databasePath, runID),
	)
}

func readWorkspaceTransferTaskSummary(
	t *testing.T,
	databasePath string,
	payloadRef string,
) corecontract.WorkspaceTaskSummaryV1 {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var canonical []byte
	queryErr := database.QueryRow(`
		SELECT canonical_bytes
		FROM content_records
		WHERE content_digest=? AND kind='WORKSPACE_TRANSFER_PAYLOAD'
	`, payloadRef).Scan(&canonical)
	closeErr := database.Close()
	if err := errors.Join(queryErr, closeErr); err != nil {
		t.Fatal(err)
	}
	summary, err := corecontract.RestoreWorkspaceTaskSummaryV1(canonical)
	if err != nil {
		t.Fatalf("restore Workspace task summary %q: %v", payloadRef, err)
	}
	return summary
}

func rewriteWorkspaceTransferEvidence(
	t *testing.T,
	databasePath string,
	runID string,
	evidence []corecontract.WorkspaceTransferEvidenceV1,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var compilationRef string
	if err := database.QueryRow(`
		SELECT context_compilation_ref
		FROM model_dispatch_attempts
		WHERE run_id=?
	`, runID).Scan(&compilationRef); err != nil {
		t.Fatal(err)
	}
	var canonical []byte
	if err := database.QueryRow(`
		SELECT canonical_bytes FROM content_records WHERE content_digest=?
	`, compilationRef).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatalf("restore transfer compilation for rewrite: %v", err)
	}
	compilation.WorkspaceTransfers = append(
		[]corecontract.WorkspaceTransferEvidenceV1(nil),
		evidence...,
	)
	_, rewritten, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil {
		t.Fatalf("freeze rewritten transfer compilation: %v", err)
	}
	rewrittenRef := insertCompositeContent(
		t,
		database,
		currentstore.ContentContextCompilation,
		rewritten,
	)
	result := execClosedFileTamperV1(t, database,
		[]string{"model_dispatch_attempts_observation_update_guard"}, `
		UPDATE model_dispatch_attempts
		SET context_compilation_ref=?
		WHERE run_id=?
	`, rewrittenRef, runID)
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("rewrite transfer compilation affected=%d error=%v", affected, err)
	}
}

func findWorkspaceTransferEnvelopeRef(
	t *testing.T,
	databasePath string,
	direction corecontract.WorkspaceTransferDirectionV1,
	childRunID string,
) string {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := database.Query(`
		SELECT content_digest, canonical_bytes
		FROM content_records
		WHERE kind='WORKSPACE_TRANSFER_ENVELOPE'
		ORDER BY content_digest
	`)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	found := ""
	for rows.Next() {
		var ref string
		var canonical []byte
		if err := rows.Scan(&ref, &canonical); err != nil {
			t.Fatal(err)
		}
		var envelope corecontract.WorkspaceTransferEnvelopeV1
		if err := json.Unmarshal(canonical, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Direction == direction && envelope.ChildRunID == childRunID {
			if found != "" {
				t.Fatalf("duplicate %s envelope for Child %q", direction, childRunID)
			}
			found = ref
		}
	}
	closeErr := errors.Join(rows.Err(), rows.Close(), database.Close())
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if found == "" {
		t.Fatalf("%s envelope for Child %q is absent", direction, childRunID)
	}
	return found
}

func tamperWorkspaceTransferEvidenceDigest(
	t *testing.T,
	fixture compositeBackupFixture,
) {
	t.Helper()
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var compilationRef string
	if err := database.QueryRow(`
		SELECT context_compilation_ref
		FROM model_dispatch_attempts
		WHERE run_id=?
	`, fixture.childRunIDs[0]).Scan(&compilationRef); err != nil {
		t.Fatal(err)
	}
	var canonical []byte
	if err := database.QueryRow(`
		SELECT canonical_bytes FROM content_records WHERE content_digest=?
	`, compilationRef).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(canonical)
	if err != nil || len(compilation.WorkspaceTransfers) != 1 {
		t.Fatalf("restore transfer compilation: %v", err)
	}
	tampered := strings.Repeat("f", 64)
	if tampered == compilation.WorkspaceTransfers[0].EnvelopeDigest {
		tampered = strings.Repeat("e", 64)
	}
	compilation.WorkspaceTransfers[0].EnvelopeDigest = tampered
	_, rewritten, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil {
		t.Fatal(err)
	}
	rewrittenRef := insertCompositeContent(
		t,
		database,
		currentstore.ContentContextCompilation,
		rewritten,
	)
	result := execClosedFileTamperV1(t, database,
		[]string{"model_dispatch_attempts_observation_update_guard"}, `
		UPDATE model_dispatch_attempts
		SET context_compilation_ref=?
		WHERE run_id=?
	`, rewrittenRef, fixture.childRunIDs[0])
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("tamper transfer compilation affected=%d error=%v", affected, err)
	}
}
