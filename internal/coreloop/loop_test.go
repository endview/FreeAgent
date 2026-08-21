package coreloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestUniversalLoopPureChatSuccessTerminalReadAndReentry(t *testing.T) {
	fixture := newLoopIntegrationFixture(t, "run-success", "answer me")
	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)

	first, err := loop.Run(context.Background(), integrationRunInput(fixture.runID))
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Disposition != loopapi.DispositionTerminated ||
		first.ReasonCode != reasonModelSucceeded ||
		invoker.callCount() != 1 {
		t.Fatalf("first result=%+v calls=%d", first, invoker.callCount())
	}
	terminal, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		fixture.runID,
	)
	if err != nil {
		t.Fatalf("GetTerminalRunResult: %v", err)
	}
	if terminal.State != corecontract.ModelAttemptSucceeded ||
		terminal.Output.AssistantText != fixture.taskText ||
		len(terminal.OutputCanonical) == 0 {
		t.Fatalf("terminal result=%+v", terminal)
	}

	second, err := loop.Run(context.Background(), integrationRunInput(fixture.runID))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Disposition != loopapi.DispositionTerminated ||
		second.ReasonCode != reasonModelSucceeded ||
		invoker.callCount() != 1 {
		t.Fatalf("second result=%+v calls=%d", second, invoker.callCount())
	}
}

func TestUniversalLoopRunClaimedConsumesFairSchedulerLease(t *testing.T) {
	assertPersistenceBudgetsAndLeaseWindow(t)
	fixture := newLoopIntegrationFixture(
		t,
		"run-scheduler-claimed",
		"scheduled answer",
	)
	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)
	maximumInput := integrationRunInput(fixture.runID)
	maximumInput.MaxDuration = time.Duration(1<<63-1) - RunLeaseTailGrace
	if err := loop.validateRunInput(context.Background(), maximumInput); err != nil {
		t.Fatalf("maximum non-overflowing Run input: %v", err)
	}
	overflowingInput := maximumInput
	overflowingInput.MaxDuration++
	if err := loop.validateRunInput(context.Background(), overflowingInput); !errors.Is(err, ErrInvalidUniversalLoop) {
		t.Fatalf("overflowing Run input error=%v", err)
	}
	claim, err := fixture.store.ClaimFairRun(
		context.Background(),
		currentstore.ClaimFairRunInput{
			TenantID: "tenant-loop",
			OwnerID:  "scheduler-claimed-owner",
			TTL:      integrationRunInput(fixture.runID).MaxDuration + RunLeaseTailGrace,
			Limits: currentstore.FairSchedulerLimits{
				GlobalWorkers:         4,
				MaxActivePerWorkspace: 2,
				MaxActivePerFamily:    2,
			},
		},
	)
	if err != nil || claim.Status != currentstore.FairSchedulerClaimed ||
		claim.Lease.RunID != fixture.runID {
		t.Fatalf("ClaimFairRun=%+v error=%v", claim, err)
	}

	result, err := loop.RunClaimed(
		context.Background(),
		integrationRunInput(fixture.runID),
		claim.Lease,
	)
	if err != nil {
		t.Fatalf("RunClaimed: %v", err)
	}
	if result.Disposition != loopapi.DispositionTerminated ||
		result.ReasonCode != reasonModelSucceeded ||
		invoker.callCount() != 1 {
		t.Fatalf("RunClaimed result=%+v calls=%d", result, invoker.callCount())
	}
	state, found, err := fixture.store.GetWorkspaceSchedulerState(
		context.Background(),
		"tenant-loop",
		"workspace-loop",
	)
	if err != nil || !found || state.ServedUnits != 1 || state.Revision != 1 {
		t.Fatalf("Scheduler state=(%+v,%v,%v)", state, found, err)
	}
}

