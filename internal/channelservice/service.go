// Package channelservice admits authenticated, normalized Channel envelopes
// into the single Current Store and advances the resulting Run through the
// single Universal Loop. It owns no listener, adapter, queue, Store, Run, or
// scheduler.
package channelservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	channelJSONMediaType = "application/json"

	channelCancellationScope = "run"
	channelDefaultDeadline   = 2 * time.Minute
	channelLoopMaxDuration   = 2 * time.Minute
	channelPureLoopMaxSteps  = 2 // final model + CHANNEL_SEND
	channelActionMaxSteps    = 4 // model-1 + Action + model-2 + CHANNEL_SEND

	channelAdmissionKeyPrefix = "channel/v1/"
	channelRunIDPrefix        = "channel-run-"
	channelMemberIDPrefix     = "channel-member-"
	channelRecoveryPrefix     = "channel-recovery-"

	channelRunIDDomain            = "freeagent.channelservice.run-id/v1"
	channelMemberIDDomain         = "freeagent.channelservice.member-id/v1"
	channelRecoveryIDDomain       = "freeagent.channelservice.recovery-root/v1"
	channelAuthorizedReason       = "AUTHORIZED"
	channelIdentityRejectedReason = "IDENTITY_NOT_ACTIVE"
)

var (
	// ErrInvalidIngress identifies an invalid dependency or caller-supplied
	// Core scope. Adapter authentication errors never reach this package.
	ErrInvalidIngress = errors.New("channelservice: invalid ingress")

	// ErrIngressDenied identifies a missing, disabled, mismatched, or
	// unauthorized current Workspace route. It is deliberately fail-closed.
	ErrIngressDenied = errors.New("channelservice: ingress denied")

	// ErrIngressIntegrity identifies a result or frozen closure that does not
	// close to the exact authenticated event and admitted Run.
	ErrIngressIntegrity = errors.New("channelservice: ingress integrity violation")

	// ErrActionProfileUnsupported is returned by the pure constructor when a
	// selected Profile needs admission-time Action materialization. Callers
	// must opt into NewActionCapableService; there is no silent pure fallback.
	ErrActionProfileUnsupported = errors.New(
		"channelservice: Action-capable Channel profile is not enabled",
	)
)

var (
	modelGeneratePortV1 = moduleapi.PortRef{
		Name:         moduleapi.PortNameModelGenerate,
		ExactVersion: moduleapi.PortVersionV2,
	}
	channelTransportPortV1 = moduleapi.PortRef{
		Name:         moduleapi.PortNameChannelTransport,
		ExactVersion: moduleapi.PortVersionV1,
	}
)

// IngressInput is the entire Core-owned input after a concrete Adapter has
// authenticated and normalized one provider request. Tenant and Workspace
// are supplied by the trusted composition boundary; the Adapter cannot put
// either into ChannelInboundEnvelopeV1. Deadline is optional Core policy.
type IngressInput struct {
	TenantID    string
	WorkspaceID string
	Envelope    moduleapi.ChannelInboundEnvelopeV1
	Deadline    time.Time
}

// IngressResult reports the durable ingress identity and the exact Universal
// Loop result. Duplicate is true for a previously committed exact envelope.
// An ACCEPTED duplicate resumes only its original Run through the idempotent
// Universal Loop: READY may begin its first model Attempt, terminal/waiting is
// read as-is, and persisted PENDING is recovered without semantic replay.
// Deadline is populated only when this call created the Admission.
type IngressResult struct {
	IngressKey       string
	RunID            string
	Deadline         time.Time
	Receipt          currentstore.ChannelIngressReceipt
	AdmissionCreated bool
	Duplicate        bool
	LoopInvoked      bool
	LoopResult       loopapi.RunResult
}

// Service is a thin application boundary over the existing Current Store,
// Assembly Compiler, and Universal Loop. The caller retains dependency
// ownership and is responsible for Channel enablement and graceful shutdown.
type Service struct {
	store              *currentstore.Store
	loop               loopapi.Loop
	actionMaterializer assemblycompiler.ActionMaterializerV1
	maxSteps           uint32
	now                func() time.Time
}

