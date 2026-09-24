package controlhttp

import (
	"net/http"
	"strings"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func (handler *HandlerV1) serveUnknownOutcomeListV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	limit, cursor, err := parseListQueryV1(request.URL.RawQuery)
	if err != nil || cursor != "" ||
		!exactContentTypeV1(request.Header, false) ||
		!emptyRequestBodyV1(request) ||
		hasUnknownFreeAgentAuthorityHeaderV1(request.Header) ||
		hasUnsupportedConditionalReadHeaderV1(request.Header) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	if _, _, err := exactStrongIfNoneMatchV1(request.Header); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	scope, err := scopeFromHeadersV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	permit, err := handler.admitReadV1(request, scope)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer permit.Release()
	observedAt, ok := handler.observedAtV1()
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		return true
	}
	result, err := handler.management.ListUnknownOutcomesV1(
		request.Context(),
		controlapp.UnknownOutcomeListInputV1{
			Authorization: permit, Scope: scope, Limit: limit, ObservedAt: observedAt,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	if result.SchemaVersion != controlapp.UnknownOutcomeListSchemaVersionV1 ||
		result.Scope != scope || !moduleapi.ValidSHA256(result.ProjectionDigest) ||
		!validStrongETagV1(result.StrongETag) {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	encoded, err := marshalResponseV1(result)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	httpETag := responseStrongETagV1(
		"freeagent.control-http-unknown-outcome-list-etag/v1",
		result.StrongETag,
		encoded,
	)
	writer.Header().Set("ETag", httpETag)
	if notModified, err := matchesIfNoneMatchV1(request.Header, httpETag); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	} else if notModified {
		writer.WriteHeader(http.StatusNotModified)
		return true
	}
	writeEncodedJSONV1(writer, http.StatusOK, encoded)
	return true
}

func (handler *HandlerV1) serveUnknownOutcomeDetailV1(
	writer http.ResponseWriter,
	request *http.Request,
	resource string,
) bool {
	if request.URL.RawQuery != "" ||
		!exactContentTypeV1(request.Header, false) ||
		!emptyRequestBodyV1(request) ||
		hasUnknownFreeAgentAuthorityHeaderV1(request.Header) ||
		hasUnsupportedConditionalReadHeaderV1(request.Header) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	if _, _, err := exactStrongIfNoneMatchV1(request.Header); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	parts := strings.Split(resource, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
		strings.ContainsAny(parts[0], "/") || strings.ContainsAny(parts[1], "/") ||
		!validUnknownAttemptIDV1(parts[1]) {
		handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
		return true
	}
	kind := controlapp.UnknownAttemptKindV1(parts[0])
	if kind != controlapp.UnknownAttemptModelV1 &&
		kind != controlapp.UnknownAttemptActionV1 &&
		kind != controlapp.UnknownAttemptChannelV1 {
		handler.writeErrorV1(writer, controlapicontract.ErrorNotFoundV1, 0)
		return true
	}
	scope, err := scopeFromHeadersV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	permit, err := handler.admitReadV1(request, scope)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer permit.Release()
	observedAt, ok := handler.observedAtV1()
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		return true
	}
	result, err := handler.management.GetUnknownOutcomeV1(
		request.Context(),
		controlapp.UnknownOutcomeDetailInputV1{
			Authorization: permit, Scope: scope, Kind: kind,
			AttemptID: parts[1], ObservedAt: observedAt,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	if result.SchemaVersion != controlapp.UnknownOutcomeDetailSchemaVersionV1 ||
		result.Scope != scope || result.Item.Kind != kind ||
		result.Item.AttemptID != parts[1] ||
		!moduleapi.ValidSHA256(result.ProjectionDigest) ||
		!validStrongETagV1(result.StrongETag) {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	encoded, err := marshalResponseV1(result)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	httpETag := responseStrongETagV1(
		"freeagent.control-http-unknown-outcome-detail-etag/v1",
		result.StrongETag,
		encoded,
	)
	writer.Header().Set("ETag", httpETag)
	if notModified, err := matchesIfNoneMatchV1(request.Header, httpETag); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	} else if notModified {
		writer.WriteHeader(http.StatusNotModified)
		return true
	}
	writeEncodedJSONV1(writer, http.StatusOK, encoded)
	return true
}

func validUnknownAttemptIDV1(value string) bool {
	return len(value) <= moduleapi.MaxOpaqueIDBytes && rawURLTokenV1(value)
}

func (handler *HandlerV1) serveStoreManagementV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	limit, cursor, err := parseListQueryV1(request.URL.RawQuery)
	if err != nil || cursor != "" ||
		!exactContentTypeV1(request.Header, false) ||
		!emptyRequestBodyV1(request) ||
		hasUnknownFreeAgentAuthorityHeaderV1(request.Header) ||
		hasUnsupportedConditionalReadHeaderV1(request.Header) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	if _, _, err := exactStrongIfNoneMatchV1(request.Header); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	scope, err := scopeFromHeadersV1(request.Header)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	permit, err := handler.admitReadV1(request, scope)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	defer permit.Release()
	observedAt, ok := handler.observedAtV1()
	if !ok {
		handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		return true
	}
	result, err := handler.management.GetStoreManagementV1(
		request.Context(),
		controlapp.StoreManagementInputV1{
			Authorization: permit, Scope: scope, Limit: limit, ObservedAt: observedAt,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	if result.SchemaVersion != controlapp.StoreManagementSchemaVersionV1 ||
		result.Scope != scope || !moduleapi.ValidSHA256(result.ProjectionDigest) ||
		!validStrongETagV1(result.StrongETag) {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	encoded, err := marshalResponseV1(result)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	httpETag := responseStrongETagV1(
		"freeagent.control-http-store-management-etag/v1",
		result.StrongETag,
		encoded,
	)
	writer.Header().Set("ETag", httpETag)
	if notModified, err := matchesIfNoneMatchV1(request.Header, httpETag); err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	} else if notModified {
		writer.WriteHeader(http.StatusNotModified)
		return true
	}
	writeEncodedJSONV1(writer, http.StatusOK, encoded)
	return true
}
