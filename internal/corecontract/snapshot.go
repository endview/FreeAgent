package corecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	MemberExecutionSnapshotSchemaVersionV2 = "member-execution-snapshot/v2"
	AssemblyCompilerVersionV2              = "assembly-compiler/v2"
	RunManifestSchemaVersionV2             = "run-manifest/v2"
	CoreRuntimeVersionV2                   = "core-runtime/v2"

	memberSnapshotDigestDomain = "freeagent.member-snapshot/v2"
	runManifestDigestDomain    = "freeagent.run-manifest/v2"
)

// MemberExecutionSnapshot is the immutable, recovery-authoritative assembly
// for one member. Optional capabilities remain selected by PortPlans. Actions
// additionally freeze the bounded Describe material required to recover the
// model-visible schema without calling a Provider again.
type MemberExecutionSnapshot struct {
	SchemaVersion        string                     `json:"schema_version"`
	CompilerVersion      string                     `json:"compiler_version"`
	Catalog              CatalogSnapshotRef         `json:"catalog"`
	MemberID             string                     `json:"member_id"`
	Agent                AgentRef                   `json:"agent"`
	Profile              ProfileRef                 `json:"profile"`
	ModelProfile         *ModelProfileRef           `json:"model_profile,omitempty"`
	Workspace            WorkspaceRef               `json:"workspace"`
	PortPlans            []moduleapi.PortPlan       `json:"port_plans"`
	Actions              []FrozenActionDefinitionV1 `json:"actions,omitempty"`
	ContextPolicy        PolicyRef                  `json:"context_policy"`
	SchedulingPolicy     PolicyRef                  `json:"scheduling_policy"`
	MemberSnapshotDigest string                     `json:"member_snapshot_digest"`
}

// NewMemberExecutionSnapshot validates, orders, freezes and digests one S1
// member snapshot. PortPlans are ordered by exact PortRef; binding order is
// preserved exactly.
func NewMemberExecutionSnapshot(
	input MemberExecutionSnapshot,
) (MemberExecutionSnapshot, []byte, error) {
	if input.SchemaVersion != MemberExecutionSnapshotSchemaVersionV2 {
		return MemberExecutionSnapshot{}, nil,
			fmt.Errorf(
				"corecontract: member snapshot schema version must be %q",
				MemberExecutionSnapshotSchemaVersionV2,
			)
	}
	if input.CompilerVersion != AssemblyCompilerVersionV2 {
		return MemberExecutionSnapshot{}, nil,
			fmt.Errorf(
				"corecontract: compiler version must be %q",
				AssemblyCompilerVersionV2,
			)
	}
	if err := input.Catalog.Validate(); err != nil {
		return MemberExecutionSnapshot{}, nil, err
	}
	if !validOpaque(input.MemberID, maxOpaqueIDBytes) {
		return MemberExecutionSnapshot{}, nil,
			fmt.Errorf("corecontract: invalid member ID")
	}
	if err := input.Agent.Validate(); err != nil {
		return MemberExecutionSnapshot{}, nil, err
	}
	if err := input.Profile.Validate(); err != nil {
		return MemberExecutionSnapshot{}, nil, err
	}
	if input.ModelProfile != nil {
		if err := input.ModelProfile.Validate(); err != nil {
			return MemberExecutionSnapshot{}, nil, err
		}
	}
	if err := input.Workspace.Validate(); err != nil {
		return MemberExecutionSnapshot{}, nil, err
	}
	if err := input.ContextPolicy.Validate(); err != nil {
		return MemberExecutionSnapshot{}, nil, err
	}
	if err := input.SchedulingPolicy.Validate(); err != nil {
		return MemberExecutionSnapshot{}, nil, err
	}

	plans := make([]moduleapi.PortPlan, len(input.PortPlans))
	seen := make(map[string]struct{}, len(input.PortPlans))
	modelPlanFound := false
	for index, plan := range input.PortPlans {
		frozen, err := moduleapi.NewPortPlan(plan)
		if err != nil {
			return MemberExecutionSnapshot{}, nil,
				fmt.Errorf("corecontract: port plan %d: %w", index, err)
		}
		key, err := frozen.Port.CanonicalKey()
		if err != nil {
			return MemberExecutionSnapshot{}, nil, err
		}
		if _, duplicate := seen[key]; duplicate {
			return MemberExecutionSnapshot{}, nil,
				fmt.Errorf("corecontract: duplicate port plan %q", key)
		}
		seen[key] = struct{}{}
		if frozen.Port.Name == moduleapi.PortNameModelGenerate &&
			frozen.Port.ExactVersion == moduleapi.PortVersionV2 {
			modelPlanFound = true
		}
		plans[index] = frozen
	}
	if !modelPlanFound {
		return MemberExecutionSnapshot{}, nil,
			fmt.Errorf("corecontract: S1 member snapshot requires model.generate/v2")
	}
	sort.Slice(plans, func(left, right int) bool {
		if plans[left].Port.Name != plans[right].Port.Name {
			return plans[left].Port.Name < plans[right].Port.Name
		}
		return plans[left].Port.ExactVersion < plans[right].Port.ExactVersion
	})
	var actionPlan *moduleapi.PortPlan
	for index := range plans {
		if plans[index].Port.Name == moduleapi.PortNameActionProvider &&
			plans[index].Port.ExactVersion == moduleapi.PortVersionV1 {
			actionPlan = &plans[index]
			break
		}
	}
	actions, err := freezeMemberSnapshotActionsV1(input.Actions, actionPlan)
	if err != nil {
		return MemberExecutionSnapshot{}, nil, err
	}

	frozen := input
	frozen.ModelProfile = cloneModelProfileRef(input.ModelProfile)
	frozen.PortPlans = plans
	frozen.Actions = actions
	frozen.MemberSnapshotDigest = ""
	identityCanonical, err := canonicalMemberSnapshot(frozen, false)
	if err != nil {
		return MemberExecutionSnapshot{}, nil, err
	}
	frozen.MemberSnapshotDigest = moduleapi.Digest(
		memberSnapshotDigestDomain,
		identityCanonical,
	)
	canonical, err := canonicalMemberSnapshot(frozen, true)
	if err != nil {
		return MemberExecutionSnapshot{}, nil, err
	}
	return frozen, canonical, nil
}

