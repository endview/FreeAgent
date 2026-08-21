package controlhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func moduleDisableBodyV1(
	t *testing.T,
	target controlapp.ModuleBindingTargetV1,
) []byte {
	t.Helper()
	port := moduleapi.PortRef{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}
	if target.Kind == controlapp.ModuleBindingTargetWorkspaceChannelEndpointV1 {
		port.Name = moduleapi.PortNameChannelTransport
	}
	_, canonical, _, err := controlapp.NewModuleDisableDryRunBodyV1(
		controlapp.ModuleDisableDryRunBodyV1{
			SchemaVersion:           controlapp.ModuleDisableDryRunBodySchemaVersionV1,
			ExpectedPointerRevision: 7,
			BindingTarget:           target,
			InstanceID:              "instance-a",
			Port:                    port,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func (fixture *handlerFixtureV1) moduleDisableRequestV1(
	body []byte,
	scope controlapicontract.ControlScopeV1,
) *http.Request {
	request := newRequestV1(
		http.MethodPost,
		ModuleDisableDryRunPathV1,
		bytes.NewReader(body),
	)
	request.Header.Set("Origin", "http://"+testAuthorityV1)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(ScopeKindHeaderV1, string(scope.Kind))
	request.Header.Set(TenantIDHeaderV1, scope.TenantID)
	if scope.Kind == controlapicontract.ScopeWorkspaceV1 {
		request.Header.Set(WorkspaceIDHeaderV1, scope.WorkspaceID)
	}
	basis := basicPublishedBasisV1(scope.TenantID, 7)
	pointer, err := controlapp.PublishedPointerRefV1(basis)
	if err != nil {
		panic(err)
	}
	request.Header.Set("If-Match", `"`+pointer.Digest+`"`)
	request.Header.Set(CSRFHeaderV1, fixture.csrf)
	request.AddCookie(&http.Cookie{Name: SessionCookieV1, Value: fixture.credential})
	return request
}

func TestModuleDisableDryRunExactTransportAndApplicationInputV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{tenantScopeV1()},
		true,
	)
	var admittedSession controlapicontract.ControlSessionV1
	fixture.disable.dryRun = func(
		_ context.Context,
		input controlapp.ModuleDisableDryRunInputV1,
	) (controlapp.ModuleDisableDryRunResultV1, error) {
		admittedSession = input.Authorization.Session()
		return basicModuleDisableDryRunResultV1(input)
	}
	body := moduleDisableBodyV1(t, controlapp.ModuleBindingTargetV1{
		Kind:      controlapp.ModuleBindingTargetProfileV1,
		ProfileID: "profile-a",
	})
	request := fixture.moduleDisableRequestV1(body, tenantScopeV1())
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertSecurityHeadersV1(t, recorder.Header())
	if recorder.Header().Get("ETag") != "" ||
		recorder.Header().Get("Set-Cookie") != "" ||
		recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected response headers: %v", recorder.Header())
	}
	var response controlapp.ModuleDisableDryRunResultV1
	decoder := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil ||
		response.SchemaVersion != controlapp.ModuleDisableDryRunResultSchemaVersionV1 ||
		response.Request.Intent != controlapicontract.OperationIntentDryRunV1 ||
		response.Receipt.Status != controlapicontract.OperationStatusDryRunV1 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	for _, forbidden := range []string{
		fixture.credential,
		fixture.csrf,
		"idempotency_key_digest",
		"confirmation_digest",
	} {
		if forbidden != "" && strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
	calls, input := fixture.disable.snapshot()
	if calls != 1 || input.Authorization == nil ||
		admittedSession.PrincipalID != "operator-owner" ||
		input.Authorization.Session().PrincipalID != "" ||
		input.Scope != tenantScopeV1() ||
		input.ExpectedPointer != (controlapicontract.ExpectedResourceRefV1{
			Kind:       controlapicontract.ResourcePublishedPointerV1,
			ResourceID: testTenantV1,
			Revision:   7,
			Digest: func() string {
				pointer, _ := controlapp.PublishedPointerRefV1(
					basicPublishedBasisV1(testTenantV1, 7),
				)
				return pointer.Digest
			}(),
		}) || input.ObservedAt != uint64(testNowV1.Add(time.Second).UnixMicro()) ||
		input.Body.BindingTarget.ProfileID != "profile-a" {
		t.Fatalf("calls=%d input=%+v", calls, input)
	}
	_, getCalls := fixture.service.counts()
	if getCalls != 0 {
		t.Fatalf("Dry-run route fell through to module detail: %d calls", getCalls)
	}
}

func TestModuleDisableDryRunCanonicalBodyAndBoundsV1(t *testing.T) {
	t.Parallel()
	valid := moduleDisableBodyV1(t, controlapp.ModuleBindingTargetV1{
		Kind:      controlapp.ModuleBindingTargetProfileV1,
		ProfileID: "profile-a",
	})
	tests := []struct {
		name   string
		body   []byte
		status int
		code   controlapicontract.ErrorCodeV1
	}{
		{
			name:   "unknown field",
			body:   []byte(strings.TrimSuffix(string(valid), "}") + `,"unknown":true}`),
			status: http.StatusBadRequest, code: controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "duplicate field",
			body: []byte(strings.Replace(
				string(valid),
				`"instance_id":"instance-a"`,
				`"instance_id":"instance-a","instance_id":"instance-a"`,
				1,
			)),
			status: http.StatusBadRequest, code: controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "noncanonical whitespace", body: append([]byte(" "), valid...),
			status: http.StatusBadRequest, code: controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "invalid semantic revision",
			body: []byte(strings.Replace(
				string(valid),
				`"expected_pointer_revision":7`,
				`"expected_pointer_revision":0`,
				1,
			)),
			status: http.StatusBadRequest, code: controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "oversized", body: bytes.Repeat([]byte("x"), MaximumOperationBodyBytesV1+1),
			status: http.StatusTooManyRequests, code: controlapicontract.ErrorResourceExhaustedV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newHandlerFixtureV1(
				t,
				[]controlapicontract.ControlScopeV1{tenantScopeV1()},
				true,
			)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(
				recorder,
				fixture.moduleDisableRequestV1(test.body, tenantScopeV1()),
			)
			assertErrorCodeV1(t, recorder, test.status, test.code)
			if calls, _ := fixture.disable.snapshot(); calls != 0 {
				t.Fatalf("invalid body reached service: %d calls", calls)
			}
		})
	}
}

