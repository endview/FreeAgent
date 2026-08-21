// Package controlruntime provides process-local lifecycle primitives for the
// optional Control listener. It owns no listener, Store, session, credential,
// handoff, application service, or external effect.
package controlruntime

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sync"
)

// AdmissionState is the process-local state of one shared HTTP admission
// boundary. State transitions are one-way: STARTUP_CLOSED -> OPEN -> DRAINING,
// or STARTUP_CLOSED -> DRAINING when startup fails before service readiness.
type AdmissionState string

const (
	AdmissionStartupClosed AdmissionState = "STARTUP_CLOSED"
	AdmissionOpen          AdmissionState = "OPEN"
	AdmissionDraining      AdmissionState = "DRAINING"
)

var (
	ErrInvalidAdmissionTransition = errors.New(
		"controlruntime: invalid Admission transition",
	)
	ErrAdmissionNotDraining = errors.New(
		"controlruntime: Admission is not draining",
	)
	ErrInvalidAdmissionHandler = errors.New(
		"controlruntime: Admission handler is nil",
	)
)

// AdmissionGate is one linearizable admission boundary shared by every
// explicitly enabled HTTP listener. It starts closed, opens at most once, and
// permanently rejects new requests after drain begins.
type AdmissionGate struct {
	mu sync.Mutex

	state         AdmissionState
	inFlight      uint64
	drained       chan struct{}
	drainedClosed bool

	// shutdownClaimed binds this process-wide Admission boundary to exactly
	// one ShutdownCoordinator. It is guarded by mu so ownership can be claimed
	// atomically with the coordinator's ShutdownBudget before either is
	// published as consumed.
	shutdownClaimed bool
}

// NewAdmissionGate creates a startup-closed gate. Binding or serving a socket
// does not open this gate; the trusted composition root must call Open only
// after every startup handoff obligation has succeeded.
func NewAdmissionGate() *AdmissionGate {
	return &AdmissionGate{
		state:   AdmissionStartupClosed,
		drained: make(chan struct{}),
	}
}

// State returns the current process-local lifecycle state.
func (gate *AdmissionGate) State() AdmissionState {
	if gate == nil {
		return AdmissionDraining
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.state
}

// Open is the sole startup admission linearization point.
func (gate *AdmissionGate) Open() error {
	if gate == nil {
		return ErrInvalidAdmissionTransition
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.state != AdmissionStartupClosed {
		return ErrInvalidAdmissionTransition
	}
	gate.state = AdmissionOpen
	return nil
}

// BeginDrain permanently closes admission and returns a channel closed after
// every request admitted before this call has returned. The operation is
// idempotent so concurrent listener failures and process cancellation share
// one drain boundary.
func (gate *AdmissionGate) BeginDrain() <-chan struct{} {
	if gate == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.state != AdmissionDraining {
		gate.state = AdmissionDraining
	}
	gate.closeDrainedLocked()
	return gate.drained
}

// WaitDrained waits for the drain already started by BeginDrain. It never
// changes admission state and never manufactures an unbounded background wait.
func (gate *AdmissionGate) WaitDrained(ctx context.Context) error {
	if ctx == nil {
		return errors.New("controlruntime: drain context is nil")
	}
	if gate == nil {
		return nil
	}
	gate.mu.Lock()
	if gate.state != AdmissionDraining {
		gate.mu.Unlock()
		return ErrAdmissionNotDraining
	}
	drained := gate.drained
	gate.mu.Unlock()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Wrap returns a handler governed by this gate. A rejected request receives a
// fixed 503 and never enters next. An admitted request owns its slot until next
// returns; defer releases the slot during ordinary return, cancellation-aware
// return, and panic unwinding. A panic is deliberately not recovered here.
func (gate *AdmissionGate) Wrap(next http.Handler) (http.Handler, error) {
	if gate == nil || nilInterfaceV1(next) {
		return nil, ErrInvalidAdmissionHandler
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !gate.admit() {
			response.Header().Set("Cache-Control", "no-store")
			response.Header().Set("Connection", "close")
			response.Header().Set("Retry-After", "1")
			http.Error(
				response,
				http.StatusText(http.StatusServiceUnavailable),
				http.StatusServiceUnavailable,
			)
			return
		}
		defer gate.complete()
		next.ServeHTTP(response, request)
	}), nil
}

func nilInterfaceV1(value any) bool {
	if value == nil {
		return true
	}
	reflection := reflect.ValueOf(value)
	switch reflection.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflection.IsNil()
	default:
		return false
	}
}

func (gate *AdmissionGate) admit() bool {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.state != AdmissionOpen {
		return false
	}
	gate.inFlight++
	return true
}

func (gate *AdmissionGate) complete() {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.inFlight == 0 {
		panic("controlruntime: Admission in-flight underflow")
	}
	gate.inFlight--
	gate.closeDrainedLocked()
}

func (gate *AdmissionGate) closeDrainedLocked() {
	if gate.state == AdmissionDraining && gate.inFlight == 0 &&
		!gate.drainedClosed {
		close(gate.drained)
		gate.drainedClosed = true
	}
}
