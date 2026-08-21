package learningcontract

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	// MaterializedVersionSchemaVersionV1 is the sole pure W4-L3 version wire.
	// It is an immutable, authority-free projection and is not an installation,
	// activation, binding, publication, or Catalog receipt.
	MaterializedVersionSchemaVersionV1 = "learning-materialized-version/v1"

	MaxMaterializedVersionWireBytesV1 = 16 << 10

	MaterializedKnowledgeEntrypointV1 = "content/source.json"
	MaterializedSkillEntrypointV1     = "content/context.json"

	materializedVersionDigestDomainV1 = "freeagent.learning-materialized-version/v1"
	maximumMaterializedVersionNodesV1 = 64
)

// MaterializedVersionV1 binds one approved Proposal and its exact review facts
// to one deterministic module artifact. The wire deliberately contains no
// Profile, Instance, Binding, Config, Authority, Trust, Secret, Catalog, or
// external-effect fields.
type MaterializedVersionV1 struct {
	SchemaVersion        string         `json:"schema_version"`
	ProposalID           string         `json:"proposal_id"`
	ProposalRevision     uint64         `json:"proposal_revision"`
	ReviewRunID          string         `json:"review_run_id"`
	ReviewerAttemptID    string         `json:"reviewer_attempt_id"`
	ApproveVerdictDigest string         `json:"approve_verdict_digest"`
	TenantID             string         `json:"tenant_id"`
	Kind                 ProposalKindV1 `json:"kind"`
	SourceFingerprint    string         `json:"source_fingerprint"`
	ContentFingerprint   string         `json:"content_fingerprint"`
	DraftDigest          string         `json:"draft_digest"`
	Target               moduleapi.Ref  `json:"target"`
	ArtifactDigest       string         `json:"artifact_digest"`
	ArtifactSizeBytes    uint64         `json:"artifact_size_bytes"`
}

// MaterializedArtifactV1 is the complete two-file package reconstructed from
// a MaterializedVersionV1 and its Proposal/Draft parents. All returned byte
// slices are detached from caller input and from one another.
type MaterializedArtifactV1 struct {
	ManifestCanonical []byte
	EntrypointPath    string
	PayloadCanonical  []byte
	ArtifactDigest    string
	ArtifactSizeBytes uint64
}

// NewMaterializedVersionV1 validates the exact Proposal, Draft, and canonical
// APPROVE verdict parents, derives the fixed module package, and freezes its
// authority-free version projection. Derived binding and artifact fields may
// be empty in input; any supplied value must equal the recomputation.
func NewMaterializedVersionV1(
	input MaterializedVersionV1,
	proposalCanonical []byte,
	draftCanonical []byte,
	approveVerdictCanonical []byte,
) (MaterializedVersionV1, []byte, string, error) {
	if input.SchemaVersion != MaterializedVersionSchemaVersionV1 {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version schema_version must be %q",
			MaterializedVersionSchemaVersionV1,
		)
	}
	if !moduleapi.ValidSHA256(input.ProposalID) {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version ProposalID must be SHA-256",
		)
	}
	if input.ProposalRevision != 2 && input.ProposalRevision != 3 {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version proposal_revision must be 2 or 3",
		)
	}
	if !validOpaqueV1(input.ReviewRunID) ||
		!validOpaqueV1(input.ReviewerAttemptID) {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version review identities are invalid",
		)
	}

	proposalBytes := bytes.Clone(proposalCanonical)
	draftBytes := bytes.Clone(draftCanonical)
	verdictBytes := bytes.Clone(approveVerdictCanonical)
	proposal, err := RestoreProposalV1(
		proposalBytes,
		draftBytes,
		input.ProposalID,
	)
	if err != nil {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version Proposal/Draft: %w",
			err,
		)
	}

	verdict, rebuiltVerdict, verdictDigest, err := ParseReviewVerdictV1(verdictBytes)
	if err != nil {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version APPROVE verdict: %w",
			err,
		)
	}
	if !bytes.Equal(rebuiltVerdict, verdictBytes) {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version verdict is not exact canonical form",
		)
	}
	if verdict.Decision != ReviewDecisionApproveV1 {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version requires an APPROVE verdict",
		)
	}
	if err := verdict.ValidateForProposalV1(proposal, input.ProposalID); err != nil {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version verdict binding: %w",
			err,
		)
	}
	if input.ApproveVerdictDigest != "" &&
		input.ApproveVerdictDigest != verdictDigest {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version APPROVE verdict digest differs from its verdict",
		)
	}

	artifact, err := buildMaterializedArtifactV1(proposal, draftBytes)
	if err != nil {
		return MaterializedVersionV1{}, nil, "", err
	}
	if err := validateSuppliedMaterializedBindingsV1(
		input,
		proposal,
		artifact,
	); err != nil {
		return MaterializedVersionV1{}, nil, "", err
	}

	frozen := input
	frozen.ApproveVerdictDigest = verdictDigest
	frozen.TenantID = proposal.TenantID
	frozen.Kind = proposal.Kind
	frozen.SourceFingerprint = proposal.SourceFingerprint
	frozen.ContentFingerprint = proposal.ContentFingerprint
	frozen.DraftDigest = proposal.DraftDigest
	frozen.Target = proposal.Target
	frozen.ArtifactDigest = artifact.ArtifactDigest
	frozen.ArtifactSizeBytes = artifact.ArtifactSizeBytes

	canonical, err := canonicalJSONV1(frozen)
	if err != nil {
		return MaterializedVersionV1{}, nil, "", err
	}
	if len(canonical) > MaxMaterializedVersionWireBytesV1 {
		return MaterializedVersionV1{}, nil, "", fmt.Errorf(
			"learningcontract: materialized version exceeds %d bytes",
			MaxMaterializedVersionWireBytesV1,
		)
	}
	versionID := moduleapi.Digest(
		materializedVersionDigestDomainV1,
		canonical,
	)
	return frozen, bytes.Clone(canonical), versionID, nil
}

