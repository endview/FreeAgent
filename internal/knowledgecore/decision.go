// Package knowledgecore contains deterministic, side-effect-free knowledge
// selection rules. It owns no Store, provider, authority or runtime I/O.
package knowledgecore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	DecisionSetSchemaVersionV1 = "knowledge-routing-decision-set/v1"

	DecisionSetDigestDomainV1        = "freeagent.knowledge-routing-decision-set/v1"
	ExactQuestionFingerprintDomainV1 = "freeagent.knowledge-exact-question/v1"
	// RoutingAlgorithmVersionV1 is persisted with reuse provenance. Any
	// semantic change to classification requires a new value.
	RoutingAlgorithmVersionV1 = "freeagent.knowledge-routing/v1"

	// A decision contains only bounded projections of already bounded Binding
	// configuration. This limit covers every allowed Binding plus the task-size
	// ceiling and keeps canonicalization explicitly resource bounded.
	MaxDecisionSetBytesV1 = moduleapi.MaxManifestEntries*moduleapi.MaxConfigBytes + moduleapi.MaxTextBytes
)

var ErrInvalidInput = errors.New("knowledgecore: invalid input")

// DecisionKind is shared by the K1 routing result and the K3 reuse overlay.
// Decide itself continues to emit only FRESH_RAG or NOT_SELECTED; REUSE can
// be emitted only by EvaluateReuseV1 after its stricter evidence checks.
type DecisionKind string

const (
	DecisionFreshRAG    DecisionKind = "FRESH_RAG"
	DecisionNotSelected DecisionKind = "NOT_SELECTED"
	DecisionReuse       DecisionKind = "REUSE"
)

// BindingInput binds the PortPlan index to its already consumer-owned
// Knowledge configuration. Decide independently restores the normalized
// configuration so caller-owned pointers and slices cannot leak into output.
type BindingInput struct {
	BindingIndex uint32
	Config       moduleapi.KnowledgeContextBindingV1
}

// BindingDecision is one stable outcome for one input Binding.
type BindingDecision struct {
	BindingIndex             uint32       `json:"binding_index"`
	Decision                 DecisionKind `json:"decision"`
	MatchedTerms             []string     `json:"matched_terms"`
	CollectionTags           []string     `json:"collection_tags"`
	MinMatchTerms            uint32       `json:"min_match_terms"`
	ExactQuestionFingerprint string       `json:"exact_question_fingerprint"`
}

// DecisionSet is the ordered, canonicalizable K1 result. Decisions always
// preserve the strictly increasing BindingIndex order supplied to Decide.
type DecisionSet struct {
	SchemaVersion string            `json:"schema_version"`
	Decisions     []BindingDecision `json:"decisions"`
}

type evaluatedBinding struct {
	index          uint32
	routed         bool
	qualified      bool
	matchedTerms   []string
	collectionTags []string
	minMatchTerms  uint32
}

