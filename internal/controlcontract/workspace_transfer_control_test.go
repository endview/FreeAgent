package controlcontract

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestWorkspaceTransferControlOptionalPreservesLegacyCanonicalBytes(
	t *testing.T,
) {
	input := validControlSnapshot()
	input.CompositeAgents = validCompositeAgentDefinitions()
	legacy, legacyRef, legacyCanonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(legacyCanonical, []byte(`"transfer_grants"`)) ||
		bytes.Contains(legacyCanonical, []byte(`"target_workspace_id"`)) {
		t.Fatalf("legacy Control exposes transfer fields: %s", legacyCanonical)
	}

	type legacyWorkspaceDefinition struct {
		Workspace         corecontract.WorkspaceRef   `json:"workspace"`
		BudgetPolicy      corecontract.PolicyRef      `json:"budget_policy"`
		ChannelEndpoints  []ChannelEndpointDefinition `json:"channel_endpoints,omitempty"`
		ChannelIdentities []ChannelIdentityDefinition `json:"channel_identities,omitempty"`
	}
	type legacyCompositeMember struct {
		SlotID            string `json:"slot_id"`
		AgentID           string `json:"agent_id"`
		ProfileID         string `json:"profile_id"`
		FocusID           string `json:"focus_id"`
		WeightBasisPoints uint32 `json:"weight_basis_points"`
	}
	type legacyCompositeDefinition struct {
		SchemaVersion        string                         `json:"schema_version"`
		AgentID              string                         `json:"agent_id"`
		CoordinatorProfileID string                         `json:"coordinator_profile_id"`
		Members              []legacyCompositeMember        `json:"members"`
		Reviewer             *CompositeReviewerDefinitionV1 `json:"reviewer,omitempty"`
		Decision             *CompositeDecisionDefinitionV1 `json:"decision,omitempty"`
	}
	legacyWorkspaces := make([]legacyWorkspaceDefinition, len(legacy.Workspaces))
	for index, workspace := range legacy.Workspaces {
		legacyWorkspaces[index] = legacyWorkspaceDefinition{
			Workspace:         workspace.Workspace,
			BudgetPolicy:      workspace.BudgetPolicy,
			ChannelEndpoints:  workspace.ChannelEndpoints,
			ChannelIdentities: workspace.ChannelIdentities,
		}
	}
	legacyDefinitions := make(
		[]legacyCompositeDefinition,
		len(legacy.CompositeAgents),
	)
	for index, definition := range legacy.CompositeAgents {
		members := make([]legacyCompositeMember, len(definition.Members))
		for memberIndex, member := range definition.Members {
			members[memberIndex] = legacyCompositeMember{
				SlotID:            member.SlotID,
				AgentID:           member.AgentID,
				ProfileID:         member.ProfileID,
				FocusID:           member.FocusID,
				WeightBasisPoints: member.WeightBasisPoints,
			}
		}
		legacyDefinitions[index] = legacyCompositeDefinition{
			SchemaVersion:        definition.SchemaVersion,
			AgentID:              definition.AgentID,
			CoordinatorProfileID: definition.CoordinatorProfileID,
			Members:              members,
			Reviewer:             definition.Reviewer,
			Decision:             definition.Decision,
		}
	}
	legacyShape, err := canonicalJSON(struct {
		SchemaVersion   string                      `json:"schema_version"`
		SnapshotID      string                      `json:"snapshot_id"`
		TenantID        string                      `json:"tenant_id"`
		Revision        uint64                      `json:"revision"`
		Agents          []corecontract.AgentRef     `json:"agents"`
		CompositeAgents []legacyCompositeDefinition `json:"composite_agents,omitempty"`
		Workspaces      []legacyWorkspaceDefinition `json:"workspaces"`
		Profiles        []ProfileDefinition         `json:"profiles"`
		Digest          string                      `json:"digest,omitempty"`
	}{
		SchemaVersion:   legacy.SchemaVersion,
		SnapshotID:      legacy.SnapshotID,
		TenantID:        legacy.TenantID,
		Revision:        legacy.Revision,
		Agents:          legacy.Agents,
		CompositeAgents: legacyDefinitions,
		Workspaces:      legacyWorkspaces,
		Profiles:        legacy.Profiles,
		Digest:          legacy.Digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacyCanonical, legacyShape) {
		t.Fatalf(
			"unset Workspace transfer changed legacy canonical bytes\ngot:  %s\nwant: %s",
			legacyCanonical,
			legacyShape,
		)
	}

	empty := input
	empty.Workspaces = append([]WorkspaceDefinition(nil), input.Workspaces...)
	for index := range empty.Workspaces {
		empty.Workspaces[index].TransferGrants =
			[]corecontract.WorkspaceTransferGrantV1{}
	}
	_, emptyRef, emptyCanonical, err := NewControlSnapshot(empty)
	if err != nil {
		t.Fatal(err)
	}
	if emptyRef != legacyRef || !bytes.Equal(emptyCanonical, legacyCanonical) {
		t.Fatal("empty transfer grant slices changed the legacy Control identity")
	}
}

func TestWorkspaceTransferControlFreezesFindsAndDefensivelyCopies(t *testing.T) {
	input := validWorkspaceTransferControl()
	frozen, ref, canonical, err := NewControlSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`"transfer_grants"`)) ||
		!bytes.Contains(canonical, []byte(`"target_workspace_id":"workspace.a"`)) {
		t.Fatalf("Workspace transfer configuration absent: %s", canonical)
	}
	root, found := frozen.FindWorkspace("workspace.z")
	if !found {
		t.Fatal("root Workspace absent")
	}
	target, found := frozen.FindWorkspace("workspace.a")
	if !found {
		t.Fatal("target Workspace absent")
	}
	grant, found := root.FindTransferGrantForPeer(target.Workspace)
	if !found || grant.GrantID != "grant-root-target" {
		t.Fatalf("root grant=%+v found=%v", grant, found)
	}

	input.Workspaces[0].TransferGrants[0].SendPayloadKinds[0] =
		corecontract.WorkspaceTransferPayloadSpecialistResultV1
	grant.SendPayloadKinds[0] = corecontract.WorkspaceTransferPayloadSpecialistResultV1
	again, _ := frozen.FindWorkspace("workspace.z")
	againGrant, found := again.FindTransferGrantForPeer(target.Workspace)
	if !found ||
		againGrant.SendPayloadKinds[0] !=
			corecontract.WorkspaceTransferPayloadTaskSummaryV1 {
		t.Fatal("Workspace transfer grant aliases input or lookup output")
	}
	restored, err := RestoreControlSnapshot(canonical, ref)
	if err != nil || !reflect.DeepEqual(restored, frozen) {
		t.Fatalf("restore Workspace transfer Control error=%v", err)
	}
}