func TestModuleDisableDryRunRequiresExactIfMatchAndRejectsMutationHeadersV1(t *testing.T) {
	t.Parallel()
	body := moduleDisableBodyV1(t, controlapp.ModuleBindingTargetV1{
		Kind:      controlapp.ModuleBindingTargetProfileV1,
		ProfileID: "profile-a",
	})
	tests := []struct {
		name   string
		mutate func(*http.Request)
		status int
		code   controlapicontract.ErrorCodeV1
	}{
		{"missing", func(r *http.Request) { r.Header.Del("If-Match") }, 428, controlapicontract.ErrorPreconditionRequiredV1},
		{"wildcard", func(r *http.Request) { r.Header.Set("If-Match", "*") }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"weak", func(r *http.Request) { r.Header.Set("If-Match", `W/"`+strings.Repeat("a", 64)+`"`) }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"unquoted", func(r *http.Request) { r.Header.Set("If-Match", strings.Repeat("a", 64)) }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"uppercase", func(r *http.Request) { r.Header.Set("If-Match", `"`+strings.Repeat("A", 64)+`"`) }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"multiple lines", func(r *http.Request) { r.Header.Add("If-Match", `"`+strings.Repeat("b", 64)+`"`) }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"comma list", func(r *http.Request) {
			r.Header.Set("If-Match", `"`+strings.Repeat("a", 64)+`","`+strings.Repeat("b", 64)+`"`)
		}, 400, controlapicontract.ErrorInvalidRequestV1},
		{"idempotency", func(r *http.Request) { r.Header.Set("Idempotency-Key", "") }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"confirmation", func(r *http.Request) { r.Header.Set(ConfirmationHeaderV1, "confirmed") }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"unknown authority header", func(r *http.Request) { r.Header.Set("X-FreeAgent-Confirmation-Digest", strings.Repeat("b", 64)) }, 400, controlapicontract.ErrorInvalidRequestV1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{tenantScopeV1()}, true)
			request := fixture.moduleDisableRequestV1(body, tenantScopeV1())
			test.mutate(request)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(t, recorder, test.status, test.code)
			if calls, _ := fixture.disable.snapshot(); calls != 0 {
				t.Fatalf("invalid precondition reached service: %d calls", calls)
			}
		})
	}
}

