package controlcontract

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestControlSnapshotOptionalCompositeAgentsFreezeRestoreAndFind(t *testing.T) {
	pure, pureRef, pureCanonical, err := NewControlSnapshot(validControlSnapshot())
	if err != nil {
		t.Fatalf("NewControlSnapshot(pure) error = %v", err)
	}
	if bytes.Contains(pureCanonical, []byte(`"composite_agents"`)) {
		t.Fatalf("nil CompositeAgents changed the pure Control wire: %s", pureCanonical)
	}
	legacyCanonical, err := canonicalJSON(struct {
		SchemaVersion string                  `json:"schema_version"`
		SnapshotID    string                  `json:"snapshot_id"`
		TenantID      string                  `json:"tenant_id"`
		Revision      uint64                  `json:"revision"`
		Agents        []corecontract.AgentRef `json:"agents"`
		Workspaces    []WorkspaceDefinition   `json:"workspaces"`
		Profiles      []ProfileDefinition     `json:"profiles"`
		Digest        string                  `json:"digest,omitempty"`
	}{
		SchemaVersion: pure.SchemaVersion,
		SnapshotID:    pure.SnapshotID,
		TenantID:      pure.TenantID,
		Revision:      pure.Revision,
		Agents:        pure.Agents,
		Workspaces:    pure.Workspaces,
		Profiles:      pure.Profiles,
		Digest:        pure.Digest,
	})
	if err != nil {
		t.Fatalf("canonicalJSON(legacy Control wire) error = %v", err)
	}
	if !bytes.Equal(pureCanonical, legacyCanonical) {
		t.Fatalf("nil CompositeAgents changed canonical Control\ngot:  %s\nwant: %s", pureCanonical, legacyCanonical)
	}
	if _, err := RestoreControlSnapshot(pureCanonical, pureRef); err != nil {
		t.Fatalf("RestoreControlSnapshot(pure) error = %v", err)
	}

	input := validControlSnapshot()
	input.CompositeAgents = validCompositeAgentDefinitions()
	frozen, ref, canonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatalf("NewControlSnapshot(composite) error = %v", err)
	}
	if got := []string{
		frozen.CompositeAgents[0].AgentID,
		frozen.CompositeAgents[1].AgentID,
	}; !reflect.DeepEqual(got, []string{"agent.a", "agent.z"}) {
		t.Fatalf("composite Agent order = %v", got)
	}
	definition, found := frozen.FindCompositeAgent("agent.z")
	if !found {
		t.Fatal("agent.z composite definition not found")
	}
	if got := []string{
		definition.Members[0].SlotID,
		definition.Members[1].SlotID,
	}; !reflect.DeepEqual(got, []string{"slot.a", "slot.z"}) {
		t.Fatalf("composite member order = %v", got)
	}
	member, found := definition.FindMember("slot.z")
	if !found || member.FocusID != "frontend" || member.WeightBasisPoints != 3000 {
		t.Fatalf("FindMember(slot.z) = %+v, found=%v", member, found)
	}
	if _, found := definition.FindMember("missing"); found {
		t.Fatal("missing composite member reported present")
	}
	if _, found := frozen.FindCompositeAgent("missing"); found {
		t.Fatal("missing composite Agent reported present")
	}

	input.CompositeAgents[0].Members[0].FocusID = "mutated-input"
	definition.Members[0].FocusID = "mutated-lookup"
	again, found := frozen.FindCompositeAgent("agent.z")
	if !found || again.Members[0].FocusID != "backend" {
		t.Fatalf("composite definition aliases input or lookup output: %+v", again)
	}
	if !bytes.Contains(canonical, []byte(`"composite_agents":[`)) {
		t.Fatalf("composite definitions absent from canonical Control: %s", canonical)
	}
	restored, err := RestoreControlSnapshot(canonical, ref)
	if err != nil {
		t.Fatalf("RestoreControlSnapshot(composite) error = %v", err)
	}
	if !reflect.DeepEqual(restored, frozen) {
		t.Fatal("restored composite Control differs")
	}
}

