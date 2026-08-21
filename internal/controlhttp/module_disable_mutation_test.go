package controlhttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const mutationIdempotencyKeyV1 = "module-disable-case-0001"

type fakeModuleDisableMutationServicesV1 struct {
	mutex       sync.Mutex
	issueCalls  int
	mutateCalls int
	lastIssue   IssueModuleDisableConfirmationInputV1
	lastMutate  MutateModuleDisableInputV1
	issue       func(context.Context, IssueModuleDisableConfirmationInputV1) (IssueModuleDisableConfirmationResultV1, error)
	mutate      func(context.Context, MutateModuleDisableInputV1) (MutateModuleDisableResultV1, error)
}

func (service *fakeModuleDisableMutationServicesV1) IssueModuleDisableConfirmationV1(
	ctx context.Context,
	input IssueModuleDisableConfirmationInputV1,
) (IssueModuleDisableConfirmationResultV1, error) {
	service.mutex.Lock()
	service.issueCalls++
	service.lastIssue = input
	function := service.issue
	service.mutex.Unlock()
	if function != nil {
		return function(ctx, input)
	}
	return basicModuleDisableConfirmationResultV1(input)
}

func (service *fakeModuleDisableMutationServicesV1) MutateModuleDisableV1(
	ctx context.Context,
	input MutateModuleDisableInputV1,
) (MutateModuleDisableResultV1, error) {
	service.mutex.Lock()
	service.mutateCalls++
	service.lastMutate = input
	function := service.mutate
	service.mutex.Unlock()
	if function != nil {
		return function(ctx, input)
	}
	return basicModuleDisableMutationResultV1(input)
}

func (service *fakeModuleDisableMutationServicesV1) snapshot() (
	int,
	int,
	IssueModuleDisableConfirmationInputV1,
	MutateModuleDisableInputV1,
) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	return service.issueCalls, service.mutateCalls, service.lastIssue, service.lastMutate
}

func newModuleDisableMutationFixtureV1(
	t *testing.T,
) (*handlerFixtureV1, *fakeModuleDisableMutationServicesV1) {
	t.Helper()
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{tenantScopeV1()},
		true,
	)
	service := &fakeModuleDisableMutationServicesV1{}
	handler, err := NewHandlerV1(ConfigV1{
		Authority:                 testAuthorityV1,
		Registry:                  fixture.registry,
		Modules:                   fixture.service,
		ModuleDisableDryRun:       fixture.disable,
		ModuleDisableConfirmation: service,
		ModuleDisableMutation:     service,
		Cursor:                    fixture.codec,
		Now:                       func() time.Time { return testNowV1.Add(time.Second) },
		Entropy: bytes.NewReader(
			bytes.Repeat([]byte{0x6b}, correlationBytesV1),
		),
	})
	if err != nil {
		t.Fatalf("mutation handler: %v", err)
	}
	fixture.handler = handler
	return fixture, service
}

func narrowModuleDisableBodyV1(t *testing.T) []byte {
	t.Helper()
	_, canonical, _, err := controlapp.NewModuleDisableDryRunBodyV1(
		controlapp.ModuleDisableDryRunBodyV1{
			SchemaVersion:           controlapp.ModuleDisableDryRunBodySchemaVersionV1,
			ExpectedPointerRevision: 7,
			BindingTarget: controlapp.ModuleBindingTargetV1{
				Kind:      controlapp.ModuleBindingTargetProfileV1,
				ProfileID: "profile-a",
			},
			InstanceID: "instance-a",
			Port: moduleapi.PortRef{
				Name:         moduleapi.PortNameContextProvide,
				ExactVersion: moduleapi.PortVersionV1,
			},
		},
	)
	if err != nil {
		t.Fatalf("narrow body: %v", err)
	}
	return canonical
}