// New constructs the pure model-to-Channel application service. It performs
// no Store read and therefore cannot make Pure Chat touch Channel state merely
// because this package is linked into a binary.
func New(store *currentstore.Store, loop loopapi.Loop) (*Service, error) {
	if store == nil || isNilDependency(loop) {
		return nil, fmt.Errorf(
			"%w: Current Store and Universal Loop are required",
			ErrInvalidIngress,
		)
	}
	return &Service{
		store: store, loop: loop, maxSteps: channelPureLoopMaxSteps, now: time.Now,
	}, nil
}

// NewActionCapableService explicitly enables admission-time Action Describe
// and the existing bounded model-1 -> Action -> model-2 -> Channel path. The
// supplied materializer is read-only and shares the caller's exact Registry;
// execution remains private to the existing Gateway.
func NewActionCapableService(
	store *currentstore.Store,
	loop loopapi.Loop,
	materializer assemblycompiler.ActionMaterializerV1,
) (*Service, error) {
	if isNilDependency(materializer) {
		return nil, fmt.Errorf(
			"%w: admission Action materializer is required",
			ErrInvalidIngress,
		)
	}
	service, err := New(store, loop)
	if err != nil {
		return nil, err
	}
	service.actionMaterializer = materializer
	service.maxSteps = channelActionMaxSteps
	return service, nil
}

