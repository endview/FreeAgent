package moduledisablecontract

import (
	"bytes"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleDisableBodyCanonicalStrictRoundTripV1(t *testing.T) {
	input := moduleDisableBodyFixtureV1()
	frozen, canonical, digest, err := NewModuleDisableDryRunBodyV1(input)
	if err != nil {
		t.Fatal(err)
	}
	const wantCanonical = `{"binding_target":{"kind":"PROFILE","profile_id":"profile-a"},"expected_pointer_revision":7,"instance_id":"instance-a","port":{"exact_version":"v1","name":"action.provider"},"schema_version":"control-module-disable-dry-run-input/v1"}`
	const wantDigest = "7fa5d6357cad71962d0f17a51ea7e36d41551391c30a0015c920f5df9647d0d5"
	if string(canonical) != wantCanonical || digest != wantDigest || frozen != input {
		t.Fatalf("body canary drift: digest=%s canonical=%s", digest, canonical)
	}
	restored, err := RestoreModuleDisableDryRunBodyV1(canonical, digest)
	if err != nil || restored != input {
		t.Fatalf("restore body: restored=%+v err=%v", restored, err)
	}
	canonical[0] ^= 0xff
	again, err := RestoreModuleDisableDryRunBodyV1([]byte(wantCanonical), wantDigest)
	if err != nil || again != input {
		t.Fatalf("body aliases returned bytes: restored=%+v err=%v", again, err)
	}

	exact := []byte(wantCanonical)
	invalid := map[string][]byte{
		"unknown": bytes.Replace(
			exact,
			[]byte(`{"binding_target"`),
			[]byte(`{"automatic":true,"binding_target"`),
			1,
		),
		"duplicate": append(
			[]byte(`{"schema_version":"control-module-disable-dry-run-input/v1",`),
			exact[1:]...,
		),
		"explicit null": bytes.Replace(
			exact,
			[]byte(`"instance_id":"instance-a"`),
			[]byte(`"instance_id":null`),
			1,
		),
		"trailing":     append(bytes.Clone(exact), []byte(`{}`)...),
		"noncanonical": append([]byte(" "), exact...),
	}
	for name, wire := range invalid {
		t.Run(name, func(t *testing.T) {
			wireDigest := moduleapi.Digest(ModuleDisableDryRunInputDigestDomainV1, wire)
			if _, err := RestoreModuleDisableDryRunBodyV1(wire, wireDigest); err == nil {
				t.Fatal("invalid body accepted")
			}
		})
	}
}

func TestModuleDisableBodyTargetMatrixV1(t *testing.T) {
	base := moduleDisableBodyFixtureV1()
	mutations := map[string]func(*ModuleDisableDryRunBodyV1){
		"zero revision": func(value *ModuleDisableDryRunBodyV1) {
			value.ExpectedPointerRevision = 0
		},
		"cross target field": func(value *ModuleDisableDryRunBodyV1) {
			value.BindingTarget.WorkspaceID = "workspace-a"
		},
		"invalid instance": func(value *ModuleDisableDryRunBodyV1) {
			value.InstanceID = " instance-a"
		},
		"invalid port": func(value *ModuleDisableDryRunBodyV1) {
			value.Port.ExactVersion = ""
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if _, _, _, err := NewModuleDisableDryRunBodyV1(candidate); err == nil {
				t.Fatal("invalid body accepted")
			}
		})
	}
	channel := base
	channel.BindingTarget = ModuleBindingTargetV1{
		Kind:        ModuleBindingTargetWorkspaceChannelEndpointV1,
		WorkspaceID: "workspace-a",
		EndpointID:  "endpoint-a",
	}
	channel.Port = moduleapi.PortRef{
		Name:         moduleapi.PortNameChannelTransport,
		ExactVersion: moduleapi.PortVersionV1,
	}
	if _, _, _, err := NewModuleDisableDryRunBodyV1(channel); err != nil {
		t.Fatalf("valid Channel target rejected: %v", err)
	}
	c1Instance := base
	c1Instance.InstanceID = "a\u0080b"
	if _, _, _, err := NewModuleDisableDryRunBodyV1(c1Instance); err != nil {
		t.Fatalf("valid canonical module instance C1 ID rejected: %v", err)
	}
}

