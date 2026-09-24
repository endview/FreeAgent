package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
)

// ReadOnlyObserver is a capability-restricted Current Store view for offline
// evidence extraction. It owns a SQLite mode=ro/query_only/immutable
// connection and
// deliberately exposes only verified read projections. It never acquires the
// writer fence, changes journal mode, performs recovery, or exposes *Store.
type ReadOnlyObserver struct {
	store *Store
}

// OpenReadOnlyObserver verifies an existing self-contained FAC2 Store and
// opens a query-only handle over that exact path. The source must be closed;
// immutable mode deliberately ignores WAL state and this function therefore
// rejects every SQLite sidecar before opening.
func OpenReadOnlyObserver(
	ctx context.Context,
	path string,
) (*ReadOnlyObserver, error) {
	if ctx == nil {
		return nil, errors.New("currentstore: observer context is nil")
	}
	canonicalPath, err := existingDatabasePath(path)
	if err != nil {
		return nil, err
	}
	if err := rejectSQLiteSidecars(canonicalPath); err != nil {
		return nil, err
	}
	verification, err := verifyCurrentStoreReadOnly(ctx, canonicalPath, true, false)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(
		"sqlite",
		sqliteImmutableURI(verification.Path, []string{
			"query_only(1)",
			"foreign_keys(1)",
			"trusted_schema(0)",
			"busy_timeout(0)",
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("currentstore: open read-only observer: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	connection, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf(
			"currentstore: acquire read-only observer connection: %w",
			err,
		)
	}
	for pragma, expected := range map[string]int{
		"query_only":     1,
		"foreign_keys":   1,
		"trusted_schema": 0,
	} {
		var actual int
		if err := connection.QueryRowContext(
			ctx,
			"PRAGMA "+pragma,
		).Scan(&actual); err != nil || actual != expected {
			_ = connection.Close()
			_ = db.Close()
			if err != nil {
				return nil, fmt.Errorf(
					"currentstore: read observer PRAGMA %s: %w",
					pragma,
					err,
				)
			}
			return nil, &IdentityError{
				Field:    "observer PRAGMA " + pragma,
				Expected: strconv.Itoa(expected),
				Actual:   strconv.Itoa(actual),
			}
		}
	}
	if err := connection.Close(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf(
			"currentstore: close observer verification connection: %w",
			err,
		)
	}
	return &ReadOnlyObserver{store: &Store{
		path: verification.Path,
		db:   db,
	}}, nil
}

// Close releases only the read-only database handle. There is no writer
// lease or recovery work to release.
func (observer *ReadOnlyObserver) Close() error {
	if observer == nil || observer.store == nil {
		return nil
	}
	err := observer.store.Close()
	observer.store = nil
	return err
}

// ListCompositeRootRunIDs returns every intact Composite ROOT in persisted
// creation order. The method restores each root manifest before classification
// so malformed bytes cannot silently disappear from an audit.
func (observer *ReadOnlyObserver) ListCompositeRootRunIDs(
	ctx context.Context,
) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("currentstore: observer context is nil")
	}
	if observer == nil || observer.store == nil {
		return nil, ErrStoreClosed
	}
	unlock, err := observer.store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()

	tx, err := observer.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: begin Composite root observation: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	rows, err := tx.QueryContext(ctx, `
		SELECT r.run_id, m.canonical_json, m.digest
		FROM runs AS r
		JOIN run_manifests AS m ON m.run_id=r.run_id
		WHERE r.parent_run_id IS NULL
		ORDER BY r.created_at, r.run_id
	`)
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: list Composite root manifests: %w",
			err,
		)
	}
	defer rows.Close()
	rootRunIDs := make([]string, 0)
	for rows.Next() {
		var runID, digest string
		var canonical []byte
		if err := rows.Scan(&runID, &canonical, &digest); err != nil {
			return nil, fmt.Errorf(
				"currentstore: scan Composite root manifest: %w",
				err,
			)
		}
		manifest, err := corecontract.RestoreRunManifest(canonical)
		if err != nil || manifest.RunID != runID ||
			manifest.ManifestDigest != digest {
			return nil, fmt.Errorf(
				"%w: root Run manifest projection is invalid",
				ErrCompositeUsageProjectionIntegrity,
			)
		}
		if manifest.Composite == nil {
			continue
		}
		if manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
			manifest.Composite.Plan == nil || manifest.ParentRunID != "" {
			return nil, fmt.Errorf(
				"%w: parentless Composite Run is not an intact ROOT",
				ErrCompositeUsageProjectionIntegrity,
			)
		}
		rootRunIDs = append(rootRunIDs, runID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"currentstore: iterate Composite root manifests: %w",
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf(
			"currentstore: close Composite root manifest rows: %w",
			err,
		)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf(
			"currentstore: commit Composite root observation: %w",
			err,
		)
	}
	committed = true
	return rootRunIDs, nil
}

func (observer *ReadOnlyObserver) GetCompositeFamilyUsageProjection(
	ctx context.Context,
	rootRunID string,
) (CompositeFamilyUsageProjectionV1, error) {
	if observer == nil || observer.store == nil {
		return CompositeFamilyUsageProjectionV1{}, ErrStoreClosed
	}
	return observer.store.GetCompositeFamilyUsageProjection(ctx, rootRunID)
}

func (observer *ReadOnlyObserver) GetTerminalRunResult(
	ctx context.Context,
	runID string,
) (TerminalRunResult, error) {
	if observer == nil || observer.store == nil {
		return TerminalRunResult{}, ErrStoreClosed
	}
	return observer.store.GetTerminalRunResult(ctx, runID)
}

