package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type learningMaterializedArtifactFixtureV1 struct {
	Proposal          learningcontract.ProposalV1
	ProposalCanonical []byte
	DraftCanonical    []byte
	Version           learningcontract.MaterializedVersionV1
	VersionCanonical  []byte
	VersionID         string
	Artifact          learningcontract.MaterializedArtifactV1
}

func TestLearningMaterializedArtifactExportIsExactIdempotentAndConcurrent(
	t *testing.T,
) {
	for _, kind := range []learningcontract.ProposalKindV1{
		learningcontract.ProposalKindKnowledgeV1,
		learningcontract.ProposalKindSkillV1,
	} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newLearningMaterializedArtifactFixtureV1(t, kind)
			root := t.TempDir()
			target := filepath.Join(root, "handoff")
			manifestBefore := bytes.Clone(fixture.Artifact.ManifestCanonical)
			payloadBefore := bytes.Clone(fixture.Artifact.PayloadCanonical)

			resolved, created, err := exportLearningMaterializedArtifactV1(
				context.Background(),
				target,
				fixture.Artifact,
			)
			if err != nil || !created {
				t.Fatalf("first export path=%q created=%v error=%v", resolved, created, err)
			}
			if err := verifyLearningMaterializedArtifactDirectoryV1(
				context.Background(),
				resolved,
				fixture.Artifact,
			); err != nil {
				t.Fatalf("verify first export: %v", err)
			}
			fixture.Artifact.ManifestCanonical[0] ^= 1
			fixture.Artifact.PayloadCanonical[0] ^= 1
			storedManifest, err := os.ReadFile(filepath.Join(
				resolved,
				moduleapi.ArtifactManifestPath,
			))
			if err != nil || !bytes.Equal(storedManifest, manifestBefore) {
				t.Fatalf("export aliased manifest: %v", err)
			}
			storedPayload, err := os.ReadFile(filepath.Join(
				resolved,
				filepath.FromSlash(fixture.Artifact.EntrypointPath),
			))
			if err != nil || !bytes.Equal(storedPayload, payloadBefore) {
				t.Fatalf("export aliased payload: %v", err)
			}
			fixture.Artifact.ManifestCanonical = manifestBefore
			fixture.Artifact.PayloadCanonical = payloadBefore
			resolvedRetry, retryCreated, err := exportLearningMaterializedArtifactV1(
				context.Background(),
				target,
				fixture.Artifact,
			)
			if err != nil || retryCreated || !samePath(resolvedRetry, resolved) {
				t.Fatalf(
					"exact retry path=%q created=%v error=%v",
					resolvedRetry,
					retryCreated,
					err,
				)
			}
			manifestPath := filepath.Join(resolved, moduleapi.ArtifactManifestPath)
			payloadPath := filepath.Join(
				resolved,
				filepath.FromSlash(fixture.Artifact.EntrypointPath),
			)
			if err := os.Chmod(manifestPath, 0o400); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(payloadPath, 0o400); err != nil {
				_ = os.Chmod(manifestPath, 0o600)
				t.Fatal(err)
			}
			_, readOnlyCreated, readOnlyErr :=
				exportLearningMaterializedArtifactV1(
					context.Background(),
					target,
					fixture.Artifact,
				)
			manifestModeErr := os.Chmod(manifestPath, 0o600)
			payloadModeErr := os.Chmod(payloadPath, 0o600)
			if readOnlyErr != nil || readOnlyCreated ||
				manifestModeErr != nil || payloadModeErr != nil {
				t.Fatalf(
					"read-only exact retry created=%v error=%v restore=(%v,%v)",
					readOnlyCreated,
					readOnlyErr,
					manifestModeErr,
					payloadModeErr,
				)
			}

			concurrentTarget := filepath.Join(root, "concurrent")
			const callers = 8
			start := make(chan struct{})
			results := make([]struct {
				created bool
				err     error
			}, callers)
			var wait sync.WaitGroup
			for index := range results {
				wait.Add(1)
				go func(index int) {
					defer wait.Done()
					<-start
					_, results[index].created, results[index].err =
						exportLearningMaterializedArtifactV1(
							context.Background(),
							concurrentTarget,
							fixture.Artifact,
						)
				}(index)
			}
			close(start)
			wait.Wait()
			createdCount := 0
			for _, result := range results {
				if result.err != nil {
					t.Fatalf("concurrent export error: %v", result.err)
				}
				if result.created {
					createdCount++
				}
			}
			if createdCount != 1 {
				t.Fatalf("concurrent creators=%d want 1", createdCount)
			}
			assertNoLearningMaterializeStagesV1(t, root)
		})
	}
}

