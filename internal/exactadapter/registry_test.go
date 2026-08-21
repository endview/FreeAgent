package exactadapter_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
)

var (
	_ modulehost.ExactAdapterRegistry              = (*exactadapter.Registry)(nil)
	_ activationresolver.ExactAdapterRegistryProbe = (*exactadapter.Registry)(nil)
)

func TestRegistryResolvesOnlyExactKeyAndProbesSameMap(t *testing.T) {
	artifact := strings.Repeat("a", 64)
	invoker := &stubInvoker{}
	registrations := []exactadapter.Registration{
		{
			ArtifactDigest:  artifact,
			AdapterIdentity: "builtin.echo",
			Invoker:         invoker,
		},
	}
	registry, err := exactadapter.NewRegistry(registrations...)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	registrations[0] = exactadapter.Registration{
		ArtifactDigest:  strings.Repeat("f", 64),
		AdapterIdentity: "mutated",
		Invoker:         &stubInvoker{},
	}

	resolved, err := registry.ResolveExact(
		context.Background(),
		artifact,
		"builtin.echo",
	)
	if err != nil {
		t.Fatalf("ResolveExact: %v", err)
	}
	if resolved != invoker {
		t.Fatal("ResolveExact returned a different invoker")
	}
	registered, err := registry.IsRegistered(
		context.Background(),
		artifact,
		"builtin.echo",
	)
	if err != nil || !registered {
		t.Fatalf("IsRegistered=%v error=%v", registered, err)
	}

	for _, key := range []struct {
		artifact string
		adapter  string
	}{
		{artifact: strings.Repeat("b", 64), adapter: "builtin.echo"},
		{artifact: artifact, adapter: "builtin.other"},
	} {
		if _, err := registry.ResolveExact(
			context.Background(),
			key.artifact,
			key.adapter,
		); !errors.Is(err, exactadapter.ErrAdapterNotFound) {
			t.Fatalf("non-exact ResolveExact(%q,%q) error=%v", key.artifact, key.adapter, err)
		}
		registered, err := registry.IsRegistered(
			context.Background(),
			key.artifact,
			key.adapter,
		)
		if err != nil || registered {
			t.Fatalf(
				"non-exact IsRegistered(%q,%q)=%v error=%v",
				key.artifact,
				key.adapter,
				registered,
				err,
			)
		}
	}
}

func TestRegistryRejectsDuplicateEmptyAndWrongDynamicTypes(t *testing.T) {
	artifact := strings.Repeat("a", 64)
	valid := exactadapter.Registration{
		ArtifactDigest:  artifact,
		AdapterIdentity: "builtin.echo",
		Invoker:         &stubInvoker{},
	}
	var typedNil *stubInvoker
	tests := []struct {
		name          string
		registrations []exactadapter.Registration
		want          error
	}{
		{
			name: "empty registry",
			want: exactadapter.ErrInvalidAdapter,
		},
		{
			name: "empty digest",
			registrations: []exactadapter.Registration{
				{
					AdapterIdentity: "builtin.echo",
					Invoker:         &stubInvoker{},
				},
			},
			want: exactadapter.ErrInvalidAdapter,
		},
		{
			name: "empty adapter identity",
			registrations: []exactadapter.Registration{
				{
					ArtifactDigest: artifact,
					Invoker:        &stubInvoker{},
				},
			},
			want: exactadapter.ErrInvalidAdapter,
		},
		{
			name: "wrong dynamic type",
			registrations: []exactadapter.Registration{
				{
					ArtifactDigest:  artifact,
					AdapterIdentity: "builtin.echo",
					Invoker:         struct{}{},
				},
			},
			want: exactadapter.ErrInvalidAdapter,
		},
		{
			name: "typed nil invoker",
			registrations: []exactadapter.Registration{
				{
					ArtifactDigest:  artifact,
					AdapterIdentity: "builtin.echo",
					Invoker:         typedNil,
				},
			},
			want: exactadapter.ErrInvalidAdapter,
		},
		{
			name:          "duplicate exact key",
			registrations: []exactadapter.Registration{valid, valid},
			want:          exactadapter.ErrDuplicateAdapter,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := exactadapter.NewRegistry(test.registrations...)
			if !errors.Is(err, test.want) {
				t.Fatalf("NewRegistry error=%v want %v", err, test.want)
			}
		})
	}
}