func TestCompositeAgentWireKeepsWorkspaceBindingMemberOptional(t *testing.T) {
	definitionFields := reflect.TypeOf(CompositeAgentDefinitionV1{})
	gotDefinition := make([]string, definitionFields.NumField())
	for index := range gotDefinition {
		gotDefinition[index] = definitionFields.Field(index).Name
	}
	if want := []string{
		"SchemaVersion",
		"AgentID",
		"CoordinatorProfileID",
		"Members",
		"Reviewer",
		"Decision",
	}; !reflect.DeepEqual(gotDefinition, want) {
		t.Fatalf("CompositeAgentDefinitionV1 fields = %v, want %v", gotDefinition, want)
	}

	memberFields := reflect.TypeOf(CompositeAgentMemberV1{})
	gotMember := make([]string, memberFields.NumField())
	for index := range gotMember {
		gotMember[index] = memberFields.Field(index).Name
	}
	if want := []string{
		"SlotID",
		"AgentID",
		"ProfileID",
		"FocusID",
		"WeightBasisPoints",
		"TargetWorkspaceID",
	}; !reflect.DeepEqual(gotMember, want) {
		t.Fatalf("CompositeAgentMemberV1 fields = %v, want %v", gotMember, want)
	}
}

func TestCompositeDecisionIsExplicitOptionalCanonicalAndDefensivelyCloned(
	t *testing.T,
) {
	input := validControlSnapshot()
	input.CompositeAgents = []CompositeAgentDefinitionV1{
		cloneCompositeAgentDefinition(validCompositeAgentDefinitions()[0]),
	}
	input.CompositeAgents[0].Reviewer = &CompositeReviewerDefinitionV1{
		SchemaVersion:   CompositeReviewerSchemaVersionV1,
		AgentID:         "agent.a",
		ProfileID:       "profile.a",
		MaxOutputTokens: 512,
		Policy:          CompositeReviewerPolicyResultsGateV1,
	}

	legacy, legacyRef, legacyCanonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacyCanonical, []byte(`"decision"`)) {
		t.Fatalf("Decision=nil changed the legacy Reviewer wire: %s", legacyCanonical)
	}
	type legacyCompositeDefinition struct {
		SchemaVersion        string                         `json:"schema_version"`
		AgentID              string                         `json:"agent_id"`
		CoordinatorProfileID string                         `json:"coordinator_profile_id"`
		Members              []CompositeAgentMemberV1       `json:"members"`
		Reviewer             *CompositeReviewerDefinitionV1 `json:"reviewer,omitempty"`
	}
	legacyDefinitions := make([]legacyCompositeDefinition, len(legacy.CompositeAgents))
	for index, definition := range legacy.CompositeAgents {
		legacyDefinitions[index] = legacyCompositeDefinition{
			SchemaVersion:        definition.SchemaVersion,
			AgentID:              definition.AgentID,
			CoordinatorProfileID: definition.CoordinatorProfileID,
			Members:              definition.Members,
			Reviewer:             definition.Reviewer,
		}
	}
	legacyShapeCanonical, err := canonicalJSON(struct {
		SchemaVersion   string                      `json:"schema_version"`
		SnapshotID      string                      `json:"snapshot_id"`
		TenantID        string                      `json:"tenant_id"`
		Revision        uint64                      `json:"revision"`
		Agents          []corecontract.AgentRef     `json:"agents"`
		CompositeAgents []legacyCompositeDefinition `json:"composite_agents,omitempty"`
		Workspaces      []WorkspaceDefinition       `json:"workspaces"`
		Profiles        []ProfileDefinition         `json:"profiles"`
		Digest          string                      `json:"digest,omitempty"`
	}{
		SchemaVersion:   legacy.SchemaVersion,
		SnapshotID:      legacy.SnapshotID,
		TenantID:        legacy.TenantID,
		Revision:        legacy.Revision,
		Agents:          legacy.Agents,
		CompositeAgents: legacyDefinitions,
		Workspaces:      legacy.Workspaces,
		Profiles:        legacy.Profiles,
		Digest:          legacy.Digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacyCanonical, legacyShapeCanonical) {
		t.Fatalf(
			"Decision=nil changed legacy canonical bytes\ngot:  %s\nwant: %s",
			legacyCanonical,
			legacyShapeCanonical,
		)
	}
	if _, err := RestoreControlSnapshot(legacyCanonical, legacyRef); err != nil {
		t.Fatalf("restore legacy Reviewer Control: %v", err)
	}

	input.CompositeAgents[0].Decision = &CompositeDecisionDefinitionV1{
		SchemaVersion: CompositeDecisionSchemaVersionV1,
	}
	frozen, ref, canonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`"decision":{"schema_version":"composite-decision/v1"}`)) {
		t.Fatalf("explicit Decision absent from Control wire: %s", canonical)
	}
	definition, found := frozen.FindCompositeAgent(input.CompositeAgents[0].AgentID)
	if !found || definition.Decision == nil ||
		definition.Decision.SchemaVersion != CompositeDecisionSchemaVersionV1 {
		t.Fatalf("frozen Decision=%+v found=%v", definition.Decision, found)
	}
	input.CompositeAgents[0].Decision.SchemaVersion = "mutated-input"
	definition.Decision.SchemaVersion = "mutated-lookup"
	again, _ := frozen.FindCompositeAgent(definition.AgentID)
	if again.Decision == nil ||
		again.Decision.SchemaVersion != CompositeDecisionSchemaVersionV1 {
		t.Fatalf("Decision aliases input or lookup: %+v", again.Decision)
	}
	restored, err := RestoreControlSnapshot(canonical, ref)
	if err != nil || !reflect.DeepEqual(restored, frozen) {
		t.Fatalf("Decision Control round trip error=%v", err)
	}
}

