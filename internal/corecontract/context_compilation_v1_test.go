package corecontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func compilationDigest(character string) string {
	return strings.Repeat(character, 64)
}

func validCompilationBase() ContextCompilationV1 {
	return ContextCompilationV1{
		SchemaVersion: ContextCompilationSchemaVersionV1,
		WorkspaceScope: WorkspaceRef{
			ID: "workspace", Version: "1", Digest: compilationDigest("a"),
		},
		ContextPolicy: PolicyRef{
			ID: "context", Version: "1", Digest: compilationDigest("b"),
		},
		EstimatorVersion:        ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
		SummaryAlgorithmVersion: ContextSummaryHeadTailExtractiveV1,
		InputBudgetTokens:       1000,
		RestoreWatermarkTokens:  850,
		OriginalEstimateTokens:  900,
		Drops:                   []ContextCompilationDropV1{},
		FinalRequestDigest:      compilationDigest("c"),
	}
}

func TestContextHistoryTurnDigestV1UsesSequenceAndContentOnly(t *testing.T) {
	content := compilationDigest("d")
	first, err := ContextHistoryTurnDigestV1(1, content)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := ContextHistoryTurnDigestV1(1, content)
	if err != nil || repeated != first {
		t.Fatalf("repeated digest=%s error=%v, want %s", repeated, err, first)
	}
	second, err := ContextHistoryTurnDigestV1(2, content)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("sequence did not distinguish repeated MODEL_RESULT content")
	}
	if _, err := ContextHistoryTurnDigestV1(0, content); err == nil {
		t.Fatal("accepted zero History sequence")
	}
	if _, err := ContextHistoryTurnDigestV1(1, "result"); err == nil {
		t.Fatal("accepted invalid source content digest")
	}
}

func TestContextConversationTurnDigestV1BindsPairSourcesAndIndex(
	t *testing.T,
) {
	user := compilationDigest("a")
	assistant := compilationDigest("b")
	first, err := ContextConversationTurnDigestV1(1, user, assistant)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := ContextConversationTurnDigestV1(1, user, assistant)
	if err != nil || repeated != first {
		t.Fatalf("repeated Conversation digest=%s err=%v, want %s", repeated, err, first)
	}
	second, err := ContextConversationTurnDigestV1(2, user, assistant)
	if err != nil || second == first {
		t.Fatalf("turn-index digest=%s err=%v, first=%s", second, err, first)
	}
	changedUser, err := ContextConversationTurnDigestV1(
		1,
		compilationDigest("c"),
		assistant,
	)
	if err != nil || changedUser == first {
		t.Fatalf("USER-source digest=%s err=%v, first=%s", changedUser, err, first)
	}
	changedAssistant, err := ContextConversationTurnDigestV1(
		1,
		user,
		compilationDigest("d"),
	)
	if err != nil || changedAssistant == first {
		t.Fatalf("ASSISTANT-source digest=%s err=%v, first=%s", changedAssistant, err, first)
	}
	if _, err := ContextConversationTurnDigestV1(0, user, assistant); err == nil {
		t.Fatal("zero Conversation turn index was accepted")
	}
	if _, err := ContextConversationTurnDigestV1(1, "user", assistant); err == nil {
		t.Fatal("invalid Conversation USER digest was accepted")
	}
	if _, err := ContextConversationTurnDigestV1(1, user, "assistant"); err == nil {
		t.Fatal("invalid Conversation ASSISTANT digest was accepted")
	}
}

func TestContextHeadTailSummaryV1PreservesShortAndUnicodeBoundaries(t *testing.T) {
	short := []moduleapi.ModelMessageV1{{
		Role:    moduleapi.ModelRoleAssistant,
		Content: strings.Repeat("中文🙂", 30),
	}}
	got, err := ContextHeadTailSummaryV1(short)
	if err != nil {
		t.Fatal(err)
	}
	want := contextSummaryPrefixV1 + "[ASSISTANT]\n" + short[0].Content
	if got != want {
		t.Fatalf("short summary was truncated\ngot=%q\nwant=%q", got, want)
	}
	long := []moduleapi.ModelMessageV1{{
		Role:    moduleapi.ModelRoleAssistant,
		Content: strings.Repeat("前中文🙂后", 400),
	}}
	got, err = ContextHeadTailSummaryV1(long)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, contextSummaryOmissionV1) || !utf8.ValidString(got) {
		t.Fatalf("invalid long summary: %q", got)
	}
	if got != moduleapi.CanonicalText(got) || len(got) > contextSummaryMaxTextBytesV1 {
		t.Fatalf("summary is not bounded canonical text: %d bytes", len(got))
	}
	repeated, err := ContextHeadTailSummaryV1(long)
	if err != nil || repeated != got {
		t.Fatalf("summary is not deterministic: error=%v", err)
	}
}

