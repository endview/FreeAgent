package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/corecontract"
)

func TestRequestRunCancellationFamilyAtomicIdempotentAndConflicts(
	t *testing.T,
) {
	t.Run("atomic install and exact retry", func(t *testing.T) {
		fixture := newCommittedCompositeRuntimeFixture(t)
		request, canonical := newFamilyCancellationRequest(
			t,
			fixture.compiled.Parent.RunManifest,
			"USER_REQUEST",
		)
		before := admissionCommitCounts(t, fixture.store)
		result, err := fixture.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: canonical},
		)
		if err != nil {
			t.Fatalf("RequestRunCancellation: %v", err)
		}
		if !result.Created || result.Request != request || result.Ref == "" {
			t.Fatalf("created cancellation=%+v", result)
		}
		assertCompositeFamilyLatch(t, fixture, result.Ref)
		record, err := fixture.store.GetContent(
			context.Background(),
			result.Ref,
		)
		if err != nil || record.Kind != ContentRunCancellation ||
			record.MediaType != admissionJSONMediaType ||
			!bytes.Equal(record.CanonicalBytes, canonical) {
			t.Fatalf("cancellation content=%+v error=%v", record, err)
		}
		afterCreate := admissionCommitCounts(t, fixture.store)
		if afterCreate[5] != before[5]+1 {
			t.Fatalf(
				"cancellation content count=%d want %d",
				afterCreate[5],
				before[5]+1,
			)
		}

		latchesBeforeRetry := loadCompositeLatchSnapshot(t, fixture)
		retry, err := fixture.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: canonical},
		)
		if err != nil {
			t.Fatalf("exact cancellation retry: %v", err)
		}
		if retry.Created || retry.Request != request || retry.Ref != result.Ref {
			t.Fatalf("retry cancellation=%+v", retry)
		}
		if got := loadCompositeLatchSnapshot(t, fixture); !reflect.DeepEqual(
			got,
			latchesBeforeRetry,
		) {
			t.Fatalf("exact retry rewrote latches: before=%+v after=%+v", latchesBeforeRetry, got)
		}
		if afterRetry := admissionCommitCounts(t, fixture.store); afterRetry != afterCreate {
			t.Fatalf("exact retry changed rows: before=%v after=%v", afterCreate, afterRetry)
		}
	})

	t.Run("forced Child update failure rolls back every latch and content", func(t *testing.T) {
		fixture := newCommittedCompositeRuntimeFixture(t)
		_, canonical := newFamilyCancellationRequest(
			t,
			fixture.compiled.Parent.RunManifest,
			"OPERATOR_REQUEST",
		)
		ref, err := ComputeContentDigest(
			ContentRunCancellation,
			admissionJSONMediaType,
			canonical,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.db.Exec(`
			CREATE TRIGGER fail_composite_child_cancel
			BEFORE UPDATE OF cancel_request_ref ON runs
			WHEN OLD.parent_run_id IS NOT NULL
			 AND NEW.cancel_request_ref IS NOT NULL
			BEGIN
				SELECT RAISE(ABORT, 'forced composite Child latch failure');
			END
		`); err != nil {
			t.Fatalf("install cancellation failure trigger: %v", err)
		}
		before := admissionCommitCounts(t, fixture.store)
		if _, err := fixture.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: canonical},
		); err == nil {
			t.Fatal("forced Child latch failure was accepted")
		}
		assertCompositeFamilyLatch(t, fixture, "")
		if after := admissionCommitCounts(t, fixture.store); after != before {
			t.Fatalf("failed cancellation changed rows: before=%v after=%v", before, after)
		}
		if _, err := fixture.store.GetContent(
			context.Background(),
			ref,
		); !errors.Is(err, ErrContentNotFound) {
			t.Fatalf("rolled-back cancellation content error=%v", err)
		}
	})

	t.Run("different canonical latch conflicts", func(t *testing.T) {
		fixture := newCommittedCompositeRuntimeFixture(t)
		_, firstCanonical := newFamilyCancellationRequest(
			t,
			fixture.compiled.Parent.RunManifest,
			"USER_REQUEST",
		)
		first, err := fixture.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: firstCanonical},
		)
		if err != nil {
			t.Fatal(err)
		}
		before := loadCompositeLatchSnapshot(t, fixture)
		_, differentCanonical := newFamilyCancellationRequest(
			t,
			fixture.compiled.Parent.RunManifest,
			"OPERATOR_REQUEST",
		)
		if _, err := fixture.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: differentCanonical},
		); !errors.Is(err, ErrRunCancellationConflict) {
			t.Fatalf("different cancellation error=%v", err)
		}
		if got := loadCompositeLatchSnapshot(t, fixture); !reflect.DeepEqual(got, before) {
			t.Fatalf("conflict rewrote family: before=%+v after=%+v", before, got)
		}
		assertCompositeFamilyLatch(t, fixture, first.Ref)
	})

	t.Run("partial same latch conflicts without healing", func(t *testing.T) {
		fixture := newCommittedCompositeRuntimeFixture(t)
		_, canonical := newFamilyCancellationRequest(
			t,
			fixture.compiled.Parent.RunManifest,
			"USER_REQUEST",
		)
		if _, err := fixture.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: canonical},
		); err != nil {
			t.Fatal(err)
		}
		partialRunID := fixture.compiled.Children[0].RunManifest.RunID
		execClosedFileTamperV1(t, fixture.store, nil, `
			UPDATE runs SET cancel_request_ref=NULL WHERE run_id=?
		`, partialRunID)
		before := loadCompositeLatchSnapshot(t, fixture)
		if _, err := fixture.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: canonical},
		); !errors.Is(err, ErrRunCancellationConflict) {
			t.Fatalf("partial cancellation error=%v", err)
		}
		if got := loadCompositeLatchSnapshot(t, fixture); !reflect.DeepEqual(got, before) {
			t.Fatalf("partial conflict healed or rewrote family: before=%+v after=%+v", before, got)
		}
	})
}

