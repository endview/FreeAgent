package currentstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestLearningReviewAdmissionIsAtomicAndExactRetry(t *testing.T) {
	fixture := newLearningReviewStoreFixture(t, json.RawMessage(`{"max_tokens":512}`))
	before := admissionCommitCounts(t, fixture.store)
	first, err := fixture.store.CommitLearningReviewAdmission(
		context.Background(),
		CommitLearningReviewAdmissionInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 0,
			Run:                      fixture.admission,
		},
	)
	if err != nil {
		t.Fatalf("CommitLearningReviewAdmission: %v", err)
	}
	if !first.Created || !first.Run.Created ||
		first.Proposal.State != LearningProposalReviewPending ||
		first.Proposal.Revision != 1 ||
		first.Proposal.ReviewRunID != fixture.manifest.RunID ||
		first.Proposal.ReviewerAttemptID != "" {
		t.Fatalf("first=%+v", first)
	}
	after := admissionCommitCounts(t, fixture.store)
	if after[0] != before[0]+1 || after[1] != before[1]+1 ||
		after[2] != before[2]+1 || after[3] != before[3]+1 ||
		after[4] != before[4]+1 || after[5] != before[5]+2 {
		t.Fatalf("atomic admission counts before=%v after=%v", before, after)
	}

	retry, err := fixture.store.CommitLearningReviewAdmission(
		context.Background(),
		CommitLearningReviewAdmissionInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 0,
			Run:                      fixture.admission,
		},
	)
	if err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if retry.Created || retry.Run.Created ||
		retry.Proposal.Revision != first.Proposal.Revision ||
		retry.Proposal.ReviewRunID != first.Proposal.ReviewRunID ||
		!retry.Proposal.UpdatedAt.Equal(first.Proposal.UpdatedAt) ||
		admissionCommitCounts(t, fixture.store) != after {
		t.Fatalf("retry=%+v first=%+v", retry, first)
	}
}