func TestContextHeadTailSummaryV1AcceptsOnlyCompleteConversationPairs(
	t *testing.T,
) {
	pairs := []moduleapi.ModelMessageV1{
		{Role: moduleapi.ModelRoleUser, Content: "first question"},
		{Role: moduleapi.ModelRoleAssistant, Content: "first answer"},
		{Role: moduleapi.ModelRoleUser, Content: "second question"},
		{Role: moduleapi.ModelRoleAssistant, Content: "second answer"},
	}
	got, err := ContextHeadTailSummaryV1(pairs)
	if err != nil {
		t.Fatal(err)
	}
	want := contextSummaryPrefixV1 +
		"[USER]\nfirst question\n[ASSISTANT]\nfirst answer\n" +
		"[USER]\nsecond question\n[ASSISTANT]\nsecond answer"
	if got != want {
		t.Fatalf("Conversation summary=%q, want %q", got, want)
	}
	for name, messages := range map[string][]moduleapi.ModelMessageV1{
		"USER only":        pairs[:1],
		"reversed pair":    {pairs[1], pairs[0]},
		"pair plus USER":   pairs[:3],
		"legacy then pair": {{Role: moduleapi.ModelRoleAssistant, Content: "legacy"}, pairs[0], pairs[1]},
	} {
		if _, err := ContextHeadTailSummaryV1(messages); err == nil {
			t.Fatalf("%s range was accepted", name)
		}
	}

	legacy := []moduleapi.ModelMessageV1{
		{Role: moduleapi.ModelRoleAssistant, Content: "legacy one"},
		{Role: moduleapi.ModelRoleAssistant, Content: "legacy two"},
	}
	legacySummary, err := ContextHeadTailSummaryV1(legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyWant := contextSummaryPrefixV1 +
		"[ASSISTANT]\nlegacy one\n[ASSISTANT]\nlegacy two"
	if legacySummary != legacyWant {
		t.Fatalf("legacy summary bytes changed: %q, want %q", legacySummary, legacyWant)
	}
}

func TestContextHeadTailSummaryV1RejectsInvalidHistory(t *testing.T) {
	if _, err := ContextHeadTailSummaryV1(nil); err == nil {
		t.Fatal("accepted empty History range")
	}
	if _, err := ContextHeadTailSummaryV1([]moduleapi.ModelMessageV1{{
		Role:    moduleapi.ModelRoleUser,
		Content: "task",
	}}); err == nil {
		t.Fatal("accepted non-ASSISTANT History message")
	}
	if _, err := ContextHeadTailSummaryV1([]moduleapi.ModelMessageV1{{
		Role: moduleapi.ModelRoleAssistant,
	}}); err == nil {
		t.Fatal("accepted empty History content")
	}
}

func TestContextCompilationV1SummaryRoundTrip(t *testing.T) {
	value := validCompilationBase()
	value.Summary = &ContextCompilationSummaryV1{
		SourceTurnDigests:    []string{compilationDigest("d"), compilationDigest("e")},
		Text:                 contextSummaryPrefixV1 + "[ASSISTANT]\nEarlier conversation.",
		BeforeEstimateTokens: 900,
		AfterEstimateTokens:  820,
	}
	value.FinalEstimateTokens = 820
	value.StopReason = ContextCompilationSummaryToWatermark
	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	value.Summary.SourceTurnDigests[0] = compilationDigest("f")
	if frozen.Summary.SourceTurnDigests[0] != compilationDigest("d") {
		t.Fatal("NewContextCompilationV1 aliased summary source digests")
	}
	restored, err := RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuilt, err := NewContextCompilationV1(restored)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		t.Fatalf("round trip error=%v\nfirst=%s\nagain=%s", err, canonical, rebuilt)
	}
}

func TestContextCompilationV1DropRoundTrip(t *testing.T) {
	value := validCompilationBase()
	value.OriginalEstimateTokens = 1100
	value.Drops = []ContextCompilationDropV1{
		{
			UnitKind:             ContextCompilationUnitHistoryTurn,
			UnitDigest:           compilationDigest("d"),
			BeforeEstimateTokens: 1100,
			AfterEstimateTokens:  920,
		},
		{
			UnitKind:             ContextCompilationUnitHistoryTurn,
			UnitDigest:           compilationDigest("e"),
			BeforeEstimateTokens: 920,
			AfterEstimateTokens:  800,
		},
	}
	value.FinalEstimateTokens = 800
	value.StopReason = ContextCompilationDropToWatermark
	_, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreContextCompilationV1(canonical); err != nil {
		t.Fatal(err)
	}
}

func TestContextCompilationV1NoEligibleSummary(t *testing.T) {
	value := validCompilationBase()
	value.FinalEstimateTokens = value.OriginalEstimateTokens
	value.StopReason = ContextCompilationNoEligibleSummary
	_, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte(`"knowledge_retrievals"`)) {
		t.Fatal("no-RAG canonical form unexpectedly contains knowledge_retrievals")
	}
}