func TestCompositeCancellationBlocksNewBeginButNeverReplaysExactAttempt(
	t *testing.T,
) {
	fixture := newCommittedCompositeRuntimeFixture(t)
	firstInput := newCompositeChildBeginInput(
		t,
		fixture,
		0,
		"attempt-before-family-cancel",
		"child-step-before-family-cancel",
	)
	blockedInput := newCompositeChildBeginInput(
		t,
		fixture,
		1,
		"attempt-after-family-cancel",
		"child-step-after-family-cancel",
	)
	first, err := fixture.store.BeginModelDispatch(
		context.Background(),
		firstInput,
	)
	if err != nil || !first.Created || !first.InvokeAllowed {
		t.Fatalf("first Begin=%+v error=%v", first, err)
	}
	if !first.ConsumeModelInvocationPermit() {
		t.Fatal("first authorized Begin did not carry its one-shot permit")
	}
	_, canonical := newFamilyCancellationRequest(
		t,
		fixture.compiled.Parent.RunManifest,
		"USER_REQUEST",
	)
	if _, err := fixture.store.RequestRunCancellation(
		context.Background(),
		RequestRunCancellationInput{Canonical: canonical},
	); err != nil {
		t.Fatalf("cancel family: %v", err)
	}

	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		blockedInput,
	); !errors.Is(err, ErrRunCanceled) {
		t.Fatalf("new Begin after cancellation error=%v", err)
	}
	retry, err := fixture.store.BeginModelDispatch(
		context.Background(),
		firstInput,
	)
	if err != nil {
		t.Fatalf("exact pre-cancel retry: %v", err)
	}
	if retry.Created || retry.InvokeAllowed || retry.ConsumeModelInvocationPermit() ||
		retry.Attempt.AttemptID != first.Attempt.AttemptID {
		t.Fatalf("exact retry regained invocation authority: %+v", retry)
	}
	var attempts int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts
	`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("model attempt count=%d want 1", attempts)
	}
}

func TestRunCancellationDoesNotRewriteOrReplayModelUnknown(t *testing.T) {
	fixture := newCommittedCompositeRuntimeFixture(t)
	input := newCompositeChildBeginInput(
		t,
		fixture,
		0,
		"attempt-unknown-before-cancel",
		"child-step-unknown-before-cancel",
	)
	begin, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			ProviderRequestID:       "provider-unknown-before-cancel",
			UnknownReason:           "provider completion is ambiguous",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, canonical := newFamilyCancellationRequest(
		t,
		fixture.compiled.Parent.RunManifest,
		corecontract.CancellationReasonOperatorRequestV1,
	)
	if _, err := fixture.store.RequestRunCancellation(
		context.Background(),
		RequestRunCancellationInput{Canonical: canonical},
	); err != nil {
		t.Fatal(err)
	}
	var (
		state    string
		revision int64
	)
	if err := fixture.store.db.QueryRow(`
		SELECT state, revision
		FROM model_dispatch_attempts
		WHERE attempt_id=?
	`, begin.Attempt.AttemptID).Scan(&state, &revision); err != nil {
		t.Fatal(err)
	}
	if state != string(corecontract.ModelAttemptUnknown) ||
		revision != int64(unknown.Record.Attempt.Revision) {
		t.Fatalf("cancellation rewrote UNKNOWN as state=%q revision=%d", state, revision)
	}

	retryInput := input
	retryInput.Lease = unknown.Lease
	retry, err := fixture.store.BeginModelDispatch(
		context.Background(),
		retryInput,
	)
	if err != nil {
		t.Fatalf("exact UNKNOWN retry: %v", err)
	}
	if retry.Created || retry.InvokeAllowed || retry.ConsumeModelInvocationPermit() ||
		retry.Attempt.State != corecontract.ModelAttemptUnknown {
		t.Fatalf("UNKNOWN retry regained authority: %+v", retry)
	}
	var attempts int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts
	`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("attempt count=%d error=%v", attempts, err)
	}
}

