package moduleapi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestKnowledgeContextBindingV1RoundTripAndDynamicConfig(t *testing.T) {
	source := knowledgeTestSourceRef("a")
	input := KnowledgeContextBindingV1{
		SchemaVersion:     KnowledgeContextBindingSchemaV1,
		Source:            source,
		MaxHits:           8,
		MaxTotalTextBytes: 4096,
	}
	frozen, canonical, err := NewKnowledgeContextBindingV1(input)
	if err != nil {
		t.Fatalf("NewKnowledgeContextBindingV1: %v", err)
	}
	restored, err := RestoreKnowledgeContextBindingV1(canonical)
	if err != nil {
		t.Fatalf("RestoreKnowledgeContextBindingV1: %v", err)
	}
	if !reflect.DeepEqual(restored, frozen) {
		t.Fatalf("restored=%+v frozen=%+v", restored, frozen)
	}

	contextConfig, _, err := NewContextBindingConfigV1(ContextBindingConfigV1{
		SchemaVersion: ContextBindingConfigSchemaV1,
		Placement:     ContextPlacementUntrustedData,
		AllowSummary:  false,
		AllowDrop:     false,
		Parameters:    canonical,
	})
	if err != nil {
		t.Fatalf("NewContextBindingConfigV1: %v", err)
	}
	dynamic, err := RestoreKnowledgeContextBindingParametersV1(contextConfig)
	if err != nil {
		t.Fatalf("RestoreKnowledgeContextBindingParametersV1: %v", err)
	}
	if !reflect.DeepEqual(dynamic, input) {
		t.Fatalf("dynamic=%+v want %+v", dynamic, input)
	}
	contextConfig.Placement = ContextPlacementTrustedInstruction
	if _, err := RestoreKnowledgeContextBindingParametersV1(contextConfig); err == nil {
		t.Fatal("trusted placement was accepted for dynamic knowledge")
	}
}

func TestKnowledgeContextBindingV1LegacyCanonicalAndDigestCanary(t *testing.T) {
	input := KnowledgeContextBindingV1{
		SchemaVersion:     KnowledgeContextBindingSchemaV1,
		Source:            knowledgeTestSourceRef("a"),
		MaxHits:           8,
		MaxTotalTextBytes: 4096,
	}
	_, canonical, err := NewKnowledgeContextBindingV1(input)
	if err != nil {
		t.Fatalf("NewKnowledgeContextBindingV1: %v", err)
	}
	wantCanonical := []byte(`{"max_hits":8,"max_total_text_bytes":4096,"schema_version":"knowledge-context-binding/v1","source":{"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","id":"shared.docs","version":"1.0.0"}}`)
	if !bytes.Equal(canonical, wantCanonical) {
		t.Fatalf("legacy canonical changed:\n got %s\nwant %s", canonical, wantCanonical)
	}
	digest, err := ComputeKnowledgeContextBindingDigestV1(input)
	if err != nil {
		t.Fatalf("ComputeKnowledgeContextBindingDigestV1: %v", err)
	}
	const wantDigest = "5aabbf71183ec77a9b6524f0bd596e112f11b7b0b819084486d6a75e00e9d8f6"
	if digest != wantDigest {
		t.Fatalf("legacy digest=%s, want %s", digest, wantDigest)
	}
	if bytes.Contains(canonical, []byte(`"routing"`)) {
		t.Fatal("nil routing changed the legacy canonical wire")
	}
}

