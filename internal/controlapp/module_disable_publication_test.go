package controlapp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleDisablePublicationReceiptCanonicalRoundTripAndCopyV1(t *testing.T) {
	input := validModuleDisablePublicationReceiptFixtureV1(t)
	frozen, canonical, ref, err := NewModuleDisablePublicationReceiptV1(input)
	if err != nil {
		t.Fatalf("NewModuleDisablePublicationReceiptV1: %v", err)
	}
	if ref.Kind != controlapicontract.DomainReceiptModuleDisableV1 ||
		ref.ID != moduleapi.Digest(
			"freeagent.module-disable-publication-id/v1",
			canonical,
		) || ref.Digest != moduleapi.Digest(
		"freeagent.module-disable-publication-receipt/v1",
		canonical,
	) {
		t.Fatalf("domain receipt ref=%+v", ref)
	}
	const wantCanonical = `{"catalog_change":"RETAIN_INSTANCE","disabled_plan":{"binding_target":{"kind":"PROFILE","profile_id":"profile-publication"},"desired_state":"DISABLED","expected_pointer_revision":41,"instance_id":"instance-publication","port":{"exact_version":"v1","name":"context.provide"},"schema_version":"module-apply-plan/v1","tenant_id":"tenant-publication"},"plan_digest":"6bbb61ee71576dabb41a2f455393a1f96fe4e1e72f950f8cdaf24d13b646d508","post_basis":{"catalog":{"digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","id":"module-apply-catalog-v1-6bbb61ee71576dabb41a2f455393a1f96fe4e1e72f950f8cdaf24d13b646d508","revision":24},"control":{"digest":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","id":"module-apply-control-v1-6bbb61ee71576dabb41a2f455393a1f96fe4e1e72f950f8cdaf24d13b646d508","revision":18},"pointer_revision":42,"tenant_id":"tenant-publication"},"pre_basis":{"catalog":{"digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","id":"catalog-publication-23","revision":23},"control":{"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","id":"control-publication-17","revision":17},"pointer_revision":41,"tenant_id":"tenant-publication"},"removed_binding":{"authority_ceiling_ref":"7777777777777777777777777777777777777777777777777777777777777777","config_ref":"6666666666666666666666666666666666666666666666666666666666666666","failure_policy":"OPTIONAL","instance_id":"instance-publication","port":{"exact_version":"v1","name":"context.provide"},"port_binding_index":1,"static_context_refs":["eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"],"target":{"kind":"PROFILE","profile_id":"profile-publication"}},"schema_version":"module-disable-publication-receipt/v1"}`
	if string(canonical) != wantCanonical ||
		ref.ID != "f80fa9047b5c9a478611221c5e17c0a906973ad0e02071abbe718c4891d6649b" ||
		ref.Digest != "dacd979cfe53f09f24363c5aac2ad41f8a35cdcba8e3da5bb187efc6a6880468" {
		t.Fatalf(
			"publication canary drift: id=%s digest=%s canonical=%s",
			ref.ID,
			ref.Digest,
			canonical,
		)
	}
	restored, err := RestoreModuleDisablePublicationReceiptV1(canonical, ref)
	if err != nil {
		t.Fatalf("RestoreModuleDisablePublicationReceiptV1: %v", err)
	}
	if !bytes.Equal(restored.DisabledPlan, input.DisabledPlan) ||
		restored.PlanDigest != input.PlanDigest ||
		restored.PreBasis != input.PreBasis ||
		restored.PostBasis != input.PostBasis ||
		restored.RemovedBinding.Target != input.RemovedBinding.Target ||
		len(restored.RemovedBinding.StaticContextRefs) != 1 {
		t.Fatalf("restored publication receipt drifted: %+v", restored)
	}

	input.DisabledPlan[0] = '['
	input.RemovedBinding.StaticContextRefs[0] = digestOfByteV1('0')
	if frozen.DisabledPlan[0] != '{' ||
		frozen.RemovedBinding.StaticContextRefs[0] != digestOfByteV1('e') {
		t.Fatal("frozen receipt aliases caller-owned plan or static references")
	}
	frozen.DisabledPlan[0] = '['
	frozen.RemovedBinding.StaticContextRefs[0] = digestOfByteV1('1')
	again, err := RestoreModuleDisablePublicationReceiptV1(canonical, ref)
	if err != nil || again.DisabledPlan[0] != '{' ||
		again.RemovedBinding.StaticContextRefs[0] != digestOfByteV1('e') {
		t.Fatalf("canonical aliases prior result: restored=%+v err=%v", again, err)
	}
}

