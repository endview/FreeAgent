package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestOverviewObservationSemanticGateAcceptsResourceRunLagAfterCancellation(t *testing.T) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	if _, err := fixture.store.BeginModelDispatch(context.Background(), beginInput); err != nil {
		t.Fatal(err)
	}
	manifest := restoreCancellationTestManifest(t, fixture.input.RunManifestCanonical)
	_, cancellation := newOrdinaryCancellationRequest(
		t, manifest, corecontract.CancellationReasonUserRequestV1,
	)
	if _, err := fixture.store.RequestRunCancellation(
		context.Background(), RequestRunCancellationInput{Canonical: cancellation},
	); err != nil {
		t.Fatal(err)
	}
	verifyOverviewObservationGateInTransactionV1(t, fixture.store)
}

func TestOverviewResourceOnlineHeadRejectsMissingAndMismatchedTransitionCarrier(t *testing.T) {
	for _, test := range []struct {
		name      string
		trigger   string
		statement string
		args      func(string) []any
	}{
		{
			name:    "missing",
			trigger: "overview_resource_transition_carriers_reject_delete",
			statement: `DELETE FROM overview_resource_transition_carriers
				WHERE resource_kind='MODEL' AND resource_id=?`,
			args: func(resourceID string) []any { return []any{resourceID} },
		},
		{
			name:    "Store identity mismatch",
			trigger: "overview_resource_transition_carriers_reject_update",
			statement: `UPDATE overview_resource_transition_carriers
				SET store_instance_id='foreign-store'
				WHERE resource_kind='MODEL' AND resource_id=?`,
			args: func(resourceID string) []any { return []any{resourceID} },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture, _, input := newModelDispatchFixture(t)
			begin, err := fixture.store.BeginModelDispatch(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			execClosedFileTamperV1(
				t, fixture.store, []string{test.trigger}, test.statement,
				test.args(begin.Attempt.AttemptID)...,
			)
			if _, err := loadOverviewResourceHeadOnlineV1(
				context.Background(), fixture.store.db,
				overviewResourceModelV1, begin.Attempt.AttemptID,
			); !errors.Is(err, ErrLoopIntegrity) {
				t.Fatalf("online resource head carrier drift error=%v", err)
			}
		})
	}
}

func TestOverviewObservationSemanticGateRejectsHistoricalRunSnapshotTamper(t *testing.T) {
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
			State:                   corecontract.ModelAttemptUnknown, UnknownReason: "semantic-gate-fixture",
		},
	); err != nil {
		t.Fatal(err)
	}
	tamperWithRestoredTriggerV1(
		t, fixture.store.db, "run_observation_snapshots_reject_update",
		`UPDATE run_observation_snapshots SET canonical_json=X'7B7D'
		 WHERE run_id=? AND observation_sequence=1`, begin.Attempt.RunID,
	)
	connection, err := fixture.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN`); err != nil {
		t.Fatal(err)
	}
	err = VerifyOverviewObservationSemanticClosureV1(context.Background(), connection)
	_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
	if !errors.Is(err, ErrLoopIntegrity) {
		t.Fatalf("historical Run observation tamper error=%v", err)
	}
}

func TestOverviewResourceExactRetryRejectsCurrentSnapshotTamper(t *testing.T) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(context.Background(), beginInput)
	if err != nil {
		t.Fatal(err)
	}
	outcome := CommitModelDispatchOutcomeInput{
		Lease: begin.Lease, AttemptID: begin.Attempt.AttemptID,
		InvocationID: begin.Attempt.AttemptID, Provider: begin.Attempt.Binding.Provider,
		ExpectedAttemptRevision: begin.Attempt.Revision,
		State:                   corecontract.ModelAttemptUnknown, UnknownReason: "exact-retry-fixture",
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(context.Background(), outcome); err != nil {
		t.Fatal(err)
	}
	tamperWithRestoredTriggerV1(
		t, fixture.store.db, "overview_resource_snapshots_reject_update",
		`UPDATE overview_resource_snapshots SET canonical_json=X'7B7D'
		 WHERE resource_kind='MODEL' AND resource_id=?
		 AND observation_sequence=(SELECT MAX(observation_sequence)
		 FROM overview_resource_snapshots WHERE resource_kind='MODEL' AND resource_id=?)`,
		begin.Attempt.AttemptID, begin.Attempt.AttemptID,
	)
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(), outcome,
	); !errors.Is(err, ErrLoopIntegrity) {
		t.Fatalf("exact retry after resource head tamper error=%v", err)
	}
}

func TestOpenExistingCurrentStoreRejectsObservationTamperWithIntactSchema(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	created, err := fixture.store.CommitRunAdmission(context.Background(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	tamperWithRestoredTriggerV1(
		t, fixture.store.db, "run_observation_snapshots_reject_update",
		`UPDATE run_observation_snapshots SET canonical_json=X'7B7D'
		 WHERE run_id=? AND observation_sequence=1`, created.RunID,
	)
	path := fixture.store.Path()
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenExistingCurrentStore(context.Background(), path); err == nil {
		t.Fatal("OpenExistingCurrentStore accepted semantic observation tamper")
	}
}

func TestOverviewObservationSemanticGateRejectsHistoricalBasisChildTamper(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	fixture.basis = advancePublishedBasisForTestV1(
		t, fixture.store, fixture.basis, fixture.controlCanonical, fixture.catalogCanonical,
	)
	tamperWithRestoredTriggerV1(
		t, fixture.store.db, "overview_basis_workspaces_reject_update",
		`UPDATE overview_basis_workspaces SET workspace_version=workspace_version||'-tampered'
		 WHERE projection_digest=(SELECT projection_digest FROM overview_basis_snapshots
		 WHERE tenant_id=? AND pointer_revision=1) AND ordinal=0`, fixture.intent.TenantID,
	)
	connection, err := fixture.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(context.Background(), `BEGIN`); err != nil {
		t.Fatal(err)
	}
	err = VerifyOverviewObservationSemanticClosureV1(context.Background(), connection)
	_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
	if !errors.Is(err, ErrLoopIntegrity) {
		t.Fatalf("historical basis projection tamper error=%v", err)
	}
}

func verifyOverviewObservationGateInTransactionV1(t *testing.T, store *Store) {
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
	defer connection.ExecContext(context.Background(), `ROLLBACK`)
	if err := VerifyOverviewObservationSemanticClosureV1(ctx, connection); err != nil {
		t.Fatalf("verify intact Overview observation closure: %v", err)
	}
}

func tamperWithRestoredTriggerV1(
	t *testing.T,
	database *sql.DB,
	trigger, statement string,
	args ...any,
) {
	t.Helper()
	var definition string
	if err := database.QueryRow(
		`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?`, trigger,
	).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DROP TRIGGER ` + trigger); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(definition); err != nil {
		t.Fatal(err)
	}
}
