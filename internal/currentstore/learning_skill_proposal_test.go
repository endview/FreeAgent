package currentstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestSubmitStaticSkillProposalCreatesGetsRetriesAndDetachesBytes(
	t *testing.T,
) {
	fixture := newLearningProposalStoreFixture(t)
	input := staticSkillProposalInput(
		t,
		fixture,
		"freeagent.test.skill.learning",
		"1.0.0",
		"Prefer the smallest exact tool contract that satisfies the task.",
		localLearningEvidence("skill-origin", "skill-revision"),
	)
	pristine := cloneLearningSubmitInput(input)
	before := snapshotLearningOutsideScope(t, fixture.store)
	beforeContent := learningSkillContentRecordCount(t, fixture.store)

	first, err := fixture.store.SubmitStaticSkillProposal(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("SubmitStaticSkillProposal: %v", err)
	}
	if !first.Created ||
		first.Record.Proposal.Kind != learningcontract.ProposalKindSkillV1 ||
		first.Record.State != LearningProposalSubmitted ||
		first.Record.Revision != 0 ||
		first.Record.ProposerAttemptID != fixture.attemptID ||
		!first.Record.CreatedAt.Equal(first.Record.UpdatedAt) {
		t.Fatalf("first result=%+v", first)
	}
	if _, err := learningcontract.RestoreProposalV1(
		first.Record.ProposalCanonical,
		first.Record.DraftCanonical,
		first.Record.ProposalID,
	); err != nil {
		t.Fatalf("restore stored Skill Proposal: %v", err)
	}
	staticContext, err := corecontract.RestoreStaticContextV1(
		first.Record.DraftCanonical,
	)
	if err != nil {
		t.Fatalf("restore stored static Skill Draft: %v", err)
	}
	if staticContext.Text !=
		"Prefer the smallest exact tool contract that satisfies the task." {
		t.Fatalf("stored static Skill text=%q", staticContext.Text)
	}

	afterCreate := snapshotLearningOutsideScope(t, fixture.store)
	if afterCreate.LearningProposals != before.LearningProposals+1 {
		t.Fatalf("Learning rows before=%+v after=%+v", before, afterCreate)
	}
	afterCreate.LearningProposals = before.LearningProposals
	if afterCreate != before {
		t.Fatalf(
			"static Skill submission changed Runtime state\nbefore=%+v\nafter=%+v",
			before,
			afterCreate,
		)
	}
	if got := learningSkillContentRecordCount(t, fixture.store); got != beforeContent {
		t.Fatalf("content_records count=%d want unchanged %d", got, beforeContent)
	}

	// Mutating both the admission buffers and the returned record must not
	// alias either the exact-retry comparison or persisted bytes.
	input.DraftCanonical[0] = 'x'
	input.SourceEvidence.OriginMaterial[0] = 'x'
	first.Record.ProposalCanonical[0] = 'x'
	first.Record.DraftCanonical[0] = 'x'

	retry, err := fixture.store.SubmitStaticSkillProposal(
		context.Background(),
		pristine,
	)
	if err != nil {
		t.Fatalf("exact static Skill retry: %v", err)
	}
	if retry.Created || retry.Record.ProposalID != first.Record.ProposalID ||
		!retry.Record.CreatedAt.Equal(first.Record.CreatedAt) ||
		retry.Record.ProposalCanonical[0] != '{' ||
		retry.Record.DraftCanonical[0] != '{' {
		t.Fatalf("retry=%+v first=%+v", retry, first)
	}
	assertLearningProposalCount(t, fixture.store, 1)

	stored, err := fixture.store.GetStaticSkillProposal(
		context.Background(),
		fixture.manifest.TenantID,
		retry.Record.ProposalID,
	)
	if err != nil {
		t.Fatalf("GetStaticSkillProposal: %v", err)
	}
	stored.ProposalCanonical[0] = 'x'
	stored.DraftCanonical[0] = 'x'
	again, err := fixture.store.GetStaticSkillProposal(
		context.Background(),
		fixture.manifest.TenantID,
		retry.Record.ProposalID,
	)
	if err != nil {
		t.Fatalf("second GetStaticSkillProposal: %v", err)
	}
	if again.ProposalCanonical[0] != '{' || again.DraftCanonical[0] != '{' {
		t.Fatal("GetStaticSkillProposal returned aliased persisted bytes")
	}
	if _, err := fixture.store.GetStaticSkillProposal(
		context.Background(),
		"another-tenant",
		retry.Record.ProposalID,
	); !errors.Is(err, ErrLearningProposalNotFound) {
		t.Fatalf("cross-tenant static Skill Get error=%v", err)
	}
}

