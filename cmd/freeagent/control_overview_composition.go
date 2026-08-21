package main

import (
	"context"
	"errors"

	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlhttp"
	"github.com/endview/freeagent/internal/controloverview"
	"github.com/endview/freeagent/internal/currentstore"
)

// productionControlOverviewReaderV1 is a lifecycle-free view of the sole
// already-open production Store. It never opens a second database handle and
// maps private storage failures before they reach the HTTP boundary.
type productionControlOverviewReaderV1 struct {
	store *currentstore.Store
}

func (reader productionControlOverviewReaderV1) LoadControlOverviewV1(
	ctx context.Context,
	tenantID string,
	workspaceID string,
	limit uint16,
) (controloverview.SnapshotV1, error) {
	if reader.store == nil {
		return controloverview.SnapshotV1{}, controlapp.ErrStoreUnavailable
	}
	snapshot, err := reader.store.LoadControlOverviewV1(
		ctx,
		tenantID,
		workspaceID,
		limit,
	)
	if err != nil {
		return controloverview.SnapshotV1{}, mapControlOverviewStoreErrorV1(err)
	}
	return snapshot, nil
}

func mapControlOverviewStoreErrorV1(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return controlapp.ErrCancelled
	case errors.Is(err, controloverview.ErrInvalidRequest),
		errors.Is(err, controloverview.ErrNotFound),
		errors.Is(err, controloverview.ErrIntegrity):
		return err
	case errors.Is(err, currentstore.ErrOwnerActive):
		return controlapp.ErrStoreBusy
	case errors.Is(err, currentstore.ErrStoreClosed):
		return controlapp.ErrStoreUnavailable
	case errors.Is(err, currentstore.ErrAdmissionIntegrity),
		errors.Is(err, currentstore.ErrPublishedBasisIntegrity):
		return controlapp.ErrIntegrityFailure
	default:
		return controlapp.ErrStoreUnavailable
	}
}

var newControlOverviewServiceV1 = func(
	store *currentstore.Store,
) (controlhttp.OverviewServiceV1, error) {
	return controlapp.NewOverviewServiceV1(
		productionControlOverviewReaderV1{store: store},
	)
}

var _ controlapp.OverviewReaderV1 = productionControlOverviewReaderV1{}
