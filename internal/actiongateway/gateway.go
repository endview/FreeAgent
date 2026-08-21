// Package actiongateway owns the only runtime path from a persisted Action
// DispatchAttempt to the private Host Adapter executor capability.
package actiongateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var ErrInvalidGateway = errors.New("actiongateway: invalid Action Gateway")

const (
	classificationGatewayClosureDenied = "GATEWAY_CLOSURE_DENIED"
	classificationActivationDenied     = "CURRENT_ACTIVATION_DENIED_BEFORE_ACTION"
	classificationAdapterUnavailable   = "ACTION_EXECUTOR_UNAVAILABLE"
	classificationContextExpired       = "ACTION_DEADLINE_EXPIRED_BEFORE_EXECUTOR"
	classificationResultRejected       = "ACTION_RESULT_INVALID_OR_OVERSIZE"
	unknownExecutorError               = "EXECUTOR_ERROR_AFTER_DISPATCH"
	unknownInvalidExecutorResult       = "INVALID_EXECUTOR_RESULT"
)

var actionPortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameActionProvider,
	ExactVersion: moduleapi.PortVersionV1,
}

// StoreBoundary is the narrow read/fencing surface required after the
// process-local permit is consumed. It has no mutation or retry operation.
type StoreBoundary interface {
	LoadRunForLoop(context.Context, currentstore.RunLease) (currentstore.RunForLoop, error)
	CheckCurrentActivation(
		context.Context,
		string,
		moduleapi.PortRef,
		moduleapi.ActivatedModuleRef,
	) error
}

// ResultV1 distinguishes a confirmed Effect whose result is rejected from a
// genuinely UNKNOWN Effect. CanonicalResult is present only for an AVAILABLE
// successful execution.
type ResultV1 struct {
	AttemptID                     string
	Provider                      moduleapi.ActivatedModuleRef
	Outcome                       moduleapi.ActionExecutionOutcomeV1
	CanonicalResult               json.RawMessage
	ProviderReceipt               json.RawMessage
	ExternalOperationID           string
	ErrorClassification           string
	UnknownReason                 string
	ResultRejectionClassification string
}

// Gateway keeps no durable state. The authoritative one-shot grant remains
// the Store result's private shared atomic permit.
type Gateway struct {
	store    StoreBoundary
	registry modulehost.ExactAdapterRegistry
	now      func() time.Time
}

func New(
	store StoreBoundary,
	registry modulehost.ExactAdapterRegistry,
) (*Gateway, error) {
	if isNilDependency(store) || isNilDependency(registry) {
		return nil, fmt.Errorf(
			"%w: Store boundary and exact adapter registry are required",
			ErrInvalidGateway,
		)
	}
	return &Gateway{store: store, registry: registry, now: time.Now}, nil
}

