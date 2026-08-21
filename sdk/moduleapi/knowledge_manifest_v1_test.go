package moduleapi

import (
	"strings"
	"testing"
)

func TestClassifyExactKnowledgeManifestV1(t *testing.T) {
	legacy := ModuleManifestV1{
		Runtime: RuntimeRequestV1{
			Mode:       RuntimeModeRequestTrustedInProcess,
			Protocol:   RuntimeProtocolGoInProcessV1,
			Entrypoint: "content/source.json",
		},
		Provides: ExactKnowledgeManifestProvidesV1(),
	}
	governed := cloneKnowledgeManifestClassifierTestV1(legacy)
	governed.Requires = []PortRef{ExactModelGeneratePortV1()}
	governed.RequestedPermissions = []Permission{PermissionKnowledgeReadV1}

	tests := []struct {
		name       string
		manifest   ModuleManifestV1
		wantShape  KnowledgeManifestShapeV1
		wantDetail string
	}{
		{
			name:      "legacy permissionless",
			manifest:  legacy,
			wantShape: KnowledgeManifestLegacyPermissionlessV1,
		},
		{
			name:      "governed exact pair",
			manifest:  governed,
			wantShape: KnowledgeManifestGovernedV1,
		},
		{
			name: "permission only",
			manifest: func() ModuleManifestV1 {
				value := cloneKnowledgeManifestClassifierTestV1(governed)
				value.Requires = nil
				return value
			}(),
			wantDetail: "exactly legacy permissionless or governed",
		},
		{
			name: "require only",
			manifest: func() ModuleManifestV1 {
				value := cloneKnowledgeManifestClassifierTestV1(governed)
				value.RequestedPermissions = nil
				return value
			}(),
			wantDetail: "exactly legacy permissionless or governed",
		},
		{
			name: "extra require",
			manifest: func() ModuleManifestV1 {
				value := cloneKnowledgeManifestClassifierTestV1(governed)
				value.Requires = append(value.Requires, PortRef{
					Name:         PortNameActionProvider,
					ExactVersion: PortVersionV1,
				})
				return value
			}(),
			wantDetail: "exactly legacy permissionless or governed",
		},
		{
			name: "wrong runtime mode",
			manifest: func() ModuleManifestV1 {
				value := cloneKnowledgeManifestClassifierTestV1(governed)
				value.Runtime.Mode = RuntimeModeRequestDeclarative
				return value
			}(),
			wantDetail: "exact TRUSTED_IN_PROCESS/go-in-process/v1",
		},
		{
			name: "wrong runtime protocol",
			manifest: func() ModuleManifestV1 {
				value := cloneKnowledgeManifestClassifierTestV1(governed)
				value.Runtime.Protocol = RuntimeProtocolStaticV1
				return value
			}(),
			wantDetail: "exact TRUSTED_IN_PROCESS/go-in-process/v1",
		},
		{
			name: "non content entrypoint",
			manifest: func() ModuleManifestV1 {
				value := cloneKnowledgeManifestClassifierTestV1(governed)
				value.Runtime.Entrypoint = "builtin.demo"
				return value
			}(),
			wantDetail: "canonical content/ path",
		},
		{
			name: "extra provide",
			manifest: func() ModuleManifestV1 {
				value := cloneKnowledgeManifestClassifierTestV1(governed)
				value.Provides = append(value.Provides, PortRef{
					Name:         PortNameActionProvider,
					ExactVersion: PortVersionV1,
				})
				return value
			}(),
			wantDetail: "provide only exact context.provide/v1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shape, err := ClassifyExactKnowledgeManifestV1(test.manifest)
			if test.wantDetail == "" {
				if err != nil || shape != test.wantShape {
					t.Fatalf("classification=(%q,%v), want (%q,nil)", shape, err, test.wantShape)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf("classification=(%q,%v), want error containing %q", shape, err, test.wantDetail)
			}
		})
	}
}

func cloneKnowledgeManifestClassifierTestV1(
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