func TestModuleDisableEvaluationCanonicalStrictRoundTripV1(t *testing.T) {
	input := moduleDisableEvaluationFixtureV1(t)
	frozen, canonical, digest, err := NewModuleDisableEvaluationV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "ae09fced18f240656fb2b67054125a2752121e5868c64caa0ea15e00c8fce72e" {
		t.Fatalf("evaluation canary=%s canonical=%s", digest, canonical)
	}
	restored, err := RestoreModuleDisableEvaluationV1(canonical, digest)
	if err != nil || restored.Projection.BindingRemoval == nil ||
		restored.Projection.BindingRemoval.StaticContextRefs[0] != digestByteV1('e') {
		t.Fatalf("restore evaluation: restored=%+v err=%v", restored, err)
	}
	input.Projection.BindingRemoval.StaticContextRefs[0] = digestByteV1('0')
	if frozen.Projection.BindingRemoval.StaticContextRefs[0] != digestByteV1('e') {
		t.Fatal("frozen evaluation aliases caller slice")
	}

	invalid := map[string][]byte{
		"unknown": bytes.Replace(
			canonical,
			[]byte(`{"expected_ref"`),
			[]byte(`{"automatic":true,"expected_ref"`),
			1,
		),
		"duplicate": append(
			[]byte(`{"schema_version":"control-module-disable-evaluation/v1",`),
			canonical[1:]...,
		),
		"explicit null array": bytes.Replace(
			canonical,
			[]byte(`"static_context_refs":["`+digestByteV1('e')+`"]`),
			[]byte(`"static_context_refs":null`),
			1,
		),
		"trailing": append(bytes.Clone(canonical), []byte(`{}`)...),
	}
	for name, wire := range invalid {
		t.Run(name, func(t *testing.T) {
			wireDigest := moduleapi.Digest(moduleDisableEvaluationDigestDomainV1, wire)
			if _, err := RestoreModuleDisableEvaluationV1(wire, wireDigest); err == nil {
				t.Fatal("invalid evaluation accepted")
			}
		})
	}
}

func TestModuleDisableEvaluationAcceptsModuleInstanceC1IDV1(t *testing.T) {
	t.Parallel()
	input := moduleDisableEvaluationFixtureV1(t)
	input.Projection.InstanceID = "a\u0080b"
	if _, _, _, err := NewModuleDisableEvaluationV1(input); err != nil {
		t.Fatalf("valid canonical module instance C1 ID rejected: %v", err)
	}
}

func TestModuleDisableEvaluationProjectionMatrixV1(t *testing.T) {
	base := moduleDisableEvaluationFixtureV1(t)
	mutations := map[string]func(*ModuleDisableEvaluationV1){
		"wrong operation": func(value *ModuleDisableEvaluationV1) {
			value.Operation = controlapicontract.OperationModuleApplyV1
		},
		"input digest": func(value *ModuleDisableEvaluationV1) {
			value.InputDigest = "bad"
		},
		"expected ref": func(value *ModuleDisableEvaluationV1) {
			value.ExpectedRef.Revision++
		},
		"plan digest": func(value *ModuleDisableEvaluationV1) {
			value.Projection.PlanDigest = "bad"
		},
		"candidate adjacency": func(value *ModuleDisableEvaluationV1) {
			value.Projection.CandidateBasis.PointerRevision++
		},
		"missing removal": func(value *ModuleDisableEvaluationV1) {
			value.Projection.BindingRemoval = nil
		},
		"wrong target": func(value *ModuleDisableEvaluationV1) {
			value.Projection.BindingRemoval.Target.Kind =
				ModuleBindingTargetWorkspaceChannelEndpointV1
		},
		"wrong port": func(value *ModuleDisableEvaluationV1) {
			value.Projection.BindingRemoval.Port.Name = moduleapi.PortNameModelGenerate
		},
		"required": func(value *ModuleDisableEvaluationV1) {
			value.Projection.BindingRemoval.FailurePolicy = moduleapi.FailureRequired
		},
		"duplicate static ref": func(value *ModuleDisableEvaluationV1) {
			value.Projection.BindingRemoval.StaticContextRefs = append(
				value.Projection.BindingRemoval.StaticContextRefs,
				value.Projection.BindingRemoval.StaticContextRefs[0],
			)
		},
		"no catalog effect": func(value *ModuleDisableEvaluationV1) {
			value.Projection.CatalogChange = ModuleDisableCatalogNoneV1
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := cloneEvaluationFixtureV1(base)
			mutate(&candidate)
			if _, _, _, err := NewModuleDisableEvaluationV1(candidate); err == nil {
				t.Fatal("invalid evaluation accepted")
			}
		})
	}

	noChange := cloneEvaluationFixtureV1(base)
	noChange.Projection.Disposition = ModuleDisableNoChangeV1
	noChange.Projection.CandidateBasis = noChange.Projection.ObservedBasis
	noChange.Projection.BindingRemoval = nil
	noChange.Projection.CatalogChange = ModuleDisableCatalogNoneV1
	if _, _, _, err := NewModuleDisableEvaluationV1(noChange); err != nil {
		t.Fatalf("valid NO_CHANGE rejected: %v", err)
	}
	already := cloneEvaluationFixtureV1(base)
	already.Projection.Disposition = ModuleDisableAlreadyAppliedV1
	already.Projection.ObservedBasis = already.Projection.CandidateBasis
	already.Projection.BindingRemoval = nil
	already.Projection.CatalogChange = ModuleDisableCatalogNoneV1
	if _, _, _, err := NewModuleDisableEvaluationV1(already); err != nil {
		t.Fatalf("valid ALREADY_APPLIED rejected: %v", err)
	}
}

