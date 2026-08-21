package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDrainingHTTPHandlerRejectsConcurrentRequestsAfterAdmissionCloses(
	t *testing.T,
) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan struct{})
	var entered atomic.Uint64
	handler := &drainingHTTPHandler{next: http.HandlerFunc(
		func(response http.ResponseWriter, _ *http.Request) {
			entered.Add(1)
			close(started)
			<-release
			response.WriteHeader(http.StatusNoContent)
		},
	)}
	go func() {
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/chat", nil),
		)
		close(firstDone)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not acquire admission")
	}
	drainDone := handler.beginDrain()
	select {
	case <-drainDone:
		t.Fatal("drain completed while admitted request was active")
	default:
	}

	const attempts = 64
	results := make(chan *httptest.ResponseRecorder, attempts)
	var requests sync.WaitGroup
	requests.Add(attempts)
	for index := 0; index < attempts; index++ {
		go func() {
			defer requests.Done()
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequest(
					http.MethodPost,
					"http://127.0.0.1/channel/inbound",
					nil,
				),
			)
			results <- response
		}()
	}
	requests.Wait()
	close(results)
	for response := range results {
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("post-drain status=%d", response.Code)
		}
		if response.Header().Get("Connection") != "close" {
			t.Fatalf("post-drain Connection=%q", response.Header().Get("Connection"))
		}
	}
	if got := entered.Load(); got != 1 {
		t.Fatalf("downstream entries after drain=%d, want 1 total", got)
	}

	close(release)
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("admitted request did not finish")
	}
	select {
	case <-drainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not complete after admitted request finished")
	}
}

func TestDrainingHTTPHandlerConcurrentAdmissionBoundary(t *testing.T) {
	t.Parallel()

	const (
		iterations = 50
		attempts   = 32
	)
	for iteration := 0; iteration < iterations; iteration++ {
		var entered atomic.Uint64
		handler := &drainingHTTPHandler{next: http.HandlerFunc(
			func(response http.ResponseWriter, _ *http.Request) {
				entered.Add(1)
				response.WriteHeader(http.StatusNoContent)
			},
		)}
		start := make(chan struct{})
		statuses := make(chan int, attempts)
		var requests sync.WaitGroup
		requests.Add(attempts)
		for index := 0; index < attempts; index++ {
			go func() {
				defer requests.Done()
				<-start
				response := httptest.NewRecorder()
				handler.ServeHTTP(
					response,
					httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil),
				)
				statuses <- response.Code
			}()
		}
		close(start)
		drainDone := handler.beginDrain()
		requests.Wait()
		close(statuses)
		var admitted uint64
		for status := range statuses {
			switch status {
			case http.StatusNoContent:
				admitted++
			case http.StatusServiceUnavailable:
			default:
				t.Fatalf("iteration %d: unexpected status=%d", iteration, status)
			}
		}
		if got := entered.Load(); got != admitted {
			t.Fatalf(
				"iteration %d: downstream entries=%d, admitted responses=%d",
				iteration,
				got,
				admitted,
			)
		}
		select {
		case <-drainDone:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: drain did not complete", iteration)
		}
	}
}

func TestServeHTTPDrainsActiveRequestBeforeCancellation(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	requestCanceled := make(chan struct{}, 1)
	next := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		close(started)
		select {
		case <-release:
			response.WriteHeader(http.StatusNoContent)
		case <-request.Context().Done():
			requestCanceled <- struct{}{}
		}
	})
	tracked := &drainingHTTPHandler{next: next}
	requestContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	server := &http.Server{
		Handler: tracked,
		BaseContext: func(net.Listener) context.Context {
			return requestContext
		},
	}
	lifecycle, stopLifecycle := context.WithCancel(context.Background())
	result := make(chan struct {
		drained bool
		err     error
	}, 1)
	go func() {
		drained, serveErr := serveHTTPUntilStopped(
			lifecycle,
			server,
			listener,
			tracked,
			cancelRequests,
		)
		result <- struct {
			drained bool
			err     error
		}{drained: drained, err: serveErr}
	}()

	responseResult := make(chan error, 1)
	go func() {
		response, requestErr := http.Get(fmt.Sprintf("http://%s/", listener.Addr()))
		if requestErr != nil {
			responseResult <- requestErr
			return
		}
		_, copyErr := io.Copy(io.Discard, response.Body)
		responseResult <- errors.Join(copyErr, response.Body.Close())
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("active request did not start")
	}
	stopLifecycle()

	select {
	case <-requestCanceled:
		t.Fatal("lifecycle cancellation immediately canceled active request")
	case outcome := <-result:
		t.Fatalf("server returned before active request drained: %+v", outcome)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case requestErr := <-responseResult:
		if requestErr != nil {
			t.Fatalf("active request completion: %v", requestErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active request did not complete")
	}
	select {
	case outcome := <-result:
		if outcome.err != nil || !outcome.drained {
			t.Fatalf("graceful drain result: %+v", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not return after request drained")
	}
}

func TestForcedHTTPShutdownReportsUndrainedHandlerAndCancelsOnlyAfterGrace(
	t *testing.T,
) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	requestContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	tracked := &drainingHTTPHandler{next: http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			close(started)
			<-request.Context().Done()
			close(canceled)
			<-release
		},
	)}
	server := &http.Server{
		Handler: tracked,
		BaseContext: func(net.Listener) context.Context {
			return requestContext
		},
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get(fmt.Sprintf("http://%s/", listener.Addr()))
		if requestErr != nil {
			requestDone <- requestErr
			return
		}
		_, copyErr := io.Copy(io.Discard, response.Body)
		requestDone <- errors.Join(copyErr, response.Body.Close())
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("active request did not start")
	}

	type shutdownResult struct {
		drained bool
		err     error
	}
	result := make(chan shutdownResult, 1)
	go func() {
		drained, shutdownErr := stopAndDrainHTTPServerWithTimeouts(
			server,
			tracked,
			cancelRequests,
			100*time.Millisecond,
			50*time.Millisecond,
		)
		result <- shutdownResult{drained: drained, err: shutdownErr}
	}()
	select {
	case <-canceled:
		t.Fatal("request was canceled before the grace period elapsed")
	case outcome := <-result:
		t.Fatalf("shutdown returned during grace period: %+v", outcome)
	case <-time.After(25 * time.Millisecond):
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("forced shutdown did not cancel request")
	}
	select {
	case outcome := <-result:
		if outcome.drained || !errors.Is(outcome.err, errHTTPHandlersNotDrained) {
			t.Fatalf("forced shutdown result: %+v", outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("forced shutdown did not return")
	}

	close(release)
	select {
	case <-requestDone:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled request did not exit after release")
	}
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}