// Execute consumes the Store permit before any private executor can be
// resolved. After consumption every pre-effect denial is returned as a known
// FAILED result, while every ambiguous post-call error is returned UNKNOWN.
func (gateway *Gateway) Execute(
	ctx context.Context,
	grant currentstore.CommitModelActionAndBeginDispatchResult,
) (ResultV1, error) {
	if gateway == nil || isNilDependency(gateway.store) ||
		isNilDependency(gateway.registry) || gateway.now == nil {
		return ResultV1{}, fmt.Errorf("%w: Gateway is not initialized", ErrInvalidGateway)
	}
	if ctx == nil {
		return ResultV1{}, fmt.Errorf("%w: context is nil", ErrInvalidGateway)
	}
	if !grant.Applied || !grant.GatewayAllowed ||
		grant.Action.Attempt.AttemptID == "" {
		return ResultV1{}, fmt.Errorf("%w: Store result grants no execution", ErrInvalidGateway)
	}
	if !grant.ConsumeActionGatewayPermit() {
		return ResultV1{}, fmt.Errorf("%w: execution permit is absent or consumed", ErrInvalidGateway)
	}

	attemptID := grant.Action.Attempt.AttemptID
	provider := grant.Action.Attempt.Binding.Provider
	fail := func(classification string) (ResultV1, error) {
		return failedResult(attemptID, provider, classification), nil
	}
	if err := ctx.Err(); err != nil {
		return fail(classificationContextExpired)
	}
	now := gateway.now().UTC()
	if !now.Before(grant.Action.Attempt.Deadline) {
		return fail(classificationContextExpired)
	}
	run, err := gateway.store.LoadRunForLoop(ctx, grant.Lease)
	if err != nil {
		return fail(classificationGatewayClosureDenied)
	}
	closure, err := validateGrantClosure(
		run,
		grant,
		now,
	)
	if err != nil {
		return fail(classificationGatewayClosureDenied)
	}
	provider = closure.Record.Attempt.Binding.Provider
	if err := gateway.store.CheckCurrentActivation(
		ctx,
		closure.Record.Attempt.RunID,
		actionPortV1,
		provider,
	); err != nil {
		return fail(classificationActivationDenied)
	}
	if err := ctx.Err(); err != nil {
		return fail(classificationContextExpired)
	}
	invoker, err := gateway.registry.ResolveExact(
		ctx,
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	)
	if err != nil || isNilDependency(invoker) {
		return fail(classificationAdapterUnavailable)
	}
	executor, ok := invoker.(modulehost.ActionExecutor)
	if !ok || isNilDependency(executor) {
		return fail(classificationAdapterUnavailable)
	}
	request, _, err := moduleapi.NewActionExecutionRequestV1(
		moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        closure.Record.Attempt.AttemptID,
			PublicActionID:   closure.Definition.PublicActionID,
			ProviderActionID: closure.Definition.ProviderActionID,
			DefinitionDigest: closure.Definition.DefinitionDigest,
			MaxResultBytes:   closure.Definition.MaxResultBytes,
			PreparedPayload:  bytes.Clone(closure.Proposal.PreparedPayload),
		},
	)
	if err != nil {
		return fail(classificationGatewayClosureDenied)
	}
	execution, err := modulehost.NewPreparedActionExecutionV1(
		modulehost.PreparedActionExecutionV1{
			Request:            request,
			Binding:            closure.Binding,
			ConfigCanonical:    closure.ConfigCanonical,
			AuthorityCanonical: closure.AuthorityCanonical,
		},
	)
	if err != nil {
		return fail(classificationGatewayClosureDenied)
	}
	callContext, cancel := context.WithDeadline(
		ctx,
		closure.Record.Attempt.Deadline,
	)
	defer cancel()
	if err := callContext.Err(); err != nil {
		return fail(classificationContextExpired)
	}
	// ResolveExact may perform a bounded lazy artifact load. Recheck the
	// deny-only current Catalog immediately before entering the private
	// executor so a revocation linearized during that load cannot reach module
	// code. A revocation after this check does not rewrite or kill an already
	// admitted execution; offline Disable still drains the serving process.
	if err := gateway.store.CheckCurrentActivation(
		callContext,
		closure.Record.Attempt.RunID,
		actionPortV1,
		provider,
	); err != nil {
		return fail(classificationActivationDenied)
	}
	executed, executeErr := executor.ExecutePrepared(callContext, execution)
	return normalizeExecutorResult(
		execution.Request,
		provider,
		executed,
		executeErr,
	), nil
}

type validatedGrantClosureV1 struct {
	Record             currentstore.ActionDispatchRecord
	Definition         corecontract.FrozenActionDefinitionV1
	Proposal           corecontract.ActionProposalV1
	Binding            moduleapi.PortBinding
	ConfigCanonical    json.RawMessage
	AuthorityCanonical json.RawMessage
}

