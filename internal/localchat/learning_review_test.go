package localchat

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestLearningReviewServiceUsesOneOrdinaryRunAndExactRetryDoesNotReplay(
	t *testing.T,
) {
	fixture := newLearningReviewServiceFixture(t)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	input := LearningReviewInput{
		TenantID:          fixture.chat.tenantID,
		PrincipalID:       "principal-learning-review",
		ProposalID:        fixture.proposal.ProposalID,
		ReviewerAgentID:   fixture.reviewerAgentID,
		ReviewerProfileID: fixture.reviewerProfileID,
		ReviewID:          "review-request-stable",
		Deadline:          deadline,
		MaxOutputTokens:   512,
	}

	first, err := fixture.service.Review(context.Background(), input)
	if err != nil {
		t.Fatalf("first Review: %v", err)
	}
	if !first.AdmissionCreated || !first.FinalizeApplied ||
		first.RunID == "" || first.ProposalID != input.ProposalID ||
		first.Proposal.State != currentstore.LearningProposalApproved ||
		first.Proposal.Revision != 2 ||
		first.Proposal.ReviewerAttemptID == "" ||
		first.LoopResult.Disposition != loopapi.DispositionTerminated {
		t.Fatalf("first Learning Review=%+v", first)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("first review model calls=%d want 1", got)
	}

	second, err := fixture.service.Review(context.Background(), input)
	if err != nil {
		t.Fatalf("exact Review retry: %v", err)
	}
	if second.AdmissionCreated || second.FinalizeApplied ||
		second.RunID != first.RunID ||
		second.Proposal.ProposalID != first.Proposal.ProposalID ||
		second.Proposal.State != currentstore.LearningProposalApproved ||
		second.Proposal.Revision != first.Proposal.Revision ||
		second.Proposal.ReviewerAttemptID != first.Proposal.ReviewerAttemptID {
		t.Fatalf("retry=%+v first=%+v", second, first)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("exact retry replayed Reviewer model: calls=%d", got)
	}

	requestCanonical := fixture.invoker.onlyInput(t)
	modelRequest, err := moduleapi.RestoreModelGenerateRequestV1(requestCanonical)
	if err != nil {
		t.Fatalf("RestoreModelGenerateRequestV1: %v", err)
	}
	if len(modelRequest.Messages) == 0 {
		t.Fatal("Reviewer model request has no TaskInput message")
	}
	last := modelRequest.Messages[len(modelRequest.Messages)-1]
	if last.Role != moduleapi.ModelRoleUser {
		t.Fatalf("Reviewer last message role=%q", last.Role)
	}
	reviewRequest, _, _, err := learningcontract.ParseReviewRequestV1(
		[]byte(last.Content),
	)
	if err != nil {
		t.Fatalf("ParseReviewRequestV1(model message): %v", err)
	}
	if reviewRequest.ProposalID != fixture.proposal.ProposalID ||
		reviewRequest.Instructions != learningcontract.ReviewInstructionsV1 ||
		reviewRequest.MaxOutputTokens != input.MaxOutputTokens ||
		!bytes.Equal(reviewRequest.ProposalCanonical, fixture.proposal.ProposalCanonical) ||
		!bytes.Equal(reviewRequest.DraftCanonical, fixture.proposal.DraftCanonical) {
		t.Fatalf("model ReviewRequest=%+v", reviewRequest)
	}
	for _, forbidden := range []string{
		input.ReviewID,
		first.RunID,
		"learning-review-member-",
		"learning-review-recovery-",
		deadline.Format(time.RFC3339Nano),
	} {
		if bytes.Contains(requestCanonical, []byte(forbidden)) {
			t.Fatalf("dynamic review identity %q leaked into model request", forbidden)
		}
	}

	dispatch, err := fixture.chat.store.GetModelDispatchRecord(
		context.Background(),
		first.Proposal.ReviewerAttemptID,
	)
	if err != nil || dispatch.Attempt.RunID != first.RunID ||
		dispatch.Attempt.State != corecontract.ModelAttemptSucceeded ||
		dispatch.Usage.RunID != first.RunID {
		t.Fatalf("ordinary Reviewer dispatch=%+v error=%v", dispatch, err)
	}
}

