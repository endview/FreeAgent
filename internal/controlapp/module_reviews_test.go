package controlapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

type reviewReaderFixtureV1 struct {
	records    []ModuleUpgradeReviewRecordV1
	decisions  map[string]ModuleCandidateDecisionRecordV1
	admissions map[string]ModuleArtifactAdmissionRecordV1
	listTenant string
	listLimit  uint16
}

func (reader *reviewReaderFixtureV1) ListModuleUpgradeReviews(
	_ context.Context,
	tenantID string,
	limit uint16,
) ([]ModuleUpgradeReviewRecordV1, error) {
	reader.listTenant = tenantID
	reader.listLimit = limit
	return append([]ModuleUpgradeReviewRecordV1(nil), reader.records...), nil
}

func (reader *reviewReaderFixtureV1) GetModuleUpgradeReview(
	_ context.Context,
	reviewID string,
) (ModuleUpgradeReviewRecordV1, error) {
	for _, record := range reader.records {
		if record.ReviewID == reviewID {
			return record, nil
		}
	}
	return ModuleUpgradeReviewRecordV1{}, ErrNotFound
}

func (reader *reviewReaderFixtureV1) GetModuleCandidateDecision(
	_ context.Context,
	reviewID string,
) (ModuleCandidateDecisionRecordV1, error) {
	if decision, ok := reader.decisions[reviewID]; ok {
		return decision, nil
	}
	return ModuleCandidateDecisionRecordV1{}, ErrNotFound
}

func (reader *reviewReaderFixtureV1) GetModuleArtifactAdmission(
	_ context.Context,
	admissionID string,
	_ string,
) (ModuleArtifactAdmissionRecordV1, error) {
	if admission, ok := reader.admissions[admissionID]; ok {
		return admission, nil
	}
	return ModuleArtifactAdmissionRecordV1{}, ErrNotFound
}

func reviewProjectionFixtureV1(
	reviewID string,
	target ModuleUpgradeReviewBindingTargetV1,
) ModuleUpgradeReviewRecordV1 {
	return ModuleUpgradeReviewRecordV1{
		ReviewID: reviewID,
		Review: ModuleUpgradeReviewProjectionV1{
			SchemaVersion:           "module-upgrade-review/v1",
			CandidateID:             testHashV1("a"),
			ReviewKey:               testHashV1("b"),
			TenantID:                testTenantIDV1,
			ArtifactAdmissionID:     testHashV1("c"),
			OperatorPrincipalID:     "operator-a",
			ReviewRequestDigest:     testHashV1("d"),
			BindingTarget:           target,
			Port:                    moduleapi.PortRef{Name: "context.provide", ExactVersion: "v1"},
			PortBindingIndex:        0,
			TargetInstanceID:        "instance-target",
			TargetModule:            moduleapi.Ref{ID: "fixture.module", Version: "1.0.0"},
			TargetArtifactDigest:    testHashV1("e"),
			TargetArtifactSizeBytes: 128,
			Conclusion:              "WOULD_APPLY",
			ReasonCodes:             []string{"OPERATOR_GRANT_REQUIRED"},
		},
		CreatedAt: time.UnixMicro(1_500),
	}
}

func reviewAdmissionFixtureV1(review ModuleUpgradeReviewRecordV1) ModuleArtifactAdmissionRecordV1 {
	return ModuleArtifactAdmissionRecordV1{
		AdmissionID:                 review.Review.ArtifactAdmissionID,
		SourceID:                    "local-source",
		SourcePolicyID:              testHashV1("f"),
		SourcePolicyRevision:        4,
		SnapshotID:                  testHashV1("0"),
		SnapshotObservationRevision: 5,
		EntryOrdinal:                2,
		Module:                      review.Review.TargetModule,
		Artifact: ModuleArtifactProjectionV1{
			ArtifactDigest:    review.Review.TargetArtifactDigest,
			Module:            review.Review.TargetModule,
			ManifestRef:       testHashV1("1"),
			ArtifactSizeBytes: review.Review.TargetArtifactSizeBytes,
			CoveredFileCount:  3,
		},
		AdmittedAt: time.UnixMicro(1_600),
	}
}

