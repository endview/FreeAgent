// Package activationresolver resolves one installed module into the only S1
// activation assignment Core is allowed to persist.
package activationresolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	activationIDDomain      = "freeagent.module-activation/v1"
	moduleManifestMediaType = "application/json"
	maxOpaqueIDBytes        = 256
)

var (
	// ErrInvalidInput identifies malformed resolver configuration or input.
	ErrInvalidInput = errors.New("activationresolver: invalid input")

	// ErrTrustedModuleNotAllowlisted means the exact installed artifact has no
	// local Core grant for trusted in-process execution.
	ErrTrustedModuleNotAllowlisted = errors.New(
		"activationresolver: trusted module is not allowlisted",
	)

	// ErrLocalProcessModuleNotGranted means the exact installed artifact and
	// protocol have no Core-owned LOCAL_PROCESS grant.
	ErrLocalProcessModuleNotGranted = errors.New(
		"activationresolver: local process module is not granted",
	)

	// ErrRemoteActionModuleNotGranted means the exact installed artifact and
	// protocol have no Core-owned REMOTE Action grant.
	ErrRemoteActionModuleNotGranted = errors.New(
		"activationresolver: remote action module is not granted",
	)

	// ErrWASMActionModuleNotGranted means the exact installed artifact and
	// protocol have no Core-owned WASM Action Host grant.
	ErrWASMActionModuleNotGranted = errors.New(
		"activationresolver: wasm action module is not granted",
	)

	// ErrAdapterNotRegistered means an allowlisted exact adapter is absent
	// from the local read-only registry view.
	ErrAdapterNotRegistered = errors.New(
		"activationresolver: exact adapter is not registered",
	)

	// ErrRuntimeModeUnavailable means an installation is valid but its
	// requested Host adapter is deliberately unavailable in S1.
	ErrRuntimeModeUnavailable = errors.New(
		"activationresolver: runtime mode is unavailable in S1",
	)

	// ErrAssertionMismatch means an optional import/seed assertion disagrees
	// with the independently resolved Core assignment.
	ErrAssertionMismatch = errors.New(
		"activationresolver: expected assignment does not match",
	)
)

// ExactAdapterRegistryProbe is the read-only subset of the local adapter
// registry needed during activation. It supports no registration, fallback,
// enumeration, or best-match lookup.
type ExactAdapterRegistryProbe interface {
	IsRegistered(
		context.Context,
		string, // artifact digest
		string, // adapter identity
	) (bool, error)
}

// TrustedInProcessAllowlistEntry is one deployment-local trust grant. All
// three package identity fields must match before AdapterIdentity is used.
type TrustedInProcessAllowlistEntry struct {
	ModuleID        string
	ExactVersion    string
	ArtifactDigest  string
	AdapterIdentity string
}

// LocalProcessGrant is one Core-owned assignment for an exact package and
// Host protocol. Manifest and seed assertions cannot create this grant.
type LocalProcessGrant struct {
	ModuleID        string
	ExactVersion    string
	ArtifactDigest  string
	Protocol        string
	AdapterIdentity string
}

// RemoteActionGrant is one Core-owned REMOTE Action Host assignment for an
// exact package and protocol. Endpoint, SecretRef, network authority, and
// resource ceilings are deliberately absent: they belong to the frozen
// Binding Config/Authority rather than an installation or activation.
type RemoteActionGrant struct {
	ModuleID        string
	ExactVersion    string
	ArtifactDigest  string
	Protocol        string
	AdapterIdentity string
}

// WASMActionGrant is one Core-owned pure-compute WASM Action Host assignment
// for an exact package and protocol. It grants no WASI, filesystem, network,
// environment, clock, random, Secret, Store, or generic host-call capability.
type WASMActionGrant struct {
	ModuleID        string
	ExactVersion    string
	ArtifactDigest  string
	Protocol        string
	AdapterIdentity string
}

// Config is immutable resolver configuration supplied by the local Core
// composition root, never by a module manifest, seed, or Current Store row.
type Config struct {
	DeclarativeAdapterIdentity string
	TrustedInProcessAllowlist  []TrustedInProcessAllowlistEntry
	LocalProcessGrants         []LocalProcessGrant
	RemoteActionGrants         []RemoteActionGrant
	WASMActionGrants           []WASMActionGrant
}

