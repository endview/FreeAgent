package controlapp

import (
	"context"
	"errors"
	"math"
	"reflect"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/controloverview"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	OverviewSchemaVersionV1  = "control-overview/v1"
	OverviewItemLimitV1      = uint16(12)
	OverviewWorkspaceLimitV1 = uint16(256)

	overviewSectionSourceDomainV1 = "freeagent.control-overview-section-source/v1"
	overviewProjectionDomainV1    = "freeagent.control-overview/v1"
	overviewETagDomainV1          = "freeagent.control-overview-etag/v1"

	// Keep detached transport validation independent of the runtime domain.
	overviewRunStateAdmittedV1              = "ADMITTED"
	overviewRunStateWaitingReconciliationV1 = "WAITING_RECONCILIATION"
	overviewRunStateTerminatedV1            = "TERMINATED"
)

// OverviewReaderV1 is the complete Current Store dependency of the W6-3
// read-only Overview. Implementations must use one read transaction on the
// already-open Store and return detached, bounded safe facts.
type OverviewReaderV1 interface {
	LoadControlOverviewV1(
		context.Context,
		string,
		string,
		uint16,
	) (controloverview.SnapshotV1, error)
}

type GetOverviewInputV1 struct {
	Authorization AuthorizationContextV1
	Scope         controlapicontract.ControlScopeV1
	ObservedAt    uint64
}

type OverviewResultV1 struct {
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

	ProjectionDigest string `json:"projection_digest"`
	StrongETag       string `json:"strong_etag"`

	// basisSourceUpdatedAtUnixMicros is deliberately package-private. It lets
	// the detached verifier reproduce the Store's exact source clock without
	// adding an unchecked field to the wire contract.
	basisSourceUpdatedAtUnixMicros uint64
}

type OverviewServiceV1 struct {
	reader OverviewReaderV1
}

func NewOverviewServiceV1(reader OverviewReaderV1) (*OverviewServiceV1, error) {
	if nilInterfaceV1(reader) {
		return nil, ErrInvalidRequest
	}
	return &OverviewServiceV1{reader: reader}, nil
}

// GetOverviewV1 returns one bounded current-scope projection. Authorization
// is repeated before Store access, for every projected resource, and directly
// before return.
func (service *OverviewServiceV1) GetOverviewV1(
	ctx context.Context,
	input GetOverviewInputV1,
) (OverviewResultV1, error) {
	if service == nil || nilInterfaceV1(service.reader) || ctx == nil {
		return OverviewResultV1{}, ErrInvalidRequest
	}
	authorized, err := authorizeModulesRequestV1(
		input.Authorization, input.Scope, input.ObservedAt,
	)
	if err != nil {
		return OverviewResultV1{}, err
	}
	if err := reauthorizeModulesRequestV1(input.Authorization, authorized); err != nil {
		return OverviewResultV1{}, err
	}
	workspaceID := ""
	if authorized.scope.Kind == controlapicontract.ScopeWorkspaceV1 {
		workspaceID = authorized.scope.WorkspaceID
	}
	snapshot, err := service.reader.LoadControlOverviewV1(
		ctx, authorized.scope.TenantID, workspaceID, OverviewItemLimitV1,
	)
	if err != nil {
		return OverviewResultV1{}, classifyOverviewReaderErrorV1(err)
	}
	if err := validateAndReauthorizeOverviewV1(
		input.Authorization, authorized, snapshot,
	); err != nil {
		return OverviewResultV1{}, err
	}
	basis := PublishedBasisRefV1(snapshot.Basis)
	if err := basis.Validate(); err != nil || basis.TenantID != authorized.scope.TenantID {
		return OverviewResultV1{}, ErrIntegrityFailure
	}
	pointer, err := PublishedPointerRefV1(basis)
	if err != nil {
		return OverviewResultV1{}, err
	}
	sections, err := overviewSectionsV1(authorized, basis.PointerRevision, snapshot)
	if err != nil {
		return OverviewResultV1{}, err
	}
	view, _, viewDigest, err := controlapicontract.NewControlViewSnapshotV1(
		controlapicontract.ControlViewSnapshotV1{
			SchemaVersion: controlapicontract.ControlViewSnapshotSchemaVersionV1,
			Scope:         authorized.scope, ScopeDigest: authorized.scopeDigest,
			ObservedAtUnixMicros: snapshot.SourceUpdatedAtUnixMicros,
			Basis:                basis, Sections: sections,
		},
	)
	if err != nil {
		return OverviewResultV1{}, ErrIntegrityFailure
	}
	result := OverviewResultV1{
		SchemaVersion: OverviewSchemaVersionV1, PublishedPointer: pointer, Basis: basis,
		View: view, ViewSnapshotDigest: viewDigest,
		Workspaces:          cloneOverviewWorkspacesV1(snapshot.Workspaces),
		WorkspacesTruncated: snapshot.WorkspacesTruncated,
		Runs:                cloneOverviewRunsV1(snapshot.Runs), RunsTruncated: snapshot.RunsTruncated,
		Unknown: cloneOverviewUnknownV1(snapshot.Unknown), UnknownTruncated: snapshot.UnknownTruncated,
		Learning: cloneOverviewLearningV1(snapshot.Learning), LearningTruncated: snapshot.LearningTruncated,
		ModuleCandidates:          cloneOverviewCandidatesV1(snapshot.ModuleCandidates),
		ModuleCandidatesTruncated: snapshot.ModuleCandidatesTruncated,
		Usage:                     cloneOverviewUsageV1(snapshot.Usage), UsageTruncated: snapshot.UsageTruncated,
		basisSourceUpdatedAtUnixMicros: snapshot.BasisSourceUpdatedAtUnixMicros,
	}
	result.ProjectionDigest, err = digestOverviewProjectionV1(result)
	if err != nil {
		return OverviewResultV1{}, ErrResourceExhausted
	}
	result.StrongETag, err = digestStrongETagV1(
		overviewETagDomainV1, authorized, result.ProjectionDigest,
	)
	if err != nil {
		return OverviewResultV1{}, ErrIntegrityFailure
	}
	if err := validateOverviewResultV1(
		input.Authorization, input.Scope, input.ObservedAt, result, false,
	); err != nil {
		return OverviewResultV1{}, err
	}
	return cloneOverviewResultV1(result), nil
}