// RestoreMemberExecutionSnapshot accepts only exact canonical bytes and
// verifies the digest after rebuilding all phase invariants.
func RestoreMemberExecutionSnapshot(
	canonical []byte,
) (MemberExecutionSnapshot, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return MemberExecutionSnapshot{}, err
	}
	var decoded MemberExecutionSnapshot
	if err := decodeStrict(canonical, &decoded); err != nil {
		return MemberExecutionSnapshot{}, err
	}
	rebuilt, rebuiltCanonical, err := NewMemberExecutionSnapshot(decoded)
	if err != nil {
		return MemberExecutionSnapshot{}, err
	}
	if !bytes.Equal(rebuiltCanonical, canonical) ||
		rebuilt.MemberSnapshotDigest != decoded.MemberSnapshotDigest {
		return MemberExecutionSnapshot{},
			fmt.Errorf("corecontract: member snapshot is not frozen canonically")
	}
	return rebuilt, nil
}

func canonicalMemberSnapshot(
	snapshot MemberExecutionSnapshot,
	includeDigest bool,
) ([]byte, error) {
	type wire struct {
		SchemaVersion        string                     `json:"schema_version"`
		CompilerVersion      string                     `json:"compiler_version"`
		Catalog              CatalogSnapshotRef         `json:"catalog"`
		MemberID             string                     `json:"member_id"`
		Agent                AgentRef                   `json:"agent"`
		Profile              ProfileRef                 `json:"profile"`
		ModelProfile         *ModelProfileRef           `json:"model_profile,omitempty"`
		Workspace            WorkspaceRef               `json:"workspace"`
		PortPlans            []moduleapi.PortPlan       `json:"port_plans"`
		Actions              []FrozenActionDefinitionV1 `json:"actions,omitempty"`
		ContextPolicy        PolicyRef                  `json:"context_policy"`
		SchedulingPolicy     PolicyRef                  `json:"scheduling_policy"`
		MemberSnapshotDigest string                     `json:"member_snapshot_digest,omitempty"`
	}
	value := wire{
		SchemaVersion:    snapshot.SchemaVersion,
		CompilerVersion:  snapshot.CompilerVersion,
		Catalog:          snapshot.Catalog,
		MemberID:         snapshot.MemberID,
		Agent:            snapshot.Agent,
		Profile:          snapshot.Profile,
		ModelProfile:     snapshot.ModelProfile,
		Workspace:        snapshot.Workspace,
		PortPlans:        snapshot.PortPlans,
		Actions:          snapshot.Actions,
		ContextPolicy:    snapshot.ContextPolicy,
		SchedulingPolicy: snapshot.SchedulingPolicy,
	}
	if includeDigest {
		value.MemberSnapshotDigest = snapshot.MemberSnapshotDigest
	}
	return canonicalJSON(value)
}