func TestModuleDisablePublicationCanonicalStrictRoundTripV1(t *testing.T) {
	input := moduleDisablePublicationFixtureV1(t)
	frozen, canonical, ref, err := NewModuleDisablePublicationReceiptV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != "f80fa9047b5c9a478611221c5e17c0a906973ad0e02071abbe718c4891d6649b" ||
		ref.Digest != "dacd979cfe53f09f24363c5aac2ad41f8a35cdcba8e3da5bb187efc6a6880468" ||
		ref.ID == ref.Digest {
		t.Fatalf("publication canary drift: ref=%+v canonical=%s", ref, canonical)
	}
	for _, forbidden := range []string{
		"principal", "idempotency", "request_digest", "confirmation", "session", "completed_at",
	} {
		if bytes.Contains(canonical, []byte(forbidden)) {
			t.Fatalf("identity/dynamic field %q leaked into domain receipt", forbidden)
		}
	}
	restored, err := RestoreModuleDisablePublicationReceiptV1(canonical, ref)
	if err != nil || restored.PlanDigest != input.PlanDigest ||
		!bytes.Equal(restored.DisabledPlan, input.DisabledPlan) {
		t.Fatalf("restore publication: restored=%+v err=%v", restored, err)
	}
	input.DisabledPlan[0] ^= 0xff
	input.RemovedBinding.StaticContextRefs[0] = digestByteV1('0')
	if frozen.DisabledPlan[0] != '{' ||
		frozen.RemovedBinding.StaticContextRefs[0] != digestByteV1('e') {
		t.Fatal("frozen publication aliases caller bytes")
	}

	nullArray := bytes.Replace(
		canonical,
		[]byte(`"static_context_refs":["`+digestByteV1('e')+`"]`),
		[]byte(`"static_context_refs":null`),
		1,
	)
	nullRef := domainRefForWireV1(nullArray)
	if _, err := RestoreModuleDisablePublicationReceiptV1(nullArray, nullRef); err == nil {
		t.Fatal("explicit-null static refs accepted")
	}
	duplicate := append(
		[]byte(`{"schema_version":"module-disable-publication-receipt/v1",`),
		canonical[1:]...,
	)
	if _, err := RestoreModuleDisablePublicationReceiptV1(
		duplicate,
		domainRefForWireV1(duplicate),
	); err == nil {
		t.Fatal("duplicate publication field accepted")
	}
}