// ValidateOverviewResultV1 is the sole application verifier for a detached
// Overview result. Transports call it with the same live Permit immediately
// before encoding, so a substituted or drifting OverviewService cannot turn
// unchecked DTOs, section metadata, digests, or ETags into an authorized wire.
func ValidateOverviewResultV1(
	authorization AuthorizationContextV1,
	scope controlapicontract.ControlScopeV1,
	observedAt uint64,
	result OverviewResultV1,
) error {
	return validateOverviewResultV1(
		authorization, scope, observedAt, result, true,
	)
}

func validateOverviewResultV1(
	authorization AuthorizationContextV1,
	scope controlapicontract.ControlScopeV1,
	observedAt uint64,
	result OverviewResultV1,
	enforceSourceTimeUpperBound bool,
) error {
	request, err := authorizeModulesRequestV1(authorization, scope, observedAt)
	if err != nil {
		return err
	}
	if result.SchemaVersion != OverviewSchemaVersionV1 ||
		result.Basis.TenantID != request.scope.TenantID ||
		result.View.ObservedAtUnixMicros == 0 ||
		(enforceSourceTimeUpperBound &&
			result.View.ObservedAtUnixMicros > request.observedAt) {
		return ErrIntegrityFailure
	}
	if err := result.Basis.Validate(); err != nil {
		return ErrIntegrityFailure
	}
	basis := controlcontract.PublishedBasis{
		TenantID: result.Basis.TenantID, PointerRevision: result.Basis.PointerRevision,
		Control: controlcontract.ControlSnapshotRef{
			SnapshotID: result.Basis.Control.ID, Revision: result.Basis.Control.Revision,
			Digest: result.Basis.Control.Digest,
		},
		Catalog: controlcontract.CatalogGenerationRef{
			GenerationID: result.Basis.Catalog.ID, Generation: result.Basis.Catalog.Revision,
			Digest: result.Basis.Catalog.Digest,
		},
	}
	snapshot := controloverview.SnapshotV1{
		Basis:                          basis,
		BasisSourceUpdatedAtUnixMicros: result.basisSourceUpdatedAtUnixMicros,
		SourceUpdatedAtUnixMicros:      result.View.ObservedAtUnixMicros,
		Workspaces:                     cloneOverviewWorkspacesV1(result.Workspaces),
		WorkspacesTruncated:            result.WorkspacesTruncated,
		Runs:                           cloneOverviewRunsV1(result.Runs), RunsTruncated: result.RunsTruncated,
		Unknown: cloneOverviewUnknownV1(result.Unknown), UnknownTruncated: result.UnknownTruncated,
		Learning: cloneOverviewLearningV1(result.Learning), LearningTruncated: result.LearningTruncated,
		ModuleCandidates:          cloneOverviewCandidatesV1(result.ModuleCandidates),
		ModuleCandidatesTruncated: result.ModuleCandidatesTruncated,
		Usage:                     cloneOverviewUsageV1(result.Usage), UsageTruncated: result.UsageTruncated,
	}
	if err := validateAndReauthorizeOverviewV1(authorization, request, snapshot); err != nil {
		return err
	}
	expectedPointer, err := PublishedPointerRefV1(result.Basis)
	if err != nil || expectedPointer != result.PublishedPointer {
		return ErrIntegrityFailure
	}
	sections, err := overviewSectionsV1(request, result.Basis.PointerRevision, snapshot)
	if err != nil {
		return err
	}
	expectedView, _, expectedViewDigest, err := controlapicontract.NewControlViewSnapshotV1(
		controlapicontract.ControlViewSnapshotV1{
			SchemaVersion: controlapicontract.ControlViewSnapshotSchemaVersionV1,
			Scope:         request.scope, ScopeDigest: request.scopeDigest,
			ObservedAtUnixMicros: snapshot.SourceUpdatedAtUnixMicros,
			Basis:                result.Basis, Sections: sections,
		},
	)
	if err != nil || !reflect.DeepEqual(expectedView, result.View) ||
		expectedViewDigest != result.ViewSnapshotDigest {
		return ErrIntegrityFailure
	}
	expectedProjection, err := digestOverviewProjectionV1(result)
	if err != nil {
		return ErrResourceExhausted
	}
	if expectedProjection != result.ProjectionDigest {
		return ErrIntegrityFailure
	}
	expectedETag, err := digestStrongETagV1(
		overviewETagDomainV1, request, expectedProjection,
	)
	if err != nil || expectedETag != result.StrongETag {
		return ErrIntegrityFailure
	}
	return reauthorizeModulesRequestV1(authorization, request)
}

