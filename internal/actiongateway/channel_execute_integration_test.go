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

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestExecuteChannelConsumesRealStorePermitExactlyOnceConcurrently(
	t *testing.T,
) {
	fixture := newRealChannelGatewayFixture(t)
	const callers = 12
	start := make(chan struct{})
	type outcome struct {
		result ChannelResultV1
		err    error
	}
	outcomes := make(chan outcome, callers)
	var workers sync.WaitGroup
	workers.Add(callers)
	for range callers {
		go func() {
			defer workers.Done()
			<-start
			result, err := fixture.gateway.ExecuteChannel(
				context.Background(),
				fixture.grant,
			)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)
	select {
	case <-fixture.executor.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("real Store permit did not reach Channel executor")
	}
	close(fixture.executor.release)
	workers.Wait()
	close(outcomes)

	var succeeded, denied int
	for got := range outcomes {
		switch {
		case got.err == nil:
			succeeded++
			if got.result.Outcome != moduleapi.ChannelExecutionSucceeded ||
				got.result.AttemptID != fixture.grant.Channel.Attempt.AttemptID ||
				got.result.ExternalOperationID != "delivery-real-permit-1" {
				t.Fatalf("successful real permit result=%+v", got.result)
			}
		case errors.Is(got.err, ErrInvalidGateway):
			denied++
		default:
			t.Fatalf("unexpected concurrent Gateway error=%v", got.err)
		}
	}
	if succeeded != 1 || denied != callers-1 || fixture.executor.calls.Load() != 1 {
		t.Fatalf(
			"real permit fan-out: succeeded=%d denied=%d executor_calls=%d",
			succeeded,
			denied,
			fixture.executor.calls.Load(),
		)
	}
	if _, err := fixture.gateway.ExecuteChannel(
		context.Background(),
		fixture.grant,
	); !errors.Is(err, ErrInvalidGateway) {
		t.Fatalf("sequential real permit replay error=%v", err)
	}
	if fixture.executor.calls.Load() != 1 {
		t.Fatalf("real permit replay reached executor %d times", fixture.executor.calls.Load())
	}
}

type realChannelGatewayFixture struct {
	grant    currentstore.CommitModelChannelAndBeginDispatchResult
	executor *blockingGatewayChannel
	gateway  *Gateway
}

type blockingGatewayChannel struct {
	calls   atomic.Uint32
	entered chan struct{}
	release chan struct{}
}

var (
	_ modulehost.ModuleInvoker   = (*blockingGatewayChannel)(nil)
	_ modulehost.ChannelExecutor = (*blockingGatewayChannel)(nil)
)

func (*blockingGatewayChannel) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return modulehost.InvocationResult{}, errors.New("generic invocation is forbidden")
}

func (channel *blockingGatewayChannel) ExecutePrepared(
	ctx context.Context,
	request moduleapi.ChannelExecutionRequestV1,
) (moduleapi.ChannelExecutionResultV1, error) {
	channel.calls.Add(1)
	select {
	case channel.entered <- struct{}{}:
	default:
	}
	select {
	case <-channel.release:
	case <-ctx.Done():
		return moduleapi.ChannelExecutionResultV1{}, ctx.Err()
	}
	result, _, err := moduleapi.NewChannelExecutionResultV1(
		moduleapi.ChannelExecutionResultV1{
			SchemaVersion:       moduleapi.ChannelExecutionResultSchemaV1,
			AttemptID:           request.AttemptID,
			Outcome:             moduleapi.ChannelExecutionSucceeded,
			ExternalOperationID: "delivery-real-permit-1",
			ProviderReceipt:     json.RawMessage(`{"accepted":true}`),
		},
	)
	return result, err
}

