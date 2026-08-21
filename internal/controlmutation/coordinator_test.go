package controlmutation

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlconfirmation"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/internal/moduledisablecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type storeOutcomeV1 struct {
	record currentstore.StoredControlOperationReceiptV1
	err    error
}

type storeStubV1 struct {
	mutex sync.Mutex
	calls []string

	evaluation     currentstore.ModuleDisableControlOperationEvaluationV1
	evaluationErr  error
	evaluationHook func()
	resolveQueue   []storeOutcomeV1
	resolveRecord  currentstore.StoredControlOperationReceiptV1
	resolveErr     error
	commitRecord   currentstore.StoredControlOperationReceiptV1
	commitCreated  bool
	commitErr      error
}

func (store *storeStubV1) EvaluateModuleDisableControlOperationV1(
	_ context.Context,
	_ currentstore.EvaluateModuleDisableControlOperationInputV1,
) (currentstore.ModuleDisableControlOperationEvaluationV1, error) {
	store.mutex.Lock()
	store.calls = append(store.calls, "evaluate")
	result, err, hook := store.evaluation, store.evaluationErr, store.evaluationHook
	store.mutex.Unlock()
	if hook != nil {
		hook()
	}
	return result, err
}

func (store *storeStubV1) ResolveControlOperationReceiptV1(
	_ context.Context,
	_ []byte,
	_ string,
) (currentstore.StoredControlOperationReceiptV1, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.calls = append(store.calls, "resolve")
	if len(store.resolveQueue) != 0 {
		outcome := store.resolveQueue[0]
		store.resolveQueue = store.resolveQueue[1:]
		return outcome.record, outcome.err
	}
	return store.resolveRecord, store.resolveErr
}

func (store *storeStubV1) CommitModuleDisableControlOperationV1(
	_ context.Context,
	_ currentstore.CommitModuleDisableControlOperationInputV1,
) (currentstore.StoredControlOperationReceiptV1, bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.calls = append(store.calls, "commit")
	return store.commitRecord, store.commitCreated, store.commitErr
}

func (store *storeStubV1) resetCalls() {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.calls = nil
}

func (store *storeStubV1) callSnapshot() []string {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return append([]string(nil), store.calls...)
}

type testClockV1 struct {
	mutex sync.Mutex
	now   time.Time
}

func (clock *testClockV1) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.now
}

func (clock *testClockV1) Advance(delta time.Duration) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.now = clock.now.Add(delta)
}

type coordinatorFixtureV1 struct {
	coordinator  *CoordinatorV1
	store        *storeStubV1
	clock        *testClockV1
	permit       *controlsession.PermitV1
	issueInput   IssueModuleDisableConfirmationInputV1
	evaluation   currentstore.ModuleDisableControlOperationEvaluationV1
	confirmation *controlconfirmation.RegistryV1
}

func newCoordinatorFixtureV1(t *testing.T) coordinatorFixtureV1 {
	t.Helper()
	scope := controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeTenantV1,
		TenantID:      "tenant-controlmutation",
	}
	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(
		[]controlapicontract.ControlScopeV1{scope},
	)
	if err != nil {
		t.Fatal(err)
	}
	clock := &testClockV1{now: time.Unix(1_700_000_000, 0).UTC()}
	sessions, bootstrap, err := controlsession.NewRegistryV1(controlsession.RegistryConfigV1{
		PrincipalID:           "operator-controlmutation",
		AuthorizationRevision: 17,
		Capabilities: []controlapicontract.ControlCapabilityV1{
			controlapicontract.CapabilityOperateModulesV1,
		},
		ScopeSet: scopeSet,
		Entropy:  bytes.NewReader(testEntropyV1(4096)),
		Now:      clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := sessions.ExchangeBootstrap(bootstrap.Capability)
	zeroBytesV1(bootstrap.Capability)
	if err != nil {
		sessions.Close()
		t.Fatal(err)
	}
	permit, err := sessions.Admit(controlsession.AdmissionV1{
		SessionCredential: issued.SessionCredential,
		CSRFToken:         issued.CSRFToken,
		RequireCSRF:       true,
		Capability:        controlapicontract.CapabilityOperateModulesV1,
		Scope:             scope,
	})
	zeroBytesV1(issued.SessionCredential)
	zeroBytesV1(issued.CSRFToken)
	if err != nil {
		sessions.Close()
		t.Fatal(err)
	}
	confirmations, err := controlconfirmation.NewRegistryV1(
		controlconfirmation.RegistryConfigV1{
			Entropy: bytes.NewReader(testEntropyV1(4096)),
			Now:     clock.Now,
		},
	)
	if err != nil {
		permit.Release()
		sessions.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		confirmations.Close()
		permit.Release()
		sessions.Close()
	})

	evaluation, expected, body := testEvaluationV1(t, scope.TenantID)
	store := &storeStubV1{
		evaluation: evaluation,
		resolveErr: currentstore.ErrControlOperationReceiptNotFoundV1,
	}
	coordinator, err := NewCoordinatorV1(store, confirmations)
	if err != nil {
		t.Fatal(err)
	}
	return coordinatorFixtureV1{
		coordinator: coordinator,
		store:       store,
		clock:       clock,
		permit:      permit,
		issueInput: IssueModuleDisableConfirmationInputV1{
			Authorization:        permit,
			Scope:                scope,
			ExpectedPointer:      expected,
			Body:                 body,
			IdempotencyKeyDigest: testDigestV1("idempotency"),
		},
		evaluation:   evaluation,
		confirmation: confirmations,
	}
}

