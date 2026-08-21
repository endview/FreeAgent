package currentstore

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func TestAcquireRunLeaseAllowsExactlyOneOfTwoOwners(t *testing.T) {
	store, runID := newCommittedLeaseFixture(t)
	inputs := []AcquireRunLeaseInput{
		newLeaseAcquireInput(runID, "owner-a", 0, 0),
		newLeaseAcquireInput(runID, "owner-b", 0, 0),
	}

	start := make(chan struct{})
	results := make(chan struct {
		lease RunLease
		err   error
	}, len(inputs))
	var workers sync.WaitGroup
	for _, input := range inputs {
		input := input
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			lease, err := store.AcquireRunLease(context.Background(), input)
			results <- struct {
				lease RunLease
				err   error
			}{lease: lease, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	var acquired RunLease
	var successes, conflicts int
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			acquired = result.lease
		case errors.Is(result.err, ErrRunLeaseConflict):
			conflicts++
		default:
			t.Fatalf("unexpected competing acquire error: %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	if acquired.LeaseEpoch != 1 ||
		acquired.RunRevision != 0 ||
		acquired.FrameRevision != 1 ||
		acquired.ExpiresAt.IsZero() {
		t.Fatalf("acquired lease=%+v", acquired)
	}
}

func TestAcquireRunLeaseRejectsUnexpiredOwner(t *testing.T) {
	store, runID := newCommittedLeaseFixture(t)
	first, err := store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(runID, "owner-a", 0, 0),
	)
	if err != nil {
		t.Fatalf("first AcquireRunLease: %v", err)
	}

	_, err = store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(
			runID,
			"owner-b",
			first.RunRevision,
			first.FrameRevision,
		),
	)
	if !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("unexpired competing owner error=%v", err)
	}
	assertStoredLease(
		t,
		store,
		runID,
		first.RunRevision,
		first.FrameRevision,
		first.OwnerID,
		first.LeaseEpoch,
		true,
	)
}

func TestAcquireRunLeaseTakesOverExpiredLeaseWithMonotonicEpoch(
	t *testing.T,
) {
	store, runID := newCommittedLeaseFixture(t)
	first, err := store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(runID, "owner-a", 0, 0),
	)
	if err != nil {
		t.Fatalf("first AcquireRunLease: %v", err)
	}
	expireStoredLease(t, store, runID)

	second, err := store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(
			runID,
			"owner-b",
			first.RunRevision,
			first.FrameRevision,
		),
	)
	if err != nil {
		t.Fatalf("expired takeover: %v", err)
	}
	if second.OwnerID != "owner-b" ||
		second.LeaseEpoch != first.LeaseEpoch+1 ||
		second.RunRevision != first.RunRevision ||
		second.FrameRevision != first.FrameRevision+1 {
		t.Fatalf("takeover=%+v after first=%+v", second, first)
	}
}

func TestRenewAndReleaseRequireCurrentExactToken(t *testing.T) {
	store, runID := newCommittedLeaseFixture(t)
	first, err := store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(runID, "owner-a", 0, 0),
	)
	if err != nil {
		t.Fatalf("AcquireRunLease: %v", err)
	}
	renewed, err := store.RenewRunLease(
		context.Background(),
		RenewRunLeaseInput{Lease: first, TTL: time.Minute},
	)
	if err != nil {
		t.Fatalf("RenewRunLease: %v", err)
	}
	if renewed.LeaseEpoch != first.LeaseEpoch ||
		renewed.RunRevision != first.RunRevision ||
		renewed.FrameRevision != first.FrameRevision+1 {
		t.Fatalf("renewed=%+v first=%+v", renewed, first)
	}

	if _, err := store.RenewRunLease(
		context.Background(),
		RenewRunLeaseInput{Lease: first, TTL: time.Minute},
	); !errors.Is(err, ErrRunLeaseConflict) {
		t.Fatalf("stale renewal error=%v", err)
	}
	if err := store.ReleaseRunLease(
		context.Background(),
		first,
	); !errors.Is(err, ErrRunLeaseConflict) {
		t.Fatalf("stale release error=%v", err)
	}
	staleRunRevision := renewed
	staleRunRevision.RunRevision++
	if _, err := store.RenewRunLease(
		context.Background(),
		RenewRunLeaseInput{
			Lease: staleRunRevision,
			TTL:   time.Minute,
		},
	); !errors.Is(err, ErrRunLeaseConflict) {
		t.Fatalf("stale Run revision renewal error=%v", err)
	}
	if err := store.ReleaseRunLease(
		context.Background(),
		staleRunRevision,
	); !errors.Is(err, ErrRunLeaseConflict) {
		t.Fatalf("stale Run revision release error=%v", err)
	}
	if err := store.ReleaseRunLease(
		context.Background(),
		renewed,
	); err != nil {
		t.Fatalf("ReleaseRunLease: %v", err)
	}
	assertStoredLease(
		t,
		store,
		runID,
		renewed.RunRevision,
		renewed.FrameRevision+1,
		"",
		renewed.LeaseEpoch,
		false,
	)
	if err := store.ReleaseRunLease(
		context.Background(),
		renewed,
	); !errors.Is(err, ErrRunLeaseConflict) {
		t.Fatalf("duplicate release error=%v", err)
	}
	reacquired, err := store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(
			runID,
			"owner-b",
			renewed.RunRevision,
			renewed.FrameRevision+1,
		),
	)
	if err != nil {
		t.Fatalf("AcquireRunLease after release: %v", err)
	}
	if reacquired.LeaseEpoch != renewed.LeaseEpoch+1 {
		t.Fatalf(
			"reacquired epoch=%d want %d",
			reacquired.LeaseEpoch,
			renewed.LeaseEpoch+1,
		)
	}
}