func TestRegistryRejectsMalformedLookupWithoutAlternateSearch(t *testing.T) {
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  strings.Repeat("a", 64),
		AdapterIdentity: "builtin.echo",
		Invoker:         &stubInvoker{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ResolveExact(
		context.Background(),
		"",
		"builtin.echo",
	); !errors.Is(err, exactadapter.ErrInvalidAdapter) {
		t.Fatalf("empty lookup digest error=%v", err)
	}
	if _, err := registry.ResolveExact(
		context.Background(),
		strings.Repeat("a", 64),
		"",
	); !errors.Is(err, exactadapter.ErrInvalidAdapter) {
		t.Fatalf("empty lookup adapter error=%v", err)
	}
}

func TestRegistryLoadsOneExactKeyOnlyOnResolveAndCachesIt(t *testing.T) {
	artifact := strings.Repeat("c", 64)
	invoker := &stubInvoker{}
	loads := 0
	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			_ context.Context,
			gotArtifact string,
			gotAdapter string,
		) (modulehost.ModuleInvoker, error) {
			loads++
			if gotArtifact != artifact || gotAdapter != "builtin.lazy" {
				return nil, exactadapter.ErrAdapterNotFound
			}
			return invoker, nil
		},
	)
	if err != nil {
		t.Fatalf("NewRegistryWithLoader: %v", err)
	}
	registered, err := registry.IsRegistered(
		context.Background(),
		artifact,
		"builtin.lazy",
	)
	if err != nil || registered || loads != 0 {
		t.Fatalf("pre-resolve registered/loads = %v/%d, error=%v", registered, loads, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		resolved, err := registry.ResolveExact(
			context.Background(),
			artifact,
			"builtin.lazy",
		)
		if err != nil || resolved != invoker {
			t.Fatalf("ResolveExact(%d) = %v, %v", attempt, resolved, err)
		}
	}
	if loads != 1 {
		t.Fatalf("loader calls = %d, want 1", loads)
	}
	registered, err = registry.IsRegistered(
		context.Background(),
		artifact,
		"builtin.lazy",
	)
	if err != nil || !registered {
		t.Fatalf("post-resolve registered = %v, error=%v", registered, err)
	}
}

