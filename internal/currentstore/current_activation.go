package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

var ErrCurrentActivationDenied = errors.New(
	"currentstore: frozen Provider is not active in current Catalog",
)

// CheckCurrentActivation performs the sole runtime exception to frozen Run
// recovery: a deny-only read of the current Control/Catalog pointer. It can
// only confirm that the exact frozen Provider still exposes the exact Port; it
// never returns a replacement Provider or current configuration to callers.
func (store *Store) CheckCurrentActivation(
	ctx context.Context,
	runID string,
	port moduleapi.PortRef,
	provider moduleapi.ActivatedModuleRef,
) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrCurrentActivationDenied)
	}
	if !validLeaseOpaqueID(runID) {
		return fmt.Errorf("%w: invalid Run ID", ErrCurrentActivationDenied)
	}
	if err := port.Validate(); err != nil {
		return fmt.Errorf("%w: invalid Port: %v", ErrCurrentActivationDenied, err)
	}
	if err := provider.Validate(); err != nil {
		return fmt.Errorf(
			"%w: invalid frozen Provider: %v",
			ErrCurrentActivationDenied,
			err,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCurrentActivationDenied, err)
	}
	defer unlock()
	return checkCurrentActivation(
		ctx,
		store.db,
		runID,
		port,
		provider,
	)
}

func checkCurrentActivation(
	ctx context.Context,
	queryer publicationQueryer,
	runID string,
	port moduleapi.PortRef,
	provider moduleapi.ActivatedModuleRef,
) error {
	var tenantID string
	if err := queryer.QueryRowContext(ctx, `
		SELECT tenant_id
		FROM runs
		WHERE run_id=?
	`, runID).Scan(&tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf(
				"%w: Run %q is absent",
				ErrCurrentActivationDenied,
				runID,
			)
		}
		return fmt.Errorf(
			"%w: read Run tenant: %v",
			ErrCurrentActivationDenied,
			err,
		)
	}
	if err := validatePublishedBasisTenantID(tenantID); err != nil {
		return fmt.Errorf(
			"%w: invalid Run tenant: %v",
			ErrCurrentActivationDenied,
			err,
		)
	}

	current, found, err := queryCurrentControlPointer(ctx, queryer, tenantID)
	if err != nil {
		return fmt.Errorf(
			"%w: current pointer: %v",
			ErrCurrentActivationDenied,
			err,
		)
	}
	if !found || current.TenantID != tenantID {
		return fmt.Errorf(
			"%w: Tenant %q has no valid current pointer",
			ErrCurrentActivationDenied,
			tenantID,
		)
	}
	_, catalog, err := loadAdmissionControlCatalog(
		ctx,
		queryer,
		tenantID,
		current.SnapshotID,
		current.CatalogGenerationID,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: current Control/Catalog closure: %v",
			ErrCurrentActivationDenied,
			err,
		)
	}
	entry, found := catalog.FindInstance(provider.InstanceID)
	if !found || entry.Activation != provider ||
		!currentCatalogProvidesPort(entry.Provides, port) {
		return fmt.Errorf(
			"%w: exact Provider %s/%d is absent for %s/%s",
			ErrCurrentActivationDenied,
			provider.InstanceID,
			provider.ActivationRevision,
			port.Name,
			port.ExactVersion,
		)
	}
	return nil
}

func currentCatalogProvidesPort(
	provided []moduleapi.PortRef,
	want moduleapi.PortRef,
) bool {
	for _, candidate := range provided {
		if candidate == want {
			return true
		}
	}
	return false
}