func TestControlSnapshotRejectsInvalidCompositeDecision(t *testing.T) {
	valid := func() ControlSnapshot {
		input := validControlSnapshot()
		input.CompositeAgents = []CompositeAgentDefinitionV1{
			cloneCompositeAgentDefinition(validCompositeAgentDefinitions()[0]),
		}
		input.CompositeAgents[0].Reviewer = &CompositeReviewerDefinitionV1{
			SchemaVersion:   CompositeReviewerSchemaVersionV1,
			AgentID:         "agent.a",
			ProfileID:       "profile.a",
			MaxOutputTokens: 512,
			Policy:          CompositeReviewerPolicyResultsGateV1,
		}
		input.CompositeAgents[0].Decision = &CompositeDecisionDefinitionV1{
			SchemaVersion: CompositeDecisionSchemaVersionV1,
		}
		return input
	}

	wrongSchema := valid()
	wrongSchema.CompositeAgents[0].Decision.SchemaVersion = "composite-decision/v2"
	if _, _, _, err := NewControlSnapshot(wrongSchema); err == nil {
		t.Fatal("Decision with unknown schema was accepted")
	}
	withoutReviewer := valid()
	withoutReviewer.CompositeAgents[0].Reviewer = nil
	if _, _, _, err := NewControlSnapshot(withoutReviewer); err == nil {
		t.Fatal("Decision without Reviewer was accepted")
	}
}

func TestCompositeReviewerIsOptionalCanonicalAndDefensivelyCloned(t *testing.T) {
	input := validControlSnapshot()
	input.CompositeAgents = validCompositeAgentDefinitions()
	_, _, disabledCanonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(disabledCanonical, []byte(`"reviewer"`)) {
		t.Fatalf("Reviewer-disabled Control changed its wire: %s", disabledCanonical)
	}

	input.CompositeAgents[0].Reviewer = &CompositeReviewerDefinitionV1{
		SchemaVersion:   CompositeReviewerSchemaVersionV1,
		AgentID:         "agent.a",
		ProfileID:       "profile.a",
		MaxOutputTokens: 512,
		Policy:          CompositeReviewerPolicyResultsGateV1,
	}
	frozen, ref, canonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	definition, found := frozen.FindCompositeAgent("agent.z")
	if !found || definition.Reviewer == nil ||
		definition.Reviewer.MaxOutputTokens != 512 {
		t.Fatalf("frozen Reviewer = %+v", definition.Reviewer)
	}
	input.CompositeAgents[0].Reviewer.MaxOutputTokens = 1
	definition.Reviewer.MaxOutputTokens = 2
	again, _ := frozen.FindCompositeAgent("agent.z")
	if again.Reviewer == nil || again.Reviewer.MaxOutputTokens != 512 {
		t.Fatalf("Reviewer aliases input or lookup: %+v", again.Reviewer)
	}
	restored, err := RestoreControlSnapshot(canonical, ref)
	if err != nil || !reflect.DeepEqual(restored, frozen) {
		t.Fatalf("Reviewer Control round trip error=%v", err)
	}
}