func classifyOverviewReaderErrorV1(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ErrCancelled
	case errors.Is(err, controloverview.ErrInvalidRequest):
		return ErrInvalidRequest
	case errors.Is(err, controloverview.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, controloverview.ErrIntegrity):
		return ErrIntegrityFailure
	default:
		return classifyReaderErrorV1(err, ErrStoreUnavailable)
	}
}

func validateAndReauthorizeOverviewV1(
	authorization AuthorizationContextV1,
	request authorizedRequestV1,
	snapshot controloverview.SnapshotV1,
) error {
	if snapshot.Basis.TenantID != request.scope.TenantID ||
		!validPositiveJSONIntegerV1(snapshot.BasisSourceUpdatedAtUnixMicros) ||
		!validPositiveJSONIntegerV1(snapshot.SourceUpdatedAtUnixMicros) ||
		snapshot.Workspaces == nil || snapshot.Runs == nil || snapshot.Unknown == nil ||
		snapshot.Learning == nil || snapshot.ModuleCandidates == nil || snapshot.Usage == nil ||
		len(snapshot.Workspaces) > int(OverviewWorkspaceLimitV1) || snapshot.WorkspacesTruncated ||
		len(snapshot.Runs) > int(OverviewItemLimitV1) ||
		len(snapshot.Unknown) > int(OverviewItemLimitV1) ||
		len(snapshot.Learning) > int(OverviewItemLimitV1) ||
		len(snapshot.ModuleCandidates) > int(OverviewItemLimitV1) ||
		len(snapshot.Usage) > int(OverviewItemLimitV1) ||
		(snapshot.RunsTruncated && len(snapshot.Runs) != int(OverviewItemLimitV1)) ||
		(snapshot.UnknownTruncated && len(snapshot.Unknown) != int(OverviewItemLimitV1)) ||
		(snapshot.LearningTruncated && len(snapshot.Learning) != int(OverviewItemLimitV1)) ||
		(snapshot.ModuleCandidatesTruncated && len(snapshot.ModuleCandidates) != int(OverviewItemLimitV1)) ||
		(snapshot.UsageTruncated && len(snapshot.Usage) != int(OverviewItemLimitV1)) {
		return ErrIntegrityFailure
	}
	if err := snapshot.Basis.Validate(); err != nil {
		return ErrIntegrityFailure
	}
	if snapshot.SourceUpdatedAtUnixMicros != exactOverviewSourceUpdatedAtV1(snapshot) {
		return ErrIntegrityFailure
	}
	seenRuns := make(map[string]controloverview.RunV1, len(snapshot.Runs))
	seenUnknown := make(map[struct{ kind, resourceID string }]struct{}, len(snapshot.Unknown))
	seenLearning := make(map[string]struct{}, len(snapshot.Learning))
	seenModuleReviews := make(map[string]struct{}, len(snapshot.ModuleCandidates))
	seenUsage := make(map[string]struct{}, len(snapshot.Usage))
	modelUnknown := make(map[string]controloverview.UnknownV1)
	proposalUnknown := make(map[string]controloverview.UnknownV1)
	for index, workspace := range snapshot.Workspaces {
		if err := reauthorizeModulesRequestV1(authorization, request); err != nil {
			return err
		}
		if err := workspace.Validate(); err != nil ||
			(request.scope.Kind == controlapicontract.ScopeWorkspaceV1 && workspace.ID != request.scope.WorkspaceID) ||
			(index > 0 && snapshot.Workspaces[index-1].ID >= workspace.ID) {
			return ErrIntegrityFailure
		}
	}
	for index, item := range snapshot.Runs {
		if err := reauthorizeModulesRequestV1(authorization, request); err != nil {
			return err
		}
		if _, duplicate := seenRuns[item.RunID]; duplicate {
			return ErrIntegrityFailure
		}
		seenRuns[item.RunID] = item
		if !validOverviewOwnershipV1(request.scope, item.TenantID, item.WorkspaceID) ||
			!validOpaqueIDV1(item.RunID) ||
			!validOverviewRunProjectionV1(item.State, item.Disposition, item.Revision) ||
			!validPositiveJSONIntegerV1(item.CreatedAtUnixMicros) ||
			!validPositiveJSONIntegerV1(item.UpdatedAtUnixMicros) ||
			item.UpdatedAtUnixMicros < item.CreatedAtUnixMicros ||
			item.UpdatedAtUnixMicros > snapshot.SourceUpdatedAtUnixMicros ||
			(index > 0 && !overviewOrderV1(snapshot.Runs[index-1].UpdatedAtUnixMicros,
				snapshot.Runs[index-1].RunID, item.UpdatedAtUnixMicros, item.RunID)) {
			return ErrIntegrityFailure
		}
	}
	for index, item := range snapshot.Unknown {
		if err := reauthorizeModulesRequestV1(authorization, request); err != nil {
			return err
		}
		identity := struct{ kind, resourceID string }{item.Kind, item.ResourceID}
		if _, duplicate := seenUnknown[identity]; duplicate {
			return ErrIntegrityFailure
		}
		seenUnknown[identity] = struct{}{}
		if !validOverviewOwnershipV1(request.scope, item.TenantID, item.WorkspaceID) ||
			!validOverviewUnknownKindV1(item.Kind) || !validOpaqueIDV1(item.ResourceID) ||
			!validOpaqueIDV1(item.RunID) ||
			((item.Kind == controloverview.UnknownKindLearningProposalV1 ||
				item.Kind == controloverview.UnknownKindLearningTaskV1) &&
				!moduleapi.ValidSHA256(item.ResourceID)) ||
			!validOverviewUnknownRevisionV1(item.Kind, item.Revision) ||
			!validPositiveJSONIntegerV1(item.UpdatedAtUnixMicros) ||
			item.UpdatedAtUnixMicros > snapshot.SourceUpdatedAtUnixMicros ||
			(index > 0 && !overviewOrderWithKindV1(snapshot.Unknown[index-1].UpdatedAtUnixMicros,
				snapshot.Unknown[index-1].ResourceID, snapshot.Unknown[index-1].Kind,
				item.UpdatedAtUnixMicros, item.ResourceID, item.Kind)) {
			return ErrIntegrityFailure
		}
		if run, found := seenRuns[item.RunID]; found &&
			(run.TenantID != item.TenantID || run.WorkspaceID != item.WorkspaceID ||
				run.State != overviewRunStateWaitingReconciliationV1 ||
				run.Disposition != overviewRunStateWaitingReconciliationV1) {
			return ErrIntegrityFailure
		}
		if item.Kind == controloverview.UnknownKindModelV1 {
			modelUnknown[item.ResourceID] = item
		} else if item.Kind == controloverview.UnknownKindLearningProposalV1 {
			proposalUnknown[item.ResourceID] = item
		}
	}
	for index, item := range snapshot.Learning {
		if err := reauthorizeModulesRequestV1(authorization, request); err != nil {
			return err
		}
		if _, duplicate := seenLearning[item.ProposalID]; duplicate {
			return ErrIntegrityFailure
		}
		seenLearning[item.ProposalID] = struct{}{}
		if !validOverviewOwnershipV1(request.scope, item.TenantID, item.WorkspaceID) ||
			!moduleapi.ValidSHA256(item.ProposalID) || !validOverviewLearningKindV1(item.Kind) ||
			!validOverviewLearningProjectionV1(item.State, item.Revision) ||
			!validPositiveJSONIntegerV1(item.CreatedAtUnixMicros) ||
			!validPositiveJSONIntegerV1(item.UpdatedAtUnixMicros) ||
			item.UpdatedAtUnixMicros < item.CreatedAtUnixMicros ||
			item.UpdatedAtUnixMicros > snapshot.SourceUpdatedAtUnixMicros ||
			(index > 0 && !overviewOrderV1(snapshot.Learning[index-1].UpdatedAtUnixMicros,
				snapshot.Learning[index-1].ProposalID, item.UpdatedAtUnixMicros, item.ProposalID)) {
			return ErrIntegrityFailure
		}
		if unknown, found := proposalUnknown[item.ProposalID]; found &&
			(item.State != "REVIEW_UNKNOWN" || item.Revision != unknown.Revision ||
				item.TenantID != unknown.TenantID || item.WorkspaceID != unknown.WorkspaceID ||
				item.UpdatedAtUnixMicros != unknown.UpdatedAtUnixMicros) {
			return ErrIntegrityFailure
		}
	}
	for index, item := range snapshot.ModuleCandidates {
		if err := reauthorizeModulesRequestV1(authorization, request); err != nil {
			return err
		}
		if _, duplicate := seenModuleReviews[item.ReviewID]; duplicate {
			return ErrIntegrityFailure
		}
		seenModuleReviews[item.ReviewID] = struct{}{}
		if item.TenantID != request.scope.TenantID || !moduleapi.ValidSHA256(item.ReviewID) ||
			!moduleapi.ValidSHA256(item.CandidateID) || !validOverviewModuleTargetV1(request.scope, item) ||
			!validOpaqueIDV1(item.CurrentInstanceID) || !validOpaqueIDV1(item.TargetInstanceID) ||
			item.CurrentInstanceID == item.TargetInstanceID ||
			(moduleapi.Ref{ID: item.CurrentModuleID, Version: item.CurrentExactVersion}).Validate() != nil ||
			(moduleapi.Ref{ID: item.TargetModuleID, Version: item.TargetExactVersion}).Validate() != nil ||
			item.CurrentModuleID != item.TargetModuleID ||
			item.CurrentExactVersion == item.TargetExactVersion ||
			!moduleapi.ValidSHA256(item.CurrentArtifactDigest) ||
			!moduleapi.ValidSHA256(item.TargetArtifactDigest) ||
			item.CurrentArtifactDigest == item.TargetArtifactDigest ||
			!validOverviewConclusionV1(item.Conclusion) ||
			!validPositiveJSONIntegerV1(item.CreatedAtUnixMicros) ||
			item.CreatedAtUnixMicros > snapshot.SourceUpdatedAtUnixMicros ||
			(index > 0 && !overviewOrderV1(snapshot.ModuleCandidates[index-1].CreatedAtUnixMicros,
				snapshot.ModuleCandidates[index-1].ReviewID, item.CreatedAtUnixMicros, item.ReviewID)) {
			return ErrIntegrityFailure
		}
	}
	for index, item := range snapshot.Usage {
		if err := reauthorizeModulesRequestV1(authorization, request); err != nil {
			return err
		}
		if _, duplicate := seenUsage[item.AttemptID]; duplicate {
			return ErrIntegrityFailure
		}
		seenUsage[item.AttemptID] = struct{}{}
		if !validOverviewOwnershipV1(request.scope, item.TenantID, item.WorkspaceID) ||
			!validOpaqueIDV1(item.AttemptID) || !validOpaqueIDV1(item.RunID) ||
			!validOverviewUsageProjectionV1(item) ||
			!validPositiveJSONIntegerV1(item.UpdatedAtUnixMicros) ||
			item.UpdatedAtUnixMicros > snapshot.SourceUpdatedAtUnixMicros ||
			(index > 0 && !overviewOrderV1(snapshot.Usage[index-1].UpdatedAtUnixMicros,
				snapshot.Usage[index-1].AttemptID, item.UpdatedAtUnixMicros, item.AttemptID)) {
			return ErrIntegrityFailure
		}
		if unknown, found := modelUnknown[item.AttemptID]; found &&
			(item.UsageStatus != "PENDING_RECONCILIATION" ||
				item.Revision != unknown.Revision || item.RunID != unknown.RunID ||
				item.TenantID != unknown.TenantID || item.WorkspaceID != unknown.WorkspaceID ||
				item.UpdatedAtUnixMicros != unknown.UpdatedAtUnixMicros) {
			return ErrIntegrityFailure
		}
	}
	return nil
}

