package controlhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlsession"
)

type bootstrapExchangeRequestV1 struct {
	SchemaVersion string `json:"schema_version"`
	Capability    string `json:"capability"`
}

type bootstrapExchangeResponseV2 struct {
	SchemaVersion    string                              `json:"schema_version"`
	Session          controlapicontract.ControlSessionV1 `json:"session"`
	AuthorizedScopes []controlapicontract.ControlScopeV1 `json:"authorized_scopes"`
	CSRFToken        string                              `json:"csrf_token"`
	ResumeCredential string                              `json:"resume_credential"`
}

type sessionResumeRequestV1 struct {
	SchemaVersion    string `json:"schema_version"`
	ResumeCredential string `json:"resume_credential"`
}

type sessionResumeResponseV1 struct {
	SchemaVersion    string                              `json:"schema_version"`
	Session          controlapicontract.ControlSessionV1 `json:"session"`
	AuthorizedScopes []controlapicontract.ControlScopeV1 `json:"authorized_scopes"`
	CSRFToken        string                              `json:"csrf_token"`
	ResumeCredential string                              `json:"resume_credential"`
}

type modulesPageResponseV1 struct {
	SchemaVersion      string                                   `json:"schema_version"`
	PublishedPointer   controlapicontract.ExpectedResourceRefV1 `json:"published_pointer"`
	Basis              controlapicontract.PublishedBasisRefV1   `json:"basis"`
	View               controlapicontract.ControlViewSnapshotV1 `json:"view"`
	ViewSnapshotDigest string                                   `json:"view_snapshot_digest"`
	SourceRevision     uint64                                   `json:"source_revision"`
	SourceDigest       string                                   `json:"source_digest"`
	SortVersion        string                                   `json:"sort_version"`
	FilterDigest       string                                   `json:"filter_digest"`
	Items              []controlapp.ModuleSummaryV1             `json:"items"`
	HasMore            bool                                     `json:"has_more"`
	NextCursor         string                                   `json:"next_cursor,omitempty"`
	ProjectionDigest   string                                   `json:"projection_digest"`
}

func newModulesPageResponseV1(
	page controlapp.ModulesPageV1,
	nextCursor string,
) modulesPageResponseV1 {
	items := make([]controlapp.ModuleSummaryV1, len(page.Items))
	for index := range page.Items {
		items[index] = page.Items[index]
		items[index].Provides = append(
			items[index].Provides[:0:0],
			page.Items[index].Provides...,
		)
	}
	view := page.View
	view.Sections = append(view.Sections[:0:0], page.View.Sections...)
	return modulesPageResponseV1{
		SchemaVersion:      ModulesHTTPPageSchemaV1,
		PublishedPointer:   page.PublishedPointer,
		Basis:              page.Basis,
		View:               view,
		ViewSnapshotDigest: page.ViewSnapshotDigest,
		SourceRevision:     page.SourceRevision,
		SourceDigest:       page.SourceDigest,
		SortVersion:        page.SortVersion,
		FilterDigest:       page.FilterDigest,
		Items:              items,
		HasMore:            page.HasMore,
		NextCursor:         nextCursor,
		ProjectionDigest:   page.ProjectionDigest,
	}
}

type errorResponseV1 struct {
	SchemaVersion     string                         `json:"schema_version"`
	Code              controlapicontract.ErrorCodeV1 `json:"code"`
	CorrelationID     string                         `json:"correlation_id"`
	Message           string                         `json:"message"`
	RetryAfterSeconds uint32                         `json:"retry_after_seconds,omitempty"`
}

func marshalResponseV1(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || len(encoded) > MaximumResponseBodyBytesV1 {
		return nil, errors.New("controlhttp: response exceeds transport bounds")
	}
	return encoded, nil
}

func writeEncodedJSONV1(writer http.ResponseWriter, status int, encoded []byte) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(encoded)
}

func (handler *HandlerV1) writeMappedErrorV1(
	writer http.ResponseWriter,
	err error,
) {
	code := controlapicontract.ErrorInternalV1
	switch {
	case errors.Is(err, controlsession.ErrUnauthenticated):
		code = controlapicontract.ErrorUnauthenticatedV1
	case errors.Is(err, controlsession.ErrSessionExpired),
		errors.Is(err, controlapp.ErrSessionExpired):
		code = controlapicontract.ErrorSessionExpiredV1
	case errors.Is(err, controlsession.ErrForbidden),
		errors.Is(err, controlapp.ErrForbidden),
		errors.Is(err, ErrModuleDisableConfirmationRejectedV1):
		code = controlapicontract.ErrorForbiddenV1
	case errors.Is(err, controlsession.ErrResourceExhausted),
		errors.Is(err, controlapp.ErrResourceExhausted):
		code = controlapicontract.ErrorResourceExhaustedV1
	case errors.Is(err, controlapp.ErrInvalidRequest):
		code = controlapicontract.ErrorInvalidRequestV1
	case errors.Is(err, controlapp.ErrPreconditionRequired):
		code = controlapicontract.ErrorPreconditionRequiredV1
	case errors.Is(err, controlapp.ErrRevisionConflict):
		code = controlapicontract.ErrorRevisionConflictV1
	case errors.Is(err, ErrModuleDisableIdempotencyConflictV1):
		code = controlapicontract.ErrorIdempotencyConflictV1
	case errors.Is(err, controlapp.ErrConflict):
		code = controlapicontract.ErrorConflictV1
	case errors.Is(err, controlapp.ErrNotFound):
		code = controlapicontract.ErrorNotFoundV1
	case errors.Is(err, controlapp.ErrCursorInvalid):
		code = controlapicontract.ErrorCursorInvalidV1
	case errors.Is(err, controlapp.ErrCursorStale):
		code = controlapicontract.ErrorCursorStaleV1
	case errors.Is(err, controlapp.ErrStoreBusy):
		code = controlapicontract.ErrorStoreBusyV1
	case errors.Is(err, controlapp.ErrStoreUnavailable),
		errors.Is(err, controlsession.ErrClosed):
		code = controlapicontract.ErrorStoreUnavailableV1
	case errors.Is(err, controlapp.ErrIntegrityFailure):
		code = controlapicontract.ErrorIntegrityFailureV1
	case errors.Is(err, controlapp.ErrCancelled),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		code = controlapicontract.ErrorCancelledV1
	}
	handler.writeErrorV1(writer, code, 0)
}

