package controlhttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	testAuthorityV1 = "127.0.0.1:38117"
	testTenantV1    = "tenant-a"
	testWorkspaceV1 = "workspace-a"
)

var testNowV1 = time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

type sequenceReaderV1 struct {
	next atomic.Uint64
}

func (reader *sequenceReaderV1) Read(output []byte) (int, error) {
	for index := range output {
		value := reader.next.Add(1)
		output[index] = byte(value ^ value>>8 ^ value>>16)
	}
	return len(output), nil
}

type fakeModulesServiceV1 struct {
	mutex     sync.Mutex
	listCalls int
	getCalls  int
	lastList  controlapp.ListModulesInputV1
	lastGet   controlapp.GetModuleInputV1
	list      func(context.Context, controlapp.ListModulesInputV1) (controlapp.ModulesPageV1, error)
	get       func(context.Context, controlapp.GetModuleInputV1) (controlapp.ModuleDetailResultV1, error)
}

type fakeModuleDisableDryRunServiceV1 struct {
	mutex  sync.Mutex
	calls  int
	last   controlapp.ModuleDisableDryRunInputV1
	dryRun func(context.Context, controlapp.ModuleDisableDryRunInputV1) (controlapp.ModuleDisableDryRunResultV1, error)
}

type fakeModuleUpgradeReviewServiceV1 struct {
	list   func(context.Context, controlapp.ModuleUpgradeReviewListInputV1) (controlapp.ModuleUpgradeReviewListResultV1, error)
	detail func(context.Context, controlapp.GetModuleUpgradeReviewInputV1) (controlapp.ModuleUpgradeReviewDetailResultV1, error)
}

func (service *fakeModuleUpgradeReviewServiceV1) ListModuleUpgradeReviewsV1(
	ctx context.Context,
	input controlapp.ModuleUpgradeReviewListInputV1,
) (controlapp.ModuleUpgradeReviewListResultV1, error) {
	if service.list != nil {
		return service.list(ctx, input)
	}
	return controlapp.ModuleUpgradeReviewListResultV1{}, controlapp.ErrNotFound
}

func (service *fakeModuleUpgradeReviewServiceV1) GetModuleUpgradeReviewV1(
	ctx context.Context,
	input controlapp.GetModuleUpgradeReviewInputV1,
) (controlapp.ModuleUpgradeReviewDetailResultV1, error) {
	if service.detail != nil {
		return service.detail(ctx, input)
	}
	return controlapp.ModuleUpgradeReviewDetailResultV1{}, controlapp.ErrNotFound
}

func (service *fakeModuleDisableDryRunServiceV1) DryRunModuleDisableV1(
	ctx context.Context,
	input controlapp.ModuleDisableDryRunInputV1,
) (controlapp.ModuleDisableDryRunResultV1, error) {
	service.mutex.Lock()
	service.calls++
	service.last = input
	function := service.dryRun
	service.mutex.Unlock()
	if function != nil {
		return function(ctx, input)
	}
	return basicModuleDisableDryRunResultV1(input)
}

func (service *fakeModuleDisableDryRunServiceV1) snapshot() (
	int,
	controlapp.ModuleDisableDryRunInputV1,
) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	return service.calls, service.last
}

func (service *fakeModulesServiceV1) ListModulesV1(
	ctx context.Context,
	input controlapp.ListModulesInputV1,
) (controlapp.ModulesPageV1, error) {
	service.mutex.Lock()
	service.listCalls++
	service.lastList = input
	function := service.list
	service.mutex.Unlock()
	if function != nil {
		return function(ctx, input)
	}
	return basicPageV1(), nil
}

func (service *fakeModulesServiceV1) GetModuleV1(
	ctx context.Context,
	input controlapp.GetModuleInputV1,
) (controlapp.ModuleDetailResultV1, error) {
	service.mutex.Lock()
	service.getCalls++
	service.lastGet = input
	function := service.get
	service.mutex.Unlock()
	if function != nil {
		return function(ctx, input)
	}
	return basicDetailV1(input.InstanceID), nil
}

func (service *fakeModulesServiceV1) counts() (int, int) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	return service.listCalls, service.getCalls
}

type fakeCursorCodecV1 struct {
	encoded    string
	decoded    controlapp.DecodedModulesCursorV1
	decodeErr  error
	encodeErr  error
	staleError error
	decodeHits atomic.Int32
	encodeHits atomic.Int32
}

func (codec *fakeCursorCodecV1) EncodeModulesCursorV1(
	controlapp.DecodedModulesCursorV1,
) (string, error) {
	codec.encodeHits.Add(1)
	if codec.encodeErr != nil {
		return "", codec.encodeErr
	}
	if codec.encoded == "" {
		return "opaque_signed_page_token", nil
	}
	return codec.encoded, nil
}

func (codec *fakeCursorCodecV1) DecodeModulesCursorV1(
	string,
) (controlapp.DecodedModulesCursorV1, error) {
	codec.decodeHits.Add(1)
	return codec.decoded, codec.decodeErr
}

func (codec *fakeCursorCodecV1) IsStaleModulesCursorErrorV1(err error) bool {
	return codec.staleError != nil && errors.Is(err, codec.staleError)
}

type handlerFixtureV1 struct {
	handler    *HandlerV1
	registry   *controlsession.RegistryV1
	bootstrap  controlsession.BootstrapMaterialV1
	service    *fakeModulesServiceV1
	disable    *fakeModuleDisableDryRunServiceV1
	codec      *fakeCursorCodecV1
	credential string
	csrf       string
	resume     string
}

type panicAfterPartialWriteV1 struct {
	recorder *httptest.ResponseRecorder
}

func (writer *panicAfterPartialWriteV1) Header() http.Header {
	return writer.recorder.Header()
}

func (writer *panicAfterPartialWriteV1) WriteHeader(status int) {
	writer.recorder.WriteHeader(status)
}

func (writer *panicAfterPartialWriteV1) Write(payload []byte) (int, error) {
	limit := len(payload)
	if limit > 16 {
		limit = 16
	}
	_, _ = writer.recorder.Write(payload[:limit])
	panic("writer failed after committing bytes")
}

func newHandlerFixtureV1(
	t *testing.T,
	scopes []controlapicontract.ControlScopeV1,
	exchange bool,
) *handlerFixtureV1 {
	return newHandlerFixtureWithCapabilitiesV1(
		t,
		scopes,
		[]controlapicontract.ControlCapabilityV1{
			controlapicontract.CapabilityObserveV1,
			controlapicontract.CapabilityOperateModulesV1,
		},
		exchange,
	)
}