func TestRegistryCoalescesSameExactKeyAndLetsWaiterCancel(t *testing.T) {
	artifact := strings.Repeat("d", 64)
	invoker := &stubInvoker{}
	loaderStarted := make(chan struct{})
	releaseLoader := make(chan struct{})
	var loaderCalls atomic.Int32
	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			_ context.Context,
			gotArtifact string,
			gotAdapter string,
		) (modulehost.ModuleInvoker, error) {
			if gotArtifact != artifact || gotAdapter != "builtin.same-key" {
				return nil, exactadapter.ErrAdapterNotFound
			}
			loaderCalls.Add(1)
			close(loaderStarted)
			<-releaseLoader
			return invoker, nil
		},
	)
	if err != nil {
		t.Fatalf("NewRegistryWithLoader: %v", err)
	}

	leaderResult := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			context.Background(),
			artifact,
			"builtin.same-key",
		)
		leaderResult <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	awaitSignal(t, loaderStarted, "leader Loader start")

	probeContext, cancelProbe := context.WithCancel(context.Background())
	probeObserved := &waitObservedContext{
		Context:  probeContext,
		observed: make(chan struct{}),
	}
	probeResult := make(chan registrationResult, 1)
	go func() {
		registered, probeErr := registry.IsRegistered(
			probeObserved,
			artifact,
			"builtin.same-key",
		)
		probeResult <- registrationResult{registered: registered, err: probeErr}
	}()
	awaitSignal(t, probeObserved.observed, "same-key probe flight wait")
	select {
	case result := <-probeResult:
		t.Fatalf("same-key probe returned while Loader was in flight: %+v", result)
	default:
	}
	cancelProbe()
	probe := awaitRegistrationResult(t, probeResult, "cancelled same-key probe")
	if probe.registered || !errors.Is(probe.err, context.Canceled) {
		t.Fatalf("cancelled same-key probe=%v/%v", probe.registered, probe.err)
	}

	waitContext, cancelWait := context.WithCancel(context.Background())
	observed := &waitObservedContext{
		Context:  waitContext,
		observed: make(chan struct{}),
	}
	waiterResult := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			observed,
			artifact,
			"builtin.same-key",
		)
		waiterResult <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	awaitSignal(t, observed.observed, "same-key Resolve flight wait")
	cancelWait()
	waiter := awaitResolveResult(t, waiterResult, "cancelled waiter")
	if waiter.invoker != nil || !errors.Is(waiter.err, context.Canceled) {
		t.Fatalf("cancelled waiter=%v/%v, want nil/context.Canceled", waiter.invoker, waiter.err)
	}
	if got := loaderCalls.Load(); got != 1 {
		t.Fatalf("Loader calls before release=%d, want 1", got)
	}

	coalescedObserved := &waitObservedContext{
		Context:  context.Background(),
		observed: make(chan struct{}),
	}
	coalescedResult := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			coalescedObserved,
			artifact,
			"builtin.same-key",
		)
		coalescedResult <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	awaitSignal(t, coalescedObserved.observed, "coalesced Resolve flight wait")

	successfulProbeContext := &waitObservedContext{
		Context:  context.Background(),
		observed: make(chan struct{}),
	}
	successfulProbe := make(chan registrationResult, 1)
	go func() {
		registered, probeErr := registry.IsRegistered(
			successfulProbeContext,
			artifact,
			"builtin.same-key",
		)
		successfulProbe <- registrationResult{registered: registered, err: probeErr}
	}()
	awaitSignal(t, successfulProbeContext.observed, "successful probe flight wait")
	close(releaseLoader)
	leader := awaitResolveResult(t, leaderResult, "leader result")
	if leader.err != nil || leader.invoker != invoker {
		t.Fatalf("leader result=%v/%v", leader.invoker, leader.err)
	}
	coalesced := awaitResolveResult(t, coalescedResult, "coalesced waiter result")
	if coalesced.err != nil || coalesced.invoker != invoker {
		t.Fatalf("coalesced result=%v/%v", coalesced.invoker, coalesced.err)
	}
	probe = awaitRegistrationResult(t, successfulProbe, "successful same-key probe")
	if !probe.registered || probe.err != nil {
		t.Fatalf("successful same-key probe=%v/%v", probe.registered, probe.err)
	}
	resolved, err := registry.ResolveExact(
		context.Background(),
		artifact,
		"builtin.same-key",
	)
	if err != nil || resolved != invoker || loaderCalls.Load() != 1 {
		t.Fatalf(
			"cached result=%v error=%v Loader calls=%d",
			resolved,
			err,
			loaderCalls.Load(),
		)
	}
}

