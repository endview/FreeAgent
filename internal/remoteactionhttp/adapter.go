package remoteactionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidAdapter    = errors.New("remoteactionhttp: invalid Adapter")
	ErrGenericInvocation = errors.New(
		"remoteactionhttp: generic ModuleHost invocation is forbidden",
	)
	ErrDescribe = errors.New("remoteactionhttp: Describe rejected")
	ErrPrepare  = errors.New("remoteactionhttp: Prepare rejected")
	ErrExecute  = errors.New("remoteactionhttp: execution rejected before dispatch")
)

// SecretIdentityV1 closes one late-bound credential lookup over the exact
// artifact/adapter, activated Instance, endpoint and SecretRef selected by the
// private Gateway closure. It contains no secret material.
type SecretIdentityV1 struct {
	Provider    moduleapi.ActivatedModuleRef
	EndpointURL string
	SecretRef   string
}

// SecretResolver is supplied by the trusted composition root. The returned
// slice is owned by the caller and is cleared after one ExecutePrepared call.
// Implementations must not put secret material in returned errors.
type SecretResolver interface {
	ResolveSecret(context.Context, SecretIdentityV1) ([]byte, error)
}

type reusableProviderIdentity struct {
	moduleID        string
	version         string
	artifactDigest  string
	executionClass  moduleapi.ExecutionClass
	adapterIdentity string
}

func newReusableProviderIdentity(
	provider moduleapi.ActivatedModuleRef,
) (reusableProviderIdentity, error) {
	if err := validateProvider(provider); err != nil {
		return reusableProviderIdentity{}, err
	}
	return reusableProviderIdentity{
		moduleID:        provider.ModuleID,
		version:         provider.Version,
		artifactDigest:  provider.ArtifactDigest,
		executionClass:  provider.ExecutionClass,
		adapterIdentity: provider.AdapterIdentity,
	}, nil
}

func (identity reusableProviderIdentity) matches(
	provider moduleapi.ActivatedModuleRef,
) bool {
	if err := provider.Validate(); err != nil {
		return false
	}
	return provider.ModuleID == identity.moduleID &&
		provider.Version == identity.version &&
		provider.ArtifactDigest == identity.artifactDigest &&
		provider.ExecutionClass == identity.executionClass &&
		provider.AdapterIdentity == identity.adapterIdentity
}

// Adapter is immutable and safe for concurrent activated Instances of one
// exact artifact. It stores only artifact-level definitions, the reusable
// provider identity, a trusted resolver and a Core-owned transport. In
// particular it has no endpoint or SecretRef field.
type Adapter struct {
	provider               reusableProviderIdentity
	definitions            []moduleapi.ActionDefinitionV1
	definitionByProviderID map[string]moduleapi.ActionDefinitionV1
	resolver               SecretResolver
	client                 *http.Client
}

var _ modulehost.ModuleInvoker = (*Adapter)(nil)
var _ moduleapi.ActionProviderV1 = (*Adapter)(nil)
var _ modulehost.ActionExecutor = (*Adapter)(nil)

// New constructs the production Adapter. SecretResolver is the only injected
// effectful dependency; HTTP policy is always the Core-owned hardened client.
func New(
	provider moduleapi.ActivatedModuleRef,
	descriptor DescriptorV1,
	resolver SecretResolver,
) (*Adapter, error) {
	return newAdapter(provider, descriptor, resolver, newProductionHTTPClient())
}

// NewFromArtifact is the production convenience path that verifies and
// restores the selected artifact before constructing the Adapter.
func NewFromArtifact(
	ctx context.Context,
	provider moduleapi.ActivatedModuleRef,
	artifactDirectory string,
	expectedCoveredSize uint64,
	resolver SecretResolver,
) (*Adapter, error) {
	descriptor, err := LoadDescriptorFromArtifact(
		ctx,
		provider,
		artifactDirectory,
		expectedCoveredSize,
	)
	if err != nil {
		return nil, err
	}
	return New(provider, descriptor, resolver)
}

