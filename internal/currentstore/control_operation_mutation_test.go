package currentstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/moduledisablecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCommitModuleDisableControlOperationAtomicOutcomesV1(t *testing.T) {
	t.Run("APPLIED", func(t *testing.T) {
		fixture := newControlReceiptFixtureV1(
			t,
			moduleapi.ContextPlacementTrustedInstruction,
		)
		preBasis := fixture.basis
		built := buildControlReceiptRowV1(
			t,
			fixture,
			fixture.basis,
			fixture.control,
			fixture.catalog,
		)
		if built.result.Publication == nil ||
			built.metadata.status != controlapicontract.OperationStatusAppliedV1 {
			t.Fatalf("fixture result=%+v metadata=%+v", built.result, built.metadata)
		}

		record, created, err := fixture.store.CommitModuleDisableControlOperationV1(
			context.Background(),
			moduleDisableControlOperationInputFromBuiltV1(built),
		)
		if err != nil {
			t.Fatalf("CommitModuleDisableControlOperationV1: %v", err)
		}
		if !created || record.Status != controlapicontract.OperationStatusAppliedV1 ||
			record.DomainReceipt == nil || record.DomainReceiptRef == nil ||
			record.DomainReceipt.PlanDigest != built.planDigest ||
			record.PreBasis != apiBasisFromControlBasisV1(preBasis) ||
			record.PostBasis != apiBasisFromControlBasisV1(built.result.CandidateBasis) ||
			record.AuthorizationRevision != uint64(built.metadata.policyRevision) ||
			record.ScopeSetDigest != built.metadata.scopeSetDigest ||
			!bytes.Equal(record.InputCanonical, built.payload.input) ||
			!bytes.Equal(record.EvaluationCanonical, built.payload.evaluation) ||
			record.ControlReceipt.CompletedAtUnixMicros == 0 {
			t.Fatalf("APPLIED record=%+v created=%v", record, created)
		}
		current, _, _, err := fixture.store.LoadPublishedBasis(
			context.Background(),
			preBasis.TenantID,
		)
		if err != nil || current != built.result.CandidateBasis {
			t.Fatalf("current basis=%+v error=%v", current, err)
		}
		assertModuleDisableControlOperationExactRetryV1(t, fixture.store, record)
		alternate := alternateControlReceiptRequestV1(t, built.request)
		_, conflictCreated, conflictErr :=
			fixture.store.CommitModuleDisableControlOperationV1(
				context.Background(),
				CommitModuleDisableControlOperationInputV1{
					RequestCanonical: alternate.canonical,
					RequestDigest:    alternate.digest,
				},
			)
		var conflict *ControlOperationReceiptConflictErrorV1
		if conflictCreated ||
			!errors.Is(conflictErr, ErrControlOperationReceiptConflictV1) ||
			!errors.As(conflictErr, &conflict) || conflict.Identity != record.Identity {
			t.Fatalf("identity conflict created=%v error=%T %v", conflictCreated, conflictErr, conflictErr)
		}
		if err := VerifyControlOperationReceiptSemanticClosureV1(
			context.Background(),
			fixture.store.db,
		); err != nil {
			t.Fatalf("semantic closure: %v", err)
		}
	})

	t.Run("NO_CHANGE", func(t *testing.T) {
		fixture := newControlReceiptFixtureV1(
			t,
			moduleapi.ContextPlacementTrustedInstruction,
		)
		advanceControlReceiptFixtureToDisabledV1(t, &fixture)
		preBasis := fixture.basis
		built := buildControlReceiptRowV1(
			t,
			fixture,
			fixture.basis,
			fixture.control,
			fixture.catalog,
		)
		if built.metadata.status != controlapicontract.OperationStatusNoChangeV1 {
			t.Fatalf("fixture status=%s", built.metadata.status)
		}

		record, created, err := fixture.store.CommitModuleDisableControlOperationV1(
			context.Background(),
			moduleDisableControlOperationInputFromBuiltV1(built),
		)
		if err != nil {
			t.Fatalf("CommitModuleDisableControlOperationV1: %v", err)
		}
		if !created || record.Status != controlapicontract.OperationStatusNoChangeV1 ||
			record.DomainReceipt != nil || record.DomainReceiptRef != nil ||
			record.PreBasis != apiBasisFromControlBasisV1(preBasis) ||
			record.PostBasis != record.PreBasis ||
			!bytes.Equal(record.PreBasisCanonical, record.PostBasisCanonical) {
			t.Fatalf("NO_CHANGE record=%+v created=%v", record, created)
		}
		current, _, _, err := fixture.store.LoadPublishedBasis(
			context.Background(),
			preBasis.TenantID,
		)
		if err != nil || current != preBasis {
			t.Fatalf("NO_CHANGE current=%+v error=%v", current, err)
		}
		assertModuleDisableControlOperationExactRetryV1(t, fixture.store, record)
		if err := VerifyControlOperationReceiptSemanticClosureV1(
			context.Background(),
			fixture.store.db,
		); err != nil {
			t.Fatalf("semantic closure: %v", err)
		}
	})
}

