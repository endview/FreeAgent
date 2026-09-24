package controlapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

type managementReaderFixtureV1 struct {
	unknown          []UnknownAttemptRecordV1
	unknownListErr   error
	unknownDetail    UnknownAttemptRecordV1
	unknownDetailErr error
	admissions       []ModuleArtifactAdmissionRecordV1
	admissionListErr error
	verification     StoreVerificationRecordV1
	verifyErr        error
	listTenant       string
	listWorkspace    string
	listLimit        int
}

func (reader *managementReaderFixtureV1) ListUnknownAttempts(
	_ context.Context,
	tenantID string,
	workspaceID string,
	limit int,
) ([]UnknownAttemptRecordV1, bool, error) {
	reader.listTenant = tenantID
	reader.listWorkspace = workspaceID
	reader.listLimit = limit
	if reader.unknownListErr != nil {
		return nil, false, reader.unknownListErr
	}
	return append([]UnknownAttemptRecordV1(nil), reader.unknown...), false, nil
}

func (reader *managementReaderFixtureV1) GetUnknownAttempt(
	_ context.Context,
	_ string,
	_ string,
	_ UnknownAttemptKindV1,
	_ string,
) (UnknownAttemptRecordV1, error) {
	if reader.unknownDetailErr != nil {
		return UnknownAttemptRecordV1{}, reader.unknownDetailErr
	}
	return reader.unknownDetail, nil
}

func (reader *managementReaderFixtureV1) ListArtifactAdmissions(
	_ context.Context,
	tenantID string,
	workspaceID string,
	limit int,
) ([]ModuleArtifactAdmissionRecordV1, bool, error) {
	reader.listTenant = tenantID
	reader.listWorkspace = workspaceID
	reader.listLimit = limit
	if reader.admissionListErr != nil {
		return nil, false, reader.admissionListErr
	}
	return append([]ModuleArtifactAdmissionRecordV1(nil), reader.admissions...), false, nil
}

func (reader *managementReaderFixtureV1) VerifyStore(
	_ context.Context,
) (StoreVerificationRecordV1, error) {
	if reader.verifyErr != nil {
		return StoreVerificationRecordV1{}, reader.verifyErr
	}
	return reader.verification, nil
}

func managementUnknownRecordV1(
	kind UnknownAttemptKindV1,
	attemptID string,
	tenantID string,
	workspaceID string,
) UnknownAttemptRecordV1 {
	input := uint64(17)
	return UnknownAttemptRecordV1{
		Kind: kind, AttemptID: attemptID, RunID: "run-" + attemptID,
		TenantID: tenantID, WorkspaceID: workspaceID, State: "UNKNOWN",
		Provider: "zhipu", Model: "glm-4.5",
		ErrorClassification: "MAY_HAVE_BEEN_DELIVERED",
		UnknownReason:       "provider response was not observed",
		Revision:            2, CreatedAt: time.UnixMicro(1_000),
		UpdatedAt: time.UnixMicro(2_000), InputTokens: &input,
	}
}

