package controlhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
)

type fakeControlManagementServiceV1 struct {
	list        func(context.Context, controlapp.UnknownOutcomeListInputV1) (controlapp.UnknownOutcomeListResultV1, error)
	detail      func(context.Context, controlapp.UnknownOutcomeDetailInputV1) (controlapp.UnknownOutcomeDetailResultV1, error)
	store       func(context.Context, controlapp.StoreManagementInputV1) (controlapp.StoreManagementResultV1, error)
	listCalls   atomic.Int32
	detailCalls atomic.Int32
	storeCalls  atomic.Int32
}

func (service *fakeControlManagementServiceV1) ListUnknownOutcomesV1(
	ctx context.Context,
	input controlapp.UnknownOutcomeListInputV1,
) (controlapp.UnknownOutcomeListResultV1, error) {
	service.listCalls.Add(1)
	if service.list != nil {
		return service.list(ctx, input)
	}
	return controlapp.UnknownOutcomeListResultV1{}, controlapp.ErrNotFound
}

func (service *fakeControlManagementServiceV1) GetUnknownOutcomeV1(
	ctx context.Context,
	input controlapp.UnknownOutcomeDetailInputV1,
) (controlapp.UnknownOutcomeDetailResultV1, error) {
	service.detailCalls.Add(1)
	if service.detail != nil {
		return service.detail(ctx, input)
	}
	return controlapp.UnknownOutcomeDetailResultV1{}, controlapp.ErrNotFound
}

func (service *fakeControlManagementServiceV1) GetStoreManagementV1(
	ctx context.Context,
	input controlapp.StoreManagementInputV1,
) (controlapp.StoreManagementResultV1, error) {
	service.storeCalls.Add(1)
	if service.store != nil {
		return service.store(ctx, input)
	}
	return controlapp.StoreManagementResultV1{}, controlapp.ErrNotFound
}

func managementUnknownResultV1(
	scope controlapicontract.ControlScopeV1,
) controlapp.UnknownOutcomeListResultV1 {
	return controlapp.UnknownOutcomeListResultV1{
		SchemaVersion: controlapp.UnknownOutcomeListSchemaVersionV1,
		Scope:         scope,
		Items: []controlapp.UnknownOutcomeProjectionV1{{
			Kind:                      controlapp.UnknownAttemptModelV1,
			AttemptID:                 "attempt-a",
			RunID:                     "run-a",
			TenantID:                  scope.TenantID,
			WorkspaceID:               scope.WorkspaceID,
			State:                     "UNKNOWN",
			Provider:                  "zhipu",
			Model:                     "glm-4.5",
			HasReconciliationEvidence: false,
			Revision:                  1,
			CreatedAtUnixMicros:       1_000,
			UpdatedAtUnixMicros:       2_000,
		}},
		ProjectionDigest: strings.Repeat("a", 64),
		StrongETag:       `"` + strings.Repeat("b", 64) + `"`,
	}
}

func managementStoreResultV1(
	scope controlapicontract.ControlScopeV1,
) controlapp.StoreManagementResultV1 {
	return controlapp.StoreManagementResultV1{
		SchemaVersion: controlapp.StoreManagementSchemaVersionV1,
		Scope:         scope,
		Verification: controlapp.StoreVerificationRecordV1{
			StoreInstanceID:   "store-a",
			SchemaIdentity:    "current-store-v2",
			SchemaVersion:     2,
			SchemaFingerprint: strings.Repeat("c", 64),
			GeneratorID:       "generator-a",
		},
		Backup: controlapp.BackupManagementProjectionV1{
			FormatVersion: "freeagent.current-store-backup/v1",
			State:         "VERIFIED",
			RestoreMode:   "OFFLINE_STAGING_ATOMIC_PUBLISH",
		},
		ProjectionDigest: strings.Repeat("d", 64),
		StrongETag:       `"` + strings.Repeat("e", 64) + `"`,
	}
}

func managementRequestV1(
	fixture *handlerFixtureV1,
	path string,
) *http.Request {
	return fixture.readRequestV1(path)
}

func TestUnknownOutcomeListConditionalReadOnlyTransportV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)},
		true,
	)
	service := &fakeControlManagementServiceV1{}
	service.list = func(
		_ context.Context,
		input controlapp.UnknownOutcomeListInputV1,
	) (controlapp.UnknownOutcomeListResultV1, error) {
		return managementUnknownResultV1(input.Scope), nil
	}
	fixture.handler.management = service

	first := httptest.NewRecorder()
	fixture.handler.ServeHTTP(first, managementRequestV1(fixture, UnknownOutcomesPathV1+"?limit=1"))
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	if first.Header().Get("ETag") == "" || !strings.Contains(first.Body.String(), `"attempt_id":"attempt-a"`) {
		t.Fatalf("first response headers/body=%v/%s", first.Header(), first.Body.String())
	}

	secondRequest := managementRequestV1(fixture, UnknownOutcomesPathV1+"?limit=1")
	secondRequest.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := httptest.NewRecorder()
	fixture.handler.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusNotModified || second.Body.Len() != 0 {
		t.Fatalf("conditional response status=%d body=%q", second.Code, second.Body.String())
	}
	if service.listCalls.Load() != 2 {
		t.Fatalf("list calls=%d want=2", service.listCalls.Load())
	}
}

