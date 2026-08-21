package learningcontract

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestKnowledgeProposalV1CanonicalRestoreAndVersionIndependentContentFingerprint(
	t *testing.T,
) {
	firstDraft := knowledgeDraftV1(t, "1.0.0", "document-v1", digestV1("a"))
	input := proposalInputV1(ProposalKindKnowledgeV1, "1.0.0")
	first, canonical, proposalID, err := NewProposalV1(
		input,
		firstDraft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatalf("NewProposalV1: %v", err)
	}
	if !moduleapi.ValidSHA256(proposalID) ||
		!moduleapi.ValidSHA256(first.SourceFingerprint) ||
		!moduleapi.ValidSHA256(first.DraftDigest) ||
		!moduleapi.ValidSHA256(first.ContentFingerprint) {
		t.Fatalf("derived proposal identities are invalid: %+v %s", first, proposalID)
	}
	restored, err := RestoreProposalV1(canonical, firstDraft, proposalID)
	if err != nil {
		t.Fatalf("RestoreProposalV1: %v", err)
	}
	if restored != first {
		t.Fatalf("restored proposal differs: %+v %+v", restored, first)
	}

	secondDraft := knowledgeDraftV1(t, "2.0.0", "document-v2", digestV1("b"))
	secondInput := proposalInputV1(ProposalKindKnowledgeV1, "2.0.0")
	second, _, secondID, err := NewProposalV1(
		secondInput,
		secondDraft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatalf("NewProposalV1(version bump): %v", err)
	}
	if first.DraftDigest == second.DraftDigest || proposalID == secondID {
		t.Fatal("exact draft/version bump did not change exact identities")
	}
	if first.ContentFingerprint != second.ContentFingerprint {
		t.Fatalf(
			"semantic no-change fingerprint changed across version-only bump: %s %s",
			first.ContentFingerprint,
			second.ContentFingerprint,
		)
	}
}

