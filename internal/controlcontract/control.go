package controlcontract

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// BindingSpec is a consumer-owned request to bind one exact activated
// InstanceID. Provider identity is deliberately absent and can only be
// resolved from the referenced CatalogGeneration.
type BindingSpec struct {
	Port                moduleapi.PortRef       `json:"port"`
	InstanceID          string                  `json:"instance_id"`
	ConfigRef           string                  `json:"config_ref"`
	AuthorityCeilingRef string                  `json:"authority_ceiling_ref"`
	StaticContextRefs   []string                `json:"static_context_refs"`
	FailurePolicy       moduleapi.FailurePolicy `json:"failure_policy"`
}

const (
	ChannelEndpointSchemaVersionV1       = "channel-endpoint/v1"
	CompositeAgentSchemaVersionV1        = "composite-agent/v1"
	CompositeReviewerSchemaVersionV1     = "composite-reviewer/v1"
	CompositeDecisionSchemaVersionV1     = "composite-decision/v1"
	CompositeReviewerPolicyResultsGateV1 = "RESULTS_GATE"
	CompositeReviewerMaxOutputTokensV1   = uint32(1024)
)

// ChannelEndpointDefinition is a Workspace-owned ingress/egress route. Its
// Binding is the sole consumer-owned request for channel.transport/v1; the
// activated provider identity is resolved only from the matching Catalog.
type ChannelEndpointDefinition struct {
	SchemaVersion   string      `json:"schema_version"`
	EndpointID      string      `json:"endpoint_id"`
	Channel         string      `json:"channel"`
	AccountID       string      `json:"account_id"`
	ConversationID  string      `json:"conversation_id"`
	TargetAgentID   string      `json:"target_agent_id"`
	TargetProfileID string      `json:"target_profile_id"`
	CursorScopeKey  string      `json:"cursor_scope_key"`
	Enabled         bool        `json:"enabled"`
	Binding         BindingSpec `json:"binding"`
}

// ChannelIdentityDefinition maps one authenticated external identity into a
// Core principal inside this Workspace. ACLEpoch is frozen and must be
// revalidated by the ingress transaction; Active is deny-only configuration.
type ChannelIdentityDefinition struct {
	Channel        string `json:"channel"`
	AccountID      string `json:"account_id"`
	ExternalUserID string `json:"external_user_id"`
	PrincipalID    string `json:"principal_id"`
	ACLEpoch       uint64 `json:"acl_epoch"`
	Active         bool   `json:"active"`
}

type WorkspaceDefinition struct {
	Workspace         corecontract.WorkspaceRef               `json:"workspace"`
	BudgetPolicy      corecontract.PolicyRef                  `json:"budget_policy"`
	ChannelEndpoints  []ChannelEndpointDefinition             `json:"channel_endpoints,omitempty"`
	ChannelIdentities []ChannelIdentityDefinition             `json:"channel_identities,omitempty"`
	TransferGrants    []corecontract.WorkspaceTransferGrantV1 `json:"transfer_grants,omitempty"`
}

type ProfileDefinition struct {
	Profile          corecontract.ProfileRef       `json:"profile"`
	ModelProfile     *corecontract.ModelProfileRef `json:"model_profile,omitempty"`
	ContextPolicy    corecontract.PolicyRef        `json:"context_policy"`
	CostPolicy       corecontract.PolicyRef        `json:"cost_policy"`
	SchedulingPolicy corecontract.PolicyRef        `json:"scheduling_policy"`
	Bindings         []BindingSpec                 `json:"bindings"`
}

// CompositeAgentMemberV1 is one frozen, weighted member slot selected by a
// composite Agent. Agent and Profile identities are resolved from the same
// ControlSnapshot; Workspace remains a per-Run assembly choice.
type CompositeAgentMemberV1 struct {
	SlotID            string `json:"slot_id"`
	AgentID           string `json:"agent_id"`
	ProfileID         string `json:"profile_id"`
	FocusID           string `json:"focus_id"`
	WeightBasisPoints uint32 `json:"weight_basis_points"`
	TargetWorkspaceID string `json:"target_workspace_id,omitempty"`
}

// CompositeReviewerDefinitionV1 is an optional, Operator-selected review
// gate. It carries no business weight and grants no capability by itself.
type CompositeReviewerDefinitionV1 struct {
	SchemaVersion   string `json:"schema_version"`
	AgentID         string `json:"agent_id"`
	ProfileID       string `json:"profile_id"`
	MaxOutputTokens uint32 `json:"max_output_tokens"`
	Policy          string `json:"policy"`
}

// CompositeDecisionDefinitionV1 is an explicit, deliberately parameter-free
// opt-in to the W5 structured decision protocol. Version one always freezes
// exactly one possible repair round; Reviewer presence alone remains the
// legacy S3-B review gate and never enables repair implicitly.
type CompositeDecisionDefinitionV1 struct {
	SchemaVersion string `json:"schema_version"`
}

// CompositeAgentDefinitionV1 freezes one root Agent's coordinator Profile and
// its bounded member set. Members are canonicalized by SlotID.
type CompositeAgentDefinitionV1 struct {
	SchemaVersion        string                         `json:"schema_version"`
	AgentID              string                         `json:"agent_id"`
	CoordinatorProfileID string                         `json:"coordinator_profile_id"`
	Members              []CompositeAgentMemberV1       `json:"members"`
	Reviewer             *CompositeReviewerDefinitionV1 `json:"reviewer,omitempty"`
	Decision             *CompositeDecisionDefinitionV1 `json:"decision,omitempty"`
}

