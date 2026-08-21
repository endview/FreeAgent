// Package exactadapter contains the immutable, deployment-local adapter
// registry used by the S1 Module Host and deterministic local adapters.
package exactadapter

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	// ErrInvalidAdapter identifies a malformed exact identity, an empty
	// registry, or a value that is not a non-nil ModuleInvoker.
	ErrInvalidAdapter = errors.New("exactadapter: invalid adapter")

	// ErrDuplicateAdapter identifies two registrations for the same exact
	// artifact digest and adapter identity.
	ErrDuplicateAdapter = errors.New(
		"exactadapter: duplicate exact adapter registration",
	)

	// ErrAdapterNotFound identifies an exact key that is not registered. It
	// never causes a name, version, Port, or best-match search.
	ErrAdapterNotFound = errors.New("exactadapter: exact adapter not found")
)

// Registration is one composition-root-owned exact adapter registration.
// Invoker is accepted as any so the runtime boundary can reject wrong dynamic
// types and typed nils instead of hiding them behind a panic.
type Registration struct {
	ArtifactDigest  string
	AdapterIdentity string
	Invoker         any
}

// Loader materializes one implementation only after ResolveExact requests its
// complete artifact-digest plus adapter-identity key. Different exact keys may
// be loaded concurrently, so the function must be concurrency-safe. It must
// observe Context cancellation at every blocking boundary it controls and
// must not execute a module, create an external effect, perform an unbounded
// detached operation, or derive a cached implementation from Context values
// such as Run, Workspace, Tenant, or Authority. It must not call back into its
// Registry or search by module name, version, Port, Instance, or recency.
type Loader func(
	context.Context,
	string,
	string,
) (modulehost.ModuleInvoker, error)

type adapterKey struct {
	artifactDigest  string
	adapterIdentity string
}

type loadAttempt struct {
	done                    chan struct{}
	invoker                 modulehost.ModuleInvoker
	err                     error
	leaderContextTerminated bool
}

// Registry has immutable resolution policy and is safe for concurrent use.
// A configured Loader may populate the private exact-key cache on first use.
// Concurrent misses for one exact key share one in-flight result while
// different keys can load independently. The first caller's Context governs
// that one load attempt and waiters may leave on their own Context. A live
// waiter continues its original resolution when only the leader's Context
// ended; ordinary Loader failures are shared and no unsuccessful result is
// cached. Callers still cannot mutate or enumerate registrations.
type Registry struct {
	mu       sync.RWMutex
	adapters map[adapterKey]modulehost.ModuleInvoker
	loading  map[adapterKey]*loadAttempt
	loader   Loader
}

var _ modulehost.ExactAdapterRegistry = (*Registry)(nil)

// NewRegistry validates and copies a non-empty set of exact registrations.
func NewRegistry(registrations ...Registration) (*Registry, error) {
	return newRegistry(nil, registrations...)
}

// NewRegistryWithLoader builds the same single exact Registry with optional
// eager registrations and one exact-key lazy materializer. IsRegistered never
// invokes the Loader; only ResolveExact does. Its caller must complete every
// required authorization check before requesting resolution.
func NewRegistryWithLoader(
	loader Loader,
	registrations ...Registration,
) (*Registry, error) {
	if loader == nil {
		return nil, fmt.Errorf("%w: loader is nil", ErrInvalidAdapter)
	}
	return newRegistry(loader, registrations...)
}

func newRegistry(
	loader Loader,
	registrations ...Registration,
) (*Registry, error) {
	if len(registrations) == 0 && loader == nil {
		return nil, fmt.Errorf(
			"%w: at least one registration is required",
			ErrInvalidAdapter,
		)
	}
	adapters := make(
		map[adapterKey]modulehost.ModuleInvoker,
		len(registrations),
	)
	for index, registration := range registrations {
		key, err := newAdapterKey(
			registration.ArtifactDigest,
			registration.AdapterIdentity,
		)
		if err != nil {
			return nil, fmt.Errorf("registration %d: %w", index, err)
		}
		invoker, ok := registration.Invoker.(modulehost.ModuleInvoker)
		if !ok || isNilAdapter(invoker) {
			return nil, fmt.Errorf(
				"%w: registration %d invoker must implement modulehost.ModuleInvoker and be non-nil",
				ErrInvalidAdapter,
				index,
			)
		}
		if _, duplicate := adapters[key]; duplicate {
			return nil, fmt.Errorf(
				"%w: artifact %s adapter %q",
				ErrDuplicateAdapter,
				key.artifactDigest,
				key.adapterIdentity,
			)
		}
		adapters[key] = invoker
	}
	registry := &Registry{adapters: adapters, loader: loader}
	if loader != nil {
		registry.loading = make(map[adapterKey]*loadAttempt)
	}
	return registry, nil
}

// ResolveExact implements modulehost.ExactAdapterRegistry. Only the complete
// artifact-digest plus adapter-identity key is considered.
func (registry *Registry) ResolveExact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
) (modulehost.ModuleInvoker, error) {
	if registry == nil || registry.adapters == nil {
		return nil, fmt.Errorf("%w: registry is nil", ErrInvalidAdapter)
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidAdapter)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("exactadapter: resolve exact adapter: %w", err)
	}
	key, err := newAdapterKey(artifactDigest, adapterIdentity)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("exactadapter: resolve exact adapter: %w", err)
		}
		registry.mu.RLock()
		invoker, found := registry.adapters[key]
		registry.mu.RUnlock()
		if found {
			return invoker, nil
		}
		if registry.loader == nil {
			return nil, adapterNotFound(key)
		}

		registry.mu.Lock()
		if invoker, found = registry.adapters[key]; found {
			registry.mu.Unlock()
			return invoker, nil
		}
		if attempt, loading := registry.loading[key]; loading {
			registry.mu.Unlock()
			invoker, retry, waitErr := waitForLoad(ctx, attempt)
			if retry {
				continue
			}
			return invoker, waitErr
		}
		if err := ctx.Err(); err != nil {
			registry.mu.Unlock()
			return nil, fmt.Errorf("exactadapter: resolve exact adapter: %w", err)
		}
		attempt := &loadAttempt{done: make(chan struct{})}
		registry.loading[key] = attempt
		registry.mu.Unlock()
		return registry.loadExact(ctx, key, attempt)
	}
}