// newAdapterWithRoundTripper is test-only by visibility. Production callers
// cannot replace Core redirect, proxy, replay or DNS policy.
func newAdapterWithRoundTripper(
	provider moduleapi.ActivatedModuleRef,
	descriptor DescriptorV1,
	resolver SecretResolver,
	transport http.RoundTripper,
) (*Adapter, error) {
	if isNilDependency(transport) {
		return nil, fmt.Errorf("%w: test RoundTripper is nil", ErrInvalidAdapter)
	}
	return newAdapter(
		provider,
		descriptor,
		resolver,
		newHTTPClient(transport),
	)
}

func newAdapter(
	provider moduleapi.ActivatedModuleRef,
	descriptor DescriptorV1,
	resolver SecretResolver,
	client *http.Client,
) (*Adapter, error) {
	identity, err := newReusableProviderIdentity(provider)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAdapter, err)
	}
	frozenDescriptor, _, err := NewDescriptorV1(descriptor)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidAdapter, err)
	}
	if isNilDependency(resolver) || client == nil ||
		isNilDependency(client.Transport) {
		return nil, fmt.Errorf(
			"%w: SecretResolver and Core HTTP client are required",
			ErrInvalidAdapter,
		)
	}
	byProviderID := make(
		map[string]moduleapi.ActionDefinitionV1,
		len(frozenDescriptor.Actions),
	)
	for _, definition := range frozenDescriptor.Actions {
		byProviderID[definition.ProviderActionID] = cloneDefinition(definition)
	}
	return &Adapter{
		provider:               identity,
		definitions:            cloneDefinitions(frozenDescriptor.Actions),
		definitionByProviderID: byProviderID,
		resolver:               resolver,
		client:                 client,
	}, nil
}

func (*Adapter) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return modulehost.InvocationResult{}, ErrGenericInvocation
}

// Describe is offline and deterministic. Restoring Parameters proves the
// consumer selected the native binding wire but performs no DNS, network or
// secret resolution.
func (adapter *Adapter) Describe(
	ctx context.Context,
	request moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	if err := validateContext(adapter, ctx, ErrDescribe); err != nil {
		return nil, err
	}
	frozen, _, err := moduleapi.NewActionDescribeRequestV1(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request: %v", ErrDescribe, err)
	}
	parameters, err := moduleapi.RestoreRemoteActionHTTPBindingParametersV1(
		frozen.Parameters,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: parameters: %v", ErrDescribe, err)
	}
	if err := rejectNonPublicLiteralEndpoint(parameters.EndpointURL); err != nil {
		return nil, fmt.Errorf("%w: endpoint: %v", ErrDescribe, err)
	}
	return cloneDefinitions(adapter.definitions), nil
}

// Prepare validates one exact static definition and returns the canonical
// Action input unchanged. It performs no DNS, network or secret resolution.
func (adapter *Adapter) Prepare(
	ctx context.Context,
	request moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	if err := validateContext(adapter, ctx, ErrPrepare); err != nil {
		return nil, err
	}
	frozen, _, err := moduleapi.NewActionRequestV1(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request: %v", ErrPrepare, err)
	}
	definition, found := adapter.definitionByProviderID[frozen.ProviderActionID]
	if !found {
		return nil, fmt.Errorf(
			"%w: provider_action_id is absent from the offline descriptor",
			ErrPrepare,
		)
	}
	if err := moduleapi.ValidateActionInputV1(
		definition.InputSchema,
		frozen.CanonicalInput,
	); err != nil {
		return nil, fmt.Errorf("%w: input: %v", ErrPrepare, err)
	}
	prepared, err := moduleapi.CanonicalizeActionPreparedPayloadV1(
		frozen.CanonicalInput,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrPrepare, err)
	}
	return bytes.Clone(prepared), nil
}

func validateContext(adapter *Adapter, ctx context.Context, kind error) error {
	if adapter == nil {
		return fmt.Errorf("%w: Adapter is nil", kind)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", kind)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: context: %v", kind, err)
	}
	return nil
}

func cloneDefinition(
	definition moduleapi.ActionDefinitionV1,
) moduleapi.ActionDefinitionV1 {
	definition.InputSchema = bytes.Clone(definition.InputSchema)
	return definition
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