func TestControlManagementServiceScopesUnknownAndPreservesNullableUsageV1(t *testing.T) {
	t.Parallel()
	reader := &managementReaderFixtureV1{
		unknown: []UnknownAttemptRecordV1{
			managementUnknownRecordV1(
				UnknownAttemptModelV1, "attempt-b", testTenantIDV1, testWorkspaceBV1,
			),
			managementUnknownRecordV1(
				UnknownAttemptKindV1("INVALID"), "attempt-other", "tenant-b", "",
			),
			managementUnknownRecordV1(
				UnknownAttemptActionV1, "attempt-a", testTenantIDV1, testWorkspaceAV1,
			),
		},
	}
	service, err := NewControlManagementServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	scope := testTenantScopeV1()
	accessContext := newTestAuthorizationV1("operator-a", scope)
	result, err := service.ListUnknownOutcomesV1(context.Background(), UnknownOutcomeListInputV1{
		Authorization: accessContext,
		Scope:         scope, Limit: 1, ObservedAt: testObservedAtV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reader.listTenant != testTenantIDV1 || reader.listWorkspace != "" ||
		reader.listLimit != int(MaximumUnknownOutcomeItemsV1)+1 {
		t.Fatalf("reader scope/limit = %q/%q/%d", reader.listTenant, reader.listWorkspace, reader.listLimit)
	}
	if len(result.Items) != 1 || result.Items[0].TenantID != testTenantIDV1 ||
		!result.HasMore || result.Items[0].Usage.CachedInputTokens != nil ||
		result.Items[0].Usage.OutputTokens != nil {
		t.Fatalf("unknown projection = %#v", result.Items)
	}
	if !moduleapi.ValidSHA256(result.ProjectionDigest) ||
		!validStrongETagForTestV1(result.StrongETag) {
		t.Fatalf("invalid result integrity fields: %#v", result)
	}
}

func TestControlManagementServiceRejectsInvalidAndCrossScopeUnknownDetailV1(t *testing.T) {
	t.Parallel()
	reader := &managementReaderFixtureV1{
		unknownDetail: managementUnknownRecordV1(
			UnknownAttemptModelV1, "attempt-a", testTenantIDV1, testWorkspaceBV1,
		),
	}
	service, err := NewControlManagementServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	scope := testWorkspaceScopeV1(testWorkspaceAV1)
	accessContext := newTestAuthorizationV1("operator-a", scope)
	_, err = service.GetUnknownOutcomeV1(context.Background(), UnknownOutcomeDetailInputV1{
		Authorization: accessContext,
		Scope:         scope, Kind: UnknownAttemptModelV1, AttemptID: " ",
		ObservedAt: testObservedAtV1,
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid attempt ID error = %v", err)
	}
	_, err = service.GetUnknownOutcomeV1(context.Background(), UnknownOutcomeDetailInputV1{
		Authorization: accessContext,
		Scope:         scope, Kind: UnknownAttemptModelV1, AttemptID: "attempt-a",
		ObservedAt: testObservedAtV1,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace detail error = %v", err)
	}
	privateStoreLocation := "private store path: " + string([]byte{67, 58, 92}) + "secret" + string([]byte{92}) + "store.db"
	reader.unknownDetailErr = errors.New(privateStoreLocation)
	tenantAccessContext := newTestAuthorizationV1("operator-a", testTenantScopeV1())
	_, err = service.GetUnknownOutcomeV1(context.Background(), UnknownOutcomeDetailInputV1{
		Authorization: tenantAccessContext,
		Scope:         testTenantScopeV1(), Kind: UnknownAttemptModelV1, AttemptID: "attempt-a",
		ObservedAt: testObservedAtV1,
	})
	if !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("reader error = %v", err)
	}
}

func TestControlManagementServiceProjectsStoreVerificationAndArtifactsReadOnlyV1(t *testing.T) {
	t.Parallel()
	reader := &managementReaderFixtureV1{
		verification: StoreVerificationRecordV1{
			StoreInstanceID: "store-a", SchemaIdentity: "current-store-v2",
			SchemaVersion: 2, SchemaFingerprint: testHashV1("a"), GeneratorID: "generator-a",
		},
		admissions: []ModuleArtifactAdmissionRecordV1{{
			AdmissionID: testHashV1("b"), SourceID: "local-source",
			SourcePolicyID: testHashV1("c"), SourcePolicyRevision: 3,
			SnapshotID: testHashV1("d"), SnapshotObservationRevision: 4,
			EntryOrdinal: 1, Module: moduleapi.Ref{ID: "fixture.module", Version: "1.0.0"},
			Artifact: ModuleArtifactProjectionV1{
				ArtifactDigest: testHashV1("e"),
				Module:         moduleapi.Ref{ID: "fixture.module", Version: "1.0.0"},
				ManifestRef:    testHashV1("f"), ArtifactSizeBytes: 128, CoveredFileCount: 2,
			},
			AdmittedAt: time.UnixMicro(3_000),
		}},
	}
	service, err := NewControlManagementServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	scope := testTenantScopeV1()
	accessContext := newTestAuthorizationV1("operator-a", scope)
	result, err := service.GetStoreManagementV1(context.Background(), StoreManagementInputV1{
		Authorization: accessContext,
		Scope:         scope, Limit: 2, ObservedAt: testObservedAtV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verification.SchemaIdentity != "current-store-v2" ||
		len(result.Artifacts) != 1 ||
		result.Backup.OnlineCreate || result.Backup.OnlineRestore ||
		result.Backup.RestoreMode != "OFFLINE_STAGING_ATOMIC_PUBLISH" ||
		reader.listLimit != int(MaximumArtifactAdmissionItemsV1)+1 {
		t.Fatalf("store management result = %#v", result)
	}
	if !moduleapi.ValidSHA256(result.ProjectionDigest) ||
		!validStrongETagForTestV1(result.StrongETag) {
		t.Fatalf("invalid store integrity fields: %#v", result)
	}
}

func validStrongETagForTestV1(value string) bool {
	return len(value) == 66 && value[0] == '"' && value[len(value)-1] == '"' &&
		moduleapi.ValidSHA256(value[1:len(value)-1])
}

var _ ControlManagementReaderV1 = (*managementReaderFixtureV1)(nil)