func TestEvaluateModuleDisableControlOperationIsEffectFreeAndMatchesCommitV1(
	t *testing.T,
) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementTrustedInstruction,
	)
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	preBasis := fixture.basis
	preRef := expectedPublishedPointerRefV1(
		apiBasisFromControlBasisV1(preBasis),
		built.metadata.preBasisDigest,
	)

	evaluated, err := fixture.store.EvaluateModuleDisableControlOperationV1(
		context.Background(),
		EvaluateModuleDisableControlOperationInputV1{
			TenantID:        preBasis.TenantID,
			ExpectedPointer: preRef,
			InputCanonical:  built.payload.input,
			InputDigest:     built.metadata.inputDigest,
		},
	)
	if err != nil {
		t.Fatalf("EvaluateModuleDisableControlOperationV1: %v", err)
	}
	if evaluated.InputDigest != built.metadata.inputDigest ||
		evaluated.PlanDigest != built.planDigest ||
		evaluated.EvaluationDigest != built.metadata.evaluationDigest ||
		!bytes.Equal(evaluated.InputCanonical, built.payload.input) ||
		!bytes.Equal(evaluated.EvaluationCanonical, built.payload.evaluation) ||
		evaluated.Evaluation.Projection.Disposition !=
			moduledisablecontract.ModuleDisableWouldApplyV1 {
		t.Fatalf("effect-free evaluation=%+v", evaluated)
	}
	current, _, _, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		preBasis.TenantID,
	)
	if err != nil || current != preBasis {
		t.Fatalf("evaluation changed current=%+v error=%v", current, err)
	}
	assertControlOperationReceiptRowCountV1(t, fixture.store, 0)

	record, created, err := fixture.store.CommitModuleDisableControlOperationV1(
		context.Background(),
		moduleDisableControlOperationInputFromBuiltV1(built),
	)
	if err != nil || !created || record.EvaluationDigest != evaluated.EvaluationDigest ||
		!bytes.Equal(record.EvaluationCanonical, evaluated.EvaluationCanonical) {
		t.Fatalf("commit after evaluation=(%+v,%v,%v)", record, created, err)
	}
}

func TestEvaluateAndCommitModuleDisableControlOperationRejectIneligibleSliceV1(
	t *testing.T,
) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementUntrustedData,
	)
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	preRef := expectedPublishedPointerRefV1(
		apiBasisFromControlBasisV1(fixture.basis),
		built.metadata.preBasisDigest,
	)
	_, err := fixture.store.EvaluateModuleDisableControlOperationV1(
		context.Background(),
		EvaluateModuleDisableControlOperationInputV1{
			TenantID:        fixture.basis.TenantID,
			ExpectedPointer: preRef,
			InputCanonical:  built.payload.input,
			InputDigest:     built.metadata.inputDigest,
		},
	)
	if !errors.Is(err, ErrModuleDisableControlOperationIneligibleV1) {
		t.Fatalf("ineligible evaluation error=%v", err)
	}
	_, created, err := fixture.store.CommitModuleDisableControlOperationV1(
		context.Background(),
		moduleDisableControlOperationInputFromBuiltV1(built),
	)
	if !errors.Is(err, ErrModuleDisableControlOperationIneligibleV1) || created {
		t.Fatalf("ineligible commit created=%v error=%v", created, err)
	}
	current, _, _, loadErr := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.basis.TenantID,
	)
	if loadErr != nil || current != fixture.basis {
		t.Fatalf("ineligible current=%+v error=%v", current, loadErr)
	}
	assertControlOperationReceiptRowCountV1(t, fixture.store, 0)
}

