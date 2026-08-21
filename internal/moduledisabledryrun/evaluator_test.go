package moduledisabledryrun

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type disableFactsV1 struct {
	basis   controlcontract.PublishedBasis
	control controlcontract.ControlSnapshot
	catalog controlcontract.CatalogGeneration
}

type disableReadViewV1 struct {
	loads       []disableFactsV1
	loadCalls   int
	loadErr     error
	verifyErr   error
	verifyCalls int
	revisions   map[[2]uint64]disableFactsV1
}

func (view *disableReadViewV1) LoadPublishedBasis(
	ctx context.Context,
	tenantID string,
) (
	controlcontract.PublishedBasis,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
	error,
) {
	if err := ctx.Err(); err != nil {
		return controlcontract.PublishedBasis{}, controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, err
	}
	view.loadCalls++
	if view.loadErr != nil {
		return controlcontract.PublishedBasis{}, controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, view.loadErr
	}
	if len(view.loads) == 0 {
		return controlcontract.PublishedBasis{}, controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, errors.New("no published fixture")
	}
	index := view.loadCalls - 1
	if index >= len(view.loads) {
		index = len(view.loads) - 1
	}
	facts := view.loads[index]
	return facts.basis, facts.control, facts.catalog, nil
}

func (view *disableReadViewV1) VerifyPublishedControlCatalogClosureV1(
	context.Context,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
) error {
	view.verifyCalls++
	return view.verifyErr
}

func (view *disableReadViewV1) LoadControlCatalogRevision(
	ctx context.Context,
	tenantID string,
	controlRevision uint64,
	catalogGeneration uint64,
) (
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
	error,
) {
	if err := ctx.Err(); err != nil {
		return controlcontract.ControlSnapshot{}, controlcontract.CatalogGeneration{}, err
	}
	facts, found := view.revisions[[2]uint64{controlRevision, catalogGeneration}]
	if !found || facts.control.TenantID != tenantID {
		return controlcontract.ControlSnapshot{}, controlcontract.CatalogGeneration{},
			errors.New("revision fixture is absent")
	}
	return facts.control, facts.catalog, nil
}

func TestEvaluateProfileDisableWouldApplyAndRemoveLastReferenceV1(t *testing.T) {
	port := actionPortV1()
	facts := freezeDisableFactsV1(t, []controlcontract.ProfileDefinition{
		profileV1("profile-a", []controlcontract.BindingSpec{
			bindingV1(port, "instance-a", "1"),
		}),
	}, nil, []controlcontract.CatalogEntry{
		catalogEntryV1("module.a", "instance-a", port, "a"),
	}, 7)
	before := cloneFactsV1(t, facts)
	view := &disableReadViewV1{loads: []disableFactsV1{facts}}
	excluded := ""
	result, err := EvaluateV1(
		context.Background(),
		view,
		func(
			_ context.Context,
			_ controlcontract.CatalogGeneration,
			candidate string,
		) error {
			excluded = candidate
			return nil
		},
		inputV1(port, 7, "profile-a", "instance-a", "d"),
	)
	if err != nil {
		t.Fatalf("EvaluateV1(): %v", err)
	}
	if result.Status != StatusWouldApplyV1 || result.Publication == nil ||
		result.PreconditionBasis != facts.basis ||
		result.CatalogChange != CatalogChangeRemoveInstanceV1 ||
		result.ExcludedInstanceID != "instance-a" || excluded != "instance-a" ||
		result.CandidateBasis.PointerRevision != 8 || view.loadCalls != 2 ||
		view.verifyCalls != 1 {
		t.Fatalf("profile Disable result=%+v excluded=%q calls=%d/%d", result, excluded, view.loadCalls, view.verifyCalls)
	}
	if result.BindingRemoval.PortBindingIndex == nil ||
		*result.BindingRemoval.PortBindingIndex != 0 ||
		result.BindingRemoval.ConfigRef != hashV1("1") ||
		result.BindingRemoval.AuthorityCeilingRef != hashV1("1") ||
		result.BindingRemoval.FailurePolicy == nil ||
		*result.BindingRemoval.FailurePolicy != moduleapi.FailureRequired {
		t.Fatalf("binding removal=%+v", result.BindingRemoval)
	}
	if !reflect.DeepEqual(facts, before) {
		t.Fatal("EvaluateV1 mutated caller-owned observed facts")
	}
	candidateControl, err := controlcontract.RestoreControlSnapshot(
		result.Publication.ControlCanonical,
		result.Publication.ControlRef,
	)
	if err != nil || len(candidateControl.Profiles[0].Bindings) != 0 {
		t.Fatalf("candidate Control=%+v err=%v", candidateControl, err)
	}
	candidateCatalog, err := controlcontract.RestoreCatalogGeneration(
		result.Publication.CatalogCanonical,
		result.Publication.CatalogRef,
	)
	if err != nil || len(candidateCatalog.Entries) != 0 {
		t.Fatalf("candidate Catalog=%+v err=%v", candidateCatalog, err)
	}

	// Returned bytes are detached from subsequent calls and caller mutation.
	controlCanonical := bytes.Clone(result.Publication.ControlCanonical)
	result.Publication.ControlCanonical[0] ^= 0xff
	again, err := EvaluateV1(
		context.Background(),
		&disableReadViewV1{loads: []disableFactsV1{facts}},
		func(context.Context, controlcontract.CatalogGeneration, string) error { return nil },
		inputV1(port, 7, "profile-a", "instance-a", "d"),
	)
	if err != nil || !bytes.Equal(again.Publication.ControlCanonical, controlCanonical) {
		t.Fatalf("result aliases evaluator state: err=%v", err)
	}
}

