package actiongateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestGatewayExecuteRechecksPendingClosureBeforeExecutor(t *testing.T) {
	tests := []struct {
		name               string
		mutate             func(*testing.T, *gatewayExecuteFixture)
		wantClassification string
	}{
		{
			name: "current activation revoked",
			mutate: func(_ *testing.T, fixture *gatewayExecuteFixture) {
				fixture.boundary.activationErr = errors.New("revoked after PENDING")
			},
			wantClassification: classificationActivationDenied,
		},
		{
			name: "lease rejected",
			mutate: func(_ *testing.T, fixture *gatewayExecuteFixture) {
				fixture.boundary.loadErr = currentstore.ErrRunLeaseConflict
			},
			wantClassification: classificationGatewayClosureDenied,
		},
		{
			name: "fencing revision drift",
			mutate: func(_ *testing.T, fixture *gatewayExecuteFixture) {
				fixture.boundary.run.Frame.Revision++
			},
			wantClassification: classificationGatewayClosureDenied,
		},
		{
			name: "deadline expired",
			mutate: func(_ *testing.T, fixture *gatewayExecuteFixture) {
				fixture.gateway.now = func() time.Time {
					return fixture.grant.Action.Attempt.Deadline
				}
			},
			wantClassification: classificationContextExpired,
		},
		{
			name: "authority drift",
			mutate: func(t *testing.T, fixture *gatewayExecuteFixture) {
				binding := gatewayActionBinding(t, fixture.boundary.run)
				record := gatewayContent(t, fixture.boundary.run, binding.AuthorityCeilingRef)
				authority, err := moduleapi.RestoreActionAuthorityCeilingV1(
					record.CanonicalBytes,
				)
				if err != nil {
					t.Fatal(err)
				}
				authority.AllowedWorkspaceIDs = []string{"revoked-workspace"}
				_, canonical, err := moduleapi.NewActionAuthorityCeilingV1(authority)
				if err != nil {
					t.Fatal(err)
				}
				replaceGatewayContent(t, &fixture.boundary.run, record.Digest, canonical)
			},
			wantClassification: classificationGatewayClosureDenied,
		},
		{
			name: "local effect drift",
			mutate: func(t *testing.T, fixture *gatewayExecuteFixture) {
				binding := gatewayActionBinding(t, fixture.boundary.run)
				record := gatewayContent(t, fixture.boundary.run, binding.ConfigRef)
				config, err := moduleapi.RestoreActionBindingConfigV1(record.CanonicalBytes)
				if err != nil {
					t.Fatal(err)
				}
				config.Actions[0].LocalEffectClass = moduleapi.EffectReadOnly
				_, canonical, err := moduleapi.NewActionBindingConfigV1(config)
				if err != nil {
					t.Fatal(err)
				}
				replaceGatewayContent(t, &fixture.boundary.run, record.Digest, canonical)
			},
			wantClassification: classificationGatewayClosureDenied,
		},
		{
			name: "budget head drift",
			mutate: func(t *testing.T, fixture *gatewayExecuteFixture) {
				sequence, err := corecontract.ParseUsageLedgerRefV1(
					fixture.boundary.run.Frame.UsageLedgerRef,
					fixture.boundary.run.RunID,
				)
				if err != nil {
					t.Fatal(err)
				}
				drifted, err := corecontract.NewUsageLedgerRefV1(
					fixture.boundary.run.RunID,
					sequence+1,
				)
				if err != nil {
					t.Fatal(err)
				}
				fixture.boundary.run.Frame.UsageLedgerRef = drifted
			},
			wantClassification: classificationGatewayClosureDenied,
		},
		{
			name: "persisted identity drift",
			mutate: func(_ *testing.T, fixture *gatewayExecuteFixture) {
				fixture.boundary.run.ActionDispatches[0].Attempt.ProviderActionID =
					"text.other"
			},
			wantClassification: classificationGatewayClosureDenied,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGatewayExecuteFixture(t, nil)
			test.mutate(t, fixture)
			result, err := fixture.gateway.Execute(context.Background(), fixture.grant)
			if err != nil {
				t.Fatalf("Gateway.Execute() error = %v", err)
			}
			if result.Outcome != moduleapi.ActionExecutionFailed ||
				result.ErrorClassification != test.wantClassification ||
				result.AttemptID != fixture.grant.Action.Attempt.AttemptID {
				t.Fatalf("Gateway denial = %+v", result)
			}
			if got := fixture.executor.calls.Load(); got != 0 {
				t.Fatalf("denied Gateway invoked executor %d times", got)
			}
		})
	}
}