func newHandlerFixtureWithCapabilitiesV1(
	t *testing.T,
	scopes []controlapicontract.ControlScopeV1,
	capabilities []controlapicontract.ControlCapabilityV1,
	exchange bool,
) *handlerFixtureV1 {
	t.Helper()
	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(scopes)
	if err != nil {
		t.Fatalf("scope set: %v", err)
	}
	registry, bootstrap, err := controlsession.NewRegistryV1(
		controlsession.RegistryConfigV1{
			PrincipalID:           "operator-owner",
			AuthorizationRevision: 1,
			Capabilities:          capabilities,
			ScopeSet:              scopeSet,
			Entropy:               &sequenceReaderV1{},
			Now:                   func() time.Time { return testNowV1 },
		},
	)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	service := &fakeModulesServiceV1{}
	disable := &fakeModuleDisableDryRunServiceV1{}
	codec := &fakeCursorCodecV1{}
	handler, err := NewHandlerV1(ConfigV1{
		Authority:           testAuthorityV1,
		Registry:            registry,
		Modules:             service,
		ModuleDisableDryRun: disable,
		Cursor:              codec,
		Now:                 func() time.Time { return testNowV1.Add(time.Second) },
		Entropy:             bytes.NewReader(bytes.Repeat([]byte{0x5a}, correlationBytesV1)),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	fixture := &handlerFixtureV1{
		handler:   handler,
		registry:  registry,
		bootstrap: bootstrap,
		service:   service,
		disable:   disable,
		codec:     codec,
	}
	if exchange {
		issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
		if err != nil {
			t.Fatalf("exchange bootstrap: %v", err)
		}
		fixture.credential = encodeCredentialV1(issued.SessionCredential)
		fixture.csrf = encodeCredentialV1(issued.CSRFToken)
		fixture.resume = encodeCredentialV1(issued.ResumeCredential)
		clear(issued.SessionCredential)
		clear(issued.CSRFToken)
		clear(issued.ResumeCredential)
	}
	t.Cleanup(registry.Close)
	return fixture
}

func workspaceScopeV1(workspace string) controlapicontract.ControlScopeV1 {
	return controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeWorkspaceV1,
		TenantID:      testTenantV1,
		WorkspaceID:   workspace,
	}
}

func tenantScopeV1() controlapicontract.ControlScopeV1 {
	return controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeTenantV1,
		TenantID:      testTenantV1,
	}
}

func (fixture *handlerFixtureV1) readRequestV1(path string) *http.Request {
	request := newRequestV1(http.MethodGet, path, nil)
	request.Header.Set(ScopeKindHeaderV1, string(controlapicontract.ScopeWorkspaceV1))
	request.Header.Set(TenantIDHeaderV1, testTenantV1)
	request.Header.Set(WorkspaceIDHeaderV1, testWorkspaceV1)
	request.Header.Set(CSRFHeaderV1, fixture.csrf)
	request.AddCookie(&http.Cookie{Name: SessionCookieV1, Value: fixture.credential})
	return request
}

func newRequestV1(method, path string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, path, body)
	request.Host = testAuthorityV1
	return request
}

func basicPageV1() controlapp.ModulesPageV1 {
	return controlapp.ModulesPageV1{
		SchemaVersion: controlapp.ModulesPageSchemaVersionV1,
		Items: []controlapp.ModuleSummaryV1{
			{
				InstanceID: "module-one",
				ModuleID:   "example.module",
				Provides:   []moduleapi.PortRef{},
			},
		},
		StrongETag: `"` + strings.Repeat("a", 64) + `"`,
	}
}

func basicDetailV1(instanceID string) controlapp.ModuleDetailResultV1 {
	return controlapp.ModuleDetailResultV1{
		SchemaVersion: controlapp.ModuleDetailSchemaVersionV1,
		Module: controlapp.ModuleDetailV1{
			Summary: controlapp.ModuleSummaryV1{
				InstanceID: instanceID,
				ModuleID:   "example.module",
				Provides:   []moduleapi.PortRef{},
			},
			Bindings: []controlapp.ModuleBindingSummaryV1{},
		},
		StrongETag: `"` + strings.Repeat("b", 64) + `"`,
	}
}

func basicModuleDisableDryRunResultV1(
	input controlapp.ModuleDisableDryRunInputV1,
) (controlapp.ModuleDisableDryRunResultV1, error) {
	_, bodyCanonical, inputDigest, err := controlapp.NewModuleDisableDryRunBodyV1(input.Body)
	clear(bodyCanonical)
	if err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, err
	}
	session := input.Authorization.Session()
	request, requestCanonical, requestDigest, err := controlapicontract.NewControlOperationRequestV1(
		controlapicontract.ControlOperationRequestV1{
			SchemaVersion: controlapicontract.ControlOperationRequestSchemaVersionV1,
			PrincipalID:   session.PrincipalID,
			Capability:    controlapicontract.CapabilityOperateModulesV1,
			Scope:         input.Scope,
			Operation:     controlapicontract.OperationModuleDisableV1,
			Intent:        controlapicontract.OperationIntentDryRunV1,
			InputDigest:   inputDigest,
			ExpectedRef:   input.ExpectedPointer,
		},
	)
	if err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, err
	}
	receipt, _, receiptDigest, err := controlapp.NewModuleDisableDryRunReceiptV1(
		requestCanonical,
		requestDigest,
		input.ObservedAt,
	)
	clear(requestCanonical)
	if err != nil {
		return controlapp.ModuleDisableDryRunResultV1{}, err
	}
	basis := basicPublishedBasisV1(input.Scope.TenantID, input.ExpectedPointer.Revision)
	return controlapp.ModuleDisableDryRunResultV1{
		SchemaVersion: controlapp.ModuleDisableDryRunResultSchemaVersionV1,
		Request:       request,
		RequestDigest: requestDigest,
		Receipt:       receipt,
		ReceiptDigest: receiptDigest,
		Projection: controlapp.ModuleDisableProjectionV1{
			Disposition:       controlapp.ModuleDisableNoChangeV1,
			PlanDigest:        strings.Repeat("3", 64),
			InstanceID:        input.Body.InstanceID,
			PreconditionBasis: basis,
			ObservedBasis:     basis,
			CandidateBasis:    basis,
			CandidateState:    controlapp.ModuleDisableProjectedNotReservedV1,
			CatalogChange:     controlapp.ModuleDisableCatalogNoneV1,
		},
	}, nil
}

func basicPublishedBasisV1(
	tenantID string,
	pointerRevision uint64,
) controlapicontract.PublishedBasisRefV1 {
	return controlapicontract.PublishedBasisRefV1{
		TenantID:        tenantID,
		PointerRevision: pointerRevision,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID: "control-a", Revision: 1, Digest: strings.Repeat("1", 64),
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID: "catalog-a", Revision: 1, Digest: strings.Repeat("2", 64),
		},
	}
}

func nextPublishedBasisV1(
	basis controlapicontract.PublishedBasisRefV1,
) controlapicontract.PublishedBasisRefV1 {
	basis.PointerRevision++
	basis.Control.ID = "control-b"
	basis.Control.Revision++
	basis.Control.Digest = strings.Repeat("4", 64)
	basis.Catalog.ID = "catalog-b"
	basis.Catalog.Revision++
	basis.Catalog.Digest = strings.Repeat("5", 64)
	return basis
}

