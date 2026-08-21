package knowledgecore

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// ReuseCurrentFactsV1 is the exact, already-frozen current-side material used
// by the K3 overlay. Authority resolution and candidate lookup stay with the
// caller; the digest is only an equality closure and cannot grant authority.
type ReuseCurrentFactsV1 struct {
	ConversationID          string
	Request                 moduleapi.KnowledgeContextRequestV1
	Provider                moduleapi.ActivatedModuleRef
	ConfigDigest            string
	AuthorityDigest         string
	RoutingAlgorithmVersion string
	DecisionSetDigest       string
	Decision                BindingDecision
}

// ReuseCandidateV1 is one frozen candidate selected by the caller. V1 accepts
// no candidate collection, so a failed evaluation cannot fall through to an
// older result. IsLatestExactQuestionCandidate is a persisted query fact that
// must be rechecked transactionally by the Store before creating PENDING.
type ReuseCandidateV1 struct {
	ConversationID                 string
	SourceAttemptID                string
	SourceCompilationDigest        string
	Request                        moduleapi.KnowledgeContextRequestV1
	Output                         moduleapi.KnowledgeContextOutputV1
	Provider                       moduleapi.ActivatedModuleRef
	ConfigDigest                   string
	AuthorityDigest                string
	BindingIndex                   uint32
	RoutingAlgorithmVersion        string
	DecisionSetDigest              string
	RetrievedAtUnixMS              uint64
	LookbackTurns                  uint32
	IsLatestExactQuestionCandidate bool
	FreshRetrieval                 bool
	SourceAttemptSucceeded         bool
}

// ReuseEvaluationInputV1 contains only caller-frozen facts. EvaluateReuseV1
// performs no clock, Store, History, Memory, Provider, or authority I/O.
type ReuseEvaluationInputV1 struct {
	Policy              moduleapi.KnowledgeReusePolicyV1
	EvaluatedAtUnixMS   uint64
	Current             ReuseCurrentFactsV1
	Candidate           ReuseCandidateV1
	CategoryCounter     moduleapi.MemoryCandidateV1
	RepeatedTermCounter moduleapi.MemoryCandidateV1
}

// ReuseEvaluationV1 carries the exact frozen evidence only when REUSE wins.
// A FRESH_RAG result intentionally carries no candidate that could be used as
// an implicit fallback or accidentally persisted as reuse evidence.
type ReuseEvaluationV1 struct {
	Decision            DecisionKind
	EvaluatedAtUnixMS   uint64
	Candidate           *ReuseCandidateV1
	CategoryCounter     *moduleapi.MemoryCandidateV1
	RepeatedTermCounter *moduleapi.MemoryCandidateV1
}