func assertPersistenceBudgetsAndLeaseWindow(t *testing.T) {
	t.Helper()
	if persistenceGrace != 5*time.Second ||
		compositeDecisionPersistenceGrace != 15*time.Second ||
		compositeDecisionFinalizationMargin != 5*time.Second ||
		compositeDecisionFinalizationReserve != 15*time.Second ||
		RunLeaseTailGrace != 30*time.Second ||
		leaseTailGrace != RunLeaseTailGrace {
		t.Fatalf(
			"persistence budgets generic=%s composite=%s margin=%s reserve=%s tail=%s alias=%s",
			persistenceGrace,
			compositeDecisionPersistenceGrace,
			compositeDecisionFinalizationMargin,
			compositeDecisionFinalizationReserve,
			RunLeaseTailGrace,
			leaseTailGrace,
		)
	}

	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()
	generic, cancelGeneric := persistenceContext(parent)
	defer cancelGeneric()
	composite, cancelComposite, err := compositeDecisionPersistenceContext(
		parent,
		time.Now().Add(RunLeaseTailGrace+time.Second),
	)
	if err != nil {
		t.Fatalf("compositeDecisionPersistenceContext: %v", err)
	}
	defer cancelComposite()
	if generic.Err() != nil || composite.Err() != nil {
		t.Fatalf(
			"detached persistence inherited cancellation: generic=%v composite=%v",
			generic.Err(),
			composite.Err(),
		)
	}
	genericDeadline, genericOK := generic.Deadline()
	compositeDeadline, compositeOK := composite.Deadline()
	if !genericOK || !compositeOK {
		t.Fatal("detached persistence deadlines are absent")
	}
	now := time.Now()
	if remaining := genericDeadline.Sub(now); remaining <= 0 ||
		remaining > persistenceGrace {
		t.Fatalf("generic persistence remaining=%s", remaining)
	}
	if remaining := compositeDeadline.Sub(now); remaining <= 0 ||
		remaining > compositeDecisionPersistenceGrace {
		t.Fatalf("Composite decision persistence remaining=%s", remaining)
	}
	t0 := time.Date(2026, 8, 21, 11, 0, 0, 0, time.UTC)
	clampedExpiry := t0.Add(24 * time.Second)
	clampedDeadline, err := compositeDecisionPersistenceDeadline(t0, clampedExpiry)
	if err != nil || clampedDeadline != t0.Add(9*time.Second) {
		t.Fatalf(
			"clamped composite deadline=(%s,%v) want=%s",
			clampedDeadline,
			err,
			t0.Add(9*time.Second),
		)
	}
	virtualCompletion := t0.Add(6 * time.Second)
	if !virtualCompletion.After(t0.Add(persistenceGrace)) ||
		!virtualCompletion.Before(clampedDeadline) {
		t.Fatalf("virtual composite completion=%s deadline=%s", virtualCompletion, clampedDeadline)
	}
	fullDeadline, err := compositeDecisionPersistenceDeadline(
		t0,
		t0.Add(RunLeaseTailGrace+time.Second),
	)
	if err != nil || fullDeadline != t0.Add(compositeDecisionPersistenceGrace) {
		t.Fatalf("full composite deadline=(%s,%v)", fullDeadline, err)
	}
	exhaustedDeadline, err := compositeDecisionPersistenceDeadline(
		t0,
		t0.Add(compositeDecisionFinalizationReserve),
	)
	if !exhaustedDeadline.IsZero() || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf(
			"exhausted composite deadline=(%s,%v)",
			exhaustedDeadline,
			err,
		)
	}
	synctest.Test(t, func(t *testing.T) {
		type persistenceValueKey struct{}
		const retainedValue = "retained"
		start := time.Now()
		parent, cancelParent := context.WithCancel(
			context.WithValue(t.Context(), persistenceValueKey{}, retainedValue),
		)
		composite, cancelComposite, err := compositeDecisionPersistenceContext(
			parent,
			start.Add(24*time.Second),
		)
		if err != nil {
			t.Fatalf("synctest composite context: %v", err)
		}
		defer cancelComposite()
		cancelParent()
		synctest.Wait()
		if composite.Err() != nil ||
			composite.Value(persistenceValueKey{}) != retainedValue {
			t.Fatalf(
				"detached composite after parent cancel=(%v,%v)",
				composite.Err(),
				composite.Value(persistenceValueKey{}),
			)
		}
		time.Sleep(6 * time.Second)
		synctest.Wait()
		if composite.Err() != nil {
			t.Fatalf("composite expired at virtual 6s: %v", composite.Err())
		}
		time.Sleep(3 * time.Second)
		synctest.Wait()
		if !errors.Is(composite.Err(), context.DeadlineExceeded) {
			t.Fatalf("composite at virtual 9s error=%v", composite.Err())
		}
	})

	base := t0
	maxDuration := 30 * time.Second
	leaseExpiry := base.Add(maxDuration + RunLeaseTailGrace)
	if got := narrowedModelDeadline(base.Add(time.Hour), leaseExpiry); got !=
		base.Add(maxDuration) {
		t.Fatalf(
			"narrowed deadline=%s want=%s",
			got,
			base.Add(maxDuration),
		)
	}
	maximumRunDuration := time.Duration(1<<63-1) - RunLeaseTailGrace
	if maximumRunDuration+RunLeaseTailGrace != time.Duration(1<<63-1) {
		t.Fatalf("maximum Run duration overflows its lease")
	}
}

func TestUniversalLoopRunClaimedRejectsMismatchAndReleasesLease(t *testing.T) {
	fixture := newLoopIntegrationFixture(
		t,
		"run-scheduler-mismatch",
		"must not run",
	)
	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)
	lease, err := fixture.store.AcquireCurrentRunLease(
		context.Background(),
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   fixture.runID,
			OwnerID: "scheduler-mismatch-owner",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input := integrationRunInput("another-run")
	if _, err := loop.RunClaimed(
		context.Background(),
		input,
		lease,
	); !errors.Is(err, ErrInvalidUniversalLoop) {
		t.Fatalf("RunClaimed mismatch error=%v", err)
	}
	if invoker.callCount() != 0 {
		t.Fatalf("mismatched RunClaimed invoked model %d times", invoker.callCount())
	}
	verify, err := fixture.store.AcquireCurrentRunLease(
		context.Background(),
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   fixture.runID,
			OwnerID: "scheduler-mismatch-verifier",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("mismatched RunClaimed leaked lease: %v", err)
	}
	if err := fixture.store.ReleaseRunLease(context.Background(), verify); err != nil {
		t.Fatal(err)
	}
}

func TestUniversalLoopOrdinaryCancellationCreatesNoModelAttempt(t *testing.T) {
	fixture := newLoopIntegrationFixture(t, "run-canceled", "do not invoke")
	ctx := context.Background()
	lease, err := fixture.store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   fixture.runID,
			OwnerID: "cancellation-manifest-reader",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixture.store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ReleaseRunLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	_, canonical, err := corecontract.NewRunCancellationRequestV1(
		corecontract.RunCancellationRequestV1{
			SchemaVersion:      corecontract.RunCancellationRequestSchemaVersionV1,
			RootRunID:          run.RunID,
			RootManifestDigest: run.Manifest.ManifestDigest,
			Scope:              corecontract.CancellationScopeRunV1,
			ReasonCode:         corecontract.CancellationReasonUserRequestV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.RequestRunCancellation(
		ctx,
		currentstore.RequestRunCancellationInput{Canonical: canonical},
	); err != nil {
		t.Fatal(err)
	}
	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)
	result, err := loop.Run(ctx, integrationRunInput(fixture.runID))
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != loopapi.DispositionWaitingExternal ||
		result.ReasonCode != reasonRunCanceled ||
		invoker.callCount() != 0 {
		t.Fatalf("result=%+v calls=%d", result, invoker.callCount())
	}
	verifyLease, err := fixture.store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   fixture.runID,
			OwnerID: "cancellation-attempt-reader",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := fixture.store.LoadRunForLoop(ctx, verifyLease)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ModelDispatches) != 0 {
		t.Fatalf("canceled Run persisted %d model attempts", len(loaded.ModelDispatches))
	}
	if err := fixture.store.ReleaseRunLease(ctx, verifyLease); err != nil {
		t.Fatal(err)
	}
}

