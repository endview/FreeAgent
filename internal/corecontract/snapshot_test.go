package corecontract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestMemberSnapshotFreezesPortOrderWithoutReorderingBindings(t *testing.T) {
	input := validMemberSnapshotInput()
	contextPlan := moduleapi.PortPlan{
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameContextProvide,
			ExactVersion: moduleapi.PortVersionV1,
		},
		Bindings: []moduleapi.PortBinding{
			validSnapshotBinding("context.role", "role-z", moduleapi.FailureRequired),
			validSnapshotBinding("skill.static", "skill-a", moduleapi.FailureOptional),
		},
	}
	contextPlan.Bindings[0].StaticContextRefs = []string{
		strings.Repeat("b", 64),
		strings.Repeat("c", 64),
	}
	contextPlan.Bindings[1].StaticContextRefs = []string{
		strings.Repeat("d", 64),
	}
	input.PortPlans = append([]moduleapi.PortPlan{contextPlan}, input.PortPlans...)

	snapshot, canonical, err := NewMemberExecutionSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PortPlans[0].Port.Name != moduleapi.PortNameContextProvide ||
		snapshot.PortPlans[1].Port.Name != moduleapi.PortNameModelGenerate {
		t.Fatalf("port plans were not canonically ordered: %+v", snapshot.PortPlans)
	}
	if got := snapshot.PortPlans[0].Bindings[0].Provider.InstanceID; got != "role-z" {
		t.Fatalf("binding order changed: first instance = %q", got)
	}
	input.PortPlans[0].Bindings[0].Provider.InstanceID = "mutated"
	input.PortPlans[0].Bindings[0].StaticContextRefs[0] = strings.Repeat("d", 64)
	if got := snapshot.PortPlans[0].Bindings[0].Provider.InstanceID; got != "role-z" {
		t.Fatalf("snapshot aliases caller binding: %q", got)
	}
	if got := snapshot.PortPlans[0].Bindings[0].StaticContextRefs[0]; got !=
		strings.Repeat("b", 64) {
		t.Fatalf("snapshot aliases caller static context refs: %q", got)
	}
	if !bytes.Contains(
		canonical,
		[]byte(
			`"static_context_refs":["`+
				strings.Repeat("b", 64)+
				`","`+
				strings.Repeat("c", 64)+
				`"]`,
		),
	) || !bytes.Contains(canonical, []byte(`"static_context_refs":[]`)) {
		t.Fatalf("snapshot did not freeze static context arrays: %s", canonical)
	}
	if !moduleapi.ValidSHA256(snapshot.MemberSnapshotDigest) {
		t.Fatalf("invalid snapshot digest %q", snapshot.MemberSnapshotDigest)
	}
	restored, err := RestoreMemberExecutionSnapshot(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if restored.MemberSnapshotDigest != snapshot.MemberSnapshotDigest {
		t.Fatal("restored digest changed")
	}
}

func TestMemberSnapshotFailsClosedOnMissingOrDuplicateModelPlan(t *testing.T) {
	input := validMemberSnapshotInput()
	input.PortPlans = nil
	if _, _, err := NewMemberExecutionSnapshot(input); err == nil {
		t.Fatal("snapshot without model.generate/v2 was accepted")
	}

	input = validMemberSnapshotInput()
	input.PortPlans = append(input.PortPlans, input.PortPlans[0])
	if _, _, err := NewMemberExecutionSnapshot(input); err == nil {
		t.Fatal("duplicate PortPlan was accepted")
	}
}