// ResolveInput contains the immutable installation and Core-owned activation
// coordinates. Expected fields are assertions only and never participate in
// authorization or ActivationID derivation.
type ResolveInput struct {
	Installation       currentstore.ModuleInstallation
	TenantID           string
	InstanceID         string
	ActivationRevision uint64

	ExpectedExecutionClass  moduleapi.ExecutionClass
	ExpectedAdapterIdentity string
}

type trustedArtifactKey struct {
	moduleID       string
	exactVersion   string
	artifactDigest string
}

type localProcessAssignment struct {
	protocol        string
	adapterIdentity string
}

type remoteActionAssignment struct {
	protocol        string
	adapterIdentity string
}

type wasmActionAssignment struct {
	protocol        string
	adapterIdentity string
}

// Resolver is an immutable, concurrency-safe activation resolver, assuming
// its registry probe is safe for concurrent reads.
type Resolver struct {
	declarativeAdapterIdentity string
	trusted                    map[trustedArtifactKey]string
	localProcess               map[trustedArtifactKey]localProcessAssignment
	remoteAction               map[trustedArtifactKey]remoteActionAssignment
	wasmAction                 map[trustedArtifactKey]wasmActionAssignment
	registry                   ExactAdapterRegistryProbe
}

// New constructs the sole minimal S1 resolver. Trusted allowlist entries are
// copied into an exact-key map and cannot be changed through Config afterward.
func New(
	config Config,
	registry ExactAdapterRegistryProbe,
) (*Resolver, error) {
	if err := validateOpaque(
		"declarative adapter identity",
		config.DeclarativeAdapterIdentity,
	); err != nil {
		return nil, err
	}
	if (len(config.TrustedInProcessAllowlist) > 0 ||
		len(config.LocalProcessGrants) > 0 ||
		len(config.RemoteActionGrants) > 0 ||
		len(config.WASMActionGrants) > 0) &&
		isNilDependency(registry) {
		return nil, fmt.Errorf(
			"%w: exact adapter registry probe is nil",
			ErrInvalidInput,
		)
	}

	trusted := make(
		map[trustedArtifactKey]string,
		len(config.TrustedInProcessAllowlist),
	)
	for index, entry := range config.TrustedInProcessAllowlist {
		ref := moduleapi.Ref{
			ID:      entry.ModuleID,
			Version: entry.ExactVersion,
		}
		if err := ref.Validate(); err != nil {
			return nil, fmt.Errorf(
				"%w: trusted allowlist entry %d identity: %v",
				ErrInvalidInput,
				index,
				err,
			)
		}
		if !moduleapi.ValidSHA256(entry.ArtifactDigest) {
			return nil, fmt.Errorf(
				"%w: trusted allowlist entry %d artifact digest is invalid",
				ErrInvalidInput,
				index,
			)
		}
		if err := validateOpaque(
			"trusted allowlist adapter identity",
			entry.AdapterIdentity,
		); err != nil {
			return nil, fmt.Errorf("entry %d: %w", index, err)
		}
		key := trustedArtifactKey{
			moduleID:       entry.ModuleID,
			exactVersion:   entry.ExactVersion,
			artifactDigest: entry.ArtifactDigest,
		}
		if _, duplicate := trusted[key]; duplicate {
			return nil, fmt.Errorf(
				"%w: duplicate trusted allowlist entry %d for %s@%s artifact %s",
				ErrInvalidInput,
				index,
				entry.ModuleID,
				entry.ExactVersion,
				entry.ArtifactDigest,
			)
		}
		trusted[key] = entry.AdapterIdentity
	}

	localProcess := make(
		map[trustedArtifactKey]localProcessAssignment,
		len(config.LocalProcessGrants),
	)
	for index, grant := range config.LocalProcessGrants {
		ref := moduleapi.Ref{
			ID:      grant.ModuleID,
			Version: grant.ExactVersion,
		}
		if err := ref.Validate(); err != nil {
			return nil, fmt.Errorf(
				"%w: local process grant %d identity: %v",
				ErrInvalidInput,
				index,
				err,
			)
		}
		if !moduleapi.ValidSHA256(grant.ArtifactDigest) {
			return nil, fmt.Errorf(
				"%w: local process grant %d artifact digest is invalid",
				ErrInvalidInput,
				index,
			)
		}
		if grant.Protocol != moduleapi.RuntimeProtocolMCPStdio20251125 {
			return nil, fmt.Errorf(
				"%w: local process grant %d protocol must be %q",
				ErrInvalidInput,
				index,
				moduleapi.RuntimeProtocolMCPStdio20251125,
			)
		}
		if err := validateOpaque(
			"local process grant adapter identity",
			grant.AdapterIdentity,
		); err != nil {
			return nil, fmt.Errorf("grant %d: %w", index, err)
		}
		key := trustedArtifactKey{
			moduleID:       grant.ModuleID,
			exactVersion:   grant.ExactVersion,
			artifactDigest: grant.ArtifactDigest,
		}
		if _, duplicate := localProcess[key]; duplicate {
			return nil, fmt.Errorf(
				"%w: duplicate local process grant %d for %s@%s artifact %s",
				ErrInvalidInput,
				index,
				grant.ModuleID,
				grant.ExactVersion,
				grant.ArtifactDigest,
			)
		}
		localProcess[key] = localProcessAssignment{
			protocol:        grant.Protocol,
			adapterIdentity: grant.AdapterIdentity,
		}
	}

	remoteAction := make(
		map[trustedArtifactKey]remoteActionAssignment,
		len(config.RemoteActionGrants),
	)
	for index, grant := range config.RemoteActionGrants {
		ref := moduleapi.Ref{
			ID:      grant.ModuleID,
			Version: grant.ExactVersion,
		}
		if err := ref.Validate(); err != nil {
			return nil, fmt.Errorf(
				"%w: remote action grant %d identity: %v",
				ErrInvalidInput,
				index,
				err,
			)
		}
		if !moduleapi.ValidSHA256(grant.ArtifactDigest) {
			return nil, fmt.Errorf(
				"%w: remote action grant %d artifact digest is invalid",
				ErrInvalidInput,
				index,
			)
		}
		if grant.Protocol != moduleapi.RuntimeProtocolFreeAgentActionHTTPV1 {
			return nil, fmt.Errorf(
				"%w: remote action grant %d protocol must be %q",
				ErrInvalidInput,
				index,
				moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
			)
		}
		if err := validateOpaque(
			"remote action grant adapter identity",
			grant.AdapterIdentity,
		); err != nil {
			return nil, fmt.Errorf("grant %d: %w", index, err)
		}
		key := trustedArtifactKey{
			moduleID:       grant.ModuleID,
			exactVersion:   grant.ExactVersion,
			artifactDigest: grant.ArtifactDigest,
		}
		if _, duplicate := remoteAction[key]; duplicate {
			return nil, fmt.Errorf(
				"%w: duplicate remote action grant %d for %s@%s artifact %s",
				ErrInvalidInput,
				index,
				grant.ModuleID,
				grant.ExactVersion,
				grant.ArtifactDigest,
			)
		}
		remoteAction[key] = remoteActionAssignment{
			protocol:        grant.Protocol,
			adapterIdentity: grant.AdapterIdentity,
		}
	}

	wasmAction := make(
		map[trustedArtifactKey]wasmActionAssignment,
		len(config.WASMActionGrants),
	)
	for index, grant := range config.WASMActionGrants {
		ref := moduleapi.Ref{ID: grant.ModuleID, Version: grant.ExactVersion}
		if err := ref.Validate(); err != nil {
			return nil, fmt.Errorf(
				"%w: WASM action grant %d identity: %v",
				ErrInvalidInput,
				index,
				err,
			)
		}
		if !moduleapi.ValidSHA256(grant.ArtifactDigest) {
			return nil, fmt.Errorf(
				"%w: WASM action grant %d artifact digest is invalid",
				ErrInvalidInput,
				index,
			)
		}
		if grant.Protocol != moduleapi.RuntimeProtocolFreeAgentActionWASMV1 {
			return nil, fmt.Errorf(
				"%w: WASM action grant %d protocol must be %q",
				ErrInvalidInput,
				index,
				moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
			)
		}
		if err := validateOpaque(
			"WASM action grant adapter identity",
			grant.AdapterIdentity,
		); err != nil {
			return nil, fmt.Errorf("grant %d: %w", index, err)
		}
		key := trustedArtifactKey{
			moduleID:       grant.ModuleID,
			exactVersion:   grant.ExactVersion,
			artifactDigest: grant.ArtifactDigest,
		}
		if _, duplicate := wasmAction[key]; duplicate {
			return nil, fmt.Errorf(
				"%w: duplicate WASM action grant %d for %s@%s artifact %s",
				ErrInvalidInput,
				index,
				grant.ModuleID,
				grant.ExactVersion,
				grant.ArtifactDigest,
			)
		}
		wasmAction[key] = wasmActionAssignment{
			protocol:        grant.Protocol,
			adapterIdentity: grant.AdapterIdentity,
		}
	}

	return &Resolver{
		declarativeAdapterIdentity: config.DeclarativeAdapterIdentity,
		trusted:                    trusted,
		localProcess:               localProcess,
		remoteAction:               remoteAction,
		wasmAction:                 wasmAction,
		registry:                   registry,
	}, nil
}

