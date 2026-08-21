package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlhandoff"
	"github.com/endview/freeagent/internal/controlhttp"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type runningControlSessionE2EV1 struct {
	origin      string
	chatAddress string
	handoffPath string
	handoff     controlServeE2EHandoffV1
	cancel      context.CancelFunc
	done        <-chan error
	stopped     bool
}

type controlSessionBootstrapE2EV1 struct {
	response controlServeE2EBootstrapResponseV2
	cookie   *http.Cookie
}

type controlServeE2EErrorResponseV1 struct {
	SchemaVersion string                         `json:"schema_version"`
	Code          controlapicontract.ErrorCodeV1 `json:"code"`
	CorrelationID string                         `json:"correlation_id"`
	Message       string                         `json:"message"`
}

func TestProductionControlSessionResumeSurvivesReloadAndRejectsOldProcessV1(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	originalVerifier := verifyControlNonElevatedV1
	verifyControlNonElevatedV1 = func() error { return nil }
	t.Cleanup(func() { verifyControlNonElevatedV1 = originalVerifier })

	first := startProductionControlSessionE2EV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "control-bootstrap-first.json"),
	)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	bootstrap := exchangeProductionControlBootstrapE2EV1(t, client, first)
	if got := productionCookieJarSessionValueE2EV1(t, jar, first.origin); got != bootstrap.cookie.Value {
		t.Fatal("production bootstrap response cookie was not retained by CookieJar")
	}
	assertProductionControlModulesGETE2EV1(
		t,
		client,
		first.origin,
		bootstrap.response.CSRFToken,
		http.StatusOK,
		controlapicontract.ErrorNoneV1,
	)

	// Model a browser reload: the HttpOnly cookie and exact-origin
	// sessionStorage resume proof survive, while the in-memory CSRF proof does
	// not. Cookie-only API access remains unauthenticated.
	sessionStorageResume := bootstrap.response.ResumeCredential
	originalCSRF := bootstrap.response.CSRFToken
	assertProductionControlModulesGETE2EV1(
		t,
		client,
		first.origin,
		"",
		http.StatusUnauthorized,
		controlapicontract.ErrorUnauthenticatedV1,
	)

	resumeStatus, resumed := performProductionControlResumeE2EV1(
		t,
		client,
		first.origin,
		sessionStorageResume,
	)
	if resumeStatus != http.StatusOK {
		t.Fatalf("production Control resume status=%d", resumeStatus)
	}
	if resumed.SchemaVersion != controlhttp.SessionResumeResponseSchemaV1 ||
		!reflect.DeepEqual(resumed.Session, bootstrap.response.Session) ||
		!reflect.DeepEqual(resumed.AuthorizedScopes, bootstrap.response.AuthorizedScopes) ||
		!validEncodedControlCredentialE2EV1(resumed.CSRFToken) ||
		!validEncodedControlCredentialE2EV1(resumed.ResumeCredential) ||
		resumed.CSRFToken == originalCSRF ||
		resumed.ResumeCredential == sessionStorageResume ||
		resumed.CSRFToken == resumed.ResumeCredential {
		t.Fatal("production Control resume did not rotate an exact safe session projection")
	}
	resumedScopeSet, err := controlsession.NewAuthorizedScopeSetV1(resumed.AuthorizedScopes)
	if err != nil || resumedScopeSet.Digest() != resumed.Session.ScopeSetDigest {
		t.Fatalf("resumed authorized scope projection: %v", err)
	}

	// Rotation invalidates both old browser-held proofs. A rejected old-resume
	// replay must not prevent the new CSRF proof from authorizing API reads.
	assertProductionControlModulesGETE2EV1(
		t,
		client,
		first.origin,
		originalCSRF,
		http.StatusUnauthorized,
		controlapicontract.ErrorUnauthenticatedV1,
	)
	oldResumeStatus, _ := performProductionControlResumeE2EV1(
		t,
		client,
		first.origin,
		sessionStorageResume,
	)
	if oldResumeStatus != http.StatusUnauthorized {
		t.Fatalf("rotated resume proof status=%d, want 401", oldResumeStatus)
	}
	assertProductionControlModulesGETE2EV1(
		t,
		client,
		first.origin,
		resumed.CSRFToken,
		http.StatusOK,
		controlapicontract.ErrorNoneV1,
	)

	oldProcessBootID := resumed.Session.BootID
	oldProcessResume := resumed.ResumeCredential
	oldProcessCookie := productionCookieJarSessionValueE2EV1(t, jar, first.origin)
	firstOrigin := first.origin
	first.stopV1(t)

	second := startProductionControlSessionE2EV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "control-bootstrap-second.json"),
	)
	if second.origin == firstOrigin {
		t.Fatal("production restart did not exercise a different Control port")
	}
	if got := productionCookieJarSessionValueE2EV1(t, jar, second.origin); got != oldProcessCookie {
		t.Fatal("host-only session cookie did not cross the Control port boundary")
	}
	restartStatus, _ := performProductionControlResumeE2EV1(
		t,
		client,
		second.origin,
		oldProcessResume,
	)
	if restartStatus != http.StatusUnauthorized {
		t.Fatalf("previous-process resume status=%d, want 401", restartStatus)
	}
	if got := productionCookieJarSessionValueE2EV1(t, jar, second.origin); got != oldProcessCookie {
		t.Fatal("rejected previous-process resume changed the stale browser cookie")
	}
	assertMissingProductionBootstrapCapabilityDoesNotReplaceCookieE2EV1(
		t,
		client,
		jar,
		second,
		oldProcessCookie,
	)
	secondBootstrap := exchangeProductionControlBootstrapE2EV1(t, client, second)
	if secondBootstrap.response.Session.BootID == oldProcessBootID {
		t.Fatal("Control restart reused the previous process Boot identity")
	}
	if got := productionCookieJarSessionValueE2EV1(t, jar, second.origin); got == oldProcessCookie || got != secondBootstrap.cookie.Value {
		t.Fatal("new bootstrap did not rotate the stale cross-port CookieJar credential")
	}
	assertProductionControlModulesGETE2EV1(
		t,
		client,
		second.origin,
		secondBootstrap.response.CSRFToken,
		http.StatusOK,
		controlapicontract.ErrorNoneV1,
	)
	second.stopV1(t)
}

