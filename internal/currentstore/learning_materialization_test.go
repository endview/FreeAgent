package currentstore

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
)

func TestMaterializeApprovedLearningProposalIsDeterministicAndDetached(t *testing.T) {
	fixture := newApprovedLearningReviewStoreFixture(t)
	ctx := context.Background()
	input := MaterializeApprovedLearningProposalInput{
		TenantID:                 fixture.proposal.Proposal.TenantID,
		ProposalID:               fixture.proposal.ProposalID,
		ExpectedProposalRevision: 2,
	}

	before := learningMaterializationSideEffectCounts(t, fixture.store)
	first, err := fixture.store.MaterializeApprovedLearningProposal(ctx, input)
	if err != nil {
		t.Fatalf("MaterializeApprovedLearningProposal: %v", err)
	}
	if !first.Created || first.Record.Version.ProposalRevision != 2 ||
		first.Record.VersionID == "" || first.Record.MaterializedAt.IsZero() {
		t.Fatalf("unexpected first materialization: %#v", first)
	}
	artifact, err := learningcontract.MaterializedVersionArtifactV1(
		first.Record.VersionCanonical,
		first.Record.VersionID,
		first.Record.Proposal.ProposalCanonical,
		first.Record.Proposal.DraftCanonical,
	)
	if err != nil {
		t.Fatalf("MaterializedVersionArtifactV1: %v", err)
	}
	if artifact.ArtifactDigest != first.Record.ArtifactDigest ||
		artifact.ArtifactSizeBytes != first.Record.ArtifactSizeBytes {
		t.Fatal("Store result differs from reconstructed artifact")
	}

	first.Record.VersionCanonical[0] ^= 0xff
	first.Record.Proposal.ProposalCanonical[0] ^= 0xff
	first.Record.Proposal.DraftCanonical[0] ^= 0xff
	retry, err := fixture.store.MaterializeApprovedLearningProposal(ctx, input)
	if err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if retry.Created || retry.Record.VersionID == "" ||
		retry.Record.MaterializedAt != first.Record.MaterializedAt {
		t.Fatalf("unexpected retry: %#v", retry)
	}
	got, err := fixture.store.GetLearningMaterializedVersion(
		ctx,
		input.TenantID,
		input.ProposalID,
	)
	if err != nil {
		t.Fatalf("GetLearningMaterializedVersion: %v", err)
	}
	if got.VersionID != retry.Record.VersionID ||
		!bytes.Equal(got.VersionCanonical, retry.Record.VersionCanonical) {
		t.Fatal("Get differs from exact retry")
	}
	if after := learningMaterializationSideEffectCounts(t, fixture.store); after != before {
		t.Fatalf("materialization changed forbidden side effects: before=%v after=%v", before, after)
	}
}

func TestMaterializeApprovedLearningProposalRejectsNonApprovedAndWrongFence(t *testing.T) {
	ctx := context.Background()
	submitted := newLearningReviewStoreFixture(t, []byte(`{"max_tokens":512}`))
	if _, err := submitted.store.MaterializeApprovedLearningProposal(
		ctx,
		MaterializeApprovedLearningProposalInput{
			TenantID:                 submitted.proposal.Proposal.TenantID,
			ProposalID:               submitted.proposal.ProposalID,
			ExpectedProposalRevision: 2,
		},
	); !errors.Is(err, ErrLearningMaterializationNotApproved) {
		t.Fatalf("SUBMITTED materialization error=%v", err)
	}

	approved := newApprovedLearningReviewStoreFixture(t)
	if _, err := approved.store.MaterializeApprovedLearningProposal(
		ctx,
		MaterializeApprovedLearningProposalInput{
			TenantID:                 approved.proposal.Proposal.TenantID,
			ProposalID:               approved.proposal.ProposalID,
			ExpectedProposalRevision: 3,
		},
	); !errors.Is(err, ErrLearningMaterializationNotApproved) {
		t.Fatalf("stale revision materialization error=%v", err)
	}
	var versionCount int
	if err := approved.store.db.QueryRow(`
		SELECT COUNT(*) FROM learning_proposals WHERE version_id IS NOT NULL
	`).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if versionCount != 0 {
		t.Fatalf("rejected materialization wrote %d Versions", versionCount)
	}
}

