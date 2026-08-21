// Package runscheduler provides the optional, tenant-bound fair execution
// facade. It owns no queue, model authority, or durable result: every candidate,
// permit, and outcome remains in Current Store and the single Universal Loop.
package runscheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ScheduledMaxSteps    uint32        = 4
	ScheduledMaxDuration time.Duration = 2 * time.Minute
	defaultPollInterval                = 10 * time.Millisecond
	ownerPrefix                        = "runscheduler-"

	reasonRunCanceled              = "RUN_CANCELED"
	reasonCompositeFamilyCanceled  = "COMPOSITE_FAMILY_CANCELED"
	reasonCompositeChildrenPending = "COMPOSITE_CHILDREN_PENDING"
	reasonCompositeChildUnknown    = "COMPOSITE_CHILD_UNKNOWN"
)

var (
	ErrInvalidScheduler   = errors.New("runscheduler: invalid Scheduler")
	ErrSchedulerClosed    = errors.New("runscheduler: Scheduler is closed")
	ErrSchedulerIntegrity = errors.New(
		"runscheduler: Scheduler integrity violation",
	)
)

// Config is frozen by the Operator at production-composition startup. Enabled
// is deliberately outside this type: callers either construct a Scheduler or
// keep injecting the direct Universal Loop.
type Config struct {
	TenantID     string
	Limits       currentstore.FairSchedulerLimits
	PollInterval time.Duration
}

type schedulerStore interface {
	ClaimFairRun(
		context.Context,
		currentstore.ClaimFairRunInput,
	) (currentstore.FairRunClaimResult, error)
	GetFairRunTargetView(
		context.Context,
		string,
	) (currentstore.FairRunTargetView, error)
	GetTerminalRunResult(
		context.Context,
		string,
	) (currentstore.TerminalRunResult, error)
	ReleaseRunLease(context.Context, currentstore.RunLease) error
}

type claimedRunner interface {
	RunClaimed(
		context.Context,
		loopapi.RunInput,
		currentstore.RunLease,
	) (loopapi.RunResult, error)
}

// DefaultConfig returns the first S3-A policy for one exact tenant.
func DefaultConfig(tenantID string) Config {
	return Config{
		TenantID: tenantID,
		Limits: currentstore.FairSchedulerLimits{
			GlobalWorkers:         4,
			MaxActivePerWorkspace: 2,
			MaxActivePerFamily:    2,
		},
		PollInterval: defaultPollInterval,
	}
}

// Scheduler implements loopapi.Loop for existing production consumers. Each
// caller can execute another fairly selected Run before its own target; target
// completion is always re-read from Current Store, so in-memory coordination is
// only an efficiency detail and is safe to lose on restart.
type Scheduler struct {
	store        schedulerStore
	loop         claimedRunner
	tenantID     string
	limits       currentstore.FairSchedulerLimits
	pollInterval time.Duration
	workerSlots  chan struct{}

	claimMu  sync.Mutex
	stateMu  sync.Mutex
	closing  bool
	fatalErr error
	stopCh   chan struct{}
	active   sync.WaitGroup
}

var _ loopapi.Loop = (*Scheduler)(nil)

func New(
	store *currentstore.Store,
	loop *coreloop.UniversalLoop,
	config Config,
) (*Scheduler, error) {
	if store == nil || loop == nil || !validOpaque(config.TenantID) {
		return nil, fmt.Errorf(
			"%w: Store, Universal Loop, and tenant are required",
			ErrInvalidScheduler,
		)
	}
	if config.Limits.GlobalWorkers == 0 ||
		config.Limits.MaxActivePerWorkspace == 0 ||
		config.Limits.MaxActivePerFamily == 0 ||
		config.Limits.MaxActivePerWorkspace > config.Limits.GlobalWorkers ||
		config.Limits.MaxActivePerFamily > config.Limits.GlobalWorkers {
		return nil, fmt.Errorf(
			"%w: invalid concurrency limits",
			ErrInvalidScheduler,
		)
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultPollInterval
	}
	if config.PollInterval < time.Millisecond || config.PollInterval > time.Second {
		return nil, fmt.Errorf(
			"%w: poll interval must be between 1ms and 1s",
			ErrInvalidScheduler,
		)
	}
	return &Scheduler{
		store:        store,
		loop:         loop,
		tenantID:     config.TenantID,
		limits:       config.Limits,
		pollInterval: config.PollInterval,
		workerSlots:  make(chan struct{}, config.Limits.GlobalWorkers),
		stopCh:       make(chan struct{}),
	}, nil
}

