package runscheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
)

type fakeSchedulerStore struct {
	mu         sync.Mutex
	view       currentstore.FairRunTargetView
	terminal   currentstore.TerminalRunResult
	claim      func(context.Context, currentstore.ClaimFairRunInput) (currentstore.FairRunClaimResult, error)
	claims     int
	releases   int
	releaseErr error
}

func (store *fakeSchedulerStore) ClaimFairRun(
	ctx context.Context,
	input currentstore.ClaimFairRunInput,
) (currentstore.FairRunClaimResult, error) {
	store.mu.Lock()
	store.claims++
	claim := store.claim
	store.mu.Unlock()
	if claim == nil {
		return currentstore.FairRunClaimResult{
			Status: currentstore.FairSchedulerNoRunnable,
		}, nil
	}
	return claim(ctx, input)
}

func (store *fakeSchedulerStore) GetFairRunTargetView(
	_ context.Context,
	_ string,
) (currentstore.FairRunTargetView, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.view, nil
}

func (store *fakeSchedulerStore) GetTerminalRunResult(
	_ context.Context,
	_ string,
) (currentstore.TerminalRunResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.terminal, nil
}

func (store *fakeSchedulerStore) ReleaseRunLease(
	_ context.Context,
	_ currentstore.RunLease,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.releases++
	return store.releaseErr
}

func (store *fakeSchedulerStore) counts() (claims int, releases int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.claims, store.releases
}

type fakeClaimedRunner struct {
	run func(context.Context, loopapi.RunInput, currentstore.RunLease) (loopapi.RunResult, error)
}

func (runner fakeClaimedRunner) RunClaimed(
	ctx context.Context,
	input loopapi.RunInput,
	lease currentstore.RunLease,
) (loopapi.RunResult, error) {
	return runner.run(ctx, input, lease)
}

func TestSchedulerExecutionFailureTripsStableFatalLatch(t *testing.T) {
	const runID = "fatal-target"
	failure := errors.New("injected claimed execution failure")
	var observedClaimTTL atomic.Int64
	store := &fakeSchedulerStore{
		view: readySchedulerView(runID),
		claim: func(
			_ context.Context,
			input currentstore.ClaimFairRunInput,
		) (currentstore.FairRunClaimResult, error) {
			observedClaimTTL.Store(int64(input.TTL))
			return claimedSchedulerResult(runID), nil
		},
	}
	scheduler := newFakeScheduler(store, fakeClaimedRunner{
		run: func(
			context.Context,
			loopapi.RunInput,
			currentstore.RunLease,
		) (loopapi.RunResult, error) {
			return loopapi.RunResult{}, failure
		},
	}, 2)

	firstErr := runFakeTarget(scheduler, runID)
	if !errors.Is(firstErr, failure) {
		t.Fatalf("first Run error=%v want injected failure", firstErr)
	}
	secondErr := runFakeTarget(scheduler, runID)
	if !errors.Is(secondErr, failure) || secondErr.Error() != firstErr.Error() {
		t.Fatalf("latched Run error=%v want stable %v", secondErr, firstErr)
	}
	if claims, _ := store.counts(); claims != 1 {
		t.Fatalf("fatal Scheduler claims=%d want 1", claims)
	}
	if got := time.Duration(observedClaimTTL.Load()); got != 2*time.Minute+40*time.Second ||
		coreloop.RunLeaseTailGrace != 40*time.Second {
		t.Fatalf(
			"Scheduler claim TTL=%s tail=%s want=2m40s/40s",
			got,
			coreloop.RunLeaseTailGrace,
		)
	}
}

