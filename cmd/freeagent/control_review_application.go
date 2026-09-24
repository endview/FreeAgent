package main

import (
	"context"
	"errors"

	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleupgrade"
)

// productionModuleUpgradeReviewReaderV1 is the only composition adapter for
// the P4 read service. It maps Current Store records to detached application
// facts and never exposes canonical bodies, filesystem paths, or SQL handles.
type productionModuleUpgradeReviewReaderV1 struct {
	store *currentstore.Store
}

func (reader productionModuleUpgradeReviewReaderV1) ListModuleUpgradeReviews(
	ctx context.Context,
	tenantID string,
	limit uint16,
) ([]controlapp.ModuleUpgradeReviewRecordV1, error) {
	if reader.store == nil {
		return nil, controlapp.ErrStoreUnavailable
	}
	records, err := reader.store.ListModuleUpgradeReviewsBounded(ctx, tenantID, int(limit))
	if err != nil {
		return nil, mapControlStoreReadErrorV1(err)
	}
	result := make([]controlapp.ModuleUpgradeReviewRecordV1, len(records))
	for index, record := range records {
		result[index] = controlapp.ModuleUpgradeReviewRecordV1{
			ReviewID: record.ReviewID, Review: projectModuleUpgradeReviewV1(record.Review), CreatedAt: record.CreatedAt,
		}
	}
	return result, nil
}

func (reader productionModuleUpgradeReviewReaderV1) GetModuleUpgradeReview(
	ctx context.Context,
	reviewID string,
) (controlapp.ModuleUpgradeReviewRecordV1, error) {
	if reader.store == nil {
		return controlapp.ModuleUpgradeReviewRecordV1{}, controlapp.ErrStoreUnavailable
	}
	record, err := reader.store.GetModuleUpgradeReview(ctx, reviewID)
	if err != nil {
		if errors.Is(err, currentstore.ErrModuleUpgradeReviewNotFound) {
			return controlapp.ModuleUpgradeReviewRecordV1{}, controlapp.ErrNotFound
		}
		return controlapp.ModuleUpgradeReviewRecordV1{}, mapControlStoreReadErrorV1(err)
	}
	return controlapp.ModuleUpgradeReviewRecordV1{
		ReviewID: record.ReviewID, Review: projectModuleUpgradeReviewV1(record.Review), CreatedAt: record.CreatedAt,
	}, nil
}

func (reader productionModuleUpgradeReviewReaderV1) GetModuleCandidateDecision(
	ctx context.Context,
	reviewID string,
) (controlapp.ModuleCandidateDecisionRecordV1, error) {
	if reader.store == nil {
		return controlapp.ModuleCandidateDecisionRecordV1{}, controlapp.ErrStoreUnavailable
	}
	record, err := reader.store.GetModuleCandidateDecision(ctx, reviewID)
	if err != nil {
		if errors.Is(err, currentstore.ErrModuleUpgradeReviewNotFound) {
			return controlapp.ModuleCandidateDecisionRecordV1{}, controlapp.ErrNotFound
		}
		return controlapp.ModuleCandidateDecisionRecordV1{}, mapControlStoreReadErrorV1(err)
	}
	return controlapp.ModuleCandidateDecisionRecordV1{
		DecisionID: record.DecisionID, Decision: record.Decision, DecidedAt: record.DecidedAt,
	}, nil
}

func (reader productionModuleUpgradeReviewReaderV1) GetModuleArtifactAdmission(
	ctx context.Context,
	admissionID string,
	tenantID string,
) (controlapp.ModuleArtifactAdmissionRecordV1, error) {
	if reader.store == nil {
		return controlapp.ModuleArtifactAdmissionRecordV1{}, controlapp.ErrStoreUnavailable
	}
	record, err := reader.store.GetModuleArtifactAdmissionForTenantV1(ctx, admissionID, tenantID)
	if err != nil {
		if errors.Is(err, currentstore.ErrModuleArtifactAdmissionNotFound) {
			return controlapp.ModuleArtifactAdmissionRecordV1{}, controlapp.ErrNotFound
		}
		return controlapp.ModuleArtifactAdmissionRecordV1{}, mapControlStoreReadErrorV1(err)
	}
	return controlapp.ModuleArtifactAdmissionRecordV1{
		AdmissionID:                 record.AdmissionID,
		SourceID:                    record.Record.SourceID,
		SourcePolicyID:              record.Record.SourcePolicyID,
		SourcePolicyRevision:        record.Record.SourcePolicyRevision,
		SnapshotID:                  record.Record.SnapshotID,
		SnapshotObservationRevision: record.Record.SnapshotObservationRevision,
		EntryOrdinal:                record.Record.EntryOrdinal,
		Module:                      record.Record.Module,
		Artifact: controlapp.ModuleArtifactProjectionV1{
			ArtifactDigest:    record.Artifact.ArtifactDigest,
			Module:            record.Artifact.Module,
			ManifestRef:       record.Artifact.ManifestRef,
			ArtifactSizeBytes: record.Artifact.ArtifactSizeBytes,
			CoveredFileCount:  record.Artifact.CoveredFileCount,
		},
		AdmittedAt: record.AdmittedAt,
	}, nil
}

func projectModuleUpgradeReviewV1(review moduleupgrade.ReviewV1) controlapp.ModuleUpgradeReviewProjectionV1 {
	return controlapp.ModuleUpgradeReviewProjectionV1{
		SchemaVersion:           review.SchemaVersion,
		CandidateID:             review.CandidateID,
		ReviewKey:               review.ReviewKey,
		TenantID:                review.TenantID,
		ArtifactAdmissionID:     review.ArtifactAdmissionID,
		OperatorPrincipalID:     review.OperatorPrincipalID,
		ReviewRequestDigest:     review.ReviewRequestDigest,
		BindingTarget:           projectModuleUpgradeBindingTargetV1(review.BindingTarget),
		Port:                    review.Port,
		PortBindingIndex:        review.PortBindingIndex,
		TargetInstanceID:        review.TargetInstanceID,
		TargetModule:            review.Target.Module,
		TargetArtifactDigest:    review.Target.ArtifactDigest,
		TargetArtifactSizeBytes: review.Target.ArtifactSizeBytes,
		Conclusion:              string(review.Conclusion),
		ReasonCodes:             projectModuleUpgradeReasonCodesV1(review.ReasonCodes),
	}
}

func projectModuleUpgradeBindingTargetV1(target moduleupgrade.BindingTargetV1) controlapp.ModuleUpgradeReviewBindingTargetV1 {
	return controlapp.ModuleUpgradeReviewBindingTargetV1{
		Kind:        string(target.Kind),
		ProfileID:   target.ProfileID,
		WorkspaceID: target.WorkspaceID,
		EndpointID:  target.EndpointID,
	}
}

func projectModuleUpgradeReasonCodesV1(codes []moduleupgrade.ReasonCodeV1) []string {
	if len(codes) == 0 {
		return nil
	}
	result := make([]string, len(codes))
	for index, code := range codes {
		result[index] = string(code)
	}
	return result
}

var _ controlapp.ModuleUpgradeReviewReaderV1 = productionModuleUpgradeReviewReaderV1{}