func TestCommitModuleDisableControlOperationRejectsBackfillAndEvaluationDriftV1(
	t *testing.T,
) {
	t.Run("candidate already published without receipt", func(t *testing.T) {
		fixture := newControlReceiptFixtureV1(
			t,
			moduleapi.ContextPlacementTrustedInstruction,
		)
		built := buildControlReceiptRowV1(
			t,
			fixture,
			fixture.basis,
			fixture.control,
			fixture.catalog,
		)
		if _, err := fixture.store.PublishControlCatalog(
			context.Background(),
			publishInputFromDisableResultV1(t, built.result),
		); err != nil {
			t.Fatalf("publish candidate without receipt: %v", err)
		}

		_, created, err := fixture.store.CommitModuleDisableControlOperationV1(
			context.Background(),
			moduleDisableControlOperationInputFromBuiltV1(built),
		)
		if !errors.Is(err, ErrPublicationConflict) || created {
			t.Fatalf("backfill result created=%v error=%v", created, err)
		}
		assertControlOperationReceiptRowCountV1(t, fixture.store, 0)
	})

	t.Run("confirmed evaluation differs from exact replay", func(t *testing.T) {
		fixture := newControlReceiptFixtureV1(
			t,
			moduleapi.ContextPlacementTrustedInstruction,
		)
		built := buildControlReceiptRowV1(
			t,
			fixture,
			fixture.basis,
			fixture.control,
			fixture.catalog,
		)
		evaluation, err := moduledisablecontract.RestoreModuleDisableEvaluationV1(
			built.payload.evaluation,
			built.metadata.evaluationDigest,
		)
		if err != nil {
			t.Fatal(err)
		}
		if evaluation.Projection.CatalogChange ==
			moduledisablecontract.ModuleDisableCatalogRetainInstanceV1 {
			evaluation.Projection.CatalogChange =
				moduledisablecontract.ModuleDisableCatalogRemoveInstanceV1
		} else {
			evaluation.Projection.CatalogChange =
				moduledisablecontract.ModuleDisableCatalogRetainInstanceV1
		}
		_, evaluationCanonical, evaluationDigest, err :=
			moduledisablecontract.NewModuleDisableEvaluationV1(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		request := mutateControlReceiptRequestV1(
			t,
			built.request,
			func(value *controlapicontract.ControlOperationRequestV1) {
				value.OperationEvaluationDigest = evaluationDigest
			},
		)
		input := moduleDisableControlOperationInputFromBuiltV1(built)
		input.RequestCanonical = request.canonical
		input.RequestDigest = request.digest
		input.EvaluationCanonical = evaluationCanonical

		_, created, err := fixture.store.CommitModuleDisableControlOperationV1(
			context.Background(),
			input,
		)
		if !errors.Is(err, ErrInvalidControlOperationReceiptV1) || created {
			t.Fatalf("evaluation drift created=%v error=%v", created, err)
		}
		current, _, _, loadErr := fixture.store.LoadPublishedBasis(
			context.Background(),
			fixture.basis.TenantID,
		)
		if loadErr != nil || current != fixture.basis {
			t.Fatalf("evaluation drift current=%+v error=%v", current, loadErr)
		}
		assertControlOperationReceiptRowCountV1(t, fixture.store, 0)
	})
}

func TestCommitModuleDisableControlOperationReceiptFailureRollsBackPublicationV1(
	t *testing.T,
) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementTrustedInstruction,
	)
	preBasis := fixture.basis
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	if built.result.Publication == nil {
		t.Fatal("fixture has no candidate publication")
	}
	if _, err := fixture.store.db.ExecContext(context.Background(), `
		CREATE TRIGGER test_reject_module_disable_receipt
		BEFORE INSERT ON control_operation_receipts
		BEGIN
			SELECT RAISE(ABORT, 'test forced receipt failure');
		END
	`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = fixture.store.db.ExecContext(
			context.Background(),
			`DROP TRIGGER IF EXISTS test_reject_module_disable_receipt`,
		)
	})

	_, created, err := fixture.store.CommitModuleDisableControlOperationV1(
		context.Background(),
		moduleDisableControlOperationInputFromBuiltV1(built),
	)
	if !errors.Is(err, ErrControlOperationReceiptIntegrityV1) || created {
		t.Fatalf("forced receipt failure created=%v error=%v", created, err)
	}
	current, _, _, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		preBasis.TenantID,
	)
	if err != nil || current != preBasis {
		t.Fatalf("rolled-back current=%+v error=%v", current, err)
	}
	assertControlOperationReceiptRowCountV1(t, fixture.store, 0)
	for table, id := range map[string]string{
		"control_snapshots":           built.result.Publication.ControlRef.SnapshotID,
		"runtime_catalog_generations": built.result.Publication.CatalogRef.GenerationID,
	} {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s=?", table, map[string]string{
			"control_snapshots":           "snapshot_id",
			"runtime_catalog_generations": "generation_id",
		}[table])
		if err := fixture.store.db.QueryRowContext(
			context.Background(),
			query,
			id,
		).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rolled-back %s count=%d error=%v", table, count, err)
		}
	}
}