func (fixture *handlerFixtureV1) moduleDisableMutationRequestV1(
	t *testing.T,
	path string,
	includeEvaluation bool,
	includeProof bool,
) *http.Request {
	t.Helper()
	request := newRequestV1(
		http.MethodPost,
		path,
		bytes.NewReader(narrowModuleDisableBodyV1(t)),
	)
	request.Header.Set("Origin", "http://"+testAuthorityV1)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(ScopeKindHeaderV1, string(controlapicontract.ScopeTenantV1))
	request.Header.Set(TenantIDHeaderV1, testTenantV1)
	basis := basicPublishedBasisV1(testTenantV1, 7)
	pointer, err := controlapp.PublishedPointerRefV1(basis)
	if err != nil {
		t.Fatalf("pointer: %v", err)
	}
	request.Header.Set("If-Match", `"`+pointer.Digest+`"`)
	request.Header.Set("Idempotency-Key", mutationIdempotencyKeyV1)
	request.Header.Set(CSRFHeaderV1, fixture.csrf)
	request.AddCookie(&http.Cookie{Name: SessionCookieV1, Value: fixture.credential})
	if includeEvaluation {
		inputBody, _, inputDigest, err := controlapp.NewModuleDisableDryRunBodyV1(
			controlapp.ModuleDisableDryRunBodyV1{
				SchemaVersion:           controlapp.ModuleDisableDryRunBodySchemaVersionV1,
				ExpectedPointerRevision: 7,
				BindingTarget: controlapp.ModuleBindingTargetV1{
					Kind:      controlapp.ModuleBindingTargetProfileV1,
					ProfileID: "profile-a",
				},
				InstanceID: "instance-a",
				Port: moduleapi.PortRef{
					Name:         moduleapi.PortNameContextProvide,
					ExactVersion: moduleapi.PortVersionV1,
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		evaluation, _, digest, err := moduleDisableEvaluationForInputV1(
			inputBody,
			inputDigest,
			pointer,
		)
		_ = evaluation
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(OperationEvaluationDigestHeaderV1, digest)
	}
	if includeProof {
		request.Header.Set(
			ConfirmationHeaderV1,
			base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x7c}, ConfirmationProofBytesV1)),
		)
	}
	return request
}

func moduleDisableEvaluationForInputV1(
	body controlapp.ModuleDisableDryRunBodyV1,
	inputDigest string,
	expected controlapicontract.ExpectedResourceRefV1,
) (controlapp.ModuleDisableEvaluationV1, []byte, string, error) {
	basis := basicPublishedBasisV1(expected.ResourceID, expected.Revision)
	return controlapp.NewModuleDisableEvaluationV1(controlapp.ModuleDisableEvaluationV1{
		SchemaVersion: controlapp.ModuleDisableEvaluationSchemaVersionV1,
		Operation:     controlapicontract.OperationModuleDisableV1,
		InputDigest:   inputDigest,
		ExpectedRef:   expected,
		Projection: controlapp.ModuleDisableProjectionV1{
			Disposition:       controlapp.ModuleDisableNoChangeV1,
			PlanDigest:        strings.Repeat("3", 64),
			InstanceID:        body.InstanceID,
			PreconditionBasis: basis,
			ObservedBasis:     basis,
			CandidateBasis:    basis,
			CandidateState:    controlapp.ModuleDisableProjectedNotReservedV1,
			CatalogChange:     controlapp.ModuleDisableCatalogNoneV1,
		},
	})
}

func stableModuleDisableMutationRequestV1(
	authorization *controlsession.PermitV1,
	scope controlapicontract.ControlScopeV1,
	expected controlapicontract.ExpectedResourceRefV1,
	inputDigest string,
	idempotencyKeyDigest string,
	evaluationDigest string,
) (
	controlapicontract.ControlConfirmationStatementV1,
	string,
	controlapicontract.ControlOperationRequestV1,
	string,
	error,
) {
	_, _, scopeDigest, err := controlapicontract.NewControlScopeV1(scope)
	if err != nil {
		return controlapicontract.ControlConfirmationStatementV1{}, "",
			controlapicontract.ControlOperationRequestV1{}, "", err
	}
	statement, statementCanonical, statementDigest, err :=
		controlapicontract.NewControlConfirmationStatementV1(
			controlapicontract.ControlConfirmationStatementV1{
				SchemaVersion:             controlapicontract.ControlConfirmationStatementSchemaVersionV1,
				PrincipalID:               authorization.Session().PrincipalID,
				Capability:                controlapicontract.CapabilityOperateModulesV1,
				Intent:                    controlapicontract.OperationIntentMutateV1,
				Operation:                 controlapicontract.OperationModuleDisableV1,
				Scope:                     scope,
				ScopeDigest:               scopeDigest,
				IdempotencyKeyDigest:      idempotencyKeyDigest,
				InputDigest:               inputDigest,
				OperationEvaluationDigest: evaluationDigest,
				ExpectedRef:               expected,
			},
		)
	clear(statementCanonical)
	if err != nil {
		return controlapicontract.ControlConfirmationStatementV1{}, "",
			controlapicontract.ControlOperationRequestV1{}, "", err
	}
	request, requestCanonical, requestDigest, err :=
		controlapicontract.NewControlOperationRequestV1(
			controlapicontract.ControlOperationRequestV1{
				SchemaVersion:             controlapicontract.ControlOperationRequestSchemaVersionV1,
				PrincipalID:               authorization.Session().PrincipalID,
				Capability:                controlapicontract.CapabilityOperateModulesV1,
				Scope:                     scope,
				Operation:                 controlapicontract.OperationModuleDisableV1,
				Intent:                    controlapicontract.OperationIntentMutateV1,
				IdempotencyKeyDigest:      idempotencyKeyDigest,
				InputDigest:               inputDigest,
				OperationEvaluationDigest: evaluationDigest,
				ExpectedRef:               expected,
				ConfirmationDigest:        statementDigest,
			},
		)
	clear(requestCanonical)
	if err != nil {
		return controlapicontract.ControlConfirmationStatementV1{}, "",
			controlapicontract.ControlOperationRequestV1{}, "", err
	}
	return statement, statementDigest, request, requestDigest, nil
}