func TestRestoreModuleDisablePublicationReceiptRejectsNonExactWireOrRefV1(t *testing.T) {
	_, canonical, ref, err := NewModuleDisablePublicationReceiptV1(
		validModuleDisablePublicationReceiptFixtureV1(t),
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutated := range map[string][]byte{
		"empty":         nil,
		"unknown field": bytes.Replace(canonical, []byte(`{"catalog_change"`), []byte(`{"automatic":true,"catalog_change"`), 1),
		"trailing JSON": append(bytes.Clone(canonical), []byte(`{}`)...),
		"non canonical": append([]byte(" "), canonical...),
		"oversize":      bytes.Repeat([]byte{'x'}, MaximumModuleDisablePublicationReceiptBytesV1+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RestoreModuleDisablePublicationReceiptV1(mutated, ref); err == nil {
				t.Fatal("mutated publication receipt was accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*controlapicontract.DomainReceiptRefV1){
		"kind": func(value *controlapicontract.DomainReceiptRefV1) {
			value.Kind = controlapicontract.DomainReceiptModuleApplyV1
		},
		"id": func(value *controlapicontract.DomainReceiptRefV1) {
			value.ID = strings.Repeat("0", 64)
		},
		"digest": func(value *controlapicontract.DomainReceiptRefV1) {
			value.Digest = strings.Repeat("0", 64)
		},
	} {
		t.Run("ref "+name, func(t *testing.T) {
			mutated := ref
			mutate(&mutated)
			if _, err := RestoreModuleDisablePublicationReceiptV1(canonical, mutated); err == nil {
				t.Fatal("mismatched domain receipt reference was accepted")
			}
		})
	}
}

func TestModuleDisablePublicationReceiptRejectsBrokenBasisPlanAndEffectV1(t *testing.T) {
	base := validModuleDisablePublicationReceiptFixtureV1(t)
	tests := map[string]func(*ModuleDisablePublicationReceiptV1){
		"schema": func(value *ModuleDisablePublicationReceiptV1) {
			value.SchemaVersion = "module-disable-publication-receipt/v2"
		},
		"noncanonical plan": func(value *ModuleDisablePublicationReceiptV1) {
			value.DisabledPlan = append([]byte(" "), value.DisabledPlan...)
		},
		"plan digest": func(value *ModuleDisablePublicationReceiptV1) {
			value.PlanDigest = digestOfByteV1('0')
		},
		"pre tenant": func(value *ModuleDisablePublicationReceiptV1) {
			value.PreBasis.TenantID = "tenant-other"
		},
		"pre plan revision": func(value *ModuleDisablePublicationReceiptV1) {
			value.PreBasis.PointerRevision--
		},
		"post tenant": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.TenantID = "tenant-other"
		},
		"post pointer adjacency": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.PointerRevision++
		},
		"post control revision": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.Control.Revision++
		},
		"post catalog revision": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.Catalog.Revision++
		},
		"candidate control ID": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.Control.ID = "control-other"
		},
		"candidate catalog ID": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.Catalog.ID = "catalog-other"
		},
		"unchanged control digest": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.Control.Digest = value.PreBasis.Control.Digest
		},
		"unchanged catalog digest": func(value *ModuleDisablePublicationReceiptV1) {
			value.PostBasis.Catalog.Digest = value.PreBasis.Catalog.Digest
		},
		"removed target kind": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.Target.Kind = ModuleBindingTargetWorkspaceChannelEndpointV1
		},
		"removed target profile": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.Target.ProfileID = "profile-other"
		},
		"removed target workspace field": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.Target.WorkspaceID = "workspace-forbidden"
		},
		"removed instance": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.InstanceID = "instance-other"
		},
		"removed port": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.Port.Name = moduleapi.PortNameModelGenerate
		},
		"removed port version": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.Port.ExactVersion = "v2"
		},
		"removed required": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.FailurePolicy = moduleapi.FailureRequired
		},
		"removed ordinal": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.PortBindingIndex = uint32(moduleapi.MaxManifestEntries)
		},
		"removed config": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.ConfigRef = "bad"
		},
		"removed authority": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.AuthorityCeilingRef = "bad"
		},
		"removed static": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.StaticContextRefs[0] = "bad"
		},
		"duplicate static": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.StaticContextRefs = append(
				value.RemovedBinding.StaticContextRefs,
				value.RemovedBinding.StaticContextRefs[0],
			)
		},
		"too many static": func(value *ModuleDisablePublicationReceiptV1) {
			value.RemovedBinding.StaticContextRefs = make(
				[]string,
				moduleapi.MaxManifestEntries+1,
			)
			for index := range value.RemovedBinding.StaticContextRefs {
				value.RemovedBinding.StaticContextRefs[index] = digestOfIndexV1(index)
			}
		},
		"catalog no change": func(value *ModuleDisablePublicationReceiptV1) {
			value.CatalogChange = ModuleDisableCatalogNoneV1
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := cloneModuleDisablePublicationReceiptV1(base)
			mutate(&input)
			if _, _, _, err := NewModuleDisablePublicationReceiptV1(input); err == nil {
				t.Fatal("broken publication receipt was accepted")
			}
		})
	}
}

