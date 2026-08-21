package controlapp

import (
	"bytes"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleDisableEvaluationCanonicalRoundTripAndDefensiveCopyV1(t *testing.T) {
	input := validModuleDisableEvaluationFixtureV1(t)
	frozen, canonical, digest, err := NewModuleDisableEvaluationV1(input)
	if err != nil {
		t.Fatalf("NewModuleDisableEvaluationV1: %v", err)
	}
	if len(canonical) == 0 || !moduleapi.ValidSHA256(digest) {
		t.Fatalf("invalid canonical result: bytes=%d digest=%q", len(canonical), digest)
	}
	if digest != "ae09fced18f240656fb2b67054125a2752121e5868c64caa0ea15e00c8fce72e" {
		t.Fatalf("evaluation digest canary=%s canonical=%s", digest, canonical)
	}
	restored, err := RestoreModuleDisableEvaluationV1(canonical, digest)
	if err != nil {
		t.Fatalf("RestoreModuleDisableEvaluationV1: %v", err)
	}
	if restored.SchemaVersion != input.SchemaVersion ||
		restored.Operation != input.Operation ||
		restored.InputDigest != input.InputDigest ||
		restored.ExpectedRef != input.ExpectedRef ||
		restored.Projection.BindingRemoval == nil ||
		len(restored.Projection.BindingRemoval.StaticContextRefs) != 1 {
		t.Fatalf("restored evaluation drifted: %+v", restored)
	}

	input.Projection.BindingRemoval.StaticContextRefs[0] = digestOfByteV1('0')
	if frozen.Projection.BindingRemoval.StaticContextRefs[0] == input.Projection.BindingRemoval.StaticContextRefs[0] {
		t.Fatal("frozen evaluation aliases caller StaticContextRefs")
	}
	frozen.Projection.BindingRemoval.StaticContextRefs[0] = digestOfByteV1('1')
	again, err := RestoreModuleDisableEvaluationV1(canonical, digest)
	if err != nil || again.Projection.BindingRemoval.StaticContextRefs[0] != digestOfByteV1('e') {
		t.Fatalf("returned evaluation aliases canonical parent: value=%q err=%v", again.Projection.BindingRemoval.StaticContextRefs[0], err)
	}

	for name, mutated := range map[string][]byte{
		"unknown field": bytes.Replace(canonical, []byte(`{"expected_ref"`), []byte(`{"extra":1,"expected_ref"`), 1),
		"trailing JSON": append(bytes.Clone(canonical), []byte(` {}`)...),
		"non canonical": append([]byte(" "), canonical...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RestoreModuleDisableEvaluationV1(mutated, digest); err == nil {
				t.Fatal("mutated evaluation was accepted")
			}
		})
	}
}

func TestModuleDisableEvaluationRejectsBrokenProjectionMatrixV1(t *testing.T) {
	base := validModuleDisableEvaluationFixtureV1(t)
	tests := map[string]func(*ModuleDisableEvaluationV1){
		"schema":       func(value *ModuleDisableEvaluationV1) { value.SchemaVersion = "control-module-disable-evaluation/v2" },
		"operation":    func(value *ModuleDisableEvaluationV1) { value.Operation = controlapicontract.OperationModuleApplyV1 },
		"input digest": func(value *ModuleDisableEvaluationV1) { value.InputDigest = "bad" },
		"expected kind": func(value *ModuleDisableEvaluationV1) {
			value.ExpectedRef.Kind = controlapicontract.ResourceModuleActivationV1
		},
		"plan digest":         func(value *ModuleDisableEvaluationV1) { value.Projection.PlanDigest = "bad" },
		"instance":            func(value *ModuleDisableEvaluationV1) { value.Projection.InstanceID = " instance" },
		"candidate state":     func(value *ModuleDisableEvaluationV1) { value.Projection.CandidateState = "RESERVED" },
		"precondition parent": func(value *ModuleDisableEvaluationV1) { value.Projection.PreconditionBasis.PointerRevision++ },
		"observed parent": func(value *ModuleDisableEvaluationV1) {
			value.Projection.ObservedBasis.Control.Digest = digestOfByteV1('9')
		},
		"candidate pointer":          func(value *ModuleDisableEvaluationV1) { value.Projection.CandidateBasis.PointerRevision++ },
		"candidate control revision": func(value *ModuleDisableEvaluationV1) { value.Projection.CandidateBasis.Control.Revision++ },
		"candidate control identity": func(value *ModuleDisableEvaluationV1) {
			value.Projection.CandidateBasis.Control.ID = value.Projection.ObservedBasis.Control.ID
		},
		"catalog none":    func(value *ModuleDisableEvaluationV1) { value.Projection.CatalogChange = ModuleDisableCatalogNoneV1 },
		"binding missing": func(value *ModuleDisableEvaluationV1) { value.Projection.BindingRemoval = nil },
		"binding target":  func(value *ModuleDisableEvaluationV1) { value.Projection.BindingRemoval.Target.ProfileID = "" },
		"binding port":    func(value *ModuleDisableEvaluationV1) { value.Projection.BindingRemoval.Port.ExactVersion = "" },
		"binding non-context port": func(value *ModuleDisableEvaluationV1) {
			value.Projection.BindingRemoval.Port.Name = moduleapi.PortNameModelGenerate
		},
		"binding required": func(value *ModuleDisableEvaluationV1) {
			value.Projection.BindingRemoval.FailurePolicy = moduleapi.FailureRequired
		},
		"binding ordinal out of range": func(value *ModuleDisableEvaluationV1) {
			value.Projection.BindingRemoval.PortBindingIndex = uint32(moduleapi.MaxManifestEntries)
		},
		"binding workspace endpoint": func(value *ModuleDisableEvaluationV1) {
			value.Projection.BindingRemoval.Target = ModuleBindingTargetV1{
				Kind:        ModuleBindingTargetWorkspaceChannelEndpointV1,
				WorkspaceID: "workspace-evaluation",
				EndpointID:  "endpoint-evaluation",
			}
		},
		"binding config":    func(value *ModuleDisableEvaluationV1) { value.Projection.BindingRemoval.ConfigRef = "bad" },
		"binding authority": func(value *ModuleDisableEvaluationV1) { value.Projection.BindingRemoval.AuthorityCeilingRef = "bad" },
		"binding static":    func(value *ModuleDisableEvaluationV1) { value.Projection.BindingRemoval.StaticContextRefs[0] = "bad" },
		"binding duplicate static": func(value *ModuleDisableEvaluationV1) {
			value.Projection.BindingRemoval.StaticContextRefs = append(
				value.Projection.BindingRemoval.StaticContextRefs,
				value.Projection.BindingRemoval.StaticContextRefs[0],
			)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := cloneModuleDisableEvaluationFixtureV1(base)
			mutate(&value)
			if _, _, _, err := NewModuleDisableEvaluationV1(value); err == nil {
				t.Fatal("broken evaluation was accepted")
			}
		})
	}
}

