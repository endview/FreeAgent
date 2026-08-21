package controlruntime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestShutdownCoordinatorOrdersTwoEndpointsDrainRecoveryAndClose(t *testing.T) {
	t.Parallel()
	gate := NewAdmissionGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	handler, err := gate.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(handlerStarted)
		<-releaseHandler
	}))
	if err != nil {
		t.Fatal(err)
	}
	handlerDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(handlerDone)
	}()
	<-handlerStarted

	budget, err := newShutdownBudget(context.Background(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	endpointStarted := make(chan int, 2)
	releaseEndpoints := make(chan struct{})
	var mu sync.Mutex
	order := make([]string, 0, 5)
	appendOrder := func(value string) {
		mu.Lock()
		order = append(order, value)
		mu.Unlock()
	}
	endpoint := func(index int) ShutdownStep {
		return func(context.Context) error {
			endpointStarted <- index
			<-releaseEndpoints
			appendOrder(string(rune('0' + index)))
			return nil
		}
	}
	var cancelCalls atomic.Uint64
	coordinator, err := NewShutdownCoordinator(gate, budget, ShutdownHooks{
		Endpoints:      [2]ShutdownStep{endpoint(0), endpoint(1)},
		CancelRequests: func() { cancelCalls.Add(1) },
		DrainRuntime: func(context.Context) error {
			appendOrder("runtime")
			return nil
		},
		RecoverStore: func(context.Context) error {
			appendOrder("recover")
			return nil
		},
		CloseStore: func(context.Context) error {
			appendOrder("close")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resultCh := make(chan ShutdownResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, shutdownErr := coordinator.Shutdown(context.Background())
		resultCh <- result
		errCh <- shutdownErr
	}()
	seen := map[int]bool{}
	for len(seen) < 2 {
		select {
		case index := <-endpointStarted:
			seen[index] = true
		case <-time.After(time.Second):
			t.Fatal("both endpoint shutdown callbacks did not start")
		}
	}
	close(releaseEndpoints)
	select {
	case <-resultCh:
		t.Fatal("shutdown advanced before admitted handler drained")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseHandler)
	<-handlerDone
	result := <-resultCh
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if !result.AdmissionDrained || !result.EndpointsStopped ||
		!result.RuntimeDrained || !result.RecoveryCompleted ||
		!result.StoreCloseCompleted || result.CancellationIssued {
		t.Fatalf("shutdown result=%+v", result)
	}
	if cancelCalls.Load() != 0 {
		t.Fatalf("graceful cancellation calls=%d", cancelCalls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 5 || order[2] != "runtime" || order[3] != "recover" ||
		order[4] != "close" {
		t.Fatalf("shutdown order=%v", order)
	}
}

func TestShutdownCoordinatorConcurrentWaitersDoNotRepeatSequence(t *testing.T) {
	t.Parallel()
	gate := NewAdmissionGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	budget, err := newShutdownBudget(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var endpoints, runtime, recoveries, closes atomic.Uint64
	coordinator, err := NewShutdownCoordinator(gate, budget, ShutdownHooks{
		Endpoints: [2]ShutdownStep{
			func(context.Context) error { endpoints.Add(1); <-release; return nil },
			func(context.Context) error { endpoints.Add(1); <-release; return nil },
		},
		CancelRequests: func() {},
		DrainRuntime:   func(context.Context) error { runtime.Add(1); return nil },
		RecoverStore:   func(context.Context) error { recoveries.Add(1); return nil },
		CloseStore:     func(context.Context) error { closes.Add(1); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.Shutdown(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error=%v", err)
	}
	const waiters = 32
	results := make(chan error, waiters)
	for index := 0; index < waiters; index++ {
		go func() {
			_, err := coordinator.Shutdown(context.Background())
			results <- err
		}()
	}
	close(release)
	for index := 0; index < waiters; index++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if endpoints.Load() != 2 || runtime.Load() != 1 || recoveries.Load() != 1 ||
		closes.Load() != 1 {
		t.Fatalf(
			"calls endpoints=%d runtime=%d recover=%d close=%d",
			endpoints.Load(), runtime.Load(), recoveries.Load(), closes.Load(),
		)
	}
}

func TestShutdownCoordinatorUndrainedAdmissionNeverRecoversOrCloses(t *testing.T) {
	t.Parallel()
	gate := NewAdmissionGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	handler, err := gate.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	}))
	if err != nil {
		t.Fatal(err)
	}
	go handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	<-started
	budget, err := newShutdownBudget(context.Background(), 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	cancelObserved := make(chan struct{})
	var canceled, runtime, recovered, closed atomic.Uint64
	coordinator, err := NewShutdownCoordinator(gate, budget, ShutdownHooks{
		Endpoints: [2]ShutdownStep{
			func(context.Context) error { return nil },
			func(context.Context) error { return nil },
		},
		CancelRequests: func() {
			canceled.Add(1)
			close(cancelObserved)
		},
		DrainRuntime: func(context.Context) error { runtime.Add(1); return nil },
		RecoverStore: func(context.Context) error { recovered.Add(1); return nil },
		CloseStore:   func(context.Context) error { closed.Add(1); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Shutdown(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v", err)
	}
	if result.AdmissionDrained || result.RuntimeDrained ||
		result.RecoveryCompleted || result.StoreCloseCompleted {
		t.Fatalf("unsafe shutdown result=%+v", result)
	}
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not issue cancellation")
	}
	if canceled.Load() != 1 || runtime.Load() != 0 || recovered.Load() != 0 ||
		closed.Load() != 0 {
		t.Fatalf(
			"calls cancel=%d runtime=%d recovery=%d close=%d",
			canceled.Load(), runtime.Load(), recovered.Load(), closed.Load(),
		)
	}
	close(release)
}

func TestShutdownCoordinatorCallbackPanicStopsUnsafeSuccessors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		panicEndpoint bool
		panicRuntime  bool
		panicRecovery bool
		panicClose    bool
		wantRuntime   uint64
		wantRecovery  uint64
		wantClose     uint64
	}{
		{name: "endpoint", panicEndpoint: true},
		{name: "runtime", panicRuntime: true, wantRuntime: 1},
		{name: "recovery", panicRecovery: true, wantRuntime: 1, wantRecovery: 1},
		{name: "close", panicClose: true, wantRuntime: 1, wantRecovery: 1, wantClose: 1},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gate := NewAdmissionGate()
			if err := gate.Open(); err != nil {
				t.Fatal(err)
			}
			budget, err := newShutdownBudget(context.Background(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var runtime, recovery, closeCalls atomic.Uint64
			endpoint0 := func(context.Context) error { return nil }
			if test.panicEndpoint {
				endpoint0 = func(context.Context) error { panic("endpoint") }
			}
			coordinator, err := NewShutdownCoordinator(gate, budget, ShutdownHooks{
				Endpoints:      [2]ShutdownStep{endpoint0, func(context.Context) error { return nil }},
				CancelRequests: func() {},
				DrainRuntime: func(context.Context) error {
					runtime.Add(1)
					if test.panicRuntime {
						panic("runtime")
					}
					return nil
				},
				RecoverStore: func(context.Context) error {
					recovery.Add(1)
					if test.panicRecovery {
						panic("recovery")
					}
					return nil
				},
				CloseStore: func(context.Context) error {
					closeCalls.Add(1)
					if test.panicClose {
						panic("close")
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = coordinator.Shutdown(context.Background())
			if !errors.Is(err, ErrShutdownPanic) {
				t.Fatalf("panic error=%v", err)
			}
			if runtime.Load() != test.wantRuntime ||
				recovery.Load() != test.wantRecovery || closeCalls.Load() != test.wantClose {
				t.Fatalf(
					"calls runtime=%d recovery=%d close=%d",
					runtime.Load(), recovery.Load(), closeCalls.Load(),
				)
			}
		})
	}
}

func TestShutdownCoordinatorStepFailureDoesNotCrossSafetyBoundary(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("step failed")
	for _, failed := range []string{"endpoint", "runtime", "recovery", "close"} {
		failed := failed
		t.Run(failed, func(t *testing.T) {
			t.Parallel()
			gate := NewAdmissionGate()
			if err := gate.Open(); err != nil {
				t.Fatal(err)
			}
			budget, err := newShutdownBudget(context.Background(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var runtime, recovery, closeCalls atomic.Uint64
			endpoint := func(context.Context) error {
				if failed == "endpoint" {
					return sentinel
				}
				return nil
			}
			coordinator, err := NewShutdownCoordinator(gate, budget, ShutdownHooks{
				Endpoints:      [2]ShutdownStep{endpoint, func(context.Context) error { return nil }},
				CancelRequests: func() {},
				DrainRuntime: func(context.Context) error {
					runtime.Add(1)
					if failed == "runtime" {
						return sentinel
					}
					return nil
				},
				RecoverStore: func(context.Context) error {
					recovery.Add(1)
					if failed == "recovery" {
						return sentinel
					}
					return nil
				},
				CloseStore: func(context.Context) error {
					closeCalls.Add(1)
					if failed == "close" {
						return sentinel
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = coordinator.Shutdown(context.Background())
			if !errors.Is(err, sentinel) {
				t.Fatalf("failure error=%v", err)
			}
			if failed == "endpoint" && (runtime.Load() != 0 || recovery.Load() != 0 || closeCalls.Load() != 0) {
				t.Fatal("endpoint failure crossed into unsafe successors")
			}
			if failed == "runtime" && (recovery.Load() != 0 || closeCalls.Load() != 0) {
				t.Fatal("runtime failure crossed into Store steps")
			}
			if failed == "recovery" && closeCalls.Load() != 0 {
				t.Fatal("recovery failure closed Store")
			}
		})
	}
}

func TestShutdownBudgetNeverExtendsParentAndHasFrozenMaximum(t *testing.T) {
	t.Parallel()
	parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	budget, err := NewShutdownBudget(parent)
	if err != nil {
		t.Fatal(err)
	}
	parentDeadline, _ := parent.Deadline()
	budgetDeadline, ok := budget.start().Deadline()
	if !ok || budgetDeadline.After(parentDeadline) {
		t.Fatalf("budget deadline=%v parent=%v", budgetDeadline, parentDeadline)
	}
	maximum, err := NewShutdownBudget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deadline, ok := maximum.start().Deadline()
	if !ok {
		t.Fatal("maximum shutdown budget has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining > 30*time.Second || remaining < 29*time.Second {
		t.Fatalf("maximum shutdown budget=%s", remaining)
	}
	budget.close()
	maximum.close()
}

func TestShutdownBudgetStartsWhenSequenceStarts(t *testing.T) {
	t.Parallel()
	budget, err := newShutdownBudget(context.Background(), 40*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	deadline, ok := budget.start().Deadline()
	if !ok {
		t.Fatal("lazy budget has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 25*time.Millisecond || remaining > 40*time.Millisecond {
		t.Fatalf("budget started before shutdown: remaining=%s", remaining)
	}
	budget.close()
}

func TestShutdownConfigurationFailsClosed(t *testing.T) {
	t.Parallel()
	if _, err := NewShutdownBudget(nil); !errors.Is(err, ErrInvalidShutdown) {
		t.Fatalf("nil parent error=%v", err)
	}
	gate := NewAdmissionGate()
	budget, err := newShutdownBudget(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewShutdownCoordinator(gate, budget, ShutdownHooks{})
	if !errors.Is(err, ErrInvalidShutdown) {
		t.Fatalf("empty hooks error=%v", err)
	}
	coordinator, err := NewShutdownCoordinator(gate, budget, ShutdownHooks{
		Endpoints: [2]ShutdownStep{
			func(context.Context) error { return nil },
			func(context.Context) error { return nil },
		},
		CancelRequests: func() {},
		DrainRuntime:   func(context.Context) error { return nil },
		RecoverStore:   func(context.Context) error { return nil },
		CloseStore:     func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Shutdown(nil); !errors.Is(err, ErrInvalidShutdown) {
		t.Fatalf("nil wait context error=%v", err)
	}
	if _, err := NewShutdownCoordinator(gate, budget, ShutdownHooks{
		Endpoints: [2]ShutdownStep{
			func(context.Context) error { return nil },
			func(context.Context) error { return nil },
		},
		CancelRequests: func() {},
		DrainRuntime:   func(context.Context) error { return nil },
		RecoverStore:   func(context.Context) error { return nil },
		CloseStore:     func(context.Context) error { return nil },
	}); !errors.Is(err, ErrInvalidShutdown) {
		t.Fatalf("reused budget error=%v", err)
	}
}

func TestShutdownOwnershipClaimIsAtomicAndUnique(t *testing.T) {
	t.Parallel()
	hooks := ShutdownHooks{
		Endpoints: [2]ShutdownStep{
			func(context.Context) error { return nil },
			func(context.Context) error { return nil },
		},
		CancelRequests: func() {},
		DrainRuntime:   func(context.Context) error { return nil },
		RecoverStore:   func(context.Context) error { return nil },
		CloseStore:     func(context.Context) error { return nil },
	}
	invalidGateBudget, err := newShutdownBudget(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewShutdownCoordinator(&AdmissionGate{}, invalidGateBudget, hooks); !errors.Is(
		err,
		ErrInvalidShutdown,
	) {
		t.Fatalf("zero-value Admission gate error=%v", err)
	}
	if _, err := NewShutdownCoordinator(
		NewAdmissionGate(),
		invalidGateBudget,
		hooks,
	); err != nil {
		t.Fatalf("invalid gate polluted budget: %v", err)
	}

	sharedGate := NewAdmissionGate()
	firstBudget, err := newShutdownBudget(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewShutdownCoordinator(sharedGate, firstBudget, hooks); err != nil {
		t.Fatal(err)
	}

	unusedBudget, err := newShutdownBudget(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewShutdownCoordinator(sharedGate, unusedBudget, hooks); !errors.Is(
		err,
		ErrInvalidShutdown,
	) {
		t.Fatalf("second coordinator on one gate error=%v", err)
	}
	if _, err := NewShutdownCoordinator(NewAdmissionGate(), unusedBudget, hooks); err != nil {
		t.Fatalf("failed gate claim polluted unused budget: %v", err)
	}

	unusedGate := NewAdmissionGate()
	if _, err := NewShutdownCoordinator(unusedGate, firstBudget, hooks); !errors.Is(
		err,
		ErrInvalidShutdown,
	) {
		t.Fatalf("second coordinator on one budget error=%v", err)
	}
	replacementBudget, err := newShutdownBudget(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewShutdownCoordinator(unusedGate, replacementBudget, hooks); err != nil {
		t.Fatalf("failed budget claim polluted unused gate: %v", err)
	}
}

func TestShutdownOwnershipConcurrentClaimsHaveOneWinner(t *testing.T) {
	t.Parallel()
	const contenders = 32
	hooks := ShutdownHooks{
		Endpoints: [2]ShutdownStep{
			func(context.Context) error { return nil },
			func(context.Context) error { return nil },
		},
		CancelRequests: func() {},
		DrainRuntime:   func(context.Context) error { return nil },
		RecoverStore:   func(context.Context) error { return nil },
		CloseStore:     func(context.Context) error { return nil },
	}
	sharedGate := NewAdmissionGate()
	budgets := make([]*ShutdownBudget, contenders)
	for index := range budgets {
		budget, err := newShutdownBudget(context.Background(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		budgets[index] = budget
	}
	start := make(chan struct{})
	type claimResult struct {
		index int
		err   error
	}
	results := make(chan claimResult, contenders)
	for index, budget := range budgets {
		go func(index int, budget *ShutdownBudget) {
			<-start
			_, err := NewShutdownCoordinator(sharedGate, budget, hooks)
			results <- claimResult{index: index, err: err}
		}(index, budget)
	}
	close(start)
	winner := -1
	for range contenders {
		result := <-results
		if result.err == nil {
			if winner >= 0 {
				t.Fatalf("multiple ownership winners: %d and %d", winner, result.index)
			}
			winner = result.index
			continue
		}
		if !errors.Is(result.err, ErrInvalidShutdown) {
			t.Fatalf("claim %d error=%v", result.index, result.err)
		}
	}
	if winner < 0 {
		t.Fatal("concurrent ownership claim had no winner")
	}
	for index, budget := range budgets {
		if index == winner {
			continue
		}
		if _, err := NewShutdownCoordinator(NewAdmissionGate(), budget, hooks); err != nil {
			t.Fatalf("losing claim polluted budget %d: %v", index, err)
		}
	}
}

func TestShutdownBlockingCancellationCannotExceedHardDeadline(t *testing.T) {
	t.Parallel()
	gate := NewAdmissionGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	budget, err := newShutdownBudget(context.Background(), 40*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	releaseCancel := make(chan struct{})
	cancelReturned := make(chan struct{})
	var recovery, closeCalls atomic.Uint64
	sentinel := errors.New("runtime drain failed")
	coordinator, err := NewShutdownCoordinator(gate, budget, ShutdownHooks{
		Endpoints: [2]ShutdownStep{
			func(context.Context) error { return nil },
			func(context.Context) error { return nil },
		},
		CancelRequests: func() {
			defer close(cancelReturned)
			<-releaseCancel
		},
		DrainRuntime: func(context.Context) error { return sentinel },
		RecoverStore: func(context.Context) error {
			recovery.Add(1)
			return nil
		},
		CloseStore: func(context.Context) error {
			closeCalls.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	result, err := coordinator.Shutdown(context.Background())
	elapsed := time.Since(started)
	if !errors.Is(err, sentinel) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocking cancellation error=%v", err)
	}
	if elapsed < 20*time.Millisecond || elapsed > 500*time.Millisecond {
		t.Fatalf("blocking cancellation elapsed=%s", elapsed)
	}
	if !result.CancellationIssued || result.RuntimeDrained ||
		result.RecoveryCompleted || result.StoreCloseCompleted {
		t.Fatalf("blocking cancellation result=%+v", result)
	}
	if recovery.Load() != 0 || closeCalls.Load() != 0 {
		t.Fatalf("blocking cancellation crossed Store boundary")
	}
	close(releaseCancel)
	select {
	case <-cancelReturned:
	case <-time.After(time.Second):
		t.Fatal("released cancellation callback did not return")
	}
}

func TestShutdownErrorDoesNotExposePanicValue(t *testing.T) {
	t.Parallel()
	err := callShutdownStep(context.Background(), "test step", func(context.Context) error {
		panic("sensitive-panic-value")
	})
	if !errors.Is(err, ErrShutdownPanic) || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("panic error=%q", err)
	}
}

func TestShutdownCancellationPanicIsSanitizedAndStopsSuccessors(t *testing.T) {
	t.Parallel()
	gate := NewAdmissionGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	budget, err := newShutdownBudget(context.Background(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var runtime, recovery, closeCalls atomic.Uint64
	coordinator, err := NewShutdownCoordinator(gate, budget, ShutdownHooks{
		Endpoints: [2]ShutdownStep{
			func(context.Context) error { return errors.New("endpoint failure") },
			func(context.Context) error { return nil },
		},
		CancelRequests: func() { panic("sensitive-cancel-panic") },
		DrainRuntime:   func(context.Context) error { runtime.Add(1); return nil },
		RecoverStore:   func(context.Context) error { recovery.Add(1); return nil },
		CloseStore:     func(context.Context) error { closeCalls.Add(1); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.Shutdown(context.Background())
	if !errors.Is(err, ErrShutdownPanic) || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("cancellation panic error=%q", err)
	}
	if runtime.Load() != 0 || recovery.Load() != 0 || closeCalls.Load() != 0 {
		t.Fatalf(
			"cancellation panic crossed safety boundary: runtime=%d recovery=%d close=%d",
			runtime.Load(), recovery.Load(), closeCalls.Load(),
		)
	}
}