func TestGatewayExecuteRechecksRevocationAfterLazyResolution(t *testing.T) {
	fixture := newGatewayExecuteFixture(t, nil)
	registry := &revokingGatewayRegistry{
		delegate: fixture.gateway.registry,
		afterResolve: func() {
			fixture.boundary.activationErr = errors.New(
				"revoked while exact adapter was loading",
			)
		},
	}
	gateway, err := New(fixture.boundary, registry)
	if err != nil {
		t.Fatal(err)
	}
	gateway.now = fixture.gateway.now

	result, err := gateway.Execute(context.Background(), fixture.grant)
	if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != classificationActivationDenied {
		t.Fatalf("revoked result=%+v error=%v", result, err)
	}
	if fixture.executor.calls.Load() != 0 {
		t.Fatalf("revoked execution entered executor %d times", fixture.executor.calls.Load())
	}
	if registry.resolutions.Load() != 1 || fixture.boundary.activation.Load() != 2 {
		t.Fatalf(
			"resolution/checks=%d/%d want 1/2",
			registry.resolutions.Load(),
			fixture.boundary.activation.Load(),
		)
	}
}

func TestGatewayExecuteRevocationBeforeFirstCheckSkipsLazyResolution(t *testing.T) {
	fixture := newGatewayExecuteFixture(t, nil)
	fixture.boundary.activationErr = errors.New("revoked before exact resolution")
	registry := &revokingGatewayRegistry{delegate: fixture.gateway.registry}
	gateway, err := New(fixture.boundary, registry)
	if err != nil {
		t.Fatal(err)
	}
	gateway.now = fixture.gateway.now

	result, err := gateway.Execute(context.Background(), fixture.grant)
	if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != classificationActivationDenied {
		t.Fatalf("pre-resolution revocation result=%+v error=%v", result, err)
	}
	if registry.resolutions.Load() != 0 || fixture.executor.calls.Load() != 0 ||
		fixture.boundary.activation.Load() != 1 {
		t.Fatalf(
			"pre-resolution revocation resolutions/executions/checks=%d/%d/%d want 0/0/1",
			registry.resolutions.Load(),
			fixture.executor.calls.Load(),
			fixture.boundary.activation.Load(),
		)
	}
}

func TestGatewayExecuteRevocationAfterSecondCheckDoesNotRewriteAdmittedCall(
	t *testing.T,
) {
	fixture := newGatewayExecuteFixture(t, nil)
	fixture.executor.result = func(request moduleapi.ActionExecutionRequestV1) (
		moduleapi.ActionExecutionResultV1,
		error,
	) {
		fixture.boundary.activationErr = errors.New(
			"revoked after execution admission",
		)
		return moduleapi.ActionExecutionResultV1{
			SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
			AttemptID:           request.AttemptID,
			Outcome:             moduleapi.ActionExecutionFailed,
			ErrorClassification: "PROVIDER_REJECTED",
		}, nil
	}

	result, err := fixture.gateway.Execute(context.Background(), fixture.grant)
	if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != "PROVIDER_REJECTED" {
		t.Fatalf("post-admission revocation result=%+v error=%v", result, err)
	}
	if fixture.boundary.activation.Load() != 2 ||
		fixture.executor.calls.Load() != 1 {
		t.Fatalf(
			"post-admission checks/executions=%d/%d want 2/1",
			fixture.boundary.activation.Load(),
			fixture.executor.calls.Load(),
		)
	}
}