func TestModuleDisableEvaluationAcceptsClosedNoChangeAndAlreadyAppliedV1(t *testing.T) {
	wouldApply := validModuleDisableEvaluationFixtureV1(t)
	noChange := cloneModuleDisableEvaluationFixtureV1(wouldApply)
	noChange.Projection.Disposition = ModuleDisableNoChangeV1
	noChange.Projection.CandidateBasis = noChange.Projection.ObservedBasis
	noChange.Projection.BindingRemoval = nil
	noChange.Projection.CatalogChange = ModuleDisableCatalogNoneV1
	if _, _, _, err := NewModuleDisableEvaluationV1(noChange); err != nil {
		t.Fatalf("valid NO_CHANGE evaluation: %v", err)
	}
	already := cloneModuleDisableEvaluationFixtureV1(wouldApply)
	already.Projection.Disposition = ModuleDisableAlreadyAppliedV1
	already.Projection.ObservedBasis = already.Projection.CandidateBasis
	already.Projection.BindingRemoval = nil
	already.Projection.CatalogChange = ModuleDisableCatalogNoneV1
	if _, _, _, err := NewModuleDisableEvaluationV1(already); err != nil {
		t.Fatalf("valid ALREADY_APPLIED evaluation: %v", err)
	}
	already.Projection.BindingRemoval = wouldApply.Projection.BindingRemoval
	if _, _, _, err := NewModuleDisableEvaluationV1(already); err == nil {
		t.Fatal("ALREADY_APPLIED accepted a removal effect projection")
	}
}

func validModuleDisableEvaluationFixtureV1(t *testing.T) ModuleDisableEvaluationV1 {
	t.Helper()
	before := controlapicontract.PublishedBasisRefV1{
		TenantID:        "tenant-evaluation",
		PointerRevision: 9,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID: "control-evaluation-9", Revision: 9, Digest: digestOfByteV1('a'),
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID: "catalog-evaluation-9", Revision: 9, Digest: digestOfByteV1('b'),
		},
	}
	after := controlapicontract.PublishedBasisRefV1{
		TenantID:        "tenant-evaluation",
		PointerRevision: 10,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID: "control-evaluation-10", Revision: 10, Digest: digestOfByteV1('c'),
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID: "catalog-evaluation-10", Revision: 10, Digest: digestOfByteV1('d'),
		},
	}
	expected, err := PublishedPointerRefV1(before)
	if err != nil {
		t.Fatalf("PublishedPointerRefV1: %v", err)
	}
	return ModuleDisableEvaluationV1{
		SchemaVersion: ModuleDisableEvaluationSchemaVersionV1,
		Operation:     controlapicontract.OperationModuleDisableV1,
		InputDigest:   digestOfByteV1('8'),
		ExpectedRef:   expected,
		Projection: ModuleDisableProjectionV1{
			Disposition:       ModuleDisableWouldApplyV1,
			PlanDigest:        digestOfByteV1('f'),
			InstanceID:        "instance-evaluation",
			PreconditionBasis: before,
			ObservedBasis:     before,
			CandidateBasis:    after,
			CandidateState:    ModuleDisableProjectedNotReservedV1,
			BindingRemoval: &ModuleDisableBindingRemovalV1{
				Target:              ModuleBindingTargetV1{Kind: ModuleBindingTargetProfileV1, ProfileID: "profile-evaluation"},
				Port:                moduleapi.PortRef{Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1},
				PortBindingIndex:    1,
				ConfigRef:           digestOfByteV1('6'),
				AuthorityCeilingRef: digestOfByteV1('7'),
				StaticContextRefs:   []string{digestOfByteV1('e')},
				FailurePolicy:       moduleapi.FailureOptional,
			},
			CatalogChange: ModuleDisableCatalogRetainInstanceV1,
		},
	}
}

func cloneModuleDisableEvaluationFixtureV1(input ModuleDisableEvaluationV1) ModuleDisableEvaluationV1 {
	cloned := input
	cloned.Projection = input.Projection
	if input.Projection.BindingRemoval != nil {
		binding := *input.Projection.BindingRemoval
		binding.StaticContextRefs = append(
			[]string(nil),
			input.Projection.BindingRemoval.StaticContextRefs...,
		)
		cloned.Projection.BindingRemoval = &binding
	}
	return cloned
}

func digestOfByteV1(value byte) string {
	return string(bytes.Repeat([]byte{value}, 64))
}
