package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
	moderncsqlite "modernc.org/sqlite"
)

type sqliteBackupConnection interface {
	NewBackup(string) (*moderncsqlite.Backup, error)
}

// semanticQueryer is the read-only surface shared by every semantic verifier.
// inspectSnapshot passes one explicitly begun *sql.Conn so all closure checks
// observe one coherent SQLite snapshot. Tests may still pass *sql.DB directly
// to the focused helpers.
type semanticQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type installationRef struct {
	InstallationID string
	ModuleID       string
	ExactVersion   string
	ManifestRef    string
	ArtifactDigest string
	ManifestBytes  []byte
}

// moduleArtifactRef is one inert server-owned artifact fact. It is distinct
// from an installation: ingress grants no activation, binding, execution, or
// tenant authority, but its bytes are nevertheless part of the durable Store
// closure and therefore must be backed up.
type moduleArtifactRef struct {
	ModuleID          string
	ExactVersion      string
	ManifestRef       string
	ArtifactDigest    string
	ArtifactSizeBytes uint64
	CoveredFileCount  uint64
	ManifestBytes     []byte
}

type snapshotState struct {
	Identity        StoreIdentity
	Current         []CurrentPublication
	Installations   []installationRef
	ModuleArtifacts []moduleArtifactRef
	AttemptCounts   AttemptCounts
}

func sqliteFileURI(path, mode string, pragmas ...string) string {
	normalized := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && filepath.VolumeName(path) != "" &&
		!strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	uri := url.URL{Scheme: "file", Path: normalized}
	query := uri.Query()
	query.Set("mode", mode)
	for _, pragma := range pragmas {
		query.Add("_pragma", pragma)
	}
	uri.RawQuery = query.Encode()
	return uri.String()
}

func openReadOnlyDatabase(path string) (*sql.DB, error) {
	database, err := sql.Open(
		"sqlite",
		sqliteFileURI(
			path,
			"ro",
			"query_only(1)",
			"foreign_keys(1)",
			"trusted_schema(0)",
			"busy_timeout(0)",
		),
	)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	return database, nil
}