func TestEvaluateExactBasisMatchesLiveProjectionAndFailsClosedV1(t *testing.T) {
	port := actionPortV1()
	facts := freezeDisableFactsV1(t, []controlcontract.ProfileDefinition{
		profileV1("profile-a", []controlcontract.BindingSpec{
			bindingV1(port, "instance-a", "1"),
		}),
	}, nil, []controlcontract.CatalogEntry{
		catalogEntryV1("module.a", "instance-a", port, "a"),
	}, 7)
	input := inputV1(port, 7, "profile-a", "instance-a", "d")

	live, err := EvaluateV1(
		context.Background(),
		&disableReadViewV1{loads: []disableFactsV1{facts}},
		func(context.Context, controlcontract.CatalogGeneration, string) error { return nil },
		input,
	)
	if err != nil {
		t.Fatalf("EvaluateV1(): %v", err)
	}
	exact, err := EvaluateExactBasisV1(input, facts.basis, facts.control, facts.catalog)
	if err != nil {
		t.Fatalf("EvaluateExactBasisV1(): %v", err)
	}
	if !reflect.DeepEqual(exact, live) {
		t.Fatalf("exact replay differs from live projection:\n exact=%+v\n live=%+v", exact, live)
	}

	wrongPointer := facts.basis
	wrongPointer.PointerRevision++
	_, err = EvaluateExactBasisV1(input, wrongPointer, facts.control, facts.catalog)
	assertFailureCodeV1(t, err, FailurePointerConflictV1)

	wrongCatalog := facts.catalog
	wrongCatalog.ControlSnapshotDigest = hashV1("f")
	_, err = EvaluateExactBasisV1(input, facts.basis, facts.control, wrongCatalog)
	assertFailureCodeV1(t, err, FailureStoreInvalidV1)
}

