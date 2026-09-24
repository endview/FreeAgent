package main

import (
	"context"
	"errors"

	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/currentstore"
)

type productionControlManagementReaderV1 struct {
	store *currentstore.Store
}

func (reader productionControlManagementReaderV1) ListUnknownAttempts(
	ctx context.Context,
	tenantID string,
	workspaceID string,
	limit int,
) ([]controlapp.UnknownAttemptRecordV1, bool, error) {
	if reader.store == nil {
		return nil, false, controlapp.ErrStoreUnavailable
	}
	records, hasMore, err := reader.store.ListUnknownAttemptProjectionsV1(
		ctx, tenantID, workspaceID, limit,
	)
	if err != nil {
		return nil, false, mapControlManagementStoreReadErrorV1(err)
	}
	result := make([]controlapp.UnknownAttemptRecordV1, len(records))
	for index, record := range records {
		result[index] = projectUnknownAttemptV1(record)
	}
	return result, hasMore, nil
}

func (reader productionControlManagementReaderV1) GetUnknownAttempt(
	ctx context.Context,
	tenantID string,
	workspaceID string,
	kind controlapp.UnknownAttemptKindV1,
	attemptID string,
) (controlapp.UnknownAttemptRecordV1, error) {
	if reader.store == nil {
		return controlapp.UnknownAttemptRecordV1{}, controlapp.ErrStoreUnavailable
	}
	record, err := reader.store.GetUnknownAttemptProjectionV1(
		ctx, tenantID, workspaceID, currentstore.UnknownAttemptKindV1(kind), attemptID,
	)
	if err != nil {
		return controlapp.UnknownAttemptRecordV1{}, mapControlManagementStoreReadErrorV1(err)
	}
	return projectUnknownAttemptV1(record), nil
}

func (reader productionControlManagementReaderV1) ListArtifactAdmissions(
	ctx context.Context,
	tenantID string,
	workspaceID string,
	limit int,
) ([]controlapp.ModuleArtifactAdmissionRecordV1, bool, error) {
	if reader.store == nil {
		return nil, false, controlapp.ErrStoreUnavailable
	}
	records, hasMore, err := reader.store.ListModuleArtifactAdmissionsForTenantV1(
		ctx, tenantID, workspaceID, limit,
	)
	if err != nil {
		return nil, false, mapControlManagementStoreReadErrorV1(err)
	}
	result := make([]controlapp.ModuleArtifactAdmissionRecordV1, len(records))
	for index, record := range records {
		result[index] = controlapp.ModuleArtifactAdmissionRecordV1{
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
		}
	}
	return result, hasMore, nil
}

func (reader productionControlManagementReaderV1) VerifyStore(
	ctx context.Context,
) (controlapp.StoreVerificationRecordV1, error) {
	if reader.store == nil {
		return controlapp.StoreVerificationRecordV1{}, controlapp.ErrStoreUnavailable
	}
	verification, err := reader.store.VerifyReadOnlyV1(ctx)
	if err != nil {
		return controlapp.StoreVerificationRecordV1{}, mapControlManagementStoreReadErrorV1(err)
	}
	return controlapp.StoreVerificationRecordV1{
		StoreInstanceID:   verification.StoreInstanceID,
		SchemaIdentity:    verification.SchemaIdentity,
		SchemaVersion:     verification.SchemaVersion,
		SchemaFingerprint: verification.SchemaFingerprint,
		GeneratorID:       verification.GeneratorID,
	}, nil
}

func projectUnknownAttemptV1(
	record currentstore.UnknownAttemptProjectionV1,
) controlapp.UnknownAttemptRecordV1 {
	return controlapp.UnknownAttemptRecordV1{
		Kind:                      controlapp.UnknownAttemptKindV1(record.Kind),
		AttemptID:                 record.AttemptID,
		RunID:                     record.RunID,
		TenantID:                  record.TenantID,
		WorkspaceID:               record.WorkspaceID,
		State:                     record.State,
		Provider:                  record.Provider,
		Model:                     record.Model,
		ProviderRequestID:         record.ProviderRequestID,
		ExternalOperationID:       record.ExternalOperationID,
		EndpointID:                record.EndpointID,
		ErrorClassification:       record.ErrorClassification,
		UnknownReason:             record.UnknownReason,
		HasReconciliationEvidence: record.HasReconciliationEvidence,
		ReconciliationEvidenceRef: record.ReconciliationEvidenceRef,
		Revision:                  record.Revision,
		CreatedAt:                 record.CreatedAt,
		UpdatedAt:                 record.UpdatedAt,
		InputTokens:               record.Usage.InputTokens,
		CachedInputTokens:         record.Usage.CachedInputTokens,
		UncachedInputTokens:       record.Usage.UncachedInputTokens,
		OutputTokens:              record.Usage.OutputTokens,
		ReasoningTokens:           record.Usage.ReasoningTokens,
	}
}

func mapControlManagementStoreReadErrorV1(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, controlapp.ErrNotFound),
		errors.Is(err, currentstore.ErrModuleArtifactAdmissionNotFound),
		errors.Is(err, currentstore.ErrModuleUpgradeReviewNotFound),
		errors.Is(err, currentstore.ErrActionDispatchConflict),
		errors.Is(err, currentstore.ErrChannelDispatchConflict):
		return controlapp.ErrNotFound
	case errors.Is(err, currentstore.ErrInvalidModelDispatch),
		errors.Is(err, currentstore.ErrInvalidModuleArtifactIngress),
		errors.Is(err, currentstore.ErrInvalidModuleUpgradeReview):
		return controlapp.ErrInvalidRequest
	case errors.Is(err, currentstore.ErrModuleArtifactIngressIntegrity),
		errors.Is(err, currentstore.ErrModuleUpgradeIntegrity):
		return controlapp.ErrIntegrityFailure
	case errors.Is(err, currentstore.ErrOwnerActive):
		return controlapp.ErrStoreBusy
	case errors.Is(err, currentstore.ErrStoreClosed),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return controlapp.ErrCancelled
		}
		return controlapp.ErrStoreUnavailable
	default:
		var identity *currentstore.IdentityError
		if errors.As(err, &identity) {
			return controlapp.ErrIntegrityFailure
		}
		return controlapp.ErrStoreUnavailable
	}
}

var _ controlapp.ControlManagementReaderV1 = productionControlManagementReaderV1{}
