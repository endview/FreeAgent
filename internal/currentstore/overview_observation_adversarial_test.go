package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestOverviewObservationSemanticGateRejectsSelfResignedResourceGenesisTailCollapse(
	t *testing.T,
) {
	harness := newActionStoreHarness(t)
	begin := harness.beginAction(t, "action-observation-tail-collapse")
	terminal, err := harness.store.CommitActionDispatchOutcome(
		context.Background(),
		actionOutcomeInput(begin.Action, begin.Lease, moduleapi.ActionExecutionFailed),
	)
	if err != nil {
		t.Fatal(err)
	}
	resourceID := terminal.Record.Attempt.AttemptID
	record, err := loadOverviewResourceHeadOnlineV1(
		context.Background(), harness.store.db, overviewResourceActionV1, resourceID,
	)
	if err != nil {
		t.Fatal(err)
	}
	oldDigest := record.Digest
	carrier, err := observationRunRefByTransitionForTestV1(
		context.Background(), harness.store.db, terminal.Record.Attempt.RunID,
		runObservationActionBeginV1,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Model a coordinated rollback of both the raw observation counter and the
	// resource ledger tail.  All digests, refs, head projections and FKs are
	// made mutually consistent; only the impossible terminal-as-BEGIN lifecycle
	// remains.  ignore_check_constraints lets this test reach the Go replay gate
	// instead of stopping at the redundant SQLite CHECK first.
	record.Snapshot.ObservationSequence = 1
	record.Snapshot.PreviousSnapshotDigest = ""
	record.Snapshot.TransitionKind = overviewTransitionActionBeginV1
	record.Snapshot.SubjectRun = cloneOverviewRunRefForTestV1(carrier)
	record.Snapshot.CausalRun = cloneOverviewRunRefForTestV1(carrier)
	record.Canonical, record.Digest, err = canonicalOverviewResourceV1(record.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	coordinatedObservationTamperForTestV1(t, harness.store.db, true, []string{
		"dispatch_attempts_observation_update_guard",
		"overview_resource_snapshots_reject_delete",
		"overview_resource_snapshots_reject_update",
		"overview_resource_heads_validate_update",
		"overview_resource_transition_carriers_reject_delete",
	}, func(ctx context.Context, connection *sql.Conn) error {
		if _, err := connection.ExecContext(ctx, `DELETE FROM overview_resource_transition_carriers
			WHERE resource_kind='ACTION' AND resource_id=?`, resourceID); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE dispatch_attempts
			SET overview_observation_sequence=1
			WHERE dispatch_kind='ACTION' AND attempt_id=?`, resourceID); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `DELETE FROM overview_resource_snapshots
			WHERE resource_kind='ACTION' AND resource_id=? AND observation_sequence=1`,
			resourceID); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE overview_resource_snapshots SET
			snapshot_digest=?,observation_sequence=1,previous_snapshot_digest=NULL,
			transition_kind='ACTION_BEGIN',subject_run_id=?,
			subject_run_observation_sequence=?,subject_run_observation_digest=?,
			causal_run_id=?,causal_run_observation_sequence=?,
			causal_run_observation_digest=?,canonical_json=?
			WHERE resource_kind='ACTION' AND resource_id=? AND snapshot_digest=?`,
			record.Digest, carrier.RunID, int64(carrier.Sequence), carrier.Digest,
			carrier.RunID, int64(carrier.Sequence), carrier.Digest, record.Canonical,
			resourceID, oldDigest); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE overview_resource_heads
			SET observation_sequence=1,snapshot_digest=?
			WHERE resource_kind='ACTION' AND resource_id=?`, record.Digest, resourceID); err != nil {
			return err
		}
		_, err := connection.ExecContext(ctx, `INSERT INTO overview_resource_transition_carriers(
			resource_kind,resource_id,observation_sequence,snapshot_digest,store_instance_id
		) SELECT 'ACTION',?,1,?,store_instance_id FROM store_meta WHERE singleton=1`,
			resourceID, record.Digest)
		return err
	})
	requireOverviewObservationGateErrorForTestV1(
		t, harness.store, "historical Run dispatch resource projection",
	)
}

func TestOverviewObservationSemanticGateRejectsSelfResignedSameRunCausalRefSwap(
	t *testing.T,
) {
	harness := newActionStoreHarness(t)
	begin := harness.beginAction(t, "action-observation-causal-swap")
	resourceID := begin.Action.Attempt.AttemptID
	record, err := loadOverviewResourceHeadOnlineV1(
		context.Background(), harness.store.db, overviewResourceActionV1, resourceID,
	)
	if err != nil {
		t.Fatal(err)
	}
	oldDigest := record.Digest
	wrongCarrier, err := observationRunRefByTransitionForTestV1(
		context.Background(), harness.store.db, begin.Action.Attempt.RunID,
		runObservationModelBeginV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	record.Snapshot.SubjectRun = cloneOverviewRunRefForTestV1(wrongCarrier)
	record.Snapshot.CausalRun = cloneOverviewRunRefForTestV1(wrongCarrier)
	record.Canonical, record.Digest, err = canonicalOverviewResourceV1(record.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	coordinatedObservationTamperForTestV1(t, harness.store.db, false, []string{
		"overview_resource_snapshots_reject_update",
		"overview_resource_heads_validate_update",
		"overview_resource_transition_carriers_reject_delete",
	}, func(ctx context.Context, connection *sql.Conn) error {
		if _, err := connection.ExecContext(ctx, `DELETE FROM overview_resource_transition_carriers
			WHERE resource_kind='ACTION' AND resource_id=?`, resourceID); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE overview_resource_snapshots SET
			snapshot_digest=?,subject_run_observation_sequence=?,
			subject_run_observation_digest=?,causal_run_observation_sequence=?,
			causal_run_observation_digest=?,canonical_json=?
			WHERE resource_kind='ACTION' AND resource_id=? AND snapshot_digest=?`,
			record.Digest, int64(wrongCarrier.Sequence), wrongCarrier.Digest,
			int64(wrongCarrier.Sequence), wrongCarrier.Digest, record.Canonical,
			resourceID, oldDigest); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE overview_resource_heads
			SET snapshot_digest=? WHERE resource_kind='ACTION' AND resource_id=?`,
			record.Digest, resourceID); err != nil {
			return err
		}
		_, err := connection.ExecContext(ctx, `INSERT INTO overview_resource_transition_carriers(
			resource_kind,resource_id,observation_sequence,snapshot_digest,store_instance_id
		) SELECT 'ACTION',?,1,?,store_instance_id FROM store_meta WHERE singleton=1`,
			resourceID, record.Digest)
		return err
	})
	requireOverviewObservationGateErrorForTestV1(
		t, harness.store, "historical Run dispatch resource cardinality",
	)
}

func TestOverviewObservationSemanticGateRejectsSelfResignedHistoricalModelUsageMatrix(
	t *testing.T,
) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(context.Background(), beginInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(), CommitModelDispatchOutcomeInput{
			Lease: begin.Lease, AttemptID: begin.Attempt.AttemptID,
			InvocationID: begin.Attempt.AttemptID, Provider: begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptFailed,
			ErrorClassification:     "OBSERVATION_USAGE_MATRIX_FIXTURE",
		},
	); err != nil {
		t.Fatal(err)
	}
	resourceID := begin.Attempt.AttemptID
	ancestor, err := observationResourceSnapshotForTestV1(
		context.Background(), fixture.store.db, overviewResourceModelV1, resourceID, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	head, err := loadOverviewResourceHeadOnlineV1(
		context.Background(), fixture.store.db, overviewResourceModelV1, resourceID,
	)
	if err != nil {
		t.Fatal(err)
	}
	oldAncestorDigest, oldHeadDigest := ancestor.Digest, head.Digest
	ancestor.Snapshot.UsageStatus = modelUsageStatusReported
	ancestor.Canonical, ancestor.Digest, err = canonicalOverviewResourceV1(ancestor.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	head.Snapshot.PreviousSnapshotDigest = ancestor.Digest
	head.Canonical, head.Digest, err = canonicalOverviewResourceV1(head.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	coordinatedObservationTamperForTestV1(t, fixture.store.db, true, []string{
		"overview_resource_snapshots_reject_update",
		"overview_resource_heads_validate_update",
		"overview_resource_transition_carriers_reject_delete",
		"overview_resource_transition_carriers_validate_insert",
	}, func(ctx context.Context, connection *sql.Conn) error {
		if _, err := connection.ExecContext(ctx, `DELETE FROM overview_resource_transition_carriers
			WHERE resource_kind='MODEL' AND resource_id=?`, resourceID); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE overview_resource_snapshots SET
			snapshot_digest=?,usage_status=?,canonical_json=?
			WHERE resource_kind='MODEL' AND resource_id=? AND snapshot_digest=?`,
			ancestor.Digest, ancestor.Snapshot.UsageStatus, ancestor.Canonical,
			resourceID, oldAncestorDigest); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE overview_resource_snapshots SET
			snapshot_digest=?,previous_snapshot_digest=?,canonical_json=?
			WHERE resource_kind='MODEL' AND resource_id=? AND snapshot_digest=?`,
			head.Digest, ancestor.Digest, head.Canonical, resourceID, oldHeadDigest); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `UPDATE overview_resource_heads
			SET snapshot_digest=? WHERE resource_kind='MODEL' AND resource_id=?`,
			head.Digest, resourceID); err != nil {
			return err
		}
		if _, err := connection.ExecContext(ctx, `INSERT INTO overview_resource_transition_carriers(
			resource_kind,resource_id,observation_sequence,snapshot_digest,store_instance_id
		) SELECT 'MODEL',?,1,?,store_instance_id FROM store_meta WHERE singleton=1`,
			resourceID, ancestor.Digest); err != nil {
			return err
		}
		_, err := connection.ExecContext(ctx, `INSERT INTO overview_resource_transition_carriers(
			resource_kind,resource_id,observation_sequence,snapshot_digest,store_instance_id
		) SELECT 'MODEL',?,2,?,store_instance_id FROM store_meta WHERE singleton=1`,
			resourceID, head.Digest)
		return err
	})
	requireOverviewObservationGateErrorForTestV1(
		t, fixture.store, "pending Model Usage observation",
	)
}

func observationRunRefByTransitionForTestV1(
	ctx context.Context,
	queryer readQueryerV1,
	runID string,
	transition runObservationTransitionV1,
) (*overviewRunObservationRefV1, error) {
	var sequence int64
	var digest string
	if err := queryer.QueryRowContext(ctx, `SELECT observation_sequence,snapshot_digest
		FROM run_observation_snapshots WHERE run_id=? AND transition_kind=?
		ORDER BY observation_sequence LIMIT 1`, runID, string(transition)).Scan(
		&sequence, &digest,
	); err != nil {
		return nil, err
	}
	return &overviewRunObservationRefV1{
		RunID: runID, Sequence: uint64(sequence), Digest: digest,
	}, nil
}

func observationResourceSnapshotForTestV1(
	ctx context.Context,
	queryer readQueryerV1,
	kind, resourceID string,
	sequence uint64,
) (overviewResourceRecordV1, error) {
	var digest string
	if err := queryer.QueryRowContext(ctx, `SELECT snapshot_digest
		FROM overview_resource_snapshots
		WHERE resource_kind=? AND resource_id=? AND observation_sequence=?`,
		kind, resourceID, int64(sequence)).Scan(&digest); err != nil {
		return overviewResourceRecordV1{}, err
	}
	return loadOverviewResourceSnapshotV1(ctx, queryer, digest)
}

func cloneOverviewRunRefForTestV1(
	ref *overviewRunObservationRefV1,
) *overviewRunObservationRefV1 {
	if ref == nil {
		return nil
	}
	copy := *ref
	return &copy
}

func coordinatedObservationTamperForTestV1(
	t *testing.T,
	database *sql.DB,
	ignoreChecks bool,
	triggers []string,
	mutate func(context.Context, *sql.Conn) error,
) {
	t.Helper()
	ctx := context.Background()
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	definitions := make([]string, len(triggers))
	for index, trigger := range triggers {
		if err := connection.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?`, trigger,
		).Scan(&definitions[index]); err != nil {
			t.Fatalf("load trigger %s: %v", trigger, err)
		}
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	defer connection.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`)
	if ignoreChecks {
		if _, err := connection.ExecContext(ctx, `PRAGMA ignore_check_constraints=ON`); err != nil {
			t.Fatal(err)
		}
		defer connection.ExecContext(
			context.Background(), `PRAGMA ignore_check_constraints=OFF`,
		)
	}
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	for _, trigger := range triggers {
		if _, err := connection.ExecContext(ctx, `DROP TRIGGER `+trigger); err != nil {
			t.Fatalf("drop trigger %s: %v", trigger, err)
		}
	}
	if err := mutate(ctx, connection); err != nil {
		t.Fatal(err)
	}
	for index, trigger := range triggers {
		if _, err := connection.ExecContext(ctx, definitions[index]); err != nil {
			t.Fatalf("restore trigger %s: %v", trigger, err)
		}
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}
	committed = true
	rows, err := connection.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("coordinated observation tamper left a foreign-key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func requireOverviewObservationGateErrorForTestV1(
	t *testing.T,
	store *Store,
	wantText string,
) {
	t.Helper()
	ctx := context.Background()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	err = VerifyOverviewObservationSemanticClosureV1(ctx, connection)
	_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
	if !errors.Is(err, ErrLoopIntegrity) || !strings.Contains(err.Error(), wantText) {
		t.Fatalf("Overview observation semantic gate error=%v, want %q", err, wantText)
	}
}
