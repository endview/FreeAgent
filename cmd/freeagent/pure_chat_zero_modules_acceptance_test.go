package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/loopbackchannel"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/internal/wasmaction"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	w1PureChatSkillCanaryV1  = "UNBOUND_SKILL_CANARY_MUST_NOT_REACH_PURE_CHAT"
	w1PureChatBasicContextV1 = "You are FreeAgent, a helpful assistant."
)

// TestW1PureChatUnifiedZeroOptionalModulesAcceptanceV1 is the authoritative
// W1 zero-module acceptance. The fixture deliberately makes optional module
// state available but leaves it outside the selected pure-chat Profile:
//
//   - Knowledge remains installed, active, and Catalog-visible after its
//     Binding is removed.
//   - Memory has a non-empty Agent head and a Catalog-visible provider, but no
//     selected Binding.
//   - Skill, MCP/Action, REMOTE Action, WASM Action, and Channel providers
//     are installed, active, and Catalog-visible, but neither the Profile
//     nor Workspace selects them.
//
// The request still traverses the production composition, Universal Loop,
// Action-capable ChatService, Module Host, and Current Store. A registry probe
// proves that only the frozen model adapter is resolved. Store deltas and the
// frozen Run then prove that no optional module call or persistence effect was
// hidden behind a successful reply.
func TestW1PureChatUnifiedZeroOptionalModulesAcceptanceV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initialized, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     ragExampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	})
	if err != nil {
		t.Fatalf("initialize production RAG fixture: %v", err)
	}
	if initialized.Defaults.ProfileID != defaultProfileID ||
		initialized.Defaults.WorkspaceID != defaultWorkspaceID ||
		initialized.Defaults.AgentID != defaultAgentID {
		t.Fatalf("RAG fixture changed default pure-chat assembly: %+v", initialized.Defaults)
	}

	// Keep a real Knowledge provider available while removing its only
	// consumer-owned Binding.
	publishControlWithoutKnowledgeBinding(t, databasePath, "knowledge-shared")

	// A non-empty Memory head is stronger than an empty Store: Pure Chat must
	// ignore it because Memory was not selected by the Profile.
	memoryBefore := seedNonEmptyModuleApplyMemoryGenesisV1(t, databasePath)

	// Make the remaining executable providers current without binding them.
	// Their artifacts are intentionally absent: an accidental resolution is
	// both counted by the registry probe and fails the production loader.
	unboundProviders := installW1PureChatUnboundProvidersV1(t, databasePath)
	skillCanary := putW1PureChatUnboundSkillCanaryV1(t, databasePath)

	before := readW1PureChatOptionalStateV1(t, databasePath)
	remoteResolver := &w2r1UnavailableMaterialResolverV1{
		databasePath: databasePath,
	}
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{
			RemoteAction: &productionRemoteActionRuntimeConfig{
				SecretResolver: remoteResolver,
			},
		},
	)
	if err != nil {
		t.Fatalf("open production composition: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			if err := composition.Close(); err != nil {
				t.Errorf("close production composition: %v", err)
			}
		}
	})

	_, control, catalog, err := composition.store.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("load current production basis: %v", err)
	}
	assertW1PureChatOptionalProvidersUnboundV1(
		t,
		control,
		catalog,
		unboundProviders,
	)

	registryProbe := newW1PureChatRegistryProbeV1(composition.registry)
	probedLoop, err := coreloop.NewUniversalLoop(composition.store, registryProbe)
	if err != nil {
		t.Fatalf("construct production Universal Loop with registry probe: %v", err)
	}
	probedChat, err := newProductionChatService(
		composition.store,
		probedLoop,
		registryProbe,
	)
	if err != nil {
		t.Fatalf("construct production Action-capable ChatService: %v", err)
	}

	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: defaultPrincipalID,
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   defaultProfileID,
		Message:     "ordinary Pure Chat must not discover optional modules",
		RequestID:   "w1-pure-chat-zero-optional-modules-v1",
		Deadline: time.Date(
			2099, time.January, 2, 3, 4, 5, 0, time.UTC,
		),
	}
	result, err := probedChat.Chat(ctx, input)
	if err != nil {
		t.Fatalf("production Pure Chat: %v", err)
	}
	if !result.AdmissionCreated || result.TerminalResult == nil ||
		result.LoopResult.Disposition != loopapi.DispositionTerminated ||
		result.TerminalResult.State != corecontract.ModelAttemptSucceeded ||
		result.Reply != input.Message || result.FailureCode != "" {
		t.Fatalf("production Pure Chat result=%+v", result)
	}

	dispatch, err := composition.store.GetModelDispatchRecord(
		ctx,
		result.TerminalResult.AttemptID,
	)
	if err != nil {
		t.Fatalf("load Pure Chat model dispatch: %v", err)
	}
	if dispatch.Attempt.ContextCompilation != nil {
		t.Fatalf(
			"Pure Chat persisted optional dynamic context evidence: %+v",
			dispatch.Attempt.ContextCompilation,
		)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		dispatch.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("restore Pure Chat model request: %v", err)
	}
	wantMessages := []moduleapi.ModelMessageV1{
		{Role: moduleapi.ModelRoleSystem, Content: w1PureChatBasicContextV1},
		{Role: moduleapi.ModelRoleUser, Content: input.Message},
	}
	if !reflect.DeepEqual(request.Messages, wantMessages) || len(request.Actions) != 0 {
		t.Fatalf(
			"Pure Chat request exposed optional context or Actions: messages=%+v actions=%+v",
			request.Messages,
			request.Actions,
		)
	}
	if bytes.Contains(
		dispatch.Attempt.Request.CanonicalBytes,
		[]byte(w1PureChatSkillCanaryV1),
	) {
		t.Fatalf("unbound Skill %s entered the model request", skillCanary.Digest)
	}

	modelProvider := dispatch.Attempt.Binding.Provider
	registryCalls := registryProbe.snapshot()
	wantModelKey := w1PureChatRegistryKeyV1(
		modelProvider.ArtifactDigest,
		modelProvider.AdapterIdentity,
	)
	if len(registryCalls) != 1 || registryCalls[wantModelKey] != 1 {
		t.Fatalf(
			"Pure Chat resolved adapters other than its one frozen model: %+v",
			registryCalls,
		)
	}
	for _, provider := range unboundProviders {
		if calls := registryCalls[w1PureChatRegistryKeyV1(
			provider.ArtifactDigest,
			provider.AdapterIdentity,
		)]; calls != 0 {
			t.Fatalf("unbound provider %s resolved %d times", provider.InstanceID, calls)
		}
	}
	if remoteResolver.snapshot().calls != 0 {
		t.Fatal("Pure Chat resolved unbound REMOTE runtime material")
	}

	if err := composition.Close(); err != nil {
		t.Fatalf("close production composition: %v", err)
	}
	closed = true
	after := readW1PureChatOptionalStateV1(t, databasePath)
	if !reflect.DeepEqual(before.OptionalTables, after.OptionalTables) ||
		!reflect.DeepEqual(before.OptionalContentKinds, after.OptionalContentKinds) ||
		before.PointerRevision != after.PointerRevision {
		t.Fatalf(
			"Pure Chat changed optional durable state:\nbefore=%+v\nafter=%+v",
			before,
			after,
		)
	}
	if after.Runs != before.Runs+1 ||
		after.MemberSnapshots != before.MemberSnapshots+1 ||
		after.RunManifests != before.RunManifests+1 ||
		after.ModelAttempts != before.ModelAttempts+1 {
		t.Fatalf(
			"Pure Chat did not persist exactly one ordinary model Run: before=%+v after=%+v",
			before,
			after,
		)
	}

	memoryAfter := loadModuleApplyMemoryHeadV1(t, databasePath)
	if memoryAfter.SnapshotRef != memoryBefore.SnapshotRef ||
		memoryAfter.SourceAttemptID != memoryBefore.SourceAttemptID ||
		!bytes.Equal(memoryAfter.CanonicalBytes, memoryBefore.CanonicalBytes) {
		t.Fatalf(
			"Pure Chat changed unbound Memory: before=%+v after=%+v",
			memoryBefore,
			memoryAfter,
		)
	}
	assertW1PureChatOrdinaryRunV1(t, databasePath, result.RunID)
}

