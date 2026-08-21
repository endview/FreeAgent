package assemblycompiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	CurrentMemberSnapshotSchemaVersion = corecontract.MemberExecutionSnapshotSchemaVersionV1
	CurrentCompilerVersion             = corecontract.AssemblyCompilerVersionV1
	CurrentRunManifestSchemaVersion    = corecontract.RunManifestSchemaVersionV1
	CurrentCoreRuntimeVersion          = corecontract.CoreRuntimeVersionV1
)

var ErrCapabilityNotAvailable = errors.New(
	"assemblycompiler: capability not available in the frozen assembly",
)

// CompileInput contains one stable ingress intent and the exact published
// Control/Catalog bytes observed before compilation. Catalog entries and
// Binding requests cannot be supplied independently.
type CompileInput struct {
	IntentCanonical []byte
	IntentDigest    string

	RunID           string
	MemberID        string
	RecoveryRootRef string

	PublishedBasis   controlcontract.PublishedBasis
	ControlCanonical []byte
	CatalogCanonical []byte

	ActionMaterializer     ActionMaterializerV1
	ActionBindingMaterials []actionmaterializer.BindingMaterialV1
}

// ActionMaterializerV1 is the sole optional admission-time Describe gate used
// by the Compiler. Keeping the interface here permits deterministic compiler
// tests while the production implementation remains actionmaterializer.Materializer.
type ActionMaterializerV1 interface {
	Materialize(
		context.Context,
		actionmaterializer.InputV1,
	) ([]corecontract.FrozenActionDefinitionV1, error)
}

// CompileOutput carries the immutable values and exact persistence bytes.
// Admission must revalidate PublishedBasis inside its Store transaction.
type CompileOutput struct {
	PublishedBasis          controlcontract.PublishedBasis
	MemberSnapshot          corecontract.MemberExecutionSnapshot
	MemberSnapshotCanonical []byte
	RunManifest             corecontract.RunManifest
	RunManifestCanonical    []byte
}

// Compiler is the sole S1 assembly compiler.
type Compiler struct{}

