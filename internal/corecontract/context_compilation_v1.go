package corecontract

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ContextCompilationSchemaVersionV1     = "context-compilation/v1"
	contextHistoryTurnDigestDomainV1      = "freeagent.context-history-turn/v1"
	contextConversationTurnDigestDomainV1 = "freeagent.context-conversation-turn/v1"
	contextSummaryMaxTextBytesV1          = 1024
	contextSummaryPrefixV1                = "Earlier History (deterministic extract):\n"
	contextSummaryOmissionV1              = "\n[... earlier History omitted ...]\n"
)

type contextHistoryTurnIdentityV1 struct {
	Sequence            uint64 `json:"sequence"`
	SourceContentDigest string `json:"source_content_digest"`
}

type contextConversationTurnIdentityV1 struct {
	TurnIndex                    uint64 `json:"turn_index"`
	UserSourceContentDigest      string `json:"user_source_content_digest"`
	AssistantSourceContentDigest string `json:"assistant_source_content_digest"`
}

// ContextHistoryTurnDigestV1 identifies one current S2.1 History turn without
// Run, Attempt, timestamp or provider metadata. Sequence distinguishes repeated
// MODEL_RESULT content, while the source digest remains directly Store-verifiable.
func ContextHistoryTurnDigestV1(
	sequence uint64,
	sourceContentDigest string,
) (string, error) {
	if sequence == 0 || sequence > math.MaxInt64 {
		return "", fmt.Errorf(
			"corecontract: History turn sequence must fit a positive signed integer",
		)
	}
	if !moduleapi.ValidSHA256(sourceContentDigest) {
		return "", fmt.Errorf(
			"corecontract: invalid History turn source content digest",
		)
	}
	canonical, err := canonicalJSON(contextHistoryTurnIdentityV1{
		Sequence:            sequence,
		SourceContentDigest: sourceContentDigest,
	})
	if err != nil {
		return "", err
	}
	return moduleapi.Digest(contextHistoryTurnDigestDomainV1, canonical), nil
}

// ContextConversationTurnDigestV1 identifies one complete, chronological
// Conversation predecessor turn. Both source digests remain directly
// Store-verifiable; Run, Attempt, timestamp and provider metadata are excluded
// from the retention identity used by Summary and Drop evidence.
func ContextConversationTurnDigestV1(
	turnIndex uint64,
	userSourceContentDigest string,
	assistantSourceContentDigest string,
) (string, error) {
	if turnIndex == 0 || turnIndex > math.MaxInt64 {
		return "", fmt.Errorf(
			"corecontract: Conversation turn index must fit a positive signed integer",
		)
	}
	if !moduleapi.ValidSHA256(userSourceContentDigest) {
		return "", fmt.Errorf(
			"corecontract: invalid Conversation USER source content digest",
		)
	}
	if !moduleapi.ValidSHA256(assistantSourceContentDigest) {
		return "", fmt.Errorf(
			"corecontract: invalid Conversation ASSISTANT source content digest",
		)
	}
	canonical, err := canonicalJSON(contextConversationTurnIdentityV1{
		TurnIndex:                    turnIndex,
		UserSourceContentDigest:      userSourceContentDigest,
		AssistantSourceContentDigest: assistantSourceContentDigest,
	})
	if err != nil {
		return "", err
	}
	return moduleapi.Digest(
		contextConversationTurnDigestDomainV1,
		canonical,
	), nil
}

// ContextHeadTailSummaryV1 is the single deterministic implementation used by
// both Context Compiler and Store verification. It accepts either the legacy
// non-empty continuous ASSISTANT-only History range or a non-empty range of
// complete USER+ASSISTANT Conversation pairs. It performs no I/O and reads no
// runtime state.
func ContextHeadTailSummaryV1(
	messages []moduleapi.ModelMessageV1,
) (string, error) {
	if len(messages) == 0 || len(messages) > moduleapi.MaxManifestEntries {
		return "", fmt.Errorf(
			"corecontract: summary History range must contain between 1 and %d messages",
			moduleapi.MaxManifestEntries,
		)
	}
	legacyAssistantRange := true
	conversationPairRange := len(messages)%2 == 0
	for index, message := range messages {
		if message.Role != moduleapi.ModelRoleAssistant {
			legacyAssistantRange = false
		}
		wantConversationRole := moduleapi.ModelRoleUser
		if index%2 == 1 {
			wantConversationRole = moduleapi.ModelRoleAssistant
		}
		if message.Role != wantConversationRole {
			conversationPairRange = false
		}
		if err := validateChatText("summary History message", message.Content); err != nil {
			return "", err
		}
	}
	if !legacyAssistantRange && !conversationPairRange {
		return "", fmt.Errorf(
			"corecontract: summary History must be all ASSISTANT or complete USER+ASSISTANT pairs",
		)
	}
	available := contextSummaryMaxTextBytesV1 -
		len(contextSummaryPrefixV1) - len(contextSummaryOmissionV1)
	headBudget := available / 2
	tailBudget := available - headBudget
	head := make([]byte, 0, headBudget)
	tail := make([]byte, 0, tailBudget)
	total := 0
	for index, message := range messages {
		fragments := []string{
			"[", string(message.Role), "]\n", message.Content,
		}
		if index+1 != len(messages) {
			fragments = append(fragments, "\n")
		}
		for _, fragment := range fragments {
			total += len(fragment)
			head = appendContextSummaryHead(head, fragment, headBudget)
			tail = appendContextSummaryTail(tail, fragment, tailBudget)
		}
	}
	if total <= available {
		return moduleapi.CanonicalText(
			contextSummaryPrefixV1 + completeContextHistoryText(messages),
		), nil
	}
	head = trimContextSummaryUTF8Suffix(head)
	tail = trimContextSummaryUTF8Prefix(tail)
	return moduleapi.CanonicalText(
		contextSummaryPrefixV1 + string(head) +
			contextSummaryOmissionV1 + string(tail),
	), nil
}

func completeContextHistoryText(messages []moduleapi.ModelMessageV1) string {
	var builder strings.Builder
	for index, message := range messages {
		if index != 0 {
			builder.WriteByte('\n')
		}
		builder.WriteByte('[')
		builder.WriteString(string(message.Role))
		builder.WriteString("]\n")
		builder.WriteString(message.Content)
	}
	return builder.String()
}

func appendContextSummaryHead(current []byte, fragment string, limit int) []byte {
	remaining := limit - len(current)
	if remaining <= 0 {
		return current
	}
	if len(fragment) <= remaining {
		return append(current, fragment...)
	}
	return append(current, fragment[:remaining]...)
}

func appendContextSummaryTail(current []byte, fragment string, limit int) []byte {
	if len(fragment) >= limit {
		return append(current[:0], fragment[len(fragment)-limit:]...)
	}
	if len(current)+len(fragment) > limit {
		drop := len(current) + len(fragment) - limit
		copy(current, current[drop:])
		current = current[:len(current)-drop]
	}
	return append(current, fragment...)
}

func trimContextSummaryUTF8Suffix(value []byte) []byte {
	for len(value) != 0 && !utf8.Valid(value) {
		value = value[:len(value)-1]
	}
	return value
}

func trimContextSummaryUTF8Prefix(value []byte) []byte {
	for len(value) != 0 && !utf8.Valid(value) {
		value = value[1:]
	}
	return value
}

// ContextCompilationStopReasonV1 is the closed reason set for the one S2.1
// compilation record. The record embeds summary and Drop evidence instead of
// creating separate object families.
type ContextCompilationStopReasonV1 string

const (
	ContextCompilationSummaryToWatermark              ContextCompilationStopReasonV1 = "SUMMARY_TO_WATERMARK"
	ContextCompilationSummaryStrictlyReduced          ContextCompilationStopReasonV1 = "SUMMARY_STRICTLY_REDUCED"
	ContextCompilationNoEligibleSummary               ContextCompilationStopReasonV1 = "NO_ELIGIBLE_SUMMARY_UNDER_BUDGET"
	ContextCompilationDropToWatermark                 ContextCompilationStopReasonV1 = "DROP_TO_WATERMARK"
	ContextCompilationRetrievalBelowWatermark         ContextCompilationStopReasonV1 = "RETRIEVAL_EVIDENCE_BELOW_WATERMARK"
	ContextCompilationKnowledgeShortcutBelowWatermark ContextCompilationStopReasonV1 = "KNOWLEDGE_SHORTCUT_BELOW_WATERMARK"
	ContextCompilationActionResultReserved            ContextCompilationStopReasonV1 = "ACTION_RESULT_RESERVED_BELOW_WATERMARK"
	ContextCompilationCompositeBelowWatermark         ContextCompilationStopReasonV1 = "COMPOSITE_EVIDENCE_BELOW_WATERMARK"
)