// EvaluateReuseV1 applies the exact-question reuse overlay to one and only one
// latest candidate. Any stale or non-matching valid fact safely returns
// FRESH_RAG. Structurally invalid current/candidate material fails closed with
// ErrInvalidInput. Counter proof failures are ordinary cache misses because
// counters are optional gates, never knowledge or authority.
func EvaluateReuseV1(input ReuseEvaluationInputV1) (ReuseEvaluationV1, error) {
	if err := input.Policy.Validate(); err != nil {
		return ReuseEvaluationV1{}, invalid("reuse policy", err)
	}
	if input.EvaluatedAtUnixMS == 0 ||
		input.EvaluatedAtUnixMS > moduleapi.MaxMemorySafeIntegerV1 {
		return ReuseEvaluationV1{}, invalidf(
			"evaluated_at_unix_ms must be between 1 and %d",
			moduleapi.MaxMemorySafeIntegerV1,
		)
	}

	current, err := freezeReuseCurrentFactsV1(input.Current)
	if err != nil {
		return ReuseEvaluationV1{}, err
	}
	candidate, err := freezeReuseCandidateV1(input.Candidate)
	if err != nil {
		return ReuseEvaluationV1{}, err
	}
	fresh := func() (ReuseEvaluationV1, error) {
		return ReuseEvaluationV1{
			Decision:          DecisionFreshRAG,
			EvaluatedAtUnixMS: input.EvaluatedAtUnixMS,
		}, nil
	}
	if candidate.RetrievedAtUnixMS > input.EvaluatedAtUnixMS {
		return ReuseEvaluationV1{}, invalidf(
			"candidate retrieval time is after evaluated_at_unix_ms",
		)
	}

	// Source state gates are misses rather than malformed structures.
	if !candidate.IsLatestExactQuestionCandidate ||
		!candidate.FreshRetrieval ||
		!candidate.SourceAttemptSucceeded ||
		len(candidate.Output.Hits) == 0 {
		return fresh()
	}
	if candidate.LookbackTurns > input.Policy.MaxLookbackTurns ||
		candidate.LookbackTurns > moduleapi.MaxKnowledgeReuseLookbackTurnsV1 {
		return fresh()
	}
	ageMS := input.EvaluatedAtUnixMS - candidate.RetrievedAtUnixMS
	if ageMS >= input.Policy.ReuseTTLSeconds*1000 {
		return fresh()
	}

	// Full request equality deliberately includes Tenant, complete Workspace
	// and Agent refs, TaskInputRef, Source revision, exact NFC query, and both
	// request limits. No trimming, case-folding, or semantic matching occurs.
	if current.ConversationID != candidate.ConversationID ||
		current.Request != candidate.Request ||
		current.Provider != candidate.Provider ||
		current.ConfigDigest != candidate.ConfigDigest ||
		current.AuthorityDigest != candidate.AuthorityDigest ||
		current.Decision.BindingIndex != candidate.BindingIndex ||
		current.RoutingAlgorithmVersion != candidate.RoutingAlgorithmVersion ||
		current.DecisionSetDigest != candidate.DecisionSetDigest {
		return fresh()
	}

	// The original fresh result must still close both the source request and
	// the exact current request. These are intentionally separate calls even
	// though V1 requires byte-equivalent request values.
	if moduleapi.ValidateKnowledgeContextOutputForRequestV1(
		candidate.Request,
		candidate.Output,
	) != nil || moduleapi.ValidateKnowledgeContextOutputForRequestV1(
		current.Request,
		candidate.Output,
	) != nil {
		return fresh()
	}

	if !validReuseCounter(
		input.CategoryCounter,
		moduleapi.MemoryEntryCategoryCount,
		current.Decision.CollectionTags,
		input.Policy.MinCategoryCount,
	) || !validReuseCounter(
		input.RepeatedTermCounter,
		moduleapi.MemoryEntryRepeatedTermCount,
		current.Decision.MatchedTerms,
		input.Policy.MinRepeatedTermCount,
	) || input.CategoryCounter.EntryDigest == input.RepeatedTermCounter.EntryDigest {
		return fresh()
	}

	category := input.CategoryCounter
	repeated := input.RepeatedTermCounter
	return ReuseEvaluationV1{
		Decision:            DecisionReuse,
		EvaluatedAtUnixMS:   input.EvaluatedAtUnixMS,
		Candidate:           &candidate,
		CategoryCounter:     &category,
		RepeatedTermCounter: &repeated,
	}, nil
}

func freezeReuseCurrentFactsV1(input ReuseCurrentFactsV1) (ReuseCurrentFactsV1, error) {
	if err := validateReuseOpaque("current conversation ID", input.ConversationID); err != nil {
		return ReuseCurrentFactsV1{}, err
	}
	request, _, _, err := moduleapi.NewKnowledgeContextRequestV1(input.Request)
	if err != nil {
		return ReuseCurrentFactsV1{}, invalid("current request", err)
	}
	if err := input.Provider.Validate(); err != nil {
		return ReuseCurrentFactsV1{}, invalid("current provider", err)
	}
	if !moduleapi.ValidSHA256(input.ConfigDigest) ||
		!moduleapi.ValidSHA256(input.AuthorityDigest) ||
		!moduleapi.ValidSHA256(input.DecisionSetDigest) {
		return ReuseCurrentFactsV1{}, invalidf(
			"current Config, Authority, and decision-set digests must be lowercase SHA-256",
		)
	}
	if input.RoutingAlgorithmVersion != RoutingAlgorithmVersionV1 {
		return ReuseCurrentFactsV1{}, invalidf(
			"current routing algorithm must be %q",
			RoutingAlgorithmVersionV1,
		)
	}
	decision, err := freezeReuseBindingDecision(input.Decision, request.QueryText)
	if err != nil {
		return ReuseCurrentFactsV1{}, err
	}
	input.Request = request
	input.Decision = decision
	return input, nil
}