func startProductionControlSessionE2EV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	handoffPath string,
) *runningControlSessionE2EV1 {
	t.Helper()
	installControlServeE2EHandoffPublisherV1(t, handoffPath)
	serveContext, cancel := context.WithCancel(context.Background())
	stdoutReader, stdoutWriter := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := run(
			serveContext,
			[]string{
				"serve",
				"--db", databasePath,
				"--artifact-root", artifactRoot,
				"--tenant", defaultTenantID,
				"--listen", "127.0.0.1:0",
				"--enable-control",
				"--control-handoff-path", handoffPath,
			},
			stdoutWriter,
			io.Discard,
		)
		_ = stdoutWriter.CloseWithError(err)
		done <- err
	}()

	var ready map[string]string
	if err := json.NewDecoder(stdoutReader).Decode(&ready); err != nil {
		cancel()
		t.Fatalf("decode Control session readiness: %v", err)
	}
	if ready["status"] != "ready" || ready["control"] != "enabled" ||
		ready["listen"] == "" {
		cancel()
		t.Fatalf("Control session readiness=%v", ready)
	}
	handoffCanonical, err := os.ReadFile(handoffPath)
	if err != nil {
		cancel()
		t.Fatalf("read Control session handoff: %v", err)
	}
	defer clear(handoffCanonical)
	var handoff controlServeE2EHandoffV1
	decodeControlServeE2ESecretJSONV1(t, handoffCanonical, &handoff)
	if handoff.SchemaVersion != controlhandoff.SchemaVersionV1 ||
		!strings.HasPrefix(handoff.Origin, "http://127.0.0.1:") ||
		!validEncodedControlCredentialE2EV1(handoff.Capability) ||
		strings.TrimPrefix(handoff.Origin, "http://") == ready["listen"] {
		cancel()
		t.Fatal("Control session handoff is incomplete or shares the Chat listener")
	}

	serve := &runningControlSessionE2EV1{
		origin:      handoff.Origin,
		chatAddress: ready["listen"],
		handoffPath: handoffPath,
		handoff:     handoff,
		cancel:      cancel,
		done:        done,
	}
	t.Cleanup(func() { serve.stopV1(t) })
	return serve
}

