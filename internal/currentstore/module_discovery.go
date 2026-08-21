package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidModuleSource             = errors.New("currentstore: invalid module source request")
	ErrModuleSourceNotFound            = errors.New("currentstore: module source not found")
	ErrModuleSourceConflict            = errors.New("currentstore: module source conflict")
	ErrModuleSourceStale               = errors.New("currentstore: module source revision is stale")
	ErrModulePublisherKeyNotFound      = errors.New("currentstore: module publisher key not found")
	ErrModulePublisherKeyRevoked       = errors.New("currentstore: module publisher key is revoked")
	ErrModuleDiscoverySnapshotNotFound = errors.New("currentstore: module discovery snapshot not found")
	ErrModuleDiscoveryIntegrity        = errors.New("currentstore: module discovery integrity violation")
)

// ModulePublisherKey is local key material together with its irrevocable
// revocation state. It grants no authority outside a registered SourcePolicy.
type ModulePublisherKey struct {
	PublisherKeyID string
	Canonical      []byte
	Revision       uint64
	ImportedAt     time.Time
	RevokedAt      *time.Time
}

// ModuleSource is the current Operator policy for one immutable source
// identity. SourceID can never be rebound to another Kind or OriginDigest.
type ModuleSource struct {
	SourceID            string
	Policy              moduleapi.ModuleSourcePolicyV1
	PolicyCanonical     []byte
	PolicyID            string
	PolicyRevision      uint64
	CurrentSnapshotID   string
	ObservationRevision uint64
	RegisteredAt        time.Time
	UpdatedAt           time.Time
}

// ModuleSourceRefreshBasis is the exact Store-owned admission basis captured
// before source I/O. CommitModuleSourceRefresh rechecks every current field in
// BEGIN IMMEDIATE after I/O, so a stale or revoked basis writes nothing.
type ModuleSourceRefreshBasis struct {
	Source       ModuleSource
	PublisherKey *ModulePublisherKey
}

// ModuleDiscoverySnapshot is one immutable Store observation. Canonical
// Policy, Index and Snapshot bytes retain their full parent closure.
type ModuleDiscoverySnapshot struct {
	SnapshotID            string
	SourceID              string
	SourcePolicyID        string
	SourcePolicyCanonical []byte
	IndexID               string
	IndexCanonical        []byte
	Snapshot              moduleapi.ModuleDiscoverySnapshotV1
	SnapshotCanonical     []byte
	SourcePolicyRevision  uint64
	PublisherKeyID        string
	PublisherKeyRevision  uint64
	ObservationRevision   uint64
	ObservedAt            time.Time
}

// RegisterModuleSourceInput imports optional immutable key material and
// registers or CAS-updates one current SourcePolicy. ExpectedPolicyRevision is
// zero for first registration. Exact retries are idempotent even when they
// carry the predecessor revision.
type RegisterModuleSourceInput struct {
	PolicyCanonical        []byte
	PublisherKeyCanonical  []byte
	ExpectedPolicyRevision uint64
}