func (reason ContextCompilationStopReasonV1) Validate() error {
	switch reason {
	case ContextCompilationSummaryToWatermark,
		ContextCompilationSummaryStrictlyReduced,
		ContextCompilationNoEligibleSummary,
		ContextCompilationDropToWatermark,
		ContextCompilationRetrievalBelowWatermark,
		ContextCompilationKnowledgeShortcutBelowWatermark,
		ContextCompilationActionResultReserved,
		ContextCompilationCompositeBelowWatermark:
		return nil
	default:
		return fmt.Errorf(
			"corecontract: unsupported context compilation stop reason %q",
			reason,
		)
	}
}

// ContextCompilationUnitKindV1 identifies a complete unit removed from the
// request view. S2.1 only removes one complete persisted ASSISTANT History
// turn at a time.
type ContextCompilationUnitKindV1 string

const ContextCompilationUnitHistoryTurn ContextCompilationUnitKindV1 = "HISTORY_TURN"

func (kind ContextCompilationUnitKindV1) Validate() error {
	switch kind {
	case ContextCompilationUnitHistoryTurn:
		return nil
	default:
		return fmt.Errorf(
			"corecontract: unsupported context compilation unit kind %q",
			kind,
		)
	}
}

// ContextCompilationSummaryV1 is nested evidence for one deterministic
// summary over one continuous, complete History-turn range.
type ContextCompilationSummaryV1 struct {
	SourceTurnDigests    []string `json:"source_turn_digests"`
	Text                 string   `json:"text"`
	BeforeEstimateTokens uint64   `json:"before_estimate_tokens"`
	AfterEstimateTokens  uint64   `json:"after_estimate_tokens"`
}

// ContextCompilationDropV1 records one oldest complete unit removed in one
// Drop batch and the estimate immediately before and after that batch.
type ContextCompilationDropV1 struct {
	UnitKind             ContextCompilationUnitKindV1 `json:"unit_kind"`
	UnitDigest           string                       `json:"unit_digest"`
	BeforeEstimateTokens uint64                       `json:"before_estimate_tokens"`
	AfterEstimateTokens  uint64                       `json:"after_estimate_tokens"`
}

// KnowledgeRetrievalEvidenceV1 closes one dynamic context.provide/v1 result
// over its exact frozen Binding, authority, request, scope, source and output.
// The evidence remains part of the sole Context Compilation record; it is not
// a second retrieval ledger or a mutable knowledge snapshot.
type KnowledgeRetrievalEvidenceV1 struct {
	BindingIndex        uint32                          `json:"binding_index"`
	ConfigRef           string                          `json:"config_ref"`
	AuthorityCeilingRef string                          `json:"authority_ceiling_ref"`
	RequestDigest       string                          `json:"request_digest"`
	Scope               moduleapi.KnowledgeQueryScopeV1 `json:"scope"`
	Source              moduleapi.KnowledgeSourceRefV1  `json:"source"`
	Hits                []moduleapi.KnowledgeHitV1      `json:"hits"`
	OutputDigest        string                          `json:"output_digest"`
	Provenance          *KnowledgeRetrievalProvenanceV1 `json:"provenance,omitempty"`
}

// KnowledgeRetrievalProvenanceV1 is present only for a fresh retrieval whose
// Binding enables exact-question reuse. RetrievedAtUnixMS is captured at the
// successful Knowledge read boundary; it is not a later model Attempt time.
// Routing metadata remains evidence only and is never projected into prompt.
type KnowledgeRetrievalProvenanceV1 struct {
	Provider                moduleapi.ActivatedModuleRef `json:"provider"`
	RoutingAlgorithmVersion string                       `json:"routing_algorithm_version"`
	DecisionSetDigest       string                       `json:"decision_set_digest"`
	RetrievedAtUnixMS       uint64                       `json:"retrieved_at_unix_ms"`
}

// KnowledgeReuseEvidenceV1 embeds the exact earlier fresh retrieval used to
// rebuild the current Knowledge prompt. Source identity is explicit so Store
// validation can close it against one Conversation turn and one successful
// Attempt/Compilation without a cache table. Counter proofs reference the
// sole Memory read in this Compilation instead of duplicating its snapshot,
// authority, scope, configuration or evaluation time.
type KnowledgeReuseEvidenceV1 struct {
	SourceConversationID string                       `json:"source_conversation_id"`
	SourceTurnIndex      uint64                       `json:"source_turn_index"`
	SourceRunID          string                       `json:"source_run_id"`
	SourceAttemptID      string                       `json:"source_attempt_id"`
	SourceCompilationRef string                       `json:"source_compilation_ref"`
	FreshRetrieval       KnowledgeRetrievalEvidenceV1 `json:"fresh_retrieval"`
	MemoryBindingIndex   uint32                       `json:"memory_binding_index"`
	CategoryCounter      moduleapi.MemoryCandidateV1  `json:"category_counter"`
	RepeatedTermCounter  moduleapi.MemoryCandidateV1  `json:"repeated_term_counter"`
}

// KnowledgeShortcutModeV1 is intentionally closed to NOT_SELECTED. A later
// reuse slice must add a distinct evidence shape instead of disguising a
// cached answer or an absent provider call as this routing-only shortcut.
type KnowledgeShortcutModeV1 string

const KnowledgeShortcutNotSelectedV1 KnowledgeShortcutModeV1 = "NOT_SELECTED"

// KnowledgeShortcutEvidenceV1 proves that one exact, authorized Knowledge
// Binding was deterministically not selected before retrieval. It contains no
// request, output or hit fields and therefore cannot represent a fake zero-hit
// provider result.
type KnowledgeShortcutEvidenceV1 struct {
	BindingIndex             uint32                          `json:"binding_index"`
	ConfigRef                string                          `json:"config_ref"`
	AuthorityCeilingRef      string                          `json:"authority_ceiling_ref"`
	Scope                    moduleapi.KnowledgeQueryScopeV1 `json:"scope"`
	Source                   moduleapi.KnowledgeSourceRefV1  `json:"source"`
	DecisionSetDigest        string                          `json:"decision_set_digest"`
	ExactQuestionFingerprint string                          `json:"exact_question_fingerprint"`
	CollectionTags           []string                        `json:"collection_tags"`
	MatchedTerms             []string                        `json:"matched_terms"`
	MinMatchTerms            uint32                          `json:"min_match_terms"`
	Mode                     KnowledgeShortcutModeV1         `json:"mode"`
}

// MemoryReadEvidenceV1 closes one dynamic context.provide/v1 Memory result
// over the exact immutable snapshot selected for this model Attempt. The
// evaluated time is evidence for deterministic TTL filtering and is never
// projected into the model prompt.
type MemoryReadEvidenceV1 struct {
	BindingIndex        uint32                        `json:"binding_index"`
	ConfigRef           string                        `json:"config_ref"`
	AuthorityCeilingRef string                        `json:"authority_ceiling_ref"`
	RequestDigest       string                        `json:"request_digest"`
	Scope               moduleapi.MemoryQueryScopeV1  `json:"scope"`
	Snapshot            moduleapi.MemorySnapshotRefV1 `json:"snapshot"`
	EvaluatedAtUnixMS   uint64                        `json:"evaluated_at_unix_ms"`
	SelectedEntries     []moduleapi.MemoryCandidateV1 `json:"selected_entries"`
	OutputDigest        string                        `json:"output_digest"`
}

// ActionResultReservationV1 reserves room for the largest complete untrusted
// Action result envelope that may be appended to the next model request. The
// estimate uses the same frozen estimator as the surrounding compilation.
type ActionResultReservationV1 struct {
	MaxEnvelopeBytes uint64 `json:"max_envelope_bytes"`
	EstimatedTokens  uint64 `json:"estimated_tokens"`
}

// CompositeChildResultEvidenceV1 records the exact Root-plan identity and
// token-budget treatment of one successful Child MODEL_RESULT. Result text is
// not copied into evidence: FinalRequestDigest binds its complete, unmodified
// envelope. OriginalBytes and RetainedBytes must therefore always be equal,
// and Truncated must always be false. TerminalRevision is the Child's semantic
// terminal Run revision; lease acquire/release Frame revisions are deliberately
// excluded so an exact retry cannot change the Root compilation.
type CompositeChildResultEvidenceV1 struct {
	RunID                string                `json:"run_id"`
	ChildManifestDigest  string                `json:"child_manifest_digest"`
	MemberSnapshotDigest string                `json:"member_snapshot_digest"`
	ResultRef            string                `json:"result_ref"`
	TerminalRevision     uint64                `json:"terminal_revision"`
	Assignment           CompositeAssignmentV1 `json:"assignment"`
	AllocatedTokens      uint64                `json:"allocated_tokens"`
	EstimatedTokens      uint64                `json:"estimated_tokens"`
	OriginalBytes        uint64                `json:"original_bytes"`
	RetainedBytes        uint64                `json:"retained_bytes"`
	Truncated            bool                  `json:"truncated"`
}