func TestModuleDisablePublicationPlanBasisEffectClosureV1(t *testing.T) {
	base := moduleDisablePublicationFixtureV1(t)
	mutations := map[string]func(*ModuleDisablePublicationReceiptV1){
		"plan digest": func(value *ModuleDisablePublicationReceiptV1) {
			value.PlanDigest = digestByteV1('0')
		},
		"pre tenant": func(value *ModuleDisablePublicationReceiptV1) {
			value.PreBasis.TenantID = "tenant-other"
		},
		"pre revision": func(value *ModuleDisablePublicationReceiptV1) {
			value.PreBasis.PointerRevision--
		},
		"post adjacency": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.PointerRevision++
		},
		"candidate control": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.Control.ID = "control-other"
		},
		"candidate catalog": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.Catalog.ID = "catalog-other"
		},
		"removed profile": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.Target.ProfileID = "profile-other"
		},
		"removed instance": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.InstanceID = "instance-other"
		},
		"removed port": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.Port.Name = moduleapi.PortNameModelGenerate
		},
		"removed policy": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.FailurePolicy = moduleapi.FailureRequired
		},
		"catalog none": func(value *ModuleDisablePublicationReceiptV1) {
			value.CatalogChange = ModuleDisableCatalogNoneV1
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := cloneModuleDisablePublicationReceiptV1(base)
			mutate(&candidate)
			if _, _, _, err := NewModuleDisablePublicationReceiptV1(candidate); err == nil {
				t.Fatal("invalid publication accepted")
			}
		})
	}
}

func TestModuleDisableStableArraysNormalizeToEmptyV1(t *testing.T) {
	evaluation := moduleDisableEvaluationFixtureV1(t)
	evaluation.Projection.BindingRemoval.StaticContextRefs = nil
	frozenEvaluation, evaluationCanonical, _, err :=
		NewModuleDisableEvaluationV1(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	if frozenEvaluation.Projection.BindingRemoval.StaticContextRefs == nil ||
		!bytes.Contains(evaluationCanonical, []byte(`"static_context_refs":[]`)) {
		t.Fatalf("evaluation empty refs not normalized: %s", evaluationCanonical)
	}

	publication := moduleDisablePublicationFixtureV1(t)
	publication.RemovedBinding.StaticContextRefs = nil
	frozenNil, canonicalNil, refNil, err :=
		NewModuleDisablePublicationReceiptV1(publication)
	if err != nil {
		t.Fatal(err)
	}
	publication.RemovedBinding.StaticContextRefs = []string{}
	_, canonicalEmpty, refEmpty, err := NewModuleDisablePublicationReceiptV1(publication)
	if err != nil {
		t.Fatal(err)
	}
	if frozenNil.RemovedBinding.StaticContextRefs == nil ||
		!bytes.Equal(canonicalNil, canonicalEmpty) || refNil != refEmpty ||
		!bytes.Contains(canonicalNil, []byte(`"static_context_refs":[]`)) {
		t.Fatalf("publication empty refs not normalized: %s / %s", canonicalNil, canonicalEmpty)
	}
}

func moduleDisableBodyFixtureV1() ModuleDisableDryRunBodyV1 {
	return ModuleDisableDryRunBodyV1{
		SchemaVersion:           ModuleDisableDryRunBodySchemaVersionV1,
		ExpectedPointerRevision: 7,
		BindingTarget: ModuleBindingTargetV1{
			Kind:      ModuleBindingTargetProfileV1,
			ProfileID: "profile-a",
		},
		InstanceID: "instance-a",
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameActionProvider,
			ExactVersion: moduleapi.PortVersionV1,
		},
	}
}

func moduleDisableEvaluationFixtureV1(t *testing.T) ModuleDisableEvaluationV1 {
	t.Helper()
	before := controlapicontract.PublishedBasisRefV1{
		TenantID:        "tenant-evaluation",
		PointerRevision: 9,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID: "control-evaluation-9", Revision: 9, Digest: digestByteV1('a'),
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID: "catalog-evaluation-9", Revision: 9, Digest: digestByteV1('b'),
		},
	}
	after := controlapicontract.PublishedBasisRefV1{
		TenantID:        "tenant-evaluation",
		PointerRevision: 10,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID: "control-evaluation-10", Revision: 10, Digest: digestByteV1('c'),
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID: "catalog-evaluation-10", Revision: 10, Digest: digestByteV1('d'),
		},
	}
	expected, err := publishedPointerRefV1(before)
	if err != nil {
		t.Fatal(err)
	}
	return ModuleDisableEvaluationV1{
		SchemaVersion: ModuleDisableEvaluationSchemaVersionV1,
		Operation:     controlapicontract.OperationModuleDisableV1,
		InputDigest:   digestByteV1('8'),
		ExpectedRef:   expected,
		Projection: ModuleDisableProjectionV1{
			Disposition:       ModuleDisableWouldApplyV1,
			PlanDigest:        digestByteV1('f'),
			InstanceID:        "instance-evaluation",
			PreconditionBasis: before,
			ObservedBasis:     before,
			CandidateBasis:    after,
			CandidateState:    ModuleDisableProjectedNotReservedV1,
			BindingRemoval: &ModuleDisableBindingRemovalV1{
				Target: ModuleBindingTargetV1{
					Kind: ModuleBindingTargetProfileV1, ProfileID: "profile-evaluation",
				},
				Port: moduleapi.PortRef{
					Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1,
				},
				PortBindingIndex:    1,
				ConfigRef:           digestByteV1('6'),
				AuthorityCeilingRef: digestByteV1('7'),
				StaticContextRefs:   []string{digestByteV1('e')},
				FailurePolicy:       moduleapi.FailureOptional,
			},
			CatalogChange: ModuleDisableCatalogRetainInstanceV1,
		},
	}
}