func TestModuleDisableDryRunAuthenticationCSRFOriginAndScopeV1(t *testing.T) {
	t.Parallel()
	body := moduleDisableBodyV1(t, controlapp.ModuleBindingTargetV1{
		Kind:      controlapp.ModuleBindingTargetProfileV1,
		ProfileID: "profile-a",
	})
	tests := []struct {
		name   string
		mutate func(*http.Request)
		status int
		code   controlapicontract.ErrorCodeV1
	}{
		{"missing origin", func(r *http.Request) { r.Header.Del("Origin") }, 403, controlapicontract.ErrorForbiddenV1},
		{"wrong origin", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:9") }, 403, controlapicontract.ErrorForbiddenV1},
		{"wrong host", func(r *http.Request) { r.Host = "localhost:38117" }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"query", func(r *http.Request) { r.URL.RawQuery = "apply=true"; r.RequestURI += "?apply=true" }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"missing content type", func(r *http.Request) { r.Header.Del("Content-Type") }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"nonexact content type", func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=utf-8") }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"forwarded", func(r *http.Request) { r.Header.Set("Forwarded", "host=evil") }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"missing session", func(r *http.Request) { r.Header.Del("Cookie") }, 401, controlapicontract.ErrorUnauthenticatedV1},
		{"duplicate session", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: SessionCookieV1, Value: "invalid"}) }, 401, controlapicontract.ErrorUnauthenticatedV1},
		{"missing csrf", func(r *http.Request) { r.Header.Del(CSRFHeaderV1) }, 401, controlapicontract.ErrorUnauthenticatedV1},
		{"invalid csrf", func(r *http.Request) { r.Header.Set(CSRFHeaderV1, "invalid") }, 401, controlapicontract.ErrorUnauthenticatedV1},
		{"duplicate csrf", func(r *http.Request) { r.Header.Add(CSRFHeaderV1, "invalid") }, 401, controlapicontract.ErrorUnauthenticatedV1},
		{"missing scope", func(r *http.Request) { r.Header.Del(ScopeKindHeaderV1) }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"detail-only instance encoding header", func(r *http.Request) {
			r.Header.Set(ModuleInstanceIDEncodingHeaderV1, ModuleInstanceIDEncodingBase64URLUTF8V1)
		}, 400, controlapicontract.ErrorInvalidRequestV1},
		{"cross tenant", func(r *http.Request) { r.Header.Set(TenantIDHeaderV1, "tenant-b") }, 403, controlapicontract.ErrorForbiddenV1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{tenantScopeV1()}, true)
			request := fixture.moduleDisableRequestV1(body, tenantScopeV1())
			test.mutate(request)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(t, recorder, test.status, test.code)
			if calls, _ := fixture.disable.snapshot(); calls != 0 {
				t.Fatalf("unauthorized request reached service: %d calls", calls)
			}
		})
	}

	t.Run("missing capability", func(t *testing.T) {
		fixture := newHandlerFixtureWithCapabilitiesV1(
			t,
			[]controlapicontract.ControlScopeV1{tenantScopeV1()},
			[]controlapicontract.ControlCapabilityV1{controlapicontract.CapabilityObserveV1},
			true,
		)
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(
			recorder,
			fixture.moduleDisableRequestV1(body, tenantScopeV1()),
		)
		assertErrorCodeV1(t, recorder, http.StatusForbidden, controlapicontract.ErrorForbiddenV1)
		if calls, _ := fixture.disable.snapshot(); calls != 0 {
			t.Fatalf("missing capability reached service: %d calls", calls)
		}
	})
}

