package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/moduleartifactingress"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidModuleArtifactIngress    = errors.New("currentstore: invalid module artifact ingress")
	ErrModuleArtifactAdmissionNotFound = errors.New("currentstore: module artifact admission not found")
	ErrModuleArtifactIngressConflict   = errors.New("currentstore: module artifact ingress conflict")
	ErrModuleArtifactIngressStale      = errors.New("currentstore: module artifact ingress basis is stale")
	ErrModuleArtifactIngressIntegrity  = errors.New("currentstore: module artifact ingress integrity violation")
	ErrModuleArtifactIngressQuota      = errors.New("currentstore: module artifact ingress quota exceeded")
)

// ModuleArtifactIngressSelectionV1 identifies one exact entry in the current
// observation head. PackagePath remains Store-owned evidence and is never
// accepted from the caller as a host path.
type ModuleArtifactIngressSelectionV1 struct {
	SourceID       string
	SnapshotID     string
	Module         moduleapi.Ref
	ArtifactDigest string
}

// ModuleArtifactIngressBasisV1 is detached pre-I/O evidence. Commit rechecks
// the complete basis in BEGIN IMMEDIATE after the package has been verified
// and published to the server-owned artifact store.
type ModuleArtifactIngressBasisV1 struct {
	Selection    ModuleArtifactIngressSelectionV1
	Source       ModuleSource
	Snapshot     ModuleDiscoverySnapshot
	EntryOrdinal uint32
	Entry        moduleapi.ModuleDiscoveryEntryV1
}

// ModuleArtifactV1 is the immutable Current Store projection of one inert,
// content-addressed server-owned artifact.
type ModuleArtifactV1 struct {
	ArtifactDigest    string
	Module            moduleapi.Ref
	ManifestRef       string
	ManifestCanonical []byte
	ArtifactSizeBytes uint64
	CoveredFileCount  uint64
	IngressedAt       time.Time
}

// ModuleArtifactAdmissionV1 binds an Artifact to the exact immutable supply
// observation that authorized ingress. It grants no installation, activation,
// binding, execution, authority, secret, or effect.
type ModuleArtifactAdmissionV1 struct {
	AdmissionID string
	Record      moduleartifactingress.RecordV1
	Canonical   []byte
	Artifact    ModuleArtifactV1
	AdmittedAt  time.Time
}

