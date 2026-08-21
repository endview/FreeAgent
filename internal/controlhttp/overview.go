package controlhttp

import (
	"net/http"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controloverview"
)

type overviewResponseV1 struct {
	SchemaVersion      string                                   `json:"schema_version"`
	PublishedPointer   controlapicontract.ExpectedResourceRefV1 `json:"published_pointer"`
	Basis              controlapicontract.PublishedBasisRefV1   `json:"basis"`
	View               controlapicontract.ControlViewSnapshotV1 `json:"view"`
	ViewSnapshotDigest string                                   `json:"view_snapshot_digest"`

	Workspaces                []controloverview.WorkspaceRefV1    `json:"workspaces"`
	WorkspacesTruncated       bool                                `json:"workspaces_truncated"`
	Runs                      []controloverview.RunV1             `json:"runs"`
	RunsTruncated             bool                                `json:"runs_truncated"`
	Unknown                   []controloverview.UnknownV1         `json:"unknown"`
	UnknownTruncated          bool                                `json:"unknown_truncated"`
	Learning                  []controloverview.LearningV1        `json:"learning"`
	LearningTruncated         bool                                `json:"learning_truncated"`
	ModuleCandidates          []controloverview.ModuleCandidateV1 `json:"module_candidates"`
	ModuleCandidatesTruncated bool                                `json:"module_candidates_truncated"`
	Usage                     []controloverview.UsageV1           `json:"usage"`
	UsageTruncated            bool                                `json:"usage_truncated"`
	ProjectionDigest          string                              `json:"projection_digest"`
}

func (handler *HandlerV1) serveOverviewV1(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if request.URL.RawQuery != "" || !emptyRequestBodyV1(request) ||
		!exactContentTypeV1(request.Header, false) ||
		hasUnknownFreeAgentAuthorityHeaderV1(request.Header) ||
		headerPresentV1(request.Header, ModuleInstanceIDEncodingHeaderV1) ||
		headerPresentV1(request.Header, "Authorization") ||
		headerPresentV1(request.Header, "Proxy-Authorization") ||
		headerPresentV1(request.Header, "Idempotency-Key") ||
		hasUnsupportedConditionalReadHeaderV1(request.Header) ||
		headerPresentV1(request.Header, ConfirmationHeaderV1) ||
		headerPresentV1(request.Header, OperationEvaluationDigestHeaderV1) {
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
	result, err := handler.overview.GetOverviewV1(
		request.Context(),
		controlapp.GetOverviewInputV1{
			Authorization: permit,
			Scope:         scope,
			ObservedAt:    observedAt,
		},
	)
	if err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	completedAt, ok := handler.observedAtV1()
	if !ok || completedAt < observedAt {
		handler.writeErrorV1(writer, controlapicontract.ErrorInternalV1, 0)
		return true
	}
	if err := controlapp.ValidateOverviewResultV1(
		permit, scope, completedAt, result,
	); err != nil {
		handler.writeMappedErrorV1(writer, err)
		return true
	}
	response := newOverviewResponseV1(result)
	encoded, err := marshalResponseV1(response)
	if err != nil {
		handler.writeErrorV1(writer, controlapicontract.ErrorResourceExhaustedV1, 0)
		return true
	}
	httpETag := responseStrongETagV1(
		"freeagent.control-http-overview-etag/v1",
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

func hasUnsupportedConditionalReadHeaderV1(header http.Header) bool {
	for _, name := range []string{
		"If-Match",
		"If-Modified-Since",
		"If-Range",
		"If-Unmodified-Since",
		"Range",
		"Content-Range",
	} {
		if headerPresentV1(header, name) {
			return true
		}
	}
	return false
}

func newOverviewResponseV1(result controlapp.OverviewResultV1) overviewResponseV1 {
	view := result.View
	view.Sections = append([]controlapicontract.ControlViewSectionV1{}, result.View.Sections...)
	usage := make([]controloverview.UsageV1, len(result.Usage))
	for index := range result.Usage {
		usage[index] = cloneOverviewHTTPUsageV1(result.Usage[index])
	}
	return overviewResponseV1{
		SchemaVersion:    OverviewHTTPResponseSchemaV1,
		PublishedPointer: result.PublishedPointer, Basis: result.Basis,
		View: view, ViewSnapshotDigest: result.ViewSnapshotDigest,
		Workspaces:          append([]controloverview.WorkspaceRefV1{}, result.Workspaces...),
		WorkspacesTruncated: result.WorkspacesTruncated,
		Runs:                append([]controloverview.RunV1{}, result.Runs...), RunsTruncated: result.RunsTruncated,
		Unknown: append([]controloverview.UnknownV1{}, result.Unknown...), UnknownTruncated: result.UnknownTruncated,
		Learning: append([]controloverview.LearningV1{}, result.Learning...), LearningTruncated: result.LearningTruncated,
		ModuleCandidates:          append([]controloverview.ModuleCandidateV1{}, result.ModuleCandidates...),
		ModuleCandidatesTruncated: result.ModuleCandidatesTruncated,
		Usage:                     usage, UsageTruncated: result.UsageTruncated,
		ProjectionDigest: result.ProjectionDigest,
	}
}

func cloneOverviewHTTPUsageV1(input controloverview.UsageV1) controloverview.UsageV1 {
	result := input
	for source, target := range map[*uint64]**uint64{
		input.InputTokens:         &result.InputTokens,
		input.CachedInputTokens:   &result.CachedInputTokens,
		input.UncachedInputTokens: &result.UncachedInputTokens,
		input.OutputTokens:        &result.OutputTokens,
		input.ReasoningTokens:     &result.ReasoningTokens,
	} {
		if source != nil {
			value := *source
			*target = &value
		}
	}
	return result
}
