package localchat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	roleModuleID     = "freeagent.example.role.architect"
	skillModuleID    = "freeagent.example.skill.implementation"
	roleContextText  = "Role: prioritize clear architecture, explicit trade-offs, and coherent system structure."
	skillContextText = "Skill: implement concrete details precisely and keep changes scoped to the requested work."
)

func TestDeclarativeRoleAndSkillTraverseProductionPureChatChain(t *testing.T) {
	t.Run("role and skill retain frozen order", func(t *testing.T) {
		prepared := prepareRoleSkillSeed(t, true)
		service, store, recorder := newRoleSkillChatService(t, prepared)
		assembly := prepared.DefaultAssembly()
		input := ChatInput{
			TenantID:    assembly.TenantID,
			PrincipalID: "principal-role-skill-e2e",
			WorkspaceID: assembly.WorkspaceID,
			AgentID:     assembly.AgentID,
			ProfileID:   assembly.ProfileID,
			Message:     "build the smallest viable feature",
			RequestID:   "role-skill-e2e-request",
			Deadline:    time.Date(2099, time.January, 2, 3, 4, 5, 0, time.UTC),
		}

		result, err := service.Chat(context.Background(), input)
		if err != nil {
			t.Fatalf("Chat() error = %v", err)
		}
		if result.TerminalResult == nil || result.Reply != input.Message || recorder.callCount() != 1 {
			t.Fatalf("Chat() = %+v, model calls=%d", result, recorder.callCount())
		}
		request := recordedRequest(t, recorder)
		wantMessages := []moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: roleContextText},
			{Role: moduleapi.ModelRoleSystem, Content: skillContextText},
			{Role: moduleapi.ModelRoleUser, Content: input.Message},
		}
		if !modelMessagesEqual(request.Messages, wantMessages) {
			t.Fatalf("model request messages = %+v, want %+v", request.Messages, wantMessages)
		}

		plan := loadContextPlan(t, store, result.RunID)
		if len(plan.Bindings) != 2 ||
			plan.Bindings[0].Provider.ModuleID != roleModuleID ||
			plan.Bindings[1].Provider.ModuleID != skillModuleID ||
			len(plan.Bindings[0].StaticContextRefs) != 1 ||
			len(plan.Bindings[1].StaticContextRefs) != 1 {
			t.Fatalf("frozen Role/Skill PortPlan = %+v", plan)
		}
	})

	t.Run("pure chat stays minimal without declarative modules", func(t *testing.T) {
		prepared := prepareRoleSkillSeed(t, false)
		service, store, recorder := newRoleSkillChatService(t, prepared)
		assembly := prepared.DefaultAssembly()
		input := ChatInput{
			TenantID:    assembly.TenantID,
			PrincipalID: "principal-minimal-e2e",
			WorkspaceID: assembly.WorkspaceID,
			AgentID:     assembly.AgentID,
			ProfileID:   assembly.ProfileID,
			Message:     "plain chat",
			RequestID:   "minimal-e2e-request",
			Deadline:    time.Date(2099, time.January, 2, 3, 4, 5, 0, time.UTC),
		}

		result, err := service.Chat(context.Background(), input)
		if err != nil {
			t.Fatalf("Chat() error = %v", err)
		}
		request := recordedRequest(t, recorder)
		wantMessages := []moduleapi.ModelMessageV1{{
			Role: moduleapi.ModelRoleUser, Content: input.Message,
		}}
		if !modelMessagesEqual(request.Messages, wantMessages) {
			t.Fatalf("minimal model request messages = %+v", request.Messages)
		}
		if plan, found := tryLoadContextPlan(t, store, result.RunID); found {
			t.Fatalf("minimal Pure Chat unexpectedly froze context PortPlan: %+v", plan)
		}
	})
}

func prepareRoleSkillSeed(t *testing.T, includeModules bool) *bootstrapseed.Prepared {
	t.Helper()
	seedPath := productionSeedPath(t)
	seedBytes, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	var seed map[string]any
	decoder := json.NewDecoder(bytes.NewReader(seedBytes))
	decoder.UseNumber()
	if err := decoder.Decode(&seed); err != nil {
		t.Fatal(err)
	}
	seed["seed_id"] = "freeagent.test.role-skill-pure-chat"
	if includeModules {
		seed["declarative_context_providers"] = []any{
			declarativeProviderSeed(t, filepath.Dir(seedPath), roleModuleID, "role-architect", "install-role-architect"),
			declarativeProviderSeed(t, filepath.Dir(seedPath), skillModuleID, "skill-implementation", "install-skill-implementation"),
		}
	} else {
		delete(seed, "declarative_context_providers")
	}
	encoded, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := bootstrapseed.Prepare(canonical, filepath.Dir(seedPath))
	if err != nil {
		t.Fatalf("Prepare(role/skill seed) error = %v", err)
	}
	return prepared
}

