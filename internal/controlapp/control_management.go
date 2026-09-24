package controlapp

import (
	"context"
	"sort"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
)

const (
	UnknownOutcomeListSchemaVersionV1   = "control-unknown-outcome-list/v1"
	UnknownOutcomeDetailSchemaVersionV1 = "control-unknown-outcome-detail/v1"
	StoreManagementSchemaVersionV1      = "control-store-management/v1"
	MaximumUnknownOutcomeItemsV1        = uint16(controlapicontract.MaxControlPageSizeV1)
	MaximumArtifactAdmissionItemsV1     = uint16(controlapicontract.MaxControlPageSizeV1)

	controlManagementProjectionDomainV1 = "freeagent.control-management-projection/v1"
	controlManagementETagDomainV1       = "freeagent.control-management-etag/v1"
)

type UnknownAttemptKindV1 string

const (
	UnknownAttemptModelV1   UnknownAttemptKindV1 = "MODEL"
	UnknownAttemptActionV1  UnknownAttemptKindV1 = "ACTION"
	UnknownAttemptChannelV1 UnknownAttemptKindV1 = "CHANNEL"
)

// UnknownAttemptRecordV1 is a detached Store projection. It contains enough
// facts to reconcile the original Attempt, but no request, receipt, secret,
// path, or replay material.
type UnknownAttemptRecordV1 struct {
	Kind                      UnknownAttemptKindV1
	AttemptID                 string
	RunID                     string
	TenantID                  string
	WorkspaceID               string
	State                     string
	Provider                  string
	Model                     string
	ProviderRequestID         string
	ExternalOperationID       string
	EndpointID                string
	ErrorClassification       string
	UnknownReason             string
	HasReconciliationEvidence bool
	ReconciliationEvidenceRef string
	Revision                  uint64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	InputTokens               *uint64
	CachedInputTokens         *uint64
	UncachedInputTokens       *uint64
	OutputTokens              *uint64
	ReasoningTokens           *uint64
}

type StoreVerificationRecordV1 struct {
	StoreInstanceID   string `json:"store_instance_id"`
	SchemaIdentity    string `json:"schema_identity"`
	SchemaVersion     int    `json:"schema_version"`
	SchemaFingerprint string `json:"schema_fingerprint"`
	GeneratorID       string `json:"generator_id"`
}

type BackupManagementProjectionV1 struct {
	FormatVersion string `json:"format_version"`
	State         string `json:"state"`
	OnlineCreate  bool   `json:"online_create"`
	OnlineRestore bool   `json:"online_restore"`
	RestoreMode   string `json:"restore_mode"`
}

// ControlManagementReaderV1 is the read-only boundary for P4's second batch.
// It deliberately exposes no backup path, SQL handle, mutation, or restore
// operation. Implementations must return detached, bounded projections.
type ControlManagementReaderV1 interface {
	ListUnknownAttempts(
		context.Context,
		string,
		string,
		int,
	) ([]UnknownAttemptRecordV1, bool, error)
	GetUnknownAttempt(
		context.Context,
		string,
		string,
		UnknownAttemptKindV1,
		string,
	) (UnknownAttemptRecordV1, error)
	ListArtifactAdmissions(
		context.Context,
		string,
		string,
		int,
	) ([]ModuleArtifactAdmissionRecordV1, bool, error)
	VerifyStore(context.Context) (StoreVerificationRecordV1, error)
}

type UnknownOutcomeListInputV1 struct {
	Authorization AuthorizationContextV1
	Scope         controlapicontract.ControlScopeV1
	Limit         uint16
	ObservedAt    uint64
}

type UnknownOutcomeDetailInputV1 struct {
	Authorization AuthorizationContextV1
	Scope         controlapicontract.ControlScopeV1
	Kind          UnknownAttemptKindV1
	AttemptID     string
	ObservedAt    uint64
}

type UnknownOutcomeProjectionV1 struct {
	Kind                      UnknownAttemptKindV1     `json:"kind"`
	AttemptID                 string                   `json:"attempt_id"`
	RunID                     string                   `json:"run_id"`
	TenantID                  string                   `json:"tenant_id"`
	WorkspaceID               string                   `json:"workspace_id"`
	State                     string                   `json:"state"`
	Provider                  string                   `json:"provider,omitempty"`
	Model                     string                   `json:"model,omitempty"`
	ProviderRequestID         string                   `json:"provider_request_id,omitempty"`
	ExternalOperationID       string                   `json:"external_operation_id,omitempty"`
	EndpointID                string                   `json:"endpoint_id,omitempty"`
	ErrorClassification       string                   `json:"error_classification,omitempty"`
	UnknownReason             string                   `json:"unknown_reason,omitempty"`
	HasReconciliationEvidence bool                     `json:"has_reconciliation_evidence"`
	ReconciliationEvidenceRef string                   `json:"reconciliation_evidence_ref,omitempty"`
	Revision                  uint64                   `json:"revision"`
	CreatedAtUnixMicros       uint64                   `json:"created_at_unix_micros"`
	UpdatedAtUnixMicros       uint64                   `json:"updated_at_unix_micros"`
	Usage                     UnknownUsageProjectionV1 `json:"usage"`
}