// ControlSnapshot is tenant-scoped desired configuration. It intentionally
// contains no CatalogGeneration ID or digest, avoiding a reverse digest edge.
type ControlSnapshot struct {
	SchemaVersion   string                       `json:"schema_version"`
	SnapshotID      string                       `json:"snapshot_id"`
	TenantID        string                       `json:"tenant_id"`
	Revision        uint64                       `json:"revision"`
	Agents          []corecontract.AgentRef      `json:"agents"`
	CompositeAgents []CompositeAgentDefinitionV1 `json:"composite_agents,omitempty"`
	Workspaces      []WorkspaceDefinition        `json:"workspaces"`
	Profiles        []ProfileDefinition          `json:"profiles"`
	Digest          string                       `json:"digest,omitempty"`
}

func NewControlSnapshot(
	input ControlSnapshot,
) (ControlSnapshot, ControlSnapshotRef, []byte, error) {
	if input.SchemaVersion != ControlSnapshotSchemaVersionV1 {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil,
			fmt.Errorf(
				"controlcontract: control snapshot schema_version must be %q",
				ControlSnapshotSchemaVersionV1,
			)
	}
	if !validOpaque(input.SnapshotID) {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil,
			fmt.Errorf("controlcontract: invalid control snapshot ID")
	}
	if !validOpaque(input.TenantID) {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil,
			fmt.Errorf("controlcontract: invalid control snapshot tenant ID")
	}
	if input.Revision == 0 {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil,
			fmt.Errorf("controlcontract: control snapshot revision must be positive")
	}

	agents, err := canonicalAgents(input.Agents)
	if err != nil {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil, err
	}
	workspaces, err := canonicalWorkspaces(input.TenantID, input.Workspaces)
	if err != nil {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil, err
	}
	profiles, err := canonicalProfiles(input.Profiles)
	if err != nil {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil, err
	}
	compositeAgents, err := canonicalCompositeAgents(input.CompositeAgents)
	if err != nil {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil, err
	}
	if err := validateCompositeAgentControlClosure(
		agents,
		profiles,
		workspaces,
		compositeAgents,
	); err != nil {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil, err
	}
	if err := validateChannelControlClosure(agents, workspaces, profiles); err != nil {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil, err
	}

	frozen := ControlSnapshot{
		SchemaVersion:   input.SchemaVersion,
		SnapshotID:      input.SnapshotID,
		TenantID:        input.TenantID,
		Revision:        input.Revision,
		Agents:          agents,
		CompositeAgents: compositeAgents,
		Workspaces:      workspaces,
		Profiles:        profiles,
	}
	identityCanonical, err := canonicalJSON(frozen)
	if err != nil {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil, err
	}
	frozen.Digest = moduleapi.Digest(
		controlSnapshotDigestDomain,
		identityCanonical,
	)
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return ControlSnapshot{}, ControlSnapshotRef{}, nil, err
	}
	ref := ControlSnapshotRef{
		SnapshotID: frozen.SnapshotID,
		Revision:   frozen.Revision,
		Digest:     frozen.Digest,
	}
	return cloneControlSnapshot(frozen), ref, bytes.Clone(canonical), nil
}

func RestoreControlSnapshot(
	canonical []byte,
	ref ControlSnapshotRef,
) (ControlSnapshot, error) {
	if err := ref.Validate(); err != nil {
		return ControlSnapshot{}, err
	}
	if err := requireCanonicalObject(canonical); err != nil {
		return ControlSnapshot{}, err
	}
	var decoded ControlSnapshot
	if err := decodeStrict(canonical, &decoded); err != nil {
		return ControlSnapshot{}, err
	}
	rebuilt, rebuiltRef, rebuiltCanonical, err := NewControlSnapshot(decoded)
	if err != nil {
		return ControlSnapshot{}, err
	}
	if rebuiltRef != ref ||
		decoded.Digest != rebuilt.Digest ||
		!bytes.Equal(canonical, rebuiltCanonical) {
		return ControlSnapshot{},
			fmt.Errorf("controlcontract: control snapshot does not match reference")
	}
	return rebuilt, nil
}

func (snapshot ControlSnapshot) FindAgent(id string) (corecontract.AgentRef, bool) {
	for _, agent := range snapshot.Agents {
		if agent.ID == id {
			return agent, true
		}
	}
	return corecontract.AgentRef{}, false
}

// FindCompositeAgent returns a defensive copy of the definition rooted at the
// exact Agent ID.
func (snapshot ControlSnapshot) FindCompositeAgent(
	agentID string,
) (CompositeAgentDefinitionV1, bool) {
	for _, definition := range snapshot.CompositeAgents {
		if definition.AgentID == agentID {
			return cloneCompositeAgentDefinition(definition), true
		}
	}
	return CompositeAgentDefinitionV1{}, false
}

// FindMember returns the member occupying one exact composite slot.
func (definition CompositeAgentDefinitionV1) FindMember(
	slotID string,
) (CompositeAgentMemberV1, bool) {
	for _, member := range definition.Members {
		if member.SlotID == slotID {
			return member, true
		}
	}
	return CompositeAgentMemberV1{}, false
}

