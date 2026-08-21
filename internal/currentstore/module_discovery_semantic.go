package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// VerifyModuleDiscoverySemanticClosureV1 rebuilds the complete persisted
// SourcePolicy -> Index -> Snapshot closure from one coherent SQL snapshot.
// It is deliberately pure Store verification: it performs no filesystem or
// network I/O and never downloads, installs, grants, activates, or binds a
// module.
func VerifyModuleDiscoverySemanticClosureV1(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
) error {
	if ctx == nil || queryer == nil {
		return fmt.Errorf("%w: nil discovery semantic verifier input", ErrModuleDiscoveryIntegrity)
	}

	keyIDs, err := readDiscoveryIdentityColumn(
		ctx,
		queryer,
		`SELECT publisher_key_id FROM module_publisher_keys ORDER BY publisher_key_id COLLATE BINARY`,
	)
	if err != nil {
		return fmt.Errorf("%w: list PublisherKeys: %v", ErrModuleDiscoveryIntegrity, err)
	}
	keys := make(map[string]ModulePublisherKey, len(keyIDs))
	for _, keyID := range keyIDs {
		key, found, err := queryModulePublisherKey(ctx, queryer, keyID)
		if err != nil || !found {
			return fmt.Errorf("%w: read PublisherKey %s: %v", ErrModuleDiscoveryIntegrity, keyID, err)
		}
		_, canonical, restoredID, err := moduleapi.ParseModulePublisherKeyV1(key.Canonical)
		if err != nil || restoredID != key.PublisherKeyID || !bytes.Equal(canonical, key.Canonical) {
			return fmt.Errorf("%w: PublisherKey %s canonical identity is invalid", ErrModuleDiscoveryIntegrity, keyID)
		}
		if key.RevokedAt == nil && key.Revision != 1 ||
			key.RevokedAt != nil && key.Revision != 2 {
			return fmt.Errorf("%w: PublisherKey %s has impossible revision/revocation state", ErrModuleDiscoveryIntegrity, keyID)
		}
		keys[keyID] = key
	}

	sourceIDs, err := readDiscoveryIdentityColumn(
		ctx,
		queryer,
		`SELECT source_id FROM module_sources ORDER BY source_id COLLATE BINARY`,
	)
	if err != nil {
		return fmt.Errorf("%w: list Sources: %v", ErrModuleDiscoveryIntegrity, err)
	}
	sources := make(map[string]ModuleSource, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		source, found, err := queryModuleSource(ctx, queryer, sourceID)
		if err != nil || !found {
			return fmt.Errorf("%w: read Source %q: %v", ErrModuleDiscoveryIntegrity, sourceID, err)
		}
		if source.PolicyRevision == 0 || source.UpdatedAt.Before(source.RegisteredAt) {
			return fmt.Errorf("%w: Source %q has invalid revision or timestamps", ErrModuleDiscoveryIntegrity, sourceID)
		}
		if source.Policy.SignatureRequired {
			if _, found := keys[source.Policy.PublisherKeyID]; !found {
				return fmt.Errorf("%w: Source %q references a missing PublisherKey", ErrModuleDiscoveryIntegrity, sourceID)
			}
		}
		sources[sourceID] = source
	}

	snapshotIDs, err := readDiscoveryIdentityColumn(
		ctx,
		queryer,
		`SELECT snapshot_id FROM module_discovery_snapshots ORDER BY source_id COLLATE BINARY, observation_revision, snapshot_id COLLATE BINARY`,
	)
	if err != nil {
		return fmt.Errorf("%w: list Snapshots: %v", ErrModuleDiscoveryIntegrity, err)
	}
	type sourceObservationState struct {
		count              uint64
		lastID             string
		lastPolicyRevision uint64
		policyByRev        map[uint64]string
	}
	observations := make(map[string]*sourceObservationState, len(sources))
	for sourceID := range sources {
		observations[sourceID] = &sourceObservationState{policyByRev: make(map[uint64]string)}
	}

	for _, snapshotID := range snapshotIDs {
		record, found, err := queryModuleDiscoverySnapshot(ctx, queryer, snapshotID)
		if err != nil || !found {
			return fmt.Errorf("%w: read Snapshot %s: %v", ErrModuleDiscoveryIntegrity, snapshotID, err)
		}
		source, found := sources[record.SourceID]
		if !found {
			return fmt.Errorf("%w: Snapshot %s references missing Source", ErrModuleDiscoveryIntegrity, snapshotID)
		}
		policy, _, policyID, err := moduleapi.ParseModuleSourcePolicyV1(record.SourcePolicyCanonical)
		if err != nil || policyID != record.SourcePolicyID || policy.SourceID != record.SourceID {
			return fmt.Errorf("%w: Snapshot %s has invalid SourcePolicy parent", ErrModuleDiscoveryIntegrity, snapshotID)
		}
		// SourceID is a stable suppression boundary. Historical policies may
		// change limits, prefixes, signature requirements, or keys, but never
		// the physical source identity represented by Kind+OriginDigest.
		if policy.Kind != source.Policy.Kind || policy.OriginDigest != source.Policy.OriginDigest ||
			record.SourcePolicyRevision == 0 || record.SourcePolicyRevision > source.PolicyRevision {
			return fmt.Errorf("%w: Snapshot %s violates immutable Source identity/revision", ErrModuleDiscoveryIntegrity, snapshotID)
		}
		if record.SourcePolicyRevision == source.PolicyRevision &&
			record.SourcePolicyID != source.PolicyID {
			return fmt.Errorf("%w: Snapshot %s differs from its current policy revision", ErrModuleDiscoveryIntegrity, snapshotID)
		}
		state := observations[record.SourceID]
		state.count++
		if record.ObservationRevision != state.count {
			return fmt.Errorf("%w: Source %q observation revisions are not contiguous", ErrModuleDiscoveryIntegrity, record.SourceID)
		}
		if state.count > 1 &&
			record.SourcePolicyRevision < state.lastPolicyRevision {
			return fmt.Errorf(
				"%w: Source %q observation policy revisions move backwards",
				ErrModuleDiscoveryIntegrity,
				record.SourceID,
			)
		}
		state.lastID = record.SnapshotID
		state.lastPolicyRevision = record.SourcePolicyRevision
		if existing := state.policyByRev[record.SourcePolicyRevision]; existing != "" && existing != record.SourcePolicyID {
			return fmt.Errorf("%w: Source %q policy revision maps to multiple policies", ErrModuleDiscoveryIntegrity, record.SourceID)
		}
		state.policyByRev[record.SourcePolicyRevision] = record.SourcePolicyID

		if policy.SignatureRequired {
			key, found := keys[policy.PublisherKeyID]
			if !found || record.PublisherKeyID != policy.PublisherKeyID ||
				record.PublisherKeyRevision != 1 || key.Revision < record.PublisherKeyRevision ||
				record.ObservedAt.Before(key.ImportedAt) ||
				key.RevokedAt != nil && record.ObservedAt.After(*key.RevokedAt) {
				return fmt.Errorf("%w: Snapshot %s has invalid PublisherKey observation basis", ErrModuleDiscoveryIntegrity, snapshotID)
			}
		} else if record.PublisherKeyID != "" || record.PublisherKeyRevision != 0 {
			return fmt.Errorf("%w: unsigned Snapshot %s carries PublisherKey authority", ErrModuleDiscoveryIntegrity, snapshotID)
		}
		if err := verifyDiscoveryEntryProjection(ctx, queryer, record); err != nil {
			return err
		}
	}

	for sourceID, source := range sources {
		state := observations[sourceID]
		if state.count != source.ObservationRevision {
			return fmt.Errorf("%w: Source %q observation head revision is inconsistent", ErrModuleDiscoveryIntegrity, sourceID)
		}
		if source.CurrentSnapshotID != "" {
			if state.lastID != source.CurrentSnapshotID {
				return fmt.Errorf("%w: Source %q current Snapshot is not its observation head", ErrModuleDiscoveryIntegrity, sourceID)
			}
			current, found, err := queryModuleDiscoverySnapshot(ctx, queryer, source.CurrentSnapshotID)
			if err != nil || !found || current.SourceID != sourceID ||
				current.SourcePolicyID != source.PolicyID ||
				current.SourcePolicyRevision != source.PolicyRevision {
				return fmt.Errorf("%w: Source %q current Snapshot parent closure is invalid", ErrModuleDiscoveryIntegrity, sourceID)
			}
		} else if state.count > 0 && state.lastPolicyRevision >= source.PolicyRevision {
			// A Source may have no current Snapshot after an explicit Policy
			// revision, but a refreshed current Policy must always point at its
			// immutable observation head. This rejects a cleared/tampered head
			// without invalidating legitimate policy updates awaiting refresh.
			return fmt.Errorf("%w: Source %q omits its current observation head", ErrModuleDiscoveryIntegrity, sourceID)
		}
	}

	if err := verifyDiscoveryGlobalModuleRefs(ctx, queryer); err != nil {
		return err
	}
	return nil
}