type UnknownUsageProjectionV1 struct {
	InputTokens         *uint64 `json:"input_tokens"`
	CachedInputTokens   *uint64 `json:"cached_input_tokens"`
	UncachedInputTokens *uint64 `json:"uncached_input_tokens"`
	OutputTokens        *uint64 `json:"output_tokens"`
	ReasoningTokens     *uint64 `json:"reasoning_tokens"`
}

type UnknownOutcomeListResultV1 struct {
	SchemaVersion    string                            `json:"schema_version"`
	Scope            controlapicontract.ControlScopeV1 `json:"scope"`
	Items            []UnknownOutcomeProjectionV1      `json:"items"`
	HasMore          bool                              `json:"has_more"`
	ProjectionDigest string                            `json:"projection_digest"`
	StrongETag       string                            `json:"strong_etag"`
}

type UnknownOutcomeDetailResultV1 struct {
	SchemaVersion    string                            `json:"schema_version"`
	Scope            controlapicontract.ControlScopeV1 `json:"scope"`
	Item             UnknownOutcomeProjectionV1        `json:"item"`
	ProjectionDigest string                            `json:"projection_digest"`
	StrongETag       string                            `json:"strong_etag"`
}

type StoreManagementInputV1 struct {
	Authorization AuthorizationContextV1
	Scope         controlapicontract.ControlScopeV1
	Limit         uint16
	ObservedAt    uint64
}

type StoreManagementResultV1 struct {
	SchemaVersion    string                                `json:"schema_version"`
	Scope            controlapicontract.ControlScopeV1     `json:"scope"`
	Verification     StoreVerificationRecordV1             `json:"verification"`
	Backup           BackupManagementProjectionV1          `json:"backup"`
	Artifacts        []ModuleArtifactAdmissionProjectionV1 `json:"artifacts"`
	HasMore          bool                                  `json:"has_more"`
	ProjectionDigest string                                `json:"projection_digest"`
	StrongETag       string                                `json:"strong_etag"`
}

type controlManagementETagInputV1 struct {
	Session    controlapicontract.ControlSessionV1 `json:"session"`
	Scope      controlapicontract.ControlScopeV1   `json:"scope"`
	Projection string                              `json:"projection"`
}

func (service *ControlManagementServiceV1) ListUnknownOutcomesV1(
	ctx context.Context,
	input UnknownOutcomeListInputV1,
) (UnknownOutcomeListResultV1, error) {
	if service == nil || nilInterfaceV1(service.reader) || ctx == nil {
		return UnknownOutcomeListResultV1{}, ErrInvalidRequest
	}
	authorized, err := authorizeModulesRequestV1(input.Authorization, input.Scope, input.ObservedAt)
	if err != nil {
		return UnknownOutcomeListResultV1{}, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = DefaultModulesPageLimitV1
	}
	if limit < 1 || limit > MaximumUnknownOutcomeItemsV1 {
		return UnknownOutcomeListResultV1{}, ErrInvalidRequest
	}
	records, hasMore, err := service.reader.ListUnknownAttempts(
		ctx, authorized.scope.TenantID, workspaceForScopeV1(authorized.scope),
		int(MaximumUnknownOutcomeItemsV1)+1,
	)
	if err != nil {
		return UnknownOutcomeListResultV1{}, classifyReaderErrorV1(err, ErrStoreUnavailable)
	}
	if len(records) > int(MaximumUnknownOutcomeItemsV1)+1 {
		return UnknownOutcomeListResultV1{}, ErrResourceExhausted
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].UpdatedAt.Equal(records[right].UpdatedAt) {
			return records[left].AttemptID < records[right].AttemptID
		}
		return records[left].UpdatedAt.Before(records[right].UpdatedAt)
	})
	items := make([]UnknownOutcomeProjectionV1, 0, minIntV1(len(records), int(limit)))
	for _, record := range records {
		if !unknownRecordVisibleInScopeV1(record, authorized.scope) {
			continue
		}
		items = append(items, unknownOutcomeProjectionV1(record))
		if len(items) > int(limit) {
			hasMore = true
			items = items[:limit]
			break
		}
	}
	if len(records) <= int(limit) {
		hasMore = false
	}
	result := UnknownOutcomeListResultV1{
		SchemaVersion: UnknownOutcomeListSchemaVersionV1,
		Scope:         authorized.scope,
		Items:         items,
		HasMore:       hasMore,
	}
	result.ProjectionDigest, err = canonicalDigestStringV1(
		controlManagementProjectionDomainV1,
		struct {
			Scope   controlapicontract.ControlScopeV1 `json:"scope"`
			Items   []UnknownOutcomeProjectionV1      `json:"items"`
			HasMore bool                              `json:"has_more"`
		}{authorized.scope, result.Items, result.HasMore},
	)
	if err != nil {
		return UnknownOutcomeListResultV1{}, ErrIntegrityFailure
	}
	result.StrongETag, err = quotedDigestV1(
		controlManagementETagDomainV1,
		controlManagementETagInputV1{
			Session: authorized.session, Scope: authorized.scope,
			Projection: result.ProjectionDigest,
		},
	)
	if err != nil {
		return UnknownOutcomeListResultV1{}, ErrIntegrityFailure
	}
	if err := reauthorizeModulesRequestV1(input.Authorization, authorized); err != nil {
		return UnknownOutcomeListResultV1{}, err
	}
	return result, nil
}

