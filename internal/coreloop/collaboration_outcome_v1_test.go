package coreloop

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestNormalizeCollaborationSpecialistOutcomeCanonicalizesOriginalAttempt(
	t *testing.T,
) {
	sourceDigest := strings.Repeat("a", moduleapi.SHA256HexLength)
	documentDigest := strings.Repeat("b", moduleapi.SHA256HexLength)
	chunkDigest := strings.Repeat("c", moduleapi.SHA256HexLength)
	modelJSON := `{
		"risks":[],
		"schema_version":"specialist-contribution/v1",
		"proposal":"Use the bounded implementation.",
		"evidence":[
			{"ref":"` + sourceDigest + `","bounded_claim":"The source is in the prompt."},
			{"ref":"` + documentDigest + `","bounded_claim":"The document is in the prompt."},
			{"ref":"` + chunkDigest + `","bounded_claim":"The chunk is in the prompt."}
		],
		"conflicts":[],
		"assumptions":[]
	}`
	input := collaborationSucceededOutcomeV1(t, modelJSON)
	usage := append([]byte(nil), input.UsageReceiptCanonical...)
	compilation := &corecontract.ContextCompilationV1{
		KnowledgeRetrievals: []corecontract.KnowledgeRetrievalEvidenceV1{{
			Source: moduleapi.KnowledgeSourceRefV1{Digest: sourceDigest},
			Hits: []moduleapi.KnowledgeHitV1{{
				Document:    moduleapi.KnowledgeDocumentRefV1{Digest: documentDigest},
				ChunkDigest: chunkDigest,
			}},
		}},
	}

	normalized, digest, err := normalizeCollaborationSpecialistOutcomeV1(
		input,
		compilation,
	)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if normalized.State != corecontract.ModelAttemptSucceeded ||
		!moduleapi.ValidSHA256(digest) ||
		!bytes.Equal(normalized.UsageReceiptCanonical, usage) ||
		normalized.AttemptID != input.AttemptID ||
		normalized.ExpectedAttemptRevision != input.ExpectedAttemptRevision {
		t.Fatalf("normalized outcome=%+v digest=%q", normalized, digest)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(
		normalized.OutputCanonical,
	)
	if err != nil || output.ProviderRequestID != "provider-request" {
		t.Fatalf("normalized output=%+v error=%v", output, err)
	}
	contribution, canonical, restoredDigest, err :=
		corecontract.ParseSpecialistContributionV1(
			[]byte(output.AssistantText),
		)
	if err != nil || contribution.Proposal == "" ||
		restoredDigest != digest || output.AssistantText != string(canonical) {
		t.Fatalf(
			"contribution=%+v canonical=%s digest=%q error=%v",
			contribution,
			canonical,
			restoredDigest,
			err,
		)
	}
}

func TestNormalizeCollaborationSpecialistOutcomeFailsClosedOnWireAndEvidence(
	t *testing.T,
) {
	unknownRef := strings.Repeat("d", moduleapi.SHA256HexLength)
	validWithUnknownEvidence := `{"schema_version":"specialist-contribution/v1","proposal":"Proposal.","evidence":[{"ref":"` + unknownRef + `","bounded_claim":"Not visible."}],"assumptions":[],"risks":[],"conflicts":[]}`
	tests := []struct {
		name           string
		input          currentstore.CommitModelDispatchOutcomeInput
		compilation    *corecontract.ContextCompilationV1
		classification string
	}{
		{
			name: "malformed contribution",
			input: collaborationSucceededOutcomeV1(
				t,
				`{"schema_version":"specialist-contribution/v1"}`,
			),
			classification: reasonCollaborationSpecialistOutputInvalid,
		},
		{
			name: "invisible evidence ref",
			input: collaborationSucceededOutcomeV1(
				t,
				validWithUnknownEvidence,
			),
			classification: reasonCollaborationSpecialistEvidenceInvalid,
		},
		{
			name: "no RAG evidence permits only empty evidence",
			input: collaborationSucceededOutcomeV1(
				t,
				validWithUnknownEvidence,
			),
			compilation:    &corecontract.ContextCompilationV1{},
			classification: reasonCollaborationSpecialistEvidenceInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage := append([]byte(nil), test.input.UsageReceiptCanonical...)
			normalized, digest, err :=
				normalizeCollaborationSpecialistOutcomeV1(
					test.input,
					test.compilation,
				)
			if err != nil || digest != "" ||
				normalized.State != corecontract.ModelAttemptFailed ||
				len(normalized.OutputCanonical) != 0 ||
				normalized.ErrorClassification != test.classification ||
				!bytes.Equal(normalized.UsageReceiptCanonical, usage) {
				t.Fatalf(
					"normalized=%+v digest=%q error=%v",
					normalized,
					digest,
					err,
				)
			}
		})
	}
}