func freezeMemberSnapshotActionsV1(
	input []FrozenActionDefinitionV1,
	actionPlan *moduleapi.PortPlan,
) ([]FrozenActionDefinitionV1, error) {
	if actionPlan == nil {
		if len(input) != 0 {
			return nil, fmt.Errorf(
				"corecontract: member Actions require action.provider/v1 PortPlan",
			)
		}
		return nil, nil
	}
	if len(input) == 0 || len(input) > moduleapi.MaxActionsPerMemberV1 {
		return nil, fmt.Errorf(
			"corecontract: action.provider/v1 member requires between 1 and %d frozen Actions",
			moduleapi.MaxActionsPerMemberV1,
		)
	}
	frozen := make([]FrozenActionDefinitionV1, len(input))
	seenPublicIDs := make(map[string]struct{}, len(input))
	aggregateBytes := 0
	for index, definition := range input {
		validated, _, err := NewFrozenActionDefinitionV1(definition)
		if err != nil {
			return nil, fmt.Errorf(
				"corecontract: member Action %d: %w",
				index,
				err,
			)
		}
		if uint64(validated.BindingIndex) >= uint64(len(actionPlan.Bindings)) {
			return nil, fmt.Errorf(
				"corecontract: member Action %q BindingIndex is outside action.provider/v1 PortPlan",
				validated.PublicActionID,
			)
		}
		if _, duplicate := seenPublicIDs[validated.PublicActionID]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: duplicate member PublicActionID %q",
				validated.PublicActionID,
			)
		}
		seenPublicIDs[validated.PublicActionID] = struct{}{}
		aggregateBytes += len(validated.Description) + len(validated.InputSchema)
		if aggregateBytes > moduleapi.MaxActionDefinitionAggregateBytesV1 {
			return nil, fmt.Errorf(
				"corecontract: member Action descriptions and schemas exceed %d aggregate bytes",
				moduleapi.MaxActionDefinitionAggregateBytesV1,
			)
		}
		frozen[index] = validated
	}
	sort.Slice(frozen, func(left, right int) bool {
		return frozen[left].PublicActionID < frozen[right].PublicActionID
	})
	return frozen, nil
}

// ModelActionDefinitionsV1 projects only the bounded public model view from
// frozen Core definitions. Provider identity, Binding, Effect, authority and
// result limits never cross this boundary.
func ModelActionDefinitionsV1(
	definitions []FrozenActionDefinitionV1,
) ([]moduleapi.ModelActionDefinitionV1, error) {
	if len(definitions) == 0 {
		return nil, nil
	}
	if len(definitions) > moduleapi.MaxActionsPerMemberV1 {
		return nil, fmt.Errorf(
			"corecontract: model Action projection exceeds %d definitions",
			moduleapi.MaxActionsPerMemberV1,
		)
	}
	modelActions := make([]moduleapi.ModelActionDefinitionV1, len(definitions))
	previousPublicID := ""
	for index, definition := range definitions {
		frozen, _, err := NewFrozenActionDefinitionV1(definition)
		if err != nil {
			return nil, fmt.Errorf(
				"corecontract: model Action projection %d: %w",
				index,
				err,
			)
		}
		if index != 0 && frozen.PublicActionID <= previousPublicID {
			return nil, fmt.Errorf(
				"corecontract: model Action projection requires strictly sorted unique PublicActionID values",
			)
		}
		modelActions[index] = moduleapi.ModelActionDefinitionV1{
			ActionID:    frozen.PublicActionID,
			Description: frozen.Description,
			InputSchema: bytes.Clone(frozen.InputSchema),
		}
		previousPublicID = frozen.PublicActionID
	}
	return modelActions, nil
}

func cloneModelProfileRef(ref *ModelProfileRef) *ModelProfileRef {
	if ref == nil {
		return nil
	}
	cloned := *ref
	return &cloned
}

// RunManifest is the immutable recovery root for one admitted Run.
type RunManifest struct {
	SchemaVersion         string                 `json:"schema_version"`
	CoreRuntimeVersion    string                 `json:"core_runtime_version"`
	AdmissionKey          string                 `json:"admission_key"`
	AdmissionIntentDigest string                 `json:"admission_intent_digest"`
	RunID                 string                 `json:"run_id"`
	TenantID              string                 `json:"tenant_id"`
	Workspace             WorkspaceRef           `json:"workspace"`
	PrimaryAgent          AgentRef               `json:"primary_agent"`
	Members               []MemberSnapshotRef    `json:"members"`
	PrimaryMemberID       string                 `json:"primary_member_id"`
	TaskInputRef          string                 `json:"task_input_ref"`
	TaskInputDigest       string                 `json:"task_input_digest"`
	ConversationTurn      *ConversationTurnRefV1 `json:"conversation_turn,omitempty"`
	ParentRunID           string                 `json:"parent_run_id,omitempty"`
	CancellationScope     string                 `json:"cancellation_scope"`
	Deadline              time.Time              `json:"deadline"`
	RecoveryRootRef       string                 `json:"recovery_root_ref"`
	Composite             *CompositeRunNodeV1    `json:"composite,omitempty"`
	ManifestDigest        string                 `json:"manifest_digest"`
}