func TestRenewRunLeaseRejectsExpiredExactToken(t *testing.T) {
	store, runID := newCommittedLeaseFixture(t)
	lease, err := store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(runID, "owner", 0, 0),
	)
	if err != nil {
		t.Fatalf("AcquireRunLease: %v", err)
	}
	expireStoredLease(t, store, runID)
	if _, err := store.RenewRunLease(
		context.Background(),
		RenewRunLeaseInput{Lease: lease, TTL: time.Minute},
	); !errors.Is(err, ErrRunLeaseUnavailable) {
		t.Fatalf("expired renewal error=%v", err)
	}
	if err := store.ReleaseRunLease(
		context.Background(),
		lease,
	); err != nil {
		t.Fatalf("release exact expired token: %v", err)
	}
	assertStoredLease(
		t,
		store,
		runID,
		lease.RunRevision,
		lease.FrameRevision+1,
		"",
		lease.LeaseEpoch,
		false,
	)
}

func TestExpiredOldOwnerCannotArriveAfterTakeover(t *testing.T) {
	store, runID := newCommittedLeaseFixture(t)
	oldLease, err := store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(runID, "owner-old", 0, 0),
	)
	if err != nil {
		t.Fatalf("old owner AcquireRunLease: %v", err)
	}
	expireStoredLease(t, store, runID)
	currentLease, err := store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(
			runID,
			"owner-current",
			oldLease.RunRevision,
			oldLease.FrameRevision,
		),
	)
	if err != nil {
		t.Fatalf("takeover AcquireRunLease: %v", err)
	}

	if _, err := store.RenewRunLease(
		context.Background(),
		RenewRunLeaseInput{Lease: oldLease, TTL: time.Minute},
	); !errors.Is(err, ErrRunLeaseConflict) {
		t.Fatalf("late old-owner renewal error=%v", err)
	}
	if err := store.ReleaseRunLease(
		context.Background(),
		oldLease,
	); !errors.Is(err, ErrRunLeaseConflict) {
		t.Fatalf("late old-owner release error=%v", err)
	}
	assertStoredLease(
		t,
		store,
		runID,
		currentLease.RunRevision,
		currentLease.FrameRevision,
		currentLease.OwnerID,
		currentLease.LeaseEpoch,
		true,
	)
}

