package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// LoadControlCatalogRevision returns one exact immutable historical
// Control/Catalog pair by its tenant-scoped revisions. It does not consult or
// change the current pointer. The Catalog must close to the selected Control.
func (store *Store) LoadControlCatalogRevision(
	ctx context.Context,
	tenantID string,
	controlRevision uint64,
	catalogGeneration uint64,
) (
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	returnErr error,
) {
	defer func() {
		returnErr = store.classifyPublishedBasisContentionV1(returnErr)
	}()
	if ctx == nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf("%w: context is nil", ErrInvalidPublishedBasis)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, err
	}
	defer unlock()
	if err := validatePublishedBasisTenantID(tenantID); err != nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, err
	}
	if controlRevision == 0 || controlRevision > math.MaxInt64 ||
		catalogGeneration == 0 || catalogGeneration > math.MaxInt64 {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf(
				"%w: historical Control/Catalog revisions are invalid",
				ErrInvalidPublishedBasis,
			)
	}
	var controlID, catalogID string
	err = store.db.QueryRowContext(ctx, `
		SELECT control.snapshot_id, catalog.generation_id
		FROM control_snapshots AS control
		JOIN runtime_catalog_generations AS catalog
		  ON catalog.control_snapshot_id=control.snapshot_id
		WHERE control.tenant_id=? AND control.revision=?
		  AND catalog.tenant_id=? AND catalog.generation=?
	`,
		tenantID,
		int64(controlRevision),
		tenantID,
		int64(catalogGeneration),
	).Scan(&controlID, &catalogID)
	if errors.Is(err, sql.ErrNoRows) {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf(
				"%w: historical Control/Catalog revision pair is absent",
				ErrPublishedBasisNotFound,
			)
	}
	if err != nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf("currentstore: read historical Control/Catalog pair: %w", err)
	}
	control, catalog, err = loadAdmissionControlCatalog(
		ctx,
		store.db,
		tenantID,
		controlID,
		catalogID,
	)
	if err != nil {
		if errors.Is(err, ErrAdmissionIntegrity) {
			return controlcontract.ControlSnapshot{},
				controlcontract.CatalogGeneration{},
				fmt.Errorf(
					"%w: historical Control/Catalog closure: %v",
					ErrPublishedBasisIntegrity,
					err,
				)
		}
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, err
	}
	if control.Revision != controlRevision ||
		catalog.Generation != catalogGeneration {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf(
				"%w: historical Control/Catalog revisions changed",
				ErrPublishedBasisIntegrity,
			)
	}
	return control, catalog, nil
}

var (
	// ErrInvalidPublishedBasis identifies a malformed PublishedBasis lookup.
	ErrInvalidPublishedBasis = errors.New(
		"currentstore: invalid PublishedBasis lookup",
	)

	// ErrPublishedBasisNotFound distinguishes a tenant with no current
	// Control/Catalog pointer, or an absent exact historical revision pair,
	// from invalid input or a damaged closure.
	ErrPublishedBasisNotFound = errors.New(
		"currentstore: PublishedBasis not found",
	)

	// ErrPublishedBasisIntegrity identifies a current pointer or immutable
	// Control/Catalog row that does not reconstruct one exact published basis.
	ErrPublishedBasisIntegrity = errors.New(
		"currentstore: PublishedBasis integrity violation",
	)
)

