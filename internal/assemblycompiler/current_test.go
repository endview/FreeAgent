package assemblycompiler

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCurrentCompilerUsesOneFrozenControlCatalogAndPreservesBindingOrder(
	t *testing.T,
) {
	input := validCurrentCompileInput(t)
	first, err := (Compiler{}).Compile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (Compiler{}).Compile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(
		first.MemberSnapshotCanonical,
		second.MemberSnapshotCanonical,
	) || !bytes.Equal(
		first.RunManifestCanonical,
		second.RunManifestCanonical,
	) {
		t.Fatal("same complete compile input produced different bytes")
	}
	if first.PublishedBasis != input.PublishedBasis {
		t.Fatal("compiler changed the published basis")
	}
	contextPlan := first.MemberSnapshot.PortPlans[0]
	if contextPlan.Port.Name != moduleapi.PortNameContextProvide ||
		len(contextPlan.Bindings) != 2 ||
		contextPlan.Bindings[0].Provider.InstanceID != "context-role" ||
		contextPlan.Bindings[1].Provider.InstanceID != "context-skill" {
		t.Fatalf("context binding order changed: %+v", contextPlan)
	}
	if got := contextPlan.Bindings[0].StaticContextRefs; len(got) != 2 ||
		got[0] != hash("0") ||
		got[1] != hash("1") {
		t.Fatalf("static context order changed: %v", got)
	}
	first.MemberSnapshot.PortPlans[0].Bindings[0].StaticContextRefs[0] =
		hash("e")
	third, err := (Compiler{}).Compile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got := third.MemberSnapshot.PortPlans[0].
		Bindings[0].StaticContextRefs[0]; got != hash("0") {
		t.Fatalf("compiler output aliases a prior result: %s", got)
	}
	if first.MemberSnapshot.PortPlans[1].Port.Name !=
		moduleapi.PortNameModelGenerate {
		t.Fatalf("port plans are not canonical: %+v", first.MemberSnapshot.PortPlans)
	}
}

func TestCurrentCompilerDerivesConversationTurnOnlyFromAdmissionIntent(
	t *testing.T,
) {
	input := validCurrentCompileInput(t)
	baseline, err := (Compiler{}).Compile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	intent := currentIntent(
		[]moduleapi.PortRef{currentPort(moduleapi.PortNameModelGenerate)},
		[]byte(`{}`),
	)
	intent.ConversationTurn = &corecontract.ConversationTurnIntentV1{
		SchemaVersion:                corecontract.ConversationTurnIntentSchemaVersionV1,
		ConversationID:               "conversation-1",
		ExpectedConversationRevision: 1,
		ExpectedHeadRunID:            "run-previous",
	}
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	input.IntentCanonical = canonical
	input.IntentDigest = digest
	compiled, err := (Compiler{}).Compile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	turn := compiled.RunManifest.ConversationTurn
	if turn == nil ||
		turn.SchemaVersion != corecontract.ConversationTurnRefSchemaVersionV1 ||
		turn.ConversationID != "conversation-1" ||
		turn.PrincipalID != intent.PrincipalID ||
		turn.TurnIndex != 2 ||
		turn.PredecessorRunID != "run-previous" {
		t.Fatalf("derived Conversation turn=%+v", turn)
	}
	if !bytes.Equal(
		baseline.MemberSnapshotCanonical,
		compiled.MemberSnapshotCanonical,
	) {
		t.Fatal("Conversation relation changed the frozen Agent assembly")
	}
	if bytes.Equal(
		baseline.RunManifestCanonical,
		compiled.RunManifestCanonical,
	) {
		t.Fatal("Conversation relation did not enter the immutable RunManifest")
	}
}

func TestCurrentCompilerCatalogBytesAreTheOnlyProviderSource(t *testing.T) {
	input := validCurrentCompileInput(t)
	_, _, unusedCatalog, unusedRef := currentControlCatalog(t, true)
	input.CatalogCanonical = unusedCatalog
	input.PublishedBasis.Catalog = unusedRef
	output, err := (Compiler{}).Compile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range output.MemberSnapshot.PortPlans {
		for _, binding := range plan.Bindings {
			if binding.Provider.InstanceID == "unused-context" {
				t.Fatal("compiler attached an available but unrequested provider")
			}
		}
	}

	input = validCurrentCompileInput(t)
	input.PublishedBasis.Catalog.Digest = strings.Repeat("f", 64)
	if _, err := (Compiler{}).Compile(
		context.Background(),
		input,
	); err == nil {
		t.Fatal("catalog bytes independent from CatalogRef were accepted")
	}
}