func TestRunPermitErrorResultClosesPostLoadCancellationWindow(t *testing.T) {
	fixture := newLoopIntegrationFixture(
		t,
		"run-cancel-after-load",
		"do not invoke after the stale read",
	)
	ctx := context.Background()
	lease, err := fixture.store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   fixture.runID,
			OwnerID: "post-load-cancel-owner",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixture.store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if run.CancellationRequest != nil {
		t.Fatal("pre-cancel Loop snapshot unexpectedly contains a latch")
	}
	prepared, err := preparePureChatRequestV1(run)
	if err != nil {
		t.Fatal(err)
	}
	_, canonical, err := corecontract.NewRunCancellationRequestV1(
		corecontract.RunCancellationRequestV1{
			SchemaVersion:      corecontract.RunCancellationRequestSchemaVersionV1,
			RootRunID:          run.RunID,
			RootManifestDigest: run.Manifest.ManifestDigest,
			Scope:              corecontract.CancellationScopeRunV1,
			ReasonCode:         corecontract.CancellationReasonUserRequestV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.RequestRunCancellation(
		ctx,
		currentstore.RequestRunCancellationInput{Canonical: canonical},
	); err != nil {
		t.Fatal(err)
	}
	_, permitErr := fixture.store.BeginModelDispatch(
		ctx,
		currentstore.BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   integrationAttemptID(t, run.RunID),
			LogicalStepID:               pureChatLogicalStepID,
			ContextCompilationCanonical: prepared.ContextCompilationCanonical,
			RequestCanonical:            prepared.RequestCanonical,
			Deadline: time.Now().UTC().Add(30 * time.Second).
				Truncate(time.Microsecond),
		},
	)
	if !errors.Is(permitErr, currentstore.ErrRunCanceled) {
		t.Fatalf("post-load Begin error=%v", permitErr)
	}
	result, returnedLease, normalizedErr := runPermitErrorResult(
		run,
		lease,
		permitErr,
	)
	if normalizedErr != nil || returnedLease != lease ||
		result.Disposition != loopapi.DispositionWaitingExternal ||
		result.ReasonCode != reasonRunCanceled {
		t.Fatalf(
			"normalized result=%+v lease=%+v error=%v",
			result,
			returnedLease,
			normalizedErr,
		)
	}
	if err := fixture.store.ReleaseRunLease(ctx, lease); err != nil {
		t.Fatal(err)
	}

	for _, role := range []corecontract.CompositeRunRoleV1{
		corecontract.CompositeRunRoleRootV1,
		corecontract.CompositeRunRoleChildV1,
	} {
		familyRun := currentstore.RunForLoop{
			RunID: "family-run-" + string(role),
			Manifest: corecontract.RunManifest{
				Composite: &corecontract.CompositeRunNodeV1{Role: role},
			},
		}
		familyLease := currentstore.RunLease{RunID: familyRun.RunID}
		familyResult, gotLease, gotErr := runPermitErrorResult(
			familyRun,
			familyLease,
			fmt.Errorf("permit: %w", currentstore.ErrRunCanceled),
		)
		if gotErr != nil || gotLease != familyLease ||
			familyResult.Disposition != loopapi.DispositionWaitingExternal ||
			familyResult.ReasonCode != reasonCompositeFamilyCanceled {
			t.Fatalf(
				"role %s normalized result=%+v lease=%+v error=%v",
				role,
				familyResult,
				gotLease,
				gotErr,
			)
		}
	}

	sentinel := errors.New("other permit failure")
	_, gotLease, gotErr := runPermitErrorResult(run, lease, sentinel)
	if gotLease != lease || !errors.Is(gotErr, sentinel) {
		t.Fatalf("non-cancellation error was changed: lease=%+v error=%v", gotLease, gotErr)
	}
}

func TestUniversalLoopPersistsWatermarkCompilationWithoutHistory(t *testing.T) {
	taskText := strings.Repeat("x", 1024)
	estimate := pureTaskRequestEstimate(t, taskText)
	fixture := newLoopIntegrationFixtureWithDeadline(
		t,
		"run-watermark-compilation",
		taskText,
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		contextPolicyForInputBudget(estimate+1),
	)
	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)

	result, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.runID),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Disposition != loopapi.DispositionTerminated ||
		result.ReasonCode != reasonModelSucceeded ||
		invoker.callCount() != 1 {
		t.Fatalf("result=%+v calls=%d", result, invoker.callCount())
	}
	record, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		integrationAttemptID(t, fixture.runID),
	)
	if err != nil {
		t.Fatalf("GetModelDispatchRecord: %v", err)
	}
	if record.Attempt.ContextCompilation == nil {
		t.Fatal("watermark request did not persist context compilation")
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		record.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("RestoreContextCompilationV1: %v", err)
	}
	if compilation.StopReason !=
		corecontract.ContextCompilationNoEligibleSummary ||
		compilation.Summary != nil || len(compilation.Drops) != 0 ||
		compilation.OriginalEstimateTokens != estimate ||
		compilation.FinalEstimateTokens != estimate {
		t.Fatalf("compilation=%+v", compilation)
	}
}

func TestUniversalLoopRejectsProtectedFullBudgetBeforeInvocation(t *testing.T) {
	taskText := strings.Repeat("x", 1024)
	estimate := pureTaskRequestEstimate(t, taskText)
	fixture := newLoopIntegrationFixtureWithDeadline(
		t,
		"run-protected-full-budget",
		taskText,
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		contextPolicyForInputBudget(estimate),
	)
	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)

	_, err = loop.Run(
		context.Background(),
		integrationRunInput(fixture.runID),
	)
	if !errors.Is(err, ErrInvalidPureChatRequest) ||
		!errors.Is(err, contextcompiler.ErrContextBudgetExceeded) {
		t.Fatalf("Run error=%v", err)
	}
	if invoker.callCount() != 0 {
		t.Fatalf("adapter calls=%d, want 0", invoker.callCount())
	}
	if _, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		integrationAttemptID(t, fixture.runID),
	); err == nil {
		t.Fatal("protected overflow persisted a model Attempt")
	}
}

func TestUniversalLoopExpiredDeadlineTerminatesWithoutInvocation(t *testing.T) {
	deadline := time.Now().
		UTC().
		Add(-time.Minute).
		Truncate(time.Microsecond)
	fixture := newLoopIntegrationFixtureWithDeadline(
		t,
		"run-expired-deadline",
		"must not invoke",
		deadline,
	)
	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)

	result, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.runID),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Disposition != loopapi.DispositionTerminated ||
		result.ReasonCode != reasonModelFailed ||
		invoker.callCount() != 0 {
		t.Fatalf("result=%+v calls=%d", result, invoker.callCount())
	}
	terminal, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		fixture.runID,
	)
	if err != nil {
		t.Fatalf("GetTerminalRunResult: %v", err)
	}
	if terminal.State != corecontract.ModelAttemptFailed ||
		terminal.ErrorClassification !=
			"DEADLINE_EXPIRED_BEFORE_DISPATCH" ||
		len(terminal.OutputCanonical) != 0 {
		t.Fatalf("terminal=%+v", terminal)
	}

	second, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.runID),
	)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Disposition != loopapi.DispositionTerminated ||
		second.ReasonCode != reasonModelFailed ||
		invoker.callCount() != 0 {
		t.Fatalf("second=%+v calls=%d", second, invoker.callCount())
	}
}