func TestRegistryLoadsDifferentExactKeysConcurrently(t *testing.T) {
	firstArtifact := strings.Repeat("e", 64)
	secondArtifact := strings.Repeat("f", 64)
	firstInvoker := &stubInvoker{}
	secondInvoker := &stubInvoker{}
	started := make(chan string, 2)
	release := make(chan struct{})
	var loaderCalls atomic.Int32
	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			_ context.Context,
			artifact string,
			adapter string,
		) (modulehost.ModuleInvoker, error) {
			if adapter != "builtin.parallel" {
				return nil, exactadapter.ErrAdapterNotFound
			}
			loaderCalls.Add(1)
			started <- artifact
			<-release
			switch artifact {
			case firstArtifact:
				return firstInvoker, nil
			case secondArtifact:
				return secondInvoker, nil
			default:
				return nil, exactadapter.ErrAdapterNotFound
			}
		},
	)
	if err != nil {
		t.Fatalf("NewRegistryWithLoader: %v", err)
	}

	results := make(chan resolveResult, 2)
	resolve := func(artifact string) {
		invoker, resolveErr := registry.ResolveExact(
			context.Background(),
			artifact,
			"builtin.parallel",
		)
		results <- resolveResult{invoker: invoker, err: resolveErr}
	}
	go resolve(firstArtifact)
	firstStarted := awaitString(t, started, "first exact-key Loader start")
	go resolve(secondArtifact)

	secondStarted := ""
	select {
	case secondStarted = <-started:
	case <-time.After(5 * time.Second):
	}
	close(release)
	firstResult := awaitResolveResult(t, results, "first parallel result")
	secondResult := awaitResolveResult(t, results, "second parallel result")

	if secondStarted == "" {
		t.Fatal("second exact-key Loader did not start while the first key was blocked")
	}
	if firstStarted == secondStarted ||
		(firstStarted != firstArtifact && firstStarted != secondArtifact) ||
		(secondStarted != firstArtifact && secondStarted != secondArtifact) {
		t.Fatalf("Loader starts=%q/%q, want both exact artifacts", firstStarted, secondStarted)
	}
	if firstResult.err != nil || secondResult.err != nil {
		t.Fatalf("parallel results errors=%v/%v", firstResult.err, secondResult.err)
	}
	if loaderCalls.Load() != 2 {
		t.Fatalf("Loader calls=%d, want 2", loaderCalls.Load())
	}
}

func TestRegistryTreatsAdapterIdentityAsPartOfFlightKey(t *testing.T) {
	artifact := strings.Repeat("5", 64)
	firstAdapter := "builtin.parallel.first"
	secondAdapter := "builtin.parallel.second"
	firstInvoker := &stubInvoker{}
	secondInvoker := &stubInvoker{}
	started := make(chan string, 2)
	release := make(chan struct{})
	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			_ context.Context,
			gotArtifact string,
			adapter string,
		) (modulehost.ModuleInvoker, error) {
			if gotArtifact != artifact {
				return nil, exactadapter.ErrAdapterNotFound
			}
			started <- adapter
			<-release
			switch adapter {
			case firstAdapter:
				return firstInvoker, nil
			case secondAdapter:
				return secondInvoker, nil
			default:
				return nil, exactadapter.ErrAdapterNotFound
			}
		},
	)
	if err != nil {
		t.Fatalf("NewRegistryWithLoader: %v", err)
	}

	type keyedResult struct {
		adapter string
		resolveResult
	}
	results := make(chan keyedResult, 2)
	resolve := func(adapter string) {
		invoker, resolveErr := registry.ResolveExact(
			context.Background(),
			artifact,
			adapter,
		)
		results <- keyedResult{
			adapter: adapter,
			resolveResult: resolveResult{
				invoker: invoker,
				err:     resolveErr,
			},
		}
	}
	go resolve(firstAdapter)
	if got := awaitString(t, started, "first adapter Loader start"); got != firstAdapter {
		t.Fatalf("first Loader adapter=%q, want %q", got, firstAdapter)
	}
	go resolve(secondAdapter)
	secondStarted := ""
	select {
	case secondStarted = <-started:
	case <-time.After(5 * time.Second):
	}
	close(release)
	if secondStarted != secondAdapter {
		t.Fatalf("second Loader adapter=%q, want %q", secondStarted, secondAdapter)
	}

	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("ResolveExact(%q) error=%v", result.adapter, result.err)
		}
		want := modulehost.ModuleInvoker(firstInvoker)
		if result.adapter == secondAdapter {
			want = secondInvoker
		}
		if result.invoker != want {
			t.Fatalf("ResolveExact(%q) returned another invoker", result.adapter)
		}
	}
}

