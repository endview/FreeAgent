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

func TestStaticSkillProposalBundleRoundTripPreservesMixedLearningClosure(
	t *testing.T,
) {
	fixture := newStaticSkillBackupFixture(t)
	bundle := filepath.Join(t.TempDir(), "mixed-learning.bundle")
	if _, err := CreateBundle(
		context.Background(),
		fixture.knowledge.base.databasePath,
		fixture.knowledge.base.artifactRoot,
		bundle,
		"static-skill-roundtrip-test/v1",
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
		t.Fatalf("OpenExistingCurrentStore(restored): %v", err)
	}
	defer restoredStore.Close()

	restoredKnowledge, err := restoredStore.GetKnowledgeProposal(
		context.Background(),
		fixture.knowledge.record.Proposal.TenantID,
		fixture.knowledge.record.ProposalID,
	)
	if err != nil {
		t.Fatalf("GetKnowledgeProposal(restored): %v", err)
	}
	assertExactLearningProposalRecord(
		t,
		restoredKnowledge,
		fixture.knowledge.record,
	)
	restoredSkill, err := restoredStore.GetStaticSkillProposal(
		context.Background(),
		fixture.skill.Proposal.TenantID,
		fixture.skill.ProposalID,
	)
	if err != nil {
		t.Fatalf("GetStaticSkillProposal(restored): %v", err)
	}
	assertExactLearningProposalRecord(t, restoredSkill, fixture.skill)

	database, err := openReadOnlyDatabase(restoredDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var knowledgeCount, skillCount int
	if err := database.QueryRow(`
		SELECT
			SUM(CASE WHEN proposal_kind='KNOWLEDGE' THEN 1 ELSE 0 END),
			SUM(CASE WHEN proposal_kind='SKILL' THEN 1 ELSE 0 END)
		FROM learning_proposals
	`).Scan(&knowledgeCount, &skillCount); err != nil {
		t.Fatal(err)
	}
	if knowledgeCount != 1 || skillCount != 1 {
		t.Fatalf(
			"restored Learning kinds Knowledge=%d Skill=%d, want 1/1",
			knowledgeCount,
			skillCount,
		)
	}
}

func TestStaticSkillProposalSemanticGateRejectsSkillSpecificTamper(
	t *testing.T,
) {
	tests := []struct {
		name   string
		tamper func(*testing.T, *sql.DB, staticSkillBackupFixture)
	}{
		{
			name: "kind projection",
			tamper: func(
				t *testing.T,
				database *sql.DB,
				fixture staticSkillBackupFixture,
			) {
				t.Helper()
				execClosedFileTamperV1(
					t,
					database,
					[]string{"learning_proposals_observation_update_guard"},
					`
					UPDATE learning_proposals
					SET proposal_kind='KNOWLEDGE'
					WHERE proposal_id=?
				`,
					fixture.skill.ProposalID,
				)
			},
		},
		{
			name: "different canonical static draft and coordinated size",
			tamper: func(
				t *testing.T,
				database *sql.DB,
				fixture staticSkillBackupFixture,
			) {
				t.Helper()
				_, changed, err := corecontract.NewStaticContextV1(
					corecontract.StaticContextV1{
						SchemaVersion: corecontract.StaticContextSchemaVersionV1,
						Text:          "Skill: this is a different valid canonical draft.",
					},
				)
				if err != nil {
					t.Fatal(err)
				}
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
					fixture.skill.ProposalID,
				)
			},
		},
		{
			name: "target projection",
			tamper: func(
				t *testing.T,
				database *sql.DB,
				fixture staticSkillBackupFixture,
			) {
				t.Helper()
				execClosedFileTamperV1(
					t,
					database,
					[]string{"learning_proposals_observation_update_guard"},
					`
					UPDATE learning_proposals
					SET target_version='9.9.9'
					WHERE proposal_id=?
				`,
					fixture.skill.ProposalID,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStaticSkillBackupFixture(t)
			bundle := filepath.Join(t.TempDir(), "valid.bundle")
			if _, err := CreateBundle(
				context.Background(),
				fixture.knowledge.base.databasePath,
				fixture.knowledge.base.artifactRoot,
				bundle,
				"static-skill-tamper-test/v1",
			); err != nil {
				t.Fatalf("CreateBundle(valid): %v", err)
			}

			bundleDatabase, err := sql.Open(
				"sqlite",
				sqliteFileURI(
					filepath.Join(bundle, databaseName),
					"rw",
					"foreign_keys(1)",
				),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = bundleDatabase.Close() })
			test.tamper(t, bundleDatabase, fixture)
			if err := bundleDatabase.Close(); err != nil {
				t.Fatal(err)
			}
			rewriteBundleDatabaseIdentity(t, bundle)
			if _, err := VerifyBundle(
				context.Background(),
				bundle,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("VerifyBundle(tampered) error=%v, want ErrIntegrity", err)
			}

			sourceDatabase, err := sql.Open(
				"sqlite",
				sqliteFileURI(
					fixture.knowledge.base.databasePath,
					"rw",
					"foreign_keys(1)",
				),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sourceDatabase.Close() })
			test.tamper(t, sourceDatabase, fixture)
			if err := sourceDatabase.Close(); err != nil {
				t.Fatal(err)
			}
			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				fixture.knowledge.base.databasePath,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("semantic gate error=%v, want ErrIntegrity", err)
			}
			if _, err := CreateBundle(
				context.Background(),
				fixture.knowledge.base.databasePath,
				fixture.knowledge.base.artifactRoot,
				filepath.Join(t.TempDir(), "rejected.bundle"),
				"static-skill-tamper-test/v1",
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("CreateBundle(tampered) error=%v, want ErrIntegrity", err)
			}
		})
	}
}

