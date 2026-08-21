// Package modulesource observes one exact module discovery index and freezes it
// into an authority-free discovery snapshot. It owns no Store, Secret, Module
// Host, package downloader, installer, activation path, or Apply authority.
package modulesource

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	defaultObservationTimeout = 15 * time.Second
	maximumObservationTimeout = 60 * time.Second
	minimumObservationTimeout = time.Millisecond
	maximumHTTPSOrigins       = 32
)

// FailureCode is the closed, non-sensitive failure classification exposed by
// this observation-only package. Dynamic URLs, addresses, response data, and
// operating-system errors never appear in Error().
type FailureCode string

const (
	FailureSourceInputInvalid FailureCode = "SOURCE_INPUT_INVALID"
	FailureSourceDisabled     FailureCode = "SOURCE_DISABLED"
	FailureSourceDenied       FailureCode = "SOURCE_DENIED"
	FailureSourceUnavailable  FailureCode = "SOURCE_UNAVAILABLE"
	FailureSourceIndexInvalid FailureCode = "SOURCE_INDEX_INVALID"
	FailureSourceDrift        FailureCode = "SOURCE_DRIFT"
	FailureSourceCancelled    FailureCode = "SOURCE_CANCELLED"
	FailureInternal           FailureCode = "INTERNAL_ERROR"
)

// Error retains an in-process cause for errors.Is/errors.As while presenting
// only a fixed code at command or logging boundaries.
type Error struct {
	code  FailureCode
	cause error
}

func (failure *Error) Error() string {
	if failure == nil {
		return "modulesource: observation failed (INTERNAL_ERROR)"
	}
	return fmt.Sprintf("modulesource: observation failed (%s)", failure.code)
}

func (failure *Error) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

// FailureCodeOf returns a closed code. Unknown dependency errors collapse to
// INTERNAL_ERROR rather than crossing the observation boundary verbatim.
func FailureCodeOf(err error) FailureCode {
	if err == nil {
		return ""
	}
	var failure *Error
	if errors.As(err, &failure) && failure != nil && validFailureCode(failure.code) {
		return failure.code
	}
	return FailureInternal
}

func validFailureCode(code FailureCode) bool {
	switch code {
	case FailureSourceInputInvalid,
		FailureSourceDisabled,
		FailureSourceDenied,
		FailureSourceUnavailable,
		FailureSourceIndexInvalid,
		FailureSourceDrift,
		FailureSourceCancelled,
		FailureInternal:
		return true
	default:
		return false
	}
}

func observationFailure(code FailureCode, cause error) error {
	if !validFailureCode(code) {
		code = FailureInternal
	}
	return &Error{code: code, cause: cause}
}

// Config is trusted process configuration. HTTPS is disabled by default. The
// origin allowlist is copied, validated, sorted, and frozen by New.
type Config struct {
	HTTPSIndexEnabled    bool
	HTTPSOriginAllowlist []string
	Timeout              time.Duration
}

// ObserveRequest selects exactly one location matching the frozen Source
// Policy kind. Policy bytes and identifiers are copied before any I/O.
type ObserveRequest struct {
	SourcePolicyCanonical []byte
	SourcePolicyID        string
	LocalDirectory        string
	HTTPSIndexURL         string
}

// Observation contains only immutable, authority-free content identities and
// owned canonical bytes. PackagePath values remain opaque source data.
type Observation struct {
	SourcePolicyID    string
	Index             moduleapi.ModuleDiscoveryIndexV1
	IndexCanonical    []byte
	IndexID           string
	Snapshot          moduleapi.ModuleDiscoverySnapshotV1
	SnapshotCanonical []byte
	SnapshotID        string
}

type httpsDependencies struct {
	lookupIPAddr func(context.Context, string) ([]net.IPAddr, error)
	dialContext  func(context.Context, string, string) (net.Conn, error)
	tlsConfig    *tls.Config
}

// Provider is immutable after construction and safe for concurrent, explicit
// observations. Every HTTPS observation constructs a fresh transport/client.
type Provider struct {
	httpsEnabled bool
	httpsOrigins map[string]struct{}
	timeout      time.Duration
	https        httpsDependencies
}

