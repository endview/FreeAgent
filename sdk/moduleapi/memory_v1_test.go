package moduleapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMemoryContextBindingV1FreezesAlgorithmConfigAndDynamicEnvelope(t *testing.T) {
	input := memoryTestBinding()
	input.Kinds = []MemoryEntryKindV1{
		MemoryEntryTaskSummary,
		MemoryEntryFact,
		MemoryEntryRepeatedTermCount,
		MemoryEntryCategoryCount,
	}
	input.CategoryRules = []MemoryCategoryRuleV1{
		{Key: "backend", Terms: []string{"service", "api"}},
		{Key: "frontend", Terms: []string{"ui", "css"}},
	}
	input.StopTerms = []string{"the", "a"}
	frozen, canonical, digest, err := NewMemoryContextBindingV1(input)
	if err != nil {
		t.Fatalf("NewMemoryContextBindingV1: %v", err)
	}
	if frozen.Kinds[0] != MemoryEntryCategoryCount || frozen.CategoryRules[0].Key != "backend" || frozen.StopTerms[0] != "a" {
		t.Fatalf("binding was not canonically ordered: %+v", frozen)
	}
	input.Kinds[0] = MemoryEntryPreference
	input.CategoryRules[0].Terms[0] = "mutated"
	input.StopTerms[0] = "mutated"
	if frozen.Kinds[len(frozen.Kinds)-1] != MemoryEntryTaskSummary || frozen.CategoryRules[0].Terms[0] != "api" || frozen.StopTerms[1] != "the" {
		t.Fatal("frozen binding aliases caller slices")
	}
	restored, restoredDigest, err := RestoreMemoryContextBindingV1(canonical)
	if err != nil {
		t.Fatalf("RestoreMemoryContextBindingV1: %v", err)
	}
	if restoredDigest != digest || !bytes.Equal(canonical, memoryMustBindingCanonical(t, restored)) {
		t.Fatal("binding did not round trip with stable algorithm digest")
	}
	computed, err := ComputeMemoryContextBindingDigestV1(restored)
	if err != nil || computed != digest {
		t.Fatalf("ComputeMemoryContextBindingDigestV1=(%s,%v), want %s", computed, err, digest)
	}

	config, _, err := NewContextBindingConfigV1(ContextBindingConfigV1{
		SchemaVersion: ContextBindingConfigSchemaV1,
		Placement:     ContextPlacementUntrustedData,
		AllowSummary:  false,
		AllowDrop:     false,
		Parameters:    canonical,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, gotDigest, err := RestoreMemoryContextBindingParametersV1(config); err != nil || gotDigest != digest {
		t.Fatalf("RestoreMemoryContextBindingParametersV1=(%s,%v), want %s", gotDigest, err, digest)
	}
	config.AllowDrop = true
	if _, _, err := RestoreMemoryContextBindingParametersV1(config); err == nil {
		t.Fatal("droppable dynamic memory binding was accepted")
	}
}

func TestMemoryBindingRejectsUnfrozenOrIrrelevantAlgorithmInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*MemoryContextBindingV1)
	}{
		{"duplicate kind", func(value *MemoryContextBindingV1) { value.Kinds = append(value.Kinds, MemoryEntryFact) }},
		{"missing category rules", func(value *MemoryContextBindingV1) { value.CategoryRules = nil }},
		{"stop term overlaps category", func(value *MemoryContextBindingV1) { value.StopTerms = []string{"api"} }},
		{"missing summary bound", func(value *MemoryContextBindingV1) { value.SummaryMaxTextBytes = 0 }},
		{"too-small summary bound", func(value *MemoryContextBindingV1) { value.SummaryMaxTextBytes = MinMemorySummaryTextBytesV1 - 1 }},
		{"unsafe ttl", func(value *MemoryContextBindingV1) { value.EntryTTLSeconds = MaxMemoryTTLSecondsV1 + 1 }},
		{"duplicate term", func(value *MemoryContextBindingV1) { value.CategoryRules[0].Terms = []string{"api", "api"} }},
		{"non nfc term", func(value *MemoryContextBindingV1) { value.CategoryRules[0].Terms = []string{"e\u0301"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := memoryTestBinding()
			test.mutate(&value)
			if _, _, _, err := NewMemoryContextBindingV1(value); err == nil {
				t.Fatal("invalid binding was accepted")
			}
		})
	}

	factOnly := MemoryContextBindingV1{
		SchemaVersion: MemoryContextBindingSchemaV1,
		Kinds:         []MemoryEntryKindV1{MemoryEntryFact},
		MaxItems:      1, MaxTotalTextBytes: 64,
	}
	if _, _, _, err := NewMemoryContextBindingV1(factOnly); err != nil {
		t.Fatalf("fact-only binding should not require update algorithm: %v", err)
	}
	factOnly.EntryTTLSeconds = 1
	if _, _, _, err := NewMemoryContextBindingV1(factOnly); err == nil {
		t.Fatal("irrelevant derived TTL was accepted")
	}
}