func TestRunLeaseClassifiesInvalidUnavailableConflictAndIntegrity(
	t *testing.T,
) {
	t.Run("invalid", func(t *testing.T) {
		store, runID := newCommittedLeaseFixture(t)
		input := newLeaseAcquireInput(runID, "owner", 0, 0)
		input.ExpectedRunRevision = uint64(math.MaxInt64) + 1
		if _, err := store.AcquireRunLease(
			context.Background(),
			input,
		); !errors.Is(err, ErrInvalidRunLease) {
			t.Fatalf("SQLite INTEGER overflow error=%v", err)
		}
		input = newLeaseAcquireInput(runID, "owner", 0, 0)
		input.TTL = 0
		if _, err := store.AcquireRunLease(
			context.Background(),
			input,
		); !errors.Is(err, ErrInvalidRunLease) {
			t.Fatalf("zero TTL error=%v", err)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		store, _ := newCommittedLeaseFixture(t)
		if _, err := store.AcquireRunLease(
			context.Background(),
			newLeaseAcquireInput("missing-run", "owner", 0, 0),
		); !errors.Is(err, ErrRunLeaseUnavailable) {
			t.Fatalf("missing Run error=%v", err)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		store, runID := newCommittedLeaseFixture(t)
		input := newLeaseAcquireInput(runID, "owner", 1, 0)
		if _, err := store.AcquireRunLease(
			context.Background(),
			input,
		); !errors.Is(err, ErrRunLeaseConflict) {
			t.Fatalf("stale Run revision error=%v", err)
		}
	})

	t.Run("integrity", func(t *testing.T) {
		store, runID := newCommittedLeaseFixture(t)
		if _, err := store.db.Exec(
			`DELETE FROM loop_frames WHERE run_id=?`,
			runID,
		); err != nil {
			t.Fatalf("delete LoopFrame: %v", err)
		}
		if _, err := store.AcquireRunLease(
			context.Background(),
			newLeaseAcquireInput(runID, "owner", 0, 0),
		); !errors.Is(err, ErrRunLeaseIntegrity) {
			t.Fatalf("missing LoopFrame error=%v", err)
		}
	})
}

func TestRunLeasePersistsSQLiteIntegersAndRejectsEpochOverflow(t *testing.T) {
	store, runID := newCommittedLeaseFixture(t)
	lease, err := store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(runID, "owner-a", 0, 0),
	)
	if err != nil {
		t.Fatalf("AcquireRunLease: %v", err)
	}
	var frameType, epochType, expiryType string
	if err := store.db.QueryRow(`
		SELECT
			typeof(frame_revision),
			typeof(lease_epoch),
			typeof(lease_expiry)
		FROM loop_frames
		WHERE run_id=?
	`, runID).Scan(&frameType, &epochType, &expiryType); err != nil {
		t.Fatalf("read SQLite storage classes: %v", err)
	}
	if frameType != "integer" ||
		epochType != "integer" ||
		expiryType != "integer" {
		t.Fatalf(
			"SQLite storage classes frame=%q epoch=%q expiry=%q",
			frameType,
			epochType,
			expiryType,
		)
	}

	if _, err := store.db.Exec(`
		UPDATE loop_frames
		SET lease_epoch=?, lease_expiry=?
		WHERE run_id=?
	`, int64(math.MaxInt64), nowUnixMicro()-1, runID); err != nil {
		t.Fatalf("seed maximum epoch: %v", err)
	}
	_, err = store.AcquireRunLease(
		context.Background(),
		newLeaseAcquireInput(
			runID,
			"owner-b",
			lease.RunRevision,
			lease.FrameRevision,
		),
	)
	if !errors.Is(err, ErrRunLeaseIntegrity) {
		t.Fatalf("maximum persisted epoch error=%v", err)
	}
	assertStoredLease(
		t,
		store,
		runID,
		lease.RunRevision,
		lease.FrameRevision,
		lease.OwnerID,
		uint64(math.MaxInt64),
		true,
	)
}

func newCommittedLeaseFixture(t *testing.T) (*Store, string) {
	t.Helper()
	fixture := newAdmissionCommitFixture(t)
	result, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("CommitRunAdmission: %v", err)
	}
	return fixture.store, result.RunID
}

func newLeaseAcquireInput(
	runID string,
	ownerID string,
	runRevision uint64,
	frameRevision uint64,
) AcquireRunLeaseInput {
	return AcquireRunLeaseInput{
		RunID:                 runID,
		OwnerID:               ownerID,
		ExpectedRunRevision:   runRevision,
		ExpectedFrameRevision: frameRevision,
		TTL:                   time.Minute,
	}
}

func expireStoredLease(t *testing.T, store *Store, runID string) {
	t.Helper()
	result, err := store.db.Exec(`
		UPDATE loop_frames
		SET lease_expiry=?
		WHERE run_id=?
	`, nowUnixMicro()-1, runID)
	if err != nil {
		t.Fatalf("expire stored lease: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("inspect expired lease update: %v", err)
	}
	if affected != 1 {
		t.Fatalf("expired lease update affected %d rows", affected)
	}
}

func assertStoredLease(
	t *testing.T,
	store *Store,
	runID string,
	wantRunRevision uint64,
	wantFrameRevision uint64,
	wantOwner string,
	wantEpoch uint64,
	wantExpiry bool,
) {
	t.Helper()
	var (
		runRevision   int64
		frameRevision int64
		owner         *string
		epoch         int64
		expiry        *int64
	)
	if err := store.db.QueryRow(`
		SELECT
			runs.revision,
			loop_frames.frame_revision,
			loop_frames.lease_owner,
			loop_frames.lease_epoch,
			loop_frames.lease_expiry
		FROM runs
		JOIN loop_frames ON loop_frames.run_id=runs.run_id
		WHERE runs.run_id=?
	`, runID).Scan(
		&runRevision,
		&frameRevision,
		&owner,
		&epoch,
		&expiry,
	); err != nil {
		t.Fatalf("read stored lease: %v", err)
	}
	if uint64(runRevision) != wantRunRevision ||
		uint64(frameRevision) != wantFrameRevision ||
		uint64(epoch) != wantEpoch {
		t.Fatalf(
			"stored revisions/epoch=%d/%d/%d want %d/%d/%d",
			runRevision,
			frameRevision,
			epoch,
			wantRunRevision,
			wantFrameRevision,
			wantEpoch,
		)
	}
	if wantOwner == "" {
		if owner != nil {
			t.Fatalf("stored owner=%q want NULL", *owner)
		}
	} else if owner == nil || *owner != wantOwner {
		t.Fatalf("stored owner=%v want %q", owner, wantOwner)
	}
	if (expiry != nil) != wantExpiry {
		t.Fatalf("stored expiry=%v want present=%v", expiry, wantExpiry)
	}
}
