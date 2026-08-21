package learningcontract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestMaterializedVersionV1KnownVectorsAndArtifactRebuild(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		kind             ProposalKindV1
		revision         uint64
		wantVersionID    string
		wantArtifactID   string
		wantArtifactSize uint64
		wantManifest     string
		wantCanonical    string
	}{
		{
			name:             "Knowledge approved revision 2",
			kind:             ProposalKindKnowledgeV1,
			revision:         2,
			wantVersionID:    "e92841f9f71632c1a712a70a20df886dfecb38ff47b873d994c3af303790ff5f",
			wantArtifactID:   "9cfbbb31da780c49bf94a658fd8c53e06e3aed785587072e79e7d30cd5824636",
			wantArtifactSize: 788,
			wantManifest:     `{"api_version":"freeagent.module/v1","id":"freeagent.example.knowledge.learning","provides":[{"exact_version":"v1","name":"context.provide"}],"runtime":{"entrypoint":"content/source.json","mode":"TRUSTED_IN_PROCESS","protocol":"go-in-process/v1"},"version":"1.0.0"}`,
			wantCanonical:    `{"approve_verdict_digest":"76f0b52b3d13bfa15419450eee6efd23b1e6fa92a26435621a30e147c49f914c","artifact_digest":"9cfbbb31da780c49bf94a658fd8c53e06e3aed785587072e79e7d30cd5824636","artifact_size_bytes":788,"content_fingerprint":"5757d1a11b5325a16e55d07d47f228ee1776766a74ad06e8797d089d16b9e423","draft_digest":"7775f6abc2bc009210a99ccaf957fdae117b005809068a3ccc94b6e4d06c8f8d","kind":"KNOWLEDGE","proposal_id":"7b55661891e04e075fd504d601c836d4b395275b22c0dc24b43897ad5f7781e6","proposal_revision":2,"review_run_id":"review-run-1","reviewer_attempt_id":"review-attempt-1","schema_version":"learning-materialized-version/v1","source_fingerprint":"3cc7417bf3c7def498dd076fbe695e9bc29bb4e7294f005dca4bb45b359e33bd","target":{"id":"freeagent.example.knowledge.learning","version":"1.0.0"},"tenant_id":"tenant-a"}`,
		},
		{
			name:             "Skill reconciled approval revision 3",
			kind:             ProposalKindSkillV1,
			revision:         3,
			wantVersionID:    "000f3025408ef08b3e542728a1cbc4889e18c0af5c70ada52c4bc57fd13f217d",
			wantArtifactID:   "eb399010a8e46b7cf1280adf5a4c3070d982cc5e4b8bcefbc91afdc999ee78a1",
			wantArtifactSize: 336,
			wantManifest:     `{"api_version":"freeagent.module/v1","id":"freeagent.example.skill.learning","provides":[{"exact_version":"v1","name":"context.provide"}],"runtime":{"entrypoint":"content/context.json","mode":"DECLARATIVE","protocol":"static/v1"},"version":"1.0.0"}`,
			wantCanonical:    `{"approve_verdict_digest":"0c12eb26685ab6a5d511dc7f8493ac785537f102138885713d69add4fa205061","artifact_digest":"eb399010a8e46b7cf1280adf5a4c3070d982cc5e4b8bcefbc91afdc999ee78a1","artifact_size_bytes":336,"content_fingerprint":"29b88580e286cf693954a4b813c765aee523da1423a3bf673d4a6cebfbce36c0","draft_digest":"646c68e2af904c82bf8b638a7e55806efcec651fcae5bae8b3797f78d72d49ee","kind":"SKILL","proposal_id":"b348bff32e2b9384311a8e385b989c104ca3a05f9c1f921fb0ef73c27ebe26b5","proposal_revision":3,"review_run_id":"review-run-1","reviewer_attempt_id":"review-attempt-1","schema_version":"learning-materialized-version/v1","source_fingerprint":"3cc7417bf3c7def498dd076fbe695e9bc29bb4e7294f005dca4bb45b359e33bd","target":{"id":"freeagent.example.skill.learning","version":"1.0.0"},"tenant_id":"tenant-a"}`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newMaterializedVersionFixtureV1(
				t,
				test.kind,
				test.revision,
			)
			if fixture.versionID != test.wantVersionID ||
				fixture.version.ArtifactDigest != test.wantArtifactID ||
				fixture.version.ArtifactSizeBytes != test.wantArtifactSize ||
				string(fixture.versionCanonical) != test.wantCanonical {
				t.Fatalf(
					"known vector:\nversion_id=%s\nartifact_digest=%s\nartifact_size=%d\ncanonical=%s",
					fixture.versionID,
					fixture.version.ArtifactDigest,
					fixture.version.ArtifactSizeBytes,
					fixture.versionCanonical,
				)
			}

			artifact, err := MaterializedVersionArtifactV1(
				fixture.versionCanonical,
				fixture.versionID,
				fixture.proposalCanonical,
				fixture.draftCanonical,
			)
			if err != nil {
				t.Fatalf("MaterializedVersionArtifactV1: %v", err)
			}
			if string(artifact.ManifestCanonical) != test.wantManifest ||
				artifact.ArtifactDigest != test.wantArtifactID ||
				artifact.ArtifactSizeBytes != test.wantArtifactSize ||
				!bytes.Equal(artifact.PayloadCanonical, fixture.draftCanonical) {
				t.Fatalf("artifact = %#v", artifact)
			}
			wantPath := MaterializedKnowledgeEntrypointV1
			if test.kind == ProposalKindSkillV1 {
				wantPath = MaterializedSkillEntrypointV1
			}
			if artifact.EntrypointPath != wantPath {
				t.Fatalf("entrypoint=%q want=%q", artifact.EntrypointPath, wantPath)
			}

			restored, err := RestoreMaterializedVersionV1(
				fixture.versionCanonical,
				fixture.versionID,
				fixture.proposalCanonical,
				fixture.draftCanonical,
				fixture.verdictCanonical,
			)
			if err != nil || restored != fixture.version {
				t.Fatalf("RestoreMaterializedVersionV1=%+v err=%v", restored, err)
			}
		})
	}
}

