package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/loopapi"
)

func TestOpenProductionCompositionRecoversEveryRunBeforeChat(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize production data: %v", err)
	}
	preparing, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open preparation composition: %v", err)
	}

	runs := admitStartupRecoveryRuns(t, preparing.store)
	pendingID := beginStartupRecoveryPending(
		t,
		preparing.store,
		runs["pending"],
	)
	unknownID := beginStartupRecoveryPending(
		t,
		preparing.store,
		runs["unknown"],
	)
	unknownBefore := commitStartupRecoveryUnknown(
		t,
		preparing.store,
		runs["unknown"],
		unknownID,
	)
	if err := preparing.Close(); err != nil {
		t.Fatalf("close preparation composition: %v", err)
	}

	recovered, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open recovered composition: %v", err)
	}
	defer recovered.Close()

	scan, err := recovered.store.ScanStartupRecovery(context.Background())
	if err != nil {
		t.Fatalf("scan recovered composition: %v", err)
	}
	byRun := make(map[string]currentstore.StartupRecoveryRun, len(scan))
	for _, item := range scan {
		byRun[item.RunID] = item
	}
	if len(byRun) != 3 {
		t.Fatalf("startup recovery scanned %d Runs, want 3: %+v", len(byRun), scan)
	}
	ready := byRun[runs["ready"]]
	if ready.FrameStep != corecontract.InitialLoopStep ||
		ready.UnsettledAttemptID != "" ||
		ready.UnsettledAttemptState != "" {
		t.Fatalf("READY Run was executed during startup: %+v", ready)
	}
	// The compiled production adapter deterministically succeeds. Keeping the
	// original Attempt as MODEL_UNKNOWN proves startup used the PENDING recovery
	// branch and never invoked that adapter.
	recoveredPending := byRun[runs["pending"]]
	if recoveredPending.FrameStep !=
		corecontract.WaitingReconciliationLoopStep ||
		recoveredPending.UnsettledAttemptID != pendingID ||
		recoveredPending.UnsettledAttemptState !=
			corecontract.ModelAttemptUnknown {
		t.Fatalf("PENDING Run was not recovered in place: %+v", recoveredPending)
	}
	recoveredUnknown := byRun[runs["unknown"]]
	if recoveredUnknown.FrameStep !=
		corecontract.WaitingReconciliationLoopStep ||
		recoveredUnknown.UnsettledAttemptID != unknownID ||
		recoveredUnknown.UnsettledAttemptState !=
			corecontract.ModelAttemptUnknown {
		t.Fatalf("MODEL_UNKNOWN Run changed identity/state: %+v", recoveredUnknown)
	}
	unknownAfter, err := recovered.store.GetModelDispatchRecord(
		context.Background(),
		unknownID,
	)
	if err != nil {
		t.Fatalf("load preserved MODEL_UNKNOWN Attempt: %v", err)
	}
	if unknownAfter.Attempt.AttemptID != unknownBefore.Attempt.AttemptID ||
		unknownAfter.Attempt.State != unknownBefore.Attempt.State ||
		unknownAfter.Attempt.Revision != unknownBefore.Attempt.Revision {
		t.Fatalf(
			"startup changed existing MODEL_UNKNOWN Attempt:\nbefore=%+v\nafter=%+v",
			unknownBefore.Attempt,
			unknownAfter.Attempt,
		)
	}
	pendingAfter, err := recovered.store.GetModelDispatchRecord(
		context.Background(),
		pendingID,
	)
	if err != nil {
		t.Fatalf("load recovered PENDING Attempt: %v", err)
	}
	if pendingAfter.Attempt.State != corecontract.ModelAttemptUnknown ||
		pendingAfter.Attempt.UnknownReason !=
			"RECOVERED_PENDING_AFTER_CRASH" {
		t.Fatalf("recovered PENDING Attempt=%+v", pendingAfter.Attempt)
	}
}

