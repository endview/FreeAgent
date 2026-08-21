package knowledgecore

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestDecideLegacyBindingAlwaysUsesFreshRAG(t *testing.T) {
	set, canonical, digest, err := Decide("hello", []BindingInput{
		knowledgeTestBinding(3, nil),
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if len(set.Decisions) != 1 {
		t.Fatalf("decisions=%+v", set.Decisions)
	}
	decision := set.Decisions[0]
	if decision.BindingIndex != 3 || decision.Decision != DecisionFreshRAG ||
		len(decision.MatchedTerms) != 0 || len(decision.CollectionTags) != 0 ||
		decision.MinMatchTerms != 0 || !moduleapi.ValidSHA256(decision.ExactQuestionFingerprint) {
		t.Fatalf("legacy decision=%+v", decision)
	}
	if !bytes.Contains(canonical, []byte(`"decision":"FRESH_RAG"`)) ||
		digest != moduleapi.Digest(DecisionSetDigestDomainV1, canonical) {
		t.Fatalf("canonical/digest mismatch: %s %s", canonical, digest)
	}
}

func TestDecideSelectsMatchingRoutedBindingsAndKeepsLegacyFresh(t *testing.T) {
	bindings := []BindingInput{
		knowledgeTestBinding(1, nil),
		knowledgeTestBinding(4, knowledgeTestRouting(
			[]string{"backend"},
			[]string{"server", "api"},
			1,
		)),
		knowledgeTestBinding(9, knowledgeTestRouting(
			[]string{"frontend"},
			[]string{"html", "css"},
			1,
		)),
	}
	set, _, _, err := Decide("Design an API gateway", bindings)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	wantKinds := []DecisionKind{DecisionFreshRAG, DecisionFreshRAG, DecisionNotSelected}
	for index, want := range wantKinds {
		if set.Decisions[index].Decision != want {
			t.Fatalf("decision %d=%+v, want %s", index, set.Decisions[index], want)
		}
	}
	if !reflect.DeepEqual(set.Decisions[1].MatchedTerms, []string{"api"}) ||
		!reflect.DeepEqual(set.Decisions[1].CollectionTags, []string{"backend"}) ||
		len(set.Decisions[2].MatchedTerms) != 0 {
		t.Fatalf("match projection=%+v", set.Decisions)
	}
}

func TestDecideAppliesThresholdAndCanSelectMultipleAmbiguousCollections(t *testing.T) {
	bindings := []BindingInput{
		knowledgeTestBinding(2, knowledgeTestRouting(
			[]string{"architecture", "backend"},
			[]string{"server", "api"},
			2,
		)),
		knowledgeTestBinding(5, knowledgeTestRouting(
			[]string{"architecture", "network"},
			[]string{"api", "gateway"},
			1,
		)),
		knowledgeTestBinding(8, knowledgeTestRouting(
			[]string{"architecture", "security"},
			[]string{"api", "auth"},
			1,
		)),
	}
	set, _, _, err := Decide("API gateway auth design", bindings)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if set.Decisions[0].Decision != DecisionNotSelected ||
		set.Decisions[1].Decision != DecisionFreshRAG ||
		set.Decisions[2].Decision != DecisionFreshRAG {
		t.Fatalf("threshold/ambiguity decisions=%+v", set.Decisions)
	}
	if !reflect.DeepEqual(set.Decisions[1].MatchedTerms, []string{"api", "gateway"}) ||
		!reflect.DeepEqual(set.Decisions[2].MatchedTerms, []string{"api", "auth"}) {
		t.Fatalf("matched terms=%+v", set.Decisions)
	}
}

func TestDecideNoMatchUsesLowConfidenceFreshFallbackForAllRouted(t *testing.T) {
	bindings := []BindingInput{
		knowledgeTestBinding(0, knowledgeTestRouting([]string{"backend"}, []string{"api"}, 1)),
		knowledgeTestBinding(7, knowledgeTestRouting([]string{"frontend"}, []string{"css"}, 1)),
	}
	set, _, _, err := Decide("write a poem", bindings)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	for index, decision := range set.Decisions {
		if decision.Decision != DecisionFreshRAG || len(decision.MatchedTerms) != 0 {
			t.Fatalf("fallback decision %d=%+v", index, decision)
		}
	}
}

func TestDecideMatchesChineseAndCaseInsensitiveTextButFingerprintsExactText(t *testing.T) {
	bindings := []BindingInput{
		knowledgeTestBinding(1, knowledgeTestRouting(
			[]string{"data"},
			[]string{"api", "数据库"},
			1,
		)),
	}
	upper, _, _, err := Decide("检查 API 和数据库设计", bindings)
	if err != nil {
		t.Fatalf("Decide upper: %v", err)
	}
	lower, _, _, err := Decide("检查 api 和数据库设计", bindings)
	if err != nil {
		t.Fatalf("Decide lower: %v", err)
	}
	want := []string{"api", "数据库"}
	if !reflect.DeepEqual(upper.Decisions[0].MatchedTerms, want) ||
		!reflect.DeepEqual(lower.Decisions[0].MatchedTerms, want) {
		t.Fatalf("case/Chinese matches: %+v %+v", upper, lower)
	}
	if upper.Decisions[0].ExactQuestionFingerprint ==
		lower.Decisions[0].ExactQuestionFingerprint {
		t.Fatal("case-folded text entered the exact-question fingerprint")
	}
}

func TestDecideIsRestartDeterministicAndDefensivelyCopies(t *testing.T) {
	routing := knowledgeTestRouting(
		[]string{"backend", "architecture"},
		[]string{"server", "api"},
		1,
	)
	routing.Reuse = &moduleapi.KnowledgeReusePolicyV1{
		ExactQuestionOnly:    true,
		MinCategoryCount:     2,
		MinRepeatedTermCount: 2,
		MaxLookbackTurns:     16,
		ReuseTTLSeconds:      60,
	}
	bindings := []BindingInput{knowledgeTestBinding(11, routing)}
	first, firstCanonical, firstDigest, err := Decide("API design", bindings)
	if err != nil {
		t.Fatalf("first Decide: %v", err)
	}
	second, secondCanonical, secondDigest, err := Decide("API design", bindings)
	if err != nil {
		t.Fatalf("second Decide: %v", err)
	}
	if !reflect.DeepEqual(first, second) ||
		!bytes.Equal(firstCanonical, secondCanonical) ||
		firstDigest != secondDigest {
		t.Fatalf("restart decision drift:\n%+v\n%+v\n%s\n%s", first, second, firstCanonical, secondCanonical)
	}
	const wantFingerprint = "451ed1fb4f53cc1db511bc5bf8816c76251518f2f5319ddf7f240a7ce2379370"
	if first.Decisions[0].ExactQuestionFingerprint != wantFingerprint {
		t.Fatalf("fingerprint=%s, want %s", first.Decisions[0].ExactQuestionFingerprint, wantFingerprint)
	}
	wantCanonical := []byte(`{"decisions":[{"binding_index":11,"collection_tags":["architecture","backend"],"decision":"FRESH_RAG","exact_question_fingerprint":"451ed1fb4f53cc1db511bc5bf8816c76251518f2f5319ddf7f240a7ce2379370","matched_terms":["api"],"min_match_terms":1}],"schema_version":"knowledge-routing-decision-set/v1"}`)
	if !bytes.Equal(firstCanonical, wantCanonical) {
		t.Fatalf("canonical decision changed:\n got %s\nwant %s", firstCanonical, wantCanonical)
	}
	const wantDigest = "49f81aed89f3ff6ae2bb65e629242c9029ba9770cf55d04d00463230646b9f39"
	if firstDigest != wantDigest {
		t.Fatalf("decision digest=%s, want %s", firstDigest, wantDigest)
	}
	if first.Decisions[0].Decision != DecisionFreshRAG {
		t.Fatal("K1 reuse configuration changed FRESH_RAG into an unimplemented outcome")
	}

	bindings[0].Config.Routing.CollectionTags[0] = "mutated"
	bindings[0].Config.Routing.MatchTerms[0] = "mutated"
	bindings[0].Config.Routing.Reuse.MinCategoryCount = 99
	if !reflect.DeepEqual(first.Decisions[0].CollectionTags, []string{"architecture", "backend"}) ||
		!reflect.DeepEqual(first.Decisions[0].MatchedTerms, []string{"api"}) {
		t.Fatal("decision aliases caller-owned routing data")
	}
	first.Decisions[0].MatchedTerms[0] = "changed"
	if bytes.Contains(firstCanonical, []byte("changed")) {
		t.Fatal("returned decision aliases its canonical bytes")
	}
}

func TestDecideAcceptsStableEmptyInput(t *testing.T) {
	set, canonical, digest, err := Decide("pure chat", nil)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if set.SchemaVersion != DecisionSetSchemaVersionV1 || len(set.Decisions) != 0 ||
		!bytes.Equal(canonical, []byte(`{"decisions":[],"schema_version":"knowledge-routing-decision-set/v1"}`)) ||
		digest != moduleapi.Digest(DecisionSetDigestDomainV1, canonical) {
		t.Fatalf("empty decision set=%+v %s %s", set, canonical, digest)
	}
}

func TestDecideRejectsInvalidInputs(t *testing.T) {
	valid := []BindingInput{knowledgeTestBinding(1, nil)}
	tasks := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"non-NFC", "e\u0301"},
		{"invalid UTF-8", string([]byte{0xff})},
		{"oversized", strings.Repeat("x", moduleapi.MaxTextBytes+1)},
	}
	for _, test := range tasks {
		t.Run("task/"+test.name, func(t *testing.T) {
			if _, _, _, err := Decide(test.text, valid); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error=%v, want ErrInvalidInput", err)
			}
		})
	}

	tests := []struct {
		name     string
		bindings []BindingInput
	}{
		{
			"duplicate indices",
			[]BindingInput{knowledgeTestBinding(1, nil), knowledgeTestBinding(1, nil)},
		},
		{
			"decreasing indices",
			[]BindingInput{knowledgeTestBinding(2, nil), knowledgeTestBinding(1, nil)},
		},
		{
			"too many bindings",
			make([]BindingInput, moduleapi.MaxManifestEntries+1),
		},
		{
			"invalid config",
			[]BindingInput{{
				BindingIndex: 1,
				Config: moduleapi.KnowledgeContextBindingV1{
					SchemaVersion: moduleapi.KnowledgeContextBindingSchemaV1,
					Source:        knowledgeTestSource("a"),
				},
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := Decide("valid", test.bindings); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error=%v, want ErrInvalidInput", err)
			}
		})
	}
}

func knowledgeTestBinding(
	index uint32,
	routing *moduleapi.KnowledgeRoutingPolicyV1,
) BindingInput {
	return BindingInput{
		BindingIndex: index,
		Config: moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            knowledgeTestSource("a"),
			MaxHits:           8,
			MaxTotalTextBytes: 4096,
			Routing:           routing,
		},
	}
}

func knowledgeTestRouting(
	tags []string,
	terms []string,
	minimum uint32,
) *moduleapi.KnowledgeRoutingPolicyV1 {
	return &moduleapi.KnowledgeRoutingPolicyV1{
		SchemaVersion:  moduleapi.KnowledgeRoutingPolicySchemaV1,
		CollectionTags: append([]string(nil), tags...),
		MatchTerms:     append([]string(nil), terms...),
		MinMatchTerms:  minimum,
	}
}

func knowledgeTestSource(character string) moduleapi.KnowledgeSourceRefV1 {
	return moduleapi.KnowledgeSourceRefV1{
		ID:      "shared.docs",
		Version: "1.0.0",
		Digest:  strings.Repeat(character, moduleapi.SHA256HexLength),
	}
}
