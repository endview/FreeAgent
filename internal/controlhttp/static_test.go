package controlhttp

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
)

type staticResolverFixtureV1 struct {
	mutex   sync.Mutex
	calls   []string
	resolve func(string) (StaticAssetV1, bool, error)
}

func (resolver *staticResolverFixtureV1) ResolveStaticAssetV1(
	path string,
) (StaticAssetV1, bool, error) {
	resolver.mutex.Lock()
	resolver.calls = append(resolver.calls, path)
	function := resolver.resolve
	resolver.mutex.Unlock()
	if function == nil {
		return staticFixtureAssetV1(path), true, nil
	}
	return function(path)
}

func (resolver *staticResolverFixtureV1) paths() []string {
	resolver.mutex.Lock()
	defer resolver.mutex.Unlock()
	return append([]string(nil), resolver.calls...)
}

func staticFixtureAssetV1(path string) StaticAssetV1 {
	mediaType := ""
	switch path {
	case "index.html":
		mediaType = "text/html; charset=utf-8"
	case "assets/app.css":
		mediaType = "text/css; charset=utf-8"
	case "assets/app.js", "assets/react.js", "assets/tanstack-query.js":
		mediaType = "text/javascript; charset=utf-8"
	}
	payload := []byte("fixture:" + path + "\n")
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	return StaticAssetV1{
		Path: path, MediaType: mediaType, Size: uint64(len(payload)),
		SHA256: digest, Bytes: payload,
	}
}

func newStaticHandlerFixtureV1(
	t *testing.T,
	resolver StaticAssetResolverV1,
) *handlerFixtureV1 {
	t.Helper()
	fixture := newHandlerFixtureV1(t, []controlapicontract.ControlScopeV1{tenantScopeV1()}, false)
	fixture.handler.staticAssets = resolver
	return fixture
}

func staticRequestV1(method, path string) *http.Request {
	return newRequestV1(method, path, nil)
}

func TestStaticUIExactGETHEADAndConditionalTransportV1(t *testing.T) {
	t.Parallel()
	resolver := &staticResolverFixtureV1{}
	fixture := newStaticHandlerFixtureV1(t, resolver)
	routes := []struct {
		urlPath   string
		assetPath string
	}{
		{StaticUIRootPathV1, "index.html"},
		{StaticUIIndexPathV1, "index.html"},
		{StaticUIAppCSSPathV1, "assets/app.css"},
		{StaticUIAppJSPathV1, "assets/app.js"},
		{StaticUIReactJSPathV1, "assets/react.js"},
		{StaticUITanStackQueryJSPathV1, "assets/tanstack-query.js"},
	}
	for _, route := range routes {
		route := route
		t.Run(route.urlPath, func(t *testing.T) {
			get := httptest.NewRecorder()
			fixture.handler.ServeHTTP(get, staticRequestV1(http.MethodGet, route.urlPath))
			want := staticFixtureAssetV1(route.assetPath)
			if get.Code != http.StatusOK || !bytes.Equal(get.Body.Bytes(), want.Bytes) {
				t.Fatalf("GET status=%d body=%q want=%q", get.Code, get.Body.Bytes(), want.Bytes)
			}
			assertStaticResponseHeadersV1(t, get.Header(), want, true)

			head := httptest.NewRecorder()
			fixture.handler.ServeHTTP(head, staticRequestV1(http.MethodHead, route.urlPath))
			if head.Code != http.StatusOK || head.Body.Len() != 0 {
				t.Fatalf("HEAD status=%d body=%q", head.Code, head.Body.String())
			}
			assertStaticResponseHeadersV1(t, head.Header(), want, true)
			for _, name := range []string{
				"Cache-Control", "Content-Security-Policy", "Referrer-Policy",
				"X-Content-Type-Options", "X-Frame-Options",
				"Cross-Origin-Opener-Policy", "Cross-Origin-Resource-Policy",
				"Permissions-Policy", "Content-Type", "Content-Length", "ETag",
			} {
				if get.Header().Get(name) != head.Header().Get(name) {
					t.Errorf("GET/HEAD %s differ: %q / %q", name, get.Header().Get(name), head.Header().Get(name))
				}
			}

			conditionalRequest := staticRequestV1(http.MethodGet, route.urlPath)
			conditionalRequest.Header.Set("If-None-Match", get.Header().Get("ETag"))
			conditional := httptest.NewRecorder()
			fixture.handler.ServeHTTP(conditional, conditionalRequest)
			if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 ||
				conditional.Header().Get("ETag") != get.Header().Get("ETag") {
				t.Fatalf("conditional status=%d ETag=%q body=%q", conditional.Code, conditional.Header().Get("ETag"), conditional.Body.String())
			}
			assertSecurityHeadersV1(t, conditional.Header())
		})
	}
	wantCalls := []string{
		"index.html", "index.html", "index.html",
		"index.html", "index.html", "index.html",
		"assets/app.css", "assets/app.css", "assets/app.css",
		"assets/app.js", "assets/app.js", "assets/app.js",
		"assets/react.js", "assets/react.js", "assets/react.js",
		"assets/tanstack-query.js", "assets/tanstack-query.js", "assets/tanstack-query.js",
	}
	if got := resolver.paths(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("resolver paths=%v want=%v", got, wantCalls)
	}
}

