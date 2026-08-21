package currentstore

import (
	"context"
	"fmt"
	"math"
	"time"
)

// AcquireCurrentRunLeaseInput asks Current Store to acquire the exact Run and
// Frame head that is current inside the acquisition transaction. It is the
// restart-safe counterpart of AcquireRunLease for callers that know only the
// immutable Run identity.
type AcquireCurrentRunLeaseInput struct {
	RunID   string
	OwnerID string
	TTL     time.Duration
}

// AcquireCurrentRunLease atomically reads the current Run/Frame head and
// acquires an empty or expired lease for that exact head. It leaves the Run
// revision unchanged, increments the Frame revision exactly once, and always
// increments the retained fencing epoch.
func (store *Store) AcquireCurrentRunLease(
	ctx context.Context,
	input AcquireCurrentRunLeaseInput,
) (RunLease, error) {
	if ctx == nil {
		return RunLease{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidRunLease,
		)
	}
	if !validLeaseOpaqueID(input.RunID) {
		return RunLease{}, fmt.Errorf(
			"%w: invalid Run ID",
			ErrInvalidRunLease,
		)
	}
	if !validLeaseOpaqueID(input.OwnerID) {
		return RunLease{}, fmt.Errorf(
			"%w: invalid owner ID",
			ErrInvalidRunLease,
		)
	}
	ttlMicros, err := validateLeaseTTL(input.TTL)
	if err != nil {
		return RunLease{}, err
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return RunLease{}, err
	}
	defer unlock()

	observedAt := nowUnixMicro()
	expiry, err := leaseExpiry(observedAt, ttlMicros)
	if err != nil {
		return RunLease{}, err
	}
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return RunLease{}, fmt.Errorf(
			"currentstore: acquire current Run lease connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return RunLease{}, fmt.Errorf(
			"currentstore: begin AcquireCurrentRunLease: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	current, err := loadStoredRunLease(ctx, connection, input.RunID)
	if err != nil {
		return RunLease{}, err
	}
	if current.owner.Valid && current.expiry.Int64 > observedAt {
		return RunLease{}, fmt.Errorf(
			"%w: Run %q is held by an unexpired owner",
			ErrRunLeaseUnavailable,
			input.RunID,
		)
	}
	if current.epoch == math.MaxInt64 {
		return RunLease{}, fmt.Errorf(
			"%w: Run %q lease epoch cannot advance",
			ErrRunLeaseIntegrity,
			input.RunID,
		)
	}

	result, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET
			frame_revision=frame_revision+1,
			lease_owner=?,
			lease_epoch=lease_epoch+1,
			lease_expiry=?
		WHERE run_id=?
		  AND frame_revision=?
		  AND lease_epoch=?
		  AND (
		      lease_owner IS NULL
		      OR lease_expiry<=?
		  )
		  AND EXISTS(
		      SELECT 1
		      FROM runs
		      WHERE runs.run_id=loop_frames.run_id
		        AND runs.revision=?
		  )
	`,
		input.OwnerID,
		expiry,
		input.RunID,
		current.frameRevision,
		current.epoch,
		observedAt,
		current.runRevision,
	)
	if err != nil {
		return RunLease{}, fmt.Errorf(
			"currentstore: acquire current Run lease: %w",
			err,
		)
	}
	if err := requireLeaseCASRow(result, "acquire current"); err != nil {
		return RunLease{}, err
	}
	acquired, err := newRunLease(
		input.RunID,
		input.OwnerID,
		current.epoch+1,
		current.runRevision,
		current.frameRevision+1,
		expiry,
	)
	if err != nil {
		return RunLease{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return RunLease{}, fmt.Errorf(
			"currentstore: commit AcquireCurrentRunLease: %w",
			err,
		)
	}
	committed = true
	return acquired, nil
}