// CompositeReviewVerdictEvidenceV1 binds the approved Reviewer MODEL_RESULT
// used by a root merge without copying the verdict into a second fact.
type CompositeReviewVerdictEvidenceV1 struct {
	ReviewerRunID          string           `json:"reviewer_run_id"`
	ReviewerManifestDigest string           `json:"reviewer_manifest_digest"`
	MemberSnapshotDigest   string           `json:"member_snapshot_digest"`
	AttemptID              string           `json:"attempt_id"`
	LogicalStepID          string           `json:"logical_step_id"`
	ResultRef              string           `json:"result_ref"`
	TerminalRunRevision    uint64           `json:"terminal_run_revision"`
	TerminalFrameRevision  uint64           `json:"terminal_frame_revision"`
	SpecialistResultDigest string           `json:"specialist_result_digest"`
	Decision               ReviewDecisionV1 `json:"decision"`
}

// CompositeContextEvidenceV1 keeps Parent/Child context projection inside the
// sole context-compilation/v1 record. CHILD carries only its immutable focus
// assignment. ROOT carries the fixed half-budget and one result record in the
// already-frozen plan order.
type CompositeContextEvidenceV1 struct {
	Role                    CompositeRunRoleV1                `json:"role"`
	Assignment              *CompositeAssignmentV1            `json:"assignment,omitempty"`
	ChildResultBudgetTokens uint64                            `json:"child_result_budget_tokens,omitempty"`
	ChildResults            []CompositeChildResultEvidenceV1  `json:"child_results,omitempty"`
	SpecialistResultDigest  string                            `json:"specialist_result_digest,omitempty"`
	ReviewVerdict           *CompositeReviewVerdictEvidenceV1 `json:"review_verdict,omitempty"`
}

// WorkspaceTransferEvidenceV1 is the minimal immutable edge retained by the
// context compilation when a W5 collaboration crosses a Workspace boundary.
// EnvelopeRef is the traversable WORKSPACE_TRANSFER_ENVELOPE ContentRecord
// reference; EnvelopeDigest independently binds the strict envelope protocol
// bytes. PayloadRef remains the sole transferred payload fact.
type WorkspaceTransferEvidenceV1 struct {
	Direction      WorkspaceTransferDirectionV1   `json:"direction"`
	PayloadKind    WorkspaceTransferPayloadKindV1 `json:"payload_kind"`
	EnvelopeRef    string                         `json:"envelope_ref"`
	EnvelopeDigest string                         `json:"envelope_digest"`
	PayloadRef     string                         `json:"payload_ref"`
	RootRunID      string                         `json:"root_run_id"`
	ChildRunID     string                         `json:"child_run_id"`
	SlotID         string                         `json:"slot_id"`
}

// CompositeChildResultBudgetTokensV1 fixes the aggregate Root merge slice at
// floor(parent input budget / 2) in the frozen estimator's token units.
func CompositeChildResultBudgetTokensV1(inputBudget uint64) (uint64, error) {
	if inputBudget == 0 || inputBudget > maximumJSONSafeIntegerV1 {
		return 0, fmt.Errorf(
			"corecontract: Composite parent input budget must fit a positive JSON safe integer",
		)
	}
	return inputBudget / 2, nil
}

// CompositeChildResultAllocationsTokensV1 allocates the complete frozen pool
// across the already-canonical Child order. Each Child first receives
// floor(pool*weight/10000); rounding remainder is then assigned one token at a
// time in that same weight-descending, SlotID-stable order. This batch-only
// API prevents callers from silently losing or independently redistributing
// the remainder.
func CompositeChildResultAllocationsTokensV1(
	childResultBudget uint64,
	assignments []CompositeAssignmentV1,
) ([]uint64, error) {
	if childResultBudget > maximumJSONSafeIntegerV1 ||
		len(assignments) < CompositeMinChildrenV1 ||
		len(assignments) > CompositeMaxChildrenV1 {
		return nil, fmt.Errorf(
			"corecontract: invalid Composite Child-result allocation input",
		)
	}
	allocations := make([]uint64, len(assignments))
	seenSlots := make(map[string]struct{}, len(assignments))
	var allocated uint64
	var totalWeight uint64
	for index, assignment := range assignments {
		if err := assignment.Validate(); err != nil {
			return nil, fmt.Errorf(
				"corecontract: Composite Child-result assignment %d: %w",
				index,
				err,
			)
		}
		if index != 0 {
			previous := assignments[index-1]
			if assignment.WeightBasisPoints > previous.WeightBasisPoints ||
				assignment.WeightBasisPoints == previous.WeightBasisPoints &&
					assignment.SlotID <= previous.SlotID {
				return nil, fmt.Errorf(
					"corecontract: Composite Child-result assignments must follow descending weight then slot order",
				)
			}
		}
		if _, duplicate := seenSlots[assignment.SlotID]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: duplicate Composite Child-result slot %q",
				assignment.SlotID,
			)
		}
		seenSlots[assignment.SlotID] = struct{}{}
		weight := uint64(assignment.WeightBasisPoints)
		quotient := childResultBudget / CompositeWeightBasisPointsV1
		remainder := childResultBudget % CompositeWeightBasisPointsV1
		allocation := quotient*weight +
			(remainder*weight)/CompositeWeightBasisPointsV1
		allocations[index] = allocation
		allocated += allocation
		totalWeight += weight
	}
	if totalWeight != CompositeWeightBasisPointsV1 || allocated > childResultBudget {
		return nil, fmt.Errorf(
			"corecontract: Composite Child-result weights or allocations do not close",
		)
	}
	remainder := childResultBudget - allocated
	if remainder > uint64(len(allocations)) {
		return nil, fmt.Errorf(
			"corecontract: Composite Child-result rounding remainder is invalid",
		)
	}
	for index := uint64(0); index < remainder; index++ {
		allocations[index]++
	}
	return allocations, nil
}

func (reservation ActionResultReservationV1) Validate() error {
	if reservation.MaxEnvelopeBytes == 0 ||
		reservation.MaxEnvelopeBytes > math.MaxInt64 ||
		reservation.EstimatedTokens == 0 ||
		reservation.EstimatedTokens > math.MaxInt64 {
		return fmt.Errorf(
			"corecontract: Action result reservation must use positive signed integers",
		)
	}
	return nil
}

// ContextCompilationV1 is the sole canonical S2.1 compilation record. It is
// absent below the 85% watermark unless a dynamic context read, an Action
// reservation, or a Composite projection occurred, and embeds all optional
// Knowledge, Memory, Composite, optional Workspace-transfer, summary and Drop
// evidence.
type ContextCompilationV1 struct {
	SchemaVersion           string                         `json:"schema_version"`
	WorkspaceScope          WorkspaceRef                   `json:"workspace_scope"`
	ContextPolicy           PolicyRef                      `json:"context_policy"`
	EstimatorVersion        string                         `json:"estimator_version"`
	SummaryAlgorithmVersion string                         `json:"summary_algorithm_version"`
	InputBudgetTokens       uint64                         `json:"input_budget_tokens"`
	RestoreWatermarkTokens  uint64                         `json:"restore_watermark_tokens"`
	OriginalEstimateTokens  uint64                         `json:"original_estimate_tokens"`
	ActionResultReservation *ActionResultReservationV1     `json:"action_result_reservation,omitempty"`
	KnowledgeRetrievals     []KnowledgeRetrievalEvidenceV1 `json:"knowledge_retrievals,omitempty"`
	KnowledgeReuses         []KnowledgeReuseEvidenceV1     `json:"knowledge_reuses,omitempty"`
	KnowledgeShortcuts      []KnowledgeShortcutEvidenceV1  `json:"knowledge_shortcuts,omitempty"`
	MemoryReads             []MemoryReadEvidenceV1         `json:"memory_reads,omitempty"`
	Composite               *CompositeContextEvidenceV1    `json:"composite,omitempty"`
	WorkspaceTransfers      []WorkspaceTransferEvidenceV1  `json:"workspace_transfers,omitempty"`
	Summary                 *ContextCompilationSummaryV1   `json:"summary,omitempty"`
	Drops                   []ContextCompilationDropV1     `json:"drops"`
	FinalEstimateTokens     uint64                         `json:"final_estimate_tokens"`
	StopReason              ContextCompilationStopReasonV1 `json:"stop_reason"`
	FinalRequestDigest      string                         `json:"final_request_digest"`
}