func TestStaticUIRejectsNonReadEnvelopeBeforeResolverV1(t *testing.T) {
	t.Parallel()
	resolver := &staticResolverFixtureV1{}
	fixture := newStaticHandlerFixtureV1(t, resolver)
	tests := []struct {
		name   string
		method string
		path   string
		mutate func(*http.Request)
		status int
	}{
		{"query", http.MethodGet, StaticUIRootPathV1 + "?x=1", nil, http.StatusBadRequest},
		{"content type", http.MethodGet, StaticUIRootPathV1, func(request *http.Request) { request.Header.Set("Content-Type", "text/plain") }, http.StatusBadRequest},
		{"range", http.MethodGet, StaticUIAppJSPathV1, func(request *http.Request) { request.Header.Set("Range", "bytes=0-1") }, http.StatusBadRequest},
		{"modified since", http.MethodGet, StaticUIAppJSPathV1, func(request *http.Request) { request.Header.Set("If-Modified-Since", "Thu, 13 Aug 2026 12:00:00 GMT") }, http.StatusBadRequest},
		{"malformed if none match", http.MethodGet, StaticUIAppJSPathV1, func(request *http.Request) { request.Header.Set("If-None-Match", "*") }, http.StatusBadRequest},
		{"multiple if none match", http.MethodGet, StaticUIAppJSPathV1, func(request *http.Request) {
			request.Header.Add("If-None-Match", `"`+fmt.Sprintf("%064x", 1)+`"`)
			request.Header.Add("If-None-Match", `"`+fmt.Sprintf("%064x", 2)+`"`)
		}, http.StatusBadRequest},
		{"authority", http.MethodGet, StaticUIRootPathV1, func(request *http.Request) { request.Header.Set("Authorization", "Bearer secret") }, http.StatusBadRequest},
		{"scope authority", http.MethodGet, StaticUIRootPathV1, func(request *http.Request) { request.Header.Set(TenantIDHeaderV1, testTenantV1) }, http.StatusBadRequest},
		{"mutation", http.MethodGet, StaticUIRootPathV1, func(request *http.Request) { request.Header.Set("If-Match", `"`+fmt.Sprintf("%064x", 1)+`"`) }, http.StatusBadRequest},
		{"forwarded", http.MethodGet, StaticUIRootPathV1, func(request *http.Request) { request.Header.Set("Forwarded", "host=elsewhere") }, http.StatusBadRequest},
		{"post", http.MethodPost, StaticUIRootPathV1, func(request *http.Request) { request.Header.Set("Origin", "http://"+testAuthorityV1) }, http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := staticRequestV1(test.method, test.path)
			if test.mutate != nil {
				test.mutate(request)
			}
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(t, recorder, test.status, controlapicontract.ErrorInvalidRequestV1)
		})
	}
	if calls := resolver.paths(); len(calls) != 0 {
		t.Fatalf("rejected envelopes reached resolver: %v", calls)
	}
}

func TestStaticUIIgnoresAmbientCookiesAndReturnsIdenticalRepresentationV1(t *testing.T) {
	t.Parallel()
	resolver := &staticResolverFixtureV1{}
	fixture := newStaticHandlerFixtureV1(t, resolver)

	withoutCookie := httptest.NewRecorder()
	fixture.handler.ServeHTTP(
		withoutCookie,
		staticRequestV1(http.MethodGet, StaticUIIndexPathV1),
	)
	withCookieRequest := staticRequestV1(http.MethodGet, StaticUIIndexPathV1)
	withCookieRequest.Header.Set("Cookie", "theme=dark; ambient=value")
	withCookie := httptest.NewRecorder()
	fixture.handler.ServeHTTP(withCookie, withCookieRequest)

	if withoutCookie.Code != http.StatusOK || withCookie.Code != http.StatusOK ||
		!bytes.Equal(withoutCookie.Body.Bytes(), withCookie.Body.Bytes()) ||
		withoutCookie.Header().Get("ETag") != withCookie.Header().Get("ETag") ||
		withoutCookie.Header().Get("Content-Length") != withCookie.Header().Get("Content-Length") ||
		withoutCookie.Header().Get("Content-Type") != withCookie.Header().Get("Content-Type") ||
		withCookie.Header().Get("Set-Cookie") != "" {
		t.Fatalf(
			"cookie changed public representation: without=(%d,%q,%q) with=(%d,%q,%q)",
			withoutCookie.Code, withoutCookie.Header().Get("ETag"), withoutCookie.Body.String(),
			withCookie.Code, withCookie.Header().Get("ETag"), withCookie.Body.String(),
		)
	}
}