func moduleDisablePublicationFixtureV1(t *testing.T) ModuleDisablePublicationReceiptV1 {
	t.Helper()
	plan, planCanonical, planDigest, err := moduleapplyplan.FreezeProfileContextDisableV1(
		moduleapplyplan.ProfileContextDisableInputV1{
			TenantID:                "tenant-publication",
			ExpectedPointerRevision: 41,
			ProfileID:               "profile-publication",
			InstanceID:              "instance-publication",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := moduleapplyplan.DeriveCandidateIDsV1(planDigest)
	if err != nil {
		t.Fatal(err)
	}
	pre := controlapicontract.PublishedBasisRefV1{
		TenantID:        plan.TenantID,
		PointerRevision: plan.ExpectedPointerRevision,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID: "control-publication-17", Revision: 17, Digest: digestByteV1('a'),
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID: "catalog-publication-23", Revision: 23, Digest: digestByteV1('b'),
		},
	}
	post := controlapicontract.PublishedBasisRefV1{
		TenantID:        plan.TenantID,
		PointerRevision: plan.ExpectedPointerRevision + 1,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID: candidates.ControlSnapshotID, Revision: 18, Digest: digestByteV1('c'),
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID: candidates.CatalogGenerationID, Revision: 24, Digest: digestByteV1('d'),
		},
	}
	return ModuleDisablePublicationReceiptV1{
		SchemaVersion: ModuleDisablePublicationReceiptSchemaVersionV1,
		DisabledPlan:  planCanonical,
		PlanDigest:    planDigest,
		PreBasis:      pre,
		PostBasis:     post,
		RemovedBinding: ModuleDisablePublicationRemovedBindingV1{
			Target: ModuleBindingTargetV1{
				Kind: ModuleBindingTargetProfileV1, ProfileID: plan.BindingTarget.ProfileID,
			},
			InstanceID:          plan.InstanceID,
			Port:                plan.Port,
			PortBindingIndex:    1,
			ConfigRef:           digestByteV1('6'),
			AuthorityCeilingRef: digestByteV1('7'),
			StaticContextRefs:   []string{digestByteV1('e')},
			FailurePolicy:       moduleapi.FailureOptional,
		},
		CatalogChange: ModuleDisableCatalogRetainInstanceV1,
	}
}

func cloneEvaluationFixtureV1(input ModuleDisableEvaluationV1) ModuleDisableEvaluationV1 {
	cloned := input
	cloned.Projection = cloneModuleDisableProjectionV1(input.Projection)
	return cloned
}

func domainRefForWireV1(wire []byte) controlapicontract.DomainReceiptRefV1 {
	return controlapicontract.DomainReceiptRefV1{
		Kind: controlapicontract.DomainReceiptModuleDisableV1,
		ID:   moduleapi.Digest(moduleDisablePublicationIDDomainV1, wire),
		Digest: moduleapi.Digest(
			moduleDisablePublicationReceiptDomainV1,
			wire,
		),
	}
}

func digestByteV1(value byte) string {
	return string(bytes.Repeat([]byte{value}, 64))
}

func TestModuleDisableContractDomainsRemainDistinctV1(t *testing.T) {
	body := moduleDisableBodyFixtureV1()
	_, bodyCanonical, bodyDigest, err := NewModuleDisableDryRunBodyV1(body)
	if err != nil {
		t.Fatal(err)
	}
	if bodyDigest == moduleapi.Digest(moduleDisableEvaluationDigestDomainV1, bodyCanonical) ||
		strings.Contains(string(bodyCanonical), "proof") {
		t.Fatal("body digest domain or authority-free wire drifted")
	}
}
