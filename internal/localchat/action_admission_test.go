package localchat

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestActionChatServiceLoadsSelectedBindingMaterialsInOrder(t *testing.T) {
	fixture := newChatServiceFixture(t)
	published := publishSelectedActionProfile(t, fixture)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	input := fixture.input("action-admission", "use the optional action profile", deadline)

	if _, err := fixture.service.Chat(context.Background(), input); !errors.Is(
		err,
		assemblycompiler.ErrCapabilityNotAvailable,
	) {
		t.Fatalf("legacy constructor did not fail closed on Action profile: %v", err)
	}

	materializer := &recordingLocalActionMaterializer{
		definitions: []corecontract.FrozenActionDefinitionV1{
			localFrozenAction(t, "z.action", "provider.first", 0),
			localFrozenAction(t, "a.action", "provider.second", 1),
		},
	}
	waiting := &chatWaitingLoop{}
	service, err := NewActionChatService(fixture.store, waiting, materializer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Chat(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AdmissionCreated ||
		result.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation {
		t.Fatalf("result=%+v", result)
	}
	if materializer.calls != 1 || len(materializer.inputs) != 1 {
		t.Fatalf("materializer calls=%d inputs=%d", materializer.calls, len(materializer.inputs))
	}
	materialized := materializer.inputs[0]
	if materialized.TenantID != fixture.tenantID ||
		materialized.WorkspaceID != fixture.workspaceID ||
		materialized.Plan.Port.Name != moduleapi.PortNameActionProvider ||
		len(materialized.Plan.Bindings) != 2 ||
		materialized.Plan.Bindings[0].Provider.InstanceID != "instance-chat-action-first" ||
		materialized.Plan.Bindings[1].Provider.InstanceID != "instance-chat-action-second" {
		t.Fatalf("materializer received wrong frozen Action plan: %+v", materialized)
	}
	if len(materialized.Bindings) != 2 ||
		!bytes.Equal(materialized.Bindings[0].ConfigCanonical, published.configs[0]) ||
		!bytes.Equal(materialized.Bindings[0].AuthorityCanonical, published.authorities[0]) ||
		!bytes.Equal(materialized.Bindings[1].ConfigCanonical, published.configs[1]) ||
		!bytes.Equal(materialized.Bindings[1].AuthorityCanonical, published.authorities[1]) {
		t.Fatalf("Action Config/Authority material order changed: %+v", materialized.Bindings)
	}

	retry, err := service.Chat(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if retry.AdmissionCreated || retry.RunID != result.RunID {
		t.Fatalf("retry=%+v first=%+v", retry, result)
	}
	if materializer.calls != 1 {
		t.Fatalf("admission reentry called Describe again: %d", materializer.calls)
	}
}

func TestActionCapableChatServiceKeepsPureChatActionFree(t *testing.T) {
	fixture := newChatServiceFixture(t)
	materializer := &recordingLocalActionMaterializer{}
	service, err := NewActionChatService(
		fixture.store,
		fixture.service.loop,
		materializer,
	)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	result, err := service.Chat(
		context.Background(),
		fixture.input("pure-with-action-gate", "remain pure", deadline),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AdmissionCreated || materializer.calls != 0 {
		t.Fatalf("result=%+v Action calls=%d", result, materializer.calls)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		fixture.invoker.onlyInput(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Actions) != 0 {
		t.Fatalf("Pure Chat unexpectedly exposed Actions: %+v", request.Actions)
	}

	var typedNil *recordingLocalActionMaterializer
	if _, err := NewActionChatService(
		fixture.store,
		fixture.service.loop,
		typedNil,
	); !errors.Is(err, ErrInvalidChat) {
		t.Fatalf("typed nil Action materializer error=%v", err)
	}
}

func TestPureChatHasZeroActionAccessWithAvailableUnselectedProviders(
	t *testing.T,
) {
	deadline := time.Date(2099, time.January, 2, 3, 4, 5, 0, time.UTC)
	message := "Pure Chat canonical request must ignore available Actions."

	baseline := newChatServiceFixture(t)
	baselineResult, err := baseline.service.Chat(
		context.Background(),
		baseline.input("pure-zero-baseline", message, deadline),
	)
	if err != nil || baselineResult.TerminalResult == nil {
		t.Fatalf("baseline Pure Chat=%+v error=%v", baselineResult, err)
	}
	baselineCanonical := baseline.invoker.onlyInput(t)

	fixture := newChatServiceFixture(t)
	actionProviders := publishAvailableUnselectedActionProfile(t, fixture)
	if len(actionProviders) != 2 {
		t.Fatalf("available Action providers=%d want 2", len(actionProviders))
	}
	_, _, catalog, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.tenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	var modelProvider moduleapi.ActivatedModuleRef
	for _, entry := range catalog.Entries {
		for _, port := range entry.Provides {
			if port == chatModelGeneratePortV1 {
				modelProvider = entry.Activation
			}
		}
	}
	if modelProvider.InstanceID == "" {
		t.Fatal("published Catalog lost model.generate/v1")
	}

	bomb := &zeroAccessActionAdapter{}
	loaderCalls := 0
	registry, err := exactadapter.NewRegistryWithLoader(
		func(
			_ context.Context,
			artifactDigest string,
			adapterIdentity string,
		) (modulehost.ModuleInvoker, error) {
			loaderCalls++
			for _, provider := range actionProviders {
				if provider.ArtifactDigest == artifactDigest &&
					provider.AdapterIdentity == adapterIdentity {
					return bomb, nil
				}
			}
			return nil, fmt.Errorf(
				"unexpected lazy adapter %s/%s",
				artifactDigest,
				adapterIdentity,
			)
		},
		exactadapter.Registration{
			ArtifactDigest:  modelProvider.ArtifactDigest,
			AdapterIdentity: modelProvider.AdapterIdentity,
			Invoker:         fixture.invoker,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	loop, err := coreloop.NewUniversalLoop(fixture.store, registry)
	if err != nil {
		t.Fatal(err)
	}
	realMaterializer, err := actionmaterializer.New(registry)
	if err != nil {
		t.Fatal(err)
	}
	materializer := &countingActionMaterializer{delegate: realMaterializer}
	service, err := NewActionChatService(fixture.store, loop, materializer)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Chat(
		context.Background(),
		fixture.input("pure-zero-with-action-catalog", message, deadline),
	)
	if err != nil || result.TerminalResult == nil ||
		result.LoopResult.Disposition != loopapi.DispositionTerminated {
		t.Fatalf("Pure Chat with Action Catalog=%+v error=%v", result, err)
	}
	requestCanonical := fixture.invoker.onlyInput(t)
	if !bytes.Equal(requestCanonical, baselineCanonical) {
		t.Fatalf(
			"unselected Action changed Pure Chat request:\nbaseline=%s\naction-catalog=%s",
			baselineCanonical,
			requestCanonical,
		)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(requestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Actions) != 0 {
		t.Fatalf("Pure Chat exposed Actions: %+v", request.Actions)
	}
	if loaderCalls != 0 || materializer.calls != 0 ||
		bomb.invokeCalls != 0 || bomb.describeCalls != 0 ||
		bomb.prepareCalls != 0 || bomb.executeCalls != 0 {
		t.Fatalf(
			"Pure Chat Action access loader=%d materializer=%d invoke=%d Describe=%d Prepare=%d Execute=%d",
			loaderCalls,
			materializer.calls,
			bomb.invokeCalls,
			bomb.describeCalls,
			bomb.prepareCalls,
			bomb.executeCalls,
		)
	}

	database, err := sql.Open("sqlite", fixture.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var dispatches int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?
	`, result.RunID).Scan(&dispatches); err != nil {
		t.Fatal(err)
	}
	if dispatches != 0 {
		t.Fatalf("Pure Chat Action dispatch rows=%d want 0", dispatches)
	}
	var memberCanonical []byte
	if err := database.QueryRow(`
		SELECT canonical_json
		FROM member_execution_snapshots
		WHERE run_id=?
	`, result.RunID).Scan(&memberCanonical); err != nil {
		t.Fatal(err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(member.Actions) != 0 {
		t.Fatalf("Pure Chat froze Actions: %+v", member.Actions)
	}
	for _, plan := range member.PortPlans {
		if plan.Port.Name == moduleapi.PortNameActionProvider {
			t.Fatalf("Pure Chat froze Action PortPlan: %+v", plan)
		}
	}
}

func TestActionMaterialLoadingIgnoresUnselectedProfiles(t *testing.T) {
	fixture := newChatServiceFixture(t)
	_, control, _, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.tenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	other := control.Profiles[0]
	other.Profile = corecontract.ProfileRef{
		ID: "profile-unselected-action", Version: "v1", Digest: strings.Repeat("d", 64),
	}
	other.Bindings = []controlcontract.BindingSpec{{
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameActionProvider,
			ExactVersion: moduleapi.PortVersionV1,
		},
		InstanceID:          "unselected-action",
		ConfigRef:           strings.Repeat("e", 64),
		AuthorityCeilingRef: strings.Repeat("f", 64),
		FailurePolicy:       moduleapi.FailureRequired,
	}}
	control.Profiles = append(control.Profiles, other)
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:          fixture.tenantID,
			AdmissionKey:      "selected-profile-only",
			PrincipalID:       "principal-chat",
			WorkspaceID:       fixture.workspaceID,
			AgentID:           fixture.agentID,
			ProfileID:         fixture.profileID,
			TaskInputRef:      strings.Repeat("1", 64),
			RequestedPorts:    []moduleapi.PortRef{chatModelGeneratePortV1},
			Deadline:          time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
			CancellationScope: chatCancellationScope,
			ExplicitLimits:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	materials, err := fixture.service.loadActionBindingMaterials(
		context.Background(),
		control,
		intentCanonical,
		intentDigest,
	)
	if err != nil || len(materials) != 0 {
		t.Fatalf("unselected Action profile affected material loading: materials=%+v err=%v", materials, err)
	}
}

type publishedActionChatMaterials struct {
	configs     [][]byte
	authorities [][]byte
}

func publishSelectedActionProfile(
	t *testing.T,
	fixture *chatServiceFixture,
) publishedActionChatMaterials {
	t.Helper()
	ctx := context.Background()
	providerIDs := []string{"provider.first", "provider.second"}
	publicIDs := []string{"z.action", "a.action"}
	configs := make([][]byte, len(providerIDs))
	authorities := make([][]byte, len(providerIDs))
	configRefs := make([]string, len(providerIDs))
	authorityRefs := make([]string, len(providerIDs))
	for index := range providerIDs {
		_, configCanonical, err := moduleapi.NewActionBindingConfigV1(
			moduleapi.ActionBindingConfigV1{
				SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
				Actions: []moduleapi.ActionBindingMappingV1{{
					PublicActionID:   publicIDs[index],
					ProviderActionID: providerIDs[index],
					LocalEffectClass: moduleapi.EffectNone,
					MaxResultBytes:   1024,
				}},
				Parameters: json.RawMessage(`{"binding_index":` +
					string(rune('1'+index)) + `}`),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, authorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
			moduleapi.ActionAuthorityCeilingV1{
				SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
				TenantID:                 fixture.tenantID,
				AllowedWorkspaceIDs:      []string{fixture.workspaceID},
				AllowedProviderActionIDs: []string{providerIDs[index]},
				MaxEffectClass:           moduleapi.EffectNone,
				MaxResultBytes:           1024,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		configs[index] = bytes.Clone(configCanonical)
		authorities[index] = bytes.Clone(authorityCanonical)
		configRefs[index] = putChatContent(
			t,
			fixture.store,
			currentstore.ContentConfig,
			configCanonical,
		)
		authorityRefs[index] = putChatContent(
			t,
			fixture.store,
			currentstore.ContentAuthorityCeiling,
			authorityCanonical,
		)
	}

	first := installChatActionProvider(
		t,
		fixture.store,
		fixture.tenantID,
		"first",
		"b",
	)
	second := installChatActionProvider(
		t,
		fixture.store,
		fixture.tenantID,
		"second",
		"c",
	)
	basis, control, catalog, err := fixture.store.LoadPublishedBasis(
		ctx,
		fixture.tenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID != fixture.profileID {
			continue
		}
		control.Profiles[index].Bindings = append(
			control.Profiles[index].Bindings,
			controlcontract.BindingSpec{
				Port: moduleapi.PortRef{
					Name:         moduleapi.PortNameActionProvider,
					ExactVersion: moduleapi.PortVersionV1,
				},
				InstanceID:          first.InstanceID,
				ConfigRef:           configRefs[0],
				AuthorityCeilingRef: authorityRefs[0],
				FailurePolicy:       moduleapi.FailureRequired,
			},
			controlcontract.BindingSpec{
				Port: moduleapi.PortRef{
					Name:         moduleapi.PortNameActionProvider,
					ExactVersion: moduleapi.PortVersionV1,
				},
				InstanceID:          second.InstanceID,
				ConfigRef:           configRefs[1],
				AuthorityCeilingRef: authorityRefs[1],
				FailurePolicy:       moduleapi.FailureRequired,
			},
		)
		found = true
		break
	}
	if !found {
		t.Fatal("selected profile not found")
	}
	control.SnapshotID = "control-chat-actions"
	control.Revision++
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-chat-actions"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Entries = append(
		catalog.Entries,
		controlcontract.CatalogEntry{
			Activation: first,
			Provides: []moduleapi.PortRef{{
				Name:         moduleapi.PortNameActionProvider,
				ExactVersion: moduleapi.PortVersionV1,
			}},
		},
		controlcontract.CatalogEntry{
			Activation: second,
			Provides: []moduleapi.PortRef{{
				Name:         moduleapi.PortNameActionProvider,
				ExactVersion: moduleapi.PortVersionV1,
			}},
		},
	)
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatal(err)
	}
	return publishedActionChatMaterials{
		configs:     configs,
		authorities: authorities,
	}
}

func publishAvailableUnselectedActionProfile(
	t *testing.T,
	fixture *chatServiceFixture,
) []moduleapi.ActivatedModuleRef {
	t.Helper()
	publishSelectedActionProfile(t, fixture)
	basis, control, catalog, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.tenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	selectedIndex := -1
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID == fixture.profileID {
			selectedIndex = index
			break
		}
	}
	if selectedIndex < 0 {
		t.Fatal("selected Pure Chat Profile not found")
	}
	actionProfile := control.Profiles[selectedIndex]
	actionProfile.Profile = corecontract.ProfileRef{
		ID:      "profile-z-unselected-action",
		Version: "v1",
		Digest:  strings.Repeat("e", 64),
	}
	pureBindings := make([]controlcontract.BindingSpec, 0, 1)
	for _, binding := range control.Profiles[selectedIndex].Bindings {
		if binding.Port.Name != moduleapi.PortNameActionProvider {
			pureBindings = append(pureBindings, binding)
		}
	}
	control.Profiles[selectedIndex].Bindings = pureBindings
	control.Profiles = append(control.Profiles, actionProfile)
	control.SnapshotID = "control-chat-pure-with-unselected-actions"
	control.Revision++
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-chat-pure-with-unselected-actions"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatal(err)
	}
	providers := make([]moduleapi.ActivatedModuleRef, 0, 2)
	for _, entry := range catalog.Entries {
		for _, port := range entry.Provides {
			if port.Name == moduleapi.PortNameActionProvider &&
				port.ExactVersion == moduleapi.PortVersionV1 {
				providers = append(providers, entry.Activation)
				break
			}
		}
	}
	return providers
}

func installChatActionProvider(
	t *testing.T,
	store *currentstore.Store,
	tenantID string,
	suffix string,
	digestCharacter string,
) moduleapi.ActivatedModuleRef {
	t.Helper()
	manifestBytes, err := json.Marshal(map[string]any{
		"api_version": moduleapi.ModuleManifestAPIVersionV1,
		"id":          "test.chat.action." + suffix,
		"version":     "v1",
		"runtime": map[string]any{
			"mode":       string(moduleapi.RuntimeModeRequestTrustedInProcess),
			"protocol":   moduleapi.RuntimeProtocolGoInProcessV1,
			"entrypoint": "builtin.localchat.action." + suffix,
		},
		"provides": []any{map[string]any{
			"name":          moduleapi.PortNameActionProvider,
			"exact_version": moduleapi.PortVersionV1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err = moduleapi.CanonicalJSON(manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		chatJSONMediaType,
		manifestBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := store.InstallModule(
		context.Background(),
		currentstore.InstallModuleInput{
			InstallationID:      "installation-chat-action-" + suffix,
			ModuleID:            manifest.ID,
			ExactVersion:        manifest.Version,
			ExpectedManifestRef: manifestRef,
			ManifestBytes:       manifestBytes,
			ArtifactDigest:      strings.Repeat(digestCharacter, 64),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := store.ActivateModule(
		context.Background(),
		currentstore.ActivateModuleInput{
			ActivationID:       "activation-chat-action-" + suffix,
			TenantID:           tenantID,
			InstanceID:         "instance-chat-action-" + suffix,
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "builtin.localchat.action." + suffix,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}
}

func localFrozenAction(
	t *testing.T,
	publicID string,
	providerID string,
	bindingIndex uint32,
) corecontract.FrozenActionDefinitionV1 {
	t.Helper()
	schema, err := moduleapi.CanonicalizeActionInputSchemaV1(
		json.RawMessage(`{"additionalProperties":false,"properties":{},"type":"object"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	definition, _, err := corecontract.NewFrozenActionDefinitionV1(
		corecontract.FrozenActionDefinitionV1{
			PublicActionID:   publicID,
			ProviderActionID: providerID,
			BindingIndex:     bindingIndex,
			Description:      "Local Chat Action " + publicID,
			InputSchema:      schema,
			EffectClass:      moduleapi.EffectNone,
			MaxResultBytes:   1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

type recordingLocalActionMaterializer struct {
	calls       int
	inputs      []actionmaterializer.InputV1
	definitions []corecontract.FrozenActionDefinitionV1
}

func (materializer *recordingLocalActionMaterializer) Materialize(
	_ context.Context,
	input actionmaterializer.InputV1,
) ([]corecontract.FrozenActionDefinitionV1, error) {
	materializer.calls++
	cloned := input
	cloned.Plan.Bindings = append(
		[]moduleapi.PortBinding(nil),
		input.Plan.Bindings...,
	)
	cloned.Bindings = make(
		[]actionmaterializer.BindingMaterialV1,
		len(input.Bindings),
	)
	for index, binding := range input.Bindings {
		cloned.Bindings[index] = actionmaterializer.BindingMaterialV1{
			ConfigCanonical:    bytes.Clone(binding.ConfigCanonical),
			AuthorityCanonical: bytes.Clone(binding.AuthorityCanonical),
		}
	}
	materializer.inputs = append(materializer.inputs, cloned)
	return append(
		[]corecontract.FrozenActionDefinitionV1(nil),
		materializer.definitions...,
	), nil
}

type countingActionMaterializer struct {
	delegate assemblycompiler.ActionMaterializerV1
	calls    int
}

func (materializer *countingActionMaterializer) Materialize(
	ctx context.Context,
	input actionmaterializer.InputV1,
) ([]corecontract.FrozenActionDefinitionV1, error) {
	materializer.calls++
	return materializer.delegate.Materialize(ctx, input)
}

type zeroAccessActionAdapter struct {
	invokeCalls   int
	describeCalls int
	prepareCalls  int
	executeCalls  int
}

func (adapter *zeroAccessActionAdapter) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	adapter.invokeCalls++
	return modulehost.InvocationResult{}, errors.New(
		"Pure Chat reached generic Action invocation",
	)
}

func (adapter *zeroAccessActionAdapter) Describe(
	context.Context,
	moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	adapter.describeCalls++
	return nil, errors.New("Pure Chat reached Action Describe")
}

func (adapter *zeroAccessActionAdapter) Prepare(
	context.Context,
	moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	adapter.prepareCalls++
	return nil, errors.New("Pure Chat reached Action Prepare")
}

func (adapter *zeroAccessActionAdapter) ExecutePrepared(
	context.Context,
	modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	adapter.executeCalls++
	return moduleapi.ActionExecutionResultV1{}, errors.New(
		"Pure Chat reached Action executor",
	)
}
