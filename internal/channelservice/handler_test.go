package channelservice

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestHTTPHandlerUsesOneDecoderAndOneIngressPath(t *testing.T) {
	decoder := &handlerDecoder{envelope: handlerEnvelope()}
	ingress := &handlerIngress{result: IngressResult{
		IngressKey: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RunID:      "run-channel-handler",
	}}
	handler, err := NewHTTPHandler(
		"tenant-handler",
		"workspace-handler",
		"/channel/inbound",
		decoder,
		ingress,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/channel/inbound",
		strings.NewReader(`{}`),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || decoder.calls != 1 || ingress.calls != 1 {
		t.Fatalf(
			"status=%d decoder=%d ingress=%d body=%s",
			response.Code, decoder.calls, ingress.calls, response.Body.String(),
		)
	}
	if ingress.input.TenantID != "tenant-handler" ||
		ingress.input.WorkspaceID != "workspace-handler" ||
		ingress.input.Envelope.EndpointID != "endpoint-handler" ||
		!strings.Contains(response.Body.String(), `"status":"accepted"`) ||
		response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("input=%+v headers=%v body=%s", ingress.input, response.Header(), response.Body.String())
	}
}

func TestHTTPHandlerReturnsDurableRejectedReceiptAsProcessed(t *testing.T) {
	decoder := &handlerDecoder{envelope: handlerEnvelope()}
	ingress := &handlerIngress{result: IngressResult{
		IngressKey: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Receipt: currentstore.ChannelIngressReceipt{
			Disposition: currentstore.ChannelIngressRejected,
			Created:     true,
		},
	}}
	handler, err := NewHTTPHandler(
		"tenant-handler",
		"workspace-handler",
		"/channel/inbound",
		decoder,
		ingress,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/channel/inbound",
		strings.NewReader(`{}`),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || decoder.calls != 1 || ingress.calls != 1 ||
		!strings.Contains(response.Body.String(), `"status":"rejected"`) ||
		!strings.Contains(response.Body.String(), `"ingress_key":"bbbb`) ||
		strings.Contains(response.Body.String(), `"run_id"`) {
		t.Fatalf(
			"status=%d decoder=%d ingress=%d body=%s",
			response.Code,
			decoder.calls,
			ingress.calls,
			response.Body.String(),
		)
	}
}

func TestHTTPHandlerFailsClosedWithoutLeakingErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		decodeErr  error
		ingressErr error
		want       int
	}{
		{name: "wrong path", path: "/wrong", want: http.StatusNotFound},
		{name: "decoder", path: "/channel/inbound", decodeErr: errors.New("secret-value"), want: http.StatusBadRequest},
		{name: "denied", path: "/channel/inbound", ingressErr: ErrIngressDenied, want: http.StatusForbidden},
		{name: "cursor conflict", path: "/channel/inbound", ingressErr: currentstore.ErrChannelIngressConflict, want: http.StatusConflict},
		{name: "internal", path: "/channel/inbound", ingressErr: errors.New("database detail"), want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder := &handlerDecoder{envelope: handlerEnvelope(), err: test.decodeErr}
			ingress := &handlerIngress{err: test.ingressErr}
			handler, err := NewHTTPHandler(
				"tenant-handler", "workspace-handler", "/channel/inbound",
				decoder, ingress,
			)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"http://127.0.0.1"+test.path,
				strings.NewReader(`{}`),
			)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want ||
				strings.Contains(response.Body.String(), "secret-value") ||
				strings.Contains(response.Body.String(), "database detail") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestNewHTTPHandlerRejectsImplicitOrAmbiguousConfiguration(t *testing.T) {
	decoder := &handlerDecoder{envelope: handlerEnvelope()}
	ingress := &handlerIngress{}
	for _, test := range []struct {
		tenant, workspace, path string
		decoder                 InboundDecoder
		ingress                 IngressUseCase
	}{
		{workspace: "workspace", path: "/channel", decoder: decoder, ingress: ingress},
		{tenant: "tenant", path: "/channel", decoder: decoder, ingress: ingress},
		{tenant: "tenant", workspace: "workspace", path: "channel", decoder: decoder, ingress: ingress},
		{tenant: "tenant", workspace: "workspace", path: "/channel?x=1", decoder: decoder, ingress: ingress},
		{tenant: "tenant", workspace: "workspace", path: "/channel", ingress: ingress},
		{tenant: "tenant", workspace: "workspace", path: "/channel", decoder: decoder},
	} {
		if _, err := NewHTTPHandler(
			test.tenant, test.workspace, test.path, test.decoder, test.ingress,
		); err == nil {
			t.Fatalf("accepted invalid handler config: %+v", test)
		}
	}
}

type handlerDecoder struct {
	envelope moduleapi.ChannelInboundEnvelopeV1
	err      error
	calls    int
}

func (decoder *handlerDecoder) DecodeInbound(
	context.Context,
	*http.Request,
) (moduleapi.ChannelInboundEnvelopeV1, error) {
	decoder.calls++
	return decoder.envelope, decoder.err
}

type handlerIngress struct {
	result IngressResult
	err    error
	calls  int
	input  IngressInput
}

func (ingress *handlerIngress) AdmitAndRun(
	_ context.Context,
	input IngressInput,
) (IngressResult, error) {
	ingress.calls++
	ingress.input = input
	return ingress.result, ingress.err
}

func handlerEnvelope() moduleapi.ChannelInboundEnvelopeV1 {
	return moduleapi.ChannelInboundEnvelopeV1{
		SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
		EndpointID:      "endpoint-handler",
		ProviderEventID: "event-handler",
		ExternalUserID:  "external-handler",
		Message:         "hello",
		ReplyTarget:     []byte(`{"conversation":"handler"}`),
		CursorBefore:    []byte(`{"offset":0}`),
		CursorAfter:     []byte(`{"offset":1}`),
	}
}