func (serve *runningControlSessionE2EV1) stopV1(t *testing.T) {
	t.Helper()
	if serve == nil || serve.stopped {
		return
	}
	serve.stopped = true
	serve.cancel()
	select {
	case err := <-serve.done:
		if err != nil {
			t.Fatalf("Control session serve shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Control session serve did not shut down")
	}
}

func exchangeProductionControlBootstrapE2EV1(
	t *testing.T,
	client *http.Client,
	serve *runningControlSessionE2EV1,
) controlSessionBootstrapE2EV1 {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"schema_version": controlhttp.BootstrapRequestSchemaV1,
		"capability":     serve.handoff.Capability,
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		serve.origin+controlhttp.BootstrapPathV1,
		bytes.NewReader(canonical),
	)
	if err != nil {
		clear(canonical)
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", serve.origin)
	response, err := client.Do(request)
	clear(canonical)
	if err != nil {
		t.Fatalf("exchange production Control bootstrap: %v", err)
	}
	body := readControlServeE2EResponseV1(t, response)
	defer clear(body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("production Control bootstrap status=%d", response.StatusCode)
	}
	var decoded controlServeE2EBootstrapResponseV2
	decodeControlServeE2ESecretJSONV1(t, body, &decoded)
	if decoded.SchemaVersion != controlhttp.BootstrapResponseSchemaV2 ||
		!validEncodedControlCredentialE2EV1(decoded.CSRFToken) ||
		!validEncodedControlCredentialE2EV1(decoded.ResumeCredential) ||
		decoded.CSRFToken == decoded.ResumeCredential {
		t.Fatal("production Control bootstrap did not return distinct v2 browser proofs")
	}
	_, sessionCanonical, _, err := controlapicontract.NewControlSessionV1(decoded.Session)
	clear(sessionCanonical)
	if err != nil {
		t.Fatalf("production Control bootstrap session: %v", err)
	}
	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(decoded.AuthorizedScopes)
	if err != nil || scopeSet.Digest() != decoded.Session.ScopeSetDigest ||
		len(decoded.AuthorizedScopes) != 1 ||
		decoded.AuthorizedScopes[0].Kind != controlapicontract.ScopeTenantV1 ||
		decoded.AuthorizedScopes[0].TenantID != defaultTenantID {
		t.Fatalf("production Control bootstrap authorized scopes: %v", err)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == controlhttp.SessionCookieV1 {
			copy := *cookie
			sessionCookie = &copy
		}
	}
	if sessionCookie == nil || !validEncodedControlCredentialE2EV1(sessionCookie.Value) ||
		sessionCookie.Path != "/control/" || sessionCookie.Domain != "" ||
		!sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("production Control bootstrap returned an unsafe session cookie")
	}
	waitForControlServeE2EAbsentV1(t, serve.handoffPath, time.Second)
	return controlSessionBootstrapE2EV1{response: decoded, cookie: sessionCookie}
}

func assertMissingProductionBootstrapCapabilityDoesNotReplaceCookieE2EV1(
	t *testing.T,
	client *http.Client,
	jar http.CookieJar,
	serve *runningControlSessionE2EV1,
	wantCookie string,
) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"schema_version": controlhttp.BootstrapRequestSchemaV1,
		"capability":     "",
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		serve.origin+controlhttp.BootstrapPathV1,
		bytes.NewReader(canonical),
	)
	if err != nil {
		clear(canonical)
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", serve.origin)
	response, err := client.Do(request)
	clear(canonical)
	if err != nil {
		t.Fatalf("missing-capability production bootstrap: %v", err)
	}
	body := readControlServeE2EResponseV1(t, response)
	defer clear(body)
	if response.StatusCode != http.StatusUnauthorized ||
		response.Header.Get("Set-Cookie") != "" {
		t.Fatalf(
			"missing-capability production bootstrap status=%d Set-Cookie=%q",
			response.StatusCode,
			response.Header.Get("Set-Cookie"),
		)
	}
	assertControlServeE2EErrorV1(
		t,
		body,
		controlapicontract.ErrorUnauthenticatedV1,
	)
	if got := productionCookieJarSessionValueE2EV1(t, jar, serve.origin); got != wantCookie {
		t.Fatal("missing bootstrap capability replaced the stale CookieJar credential")
	}
}