func TestLearningMaterializedArtifactExportRejectsCancellationAndDrift(
	t *testing.T,
) {
	fixture := newLearningMaterializedArtifactFixtureV1(
		t,
		learningcontract.ProposalKindSkillV1,
	)
	root := t.TempDir()
	cancelledTarget := filepath.Join(root, "cancelled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := exportLearningMaterializedArtifactV1(
		ctx,
		cancelledTarget,
		fixture.Artifact,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled export error=%v", err)
	}
	if _, err := os.Lstat(cancelledTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled export created target: %v", err)
	}

	target := filepath.Join(root, "drifted")
	if _, created, err := exportLearningMaterializedArtifactV1(
		context.Background(),
		target,
		fixture.Artifact,
	); err != nil || !created {
		t.Fatalf("seed drift target created=%v error=%v", created, err)
	}
	payloadPath := filepath.Join(
		target,
		filepath.FromSlash(fixture.Artifact.EntrypointPath),
	)
	if err := os.WriteFile(payloadPath, []byte(`{"changed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := exportLearningMaterializedArtifactV1(
		context.Background(),
		target,
		fixture.Artifact,
	); err == nil {
		t.Fatal("drifted existing handoff was accepted")
	}
	assertNoLearningMaterializeStagesV1(t, root)
}

func TestLearningMaterializedArtifactExportRejectsUnsafeExistingHandoff(
	t *testing.T,
) {
	fixture := newLearningMaterializedArtifactFixtureV1(
		t,
		learningcontract.ProposalKindKnowledgeV1,
	)
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{
			name: "root symlink",
			mutate: func(t *testing.T, root string, target string) {
				realTarget := filepath.Join(root, "real-handoff")
				if err := os.Rename(target, realTarget); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(realTarget, target); err != nil {
					t.Skipf("directory symlinks are unavailable: %v", err)
				}
			},
		},
		{
			name: "payload hardlink",
			mutate: func(t *testing.T, root string, target string) {
				payload := filepath.Join(
					target,
					filepath.FromSlash(fixture.Artifact.EntrypointPath),
				)
				external := filepath.Join(root, "hardlink-source.json")
				if err := os.WriteFile(
					external,
					fixture.Artifact.PayloadCanonical,
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(payload); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(external, payload); err != nil {
					t.Skipf("hardlinks are unavailable: %v", err)
				}
			},
		},
		{
			name: "payload symlink",
			mutate: func(t *testing.T, root string, target string) {
				payload := filepath.Join(
					target,
					filepath.FromSlash(fixture.Artifact.EntrypointPath),
				)
				external := filepath.Join(root, "symlink-source.json")
				if err := os.WriteFile(
					external,
					fixture.Artifact.PayloadCanonical,
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(payload); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, payload); err != nil {
					t.Skipf("symlinks are unavailable: %v", err)
				}
			},
		},
		{
			name: "oversized payload",
			mutate: func(t *testing.T, _ string, target string) {
				payload := filepath.Join(
					target,
					filepath.FromSlash(fixture.Artifact.EntrypointPath),
				)
				if err := os.Truncate(
					payload,
					moduleapi.DefaultArtifactMaxFileBytes+1,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra file",
			mutate: func(t *testing.T, _ string, target string) {
				if err := os.WriteFile(
					filepath.Join(target, "unexpected.txt"),
					[]byte("unexpected"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "handoff")
			if _, created, err := exportLearningMaterializedArtifactV1(
				context.Background(),
				target,
				fixture.Artifact,
			); err != nil || !created {
				t.Fatalf("seed handoff created=%v error=%v", created, err)
			}
			test.mutate(t, root, target)
			if _, _, err := exportLearningMaterializedArtifactV1(
				context.Background(),
				target,
				fixture.Artifact,
			); err == nil {
				t.Fatal("unsafe existing handoff was accepted")
			}
			assertNoLearningMaterializeStagesV1(t, root)
		})
	}
}

func TestLearningMaterializedArtifactExportRejectsSymlinkParent(
	t *testing.T,
) {
	fixture := newLearningMaterializedArtifactFixtureV1(
		t,
		learningcontract.ProposalKindSkillV1,
	)
	root := t.TempDir()
	realParent := filepath.Join(root, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasParent := filepath.Join(root, "alias-parent")
	if err := os.Symlink(realParent, aliasParent); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	if _, _, err := exportLearningMaterializedArtifactV1(
		context.Background(),
		filepath.Join(aliasParent, "handoff"),
		fixture.Artifact,
	); err == nil {
		t.Fatal("handoff with a symlink parent was accepted")
	}
	if _, err := os.Lstat(filepath.Join(realParent, "handoff")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected symlink parent created a target: %v", err)
	}
}

func TestLearningMaterializePathRecheckRejectsParentRedirection(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	if err := os.WriteFile(databasePath, []byte("store"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Join(root, "artifacts")
	if err := os.Mkdir(artifactRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	handoffParent := filepath.Join(root, "handoff-parent")
	if err := os.Mkdir(handoffParent, 0o700); err != nil {
		t.Fatal(err)
	}
	handoffTarget, err := resolveNewTarget(
		filepath.Join(handoffParent, "candidate"),
		"Learning artifact handoff",
	)
	if err != nil {
		t.Fatal(err)
	}
	movedParent := filepath.Join(root, "handoff-parent-before-redirect")
	if err := os.Rename(handoffParent, movedParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(artifactRoot, handoffParent); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	if _, err := recheckLearningMaterializeHandoffPathsV1(
		handoffTarget,
		artifactRoot,
		databasePath,
	); err == nil {
		t.Fatal("redirected handoff parent passed the post-materialization fence")
	}
	if _, err := os.Lstat(filepath.Join(artifactRoot, "candidate")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path recheck created a target in the active artifact root: %v", err)
	}
}

func TestLearningMaterializedArtifactsReachExistingVerifyAndDryRunHandoff(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	databaseBefore := snapshotModuleDryRunDatabaseV1(t, databasePath)
	artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)

	for _, kind := range []learningcontract.ProposalKindV1{
		learningcontract.ProposalKindKnowledgeV1,
		learningcontract.ProposalKindSkillV1,
	} {
		t.Run(string(kind), func(t *testing.T) {
			fixture := newLearningMaterializedArtifactFixtureV1(t, kind)
			handoff := filepath.Join(root, "handoff-"+strings.ToLower(string(kind)))
			if _, created, err := exportLearningMaterializedArtifactV1(
				context.Background(),
				handoff,
				fixture.Artifact,
			); err != nil || !created {
				t.Fatalf("export created=%v error=%v", created, err)
			}

			var verifyOutput bytes.Buffer
			if err := runModuleVerify(
				context.Background(),
				[]string{"--artifact", handoff},
				&verifyOutput,
				&bytes.Buffer{},
			); err != nil {
				t.Fatalf("module-verify generated artifact: %v", err)
			}
			var report moduleconformance.Report
			if err := json.Unmarshal(verifyOutput.Bytes(), &report); err != nil ||
				report.Module.ID != fixture.Version.Target.ID ||
				report.ArtifactDigest != fixture.Artifact.ArtifactDigest ||
				report.ArtifactSizeBytes != fixture.Artifact.ArtifactSizeBytes {
				t.Fatalf("module-verify report=%+v error=%v", report, err)
			}

			var plan []byte
			switch kind {
			case learningcontract.ProposalKindKnowledgeV1:
				_, source, err := moduleapi.RestoreKnowledgeSourceV1(
					fixture.DraftCanonical,
				)
				if err != nil {
					t.Fatal(err)
				}
				plan = newEnabledModuleApplyKnowledgePlanV1(
					t,
					moduleApplyKnowledgeFixtureV1{
						ModuleID:          fixture.Version.Target.ID,
						ExactVersion:      fixture.Version.Target.Version,
						ArtifactDirectory: handoff,
						ArtifactDigest:    fixture.Artifact.ArtifactDigest,
						ArtifactSizeBytes: fixture.Artifact.ArtifactSizeBytes,
						Source:            source,
					},
					1,
					moduleapi.KnowledgeScopeRuleV1{
						TenantID: defaultTenantID, WorkspaceID: "*",
						AgentID: "*", TaskInputRef: "*",
					},
				)
			case learningcontract.ProposalKindSkillV1:
				plan = newNamedEnabledDeclarativeModuleApplyPlanV1(
					t,
					moduleApplyDeclarativeFixtureV1{
						ModuleID:          fixture.Version.Target.ID,
						InstanceID:        "learning-materialized-skill",
						ArtifactDirectory: handoff,
						ArtifactDigest:    fixture.Artifact.ArtifactDigest,
						ArtifactSizeBytes: fixture.Artifact.ArtifactSizeBytes,
					},
					moduleApplyTestProfileID,
					1,
					1,
				)
			default:
				t.Fatalf("unsupported kind %q", kind)
			}
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(
					root,
					"learning-"+strings.ToLower(string(kind))+"-plan.json",
				),
				plan,
			)
			dryRun, err := runModuleDryRunFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				handoff,
				"",
			)
			if err != nil || dryRun.Status != moduleApplyStatusWouldApply {
				t.Fatalf("module-dry-run result=%+v error=%v", dryRun, err)
			}
			if _, err := os.Lstat(filepath.Join(
				artifactRoot,
				fixture.Artifact.ArtifactDigest,
			)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("dry-run published artifact: %v", err)
			}
		})
	}
	if databaseAfter := snapshotModuleDryRunDatabaseV1(
		t,
		databasePath,
	); !reflect.DeepEqual(databaseAfter, databaseBefore) {
		t.Fatal("verify/dry-run mutated Current Store bytes or sidecars")
	}
	if artifactsAfter := snapshotModuleDryRunTreeV1(
		t,
		artifactRoot,
	); !reflect.DeepEqual(artifactsAfter, artifactsBefore) {
		t.Fatal("verify/dry-run mutated active artifact root")
	}
}

func newLearningMaterializedArtifactFixtureV1(
	t *testing.T,
	kind learningcontract.ProposalKindV1,
) learningMaterializedArtifactFixtureV1 {
	t.Helper()
	target := moduleapi.Ref{Version: moduleApplyTestVersion}
	var draft []byte
	switch kind {
	case learningcontract.ProposalKindKnowledgeV1:
		target.ID = "freeagent.learning.knowledge.materialized"
		_, canonical, _, err := moduleapi.NewKnowledgeSourceV1(
			moduleapi.KnowledgeSourceV1{
				SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
				ID:            target.ID,
				Version:       target.Version,
				Chunks: []moduleapi.KnowledgeChunkV1{{
					Document: moduleapi.KnowledgeDocumentRefV1{
						ID: "document-learning", Version: "1",
						Digest: learningMaterializeTestDigestV1("1"),
					},
					ChunkID: "chunk-learning",
					Text:    "Approved knowledge remains inert until an Operator binds it.",
					VisibleTo: []moduleapi.KnowledgeScopeRuleV1{{
						TenantID: defaultTenantID, WorkspaceID: "*",
						AgentID: "*", TaskInputRef: "*",
					}},
				}},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		draft = canonical
	case learningcontract.ProposalKindSkillV1:
		target.ID = "freeagent.learning.skill.materialized"
		_, canonical, err := corecontract.NewStaticContextV1(
			corecontract.StaticContextV1{
				SchemaVersion: corecontract.StaticContextSchemaVersionV1,
				Text:          "Use exact immutable evidence and leave authority decisions to the Operator.",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		draft = canonical
	default:
		t.Fatalf("unsupported Proposal kind %q", kind)
	}
	proposal, proposalCanonical, proposalID, err := learningcontract.NewProposalV1(
		learningcontract.ProposalV1{
			SchemaVersion: learningcontract.ProposalSchemaVersionV1,
			Kind:          kind,
			TenantID:      defaultTenantID,
			Workspace: corecontract.WorkspaceRef{
				ID: "workspace-learning", Version: "1",
				Digest: learningMaterializeTestDigestV1("a"),
			},
			ProposerAgent: corecontract.AgentRef{
				ID: "agent-learning", Version: "1",
				Digest: learningMaterializeTestDigestV1("b"),
			},
			ProposerProfile: corecontract.ProfileRef{
				ID: "profile-learning", Version: "1",
				Digest: learningMaterializeTestDigestV1("c"),
			},
			ProposerRunID:          "run-learning",
			ProposerManifestDigest: learningMaterializeTestDigestV1("d"),
			ProposerMember: corecontract.MemberSnapshotRef{
				MemberID: "member-learning",
				Digest:   learningMaterializeTestDigestV1("e"),
			},
			ProposerResultRef: learningMaterializeTestDigestV1("f"),
			Target:            target,
		},
		draft,
		learningcontract.SourceEvidenceV1{
			Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, verdictCanonical, _, err := learningcontract.NewReviewVerdictV1(
		learningcontract.ReviewVerdictV1{
			SchemaVersion:      learningcontract.ReviewVerdictSchemaVersionV1,
			ProposalID:         proposalID,
			SourceFingerprint:  proposal.SourceFingerprint,
			ContentFingerprint: proposal.ContentFingerprint,
			Decision:           learningcontract.ReviewDecisionApproveV1,
			IssueCodes:         []learningcontract.ReviewIssueCodeV1{},
			BoundedReason:      "Approved exact candidate for inert materialization.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	version, versionCanonical, versionID, err :=
		learningcontract.NewMaterializedVersionV1(
			learningcontract.MaterializedVersionV1{
				SchemaVersion:     learningcontract.MaterializedVersionSchemaVersionV1,
				ProposalID:        proposalID,
				ProposalRevision:  2,
				ReviewRunID:       "run-learning-review",
				ReviewerAttemptID: "attempt-learning-review",
			},
			proposalCanonical,
			draft,
			verdictCanonical,
		)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := learningcontract.MaterializedVersionArtifactV1(
		versionCanonical,
		versionID,
		proposalCanonical,
		draft,
	)
	if err != nil {
		t.Fatal(err)
	}
	return learningMaterializedArtifactFixtureV1{
		Proposal:          proposal,
		ProposalCanonical: proposalCanonical,
		DraftCanonical:    bytes.Clone(draft),
		Version:           version,
		VersionCanonical:  versionCanonical,
		VersionID:         versionID,
		Artifact:          artifact,
	}
}

func learningMaterializeTestDigestV1(character string) string {
	return strings.Repeat(character, 64)
}

func assertNoLearningMaterializeStagesV1(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), learningMaterializeStagePrefixV1) {
			t.Fatalf("temporary Learning materialize stage remains: %s", entry.Name())
		}
	}
}