func validKnowledgeRetrievalEvidence(t *testing.T) KnowledgeRetrievalEvidenceV1 {
	t.Helper()
	scope := moduleapi.KnowledgeQueryScopeV1{
		TenantID: "tenant-a",
		Workspace: moduleapi.KnowledgeObjectRefV1{
			ID: "workspace", Version: "1", Digest: compilationDigest("1"),
		},
		Agent: moduleapi.KnowledgeObjectRefV1{
			ID: "agent", Version: "1", Digest: compilationDigest("2"),
		},
		TaskInputRef: compilationDigest("3"),
	}
	source := moduleapi.KnowledgeSourceRefV1{
		ID: "knowledge.source", Version: "1", Digest: compilationDigest("4"),
	}
	hit := moduleapi.KnowledgeHitV1{
		Rank: 1,
		Document: moduleapi.KnowledgeDocumentRefV1{
			ID: "document", Version: "1", Digest: compilationDigest("5"),
		},
		ChunkID: "chunk-1",
		Text:    "bounded knowledge text",
		VisibleTo: []moduleapi.KnowledgeScopeRuleV1{{
			TenantID:     scope.TenantID,
			WorkspaceID:  scope.Workspace.ID,
			AgentID:      scope.Agent.ID,
			TaskInputRef: scope.TaskInputRef,
		}},
	}
	chunkDigest, err := moduleapi.ComputeKnowledgeChunkDigestV1(
		moduleapi.KnowledgeChunkV1{
			Document:  hit.Document,
			ChunkID:   hit.ChunkID,
			Text:      hit.Text,
			VisibleTo: hit.VisibleTo,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	hit.ChunkDigest = chunkDigest
	requestDigest := compilationDigest("6")
	_, _, outputDigest, err := moduleapi.NewKnowledgeContextOutputV1(
		moduleapi.KnowledgeContextOutputV1{
			SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
			RequestDigest: requestDigest,
			Source:        source,
			Hits:          []moduleapi.KnowledgeHitV1{hit},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return KnowledgeRetrievalEvidenceV1{
		BindingIndex:        1,
		ConfigRef:           compilationDigest("7"),
		AuthorityCeilingRef: compilationDigest("8"),
		RequestDigest:       requestDigest,
		Scope:               scope,
		Source:              source,
		Hits:                []moduleapi.KnowledgeHitV1{hit},
		OutputDigest:        outputDigest,
	}
}

func validKnowledgeRetrievalProvenance() *KnowledgeRetrievalProvenanceV1 {
	return &KnowledgeRetrievalProvenanceV1{
		Provider: moduleapi.ActivatedModuleRef{
			ModuleID:           "test.knowledge",
			Version:            "1.0.0",
			ArtifactDigest:     compilationDigest("a"),
			InstanceID:         "knowledge-instance",
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "test.knowledge.adapter.v1",
			ActivationRevision: 1,
		},
		RoutingAlgorithmVersion: "freeagent.knowledge-routing/v1",
		DecisionSetDigest:       compilationDigest("f"),
		RetrievedAtUnixMS:       1000,
	}
}

func validReusableKnowledgeRetrievalEvidence(
	t *testing.T,
	bindingIndex uint32,
) KnowledgeRetrievalEvidenceV1 {
	t.Helper()
	evidence := validKnowledgeRetrievalEvidence(t)
	evidence.BindingIndex = bindingIndex
	evidence.Provenance = validKnowledgeRetrievalProvenance()
	return evidence
}

func validKnowledgeReuseEvidence(
	t *testing.T,
	knowledgeBindingIndex uint32,
	memoryBindingIndex uint32,
) KnowledgeReuseEvidenceV1 {
	t.Helper()
	return KnowledgeReuseEvidenceV1{
		SourceConversationID: "conversation-main",
		SourceTurnIndex:      1,
		SourceRunID:          "run-source",
		SourceAttemptID:      "attempt-source",
		SourceCompilationRef: compilationDigest("c"),
		FreshRetrieval: validReusableKnowledgeRetrievalEvidence(
			t,
			knowledgeBindingIndex,
		),
		MemoryBindingIndex: memoryBindingIndex,
		CategoryCounter: moduleapi.MemoryCandidateV1{
			EntryDigest: compilationDigest("d"),
			Kind:        moduleapi.MemoryEntryCategoryCount,
			Key:         "backend",
			Count:       17,
		},
		RepeatedTermCounter: moduleapi.MemoryCandidateV1{
			EntryDigest: compilationDigest("e"),
			Kind:        moduleapi.MemoryEntryRepeatedTermCount,
			Key:         "api",
			Count:       9,
		},
	}
}

func validKnowledgeShortcutEvidence(t *testing.T) KnowledgeShortcutEvidenceV1 {
	t.Helper()
	retrieval := validKnowledgeRetrievalEvidence(t)
	return KnowledgeShortcutEvidenceV1{
		BindingIndex:             2,
		ConfigRef:                compilationDigest("7"),
		AuthorityCeilingRef:      compilationDigest("8"),
		Scope:                    retrieval.Scope,
		Source:                   retrieval.Source,
		DecisionSetDigest:        compilationDigest("d"),
		ExactQuestionFingerprint: compilationDigest("e"),
		CollectionTags:           []string{"architecture", "backend"},
		MatchedTerms:             []string{"api"},
		MinMatchTerms:            2,
		Mode:                     KnowledgeShortcutNotSelectedV1,
	}
}

func validMemoryReadEvidence(
	t *testing.T,
	bindingIndex uint32,
) MemoryReadEvidenceV1 {
	t.Helper()
	scope := moduleapi.MemoryQueryScopeV1{
		TenantID: "tenant-a",
		Workspace: moduleapi.MemoryObjectRefV1{
			ID: "workspace", Version: "1", Digest: compilationDigest("1"),
		},
		Agent: moduleapi.MemoryObjectRefV1{
			ID: "agent", Version: "1", Digest: compilationDigest("2"),
		},
		TaskInputRef: compilationDigest("3"),
	}
	snapshot := moduleapi.MemorySnapshotRefV1{
		TenantID: scope.TenantID,
		AgentID:  scope.Agent.ID,
		Revision: 1,
		Digest:   compilationDigest("4"),
	}
	selected := []moduleapi.MemoryCandidateV1{
		{
			EntryDigest: compilationDigest("5"),
			Kind:        moduleapi.MemoryEntryFact,
			Key:         "preferred-language",
			Text:        "Go",
		},
		{
			EntryDigest: compilationDigest("6"),
			Kind:        moduleapi.MemoryEntryCategoryCount,
			Key:         "backend",
			Count:       17,
		},
	}
	requestDigest := compilationDigest("7")
	_, _, outputDigest, err := moduleapi.NewMemoryContextOutputV1(
		moduleapi.MemoryContextOutputV1{
			SchemaVersion: moduleapi.MemoryContextOutputSchemaV1,
			RequestDigest: requestDigest,
			Snapshot:      snapshot,
			SelectedEntryDigests: []string{
				selected[0].EntryDigest,
				selected[1].EntryDigest,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return MemoryReadEvidenceV1{
		BindingIndex:        bindingIndex,
		ConfigRef:           compilationDigest("8"),
		AuthorityCeilingRef: compilationDigest("9"),
		RequestDigest:       requestDigest,
		Scope:               scope,
		Snapshot:            snapshot,
		EvaluatedAtUnixMS:   2000,
		SelectedEntries:     selected,
		OutputDigest:        outputDigest,
	}
}

func TestContextCompilationV1KnowledgeRetrievalBelowWatermarkRoundTrip(t *testing.T) {
	evidence := validKnowledgeRetrievalEvidence(t)
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationRetrievalBelowWatermark
	value.KnowledgeRetrievals = []KnowledgeRetrievalEvidenceV1{evidence}
	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`"knowledge_retrievals"`)) {
		t.Fatal("RAG canonical form omitted knowledge_retrievals")
	}
	if frozen.KnowledgeRetrievals[0].Provenance != nil ||
		bytes.Contains(canonical, []byte(`"provenance"`)) ||
		bytes.Contains(canonical, []byte(`"knowledge_reuses"`)) {
		t.Fatalf("legacy Knowledge retrieval wire gained K3 fields: %s", canonical)
	}
	value.KnowledgeRetrievals[0].Hits[0].Text = "mutated"
	value.KnowledgeRetrievals[0].Hits[0].VisibleTo[0].AgentID = "other"
	if frozen.KnowledgeRetrievals[0].Hits[0].Text != "bounded knowledge text" ||
		frozen.KnowledgeRetrievals[0].Hits[0].VisibleTo[0].AgentID != "agent" {
		t.Fatal("NewContextCompilationV1 aliased Knowledge retrieval evidence")
	}
	restored, err := RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuilt, err := NewContextCompilationV1(restored)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		t.Fatalf("RAG round trip error=%v\nfirst=%s\nagain=%s", err, canonical, rebuilt)
	}
}

func TestContextCompilationV1KnowledgeReuseRoundTripAndDefensiveFreeze(
	t *testing.T,
) {
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationRetrievalBelowWatermark
	value.MemoryReads = []MemoryReadEvidenceV1{validMemoryReadEvidence(t, 2)}
	value.KnowledgeReuses = []KnowledgeReuseEvidenceV1{
		validKnowledgeReuseEvidence(t, 1, 2),
	}

	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen.KnowledgeReuses) != 1 ||
		len(frozen.KnowledgeRetrievals) != 0 ||
		!bytes.Contains(canonical, []byte(`"knowledge_reuses"`)) ||
		bytes.Contains(canonical, []byte(`"knowledge_retrievals"`)) {
		t.Fatalf("Knowledge reuse canonical evidence shape = %s", canonical)
	}

	value.KnowledgeReuses[0].FreshRetrieval.Hits[0].Text = "mutated"
	value.KnowledgeReuses[0].FreshRetrieval.Hits[0].VisibleTo[0].AgentID =
		"other-agent"
	value.KnowledgeReuses[0].FreshRetrieval.Provenance.Provider.InstanceID =
		"other-instance"
	value.KnowledgeReuses[0].CategoryCounter.Count = 999
	if frozen.KnowledgeReuses[0].FreshRetrieval.Hits[0].Text !=
		"bounded knowledge text" ||
		frozen.KnowledgeReuses[0].FreshRetrieval.Hits[0].VisibleTo[0].AgentID !=
			"agent" ||
		frozen.KnowledgeReuses[0].FreshRetrieval.Provenance.Provider.InstanceID !=
			"knowledge-instance" ||
		frozen.KnowledgeReuses[0].CategoryCounter.Count != 17 {
		t.Fatal("NewContextCompilationV1 aliased Knowledge reuse evidence")
	}

	restored, err := RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuilt, err := NewContextCompilationV1(restored)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		t.Fatalf("Knowledge reuse round trip error=%v\nfirst=%s\nagain=%s", err, canonical, rebuilt)
	}
}

func TestContextCompilationV1RejectsBrokenKnowledgeReuseEvidence(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(*ContextCompilationV1)
	}{
		{"source conversation", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].SourceConversationID = ""
		}},
		{"source turn", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].SourceTurnIndex = 0
		}},
		{"source attempt", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].SourceAttemptID = ""
		}},
		{"source compilation", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].SourceCompilationRef = "compilation"
		}},
		{"nested provenance absent", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].FreshRetrieval.Provenance = nil
		}},
		{"nested hits absent", func(value *ContextCompilationV1) {
			fresh := &value.KnowledgeReuses[0].FreshRetrieval
			fresh.Hits = nil
			_, _, outputDigest, err := moduleapi.NewKnowledgeContextOutputV1(
				moduleapi.KnowledgeContextOutputV1{
					SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
					RequestDigest: fresh.RequestDigest,
					Source:        fresh.Source,
					Hits:          []moduleapi.KnowledgeHitV1{},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			fresh.OutputDigest = outputDigest
		}},
		{"nested output tampered", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].FreshRetrieval.Hits[0].Text = "tampered"
		}},
		{"nested provider", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].FreshRetrieval.Provenance.Provider.ModuleID =
				"Invalid"
		}},
		{"nested routing digest", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].FreshRetrieval.Provenance.DecisionSetDigest =
				"decision"
		}},
		{"nested retrieval time", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].FreshRetrieval.Provenance.RetrievedAtUnixMS =
				0
		}},
		{"absent Memory proof", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].MemoryBindingIndex = 3
		}},
		{"Knowledge Binding used as Memory proof", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].MemoryBindingIndex = 1
		}},
		{"category counter kind", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].CategoryCounter.Kind =
				moduleapi.MemoryEntryRepeatedTermCount
		}},
		{"repeated counter kind", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].RepeatedTermCounter.Kind =
				moduleapi.MemoryEntryCategoryCount
		}},
		{"counter digest reused", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].RepeatedTermCounter.EntryDigest =
				value.KnowledgeReuses[0].CategoryCounter.EntryDigest
		}},
		{"Memory proof scope", func(value *ContextCompilationV1) {
			value.MemoryReads[0].Scope.Agent.Version = "2"
		}},
		{"Memory proof before retrieval", func(value *ContextCompilationV1) {
			value.MemoryReads[0].EvaluatedAtUnixMS = 999
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validCompilationBase()
			value.OriginalEstimateTokens = 700
			value.FinalEstimateTokens = 700
			value.StopReason = ContextCompilationRetrievalBelowWatermark
			value.MemoryReads = []MemoryReadEvidenceV1{
				validMemoryReadEvidence(t, 2),
			}
			value.KnowledgeReuses = []KnowledgeReuseEvidenceV1{
				validKnowledgeReuseEvidence(t, 1, 2),
			}
			test.mutate(&value)
			if _, _, err := NewContextCompilationV1(value); err == nil {
				t.Fatal("accepted invalid Knowledge reuse evidence")
			}
		})
	}
}