func (snapshot ControlSnapshot) FindWorkspace(
	id string,
) (WorkspaceDefinition, bool) {
	for _, workspace := range snapshot.Workspaces {
		if workspace.Workspace.ID == id {
			return cloneWorkspaceDefinition(workspace), true
		}
	}
	return WorkspaceDefinition{}, false
}

func (workspace WorkspaceDefinition) FindChannelEndpoint(
	endpointID string,
) (ChannelEndpointDefinition, bool) {
	for _, endpoint := range workspace.ChannelEndpoints {
		if endpoint.EndpointID == endpointID {
			return cloneChannelEndpointDefinition(endpoint), true
		}
	}
	return ChannelEndpointDefinition{}, false
}

// FindTransferGrantForPeer returns the sole exact grant owned by this
// Workspace for one frozen peer. ControlSnapshot validation rejects ambiguous
// owner/peer pairs before this lookup can be used by the Assembly Compiler.
func (workspace WorkspaceDefinition) FindTransferGrantForPeer(
	peer corecontract.WorkspaceRef,
) (corecontract.WorkspaceTransferGrantV1, bool) {
	for _, grant := range workspace.TransferGrants {
		if grant.PeerWorkspace == peer {
			return cloneWorkspaceTransferGrantV1(grant), true
		}
	}
	return corecontract.WorkspaceTransferGrantV1{}, false
}

func (snapshot ControlSnapshot) FindProfile(
	id string,
) (ProfileDefinition, bool) {
	for _, profile := range snapshot.Profiles {
		if profile.Profile.ID == id {
			return cloneProfileDefinition(profile), true
		}
	}
	return ProfileDefinition{}, false
}

func canonicalAgents(
	input []corecontract.AgentRef,
) ([]corecontract.AgentRef, error) {
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"controlcontract: agent list may contain at most %d entries",
			moduleapi.MaxManifestEntries,
		)
	}
	agents := make([]corecontract.AgentRef, len(input))
	copy(agents, input)
	seen := make(map[string]struct{}, len(agents))
	for index, agent := range agents {
		if err := agent.Validate(); err != nil {
			return nil, fmt.Errorf("controlcontract: agent %d: %w", index, err)
		}
		if _, duplicate := seen[agent.ID]; duplicate {
			return nil, fmt.Errorf(
				"controlcontract: duplicate agent ID %q",
				agent.ID,
			)
		}
		seen[agent.ID] = struct{}{}
	}
	sort.Slice(agents, func(left, right int) bool {
		return lessTypedRef(
			agents[left].ID,
			agents[left].Version,
			agents[left].Digest,
			agents[right].ID,
			agents[right].Version,
			agents[right].Digest,
		)
	})
	return agents, nil
}

func canonicalCompositeAgents(
	input []CompositeAgentDefinitionV1,
) ([]CompositeAgentDefinitionV1, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"controlcontract: composite agent list may contain at most %d entries",
			moduleapi.MaxManifestEntries,
		)
	}
	definitions := make([]CompositeAgentDefinitionV1, len(input))
	seenAgents := make(map[string]struct{}, len(input))
	for index, definition := range input {
		if definition.SchemaVersion != CompositeAgentSchemaVersionV1 {
			return nil, fmt.Errorf(
				"controlcontract: composite agent %d schema_version must be %q",
				index,
				CompositeAgentSchemaVersionV1,
			)
		}
		if !validOpaque(definition.AgentID) {
			return nil, fmt.Errorf(
				"controlcontract: composite agent %d has invalid agent ID",
				index,
			)
		}
		if !validOpaque(definition.CoordinatorProfileID) {
			return nil, fmt.Errorf(
				"controlcontract: composite agent %d has invalid coordinator profile ID",
				index,
			)
		}
		if _, duplicate := seenAgents[definition.AgentID]; duplicate {
			return nil, fmt.Errorf(
				"controlcontract: duplicate composite root agent ID %q",
				definition.AgentID,
			)
		}
		seenAgents[definition.AgentID] = struct{}{}
		if definition.Reviewer != nil {
			reviewer := *definition.Reviewer
			if reviewer.SchemaVersion != CompositeReviewerSchemaVersionV1 ||
				!validOpaque(reviewer.AgentID) ||
				!validOpaque(reviewer.ProfileID) ||
				reviewer.MaxOutputTokens == 0 ||
				reviewer.MaxOutputTokens > CompositeReviewerMaxOutputTokensV1 ||
				reviewer.Policy != CompositeReviewerPolicyResultsGateV1 {
				return nil, fmt.Errorf(
					"controlcontract: composite agent %q has invalid Reviewer definition",
					definition.AgentID,
				)
			}
			definition.Reviewer = &reviewer
		}
		if definition.Decision != nil {
			decision := *definition.Decision
			if definition.Reviewer == nil ||
				decision.SchemaVersion != CompositeDecisionSchemaVersionV1 {
				return nil, fmt.Errorf(
					"controlcontract: composite agent %q has an invalid decision protocol",
					definition.AgentID,
				)
			}
			definition.Decision = &decision
		}
		if len(definition.Members) < 2 || len(definition.Members) > 8 {
			return nil, fmt.Errorf(
				"controlcontract: composite agent %q requires between 2 and 8 members",
				definition.AgentID,
			)
		}

		members := make([]CompositeAgentMemberV1, len(definition.Members))
		seenSlots := make(map[string]struct{}, len(definition.Members))
		seenFocus := make(map[string]struct{}, len(definition.Members))
		var totalWeight uint64
		for memberIndex, member := range definition.Members {
			for name, value := range map[string]string{
				"slot ID":    member.SlotID,
				"agent ID":   member.AgentID,
				"profile ID": member.ProfileID,
				"focus ID":   member.FocusID,
			} {
				if !validOpaque(value) {
					return nil, fmt.Errorf(
						"controlcontract: composite agent %q member %d has invalid %s",
						definition.AgentID,
						memberIndex,
						name,
					)
				}
			}
			if member.TargetWorkspaceID != "" &&
				!validOpaque(member.TargetWorkspaceID) {
				return nil, fmt.Errorf(
					"controlcontract: composite agent %q member %d has invalid target Workspace ID",
					definition.AgentID,
					memberIndex,
				)
			}
			if member.SlotID == corecontract.CompositeReviewerParentSlotIDV1 {
				return nil, fmt.Errorf(
					"controlcontract: composite agent %q member %d uses the reserved Reviewer slot",
					definition.AgentID,
					memberIndex,
				)
			}
			if _, duplicate := seenSlots[member.SlotID]; duplicate {
				return nil, fmt.Errorf(
					"controlcontract: composite agent %q has duplicate slot ID %q",
					definition.AgentID,
					member.SlotID,
				)
			}
			seenSlots[member.SlotID] = struct{}{}
			if _, duplicate := seenFocus[member.FocusID]; duplicate {
				return nil, fmt.Errorf(
					"controlcontract: composite agent %q has duplicate focus ID %q",
					definition.AgentID,
					member.FocusID,
				)
			}
			seenFocus[member.FocusID] = struct{}{}
			if member.WeightBasisPoints == 0 || member.WeightBasisPoints > 10000 {
				return nil, fmt.Errorf(
					"controlcontract: composite agent %q member %q weight must be between 1 and 10000 basis points",
					definition.AgentID,
					member.SlotID,
				)
			}
			totalWeight += uint64(member.WeightBasisPoints)
			members[memberIndex] = member
		}
		if totalWeight != 10000 {
			return nil, fmt.Errorf(
				"controlcontract: composite agent %q member weights total %d basis points, want 10000",
				definition.AgentID,
				totalWeight,
			)
		}
		sort.Slice(members, func(left, right int) bool {
			return compareText(members[left].SlotID, members[right].SlotID) < 0
		})
		definition.Members = members
		definitions[index] = definition
	}
	sort.Slice(definitions, func(left, right int) bool {
		return compareText(
			definitions[left].AgentID,
			definitions[right].AgentID,
		) < 0
	})
	return definitions, nil
}