type w1PureChatUnboundModuleV1 struct {
	ModuleID        string
	InstanceID      string
	ExecutionClass  moduleapi.ExecutionClass
	AdapterIdentity string
	Port            moduleapi.PortRef
	RuntimeMode     moduleapi.RuntimeModeRequest
	RuntimeProtocol string
	Entrypoint      string
}

func installW1PureChatUnboundProvidersV1(
	t *testing.T,
	databasePath string,
) []moduleapi.ActivatedModuleRef {
	t.Helper()
	definitions := []w1PureChatUnboundModuleV1{
		{
			ModuleID:        "freeagent.test.w1.memory.unbound",
			InstanceID:      "w1-memory-unbound",
			ExecutionClass:  moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity: localMemoryAdapterID,
			Port:            productionContextPort,
			RuntimeMode:     moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol: moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint:      "freeagent.test.w1.memory.unbound/v1",
		},
		{
			ModuleID:        "freeagent.test.w1.skill.unbound",
			InstanceID:      "w1-skill-unbound",
			ExecutionClass:  moduleapi.ExecutionDeclarative,
			AdapterIdentity: declarativeAdapterID,
			Port:            productionContextPort,
			RuntimeMode:     moduleapi.RuntimeModeRequestDeclarative,
			RuntimeProtocol: moduleapi.RuntimeProtocolStaticV1,
			Entrypoint:      "content/skill.json",
		},
		{
			ModuleID:        "freeagent.test.w1.mcp-action.unbound",
			InstanceID:      "w1-mcp-action-unbound",
			ExecutionClass:  moduleapi.ExecutionLocalProcess,
			AdapterIdentity: mcpstdio.AdapterIdentityV1,
			Port:            productionActionPort,
			RuntimeMode:     moduleapi.RuntimeModeRequestLocalProcess,
			RuntimeProtocol: moduleapi.RuntimeProtocolMCPStdio20251125,
			Entrypoint:      "content/mcp-server",
		},
		{
			ModuleID:        "freeagent.test.w1.channel.unbound",
			InstanceID:      "w1-channel-unbound",
			ExecutionClass:  moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity: loopbackchannel.AdapterIdentityV1,
			Port: moduleapi.PortRef{
				Name:         moduleapi.PortNameChannelTransport,
				ExactVersion: moduleapi.PortVersionV1,
			},
			RuntimeMode:     moduleapi.RuntimeModeRequestTrustedInProcess,
			RuntimeProtocol: moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint:      "freeagent.test.w1.channel.unbound/v1",
		},
		{
			ModuleID:        "freeagent.test.w2r1.remote-action.unbound",
			InstanceID:      "w2r1-remote-action-unbound",
			ExecutionClass:  moduleapi.ExecutionRemote,
			AdapterIdentity: remoteactionhttp.AdapterIdentityV1,
			Port:            productionActionPort,
			RuntimeMode:     moduleapi.RuntimeModeRequestRemote,
			RuntimeProtocol: moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
			Entrypoint:      "content/actions.json",
		},
		{
			ModuleID:        "freeagent.test.w2r2.wasm-action.unbound",
			InstanceID:      "w2r2-wasm-action-unbound",
			ExecutionClass:  moduleapi.ExecutionWASM,
			AdapterIdentity: wasmaction.AdapterIdentityV1,
			Port:            productionActionPort,
			RuntimeMode:     moduleapi.RuntimeModeRequestWASM,
			RuntimeProtocol: moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
			Entrypoint:      "content/actions.json",
		},
	}

	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Store for unbound providers: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close Store for unbound providers: %v", err)
		}
	}()
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatalf("load basis for unbound providers: %v", err)
	}
	providers := make([]moduleapi.ActivatedModuleRef, 0, len(definitions)+1)
	if knowledge, found := catalog.FindInstance("knowledge-shared"); found {
		providers = append(providers, knowledge.Activation)
	} else {
		t.Fatal("unbound production Knowledge provider is absent")
	}
	for index, definition := range definitions {
		manifest, err := w1PureChatUnboundManifestV1(definition)
		if err != nil {
			t.Fatalf("freeze unbound manifest %s: %v", definition.InstanceID, err)
		}
		manifestRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentModuleManifest,
			"application/json",
			manifest,
		)
		if err != nil {
			t.Fatalf("compute unbound manifest ref %s: %v", definition.InstanceID, err)
		}
		artifactDigest := moduleapi.Digest(
			"freeagent.test.w1-unbound-artifact/v1",
			manifest,
		)
		installationID := fmt.Sprintf("w1-unbound-installation-%d", index)
		if _, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
			InstallationID:      installationID,
			ModuleID:            definition.ModuleID,
			ExactVersion:        "1.0.0",
			ExpectedManifestRef: manifestRef,
			ManifestBytes:       manifest,
			ArtifactDigest:      artifactDigest,
		}); err != nil {
			t.Fatalf("install unbound provider %s: %v", definition.InstanceID, err)
		}
		if _, err := store.ActivateModule(ctx, currentstore.ActivateModuleInput{
			ActivationID:       fmt.Sprintf("w1-unbound-activation-%d", index),
			TenantID:           defaultTenantID,
			InstanceID:         definition.InstanceID,
			InstallationID:     installationID,
			ActivationRevision: 1,
			ExecutionClass:     definition.ExecutionClass,
			AdapterIdentity:    definition.AdapterIdentity,
		}); err != nil {
			t.Fatalf("activate unbound provider %s: %v", definition.InstanceID, err)
		}
		provider := moduleapi.ActivatedModuleRef{
			ModuleID:           definition.ModuleID,
			Version:            "1.0.0",
			ArtifactDigest:     artifactDigest,
			InstanceID:         definition.InstanceID,
			ExecutionClass:     definition.ExecutionClass,
			AdapterIdentity:    definition.AdapterIdentity,
			ActivationRevision: 1,
		}
		providers = append(providers, provider)
		catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{
			Activation: provider,
			Provides:   []moduleapi.PortRef{definition.Port},
		})
	}

	control.SnapshotID = "control-w1-pure-chat-unbound-optionals"
	control.Revision++
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze unbound-provider Control: %v", err)
	}
	catalog.GenerationID = "catalog-w1-pure-chat-unbound-optionals"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze unbound-provider Catalog: %v", err)
	}
	if _, err := store.PublishControlCatalog(ctx, currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: basis.PointerRevision,
		NewPointerRevision:      basis.PointerRevision + 1,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	}); err != nil {
		t.Fatalf("publish unbound providers: %v", err)
	}
	return providers
}