// Run waits for one target Run while contributing bounded worker capacity to
// the same tenant's fair runnable set. Actual scheduled execution uses the
// frozen S3-A quantum, not request-local transient limits.
func (scheduler *Scheduler) Run(
	ctx context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	if scheduler == nil || scheduler.store == nil || scheduler.loop == nil {
		return loopapi.RunResult{}, fmt.Errorf(
			"%w: implementation is not initialized",
			ErrInvalidScheduler,
		)
	}
	if ctx == nil {
		return loopapi.RunResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidScheduler,
		)
	}
	if err := input.Validate(); err != nil {
		return loopapi.RunResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidScheduler,
			err,
		)
	}
	if err := scheduler.enter(); err != nil {
		return loopapi.RunResult{}, err
	}
	defer scheduler.active.Done()
	if err := scheduler.acquireWorker(ctx); err != nil {
		return loopapi.RunResult{}, err
	}
	defer scheduler.releaseWorker()

	for {
		stable, result, err := scheduler.readStableTarget(ctx, input.RunID)
		if err != nil {
			return loopapi.RunResult{}, err
		}
		if stable {
			return result, nil
		}
		if err := scheduler.runningError(); err != nil {
			return loopapi.RunResult{}, err
		}

		ownerID, err := newOwnerID()
		if err != nil {
			return loopapi.RunResult{}, err
		}
		claim, err := scheduler.claimFairRun(
			ctx,
			currentstore.ClaimFairRunInput{
				TenantID: scheduler.tenantID,
				OwnerID:  ownerID,
				TTL:      ScheduledMaxDuration + coreloop.RunLeaseTailGrace,
				Limits:   scheduler.limits,
			},
		)
		if err != nil {
			return loopapi.RunResult{}, err
		}
		switch claim.Status {
		case currentstore.FairSchedulerClaimed:
			if stopErr := scheduler.runningError(); stopErr != nil {
				releaseErr := scheduler.store.ReleaseRunLease(
					context.Background(),
					claim.Lease,
				)
				return loopapi.RunResult{}, errors.Join(stopErr, releaseErr)
			}
			executionCtx, cancel := context.WithTimeout(
				context.Background(),
				ScheduledMaxDuration,
			)
			executed, runErr := scheduler.loop.RunClaimed(
				executionCtx,
				loopapi.RunInput{
					RunID:       claim.Lease.RunID,
					MaxSteps:    ScheduledMaxSteps,
					MaxDuration: ScheduledMaxDuration,
				},
				claim.Lease,
			)
			cancel()
			if runErr != nil {
				fatal := fmt.Errorf(
					"runscheduler: execute claimed Run %q: %w",
					claim.Lease.RunID,
					runErr,
				)
				return loopapi.RunResult{}, scheduler.tripFatal(fatal)
			}
			if executed.RunID != claim.Lease.RunID {
				return loopapi.RunResult{}, scheduler.tripFatal(fmt.Errorf(
					"%w: Universal Loop returned another Run",
					ErrSchedulerIntegrity,
				))
			}
			if claim.Lease.RunID == input.RunID {
				return executed, nil
			}
			if err := scheduler.runningError(); err != nil {
				return loopapi.RunResult{}, err
			}
		case currentstore.FairSchedulerNoRunnable,
			currentstore.FairSchedulerCapacityExhausted:
			if err := scheduler.wait(ctx); err != nil {
				return loopapi.RunResult{}, err
			}
		default:
			return loopapi.RunResult{}, scheduler.tripFatal(fmt.Errorf(
				"%w: unsupported claim status %q",
				ErrSchedulerIntegrity,
				claim.Status,
			))
		}
	}
}