func TestLearningReviewAdmissionRejectsUnsafeIdentityAndTokenConfig(t *testing.T) {
	tests := []struct {
		name       string
		parameters json.RawMessage
		mutate     func(*learningReviewStoreFixture)
	}{
		{name: "missing max_tokens", parameters: json.RawMessage(`{"temperature":0}`)},
		{name: "max_tokens exceeds request", parameters: json.RawMessage(`{"max_tokens":513}`)},
		{
			name:       "same Agent",
			parameters: json.RawMessage(`{"max_tokens":512}`),
			mutate: func(fixture *learningReviewStoreFixture) {
				fixture.replaceReviewerScope(
					t,
					fixture.proposal.Proposal.ProposerAgent.ID,
					fixture.member.Profile.ID,
					fixture.member.Workspace.ID,
				)
			},
		},
		{
			name:       "same Profile",
			parameters: json.RawMessage(`{"max_tokens":512}`),
			mutate: func(fixture *learningReviewStoreFixture) {
				fixture.replaceReviewerScope(
					t,
					fixture.member.Agent.ID,
					fixture.proposal.Proposal.ProposerProfile.ID,
					fixture.member.Workspace.ID,
				)
			},
		},
		{
			name:       "same Member ID",
			parameters: json.RawMessage(`{"max_tokens":512}`),
			mutate: func(fixture *learningReviewStoreFixture) {
				fixture.replaceReviewerMemberID(t, fixture.proposal.Proposal.ProposerMember.MemberID)
			},
		},
		{
			name:       "different Workspace",
			parameters: json.RawMessage(`{"max_tokens":512}`),
			mutate: func(fixture *learningReviewStoreFixture) {
				fixture.replaceReviewerScope(
					t,
					fixture.member.Agent.ID,
					fixture.member.Profile.ID,
					"workspace-learning-review-other",
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLearningReviewStoreFixture(t, test.parameters)
			if test.mutate != nil {
				test.mutate(fixture)
			}
			before := admissionCommitCounts(t, fixture.store)
			_, err := fixture.store.CommitLearningReviewAdmission(
				context.Background(),
				CommitLearningReviewAdmissionInput{
					TenantID:                 fixture.proposal.Proposal.TenantID,
					ProposalID:               fixture.proposal.ProposalID,
					ExpectedProposalRevision: 0,
					Run:                      fixture.admission,
				},
			)
			if !errors.Is(err, ErrLearningReviewLineage) &&
				!errors.Is(err, ErrInvalidLearningReview) {
				t.Fatalf("admission error=%v", err)
			}
			if after := admissionCommitCounts(t, fixture.store); after != before {
				t.Fatalf("rejected admission wrote rows before=%v after=%v", before, after)
			}
			proposal, getErr := fixture.store.GetLearningProposal(
				context.Background(),
				fixture.proposal.Proposal.TenantID,
				fixture.proposal.ProposalID,
			)
			if getErr != nil || proposal.State != LearningProposalSubmitted ||
				proposal.Revision != 0 {
				t.Fatalf("rejected admission changed Proposal=%+v error=%v", proposal, getErr)
			}
		})
	}
}

func TestLearningReviewAdmissionConcurrentRunsHaveOneWinner(t *testing.T) {
	fixture := newLearningReviewStoreFixture(t, json.RawMessage(`{"max_tokens":512}`))
	inputs := []CommitRunAdmissionInput{
		fixture.admission,
		fixture.alternateAdmission(t, "alternate"),
	}
	type outcome struct {
		result CommitLearningReviewAdmissionResult
		err    error
	}
	start := make(chan struct{})
	results := make([]outcome, len(inputs))
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index].result, results[index].err =
				fixture.store.CommitLearningReviewAdmission(
					context.Background(),
					CommitLearningReviewAdmissionInput{
						TenantID:                 fixture.proposal.Proposal.TenantID,
						ProposalID:               fixture.proposal.ProposalID,
						ExpectedProposalRevision: 0,
						Run:                      inputs[index],
					},
				)
		}(index)
	}
	close(start)
	wait.Wait()
	created, conflicted := 0, 0
	for _, result := range results {
		if result.err == nil && result.result.Created {
			created++
		} else if errors.Is(result.err, ErrLearningReviewConflict) {
			conflicted++
		} else {
			t.Fatalf("unexpected concurrent result=%+v", result)
		}
	}
	if created != 1 || conflicted != 1 {
		t.Fatalf("created=%d conflicted=%d results=%+v", created, conflicted, results)
	}
	var runCount int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM runs
		WHERE run_id IN ('run-learning-review', 'run-learning-review-alternate')
	`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 {
		t.Fatalf("Reviewer Runs=%d want 1", runCount)
	}
}

func TestLearningReviewAdmissionRejectsCrossTenantRunWithoutWrites(t *testing.T) {
	fixture := newLearningReviewStoreFixture(t, json.RawMessage(`{"max_tokens":512}`))
	otherTenant := newLearningProposalStoreFixtureForTenant(
		t,
		fixture.base,
		"tenant-learning-review-other",
	)
	var reviewTask ContentInput
	for _, content := range fixture.admission.Contents {
		if content.Kind == ContentTaskInput {
			reviewTask = content
			break
		}
	}
	if reviewTask.Digest == "" {
		t.Fatal("Reviewer TASK_INPUT is absent")
	}
	intent := otherTenant.admission.intent
	intent.AdmissionKey = "admission-cross-tenant-review"
	intent.TaskInputRef = reviewTask.Digest
	intent.RequestedPorts = []moduleapi.PortRef{{
		Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV1,
	}}
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		context.Background(),
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            "run-cross-tenant-review",
			MemberID:         "member-cross-tenant-review",
			RecoveryRootRef:  "recovery/run-cross-tenant-review",
			PublishedBasis:   otherTenant.admission.basis,
			ControlCanonical: otherTenant.admission.controlCanonical,
			CatalogCanonical: otherTenant.admission.catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	before := admissionCommitCounts(t, fixture.store)
	_, err = fixture.store.CommitLearningReviewAdmission(
		context.Background(),
		CommitLearningReviewAdmissionInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 0,
			Run: CommitRunAdmissionInput{
				PublishedBasis:          otherTenant.admission.basis,
				IntentCanonical:         intentCanonical,
				IntentDigest:            intentDigest,
				MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
				RunManifestCanonical:    compiled.RunManifestCanonical,
				Contents:                []ContentInput{reviewTask},
			},
		},
	)
	if !errors.Is(err, ErrLearningReviewLineage) &&
		!errors.Is(err, ErrInvalidLearningReview) {
		t.Fatalf("cross-Tenant admission error=%v", err)
	}
	if after := admissionCommitCounts(t, fixture.store); after != before {
		t.Fatalf("cross-Tenant rejection wrote rows before=%v after=%v", before, after)
	}
}

func TestGenericAdmissionStillRejectsDirectLearningReviewRequest(t *testing.T) {
	fixture := newLearningReviewStoreFixture(t, json.RawMessage(`{"max_tokens":512}`))
	var wrapped ContentInput
	for _, content := range fixture.admission.Contents {
		if content.Kind == ContentTaskInput {
			wrapped = content
			break
		}
	}
	task, err := corecontract.RestoreTaskInputV1(wrapped.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	direct := newAdmissionContent(t, ContentTaskInput, []byte(task.Text))
	intent, err := corecontract.RestoreAdmissionIntentV1(
		fixture.admission.IntentCanonical,
		fixture.admission.IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	intent.AdmissionKey = "generic-direct-learning-review"
	intent.TaskInputRef = direct.Digest
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	fixture.admission.IntentCanonical = canonical
	fixture.admission.IntentDigest = digest
	fixture.admission.Contents = []ContentInput{direct}
	fixture.recompileAdmission(t, "run-generic-direct-learning-review", "member-generic-direct-learning-review")
	before := admissionCommitCounts(t, fixture.store)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.admission,
	); !errors.Is(err, ErrAdmissionIntegrity) &&
		!errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("generic direct ReviewRequest error=%v", err)
	}
	if after := admissionCommitCounts(t, fixture.store); after != before {
		t.Fatalf("generic rejection wrote rows before=%v after=%v", before, after)
	}
}

func TestLearningReviewFinalizationMatrix(t *testing.T) {
	tests := []struct {
		name      string
		state     corecontract.ModelAttemptState
		assistant func(learningReviewStoreFixture) string
		action    bool
		want      LearningProposalState
	}{
		{
			name: "APPROVE", state: corecontract.ModelAttemptSucceeded,
			assistant: func(f learningReviewStoreFixture) string {
				return f.verdict(t, learningcontract.ReviewDecisionApproveV1, nil)
			},
			want: LearningProposalApproved,
		},
		{
			name: "REJECT", state: corecontract.ModelAttemptSucceeded,
			assistant: func(f learningReviewStoreFixture) string {
				return f.verdict(t, learningcontract.ReviewDecisionRejectV1,
					[]learningcontract.ReviewIssueCodeV1{learningcontract.ReviewIssueMissingEvidenceV1})
			},
			want: LearningProposalRejected,
		},
		{
			name: "invalid verdict JSON", state: corecontract.ModelAttemptSucceeded,
			assistant: func(learningReviewStoreFixture) string { return `{not-json}` },
			want:      LearningProposalReviewFailed,
		},
		{
			name: "ActionRequest", state: corecontract.ModelAttemptSucceeded,
			action: true, want: LearningProposalReviewFailed,
		},
		{name: "model FAILED", state: corecontract.ModelAttemptFailed, want: LearningProposalReviewFailed},
		{name: "model UNKNOWN", state: corecontract.ModelAttemptUnknown, want: LearningProposalReviewUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLearningReviewStoreFixture(t, json.RawMessage(`{"max_tokens":512}`))
			fixture.commitAdmission(t)
			begin := fixture.beginAttempt(t)
			fixture.commitAttemptOutcome(t, begin, test.state, test.assistant, test.action, nil)
			finalized, err := fixture.store.FinalizeLearningReview(
				context.Background(),
				FinalizeLearningReviewInput{
					TenantID:                 fixture.proposal.Proposal.TenantID,
					ProposalID:               fixture.proposal.ProposalID,
					ExpectedProposalRevision: 1,
				},
			)
			if err != nil {
				t.Fatalf("FinalizeLearningReview: %v", err)
			}
			if !finalized.Applied || finalized.Proposal.State != test.want ||
				finalized.Proposal.Revision != 2 ||
				finalized.Proposal.ReviewerAttemptID != begin.Attempt.AttemptID {
				t.Fatalf("finalized=%+v", finalized)
			}
			retry, err := fixture.store.FinalizeLearningReview(
				context.Background(),
				FinalizeLearningReviewInput{
					TenantID:                 fixture.proposal.Proposal.TenantID,
					ProposalID:               fixture.proposal.ProposalID,
					ExpectedProposalRevision: 2,
				},
			)
			if err != nil || retry.Applied || retry.Proposal.State != test.want ||
				retry.Proposal.Revision != 2 {
				t.Fatalf("idempotent Finalize=%+v error=%v", retry, err)
			}
		})
	}
}

func TestLearningReviewFinalizeWaitsForAttempt(t *testing.T) {
	fixture := newLearningReviewStoreFixture(t, json.RawMessage(`{"max_tokens":512}`))
	fixture.commitAdmission(t)
	if _, err := fixture.store.FinalizeLearningReview(
		context.Background(),
		FinalizeLearningReviewInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 1,
		},
	); !errors.Is(err, ErrLearningReviewNotReady) {
		t.Fatalf("no-Attempt Finalize error=%v", err)
	}
	fixture.beginAttempt(t)
	if _, err := fixture.store.FinalizeLearningReview(
		context.Background(),
		FinalizeLearningReviewInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 1,
		},
	); !errors.Is(err, ErrLearningReviewNotReady) {
		t.Fatalf("PENDING Attempt Finalize error=%v", err)
	}
}

func TestLearningReviewUnknownReconcilesSameAttemptAtRevisionThree(t *testing.T) {
	fixture := newLearningReviewStoreFixture(t, json.RawMessage(`{"max_tokens":512}`))
	fixture.commitAdmission(t)
	begin := fixture.beginAttempt(t)
	unknown := fixture.commitAttemptOutcome(
		t, begin, corecontract.ModelAttemptUnknown, nil, false, nil,
	)
	first, err := fixture.store.FinalizeLearningReview(
		context.Background(),
		FinalizeLearningReviewInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 1,
		},
	)
	if err != nil || first.Proposal.State != LearningProposalReviewUnknown ||
		first.Proposal.Revision != 2 {
		t.Fatalf("UNKNOWN projection=%+v error=%v", first, err)
	}
	approved := func(f learningReviewStoreFixture) string {
		return f.verdict(t, learningcontract.ReviewDecisionApproveV1, nil)
	}
	fixture.commitAttemptOutcome(
		t,
		BeginModelDispatchResult{Lease: unknown.Lease, Attempt: unknown.Record.Attempt},
		corecontract.ModelAttemptSucceeded,
		approved,
		false,
		[]byte(`{"kind":"provider_lookup","request_id":"review-request"}`),
	)
	finalized, err := fixture.store.FinalizeLearningReview(
		context.Background(),
		FinalizeLearningReviewInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 2,
		},
	)
	if err != nil || !finalized.Applied ||
		finalized.Proposal.State != LearningProposalApproved ||
		finalized.Proposal.Revision != 3 ||
		finalized.Proposal.ReviewerAttemptID != begin.Attempt.AttemptID {
		t.Fatalf("reconciled=%+v error=%v", finalized, err)
	}
	// Revision 3 is a supported exact retry and must not be rejected by the
	// input preflight.
	retry, err := fixture.store.FinalizeLearningReview(
		context.Background(),
		FinalizeLearningReviewInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 3,
		},
	)
	if err != nil || retry.Applied || retry.Proposal.Revision != 3 {
		t.Fatalf("revision-3 retry=%+v error=%v", retry, err)
	}
}

func TestLearningReviewSemanticClosureRejectsCriticalTamper(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*testing.T, learningReviewStoreFixture)
	}{
		{
			name: "Reviewer Run",
			tamper: func(t *testing.T, fixture learningReviewStoreFixture) {
				execClosedFileTamperV1(t, fixture.store,
					[]string{"learning_proposals_observation_update_guard"}, `
					UPDATE learning_proposals SET review_run_id=? WHERE proposal_id=?
				`, fixture.proposal.Proposal.ProposerRunID, fixture.proposal.ProposalID)
			},
		},
		{
			name: "Reviewer Attempt",
			tamper: func(t *testing.T, fixture learningReviewStoreFixture) {
				execClosedFileTamperV1(t, fixture.store,
					[]string{"learning_proposals_observation_update_guard"}, `
					UPDATE learning_proposals SET reviewer_attempt_id=? WHERE proposal_id=?
				`, fixture.base.attemptID, fixture.proposal.ProposalID)
			},
		},
		{
			name: "terminal state",
			tamper: func(t *testing.T, fixture learningReviewStoreFixture) {
				execClosedFileTamperV1(t, fixture.store,
					[]string{"learning_proposals_observation_update_guard"}, `
					UPDATE learning_proposals SET state='REJECTED' WHERE proposal_id=?
				`, fixture.proposal.ProposalID)
			},
		},
		{
			name: "revision 3 without evidence",
			tamper: func(t *testing.T, fixture learningReviewStoreFixture) {
				execClosedFileTamperV1(t, fixture.store,
					[]string{"learning_proposals_observation_update_guard"}, `
					UPDATE learning_proposals SET revision=3 WHERE proposal_id=?
				`, fixture.proposal.ProposalID)
			},
		},
		{
			name: "Review TaskInput",
			tamper: func(t *testing.T, fixture learningReviewStoreFixture) {
				_, changed, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
					SchemaVersion: corecontract.TaskInputSchemaVersionV1,
					Text:          "tampered Reviewer task",
				})
				if err != nil {
					t.Fatal(err)
				}
				execClosedFileTamperV1(t, fixture.store,
					[]string{"content_records_reject_update"}, `
					UPDATE content_records
					SET canonical_bytes=?, size_bytes=?
					WHERE content_digest=?
				`, changed, len(changed), fixture.manifest.TaskInputRef)
			},
		},
		{
			name: "Reviewer identity",
			tamper: func(t *testing.T, fixture learningReviewStoreFixture) {
				var proposerCanonical []byte
				if err := fixture.store.db.QueryRow(`
					SELECT canonical_json FROM member_execution_snapshots
					WHERE run_id=? AND member_id=?
				`, fixture.proposal.Proposal.ProposerRunID,
					fixture.proposal.Proposal.ProposerMember.MemberID,
				).Scan(&proposerCanonical); err != nil {
					t.Fatal(err)
				}
				execClosedFileTamperV1(t, fixture.store,
					[]string{"member_execution_snapshots_reject_update"}, `
					UPDATE member_execution_snapshots SET canonical_json=?
					WHERE run_id=? AND member_id=?
				`, proposerCanonical, fixture.manifest.RunID, fixture.member.MemberID)
			},
		},
		{
			name: "Result Verdict",
			tamper: func(t *testing.T, fixture learningReviewStoreFixture) {
				_, changed, err := moduleapi.NewModelGenerateOutputV1(
					moduleapi.ModelGenerateOutputV1{
						SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
						AssistantText: `{not-a-verdict}`,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				execClosedFileTamperV1(t, fixture.store,
					[]string{"content_records_reject_update"}, `
					UPDATE content_records
					SET canonical_bytes=?, size_bytes=?
					WHERE content_digest=(
						SELECT result_ref FROM model_dispatch_attempts WHERE run_id=?
					)
				`, changed, len(changed), fixture.manifest.RunID)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newApprovedLearningReviewStoreFixture(t)
			test.tamper(t, fixture)
			connection, err := fixture.store.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			verifyErr := VerifyLearningProposalSemanticClosureV1(
				context.Background(),
				connection,
			)
			if err := connection.Close(); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(verifyErr, ErrLearningProposalIntegrity) {
				t.Fatalf("semantic verifier error=%v", verifyErr)
			}
			if _, err := fixture.store.GetLearningProposal(
				context.Background(),
				fixture.proposal.Proposal.TenantID,
				fixture.proposal.ProposalID,
			); !errors.Is(err, ErrLearningProposalIntegrity) {
				t.Fatalf("GetLearningProposal tamper error=%v", err)
			}
		})
	}
}

func newApprovedLearningReviewStoreFixture(t *testing.T) learningReviewStoreFixture {
	t.Helper()
	fixture := newLearningReviewStoreFixture(t, json.RawMessage(`{"max_tokens":512}`))
	fixture.commitAdmission(t)
	begin := fixture.beginAttempt(t)
	fixture.commitAttemptOutcome(
		t,
		begin,
		corecontract.ModelAttemptSucceeded,
		func(f learningReviewStoreFixture) string {
			return f.verdict(t, learningcontract.ReviewDecisionApproveV1, nil)
		},
		false,
		nil,
	)
	if _, err := fixture.store.FinalizeLearningReview(
		context.Background(),
		FinalizeLearningReviewInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 1,
		},
	); err != nil {
		t.Fatal(err)
	}
	return *fixture
}

type learningReviewStoreFixture struct {
	store      *Store
	base       learningProposalStoreFixture
	proposal   LearningProposalRecord
	admission  CommitRunAdmissionInput
	manifest   corecontract.RunManifest
	member     corecontract.MemberExecutionSnapshot
	parameters json.RawMessage
	attemptID  string
}

func newLearningReviewStoreFixture(
	t *testing.T,
	parameters json.RawMessage,
) *learningReviewStoreFixture {
	t.Helper()
	base := newLearningProposalStoreFixture(t)
	submitted, err := base.store.SubmitKnowledgeProposal(
		context.Background(),
		base.input(
			t,
			"review-v1",
			"Review this bounded candidate.",
			localLearningEvidence("review-origin", "review-revision"),
			base.manifest.TenantID,
			base.member.Workspace.ID,
		),
	)
	if err != nil {
		t.Fatalf("SubmitKnowledgeProposal: %v", err)
	}

	control, err := controlcontract.RestoreControlSnapshot(
		base.admission.controlCanonical,
		base.admission.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		base.admission.catalogCanonical,
		base.admission.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	baseBinding := control.Profiles[0].Bindings[0]
	baseConfigRecord, err := base.store.GetContent(context.Background(), baseBinding.ConfigRef)
	if err != nil {
		t.Fatal(err)
	}
	modelConfig, err := moduleapi.RestoreModelBindingConfigV1(baseConfigRecord.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	modelConfig.Parameters = bytes.Clone(parameters)
	_, configCanonical, err := moduleapi.NewModelBindingConfigV1(modelConfig)
	if err != nil {
		t.Fatal(err)
	}
	config := newAdmissionContent(t, ContentConfig, configCanonical)
	if _, err := base.store.PutContent(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	baseBinding.ConfigRef = config.Digest

	reviewerAgent := corecontract.AgentRef{
		ID: "agent-learning-reviewer", Version: "v1", Digest: strings.Repeat("8", 64),
	}
	reviewerProfile := corecontract.ProfileRef{
		ID: "profile-learning-reviewer", Version: "v1", Digest: strings.Repeat("9", 64),
	}
	profile := control.Profiles[0]
	profile.Profile = reviewerProfile
	profile.Bindings = []controlcontract.BindingSpec{baseBinding}
	// Keep the proposer's current Profile compilable as a model-only Reviewer
	// solely so negative tests can prove same-Profile rejection without being
	// masked by an unrelated Binding/config failure. The proposer Run remains
	// bound to its earlier immutable snapshot.
	control.Profiles[0].Bindings = []controlcontract.BindingSpec{baseBinding}
	otherWorkspace := control.Workspaces[0]
	otherWorkspace.Workspace = corecontract.WorkspaceRef{
		ID: "workspace-learning-review-other", Version: "v1", Digest: strings.Repeat("7", 64),
	}
	control.Workspaces = append(control.Workspaces, otherWorkspace)
	control.SnapshotID = "control-learning-review-store"
	control.Revision++
	control.Digest = ""
	control.Agents = append(control.Agents, reviewerAgent)
	control.Profiles = append(control.Profiles, profile)
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-learning-review-store"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := base.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: base.admission.basis.PointerRevision,
			NewPointerRevision:      base.admission.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, reviewCanonical, _, err := learningcontract.NewReviewRequestV1(
		learningcontract.ReviewRequestV1{
			SchemaVersion:       learningcontract.ReviewRequestSchemaVersionV1,
			ProposalID:          submitted.Record.ProposalID,
			SourceFingerprint:   submitted.Record.Proposal.SourceFingerprint,
			ContentFingerprint:  submitted.Record.Proposal.ContentFingerprint,
			DraftDigest:         submitted.Record.Proposal.DraftDigest,
			OutputSchemaVersion: learningcontract.ReviewVerdictSchemaVersionV1,
			MaxOutputTokens:     512,
			ReviewPolicy:        learningcontract.ReviewPolicyProposalGateV1,
			Instructions:        learningcontract.ReviewInstructionsV1,
			ProposalCanonical:   bytes.Clone(submitted.Record.ProposalCanonical),
			DraftCanonical:      bytes.Clone(submitted.Record.DraftCanonical),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
		SchemaVersion: corecontract.TaskInputSchemaVersionV1,
		Text:          string(reviewCanonical),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := newAdmissionContent(t, ContentTaskInput, taskCanonical)
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion: corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:      submitted.Record.Proposal.TenantID,
			AdmissionKey:  "learning-review-admission",
			PrincipalID:   "learning-review-principal",
			WorkspaceID:   submitted.Record.Proposal.Workspace.ID,
			AgentID:       reviewerAgent.ID,
			ProfileID:     reviewerProfile.ID,
			TaskInputRef:  task.Digest,
			RequestedPorts: []moduleapi.PortRef{{
				Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV1,
			}},
			Deadline:          time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
			CancellationScope: "run",
			ExplicitLimits:    []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		context.Background(),
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            "run-learning-review",
			MemberID:         "member-learning-reviewer",
			RecoveryRootRef:  "recovery/run-learning-review",
			PublishedBasis:   basis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := corecontract.RestoreRunManifest(compiled.RunManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(compiled.MemberSnapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
	return &learningReviewStoreFixture{
		store:    base.store,
		base:     base,
		proposal: submitted.Record,
		admission: CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         intentCanonical,
			IntentDigest:            intentDigest,
			MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
			RunManifestCanonical:    compiled.RunManifestCanonical,
			Contents:                []ContentInput{task},
		},
		manifest:   manifest,
		member:     member,
		parameters: bytes.Clone(parameters),
		attemptID:  "attempt-learning-review",
	}
}

func (fixture *learningReviewStoreFixture) replaceReviewerMemberID(
	t *testing.T,
	memberID string,
) {
	t.Helper()
	// The immutable Control/Catalog are already frozen; only recompilation with
	// a different Member identity is required.
	var controlCanonical, catalogCanonical []byte
	if err := fixture.store.db.QueryRow(`SELECT canonical_json FROM control_snapshots WHERE snapshot_id=?`,
		fixture.admission.PublishedBasis.Control.SnapshotID).Scan(&controlCanonical); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRow(`SELECT canonical_json FROM runtime_catalog_generations WHERE generation_id=?`,
		fixture.admission.PublishedBasis.Catalog.GenerationID).Scan(&catalogCanonical); err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		context.Background(),
		assemblycompiler.CompileInput{
			IntentCanonical:  fixture.admission.IntentCanonical,
			IntentDigest:     fixture.admission.IntentDigest,
			RunID:            fixture.manifest.RunID,
			MemberID:         memberID,
			RecoveryRootRef:  "recovery/" + fixture.manifest.RunID,
			PublishedBasis:   fixture.admission.PublishedBasis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.admission.MemberSnapshotCanonical = compiled.MemberSnapshotCanonical
	fixture.admission.RunManifestCanonical = compiled.RunManifestCanonical
	fixture.manifest, err = corecontract.RestoreRunManifest(compiled.RunManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	fixture.member, err = corecontract.RestoreMemberExecutionSnapshot(compiled.MemberSnapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
}

func (fixture *learningReviewStoreFixture) replaceReviewerScope(
	t *testing.T,
	agentID string,
	profileID string,
	workspaceID string,
) {
	t.Helper()
	intent, err := corecontract.RestoreAdmissionIntentV1(
		fixture.admission.IntentCanonical,
		fixture.admission.IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	intent.AgentID = agentID
	intent.ProfileID = profileID
	intent.WorkspaceID = workspaceID
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	fixture.admission.IntentCanonical = canonical
	fixture.admission.IntentDigest = digest
	fixture.recompileAdmission(t, fixture.manifest.RunID, fixture.member.MemberID)
}

func (fixture learningReviewStoreFixture) alternateAdmission(
	t *testing.T,
	suffix string,
) CommitRunAdmissionInput {
	t.Helper()
	intent, err := corecontract.RestoreAdmissionIntentV1(
		fixture.admission.IntentCanonical,
		fixture.admission.IntentDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	intent.AdmissionKey += "-" + suffix
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	clone := fixture
	clone.admission.IntentCanonical = canonical
	clone.admission.IntentDigest = digest
	clone.recompileAdmission(t, "run-learning-review-"+suffix, "member-learning-reviewer-"+suffix)
	return clone.admission
}

func (fixture *learningReviewStoreFixture) recompileAdmission(
	t *testing.T,
	runID string,
	memberID string,
) {
	t.Helper()
	var controlCanonical, catalogCanonical []byte
	if err := fixture.store.db.QueryRow(
		`SELECT canonical_json FROM control_snapshots WHERE snapshot_id=?`,
		fixture.admission.PublishedBasis.Control.SnapshotID,
	).Scan(&controlCanonical); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRow(
		`SELECT canonical_json FROM runtime_catalog_generations WHERE generation_id=?`,
		fixture.admission.PublishedBasis.Catalog.GenerationID,
	).Scan(&catalogCanonical); err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		context.Background(),
		assemblycompiler.CompileInput{
			IntentCanonical:  fixture.admission.IntentCanonical,
			IntentDigest:     fixture.admission.IntentDigest,
			RunID:            runID,
			MemberID:         memberID,
			RecoveryRootRef:  "recovery/" + runID,
			PublishedBasis:   fixture.admission.PublishedBasis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.admission.MemberSnapshotCanonical = compiled.MemberSnapshotCanonical
	fixture.admission.RunManifestCanonical = compiled.RunManifestCanonical
	fixture.manifest, err = corecontract.RestoreRunManifest(compiled.RunManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	fixture.member, err = corecontract.RestoreMemberExecutionSnapshot(compiled.MemberSnapshotCanonical)
	if err != nil {
		t.Fatal(err)
	}
}

func (fixture learningReviewStoreFixture) commitAdmission(t *testing.T) {
	t.Helper()
	if _, err := fixture.store.CommitLearningReviewAdmission(
		context.Background(),
		CommitLearningReviewAdmissionInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 0,
			Run:                      fixture.admission,
		},
	); err != nil {
		t.Fatal(err)
	}
}

func (fixture learningReviewStoreFixture) beginAttempt(t *testing.T) BeginModelDispatchResult {
	t.Helper()
	lease, err := fixture.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 fixture.manifest.RunID,
			OwnerID:               "learning-review-worker",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, requestCanonical, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{{
				Role: moduleapi.ModelRoleUser, Content: "Review the exact candidate.",
			}},
			Parameters: bytes.Clone(fixture.parameters),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:            lease,
			AttemptID:        fixture.attemptID,
			LogicalStepID:    corecontract.PureChatModelLogicalStepIDV1,
			RequestCanonical: requestCanonical,
			Deadline:         fixture.manifest.Deadline.Add(-time.Minute).Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return begin
}

func (fixture learningReviewStoreFixture) commitAttemptOutcome(
	t *testing.T,
	begin BeginModelDispatchResult,
	state corecontract.ModelAttemptState,
	assistant func(learningReviewStoreFixture) string,
	action bool,
	evidence []byte,
) CommitModelDispatchOutcomeResult {
	t.Helper()
	input := CommitModelDispatchOutcomeInput{
		Lease:                           begin.Lease,
		AttemptID:                       begin.Attempt.AttemptID,
		InvocationID:                    begin.Attempt.AttemptID,
		Provider:                        begin.Attempt.Binding.Provider,
		ExpectedAttemptRevision:         begin.Attempt.Revision,
		State:                           state,
		ReconciliationEvidenceCanonical: bytes.Clone(evidence),
	}
	if state == corecontract.ModelAttemptFailed {
		input.ErrorClassification = "LEARNING_REVIEW_TEST_FAILURE"
	}
	if state == corecontract.ModelAttemptUnknown {
		input.ProviderRequestID = "review-request"
		input.UnknownReason = "review outcome was not observed"
	}
	if state == corecontract.ModelAttemptSucceeded {
		output := moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
		}
		if len(evidence) != 0 {
			input.ProviderRequestID = "review-request"
			output.ProviderRequestID = "review-request"
		}
		if action {
			output.ActionRequest = &moduleapi.ModelActionRequestV1{
				ActionID: "review.forbidden", CanonicalInput: json.RawMessage(`{}`),
			}
		} else {
			output.AssistantText = assistant(fixture)
		}
		_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(output)
		if err != nil {
			t.Fatal(err)
		}
		_, usageCanonical := modelSuccessOutcomeCanonical(t)
		if action {
			rejected, err := fixture.store.CommitLegalModelActionRejection(
				context.Background(),
				CommitLegalModelActionRejectionInput{
					Lease:                        begin.Lease,
					ModelAttemptID:               begin.Attempt.AttemptID,
					InvocationID:                 begin.Attempt.AttemptID,
					Provider:                     begin.Attempt.Binding.Provider,
					ExpectedModelAttemptRevision: begin.Attempt.Revision,
					OutputCanonical:              outputCanonical,
					UsageReceiptCanonical:        usageCanonical,
					ErrorClassification:          ModelActionRejectionUnknownAction,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			return CommitModelDispatchOutcomeResult{
				Record: rejected.Model, Lease: rejected.Lease, Applied: rejected.Applied,
			}
		}
		input.OutputCanonical = outputCanonical
		input.UsageReceiptCanonical = usageCanonical
	}
	committed, err := fixture.store.CommitModelDispatchOutcome(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return committed
}

func (fixture learningReviewStoreFixture) verdict(
	t *testing.T,
	decision learningcontract.ReviewDecisionV1,
	issues []learningcontract.ReviewIssueCodeV1,
) string {
	t.Helper()
	_, canonical, _, err := learningcontract.NewReviewVerdictV1(
		learningcontract.ReviewVerdictV1{
			SchemaVersion:      learningcontract.ReviewVerdictSchemaVersionV1,
			ProposalID:         fixture.proposal.ProposalID,
			SourceFingerprint:  fixture.proposal.Proposal.SourceFingerprint,
			ContentFingerprint: fixture.proposal.Proposal.ContentFingerprint,
			Decision:           decision,
			IssueCodes:         issues,
			BoundedReason:      "The bounded review decision is supported.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}