func TestMaterializedVersionV1CarriesNoAuthorityOrOperatorSelection(t *testing.T) {
	t.Parallel()
	fixture := newMaterializedVersionFixtureV1(
		t,
		ProposalKindKnowledgeV1,
		2,
	)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(fixture.versionCanonical, &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"profile", "profile_id", "instance", "instance_id", "binding",
		"config", "authority", "trust", "secret", "catalog",
		"catalog_revision", "expected_pointer_revision", "failure_policy",
	} {
		if _, exists := fields[forbidden]; exists {
			t.Fatalf("materialized version contains forbidden field %q", forbidden)
		}
	}
	artifact, err := MaterializedVersionArtifactV1(
		fixture.versionCanonical,
		fixture.versionID,
		fixture.proposalCanonical,
		fixture.draftCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(
		artifact.ManifestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Requires) != 0 ||
		len(manifest.RequestedPermissions) != 0 ||
		manifest.ConfigSchema != nil || manifest.Lifecycle != nil ||
		manifest.Health != nil {
		t.Fatalf("manifest contains optional authority/lifecycle requests: %+v", manifest)
	}
}

func TestMaterializedVersionV1RejectsWrongReviewAndParentBindings(t *testing.T) {
	t.Parallel()
	fixture := newMaterializedVersionFixtureV1(
		t,
		ProposalKindKnowledgeV1,
		2,
	)
	base := MaterializedVersionV1{
		SchemaVersion:     MaterializedVersionSchemaVersionV1,
		ProposalID:        fixture.proposalID,
		ProposalRevision:  2,
		ReviewRunID:       "review-run-1",
		ReviewerAttemptID: "review-attempt-1",
	}
	mutations := []struct {
		name   string
		mutate func(*MaterializedVersionV1)
	}{
		{"schema", func(value *MaterializedVersionV1) { value.SchemaVersion = "learning-materialized-version/v2" }},
		{"revision 0", func(value *MaterializedVersionV1) { value.ProposalRevision = 0 }},
		{"revision 1", func(value *MaterializedVersionV1) { value.ProposalRevision = 1 }},
		{"revision 4", func(value *MaterializedVersionV1) { value.ProposalRevision = 4 }},
		{"review run", func(value *MaterializedVersionV1) { value.ReviewRunID = "" }},
		{"review attempt", func(value *MaterializedVersionV1) { value.ReviewerAttemptID = "\n" }},
		{"verdict digest", func(value *MaterializedVersionV1) { value.ApproveVerdictDigest = digestV1("0") }},
		{"tenant", func(value *MaterializedVersionV1) { value.TenantID = "tenant-b" }},
		{"kind", func(value *MaterializedVersionV1) { value.Kind = ProposalKindSkillV1 }},
		{"source", func(value *MaterializedVersionV1) { value.SourceFingerprint = digestV1("0") }},
		{"content", func(value *MaterializedVersionV1) { value.ContentFingerprint = digestV1("0") }},
		{"draft", func(value *MaterializedVersionV1) { value.DraftDigest = digestV1("0") }},
		{"target", func(value *MaterializedVersionV1) {
			value.Target = moduleapi.Ref{ID: "freeagent.other", Version: "1.0.0"}
		}},
		{"artifact digest", func(value *MaterializedVersionV1) { value.ArtifactDigest = digestV1("0") }},
		{"artifact size", func(value *MaterializedVersionV1) { value.ArtifactSizeBytes = 1 }},
	}
	for _, mutation := range mutations {
		mutation := mutation
		t.Run(mutation.name, func(t *testing.T) {
			input := base
			mutation.mutate(&input)
			if _, _, _, err := NewMaterializedVersionV1(
				input,
				fixture.proposalCanonical,
				fixture.draftCanonical,
				fixture.verdictCanonical,
			); err == nil {
				t.Fatal("NewMaterializedVersionV1 accepted mismatched input")
			}
		})
	}

	reject, rejectCanonical, _, err := NewReviewVerdictV1(ReviewVerdictV1{
		SchemaVersion:      ReviewVerdictSchemaVersionV1,
		ProposalID:         fixture.proposalID,
		SourceFingerprint:  fixture.proposal.SourceFingerprint,
		ContentFingerprint: fixture.proposal.ContentFingerprint,
		Decision:           ReviewDecisionRejectV1,
		IssueCodes:         []ReviewIssueCodeV1{ReviewIssueContentInvalidV1},
		BoundedReason:      "Candidate is invalid.",
	})
	if err != nil || reject.Decision != ReviewDecisionRejectV1 {
		t.Fatal(err)
	}
	if _, _, _, err := NewMaterializedVersionV1(
		base,
		fixture.proposalCanonical,
		fixture.draftCanonical,
		rejectCanonical,
	); err == nil {
		t.Fatal("NewMaterializedVersionV1 accepted REJECT")
	}
	if _, _, _, err := NewMaterializedVersionV1(
		base,
		fixture.proposalCanonical,
		fixture.draftCanonical,
		append([]byte(" "), fixture.verdictCanonical...),
	); err == nil {
		t.Fatal("NewMaterializedVersionV1 accepted non-canonical verdict")
	}

	changedDraft := knowledgeDraftV1(t, "1.0.0", "2", digestV1("b"))
	if _, _, _, err := NewMaterializedVersionV1(
		base,
		fixture.proposalCanonical,
		changedDraft,
		fixture.verdictCanonical,
	); err == nil {
		t.Fatal("NewMaterializedVersionV1 accepted changed Draft")
	}
}

func TestRestoreMaterializedVersionV1IsStrict(t *testing.T) {
	t.Parallel()
	fixture := newMaterializedVersionFixtureV1(
		t,
		ProposalKindSkillV1,
		3,
	)
	if _, err := RestoreMaterializedVersionV1(
		append([]byte(" "), fixture.versionCanonical...),
		fixture.versionID,
		fixture.proposalCanonical,
		fixture.draftCanonical,
		fixture.verdictCanonical,
	); err == nil {
		t.Fatal("RestoreMaterializedVersionV1 accepted surrounding whitespace")
	}
	unknown := canonicalObjectMutationV1(
		t,
		fixture.versionCanonical,
		func(fields map[string]any) { fields["authority"] = "self-granted" },
	)
	if _, err := RestoreMaterializedVersionV1(
		unknown,
		moduleapi.Digest(materializedVersionDigestDomainV1, unknown),
		fixture.proposalCanonical,
		fixture.draftCanonical,
		fixture.verdictCanonical,
	); err == nil {
		t.Fatal("RestoreMaterializedVersionV1 accepted unknown authority field")
	}
	if _, err := RestoreMaterializedVersionV1(
		fixture.versionCanonical,
		digestV1("0"),
		fixture.proposalCanonical,
		fixture.draftCanonical,
		fixture.verdictCanonical,
	); err == nil {
		t.Fatal("RestoreMaterializedVersionV1 accepted wrong VersionID")
	}
	if _, err := RestoreMaterializedVersionV1(
		fixture.versionCanonical,
		fixture.versionID,
		fixture.proposalCanonical,
		fixture.draftCanonical,
		bytes.Replace(
			fixture.verdictCanonical,
			[]byte("Approved candidate."),
			[]byte("Changed candidate."),
			1,
		),
	); err == nil {
		t.Fatal("RestoreMaterializedVersionV1 accepted changed verdict")
	}
}

func TestMaterializedVersionArtifactV1DefensiveCopiesAndTamperRejection(t *testing.T) {
	t.Parallel()
	fixture := newMaterializedVersionFixtureV1(
		t,
		ProposalKindKnowledgeV1,
		2,
	)
	first, err := MaterializedVersionArtifactV1(
		fixture.versionCanonical,
		fixture.versionID,
		fixture.proposalCanonical,
		fixture.draftCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantManifest := bytes.Clone(first.ManifestCanonical)
	wantPayload := bytes.Clone(first.PayloadCanonical)
	first.ManifestCanonical[0] ^= 0xff
	first.PayloadCanonical[0] ^= 0xff
	second, err := MaterializedVersionArtifactV1(
		fixture.versionCanonical,
		fixture.versionID,
		fixture.proposalCanonical,
		fixture.draftCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(second.ManifestCanonical, wantManifest) ||
		!bytes.Equal(second.PayloadCanonical, wantPayload) {
		t.Fatal("MaterializedVersionArtifactV1 returned aliased bytes")
	}

	tampered := canonicalObjectMutationV1(
		t,
		fixture.versionCanonical,
		func(fields map[string]any) { fields["artifact_digest"] = digestV1("0") },
	)
	if _, err := MaterializedVersionArtifactV1(
		tampered,
		moduleapi.Digest(materializedVersionDigestDomainV1, tampered),
		fixture.proposalCanonical,
		fixture.draftCanonical,
	); err == nil {
		t.Fatal("MaterializedVersionArtifactV1 accepted artifact digest tamper")
	}
	if _, err := MaterializedVersionArtifactV1(
		fixture.versionCanonical,
		fixture.versionID,
		fixture.proposalCanonical,
		append(bytes.Clone(fixture.draftCanonical), ' '),
	); err == nil {
		t.Fatal("MaterializedVersionArtifactV1 accepted Draft tamper")
	}
}

type materializedVersionFixtureV1 struct {
	proposal          ProposalV1
	proposalCanonical []byte
	proposalID        string
	draftCanonical    []byte
	verdictCanonical  []byte
	version           MaterializedVersionV1
	versionCanonical  []byte
	versionID         string
}

func newMaterializedVersionFixtureV1(
	t *testing.T,
	kind ProposalKindV1,
	revision uint64,
) materializedVersionFixtureV1 {
	t.Helper()
	input := proposalInputV1(kind, "1.0.0")
	var draft []byte
	switch kind {
	case ProposalKindKnowledgeV1:
		draft = knowledgeDraftV1(t, "1.0.0", "1", digestV1("a"))
	case ProposalKindSkillV1:
		input.Target.ID = "freeagent.example.skill.learning"
		_, canonical, err := corecontract.NewStaticContextV1(
			corecontract.StaticContextV1{
				SchemaVersion: corecontract.StaticContextSchemaVersionV1,
				Text:          "Skill: preserve exact governed behavior.",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		draft = canonical
	default:
		t.Fatalf("unsupported fixture kind %q", kind)
	}
	proposal, proposalCanonical, proposalID, err := NewProposalV1(
		input,
		draft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, verdictCanonical, verdictDigest, err := NewReviewVerdictV1(
		ReviewVerdictV1{
			SchemaVersion:      ReviewVerdictSchemaVersionV1,
			ProposalID:         proposalID,
			SourceFingerprint:  proposal.SourceFingerprint,
			ContentFingerprint: proposal.ContentFingerprint,
			Decision:           ReviewDecisionApproveV1,
			IssueCodes:         []ReviewIssueCodeV1{},
			BoundedReason:      "Approved candidate.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	version, versionCanonical, versionID, err := NewMaterializedVersionV1(
		MaterializedVersionV1{
			SchemaVersion:        MaterializedVersionSchemaVersionV1,
			ProposalID:           proposalID,
			ProposalRevision:     revision,
			ReviewRunID:          "review-run-1",
			ReviewerAttemptID:    "review-attempt-1",
			ApproveVerdictDigest: verdictDigest,
		},
		proposalCanonical,
		draft,
		verdictCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return materializedVersionFixtureV1{
		proposal:          proposal,
		proposalCanonical: bytes.Clone(proposalCanonical),
		proposalID:        proposalID,
		draftCanonical:    bytes.Clone(draft),
		verdictCanonical:  bytes.Clone(verdictCanonical),
		version:           version,
		versionCanonical:  bytes.Clone(versionCanonical),
		versionID:         versionID,
	}
}

func canonicalObjectMutationV1(
	t *testing.T,
	canonical []byte,
	mutate func(map[string]any),
) []byte {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		t.Fatal(err)
	}
	mutate(fields)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	result, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMaterializedVersionConstantsAreCanonicalAndBounded(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		MaterializedVersionSchemaVersionV1,
		MaterializedKnowledgeEntrypointV1,
		MaterializedSkillEntrypointV1,
	} {
		if value != moduleapi.CanonicalText(value) ||
			strings.TrimSpace(value) != value {
			t.Fatalf("non-canonical materialized version constant %q", value)
		}
	}
}
