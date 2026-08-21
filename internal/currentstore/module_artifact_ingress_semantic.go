package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// VerifyModuleArtifactIngressSemanticClosureV1 rebuilds every immutable
// Artifact -> Manifest and Admission -> historical supply observation edge
// from one coherent SQL snapshot. It performs no filesystem I/O and grants no
// install, activation, binding, execution, authority, or effect.
func VerifyModuleArtifactIngressSemanticClosureV1(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
) error {
	if ctx == nil || queryer == nil {
		return fmt.Errorf("%w: nil semantic verifier input", ErrModuleArtifactIngressIntegrity)
	}
	var artifactCount, admissionCount int
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM module_artifacts`).Scan(&artifactCount); err != nil {
		return fmt.Errorf("%w: count Artifacts: %v", ErrModuleArtifactIngressIntegrity, err)
	}
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM module_artifact_admissions`).Scan(&admissionCount); err != nil {
		return fmt.Errorf("%w: count Admissions: %v", ErrModuleArtifactIngressIntegrity, err)
	}
	if artifactCount > 256 || admissionCount > 256 || artifactCount > admissionCount {
		return fmt.Errorf("%w: Artifact/Admission store quota or cardinality differs", ErrModuleArtifactIngressIntegrity)
	}
	var overQuotaSources int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT source_id FROM module_artifact_admissions
			GROUP BY source_id HAVING COUNT(*) > 256
		)
	`).Scan(&overQuotaSources); err != nil || overQuotaSources != 0 {
		return fmt.Errorf("%w: Admission source quota differs: count=%d err=%v", ErrModuleArtifactIngressIntegrity, overQuotaSources, err)
	}

	artifactIDs, err := readDiscoveryIdentityColumn(ctx, queryer, `
		SELECT artifact_digest FROM module_artifacts
		ORDER BY artifact_digest COLLATE BINARY
	`)
	if err != nil {
		return fmt.Errorf("%w: list Artifacts: %v", ErrModuleArtifactIngressIntegrity, err)
	}
	for _, artifactID := range artifactIDs {
		artifact, found, err := queryModuleArtifactV1(ctx, queryer, artifactID)
		if err != nil || !found {
			return fmt.Errorf("%w: read Artifact %s: %v", ErrModuleArtifactIngressIntegrity, artifactID, err)
		}
		if artifact.ArtifactDigest != artifactID || !moduleapi.ValidSHA256(artifactID) ||
			artifact.ArtifactSizeBytes == 0 || artifact.ArtifactSizeBytes > moduleapi.MaxModuleSourcePackageBytesV1 ||
			artifact.CoveredFileCount == 0 || artifact.CoveredFileCount > uint64(moduleapi.DefaultArtifactMaxFiles) {
			return fmt.Errorf("%w: Artifact %s projection is outside bounds", ErrModuleArtifactIngressIntegrity, artifactID)
		}
		var refCount int
		if err := queryer.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM module_discovery_module_refs
			WHERE module_id=? AND exact_version=? AND artifact_digest=?
		`, artifact.Module.ID, artifact.Module.Version, artifact.ArtifactDigest).Scan(&refCount); err != nil || refCount != 1 {
			return fmt.Errorf("%w: Artifact %s discovery ModuleRef closure differs", ErrModuleArtifactIngressIntegrity, artifactID)
		}
		var parents int
		if err := queryer.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM module_artifact_admissions WHERE artifact_digest=?
		`, artifactID).Scan(&parents); err != nil || parents == 0 {
			return fmt.Errorf("%w: Artifact %s has no Admission", ErrModuleArtifactIngressIntegrity, artifactID)
		}
	}

	admissionIDs, err := readDiscoveryIdentityColumn(ctx, queryer, `
		SELECT admission_id FROM module_artifact_admissions
		ORDER BY admission_id COLLATE BINARY
	`)
	if err != nil {
		return fmt.Errorf("%w: list Admissions: %v", ErrModuleArtifactIngressIntegrity, err)
	}
	for _, admissionID := range admissionIDs {
		admission, found, err := queryModuleArtifactAdmissionV1(ctx, queryer, admissionID)
		if err != nil || !found {
			return fmt.Errorf("%w: read Admission %s: %v", ErrModuleArtifactIngressIntegrity, admissionID, err)
		}
		record := admission.Record
		artifact := admission.Artifact
		if record.ArtifactDigest != artifact.ArtifactDigest || record.Module != artifact.Module ||
			record.ArtifactSizeBytes != artifact.ArtifactSizeBytes || record.ManifestRef != artifact.ManifestRef ||
			record.CoveredFileCount != artifact.CoveredFileCount || admission.AdmittedAt.Before(artifact.IngressedAt) {
			return fmt.Errorf("%w: Admission %s Artifact projection differs", ErrModuleArtifactIngressIntegrity, admissionID)
		}
		snapshot, found, err := queryModuleDiscoverySnapshot(ctx, queryer, record.SnapshotID)
		if err != nil || !found {
			return fmt.Errorf("%w: Admission %s Snapshot closure: %v", ErrModuleArtifactIngressIntegrity, admissionID, err)
		}
		if snapshot.SourceID != record.SourceID || snapshot.SourcePolicyID != record.SourcePolicyID ||
			snapshot.SourcePolicyRevision != record.SourcePolicyRevision ||
			snapshot.ObservationRevision != record.SnapshotObservationRevision ||
			snapshot.PublisherKeyID != "" || snapshot.PublisherKeyRevision != 0 ||
			admission.AdmittedAt.Before(snapshot.ObservedAt) {
			return fmt.Errorf("%w: Admission %s Snapshot projection differs", ErrModuleArtifactIngressIntegrity, admissionID)
		}
		policy, canonical, policyID, err := moduleapi.ParseModuleSourcePolicyV1(snapshot.SourcePolicyCanonical)
		if err != nil || policyID != record.SourcePolicyID || !bytes.Equal(canonical, snapshot.SourcePolicyCanonical) ||
			policy.SourceID != record.SourceID || policy.Kind != moduleapi.ModuleSourceKindLocalDirectoryV1 ||
			policy.Network != moduleapi.ModuleSourceNetworkDenyV1 || policy.SignatureRequired || policy.PublisherKeyID != "" {
			return fmt.Errorf("%w: Admission %s historical SourcePolicy is not unsigned LOCAL_DIRECTORY+DENY", ErrModuleArtifactIngressIntegrity, admissionID)
		}
		if uint64(record.EntryOrdinal) >= uint64(len(snapshot.Snapshot.Entries)) {
			return fmt.Errorf("%w: Admission %s entry ordinal is absent", ErrModuleArtifactIngressIntegrity, admissionID)
		}
		entry := snapshot.Snapshot.Entries[record.EntryOrdinal]
		if entry.Module != record.Module || entry.ArtifactDigest != record.ArtifactDigest ||
			entry.ArtifactSizeBytes != record.ArtifactSizeBytes || entry.PackagePath != record.PackagePath ||
			entry.SignatureID != "" {
			return fmt.Errorf("%w: Admission %s discovery entry projection differs", ErrModuleArtifactIngressIntegrity, admissionID)
		}
		if err := moduleapi.ValidateModuleSourceCandidateV1(policy, entry.Module, entry.ArtifactSizeBytes, false); err != nil {
			return fmt.Errorf("%w: Admission %s violates historical SourcePolicy: %v", ErrModuleArtifactIngressIntegrity, admissionID, err)
		}
	}
	if len(artifactIDs) != artifactCount || len(admissionIDs) != admissionCount {
		return fmt.Errorf("%w: Artifact/Admission enumeration count differs", ErrModuleArtifactIngressIntegrity)
	}
	return nil
}