func (service *ControlManagementServiceV1) GetUnknownOutcomeV1(
	ctx context.Context,
	input UnknownOutcomeDetailInputV1,
) (UnknownOutcomeDetailResultV1, error) {
	if service == nil || nilInterfaceV1(service.reader) || ctx == nil ||
		!validUnknownAttemptKindV1(input.Kind) || !validOpaqueIDV1(input.AttemptID) {
		return UnknownOutcomeDetailResultV1{}, ErrInvalidRequest
	}
	authorized, err := authorizeModulesRequestV1(input.Authorization, input.Scope, input.ObservedAt)
	if err != nil {
		return UnknownOutcomeDetailResultV1{}, err
	}
	record, err := service.reader.GetUnknownAttempt(
		ctx, authorized.scope.TenantID, workspaceForScopeV1(authorized.scope),
		input.Kind, input.AttemptID,
	)
	if err != nil {
		return UnknownOutcomeDetailResultV1{}, classifyReaderErrorV1(err, ErrStoreUnavailable)
	}
	if !unknownRecordVisibleInScopeV1(record, authorized.scope) {
		return UnknownOutcomeDetailResultV1{}, ErrNotFound
	}
	result := UnknownOutcomeDetailResultV1{
		SchemaVersion: UnknownOutcomeDetailSchemaVersionV1,
		Scope:         authorized.scope,
		Item:          unknownOutcomeProjectionV1(record),
	}
	result.ProjectionDigest, err = canonicalDigestStringV1(
		controlManagementProjectionDomainV1,
		struct {
			Scope controlapicontract.ControlScopeV1 `json:"scope"`
			Item  UnknownOutcomeProjectionV1        `json:"item"`
		}{authorized.scope, result.Item},
	)
	if err != nil {
		return UnknownOutcomeDetailResultV1{}, ErrIntegrityFailure
	}
	result.StrongETag, err = quotedDigestV1(
		controlManagementETagDomainV1,
		controlManagementETagInputV1{
			Session: authorized.session, Scope: authorized.scope,
			Projection: result.ProjectionDigest,
		},
	)
	if err != nil {
		return UnknownOutcomeDetailResultV1{}, ErrIntegrityFailure
	}
	if err := reauthorizeModulesRequestV1(input.Authorization, authorized); err != nil {
		return UnknownOutcomeDetailResultV1{}, err
	}
	return result, nil
}

