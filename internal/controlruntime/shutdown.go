package controlruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/controlapipolicy"
)

var (
	ErrInvalidShutdown = errors.New("controlruntime: invalid shutdown configuration")
	ErrShutdownPanic   = errors.New("controlruntime: shutdown callback panicked")
)

// ShutdownStep is one bounded, context-aware lifecycle step. Implementations
// must stop accepting their own work before returning successfully.
type ShutdownStep func(context.Context) error

// ShutdownBudget owns the one context used by the underlying shutdown
// sequence. Individual callers use their own contexts only while waiting for
// that sequence; abandoning one waiter cannot restart or cancel the sequence.
type ShutdownBudget struct {
	parent  context.Context
	maximum time.Duration

	claimMu sync.Mutex
	claimed bool

	startOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

// NewShutdownBudget creates the only W6-1 shutdown budget. A shorter parent
// deadline remains authoritative; this function can never extend it beyond
// the frozen 30-second maximum.
func NewShutdownBudget(parent context.Context) (*ShutdownBudget, error) {
	return newShutdownBudget(
		parent,
		time.Duration(controlapipolicy.ShutdownTimeoutMillisV1)*time.Millisecond,
	)
}

func newShutdownBudget(
	parent context.Context,
	maximum time.Duration,
) (*ShutdownBudget, error) {
	if parent == nil || maximum <= 0 {
		return nil, ErrInvalidShutdown
	}
	return &ShutdownBudget{parent: parent, maximum: maximum}, nil
}

// start deliberately begins the timeout only when the unique shutdown
// sequence starts, not when a long-lived serve process constructs its
// coordinator. The parent remains authoritative; callers that are reacting to
// a canceled lifecycle should pass context.WithoutCancel(lifecycle) when they
// intend to spend the full graceful-shutdown budget.
func (budget *ShutdownBudget) start() context.Context {
	budget.startOnce.Do(func() {
		budget.ctx, budget.cancel = context.WithTimeout(
			budget.parent,
			budget.maximum,
		)
	})
	return budget.ctx
}

func (budget *ShutdownBudget) close() {
	if budget == nil {
		return
	}
	budget.closeOnce.Do(func() {
		if budget.cancel != nil {
			budget.cancel()
		}
	})
}

// ShutdownHooks contains the complete side-effect boundary of the generic
// coordinator. Endpoints are exact two-listener stop/drain callbacks. Optional
// runtime details are represented by an explicit no-op callback rather than a
// nil capability.
type ShutdownHooks struct {
	Endpoints [2]ShutdownStep
	// CancelRequests must be a process-owned, idempotent, non-blocking
	// context.CancelFunc. The coordinator still calls it behind the hard
	// shutdown deadline so an incorrect blocking implementation cannot stall
	// the unique sequence or cross into Store recovery/close.
	CancelRequests context.CancelFunc
	DrainRuntime   ShutdownStep
	RecoverStore   ShutdownStep
	CloseStore     ShutdownStep
}

// ShutdownResult records only which lifecycle boundaries were proven. It is
// process-local diagnostic state and is not a durable receipt.
type ShutdownResult struct {
	AdmissionDrained    bool
	EndpointsStopped    bool
	CancellationIssued  bool
	RuntimeDrained      bool
	RecoveryCompleted   bool
	StoreCloseCompleted bool
}

// ShutdownCoordinator serializes every shutdown trigger into one sequence.
// The first Shutdown call starts it; concurrent and later calls only wait for
// that same result.
type ShutdownCoordinator struct {
	gate   *AdmissionGate
	budget *ShutdownBudget
	hooks  ShutdownHooks

	startOnce  sync.Once
	done       chan struct{}
	mu         sync.Mutex
	result     ShutdownResult
	err        error
	cancelOnce sync.Once
	cancelErr  error
}

func NewShutdownCoordinator(
	gate *AdmissionGate,
	budget *ShutdownBudget,
	hooks ShutdownHooks,
) (*ShutdownCoordinator, error) {
	if gate == nil || budget == nil ||
		hooks.Endpoints[0] == nil || hooks.Endpoints[1] == nil ||
		hooks.CancelRequests == nil || hooks.DrainRuntime == nil ||
		hooks.RecoverStore == nil || hooks.CloseStore == nil {
		return nil, ErrInvalidShutdown
	}
	if err := claimShutdownOwnership(gate, budget); err != nil {
		return nil, err
	}
	return &ShutdownCoordinator{
		gate: gate, budget: budget, hooks: hooks, done: make(chan struct{}),
	}, nil
}

// claimShutdownOwnership atomically consumes one AdmissionGate and one
// ShutdownBudget. Every construction failure leaves both inputs reusable; a
// successful claim permanently prevents a second coordinator from repeating
// endpoint drain, Store-only recovery, or Store close for the same Admission
// boundary.
func claimShutdownOwnership(
	gate *AdmissionGate,
	budget *ShutdownBudget,
) error {
	if gate == nil || budget == nil || budget.parent == nil ||
		budget.maximum <= 0 {
		return ErrInvalidShutdown
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.drained == nil ||
		(gate.state != AdmissionStartupClosed && gate.state != AdmissionOpen &&
			gate.state != AdmissionDraining) {
		return fmt.Errorf(
			"%w: Admission gate was not constructed",
			ErrInvalidShutdown,
		)
	}
	budget.claimMu.Lock()
	defer budget.claimMu.Unlock()
	if gate.shutdownClaimed {
		return fmt.Errorf(
			"%w: Admission gate already has a shutdown coordinator",
			ErrInvalidShutdown,
		)
	}
	if budget.claimed {
		return fmt.Errorf(
			"%w: shutdown budget is already claimed",
			ErrInvalidShutdown,
		)
	}
	gate.shutdownClaimed = true
	budget.claimed = true
	return nil
}

// Shutdown waits for the unique shutdown sequence. waitCtx controls only this
// caller's wait. Even a caller that times out cannot cancel, repeat, or replace
// the sequence owned by the coordinator's ShutdownBudget.
func (coordinator *ShutdownCoordinator) Shutdown(
	waitCtx context.Context,
) (ShutdownResult, error) {
	if coordinator == nil || waitCtx == nil {
		return ShutdownResult{}, ErrInvalidShutdown
	}
	coordinator.startOnce.Do(func() {
		go coordinator.run()
	})
	select {
	case <-coordinator.done:
		coordinator.mu.Lock()
		defer coordinator.mu.Unlock()
		return coordinator.result, coordinator.err
	case <-waitCtx.Done():
		return ShutdownResult{}, waitCtx.Err()
	}
}

func (coordinator *ShutdownCoordinator) run() {
	defer close(coordinator.done)
	ctx := coordinator.budget.start()
	defer coordinator.budget.close()

	result, err := coordinator.stopEndpointsAndAdmission(ctx)
	if err != nil {
		coordinator.finish(result, err)
		return
	}

	if err := runShutdownStep(
		ctx,
		"drain shared runtime",
		coordinator.hooks.DrainRuntime,
	); err != nil {
		cancelErr := coordinator.cancel(ctx, &result)
		coordinator.finish(result, errors.Join(err, cancelErr))
		return
	}
	result.RuntimeDrained = true

	if err := runShutdownStep(
		ctx,
		"recover Store",
		coordinator.hooks.RecoverStore,
	); err != nil {
		coordinator.finish(result, err)
		return
	}
	result.RecoveryCompleted = true

	if err := runShutdownStep(
		ctx,
		"close Store",
		coordinator.hooks.CloseStore,
	); err != nil {
		coordinator.finish(result, err)
		return
	}
	result.StoreCloseCompleted = true
	coordinator.finish(result, nil)
}

func (coordinator *ShutdownCoordinator) stopEndpointsAndAdmission(
	ctx context.Context,
) (ShutdownResult, error) {
	result := ShutdownResult{}
	drained := coordinator.gate.BeginDrain()
	type endpointResult struct {
		index int
		err   error
	}
	endpointResults := make(chan endpointResult, len(coordinator.hooks.Endpoints))
	for index, endpoint := range coordinator.hooks.Endpoints {
		go func(index int, endpoint ShutdownStep) {
			endpointResults <- endpointResult{
				index: index,
				err:   callShutdownStep(ctx, fmt.Sprintf("stop endpoint %d", index), endpoint),
			}
		}(index, endpoint)
	}

	completed := [2]bool{}
	remaining := len(coordinator.hooks.Endpoints)
	var endpointErr error
	for remaining > 0 || !result.AdmissionDrained {
		select {
		case outcome := <-endpointResults:
			if completed[outcome.index] {
				endpointErr = errors.Join(
					endpointErr,
					fmt.Errorf("%w: duplicate endpoint completion", ErrInvalidShutdown),
				)
				endpointErr = errors.Join(
					endpointErr,
					coordinator.cancel(ctx, &result),
				)
				continue
			}
			completed[outcome.index] = true
			remaining--
			if outcome.err != nil {
				endpointErr = errors.Join(endpointErr, outcome.err)
				endpointErr = errors.Join(
					endpointErr,
					coordinator.cancel(ctx, &result),
				)
			}
		case <-drained:
			result.AdmissionDrained = true
			drained = nil
		case <-ctx.Done():
			cancelErr := coordinator.cancel(ctx, &result)
			return result, errors.Join(endpointErr, cancelErr, ctx.Err())
		}
	}
	if endpointErr != nil {
		return result, endpointErr
	}
	result.EndpointsStopped = true
	return result, nil
}

func (coordinator *ShutdownCoordinator) cancel(
	ctx context.Context,
	result *ShutdownResult,
) error {
	coordinator.cancelOnce.Do(func() {
		coordinator.cancelErr = callCancelRequests(
			ctx,
			coordinator.hooks.CancelRequests,
		)
	})
	result.CancellationIssued = true
	return coordinator.cancelErr
}

func (coordinator *ShutdownCoordinator) finish(
	result ShutdownResult,
	err error,
) {
	coordinator.mu.Lock()
	coordinator.result = result
	coordinator.err = err
	coordinator.mu.Unlock()
}

func runShutdownStep(
	ctx context.Context,
	name string,
	step ShutdownStep,
) error {
	result := make(chan error, 1)
	go func() { result <- callShutdownStep(ctx, name, step) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func callShutdownStep(
	ctx context.Context,
	name string,
	step ShutdownStep,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: %s", ErrShutdownPanic, name)
		}
	}()
	if step == nil {
		return ErrInvalidShutdown
	}
	if err := step(ctx); err != nil {
		return fmt.Errorf("controlruntime: %s: %w", name, err)
	}
	return nil
}

func callCancelRequests(
	ctx context.Context,
	cancel context.CancelFunc,
) error {
	if ctx == nil || cancel == nil {
		return ErrInvalidShutdown
	}
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		result <- invokeCancelRequests(cancel)
	}()
	// This handshake contains no caller code. It only proves the cancellation
	// goroutine has reached its invocation boundary before CancellationIssued
	// may be reported, including when the shutdown context is already done.
	<-started
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func invokeCancelRequests(cancel context.CancelFunc) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("%w: cancel requests", ErrShutdownPanic)
		}
	}()
	cancel()
	return nil
}