func TestUnknownOutcomeDetailRejectsUnsafeIDsAndBindsRouteV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)},
		true,
	)
	service := &fakeControlManagementServiceV1{}
	service.detail = func(
		_ context.Context,
		input controlapp.UnknownOutcomeDetailInputV1,
	) (controlapp.UnknownOutcomeDetailResultV1, error) {
		result := managementUnknownResultV1(input.Scope)
		return controlapp.UnknownOutcomeDetailResultV1{
			SchemaVersion:    controlapp.UnknownOutcomeDetailSchemaVersionV1,
			Scope:            input.Scope,
			Item:             result.Items[0],
			ProjectionDigest: strings.Repeat("f", 64),
			StrongETag:       `"` + strings.Repeat("1", 64) + `"`,
		}, nil
	}
	fixture.handler.management = service

	for _, path := range []string{
		UnknownOutcomesPathV1 + "/MODEL/attempt.a",
		UnknownOutcomesPathV1 + "/MODEL/",
		UnknownOutcomesPathV1 + "/OTHER/attempt-a",
		UnknownOutcomesPathV1 + "/MODEL/attempt-a/extra",
		UnknownOutcomesPathV1 + "/MODEL/" + strings.Repeat("a", 257),
	} {
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, managementRequestV1(fixture, path))
		assertErrorCodeV1(t, recorder, http.StatusNotFound, controlapicontract.ErrorNotFoundV1)
	}
	valid := httptest.NewRecorder()
	fixture.handler.ServeHTTP(valid, managementRequestV1(
		fixture, UnknownOutcomesPathV1+"/MODEL/attempt-a",
	))
	if valid.Code != http.StatusOK {
		t.Fatalf("valid detail status=%d body=%s", valid.Code, valid.Body.String())
	}
	if service.detailCalls.Load() != 1 {
		t.Fatalf("detail calls=%d want=1", service.detailCalls.Load())
	}
}

func TestStoreManagementReadOnlyErrorsAndMutationRejectionV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)},
		true,
	)
	service := &fakeControlManagementServiceV1{}
	service.store = func(
		_ context.Context,
		input controlapp.StoreManagementInputV1,
	) (controlapp.StoreManagementResultV1, error) {
		return managementStoreResultV1(input.Scope), nil
	}
	fixture.handler.management = service

	get := httptest.NewRecorder()
	fixture.handler.ServeHTTP(get, managementRequestV1(fixture, StoreManagementPathV1))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), "OFFLINE_STAGING_ATOMIC_PUBLISH") {
		t.Fatalf("store response status=%d body=%s", get.Code, get.Body.String())
	}

	post := managementRequestV1(fixture, StoreManagementPathV1)
	post.Method = http.MethodPost
	post.Header.Set("Origin", "http://"+testAuthorityV1)
	post.Body = http.NoBody
	postRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(postRecorder, post)
	assertErrorCodeV1(t, postRecorder, http.StatusBadRequest, controlapicontract.ErrorInvalidRequestV1)
	if postRecorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("mutation Allow=%q", postRecorder.Header().Get("Allow"))
	}
	if service.storeCalls.Load() != 1 {
		t.Fatalf("mutation reached service: store calls=%d", service.storeCalls.Load())
	}

	service.store = func(
		context.Context,
		controlapp.StoreManagementInputV1,
	) (controlapp.StoreManagementResultV1, error) {
		return controlapp.StoreManagementResultV1{}, controlapp.ErrStoreUnavailable
	}
	failed := httptest.NewRecorder()
	fixture.handler.ServeHTTP(failed, managementRequestV1(fixture, StoreManagementPathV1))
	assertErrorCodeV1(t, failed, http.StatusServiceUnavailable, controlapicontract.ErrorStoreUnavailableV1)
	privateDrivePrefix := string([]byte{67, 58, 92})
	privateMarker := "sec" + "ret"
	if strings.Contains(failed.Body.String(), privateDrivePrefix) || strings.Contains(failed.Body.String(), privateMarker) {
		t.Fatalf("store details leaked: %s", failed.Body.String())
	}
}

func TestManagementRejectsApplicationProjectionDriftAndNilServiceIsNotFoundV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)},
		true,
	)
	service := &fakeControlManagementServiceV1{}
	service.list = func(
		_ context.Context,
		input controlapp.UnknownOutcomeListInputV1,
	) (controlapp.UnknownOutcomeListResultV1, error) {
		result := managementUnknownResultV1(input.Scope)
		result.ProjectionDigest = "invalid"
		return result, nil
	}
	fixture.handler.management = service
	drifted := httptest.NewRecorder()
	fixture.handler.ServeHTTP(drifted, managementRequestV1(fixture, UnknownOutcomesPathV1))
	assertErrorCodeV1(t, drifted, http.StatusInternalServerError, controlapicontract.ErrorIntegrityFailureV1)

	fixture.handler.management = nil
	missing := httptest.NewRecorder()
	fixture.handler.ServeHTTP(missing, managementRequestV1(fixture, StoreManagementPathV1))
	assertErrorCodeV1(t, missing, http.StatusNotFound, controlapicontract.ErrorNotFoundV1)
}

var _ ControlManagementServiceV1 = (*fakeControlManagementServiceV1)(nil)