func NewContextCompilationV1(
	input ContextCompilationV1,
) (ContextCompilationV1, []byte, error) {
	if input.SchemaVersion != ContextCompilationSchemaVersionV1 {
		return ContextCompilationV1{}, nil, fmt.Errorf(
			"corecontract: context compilation schema version must be %q",
			ContextCompilationSchemaVersionV1,
		)
	}
	if err := input.WorkspaceScope.Validate(); err != nil {
		return ContextCompilationV1{}, nil, err
	}
	if err := input.ContextPolicy.Validate(); err != nil {
		return ContextCompilationV1{}, nil, err
	}
	if input.EstimatorVersion !=
		ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1 {
		return ContextCompilationV1{}, nil, fmt.Errorf(
			"corecontract: unsupported compilation estimator %q",
			input.EstimatorVersion,
		)
	}
	if input.SummaryAlgorithmVersion != ContextSummaryHeadTailExtractiveV1 {
		return ContextCompilationV1{}, nil, fmt.Errorf(
			"corecontract: unsupported summary algorithm %q",
			input.SummaryAlgorithmVersion,
		)
	}
	expectedWatermark, err := ContextRestoreWatermarkTokensV1(
		input.InputBudgetTokens,
	)
	if err != nil {
		return ContextCompilationV1{}, nil, err
	}
	if input.RestoreWatermarkTokens != expectedWatermark {
		return ContextCompilationV1{}, nil, fmt.Errorf(
			"corecontract: context compilation restore watermark mismatch",
		)
	}
	composite, err := freezeCompositeContextEvidenceV1(
		input.Composite,
		input.InputBudgetTokens,
	)
	if err != nil {
		return ContextCompilationV1{}, nil, err
	}
	workspaceTransfers, err := freezeWorkspaceTransferEvidenceV1(
		input.WorkspaceTransfers,
		composite,
	)
	if err != nil {
		return ContextCompilationV1{}, nil, err
	}
	if input.OriginalEstimateTokens < input.RestoreWatermarkTokens {
		validBelowWatermark := false
		switch {
		case composite != nil:
			validBelowWatermark = input.StopReason ==
				ContextCompilationCompositeBelowWatermark
		case len(input.KnowledgeRetrievals) != 0 ||
			len(input.KnowledgeReuses) != 0 || len(input.MemoryReads) != 0:
			validBelowWatermark = input.StopReason ==
				ContextCompilationRetrievalBelowWatermark
		case len(input.KnowledgeShortcuts) != 0:
			validBelowWatermark = input.StopReason ==
				ContextCompilationKnowledgeShortcutBelowWatermark
		case input.ActionResultReservation != nil:
			validBelowWatermark = input.StopReason ==
				ContextCompilationActionResultReserved
		}
		if !validBelowWatermark {
			return ContextCompilationV1{}, nil, fmt.Errorf(
				"corecontract: compilation below the restore watermark requires matching dynamic context, Action reservation, or Composite evidence",
			)
		}
	}
	if input.OriginalEstimateTokens == 0 ||
		input.OriginalEstimateTokens > math.MaxInt64 ||
		input.FinalEstimateTokens == 0 ||
		input.FinalEstimateTokens > math.MaxInt64 {
		return ContextCompilationV1{}, nil, fmt.Errorf(
			"corecontract: compilation estimates must be positive signed integers",
		)
	}
	if err := input.StopReason.Validate(); err != nil {
		return ContextCompilationV1{}, nil, err
	}
	if !moduleapi.ValidSHA256(input.FinalRequestDigest) {
		return ContextCompilationV1{}, nil, fmt.Errorf(
			"corecontract: invalid final model request digest",
		)
	}

	frozen := input
	frozen.Composite = composite
	frozen.WorkspaceTransfers = workspaceTransfers
	if input.ActionResultReservation != nil {
		if err := input.ActionResultReservation.Validate(); err != nil {
			return ContextCompilationV1{}, nil, err
		}
		reservation := *input.ActionResultReservation
		frozen.ActionResultReservation = &reservation
	}
	knowledgeRetrievals, err := freezeKnowledgeRetrievalEvidence(
		input.KnowledgeRetrievals,
	)
	if err != nil {
		return ContextCompilationV1{}, nil, err
	}
	frozen.KnowledgeRetrievals = knowledgeRetrievals
	knowledgeShortcuts, err := freezeKnowledgeShortcutEvidence(
		input.KnowledgeShortcuts,
	)
	if err != nil {
		return ContextCompilationV1{}, nil, err
	}
	memoryReads, err := freezeMemoryReadEvidence(input.MemoryReads)
	if err != nil {
		return ContextCompilationV1{}, nil, err
	}
	knowledgeReuses, err := freezeKnowledgeReuseEvidence(
		input.KnowledgeReuses,
		memoryReads,
	)
	if err != nil {
		return ContextCompilationV1{}, nil, err
	}
	if err := validateDynamicBindingIndexUnion(
		knowledgeRetrievals,
		knowledgeReuses,
		knowledgeShortcuts,
		memoryReads,
	); err != nil {
		return ContextCompilationV1{}, nil, err
	}
	frozen.KnowledgeReuses = knowledgeReuses
	frozen.KnowledgeShortcuts = knowledgeShortcuts
	frozen.MemoryReads = memoryReads
	currentEstimate := input.OriginalEstimateTokens
	if input.Summary != nil {
		summary, err := freezeContextCompilationSummary(
			*input.Summary,
			currentEstimate,
		)
		if err != nil {
			return ContextCompilationV1{}, nil, err
		}
		frozen.Summary = &summary
		currentEstimate = summary.AfterEstimateTokens
	}
	frozen.Drops = append([]ContextCompilationDropV1{}, input.Drops...)
	if len(frozen.Drops) > moduleapi.MaxManifestEntries {
		return ContextCompilationV1{}, nil, fmt.Errorf(
			"corecontract: compilation may contain at most %d Drops",
			moduleapi.MaxManifestEntries,
		)
	}
	seenDrops := make(map[string]struct{}, len(frozen.Drops))
	for index, drop := range frozen.Drops {
		if err := drop.UnitKind.Validate(); err != nil {
			return ContextCompilationV1{}, nil, err
		}
		if !moduleapi.ValidSHA256(drop.UnitDigest) {
			return ContextCompilationV1{}, nil, fmt.Errorf(
				"corecontract: Drop %d has invalid unit digest",
				index,
			)
		}
		if _, duplicate := seenDrops[drop.UnitDigest]; duplicate {
			return ContextCompilationV1{}, nil, fmt.Errorf(
				"corecontract: duplicate dropped unit %s",
				drop.UnitDigest,
			)
		}
		seenDrops[drop.UnitDigest] = struct{}{}
		if drop.BeforeEstimateTokens != currentEstimate ||
			drop.AfterEstimateTokens >= drop.BeforeEstimateTokens {
			return ContextCompilationV1{}, nil, fmt.Errorf(
				"corecontract: Drop %d estimate chain is invalid",
				index,
			)
		}
		if index+1 != len(frozen.Drops) &&
			drop.AfterEstimateTokens <= input.RestoreWatermarkTokens {
			return ContextCompilationV1{}, nil, fmt.Errorf(
				"corecontract: Drop %d continued after reaching the restore watermark",
				index,
			)
		}
		currentEstimate = drop.AfterEstimateTokens
	}
	if input.FinalEstimateTokens != currentEstimate {
		return ContextCompilationV1{}, nil, fmt.Errorf(
			"corecontract: final context estimate does not close the transformation chain",
		)
	}
	if err := validateContextCompilationOutcome(frozen); err != nil {
		return ContextCompilationV1{}, nil, err
	}
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return ContextCompilationV1{}, nil, err
	}
	return frozen, canonical, nil
}