func TestUniversalLoopConcurrentRunInvokesModelOnce(t *testing.T) {
	fixture := newLoopIntegrationFixture(t, "run-concurrent", "one call")
	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{
		delegate: echo,
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	loop := newIntegrationLoop(t, fixture, invoker)

	firstDone := make(chan error, 1)
	go func() {
		_, runErr := loop.Run(
			context.Background(),
			integrationRunInput(fixture.runID),
		)
		firstDone <- runErr
	}()
	select {
	case <-invoker.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first Run did not enter adapter")
	}
	_, secondErr := loop.Run(
		context.Background(),
		integrationRunInput(fixture.runID),
	)
	if !errors.Is(secondErr, currentstore.ErrRunLeaseUnavailable) {
		t.Fatalf("concurrent Run error=%v", secondErr)
	}
	close(invoker.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if invoker.callCount() != 1 {
		t.Fatalf("adapter calls=%d, want 1", invoker.callCount())
	}
}

func TestUniversalLoopRecoversPersistedPendingWithoutReplay(t *testing.T) {
	fixture := newLoopIntegrationFixture(t, "run-pending", "do not replay")
	begin := beginIntegrationDispatch(t, fixture, 10*time.Minute, 5*time.Minute)
	// A crashed owner would eventually lose this lease by expiry. Releasing
	// only the lease fencing head gives the test the same recoverable PENDING
	// state without a wall-clock sleep; no model result is committed.
	if err := fixture.store.ReleaseRunLease(
		context.Background(),
		begin.Lease,
	); err != nil {
		t.Fatalf("release simulated crashed owner: %v", err)
	}

	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)
	result, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.runID),
	)
	if err != nil {
		t.Fatalf("recover Run: %v", err)
	}
	if result.Disposition != loopapi.DispositionWaitingReconciliation ||
		result.ReasonCode != reasonRecoveredPending ||
		invoker.callCount() != 0 {
		t.Fatalf("result=%+v calls=%d", result, invoker.callCount())
	}
}

func TestUniversalLoopCurrentRevocationCreatesNoAttemptOrAdapterCall(
	t *testing.T,
) {
	fixture := newLoopIntegrationFixture(
		t,
		"run-current-revoked",
		"must not call",
	)
	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)
	publishRevokedLoopCatalog(t, fixture.store)

	_, err = loop.Run(
		context.Background(),
		integrationRunInput(fixture.runID),
	)
	if !errors.Is(err, currentstore.ErrCurrentActivationDenied) {
		t.Fatalf("Run error=%v", err)
	}
	if invoker.callCount() != 0 {
		t.Fatalf("adapter calls=%d, want 0", invoker.callCount())
	}
	if _, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		integrationAttemptID(t, fixture.runID),
	); err == nil {
		t.Fatal("current revocation persisted a model Attempt")
	}
}

func TestUniversalLoopRevokedBetweenBeginAndHostFailsWithoutAdapter(
	t *testing.T,
) {
	fixture := newLoopIntegrationFixture(
		t,
		"run-revoked-after-begin",
		"must not reach adapter",
	)
	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)
	current := &recordingCurrentActivationChecker{
		err: currentstore.ErrCurrentActivationDenied,
	}
	loop.currentActivation = current

	result, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.runID),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Disposition != loopapi.DispositionTerminated ||
		result.ReasonCode != reasonModelFailed ||
		invoker.callCount() != 0 {
		t.Fatalf("result=%+v calls=%d", result, invoker.callCount())
	}
	if current.calls != 1 ||
		current.port.Name != moduleapi.PortNameModelGenerate ||
		current.provider != fixture.provider {
		t.Fatalf("current activation check=%+v", current)
	}
	terminal, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		fixture.runID,
	)
	if err != nil {
		t.Fatalf("GetTerminalRunResult: %v", err)
	}
	if terminal.State != corecontract.ModelAttemptFailed ||
		terminal.ErrorClassification !=
			currentActivationDeniedClassification {
		t.Fatalf("terminal=%+v", terminal)
	}
}

func TestUniversalLoopRecoversPendingAfterRevocationWithoutReplay(
	t *testing.T,
) {
	fixture := newLoopIntegrationFixture(
		t,
		"run-revoked-pending",
		"restore only",
	)
	begin := beginIntegrationDispatch(t, fixture, 10*time.Minute, 5*time.Minute)
	publishRevokedLoopCatalog(t, fixture.store)
	if err := fixture.store.ReleaseRunLease(
		context.Background(),
		begin.Lease,
	); err != nil {
		t.Fatalf("release simulated crashed owner: %v", err)
	}

	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)
	result, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.runID),
	)
	if err != nil {
		t.Fatalf("recover Run: %v", err)
	}
	if result.Disposition != loopapi.DispositionWaitingReconciliation ||
		result.ReasonCode != reasonRecoveredPending ||
		invoker.callCount() != 0 {
		t.Fatalf("result=%+v calls=%d", result, invoker.callCount())
	}
	stored, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		begin.Attempt.AttemptID,
	)
	if err != nil {
		t.Fatalf("GetModelDispatchRecord: %v", err)
	}
	if stored.Attempt.State != corecontract.ModelAttemptUnknown {
		t.Fatalf("recovered state=%q", stored.Attempt.State)
	}
}