func TestEvaluateProfileDisableRetainsFanoutAndPortOrdinalV1(t *testing.T) {
	port := actionPortV1()
	otherPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	facts := freezeDisableFactsV1(t, []controlcontract.ProfileDefinition{
		profileV1("profile-a", []controlcontract.BindingSpec{
			bindingV1(otherPort, "context-a", "2"),
			bindingV1(port, "action-before", "3"),
			bindingV1(port, "instance-a", "4"),
		}),
		profileV1("profile-b", []controlcontract.BindingSpec{
			bindingV1(port, "instance-a", "5"),
		}),
	}, nil, []controlcontract.CatalogEntry{
		catalogEntryV1("module.action-before", "action-before", port, "b"),
		catalogEntryV1("module.context-a", "context-a", otherPort, "c"),
		catalogEntryV1("module.a", "instance-a", port, "d"),
	}, 3)
	result, err := EvaluateV1(
		context.Background(),
		&disableReadViewV1{loads: []disableFactsV1{facts}},
		func(context.Context, controlcontract.CatalogGeneration, string) error { return nil },
		inputV1(port, 3, "profile-a", "instance-a", "e"),
	)
	if err != nil {
		t.Fatalf("EvaluateV1(): %v", err)
	}
	if result.CatalogChange != CatalogChangeRetainInstanceV1 ||
		result.ExcludedInstanceID != "" ||
		result.BindingRemoval.PortBindingIndex == nil ||
		*result.BindingRemoval.PortBindingIndex != 1 {
		t.Fatalf("fanout result=%+v", result)
	}
	candidate, err := controlcontract.RestoreCatalogGeneration(
		result.Publication.CatalogCanonical,
		result.Publication.CatalogRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := candidate.FindInstance("instance-a"); !found {
		t.Fatal("fanout Disable removed still-referenced Catalog entry")
	}
}

func TestEvaluateDisableAlreadyAppliedAndNoChangeV1(t *testing.T) {
	port := actionPortV1()
	previous := freezeDisableFactsV1(t, []controlcontract.ProfileDefinition{
		profileV1("profile-a", []controlcontract.BindingSpec{
			bindingV1(port, "instance-a", "1"),
		}),
	}, nil, []controlcontract.CatalogEntry{
		catalogEntryV1("module.a", "instance-a", port, "a"),
	}, 4)
	input := inputV1(port, 4, "profile-a", "instance-a", "b")
	would, err := EvaluateV1(
		context.Background(),
		&disableReadViewV1{loads: []disableFactsV1{previous}},
		func(context.Context, controlcontract.CatalogGeneration, string) error { return nil },
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	current := factsFromPublicationV1(t, *would.Publication)
	retryView := &disableReadViewV1{
		loads: []disableFactsV1{current},
		revisions: map[[2]uint64]disableFactsV1{
			{previous.control.Revision, previous.catalog.Generation}: previous,
		},
	}
	retry, err := EvaluateV1(
		context.Background(),
		retryView,
		func(context.Context, controlcontract.CatalogGeneration, string) error { return nil },
		input,
	)
	if err != nil || retry.Status != StatusAlreadyAppliedV1 || retry.Publication != nil ||
		retry.PreconditionBasis != previous.basis || retry.CandidateBasis != current.basis {
		t.Fatalf("exact retry=%+v err=%v", retry, err)
	}
	_, err = EvaluateV1(
		context.Background(),
		&disableReadViewV1{loads: []disableFactsV1{current}},
		func(context.Context, controlcontract.CatalogGeneration, string) error { return nil },
		input,
	)
	assertFailureCodeV1(t, err, FailureStoreInvalidV1)
	noChangeInput := inputV1(port, current.basis.PointerRevision, "profile-a", "instance-a", "c")
	noChange, err := EvaluateV1(
		context.Background(),
		&disableReadViewV1{loads: []disableFactsV1{current}},
		func(context.Context, controlcontract.CatalogGeneration, string) error { return nil },
		noChangeInput,
	)
	if err != nil || noChange.Status != StatusNoChangeV1 || noChange.Publication != nil ||
		noChange.PreconditionBasis != current.basis ||
		noChange.CatalogChange != CatalogChangeNoneV1 {
		t.Fatalf("no change=%+v err=%v", noChange, err)
	}
}

func TestEvaluateWorkspaceChannelDisableV1(t *testing.T) {
	port := moduleapi.PortRef{
		Name:         moduleapi.PortNameChannelTransport,
		ExactVersion: moduleapi.PortVersionV1,
	}
	endpoint := controlcontract.ChannelEndpointDefinition{
		SchemaVersion:   controlcontract.ChannelEndpointSchemaVersionV1,
		EndpointID:      "endpoint-a",
		Channel:         "loopback-http",
		AccountID:       "account-a",
		ConversationID:  "conversation-a",
		TargetAgentID:   "agent-a",
		TargetProfileID: "profile-a",
		CursorScopeKey:  "cursor-a",
		Enabled:         true,
		Binding:         bindingV1(port, "channel-a", "9"),
	}
	workspace := workspaceV1(endpoint)
	facts := freezeDisableFactsV1(t, []controlcontract.ProfileDefinition{
		profileV1("profile-a", nil),
	}, []controlcontract.WorkspaceDefinition{workspace}, []controlcontract.CatalogEntry{
		catalogEntryV1("module.channel", "channel-a", port, "f"),
	}, 9)
	input := inputV1(port, 9, "", "channel-a", "1")
	input.TargetKind = TargetWorkspaceChannelEndpointV1
	input.ProfileID = ""
	input.WorkspaceID = "workspace-a"
	input.EndpointID = "endpoint-a"
	result, err := EvaluateV1(
		context.Background(),
		&disableReadViewV1{loads: []disableFactsV1{facts}},
		func(context.Context, controlcontract.CatalogGeneration, string) error { return nil },
		input,
	)
	if err != nil || result.Status != StatusWouldApplyV1 ||
		result.BindingRemoval.PortBindingIndex == nil ||
		*result.BindingRemoval.PortBindingIndex != 0 ||
		result.CatalogChange != CatalogChangeRemoveInstanceV1 {
		t.Fatalf("Channel Disable=%+v err=%v", result, err)
	}
	candidate, err := controlcontract.RestoreControlSnapshot(
		result.Publication.ControlCanonical,
		result.Publication.ControlRef,
	)
	if err != nil || len(candidate.Workspaces[0].ChannelEndpoints) != 0 {
		t.Fatalf("Channel candidate=%+v err=%v", candidate, err)
	}
}

func TestEvaluateDisableFailureMatrixV1(t *testing.T) {
	port := actionPortV1()
	facts := freezeDisableFactsV1(t, []controlcontract.ProfileDefinition{
		profileV1("profile-a", []controlcontract.BindingSpec{
			bindingV1(port, "instance-a", "1"),
		}),
	}, nil, []controlcontract.CatalogEntry{
		catalogEntryV1("module.a", "instance-a", port, "a"),
	}, 2)
	valid := inputV1(port, 2, "profile-a", "instance-a", "b")
	wrongCandidate := valid
	wrongCandidate.CandidateControlSnapshotID =
		moduleapplyplan.CandidateControlSnapshotIDPrefixV1 + strings.Repeat("f", 64)
	wrongCatalogCandidate := valid
	wrongCatalogCandidate.CandidateCatalogGenerationID =
		moduleapplyplan.CandidateCatalogGenerationIDPrefixV1 + strings.Repeat("f", 64)
	verifier := func(context.Context, controlcontract.CatalogGeneration, string) error { return nil }
	tests := []struct {
		name     string
		ctx      context.Context
		view     ReadViewV1
		verifier ArtifactVerifierV1
		input    InputV1
		want     FailureCodeV1
	}{
		{name: "nil context", view: &disableReadViewV1{}, verifier: verifier, input: valid, want: FailureInvalidInputV1},
		{name: "typed nil view", ctx: context.Background(), view: (*disableReadViewV1)(nil), verifier: verifier, input: valid, want: FailureInvalidInputV1},
		{name: "nil verifier", ctx: context.Background(), view: &disableReadViewV1{}, input: valid, want: FailureInvalidInputV1},
		{name: "invalid projection", ctx: context.Background(), view: &disableReadViewV1{}, verifier: verifier, input: InputV1{}, want: FailureInvalidInputV1},
		{name: "caller-selected candidate", ctx: context.Background(), view: &disableReadViewV1{}, verifier: verifier, input: wrongCandidate, want: FailureInvalidInputV1},
		{name: "caller-selected catalog candidate", ctx: context.Background(), view: &disableReadViewV1{}, verifier: verifier, input: wrongCatalogCandidate, want: FailureInvalidInputV1},
		{name: "cancelled", ctx: cancelledContextV1(), view: &disableReadViewV1{}, verifier: verifier, input: valid, want: FailureCancelledV1},
		{name: "Store read", ctx: context.Background(), view: &disableReadViewV1{loadErr: errors.New("read")}, verifier: verifier, input: valid, want: FailureStoreInvalidV1},
		{name: "artifact", ctx: context.Background(), view: &disableReadViewV1{loads: []disableFactsV1{facts}}, verifier: func(context.Context, controlcontract.CatalogGeneration, string) error { return errors.New("artifact") }, input: valid, want: FailureArtifactInvalidV1},
		{name: "closure", ctx: context.Background(), view: &disableReadViewV1{loads: []disableFactsV1{facts}, verifyErr: errors.New("closure")}, verifier: verifier, input: valid, want: FailureStoreInvalidV1},
		{name: "pointer", ctx: context.Background(), view: &disableReadViewV1{loads: []disableFactsV1{facts}}, verifier: verifier, input: withExpectedPointerV1(valid, 1), want: FailurePointerConflictV1},
		{name: "target", ctx: context.Background(), view: &disableReadViewV1{loads: []disableFactsV1{facts}}, verifier: verifier, input: withProfileV1(valid, "absent"), want: FailureTargetConflictV1},
		{name: "final drift", ctx: context.Background(), view: &disableReadViewV1{loads: []disableFactsV1{facts, withPointerV1(facts, 3)}}, verifier: verifier, input: valid, want: FailurePointerConflictV1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := EvaluateV1(test.ctx, test.view, test.verifier, test.input)
			var failure *FailureV1
			if !errors.As(err, &failure) || failure.Code() != test.want {
				t.Fatalf("failure=%v typed=%+v want=%s", err, failure, test.want)
			}
		})
	}
}

func TestEvaluateExactRetryRejectsDifferentCanonicalPublicationV1(t *testing.T) {
	port := actionPortV1()
	previous := freezeDisableFactsV1(t, []controlcontract.ProfileDefinition{
		profileV1("profile-a", []controlcontract.BindingSpec{
			bindingV1(port, "instance-a", "1"),
		}),
	}, nil, []controlcontract.CatalogEntry{
		catalogEntryV1("module.a", "instance-a", port, "a"),
	}, 5)
	input := inputV1(port, 5, "profile-a", "instance-a", "b")
	would, err := EvaluateV1(
		context.Background(),
		&disableReadViewV1{loads: []disableFactsV1{previous}},
		func(context.Context, controlcontract.CatalogGeneration, string) error { return nil },
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	current := factsFromPublicationV1(t, *would.Publication)
	current.control.SnapshotID = "different-plan"
	_, currentControlRef, _, err := controlcontract.NewControlSnapshot(current.control)
	if err != nil {
		t.Fatal(err)
	}
	current.basis.Control = currentControlRef
	_, err = EvaluateV1(
		context.Background(),
		&disableReadViewV1{
			loads: []disableFactsV1{current},
			revisions: map[[2]uint64]disableFactsV1{
				{previous.control.Revision, previous.catalog.Generation}: previous,
			},
		},
		func(context.Context, controlcontract.CatalogGeneration, string) error { return nil },
		input,
	)
	assertFailureCodeV1(t, err, FailurePointerConflictV1)
}

func freezeDisableFactsV1(
	t *testing.T,
	profiles []controlcontract.ProfileDefinition,
	workspaces []controlcontract.WorkspaceDefinition,
	entries []controlcontract.CatalogEntry,
	pointer uint64,
) disableFactsV1 {
	t.Helper()
	agents := []corecontract.AgentRef(nil)
	if len(workspaces) != 0 {
		agents = []corecontract.AgentRef{{
			ID: "agent-a", Version: "v1", Digest: hashV1("7"),
		}}
	}
	control, controlRef, _, err := controlcontract.NewControlSnapshot(
		controlcontract.ControlSnapshot{
			SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV1,
			SnapshotID:    "control-observed",
			TenantID:      "tenant-a",
			Revision:      pointer,
			Agents:        agents,
			Workspaces:    workspaces,
			Profiles:      profiles,
		},
	)
	if err != nil {
		t.Fatalf("freeze Control: %v", err)
	}
	catalog, catalogRef, _, err := controlcontract.NewCatalogGeneration(
		controlcontract.CatalogGeneration{
			SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
			GenerationID:          "catalog-observed",
			Generation:            pointer,
			TenantID:              "tenant-a",
			ControlSnapshotID:     controlRef.SnapshotID,
			ControlSnapshotDigest: controlRef.Digest,
			Entries:               entries,
		},
	)
	if err != nil {
		t.Fatalf("freeze Catalog: %v", err)
	}
	return disableFactsV1{
		basis: controlcontract.PublishedBasis{
			TenantID:        "tenant-a",
			PointerRevision: pointer,
			Control:         controlRef,
			Catalog:         catalogRef,
		},
		control: control,
		catalog: catalog,
	}
}

func factsFromPublicationV1(t *testing.T, publication PublicationV1) disableFactsV1 {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		publication.ControlCanonical,
		publication.ControlRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		publication.CatalogCanonical,
		publication.CatalogRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	return disableFactsV1{
		basis: controlcontract.PublishedBasis{
			TenantID:        control.TenantID,
			PointerRevision: publication.NewPointerRevision,
			Control:         publication.ControlRef,
			Catalog:         publication.CatalogRef,
		},
		control: control,
		catalog: catalog,
	}
}

func cloneFactsV1(t *testing.T, facts disableFactsV1) disableFactsV1 {
	t.Helper()
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(facts.control)
	if err != nil {
		t.Fatal(err)
	}
	control, err := controlcontract.RestoreControlSnapshot(controlCanonical, controlRef)
	if err != nil {
		t.Fatal(err)
	}
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(facts.catalog)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(catalogCanonical, catalogRef)
	if err != nil {
		t.Fatal(err)
	}
	return disableFactsV1{basis: facts.basis, control: control, catalog: catalog}
}

func profileV1(
	id string,
	bindings []controlcontract.BindingSpec,
) controlcontract.ProfileDefinition {
	return controlcontract.ProfileDefinition{
		Profile: corecontract.ProfileRef{ID: id, Version: "v1", Digest: hashV1("1")},
		ContextPolicy: corecontract.PolicyRef{
			ID: "context." + id, Version: "v1", Digest: hashV1("2"),
		},
		CostPolicy: corecontract.PolicyRef{
			ID: "cost." + id, Version: "v1", Digest: hashV1("3"),
		},
		SchedulingPolicy: corecontract.PolicyRef{
			ID: "schedule." + id, Version: "v1", Digest: hashV1("4"),
		},
		Bindings: bindings,
	}
}

func workspaceV1(
	endpoint controlcontract.ChannelEndpointDefinition,
) controlcontract.WorkspaceDefinition {
	return controlcontract.WorkspaceDefinition{
		Workspace: corecontract.WorkspaceRef{
			ID: "workspace-a", Version: "v1", Digest: hashV1("5"),
		},
		BudgetPolicy: corecontract.PolicyRef{
			ID: "budget.workspace-a", Version: "v1", Digest: hashV1("6"),
		},
		ChannelEndpoints: []controlcontract.ChannelEndpointDefinition{endpoint},
		ChannelIdentities: []controlcontract.ChannelIdentityDefinition{{
			Channel: "loopback-http", AccountID: "account-a",
			ExternalUserID: "external-a", PrincipalID: "principal-a",
			ACLEpoch: 1, Active: true,
		}},
	}
}

func bindingV1(
	port moduleapi.PortRef,
	instanceID string,
	digestCharacter string,
) controlcontract.BindingSpec {
	static := []string(nil)
	if port.Name == moduleapi.PortNameContextProvide {
		static = []string{hashV1("8")}
	}
	return controlcontract.BindingSpec{
		Port:                port,
		InstanceID:          instanceID,
		ConfigRef:           hashV1(digestCharacter),
		AuthorityCeilingRef: hashV1(strings.ToUpper(digestCharacter)),
		StaticContextRefs:   static,
		FailurePolicy:       moduleapi.FailureRequired,
	}
}

func catalogEntryV1(
	moduleID string,
	instanceID string,
	port moduleapi.PortRef,
	digestCharacter string,
) controlcontract.CatalogEntry {
	class := moduleapi.ExecutionTrustedInProcess
	if port.Name == moduleapi.PortNameContextProvide {
		class = moduleapi.ExecutionDeclarative
	}
	return controlcontract.CatalogEntry{
		Activation: moduleapi.ActivatedModuleRef{
			ModuleID: moduleID, Version: "v1", ArtifactDigest: hashV1(digestCharacter),
			InstanceID: instanceID, ExecutionClass: class,
			AdapterIdentity: "adapter." + moduleID, ActivationRevision: 1,
		},
		Provides: []moduleapi.PortRef{port},
	}
}

func actionPortV1() moduleapi.PortRef {
	return moduleapi.PortRef{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}
}

func inputV1(
	port moduleapi.PortRef,
	expected uint64,
	profileID string,
	instanceID string,
	digestCharacter string,
) InputV1 {
	digest := hashV1(digestCharacter)
	candidateIDs, err := moduleapplyplan.DeriveCandidateIDsV1(digest)
	if err != nil {
		panic(err)
	}
	return InputV1{
		TenantID: "tenant-a", ExpectedPointerRevision: expected,
		TargetKind: TargetProfileV1, ProfileID: profileID,
		InstanceID: instanceID, Port: port, PlanDigest: digest,
		CandidateControlSnapshotID:   candidateIDs.ControlSnapshotID,
		CandidateCatalogGenerationID: candidateIDs.CatalogGenerationID,
	}
}

func hashV1(character string) string {
	return strings.Repeat(strings.ToLower(character), moduleapi.SHA256HexLength)
}

func cancelledContextV1() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func withExpectedPointerV1(input InputV1, expected uint64) InputV1 {
	input.ExpectedPointerRevision = expected
	return input
}

func withProfileV1(input InputV1, profileID string) InputV1 {
	input.ProfileID = profileID
	return input
}

func withPointerV1(facts disableFactsV1, pointer uint64) disableFactsV1 {
	facts.basis.PointerRevision = pointer
	return facts
}

func assertFailureCodeV1(t *testing.T, err error, want FailureCodeV1) {
	t.Helper()
	var failure *FailureV1
	if !errors.As(err, &failure) || failure.Code() != want {
		t.Fatalf("failure=%v typed=%+v want=%s", err, failure, want)
	}
}