func TestSchedulerCloseLinearizesBeforeAnyLaterClaim(t *testing.T) {
	const runID = "close-target"
	claimStarted := make(chan struct{})
	releaseClaim := make(chan struct{})
	var claimStartedOnce sync.Once
	store := &fakeSchedulerStore{
		view: readySchedulerView(runID),
		claim: func(
			ctx context.Context,
			_ currentstore.ClaimFairRunInput,
		) (currentstore.FairRunClaimResult, error) {
			claimStartedOnce.Do(func() { close(claimStarted) })
			select {
			case <-releaseClaim:
				return currentstore.FairRunClaimResult{
					Status: currentstore.FairSchedulerNoRunnable,
				}, nil
			case <-ctx.Done():
				return currentstore.FairRunClaimResult{}, ctx.Err()
			}
		},
	}
	scheduler := newFakeScheduler(store, fakeClaimedRunner{
		run: func(
			context.Context,
			loopapi.RunInput,
			currentstore.RunLease,
		) (loopapi.RunResult, error) {
			t.Fatal("Close test unexpectedly executed a claimed Run")
			return loopapi.RunResult{}, nil
		},
	}, 1)
	runDone := make(chan error, 1)
	go func() { runDone <- runFakeTarget(scheduler, runID) }()
	<-claimStarted
	closeDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		closeDone <- scheduler.Close(ctx)
	}()
	close(releaseClaim)
	if err := <-runDone; !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("in-flight Run after Close error=%v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A claim may linearize while Close itself is still waiting for claimMu.
	// Once Close returns, however, no later Run may reach the Store.
	claimsAtClose, _ := store.counts()
	if err := runFakeTarget(scheduler, runID); !errors.Is(err, ErrSchedulerClosed) {
		t.Fatalf("post-Close Run error=%v", err)
	}
	if claims, _ := store.counts(); claims != claimsAtClose {
		t.Fatalf(
			"post-Close claims=%d want unchanged count %d",
			claims,
			claimsAtClose,
		)
	}
}

func TestSchedulerConcurrentSameTargetExecutesOneClaim(t *testing.T) {
	const runID = "shared-target"
	loopStarted := make(chan struct{})
	finishLoop := make(chan struct{})
	secondClaimSeen := make(chan struct{})
	var secondOnce sync.Once
	var claimed atomic.Int32
	store := &fakeSchedulerStore{view: readySchedulerView(runID)}
	store.claim = func(
		_ context.Context,
		_ currentstore.ClaimFairRunInput,
	) (currentstore.FairRunClaimResult, error) {
		if claimed.CompareAndSwap(0, 1) {
			store.mu.Lock()
			store.view.LeaseActive = true
			store.mu.Unlock()
			return claimedSchedulerResult(runID), nil
		}
		secondOnce.Do(func() { close(secondClaimSeen) })
		return currentstore.FairRunClaimResult{
			Status: currentstore.FairSchedulerCapacityExhausted,
		}, nil
	}
	scheduler := newFakeScheduler(store, fakeClaimedRunner{
		run: func(
			_ context.Context,
			_ loopapi.RunInput,
			_ currentstore.RunLease,
		) (loopapi.RunResult, error) {
			close(loopStarted)
			<-finishLoop
			store.mu.Lock()
			store.view.LeaseActive = false
			store.view.FrameStep = corecontract.TerminatedLoopStep
			store.view.State = corecontract.TerminatedLoopStep
			store.view.Disposition = corecontract.TerminatedLoopStep
			store.view.FrameRevision = 2
			store.terminal = currentstore.TerminalRunResult{
				RunID: runID, FrameRevision: 2, ReasonCode: "MODEL_SUCCEEDED",
			}
			store.mu.Unlock()
			return loopapi.RunResult{
				RunID: runID, Disposition: loopapi.DispositionTerminated,
				FrameRevision: 2, ReasonCode: "MODEL_SUCCEEDED",
			}, nil
		},
	}, 2)
	results := make(chan error, 2)
	go func() { results <- runFakeTarget(scheduler, runID) }()
	<-loopStarted
	go func() { results <- runFakeTarget(scheduler, runID) }()
	select {
	case <-secondClaimSeen:
	case <-time.After(time.Second):
		t.Fatal("second caller did not observe existing target lease")
	}
	close(finishLoop)
	for index := 0; index < 2; index++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Run %d: %v", index, err)
		}
	}
	if claimed.Load() != 1 {
		t.Fatalf("claimed executions=%d want 1", claimed.Load())
	}
}

