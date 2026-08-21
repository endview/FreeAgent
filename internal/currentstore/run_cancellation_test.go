package currentstore

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestRequestRunCancellationOrdinaryRunIsAtomicAndIdempotent(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatal(err)
	}
	manifest := restoreCancellationTestManifest(t, fixture.input.RunManifestCanonical)
	request, canonical := newOrdinaryCancellationRequest(
		t,
		manifest,
		corecontract.CancellationReasonUserRequestV1,
	)

	before := admissionCommitCounts(t, fixture.store)
	created, err := fixture.store.RequestRunCancellation(
		context.Background(),
		RequestRunCancellationInput{Canonical: canonical},
	)
	if err != nil {
		t.Fatalf("RequestRunCancellation: %v", err)
	}
	if !created.Created || created.Request != request || created.Ref == "" {
		t.Fatalf("created cancellation=%+v", created)
	}
	var latch string
	if err := fixture.store.db.QueryRow(`
		SELECT cancel_request_ref FROM runs WHERE run_id=?
	`, manifest.RunID).Scan(&latch); err != nil || latch != created.Ref {
		t.Fatalf("ordinary latch=%q error=%v", latch, err)
	}
	record, err := fixture.store.GetContent(context.Background(), created.Ref)
	if err != nil || record.Kind != ContentRunCancellation ||
		string(record.CanonicalBytes) != string(canonical) {
		t.Fatalf("cancellation content=%+v error=%v", record, err)
	}
	afterCreate := admissionCommitCounts(t, fixture.store)
	if afterCreate[5] != before[5]+1 {
		t.Fatalf("content count=%d want %d", afterCreate[5], before[5]+1)
	}

	rowBefore := loadSingleCancellationLatchSnapshot(t, fixture.store, manifest.RunID)
	retry, err := fixture.store.RequestRunCancellation(
		context.Background(),
		RequestRunCancellationInput{Canonical: canonical},
	)
	if err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if retry.Created || retry.Request != request || retry.Ref != created.Ref {
		t.Fatalf("retry=%+v", retry)
	}
	rowAfter := loadSingleCancellationLatchSnapshot(t, fixture.store, manifest.RunID)
	if !reflect.DeepEqual(rowAfter, rowBefore) {
		t.Fatalf("retry rewrote latch: before=%+v after=%+v", rowBefore, rowAfter)
	}
	if afterRetry := admissionCommitCounts(t, fixture.store); afterRetry != afterCreate {
		t.Fatalf("retry changed rows: before=%v after=%v", afterCreate, afterRetry)
	}

	_, different := newOrdinaryCancellationRequest(
		t,
		manifest,
		corecontract.CancellationReasonOperatorRequestV1,
	)
	if _, err := fixture.store.RequestRunCancellation(
		context.Background(),
		RequestRunCancellationInput{Canonical: different},
	); !errors.Is(err, ErrRunCancellationConflict) {
		t.Fatalf("different second request error=%v", err)
	}
	if got := loadSingleCancellationLatchSnapshot(t, fixture.store, manifest.RunID); !reflect.DeepEqual(got, rowBefore) {
		t.Fatalf("conflict rewrote latch: before=%+v after=%+v", rowBefore, got)
	}
}

func TestRequestRunCancellationRejectsWrongTargetWithoutWrites(t *testing.T) {
	t.Run("ordinary digest and scope", func(t *testing.T) {
		fixture := newAdmissionCommitFixture(t)
		if _, err := fixture.store.CommitRunAdmission(
			context.Background(),
			fixture.input,
		); err != nil {
			t.Fatal(err)
		}
		manifest := restoreCancellationTestManifest(t, fixture.input.RunManifestCanonical)
		before := admissionCommitCounts(t, fixture.store)
		for _, request := range []corecontract.RunCancellationRequestV1{
			{
				SchemaVersion:      corecontract.RunCancellationRequestSchemaVersionV1,
				RootRunID:          manifest.RunID,
				RootManifestDigest: cancellationTestDigest('f'),
				Scope:              corecontract.CancellationScopeRunV1,
				ReasonCode:         corecontract.CancellationReasonUserRequestV1,
			},
			{
				SchemaVersion:      corecontract.RunCancellationRequestSchemaVersionV1,
				RootRunID:          manifest.RunID,
				RootManifestDigest: manifest.ManifestDigest,
				Scope:              corecontract.CancellationScopeFamilyV1,
				ReasonCode:         corecontract.CancellationReasonUserRequestV1,
			},
		} {
			_, canonical, err := corecontract.NewRunCancellationRequestV1(request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.RequestRunCancellation(
				context.Background(),
				RequestRunCancellationInput{Canonical: canonical},
			); !errors.Is(err, ErrInvalidRunCancellation) {
				t.Fatalf("invalid target error=%v", err)
			}
		}
		if after := admissionCommitCounts(t, fixture.store); after != before {
			t.Fatalf("rejected ordinary cancellation wrote rows: before=%v after=%v", before, after)
		}
	})

	t.Run("direct inherited Child", func(t *testing.T) {
		fixture := newCommittedCompositeRuntimeFixture(t)
		child := fixture.compiled.Children[0].RunManifest
		request := corecontract.RunCancellationRequestV1{
			SchemaVersion:      corecontract.RunCancellationRequestSchemaVersionV1,
			RootRunID:          child.RunID,
			RootManifestDigest: child.ManifestDigest,
			Scope:              corecontract.CancellationScopeFamilyV1,
			ReasonCode:         corecontract.CancellationReasonUserRequestV1,
		}
		_, canonical, err := corecontract.NewRunCancellationRequestV1(request)
		if err != nil {
			t.Fatal(err)
		}
		before := admissionCommitCounts(t, fixture.store)
		if _, err := fixture.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: canonical},
		); !errors.Is(err, ErrInvalidRunCancellation) {
			t.Fatalf("direct Child cancellation error=%v", err)
		}
		assertCompositeFamilyLatch(t, fixture, "")
		if after := admissionCommitCounts(t, fixture.store); after != before {
			t.Fatalf("rejected Child cancellation wrote rows: before=%v after=%v", before, after)
		}
	})
}