// AdmitAndRun deduplicates first, resolves the current Workspace-owned route
// and identity, atomically commits ingress plus Run Admission, then advances
// exactly that original Run through the same idempotent Universal Loop.
func (service *Service) AdmitAndRun(
	ctx context.Context,
	input IngressInput,
) (IngressResult, error) {
	if service == nil || service.store == nil ||
		isNilDependency(service.loop) || service.maxSteps == 0 || service.now == nil {
		return IngressResult{}, fmt.Errorf(
			"%w: Service is not initialized",
			ErrInvalidIngress,
		)
	}
	if ctx == nil {
		return IngressResult{}, fmt.Errorf("%w: context is nil", ErrInvalidIngress)
	}
	if err := ctx.Err(); err != nil {
		return IngressResult{}, err
	}

	envelope, envelopeCanonical, err :=
		moduleapi.NewChannelInboundEnvelopeV1(input.Envelope)
	if err != nil {
		return IngressResult{}, fmt.Errorf("%w: envelope: %v", ErrInvalidIngress, err)
	}
	envelopeContent, err := contentInput(
		currentstore.ContentChannelIngressEnvelope,
		envelopeCanonical,
	)
	if err != nil {
		return IngressResult{}, err
	}
	ingressKey, providerEventDigest, err :=
		currentstore.ComputeChannelIngressIdentity(
			input.TenantID,
			envelope.EndpointID,
			envelope.ProviderEventID,
		)
	if err != nil {
		return IngressResult{}, fmt.Errorf("%w: identity: %v", ErrInvalidIngress, err)
	}
	result := IngressResult{IngressKey: ingressKey}

	// This lookup intentionally precedes Control/Catalog, Cursor and
	// compilation. A duplicate is a durable fact, never another Admission or
	// Run. ACCEPTED duplicates still resume that exact Run below so a crash
	// between the atomic commit and first Loop call cannot strand READY forever.
	existing, found, err := service.store.ResolveChannelIngress(
		ctx,
		input.TenantID,
		envelope.EndpointID,
		ingressKey,
		envelopeContent.Digest,
	)
	if err != nil {
		return result, err
	}
	if found {
		if existing.WorkspaceID != input.WorkspaceID ||
			existing.EndpointID != envelope.EndpointID ||
			existing.IngressKey != ingressKey ||
			existing.EnvelopeRef != envelopeContent.Digest {
			return result, fmt.Errorf(
				"%w: duplicate receipt belongs to another Core scope",
				ErrIngressIntegrity,
			)
		}
		result.RunID = existing.RunID
		result.Receipt = existing
		result.Duplicate = true
		if existing.Disposition != currentstore.ChannelIngressAccepted {
			return result, nil
		}
		if existing.RunID == "" {
			return result, fmt.Errorf(
				"%w: accepted duplicate has no original Run",
				ErrIngressIntegrity,
			)
		}
		stable, isStable, err := service.stableAcceptedDuplicateResult(
			ctx,
			existing.RunID,
		)
		if err != nil {
			return result, err
		}
		if isStable {
			result.LoopResult = stable
			return result, nil
		}
		if err := service.validateAcceptedDuplicateRoute(
			ctx,
			input.WorkspaceID,
			envelope,
			existing,
		); err != nil {
			return result, err
		}
		return service.advanceOriginalRun(ctx, result)
	}

	basis, control, catalog, err :=
		service.store.LoadPublishedBasis(ctx, input.TenantID)
	if err != nil {
		return result, err
	}
	route, workspace, err := resolveCurrentEndpoint(
		control,
		catalog,
		input.WorkspaceID,
		envelope,
	)
	if err != nil {
		return result, err
	}

	currentCursor, err := service.store.GetCurrentChannelCursor(
		ctx,
		input.TenantID,
		envelope.EndpointID,
		route.endpoint.CursorScopeKey,
	)
	if err != nil {
		return result, err
	}
	cursorBefore, err := contentInput(
		currentstore.ContentChannelCursor,
		envelope.CursorBefore,
	)
	if err != nil {
		return result, err
	}
	if currentCursor.WorkspaceID != input.WorkspaceID ||
		currentCursor.EndpointBindingDigest != route.bindingDigest ||
		currentCursor.CursorAfterRef != cursorBefore.Digest {
		return result, fmt.Errorf(
			"%w: current Cursor, Workspace, or Endpoint Binding changed",
			currentstore.ErrChannelIngressConflict,
		)
	}
	cursorAfter, err := contentInput(
		currentstore.ContentChannelCursor,
		envelope.CursorAfter,
	)
	if err != nil {
		return result, err
	}
	commonIngress := currentstore.ChannelIngressEventInput{
		PublishedBasis:         basis,
		TenantID:               input.TenantID,
		WorkspaceID:            input.WorkspaceID,
		EndpointID:             envelope.EndpointID,
		CursorScopeKey:         route.endpoint.CursorScopeKey,
		ExpectedCursorRevision: currentCursor.CursorRevision,
		CursorBeforeRef:        currentCursor.CursorAfterRef,
		CursorAfter:            cursorAfter,
		EndpointBindingDigest:  route.bindingDigest,
		IngressKey:             ingressKey,
		ProviderEventIDDigest:  providerEventDigest,
		Envelope:               envelopeContent,
	}

	var active bool
	route, active = bindCurrentIdentity(route, workspace, envelope)
	if !active {
		rejectedInput := commonIngress
		rejectedInput.Reason = channelIdentityRejectedReason
		receipt, rejectErr := service.store.CommitRejectedChannelIngress(
			ctx,
			rejectedInput,
		)
		if rejectErr != nil {
			return result, rejectErr
		}
		result.Receipt = receipt
		result.Duplicate = !receipt.Created
		return result, nil
	}
	profile, found := control.FindProfile(route.endpoint.TargetProfileID)
	if !found {
		return result, fmt.Errorf(
			"%w: selected Profile is absent from current Control",
			ErrIngressIntegrity,
		)
	}
	actionMaterials, err := service.loadActionBindingMaterials(ctx, profile)
	if err != nil {
		return result, err
	}
	if profileBindsAction(control, route.endpoint.TargetProfileID) &&
		isNilDependency(service.actionMaterializer) {
		return result, ErrActionProfileUnsupported
	}

	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          envelope.Message,
		},
	)
	if err != nil {
		return result, fmt.Errorf("%w: task input: %v", ErrInvalidIngress, err)
	}
	task, err := contentInput(currentstore.ContentTaskInput, taskCanonical)
	if err != nil {
		return result, err
	}
	deadline, err := service.freezeDeadline(input.Deadline)
	if err != nil {
		return result, err
	}
	admissionKey := channelAdmissionKeyPrefix + ingressKey
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:          input.TenantID,
			AdmissionKey:      admissionKey,
			PrincipalID:       route.principalID,
			WorkspaceID:       input.WorkspaceID,
			AgentID:           route.endpoint.TargetAgentID,
			ProfileID:         route.endpoint.TargetProfileID,
			ChannelEndpointID: envelope.EndpointID,
			TaskInputRef:      task.Digest,
			RequestedPorts:    []moduleapi.PortRef{modelGeneratePortV1},
			Deadline:          deadline,
			CancellationScope: channelCancellationScope,
			ExplicitLimits:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		return result, fmt.Errorf("%w: admission intent: %v", ErrInvalidIngress, err)
	}

	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil || controlRef != basis.Control {
		return result, fmt.Errorf(
			"%w: current Control does not reconstruct PublishedBasis: %v",
			ErrIngressIntegrity,
			err,
		)
	}
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil || catalogRef != basis.Catalog {
		return result, fmt.Errorf(
			"%w: current Catalog does not reconstruct PublishedBasis: %v",
			ErrIngressIntegrity,
			err,
		)
	}
	runID := deriveID(channelRunIDPrefix, channelRunIDDomain, ingressKey)
	memberID := deriveID(channelMemberIDPrefix, channelMemberIDDomain, ingressKey)
	recoveryRoot := deriveID(
		channelRecoveryPrefix,
		channelRecoveryIDDomain,
		ingressKey,
	)
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		ctx,
		assemblycompiler.CompileInput{
			IntentCanonical:        intentCanonical,
			IntentDigest:           intentDigest,
			RunID:                  runID,
			MemberID:               memberID,
			RecoveryRootRef:        recoveryRoot,
			PublishedBasis:         basis,
			ControlCanonical:       controlCanonical,
			CatalogCanonical:       catalogCanonical,
			ActionMaterializer:     service.actionMaterializer,
			ActionBindingMaterials: actionMaterials,
		},
	)
	if err != nil {
		return result, err
	}

	acceptedIngress := commonIngress
	acceptedIngress.Reason = channelAuthorizedReason
	committed, err := service.store.CommitChannelIngressAndRunAdmission(
		ctx,
		currentstore.CommitChannelIngressAdmissionInput{
			Ingress:     acceptedIngress,
			PrincipalID: route.principalID,
			ACLEpoch:    route.aclEpoch,
			Admission: currentstore.CommitRunAdmissionInput{
				PublishedBasis:          basis,
				IntentCanonical:         intentCanonical,
				IntentDigest:            intentDigest,
				MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
				RunManifestCanonical:    compiled.RunManifestCanonical,
				Contents:                []currentstore.ContentInput{task},
			},
		},
	)
	if err != nil {
		return result, err
	}
	result.RunID = committed.Admission.RunID
	result.Receipt = committed.Receipt
	result.AdmissionCreated = committed.Created
	if !committed.Created {
		result.Duplicate = true
		stable, isStable, err := service.stableAcceptedDuplicateResult(
			ctx,
			committed.Receipt.RunID,
		)
		if err != nil {
			return result, err
		}
		if isStable {
			result.LoopResult = stable
			return result, nil
		}
		if err := service.validateAcceptedDuplicateRoute(
			ctx,
			input.WorkspaceID,
			envelope,
			committed.Receipt,
		); err != nil {
			return result, err
		}
		return service.advanceOriginalRun(ctx, result)
	}
	if result.RunID != runID || committed.Receipt.RunID != runID ||
		committed.Receipt.IngressKey != ingressKey {
		return result, fmt.Errorf(
			"%w: committed ingress does not close to derived Run",
			ErrIngressIntegrity,
		)
	}
	result.Deadline = deadline
	return service.advanceOriginalRun(ctx, result)
}