func TestProductionStartupLeavesModelReadyAfterActionForLaterRun(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     actionExampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize Action startup fixture: %v", err)
	}
	preparing, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open Action startup fixture: %v", err)
	}
	readyOnly, err := newProductionChatService(
		preparing.store,
		leaveStartupRunReadyLoop{},
		preparing.registry,
	)
	if err != nil {
		_ = preparing.Close()
		t.Fatalf("create Action admission service: %v", err)
	}
	admitted, err := readyOnly.Chat(
		context.Background(),
		localchat.ChatInput{
			TenantID:    defaultTenantID,
			PrincipalID: "principal-action-ready-recovery",
			WorkspaceID: defaultWorkspaceID,
			AgentID:     defaultAgentID,
			ProfileID:   "action-chat",
			Message:     "leave the final model step for later",
			RequestID:   "action-ready-recovery-1",
			Deadline: time.Now().UTC().Add(time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if err != nil {
		_ = preparing.Close()
		t.Fatalf("admit Action startup Run: %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(
		preparing.store,
		preparing.registry,
	)
	if err != nil {
		_ = preparing.Close()
		t.Fatalf("create Action startup Loop: %v", err)
	}
	advanced, err := loop.Run(context.Background(), loopapi.RunInput{
		RunID:       admitted.RunID,
		MaxSteps:    2,
		MaxDuration: time.Minute,
	})
	if err != nil || advanced.Disposition != loopapi.DispositionYielded {
		_ = preparing.Close()
		t.Fatalf("advance to MODEL_READY_AFTER_ACTION=%+v error=%v", advanced, err)
	}
	before, err := loadStartupRunForTest(
		context.Background(),
		preparing.store,
		admitted.RunID,
	)
	if err != nil {
		_ = preparing.Close()
		t.Fatalf("load pre-restart Action closure: %v", err)
	}
	if before.Frame.Step != corecontract.ModelReadyAfterActionLoopStep ||
		len(before.ModelDispatches) != 1 ||
		len(before.ActionDispatches) != 1 ||
		before.ModelDispatches[0].Attempt.State !=
			corecontract.ModelAttemptSucceeded ||
		before.ActionDispatches[0].Attempt.State !=
			currentstore.ActionDispatchSucceeded {
		_ = preparing.Close()
		t.Fatalf("pre-restart Action closure=%+v", before)
	}
	modelID := before.ModelDispatches[0].Attempt.AttemptID
	actionID := before.ActionDispatches[0].Attempt.AttemptID
	actionOperationKey :=
		before.ActionDispatches[0].Attempt.LogicalOperationKey
	if err := preparing.Close(); err != nil {
		t.Fatalf("close Action startup fixture: %v", err)
	}

	recovered, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("restart MODEL_READY_AFTER_ACTION composition: %v", err)
	}
	defer recovered.Close()
	after, err := loadStartupRunForTest(
		context.Background(),
		recovered.store,
		admitted.RunID,
	)
	if err != nil {
		t.Fatalf("load post-restart Action closure: %v", err)
	}
	if after.Frame.Step != corecontract.ModelReadyAfterActionLoopStep ||
		len(after.ModelDispatches) != 1 ||
		len(after.ActionDispatches) != 1 ||
		after.ModelDispatches[0].Attempt.AttemptID != modelID ||
		after.ActionDispatches[0].Attempt.AttemptID != actionID ||
		after.ActionDispatches[0].Attempt.LogicalOperationKey !=
			actionOperationKey {
		t.Fatalf(
			"startup advanced or changed MODEL_READY_AFTER_ACTION:\nbefore=%+v\nafter=%+v",
			before.Frame,
			after.Frame,
		)
	}
}

func TestProductionStartupRecoveryClosesPendingWithoutArtifactInspection(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializeStartupRecoveryTestData(t, databasePath, artifactRoot)

	composition, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open preparation composition: %v", err)
	}
	defer composition.Close()
	runs := admitStartupRecoveryRuns(t, composition.store)
	attemptID := beginStartupRecoveryPending(
		t,
		composition.store,
		runs["pending"],
	)
	before, err := composition.store.GetModelDispatchRecord(
		context.Background(),
		attemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	offlineArtifacts := artifactRoot + ".offline"
	if err := os.Rename(artifactRoot, offlineArtifacts); err != nil {
		t.Fatalf("take artifacts offline: %v", err)
	}
	defer func() {
		if err := os.Rename(offlineArtifacts, artifactRoot); err != nil {
			t.Errorf("restore artifacts: %v", err)
		}
	}()
	if err := runProductionStartupRecovery(
		context.Background(),
		composition.store,
	); err != nil {
		t.Fatalf("Store-only startup recovery with artifacts offline: %v", err)
	}
	after, err := composition.store.GetModelDispatchRecord(
		context.Background(),
		attemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if after.Attempt.State != corecontract.ModelAttemptUnknown ||
		after.Attempt.Revision != before.Attempt.Revision+1 ||
		after.Attempt.UnknownReason != "RECOVERED_PENDING_AFTER_CRASH" {
		t.Fatalf(
			"Store-only recovery did not close PENDING in place:\nbefore=%+v\nafter=%+v",
			before.Attempt,
			after.Attempt,
		)
	}
	scan, err := composition.store.ScanStartupRecovery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range scan {
		if candidate.RunID != runs["pending"] {
			continue
		}
		if candidate.UnsettledAttemptID != attemptID ||
			candidate.UnsettledAttemptState !=
				corecontract.ModelAttemptUnknown ||
			candidate.FrameStep !=
				corecontract.WaitingReconciliationLoopStep {
			t.Fatalf("Store-only recovery projection: %+v", candidate)
		}
		return
	}
	t.Fatal("recovered Run disappeared from the safety ledger")
}

func TestProductionShutdownRecoveryUsesBoundedDetachedContext(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializeStartupRecoveryTestData(t, databasePath, artifactRoot)
	composition, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()
	runs := admitStartupRecoveryRuns(t, composition.store)
	attemptID := beginStartupRecoveryPending(
		t,
		composition.store,
		runs["pending"],
	)
	lifecycle, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runProductionShutdownRecovery(
		lifecycle,
		composition.store,
	); err != nil {
		t.Fatalf("shutdown recovery: %v", err)
	}
	recovered, err := composition.store.GetModelDispatchRecord(
		context.Background(),
		attemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Attempt.State != corecontract.ModelAttemptUnknown ||
		recovered.Attempt.UnknownReason != startupRecoveryUnknownReason {
		t.Fatalf("shutdown recovered Attempt=%+v", recovered.Attempt)
	}
}

func TestProductionShutdownRecoveryWithinUsesCoordinatorContext(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializeStartupRecoveryTestData(t, databasePath, artifactRoot)
	composition, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()
	runs := admitStartupRecoveryRuns(t, composition.store)
	attemptID := beginStartupRecoveryPending(
		t,
		composition.store,
		runs["pending"],
	)

	shutdownContext, cancel := context.WithCancel(context.Background())
	cancel()
	err = runProductionShutdownRecoveryWithinV1(
		shutdownContext,
		composition.store,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("coordinator-bound shutdown recovery error = %v", err)
	}
	unchanged, err := composition.store.GetModelDispatchRecord(
		context.Background(),
		attemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Attempt.State != corecontract.ModelAttemptPending {
		t.Fatalf(
			"canceled coordinator recovery changed Attempt=%+v",
			unchanged.Attempt,
		)
	}
}

func TestOpenProductionCompositionFailsClosedDuringStartupRecovery(t *testing.T) {
	t.Run("damaged recovery closure", func(t *testing.T) {
		root := t.TempDir()
		databasePath := filepath.Join(root, "current.sqlite")
		artifactRoot := filepath.Join(root, "artifacts")
		initializeStartupRecoveryTestData(t, databasePath, artifactRoot)

		composition, err := openProductionComposition(
			context.Background(),
			databasePath,
			artifactRoot,
			defaultTenantID,
		)
		if err != nil {
			t.Fatalf("open preparation composition: %v", err)
		}
		runs := admitStartupRecoveryRuns(t, composition.store)
		if err := composition.Close(); err != nil {
			t.Fatalf("close preparation composition: %v", err)
		}
		database, err := sql.Open("sqlite", databasePath)
		if err != nil {
			t.Fatalf("open database for corruption: %v", err)
		}
		_, updateErr := database.Exec(`
			UPDATE loop_frames
			SET continuation=x'7b7d'
			WHERE run_id=?
		`, runs["ready"])
		closeErr := database.Close()
		if err := errors.Join(updateErr, closeErr); err != nil {
			t.Fatalf("corrupt READY continuation: %v", err)
		}

		opened, err := openProductionComposition(
			context.Background(),
			databasePath,
			artifactRoot,
			defaultTenantID,
		)
		if err == nil || opened != nil {
			t.Fatalf("damaged closure opened service: composition=%v error=%v", opened, err)
		}
		store, openErr := currentstore.OpenExistingCurrentStore(
			context.Background(),
			databasePath,
		)
		if openErr == nil || store != nil {
			if store != nil {
				_ = store.Close()
			}
			t.Fatalf("damaged Store reopened after failed startup: store=%v error=%v", store, openErr)
		}
		if errors.Is(openErr, currentstore.ErrOwnerActive) {
			t.Fatalf("failed startup leaked Store owner: %v", openErr)
		}
	})

	t.Run("unexpired Run lease", func(t *testing.T) {
		root := t.TempDir()
		databasePath := filepath.Join(root, "current.sqlite")
		artifactRoot := filepath.Join(root, "artifacts")
		initializeStartupRecoveryTestData(t, databasePath, artifactRoot)

		composition, err := openProductionComposition(
			context.Background(),
			databasePath,
			artifactRoot,
			defaultTenantID,
		)
		if err != nil {
			t.Fatalf("open preparation composition: %v", err)
		}
		runs := admitStartupRecoveryRuns(t, composition.store)
		if _, err := composition.store.AcquireCurrentRunLease(
			context.Background(),
			currentstore.AcquireCurrentRunLeaseInput{
				RunID:   runs["ready"],
				OwnerID: "simulated-crashed-loop-owner",
				TTL:     time.Hour,
			},
		); err != nil {
			t.Fatalf("acquire simulated crash lease: %v", err)
		}
		if err := composition.Close(); err != nil {
			t.Fatalf("close preparation composition: %v", err)
		}

		opened, err := openProductionComposition(
			context.Background(),
			databasePath,
			artifactRoot,
			defaultTenantID,
		)
		if !errors.Is(err, currentstore.ErrRunLeaseUnavailable) || opened != nil {
			t.Fatalf("unexpired lease opened service: composition=%v error=%v", opened, err)
		}
	})
}

type leaveStartupRunReadyLoop struct{}

func (leaveStartupRunReadyLoop) Run(
	_ context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	return loopapi.RunResult{
		RunID:         input.RunID,
		Disposition:   loopapi.DispositionYielded,
		FrameRevision: 0,
		ReasonCode:    "test-ready",
	}, nil
}

func initializeStartupRecoveryTestData(
	t *testing.T,
	databasePath string,
	artifactRoot string,
) {
	t.Helper()
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize production data: %v", err)
	}
}

func admitStartupRecoveryRuns(
	t *testing.T,
	store *currentstore.Store,
) map[string]string {
	t.Helper()
	service, err := localchat.NewChatService(store, leaveStartupRunReadyLoop{})
	if err != nil {
		t.Fatalf("create ready-only ChatService: %v", err)
	}
	runs := make(map[string]string, 3)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	for _, name := range []string{"ready", "pending", "unknown"} {
		result, err := service.Chat(context.Background(), localchat.ChatInput{
			TenantID:    defaultTenantID,
			PrincipalID: defaultPrincipalID,
			WorkspaceID: defaultWorkspaceID,
			AgentID:     defaultAgentID,
			ProfileID:   defaultProfileID,
			Message:     "startup recovery " + name,
			RequestID:   "startup-recovery-" + name,
			Deadline:    deadline,
		})
		if err != nil {
			t.Fatalf("admit %s Run: %v", name, err)
		}
		runs[name] = result.RunID
	}
	return runs
}

func beginStartupRecoveryPending(
	t *testing.T,
	store *currentstore.Store,
	runID string,
) string {
	t.Helper()
	lease, err := store.AcquireCurrentRunLease(
		context.Background(),
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "prepare-startup-pending",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("acquire %s: %v", runID, err)
	}
	run, err := store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatalf("load %s: %v", runID, err)
	}
	_, request, err := coreloop.BuildPureChatRequestV1(run)
	if err != nil {
		t.Fatalf("build %s request: %v", runID, err)
	}
	operationKey, err := corecontract.ModelLogicalOperationKey(
		run.RunID,
		run.Member.MemberID,
		"s1.pure-chat.model-generate.1",
	)
	if err != nil {
		t.Fatalf("build %s operation key: %v", runID, err)
	}
	begin, err := store.BeginModelDispatch(
		context.Background(),
		currentstore.BeginModelDispatchInput{
			Lease:            lease,
			AttemptID:        "model-attempt-" + operationKey,
			LogicalStepID:    "s1.pure-chat.model-generate.1",
			RequestCanonical: request,
			Deadline: time.Now().UTC().Add(30 * time.Second).
				Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatalf("begin %s PENDING: %v", runID, err)
	}
	if !begin.Created || !begin.InvokeAllowed ||
		begin.Attempt.State != corecontract.ModelAttemptPending {
		t.Fatalf("%s did not create an invocable PENDING Attempt: %+v", runID, begin)
	}
	if err := store.ReleaseRunLease(context.Background(), begin.Lease); err != nil {
		t.Fatalf("release %s PENDING lease: %v", runID, err)
	}
	return begin.Attempt.AttemptID
}

func commitStartupRecoveryUnknown(
	t *testing.T,
	store *currentstore.Store,
	runID string,
	attemptID string,
) currentstore.ModelDispatchRecord {
	t.Helper()
	lease, err := store.AcquireCurrentRunLease(
		context.Background(),
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "prepare-startup-unknown",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("acquire %s for UNKNOWN: %v", runID, err)
	}
	record, err := store.GetModelDispatchRecord(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("load %s PENDING: %v", runID, err)
	}
	committed, err := store.CommitModelDispatchOutcome(
		context.Background(),
		currentstore.CommitModelDispatchOutcomeInput{
			Lease:                   lease,
			AttemptID:               record.Attempt.AttemptID,
			InvocationID:            record.Attempt.AttemptID,
			Provider:                record.Attempt.Binding.Provider,
			ExpectedAttemptRevision: record.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			UnknownReason:           "test preexisting unknown",
		},
	)
	if err != nil {
		t.Fatalf("commit %s UNKNOWN: %v", runID, err)
	}
	if err := store.ReleaseRunLease(context.Background(), committed.Lease); err != nil {
		t.Fatalf("release %s UNKNOWN lease: %v", runID, err)
	}
	return committed.Record
}

func loadStartupRunForTest(
	ctx context.Context,
	store *currentstore.Store,
	runID string,
) (currentstore.RunForLoop, error) {
	lease, err := store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "startup-recovery-test-loader",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		return currentstore.RunForLoop{}, err
	}
	run, loadErr := store.LoadRunForLoop(ctx, lease)
	releaseErr := store.ReleaseRunLease(ctx, lease)
	return run, errors.Join(loadErr, releaseErr)
}