func (service *ControlManagementServiceV1) GetStoreManagementV1(
	ctx context.Context,
	input StoreManagementInputV1,
) (StoreManagementResultV1, error) {
	if service == nil || nilInterfaceV1(service.reader) || ctx == nil {
		return StoreManagementResultV1{}, ErrInvalidRequest
	}
	authorized, err := authorizeModulesRequestV1(input.Authorization, input.Scope, input.ObservedAt)
	if err != nil {
		return StoreManagementResultV1{}, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = DefaultModulesPageLimitV1
	}
	if limit < 1 || limit > MaximumArtifactAdmissionItemsV1 {
		return StoreManagementResultV1{}, ErrInvalidRequest
	}
	verification, err := service.reader.VerifyStore(ctx)
	if err != nil {
		return StoreManagementResultV1{}, classifyReaderErrorV1(err, ErrStoreUnavailable)
	}
	admissions, hasMore, err := service.reader.ListArtifactAdmissions(
		ctx, authorized.scope.TenantID, workspaceForScopeV1(authorized.scope),
		int(MaximumArtifactAdmissionItemsV1)+1,
	)
	if err != nil {
		return StoreManagementResultV1{}, classifyReaderErrorV1(err, ErrStoreUnavailable)
	}
	if len(admissions) > int(MaximumArtifactAdmissionItemsV1)+1 {
		return StoreManagementResultV1{}, ErrResourceExhausted
	}
	artifacts := make([]ModuleArtifactAdmissionProjectionV1, 0, minIntV1(len(admissions), int(limit)))
	for _, admission := range admissions {
		artifacts = append(artifacts, admissionProjectionV1(admission))
		if len(artifacts) > int(limit) {
			hasMore = true
			artifacts = artifacts[:limit]
			break
		}
	}
	if len(admissions) <= int(limit) {
		hasMore = false
	}
	result := StoreManagementResultV1{
		SchemaVersion: StoreManagementSchemaVersionV1,
		Scope:         authorized.scope,
		Verification:  verification,
		Backup: BackupManagementProjectionV1{
			FormatVersion: "freeagent.current-store-backup/v1",
			State:         "VERIFIED",
			OnlineCreate:  false,
			OnlineRestore: false,
			RestoreMode:   "OFFLINE_STAGING_ATOMIC_PUBLISH",
		},
		Artifacts: artifacts,
		HasMore:   hasMore,
	}
	result.ProjectionDigest, err = canonicalDigestStringV1(
		controlManagementProjectionDomainV1,
		struct {
			Scope        controlapicontract.ControlScopeV1     `json:"scope"`
			Verification StoreVerificationRecordV1             `json:"verification"`
			Backup       BackupManagementProjectionV1          `json:"backup"`
			Artifacts    []ModuleArtifactAdmissionProjectionV1 `json:"artifacts"`
			HasMore      bool                                  `json:"has_more"`
		}{
			authorized.scope, result.Verification, result.Backup,
			result.Artifacts, result.HasMore,
		},
	)
	if err != nil {
		return StoreManagementResultV1{}, ErrIntegrityFailure
	}
	result.StrongETag, err = quotedDigestV1(
		controlManagementETagDomainV1,
		controlManagementETagInputV1{
			Session: authorized.session, Scope: authorized.scope,
			Projection: result.ProjectionDigest,
		},
	)
	if err != nil {
		return StoreManagementResultV1{}, ErrIntegrityFailure
	}
	if err := reauthorizeModulesRequestV1(input.Authorization, authorized); err != nil {
		return StoreManagementResultV1{}, err
	}
	return result, nil
}

type ControlManagementServiceV1 struct {
	reader ControlManagementReaderV1
}

func NewControlManagementServiceV1(
	reader ControlManagementReaderV1,
) (*ControlManagementServiceV1, error) {
	if nilInterfaceV1(reader) {
		return nil, ErrInvalidRequest
	}
	return &ControlManagementServiceV1{reader: reader}, nil
}

func workspaceForScopeV1(scope controlapicontract.ControlScopeV1) string {
	if scope.Kind == controlapicontract.ScopeWorkspaceV1 {
		return scope.WorkspaceID
	}
	return ""
}

func validUnknownAttemptKindV1(kind UnknownAttemptKindV1) bool {
	switch kind {
	case UnknownAttemptModelV1, UnknownAttemptActionV1, UnknownAttemptChannelV1:
		return true
	default:
		return false
	}
}

func unknownRecordVisibleInScopeV1(
	record UnknownAttemptRecordV1,
	scope controlapicontract.ControlScopeV1,
) bool {
	return record.TenantID == scope.TenantID &&
		(scope.Kind == controlapicontract.ScopeTenantV1 ||
			record.WorkspaceID == scope.WorkspaceID)
}

func unknownOutcomeProjectionV1(
	record UnknownAttemptRecordV1,
) UnknownOutcomeProjectionV1 {
	return UnknownOutcomeProjectionV1{
		Kind: record.Kind, AttemptID: record.AttemptID, RunID: record.RunID,
		TenantID: record.TenantID, WorkspaceID: record.WorkspaceID,
		State: record.State, Provider: record.Provider, Model: record.Model,
		ProviderRequestID:   record.ProviderRequestID,
		ExternalOperationID: record.ExternalOperationID, EndpointID: record.EndpointID,
		ErrorClassification: record.ErrorClassification, UnknownReason: record.UnknownReason,
		HasReconciliationEvidence: record.HasReconciliationEvidence,
		ReconciliationEvidenceRef: record.ReconciliationEvidenceRef,
		Revision:                  record.Revision, CreatedAtUnixMicros: unixMicrosV1(record.CreatedAt),
		UpdatedAtUnixMicros: unixMicrosV1(record.UpdatedAt),
		Usage: UnknownUsageProjectionV1{
			InputTokens: record.InputTokens, CachedInputTokens: record.CachedInputTokens,
			UncachedInputTokens: record.UncachedInputTokens, OutputTokens: record.OutputTokens,
			ReasoningTokens: record.ReasoningTokens,
		},
	}
}