func validateCompositeAgentControlClosure(
	agents []corecontract.AgentRef,
	profiles []ProfileDefinition,
	workspaces []WorkspaceDefinition,
	definitions []CompositeAgentDefinitionV1,
) error {
	agentIDs := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		agentIDs[agent.ID] = struct{}{}
	}
	profileIDs := make(map[string]struct{}, len(profiles))
	for _, profile := range profiles {
		profileIDs[profile.Profile.ID] = struct{}{}
	}
	workspaceIDs := make(map[string]struct{}, len(workspaces))
	for _, workspace := range workspaces {
		workspaceIDs[workspace.Workspace.ID] = struct{}{}
	}
	for _, definition := range definitions {
		if _, present := agentIDs[definition.AgentID]; !present {
			return fmt.Errorf(
				"controlcontract: composite root agent %q is absent",
				definition.AgentID,
			)
		}
		if _, present := profileIDs[definition.CoordinatorProfileID]; !present {
			return fmt.Errorf(
				"controlcontract: composite agent %q coordinator profile %q is absent",
				definition.AgentID,
				definition.CoordinatorProfileID,
			)
		}
		if definition.Reviewer != nil {
			if _, present := agentIDs[definition.Reviewer.AgentID]; !present {
				return fmt.Errorf(
					"controlcontract: composite agent %q Reviewer agent %q is absent",
					definition.AgentID,
					definition.Reviewer.AgentID,
				)
			}
			if _, present := profileIDs[definition.Reviewer.ProfileID]; !present {
				return fmt.Errorf(
					"controlcontract: composite agent %q Reviewer profile %q is absent",
					definition.AgentID,
					definition.Reviewer.ProfileID,
				)
			}
		}
		for _, member := range definition.Members {
			if _, present := agentIDs[member.AgentID]; !present {
				return fmt.Errorf(
					"controlcontract: composite agent %q member slot %q agent %q is absent",
					definition.AgentID,
					member.SlotID,
					member.AgentID,
				)
			}
			if _, present := profileIDs[member.ProfileID]; !present {
				return fmt.Errorf(
					"controlcontract: composite agent %q member slot %q profile %q is absent",
					definition.AgentID,
					member.SlotID,
					member.ProfileID,
				)
			}
			if member.TargetWorkspaceID != "" {
				if _, present := workspaceIDs[member.TargetWorkspaceID]; !present {
					return fmt.Errorf(
						"controlcontract: composite agent %q member slot %q target Workspace %q is absent",
						definition.AgentID,
						member.SlotID,
						member.TargetWorkspaceID,
					)
				}
			}
		}
	}
	return nil
}