func w1PureChatUnboundManifestV1(
	definition w1PureChatUnboundModuleV1,
) ([]byte, error) {
	wire := map[string]any{
		"api_version": moduleapi.ModuleManifestAPIVersionV1,
		"id":          definition.ModuleID,
		"version":     "1.0.0",
		"runtime": map[string]any{
			"mode":       string(definition.RuntimeMode),
			"protocol":   definition.RuntimeProtocol,
			"entrypoint": definition.Entrypoint,
		},
		"provides": []any{map[string]any{
			"name":          definition.Port.Name,
			"exact_version": definition.Port.ExactVersion,
		}},
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return nil, err
	}
	_, restored, err := moduleapi.ParseModuleManifestV1(canonical)
	return restored, err
}

func putW1PureChatUnboundSkillCanaryV1(
	t *testing.T,
	databasePath string,
) currentstore.ContentRecord {
	t.Helper()
	_, canonical, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text:          w1PureChatSkillCanaryV1,
		},
	)
	if err != nil {
		t.Fatalf("freeze unbound Skill canary: %v", err)
	}
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentStaticContext,
		"application/json",
		canonical,
	)
	if err != nil {
		t.Fatalf("digest unbound Skill canary: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open Store for unbound Skill canary: %v", err)
	}
	record, putErr := store.PutContent(context.Background(), currentstore.ContentInput{
		Digest:         digest,
		Kind:           currentstore.ContentStaticContext,
		MediaType:      "application/json",
		CanonicalBytes: canonical,
	})
	closeErr := store.Close()
	if putErr != nil || closeErr != nil {
		t.Fatalf("put unbound Skill canary: %v", errors.Join(putErr, closeErr))
	}
	return record
}