func TestContextCompilationV1KnowledgeShortcutBelowWatermarkRoundTrip(t *testing.T) {
	evidence := validKnowledgeShortcutEvidence(t)
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationKnowledgeShortcutBelowWatermark
	value.KnowledgeShortcuts = []KnowledgeShortcutEvidenceV1{evidence}
	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(frozen.KnowledgeRetrievals) != 0 ||
		len(frozen.KnowledgeShortcuts) != 1 ||
		!bytes.Contains(canonical, []byte(`"knowledge_shortcuts"`)) ||
		!bytes.Contains(canonical, []byte(`"stop_reason":"KNOWLEDGE_SHORTCUT_BELOW_WATERMARK"`)) ||
		bytes.Contains(canonical, []byte(`"knowledge_retrievals"`)) ||
		bytes.Contains(canonical, []byte(`"stop_reason":"RETRIEVAL_EVIDENCE_BELOW_WATERMARK"`)) {
		t.Fatalf("shortcut evidence shape=%+v %s", frozen, canonical)
	}
	value.KnowledgeShortcuts[0].CollectionTags[0] = "mutated"
	value.KnowledgeShortcuts[0].MatchedTerms[0] = "mutated"
	if frozen.KnowledgeShortcuts[0].CollectionTags[0] != "architecture" ||
		frozen.KnowledgeShortcuts[0].MatchedTerms[0] != "api" {
		t.Fatal("ContextCompilation aliases shortcut evidence")
	}
	restored, err := RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuilt, err := NewContextCompilationV1(restored)
	if err != nil || !bytes.Equal(canonical, rebuilt) {
		t.Fatalf("shortcut round trip error=%v\nfirst=%s\nagain=%s", err, canonical, rebuilt)
	}
}