func canonicalWorkspaces(
	tenantID string,
	input []WorkspaceDefinition,
) ([]WorkspaceDefinition, error) {
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"controlcontract: workspace list may contain at most %d entries",
			moduleapi.MaxManifestEntries,
		)
	}
	workspaces := make([]WorkspaceDefinition, len(input))
	copy(workspaces, input)
	seen := make(map[string]struct{}, len(workspaces))
	workspaceRefs := make(
		map[string]corecontract.WorkspaceRef,
		len(workspaces),
	)
	for index, workspace := range workspaces {
		if err := workspace.Workspace.Validate(); err != nil {
			return nil, fmt.Errorf(
				"controlcontract: workspace %d: %w",
				index,
				err,
			)
		}
		if err := workspace.BudgetPolicy.Validate(); err != nil {
			return nil, fmt.Errorf(
				"controlcontract: workspace %d budget policy: %w",
				index,
				err,
			)
		}
		if _, duplicate := seen[workspace.Workspace.ID]; duplicate {
			return nil, fmt.Errorf(
				"controlcontract: duplicate workspace ID %q",
				workspace.Workspace.ID,
			)
		}
		seen[workspace.Workspace.ID] = struct{}{}
		workspaceRefs[workspace.Workspace.ID] = workspace.Workspace
	}
	seenGrantIDs := make(map[string]struct{})
	for index, workspace := range workspaces {
		endpoints, err := canonicalChannelEndpoints(workspace.ChannelEndpoints)
		if err != nil {
			return nil, fmt.Errorf(
				"controlcontract: workspace %d: %w",
				index,
				err,
			)
		}
		identities, err := canonicalChannelIdentities(workspace.ChannelIdentities)
		if err != nil {
			return nil, fmt.Errorf(
				"controlcontract: workspace %d: %w",
				index,
				err,
			)
		}
		workspace.ChannelEndpoints = endpoints
		workspace.ChannelIdentities = identities
		grants, err := canonicalWorkspaceTransferGrants(
			tenantID,
			workspace.Workspace,
			workspaceRefs,
			workspace.TransferGrants,
			seenGrantIDs,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"controlcontract: workspace %d: %w",
				index,
				err,
			)
		}
		workspace.TransferGrants = grants
		workspaces[index] = workspace
	}
	sort.Slice(workspaces, func(left, right int) bool {
		return lessTypedRef(
			workspaces[left].Workspace.ID,
			workspaces[left].Workspace.Version,
			workspaces[left].Workspace.Digest,
			workspaces[right].Workspace.ID,
			workspaces[right].Workspace.Version,
			workspaces[right].Workspace.Digest,
		)
	})
	return workspaces, nil
}

func canonicalWorkspaceTransferGrants(
	tenantID string,
	owner corecontract.WorkspaceRef,
	workspaceRefs map[string]corecontract.WorkspaceRef,
	input []corecontract.WorkspaceTransferGrantV1,
	seenGrantIDs map[string]struct{},
) ([]corecontract.WorkspaceTransferGrantV1, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"Workspace transfer grant list may contain at most %d entries",
			moduleapi.MaxManifestEntries,
		)
	}
	grants := make([]corecontract.WorkspaceTransferGrantV1, len(input))
	seenPeers := make(map[string]struct{}, len(input))
	for index, grant := range input {
		frozen, _, _, err := corecontract.NewWorkspaceTransferGrantV1(grant)
		if err != nil {
			return nil, fmt.Errorf(
				"Workspace transfer grant %d: %w",
				index,
				err,
			)
		}
		peer, present := workspaceRefs[frozen.PeerWorkspace.ID]
		if frozen.TenantID != tenantID || frozen.Workspace != owner ||
			!present || frozen.PeerWorkspace != peer {
			return nil, fmt.Errorf(
				"Workspace transfer grant %q does not close to the owning Control Workspaces",
				frozen.GrantID,
			)
		}
		if _, duplicate := seenPeers[frozen.PeerWorkspace.ID]; duplicate {
			return nil, fmt.Errorf(
				"duplicate Workspace transfer grant for peer %q",
				frozen.PeerWorkspace.ID,
			)
		}
		if _, duplicate := seenGrantIDs[frozen.GrantID]; duplicate {
			return nil, fmt.Errorf(
				"duplicate Workspace transfer grant ID %q",
				frozen.GrantID,
			)
		}
		seenPeers[frozen.PeerWorkspace.ID] = struct{}{}
		seenGrantIDs[frozen.GrantID] = struct{}{}
		grants[index] = frozen
	}
	sort.Slice(grants, func(left, right int) bool {
		if grants[left].PeerWorkspace.ID != grants[right].PeerWorkspace.ID {
			return compareText(
				grants[left].PeerWorkspace.ID,
				grants[right].PeerWorkspace.ID,
			) < 0
		}
		return compareText(grants[left].GrantID, grants[right].GrantID) < 0
	})
	return grants, nil
}