func digestOfIndexV1(index int) string {
	value := "0123456789abcdef"
	prefix := value[(index>>4)&15]
	suffix := value[index&15]
	return string([]byte{prefix, suffix}) + strings.Repeat("0", 62)
}

func cloneModuleDisablePublicationReceiptV1(
	input ModuleDisablePublicationReceiptV1,
) ModuleDisablePublicationReceiptV1 {
	cloned := input
	cloned.DisabledPlan = bytes.Clone(input.DisabledPlan)
	cloned.RemovedBinding.StaticContextRefs = append(
		[]string{},
		input.RemovedBinding.StaticContextRefs...,
	)
	return cloned
}

func TestModuleDisablePublicationReceiptAcceptsClosedCatalogEffectsV1(t *testing.T) {
	base := validModuleDisablePublicationReceiptFixtureV1(t)
	for _, change := range []ModuleDisableCatalogChangeV1{
		ModuleDisableCatalogRetainInstanceV1,
		ModuleDisableCatalogRemoveInstanceV1,
	} {
		input := cloneModuleDisablePublicationReceiptV1(base)
		input.CatalogChange = change
		if _, _, _, err := NewModuleDisablePublicationReceiptV1(input); err != nil {
			t.Fatalf("valid catalog change %q: %v", change, err)
		}
	}
}

func TestModuleDisablePublicationReceiptNormalizesEmptyStaticRefsV1(t *testing.T) {
	withNil := validModuleDisablePublicationReceiptFixtureV1(t)
	withNil.RemovedBinding.StaticContextRefs = nil
	withEmpty := cloneModuleDisablePublicationReceiptV1(withNil)
	withEmpty.RemovedBinding.StaticContextRefs = []string{}

	frozenNil, canonicalNil, refNil, err :=
		NewModuleDisablePublicationReceiptV1(withNil)
	if err != nil {
		t.Fatalf("New receipt with nil static refs: %v", err)
	}
	frozenEmpty, canonicalEmpty, refEmpty, err :=
		NewModuleDisablePublicationReceiptV1(withEmpty)
	if err != nil {
		t.Fatalf("New receipt with empty static refs: %v", err)
	}
	if frozenNil.RemovedBinding.StaticContextRefs == nil ||
		frozenEmpty.RemovedBinding.StaticContextRefs == nil ||
		!bytes.Equal(canonicalNil, canonicalEmpty) || refNil != refEmpty ||
		!bytes.Contains(canonicalNil, []byte(`"static_context_refs":[]`)) ||
		bytes.Contains(canonicalNil, []byte(`"static_context_refs":null`)) {
		t.Fatalf(
			"empty static refs did not normalize: nil=%s empty=%s refs=%+v/%+v",
			canonicalNil,
			canonicalEmpty,
			refNil,
			refEmpty,
		)
	}
}

func validModuleDisablePublicationReceiptFixtureV1(
	t *testing.T,
) ModuleDisablePublicationReceiptV1 {
	t.Helper()
	plan, planCanonical, planDigest, err :=
		moduleapplyplan.FreezeProfileContextDisableV1(
			moduleapplyplan.ProfileContextDisableInputV1{
				TenantID:                "tenant-publication",
				ExpectedPointerRevision: 41,
				ProfileID:               "profile-publication",
				InstanceID:              "instance-publication",
			},
		)
	if err != nil {
		t.Fatalf("FreezeProfileContextDisableV1: %v", err)
	}
	candidates, err := moduleapplyplan.DeriveCandidateIDsV1(planDigest)
	if err != nil {
		t.Fatalf("DeriveCandidateIDsV1: %v", err)
	}
	pre := controlapicontract.PublishedBasisRefV1{
		TenantID:        plan.TenantID,
		PointerRevision: plan.ExpectedPointerRevision,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID: "control-publication-17", Revision: 17, Digest: digestOfByteV1('a'),
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID: "catalog-publication-23", Revision: 23, Digest: digestOfByteV1('b'),
		},
	}
	post := controlapicontract.PublishedBasisRefV1{
		TenantID:        plan.TenantID,
		PointerRevision: plan.ExpectedPointerRevision + 1,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID: candidates.ControlSnapshotID, Revision: 18, Digest: digestOfByteV1('c'),
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID: candidates.CatalogGenerationID, Revision: 24, Digest: digestOfByteV1('d'),
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
			ConfigRef:           digestOfByteV1('6'),
			AuthorityCeilingRef: digestOfByteV1('7'),
			StaticContextRefs:   []string{digestOfByteV1('e')},
			FailurePolicy:       moduleapi.FailureOptional,
		},
		CatalogChange: ModuleDisableCatalogRetainInstanceV1,
	}
}