func readDiscoveryIdentityColumn(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
	statement string,
) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			_ = rows.Close()
			return nil, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return values, nil
}

func verifyDiscoveryEntryProjection(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
	record ModuleDiscoverySnapshot,
) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT entry_ordinal,module_id,exact_version,artifact_digest,
		       artifact_size_bytes,package_path,signature_id
		FROM module_discovery_entries
		WHERE snapshot_id=?
		ORDER BY entry_ordinal
	`, record.SnapshotID)
	if err != nil {
		return fmt.Errorf("%w: list Snapshot %s entries: %v", ErrModuleDiscoveryIntegrity, record.SnapshotID, err)
	}
	position := 0
	for rows.Next() {
		var ordinal int
		var moduleID, exactVersion, digest, packagePath string
		var size int64
		var signature sql.NullString
		if err := rows.Scan(&ordinal, &moduleID, &exactVersion, &digest, &size, &packagePath, &signature); err != nil {
			_ = rows.Close()
			return fmt.Errorf("%w: scan Snapshot entry: %v", ErrModuleDiscoveryIntegrity, err)
		}
		if position >= len(record.Snapshot.Entries) || ordinal != position {
			_ = rows.Close()
			return fmt.Errorf("%w: Snapshot %s entry ordinals/count differ", ErrModuleDiscoveryIntegrity, record.SnapshotID)
		}
		expected := record.Snapshot.Entries[position]
		storedSignature := ""
		if signature.Valid {
			storedSignature = signature.String
		}
		if moduleID != expected.Module.ID || exactVersion != expected.Module.Version ||
			digest != expected.ArtifactDigest || size <= 0 || uint64(size) != expected.ArtifactSizeBytes ||
			packagePath != expected.PackagePath || storedSignature != expected.SignatureID {
			_ = rows.Close()
			return fmt.Errorf("%w: Snapshot %s entry %d differs from canonical parent", ErrModuleDiscoveryIntegrity, record.SnapshotID, position)
		}
		position++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("%w: iterate Snapshot entries: %v", ErrModuleDiscoveryIntegrity, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("%w: close Snapshot entries: %v", ErrModuleDiscoveryIntegrity, err)
	}
	if position != len(record.Snapshot.Entries) {
		return fmt.Errorf("%w: Snapshot %s entry count differs from canonical parent", ErrModuleDiscoveryIntegrity, record.SnapshotID)
	}
	return nil
}

func verifyDiscoveryGlobalModuleRefs(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
) error {
	rows, err := queryer.QueryContext(ctx, `
		SELECT module_id,exact_version,artifact_digest,first_source_id,
		       first_snapshot_id,first_observed_at
		FROM module_discovery_module_refs
		ORDER BY module_id COLLATE BINARY,exact_version COLLATE BINARY
	`)
	if err != nil {
		return fmt.Errorf("%w: list global ModuleRefs: %v", ErrModuleDiscoveryIntegrity, err)
	}
	type globalRef struct {
		moduleID, version, digest, sourceID, snapshotID string
		observedAt                                      int64
	}
	var refs []globalRef
	for rows.Next() {
		var ref globalRef
		if err := rows.Scan(&ref.moduleID, &ref.version, &ref.digest, &ref.sourceID, &ref.snapshotID, &ref.observedAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("%w: scan global ModuleRef: %v", ErrModuleDiscoveryIntegrity, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("%w: iterate global ModuleRefs: %v", ErrModuleDiscoveryIntegrity, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("%w: close global ModuleRefs: %v", ErrModuleDiscoveryIntegrity, err)
	}
	for _, ref := range refs {
		var entryCount int
		if err := queryer.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM module_discovery_entries
			WHERE module_id=? AND exact_version=? AND artifact_digest=?
		`, ref.moduleID, ref.version, ref.digest).Scan(&entryCount); err != nil || entryCount == 0 {
			return fmt.Errorf("%w: global ModuleRef %s@%s is orphaned", ErrModuleDiscoveryIntegrity, ref.moduleID, ref.version)
		}
		var firstDigest string
		var snapshotObserved int64
		if err := queryer.QueryRowContext(ctx, `
			SELECT e.artifact_digest,s.observed_at
			FROM module_discovery_entries e
			JOIN module_discovery_snapshots s ON s.snapshot_id=e.snapshot_id
			WHERE e.snapshot_id=? AND s.source_id=? AND e.module_id=? AND e.exact_version=?
		`, ref.snapshotID, ref.sourceID, ref.moduleID, ref.version).Scan(&firstDigest, &snapshotObserved); err != nil || firstDigest != ref.digest || snapshotObserved != ref.observedAt {
			return fmt.Errorf("%w: global ModuleRef %s@%s first-observation closure is invalid", ErrModuleDiscoveryIntegrity, ref.moduleID, ref.version)
		}
		var externalDigest string
		err := queryer.QueryRowContext(ctx, `SELECT artifact_digest FROM module_installations WHERE module_id=? AND exact_version=?`, ref.moduleID, ref.version).Scan(&externalDigest)
		if err == nil && externalDigest != ref.digest {
			return fmt.Errorf("%w: global ModuleRef %s@%s conflicts with Installation", ErrModuleDiscoveryIntegrity, ref.moduleID, ref.version)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: inspect Installation closure: %v", ErrModuleDiscoveryIntegrity, err)
		}
		err = queryer.QueryRowContext(ctx, `SELECT artifact_digest FROM learning_proposals WHERE target_id=? AND target_version=? AND version_id IS NOT NULL`, ref.moduleID, ref.version).Scan(&externalDigest)
		if err == nil && externalDigest != ref.digest {
			return fmt.Errorf("%w: global ModuleRef %s@%s conflicts with Learning Version", ErrModuleDiscoveryIntegrity, ref.moduleID, ref.version)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: inspect Learning Version closure: %v", ErrModuleDiscoveryIntegrity, err)
		}
	}
	var distinctEntryRefs, globalRefCount int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT module_id,exact_version,artifact_digest
			FROM module_discovery_entries
			GROUP BY module_id,exact_version,artifact_digest
		)
	`).Scan(&distinctEntryRefs); err != nil {
		return fmt.Errorf("%w: count projected ModuleRefs: %v", ErrModuleDiscoveryIntegrity, err)
	}
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM module_discovery_module_refs`).Scan(&globalRefCount); err != nil {
		return fmt.Errorf("%w: count global ModuleRefs: %v", ErrModuleDiscoveryIntegrity, err)
	}
	if distinctEntryRefs != globalRefCount {
		return fmt.Errorf("%w: global ModuleRef projection count differs from entries", ErrModuleDiscoveryIntegrity)
	}
	return nil
}