func TestMaterializeApprovedLearningProposalRejectsEveryNonApprovedState(t *testing.T) {
	for _, state := range []LearningProposalState{
		LearningProposalSubmitted,
		LearningProposalReviewPending,
		LearningProposalRejected,
		LearningProposalReviewFailed,
		LearningProposalReviewUnknown,
	} {
		state := state
		t.Run(string(state), func(t *testing.T) {
			fixture := newLearningMaterializationStateFixture(t, state)
			before := learningMaterializationSideEffectCounts(t, fixture.store)
			if _, err := fixture.store.MaterializeApprovedLearningProposal(
				context.Background(),
				MaterializeApprovedLearningProposalInput{
					TenantID:                 fixture.proposal.Proposal.TenantID,
					ProposalID:               fixture.proposal.ProposalID,
					ExpectedProposalRevision: 2,
				},
			); !errors.Is(err, ErrLearningMaterializationNotApproved) {
				t.Fatalf("%s materialization error=%v", state, err)
			}
			var versions int
			if err := fixture.store.db.QueryRow(`
				SELECT COUNT(*) FROM learning_proposals WHERE version_id IS NOT NULL
			`).Scan(&versions); err != nil {
				t.Fatal(err)
			}
			if versions != 0 || learningMaterializationSideEffectCounts(t, fixture.store) != before {
				t.Fatalf("%s rejection changed Store facts", state)
			}
		})
	}
}

func TestMaterializeApprovedLearningProposalAcceptsReconciledRevisionThree(t *testing.T) {
	fixture := newLearningReviewStoreFixture(t, []byte(`{"max_tokens":512}`))
	fixture.commitAdmission(t)
	begin := fixture.beginAttempt(t)
	unknown := fixture.commitAttemptOutcome(
		t, begin, corecontract.ModelAttemptUnknown, nil, false, nil,
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
	fixture.commitAttemptOutcome(
		t,
		BeginModelDispatchResult{Lease: unknown.Lease, Attempt: unknown.Record.Attempt},
		corecontract.ModelAttemptSucceeded,
		func(f learningReviewStoreFixture) string {
			return f.verdict(t, learningcontract.ReviewDecisionApproveV1, nil)
		},
		false,
		[]byte(`{"kind":"provider_lookup","request_id":"materialization-review"}`),
	)
	if _, err := fixture.store.FinalizeLearningReview(
		context.Background(),
		FinalizeLearningReviewInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 2,
		},
	); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.store.MaterializeApprovedLearningProposal(
		context.Background(),
		MaterializeApprovedLearningProposalInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 3,
		},
	)
	if err != nil || !result.Created || result.Record.Version.ProposalRevision != 3 {
		t.Fatalf("revision-3 materialization=%+v error=%v", result, err)
	}
}

func TestMaterializeApprovedLearningProposalConcurrentSingleCreator(t *testing.T) {
	fixture := newApprovedLearningReviewStoreFixture(t)
	input := MaterializeApprovedLearningProposalInput{
		TenantID:                 fixture.proposal.Proposal.TenantID,
		ProposalID:               fixture.proposal.ProposalID,
		ExpectedProposalRevision: 2,
	}
	const workers = 8
	results := make(chan MaterializeApprovedLearningProposalResult, workers)
	errorsOut := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := fixture.store.MaterializeApprovedLearningProposal(
				context.Background(),
				input,
			)
			results <- result
			errorsOut <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatalf("concurrent materialization: %v", err)
		}
	}
	created := 0
	var versionID string
	for result := range results {
		if result.Created {
			created++
		}
		if versionID == "" {
			versionID = result.Record.VersionID
		} else if result.Record.VersionID != versionID {
			t.Fatal("concurrent calls returned different VersionIDs")
		}
	}
	if created != 1 {
		t.Fatalf("creators=%d, want 1", created)
	}
}