func TestLearningReviewServiceRejectsUnstableIdentityBeforeAdmission(
	t *testing.T,
) {
	fixture := newLearningReviewServiceFixture(t)
	result, err := fixture.service.Review(
		context.Background(),
		LearningReviewInput{
			TenantID:          fixture.chat.tenantID,
			PrincipalID:       "principal-learning-review",
			ProposalID:        fixture.proposal.ProposalID,
			ReviewerAgentID:   fixture.reviewerAgentID,
			ReviewerProfileID: fixture.reviewerProfileID,
			ReviewID:          "explicit-review-without-deadline",
		},
	)
	if err == nil || result.ReviewID != "explicit-review-without-deadline" ||
		result.RunID != "" {
		t.Fatalf("unstable identity result=%+v error=%v", result, err)
	}
	if got := fixture.invoker.callCount(); got != 0 {
		t.Fatalf("invalid identity invoked Reviewer model %d times", got)
	}
	proposal, getErr := fixture.chat.store.GetLearningProposal(
		context.Background(),
		fixture.chat.tenantID,
		fixture.proposal.ProposalID,
	)
	if getErr != nil || proposal.State != currentstore.LearningProposalSubmitted ||
		proposal.Revision != 0 || proposal.ReviewRunID != "" {
		t.Fatalf("invalid identity changed Proposal=%+v error=%v", proposal, getErr)
	}
}

func TestLearningReviewServiceUnknownNeverReplaysReviewerModel(
	t *testing.T,
) {
	fixture := newLearningReviewServiceFixture(t)
	fixture.invoker.unknown = true
	input := LearningReviewInput{
		TenantID:          fixture.chat.tenantID,
		PrincipalID:       "principal-learning-review-unknown",
		ProposalID:        fixture.proposal.ProposalID,
		ReviewerAgentID:   fixture.reviewerAgentID,
		ReviewerProfileID: fixture.reviewerProfileID,
		ReviewID:          "review-request-unknown-stable",
		Deadline:          time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		MaxOutputTokens:   512,
	}

	first, err := fixture.service.Review(context.Background(), input)
	if err != nil {
		t.Fatalf("first UNKNOWN Review: %v", err)
	}
	if !first.AdmissionCreated || !first.FinalizeApplied ||
		first.Proposal.State != currentstore.LearningProposalReviewUnknown ||
		first.Proposal.Revision != 2 ||
		first.Proposal.ReviewerAttemptID == "" ||
		first.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		first.LoopResult.ReasonCode !=
			string(modulehost.UnknownClassResponseBodyReadIncomplete) {
		t.Fatalf("first UNKNOWN Learning Review=%+v", first)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("first UNKNOWN review model calls=%d want 1", got)
	}

	retried, err := fixture.service.Review(context.Background(), input)
	if err != nil {
		t.Fatalf("exact UNKNOWN Review retry: %v", err)
	}
	if retried.AdmissionCreated || retried.FinalizeApplied ||
		retried.RunID != first.RunID ||
		retried.Proposal.State != currentstore.LearningProposalReviewUnknown ||
		retried.Proposal.Revision != first.Proposal.Revision ||
		retried.Proposal.ReviewerAttemptID != first.Proposal.ReviewerAttemptID ||
		retried.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation {
		t.Fatalf("UNKNOWN retry=%+v first=%+v", retried, first)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("UNKNOWN retry replayed Reviewer model: calls=%d", got)
	}

	dispatch, err := fixture.chat.store.GetModelDispatchRecord(
		context.Background(),
		first.Proposal.ReviewerAttemptID,
	)
	if err != nil || dispatch.Attempt.RunID != first.RunID ||
		dispatch.Attempt.State != corecontract.ModelAttemptUnknown ||
		dispatch.Attempt.SourceDispatchAttemptID != "" {
		t.Fatalf("UNKNOWN ordinary Reviewer dispatch=%+v error=%v", dispatch, err)
	}
}