func TestKnowledgeContentFingerprintIgnoresRenameOnlyMetadata(t *testing.T) {
	firstDraft := knowledgeDraftWithIdentityV1(
		t,
		"freeagent.example.knowledge.learning",
		"1.0.0",
		"document-a",
		"document-v1",
		digestV1("a"),
		"chunk-a",
		"The same governed semantic Knowledge content.",
		"workspace-a",
	)
	firstInput := proposalInputV1(ProposalKindKnowledgeV1, "1.0.0")
	first, _, _, err := NewProposalV1(
		firstInput,
		firstDraft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatal(err)
	}

	secondDraft := knowledgeDraftWithIdentityV1(
		t,
		"freeagent.example.knowledge.renamed",
		"9.0.0",
		"document-renamed",
		"document-v9",
		digestV1("9"),
		"chunk-renamed",
		"The same governed semantic Knowledge content.",
		"workspace-a",
	)
	secondInput := proposalInputV1(ProposalKindKnowledgeV1, "9.0.0")
	secondInput.Target.ID = "freeagent.example.knowledge.renamed"
	second, _, _, err := NewProposalV1(
		secondInput,
		secondDraft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentFingerprint != second.ContentFingerprint {
		t.Fatal("rename-only Knowledge metadata bypassed semantic deduplication")
	}

	changedTextDraft := knowledgeDraftWithIdentityV1(
		t,
		"freeagent.example.knowledge.learning",
		"2.0.0",
		"document-a",
		"document-v2",
		digestV1("2"),
		"chunk-a",
		"The governed semantic Knowledge content actually changed.",
		"workspace-a",
	)
	changedTextInput := proposalInputV1(ProposalKindKnowledgeV1, "2.0.0")
	changedText, _, _, err := NewProposalV1(
		changedTextInput,
		changedTextDraft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentFingerprint == changedText.ContentFingerprint {
		t.Fatal("changed Knowledge text did not change the semantic fingerprint")
	}

	changedScopeDraft := knowledgeDraftWithIdentityV1(
		t,
		"freeagent.example.knowledge.learning",
		"3.0.0",
		"document-a",
		"document-v3",
		digestV1("3"),
		"chunk-a",
		"The same governed semantic Knowledge content.",
		"*",
	)
	changedScopeInput := proposalInputV1(ProposalKindKnowledgeV1, "3.0.0")
	changedScope, _, _, err := NewProposalV1(
		changedScopeInput,
		changedScopeDraft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentFingerprint == changedScope.ContentFingerprint {
		t.Fatal("changed requested visibility did not change the semantic fingerprint")
	}
}

func TestSourceEvidenceV1DerivesStableNonPersistedIdentity(t *testing.T) {
	draft := knowledgeDraftV1(t, "1.0.0", "document-v1", digestV1("a"))
	base := proposalInputV1(ProposalKindKnowledgeV1, "1.0.0")
	agent, _, _, err := NewProposalV1(base, draft, agentSourceEvidenceV1())
	if err != nil {
		t.Fatal(err)
	}
	if agent.Source.OriginDigest != base.ProposerResultRef ||
		agent.Source.RevisionDigest != base.ProposerResultRef {
		t.Fatal("agent-generated source was not fixed to the exact proposer result")
	}

	origin := []byte("normalized-local-origin-descriptor")
	revision := []byte("exact-local-revision-material")
	localEvidence := SourceEvidenceV1{
		Mechanism:        SourceMechanismLocalImportV1,
		OriginMaterial:   origin,
		RevisionMaterial: revision,
	}
	first, canonical, proposalID, err := NewProposalV1(base, draft, localEvidence)
	if err != nil {
		t.Fatal(err)
	}
	second, _, _, err := NewProposalV1(base, draft, localEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if first.Source != second.Source ||
		first.SourceFingerprint != second.SourceFingerprint {
		t.Fatal("identical imported evidence did not derive a stable identity")
	}
	if bytes.Contains(canonical, origin) || bytes.Contains(canonical, revision) {
		t.Fatal("raw source evidence leaked into the persisted proposal")
	}
	if _, err := RestoreProposalV1(canonical, draft, proposalID); err != nil {
		t.Fatalf("RestoreProposalV1(imported source): %v", err)
	}

	changedEvidence := localEvidence
	changedEvidence.RevisionMaterial = []byte("changed-local-revision")
	changed, _, _, err := NewProposalV1(base, draft, changedEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceFingerprint == changed.SourceFingerprint {
		t.Fatal("changed source revision reused the source fingerprint")
	}

	remoteEvidence := localEvidence
	remoteEvidence.Mechanism = SourceMechanismRemoteReferenceV1
	remote, _, _, err := NewProposalV1(base, draft, remoteEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceFingerprint == remote.SourceFingerprint {
		t.Fatal("different source mechanisms shared a fingerprint domain")
	}

	claimed := base
	claimed.Source = SourceIdentityV1{
		Mechanism:      SourceMechanismLocalImportV1,
		OriginDigest:   digestV1("1"),
		RevisionDigest: digestV1("2"),
	}
	if _, _, _, err := NewProposalV1(claimed, draft, localEvidence); err == nil {
		t.Fatal("caller-declared source identity bypassed evidence derivation")
	}
}

func TestStaticSkillProposalV1UsesExistingContextContract(t *testing.T) {
	_, draft, err := corecontract.NewStaticContextV1(corecontract.StaticContextV1{
		SchemaVersion: corecontract.StaticContextSchemaVersionV1,
		Text:          "Skill: provide exact implementation evidence.",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := proposalInputV1(ProposalKindSkillV1, "1.0.0")
	input.Target.ID = "freeagent.example.skill.evidence"
	proposal, canonical, proposalID, err := NewProposalV1(
		input,
		draft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatalf("NewProposalV1(Skill): %v", err)
	}
	if proposal.DraftDigest == proposal.ContentFingerprint {
		t.Fatal("exact Skill draft digest and semantic content fingerprint share a domain")
	}
	if _, err := RestoreProposalV1(canonical, draft, proposalID); err != nil {
		t.Fatalf("RestoreProposalV1(Skill): %v", err)
	}
}

func TestProposalV1RejectsInvalidParentsAndClaimedFingerprints(t *testing.T) {
	draft := knowledgeDraftV1(t, "1.0.0", "document-v1", digestV1("a"))
	base := proposalInputV1(ProposalKindKnowledgeV1, "1.0.0")
	valid, canonical, proposalID, err := NewProposalV1(
		base,
		draft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		mutate func(*ProposalV1)
		draft  []byte
	}{
		{"schema", func(value *ProposalV1) { value.SchemaVersion = "learning-proposal/v2" }, draft},
		{"kind", func(value *ProposalV1) { value.Kind = "TOOL" }, draft},
		{"tenant", func(value *ProposalV1) { value.TenantID = " tenant" }, draft},
		{"run control", func(value *ProposalV1) { value.ProposerRunID = "run\nid" }, draft},
		{"workspace ref", func(value *ProposalV1) { value.Workspace.Digest = "bad" }, draft},
		{"agent ref", func(value *ProposalV1) { value.ProposerAgent.Version = "" }, draft},
		{"profile ref", func(value *ProposalV1) { value.ProposerProfile.ID = "profile\tid" }, draft},
		{"member ref", func(value *ProposalV1) { value.ProposerMember.Digest = "bad" }, draft},
		{"manifest", func(value *ProposalV1) { value.ProposerManifestDigest = "bad" }, draft},
		{"result", func(value *ProposalV1) { value.ProposerResultRef = "bad" }, draft},
		{"source mechanism", func(value *ProposalV1) { value.Source.Mechanism = "WEB" }, draft},
		{"generated lineage", func(value *ProposalV1) { value.Source.OriginDigest = digestV1("c") }, draft},
		{"target", func(value *ProposalV1) { value.Target.ID = "wrong.knowledge" }, draft},
		{"source fingerprint", func(value *ProposalV1) { value.SourceFingerprint = digestV1("d") }, draft},
		{"draft digest", func(value *ProposalV1) { value.DraftDigest = digestV1("e") }, draft},
		{"content fingerprint", func(value *ProposalV1) { value.ContentFingerprint = digestV1("f") }, draft},
		{"noncanonical draft", func(value *ProposalV1) {}, append([]byte(" "), draft...)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if _, _, _, err := NewProposalV1(
				value,
				test.draft,
				agentSourceEvidenceV1(),
			); err == nil {
				t.Fatal("invalid proposal was accepted")
			}
		})
	}

	unknown := bytes.Replace(
		canonical,
		[]byte(`"schema_version":"learning-proposal/v1"`),
		[]byte(`"extra":true,"schema_version":"learning-proposal/v1"`),
		1,
	)
	if _, err := RestoreProposalV1(unknown, draft, proposalID); err == nil {
		t.Fatal("proposal with unknown field was restored")
	}
	if _, err := RestoreProposalV1(append([]byte(" "), canonical...), draft, proposalID); err == nil {
		t.Fatal("noncanonical proposal was restored")
	}
	if _, err := RestoreProposalV1(canonical, draft, digestV1("9")); err == nil {
		t.Fatal("proposal restored under a different ID")
	}
	if valid.SourceFingerprint == "" {
		t.Fatal("valid proposal lost its source fingerprint")
	}
}

func TestKnowledgeProposalV1RejectsCrossTenantVisibility(t *testing.T) {
	draft := knowledgeDraftForTenantV1(t, "other-tenant")
	input := proposalInputV1(ProposalKindKnowledgeV1, "1.0.0")
	if _, _, _, err := NewProposalV1(
		input,
		draft,
		agentSourceEvidenceV1(),
	); err == nil {
		t.Fatal("cross-tenant Knowledge draft was accepted")
	}
}

func TestReviewRequestV1CanonicalParseRestoreAndDefensiveCopies(t *testing.T) {
	draft := knowledgeDraftV1(t, "1.0.0", "document-v1", digestV1("a"))
	input := reviewRequestInputV1(t, ProposalKindKnowledgeV1, draft, "1.0.0")
	proposalBefore := bytes.Clone(input.ProposalCanonical)
	draftBefore := bytes.Clone(input.DraftCanonical)

	request, canonical, digest, err := NewReviewRequestV1(input)
	if err != nil {
		t.Fatalf("NewReviewRequestV1: %v", err)
	}
	if !moduleapi.ValidSHA256(digest) ||
		request.SchemaVersion != ReviewRequestSchemaVersionV1 ||
		request.OutputSchemaVersion != ReviewVerdictSchemaVersionV1 ||
		request.ReviewPolicy != ReviewPolicyProposalGateV1 ||
		request.Instructions != ReviewInstructionsV1 ||
		request.MaxOutputTokens != MaxReviewOutputTokensV1 ||
		!bytes.Equal(request.ProposalCanonical, proposalBefore) ||
		!bytes.Equal(request.DraftCanonical, draftBefore) {
		t.Fatalf("frozen review request is incomplete: %+v", request)
	}
	restored, err := RestoreReviewRequestV1(canonical, digest)
	if err != nil {
		t.Fatalf("RestoreReviewRequestV1: %v", err)
	}
	if !reflect.DeepEqual(restored, request) {
		t.Fatal("restored review request differs")
	}

	// Parse accepts outer key reordering while the embedded parents remain
	// exact canonical JSON.
	reordered, err := json.Marshal(struct {
		Draft              json.RawMessage `json:"draft"`
		Proposal           json.RawMessage `json:"proposal"`
		ReviewPolicy       ReviewPolicyV1  `json:"review_policy"`
		Instructions       string          `json:"instructions"`
		MaxOutputTokens    uint32          `json:"max_output_tokens"`
		OutputSchema       string          `json:"output_schema_version"`
		DraftDigest        string          `json:"draft_digest"`
		ContentFingerprint string          `json:"content_fingerprint"`
		SourceFingerprint  string          `json:"source_fingerprint"`
		ProposalID         string          `json:"proposal_id"`
		SchemaVersion      string          `json:"schema_version"`
	}{
		Draft:              request.DraftCanonical,
		Proposal:           request.ProposalCanonical,
		ReviewPolicy:       request.ReviewPolicy,
		Instructions:       request.Instructions,
		MaxOutputTokens:    request.MaxOutputTokens,
		OutputSchema:       request.OutputSchemaVersion,
		DraftDigest:        request.DraftDigest,
		ContentFingerprint: request.ContentFingerprint,
		SourceFingerprint:  request.SourceFingerprint,
		ProposalID:         request.ProposalID,
		SchemaVersion:      request.SchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, rebuilt, rebuiltDigest, err := ParseReviewRequestV1(reordered)
	if err != nil {
		t.Fatalf("ParseReviewRequestV1: %v", err)
	}
	if !reflect.DeepEqual(parsed, request) ||
		!bytes.Equal(rebuilt, canonical) || rebuiltDigest != digest {
		t.Fatal("parsed review request did not rebuild the canonical wire")
	}

	// Every caller-owned or returned buffer is detached from all other values.
	input.ProposalCanonical[0] ^= 1
	input.DraftCanonical[0] ^= 1
	request.ProposalCanonical[0] ^= 1
	request.DraftCanonical[0] ^= 1
	restored.ProposalCanonical[0] ^= 1
	restored.DraftCanonical[0] ^= 1
	again, err := RestoreReviewRequestV1(canonical, digest)
	if err != nil || !bytes.Equal(again.ProposalCanonical, proposalBefore) ||
		!bytes.Equal(again.DraftCanonical, draftBefore) {
		t.Fatalf("review request buffers alias: %+v error=%v", again, err)
	}
}

func TestReviewRequestV1EnforcesExact64KiBDraftWithoutTruncation(t *testing.T) {
	exactDraft := staticSkillDraftWithExactCanonicalSizeV1(
		t,
		MaxReviewDraftBytesV1,
	)
	exactInput := reviewRequestInputV1(
		t,
		ProposalKindSkillV1,
		exactDraft,
		"1.0.0",
	)
	exact, _, _, err := NewReviewRequestV1(exactInput)
	if err != nil {
		t.Fatalf("64 KiB review Draft: %v", err)
	}
	if len(exact.DraftCanonical) != MaxReviewDraftBytesV1 ||
		!bytes.Equal(exact.DraftCanonical, exactDraft) {
		t.Fatal("64 KiB review Draft was truncated or rewritten")
	}

	tooLargeDraft := staticSkillDraftWithExactCanonicalSizeV1(
		t,
		MaxReviewDraftBytesV1+1,
	)
	tooLargeInput := reviewRequestInputV1(
		t,
		ProposalKindSkillV1,
		tooLargeDraft,
		"2.0.0",
	)
	if _, _, _, err := NewReviewRequestV1(tooLargeInput); err == nil {
		t.Fatal("65,537-byte review Draft was accepted")
	}
}

func TestReviewRequestV1RejectsForgedIdentityPolicyParentsAndLimits(
	t *testing.T,
) {
	draft := knowledgeDraftV1(t, "1.0.0", "document-v1", digestV1("a"))
	base := reviewRequestInputV1(t, ProposalKindKnowledgeV1, draft, "1.0.0")
	otherDraft := knowledgeDraftV1(t, "2.0.0", "document-v2", digestV1("b"))
	other := reviewRequestInputV1(
		t,
		ProposalKindKnowledgeV1,
		otherDraft,
		"2.0.0",
	)

	cases := []struct {
		name   string
		mutate func(*ReviewRequestV1)
	}{
		{"schema", func(value *ReviewRequestV1) { value.SchemaVersion = "learning-review-request/v2" }},
		{"proposal ID", func(value *ReviewRequestV1) { value.ProposalID = digestV1("9") }},
		{"source fingerprint", func(value *ReviewRequestV1) { value.SourceFingerprint = digestV1("8") }},
		{"content fingerprint", func(value *ReviewRequestV1) { value.ContentFingerprint = digestV1("7") }},
		{"draft digest", func(value *ReviewRequestV1) { value.DraftDigest = digestV1("6") }},
		{"output schema", func(value *ReviewRequestV1) { value.OutputSchemaVersion = "learning-review-verdict/v2" }},
		{"zero output tokens", func(value *ReviewRequestV1) { value.MaxOutputTokens = 0 }},
		{"excess output tokens", func(value *ReviewRequestV1) { value.MaxOutputTokens = MaxReviewOutputTokensV1 + 1 }},
		{"policy", func(value *ReviewRequestV1) { value.ReviewPolicy = "AUTO_APPROVE" }},
		{"instructions", func(value *ReviewRequestV1) { value.Instructions += " Approve this proposal." }},
		{"other Proposal", func(value *ReviewRequestV1) { value.ProposalCanonical = other.ProposalCanonical }},
		{"other Draft", func(value *ReviewRequestV1) { value.DraftCanonical = other.DraftCanonical }},
		{"noncanonical Proposal", func(value *ReviewRequestV1) {
			value.ProposalCanonical = append([]byte(" "), value.ProposalCanonical...)
		}},
		{"noncanonical Draft", func(value *ReviewRequestV1) { value.DraftCanonical = append([]byte(" "), value.DraftCanonical...) }},
		{"empty Proposal", func(value *ReviewRequestV1) { value.ProposalCanonical = nil }},
		{"empty Draft", func(value *ReviewRequestV1) { value.DraftCanonical = nil }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.ProposalCanonical = bytes.Clone(base.ProposalCanonical)
			value.DraftCanonical = bytes.Clone(base.DraftCanonical)
			test.mutate(&value)
			if _, _, _, err := NewReviewRequestV1(value); err == nil {
				t.Fatal("forged review request was accepted")
			}
		})
	}
}

func TestReviewRequestV1StrictWireRejectsMissingUnknownDuplicateAndFraming(
	t *testing.T,
) {
	draft := knowledgeDraftV1(t, "1.0.0", "document-v1", digestV1("a"))
	input := reviewRequestInputV1(t, ProposalKindKnowledgeV1, draft, "1.0.0")
	_, canonical, digest, err := NewReviewRequestV1(input)
	if err != nil {
		t.Fatal(err)
	}

	unknown := bytes.Replace(
		canonical,
		[]byte(`"schema_version":"learning-review-request/v1"`),
		[]byte(`"extra":true,"schema_version":"learning-review-request/v1"`),
		1,
	)
	if _, _, _, err := ParseReviewRequestV1(unknown); err == nil {
		t.Fatal("review request with unknown field was parsed")
	}
	missing := bytes.Replace(
		canonical,
		[]byte(`"review_policy":"PROPOSAL_GATE",`),
		nil,
		1,
	)
	if _, _, _, err := ParseReviewRequestV1(missing); err == nil {
		t.Fatal("review request with missing field was parsed")
	}
	duplicate := bytes.Replace(
		canonical,
		[]byte(`"schema_version":"learning-review-request/v1"`),
		[]byte(`"schema_version":"learning-review-request/v1","schema_version":"learning-review-request/v1"`),
		1,
	)
	if _, _, _, err := ParseReviewRequestV1(duplicate); err == nil {
		t.Fatal("review request with duplicate field was parsed")
	}
	for _, invalid := range [][]byte{
		append([]byte(" "), canonical...),
		append(bytes.Clone(canonical), []byte(` {}`)...),
		[]byte("```json\n" + string(canonical) + "\n```"),
	} {
		if _, _, _, err := ParseReviewRequestV1(invalid); err == nil {
			t.Fatal("invalid review request framing was parsed")
		}
	}
	if _, err := RestoreReviewRequestV1(canonical, digestV1("f")); err == nil {
		t.Fatal("review request restored under another digest")
	}
	if _, err := RestoreReviewRequestV1(append([]byte(" "), canonical...), digest); err == nil {
		t.Fatal("noncanonical review request was restored")
	}
}

func TestReviewVerdictV1StrictParseAndProposalBinding(t *testing.T) {
	draft := knowledgeDraftV1(t, "1.0.0", "document-v1", digestV1("a"))
	proposal, _, proposalID, err := NewProposalV1(
		proposalInputV1(ProposalKindKnowledgeV1, "1.0.0"),
		draft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	input := ReviewVerdictV1{
		SchemaVersion:      ReviewVerdictSchemaVersionV1,
		ProposalID:         proposalID,
		SourceFingerprint:  proposal.SourceFingerprint,
		ContentFingerprint: proposal.ContentFingerprint,
		Decision:           ReviewDecisionApproveV1,
		IssueCodes:         []ReviewIssueCodeV1{},
		BoundedReason:      "The exact candidate is supported by its frozen evidence.",
	}
	verdict, canonical, digest, err := NewReviewVerdictV1(input)
	if err != nil {
		t.Fatalf("NewReviewVerdictV1: %v", err)
	}
	if err := verdict.ValidateForProposalV1(proposal, proposalID); err != nil {
		t.Fatalf("ValidateForProposalV1: %v", err)
	}
	reordered := []byte(
		`{"source_fingerprint":"` + proposal.SourceFingerprint +
			`","schema_version":"learning-review-verdict/v1","proposal_id":"` + proposalID +
			`","issue_codes":[],"decision":"APPROVE","content_fingerprint":"` + proposal.ContentFingerprint +
			`","bounded_reason":"The exact candidate is supported by its frozen evidence."}`,
	)
	parsed, rebuilt, rebuiltDigest, err := ParseReviewVerdictV1(reordered)
	if err != nil {
		t.Fatalf("ParseReviewVerdictV1: %v", err)
	}
	if !reflect.DeepEqual(parsed, verdict) ||
		!bytes.Equal(rebuilt, canonical) ||
		rebuiltDigest != digest {
		t.Fatal("parsed review did not rebuild the canonical verdict")
	}
	if _, err := RestoreReviewVerdictV1(canonical, digest); err != nil {
		t.Fatalf("RestoreReviewVerdictV1: %v", err)
	}
}

func TestReviewVerdictV1NormalizesEmptyIssuesAndRejectsForgedTypedValue(
	t *testing.T,
) {
	draft := knowledgeDraftV1(t, "1.0.0", "document-v1", digestV1("a"))
	proposal, _, proposalID, err := NewProposalV1(
		proposalInputV1(ProposalKindKnowledgeV1, "1.0.0"),
		draft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	base := ReviewVerdictV1{
		SchemaVersion:      ReviewVerdictSchemaVersionV1,
		ProposalID:         proposalID,
		SourceFingerprint:  proposal.SourceFingerprint,
		ContentFingerprint: proposal.ContentFingerprint,
		Decision:           ReviewDecisionApproveV1,
		BoundedReason:      "The exact frozen candidate is approved.",
	}
	fromNil, nilCanonical, nilDigest, err := NewReviewVerdictV1(base)
	if err != nil {
		t.Fatal(err)
	}
	withEmpty := base
	withEmpty.IssueCodes = []ReviewIssueCodeV1{}
	fromEmpty, emptyCanonical, emptyDigest, err := NewReviewVerdictV1(withEmpty)
	if err != nil {
		t.Fatal(err)
	}
	if fromNil.IssueCodes == nil || fromEmpty.IssueCodes == nil ||
		!bytes.Equal(nilCanonical, emptyCanonical) || nilDigest != emptyDigest {
		t.Fatal("nil and empty APPROVE issue lists did not normalize to one [] wire")
	}

	forged := base
	forged.Decision = ""
	forged.BoundedReason = ""
	if err := forged.ValidateForProposalV1(proposal, proposalID); err == nil {
		t.Fatal("unvalidated forged typed verdict bound to a Proposal")
	}

	valid, _, _, err := NewReviewVerdictV1(base)
	if err != nil {
		t.Fatal(err)
	}
	mutatedProposal := proposal
	mutatedProposal.ProposerAgent.Version = "2"
	if err := valid.ValidateForProposalV1(mutatedProposal, proposalID); err == nil {
		t.Fatal("verdict bound to a Proposal mutated outside its fingerprints")
	}
}

func TestLearningWiresEnforceFixedJSONLimitsAndStrictFraming(t *testing.T) {
	oversized := bytes.Repeat([]byte(" "), MaxReviewVerdictWireBytesV1+1)
	if _, _, _, err := ParseReviewVerdictV1(oversized); err == nil {
		t.Fatal("oversized review wire was parsed")
	}
	deep := []byte(strings.Repeat("[", maximumCanonicalDepthV1+1) +
		"0" + strings.Repeat("]", maximumCanonicalDepthV1+1))
	if _, _, _, err := ParseReviewVerdictV1(deep); err == nil {
		t.Fatal("over-deep review JSON was parsed")
	}
	duplicate := []byte(`{"schema_version":"learning-review-verdict/v1","schema_version":"learning-review-verdict/v1"}`)
	if _, _, _, err := ParseReviewVerdictV1(duplicate); err == nil {
		t.Fatal("review JSON with duplicate keys was parsed")
	}
	framed := []byte(" ```json\n{}\n``` ")
	if _, _, _, err := ParseReviewVerdictV1(framed); err == nil {
		t.Fatal("code-fenced review JSON was parsed")
	}

	draft := knowledgeDraftV1(t, "1.0.0", "document-v1", digestV1("a"))
	if _, err := RestoreProposalV1(
		bytes.Repeat([]byte("x"), MaxProposalWireBytesV1+1),
		draft,
		digestV1("1"),
	); err == nil {
		t.Fatal("oversized proposal wire was restored")
	}
}

func TestReviewVerdictV1RejectsInvalidDecisionsIssuesAndWire(t *testing.T) {
	base := ReviewVerdictV1{
		SchemaVersion:      ReviewVerdictSchemaVersionV1,
		ProposalID:         digestV1("1"),
		SourceFingerprint:  digestV1("2"),
		ContentFingerprint: digestV1("3"),
		Decision:           ReviewDecisionRejectV1,
		IssueCodes: []ReviewIssueCodeV1{
			ReviewIssueMissingEvidenceV1,
			ReviewIssueUnsupportedClaimV1,
		},
		BoundedReason: "The proposal lacks the required evidence.",
	}
	if _, _, _, err := NewReviewVerdictV1(base); err != nil {
		t.Fatalf("valid REJECT: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*ReviewVerdictV1)
	}{
		{"unknown decision", func(value *ReviewVerdictV1) { value.Decision = "REPAIR" }},
		{"reject without issues", func(value *ReviewVerdictV1) { value.IssueCodes = nil }},
		{"approve with issues", func(value *ReviewVerdictV1) { value.Decision = ReviewDecisionApproveV1 }},
		{"unsorted issues", func(value *ReviewVerdictV1) {
			value.IssueCodes[0], value.IssueCodes[1] = value.IssueCodes[1], value.IssueCodes[0]
		}},
		{"duplicate issues", func(value *ReviewVerdictV1) { value.IssueCodes[1] = value.IssueCodes[0] }},
		{"unknown issue", func(value *ReviewVerdictV1) { value.IssueCodes = []ReviewIssueCodeV1{"OTHER"} }},
		{"blank reason", func(value *ReviewVerdictV1) { value.BoundedReason = "" }},
		{"untrimmed reason", func(value *ReviewVerdictV1) { value.BoundedReason = " reason" }},
		{"bad identity", func(value *ReviewVerdictV1) { value.ProposalID = "bad" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.IssueCodes = append([]ReviewIssueCodeV1(nil), base.IssueCodes...)
			test.mutate(&value)
			if _, _, _, err := NewReviewVerdictV1(value); err == nil {
				t.Fatal("invalid review verdict was accepted")
			}
		})
	}
	unknown := []byte(
		`{"bounded_reason":"reason","content_fingerprint":"` + digestV1("3") +
			`","decision":"APPROVE","extra":true,"issue_codes":[],"proposal_id":"` + digestV1("1") +
			`","schema_version":"learning-review-verdict/v1","source_fingerprint":"` + digestV1("2") + `"}`,
	)
	if _, _, _, err := ParseReviewVerdictV1(unknown); err == nil {
		t.Fatal("review verdict with unknown field was parsed")
	}
	missingIssues := []byte(
		`{"bounded_reason":"reason","content_fingerprint":"` + digestV1("3") +
			`","decision":"APPROVE","proposal_id":"` + digestV1("1") +
			`","schema_version":"learning-review-verdict/v1","source_fingerprint":"` + digestV1("2") + `"}`,
	)
	if _, _, _, err := ParseReviewVerdictV1(missingIssues); err == nil {
		t.Fatal("review verdict with a missing required field was parsed")
	}
	if _, _, _, err := ParseReviewVerdictV1(append(unknown, []byte(` {}`)...)); err == nil {
		t.Fatal("review verdict with a second JSON value was parsed")
	}
}

func TestReviewVerdictV1CannotCrossProposal(t *testing.T) {
	draft := knowledgeDraftV1(t, "1.0.0", "document-v1", digestV1("a"))
	proposal, _, proposalID, err := NewProposalV1(
		proposalInputV1(ProposalKindKnowledgeV1, "1.0.0"),
		draft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	verdict, _, _, err := NewReviewVerdictV1(ReviewVerdictV1{
		SchemaVersion:      ReviewVerdictSchemaVersionV1,
		ProposalID:         proposalID,
		SourceFingerprint:  proposal.SourceFingerprint,
		ContentFingerprint: proposal.ContentFingerprint,
		Decision:           ReviewDecisionApproveV1,
		IssueCodes:         []ReviewIssueCodeV1{},
		BoundedReason:      "Approved for the exact proposal only.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := verdict.ValidateForProposalV1(proposal, digestV1("7")); err == nil {
		t.Fatal("review verdict crossed ProposalID")
	}
	proposal.ContentFingerprint = digestV1("8")
	if err := verdict.ValidateForProposalV1(proposal, proposalID); err == nil {
		t.Fatal("review verdict crossed content fingerprint")
	}
}

func reviewRequestInputV1(
	t *testing.T,
	kind ProposalKindV1,
	draft []byte,
	version string,
) ReviewRequestV1 {
	t.Helper()
	proposalInput := proposalInputV1(kind, version)
	if kind == ProposalKindSkillV1 {
		proposalInput.Target.ID = "freeagent.example.skill.review"
	}
	proposal, proposalCanonical, proposalID, err := NewProposalV1(
		proposalInput,
		draft,
		agentSourceEvidenceV1(),
	)
	if err != nil {
		t.Fatalf("NewProposalV1(review request parent): %v", err)
	}
	return ReviewRequestV1{
		SchemaVersion:       ReviewRequestSchemaVersionV1,
		ProposalID:          proposalID,
		SourceFingerprint:   proposal.SourceFingerprint,
		ContentFingerprint:  proposal.ContentFingerprint,
		DraftDigest:         proposal.DraftDigest,
		OutputSchemaVersion: ReviewVerdictSchemaVersionV1,
		MaxOutputTokens:     MaxReviewOutputTokensV1,
		ReviewPolicy:        ReviewPolicyProposalGateV1,
		Instructions:        ReviewInstructionsV1,
		ProposalCanonical:   proposalCanonical,
		DraftCanonical:      bytes.Clone(draft),
	}
}

func staticSkillDraftWithExactCanonicalSizeV1(
	t *testing.T,
	size int,
) []byte {
	t.Helper()
	_, smallest, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text:          "x",
		},
	)
	if err != nil {
		t.Fatalf("NewStaticContextV1(size probe): %v", err)
	}
	overhead := len(smallest) - 1
	if size <= overhead {
		t.Fatalf("requested static Skill Draft size %d is not representable", size)
	}
	_, canonical, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text:          strings.Repeat("x", size-overhead),
		},
	)
	if err != nil {
		t.Fatalf("NewStaticContextV1(exact size): %v", err)
	}
	if len(canonical) != size {
		t.Fatalf("static Skill Draft size = %d, want %d", len(canonical), size)
	}
	return canonical
}

func proposalInputV1(kind ProposalKindV1, version string) ProposalV1 {
	resultRef := digestV1("4")
	return ProposalV1{
		SchemaVersion: ProposalSchemaVersionV1,
		Kind:          kind,
		TenantID:      "tenant-a",
		Workspace: corecontract.WorkspaceRef{
			ID: "workspace-a", Version: "1", Digest: digestV1("a"),
		},
		ProposerAgent: corecontract.AgentRef{
			ID: "agent-author", Version: "1", Digest: digestV1("b"),
		},
		ProposerProfile: corecontract.ProfileRef{
			ID: "profile-author", Version: "1", Digest: digestV1("c"),
		},
		ProposerRunID:          "run-author-1",
		ProposerManifestDigest: digestV1("d"),
		ProposerMember: corecontract.MemberSnapshotRef{
			MemberID: "member-author", Digest: digestV1("e"),
		},
		ProposerResultRef: resultRef,
		Target: moduleapi.Ref{
			ID:      "freeagent.example.knowledge.learning",
			Version: version,
		},
	}
}

func agentSourceEvidenceV1() SourceEvidenceV1 {
	return SourceEvidenceV1{Mechanism: SourceMechanismAgentGeneratedV1}
}

func knowledgeDraftV1(
	t *testing.T,
	version string,
	documentVersion string,
	documentDigest string,
) []byte {
	return knowledgeDraftWithIdentityV1(
		t,
		"freeagent.example.knowledge.learning",
		version,
		"document-a",
		documentVersion,
		documentDigest,
		"chunk-a",
		"The governed Knowledge candidate keeps its source and scope evidence.",
		"workspace-a",
	)
}

func knowledgeDraftWithIdentityV1(
	t *testing.T,
	sourceID string,
	version string,
	documentID string,
	documentVersion string,
	documentDigest string,
	chunkID string,
	text string,
	workspaceID string,
) []byte {
	t.Helper()
	_, canonical, _, err := moduleapi.NewKnowledgeSourceV1(
		moduleapi.KnowledgeSourceV1{
			SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
			ID:            sourceID,
			Version:       version,
			Chunks: []moduleapi.KnowledgeChunkV1{{
				Document: moduleapi.KnowledgeDocumentRefV1{
					ID:      documentID,
					Version: documentVersion,
					Digest:  documentDigest,
				},
				ChunkID: chunkID,
				Text:    text,
				VisibleTo: []moduleapi.KnowledgeScopeRuleV1{{
					TenantID:     "tenant-a",
					WorkspaceID:  workspaceID,
					AgentID:      "*",
					TaskInputRef: "*",
				}},
			}},
		},
	)
	if err != nil {
		t.Fatalf("NewKnowledgeSourceV1: %v", err)
	}
	return canonical
}

func knowledgeDraftForTenantV1(t *testing.T, tenantID string) []byte {
	t.Helper()
	_, canonical, _, err := moduleapi.NewKnowledgeSourceV1(
		moduleapi.KnowledgeSourceV1{
			SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
			ID:            "freeagent.example.knowledge.learning",
			Version:       "1.0.0",
			Chunks: []moduleapi.KnowledgeChunkV1{{
				Document: moduleapi.KnowledgeDocumentRefV1{
					ID:      "document-a",
					Version: "1",
					Digest:  digestV1("a"),
				},
				ChunkID: "chunk-a",
				Text:    "Tenant-scoped candidate.",
				VisibleTo: []moduleapi.KnowledgeScopeRuleV1{{
					TenantID:     tenantID,
					WorkspaceID:  "*",
					AgentID:      "*",
					TaskInputRef: "*",
				}},
			}},
		},
	)
	if err != nil {
		t.Fatalf("NewKnowledgeSourceV1: %v", err)
	}
	return canonical
}

func digestV1(character string) string {
	return strings.Repeat(character, 64)
}