func declarativeProviderSeed(
	t *testing.T,
	artifactBase string,
	moduleID string,
	instanceID string,
	installationID string,
) map[string]any {
	t.Helper()
	relativePath := filepath.ToSlash(filepath.Join(
		"bootstrap-artifacts", moduleID, "1.0.0",
	))
	digest, size := roleSkillArtifactLock(t, filepath.Join(
		artifactBase, filepath.FromSlash(relativePath),
	))
	return map[string]any{
		"config": map[string]any{
			"allow_drop":     false,
			"allow_summary":  false,
			"parameters":     map[string]any{},
			"placement":      string(moduleapi.ContextPlacementTrustedInstruction),
			"schema_version": moduleapi.ContextBindingConfigSchemaV1,
		},
		"failure_policy": string(moduleapi.FailureRequired),
		"module": map[string]any{
			"activation_revision":       json.Number("1"),
			"artifact_digest":           digest,
			"artifact_relative_path":    relativePath,
			"artifact_size_bytes":       json.Number(fmt.Sprintf("%d", size)),
			"exact_version":             "1.0.0",
			"expected_adapter_identity": "freeagent.adapter.declarative/v1",
			"expected_execution_class":  string(moduleapi.ExecutionDeclarative),
			"installation_id":           installationID,
			"instance_id":               instanceID,
			"module_id":                 moduleID,
		},
		"port": map[string]any{
			"exact_version": moduleapi.PortVersionV1,
			"name":          moduleapi.PortNameContextProvide,
		},
	}
}

func roleSkillArtifactLock(t *testing.T, root string) (string, uint64) {
	t.Helper()
	manifestBytes, err := os.ReadFile(filepath.Join(root, moduleapi.ArtifactManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	_, canonical, err := moduleapi.ParseModuleManifestV1(manifestBytes)
	if err != nil || !bytes.Equal(canonical, manifestBytes) {
		t.Fatalf("artifact %s manifest is not exact canonical JSON: %v", root, err)
	}
	files, err := moduleapi.ScanArtifactDirectory(root, moduleapi.ArtifactMetadataPaths{})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(canonical, files)
	if err != nil {
		t.Fatal(err)
	}
	size := uint64(len(canonical))
	for _, file := range files {
		size += uint64(len(file.Content))
	}
	return digest, size
}

func newRoleSkillChatService(
	t *testing.T,
	prepared *bootstrapseed.Prepared,
) (*ChatService, *currentstore.Store, *recordingChatInvoker) {
	t.Helper()
	assertion := prepared.ModelAssertion()
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           assertion.ModuleID,
		Version:            assertion.ExactVersion,
		ArtifactDigest:     assertion.ArtifactDigest,
		InstanceID:         assertion.InstanceID,
		ExecutionClass:     assertion.ExpectedExecutionClass,
		AdapterIdentity:    assertion.ExpectedAdapterIdentity,
		ActivationRevision: assertion.ActivationRevision,
	}
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &recordingChatInvoker{delegate: echo}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         recorder,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := activationresolver.New(activationresolver.Config{
		DeclarativeAdapterIdentity: "freeagent.adapter.declarative/v1",
		TrustedInProcessAllowlist: []activationresolver.TrustedInProcessAllowlistEntry{{
			ModuleID:        assertion.ModuleID,
			ExactVersion:    assertion.ExactVersion,
			ArtifactDigest:  assertion.ArtifactDigest,
			AdapterIdentity: assertion.ExpectedAdapterIdentity,
		}},
	}, registry)
	if err != nil {
		t.Fatal(err)
	}
	store := newProductionSeedStore(t)
	if _, err := prepared.Import(context.Background(), store, resolver); err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewChatService(store, loop)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, recorder
}

func recordedRequest(t *testing.T, recorder *recordingChatInvoker) moduleapi.ModelGenerateRequestV1 {
	t.Helper()
	request, err := moduleapi.RestoreModelGenerateRequestV1(recorder.onlyInput(t))
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func modelMessagesEqual(left, right []moduleapi.ModelMessageV1) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func loadContextPlan(
	t *testing.T,
	store *currentstore.Store,
	runID string,
) moduleapi.PortPlan {
	t.Helper()
	plan, found := tryLoadContextPlan(t, store, runID)
	if !found {
		t.Fatal("frozen Run has no context.provide/v1 PortPlan")
	}
	return plan
}

func tryLoadContextPlan(
	t *testing.T,
	store *currentstore.Store,
	runID string,
) (moduleapi.PortPlan, bool) {
	t.Helper()
	lease, err := store.AcquireCurrentRunLease(
		context.Background(),
		currentstore.AcquireCurrentRunLeaseInput{
			RunID: runID, OwnerID: "role-skill-e2e-inspector", TTL: time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.ReleaseRunLease(context.Background(), lease); err != nil {
			t.Errorf("ReleaseRunLease() error = %v", err)
		}
	}()
	run, err := store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range run.Member.PortPlans {
		if plan.Port == (moduleapi.PortRef{
			Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1,
		}) {
			return plan, true
		}
	}
	return moduleapi.PortPlan{}, false
}