func TestStaticSkillAndKnowledgeProposalEndpointsRejectWrongKind(t *testing.T) {
	fixture := newLearningProposalStoreFixture(t)
	evidence := localLearningEvidence("endpoint-origin", "endpoint-revision")
	knowledge := fixture.input(
		t,
		"1.0.0",
		"Knowledge remains a Knowledge Draft.",
		evidence,
		fixture.manifest.TenantID,
		fixture.member.Workspace.ID,
	)
	skill := staticSkillProposalInput(
		t,
		fixture,
		"freeagent.test.skill.endpoint",
		"1.0.0",
		"A static Skill remains a static-context/v1 Draft.",
		evidence,
	)

	if _, err := fixture.store.SubmitStaticSkillProposal(
		context.Background(),
		knowledge,
	); !errors.Is(err, ErrInvalidLearningProposal) {
		t.Fatalf("Knowledge through Skill endpoint error=%v", err)
	}
	if _, err := fixture.store.SubmitKnowledgeProposal(
		context.Background(),
		skill,
	); !errors.Is(err, ErrInvalidLearningProposal) {
		t.Fatalf("Skill through Knowledge endpoint error=%v", err)
	}
	assertLearningProposalCount(t, fixture.store, 0)

	knowledgeResult, err := fixture.store.SubmitKnowledgeProposal(
		context.Background(),
		knowledge,
	)
	if err != nil {
		t.Fatalf("submit Knowledge through Knowledge endpoint: %v", err)
	}
	skillResult, err := fixture.store.SubmitStaticSkillProposal(
		context.Background(),
		skill,
	)
	if err != nil {
		t.Fatalf("submit Skill through Skill endpoint: %v", err)
	}
	if _, err := fixture.store.GetKnowledgeProposal(
		context.Background(),
		fixture.manifest.TenantID,
		skillResult.Record.ProposalID,
	); !errors.Is(err, ErrLearningProposalNotFound) {
		t.Fatalf("Skill through Knowledge getter error=%v", err)
	}
	if _, err := fixture.store.GetStaticSkillProposal(
		context.Background(),
		fixture.manifest.TenantID,
		knowledgeResult.Record.ProposalID,
	); !errors.Is(err, ErrLearningProposalNotFound) {
		t.Fatalf("Knowledge through Skill getter error=%v", err)
	}
	assertLearningProposalCount(t, fixture.store, 2)
}

func TestSubmitStaticSkillProposalRejectsInvalidDraftContracts(t *testing.T) {
	fixture := newLearningProposalStoreFixture(t)
	base := staticSkillProposalInput(
		t,
		fixture,
		"freeagent.test.skill.invalid-draft",
		"1.0.0",
		"Only the exact static-context/v1 contract is accepted.",
		localLearningEvidence("invalid-origin", "invalid-revision"),
	)
	overLimit := append(
		[]byte(`{"schema_version":"static-context/v1","text":"`),
		bytes.Repeat([]byte("x"), moduleapi.MaxTextBytes+1)...,
	)
	overLimit = append(overLimit, '"', '}')

	tests := []struct {
		name  string
		draft []byte
	}{
		{
			name:  "non-canonical static context",
			draft: append([]byte(" "), base.DraftCanonical...),
		},
		{
			name: "wrong static context schema",
			draft: []byte(
				`{"schema_version":"static-context/v2","text":"wrong schema"}`,
			),
		},
		{
			name: "Knowledge Source presented as Skill",
			draft: learningKnowledgeDraft(
				t,
				base.Proposal.Target.ID,
				base.Proposal.Target.Version,
				"A Knowledge Source is not a static Skill wire.",
				fixture.manifest.TenantID,
				fixture.member.Workspace.ID,
			),
		},
		{
			name:  "complete canonical draft over one MiB",
			draft: overLimit,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := cloneLearningSubmitInput(base)
			input.DraftCanonical = bytes.Clone(test.draft)
			if _, err := fixture.store.SubmitStaticSkillProposal(
				context.Background(),
				input,
			); !errors.Is(err, ErrInvalidLearningProposal) {
				t.Fatalf("invalid static Skill Draft error=%v", err)
			}
			assertLearningProposalCount(t, fixture.store, 0)
		})
	}
}

