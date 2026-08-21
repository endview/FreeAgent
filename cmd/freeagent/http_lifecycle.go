package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	gracefulHTTPDrainTimeout = 30 * time.Second
	forcedHTTPDrainTimeout   = 10 * time.Second
)

var errHTTPHandlersNotDrained = errors.New(
	"HTTP handlers did not stop after forced cancellation",
)

type drainingHTTPHandler struct {
	next http.Handler

	mutex     sync.Mutex
	draining  bool
	inFlight  uint64
	drainDone chan struct{}
}

func (handler *drainingHTTPHandler) ServeHTTP(
	response http.ResponseWriter,
	request *http.Request,
) {
	if !handler.admit() {
		response.Header().Set("Connection", "close")
		response.Header().Set("Retry-After", "1")
		http.Error(
			response,
			http.StatusText(http.StatusServiceUnavailable),
			http.StatusServiceUnavailable,
		)
		return
	}
	defer handler.complete()
	handler.next.ServeHTTP(response, request)
}

func (handler *drainingHTTPHandler) admit() bool {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	if handler.draining {
		return false
	}
	handler.inFlight++
	return true
}

func (handler *drainingHTTPHandler) complete() {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	handler.inFlight--
	if handler.draining && handler.inFlight == 0 {
		close(handler.drainDone)
	}
}

// beginDrain is the linearization point for HTTP admission. Requests that
// acquired admission before this call may finish; every later request is
// rejected without reaching Core or Channel handlers.
func (handler *drainingHTTPHandler) beginDrain() <-chan struct{} {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	if handler.draining {
		return handler.drainDone
	}
	handler.draining = true
	handler.drainDone = make(chan struct{})
	if handler.inFlight == 0 {
		close(handler.drainDone)
	}
	return handler.drainDone
}

func (handler *drainingHTTPHandler) waitFor(ctx context.Context) error {
	done := handler.beginDrain()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func serveHTTPUntilStopped(
	lifecycle context.Context,
	server *http.Server,
	listener net.Listener,
	handler *drainingHTTPHandler,
	cancelRequests context.CancelFunc,
) (bool, error) {
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()

	select {
	case serveErr := <-serveErrors:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return true, nil
		}
		drained, drainErr := stopAndDrainHTTPServer(
			server,
			handler,
			cancelRequests,
		)
		return drained, errors.Join(serveErr, drainErr)
	case <-lifecycle.Done():
		drained, drainErr := stopAndDrainHTTPServer(
			server,
			handler,
			cancelRequests,
		)
		serveErr := <-serveErrors
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return drained, errors.Join(drainErr, serveErr)
	}
}

func stopAndDrainHTTPServer(
	server *http.Server,
	handler *drainingHTTPHandler,
	cancelRequests context.CancelFunc,
) (bool, error) {
	return stopAndDrainHTTPServerWithTimeouts(
		server,
		handler,
		cancelRequests,
		gracefulHTTPDrainTimeout,
		forcedHTTPDrainTimeout,
	)
}

func stopAndDrainHTTPServerWithTimeouts(
	server *http.Server,
	handler *drainingHTTPHandler,
	cancelRequests context.CancelFunc,
	graceTimeout time.Duration,
	forceTimeout time.Duration,
) (bool, error) {
	// Close process-local admission before closing the listener. This also
	// rejects requests already queued on keep-alive connections and prevents
	// them from extending the drain indefinitely.
	handler.beginDrain()
	graceContext, cancelGrace := context.WithTimeout(
		context.Background(),
		graceTimeout,
	)
	shutdownErr := server.Shutdown(graceContext)
	waitErr := handler.waitFor(graceContext)
	cancelGrace()
	if shutdownErr == nil && waitErr == nil {
		return true, nil
	}

	// Only after ingress has stopped and the grace period elapsed do requests
	// receive cancellation. Current Store recovery then owns any persisted
	// PENDING/MODEL_UNKNOWN state.
	cancelRequests()
	closeErr := server.Close()
	if errors.Is(closeErr, http.ErrServerClosed) {
		closeErr = nil
	}
	forceContext, cancelForce := context.WithTimeout(
		context.Background(),
		forceTimeout,
	)
	forceWaitErr := handler.waitFor(forceContext)
	cancelForce()
	if forceWaitErr != nil {
		return false, errors.Join(
			shutdownErr,
			waitErr,
			closeErr,
			errHTTPHandlersNotDrained,
			forceWaitErr,
		)
	}
	return true, errors.Join(shutdownErr, waitErr, closeErr)
}