func basicModuleDisableConfirmationResultV1(
	input IssueModuleDisableConfirmationInputV1,
) (IssueModuleDisableConfirmationResultV1, error) {
	_, bodyCanonical, inputDigest, err := controlapp.NewModuleDisableDryRunBodyV1(input.Body)
	clear(bodyCanonical)
	if err != nil {
		return IssueModuleDisableConfirmationResultV1{}, err
	}
	evaluation, evaluationCanonical, evaluationDigest, err :=
		moduleDisableEvaluationForInputV1(input.Body, inputDigest, input.ExpectedPointer)
	clear(evaluationCanonical)
	if err != nil {
		return IssueModuleDisableConfirmationResultV1{}, err
	}
	statement, statementDigest, request, requestDigest, err :=
		stableModuleDisableMutationRequestV1(
			input.Authorization,
			input.Scope,
			input.ExpectedPointer,
			inputDigest,
			input.IdempotencyKeyDigest,
			evaluationDigest,
		)
	if err != nil {
		return IssueModuleDisableConfirmationResultV1{}, err
	}
	return IssueModuleDisableConfirmationResultV1{
		SchemaVersion:       ModuleDisableConfirmationResultSchemaVersionV1,
		Request:             request,
		RequestDigest:       requestDigest,
		Evaluation:          evaluation,
		EvaluationDigest:    evaluationDigest,
		Statement:           statement,
		StatementDigest:     statementDigest,
		ConfirmationProof:   bytes.Repeat([]byte{0x7c}, ConfirmationProofBytesV1),
		ExpiresAtUnixMicros: uint64(testNowV1.Add(time.Minute).UnixMicro()),
	}, nil
}

func basicModuleDisableMutationResultV1(
	input MutateModuleDisableInputV1,
) (MutateModuleDisableResultV1, error) {
	_, bodyCanonical, inputDigest, err := controlapp.NewModuleDisableDryRunBodyV1(input.Body)
	clear(bodyCanonical)
	if err != nil {
		return MutateModuleDisableResultV1{}, err
	}
	_, _, request, requestDigest, err := stableModuleDisableMutationRequestV1(
		input.Authorization,
		input.Scope,
		input.ExpectedPointer,
		inputDigest,
		input.IdempotencyKeyDigest,
		input.OperationEvaluationDigest,
	)
	if err != nil {
		return MutateModuleDisableResultV1{}, err
	}
	pre := input.ExpectedPointer
	post := input.ExpectedPointer
	receipt, receiptCanonical, receiptDigest, err :=
		controlapicontract.NewControlOperationReceiptV1(
			controlapicontract.ControlOperationReceiptV1{
				SchemaVersion:         controlapicontract.ControlOperationReceiptSchemaVersionV1,
				RequestDigest:         requestDigest,
				Intent:                controlapicontract.OperationIntentMutateV1,
				IdempotencyKeyDigest:  input.IdempotencyKeyDigest,
				PrincipalID:           request.PrincipalID,
				ScopeDigest:           request.ScopeDigest,
				Operation:             request.Operation,
				Status:                controlapicontract.OperationStatusNoChangeV1,
				ErrorCode:             controlapicontract.ErrorNoneV1,
				PreRef:                &pre,
				PostRef:               &post,
				ReplayDisposition:     controlapicontract.ReplayReturnExactReceiptV1,
				CompletedAtUnixMicros: uint64(testNowV1.Add(2 * time.Second).UnixMicro()),
			},
		)
	clear(receiptCanonical)
	if err != nil {
		return MutateModuleDisableResultV1{}, err
	}
	return MutateModuleDisableResultV1{
		SchemaVersion: ModuleDisableMutationResultSchemaVersionV1,
		Request:       request,
		RequestDigest: requestDigest,
		Receipt:       receipt,
		ReceiptDigest: receiptDigest,
	}, nil
}

