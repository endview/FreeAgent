package currentstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestBeginModelDispatchCommitsPreNetworkBoundary(t *testing.T) {
	fixture, lease, input := newModelDispatchFixture(t)
	result, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created ||
		!result.InvokeAllowed ||
		result.Attempt.State != corecontract.ModelAttemptPending ||
		result.Attempt.AttemptID != input.AttemptID ||
		result.Lease.FrameRevision != lease.FrameRevision+1 {
		t.Fatalf("result=%+v", result)
	}
	modelConfig, err := moduleapi.RestoreModelBindingConfigV1(
		result.Attempt.ModelConfigCanonical,
	)
	if err != nil ||
		modelConfig.Provider != result.Attempt.Provider ||
		modelConfig.Model != result.Attempt.Model ||
		modelConfig.BillingVersion != result.Attempt.BillingVersion ||
		modelConfig.PriceSnapshotID != result.Attempt.PriceSnapshotID {
		t.Fatalf(
			"transient model config=%+v error=%v",
			modelConfig,
			err,
		)
	}

	var (
		state          string
		pendingAttempt sql.NullString
		step           string
		frameRevision  int64
		lastEvent      int64
		ledgerSequence sql.NullInt64
		inputTokens    sql.NullInt64
		outputTokens   sql.NullInt64
		usageStatus    string
	)
	if err := fixture.store.db.QueryRow(`
		SELECT
			attempt.state,
			frame.pending_attempt_id,
			frame.step,
			frame.frame_revision,
			frame.last_authoritative_event,
			usage.ledger_sequence,
			usage.input_tokens,
			usage.output_tokens,
			usage.reconciliation_status
		FROM model_dispatch_attempts AS attempt
		JOIN loop_frames AS frame ON frame.run_id=attempt.run_id
		JOIN model_usage AS usage ON usage.attempt_id=attempt.attempt_id
		WHERE attempt.attempt_id=?
	`, input.AttemptID).Scan(
		&state,
		&pendingAttempt,
		&step,
		&frameRevision,
		&lastEvent,
		&ledgerSequence,
		&inputTokens,
		&outputTokens,
		&usageStatus,
	); err != nil {
		t.Fatal(err)
	}
	if state != "PENDING" ||
		!pendingAttempt.Valid ||
		pendingAttempt.String != input.AttemptID ||
		step != corecontract.ModelPendingLoopStep ||
		frameRevision != int64(result.Lease.FrameRevision) ||
		lastEvent != 1 ||
		ledgerSequence.Valid ||
		inputTokens.Valid ||
		outputTokens.Valid ||
		usageStatus != "PENDING" {
		t.Fatalf(
			"attempt/frame/usage=%q %+v %q %d %d %+v %+v %+v %q",
			state,
			pendingAttempt,
			step,
			frameRevision,
			lastEvent,
			ledgerSequence,
			inputTokens,
			outputTokens,
			usageStatus,
		)
	}
	request, err := fixture.store.GetContent(
		context.Background(),
		result.Attempt.Request.Digest,
	)
	if err != nil ||
		request.Kind != ContentModelRequest ||
		string(request.CanonicalBytes) != string(input.RequestCanonical) {
		t.Fatalf("request=%+v error=%v", request, err)
	}
	run, err := fixture.store.LoadRunForLoop(
		context.Background(),
		result.Lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	if run.Frame.PendingAttemptID != input.AttemptID ||
		run.Frame.Step != corecontract.ModelPendingLoopStep ||
		len(run.ModelDispatches) != 1 ||
		run.ModelDispatches[0].Attempt.State !=
			corecontract.ModelAttemptPending ||
		run.ModelDispatches[0].Usage.ReconciliationStatus !=
			modelUsageStatusPending {
		t.Fatalf("loaded frame=%+v", run.Frame)
	}
}

func TestBeginModelDispatchInvocationPermitIsPrivateSharedAndOneShot(
	t *testing.T,
) {
	fixture, _, input := newModelDispatchFixture(t)
	result, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	changed := result
	changed.Attempt.AttemptID = "forged-attempt"
	if changed.ConsumeModelInvocationPermit() {
		t.Fatal("changed Begin closure consumed the private permit")
	}
	copy := result
	if !copy.ConsumeModelInvocationPermit() {
		t.Fatal("exact Begin result did not consume its private permit")
	}
	if result.ConsumeModelInvocationPermit() {
		t.Fatal("a copied Begin result consumed the shared permit twice")
	}
	forged := BeginModelDispatchResult{
		Attempt:       result.Attempt,
		Lease:         result.Lease,
		Created:       true,
		InvokeAllowed: true,
	}
	if forged.ConsumeModelInvocationPermit() {
		t.Fatal("caller-constructed Begin result forged a private permit")
	}

	retryInput := input
	retryInput.Lease = result.Lease
	retry, err := fixture.store.BeginModelDispatch(
		context.Background(),
		retryInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ConsumeModelInvocationPermit() {
		t.Fatal("exact retry recreated an invocation permit")
	}
}

func TestBeginModelDispatchExactRetryNeverGrantsReplay(t *testing.T) {
	fixture, _, input := newModelDispatchFixture(t)
	first, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !first.InvokeAllowed ||
		second.Created ||
		second.InvokeAllowed ||
		second.Attempt.AttemptID != first.Attempt.AttemptID ||
		second.Lease != first.Lease {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	var count int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("attempt count=%d", count)
	}
}

func TestBeginModelDispatchExpiredExactRetryStillDeniesReplay(
	t *testing.T,
) {
	fixture, _, input := newModelDispatchFixture(t)
	// Use the real clock so this retry proof does not depend on rewriting an
	// immutable attempt (and its observation ledger) behind the Store's back.
	input.Deadline = time.Now().
		UTC().
		Add(500 * time.Millisecond).
		Truncate(time.Microsecond)
	first, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || !first.InvokeAllowed {
		t.Fatalf("first=%+v", first)
	}
	remaining := time.Until(input.Deadline)
	if remaining > 2*time.Second {
		t.Fatalf("short deadline wait=%s exceeds test bound", remaining)
	}
	if remaining > 0 {
		deadlineTimer := time.NewTimer(remaining + time.Millisecond)
		timeoutTimer := time.NewTimer(2 * time.Second)
		defer deadlineTimer.Stop()
		defer timeoutTimer.Stop()
		select {
		case <-deadlineTimer.C:
		case <-timeoutTimer.C:
			t.Fatal("real deadline did not elapse within test bound")
		}
	}
	retry, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Created ||
		retry.InvokeAllowed ||
		retry.Attempt.AttemptID != first.Attempt.AttemptID {
		t.Fatalf("retry=%+v", retry)
	}
}

func TestBeginModelDispatchExpiredBeforeDispatchCommitsDirectTerminal(
	t *testing.T,
) {
	fixture, lease, input := newModelDispatchFixture(t)
	input.Deadline = time.Now().
		UTC().
		Add(-time.Minute).
		Truncate(time.Microsecond)

	result, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created ||
		result.InvokeAllowed ||
		result.ConsumeModelInvocationPermit() ||
		result.Attempt.State != corecontract.ModelAttemptFailed ||
		result.Attempt.ErrorClassification !=
			modelDeadlineExpiredBeforeDispatchClassification ||
		result.Attempt.Revision != 0 ||
		result.Lease.RunRevision != lease.RunRevision+1 ||
		result.Lease.FrameRevision != lease.FrameRevision+1 {
		t.Fatalf("result=%+v", result)
	}

	loaded, err := fixture.store.LoadRunForLoop(
		context.Background(),
		result.Lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != corecontract.TerminatedLoopStep ||
		loaded.Disposition != corecontract.TerminatedLoopStep ||
		loaded.Frame.Step != corecontract.TerminatedLoopStep ||
		loaded.Frame.PendingAttemptID != "" ||
		len(loaded.ModelDispatches) != 1 ||
		loaded.ModelDispatches[0].Attempt.State !=
			corecontract.ModelAttemptFailed ||
		loaded.ModelDispatches[0].Usage.ReconciliationStatus !=
			modelUsageStatusNoReport ||
		loaded.ModelDispatches[0].Usage.LedgerSequence != nil ||
		len(loaded.History) != 0 {
		t.Fatalf("loaded=%+v", loaded)
	}

	terminal, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		input.Lease.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.State != corecontract.ModelAttemptFailed ||
		terminal.AttemptID != input.AttemptID ||
		terminal.ErrorClassification !=
			modelDeadlineExpiredBeforeDispatchClassification ||
		len(terminal.OutputCanonical) != 0 {
		t.Fatalf("terminal=%+v", terminal)
	}

	var (
		attemptState       string
		classification     string
		attemptRevision    int64
		ledgerSequence     sql.NullInt64
		usageRevision      int64
		usageStatus        string
		runState           string
		disposition        string
		runRevision        int64
		frameStep          string
		frameRevision      int64
		pendingAttempt     sql.NullString
		lastEvent          int64
		totalEvents        int64
		terminalEventCount int64
	)
	if err := fixture.store.db.QueryRow(`
		SELECT
			a.state,
			a.error_classification,
			a.revision,
			u.ledger_sequence,
			u.revision,
			u.reconciliation_status,
			r.state,
			r.disposition,
			r.revision,
			f.step,
			f.frame_revision,
			f.pending_attempt_id,
			f.last_authoritative_event,
			(SELECT COUNT(*) FROM run_events WHERE run_id=r.run_id),
			(SELECT COUNT(*) FROM run_events
			 WHERE run_id=r.run_id AND event_kind=?)
		FROM model_dispatch_attempts AS a
		JOIN model_usage AS u ON u.attempt_id=a.attempt_id
		JOIN runs AS r ON r.run_id=a.run_id
		JOIN loop_frames AS f ON f.run_id=a.run_id
		WHERE a.attempt_id=?
	`,
		corecontract.ModelDispatchTerminalEventKind,
		input.AttemptID,
	).Scan(
		&attemptState,
		&classification,
		&attemptRevision,
		&ledgerSequence,
		&usageRevision,
		&usageStatus,
		&runState,
		&disposition,
		&runRevision,
		&frameStep,
		&frameRevision,
		&pendingAttempt,
		&lastEvent,
		&totalEvents,
		&terminalEventCount,
	); err != nil {
		t.Fatal(err)
	}
	if attemptState != string(corecontract.ModelAttemptFailed) ||
		classification !=
			modelDeadlineExpiredBeforeDispatchClassification ||
		attemptRevision != 0 ||
		ledgerSequence.Valid ||
		usageRevision != 0 ||
		usageStatus != modelUsageStatusNoReport ||
		runState != corecontract.TerminatedLoopStep ||
		disposition != corecontract.TerminatedLoopStep ||
		runRevision != int64(result.Lease.RunRevision) ||
		frameStep != corecontract.TerminatedLoopStep ||
		frameRevision != int64(result.Lease.FrameRevision) ||
		pendingAttempt.Valid ||
		lastEvent != 1 ||
		totalEvents != 2 ||
		terminalEventCount != 1 {
		t.Fatalf(
			"attempt=%q/%q/%d usage=%+v/%d/%q run=%q/%q/%d frame=%q/%d/%+v/%d events=%d/%d",
			attemptState,
			classification,
			attemptRevision,
			ledgerSequence,
			usageRevision,
			usageStatus,
			runState,
			disposition,
			runRevision,
			frameStep,
			frameRevision,
			pendingAttempt,
			lastEvent,
			totalEvents,
			terminalEventCount,
		)
	}
}

func TestBeginModelDispatchExpiredBeforeDispatchExactRetryIsReadOnly(
	t *testing.T,
) {
	fixture, _, input := newModelDispatchFixture(t)
	input.Deadline = time.Now().
		UTC().
		Add(-time.Minute).
		Truncate(time.Microsecond)
	first, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Created ||
		retry.InvokeAllowed ||
		retry.ConsumeModelInvocationPermit() ||
		retry.Attempt.AttemptID != first.Attempt.AttemptID ||
		retry.Attempt.State != corecontract.ModelAttemptFailed ||
		retry.Attempt.ErrorClassification !=
			modelDeadlineExpiredBeforeDispatchClassification ||
		retry.Lease != first.Lease {
		t.Fatalf("first=%+v retry=%+v", first, retry)
	}
	var attempts, usage, events int
	if err := fixture.store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM model_usage),
			(SELECT COUNT(*) FROM run_events WHERE run_id=?)
	`, input.Lease.RunID).Scan(&attempts, &usage, &events); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || usage != 1 || events != 2 {
		t.Fatalf(
			"attempts=%d usage=%d events=%d",
			attempts,
			usage,
			events,
		)
	}
}

func TestBeginModelDispatchExpiredBeforeDispatchRollsBackAtomically(
	t *testing.T,
) {
	fixture, lease, input := newModelDispatchFixture(t)
	input.Deadline = time.Now().
		UTC().
		Add(-time.Minute).
		Truncate(time.Microsecond)
	if _, err := fixture.store.db.Exec(`
		CREATE TRIGGER reject_expired_terminal_event
		BEFORE INSERT ON run_events
		WHEN NEW.event_kind='MODEL_DISPATCH_TERMINAL'
		BEGIN
			SELECT RAISE(ABORT, 'injected terminal event failure');
		END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	); err == nil {
		t.Fatal("injected terminal event failure was accepted")
	}

	var (
		attempts      int
		usage         int
		events        int
		runState      string
		disposition   sql.NullString
		runRevision   int64
		frameStep     string
		frameRevision int64
		pending       sql.NullString
		lastEvent     int64
	)
	if err := fixture.store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM model_usage),
			(SELECT COUNT(*) FROM run_events WHERE run_id=r.run_id),
			r.state,
			r.disposition,
			r.revision,
			f.step,
			f.frame_revision,
			f.pending_attempt_id,
			f.last_authoritative_event
		FROM runs AS r
		JOIN loop_frames AS f ON f.run_id=r.run_id
		WHERE r.run_id=?
	`, input.Lease.RunID).Scan(
		&attempts,
		&usage,
		&events,
		&runState,
		&disposition,
		&runRevision,
		&frameStep,
		&frameRevision,
		&pending,
		&lastEvent,
	); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 ||
		usage != 0 ||
		events != 1 ||
		runState != corecontract.InitialRunState ||
		disposition.Valid ||
		runRevision != int64(lease.RunRevision) ||
		frameStep != corecontract.InitialLoopStep ||
		frameRevision != int64(lease.FrameRevision) ||
		pending.Valid ||
		lastEvent != 0 {
		t.Fatalf(
			"attempts=%d usage=%d events=%d run=%q/%+v/%d frame=%q/%d/%+v/%d",
			attempts,
			usage,
			events,
			runState,
			disposition,
			runRevision,
			frameStep,
			frameRevision,
			pending,
			lastEvent,
		)
	}
}

func TestBeginModelDispatchRetryRevalidatesUsagePlaceholder(
	t *testing.T,
) {
	fixture, _, input := newModelDispatchFixture(t)
	first, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	execClosedFileTamperV1(
		t,
		fixture.store,
		[]string{"model_usage_observation_update_guard"},
		`
		UPDATE model_usage
		SET reconciliation_status='CORRUPTED'
		WHERE attempt_id=?
	`,
		input.AttemptID,
	)
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	); !errors.Is(err, ErrModelDispatchIntegrity) {
		t.Fatalf(
			"corrupt placeholder retry first=%+v error=%v",
			first,
			err,
		)
	}
}

func TestBeginModelDispatchSameStepDifferentRequestConflicts(
	t *testing.T,
) {
	fixture, _, input := newModelDispatchFixture(t)
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	); err != nil {
		t.Fatal(err)
	}
	_, changedRequest, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{
				{Role: moduleapi.ModelRoleUser, Content: "different"},
			},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	input.RequestCanonical = changedRequest
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	); !errors.Is(err, ErrModelDispatchConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	var count int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("attempt count=%d", count)
	}
}

func TestBeginModelDispatchFailureLeavesNoPartialAttempt(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(
			*admissionCommitFixture,
			*BeginModelDispatchInput,
			corecontract.RunManifest,
		)
	}{
		{
			name: "missing price",
			mutate: func(
				fixture *admissionCommitFixture,
				_ *BeginModelDispatchInput,
				_ corecontract.RunManifest,
			) {
				if _, err := fixture.store.db.Exec(`
					DELETE FROM model_price_snapshots
					WHERE price_snapshot_id='price-deepseek-v4'
				`); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "deadline exceeds manifest",
			mutate: func(
				_ *admissionCommitFixture,
				input *BeginModelDispatchInput,
				manifest corecontract.RunManifest,
			) {
				input.Deadline = manifest.Deadline.
					Add(time.Microsecond).
					Truncate(time.Microsecond)
			},
		},
		{
			name: "parameters differ from frozen config",
			mutate: func(
				_ *admissionCommitFixture,
				input *BeginModelDispatchInput,
				_ corecontract.RunManifest,
			) {
				_, request, err :=
					moduleapi.NewModelGenerateRequestV1(
						moduleapi.ModelGenerateRequestV1{
							SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
							Messages: []moduleapi.ModelMessageV1{
								{
									Role:    moduleapi.ModelRoleUser,
									Content: "Say hello.",
								},
							},
							Parameters: json.RawMessage(
								`{"temperature":1}`,
							),
						},
					)
				if err != nil {
					t.Fatal(err)
				}
				input.RequestCanonical = request
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, lease, input := newModelDispatchFixture(t)
			manifest, err := corecontract.RestoreRunManifest(
				fixture.input.RunManifestCanonical,
			)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(fixture, &input, manifest)
			if _, err := fixture.store.BeginModelDispatch(
				context.Background(),
				input,
			); err == nil {
				t.Fatal("invalid dispatch accepted")
			}
			var attempts, usage int
			if err := fixture.store.db.QueryRow(`
				SELECT
					(SELECT COUNT(*) FROM model_dispatch_attempts),
					(SELECT COUNT(*) FROM model_usage)
			`).Scan(&attempts, &usage); err != nil {
				t.Fatal(err)
			}
			var (
				frameRevision int64
				pending       sql.NullString
			)
			if err := fixture.store.db.QueryRow(`
				SELECT frame_revision, pending_attempt_id
				FROM loop_frames
				WHERE run_id=?
			`, lease.RunID).Scan(
				&frameRevision,
				&pending,
			); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 ||
				usage != 0 ||
				frameRevision != int64(lease.FrameRevision) ||
				pending.Valid {
				t.Fatalf(
					"partial attempts=%d usage=%d frame=%d pending=%+v",
					attempts,
					usage,
					frameRevision,
					pending,
				)
			}
		})
	}
}

func TestBeginModelDispatchRevokedCurrentProviderLeavesNoAttempt(
	t *testing.T,
) {
	fixture, lease, input := newModelDispatchFixture(t)
	publishEmptyCurrentCatalog(
		t,
		fixture.store,
		fixture.intent.TenantID,
		fixture.basis.PointerRevision,
	)
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	); !errors.Is(err, ErrCurrentActivationDenied) {
		t.Fatalf("BeginModelDispatch() error = %v", err)
	}

	var attempts, usage int
	if err := fixture.store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM model_usage)
	`).Scan(&attempts, &usage); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || usage != 0 {
		t.Fatalf("attempt/usage rows = %d/%d, want 0/0", attempts, usage)
	}
	var (
		frameRevision int64
		step          string
		pending       sql.NullString
	)
	if err := fixture.store.db.QueryRow(`
		SELECT frame_revision, step, pending_attempt_id
		FROM loop_frames
		WHERE run_id=?
	`, lease.RunID).Scan(&frameRevision, &step, &pending); err != nil {
		t.Fatal(err)
	}
	if frameRevision != int64(lease.FrameRevision) ||
		step != corecontract.InitialLoopStep || pending.Valid {
		t.Fatalf(
			"frame after denial = revision %d step %q pending %+v",
			frameRevision,
			step,
			pending,
		)
	}
}