func TestOrdinaryCancellationBlocksPermitAndLoadsExactClosure(t *testing.T) {
	fixture, lease, input := newModelDispatchFixture(t)
	manifest := restoreCancellationTestManifest(t, fixture.input.RunManifestCanonical)
	request, canonical := newOrdinaryCancellationRequest(
		t,
		manifest,
		corecontract.CancellationReasonPolicyEnforcedV1,
	)
	cancelled, err := fixture.store.RequestRunCancellation(
		context.Background(),
		RequestRunCancellationInput{Canonical: canonical},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	); !errors.Is(err, ErrRunCanceled) {
		t.Fatalf("Begin after ordinary cancellation error=%v", err)
	}
	var attempts int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts
	`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("attempt count=%d error=%v", attempts, err)
	}
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop: %v", err)
	}
	if run.CancellationRef != cancelled.Ref || run.CancellationRequest == nil ||
		*run.CancellationRequest != request {
		t.Fatalf("loaded cancellation=%q %+v", run.CancellationRef, run.CancellationRequest)
	}
}

func TestRunCancellationRacesModelBeginAtOnePermitBoundary(t *testing.T) {
	fixture, _, input := newModelDispatchFixture(t)
	manifest := restoreCancellationTestManifest(t, fixture.input.RunManifestCanonical)
	request, canonical := newOrdinaryCancellationRequest(
		t,
		manifest,
		corecontract.CancellationReasonUserRequestV1,
	)
	type beginResponse struct {
		result BeginModelDispatchResult
		err    error
	}
	type cancelResponse struct {
		result RunCancellationResult
		err    error
	}
	start := make(chan struct{})
	beginDone := make(chan beginResponse, 1)
	cancelDone := make(chan cancelResponse, 1)
	go func() {
		<-start
		result, err := fixture.store.BeginModelDispatch(
			context.Background(),
			input,
		)
		beginDone <- beginResponse{result: result, err: err}
	}()
	go func() {
		<-start
		result, err := fixture.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: canonical},
		)
		cancelDone <- cancelResponse{result: result, err: err}
	}()
	close(start)
	begin := <-beginDone
	cancelled := <-cancelDone
	if cancelled.err != nil || !cancelled.result.Created ||
		cancelled.result.Request != request {
		t.Fatalf("racing cancellation=%+v error=%v", cancelled.result, cancelled.err)
	}

	var attempts int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts
	`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	switch {
	case begin.err == nil:
		if attempts != 1 || !begin.result.Created || !begin.result.InvokeAllowed ||
			!begin.result.ConsumeModelInvocationPermit() {
			t.Fatalf("Begin-won race result=%+v attempts=%d", begin.result, attempts)
		}
		retry, err := fixture.store.BeginModelDispatch(
			context.Background(),
			input,
		)
		if err != nil {
			t.Fatalf("exact pre-cancel retry: %v", err)
		}
		if retry.Created || retry.InvokeAllowed || retry.ConsumeModelInvocationPermit() {
			t.Fatalf("exact retry regained authority after cancellation: %+v", retry)
		}
	case errors.Is(begin.err, ErrRunCanceled):
		if attempts != 0 || begin.result.Created || begin.result.InvokeAllowed ||
			begin.result.ConsumeModelInvocationPermit() {
			t.Fatalf("cancel-won race result=%+v attempts=%d", begin.result, attempts)
		}
		if _, err := fixture.store.BeginModelDispatch(
			context.Background(),
			input,
		); !errors.Is(err, ErrRunCanceled) {
			t.Fatalf("post-cancel Begin error=%v", err)
		}
	default:
		t.Fatalf("racing Begin returned unexpected error: %v", begin.err)
	}
	var latch string
	if err := fixture.store.db.QueryRow(`
		SELECT cancel_request_ref FROM runs WHERE run_id=?
	`, manifest.RunID).Scan(&latch); err != nil || latch != cancelled.result.Ref {
		t.Fatalf("racing latch=%q error=%v", latch, err)
	}
}