// New constructs the production provider. It intentionally accepts no HTTP
// client, proxy, credential, Secret resolver, cookie jar, or header injection.
func New(config Config) (*Provider, error) {
	dialer := &net.Dialer{
		Timeout:   normalizedTimeout(config.Timeout),
		KeepAlive: -1,
	}
	return newProvider(config, httpsDependencies{
		lookupIPAddr: net.DefaultResolver.LookupIPAddr,
		dialContext:  dialer.DialContext,
	})
}

// newProvider is an unexported hermetic-test seam. Production callers cannot
// replace DNS, dialing, TLS, redirect, proxy, retry, or response policy.
func newProvider(config Config, dependencies httpsDependencies) (*Provider, error) {
	timeout := normalizedTimeout(config.Timeout)
	if timeout < minimumObservationTimeout || timeout > maximumObservationTimeout {
		return nil, observationFailure(
			FailureSourceInputInvalid,
			errors.New("observation timeout is outside the supported range"),
		)
	}
	if !config.HTTPSIndexEnabled && len(config.HTTPSOriginAllowlist) != 0 {
		return nil, observationFailure(
			FailureSourceInputInvalid,
			errors.New("disabled HTTPS source has an origin allowlist"),
		)
	}
	if config.HTTPSIndexEnabled &&
		(len(config.HTTPSOriginAllowlist) == 0 ||
			len(config.HTTPSOriginAllowlist) > maximumHTTPSOrigins) {
		return nil, observationFailure(
			FailureSourceInputInvalid,
			errors.New("HTTPS origin allowlist size is invalid"),
		)
	}

	origins := make([]string, len(config.HTTPSOriginAllowlist))
	copy(origins, config.HTTPSOriginAllowlist)
	for index, origin := range origins {
		canonical, err := parseCanonicalHTTPSOrigin(origin)
		if err != nil || canonical != origin {
			return nil, observationFailure(
				FailureSourceInputInvalid,
				fmt.Errorf("HTTPS origin allowlist entry %d is invalid", index),
			)
		}
	}
	sort.Strings(origins)
	for index := 1; index < len(origins); index++ {
		if origins[index-1] == origins[index] {
			return nil, observationFailure(
				FailureSourceInputInvalid,
				errors.New("HTTPS origin allowlist contains a duplicate"),
			)
		}
	}
	frozenOrigins := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		frozenOrigins[origin] = struct{}{}
	}

	if config.HTTPSIndexEnabled &&
		(dependencies.lookupIPAddr == nil || dependencies.dialContext == nil) {
		return nil, observationFailure(
			FailureInternal,
			errors.New("HTTPS dependencies are unavailable"),
		)
	}
	var tlsConfig *tls.Config
	if dependencies.tlsConfig != nil {
		tlsConfig = dependencies.tlsConfig.Clone()
	}
	return &Provider{
		httpsEnabled: config.HTTPSIndexEnabled,
		httpsOrigins: frozenOrigins,
		timeout:      timeout,
		https: httpsDependencies{
			lookupIPAddr: dependencies.lookupIPAddr,
			dialContext:  dependencies.dialContext,
			tlsConfig:    tlsConfig,
		},
	}, nil
}

func normalizedTimeout(value time.Duration) time.Duration {
	if value == 0 {
		return defaultObservationTimeout
	}
	return value
}