// Resolve strictly restores the installed manifest, independently assigns the
// execution class and Core-owned adapter, and returns a Store-ready immutable
// activation input. It performs no Store read or write.
func (resolver *Resolver) Resolve(
	ctx context.Context,
	input ResolveInput,
) (currentstore.ActivateModuleInput, error) {
	if resolver == nil {
		return currentstore.ActivateModuleInput{}, fmt.Errorf(
			"%w: resolver is nil",
			ErrInvalidInput,
		)
	}
	if ctx == nil {
		return currentstore.ActivateModuleInput{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidInput,
		)
	}
	if err := ctx.Err(); err != nil {
		return currentstore.ActivateModuleInput{}, err
	}
	if err := validateResolveInput(input); err != nil {
		return currentstore.ActivateModuleInput{}, err
	}

	manifest, canonicalManifest, err := moduleapi.ParseModuleManifestV1(
		bytes.Clone(input.Installation.ManifestBytes),
	)
	if err != nil {
		return currentstore.ActivateModuleInput{}, fmt.Errorf(
			"%w: parse installation manifest: %v",
			ErrInvalidInput,
			err,
		)
	}
	if manifest.ID != input.Installation.ModuleID ||
		manifest.Version != input.Installation.ExactVersion {
		return currentstore.ActivateModuleInput{}, fmt.Errorf(
			"%w: manifest identity %s@%s does not match installation %s@%s",
			ErrInvalidInput,
			manifest.ID,
			manifest.Version,
			input.Installation.ModuleID,
			input.Installation.ExactVersion,
		)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		moduleManifestMediaType,
		canonicalManifest,
	)
	if err != nil {
		return currentstore.ActivateModuleInput{}, fmt.Errorf(
			"%w: compute installation manifest ref: %v",
			ErrInvalidInput,
			err,
		)
	}
	if manifestRef != input.Installation.ManifestRef {
		return currentstore.ActivateModuleInput{}, fmt.Errorf(
			"%w: installation manifest ref does not match manifest bytes",
			ErrInvalidInput,
		)
	}

	executionClass, adapterIdentity, err :=
		resolver.resolveProvider(ctx, input.Installation, manifest)
	if err != nil {
		return currentstore.ActivateModuleInput{}, err
	}
	if input.ExpectedExecutionClass != "" &&
		input.ExpectedExecutionClass != executionClass {
		return currentstore.ActivateModuleInput{}, fmt.Errorf(
			"%w: execution class is %q, expected %q",
			ErrAssertionMismatch,
			executionClass,
			input.ExpectedExecutionClass,
		)
	}
	if input.ExpectedAdapterIdentity != "" &&
		input.ExpectedAdapterIdentity != adapterIdentity {
		return currentstore.ActivateModuleInput{}, fmt.Errorf(
			"%w: adapter identity is %q, expected %q",
			ErrAssertionMismatch,
			adapterIdentity,
			input.ExpectedAdapterIdentity,
		)
	}

	activationID, err := deriveActivationID(
		input,
		executionClass,
		adapterIdentity,
	)
	if err != nil {
		return currentstore.ActivateModuleInput{}, err
	}
	return currentstore.ActivateModuleInput{
		ActivationID:       activationID,
		TenantID:           input.TenantID,
		InstanceID:         input.InstanceID,
		InstallationID:     input.Installation.InstallationID,
		ActivationRevision: input.ActivationRevision,
		ExecutionClass:     executionClass,
		AdapterIdentity:    adapterIdentity,
	}, nil
}