func TestModuleDisableConfirmationAndMutationExactTransportV1(t *testing.T) {
	t.Parallel()
	fixture, service := newModuleDisableMutationFixtureV1(t)

	confirmation := fixture.moduleDisableMutationRequestV1(
		t,
		ModuleDisableConfirmationPathV1,
		false,
		false,
	)
	confirmationRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(confirmationRecorder, confirmation)
	if confirmationRecorder.Code != http.StatusOK ||
		confirmationRecorder.Header().Get("Cache-Control") != "no-store" ||
		confirmationRecorder.Header().Get("Content-Type") != "application/json" ||
		confirmationRecorder.Header().Get(ConfirmationHeaderV1) != "" {
		t.Fatalf(
			"confirmation status=%d cache=%q body=%s",
			confirmationRecorder.Code,
			confirmationRecorder.Header().Get("Cache-Control"),
			confirmationRecorder.Body.String(),
		)
	}
	var confirmationResponse moduleDisableConfirmationResponseV1
	if err := json.Unmarshal(confirmationRecorder.Body.Bytes(), &confirmationResponse); err != nil {
		t.Fatalf("decode confirmation: %v", err)
	}
	if confirmationResponse.Request.ExpectedRef.Revision != 7 ||
		confirmationResponse.Request.ExpectedRef != confirmationResponse.Statement.ExpectedRef ||
		confirmationResponse.Request.OperationEvaluationDigest !=
			confirmationResponse.EvaluationDigest ||
		confirmationResponse.ConfirmationProof == "" {
		t.Fatalf("confirmation response lost exact bindings: %+v", confirmationResponse)
	}
	if strings.Contains(confirmationRecorder.Body.String(), mutationIdempotencyKeyV1) {
		t.Fatal("raw idempotency key leaked in confirmation response")
	}

	mutation := fixture.moduleDisableMutationRequestV1(
		t,
		ModuleDisableMutatePathV1,
		true,
		true,
	)
	mutationRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(mutationRecorder, mutation)
	if mutationRecorder.Code != http.StatusOK ||
		mutationRecorder.Header().Get("Cache-Control") != "no-store" ||
		mutationRecorder.Header().Get("Content-Type") != "application/json" ||
		mutationRecorder.Header().Get(ConfirmationHeaderV1) != "" {
		t.Fatalf(
			"mutation status=%d cache=%q body=%s",
			mutationRecorder.Code,
			mutationRecorder.Header().Get("Cache-Control"),
			mutationRecorder.Body.String(),
		)
	}
	if strings.Contains(mutationRecorder.Body.String(), mutationIdempotencyKeyV1) ||
		strings.Contains(
			mutationRecorder.Body.String(),
			base64.RawURLEncoding.EncodeToString(
				bytes.Repeat([]byte{0x7c}, ConfirmationProofBytesV1),
			),
		) {
		t.Fatal("raw mutation authority leaked in response")
	}
	issueCalls, mutateCalls, lastIssue, lastMutate := service.snapshot()
	if issueCalls != 1 || mutateCalls != 1 ||
		lastIssue.ExpectedPointer.Revision != 7 ||
		lastMutate.ExpectedPointer.Revision != 7 ||
		lastIssue.ExpectedPointer != lastMutate.ExpectedPointer ||
		lastIssue.Authorization == nil || lastMutate.Authorization == nil ||
		len(lastMutate.ConfirmationProof) != ConfirmationProofBytesV1 {
		t.Fatalf(
			"service calls issue=%d mutate=%d issue=%+v mutate=%+v",
			issueCalls,
			mutateCalls,
			lastIssue,
			lastMutate,
		)
	}

	// An exact durable retry is intentionally admitted without the old proof;
	// lookup-before-proof is owned by the application coordinator.
	retry := fixture.moduleDisableMutationRequestV1(
		t,
		ModuleDisableMutatePathV1,
		true,
		false,
	)
	retryRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(retryRecorder, retry)
	if retryRecorder.Code != http.StatusOK {
		t.Fatalf("proofless retry status=%d body=%s", retryRecorder.Code, retryRecorder.Body.String())
	}
	_, mutateCalls, _, lastMutate = service.snapshot()
	if mutateCalls != 2 || lastMutate.ConfirmationProof != nil {
		t.Fatalf("proofless retry input calls=%d proof=%x", mutateCalls, lastMutate.ConfirmationProof)
	}
}