func TestBeginModelDispatchRevocationDoesNotReplayHistoricalPending(
	t *testing.T,
) {
	fixture, _, input := newModelDispatchFixture(t)
	first, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	publishEmptyCurrentCatalog(
		t,
		fixture.store,
		fixture.intent.TenantID,
		fixture.basis.PointerRevision,
	)
	input.Lease = first.Lease
	retry, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("exact historical retry after revocation: %v", err)
	}
	if retry.Created || retry.InvokeAllowed ||
		retry.Attempt.AttemptID != first.Attempt.AttemptID ||
		retry.Attempt.State != corecontract.ModelAttemptPending {
		t.Fatalf("historical retry = %+v", retry)
	}
	var attempts int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts
	`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("historical retry attempt rows = %d, want 1", attempts)
	}
}

func TestBeginModelDispatchUsesFrozenBindingAndPrice(t *testing.T) {
	fixture, _, input := newModelDispatchFixture(t)
	result, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantBinding, err := exactModelBinding(
		mustRestoreMemberSnapshot(t, fixture),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempt.Binding.Provider != wantBinding.Provider ||
		result.Attempt.Provider != "deepseek" ||
		result.Attempt.Model != "deepseek-v4-pro" ||
		result.Attempt.BillingVersion != "2026-07" {
		t.Fatalf("attempt=%+v", result.Attempt)
	}
	result.Attempt.Binding.StaticContextRefs = append(
		result.Attempt.Binding.StaticContextRefs,
		"mutated",
	)
	result.Attempt.Request.CanonicalBytes[0] = 'x'
	stored, err := queryModelDispatchAttempt(
		context.Background(),
		fixture.store.db,
		input.AttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Request.CanonicalBytes[0] != '{' {
		t.Fatal("caller mutated persisted attempt")
	}
}

func TestModelBudgetAcceptsMaximumLengthBudgetStateRefV1(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	manifest, err := corecontract.RestoreRunManifest(
		fixture.input.RunManifestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	runID := strings.Repeat("r", 256)
	reference, err := corecontract.NewBudgetStateRefV1(runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalModelBudget(
		manifest.BudgetPolicy,
		runID,
		reference,
	)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restoreModelBudget(canonical, runID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.BudgetStateRef != reference ||
		restored.BudgetPolicy != manifest.BudgetPolicy ||
		restored.LedgerSequence != 0 {
		t.Fatalf("restored=%+v", restored)
	}
}

func newModelDispatchFixture(
	t *testing.T,
) (*admissionCommitFixture, RunLease, BeginModelDispatchInput) {
	return newModelDispatchFixtureWithModelProfileAndPrice(
		t,
		0,
		testModelPriceSnapshot(),
	)
}

func newModelDispatchFixtureWithModelProfile(
	t *testing.T,
	contextWindowTokens uint64,
) (*admissionCommitFixture, RunLease, BeginModelDispatchInput) {
	return newModelDispatchFixtureWithModelProfileAndPrice(
		t,
		contextWindowTokens,
		testModelPriceSnapshot(),
	)
}

func newModelDispatchFixtureWithModelProfileAndPrice(
	t *testing.T,
	contextWindowTokens uint64,
	price corecontract.ModelPriceSnapshotV1,
) (*admissionCommitFixture, RunLease, BeginModelDispatchInput) {
	t.Helper()
	fixture := newAdmissionCommitFixtureWithModelProfileAndPrice(
		t,
		contextWindowTokens,
		price,
	)
	fixture.intent.Deadline = time.Now().
		UTC().
		Add(2 * time.Hour).
		Truncate(time.Microsecond)
	_, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(fixture.intent)
	if err != nil {
		t.Fatal(err)
	}
	fixture.input = fixture.compileInput(
		t,
		intentCanonical,
		intentDigest,
		"run-admitted",
	)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 "run-admitted",
			OwnerID:               "loop-worker-model",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := corecontract.RestoreRunManifest(
		fixture.input.RunManifestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, requestCanonical, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{
				{
					Role:    moduleapi.ModelRoleSystem,
					Content: "Follow Core constraints.",
				},
				{
					Role:    moduleapi.ModelRoleUser,
					Content: "Say hello.",
				},
			},
			Parameters: json.RawMessage(`{"temperature":0}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture, lease, BeginModelDispatchInput{
		Lease:            lease,
		AttemptID:        "attempt-reply-1",
		LogicalStepID:    "reply-1",
		RequestCanonical: requestCanonical,
		Deadline: manifest.Deadline.Add(-time.Hour).
			Truncate(time.Microsecond),
	}
}

func mustRestoreMemberSnapshot(
	t *testing.T,
	fixture *admissionCommitFixture,
) corecontract.MemberExecutionSnapshot {
	t.Helper()
	member, err := corecontract.RestoreMemberExecutionSnapshot(
		fixture.input.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return member
}