func TestModuleDisableDryRunWorkspaceScopeAndApplicationErrorsV1(t *testing.T) {
	t.Parallel()
	scope := workspaceScopeV1(testWorkspaceV1)
	body := moduleDisableBodyV1(t, controlapp.ModuleBindingTargetV1{
		Kind:        controlapp.ModuleBindingTargetWorkspaceChannelEndpointV1,
		WorkspaceID: testWorkspaceV1,
		EndpointID:  "endpoint-a",
	})
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{scope}, true)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, fixture.moduleDisableRequestV1(body, scope))
	if recorder.Code != http.StatusOK {
		t.Fatalf("workspace status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	_, input := fixture.disable.snapshot()
	if input.Scope != scope || input.Body.BindingTarget.WorkspaceID != testWorkspaceV1 ||
		input.Body.Port.Name != moduleapi.PortNameChannelTransport {
		t.Fatalf("workspace input=%+v", input)
	}

	for _, test := range []struct {
		err    error
		status int
		code   controlapicontract.ErrorCodeV1
	}{
		{controlapp.ErrPreconditionRequired, 428, controlapicontract.ErrorPreconditionRequiredV1},
		{controlapp.ErrRevisionConflict, 409, controlapicontract.ErrorRevisionConflictV1},
		{controlapp.ErrConflict, 409, controlapicontract.ErrorConflictV1},
	} {
		fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{scope}, true)
		fixture.disable.dryRun = func(
			context.Context,
			controlapp.ModuleDisableDryRunInputV1,
		) (controlapp.ModuleDisableDryRunResultV1, error) {
			return controlapp.ModuleDisableDryRunResultV1{}, errors.Join(
				errors.New("private store detail"),
				test.err,
			)
		}
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, fixture.moduleDisableRequestV1(body, scope))
		assertErrorCodeV1(t, recorder, test.status, test.code)
		if strings.Contains(recorder.Body.String(), "private store detail") {
			t.Fatal("application detail leaked")
		}
	}
}

func TestModuleDisableDryRunHasNoFallbackRouteV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{tenantScopeV1()}, true)
	body := moduleDisableBodyV1(t, controlapp.ModuleBindingTargetV1{
		Kind:      controlapp.ModuleBindingTargetProfileV1,
		ProfileID: "profile-a",
	})
	for _, path := range []string{
		"/control/api/v1/modules/disable",
		"/control/api/v1/modules/disable/apply",
	} {
		request := fixture.moduleDisableRequestV1(body, tenantScopeV1())
		request.URL.Path = path
		request.RequestURI = path
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		assertErrorCodeV1(t, recorder, http.StatusBadRequest, controlapicontract.ErrorInvalidRequestV1)
	}
	for _, path := range []string{
		ModuleDisableConfirmationPathV1,
		ModuleDisableMutatePathV1,
	} {
		request := fixture.moduleDisableRequestV1(body, tenantScopeV1())
		request.URL.Path = path
		request.RequestURI = path
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		assertErrorCodeV1(t, recorder, http.StatusNotFound, controlapicontract.ErrorNotFoundV1)
	}
	get := fixture.moduleDisableRequestV1(body, tenantScopeV1())
	get.Method = http.MethodGet
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, get)
	if recorder.Code != http.StatusBadRequest || recorder.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("method status=%d Allow=%q", recorder.Code, recorder.Header().Get("Allow"))
	}
	if calls, _ := fixture.disable.snapshot(); calls != 0 {
		t.Fatalf("non-Dry-run route reached service: %d calls", calls)
	}
	listCalls, getCalls := fixture.service.counts()
	if listCalls != 0 || getCalls != 0 {
		t.Fatalf("non-Dry-run route reached read service: list=%d get=%d", listCalls, getCalls)
	}
}