func TestRegistryColdLoadDoesNotBlockEagerHitOrUnrelatedProbe(t *testing.T) {
	coldArtifact := strings.Repeat("6", 64)
	eagerArtifact := strings.Repeat("7", 64)
	unknownArtifact := strings.Repeat("8", 64)
	coldInvoker := &stubInvoker{}
	eagerInvoker := &stubInvoker{}
	loaderStarted := make(chan struct{})
	releaseLoader := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseLoader) })
	}
	defer release()

	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			_ context.Context,
			artifact string,
			adapter string,
		) (modulehost.ModuleInvoker, error) {
			if artifact != coldArtifact || adapter != "builtin.cold" {
				return nil, exactadapter.ErrAdapterNotFound
			}
			close(loaderStarted)
			<-releaseLoader
			return coldInvoker, nil
		},
		exactadapter.Registration{
			ArtifactDigest:  eagerArtifact,
			AdapterIdentity: "builtin.eager",
			Invoker:         eagerInvoker,
		},
	)
	if err != nil {
		t.Fatalf("NewRegistryWithLoader: %v", err)
	}
	coldResult := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			context.Background(),
			coldArtifact,
			"builtin.cold",
		)
		coldResult <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	awaitSignal(t, loaderStarted, "cold Loader start")

	eagerResult := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			context.Background(),
			eagerArtifact,
			"builtin.eager",
		)
		eagerResult <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	eager := awaitResolveResult(t, eagerResult, "eager cache hit")
	if eager.err != nil || eager.invoker != eagerInvoker {
		t.Fatalf("eager cache hit=%v/%v", eager.invoker, eager.err)
	}

	probeResult := make(chan registrationResult, 1)
	go func() {
		registered, probeErr := registry.IsRegistered(
			context.Background(),
			unknownArtifact,
			"builtin.unknown",
		)
		probeResult <- registrationResult{registered: registered, err: probeErr}
	}()
	probe := awaitRegistrationResult(t, probeResult, "unrelated probe")
	if probe.registered || probe.err != nil {
		t.Fatalf("unrelated probe=%v/%v, want false/nil", probe.registered, probe.err)
	}

	release()
	cold := awaitResolveResult(t, coldResult, "cold Loader result")
	if cold.err != nil || cold.invoker != coldInvoker {
		t.Fatalf("cold Loader result=%v/%v", cold.invoker, cold.err)
	}
}