func TestControlSnapshotRejectsInvalidCompositeReviewer(t *testing.T) {
	valid := func() ControlSnapshot {
		input := validControlSnapshot()
		input.CompositeAgents = []CompositeAgentDefinitionV1{
			cloneCompositeAgentDefinition(validCompositeAgentDefinitions()[0]),
		}
		input.CompositeAgents[0].Reviewer = &CompositeReviewerDefinitionV1{
			SchemaVersion:   CompositeReviewerSchemaVersionV1,
			AgentID:         "agent.a",
			ProfileID:       "profile.a",
			MaxOutputTokens: 1024,
			Policy:          CompositeReviewerPolicyResultsGateV1,
		}
		return input
	}
	for _, test := range []struct {
		name   string
		mutate func(*ControlSnapshot)
	}{
		{"schema", func(v *ControlSnapshot) { v.CompositeAgents[0].Reviewer.SchemaVersion = "composite-reviewer/v2" }},
		{"zero tokens", func(v *ControlSnapshot) { v.CompositeAgents[0].Reviewer.MaxOutputTokens = 0 }},
		{"too many tokens", func(v *ControlSnapshot) { v.CompositeAgents[0].Reviewer.MaxOutputTokens = 1025 }},
		{"policy", func(v *ControlSnapshot) { v.CompositeAgents[0].Reviewer.Policy = "AUTO" }},
		{"absent agent", func(v *ControlSnapshot) { v.CompositeAgents[0].Reviewer.AgentID = "agent.absent" }},
		{"absent profile", func(v *ControlSnapshot) { v.CompositeAgents[0].Reviewer.ProfileID = "profile.absent" }},
		{"reserved Specialist slot", func(v *ControlSnapshot) {
			v.CompositeAgents[0].Members[0].SlotID = corecontract.CompositeReviewerParentSlotIDV1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := valid()
			test.mutate(&input)
			if _, _, _, err := NewControlSnapshot(input); err == nil {
				t.Fatal("invalid Reviewer was accepted")
			}
		})
	}
}