func TestNewHandlerAndServerPolicyV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, false)
	for _, authority := range []string{
		"", "localhost:38117", "0.0.0.0:38117", "[::1]:38117",
		"127.0.0.1:0", "127.0.0.1:038117", "127.0.0.1:65536",
	} {
		_, err := NewHandlerV1(ConfigV1{
			Authority:           authority,
			Registry:            fixture.registry,
			Modules:             fixture.service,
			ModuleDisableDryRun: fixture.disable,
			Cursor:              fixture.codec,
			Entropy:             bytes.NewReader(make([]byte, correlationBytesV1)),
		})
		if !errors.Is(err, ErrInvalidConfiguration) {
			t.Errorf("authority %q error = %v", authority, err)
		}
	}
	policy := RecommendedServerPolicyV1()
	server := &http.Server{}
	if err := policy.ApplyTo(server); err != nil {
		t.Fatal(err)
	}
	if server.ReadHeaderTimeout != 5*time.Second ||
		server.ReadTimeout != 30*time.Second ||
		server.WriteTimeout != 30*time.Second ||
		server.IdleTimeout != 60*time.Second ||
		server.MaxHeaderBytes != 16<<10 || server.Handler != nil || server.Addr != "" {
		t.Fatalf("unexpected server policy: %+v", server)
	}
	bad := policy
	bad.ReadHeaderTimeout++
	if err := bad.ApplyTo(&http.Server{}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("modified server policy error = %v", err)
	}
	config := ConfigV1{
		Authority:           testAuthorityV1,
		Registry:            fixture.registry,
		Modules:             fixture.service,
		ModuleDisableDryRun: fixture.disable,
		Cursor:              fixture.codec,
		Entropy:             bytes.NewReader(make([]byte, correlationBytesV1)),
	}
	config.ModuleDisableDryRun = nil
	if _, err := NewHandlerV1(config); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("nil Disable Dry-run service error=%v", err)
	}
	var typedNilDisable *fakeModuleDisableDryRunServiceV1
	config.ModuleDisableDryRun = typedNilDisable
	if _, err := NewHandlerV1(config); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("typed-nil Disable Dry-run service error=%v", err)
	}
}

func TestBootstrapExchangeCookieAndReplayV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{tenantScopeV1()}, false)
	capability := base64.RawURLEncoding.EncodeToString(fixture.bootstrap.Capability)
	body := []byte(fmt.Sprintf(
		`{"capability":%q,"schema_version":%q}`,
		capability,
		BootstrapRequestSchemaV1,
	))
	request := newRequestV1(
		http.MethodPost,
		BootstrapPathV1,
		bytes.NewReader(body),
	)
	request.Header.Set("Origin", "http://"+testAuthorityV1)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertSecurityHeadersV1(t, recorder.Header())
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != SessionCookieV1 || cookie.Value == "" ||
		cookie.Path != "/control/" || !cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" ||
		cookie.MaxAge != 0 || !cookie.Expires.IsZero() || cookie.Secure {
		t.Fatalf("unsafe session cookie: %+v", cookie)
	}
	var response bootstrapExchangeResponseV2
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != BootstrapResponseSchemaV2 ||
		response.CSRFToken == "" || response.ResumeCredential == "" ||
		response.AuthorizedScopes == nil || len(response.AuthorizedScopes) != 1 ||
		response.Session.PrincipalID != "operator-owner" ||
		strings.Contains(recorder.Body.String(), capability) ||
		strings.Contains(recorder.Body.String(), cookie.Value) {
		t.Fatalf("unsafe bootstrap response: %s", recorder.Body.String())
	}
	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(response.AuthorizedScopes)
	if err != nil || scopeSet.Digest() != response.Session.ScopeSetDigest {
		t.Fatalf("bootstrap authorized scopes do not match session digest: digest=%q err=%v", scopeSet.Digest(), err)
	}

	replay := newRequestV1(
		http.MethodPost,
		BootstrapPathV1,
		bytes.NewReader(body),
	)
	replay.Header.Set("Origin", "http://"+testAuthorityV1)
	replay.Header.Set("Content-Type", "application/json")
	replayRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(replayRecorder, replay)
	assertErrorCodeV1(t, replayRecorder, http.StatusUnauthorized, controlapicontract.ErrorUnauthenticatedV1)
	if strings.Contains(replayRecorder.Body.String(), capability) {
		t.Fatal("replay error leaked bootstrap capability")
	}
}

func TestBootstrapReplacesOnlySoleCanonicalStaleSessionCookieV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{tenantScopeV1()},
		false,
	)
	capability := encodeCredentialV1(fixture.bootstrap.Capability)
	body := []byte(fmt.Sprintf(
		`{"capability":%q,"schema_version":%q}`,
		capability,
		BootstrapRequestSchemaV1,
	))
	stale := encodeCredentialV1(bytes.Repeat(
		[]byte{0xa5},
		controlsession.CredentialBytesV1,
	))
	request := newRequestV1(
		http.MethodPost,
		BootstrapPathV1,
		bytes.NewReader(body),
	)
	request.Header.Set("Origin", "http://"+testAuthorityV1)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", SessionCookieV1+"="+stale)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bootstrap with stale cookie status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookieV1 ||
		cookies[0].Value == stale || !validStrongCredentialCookieTestV1(cookies[0].Value) {
		t.Fatalf("bootstrap did not safely replace stale session cookie: %v", cookies)
	}
}

func TestBootstrapCookieEnvelopeRejectsAmbientBeforeCapabilityExchangeV1(t *testing.T) {
	t.Parallel()
	canonicalStale := encodeCredentialV1(bytes.Repeat(
		[]byte{0xb6},
		controlsession.CredentialBytesV1,
	))
	tests := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{
			name: "ambient",
			mutate: func(request *http.Request) {
				request.Header.Set("Cookie", "ambient=x")
			},
		},
		{
			name: "malformed FreeAgent",
			mutate: func(request *http.Request) {
				request.Header.Set("Cookie", SessionCookieV1+"=not-base64")
			},
		},
		{
			name: "FreeAgent plus ambient",
			mutate: func(request *http.Request) {
				request.Header.Set(
					"Cookie",
					SessionCookieV1+"="+canonicalStale+"; ambient=x",
				)
			},
		},
		{
			name: "duplicate FreeAgent",
			mutate: func(request *http.Request) {
				request.Header.Set(
					"Cookie",
					SessionCookieV1+"="+canonicalStale+"; "+
						SessionCookieV1+"="+canonicalStale,
				)
			},
		},
		{
			name: "multiple Cookie fields",
			mutate: func(request *http.Request) {
				request.Header.Add("Cookie", SessionCookieV1+"="+canonicalStale)
				request.Header.Add("Cookie", SessionCookieV1+"="+canonicalStale)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newHandlerFixtureV1(
				t,
				[]controlapicontract.ControlScopeV1{tenantScopeV1()},
				false,
			)
			capability := encodeCredentialV1(fixture.bootstrap.Capability)
			body := []byte(fmt.Sprintf(
				`{"capability":%q,"schema_version":%q}`,
				capability,
				BootstrapRequestSchemaV1,
			))
			request := newRequestV1(
				http.MethodPost,
				BootstrapPathV1,
				bytes.NewReader(body),
			)
			request.Header.Set("Origin", "http://"+testAuthorityV1)
			request.Header.Set("Content-Type", "application/json")
			test.mutate(request)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(
				t,
				recorder,
				http.StatusBadRequest,
				controlapicontract.ErrorInvalidRequestV1,
			)
			if recorder.Header().Get("Set-Cookie") != "" {
				t.Fatal("rejected bootstrap cookie envelope changed cookie state")
			}

			clean := newRequestV1(
				http.MethodPost,
				BootstrapPathV1,
				bytes.NewReader(body),
			)
			clean.Header.Set("Origin", "http://"+testAuthorityV1)
			clean.Header.Set("Content-Type", "application/json")
			cleanRecorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(cleanRecorder, clean)
			if cleanRecorder.Code != http.StatusOK {
				t.Fatalf(
					"cookie-envelope rejection consumed capability: status=%d body=%s",
					cleanRecorder.Code,
					cleanRecorder.Body.String(),
				)
			}
		})
	}
}