// Close stops future Run entries and waits for active facade calls. Production
// shutdown invokes it only after stopping new Admission/HTTP work; an in-flight
// claimed Loop quantum is never canceled into a replayable state by Close.
func (scheduler *Scheduler) Close(ctx context.Context) error {
	if scheduler == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("%w: close context is nil", ErrInvalidScheduler)
	}
	scheduler.claimMu.Lock()
	scheduler.stateMu.Lock()
	scheduler.stopLocked(nil)
	scheduler.stateMu.Unlock()
	scheduler.claimMu.Unlock()
	done := make(chan struct{})
	go func() {
		scheduler.active.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (scheduler *Scheduler) readStableTarget(
	ctx context.Context,
	runID string,
) (bool, loopapi.RunResult, error) {
	view, err := scheduler.store.GetFairRunTargetView(ctx, runID)
	if err != nil {
		return false, loopapi.RunResult{}, err
	}
	if view.TenantID != scheduler.tenantID {
		return false, loopapi.RunResult{}, fmt.Errorf(
			"%w: target belongs to another tenant",
			ErrInvalidScheduler,
		)
	}
	if view.IsChannel {
		return false, loopapi.RunResult{}, fmt.Errorf(
			"%w: Channel Runs are not supported by S3-A Scheduler",
			ErrInvalidScheduler,
		)
	}
	if view.LeaseActive {
		return false, loopapi.RunResult{}, nil
	}
	if view.Cancellation != nil {
		reason := reasonRunCanceled
		if view.IsComposite {
			reason = reasonCompositeFamilyCanceled
		}
		return true, newResult(
			view.RunID,
			loopapi.DispositionWaitingExternal,
			view.FrameRevision,
			reason,
		), nil
	}
	switch view.FrameStep {
	case corecontract.TerminatedLoopStep:
		terminal, err := scheduler.store.GetTerminalRunResult(ctx, view.RunID)
		if err != nil {
			return false, loopapi.RunResult{}, err
		}
		return true, newResult(
			view.RunID,
			loopapi.DispositionTerminated,
			terminal.FrameRevision,
			terminal.ReasonCode,
		), nil
	case corecontract.WaitingReconciliationLoopStep:
		return true, newResult(
			view.RunID,
			loopapi.DispositionWaitingReconciliation,
			view.FrameRevision,
			view.WaitingReason,
		), nil
	case corecontract.ModelPendingLoopStep,
		corecontract.ActionPendingLoopStep,
		corecontract.ChannelPendingLoopStep:
		return true, newResult(
			view.RunID,
			loopapi.DispositionWaitingExternal,
			view.FrameRevision,
			view.FrameStep,
		), nil
	case corecontract.WaitingChildrenLoopStep:
		if view.CompositeChildUnknown {
			return true, newResult(
				view.RunID,
				loopapi.DispositionWaitingReconciliation,
				view.FrameRevision,
				reasonCompositeChildUnknown,
			), nil
		}
		if view.CompositeChildrenPending {
			return true, newResult(
				view.RunID,
				loopapi.DispositionWaitingExternal,
				view.FrameRevision,
				reasonCompositeChildrenPending,
			), nil
		}
	case corecontract.InitialLoopStep,
		corecontract.ModelReadyAfterActionLoopStep:
		return false, loopapi.RunResult{}, nil
	default:
		return false, loopapi.RunResult{}, fmt.Errorf(
			"%w: unsupported target Frame step %q",
			ErrSchedulerIntegrity,
			view.FrameStep,
		)
	}
	return false, loopapi.RunResult{}, nil
}

func (scheduler *Scheduler) enter() error {
	scheduler.stateMu.Lock()
	defer scheduler.stateMu.Unlock()
	if scheduler.fatalErr != nil {
		return scheduler.fatalErr
	}
	if scheduler.closing {
		return ErrSchedulerClosed
	}
	scheduler.active.Add(1)
	return nil
}

func (scheduler *Scheduler) acquireWorker(ctx context.Context) error {
	select {
	case scheduler.workerSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-scheduler.stopCh:
		return scheduler.runningError()
	}
}

func (scheduler *Scheduler) releaseWorker() {
	<-scheduler.workerSlots
}

func (scheduler *Scheduler) claimFairRun(
	ctx context.Context,
	input currentstore.ClaimFairRunInput,
) (currentstore.FairRunClaimResult, error) {
	scheduler.claimMu.Lock()
	defer scheduler.claimMu.Unlock()
	if err := scheduler.runningError(); err != nil {
		return currentstore.FairRunClaimResult{}, err
	}
	return scheduler.store.ClaimFairRun(ctx, input)
}

func (scheduler *Scheduler) tripFatal(cause error) error {
	if cause == nil {
		cause = ErrSchedulerIntegrity
	}
	scheduler.claimMu.Lock()
	scheduler.stateMu.Lock()
	scheduler.stopLocked(cause)
	fatal := scheduler.fatalErr
	scheduler.stateMu.Unlock()
	scheduler.claimMu.Unlock()
	return fatal
}

func (scheduler *Scheduler) stopLocked(fatal error) {
	if fatal != nil && scheduler.fatalErr == nil {
		scheduler.fatalErr = fatal
	}
	if !scheduler.closing {
		scheduler.closing = true
		close(scheduler.stopCh)
	}
}

func (scheduler *Scheduler) runningError() error {
	scheduler.stateMu.Lock()
	defer scheduler.stateMu.Unlock()
	if scheduler.fatalErr != nil {
		return scheduler.fatalErr
	}
	if scheduler.closing {
		return ErrSchedulerClosed
	}
	return nil
}

func (scheduler *Scheduler) wait(ctx context.Context) error {
	timer := time.NewTimer(scheduler.pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-scheduler.stopCh:
		return scheduler.runningError()
	case <-timer.C:
		return nil
	}
}

func newResult(
	runID string,
	disposition loopapi.Disposition,
	frameRevision uint64,
	reason string,
) loopapi.RunResult {
	return loopapi.RunResult{
		RunID:         runID,
		Disposition:   disposition,
		FrameRevision: frameRevision,
		ReasonCode:    reason,
	}
}

func newOwnerID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("runscheduler: generate owner ID: %w", err)
	}
	return ownerPrefix + hex.EncodeToString(raw[:]), nil
}

func validOpaque(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