func TestUniversalLoopReopensHighWatermarkPendingWithoutRecompileOrReplay(
	t *testing.T,
) {
	taskText := strings.Repeat("x", 1024)
	estimate := pureTaskRequestEstimate(t, taskText)
	fixture := newLoopIntegrationFixtureWithDeadline(
		t,
		"run-reopen-high-watermark-pending",
		taskText,
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		contextPolicyForInputBudget(estimate+1),
	)
	begin := beginIntegrationDispatch(t, fixture, 10*time.Minute, 5*time.Minute)
	if begin.Attempt.ContextCompilation == nil {
		t.Fatal("high-watermark PENDING Attempt has no compilation")
	}
	originalRequestDigest := begin.Attempt.Request.Digest
	originalRequest := bytes.Clone(begin.Attempt.Request.CanonicalBytes)
	originalCompilationDigest := begin.Attempt.ContextCompilation.Digest
	originalCompilation := bytes.Clone(
		begin.Attempt.ContextCompilation.CanonicalBytes,
	)
	lease := begin.Lease
	begin = currentstore.BeginModelDispatchResult{}
	if err := fixture.store.ReleaseRunLease(
		context.Background(),
		lease,
	); err != nil {
		t.Fatalf("release simulated crashed owner: %v", err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	reopened, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		fixture.dbPath,
	)
	if err != nil {
		t.Fatalf("OpenExistingCurrentStore after restart: %v", err)
	}
	fixture.store = reopened

	echo, err := exactadapter.NewDeterministicEcho(fixture.provider)
	if err != nil {
		t.Fatal(err)
	}
	invoker := &integrationInvoker{delegate: echo}
	loop := newIntegrationLoop(t, fixture, invoker)
	result, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.runID),
	)
	if err != nil {
		t.Fatalf("recover Run: %v", err)
	}
	if result.Disposition != loopapi.DispositionWaitingReconciliation ||
		result.ReasonCode != reasonRecoveredPending ||
		invoker.callCount() != 0 {
		t.Fatalf("result=%+v calls=%d", result, invoker.callCount())
	}
	stored, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		"manual-model-attempt",
	)
	if err != nil {
		t.Fatalf("GetModelDispatchRecord: %v", err)
	}
	if stored.Attempt.ContextCompilation == nil ||
		stored.Attempt.Request.Digest != originalRequestDigest ||
		!bytes.Equal(stored.Attempt.Request.CanonicalBytes, originalRequest) ||
		stored.Attempt.ContextCompilation.Digest != originalCompilationDigest ||
		!bytes.Equal(
			stored.Attempt.ContextCompilation.CanonicalBytes,
			originalCompilation,
		) {
		t.Fatalf("recovered Attempt drifted: %+v", stored.Attempt)
	}
	unsettled, err := fixture.store.ScanUnsettledModelDispatchRecords(
		context.Background(),
		fixture.runID,
	)
	if err != nil {
		t.Fatalf("ScanUnsettledModelDispatchRecords: %v", err)
	}
	if len(unsettled) != 1 ||
		unsettled[0].Attempt.AttemptID != "manual-model-attempt" {
		t.Fatalf("unsettled=%+v", unsettled)
	}
}

func TestUniversalLoopMapsInvocationOutcomes(t *testing.T) {
	_, validOutput, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: "valid",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, unrequestedActionOutput, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			ActionRequest: &moduleapi.ModelActionRequestV1{
				ActionID:       "text.stats",
				CanonicalInput: json.RawMessage(`{}`),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		result      modulehost.InvocationResult
		invokeErr   error
		disposition loopapi.Disposition
		reason      string
		terminal    corecontract.ModelAttemptState
		revokeAfter bool
	}{
		{
			name:        "failed",
			result:      modulehost.InvocationResult{Outcome: modulehost.InvocationFailed},
			disposition: loopapi.DispositionTerminated,
			reason:      reasonModelFailed,
			terminal:    corecontract.ModelAttemptFailed,
		},
		{
			name:        "unknown",
			result:      modulehost.InvocationResult{Outcome: modulehost.InvocationUnknown},
			disposition: loopapi.DispositionWaitingReconciliation,
			reason:      reasonModelUnknown,
			revokeAfter: true,
		},
		{
			name: "classified unknown",
			result: modulehost.InvocationResult{
				Outcome: modulehost.InvocationUnknown,
				UnknownClass: modulehost.
					UnknownClassResponseBodyReadIncomplete,
			},
			disposition: loopapi.DispositionWaitingReconciliation,
			reason: string(modulehost.
				UnknownClassResponseBodyReadIncomplete),
			revokeAfter: true,
		},
		{
			name:        "generic error",
			invokeErr:   errors.New("transport result uncertain"),
			disposition: loopapi.DispositionWaitingReconciliation,
			reason:      reasonHostErrorAfterPending,
		},
		{
			name: "malformed model output",
			result: modulehost.InvocationResult{
				Outcome: modulehost.InvocationSucceeded,
				Output:  []byte(`{`),
			},
			disposition: loopapi.DispositionTerminated,
			reason:      reasonModelFailed,
			terminal:    corecontract.ModelAttemptFailed,
		},
		{
			name: "malformed usage remains unknown",
			result: modulehost.InvocationResult{
				Outcome:      modulehost.InvocationSucceeded,
				Output:       validOutput,
				UsageReceipt: []byte(`{`),
			},
			disposition: loopapi.DispositionWaitingReconciliation,
			reason:      reasonInvalidProviderResult,
		},
		{
			name: "unrequested action in pure chat",
			result: modulehost.InvocationResult{
				Outcome: modulehost.InvocationSucceeded,
				Output:  unrequestedActionOutput,
			},
			disposition: loopapi.DispositionTerminated,
			reason:      currentstore.ModelActionRejectionUnknownAction,
			terminal:    corecontract.ModelAttemptSucceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLoopIntegrationFixture(
				t,
				"run-outcome-"+strings.ReplaceAll(test.name, " ", "-"),
				"map outcome",
			)
			invocationResult := test.result
			invocationResult.Provider = fixture.provider
			invoker := &integrationInvoker{
				fixed:     invocationResult,
				invokeErr: test.invokeErr,
			}
			loop := newIntegrationLoop(t, fixture, invoker)
			result, err := loop.Run(
				context.Background(),
				integrationRunInput(fixture.runID),
			)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Disposition != test.disposition ||
				result.ReasonCode != test.reason ||
				invoker.callCount() != 1 {
				t.Fatalf("result=%+v calls=%d", result, invoker.callCount())
			}
			if test.terminal != "" {
				terminal, err := fixture.store.GetTerminalRunResult(
					context.Background(),
					fixture.runID,
				)
				if err != nil || terminal.State != test.terminal {
					t.Fatalf("terminal=%+v error=%v", terminal, err)
				}
			} else {
				unsettled, scanErr := fixture.store.
					ScanUnsettledModelDispatchRecords(
						context.Background(),
						fixture.runID,
					)
				if scanErr != nil || len(unsettled) != 1 ||
					unsettled[0].Attempt.UnknownReason != test.reason ||
					unsettled[0].Attempt.State !=
						corecontract.ModelAttemptUnknown ||
					unsettled[0].Attempt.ResultRef != "" ||
					unsettled[0].Attempt.ProviderReceiptRef != "" ||
					unsettled[0].Usage.ReconciliationStatus !=
						"PENDING_RECONCILIATION" {
					t.Fatalf(
						"unsettled=%+v scan error=%v",
						unsettled,
						scanErr,
					)
				}
				if test.revokeAfter {
					publishRevokedLoopCatalog(t, fixture.store)
				}
				reentered, err := loop.Run(
					context.Background(),
					integrationRunInput(fixture.runID),
				)
				if err != nil ||
					reentered.Disposition != test.disposition ||
					reentered.ReasonCode != test.reason ||
					invoker.callCount() != 1 {
					t.Fatalf(
						"reentered=%+v calls=%d error=%v",
						reentered,
						invoker.callCount(),
						err,
					)
				}
			}
		})
	}
}