func TestBootstrapMissingCapabilityCannotReplaceStaleCookieV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{tenantScopeV1()},
		false,
	)
	stale := encodeCredentialV1(bytes.Repeat(
		[]byte{0xc7},
		controlsession.CredentialBytesV1,
	))
	body := []byte(fmt.Sprintf(
		`{"capability":"","schema_version":%q}`,
		BootstrapRequestSchemaV1,
	))
	request := newRequestV1(
		http.MethodPost,
		BootstrapPathV1,
		bytes.NewReader(body),
	)
	request.Header.Set("Origin", "http://"+testAuthorityV1)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", SessionCookieV1+"="+stale)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	assertErrorCodeV1(
		t,
		recorder,
		http.StatusUnauthorized,
		controlapicontract.ErrorUnauthenticatedV1,
	)
	if recorder.Header().Get("Set-Cookie") != "" {
		t.Fatal("missing bootstrap capability replaced stale cookie")
	}

	capability := encodeCredentialV1(fixture.bootstrap.Capability)
	validBody := []byte(fmt.Sprintf(
		`{"capability":%q,"schema_version":%q}`,
		capability,
		BootstrapRequestSchemaV1,
	))
	valid := newRequestV1(
		http.MethodPost,
		BootstrapPathV1,
		bytes.NewReader(validBody),
	)
	valid.Header.Set("Origin", "http://"+testAuthorityV1)
	valid.Header.Set("Content-Type", "application/json")
	valid.Header.Set("Cookie", SessionCookieV1+"="+stale)
	validRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(validRecorder, valid)
	if validRecorder.Code != http.StatusOK {
		t.Fatalf(
			"missing capability consumed bootstrap authority: status=%d body=%s",
			validRecorder.Code,
			validRecorder.Body.String(),
		)
	}
}

func validStrongCredentialCookieTestV1(value string) bool {
	decoded, ok := decodeCredentialV1(value)
	clear(decoded)
	return ok
}

func TestBootstrapRejectsAmbientAuthorityAndInvalidBodiesV1(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*http.Request, []byte)
		status int
		code   controlapicontract.ErrorCodeV1
	}{
		{"wrong host", func(r *http.Request, _ []byte) { r.Host = "localhost:38117" }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"missing origin", func(r *http.Request, _ []byte) { r.Header.Del("Origin") }, 403, controlapicontract.ErrorForbiddenV1},
		{"wrong origin", func(r *http.Request, _ []byte) { r.Header.Set("Origin", "http://127.0.0.1:9") }, 403, controlapicontract.ErrorForbiddenV1},
		{"combined origins", func(r *http.Request, _ []byte) { r.Header.Set("Origin", "http://"+testAuthorityV1+", http://evil") }, 403, controlapicontract.ErrorForbiddenV1},
		{"forwarded", func(r *http.Request, _ []byte) { r.Header.Set("Forwarded", "host=evil") }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"method override", func(r *http.Request, _ []byte) { r.Header.Set("X-HTTP-Method-Override", "GET") }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"module instance encoding", func(r *http.Request, _ []byte) {
			r.Header.Set(ModuleInstanceIDEncodingHeaderV1, ModuleInstanceIDEncodingBase64URLUTF8V1)
		}, 400, controlapicontract.ErrorInvalidRequestV1},
		{"scope encoding", func(r *http.Request, _ []byte) {
			r.Header.Set(ScopeIDEncodingHeaderV1, ScopeIDEncodingBase64URLUTF8V1)
		}, 400, controlapicontract.ErrorInvalidRequestV1},
		{"authorization", func(r *http.Request, _ []byte) {
			r.Header.Set("Authorization", "Bearer ambient")
		}, 400, controlapicontract.ErrorInvalidRequestV1},
		{"unknown FreeAgent authority", func(r *http.Request, _ []byte) {
			r.Header.Set("X-FreeAgent-Ambient", "x")
		}, 400, controlapicontract.ErrorInvalidRequestV1},
		{"query", func(r *http.Request, _ []byte) { r.URL.RawQuery = "capability=x"; r.RequestURI += "?capability=x" }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"cookie", func(r *http.Request, _ []byte) { r.AddCookie(&http.Cookie{Name: "ambient", Value: "x"}) }, 400, controlapicontract.ErrorInvalidRequestV1},
		{"wrong content type", func(r *http.Request, _ []byte) { r.Header.Set("Content-Type", "application/json; charset=utf-8") }, 400, controlapicontract.ErrorInvalidRequestV1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{tenantScopeV1()}, false)
			capability := encodeCredentialV1(fixture.bootstrap.Capability)
			body := []byte(fmt.Sprintf(`{"capability":%q,"schema_version":%q}`, capability, BootstrapRequestSchemaV1))
			request := newRequestV1(http.MethodPost, BootstrapPathV1, bytes.NewReader(body))
			request.Header.Set("Origin", "http://"+testAuthorityV1)
			request.Header.Set("Content-Type", "application/json")
			test.mutate(request, body)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(t, recorder, test.status, test.code)

			clean := newRequestV1(http.MethodPost, BootstrapPathV1, bytes.NewReader(body))
			clean.Header.Set("Origin", "http://"+testAuthorityV1)
			clean.Header.Set("Content-Type", "application/json")
			cleanRecorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(cleanRecorder, clean)
			if cleanRecorder.Code != http.StatusOK {
				t.Fatalf("rejected bootstrap envelope consumed authority: status=%d body=%s", cleanRecorder.Code, cleanRecorder.Body.String())
			}
		})
	}

	for name, body := range map[string][]byte{
		"unknown":   []byte(`{"schema_version":"control-bootstrap-exchange/v1","capability":"x","extra":1}`),
		"duplicate": []byte(`{"schema_version":"control-bootstrap-exchange/v1","capability":"x","capability":"y"}`),
		"trailing":  []byte(`{"schema_version":"control-bootstrap-exchange/v1","capability":"x"}{}`),
		"oversized": bytes.Repeat([]byte(" "), MaximumBootstrapBodyBytesV1+1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{tenantScopeV1()}, false)
			request := newRequestV1(http.MethodPost, BootstrapPathV1, bytes.NewReader(body))
			request.Header.Set("Origin", "http://"+testAuthorityV1)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			want := controlapicontract.ErrorInvalidRequestV1
			status := http.StatusBadRequest
			if name == "oversized" {
				want = controlapicontract.ErrorResourceExhaustedV1
				status = http.StatusTooManyRequests
			}
			assertErrorCodeV1(t, recorder, status, want)
		})
	}

	t.Run("noncanonical valid body", func(t *testing.T) {
		t.Parallel()
		fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{tenantScopeV1()}, false)
		capability := encodeCredentialV1(fixture.bootstrap.Capability)
		body := []byte(fmt.Sprintf(
			`{ "capability":%q,"schema_version":%q}`,
			capability,
			BootstrapRequestSchemaV1,
		))
		request := newRequestV1(http.MethodPost, BootstrapPathV1, bytes.NewReader(body))
		request.Header.Set("Origin", "http://"+testAuthorityV1)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		assertErrorCodeV1(t, recorder, http.StatusBadRequest, controlapicontract.ErrorInvalidRequestV1)
	})
}

