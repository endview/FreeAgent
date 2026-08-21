package knowledgecore

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestEvaluateReuseV1ReturnsReuseDeterministicallyAndDefensively(t *testing.T) {
	input := validReuseEvaluationInput(t)
	first, err := EvaluateReuseV1(input)
	if err != nil {
		t.Fatalf("EvaluateReuseV1: %v", err)
	}
	second, err := EvaluateReuseV1(input)
	if err != nil {
		t.Fatalf("EvaluateReuseV1 repeat: %v", err)
	}
	if first.Decision != DecisionReuse || !reflect.DeepEqual(first, second) {
		t.Fatalf("reuse result drift:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if first.Candidate == nil || first.CategoryCounter == nil ||
		first.RepeatedTermCounter == nil ||
		first.Candidate.SourceAttemptID != "attempt-source" {
		t.Fatalf("reuse evidence=%+v", first)
	}

	input.Candidate.Output.Hits[0].Text = "caller mutation"
	input.Candidate.Output.Hits[0].VisibleTo[0].WorkspaceID = "workspace-other"
	input.Current.Decision.CollectionTags[0] = "changed"
	if first.Candidate.Output.Hits[0].Text != "stable knowledge" ||
		first.Candidate.Output.Hits[0].VisibleTo[0].WorkspaceID != "workspace-main" {
		t.Fatal("result aliases caller-owned candidate output")
	}
	first.Candidate.Output.Hits[0].Text = "result mutation"
	first.Candidate.Output.Hits[0].VisibleTo[0].AgentID = "agent-other"
	if second.Candidate.Output.Hits[0].Text != "stable knowledge" ||
		second.Candidate.Output.Hits[0].VisibleTo[0].AgentID != "agent-main" {
		t.Fatal("independent evaluations alias each other")
	}
}

func TestEvaluateReuseV1TTLAndLookbackBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*ReuseEvaluationInputV1)
		decision DecisionKind
	}{
		{
			name: "one millisecond before expiry",
			mutate: func(input *ReuseEvaluationInputV1) {
				input.Policy.ReuseTTLSeconds = 1
				input.EvaluatedAtUnixMS = input.Candidate.RetrievedAtUnixMS + 999
			},
			decision: DecisionReuse,
		},
		{
			name: "exact expiry",
			mutate: func(input *ReuseEvaluationInputV1) {
				input.Policy.ReuseTTLSeconds = 1
				input.EvaluatedAtUnixMS = input.Candidate.RetrievedAtUnixMS + 1000
			},
			decision: DecisionFreshRAG,
		},
		{
			name: "at lookback limit",
			mutate: func(input *ReuseEvaluationInputV1) {
				input.Candidate.LookbackTurns = input.Policy.MaxLookbackTurns
			},
			decision: DecisionReuse,
		},
		{
			name: "outside lookback limit",
			mutate: func(input *ReuseEvaluationInputV1) {
				input.Candidate.LookbackTurns = input.Policy.MaxLookbackTurns + 1
			},
			decision: DecisionFreshRAG,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validReuseEvaluationInput(t)
			test.mutate(&input)
			result, err := EvaluateReuseV1(input)
			if err != nil {
				t.Fatalf("EvaluateReuseV1: %v", err)
			}
			if result.Decision != test.decision {
				t.Fatalf("decision=%s want %s", result.Decision, test.decision)
			}
			assertFreshCarriesNoReuseEvidence(t, result)
		})
	}
}