// NewRunManifest freezes the single-member S1 manifest. Deadline is
// normalized to RFC3339Nano UTC before it participates in the digest.
func NewRunManifest(input RunManifest) (RunManifest, []byte, error) {
	if input.SchemaVersion != RunManifestSchemaVersionV2 {
		return RunManifest{}, nil,
			fmt.Errorf(
				"corecontract: run manifest schema version must be %q",
				RunManifestSchemaVersionV2,
			)
	}
	if input.CoreRuntimeVersion != CoreRuntimeVersionV2 {
		return RunManifest{}, nil,
			fmt.Errorf(
				"corecontract: Core runtime version must be %q",
				CoreRuntimeVersionV2,
			)
	}
	for name, value := range map[string]string{
		"admission key":      input.AdmissionKey,
		"run ID":             input.RunID,
		"tenant ID":          input.TenantID,
		"primary member ID":  input.PrimaryMemberID,
		"cancellation scope": input.CancellationScope,
		"recovery root ref":  input.RecoveryRootRef,
	} {
		if !validOpaque(value, maxOpaqueIDBytes) {
			return RunManifest{}, nil,
				fmt.Errorf("corecontract: invalid %s", name)
		}
	}
	if !moduleapi.ValidSHA256(input.AdmissionIntentDigest) {
		return RunManifest{}, nil,
			fmt.Errorf("corecontract: invalid admission intent digest")
	}
	if err := input.Workspace.Validate(); err != nil {
		return RunManifest{}, nil, err
	}
	if err := input.PrimaryAgent.Validate(); err != nil {
		return RunManifest{}, nil, err
	}
	if len(input.Members) != 1 {
		return RunManifest{}, nil,
			fmt.Errorf("corecontract: S1 manifest requires exactly one member")
	}
	if err := input.Members[0].Validate(); err != nil {
		return RunManifest{}, nil, err
	}
	if input.Members[0].MemberID != input.PrimaryMemberID {
		return RunManifest{}, nil,
			fmt.Errorf("corecontract: primary member does not match member snapshot ref")
	}
	if !moduleapi.ValidSHA256(input.TaskInputRef) ||
		input.TaskInputRef != input.TaskInputDigest {
		return RunManifest{}, nil,
			fmt.Errorf("corecontract: task input ref and digest must be one content digest")
	}
	if input.ConversationTurn != nil {
		if err := input.ConversationTurn.Validate(); err != nil {
			return RunManifest{}, nil, err
		}
		if input.ConversationTurn.PredecessorRunID == input.RunID {
			return RunManifest{}, nil,
				fmt.Errorf("corecontract: conversation Run cannot precede itself")
		}
		if input.ParentRunID != "" || input.Composite != nil {
			return RunManifest{}, nil,
				fmt.Errorf("corecontract: Conversation and Composite Run relations are mutually exclusive in v1")
		}
	}
	if input.Deadline.IsZero() {
		return RunManifest{}, nil,
			fmt.Errorf("corecontract: deadline is required")
	}

	frozen := input
	frozen.Members = append([]MemberSnapshotRef(nil), input.Members...)
	frozen.ConversationTurn = cloneConversationTurnRefV1(input.ConversationTurn)
	composite, err := freezeCompositeRunNodeV1(input)
	if err != nil {
		return RunManifest{}, nil, err
	}
	frozen.Composite = composite
	frozen.Deadline = input.Deadline.Round(0).UTC()
	frozen.ManifestDigest = ""
	identityCanonical, err := canonicalRunManifest(frozen, false)
	if err != nil {
		return RunManifest{}, nil, err
	}
	frozen.ManifestDigest = moduleapi.Digest(
		runManifestDigestDomain,
		identityCanonical,
	)
	canonical, err := canonicalRunManifest(frozen, true)
	if err != nil {
		return RunManifest{}, nil, err
	}
	return frozen, canonical, nil
}