func canonicalChannelEndpoints(
	input []ChannelEndpointDefinition,
) ([]ChannelEndpointDefinition, error) {
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"channel endpoint list may contain at most %d entries",
			moduleapi.MaxManifestEntries,
		)
	}
	endpoints := make([]ChannelEndpointDefinition, len(input))
	seenIDs := make(map[string]struct{}, len(input))
	seenEnabledRoutes := make(map[string]struct{}, len(input))
	for index, endpoint := range input {
		if endpoint.SchemaVersion != ChannelEndpointSchemaVersionV1 {
			return nil, fmt.Errorf(
				"channel endpoint %d schema_version must be %q",
				index,
				ChannelEndpointSchemaVersionV1,
			)
		}
		for name, value := range map[string]string{
			"endpoint_id":       endpoint.EndpointID,
			"channel":           endpoint.Channel,
			"account_id":        endpoint.AccountID,
			"conversation_id":   endpoint.ConversationID,
			"target_agent_id":   endpoint.TargetAgentID,
			"target_profile_id": endpoint.TargetProfileID,
			"cursor_scope_key":  endpoint.CursorScopeKey,
		} {
			if !validOpaque(value) {
				return nil, fmt.Errorf(
					"channel endpoint %d has invalid %s",
					index,
					name,
				)
			}
		}
		if _, duplicate := seenIDs[endpoint.EndpointID]; duplicate {
			return nil, fmt.Errorf(
				"duplicate channel endpoint ID %q",
				endpoint.EndpointID,
			)
		}
		seenIDs[endpoint.EndpointID] = struct{}{}
		if endpoint.Enabled {
			route := endpoint.Channel + "\x00" + endpoint.AccountID +
				"\x00" + endpoint.ConversationID
			if _, duplicate := seenEnabledRoutes[route]; duplicate {
				return nil, fmt.Errorf(
					"duplicate enabled channel route %q/%q/%q",
					endpoint.Channel,
					endpoint.AccountID,
					endpoint.ConversationID,
				)
			}
			seenEnabledRoutes[route] = struct{}{}
		}
		bindings, err := canonicalBindings([]BindingSpec{endpoint.Binding})
		if err != nil {
			return nil, fmt.Errorf(
				"channel endpoint %d binding: %w",
				index,
				err,
			)
		}
		binding := bindings[0]
		if binding.Port != (moduleapi.PortRef{
			Name:         moduleapi.PortNameChannelTransport,
			ExactVersion: moduleapi.PortVersionV1,
		}) || binding.FailurePolicy != moduleapi.FailureRequired ||
			len(binding.StaticContextRefs) != 0 {
			return nil, fmt.Errorf(
				"channel endpoint %d requires one REQUIRED channel.transport/v1 binding without static context",
				index,
			)
		}
		endpoint.Binding = binding
		endpoints[index] = endpoint
	}
	sort.Slice(endpoints, func(left, right int) bool {
		return compareText(endpoints[left].EndpointID, endpoints[right].EndpointID) < 0
	})
	return endpoints, nil
}

func canonicalChannelIdentities(
	input []ChannelIdentityDefinition,
) ([]ChannelIdentityDefinition, error) {
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"channel identity list may contain at most %d entries",
			moduleapi.MaxManifestEntries,
		)
	}
	identities := make([]ChannelIdentityDefinition, len(input))
	seen := make(map[string]struct{}, len(input))
	for index, identity := range input {
		for name, value := range map[string]string{
			"channel":          identity.Channel,
			"account_id":       identity.AccountID,
			"external_user_id": identity.ExternalUserID,
			"principal_id":     identity.PrincipalID,
		} {
			if !validOpaque(value) {
				return nil, fmt.Errorf(
					"channel identity %d has invalid %s",
					index,
					name,
				)
			}
		}
		if identity.ACLEpoch == 0 {
			return nil, fmt.Errorf(
				"channel identity %d acl_epoch must be positive",
				index,
			)
		}
		key := identity.Channel + "\x00" + identity.AccountID +
			"\x00" + identity.ExternalUserID
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf(
				"duplicate channel identity for %q/%q/%q",
				identity.Channel,
				identity.AccountID,
				identity.ExternalUserID,
			)
		}
		seen[key] = struct{}{}
		identities[index] = identity
	}
	sort.Slice(identities, func(left, right int) bool {
		leftKey := identities[left].Channel + "\x00" + identities[left].AccountID +
			"\x00" + identities[left].ExternalUserID
		rightKey := identities[right].Channel + "\x00" + identities[right].AccountID +
			"\x00" + identities[right].ExternalUserID
		return compareText(leftKey, rightKey) < 0
	})
	return identities, nil
}

func validateChannelControlClosure(
	agents []corecontract.AgentRef,
	workspaces []WorkspaceDefinition,
	profiles []ProfileDefinition,
) error {
	agentIDs := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		agentIDs[agent.ID] = struct{}{}
	}
	profileIDs := make(map[string]struct{}, len(profiles))
	for _, profile := range profiles {
		profileIDs[profile.Profile.ID] = struct{}{}
		for _, binding := range profile.Bindings {
			if binding.Port.Name == moduleapi.PortNameChannelTransport &&
				binding.Port.ExactVersion == moduleapi.PortVersionV1 {
				return fmt.Errorf(
					"controlcontract: channel.transport/v1 belongs to a Workspace endpoint, not a Profile",
				)
			}
		}
	}
	seenEndpointIDs := make(map[string]struct{})
	seenEnabledRoutes := make(map[string]struct{})
	for _, workspace := range workspaces {
		activeIdentityAccounts := make(map[string]struct{})
		for _, identity := range workspace.ChannelIdentities {
			if identity.Active {
				activeIdentityAccounts[identity.Channel+"\x00"+identity.AccountID] = struct{}{}
			}
		}
		for _, endpoint := range workspace.ChannelEndpoints {
			if _, duplicate := seenEndpointIDs[endpoint.EndpointID]; duplicate {
				return fmt.Errorf(
					"controlcontract: duplicate tenant channel endpoint ID %q",
					endpoint.EndpointID,
				)
			}
			seenEndpointIDs[endpoint.EndpointID] = struct{}{}
			if _, present := agentIDs[endpoint.TargetAgentID]; !present {
				return fmt.Errorf(
					"controlcontract: channel endpoint %q target agent %q is absent",
					endpoint.EndpointID,
					endpoint.TargetAgentID,
				)
			}
			if _, present := profileIDs[endpoint.TargetProfileID]; !present {
				return fmt.Errorf(
					"controlcontract: channel endpoint %q target profile %q is absent",
					endpoint.EndpointID,
					endpoint.TargetProfileID,
				)
			}
			if !endpoint.Enabled {
				continue
			}
			route := endpoint.Channel + "\x00" + endpoint.AccountID +
				"\x00" + endpoint.ConversationID
			if _, duplicate := seenEnabledRoutes[route]; duplicate {
				return fmt.Errorf(
					"controlcontract: enabled channel route %q/%q/%q is not tenant-unique",
					endpoint.Channel,
					endpoint.AccountID,
					endpoint.ConversationID,
				)
			}
			seenEnabledRoutes[route] = struct{}{}
			identityAccount := endpoint.Channel + "\x00" + endpoint.AccountID
			if _, present := activeIdentityAccounts[identityAccount]; !present {
				return fmt.Errorf(
					"controlcontract: enabled channel endpoint %q has no active identity for its channel/account",
					endpoint.EndpointID,
				)
			}
		}
	}
	return nil
}