func TestCompositeFamilyModelDispatchLimitCountsUnknownAndExactRetryAtCap(
	t *testing.T,
) {
	fixture := newCommittedCompositeRuntimeFixture(t)
	unknownInput := newCompositeChildBeginInput(
		t,
		fixture,
		0,
		"attempt-family-unknown",
		"child-step-family-unknown",
	)
	unknownBegin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		unknownInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   unknownBegin.Lease,
			AttemptID:               unknownBegin.Attempt.AttemptID,
			InvocationID:            unknownBegin.Attempt.AttemptID,
			Provider:                unknownBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: unknownBegin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			ProviderRequestID:       "provider-request-1",
			UnknownReason:           "provider completion is ambiguous",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Record.Attempt.State != corecontract.ModelAttemptUnknown {
		t.Fatalf("unknown outcome=%+v", unknown.Record.Attempt)
	}
	unknownProjection, err := fixture.store.GetCompositeFamilyUsageProjection(
		context.Background(),
		fixture.compiled.Parent.RunManifest.RunID,
	)
	if err != nil || unknownProjection.Aggregate.AttemptSlotsUsed != 1 {
		t.Fatalf("UNKNOWN family projection=%+v error=%v", unknownProjection, err)
	}

	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	reconciled, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   unknown.Lease,
			AttemptID:               unknownBegin.Attempt.AttemptID,
			InvocationID:            unknownBegin.Attempt.AttemptID,
			Provider:                unknownBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: unknown.Record.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
			ReconciliationEvidenceCanonical: []byte(
				`{"kind":"provider_lookup","request_id":"provider-request-1"}`,
			),
		},
	)
	if err != nil {
		t.Fatalf("reconcile UNKNOWN Child: %v", err)
	}
	if err := fixture.store.ReleaseRunLease(
		context.Background(),
		reconciled.Lease,
	); err != nil {
		t.Fatalf("release reconciled Child lease: %v", err)
	}
	finishCompositeChildForAcceptance(t, fixture, 1)
	rootInput, rootBegin := beginCompositeRootForCapTest(
		t,
		fixture,
		"family-cap-root",
		"attempt-family-cap-root",
	)

	root := fixture.compiled.Parent.RunManifest
	var familyAttempts int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*)
		FROM model_dispatch_attempts AS attempt
		JOIN runs AS family_run ON family_run.run_id=attempt.run_id
		WHERE family_run.run_id=? OR family_run.parent_run_id=?
	`, root.RunID, root.RunID).Scan(&familyAttempts); err != nil {
		t.Fatal(err)
	}
	if familyAttempts != int(root.Composite.Plan.FamilyModelDispatchLimit) {
		t.Fatalf(
			"family attempt count=%d want limit %d",
			familyAttempts,
			root.Composite.Plan.FamilyModelDispatchLimit,
		)
	}
	retryInput := rootInput
	retryInput.Lease = rootBegin.Lease
	retry, err := fixture.store.BeginModelDispatch(
		context.Background(),
		retryInput,
	)
	if err != nil || retry.Created || retry.InvokeAllowed ||
		retry.ConsumeModelInvocationPermit() ||
		retry.Attempt.AttemptID != rootBegin.Attempt.AttemptID {
		t.Fatalf("exact retry at family cap=%+v error=%v", retry, err)
	}
	var attemptsAfterRetry int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts
	`).Scan(&attemptsAfterRetry); err != nil {
		t.Fatal(err)
	}
	if attemptsAfterRetry != familyAttempts {
		t.Fatalf(
			"exact retry at cap changed Attempt count: %d -> %d",
			familyAttempts,
			attemptsAfterRetry,
		)
	}
}