func TestModuleDisableMutateEvaluationDigestPreconditionV1(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
		status int
		code   controlapicontract.ErrorCodeV1
	}{
		{
			name:   "missing",
			mutate: func(request *http.Request) { request.Header.Del(OperationEvaluationDigestHeaderV1) },
			status: http.StatusPreconditionRequired,
			code:   controlapicontract.ErrorPreconditionRequiredV1,
		},
		{
			name: "malformed",
			mutate: func(request *http.Request) {
				request.Header.Set(OperationEvaluationDigestHeaderV1, strings.Repeat("A", 64))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "multiple",
			mutate: func(request *http.Request) {
				request.Header.Add(OperationEvaluationDigestHeaderV1, strings.Repeat("4", 64))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture, service := newModuleDisableMutationFixtureV1(t)
			request := fixture.moduleDisableMutationRequestV1(
				t,
				ModuleDisableMutatePathV1,
				true,
				false,
			)
			test.mutate(request)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(t, recorder, test.status, test.code)
			_, mutateCalls, _, _ := service.snapshot()
			if mutateCalls != 0 {
				t.Fatalf("invalid precondition reached service: %d", mutateCalls)
			}
		})
	}
}

func TestModuleDisableMutateAuthenticatesBeforeEvaluationFactsV1(t *testing.T) {
	t.Parallel()
	fixture, service := newModuleDisableMutationFixtureV1(t)
	request := fixture.moduleDisableMutationRequestV1(
		t,
		ModuleDisableMutatePathV1,
		false,
		false,
	)
	request.Header.Del("Cookie")
	request.Header.Del(CSRFHeaderV1)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	assertErrorCodeV1(
		t,
		recorder,
		http.StatusUnauthorized,
		controlapicontract.ErrorUnauthenticatedV1,
	)
	_, mutateCalls, _, _ := service.snapshot()
	if mutateCalls != 0 {
		t.Fatalf("unauthenticated request reached mutation service: %d", mutateCalls)
	}
}

func TestModuleDisableMutationRoutesRejectAmbiguousTransportV1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		path   string
		mutate bool
		change func(*http.Request)
		status int
		code   controlapicontract.ErrorCodeV1
	}{
		{
			name:   "confirmation query",
			path:   ModuleDisableConfirmationPathV1,
			change: func(request *http.Request) { request.URL.RawQuery = "retry=1"; request.RequestURI += "?retry=1" },
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "confirmation multiple key",
			path:   ModuleDisableConfirmationPathV1,
			change: func(request *http.Request) { request.Header.Add("Idempotency-Key", "other-module-key-0002") },
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "confirmation forbidden proof",
			path:   ModuleDisableConfirmationPathV1,
			change: func(request *http.Request) { request.Header.Set(ConfirmationHeaderV1, "forbidden") },
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "confirmation forbidden evaluation",
			path: ModuleDisableConfirmationPathV1,
			change: func(request *http.Request) {
				request.Header.Set(OperationEvaluationDigestHeaderV1, strings.Repeat("4", 64))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "confirmation workspace scope",
			path: ModuleDisableConfirmationPathV1,
			change: func(request *http.Request) {
				request.Header.Set(ScopeKindHeaderV1, string(controlapicontract.ScopeWorkspaceV1))
				request.Header.Set(WorkspaceIDHeaderV1, testWorkspaceV1)
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "confirmation wrong port",
			path: ModuleDisableConfirmationPathV1,
			change: func(request *http.Request) {
				replaceRequestBodyV1(request, moduleDisableBodyV1(
					t,
					controlapp.ModuleBindingTargetV1{
						Kind:      controlapp.ModuleBindingTargetProfileV1,
						ProfileID: "profile-a",
					},
				))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "confirmation noncanonical body",
			path: ModuleDisableConfirmationPathV1,
			change: func(request *http.Request) {
				replaceRequestBodyV1(request, append(narrowModuleDisableBodyV1(t), '\n'))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "mutate query",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) { request.URL.RawQuery = "retry=1"; request.RequestURI += "?retry=1" },
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "mutate multiple proof",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) {
				request.Header.Add(ConfirmationHeaderV1, base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, ConfirmationProofBytesV1)))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "mutate unknown authority header",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) { request.Header.Set("X-FreeAgent-Override", "true") },
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "mutate whitespace key",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) { request.Header.Set("Idempotency-Key", " invalid-key-0001") },
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "mutate short key",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) { request.Header.Set("Idempotency-Key", "too-short") },
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "mutate non-ascii key",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) { request.Header.Set("Idempotency-Key", "module-disable-é-0001") },
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "mutate workspace scope",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) {
				request.Header.Set(ScopeKindHeaderV1, string(controlapicontract.ScopeWorkspaceV1))
				request.Header.Set(WorkspaceIDHeaderV1, testWorkspaceV1)
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "mutate wrong port",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) {
				replaceRequestBodyV1(request, moduleDisableBodyV1(
					t,
					controlapp.ModuleBindingTargetV1{
						Kind:      controlapp.ModuleBindingTargetProfileV1,
						ProfileID: "profile-a",
					},
				))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "mutate noncanonical body",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) {
				replaceRequestBodyV1(request, append(narrowModuleDisableBodyV1(t), '\n'))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name:   "mutate wrong origin",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) { request.Header.Set("Origin", "http://127.0.0.1:1") },
			status: http.StatusForbidden,
			code:   controlapicontract.ErrorForbiddenV1,
		},
		{
			name:   "mutate missing csrf",
			path:   ModuleDisableMutatePathV1,
			mutate: true,
			change: func(request *http.Request) { request.Header.Del(CSRFHeaderV1) },
			status: http.StatusUnauthorized,
			code:   controlapicontract.ErrorUnauthenticatedV1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture, service := newModuleDisableMutationFixtureV1(t)
			request := fixture.moduleDisableMutationRequestV1(
				t,
				test.path,
				test.mutate,
				test.mutate,
			)
			test.change(request)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(t, recorder, test.status, test.code)
			issueCalls, mutateCalls, _, _ := service.snapshot()
			if issueCalls != 0 || mutateCalls != 0 {
				t.Fatalf("ambiguous transport reached service: issue=%d mutate=%d", issueCalls, mutateCalls)
			}
		})
	}
}