func TestLearningMaterializationAndInstallationAreCompatibleInBothOrders(t *testing.T) {
	for _, materializeFirst := range []bool{false, true} {
		name := "install then materialize"
		if materializeFirst {
			name = "materialize then install"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newApprovedLearningReviewStoreFixture(t)
			artifact := deriveLearningMaterializationArtifact(t, fixture)
			input := MaterializeApprovedLearningProposalInput{
				TenantID:                 fixture.proposal.Proposal.TenantID,
				ProposalID:               fixture.proposal.ProposalID,
				ExpectedProposalRevision: 2,
			}
			install := learningMaterializationInstallInput(t, fixture, artifact)
			if materializeFirst {
				if _, err := fixture.store.MaterializeApprovedLearningProposal(
					context.Background(), input,
				); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.store.InstallModule(context.Background(), install); err != nil {
					t.Fatalf("compatible InstallModule: %v", err)
				}
			} else {
				if _, err := fixture.store.InstallModule(context.Background(), install); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.store.MaterializeApprovedLearningProposal(
					context.Background(), input,
				); err != nil {
					t.Fatalf("compatible materialization: %v", err)
				}
			}
		})
	}
}

func TestLearningMaterializationAndInstallationRejectConflictInBothOrders(t *testing.T) {
	for _, materializeFirst := range []bool{false, true} {
		name := "conflicting install then materialize"
		if materializeFirst {
			name = "materialize then conflicting install"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newApprovedLearningReviewStoreFixture(t)
			artifact := deriveLearningMaterializationArtifact(t, fixture)
			input := MaterializeApprovedLearningProposalInput{
				TenantID:                 fixture.proposal.Proposal.TenantID,
				ProposalID:               fixture.proposal.ProposalID,
				ExpectedProposalRevision: 2,
			}
			install := learningMaterializationInstallInput(t, fixture, artifact)
			install.ArtifactDigest = learningMaterializationDigest(14)
			if materializeFirst {
				if _, err := fixture.store.MaterializeApprovedLearningProposal(
					context.Background(), input,
				); err != nil {
					t.Fatal(err)
				}
				var before, installationsBefore int
				if err := fixture.store.db.QueryRow(`SELECT COUNT(*) FROM content_records`).Scan(&before); err != nil {
					t.Fatal(err)
				}
				if err := fixture.store.db.QueryRow(`SELECT COUNT(*) FROM module_installations`).Scan(&installationsBefore); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.store.InstallModule(
					context.Background(), install,
				); !errors.Is(err, ErrModuleConflict) {
					t.Fatalf("conflicting InstallModule error=%v", err)
				}
				var after, installations int
				if err := fixture.store.db.QueryRow(`SELECT COUNT(*) FROM content_records`).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if err := fixture.store.db.QueryRow(`SELECT COUNT(*) FROM module_installations`).Scan(&installations); err != nil {
					t.Fatal(err)
				}
				if after != before || installations != installationsBefore {
					t.Fatal("conflicting InstallModule wrote manifest or Installation")
				}
			} else {
				if _, err := fixture.store.InstallModule(context.Background(), install); err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.store.MaterializeApprovedLearningProposal(
					context.Background(), input,
				); !errors.Is(err, ErrLearningMaterializationConflict) {
					t.Fatalf("conflicting materialization error=%v", err)
				}
				var versions int
				if err := fixture.store.db.QueryRow(`
					SELECT COUNT(*) FROM learning_proposals WHERE version_id IS NOT NULL
				`).Scan(&versions); err != nil {
					t.Fatal(err)
				}
				if versions != 0 {
					t.Fatal("conflicting materialization wrote a Version")
				}
			}
		})
	}
}