func (resolver *Resolver) resolveProvider(
	ctx context.Context,
	installation currentstore.ModuleInstallation,
	manifest moduleapi.ModuleManifestV1,
) (moduleapi.ExecutionClass, string, error) {
	switch manifest.Runtime.Mode {
	case moduleapi.RuntimeModeRequestDeclarative:
		if manifest.Runtime.Protocol != moduleapi.RuntimeProtocolStaticV1 {
			return "", "", fmt.Errorf(
				"%w: declarative manifest protocol is not static/v1",
				ErrInvalidInput,
			)
		}
		return moduleapi.ExecutionDeclarative,
			resolver.declarativeAdapterIdentity, nil

	case moduleapi.RuntimeModeRequestTrustedInProcess:
		if manifest.Runtime.Protocol !=
			moduleapi.RuntimeProtocolGoInProcessV1 {
			return "", "", fmt.Errorf(
				"%w: trusted in-process manifest protocol is not go-in-process/v1",
				ErrInvalidInput,
			)
		}
		key := trustedArtifactKey{
			moduleID:       installation.ModuleID,
			exactVersion:   installation.ExactVersion,
			artifactDigest: installation.ArtifactDigest,
		}
		adapterIdentity, allowed := resolver.trusted[key]
		if !allowed {
			return "", "", fmt.Errorf(
				"%w: %s@%s artifact %s",
				ErrTrustedModuleNotAllowlisted,
				installation.ModuleID,
				installation.ExactVersion,
				installation.ArtifactDigest,
			)
		}
		if isNilDependency(resolver.registry) {
			return "", "", fmt.Errorf(
				"%w: registry probe is unavailable",
				ErrAdapterNotRegistered,
			)
		}
		registered, err := resolver.registry.IsRegistered(
			ctx,
			installation.ArtifactDigest,
			adapterIdentity,
		)
		if err != nil {
			return "", "", fmt.Errorf(
				"activationresolver: probe exact adapter %q: %w",
				adapterIdentity,
				err,
			)
		}
		if !registered {
			return "", "", fmt.Errorf(
				"%w: artifact %s adapter %q",
				ErrAdapterNotRegistered,
				installation.ArtifactDigest,
				adapterIdentity,
			)
		}
		return moduleapi.ExecutionTrustedInProcess, adapterIdentity, nil

	case moduleapi.RuntimeModeRequestLocalProcess:
		key := trustedArtifactKey{
			moduleID:       installation.ModuleID,
			exactVersion:   installation.ExactVersion,
			artifactDigest: installation.ArtifactDigest,
		}
		assignment, granted := resolver.localProcess[key]
		if !granted || assignment.protocol != manifest.Runtime.Protocol {
			return "", "", fmt.Errorf(
				"%w: %s@%s artifact %s protocol %q",
				ErrLocalProcessModuleNotGranted,
				installation.ModuleID,
				installation.ExactVersion,
				installation.ArtifactDigest,
				manifest.Runtime.Protocol,
			)
		}
		if isNilDependency(resolver.registry) {
			return "", "", fmt.Errorf(
				"%w: registry probe is unavailable",
				ErrAdapterNotRegistered,
			)
		}
		registered, err := resolver.registry.IsRegistered(
			ctx,
			installation.ArtifactDigest,
			assignment.adapterIdentity,
		)
		if err != nil {
			return "", "", fmt.Errorf(
				"activationresolver: probe exact adapter %q: %w",
				assignment.adapterIdentity,
				err,
			)
		}
		if !registered {
			return "", "", fmt.Errorf(
				"%w: artifact %s adapter %q",
				ErrAdapterNotRegistered,
				installation.ArtifactDigest,
				assignment.adapterIdentity,
			)
		}
		return moduleapi.ExecutionLocalProcess,
			assignment.adapterIdentity, nil

	case moduleapi.RuntimeModeRequestRemote:
		if len(manifest.Provides) != 1 ||
			manifest.Provides[0] != (moduleapi.PortRef{
				Name:         moduleapi.PortNameActionProvider,
				ExactVersion: moduleapi.PortVersionV1,
			}) || len(manifest.Requires) != 0 ||
			len(manifest.RequestedPermissions) != 0 {
			return "", "", fmt.Errorf(
				"%w: REMOTE runtime must provide only action.provider/v1 and request no dependency or permission",
				ErrRuntimeModeUnavailable,
			)
		}
		key := trustedArtifactKey{
			moduleID:       installation.ModuleID,
			exactVersion:   installation.ExactVersion,
			artifactDigest: installation.ArtifactDigest,
		}
		assignment, granted := resolver.remoteAction[key]
		if !granted || assignment.protocol != manifest.Runtime.Protocol {
			return "", "", fmt.Errorf(
				"%w: %s@%s artifact %s protocol %q",
				ErrRemoteActionModuleNotGranted,
				installation.ModuleID,
				installation.ExactVersion,
				installation.ArtifactDigest,
				manifest.Runtime.Protocol,
			)
		}
		if isNilDependency(resolver.registry) {
			return "", "", fmt.Errorf(
				"%w: registry probe is unavailable",
				ErrAdapterNotRegistered,
			)
		}
		registered, err := resolver.registry.IsRegistered(
			ctx,
			installation.ArtifactDigest,
			assignment.adapterIdentity,
		)
		if err != nil {
			return "", "", fmt.Errorf(
				"activationresolver: probe exact adapter %q: %w",
				assignment.adapterIdentity,
				err,
			)
		}
		if !registered {
			return "", "", fmt.Errorf(
				"%w: artifact %s adapter %q",
				ErrAdapterNotRegistered,
				installation.ArtifactDigest,
				assignment.adapterIdentity,
			)
		}
		return moduleapi.ExecutionRemote, assignment.adapterIdentity, nil

	case moduleapi.RuntimeModeRequestWASM:
		if len(manifest.Provides) != 1 ||
			manifest.Provides[0] != (moduleapi.PortRef{
				Name:         moduleapi.PortNameActionProvider,
				ExactVersion: moduleapi.PortVersionV1,
			}) || len(manifest.Requires) != 0 ||
			len(manifest.RequestedPermissions) != 0 {
			return "", "", fmt.Errorf(
				"%w: WASM runtime must provide only action.provider/v1 and request no dependency or permission",
				ErrRuntimeModeUnavailable,
			)
		}
		key := trustedArtifactKey{
			moduleID:       installation.ModuleID,
			exactVersion:   installation.ExactVersion,
			artifactDigest: installation.ArtifactDigest,
		}
		assignment, granted := resolver.wasmAction[key]
		if !granted || assignment.protocol != manifest.Runtime.Protocol {
			return "", "", fmt.Errorf(
				"%w: %s@%s artifact %s protocol %q",
				ErrWASMActionModuleNotGranted,
				installation.ModuleID,
				installation.ExactVersion,
				installation.ArtifactDigest,
				manifest.Runtime.Protocol,
			)
		}
		if isNilDependency(resolver.registry) {
			return "", "", fmt.Errorf(
				"%w: registry probe is unavailable",
				ErrAdapterNotRegistered,
			)
		}
		registered, err := resolver.registry.IsRegistered(
			ctx,
			installation.ArtifactDigest,
			assignment.adapterIdentity,
		)
		if err != nil {
			return "", "", fmt.Errorf(
				"activationresolver: probe exact adapter %q: %w",
				assignment.adapterIdentity,
				err,
			)
		}
		if !registered {
			return "", "", fmt.Errorf(
				"%w: artifact %s adapter %q",
				ErrAdapterNotRegistered,
				installation.ArtifactDigest,
				assignment.adapterIdentity,
			)
		}
		return moduleapi.ExecutionWASM, assignment.adapterIdentity, nil

	default:
		// Keep the fail-closed switch local so future manifest modes cannot
		// silently become S1 execution classes.
		return "", "", fmt.Errorf(
			"%w: unsupported runtime request %q",
			ErrInvalidInput,
			manifest.Runtime.Mode,
		)
	}
}