// LoadPublishedBasis returns the exact current PublishedBasis together with
// detached ControlSnapshot and CatalogGeneration contract values. Every stored
// canonical object is strictly restored against its projected revision and
// digest, and the Catalog must close one-way to the returned Control.
func (store *Store) LoadPublishedBasis(
	ctx context.Context,
	tenantID string,
) (
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	returnErr error,
) {
	defer func() {
		returnErr = store.classifyPublishedBasisContentionV1(returnErr)
	}()
	if ctx == nil {
		return emptyPublishedBasisResult(fmt.Errorf(
			"%w: context is nil",
			ErrInvalidPublishedBasis,
		))
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return emptyPublishedBasisResult(err)
	}
	defer unlock()

	if err := validatePublishedBasisTenantID(tenantID); err != nil {
		return emptyPublishedBasisResult(err)
	}

	current, found, err := queryCurrentControlPointer(
		ctx,
		store.db,
		tenantID,
	)
	if err != nil {
		if errors.Is(err, ErrPublicationConflict) {
			return emptyPublishedBasisResult(fmt.Errorf(
				"%w: current pointer: %v",
				ErrPublishedBasisIntegrity,
				err,
			))
		}
		return emptyPublishedBasisResult(fmt.Errorf(
			"currentstore: load PublishedBasis pointer: %w",
			err,
		))
	}
	if !found {
		return emptyPublishedBasisResult(fmt.Errorf(
			"%w: tenant %q has no current Control/Catalog pointer",
			ErrPublishedBasisNotFound,
			tenantID,
		))
	}
	if current.TenantID != tenantID {
		return emptyPublishedBasisResult(fmt.Errorf(
			"%w: current pointer tenant differs from lookup tenant",
			ErrPublishedBasisIntegrity,
		))
	}

	control, catalog, err = loadAdmissionControlCatalog(
		ctx,
		store.db,
		tenantID,
		current.SnapshotID,
		current.CatalogGenerationID,
	)
	if err != nil {
		if errors.Is(err, ErrAdmissionIntegrity) {
			return emptyPublishedBasisResult(fmt.Errorf(
				"%w: current Control/Catalog closure: %v",
				ErrPublishedBasisIntegrity,
				err,
			))
		}
		return emptyPublishedBasisResult(fmt.Errorf(
			"currentstore: load PublishedBasis Control/Catalog closure: %w",
			err,
		))
	}

	basis = controlcontract.PublishedBasis{
		TenantID:        tenantID,
		PointerRevision: current.PointerRevision,
		Control: controlcontract.ControlSnapshotRef{
			SnapshotID: control.SnapshotID,
			Revision:   control.Revision,
			Digest:     control.Digest,
		},
		Catalog: controlcontract.CatalogGenerationRef{
			GenerationID: catalog.GenerationID,
			Generation:   catalog.Generation,
			Digest:       catalog.Digest,
		},
	}
	if err := basis.Validate(); err != nil {
		return emptyPublishedBasisResult(fmt.Errorf(
			"%w: reconstructed basis: %v",
			ErrPublishedBasisIntegrity,
			err,
		))
	}
	if control.TenantID != tenantID ||
		catalog.TenantID != tenantID ||
		current.SnapshotID != basis.Control.SnapshotID ||
		current.CatalogGenerationID != basis.Catalog.GenerationID ||
		catalog.ControlSnapshotID != basis.Control.SnapshotID ||
		catalog.ControlSnapshotDigest != basis.Control.Digest {
		return emptyPublishedBasisResult(fmt.Errorf(
			"%w: current pointer and Control/Catalog projections do not close",
			ErrPublishedBasisIntegrity,
		))
	}

	return basis, control, catalog, nil
}

// VerifyPublishedControlCatalogClosureV1 replays the complete read-only
// publication gate against one already-loaded pair while retaining Store
// lifecycle protection. It does not consult a caller-supplied graph, create an
// Adapter, or write any derived state.
func (store *Store) VerifyPublishedControlCatalogClosureV1(
	ctx context.Context,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (returnErr error) {
	defer func() {
		returnErr = store.classifyPublishedBasisContentionV1(returnErr)
	}()
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidPublishedBasis)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return err
	}
	defer unlock()
	return VerifyPublishedControlCatalogClosureV1(
		ctx,
		store.db,
		control,
		catalog,
	)
}

func (store *Store) classifyPublishedBasisContentionV1(err error) error {
	if err == nil || store == nil {
		return err
	}
	return classifySQLiteOwnerContention(store.path, err)
}

func emptyPublishedBasisResult(
	err error,
) (
	controlcontract.PublishedBasis,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
	error,
) {
	return controlcontract.PublishedBasis{},
		controlcontract.ControlSnapshot{},
		controlcontract.CatalogGeneration{},
		err
}

func validatePublishedBasisTenantID(tenantID string) error {
	if tenantID == "" ||
		len(tenantID) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(tenantID) ||
		tenantID != strings.TrimSpace(tenantID) ||
		tenantID != moduleapi.CanonicalText(tenantID) {
		return fmt.Errorf(
			"%w: tenant ID must be canonical, non-empty and at most %d bytes",
			ErrInvalidPublishedBasis,
			moduleapi.MaxOpaqueIDBytes,
		)
	}
	for _, character := range tenantID {
		if unicode.IsControl(character) {
			return fmt.Errorf(
				"%w: tenant ID contains a control character",
				ErrInvalidPublishedBasis,
			)
		}
	}
	return nil
}