func (store *Store) RegisterModuleSource(ctx context.Context, input RegisterModuleSourceInput) (result ModuleSource, returnErr error) {
	if ctx == nil {
		return result, fmt.Errorf("%w: context is nil", ErrInvalidModuleSource)
	}
	policy, policyCanonical, policyID, err := moduleapi.ParseModuleSourcePolicyV1(bytes.Clone(input.PolicyCanonical))
	if err != nil {
		return result, fmt.Errorf("%w: parse SourcePolicy: %v", ErrInvalidModuleSource, err)
	}
	var suppliedKey *ModulePublisherKey
	if len(input.PublisherKeyCanonical) != 0 {
		_, canonical, keyID, parseErr := moduleapi.ParseModulePublisherKeyV1(bytes.Clone(input.PublisherKeyCanonical))
		if parseErr != nil {
			return result, fmt.Errorf("%w: parse PublisherKey: %v", ErrInvalidModuleSource, parseErr)
		}
		if !policy.SignatureRequired || policy.PublisherKeyID != keyID {
			return result, fmt.Errorf("%w: PublisherKey does not match signed SourcePolicy", ErrInvalidModuleSource)
		}
		suppliedKey = &ModulePublisherKey{PublisherKeyID: keyID, Canonical: canonical}
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("currentstore: acquire module source connection: %w", err)
	}
	defer connection.Close()
	if _, err = connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, fmt.Errorf("currentstore: begin module source registration: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()

	if policy.SignatureRequired {
		key, found, keyErr := queryModulePublisherKey(ctx, connection, policy.PublisherKeyID)
		if keyErr != nil {
			return result, keyErr
		}
		if found {
			if suppliedKey != nil && !bytes.Equal(key.Canonical, suppliedKey.Canonical) {
				return result, fmt.Errorf("%w: PublisherKey ID has different canonical bytes", ErrModuleSourceConflict)
			}
			if key.RevokedAt != nil {
				return result, ErrModulePublisherKeyRevoked
			}
		} else {
			if suppliedKey == nil {
				return result, ErrModulePublisherKeyNotFound
			}
			now := nowUnixMicro()
			if _, err := connection.ExecContext(ctx, `INSERT INTO module_publisher_keys(publisher_key_id,key_canonical,revision,imported_at) VALUES(?,?,1,?)`, suppliedKey.PublisherKeyID, suppliedKey.Canonical, now); err != nil {
				return result, fmt.Errorf("currentstore: import PublisherKey: %w", err)
			}
		}
	} else if suppliedKey != nil {
		return result, fmt.Errorf("%w: unsigned SourcePolicy cannot import a PublisherKey", ErrInvalidModuleSource)
	}

	existing, found, err := queryModuleSource(ctx, connection, policy.SourceID)
	if err != nil {
		return result, err
	}
	now := nowUnixMicro()
	if !found {
		if input.ExpectedPolicyRevision != 0 {
			return result, ErrModuleSourceStale
		}
		var publisher any
		if policy.SignatureRequired {
			publisher = policy.PublisherKeyID
		}
		_, err = connection.ExecContext(ctx, `
			INSERT INTO module_sources(source_id,source_policy_id,source_policy_canonical,source_kind,origin_digest,publisher_key_id,policy_revision,current_snapshot_id,observation_revision,registered_at,updated_at)
			VALUES(?,?,?,?,?,?,1,NULL,0,?,?)`, policy.SourceID, policyID, policyCanonical, string(policy.Kind), policy.OriginDigest, publisher, now, now)
		if err != nil {
			return result, fmt.Errorf("currentstore: insert module source: %w", err)
		}
	} else {
		if existing.Policy.Kind != policy.Kind || existing.Policy.OriginDigest != policy.OriginDigest {
			return result, fmt.Errorf("%w: SourceID cannot be rebound to another Kind or OriginDigest", ErrModuleSourceConflict)
		}
		if existing.PolicyID == policyID && bytes.Equal(existing.PolicyCanonical, policyCanonical) {
			result = existing
			if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
				return result, err
			}
			committed = true
			return detachModuleSource(result), nil
		}
		if input.ExpectedPolicyRevision != existing.PolicyRevision {
			return result, ErrModuleSourceStale
		}
		if existing.PolicyRevision >= math.MaxInt64 {
			return result, fmt.Errorf("%w: policy revision exhausted", ErrModuleDiscoveryIntegrity)
		}
		var publisher any
		if policy.SignatureRequired {
			publisher = policy.PublisherKeyID
		}
		write, writeErr := connection.ExecContext(ctx, `
			UPDATE module_sources SET source_policy_id=?,source_policy_canonical=?,publisher_key_id=?,policy_revision=policy_revision+1,current_snapshot_id=NULL,updated_at=?
			WHERE source_id=? AND policy_revision=? AND source_kind=? AND origin_digest=?`, policyID, policyCanonical, publisher, now, policy.SourceID, int64(existing.PolicyRevision), string(policy.Kind), policy.OriginDigest)
		if writeErr != nil {
			return result, fmt.Errorf("currentstore: update module source: %w", writeErr)
		}
		affected, _ := write.RowsAffected()
		if affected != 1 {
			return result, ErrModuleSourceStale
		}
	}
	result, found, err = queryModuleSource(ctx, connection, policy.SourceID)
	if err != nil || !found {
		return result, fmt.Errorf("%w: re-read registered source: %v", ErrModuleDiscoveryIntegrity, err)
	}
	if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, fmt.Errorf("currentstore: commit module source registration: %w", err)
	}
	committed = true
	return detachModuleSource(result), nil
}

