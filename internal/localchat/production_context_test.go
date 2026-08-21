package localchat

import (
	"bytes"
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestBootstrapDeclarativeContextReachesModelRequestWithoutReplay(
	t *testing.T,
) {
	ctx := context.Background()
	prepared, err := bootstrapseed.PrepareFile(productionSeedPath(t))
	if err != nil {
		t.Fatalf("PrepareFile() error = %v", err)
	}
	assertions := prepared.ModuleAssertions()
	if len(assertions) != 2 {
		t.Fatalf("ModuleAssertions() = %+v", assertions)
	}
	modelAssertion := prepared.ModelAssertion()
	contextAssertion := assertions[1]
	modelProvider := moduleapi.ActivatedModuleRef{
		ModuleID:           modelAssertion.ModuleID,
		Version:            modelAssertion.ExactVersion,
		ArtifactDigest:     modelAssertion.ArtifactDigest,
		InstanceID:         modelAssertion.InstanceID,
		ExecutionClass:     modelAssertion.ExpectedExecutionClass,
		AdapterIdentity:    modelAssertion.ExpectedAdapterIdentity,
		ActivationRevision: modelAssertion.ActivationRevision,
	}
	echo, err := exactadapter.NewDeterministicEcho(modelProvider)
	if err != nil {
		t.Fatalf("NewDeterministicEcho() error = %v", err)
	}
	recorder := &recordingChatInvoker{delegate: echo}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  modelProvider.ArtifactDigest,
		AdapterIdentity: modelProvider.AdapterIdentity,
		Invoker:         recorder,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	resolver, err := activationresolver.New(
		activationresolver.Config{
			DeclarativeAdapterIdentity: contextAssertion.ExpectedAdapterIdentity,
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
		t.Fatalf("activationresolver.New() error = %v", err)
	}
	store := newProductionSeedStore(t)
	if _, err := prepared.Import(ctx, store, resolver); err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatalf("NewUniversalLoop() error = %v", err)
	}
	service, err := NewChatService(store, loop)
	if err != nil {
		t.Fatalf("NewChatService() error = %v", err)
	}
	assembly := prepared.DefaultAssembly()
	input := ChatInput{
		TenantID:    assembly.TenantID,
		PrincipalID: "principal-production-seed",
		WorkspaceID: assembly.WorkspaceID,
		AgentID:     assembly.AgentID,
		ProfileID:   assembly.ProfileID,
		Message:     "verify declarative context",
		RequestID:   "production-context-request",
		Deadline:    time.Date(2099, time.January, 2, 3, 4, 5, 0, time.UTC),
	}

	first, err := service.Chat(ctx, input)
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}
	if first.TerminalResult == nil || first.Reply != input.Message ||
		recorder.callCount() != 1 {
		t.Fatalf("first Chat() = %+v, calls=%d", first, recorder.callCount())
	}
	second, err := service.Chat(ctx, input)
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if second.AdmissionCreated || second.RunID != first.RunID ||
		second.TerminalResult == nil ||
		second.TerminalResult.AttemptID != first.TerminalResult.AttemptID ||
		recorder.callCount() != 1 {
		t.Fatalf(
			"reentry Chat() = %+v, first=%+v, calls=%d",
			second,
			first,
			recorder.callCount(),
		)
	}

	requestCanonical := recorder.onlyInput(t)
	request, err := moduleapi.RestoreModelGenerateRequestV1(requestCanonical)
	if err != nil {
		t.Fatalf("RestoreModelGenerateRequestV1() error = %v", err)
	}
	if len(request.Messages) != 2 ||
		request.Messages[0].Role != moduleapi.ModelRoleSystem ||
		request.Messages[0].Content != "You are FreeAgent, a helpful assistant." ||
		request.Messages[1].Role != moduleapi.ModelRoleUser ||
		request.Messages[1].Content != input.Message {
		t.Fatalf("model request messages = %+v", request.Messages)
	}
	dispatch, err := store.GetModelDispatchRecord(
		ctx,
		first.TerminalResult.AttemptID,
	)
	if err != nil {
		t.Fatalf("GetModelDispatchRecord() error = %v", err)
	}
	if !bytes.Equal(dispatch.Attempt.Request.CanonicalBytes, requestCanonical) {
		t.Fatalf(
			"persisted request = %s, invoked request = %s",
			dispatch.Attempt.Request.CanonicalBytes,
			requestCanonical,
		)
	}

	lease, err := store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   first.RunID,
			OwnerID: "production-context-inspector",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("AcquireCurrentRunLease() error = %v", err)
	}
	defer func() {
		if err := store.ReleaseRunLease(context.Background(), lease); err != nil {
			t.Errorf("ReleaseRunLease() error = %v", err)
		}
	}()
	run, err := store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop() error = %v", err)
	}
	var contextPlan moduleapi.PortPlan
	for _, plan := range run.Member.PortPlans {
		if plan.Port.Name == moduleapi.PortNameContextProvide {
			contextPlan = plan
			break
		}
	}
	if contextPlan.Port != (moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}) || len(contextPlan.Bindings) != 1 {
		t.Fatalf("frozen context PortPlan = %+v", contextPlan)
	}
	binding := contextPlan.Bindings[0]
	if binding.Provider.ModuleID != contextAssertion.ModuleID ||
		binding.Provider.ArtifactDigest != contextAssertion.ArtifactDigest ||
		binding.Provider.ExecutionClass != moduleapi.ExecutionDeclarative ||
		binding.Provider.AdapterIdentity != contextAssertion.ExpectedAdapterIdentity ||
		len(binding.StaticContextRefs) != 1 {
		t.Fatalf("frozen context Binding = %+v", binding)
	}
	content, found := run.FindContent(binding.StaticContextRefs[0])
	if !found || content.Kind != currentstore.ContentStaticContext {
		t.Fatalf("frozen static context = %+v, found=%v", content, found)
	}
	staticContext, err := corecontract.RestoreStaticContextV1(content.CanonicalBytes)
	if err != nil || staticContext.Text != request.Messages[0].Content {
		t.Fatalf("frozen static context = %+v, error=%v", staticContext, err)
	}
}

func productionSeedPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(
		filepath.Dir(source),
		"..",
		"..",
		"examples",
		"current-v1.bootstrap.seed.json",
	))
}

func newProductionSeedStore(t *testing.T) *currentstore.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(context.Background(), path); err != nil {
		t.Fatalf("InitFreshCurrentStore() error = %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenExistingCurrentStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})
	return store
}