func (handler *HandlerV1) writeErrorV1(
	writer http.ResponseWriter,
	code controlapicontract.ErrorCodeV1,
	retryAfter uint32,
) {
	if code == controlapicontract.ErrorNoneV1 || code.Validate() != nil {
		code = controlapicontract.ErrorInternalV1
	}
	status, defaultRetry := errorHTTPStatusV1(code)
	if retryAfter == 0 {
		retryAfter = defaultRetry
	}
	response := errorResponseV1{
		SchemaVersion:     "control-error/v1",
		Code:              code,
		CorrelationID:     handler.nextCorrelationIDV1(),
		Message:           errorMessageV1(code),
		RetryAfterSeconds: retryAfter,
	}
	encoded, err := marshalResponseV1(response)
	if err != nil {
		writeStaticInternalErrorV1(writer)
		return
	}
	writer.Header().Del("ETag")
	writer.Header().Del("Set-Cookie")
	if retryAfter != 0 {
		writer.Header().Set("Retry-After", strconv.FormatUint(uint64(retryAfter), 10))
	} else {
		writer.Header().Del("Retry-After")
	}
	writeEncodedJSONV1(writer, status, encoded)
}

func writeStaticInternalErrorV1(writer http.ResponseWriter) {
	setSecurityHeadersV1(writer.Header())
	writer.Header().Del("ETag")
	writer.Header().Del("Set-Cookie")
	writeEncodedJSONV1(
		writer,
		http.StatusInternalServerError,
		[]byte(`{"schema_version":"control-error/v1","code":"INTERNAL_ERROR","correlation_id":"control-unavailable","message":"the control service could not complete the request"}`),
	)
}

func errorHTTPStatusV1(code controlapicontract.ErrorCodeV1) (int, uint32) {
	switch code {
	case controlapicontract.ErrorInvalidRequestV1,
		controlapicontract.ErrorCursorInvalidV1:
		return http.StatusBadRequest, 0
	case controlapicontract.ErrorUnauthenticatedV1,
		controlapicontract.ErrorSessionExpiredV1:
		return http.StatusUnauthorized, 0
	case controlapicontract.ErrorForbiddenV1:
		return http.StatusForbidden, 0
	case controlapicontract.ErrorNotFoundV1:
		return http.StatusNotFound, 0
	case controlapicontract.ErrorConflictV1,
		controlapicontract.ErrorRevisionConflictV1,
		controlapicontract.ErrorIdempotencyConflictV1,
		controlapicontract.ErrorCursorStaleV1,
		controlapicontract.ErrorOutcomeUnknownV1:
		return http.StatusConflict, 0
	case controlapicontract.ErrorPreconditionRequiredV1:
		return http.StatusPreconditionRequired, 0
	case controlapicontract.ErrorResourceExhaustedV1:
		return http.StatusTooManyRequests, 1
	case controlapicontract.ErrorStoreBusyV1,
		controlapicontract.ErrorStoreUnavailableV1:
		return http.StatusServiceUnavailable, 1
	case controlapicontract.ErrorCancelledV1:
		return http.StatusRequestTimeout, 0
	case controlapicontract.ErrorIntegrityFailureV1,
		controlapicontract.ErrorInternalV1:
		return http.StatusInternalServerError, 0
	default:
		return http.StatusInternalServerError, 0
	}
}

func errorMessageV1(code controlapicontract.ErrorCodeV1) string {
	switch code {
	case controlapicontract.ErrorInvalidRequestV1:
		return "the request is invalid"
	case controlapicontract.ErrorUnauthenticatedV1:
		return "authentication is required"
	case controlapicontract.ErrorSessionExpiredV1:
		return "the control session has expired"
	case controlapicontract.ErrorForbiddenV1:
		return "the request is not authorized"
	case controlapicontract.ErrorNotFoundV1:
		return "the requested resource was not found"
	case controlapicontract.ErrorConflictV1:
		return "the request conflicts with current state"
	case controlapicontract.ErrorPreconditionRequiredV1:
		return "an exact precondition is required"
	case controlapicontract.ErrorRevisionConflictV1:
		return "the exact resource basis is stale"
	case controlapicontract.ErrorIdempotencyConflictV1:
		return "the idempotency identity conflicts with an earlier request"
	case controlapicontract.ErrorCursorInvalidV1:
		return "the page cursor is invalid"
	case controlapicontract.ErrorCursorStaleV1:
		return "the page cursor is stale; fetch the collection again"
	case controlapicontract.ErrorResourceExhaustedV1:
		return "a control resource limit was reached"
	case controlapicontract.ErrorStoreBusyV1:
		return "the current store is busy"
	case controlapicontract.ErrorStoreUnavailableV1:
		return "the current store is unavailable"
	case controlapicontract.ErrorIntegrityFailureV1:
		return "control data failed integrity verification"
	case controlapicontract.ErrorOutcomeUnknownV1:
		return "the original operation outcome is unknown and cannot be replayed"
	case controlapicontract.ErrorCancelledV1:
		return "the request was cancelled before completion"
	default:
		return "the control service could not complete the request"
	}
}