func TestEvaluateReuseV1FallsBackFreshAcrossExactAxesWithoutOlderCandidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReuseEvaluationInputV1)
	}{
		{"not latest exact candidate", func(v *ReuseEvaluationInputV1) { v.Candidate.IsLatestExactQuestionCandidate = false }},
		{"not fresh retrieval", func(v *ReuseEvaluationInputV1) { v.Candidate.FreshRetrieval = false }},
		{"source not successful", func(v *ReuseEvaluationInputV1) { v.Candidate.SourceAttemptSucceeded = false }},
		{"conversation", func(v *ReuseEvaluationInputV1) { v.Candidate.ConversationID = "conversation-other" }},
		{"exact query bytes", func(v *ReuseEvaluationInputV1) { v.Candidate.Request.QueryText = "API design " }},
		{"complete workspace ref", func(v *ReuseEvaluationInputV1) { v.Candidate.Request.Scope.Workspace.Version = "2.0.0" }},
		{"task input ref", func(v *ReuseEvaluationInputV1) { v.Candidate.Request.Scope.TaskInputRef = reuseDigest("1") }},
		{"source revision", func(v *ReuseEvaluationInputV1) { v.Candidate.Request.Source.Version = "2.0.0" }},
		{"request limits", func(v *ReuseEvaluationInputV1) { v.Candidate.Request.MaxHits-- }},
		{"provider", func(v *ReuseEvaluationInputV1) { v.Candidate.Provider.ActivationRevision++ }},
		{"config", func(v *ReuseEvaluationInputV1) { v.Candidate.ConfigDigest = reuseDigest("2") }},
		{"authority", func(v *ReuseEvaluationInputV1) { v.Candidate.AuthorityDigest = reuseDigest("3") }},
		{"binding", func(v *ReuseEvaluationInputV1) { v.Candidate.BindingIndex++ }},
		{"routing algorithm", func(v *ReuseEvaluationInputV1) {
			v.Candidate.RoutingAlgorithmVersion = "freeagent.knowledge-routing/v2"
		}},
		{"decision set", func(v *ReuseEvaluationInputV1) { v.Candidate.DecisionSetDigest = reuseDigest("4") }},
		{"category kind", func(v *ReuseEvaluationInputV1) { v.CategoryCounter.Kind = moduleapi.MemoryEntryRepeatedTermCount }},
		{"category key outside decision tags", func(v *ReuseEvaluationInputV1) { v.CategoryCounter.Key = "frontend" }},
		{"category count", func(v *ReuseEvaluationInputV1) { v.CategoryCounter.Count = v.Policy.MinCategoryCount - 1 }},
		{"repeated digest", func(v *ReuseEvaluationInputV1) { v.RepeatedTermCounter.EntryDigest = "not-a-digest" }},
		{"repeated key outside matched terms", func(v *ReuseEvaluationInputV1) { v.RepeatedTermCounter.Key = "server" }},
		{"repeated count", func(v *ReuseEvaluationInputV1) { v.RepeatedTermCounter.Count = v.Policy.MinRepeatedTermCount - 1 }},
		{"same counter digest", func(v *ReuseEvaluationInputV1) { v.RepeatedTermCounter.EntryDigest = v.CategoryCounter.EntryDigest }},
		{"source output request closure", func(v *ReuseEvaluationInputV1) { v.Candidate.Output.RequestDigest = reuseDigest("5") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validReuseEvaluationInput(t)
			test.mutate(&input)
			result, err := EvaluateReuseV1(input)
			if err != nil {
				t.Fatalf("valid cache miss returned error: %v", err)
			}
			if result.Decision != DecisionFreshRAG {
				t.Fatalf("decision=%s want FRESH_RAG", result.Decision)
			}
			assertFreshCarriesNoReuseEvidence(t, result)
		})
	}

	t.Run("empty successful output", func(t *testing.T) {
		input := validReuseEvaluationInput(t)
		input.Candidate.Output = reuseOutputForRequest(t, input.Candidate.Request, nil)
		result, err := EvaluateReuseV1(input)
		if err != nil || result.Decision != DecisionFreshRAG {
			t.Fatalf("empty output result=%+v error=%v", result, err)
		}
		assertFreshCarriesNoReuseEvidence(t, result)
	})

	t.Run("hit no longer visible", func(t *testing.T) {
		input := validReuseEvaluationInput(t)
		input.Candidate.Output = reuseOutputForRequest(t, input.Candidate.Request, []moduleapi.KnowledgeScopeRuleV1{{
			TenantID:     input.Candidate.Request.Scope.TenantID,
			WorkspaceID:  "workspace-other",
			AgentID:      "*",
			TaskInputRef: "*",
		}})
		result, err := EvaluateReuseV1(input)
		if err != nil || result.Decision != DecisionFreshRAG {
			t.Fatalf("invisible output result=%+v error=%v", result, err)
		}
		assertFreshCarriesNoReuseEvidence(t, result)
	})
}