func TestModuleUpgradeReviewServiceScopesListAndBoundsReader(t *testing.T) {
	tenantReview := reviewProjectionFixtureV1(testHashV1("1"), ModuleUpgradeReviewBindingTargetV1{Kind: "PROFILE", ProfileID: "profile-a"})
	workspaceReview := reviewProjectionFixtureV1(testHashV1("2"), ModuleUpgradeReviewBindingTargetV1{Kind: "WORKSPACE_CHANNEL_ENDPOINT", WorkspaceID: testWorkspaceAV1, EndpointID: "endpoint-a"})
	reader := &reviewReaderFixtureV1{records: []ModuleUpgradeReviewRecordV1{workspaceReview, tenantReview}, admissions: map[string]ModuleArtifactAdmissionRecordV1{}}
	service, err := NewModuleUpgradeReviewServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	accessContext := newTestAuthorizationV1("operator-a", testTenantScopeV1(), testWorkspaceScopeV1(testWorkspaceAV1))

	result, err := service.ListModuleUpgradeReviewsV1(context.Background(), ModuleUpgradeReviewListInputV1{
		Authorization: accessContext,
		Scope:         testTenantScopeV1(),
		Limit:         2,
		ObservedAt:    testObservedAtV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reader.listTenant != testTenantIDV1 || reader.listLimit != MaximumModuleUpgradeReviewItemsV1+1 {
		t.Fatalf("reader call = tenant %q limit %d", reader.listTenant, reader.listLimit)
	}
	if len(result.Items) != 2 || result.Items[0].ReviewID != tenantReview.ReviewID || result.Items[1].ReviewID != workspaceReview.ReviewID {
		t.Fatalf("tenant result = %#v", result.Items)
	}

	result, err = service.ListModuleUpgradeReviewsV1(context.Background(), ModuleUpgradeReviewListInputV1{
		Authorization: accessContext,
		Scope:         testWorkspaceScopeV1(testWorkspaceAV1),
		Limit:         2,
		ObservedAt:    testObservedAtV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ReviewID != workspaceReview.ReviewID {
		t.Fatalf("workspace result = %#v", result.Items)
	}
}

func TestModuleUpgradeReviewServiceDetailProjectsAdmissionAndDecision(t *testing.T) {
	review := reviewProjectionFixtureV1(testHashV1("3"), ModuleUpgradeReviewBindingTargetV1{Kind: "PROFILE", ProfileID: "profile-a"})
	reader := &reviewReaderFixtureV1{
		records:    []ModuleUpgradeReviewRecordV1{review},
		admissions: map[string]ModuleArtifactAdmissionRecordV1{},
		decisions:  map[string]ModuleCandidateDecisionRecordV1{},
	}
	reader.admissions[review.Review.ArtifactAdmissionID] = reviewAdmissionFixtureV1(review)
	reader.decisions[review.ReviewID] = ModuleCandidateDecisionRecordV1{
		DecisionID: testHashV1("4"),
		Decision: moduleapi.ModuleCandidateDecisionV1{
			Decision:            moduleapi.ModuleCandidateDecisionApproveV1,
			OperatorPrincipalID: "operator-b",
			Reason:              "reviewed",
		},
		DecidedAt: time.UnixMicro(1_700),
	}
	service, err := NewModuleUpgradeReviewServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	accessContext := newTestAuthorizationV1("operator-a", testTenantScopeV1())
	result, err := service.GetModuleUpgradeReviewV1(context.Background(), GetModuleUpgradeReviewInputV1{
		Authorization: accessContext,
		Scope:         testTenantScopeV1(),
		ReviewID:      review.ReviewID,
		ObservedAt:    testObservedAtV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Admission.SourceID != "local-source" || result.Admission.Module != review.Review.TargetModule {
		t.Fatalf("admission projection = %#v", result.Admission)
	}
	if result.Decision == nil || result.Decision.Decision != moduleapi.ModuleCandidateDecisionApproveV1 {
		t.Fatalf("decision projection = %#v", result.Decision)
	}
	if result.Artifact.ManifestRef == "" || result.Artifact.ArtifactSizeBytes != 128 {
		t.Fatalf("artifact projection = %#v", result.Artifact)
	}
}

func TestModuleUpgradeReviewServiceRejectsCrossTenantDetail(t *testing.T) {
	review := reviewProjectionFixtureV1(testHashV1("5"), ModuleUpgradeReviewBindingTargetV1{Kind: "PROFILE", ProfileID: "profile-a"})
	review.Review.TenantID = "tenant-other"
	reader := &reviewReaderFixtureV1{records: []ModuleUpgradeReviewRecordV1{review}}
	service, err := NewModuleUpgradeReviewServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	accessContext := newTestAuthorizationV1("operator-a", testTenantScopeV1())
	_, err = service.GetModuleUpgradeReviewV1(context.Background(), GetModuleUpgradeReviewInputV1{
		Authorization: accessContext,
		Scope:         testTenantScopeV1(),
		ReviewID:      review.ReviewID,
		ObservedAt:    testObservedAtV1,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant error = %v", err)
	}
}

var _ ModuleUpgradeReviewReaderV1 = (*reviewReaderFixtureV1)(nil)