type learningReviewServiceFixture struct {
	chat              *chatServiceFixture
	service           *LearningReviewService
	invoker           *learningReviewApproveInvoker
	proposal          currentstore.LearningProposalRecord
	reviewerAgentID   string
	reviewerProfileID string
}

func newLearningReviewServiceFixture(t *testing.T) *learningReviewServiceFixture {
	t.Helper()
	chat := newChatServiceFixture(t)
	ctx := context.Background()
	proposer, err := chat.service.Chat(
		ctx,
		chat.input(
			"learning-review-proposer-request",
			"candidate skill source",
			time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		),
	)
	if err != nil || proposer.TerminalResult == nil {
		t.Fatalf("create proposer Run=%+v error=%v", proposer, err)
	}

	lease, err := chat.store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   proposer.RunID,
			OwnerID: "learning-review-test-proposer-read",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("Acquire proposer lease: %v", err)
	}
	run, loadErr := chat.store.LoadRunForLoop(ctx, lease)
	releaseErr := chat.store.ReleaseRunLease(ctx, lease)
	if loadErr != nil || releaseErr != nil {
		t.Fatalf("load/release proposer Run: load=%v release=%v", loadErr, releaseErr)
	}
	dispatch, err := chat.store.GetModelDispatchRecord(
		ctx,
		proposer.TerminalResult.AttemptID,
	)
	if err != nil {
		t.Fatalf("Get proposer Model Attempt: %v", err)
	}
	_, draftCanonical, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text:          "bounded reusable skill context",
		},
	)
	if err != nil {
		t.Fatalf("NewStaticContextV1: %v", err)
	}
	submitted, err := chat.store.SubmitStaticSkillProposal(
		ctx,
		currentstore.SubmitStaticSkillProposalInput{
			Proposal: learningcontract.ProposalV1{
				SchemaVersion:          learningcontract.ProposalSchemaVersionV1,
				Kind:                   learningcontract.ProposalKindSkillV1,
				TenantID:               chat.tenantID,
				Workspace:              run.Manifest.Workspace,
				ProposerAgent:          run.Member.Agent,
				ProposerProfile:        run.Member.Profile,
				ProposerRunID:          run.RunID,
				ProposerManifestDigest: run.Manifest.ManifestDigest,
				ProposerMember:         run.Manifest.Members[0],
				ProposerResultRef:      dispatch.Attempt.ResultRef,
				Target: moduleapi.Ref{
					ID:      "freeagent.test.learning-review-skill",
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
		t.Fatalf("SubmitStaticSkillProposal=%+v error=%v", submitted, err)
	}

	reviewerAgentID, reviewerProfileID, provider :=
		publishLearningReviewerControl(t, chat)
	invoker := &learningReviewApproveInvoker{proposal: submitted.Record}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         invoker,
	})
	if err != nil {
		t.Fatalf("New reviewer Registry: %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(chat.store, registry)
	if err != nil {
		t.Fatalf("New reviewer UniversalLoop: %v", err)
	}
	base, err := NewChatService(chat.store, loop)
	if err != nil {
		t.Fatalf("New reviewer ChatService: %v", err)
	}
	service, err := NewLearningReviewService(base)
	if err != nil {
		t.Fatalf("NewLearningReviewService: %v", err)
	}
	return &learningReviewServiceFixture{
		chat:              chat,
		service:           service,
		invoker:           invoker,
		proposal:          submitted.Record,
		reviewerAgentID:   reviewerAgentID,
		reviewerProfileID: reviewerProfileID,
	}
}

func publishLearningReviewerControl(
	t *testing.T,
	chat *chatServiceFixture,
) (string, string, moduleapi.ActivatedModuleRef) {
	t.Helper()
	ctx := context.Background()
	basis, control, catalog, err := chat.store.LoadPublishedBasis(
		ctx,
		chat.tenantID,
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis: %v", err)
	}
	const (
		reviewerAgentID   = "agent-learning-reviewer"
		reviewerProfileID = "profile-learning-reviewer"
	)
	reviewerAgent := corecontract.AgentRef{
		ID: reviewerAgentID, Version: "v1", Digest: strings.Repeat("8", 64),
	}
	reviewerProfile := corecontract.ProfileRef{
		ID: reviewerProfileID, Version: "v1", Digest: strings.Repeat("9", 64),
	}
	profile := control.Profiles[0]
	profile.Profile = reviewerProfile
	if len(profile.Bindings) != 1 {
		t.Fatalf("base profile Bindings=%d", len(profile.Bindings))
	}
	entry, found := catalog.FindInstance(profile.Bindings[0].InstanceID)
	if !found {
		t.Fatal("base model Provider is absent from Catalog")
	}
	provider := entry.Activation
	_, configCanonical, err := moduleapi.NewModelBindingConfigV2(
		moduleapi.ModelBindingConfigV2{
			SchemaVersion: moduleapi.ModelBindingConfigSchemaV2,
			Provider:      "test-provider",
			Model:         "test-model",
			ModelBuildID:  "test-model-build-v1",
			Parameters:    json.RawMessage(`{"max_tokens":512,"temperature":0}`),
		},
	)
	if err != nil {
		t.Fatalf("New reviewer ModelBindingConfigV2: %v", err)
	}
	profile.Bindings[0].ConfigRef = putChatContent(
		t,
		chat.store,
		currentstore.ContentConfig,
		configCanonical,
	)
	control.SnapshotID = "control-learning-reviewer"
	control.Revision++
	control.Digest = ""
	control.Agents = append(control.Agents, reviewerAgent)
	control.Profiles = append(control.Profiles, profile)
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("New reviewer ControlSnapshot: %v", err)
	}
	catalog.GenerationID = "catalog-learning-reviewer"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("New reviewer CatalogGeneration: %v", err)
	}
	if _, err := chat.store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatalf("Publish reviewer Control/Catalog: %v", err)
	}
	return reviewerAgentID, reviewerProfileID, provider
}