func TestEvaluateReuseV1FailsClosedOnInvalidStructure(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReuseEvaluationInputV1)
	}{
		{"zero ttl", func(v *ReuseEvaluationInputV1) { v.Policy.ReuseTTLSeconds = 0 }},
		{"zero evaluation time", func(v *ReuseEvaluationInputV1) { v.EvaluatedAtUnixMS = 0 }},
		{"current conversation", func(v *ReuseEvaluationInputV1) { v.Current.ConversationID = " bad " }},
		{"current request", func(v *ReuseEvaluationInputV1) { v.Current.Request.QueryText = "e\u0301" }},
		{"current provider", func(v *ReuseEvaluationInputV1) { v.Current.Provider.ModuleID = "Bad!" }},
		{"current digest", func(v *ReuseEvaluationInputV1) { v.Current.ConfigDigest = "bad" }},
		{"unsupported current algorithm", func(v *ReuseEvaluationInputV1) { v.Current.RoutingAlgorithmVersion = "freeagent.knowledge-routing/v2" }},
		{"non-fresh K1 decision", func(v *ReuseEvaluationInputV1) { v.Current.Decision.Decision = DecisionNotSelected }},
		{"decision fingerprint", func(v *ReuseEvaluationInputV1) { v.Current.Decision.ExactQuestionFingerprint = reuseDigest("6") }},
		{"unsorted decision tags", func(v *ReuseEvaluationInputV1) {
			v.Current.Decision.CollectionTags = []string{"backend", "architecture"}
		}},
		{"candidate attempt", func(v *ReuseEvaluationInputV1) { v.Candidate.SourceAttemptID = "" }},
		{"candidate request", func(v *ReuseEvaluationInputV1) { v.Candidate.Request.MaxHits = 0 }},
		{"candidate output", func(v *ReuseEvaluationInputV1) { v.Candidate.Output.Hits[0].Rank = 2 }},
		{"candidate provider", func(v *ReuseEvaluationInputV1) { v.Candidate.Provider.InstanceID = "" }},
		{"candidate digest", func(v *ReuseEvaluationInputV1) { v.Candidate.SourceCompilationDigest = "bad" }},
		{"candidate algorithm", func(v *ReuseEvaluationInputV1) { v.Candidate.RoutingAlgorithmVersion = "" }},
		{"zero retrieval time", func(v *ReuseEvaluationInputV1) { v.Candidate.RetrievedAtUnixMS = 0 }},
		{"retrieval after evaluation", func(v *ReuseEvaluationInputV1) { v.Candidate.RetrievedAtUnixMS = v.EvaluatedAtUnixMS + 1 }},
		{"zero lookback", func(v *ReuseEvaluationInputV1) { v.Candidate.LookbackTurns = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validReuseEvaluationInput(t)
			test.mutate(&input)
			if _, err := EvaluateReuseV1(input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error=%v want ErrInvalidInput", err)
			}
		})
	}
}

func assertFreshCarriesNoReuseEvidence(t *testing.T, result ReuseEvaluationV1) {
	t.Helper()
	if result.Decision == DecisionFreshRAG &&
		(result.Candidate != nil || result.CategoryCounter != nil ||
			result.RepeatedTermCounter != nil) {
		t.Fatalf("FRESH_RAG leaked reusable evidence: %+v", result)
	}
}