func TestModuleDisableDryRunRejectsApplicationResultDriftV1(t *testing.T) {
	t.Parallel()
	body := moduleDisableBodyV1(t, controlapp.ModuleBindingTargetV1{
		Kind:      controlapp.ModuleBindingTargetProfileV1,
		ProfileID: "profile-a",
	})
	tests := []struct {
		name   string
		mutate func(*controlapp.ModuleDisableDryRunResultV1) error
	}{
		{
			name: "rejected receipt cannot be returned as successful Dry-run",
			mutate: func(result *controlapp.ModuleDisableDryRunResultV1) error {
				result.Receipt.Status = controlapicontract.OperationStatusRejectedV1
				result.Receipt.ErrorCode = controlapicontract.ErrorConflictV1
				frozen, canonical, digest, err :=
					controlapicontract.NewControlOperationReceiptV1(result.Receipt)
				clear(canonical)
				result.Receipt = frozen
				result.ReceiptDigest = digest
				return err
			},
		},
		{
			name: "projection does not bind If-Match",
			mutate: func(result *controlapp.ModuleDisableDryRunResultV1) error {
				basis := result.Projection.PreconditionBasis
				basis.Control.Digest = strings.Repeat("8", 64)
				result.Projection.PreconditionBasis = basis
				result.Projection.ObservedBasis = basis
				result.Projection.CandidateBasis = basis
				return nil
			},
		},
		{
			name: "projection names another instance",
			mutate: func(result *controlapp.ModuleDisableDryRunResultV1) error {
				result.Projection.InstanceID = "instance-other"
				return nil
			},
		},
		{
			name: "already applied basis changes only its pointer",
			mutate: func(result *controlapp.ModuleDisableDryRunResultV1) error {
				result.Projection.Disposition = controlapp.ModuleDisableAlreadyAppliedV1
				next := result.Projection.PreconditionBasis
				next.PointerRevision++
				result.Projection.ObservedBasis = next
				result.Projection.CandidateBasis = next
				return nil
			},
		},
		{
			name: "would apply omits exact removal",
			mutate: func(result *controlapp.ModuleDisableDryRunResultV1) error {
				result.Projection.Disposition = controlapp.ModuleDisableWouldApplyV1
				return nil
			},
		},
		{
			name: "would apply claims no Catalog change",
			mutate: func(result *controlapp.ModuleDisableDryRunResultV1) error {
				result.Projection.Disposition = controlapp.ModuleDisableWouldApplyV1
				result.Projection.CandidateBasis = nextPublishedBasisV1(
					result.Projection.ObservedBasis,
				)
				result.Projection.BindingRemoval = &controlapp.ModuleDisableBindingRemovalV1{
					Target: controlapp.ModuleBindingTargetV1{
						Kind:      controlapp.ModuleBindingTargetProfileV1,
						ProfileID: "profile-a",
					},
					Port: moduleapi.PortRef{
						Name:         moduleapi.PortNameActionProvider,
						ExactVersion: moduleapi.PortVersionV1,
					},
					ConfigRef:           strings.Repeat("6", 64),
					AuthorityCeilingRef: strings.Repeat("7", 64),
					StaticContextRefs:   []string{},
					FailurePolicy:       moduleapi.FailureRequired,
				}
				result.Projection.CatalogChange = controlapp.ModuleDisableCatalogNoneV1
				return nil
			},
		},
		{
			name: "Workspace removal uses a nonzero index",
			mutate: func(result *controlapp.ModuleDisableDryRunResultV1) error {
				result.Projection.Disposition = controlapp.ModuleDisableWouldApplyV1
				result.Projection.CandidateBasis = nextPublishedBasisV1(
					result.Projection.ObservedBasis,
				)
				result.Projection.CatalogChange = controlapp.ModuleDisableCatalogRemoveInstanceV1
				result.Projection.BindingRemoval = &controlapp.ModuleDisableBindingRemovalV1{
					Target: controlapp.ModuleBindingTargetV1{
						Kind:        controlapp.ModuleBindingTargetWorkspaceChannelEndpointV1,
						WorkspaceID: "workspace-a",
						EndpointID:  "endpoint-a",
					},
					Port: moduleapi.PortRef{
						Name:         moduleapi.PortNameChannelTransport,
						ExactVersion: moduleapi.PortVersionV1,
					},
					PortBindingIndex:    1,
					ConfigRef:           strings.Repeat("6", 64),
					AuthorityCeilingRef: strings.Repeat("7", 64),
					StaticContextRefs:   []string{},
					FailurePolicy:       moduleapi.FailureRequired,
				}
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{tenantScopeV1()}, true)
			fixture.disable.dryRun = func(
				ctx context.Context,
				input controlapp.ModuleDisableDryRunInputV1,
			) (controlapp.ModuleDisableDryRunResultV1, error) {
				result, err := basicModuleDisableDryRunResultV1(input)
				if err != nil {
					return controlapp.ModuleDisableDryRunResultV1{}, err
				}
				if err := test.mutate(&result); err != nil {
					return controlapp.ModuleDisableDryRunResultV1{}, err
				}
				return result, nil
			}
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(
				recorder,
				fixture.moduleDisableRequestV1(body, tenantScopeV1()),
			)
			assertErrorCodeV1(
				t,
				recorder,
				http.StatusInternalServerError,
				controlapicontract.ErrorIntegrityFailureV1,
			)
		})
	}
}

