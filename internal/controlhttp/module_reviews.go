package controlhttp

import (
	"net/http"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func (handler *HandlerV1) serveModuleUpgradeReviewListV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if !exactContentTypeV1(request.Header, false) || !emptyRequestBodyV1(request) ||
		hasUnknownFreeAgentAuthorityHeaderV1(request.Header) {
		handler.writeErrorV1(writer, controlapicontract.ErrorInvalidRequestV1, 0)
		return true
	}
	limit, cursor, err := parseListQueryV1(request.URL.RawQuery)
	if err != nil || cursor != "" {
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
	result, err := handler.moduleUpgradeReviews.ListModuleUpgradeReviewsV1(
		request.Context(), controlapp.ModuleUpgradeReviewListInputV1{
			Authorization: permit, Scope: scope, Limit: limit, ObservedAt: observedAt,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	if result.SchemaVersion != controlapp.ModuleUpgradeReviewListSchemaVersionV1 ||
		result.Scope != scope || !validStrongETagV1(result.StrongETag) ||
		!moduleapi.ValidSHA256(result.ProjectionDigest) {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	encoded, err := marshalResponseV1(result)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	httpETag := responseStrongETagV1(
		"freeagent.control-http-module-upgrade-review-list-etag/v1",
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

func (handler *HandlerV1) serveModuleUpgradeReviewDetailV1(
	writer http.ResponseWriter,
	request *http.Request,
	reviewID string,
) bool {
	if request.URL.RawQuery != "" || !exactContentTypeV1(request.Header, false) ||
		!emptyRequestBodyV1(request) || hasUnknownFreeAgentAuthorityHeaderV1(request.Header) {
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
	result, err := handler.moduleUpgradeReviews.GetModuleUpgradeReviewV1(
		request.Context(), controlapp.GetModuleUpgradeReviewInputV1{
			Authorization: permit, Scope: scope, ReviewID: reviewID, ObservedAt: observedAt,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	if result.SchemaVersion != controlapp.ModuleUpgradeReviewDetailSchemaVersionV1 ||
		result.Scope != scope || result.ReviewID != reviewID ||
		result.Review.ArtifactAdmissionID == "" ||
		!validStrongETagV1(result.StrongETag) ||
		!moduleapi.ValidSHA256(result.ProjectionDigest) {
		handler.writeErrorV1(writer, controlapicontract.ErrorIntegrityFailureV1, 0)
		return true
	}
	encoded, err := marshalResponseV1(result)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	httpETag := responseStrongETagV1(
		"freeagent.control-http-module-upgrade-review-detail-etag/v1",
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