func TestLearningMaterializationAndInstallationConcurrentIdentityGate(t *testing.T) {
	for _, compatible := range []bool{true, false} {
		name := "compatible"
		if !compatible {
			name = "conflicting"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newApprovedLearningReviewStoreFixture(t)
			artifact := deriveLearningMaterializationArtifact(t, fixture)
			materializeInput := MaterializeApprovedLearningProposalInput{
				TenantID:                 fixture.proposal.Proposal.TenantID,
				ProposalID:               fixture.proposal.ProposalID,
				ExpectedProposalRevision: 2,
			}
			installInput := learningMaterializationInstallInput(t, fixture, artifact)
			if !compatible {
				installInput.ArtifactDigest = learningMaterializationDigest(13)
			}
			start := make(chan struct{})
			errorsOut := make(chan error, 2)
			go func() {
				<-start
				_, err := fixture.store.MaterializeApprovedLearningProposal(
					context.Background(), materializeInput,
				)
				errorsOut <- err
			}()
			go func() {
				<-start
				_, err := fixture.store.InstallModule(
					context.Background(), installInput,
				)
				errorsOut <- err
			}()
			close(start)
			firstErr, secondErr := <-errorsOut, <-errorsOut
			if compatible {
				if firstErr != nil || secondErr != nil {
					t.Fatalf("compatible concurrent errors=(%v, %v)", firstErr, secondErr)
				}
				return
			}
			nilCount := 0
			for _, err := range []error{firstErr, secondErr} {
				if err == nil {
					nilCount++
					continue
				}
				if !errors.Is(err, ErrModuleConflict) &&
					!errors.Is(err, ErrLearningMaterializationConflict) {
					t.Fatalf("unexpected concurrent conflict error=%v", err)
				}
			}
			if nilCount != 1 {
				t.Fatalf("conflicting concurrent success count=%d, errors=(%v, %v)", nilCount, firstErr, secondErr)
			}
		})
	}
}

func TestLearningMaterializedTargetGateIsGlobalAcrossKindAndTenant(t *testing.T) {
	fixture := newApprovedLearningReviewStoreFixture(t)
	if _, err := fixture.store.MaterializeApprovedLearningProposal(
		context.Background(),
		MaterializeApprovedLearningProposalInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 2,
		},
	); err != nil {
		t.Fatal(err)
	}

	_, skillDraft, err := corecontract.NewStaticContextV1(corecontract.StaticContextV1{
		SchemaVersion: corecontract.StaticContextSchemaVersionV1,
		Text:          "A distinct Skill Proposal claims the same global ModuleRef.",
	})
	if err != nil {
		t.Fatal(err)
	}
	skillInput := fixture.base.input(
		t,
		fixture.proposal.Proposal.Target.Version,
		"unused Knowledge body",
		localLearningEvidence("global-kind-origin", "global-kind-revision"),
		fixture.proposal.Proposal.TenantID,
		fixture.base.member.Workspace.ID,
	)
	skillInput.Proposal.Kind = learningcontract.ProposalKindSkillV1
	skillInput.DraftCanonical = skillDraft
	skill, err := fixture.store.SubmitStaticSkillProposal(
		context.Background(),
		skillInput,
	)
	if err != nil {
		t.Fatalf("submit cross-kind Proposal: %v", err)
	}

	otherTenant := newLearningProposalStoreFixtureForTenant(
		t,
		fixture.base,
		"tenant-learning-materialized-other",
	)
	otherInput := otherTenant.input(
		t,
		fixture.proposal.Proposal.Target.Version,
		"A cross-Tenant Proposal claims the same global ModuleRef.",
		localLearningEvidence("global-tenant-origin", "global-tenant-revision"),
		otherTenant.manifest.TenantID,
		otherTenant.member.Workspace.ID,
	)
	other, err := fixture.store.SubmitKnowledgeProposal(
		context.Background(),
		otherInput,
	)
	if err != nil {
		t.Fatalf("submit cross-Tenant Proposal: %v", err)
	}

	connection, err := fixture.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, proposalID := range []string{
		skill.Record.ProposalID,
		other.Record.ProposalID,
	} {
		if err := requireLearningMaterializedTargetAvailable(
			context.Background(),
			connection,
			fixture.proposal.Proposal.Target,
			proposalID,
		); !errors.Is(err, ErrLearningMaterializationConflict) {
			t.Fatalf("global target conflict for Proposal %s error=%v", proposalID, err)
		}
	}
}