func createSQLiteSnapshot(
	ctx context.Context,
	sourcePath string,
	destinationPath string,
) (returnErr error) {
	empty, err := os.OpenFile(
		destinationPath,
		os.O_CREATE|os.O_EXCL|os.O_RDWR,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("currentbackup: create SQLite snapshot target: %w", err)
	}
	if err := empty.Close(); err != nil {
		_ = os.Remove(destinationPath)
		return fmt.Errorf("currentbackup: close SQLite snapshot target: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			removeSQLiteFiles(destinationPath)
		}
	}()

	source, err := openReadOnlyDatabase(sourcePath)
	if err != nil {
		return fmt.Errorf("currentbackup: open source for SQLite backup: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, source.Close()) }()
	connection, err := source.Conn(ctx)
	if err != nil {
		return fmt.Errorf("currentbackup: acquire source backup connection: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, connection.Close()) }()
	if err := connection.Raw(func(driverConnection any) (rawErr error) {
		backuper, ok := driverConnection.(sqliteBackupConnection)
		if !ok {
			return errors.New("configured SQLite driver does not support Backup API")
		}
		backup, err := backuper.NewBackup(sqliteFileURI(destinationPath, "rw"))
		if err != nil {
			return err
		}
		defer func() { rawErr = errors.Join(rawErr, backup.Finish()) }()
		for {
			more, err := backup.Step(256)
			if err != nil {
				return err
			}
			if !more {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}); err != nil {
		return fmt.Errorf("currentbackup: SQLite Backup API: %w", err)
	}
	if err := consolidateSQLiteSnapshot(ctx, destinationPath); err != nil {
		return err
	}
	if err := verifyNoSQLiteSidecars(destinationPath); err != nil {
		return err
	}
	if _, err := currentstore.VerifyCurrentStoreReadOnly(ctx, destinationPath); err != nil {
		return fmt.Errorf("currentbackup: verify SQLite snapshot: %w", err)
	}
	if err := syncRegularFile(destinationPath); err != nil {
		return fmt.Errorf("currentbackup: sync SQLite snapshot: %w", err)
	}
	keep = true
	return nil
}

func consolidateSQLiteSnapshot(ctx context.Context, path string) error {
	database, err := sql.Open(
		"sqlite",
		sqliteFileURI(
			path,
			"rw",
			"foreign_keys(1)",
			"trusted_schema(0)",
			"busy_timeout(0)",
			"synchronous(FULL)",
		),
	)
	if err != nil {
		return fmt.Errorf("currentbackup: open SQLite snapshot for consolidation: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()
	var busy, remaining, checkpointed int
	if err := database.QueryRowContext(
		ctx,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	).Scan(&busy, &remaining, &checkpointed); err != nil {
		return fmt.Errorf("currentbackup: checkpoint SQLite snapshot: %w", err)
	}
	if busy != 0 || remaining > 0 {
		return fmt.Errorf(
			"%w: SQLite checkpoint incomplete: busy=%d remaining=%d checkpointed=%d",
			ErrIntegrity,
			busy,
			remaining,
			checkpointed,
		)
	}
	var journalMode string
	if err := database.QueryRowContext(
		ctx,
		`PRAGMA journal_mode=DELETE`,
	).Scan(&journalMode); err != nil {
		return fmt.Errorf("currentbackup: set snapshot journal mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "delete") {
		return fmt.Errorf(
			"%w: snapshot journal mode is %q, want DELETE",
			ErrIntegrity,
			journalMode,
		)
	}
	return database.Close()
}

func inspectSnapshot(ctx context.Context, path string) (snapshotState, error) {
	verification, err := currentstore.VerifyCurrentStoreReadOnly(ctx, path)
	if err != nil {
		return snapshotState{}, fmt.Errorf("%w: verify Current Store: %v", ErrIntegrity, err)
	}
	database, err := openReadOnlyDatabase(path)
	if err != nil {
		return snapshotState{}, fmt.Errorf("currentbackup: open inspected snapshot: %w", err)
	}
	defer database.Close()
	connection, err := database.Conn(ctx)
	if err != nil {
		return snapshotState{}, fmt.Errorf(
			"currentbackup: acquire semantic verification connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return snapshotState{}, fmt.Errorf(
			"currentbackup: begin semantic verification: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	state := snapshotState{
		Current:         make([]CurrentPublication, 0),
		Installations:   make([]installationRef, 0),
		ModuleArtifacts: make([]moduleArtifactRef, 0),
		Identity: StoreIdentity{
			ApplicationID:     currentstore.ApplicationID,
			UserVersion:       currentstore.UserVersion,
			SchemaIdentity:    verification.SchemaIdentity,
			SchemaFingerprint: verification.SchemaFingerprint,
			GeneratorID:       verification.GeneratorID,
			StoreInstanceID:   verification.StoreInstanceID,
		},
	}
	if err := preflightSnapshotPopulationBoundsV1(ctx, connection); err != nil {
		return snapshotState{}, err
	}
	if err := inspectCurrentPublications(ctx, connection, &state); err != nil {
		return snapshotState{}, err
	}
	if err := inspectInstallations(ctx, connection, &state); err != nil {
		return snapshotState{}, err
	}
	if err := currentstore.VerifyModuleArtifactIngressSemanticClosureV1(
		ctx,
		connection,
	); err != nil {
		return snapshotState{}, fmt.Errorf(
			"%w: module artifact ingress closure: %v",
			ErrIntegrity,
			err,
		)
	}
	if err := inspectModuleArtifacts(ctx, connection, &state); err != nil {
		return snapshotState{}, err
	}
	if len(distinctArtifactDigests(state.Installations, state.ModuleArtifacts)) > maxArtifactCount {
		return snapshotState{}, fmt.Errorf(
			"%w: managed artifact count exceeds %d",
			ErrIntegrity,
			maxArtifactCount,
		)
	}
	if err := inspectAgentMemoryRevisions(ctx, connection); err != nil {
		return snapshotState{}, err
	}
	if err := inspectCoreRunSemanticClosure(ctx, connection); err != nil {
		return snapshotState{}, err
	}
	if err := currentstore.VerifyOverviewObservationSemanticClosureV1(
		ctx,
		connection,
	); err != nil {
		return snapshotState{}, fmt.Errorf(
			"%w: Overview observation closure: %v",
			ErrIntegrity,
			err,
		)
	}
	if err := currentstore.VerifyConversationCompilerOutputsV1(
		ctx,
		connection,
	); err != nil {
		return snapshotState{}, fmt.Errorf(
			"%w: Conversation Context Compiler closure: %v",
			ErrIntegrity,
			err,
		)
	}
	if err := currentstore.VerifyControlOperationReceiptSemanticClosureV1(
		ctx,
		connection,
	); err != nil {
		return snapshotState{}, fmt.Errorf(
			"%w: Control operation receipt closure: %v",
			ErrIntegrity,
			err,
		)
	}
	if err := inspectSchedulerSemanticClosure(ctx, connection); err != nil {
		return snapshotState{}, err
	}
	if err := inspectActionSemanticClosure(ctx, connection); err != nil {
		return snapshotState{}, err
	}
	if err := inspectChannelSemanticClosure(ctx, connection); err != nil {
		return snapshotState{}, err
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN state='PENDING' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state='MODEL_UNKNOWN' THEN 1 ELSE 0 END), 0)
		FROM model_dispatch_attempts
	`).Scan(&state.AttemptCounts.ModelPending, &state.AttemptCounts.ModelUnknown); err != nil {
		return snapshotState{}, fmt.Errorf("currentbackup: count model attempts: %w", err)
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN state='PENDING' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state='UNKNOWN' THEN 1 ELSE 0 END), 0)
		FROM dispatch_attempts
		WHERE dispatch_kind='ACTION'
	`).Scan(&state.AttemptCounts.ActionPending, &state.AttemptCounts.ActionUnknown); err != nil {
		return snapshotState{}, fmt.Errorf("currentbackup: count Action attempts: %w", err)
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM channel_ingress_receipts),
			(SELECT COUNT(*) FROM (
				SELECT tenant_id, endpoint_id, cursor_scope_key
				FROM channel_ingress_receipts
				GROUP BY tenant_id, endpoint_id, cursor_scope_key
			))
	`).Scan(
		&state.AttemptCounts.ChannelIngressReceipts,
		&state.AttemptCounts.ChannelCursorScopes,
	); err != nil {
		return snapshotState{}, fmt.Errorf("currentbackup: count Channel ingress: %w", err)
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN state='PENDING' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN state='UNKNOWN' THEN 1 ELSE 0 END), 0)
		FROM dispatch_attempts
		WHERE dispatch_kind='CHANNEL_SEND'
	`).Scan(
		&state.AttemptCounts.ChannelSendPending,
		&state.AttemptCounts.ChannelSendUnknown,
	); err != nil {
		return snapshotState{}, fmt.Errorf("currentbackup: count Channel sends: %w", err)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return snapshotState{}, fmt.Errorf(
			"currentbackup: commit semantic verification: %w",
			err,
		)
	}
	committed = true
	return state, nil
}

// preflightSnapshotPopulationBoundsV1 rejects database populations whose
// materialized projections cannot fit the public Backup contract. These
// scalar counts run before any manifest/content BLOB is scanned or cloned, so
// a structurally valid but hostile database cannot amplify memory ahead of the
// physical artifact-root limits.
func preflightSnapshotPopulationBoundsV1(
	ctx context.Context,
	database semanticQueryer,
) error {
	var currentCount, installationCount, artifactCount int64
	if err := database.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM control_current),
			(SELECT COUNT(*) FROM module_installations),
			(SELECT COUNT(*) FROM module_artifacts)
	`).Scan(&currentCount, &installationCount, &artifactCount); err != nil {
		return fmt.Errorf("currentbackup: read snapshot population bounds: %w", err)
	}
	if currentCount < 0 || currentCount > int64(maxCurrentCount) {
		return fmt.Errorf(
			"%w: current publication count exceeds %d",
			ErrIntegrity,
			maxCurrentCount,
		)
	}
	if installationCount < 0 || installationCount > int64(maxArtifactCount) {
		return fmt.Errorf(
			"%w: module installation count exceeds %d",
			ErrIntegrity,
			maxArtifactCount,
		)
	}
	if artifactCount < 0 || artifactCount > int64(maxArtifactCount) {
		return fmt.Errorf(
			"%w: module artifact count exceeds %d",
			ErrIntegrity,
			maxArtifactCount,
		)
	}
	return nil
}

// VerifyCurrentStoreSemanticClosure performs the same read-only database
// semantic checks used by backup/restore without reading an artifact tree or
// constructing any Adapter. Explicit optional-module enablement may use this
// as a fail-closed gate after Store-only startup recovery.
func VerifyCurrentStoreSemanticClosure(ctx context.Context, path string) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	resolved, err := resolveExistingRegularFile(path, "Current Store database")
	if err != nil {
		return err
	}
	if _, err := inspectSnapshot(ctx, resolved); err != nil {
		return fmt.Errorf("currentbackup: verify Current Store semantic closure: %w", err)
	}
	return nil
}

// inspectAgentMemoryRevisions verifies the complete semantic closure carried
// inside the SQLite snapshot. Memory remains part of database.sqlite: the
// bundle format has no parallel Memory index or sidecar to drift from this
// authoritative append-only chain.
func inspectAgentMemoryRevisions(
	ctx context.Context,
	database semanticQueryer,
) error {
	rows, err := database.QueryContext(ctx, `
		SELECT
			memory.tenant_id,
			memory.agent_id,
			memory.revision,
			memory.snapshot_ref,
			memory.source_attempt_id,
			content.kind,
			content.media_type,
			content.canonical_bytes,
			content.size_bytes,
			source.state,
			source_run.tenant_id,
			source_member.agent_id
		FROM agent_memory_revisions AS memory
		JOIN content_records AS content
		  ON content.content_digest=memory.snapshot_ref
		LEFT JOIN model_dispatch_attempts AS source
		  ON source.attempt_id=memory.source_attempt_id
		LEFT JOIN runs AS source_run
		  ON source_run.run_id=source.run_id
		LEFT JOIN member_execution_snapshots AS source_member
		  ON source_member.run_id=source.run_id
		 AND source_member.member_id=source.member_id
		ORDER BY memory.tenant_id, memory.agent_id, memory.revision
	`)
	if err != nil {
		return fmt.Errorf("currentbackup: read Agent Memory revisions: %w", err)
	}
	defer rows.Close()

	var (
		previousTenant   string
		previousAgent    string
		previousRevision uint64
		previousDigest   string
		havePrevious     bool
	)
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var (
			tenantID      string
			agentID       string
			revision      int64
			snapshotRef   string
			sourceAttempt sql.NullString
			kind          string
			mediaType     string
			canonical     []byte
			sizeBytes     int64
			sourceState   sql.NullString
			sourceTenant  sql.NullString
			sourceAgent   sql.NullString
		)
		if err := rows.Scan(
			&tenantID,
			&agentID,
			&revision,
			&snapshotRef,
			&sourceAttempt,
			&kind,
			&mediaType,
			&canonical,
			&sizeBytes,
			&sourceState,
			&sourceTenant,
			&sourceAgent,
		); err != nil {
			return fmt.Errorf("currentbackup: scan Agent Memory revision: %w", err)
		}
		if revision < 1 || uint64(revision) > moduleapi.MaxMemorySafeIntegerV1 {
			return fmt.Errorf("%w: Agent Memory revision is outside the supported range", ErrIntegrity)
		}
		currentRevision := uint64(revision)
		newOwner := !havePrevious || tenantID != previousTenant || agentID != previousAgent
		if newOwner {
			if currentRevision != 1 {
				return fmt.Errorf(
					"%w: Agent Memory chain for tenant=%q agent=%q does not start at revision 1",
					ErrIntegrity,
					tenantID,
					agentID,
				)
			}
		} else if currentRevision != previousRevision+1 {
			return fmt.Errorf(
				"%w: Agent Memory chain for tenant=%q agent=%q is not continuous at revision %d",
				ErrIntegrity,
				tenantID,
				agentID,
				currentRevision,
			)
		}
		if kind != string(currentstore.ContentMemorySnapshot) ||
			mediaType != "application/json" ||
			int64(len(canonical)) != sizeBytes ||
			!moduleapi.ValidSHA256(snapshotRef) {
			return fmt.Errorf(
				"%w: invalid Agent Memory snapshot ContentRecord closure",
				ErrIntegrity,
			)
		}
		computed, err := currentstore.ComputeContentDigest(
			currentstore.ContentMemorySnapshot,
			"application/json",
			canonical,
		)
		if err != nil || computed != snapshotRef {
			return fmt.Errorf(
				"%w: Agent Memory snapshot content digest differs: %v",
				ErrIntegrity,
				err,
			)
		}
		snapshot, err := moduleapi.RestoreAgentMemorySnapshotV1(canonical)
		if err != nil {
			return fmt.Errorf(
				"%w: restore Agent Memory snapshot: %v",
				ErrIntegrity,
				err,
			)
		}
		if snapshot.TenantID != tenantID || snapshot.AgentID != agentID ||
			snapshot.Revision != currentRevision ||
			snapshot.SourceAttemptID != sourceAttempt.String {
			return fmt.Errorf(
				"%w: Agent Memory row owner/revision/source differs from snapshot",
				ErrIntegrity,
			)
		}
		if currentRevision == 1 {
			if sourceAttempt.Valid || snapshot.PreviousSnapshotDigest != "" ||
				sourceState.Valid || sourceTenant.Valid || sourceAgent.Valid {
				return fmt.Errorf(
					"%w: Agent Memory genesis contains parent or source closure",
					ErrIntegrity,
				)
			}
		} else {
			if newOwner || !sourceAttempt.Valid || sourceAttempt.String == "" ||
				snapshot.PreviousSnapshotDigest != previousDigest {
				return fmt.Errorf(
					"%w: Agent Memory revision %d has invalid parent/source closure",
					ErrIntegrity,
					currentRevision,
				)
			}
			if !sourceState.Valid || sourceState.String != "SUCCEEDED" ||
				!sourceTenant.Valid || sourceTenant.String != tenantID ||
				!sourceAgent.Valid || sourceAgent.String != agentID {
				return fmt.Errorf(
					"%w: Agent Memory source Attempt is not SUCCEEDED for the exact owner",
					ErrIntegrity,
				)
			}
		}
		previousTenant = tenantID
		previousAgent = agentID
		previousRevision = currentRevision
		previousDigest = snapshotRef
		havePrevious = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("currentbackup: iterate Agent Memory revisions: %w", err)
	}
	return nil
}

func inspectCurrentPublications(
	ctx context.Context,
	database semanticQueryer,
	state *snapshotState,
) error {
	rows, err := database.QueryContext(ctx, `
		SELECT
			current.tenant_id,
			current.pointer_revision,
			control.snapshot_id,
			control.revision,
			control.digest,
			control.canonical_json,
			catalog.generation_id,
			catalog.generation,
			catalog.digest,
			catalog.canonical_json,
			catalog.control_snapshot_id
		FROM control_current AS current
		JOIN control_snapshots AS control
		  ON control.snapshot_id=current.snapshot_id
		JOIN runtime_catalog_generations AS catalog
		  ON catalog.generation_id=current.catalog_generation_id
		ORDER BY current.tenant_id
	`)
	if err != nil {
		return fmt.Errorf("currentbackup: read current publications: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if len(state.Current) >= maxCurrentCount {
			return fmt.Errorf(
				"%w: current publication count exceeds %d",
				ErrIntegrity,
				maxCurrentCount,
			)
		}
		var item CurrentPublication
		var controlCanonical, catalogCanonical []byte
		var catalogControlID string
		if err := rows.Scan(
			&item.TenantID,
			&item.PointerRevision,
			&item.ControlID,
			&item.ControlRevision,
			&item.ControlDigest,
			&controlCanonical,
			&item.CatalogID,
			&item.CatalogGeneration,
			&item.CatalogDigest,
			&catalogCanonical,
			&catalogControlID,
		); err != nil {
			return fmt.Errorf("currentbackup: scan current publication: %w", err)
		}
		control, err := controlcontract.RestoreControlSnapshot(
			controlCanonical,
			controlcontract.ControlSnapshotRef{
				SnapshotID: item.ControlID,
				Revision:   item.ControlRevision,
				Digest:     item.ControlDigest,
			},
		)
		if err != nil {
			return fmt.Errorf("%w: restore current Control: %v", ErrIntegrity, err)
		}
		catalog, err := controlcontract.RestoreCatalogGeneration(
			catalogCanonical,
			controlcontract.CatalogGenerationRef{
				GenerationID: item.CatalogID,
				Generation:   item.CatalogGeneration,
				Digest:       item.CatalogDigest,
			},
		)
		if err != nil {
			return fmt.Errorf("%w: restore current Catalog: %v", ErrIntegrity, err)
		}
		if control.TenantID != item.TenantID ||
			catalog.TenantID != item.TenantID ||
			catalog.ControlSnapshotID != item.ControlID ||
			catalog.ControlSnapshotDigest != item.ControlDigest ||
			catalogControlID != item.ControlID {
			return fmt.Errorf("%w: current Control/Catalog closure differs", ErrIntegrity)
		}
		if err := currentstore.VerifyPublishedControlCatalogClosureV1(
			ctx,
			database,
			control,
			catalog,
		); err != nil {
			return fmt.Errorf(
				"%w: current Control/Catalog semantic closure: %v",
				ErrIntegrity,
				err,
			)
		}
		state.Current = append(state.Current, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("currentbackup: iterate current publications: %w", err)
	}
	return nil
}

func inspectInstallations(
	ctx context.Context,
	database semanticQueryer,
	state *snapshotState,
) error {
	rows, err := database.QueryContext(ctx, `
		SELECT
			installation.installation_id,
			installation.module_id,
			installation.exact_version,
			installation.manifest_ref,
			installation.artifact_digest,
			content.kind,
			content.media_type,
			content.canonical_bytes
		FROM module_installations AS installation
		JOIN content_records AS content
		  ON content.content_digest=installation.manifest_ref
		ORDER BY installation.installation_id
	`)
	if err != nil {
		return fmt.Errorf("currentbackup: read module installations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if len(state.Installations) >= maxArtifactCount {
			return fmt.Errorf(
				"%w: module installation count exceeds %d",
				ErrIntegrity,
				maxArtifactCount,
			)
		}
		var item installationRef
		var kind, mediaType string
		if err := rows.Scan(
			&item.InstallationID,
			&item.ModuleID,
			&item.ExactVersion,
			&item.ManifestRef,
			&item.ArtifactDigest,
			&kind,
			&mediaType,
			&item.ManifestBytes,
		); err != nil {
			return fmt.Errorf("currentbackup: scan module installation: %w", err)
		}
		if kind != string(currentstore.ContentModuleManifest) ||
			mediaType != "application/json" ||
			!moduleapi.ValidSHA256(item.ManifestRef) ||
			!moduleapi.ValidSHA256(item.ArtifactDigest) {
			return fmt.Errorf("%w: invalid installed module content closure", ErrIntegrity)
		}
		manifest, canonical, err := moduleapi.ParseModuleManifestV1(item.ManifestBytes)
		if err != nil || !bytes.Equal(canonical, item.ManifestBytes) ||
			manifest.ID != item.ModuleID || manifest.Version != item.ExactVersion {
			return fmt.Errorf("%w: installation manifest differs: %v", ErrIntegrity, err)
		}
		computed, err := currentstore.ComputeContentDigest(
			currentstore.ContentModuleManifest,
			"application/json",
			canonical,
		)
		if err != nil || computed != item.ManifestRef {
			return fmt.Errorf("%w: installation manifest_ref differs", ErrIntegrity)
		}
		item.ManifestBytes = bytes.Clone(item.ManifestBytes)
		state.Installations = append(state.Installations, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("currentbackup: iterate module installations: %w", err)
	}
	return nil
}

func inspectModuleArtifacts(
	ctx context.Context,
	database semanticQueryer,
	state *snapshotState,
) error {
	rows, err := database.QueryContext(ctx, `
		SELECT
			artifact.module_id,
			artifact.exact_version,
			artifact.manifest_ref,
			artifact.artifact_digest,
			artifact.artifact_size_bytes,
			artifact.covered_file_count,
			content.kind,
			content.media_type,
			content.canonical_bytes
		FROM module_artifacts AS artifact
		JOIN content_records AS content
		  ON content.content_digest=artifact.manifest_ref
		ORDER BY artifact.artifact_digest
	`)
	if err != nil {
		return fmt.Errorf("currentbackup: read module artifacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if len(state.ModuleArtifacts) >= maxArtifactCount {
			return fmt.Errorf(
				"%w: module artifact count exceeds %d",
				ErrIntegrity,
				maxArtifactCount,
			)
		}
		var item moduleArtifactRef
		var artifactSize, coveredFileCount int64
		var kind, mediaType string
		if err := rows.Scan(
			&item.ModuleID,
			&item.ExactVersion,
			&item.ManifestRef,
			&item.ArtifactDigest,
			&artifactSize,
			&coveredFileCount,
			&kind,
			&mediaType,
			&item.ManifestBytes,
		); err != nil {
			return fmt.Errorf("currentbackup: scan module artifact: %w", err)
		}
		if artifactSize <= 0 || coveredFileCount <= 0 ||
			artifactSize > maxArtifactBytes ||
			coveredFileCount > int64(moduleapi.DefaultArtifactMaxFiles) ||
			kind != string(currentstore.ContentModuleManifest) ||
			mediaType != "application/json" ||
			!moduleapi.ValidSHA256(item.ManifestRef) ||
			!moduleapi.ValidSHA256(item.ArtifactDigest) {
			return fmt.Errorf(
				"%w: invalid module artifact content closure",
				ErrIntegrity,
			)
		}
		manifest, canonical, err := moduleapi.ParseModuleManifestV1(
			item.ManifestBytes,
		)
		if err != nil || !bytes.Equal(canonical, item.ManifestBytes) ||
			manifest.ID != item.ModuleID ||
			manifest.Version != item.ExactVersion {
			return fmt.Errorf(
				"%w: module artifact manifest differs: %v",
				ErrIntegrity,
				err,
			)
		}
		computed, err := currentstore.ComputeContentDigest(
			currentstore.ContentModuleManifest,
			"application/json",
			canonical,
		)
		if err != nil || computed != item.ManifestRef {
			return fmt.Errorf(
				"%w: module artifact manifest_ref differs",
				ErrIntegrity,
			)
		}
		item.ArtifactSizeBytes = uint64(artifactSize)
		item.CoveredFileCount = uint64(coveredFileCount)
		item.ManifestBytes = bytes.Clone(item.ManifestBytes)
		state.ModuleArtifacts = append(state.ModuleArtifacts, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("currentbackup: iterate module artifacts: %w", err)
	}
	return nil
}

func distinctArtifactDigests(
	installations []installationRef,
	moduleArtifacts []moduleArtifactRef,
) []string {
	set := make(map[string]struct{}, len(installations)+len(moduleArtifacts))
	for _, installation := range installations {
		set[installation.ArtifactDigest] = struct{}{}
	}
	for _, artifact := range moduleArtifacts {
		set[artifact.ArtifactDigest] = struct{}{}
	}
	digests := make([]string, 0, len(set))
	for digest := range set {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	return digests
}

func removeSQLiteFiles(path string) {
	for _, candidate := range []string{
		path + "-journal",
		path + "-wal",
		path + "-shm",
		path,
	} {
		_ = os.Remove(candidate)
	}
}