// RestoreMaterializedVersionV1 restores only the exact canonical version and
// exact Proposal/Draft/APPROVE parents used by NewMaterializedVersionV1.
func RestoreMaterializedVersionV1(
	canonical []byte,
	expectedVersionID string,
	proposalCanonical []byte,
	draftCanonical []byte,
	approveVerdictCanonical []byte,
) (MaterializedVersionV1, error) {
	if !moduleapi.ValidSHA256(expectedVersionID) {
		return MaterializedVersionV1{}, fmt.Errorf(
			"learningcontract: expected materialized VersionID must be SHA-256",
		)
	}
	versionBytes := bytes.Clone(canonical)
	if err := requireExactCanonicalV1(
		versionBytes,
		MaxMaterializedVersionWireBytesV1,
		maximumMaterializedVersionNodesV1,
	); err != nil {
		return MaterializedVersionV1{}, err
	}
	var decoded MaterializedVersionV1
	if err := decodeStrictV1(
		versionBytes,
		MaxMaterializedVersionWireBytesV1,
		&decoded,
	); err != nil {
		return MaterializedVersionV1{}, err
	}
	restored, rebuilt, versionID, err := NewMaterializedVersionV1(
		decoded,
		proposalCanonical,
		draftCanonical,
		approveVerdictCanonical,
	)
	if err != nil {
		return MaterializedVersionV1{}, err
	}
	if versionID != expectedVersionID || !bytes.Equal(rebuilt, versionBytes) {
		return MaterializedVersionV1{}, fmt.Errorf(
			"learningcontract: materialized version is not frozen canonically",
		)
	}
	return restored, nil
}

// MaterializedVersionArtifactV1 reconstructs the exact fixed two-file module
// package from an already-authoritative version canonical/ID and its exact
// Proposal/Draft parents. It grants no permission and does not replace the
// review-closure check performed by RestoreMaterializedVersionV1 or the Store.
func MaterializedVersionArtifactV1(
	versionCanonical []byte,
	expectedVersionID string,
	proposalCanonical []byte,
	draftCanonical []byte,
) (MaterializedArtifactV1, error) {
	version, proposal, err := restoreMaterializedVersionArtifactParentsV1(
		versionCanonical,
		expectedVersionID,
		proposalCanonical,
		draftCanonical,
	)
	if err != nil {
		return MaterializedArtifactV1{}, err
	}
	artifact, err := buildMaterializedArtifactV1(
		proposal,
		bytes.Clone(draftCanonical),
	)
	if err != nil {
		return MaterializedArtifactV1{}, err
	}
	if artifact.ArtifactDigest != version.ArtifactDigest ||
		artifact.ArtifactSizeBytes != version.ArtifactSizeBytes {
		return MaterializedArtifactV1{}, fmt.Errorf(
			"learningcontract: materialized artifact differs from its version",
		)
	}
	return detachMaterializedArtifactV1(artifact), nil
}