func validateGrantClosure(
	run currentstore.RunForLoop,
	grant currentstore.CommitModelActionAndBeginDispatchResult,
	now time.Time,
) (validatedGrantClosureV1, error) {
	want := grant.Action.Attempt
	if run.RunID != want.RunID ||
		run.Member.MemberID != want.MemberID ||
		run.Member.MemberSnapshotDigest != want.MemberSnapshotDigest ||
		run.Frame.Step != corecontract.ActionPendingLoopStep ||
		run.Frame.PendingAttemptID != "" ||
		run.Frame.PendingDispatchAttemptID != want.AttemptID ||
		run.Frame.BudgetStateRef != want.BudgetStateRef ||
		run.Frame.Revision != grant.Lease.FrameRevision ||
		run.RunRevision != grant.Lease.RunRevision ||
		!now.Before(want.Deadline) ||
		want.Deadline.After(run.Manifest.Deadline) ||
		want.Deadline.After(grant.Lease.ExpiresAt) {
		return validatedGrantClosureV1{},
			fmt.Errorf("Action grant no longer matches the fenced Run")
	}
	continued, err := corecontract.RestoreLoopContinuationV1(run.Frame.Continuation)
	if err != nil || continued.AttemptKind != corecontract.AttemptKindAction ||
		continued.AttemptID != want.AttemptID ||
		continued.LogicalStepID != want.LogicalStepID {
		return validatedGrantClosureV1{},
			fmt.Errorf("Action continuation differs from grant")
	}
	var record *currentstore.ActionDispatchRecord
	for index := range run.ActionDispatches {
		candidate := &run.ActionDispatches[index]
		if candidate.Attempt.AttemptID == want.AttemptID {
			if record != nil {
				return validatedGrantClosureV1{},
					fmt.Errorf("duplicate Action Attempt")
			}
			record = candidate
		}
	}
	if record == nil || record.Attempt.State != currentstore.ActionDispatchPending ||
		!sameAttemptClosure(record.Attempt, want) ||
		!bytes.Equal(record.Proposal.CanonicalBytes, grant.Action.Proposal.CanonicalBytes) {
		return validatedGrantClosureV1{},
			fmt.Errorf("persisted Action Attempt differs from grant")
	}
	var definition *corecontract.FrozenActionDefinitionV1
	for index := range run.Member.Actions {
		candidate := &run.Member.Actions[index]
		if candidate.PublicActionID == want.PublicActionID {
			definition = candidate
			break
		}
	}
	if definition == nil || definition.ProviderActionID != want.ProviderActionID ||
		definition.DefinitionDigest != want.DefinitionDigest ||
		definition.BindingIndex != want.BindingIndex ||
		definition.EffectClass != want.EffectClass ||
		definition.MaxResultBytes != want.MaxResultBytes {
		return validatedGrantClosureV1{},
			fmt.Errorf("frozen Action definition differs from grant")
	}
	plan, err := exactActionPlan(run.Member)
	if err != nil || uint64(definition.BindingIndex) >= uint64(len(plan.Bindings)) {
		return validatedGrantClosureV1{},
			fmt.Errorf("frozen Action Binding is absent")
	}
	binding := plan.Bindings[definition.BindingIndex]
	bindingCanonical, err := canonicalBinding(binding)
	if err != nil || !bytes.Equal(bindingCanonical, want.BindingCanonical) {
		return validatedGrantClosureV1{},
			fmt.Errorf("frozen Action Binding differs from grant")
	}
	configCanonical, authorityCanonical, err := validateLocalAuthority(
		run,
		binding,
		*definition,
	)
	if err != nil {
		return validatedGrantClosureV1{},
			err
	}
	proposal, err := corecontract.RestoreActionProposalV1(
		record.Proposal.CanonicalBytes,
		record.Proposal.Digest,
		run.Member.MemberSnapshotDigest,
		*definition,
	)
	if err != nil {
		return validatedGrantClosureV1{},
			err
	}
	return validatedGrantClosureV1{
		Record:             *record,
		Definition:         *definition,
		Proposal:           proposal,
		Binding:            binding,
		ConfigCanonical:    bytes.Clone(configCanonical),
		AuthorityCanonical: bytes.Clone(authorityCanonical),
	}, nil
}