func TestRegistryDoesNotCacheLoaderFailureOrCancellation(t *testing.T) {
	artifact := strings.Repeat("1", 64)
	invoker := &stubInvoker{}
	loadFailure := errors.New("temporary materialization failure")
	cancellableLoadStarted := make(chan struct{})
	var loaderCalls atomic.Int32
	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			ctx context.Context,
			_ string,
			_ string,
		) (modulehost.ModuleInvoker, error) {
			switch loaderCalls.Add(1) {
			case 1:
				return invoker, loadFailure
			case 2:
				close(cancellableLoadStarted)
				<-ctx.Done()
				return invoker, nil
			default:
				return invoker, nil
			}
		},
	)
	if err != nil {
		t.Fatalf("NewRegistryWithLoader: %v", err)
	}

	failedInvoker, err := registry.ResolveExact(
		context.Background(),
		artifact,
		"builtin.retry",
	)
	if failedInvoker != nil || !errors.Is(err, loadFailure) {
		t.Fatalf("first ResolveExact error=%v, want Loader failure", err)
	}
	registered, err := registry.IsRegistered(
		context.Background(),
		artifact,
		"builtin.retry",
	)
	if err != nil || registered {
		t.Fatalf("after failure IsRegistered=%v error=%v, want false/nil", registered, err)
	}

	cancelContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.ResolveExact(
		cancelContext,
		artifact,
		"builtin.retry",
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled ResolveExact error=%v, want context.Canceled", err)
	}
	if loaderCalls.Load() != 1 {
		t.Fatalf("pre-cancelled request invoked Loader; calls=%d", loaderCalls.Load())
	}

	loaderContext, cancelLoader := context.WithCancel(context.Background())
	cancelledLoadResult := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			loaderContext,
			artifact,
			"builtin.retry",
		)
		cancelledLoadResult <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	awaitSignal(t, cancellableLoadStarted, "cancellable Loader start")
	cancelLoader()
	cancelledLoad := awaitResolveResult(t, cancelledLoadResult, "cancelled Loader result")
	if cancelledLoad.invoker != nil ||
		!errors.Is(cancelledLoad.err, context.Canceled) {
		t.Fatalf("cancelled Loader result=%v/%v", cancelledLoad.invoker, cancelledLoad.err)
	}
	if loaderCalls.Load() != 2 {
		t.Fatalf("cancelled Loader calls=%d, want 2", loaderCalls.Load())
	}

	resolved, err := registry.ResolveExact(
		context.Background(),
		artifact,
		"builtin.retry",
	)
	if err != nil || resolved != invoker || loaderCalls.Load() != 3 {
		t.Fatalf(
			"retry result=%v error=%v Loader calls=%d, want success/3",
			resolved,
			err,
			loaderCalls.Load(),
		)
	}
}

func TestRegistryDoesNotPropagateLeaderCancellationToLiveWaiter(t *testing.T) {
	artifact := strings.Repeat("4", 64)
	invoker := &stubInvoker{}
	loaderStarted := make(chan struct{})
	var loaderCalls atomic.Int32
	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			ctx context.Context,
			_ string,
			_ string,
		) (modulehost.ModuleInvoker, error) {
			if loaderCalls.Add(1) == 1 {
				close(loaderStarted)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return invoker, nil
		},
	)
	if err != nil {
		t.Fatalf("NewRegistryWithLoader: %v", err)
	}

	leaderContext, cancelLeader := context.WithCancel(context.Background())
	leaderResult := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			leaderContext,
			artifact,
			"builtin.shared-cancel",
		)
		leaderResult <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	awaitSignal(t, loaderStarted, "cancellable leader Loader start")

	const waiterCount = 4
	waiterResult := make(chan resolveResult, waiterCount)
	for index := range waiterCount {
		waitContext := &waitObservedContext{
			Context:  context.Background(),
			observed: make(chan struct{}),
		}
		go func() {
			resolved, resolveErr := registry.ResolveExact(
				waitContext,
				artifact,
				"builtin.shared-cancel",
			)
			waiterResult <- resolveResult{invoker: resolved, err: resolveErr}
		}()
		awaitSignal(
			t,
			waitContext.observed,
			fmt.Sprintf("live waiter %d flight wait", index),
		)
	}
	cancelLeader()

	leader := awaitResolveResult(t, leaderResult, "cancelled leader")
	if leader.invoker != nil || !errors.Is(leader.err, context.Canceled) {
		t.Fatalf("leader result=%v/%v, want nil/context.Canceled", leader.invoker, leader.err)
	}
	for index := range waiterCount {
		waiter := awaitResolveResult(
			t,
			waiterResult,
			fmt.Sprintf("live waiter %d result", index),
		)
		if waiter.err != nil || waiter.invoker != invoker {
			t.Fatalf("live waiter result=%v/%v, want invoker/nil", waiter.invoker, waiter.err)
		}
	}
	if loaderCalls.Load() != 2 {
		t.Fatalf("leader handoff Loader calls=%d, want 2", loaderCalls.Load())
	}
	resolved, err := registry.ResolveExact(
		context.Background(),
		artifact,
		"builtin.shared-cancel",
	)
	if err != nil || resolved != invoker || loaderCalls.Load() != 2 {
		t.Fatalf(
			"post-cancellation retry=%v error=%v Loader calls=%d",
			resolved,
			err,
			loaderCalls.Load(),
		)
	}
}

