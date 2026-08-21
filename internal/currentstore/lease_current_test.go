package currentstore

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func TestAcquireCurrentRunLeaseReadsAndAdvancesCurrentHead(t *testing.T) {
	store, runID := newCommittedLeaseFixture(t)
	if _, err := store.db.Exec(`
		UPDATE loop_frames
		SET frame_revision=11
		WHERE run_id=?
	`, runID); err != nil {
		t.Fatalf("seed current Frame revision: %v", err)
	}

	lease, err := store.AcquireCurrentRunLease(
		context.Background(),
		AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "owner-current",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("AcquireCurrentRunLease: %v", err)
	}
	if lease.RunID != runID ||
		lease.OwnerID != "owner-current" ||
		lease.RunRevision != 0 ||
		lease.FrameRevision != 12 ||
		lease.LeaseEpoch != 1 ||
		lease.ExpiresAt.IsZero() {
		t.Fatalf("acquired lease=%+v", lease)
	}
	assertStoredLease(
		t,
		store,
		runID,
		0,
		12,
		"owner-current",
		1,
		true,
	)
}

func TestAcquireCurrentRunLeaseAllowsExactlyOneConcurrentOwner(
	t *testing.T,
) {
	store, runID := newCommittedLeaseFixture(t)
	owners := []string{"owner-a", "owner-b"}
	start := make(chan struct{})
	results := make(chan struct {
		lease RunLease
		err   error
	}, len(owners))

	var workers sync.WaitGroup
	for _, owner := range owners {
		owner := owner
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			lease, err := store.AcquireCurrentRunLease(
				context.Background(),
				AcquireCurrentRunLeaseInput{
					RunID:   runID,
					OwnerID: owner,
					TTL:     time.Minute,
				},
			)
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
	var successes, unavailable int
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			acquired = result.lease
		case errors.Is(result.err, ErrRunLeaseUnavailable):
			unavailable++
		default:
			t.Fatalf("unexpected competing acquire error: %v", result.err)
		}
	}
	if successes != 1 || unavailable != 1 {
		t.Fatalf(
			"successes=%d unavailable=%d",
			successes,
			unavailable,
		)
	}
	if acquired.FrameRevision != 1 || acquired.LeaseEpoch != 1 {
		t.Fatalf("acquired lease=%+v", acquired)
	}
}

func TestAcquireCurrentRunLeaseTakesOverExpiredHeadAndFencesOldOwner(
	t *testing.T,
) {
	store, runID := newCommittedLeaseFixture(t)
	first, err := store.AcquireCurrentRunLease(
		context.Background(),
		AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "owner-old",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("first AcquireCurrentRunLease: %v", err)
	}
	expireStoredLease(t, store, runID)

	second, err := store.AcquireCurrentRunLease(
		context.Background(),
		AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "owner-new",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("expired takeover: %v", err)
	}
	if second.RunRevision != first.RunRevision ||
		second.FrameRevision != first.FrameRevision+1 ||
		second.LeaseEpoch != first.LeaseEpoch+1 ||
		second.OwnerID != "owner-new" {
		t.Fatalf("second=%+v first=%+v", second, first)
	}
	if err := store.ReleaseRunLease(
		context.Background(),
		first,
	); !errors.Is(err, ErrRunLeaseConflict) {
		t.Fatalf("old owner release error=%v", err)
	}
}

func TestAcquireCurrentRunLeaseClassifiesInvalidUnavailableAndIntegrity(
	t *testing.T,
) {
	t.Run("invalid", func(t *testing.T) {
		store, runID := newCommittedLeaseFixture(t)
		valid := AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "owner",
			TTL:     time.Minute,
		}
		if _, err := store.AcquireCurrentRunLease(
			nil,
			valid,
		); !errors.Is(err, ErrInvalidRunLease) {
			t.Fatalf("nil context error=%v", err)
		}
		invalidOwner := valid
		invalidOwner.OwnerID = " "
		if _, err := store.AcquireCurrentRunLease(
			context.Background(),
			invalidOwner,
		); !errors.Is(err, ErrInvalidRunLease) {
			t.Fatalf("invalid owner error=%v", err)
		}
		invalidTTL := valid
		invalidTTL.TTL = 0
		if _, err := store.AcquireCurrentRunLease(
			context.Background(),
			invalidTTL,
		); !errors.Is(err, ErrInvalidRunLease) {
			t.Fatalf("zero TTL error=%v", err)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		store, _ := newCommittedLeaseFixture(t)
		if _, err := store.AcquireCurrentRunLease(
			context.Background(),
			AcquireCurrentRunLeaseInput{
				RunID:   "missing-run",
				OwnerID: "owner",
				TTL:     time.Minute,
			},
		); !errors.Is(err, ErrRunLeaseUnavailable) {
			t.Fatalf("missing Run error=%v", err)
		}
	})

	t.Run("missing frame integrity", func(t *testing.T) {
		store, runID := newCommittedLeaseFixture(t)
		if _, err := store.db.Exec(
			`DELETE FROM loop_frames WHERE run_id=?`,
			runID,
		); err != nil {
			t.Fatalf("delete LoopFrame: %v", err)
		}
		if _, err := store.AcquireCurrentRunLease(
			context.Background(),
			AcquireCurrentRunLeaseInput{
				RunID:   runID,
				OwnerID: "owner",
				TTL:     time.Minute,
			},
		); !errors.Is(err, ErrRunLeaseIntegrity) {
			t.Fatalf("missing LoopFrame error=%v", err)
		}
	})

	t.Run("epoch overflow integrity", func(t *testing.T) {
		store, runID := newCommittedLeaseFixture(t)
		if _, err := store.db.Exec(`
			UPDATE loop_frames
			SET
				lease_owner='expired-owner',
				lease_epoch=?,
				lease_expiry=?
			WHERE run_id=?
		`, int64(math.MaxInt64), nowUnixMicro()-1, runID); err != nil {
			t.Fatalf("seed maximum epoch: %v", err)
		}
		if _, err := store.AcquireCurrentRunLease(
			context.Background(),
			AcquireCurrentRunLeaseInput{
				RunID:   runID,
				OwnerID: "owner",
				TTL:     time.Minute,
			},
		); !errors.Is(err, ErrRunLeaseIntegrity) {
			t.Fatalf("maximum epoch error=%v", err)
		}
		assertStoredLease(
			t,
			store,
			runID,
			0,
			0,
			"expired-owner",
			uint64(math.MaxInt64),
			true,
		)
	})
}