func TestNormalizeCollaborationSpecialistOutcomeAcceptsReuseFreshRetrievalRef(
	t *testing.T,
) {
	chunkDigest := strings.Repeat("e", moduleapi.SHA256HexLength)
	input := collaborationSucceededOutcomeV1(
		t,
		`{"schema_version":"specialist-contribution/v1","proposal":"Proposal.","evidence":[{"ref":"`+chunkDigest+`","bounded_claim":"Visible reused chunk."}],"assumptions":[],"risks":[],"conflicts":[]}`,
	)
	compilation := &corecontract.ContextCompilationV1{
		KnowledgeReuses: []corecontract.KnowledgeReuseEvidenceV1{{
			FreshRetrieval: corecontract.KnowledgeRetrievalEvidenceV1{
				Hits: []moduleapi.KnowledgeHitV1{{ChunkDigest: chunkDigest}},
			},
		}},
	}
	normalized, digest, err := normalizeCollaborationSpecialistOutcomeV1(
		input,
		compilation,
	)
	if err != nil || normalized.State != corecontract.ModelAttemptSucceeded ||
		!moduleapi.ValidSHA256(digest) {
		t.Fatalf("normalized=%+v digest=%q error=%v", normalized, digest, err)
	}
}

func TestNormalizeCollaborationReviewerOutcomeInjectsHostIdentity(
	t *testing.T,
) {
	root, setDigest := collaborationReviewerRootV1()
	input := collaborationSucceededOutcomeV1(
		t,
		`{"schema_version":"collaboration-review-verdict/v1","decision":"REPAIR_REQUIRED","issue_codes":["MISSING_EVIDENCE"],"affected_slot_ids":["a-risk"],"bounded_reason":"The risk contribution needs repair."}`,
	)
	normalized, verdict, err := normalizeCollaborationReviewerOutcomeV1(
		input,
		root,
		setDigest,
		0,
	)
	if err != nil || verdict == nil ||
		normalized.State != corecontract.ModelAttemptSucceeded ||
		verdict.FamilyDigest != root.ManifestDigest ||
		verdict.ContributionSetDigest != setDigest ||
		verdict.RepairRound != 0 ||
		verdict.Decision != corecontract.CollaborationReviewDecisionRepairRequiredV1 {
		t.Fatalf("normalized=%+v verdict=%+v error=%v", normalized, verdict, err)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(
		normalized.OutputCanonical,
	)
	if err != nil || output.ProviderRequestID != "provider-request" {
		t.Fatalf("normalized output=%+v error=%v", output, err)
	}
	if strings.Contains(output.AssistantText, "family_digest") ||
		strings.Contains(output.AssistantText, "contribution_set_digest") ||
		strings.Contains(output.AssistantText, "repair_round") {
		t.Fatalf("MODEL_RESULT leaked Host-owned identity: %s", output.AssistantText)
	}
	restored, restoredCanonical, err :=
		corecontract.ParseCollaborationReviewVerdictV1(
			[]byte(output.AssistantText),
			root.ManifestDigest,
			setDigest,
			0,
		)
	if err != nil || !reflect.DeepEqual(restored, *verdict) ||
		output.AssistantText == string(restoredCanonical) {
		t.Fatalf("restored=%+v verdict=%+v error=%v", restored, verdict, err)
	}
}

func TestNormalizeCollaborationReviewerOutcomeRejectsOpenOrIllegalWire(
	t *testing.T,
) {
	root, setDigest := collaborationReviewerRootV1()
	tests := []struct {
		name  string
		round uint32
		wire  string
	}{
		{
			name: "Host identity echo",
			wire: `{"schema_version":"collaboration-review-verdict/v1","family_digest":"` + root.ManifestDigest + `","decision":"APPROVE","issue_codes":[],"affected_slot_ids":[],"bounded_reason":"Complete."}`,
		},
		{
			name: "legacy schema",
			wire: `{"schema_version":"review-verdict/v1","decision":"APPROVE","issue_codes":[],"affected_slot_ids":[],"bounded_reason":"Complete."}`,
		},
		{
			name:  "second repair",
			round: 1,
			wire:  `{"schema_version":"collaboration-review-verdict/v1","decision":"REPAIR_REQUIRED","issue_codes":["MISSING_EVIDENCE"],"affected_slot_ids":["a-risk"],"bounded_reason":"Repair again."}`,
		},
		{
			name: "unknown slot",
			wire: `{"schema_version":"collaboration-review-verdict/v1","decision":"REJECT","issue_codes":["MISSING_EVIDENCE"],"affected_slot_ids":["z-unknown"],"bounded_reason":"Unknown slot."}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := collaborationSucceededOutcomeV1(t, test.wire)
			usage := append([]byte(nil), input.UsageReceiptCanonical...)
			normalized, verdict, err :=
				normalizeCollaborationReviewerOutcomeV1(
					input,
					root,
					setDigest,
					test.round,
				)
			if err != nil || verdict != nil ||
				normalized.State != corecontract.ModelAttemptFailed ||
				len(normalized.OutputCanonical) != 0 ||
				normalized.ErrorClassification !=
					reasonCollaborationReviewOutputInvalid ||
				!bytes.Equal(normalized.UsageReceiptCanonical, usage) {
				t.Fatalf(
					"normalized=%+v verdict=%+v error=%v",
					normalized,
					verdict,
					err,
				)
			}
		})
	}
}

func collaborationSucceededOutcomeV1(
	t *testing.T,
	assistantText string,
) currentstore.CommitModelDispatchOutcomeInput {
	t.Helper()
	_, canonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     assistantText,
			ProviderRequestID: "provider-request",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return currentstore.CommitModelDispatchOutcomeInput{
		AttemptID:               "attempt-1",
		InvocationID:            "attempt-1",
		ExpectedAttemptRevision: 1,
		State:                   corecontract.ModelAttemptSucceeded,
		OutputCanonical:         canonical,
		UsageReceiptCanonical:   []byte(`{"usage":"preserved"}`),
		ProviderRequestID:       "provider-request",
	}
}

func collaborationReviewerRootV1() (corecontract.RunManifest, string) {
	familyDigest := strings.Repeat("f", moduleapi.SHA256HexLength)
	setDigest := strings.Repeat("1", moduleapi.SHA256HexLength)
	reviewer := &corecontract.CompositeReviewerRunRefV1{
		RunID:               "reviewer-0",
		ReviewLogicalStepID: corecontract.CompositeReviewLogicalStepIDV1,
	}
	return corecontract.RunManifest{
		ManifestDigest: familyDigest,
		Composite: &corecontract.CompositeRunNodeV1{
			Role:      corecontract.CompositeRunRoleRootV1,
			RootRunID: "root-run",
			Plan: &corecontract.CompositeRunPlanV1{
				Children: []corecontract.CompositeChildRunRefV1{
					{SlotID: "a-risk"},
					{SlotID: "b-delivery"},
				},
				Reviewer: reviewer,
				Decision: &corecontract.CompositeDecisionPlanV1{
					SchemaVersion: corecontract.CompositeDecisionPlanSchemaVersionV1,
				},
			},
		},
	}, setDigest
}