func TestModuleDisableMutationRouteSiblingsAreOpaqueNotFoundV1(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		ModuleDisableConfirmationPathV1 + "/sibling",
		ModuleDisableMutatePathV1 + "/sibling",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			fixture, service := newModuleDisableMutationFixtureV1(t)
			request := newRequestV1(http.MethodPost, path, strings.NewReader("x"))
			request.Header.Set("Origin", "http://"+testAuthorityV1)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Cookie", "must-not-be-inspected")
			request.Body = panicReadCloserV1{}
			request.ContentLength = 1
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(
				t,
				recorder,
				http.StatusNotFound,
				controlapicontract.ErrorNotFoundV1,
			)
			issueCalls, mutateCalls, _, _ := service.snapshot()
			if issueCalls != 0 || mutateCalls != 0 {
				t.Fatalf(
					"route sibling reached service: issue=%d mutate=%d",
					issueCalls,
					mutateCalls,
				)
			}
		})
	}
}

type panicReadCloserV1 struct{}

func (panicReadCloserV1) Read([]byte) (int, error) {
	panic("route sibling read body")
}

func (panicReadCloserV1) Close() error { return nil }

func replaceRequestBodyV1(request *http.Request, body []byte) {
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
}

func TestModuleDisableMutateMissingProofCanReachLookupBeforeProofV1(t *testing.T) {
	t.Parallel()
	fixture, service := newModuleDisableMutationFixtureV1(t)
	service.mutate = func(
		_ context.Context,
		input MutateModuleDisableInputV1,
	) (MutateModuleDisableResultV1, error) {
		if input.ConfirmationProof != nil {
			return MutateModuleDisableResultV1{}, errors.New("unexpected proof")
		}
		return MutateModuleDisableResultV1{}, ErrModuleDisableConfirmationRejectedV1
	}
	request := fixture.moduleDisableMutationRequestV1(
		t,
		ModuleDisableMutatePathV1,
		true,
		false,
	)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	assertErrorCodeV1(
		t,
		recorder,
		http.StatusForbidden,
		controlapicontract.ErrorForbiddenV1,
	)
	_, mutateCalls, _, lastMutate := service.snapshot()
	if mutateCalls != 1 || lastMutate.ConfirmationProof != nil {
		t.Fatalf("missing proof was rejected before lookup: calls=%d proof=%x", mutateCalls, lastMutate.ConfirmationProof)
	}
}