func TestSchedulerReturnsStableStoreProjectionsWithoutClaim(t *testing.T) {
	const runID = "stable-target"
	tests := []struct {
		name        string
		view        currentstore.FairRunTargetView
		terminal    currentstore.TerminalRunResult
		disposition loopapi.Disposition
		reason      string
	}{
		{
			name: "terminal",
			view: currentstore.FairRunTargetView{
				RunID: runID, TenantID: "tenant-a",
				State:       corecontract.TerminatedLoopStep,
				Disposition: corecontract.TerminatedLoopStep,
				FrameStep:   corecontract.TerminatedLoopStep,
			},
			terminal: currentstore.TerminalRunResult{
				RunID: runID, FrameRevision: 4, ReasonCode: "MODEL_SUCCEEDED",
			},
			disposition: loopapi.DispositionTerminated,
			reason:      "MODEL_SUCCEEDED",
		},
		{
			name: "unknown",
			view: currentstore.FairRunTargetView{
				RunID: runID, TenantID: "tenant-a",
				State:         corecontract.WaitingReconciliationLoopStep,
				Disposition:   corecontract.WaitingReconciliationLoopStep,
				FrameStep:     corecontract.WaitingReconciliationLoopStep,
				WaitingReason: "MODEL_UNKNOWN",
			},
			disposition: loopapi.DispositionWaitingReconciliation,
			reason:      "MODEL_UNKNOWN",
		},
		{
			name: "model pending",
			view: currentstore.FairRunTargetView{
				RunID: runID, TenantID: "tenant-a",
				State:     corecontract.InitialRunState,
				FrameStep: corecontract.ModelPendingLoopStep,
			},
			disposition: loopapi.DispositionWaitingExternal,
			reason:      corecontract.ModelPendingLoopStep,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeSchedulerStore{view: test.view, terminal: test.terminal}
			scheduler := newFakeScheduler(store, fakeClaimedRunner{
				run: func(
					context.Context,
					loopapi.RunInput,
					currentstore.RunLease,
				) (loopapi.RunResult, error) {
					t.Fatal("stable target unexpectedly executed")
					return loopapi.RunResult{}, nil
				},
			}, 1)
			result, err := scheduler.Run(context.Background(), fakeRunInput(runID))
			if err != nil || result.Disposition != test.disposition ||
				result.ReasonCode != test.reason {
				t.Fatalf("stable result=%+v error=%v", result, err)
			}
			if claims, _ := store.counts(); claims != 0 {
				t.Fatalf("stable target claims=%d want 0", claims)
			}
		})
	}
}

func newFakeScheduler(
	store schedulerStore,
	runner claimedRunner,
	workers uint32,
) *Scheduler {
	return &Scheduler{
		store:    store,
		loop:     runner,
		tenantID: "tenant-a",
		limits: currentstore.FairSchedulerLimits{
			GlobalWorkers: workers, MaxActivePerWorkspace: workers,
			MaxActivePerFamily: workers,
		},
		pollInterval: time.Millisecond,
		workerSlots:  make(chan struct{}, workers),
		stopCh:       make(chan struct{}),
	}
}

func readySchedulerView(runID string) currentstore.FairRunTargetView {
	return currentstore.FairRunTargetView{
		RunID: runID, TenantID: "tenant-a",
		State:     corecontract.InitialRunState,
		FrameStep: corecontract.InitialLoopStep,
	}
}

func claimedSchedulerResult(runID string) currentstore.FairRunClaimResult {
	return currentstore.FairRunClaimResult{
		Status: currentstore.FairSchedulerClaimed,
		Lease: currentstore.RunLease{
			RunID: runID, OwnerID: "scheduler-test-owner",
			LeaseEpoch: 1, ExpiresAt: time.Now().Add(time.Minute),
		},
		TenantID: "tenant-a", WorkspaceID: "workspace-a",
		FamilyRootRunID: runID, WorkspaceServedUnits: 1,
		WorkspaceRevision: 1,
	}
}

func fakeRunInput(runID string) loopapi.RunInput {
	return loopapi.RunInput{
		RunID: runID, MaxSteps: 4, MaxDuration: time.Second,
	}
}

func runFakeTarget(scheduler *Scheduler, runID string) error {
	_, err := scheduler.Run(context.Background(), fakeRunInput(runID))
	return err
}