func exactOverviewSourceUpdatedAtV1(snapshot controloverview.SnapshotV1) uint64 {
	result := snapshot.BasisSourceUpdatedAtUnixMicros
	for _, item := range snapshot.Runs {
		result = maxOverviewSourceMicrosV1(result, item.UpdatedAtUnixMicros)
	}
	for _, item := range snapshot.Unknown {
		result = maxOverviewSourceMicrosV1(result, item.UpdatedAtUnixMicros)
	}
	for _, item := range snapshot.Learning {
		result = maxOverviewSourceMicrosV1(result, item.UpdatedAtUnixMicros)
	}
	for _, item := range snapshot.ModuleCandidates {
		result = maxOverviewSourceMicrosV1(result, item.CreatedAtUnixMicros)
	}
	for _, item := range snapshot.Usage {
		result = maxOverviewSourceMicrosV1(result, item.UpdatedAtUnixMicros)
	}
	return result
}

func maxOverviewSourceMicrosV1(left, right uint64) uint64 {
	if right > left {
		return right
	}
	return left
}

func validOverviewRunProjectionV1(state, disposition string, revision uint64) bool {
	if !validOverviewRevisionV1(revision) {
		return false
	}
	switch state {
	case overviewRunStateAdmittedV1:
		return disposition == "" || disposition == "WAITING_EXTERNAL"
	case overviewRunStateWaitingReconciliationV1,
		overviewRunStateTerminatedV1:
		return revision >= 1 && disposition == state
	default:
		return false
	}
}