// stableAcceptedDuplicateResult separates historical idempotency from a new
// permission to advance work. TERMINATED and WAITING_RECONCILIATION are read
// through existing Store projections and returned without taking a write
// lease or entering the Universal Loop. Every runnable state still passes the
// deny-only current-route check before it may advance.
func (service *Service) stableAcceptedDuplicateResult(
	ctx context.Context,
	runID string,
) (loopapi.RunResult, bool, error) {
	terminal, err := service.store.GetTerminalRunResult(ctx, runID)
	if err == nil {
		result := loopapi.RunResult{
			RunID:         terminal.RunID,
			Disposition:   loopapi.DispositionTerminated,
			FrameRevision: terminal.FrameRevision,
			ReasonCode:    terminal.ReasonCode,
		}
		if validationErr := result.Validate(); validationErr != nil ||
			result.RunID != runID {
			return loopapi.RunResult{}, false, fmt.Errorf(
				"%w: invalid terminal duplicate projection: %v",
				ErrIngressIntegrity,
				validationErr,
			)
		}
		return result, true, nil
	}
	if !errors.Is(err, currentstore.ErrTerminalRunUnavailable) {
		return loopapi.RunResult{}, false, err
	}

	runs, err := service.store.ScanStartupRecovery(ctx)
	if err != nil {
		return loopapi.RunResult{}, false, err
	}
	for _, run := range runs {
		if run.RunID != runID {
			continue
		}
		if run.FrameStep != corecontract.WaitingReconciliationLoopStep {
			return loopapi.RunResult{}, false, nil
		}
		result := loopapi.RunResult{
			RunID:         run.RunID,
			Disposition:   loopapi.DispositionWaitingReconciliation,
			FrameRevision: run.FrameRevision,
			ReasonCode:    run.UnknownReason,
		}
		if validationErr := result.Validate(); validationErr != nil {
			return loopapi.RunResult{}, false, fmt.Errorf(
				"%w: invalid waiting duplicate projection: %v",
				ErrIngressIntegrity,
				validationErr,
			)
		}
		return result, true, nil
	}

	// A concurrent writer may have terminalized the Run after the first
	// terminal read but before the non-terminal snapshot. Retry the existing
	// terminal projection once rather than misclassifying a missing Run as a
	// stable result.
	terminal, err = service.store.GetTerminalRunResult(ctx, runID)
	if err == nil {
		result := loopapi.RunResult{
			RunID:         terminal.RunID,
			Disposition:   loopapi.DispositionTerminated,
			FrameRevision: terminal.FrameRevision,
			ReasonCode:    terminal.ReasonCode,
		}
		if validationErr := result.Validate(); validationErr != nil ||
			result.RunID != runID {
			return loopapi.RunResult{}, false, fmt.Errorf(
				"%w: invalid terminal duplicate projection: %v",
				ErrIngressIntegrity,
				validationErr,
			)
		}
		return result, true, nil
	}
	if !errors.Is(err, currentstore.ErrTerminalRunUnavailable) {
		return loopapi.RunResult{}, false, err
	}
	return loopapi.RunResult{}, false, fmt.Errorf(
		"%w: accepted duplicate original Run %q has no stable or runnable projection",
		ErrIngressIntegrity,
		runID,
	)
}