func (registry *Registry) loadExact(
	ctx context.Context,
	key adapterKey,
	attempt *loadAttempt,
) (invoker modulehost.ModuleInvoker, err error) {
	published := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if !published {
				registry.completeLoad(
					key,
					attempt,
					nil,
					fmt.Errorf("%w: loader panicked", ErrInvalidAdapter),
					false,
				)
			}
			panic(recovered)
		}
	}()

	invoker, err = registry.loader(
		ctx,
		key.artifactDigest,
		key.adapterIdentity,
	)
	leaderContextTerminated := false
	if contextErr := ctx.Err(); contextErr != nil {
		if err == nil {
			err = contextErr
		}
		leaderContextTerminated = errors.Is(err, contextErr)
	}
	if err != nil {
		invoker = nil
		err = fmt.Errorf(
			"exactadapter: load artifact %s adapter %q: %w",
			key.artifactDigest,
			key.adapterIdentity,
			err,
		)
	} else if isNilAdapter(invoker) {
		err = fmt.Errorf(
			"%w: loader returned a nil invoker",
			ErrInvalidAdapter,
		)
		invoker = nil
	}
	registry.completeLoad(
		key,
		attempt,
		invoker,
		err,
		leaderContextTerminated,
	)
	published = true
	return invoker, err
}

func (registry *Registry) completeLoad(
	key adapterKey,
	attempt *loadAttempt,
	invoker modulehost.ModuleInvoker,
	err error,
	leaderContextTerminated bool,
) {
	registry.mu.Lock()
	attempt.invoker = invoker
	attempt.err = err
	attempt.leaderContextTerminated = leaderContextTerminated
	if err == nil {
		registry.adapters[key] = invoker
	}
	delete(registry.loading, key)
	close(attempt.done)
	registry.mu.Unlock()
}

func waitForLoad(
	ctx context.Context,
	attempt *loadAttempt,
) (modulehost.ModuleInvoker, bool, error) {
	select {
	case <-attempt.done:
		if err := ctx.Err(); err != nil {
			return nil, false, fmt.Errorf(
				"exactadapter: resolve exact adapter: %w",
				err,
			)
		}
		if attempt.leaderContextTerminated {
			return nil, true, nil
		}
		return attempt.invoker, false, attempt.err
	case <-ctx.Done():
		return nil, false, fmt.Errorf(
			"exactadapter: resolve exact adapter: %w",
			ctx.Err(),
		)
	}
}

func adapterNotFound(key adapterKey) error {
	return fmt.Errorf(
		"%w: artifact %s adapter %q",
		ErrAdapterNotFound,
		key.artifactDigest,
		key.adapterIdentity,
	)
}

// IsRegistered is the read-only structural subset consumed by
// activationresolver.ExactAdapterRegistryProbe. It performs the same exact
// lookup as ResolveExact and does not introduce a second catalog. It never
// starts loading; when the same exact key is already loading, it waits for that
// attempt or its own Context cancellation and then reports the cache state. In
// a Loader-backed Registry, true means only that an implementation has already
// been materialized; it is not proof of loadability, Trust, Catalog membership,
// or authorization. Activation must use an independently constructed eager
// exact-registration view.
func (registry *Registry) IsRegistered(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
) (bool, error) {
	if registry == nil || registry.adapters == nil {
		return false, fmt.Errorf("%w: registry is nil", ErrInvalidAdapter)
	}
	if ctx == nil {
		return false, fmt.Errorf("%w: context is nil", ErrInvalidAdapter)
	}
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("exactadapter: probe exact adapter: %w", err)
	}
	key, err := newAdapterKey(artifactDigest, adapterIdentity)
	if err != nil {
		return false, err
	}
	registry.mu.RLock()
	_, found := registry.adapters[key]
	attempt := registry.loading[key]
	registry.mu.RUnlock()
	if found || attempt == nil {
		return found, nil
	}
	select {
	case <-attempt.done:
		registry.mu.RLock()
		_, found = registry.adapters[key]
		registry.mu.RUnlock()
		return found, nil
	case <-ctx.Done():
		return false, fmt.Errorf("exactadapter: probe exact adapter: %w", ctx.Err())
	}
}

func newAdapterKey(
	artifactDigest string,
	adapterIdentity string,
) (adapterKey, error) {
	if !moduleapi.ValidSHA256(artifactDigest) {
		return adapterKey{}, fmt.Errorf(
			"%w: artifact digest must be a lowercase SHA-256 digest",
			ErrInvalidAdapter,
		)
	}
	if !validAdapterIdentity(adapterIdentity) {
		return adapterKey{}, fmt.Errorf(
			"%w: adapter identity must be a canonical opaque ID",
			ErrInvalidAdapter,
		)
	}
	return adapterKey{
		artifactDigest:  artifactDigest,
		adapterIdentity: adapterIdentity,
	}, nil
}

func validAdapterIdentity(value string) bool {
	if value == "" ||
		value != strings.TrimSpace(value) ||
		len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func isNilAdapter(value any) bool {
	if value == nil {
		return true
	}
	reflection := reflect.ValueOf(value)
	switch reflection.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflection.IsNil()
	default:
		return false
	}
}