func freezeCompositeContextEvidenceV1(
	input *CompositeContextEvidenceV1,
	inputBudget uint64,
) (*CompositeContextEvidenceV1, error) {
	if input == nil {
		return nil, nil
	}
	frozen := *input
	switch input.Role {
	case CompositeRunRoleChildV1:
		if input.Assignment == nil || input.ChildResultBudgetTokens != 0 ||
			len(input.ChildResults) != 0 || input.SpecialistResultDigest != "" ||
			input.ReviewVerdict != nil {
			return nil, fmt.Errorf(
				"corecontract: Composite CHILD evidence must contain only one assignment",
			)
		}
		assignment := *input.Assignment
		if err := assignment.Validate(); err != nil {
			return nil, err
		}
		frozen.Assignment = &assignment
		frozen.ChildResults = nil
	case CompositeRunRoleRootV1, CompositeRunRoleReviewerV1:
		if input.Assignment != nil ||
			len(input.ChildResults) < CompositeMinChildrenV1 ||
			len(input.ChildResults) > CompositeMaxChildrenV1 {
			return nil, fmt.Errorf(
				"corecontract: Composite ROOT evidence requires between %d and %d Child results and no assignment",
				CompositeMinChildrenV1,
				CompositeMaxChildrenV1,
			)
		}
		expectedBudget, err := CompositeChildResultBudgetTokensV1(inputBudget)
		if err != nil {
			return nil, err
		}
		if expectedBudget == 0 ||
			input.ChildResultBudgetTokens != expectedBudget {
			return nil, fmt.Errorf(
				"corecontract: Composite ROOT Child-result budget must equal half the input budget",
			)
		}
		results := make(
			[]CompositeChildResultEvidenceV1,
			len(input.ChildResults),
		)
		assignments := make(
			[]CompositeAssignmentV1,
			len(input.ChildResults),
		)
		for index, result := range input.ChildResults {
			assignments[index] = result.Assignment
		}
		expectedAllocations, err :=
			CompositeChildResultAllocationsTokensV1(
				expectedBudget,
				assignments,
			)
		if err != nil {
			return nil, err
		}
		seenSlots := make(map[string]struct{}, len(results))
		seenRuns := make(map[string]struct{}, len(results))
		seenManifests := make(map[string]struct{}, len(results))
		seenMembers := make(map[string]struct{}, len(results))
		for index, result := range input.ChildResults {
			if !validOpaque(result.RunID, maxOpaqueIDBytes) ||
				!moduleapi.ValidSHA256(result.ChildManifestDigest) ||
				!moduleapi.ValidSHA256(result.MemberSnapshotDigest) ||
				!moduleapi.ValidSHA256(result.ResultRef) ||
				result.TerminalRevision == 0 ||
				result.TerminalRevision > maximumJSONSafeIntegerV1 {
				return nil, fmt.Errorf(
					"corecontract: invalid Composite Child-result evidence %d identity",
					index,
				)
			}
			if err := result.Assignment.Validate(); err != nil {
				return nil, fmt.Errorf(
					"corecontract: Composite Child-result evidence %d assignment: %w",
					index,
					err,
				)
			}
			if index != 0 {
				previous := input.ChildResults[index-1].Assignment
				current := result.Assignment
				if current.WeightBasisPoints > previous.WeightBasisPoints ||
					current.WeightBasisPoints == previous.WeightBasisPoints &&
						current.SlotID <= previous.SlotID {
					return nil, fmt.Errorf(
						"corecontract: Composite Child-result evidence must follow descending weight then slot order",
					)
				}
			}
			if _, duplicate := seenSlots[result.Assignment.SlotID]; duplicate {
				return nil, fmt.Errorf(
					"corecontract: duplicate Composite Child-result slot %q",
					result.Assignment.SlotID,
				)
			}
			if _, duplicate := seenRuns[result.RunID]; duplicate {
				return nil, fmt.Errorf(
					"corecontract: duplicate Composite Child-result Run %q",
					result.RunID,
				)
			}
			if _, duplicate := seenManifests[result.ChildManifestDigest]; duplicate {
				return nil, fmt.Errorf(
					"corecontract: duplicate Composite Child Manifest %s",
					result.ChildManifestDigest,
				)
			}
			if _, duplicate := seenMembers[result.MemberSnapshotDigest]; duplicate {
				return nil, fmt.Errorf(
					"corecontract: duplicate Composite Child member snapshot %s",
					result.MemberSnapshotDigest,
				)
			}
			seenSlots[result.Assignment.SlotID] = struct{}{}
			seenRuns[result.RunID] = struct{}{}
			seenManifests[result.ChildManifestDigest] = struct{}{}
			seenMembers[result.MemberSnapshotDigest] = struct{}{}
			if result.AllocatedTokens != expectedAllocations[index] {
				return nil, fmt.Errorf(
					"corecontract: Composite Child-result evidence %d allocation mismatch",
					index,
				)
			}
			if result.OriginalBytes == 0 ||
				result.OriginalBytes > uint64(moduleapi.MaxTextBytes) ||
				result.RetainedBytes != result.OriginalBytes ||
				result.Truncated || result.EstimatedTokens <= result.OriginalBytes ||
				result.EstimatedTokens > result.AllocatedTokens {
				return nil, fmt.Errorf(
					"corecontract: Composite Child-result evidence %d must retain one complete untruncated envelope",
					index,
				)
			}
			results[index] = result
		}
		frozen.Assignment = nil
		frozen.ChildResults = results
		if input.Role == CompositeRunRoleReviewerV1 {
			if !moduleapi.ValidSHA256(input.SpecialistResultDigest) ||
				input.ReviewVerdict != nil {
				return nil, fmt.Errorf(
					"corecontract: Composite REVIEWER evidence requires one Specialist result digest and no verdict",
				)
			}
			frozen.SpecialistResultDigest = input.SpecialistResultDigest
			frozen.ReviewVerdict = nil
		} else if input.ReviewVerdict == nil {
			if input.SpecialistResultDigest != "" {
				return nil, fmt.Errorf(
					"corecontract: Reviewer-disabled ROOT evidence cannot carry a Specialist result digest",
				)
			}
			frozen.SpecialistResultDigest = ""
			frozen.ReviewVerdict = nil
		} else {
			if !moduleapi.ValidSHA256(input.SpecialistResultDigest) {
				return nil, fmt.Errorf(
					"corecontract: Reviewer-enabled ROOT evidence requires the Specialist result digest",
				)
			}
			verdict := *input.ReviewVerdict
			if err := validateCompositeReviewVerdictEvidenceV1(
				verdict,
				input.SpecialistResultDigest,
			); err != nil {
				return nil, err
			}
			frozen.SpecialistResultDigest = input.SpecialistResultDigest
			frozen.ReviewVerdict = &verdict
		}
	default:
		return nil, fmt.Errorf(
			"corecontract: unsupported Composite context role %q",
			input.Role,
		)
	}
	return &frozen, nil
}

func freezeWorkspaceTransferEvidenceV1(
	input []WorkspaceTransferEvidenceV1,
	composite *CompositeContextEvidenceV1,
) ([]WorkspaceTransferEvidenceV1, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if composite == nil || len(input) > CompositeMaxChildrenV1 {
		return nil, fmt.Errorf(
			"corecontract: Workspace transfer evidence requires bounded Composite evidence",
		)
	}
	frozen := append([]WorkspaceTransferEvidenceV1(nil), input...)
	seenEnvelopes := make(map[string]struct{}, len(frozen))
	seenEnvelopeDigests := make(map[string]struct{}, len(frozen))
	seenChildren := make(map[string]struct{}, len(frozen))
	seenSlots := make(map[string]struct{}, len(frozen))
	rootRunID := frozen[0].RootRunID
	for index, evidence := range frozen {
		if !validWorkspaceTransferDirectionV1(evidence.Direction) ||
			!validWorkspaceTransferPayloadKindV1(evidence.PayloadKind) ||
			!workspaceTransferDirectionAllowsPayloadV1(
				evidence.Direction,
				evidence.PayloadKind,
			) ||
			!moduleapi.ValidSHA256(evidence.EnvelopeRef) ||
			!moduleapi.ValidSHA256(evidence.EnvelopeDigest) ||
			!moduleapi.ValidSHA256(evidence.PayloadRef) ||
			!validOpaque(evidence.RootRunID, maxOpaqueIDBytes) ||
			!validOpaque(evidence.ChildRunID, maxOpaqueIDBytes) ||
			evidence.RootRunID == evidence.ChildRunID ||
			!validOpaque(evidence.SlotID, maxOpaqueIDBytes) ||
			evidence.SlotID == CompositeReviewerParentSlotIDV1 ||
			evidence.RootRunID != rootRunID {
			return nil, fmt.Errorf(
				"corecontract: invalid Workspace transfer evidence %d identity",
				index,
			)
		}
		if _, duplicate := seenEnvelopes[evidence.EnvelopeRef]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: duplicate Workspace transfer envelope ref",
			)
		}
		if _, duplicate := seenEnvelopeDigests[evidence.EnvelopeDigest]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: duplicate Workspace transfer envelope digest",
			)
		}
		if _, duplicate := seenChildren[evidence.ChildRunID]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: duplicate Workspace transfer Child Run",
			)
		}
		if _, duplicate := seenSlots[evidence.SlotID]; duplicate {
			return nil, fmt.Errorf(
				"corecontract: duplicate Workspace transfer slot",
			)
		}
		seenEnvelopes[evidence.EnvelopeRef] = struct{}{}
		seenEnvelopeDigests[evidence.EnvelopeDigest] = struct{}{}
		seenChildren[evidence.ChildRunID] = struct{}{}
		seenSlots[evidence.SlotID] = struct{}{}
	}

	switch frozen[0].Direction {
	case WorkspaceTransferDirectionRequestV1:
		if len(frozen) != 1 ||
			composite.Role != CompositeRunRoleChildV1 ||
			composite.Assignment == nil ||
			frozen[0].SlotID != composite.Assignment.SlotID {
			return nil, fmt.Errorf(
				"corecontract: Workspace transfer REQUEST evidence must bind the Composite Child assignment",
			)
		}
	case WorkspaceTransferDirectionResultV1:
		if composite.Role != CompositeRunRoleRootV1 &&
			composite.Role != CompositeRunRoleReviewerV1 {
			return nil, fmt.Errorf(
				"corecontract: Workspace transfer RESULT evidence requires a Composite Root or Reviewer",
			)
		}
		// RESULT evidence is a strict subsequence of the already-frozen Child
		// result order. Same-Workspace results are intentionally absent.
		childIndex := 0
		for index, evidence := range frozen {
			for childIndex < len(composite.ChildResults) {
				result := composite.ChildResults[childIndex]
				childIndex++
				if result.RunID != evidence.ChildRunID ||
					result.Assignment.SlotID != evidence.SlotID {
					continue
				}
				if result.ResultRef != evidence.PayloadRef {
					return nil, fmt.Errorf(
						"corecontract: Workspace transfer RESULT evidence %d payload differs from the Child result",
						index,
					)
				}
				goto matchedResult
			}
			return nil, fmt.Errorf(
				"corecontract: Workspace transfer RESULT evidence does not follow the frozen Child-result order",
			)
		matchedResult:
		}
	default:
		return nil, fmt.Errorf(
			"corecontract: unsupported Workspace transfer evidence direction",
		)
	}
	for index := 1; index < len(frozen); index++ {
		if frozen[index].Direction != frozen[0].Direction {
			return nil, fmt.Errorf(
				"corecontract: one compilation cannot mix Workspace transfer directions",
			)
		}
	}
	return frozen, nil
}