func TestMemoryAuthorityV1NarrowsExactOwnerScopeKindsAndLimits(t *testing.T) {
	binding := memoryTestBinding()
	ceilingInput := MemoryAuthorityCeilingV1{
		SchemaVersion:       MemoryAuthorityCeilingSchemaV1,
		TenantID:            "tenant-1",
		AgentID:             "agent.main",
		AllowedWorkspaceIDs: []string{"workspace.z", "workspace.main"},
		AllowedKinds: []MemoryEntryKindV1{
			MemoryEntryRepeatedTermCount,
			MemoryEntryTaskSummary,
			MemoryEntryFact,
			MemoryEntryCategoryCount,
		},
		MaxItems: 3, MaxTotalTextBytes: 2048,
	}
	frozen, canonical, err := NewMemoryAuthorityCeilingV1(ceilingInput)
	if err != nil {
		t.Fatalf("NewMemoryAuthorityCeilingV1: %v", err)
	}
	ceilingInput.AllowedWorkspaceIDs[0] = "mutated"
	ceilingInput.AllowedKinds[0] = MemoryEntryPreference
	if frozen.AllowedWorkspaceIDs[0] != "workspace.main" || frozen.AllowedKinds[0] != MemoryEntryCategoryCount {
		t.Fatal("frozen authority aliases caller slices")
	}
	restored, err := RestoreMemoryAuthorityCeilingV1(canonical)
	if err != nil {
		t.Fatalf("RestoreMemoryAuthorityCeilingV1: %v", err)
	}
	snapshotRef := memoryTestSnapshotRef(1)
	scope := memoryTestScope()
	resolved, err := ResolveMemoryAuthorityV1(binding, restored, snapshotRef, scope)
	if err != nil {
		t.Fatalf("ResolveMemoryAuthorityV1: %v", err)
	}
	if resolved.MaxItems != 3 || resolved.MaxTotalTextBytes != 2048 || len(resolved.Kinds) != len(binding.Kinds) {
		t.Fatalf("unexpected narrowed authority: %+v", resolved)
	}
	resolved.Kinds[0] = MemoryEntryPreference
	if restored.AllowedKinds[0] != MemoryEntryCategoryCount {
		t.Fatal("resolved kinds alias authority")
	}

	scope.TenantID = "tenant-2"
	if _, err := ResolveMemoryAuthorityV1(binding, restored, snapshotRef, scope); err == nil {
		t.Fatal("cross-tenant memory read was authorized")
	}
	scope = memoryTestScope()
	scope.Workspace.ID = "workspace.denied"
	if _, err := ResolveMemoryAuthorityV1(binding, restored, snapshotRef, scope); err == nil {
		t.Fatal("denied workspace was authorized")
	}
	scope = memoryTestScope()
	scope.Workspace.ID = "*"
	if _, err := ResolveMemoryAuthorityV1(binding, restored, snapshotRef, scope); err == nil {
		t.Fatal("workspace wildcard was accepted as an exact query identity")
	}
	scope = memoryTestScope()
	scope.Agent.ID = "*"
	if _, err := ResolveMemoryAuthorityV1(binding, restored, snapshotRef, scope); err == nil {
		t.Fatal("agent wildcard was accepted as an exact query identity")
	}
	restored.AllowedKinds = []MemoryEntryKindV1{MemoryEntryFact}
	if _, err := ResolveMemoryAuthorityV1(binding, restored, snapshotRef, memoryTestScope()); err == nil {
		t.Fatal("binding kind outside authority was accepted")
	}
	wildcardMixed := ceilingInput
	wildcardMixed.AllowedWorkspaceIDs = []string{"*", "workspace.main"}
	if _, _, err := NewMemoryAuthorityCeilingV1(wildcardMixed); err == nil {
		t.Fatal("workspace wildcard mixed with exact ID was accepted")
	}
}