func TestLoadCompositeRootProjectsChildResultsInPlanOrder(t *testing.T) {
	fixture := newCommittedCompositeRuntimeFixture(t)
	rootLease := acquireCompositeTestLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"composite-root-reader",
	)
	firstInput := newCompositeChildBeginInput(
		t,
		fixture,
		0,
		"attempt-child-first",
		"child-step-first",
	)
	firstBegin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		firstInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertCompositeRootChildStates(
		t,
		fixture,
		rootLease,
		[]CompositeChildStateV1{
			CompositeChildPendingV1,
			CompositeChildPendingV1,
		},
	)

	unknown, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   firstBegin.Lease,
			AttemptID:               firstBegin.Attempt.AttemptID,
			InvocationID:            firstBegin.Attempt.AttemptID,
			Provider:                firstBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: firstBegin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			ProviderRequestID:       "provider-request-1",
			UnknownReason:           "ambiguous child completion",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	unknownView := assertCompositeRootChildStates(
		t,
		fixture,
		rootLease,
		[]CompositeChildStateV1{
			CompositeChildUnknownV1,
			CompositeChildPendingV1,
		},
	)
	if unknownView.CompositeChildren[0].ErrorClassification != modelUnknownWaitingReason {
		t.Fatalf("UNKNOWN projection=%+v", unknownView.CompositeChildren[0])
	}

	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	succeeded, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   unknown.Lease,
			AttemptID:               firstBegin.Attempt.AttemptID,
			InvocationID:            firstBegin.Attempt.AttemptID,
			Provider:                firstBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: unknown.Record.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
			ReconciliationEvidenceCanonical: []byte(
				`{"kind":"provider_lookup","request_id":"provider-request-1"}`,
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	successView := assertCompositeRootChildStates(
		t,
		fixture,
		rootLease,
		[]CompositeChildStateV1{
			CompositeChildSucceededV1,
			CompositeChildPendingV1,
		},
	)
	wantResultRef, err := ComputeContentDigest(
		ContentModelResult,
		admissionJSONMediaType,
		outputCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstResult := successView.CompositeChildren[0]
	if firstResult.AttemptID != succeeded.Record.Attempt.AttemptID ||
		firstResult.ResultRef != wantResultRef ||
		!bytes.Equal(firstResult.OutputCanonical, outputCanonical) ||
		firstResult.ErrorClassification != "" {
		t.Fatalf("SUCCEEDED projection=%+v", firstResult)
	}

	secondInput := newCompositeChildBeginInput(
		t,
		fixture,
		1,
		"attempt-child-second",
		"child-step-second",
	)
	secondBegin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		secondInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   secondBegin.Lease,
			AttemptID:               secondBegin.Attempt.AttemptID,
			InvocationID:            secondBegin.Attempt.AttemptID,
			Provider:                secondBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: secondBegin.Attempt.Revision,
			State:                   corecontract.ModelAttemptFailed,
			ErrorClassification:     "CHILD_PROVIDER_REJECTED",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	failedView := assertCompositeRootChildStates(
		t,
		fixture,
		rootLease,
		[]CompositeChildStateV1{
			CompositeChildSucceededV1,
			CompositeChildFailedV1,
		},
	)
	secondResult := failedView.CompositeChildren[1]
	if secondResult.AttemptID != failed.Record.Attempt.AttemptID ||
		secondResult.ResultRef != "" || len(secondResult.OutputCanonical) != 0 ||
		secondResult.ErrorClassification != "CHILD_PROVIDER_REJECTED" {
		t.Fatalf("FAILED projection=%+v", secondResult)
	}
}

func TestLoadCompositeRootRejectsChildClosureTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *compositeAdmissionFixture)
	}{
		{
			name: "parent slot projection",
			mutate: func(t *testing.T, fixture *compositeAdmissionFixture) {
				t.Helper()
				execClosedFileTamperV1(t, fixture.store, nil, `
					UPDATE runs SET parent_slot_id='slot-tampered'
					WHERE run_id=?
				`, fixture.compiled.Children[0].RunManifest.RunID)
			},
		},
		{
			name: "member snapshot digest",
			mutate: func(t *testing.T, fixture *compositeAdmissionFixture) {
				t.Helper()
				actual := fixture.compiled.Children[0].MemberSnapshot.MemberSnapshotDigest
				drift := strings.Repeat("f", 64)
				if drift == actual {
					drift = strings.Repeat("e", 64)
				}
				execClosedFileTamperV1(
					t,
					fixture.store,
					[]string{"member_execution_snapshots_reject_update"},
					`
					UPDATE member_execution_snapshots SET digest=?
					WHERE run_id=?
				`,
					drift,
					fixture.compiled.Children[0].RunManifest.RunID,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCommittedCompositeRuntimeFixture(t)
			rootLease := acquireCompositeTestLease(
				t,
				fixture.store,
				fixture.compiled.Parent.RunManifest.RunID,
				"composite-root-tamper-reader",
			)
			test.mutate(t, fixture)
			if _, err := fixture.store.LoadRunForLoop(
				context.Background(),
				rootLease,
			); err == nil ||
				(!errors.Is(err, ErrLoopIntegrity) &&
					!errors.Is(err, ErrAdmissionIntegrity)) {
				t.Fatalf("tampered Child closure error=%v", err)
			}
		})
	}
}