// validateAcceptedDuplicateRoute is a deny-only current-policy check. It never
// recompiles, creates another Run, or changes the frozen Admission. It merely
// prevents an accepted-but-not-yet-terminal event from being advanced after
// its Endpoint, identity, ACL epoch, or exact provider Binding was revoked.
func (service *Service) validateAcceptedDuplicateRoute(
	ctx context.Context,
	workspaceID string,
	envelope moduleapi.ChannelInboundEnvelopeV1,
	receipt currentstore.ChannelIngressReceipt,
) error {
	_, control, catalog, err := service.store.LoadPublishedBasis(
		ctx,
		receipt.TenantID,
	)
	if err != nil {
		return err
	}
	route, err := resolveCurrentRoute(control, catalog, workspaceID, envelope)
	if err != nil {
		return err
	}
	if profileBindsAction(control, route.endpoint.TargetProfileID) &&
		isNilDependency(service.actionMaterializer) {
		return ErrActionProfileUnsupported
	}
	if receipt.WorkspaceID != workspaceID ||
		receipt.EndpointID != route.endpoint.EndpointID ||
		receipt.EndpointBindingDigest != route.bindingDigest ||
		receipt.PrincipalID != route.principalID ||
		receipt.ACLEpoch != route.aclEpoch {
		return fmt.Errorf(
			"%w: accepted ingress route or authority was revoked",
			ErrIngressDenied,
		)
	}
	return nil
}

func (service *Service) advanceOriginalRun(
	ctx context.Context,
	result IngressResult,
) (IngressResult, error) {
	result.LoopInvoked = true
	advanced, err := service.loop.Run(ctx, loopapi.RunInput{
		RunID:       result.RunID,
		MaxSteps:    service.maxSteps,
		MaxDuration: channelLoopMaxDuration,
	})
	if err != nil {
		return result, err
	}
	if err := advanced.Validate(); err != nil || advanced.RunID != result.RunID {
		return result, fmt.Errorf(
			"%w: Universal Loop returned another or invalid Run: %v",
			ErrIngressIntegrity,
			err,
		)
	}
	result.LoopResult = advanced
	return result, nil
}