// RevokeModulePublisherKey is global and irreversible. Exact retries return
// the already-revoked record without changing its timestamp or revision.
func (store *Store) RevokeModulePublisherKey(ctx context.Context, publisherKeyID string, expectedRevision uint64) (result ModulePublisherKey, returnErr error) {
	if ctx == nil || !moduleapi.ValidSHA256(publisherKeyID) || expectedRevision == 0 {
		return result, fmt.Errorf("%w: invalid PublisherKey revocation", ErrInvalidModuleSource)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, err
	}
	defer connection.Close()
	if _, err = connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	key, found, err := queryModulePublisherKey(ctx, connection, publisherKeyID)
	if err != nil {
		return result, err
	}
	if !found {
		return result, ErrModulePublisherKeyNotFound
	}
	if key.RevokedAt != nil {
		if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, err
		}
		committed = true
		return detachModulePublisherKey(key), nil
	}
	if key.Revision != expectedRevision || key.Revision >= math.MaxInt64 {
		return result, ErrModuleSourceStale
	}
	write, err := connection.ExecContext(ctx, `UPDATE module_publisher_keys SET revision=revision+1,revoked_at=? WHERE publisher_key_id=? AND revision=? AND revoked_at IS NULL`, nowUnixMicro(), publisherKeyID, int64(expectedRevision))
	if err != nil {
		return result, err
	}
	affected, _ := write.RowsAffected()
	if affected != 1 {
		return result, ErrModuleSourceStale
	}
	result, found, err = queryModulePublisherKey(ctx, connection, publisherKeyID)
	if err != nil || !found {
		return result, fmt.Errorf("%w: re-read revoked key", ErrModuleDiscoveryIntegrity)
	}
	if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, err
	}
	committed = true
	return detachModulePublisherKey(result), nil
}