func TestStaticUIUnknownNilAndResolverFailuresFailClosedV1(t *testing.T) {
	t.Parallel()
	fixture := newStaticHandlerFixtureV1(t, nil)
	for _, path := range []string{
		StaticUIRootPathV1,
		"/control/ui",
		"/control/ui/assets/../index.html",
		"/control/ui/assets/app.js.map",
		"/control/ui/unknown",
	} {
		recorder := httptest.NewRecorder()
		fixture.handler.ServeHTTP(recorder, staticRequestV1(http.MethodGet, path))
		assertErrorCodeV1(t, recorder, http.StatusNotFound, controlapicontract.ErrorNotFoundV1)
	}

	tests := []struct {
		name    string
		resolve func(string) (StaticAssetV1, bool, error)
		status  int
		code    controlapicontract.ErrorCodeV1
	}{
		{"missing", func(string) (StaticAssetV1, bool, error) { return StaticAssetV1{}, false, nil }, http.StatusNotFound, controlapicontract.ErrorNotFoundV1},
		{"error", func(string) (StaticAssetV1, bool, error) {
			return StaticAssetV1{}, true, errors.New("private resolver detail")
		}, http.StatusInternalServerError, controlapicontract.ErrorIntegrityFailureV1},
		{"wrong path", func(path string) (StaticAssetV1, bool, error) {
			asset := staticFixtureAssetV1(path)
			asset.Path = "index.html"
			return asset, true, nil
		}, http.StatusInternalServerError, controlapicontract.ErrorIntegrityFailureV1},
		{"wrong media", func(path string) (StaticAssetV1, bool, error) {
			asset := staticFixtureAssetV1(path)
			asset.MediaType = "application/octet-stream"
			return asset, true, nil
		}, http.StatusInternalServerError, controlapicontract.ErrorIntegrityFailureV1},
		{"wrong size", func(path string) (StaticAssetV1, bool, error) {
			asset := staticFixtureAssetV1(path)
			asset.Size++
			return asset, true, nil
		}, http.StatusInternalServerError, controlapicontract.ErrorIntegrityFailureV1},
		{"wrong digest", func(path string) (StaticAssetV1, bool, error) {
			asset := staticFixtureAssetV1(path)
			asset.SHA256 = fmt.Sprintf("%064x", 1)
			return asset, true, nil
		}, http.StatusInternalServerError, controlapicontract.ErrorIntegrityFailureV1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &staticResolverFixtureV1{resolve: test.resolve}
			failureFixture := newStaticHandlerFixtureV1(t, resolver)
			recorder := httptest.NewRecorder()
			failureFixture.handler.ServeHTTP(recorder, staticRequestV1(http.MethodGet, StaticUIAppJSPathV1))
			assertErrorCodeV1(t, recorder, test.status, test.code)
			if bytes.Contains(recorder.Body.Bytes(), []byte("private resolver detail")) {
				t.Fatal("resolver detail escaped error mapping")
			}
		})
	}
}

func assertStaticResponseHeadersV1(
	t *testing.T,
	header http.Header,
	asset StaticAssetV1,
	wantLength bool,
) {
	t.Helper()
	assertSecurityHeadersV1(t, header)
	wants := map[string]string{
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Permissions-Policy":           controlPermissionsPolicyV1,
		"Content-Type":                 asset.MediaType,
		"ETag":                         `"` + asset.SHA256 + `"`,
	}
	if wantLength {
		wants["Content-Length"] = strconv.FormatUint(asset.Size, 10)
	}
	for name, want := range wants {
		if got := header.Get(name); got != want {
			t.Errorf("%s=%q want=%q", name, got, want)
		}
	}
	for _, absent := range []string{
		"Access-Control-Allow-Origin", "Accept-Ranges", "Content-Encoding",
		"Transfer-Encoding",
	} {
		if got := header.Get(absent); got != "" {
			t.Errorf("unexpected %s=%q", absent, got)
		}
	}
}