func assertW1PureChatOptionalProvidersUnboundV1(
	t *testing.T,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	providers []moduleapi.ActivatedModuleRef,
) {
	t.Helper()
	profile, found := control.FindProfile(defaultProfileID)
	if !found {
		t.Fatalf("default Profile %q is absent", defaultProfileID)
	}
	workspace, found := control.FindWorkspace(defaultWorkspaceID)
	if !found {
		t.Fatalf("default Workspace %q is absent", defaultWorkspaceID)
	}
	if len(workspace.ChannelEndpoints) != 0 || len(workspace.TransferGrants) != 0 ||
		len(control.CompositeAgents) != 0 {
		t.Fatalf(
			"default Pure Chat selected Channel/Transfer/Composite state: workspace=%+v composite=%+v",
			workspace,
			control.CompositeAgents,
		)
	}
	unbound := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		entry, found := catalog.FindInstance(provider.InstanceID)
		if !found || entry.Activation != provider {
			t.Fatalf("unbound provider %s is not current in Catalog", provider.InstanceID)
		}
		unbound[provider.InstanceID] = struct{}{}
	}
	contextInstances := make([]string, 0)
	modelInstances := make([]string, 0)
	for _, binding := range profile.Bindings {
		if _, forbidden := unbound[binding.InstanceID]; forbidden {
			t.Fatalf("optional provider %s is bound to default Pure Chat", binding.InstanceID)
		}
		switch binding.Port.Name {
		case moduleapi.PortNameContextProvide:
			contextInstances = append(contextInstances, binding.InstanceID)
		case moduleapi.PortNameModelGenerate:
			modelInstances = append(modelInstances, binding.InstanceID)
		case moduleapi.PortNameActionProvider, moduleapi.PortNameChannelTransport:
			t.Fatalf("default Pure Chat selected optional Port Binding: %+v", binding)
		}
	}
	if !reflect.DeepEqual(contextInstances, []string{"context-basic"}) ||
		!reflect.DeepEqual(modelInstances, []string{"model-dev-echo"}) {
		t.Fatalf(
			"default Pure Chat Binding set changed: model=%v context=%v",
			modelInstances,
			contextInstances,
		)
	}
}