func validateCompositeReviewVerdictEvidenceV1(
	evidence CompositeReviewVerdictEvidenceV1,
	specialistResultDigest string,
) error {
	if !validOpaque(evidence.ReviewerRunID, maxOpaqueIDBytes) ||
		!moduleapi.ValidSHA256(evidence.ReviewerManifestDigest) ||
		!moduleapi.ValidSHA256(evidence.MemberSnapshotDigest) ||
		!validOpaque(evidence.AttemptID, maxOpaqueIDBytes) ||
		evidence.LogicalStepID != CompositeReviewLogicalStepIDV1 ||
		!moduleapi.ValidSHA256(evidence.ResultRef) ||
		evidence.TerminalRunRevision == 0 ||
		evidence.TerminalRunRevision > maximumJSONSafeIntegerV1 ||
		evidence.TerminalFrameRevision == 0 ||
		evidence.TerminalFrameRevision > maximumJSONSafeIntegerV1 ||
		evidence.SpecialistResultDigest != specialistResultDigest ||
		evidence.Decision != ReviewDecisionApproveV1 {
		return fmt.Errorf(
			"corecontract: invalid Composite Reviewer verdict evidence",
		)
	}
	return nil
}

func freezeKnowledgeRetrievalEvidence(
	input []KnowledgeRetrievalEvidenceV1,
) ([]KnowledgeRetrievalEvidenceV1, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"corecontract: compilation may contain at most %d Knowledge retrievals",
			moduleapi.MaxManifestEntries,
		)
	}
	frozen := make([]KnowledgeRetrievalEvidenceV1, len(input))
	for index, evidence := range input {
		if index != 0 && evidence.BindingIndex <= input[index-1].BindingIndex {
			return nil, fmt.Errorf(
				"corecontract: Knowledge retrieval BindingIndex values must be strictly increasing",
			)
		}
		if !moduleapi.ValidSHA256(evidence.ConfigRef) {
			return nil, fmt.Errorf(
				"corecontract: Knowledge retrieval %d has invalid config ref",
				index,
			)
		}
		if !moduleapi.ValidSHA256(evidence.AuthorityCeilingRef) {
			return nil, fmt.Errorf(
				"corecontract: Knowledge retrieval %d has invalid authority ceiling ref",
				index,
			)
		}
		if !moduleapi.ValidSHA256(evidence.RequestDigest) {
			return nil, fmt.Errorf(
				"corecontract: Knowledge retrieval %d has invalid request digest",
				index,
			)
		}
		if err := evidence.Scope.Validate(); err != nil {
			return nil, fmt.Errorf(
				"corecontract: Knowledge retrieval %d scope: %w",
				index,
				err,
			)
		}
		if err := evidence.Source.Validate(); err != nil {
			return nil, fmt.Errorf(
				"corecontract: Knowledge retrieval %d source: %w",
				index,
				err,
			)
		}
		output, _, outputDigest, err := moduleapi.NewKnowledgeContextOutputV1(
			moduleapi.KnowledgeContextOutputV1{
				SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
				RequestDigest: evidence.RequestDigest,
				Source:        evidence.Source,
				Hits:          evidence.Hits,
			},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"corecontract: Knowledge retrieval %d output: %w",
				index,
				err,
			)
		}
		if !moduleapi.ValidSHA256(evidence.OutputDigest) ||
			evidence.OutputDigest != outputDigest {
			return nil, fmt.Errorf(
				"corecontract: Knowledge retrieval %d output digest mismatch",
				index,
			)
		}
		for hitIndex, hit := range output.Hits {
			allowed := false
			for _, rule := range hit.VisibleTo {
				if moduleapi.KnowledgeScopeAllowsV1(rule, evidence.Scope) {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, fmt.Errorf(
					"corecontract: Knowledge retrieval %d hit %d is not visible to the exact scope",
					index,
					hitIndex,
				)
			}
		}
		frozen[index] = evidence
		frozen[index].Scope = outputKnowledgeScopeCopy(evidence.Scope)
		frozen[index].Source = output.Source
		frozen[index].Hits = output.Hits
		provenance, err := freezeKnowledgeRetrievalProvenance(
			evidence.Provenance,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"corecontract: Knowledge retrieval %d provenance: %w",
				index,
				err,
			)
		}
		frozen[index].Provenance = provenance
	}
	return frozen, nil
}

func freezeKnowledgeRetrievalProvenance(
	input *KnowledgeRetrievalProvenanceV1,
) (*KnowledgeRetrievalProvenanceV1, error) {
	if input == nil {
		return nil, nil
	}
	if err := input.Provider.Validate(); err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	if !validOpaque(input.RoutingAlgorithmVersion, maxOpaqueIDBytes) {
		return nil, fmt.Errorf("routing algorithm version is invalid")
	}
	if !moduleapi.ValidSHA256(input.DecisionSetDigest) {
		return nil, fmt.Errorf("decision-set digest is invalid")
	}
	if input.RetrievedAtUnixMS == 0 ||
		input.RetrievedAtUnixMS > moduleapi.MaxMemorySafeIntegerV1 {
		return nil, fmt.Errorf(
			"retrieved time must be between 1 and %d",
			moduleapi.MaxMemorySafeIntegerV1,
		)
	}
	frozen := *input
	return &frozen, nil
}

func freezeKnowledgeReuseEvidence(
	input []KnowledgeReuseEvidenceV1,
	memoryReads []MemoryReadEvidenceV1,
) ([]KnowledgeReuseEvidenceV1, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"corecontract: compilation may contain at most %d Knowledge reuses",
			moduleapi.MaxManifestEntries,
		)
	}
	memoryByBinding := make(
		map[uint32]MemoryReadEvidenceV1,
		len(memoryReads),
	)
	for _, evidence := range memoryReads {
		memoryByBinding[evidence.BindingIndex] = evidence
	}
	frozen := make([]KnowledgeReuseEvidenceV1, len(input))
	for index, evidence := range input {
		bindingIndex := evidence.FreshRetrieval.BindingIndex
		if index != 0 && bindingIndex <=
			input[index-1].FreshRetrieval.BindingIndex {
			return nil, fmt.Errorf(
				"corecontract: Knowledge reuse BindingIndex values must be strictly increasing",
			)
		}
		if !validOpaque(evidence.SourceConversationID, maxOpaqueIDBytes) ||
			evidence.SourceTurnIndex == 0 ||
			evidence.SourceTurnIndex > maximumJSONSafeIntegerV1 ||
			!validOpaque(evidence.SourceRunID, maxOpaqueIDBytes) ||
			!validOpaque(evidence.SourceAttemptID, maxOpaqueIDBytes) ||
			!moduleapi.ValidSHA256(evidence.SourceCompilationRef) {
			return nil, fmt.Errorf(
				"corecontract: Knowledge reuse %d has invalid source identity",
				index,
			)
		}
		fresh, err := freezeKnowledgeRetrievalEvidence(
			[]KnowledgeRetrievalEvidenceV1{evidence.FreshRetrieval},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"corecontract: Knowledge reuse %d fresh retrieval: %w",
				index,
				err,
			)
		}
		if len(fresh[0].Hits) == 0 || fresh[0].Provenance == nil {
			return nil, fmt.Errorf(
				"corecontract: Knowledge reuse %d requires a non-empty fresh retrieval with provenance",
				index,
			)
		}
		if evidence.MemoryBindingIndex == bindingIndex {
			return nil, fmt.Errorf(
				"corecontract: Knowledge reuse %d cannot use its Knowledge Binding as Memory proof",
				index,
			)
		}
		memory, found := memoryByBinding[evidence.MemoryBindingIndex]
		if !found {
			return nil, fmt.Errorf(
				"corecontract: Knowledge reuse %d references an absent Memory read",
				index,
			)
		}
		if err := evidence.CategoryCounter.Validate(); err != nil ||
			evidence.CategoryCounter.Kind !=
				moduleapi.MemoryEntryCategoryCount {
			return nil, fmt.Errorf(
				"corecontract: Knowledge reuse %d category counter is invalid",
				index,
			)
		}
		if err := evidence.RepeatedTermCounter.Validate(); err != nil ||
			evidence.RepeatedTermCounter.Kind !=
				moduleapi.MemoryEntryRepeatedTermCount {
			return nil, fmt.Errorf(
				"corecontract: Knowledge reuse %d repeated-term counter is invalid",
				index,
			)
		}
		if evidence.CategoryCounter.EntryDigest ==
			evidence.RepeatedTermCounter.EntryDigest {
			return nil, fmt.Errorf(
				"corecontract: Knowledge reuse %d repeats one Memory counter digest",
				index,
			)
		}
		if !sameKnowledgeAndMemoryScope(
			fresh[0].Scope,
			memory.Scope,
		) || memory.EvaluatedAtUnixMS <
			fresh[0].Provenance.RetrievedAtUnixMS {
			return nil, fmt.Errorf(
				"corecontract: Knowledge reuse %d Memory proof scope or time differs",
				index,
			)
		}
		frozen[index] = evidence
		frozen[index].FreshRetrieval = fresh[0]
	}
	return frozen, nil
}