func TestLearningMaterializationSemanticClosureRejectsTamper(t *testing.T) {
	tests := []struct {
		name   string
		column string
		value  any
	}{
		{name: "partial projection", column: "version_id", value: nil},
		{name: "version canonical", column: "version_canonical", value: []byte(`{}`)},
		{name: "version id", column: "version_id", value: learningMaterializationDigest(1)},
		{name: "artifact digest", column: "artifact_digest", value: learningMaterializationDigest(2)},
		{name: "artifact size", column: "artifact_size_bytes", value: int64(1)},
		{name: "verdict digest", column: "approval_verdict_digest", value: learningMaterializationDigest(3)},
		{name: "materialized time", column: "materialized_at", value: int64(-1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newApprovedLearningReviewStoreFixture(t)
			_, err := fixture.store.MaterializeApprovedLearningProposal(
				context.Background(),
				MaterializeApprovedLearningProposalInput{
					TenantID:                 fixture.proposal.Proposal.TenantID,
					ProposalID:               fixture.proposal.ProposalID,
					ExpectedProposalRevision: 2,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			execClosedFileTamperV1(
				t,
				fixture.store,
				[]string{"learning_proposals_observation_update_guard"},
				`UPDATE learning_proposals SET `+test.column+`=? WHERE proposal_id=?`,
				test.value,
				fixture.proposal.ProposalID,
			)
			connection, err := fixture.store.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			verifyErr := VerifyLearningProposalSemanticClosureV1(
				context.Background(),
				connection,
			)
			_ = connection.Close()
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

func TestLearningMaterializationRejectsChronologyTamper(t *testing.T) {
	fixture := newApprovedLearningReviewStoreFixture(t)
	_, err := fixture.store.MaterializeApprovedLearningProposal(
		context.Background(),
		MaterializeApprovedLearningProposalInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 2,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.Exec(`
		UPDATE learning_proposals
		SET materialized_at=updated_at-1
		WHERE proposal_id=?
	`, fixture.proposal.ProposalID); err == nil {
		t.Fatal("schema accepted materialized_at before updated_at")
	}
	execClosedFileTamperV1(
		t,
		fixture.store,
		[]string{"learning_proposals_observation_update_guard"},
		`
		UPDATE learning_proposals
		SET materialized_at=updated_at-1
		WHERE proposal_id=?
	`, fixture.proposal.ProposalID)
	connection, err := fixture.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	verifyErr := VerifyLearningProposalSemanticClosureV1(
		context.Background(),
		connection,
	)
	_ = connection.Close()
	if !errors.Is(verifyErr, ErrLearningProposalIntegrity) {
		t.Fatalf("semantic verifier chronology error=%v", verifyErr)
	}
	if _, err := fixture.store.GetLearningMaterializedVersion(
		context.Background(),
		fixture.proposal.Proposal.TenantID,
		fixture.proposal.ProposalID,
	); !errors.Is(err, ErrLearningMaterializationIntegrity) {
		t.Fatalf("GetLearningMaterializedVersion chronology error=%v", err)
	}
}

type learningMaterializationCounts struct {
	installations int
	activations   int
	controls      int
	catalogs      int
	runs          int
	attempts      int
	usage         int
	memories      int
	channels      int
}

func learningMaterializationSideEffectCounts(
	t *testing.T,
	store *Store,
) learningMaterializationCounts {
	t.Helper()
	var result learningMaterializationCounts
	for _, item := range []struct {
		table string
		out   *int
	}{
		{table: "module_installations", out: &result.installations},
		{table: "module_activations", out: &result.activations},
		{table: "control_snapshots", out: &result.controls},
		{table: "runtime_catalog_generations", out: &result.catalogs},
		{table: "runs", out: &result.runs},
		{table: "model_dispatch_attempts", out: &result.attempts},
		{table: "model_usage", out: &result.usage},
		{table: "agent_memory_revisions", out: &result.memories},
		{table: "channel_ingress_receipts", out: &result.channels},
	} {
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + item.table).Scan(item.out); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func learningMaterializationDigest(value byte) string {
	const hex = "0123456789abcdef"
	result := make([]byte, 64)
	for index := range result {
		result[index] = hex[(int(value)+index)%len(hex)]
	}
	return string(result)
}

func newLearningMaterializationStateFixture(
	t *testing.T,
	state LearningProposalState,
) *learningReviewStoreFixture {
	t.Helper()
	fixture := newLearningReviewStoreFixture(t, []byte(`{"max_tokens":512}`))
	if state == LearningProposalSubmitted {
		return fixture
	}
	fixture.commitAdmission(t)
	if state == LearningProposalReviewPending {
		return fixture
	}
	begin := fixture.beginAttempt(t)
	switch state {
	case LearningProposalRejected:
		fixture.commitAttemptOutcome(
			t,
			begin,
			corecontract.ModelAttemptSucceeded,
			func(f learningReviewStoreFixture) string {
				return f.verdict(
					t,
					learningcontract.ReviewDecisionRejectV1,
					[]learningcontract.ReviewIssueCodeV1{
						learningcontract.ReviewIssueMissingEvidenceV1,
					},
				)
			},
			false,
			nil,
		)
	case LearningProposalReviewFailed:
		fixture.commitAttemptOutcome(
			t, begin, corecontract.ModelAttemptFailed, nil, false, nil,
		)
	case LearningProposalReviewUnknown:
		fixture.commitAttemptOutcome(
			t, begin, corecontract.ModelAttemptUnknown, nil, false, nil,
		)
	default:
		t.Fatalf("unsupported non-approved state %s", state)
	}
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
	return fixture
}

func deriveLearningMaterializationArtifact(
	t *testing.T,
	fixture learningReviewStoreFixture,
) learningcontract.MaterializedArtifactV1 {
	t.Helper()
	proposal, err := fixture.store.GetLearningProposal(
		context.Background(),
		fixture.proposal.Proposal.TenantID,
		fixture.proposal.ProposalID,
	)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := fixture.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	derived, err := deriveLearningReview(context.Background(), connection, proposal)
	_ = connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, versionCanonical, versionID, err := learningcontract.NewMaterializedVersionV1(
		learningcontract.MaterializedVersionV1{
			SchemaVersion:     learningcontract.MaterializedVersionSchemaVersionV1,
			ProposalID:        proposal.ProposalID,
			ProposalRevision:  proposal.Revision,
			ReviewRunID:       proposal.ReviewRunID,
			ReviewerAttemptID: proposal.ReviewerAttemptID,
		},
		proposal.ProposalCanonical,
		proposal.DraftCanonical,
		derived.verdictCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := learningcontract.MaterializedVersionArtifactV1(
		versionCanonical,
		versionID,
		proposal.ProposalCanonical,
		proposal.DraftCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func learningMaterializationInstallInput(
	t *testing.T,
	fixture learningReviewStoreFixture,
	artifact learningcontract.MaterializedArtifactV1,
) InstallModuleInput {
	t.Helper()
	manifestRef, err := ComputeContentDigest(
		ContentModuleManifest,
		moduleManifestMediaType,
		artifact.ManifestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return InstallModuleInput{
		InstallationID:      "installation-learning-materialized",
		ModuleID:            fixture.proposal.Proposal.Target.ID,
		ExactVersion:        fixture.proposal.Proposal.Target.Version,
		ExpectedManifestRef: manifestRef,
		ManifestBytes:       bytes.Clone(artifact.ManifestCanonical),
		ArtifactDigest:      artifact.ArtifactDigest,
	}
}