func TestMemoryEntryV1UnionDigestVisibilityAndDefensiveCopy(t *testing.T) {
	fact := memoryTestFactEntry("fact.1", "language", "prefers Go", []string{"*"})
	frozen, canonical, err := NewMemoryEntryV1(fact)
	if err != nil {
		t.Fatalf("NewMemoryEntryV1: %v", err)
	}
	if frozen.EntryDigest == "" || !MemoryEntryVisibleToWorkspaceV1(frozen, "workspace.any") {
		t.Fatalf("fact was not closed or visible: %+v", frozen)
	}
	if MemoryEntryVisibleToWorkspaceV1(frozen, "*") {
		t.Fatal("wildcard sentinel was accepted as an exact querying Workspace")
	}
	fact.VisibleWorkspaceIDs[0] = "mutated"
	fact.SourceRefs[0] = memoryTestDigest("f")
	if frozen.VisibleWorkspaceIDs[0] != "*" || frozen.SourceRefs[0] == memoryTestDigest("f") {
		t.Fatal("frozen entry aliases caller slices")
	}
	restored, err := RestoreMemoryEntryV1(canonical)
	if err != nil || restored.EntryDigest != frozen.EntryDigest {
		t.Fatalf("RestoreMemoryEntryV1=%+v,%v", restored, err)
	}
	computed, err := ComputeMemoryEntryDigestV1(restored)
	if err != nil || computed != restored.EntryDigest {
		t.Fatalf("ComputeMemoryEntryDigestV1=(%s,%v), want %s", computed, err, restored.EntryDigest)
	}
	tampered := restored
	tampered.Text = "tampered"
	if _, _, err := NewMemoryEntryV1(tampered); err == nil {
		t.Fatal("entry content changed without digest closure")
	}

	count := memoryTestCountEntry("count.1", MemoryEntryCategoryCount, "backend", 3, "workspace.main")
	if _, _, err := NewMemoryEntryV1(count); err != nil {
		t.Fatalf("valid derived count rejected: %v", err)
	}
	count.Text = "not allowed"
	if _, _, err := NewMemoryEntryV1(count); err == nil {
		t.Fatal("count entry with text was accepted")
	}
	count = memoryTestCountEntry("count.2", MemoryEntryRepeatedTermCount, "api", 0, "workspace.main")
	if _, _, err := NewMemoryEntryV1(count); err == nil {
		t.Fatal("zero count was accepted")
	}
	count = memoryTestCountEntry("count.3", MemoryEntryRepeatedTermCount, "api", 1, "workspace.main")
	count.VisibleWorkspaceIDs = []string{"*"}
	if _, _, err := NewMemoryEntryV1(count); err == nil {
		t.Fatal("derived global entry was accepted")
	}
	count = memoryTestCountEntry("count.4", MemoryEntryRepeatedTermCount, "api", 1, "workspace.main")
	count.AlgorithmConfigDigest = ""
	if _, _, err := NewMemoryEntryV1(count); err == nil {
		t.Fatal("derived entry without algorithm config digest was accepted")
	}
	count = memoryTestCountEntry("count.5", MemoryEntryRepeatedTermCount, "api", MaxMemorySafeIntegerV1+1, "workspace.main")
	if _, _, err := NewMemoryEntryV1(count); err == nil {
		t.Fatal("unsafe count was accepted")
	}
}