func (store *Store) ReadModuleSourceRefreshBasis(ctx context.Context, sourceID string) (ModuleSourceRefreshBasis, error) {
	if ctx == nil {
		return ModuleSourceRefreshBasis{}, fmt.Errorf("%w: context is nil", ErrInvalidModuleSource)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleSourceRefreshBasis{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ModuleSourceRefreshBasis{}, err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return ModuleSourceRefreshBasis{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	source, found, err := queryModuleSource(ctx, connection, sourceID)
	if err != nil {
		return ModuleSourceRefreshBasis{}, err
	}
	if !found {
		return ModuleSourceRefreshBasis{}, ErrModuleSourceNotFound
	}
	basis := ModuleSourceRefreshBasis{Source: detachModuleSource(source)}
	if source.Policy.SignatureRequired {
		key, found, err := queryModulePublisherKey(ctx, connection, source.Policy.PublisherKeyID)
		if err != nil {
			return basis, err
		}
		if !found {
			return basis, ErrModulePublisherKeyNotFound
		}
		if key.RevokedAt != nil {
			return basis, ErrModulePublisherKeyRevoked
		}
		detached := detachModulePublisherKey(key)
		basis.PublisherKey = &detached
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return basis, err
	}
	committed = true
	return basis, nil
}

// CommitModuleSourceRefresh performs no I/O. It restores all caller bytes,
// then in BEGIN IMMEDIATE rechecks the current policy/key basis, enforces the
// global ModuleRef identity, and commits one immutable observation.
func (store *Store) CommitModuleSourceRefresh(ctx context.Context, basis ModuleSourceRefreshBasis, indexCanonical []byte) (result ModuleDiscoverySnapshot, returnErr error) {
	if ctx == nil {
		return result, fmt.Errorf("%w: context is nil", ErrInvalidModuleSource)
	}
	policy, policyCanonical, policyID, err := moduleapi.ParseModuleSourcePolicyV1(bytes.Clone(basis.Source.PolicyCanonical))
	if err != nil {
		return result, fmt.Errorf("%w: refresh policy: %v", ErrInvalidModuleSource, err)
	}
	if policyID != basis.Source.PolicyID ||
		!bytes.Equal(policyCanonical, basis.Source.PolicyCanonical) ||
		basis.Source.SourceID != policy.SourceID ||
		basis.Source.PolicyRevision == 0 {
		return result, fmt.Errorf("%w: refresh basis policy differs", ErrInvalidModuleSource)
	}
	index, indexOwned, indexID, err := moduleapi.ParseModuleDiscoveryIndexV1(bytes.Clone(indexCanonical))
	if err != nil {
		return result, fmt.Errorf("%w: discovery Index: %v", ErrInvalidModuleSource, err)
	}
	if index.SourceID != policy.SourceID {
		return result, fmt.Errorf("%w: Index source differs from policy", ErrInvalidModuleSource)
	}
	snapshot, snapshotCanonical, snapshotID, err := moduleapi.NewModuleDiscoverySnapshotV1(policyCanonical, policyID, indexOwned, indexID)
	if err != nil {
		return result, fmt.Errorf("%w: build Snapshot: %v", ErrInvalidModuleSource, err)
	}
	var basisKeyID string
	var basisKeyRevision uint64
	if policy.SignatureRequired {
		if basis.PublisherKey == nil || basis.PublisherKey.PublisherKeyID != policy.PublisherKeyID || basis.PublisherKey.Revision == 0 || basis.PublisherKey.RevokedAt != nil {
			return result, fmt.Errorf("%w: signed refresh lacks live exact PublisherKey basis", ErrInvalidModuleSource)
		}
		_, keyCanonical, keyID, keyErr := moduleapi.ParseModulePublisherKeyV1(basis.PublisherKey.Canonical)
		if keyErr != nil || keyID != basis.PublisherKey.PublisherKeyID || !bytes.Equal(keyCanonical, basis.PublisherKey.Canonical) {
			return result, fmt.Errorf("%w: invalid PublisherKey basis", ErrInvalidModuleSource)
		}
		basisKeyID, basisKeyRevision = keyID, basis.PublisherKey.Revision
	} else if basis.PublisherKey != nil {
		return result, fmt.Errorf("%w: unsigned refresh has PublisherKey basis", ErrInvalidModuleSource)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, err
	}
	defer connection.Close()
	if _, err = connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	current, found, err := queryModuleSource(ctx, connection, policy.SourceID)
	if err != nil {
		return result, err
	}
	if !found {
		return result, ErrModuleSourceNotFound
	}
	if current.PolicyRevision != basis.Source.PolicyRevision || current.PolicyID != policyID || !bytes.Equal(current.PolicyCanonical, policyCanonical) || current.Policy.Kind != policy.Kind || current.Policy.OriginDigest != policy.OriginDigest {
		return result, ErrModuleSourceStale
	}
	var publisher any
	var publisherRevision any
	if policy.SignatureRequired {
		key, found, keyErr := queryModulePublisherKey(ctx, connection, basisKeyID)
		if keyErr != nil {
			return result, keyErr
		}
		if !found {
			return result, ErrModulePublisherKeyNotFound
		}
		if key.RevokedAt != nil {
			return result, ErrModulePublisherKeyRevoked
		}
		if key.Revision != basisKeyRevision || !bytes.Equal(key.Canonical, basis.PublisherKey.Canonical) {
			return result, ErrModuleSourceStale
		}
		publisher, publisherRevision = key.PublisherKeyID, int64(key.Revision)
	}
	if existing, found, exactErr := queryModuleDiscoverySnapshot(ctx, connection, snapshotID); exactErr != nil {
		return result, exactErr
	} else if found {
		if existing.SourceID != policy.SourceID || existing.SourcePolicyID != policyID || existing.IndexID != indexID || !bytes.Equal(existing.IndexCanonical, indexOwned) || !bytes.Equal(existing.SnapshotCanonical, snapshotCanonical) {
			return result, fmt.Errorf("%w: SnapshotID collision", ErrModuleDiscoveryIntegrity)
		}
		if current.CurrentSnapshotID != snapshotID ||
			current.ObservationRevision != existing.ObservationRevision {
			return result, fmt.Errorf(
				"%w: an earlier Snapshot cannot replace the current observation head",
				ErrModuleSourceConflict,
			)
		}
		if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, err
		}
		committed = true
		return detachModuleDiscoverySnapshot(existing), nil
	}
	// A new immutable observation may advance only the exact head captured
	// before source I/O. This is the post-I/O linearization fence: another
	// refresh that won the Store transaction makes this basis stale instead of
	// allowing an older, slower observation to overwrite the current head.
	if current.ObservationRevision != basis.Source.ObservationRevision ||
		current.CurrentSnapshotID != basis.Source.CurrentSnapshotID {
		return result, ErrModuleSourceStale
	}
	if err := requireDiscoveryEntriesGloballyCompatible(ctx, connection, snapshot.Entries); err != nil {
		return result, err
	}
	if current.ObservationRevision >= math.MaxInt64 {
		return result, fmt.Errorf("%w: observation revision exhausted", ErrModuleDiscoveryIntegrity)
	}
	observationRevision := current.ObservationRevision + 1
	observedAt := nowUnixMicro()
	_, err = connection.ExecContext(ctx, `
		INSERT INTO module_discovery_snapshots(snapshot_id,source_id,source_policy_id,source_policy_canonical,index_id,index_canonical,snapshot_canonical,source_policy_revision,publisher_key_id,publisher_key_revision,observation_revision,observed_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, snapshotID, policy.SourceID, policyID, policyCanonical, indexID, indexOwned, snapshotCanonical, int64(current.PolicyRevision), publisher, publisherRevision, int64(observationRevision), observedAt)
	if err != nil {
		return result, fmt.Errorf("currentstore: insert discovery Snapshot: %w", err)
	}
	for ordinal, entry := range snapshot.Entries {
		write, writeErr := connection.ExecContext(ctx, `INSERT INTO module_discovery_module_refs(module_id,exact_version,artifact_digest,first_source_id,first_snapshot_id,first_observed_at) VALUES(?,?,?,?,?,?) ON CONFLICT(module_id,exact_version) DO NOTHING`, entry.Module.ID, entry.Module.Version, entry.ArtifactDigest, policy.SourceID, snapshotID, observedAt)
		if writeErr != nil {
			return result, fmt.Errorf("currentstore: insert discovery ModuleRef: %w", writeErr)
		}
		_ = write
		var signature any
		if entry.SignatureID != "" {
			signature = entry.SignatureID
		}
		if _, writeErr = connection.ExecContext(ctx, `INSERT INTO module_discovery_entries(snapshot_id,entry_ordinal,module_id,exact_version,artifact_digest,artifact_size_bytes,package_path,signature_id) VALUES(?,?,?,?,?,?,?,?)`, snapshotID, ordinal, entry.Module.ID, entry.Module.Version, entry.ArtifactDigest, int64(entry.ArtifactSizeBytes), entry.PackagePath, signature); writeErr != nil {
			return result, fmt.Errorf("currentstore: insert discovery entry: %w", writeErr)
		}
	}
	write, err := connection.ExecContext(ctx, `UPDATE module_sources SET current_snapshot_id=?,observation_revision=?,updated_at=? WHERE source_id=? AND policy_revision=? AND source_policy_id=?`, snapshotID, int64(observationRevision), observedAt, policy.SourceID, int64(current.PolicyRevision), policyID)
	if err != nil {
		return result, err
	}
	affected, _ := write.RowsAffected()
	if affected != 1 {
		return result, ErrModuleSourceStale
	}
	result, found, err = queryModuleDiscoverySnapshot(ctx, connection, snapshotID)
	if err != nil || !found {
		return result, fmt.Errorf("%w: re-read Snapshot: %v", ErrModuleDiscoveryIntegrity, err)
	}
	if err := VerifyModuleDiscoverySemanticClosureV1(ctx, connection); err != nil {
		return result, fmt.Errorf(
			"%w: pre-commit semantic closure: %v",
			ErrModuleDiscoveryIntegrity,
			err,
		)
	}
	if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, err
	}
	committed = true
	return detachModuleDiscoverySnapshot(result), nil
}

func (store *Store) GetModuleSource(ctx context.Context, sourceID string) (ModuleSource, error) {
	if ctx == nil {
		return ModuleSource{}, fmt.Errorf("%w: context is nil", ErrInvalidModuleSource)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleSource{}, err
	}
	defer unlock()
	source, found, err := queryModuleSource(ctx, store.db, sourceID)
	if err != nil {
		return ModuleSource{}, err
	}
	if !found {
		return ModuleSource{}, ErrModuleSourceNotFound
	}
	return detachModuleSource(source), nil
}
func (store *Store) ListModuleSources(ctx context.Context) ([]ModuleSource, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidModuleSource)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()
	rows, err := store.db.QueryContext(ctx, `SELECT source_id FROM module_sources ORDER BY source_id COLLATE BINARY`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []ModuleSource
	for _, id := range ids {
		source, found, err := queryModuleSource(ctx, store.db, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrModuleDiscoveryIntegrity
		}
		out = append(out, detachModuleSource(source))
	}
	return out, nil
}
func (store *Store) GetModuleDiscoverySnapshot(ctx context.Context, snapshotID string) (ModuleDiscoverySnapshot, error) {
	if ctx == nil || !moduleapi.ValidSHA256(snapshotID) {
		return ModuleDiscoverySnapshot{}, fmt.Errorf("%w: invalid SnapshotID", ErrInvalidModuleSource)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleDiscoverySnapshot{}, err
	}
	defer unlock()
	snapshot, found, err := queryModuleDiscoverySnapshot(ctx, store.db, snapshotID)
	if err != nil {
		return ModuleDiscoverySnapshot{}, err
	}
	if !found {
		return ModuleDiscoverySnapshot{}, ErrModuleDiscoverySnapshotNotFound
	}
	return detachModuleDiscoverySnapshot(snapshot), nil
}
func (store *Store) ListModuleDiscoverySnapshots(ctx context.Context, sourceID string) ([]ModuleDiscoverySnapshot, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidModuleSource)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()
	rows, err := store.db.QueryContext(ctx, `SELECT snapshot_id FROM module_discovery_snapshots WHERE source_id=? ORDER BY observation_revision,snapshot_id COLLATE BINARY`, sourceID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]ModuleDiscoverySnapshot, 0, len(ids))
	for _, id := range ids {
		snapshot, found, err := queryModuleDiscoverySnapshot(ctx, store.db, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrModuleDiscoveryIntegrity
		}
		out = append(out, detachModuleDiscoverySnapshot(snapshot))
	}
	return out, nil
}

type moduleDiscoveryQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryModulePublisherKey(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (ModulePublisherKey, bool, error) {
	var key ModulePublisherKey
	var revision, imported int64
	var revoked sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT publisher_key_id,key_canonical,revision,imported_at,revoked_at FROM module_publisher_keys WHERE publisher_key_id=?`, id).Scan(&key.PublisherKeyID, &key.Canonical, &revision, &imported, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return key, false, nil
	}
	if err != nil {
		return key, false, err
	}
	_, canonical, derivedID, parseErr := moduleapi.ParseModulePublisherKeyV1(
		bytes.Clone(key.Canonical),
	)
	if parseErr != nil || derivedID != key.PublisherKeyID ||
		!bytes.Equal(canonical, key.Canonical) {
		return key, false, fmt.Errorf(
			"%w: invalid PublisherKey row %q",
			ErrModuleDiscoveryIntegrity,
			id,
		)
	}
	key.Revision = uint64(revision)
	key.ImportedAt, err = timeFromUnixMicro(imported)
	if err != nil {
		return key, false, err
	}
	if revoked.Valid {
		value, parseErr := timeFromUnixMicro(revoked.Int64)
		if parseErr != nil {
			return key, false, parseErr
		}
		key.RevokedAt = &value
	}
	return key, true, nil
}

func queryModuleSource(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (ModuleSource, bool, error) {
	var source ModuleSource
	var canonical []byte
	var kind, origin string
	var publisher sql.NullString
	var policyRevision, observationRevision, registered, updated int64
	var current sql.NullString
	err := q.QueryRowContext(ctx, `SELECT source_id,source_policy_id,source_policy_canonical,source_kind,origin_digest,publisher_key_id,policy_revision,current_snapshot_id,observation_revision,registered_at,updated_at FROM module_sources WHERE source_id=?`, id).Scan(&source.SourceID, &source.PolicyID, &canonical, &kind, &origin, &publisher, &policyRevision, &current, &observationRevision, &registered, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return source, false, nil
	}
	if err != nil {
		return source, false, err
	}
	policy, owned, policyID, err := moduleapi.ParseModuleSourcePolicyV1(canonical)
	if err != nil || policyID != source.PolicyID || string(policy.Kind) != kind || policy.OriginDigest != origin || (policy.SignatureRequired && (!publisher.Valid || publisher.String != policy.PublisherKeyID)) || (!policy.SignatureRequired && publisher.Valid) {
		return source, false, fmt.Errorf("%w: invalid SourcePolicy row %q", ErrModuleDiscoveryIntegrity, id)
	}
	source.Policy = policy
	source.PolicyCanonical = owned
	source.PolicyRevision = uint64(policyRevision)
	source.ObservationRevision = uint64(observationRevision)
	if current.Valid {
		source.CurrentSnapshotID = current.String
	}
	source.RegisteredAt, err = timeFromUnixMicro(registered)
	if err != nil {
		return source, false, err
	}
	source.UpdatedAt, err = timeFromUnixMicro(updated)
	if err != nil {
		return source, false, err
	}
	return source, true, nil
}

func queryModuleDiscoverySnapshot(ctx context.Context, q moduleDiscoveryQueryer, id string) (ModuleDiscoverySnapshot, bool, error) {
	var record ModuleDiscoverySnapshot
	var policyCanonical, indexCanonical, snapshotCanonical []byte
	var policyRevision, observationRevision, observed int64
	var publisher sql.NullString
	var publisherRevision sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT snapshot_id,source_id,source_policy_id,source_policy_canonical,index_id,index_canonical,snapshot_canonical,source_policy_revision,publisher_key_id,publisher_key_revision,observation_revision,observed_at FROM module_discovery_snapshots WHERE snapshot_id=?`, id).Scan(&record.SnapshotID, &record.SourceID, &record.SourcePolicyID, &policyCanonical, &record.IndexID, &indexCanonical, &snapshotCanonical, &policyRevision, &publisher, &publisherRevision, &observationRevision, &observed)
	if errors.Is(err, sql.ErrNoRows) {
		return record, false, nil
	}
	if err != nil {
		return record, false, err
	}
	snapshot, restoreErr := moduleapi.RestoreModuleDiscoverySnapshotV1(snapshotCanonical, record.SnapshotID, policyCanonical, record.SourcePolicyID, indexCanonical, record.IndexID)
	if restoreErr != nil {
		return record, false, fmt.Errorf("%w: Snapshot %s: %v", ErrModuleDiscoveryIntegrity, id, restoreErr)
	}
	record.SourcePolicyCanonical = bytes.Clone(policyCanonical)
	record.IndexCanonical = bytes.Clone(indexCanonical)
	record.SnapshotCanonical = bytes.Clone(snapshotCanonical)
	record.Snapshot = snapshot
	record.SourcePolicyRevision = uint64(policyRevision)
	record.ObservationRevision = uint64(observationRevision)
	if publisher.Valid {
		record.PublisherKeyID = publisher.String
		record.PublisherKeyRevision = uint64(publisherRevision.Int64)
	}
	record.ObservedAt, err = timeFromUnixMicro(observed)
	if err != nil {
		return record, false, err
	}
	return record, true, nil
}

func requireDiscoveryEntriesGloballyCompatible(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, entries []moduleapi.ModuleDiscoveryEntryV1) error {
	for _, entry := range entries {
		var digest string
		err := q.QueryRowContext(ctx, `SELECT artifact_digest FROM module_discovery_module_refs WHERE module_id=? AND exact_version=?`, entry.Module.ID, entry.Module.Version).Scan(&digest)
		if err == nil && digest != entry.ArtifactDigest {
			return fmt.Errorf("%w: %s@%s has conflicting observed artifact", ErrModuleSourceConflict, entry.Module.ID, entry.Module.Version)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		err = q.QueryRowContext(ctx, `SELECT artifact_digest FROM module_installations WHERE module_id=? AND exact_version=?`, entry.Module.ID, entry.Module.Version).Scan(&digest)
		if err == nil && digest != entry.ArtifactDigest {
			return fmt.Errorf("%w: %s@%s conflicts with Installation", ErrModuleSourceConflict, entry.Module.ID, entry.Module.Version)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		err = q.QueryRowContext(ctx, `SELECT artifact_digest FROM learning_proposals WHERE target_id=? AND target_version=? AND version_id IS NOT NULL`, entry.Module.ID, entry.Module.Version).Scan(&digest)
		if err == nil && digest != entry.ArtifactDigest {
			return fmt.Errorf("%w: %s@%s conflicts with materialized Learning Version", ErrModuleSourceConflict, entry.Module.ID, entry.Module.Version)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func requireModuleRefCompatibleWithDiscovery(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ref moduleapi.Ref, digest string) error {
	var observed string
	err := q.QueryRowContext(ctx, `SELECT artifact_digest FROM module_discovery_module_refs WHERE module_id=? AND exact_version=?`, ref.ID, ref.Version).Scan(&observed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: query discovery ModuleRef: %v", ErrModuleDiscoveryIntegrity, err)
	}
	if observed != digest {
		return fmt.Errorf("%w: %s@%s differs from discovered artifact", ErrModuleSourceConflict, ref.ID, ref.Version)
	}
	return nil
}

func detachModulePublisherKey(value ModulePublisherKey) ModulePublisherKey {
	value.Canonical = bytes.Clone(value.Canonical)
	if value.RevokedAt != nil {
		copy := *value.RevokedAt
		value.RevokedAt = &copy
	}
	return value
}
func detachModuleSource(value ModuleSource) ModuleSource {
	value.PolicyCanonical = bytes.Clone(value.PolicyCanonical)
	value.Policy.AllowedModuleIDPrefixes = append([]string(nil), value.Policy.AllowedModuleIDPrefixes...)
	return value
}
func detachModuleDiscoverySnapshot(value ModuleDiscoverySnapshot) ModuleDiscoverySnapshot {
	value.SourcePolicyCanonical = bytes.Clone(value.SourcePolicyCanonical)
	value.IndexCanonical = bytes.Clone(value.IndexCanonical)
	value.SnapshotCanonical = bytes.Clone(value.SnapshotCanonical)
	value.Snapshot.Entries = append([]moduleapi.ModuleDiscoveryEntryV1(nil), value.Snapshot.Entries...)
	return value
}
