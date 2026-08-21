package currentstore

import (
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

var (
	// ErrInvalidRunLease identifies a malformed lease request or fencing token.
	ErrInvalidRunLease = errors.New("currentstore: invalid Run lease")

	// ErrRunLeaseUnavailable identifies a Run that does not exist, a lease
	// currently held by an unexpired owner, or an expired lease that cannot be
	// renewed.
	ErrRunLeaseUnavailable = errors.New("currentstore: Run lease unavailable")

	// ErrRunLeaseConflict identifies a stale Run/Frame revision, owner, or
	// fencing epoch.
	ErrRunLeaseConflict = errors.New("currentstore: Run lease conflict")

	// ErrRunLeaseIntegrity identifies a missing or impossible persisted lease
	// closure.
	ErrRunLeaseIntegrity = errors.New(
		"currentstore: Run lease integrity violation",
	)
)

// AcquireRunLeaseInput is one lease-only Frame CAS. TTL is measured from the
// Store's current wall clock; wall time controls takeover eligibility, while
// LeaseEpoch provides the fencing guarantee.
type AcquireRunLeaseInput struct {
	RunID                 string
	OwnerID               string
	ExpectedRunRevision   uint64
	ExpectedFrameRevision uint64
	TTL                   time.Duration
}

// RunLease is the exact fencing token returned by AcquireRunLease and
// RenewRunLease. ExpiresAt is informational; owner, epoch, and both revisions
// form the token checked by later writes.
type RunLease struct {
	RunID         string
	OwnerID       string
	LeaseEpoch    uint64
	RunRevision   uint64
	FrameRevision uint64
	ExpiresAt     time.Time
}

// RenewRunLeaseInput extends one exact, unexpired fencing token.
type RenewRunLeaseInput struct {
	Lease RunLease
	TTL   time.Duration
}

type storedRunLease struct {
	runRevision   int64
	frameRevision int64
	owner         sql.NullString
	epoch         int64
	expiry        sql.NullInt64
}

// AcquireRunLease acquires an empty or expired Run lease. It leaves the Run
// revision unchanged, increments the Frame revision exactly once, and always
// increments the retained fencing epoch.
func (store *Store) AcquireRunLease(
	ctx context.Context,
	input AcquireRunLeaseInput,
) (RunLease, error) {
	if ctx == nil {
		return RunLease{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidRunLease,
		)
	}
	if err := validateAcquireRunLeaseInput(input); err != nil {
		return RunLease{}, err
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
			"currentstore: acquire Run lease connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return RunLease{}, fmt.Errorf(
			"currentstore: begin AcquireRunLease: %w",
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
	if err := current.requireRevisions(
		input.ExpectedRunRevision,
		input.ExpectedFrameRevision,
	); err != nil {
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
		int64(input.ExpectedFrameRevision),
		current.epoch,
		observedAt,
		int64(input.ExpectedRunRevision),
	)
	if err != nil {
		return RunLease{}, fmt.Errorf(
			"currentstore: acquire Run lease: %w",
			err,
		)
	}
	if err := requireLeaseCASRow(result, "acquire"); err != nil {
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
			"currentstore: commit AcquireRunLease: %w",
			err,
		)
	}
	committed = true

	return acquired, nil
}

// RenewRunLease renews only the exact unexpired lease token. It changes no Run
// lifecycle state and advances only frame_revision.
func (store *Store) RenewRunLease(
	ctx context.Context,
	input RenewRunLeaseInput,
) (RunLease, error) {
	if ctx == nil {
		return RunLease{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidRunLease,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return RunLease{}, err
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
			"currentstore: acquire Run lease renewal connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return RunLease{}, fmt.Errorf(
			"currentstore: begin RenewRunLease: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	lease := input.Lease
	current, err := loadStoredRunLease(ctx, connection, lease.RunID)
	if err != nil {
		return RunLease{}, err
	}
	if err := current.requireRevisions(
		lease.RunRevision,
		lease.FrameRevision,
	); err != nil {
		return RunLease{}, err
	}
	if !current.owner.Valid ||
		current.owner.String != lease.OwnerID ||
		uint64(current.epoch) != lease.LeaseEpoch {
		return RunLease{}, fmt.Errorf(
			"%w: Run %q owner or epoch is stale",
			ErrRunLeaseConflict,
			lease.RunID,
		)
	}
	if current.expiry.Int64 <= observedAt {
		return RunLease{}, fmt.Errorf(
			"%w: Run %q lease has expired",
			ErrRunLeaseUnavailable,
			lease.RunID,
		)
	}

	result, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET
			frame_revision=frame_revision+1,
			lease_expiry=?
		WHERE run_id=?
		  AND frame_revision=?
		  AND lease_owner=?
		  AND lease_epoch=?
		  AND lease_expiry>?
		  AND EXISTS(
		      SELECT 1
		      FROM runs
		      WHERE runs.run_id=loop_frames.run_id
		        AND runs.revision=?
		  )
	`,
		expiry,
		lease.RunID,
		int64(lease.FrameRevision),
		lease.OwnerID,
		int64(lease.LeaseEpoch),
		observedAt,
		int64(lease.RunRevision),
	)
	if err != nil {
		return RunLease{}, fmt.Errorf(
			"currentstore: renew Run lease: %w",
			err,
		)
	}
	if err := requireLeaseCASRow(result, "renew"); err != nil {
		return RunLease{}, err
	}
	renewed, err := newRunLease(
		lease.RunID,
		lease.OwnerID,
		current.epoch,
		current.runRevision,
		current.frameRevision+1,
		expiry,
	)
	if err != nil {
		return RunLease{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return RunLease{}, fmt.Errorf(
			"currentstore: commit RenewRunLease: %w",
			err,
		)
	}
	committed = true

	return renewed, nil
}

// ReleaseRunLease clears owner and expiry for one exact fencing token. The
// last lease epoch is deliberately retained and frame_revision advances once.
// An expired token may release only while it is still the exact persisted
// token; a takeover makes the old owner stale.
func (store *Store) ReleaseRunLease(
	ctx context.Context,
	lease RunLease,
) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidRunLease)
	}
	if err := validateRunLeaseToken(lease); err != nil {
		return err
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf(
			"currentstore: acquire Run lease release connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf(
			"currentstore: begin ReleaseRunLease: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	current, err := loadStoredRunLease(ctx, connection, lease.RunID)
	if err != nil {
		return err
	}
	if err := current.requireRevisions(
		lease.RunRevision,
		lease.FrameRevision,
	); err != nil {
		return err
	}
	if !current.owner.Valid ||
		current.owner.String != lease.OwnerID ||
		uint64(current.epoch) != lease.LeaseEpoch {
		return fmt.Errorf(
			"%w: Run %q owner or epoch is stale",
			ErrRunLeaseConflict,
			lease.RunID,
		)
	}

	result, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET
			frame_revision=frame_revision+1,
			lease_owner=NULL,
			lease_expiry=NULL
		WHERE run_id=?
		  AND frame_revision=?
		  AND lease_owner=?
		  AND lease_epoch=?
		  AND EXISTS(
		      SELECT 1
		      FROM runs
		      WHERE runs.run_id=loop_frames.run_id
		        AND runs.revision=?
		  )
	`,
		lease.RunID,
		int64(lease.FrameRevision),
		lease.OwnerID,
		int64(lease.LeaseEpoch),
		int64(lease.RunRevision),
	)
	if err != nil {
		return fmt.Errorf("currentstore: release Run lease: %w", err)
	}
	if err := requireLeaseCASRow(result, "release"); err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf(
			"currentstore: commit ReleaseRunLease: %w",
			err,
		)
	}
	committed = true
	return nil
}

func loadStoredRunLease(
	ctx context.Context,
	connection *sql.Conn,
	runID string,
) (storedRunLease, error) {
	var (
		current       storedRunLease
		frameRevision sql.NullInt64
		epoch         sql.NullInt64
	)
	err := connection.QueryRowContext(ctx, `
		SELECT
			runs.revision,
			loop_frames.frame_revision,
			loop_frames.lease_owner,
			loop_frames.lease_epoch,
			loop_frames.lease_expiry
		FROM runs
		LEFT JOIN loop_frames ON loop_frames.run_id=runs.run_id
		WHERE runs.run_id=?
	`, runID).Scan(
		&current.runRevision,
		&frameRevision,
		&current.owner,
		&epoch,
		&current.expiry,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return storedRunLease{}, fmt.Errorf(
			"%w: Run %q does not exist",
			ErrRunLeaseUnavailable,
			runID,
		)
	}
	if err != nil {
		return storedRunLease{}, fmt.Errorf(
			"currentstore: read Run lease: %w",
			err,
		)
	}
	if !frameRevision.Valid || !epoch.Valid {
		return storedRunLease{}, fmt.Errorf(
			"%w: Run %q has no LoopFrame",
			ErrRunLeaseIntegrity,
			runID,
		)
	}
	current.frameRevision = frameRevision.Int64
	current.epoch = epoch.Int64
	if err := current.validateStored(runID); err != nil {
		return storedRunLease{}, err
	}
	return current, nil
}

func (current storedRunLease) validateStored(runID string) error {
	if current.runRevision < 0 ||
		current.frameRevision < 0 ||
		current.epoch < 0 ||
		current.frameRevision == math.MaxInt64 {
		return fmt.Errorf(
			"%w: Run %q has an impossible revision or epoch",
			ErrRunLeaseIntegrity,
			runID,
		)
	}
	if current.owner.Valid != current.expiry.Valid {
		return fmt.Errorf(
			"%w: Run %q has a partial lease",
			ErrRunLeaseIntegrity,
			runID,
		)
	}
	if !current.owner.Valid {
		return nil
	}
	if !validLeaseOpaqueID(current.owner.String) ||
		current.epoch <= 0 ||
		current.expiry.Int64 <= 0 {
		return fmt.Errorf(
			"%w: Run %q has an invalid persisted lease",
			ErrRunLeaseIntegrity,
			runID,
		)
	}
	return nil
}

func (current storedRunLease) requireRevisions(
	expectedRunRevision uint64,
	expectedFrameRevision uint64,
) error {
	if uint64(current.runRevision) != expectedRunRevision ||
		uint64(current.frameRevision) != expectedFrameRevision {
		return fmt.Errorf(
			"%w: expected Run/Frame revisions %d/%d, found %d/%d",
			ErrRunLeaseConflict,
			expectedRunRevision,
			expectedFrameRevision,
			current.runRevision,
			current.frameRevision,
		)
	}
	return nil
}

func validateAcquireRunLeaseInput(input AcquireRunLeaseInput) error {
	if !validLeaseOpaqueID(input.RunID) {
		return fmt.Errorf("%w: invalid Run ID", ErrInvalidRunLease)
	}
	if !validLeaseOpaqueID(input.OwnerID) {
		return fmt.Errorf("%w: invalid owner ID", ErrInvalidRunLease)
	}
	if input.ExpectedRunRevision > math.MaxInt64 {
		return fmt.Errorf(
			"%w: Run revision exceeds SQLite INTEGER",
			ErrInvalidRunLease,
		)
	}
	if input.ExpectedFrameRevision >= math.MaxInt64 {
		return fmt.Errorf(
			"%w: Frame revision cannot advance in SQLite INTEGER",
			ErrInvalidRunLease,
		)
	}
	return nil
}

func validateRunLeaseToken(lease RunLease) error {
	if !validLeaseOpaqueID(lease.RunID) {
		return fmt.Errorf("%w: invalid Run ID", ErrInvalidRunLease)
	}
	if !validLeaseOpaqueID(lease.OwnerID) {
		return fmt.Errorf("%w: invalid owner ID", ErrInvalidRunLease)
	}
	if lease.LeaseEpoch == 0 || lease.LeaseEpoch > math.MaxInt64 {
		return fmt.Errorf(
			"%w: lease epoch is outside SQLite INTEGER",
			ErrInvalidRunLease,
		)
	}
	if lease.RunRevision > math.MaxInt64 {
		return fmt.Errorf(
			"%w: Run revision exceeds SQLite INTEGER",
			ErrInvalidRunLease,
		)
	}
	if lease.FrameRevision >= math.MaxInt64 {
		return fmt.Errorf(
			"%w: Frame revision cannot advance in SQLite INTEGER",
			ErrInvalidRunLease,
		)
	}
	return nil
}

func validateLeaseTTL(ttl time.Duration) (int64, error) {
	micros := ttl.Microseconds()
	if micros <= 0 {
		return 0, fmt.Errorf(
			"%w: TTL must be at least one microsecond",
			ErrInvalidRunLease,
		)
	}
	return micros, nil
}

func leaseExpiry(observedAt int64, ttlMicros int64) (int64, error) {
	if observedAt <= 0 {
		return 0, fmt.Errorf(
			"%w: wall clock is outside the persisted timestamp domain",
			ErrRunLeaseIntegrity,
		)
	}
	if ttlMicros <= 0 || observedAt > math.MaxInt64-ttlMicros {
		return 0, fmt.Errorf(
			"%w: lease expiry exceeds SQLite INTEGER",
			ErrInvalidRunLease,
		)
	}
	return observedAt + ttlMicros, nil
}

func validLeaseOpaqueID(value string) bool {
	if value == "" ||
		len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) ||
		value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func requireLeaseCASRow(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect Run lease %s: %w",
			operation,
			err,
		)
	}
	if affected == 0 {
		return fmt.Errorf(
			"%w: Run lease %s lost its CAS",
			ErrRunLeaseConflict,
			operation,
		)
	}
	if affected != 1 {
		return fmt.Errorf(
			"%w: Run lease %s affected %d rows",
			ErrRunLeaseIntegrity,
			operation,
			affected,
		)
	}
	return nil
}

func newRunLease(
	runID string,
	ownerID string,
	epoch int64,
	runRevision int64,
	frameRevision int64,
	expiry int64,
) (RunLease, error) {
	expiresAt, err := timeFromUnixMicro(expiry)
	if err != nil {
		return RunLease{}, fmt.Errorf(
			"%w: persisted expiry: %v",
			ErrRunLeaseIntegrity,
			err,
		)
	}
	return RunLease{
		RunID:         runID,
		OwnerID:       ownerID,
		LeaseEpoch:    uint64(epoch),
		RunRevision:   uint64(runRevision),
		FrameRevision: uint64(frameRevision),
		ExpiresAt:     expiresAt,
	}, nil
}