func TestKnowledgeContextBindingV1NilReuseCanonicalAndDigestCanary(t *testing.T) {
	input := KnowledgeContextBindingV1{
		SchemaVersion:     KnowledgeContextBindingSchemaV1,
		Source:            knowledgeTestSourceRef("a"),
		MaxHits:           8,
		MaxTotalTextBytes: 4096,
		Routing: &KnowledgeRoutingPolicyV1{
			SchemaVersion:  KnowledgeRoutingPolicySchemaV1,
			CollectionTags: []string{"architecture"},
			MatchTerms:     []string{"api"},
			MinMatchTerms:  1,
		},
	}
	_, canonical, err := NewKnowledgeContextBindingV1(input)
	if err != nil {
		t.Fatalf("NewKnowledgeContextBindingV1: %v", err)
	}
	wantCanonical := []byte(`{"max_hits":8,"max_total_text_bytes":4096,"routing":{"collection_tags":["architecture"],"match_terms":["api"],"min_match_terms":1,"schema_version":"knowledge-routing-policy/v1"},"schema_version":"knowledge-context-binding/v1","source":{"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","id":"shared.docs","version":"1.0.0"}}`)
	if !bytes.Equal(canonical, wantCanonical) {
		t.Fatalf("nil-Reuse canonical changed:\n got %s\nwant %s", canonical, wantCanonical)
	}
	digest, err := ComputeKnowledgeContextBindingDigestV1(input)
	if err != nil {
		t.Fatalf("ComputeKnowledgeContextBindingDigestV1: %v", err)
	}
	const wantDigest = "3f22001fbfedefad7e63c8a1766bfea361484e9682e9ad93bde3b9f3fdd09010"
	if digest != wantDigest {
		t.Fatalf("nil-Reuse digest=%s, want %s", digest, wantDigest)
	}
	if bytes.Contains(canonical, []byte(`"reuse"`)) ||
		bytes.Contains(canonical, []byte(`"reuse_ttl_seconds"`)) {
		t.Fatal("nil Reuse changed the legacy routing wire")
	}
}