func TestGatewayExecuteCopiedGrantInvokesExecutorExactlyOnce(t *testing.T) {
	fixture := newGatewayExecuteFixture(t, nil)
	const callers = 32
	type execution struct {
		result ResultV1
		err    error
	}
	start := make(chan struct{})
	executions := make(chan execution, callers)
	var wait sync.WaitGroup
	for range callers {
		grant := fixture.grant
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := fixture.gateway.Execute(context.Background(), grant)
			executions <- execution{result: result, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(executions)

	succeeded := 0
	denied := 0
	for execution := range executions {
		if execution.err != nil {
			if !errors.Is(execution.err, ErrInvalidGateway) {
				t.Fatalf("copied grant error = %v", execution.err)
			}
			denied++
			continue
		}
		if execution.result.Outcome != moduleapi.ActionExecutionSucceeded ||
			execution.result.AttemptID != fixture.grant.Action.Attempt.AttemptID {
			t.Fatalf("winning Gateway result = %+v", execution.result)
		}
		succeeded++
	}
	if succeeded != 1 || denied != callers-1 || fixture.executor.calls.Load() != 1 {
		t.Fatalf(
			"Gateway concurrency succeeded=%d denied=%d executor=%d",
			succeeded,
			denied,
			fixture.executor.calls.Load(),
		)
	}
}

func TestGatewayExecuteDistinguishesPostEffectIdentityAndResultFailures(
	t *testing.T,
) {
	t.Run("executor identity drift is UNKNOWN", func(t *testing.T) {
		fixture := newGatewayExecuteFixture(
			t,
			func(request moduleapi.ActionExecutionRequestV1) (
				moduleapi.ActionExecutionResultV1,
				error,
			) {
				return moduleapi.ActionExecutionResultV1{
					SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
					AttemptID:           request.AttemptID + "-other",
					Outcome:             moduleapi.ActionExecutionFailed,
					ErrorClassification: "PROVIDER_REJECTED",
				}, nil
			},
		)
		result, err := fixture.gateway.Execute(context.Background(), fixture.grant)
		if err != nil {
			t.Fatal(err)
		}
		if result.Outcome != moduleapi.ActionExecutionUnknown ||
			result.UnknownReason != unknownInvalidExecutorResult ||
			result.ErrorClassification != "" ||
			fixture.executor.calls.Load() != 1 {
			t.Fatalf("post-effect identity drift = %+v", result)
		}
	})

	t.Run("known success with rejected result stays SUCCEEDED", func(t *testing.T) {
		fixture := newGatewayExecuteFixture(
			t,
			func(request moduleapi.ActionExecutionRequestV1) (
				moduleapi.ActionExecutionResultV1,
				error,
			) {
				return moduleapi.ActionExecutionResultV1{
					SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
					AttemptID:           request.AttemptID,
					Outcome:             moduleapi.ActionExecutionSucceeded,
					CanonicalResult:     json.RawMessage(`"` + strings.Repeat("x", 300) + `"`),
					ProviderReceipt:     json.RawMessage(`{"receipt":"known"}`),
					ExternalOperationID: "known-operation-1",
				}, nil
			},
		)
		result, err := fixture.gateway.Execute(context.Background(), fixture.grant)
		if err != nil {
			t.Fatal(err)
		}
		if result.Outcome != moduleapi.ActionExecutionSucceeded ||
			result.ResultRejectionClassification != classificationResultRejected ||
			result.UnknownReason != "" || len(result.CanonicalResult) != 0 ||
			result.ExternalOperationID != "known-operation-1" ||
			fixture.executor.calls.Load() != 1 {
			t.Fatalf("known result rejection = %+v", result)
		}
	})
}

type gatewayExecuteFixture struct {
	grant    currentstore.CommitModelActionAndBeginDispatchResult
	boundary *gatewayStoreBoundary
	executor *countingGatewayAction
	gateway  *Gateway
}

type gatewayStoreBoundary struct {
	run           currentstore.RunForLoop
	loadErr       error
	activationErr error
	loadCalls     atomic.Uint32
	activation    atomic.Uint32
}

type revokingGatewayRegistry struct {
	delegate     modulehost.ExactAdapterRegistry
	afterResolve func()
	resolutions  atomic.Uint32
}

func (registry *revokingGatewayRegistry) ResolveExact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
) (modulehost.ModuleInvoker, error) {
	registry.resolutions.Add(1)
	invoker, err := registry.delegate.ResolveExact(
		ctx,
		artifactDigest,
		adapterIdentity,
	)
	if err == nil && registry.afterResolve != nil {
		registry.afterResolve()
	}
	return invoker, err
}

func (boundary *gatewayStoreBoundary) LoadRunForLoop(
	_ context.Context,
	_ currentstore.RunLease,
) (currentstore.RunForLoop, error) {
	boundary.loadCalls.Add(1)
	if boundary.loadErr != nil {
		return currentstore.RunForLoop{}, boundary.loadErr
	}
	return boundary.run, nil
}

func (boundary *gatewayStoreBoundary) CheckCurrentActivation(
	_ context.Context,
	_ string,
	_ moduleapi.PortRef,
	_ moduleapi.ActivatedModuleRef,
) error {
	boundary.activation.Add(1)
	return boundary.activationErr
}

type countingGatewayAction struct {
	delegate *exactadapter.TextStatsAction
	calls    atomic.Uint32
	result   func(moduleapi.ActionExecutionRequestV1) (
		moduleapi.ActionExecutionResultV1,
		error,
	)
}

var (
	_ modulehost.ModuleInvoker   = (*countingGatewayAction)(nil)
	_ moduleapi.ActionProviderV1 = (*countingGatewayAction)(nil)
	_ modulehost.ActionExecutor  = (*countingGatewayAction)(nil)
)

func (action *countingGatewayAction) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return action.delegate.Invoke(ctx, prepared)
}