type activationIdentityWire struct {
	TenantID           string                     `json:"tenant_id"`
	InstanceID         string                     `json:"instance_id"`
	ActivationRevision uint64                     `json:"activation_revision"`
	Installation       activationInstallationWire `json:"installation"`
	Provider           activationProviderWire     `json:"provider"`
}

type activationInstallationWire struct {
	InstallationID string `json:"installation_id"`
	ModuleID       string `json:"module_id"`
	ExactVersion   string `json:"exact_version"`
	ManifestRef    string `json:"manifest_ref"`
	ArtifactDigest string `json:"artifact_digest"`
}

type activationProviderWire struct {
	ExecutionClass  moduleapi.ExecutionClass `json:"execution_class"`
	AdapterIdentity string                   `json:"adapter_identity"`
}

func deriveActivationID(
	input ResolveInput,
	executionClass moduleapi.ExecutionClass,
	adapterIdentity string,
) (string, error) {
	wire := activationIdentityWire{
		TenantID:           input.TenantID,
		InstanceID:         input.InstanceID,
		ActivationRevision: input.ActivationRevision,
		Installation: activationInstallationWire{
			InstallationID: input.Installation.InstallationID,
			ModuleID:       input.Installation.ModuleID,
			ExactVersion:   input.Installation.ExactVersion,
			ManifestRef:    input.Installation.ManifestRef,
			ArtifactDigest: input.Installation.ArtifactDigest,
		},
		Provider: activationProviderWire{
			ExecutionClass:  executionClass,
			AdapterIdentity: adapterIdentity,
		},
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf(
			"activationresolver: marshal activation identity: %w",
			err,
		)
	}
	canonical, err := moduleapi.CanonicalJSON(payload)
	if err != nil {
		return "", fmt.Errorf(
			"activationresolver: canonicalize activation identity: %w",
			err,
		)
	}
	return moduleapi.Digest(activationIDDomain, canonical), nil
}

