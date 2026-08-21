package main

import (
	"slices"

	"github.com/endview/freeagent/sdk/moduleapi"
)

type knowledgeManifestShapeV1 = moduleapi.KnowledgeManifestShapeV1

const (
	knowledgeManifestLegacyPermissionlessV1 = moduleapi.KnowledgeManifestLegacyPermissionlessV1
	knowledgeManifestGovernedV1             = moduleapi.KnowledgeManifestGovernedV1
)

// knowledgeManifestDeclarationV1 is the common projection used by both a
// parsed manifest and the format-only conformance report. Keeping the exact
// shape decision here prevents Apply, Dry-run and production loading from
// accepting different Requires or permission declarations.
type knowledgeManifestDeclarationV1 = moduleapi.KnowledgeManifestDeclarationV1

func classifyExactKnowledgeManifestV1(
	manifest moduleapi.ModuleManifestV1,
) (knowledgeManifestShapeV1, error) {
	return moduleapi.ClassifyExactKnowledgeManifestV1(manifest)
}

func classifyExactKnowledgeManifestDeclarationV1(
	declaration knowledgeManifestDeclarationV1,
) (knowledgeManifestShapeV1, error) {
	return moduleapi.ClassifyExactKnowledgeManifestDeclarationV1(declaration)
}

func exactKnowledgeManifestProvidesV1() []moduleapi.PortRef {
	return moduleapi.ExactKnowledgeManifestProvidesV1()
}

func classifyExactDocumentInsightManifestV1(
	manifest moduleapi.ModuleManifestV1,
) error {
	return moduleapi.ClassifyExactDocumentInsightManifestV1(manifest)
}

func classifyExactDocumentInsightManifestDeclarationV1(
	declaration knowledgeManifestDeclarationV1,
) error {
	return moduleapi.ClassifyExactDocumentInsightManifestDeclarationV1(
		declaration,
	)
}

func exactDocumentInsightManifestProvidesV1() []moduleapi.PortRef {
	return moduleapi.ExactDocumentInsightProvidesV1()
}

// moduleApplyCatalogProvidesForPolicyV1 keeps Catalog's deliberately narrow
// Provides projection tied to the same exact Knowledge shape. Requires and
// permissions remain in the immutable Manifest and are not duplicated in the
// Catalog.
func moduleApplyCatalogProvidesForPolicyV1(
	policy moduleApplyLocalPolicyV1,
) []moduleapi.PortRef {
	if policy.HandlerKind == moduleApplyHandlerKnowledgeContextV1 {
		return exactKnowledgeManifestProvidesV1()
	}
	if policy.HandlerKind == moduleApplyHandlerDocumentInsightV1 {
		return exactDocumentInsightManifestProvidesV1()
	}
	return []moduleapi.PortRef{policy.Port}
}

func moduleApplyCatalogProvidesMatchPolicyV1(
	provides []moduleapi.PortRef,
	policy moduleApplyLocalPolicyV1,
) bool {
	return slices.Equal(provides, moduleApplyCatalogProvidesForPolicyV1(policy))
}