func productionCookieJarSessionValueE2EV1(
	t *testing.T,
	jar http.CookieJar,
	origin string,
) string {
	t.Helper()
	parsed, err := url.Parse(origin + controlhttp.BootstrapPathV1)
	if err != nil {
		t.Fatal(err)
	}
	value := ""
	count := 0
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == controlhttp.SessionCookieV1 {
			count++
			value = cookie.Value
		}
	}
	if count != 1 || !validEncodedControlCredentialE2EV1(value) {
		t.Fatalf("production CookieJar session count=%d valid=%v", count, value != "")
	}
	return value
}

func performProductionControlResumeE2EV1(
	t *testing.T,
	client *http.Client,
	origin string,
	resumeCredential string,
) (int, controlServeE2ESessionResumeResponseV1) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{
		"schema_version":    controlhttp.SessionResumeRequestSchemaV1,
		"resume_credential": resumeCredential,
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		origin+controlhttp.SessionResumePathV1,
		bytes.NewReader(canonical),
	)
	if err != nil {
		clear(canonical)
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	response, err := client.Do(request)
	clear(canonical)
	if err != nil {
		t.Fatalf("resume production Control session: %v", err)
	}
	body := readControlServeE2EResponseV1(t, response)
	defer clear(body)
	if len(response.Cookies()) != 0 || response.Header.Get("Set-Cookie") != "" ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("Control resume changed the host-only cookie or omitted no-store")
	}
	if response.StatusCode != http.StatusOK {
		assertControlServeE2EErrorV1(
			t,
			body,
			controlapicontract.ErrorUnauthenticatedV1,
		)
		return response.StatusCode, controlServeE2ESessionResumeResponseV1{}
	}
	var decoded controlServeE2ESessionResumeResponseV1
	decodeControlServeE2ESecretJSONV1(t, body, &decoded)
	return response.StatusCode, decoded
}

func assertProductionControlModulesGETE2EV1(
	t *testing.T,
	client *http.Client,
	origin string,
	csrf string,
	wantStatus int,
	wantCode controlapicontract.ErrorCodeV1,
) {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodGet,
		origin+controlhttp.ModulesPathV1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	setControlServeE2ETenantScopeV1(request)
	if csrf != "" {
		request.Header.Set(controlhttp.CSRFHeaderV1, csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET production Control modules: %v", err)
	}
	body := readControlServeE2EResponseV1(t, response)
	defer clear(body)
	if response.StatusCode != wantStatus {
		t.Fatalf("production Control modules status=%d, want %d", response.StatusCode, wantStatus)
	}
	if response.Header.Get("Cache-Control") != "no-store" ||
		response.Header.Get("Set-Cookie") != "" {
		t.Fatal("production Control modules response violated cookie/cache policy")
	}
	if wantStatus != http.StatusOK {
		assertControlServeE2EErrorV1(t, body, wantCode)
		return
	}
	var page controlServeE2EModulesPageV1
	decodeControlServeE2EJSONV1(t, body, &page)
	if page.SchemaVersion != controlhttp.ModulesHTTPPageSchemaV1 ||
		page.PublishedPointer.ResourceID != defaultTenantID ||
		page.PublishedPointer.Validate() != nil {
		t.Fatal("production Control modules returned an invalid authorized projection")
	}
}

func assertControlServeE2EErrorV1(
	t *testing.T,
	body []byte,
	wantCode controlapicontract.ErrorCodeV1,
) {
	t.Helper()
	var response controlServeE2EErrorResponseV1
	decodeControlServeE2EJSONV1(t, body, &response)
	if response.SchemaVersion != "control-error/v1" || response.Code != wantCode ||
		response.CorrelationID == "" || response.Message == "" {
		t.Fatalf("Control error envelope code=%q, want %q", response.Code, wantCode)
	}
}

func validEncodedControlCredentialE2EV1(value string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	valid := err == nil && len(decoded) == controlsession.CredentialBytesV1 &&
		base64.RawURLEncoding.EncodeToString(decoded) == value
	clear(decoded)
	return valid
}
