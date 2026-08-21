package controlhttp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlsession"
)

func TestSessionResumeRotatesProofsWithoutChangingCookieV1(t *testing.T) {
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{tenantScopeV1(), workspaceScopeV1("workspace-b")},
		true,
	)
	recorder := performSessionResumeV1(t, fixture.handler, fixture.credential, fixture.resume)
	if recorder.Code != http.StatusOK {
		t.Fatalf("resume status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	assertSecurityHeadersV1(t, recorder.Header())
	if values := recorder.Header().Values("Set-Cookie"); len(values) != 0 {
		t.Fatalf("resume changed the session cookie: %v", values)
	}
	var response sessionResumeResponseV1
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.SchemaVersion != SessionResumeResponseSchemaV1 ||
		response.Session.SessionID == "" || response.AuthorizedScopes == nil ||
		len(response.AuthorizedScopes) != 2 || response.CSRFToken == "" ||
		response.ResumeCredential == "" || response.CSRFToken == fixture.csrf ||
		response.ResumeCredential == fixture.resume {
		t.Fatalf("invalid resume response: %s", recorder.Body.String())
	}
	decodedCSRF, ok := decodeCredentialV1(response.CSRFToken)
	if !ok {
		t.Fatal("resume response CSRF was not one canonical 32-byte credential")
	}
	clear(decodedCSRF)
	decodedResume, ok := decodeCredentialV1(response.ResumeCredential)
	if !ok {
		t.Fatal("resume response credential was not one canonical 32-byte credential")
	}
	clear(decodedResume)
	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(response.AuthorizedScopes)
	if err != nil || scopeSet.Digest() != response.Session.ScopeSetDigest {
		t.Fatalf("resume authorized scopes do not match session digest: digest=%q err=%v", scopeSet.Digest(), err)
	}

	used := performSessionResumeV1(t, fixture.handler, fixture.credential, fixture.resume)
	assertErrorCodeV1(
		t,
		used,
		http.StatusUnauthorized,
		controlapicontract.ErrorUnauthenticatedV1,
	)
	next := performSessionResumeV1(
		t,
		fixture.handler,
		fixture.credential,
		response.ResumeCredential,
	)
	if next.Code != http.StatusOK {
		t.Fatalf("rotated resume credential status=%d body=%s", next.Code, next.Body.String())
	}
	listCalls, getCalls := fixture.service.counts()
	disableCalls, _ := fixture.disable.snapshot()
	if listCalls != 0 || getCalls != 0 || disableCalls != 0 ||
		fixture.codec.decodeHits.Load() != 0 || fixture.codec.encodeHits.Load() != 0 {
		t.Fatal("session resume crossed an application or Store-facing service boundary")
	}
}

func TestSessionResumeRejectsCrossPortCookieTheftAndCookieOnlyV1(t *testing.T) {
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{tenantScopeV1()},
		true,
	)
	crossPort := newSessionResumeRequestV1(fixture.credential, fixture.resume)
	crossPort.Header.Set("Origin", "http://127.0.0.1:38118")
	crossPortRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(crossPortRecorder, crossPort)
	assertErrorCodeV1(
		t,
		crossPortRecorder,
		http.StatusForbidden,
		controlapicontract.ErrorForbiddenV1,
	)

	cookieOnlyBody := []byte(fmt.Sprintf(
		`{"resume_credential":"","schema_version":%q}`,
		SessionResumeRequestSchemaV1,
	))
	cookieOnly := newSessionResumeRequestWithBodyV1(fixture.credential, cookieOnlyBody)
	cookieOnlyRecorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(cookieOnlyRecorder, cookieOnly)
	assertErrorCodeV1(
		t,
		cookieOnlyRecorder,
		http.StatusUnauthorized,
		controlapicontract.ErrorUnauthenticatedV1,
	)

	valid := performSessionResumeV1(t, fixture.handler, fixture.credential, fixture.resume)
	if valid.Code != http.StatusOK {
		t.Fatalf("rejected theft attempts consumed the resume credential: %d %s", valid.Code, valid.Body.String())
	}
}