func newCompositeRuntimeFixture(t *testing.T) *compositeAdmissionFixture {
	t.Helper()
	fixture := newCompositeAdmissionFixture(t)
	intentInput := fixture.parentIntent
	intentInput.Deadline = time.Now().
		UTC().
		Add(4 * time.Hour).
		Truncate(time.Microsecond)
	parentIntent, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(intentInput)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).CompileCompositeFamily(
		context.Background(),
		assemblycompiler.CompositeCompileInput{
			Parent: assemblycompiler.CompileInput{
				IntentCanonical:  intentCanonical,
				IntentDigest:     intentDigest,
				RunID:            "run-composite-parent",
				MemberID:         "member-composite-parent",
				RecoveryRootRef:  "recovery/run-composite-parent",
				PublishedBasis:   fixture.basis,
				ControlCanonical: fixture.controlCanonical,
				CatalogCanonical: fixture.catalogCanonical,
			},
		},
	)
	if err != nil {
		t.Fatalf("CompileCompositeFamily runtime fixture: %v", err)
	}
	fixture.parentIntent = parentIntent
	fixture.compiled = compiled
	fixture.input = CommitCompositeRunFamilyInput{
		Parent: compositeCommitRunInput(
			fixture,
			compiled.Parent,
			intentCanonical,
			intentDigest,
		),
		Children: make([]CommitRunAdmissionInput, len(compiled.Children)),
	}
	for index, child := range compiled.Children {
		fixture.input.Children[index] = compositeCommitRunInput(
			fixture,
			child.CompileOutput,
			child.IntentCanonical,
			child.IntentDigest,
		)
	}
	return fixture
}

func newCommittedCompositeRuntimeFixture(
	t *testing.T,
) *compositeAdmissionFixture {
	t.Helper()
	fixture := newCompositeRuntimeFixture(t)
	if _, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatalf("CommitCompositeRunFamily runtime fixture: %v", err)
	}
	return fixture
}