func TestMemberSnapshotOptionallyFreezesModelProfileWithoutChangingNilWire(
	t *testing.T,
) {
	withoutProfile := validMemberSnapshotInput()
	baseline, baselineCanonical, err := NewMemberExecutionSnapshot(withoutProfile)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.ModelProfile != nil ||
		bytes.Contains(baselineCanonical, []byte(`"model_profile"`)) ||
		bytes.Contains(baselineCanonical, []byte(`"actions"`)) {
		t.Fatalf("nil ModelProfile entered the canonical wire: %s", baselineCanonical)
	}
	const legacyNoActionDigest = "ea2fd5a5d12d4bbc22a58a4ea44f8925623bfcd9c277fd72d00d8d5bbe4f848c"
	if baseline.MemberSnapshotDigest != legacyNoActionDigest {
		t.Fatalf(
			"no-Action member snapshot identity changed: %s",
			baseline.MemberSnapshotDigest,
		)
	}

	profileRef := ModelProfileRef{
		ID:      "model-profile-primary",
		Version: "1",
		Digest:  strings.Repeat("d", 64),
	}
	withProfile := validMemberSnapshotInput()
	withProfile.ModelProfile = &profileRef
	frozen, canonical, err := NewMemberExecutionSnapshot(withProfile)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.ModelProfile == nil || *frozen.ModelProfile != profileRef ||
		!bytes.Contains(canonical, []byte(`"model_profile":{`)) {
		t.Fatalf("ModelProfile was not frozen: %+v %s", frozen, canonical)
	}
	profileRef.ID = "mutated-caller"
	if frozen.ModelProfile.ID != "model-profile-primary" {
		t.Fatal("snapshot aliases caller-owned ModelProfileRef")
	}
	if frozen.MemberSnapshotDigest == baseline.MemberSnapshotDigest {
		t.Fatal("ModelProfileRef did not participate in MemberSnapshotDigest")
	}
	restored, err := RestoreMemberExecutionSnapshot(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ModelProfile == nil ||
		restored.ModelProfile.ID != "model-profile-primary" {
		t.Fatalf("restored ModelProfile = %+v", restored.ModelProfile)
	}

	invalid := validMemberSnapshotInput()
	invalid.ModelProfile = &ModelProfileRef{
		ID:      "model-profile-primary",
		Version: "1",
		Digest:  strings.Repeat("D", 64),
	}
	if _, _, err := NewMemberExecutionSnapshot(invalid); err == nil {
		t.Fatal("invalid optional ModelProfileRef accepted")
	}
}

func TestMemberSnapshotClosesActionPortAndProjectsOnlyPublicModelView(
	t *testing.T,
) {
	input := validMemberSnapshotInput()
	input.PortPlans = append(input.PortPlans, validActionSnapshotPlanV1())
	first := validFrozenActionDefinitionV1()
	first.PublicActionID = "workspace.read"
	first.ProviderActionID = "provider.workspace_read"
	second := validFrozenActionDefinitionV1()
	second.PublicActionID = "file.read"
	second.ProviderActionID = "provider.file_read"
	input.Actions = []FrozenActionDefinitionV1{first, second}

	snapshot, canonical, err := NewMemberExecutionSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Actions) != 2 ||
		snapshot.Actions[0].PublicActionID != "file.read" ||
		snapshot.Actions[1].PublicActionID != "workspace.read" {
		t.Fatalf("Actions were not canonically sorted: %+v", snapshot.Actions)
	}
	input.Actions[0].InputSchema[0] = '['
	if snapshot.Actions[1].InputSchema[0] != '{' {
		t.Fatal("member snapshot aliases Action schema")
	}
	if !bytes.Contains(canonical, []byte(`"actions":[`)) {
		t.Fatalf("Action snapshot omitted definitions: %s", canonical)
	}
	modelActions, err := ModelActionDefinitionsV1(snapshot.Actions)
	if err != nil {
		t.Fatal(err)
	}
	modelWire, err := moduleapi.CanonicalJSON(mustJSONMarshal(t, modelActions))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(modelWire, []byte("provider.file_read")) ||
		bytes.Contains(modelWire, []byte(`"effect_class"`)) ||
		bytes.Contains(modelWire, []byte(`"binding_index"`)) {
		t.Fatalf("private Action routing entered model view: %s", modelWire)
	}
	restored, err := RestoreMemberExecutionSnapshot(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Actions[0].DefinitionDigest !=
		snapshot.Actions[0].DefinitionDigest {
		t.Fatal("restored Action DefinitionDigest changed")
	}
}

func TestMemberSnapshotActionClosureFailsClosed(t *testing.T) {
	valid := func() MemberExecutionSnapshot {
		input := validMemberSnapshotInput()
		input.PortPlans = append(input.PortPlans, validActionSnapshotPlanV1())
		input.Actions = []FrozenActionDefinitionV1{
			validFrozenActionDefinitionV1(),
		}
		return input
	}
	tests := []struct {
		name   string
		mutate func(*MemberExecutionSnapshot)
	}{
		{
			name: "definition without PortPlan",
			mutate: func(input *MemberExecutionSnapshot) {
				input.PortPlans = input.PortPlans[:1]
			},
		},
		{
			name: "PortPlan without definition",
			mutate: func(input *MemberExecutionSnapshot) {
				input.Actions = nil
			},
		},
		{
			name: "BindingIndex outside plan",
			mutate: func(input *MemberExecutionSnapshot) {
				input.Actions[0].BindingIndex = 1
			},
		},
		{
			name: "duplicate PublicActionID",
			mutate: func(input *MemberExecutionSnapshot) {
				input.Actions = append(input.Actions, input.Actions[0])
			},
		},
		{
			name: "drifted DefinitionDigest",
			mutate: func(input *MemberExecutionSnapshot) {
				input.Actions[0].DefinitionDigest = strings.Repeat("f", 64)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid()
			test.mutate(&input)
			if _, _, err := NewMemberExecutionSnapshot(input); err == nil {
				t.Fatal("accepted invalid Action snapshot closure")
			}
		})
	}
}

func TestMemberSnapshotRestoreRejectsDigestAndWireDrift(t *testing.T) {
	snapshot, canonical, err := NewMemberExecutionSnapshot(validMemberSnapshotInput())
	if err != nil {
		t.Fatal(err)
	}
	drifted := bytes.Replace(
		canonical,
		[]byte(snapshot.MemberSnapshotDigest),
		[]byte(strings.Repeat("f", 64)),
		1,
	)
	if _, err := RestoreMemberExecutionSnapshot(drifted); err == nil {
		t.Fatal("drifted member digest was accepted")
	}
	unknown := bytes.Replace(
		canonical,
		[]byte(`"schema_version"`),
		[]byte(`"unknown":true,"schema_version"`),
		1,
	)
	if _, err := RestoreMemberExecutionSnapshot(unknown); err == nil {
		t.Fatal("unknown snapshot field was accepted")
	}
}

func TestRunManifestNormalizesDeadlineAndBindsMember(t *testing.T) {
	member, _, err := NewMemberExecutionSnapshot(validMemberSnapshotInput())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Date(
		2026, time.July, 30, 11, 12, 13, 123456789,
		time.FixedZone("CST", 8*60*60),
	)
	input := validRunManifestInput(member)
	input.Deadline = deadline

	manifest, canonical, err := NewRunManifest(input)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Deadline.Location() != time.UTC {
		t.Fatalf("deadline location = %s", manifest.Deadline.Location())
	}
	if err := manifest.ValidateAgainstMember(member); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreRunManifest(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ManifestDigest != manifest.ManifestDigest {
		t.Fatal("restored manifest digest changed")
	}
	if !bytes.Contains(canonical, []byte(`"deadline":"2026-07-30T03:12:13.123456789Z"`)) {
		t.Fatalf("deadline not encoded as normalized RFC3339Nano: %s", canonical)
	}
	if bytes.Contains(canonical, []byte(`"parent_run_id"`)) ||
		bytes.Contains(canonical, []byte(`"composite"`)) {
		t.Fatalf("Pure Chat manifest contains Composite-only fields: %s", canonical)
	}
	const pureChatManifestGoldenDigest = "859dbeb1ba990c299a9768a8fa0c987b135e0fc99082f4097018e227a0a1afa3"
	if manifest.ManifestDigest != pureChatManifestGoldenDigest {
		t.Fatalf(
			"Pure Chat manifest digest=%q want golden %q",
			manifest.ManifestDigest,
			pureChatManifestGoldenDigest,
		)
	}
}

func TestRunManifestRejectsParentWithoutCompositeAndReferenceDrift(t *testing.T) {
	member, _, err := NewMemberExecutionSnapshot(validMemberSnapshotInput())
	if err != nil {
		t.Fatal(err)
	}
	input := validRunManifestInput(member)
	input.ParentRunID = "parent"
	if _, _, err := NewRunManifest(input); err == nil {
		t.Fatal("ParentRunID without a legal Composite closure was accepted")
	}

	input = validRunManifestInput(member)
	input.TaskInputDigest = strings.Repeat("e", 64)
	if _, _, err := NewRunManifest(input); err == nil {
		t.Fatal("different TaskInputRef and TaskInputDigest were accepted")
	}

	other := member
	other.MemberSnapshotDigest = strings.Repeat("f", 64)
	manifest, _, err := NewRunManifest(validRunManifestInput(member))
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.ValidateAgainstMember(other); err == nil {
		t.Fatal("manifest accepted a different member snapshot")
	}
}

func validMemberSnapshotInput() MemberExecutionSnapshot {
	digests := func(character string) string { return strings.Repeat(character, 64) }
	return MemberExecutionSnapshot{
		SchemaVersion:   MemberExecutionSnapshotSchemaVersionV2,
		CompilerVersion: AssemblyCompilerVersionV2,
		Catalog: CatalogSnapshotRef{
			ID: "runtime-catalog", Version: "1", Digest: digests("1"),
		},
		MemberID:  "member-primary",
		Agent:     AgentRef{ID: "agent.chat", Version: "1", Digest: digests("2")},
		Profile:   ProfileRef{ID: "profile.chat", Version: "1", Digest: digests("3")},
		Workspace: WorkspaceRef{ID: "workspace.main", Version: "1", Digest: digests("4")},
		PortPlans: []moduleapi.PortPlan{
			{
				Port: moduleapi.PortRef{
					Name:         moduleapi.PortNameModelGenerate,
					ExactVersion: moduleapi.PortVersionV2,
				},
				Bindings: []moduleapi.PortBinding{
					validSnapshotBinding(
						"model.deepseek",
						"model-primary",
						moduleapi.FailureRequired,
					),
				},
			},
		},
		ContextPolicy: PolicyRef{
			ID: "policy.context", Version: "1", Digest: digests("8"),
		},
		SchedulingPolicy: PolicyRef{
			ID: "policy.scheduling", Version: "1", Digest: digests("a"),
		},
	}
}

func validRunManifestInput(member MemberExecutionSnapshot) RunManifest {
	digest := func(character string) string { return strings.Repeat(character, 64) }
	return RunManifest{
		SchemaVersion:         RunManifestSchemaVersionV2,
		CoreRuntimeVersion:    CoreRuntimeVersionV2,
		AdmissionKey:          "admission-1",
		AdmissionIntentDigest: digest("b"),
		RunID:                 "run-1",
		TenantID:              "tenant-1",
		Workspace:             member.Workspace,
		PrimaryAgent:          member.Agent,
		Members: []MemberSnapshotRef{
			{MemberID: member.MemberID, Digest: member.MemberSnapshotDigest},
		},
		PrimaryMemberID:   member.MemberID,
		TaskInputRef:      digest("c"),
		TaskInputDigest:   digest("c"),
		CancellationScope: "run",
		Deadline:          time.Date(2026, time.July, 30, 3, 12, 13, 0, time.UTC),
		RecoveryRootRef:   "recovery/run-1",
	}
}

func validSnapshotBinding(
	moduleID string,
	instanceID string,
	failurePolicy moduleapi.FailurePolicy,
) moduleapi.PortBinding {
	return moduleapi.PortBinding{
		Provider: moduleapi.ActivatedModuleRef{
			ModuleID:           moduleID,
			Version:            "1",
			ArtifactDigest:     strings.Repeat("5", 64),
			InstanceID:         instanceID,
			ExecutionClass:     moduleapi.ExecutionDeclarative,
			AdapterIdentity:    "builtin.test",
			ActivationRevision: 1,
		},
		ConfigRef:           strings.Repeat("6", 64),
		AuthorityCeilingRef: strings.Repeat("7", 64),
		StaticContextRefs:   []string{},
		FailurePolicy:       failurePolicy,
	}
}

func validActionSnapshotPlanV1() moduleapi.PortPlan {
	binding := validSnapshotBinding(
		"tool.files",
		"files-primary",
		moduleapi.FailureRequired,
	)
	binding.Provider.ExecutionClass = moduleapi.ExecutionTrustedInProcess
	return moduleapi.PortPlan{
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameActionProvider,
			ExactVersion: moduleapi.PortVersionV1,
		},
		Bindings: []moduleapi.PortBinding{binding},
	}
}

func mustJSONMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
