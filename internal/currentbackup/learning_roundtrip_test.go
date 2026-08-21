package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestLearningProposalBundleRoundTripPreservesExactClosure(t *testing.T) {
	fixture := newLearningProposalBackupFixture(t)
	bundle := filepath.Join(t.TempDir(), "learning.bundle")
	if _, err := CreateBundle(
		context.Background(),
		fixture.base.databasePath,
		fixture.base.artifactRoot,
		bundle,
		"learning-roundtrip-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle: %v", err)
	}
	if _, err := VerifyBundle(context.Background(), bundle); err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	restoredDatabase := filepath.Join(t.TempDir(), "restored.sqlite")
	restoredArtifacts := filepath.Join(t.TempDir(), "restored-artifacts")
	if err := RestoreBundle(
		context.Background(),
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle: %v", err)
	}
	restoredStore, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		restoredDatabase,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredStore.Close()
	restored, err := restoredStore.GetKnowledgeProposal(
		context.Background(),
		fixture.record.Proposal.TenantID,
		fixture.record.ProposalID,
	)
	if err != nil {
		t.Fatalf("GetKnowledgeProposal(restored): %v", err)
	}
	if restored.ProposalID != fixture.record.ProposalID ||
		restored.ProposerAttemptID != fixture.record.ProposerAttemptID ||
		restored.State != fixture.record.State ||
		restored.Revision != fixture.record.Revision ||
		!restored.CreatedAt.Equal(fixture.record.CreatedAt) ||
		!bytes.Equal(restored.ProposalCanonical, fixture.record.ProposalCanonical) ||
		!bytes.Equal(restored.DraftCanonical, fixture.record.DraftCanonical) {
		t.Fatalf("restored=%+v source=%+v", restored, fixture.record)
	}
}

func TestLearningProposalSemanticGateRejectsTamper(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*testing.T, *sql.DB, learningProposalBackupFixture)
	}{
		{
			name: "content fingerprint projection",
			tamper: func(t *testing.T, database *sql.DB, fixture learningProposalBackupFixture) {
				t.Helper()
				execClosedFileTamperV1(
					t,
					database,
					[]string{"learning_proposals_observation_update_guard"},
					`
					UPDATE learning_proposals
					SET content_fingerprint=?
					WHERE proposal_id=?
				`,
					moduleapi.Digest("freeagent.learning-tamper/v1", []byte("changed")),
					fixture.record.ProposalID,
				)
			},
		},
		{
			name: "proposal canonical",
			tamper: func(t *testing.T, database *sql.DB, fixture learningProposalBackupFixture) {
				t.Helper()
				changed := []byte(`{}`)
				execClosedFileTamperV1(
					t,
					database,
					[]string{"learning_proposals_observation_update_guard"},
					`
					UPDATE learning_proposals
					SET proposal_canonical=?, proposal_size_bytes=?
					WHERE proposal_id=?
				`,
					changed,
					len(changed),
					fixture.record.ProposalID,
				)
			},
		},
		{
			name: "draft canonical",
			tamper: func(t *testing.T, database *sql.DB, fixture learningProposalBackupFixture) {
				t.Helper()
				changed := []byte(`{}`)
				execClosedFileTamperV1(
					t,
					database,
					[]string{"learning_proposals_observation_update_guard"},
					`
					UPDATE learning_proposals
					SET draft_canonical=?, draft_size_bytes=?
					WHERE proposal_id=?
				`,
					changed,
					len(changed),
					fixture.record.ProposalID,
				)
			},
		},
		{
			name: "different valid proposer attempt",
			tamper: func(t *testing.T, database *sql.DB, fixture learningProposalBackupFixture) {
				t.Helper()
				execClosedFileTamperV1(
					t,
					database,
					[]string{"learning_proposals_observation_update_guard"},
					`
					UPDATE learning_proposals
					SET proposer_attempt_id=?
					WHERE proposal_id=?
				`,
					fixture.base.pendingAttemptID,
					fixture.record.ProposalID,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLearningProposalBackupFixture(t)
			database, err := sql.Open(
				"sqlite",
				sqliteFileURI(fixture.base.databasePath, "rw", "foreign_keys(1)"),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			test.tamper(t, database, fixture)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				fixture.base.databasePath,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("semantic gate error=%v", err)
			}
			bundle := filepath.Join(t.TempDir(), "tampered.bundle")
			if _, err := CreateBundle(
				context.Background(),
				fixture.base.databasePath,
				fixture.base.artifactRoot,
				bundle,
				"learning-tamper-test/v1",
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("CreateBundle tamper error=%v", err)
			}
		})
	}
}