func TestModuleDisableDryRunAcceptsFrozenDispositionRelationshipsV1(t *testing.T) {
	t.Parallel()
	body := moduleDisableBodyV1(t, controlapp.ModuleBindingTargetV1{
		Kind:      controlapp.ModuleBindingTargetProfileV1,
		ProfileID: "profile-a",
	})
	for _, disposition := range []controlapp.ModuleDisableDispositionV1{
		controlapp.ModuleDisableAlreadyAppliedV1,
		controlapp.ModuleDisableWouldApplyV1,
	} {
		t.Run(string(disposition), func(t *testing.T) {
			t.Parallel()
			fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{tenantScopeV1()}, true)
			fixture.disable.dryRun = func(
				ctx context.Context,
				input controlapp.ModuleDisableDryRunInputV1,
			) (controlapp.ModuleDisableDryRunResultV1, error) {
				result, err := basicModuleDisableDryRunResultV1(input)
				if err != nil {
					return controlapp.ModuleDisableDryRunResultV1{}, err
				}
				result.Projection.Disposition = disposition
				next := nextPublishedBasisV1(result.Projection.PreconditionBasis)
				result.Projection.CandidateBasis = next
				if disposition == controlapp.ModuleDisableAlreadyAppliedV1 {
					result.Projection.ObservedBasis = next
					return result, nil
				}
				result.Projection.CatalogChange = controlapp.ModuleDisableCatalogRetainInstanceV1
				result.Projection.BindingRemoval = &controlapp.ModuleDisableBindingRemovalV1{
					Target: controlapp.ModuleBindingTargetV1{
						Kind:      controlapp.ModuleBindingTargetProfileV1,
						ProfileID: "profile-a",
					},
					Port: moduleapi.PortRef{
						Name:         moduleapi.PortNameActionProvider,
						ExactVersion: moduleapi.PortVersionV1,
					},
					ConfigRef:           strings.Repeat("6", 64),
					AuthorityCeilingRef: strings.Repeat("7", 64),
					StaticContextRefs:   []string{},
					FailurePolicy:       moduleapi.FailureRequired,
				}
				return result, nil
			}
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(
				recorder,
				fixture.moduleDisableRequestV1(body, tenantScopeV1()),
			)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