func (action *countingGatewayAction) Describe(
	ctx context.Context,
	request moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	return action.delegate.Describe(ctx, request)
}

func (action *countingGatewayAction) Prepare(
	ctx context.Context,
	request moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	return action.delegate.Prepare(ctx, request)
}

func (action *countingGatewayAction) ExecutePrepared(
	ctx context.Context,
	execution modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	action.calls.Add(1)
	if action.result != nil {
		return action.result(execution.Request)
	}
	return action.delegate.ExecutePrepared(ctx, execution)
}

type gatewayAdmissionOnlyLoop struct{}

func (gatewayAdmissionOnlyLoop) Run(
	_ context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	return loopapi.RunResult{
		RunID:         input.RunID,
		Disposition:   loopapi.DispositionYielded,
		FrameRevision: 0,
		ReasonCode:    "admission-only",
	}, nil
}

func newGatewayExecuteFixture(
	t *testing.T,
	result func(moduleapi.ActionExecutionRequestV1) (
		moduleapi.ActionExecutionResultV1,
		error,
	),
) *gatewayExecuteFixture {
	t.Helper()
	ctx := context.Background()
	prepared, err := bootstrapseed.PrepareFile(filepath.Join(
		"..",
		"..",
		"examples",
		"current-v1.action.bootstrap.seed.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	modelAssertion := prepared.ModelAssertion()
	var actionAssertion bootstrapseed.ModuleAssertion
	for _, assertion := range prepared.ModuleAssertions() {
		if assertion.ExpectedAdapterIdentity ==
			"freeagent.adapter.action.text-stats/v1" {
			actionAssertion = assertion
			break
		}
	}
	if actionAssertion.ArtifactDigest == "" {
		t.Fatal("Action bootstrap assertion is absent")
	}
	modelProvider := gatewayActivatedProvider(modelAssertion)
	actionProvider := gatewayActivatedProvider(actionAssertion)
	echo, err := exactadapter.NewDeterministicEcho(modelProvider)
	if err != nil {
		t.Fatal(err)
	}
	textStats, err := exactadapter.NewTextStatsAction(actionProvider)
	if err != nil {
		t.Fatal(err)
	}
	executor := &countingGatewayAction{delegate: textStats, result: result}
	registry, err := exactadapter.NewRegistry(
		exactadapter.Registration{
			ArtifactDigest:  modelProvider.ArtifactDigest,
			AdapterIdentity: modelProvider.AdapterIdentity,
			Invoker:         echo,
		},
		exactadapter.Registration{
			ArtifactDigest:  actionProvider.ArtifactDigest,
			AdapterIdentity: actionProvider.AdapterIdentity,
			Invoker:         executor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	allowlist := make([]activationresolver.TrustedInProcessAllowlistEntry, 0, 2)
	for _, assertion := range prepared.ModuleAssertions() {
		if assertion.ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess {
			continue
		}
		allowlist = append(allowlist, activationresolver.TrustedInProcessAllowlistEntry{
			ModuleID:        assertion.ModuleID,
			ExactVersion:    assertion.ExactVersion,
			ArtifactDigest:  assertion.ArtifactDigest,
			AdapterIdentity: assertion.ExpectedAdapterIdentity,
		})
	}
	resolver, err := activationresolver.New(
		activationresolver.Config{
			DeclarativeAdapterIdentity: "freeagent.adapter.declarative/v1",
			TrustedInProcessAllowlist:  allowlist,
		},
		registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close Gateway fixture Store: %v", err)
		}
	})
	if _, err := prepared.Import(ctx, store, resolver); err != nil {
		t.Fatal(err)
	}
	materializer, err := actionmaterializer.New(registry)
	if err != nil {
		t.Fatal(err)
	}
	service, err := localchat.NewActionChatService(
		store,
		gatewayAdmissionOnlyLoop{},
		materializer,
	)
	if err != nil {
		t.Fatal(err)
	}
	assembly := prepared.DefaultAssembly()
	manifestDeadline := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	admitted, err := service.Chat(ctx, localchat.ChatInput{
		TenantID:    assembly.TenantID,
		PrincipalID: "gateway-test-principal",
		WorkspaceID: assembly.WorkspaceID,
		AgentID:     assembly.AgentID,
		ProfileID:   assembly.ProfileID,
		Message:     "count this Gateway request",
		RequestID:   "gateway-execute-request",
		Deadline:    manifestDeadline,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireCurrentRunLease(ctx, currentstore.AcquireCurrentRunLeaseInput{
		RunID:   admitted.RunID,
		OwnerID: "gateway-test-owner",
		TTL:     90 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	requestCanonical, compilationCanonical := gatewayCompileModelOne(t, run)
	modelBegin, err := store.BeginModelDispatch(ctx, currentstore.BeginModelDispatchInput{
		Lease:                       lease,
		AttemptID:                   "gateway-model-attempt-1",
		LogicalStepID:               corecontract.FirstModelLogicalStepIDV1,
		ContextCompilationCanonical: compilationCanonical,
		RequestCanonical:            requestCanonical,
		Deadline: time.Now().UTC().Add(60 * time.Minute).
			Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !modelBegin.ConsumeModelInvocationPermit() {
		t.Fatal("model fixture permit is absent")
	}
	definition := run.Member.Actions[0]
	canonicalInput, err := moduleapi.CanonicalizeAndValidateActionInputV1(
		definition.InputSchema,
		json.RawMessage(`{"text":"count this Gateway request"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, modelOutput, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			ActionRequest: &moduleapi.ModelActionRequestV1{
				ActionID:       definition.PublicActionID,
				CanonicalInput: canonicalInput,
			},
			ProviderRequestID: "gateway-model-provider-request-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, usageReceipt, err := moduleapi.NewModelUsageReceiptV2(
		moduleapi.ModelUsageReceiptV2{
			SchemaVersion: moduleapi.ModelUsageReceiptSchemaV2,
			RawReceipt:    json.RawMessage(`null`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	preparedPayload, err := executor.Prepare(ctx, moduleapi.ActionRequestV1{
		SchemaVersion:    moduleapi.ActionRequestSchemaV1,
		PublicActionID:   definition.PublicActionID,
		ProviderActionID: definition.ProviderActionID,
		DefinitionDigest: definition.DefinitionDigest,
		CanonicalInput:   canonicalInput,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, proposal, _, err := corecontract.NewActionProposalV1(
		run.Member.MemberSnapshotDigest,
		definition,
		canonicalInput,
		preparedPayload,
	)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.CommitModelActionAndBeginDispatch(
		ctx,
		currentstore.CommitModelActionAndBeginDispatchInput{
			Lease:                        modelBegin.Lease,
			ModelAttemptID:               modelBegin.Attempt.AttemptID,
			InvocationID:                 modelBegin.Attempt.AttemptID,
			Provider:                     modelBegin.Attempt.Binding.Provider,
			ExpectedModelAttemptRevision: modelBegin.Attempt.Revision,
			OutputCanonical:              modelOutput,
			UsageReceiptCanonical:        usageReceipt,
			ProviderRequestID:            "gateway-model-provider-request-1",
			DispatchAttemptID:            "gateway-action-attempt-1",
			ProposalCanonical:            proposal,
			Deadline: time.Now().UTC().Add(30 * time.Minute).
				Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !grant.Applied || !grant.GatewayAllowed {
		t.Fatalf("real Store grant = %+v", grant)
	}
	pending, err := store.LoadRunForLoop(ctx, grant.Lease)
	if err != nil {
		t.Fatal(err)
	}
	boundary := &gatewayStoreBoundary{run: pending}
	gateway, err := New(boundary, registry)
	if err != nil {
		t.Fatal(err)
	}
	gateway.now = func() time.Time {
		return grant.Action.Attempt.Deadline.Add(-time.Second)
	}
	return &gatewayExecuteFixture{
		grant:    grant,
		boundary: boundary,
		executor: executor,
		gateway:  gateway,
	}
}

func gatewayCompileModelOne(
	t *testing.T,
	run currentstore.RunForLoop,
) ([]byte, []byte) {
	t.Helper()
	modelBinding := gatewayModelBinding(t, run)
	modelConfigContent := gatewayContent(t, run, modelBinding.ConfigRef)
	modelConfig, err := moduleapi.RestoreModelBindingConfigV2(
		modelConfigContent.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	contextPolicy := gatewayContent(t, run, run.Member.ContextPolicy.Digest)
	task := gatewayContent(t, run, run.Manifest.TaskInputRef)
	contextPlan, contextMaterials := gatewayContextMaterials(t, run)
	compiled, err := contextcompiler.CompileV1(contextcompiler.CompileInputV1{
		TenantID:                       run.Manifest.TenantID,
		WorkspaceScope:                 run.Member.Workspace,
		AgentScope:                     run.Member.Agent,
		ContextPolicyRef:               run.Member.ContextPolicy,
		ContextPolicyDocumentCanonical: contextPolicy.CanonicalBytes,
		ModelParameters:                modelConfig.Parameters,
		ContextPlan:                    contextPlan,
		ContextBindings:                contextMaterials,
		Actions:                        run.Member.Actions,
		TaskInputRef:                   run.Manifest.TaskInputRef,
		TaskInputCanonical:             task.CanonicalBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Compilation == nil ||
		compiled.Compilation.ActionResultReservation == nil {
		t.Fatalf("Action compilation lacks reservation: %+v", compiled)
	}
	return bytes.Clone(compiled.RequestCanonical),
		bytes.Clone(compiled.CompilationCanonical)
}

func gatewayContextMaterials(
	t *testing.T,
	run currentstore.RunForLoop,
) (*moduleapi.PortPlan, []contextcompiler.BindingMaterialV1) {
	t.Helper()
	var selected *moduleapi.PortPlan
	for _, plan := range run.Member.PortPlans {
		if plan.Port.Name != moduleapi.PortNameContextProvide ||
			plan.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		if selected != nil {
			t.Fatal("duplicate context PortPlan")
		}
		frozen, err := moduleapi.NewPortPlan(plan)
		if err != nil {
			t.Fatal(err)
		}
		selected = &frozen
	}
	if selected == nil {
		return nil, nil
	}
	materials := make([]contextcompiler.BindingMaterialV1, len(selected.Bindings))
	for index, binding := range selected.Bindings {
		config := gatewayContent(t, run, binding.ConfigRef)
		materials[index].ConfigCanonical = bytes.Clone(config.CanonicalBytes)
		for _, ref := range binding.StaticContextRefs {
			content := gatewayContent(t, run, ref)
			materials[index].StaticContextCanonicals = append(
				materials[index].StaticContextCanonicals,
				bytes.Clone(content.CanonicalBytes),
			)
		}
	}
	return selected, materials
}

func gatewayModelBinding(
	t *testing.T,
	run currentstore.RunForLoop,
) moduleapi.PortBinding {
	t.Helper()
	for _, plan := range run.Member.PortPlans {
		if plan.Port.Name == moduleapi.PortNameModelGenerate &&
			plan.Port.ExactVersion == moduleapi.PortVersionV2 &&
			len(plan.Bindings) == 1 {
			return plan.Bindings[0]
		}
	}
	t.Fatal("exact model.generate/v2 Binding is absent")
	return moduleapi.PortBinding{}
}

func gatewayActionBinding(
	t *testing.T,
	run currentstore.RunForLoop,
) moduleapi.PortBinding {
	t.Helper()
	for _, plan := range run.Member.PortPlans {
		if plan.Port == actionPortV1 && len(plan.Bindings) == 1 {
			return plan.Bindings[0]
		}
	}
	t.Fatal("exact action.provider/v1 Binding is absent")
	return moduleapi.PortBinding{}
}

func gatewayContent(
	t *testing.T,
	run currentstore.RunForLoop,
	digest string,
) currentstore.ContentRecord {
	t.Helper()
	record, found := run.FindContent(digest)
	if !found {
		t.Fatalf("content %s is absent", digest)
	}
	return record
}

func replaceGatewayContent(
	t *testing.T,
	run *currentstore.RunForLoop,
	digest string,
	canonical []byte,
) {
	t.Helper()
	for index := range run.Contents {
		if run.Contents[index].Digest == digest {
			run.Contents[index].CanonicalBytes = bytes.Clone(canonical)
			return
		}
	}
	t.Fatalf("content %s is absent", digest)
}

func gatewayActivatedProvider(
	assertion bootstrapseed.ModuleAssertion,
) moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           assertion.ModuleID,
		Version:            assertion.ExactVersion,
		ArtifactDigest:     assertion.ArtifactDigest,
		InstanceID:         assertion.InstanceID,
		ExecutionClass:     assertion.ExpectedExecutionClass,
		AdapterIdentity:    assertion.ExpectedAdapterIdentity,
		ActivationRevision: assertion.ActivationRevision,
	}
}