func TestAgentMemorySnapshotV1CanonicalOrderMultiWorkspaceAndRevisionClosure(t *testing.T) {
	workspaceZ := memoryTestCountEntry("count.z", MemoryEntryCategoryCount, "backend", 2, "workspace.z")
	workspaceMain := memoryTestCountEntry("count.main", MemoryEntryCategoryCount, "backend", 3, "workspace.main")
	fact := memoryTestFactEntry("fact.1", "language", "Go", []string{"workspace.main"})
	input := AgentMemorySnapshotV1{
		SchemaVersion: MemorySnapshotSchemaV1,
		TenantID:      "tenant-1",
		AgentID:       "agent.main",
		Revision:      1,
		Entries:       []MemoryEntryV1{workspaceZ, fact, workspaceMain},
	}
	frozen, canonical, err := NewAgentMemorySnapshotV1(input)
	if err != nil {
		t.Fatalf("NewAgentMemorySnapshotV1: %v", err)
	}
	if frozen.Entries[0].VisibleWorkspaceIDs[0] != "workspace.main" || frozen.Entries[1].VisibleWorkspaceIDs[0] != "workspace.z" {
		t.Fatalf("snapshot did not use kind/key/visibility order: %+v", frozen.Entries)
	}
	input.Entries[0].Key = "mutated"
	if frozen.Entries[1].Key != "backend" {
		t.Fatal("snapshot aliases caller entry slice")
	}
	restored, err := RestoreAgentMemorySnapshotV1(canonical)
	if err != nil || len(restored.Entries) != 3 {
		t.Fatalf("RestoreAgentMemorySnapshotV1=%+v,%v", restored, err)
	}

	badGenesis := restored
	badGenesis.SourceAttemptID = "attempt-1"
	if _, _, err := NewAgentMemorySnapshotV1(badGenesis); err == nil {
		t.Fatal("genesis with source attempt was accepted")
	}
	next := restored
	next.Revision = 2
	next.PreviousSnapshotDigest = memoryTestDigest("a")
	next.SourceAttemptID = "attempt-2"
	if _, _, err := NewAgentMemorySnapshotV1(next); err != nil {
		t.Fatalf("valid non-genesis snapshot rejected: %v", err)
	}
	next.SourceAttemptID = ""
	if _, _, err := NewAgentMemorySnapshotV1(next); err == nil {
		t.Fatal("non-genesis snapshot without source attempt was accepted")
	}

	duplicate := restored
	duplicate.Entries = append([]MemoryEntryV1(nil), restored.Entries...)
	copyEntry := workspaceMain
	copyEntry.EntryID = "count.duplicate"
	copyEntry.Count = 4
	copyEntry.EntryDigest = ""
	duplicate.Entries = append(duplicate.Entries, copyEntry)
	if _, _, err := NewAgentMemorySnapshotV1(duplicate); err == nil {
		t.Fatal("duplicate kind/key/visibility identity was accepted")
	}
}