func overviewSectionsV1(
	request authorizedRequestV1,
	sourceRevision uint64,
	snapshot controloverview.SnapshotV1,
) ([]controlapicontract.ControlViewSectionV1, error) {
	if sourceRevision == 0 {
		return nil, ErrIntegrityFailure
	}
	type sectionInput struct {
		kind      controlapicontract.ControlViewSectionKindV1
		items     any
		count     int
		truncated bool
	}
	inputs := []sectionInput{
		{controlapicontract.ViewSectionWorkspacesV1, cloneOverviewWorkspacesV1(snapshot.Workspaces), len(snapshot.Workspaces), snapshot.WorkspacesTruncated},
		{controlapicontract.ViewSectionRunsV1, cloneOverviewRunsV1(snapshot.Runs), len(snapshot.Runs), snapshot.RunsTruncated},
		{controlapicontract.ViewSectionUnknownV1, cloneOverviewUnknownV1(snapshot.Unknown), len(snapshot.Unknown), snapshot.UnknownTruncated},
		{controlapicontract.ViewSectionLearningV1, cloneOverviewLearningV1(snapshot.Learning), len(snapshot.Learning), snapshot.LearningTruncated},
		{controlapicontract.ViewSectionModulesV1, cloneOverviewCandidatesV1(snapshot.ModuleCandidates), len(snapshot.ModuleCandidates), snapshot.ModuleCandidatesTruncated},
		{controlapicontract.ViewSectionUsageV1, cloneOverviewUsageV1(snapshot.Usage), len(snapshot.Usage), snapshot.UsageTruncated},
	}
	sections := make([]controlapicontract.ControlViewSectionV1, 0, len(inputs))
	for _, input := range inputs {
		_, digest, err := canonicalDigestV1(overviewSectionSourceDomainV1, struct {
			SchemaVersion string                                      `json:"schema_version"`
			ScopeDigest   string                                      `json:"scope_digest"`
			Kind          controlapicontract.ControlViewSectionKindV1 `json:"kind"`
			Items         any                                         `json:"items"`
			Truncated     bool                                        `json:"truncated"`
		}{"control-overview-section-source/v1", request.scopeDigest, input.kind, input.items, input.truncated})
		if err != nil {
			return nil, ErrResourceExhausted
		}
		sections = append(sections, controlapicontract.ControlViewSectionV1{
			Kind: input.kind, SourceRevision: sourceRevision, SourceDigest: digest,
			ItemCount: uint32(input.count), Truncated: input.truncated,
		})
	}
	return sections, nil
}

