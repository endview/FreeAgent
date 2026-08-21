package currentstore

import (
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// ConversationSummaryCandidateForCompilerV1 returns the only summary that a
// new Conversation Attempt may offer to the Context Compiler: the verified
// summary embedded in the direct successful predecessor's unique model
// ContextCompilation. A nil result means that the candidate is absent or not
// applicable. Corrupt authoritative content fails closed.
//
// Only SourceTurnDigests and Text are returned. Source token estimates belong
// to the earlier request and must never be reused for the current request.
func ConversationSummaryCandidateForCompilerV1(
	run RunForLoop,
) (*corecontract.ContextCompilationSummaryV1, error) {
	turn := run.Manifest.ConversationTurn
	if turn == nil || len(run.ConversationHistory) == 0 {
		return nil, nil
	}
	direct := run.ConversationHistory[len(run.ConversationHistory)-1]
	if direct.SourceContextCompilation == nil {
		return nil, nil
	}
	if direct.SourceContextCompilationAttemptID == "" {
		return nil, conversationSummaryIntegrity(
			"source ContextCompilation Attempt identity",
			ErrAdmissionIntegrity,
		)
	}

	if err := run.Manifest.ValidateAgainstMember(run.Member); err != nil {
		return nil, conversationSummaryIntegrity(
			"current Manifest/Member Agent and Workspace identity",
			err,
		)
	}
	if turn.TurnIndex <= 1 ||
		uint64(len(run.ConversationHistory)) != turn.TurnIndex-1 ||
		direct.TurnIndex != turn.TurnIndex-1 ||
		direct.SourceRunID != turn.PredecessorRunID ||
		direct.SourceAttemptID == "" {
		return nil, conversationSummaryIntegrity(
			"direct predecessor projection",
			ErrAdmissionIntegrity,
		)
	}

	record := *direct.SourceContextCompilation
	if err := validateConversationSummaryContentRecord(
		record,
		ContentContextCompilation,
	); err != nil {
		return nil, conversationSummaryIntegrity(
			"source ContextCompilation content",
			err,
		)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		record.CanonicalBytes,
	)
	if err != nil {
		return nil, conversationSummaryIntegrity(
			"source ContextCompilation wire",
			err,
		)
	}
	// A Conversation can legitimately continue after a new immutable
	// Workspace or ContextPolicy version is published. Such a source is valid
	// history but is not reusable under the current frozen scope/policy.
	if compilation.WorkspaceScope != run.Member.Workspace ||
		compilation.ContextPolicy != run.Member.ContextPolicy {
		return nil, nil
	}
	if compilation.EstimatorVersion !=
		corecontract.ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1 ||
		compilation.SummaryAlgorithmVersion !=
			corecontract.ContextSummaryHeadTailExtractiveV1 {
		return nil, conversationSummaryIntegrity(
			"source ContextCompilation scope, policy, or algorithm",
			ErrAdmissionIntegrity,
		)
	}
	if compilation.Summary == nil {
		return nil, nil
	}

	sources := compilation.Summary.SourceTurnDigests
	// The direct predecessor could only have seen the turns before itself.
	// This strict bound also prevents a crafted summary-of-summary candidate
	// from claiming the current direct predecessor as raw source material.
	if len(sources) == 0 || len(sources) > len(run.ConversationHistory)-1 {
		return nil, conversationSummaryIntegrity(
			"source summary range",
			ErrAdmissionIntegrity,
		)
	}
	messages := make([]moduleapi.ModelMessageV1, 0, len(sources)*2)
	for index, entry := range run.ConversationHistory {
		if entry.TurnIndex != uint64(index+1) ||
			entry.SourceRunID == "" || entry.SourceAttemptID == "" {
			return nil, conversationSummaryIntegrity(
				"raw Conversation History sequence",
				ErrAdmissionIntegrity,
			)
		}
		if err := validateConversationSummaryContentRecord(
			entry.UserContent,
			ContentTaskInput,
		); err != nil {
			return nil, conversationSummaryIntegrity(
				"raw Conversation USER content",
				err,
			)
		}
		if err := validateConversationSummaryContentRecord(
			entry.AssistantContent,
			ContentModelResult,
		); err != nil {
			return nil, conversationSummaryIntegrity(
				"raw Conversation ASSISTANT content",
				err,
			)
		}
		user, err := corecontract.RestoreTaskInputV1(
			entry.UserContent.CanonicalBytes,
		)
		if err != nil {
			return nil, conversationSummaryIntegrity(
				"raw Conversation USER wire",
				err,
			)
		}
		assistant, err := moduleapi.RestoreModelGenerateOutputV1(
			entry.AssistantContent.CanonicalBytes,
		)
		if err != nil || assistant.ActionRequest != nil ||
			assistant.AssistantText == "" {
			if err == nil {
				err = ErrAdmissionIntegrity
			}
			return nil, conversationSummaryIntegrity(
				"raw Conversation ASSISTANT wire",
				err,
			)
		}
		if index >= len(sources) {
			continue
		}
		digest, err := corecontract.ContextConversationTurnDigestV1(
			entry.TurnIndex,
			entry.UserContent.Digest,
			entry.AssistantContent.Digest,
		)
		if err != nil || sources[index] != digest {
			if err == nil {
				err = ErrAdmissionIntegrity
			}
			return nil, conversationSummaryIntegrity(
				"source summary oldest raw prefix",
				err,
			)
		}
		messages = append(messages,
			moduleapi.ModelMessageV1{
				Role: moduleapi.ModelRoleUser, Content: user.Text,
			},
			moduleapi.ModelMessageV1{
				Role:    moduleapi.ModelRoleAssistant,
				Content: assistant.AssistantText,
			},
		)
	}
	expectedText, err := corecontract.ContextHeadTailSummaryV1(messages)
	if err != nil || compilation.Summary.Text != expectedText {
		if err == nil {
			err = ErrAdmissionIntegrity
		}
		return nil, conversationSummaryIntegrity(
			"source summary deterministic text",
			err,
		)
	}
	return &corecontract.ContextCompilationSummaryV1{
		SourceTurnDigests: append([]string(nil), sources...),
		Text:              compilation.Summary.Text,
	}, nil
}

func validateConversationSummaryContentRecord(
	record ContentRecord,
	wantKind ContentKind,
) error {
	if record.Kind != wantKind || record.MediaType != admissionJSONMediaType ||
		record.SizeBytes != int64(len(record.CanonicalBytes)) {
		return ErrContentIntegrity
	}
	computed, err := ComputeContentDigest(
		record.Kind,
		record.MediaType,
		record.CanonicalBytes,
	)
	if err != nil {
		return err
	}
	if computed != record.Digest {
		return ErrContentIntegrity
	}
	return nil
}

func conversationSummaryIntegrity(subject string, err error) error {
	if err == nil {
		err = ErrAdmissionIntegrity
	}
	return fmt.Errorf("%w: Conversation summary %s: %w", ErrLoopIntegrity, subject, err)
}