func TestModuleDisableMutateAppliedSuccessV1(t *testing.T) {
	t.Parallel()
	fixture, service := newModuleDisableMutationFixtureV1(t)
	service.mutate = func(
		_ context.Context,
		input MutateModuleDisableInputV1,
	) (MutateModuleDisableResultV1, error) {
		result, err := basicModuleDisableMutationResultV1(input)
		if err != nil {
			return MutateModuleDisableResultV1{}, err
		}
		post := input.ExpectedPointer
		post.Revision++
		post.Digest = strings.Repeat("8", 64)
		result.Receipt.Status = controlapicontract.OperationStatusAppliedV1
		result.Receipt.PostRef = &post
		result.Receipt.DomainReceipt = &controlapicontract.DomainReceiptRefV1{
			Kind:   controlapicontract.DomainReceiptModuleDisableV1,
			ID:     "module-disable-receipt-a",
			Digest: strings.Repeat("7", 64),
		}
		frozen, canonical, digest, freezeErr :=
			controlapicontract.NewControlOperationReceiptV1(result.Receipt)
		clear(canonical)
		if freezeErr != nil {
			return MutateModuleDisableResultV1{}, freezeErr
		}
		result.Receipt = frozen
		result.ReceiptDigest = digest
		return result, nil
	}
	request := fixture.moduleDisableMutationRequestV1(
		t,
		ModuleDisableMutatePathV1,
		true,
		true,
	)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("APPLIED status=%d cache=%q body=%s", recorder.Code, recorder.Header().Get("Cache-Control"), recorder.Body.String())
	}
	var response MutateModuleDisableResultV1
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Receipt.Status != controlapicontract.OperationStatusAppliedV1 ||
		response.Receipt.PostRef == nil || response.Receipt.PostRef.Revision != 8 ||
		response.Receipt.DomainReceipt == nil {
		t.Fatalf("APPLIED response drift: %+v", response)
	}
}

func TestModuleDisableMutationServicesAreAllOrNoneV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{tenantScopeV1()},
		false,
	)
	service := &fakeModuleDisableMutationServicesV1{}
	for _, config := range []ConfigV1{
		{
			Authority:                 testAuthorityV1,
			Registry:                  fixture.registry,
			Modules:                   fixture.service,
			ModuleDisableDryRun:       fixture.disable,
			ModuleDisableConfirmation: service,
			Cursor:                    fixture.codec,
			Entropy: bytes.NewReader(
				bytes.Repeat([]byte{0x5d}, correlationBytesV1),
			),
		},
		{
			Authority:             testAuthorityV1,
			Registry:              fixture.registry,
			Modules:               fixture.service,
			ModuleDisableDryRun:   fixture.disable,
			ModuleDisableMutation: service,
			Cursor:                fixture.codec,
			Entropy: bytes.NewReader(
				bytes.Repeat([]byte{0x5e}, correlationBytesV1),
			),
		},
	} {
		if handler, err := NewHandlerV1(config); handler != nil ||
			!errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("one-sided mutation service handler=%v err=%v", handler, err)
		}
	}
}

func TestModuleDisableConfirmationRejectsServiceResultDriftV1(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*IssueModuleDisableConfirmationResultV1)
	}{
		{"schema", func(result *IssueModuleDisableConfirmationResultV1) { result.SchemaVersion = "v2" }},
		{"proof length", func(result *IssueModuleDisableConfirmationResultV1) { result.ConfirmationProof = []byte{1} }},
		{"expired", func(result *IssueModuleDisableConfirmationResultV1) {
			result.ExpiresAtUnixMicros = uint64(testNowV1.UnixMicro())
		}},
		{"evaluation digest", func(result *IssueModuleDisableConfirmationResultV1) {
			result.EvaluationDigest = strings.Repeat("0", 64)
		}},
		{"statement digest", func(result *IssueModuleDisableConfirmationResultV1) { result.StatementDigest = strings.Repeat("0", 64) }},
		{"request digest", func(result *IssueModuleDisableConfirmationResultV1) { result.RequestDigest = strings.Repeat("0", 64) }},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture, service := newModuleDisableMutationFixtureV1(t)
			service.issue = func(
				_ context.Context,
				input IssueModuleDisableConfirmationInputV1,
			) (IssueModuleDisableConfirmationResultV1, error) {
				result, err := basicModuleDisableConfirmationResultV1(input)
				if err != nil {
					return IssueModuleDisableConfirmationResultV1{}, err
				}
				test.mutate(&result)
				return result, nil
			}
			request := fixture.moduleDisableMutationRequestV1(
				t,
				ModuleDisableConfirmationPathV1,
				false,
				false,
			)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(
				t,
				recorder,
				http.StatusInternalServerError,
				controlapicontract.ErrorIntegrityFailureV1,
			)
		})
	}
}