func TestControlSnapshotRejectsInvalidCompositeAgents(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ControlSnapshot)
	}{
		{
			name: "schema version",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].SchemaVersion = "composite-agent/v2"
			},
		},
		{
			name: "invalid root Agent ID",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].AgentID = " agent.z"
			},
		},
		{
			name: "invalid coordinator Profile ID",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].CoordinatorProfileID = ""
			},
		},
		{
			name: "one member",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].Members = value.CompositeAgents[0].Members[:1]
			},
		},
		{
			name: "nine members",
			mutate: func(value *ControlSnapshot) {
				member := value.CompositeAgents[0].Members[0]
				for len(value.CompositeAgents[0].Members) < 9 {
					value.CompositeAgents[0].Members = append(
						value.CompositeAgents[0].Members,
						member,
					)
				}
			},
		},
		{
			name: "invalid member field",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].Members[0].FocusID = ""
			},
		},
		{
			name: "duplicate slot",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].Members[1].SlotID =
					value.CompositeAgents[0].Members[0].SlotID
			},
		},
		{
			name: "duplicate focus",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].Members[1].FocusID =
					value.CompositeAgents[0].Members[0].FocusID
			},
		},
		{
			name: "zero weight",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].Members[0].WeightBasisPoints = 0
			},
		},
		{
			name: "weight above 10000",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].Members[0].WeightBasisPoints = 10001
			},
		},
		{
			name: "weight sum",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].Members[0].WeightBasisPoints--
			},
		},
		{
			name: "duplicate root Agent",
			mutate: func(value *ControlSnapshot) {
				duplicate := cloneCompositeAgentDefinition(value.CompositeAgents[0])
				value.CompositeAgents = append(value.CompositeAgents, duplicate)
			},
		},
		{
			name: "absent root Agent",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].AgentID = "agent.absent"
			},
		},
		{
			name: "absent member Agent",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].Members[0].AgentID = "agent.absent"
			},
		},
		{
			name: "absent coordinator Profile",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].CoordinatorProfileID = "profile.absent"
			},
		},
		{
			name: "absent member Profile",
			mutate: func(value *ControlSnapshot) {
				value.CompositeAgents[0].Members[0].ProfileID = "profile.absent"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validControlSnapshot()
			input.CompositeAgents = []CompositeAgentDefinitionV1{
				cloneCompositeAgentDefinition(validCompositeAgentDefinitions()[0]),
			}
			test.mutate(&input)
			if _, _, _, err := NewControlSnapshot(input); err == nil {
				t.Fatal("NewControlSnapshot() error = nil")
			}
		})
	}
}

func TestRestoreControlSnapshotRejectsCompositeTamperAndOrder(t *testing.T) {
	input := validControlSnapshot()
	input.CompositeAgents = validCompositeAgentDefinitions()
	_, ref, canonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatalf("NewControlSnapshot() error = %v", err)
	}

	unknown := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		definitions := value["composite_agents"].([]any)
		definitions[0].(map[string]any)["workspace_id"] = "forbidden"
	})
	if _, err := RestoreControlSnapshot(unknown, ref); err == nil {
		t.Fatal("composite definition with Workspace binding was accepted")
	}

	unsortedDefinitions := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		definitions := value["composite_agents"].([]any)
		definitions[0], definitions[1] = definitions[1], definitions[0]
	})
	if _, err := RestoreControlSnapshot(unsortedDefinitions, ref); err == nil {
		t.Fatal("noncanonical composite Agent order was accepted")
	}

	unsortedMembers := mutateCanonicalObject(t, canonical, func(value map[string]any) {
		definitions := value["composite_agents"].([]any)
		members := definitions[1].(map[string]any)["members"].([]any)
		members[0], members[1] = members[1], members[0]
	})
	if _, err := RestoreControlSnapshot(unsortedMembers, ref); err == nil {
		t.Fatal("noncanonical composite member order was accepted")
	}
}

func validCompositeAgentDefinitions() []CompositeAgentDefinitionV1 {
	return []CompositeAgentDefinitionV1{
		{
			SchemaVersion:        CompositeAgentSchemaVersionV1,
			AgentID:              "agent.z",
			CoordinatorProfileID: "profile.z",
			Members: []CompositeAgentMemberV1{
				{
					SlotID:            "slot.z",
					AgentID:           "agent.z",
					ProfileID:         "profile.z",
					FocusID:           "frontend",
					WeightBasisPoints: 3000,
				},
				{
					SlotID:            "slot.a",
					AgentID:           "agent.a",
					ProfileID:         "profile.a",
					FocusID:           "backend",
					WeightBasisPoints: 7000,
				},
			},
		},
		{
			SchemaVersion:        CompositeAgentSchemaVersionV1,
			AgentID:              "agent.a",
			CoordinatorProfileID: "profile.a",
			Members: []CompositeAgentMemberV1{
				{
					SlotID:            "slot.network",
					AgentID:           "agent.z",
					ProfileID:         "profile.z",
					FocusID:           "networking",
					WeightBasisPoints: 4000,
				},
				{
					SlotID:            "slot.system",
					AgentID:           "agent.a",
					ProfileID:         "profile.a",
					FocusID:           "architecture",
					WeightBasisPoints: 6000,
				},
			},
		},
	}
}
