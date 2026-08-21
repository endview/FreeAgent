package moduleapi

import (
	"strings"
	"testing"
)

func TestClassifyExactDocumentInsightManifestV1(t *testing.T) {
	exact := ModuleManifestV1{
		Runtime: RuntimeRequestV1{
			Mode:       RuntimeModeRequestTrustedInProcess,
			Protocol:   RuntimeProtocolGoInProcessV1,
			Entrypoint: "content/source.json",
		},
		Provides:             ExactDocumentInsightProvidesV1(),
		Requires:             []PortRef{ExactModelGeneratePortV1()},
		RequestedPermissions: []Permission{PermissionKnowledgeReadV1},
	}
	if err := ClassifyExactDocumentInsightManifestV1(exact); err != nil {
		t.Fatalf("exact manifest error=%v", err)
	}
	declaration := KnowledgeManifestDeclarationV1{
		RuntimeMode:          exact.Runtime.Mode,
		RuntimeProtocol:      exact.Runtime.Protocol,
		Entrypoint:           exact.Runtime.Entrypoint,
		Provides:             exact.Provides,
		Requires:             exact.Requires,
		RequestedPermissions: exact.RequestedPermissions,
	}
	if err := ClassifyExactDocumentInsightManifestDeclarationV1(
		declaration,
	); err != nil {
		t.Fatalf("exact declaration error=%v", err)
	}

	tests := []struct {
		name       string
		mutate     func(*ModuleManifestV1)
		wantDetail string
	}{
		{
			name: "wrong runtime mode",
			mutate: func(value *ModuleManifestV1) {
				value.Runtime.Mode = RuntimeModeRequestDeclarative
			},
			wantDetail: "exact TRUSTED_IN_PROCESS/go-in-process/v1",
		},
		{
			name: "wrong runtime protocol",
			mutate: func(value *ModuleManifestV1) {
				value.Runtime.Protocol = RuntimeProtocolStaticV1
			},
			wantDetail: "exact TRUSTED_IN_PROCESS/go-in-process/v1",
		},
		{
			name: "different content entrypoint",
			mutate: func(value *ModuleManifestV1) {
				value.Runtime.Entrypoint = "content/other.json"
			},
			wantDetail: "exact content/source.json",
		},
		{
			name: "reversed provides",
			mutate: func(value *ModuleManifestV1) {
				value.Provides[0], value.Provides[1] =
					value.Provides[1], value.Provides[0]
			},
			wantDetail: "exact ordered action.provider/v1, context.provide/v1",
		},
		{
			name: "context only",
			mutate: func(value *ModuleManifestV1) {
				value.Provides = []PortRef{ExactContextProvidePortV1()}
			},
			wantDetail: "exact ordered action.provider/v1, context.provide/v1",
		},
		{
			name: "missing model dependency",
			mutate: func(value *ModuleManifestV1) {
				value.Requires = nil
			},
			wantDetail: "require only exact model.generate/v1",
		},
		{
			name: "extra model dependency",
			mutate: func(value *ModuleManifestV1) {
				value.Requires = append(value.Requires, PortRef{
					Name: PortNameChannelTransport, ExactVersion: PortVersionV1,
				})
			},
			wantDetail: "require only exact model.generate/v1",
		},
		{
			name: "missing knowledge permission",
			mutate: func(value *ModuleManifestV1) {
				value.RequestedPermissions = nil
			},
			wantDetail: "only exact knowledge.read",
		},
		{
			name: "extra permission",
			mutate: func(value *ModuleManifestV1) {
				value.RequestedPermissions = append(
					value.RequestedPermissions,
					Permission("network.read"),
				)
			},
			wantDetail: "only exact knowledge.read",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneDocumentInsightManifestTestV1(exact)
			test.mutate(&candidate)
			err := ClassifyExactDocumentInsightManifestV1(candidate)
			if err == nil || !strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf("error=%v, want detail %q", err, test.wantDetail)
			}
		})
	}
}

func TestDocumentInsightContractDoesNotWidenKnowledgeClassifier(t *testing.T) {
	manifest := ModuleManifestV1{
		Runtime: RuntimeRequestV1{
			Mode:       RuntimeModeRequestTrustedInProcess,
			Protocol:   RuntimeProtocolGoInProcessV1,
			Entrypoint: "content/source.json",
		},
		Provides:             ExactDocumentInsightProvidesV1(),
		Requires:             []PortRef{ExactModelGeneratePortV1()},
		RequestedPermissions: []Permission{PermissionKnowledgeReadV1},
	}
	if _, err := ClassifyExactKnowledgeManifestV1(manifest); err == nil {
		t.Fatal("legacy Knowledge classifier accepted dual-Port Document Insight")
	}
	if err := ClassifyExactDocumentInsightManifestV1(manifest); err != nil {
		t.Fatalf("Document Insight classifier error=%v", err)
	}
}

func TestExactDocumentInsightProvidesV1ReturnsDefensiveOrder(t *testing.T) {
	first := ExactDocumentInsightProvidesV1()
	first[0] = ExactModelGeneratePortV1()
	second := ExactDocumentInsightProvidesV1()
	if len(second) != 2 ||
		second[0] != (PortRef{Name: PortNameActionProvider, ExactVersion: PortVersionV1}) ||
		second[1] != ExactContextProvidePortV1() {
		t.Fatalf("provides=%+v", second)
	}
}

func TestClassifyExactDocumentInsightProviderV1(t *testing.T) {
	exact := ActivatedModuleRef{
		ModuleID:           DocumentInsightModuleIDV1,
		Version:            DocumentInsightVersionV1,
		ArtifactDigest:     DocumentInsightArtifactDigestV1,
		InstanceID:         "document-insight-main",
		ExecutionClass:     ExecutionTrustedInProcess,
		AdapterIdentity:    DocumentInsightAdapterIdentityV1,
		ActivationRevision: 1,
	}
	if err := ClassifyExactDocumentInsightProviderV1(exact); err != nil {
		t.Fatalf("exact provider error=%v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ActivatedModuleRef)
	}{
		{
			name: "different module ID",
			mutate: func(value *ActivatedModuleRef) {
				value.ModuleID = "freeagent.builtin.other"
			},
		},
		{
			name: "different version",
			mutate: func(value *ActivatedModuleRef) {
				value.Version = "1.0.1"
			},
		},
		{
			name: "different artifact",
			mutate: func(value *ActivatedModuleRef) {
				value.ArtifactDigest = strings.Repeat("a", SHA256HexLength)
			},
		},
		{
			name: "different execution class",
			mutate: func(value *ActivatedModuleRef) {
				value.ExecutionClass = ExecutionLocalProcess
			},
		},
		{
			name: "different adapter",
			mutate: func(value *ActivatedModuleRef) {
				value.AdapterIdentity = "freeagent.adapter.other/v1"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := exact
			test.mutate(&candidate)
			if err := ClassifyExactDocumentInsightProviderV1(candidate); err == nil {
				t.Fatal("drifted provider identity was accepted")
			}
		})
	}
}

func cloneDocumentInsightManifestTestV1(
	manifest ModuleManifestV1,
) ModuleManifestV1 {
	manifest.Provides = append([]PortRef(nil), manifest.Provides...)
	manifest.Requires = append([]PortRef(nil), manifest.Requires...)
	manifest.RequestedPermissions = append(
		[]Permission(nil),
		manifest.RequestedPermissions...,
	)
	return manifest
}