func TestCurrentCompilerFreezesOptionalModelProfileWithoutChangingRouting(
	t *testing.T,
) {
	baseInput := validCurrentCompileInput(t)
	base, err := (Compiler{}).Compile(context.Background(), baseInput)
	if err != nil {
		t.Fatal(err)
	}
	if base.MemberSnapshot.ModelProfile != nil || bytes.Contains(
		base.MemberSnapshotCanonical,
		[]byte(`"model_profile"`),
	) {
		t.Fatalf(
			"nil model profile changed the member snapshot wire: %s",
			base.MemberSnapshotCanonical,
		)
	}

	modelProfile := corecontract.ModelProfileRef{
		ID:      "model-profile.chat",
		Version: "1",
		Digest:  hash("a"),
	}
	profiledInput := currentCompileInputWithModelProfile(
		t,
		baseInput,
		&modelProfile,
	)
	profiled, err := (Compiler{}).Compile(
		context.Background(),
		profiledInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if profiled.MemberSnapshot.ModelProfile == nil ||
		*profiled.MemberSnapshot.ModelProfile != modelProfile {
		t.Fatalf(
			"model profile ref was not frozen exactly: %+v",
			profiled.MemberSnapshot.ModelProfile,
		)
	}
	if !bytes.Contains(
		profiled.MemberSnapshotCanonical,
		[]byte(`"model_profile":{`),
	) {
		t.Fatalf(
			"model profile ref is absent from member snapshot: %s",
			profiled.MemberSnapshotCanonical,
		)
	}
	if !reflect.DeepEqual(
		base.MemberSnapshot.PortPlans,
		profiled.MemberSnapshot.PortPlans,
	) {
		t.Fatal("model profile changed provider routing or binding order")
	}
	if base.MemberSnapshot.MemberSnapshotDigest ==
		profiled.MemberSnapshot.MemberSnapshotDigest {
		t.Fatal("model profile ref did not affect the member snapshot digest")
	}

	profiled.MemberSnapshot.ModelProfile.Digest = hash("b")
	again, err := (Compiler{}).Compile(context.Background(), profiledInput)
	if err != nil {
		t.Fatal(err)
	}
	if again.MemberSnapshot.ModelProfile == nil ||
		again.MemberSnapshot.ModelProfile.Digest != hash("a") {
		t.Fatal("compiler output aliases a prior model profile result")
	}

	changed := modelProfile
	changed.Digest = hash("c")
	changedInput := currentCompileInputWithModelProfile(
		t,
		baseInput,
		&changed,
	)
	changedOutput, err := (Compiler{}).Compile(
		context.Background(),
		changedInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if changedOutput.MemberSnapshot.ModelProfile == nil ||
		*changedOutput.MemberSnapshot.ModelProfile != changed ||
		changedOutput.MemberSnapshot.MemberSnapshotDigest ==
			profiled.MemberSnapshot.MemberSnapshotDigest {
		t.Fatal("changed model profile ref was not frozen into a new snapshot")
	}
}

func TestCurrentCompilerFailsClosedOnUnavailableCapabilityOrExplicitLimits(
	t *testing.T,
) {
	input := validCurrentCompileInput(t)
	intent, _, _, err := corecontract.NewAdmissionIntentV1(
		currentIntent(
			[]moduleapi.PortRef{
				currentPort("knowledge.retrieve"),
			},
			[]byte(`{}`),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	input.IntentCanonical = canonical
	input.IntentDigest = digest
	if _, err := (Compiler{}).Compile(
		context.Background(),
		input,
	); !errors.Is(err, ErrCapabilityNotAvailable) {
		t.Fatalf("unsupported capability error=%v", err)
	}

	input = validCurrentCompileInput(t)
	_, canonical, digest, err = corecontract.NewAdmissionIntentV1(
		currentIntent(
			[]moduleapi.PortRef{
				currentPort(moduleapi.PortNameModelGenerate),
			},
			[]byte(`{"max_steps":2}`),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	input.IntentCanonical = canonical
	input.IntentDigest = digest
	if _, err := (Compiler{}).Compile(
		context.Background(),
		input,
	); !errors.Is(err, ErrCapabilityNotAvailable) {
		t.Fatalf("ignored explicit limits error=%v", err)
	}
}

func TestCurrentCompilerFreezesWorkspaceChannelBindingOnlyForChannelAdmission(
	t *testing.T,
) {
	pureInput := validCurrentCompileInput(t)
	pure, err := (Compiler{}).Compile(context.Background(), pureInput)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range pure.MemberSnapshot.PortPlans {
		if plan.Port.Name == moduleapi.PortNameChannelTransport {
			t.Fatal("Pure Chat unexpectedly froze a Channel PortPlan")
		}
	}
	if bytes.Contains(
		pureInput.IntentCanonical,
		[]byte(`"channel_endpoint_id"`),
	) {
		t.Fatalf("Pure Chat intent wire changed: %s", pureInput.IntentCanonical)
	}

	channelInput := currentCompileInputWithChannel(t, pureInput, true)
	channel, err := (Compiler{}).Compile(context.Background(), channelInput)
	if err != nil {
		t.Fatal(err)
	}
	var channelPlan *moduleapi.PortPlan
	for index := range channel.MemberSnapshot.PortPlans {
		plan := &channel.MemberSnapshot.PortPlans[index]
		if plan.Port.Name == moduleapi.PortNameChannelTransport {
			channelPlan = plan
		}
	}
	if channelPlan == nil || len(channelPlan.Bindings) != 1 {
		t.Fatalf("exact Channel PortPlan was not frozen: %+v", channel.MemberSnapshot.PortPlans)
	}
	binding := channelPlan.Bindings[0]
	if binding.Provider.InstanceID != "channel-loopback" ||
		binding.Provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		binding.ConfigRef != hash("3") ||
		binding.AuthorityCeilingRef != hash("4") ||
		binding.FailurePolicy != moduleapi.FailureRequired {
		t.Fatalf("Channel binding did not close Workspace/Catalog exactly: %+v", binding)
	}
}

func TestCurrentCompilerRejectsMissingOrDisabledChannelEndpoint(t *testing.T) {
	input := validCurrentCompileInput(t)
	intent, err := corecontract.RestoreAdmissionIntentV1(
		input.IntentCanonical,
		input.IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	intent.ChannelEndpointID = "endpoint-absent"
	_, input.IntentCanonical, input.IntentDigest, err =
		corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Compiler{}).Compile(
		context.Background(),
		input,
	); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("missing Channel endpoint error=%v", err)
	}

	input = currentCompileInputWithChannel(t, validCurrentCompileInput(t), false)
	if _, err := (Compiler{}).Compile(
		context.Background(),
		input,
	); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled Channel endpoint error=%v", err)
	}
}

func TestCurrentCompilerRejectsBrokenControlCatalogProvenance(t *testing.T) {
	input := validCurrentCompileInput(t)
	input.PublishedBasis.TenantID = "other-tenant"
	if _, err := (Compiler{}).Compile(
		context.Background(),
		input,
	); err == nil {
		t.Fatal("cross-tenant published basis was accepted")
	}

	input = validCurrentCompileInput(t)
	controlCanonical, controlRef, _, _ := currentControlCatalog(t, false)
	control, err := controlcontract.RestoreControlSnapshot(
		controlCanonical,
		controlRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	control.SnapshotID = "different-control"
	_, changedRef, changedCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	input.ControlCanonical = changedCanonical
	input.PublishedBasis.Control = changedRef
	// Catalog still points at the original Control snapshot.
	if _, err := (Compiler{}).Compile(
		context.Background(),
		input,
	); err == nil {
		t.Fatal("Catalog linked to a different Control was accepted")
	}
}

func TestCurrentCompilerMaterializesOnlyFrozenActionPlan(t *testing.T) {
	input := currentCompileInputWithActions(t, validCurrentCompileInput(t))
	input.ActionBindingMaterials = []actionmaterializer.BindingMaterialV1{
		{
			ConfigCanonical:    []byte(`{"binding":1}`),
			AuthorityCanonical: []byte(`{"authority":1}`),
		},
		{
			ConfigCanonical:    []byte(`{"binding":2}`),
			AuthorityCanonical: []byte(`{"authority":2}`),
		},
	}
	materializer := &recordingCompilerActionMaterializer{
		definitions: []corecontract.FrozenActionDefinitionV1{
			currentFrozenAction(t, "z.action", "provider.first", 0),
			currentFrozenAction(t, "a.action", "provider.second", 1),
		},
	}
	input.ActionMaterializer = materializer

	compiled, err := (Compiler{}).Compile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if materializer.calls != 1 {
		t.Fatalf("materializer calls=%d want 1", materializer.calls)
	}
	if materializer.input.TenantID != "tenant-1" ||
		materializer.input.WorkspaceID != "workspace.main" ||
		materializer.input.Plan.Port.Name != moduleapi.PortNameActionProvider ||
		len(materializer.input.Plan.Bindings) != 2 ||
		materializer.input.Plan.Bindings[0].Provider.InstanceID != "action-first" ||
		materializer.input.Plan.Bindings[1].Provider.InstanceID != "action-second" ||
		string(materializer.input.Bindings[0].ConfigCanonical) != `{"binding":1}` ||
		string(materializer.input.Bindings[1].ConfigCanonical) != `{"binding":2}` {
		t.Fatalf("materializer input did not preserve exact Action order: %+v", materializer.input)
	}
	if len(compiled.MemberSnapshot.Actions) != 2 ||
		compiled.MemberSnapshot.Actions[0].PublicActionID != "a.action" ||
		compiled.MemberSnapshot.Actions[1].PublicActionID != "z.action" ||
		!bytes.Contains(compiled.MemberSnapshotCanonical, []byte(`"actions":[`)) {
		t.Fatalf("frozen Actions=%+v", compiled.MemberSnapshot.Actions)
	}

	input.ActionBindingMaterials[0].ConfigCanonical[2] = 'x'
	if string(materializer.input.Bindings[0].ConfigCanonical) != `{"binding":1}` {
		t.Fatal("Compiler passed caller-owned Action material bytes")
	}
}

func TestCurrentCompilerActionMaterializationFailsClosed(t *testing.T) {
	withActions := currentCompileInputWithActions(t, validCurrentCompileInput(t))
	withActions.ActionBindingMaterials = []actionmaterializer.BindingMaterialV1{
		{ConfigCanonical: []byte(`{}`), AuthorityCanonical: []byte(`{}`)},
		{ConfigCanonical: []byte(`{}`), AuthorityCanonical: []byte(`{}`)},
	}
	if _, err := (Compiler{}).Compile(context.Background(), withActions); !errors.Is(
		err,
		ErrCapabilityNotAvailable,
	) {
		t.Fatalf("missing materializer error=%v", err)
	}

	materializer := &recordingCompilerActionMaterializer{
		definitions: []corecontract.FrozenActionDefinitionV1{
			currentFrozenAction(t, "a.action", "provider.first", 0),
		},
	}
	missingMaterial := withActions
	missingMaterial.ActionMaterializer = materializer
	missingMaterial.ActionBindingMaterials = missingMaterial.ActionBindingMaterials[:1]
	if _, err := (Compiler{}).Compile(context.Background(), missingMaterial); err == nil {
		t.Fatal("incomplete Action binding materials accepted")
	}
	if materializer.calls != 0 {
		t.Fatal("materializer called before binding material cardinality closed")
	}

	pure := validCurrentCompileInput(t)
	pure.ActionBindingMaterials = []actionmaterializer.BindingMaterialV1{{
		ConfigCanonical: []byte(`{}`), AuthorityCanonical: []byte(`{}`),
	}}
	if _, err := (Compiler{}).Compile(context.Background(), pure); err == nil {
		t.Fatal("Action materials without Action PortPlan accepted")
	}
}

func TestCurrentCompilerPureChatNeverCallsOptionalActionMaterializer(
	t *testing.T,
) {
	input := validCurrentCompileInput(t)
	baseline, err := (Compiler{}).Compile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	materializer := &recordingCompilerActionMaterializer{}
	input.ActionMaterializer = materializer
	withOptionalGate, err := (Compiler{}).Compile(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if materializer.calls != 0 {
		t.Fatalf("Pure Chat called Action materializer %d times", materializer.calls)
	}
	if !bytes.Equal(
		baseline.MemberSnapshotCanonical,
		withOptionalGate.MemberSnapshotCanonical,
	) || !bytes.Equal(
		baseline.RunManifestCanonical,
		withOptionalGate.RunManifestCanonical,
	) || bytes.Contains(
		withOptionalGate.MemberSnapshotCanonical,
		[]byte(`"actions"`),
	) {
		t.Fatal("optional Action gate changed Pure Chat canonical bytes")
	}
}

func validCurrentCompileInput(t *testing.T) CompileInput {
	t.Helper()
	_, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(
			currentIntent(
				[]moduleapi.PortRef{
					currentPort(moduleapi.PortNameModelGenerate),
				},
				[]byte(`{}`),
			),
		)
	if err != nil {
		t.Fatal(err)
	}
	controlCanonical, controlRef, catalogCanonical, catalogRef :=
		currentControlCatalog(t, false)
	return CompileInput{
		IntentCanonical: intentCanonical,
		IntentDigest:    intentDigest,
		RunID:           "run-1",
		MemberID:        "member-primary",
		RecoveryRootRef: "recovery/run-1",
		PublishedBasis: controlcontract.PublishedBasis{
			TenantID:        "tenant-1",
			PointerRevision: 1,
			Control:         controlRef,
			Catalog:         catalogRef,
		},
		ControlCanonical: controlCanonical,
		CatalogCanonical: catalogCanonical,
	}
}

func currentIntent(
	requested []moduleapi.PortRef,
	limits []byte,
) corecontract.AdmissionIntentV1 {
	return corecontract.AdmissionIntentV1{
		SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
		TenantID:          "tenant-1",
		AdmissionKey:      "admission-1",
		PrincipalID:       "principal-1",
		WorkspaceID:       "workspace.main",
		AgentID:           "agent.chat",
		ProfileID:         "profile.chat",
		TaskInputRef:      hash("1"),
		RequestedPorts:    requested,
		Deadline:          time.Date(2026, time.July, 30, 6, 0, 0, 0, time.UTC),
		CancellationScope: "run",
		ExplicitLimits:    limits,
	}
}

func currentControlCatalog(
	t *testing.T,
	includeUnused bool,
) ([]byte, controlcontract.ControlSnapshotRef, []byte, controlcontract.CatalogGenerationRef) {
	t.Helper()
	control := controlcontract.ControlSnapshot{
		SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV1,
		SnapshotID:    "control-1",
		TenantID:      "tenant-1",
		Revision:      1,
		Agents: []corecontract.AgentRef{
			{ID: "agent.chat", Version: "1", Digest: hash("2")},
		},
		Workspaces: []controlcontract.WorkspaceDefinition{
			{
				Workspace: corecontract.WorkspaceRef{
					ID: "workspace.main", Version: "1", Digest: hash("3"),
				},
				BudgetPolicy: policy("policy.budget", "4"),
			},
		},
		Profiles: []controlcontract.ProfileDefinition{
			{
				Profile: corecontract.ProfileRef{
					ID: "profile.chat", Version: "1", Digest: hash("5"),
				},
				ContextPolicy:    policy("policy.context", "6"),
				CostPolicy:       policy("policy.cost", "7"),
				SchedulingPolicy: policy("policy.scheduling", "8"),
				Bindings: []controlcontract.BindingSpec{
					binding(
						moduleapi.PortNameModelGenerate,
						"model-primary",
						"9",
						"a",
						moduleapi.FailureRequired,
					),
					binding(
						moduleapi.PortNameContextProvide,
						"context-role",
						"b",
						"c",
						moduleapi.FailureRequired,
						hash("0"),
						hash("1"),
					),
					binding(
						moduleapi.PortNameContextProvide,
						"context-skill",
						"d",
						"e",
						moduleapi.FailureOptional,
						hash("2"),
					),
				},
			},
		},
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	entries := []controlcontract.CatalogEntry{
		catalogEntry(
			"model-primary",
			"model.provider",
			moduleapi.PortNameModelGenerate,
		),
		catalogEntry(
			"context-role",
			"context.role",
			moduleapi.PortNameContextProvide,
		),
		catalogEntry(
			"context-skill",
			"skill.static",
			moduleapi.PortNameContextProvide,
		),
	}
	if includeUnused {
		entries = append(entries, catalogEntry(
			"unused-context",
			"context.unused",
			moduleapi.PortNameContextProvide,
		))
	}
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(
			controlcontract.CatalogGeneration{
				SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
				GenerationID:          "catalog-1",
				Generation:            1,
				TenantID:              "tenant-1",
				ControlSnapshotID:     controlRef.SnapshotID,
				ControlSnapshotDigest: controlRef.Digest,
				Entries:               entries,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	return controlCanonical, controlRef, catalogCanonical, catalogRef
}

func currentCompileInputWithModelProfile(
	t *testing.T,
	input CompileInput,
	modelProfile *corecontract.ModelProfileRef,
) CompileInput {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		input.ControlCanonical,
		input.PublishedBasis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID != "profile.chat" {
			continue
		}
		if modelProfile == nil {
			control.Profiles[index].ModelProfile = nil
		} else {
			cloned := *modelProfile
			control.Profiles[index].ModelProfile = &cloned
		}
		found = true
		break
	}
	if !found {
		t.Fatal("profile.chat is absent from the test Control")
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}

	catalog, err := controlcontract.RestoreCatalogGeneration(
		input.CatalogCanonical,
		input.PublishedBasis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}

	input.ControlCanonical = controlCanonical
	input.CatalogCanonical = catalogCanonical
	input.PublishedBasis.Control = controlRef
	input.PublishedBasis.Catalog = catalogRef
	return input
}

func currentCompileInputWithActions(
	t *testing.T,
	input CompileInput,
) CompileInput {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		input.ControlCanonical,
		input.PublishedBasis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID != "profile.chat" {
			continue
		}
		control.Profiles[index].Bindings = append(
			control.Profiles[index].Bindings,
			binding(
				moduleapi.PortNameActionProvider,
				"action-first",
				"1",
				"2",
				moduleapi.FailureRequired,
			),
			binding(
				moduleapi.PortNameActionProvider,
				"action-second",
				"3",
				"4",
				moduleapi.FailureRequired,
			),
		)
		found = true
		break
	}
	if !found {
		t.Fatal("profile.chat is absent from the test Control")
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}

	catalog, err := controlcontract.RestoreCatalogGeneration(
		input.CatalogCanonical,
		input.PublishedBasis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	first := catalogEntry(
		"action-first",
		"action.first",
		moduleapi.PortNameActionProvider,
	)
	first.Activation.ExecutionClass = moduleapi.ExecutionTrustedInProcess
	first.Activation.AdapterIdentity = "builtin.action-first"
	second := catalogEntry(
		"action-second",
		"action.second",
		moduleapi.PortNameActionProvider,
	)
	second.Activation.ExecutionClass = moduleapi.ExecutionTrustedInProcess
	second.Activation.AdapterIdentity = "builtin.action-second"
	catalog.Entries = append(catalog.Entries, first, second)
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}

	input.ControlCanonical = controlCanonical
	input.CatalogCanonical = catalogCanonical
	input.PublishedBasis.Control = controlRef
	input.PublishedBasis.Catalog = catalogRef
	return input
}

func currentCompileInputWithChannel(
	t *testing.T,
	input CompileInput,
	enabled bool,
) CompileInput {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		input.ControlCanonical,
		input.PublishedBasis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for index := range control.Workspaces {
		if control.Workspaces[index].Workspace.ID != "workspace.main" {
			continue
		}
		control.Workspaces[index].ChannelEndpoints =
			[]controlcontract.ChannelEndpointDefinition{{
				SchemaVersion:   controlcontract.ChannelEndpointSchemaVersionV1,
				EndpointID:      "endpoint-loopback",
				Channel:         "loopback-http",
				AccountID:       "account-1",
				ConversationID:  "conversation-1",
				TargetAgentID:   "agent.chat",
				TargetProfileID: "profile.chat",
				CursorScopeKey:  "cursor/endpoint-loopback",
				Enabled:         enabled,
				Binding: binding(
					moduleapi.PortNameChannelTransport,
					"channel-loopback",
					"3",
					"4",
					moduleapi.FailureRequired,
				),
			}}
		control.Workspaces[index].ChannelIdentities =
			[]controlcontract.ChannelIdentityDefinition{{
				Channel:        "loopback-http",
				AccountID:      "account-1",
				ExternalUserID: "external-1",
				PrincipalID:    "principal-1",
				ACLEpoch:       1,
				Active:         true,
			}}
		found = true
		break
	}
	if !found {
		t.Fatal("workspace.main is absent from test Control")
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		input.CatalogCanonical,
		input.PublishedBasis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	channelEntry := catalogEntry(
		"channel-loopback",
		"channel.loopback",
		moduleapi.PortNameChannelTransport,
	)
	channelEntry.Activation.ExecutionClass = moduleapi.ExecutionTrustedInProcess
	channelEntry.Activation.AdapterIdentity = "builtin.channel-loopback"
	catalog.Entries = append(catalog.Entries, channelEntry)
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := corecontract.RestoreAdmissionIntentV1(
		input.IntentCanonical,
		input.IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	intent.ChannelEndpointID = "endpoint-loopback"
	_, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	input.IntentCanonical = intentCanonical
	input.IntentDigest = intentDigest
	input.ControlCanonical = controlCanonical
	input.CatalogCanonical = catalogCanonical
	input.PublishedBasis.Control = controlRef
	input.PublishedBasis.Catalog = catalogRef
	return input
}

func currentFrozenAction(
	t *testing.T,
	publicID string,
	providerID string,
	bindingIndex uint32,
) corecontract.FrozenActionDefinitionV1 {
	t.Helper()
	schema, err := moduleapi.CanonicalizeActionInputSchemaV1(
		[]byte(`{"additionalProperties":false,"properties":{},"type":"object"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	frozen, _, err := corecontract.NewFrozenActionDefinitionV1(
		corecontract.FrozenActionDefinitionV1{
			PublicActionID:   publicID,
			ProviderActionID: providerID,
			BindingIndex:     bindingIndex,
			Description:      "Test Action " + publicID,
			InputSchema:      schema,
			EffectClass:      moduleapi.EffectNone,
			MaxResultBytes:   1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return frozen
}

type recordingCompilerActionMaterializer struct {
	calls       int
	input       actionmaterializer.InputV1
	definitions []corecontract.FrozenActionDefinitionV1
}

func (materializer *recordingCompilerActionMaterializer) Materialize(
	_ context.Context,
	input actionmaterializer.InputV1,
) ([]corecontract.FrozenActionDefinitionV1, error) {
	materializer.calls++
	materializer.input = input
	materializer.input.Plan.Bindings = append(
		[]moduleapi.PortBinding(nil),
		input.Plan.Bindings...,
	)
	materializer.input.Bindings = make(
		[]actionmaterializer.BindingMaterialV1,
		len(input.Bindings),
	)
	for index, binding := range input.Bindings {
		materializer.input.Bindings[index] = actionmaterializer.BindingMaterialV1{
			ConfigCanonical:    bytes.Clone(binding.ConfigCanonical),
			AuthorityCanonical: bytes.Clone(binding.AuthorityCanonical),
		}
	}
	return append(
		[]corecontract.FrozenActionDefinitionV1(nil),
		materializer.definitions...,
	), nil
}

func binding(
	port string,
	instance string,
	config string,
	ceiling string,
	failure moduleapi.FailurePolicy,
	staticContextRefs ...string,
) controlcontract.BindingSpec {
	return controlcontract.BindingSpec{
		Port:                currentPort(port),
		InstanceID:          instance,
		ConfigRef:           hash(config),
		AuthorityCeilingRef: hash(ceiling),
		StaticContextRefs: append(
			[]string{},
			staticContextRefs...,
		),
		FailurePolicy: failure,
	}
}

func catalogEntry(
	instanceID string,
	moduleID string,
	port string,
) controlcontract.CatalogEntry {
	return controlcontract.CatalogEntry{
		Activation: moduleapi.ActivatedModuleRef{
			ModuleID:           moduleID,
			Version:            "v1",
			ArtifactDigest:     hash("f"),
			InstanceID:         instanceID,
			ExecutionClass:     moduleapi.ExecutionDeclarative,
			AdapterIdentity:    "builtin." + instanceID,
			ActivationRevision: 1,
		},
		Provides: []moduleapi.PortRef{currentPort(port)},
	}
}

func policy(id string, character string) corecontract.PolicyRef {
	return corecontract.PolicyRef{
		ID: id, Version: "1", Digest: hash(character),
	}
}

func currentPort(name string) moduleapi.PortRef {
	return moduleapi.PortRef{
		Name: name, ExactVersion: moduleapi.PortVersionV1,
	}
}

func hash(character string) string {
	return strings.Repeat(character, 64)
}