type w1PureChatRegistryProbeV1 struct {
	delegate modulehost.ExactAdapterRegistry

	mu    sync.Mutex
	calls map[string]int
}

func newW1PureChatRegistryProbeV1(
	delegate modulehost.ExactAdapterRegistry,
) *w1PureChatRegistryProbeV1 {
	return &w1PureChatRegistryProbeV1{
		delegate: delegate,
		calls:    make(map[string]int),
	}
}

func (probe *w1PureChatRegistryProbeV1) ResolveExact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
) (modulehost.ModuleInvoker, error) {
	key := w1PureChatRegistryKeyV1(artifactDigest, adapterIdentity)
	probe.mu.Lock()
	probe.calls[key]++
	probe.mu.Unlock()
	return probe.delegate.ResolveExact(ctx, artifactDigest, adapterIdentity)
}

func (probe *w1PureChatRegistryProbeV1) snapshot() map[string]int {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	result := make(map[string]int, len(probe.calls))
	for key, value := range probe.calls {
		result[key] = value
	}
	return result
}

func w1PureChatRegistryKeyV1(artifactDigest string, adapterIdentity string) string {
	return artifactDigest + "\x00" + adapterIdentity
}

type w1PureChatDurableStateV1 struct {
	PointerRevision      int64
	OptionalTables       map[string]int64
	OptionalContentKinds map[string]int64
	Runs                 int64
	MemberSnapshots      int64
	RunManifests         int64
	ModelAttempts        int64
}