func freezeReuseCandidateV1(input ReuseCandidateV1) (ReuseCandidateV1, error) {
	if err := validateReuseOpaque(
		"candidate conversation ID",
		input.ConversationID,
	); err != nil {
		return ReuseCandidateV1{}, err
	}
	if err := validateReuseOpaque(
		"candidate source attempt ID",
		input.SourceAttemptID,
	); err != nil {
		return ReuseCandidateV1{}, err
	}
	request, _, _, err := moduleapi.NewKnowledgeContextRequestV1(input.Request)
	if err != nil {
		return ReuseCandidateV1{}, invalid("candidate request", err)
	}
	output, _, _, err := moduleapi.NewKnowledgeContextOutputV1(input.Output)
	if err != nil {
		return ReuseCandidateV1{}, invalid("candidate output", err)
	}
	if err := input.Provider.Validate(); err != nil {
		return ReuseCandidateV1{}, invalid("candidate provider", err)
	}
	if !moduleapi.ValidSHA256(input.SourceCompilationDigest) ||
		!moduleapi.ValidSHA256(input.ConfigDigest) ||
		!moduleapi.ValidSHA256(input.AuthorityDigest) ||
		!moduleapi.ValidSHA256(input.DecisionSetDigest) {
		return ReuseCandidateV1{}, invalidf(
			"candidate Compilation, Config, Authority, and decision-set digests must be lowercase SHA-256",
		)
	}
	if err := validateReuseOpaque(
		"candidate routing algorithm version",
		input.RoutingAlgorithmVersion,
	); err != nil {
		return ReuseCandidateV1{}, err
	}
	if input.RetrievedAtUnixMS == 0 ||
		input.RetrievedAtUnixMS > moduleapi.MaxMemorySafeIntegerV1 {
		return ReuseCandidateV1{}, invalidf(
			"candidate retrieved_at_unix_ms must be between 1 and %d",
			moduleapi.MaxMemorySafeIntegerV1,
		)
	}
	if input.LookbackTurns == 0 {
		return ReuseCandidateV1{}, invalidf(
			"candidate lookback_turns must identify an earlier turn",
		)
	}
	input.Request = request
	input.Output = output
	return input, nil
}

func freezeReuseBindingDecision(
	input BindingDecision,
	queryText string,
) (BindingDecision, error) {
	if input.Decision != DecisionFreshRAG {
		return BindingDecision{}, invalidf(
			"current K1 decision must be FRESH_RAG before reuse overlay",
		)
	}
	if input.MinMatchTerms == 0 {
		return BindingDecision{}, invalidf(
			"current routed decision min_match_terms must be positive",
		)
	}
	if !moduleapi.ValidSHA256(input.ExactQuestionFingerprint) ||
		input.ExactQuestionFingerprint != moduleapi.Digest(
			ExactQuestionFingerprintDomainV1,
			[]byte(queryText),
		) {
		return BindingDecision{}, invalidf(
			"current decision exact-question fingerprint does not close query_text",
		)
	}
	tags, err := freezeReuseRoutingValues(
		"current decision collection tag",
		input.CollectionTags,
		moduleapi.MaxKnowledgeCollectionTagsV1,
		false,
	)
	if err != nil {
		return BindingDecision{}, err
	}
	terms, err := freezeReuseRoutingValues(
		"current decision matched term",
		input.MatchedTerms,
		moduleapi.MaxKnowledgeMatchTermsV1,
		true,
	)
	if err != nil {
		return BindingDecision{}, err
	}
	input.CollectionTags = tags
	input.MatchedTerms = terms
	return input, nil
}

func freezeReuseRoutingValues(
	name string,
	input []string,
	maximum int,
	allowEmpty bool,
) ([]string, error) {
	if (!allowEmpty && len(input) == 0) || len(input) > maximum {
		return nil, invalidf("%s count is outside the bounded range", name)
	}
	values := append([]string(nil), input...)
	if !sort.StringsAreSorted(values) {
		return nil, invalidf("%ss must be sorted", name)
	}
	for index, value := range values {
		if err := validateReuseOpaque(name, value); err != nil {
			return nil, err
		}
		if value != moduleapi.CanonicalText(strings.ToLower(value)) {
			return nil, invalidf("%s %d must use lowercase Unicode NFC", name, index)
		}
		if index > 0 && values[index-1] == value {
			return nil, invalidf("%ss must be unique", name)
		}
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

func validReuseCounter(
	candidate moduleapi.MemoryCandidateV1,
	kind moduleapi.MemoryEntryKindV1,
	allowedKeys []string,
	minimum uint64,
) bool {
	if candidate.Validate() != nil || candidate.Kind != kind ||
		candidate.Count < minimum {
		return false
	}
	index := sort.SearchStrings(allowedKeys, candidate.Key)
	return index < len(allowedKeys) && allowedKeys[index] == candidate.Key
}

func validateReuseOpaque(name, value string) error {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return invalidf(
			"%s must be canonical UTF-8 containing between 1 and %d bytes",
			name,
			moduleapi.MaxOpaqueIDBytes,
		)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return invalidf("%s contains an unsupported control character", name)
		}
	}
	return nil
}