func restoreMaterializedVersionArtifactParentsV1(
	versionCanonical []byte,
	expectedVersionID string,
	proposalCanonical []byte,
	draftCanonical []byte,
) (MaterializedVersionV1, ProposalV1, error) {
	if !moduleapi.ValidSHA256(expectedVersionID) {
		return MaterializedVersionV1{}, ProposalV1{}, fmt.Errorf(
			"learningcontract: expected materialized VersionID must be SHA-256",
		)
	}
	versionBytes := bytes.Clone(versionCanonical)
	if err := requireExactCanonicalV1(
		versionBytes,
		MaxMaterializedVersionWireBytesV1,
		maximumMaterializedVersionNodesV1,
	); err != nil {
		return MaterializedVersionV1{}, ProposalV1{}, err
	}
	var version MaterializedVersionV1
	if err := decodeStrictV1(
		versionBytes,
		MaxMaterializedVersionWireBytesV1,
		&version,
	); err != nil {
		return MaterializedVersionV1{}, ProposalV1{}, err
	}
	if moduleapi.Digest(materializedVersionDigestDomainV1, versionBytes) !=
		expectedVersionID {
		return MaterializedVersionV1{}, ProposalV1{}, fmt.Errorf(
			"learningcontract: materialized version does not match its VersionID",
		)
	}
	if err := validateMaterializedVersionShapeV1(version); err != nil {
		return MaterializedVersionV1{}, ProposalV1{}, err
	}
	proposal, err := RestoreProposalV1(
		bytes.Clone(proposalCanonical),
		bytes.Clone(draftCanonical),
		version.ProposalID,
	)
	if err != nil {
		return MaterializedVersionV1{}, ProposalV1{}, fmt.Errorf(
			"learningcontract: materialized artifact Proposal/Draft: %w",
			err,
		)
	}
	artifact, err := buildMaterializedArtifactV1(
		proposal,
		bytes.Clone(draftCanonical),
	)
	if err != nil {
		return MaterializedVersionV1{}, ProposalV1{}, err
	}
	if err := validateExactMaterializedBindingsV1(
		version,
		proposal,
		artifact,
	); err != nil {
		return MaterializedVersionV1{}, ProposalV1{}, err
	}
	return version, proposal, nil
}

func validateMaterializedVersionShapeV1(input MaterializedVersionV1) error {
	if input.SchemaVersion != MaterializedVersionSchemaVersionV1 ||
		!moduleapi.ValidSHA256(input.ProposalID) ||
		(input.ProposalRevision != 2 && input.ProposalRevision != 3) ||
		!validOpaqueV1(input.ReviewRunID) ||
		!validOpaqueV1(input.ReviewerAttemptID) ||
		!moduleapi.ValidSHA256(input.ApproveVerdictDigest) ||
		!validOpaqueV1(input.TenantID) ||
		!validProposalKindV1(input.Kind) ||
		!moduleapi.ValidSHA256(input.SourceFingerprint) ||
		!moduleapi.ValidSHA256(input.ContentFingerprint) ||
		!moduleapi.ValidSHA256(input.DraftDigest) ||
		input.Target.Validate() != nil ||
		!moduleapi.ValidSHA256(input.ArtifactDigest) ||
		input.ArtifactSizeBytes == 0 {
		return fmt.Errorf(
			"learningcontract: invalid materialized version identity or projection",
		)
	}
	return nil
}

func validateSuppliedMaterializedBindingsV1(
	input MaterializedVersionV1,
	proposal ProposalV1,
	artifact MaterializedArtifactV1,
) error {
	for _, binding := range []struct {
		name     string
		supplied string
		exact    string
	}{
		{name: "tenant_id", supplied: input.TenantID, exact: proposal.TenantID},
		{name: "source_fingerprint", supplied: input.SourceFingerprint, exact: proposal.SourceFingerprint},
		{name: "content_fingerprint", supplied: input.ContentFingerprint, exact: proposal.ContentFingerprint},
		{name: "draft_digest", supplied: input.DraftDigest, exact: proposal.DraftDigest},
		{name: "artifact_digest", supplied: input.ArtifactDigest, exact: artifact.ArtifactDigest},
	} {
		if binding.supplied != "" && binding.supplied != binding.exact {
			return fmt.Errorf(
				"learningcontract: materialized version %s differs from its parent",
				binding.name,
			)
		}
	}
	if input.Kind != "" && input.Kind != proposal.Kind {
		return fmt.Errorf(
			"learningcontract: materialized version kind differs from its Proposal",
		)
	}
	if !input.Target.IsZero() && input.Target != proposal.Target {
		return fmt.Errorf(
			"learningcontract: materialized version target differs from its Proposal",
		)
	}
	if input.ArtifactSizeBytes != 0 &&
		input.ArtifactSizeBytes != artifact.ArtifactSizeBytes {
		return fmt.Errorf(
			"learningcontract: materialized version artifact size differs from its artifact",
		)
	}
	return nil
}