func validateLocalAuthority(
	run currentstore.RunForLoop,
	binding moduleapi.PortBinding,
	definition corecontract.FrozenActionDefinitionV1,
) (json.RawMessage, json.RawMessage, error) {
	configContent, found := run.FindContent(binding.ConfigRef)
	if !found || configContent.Kind != currentstore.ContentConfig {
		return nil, nil, fmt.Errorf("Action Config is absent")
	}
	authorityContent, found := run.FindContent(binding.AuthorityCeilingRef)
	if !found || authorityContent.Kind != currentstore.ContentAuthorityCeiling {
		return nil, nil, fmt.Errorf("Action Authority is absent")
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(configContent.CanonicalBytes)
	if err != nil {
		return nil, nil, err
	}
	authority, err := moduleapi.RestoreActionAuthorityCeilingV1(
		authorityContent.CanonicalBytes,
	)
	if err != nil {
		return nil, nil, err
	}
	if authority.TenantID != run.Manifest.TenantID ||
		!containsOrWildcard(authority.AllowedWorkspaceIDs, run.Member.Workspace.ID) ||
		!contains(authority.AllowedProviderActionIDs, definition.ProviderActionID) {
		return nil, nil, fmt.Errorf("Action Authority scope does not close")
	}
	allowed, err := moduleapi.ActionEffectAtMostV1(
		definition.EffectClass,
		authority.MaxEffectClass,
	)
	if err != nil || !allowed || definition.MaxResultBytes > authority.MaxResultBytes {
		return nil, nil, fmt.Errorf("Action Authority limit does not close")
	}
	var mapping *moduleapi.ActionBindingMappingV1
	for index := range config.Actions {
		candidate := &config.Actions[index]
		if candidate.PublicActionID == definition.PublicActionID {
			mapping = candidate
			break
		}
	}
	if mapping == nil || mapping.ProviderActionID != definition.ProviderActionID ||
		definition.MaxResultBytes > mapping.MaxResultBytes {
		return nil, nil, fmt.Errorf("Action Config mapping does not close")
	}
	effective, err := moduleapi.HigherActionEffectV1(
		definition.EffectClass,
		mapping.LocalEffectClass,
	)
	if err != nil || effective != definition.EffectClass {
		return nil, nil, fmt.Errorf("Action local Effect classification does not close")
	}
	return bytes.Clone(configContent.CanonicalBytes),
		bytes.Clone(authorityContent.CanonicalBytes), nil
}

func normalizeExecutorResult(
	request moduleapi.ActionExecutionRequestV1,
	provider moduleapi.ActivatedModuleRef,
	executed moduleapi.ActionExecutionResultV1,
	executeErr error,
) ResultV1 {
	if executeErr != nil {
		return unknownResult(request.AttemptID, provider, unknownExecutorError)
	}
	if executed.AttemptID != request.AttemptID {
		return unknownResult(request.AttemptID, provider, unknownInvalidExecutorResult)
	}
	if !exactExecutorReceipt(executed.ProviderReceipt) {
		return unknownResult(request.AttemptID, provider, unknownInvalidExecutorResult)
	}
	frozen, _, err := moduleapi.NewActionExecutionResultV1(executed)
	if err == nil {
		err = moduleapi.ValidateActionExecutionResultForRequestV1(request, frozen)
	}
	if err != nil {
		if resultOnlyInvalidSuccess(request, executed) {
			return ResultV1{
				AttemptID:                     request.AttemptID,
				Provider:                      provider,
				Outcome:                       moduleapi.ActionExecutionSucceeded,
				ProviderReceipt:               bytes.Clone(executed.ProviderReceipt),
				ExternalOperationID:           executed.ExternalOperationID,
				ResultRejectionClassification: classificationResultRejected,
			}
		}
		return unknownResult(request.AttemptID, provider, unknownInvalidExecutorResult)
	}
	return ResultV1{
		AttemptID:           request.AttemptID,
		Provider:            provider,
		Outcome:             frozen.Outcome,
		CanonicalResult:     bytes.Clone(frozen.CanonicalResult),
		ProviderReceipt:     bytes.Clone(frozen.ProviderReceipt),
		ExternalOperationID: frozen.ExternalOperationID,
		ErrorClassification: frozen.ErrorClassification,
		UnknownReason:       frozen.UnknownReason,
	}
}

// resultOnlyInvalidSuccess proves that the Provider returned a complete,
// otherwise-valid SUCCEEDED fact and that replacing only the result body with
// one legal byte closes the exact request. This is the sole case where an
// unsafe result does not make execution certainty UNKNOWN.
func resultOnlyInvalidSuccess(
	request moduleapi.ActionExecutionRequestV1,
	executed moduleapi.ActionExecutionResultV1,
) bool {
	if executed.Outcome != moduleapi.ActionExecutionSucceeded ||
		len(executed.CanonicalResult) == 0 ||
		executed.AttemptID != request.AttemptID ||
		!exactExecutorReceipt(executed.ProviderReceipt) {
		return false
	}
	probe := executed
	probe.CanonicalResult = json.RawMessage(`0`)
	frozen, _, err := moduleapi.NewActionExecutionResultV1(probe)
	if err != nil {
		return false
	}
	return moduleapi.ValidateActionExecutionResultForRequestV1(
		request,
		frozen,
	) == nil
}

func exactExecutorReceipt(receipt json.RawMessage) bool {
	if len(receipt) == 0 {
		return true
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		receipt,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: moduleapi.MaxActionReceiptBytesV1,
			MaxDepth: 32,
			MaxNodes: 64 << 10,
		},
	)
	return err == nil && len(canonical) != 0 && canonical[0] == '{' &&
		bytes.Equal(canonical, receipt)
}