func TestContextCompilationV1RejectsBrokenKnowledgeShortcutEvidence(t *testing.T) {
	valid := validKnowledgeShortcutEvidence(t)
	tests := []struct {
		name   string
		mutate func(*KnowledgeShortcutEvidenceV1)
	}{
		{"mode", func(value *KnowledgeShortcutEvidenceV1) { value.Mode = "FRESH_RAG" }},
		{"config ref", func(value *KnowledgeShortcutEvidenceV1) { value.ConfigRef = "config" }},
		{"authority ref", func(value *KnowledgeShortcutEvidenceV1) { value.AuthorityCeilingRef = "authority" }},
		{"decision digest", func(value *KnowledgeShortcutEvidenceV1) { value.DecisionSetDigest = "decision" }},
		{"question fingerprint", func(value *KnowledgeShortcutEvidenceV1) { value.ExactQuestionFingerprint = "question" }},
		{"scope", func(value *KnowledgeShortcutEvidenceV1) { value.Scope.TenantID = "*" }},
		{"source", func(value *KnowledgeShortcutEvidenceV1) { value.Source.Digest = "source" }},
		{"empty tags", func(value *KnowledgeShortcutEvidenceV1) { value.CollectionTags = nil }},
		{"unsorted tags", func(value *KnowledgeShortcutEvidenceV1) { value.CollectionTags = []string{"backend", "architecture"} }},
		{"duplicate tags", func(value *KnowledgeShortcutEvidenceV1) { value.CollectionTags = []string{"backend", "backend"} }},
		{"uppercase tag", func(value *KnowledgeShortcutEvidenceV1) { value.CollectionTags = []string{"Backend"} }},
		{"unsorted terms", func(value *KnowledgeShortcutEvidenceV1) { value.MatchedTerms = []string{"server", "api"} }},
		{"uppercase term", func(value *KnowledgeShortcutEvidenceV1) { value.MatchedTerms = []string{"API"} }},
		{"zero threshold", func(value *KnowledgeShortcutEvidenceV1) { value.MinMatchTerms = 0 }},
		{"qualified match count", func(value *KnowledgeShortcutEvidenceV1) { value.MinMatchTerms = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := valid
			evidence.CollectionTags = append([]string(nil), valid.CollectionTags...)
			evidence.MatchedTerms = append([]string(nil), valid.MatchedTerms...)
			test.mutate(&evidence)
			value := validCompilationBase()
			value.OriginalEstimateTokens = 700
			value.FinalEstimateTokens = 700
			value.StopReason = ContextCompilationKnowledgeShortcutBelowWatermark
			value.KnowledgeShortcuts = []KnowledgeShortcutEvidenceV1{evidence}
			if _, _, err := NewContextCompilationV1(value); err == nil {
				t.Fatal("accepted invalid Knowledge shortcut evidence")
			}
		})
	}
}

func TestContextCompilationV1MemoryBelowWatermarkRoundTrip(t *testing.T) {
	evidence := validMemoryReadEvidence(t, 2)
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationRetrievalBelowWatermark
	value.MemoryReads = []MemoryReadEvidenceV1{evidence}
	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`"memory_reads"`)) ||
		bytes.Contains(canonical, []byte(`"knowledge_retrievals"`)) {
		t.Fatalf("Memory-only canonical evidence shape = %s", canonical)
	}
	value.MemoryReads[0].SelectedEntries[0].Text = "mutated"
	if frozen.MemoryReads[0].SelectedEntries[0].Text != "Go" {
		t.Fatal("NewContextCompilationV1 aliased Memory selected entries")
	}
	restored, err := RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuilt, err := NewContextCompilationV1(restored)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		t.Fatalf("Memory round trip error=%v\nfirst=%s\nagain=%s", err, canonical, rebuilt)
	}
	if !bytes.Equal(canonical, rebuilt) ||
		!bytes.Equal(
			[]byte(restored.MemoryReads[0].SelectedEntries[0].Text),
			[]byte("Go"),
		) {
		t.Fatal("restored Memory evidence changed")
	}
}

func TestContextCompilationV1ActionReservationBelowWatermarkRoundTrip(
	t *testing.T,
) {
	reservation := ActionResultReservationV1{
		MaxEnvelopeBytes: 4096,
		EstimatedTokens:  4096,
	}
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationActionResultReserved
	value.ActionResultReservation = &reservation

	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`"action_result_reservation":{`)) {
		t.Fatalf("Action reservation omitted: %s", canonical)
	}
	reservation.MaxEnvelopeBytes = 1
	if frozen.ActionResultReservation == nil ||
		frozen.ActionResultReservation.MaxEnvelopeBytes != 4096 {
		t.Fatal("NewContextCompilationV1 aliases Action reservation")
	}
	restored, err := RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuilt, err := NewContextCompilationV1(restored)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		t.Fatalf("Action reservation round trip error=%v", err)
	}
}

