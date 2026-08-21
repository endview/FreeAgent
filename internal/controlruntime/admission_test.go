package controlruntime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type typedNilAdmissionHandlerV1 struct{}

func (*typedNilAdmissionHandlerV1) ServeHTTP(
	http.ResponseWriter,
	*http.Request,
) {
}

func TestAdmissionGateStartsClosedAndNeverInvokesInner(t *testing.T) {
	t.Parallel()
	gate := NewAdmissionGate()
	var entered atomic.Uint64
	handler, err := gate.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered.Add(1)
	}))
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 32; index++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != http.StatusServiceUnavailable ||
			response.Header().Get("Connection") != "close" {
			t.Fatalf("closed response=%d headers=%v", response.Code, response.Header())
		}
	}
	if got := entered.Load(); got != 0 {
		t.Fatalf("startup-closed inner entries=%d", got)
	}
	if gate.State() != AdmissionStartupClosed {
		t.Fatalf("initial state=%q", gate.State())
	}
}

func TestAdmissionGateSharesOneOpenAndDrainBoundary(t *testing.T) {
	t.Parallel()
	gate := NewAdmissionGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	if err := gate.Open(); !errors.Is(err, ErrInvalidAdmissionTransition) {
		t.Fatalf("second Open error=%v", err)
	}

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var entered atomic.Uint64
	inner := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		entered.Add(1)
		started <- struct{}{}
		<-release
		response.WriteHeader(http.StatusNoContent)
	})
	first, err := gate.Wrap(inner)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.Wrap(inner)
	if err != nil {
		t.Fatal(err)
	}
	var requests sync.WaitGroup
	for _, handler := range []http.Handler{first, second} {
		requests.Add(1)
		go func(handler http.Handler) {
			defer requests.Done()
			handler.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodGet, "/", nil),
			)
		}(handler)
	}
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("admitted request did not enter")
		}
	}
	drained := gate.BeginDrain()
	if gate.State() != AdmissionDraining {
		t.Fatalf("draining state=%q", gate.State())
	}
	select {
	case <-drained:
		t.Fatal("drain completed with admitted requests active")
	default:
	}
	for _, handler := range []http.Handler{first, second} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/", nil),
		)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("post-drain status=%d", response.Code)
		}
	}
	if entered.Load() != 2 {
		t.Fatalf("inner entries after drain=%d", entered.Load())
	}
	close(release)
	requests.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gate.WaitDrained(ctx); err != nil {
		t.Fatal(err)
	}
	if err := gate.Open(); !errors.Is(err, ErrInvalidAdmissionTransition) {
		t.Fatalf("Open after drain error=%v", err)
	}
}

func TestAdmissionGateCancellationAwareReturnReleasesSlot(t *testing.T) {
	t.Parallel()
	gate := NewAdmissionGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	handler, err := gate.Wrap(http.HandlerFunc(func(
		_ http.ResponseWriter,
		request *http.Request,
	) {
		close(started)
		<-request.Context().Done()
	}))
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/", nil).WithContext(requestCtx),
		)
		close(done)
	}()
	<-started
	gate.BeginDrain()
	cancelRequest()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled handler did not return")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gate.WaitDrained(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAdmissionGatePanicReleasesSlotAndContinuesPanic(t *testing.T) {
	t.Parallel()
	gate := NewAdmissionGate()
	if err := gate.Open(); err != nil {
		t.Fatal(err)
	}
	const marker = "expected-inner-panic"
	handler, err := gate.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(marker)
	}))
	if err != nil {
		t.Fatal(err)
	}
	recovered := make(chan any, 1)
	go func() {
		defer func() { recovered <- recover() }()
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/", nil),
		)
	}()
	if got := <-recovered; got != marker {
		t.Fatalf("recovered=%v", got)
	}
	gate.BeginDrain()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gate.WaitDrained(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAdmissionGateRejectsInvalidUses(t *testing.T) {
	t.Parallel()
	gate := NewAdmissionGate()
	if _, err := gate.Wrap(nil); !errors.Is(err, ErrInvalidAdmissionHandler) {
		t.Fatalf("nil handler error=%v", err)
	}
	var typedNilPointer *typedNilAdmissionHandlerV1
	if _, err := gate.Wrap(typedNilPointer); !errors.Is(
		err,
		ErrInvalidAdmissionHandler,
	) {
		t.Fatalf("typed-nil pointer handler error=%v", err)
	}
	var typedNilFunction http.HandlerFunc
	if _, err := gate.Wrap(typedNilFunction); !errors.Is(
		err,
		ErrInvalidAdmissionHandler,
	) {
		t.Fatalf("typed-nil function handler error=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := gate.WaitDrained(ctx); !errors.Is(err, ErrAdmissionNotDraining) {
		t.Fatalf("pre-drain Wait error=%v", err)
	}
	gate.BeginDrain()
	gate.BeginDrain()
	if err := gate.WaitDrained(context.Background()); err != nil {
		t.Fatal(err)
	}
}