func TestModuleDisableMutationErrorMappingIsFiniteV1(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   controlapicontract.ErrorCodeV1
	}{
		{
			name:   "confirmation rejected",
			err:    ErrModuleDisableConfirmationRejectedV1,
			status: http.StatusForbidden,
			code:   controlapicontract.ErrorForbiddenV1,
		},
		{
			name:   "idempotency conflict",
			err:    ErrModuleDisableIdempotencyConflictV1,
			status: http.StatusConflict,
			code:   controlapicontract.ErrorIdempotencyConflictV1,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture, service := newModuleDisableMutationFixtureV1(t)
			service.mutate = func(
				context.Context,
				MutateModuleDisableInputV1,
			) (MutateModuleDisableResultV1, error) {
				return MutateModuleDisableResultV1{}, errors.Join(
					errors.New("private mutation detail"),
					test.err,
				)
			}
			request := fixture.moduleDisableMutationRequestV1(
				t,
				ModuleDisableMutatePathV1,
				true,
				true,
			)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(t, recorder, test.status, test.code)
			if strings.Contains(recorder.Body.String(), "private mutation detail") {
				t.Fatal("private mutation detail leaked")
			}
		})
	}
}

func TestModuleDisableMutationResultRejectsUnknownV1(t *testing.T) {
	t.Parallel()
	fixture, service := newModuleDisableMutationFixtureV1(t)
	service.mutate = func(
		_ context.Context,
		input MutateModuleDisableInputV1,
	) (MutateModuleDisableResultV1, error) {
		result, err := basicModuleDisableMutationResultV1(input)
		if err != nil {
			return MutateModuleDisableResultV1{}, err
		}
		result.Receipt.Status = controlapicontract.OperationStatusUnknownV1
		result.Receipt.ErrorCode = controlapicontract.ErrorOutcomeUnknownV1
		result.Receipt.PostRef = nil
		result.Receipt.DomainReceipt = &controlapicontract.DomainReceiptRefV1{
			Kind:   controlapicontract.DomainReceiptOutcomeUnknownV1,
			ID:     "outcome-unknown-a",
			Digest: strings.Repeat("9", 64),
		}
		result.Receipt.ReplayDisposition = controlapicontract.ReplayExactReceiptOnlyNoReplayV1
		_, canonical, digest, freezeErr :=
			controlapicontract.NewControlOperationReceiptV1(result.Receipt)
		clear(canonical)
		if freezeErr != nil {
			return MutateModuleDisableResultV1{}, freezeErr
		}
		result.ReceiptDigest = digest
		return result, nil
	}
	request := fixture.moduleDisableMutationRequestV1(
		t,
		ModuleDisableMutatePathV1,
		true,
		false,
	)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	assertErrorCodeV1(
		t,
		recorder,
		http.StatusInternalServerError,
		controlapicontract.ErrorIntegrityFailureV1,
	)
}

func TestModuleDisableMutationRejectsServiceResultDriftV1(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*MutateModuleDisableResultV1)
	}{
		{"schema", func(result *MutateModuleDisableResultV1) { result.SchemaVersion = "v2" }},
		{"request digest", func(result *MutateModuleDisableResultV1) { result.RequestDigest = strings.Repeat("0", 64) }},
		{"receipt digest", func(result *MutateModuleDisableResultV1) { result.ReceiptDigest = strings.Repeat("0", 64) }},
		{"wrong precondition revision", func(result *MutateModuleDisableResultV1) { result.Receipt.PreRef.Revision++ }},
		{"rejected status", func(result *MutateModuleDisableResultV1) {
			result.Receipt.Status = controlapicontract.OperationStatusRejectedV1
		}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture, service := newModuleDisableMutationFixtureV1(t)
			service.mutate = func(
				_ context.Context,
				input MutateModuleDisableInputV1,
			) (MutateModuleDisableResultV1, error) {
				result, err := basicModuleDisableMutationResultV1(input)
				if err != nil {
					return MutateModuleDisableResultV1{}, err
				}
				test.mutate(&result)
				return result, nil
			}
			request := fixture.moduleDisableMutationRequestV1(
				t,
				ModuleDisableMutatePathV1,
				true,
				false,
			)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(
				t,
				recorder,
				http.StatusInternalServerError,
				controlapicontract.ErrorIntegrityFailureV1,
			)
		})
	}
}