func newFamilyCancellationRequest(
	t *testing.T,
	root corecontract.RunManifest,
	reasonCode string,
) (corecontract.RunCancellationRequestV1, []byte) {
	t.Helper()
	request, canonical, err := corecontract.NewRunCancellationRequestV1(
		corecontract.RunCancellationRequestV1{
			SchemaVersion:      corecontract.RunCancellationRequestSchemaVersionV1,
			RootRunID:          root.RunID,
			RootManifestDigest: root.ManifestDigest,
			Scope:              corecontract.CancellationScopeFamilyV1,
			ReasonCode:         reasonCode,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return request, canonical
}

type compositeLatchSnapshotRow struct {
	RunID     string
	CancelRef sql.NullString
	UpdatedAt int64
}

func loadCompositeLatchSnapshot(
	t *testing.T,
	fixture *compositeAdmissionFixture,
) []compositeLatchSnapshotRow {
	t.Helper()
	rootRunID := fixture.compiled.Parent.RunManifest.RunID
	rows, err := fixture.store.db.Query(`
		SELECT run_id, cancel_request_ref, updated_at
		FROM runs
		WHERE run_id=? OR parent_run_id=?
		ORDER BY run_id
	`, rootRunID, rootRunID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []compositeLatchSnapshotRow
	for rows.Next() {
		var row compositeLatchSnapshotRow
		if err := rows.Scan(&row.RunID, &row.CancelRef, &row.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertCompositeFamilyLatch(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	wantRef string,
) {
	t.Helper()
	rows := loadCompositeLatchSnapshot(t, fixture)
	if len(rows) != len(fixture.compiled.Children)+1 {
		t.Fatalf("family latch row count=%d", len(rows))
	}
	for _, row := range rows {
		if wantRef == "" {
			if row.CancelRef.Valid {
				t.Fatalf("Run %q has unexpected latch %q", row.RunID, row.CancelRef.String)
			}
			continue
		}
		if !row.CancelRef.Valid || row.CancelRef.String != wantRef {
			t.Fatalf("Run %q latch=%v want %q", row.RunID, row.CancelRef, wantRef)
		}
	}
}

func acquireCompositeTestLease(
	t *testing.T,
	store *Store,
	runID string,
	ownerID string,
) RunLease {
	t.Helper()
	lease, err := store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 runID,
			OwnerID:               ownerID,
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("AcquireRunLease(%s): %v", runID, err)
	}
	return lease
}

func newCompositeChildBeginInput(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	childIndex int,
	attemptID string,
	logicalStepID string,
) BeginModelDispatchInput {
	t.Helper()
	child := fixture.compiled.Children[childIndex]
	lease := acquireCompositeTestLease(
		t,
		fixture.store,
		child.RunManifest.RunID,
		"composite-child-worker-"+child.Assignment.SlotID,
	)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop(%s): %v", child.RunManifest.RunID, err)
	}
	compiled, err := recompileContextForNewAttempt(
		corecontract.ContextCompilationV1{},
		run,
		run.Frame.Revision,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("compile composite Child model request: %v", err)
	}
	return BeginModelDispatchInput{
		Lease:                       lease,
		AttemptID:                   attemptID,
		LogicalStepID:               logicalStepID,
		ContextCompilationCanonical: compiled.CompilationCanonical,
		RequestCanonical:            compiled.RequestCanonical,
		Deadline: child.RunManifest.Deadline.
			Add(-time.Hour).
			Truncate(time.Microsecond),
	}
}

func beginCompositeRootForCapTest(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	ownerID string,
	attemptID string,
) (BeginModelDispatchInput, BeginModelDispatchResult) {
	t.Helper()
	root := fixture.compiled.Parent.RunManifest
	lease := acquireCompositeTestLease(
		t,
		fixture.store,
		root.RunID,
		ownerID,
	)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := recompileContextForNewAttempt(
		corecontract.ContextCompilationV1{},
		run,
		run.Frame.Revision,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("compile root merge at family cap: %v", err)
	}
	input := BeginModelDispatchInput{
		Lease:                       lease,
		AttemptID:                   attemptID,
		LogicalStepID:               corecontract.CompositeMergeLogicalStepIDV1,
		ContextCompilationCanonical: compiled.CompilationCanonical,
		RequestCanonical:            compiled.RequestCanonical,
		Deadline:                    root.Deadline.Add(-time.Hour).Truncate(time.Microsecond),
	}
	begin, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("begin root merge at family cap=%+v error=%v", begin, err)
	}
	return input, begin
}

func assertCompositeRootChildStates(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	rootLease RunLease,
	want []CompositeChildStateV1,
) RunForLoop {
	t.Helper()
	run, err := fixture.store.LoadRunForLoop(context.Background(), rootLease)
	if err != nil {
		t.Fatalf("LoadRunForLoop(composite root): %v", err)
	}
	plan := run.Manifest.Composite.Plan.Children
	if len(run.CompositeChildren) != len(plan) || len(want) != len(plan) {
		t.Fatalf(
			"CompositeChildren=%d plan=%d want=%d",
			len(run.CompositeChildren),
			len(plan),
			len(want),
		)
	}
	for index, child := range run.CompositeChildren {
		planned := plan[index]
		if child.SlotID != planned.SlotID || child.RunID != planned.RunID ||
			child.ManifestDigest !=
				fixture.compiled.Children[index].RunManifest.ManifestDigest ||
			child.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
			child.Assignment != planned.Assignment || child.State != want[index] {
			t.Fatalf(
				"Child projection %d=%+v plan=%+v want state=%s",
				index,
				child,
				planned,
				want[index],
			)
		}
	}
	return run
}