func TestSubmitStaticSkillProposalRejectsIndependentDuplicateAxes(
	t *testing.T,
) {
	t.Run("source", func(t *testing.T) {
		fixture := newLearningProposalStoreFixture(t)
		evidence := localLearningEvidence("same-origin", "same-revision")
		first := staticSkillProposalInput(
			t, fixture, "freeagent.test.skill.source", "1.0.0",
			"First static Skill body.", evidence,
		)
		second := staticSkillProposalInput(
			t, fixture, "freeagent.test.skill.source", "2.0.0",
			"Changed static Skill body.", evidence,
		)
		if _, err := fixture.store.SubmitStaticSkillProposal(
			context.Background(), first,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.SubmitStaticSkillProposal(
			context.Background(), second,
		); !errors.Is(err, ErrLearningSourceDuplicate) {
			t.Fatalf("source duplicate error=%v", err)
		}
		assertLearningProposalCount(t, fixture.store, 1)
	})

	t.Run("content even when target version changes", func(t *testing.T) {
		fixture := newLearningProposalStoreFixture(t)
		first := staticSkillProposalInput(
			t, fixture, "freeagent.test.skill.content", "1.0.0",
			"One immutable semantic Skill body.",
			localLearningEvidence("content-origin-a", "content-revision-a"),
		)
		second := staticSkillProposalInput(
			t, fixture, "freeagent.test.skill.content", "2.0.0",
			"One immutable semantic Skill body.",
			localLearningEvidence("content-origin-b", "content-revision-b"),
		)
		if _, err := fixture.store.SubmitStaticSkillProposal(
			context.Background(), first,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.SubmitStaticSkillProposal(
			context.Background(), second,
		); !errors.Is(err, ErrLearningContentDuplicate) {
			t.Fatalf("content duplicate error=%v", err)
		}
		assertLearningProposalCount(t, fixture.store, 1)
	})

	t.Run("target", func(t *testing.T) {
		fixture := newLearningProposalStoreFixture(t)
		first := staticSkillProposalInput(
			t, fixture, "freeagent.test.skill.target", "1.0.0",
			"First body for the immutable target.",
			localLearningEvidence("target-origin-a", "target-revision-a"),
		)
		second := staticSkillProposalInput(
			t, fixture, "freeagent.test.skill.target", "1.0.0",
			"Conflicting body for the immutable target.",
			localLearningEvidence("target-origin-b", "target-revision-b"),
		)
		if _, err := fixture.store.SubmitStaticSkillProposal(
			context.Background(), first,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.SubmitStaticSkillProposal(
			context.Background(), second,
		); !errors.Is(err, ErrLearningTargetDuplicate) {
			t.Fatalf("target duplicate error=%v", err)
		}
		assertLearningProposalCount(t, fixture.store, 1)
	})
}

func TestSubmitStaticSkillProposalConcurrentExactRetry(t *testing.T) {
	fixture := newLearningProposalStoreFixture(t)
	input := staticSkillProposalInput(
		t,
		fixture,
		"freeagent.test.skill.concurrent",
		"1.0.0",
		"Concurrent callers submit one exact static Skill Draft.",
		localLearningEvidence("concurrent-origin", "concurrent-revision"),
	)
	results := runConcurrentStaticSkillSubmissions(
		fixture.store,
		[]SubmitStaticSkillProposalInput{
			input, input, input, input, input, input, input, input,
		},
	)
	created := 0
	proposalID := ""
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("concurrent static Skill exact retry: %v", result.err)
		}
		if result.result.Created {
			created++
		}
		if proposalID == "" {
			proposalID = result.result.Record.ProposalID
		} else if proposalID != result.result.Record.ProposalID {
			t.Fatal("static Skill exact retries returned different ProposalIDs")
		}
	}
	if created != 1 {
		t.Fatalf("created=%d want 1", created)
	}
	assertLearningProposalCount(t, fixture.store, 1)
}

func TestStaticSkillAndKnowledgeDeduplicationAreKindScoped(t *testing.T) {
	fixture := newLearningProposalStoreFixture(t)
	target := moduleapi.Ref{
		ID:      "freeagent.test.learning.cross-kind",
		Version: "1.0.0",
	}
	evidence := localLearningEvidence("cross-kind-origin", "cross-kind-revision")
	knowledge := fixture.input(
		t,
		target.Version,
		"The same source and target may enter independent kind-specific governance.",
		evidence,
		fixture.manifest.TenantID,
		fixture.member.Workspace.ID,
	)
	knowledge.Proposal.Target = target
	knowledge.DraftCanonical = learningKnowledgeDraft(
		t,
		target.ID,
		target.Version,
		"The same source and target may enter independent kind-specific governance.",
		fixture.manifest.TenantID,
		fixture.member.Workspace.ID,
	)
	skill := staticSkillProposalInput(
		t,
		fixture,
		target.ID,
		target.Version,
		"The same source and target may enter independent kind-specific governance.",
		evidence,
	)

	knowledgeResult, err := fixture.store.SubmitKnowledgeProposal(
		context.Background(), knowledge,
	)
	if err != nil {
		t.Fatalf("submit cross-kind Knowledge: %v", err)
	}
	skillResult, err := fixture.store.SubmitStaticSkillProposal(
		context.Background(), skill,
	)
	if err != nil {
		t.Fatalf("submit cross-kind Skill: %v", err)
	}
	if !knowledgeResult.Created || !skillResult.Created {
		t.Fatalf(
			"cross-kind submissions Knowledge=%+v Skill=%+v",
			knowledgeResult,
			skillResult,
		)
	}
	if knowledgeResult.Record.Proposal.SourceFingerprint !=
		skillResult.Record.Proposal.SourceFingerprint {
		t.Fatal("identical cross-kind source evidence produced different source identity")
	}
	if knowledgeResult.Record.Proposal.Target != skillResult.Record.Proposal.Target {
		t.Fatal("cross-kind target identities unexpectedly differ")
	}
	if knowledgeResult.Record.Proposal.ContentFingerprint ==
		skillResult.Record.Proposal.ContentFingerprint {
		t.Fatal("Knowledge and Skill content fingerprints are not domain separated")
	}
	assertLearningProposalCount(t, fixture.store, 2)
}

func TestSubmitStaticSkillProposalDoesNotPersistRawSourceEvidence(t *testing.T) {
	fixture := newLearningProposalStoreFixture(t)
	origin := []byte("RAW-SKILL-ORIGIN-MATERIAL-MUST-NOT-PERSIST-4fdc9d58")
	revision := []byte("RAW-SKILL-REVISION-MATERIAL-MUST-NOT-PERSIST-f4bf10c3")
	input := staticSkillProposalInput(
		t,
		fixture,
		"freeagent.test.skill.no-source-material",
		"1.0.0",
		"Persist only derived source identity digests.",
		learningcontract.SourceEvidenceV1{
			Mechanism:        learningcontract.SourceMechanismRemoteReferenceV1,
			OriginMaterial:   bytes.Clone(origin),
			RevisionMaterial: bytes.Clone(revision),
		},
	)
	result, err := fixture.store.SubmitStaticSkillProposal(
		context.Background(), input,
	)
	if err != nil {
		t.Fatalf("submit source-evidence Skill: %v", err)
	}
	var proposalCanonical, draftCanonical []byte
	if err := fixture.store.db.QueryRow(`
		SELECT proposal_canonical, draft_canonical
		FROM learning_proposals
		WHERE proposal_id=?
	`, result.Record.ProposalID).Scan(&proposalCanonical, &draftCanonical); err != nil {
		t.Fatalf("read persisted static Skill bytes: %v", err)
	}
	for label, persisted := range map[string][]byte{
		"Proposal": proposalCanonical,
		"Draft":    draftCanonical,
	} {
		if bytes.Contains(persisted, origin) || bytes.Contains(persisted, revision) {
			t.Fatalf("%s persisted raw SourceEvidence", label)
		}
	}

	path := fixture.store.Path()
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close source-evidence Store: %v", err)
	}
	for _, candidate := range []string{path, path + "-journal", path + "-wal"} {
		data, err := os.ReadFile(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read Store persistence %s: %v", candidate, err)
		}
		if bytes.Contains(data, origin) || bytes.Contains(data, revision) {
			t.Fatalf("Store persistence %s contains raw SourceEvidence", candidate)
		}
	}
}