func TestContextCompilationV1ActionReservationFailsClosed(t *testing.T) {
	valid := func() ContextCompilationV1 {
		value := validCompilationBase()
		value.OriginalEstimateTokens = 700
		value.FinalEstimateTokens = 700
		value.StopReason = ContextCompilationActionResultReserved
		value.ActionResultReservation = &ActionResultReservationV1{
			MaxEnvelopeBytes: 4096,
			EstimatedTokens:  4096,
		}
		return value
	}
	tests := []struct {
		name   string
		mutate func(*ContextCompilationV1)
	}{
		{
			name: "missing reservation",
			mutate: func(value *ContextCompilationV1) {
				value.ActionResultReservation = nil
			},
		},
		{
			name: "zero estimated tokens",
			mutate: func(value *ContextCompilationV1) {
				value.ActionResultReservation.EstimatedTokens = 0
			},
		},
		{
			name: "retrieval stop without evidence",
			mutate: func(value *ContextCompilationV1) {
				value.StopReason = ContextCompilationRetrievalBelowWatermark
			},
		},
		{
			name: "Action stop with dynamic evidence",
			mutate: func(value *ContextCompilationV1) {
				value.KnowledgeRetrievals = []KnowledgeRetrievalEvidenceV1{
					validKnowledgeRetrievalEvidence(t),
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid()
			test.mutate(&value)
			if _, _, err := NewContextCompilationV1(value); err == nil {
				t.Fatal("accepted invalid Action reservation compilation")
			}
		})
	}
}

func TestContextCompilationV1WithoutActionKeepsLegacyShape(t *testing.T) {
	value := validCompilationBase()
	value.FinalEstimateTokens = value.OriginalEstimateTokens
	value.StopReason = ContextCompilationNoEligibleSummary
	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.ActionResultReservation != nil || frozen.Composite != nil ||
		len(frozen.KnowledgeShortcuts) != 0 ||
		len(frozen.KnowledgeReuses) != 0 ||
		bytes.Contains(canonical, []byte(`"action_result_reservation"`)) ||
		bytes.Contains(canonical, []byte(`"composite"`)) ||
		bytes.Contains(canonical, []byte(`"knowledge_reuses"`)) ||
		bytes.Contains(canonical, []byte(`"knowledge_shortcuts"`)) {
		t.Fatalf("no-Action compilation wire changed: %s", canonical)
	}
	const legacyNoActionWireSHA256 = "08a4ae53d4f9cf36a40f87de77974c824bdeb6b10e4b865577cc28a498969508"
	wireHash := sha256.Sum256(canonical)
	if got := hex.EncodeToString(wireHash[:]); got != legacyNoActionWireSHA256 {
		t.Fatalf("no-Action compilation wire hash changed: %s", got)
	}
}

func TestContextCompilationV1KnowledgeOutcomeBindingIndexIsThreeWayExclusive(
	t *testing.T,
) {
	newValue := func() ContextCompilationV1 {
		value := validCompilationBase()
		value.OriginalEstimateTokens = 700
		value.FinalEstimateTokens = 700
		value.StopReason = ContextCompilationRetrievalBelowWatermark
		retrieval := validKnowledgeRetrievalEvidence(t)
		retrieval.BindingIndex = 1
		value.KnowledgeRetrievals = []KnowledgeRetrievalEvidenceV1{retrieval}
		value.MemoryReads = []MemoryReadEvidenceV1{validMemoryReadEvidence(t, 2)}
		value.KnowledgeReuses = []KnowledgeReuseEvidenceV1{
			validKnowledgeReuseEvidence(t, 3, 2),
		}
		shortcut := validKnowledgeShortcutEvidence(t)
		shortcut.BindingIndex = 4
		value.KnowledgeShortcuts = []KnowledgeShortcutEvidenceV1{shortcut}
		return value
	}

	value := newValue()
	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.KnowledgeRetrievals[0].BindingIndex != 1 ||
		frozen.MemoryReads[0].BindingIndex != 2 ||
		frozen.KnowledgeReuses[0].FreshRetrieval.BindingIndex != 3 ||
		frozen.KnowledgeReuses[0].MemoryBindingIndex != 2 ||
		frozen.KnowledgeShortcuts[0].BindingIndex != 4 {
		t.Fatalf("three-way BindingIndex closure = %+v", frozen)
	}
	if _, err := RestoreContextCompilationV1(canonical); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*ContextCompilationV1)
	}{
		{"fresh and reuse", func(value *ContextCompilationV1) {
			value.KnowledgeReuses[0].FreshRetrieval.BindingIndex = 1
		}},
		{"fresh and shortcut", func(value *ContextCompilationV1) {
			value.KnowledgeShortcuts[0].BindingIndex = 1
		}},
		{"reuse and shortcut", func(value *ContextCompilationV1) {
			value.KnowledgeShortcuts[0].BindingIndex = 3
		}},
		{"reuse and Memory result", func(value *ContextCompilationV1) {
			value.MemoryReads = append(
				value.MemoryReads,
				validMemoryReadEvidence(t, 5),
			)
			value.KnowledgeReuses[0].FreshRetrieval.BindingIndex = 2
			value.KnowledgeReuses[0].MemoryBindingIndex = 5
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := newValue()
			test.mutate(&value)
			if _, _, err := NewContextCompilationV1(value); err == nil {
				t.Fatal("accepted one BindingIndex in multiple protocol outcomes")
			}
		})
	}
}

