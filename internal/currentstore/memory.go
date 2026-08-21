package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const agentMemoryJSONMediaType = "application/json"

var (
	// ErrInvalidAgentMemory identifies an invalid owner, revision, expected
	// head, or non-canonical memory-snapshot/v1 body.
	ErrInvalidAgentMemory = errors.New("currentstore: invalid Agent Memory")

	// ErrAgentMemoryNotFound identifies an Agent that has no genesis, or an
	// exact revision that does not exist.
	ErrAgentMemoryNotFound = errors.New("currentstore: Agent Memory not found")

	// ErrAgentMemoryConflict identifies a conflicting genesis, stale CAS, or
	// reuse of a source Attempt for different snapshot bytes.
	ErrAgentMemoryConflict = errors.New("currentstore: Agent Memory conflict")

	// ErrAgentMemoryIntegrity identifies a stored row whose immutable content,
	// owner, revision, parent, or source Attempt closure is broken.
	ErrAgentMemoryIntegrity = errors.New("currentstore: Agent Memory integrity violation")
)

// AgentMemoryRevisionRecord is the immutable, verified Current Store view of
// one memory revision. SnapshotRef.Digest is the ContentRecord digest; there
// is no second snapshot digest. CanonicalBytes is detached from Store storage.
type AgentMemoryRevisionRecord struct {
	SnapshotRef     moduleapi.MemorySnapshotRefV1
	SourceAttemptID string
	Snapshot        moduleapi.AgentMemorySnapshotV1
	CanonicalBytes  []byte
	CreatedAt       time.Time
}

// PutAgentMemoryGenesisResult reports whether revision 1 was inserted. An
// exact retry returns the original verified record with Applied=false.
type PutAgentMemoryGenesisResult struct {
	Record  AgentMemoryRevisionRecord
	Applied bool
}