func validateResolveInput(input ResolveInput) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "tenant ID", value: input.TenantID},
		{name: "instance ID", value: input.InstanceID},
		{
			name:  "installation ID",
			value: input.Installation.InstallationID,
		},
	} {
		if err := validateOpaque(field.name, field.value); err != nil {
			return err
		}
	}
	ref := moduleapi.Ref{
		ID:      input.Installation.ModuleID,
		Version: input.Installation.ExactVersion,
	}
	if err := ref.Validate(); err != nil {
		return fmt.Errorf(
			"%w: installation identity: %v",
			ErrInvalidInput,
			err,
		)
	}
	if !moduleapi.ValidSHA256(input.Installation.ManifestRef) {
		return fmt.Errorf(
			"%w: installation manifest ref is invalid",
			ErrInvalidInput,
		)
	}
	if !moduleapi.ValidSHA256(input.Installation.ArtifactDigest) {
		return fmt.Errorf(
			"%w: installation artifact digest is invalid",
			ErrInvalidInput,
		)
	}
	if input.ActivationRevision == 0 ||
		input.ActivationRevision > math.MaxInt64 {
		return fmt.Errorf(
			"%w: activation revision must be between 1 and %d",
			ErrInvalidInput,
			uint64(math.MaxInt64),
		)
	}
	if input.ExpectedExecutionClass != "" {
		switch input.ExpectedExecutionClass {
		case moduleapi.ExecutionDeclarative,
			moduleapi.ExecutionTrustedInProcess,
			moduleapi.ExecutionLocalProcess,
			moduleapi.ExecutionRemote,
			moduleapi.ExecutionWASM:
		default:
			return fmt.Errorf(
				"%w: expected execution class %q is not supported",
				ErrInvalidInput,
				input.ExpectedExecutionClass,
			)
		}
	}
	if input.ExpectedAdapterIdentity != "" {
		if err := validateOpaque(
			"expected adapter identity",
			input.ExpectedAdapterIdentity,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateOpaque(name, value string) error {
	if value == "" ||
		value != strings.TrimSpace(value) ||
		len(value) > maxOpaqueIDBytes ||
		!utf8.ValidString(value) ||
		value != moduleapi.CanonicalText(value) {
		return fmt.Errorf(
			"%w: %s must be canonical UTF-8, non-empty, trimmed, and at most %d bytes",
			ErrInvalidInput,
			name,
			maxOpaqueIDBytes,
		)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf(
				"%w: %s contains an unsupported control character",
				ErrInvalidInput,
				name,
			)
		}
	}
	return nil
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