type currentRoute struct {
	endpoint      controlcontract.ChannelEndpointDefinition
	principalID   string
	aclEpoch      uint64
	bindingDigest string
}

func resolveCurrentRoute(
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	workspaceID string,
	envelope moduleapi.ChannelInboundEnvelopeV1,
) (currentRoute, error) {
	route, workspace, err := resolveCurrentEndpoint(
		control,
		catalog,
		workspaceID,
		envelope,
	)
	if err != nil {
		return currentRoute{}, err
	}
	route, active := bindCurrentIdentity(route, workspace, envelope)
	if !active {
		return currentRoute{}, fmt.Errorf(
			"%w: authenticated external identity is not active",
			ErrIngressDenied,
		)
	}
	return route, nil
}

// resolveCurrentEndpoint proves only the Workspace-owned route and exact
// provider Binding. Identity is deliberately separate so an authenticated
// poison event can reach the Store's atomic REJECTED/Cursor transaction
// without treating a disabled or missing Endpoint as consumable input.
func resolveCurrentEndpoint(
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	workspaceID string,
	envelope moduleapi.ChannelInboundEnvelopeV1,
) (currentRoute, controlcontract.WorkspaceDefinition, error) {
	workspace, found := control.FindWorkspace(workspaceID)
	if !found {
		return currentRoute{}, controlcontract.WorkspaceDefinition{}, fmt.Errorf(
			"%w: Workspace is absent",
			ErrIngressDenied,
		)
	}
	endpoint, found := workspace.FindChannelEndpoint(envelope.EndpointID)
	if !found || !endpoint.Enabled {
		return currentRoute{}, controlcontract.WorkspaceDefinition{}, fmt.Errorf(
			"%w: Endpoint is absent or disabled",
			ErrIngressDenied,
		)
	}
	entry, found := catalog.FindInstance(endpoint.Binding.InstanceID)
	if !found || !providesExact(entry.Provides, channelTransportPortV1) {
		return currentRoute{}, controlcontract.WorkspaceDefinition{}, fmt.Errorf(
			"%w: exact Endpoint provider is absent from current Catalog",
			ErrIngressDenied,
		)
	}
	binding := moduleapi.PortBinding{
		Provider:            entry.Activation,
		ConfigRef:           endpoint.Binding.ConfigRef,
		AuthorityCeilingRef: endpoint.Binding.AuthorityCeilingRef,
		StaticContextRefs: append(
			[]string{},
			endpoint.Binding.StaticContextRefs...,
		),
		FailurePolicy: endpoint.Binding.FailurePolicy,
	}
	bindingDigest, err := moduleapi.ComputeChannelEndpointBindingDigestV1(binding)
	if err != nil {
		return currentRoute{}, controlcontract.WorkspaceDefinition{}, fmt.Errorf(
			"%w: invalid exact Endpoint binding: %v",
			ErrIngressIntegrity,
			err,
		)
	}
	return currentRoute{
		endpoint:      endpoint,
		bindingDigest: bindingDigest,
	}, workspace, nil
}

func bindCurrentIdentity(
	route currentRoute,
	workspace controlcontract.WorkspaceDefinition,
	envelope moduleapi.ChannelInboundEnvelopeV1,
) (currentRoute, bool) {
	var identity *controlcontract.ChannelIdentityDefinition
	for index := range workspace.ChannelIdentities {
		candidate := &workspace.ChannelIdentities[index]
		if candidate.Channel == route.endpoint.Channel &&
			candidate.AccountID == route.endpoint.AccountID &&
			candidate.ExternalUserID == envelope.ExternalUserID {
			identity = candidate
			break
		}
	}
	if identity == nil || !identity.Active || identity.ACLEpoch == 0 {
		return route, false
	}
	route.principalID = identity.PrincipalID
	route.aclEpoch = identity.ACLEpoch
	return route, true
}