func (Compiler) Compile(
	ctx context.Context,
	input CompileInput,
) (CompileOutput, error) {
	if ctx == nil {
		return CompileOutput{}, fmt.Errorf("assemblycompiler: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return CompileOutput{}, err
	}
	for name, value := range map[string]string{
		"run ID":            input.RunID,
		"member ID":         input.MemberID,
		"recovery root ref": input.RecoveryRootRef,
	} {
		if !validCurrentOpaque(value) {
			return CompileOutput{}, fmt.Errorf(
				"assemblycompiler: invalid %s",
				name,
			)
		}
	}
	if err := input.PublishedBasis.Validate(); err != nil {
		return CompileOutput{}, err
	}

	intent, err := corecontract.RestoreAdmissionIntentV1(
		bytes.Clone(input.IntentCanonical),
		input.IntentDigest,
	)
	if err != nil {
		return CompileOutput{}, fmt.Errorf(
			"assemblycompiler: restore admission intent: %w",
			err,
		)
	}
	if string(intent.ExplicitLimits) != "{}" {
		return CompileOutput{}, fmt.Errorf(
			"%w: explicit limit merging is not implemented",
			ErrCapabilityNotAvailable,
		)
	}
	if intent.TenantID != input.PublishedBasis.TenantID {
		return CompileOutput{}, fmt.Errorf(
			"assemblycompiler: admission tenant does not match published basis",
		)
	}
	conversationTurn, err := conversationTurnRefFromIntentV1(intent)
	if err != nil {
		return CompileOutput{}, err
	}

	control, err := controlcontract.RestoreControlSnapshot(
		bytes.Clone(input.ControlCanonical),
		input.PublishedBasis.Control,
	)
	if err != nil {
		return CompileOutput{}, fmt.Errorf(
			"assemblycompiler: restore control snapshot: %w",
			err,
		)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		bytes.Clone(input.CatalogCanonical),
		input.PublishedBasis.Catalog,
	)
	if err != nil {
		return CompileOutput{}, fmt.Errorf(
			"assemblycompiler: restore runtime catalog: %w",
			err,
		)
	}
	if control.TenantID != intent.TenantID ||
		catalog.TenantID != intent.TenantID ||
		catalog.ControlSnapshotID != control.SnapshotID ||
		catalog.ControlSnapshotDigest != control.Digest {
		return CompileOutput{}, fmt.Errorf(
			"assemblycompiler: published Control/Catalog provenance does not close",
		)
	}

	agent, found := control.FindAgent(intent.AgentID)
	if !found {
		return CompileOutput{}, fmt.Errorf(
			"assemblycompiler: agent %q is absent from frozen Control",
			intent.AgentID,
		)
	}
	workspace, found := control.FindWorkspace(intent.WorkspaceID)
	if !found {
		return CompileOutput{}, fmt.Errorf(
			"assemblycompiler: workspace %q is absent from frozen Control",
			intent.WorkspaceID,
		)
	}
	profile, found := control.FindProfile(intent.ProfileID)
	if !found {
		return CompileOutput{}, fmt.Errorf(
			"assemblycompiler: profile %q is absent from frozen Control",
			intent.ProfileID,
		)
	}
	if err := requireRequestedPorts(
		intent.RequestedPorts,
		profile.Bindings,
	); err != nil {
		return CompileOutput{}, err
	}

	plans, err := resolvePortPlans(profile.Bindings, catalog)
	if err != nil {
		return CompileOutput{}, err
	}
	if intent.ChannelEndpointID != "" {
		endpoint, present := workspace.FindChannelEndpoint(
			intent.ChannelEndpointID,
		)
		if !present {
			return CompileOutput{}, fmt.Errorf(
				"assemblycompiler: channel endpoint %q is absent from the frozen Workspace",
				intent.ChannelEndpointID,
			)
		}
		if !endpoint.Enabled {
			return CompileOutput{}, fmt.Errorf(
				"assemblycompiler: channel endpoint %q is disabled",
				intent.ChannelEndpointID,
			)
		}
		if endpoint.TargetAgentID != intent.AgentID ||
			endpoint.TargetProfileID != intent.ProfileID {
			return CompileOutput{}, fmt.Errorf(
				"assemblycompiler: channel endpoint target does not match the admission agent/profile",
			)
		}
		channelPlans, resolveErr := resolvePortPlans(
			[]controlcontract.BindingSpec{endpoint.Binding},
			catalog,
		)
		if resolveErr != nil {
			return CompileOutput{}, fmt.Errorf(
				"assemblycompiler: resolve channel endpoint %q: %w",
				intent.ChannelEndpointID,
				resolveErr,
			)
		}
		if len(channelPlans) != 1 ||
			channelPlans[0].Port != (moduleapi.PortRef{
				Name:         moduleapi.PortNameChannelTransport,
				ExactVersion: moduleapi.PortVersionV1,
			}) {
			return CompileOutput{}, fmt.Errorf(
				"assemblycompiler: channel endpoint did not resolve one exact channel.transport/v1 PortPlan",
			)
		}
		for _, plan := range plans {
			if plan.Port == channelPlans[0].Port {
				return CompileOutput{}, fmt.Errorf(
					"assemblycompiler: duplicate channel.transport/v1 PortPlan",
				)
			}
		}
		plans = append(plans, channelPlans[0])
	}
	actions, err := materializeMemberActionsV1(
		ctx,
		input.ActionMaterializer,
		input.ActionBindingMaterials,
		intent.TenantID,
		workspace.Workspace.ID,
		plans,
	)
	if err != nil {
		return CompileOutput{}, err
	}
	catalogRef := corecontract.CatalogSnapshotRef{
		ID: input.PublishedBasis.Catalog.GenerationID,
		Version: strconv.FormatUint(
			input.PublishedBasis.Catalog.Generation,
			10,
		),
		Digest: input.PublishedBasis.Catalog.Digest,
	}
	member, memberCanonical, err :=
		corecontract.NewMemberExecutionSnapshot(
			corecontract.MemberExecutionSnapshot{
				SchemaVersion:    CurrentMemberSnapshotSchemaVersion,
				CompilerVersion:  CurrentCompilerVersion,
				Catalog:          catalogRef,
				MemberID:         input.MemberID,
				Agent:            agent,
				Profile:          profile.Profile,
				ModelProfile:     profile.ModelProfile,
				Workspace:        workspace.Workspace,
				PortPlans:        plans,
				Actions:          actions,
				ContextPolicy:    profile.ContextPolicy,
				CostPolicy:       profile.CostPolicy,
				SchedulingPolicy: profile.SchedulingPolicy,
			},
		)
	if err != nil {
		return CompileOutput{}, fmt.Errorf(
			"assemblycompiler: freeze member snapshot: %w",
			err,
		)
	}
	manifest, manifestCanonical, err := corecontract.NewRunManifest(
		corecontract.RunManifest{
			SchemaVersion:         CurrentRunManifestSchemaVersion,
			CoreRuntimeVersion:    CurrentCoreRuntimeVersion,
			AdmissionKey:          intent.AdmissionKey,
			AdmissionIntentDigest: input.IntentDigest,
			RunID:                 input.RunID,
			TenantID:              intent.TenantID,
			Workspace:             workspace.Workspace,
			PrimaryAgent:          agent,
			Members: []corecontract.MemberSnapshotRef{
				{
					MemberID: member.MemberID,
					Digest:   member.MemberSnapshotDigest,
				},
			},
			PrimaryMemberID:   member.MemberID,
			TaskInputRef:      intent.TaskInputRef,
			TaskInputDigest:   intent.TaskInputRef,
			ConversationTurn:  conversationTurn,
			BudgetPolicy:      workspace.BudgetPolicy,
			CancellationScope: intent.CancellationScope,
			Deadline:          intent.Deadline,
			RecoveryRootRef:   input.RecoveryRootRef,
		},
	)
	if err != nil {
		return CompileOutput{}, fmt.Errorf(
			"assemblycompiler: freeze run manifest: %w",
			err,
		)
	}
	if err := manifest.ValidateAgainstMember(member); err != nil {
		return CompileOutput{}, err
	}
	return CompileOutput{
		PublishedBasis:          input.PublishedBasis,
		MemberSnapshot:          member,
		MemberSnapshotCanonical: bytes.Clone(memberCanonical),
		RunManifest:             manifest,
		RunManifestCanonical:    bytes.Clone(manifestCanonical),
	}, nil
}

func conversationTurnRefFromIntentV1(
	intent corecontract.AdmissionIntentV1,
) (*corecontract.ConversationTurnRefV1, error) {
	if intent.ConversationTurn == nil {
		return nil, nil
	}
	if err := intent.ConversationTurn.Validate(); err != nil {
		return nil, fmt.Errorf("assemblycompiler: Conversation intent: %w", err)
	}
	ref := &corecontract.ConversationTurnRefV1{
		SchemaVersion:    corecontract.ConversationTurnRefSchemaVersionV1,
		ConversationID:   intent.ConversationTurn.ConversationID,
		PrincipalID:      intent.PrincipalID,
		TurnIndex:        intent.ConversationTurn.ExpectedConversationRevision + 1,
		PredecessorRunID: intent.ConversationTurn.ExpectedHeadRunID,
	}
	if err := ref.Validate(); err != nil {
		return nil, fmt.Errorf("assemblycompiler: Conversation Run ref: %w", err)
	}
	return ref, nil
}

func materializeMemberActionsV1(
	ctx context.Context,
	materializer ActionMaterializerV1,
	materials []actionmaterializer.BindingMaterialV1,
	tenantID string,
	workspaceID string,
	plans []moduleapi.PortPlan,
) ([]corecontract.FrozenActionDefinitionV1, error) {
	var actionPlan *moduleapi.PortPlan
	for index := range plans {
		if plans[index].Port.Name == moduleapi.PortNameActionProvider &&
			plans[index].Port.ExactVersion == moduleapi.PortVersionV1 {
			actionPlan = &plans[index]
			break
		}
	}
	if actionPlan == nil {
		if len(materials) != 0 {
			return nil, fmt.Errorf(
				"assemblycompiler: Action binding materials require action.provider/v1 PortPlan",
			)
		}
		// An explicitly Action-capable service may compile a Pure Chat profile.
		// The materializer remains entirely untouched on this branch.
		return nil, nil
	}
	if isNilActionMaterializerV1(materializer) {
		return nil, fmt.Errorf(
			"%w: action.provider/v1 requires an admission Action materializer",
			ErrCapabilityNotAvailable,
		)
	}
	if len(materials) != len(actionPlan.Bindings) {
		return nil, fmt.Errorf(
			"assemblycompiler: action.provider/v1 requires one ordered Config/Authority material pair per Binding",
		)
	}
	frozenMaterials := make(
		[]actionmaterializer.BindingMaterialV1,
		len(materials),
	)
	for index, material := range materials {
		frozenMaterials[index] = actionmaterializer.BindingMaterialV1{
			ConfigCanonical:    bytes.Clone(material.ConfigCanonical),
			AuthorityCanonical: bytes.Clone(material.AuthorityCanonical),
		}
	}
	actions, err := materializer.Materialize(
		ctx,
		actionmaterializer.InputV1{
			TenantID:    tenantID,
			WorkspaceID: workspaceID,
			Plan:        *actionPlan,
			Bindings:    frozenMaterials,
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"assemblycompiler: materialize action.provider/v1: %w",
			err,
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return actions, nil
}

func isNilActionMaterializerV1(value ActionMaterializerV1) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func requireRequestedPorts(
	requested []moduleapi.PortRef,
	bindings []controlcontract.BindingSpec,
) error {
	available := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		key, _ := binding.Port.CanonicalKey()
		available[key] = struct{}{}
	}
	for _, port := range requested {
		registered := false
		for _, supported := range moduleapi.S1PortRefs() {
			if supported == port {
				registered = true
				break
			}
		}
		key, _ := port.CanonicalKey()
		if !registered {
			return fmt.Errorf(
				"%w: %s/%s",
				ErrCapabilityNotAvailable,
				port.Name,
				port.ExactVersion,
			)
		}
		if _, present := available[key]; !present {
			return fmt.Errorf(
				"%w: frozen profile does not bind %s/%s",
				ErrCapabilityNotAvailable,
				port.Name,
				port.ExactVersion,
			)
		}
	}
	return nil
}

func resolvePortPlans(
	requests []controlcontract.BindingSpec,
	catalog controlcontract.CatalogGeneration,
) ([]moduleapi.PortPlan, error) {
	grouped := make(map[string][]moduleapi.PortBinding)
	portByKey := make(map[string]moduleapi.PortRef)
	for index, request := range requests {
		entry, found := catalog.FindInstance(request.InstanceID)
		if !found {
			return nil, fmt.Errorf(
				"assemblycompiler: activated instance %q is absent from the frozen catalog",
				request.InstanceID,
			)
		}
		if !entryProvides(entry, request.Port) {
			return nil, fmt.Errorf(
				"assemblycompiler: activated instance %q does not provide %s/%s",
				request.InstanceID,
				request.Port.Name,
				request.Port.ExactVersion,
			)
		}
		binding := moduleapi.PortBinding{
			Provider:            entry.Activation,
			ConfigRef:           request.ConfigRef,
			AuthorityCeilingRef: request.AuthorityCeilingRef,
			StaticContextRefs: append(
				[]string{},
				request.StaticContextRefs...,
			),
			FailurePolicy: request.FailurePolicy,
		}
		if err := binding.Validate(); err != nil {
			return nil, fmt.Errorf(
				"assemblycompiler: binding request %d: %w",
				index,
				err,
			)
		}
		key, _ := request.Port.CanonicalKey()
		grouped[key] = append(grouped[key], binding)
		portByKey[key] = request.Port
	}

	plans := make([]moduleapi.PortPlan, 0, len(grouped))
	for _, port := range moduleapi.S1PortRefs() {
		key, _ := port.CanonicalKey()
		bindings := grouped[key]
		if len(bindings) == 0 {
			continue
		}
		plan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
			Port:     portByKey[key],
			Bindings: bindings,
		})
		if err != nil {
			return nil, fmt.Errorf(
				"assemblycompiler: freeze %s/%s: %w",
				port.Name,
				port.ExactVersion,
				err,
			)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func entryProvides(
	entry controlcontract.CatalogEntry,
	requested moduleapi.PortRef,
) bool {
	for _, provided := range entry.Provides {
		if provided == requested {
			return true
		}
	}
	return false
}

func validCurrentOpaque(value string) bool {
	if value == "" ||
		len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) ||
		value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