func TestRegistrySharesOrdinaryFailureWithoutContextHandoff(t *testing.T) {
	artifact := strings.Repeat("9", 64)
	invoker := &stubInvoker{}
	loadFailure := errors.New("independent Loader failure")
	loaderStarted := make(chan struct{})
	releaseLoader := make(chan struct{})
	var loaderCalls atomic.Int32
	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			_ context.Context,
			_ string,
			_ string,
		) (modulehost.ModuleInvoker, error) {
			if loaderCalls.Add(1) == 1 {
				close(loaderStarted)
				<-releaseLoader
				return invoker, loadFailure
			}
			return invoker, nil
		},
	)
	if err != nil {
		t.Fatalf("NewRegistryWithLoader: %v", err)
	}

	leaderContext, cancelLeader := context.WithCancel(context.Background())
	leaderResult := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			leaderContext,
			artifact,
			"builtin.ordinary-failure",
		)
		leaderResult <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	awaitSignal(t, loaderStarted, "ordinary-failure Loader start")

	waitContext := &waitObservedContext{
		Context:  context.Background(),
		observed: make(chan struct{}),
	}
	waiterResult := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			waitContext,
			artifact,
			"builtin.ordinary-failure",
		)
		waiterResult <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	awaitSignal(t, waitContext.observed, "ordinary-failure waiter flight wait")
	cancelLeader()
	close(releaseLoader)

	for label, result := range map[string]resolveResult{
		"leader": awaitResolveResult(t, leaderResult, "ordinary-failure leader"),
		"waiter": awaitResolveResult(t, waiterResult, "ordinary-failure waiter"),
	} {
		if result.invoker != nil || !errors.Is(result.err, loadFailure) {
			t.Fatalf("%s result=%v/%v, want nil/loadFailure", label, result.invoker, result.err)
		}
	}
	if loaderCalls.Load() != 1 {
		t.Fatalf("ordinary failure Loader calls=%d, want 1", loaderCalls.Load())
	}

	resolved, err := registry.ResolveExact(
		context.Background(),
		artifact,
		"builtin.ordinary-failure",
	)
	if err != nil || resolved != invoker || loaderCalls.Load() != 2 {
		t.Fatalf(
			"fresh retry=%v error=%v Loader calls=%d",
			resolved,
			err,
			loaderCalls.Load(),
		)
	}
}

func TestRegistryDoesNotCacheTypedNilLoaderResult(t *testing.T) {
	artifact := strings.Repeat("2", 64)
	invoker := &stubInvoker{}
	var typedNil *stubInvoker
	var loaderCalls atomic.Int32
	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			context.Context,
			string,
			string,
		) (modulehost.ModuleInvoker, error) {
			if loaderCalls.Add(1) == 1 {
				return typedNil, nil
			}
			return invoker, nil
		},
	)
	if err != nil {
		t.Fatalf("NewRegistryWithLoader: %v", err)
	}
	if _, err := registry.ResolveExact(
		context.Background(),
		artifact,
		"builtin.typed-nil",
	); !errors.Is(err, exactadapter.ErrInvalidAdapter) {
		t.Fatalf("typed-nil Loader error=%v, want ErrInvalidAdapter", err)
	}
	registered, err := registry.IsRegistered(
		context.Background(),
		artifact,
		"builtin.typed-nil",
	)
	if err != nil || registered {
		t.Fatalf("typed-nil IsRegistered=%v error=%v, want false/nil", registered, err)
	}
	resolved, err := registry.ResolveExact(
		context.Background(),
		artifact,
		"builtin.typed-nil",
	)
	if err != nil || resolved != invoker || loaderCalls.Load() != 2 {
		t.Fatalf(
			"typed-nil retry=%v error=%v Loader calls=%d",
			resolved,
			err,
			loaderCalls.Load(),
		)
	}
}