type learningReviewApproveInvoker struct {
	mu       sync.Mutex
	proposal currentstore.LearningProposalRecord
	inputs   [][]byte
	unknown  bool
}

func (invoker *learningReviewApproveInvoker) Invoke(
	_ context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	invoker.mu.Lock()
	invoker.inputs = append(
		invoker.inputs,
		bytes.Clone(prepared.Invocation.Input),
	)
	invoker.mu.Unlock()
	if invoker.unknown {
		return modulehost.InvocationResult{
			InvocationID: prepared.Invocation.InvocationID,
			Provider:     prepared.Binding.Provider,
			Outcome:      modulehost.InvocationUnknown,
			UnknownClass: modulehost.UnknownClassResponseBodyReadIncomplete,
		}, nil
	}
	_, verdictCanonical, _, err := learningcontract.NewReviewVerdictV1(
		learningcontract.ReviewVerdictV1{
			SchemaVersion:      learningcontract.ReviewVerdictSchemaVersionV1,
			ProposalID:         invoker.proposal.ProposalID,
			SourceFingerprint:  invoker.proposal.Proposal.SourceFingerprint,
			ContentFingerprint: invoker.proposal.Proposal.ContentFingerprint,
			Decision:           learningcontract.ReviewDecisionApproveV1,
			BoundedReason:      "The exact bounded draft is supported.",
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(verdictCanonical),
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	return modulehost.InvocationResult{
		InvocationID: prepared.Invocation.InvocationID,
		Provider:     prepared.Binding.Provider,
		Outcome:      modulehost.InvocationSucceeded,
		Output:       outputCanonical,
	}, nil
}

func (invoker *learningReviewApproveInvoker) callCount() int {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	return len(invoker.inputs)
}

func (invoker *learningReviewApproveInvoker) onlyInput(t *testing.T) []byte {
	t.Helper()
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	if len(invoker.inputs) != 1 {
		t.Fatalf("Reviewer model inputs=%d want 1", len(invoker.inputs))
	}
	return bytes.Clone(invoker.inputs[0])
}