// newRealBeginResult is shared by gate tests. It deliberately creates the
// process-local invocation permit through the real Current Store transaction;
// a caller-constructed BeginModelDispatchResult can never substitute for it.
func newRealBeginResult(t *testing.T) currentstore.BeginModelDispatchResult {
	t.Helper()
	fixture := newLoopIntegrationFixture(t, "run-real-gate", "gate request")
	return beginIntegrationDispatchAtLeaseTail(t, fixture, 15*time.Minute, 0)
}

func newRealBeginResultAtLeaseTail(
	t *testing.T,
	offset time.Duration,
) currentstore.BeginModelDispatchResult {
	t.Helper()
	fixture := newLoopIntegrationFixture(t, "run-real-gate", "gate request")
	return beginIntegrationDispatchAtLeaseTail(t, fixture, 15*time.Minute, offset)
}

type loopIntegrationFixture struct {
	store    *currentstore.Store
	dbPath   string
	runID    string
	taskText string
	provider moduleapi.ActivatedModuleRef
}

func publishRevokedLoopCatalog(t *testing.T, store *currentstore.Store) {
	t.Helper()
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(controlcontract.ControlSnapshot{
			SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV1,
			SnapshotID:    "control-loop-revoked",
			TenantID:      "tenant-loop",
			Revision:      2,
			Agents:        []corecontract.AgentRef{},
			Workspaces:    []controlcontract.WorkspaceDefinition{},
			Profiles:      []controlcontract.ProfileDefinition{},
		})
	if err != nil {
		t.Fatal(err)
	}
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(
			controlcontract.CatalogGeneration{
				SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
				GenerationID:          "catalog-loop-revoked",
				Generation:            2,
				TenantID:              "tenant-loop",
				ControlSnapshotID:     controlRef.SnapshotID,
				ControlSnapshotDigest: controlRef.Digest,
				Entries:               []controlcontract.CatalogEntry{},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishControlCatalog(
		context.Background(),
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: 1,
			NewPointerRevision:      2,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatalf("publish revoked current Catalog: %v", err)
	}
}

func pureTaskRequestEstimate(t *testing.T, taskText string) uint64 {
	t.Helper()
	_, canonical, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{{
				Role: moduleapi.ModelRoleUser, Content: taskText,
			}},
			Parameters: json.RawMessage(`{"temperature":0}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(len(canonical))
}

func contextPolicyForInputBudget(
	inputBudget uint64,
) corecontract.ContextPolicyV1 {
	return corecontract.ContextPolicyV1{
		SchemaVersion:        corecontract.ContextPolicySchemaVersionV1,
		ContextWindowTokens:  inputBudget + 1,
		ReservedOutputTokens: 1,
		RecentHistoryTurns:   0,
		EstimatorVersion: corecontract.
			ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
	}
}

func integrationAttemptID(t *testing.T, runID string) string {
	t.Helper()
	operationKey, err := corecontract.ModelLogicalOperationKey(
		runID,
		"member-primary",
		pureChatLogicalStepID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return modelAttemptIDPrefix + operationKey
}

func newLoopIntegrationFixture(
	t *testing.T,
	runID string,
	taskText string,
) *loopIntegrationFixture {
	return newLoopIntegrationFixtureWithDeadline(
		t,
		runID,
		taskText,
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)
}

func newLoopIntegrationFixtureWithDeadline(
	t *testing.T,
	runID string,
	taskText string,
	deadline time.Time,
	contextPolicies ...corecontract.ContextPolicyV1,
) *loopIntegrationFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, path); err != nil {
		t.Fatalf("InitFreshCurrentStore: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatalf("OpenExistingCurrentStore: %v", err)
	}

	const (
		tenantID        = "tenant-loop"
		providerName    = "test-provider"
		modelName       = "test-model"
		billingVersion  = "billing-v1"
		priceSnapshotID = "price-test-v1"
	)
	if _, err := store.PutModelPriceSnapshot(
		ctx,
		corecontract.ModelPriceSnapshotV1{
			SchemaVersion:   corecontract.ModelPriceSnapshotSchemaVersionV1,
			PriceSnapshotID: priceSnapshotID,
			Provider:        providerName,
			Model:           modelName,
			BillingVersion:  billingVersion,
			Currency:        "USD",
			PricingStatus:   corecontract.PricingKnown,
			Pricing: json.RawMessage(
				`{"input_per_million":1,"output_per_million":2}`,
			),
		},
	); err != nil {
		t.Fatalf("PutModelPriceSnapshot: %v", err)
	}
	_, modelConfigCanonical, err := moduleapi.NewModelBindingConfigV1(
		moduleapi.ModelBindingConfigV1{
			SchemaVersion:   moduleapi.ModelBindingConfigSchemaV1,
			Provider:        providerName,
			Model:           modelName,
			ModelBuildID:    "test-model-build-v1",
			BillingVersion:  billingVersion,
			PriceSnapshotID: priceSnapshotID,
			Parameters:      json.RawMessage(`{"temperature":0}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configRef := putIntegrationContent(
		t,
		store,
		currentstore.ContentConfig,
		modelConfigCanonical,
	)
	authorityRef := putIntegrationContent(
		t,
		store,
		currentstore.ContentAuthorityCeiling,
		[]byte(`{"effects":[],"filesystem_roots":[],"network_allowlist":[],"schema_version":"authority-ceiling/v1","secret_refs":[]}`),
	)
	contextPolicy := putIntegrationPolicy(
		t, store, "context-policy", corecontract.PolicyContext,
		contextPolicies...,
	)
	costPolicy := putIntegrationPolicy(
		t, store, "cost-policy", corecontract.PolicyCost,
	)
	schedulingPolicy := putIntegrationPolicy(
		t, store, "scheduling-policy", corecontract.PolicyScheduling,
	)
	budgetPolicy := putIntegrationPolicy(
		t, store, "budget-policy", corecontract.PolicyCost,
	)

	manifestBytes := integrationModuleManifest(t)
	parsedManifest, _, err := moduleapi.ParseModuleManifestV1(manifestBytes)
	if err != nil {
		t.Fatalf("ParseModuleManifestV1: %v", err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		manifestBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := store.InstallModule(
		ctx,
		currentstore.InstallModuleInput{
			InstallationID:      "installation-model",
			ModuleID:            parsedManifest.ID,
			ExactVersion:        parsedManifest.Version,
			ExpectedManifestRef: manifestRef,
			ManifestBytes:       manifestBytes,
			ArtifactDigest:      strings.Repeat("a", 64),
		},
	)
	if err != nil {
		t.Fatalf("InstallModule: %v", err)
	}
	activation, err := store.ActivateModule(
		ctx,
		currentstore.ActivateModuleInput{
			ActivationID:       "activation-model",
			TenantID:           tenantID,
			InstanceID:         "instance-model",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "core.integration.echo",
		},
	)
	if err != nil {
		t.Fatalf("ActivateModule: %v", err)
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
	modelPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameModelGenerate,
		ExactVersion: moduleapi.PortVersionV1,
	}
	agent := corecontract.AgentRef{
		ID: "agent-loop", Version: "v1", Digest: strings.Repeat("2", 64),
	}
	workspace := corecontract.WorkspaceRef{
		ID: "workspace-loop", Version: "v1", Digest: strings.Repeat("3", 64),
	}
	profile := corecontract.ProfileRef{
		ID: "profile-loop", Version: "v1", Digest: strings.Repeat("4", 64),
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(controlcontract.ControlSnapshot{
			SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV1,
			SnapshotID:    "control-loop",
			TenantID:      tenantID,
			Revision:      1,
			Agents:        []corecontract.AgentRef{agent},
			Workspaces: []controlcontract.WorkspaceDefinition{{
				Workspace: workspace, BudgetPolicy: budgetPolicy,
			}},
			Profiles: []controlcontract.ProfileDefinition{{
				Profile:          profile,
				ContextPolicy:    contextPolicy,
				CostPolicy:       costPolicy,
				SchedulingPolicy: schedulingPolicy,
				Bindings: []controlcontract.BindingSpec{{
					Port:                modelPort,
					InstanceID:          provider.InstanceID,
					ConfigRef:           configRef,
					AuthorityCeilingRef: authorityRef,
					FailurePolicy:       moduleapi.FailureRequired,
				}},
			}},
		})
	if err != nil {
		t.Fatalf("NewControlSnapshot: %v", err)
	}
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(controlcontract.CatalogGeneration{
			SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
			GenerationID:          "catalog-loop",
			Generation:            1,
			TenantID:              tenantID,
			ControlSnapshotID:     controlRef.SnapshotID,
			ControlSnapshotDigest: controlRef.Digest,
			Entries: []controlcontract.CatalogEntry{{
				Activation: provider,
				Provides:   []moduleapi.PortRef{modelPort},
			}},
		})
	if err != nil {
		t.Fatalf("NewCatalogGeneration: %v", err)
	}
	basis, err := store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: 0,
			NewPointerRevision:      1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("PublishControlCatalog: %v", err)
	}

	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          taskText,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task := newIntegrationContent(
		t,
		currentstore.ContentTaskInput,
		taskCanonical,
	)
	intent, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(corecontract.AdmissionIntentV1{
			SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:          tenantID,
			AdmissionKey:      "admission-" + runID,
			PrincipalID:       "principal-loop",
			WorkspaceID:       workspace.ID,
			AgentID:           agent.ID,
			ProfileID:         profile.ID,
			TaskInputRef:      task.Digest,
			RequestedPorts:    []moduleapi.PortRef{modelPort},
			Deadline:          deadline,
			CancellationScope: "run",
			ExplicitLimits:    json.RawMessage(`{}`),
		})
	if err != nil {
		t.Fatalf("NewAdmissionIntentV1: %v", err)
	}
	_ = intent
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		ctx,
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            runID,
			MemberID:         "member-primary",
			RecoveryRootRef:  "recovery/" + runID,
			PublishedBasis:   basis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := store.CommitRunAdmission(
		ctx,
		currentstore.CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         intentCanonical,
			IntentDigest:            intentDigest,
			MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
			RunManifestCanonical:    compiled.RunManifestCanonical,
			Contents:                []currentstore.ContentInput{task},
		},
	); err != nil {
		t.Fatalf("CommitRunAdmission: %v", err)
	}
	fixture := &loopIntegrationFixture{
		store: store, dbPath: path, runID: runID,
		taskText: taskText, provider: provider,
	}
	t.Cleanup(func() {
		if err := fixture.store.Close(); err != nil {
			t.Errorf("Close Current Store: %v", err)
		}
	})
	return fixture
}

func beginIntegrationDispatch(
	t *testing.T,
	fixture *loopIntegrationFixture,
	leaseTTL time.Duration,
	deadlineAfter time.Duration,
) currentstore.BeginModelDispatchResult {
	t.Helper()
	return beginIntegrationDispatchWithDeadline(
		t,
		fixture,
		leaseTTL,
		func(currentstore.RunLease) time.Time {
			return time.Now().UTC().Add(deadlineAfter).Truncate(time.Microsecond)
		},
	)
}

func beginIntegrationDispatchAtLeaseTail(
	t *testing.T,
	fixture *loopIntegrationFixture,
	leaseTTL time.Duration,
	offset time.Duration,
) currentstore.BeginModelDispatchResult {
	t.Helper()
	return beginIntegrationDispatchWithDeadline(
		t,
		fixture,
		leaseTTL,
		func(lease currentstore.RunLease) time.Time {
			return lease.ExpiresAt.Add(-RunLeaseTailGrace + offset)
		},
	)
}

func beginIntegrationDispatchWithDeadline(
	t *testing.T,
	fixture *loopIntegrationFixture,
	leaseTTL time.Duration,
	deadlineFor func(currentstore.RunLease) time.Time,
) currentstore.BeginModelDispatchResult {
	t.Helper()
	ctx := context.Background()
	lease, err := fixture.store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID: fixture.runID, OwnerID: "manual-loop-owner", TTL: leaseTTL,
		},
	)
	if err != nil {
		t.Fatalf("AcquireCurrentRunLease: %v", err)
	}
	run, err := fixture.store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop: %v", err)
	}
	prepared, err := preparePureChatRequestV1(run)
	if err != nil {
		t.Fatalf("preparePureChatRequestV1: %v", err)
	}
	deadline := deadlineFor(lease).UTC().Truncate(time.Microsecond)
	begin, err := fixture.store.BeginModelDispatch(
		ctx,
		currentstore.BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "manual-model-attempt",
			LogicalStepID:               "manual-model-step",
			ContextCompilationCanonical: prepared.ContextCompilationCanonical,
			RequestCanonical:            prepared.RequestCanonical,
			Deadline:                    deadline,
		},
	)
	if err != nil {
		t.Fatalf("BeginModelDispatch: %v", err)
	}
	if !begin.Created || !begin.InvokeAllowed ||
		begin.Attempt.State != corecontract.ModelAttemptPending {
		t.Fatalf("BeginModelDispatch result=%+v", begin)
	}
	if begin.Lease.ExpiresAt.Before(deadline.Add(persistenceGrace)) {
		t.Fatalf(
			"lease expiry %s does not cover deadline %s plus persistence grace",
			begin.Lease.ExpiresAt,
			deadline,
		)
	}
	return begin
}

