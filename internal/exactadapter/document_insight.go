package exactadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	// DocumentInsightAdapterIdentityV1 is the sole compiled implementation
	// identity for the first governed dual-Port document-insight artifact.
	DocumentInsightAdapterIdentityV1 = moduleapi.DocumentInsightAdapterIdentityV1
)

var (
	ErrInvalidDocumentInsightProvider = errors.New(
		"exactadapter: invalid document insight provider",
	)
	ErrDocumentInsightInvocation = errors.New(
		"exactadapter: document insight rejected generic invocation",
	)
)

var actionProviderPortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameActionProvider,
	ExactVersion: moduleapi.PortVersionV1,
}

// DocumentInsight is one immutable artifact-scoped implementation shared by
// both exact Ports. Context invocation delegates to DeterministicKnowledge;
// Action discovery/preparation and the private Gateway executor delegate to
// TextStatsAction. Generic Action invocation remains forbidden.
type DocumentInsight struct {
	knowledge *DeterministicKnowledge
	action    *TextStatsAction
}

var _ modulehost.ModuleInvoker = (*DocumentInsight)(nil)
var _ moduleapi.ActionProviderV1 = (*DocumentInsight)(nil)
var _ modulehost.ActionExecutor = (*DocumentInsight)(nil)

// NewDocumentInsight restores one immutable local shared-document source and
// binds both capabilities to the same exact trusted artifact adapter.
func NewDocumentInsight(
	provider moduleapi.ActivatedModuleRef,
	sourceCanonical []byte,
) (*DocumentInsight, error) {
	if err := provider.Validate(); err != nil {
		return nil, fmt.Errorf(
			"%w: provider: %v",
			ErrInvalidDocumentInsightProvider,
			err,
		)
	}
	if provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		provider.AdapterIdentity != DocumentInsightAdapterIdentityV1 {
		return nil, fmt.Errorf(
			"%w: provider is not the exact trusted document insight adapter",
			ErrInvalidDocumentInsightProvider,
		)
	}
	knowledge, err := NewDeterministicKnowledge(provider, sourceCanonical)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: knowledge: %v",
			ErrInvalidDocumentInsightProvider,
			err,
		)
	}
	action, err := NewTextStatsAction(provider)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: action: %v",
			ErrInvalidDocumentInsightProvider,
			err,
		)
	}
	return &DocumentInsight{knowledge: knowledge, action: action}, nil
}

// Invoke dispatches only the public Context Port. An Action Port invocation
// deliberately reaches TextStatsAction's generic rejection, preserving the
// Gateway-only ExecutePrepared boundary.
func (adapter *DocumentInsight) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	if adapter == nil || adapter.knowledge == nil || adapter.action == nil {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: adapter is nil or incomplete",
			ErrDocumentInsightInvocation,
		)
	}
	switch prepared.Invocation.Port {
	case moduleapi.ExactContextProvidePortV1():
		return adapter.knowledge.Invoke(ctx, prepared)
	case actionProviderPortV1:
		return adapter.action.Invoke(ctx, prepared)
	default:
		return modulehost.InvocationResult{}, fmt.Errorf(
			"%w: Port must be context.provide/v1 or action.provider/v1",
			ErrDocumentInsightInvocation,
		)
	}
}

func (adapter *DocumentInsight) Describe(
	ctx context.Context,
	request moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	if adapter == nil || adapter.action == nil {
		return nil, fmt.Errorf("%w: adapter is nil", ErrTextStatsDescribe)
	}
	return adapter.action.Describe(ctx, request)
}

func (adapter *DocumentInsight) Prepare(
	ctx context.Context,
	request moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	if adapter == nil || adapter.action == nil {
		return nil, fmt.Errorf("%w: adapter is nil", ErrTextStatsPrepare)
	}
	return adapter.action.Prepare(ctx, request)
}

func (adapter *DocumentInsight) ExecutePrepared(
	ctx context.Context,
	execution modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	if adapter == nil || adapter.action == nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: adapter is nil",
			ErrTextStatsExecution,
		)
	}
	return adapter.action.ExecutePrepared(ctx, execution)
}