func sameKnowledgeAndMemoryScope(
	knowledge moduleapi.KnowledgeQueryScopeV1,
	memory moduleapi.MemoryQueryScopeV1,
) bool {
	return knowledge.TenantID == memory.TenantID &&
		knowledge.Workspace.ID == memory.Workspace.ID &&
		knowledge.Workspace.Version == memory.Workspace.Version &&
		knowledge.Workspace.Digest == memory.Workspace.Digest &&
		knowledge.Agent.ID == memory.Agent.ID &&
		knowledge.Agent.Version == memory.Agent.Version &&
		knowledge.Agent.Digest == memory.Agent.Digest &&
		knowledge.TaskInputRef == memory.TaskInputRef
}

func freezeKnowledgeShortcutEvidence(
	input []KnowledgeShortcutEvidenceV1,
) ([]KnowledgeShortcutEvidenceV1, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"corecontract: compilation may contain at most %d Knowledge shortcuts",
			moduleapi.MaxManifestEntries,
		)
	}
	frozen := make([]KnowledgeShortcutEvidenceV1, len(input))
	for index, evidence := range input {
		if index != 0 && evidence.BindingIndex <= input[index-1].BindingIndex {
			return nil, fmt.Errorf(
				"corecontract: Knowledge shortcut BindingIndex values must be strictly increasing",
			)
		}
		if evidence.Mode != KnowledgeShortcutNotSelectedV1 {
			return nil, fmt.Errorf(
				"corecontract: Knowledge shortcut %d mode must be NOT_SELECTED",
				index,
			)
		}
		if !moduleapi.ValidSHA256(evidence.ConfigRef) ||
			!moduleapi.ValidSHA256(evidence.AuthorityCeilingRef) ||
			!moduleapi.ValidSHA256(evidence.DecisionSetDigest) ||
			!moduleapi.ValidSHA256(evidence.ExactQuestionFingerprint) {
			return nil, fmt.Errorf(
				"corecontract: Knowledge shortcut %d has an invalid closure digest",
				index,
			)
		}
		if err := evidence.Scope.Validate(); err != nil {
			return nil, fmt.Errorf(
				"corecontract: Knowledge shortcut %d scope: %w",
				index,
				err,
			)
		}
		if err := evidence.Source.Validate(); err != nil {
			return nil, fmt.Errorf(
				"corecontract: Knowledge shortcut %d source: %w",
				index,
				err,
			)
		}
		tags, err := freezeKnowledgeShortcutStrings(
			"collection tag",
			evidence.CollectionTags,
			moduleapi.MaxKnowledgeCollectionTagsV1,
			false,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"corecontract: Knowledge shortcut %d: %w",
				index,
				err,
			)
		}
		terms, err := freezeKnowledgeShortcutStrings(
			"matched term",
			evidence.MatchedTerms,
			moduleapi.MaxKnowledgeMatchTermsV1,
			true,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"corecontract: Knowledge shortcut %d: %w",
				index,
				err,
			)
		}
		if evidence.MinMatchTerms == 0 ||
			evidence.MinMatchTerms > moduleapi.MaxKnowledgeMatchTermsV1 ||
			uint32(len(terms)) >= evidence.MinMatchTerms {
			return nil, fmt.Errorf(
				"corecontract: Knowledge shortcut %d match threshold does not prove NOT_SELECTED",
				index,
			)
		}
		frozen[index] = evidence
		frozen[index].Scope = outputKnowledgeScopeCopy(evidence.Scope)
		frozen[index].CollectionTags = tags
		frozen[index].MatchedTerms = terms
	}
	return frozen, nil
}

func freezeKnowledgeShortcutStrings(
	name string,
	input []string,
	maximum int,
	allowEmpty bool,
) ([]string, error) {
	minimum := 1
	if allowEmpty {
		minimum = 0
	}
	if len(input) > maximum || len(input) < minimum {
		return nil, fmt.Errorf(
			"Knowledge shortcut %ss must contain between %d and %d values",
			name,
			minimum,
			maximum,
		)
	}
	values := append([]string{}, input...)
	for index, value := range values {
		if value == "" || len(value) > moduleapi.MaxKnowledgeRoutingValueBytesV1 ||
			!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
			value != moduleapi.CanonicalText(value) ||
			value != moduleapi.CanonicalText(strings.ToLower(value)) {
			return nil, fmt.Errorf(
				"Knowledge shortcut %s %d must be bounded lowercase Unicode NFC",
				name,
				index,
			)
		}
		if index != 0 && value <= values[index-1] {
			return nil, fmt.Errorf(
				"Knowledge shortcut %ss must be strictly sorted and unique",
				name,
			)
		}
	}
	return values, nil
}

func freezeMemoryReadEvidence(
	input []MemoryReadEvidenceV1,
) ([]MemoryReadEvidenceV1, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"corecontract: compilation may contain at most %d Memory reads",
			moduleapi.MaxManifestEntries,
		)
	}
	frozen := make([]MemoryReadEvidenceV1, len(input))
	for index, evidence := range input {
		if index != 0 && evidence.BindingIndex <= input[index-1].BindingIndex {
			return nil, fmt.Errorf(
				"corecontract: Memory read BindingIndex values must be strictly increasing",
			)
		}
		if !moduleapi.ValidSHA256(evidence.ConfigRef) {
			return nil, fmt.Errorf(
				"corecontract: Memory read %d has invalid config ref",
				index,
			)
		}
		if !moduleapi.ValidSHA256(evidence.AuthorityCeilingRef) {
			return nil, fmt.Errorf(
				"corecontract: Memory read %d has invalid authority ceiling ref",
				index,
			)
		}
		if !moduleapi.ValidSHA256(evidence.RequestDigest) {
			return nil, fmt.Errorf(
				"corecontract: Memory read %d has invalid request digest",
				index,
			)
		}
		if err := evidence.Scope.Validate(); err != nil {
			return nil, fmt.Errorf(
				"corecontract: Memory read %d scope: %w",
				index,
				err,
			)
		}
		if err := evidence.Snapshot.Validate(); err != nil {
			return nil, fmt.Errorf(
				"corecontract: Memory read %d snapshot: %w",
				index,
				err,
			)
		}
		if evidence.Snapshot.TenantID != evidence.Scope.TenantID ||
			evidence.Snapshot.AgentID != evidence.Scope.Agent.ID {
			return nil, fmt.Errorf(
				"corecontract: Memory read %d snapshot owner differs from scope",
				index,
			)
		}
		if evidence.EvaluatedAtUnixMS == 0 ||
			evidence.EvaluatedAtUnixMS > moduleapi.MaxMemorySafeIntegerV1 {
			return nil, fmt.Errorf(
				"corecontract: Memory read %d has invalid evaluated time",
				index,
			)
		}
		if len(evidence.SelectedEntries) > moduleapi.MaxMemoryItemsV1 {
			return nil, fmt.Errorf(
				"corecontract: Memory read %d exceeds selected entry limit",
				index,
			)
		}
		selected := make([]moduleapi.MemoryCandidateV1, len(evidence.SelectedEntries))
		digests := make([]string, len(evidence.SelectedEntries))
		seen := make(map[string]struct{}, len(evidence.SelectedEntries))
		for selectedIndex, entry := range evidence.SelectedEntries {
			if err := entry.Validate(); err != nil {
				return nil, fmt.Errorf(
					"corecontract: Memory read %d selected entry %d: %w",
					index,
					selectedIndex,
					err,
				)
			}
			if _, duplicate := seen[entry.EntryDigest]; duplicate {
				return nil, fmt.Errorf(
					"corecontract: Memory read %d repeats selected entry",
					index,
				)
			}
			seen[entry.EntryDigest] = struct{}{}
			selected[selectedIndex] = entry
			digests[selectedIndex] = entry.EntryDigest
		}
		_, _, outputDigest, err := moduleapi.NewMemoryContextOutputV1(
			moduleapi.MemoryContextOutputV1{
				SchemaVersion:        moduleapi.MemoryContextOutputSchemaV1,
				RequestDigest:        evidence.RequestDigest,
				Snapshot:             evidence.Snapshot,
				SelectedEntryDigests: digests,
			},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"corecontract: Memory read %d output: %w",
				index,
				err,
			)
		}
		if !moduleapi.ValidSHA256(evidence.OutputDigest) ||
			evidence.OutputDigest != outputDigest {
			return nil, fmt.Errorf(
				"corecontract: Memory read %d output digest mismatch",
				index,
			)
		}
		frozen[index] = evidence
		frozen[index].SelectedEntries = selected
	}
	return frozen, nil
}