func TestRunCancellationBlocksNewExternalEffectPermits(t *testing.T) {
	t.Run("Action", func(t *testing.T) {
		harness := newActionStoreHarness(t)
		run, err := harness.store.LoadRunForLoop(
			context.Background(),
			harness.modelBegin.Lease,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, canonical := newOrdinaryCancellationRequest(
			t,
			run.Manifest,
			corecontract.CancellationReasonOperatorRequestV1,
		)
		if _, err := harness.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: canonical},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.store.CommitModelActionAndBeginDispatch(
			context.Background(),
			harness.actionBeginInput(t, "action-after-cancel"),
		); !errors.Is(err, ErrRunCanceled) {
			t.Fatalf("Action begin after cancellation error=%v", err)
		}
		assertPendingModelAndNoExternalDispatch(
			t,
			harness.store,
			harness.modelBegin.Attempt.AttemptID,
		)
	})

	t.Run("Channel", func(t *testing.T) {
		harness := newChannelDispatchHarness(
			t,
			"run-channel-canceled",
			"event-channel-canceled",
		)
		run, err := harness.store.LoadRunForLoop(
			context.Background(),
			harness.modelBegin.Lease,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, canonical := newOrdinaryCancellationRequest(
			t,
			run.Manifest,
			corecontract.CancellationReasonOperatorRequestV1,
		)
		if _, err := harness.store.RequestRunCancellation(
			context.Background(),
			RequestRunCancellationInput{Canonical: canonical},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.store.CommitModelChannelAndBeginDispatch(
			context.Background(),
			harness.beginInput,
		); !errors.Is(err, ErrRunCanceled) {
			t.Fatalf("Channel begin after cancellation error=%v", err)
		}
		assertPendingModelAndNoExternalDispatch(
			t,
			harness.store,
			harness.modelBegin.Attempt.AttemptID,
		)
	})
}

func newOrdinaryCancellationRequest(
	t *testing.T,
	manifest corecontract.RunManifest,
	reasonCode string,
) (corecontract.RunCancellationRequestV1, []byte) {
	t.Helper()
	request, canonical, err := corecontract.NewRunCancellationRequestV1(
		corecontract.RunCancellationRequestV1{
			SchemaVersion:      corecontract.RunCancellationRequestSchemaVersionV1,
			RootRunID:          manifest.RunID,
			RootManifestDigest: manifest.ManifestDigest,
			Scope:              corecontract.CancellationScopeRunV1,
			ReasonCode:         reasonCode,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return request, canonical
}

func restoreCancellationTestManifest(
	t *testing.T,
	canonical []byte,
) corecontract.RunManifest {
	t.Helper()
	manifest, err := corecontract.RestoreRunManifest(canonical)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

type singleCancellationLatchSnapshot struct {
	Ref       string
	UpdatedAt int64
}

func loadSingleCancellationLatchSnapshot(
	t *testing.T,
	store *Store,
	runID string,
) singleCancellationLatchSnapshot {
	t.Helper()
	var snapshot singleCancellationLatchSnapshot
	if err := store.db.QueryRow(`
		SELECT cancel_request_ref, updated_at FROM runs WHERE run_id=?
	`, runID).Scan(&snapshot.Ref, &snapshot.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func cancellationTestDigest(value byte) string {
	result := make([]byte, 64)
	for index := range result {
		result[index] = value
	}
	return string(result)
}

func assertPendingModelAndNoExternalDispatch(
	t *testing.T,
	store *Store,
	attemptID string,
) {
	t.Helper()
	var state string
	if err := store.db.QueryRow(`
		SELECT state FROM model_dispatch_attempts WHERE attempt_id=?
	`, attemptID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(corecontract.ModelAttemptPending) {
		t.Fatalf("cancellation rewrote PENDING Model Attempt to %q", state)
	}
	var dispatches int
	if err := store.db.QueryRow(`
		SELECT COUNT(*) FROM dispatch_attempts
	`).Scan(&dispatches); err != nil {
		t.Fatal(err)
	}
	if dispatches != 0 {
		t.Fatalf("canceled Run persisted %d external dispatch Attempts", dispatches)
	}
}