func failedResult(
	attemptID string,
	provider moduleapi.ActivatedModuleRef,
	classification string,
) ResultV1 {
	return ResultV1{
		AttemptID:           attemptID,
		Provider:            provider,
		Outcome:             moduleapi.ActionExecutionFailed,
		ErrorClassification: classification,
	}
}

func unknownResult(
	attemptID string,
	provider moduleapi.ActivatedModuleRef,
	reason string,
) ResultV1 {
	return ResultV1{
		AttemptID:     attemptID,
		Provider:      provider,
		Outcome:       moduleapi.ActionExecutionUnknown,
		UnknownReason: reason,
	}
}

func exactActionPlan(
	member corecontract.MemberExecutionSnapshot,
) (moduleapi.PortPlan, error) {
	for _, plan := range member.PortPlans {
		if plan.Port == actionPortV1 {
			return moduleapi.NewPortPlan(plan)
		}
	}
	return moduleapi.PortPlan{}, fmt.Errorf("action.provider/v1 PortPlan is absent")
}

func canonicalBinding(binding moduleapi.PortBinding) ([]byte, error) {
	raw, err := json.Marshal(binding)
	if err != nil {
		return nil, err
	}
	return moduleapi.CanonicalJSON(raw)
}

func sameAttemptClosure(
	left currentstore.ActionDispatchAttemptRecord,
	right currentstore.ActionDispatchAttemptRecord,
) bool {
	return left.AttemptID == right.AttemptID &&
		left.RunID == right.RunID && left.MemberID == right.MemberID &&
		left.LogicalStepID == right.LogicalStepID &&
		left.SourceModelAttemptID == right.SourceModelAttemptID &&
		left.MemberSnapshotDigest == right.MemberSnapshotDigest &&
		left.BindingIndex == right.BindingIndex &&
		bytes.Equal(left.BindingCanonical, right.BindingCanonical) &&
		left.PublicActionID == right.PublicActionID &&
		left.ProviderActionID == right.ProviderActionID &&
		left.DefinitionDigest == right.DefinitionDigest &&
		left.ProposalRef == right.ProposalRef &&
		left.EffectClass == right.EffectClass &&
		left.MaxResultBytes == right.MaxResultBytes &&
		left.Deadline.Equal(right.Deadline) &&
		left.BudgetStateRef == right.BudgetStateRef &&
		left.State == right.State && left.Revision == right.Revision
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsOrWildcard(values []string, wanted string) bool {
	return contains(values, "*") || contains(values, wanted)
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
