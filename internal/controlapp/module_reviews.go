package controlapp

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ModuleUpgradeReviewListSchemaVersionV1   = "control-module-upgrade-review-list/v1"
	ModuleUpgradeReviewDetailSchemaVersionV1 = "control-module-upgrade-review-detail/v1"
	MaximumModuleUpgradeReviewItemsV1        = uint16(controlapicontract.MaxControlPageSizeV1)
	moduleUpgradeReviewProjectionDomainV1    = "freeagent.control-module-upgrade-review-projection/v1"
	moduleUpgradeReviewETagDomainV1          = "freeagent.control-module-upgrade-review-etag/v1"
)

// ModuleUpgradeReviewRecordV1 is the bounded source projection consumed by
// this service. The control layer never receives a SQL handle or a host path.
type ModuleUpgradeReviewRecordV1 struct {
	ReviewID  string
	Review    ModuleUpgradeReviewProjectionV1
	CreatedAt time.Time
}

type ModuleCandidateDecisionRecordV1 struct {
	DecisionID string
	Decision   moduleapi.ModuleCandidateDecisionV1
	DecidedAt  time.Time
}

type ModuleArtifactAdmissionRecordV1 struct {
	AdmissionID                 string
	SourceID                    string
	SourcePolicyID              string
	SourcePolicyRevision        uint64
	SnapshotID                  string
	SnapshotObservationRevision uint64
	EntryOrdinal                uint32
	Module                      moduleapi.Ref
	Artifact                    ModuleArtifactProjectionV1
	AdmittedAt                  time.Time
}

type ModuleArtifactProjectionV1 struct {
	ArtifactDigest    string        `json:"artifact_digest"`
	Module            moduleapi.Ref `json:"module"`
	ManifestRef       string        `json:"manifest_ref"`
	ArtifactSizeBytes uint64        `json:"artifact_size_bytes"`
	CoveredFileCount  uint64        `json:"covered_file_count"`
}

// ModuleUpgradeReviewReaderV1 is the only Store dependency of the P4 read
// service. Implementations must return detached values and enforce their own
// bounded source query; no online caller receives the Current Store APIs.
type ModuleUpgradeReviewReaderV1 interface {
	ListModuleUpgradeReviews(context.Context, string, uint16) ([]ModuleUpgradeReviewRecordV1, error)
	GetModuleUpgradeReview(context.Context, string) (ModuleUpgradeReviewRecordV1, error)
	GetModuleCandidateDecision(context.Context, string) (ModuleCandidateDecisionRecordV1, error)
	GetModuleArtifactAdmission(context.Context, string, string) (ModuleArtifactAdmissionRecordV1, error)
}

type ModuleUpgradeReviewListInputV1 struct {
	Authorization AuthorizationContextV1
	Scope         controlapicontract.ControlScopeV1
	Limit         uint16
	ObservedAt    uint64
}

type ModuleUpgradeReviewItemV1 struct {
	ReviewID                string                               `json:"review_id"`
	CandidateID             string                               `json:"candidate_id"`
	ReviewKey               string                               `json:"review_key"`
	TenantID                string                               `json:"tenant_id"`
	ArtifactAdmissionID     string                               `json:"artifact_admission_id"`
	OperatorPrincipalID     string                               `json:"operator_principal_id"`
	ReviewRequestDigest     string                               `json:"review_request_digest"`
	BindingTarget           ModuleUpgradeReviewBindingTargetV1   `json:"binding_target"`
	Port                    moduleapi.PortRef                    `json:"port"`
	TargetInstanceID        string                               `json:"target_instance_id"`
	TargetModule            moduleapi.Ref                        `json:"target_module"`
	TargetArtifactDigest    string                               `json:"target_artifact_digest"`
	TargetArtifactSizeBytes uint64                               `json:"target_artifact_size_bytes"`
	Conclusion              string                               `json:"conclusion"`
	ReasonCodes             []string                             `json:"reason_codes"`
	CreatedAtUnixMicros     uint64                               `json:"created_at_unix_micros"`
	Decision                *ModuleCandidateDecisionProjectionV1 `json:"decision,omitempty"`
	Artifact                *ModuleArtifactProjectionV1          `json:"artifact,omitempty"`
}