func TestSessionResumeConcurrentUseHasOneHTTPWinnerV1(t *testing.T) {
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{tenantScopeV1()},
		true,
	)
	const contenders = 16
	start := make(chan struct{})
	var wait sync.WaitGroup
	var successes atomic.Int32
	var unauthenticated atomic.Int32
	var unexpected atomic.Int32
	for range contenders {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			recorder := performSessionResumeV1(
				t,
				fixture.handler,
				fixture.credential,
				fixture.resume,
			)
			switch recorder.Code {
			case http.StatusOK:
				successes.Add(1)
			case http.StatusUnauthorized:
				var response errorResponseV1
				if json.Unmarshal(recorder.Body.Bytes(), &response) == nil &&
					response.Code == controlapicontract.ErrorUnauthenticatedV1 {
					unauthenticated.Add(1)
				} else {
					unexpected.Add(1)
				}
			default:
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wait.Wait()
	if successes.Load() != 1 || unauthenticated.Load() != contenders-1 ||
		unexpected.Load() != 0 {
		t.Fatalf(
			"success=%d unauthenticated=%d unexpected=%d",
			successes.Load(),
			unauthenticated.Load(),
			unexpected.Load(),
		)
	}
}

func TestSessionResumeExactRouteHeaderAndBodyContractV1(t *testing.T) {
	wrongResume := base64.RawURLEncoding.EncodeToString(
		bytes.Repeat([]byte{0xee}, controlsession.CredentialBytesV1),
	)
	tests := []struct {
		name   string
		mutate func(*http.Request)
		status int
		code   controlapicontract.ErrorCodeV1
		allow  string
	}{
		{
			name: "wrong method",
			mutate: func(request *http.Request) {
				request.Method = http.MethodGet
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
			allow:  http.MethodPost,
		},
		{
			name: "sibling route",
			mutate: func(request *http.Request) {
				request.URL.Path += "/extra"
				request.RequestURI = request.URL.Path
			},
			status: http.StatusNotFound,
			code:   controlapicontract.ErrorNotFoundV1,
		},
		{
			name: "query",
			mutate: func(request *http.Request) {
				request.URL.RawQuery = "resume_credential=x"
				request.RequestURI += "?resume_credential=x"
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "wrong content type",
			mutate: func(request *http.Request) {
				request.Header.Set("Content-Type", "application/json; charset=utf-8")
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "csrf header",
			mutate: func(request *http.Request) {
				request.Header.Set(CSRFHeaderV1, wrongResume)
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "scope header",
			mutate: func(request *http.Request) {
				request.Header.Set(ScopeKindHeaderV1, string(controlapicontract.ScopeTenantV1))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "tenant scope header",
			mutate: func(request *http.Request) {
				request.Header.Set(TenantIDHeaderV1, testTenantV1)
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "workspace scope header",
			mutate: func(request *http.Request) {
				request.Header.Set(WorkspaceIDHeaderV1, testWorkspaceV1)
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "confirmation header",
			mutate: func(request *http.Request) {
				request.Header.Set(ConfirmationHeaderV1, wrongResume)
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "evaluation digest header",
			mutate: func(request *http.Request) {
				request.Header.Set(OperationEvaluationDigestHeaderV1, strings.Repeat("a", 64))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "unknown authority header",
			mutate: func(request *http.Request) {
				request.Header.Set("X-FreeAgent-Admin", "true")
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "proxy authorization header",
			mutate: func(request *http.Request) {
				request.Header.Set("Proxy-Authorization", "Basic ambient")
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "authorization header",
			mutate: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer ambient")
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "if-none-match header",
			mutate: func(request *http.Request) {
				request.Header.Set("If-None-Match", `"`+strings.Repeat("a", 64)+`"`)
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "idempotency header",
			mutate: func(request *http.Request) {
				request.Header.Set("Idempotency-Key", "ambient")
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "conditional header",
			mutate: func(request *http.Request) {
				request.Header.Set("If-Match", `"`+strings.Repeat("a", 64)+`"`)
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "missing cookie",
			mutate: func(request *http.Request) {
				request.Header.Del("Cookie")
			},
			status: http.StatusUnauthorized,
			code:   controlapicontract.ErrorUnauthenticatedV1,
		},
		{
			name: "duplicate session cookie",
			mutate: func(request *http.Request) {
				request.AddCookie(&http.Cookie{Name: SessionCookieV1, Value: wrongResume})
			},
			status: http.StatusUnauthorized,
			code:   controlapicontract.ErrorUnauthenticatedV1,
		},
		{
			name: "wrong cookie",
			mutate: func(request *http.Request) {
				request.Header.Set("Cookie", SessionCookieV1+"="+wrongResume)
			},
			status: http.StatusUnauthorized,
			code:   controlapicontract.ErrorUnauthenticatedV1,
		},
		{
			name: "missing resume",
			mutate: func(request *http.Request) {
				body := []byte(fmt.Sprintf(
					`{"resume_credential":"","schema_version":%q}`,
					SessionResumeRequestSchemaV1,
				))
				request.Body = ioNopCloserV1(body)
				request.ContentLength = int64(len(body))
			},
			status: http.StatusUnauthorized,
			code:   controlapicontract.ErrorUnauthenticatedV1,
		},
		{
			name: "wrong resume",
			mutate: func(request *http.Request) {
				body := canonicalSessionResumeBodyV1(wrongResume)
				request.Body = ioNopCloserV1(body)
				request.ContentLength = int64(len(body))
			},
			status: http.StatusUnauthorized,
			code:   controlapicontract.ErrorUnauthenticatedV1,
		},
		{
			name: "noncanonical body",
			mutate: func(request *http.Request) {
				body := []byte(fmt.Sprintf(
					`{"schema_version":%q,"resume_credential":%q}`,
					SessionResumeRequestSchemaV1,
					wrongResume,
				))
				request.Body = ioNopCloserV1(body)
				request.ContentLength = int64(len(body))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "unknown body property",
			mutate: func(request *http.Request) {
				body := []byte(fmt.Sprintf(
					`{"ambient":true,"resume_credential":%q,"schema_version":%q}`,
					wrongResume,
					SessionResumeRequestSchemaV1,
				))
				request.Body = ioNopCloserV1(body)
				request.ContentLength = int64(len(body))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "wrong schema",
			mutate: func(request *http.Request) {
				body := []byte(fmt.Sprintf(
					`{"resume_credential":%q,"schema_version":"control-session-resume/v2"}`,
					wrongResume,
				))
				request.Body = ioNopCloserV1(body)
				request.ContentLength = int64(len(body))
			},
			status: http.StatusBadRequest,
			code:   controlapicontract.ErrorInvalidRequestV1,
		},
		{
			name: "body too large",
			mutate: func(request *http.Request) {
				body := bytes.Repeat([]byte{'x'}, MaximumSessionResumeBodyBytesV1+1)
				request.Body = ioNopCloserV1(body)
				request.ContentLength = int64(len(body))
			},
			status: http.StatusTooManyRequests,
			code:   controlapicontract.ErrorResourceExhaustedV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHandlerFixtureV1(
				t,
				[]controlapicontract.ControlScopeV1{tenantScopeV1()},
				true,
			)
			request := newSessionResumeRequestV1(fixture.credential, fixture.resume)
			test.mutate(request)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(t, recorder, test.status, test.code)
			if got := recorder.Header().Get("Allow"); got != test.allow {
				t.Fatalf("Allow=%q, want %q", got, test.allow)
			}
			if values := recorder.Header().Values("Set-Cookie"); len(values) != 0 {
				t.Fatalf("rejected resume changed cookie: %v", values)
			}
		})
	}
}

func TestSessionResumeReturnsExpiredAndNewProcessRejectsOldProofsV1(t *testing.T) {
	clock := &sessionResumeClockV1{value: testNowV1}
	fixture := newSessionResumeClockFixtureV1(t, clock, 0)
	clock.Set(testNowV1.Add(controlsession.SessionIdleLifetimeV1))
	expired := performSessionResumeV1(t, fixture.handler, fixture.credential, fixture.resume)
	assertErrorCodeV1(
		t,
		expired,
		http.StatusUnauthorized,
		controlapicontract.ErrorSessionExpiredV1,
	)

	old := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{tenantScopeV1()},
		true,
	)
	newClock := &sessionResumeClockV1{value: testNowV1}
	newProcess := newSessionResumeClockFixtureV1(t, newClock, 50_000)
	restarted := performSessionResumeV1(t, newProcess.handler, old.credential, old.resume)
	assertErrorCodeV1(
		t,
		restarted,
		http.StatusUnauthorized,
		controlapicontract.ErrorUnauthenticatedV1,
	)
}

func performSessionResumeV1(
	t *testing.T,
	handler *HandlerV1,
	cookieCredential string,
	resumeCredential string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := newSessionResumeRequestV1(cookieCredential, resumeCredential)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func newSessionResumeRequestV1(
	cookieCredential string,
	resumeCredential string,
) *http.Request {
	return newSessionResumeRequestWithBodyV1(
		cookieCredential,
		canonicalSessionResumeBodyV1(resumeCredential),
	)
}

func canonicalSessionResumeBodyV1(resumeCredential string) []byte {
	return []byte(fmt.Sprintf(
		`{"resume_credential":%q,"schema_version":%q}`,
		resumeCredential,
		SessionResumeRequestSchemaV1,
	))
}

func newSessionResumeRequestWithBodyV1(
	cookieCredential string,
	body []byte,
) *http.Request {
	request := newRequestV1(
		http.MethodPost,
		SessionResumePathV1,
		bytes.NewReader(body),
	)
	request.Header.Set("Origin", "http://"+testAuthorityV1)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{
		Name:  SessionCookieV1,
		Value: cookieCredential,
	})
	return request
}

func ioNopCloserV1(body []byte) *readCloserV1 {
	return &readCloserV1{Reader: bytes.NewReader(body)}
}

type readCloserV1 struct {
	*bytes.Reader
}

func (reader *readCloserV1) Close() error { return nil }

type sessionResumeClockV1 struct {
	mutex sync.Mutex
	value time.Time
}

func (clock *sessionResumeClockV1) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.value
}

func (clock *sessionResumeClockV1) Set(value time.Time) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.value = value
}

func newSessionResumeClockFixtureV1(
	t *testing.T,
	clock *sessionResumeClockV1,
	entropyStart uint64,
) *handlerFixtureV1 {
	t.Helper()
	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(
		[]controlapicontract.ControlScopeV1{tenantScopeV1()},
	)
	if err != nil {
		t.Fatal(err)
	}
	entropy := &sequenceReaderV1{}
	entropy.next.Store(entropyStart)
	registry, bootstrap, err := controlsession.NewRegistryV1(controlsession.RegistryConfigV1{
		PrincipalID:           "operator-owner",
		AuthorizationRevision: 1,
		Capabilities: []controlapicontract.ControlCapabilityV1{
			controlapicontract.CapabilityObserveV1,
			controlapicontract.CapabilityOperateModulesV1,
		},
		ScopeSet: scopeSet,
		Entropy:  entropy,
		Now:      clock.Now,
	})
	if err != nil {
		t.Fatal(err)
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
		Now:                 clock.Now,
		Entropy:             bytes.NewReader(bytes.Repeat([]byte{0x7a}, correlationBytesV1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := registry.ExchangeBootstrap(bootstrap.Capability)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &handlerFixtureV1{
		handler:    handler,
		registry:   registry,
		bootstrap:  bootstrap,
		service:    service,
		disable:    disable,
		codec:      codec,
		credential: encodeCredentialV1(issued.SessionCredential),
		csrf:       encodeCredentialV1(issued.CSRFToken),
		resume:     encodeCredentialV1(issued.ResumeCredential),
	}
	clear(issued.SessionCredential)
	clear(issued.CSRFToken)
	clear(issued.ResumeCredential)
	t.Cleanup(registry.Close)
	return fixture
}