func TestSubmitStaticSkillProposalReusesLineageAndClosedStoreGuards(
	t *testing.T,
) {
	t.Run("lineage", func(t *testing.T) {
		fixture := newLearningProposalStoreFixture(t)
		input := staticSkillProposalInput(
			t,
			fixture,
			"freeagent.test.skill.lineage",
			"1.0.0",
			"A static Skill must close to one successful Model result.",
			localLearningEvidence("lineage-origin", "lineage-revision"),
		)
		input.Proposal.ProposerResultRef = learningTestDigest("wrong-skill-result")
		before := snapshotLearningOutsideScope(t, fixture.store)
		if _, err := fixture.store.SubmitStaticSkillProposal(
			context.Background(), input,
		); !errors.Is(err, ErrLearningProposalLineage) {
			t.Fatalf("static Skill lineage error=%v", err)
		}
		if after := snapshotLearningOutsideScope(t, fixture.store); after != before {
			t.Fatalf("lineage rejection changed Store\nbefore=%+v\nafter=%+v", before, after)
		}
	})

	t.Run("closed Store", func(t *testing.T) {
		fixture := newLearningProposalStoreFixture(t)
		input := staticSkillProposalInput(
			t,
			fixture,
			"freeagent.test.skill.closed",
			"1.0.0",
			"A closed Store rejects a static Skill Proposal.",
			localLearningEvidence("closed-skill-origin", "closed-skill-revision"),
		)
		if err := fixture.store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if _, err := fixture.store.SubmitStaticSkillProposal(
			context.Background(), input,
		); !errors.Is(err, ErrStoreClosed) {
			t.Fatalf("closed Store static Skill error=%v", err)
		}
	})
}