func integrationRunInput(runID string) loopapi.RunInput {
	return loopapi.RunInput{
		RunID: runID, MaxSteps: 1, MaxDuration: 30 * time.Second,
	}
}

func newIntegrationLoop(
	t *testing.T,
	fixture *loopIntegrationFixture,
	invoker modulehost.ModuleInvoker,
) *UniversalLoop {
	t.Helper()
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  fixture.provider.ArtifactDigest,
		AdapterIdentity: fixture.provider.AdapterIdentity,
		Invoker:         invoker,
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	loop, err := NewUniversalLoop(fixture.store, registry)
	if err != nil {
		t.Fatalf("NewUniversalLoop: %v", err)
	}
	return loop
}

type integrationInvoker struct {
	delegate  modulehost.ModuleInvoker
	fixed     modulehost.InvocationResult
	invokeErr error
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
	calls     atomic.Int32
}

func (invoker *integrationInvoker) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	invoker.calls.Add(1)
	if invoker.entered != nil {
		invoker.once.Do(func() { close(invoker.entered) })
	}
	if invoker.release != nil {
		select {
		case <-invoker.release:
		case <-ctx.Done():
			return modulehost.InvocationResult{}, ctx.Err()
		}
	}
	if invoker.delegate != nil {
		return invoker.delegate.Invoke(ctx, prepared)
	}
	return invoker.fixed, invoker.invokeErr
}