func readW1PureChatOptionalStateV1(
	t *testing.T,
	databasePath string,
) w1PureChatDurableStateV1 {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open SQLite observation handle: %v", err)
	}
	defer database.Close()
	state := w1PureChatDurableStateV1{
		OptionalTables:       make(map[string]int64),
		OptionalContentKinds: make(map[string]int64),
	}
	if err := database.QueryRow(`
		SELECT pointer_revision FROM control_current WHERE tenant_id=?
	`, defaultTenantID).Scan(&state.PointerRevision); err != nil {
		t.Fatalf("read current pointer revision: %v", err)
	}
	for _, table := range []string{
		"module_installations",
		"module_activations",
		"control_snapshots",
		"runtime_catalog_generations",
		"agent_memory_revisions",
		"dispatch_attempts",
		"channel_ingress_receipts",
		"workspace_scheduler_state",
		"learning_proposals",
		"learning_cycle_schedules",
		"learning_cycle_tasks",
		"module_publisher_keys",
		"module_sources",
		"module_discovery_snapshots",
		"module_discovery_module_refs",
		"module_discovery_entries",
		"module_upgrade_candidates",
		"module_upgrade_reviews",
		"module_candidate_decisions",
	} {
		var count int64
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count optional table %s: %v", table, err)
		}
		state.OptionalTables[table] = count
	}
	kinds := []currentstore.ContentKind{
		currentstore.ContentContextCompilation,
		currentstore.ContentActionProposal,
		currentstore.ContentActionResult,
		currentstore.ContentMemorySnapshot,
		currentstore.ContentStaticContext,
		currentstore.ContentRunCancellation,
		currentstore.ContentChannelCursor,
		currentstore.ContentChannelIngressEnvelope,
		currentstore.ContentChannelSendProposal,
		currentstore.ContentChannelSendResult,
		currentstore.ContentWorkspaceTransferPayload,
		currentstore.ContentWorkspaceTransferEnvelope,
	}
	for _, kind := range kinds {
		var count int64
		if err := database.QueryRow(
			"SELECT COUNT(*) FROM content_records WHERE kind=?",
			string(kind),
		).Scan(&count); err != nil {
			t.Fatalf("count optional content kind %s: %v", kind, err)
		}
		state.OptionalContentKinds[string(kind)] = count
	}
	for target, destination := range map[string]*int64{
		"runs":                       &state.Runs,
		"member_execution_snapshots": &state.MemberSnapshots,
		"run_manifests":              &state.RunManifests,
		"model_dispatch_attempts":    &state.ModelAttempts,
	} {
		if err := database.QueryRow("SELECT COUNT(*) FROM " + target).Scan(destination); err != nil {
			t.Fatalf("count required Pure Chat table %s: %v", target, err)
		}
	}
	return state
}

func assertW1PureChatOrdinaryRunV1(
	t *testing.T,
	databasePath string,
	runID string,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var memberCanonical, manifestCanonical []byte
	if err := database.QueryRow(`
		SELECT canonical_json FROM member_execution_snapshots WHERE run_id=?
	`, runID).Scan(&memberCanonical); err != nil {
		t.Fatalf("read Pure Chat Member: %v", err)
	}
	if err := database.QueryRow(`
		SELECT canonical_json FROM run_manifests WHERE run_id=?
	`, runID).Scan(&manifestCanonical); err != nil {
		t.Fatalf("read Pure Chat Manifest: %v", err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil {
		t.Fatalf("restore Pure Chat Member: %v", err)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil {
		t.Fatalf("restore Pure Chat Manifest: %v", err)
	}
	if manifest.Composite != nil || manifest.ParentRunID != "" ||
		len(member.Actions) != 0 {
		t.Fatalf("Pure Chat froze Composite or Action state: manifest=%+v member=%+v", manifest, member)
	}
	portNames := make([]string, 0, len(member.PortPlans))
	for _, plan := range member.PortPlans {
		portNames = append(portNames, plan.Port.Name)
		if plan.Port.Name == moduleapi.PortNameActionProvider ||
			plan.Port.Name == moduleapi.PortNameChannelTransport {
			t.Fatalf("Pure Chat froze optional PortPlan: %+v", plan)
		}
		if plan.Port.Name == moduleapi.PortNameContextProvide {
			if len(plan.Bindings) != 1 || plan.Bindings[0].Provider.InstanceID != "context-basic" {
				t.Fatalf("Pure Chat Context plan contains an optional module: %+v", plan)
			}
		}
	}
	sort.Strings(portNames)
	if !reflect.DeepEqual(portNames, []string{
		moduleapi.PortNameContextProvide,
		moduleapi.PortNameModelGenerate,
	}) {
		t.Fatalf("Pure Chat PortPlans=%v", portNames)
	}
	var childRuns, actionOrChannelDispatches int64
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM runs WHERE parent_run_id=?
	`, runID).Scan(&childRuns); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?
	`, runID).Scan(&actionOrChannelDispatches); err != nil {
		t.Fatal(err)
	}
	if childRuns != 0 || actionOrChannelDispatches != 0 {
		t.Fatalf(
			"Pure Chat produced Specialist/Reviewer/Action/Channel work: children=%d dispatches=%d",
			childRuns,
			actionOrChannelDispatches,
		)
	}
}
