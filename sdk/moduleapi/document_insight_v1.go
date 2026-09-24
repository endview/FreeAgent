package moduleapi

import (
	"fmt"
	"slices"
)

const (
	documentInsightEntrypointV1 = "content/source.json"

	// DocumentInsightModuleIDV1, DocumentInsightVersionV2,
	// DocumentInsightArtifactDigestV2, and DocumentInsightAdapterIdentityV1
	// identify the only reserved dual-Port product accepted by the E5-B
	// runtime policy. A package cannot obtain this identity by declaring the
	// same Ports or permissions.
	DocumentInsightModuleIDV1        = "freeagent.builtin.document-insight"
	DocumentInsightVersionV2         = "2.0.0"
	DocumentInsightArtifactDigestV2  = "9cf2e60f4b6d30cfd93ea93245f4a6eadbd4f365b93f06b7decb389c4f4d4bfa"
	DocumentInsightAdapterIdentityV1 = "freeagent.adapter.document-insight/v1"
)

// ExactDocumentInsightProvidesV1 returns the complete, ordered Port set for
// the first document-insight module. Ordering is part of the exact manifest
// contract rather than a discovery or preference hint.
func ExactDocumentInsightProvidesV1() []PortRef {
	return []PortRef{
		{Name: PortNameActionProvider, ExactVersion: PortVersionV1},
		ExactContextProvidePortV1(),
	}
}

// ClassifyExactDocumentInsightManifestV1 accepts only the governed dual-Port
// document-insight declaration. The declaration requests knowledge access;
// it does not grant permission, trust, activation, or a Binding.
func ClassifyExactDocumentInsightManifestV1(
	manifest ModuleManifestV1,
) error {
	return ClassifyExactDocumentInsightManifestDeclarationV1(
		KnowledgeManifestDeclarationV1{
			RuntimeMode:          manifest.Runtime.Mode,
			RuntimeProtocol:      manifest.Runtime.Protocol,
			Entrypoint:           manifest.Runtime.Entrypoint,
			Provides:             manifest.Provides,
			Requires:             manifest.Requires,
			RequestedPermissions: manifest.RequestedPermissions,
		},
	)
}

// ClassifyExactDocumentInsightProviderV1 accepts only the Core-assigned
// activation identity for the reserved E5-B product. Callers that encounter
// the reserved Module ID must use this classifier before considering generic
// protocol handlers, so a drifted version, artifact, Adapter, or execution
// class cannot fall back to an ordinary Knowledge or Action implementation.
func ClassifyExactDocumentInsightProviderV1(
	provider ActivatedModuleRef,
) error {
	if err := provider.Validate(); err != nil {
		return fmt.Errorf("Document Insight provider identity is invalid: %w", err)
	}
	if provider.ModuleID != DocumentInsightModuleIDV1 ||
		provider.Version != DocumentInsightVersionV2 ||
		provider.ArtifactDigest != DocumentInsightArtifactDigestV2 ||
		provider.ExecutionClass != ExecutionTrustedInProcess ||
		provider.AdapterIdentity != DocumentInsightAdapterIdentityV1 {
		return fmt.Errorf(
			"Document Insight provider must use the exact reserved module, version, artifact, execution class, and adapter identity",
		)
	}
	return nil
}

// ClassifyExactDocumentInsightManifestDeclarationV1 is the format-neutral
// classifier used by package, Store, Backup, and runtime boundaries. Partial
// declarations fail closed so the Action and Context capabilities cannot be
// authorized independently from the module's model dependency and permission
// request.
func ClassifyExactDocumentInsightManifestDeclarationV1(
	declaration KnowledgeManifestDeclarationV1,
) error {
	if declaration.RuntimeMode != RuntimeModeRequestTrustedInProcess ||
		declaration.RuntimeProtocol != RuntimeProtocolGoInProcessV1 {
		return fmt.Errorf(
			"Document Insight manifest must request exact TRUSTED_IN_PROCESS/go-in-process/v1 runtime",
		)
	}
	entrypoint, err := NormalizeArtifactPath(declaration.Entrypoint)
	if err != nil || entrypoint != documentInsightEntrypointV1 ||
		entrypoint != declaration.Entrypoint {
		return fmt.Errorf(
			"Document Insight manifest entrypoint must be exact %s",
			documentInsightEntrypointV1,
		)
	}
	if !slices.Equal(
		declaration.Provides,
		ExactDocumentInsightProvidesV1(),
	) {
		return fmt.Errorf(
			"Document Insight manifest must provide exact ordered action.provider/v1, context.provide/v1",
		)
	}
	if !slices.Equal(
		declaration.Requires,
		[]PortRef{ExactModelGeneratePortV1()},
	) {
		return fmt.Errorf(
			"Document Insight manifest must require only exact model.generate/v2",
		)
	}
	if !slices.Equal(
		declaration.RequestedPermissions,
		[]Permission{PermissionKnowledgeReadV1},
	) {
		return fmt.Errorf(
			"Document Insight manifest must request only exact knowledge.read permission",
		)
	}
	return nil
}