func digestOverviewProjectionV1(result OverviewResultV1) (string, error) {
	copy := cloneOverviewResultV1(result)
	copy.ProjectionDigest, copy.StrongETag = "", ""
	_, digest, err := canonicalDigestV1(overviewProjectionDomainV1, copy)
	return digest, err
}

func validOverviewOwnershipV1(scope controlapicontract.ControlScopeV1, tenant, workspace string) bool {
	return tenant == scope.TenantID && validOpaqueIDV1(workspace) &&
		(scope.Kind == controlapicontract.ScopeTenantV1 || workspace == scope.WorkspaceID)
}

func validOverviewRevisionV1(value uint64) bool {
	return value <= uint64(1<<53-1) && value <= math.MaxInt64
}

func overviewOrderV1(previousTime uint64, previousID string, currentTime uint64, currentID string) bool {
	return previousTime > currentTime || (previousTime == currentTime && previousID > currentID)
}

func overviewOrderWithKindV1(pt uint64, pi, pk string, ct uint64, ci, ck string) bool {
	return pt > ct || (pt == ct && (pi > ci || (pi == ci && pk > ck)))
}

func validOverviewUnknownKindV1(value string) bool {
	switch value {
	case controloverview.UnknownKindModelV1, controloverview.UnknownKindActionV1,
		controloverview.UnknownKindChannelSendV1,
		controloverview.UnknownKindLearningProposalV1,
		controloverview.UnknownKindLearningTaskV1:
		return true
	default:
		return false
	}
}