func TestAuthenticatedModulesListDetailCursorAndETagV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
	page := basicPageV1()
	page.HasMore = true
	page.NextCursor = &controlapp.DecodedModulesCursorV1{LastInstanceID: "module-one"}
	var admittedSession controlapicontract.ControlSessionV1
	fixture.service.list = func(
		_ context.Context,
		input controlapp.ListModulesInputV1,
	) (controlapp.ModulesPageV1, error) {
		admittedSession = input.Authorization.Session()
		return page, nil
	}
	fixture.codec.encoded = "opaque_signed_cursor"

	request := fixture.readRequestV1(ModulesPathV1 + "?limit=1")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertSecurityHeadersV1(t, recorder.Header())
	etag := recorder.Header().Get("ETag")
	if !validStrongETagV1(etag) || etag == page.StrongETag {
		t.Fatalf("HTTP ETag does not bind transport response: %q", etag)
	}
	if !strings.Contains(recorder.Body.String(), `"next_cursor":"opaque_signed_cursor"`) ||
		strings.Contains(recorder.Body.String(), "last_instance_id") {
		t.Fatalf("decoded cursor leaked in response: %s", recorder.Body.String())
	}
	fixture.service.mutex.Lock()
	input := fixture.service.lastList
	fixture.service.mutex.Unlock()
	if input.Authorization == nil || admittedSession.PrincipalID != "operator-owner" ||
		input.Authorization.Session().PrincipalID != "" ||
		input.Scope != workspaceScopeV1(testWorkspaceV1) || input.Page.Limit != 1 ||
		input.ObservedAt != uint64(testNowV1.Add(time.Second).UnixMicro()) {
		t.Fatalf("wrong application input: %+v", input)
	}

	notModified := fixture.readRequestV1(ModulesPathV1 + "?limit=1")
	notModified.Header.Set("If-None-Match", etag)
	notModifiedRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(notModifiedRecorder, notModified)
	if notModifiedRecorder.Code != http.StatusNotModified ||
		notModifiedRecorder.Body.Len() != 0 ||
		notModifiedRecorder.Header().Get("ETag") != etag {
		t.Fatalf("304 response=%d %q", notModifiedRecorder.Code, notModifiedRecorder.Body.String())
	}

	cursorRequest := fixture.readRequestV1(ModulesPathV1 + "?limit=1&cursor=opaque_signed_cursor")
	cursorRequest.Header.Set(CSRFHeaderV1, fixture.csrf)
	cursorRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(cursorRecorder, cursorRequest)
	if cursorRecorder.Code != http.StatusOK || fixture.codec.decodeHits.Load() != 1 {
		t.Fatalf("cursor request status=%d body=%s", cursorRecorder.Code, cursorRecorder.Body.String())
	}

	detail := fixture.readRequestV1(ModulesPathV1 + "/module-one")
	detailRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(detailRecorder, detail)
	if detailRecorder.Code != http.StatusOK || !validStrongETagV1(detailRecorder.Header().Get("ETag")) ||
		!strings.Contains(detailRecorder.Body.String(), `"instance_id":"module-one"`) {
		t.Fatalf("detail status=%d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
	encodedDetail := fixture.readRequestV1(
		ModulesPathV1 + "/" + base64.RawURLEncoding.EncodeToString([]byte("module-one")),
	)
	encodedDetail.Header.Set(
		ModuleInstanceIDEncodingHeaderV1,
		ModuleInstanceIDEncodingBase64URLUTF8V1,
	)
	encodedDetail.Header.Set(ScopeIDEncodingHeaderV1, ScopeIDEncodingBase64URLUTF8V1)
	encodedDetail.Header.Set(TenantIDHeaderV1, base64.RawURLEncoding.EncodeToString([]byte(testTenantV1)))
	encodedDetail.Header.Set(WorkspaceIDHeaderV1, base64.RawURLEncoding.EncodeToString([]byte(testWorkspaceV1)))
	encodedRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(encodedRecorder, encodedDetail)
	if encodedRecorder.Code != http.StatusOK ||
		!strings.Contains(encodedRecorder.Body.String(), `"instance_id":"module-one"`) {
		t.Fatalf("encoded detail status=%d body=%s", encodedRecorder.Code, encodedRecorder.Body.String())
	}
	_, getCalls := fixture.service.counts()
	if getCalls != 2 {
		t.Fatalf("detail calls=%d", getCalls)
	}
}

func TestAuthenticatedModuleUpgradeReviewListETagAndScopeV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
	reviewID := strings.Repeat("a", moduleapi.SHA256HexLength)
	service := &fakeModuleUpgradeReviewServiceV1{}
	service.list = func(_ context.Context, input controlapp.ModuleUpgradeReviewListInputV1) (controlapp.ModuleUpgradeReviewListResultV1, error) {
		return controlapp.ModuleUpgradeReviewListResultV1{
			SchemaVersion: controlapp.ModuleUpgradeReviewListSchemaVersionV1,
			Scope:         input.Scope,
			Items: []controlapp.ModuleUpgradeReviewItemV1{{
				ReviewID: reviewID, CandidateID: strings.Repeat("b", 64), ReviewKey: strings.Repeat("c", 64),
				TenantID: testTenantV1, ArtifactAdmissionID: strings.Repeat("d", 64),
				OperatorPrincipalID: "operator-owner", ReviewRequestDigest: strings.Repeat("e", 64),
				BindingTarget: controlapp.ModuleUpgradeReviewBindingTargetV1{Kind: "WORKSPACE_CHANNEL_ENDPOINT", WorkspaceID: testWorkspaceV1, EndpointID: "endpoint-a"},
				Port:          moduleapi.PortRef{Name: "context.provide", ExactVersion: "v1"}, TargetInstanceID: "module-one",
				TargetModule: moduleapi.Ref{ID: "fixture.module", Version: "1.0.0"}, TargetArtifactDigest: strings.Repeat("f", 64),
				Conclusion: "WOULD_APPLY", ReasonCodes: []string{}, CreatedAtUnixMicros: 1_500,
			}},
			HasMore: false, ProjectionDigest: strings.Repeat("1", 64), StrongETag: `"` + strings.Repeat("2", 64) + `"`,
		}, nil
	}
	fixture.handler.moduleUpgradeReviews = service
	request := fixture.readRequestV1(ModuleUpgradeReviewsPathV1 + "?limit=1")
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !validStrongETagV1(recorder.Header().Get("ETag")) || !strings.Contains(recorder.Body.String(), reviewID) {
		t.Fatalf("review list status=%d etag=%q body=%s", recorder.Code, recorder.Header().Get("ETag"), recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"workspace_id":"`+testWorkspaceV1+`"`) || strings.Contains(recorder.Body.String(), "canonical") {
		t.Fatalf("review list exposed unsafe fields: %s", recorder.Body.String())
	}
	conditional := fixture.readRequestV1(ModuleUpgradeReviewsPathV1 + "?limit=1")
	conditional.Header.Set("If-None-Match", recorder.Header().Get("ETag"))
	conditionalRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(conditionalRecorder, conditional)
	if conditionalRecorder.Code != http.StatusNotModified || conditionalRecorder.Body.Len() != 0 {
		t.Fatalf("review conditional status=%d body=%q", conditionalRecorder.Code, conditionalRecorder.Body.String())
	}
}

func TestReadAuthorizationScopeAndCursorFailuresV1(t *testing.T) {
	t.Parallel()
	t.Run("cross workspace is forbidden before service", func(t *testing.T) {
		fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
		request := fixture.readRequestV1(ModulesPathV1)
		request.Header.Set(WorkspaceIDHeaderV1, "workspace-b")
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		assertErrorCodeV1(t, recorder, http.StatusForbidden, controlapicontract.ErrorForbiddenV1)
		listCalls, _ := fixture.service.counts()
		if listCalls != 0 {
			t.Fatalf("unauthorized service calls=%d", listCalls)
		}
	})

	t.Run("missing and duplicate credential or CSRF", func(t *testing.T) {
		fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
		for _, mutate := range []func(*http.Request){
			func(r *http.Request) { r.Header.Del("Cookie") },
			func(r *http.Request) { r.Header.Add("Cookie", SessionCookieV1+"="+fixture.credential) },
			func(r *http.Request) { r.Header.Set("Cookie", SessionCookieV1+"=not-base64") },
			func(r *http.Request) { r.Header.Del(CSRFHeaderV1) },
			func(r *http.Request) { r.Header.Add(CSRFHeaderV1, fixture.csrf) },
			func(r *http.Request) { r.Header.Set(CSRFHeaderV1, "not-base64") },
		} {
			request := fixture.readRequestV1(ModulesPathV1)
			mutate(request)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(t, recorder, http.StatusUnauthorized, controlapicontract.ErrorUnauthenticatedV1)
		}
	})

	t.Run("cursor invalid and stale remain distinct", func(t *testing.T) {
		fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
		invalid := errors.New("signature contained C:" + `\secret\cursor.key`)
		fixture.codec.decodeErr = invalid
		request := fixture.readRequestV1(ModulesPathV1 + "?cursor=opaque")
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		assertErrorCodeV1(t, recorder, http.StatusBadRequest, controlapicontract.ErrorCursorInvalidV1)
		if strings.Contains(recorder.Body.String(), "secret") {
			t.Fatal("cursor error leaked details")
		}

		stale := errors.New("stale")
		fixture.codec.decodeErr = fmt.Errorf("wrapped: %w", stale)
		fixture.codec.staleError = stale
		request = fixture.readRequestV1(ModulesPathV1 + "?cursor=opaque")
		recorder = httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		assertErrorCodeV1(t, recorder, http.StatusConflict, controlapicontract.ErrorCursorStaleV1)
	})
}

func TestMethodsQueriesHeadersAndNoRedirectV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
	for _, target := range []string{
		ModulesPathV1 + "?unknown=1",
		ModulesPathV1 + "?limit=01",
		ModulesPathV1 + "?limit=101",
		ModulesPathV1 + "?limit=1&limit=2",
		ModulesPathV1 + "?cursor=opaque&limit=1",
		ModulesPathV1 + "?cursor=%6fpaque",
		ModulesPathV1 + "?cursor=opaque+token",
		ModulesPathV1 + "?limit=1&&cursor=opaque",
		ModulesPathV1 + "/module-one?cursor=x",
	} {
		request := fixture.readRequestV1(target)
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		assertErrorCodeV1(t, recorder, http.StatusBadRequest, controlapicontract.ErrorInvalidRequestV1)
	}
	listWithDetailEncoding := fixture.readRequestV1(ModulesPathV1)
	listWithDetailEncoding.Header.Set(
		ModuleInstanceIDEncodingHeaderV1,
		ModuleInstanceIDEncodingBase64URLUTF8V1,
	)
	listWithDetailEncodingRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(listWithDetailEncodingRecorder, listWithDetailEncoding)
	assertErrorCodeV1(
		t,
		listWithDetailEncodingRecorder,
		http.StatusBadRequest,
		controlapicontract.ErrorInvalidRequestV1,
	)
	for name, mutate := range map[string]func(*http.Request){
		"unknown encoding": func(request *http.Request) {
			request.Header.Set(ModuleInstanceIDEncodingHeaderV1, "unknown")
		},
		"duplicate encoding": func(request *http.Request) {
			request.Header.Del(ModuleInstanceIDEncodingHeaderV1)
			request.Header.Add(
				ModuleInstanceIDEncodingHeaderV1,
				ModuleInstanceIDEncodingBase64URLUTF8V1,
			)
			request.Header.Add(
				ModuleInstanceIDEncodingHeaderV1,
				ModuleInstanceIDEncodingBase64URLUTF8V1,
			)
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := fixture.readRequestV1(ModulesPathV1 + "/aW5zdGFuY2UtYQ")
			mutate(request)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			if name == "duplicate encoding" {
				assertErrorCodeV1(t, recorder, http.StatusBadRequest, controlapicontract.ErrorInvalidRequestV1)
				return
			}
			assertErrorCodeV1(t, recorder, http.StatusNotFound, controlapicontract.ErrorNotFoundV1)
		})
	}

	post := newRequestV1(http.MethodPost, ModulesPathV1, strings.NewReader("{}"))
	post.Header.Set("Origin", "http://"+testAuthorityV1)
	postRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(postRecorder, post)
	if postRecorder.Code != http.StatusBadRequest || postRecorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method response=%d Allow=%q", postRecorder.Code, postRecorder.Header().Get("Allow"))
	}

	redirect := fixture.readRequestV1(BootstrapPathV1 + "/")
	redirectRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(redirectRecorder, redirect)
	if redirectRecorder.Code != http.StatusNotFound || redirectRecorder.Header().Get("Location") != "" {
		t.Fatalf("implicit redirect: status=%d location=%q", redirectRecorder.Code, redirectRecorder.Header().Get("Location"))
	}

	wrongOrigin := fixture.readRequestV1(ModulesPathV1)
	wrongOrigin.Header.Set("Origin", "null")
	wrongOriginRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(wrongOriginRecorder, wrongOrigin)
	assertErrorCodeV1(t, wrongOriginRecorder, http.StatusForbidden, controlapicontract.ErrorForbiddenV1)
}

func TestRequestAndResponseLimitsAndErrorSanitizationV1(t *testing.T) {
	t.Parallel()
	t.Run("request target", func(t *testing.T) {
		fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
		request := fixture.readRequestV1("/control/" + strings.Repeat("x", MaximumRequestTargetBytesV1))
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		assertErrorCodeV1(t, recorder, http.StatusTooManyRequests, controlapicontract.ErrorResourceExhaustedV1)
	})

	t.Run("header count", func(t *testing.T) {
		fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
		request := fixture.readRequestV1(ModulesPathV1)
		for index := range MaximumRequestHeaderCountV1 {
			request.Header.Set(fmt.Sprintf("X-Test-%02d", index), "x")
		}
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		assertErrorCodeV1(t, recorder, http.StatusTooManyRequests, controlapicontract.ErrorResourceExhaustedV1)
	})

	t.Run("header bytes", func(t *testing.T) {
		fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
		request := fixture.readRequestV1(ModulesPathV1)
		request.Header.Set("X-Oversized", strings.Repeat("x", MaximumRequestHeaderBytesV1))
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, request)
		assertErrorCodeV1(t, recorder, http.StatusTooManyRequests, controlapicontract.ErrorResourceExhaustedV1)
	})

	t.Run("response bytes", func(t *testing.T) {
		fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
		fixture.service.list = func(context.Context, controlapp.ListModulesInputV1) (controlapp.ModulesPageV1, error) {
			page := basicPageV1()
			page.Items[0].ModuleID = strings.Repeat("x", MaximumResponseBodyBytesV1)
			return page, nil
		}
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, fixture.readRequestV1(ModulesPathV1))
		assertErrorCodeV1(t, recorder, http.StatusTooManyRequests, controlapicontract.ErrorResourceExhaustedV1)
	})

	t.Run("application error is finite and sanitized", func(t *testing.T) {
		fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
		fixture.service.list = func(context.Context, controlapp.ListModulesInputV1) (controlapp.ModulesPageV1, error) {
			return controlapp.ModulesPageV1{}, fmt.Errorf("SELECT secret FROM C:"+`\private\store.db: %w`, controlapp.ErrStoreUnavailable)
		}
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, fixture.readRequestV1(ModulesPathV1))
		assertErrorCodeV1(t, recorder, http.StatusServiceUnavailable, controlapicontract.ErrorStoreUnavailableV1)
		body := recorder.Body.String()
		for _, forbidden := range []string{"SELECT", "secret", "private", "store.db"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("error leaked %q: %s", forbidden, body)
			}
		}
	})

	t.Run("finite application error mapping", func(t *testing.T) {
		cases := []struct {
			err    error
			status int
			code   controlapicontract.ErrorCodeV1
		}{
			{controlapp.ErrInvalidRequest, 400, controlapicontract.ErrorInvalidRequestV1},
			{controlapp.ErrSessionExpired, 401, controlapicontract.ErrorSessionExpiredV1},
			{controlapp.ErrForbidden, 403, controlapicontract.ErrorForbiddenV1},
			{controlapp.ErrNotFound, 404, controlapicontract.ErrorNotFoundV1},
			{controlapp.ErrCursorInvalid, 400, controlapicontract.ErrorCursorInvalidV1},
			{controlapp.ErrCursorStale, 409, controlapicontract.ErrorCursorStaleV1},
			{controlapp.ErrResourceExhausted, 429, controlapicontract.ErrorResourceExhaustedV1},
			{controlapp.ErrStoreBusy, 503, controlapicontract.ErrorStoreBusyV1},
			{controlapp.ErrStoreUnavailable, 503, controlapicontract.ErrorStoreUnavailableV1},
			{controlapp.ErrIntegrityFailure, 500, controlapicontract.ErrorIntegrityFailureV1},
			{controlapp.ErrCancelled, 408, controlapicontract.ErrorCancelledV1},
			{errors.New("unclassified private detail"), 500, controlapicontract.ErrorInternalV1},
		}
		for index, test := range cases {
			fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
			fixture.service.list = func(context.Context, controlapp.ListModulesInputV1) (controlapp.ModulesPageV1, error) {
				return controlapp.ModulesPageV1{}, test.err
			}
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, fixture.readRequestV1(ModulesPathV1))
			if recorder.Code != test.status {
				t.Fatalf("case %d status=%d want=%d", index, recorder.Code, test.status)
			}
			var envelope errorResponseV1
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil || envelope.Code != test.code {
				t.Fatalf("case %d response=%s", index, recorder.Body.String())
			}
		}
	})
}

func TestRequestDeadlineAndAdmissionCeilingsV1(t *testing.T) {
	t.Parallel()
	if controlsession.MaximumGlobalAdmissionsV1 != 32 ||
		controlsession.MaximumSessionAdmissionsV1 != 8 ||
		MaximumTransportAdmissionsV1 != 32 {
		t.Fatalf(
			"admission ceilings changed: global=%d session=%d",
			controlsession.MaximumGlobalAdmissionsV1,
			controlsession.MaximumSessionAdmissionsV1,
		)
	}
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
	fixture.service.list = func(ctx context.Context, _ controlapp.ListModulesInputV1) (controlapp.ModulesPageV1, error) {
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining < RequestTimeoutV1-time.Second || remaining > RequestTimeoutV1 {
			return controlapp.ModulesPageV1{}, fmt.Errorf("request deadline missing: %v %v", ok, remaining)
		}
		return basicPageV1(), nil
	}
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, fixture.readRequestV1(ModulesPathV1))
	if recorder.Code != http.StatusOK {
		t.Fatalf("deadline request status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTransportAdmissionCoversBootstrapAndUnauthenticatedRequestsV1(t *testing.T) {
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)},
		true,
	)
	entered := make(chan struct{}, MaximumTransportAdmissionsV1)
	release := make(chan struct{})
	var group sync.WaitGroup
	for range MaximumTransportAdmissionsV1 {
		group.Add(1)
		go func() {
			defer group.Done()
			request := newRequestV1(http.MethodPost, BootstrapPathV1, nil)
			request.Header.Set("Origin", "http://"+testAuthorityV1)
			request.Header.Set("Content-Type", "application/json")
			request.Body = &blockingRequestBodyV1{entered: entered, release: release}
			request.ContentLength = -1
			fixture.handler.ServeHTTP(httptest.NewRecorder(), request)
		}()
	}
	for range MaximumTransportAdmissionsV1 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("transport admission did not reach bounded body read")
		}
	}

	// This request has no valid envelope or session. The transport ceiling is
	// deliberately outside both routing and authentication, so it still fails
	// immediately without entering request parsing or consuming entropy.
	request := httptest.NewRequest(http.MethodGet, ModulesPathV1, nil)
	request.Host = fixture.handler.authority
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, request)
	assertErrorCodeV1(
		t,
		recorder,
		http.StatusTooManyRequests,
		controlapicontract.ErrorResourceExhaustedV1,
	)
	close(release)
	group.Wait()

	after := httptest.NewRecorder()
	fixture.handler.ServeHTTP(after, request)
	if after.Code == http.StatusTooManyRequests {
		t.Fatal("transport admission slot was not released")
	}
}

type blockingRequestBodyV1 struct {
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (body *blockingRequestBodyV1) Read([]byte) (int, error) {
	body.once.Do(func() { body.entered <- struct{}{} })
	<-body.release
	return 0, io.EOF
}

func (*blockingRequestBodyV1) Close() error { return nil }

func TestPanicAfterPartialWriteDoesNotCommitSecondEnvelopeV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
	recorder := httptest.NewRecorder()
	writer := &panicAfterPartialWriteV1{recorder: recorder}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		fixture.handler.ServeHTTP(writer, fixture.readRequestV1(ModulesPathV1))
	}()
	if recovered == nil {
		t.Fatal("post-commit panic was swallowed instead of returning to net/http")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("partial response status=%d", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "control-error/v1") || recorder.Body.Len() > 16 {
		t.Fatalf("panic recovery attempted a second response: %q", recorder.Body.String())
	}
}

func TestPerSessionAdmissionLimitReleasesExactlyV1(t *testing.T) {
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)}, true)
	entered := make(chan struct{}, controlsession.MaximumSessionAdmissionsV1)
	release := make(chan struct{})
	fixture.service.list = func(ctx context.Context, _ controlapp.ListModulesInputV1) (controlapp.ModulesPageV1, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return basicPageV1(), nil
		case <-ctx.Done():
			return controlapp.ModulesPageV1{}, controlapp.ErrCancelled
		}
	}

	var group sync.WaitGroup
	results := make(chan int, controlsession.MaximumSessionAdmissionsV1)
	for range controlsession.MaximumSessionAdmissionsV1 {
		group.Add(1)
		go func() {
			defer group.Done()
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, fixture.readRequestV1(ModulesPathV1))
			results <- recorder.Code
		}()
	}
	for range controlsession.MaximumSessionAdmissionsV1 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("admitted request did not enter application service")
		}
	}
	ninth := httptest.NewRecorder()
	fixture.handler.ServeHTTP(ninth, fixture.readRequestV1(ModulesPathV1))
	assertErrorCodeV1(t, ninth, http.StatusTooManyRequests, controlapicontract.ErrorResourceExhaustedV1)
	close(release)
	group.Wait()
	close(results)
	for status := range results {
		if status != http.StatusOK {
			t.Fatalf("admitted request status=%d", status)
		}
	}
	after := httptest.NewRecorder()
	fixture.handler.ServeHTTP(after, fixture.readRequestV1(ModulesPathV1))
	if after.Code != http.StatusOK {
		t.Fatalf("permit was not released: %d %s", after.Code, after.Body.String())
	}
}

func assertSecurityHeadersV1(t *testing.T, header http.Header) {
	t.Helper()
	wants := map[string]string{
		"Cache-Control":                "no-store",
		"Content-Security-Policy":      controlCSPV1,
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Permissions-Policy":           controlPermissionsPolicyV1,
		"Referrer-Policy":              "no-referrer",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
	}
	for name, want := range wants {
		if got := header.Get(name); got != want {
			t.Errorf("%s=%q want %q", name, got, want)
		}
	}
	if header.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("unexpected CORS header: %q", header.Get("Access-Control-Allow-Origin"))
	}
}

func TestModuleDetailPathInstanceIDEncodingV1(t *testing.T) {
	t.Parallel()

	if decoded, ok := decodePathInstanceIDV1("instance-a", ""); !ok || decoded != "instance-a" {
		t.Fatalf("raw path-safe instance decode=(%q,%t)", decoded, ok)
	}
	if decoded, ok := decodePathInstanceIDV1("~u~YWJj", ""); !ok || decoded != "~u~YWJj" {
		t.Fatalf("legacy raw prefix instance decode=(%q,%t)", decoded, ok)
	}
	for _, value := range []string{".", ".."} {
		if decoded, ok := decodePathInstanceIDV1(value, ""); ok || decoded != "" {
			t.Fatalf("ambiguous path segment %q decoded=(%q,%t)", value, decoded, ok)
		}
	}
	for _, instanceID := range []string{"模块一", "module/one", ".", "~u~reserved", "a\u0080b"} {
		encoded := base64.RawURLEncoding.EncodeToString([]byte(instanceID))
		decoded, ok := decodePathInstanceIDV1(
			encoded,
			ModuleInstanceIDEncodingBase64URLUTF8V1,
		)
		if !ok || decoded != instanceID {
			t.Fatalf("encoded instance %q decoded=(%q,%t)", instanceID, decoded, ok)
		}
	}
	for _, encoded := range []string{"", "YQ=", "_x"} {
		if _, ok := decodePathInstanceIDV1(
			encoded,
			ModuleInstanceIDEncodingBase64URLUTF8V1,
		); ok {
			t.Fatalf("non-canonical encoded instance accepted: %q", encoded)
		}
	}
	if _, ok := decodePathInstanceIDV1("aW5zdGFuY2UtYQ", "unknown"); ok {
		t.Fatal("unknown instance ID encoding accepted")
	}
}

func TestScopeFromHeadersV1DecodesCanonicalUTF8IDs(t *testing.T) {
	t.Parallel()

	tenantID := "租户甲"
	workspaceID := "工作区一"
	header := make(http.Header)
	header.Set(ScopeKindHeaderV1, string(controlapicontract.ScopeWorkspaceV1))
	header.Set(ScopeIDEncodingHeaderV1, ScopeIDEncodingBase64URLUTF8V1)
	header.Set(TenantIDHeaderV1, base64.RawURLEncoding.EncodeToString([]byte(tenantID)))
	header.Set(WorkspaceIDHeaderV1, base64.RawURLEncoding.EncodeToString([]byte(workspaceID)))
	if hasUnknownFreeAgentAuthorityHeaderV1(header) {
		t.Fatal("scope encoding header was rejected as unknown")
	}
	scope, err := scopeFromHeadersV1(header)
	if err != nil {
		t.Fatalf("decode scope headers: %v", err)
	}
	if scope.TenantID != tenantID || scope.WorkspaceID != workspaceID ||
		scope.Kind != controlapicontract.ScopeWorkspaceV1 {
		t.Fatalf("decoded scope=%+v", scope)
	}

	header.Set(TenantIDHeaderV1, "YQ=")
	if _, err := scopeFromHeadersV1(header); err == nil {
		t.Fatal("accepted padded non-canonical scope ID encoding")
	}
	header.Set(TenantIDHeaderV1, base64.RawURLEncoding.EncodeToString([]byte(tenantID)))
	header.Set(ScopeIDEncodingHeaderV1, "unknown")
	if _, err := scopeFromHeadersV1(header); err == nil {
		t.Fatal("accepted unknown scope ID encoding")
	}
}

func assertErrorCodeV1(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	status int,
	code controlapicontract.ErrorCodeV1,
) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, status, recorder.Body.String())
	}
	assertSecurityHeadersV1(t, recorder.Header())
	var response errorResponseV1
	decoder := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, recorder.Body.String())
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("trailing error response: %v", err)
	}
	if response.SchemaVersion != "control-error/v1" || response.Code != code ||
		response.CorrelationID == "" || response.Message == "" {
		t.Fatalf("error response=%+v", response)
	}
}