func staticSkillProposalInput(
	t *testing.T,
	fixture learningProposalStoreFixture,
	targetID string,
	version string,
	text string,
	evidence learningcontract.SourceEvidenceV1,
) SubmitStaticSkillProposalInput {
	t.Helper()
	input := fixture.input(
		t,
		version,
		"discarded Knowledge fixture Draft",
		evidence,
		fixture.manifest.TenantID,
		fixture.member.Workspace.ID,
	)
	input.Proposal.Kind = learningcontract.ProposalKindSkillV1
	input.Proposal.Target = moduleapi.Ref{ID: targetID, Version: version}
	input.DraftCanonical = staticSkillDraft(t, text)
	return input
}

func staticSkillDraft(t *testing.T, text string) []byte {
	t.Helper()
	_, canonical, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text:          text,
		},
	)
	if err != nil {
		t.Fatalf("freeze static Skill Draft: %v", err)
	}
	return canonical
}

type concurrentStaticSkillSubmission struct {
	result SubmitStaticSkillProposalResult
	err    error
}

func runConcurrentStaticSkillSubmissions(
	store *Store,
	inputs []SubmitStaticSkillProposalInput,
) []concurrentStaticSkillSubmission {
	start := make(chan struct{})
	results := make([]concurrentStaticSkillSubmission, len(inputs))
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index].result, results[index].err =
				store.SubmitStaticSkillProposal(
					context.Background(),
					cloneLearningSubmitInput(inputs[index]),
				)
		}(index)
	}
	close(start)
	wait.Wait()
	return results
}

func learningSkillContentRecordCount(t *testing.T, store *Store) int64 {
	t.Helper()
	var count int64
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM content_records`).Scan(
		&count,
	); err != nil {
		t.Fatalf("count content_records: %v", err)
	}
	return count
}