func canonicalProfiles(
	input []ProfileDefinition,
) ([]ProfileDefinition, error) {
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"controlcontract: profile list may contain at most %d entries",
			moduleapi.MaxManifestEntries,
		)
	}
	profiles := make([]ProfileDefinition, len(input))
	seen := make(map[string]struct{}, len(input))
	for index, profile := range input {
		if err := profile.Profile.Validate(); err != nil {
			return nil, fmt.Errorf(
				"controlcontract: profile %d: %w",
				index,
				err,
			)
		}
		if profile.ModelProfile != nil {
			if err := profile.ModelProfile.Validate(); err != nil {
				return nil, fmt.Errorf(
					"controlcontract: profile %d model profile: %w",
					index,
					err,
				)
			}
			modelProfile := *profile.ModelProfile
			profile.ModelProfile = &modelProfile
		}
		for name, policy := range map[string]corecontract.PolicyRef{
			"context":    profile.ContextPolicy,
			"cost":       profile.CostPolicy,
			"scheduling": profile.SchedulingPolicy,
		} {
			if err := policy.Validate(); err != nil {
				return nil, fmt.Errorf(
					"controlcontract: profile %d %s policy: %w",
					index,
					name,
					err,
				)
			}
		}
		if _, duplicate := seen[profile.Profile.ID]; duplicate {
			return nil, fmt.Errorf(
				"controlcontract: duplicate profile ID %q",
				profile.Profile.ID,
			)
		}
		seen[profile.Profile.ID] = struct{}{}
		bindings, err := canonicalBindings(profile.Bindings)
		if err != nil {
			return nil, fmt.Errorf(
				"controlcontract: profile %d: %w",
				index,
				err,
			)
		}
		profile.Bindings = bindings
		profiles[index] = profile
	}
	sort.Slice(profiles, func(left, right int) bool {
		return lessTypedRef(
			profiles[left].Profile.ID,
			profiles[left].Profile.Version,
			profiles[left].Profile.Digest,
			profiles[right].Profile.ID,
			profiles[right].Profile.Version,
			profiles[right].Profile.Digest,
		)
	})
	return profiles, nil
}

func canonicalBindings(input []BindingSpec) ([]BindingSpec, error) {
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"controlcontract: binding list may contain at most %d entries",
			moduleapi.MaxManifestEntries,
		)
	}
	bindings := make([]BindingSpec, len(input))
	seen := make(map[string]struct{}, len(bindings))
	staticContextCount := 0
	for index, binding := range input {
		if err := validateS1Port(binding.Port); err != nil {
			return nil, fmt.Errorf(
				"controlcontract: binding %d port: %w",
				index,
				err,
			)
		}
		if !validOpaque(binding.InstanceID) {
			return nil, fmt.Errorf(
				"controlcontract: binding %d has invalid instance ID",
				index,
			)
		}
		if !moduleapi.ValidSHA256(binding.ConfigRef) {
			return nil, fmt.Errorf(
				"controlcontract: binding %d has invalid config ref",
				index,
			)
		}
		if !moduleapi.ValidSHA256(binding.AuthorityCeilingRef) {
			return nil, fmt.Errorf(
				"controlcontract: binding %d has invalid authority ceiling ref",
				index,
			)
		}
		if len(binding.StaticContextRefs) > moduleapi.MaxManifestEntries {
			return nil, fmt.Errorf(
				"controlcontract: binding %d has too many static context refs",
				index,
			)
		}
		staticContextCount += len(binding.StaticContextRefs)
		if staticContextCount > moduleapi.MaxManifestEntries {
			return nil, fmt.Errorf(
				"controlcontract: binding list has too many static context refs",
			)
		}
		if binding.Port.Name != moduleapi.PortNameContextProvide &&
			len(binding.StaticContextRefs) != 0 {
			return nil, fmt.Errorf(
				"controlcontract: binding %d port %s/%s cannot reference static context",
				index,
				binding.Port.Name,
				binding.Port.ExactVersion,
			)
		}
		staticSeen := make(
			map[string]struct{},
			len(binding.StaticContextRefs),
		)
		for staticIndex, ref := range binding.StaticContextRefs {
			if !moduleapi.ValidSHA256(ref) {
				return nil, fmt.Errorf(
					"controlcontract: binding %d static context ref %d is invalid",
					index,
					staticIndex,
				)
			}
			if _, duplicate := staticSeen[ref]; duplicate {
				return nil, fmt.Errorf(
					"controlcontract: binding %d has duplicate static context ref %s",
					index,
					ref,
				)
			}
			staticSeen[ref] = struct{}{}
		}
		if err := binding.FailurePolicy.Validate(); err != nil {
			return nil, fmt.Errorf(
				"controlcontract: binding %d: %w",
				index,
				err,
			)
		}
		portKey, _ := binding.Port.CanonicalKey()
		key := portKey +
			"\x00" + binding.InstanceID +
			"\x00" + binding.ConfigRef +
			"\x00" + binding.AuthorityCeilingRef +
			"\x00" + string(binding.FailurePolicy)
		for _, ref := range binding.StaticContextRefs {
			key += "\x00" + ref
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf(
				"controlcontract: duplicate binding for port %s/%s and instance %q",
				binding.Port.Name,
				binding.Port.ExactVersion,
				binding.InstanceID,
			)
		}
		seen[key] = struct{}{}
		binding.StaticContextRefs = append(
			[]string{},
			binding.StaticContextRefs...,
		)
		bindings[index] = binding
	}
	// Binding order is semantic and is deliberately not sorted.
	return bindings, nil
}