// ReadModuleArtifactIngressBasisV1 captures one coherent current unsigned
// LOCAL_DIRECTORY+DENY observation. It performs no package or filesystem I/O.
func (store *Store) ReadModuleArtifactIngressBasisV1(
	ctx context.Context,
	selection ModuleArtifactIngressSelectionV1,
) (ModuleArtifactIngressBasisV1, error) {
	if ctx == nil {
		return ModuleArtifactIngressBasisV1{}, fmt.Errorf("%w: context is nil", ErrInvalidModuleArtifactIngress)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleArtifactIngressBasisV1{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ModuleArtifactIngressBasisV1{}, fmt.Errorf("currentstore: acquire artifact ingress basis connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return ModuleArtifactIngressBasisV1{}, fmt.Errorf("currentstore: begin artifact ingress basis read: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	basis, err := rebuildModuleArtifactIngressBasisV1(ctx, connection, selection)
	if err != nil {
		return ModuleArtifactIngressBasisV1{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModuleArtifactIngressBasisV1{}, fmt.Errorf("currentstore: commit artifact ingress basis read: %w", err)
	}
	committed = true
	return detachModuleArtifactIngressBasisV1(basis), nil
}

// CommitModuleArtifactIngressV1 is the post-I/O linearization point. Exact
// retry is resolved before current-basis validation, so a lost response stays
// retrievable even after the source head advances.
func (store *Store) CommitModuleArtifactIngressV1(
	ctx context.Context,
	basis ModuleArtifactIngressBasisV1,
	manifestCanonical []byte,
	coveredFileCount uint64,
) (artifactResult ModuleArtifactV1, admissionResult ModuleArtifactAdmissionV1, returnErr error) {
	if ctx == nil {
		return artifactResult, admissionResult, fmt.Errorf("%w: context is nil", ErrInvalidModuleArtifactIngress)
	}
	manifestOwned := bytes.Clone(manifestCanonical)
	manifestRef, err := validateModuleArtifactCommitInputV1(basis, manifestOwned, coveredFileCount)
	if err != nil {
		return artifactResult, admissionResult, err
	}
	record, recordCanonical, admissionID, err := moduleartifactingress.NewRecordV1(
		moduleartifactingress.RecordV1{
			SchemaVersion:               moduleartifactingress.RecordSchemaVersionV1,
			SourceID:                    basis.Source.SourceID,
			SourcePolicyID:              basis.Source.PolicyID,
			SourcePolicyRevision:        basis.Source.PolicyRevision,
			SnapshotID:                  basis.Snapshot.SnapshotID,
			SnapshotObservationRevision: basis.Snapshot.ObservationRevision,
			EntryOrdinal:                basis.EntryOrdinal,
			PackagePath:                 basis.Entry.PackagePath,
			Module:                      basis.Entry.Module,
			ArtifactDigest:              basis.Entry.ArtifactDigest,
			ArtifactSizeBytes:           basis.Entry.ArtifactSizeBytes,
			ManifestRef:                 manifestRef,
			CoveredFileCount:            coveredFileCount,
		},
	)
	if err != nil {
		return artifactResult, admissionResult, fmt.Errorf("%w: build admission record: %v", ErrInvalidModuleArtifactIngress, err)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return artifactResult, admissionResult, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return artifactResult, admissionResult, fmt.Errorf("currentstore: acquire artifact ingress commit connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return artifactResult, admissionResult, fmt.Errorf("currentstore: begin artifact ingress commit: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()

	// This intentionally precedes the live-basis rebuild. The supplied basis
	// and Manifest have already been restored and projected into the exact
	// canonical admission identity above.
	if existing, found, queryErr := queryModuleArtifactAdmissionV1(ctx, connection, admissionID); queryErr != nil {
		return artifactResult, admissionResult, queryErr
	} else if found {
		if !bytes.Equal(existing.Canonical, recordCanonical) || !reflect.DeepEqual(existing.Record, record) {
			return artifactResult, admissionResult, fmt.Errorf("%w: AdmissionID collision", ErrModuleArtifactIngressIntegrity)
		}
		if err := VerifyModuleArtifactIngressSemanticClosureV1(ctx, connection); err != nil {
			return artifactResult, admissionResult, fmt.Errorf("%w: exact retry closure: %v", ErrModuleArtifactIngressIntegrity, err)
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return artifactResult, admissionResult, fmt.Errorf("currentstore: commit exact artifact ingress retry: %w", err)
		}
		committed = true
		return detachModuleArtifactV1(existing.Artifact), detachModuleArtifactAdmissionV1(existing), nil
	}

	live, err := rebuildModuleArtifactIngressBasisV1(ctx, connection, basis.Selection)
	if err != nil {
		return artifactResult, admissionResult, mapModuleArtifactBasisCommitErrorV1(err)
	}
	if !reflect.DeepEqual(detachModuleArtifactIngressBasisV1(basis), detachModuleArtifactIngressBasisV1(live)) {
		return artifactResult, admissionResult, ErrModuleArtifactIngressStale
	}
	if _, err := validateModuleArtifactCommitInputV1(live, manifestOwned, coveredFileCount); err != nil {
		return artifactResult, admissionResult, err
	}

	now := nowUnixMicro()
	if err := putManifestContent(ctx, connection, manifestRef, manifestOwned, time.UnixMicro(now).UTC()); err != nil {
		return artifactResult, admissionResult, err
	}
	insert, err := connection.ExecContext(ctx, `
		INSERT INTO module_artifacts(
			artifact_digest,module_id,exact_version,manifest_ref,
			artifact_size_bytes,covered_file_count,ingressed_at
		) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(artifact_digest) DO NOTHING
	`, live.Entry.ArtifactDigest, live.Entry.Module.ID, live.Entry.Module.Version,
		manifestRef, int64(live.Entry.ArtifactSizeBytes), int64(coveredFileCount), now)
	if err != nil {
		return artifactResult, admissionResult, mapModuleArtifactWriteErrorV1("insert Artifact", err)
	}
	affected, err := insert.RowsAffected()
	if err != nil || affected < 0 || affected > 1 {
		return artifactResult, admissionResult, fmt.Errorf("%w: inspect Artifact insert: affected=%d err=%v", ErrModuleArtifactIngressIntegrity, affected, err)
	}
	artifactResult, found, err := queryModuleArtifactV1(ctx, connection, live.Entry.ArtifactDigest)
	if err != nil || !found {
		return ModuleArtifactV1{}, admissionResult, fmt.Errorf("%w: read committed Artifact: %v", ErrModuleArtifactIngressIntegrity, err)
	}
	if artifactResult.Module != live.Entry.Module || artifactResult.ManifestRef != manifestRef ||
		artifactResult.ArtifactSizeBytes != live.Entry.ArtifactSizeBytes ||
		artifactResult.CoveredFileCount != coveredFileCount ||
		!bytes.Equal(artifactResult.ManifestCanonical, manifestOwned) {
		return ModuleArtifactV1{}, admissionResult, fmt.Errorf("%w: existing Artifact differs from verified object", ErrModuleArtifactIngressConflict)
	}
	_, err = connection.ExecContext(ctx, `
		INSERT INTO module_artifact_admissions(
			admission_id,admission_canonical,admission_size_bytes,
			source_id,source_policy_id,source_policy_revision,
			snapshot_id,observation_revision,entry_ordinal,
			artifact_digest,admitted_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?)
	`, admissionID, recordCanonical, len(recordCanonical), record.SourceID,
		record.SourcePolicyID, int64(record.SourcePolicyRevision), record.SnapshotID,
		int64(record.SnapshotObservationRevision), int64(record.EntryOrdinal),
		record.ArtifactDigest, now)
	if err != nil {
		return ModuleArtifactV1{}, admissionResult, mapModuleArtifactWriteErrorV1("insert Admission", err)
	}
	admissionResult, found, err = queryModuleArtifactAdmissionV1(ctx, connection, admissionID)
	if err != nil || !found {
		return ModuleArtifactV1{}, ModuleArtifactAdmissionV1{}, fmt.Errorf("%w: re-read Admission: %v", ErrModuleArtifactIngressIntegrity, err)
	}
	if err := VerifyModuleArtifactIngressSemanticClosureV1(ctx, connection); err != nil {
		return ModuleArtifactV1{}, ModuleArtifactAdmissionV1{}, fmt.Errorf("%w: pre-commit semantic closure: %v", ErrModuleArtifactIngressIntegrity, err)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModuleArtifactV1{}, ModuleArtifactAdmissionV1{}, fmt.Errorf("currentstore: commit artifact ingress: %w", err)
	}
	committed = true
	return detachModuleArtifactV1(artifactResult), detachModuleArtifactAdmissionV1(admissionResult), nil
}

// GetModuleArtifactAdmissionV1 returns one detached immutable Admission and
// its complete Artifact/Manifest closure.
func (store *Store) GetModuleArtifactAdmissionV1(ctx context.Context, admissionID string) (ModuleArtifactAdmissionV1, error) {
	if ctx == nil || !moduleapi.ValidSHA256(admissionID) {
		return ModuleArtifactAdmissionV1{}, ErrInvalidModuleArtifactIngress
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleArtifactAdmissionV1{}, err
	}
	defer unlock()
	value, found, err := queryModuleArtifactAdmissionV1(ctx, store.db, admissionID)
	if err != nil {
		return ModuleArtifactAdmissionV1{}, err
	}
	if !found {
		return ModuleArtifactAdmissionV1{}, ErrModuleArtifactAdmissionNotFound
	}
	return detachModuleArtifactAdmissionV1(value), nil
}

// GetModuleArtifactAdmissionBySelectionV1 resolves only an already committed
// exact Admission. It deliberately does not require the Source's current head
// to remain at the admitted Snapshot: a caller retrying a lost response must
// be able to recover the immutable result after discovery advances. An unseen
// historical selection is not reconstructed and therefore remains absent.
func (store *Store) GetModuleArtifactAdmissionBySelectionV1(
	ctx context.Context,
	selection ModuleArtifactIngressSelectionV1,
) (result ModuleArtifactAdmissionV1, found bool, returnErr error) {
	if ctx == nil || selection.SourceID == "" ||
		selection.SourceID != strings.TrimSpace(selection.SourceID) ||
		len(selection.SourceID) > 128 ||
		!moduleapi.ValidSHA256(selection.SnapshotID) ||
		!moduleapi.ValidSHA256(selection.ArtifactDigest) ||
		selection.Module.Validate() != nil {
		return result, false, ErrInvalidModuleArtifactIngress
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return result, false, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, false, fmt.Errorf("currentstore: acquire exact artifact ingress lookup connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return result, false, fmt.Errorf("currentstore: begin exact artifact ingress lookup: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	rows, err := connection.QueryContext(ctx, `
		SELECT admission.admission_id
		FROM module_artifact_admissions AS admission
		JOIN module_artifacts AS artifact
		  ON artifact.artifact_digest=admission.artifact_digest
		WHERE admission.source_id=?
		  AND admission.snapshot_id=?
		  AND admission.artifact_digest=?
		  AND artifact.module_id=?
		  AND artifact.exact_version=?
		ORDER BY admission.admission_id
		LIMIT 2
	`, selection.SourceID, selection.SnapshotID, selection.ArtifactDigest,
		selection.Module.ID, selection.Module.Version)
	if err != nil {
		return result, false, fmt.Errorf("currentstore: query exact artifact ingress selection: %w", err)
	}
	var admissionIDs []string
	for rows.Next() {
		var admissionID string
		if err := rows.Scan(&admissionID); err != nil {
			_ = rows.Close()
			return result, false, fmt.Errorf("currentstore: scan exact artifact ingress selection: %w", err)
		}
		admissionIDs = append(admissionIDs, admissionID)
	}
	rowsErr := rows.Err()
	closeErr := rows.Close()
	if rowsErr != nil || closeErr != nil {
		return result, false, errors.Join(rowsErr, closeErr)
	}
	if len(admissionIDs) > 1 {
		return result, false, fmt.Errorf("%w: exact ingress selection is ambiguous", ErrModuleArtifactIngressIntegrity)
	}
	if len(admissionIDs) == 1 {
		result, found, err = queryModuleArtifactAdmissionV1(ctx, connection, admissionIDs[0])
		if err != nil || !found {
			return ModuleArtifactAdmissionV1{}, false, fmt.Errorf("%w: exact ingress Admission closure: %v", ErrModuleArtifactIngressIntegrity, err)
		}
		if result.Record.SourceID != selection.SourceID ||
			result.Record.SnapshotID != selection.SnapshotID ||
			result.Record.Module != selection.Module ||
			result.Record.ArtifactDigest != selection.ArtifactDigest {
			return ModuleArtifactAdmissionV1{}, false, fmt.Errorf("%w: exact ingress selection projection differs", ErrModuleArtifactIngressIntegrity)
		}
		if err := VerifyModuleArtifactIngressSemanticClosureV1(ctx, connection); err != nil {
			return ModuleArtifactAdmissionV1{}, false, fmt.Errorf("%w: exact ingress semantic closure: %v", ErrModuleArtifactIngressIntegrity, err)
		}
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModuleArtifactAdmissionV1{}, false, fmt.Errorf("currentstore: commit exact artifact ingress lookup: %w", err)
	}
	committed = true
	if !found {
		return ModuleArtifactAdmissionV1{}, false, nil
	}
	return detachModuleArtifactAdmissionV1(result), true, nil
}

// IsModuleArtifactInstalledV1 reports whether the exact digest is already
// referenced by an immutable module installation. Artifact ingress uses this
// Store-owned fact only to distinguish a legacy installed LOCAL_PROCESS tree,
// whose descriptor-bound executable may already be 0700, from a fresh inert
// ingress object, whose ordinary files must all remain 0600.
func (store *Store) IsModuleArtifactInstalledV1(
	ctx context.Context,
	artifactDigest string,
) (bool, error) {
	if ctx == nil || !moduleapi.ValidSHA256(artifactDigest) {
		return false, ErrInvalidModuleArtifactIngress
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return false, err
	}
	defer unlock()

	var count int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM module_installations WHERE artifact_digest=?
	`, artifactDigest).Scan(&count); err != nil {
		return false, fmt.Errorf("currentstore: inspect installed artifact: %w", err)
	}
	if count < 0 || count > 1 {
		return false, fmt.Errorf("%w: installed artifact identity is ambiguous", ErrModuleArtifactIngressIntegrity)
	}
	return count == 1, nil
}

func rebuildModuleArtifactIngressBasisV1(
	ctx context.Context,
	q moduleDiscoveryQueryer,
	selection ModuleArtifactIngressSelectionV1,
) (ModuleArtifactIngressBasisV1, error) {
	if selection.SourceID == "" || selection.SourceID != strings.TrimSpace(selection.SourceID) || len(selection.SourceID) > 128 ||
		!moduleapi.ValidSHA256(selection.SnapshotID) || !moduleapi.ValidSHA256(selection.ArtifactDigest) {
		return ModuleArtifactIngressBasisV1{}, fmt.Errorf("%w: invalid source, Snapshot, or artifact identity", ErrInvalidModuleArtifactIngress)
	}
	if err := selection.Module.Validate(); err != nil {
		return ModuleArtifactIngressBasisV1{}, fmt.Errorf("%w: ModuleRef: %v", ErrInvalidModuleArtifactIngress, err)
	}
	source, found, err := queryModuleSource(ctx, q, selection.SourceID)
	if err != nil {
		return ModuleArtifactIngressBasisV1{}, err
	}
	if !found {
		return ModuleArtifactIngressBasisV1{}, ErrModuleSourceNotFound
	}
	if source.Policy.Kind != moduleapi.ModuleSourceKindLocalDirectoryV1 ||
		source.Policy.Network != moduleapi.ModuleSourceNetworkDenyV1 ||
		source.Policy.SignatureRequired || source.Policy.PublisherKeyID != "" {
		return ModuleArtifactIngressBasisV1{}, fmt.Errorf("%w: ingress requires an unsigned LOCAL_DIRECTORY+DENY source", ErrInvalidModuleArtifactIngress)
	}
	if source.CurrentSnapshotID != selection.SnapshotID {
		return ModuleArtifactIngressBasisV1{}, ErrModuleArtifactIngressStale
	}
	snapshot, found, err := queryModuleDiscoverySnapshot(ctx, q, selection.SnapshotID)
	if err != nil {
		return ModuleArtifactIngressBasisV1{}, err
	}
	if !found {
		return ModuleArtifactIngressBasisV1{}, ErrModuleDiscoverySnapshotNotFound
	}
	if snapshot.SourceID != source.SourceID || snapshot.SourcePolicyID != source.PolicyID ||
		snapshot.SourcePolicyRevision != source.PolicyRevision || snapshot.ObservationRevision != source.ObservationRevision ||
		snapshot.PublisherKeyID != "" || snapshot.PublisherKeyRevision != 0 {
		return ModuleArtifactIngressBasisV1{}, ErrModuleArtifactIngressStale
	}
	for ordinal, entry := range snapshot.Snapshot.Entries {
		if entry.Module != selection.Module || entry.ArtifactDigest != selection.ArtifactDigest {
			continue
		}
		if entry.SignatureID != "" {
			return ModuleArtifactIngressBasisV1{}, fmt.Errorf("%w: unsigned ingress entry unexpectedly carries a Signature", ErrInvalidModuleArtifactIngress)
		}
		if err := moduleapi.ValidateModuleSourceCandidateV1(source.Policy, entry.Module, entry.ArtifactSizeBytes, false); err != nil {
			return ModuleArtifactIngressBasisV1{}, fmt.Errorf("%w: source candidate: %v", ErrInvalidModuleArtifactIngress, err)
		}
		return ModuleArtifactIngressBasisV1{
			Selection: selection, Source: source, Snapshot: snapshot,
			EntryOrdinal: uint32(ordinal), Entry: entry,
		}, nil
	}
	return ModuleArtifactIngressBasisV1{}, fmt.Errorf("%w: exact entry absent from current Snapshot", ErrInvalidModuleArtifactIngress)
}

func validateModuleArtifactCommitInputV1(
	basis ModuleArtifactIngressBasisV1,
	manifestCanonical []byte,
	coveredFileCount uint64,
) (string, error) {
	if err := requireSelfConsistentModuleArtifactBasisV1(basis); err != nil {
		return "", err
	}
	if coveredFileCount == 0 || coveredFileCount > uint64(moduleapi.DefaultArtifactMaxFiles) {
		return "", fmt.Errorf("%w: covered file count is outside v1 bounds", ErrInvalidModuleArtifactIngress)
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(bytes.Clone(manifestCanonical))
	if err != nil || !bytes.Equal(canonical, manifestCanonical) {
		return "", fmt.Errorf("%w: Manifest is not exact canonical: %v", ErrInvalidModuleArtifactIngress, err)
	}
	if manifest.ID != basis.Entry.Module.ID || manifest.Version != basis.Entry.Module.Version {
		return "", fmt.Errorf("%w: Manifest identity differs from selected entry", ErrInvalidModuleArtifactIngress)
	}
	manifestRef, err := ComputeContentDigest(ContentModuleManifest, moduleManifestMediaType, manifestCanonical)
	if err != nil {
		return "", fmt.Errorf("%w: compute ManifestRef: %v", ErrInvalidModuleArtifactIngress, err)
	}
	return manifestRef, nil
}

func requireSelfConsistentModuleArtifactBasisV1(basis ModuleArtifactIngressBasisV1) error {
	policy, policyCanonical, policyID, err := moduleapi.ParseModuleSourcePolicyV1(bytes.Clone(basis.Source.PolicyCanonical))
	if err != nil || policyID != basis.Source.PolicyID || !bytes.Equal(policyCanonical, basis.Source.PolicyCanonical) ||
		!reflect.DeepEqual(policy, basis.Source.Policy) {
		return fmt.Errorf("%w: SourcePolicy basis is invalid", ErrInvalidModuleArtifactIngress)
	}
	if policy.Kind != moduleapi.ModuleSourceKindLocalDirectoryV1 || policy.Network != moduleapi.ModuleSourceNetworkDenyV1 ||
		policy.SignatureRequired || policy.PublisherKeyID != "" {
		return fmt.Errorf("%w: ingress basis is not unsigned LOCAL_DIRECTORY+DENY", ErrInvalidModuleArtifactIngress)
	}
	if basis.Selection.SourceID != basis.Source.SourceID || basis.Selection.SnapshotID != basis.Source.CurrentSnapshotID ||
		basis.Selection.SnapshotID != basis.Snapshot.SnapshotID || basis.Snapshot.SourceID != basis.Source.SourceID ||
		basis.Snapshot.SourcePolicyID != basis.Source.PolicyID || basis.Snapshot.SourcePolicyRevision != basis.Source.PolicyRevision ||
		basis.Snapshot.ObservationRevision != basis.Source.ObservationRevision || basis.Snapshot.PublisherKeyID != "" ||
		basis.Snapshot.PublisherKeyRevision != 0 || uint64(basis.EntryOrdinal) >= uint64(len(basis.Snapshot.Snapshot.Entries)) {
		return fmt.Errorf("%w: Source/Snapshot basis closure differs", ErrInvalidModuleArtifactIngress)
	}
	restored, err := moduleapi.RestoreModuleDiscoverySnapshotV1(
		bytes.Clone(basis.Snapshot.SnapshotCanonical), basis.Snapshot.SnapshotID,
		bytes.Clone(basis.Snapshot.SourcePolicyCanonical), basis.Snapshot.SourcePolicyID,
		bytes.Clone(basis.Snapshot.IndexCanonical), basis.Snapshot.IndexID,
	)
	if err != nil || !reflect.DeepEqual(restored, basis.Snapshot.Snapshot) {
		return fmt.Errorf("%w: Snapshot basis is invalid", ErrInvalidModuleArtifactIngress)
	}
	if !reflect.DeepEqual(basis.Snapshot.Snapshot.Entries[basis.EntryOrdinal], basis.Entry) ||
		basis.Selection.Module != basis.Entry.Module || basis.Selection.ArtifactDigest != basis.Entry.ArtifactDigest ||
		basis.Entry.SignatureID != "" {
		return fmt.Errorf("%w: exact entry basis differs", ErrInvalidModuleArtifactIngress)
	}
	if err := moduleapi.ValidateModuleSourceCandidateV1(policy, basis.Entry.Module, basis.Entry.ArtifactSizeBytes, false); err != nil {
		return fmt.Errorf("%w: entry violates SourcePolicy: %v", ErrInvalidModuleArtifactIngress, err)
	}
	return nil
}

func queryModuleArtifactV1(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, artifactDigest string) (ModuleArtifactV1, bool, error) {
	var value ModuleArtifactV1
	var moduleID, exactVersion string
	var artifactSize, coveredFiles, ingressedAt int64
	err := q.QueryRowContext(ctx, `
		SELECT artifact_digest,module_id,exact_version,manifest_ref,
		       artifact_size_bytes,covered_file_count,ingressed_at
		FROM module_artifacts WHERE artifact_digest=?
	`, artifactDigest).Scan(&value.ArtifactDigest, &moduleID, &exactVersion,
		&value.ManifestRef, &artifactSize, &coveredFiles, &ingressedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ModuleArtifactV1{}, false, nil
	}
	if err != nil {
		return ModuleArtifactV1{}, false, fmt.Errorf("currentstore: read module Artifact %s: %w", artifactDigest, err)
	}
	if artifactSize <= 0 || coveredFiles <= 0 {
		return ModuleArtifactV1{}, false, fmt.Errorf("%w: Artifact %s has invalid bounds", ErrModuleArtifactIngressIntegrity, artifactDigest)
	}
	value.Module = moduleapi.Ref{ID: moduleID, Version: exactVersion}
	value.ArtifactSizeBytes = uint64(artifactSize)
	value.CoveredFileCount = uint64(coveredFiles)
	value.IngressedAt, err = timeFromUnixMicro(ingressedAt)
	if err != nil {
		return ModuleArtifactV1{}, false, fmt.Errorf("%w: Artifact %s has invalid timestamp", ErrModuleArtifactIngressIntegrity, artifactDigest)
	}
	content, err := queryContent(ctx, q, value.ManifestRef)
	if err != nil || content.Kind != ContentModuleManifest || content.MediaType != moduleManifestMediaType {
		return ModuleArtifactV1{}, false, fmt.Errorf("%w: Artifact %s Manifest closure: %v", ErrModuleArtifactIngressIntegrity, artifactDigest, err)
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(content.CanonicalBytes)
	if err != nil || !bytes.Equal(canonical, content.CanonicalBytes) || manifest.ID != moduleID || manifest.Version != exactVersion {
		return ModuleArtifactV1{}, false, fmt.Errorf("%w: Artifact %s Manifest identity differs", ErrModuleArtifactIngressIntegrity, artifactDigest)
	}
	value.ManifestCanonical = bytes.Clone(content.CanonicalBytes)
	return value, true, nil
}

func queryModuleArtifactAdmissionV1(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, admissionID string) (ModuleArtifactAdmissionV1, bool, error) {
	var value ModuleArtifactAdmissionV1
	var canonical []byte
	var sourceID, policyID, snapshotID, artifactDigest string
	var size, policyRevision, observationRevision, ordinal, admittedAt int64
	err := q.QueryRowContext(ctx, `
		SELECT admission_id,admission_canonical,admission_size_bytes,
		       source_id,source_policy_id,source_policy_revision,
		       snapshot_id,observation_revision,entry_ordinal,
		       artifact_digest,admitted_at
		FROM module_artifact_admissions WHERE admission_id=?
	`, admissionID).Scan(&value.AdmissionID, &canonical, &size, &sourceID, &policyID,
		&policyRevision, &snapshotID, &observationRevision, &ordinal,
		&artifactDigest, &admittedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ModuleArtifactAdmissionV1{}, false, nil
	}
	if err != nil {
		return ModuleArtifactAdmissionV1{}, false, fmt.Errorf("currentstore: read module Artifact Admission %s: %w", admissionID, err)
	}
	if size != int64(len(canonical)) || policyRevision <= 0 || observationRevision <= 0 || ordinal < 0 {
		return ModuleArtifactAdmissionV1{}, false, fmt.Errorf("%w: Admission %s has invalid projection", ErrModuleArtifactIngressIntegrity, admissionID)
	}
	record, err := moduleartifactingress.RestoreRecordV1(bytes.Clone(canonical), admissionID)
	if err != nil || record.SourceID != sourceID ||
		record.SourcePolicyID != policyID || record.SourcePolicyRevision != uint64(policyRevision) ||
		record.SnapshotID != snapshotID || record.SnapshotObservationRevision != uint64(observationRevision) ||
		int64(record.EntryOrdinal) != ordinal || record.ArtifactDigest != artifactDigest {
		return ModuleArtifactAdmissionV1{}, false, fmt.Errorf("%w: Admission %s canonical projection differs", ErrModuleArtifactIngressIntegrity, admissionID)
	}
	artifact, found, err := queryModuleArtifactV1(ctx, q, artifactDigest)
	if err != nil || !found {
		return ModuleArtifactAdmissionV1{}, false, fmt.Errorf("%w: Admission %s Artifact closure: %v", ErrModuleArtifactIngressIntegrity, admissionID, err)
	}
	value.Record = record
	value.Canonical = bytes.Clone(canonical)
	value.Artifact = artifact
	value.AdmittedAt, err = timeFromUnixMicro(admittedAt)
	if err != nil {
		return ModuleArtifactAdmissionV1{}, false, fmt.Errorf("%w: Admission %s has invalid timestamp", ErrModuleArtifactIngressIntegrity, admissionID)
	}
	return value, true, nil
}

func detachModuleArtifactIngressBasisV1(value ModuleArtifactIngressBasisV1) ModuleArtifactIngressBasisV1 {
	value.Source = detachModuleSource(value.Source)
	value.Snapshot = detachModuleDiscoverySnapshot(value.Snapshot)
	return value
}

func detachModuleArtifactV1(value ModuleArtifactV1) ModuleArtifactV1 {
	value.ManifestCanonical = bytes.Clone(value.ManifestCanonical)
	return value
}

func detachModuleArtifactAdmissionV1(value ModuleArtifactAdmissionV1) ModuleArtifactAdmissionV1 {
	value.Canonical = bytes.Clone(value.Canonical)
	value.Artifact = detachModuleArtifactV1(value.Artifact)
	return value
}

func mapModuleArtifactBasisCommitErrorV1(err error) error {
	if errors.Is(err, ErrModuleArtifactIngressStale) || errors.Is(err, ErrModuleSourceNotFound) ||
		errors.Is(err, ErrModuleDiscoverySnapshotNotFound) || errors.Is(err, ErrInvalidModuleArtifactIngress) {
		return errors.Join(ErrModuleArtifactIngressStale, err)
	}
	return err
}

func mapModuleArtifactWriteErrorV1(operation string, err error) error {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "quota exceeded"):
		return fmt.Errorf("%w: %s: %v", ErrModuleArtifactIngressQuota, operation, err)
	case strings.Contains(message, "unique constraint"), strings.Contains(message, "parent is not exact"):
		return fmt.Errorf("%w: %s: %v", ErrModuleArtifactIngressConflict, operation, err)
	default:
		return fmt.Errorf("currentstore: %s: %w", operation, err)
	}
}
