package coreloop

import (
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	reasonCollaborationSpecialistOutputInvalid   = "COLLABORATION_SPECIALIST_OUTPUT_INVALID"
	reasonCollaborationSpecialistEvidenceInvalid = "COLLABORATION_SPECIALIST_EVIDENCE_INVALID"
	reasonCollaborationReviewOutputInvalid       = "COLLABORATION_REVIEWER_OUTPUT_INVALID"
)

// normalizeCollaborationSpecialistOutcomeV1 closes the W5 model wire before
// the original Attempt outcome is persisted. It never creates another
// Attempt: invalid structured output becomes a deterministic failure while
// the original Usage and provider receipt fields remain attached to the same
// call.
func normalizeCollaborationSpecialistOutcomeV1(
	input currentstore.CommitModelDispatchOutcomeInput,
	compilation *corecontract.ContextCompilationV1,
) (currentstore.CommitModelDispatchOutcomeInput, string, error) {
	if input.State != corecontract.ModelAttemptSucceeded {
		return input, "", nil
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(input.OutputCanonical)
	if err != nil || output.ActionRequest != nil {
		return failCollaborationOutcomeV1(
			input,
			reasonCollaborationSpecialistOutputInvalid,
		), "", nil
	}
	contribution, contributionCanonical, contributionDigest, err :=
		corecontract.ParseSpecialistContributionV1(
			[]byte(output.AssistantText),
		)
	if err != nil {
		return failCollaborationOutcomeV1(
			input,
			reasonCollaborationSpecialistOutputInvalid,
		), "", nil
	}
	allowed := collaborationAllowedEvidenceRefsV1(compilation)
	for _, evidence := range contribution.Evidence {
		if _, ok := allowed[evidence.Ref]; !ok {
			return failCollaborationOutcomeV1(
				input,
				reasonCollaborationSpecialistEvidenceInvalid,
			), "", nil
		}
	}
	_, normalized, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     string(contributionCanonical),
			ProviderRequestID: output.ProviderRequestID,
		},
	)
	if err != nil {
		return input, "", err
	}
	input.OutputCanonical = normalized
	input.ErrorClassification = ""
	input.UnknownReason = ""
	return input, contributionDigest, nil
}

func collaborationAllowedEvidenceRefsV1(
	compilation *corecontract.ContextCompilationV1,
) map[string]struct{} {
	allowed := make(map[string]struct{})
	if compilation == nil {
		return allowed
	}
	addRetrieval := func(retrieval corecontract.KnowledgeRetrievalEvidenceV1) {
		if moduleapi.ValidSHA256(retrieval.Source.Digest) {
			allowed[retrieval.Source.Digest] = struct{}{}
		}
		for _, hit := range retrieval.Hits {
			if moduleapi.ValidSHA256(hit.Document.Digest) {
				allowed[hit.Document.Digest] = struct{}{}
			}
			if moduleapi.ValidSHA256(hit.ChunkDigest) {
				allowed[hit.ChunkDigest] = struct{}{}
			}
		}
	}
	for _, retrieval := range compilation.KnowledgeRetrievals {
		addRetrieval(retrieval)
	}
	for _, reuse := range compilation.KnowledgeReuses {
		addRetrieval(reuse.FreshRetrieval)
	}
	return allowed
}

// normalizeCollaborationReviewerOutcomeV1 parses only model-owned decision
// fields, injects Host-owned family/set/round identity for validation, and
// persists the canonical model-owned semantic wire inside the original outer
// MODEL_RESULT. Current Store deterministically rebuilds the Host-bound
// VerdictCanonical from that exact result and the frozen family projection;
// it is not a second persisted model fact.
func normalizeCollaborationReviewerOutcomeV1(
	input currentstore.CommitModelDispatchOutcomeInput,
	root corecontract.RunManifest,
	contributionSetDigest string,
	repairRound uint32,
) (
	currentstore.CommitModelDispatchOutcomeInput,
	*corecontract.CollaborationReviewVerdictV1,
	error,
) {
	if input.State != corecontract.ModelAttemptSucceeded {
		return input, nil, nil
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(input.OutputCanonical)
	if err != nil || output.ActionRequest != nil {
		failed := failCollaborationOutcomeV1(
			input,
			reasonCollaborationReviewOutputInvalid,
		)
		return failed, nil, nil
	}
	verdict, _, err :=
		corecontract.ParseCollaborationReviewVerdictV1(
			[]byte(output.AssistantText),
			root.ManifestDigest,
			contributionSetDigest,
			repairRound,
		)
	if err != nil || verdict.ValidateForCollaborationReviewV1(
		root,
		contributionSetDigest,
		repairRound,
	) != nil {
		failed := failCollaborationOutcomeV1(
			input,
			reasonCollaborationReviewOutputInvalid,
		)
		return failed, nil, nil
	}
	semanticCanonical, err :=
		corecontract.CanonicalCollaborationReviewModelVerdictV1(verdict)
	if err != nil {
		return input, nil, err
	}
	_, normalized, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     string(semanticCanonical),
			ProviderRequestID: output.ProviderRequestID,
		},
	)
	if err != nil {
		return input, nil, err
	}
	input.OutputCanonical = normalized
	input.ErrorClassification = ""
	input.UnknownReason = ""
	result := verdict
	return input, &result, nil
}

func failCollaborationOutcomeV1(
	input currentstore.CommitModelDispatchOutcomeInput,
	classification string,
) currentstore.CommitModelDispatchOutcomeInput {
	input.State = corecontract.ModelAttemptFailed
	input.OutputCanonical = nil
	input.ErrorClassification = classification
	input.UnknownReason = ""
	return input
}