func validateExactMaterializedBindingsV1(
	version MaterializedVersionV1,
	proposal ProposalV1,
	artifact MaterializedArtifactV1,
) error {
	if version.TenantID != proposal.TenantID ||
		version.Kind != proposal.Kind ||
		version.SourceFingerprint != proposal.SourceFingerprint ||
		version.ContentFingerprint != proposal.ContentFingerprint ||
		version.DraftDigest != proposal.DraftDigest ||
		version.Target != proposal.Target ||
		version.ArtifactDigest != artifact.ArtifactDigest ||
		version.ArtifactSizeBytes != artifact.ArtifactSizeBytes {
		return fmt.Errorf(
			"learningcontract: materialized version does not bind its exact Proposal/Draft artifact",
		)
	}
	return nil
}

func buildMaterializedArtifactV1(
	proposal ProposalV1,
	draftCanonical []byte,
) (MaterializedArtifactV1, error) {
	var runtimeRequest moduleapi.RuntimeRequestV1
	switch proposal.Kind {
	case ProposalKindKnowledgeV1:
		runtimeRequest = moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol:   moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint: MaterializedKnowledgeEntrypointV1,
		}
	case ProposalKindSkillV1:
		runtimeRequest = moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestDeclarative,
			Protocol:   moduleapi.RuntimeProtocolStaticV1,
			Entrypoint: MaterializedSkillEntrypointV1,
		}
	default:
		return MaterializedArtifactV1{}, fmt.Errorf(
			"learningcontract: unsupported materialized Proposal kind %q",
			proposal.Kind,
		)
	}

	manifest := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         proposal.Target.ID,
		Version:    proposal.Target.Version,
		Runtime:    runtimeRequest,
		Provides: []moduleapi.PortRef{{
			Name:         moduleapi.PortNameContextProvide,
			ExactVersion: moduleapi.PortVersionV1,
		}},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return MaterializedArtifactV1{}, fmt.Errorf(
			"learningcontract: encode materialized module manifest: %w",
			err,
		)
	}
	manifestCanonical, err := moduleapi.CanonicalJSON(manifestJSON)
	if err != nil {
		return MaterializedArtifactV1{}, fmt.Errorf(
			"learningcontract: canonicalize materialized module manifest: %w",
			err,
		)
	}
	parsedManifest, parsedCanonical, err := moduleapi.ParseModuleManifestV1(
		manifestCanonical,
	)
	if err != nil || parsedManifest.ID != proposal.Target.ID ||
		parsedManifest.Version != proposal.Target.Version ||
		!bytes.Equal(parsedCanonical, manifestCanonical) {
		return MaterializedArtifactV1{}, fmt.Errorf(
			"learningcontract: verify materialized module manifest: %w",
			err,
		)
	}

	payload := bytes.Clone(draftCanonical)
	digest, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		[]moduleapi.ArtifactFile{{
			Path:    runtimeRequest.Entrypoint,
			Content: payload,
		}},
	)
	if err != nil {
		return MaterializedArtifactV1{}, fmt.Errorf(
			"learningcontract: compute materialized artifact digest: %w",
			err,
		)
	}
	return MaterializedArtifactV1{
		ManifestCanonical: bytes.Clone(manifestCanonical),
		EntrypointPath:    runtimeRequest.Entrypoint,
		PayloadCanonical:  payload,
		ArtifactDigest:    digest,
		ArtifactSizeBytes: uint64(len(manifestCanonical)) + uint64(len(payload)),
	}, nil
}

func detachMaterializedArtifactV1(
	input MaterializedArtifactV1,
) MaterializedArtifactV1 {
	input.ManifestCanonical = bytes.Clone(input.ManifestCanonical)
	input.PayloadCanonical = bytes.Clone(input.PayloadCanonical)
	return input
}