// Observe reads or fetches exactly one canonical discovery index and freezes
// one Snapshot. It never reads a package path, resolves a Secret, opens a
// Store, installs, stages, activates, binds, executes, or applies a module.
func (provider *Provider) Observe(
	ctx context.Context,
	request ObserveRequest,
) (Observation, error) {
	if provider == nil {
		return Observation{}, observationFailure(
			FailureInternal,
			errors.New("provider is nil"),
		)
	}
	if ctx == nil {
		return Observation{}, observationFailure(
			FailureSourceInputInvalid,
			errors.New("context is nil"),
		)
	}
	if err := ctx.Err(); err != nil {
		return Observation{}, cancelledObservation(err)
	}

	policyCanonical := bytes.Clone(request.SourcePolicyCanonical)
	policyID := request.SourcePolicyID
	policy, err := moduleapi.RestoreModuleSourcePolicyV1(
		policyCanonical,
		policyID,
	)
	if err != nil {
		return Observation{}, observationFailure(FailureSourceInputInvalid, err)
	}

	operationContext, cancel := context.WithTimeout(ctx, provider.timeout)
	defer cancel()

	var indexBytes []byte
	switch policy.Kind {
	case moduleapi.ModuleSourceKindLocalDirectoryV1:
		if policy.Network != moduleapi.ModuleSourceNetworkDenyV1 ||
			request.LocalDirectory == "" || request.HTTPSIndexURL != "" {
			return Observation{}, observationFailure(
				FailureSourceInputInvalid,
				errors.New("LOCAL_DIRECTORY request does not match its policy"),
			)
		}
		indexBytes, err = provider.observeLocalIndex(
			operationContext,
			policy,
			request.LocalDirectory,
		)
	case moduleapi.ModuleSourceKindHTTPSIndexV1:
		if policy.Network != moduleapi.ModuleSourceNetworkExactHTTPSV1 ||
			request.LocalDirectory != "" || request.HTTPSIndexURL == "" {
			return Observation{}, observationFailure(
				FailureSourceInputInvalid,
				errors.New("HTTPS_INDEX request does not match its policy"),
			)
		}
		if !provider.httpsEnabled {
			return Observation{}, observationFailure(
				FailureSourceDisabled,
				errors.New("HTTPS index observation is disabled"),
			)
		}
		indexBytes, err = provider.observeHTTPSIndex(
			operationContext,
			policy,
			request.HTTPSIndexURL,
		)
	default:
		return Observation{}, observationFailure(
			FailureSourceInputInvalid,
			errors.New("unsupported source kind"),
		)
	}
	if err != nil {
		return Observation{}, classifyObservationError(operationContext, err)
	}
	if err := operationContext.Err(); err != nil {
		return Observation{}, cancelledObservation(err)
	}

	index, indexCanonical, indexID, err := moduleapi.ParseModuleDiscoveryIndexV1(indexBytes)
	if err != nil {
		return Observation{}, observationFailure(FailureSourceIndexInvalid, err)
	}
	if err := operationContext.Err(); err != nil {
		return Observation{}, cancelledObservation(err)
	}
	snapshot, snapshotCanonical, snapshotID, err := moduleapi.NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		indexCanonical,
		indexID,
	)
	if err != nil {
		return Observation{}, observationFailure(FailureSourceIndexInvalid, err)
	}
	if err := operationContext.Err(); err != nil {
		return Observation{}, cancelledObservation(err)
	}

	return Observation{
		SourcePolicyID:    policyID,
		Index:             cloneDiscoveryIndex(index),
		IndexCanonical:    bytes.Clone(indexCanonical),
		IndexID:           indexID,
		Snapshot:          cloneDiscoverySnapshot(snapshot),
		SnapshotCanonical: bytes.Clone(snapshotCanonical),
		SnapshotID:        snapshotID,
	}, nil
}

func cloneDiscoveryIndex(
	index moduleapi.ModuleDiscoveryIndexV1,
) moduleapi.ModuleDiscoveryIndexV1 {
	index.Entries = append([]moduleapi.ModuleDiscoveryEntryV1(nil), index.Entries...)
	return index
}

func cloneDiscoverySnapshot(
	snapshot moduleapi.ModuleDiscoverySnapshotV1,
) moduleapi.ModuleDiscoverySnapshotV1 {
	snapshot.Entries = append(
		[]moduleapi.ModuleDiscoveryEntryV1(nil),
		snapshot.Entries...,
	)
	return snapshot
}

func cancelledObservation(err error) error {
	return observationFailure(FailureSourceCancelled, err)
}

func classifyObservationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return cancelledObservation(err)
	}
	if ctx != nil && ctx.Err() != nil {
		return cancelledObservation(ctx.Err())
	}
	var failure *Error
	if errors.As(err, &failure) && failure != nil && validFailureCode(failure.code) {
		return failure
	}
	return observationFailure(FailureInternal, err)
}