func validateDynamicBindingIndexUnion(
	knowledge []KnowledgeRetrievalEvidenceV1,
	reuses []KnowledgeReuseEvidenceV1,
	shortcuts []KnowledgeShortcutEvidenceV1,
	memory []MemoryReadEvidenceV1,
) error {
	seen := make(
		map[uint32]struct{},
		len(knowledge)+len(reuses)+len(shortcuts)+len(memory),
	)
	for _, evidence := range knowledge {
		seen[evidence.BindingIndex] = struct{}{}
	}
	for _, evidence := range reuses {
		bindingIndex := evidence.FreshRetrieval.BindingIndex
		if _, duplicate := seen[bindingIndex]; duplicate {
			return fmt.Errorf(
				"corecontract: dynamic context BindingIndex %d has multiple protocol results",
				bindingIndex,
			)
		}
		seen[bindingIndex] = struct{}{}
	}
	for _, evidence := range shortcuts {
		if _, duplicate := seen[evidence.BindingIndex]; duplicate {
			return fmt.Errorf(
				"corecontract: dynamic context BindingIndex %d has multiple protocol results",
				evidence.BindingIndex,
			)
		}
		seen[evidence.BindingIndex] = struct{}{}
	}
	for _, evidence := range memory {
		if _, duplicate := seen[evidence.BindingIndex]; duplicate {
			return fmt.Errorf(
				"corecontract: dynamic context BindingIndex %d has multiple protocol results",
				evidence.BindingIndex,
			)
		}
		seen[evidence.BindingIndex] = struct{}{}
	}
	return nil
}

func outputKnowledgeScopeCopy(
	input moduleapi.KnowledgeQueryScopeV1,
) moduleapi.KnowledgeQueryScopeV1 {
	// V1 Scope contains only immutable scalar/object-ref values. Keeping the
	// copy explicit makes this function the single place to deepen if a later
	// wire version adds a slice or map.
	return input
}

func RestoreContextCompilationV1(
	canonical []byte,
) (ContextCompilationV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return ContextCompilationV1{}, err
	}
	var decoded ContextCompilationV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return ContextCompilationV1{}, err
	}
	restored, rebuilt, err := NewContextCompilationV1(decoded)
	if err != nil {
		return ContextCompilationV1{}, err
	}
	if !bytes.Equal(rebuilt, canonical) {
		return ContextCompilationV1{}, fmt.Errorf(
			"corecontract: context compilation is not frozen canonically",
		)
	}
	return restored, nil
}

func freezeContextCompilationSummary(
	input ContextCompilationSummaryV1,
	expectedBefore uint64,
) (ContextCompilationSummaryV1, error) {
	if len(input.SourceTurnDigests) == 0 ||
		len(input.SourceTurnDigests) > moduleapi.MaxManifestEntries {
		return ContextCompilationSummaryV1{}, fmt.Errorf(
			"corecontract: summary must cover a bounded non-empty turn range",
		)
	}
	seen := make(map[string]struct{}, len(input.SourceTurnDigests))
	for index, digest := range input.SourceTurnDigests {
		if !moduleapi.ValidSHA256(digest) {
			return ContextCompilationSummaryV1{}, fmt.Errorf(
				"corecontract: summary turn %d has invalid digest",
				index,
			)
		}
		if _, duplicate := seen[digest]; duplicate {
			return ContextCompilationSummaryV1{}, fmt.Errorf(
				"corecontract: summary repeats turn digest %s",
				digest,
			)
		}
		seen[digest] = struct{}{}
	}
	if err := validateChatText("context summary", input.Text); err != nil {
		return ContextCompilationSummaryV1{}, err
	}
	if len(input.Text) > contextSummaryMaxTextBytesV1 {
		return ContextCompilationSummaryV1{}, fmt.Errorf(
			"corecontract: context summary exceeds the bounded V1 size",
		)
	}
	if input.BeforeEstimateTokens != expectedBefore ||
		input.AfterEstimateTokens >= input.BeforeEstimateTokens {
		return ContextCompilationSummaryV1{}, fmt.Errorf(
			"corecontract: summary estimate chain is invalid",
		)
	}
	frozen := input
	frozen.SourceTurnDigests = append([]string{}, input.SourceTurnDigests...)
	return frozen, nil
}

func validateContextCompilationOutcome(value ContextCompilationV1) error {
	hasSummary := value.Summary != nil
	hasDrops := len(value.Drops) != 0
	if value.OriginalEstimateTokens >= value.InputBudgetTokens {
		if hasSummary || !hasDrops ||
			value.StopReason != ContextCompilationDropToWatermark {
			return fmt.Errorf(
				"corecontract: full-budget input must use direct Drop without summary",
			)
		}
	} else if hasDrops {
		return fmt.Errorf(
			"corecontract: under-budget input cannot enter the S2.1 Drop path",
		)
	}
	switch value.StopReason {
	case ContextCompilationSummaryToWatermark:
		if !hasSummary || hasDrops ||
			value.FinalEstimateTokens > value.RestoreWatermarkTokens {
			return fmt.Errorf("corecontract: SUMMARY_TO_WATERMARK outcome mismatch")
		}
	case ContextCompilationSummaryStrictlyReduced:
		if !hasSummary || hasDrops ||
			value.FinalEstimateTokens >= value.InputBudgetTokens ||
			value.FinalEstimateTokens <= value.RestoreWatermarkTokens {
			return fmt.Errorf("corecontract: SUMMARY_STRICTLY_REDUCED outcome mismatch")
		}
	case ContextCompilationNoEligibleSummary:
		if hasSummary || hasDrops ||
			value.FinalEstimateTokens != value.OriginalEstimateTokens ||
			value.FinalEstimateTokens >= value.InputBudgetTokens {
			return fmt.Errorf("corecontract: NO_ELIGIBLE_SUMMARY outcome mismatch")
		}
	case ContextCompilationDropToWatermark:
		if hasSummary || !hasDrops ||
			value.FinalEstimateTokens > value.RestoreWatermarkTokens {
			return fmt.Errorf("corecontract: DROP_TO_WATERMARK outcome mismatch")
		}
	case ContextCompilationRetrievalBelowWatermark:
		if len(value.KnowledgeRetrievals) == 0 &&
			len(value.KnowledgeReuses) == 0 && len(value.MemoryReads) == 0 ||
			value.Composite != nil ||
			hasSummary || hasDrops ||
			value.OriginalEstimateTokens >= value.RestoreWatermarkTokens ||
			value.FinalEstimateTokens != value.OriginalEstimateTokens {
			return fmt.Errorf(
				"corecontract: RETRIEVAL_EVIDENCE_BELOW_WATERMARK outcome mismatch",
			)
		}
	case ContextCompilationKnowledgeShortcutBelowWatermark:
		if len(value.KnowledgeShortcuts) == 0 ||
			len(value.KnowledgeRetrievals) != 0 ||
			len(value.KnowledgeReuses) != 0 || len(value.MemoryReads) != 0 ||
			value.Composite != nil || hasSummary || hasDrops ||
			value.OriginalEstimateTokens >= value.RestoreWatermarkTokens ||
			value.FinalEstimateTokens != value.OriginalEstimateTokens {
			return fmt.Errorf(
				"corecontract: KNOWLEDGE_SHORTCUT_BELOW_WATERMARK outcome mismatch",
			)
		}
	case ContextCompilationActionResultReserved:
		if value.ActionResultReservation == nil ||
			len(value.KnowledgeRetrievals) != 0 ||
			len(value.KnowledgeReuses) != 0 ||
			len(value.KnowledgeShortcuts) != 0 || len(value.MemoryReads) != 0 ||
			value.Composite != nil ||
			hasSummary || hasDrops ||
			value.OriginalEstimateTokens >= value.RestoreWatermarkTokens ||
			value.FinalEstimateTokens != value.OriginalEstimateTokens {
			return fmt.Errorf(
				"corecontract: ACTION_RESULT_RESERVED_BELOW_WATERMARK outcome mismatch",
			)
		}
	case ContextCompilationCompositeBelowWatermark:
		if value.Composite == nil || hasSummary || hasDrops ||
			value.OriginalEstimateTokens >= value.RestoreWatermarkTokens ||
			value.FinalEstimateTokens != value.OriginalEstimateTokens {
			return fmt.Errorf(
				"corecontract: COMPOSITE_EVIDENCE_BELOW_WATERMARK outcome mismatch",
			)
		}
	}
	return nil
}