func TestCommitModuleDisableControlOperationConcurrentExactRetryV1(t *testing.T) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementTrustedInstruction,
	)
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	input := moduleDisableControlOperationInputFromBuiltV1(built)

	const callers = 12
	type outcome struct {
		digest  string
		created bool
		err     error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			record, created, err := fixture.store.CommitModuleDisableControlOperationV1(
				context.Background(),
				input,
			)
			outcomes <- outcome{digest: record.ReceiptDigest, created: created, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(outcomes)

	createdCount := 0
	receiptDigest := ""
	for result := range outcomes {
		if result.err != nil {
			t.Fatalf("concurrent commit: %v", result.err)
		}
		if result.created {
			createdCount++
		}
		if receiptDigest == "" {
			receiptDigest = result.digest
		} else if result.digest != receiptDigest {
			t.Fatalf("receipt digests differ: %s != %s", result.digest, receiptDigest)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count=%d, want 1", createdCount)
	}
	assertControlOperationReceiptRowCountV1(t, fixture.store, 1)
	current, _, _, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.basis.TenantID,
	)
	if err != nil || current != built.result.CandidateBasis {
		t.Fatalf("concurrent current=%+v error=%v", current, err)
	}
}

func TestCommitModuleDisableControlOperationConcurrentDistinctKeysApplyOnceV1(
	t *testing.T,
) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementTrustedInstruction,
	)
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	first := moduleDisableControlOperationInputFromBuiltV1(built)
	secondRequest := mutateControlReceiptRequestV1(
		t,
		built.request,
		func(value *controlapicontract.ControlOperationRequestV1) {
			value.IdempotencyKeyDigest = moduleapi.Digest(
				"freeagent.test.concurrent-module-disable-key/v1",
				[]byte("second"),
			)
		},
	)
	second := moduleDisableControlOperationInputFromBuiltV1(built)
	second.RequestCanonical = secondRequest.canonical
	second.RequestDigest = secondRequest.digest

	type result struct {
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for _, input := range []CommitModuleDisableControlOperationInputV1{first, second} {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, created, err := fixture.store.CommitModuleDisableControlOperationV1(
				context.Background(),
				input,
			)
			results <- result{created: created, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	createdCount := 0
	conflictCount := 0
	for outcome := range results {
		switch {
		case outcome.err == nil && outcome.created:
			createdCount++
		case errors.Is(outcome.err, ErrPublicationConflict) && !outcome.created:
			conflictCount++
		default:
			t.Fatalf("distinct-key outcome created=%v error=%v", outcome.created, outcome.err)
		}
	}
	if createdCount != 1 || conflictCount != 1 {
		t.Fatalf("created=%d conflict=%d, want 1/1", createdCount, conflictCount)
	}
	assertControlOperationReceiptRowCountV1(t, fixture.store, 1)
	current, _, _, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.basis.TenantID,
	)
	if err != nil || current != built.result.CandidateBasis {
		t.Fatalf("distinct-key current=%+v error=%v", current, err)
	}
}

func TestCommitModuleDisableControlOperationLostResponseRetryAfterReopenV1(
	t *testing.T,
) {
	fixture := newControlReceiptFixtureV1(
		t,
		moduleapi.ContextPlacementTrustedInstruction,
	)
	built := buildControlReceiptRowV1(
		t,
		fixture,
		fixture.basis,
		fixture.control,
		fixture.catalog,
	)
	record, created, err := fixture.store.CommitModuleDisableControlOperationV1(
		context.Background(),
		moduleDisableControlOperationInputFromBuiltV1(built),
	)
	if err != nil || !created {
		t.Fatalf("initial commit=(%+v,%v,%v)", record, created, err)
	}
	path := fixture.store.Path()
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close before retry: %v", err)
	}
	reopened, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen before retry: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	assertModuleDisableControlOperationExactRetryV1(t, reopened, record)
	current, _, _, err := reopened.LoadPublishedBasis(
		context.Background(),
		fixture.basis.TenantID,
	)
	if err != nil || current != built.result.CandidateBasis {
		t.Fatalf("reopened current=%+v error=%v", current, err)
	}
	assertControlOperationReceiptRowCountV1(t, reopened, 1)
}