// ValidateAgainstMember checks the cross-object identities used by recovery.
func (manifest RunManifest) ValidateAgainstMember(
	member MemberExecutionSnapshot,
) error {
	if len(manifest.Members) != 1 {
		return fmt.Errorf("corecontract: manifest member cardinality changed")
	}
	if manifest.Members[0].MemberID != member.MemberID ||
		manifest.Members[0].Digest != member.MemberSnapshotDigest {
		return fmt.Errorf("corecontract: manifest member reference mismatch")
	}
	if manifest.PrimaryMemberID != member.MemberID {
		return fmt.Errorf("corecontract: manifest primary member mismatch")
	}
	if manifest.Workspace != member.Workspace {
		return fmt.Errorf("corecontract: manifest workspace mismatch")
	}
	if manifest.PrimaryAgent != member.Agent {
		return fmt.Errorf("corecontract: manifest primary agent mismatch")
	}
	return nil
}

// RestoreRunManifest accepts only exact canonical bytes and verifies the
// self-excluding ManifestDigest.
func RestoreRunManifest(canonical []byte) (RunManifest, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return RunManifest{}, err
	}
	var decoded RunManifest
	if err := decodeStrict(canonical, &decoded); err != nil {
		return RunManifest{}, err
	}
	rebuilt, rebuiltCanonical, err := NewRunManifest(decoded)
	if err != nil {
		return RunManifest{}, err
	}
	if !bytes.Equal(rebuiltCanonical, canonical) ||
		rebuilt.ManifestDigest != decoded.ManifestDigest {
		return RunManifest{},
			fmt.Errorf("corecontract: run manifest is not frozen canonically")
	}
	return rebuilt, nil
}

func canonicalRunManifest(
	manifest RunManifest,
	includeDigest bool,
) ([]byte, error) {
	type wire struct {
		SchemaVersion         string                 `json:"schema_version"`
		CoreRuntimeVersion    string                 `json:"core_runtime_version"`
		AdmissionKey          string                 `json:"admission_key"`
		AdmissionIntentDigest string                 `json:"admission_intent_digest"`
		RunID                 string                 `json:"run_id"`
		TenantID              string                 `json:"tenant_id"`
		Workspace             WorkspaceRef           `json:"workspace"`
		PrimaryAgent          AgentRef               `json:"primary_agent"`
		Members               []MemberSnapshotRef    `json:"members"`
		PrimaryMemberID       string                 `json:"primary_member_id"`
		TaskInputRef          string                 `json:"task_input_ref"`
		TaskInputDigest       string                 `json:"task_input_digest"`
		ConversationTurn      *ConversationTurnRefV1 `json:"conversation_turn,omitempty"`
		ParentRunID           string                 `json:"parent_run_id,omitempty"`
		CancellationScope     string                 `json:"cancellation_scope"`
		Deadline              time.Time              `json:"deadline"`
		RecoveryRootRef       string                 `json:"recovery_root_ref"`
		Composite             *CompositeRunNodeV1    `json:"composite,omitempty"`
		ManifestDigest        string                 `json:"manifest_digest,omitempty"`
	}
	value := wire{
		SchemaVersion:         manifest.SchemaVersion,
		CoreRuntimeVersion:    manifest.CoreRuntimeVersion,
		AdmissionKey:          manifest.AdmissionKey,
		AdmissionIntentDigest: manifest.AdmissionIntentDigest,
		RunID:                 manifest.RunID,
		TenantID:              manifest.TenantID,
		Workspace:             manifest.Workspace,
		PrimaryAgent:          manifest.PrimaryAgent,
		Members:               manifest.Members,
		PrimaryMemberID:       manifest.PrimaryMemberID,
		TaskInputRef:          manifest.TaskInputRef,
		TaskInputDigest:       manifest.TaskInputDigest,
		ConversationTurn:      cloneConversationTurnRefV1(manifest.ConversationTurn),
		ParentRunID:           manifest.ParentRunID,
		CancellationScope:     manifest.CancellationScope,
		Deadline:              manifest.Deadline,
		RecoveryRootRef:       manifest.RecoveryRootRef,
		Composite:             cloneCompositeRunNodeV1(manifest.Composite),
	}
	if includeDigest {
		value.ManifestDigest = manifest.ManifestDigest
	}
	return canonicalJSON(value)
}

func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("corecontract: encode canonical object: %w", err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return nil, fmt.Errorf("corecontract: canonicalize object: %w", err)
	}
	return canonical, nil
}

func requireExactCanonical(canonical []byte) error {
	checked, err := moduleapi.CanonicalJSON(canonical)
	if err != nil || !bytes.Equal(checked, canonical) {
		return fmt.Errorf("corecontract: object is not canonical")
	}
	return nil
}

func decodeStrict(canonical []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("corecontract: decode object: %w", err)
	}
	return nil
}