func TestContextCompilationV1DynamicBindingIndexUnionIsUniqueAndOrdered(t *testing.T) {
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationRetrievalBelowWatermark
	knowledge := validKnowledgeRetrievalEvidence(t)
	knowledge.BindingIndex = 1
	memory := validMemoryReadEvidence(t, 2)
	value.KnowledgeRetrievals = []KnowledgeRetrievalEvidenceV1{knowledge}
	value.MemoryReads = []MemoryReadEvidenceV1{memory}
	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.KnowledgeRetrievals[0].BindingIndex != 1 ||
		frozen.MemoryReads[0].BindingIndex != 2 {
		t.Fatalf("dynamic BindingIndex order was lost: %+v", frozen)
	}
	restored, err := RestoreContextCompilationV1(canonical)
	if err != nil || restored.KnowledgeRetrievals[0].BindingIndex != 1 ||
		restored.MemoryReads[0].BindingIndex != 2 {
		t.Fatalf("dynamic BindingIndex round trip = %+v, %v", restored, err)
	}

	duplicate := value
	duplicate.MemoryReads = []MemoryReadEvidenceV1{validMemoryReadEvidence(t, 1)}
	if _, _, err := NewContextCompilationV1(duplicate); err == nil {
		t.Fatal("duplicate RAG/Memory BindingIndex was accepted")
	}
	duplicateShortcut := value
	shortcut := validKnowledgeShortcutEvidence(t)
	shortcut.BindingIndex = knowledge.BindingIndex
	duplicateShortcut.KnowledgeShortcuts = []KnowledgeShortcutEvidenceV1{shortcut}
	if _, _, err := NewContextCompilationV1(duplicateShortcut); err == nil {
		t.Fatal("duplicate retrieval/shortcut BindingIndex was accepted")
	}
	unsorted := value
	unsorted.MemoryReads = []MemoryReadEvidenceV1{
		validMemoryReadEvidence(t, 3),
		validMemoryReadEvidence(t, 2),
	}
	if _, _, err := NewContextCompilationV1(unsorted); err == nil {
		t.Fatal("non-increasing Memory BindingIndex evidence was accepted")
	}
}

func TestContextCompilationV1RejectsBrokenMemoryEvidence(t *testing.T) {
	valid := validMemoryReadEvidence(t, 2)
	tests := []struct {
		name   string
		mutate func(*MemoryReadEvidenceV1)
	}{
		{"config ref", func(value *MemoryReadEvidenceV1) { value.ConfigRef = "config" }},
		{"authority ref", func(value *MemoryReadEvidenceV1) { value.AuthorityCeilingRef = "authority" }},
		{"request digest", func(value *MemoryReadEvidenceV1) { value.RequestDigest = "request" }},
		{"snapshot", func(value *MemoryReadEvidenceV1) { value.Snapshot.Revision = 0 }},
		{"snapshot owner", func(value *MemoryReadEvidenceV1) { value.Snapshot.AgentID = "other-agent" }},
		{"scope", func(value *MemoryReadEvidenceV1) { value.Scope.TenantID = "other-tenant" }},
		{"evaluated time", func(value *MemoryReadEvidenceV1) { value.EvaluatedAtUnixMS = 0 }},
		{"candidate union", func(value *MemoryReadEvidenceV1) {
			value.SelectedEntries[0].Count = 1
		}},
		{"duplicate candidate", func(value *MemoryReadEvidenceV1) {
			value.SelectedEntries = append(value.SelectedEntries, value.SelectedEntries[0])
		}},
		{"output digest", func(value *MemoryReadEvidenceV1) { value.OutputDigest = compilationDigest("a") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := valid
			evidence.SelectedEntries = append(
				[]moduleapi.MemoryCandidateV1(nil),
				valid.SelectedEntries...,
			)
			test.mutate(&evidence)
			value := validCompilationBase()
			value.OriginalEstimateTokens = 700
			value.FinalEstimateTokens = 700
			value.StopReason = ContextCompilationRetrievalBelowWatermark
			value.MemoryReads = []MemoryReadEvidenceV1{evidence}
			if _, _, err := NewContextCompilationV1(value); err == nil {
				t.Fatal("accepted invalid Memory read evidence")
			}
		})
	}
}

func TestContextCompilationV1KnowledgeRetrievalPreservesThresholdPaths(t *testing.T) {
	evidence := validKnowledgeRetrievalEvidence(t)
	tests := []struct {
		name   string
		mutate func(*ContextCompilationV1)
	}{
		{
			name: "summary",
			mutate: func(value *ContextCompilationV1) {
				value.Summary = &ContextCompilationSummaryV1{
					SourceTurnDigests:    []string{compilationDigest("d")},
					Text:                 contextSummaryPrefixV1 + "[ASSISTANT]\nEarlier conversation.",
					BeforeEstimateTokens: 900,
					AfterEstimateTokens:  820,
				}
				value.FinalEstimateTokens = 820
				value.StopReason = ContextCompilationSummaryToWatermark
			},
		},
		{
			name: "drop",
			mutate: func(value *ContextCompilationV1) {
				value.OriginalEstimateTokens = 1100
				value.Drops = []ContextCompilationDropV1{{
					UnitKind:             ContextCompilationUnitHistoryTurn,
					UnitDigest:           compilationDigest("d"),
					BeforeEstimateTokens: 1100,
					AfterEstimateTokens:  800,
				}}
				value.FinalEstimateTokens = 800
				value.StopReason = ContextCompilationDropToWatermark
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validCompilationBase()
			value.KnowledgeRetrievals = []KnowledgeRetrievalEvidenceV1{evidence}
			test.mutate(&value)
			frozen, _, err := NewContextCompilationV1(value)
			if err != nil {
				t.Fatal(err)
			}
			if len(frozen.KnowledgeRetrievals) != 1 {
				t.Fatal("threshold path discarded Knowledge retrieval evidence")
			}
		})
	}
}