func newRealChannelGatewayFixture(t *testing.T) *realChannelGatewayFixture {
	t.Helper()
	ctx := context.Background()
	prepared, err := bootstrapseed.PrepareFile(filepath.Join(
		"..",
		"..",
		"examples",
		"current-v1.bootstrap.seed.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	modelAssertion := prepared.ModelAssertion()
	modelProvider := gatewayActivatedProvider(modelAssertion)
	modelAdapter, err := exactadapter.NewDeterministicEcho(modelProvider)
	if err != nil {
		t.Fatal(err)
	}
	channelProvider := realGatewayChannelProvider()
	channelExecutor := &blockingGatewayChannel{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	registry, err := exactadapter.NewRegistry(
		exactadapter.Registration{
			ArtifactDigest:  modelProvider.ArtifactDigest,
			AdapterIdentity: modelProvider.AdapterIdentity,
			Invoker:         modelAdapter,
		},
		exactadapter.Registration{
			ArtifactDigest:  channelProvider.ArtifactDigest,
			AdapterIdentity: channelProvider.AdapterIdentity,
			Invoker:         channelExecutor,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := activationresolver.New(
		activationresolver.Config{
			DeclarativeAdapterIdentity: "freeagent.adapter.declarative/v1",
			TrustedInProcessAllowlist: []activationresolver.TrustedInProcessAllowlistEntry{{
				ModuleID:        modelAssertion.ModuleID,
				ExactVersion:    modelAssertion.ExactVersion,
				ArtifactDigest:  modelAssertion.ArtifactDigest,
				AdapterIdentity: modelAssertion.ExpectedAdapterIdentity,
			}},
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
			t.Errorf("close real Channel Gateway Store: %v", err)
		}
	})
	if _, err := prepared.Import(ctx, store, resolver); err != nil {
		t.Fatal(err)
	}
	assembly := prepared.DefaultAssembly()
	binding, bindingDigest, basis, controlCanonical, catalogCanonical :=
		installAndEnableGatewayChannel(
			t,
			store,
			assembly,
			channelProvider,
		)
	seed := seedGatewayChannelCursor(t, store, basis, assembly, bindingDigest)
	basis, controlCanonical, catalogCanonical = enableGatewayChannelEndpoint(
		t,
		store,
		basis,
		controlCanonical,
		catalogCanonical,
	)
	admitted := admitGatewayChannelRun(
		t,
		store,
		basis,
		controlCanonical,
		catalogCanonical,
		assembly,
		seed,
		bindingDigest,
	)
	lease, err := store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   admitted.RunID,
			OwnerID: "gateway-channel-owner",
			TTL:     90 * time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := exactChannelPlan(run.Member); err != nil ||
		len(got.Bindings) != 1 || got.Bindings[0].Provider != binding.Provider {
		t.Fatalf("admitted Channel plan=%+v err=%v", got, err)
	}
	requestCanonical, compilationCanonical := gatewayCompileChannelModel(t, run)
	modelBegin, err := store.BeginModelDispatch(
		ctx,
		currentstore.BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "gateway-channel-model-attempt-1",
			LogicalStepID:               corecontract.FirstModelLogicalStepIDV1,
			ContextCompilationCanonical: compilationCanonical,
			RequestCanonical:            requestCanonical,
			Deadline: time.Now().UTC().Add(45 * time.Minute).
				Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !modelBegin.ConsumeModelInvocationPermit() {
		t.Fatal("real Channel fixture Model permit is absent")
	}
	assistantText := "channel answer from the real Store closure"
	_, modelOutput, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     assistantText,
			ProviderRequestID: "gateway-channel-model-provider-request-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, usageReceipt, err := moduleapi.NewModelUsageReceiptV1(
		moduleapi.ModelUsageReceiptV1{
			SchemaVersion: moduleapi.ModelUsageReceiptSchemaV1,
			RawReceipt:    json.RawMessage(`null`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if run.ChannelIngress == nil || run.ChannelIngressEnvelope == nil {
		t.Fatal("accepted Channel ingress is absent from real Run closure")
	}
	envelope, err := moduleapi.RestoreChannelInboundEnvelopeV1(
		run.ChannelIngressEnvelope.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, proposalCanonical, _, err := moduleapi.NewChannelSendProposalV1(
		run.Member.MemberSnapshotDigest,
		run.ChannelIngress.EndpointID,
		run.ChannelIngress.IngressKey,
		envelope.ReplyTarget,
		assistantText,
		json.RawMessage(`{"message":"channel answer from the real Store closure"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.CommitModelChannelAndBeginDispatch(
		ctx,
		currentstore.CommitModelChannelAndBeginDispatchInput{
			Lease:                        modelBegin.Lease,
			ModelAttemptID:               modelBegin.Attempt.AttemptID,
			InvocationID:                 modelBegin.Attempt.AttemptID,
			Provider:                     modelBegin.Attempt.Binding.Provider,
			ExpectedModelAttemptRevision: modelBegin.Attempt.Revision,
			OutputCanonical:              modelOutput,
			UsageReceiptCanonical:        usageReceipt,
			ProviderRequestID:            "gateway-channel-model-provider-request-1",
			DispatchAttemptID:            "gateway-channel-attempt-1",
			ProposalCanonical:            proposalCanonical,
			Deadline: time.Now().UTC().Add(30 * time.Minute).
				Truncate(time.Microsecond),
			BudgetDecision: currentstore.ChannelBudgetAllow,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !grant.Applied || !grant.GatewayAllowed {
		t.Fatalf("real Channel Store grant=%+v", grant)
	}
	gateway, err := New(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	gateway.now = func() time.Time {
		return grant.Channel.Attempt.Deadline.Add(-time.Second)
	}
	return &realChannelGatewayFixture{
		grant:    grant,
		executor: channelExecutor,
		gateway:  gateway,
	}
}

func realGatewayChannelProvider() moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           "freeagent.test.channel",
		Version:            "1.0.0",
		ArtifactDigest:     strings.Repeat("c", moduleapi.SHA256HexLength),
		InstanceID:         "gateway-channel-instance",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "freeagent.adapter.channel.test/v1",
		ActivationRevision: 1,
	}
}

func installAndEnableGatewayChannel(
	t *testing.T,
	store *currentstore.Store,
	assembly bootstrapseed.DefaultAssembly,
	provider moduleapi.ActivatedModuleRef,
) (
	moduleapi.PortBinding,
	string,
	controlcontract.PublishedBasis,
	[]byte,
	[]byte,
) {
	t.Helper()
	ctx := context.Background()
	_, configCanonical, err := moduleapi.NewChannelBindingConfigV1(
		moduleapi.ChannelBindingConfigV1{
			SchemaVersion:   moduleapi.ChannelBindingConfigSchemaV1,
			AdapterProtocol: "gateway-test/v1",
			SecretRef:       "placeholder",
			Parameters:      json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	config := putRealGatewayContent(
		t,
		store,
		currentstore.ContentConfig,
		configCanonical,
	)
	_, authorityCanonical, err := moduleapi.NewChannelAuthorityCeilingV1(
		moduleapi.ChannelAuthorityCeilingV1{
			SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
			TenantID:            "default",
			AllowedWorkspaceIDs: []string{assembly.WorkspaceID},
			AllowedEndpointIDs:  []string{"gateway-channel-endpoint"},
			AllowReceive:        true,
			AllowSend:           true,
			MaxMessageBytes:     moduleapi.MaxChannelMessageBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authority := putRealGatewayContent(
		t,
		store,
		currentstore.ContentAuthorityCeiling,
		authorityCanonical,
	)
	manifestRaw, err := json.Marshal(moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         provider.ModuleID,
		Version:    provider.Version,
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol:   moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint: provider.AdapterIdentity,
		},
		Provides: []moduleapi.PortRef{channelPortV1},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestCanonical, err := moduleapi.CanonicalJSON(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		manifestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := store.InstallModule(
		ctx,
		currentstore.InstallModuleInput{
			InstallationID:      "gateway-channel-installation",
			ModuleID:            provider.ModuleID,
			ExactVersion:        provider.Version,
			ExpectedManifestRef: manifestRef,
			ManifestBytes:       manifestCanonical,
			ArtifactDigest:      provider.ArtifactDigest,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := store.ActivateModule(
		ctx,
		currentstore.ActivateModuleInput{
			ActivationID:       "gateway-channel-activation",
			TenantID:           "default",
			InstanceID:         provider.InstanceID,
			InstallationID:     installation.InstallationID,
			ActivationRevision: provider.ActivationRevision,
			ExecutionClass:     provider.ExecutionClass,
			AdapterIdentity:    provider.AdapterIdentity,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if activation.InstanceID != provider.InstanceID ||
		installation.ArtifactDigest != provider.ArtifactDigest {
		t.Fatal("installed Channel provider identity differs from Registry")
	}
	binding := moduleapi.PortBinding{
		Provider:            provider,
		ConfigRef:           config.Digest,
		AuthorityCeilingRef: authority.Digest,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	bindingDigest, err := moduleapi.ComputeChannelEndpointBindingDigestV1(binding)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	workspaceIndex := -1
	for index := range control.Workspaces {
		if control.Workspaces[index].Workspace.ID == assembly.WorkspaceID {
			workspaceIndex = index
			break
		}
	}
	if workspaceIndex < 0 {
		t.Fatal("default Workspace is absent")
	}
	control.SnapshotID = "control-gateway-channel-disabled"
	control.Revision++
	control.Digest = ""
	control.Workspaces[workspaceIndex].ChannelEndpoints =
		[]controlcontract.ChannelEndpointDefinition{{
			SchemaVersion:   controlcontract.ChannelEndpointSchemaVersionV1,
			EndpointID:      "gateway-channel-endpoint",
			Channel:         "gateway-test",
			AccountID:       "gateway-account",
			ConversationID:  "gateway-conversation",
			TargetAgentID:   assembly.AgentID,
			TargetProfileID: assembly.ProfileID,
			CursorScopeKey:  "gateway-conversation",
			Enabled:         false,
			Binding: controlcontract.BindingSpec{
				Port:                channelPortV1,
				InstanceID:          provider.InstanceID,
				ConfigRef:           binding.ConfigRef,
				AuthorityCeilingRef: binding.AuthorityCeilingRef,
				StaticContextRefs:   []string{},
				FailurePolicy:       moduleapi.FailureRequired,
			},
		}}
	control.Workspaces[workspaceIndex].ChannelIdentities =
		[]controlcontract.ChannelIdentityDefinition{{
			Channel:        "gateway-test",
			AccountID:      "gateway-account",
			ExternalUserID: "gateway-external-user",
			PrincipalID:    "gateway-channel-principal",
			ACLEpoch:       1,
			Active:         true,
		}}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-gateway-channel-disabled"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{
		Activation: provider,
		Provides:   []moduleapi.PortRef{channelPortV1},
	})
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err = store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return binding, bindingDigest, basis, controlCanonical, catalogCanonical
}

func seedGatewayChannelCursor(
	t *testing.T,
	store *currentstore.Store,
	basis controlcontract.PublishedBasis,
	assembly bootstrapseed.DefaultAssembly,
	bindingDigest string,
) currentstore.ChannelIngressReceipt {
	t.Helper()
	cursor := realGatewayContentInput(
		t,
		currentstore.ContentChannelCursor,
		json.RawMessage(`{"offset":0}`),
	)
	receipt, err := store.SeedChannelCursor(
		context.Background(),
		currentstore.ChannelCursorSeedInput{
			PublishedBasis:        basis,
			TenantID:              basis.TenantID,
			WorkspaceID:           assembly.WorkspaceID,
			EndpointID:            "gateway-channel-endpoint",
			CursorScopeKey:        "gateway-conversation",
			EndpointBindingDigest: bindingDigest,
			CursorAfter:           cursor,
			Reason:                "GATEWAY_TEST_SEED",
			EndpointDisabled:      true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func enableGatewayChannelEndpoint(
	t *testing.T,
	store *currentstore.Store,
	basis controlcontract.PublishedBasis,
	controlCanonical []byte,
	catalogCanonical []byte,
) (controlcontract.PublishedBasis, []byte, []byte) {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		controlCanonical,
		basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	control.SnapshotID = "control-gateway-channel-enabled"
	control.Revision++
	control.Digest = ""
	control.Workspaces[0].ChannelEndpoints[0].Enabled = true
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		catalogCanonical,
		basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-gateway-channel-enabled"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err = store.PublishControlCatalog(
		context.Background(),
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return basis, controlCanonical, catalogCanonical
}

func admitGatewayChannelRun(
	t *testing.T,
	store *currentstore.Store,
	basis controlcontract.PublishedBasis,
	controlCanonical []byte,
	catalogCanonical []byte,
	assembly bootstrapseed.DefaultAssembly,
	seed currentstore.ChannelIngressReceipt,
	bindingDigest string,
) currentstore.RunAdmissionResult {
	t.Helper()
	ctx := context.Background()
	before, err := store.GetContent(ctx, seed.CursorAfterRef)
	if err != nil {
		t.Fatal(err)
	}
	after := realGatewayContentInput(
		t,
		currentstore.ContentChannelCursor,
		json.RawMessage(`{"offset":1}`),
	)
	_, envelopeCanonical, err := moduleapi.NewChannelInboundEnvelopeV1(
		moduleapi.ChannelInboundEnvelopeV1{
			SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
			EndpointID:      "gateway-channel-endpoint",
			ProviderEventID: "gateway-provider-event-1",
			ExternalUserID:  "gateway-external-user",
			Message:         "channel question",
			ReplyTarget:     json.RawMessage(`{"conversation":"gateway-conversation"}`),
			CursorBefore:    json.RawMessage(before.CanonicalBytes),
			CursorAfter:     json.RawMessage(after.CanonicalBytes),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope := realGatewayContentInput(
		t,
		currentstore.ContentChannelIngressEnvelope,
		envelopeCanonical,
	)
	ingressKey, providerEventDigest, err :=
		currentstore.ComputeChannelIngressIdentity(
			basis.TenantID,
			"gateway-channel-endpoint",
			"gateway-provider-event-1",
		)
	if err != nil {
		t.Fatal(err)
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          "channel question",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task := realGatewayContentInput(
		t,
		currentstore.ContentTaskInput,
		taskCanonical,
	)
	deadline := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion: corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:      basis.TenantID,
			AdmissionKey:  "channel/v1/" + ingressKey,
			PrincipalID:   "gateway-channel-principal",
			WorkspaceID:   assembly.WorkspaceID,
			AgentID:       assembly.AgentID,
			ProfileID:     assembly.ProfileID,
			TaskInputRef:  task.Digest,
			RequestedPorts: []moduleapi.PortRef{{
				Name:         moduleapi.PortNameModelGenerate,
				ExactVersion: moduleapi.PortVersionV1,
			}},
			ChannelEndpointID: "gateway-channel-endpoint",
			Deadline:          deadline,
			CancellationScope: "run",
			ExplicitLimits:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		ctx,
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            "gateway-channel-run-1",
			MemberID:         "gateway-channel-member-1",
			RecoveryRootRef:  "recovery/gateway-channel-run-1",
			PublishedBasis:   basis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.CommitChannelIngressAndRunAdmission(
		ctx,
		currentstore.CommitChannelIngressAdmissionInput{
			Ingress: currentstore.ChannelIngressEventInput{
				PublishedBasis:         basis,
				TenantID:               basis.TenantID,
				WorkspaceID:            assembly.WorkspaceID,
				EndpointID:             "gateway-channel-endpoint",
				CursorScopeKey:         "gateway-conversation",
				ExpectedCursorRevision: seed.CursorRevision,
				CursorBeforeRef:        seed.CursorAfterRef,
				CursorAfter:            after,
				EndpointBindingDigest:  bindingDigest,
				IngressKey:             ingressKey,
				ProviderEventIDDigest:  providerEventDigest,
				Envelope:               envelope,
				Reason:                 "AUTHORIZED",
			},
			PrincipalID: "gateway-channel-principal",
			ACLEpoch:    1,
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
		t.Fatal(err)
	}
	if !result.Created || !result.Admission.Created {
		t.Fatalf("real Channel admission=%+v", result)
	}
	return result.Admission
}

func gatewayCompileChannelModel(
	t *testing.T,
	run currentstore.RunForLoop,
) ([]byte, []byte) {
	t.Helper()
	modelBinding := gatewayModelBinding(t, run)
	modelConfigContent := gatewayContent(t, run, modelBinding.ConfigRef)
	modelConfig, err := moduleapi.RestoreModelBindingConfigV1(
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
	if compiled.Compilation != nil ||
		len(compiled.CompilationCanonical) != 0 ||
		len(run.Member.Actions) != 0 {
		t.Fatalf("Channel fixture did not compile as Pure Chat: %+v", compiled)
	}
	return bytes.Clone(compiled.RequestCanonical),
		bytes.Clone(compiled.CompilationCanonical)
}

func putRealGatewayContent(
	t *testing.T,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	canonical []byte,
) currentstore.ContentRecord {
	t.Helper()
	input := realGatewayContentInput(t, kind, canonical)
	record, err := store.PutContent(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func realGatewayContentInput(
	t *testing.T,
	kind currentstore.ContentKind,
	canonical []byte,
) currentstore.ContentInput {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := currentstore.ComputeContentDigest(
		kind,
		"application/json",
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return currentstore.ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      "application/json",
		CanonicalBytes: canonical,
	}
}