func validOverviewUnknownRevisionV1(kind string, revision uint64) bool {
	if !validOverviewRevisionV1(revision) {
		return false
	}
	switch kind {
	case controloverview.UnknownKindModelV1:
		return revision == 1
	case controloverview.UnknownKindActionV1:
		return revision == 1 || revision == 2
	case controloverview.UnknownKindChannelSendV1:
		return revision >= 1
	case controloverview.UnknownKindLearningProposalV1,
		controloverview.UnknownKindLearningTaskV1:
		return revision == 2
	default:
		return false
	}
}

func validOverviewLearningKindV1(value string) bool { return value == "KNOWLEDGE" || value == "SKILL" }

func validOverviewLearningProjectionV1(state string, revision uint64) bool {
	if !validOverviewRevisionV1(revision) {
		return false
	}
	switch state {
	case "SUBMITTED":
		return revision == 0
	case "REVIEW_PENDING":
		return revision == 1
	case "REVIEW_UNKNOWN":
		return revision == 2
	case "APPROVED", "REJECTED", "REVIEW_FAILED":
		return revision == 2 || revision == 3
	default:
		return false
	}
}

func validOverviewModuleTargetV1(scope controlapicontract.ControlScopeV1, item controloverview.ModuleCandidateV1) bool {
	if item.BindingTargetKind == "PROFILE" {
		return scope.Kind == controlapicontract.ScopeTenantV1 && item.WorkspaceID == ""
	}
	return item.BindingTargetKind == "WORKSPACE_CHANNEL_ENDPOINT" && validOpaqueIDV1(item.WorkspaceID) &&
		(scope.Kind == controlapicontract.ScopeTenantV1 || item.WorkspaceID == scope.WorkspaceID)
}