func profileBindsAction(
	control controlcontract.ControlSnapshot,
	profileID string,
) bool {
	profile, found := control.FindProfile(profileID)
	if !found {
		return false
	}
	for _, binding := range profile.Bindings {
		if binding.Port.Name == moduleapi.PortNameActionProvider &&
			binding.Port.ExactVersion == moduleapi.PortVersionV1 {
			return true
		}
	}
	return false
}

// loadActionBindingMaterials mirrors the accepted local Chat admission rule:
// Config and Authority bodies are read in the frozen Profile Binding order,
// and only when action.provider/v1 is actually selected. Describe and action
// definition freezing remain owned by the single supplied materializer and
// Assembly Compiler.
func (service *Service) loadActionBindingMaterials(
	ctx context.Context,
	profile controlcontract.ProfileDefinition,
) ([]actionmaterializer.BindingMaterialV1, error) {
	actionBindingCount := 0
	for _, binding := range profile.Bindings {
		if binding.Port.Name == moduleapi.PortNameActionProvider &&
			binding.Port.ExactVersion == moduleapi.PortVersionV1 {
			actionBindingCount++
		}
	}
	if actionBindingCount == 0 {
		return nil, nil
	}
	if isNilDependency(service.actionMaterializer) {
		return nil, ErrActionProfileUnsupported
	}

	materials := make(
		[]actionmaterializer.BindingMaterialV1,
		0,
		actionBindingCount,
	)
	for index, binding := range profile.Bindings {
		if binding.Port.Name != moduleapi.PortNameActionProvider ||
			binding.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		config, err := service.store.GetContent(ctx, binding.ConfigRef)
		if err != nil {
			return nil, fmt.Errorf(
				"channelservice: load Action Binding %d CONFIG: %w",
				index,
				err,
			)
		}
		if config.Digest != binding.ConfigRef ||
			config.Kind != currentstore.ContentConfig ||
			config.MediaType != channelJSONMediaType {
			return nil, fmt.Errorf(
				"%w: Action Binding %d ConfigRef is not canonical CONFIG",
				ErrIngressIntegrity,
				index,
			)
		}
		authority, err := service.store.GetContent(
			ctx,
			binding.AuthorityCeilingRef,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"channelservice: load Action Binding %d AUTHORITY_CEILING: %w",
				index,
				err,
			)
		}
		if authority.Digest != binding.AuthorityCeilingRef ||
			authority.Kind != currentstore.ContentAuthorityCeiling ||
			authority.MediaType != channelJSONMediaType {
			return nil, fmt.Errorf(
				"%w: Action Binding %d AuthorityCeilingRef is not canonical AUTHORITY_CEILING",
				ErrIngressIntegrity,
				index,
			)
		}
		materials = append(materials, actionmaterializer.BindingMaterialV1{
			ConfigCanonical:    bytes.Clone(config.CanonicalBytes),
			AuthorityCanonical: bytes.Clone(authority.CanonicalBytes),
		})
	}
	return materials, nil
}

func providesExact(provides []moduleapi.PortRef, want moduleapi.PortRef) bool {
	for _, provided := range provides {
		if provided == want {
			return true
		}
	}
	return false
}

func (service *Service) freezeDeadline(input time.Time) (time.Time, error) {
	if input.IsZero() {
		return service.now().UTC().Round(0).Add(channelDefaultDeadline), nil
	}
	_, offset := input.Zone()
	if offset != 0 {
		return time.Time{}, fmt.Errorf(
			"%w: deadline must use UTC (zero offset)",
			ErrInvalidIngress,
		)
	}
	return input.Round(0).UTC(), nil
}

func contentInput(
	kind currentstore.ContentKind,
	canonical []byte,
) (currentstore.ContentInput, error) {
	body := bytes.Clone(canonical)
	digest, err := currentstore.ComputeContentDigest(
		kind,
		channelJSONMediaType,
		body,
	)
	if err != nil {
		return currentstore.ContentInput{}, fmt.Errorf(
			"%w: %s content: %v",
			ErrInvalidIngress,
			kind,
			err,
		)
	}
	return currentstore.ContentInput{
		Digest: digest, Kind: kind, MediaType: channelJSONMediaType,
		CanonicalBytes: body,
	}, nil
}

func deriveID(prefix, domain, stable string) string {
	return prefix + moduleapi.Digest(domain, []byte(stable))
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