// Decide deterministically selects candidate knowledge Bindings without I/O.
//
// Legacy Bindings with no routing policy always receive FRESH_RAG. If at least
// one routed Binding reaches its threshold, only qualifying routed Bindings
// receive FRESH_RAG. If none reaches threshold, every routed Binding receives
// FRESH_RAG as the explicit low-confidence fallback.
func Decide(
	taskText string,
	bindings []BindingInput,
) (DecisionSet, []byte, string, error) {
	if err := validateTaskText(taskText); err != nil {
		return DecisionSet{}, nil, "", err
	}
	if len(bindings) > moduleapi.MaxManifestEntries {
		return DecisionSet{}, nil, "", invalidf(
			"binding count exceeds %d",
			moduleapi.MaxManifestEntries,
		)
	}

	foldedTask := foldForMatch(taskText)
	fingerprint := moduleapi.Digest(
		ExactQuestionFingerprintDomainV1,
		[]byte(taskText),
	)
	evaluated := make([]evaluatedBinding, len(bindings))
	anyRoutedQualified := false
	for index, input := range bindings {
		if index > 0 && input.BindingIndex <= bindings[index-1].BindingIndex {
			return DecisionSet{}, nil, "", invalidf(
				"binding indices must be strictly increasing and unique",
			)
		}
		frozen, _, err := moduleapi.NewKnowledgeContextBindingV1(input.Config)
		if err != nil {
			return DecisionSet{}, nil, "", invalid(
				fmt.Sprintf("binding %d config", input.BindingIndex),
				err,
			)
		}
		result := evaluatedBinding{index: input.BindingIndex}
		if frozen.Routing != nil {
			result.routed = true
			result.collectionTags = append(
				[]string(nil),
				frozen.Routing.CollectionTags...,
			)
			result.minMatchTerms = frozen.Routing.MinMatchTerms
			result.matchedTerms = make([]string, 0, len(frozen.Routing.MatchTerms))
			for _, term := range frozen.Routing.MatchTerms {
				if strings.Contains(foldedTask, term) {
					result.matchedTerms = append(result.matchedTerms, term)
				}
			}
			result.qualified = uint32(len(result.matchedTerms)) >= result.minMatchTerms
			anyRoutedQualified = anyRoutedQualified || result.qualified
		}
		evaluated[index] = result
	}

	decisions := make([]BindingDecision, len(evaluated))
	for index, item := range evaluated {
		kind := DecisionFreshRAG
		if item.routed && anyRoutedQualified && !item.qualified {
			kind = DecisionNotSelected
		}
		matched := append([]string(nil), item.matchedTerms...)
		if matched == nil {
			matched = []string{}
		}
		tags := append([]string(nil), item.collectionTags...)
		if tags == nil {
			tags = []string{}
		}
		decisions[index] = BindingDecision{
			BindingIndex:             item.index,
			Decision:                 kind,
			MatchedTerms:             matched,
			CollectionTags:           tags,
			MinMatchTerms:            item.minMatchTerms,
			ExactQuestionFingerprint: fingerprint,
		}
	}
	set := DecisionSet{
		SchemaVersion: DecisionSetSchemaVersionV1,
		Decisions:     decisions,
	}
	canonical, err := canonicalDecisionSet(set)
	if err != nil {
		return DecisionSet{}, nil, "", err
	}
	digest := moduleapi.Digest(DecisionSetDigestDomainV1, canonical)
	return cloneDecisionSet(set), append([]byte(nil), canonical...), digest, nil
}

func validateTaskText(taskText string) error {
	if taskText == "" ||
		len(taskText) > moduleapi.MaxTextBytes ||
		!utf8.ValidString(taskText) ||
		taskText != moduleapi.CanonicalText(taskText) {
		return invalidf(
			"task text must be non-empty Unicode NFC and at most %d bytes",
			moduleapi.MaxTextBytes,
		)
	}
	return nil
}

func foldForMatch(value string) string {
	return moduleapi.CanonicalText(strings.ToLower(value))
}

func canonicalDecisionSet(input DecisionSet) ([]byte, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("knowledgecore: marshal decision set: %w", err)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaxDecisionSetBytesV1,
			MaxDepth: 64,
			MaxNodes: MaxDecisionSetBytesV1,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("knowledgecore: canonicalize decision set: %w", err)
	}
	return canonical, nil
}

func cloneDecisionSet(input DecisionSet) DecisionSet {
	cloned := input
	cloned.Decisions = make([]BindingDecision, len(input.Decisions))
	for index, decision := range input.Decisions {
		cloned.Decisions[index] = decision
		cloned.Decisions[index].MatchedTerms = append(
			[]string(nil),
			decision.MatchedTerms...,
		)
		cloned.Decisions[index].CollectionTags = append(
			[]string(nil),
			decision.CollectionTags...,
		)
		if cloned.Decisions[index].MatchedTerms == nil {
			cloned.Decisions[index].MatchedTerms = []string{}
		}
		if cloned.Decisions[index].CollectionTags == nil {
			cloned.Decisions[index].CollectionTags = []string{}
		}
	}
	if cloned.Decisions == nil {
		cloned.Decisions = []BindingDecision{}
	}
	return cloned
}

func invalid(name string, cause error) error {
	return fmt.Errorf("%w: %s: %v", ErrInvalidInput, name, cause)
}

func invalidf(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, arguments...))
}