func TestMemoryRequestOutputV1CanonicalDigestsAndExactClosure(t *testing.T) {
	request := memoryTestRequest(t)
	request.Candidates = []MemoryCandidateV1{request.Candidates[1], request.Candidates[0]}
	frozen, canonical, requestDigest, err := NewMemoryContextRequestV1(request)
	if err != nil {
		t.Fatalf("NewMemoryContextRequestV1: %v", err)
	}
	if frozen.Candidates[0].Kind != MemoryEntryCategoryCount {
		t.Fatalf("candidates were not canonically sorted: %+v", frozen.Candidates)
	}
	request.Candidates[0].Key = "mutated"
	if frozen.Candidates[0].Key != "backend" {
		t.Fatal("frozen request aliases candidate slice")
	}
	restored, restoredDigest, err := RestoreMemoryContextRequestV1(canonical)
	if err != nil || restoredDigest != requestDigest {
		t.Fatalf("RestoreMemoryContextRequestV1=(%s,%v)", restoredDigest, err)
	}
	computed, err := ComputeMemoryContextRequestDigestV1(restored)
	if err != nil || computed != requestDigest {
		t.Fatalf("ComputeMemoryContextRequestDigestV1=(%s,%v), want %s", computed, err, requestDigest)
	}

	output := MemoryContextOutputV1{
		SchemaVersion: MemoryContextOutputSchemaV1,
		RequestDigest: requestDigest,
		Snapshot:      restored.Snapshot,
		SelectedEntryDigests: []string{
			restored.Candidates[1].EntryDigest,
			restored.Candidates[0].EntryDigest,
		},
	}
	frozenOutput, outputCanonical, outputDigest, err := NewMemoryContextOutputV1(output)
	if err != nil {
		t.Fatalf("NewMemoryContextOutputV1: %v", err)
	}
	if err := ValidateMemoryContextOutputForRequestV1(restored, frozenOutput); err != nil {
		t.Fatalf("ValidateMemoryContextOutputForRequestV1: %v", err)
	}
	output.SelectedEntryDigests[0] = memoryTestDigest("e")
	if frozenOutput.SelectedEntryDigests[0] == memoryTestDigest("e") {
		t.Fatal("frozen output aliases selected digest slice")
	}
	restoredOutput, restoredOutputDigest, err := RestoreMemoryContextOutputV1(outputCanonical)
	if err != nil || restoredOutputDigest != outputDigest || len(restoredOutput.SelectedEntryDigests) != 2 {
		t.Fatalf("RestoreMemoryContextOutputV1=(%s,%+v,%v)", restoredOutputDigest, restoredOutput, err)
	}
	if computed, err := ComputeMemoryContextOutputDigestV1(restoredOutput); err != nil || computed != outputDigest {
		t.Fatalf("ComputeMemoryContextOutputDigestV1=(%s,%v), want %s", computed, err, outputDigest)
	}

	zero, zeroCanonical, _, err := NewMemoryContextOutputV1(MemoryContextOutputV1{
		SchemaVersion: MemoryContextOutputSchemaV1,
		RequestDigest: requestDigest,
		Snapshot:      restored.Snapshot,
	})
	if err != nil || len(zero.SelectedEntryDigests) != 0 || !bytes.Contains(zeroCanonical, []byte(`"selected_entry_digests":[]`)) {
		t.Fatalf("zero-selection output was not canonical success: %s %v", zeroCanonical, err)
	}
}

func TestMemoryRequestOutputRejectsTimeOwnerMembershipDuplicatesAndLimits(t *testing.T) {
	request := memoryTestRequest(t)
	request.EvaluatedAtUnixMS = 0
	if _, _, _, err := NewMemoryContextRequestV1(request); err == nil {
		t.Fatal("request without frozen evaluation time was accepted")
	}
	request = memoryTestRequest(t)
	request.Snapshot.AgentID = "agent.other"
	if _, _, _, err := NewMemoryContextRequestV1(request); err == nil {
		t.Fatal("request snapshot owner differing from scope was accepted")
	}

	request = memoryTestRequest(t)
	request.Candidates = append(request.Candidates, request.Candidates[0])
	if _, _, _, err := NewMemoryContextRequestV1(request); err == nil {
		t.Fatal("duplicate candidate digest was accepted")
	}
	request = memoryTestRequest(t)
	frozen, _, requestDigest, err := NewMemoryContextRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	base := MemoryContextOutputV1{
		SchemaVersion:        MemoryContextOutputSchemaV1,
		RequestDigest:        requestDigest,
		Snapshot:             frozen.Snapshot,
		SelectedEntryDigests: []string{frozen.Candidates[0].EntryDigest},
	}
	outsider := base
	outsider.SelectedEntryDigests = []string{memoryTestDigest("f")}
	if err := ValidateMemoryContextOutputForRequestV1(frozen, outsider); err == nil {
		t.Fatal("selection outside request candidates was accepted")
	}
	duplicate := base
	duplicate.SelectedEntryDigests = []string{base.SelectedEntryDigests[0], base.SelectedEntryDigests[0]}
	if _, _, _, err := NewMemoryContextOutputV1(duplicate); err == nil {
		t.Fatal("duplicate selected digest was accepted")
	}
	wrongSnapshot := base
	wrongSnapshot.Snapshot.Revision++
	if err := ValidateMemoryContextOutputForRequestV1(frozen, wrongSnapshot); err == nil {
		t.Fatal("output for another snapshot was accepted")
	}
	lowItems := frozen
	lowItems.MaxItems = 1
	two := base
	two.RequestDigest, _ = ComputeMemoryContextRequestDigestV1(lowItems)
	two.SelectedEntryDigests = []string{frozen.Candidates[0].EntryDigest, frozen.Candidates[1].EntryDigest}
	if err := ValidateMemoryContextOutputForRequestV1(lowItems, two); err == nil {
		t.Fatal("output above exact request item limit was accepted")
	}
}