func (observer *ReadOnlyObserver) GetFairRunTargetView(
	ctx context.Context,
	runID string,
) (FairRunTargetView, error) {
	if observer == nil || observer.store == nil {
		return FairRunTargetView{}, ErrStoreClosed
	}
	return observer.store.GetFairRunTargetView(ctx, runID)
}

// LoadPublishedBasis returns the exact current Control/Catalog closure through
// the capability-restricted observer. It does not expose the underlying Store
// or any mutable publication primitive.
func (observer *ReadOnlyObserver) LoadPublishedBasis(
	ctx context.Context,
	tenantID string,
) (
	controlcontract.PublishedBasis,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
	error,
) {
	if observer == nil || observer.store == nil {
		return controlcontract.PublishedBasis{},
			controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			ErrStoreClosed
	}
	return observer.store.LoadPublishedBasis(ctx, tenantID)
}

// VerifyPublishedControlCatalogClosureV1 exposes the Store's complete
// publication verifier without exposing SQL or any mutation capability.
func (observer *ReadOnlyObserver) VerifyPublishedControlCatalogClosureV1(
	ctx context.Context,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	if observer == nil || observer.store == nil {
		return ErrStoreClosed
	}
	return observer.store.VerifyPublishedControlCatalogClosureV1(
		ctx,
		control,
		catalog,
	)
}

// GetModuleInstallationByIdentity returns one immutable installation needed
// to close a current Catalog entry to its installed manifest. The observer
// remains read-only and never exposes SQL or *Store.
func (observer *ReadOnlyObserver) GetModuleInstallationByIdentity(
	ctx context.Context,
	moduleID string,
	exactVersion string,
) (ModuleInstallation, error) {
	if observer == nil || observer.store == nil {
		return ModuleInstallation{}, ErrStoreClosed
	}
	return observer.store.GetModuleInstallationByIdentity(
		ctx,
		moduleID,
		exactVersion,
	)
}

// StartupRecoveryRequired completes the same integrity-checked startup scan
// used by the writer path, but never performs recovery. Only a crash-left
// PENDING Model, Action, or Channel Attempt requires startup recovery;
// UNKNOWN Attempts remain reconciliation work and do not permit replay.
func (observer *ReadOnlyObserver) StartupRecoveryRequired(
	ctx context.Context,
) (bool, error) {
	if observer == nil || observer.store == nil {
		return false, ErrStoreClosed
	}
	runs, err := observer.store.ScanStartupRecovery(ctx)
	if err != nil {
		return false, err
	}
	required := false
	for _, run := range runs {
		if run.UnsettledAttemptState == corecontract.ModelAttemptPending ||
			run.UnsettledActionAttemptState == ActionDispatchPending ||
			run.UnsettledChannelAttemptState == DispatchPending {
			required = true
		}
	}
	return required, nil
}

// LoadControlCatalogRevision returns one exact historical Control/Catalog
// closure without exposing the Store or its publication primitives.
func (observer *ReadOnlyObserver) LoadControlCatalogRevision(
	ctx context.Context,
	tenantID string,
	controlRevision uint64,
	catalogGeneration uint64,
) (
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
	error,
) {
	if observer == nil || observer.store == nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			ErrStoreClosed
	}
	return observer.store.LoadControlCatalogRevision(
		ctx,
		tenantID,
		controlRevision,
		catalogGeneration,
	)
}

// GetModuleActivationByIdentity returns the exact immutable activation frozen
// into a Catalog entry. Later unbound revisions remain inert.
func (observer *ReadOnlyObserver) GetModuleActivationByIdentity(
	ctx context.Context,
	tenantID string,
	instanceID string,
	activationRevision uint64,
) (ModuleActivation, error) {
	if observer == nil || observer.store == nil {
		return ModuleActivation{}, ErrStoreClosed
	}
	return observer.store.GetModuleActivationByIdentity(
		ctx,
		tenantID,
		instanceID,
		activationRevision,
	)
}

// GetLatestModuleActivationForInstance returns the latest immutable local
// activation for one exact tenant-scoped instance.
func (observer *ReadOnlyObserver) GetLatestModuleActivationForInstance(
	ctx context.Context,
	tenantID string,
	instanceID string,
) (ModuleActivation, error) {
	if observer == nil || observer.store == nil {
		return ModuleActivation{}, ErrStoreClosed
	}
	return observer.store.GetLatestModuleActivationForInstance(
		ctx,
		tenantID,
		instanceID,
	)
}

// GetContent returns one detached immutable ContentRecord addressed by its
// digest. In particular, CanonicalBytes remains a defensive copy.
func (observer *ReadOnlyObserver) GetContent(
	ctx context.Context,
	digest string,
) (ContentRecord, error) {
	if observer == nil || observer.store == nil {
		return ContentRecord{}, ErrStoreClosed
	}
	return observer.store.GetContent(ctx, digest)
}

// GetChannelCursorSeed exposes the verified revision-zero Channel cursor
// receipt without granting access to a mutable Store or resolving the current
// maximum cursor revision.
func (observer *ReadOnlyObserver) GetChannelCursorSeed(
	ctx context.Context,
	tenantID string,
	endpointID string,
	cursorScopeKey string,
) (ChannelIngressReceipt, error) {
	if observer == nil || observer.store == nil {
		return ChannelIngressReceipt{}, ErrStoreClosed
	}
	return observer.store.GetChannelCursorSeed(
		ctx,
		tenantID,
		endpointID,
		cursorScopeKey,
	)
}