func cloneProfileDefinition(profile ProfileDefinition) ProfileDefinition {
	if profile.ModelProfile != nil {
		modelProfile := *profile.ModelProfile
		profile.ModelProfile = &modelProfile
	}
	bindings := make([]BindingSpec, len(profile.Bindings))
	for index, binding := range profile.Bindings {
		binding.StaticContextRefs = append(
			[]string{},
			binding.StaticContextRefs...,
		)
		bindings[index] = binding
	}
	profile.Bindings = bindings
	return profile
}

func cloneChannelEndpointDefinition(
	endpoint ChannelEndpointDefinition,
) ChannelEndpointDefinition {
	endpoint.Binding.StaticContextRefs = append(
		[]string{},
		endpoint.Binding.StaticContextRefs...,
	)
	return endpoint
}

func cloneWorkspaceDefinition(
	workspace WorkspaceDefinition,
) WorkspaceDefinition {
	endpoints := make(
		[]ChannelEndpointDefinition,
		len(workspace.ChannelEndpoints),
	)
	for index, endpoint := range workspace.ChannelEndpoints {
		endpoints[index] = cloneChannelEndpointDefinition(endpoint)
	}
	workspace.ChannelEndpoints = endpoints
	workspace.ChannelIdentities = append(
		[]ChannelIdentityDefinition{},
		workspace.ChannelIdentities...,
	)
	if workspace.TransferGrants != nil {
		grants := make(
			[]corecontract.WorkspaceTransferGrantV1,
			len(workspace.TransferGrants),
		)
		for index, grant := range workspace.TransferGrants {
			grants[index] = cloneWorkspaceTransferGrantV1(grant)
		}
		workspace.TransferGrants = grants
	}
	return workspace
}

func cloneWorkspaceTransferGrantV1(
	grant corecontract.WorkspaceTransferGrantV1,
) corecontract.WorkspaceTransferGrantV1 {
	grant.SendPayloadKinds = append(
		[]corecontract.WorkspaceTransferPayloadKindV1(nil),
		grant.SendPayloadKinds...,
	)
	grant.ReceivePayloadKinds = append(
		[]corecontract.WorkspaceTransferPayloadKindV1(nil),
		grant.ReceivePayloadKinds...,
	)
	return grant
}

func cloneCompositeAgentDefinition(
	definition CompositeAgentDefinitionV1,
) CompositeAgentDefinitionV1 {
	definition.Members = append(
		[]CompositeAgentMemberV1(nil),
		definition.Members...,
	)
	if definition.Reviewer != nil {
		reviewer := *definition.Reviewer
		definition.Reviewer = &reviewer
	}
	if definition.Decision != nil {
		decision := *definition.Decision
		definition.Decision = &decision
	}
	return definition
}

func cloneControlSnapshot(snapshot ControlSnapshot) ControlSnapshot {
	agents := make([]corecontract.AgentRef, len(snapshot.Agents))
	copy(agents, snapshot.Agents)
	snapshot.Agents = agents
	if snapshot.CompositeAgents != nil {
		definitions := make(
			[]CompositeAgentDefinitionV1,
			len(snapshot.CompositeAgents),
		)
		for index, definition := range snapshot.CompositeAgents {
			definitions[index] = cloneCompositeAgentDefinition(definition)
		}
		snapshot.CompositeAgents = definitions
	}
	workspaces := make([]WorkspaceDefinition, len(snapshot.Workspaces))
	for index, workspace := range snapshot.Workspaces {
		workspaces[index] = cloneWorkspaceDefinition(workspace)
	}
	snapshot.Workspaces = workspaces
	profiles := make([]ProfileDefinition, len(snapshot.Profiles))
	for index, profile := range snapshot.Profiles {
		profiles[index] = cloneProfileDefinition(profile)
	}
	snapshot.Profiles = profiles
	return snapshot
}
