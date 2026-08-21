package controlhttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type typedNilHTTPHandlerV1 struct{}

func (*typedNilHTTPHandlerV1) ServeHTTP(http.ResponseWriter, *http.Request) {}

func TestWrapSecurityHeadersV1CoversOuterGateResponsesV1(t *testing.T) {
	t.Parallel()
	called := 0
	wrapped, err := WrapSecurityHeadersV1(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		called++
		if request == nil {
			t.Fatal("request was not forwarded")
		}
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	wrapped.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/control", nil))
	if called != 1 || recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("called=%d status=%d", called, recorder.Code)
	}
	assertSecurityHeadersV1(t, recorder.Header())
	if recorder.Body.Len() != 0 {
		t.Fatalf("wrapper synthesized a body: %q", recorder.Body.String())
	}
}

func TestWrapSecurityHeadersV1RejectsNilAndTypedNilV1(t *testing.T) {
	t.Parallel()
	var typedNil *typedNilHTTPHandlerV1
	for _, next := range []http.Handler{nil, typedNil} {
		wrapped, err := WrapSecurityHeadersV1(next)
		if wrapped != nil || !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("wrapped=%v err=%v", wrapped, err)
		}
	}
}
