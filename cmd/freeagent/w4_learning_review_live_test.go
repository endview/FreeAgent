package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const w4LearningReviewLiveEnabledEnvironment = "FREEAGENT_W4_L2_LIVE"

// TestW4L2LiveDeepSeekLearningReview is deliberately opt-in. It proves the
// real-provider path through the production composition, an ordinary proposer
// Run, immutable Proposal admission, an independent Reviewer Agent/Profile,
// LearningReviewService, Universal Loop, Model Attempt/Usage, and Store-owned
// review finalization. It never reads a credential value into test output and
// t.TempDir owns every database and artifact produced by the run.
func TestW4L2LiveDeepSeekLearningReview(t *testing.T) {
	if os.Getenv(w4LearningReviewLiveEnabledEnvironment) != "1" {
		t.Skip("set FREEAGENT_W4_L2_LIVE=1 to run the explicit live review")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.deepseek.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize W4-L2 live data: %v", err)
	}
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{DeepSeek: &productionDeepSeekRuntimeConfig{
			APIKeyResolver: &environmentDeepSeekAPIKeyResolver{
				name:   defaultDeepSeekAPIKeyEnvironment,
				lookup: os.LookupEnv,
			},
		}},
	)
	if err != nil {
		t.Fatalf("open W4-L2 live composition: %v", err)
	}
	defer func() {
		if closeErr := composition.Close(); closeErr != nil {
			t.Errorf("close W4-L2 live composition: %v", closeErr)
		}
	}()

	deadline := time.Now().UTC().Round(0).Add(3 * time.Minute)
	proposer, err := composition.chat.Chat(ctx, localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: defaultPrincipalID,
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "deepseek-chat",
		Message: "State one concise reason why immutable content digests are " +
			"useful for auditing an agent knowledge proposal.",
		RequestID: "w4-l2-live-proposer-v1",
		Deadline:  deadline,
	})
	if err != nil || proposer.TerminalResult == nil || proposer.Reply == "" {
		t.Fatalf("execute live proposer Run: result=%+v error=%v", proposer, err)
	}

	proposerRun := loadW4LiveRun(t, ctx, composition.store, proposer.RunID)
	proposerDispatch, err := composition.store.GetModelDispatchRecord(
		ctx,
		proposer.TerminalResult.AttemptID,
	)
	if err != nil {
		t.Fatalf("load proposer Model Attempt: %v", err)
	}
	_, draftCanonical, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text: "Immutable content digests let an auditor detect whether a " +
				"reviewed knowledge candidate changed after submission.",
		},
	)
	if err != nil {
		t.Fatalf("construct bounded live Draft: %v", err)
	}
	submitted, err := composition.store.SubmitStaticSkillProposal(
		ctx,
		currentstore.SubmitStaticSkillProposalInput{
			Proposal: learningcontract.ProposalV1{
				SchemaVersion:          learningcontract.ProposalSchemaVersionV1,
				Kind:                   learningcontract.ProposalKindSkillV1,
				TenantID:               defaultTenantID,
				Workspace:              proposerRun.Manifest.Workspace,
				ProposerAgent:          proposerRun.Member.Agent,
				ProposerProfile:        proposerRun.Member.Profile,
				ProposerRunID:          proposerRun.RunID,
				ProposerManifestDigest: proposerRun.Manifest.ManifestDigest,
				ProposerMember:         proposerRun.Manifest.Members[0],
				ProposerResultRef:      proposerDispatch.Attempt.ResultRef,
				Target: moduleapi.Ref{
					ID:      "freeagent.live.audit-digest-skill",
					Version: "v1",
				},
			},
			DraftCanonical: draftCanonical,
			SourceEvidence: learningcontract.SourceEvidenceV1{
				Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
			},
		},
	)
	if err != nil || !submitted.Created {
		t.Fatalf("submit live Learning Proposal: result=%+v error=%v", submitted, err)
	}

	reviewerAgentID, reviewerProfileID := publishW4LiveReviewer(
		t,
		ctx,
		composition.store,
	)
	reviewService, err := localchat.NewLearningReviewService(composition.chat)
	if err != nil {
		t.Fatalf("construct LearningReviewService: %v", err)
	}
	reviewInput := localchat.LearningReviewInput{
		TenantID:          defaultTenantID,
		PrincipalID:       defaultPrincipalID,
		ProposalID:        submitted.Record.ProposalID,
		ReviewerAgentID:   reviewerAgentID,
		ReviewerProfileID: reviewerProfileID,
		ReviewID:          "w4-l2-live-review-v1",
		Deadline:          deadline,
		MaxOutputTokens:   512,
	}
	first, err := reviewService.Review(ctx, reviewInput)
	if err != nil {
		t.Fatalf("execute live Learning review: %v", err)
	}
	if first.RunID == proposer.RunID || first.Proposal.ReviewerAttemptID == "" ||
		(first.Proposal.State != currentstore.LearningProposalApproved &&
			first.Proposal.State != currentstore.LearningProposalRejected) ||
		first.Proposal.Revision != 2 {
		t.Fatalf("live review did not produce a valid Store-owned verdict: %+v", first)
	}
	reviewerRun := loadW4LiveRun(t, ctx, composition.store, first.RunID)
	if len(reviewerRun.Manifest.Members) != 1 ||
		reviewerRun.Member.Agent.ID != reviewerAgentID ||
		reviewerRun.Member.Profile.ID != reviewerProfileID ||
		reviewerRun.Member.Agent.ID == proposerRun.Member.Agent.ID ||
		reviewerRun.Member.Profile.ID == proposerRun.Member.Profile.ID ||
		len(reviewerRun.Member.PortPlans) != 1 ||
		reviewerRun.Member.PortPlans[0].Port != productionModelPort {
		t.Fatalf("reviewer is not an independent model-only ordinary Run: %+v", reviewerRun)
	}
	reviewerDispatch, err := composition.store.GetModelDispatchRecord(
		ctx,
		first.Proposal.ReviewerAttemptID,
	)
	if err != nil {
		t.Fatalf("load reviewer Model Attempt/Usage: %v", err)
	}
	if reviewerDispatch.Attempt.RunID != first.RunID ||
		reviewerDispatch.Attempt.State != corecontract.ModelAttemptSucceeded ||
		reviewerDispatch.Attempt.Model != "deepseek-v4-flash" ||
		reviewerDispatch.Attempt.Provider != "deepseek" ||
		reviewerDispatch.Attempt.ProviderRequestID == "" ||
		reviewerDispatch.Usage.RunID != first.RunID ||
		reviewerDispatch.Usage.AttemptID != first.Proposal.ReviewerAttemptID ||
		reviewerDispatch.Usage.Tokens.Input == nil ||
		reviewerDispatch.Usage.Tokens.Output == nil ||
		reviewerDispatch.Usage.RawReceiptRef == "" ||
		reviewerDispatch.Usage.UsageStatus != "PROVIDER_REPORTED" {
		t.Fatalf("reviewer dispatch is not the exact ordinary Run: %+v", reviewerDispatch)
	}

	retry, err := reviewService.Review(ctx, reviewInput)
	if err != nil {
		t.Fatalf("exact live review retry: %v", err)
	}
	if retry.AdmissionCreated || retry.FinalizeApplied || retry.RunID != first.RunID ||
		retry.Proposal.ReviewerAttemptID != first.Proposal.ReviewerAttemptID ||
		retry.Proposal.Revision != first.Proposal.Revision ||
		!reflect.DeepEqual(retry.Proposal.State, first.Proposal.State) {
		t.Fatalf("exact retry changed review authority: first=%+v retry=%+v", first, retry)
	}

	report, err := json.Marshal(struct {
		ProposalID         string                   `json:"proposal_id"`
		ProposalState      string                   `json:"proposal_state"`
		ProposalRevision   uint64                   `json:"proposal_revision"`
		ProposerRunID      string                   `json:"proposer_run_id"`
		ReviewerRunID      string                   `json:"reviewer_run_id"`
		ReviewerAttemptID  string                   `json:"reviewer_attempt_id"`
		ReviewerAttempt    string                   `json:"reviewer_attempt_state"`
		UsageStatus        string                   `json:"usage_status"`
		Tokens             corecontract.UsageTokens `json:"tokens"`
		ExactRetryNoReplay bool                     `json:"exact_retry_no_replay"`
	}{
		ProposalID:         first.Proposal.ProposalID,
		ProposalState:      string(first.Proposal.State),
		ProposalRevision:   first.Proposal.Revision,
		ProposerRunID:      proposer.RunID,
		ReviewerRunID:      first.RunID,
		ReviewerAttemptID:  first.Proposal.ReviewerAttemptID,
		ReviewerAttempt:    string(reviewerDispatch.Attempt.State),
		UsageStatus:        reviewerDispatch.Usage.UsageStatus,
		Tokens:             reviewerDispatch.Usage.Tokens,
		ExactRetryNoReplay: true,
	})
	if err != nil {
		t.Fatalf("marshal sanitized live report: %v", err)
	}
	t.Logf("W4_L2_LIVE_RESULT %s", report)
}