func TestContextCompilationV1RejectsBrokenKnowledgeRetrieval(t *testing.T) {
	valid := validKnowledgeRetrievalEvidence(t)
	tests := []struct {
		name   string
		mutate func(*ContextCompilationV1)
	}{
		{
			name: "not strictly increasing",
			mutate: func(value *ContextCompilationV1) {
				second := valid
				second.Hits = append([]moduleapi.KnowledgeHitV1{}, valid.Hits...)
				value.KnowledgeRetrievals = append(value.KnowledgeRetrievals, second)
			},
		},
		{
			name: "invalid config ref",
			mutate: func(value *ContextCompilationV1) {
				value.KnowledgeRetrievals[0].ConfigRef = "config"
			},
		},
		{
			name: "invalid authority ref",
			mutate: func(value *ContextCompilationV1) {
				value.KnowledgeRetrievals[0].AuthorityCeilingRef = "authority"
			},
		},
		{
			name: "invalid request digest",
			mutate: func(value *ContextCompilationV1) {
				value.KnowledgeRetrievals[0].RequestDigest = "request"
			},
		},
		{
			name: "source does not close output",
			mutate: func(value *ContextCompilationV1) {
				value.KnowledgeRetrievals[0].Source.Digest = compilationDigest("9")
			},
		},
		{
			name: "hit does not close output",
			mutate: func(value *ContextCompilationV1) {
				value.KnowledgeRetrievals[0].Hits[0].Text = "changed"
			},
		},
		{
			name: "output digest mismatch",
			mutate: func(value *ContextCompilationV1) {
				value.KnowledgeRetrievals[0].OutputDigest = compilationDigest("a")
			},
		},
		{
			name: "hit not visible to exact scope",
			mutate: func(value *ContextCompilationV1) {
				value.KnowledgeRetrievals[0].Scope.Agent.ID = "other-agent"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validCompilationBase()
			value.OriginalEstimateTokens = 700
			value.FinalEstimateTokens = 700
			value.StopReason = ContextCompilationRetrievalBelowWatermark
			value.KnowledgeRetrievals = []KnowledgeRetrievalEvidenceV1{valid}
			value.KnowledgeRetrievals[0].Hits = append(
				[]moduleapi.KnowledgeHitV1{}, valid.Hits...,
			)
			value.KnowledgeRetrievals[0].Hits[0].VisibleTo = append(
				[]moduleapi.KnowledgeScopeRuleV1{}, valid.Hits[0].VisibleTo...,
			)
			test.mutate(&value)
			if _, _, err := NewContextCompilationV1(value); err == nil {
				t.Fatal("accepted invalid Knowledge retrieval evidence")
			}
		})
	}
}

func TestContextCompilationV1RejectsWrongRetrievalStopReasonShape(t *testing.T) {
	tests := []struct {
		name  string
		value ContextCompilationV1
	}{
		{
			name: "below watermark without retrieval",
			value: func() ContextCompilationV1 {
				value := validCompilationBase()
				value.OriginalEstimateTokens = 700
				value.FinalEstimateTokens = 700
				value.StopReason = ContextCompilationNoEligibleSummary
				return value
			}(),
		},
		{
			name: "retrieval reason without retrieval",
			value: func() ContextCompilationV1 {
				value := validCompilationBase()
				value.OriginalEstimateTokens = 700
				value.FinalEstimateTokens = 700
				value.StopReason = ContextCompilationRetrievalBelowWatermark
				return value
			}(),
		},
		{
			name: "retrieval reason at watermark",
			value: func() ContextCompilationV1 {
				value := validCompilationBase()
				value.OriginalEstimateTokens = value.RestoreWatermarkTokens
				value.FinalEstimateTokens = value.OriginalEstimateTokens
				value.StopReason = ContextCompilationRetrievalBelowWatermark
				value.KnowledgeRetrievals = []KnowledgeRetrievalEvidenceV1{
					validKnowledgeRetrievalEvidence(t),
				}
				return value
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := NewContextCompilationV1(test.value); err == nil {
				t.Fatal("accepted invalid retrieval stop reason shape")
			}
		})
	}
}

func TestContextCompilationV1RejectsBrokenEvidence(t *testing.T) {
	valid := validCompilationBase()
	valid.OriginalEstimateTokens = 1100
	valid.Drops = []ContextCompilationDropV1{{
		UnitKind:             ContextCompilationUnitHistoryTurn,
		UnitDigest:           compilationDigest("d"),
		BeforeEstimateTokens: 1100,
		AfterEstimateTokens:  800,
	}}
	valid.FinalEstimateTokens = 800
	valid.StopReason = ContextCompilationDropToWatermark
	tests := []struct {
		name   string
		mutate func(*ContextCompilationV1)
	}{
		{
			name: "wrong watermark",
			mutate: func(value *ContextCompilationV1) {
				value.RestoreWatermarkTokens++
			},
		},
		{
			name: "broken estimate chain",
			mutate: func(value *ContextCompilationV1) {
				value.Drops[0].BeforeEstimateTokens--
			},
		},
		{
			name: "drop did not reduce",
			mutate: func(value *ContextCompilationV1) {
				value.Drops[0].AfterEstimateTokens =
					value.Drops[0].BeforeEstimateTokens
				value.FinalEstimateTokens = value.Drops[0].AfterEstimateTokens
			},
		},
		{
			name: "duplicate drop",
			mutate: func(value *ContextCompilationV1) {
				value.Drops = append(value.Drops, value.Drops[0])
			},
		},
		{
			name: "drop above watermark",
			mutate: func(value *ContextCompilationV1) {
				value.Drops[0].AfterEstimateTokens = 860
				value.FinalEstimateTokens = 860
			},
		},
		{
			name: "continued after watermark",
			mutate: func(value *ContextCompilationV1) {
				value.Drops[0].AfterEstimateTokens = 800
				value.Drops = append(value.Drops, ContextCompilationDropV1{
					UnitKind:             ContextCompilationUnitHistoryTurn,
					UnitDigest:           compilationDigest("e"),
					BeforeEstimateTokens: 800,
					AfterEstimateTokens:  700,
				})
				value.FinalEstimateTokens = 700
			},
		},
		{
			name: "invalid request digest",
			mutate: func(value *ContextCompilationV1) {
				value.FinalRequestDigest = "request"
			},
		},
		{
			name: "wrong reason shape",
			mutate: func(value *ContextCompilationV1) {
				value.StopReason = ContextCompilationSummaryToWatermark
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			value.Drops = append([]ContextCompilationDropV1{}, valid.Drops...)
			test.mutate(&value)
			if _, _, err := NewContextCompilationV1(value); err == nil {
				t.Fatal("accepted invalid context compilation")
			}
		})
	}
}

func TestRestoreContextCompilationV1RejectsUnknownField(t *testing.T) {
	value := validCompilationBase()
	value.FinalEstimateTokens = value.OriginalEstimateTokens
	value.StopReason = ContextCompilationNoEligibleSummary
	_, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	mutated := append([]byte(nil), canonical[:len(canonical)-1]...)
	mutated = append(mutated, []byte(`,"unknown":true}`)...)
	checked, err := moduleapi.CanonicalJSON(mutated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreContextCompilationV1(checked); err == nil {
		t.Fatal("accepted unknown context compilation field")
	}
}