// PutAgentMemoryGenesis installs the explicit revision-1 snapshot in one
// BEGIN IMMEDIATE transaction. This is a narrow Core API: module hosts and MCP
// providers are never given a Store handle.
func (store *Store) PutAgentMemoryGenesis(
	ctx context.Context,
	snapshotCanonical []byte,
) (PutAgentMemoryGenesisResult, error) {
	if ctx == nil {
		return PutAgentMemoryGenesisResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidAgentMemory,
		)
	}
	canonical := bytes.Clone(snapshotCanonical)
	snapshot, digest, err := prepareAgentMemorySnapshot(canonical)
	if err != nil {
		return PutAgentMemoryGenesisResult{}, err
	}
	if snapshot.Revision != 1 || snapshot.PreviousSnapshotDigest != "" ||
		snapshot.SourceAttemptID != "" {
		return PutAgentMemoryGenesisResult{}, fmt.Errorf(
			"%w: genesis must be revision 1 without parent or source Attempt",
			ErrInvalidAgentMemory,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return PutAgentMemoryGenesisResult{}, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return PutAgentMemoryGenesisResult{}, fmt.Errorf(
			"currentstore: acquire Agent Memory connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return PutAgentMemoryGenesisResult{}, fmt.Errorf(
			"currentstore: begin PutAgentMemoryGenesis: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	existing, err := queryAgentMemoryRevision(
		ctx,
		connection,
		snapshot.TenantID,
		snapshot.AgentID,
		1,
	)
	if err == nil {
		if existing.SnapshotRef.Digest != digest ||
			!bytes.Equal(existing.CanonicalBytes, canonical) {
			return PutAgentMemoryGenesisResult{}, fmt.Errorf(
				"%w: Agent already has a different genesis",
				ErrAgentMemoryConflict,
			)
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return PutAgentMemoryGenesisResult{}, fmt.Errorf(
				"currentstore: commit idempotent Agent Memory genesis: %w",
				err,
			)
		}
		committed = true
		return PutAgentMemoryGenesisResult{
			Record:  cloneAgentMemoryRevisionRecord(existing),
			Applied: false,
		}, nil
	}
	if !errors.Is(err, ErrAgentMemoryNotFound) {
		return PutAgentMemoryGenesisResult{}, err
	}
	if _, err := queryCurrentAgentMemoryRevision(
		ctx,
		connection,
		snapshot.TenantID,
		snapshot.AgentID,
	); err == nil {
		return PutAgentMemoryGenesisResult{}, fmt.Errorf(
			"%w: Agent has revisions but no genesis",
			ErrAgentMemoryIntegrity,
		)
	} else if !errors.Is(err, ErrAgentMemoryNotFound) {
		return PutAgentMemoryGenesisResult{}, err
	}

	createdAtMicros := nowUnixMicro()
	if err := putAgentMemoryContent(
		ctx,
		connection,
		digest,
		canonical,
		createdAtMicros,
	); err != nil {
		return PutAgentMemoryGenesisResult{}, err
	}
	result, err := connection.ExecContext(ctx, `
		INSERT INTO agent_memory_revisions(
			tenant_id,
			agent_id,
			revision,
			snapshot_ref,
			source_attempt_id,
			created_at
		) VALUES(?, ?, 1, ?, NULL, ?)
	`, snapshot.TenantID, snapshot.AgentID, digest, createdAtMicros)
	if err != nil {
		return PutAgentMemoryGenesisResult{}, fmt.Errorf(
			"currentstore: insert Agent Memory genesis: %w",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return PutAgentMemoryGenesisResult{}, fmt.Errorf(
			"%w: genesis insert affected %d rows: %v",
			ErrAgentMemoryIntegrity,
			affected,
			err,
		)
	}
	record, err := queryAgentMemoryRevision(
		ctx,
		connection,
		snapshot.TenantID,
		snapshot.AgentID,
		1,
	)
	if err != nil {
		return PutAgentMemoryGenesisResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return PutAgentMemoryGenesisResult{}, fmt.Errorf(
			"currentstore: commit Agent Memory genesis: %w",
			err,
		)
	}
	committed = true
	return PutAgentMemoryGenesisResult{
		Record:  cloneAgentMemoryRevisionRecord(record),
		Applied: true,
	}, nil
}

// GetCurrentAgentMemory returns the exact head selected by MAX(revision).
func (store *Store) GetCurrentAgentMemory(
	ctx context.Context,
	tenantID string,
	agentID string,
) (AgentMemoryRevisionRecord, error) {
	if ctx == nil {
		return AgentMemoryRevisionRecord{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidAgentMemory,
		)
	}
	if err := validateAgentMemoryOwner(tenantID, agentID); err != nil {
		return AgentMemoryRevisionRecord{}, err
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return AgentMemoryRevisionRecord{}, err
	}
	defer unlock()
	record, err := queryCurrentAgentMemoryRevision(
		ctx,
		store.db,
		tenantID,
		agentID,
	)
	if err != nil {
		return AgentMemoryRevisionRecord{}, err
	}
	return cloneAgentMemoryRevisionRecord(record), nil
}

// GetAgentMemoryRevision returns one exact immutable revision.
func (store *Store) GetAgentMemoryRevision(
	ctx context.Context,
	tenantID string,
	agentID string,
	revision uint64,
) (AgentMemoryRevisionRecord, error) {
	if ctx == nil {
		return AgentMemoryRevisionRecord{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidAgentMemory,
		)
	}
	if err := validateAgentMemoryOwner(tenantID, agentID); err != nil {
		return AgentMemoryRevisionRecord{}, err
	}
	if revision == 0 || revision > moduleapi.MaxMemorySafeIntegerV1 ||
		revision > math.MaxInt64 {
		return AgentMemoryRevisionRecord{}, fmt.Errorf(
			"%w: revision is outside the supported range",
			ErrInvalidAgentMemory,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return AgentMemoryRevisionRecord{}, err
	}
	defer unlock()
	record, err := queryAgentMemoryRevision(
		ctx,
		store.db,
		tenantID,
		agentID,
		revision,
	)
	if err != nil {
		return AgentMemoryRevisionRecord{}, err
	}
	return cloneAgentMemoryRevisionRecord(record), nil
}

type agentMemoryQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type agentMemoryRow struct {
	tenantID        string
	agentID         string
	revision        uint64
	snapshotRef     string
	sourceAttemptID string
	createdAt       time.Time
}

func queryCurrentAgentMemoryRevision(
	ctx context.Context,
	queryer agentMemoryQueryer,
	tenantID string,
	agentID string,
) (AgentMemoryRevisionRecord, error) {
	row, err := scanAgentMemoryRow(queryer.QueryRowContext(ctx, `
		SELECT
			tenant_id,
			agent_id,
			revision,
			snapshot_ref,
			source_attempt_id,
			created_at
		FROM agent_memory_revisions
		WHERE tenant_id=? AND agent_id=?
		  AND revision=(
			SELECT MAX(revision)
			FROM agent_memory_revisions
			WHERE tenant_id=? AND agent_id=?
		  )
	`, tenantID, agentID, tenantID, agentID), tenantID, agentID, 0)
	if err != nil {
		return AgentMemoryRevisionRecord{}, err
	}
	return validateAgentMemoryRow(ctx, queryer, row, true)
}

func queryAgentMemoryRevision(
	ctx context.Context,
	queryer agentMemoryQueryer,
	tenantID string,
	agentID string,
	revision uint64,
) (AgentMemoryRevisionRecord, error) {
	row, err := scanAgentMemoryRow(queryer.QueryRowContext(ctx, `
		SELECT
			tenant_id,
			agent_id,
			revision,
			snapshot_ref,
			source_attempt_id,
			created_at
		FROM agent_memory_revisions
		WHERE tenant_id=? AND agent_id=? AND revision=?
	`, tenantID, agentID, revision), tenantID, agentID, revision)
	if err != nil {
		return AgentMemoryRevisionRecord{}, err
	}
	return validateAgentMemoryRow(ctx, queryer, row, true)
}

func queryAgentMemoryRevisionBySource(
	ctx context.Context,
	queryer agentMemoryQueryer,
	sourceAttemptID string,
) (AgentMemoryRevisionRecord, error) {
	row, err := scanAgentMemoryRow(queryer.QueryRowContext(ctx, `
		SELECT
			tenant_id,
			agent_id,
			revision,
			snapshot_ref,
			source_attempt_id,
			created_at
		FROM agent_memory_revisions
		WHERE source_attempt_id=?
	`, sourceAttemptID), "", "", 0)
	if err != nil {
		return AgentMemoryRevisionRecord{}, err
	}
	return validateAgentMemoryRow(ctx, queryer, row, true)
}

func scanAgentMemoryRow(
	row *sql.Row,
	wantedTenantID string,
	wantedAgentID string,
	wantedRevision uint64,
) (agentMemoryRow, error) {
	var (
		storedRevision  int64
		sourceAttempt   sql.NullString
		createdAtMicros int64
		stored          agentMemoryRow
	)
	err := row.Scan(
		&stored.tenantID,
		&stored.agentID,
		&storedRevision,
		&stored.snapshotRef,
		&sourceAttempt,
		&createdAtMicros,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return agentMemoryRow{}, fmt.Errorf(
			"%w: tenant=%q agent=%q revision=%d",
			ErrAgentMemoryNotFound,
			wantedTenantID,
			wantedAgentID,
			wantedRevision,
		)
	}
	if err != nil {
		return agentMemoryRow{}, fmt.Errorf(
			"currentstore: read Agent Memory revision: %w",
			err,
		)
	}
	if storedRevision < 1 || uint64(storedRevision) > moduleapi.MaxMemorySafeIntegerV1 {
		return agentMemoryRow{}, fmt.Errorf(
			"%w: stored revision is outside the supported range",
			ErrAgentMemoryIntegrity,
		)
	}
	stored.revision = uint64(storedRevision)
	stored.sourceAttemptID = sourceAttempt.String
	stored.createdAt, err = timeFromUnixMicro(createdAtMicros)
	if err != nil {
		return agentMemoryRow{}, fmt.Errorf(
			"%w: stored revision has invalid created_at",
			ErrAgentMemoryIntegrity,
		)
	}
	return stored, nil
}

func validateAgentMemoryRow(
	ctx context.Context,
	queryer agentMemoryQueryer,
	row agentMemoryRow,
	checkParent bool,
) (AgentMemoryRevisionRecord, error) {
	content, err := queryContent(ctx, queryer, row.snapshotRef)
	if err != nil {
		return AgentMemoryRevisionRecord{}, fmt.Errorf(
			"%w: snapshot content %q is unavailable or invalid: %v",
			ErrAgentMemoryIntegrity,
			row.snapshotRef,
			err,
		)
	}
	if content.Kind != ContentMemorySnapshot ||
		content.MediaType != agentMemoryJSONMediaType {
		return AgentMemoryRevisionRecord{}, fmt.Errorf(
			"%w: snapshot content has kind/media type %s/%s",
			ErrAgentMemoryIntegrity,
			content.Kind,
			content.MediaType,
		)
	}
	snapshot, err := moduleapi.RestoreAgentMemorySnapshotV1(
		content.CanonicalBytes,
	)
	if err != nil {
		return AgentMemoryRevisionRecord{}, fmt.Errorf(
			"%w: restore memory-snapshot/v1: %v",
			ErrAgentMemoryIntegrity,
			err,
		)
	}
	if snapshot.TenantID != row.tenantID || snapshot.AgentID != row.agentID ||
		snapshot.Revision != row.revision ||
		snapshot.SourceAttemptID != row.sourceAttemptID {
		return AgentMemoryRevisionRecord{}, fmt.Errorf(
			"%w: row owner/revision/source differs from snapshot",
			ErrAgentMemoryIntegrity,
		)
	}
	if row.revision == 1 {
		if row.sourceAttemptID != "" || snapshot.PreviousSnapshotDigest != "" {
			return AgentMemoryRevisionRecord{}, fmt.Errorf(
				"%w: genesis contains a parent or source Attempt",
				ErrAgentMemoryIntegrity,
			)
		}
	} else {
		if row.sourceAttemptID == "" {
			return AgentMemoryRevisionRecord{}, fmt.Errorf(
				"%w: non-genesis revision has no source Attempt",
				ErrAgentMemoryIntegrity,
			)
		}
		if err := validateAgentMemorySourceAttempt(
			ctx,
			queryer,
			row.tenantID,
			row.agentID,
			row.sourceAttemptID,
			ErrAgentMemoryIntegrity,
		); err != nil {
			return AgentMemoryRevisionRecord{}, err
		}
		if checkParent {
			parentRow, err := scanAgentMemoryRow(queryer.QueryRowContext(ctx, `
				SELECT
					tenant_id,
					agent_id,
					revision,
					snapshot_ref,
					source_attempt_id,
					created_at
				FROM agent_memory_revisions
				WHERE tenant_id=? AND agent_id=? AND revision=?
			`, row.tenantID, row.agentID, row.revision-1), row.tenantID, row.agentID, row.revision-1)
			if err != nil {
				return AgentMemoryRevisionRecord{}, fmt.Errorf(
					"%w: revision %d has no exact parent",
					ErrAgentMemoryIntegrity,
					row.revision,
				)
			}
			parent, err := validateAgentMemoryRow(
				ctx,
				queryer,
				parentRow,
				false,
			)
			if err != nil {
				return AgentMemoryRevisionRecord{}, err
			}
			if snapshot.PreviousSnapshotDigest != parent.SnapshotRef.Digest {
				return AgentMemoryRevisionRecord{}, fmt.Errorf(
					"%w: revision %d points at a different parent",
					ErrAgentMemoryIntegrity,
					row.revision,
				)
			}
		}
	}
	return AgentMemoryRevisionRecord{
		SnapshotRef: moduleapi.MemorySnapshotRefV1{
			TenantID: row.tenantID,
			AgentID:  row.agentID,
			Revision: row.revision,
			Digest:   row.snapshotRef,
		},
		SourceAttemptID: row.sourceAttemptID,
		Snapshot:        snapshot,
		CanonicalBytes:  bytes.Clone(content.CanonicalBytes),
		CreatedAt:       row.createdAt,
	}, nil
}

func validateAgentMemorySourceAttempt(
	ctx context.Context,
	queryer agentMemoryQueryer,
	tenantID string,
	agentID string,
	sourceAttemptID string,
	classification error,
) error {
	var (
		state         string
		attemptTenant string
		attemptAgent  string
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT a.state, r.tenant_id, member.agent_id
		FROM model_dispatch_attempts AS a
		JOIN runs AS r ON r.run_id=a.run_id
		JOIN member_execution_snapshots AS member
		  ON member.run_id=a.run_id AND member.member_id=a.member_id
		WHERE a.attempt_id=?
	`, sourceAttemptID).Scan(&state, &attemptTenant, &attemptAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf(
			"%w: source Attempt %q does not exist",
			classification,
			sourceAttemptID,
		)
	}
	if err != nil {
		return fmt.Errorf(
			"currentstore: read Agent Memory source Attempt: %w",
			err,
		)
	}
	if state != "SUCCEEDED" || attemptTenant != tenantID ||
		attemptAgent != agentID {
		return fmt.Errorf(
			"%w: source Attempt is not a successful Attempt for the exact owner",
			classification,
		)
	}
	return nil
}

func prepareAgentMemorySnapshot(
	canonical []byte,
) (moduleapi.AgentMemorySnapshotV1, string, error) {
	snapshot, err := moduleapi.RestoreAgentMemorySnapshotV1(canonical)
	if err != nil {
		return moduleapi.AgentMemorySnapshotV1{}, "", fmt.Errorf(
			"%w: restore memory-snapshot/v1: %v",
			ErrInvalidAgentMemory,
			err,
		)
	}
	digest, err := ComputeContentDigest(
		ContentMemorySnapshot,
		agentMemoryJSONMediaType,
		canonical,
	)
	if err != nil {
		return moduleapi.AgentMemorySnapshotV1{}, "", fmt.Errorf(
			"%w: compute snapshot ContentRecord digest: %v",
			ErrInvalidAgentMemory,
			err,
		)
	}
	return snapshot, digest, nil
}

func putAgentMemoryContent(
	ctx context.Context,
	connection *sql.Conn,
	digest string,
	canonical []byte,
	createdAtMicros int64,
) error {
	result, err := connection.ExecContext(ctx, `
		INSERT INTO content_records(
			content_digest,
			kind,
			media_type,
			canonical_bytes,
			size_bytes,
			created_at
		) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(content_digest) DO NOTHING
	`,
		digest,
		string(ContentMemorySnapshot),
		agentMemoryJSONMediaType,
		canonical,
		len(canonical),
		createdAtMicros,
	)
	if err != nil {
		return fmt.Errorf(
			"currentstore: insert Agent Memory content %s: %w",
			digest,
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect Agent Memory content insert: %w",
			err,
		)
	}
	switch affected {
	case 1:
		return nil
	case 0:
		record, err := queryContent(ctx, connection, digest)
		if err != nil {
			return fmt.Errorf(
				"%w: existing snapshot content is invalid: %v",
				ErrAgentMemoryIntegrity,
				err,
			)
		}
		if record.Kind != ContentMemorySnapshot ||
			record.MediaType != agentMemoryJSONMediaType ||
			!bytes.Equal(record.CanonicalBytes, canonical) {
			return fmt.Errorf(
				"%w: existing snapshot content differs",
				ErrAgentMemoryIntegrity,
			)
		}
		return nil
	default:
		return fmt.Errorf(
			"%w: snapshot content insert affected %d rows",
			ErrAgentMemoryIntegrity,
			affected,
		)
	}
}

func validateAgentMemoryOwner(tenantID string, agentID string) error {
	for name, value := range map[string]string{
		"tenant_id": tenantID,
		"agent_id":  agentID,
	} {
		if value == "" || value == "*" || len(value) > 256 ||
			!utf8.ValidString(value) || value != strings.TrimSpace(value) {
			return fmt.Errorf(
				"%w: %s must be an exact, trimmed UTF-8 ID",
				ErrInvalidAgentMemory,
				name,
			)
		}
		for _, character := range value {
			if unicode.IsControl(character) {
				return fmt.Errorf(
					"%w: %s contains a control character",
					ErrInvalidAgentMemory,
					name,
				)
			}
		}
	}
	return nil
}

func cloneAgentMemoryRevisionRecord(
	record AgentMemoryRevisionRecord,
) AgentMemoryRevisionRecord {
	record.CanonicalBytes = bytes.Clone(record.CanonicalBytes)
	return record
}
