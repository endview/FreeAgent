package channelservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const maxChannelHTTPPathBytes = 1024

// InboundDecoder is the narrow authenticated adapter surface exposed to the
// HTTP boundary. The concrete adapter remains responsible for exact method,
// path, loopback, authentication, media-type and body validation.
type InboundDecoder interface {
	DecodeInbound(
		context.Context,
		*http.Request,
	) (moduleapi.ChannelInboundEnvelopeV1, error)
}

// IngressUseCase is the single application path behind an enabled Channel
// handler. It must commit through Current Store and advance the Universal
// Loop; the handler owns no queue, Run, retry or background worker.
type IngressUseCase interface {
	AdmitAndRun(context.Context, IngressInput) (IngressResult, error)
}

// HTTPHandler binds one explicitly enabled Workspace Endpoint to one exact
// authenticated decoder. It is deliberately not constructed by Pure Chat.
type HTTPHandler struct {
	tenantID    string
	workspaceID string
	exactPath   string
	decoder     InboundDecoder
	ingress     IngressUseCase
}

// NewHTTPHandler performs no Store, secret or adapter call. The composition
// root must mount the returned handler only after the explicit process flag,
// current enabled Endpoint and exact adapter have all been verified.
func NewHTTPHandler(
	tenantID string,
	workspaceID string,
	exactPath string,
	decoder InboundDecoder,
	ingress IngressUseCase,
) (*HTTPHandler, error) {
	if !validHandlerOpaque(tenantID) || !validHandlerOpaque(workspaceID) ||
		!validExactHTTPPath(exactPath) || isNilHandlerDependency(decoder) ||
		isNilHandlerDependency(ingress) {
		return nil, errors.New("channelservice: invalid HTTP handler configuration")
	}
	return &HTTPHandler{
		tenantID: tenantID, workspaceID: workspaceID, exactPath: exactPath,
		decoder: decoder, ingress: ingress,
	}, nil
}

// Path returns the exact path that the trusted composition root must mount.
func (handler *HTTPHandler) Path() string {
	if handler == nil {
		return ""
	}
	return handler.exactPath
}

func (handler *HTTPHandler) ServeHTTP(
	response http.ResponseWriter,
	request *http.Request,
) {
	if handler == nil || response == nil || request == nil ||
		isNilHandlerDependency(handler.decoder) ||
		isNilHandlerDependency(handler.ingress) {
		writeChannelHTTPError(response, http.StatusInternalServerError)
		return
	}
	// Reject before invoking the decoder when a mux or caller routes another
	// path here. The concrete decoder repeats this check as the authority
	// boundary; this comparison is only a cheap fail-closed guard.
	if request.URL == nil || request.URL.Path != handler.exactPath {
		writeChannelHTTPError(response, http.StatusNotFound)
		return
	}
	envelope, err := handler.decoder.DecodeInbound(request.Context(), request)
	if err != nil {
		// Authentication and wire errors intentionally collapse to one response
		// so resolver diagnostics and credential validity cannot escape.
		writeChannelHTTPError(response, http.StatusBadRequest)
		return
	}
	result, err := handler.ingress.AdmitAndRun(
		request.Context(),
		IngressInput{
			TenantID: handler.tenantID, WorkspaceID: handler.workspaceID,
			Envelope: envelope,
		},
	)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, ErrInvalidIngress):
			status = http.StatusBadRequest
		case errors.Is(err, ErrIngressDenied):
			status = http.StatusForbidden
		case errors.Is(err, currentstore.ErrChannelIngressConflict):
			status = http.StatusConflict
		}
		writeChannelHTTPError(response, status)
		return
	}
	status := "accepted"
	if result.Receipt.Disposition == currentstore.ChannelIngressRejected {
		status = "rejected"
	} else if result.Duplicate {
		status = "duplicate"
	}
	writeChannelHTTPJSON(response, http.StatusOK, channelHTTPResponse{
		SchemaVersion: "channel-ingress-response/v1",
		Status:        status,
		IngressKey:    result.IngressKey,
		RunID:         result.RunID,
	})
}

type channelHTTPResponse struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"`
	IngressKey    string `json:"ingress_key,omitempty"`
	RunID         string `json:"run_id,omitempty"`
}

func writeChannelHTTPError(response http.ResponseWriter, status int) {
	writeChannelHTTPJSON(response, status, channelHTTPResponse{
		SchemaVersion: "channel-ingress-response/v1",
		Status:        "rejected",
	})
}

func writeChannelHTTPJSON(
	response http.ResponseWriter,
	status int,
	payload channelHTTPResponse,
) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}

func validExactHTTPPath(value string) bool {
	return value != "" && value[0] == '/' && len(value) <= maxChannelHTTPPathBytes &&
		value == strings.TrimSpace(value) && !strings.ContainsAny(value, "?#\\")
}

func validHandlerOpaque(value string) bool {
	return value != "" && len(value) <= moduleapi.MaxOpaqueIDBytes &&
		value == strings.TrimSpace(value) && value == moduleapi.CanonicalText(value)
}

func isNilHandlerDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
