package wasmaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidAdapter    = errors.New("wasmaction: invalid Adapter")
	ErrGenericInvocation = errors.New(
		"wasmaction: generic ModuleHost invocation is forbidden",
	)
	ErrDescribe = errors.New("wasmaction: Describe rejected")
	ErrPrepare  = errors.New("wasmaction: Prepare rejected")
	ErrExecute  = errors.New("wasmaction: execution rejected")
)

var emptyParametersV1 = json.RawMessage(`{}`)

type reusableProviderIdentityV1 struct {
	moduleID        string
	version         string
	artifactDigest  string
	executionClass  moduleapi.ExecutionClass
	adapterIdentity string
}

func newReusableProviderIdentityV1(
	provider moduleapi.ActivatedModuleRef,
) (reusableProviderIdentityV1, error) {
	if err := validateProvider(provider); err != nil {
		return reusableProviderIdentityV1{}, err
	}
	return reusableProviderIdentityV1{
		moduleID:        provider.ModuleID,
		version:         provider.Version,
		artifactDigest:  provider.ArtifactDigest,
		executionClass:  provider.ExecutionClass,
		adapterIdentity: provider.AdapterIdentity,
	}, nil
}

func (identity reusableProviderIdentityV1) matches(
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

// Adapter contains only artifact-scoped immutable facts. It deliberately
// excludes InstanceID and ActivationRevision so the exact artifact+adapter
// Registry key can safely serve multiple independently authorized Instances.
// It keeps no Runtime, compiled module, Config, Authority or Run state.
type Adapter struct {
	provider               reusableProviderIdentityV1
	definitions            []moduleapi.ActionDefinitionV1
	definitionByProviderID map[string]moduleapi.ActionDefinitionV1
	wasm                   []byte
}

func New(
	ctx context.Context,
	provider moduleapi.ActivatedModuleRef,
	descriptor DescriptorV1,
	wasm []byte,
) (*Adapter, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidAdapter)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	providerIdentity, err := newReusableProviderIdentityV1(provider)
	if err != nil {
		return nil, err
	}
	frozenDescriptor, _, err := NewDescriptorV1(descriptor)
	if err != nil {
		return nil, fmt.Errorf("%w: descriptor", ErrInvalidAdapter)
	}
	ownedWasm := bytes.Clone(wasm)
	if err := ValidateModuleV1(ctx, ownedWasm); err != nil {
		return nil, fmt.Errorf("%w: module", ErrInvalidAdapter)
	}
	byProviderID := make(
		map[string]moduleapi.ActionDefinitionV1,
		len(frozenDescriptor.Actions),
	)
	for _, definition := range frozenDescriptor.Actions {
		byProviderID[definition.ProviderActionID] = cloneDefinition(definition)
	}
	return &Adapter{
		provider:               providerIdentity,
		definitions:            cloneDefinitions(frozenDescriptor.Actions),
		definitionByProviderID: byProviderID,
		wasm:                   ownedWasm,
	}, nil
}

func (*Adapter) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return modulehost.InvocationResult{}, ErrGenericInvocation
}

// Describe is derived entirely from the artifact-covered descriptor. It
// neither constructs a Wasm runtime nor executes guest instructions.
func (adapter *Adapter) Describe(
	ctx context.Context,
	request moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	if err := validateContext(adapter, ctx, ErrDescribe); err != nil {
		return nil, err
	}
	if !bytes.Equal(request.Parameters, emptyParametersV1) {
		return nil, fmt.Errorf("%w: parameters must be exact {}", ErrDescribe)
	}
	if _, _, err := moduleapi.NewActionDescribeRequestV1(request); err != nil {
		return nil, fmt.Errorf("%w: request", ErrDescribe)
	}
	return cloneDefinitions(adapter.definitions), nil
}

// Prepare validates the exact static Action definition and returns the
// already-canonical input unchanged. It performs no guest execution.
func (adapter *Adapter) Prepare(
	ctx context.Context,
	request moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	if err := validateContext(adapter, ctx, ErrPrepare); err != nil {
		return nil, err
	}
	frozen, _, err := moduleapi.NewActionRequestV1(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request", ErrPrepare)
	}
	definition, found := adapter.definitionByProviderID[frozen.ProviderActionID]
	if !found {
		return nil, fmt.Errorf(
			"%w: provider_action_id is absent from descriptor",
			ErrPrepare,
		)
	}
	if err := moduleapi.ValidateActionInputV1(
		definition.InputSchema,
		frozen.CanonicalInput,
	); err != nil {
		return nil, fmt.Errorf("%w: input", ErrPrepare)
	}
	prepared, err := moduleapi.CanonicalizeActionPreparedPayloadV1(
		frozen.CanonicalInput,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: payload", ErrPrepare)
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
		return fmt.Errorf("%w: context is unavailable", kind)
	}
	return nil
}

func validateProvider(provider moduleapi.ActivatedModuleRef) error {
	if err := provider.Validate(); err != nil {
		return fmt.Errorf("%w: Provider is invalid", ErrInvalidAdapter)
	}
	if provider.ExecutionClass != moduleapi.ExecutionWASM ||
		provider.AdapterIdentity != AdapterIdentityV1 {
		return fmt.Errorf(
			"%w: Provider must be exact WASM freeagent-action-wasm/v1",
			ErrInvalidAdapter,
		)
	}
	return nil
}

func cloneDefinition(input moduleapi.ActionDefinitionV1) moduleapi.ActionDefinitionV1 {
	input.InputSchema = bytes.Clone(input.InputSchema)
	return input
}

var actionProviderPortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameActionProvider,
	ExactVersion: moduleapi.PortVersionV1,
}