func TestRegistryCleansInFlightKeyBeforeRethrowingLoaderPanic(t *testing.T) {
	artifact := strings.Repeat("3", 64)
	invoker := &stubInvoker{}
	loaderStarted := make(chan struct{})
	panicNow := make(chan struct{})
	var loaderCalls atomic.Int32
	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			context.Context,
			string,
			string,
		) (modulehost.ModuleInvoker, error) {
			if loaderCalls.Add(1) == 1 {
				close(loaderStarted)
				<-panicNow
				panic("synthetic Loader panic")
			}
			return invoker, nil
		},
	)
	if err != nil {
		t.Fatalf("NewRegistryWithLoader: %v", err)
	}

	leaderPanic := make(chan any, 1)
	go func() {
		defer func() {
			leaderPanic <- recover()
		}()
		_, _ = registry.ResolveExact(
			context.Background(),
			artifact,
			"builtin.panic",
		)
	}()
	awaitSignal(t, loaderStarted, "panicking Loader start")

	waitContext := &waitObservedContext{
		Context:  context.Background(),
		observed: make(chan struct{}),
	}
	waiterResult := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			waitContext,
			artifact,
			"builtin.panic",
		)
		waiterResult <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	awaitSignal(t, waitContext.observed, "panic waiter flight wait")

	probeContext := &waitObservedContext{
		Context:  context.Background(),
		observed: make(chan struct{}),
	}
	probeResult := make(chan registrationResult, 1)
	go func() {
		registered, probeErr := registry.IsRegistered(
			probeContext,
			artifact,
			"builtin.panic",
		)
		probeResult <- registrationResult{registered: registered, err: probeErr}
	}()
	awaitSignal(t, probeContext.observed, "panic probe flight wait")
	close(panicNow)

	select {
	case recovered := <-leaderPanic:
		if recovered == nil {
			t.Fatal("ResolveExact did not rethrow Loader panic")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Loader panic")
	}
	waiter := awaitResolveResult(t, waiterResult, "panic waiter result")
	if waiter.invoker != nil || !errors.Is(waiter.err, exactadapter.ErrInvalidAdapter) {
		t.Fatalf("panic waiter=%v/%v, want nil/ErrInvalidAdapter", waiter.invoker, waiter.err)
	}
	probe := awaitRegistrationResult(t, probeResult, "panic probe result")
	if probe.registered || probe.err != nil {
		t.Fatalf("panic probe=%v/%v, want false/nil", probe.registered, probe.err)
	}

	resultChannel := make(chan resolveResult, 1)
	go func() {
		resolved, resolveErr := registry.ResolveExact(
			context.Background(),
			artifact,
			"builtin.panic",
		)
		resultChannel <- resolveResult{invoker: resolved, err: resolveErr}
	}()
	result := awaitResolveResult(t, resultChannel, "post-panic retry")
	if result.err != nil || result.invoker != invoker || loaderCalls.Load() != 2 {
		t.Fatalf(
			"post-panic retry=%v error=%v Loader calls=%d",
			result.invoker,
			result.err,
			loaderCalls.Load(),
		)
	}
}

type resolveResult struct {
	invoker modulehost.ModuleInvoker
	err     error
}

type registrationResult struct {
	registered bool
	err        error
}

type waitObservedContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (ctx *waitObservedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

func awaitSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func awaitString(t *testing.T, values <-chan string, label string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return ""
	}
}

func awaitResolveResult(
	t *testing.T,
	results <-chan resolveResult,
	label string,
) resolveResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return resolveResult{}
	}
}

func awaitRegistrationResult(
	t *testing.T,
	results <-chan registrationResult,
	label string,
) registrationResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return registrationResult{}
	}
}

type stubInvoker struct{}

func (*stubInvoker) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return modulehost.InvocationResult{}, nil
}