func loadW4LiveRun(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	runID string,
) currentstore.RunForLoop {
	t.Helper()
	lease, err := store.AcquireCurrentRunLease(ctx, currentstore.AcquireCurrentRunLeaseInput{
		RunID:   runID,
		OwnerID: "w4-l2-live-proposer-reader",
		TTL:     time.Minute,
	})
	if err != nil {
		t.Fatalf("acquire proposer read lease: %v", err)
	}
	run, loadErr := store.LoadRunForLoop(ctx, lease)
	releaseErr := store.ReleaseRunLease(ctx, lease)
	if loadErr != nil || releaseErr != nil {
		t.Fatalf("load/release proposer Run: load=%v release=%v", loadErr, releaseErr)
	}
	return run
}

func publishW4LiveReviewer(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
) (string, string) {
	t.Helper()
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatalf("load live Control/Catalog: %v", err)
	}
	if len(control.Profiles) != 1 || len(control.Profiles[0].Bindings) != 1 {
		t.Fatalf("live base profile is not model-only: %+v", control.Profiles)
	}
	const (
		reviewerAgentID   = "w4-live-reviewer-agent"
		reviewerProfileID = "w4-live-reviewer-profile"
	)
	profile := control.Profiles[0]
	profile.Profile = corecontract.ProfileRef{
		ID: reviewerProfileID, Version: "v1", Digest: strings.Repeat("9", 64),
	}
	_, configCanonical, err := moduleapi.NewModelBindingConfigV2(
		moduleapi.ModelBindingConfigV2{
			SchemaVersion: moduleapi.ModelBindingConfigSchemaV2,
			Provider:      "deepseek",
			Model:         "deepseek-v4-flash",
			ModelBuildID:  localDeepSeekFlashBuild,
			Parameters: json.RawMessage(
				`{"max_tokens":512,"response_format":{"type":"json_object"},"temperature":0,"thinking":{"type":"disabled"}}`,
			),
		},
	)
	if err != nil {
		t.Fatalf("freeze live reviewer binding Config: %v", err)
	}
	profile.Bindings[0].ConfigRef = putW4LiveContent(
		t,
		ctx,
		store,
		currentstore.ContentConfig,
		configCanonical,
	)
	control.SnapshotID = "control-w4-l2-live-reviewer"
	control.Revision++
	control.Digest = ""
	control.Agents = append(control.Agents, corecontract.AgentRef{
		ID: reviewerAgentID, Version: "v1", Digest: strings.Repeat("8", 64),
	})
	control.Profiles = append(control.Profiles, profile)
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze live reviewer Control: %v", err)
	}
	catalog.GenerationID = "catalog-w4-l2-live-reviewer"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze live reviewer Catalog: %v", err)
	}
	if _, err := store.PublishControlCatalog(ctx, currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: basis.PointerRevision,
		NewPointerRevision:      basis.PointerRevision + 1,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	}); err != nil {
		t.Fatalf("publish live reviewer Control/Catalog: %v", err)
	}
	return reviewerAgentID, reviewerProfileID
}

func putW4LiveContent(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	body []byte,
) string {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON(body)
	if err != nil {
		t.Fatalf("canonicalize live %s: %v", kind, err)
	}
	const mediaType = "application/json"
	digest, err := currentstore.ComputeContentDigest(kind, mediaType, canonical)
	if err != nil {
		t.Fatalf("digest live %s: %v", kind, err)
	}
	if _, err := store.PutContent(ctx, currentstore.ContentInput{
		Digest: digest, Kind: kind, MediaType: mediaType, CanonicalBytes: canonical,
	}); err != nil {
		t.Fatalf("put live %s: %v", kind, err)
	}
	return digest
}