type staticSkillBackupFixture struct {
	knowledge learningProposalBackupFixture
	skill     currentstore.LearningProposalRecord
}

func newStaticSkillBackupFixture(t *testing.T) staticSkillBackupFixture {
	t.Helper()
	knowledge := newLearningProposalBackupFixture(t)
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		knowledge.base.databasePath,
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

	_, draftCanonical, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text:          "Skill: preserve exact static implementation guidance.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	lineage := knowledge.record.Proposal
	submitted, err := store.SubmitStaticSkillProposal(
		context.Background(),
		currentstore.SubmitStaticSkillProposalInput{
			Proposal: learningcontract.ProposalV1{
				SchemaVersion:          learningcontract.ProposalSchemaVersionV1,
				Kind:                   learningcontract.ProposalKindSkillV1,
				TenantID:               lineage.TenantID,
				Workspace:              lineage.Workspace,
				ProposerAgent:          lineage.ProposerAgent,
				ProposerProfile:        lineage.ProposerProfile,
				ProposerRunID:          lineage.ProposerRunID,
				ProposerManifestDigest: lineage.ProposerManifestDigest,
				ProposerMember:         lineage.ProposerMember,
				ProposerResultRef:      lineage.ProposerResultRef,
				Target: moduleapi.Ref{
					ID:      "freeagent.backup.skill.learning",
					Version: "1.0.0",
				},
			},
			DraftCanonical: draftCanonical,
			SourceEvidence: learningcontract.SourceEvidenceV1{
				Mechanism:        learningcontract.SourceMechanismLocalImportV1,
				OriginMaterial:   []byte("local://static-skill-backup-fixture"),
				RevisionMaterial: []byte("static-skill-backup-fixture/v1"),
			},
		},
	)
	if err != nil {
		t.Fatalf("SubmitStaticSkillProposal: %v", err)
	}
	if !submitted.Created {
		t.Fatal("static Skill Proposal was not created")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return staticSkillBackupFixture{
		knowledge: knowledge,
		skill:     submitted.Record,
	}
}

func assertExactLearningProposalRecord(
	t *testing.T,
	got currentstore.LearningProposalRecord,
	want currentstore.LearningProposalRecord,
) {
	t.Helper()
	if got.ProposalID != want.ProposalID ||
		got.Proposal != want.Proposal ||
		!bytes.Equal(got.ProposalCanonical, want.ProposalCanonical) ||
		!bytes.Equal(got.DraftCanonical, want.DraftCanonical) ||
		got.ProposerAttemptID != want.ProposerAttemptID ||
		got.State != want.State ||
		got.Revision != want.Revision ||
		got.CreatedAt != want.CreatedAt ||
		got.UpdatedAt != want.UpdatedAt {
		t.Fatalf("restored=%+v source=%+v", got, want)
	}
}