func (invoker *integrationInvoker) callCount() int32 {
	return invoker.calls.Load()
}

func integrationModuleManifest(t *testing.T) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"api_version": moduleapi.ModuleManifestAPIVersionV1,
		"id":          "test.model",
		"version":     "v1",
		"runtime": map[string]any{
			"mode":       string(moduleapi.RuntimeModeRequestTrustedInProcess),
			"protocol":   moduleapi.RuntimeProtocolGoInProcessV1,
			"entrypoint": "builtin.integration.echo",
		},
		"provides": []any{map[string]any{
			"name":          moduleapi.PortNameModelGenerate,
			"exact_version": moduleapi.PortVersionV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func putIntegrationPolicy(
	t *testing.T,
	store *currentstore.Store,
	id string,
	policyType corecontract.PolicyType,
	contextPolicies ...corecontract.ContextPolicyV1,
) corecontract.PolicyRef {
	t.Helper()
	body := json.RawMessage(`{"enabled":true}`)
	if policyType == corecontract.PolicyContext {
		if len(contextPolicies) > 1 {
			t.Fatal("at most one ContextPolicy override is allowed")
		}
		policy := corecontract.ContextPolicyV1{
			SchemaVersion:        corecontract.ContextPolicySchemaVersionV1,
			ContextWindowTokens:  32768,
			ReservedOutputTokens: 4096,
			RecentHistoryTurns:   8,
			EstimatorVersion: corecontract.
				ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
		}
		if len(contextPolicies) == 1 {
			policy = contextPolicies[0]
		}
		_, canonical, err := corecontract.NewContextPolicyV1(
			policy,
		)
		if err != nil {
			t.Fatal(err)
		}
		body = canonical
	}
	_, ref, canonical, err := corecontract.NewPolicyDocument(
		id,
		"v1",
		policyType,
		body,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutContent(
		context.Background(),
		currentstore.ContentInput{
			Digest: ref.Digest, Kind: currentstore.ContentPolicy,
			MediaType: "application/json", CanonicalBytes: canonical,
		},
	); err != nil {
		t.Fatalf("PutContent policy %s: %v", id, err)
	}
	return ref
}

func putIntegrationContent(
	t *testing.T,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	body []byte,
) string {
	t.Helper()
	input := newIntegrationContent(t, kind, body)
	if _, err := store.PutContent(context.Background(), input); err != nil {
		t.Fatalf("PutContent %s: %v", kind, err)
	}
	return input.Digest
}

func newIntegrationContent(
	t *testing.T,
	kind currentstore.ContentKind,
	body []byte,
) currentstore.ContentInput {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON(body)
	if err != nil {
		t.Fatalf("CanonicalJSON %s: %v", kind, err)
	}
	digest, err := currentstore.ComputeContentDigest(
		kind,
		"application/json",
		canonical,
	)
	if err != nil {
		t.Fatalf("ComputeContentDigest %s: %v", kind, err)
	}
	return currentstore.ContentInput{
		Digest: digest, Kind: kind, MediaType: "application/json",
		CanonicalBytes: canonical,
	}
}
