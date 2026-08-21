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

type overviewPreflightServiceV1 struct {
	calls atomic.Int32
}

func (service *overviewPreflightServiceV1) GetOverviewV1(
	context.Context,
	controlapp.GetOverviewInputV1,
) (controlapp.OverviewResultV1, error) {
	service.calls.Add(1)
	return controlapp.OverviewResultV1{}, controlapp.ErrNotFound
}

func TestOverviewConditionalHeadersArePreflightedBeforeApplicationV1(t *testing.T) {
	t.Parallel()
	fixture := newHandlerFixtureV1(
		t,
		[]controlapicontract.ControlScopeV1{workspaceScopeV1(testWorkspaceV1)},
		true,
	)
	service := &overviewPreflightServiceV1{}
	fixture.handler.overview = service
	strong := `"` + strings.Repeat("a", 64) + `"`
	tests := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"wildcard", func(request *http.Request) { request.Header.Set("If-None-Match", "*") }},
		{"weak", func(request *http.Request) { request.Header.Set("If-None-Match", "W/"+strong) }},
		{"malformed", func(request *http.Request) { request.Header.Set("If-None-Match", `"short"`) }},
		{"multiple", func(request *http.Request) {
			request.Header.Add("If-None-Match", strong)
			request.Header.Add("If-None-Match", strong)
		}},
		{"if match", func(request *http.Request) { request.Header.Set("If-Match", strong) }},
		{"modified since", func(request *http.Request) { request.Header.Set("If-Modified-Since", "Thu, 13 Aug 2026 12:00:00 GMT") }},
		{"if range", func(request *http.Request) { request.Header.Set("If-Range", strong) }},
		{"unmodified since", func(request *http.Request) {
			request.Header.Set("If-Unmodified-Since", "Thu, 13 Aug 2026 12:00:00 GMT")
		}},
		{"range", func(request *http.Request) { request.Header.Set("Range", "bytes=0-1") }},
		{"content range", func(request *http.Request) { request.Header.Set("Content-Range", "bytes 0-1/2") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := service.calls.Load()
			request := fixture.readRequestV1(OverviewPathV1)
			test.mutate(request)
			recorder := httptest.NewRecorder()
			fixture.handler.ServeHTTP(recorder, request)
			assertErrorCodeV1(
				t,
				recorder,
				http.StatusBadRequest,
				controlapicontract.ErrorInvalidRequestV1,
			)
			if after := service.calls.Load(); after != before {
				t.Fatalf("preflight rejection reached application: before=%d after=%d", before, after)
			}
			if got := recorder.Header().Get("ETag"); got != "" {
				t.Fatalf("preflight rejection retained ETag %q", got)
			}
		})
	}

	valid := fixture.readRequestV1(OverviewPathV1)
	valid.Header.Set("If-None-Match", strong)
	recorder := httptest.NewRecorder()
	fixture.handler.ServeHTTP(recorder, valid)
	assertErrorCodeV1(t, recorder, http.StatusNotFound, controlapicontract.ErrorNotFoundV1)
	if calls := service.calls.Load(); calls != 1 {
		t.Fatalf("valid strong If-None-Match application calls=%d want=1", calls)
	}
}