func TestKnowledgeContextBindingV1FreezesRoutingDefensively(t *testing.T) {
	reuse := &KnowledgeReusePolicyV1{
		ExactQuestionOnly:    true,
		MinCategoryCount:     3,
		MinRepeatedTermCount: 2,
		MaxLookbackTurns:     24,
		ReuseTTLSeconds:      3600,
	}
	input := KnowledgeContextBindingV1{
		SchemaVersion:     KnowledgeContextBindingSchemaV1,
		Source:            knowledgeTestSourceRef("b"),
		MaxHits:           8,
		MaxTotalTextBytes: 4096,
		Routing: &KnowledgeRoutingPolicyV1{
			SchemaVersion:  KnowledgeRoutingPolicySchemaV1,
			CollectionTags: []string{"backend", "architecture"},
			MatchTerms:     []string{"server", "api"},
			MinMatchTerms:  1,
			Reuse:          reuse,
		},
	}
	frozen, canonical, err := NewKnowledgeContextBindingV1(input)
	if err != nil {
		t.Fatalf("NewKnowledgeContextBindingV1: %v", err)
	}
	if got := frozen.Routing.CollectionTags; !reflect.DeepEqual(got, []string{"architecture", "backend"}) {
		t.Fatalf("collection tags=%v, want sorted values", got)
	}
	if got := frozen.Routing.MatchTerms; !reflect.DeepEqual(got, []string{"api", "server"}) {
		t.Fatalf("match terms=%v, want sorted values", got)
	}

	input.Routing.CollectionTags[0] = "mutated"
	input.Routing.MatchTerms[0] = "mutated"
	reuse.MinCategoryCount = 99
	reuse.ReuseTTLSeconds = 99
	if frozen.Routing.CollectionTags[1] != "backend" ||
		frozen.Routing.MatchTerms[1] != "server" ||
		frozen.Routing.Reuse.MinCategoryCount != 3 ||
		frozen.Routing.Reuse.ReuseTTLSeconds != 3600 {
		t.Fatal("frozen routing aliases caller-owned data")
	}

	restored, err := RestoreKnowledgeContextBindingV1(canonical)
	if err != nil {
		t.Fatalf("RestoreKnowledgeContextBindingV1: %v", err)
	}
	if !reflect.DeepEqual(restored, frozen) {
		t.Fatalf("restored=%+v frozen=%+v", restored, frozen)
	}

	unsorted := frozen
	unsortedRouting := *frozen.Routing
	unsortedRouting.CollectionTags = []string{"backend", "architecture"}
	unsorted.Routing = &unsortedRouting
	unsortedCanonical, err := marshalCanonicalKnowledgeWire(unsorted, MaxConfigBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreKnowledgeContextBindingV1(unsortedCanonical); err == nil {
		t.Fatal("canonically encoded but semantically unsorted routing was accepted")
	}

	var object map[string]any
	if err := json.Unmarshal(canonical, &object); err != nil {
		t.Fatal(err)
	}
	object["routing"].(map[string]any)["unknown"] = true
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err = CanonicalJSON(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreKnowledgeContextBindingV1(unknown); err == nil {
		t.Fatal("routing with an unknown field was accepted")
	}
}

func TestKnowledgeContextBindingV1RejectsInvalidRouting(t *testing.T) {
	newPolicy := func() KnowledgeRoutingPolicyV1 {
		return KnowledgeRoutingPolicyV1{
			SchemaVersion:  KnowledgeRoutingPolicySchemaV1,
			CollectionTags: []string{"architecture"},
			MatchTerms:     []string{"api", "server"},
			MinMatchTerms:  1,
			Reuse: &KnowledgeReusePolicyV1{
				ExactQuestionOnly:    true,
				MinCategoryCount:     1,
				MinRepeatedTermCount: 1,
				MaxLookbackTurns:     1,
				ReuseTTLSeconds:      1,
			},
		}
	}
	tests := []struct {
		name   string
		mutate func(*KnowledgeRoutingPolicyV1)
	}{
		{"schema", func(value *KnowledgeRoutingPolicyV1) { value.SchemaVersion = "knowledge-routing-policy/v2" }},
		{"empty tags", func(value *KnowledgeRoutingPolicyV1) { value.CollectionTags = nil }},
		{"duplicate tags", func(value *KnowledgeRoutingPolicyV1) { value.CollectionTags = []string{"a", "a"} }},
		{"uppercase tag", func(value *KnowledgeRoutingPolicyV1) { value.CollectionTags = []string{"Architecture"} }},
		{"too many tags", func(value *KnowledgeRoutingPolicyV1) {
			value.CollectionTags = make([]string, MaxKnowledgeCollectionTagsV1+1)
		}},
		{"oversized tag", func(value *KnowledgeRoutingPolicyV1) {
			value.CollectionTags = []string{strings.Repeat("x", MaxKnowledgeRoutingValueBytesV1+1)}
		}},
		{"empty terms", func(value *KnowledgeRoutingPolicyV1) { value.MatchTerms = nil }},
		{"duplicate terms", func(value *KnowledgeRoutingPolicyV1) { value.MatchTerms = []string{"api", "api"} }},
		{"case duplicate terms", func(value *KnowledgeRoutingPolicyV1) { value.MatchTerms = []string{"API", "api"} }},
		{"uppercase term", func(value *KnowledgeRoutingPolicyV1) { value.MatchTerms = []string{"API"} }},
		{"too many terms", func(value *KnowledgeRoutingPolicyV1) { value.MatchTerms = make([]string, MaxKnowledgeMatchTermsV1+1) }},
		{"noncanonical term", func(value *KnowledgeRoutingPolicyV1) { value.MatchTerms = []string{" e\u0301 "} }},
		{"zero minimum matches", func(value *KnowledgeRoutingPolicyV1) { value.MinMatchTerms = 0 }},
		{"minimum exceeds terms", func(value *KnowledgeRoutingPolicyV1) { value.MinMatchTerms = 3 }},
		{"non-exact reuse", func(value *KnowledgeRoutingPolicyV1) { value.Reuse.ExactQuestionOnly = false }},
		{"zero category threshold", func(value *KnowledgeRoutingPolicyV1) { value.Reuse.MinCategoryCount = 0 }},
		{"category threshold overflow", func(value *KnowledgeRoutingPolicyV1) {
			value.Reuse.MinCategoryCount = MaxKnowledgeReuseCountThresholdV1 + 1
		}},
		{"zero repeated threshold", func(value *KnowledgeRoutingPolicyV1) { value.Reuse.MinRepeatedTermCount = 0 }},
		{"repeated threshold overflow", func(value *KnowledgeRoutingPolicyV1) {
			value.Reuse.MinRepeatedTermCount = MaxKnowledgeReuseCountThresholdV1 + 1
		}},
		{"zero lookback", func(value *KnowledgeRoutingPolicyV1) { value.Reuse.MaxLookbackTurns = 0 }},
		{"lookback overflow", func(value *KnowledgeRoutingPolicyV1) {
			value.Reuse.MaxLookbackTurns = MaxKnowledgeReuseLookbackTurnsV1 + 1
		}},
		{"zero reuse ttl", func(value *KnowledgeRoutingPolicyV1) {
			value.Reuse.ReuseTTLSeconds = 0
		}},
		{"reuse ttl overflow", func(value *KnowledgeRoutingPolicyV1) {
			value.Reuse.ReuseTTLSeconds = MaxKnowledgeReuseTTLSecondsV1 + 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := newPolicy()
			test.mutate(&policy)
			binding := KnowledgeContextBindingV1{
				SchemaVersion:     KnowledgeContextBindingSchemaV1,
				Source:            knowledgeTestSourceRef("c"),
				MaxHits:           1,
				MaxTotalTextBytes: 1,
				Routing:           &policy,
			}
			if _, _, err := NewKnowledgeContextBindingV1(binding); err == nil {
				t.Fatal("invalid routing was accepted")
			}
		})
	}
}

func TestKnowledgeAuthorityV1SortsScopesDefensivelyAndResolvesIntersection(t *testing.T) {
	source := knowledgeTestSourceRef("b")
	task := knowledgeTestDigest("c")
	rules := []KnowledgeScopeRuleV1{
		{TenantID: "tenant-1", WorkspaceID: "workspace.z", AgentID: "*", TaskInputRef: "*"},
		{TenantID: "tenant-1", WorkspaceID: "*", AgentID: "agent.main", TaskInputRef: task},
	}
	frozen, canonical, err := NewKnowledgeAuthorityCeilingV1(KnowledgeAuthorityCeilingV1{
		SchemaVersion:     KnowledgeAuthorityCeilingSchemaV1,
		Source:            source,
		AllowedScopes:     rules,
		MaxHits:           12,
		MaxTotalTextBytes: 8192,
	})
	if err != nil {
		t.Fatalf("NewKnowledgeAuthorityCeilingV1: %v", err)
	}
	if frozen.AllowedScopes[0].WorkspaceID != "*" {
		t.Fatalf("scopes were not sorted: %+v", frozen.AllowedScopes)
	}
	rules[0].TenantID = "mutated"
	if frozen.AllowedScopes[1].TenantID != "tenant-1" {
		t.Fatal("authority ceiling aliases caller scope slice")
	}
	restored, err := RestoreKnowledgeAuthorityCeilingV1(canonical)
	if err != nil {
		t.Fatalf("RestoreKnowledgeAuthorityCeilingV1: %v", err)
	}
	if !bytes.Equal(canonical, mustKnowledgeAuthorityCanonical(t, restored)) {
		t.Fatal("authority ceiling did not round trip")
	}

	scope := knowledgeTestScope(task)
	binding := KnowledgeContextBindingV1{
		SchemaVersion:     KnowledgeContextBindingSchemaV1,
		Source:            source,
		MaxHits:           6,
		MaxTotalTextBytes: 16384,
	}
	hits, textBytes, err := ResolveKnowledgeLimitsV1(binding, restored, scope)
	if err != nil {
		t.Fatalf("ResolveKnowledgeLimitsV1: %v", err)
	}
	if hits != 6 || textBytes != 8192 {
		t.Fatalf("limits=(%d,%d), want (6,8192)", hits, textBytes)
	}
	scope.TenantID = "tenant-2"
	if _, _, err := ResolveKnowledgeLimitsV1(binding, restored, scope); err == nil {
		t.Fatal("cross-tenant query was authorized")
	}
}

func TestKnowledgeScopeRulesRequireExactTenantAndCanonicalUniqueOrder(t *testing.T) {
	task := knowledgeTestDigest("d")
	scope := knowledgeTestScope(task)
	if !KnowledgeScopeAllowsV1(KnowledgeScopeRuleV1{
		TenantID: "tenant-1", WorkspaceID: "*", AgentID: "agent.main", TaskInputRef: "*",
	}, scope) {
		t.Fatal("valid exact-tenant wildcard rule did not match")
	}
	if KnowledgeScopeAllowsV1(KnowledgeScopeRuleV1{
		TenantID: "*", WorkspaceID: "*", AgentID: "*", TaskInputRef: "*",
	}, scope) {
		t.Fatal("tenant wildcard matched")
	}
	duplicate := KnowledgeAuthorityCeilingV1{
		SchemaVersion: KnowledgeAuthorityCeilingSchemaV1,
		Source:        knowledgeTestSourceRef("e"),
		AllowedScopes: []KnowledgeScopeRuleV1{
			{TenantID: "tenant-1", WorkspaceID: "*", AgentID: "*", TaskInputRef: "*"},
			{TenantID: "tenant-1", WorkspaceID: "*", AgentID: "*", TaskInputRef: "*"},
		},
		MaxHits: 1, MaxTotalTextBytes: 1,
	}
	if _, _, err := NewKnowledgeAuthorityCeilingV1(duplicate); err == nil {
		t.Fatal("duplicate authority scope was accepted")
	}
}

func TestKnowledgeRequestV1CanonicalDigestAndStrictRestore(t *testing.T) {
	request := knowledgeTestRequest(knowledgeTestSourceRef("f"), knowledgeTestDigest("1"))
	frozen, canonical, digest, err := NewKnowledgeContextRequestV1(request)
	if err != nil {
		t.Fatalf("NewKnowledgeContextRequestV1: %v", err)
	}
	restored, restoredDigest, err := RestoreKnowledgeContextRequestV1(canonical)
	if err != nil {
		t.Fatalf("RestoreKnowledgeContextRequestV1: %v", err)
	}
	if restored != frozen || restoredDigest != digest {
		t.Fatalf("request round trip mismatch: %+v %s", restored, restoredDigest)
	}
	computed, err := ComputeKnowledgeContextRequestDigestV1(request)
	if err != nil || computed != digest {
		t.Fatalf("ComputeKnowledgeContextRequestDigestV1=(%s,%v), want %s", computed, err, digest)
	}
	if _, _, err := RestoreKnowledgeContextRequestV1(append([]byte(" "), canonical...)); err == nil {
		t.Fatal("noncanonical request was accepted")
	}
	unknown := knowledgeTestAddUnknown(t, canonical)
	if _, _, err := RestoreKnowledgeContextRequestV1(unknown); err == nil {
		t.Fatal("request with unknown field was accepted")
	}
}

func TestKnowledgeChunkAndSourceV1CloseDigestsAndCanonicalOrder(t *testing.T) {
	ruleA := KnowledgeScopeRuleV1{TenantID: "tenant-1", WorkspaceID: "workspace.z", AgentID: "*", TaskInputRef: "*"}
	ruleB := KnowledgeScopeRuleV1{TenantID: "tenant-1", WorkspaceID: "*", AgentID: "agent.main", TaskInputRef: "*"}
	chunkB := knowledgeTestChunk(t, "doc.b", "chunk.2", "second", []KnowledgeScopeRuleV1{ruleA, ruleB})
	chunkA := knowledgeTestChunk(t, "doc.a", "chunk.1", "first", []KnowledgeScopeRuleV1{ruleA})
	if chunkB.VisibleTo[0].WorkspaceID != "*" {
		t.Fatalf("visible_to was not normalized: %+v", chunkB.VisibleTo)
	}
	computedChunk, err := ComputeKnowledgeChunkDigestV1(chunkB)
	if err != nil || computedChunk != chunkB.ChunkDigest {
		t.Fatalf("ComputeKnowledgeChunkDigestV1=(%s,%v), want %s", computedChunk, err, chunkB.ChunkDigest)
	}
	_, chunkCanonical, err := NewKnowledgeChunkV1(chunkB)
	if err != nil {
		t.Fatalf("NewKnowledgeChunkV1: %v", err)
	}
	if _, err := RestoreKnowledgeChunkV1(chunkCanonical); err != nil {
		t.Fatalf("RestoreKnowledgeChunkV1: %v", err)
	}
	tampered := chunkB
	tampered.Text = "tampered"
	if _, _, err := NewKnowledgeChunkV1(tampered); err == nil {
		t.Fatal("chunk text changed without digest closure")
	}

	frozen, canonical, ref, err := NewKnowledgeSourceV1(KnowledgeSourceV1{
		SchemaVersion: KnowledgeSourceSchemaV1,
		ID:            "shared.product.docs",
		Version:       "2026.08.03",
		Chunks:        []KnowledgeChunkV1{chunkB, chunkA},
	})
	if err != nil {
		t.Fatalf("NewKnowledgeSourceV1: %v", err)
	}
	if frozen.Chunks[0].Document.ID != "doc.a" {
		t.Fatalf("source chunks were not canonically sorted: %+v", frozen.Chunks)
	}
	restored, restoredRef, err := RestoreKnowledgeSourceV1(canonical)
	if err != nil {
		t.Fatalf("RestoreKnowledgeSourceV1: %v", err)
	}
	if restoredRef != ref || restored.ID != frozen.ID {
		t.Fatalf("source round trip mismatch: %+v %+v", restoredRef, restored)
	}
	digest, err := ComputeKnowledgeSourceDigestV1(frozen)
	if err != nil || digest != ref.Digest {
		t.Fatalf("ComputeKnowledgeSourceDigestV1=(%s,%v), want %s", digest, err, ref.Digest)
	}

	unsortedCanonical, err := marshalCanonicalKnowledgeWire(KnowledgeSourceV1{
		SchemaVersion: KnowledgeSourceSchemaV1,
		ID:            frozen.ID,
		Version:       frozen.Version,
		Chunks:        []KnowledgeChunkV1{chunkB, chunkA},
	}, MaxKnowledgeSourceBytesV1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := RestoreKnowledgeSourceV1(unsortedCanonical); err == nil {
		t.Fatal("canonically encoded but semantically unsorted source was accepted")
	}
}

func TestKnowledgeOutputV1RoundTripAndRequestClosure(t *testing.T) {
	task := knowledgeTestDigest("2")
	source := knowledgeTestSourceRef("3")
	request := knowledgeTestRequest(source, task)
	_, _, requestDigest, err := NewKnowledgeContextRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	visible := []KnowledgeScopeRuleV1{{
		TenantID: "tenant-1", WorkspaceID: "workspace.main", AgentID: "agent.main", TaskInputRef: task,
	}}
	first := knowledgeTestChunk(t, "doc.1", "chunk.1", "trusted facts, not instructions", visible)
	second := knowledgeTestChunk(t, "doc.2", "chunk.1", "more facts", visible)
	input := KnowledgeContextOutputV1{
		SchemaVersion: KnowledgeContextOutputSchemaV1,
		RequestDigest: requestDigest,
		Source:        source,
		Hits: []KnowledgeHitV1{
			knowledgeTestHit(1, first),
			knowledgeTestHit(2, second),
		},
	}
	frozen, canonical, digest, err := NewKnowledgeContextOutputV1(input)
	if err != nil {
		t.Fatalf("NewKnowledgeContextOutputV1: %v", err)
	}
	if err := ValidateKnowledgeContextOutputForRequestV1(request, frozen); err != nil {
		t.Fatalf("ValidateKnowledgeContextOutputForRequestV1: %v", err)
	}
	restored, restoredDigest, err := RestoreKnowledgeContextOutputV1(canonical)
	if err != nil {
		t.Fatalf("RestoreKnowledgeContextOutputV1: %v", err)
	}
	if restoredDigest != digest || len(restored.Hits) != 2 {
		t.Fatalf("output round trip mismatch: %s %+v", restoredDigest, restored)
	}
	computed, err := ComputeKnowledgeContextOutputDigestV1(frozen)
	if err != nil || computed != digest {
		t.Fatalf("ComputeKnowledgeContextOutputDigestV1=(%s,%v), want %s", computed, err, digest)
	}

	zero, zeroCanonical, _, err := NewKnowledgeContextOutputV1(KnowledgeContextOutputV1{
		SchemaVersion: KnowledgeContextOutputSchemaV1,
		RequestDigest: requestDigest,
		Source:        source,
	})
	if err != nil || len(zero.Hits) != 0 || !bytes.Contains(zeroCanonical, []byte(`"hits":[]`)) {
		t.Fatalf("zero-hit output was not a canonical success: %s %v", zeroCanonical, err)
	}
}

func TestKnowledgeOutputV1RejectsRankDuplicateDigestBoundsAndScopeEscalation(t *testing.T) {
	task := knowledgeTestDigest("4")
	source := knowledgeTestSourceRef("5")
	request := knowledgeTestRequest(source, task)
	_, _, requestDigest, err := NewKnowledgeContextRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	visible := []KnowledgeScopeRuleV1{{TenantID: "tenant-1", WorkspaceID: "*", AgentID: "*", TaskInputRef: "*"}}
	chunk := knowledgeTestChunk(t, "doc", "chunk", "text", visible)
	base := KnowledgeContextOutputV1{
		SchemaVersion: KnowledgeContextOutputSchemaV1,
		RequestDigest: requestDigest,
		Source:        source,
		Hits:          []KnowledgeHitV1{knowledgeTestHit(1, chunk)},
	}

	wrongRank := base
	wrongRank.Hits = append([]KnowledgeHitV1(nil), base.Hits...)
	wrongRank.Hits[0].Rank = 2
	if _, _, _, err := NewKnowledgeContextOutputV1(wrongRank); err == nil {
		t.Fatal("non-contiguous rank was accepted")
	}
	duplicate := base
	duplicate.Hits = []KnowledgeHitV1{knowledgeTestHit(1, chunk), knowledgeTestHit(2, chunk)}
	if _, _, _, err := NewKnowledgeContextOutputV1(duplicate); err == nil {
		t.Fatal("duplicate chunk was accepted")
	}
	missingDigest := base
	missingDigest.Hits = append([]KnowledgeHitV1(nil), base.Hits...)
	missingDigest.Hits[0].ChunkDigest = ""
	if _, _, _, err := NewKnowledgeContextOutputV1(missingDigest); err == nil {
		t.Fatal("missing provider chunk digest was repaired instead of rejected")
	}
	oversized := KnowledgeChunkV1{
		Document: chunk.Document, ChunkID: "large", Text: strings.Repeat("x", MaxKnowledgeHitTextBytesV1+1), VisibleTo: visible,
	}
	if _, _, err := NewKnowledgeChunkV1(oversized); err == nil {
		t.Fatal("oversized chunk was accepted")
	}

	crossTenant := knowledgeTestChunk(t, "doc.other", "chunk", "secret", []KnowledgeScopeRuleV1{{
		TenantID: "tenant-2", WorkspaceID: "*", AgentID: "*", TaskInputRef: "*",
	}})
	escalated := base
	escalated.Hits = []KnowledgeHitV1{knowledgeTestHit(1, crossTenant)}
	if err := ValidateKnowledgeContextOutputForRequestV1(request, escalated); err == nil {
		t.Fatal("cross-tenant hit was accepted for exact request scope")
	}

	lowLimit := request
	lowLimit.MaxHits = 1
	second := knowledgeTestChunk(t, "doc.second", "chunk", "more", visible)
	two := base
	two.Hits = []KnowledgeHitV1{knowledgeTestHit(1, chunk), knowledgeTestHit(2, second)}
	if err := ValidateKnowledgeContextOutputForRequestV1(lowLimit, two); err == nil {
		t.Fatal("output exceeding request hit limit was accepted")
	}
}

func TestKnowledgeRestoreRejectsUnknownAndNoncanonicalWires(t *testing.T) {
	source := knowledgeTestSourceRef("6")
	task := knowledgeTestDigest("7")
	binding := KnowledgeContextBindingV1{
		SchemaVersion: KnowledgeContextBindingSchemaV1, Source: source, MaxHits: 1, MaxTotalTextBytes: 1,
	}
	_, bindingCanonical, err := NewKnowledgeContextBindingV1(binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreKnowledgeContextBindingV1(knowledgeTestAddUnknown(t, bindingCanonical)); err == nil {
		t.Fatal("binding unknown field was accepted")
	}

	request := knowledgeTestRequest(source, task)
	_, requestCanonical, requestDigest, err := NewKnowledgeContextRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	output := KnowledgeContextOutputV1{
		SchemaVersion: KnowledgeContextOutputSchemaV1,
		RequestDigest: requestDigest,
		Source:        source,
		Hits:          []KnowledgeHitV1{},
	}
	_, outputCanonical, _, err := NewKnowledgeContextOutputV1(output)
	if err != nil {
		t.Fatal(err)
	}
	for name, canonical := range map[string][]byte{
		"request": requestCanonical,
		"output":  outputCanonical,
	} {
		t.Run(name, func(t *testing.T) {
			wire := knowledgeTestAddUnknown(t, canonical)
			switch name {
			case "request":
				if _, _, err := RestoreKnowledgeContextRequestV1(wire); err == nil {
					t.Fatal("unknown field was accepted")
				}
			case "output":
				if _, _, err := RestoreKnowledgeContextOutputV1(wire); err == nil {
					t.Fatal("unknown field was accepted")
				}
			}
		})
	}
}

func knowledgeTestDigest(character string) string {
	return strings.Repeat(character, SHA256HexLength)
}

func knowledgeTestSourceRef(character string) KnowledgeSourceRefV1 {
	return KnowledgeSourceRefV1{ID: "shared.docs", Version: "1.0.0", Digest: knowledgeTestDigest(character)}
}

func knowledgeTestScope(task string) KnowledgeQueryScopeV1 {
	return KnowledgeQueryScopeV1{
		TenantID:     "tenant-1",
		Workspace:    KnowledgeObjectRefV1{ID: "workspace.main", Version: "1", Digest: knowledgeTestDigest("8")},
		Agent:        KnowledgeObjectRefV1{ID: "agent.main", Version: "1", Digest: knowledgeTestDigest("9")},
		TaskInputRef: task,
	}
}

func knowledgeTestRequest(source KnowledgeSourceRefV1, task string) KnowledgeContextRequestV1 {
	return KnowledgeContextRequestV1{
		SchemaVersion:     KnowledgeContextRequestSchemaV1,
		Source:            source,
		Scope:             knowledgeTestScope(task),
		QueryText:         "How does the product work?",
		MaxHits:           4,
		MaxTotalTextBytes: 4096,
	}
}

func knowledgeTestChunk(
	t *testing.T,
	documentID string,
	chunkID string,
	text string,
	visible []KnowledgeScopeRuleV1,
) KnowledgeChunkV1 {
	t.Helper()
	chunk, _, err := NewKnowledgeChunkV1(KnowledgeChunkV1{
		Document: KnowledgeDocumentRefV1{
			ID: documentID, Version: "1", Digest: Digest("test.document/v1", []byte(documentID)),
		},
		ChunkID:   chunkID,
		Text:      text,
		VisibleTo: visible,
	})
	if err != nil {
		t.Fatalf("NewKnowledgeChunkV1: %v", err)
	}
	return chunk
}

func knowledgeTestHit(rank uint32, chunk KnowledgeChunkV1) KnowledgeHitV1 {
	return KnowledgeHitV1{
		Rank: rank, Document: chunk.Document, ChunkID: chunk.ChunkID,
		ChunkDigest: chunk.ChunkDigest, Text: chunk.Text,
		VisibleTo: append([]KnowledgeScopeRuleV1(nil), chunk.VisibleTo...),
	}
}

func mustKnowledgeAuthorityCanonical(
	t *testing.T,
	value KnowledgeAuthorityCeilingV1,
) []byte {
	t.Helper()
	_, canonical, err := NewKnowledgeAuthorityCeilingV1(value)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func knowledgeTestAddUnknown(t *testing.T, canonical []byte) []byte {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(canonical, &object); err != nil {
		t.Fatal(err)
	}
	object["unknown"] = true
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	withUnknown, err := CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return withUnknown
}