func moduleDisableControlOperationInputFromBuiltV1(
	built builtControlReceiptRowV1,
) CommitModuleDisableControlOperationInputV1 {
	policyRevision := uint64(built.metadata.policyRevision)
	return CommitModuleDisableControlOperationInputV1{
		AuthorizationRevision: policyRevision,
		ScopeSetDigest:        built.metadata.scopeSetDigest,
		RequestCanonical:      bytes.Clone(built.payload.request),
		RequestDigest:         built.metadata.requestDigest,
		InputCanonical:        bytes.Clone(built.payload.input),
		EvaluationCanonical:   bytes.Clone(built.payload.evaluation),
	}
}

func assertModuleDisableControlOperationExactRetryV1(
	t *testing.T,
	store *Store,
	want StoredControlOperationReceiptV1,
) {
	t.Helper()
	got, created, err := store.CommitModuleDisableControlOperationV1(
		context.Background(),
		CommitModuleDisableControlOperationInputV1{
			RequestCanonical: bytes.Clone(want.RequestCanonical),
			RequestDigest:    want.RequestDigest,
			// These malformed fresh fields prove lookup-first retry semantics.
			InputCanonical:      []byte(`not-json`),
			EvaluationCanonical: []byte(`not-json`),
		},
	)
	if err != nil || created || got.ReceiptDigest != want.ReceiptDigest ||
		!bytes.Equal(got.ControlReceiptCanonical, want.ControlReceiptCanonical) ||
		!bytes.Equal(got.DomainReceiptCanonical, want.DomainReceiptCanonical) {
		t.Fatalf("exact retry=(%+v,%v,%v), want digest %s", got, created, err, want.ReceiptDigest)
	}
}

func assertControlOperationReceiptRowCountV1(
	t *testing.T,
	store *Store,
	want int,
) {
	t.Helper()
	var got int
	if err := store.db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM control_operation_receipts`,
	).Scan(&got); err != nil || got != want {
		t.Fatalf("receipt row count=%d, want %d, error=%v", got, want, err)
	}
}
