package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestLearningReviewBundleRoundTripPreservesAllReviewStates(t *testing.T) {
	fixture := newLearningReviewBackupFixture(t)
	bundle := filepath.Join(t.TempDir(), "learning-review.bundle")
	if _, err := CreateBundle(
		context.Background(),
		fixture.base.base.databasePath,
		fixture.base.base.artifactRoot,
		bundle,
		"learning-review-roundtrip-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle: %v", err)
	}
	if _, err := VerifyBundle(context.Background(), bundle); err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	restoredDatabase := filepath.Join(t.TempDir(), "restored.sqlite")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		restoredDatabase,
		filepath.Join(t.TempDir(), "restored-artifacts"),
	); err != nil {
		t.Fatalf("RestoreBundle: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		restoredDatabase,
	)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range fixture.records {
		got, err := store.GetLearningProposal(
			context.Background(),
			want.Proposal.TenantID,
			want.ProposalID,
		)
		if err != nil {
			_ = store.Close()
			t.Fatalf("GetLearningProposal(%s): %v", name, err)
		}
		if got.ProposalID != want.ProposalID ||
			got.ReviewRunID != want.ReviewRunID ||
			got.ReviewerAttemptID != want.ReviewerAttemptID ||
			got.State != want.State || got.Revision != want.Revision ||
			!got.CreatedAt.Equal(want.CreatedAt) ||
			!got.UpdatedAt.Equal(want.UpdatedAt) ||
			!bytes.Equal(got.ProposalCanonical, want.ProposalCanonical) ||
			!bytes.Equal(got.DraftCanonical, want.DraftCanonical) {
			_ = store.Close()
			t.Fatalf("%s restored=%+v source=%+v", name, got, want)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := openReadOnlyDatabase(restoredDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for name, record := range fixture.records {
		var manifestCanonical, memberCanonical []byte
		if err := database.QueryRow(`
			SELECT manifest.canonical_json, member.canonical_json
			FROM run_manifests AS manifest
			JOIN member_execution_snapshots AS member
			  ON member.run_id=manifest.run_id
			WHERE manifest.run_id=?
		`, record.ReviewRunID).Scan(&manifestCanonical, &memberCanonical); err != nil {
			t.Fatalf("load Reviewer closure %s: %v", name, err)
		}
		manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
		if err != nil {
			t.Fatal(err)
		}
		member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
		if err != nil {
			t.Fatal(err)
		}
		if member.Agent.ID != fixture.reviewerAgent.ID ||
			member.Profile.ID != fixture.reviewerProfile.ID ||
			member.MemberID == record.Proposal.ProposerMember.MemberID {
			t.Fatalf("%s Reviewer member=%+v", name, member)
		}
		var taskCanonical []byte
		if err := database.QueryRow(`
			SELECT canonical_bytes FROM content_records WHERE content_digest=?
		`, manifest.TaskInputRef).Scan(&taskCanonical); err != nil {
			t.Fatal(err)
		}
		task, err := corecontract.RestoreTaskInputV1(taskCanonical)
		if err != nil {
			t.Fatal(err)
		}
		request, canonical, _, err := learningcontract.ParseReviewRequestV1(
			[]byte(task.Text),
		)
		if err != nil || !bytes.Equal(canonical, []byte(task.Text)) ||
			request.ProposalID != record.ProposalID ||
			!bytes.Equal(request.ProposalCanonical, record.ProposalCanonical) ||
			!bytes.Equal(request.DraftCanonical, record.DraftCanonical) {
			t.Fatalf("%s ReviewRequest=%+v error=%v", name, request, err)
		}
	}
}

func TestLearningReviewBackupSemanticGateRejectsCriticalTamper(t *testing.T) {
	fixture := newLearningReviewBackupFixture(t)
	approved := fixture.records["APPROVED"]
	tests := []struct {
		name   string
		tamper func(*testing.T, *sql.DB)
	}{
		{
			name: "review_run_id",
			tamper: func(t *testing.T, database *sql.DB) {
				execClosedFileTamperV1(
					t,
					database,
					[]string{"learning_proposals_observation_update_guard"},
					`
					UPDATE learning_proposals SET review_run_id=? WHERE proposal_id=?
				`,
					approved.Proposal.ProposerRunID,
					approved.ProposalID,
				)
			},
		},
		{
			name: "reviewer_attempt_id",
			tamper: func(t *testing.T, database *sql.DB) {
				execClosedFileTamperV1(
					t,
					database,
					[]string{"learning_proposals_observation_update_guard"},
					`
					UPDATE learning_proposals SET reviewer_attempt_id=? WHERE proposal_id=?
				`,
					approved.ProposerAttemptID,
					approved.ProposalID,
				)
			},
		},
		{
			name: "state",
			tamper: func(t *testing.T, database *sql.DB) {
				execClosedFileTamperV1(
					t,
					database,
					[]string{"learning_proposals_observation_update_guard"},
					`
					UPDATE learning_proposals SET state='REJECTED' WHERE proposal_id=?
				`,
					approved.ProposalID,
				)
			},
		},
		{
			name: "revision",
			tamper: func(t *testing.T, database *sql.DB) {
				execClosedFileTamperV1(
					t,
					database,
					[]string{"learning_proposals_observation_update_guard"},
					`
					UPDATE learning_proposals SET revision=3 WHERE proposal_id=?
				`,
					approved.ProposalID,
				)
			},
		},
		{
			name: "TaskInput",
			tamper: func(t *testing.T, database *sql.DB) {
				_, changed, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
					SchemaVersion: corecontract.TaskInputSchemaVersionV1,
					Text:          "tampered ReviewRequest",
				})
				if err != nil {
					t.Fatal(err)
				}
				manifest := fixture.manifests[approved.ProposalID]
				execClosedFileTamperV1(
					t,
					database,
					[]string{"content_records_reject_update"},
					`
					UPDATE content_records SET canonical_bytes=?, size_bytes=?
					WHERE content_digest=?
				`,
					changed,
					len(changed),
					manifest.TaskInputRef,
				)
			},
		},
		{
			name: "Reviewer identity",
			tamper: func(t *testing.T, database *sql.DB) {
				var proposer []byte
				if err := database.QueryRow(`
					SELECT canonical_json FROM member_execution_snapshots
					WHERE run_id=? AND member_id=?
				`, approved.Proposal.ProposerRunID,
					approved.Proposal.ProposerMember.MemberID,
				).Scan(&proposer); err != nil {
					t.Fatal(err)
				}
				execClosedFileTamperV1(
					t,
					database,
					[]string{"member_execution_snapshots_reject_update"},
					`
					UPDATE member_execution_snapshots SET canonical_json=? WHERE run_id=?
				`,
					proposer,
					approved.ReviewRunID,
				)
			},
		},
		{
			name: "Result Verdict",
			tamper: func(t *testing.T, database *sql.DB) {
				_, changed, err := moduleapi.NewModelGenerateOutputV1(
					moduleapi.ModelGenerateOutputV1{
						SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
						AssistantText: `{not-a-verdict}`,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				execClosedFileTamperV1(
					t,
					database,
					[]string{"content_records_reject_update"},
					`
					UPDATE content_records SET canonical_bytes=?, size_bytes=?
					WHERE content_digest=(
						SELECT result_ref FROM model_dispatch_attempts WHERE attempt_id=?
					)
				`,
					changed,
					len(changed),
					approved.ReviewerAttemptID,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "tampered.sqlite")
			copyTestFile(t, fixture.base.base.databasePath, databasePath)
			database, err := sql.Open(
				"sqlite",
				sqliteFileURI(databasePath, "rw", "foreign_keys(1)"),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			test.tamper(t, database)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				databasePath,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("semantic gate error=%v", err)
			}
			if _, err := CreateBundle(
				context.Background(),
				databasePath,
				fixture.base.base.artifactRoot,
				filepath.Join(t.TempDir(), "rejected.bundle"),
				"learning-review-tamper-test/v1",
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("CreateBundle tamper error=%v", err)
			}
		})
	}
}

type learningReviewBackupFixture struct {
	base            learningProposalBackupFixture
	records         map[string]currentstore.LearningProposalRecord
	manifests       map[string]corecontract.RunManifest
	reviewerAgent   corecontract.AgentRef
	reviewerProfile corecontract.ProfileRef
}

func newLearningReviewBackupFixture(t *testing.T) learningReviewBackupFixture {
	t.Helper()
	base := newLearningProposalBackupFixture(t)
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		base.base.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()
	basis, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		base.record.Proposal.TenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	var baseProfile controlcontract.ProfileDefinition
	foundProfile := false
	for _, candidate := range control.Profiles {
		if candidate.Profile.ID == base.record.Proposal.ProposerProfile.ID {
			baseProfile = candidate
			foundProfile = true
			break
		}
	}
	if !foundProfile {
		t.Fatal("proposer Profile is absent from current Control")
	}
	var modelBinding controlcontract.BindingSpec
	foundBinding := false
	for _, binding := range baseProfile.Bindings {
		if binding.Port.Name == moduleapi.PortNameModelGenerate &&
			binding.Port.ExactVersion == moduleapi.PortVersionV2 {
			modelBinding = binding
			foundBinding = true
			break
		}
	}
	if !foundBinding {
		t.Fatal("base model Binding is absent")
	}
	configRecord, err := store.GetContent(context.Background(), modelBinding.ConfigRef)
	if err != nil {
		t.Fatal(err)
	}
	config, err := moduleapi.RestoreModelBindingConfigV2(configRecord.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	config.Parameters = json.RawMessage(`{"max_tokens":512}`)
	_, configCanonical, err := moduleapi.NewModelBindingConfigV2(config)
	if err != nil {
		t.Fatal(err)
	}
	configInput := backupReviewContentInput(t, currentstore.ContentConfig, configCanonical)
	if _, err := store.PutContent(context.Background(), configInput); err != nil {
		t.Fatal(err)
	}
	modelBinding.ConfigRef = configInput.Digest
	reviewerAgent := corecontract.AgentRef{
		ID: "agent-backup-learning-reviewer", Version: "v1", Digest: strings.Repeat("8", 64),
	}
	reviewerProfile := corecontract.ProfileRef{
		ID: "profile-backup-learning-reviewer", Version: "v1", Digest: strings.Repeat("9", 64),
	}
	baseProfile.Profile = reviewerProfile
	baseProfile.Bindings = []controlcontract.BindingSpec{modelBinding}
	control.SnapshotID = "control-backup-learning-review"
	control.Revision++
	control.Digest = ""
	control.Agents = append(control.Agents, reviewerAgent)
	control.Profiles = append(control.Profiles, baseProfile)
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-backup-learning-review"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err = store.PublishControlCatalog(
		context.Background(),
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	records := map[string]currentstore.LearningProposalRecord{
		"REVIEW_PENDING": base.record,
	}
	for _, name := range []string{
		"APPROVED", "REJECTED", "REVIEW_FAILED", "REVIEW_UNKNOWN", "RECONCILED",
	} {
		records[name] = submitBackupReviewCandidate(t, store, base.record, name)
	}
	manifests := make(map[string]corecontract.RunManifest, len(records))
	for name, record := range records {
		manifest := commitBackupReviewAdmission(
			t,
			store,
			basis,
			controlCanonical,
			catalogCanonical,
			reviewerAgent,
			reviewerProfile,
			record,
			name,
		)
		manifests[record.ProposalID] = manifest
		if name == "REVIEW_PENDING" {
			continue
		}
		begin := beginBackupReviewerAttempt(t, store, manifest, name)
		switch name {
		case "APPROVED":
			commitBackupReviewerOutcome(t, store, begin, record,
				corecontract.ModelAttemptSucceeded, learningcontract.ReviewDecisionApproveV1, nil, nil)
		case "REJECTED":
			commitBackupReviewerOutcome(t, store, begin, record,
				corecontract.ModelAttemptSucceeded, learningcontract.ReviewDecisionRejectV1,
				[]learningcontract.ReviewIssueCodeV1{learningcontract.ReviewIssueUnsupportedClaimV1}, nil)
		case "REVIEW_FAILED":
			commitBackupReviewerOutcome(t, store, begin, record,
				corecontract.ModelAttemptFailed, "", nil, nil)
		case "REVIEW_UNKNOWN", "RECONCILED":
			unknown := commitBackupReviewerOutcome(t, store, begin, record,
				corecontract.ModelAttemptUnknown, "", nil, nil)
			projected, err := store.FinalizeLearningReview(
				context.Background(),
				currentstore.FinalizeLearningReviewInput{
					TenantID:                 record.Proposal.TenantID,
					ProposalID:               record.ProposalID,
					ExpectedProposalRevision: 1,
				},
			)
			if err != nil || projected.Proposal.State != currentstore.LearningProposalReviewUnknown {
				t.Fatalf("project %s UNKNOWN=%+v error=%v", name, projected, err)
			}
			if name == "REVIEW_UNKNOWN" {
				continue
			}
			commitBackupReviewerOutcome(
				t,
				store,
				currentstore.BeginModelDispatchResult{
					Lease:   unknown.Lease,
					Attempt: unknown.Record.Attempt,
				},
				record,
				corecontract.ModelAttemptSucceeded,
				learningcontract.ReviewDecisionApproveV1,
				nil,
				[]byte(`{"kind":"provider_lookup","request_id":"backup-review-request"}`),
			)
		}
		if name != "REVIEW_UNKNOWN" {
			expected := uint64(1)
			if name == "RECONCILED" {
				expected = 2
			}
			if _, err := store.FinalizeLearningReview(
				context.Background(),
				currentstore.FinalizeLearningReviewInput{
					TenantID:                 record.Proposal.TenantID,
					ProposalID:               record.ProposalID,
					ExpectedProposalRevision: expected,
				},
			); err != nil {
				t.Fatalf("FinalizeLearningReview(%s): %v", name, err)
			}
		}
	}
	for name, record := range records {
		stored, err := store.GetLearningProposal(
			context.Background(),
			record.Proposal.TenantID,
			record.ProposalID,
		)
		if err != nil {
			t.Fatalf("GetLearningProposal(%s): %v", name, err)
		}
		records[name] = stored
	}
	if records["REVIEW_PENDING"].State != currentstore.LearningProposalReviewPending ||
		records["APPROVED"].State != currentstore.LearningProposalApproved ||
		records["REJECTED"].State != currentstore.LearningProposalRejected ||
		records["REVIEW_FAILED"].State != currentstore.LearningProposalReviewFailed ||
		records["REVIEW_UNKNOWN"].State != currentstore.LearningProposalReviewUnknown ||
		records["RECONCILED"].State != currentstore.LearningProposalApproved ||
		records["RECONCILED"].Revision != 3 {
		t.Fatalf("unexpected Review state matrix=%+v", records)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return learningReviewBackupFixture{
		base:            base,
		records:         records,
		manifests:       manifests,
		reviewerAgent:   reviewerAgent,
		reviewerProfile: reviewerProfile,
	}
}

func submitBackupReviewCandidate(
	t *testing.T,
	store *currentstore.Store,
	lineage currentstore.LearningProposalRecord,
	name string,
) currentstore.LearningProposalRecord {
	t.Helper()
	_, draft, err := corecontract.NewStaticContextV1(corecontract.StaticContextV1{
		SchemaVersion: corecontract.StaticContextSchemaVersionV1,
		Text:          "Backup Learning Review candidate " + name,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.SubmitStaticSkillProposal(
		context.Background(),
		currentstore.SubmitStaticSkillProposalInput{
			Proposal: learningcontract.ProposalV1{
				SchemaVersion:          learningcontract.ProposalSchemaVersionV1,
				Kind:                   learningcontract.ProposalKindSkillV1,
				TenantID:               lineage.Proposal.TenantID,
				Workspace:              lineage.Proposal.Workspace,
				ProposerAgent:          lineage.Proposal.ProposerAgent,
				ProposerProfile:        lineage.Proposal.ProposerProfile,
				ProposerRunID:          lineage.Proposal.ProposerRunID,
				ProposerManifestDigest: lineage.Proposal.ProposerManifestDigest,
				ProposerMember:         lineage.Proposal.ProposerMember,
				ProposerResultRef:      lineage.Proposal.ProposerResultRef,
				Target: moduleapi.Ref{
					ID:      "freeagent.backup.learning-review." + strings.ToLower(name),
					Version: "1.0.0",
				},
			},
			DraftCanonical: draft,
			SourceEvidence: learningcontract.SourceEvidenceV1{
				Mechanism:        learningcontract.SourceMechanismLocalImportV1,
				OriginMaterial:   []byte("backup-review-origin/" + name),
				RevisionMaterial: []byte("backup-review-revision/" + name),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return result.Record
}

func commitBackupReviewAdmission(
	t *testing.T,
	store *currentstore.Store,
	basis controlcontract.PublishedBasis,
	controlCanonical []byte,
	catalogCanonical []byte,
	agent corecontract.AgentRef,
	profile corecontract.ProfileRef,
	record currentstore.LearningProposalRecord,
	name string,
) corecontract.RunManifest {
	t.Helper()
	_, requestCanonical, _, err := learningcontract.NewReviewRequestV1(
		learningcontract.ReviewRequestV1{
			SchemaVersion:       learningcontract.ReviewRequestSchemaVersionV1,
			ProposalID:          record.ProposalID,
			SourceFingerprint:   record.Proposal.SourceFingerprint,
			ContentFingerprint:  record.Proposal.ContentFingerprint,
			DraftDigest:         record.Proposal.DraftDigest,
			OutputSchemaVersion: learningcontract.ReviewVerdictSchemaVersionV1,
			MaxOutputTokens:     512,
			ReviewPolicy:        learningcontract.ReviewPolicyProposalGateV1,
			Instructions:        learningcontract.ReviewInstructionsV1,
			ProposalCanonical:   bytes.Clone(record.ProposalCanonical),
			DraftCanonical:      bytes.Clone(record.DraftCanonical),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
		SchemaVersion: corecontract.TaskInputSchemaVersionV1,
		Text:          string(requestCanonical),
	})
	if err != nil {
		t.Fatal(err)
	}
	task := backupReviewContentInput(t, currentstore.ContentTaskInput, taskCanonical)
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion: corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:      record.Proposal.TenantID,
			AdmissionKey:  "backup-learning-review-" + strings.ToLower(name),
			PrincipalID:   "backup-learning-review-principal",
			WorkspaceID:   record.Proposal.Workspace.ID,
			AgentID:       agent.ID,
			ProfileID:     profile.ID,
			TaskInputRef:  task.Digest,
			RequestedPorts: []moduleapi.PortRef{{
				Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV2,
			}},
			Deadline:          time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
			CancellationScope: "run",
			ExplicitLimits:    []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-backup-learning-review-" + strings.ToLower(name)
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		context.Background(),
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            runID,
			MemberID:         "member-backup-learning-review-" + strings.ToLower(name),
			RecoveryRootRef:  "recovery/" + runID,
			PublishedBasis:   basis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := store.CommitLearningReviewAdmission(
		context.Background(),
		currentstore.CommitLearningReviewAdmissionInput{
			TenantID:                 record.Proposal.TenantID,
			ProposalID:               record.ProposalID,
			ExpectedProposalRevision: 0,
			Run: currentstore.CommitRunAdmissionInput{
				PublishedBasis:          basis,
				IntentCanonical:         intentCanonical,
				IntentDigest:            intentDigest,
				MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
				RunManifestCanonical:    compiled.RunManifestCanonical,
				Contents:                []currentstore.ContentInput{task},
			},
		},
	)
	if err != nil || !committed.Created {
		t.Fatalf("CommitLearningReviewAdmission(%s)=%+v error=%v", name, committed, err)
	}
	manifest, err := corecontract.RestoreRunManifest(compiled.RunManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func beginBackupReviewerAttempt(
	t *testing.T,
	store *currentstore.Store,
	manifest corecontract.RunManifest,
	name string,
) currentstore.BeginModelDispatchResult {
	t.Helper()
	lease, err := store.AcquireRunLease(
		context.Background(),
		currentstore.AcquireRunLeaseInput{
			RunID:                 manifest.RunID,
			OwnerID:               "worker-" + manifest.RunID,
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
				Role: moduleapi.ModelRoleUser, Content: "Review backup candidate " + name,
			}},
			Parameters: json.RawMessage(`{"max_tokens":512}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	begin, err := store.BeginModelDispatch(
		context.Background(),
		currentstore.BeginModelDispatchInput{
			Lease:            lease,
			AttemptID:        "attempt-backup-learning-review-" + strings.ToLower(name),
			LogicalStepID:    corecontract.PureChatModelLogicalStepIDV1,
			RequestCanonical: requestCanonical,
			Deadline:         manifest.Deadline.Add(-time.Minute).Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return begin
}

func commitBackupReviewerOutcome(
	t *testing.T,
	store *currentstore.Store,
	begin currentstore.BeginModelDispatchResult,
	record currentstore.LearningProposalRecord,
	state corecontract.ModelAttemptState,
	decision learningcontract.ReviewDecisionV1,
	issues []learningcontract.ReviewIssueCodeV1,
	evidence []byte,
) currentstore.CommitModelDispatchOutcomeResult {
	t.Helper()
	input := currentstore.CommitModelDispatchOutcomeInput{
		Lease:                           begin.Lease,
		AttemptID:                       begin.Attempt.AttemptID,
		InvocationID:                    begin.Attempt.AttemptID,
		Provider:                        begin.Attempt.Binding.Provider,
		ExpectedAttemptRevision:         begin.Attempt.Revision,
		State:                           state,
		ReconciliationEvidenceCanonical: bytes.Clone(evidence),
	}
	if state == corecontract.ModelAttemptFailed {
		input.ErrorClassification = "BACKUP_LEARNING_REVIEW_FAILURE"
	}
	if state == corecontract.ModelAttemptUnknown {
		input.ProviderRequestID = "backup-review-request"
		input.UnknownReason = "backup Reviewer result was not observed"
	}
	if state == corecontract.ModelAttemptSucceeded {
		_, verdictCanonical, _, err := learningcontract.NewReviewVerdictV1(
			learningcontract.ReviewVerdictV1{
				SchemaVersion:      learningcontract.ReviewVerdictSchemaVersionV1,
				ProposalID:         record.ProposalID,
				SourceFingerprint:  record.Proposal.SourceFingerprint,
				ContentFingerprint: record.Proposal.ContentFingerprint,
				Decision:           decision,
				IssueCodes:         issues,
				BoundedReason:      "Backup preserves the bounded Review decision.",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		output := moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(verdictCanonical),
		}
		if len(evidence) != 0 {
			input.ProviderRequestID = "backup-review-request"
			output.ProviderRequestID = "backup-review-request"
		}
		_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(output)
		if err != nil {
			t.Fatal(err)
		}
		input.OutputCanonical = outputCanonical
		input.UsageReceiptCanonical = backupReviewUsageCanonical(t)
	}
	result, err := store.CommitModelDispatchOutcome(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func backupReviewUsageCanonical(t *testing.T) []byte {
	t.Helper()
	zero := uint64(0)
	_, canonical, err := moduleapi.NewModelUsageReceiptV2(
		moduleapi.ModelUsageReceiptV2{
			SchemaVersion:       moduleapi.ModelUsageReceiptSchemaV2,
			InputTokens:         &zero,
			CachedInputTokens:   &zero,
			UncachedInputTokens: &zero,
			OutputTokens:        &zero,
			ReasoningTokens:     &zero,
			RawReceipt:          []byte(`{"provider":"backup-review-test"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func backupReviewContentInput(
	t *testing.T,
	kind currentstore.ContentKind,
	canonical []byte,
) currentstore.ContentInput {
	t.Helper()
	digest, err := currentstore.ComputeContentDigest(kind, "application/json", canonical)
	if err != nil {
		t.Fatal(err)
	}
	return currentstore.ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      "application/json",
		CanonicalBytes: bytes.Clone(canonical),
	}
}

func formatReviewRecords(records map[string]currentstore.LearningProposalRecord) string {
	var builder strings.Builder
	for name, record := range records {
		_, _ = fmt.Fprintf(&builder, "%s=%s/%d ", name, record.State, record.Revision)
	}
	return builder.String()
}