func validOverviewConclusionV1(value string) bool {
	return value == "WOULD_APPLY" || value == "CONFLICT" || value == "UNSUPPORTED"
}

func validOverviewTokensV1(item controloverview.UsageV1) bool {
	for _, value := range []*uint64{item.InputTokens, item.CachedInputTokens, item.UncachedInputTokens, item.OutputTokens, item.ReasoningTokens} {
		if value != nil && !validOverviewRevisionV1(*value) {
			return false
		}
	}
	return item.InputTokens == nil || item.CachedInputTokens == nil || item.UncachedInputTokens == nil ||
		*item.InputTokens == *item.CachedInputTokens+*item.UncachedInputTokens
}

func validOverviewUsageProjectionV1(item controloverview.UsageV1) bool {
	if !validOverviewRevisionV1(item.Revision) || !validOverviewTokensV1(item) {
		return false
	}
	allTokensNil := item.InputTokens == nil && item.CachedInputTokens == nil &&
		item.UncachedInputTokens == nil && item.OutputTokens == nil &&
		item.ReasoningTokens == nil
	switch item.UsageStatus {
	case "PENDING":
		return item.Revision == 0 && allTokensNil
	case "PENDING_RECONCILIATION":
		return item.Revision == 1
	case "PROVIDER_REPORTED":
		return item.Revision == 1 || item.Revision == 2
	case "NO_USAGE_REPORTED":
		return item.Revision <= 2 && allTokensNil
	default:
		return false
	}
}

func cloneOverviewWorkspacesV1(input []controloverview.WorkspaceRefV1) []controloverview.WorkspaceRefV1 {
	return append([]controloverview.WorkspaceRefV1{}, input...)
}
func cloneOverviewRunsV1(input []controloverview.RunV1) []controloverview.RunV1 {
	return append([]controloverview.RunV1{}, input...)
}
func cloneOverviewUnknownV1(input []controloverview.UnknownV1) []controloverview.UnknownV1 {
	return append([]controloverview.UnknownV1{}, input...)
}
func cloneOverviewLearningV1(input []controloverview.LearningV1) []controloverview.LearningV1 {
	return append([]controloverview.LearningV1{}, input...)
}
func cloneOverviewCandidatesV1(input []controloverview.ModuleCandidateV1) []controloverview.ModuleCandidateV1 {
	return append([]controloverview.ModuleCandidateV1{}, input...)
}
func cloneOverviewUsageV1(input []controloverview.UsageV1) []controloverview.UsageV1 {
	result := make([]controloverview.UsageV1, len(input))
	for i := range input {
		result[i] = input[i]
		for source, target := range map[*uint64]**uint64{
			input[i].InputTokens:         &result[i].InputTokens,
			input[i].CachedInputTokens:   &result[i].CachedInputTokens,
			input[i].UncachedInputTokens: &result[i].UncachedInputTokens,
			input[i].OutputTokens:        &result[i].OutputTokens,
			input[i].ReasoningTokens:     &result[i].ReasoningTokens,
		} {
			if source != nil {
				value := *source
				*target = &value
			}
		}
	}
	return result
}

func cloneOverviewResultV1(input OverviewResultV1) OverviewResultV1 {
	result := input
	result.View = cloneControlViewV1(input.View)
	result.Workspaces = cloneOverviewWorkspacesV1(input.Workspaces)
	result.Runs = cloneOverviewRunsV1(input.Runs)
	result.Unknown = cloneOverviewUnknownV1(input.Unknown)
	result.Learning = cloneOverviewLearningV1(input.Learning)
	result.ModuleCandidates = cloneOverviewCandidatesV1(input.ModuleCandidates)
	result.Usage = cloneOverviewUsageV1(input.Usage)
	return result
}