func validReuseEvaluationInput(t *testing.T) ReuseEvaluationInputV1 {
	t.Helper()
	request := moduleapi.KnowledgeContextRequestV1{
		SchemaVersion: moduleapi.KnowledgeContextRequestSchemaV1,
		Source:        knowledgeTestSource("a"),
		Scope: moduleapi.KnowledgeQueryScopeV1{
			TenantID: "tenant-main",
			Workspace: moduleapi.KnowledgeObjectRefV1{
				ID: "workspace-main", Version: "1.0.0", Digest: reuseDigest("b"),
			},
			Agent: moduleapi.KnowledgeObjectRefV1{
				ID: "agent-main", Version: "1.0.0", Digest: reuseDigest("c"),
			},
			TaskInputRef: reuseDigest("d"),
		},
		QueryText:         "API design",
		MaxHits:           4,
		MaxTotalTextBytes: 1024,
	}
	output := reuseOutputForRequest(t, request, []moduleapi.KnowledgeScopeRuleV1{{
		TenantID:     request.Scope.TenantID,
		WorkspaceID:  request.Scope.Workspace.ID,
		AgentID:      request.Scope.Agent.ID,
		TaskInputRef: request.Scope.TaskInputRef,
	}})
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           "freeagent.knowledge.test",
		Version:            "1.0.0",
		ArtifactDigest:     reuseDigest("e"),
		InstanceID:         "knowledge-main",
		ExecutionClass:     moduleapi.ExecutionDeclarative,
		AdapterIdentity:    "freeagent.knowledge.test/v1",
		ActivationRevision: 1,
	}
	decision := BindingDecision{
		BindingIndex:             2,
		Decision:                 DecisionFreshRAG,
		MatchedTerms:             []string{"api"},
		CollectionTags:           []string{"backend"},
		MinMatchTerms:            1,
		ExactQuestionFingerprint: moduleapi.Digest(ExactQuestionFingerprintDomainV1, []byte(request.QueryText)),
	}
	current := ReuseCurrentFactsV1{
		ConversationID:          "conversation-main",
		Request:                 request,
		Provider:                provider,
		ConfigDigest:            reuseDigest("f"),
		AuthorityDigest:         reuseDigest("7"),
		RoutingAlgorithmVersion: RoutingAlgorithmVersionV1,
		DecisionSetDigest:       reuseDigest("8"),
		Decision:                decision,
	}
	return ReuseEvaluationInputV1{
		Policy: moduleapi.KnowledgeReusePolicyV1{
			ExactQuestionOnly:    true,
			MinCategoryCount:     2,
			MinRepeatedTermCount: 3,
			MaxLookbackTurns:     8,
			ReuseTTLSeconds:      2,
		},
		EvaluatedAtUnixMS: 1500,
		Current:           current,
		Candidate: ReuseCandidateV1{
			ConversationID:                 current.ConversationID,
			SourceAttemptID:                "attempt-source",
			SourceCompilationDigest:        reuseDigest("9"),
			Request:                        request,
			Output:                         output,
			Provider:                       provider,
			ConfigDigest:                   current.ConfigDigest,
			AuthorityDigest:                current.AuthorityDigest,
			BindingIndex:                   decision.BindingIndex,
			RoutingAlgorithmVersion:        current.RoutingAlgorithmVersion,
			DecisionSetDigest:              current.DecisionSetDigest,
			RetrievedAtUnixMS:              1000,
			LookbackTurns:                  1,
			IsLatestExactQuestionCandidate: true,
			FreshRetrieval:                 true,
			SourceAttemptSucceeded:         true,
		},
		CategoryCounter: moduleapi.MemoryCandidateV1{
			EntryDigest: reuseDigest("a"),
			Kind:        moduleapi.MemoryEntryCategoryCount,
			Key:         "backend",
			Count:       2,
		},
		RepeatedTermCounter: moduleapi.MemoryCandidateV1{
			EntryDigest: reuseDigest("b"),
			Kind:        moduleapi.MemoryEntryRepeatedTermCount,
			Key:         "api",
			Count:       3,
		},
	}
}

func reuseOutputForRequest(
	t *testing.T,
	request moduleapi.KnowledgeContextRequestV1,
	visible []moduleapi.KnowledgeScopeRuleV1,
) moduleapi.KnowledgeContextOutputV1 {
	t.Helper()
	_, _, requestDigest, err := moduleapi.NewKnowledgeContextRequestV1(request)
	if err != nil {
		t.Fatalf("NewKnowledgeContextRequestV1: %v", err)
	}
	hits := []moduleapi.KnowledgeHitV1{}
	if visible != nil {
		chunk, _, err := moduleapi.NewKnowledgeChunkV1(moduleapi.KnowledgeChunkV1{
			Document: moduleapi.KnowledgeDocumentRefV1{
				ID: "doc.api", Version: "1.0.0", Digest: reuseDigest("c"),
			},
			ChunkID:   "chunk-api",
			Text:      "stable knowledge",
			VisibleTo: visible,
		})
		if err != nil {
			t.Fatalf("NewKnowledgeChunkV1: %v", err)
		}
		hits = append(hits, moduleapi.KnowledgeHitV1{
			Rank:        1,
			Document:    chunk.Document,
			ChunkID:     chunk.ChunkID,
			ChunkDigest: chunk.ChunkDigest,
			Text:        chunk.Text,
			VisibleTo:   chunk.VisibleTo,
		})
	}
	output, _, _, err := moduleapi.NewKnowledgeContextOutputV1(moduleapi.KnowledgeContextOutputV1{
		SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
		RequestDigest: requestDigest,
		Source:        request.Source,
		Hits:          hits,
	})
	if err != nil {
		t.Fatalf("NewKnowledgeContextOutputV1: %v", err)
	}
	return output
}

func reuseDigest(character string) string {
	return strings.Repeat(character, moduleapi.SHA256HexLength)
}