type learningProposalBackupFixture struct {
	base   backupFixture
	record currentstore.LearningProposalRecord
}

func newLearningProposalBackupFixture(t *testing.T) learningProposalBackupFixture {
	t.Helper()
	base := newBackupFixture(t)
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		base.databasePath,
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
	terminal, err := store.GetTerminalRunResult(
		context.Background(),
		base.successfulRunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.State != corecontract.ModelAttemptSucceeded {
		t.Fatalf("terminal=%+v", terminal)
	}

	readonly, err := openReadOnlyDatabase(base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var manifestCanonical, memberCanonical []byte
	var resultRef string
	if err := readonly.QueryRow(`
		SELECT manifest.canonical_json, member.canonical_json, attempt.result_ref
		FROM run_manifests AS manifest
		JOIN member_execution_snapshots AS member
		  ON member.run_id=manifest.run_id AND member.member_id=?
		JOIN model_dispatch_attempts AS attempt
		  ON attempt.run_id=manifest.run_id
		 AND attempt.member_id=member.member_id
		 AND attempt.attempt_id=?
		WHERE manifest.run_id=?
	`,
		terminal.MemberID,
		terminal.AttemptID,
		base.successfulRunID,
	).Scan(&manifestCanonical, &memberCanonical, &resultRef); err != nil {
		_ = readonly.Close()
		t.Fatal(err)
	}
	if err := readonly.Close(); err != nil {
		t.Fatal(err)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil {
		t.Fatal(err)
	}
	target := moduleapi.Ref{
		ID: "freeagent.backup.knowledge.learning", Version: "1.0.0",
	}
	_, draftCanonical, _, err := moduleapi.NewKnowledgeSourceV1(
		moduleapi.KnowledgeSourceV1{
			SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
			ID:            target.ID,
			Version:       target.Version,
			Chunks: []moduleapi.KnowledgeChunkV1{{
				Document: moduleapi.KnowledgeDocumentRefV1{
					ID:      "learning-backup-document",
					Version: "1",
					Digest: moduleapi.Digest(
						"freeagent.learning-backup-document/v1",
						[]byte("exact"),
					),
				},
				ChunkID: "learning-backup-chunk",
				Text:    "The exact Learning Proposal survives complete backup and restore.",
				VisibleTo: []moduleapi.KnowledgeScopeRuleV1{{
					TenantID:     manifest.TenantID,
					WorkspaceID:  member.Workspace.ID,
					AgentID:      "*",
					TaskInputRef: "*",
				}},
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := store.SubmitKnowledgeProposal(
		context.Background(),
		currentstore.SubmitKnowledgeProposalInput{
			Proposal: learningcontract.ProposalV1{
				SchemaVersion:          learningcontract.ProposalSchemaVersionV1,
				Kind:                   learningcontract.ProposalKindKnowledgeV1,
				TenantID:               manifest.TenantID,
				Workspace:              member.Workspace,
				ProposerAgent:          member.Agent,
				ProposerProfile:        member.Profile,
				ProposerRunID:          manifest.RunID,
				ProposerManifestDigest: manifest.ManifestDigest,
				ProposerMember: corecontract.MemberSnapshotRef{
					MemberID: member.MemberID,
					Digest:   member.MemberSnapshotDigest,
				},
				ProposerResultRef: resultRef,
				Target:            target,
			},
			DraftCanonical: draftCanonical,
			SourceEvidence: learningcontract.SourceEvidenceV1{
				Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
			},
		},
	)
	if err != nil {
		t.Fatalf("SubmitKnowledgeProposal: %v", err)
	}
	if !submitted.Created {
		t.Fatal("Learning Proposal was not created")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return learningProposalBackupFixture{base: base, record: submitted.Record}
}