func TestWorkspaceTransferControlRejectsAmbiguityAndBrokenClosure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ControlSnapshot)
	}{
		{
			name: "duplicate owner peer",
			mutate: func(input *ControlSnapshot) {
				duplicate := input.Workspaces[0].TransferGrants[0]
				duplicate.GrantID = "grant-root-target-duplicate"
				input.Workspaces[0].TransferGrants = append(
					input.Workspaces[0].TransferGrants,
					duplicate,
				)
			},
		},
		{
			name: "duplicate global grant ID",
			mutate: func(input *ControlSnapshot) {
				input.Workspaces[1].TransferGrants[0].GrantID =
					input.Workspaces[0].TransferGrants[0].GrantID
			},
		},
		{
			name: "tenant mismatch",
			mutate: func(input *ControlSnapshot) {
				input.Workspaces[0].TransferGrants[0].TenantID = "tenant-stale"
			},
		},
		{
			name: "owner mismatch",
			mutate: func(input *ControlSnapshot) {
				input.Workspaces[0].TransferGrants[0].Workspace =
					input.Workspaces[1].Workspace
				input.Workspaces[0].TransferGrants[0].PeerWorkspace =
					input.Workspaces[0].Workspace
			},
		},
		{
			name: "stale peer ref",
			mutate: func(input *ControlSnapshot) {
				input.Workspaces[0].TransferGrants[0].PeerWorkspace.Digest = hash("0")
			},
		},
		{
			name: "missing member target",
			mutate: func(input *ControlSnapshot) {
				input.CompositeAgents[0].Members[0].TargetWorkspaceID =
					"workspace.missing"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validWorkspaceTransferControl()
			test.mutate(&input)
			if _, _, _, err := NewControlSnapshot(input); err == nil {
				t.Fatal("broken Workspace transfer Control was accepted")
			}
		})
	}
}

func validWorkspaceTransferControl() ControlSnapshot {
	input := validControlSnapshot()
	input.CompositeAgents = validCompositeAgentDefinitions()
	root := input.Workspaces[0].Workspace
	target := input.Workspaces[1].Workspace
	input.Workspaces[0].TransferGrants = []corecontract.WorkspaceTransferGrantV1{
		workspaceTransferControlGrant(
			"grant-root-target",
			input.TenantID,
			root,
			target,
			true,
		),
	}
	input.Workspaces[1].TransferGrants = []corecontract.WorkspaceTransferGrantV1{
		workspaceTransferControlGrant(
			"grant-target-root",
			input.TenantID,
			target,
			root,
			false,
		),
	}
	input.CompositeAgents[0].Members[0].TargetWorkspaceID = target.ID
	return input
}

func workspaceTransferControlGrant(
	id string,
	tenantID string,
	owner corecontract.WorkspaceRef,
	peer corecontract.WorkspaceRef,
	root bool,
) corecontract.WorkspaceTransferGrantV1 {
	grant := corecontract.WorkspaceTransferGrantV1{
		SchemaVersion: corecontract.WorkspaceTransferGrantSchemaVersionV1,
		GrantID:       id,
		TenantID:      tenantID,
		Workspace:     owner,
		PeerWorkspace: peer,
		Revision:      1,
		Enabled:       true,
	}
	if root {
		grant.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadTaskSummaryV1,
		}
		grant.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadSpecialistResultV1,
		}
		grant.MaxSendPayloadBytes =
			corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1
		grant.MaxReceivePayloadBytes =
			corecontract.WorkspaceTransferMaximumPayloadBytesV1
		return grant
	}
	grant.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
		corecontract.WorkspaceTransferPayloadSpecialistResultV1,
	}
	grant.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
		corecontract.WorkspaceTransferPayloadTaskSummaryV1,
	}
	grant.MaxSendPayloadBytes = corecontract.WorkspaceTransferMaximumPayloadBytesV1
	grant.MaxReceivePayloadBytes =
		corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1
	return grant
}