func TestMemoryStrictRestoreRejectsUnknownNoncanonicalAndUnsafeNumbers(t *testing.T) {
	binding := memoryTestBinding()
	_, bindingCanonical, _, err := NewMemoryContextBindingV1(binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := RestoreMemoryContextBindingV1(memoryTestAddUnknown(t, bindingCanonical)); err == nil {
		t.Fatal("binding unknown field was accepted")
	}
	if _, _, err := RestoreMemoryContextBindingV1(append([]byte(" "), bindingCanonical...)); err == nil {
		t.Fatal("noncanonical binding was accepted")
	}

	request := memoryTestRequest(t)
	_, requestCanonical, _, err := NewMemoryContextRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := RestoreMemoryContextRequestV1(memoryTestAddUnknown(t, requestCanonical)); err == nil {
		t.Fatal("request unknown field was accepted")
	}
	unsafe := request
	unsafe.EvaluatedAtUnixMS = MaxMemorySafeIntegerV1 + 1
	if _, _, _, err := NewMemoryContextRequestV1(unsafe); err == nil {
		t.Fatal("unsafe JSON integer was accepted")
	}

	entry := memoryTestFactEntry("fact.large", "large", strings.Repeat("x", MaxMemoryEntryTextBytesV1+1), []string{"*"})
	if _, _, err := NewMemoryEntryV1(entry); err == nil {
		t.Fatal("oversized memory entry was accepted")
	}
	entry = memoryTestFactEntry("fact.nfc", "nfc", "e\u0301", []string{"*"})
	if _, _, err := NewMemoryEntryV1(entry); err == nil {
		t.Fatal("non-NFC memory text was accepted")
	}

	snapshot, snapshotCanonical, err := NewAgentMemorySnapshotV1(AgentMemorySnapshotV1{
		SchemaVersion: MemorySnapshotSchemaV1,
		TenantID:      "tenant-1",
		AgentID:       "agent.main",
		Revision:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreAgentMemorySnapshotV1(memoryTestAddUnknown(t, snapshotCanonical)); err == nil {
		t.Fatal("snapshot unknown field was accepted")
	}
	if len(snapshot.Entries) != 0 || !bytes.Contains(snapshotCanonical, []byte(`"entries":[]`)) {
		t.Fatal("empty genesis snapshot was not frozen with an empty array")
	}
	unsafeRef := memoryTestSnapshotRef(MaxMemorySafeIntegerV1 + 1)
	if err := unsafeRef.Validate(); err == nil {
		t.Fatal("unsafe snapshot revision was accepted")
	}
}

func memoryTestBinding() MemoryContextBindingV1 {
	return MemoryContextBindingV1{
		SchemaVersion: MemoryContextBindingSchemaV1,
		Kinds: []MemoryEntryKindV1{
			MemoryEntryFact,
			MemoryEntryTaskSummary,
			MemoryEntryCategoryCount,
			MemoryEntryRepeatedTermCount,
		},
		MaxItems:            8,
		MaxTotalTextBytes:   4096,
		CategoryRules:       []MemoryCategoryRuleV1{{Key: "backend", Terms: []string{"api", "service"}}},
		StopTerms:           []string{"a", "the"},
		SummaryMaxTextBytes: 1024,
		EntryTTLSeconds:     86400,
	}
}

func memoryTestScope() MemoryQueryScopeV1 {
	return MemoryQueryScopeV1{
		TenantID: "tenant-1",
		Workspace: MemoryObjectRefV1{
			ID: "workspace.main", Version: "1", Digest: memoryTestDigest("1"),
		},
		Agent: MemoryObjectRefV1{
			ID: "agent.main", Version: "1", Digest: memoryTestDigest("2"),
		},
		TaskInputRef: memoryTestDigest("3"),
	}
}

func memoryTestSnapshotRef(revision uint64) MemorySnapshotRefV1 {
	return MemorySnapshotRefV1{
		TenantID: "tenant-1", AgentID: "agent.main", Revision: revision, Digest: memoryTestDigest("4"),
	}
}

func memoryTestFactEntry(id, key, text string, workspaces []string) MemoryEntryV1 {
	return MemoryEntryV1{
		EntryID:             id,
		Kind:                MemoryEntryFact,
		Key:                 key,
		Text:                text,
		VisibleWorkspaceIDs: append([]string(nil), workspaces...),
		SourceRefs:          []string{memoryTestDigest("5")},
		CreatedAtUnixMS:     1000,
	}
}

func memoryTestCountEntry(id string, kind MemoryEntryKindV1, key string, count uint64, workspace string) MemoryEntryV1 {
	bindingDigest, err := ComputeMemoryContextBindingDigestV1(memoryTestBinding())
	if err != nil {
		panic(err)
	}
	return MemoryEntryV1{
		EntryID:               id,
		Kind:                  kind,
		Key:                   key,
		Count:                 count,
		VisibleWorkspaceIDs:   []string{workspace},
		SourceRefs:            []string{memoryTestDigest("6"), memoryTestDigest("7")},
		AlgorithmVersion:      "memory-core-v1",
		AlgorithmConfigDigest: bindingDigest,
		CreatedAtUnixMS:       1000,
		ExpiresAtUnixMS:       2000,
	}
}

func memoryTestRequest(t *testing.T) MemoryContextRequestV1 {
	t.Helper()
	fact, _, err := NewMemoryEntryV1(memoryTestFactEntry("fact.1", "language", "prefers Go", []string{"workspace.main"}))
	if err != nil {
		t.Fatal(err)
	}
	count, _, err := NewMemoryEntryV1(memoryTestCountEntry("count.1", MemoryEntryCategoryCount, "backend", 3, "workspace.main"))
	if err != nil {
		t.Fatal(err)
	}
	factCandidate, err := NewMemoryCandidateV1(fact)
	if err != nil {
		t.Fatal(err)
	}
	countCandidate, err := NewMemoryCandidateV1(count)
	if err != nil {
		t.Fatal(err)
	}
	return MemoryContextRequestV1{
		SchemaVersion:     MemoryContextRequestSchemaV1,
		Snapshot:          memoryTestSnapshotRef(1),
		Scope:             memoryTestScope(),
		QueryText:         "design a Go backend",
		EvaluatedAtUnixMS: 1500,
		Candidates:        []MemoryCandidateV1{factCandidate, countCandidate},
		MaxItems:          2,
		MaxTotalTextBytes: 1024,
	}
}

func memoryMustBindingCanonical(t *testing.T, input MemoryContextBindingV1) []byte {
	t.Helper()
	_, canonical, _, err := NewMemoryContextBindingV1(input)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func memoryTestDigest(character string) string {
	return strings.Repeat(character, SHA256HexLength)
}

func memoryTestAddUnknown(t *testing.T, canonical []byte) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &object); err != nil {
		t.Fatal(err)
	}
	object["unknown"] = json.RawMessage(`true`)
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
