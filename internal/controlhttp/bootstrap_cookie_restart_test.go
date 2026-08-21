package controlhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlsession"
)

type restartHandlerSlotV1 struct {
	current atomic.Pointer[HandlerV1]
}

func (slot *restartHandlerSlotV1) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	handler := slot.current.Load()
	if handler == nil {
		http.Error(writer, "handler unavailable", http.StatusServiceUnavailable)
		return
	}
	handler.ServeHTTP(writer, request)
}

func TestBootstrapCookieJarSameAuthorityRestartReplacesStaleCookieV1(t *testing.T) {
	var slot restartHandlerSlotV1
	server := httptest.NewUnstartedServer(&slot)
	authority := server.Listener.Addr().String()
	registryA, bootstrapA, handlerA := newRestartHandlerV1(t, authority, 0x11)
	slot.current.Store(handlerA)
	server.Start()
	t.Cleanup(server.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	firstStatus, first, firstSetCookie := performCookieJarBootstrapV1(
		t,
		client,
		server.URL,
		encodeCredentialV1(bootstrapA.Capability),
	)
	if firstStatus != http.StatusOK || firstSetCookie == "" {
		t.Fatalf("first bootstrap status=%d Set-Cookie=%q", firstStatus, firstSetCookie)
	}
	oldCookie := soleCookieJarSessionV1(t, jar, server.URL)

	// Swap the complete process-local session authority while preserving the
	// exact browser origin and CookieJar, which models a same-port restart.
	registryA.Close()
	_, bootstrapB, handlerB := newRestartHandlerV1(t, authority, 0x71)
	slot.current.Store(handlerB)

	resumeStatus, _, resumeSetCookie := performCookieJarResumeV1(
		t,
		client,
		server.URL,
		first.ResumeCredential,
	)
	if resumeStatus != http.StatusUnauthorized || resumeSetCookie != "" {
		t.Fatalf(
			"old-process resume status=%d Set-Cookie=%q",
			resumeStatus,
			resumeSetCookie,
		)
	}
	if got := soleCookieJarSessionV1(t, jar, server.URL); got != oldCookie {
		t.Fatal("rejected old-process resume changed the browser cookie")
	}

	missingStatus, _, missingSetCookie := performCookieJarBootstrapV1(
		t,
		client,
		server.URL,
		"",
	)
	if missingStatus != http.StatusUnauthorized || missingSetCookie != "" {
		t.Fatalf(
			"missing-capability bootstrap status=%d Set-Cookie=%q",
			missingStatus,
			missingSetCookie,
		)
	}
	if got := soleCookieJarSessionV1(t, jar, server.URL); got != oldCookie {
		t.Fatal("missing capability changed the browser cookie")
	}

	secondStatus, second, secondSetCookie := performCookieJarBootstrapV1(
		t,
		client,
		server.URL,
		encodeCredentialV1(bootstrapB.Capability),
	)
	if secondStatus != http.StatusOK || secondSetCookie == "" ||
		second.Session.BootID == first.Session.BootID {
		t.Fatalf(
			"restart bootstrap status=%d Set-Cookie=%q old_boot=%q new_boot=%q",
			secondStatus,
			secondSetCookie,
			first.Session.BootID,
			second.Session.BootID,
		)
	}
	newCookie := soleCookieJarSessionV1(t, jar, server.URL)
	if newCookie == oldCookie {
		t.Fatal("successful restart bootstrap did not replace the stale cookie")
	}
	newResumeStatus, _, newResumeSetCookie := performCookieJarResumeV1(
		t,
		client,
		server.URL,
		second.ResumeCredential,
	)
	if newResumeStatus != http.StatusOK || newResumeSetCookie != "" {
		t.Fatalf(
			"new-process resume status=%d Set-Cookie=%q",
			newResumeStatus,
			newResumeSetCookie,
		)
	}
}

func newRestartHandlerV1(
	t *testing.T,
	authority string,
	entropyStart uint64,
) (*controlsession.RegistryV1, controlsession.BootstrapMaterialV1, *HandlerV1) {
	t.Helper()
	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(
		[]controlapicontract.ControlScopeV1{tenantScopeV1()},
	)
	if err != nil {
		t.Fatal(err)
	}
	entropy := &sequenceReaderV1{}
	entropy.next.Store(entropyStart)
	registry, bootstrap, err := controlsession.NewRegistryV1(
		controlsession.RegistryConfigV1{
			PrincipalID: "operator-owner", AuthorizationRevision: 1,
			Capabilities: []controlapicontract.ControlCapabilityV1{
				controlapicontract.CapabilityObserveV1,
				controlapicontract.CapabilityOperateModulesV1,
			},
			ScopeSet: scopeSet, Entropy: entropy,
			Now: func() time.Time { return testNowV1 },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	handler, err := NewHandlerV1(ConfigV1{
		Authority: authority, Registry: registry,
		Modules:             &fakeModulesServiceV1{},
		ModuleDisableDryRun: &fakeModuleDisableDryRunServiceV1{},
		Cursor:              &fakeCursorCodecV1{},
		Now:                 func() time.Time { return testNowV1.Add(time.Second) },
		Entropy: bytes.NewReader(bytes.Repeat(
			[]byte{byte(entropyStart)},
			correlationBytesV1,
		)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry, bootstrap, handler
}

func performCookieJarBootstrapV1(
	t *testing.T,
	client *http.Client,
	origin string,
	capability string,
) (int, bootstrapExchangeResponseV2, string) {
	t.Helper()
	body := []byte(fmt.Sprintf(
		`{"capability":%q,"schema_version":%q}`,
		capability,
		BootstrapRequestSchemaV1,
	))
	request, err := http.NewRequest(
		http.MethodPost,
		origin+BootstrapPathV1,
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	encoded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(encoded)
	var decoded bootstrapExchangeResponseV2
	if response.StatusCode == http.StatusOK {
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode, decoded, response.Header.Get("Set-Cookie")
}

func performCookieJarResumeV1(
	t *testing.T,
	client *http.Client,
	origin string,
	resumeCredential string,
) (int, sessionResumeResponseV1, string) {
	t.Helper()
	body := []byte(fmt.Sprintf(
		`{"resume_credential":%q,"schema_version":%q}`,
		resumeCredential,
		SessionResumeRequestSchemaV1,
	))
	request, err := http.NewRequest(
		http.MethodPost,
		origin+SessionResumePathV1,
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	encoded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(encoded)
	var decoded sessionResumeResponseV1
	if response.StatusCode == http.StatusOK {
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
	}
	return response.StatusCode, decoded, response.Header.Get("Set-Cookie")
}

func soleCookieJarSessionV1(t *testing.T, jar http.CookieJar, origin string) string {
	t.Helper()
	parsed, err := url.Parse(origin + BootstrapPathV1)
	if err != nil {
		t.Fatal(err)
	}
	value := ""
	count := 0
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == SessionCookieV1 {
			count++
			value = cookie.Value
		}
	}
	if count != 1 || !validStrongCredentialCookieTestV1(value) {
		t.Fatalf("CookieJar session count=%d value_valid=%v", count, value != "")
	}
	return value
}