type ModuleCandidateDecisionProjectionV1 struct {
	DecisionID          string                                   `json:"decision_id"`
	Decision            moduleapi.ModuleCandidateDecisionValueV1 `json:"decision"`
	OperatorPrincipalID string                                   `json:"operator_principal_id"`
	Reason              string                                   `json:"reason"`
	DecidedAtUnixMicros uint64                                   `json:"decided_at_unix_micros"`
}

type ModuleUpgradeReviewBindingTargetV1 struct {
	Kind        string `json:"kind"`
	ProfileID   string `json:"profile_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	EndpointID  string `json:"endpoint_id,omitempty"`
}

// ModuleUpgradeReviewProjectionV1 is a safe projection of a verified Review.
// It omits canonical bodies, host paths, URLs, signatures, secrets, and
// target package content.
type ModuleUpgradeReviewProjectionV1 struct {
	SchemaVersion           string                             `json:"schema_version"`
	CandidateID             string                             `json:"candidate_id"`
	ReviewKey               string                             `json:"review_key"`
	TenantID                string                             `json:"tenant_id"`
	ArtifactAdmissionID     string                             `json:"artifact_admission_id"`
	OperatorPrincipalID     string                             `json:"operator_principal_id"`
	ReviewRequestDigest     string                             `json:"review_request_digest"`
	BindingTarget           ModuleUpgradeReviewBindingTargetV1 `json:"binding_target"`
	Port                    moduleapi.PortRef                  `json:"port"`
	PortBindingIndex        uint32                             `json:"port_binding_index"`
	TargetInstanceID        string                             `json:"target_instance_id"`
	TargetModule            moduleapi.Ref                      `json:"target_module"`
	TargetArtifactDigest    string                             `json:"target_artifact_digest"`
	TargetArtifactSizeBytes uint64                             `json:"target_artifact_size_bytes"`
	Conclusion              string                             `json:"conclusion"`
	ReasonCodes             []string                           `json:"reason_codes"`
}

type ModuleUpgradeReviewListResultV1 struct {
	SchemaVersion    string                            `json:"schema_version"`
	Scope            controlapicontract.ControlScopeV1 `json:"scope"`
	Items            []ModuleUpgradeReviewItemV1       `json:"items"`
	HasMore          bool                              `json:"has_more"`
	ProjectionDigest string                            `json:"projection_digest"`
	StrongETag       string                            `json:"strong_etag"`
}

type ModuleUpgradeReviewDetailResultV1 struct {
	SchemaVersion       string                               `json:"schema_version"`
	Scope               controlapicontract.ControlScopeV1    `json:"scope"`
	ReviewID            string                               `json:"review_id"`
	Review              ModuleUpgradeReviewProjectionV1      `json:"review"`
	CreatedAtUnixMicros uint64                               `json:"created_at_unix_micros"`
	Decision            *ModuleCandidateDecisionProjectionV1 `json:"decision,omitempty"`
	Artifact            ModuleArtifactProjectionV1           `json:"artifact"`
	Admission           ModuleArtifactAdmissionProjectionV1  `json:"admission"`
	ProjectionDigest    string                               `json:"projection_digest"`
	StrongETag          string                               `json:"strong_etag"`
}

type ModuleArtifactAdmissionProjectionV1 struct {
	AdmissionID                 string        `json:"admission_id"`
	SourceID                    string        `json:"source_id"`
	SourcePolicyID              string        `json:"source_policy_id"`
	SourcePolicyRevision        uint64        `json:"source_policy_revision"`
	SnapshotID                  string        `json:"snapshot_id"`
	SnapshotObservationRevision uint64        `json:"snapshot_observation_revision"`
	EntryOrdinal                uint32        `json:"entry_ordinal"`
	Module                      moduleapi.Ref `json:"module"`
	ArtifactDigest              string        `json:"artifact_digest"`
	ManifestRef                 string        `json:"manifest_ref"`
	ArtifactSizeBytes           uint64        `json:"artifact_size_bytes"`
	CoveredFileCount            uint64        `json:"covered_file_count"`
	AdmittedAtUnixMicros        uint64        `json:"admitted_at_unix_micros"`
}

type ModuleUpgradeReviewServiceV1 struct {
	reader ModuleUpgradeReviewReaderV1
}

func NewModuleUpgradeReviewServiceV1(reader ModuleUpgradeReviewReaderV1) (*ModuleUpgradeReviewServiceV1, error) {
	if nilInterfaceV1(reader) {
		return nil, ErrInvalidRequest
	}
	return &ModuleUpgradeReviewServiceV1{reader: reader}, nil
}

func (service *ModuleUpgradeReviewServiceV1) ListModuleUpgradeReviewsV1(
	ctx context.Context,
	input ModuleUpgradeReviewListInputV1,
) (ModuleUpgradeReviewListResultV1, error) {
	if service == nil || nilInterfaceV1(service.reader) || ctx == nil {
		return ModuleUpgradeReviewListResultV1{}, ErrInvalidRequest
	}
	authorized, err := authorizeModulesRequestV1(input.Authorization, input.Scope, input.ObservedAt)
	if err != nil {
		return ModuleUpgradeReviewListResultV1{}, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = DefaultModulesPageLimitV1
	}
	if limit == 0 || limit > MaximumModuleUpgradeReviewItemsV1 {
		return ModuleUpgradeReviewListResultV1{}, ErrInvalidRequest
	}
	records, err := service.reader.ListModuleUpgradeReviews(ctx, authorized.scope.TenantID, MaximumModuleUpgradeReviewItemsV1+1)
	if err != nil {
		return ModuleUpgradeReviewListResultV1{}, classifyReaderErrorV1(err, ErrStoreUnavailable)
	}
	if len(records) > int(MaximumModuleUpgradeReviewItemsV1) {
		return ModuleUpgradeReviewListResultV1{}, ErrResourceExhausted
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].CreatedAt.Equal(records[right].CreatedAt) {
			return records[left].ReviewID < records[right].ReviewID
		}
		return records[left].CreatedAt.Before(records[right].CreatedAt)
	})
	items := make([]ModuleUpgradeReviewItemV1, 0, minIntV1(len(records), int(limit)))
	for _, record := range records {
		if record.Review.ArtifactAdmissionID == "" ||
			record.Review.TenantID != authorized.scope.TenantID ||
			!reviewVisibleInScopeV1(record.Review, authorized.scope) {
			continue
		}
		item, itemErr := service.reviewItemV1(ctx, record)
		if itemErr != nil {
			return ModuleUpgradeReviewListResultV1{}, itemErr
		}
		items = append(items, item)
		if len(items) > int(limit) {
			return ModuleUpgradeReviewListResultV1{}, ErrResourceExhausted
		}
	}
	result := ModuleUpgradeReviewListResultV1{
		SchemaVersion: ModuleUpgradeReviewListSchemaVersionV1,
		Scope:         authorized.scope, Items: items, HasMore: false,
	}
	result.ProjectionDigest, err = canonicalDigestStringV1(moduleUpgradeReviewProjectionDomainV1, result.Items)
	if err != nil {
		return ModuleUpgradeReviewListResultV1{}, ErrIntegrityFailure
	}
	result.StrongETag, err = quotedDigestV1(moduleUpgradeReviewETagDomainV1, struct {
		Session    controlapicontract.ControlSessionV1 `json:"session"`
		Scope      controlapicontract.ControlScopeV1   `json:"scope"`
		Projection string                              `json:"projection"`
	}{authorized.session, authorized.scope, result.ProjectionDigest})
	if err != nil {
		return ModuleUpgradeReviewListResultV1{}, ErrIntegrityFailure
	}
	if err := reauthorizeModulesRequestV1(input.Authorization, authorized); err != nil {
		return ModuleUpgradeReviewListResultV1{}, err
	}
	return result, nil
}

func (service *ModuleUpgradeReviewServiceV1) GetModuleUpgradeReviewV1(
	ctx context.Context,
	input GetModuleUpgradeReviewInputV1,
) (ModuleUpgradeReviewDetailResultV1, error) {
	if service == nil || nilInterfaceV1(service.reader) || ctx == nil ||
		!moduleapi.ValidSHA256(input.ReviewID) {
		return ModuleUpgradeReviewDetailResultV1{}, ErrInvalidRequest
	}
	authorized, err := authorizeModulesRequestV1(input.Authorization, input.Scope, input.ObservedAt)
	if err != nil {
		return ModuleUpgradeReviewDetailResultV1{}, err
	}
	record, err := service.reader.GetModuleUpgradeReview(ctx, input.ReviewID)
	if err != nil {
		return ModuleUpgradeReviewDetailResultV1{}, classifyReaderErrorV1(err, ErrStoreUnavailable)
	}
	if record.Review.TenantID != authorized.scope.TenantID ||
		!reviewVisibleInScopeV1(record.Review, authorized.scope) {
		return ModuleUpgradeReviewDetailResultV1{}, ErrNotFound
	}
	admission, err := service.reader.GetModuleArtifactAdmission(
		ctx, record.Review.ArtifactAdmissionID, record.Review.TenantID,
	)
	if err != nil {
		return ModuleUpgradeReviewDetailResultV1{}, classifyReaderErrorV1(err, ErrStoreUnavailable)
	}
	decision, err := service.reader.GetModuleCandidateDecision(ctx, input.ReviewID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return ModuleUpgradeReviewDetailResultV1{}, classifyReaderErrorV1(err, ErrStoreUnavailable)
	}
	result := ModuleUpgradeReviewDetailResultV1{
		SchemaVersion: ModuleUpgradeReviewDetailSchemaVersionV1,
		Scope:         authorized.scope, ReviewID: record.ReviewID,
		Review: record.Review, CreatedAtUnixMicros: unixMicrosV1(record.CreatedAt),
		Artifact: admission.Artifact, Admission: admissionProjectionV1(admission),
	}
	if err == nil {
		result.Decision = decisionProjectionV1(decision)
	}
	result.ProjectionDigest, err = canonicalDigestStringV1(moduleUpgradeReviewProjectionDomainV1, result)
	if err != nil {
		return ModuleUpgradeReviewDetailResultV1{}, ErrIntegrityFailure
	}
	result.StrongETag, err = quotedDigestV1(moduleUpgradeReviewETagDomainV1, struct {
		Session    controlapicontract.ControlSessionV1 `json:"session"`
		Scope      controlapicontract.ControlScopeV1   `json:"scope"`
		Projection string                              `json:"projection"`
	}{authorized.session, authorized.scope, result.ProjectionDigest})
	if err != nil {
		return ModuleUpgradeReviewDetailResultV1{}, ErrIntegrityFailure
	}
	if err := reauthorizeModulesRequestV1(input.Authorization, authorized); err != nil {
		return ModuleUpgradeReviewDetailResultV1{}, err
	}
	return result, nil
}

type GetModuleUpgradeReviewInputV1 struct {
	Authorization AuthorizationContextV1
	Scope         controlapicontract.ControlScopeV1
	ReviewID      string
	ObservedAt    uint64
}

func (service *ModuleUpgradeReviewServiceV1) reviewItemV1(
	ctx context.Context,
	record ModuleUpgradeReviewRecordV1,
) (ModuleUpgradeReviewItemV1, error) {
	review := record.Review
	item := ModuleUpgradeReviewItemV1{
		ReviewID: record.ReviewID, CandidateID: review.CandidateID,
		ReviewKey: review.ReviewKey, TenantID: review.TenantID,
		ArtifactAdmissionID:     review.ArtifactAdmissionID,
		OperatorPrincipalID:     review.OperatorPrincipalID,
		ReviewRequestDigest:     review.ReviewRequestDigest,
		BindingTarget:           review.BindingTarget,
		Port:                    review.Port,
		TargetInstanceID:        review.TargetInstanceID,
		TargetModule:            review.TargetModule,
		TargetArtifactDigest:    review.TargetArtifactDigest,
		TargetArtifactSizeBytes: review.TargetArtifactSizeBytes,
		Conclusion:              review.Conclusion,
		ReasonCodes:             append([]string(nil), review.ReasonCodes...),
		CreatedAtUnixMicros:     unixMicrosV1(record.CreatedAt),
	}
	decision, err := service.reader.GetModuleCandidateDecision(ctx, record.ReviewID)
	if err == nil {
		item.Decision = decisionProjectionV1(decision)
	} else if !errors.Is(err, ErrNotFound) {
		return ModuleUpgradeReviewItemV1{}, classifyReaderErrorV1(err, ErrStoreUnavailable)
	}
	return item, nil
}

func reviewVisibleInScopeV1(review ModuleUpgradeReviewProjectionV1, scope controlapicontract.ControlScopeV1) bool {
	if scope.Kind == controlapicontract.ScopeTenantV1 {
		return true
	}
	return review.BindingTarget.Kind == "WORKSPACE_CHANNEL_ENDPOINT" &&
		review.BindingTarget.WorkspaceID == scope.WorkspaceID
}

func decisionProjectionV1(record ModuleCandidateDecisionRecordV1) *ModuleCandidateDecisionProjectionV1 {
	return &ModuleCandidateDecisionProjectionV1{
		DecisionID: record.DecisionID, Decision: record.Decision.Decision,
		OperatorPrincipalID: record.Decision.OperatorPrincipalID,
		Reason:              record.Decision.Reason,
		DecidedAtUnixMicros: unixMicrosV1(record.DecidedAt),
	}
}

func admissionProjectionV1(record ModuleArtifactAdmissionRecordV1) ModuleArtifactAdmissionProjectionV1 {
	return ModuleArtifactAdmissionProjectionV1{
		AdmissionID:                 record.AdmissionID,
		SourceID:                    record.SourceID,
		SourcePolicyID:              record.SourcePolicyID,
		SourcePolicyRevision:        record.SourcePolicyRevision,
		SnapshotID:                  record.SnapshotID,
		SnapshotObservationRevision: record.SnapshotObservationRevision,
		EntryOrdinal:                record.EntryOrdinal,
		Module:                      record.Module,
		ArtifactDigest:              record.Artifact.ArtifactDigest,
		ManifestRef:                 record.Artifact.ManifestRef,
		ArtifactSizeBytes:           record.Artifact.ArtifactSizeBytes,
		CoveredFileCount:            record.Artifact.CoveredFileCount,
		AdmittedAtUnixMicros:        unixMicrosV1(record.AdmittedAt),
	}
}

func unixMicrosV1(value time.Time) uint64 {
	if value.IsZero() || value.UnixMicro() <= 0 || value.UnixMicro() > int64(1<<53-1) {
		return 0
	}
	return uint64(value.UnixMicro())
}

func minIntV1(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func canonicalDigestStringV1(domain string, value any) (string, error) {
	_, digest, err := canonicalDigestV1(domain, value)
	return digest, err
}

func quotedDigestV1(domain string, value any) (string, error) {
	digest, err := canonicalDigestStringV1(domain, value)
	if err != nil {
		return "", err
	}
	return `"` + digest + `"`, nil
}