func testEvaluationV1(
	t *testing.T,
	tenantID string,
) (
	currentstore.ModuleDisableControlOperationEvaluationV1,
	controlapicontract.ExpectedResourceRefV1,
	moduledisablecontract.ModuleDisableDryRunBodyV1,
) {
	t.Helper()
	basis := controlapicontract.PublishedBasisRefV1{
		TenantID:        tenantID,
		PointerRevision: 7,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID: "control-7", Revision: 7, Digest: testDigestV1("control-7"),
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID: "catalog-7", Revision: 7, Digest: testDigestV1("catalog-7"),
		},
	}
	_, _, basisDigest, err := controlapicontract.NewPublishedBasisRefV1(basis)
	if err != nil {
		t.Fatal(err)
	}
	expected := controlapicontract.ExpectedResourceRefV1{
		Kind:       controlapicontract.ResourcePublishedPointerV1,
		ResourceID: tenantID,
		Revision:   basis.PointerRevision,
		Digest:     basisDigest,
	}
	body, inputCanonical, inputDigest, err :=
		moduledisablecontract.NewModuleDisableDryRunBodyV1(
			moduledisablecontract.ModuleDisableDryRunBodyV1{
				SchemaVersion:           moduledisablecontract.ModuleDisableDryRunBodySchemaVersionV1,
				ExpectedPointerRevision: basis.PointerRevision,
				BindingTarget: moduledisablecontract.ModuleBindingTargetV1{
					Kind:      moduledisablecontract.ModuleBindingTargetProfileV1,
					ProfileID: "profile-a",
				},
				InstanceID: "instance-a",
				Port: moduleapi.PortRef{
					Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1,
				},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	plan, planCanonical, planDigest, err := moduleapplyplan.FreezeProfileContextDisableV1(
		moduleapplyplan.ProfileContextDisableInputV1{
			TenantID: tenantID, ExpectedPointerRevision: basis.PointerRevision,
			ProfileID: body.BindingTarget.ProfileID, InstanceID: body.InstanceID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, evaluationCanonical, evaluationDigest, err :=
		moduledisablecontract.NewModuleDisableEvaluationV1(
			moduledisablecontract.ModuleDisableEvaluationV1{
				SchemaVersion: moduledisablecontract.ModuleDisableEvaluationSchemaVersionV1,
				Operation:     controlapicontract.OperationModuleDisableV1,
				InputDigest:   inputDigest,
				ExpectedRef:   expected,
				Projection: moduledisablecontract.ModuleDisableProjectionV1{
					Disposition:       moduledisablecontract.ModuleDisableNoChangeV1,
					PlanDigest:        planDigest,
					InstanceID:        body.InstanceID,
					PreconditionBasis: basis,
					ObservedBasis:     basis,
					CandidateBasis:    basis,
					CandidateState:    moduledisablecontract.ModuleDisableProjectedNotReservedV1,
					CatalogChange:     moduledisablecontract.ModuleDisableCatalogNoneV1,
				},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	return currentstore.ModuleDisableControlOperationEvaluationV1{
		Input: body, InputCanonical: inputCanonical, InputDigest: inputDigest,
		Plan: plan, PlanCanonical: planCanonical, PlanDigest: planDigest,
		Evaluation: evaluation, EvaluationCanonical: evaluationCanonical,
		EvaluationDigest: evaluationDigest,
	}, expected, body
}

func issueV1(
	t *testing.T,
	fixture coordinatorFixtureV1,
) IssueModuleDisableConfirmationResultV1 {
	t.Helper()
	result, err := fixture.coordinator.IssueModuleDisableConfirmationV1(
		context.Background(),
		fixture.issueInput,
	)
	if err != nil {
		t.Fatalf("IssueModuleDisableConfirmationV1: %v", err)
	}
	return result
}

func mutationInputV1(
	fixture coordinatorFixtureV1,
	issued IssueModuleDisableConfirmationResultV1,
) MutateModuleDisableInputV1 {
	return MutateModuleDisableInputV1{
		Authorization:             fixture.permit,
		Scope:                     fixture.issueInput.Scope,
		ExpectedPointer:           fixture.issueInput.ExpectedPointer,
		Body:                      fixture.issueInput.Body,
		IdempotencyKeyDigest:      fixture.issueInput.IdempotencyKeyDigest,
		OperationEvaluationDigest: issued.EvaluationDigest,
		Proof:                     append([]byte(nil), issued.Proof...),
	}
}

func storedRecordV1(
	t *testing.T,
	issued IssueModuleDisableConfirmationResultV1,
	status controlapicontract.ControlOperationStatusV1,
) currentstore.StoredControlOperationReceiptV1 {
	t.Helper()
	pre, post := issued.Request.ExpectedRef, issued.Request.ExpectedRef
	var domain *controlapicontract.DomainReceiptRefV1
	if status == controlapicontract.OperationStatusAppliedV1 {
		post.Revision++
		post.Digest = testDigestV1("post")
		domain = &controlapicontract.DomainReceiptRefV1{
			Kind:   controlapicontract.DomainReceiptModuleDisableV1,
			ID:     "module-disable-receipt-a",
			Digest: testDigestV1("domain-receipt"),
		}
	}
	receipt, canonical, digest, err := controlapicontract.NewControlOperationReceiptV1(
		controlapicontract.ControlOperationReceiptV1{
			SchemaVersion:         controlapicontract.ControlOperationReceiptSchemaVersionV1,
			RequestDigest:         issued.RequestDigest,
			Intent:                controlapicontract.OperationIntentMutateV1,
			IdempotencyKeyDigest:  issued.Request.IdempotencyKeyDigest,
			PrincipalID:           issued.Request.PrincipalID,
			ScopeDigest:           issued.Request.ScopeDigest,
			Operation:             controlapicontract.OperationModuleDisableV1,
			Status:                status,
			ErrorCode:             controlapicontract.ErrorNoneV1,
			PreRef:                &pre,
			PostRef:               &post,
			DomainReceipt:         domain,
			ReplayDisposition:     controlapicontract.ReplayReturnExactReceiptV1,
			CompletedAtUnixMicros: 1_700_000_000_000_000,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return currentstore.StoredControlOperationReceiptV1{
		ReceiptDigest:           digest,
		Status:                  status,
		Request:                 issued.Request,
		RequestCanonical:        append([]byte(nil), issued.RequestCanonical...),
		RequestDigest:           issued.RequestDigest,
		ControlReceipt:          receipt,
		ControlReceiptCanonical: canonical,
	}
}

func TestIssueIsEffectFreeAndProofIsExactlyBoundV1(t *testing.T) {
	fixture := newCoordinatorFixtureV1(t)
	issued := issueV1(t, fixture)
	if calls := fixture.store.callSnapshot(); !equalStringsV1(calls, []string{"evaluate"}) {
		t.Fatalf("calls=%v", calls)
	}
	if len(issued.Proof) != controlconfirmation.ProofBytesV1 ||
		issued.StatementDigest != issued.Request.ConfirmationDigest ||
		issued.EvaluationDigest != issued.Request.OperationEvaluationDigest ||
		bytes.Contains(issued.StatementCanonical, issued.Proof) ||
		bytes.Contains(issued.RequestCanonical, issued.Proof) ||
		bytes.Contains(issued.EvaluationCanonical, issued.Proof) {
		t.Fatal("issue did not return an exact proof-bound, proof-free contract set")
	}
	wantEvaluation := append([]byte(nil), issued.EvaluationCanonical...)
	fixture.store.evaluation.EvaluationCanonical[0] ^= 0xff
	if !bytes.Equal(issued.EvaluationCanonical, wantEvaluation) {
		t.Fatal("issue result aliases Store evaluation bytes")
	}
}

func TestIssueAcceptsExactWouldApplyEvaluationWithDetachedRemovalV1(t *testing.T) {
	fixture := newCoordinatorFixtureV1(t)
	before := fixture.evaluation.Evaluation.Projection.ObservedBasis
	candidates, err := moduleapplyplan.DeriveCandidateIDsV1(fixture.evaluation.PlanDigest)
	if err != nil {
		t.Fatal(err)
	}
	after := before
	after.PointerRevision++
	after.Control = controlapicontract.RevisionedDigestRefV1{
		ID: candidates.ControlSnapshotID, Revision: before.Control.Revision + 1,
		Digest: testDigestV1("would-control"),
	}
	after.Catalog = controlapicontract.RevisionedDigestRefV1{
		ID: candidates.CatalogGenerationID, Revision: before.Catalog.Revision + 1,
		Digest: testDigestV1("would-catalog"),
	}
	evaluation, canonical, digest, err := moduledisablecontract.NewModuleDisableEvaluationV1(
		moduledisablecontract.ModuleDisableEvaluationV1{
			SchemaVersion: moduledisablecontract.ModuleDisableEvaluationSchemaVersionV1,
			Operation:     controlapicontract.OperationModuleDisableV1,
			InputDigest:   fixture.evaluation.InputDigest,
			ExpectedRef:   fixture.issueInput.ExpectedPointer,
			Projection: moduledisablecontract.ModuleDisableProjectionV1{
				Disposition:       moduledisablecontract.ModuleDisableWouldApplyV1,
				PlanDigest:        fixture.evaluation.PlanDigest,
				InstanceID:        fixture.issueInput.Body.InstanceID,
				PreconditionBasis: before,
				ObservedBasis:     before,
				CandidateBasis:    after,
				CandidateState:    moduledisablecontract.ModuleDisableProjectedNotReservedV1,
				BindingRemoval: &moduledisablecontract.ModuleDisableBindingRemovalV1{
					Target:              fixture.issueInput.Body.BindingTarget,
					Port:                fixture.issueInput.Body.Port,
					ConfigRef:           testDigestV1("would-config"),
					AuthorityCeilingRef: testDigestV1("would-authority"),
					StaticContextRefs:   []string{},
					FailurePolicy:       moduleapi.FailureOptional,
				},
				CatalogChange: moduledisablecontract.ModuleDisableCatalogRetainInstanceV1,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.store.evaluation.Evaluation = evaluation
	fixture.store.evaluation.EvaluationCanonical = canonical
	fixture.store.evaluation.EvaluationDigest = digest

	issued := issueV1(t, fixture)
	defer zeroBytesV1(issued.Proof)
	if issued.Evaluation.Projection.BindingRemoval == nil ||
		issued.EvaluationDigest != digest {
		t.Fatalf("WOULD_APPLY issue drifted: %+v", issued)
	}
}

func TestMutateDurableHitRequiresNeitherProofNorEvaluationV1(t *testing.T) {
	fixture := newCoordinatorFixtureV1(t)
	issued := issueV1(t, fixture)
	fixture.store.resetCalls()
	fixture.store.resolveErr = nil
	fixture.store.resolveRecord = storedRecordV1(
		t, issued, controlapicontract.OperationStatusNoChangeV1,
	)
	input := mutationInputV1(fixture, issued)
	input.Proof = nil
	result, err := fixture.coordinator.MutateModuleDisableV1(context.Background(), input)
	if err != nil || result.ReceiptDigest != fixture.store.resolveRecord.ReceiptDigest {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if calls := fixture.store.callSnapshot(); !equalStringsV1(calls, []string{"resolve"}) {
		t.Fatalf("calls=%v", calls)
	}
}

func TestMutateMissValidOutcomesAndConsumesProofV1(t *testing.T) {
	for _, status := range []controlapicontract.ControlOperationStatusV1{
		controlapicontract.OperationStatusNoChangeV1,
		controlapicontract.OperationStatusAppliedV1,
	} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newCoordinatorFixtureV1(t)
			issued := issueV1(t, fixture)
			fixture.store.resetCalls()
			fixture.store.commitRecord = storedRecordV1(t, issued, status)
			fixture.store.commitCreated = true
			input := mutationInputV1(fixture, issued)
			callerProof := append([]byte(nil), input.Proof...)
			result, err := fixture.coordinator.MutateModuleDisableV1(
				context.Background(), input,
			)
			if err != nil || result.Receipt.Status != status || !result.Created {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if !bytes.Equal(input.Proof, callerProof) {
				t.Fatal("coordinator cleared caller-owned proof")
			}
			if calls := fixture.store.callSnapshot(); !equalStringsV1(
				calls, []string{"resolve", "evaluate", "commit"},
			) {
				t.Fatalf("calls=%v", calls)
			}
			fixture.store.resetCalls()
			_, err = fixture.coordinator.MutateModuleDisableV1(context.Background(), input)
			if !errors.Is(err, ErrConfirmationRejected) {
				t.Fatalf("used proof error=%v", err)
			}
			if calls := fixture.store.callSnapshot(); !equalStringsV1(calls, []string{"resolve"}) {
				t.Fatalf("used proof calls=%v", calls)
			}
		})
	}
}

func TestMutateRejectsMissingOrWrongProofBeforeEvaluationV1(t *testing.T) {
	for _, name := range []string{"missing", "wrong"} {
		t.Run(name, func(t *testing.T) {
			fixture := newCoordinatorFixtureV1(t)
			issued := issueV1(t, fixture)
			fixture.store.resetCalls()
			input := mutationInputV1(fixture, issued)
			if name == "missing" {
				input.Proof = nil
			} else {
				input.Proof = bytes.Repeat([]byte{0xee}, controlconfirmation.ProofBytesV1)
			}
			_, err := fixture.coordinator.MutateModuleDisableV1(context.Background(), input)
			if !errors.Is(err, ErrConfirmationRejected) {
				t.Fatalf("error=%v", err)
			}
			if calls := fixture.store.callSnapshot(); !equalStringsV1(calls, []string{"resolve"}) {
				t.Fatalf("calls=%v", calls)
			}
		})
	}
}

func TestEvaluationDriftReleasesClaimForExactRetryV1(t *testing.T) {
	fixture := newCoordinatorFixtureV1(t)
	issued := issueV1(t, fixture)
	fixture.store.resetCalls()
	fixture.store.evaluation.EvaluationDigest = testDigestV1("drift")
	input := mutationInputV1(fixture, issued)
	_, err := fixture.coordinator.MutateModuleDisableV1(context.Background(), input)
	if !errors.Is(err, ErrIntegrityFailure) {
		t.Fatalf("drift error=%v", err)
	}
	fixture.store.evaluation = fixture.evaluation
	fixture.store.commitRecord = storedRecordV1(
		t, issued, controlapicontract.OperationStatusNoChangeV1,
	)
	fixture.store.commitCreated = true
	fixture.store.resetCalls()
	if _, err := fixture.coordinator.MutateModuleDisableV1(
		context.Background(), input,
	); err != nil {
		t.Fatalf("retry after zero-effect drift: %v", err)
	}
}

func TestFinalAuthorizationFailureAfterEvaluationSkipsCommitV1(t *testing.T) {
	fixture := newCoordinatorFixtureV1(t)
	issued := issueV1(t, fixture)
	fixture.store.resetCalls()
	fixture.store.evaluationHook = func() {
		fixture.clock.Advance(controlsession.SessionAbsoluteLifetimeV1)
	}
	_, err := fixture.coordinator.MutateModuleDisableV1(
		context.Background(), mutationInputV1(fixture, issued),
	)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("error=%v", err)
	}
	if calls := fixture.store.callSnapshot(); !equalStringsV1(
		calls, []string{"resolve", "evaluate"},
	) {
		t.Fatalf("calls=%v", calls)
	}
}

func TestAnyCommitReturnConsumesProofV1(t *testing.T) {
	fixture := newCoordinatorFixtureV1(t)
	issued := issueV1(t, fixture)
	fixture.store.resetCalls()
	fixture.store.commitErr = currentstore.ErrOwnerActive
	input := mutationInputV1(fixture, issued)
	_, err := fixture.coordinator.MutateModuleDisableV1(context.Background(), input)
	if !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("commit error=%v", err)
	}
	fixture.store.commitErr = nil
	fixture.store.resetCalls()
	_, err = fixture.coordinator.MutateModuleDisableV1(context.Background(), input)
	if !errors.Is(err, ErrConfirmationRejected) {
		t.Fatalf("retry error=%v", err)
	}
	if calls := fixture.store.callSnapshot(); !equalStringsV1(calls, []string{"resolve"}) {
		t.Fatalf("retry calls=%v", calls)
	}
}

func TestResolveConflictPrecedesClaimAndEvaluationV1(t *testing.T) {
	fixture := newCoordinatorFixtureV1(t)
	issued := issueV1(t, fixture)
	fixture.store.resetCalls()
	fixture.store.resolveErr = currentstore.ErrControlOperationReceiptConflictV1
	input := mutationInputV1(fixture, issued)
	_, err := fixture.coordinator.MutateModuleDisableV1(context.Background(), input)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	if calls := fixture.store.callSnapshot(); !equalStringsV1(calls, []string{"resolve"}) {
		t.Fatalf("calls=%v", calls)
	}
	fixture.store.resolveErr = currentstore.ErrControlOperationReceiptNotFoundV1
	fixture.store.commitRecord = storedRecordV1(
		t, issued, controlapicontract.OperationStatusNoChangeV1,
	)
	fixture.store.commitCreated = true
	if _, err := fixture.coordinator.MutateModuleDisableV1(
		context.Background(), input,
	); err != nil {
		t.Fatalf("proof was claimed before conflict: %v", err)
	}
}

func TestEvaluateFailureConcurrentExactReceiptWinsV1(t *testing.T) {
	fixture := newCoordinatorFixtureV1(t)
	issued := issueV1(t, fixture)
	record := storedRecordV1(t, issued, controlapicontract.OperationStatusNoChangeV1)
	fixture.store.resetCalls()
	fixture.store.evaluationErr = currentstore.ErrPublicationConflict
	fixture.store.resolveQueue = []storeOutcomeV1{
		{err: currentstore.ErrControlOperationReceiptNotFoundV1},
		{record: record},
	}
	result, err := fixture.coordinator.MutateModuleDisableV1(
		context.Background(), mutationInputV1(fixture, issued),
	)
	if err != nil || result.ReceiptDigest != record.ReceiptDigest {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if calls := fixture.store.callSnapshot(); !equalStringsV1(
		calls, []string{"resolve", "evaluate", "resolve"},
	) {
		t.Fatalf("calls=%v", calls)
	}
}

func TestErrorMappingsAndTypedNilStoreV1(t *testing.T) {
	for _, test := range []struct {
		input error
		want  error
	}{
		{context.Canceled, ErrCancelled},
		{currentstore.ErrControlOperationReceiptConflictV1, ErrIdempotencyConflict},
		{currentstore.ErrControlOperationReceiptCapacityV1, ErrResourceExhausted},
		{currentstore.ErrModuleDisableControlOperationIneligibleV1, ErrMutationIneligible},
		{currentstore.ErrPublicationConflict, ErrRevisionConflict},
		{currentstore.ErrInvalidControlOperationReceiptV1, ErrInvalidRequest},
		{currentstore.ErrControlOperationReceiptIntegrityV1, ErrIntegrityFailure},
		{currentstore.ErrOwnerActive, ErrStoreBusy},
		{currentstore.ErrStoreClosed, ErrStoreUnavailable},
	} {
		if got := mapStoreErrorV1(test.input); !errors.Is(got, test.want) {
			t.Fatalf("mapStoreErrorV1(%v)=%v want %v", test.input, got, test.want)
		}
	}
	fixture := newCoordinatorFixtureV1(t)
	var typedNil *storeStubV1
	if _, err := NewCoordinatorV1(typedNil, fixture.confirmation); !errors.Is(
		err, ErrInvalidConfiguration,
	) {
		t.Fatalf("typed nil error=%v", err)
	}
}

func testDigestV1(label string) string {
	return moduleapi.Digest("freeagent.controlmutation.test/v1", []byte(label))
}

func testEntropyV1(size int) []byte {
	result := make([]byte, size)
	for index := range result {
		result[index] = byte(index*31 + index/251 + 17)
	}
	return result
}

func equalStringsV1(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
