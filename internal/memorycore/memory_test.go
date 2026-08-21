package memorycore

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestFilterCandidatesAppliesExactScopeTTLAndLimitsInStablePriorityOrder(t *testing.T) {
	config := memoryConfig(t, []moduleapi.MemoryEntryKindV1{
		moduleapi.MemoryEntryFact,
		moduleapi.MemoryEntryPreference,
		moduleapi.MemoryEntryTaskSummary,
	}, 2, 5, nil, nil, 5, 60)
	entries := []moduleapi.MemoryEntryV1{
		seedTextEntry(t, "preference", moduleapi.MemoryEntryPreference, "editor", "ok", []string{"*"}, 200, 0),
		// A later entry outside the exact Workspace must neither leak nor block
		// an earlier evaluated_at view.
		derivedTextEntry(t, "other-summary", moduleapi.MemoryEntryTaskSummary, "other", "ctx", "workspace-other", 350, 1000),
		seedTextEntry(t, "old-fact", moduleapi.MemoryEntryFact, "old", "oversized", []string{"*"}, 200, 0),
		seedTextEntry(t, "expired", moduleapi.MemoryEntryFact, "expired", "bad", []string{"*"}, 100, 300),
		seedTextEntry(t, "new-fact", moduleapi.MemoryEntryFact, "new", "new", []string{"workspace-main"}, 250, 0),
	}
	snapshot := memorySnapshot(t, 1, "", "", entries)
	ref := snapshotRef(snapshot, digestByte('9'))
	scope := memoryScope("workspace-main")
	authority := memoryAuthority(
		scope,
		[]moduleapi.MemoryEntryKindV1{
			moduleapi.MemoryEntryFact,
			moduleapi.MemoryEntryPreference,
			moduleapi.MemoryEntryTaskSummary,
		},
		2,
		5,
	)

	first, resolved, err := FilterCandidates(snapshot, ref, scope, config, authority, 300)
	if err != nil {
		t.Fatalf("FilterCandidates: %v", err)
	}
	second, _, err := FilterCandidates(snapshot, ref, scope, config, authority, 300)
	if err != nil {
		t.Fatalf("FilterCandidates repeat: %v", err)
	}
	if fmt.Sprintf("%#v", first) != fmt.Sprintf("%#v", second) {
		t.Fatalf("same input produced unstable result\nfirst=%#v\nsecond=%#v", first, second)
	}
	if resolved.MaxItems != 2 || resolved.MaxTotalTextBytes != 5 {
		t.Fatalf("resolved=%+v", resolved)
	}
	if len(first) != 2 || first[0].Key != "new" || first[1].Key != "editor" {
		t.Fatalf("selected candidates=%+v, want new fact then preference", first)
	}
	var textBytes int
	for _, candidate := range first {
		textBytes += len(candidate.Text)
	}
	if textBytes != 5 {
		t.Fatalf("selected text bytes=%d, want 5", textBytes)
	}
}

func TestFilterCandidatesFailsClosedOnFutureEntryOrAuthorityMismatch(t *testing.T) {
	config := memoryConfig(
		t,
		[]moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryFact},
		4,
		1024,
		nil,
		nil,
		0,
		0,
	)
	future := seedTextEntry(
		t,
		"future",
		moduleapi.MemoryEntryFact,
		"future",
		"future",
		[]string{"*"},
		301,
		0,
	)
	snapshot := memorySnapshot(t, 1, "", "", []moduleapi.MemoryEntryV1{future})
	ref := snapshotRef(snapshot, digestByte('8'))
	scope := memoryScope("workspace-main")
	authority := memoryAuthority(
		scope,
		[]moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryFact},
		4,
		1024,
	)

	if _, _, err := FilterCandidates(snapshot, ref, scope, config, authority, 300); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("future entry error=%v, want ErrInvalidInput", err)
	}

	wrongRef := ref
	wrongRef.AgentID = "agent-other"
	if _, _, err := FilterCandidates(snapshot, wrongRef, scope, config, authority, 400); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("wrong ref error=%v, want ErrInvalidInput", err)
	}

	denied := authority
	denied.AllowedWorkspaceIDs = []string{"workspace-other"}
	if _, _, err := FilterCandidates(snapshot, ref, scope, config, denied, 400); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("denied workspace error=%v, want ErrInvalidInput", err)
	}

	if _, _, err := FilterCandidates(snapshot, ref, scope, config, authority, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero evaluated time error=%v, want ErrInvalidInput", err)
	}
}

func TestSelectKnowledgeReuseCounterProofV1SelectsStableThresholdProofWithoutAliasing(
	t *testing.T,
) {
	config := memoryConfig(
		t,
		[]moduleapi.MemoryEntryKindV1{
			moduleapi.MemoryEntryCategoryCount,
			moduleapi.MemoryEntryRepeatedTermCount,
		},
		8,
		1024,
		[]moduleapi.MemoryCategoryRuleV1{
			{Key: "architecture", Terms: []string{"design"}},
			{Key: "backend", Terms: []string{"api"}},
		},
		nil,
		0,
		60,
	)
	entries := []moduleapi.MemoryEntryV1{
		derivedCountEntryForConfig(t, "category-backend", moduleapi.MemoryEntryCategoryCount, "backend", 7, "workspace-main", 100, 2000, config),
		derivedCountEntryForConfig(t, "category-architecture", moduleapi.MemoryEntryCategoryCount, "architecture", 7, "workspace-main", 100, 2000, config),
		derivedCountEntryForConfig(t, "term-server", moduleapi.MemoryEntryRepeatedTermCount, "server", 5, "workspace-main", 100, 2000, config),
		derivedCountEntryForConfig(t, "term-api", moduleapi.MemoryEntryRepeatedTermCount, "api", 5, "workspace-main", 100, 2000, config),
	}
	snapshot := memorySnapshot(t, 1, "", "", entries)
	ref := snapshotRef(snapshot, digestByte('1'))
	scope := memoryScope("workspace-main")
	authority := memoryAuthority(
		scope,
		[]moduleapi.MemoryEntryKindV1{
			moduleapi.MemoryEntryCategoryCount,
			moduleapi.MemoryEntryRepeatedTermCount,
		},
		8,
		1024,
	)
	tags := []string{"backend", "architecture"}
	terms := []string{"server", "api"}

	first, found, err := SelectKnowledgeReuseCounterProofV1(
		snapshot, ref, scope, config, authority, 500,
		tags, terms, 7, 5,
	)
	if err != nil || !found {
		t.Fatalf("SelectKnowledgeReuseCounterProofV1=(%+v,%t,%v)", first, found, err)
	}
	if first.CategoryCounter.Key != "architecture" ||
		first.RepeatedTermCounter.Key != "api" {
		t.Fatalf("stable proof selected wrong ties: %+v", first)
	}
	if !reflect.DeepEqual(tags, []string{"backend", "architecture"}) ||
		!reflect.DeepEqual(terms, []string{"server", "api"}) {
		t.Fatalf("selector mutated caller keys: tags=%v terms=%v", tags, terms)
	}

	// Mutating one returned proof must not alias either the caller snapshot or
	// a later deterministic selection.
	first.CategoryCounter.VisibleWorkspaceIDs[0] = "mutated"
	first.CategoryCounter.SourceRefs[0] = digestByte('f')
	second, found, err := SelectKnowledgeReuseCounterProofV1(
		snapshot, ref, scope, config, authority, 500,
		tags, terms, 7, 5,
	)
	if err != nil || !found || second.CategoryCounter.Key != "architecture" ||
		second.CategoryCounter.VisibleWorkspaceIDs[0] != "workspace-main" ||
		snapshot.Entries[0].VisibleWorkspaceIDs[0] != "workspace-main" {
		t.Fatalf("proof aliases input or is not deterministic: %+v found=%t err=%v", second, found, err)
	}

	for name, minimums := range map[string][2]uint64{
		"category below threshold": {8, 5},
		"term below threshold":     {7, 6},
	} {
		t.Run(name, func(t *testing.T) {
			proof, found, err := SelectKnowledgeReuseCounterProofV1(
				snapshot, ref, scope, config, authority, 500,
				tags, terms, minimums[0], minimums[1],
			)
			if err != nil || found || proof.CategoryCounter.EntryDigest != "" ||
				proof.RepeatedTermCounter.EntryDigest != "" {
				t.Fatalf("threshold miss=(%+v,%t,%v), want zero,false,nil", proof, found, err)
			}
		})
	}
}

func TestSelectKnowledgeReuseCounterProofV1RejectsStaleOrOutOfScopeCounters(
	t *testing.T,
) {
	config := memoryConfig(
		t,
		[]moduleapi.MemoryEntryKindV1{
			moduleapi.MemoryEntryCategoryCount,
			moduleapi.MemoryEntryRepeatedTermCount,
		},
		8,
		1024,
		[]moduleapi.MemoryCategoryRuleV1{{Key: "architecture", Terms: []string{"design"}}},
		nil,
		0,
		60,
	)
	scope := memoryScope("workspace-main")
	authority := memoryAuthority(
		scope,
		[]moduleapi.MemoryEntryKindV1{
			moduleapi.MemoryEntryCategoryCount,
			moduleapi.MemoryEntryRepeatedTermCount,
		},
		8,
		1024,
	)
	baseCategory := derivedCountEntryForConfig(t, "category", moduleapi.MemoryEntryCategoryCount, "architecture", 3, "workspace-main", 100, 2000, config)
	baseTerm := derivedCountEntryForConfig(t, "term", moduleapi.MemoryEntryRepeatedTermCount, "api", 2, "workspace-main", 100, 2000, config)

	tests := []struct {
		name     string
		mutate   func(*moduleapi.MemoryEntryV1)
		tags     []string
		terms    []string
		evaluate uint64
	}{
		{name: "wrong key", tags: []string{"backend"}, terms: []string{"api"}, evaluate: 500},
		{name: "wrong workspace", tags: []string{"architecture"}, terms: []string{"api"}, evaluate: 500, mutate: func(entry *moduleapi.MemoryEntryV1) {
			entry.VisibleWorkspaceIDs = []string{"workspace-other"}
		}},
		{name: "expired", tags: []string{"architecture"}, terms: []string{"api"}, evaluate: 500, mutate: func(entry *moduleapi.MemoryEntryV1) {
			entry.ExpiresAtUnixMS = 500
		}},
		{name: "wrong algorithm", tags: []string{"architecture"}, terms: []string{"api"}, evaluate: 500, mutate: func(entry *moduleapi.MemoryEntryV1) {
			entry.AlgorithmVersion = "freeagent.memory-successful-revision/other"
		}},
		{name: "wrong config", tags: []string{"architecture"}, terms: []string{"api"}, evaluate: 500, mutate: func(entry *moduleapi.MemoryEntryV1) {
			entry.AlgorithmConfigDigest = digestByte('e')
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			category := baseCategory
			category.VisibleWorkspaceIDs = append([]string(nil), baseCategory.VisibleWorkspaceIDs...)
			category.SourceRefs = append([]string(nil), baseCategory.SourceRefs...)
			if test.mutate != nil {
				test.mutate(&category)
				category.EntryDigest = ""
				var err error
				category, _, err = moduleapi.NewMemoryEntryV1(category)
				if err != nil {
					t.Fatalf("refreeze category: %v", err)
				}
			}
			snapshot := memorySnapshot(t, 1, "", "", []moduleapi.MemoryEntryV1{category, baseTerm})
			proof, found, err := SelectKnowledgeReuseCounterProofV1(
				snapshot,
				snapshotRef(snapshot, digestByte('2')),
				scope,
				config,
				authority,
				test.evaluate,
				test.tags,
				test.terms,
				3,
				2,
			)
			if err != nil || found || proof.CategoryCounter.EntryDigest != "" {
				t.Fatalf("stale/out-of-scope proof=(%+v,%t,%v), want zero,false,nil", proof, found, err)
			}
		})
	}
}

func TestSelectKnowledgeReuseCounterProofV1HonorsMaxItemsAndFailsClosed(
	t *testing.T,
) {
	config := memoryConfig(
		t,
		[]moduleapi.MemoryEntryKindV1{
			moduleapi.MemoryEntryCategoryCount,
			moduleapi.MemoryEntryRepeatedTermCount,
		},
		1,
		1024,
		[]moduleapi.MemoryCategoryRuleV1{{Key: "architecture", Terms: []string{"design"}}},
		nil,
		0,
		60,
	)
	entries := []moduleapi.MemoryEntryV1{
		derivedCountEntryForConfig(t, "category", moduleapi.MemoryEntryCategoryCount, "architecture", 3, "workspace-main", 100, 2000, config),
		derivedCountEntryForConfig(t, "term", moduleapi.MemoryEntryRepeatedTermCount, "api", 2, "workspace-main", 100, 2000, config),
	}
	snapshot := memorySnapshot(t, 1, "", "", entries)
	ref := snapshotRef(snapshot, digestByte('3'))
	scope := memoryScope("workspace-main")
	authority := memoryAuthority(
		scope,
		[]moduleapi.MemoryEntryKindV1{
			moduleapi.MemoryEntryCategoryCount,
			moduleapi.MemoryEntryRepeatedTermCount,
		},
		8,
		1024,
	)
	proof, found, err := SelectKnowledgeReuseCounterProofV1(
		snapshot, ref, scope, config, authority, 500,
		[]string{"architecture"}, []string{"api"}, 3, 2,
	)
	if err != nil || found || proof.CategoryCounter.EntryDigest != "" {
		t.Fatalf("MaxItems proof=(%+v,%t,%v), want zero,false,nil", proof, found, err)
	}

	if _, _, err := SelectKnowledgeReuseCounterProofV1(
		snapshot, ref, scope, config, authority, 500,
		[]string{"Architecture"}, []string{"api"}, 3, 2,
	); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("noncanonical key error=%v, want ErrInvalidInput", err)
	}
	if _, _, err := SelectKnowledgeReuseCounterProofV1(
		snapshot, ref, scope, config, authority, 500,
		[]string{"architecture"}, []string{"api"}, 0, 2,
	); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("zero threshold error=%v, want ErrInvalidInput", err)
	}
	invalidConfig := config
	invalidConfig.MaxItems = 0
	if _, _, err := SelectKnowledgeReuseCounterProofV1(
		snapshot, ref, scope, invalidConfig, authority, 500,
		[]string{"architecture"}, []string{"api"}, 3, 2,
	); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid Config error=%v, want ErrInvalidInput", err)
	}
}

func TestBuildSuccessfulRevisionIsDeterministicBoundedAndDoesNotCopyDomainBody(t *testing.T) {
	fact := seedTextEntry(
		t,
		"confirmed-fact",
		moduleapi.MemoryEntryFact,
		"timezone",
		"Asia/Shanghai",
		[]string{"*"},
		100,
		0,
	)
	preference := seedTextEntry(
		t,
		"confirmed-preference",
		moduleapi.MemoryEntryPreference,
		"style",
		"concise",
		[]string{"workspace-main"},
		100,
		0,
	)
	current := memorySnapshot(t, 1, "", "", []moduleapi.MemoryEntryV1{preference, fact})
	config := memoryConfig(
		t,
		[]moduleapi.MemoryEntryKindV1{
			moduleapi.MemoryEntryTaskSummary,
			moduleapi.MemoryEntryCategoryCount,
			moduleapi.MemoryEntryRepeatedTermCount,
		},
		2, // Retrieval limit must not become the durable top-K limit.
		1024,
		[]moduleapi.MemoryCategoryRuleV1{{Key: "engineering", Terms: []string{"api", "go"}}},
		[]string{"and", "the"},
		80,
		60,
	)
	input := successfulInput(
		current,
		digestByte('d'),
		"attempt-one",
		1000,
		"workspace-main",
		"Go go builds APIs. This task sentence must not be copied.",
		"The agent tests go APIs. DOMAIN_BODY_MUST_NOT_BE_COPIED.",
		config,
		'a',
		'b',
	)

	first, firstCanonical, err := BuildSuccessfulRevision(current, input)
	if err != nil {
		t.Fatalf("BuildSuccessfulRevision: %v", err)
	}
	second, secondCanonical, err := BuildSuccessfulRevision(current, input)
	if err != nil {
		t.Fatalf("BuildSuccessfulRevision repeat: %v", err)
	}
	if !bytes.Equal(firstCanonical, secondCanonical) || fmt.Sprintf("%#v", first) != fmt.Sprintf("%#v", second) {
		t.Fatal("same frozen input did not produce byte-identical revision")
	}
	if first.Revision != 2 || first.PreviousSnapshotDigest != digestByte('d') || first.SourceAttemptID != "attempt-one" {
		t.Fatalf("revision closure=%+v", first)
	}
	if entryByIdentity(first.Entries, moduleapi.MemoryEntryFact, "timezone", "*").EntryDigest != fact.EntryDigest ||
		entryByIdentity(first.Entries, moduleapi.MemoryEntryPreference, "style", "workspace-main").EntryDigest != preference.EntryDigest {
		t.Fatal("confirmed fact/preference were not preserved exactly")
	}

	summary := entryByIdentity(first.Entries, moduleapi.MemoryEntryTaskSummary, "attempt-one", "workspace-main")
	if summary.Text == "" || len(summary.Text) > int(config.SummaryMaxTextBytes) ||
		strings.Contains(summary.Text, "DOMAIN_BODY") ||
		strings.Contains(summary.Text, "must not be copied") {
		t.Fatalf("extractive summary=%q", summary.Text)
	}
	if summary.ExpiresAtUnixMS != 61000 || summary.CreatedAtUnixMS != 1000 {
		t.Fatalf("summary times=%d/%d", summary.CreatedAtUnixMS, summary.ExpiresAtUnixMS)
	}
	if len(summary.VisibleWorkspaceIDs) != 1 || summary.VisibleWorkspaceIDs[0] != "workspace-main" {
		t.Fatalf("summary visibility=%v", summary.VisibleWorkspaceIDs)
	}

	category := entryByIdentity(first.Entries, moduleapi.MemoryEntryCategoryCount, "engineering", "workspace-main")
	if category.Count != 1 {
		t.Fatalf("category count=%d, want one successful task", category.Count)
	}
	goCount := entryByIdentity(first.Entries, moduleapi.MemoryEntryRepeatedTermCount, "go", "workspace-main")
	if goCount.Count != 2 {
		t.Fatalf("go count=%d, want 2 task occurrences", goCount.Count)
	}
	if hasEntry(first.Entries, moduleapi.MemoryEntryRepeatedTermCount, "the", "workspace-main") {
		t.Fatal("case-folded stop term was retained")
	}
	for _, entry := range first.Entries {
		if isDerived(entry.Kind) && entry.AlgorithmVersion != SuccessfulRevisionAlgorithmV1 {
			t.Fatalf("derived entry algorithm=%q", entry.AlgorithmVersion)
		}
	}
}

func TestBuildSuccessfulRevisionRebasesCountsAndKeepsWorkspacesIndependent(t *testing.T) {
	current := memorySnapshot(t, 1, "", "", nil)
	config := memoryConfig(
		t,
		[]moduleapi.MemoryEntryKindV1{
			moduleapi.MemoryEntryTaskSummary,
			moduleapi.MemoryEntryCategoryCount,
			moduleapi.MemoryEntryRepeatedTermCount,
		},
		8,
		4096,
		[]moduleapi.MemoryCategoryRuleV1{{Key: "coding", Terms: []string{"go"}}},
		nil,
		64,
		3600,
	)
	firstInput := successfulInput(current, digestByte('1'), "attempt-one", 1000, "workspace-main", "go go", "go", config, '2', '3')
	first, _, err := BuildSuccessfulRevision(current, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := successfulInput(first, digestByte('4'), "attempt-two", 2000, "workspace-main", "go", "go go", config, '5', '6')
	second, _, err := BuildSuccessfulRevision(first, secondInput)
	if err != nil {
		t.Fatalf("same-workspace rebase: %v", err)
	}
	if got := entryByIdentity(second.Entries, moduleapi.MemoryEntryRepeatedTermCount, "go", "workspace-main").Count; got != 3 {
		t.Fatalf("rebased go count=%d, want 3 task occurrences", got)
	}
	if got := entryByIdentity(second.Entries, moduleapi.MemoryEntryCategoryCount, "coding", "workspace-main").Count; got != 2 {
		t.Fatalf("rebased category count=%d, want 2", got)
	}

	thirdInput := successfulInput(second, digestByte('7'), "attempt-three", 3000, "workspace-other", "go go", "go", config, '8', '9')
	third, _, err := BuildSuccessfulRevision(second, thirdInput)
	if err != nil {
		t.Fatalf("cross-workspace rebase: %v", err)
	}
	mainCount := entryByIdentity(third.Entries, moduleapi.MemoryEntryRepeatedTermCount, "go", "workspace-main")
	otherCount := entryByIdentity(third.Entries, moduleapi.MemoryEntryRepeatedTermCount, "go", "workspace-other")
	if mainCount.Count != 3 || otherCount.Count != 2 || mainCount.EntryDigest == otherCount.EntryDigest {
		t.Fatalf("workspace-isolated counts main=%+v other=%+v", mainCount, otherCount)
	}
	if len(entriesByKind(third.Entries, moduleapi.MemoryEntryTaskSummary)) != 3 {
		t.Fatalf("task summary count=%d, want 3", len(entriesByKind(third.Entries, moduleapi.MemoryEntryTaskSummary)))
	}
}

func TestBuildSuccessfulRevisionDoesNotMixCountsAcrossConfigDigests(t *testing.T) {
	current := memorySnapshot(t, 1, "", "", nil)
	firstConfig := memoryConfig(
		t,
		[]moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryRepeatedTermCount},
		8,
		4096,
		nil,
		nil,
		0,
		3600,
	)
	first, _, err := BuildSuccessfulRevision(
		current,
		successfulInput(
			current,
			digestByte('1'),
			"attempt-one",
			1000,
			"workspace-main",
			"go go",
			"go",
			firstConfig,
			'2',
			'3',
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	firstCount := entryByIdentity(
		first.Entries,
		moduleapi.MemoryEntryRepeatedTermCount,
		"go",
		"workspace-main",
	)
	if firstCount.Count != 2 {
		t.Fatalf("first count=%d, want 2 task occurrences", firstCount.Count)
	}

	secondConfig := firstConfig
	secondConfig.MaxItems = 7
	secondConfig, _, _, err = moduleapi.NewMemoryContextBindingV1(secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := BuildSuccessfulRevision(
		first,
		successfulInput(
			first,
			digestByte('4'),
			"attempt-two",
			2000,
			"workspace-main",
			"go",
			"go",
			secondConfig,
			'5',
			'6',
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	secondCount := entryByIdentity(
		second.Entries,
		moduleapi.MemoryEntryRepeatedTermCount,
		"go",
		"workspace-main",
	)
	if secondCount.Count != 1 {
		t.Fatalf("count after config change=%d, want current task delta 1", secondCount.Count)
	}
	wantDigest, err := moduleapi.ComputeMemoryContextBindingDigestV1(secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	if secondCount.AlgorithmConfigDigest != wantDigest ||
		secondCount.CreatedAtUnixMS != 2000 ||
		containsString(secondCount.SourceRefs, firstCount.SourceRefs[0]) {
		t.Fatalf("count was not reset to the new config series: %+v", secondCount)
	}
}

func TestBuildSuccessfulRevisionUsesAlgorithmTopKNotRetrievalLimit(t *testing.T) {
	current := memorySnapshot(t, 1, "", "", nil)
	config := memoryConfig(
		t,
		[]moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryRepeatedTermCount},
		1,
		1024,
		nil,
		nil,
		0,
		60,
	)
	terms := make([]string, 70)
	for index := range terms {
		terms[index] = fmt.Sprintf("z%03d", index)
	}
	input := successfulInput(
		current,
		digestByte('a'),
		"attempt-top-k",
		1000,
		"workspace-main",
		strings.Join(terms, " "),
		"z000",
		config,
		'b',
		'c',
	)
	next, _, err := BuildSuccessfulRevision(current, input)
	if err != nil {
		t.Fatal(err)
	}
	repeated := entriesByKind(next.Entries, moduleapi.MemoryEntryRepeatedTermCount)
	if len(repeated) != MaxRepeatedTermsPerWorkspaceV1 {
		t.Fatalf("repeated terms=%d, want hard top-K %d", len(repeated), MaxRepeatedTermsPerWorkspaceV1)
	}
	if !hasEntry(repeated, moduleapi.MemoryEntryRepeatedTermCount, "z000", "workspace-main") ||
		hasEntry(repeated, moduleapi.MemoryEntryRepeatedTermCount, "z069", "workspace-main") {
		t.Fatalf("stable top-K retained wrong ties: %+v", repeated)
	}
}

func TestBuildSuccessfulRevisionPrunesSummariesAndCategoriesWithStableTies(t *testing.T) {
	entries := make([]moduleapi.MemoryEntryV1, MaxTaskSummariesPerWorkspaceV1)
	for index := range entries {
		entries[index] = derivedTextEntry(
			t,
			fmt.Sprintf("summary-%02d", index),
			moduleapi.MemoryEntryTaskSummary,
			fmt.Sprintf("attempt-old-%02d", index),
			fmt.Sprintf("old summary %02d", index),
			"workspace-main",
			uint64(index+1),
			100000,
		)
	}
	current := memorySnapshot(t, 1, "", "", entries)
	rules := make([]moduleapi.MemoryCategoryRuleV1, MaxCategoryCountsPerWorkspaceV1+1)
	terms := make([]string, len(rules))
	for index := range rules {
		terms[index] = fmt.Sprintf("term%02d", index)
		rules[index] = moduleapi.MemoryCategoryRuleV1{
			Key:   fmt.Sprintf("category-%02d", index),
			Terms: []string{terms[index]},
		}
	}
	config := memoryConfig(
		t,
		[]moduleapi.MemoryEntryKindV1{
			moduleapi.MemoryEntryTaskSummary,
			moduleapi.MemoryEntryCategoryCount,
		},
		1,
		4096,
		rules,
		nil,
		64,
		60,
	)
	input := successfulInput(current, digestByte('d'), "attempt-new", 100, "workspace-main", strings.Join(terms, " "), "done", config, 'e', 'f')
	next, _, err := BuildSuccessfulRevision(current, input)
	if err != nil {
		t.Fatal(err)
	}
	summaries := entriesByKind(next.Entries, moduleapi.MemoryEntryTaskSummary)
	if len(summaries) != MaxTaskSummariesPerWorkspaceV1 ||
		hasEntry(summaries, moduleapi.MemoryEntryTaskSummary, "attempt-old-00", "workspace-main") ||
		!hasEntry(summaries, moduleapi.MemoryEntryTaskSummary, "attempt-new", "workspace-main") {
		t.Fatalf("summary top-K=%+v", summaries)
	}
	categories := entriesByKind(next.Entries, moduleapi.MemoryEntryCategoryCount)
	if len(categories) != MaxCategoryCountsPerWorkspaceV1 ||
		!hasEntry(categories, moduleapi.MemoryEntryCategoryCount, "category-00", "workspace-main") ||
		hasEntry(categories, moduleapi.MemoryEntryCategoryCount, "category-32", "workspace-main") {
		t.Fatalf("category stable top-K=%+v", categories)
	}
}

func TestBuildSuccessfulRevisionRejectsCountRevisionTTLAndSnapshotOverflow(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		config := memoryConfig(t, []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryRepeatedTermCount}, 4, 1024, nil, nil, 0, 60)
		count := derivedCountEntry(t, "full", moduleapi.MemoryEntryRepeatedTermCount, "go", moduleapi.MaxMemorySafeIntegerV1, "workspace-main", 100, 100000)
		configDigest, err := moduleapi.ComputeMemoryContextBindingDigestV1(config)
		if err != nil {
			t.Fatal(err)
		}
		count.AlgorithmConfigDigest = configDigest
		count.EntryDigest = ""
		count, _, err = moduleapi.NewMemoryEntryV1(count)
		if err != nil {
			t.Fatal(err)
		}
		current := memorySnapshot(t, 1, "", "", []moduleapi.MemoryEntryV1{count})
		input := successfulInput(current, digestByte('a'), "attempt-overflow", 200, "workspace-main", "go", "go", config, 'b', 'c')
		if _, _, err := BuildSuccessfulRevision(current, input); !errors.Is(err, ErrOverflow) {
			t.Fatalf("count overflow error=%v", err)
		}
	})

	t.Run("revision", func(t *testing.T) {
		current := memorySnapshot(t, moduleapi.MaxMemorySafeIntegerV1, digestByte('d'), "prior-attempt", nil)
		config := memoryConfig(t, []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryRepeatedTermCount}, 4, 1024, nil, nil, 0, 60)
		input := successfulInput(current, digestByte('e'), "attempt-revision-overflow", 200, "workspace-main", "go", "go", config, 'f', '1')
		if _, _, err := BuildSuccessfulRevision(current, input); !errors.Is(err, ErrOverflow) {
			t.Fatalf("revision overflow error=%v", err)
		}
	})

	t.Run("ttl addition", func(t *testing.T) {
		current := memorySnapshot(t, 1, "", "", nil)
		config := memoryConfig(t, []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryRepeatedTermCount}, 4, 1024, nil, nil, 0, moduleapi.MaxMemoryTTLSecondsV1)
		input := successfulInput(current, digestByte('2'), "attempt-ttl-overflow", moduleapi.MaxMemorySafeIntegerV1, "workspace-main", "go", "go", config, '3', '4')
		if _, _, err := BuildSuccessfulRevision(current, input); !errors.Is(err, ErrOverflow) {
			t.Fatalf("TTL overflow error=%v", err)
		}
	})

	t.Run("snapshot bytes", func(t *testing.T) {
		entries := make([]moduleapi.MemoryEntryV1, 0)
		var current moduleapi.AgentMemorySnapshotV1
		for index := 0; ; index++ {
			candidate := append(append([]moduleapi.MemoryEntryV1(nil), entries...), seedTextEntry(
				t,
				fmt.Sprintf("fact-%03d", index),
				moduleapi.MemoryEntryFact,
				fmt.Sprintf("fact-%03d", index),
				strings.Repeat("x", moduleapi.MaxMemoryEntryTextBytesV1),
				[]string{"*"},
				100,
				0,
			))
			built := moduleapi.AgentMemorySnapshotV1{
				SchemaVersion: moduleapi.MemorySnapshotSchemaV1,
				TenantID:      "tenant-one",
				AgentID:       "agent-main",
				Revision:      1,
				Entries:       candidate,
			}
			frozen, _, err := moduleapi.NewAgentMemorySnapshotV1(built)
			if err != nil {
				break
			}
			entries = candidate
			current = frozen
		}
		if len(entries) == 0 {
			t.Fatal("failed to build near-limit valid snapshot")
		}
		config := memoryConfig(t, []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryTaskSummary}, 4, 4096, nil, nil, 4096, 60)
		input := successfulInput(current, digestByte('5'), "attempt-size-overflow", 200, "workspace-main", strings.Repeat("a", 4096), strings.Repeat("b", 4096), config, '6', '7')
		if _, _, err := BuildSuccessfulRevision(current, input); !errors.Is(err, ErrOverflow) {
			t.Fatalf("snapshot byte overflow error=%v", err)
		}
	})
}

func TestBuildSuccessfulRevisionRejectsNonNFCAndTruncatesAtRuneBoundary(t *testing.T) {
	current := memorySnapshot(t, 1, "", "", nil)
	config := memoryConfig(t, []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryTaskSummary}, 4, 1024, nil, nil, 7, 60)
	invalid := successfulInput(current, digestByte('1'), "attempt-nfd", 100, "workspace-main", "Cafe\u0301", "done", config, '2', '3')
	if _, _, err := BuildSuccessfulRevision(current, invalid); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NFD source error=%v", err)
	}

	valid := successfulInput(current, digestByte('4'), "attempt-runes", 100, "workspace-main", "你好世界", "完成任务", config, '5', '6')
	next, _, err := BuildSuccessfulRevision(current, valid)
	if err != nil {
		t.Fatal(err)
	}
	summary := entryByIdentity(next.Entries, moduleapi.MemoryEntryTaskSummary, "attempt-runes", "workspace-main")
	if !utf8.ValidString(summary.Text) || summary.Text != moduleapi.CanonicalText(summary.Text) || len(summary.Text) > 7 {
		t.Fatalf("rune-safe summary=%q bytes=%d", summary.Text, len(summary.Text))
	}
}

func TestBuildSuccessfulRevisionSmallSummaryBudgetRetainsCompleteRune(
	t *testing.T,
) {
	tests := []struct {
		name       string
		taskText   string
		resultText string
		maximum    uint32
	}{
		{name: "four-byte budget with CJK", taskText: "你", resultText: "好", maximum: 4},
		{name: "four-byte budget with emoji", taskText: "😀", resultText: "🚀", maximum: 4},
		{name: "five-byte budget with emoji", taskText: "😀", resultText: "🚀", maximum: 5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := memorySnapshot(t, 1, "", "", nil)
			config := memoryConfig(
				t,
				[]moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryTaskSummary},
				4,
				1024,
				nil,
				nil,
				test.maximum,
				60,
			)
			input := successfulInput(
				current,
				digestByte('1'),
				"attempt-small-summary",
				100,
				"workspace-main",
				test.taskText,
				test.resultText,
				config,
				'2',
				'3',
			)
			first, firstCanonical, err := BuildSuccessfulRevision(current, input)
			if err != nil {
				t.Fatalf("BuildSuccessfulRevision: %v", err)
			}
			second, secondCanonical, err := BuildSuccessfulRevision(current, input)
			if err != nil {
				t.Fatalf("BuildSuccessfulRevision repeat: %v", err)
			}
			if !bytes.Equal(firstCanonical, secondCanonical) ||
				fmt.Sprintf("%#v", first) != fmt.Sprintf("%#v", second) {
				t.Fatal("small-budget summary is not deterministic")
			}
			summary := entryByIdentity(
				first.Entries,
				moduleapi.MemoryEntryTaskSummary,
				"attempt-small-summary",
				"workspace-main",
			)
			if summary.Text == "" || !utf8.ValidString(summary.Text) ||
				len(summary.Text) > int(test.maximum) {
				t.Fatalf(
					"small-budget summary=%q bytes=%d maximum=%d",
					summary.Text,
					len(summary.Text),
					test.maximum,
				)
			}
			_, size := utf8.DecodeRuneInString(summary.Text)
			if size == 0 || size > len(summary.Text) {
				t.Fatalf("summary does not retain a complete code point: %q", summary.Text)
			}
		})
	}
}

func TestBuildSuccessfulRevisionRejectsImmediateSemanticReplay(t *testing.T) {
	current := memorySnapshot(t, 2, digestByte('a'), "attempt-one", nil)
	config := memoryConfig(t, []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryRepeatedTermCount}, 4, 1024, nil, nil, 0, 60)
	input := successfulInput(current, digestByte('b'), "attempt-one", 200, "workspace-main", "go", "go", config, 'c', 'd')
	if _, _, err := BuildSuccessfulRevision(current, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("same Attempt replay error=%v", err)
	}
}

func TestBuildSuccessfulRevisionRejectsWildcardExactObjectIdentities(t *testing.T) {
	current := memorySnapshot(t, 1, "", "", nil)
	config := memoryConfig(t, []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryTaskSummary}, 4, 1024, nil, nil, 16, 60)
	base := successfulInput(current, digestByte('4'), "attempt-wildcard", 100, "workspace-main", "task", "result", config, '5', '6')

	workspaceWildcard := base
	workspaceWildcard.Workspace.ID = "*"
	if _, _, err := BuildSuccessfulRevision(current, workspaceWildcard); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("wildcard Workspace error=%v", err)
	}

	agentWildcard := base
	agentWildcard.Agent.ID = "*"
	if _, _, err := BuildSuccessfulRevision(current, agentWildcard); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("wildcard Agent error=%v", err)
	}
}

func TestBuildSuccessfulRevisionSummaryOnlyDoesNotRunTermExtraction(t *testing.T) {
	current := memorySnapshot(t, 1, "", "", nil)
	config := memoryConfig(t, []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryTaskSummary}, 4, 1024, nil, nil, 64, 60)
	terms := make([]string, maxExtractedDistinctTermsV1+1)
	for index := range terms {
		terms[index] = fmt.Sprintf("u%x", index)
	}
	input := successfulInput(current, digestByte('e'), "attempt-summary-only", 100, "workspace-main", strings.Join(terms, " "), "done", config, 'f', '1')
	if _, _, err := BuildSuccessfulRevision(current, input); err != nil {
		t.Fatalf("summary-only update unexpectedly ran count extraction: %v", err)
	}
}

func TestBuildSuccessfulRevisionCountsUnsegmentedHanBigrams(t *testing.T) {
	current := memorySnapshot(t, 1, "", "", nil)
	config := memoryConfig(
		t,
		[]moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryRepeatedTermCount},
		8,
		1024,
		nil,
		[]string{"程喜"},
		0,
		60,
	)
	input := successfulInput(current, digestByte('8'), "attempt-han", 100, "workspace-main", "喜欢编程喜欢编程", "完成", config, '9', 'a')
	next, _, err := BuildSuccessfulRevision(current, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"喜欢", "编程"} {
		if got := entryByIdentity(next.Entries, moduleapi.MemoryEntryRepeatedTermCount, key, "workspace-main").Count; got != 2 {
			t.Fatalf("Han bigram %q count=%d, want 2", key, got)
		}
	}
	if hasEntry(next.Entries, moduleapi.MemoryEntryRepeatedTermCount, "程喜", "workspace-main") {
		t.Fatal("Han stop term was retained")
	}
}

func TestNormalizedDigestUnionAlwaysRetainsCurrentSourcesWithinBound(t *testing.T) {
	existing := make([]string, moduleapi.MaxMemorySourceRefsV1)
	for index := range existing {
		existing[index] = fmt.Sprintf("%064x", index+1)
	}
	currentTask := strings.Repeat("f", moduleapi.SHA256HexLength)
	currentResult := strings.Repeat("e", moduleapi.SHA256HexLength)
	got := normalizedDigestUnion(existing, currentTask, currentResult)
	if len(got) != moduleapi.MaxMemorySourceRefsV1 {
		t.Fatalf("source refs=%d", len(got))
	}
	if !containsString(got, currentTask) || !containsString(got, currentResult) {
		t.Fatalf("current sources were dropped: %v", got)
	}
}

func memoryConfig(
	t *testing.T,
	kinds []moduleapi.MemoryEntryKindV1,
	maxItems uint32,
	maxBytes uint32,
	rules []moduleapi.MemoryCategoryRuleV1,
	stopTerms []string,
	summaryBytes uint32,
	ttlSeconds uint64,
) moduleapi.MemoryContextBindingV1 {
	t.Helper()
	frozen, _, _, err := moduleapi.NewMemoryContextBindingV1(moduleapi.MemoryContextBindingV1{
		SchemaVersion:       moduleapi.MemoryContextBindingSchemaV1,
		Kinds:               kinds,
		MaxItems:            maxItems,
		MaxTotalTextBytes:   maxBytes,
		CategoryRules:       rules,
		StopTerms:           stopTerms,
		SummaryMaxTextBytes: summaryBytes,
		EntryTTLSeconds:     ttlSeconds,
	})
	if err != nil {
		t.Fatalf("NewMemoryContextBindingV1: %v", err)
	}
	return frozen
}

func memoryScope(workspaceID string) moduleapi.MemoryQueryScopeV1 {
	return moduleapi.MemoryQueryScopeV1{
		TenantID:     "tenant-one",
		Workspace:    objectRef(workspaceID, 'w'),
		Agent:        objectRef("agent-main", 'a'),
		TaskInputRef: digestByte('t'),
	}
}

func memoryAuthority(
	scope moduleapi.MemoryQueryScopeV1,
	kinds []moduleapi.MemoryEntryKindV1,
	maxItems uint32,
	maxBytes uint32,
) moduleapi.MemoryAuthorityCeilingV1 {
	return moduleapi.MemoryAuthorityCeilingV1{
		SchemaVersion:       moduleapi.MemoryAuthorityCeilingSchemaV1,
		TenantID:            scope.TenantID,
		AgentID:             scope.Agent.ID,
		AllowedWorkspaceIDs: []string{scope.Workspace.ID},
		AllowedKinds:        kinds,
		MaxItems:            maxItems,
		MaxTotalTextBytes:   maxBytes,
	}
}

func memorySnapshot(
	t *testing.T,
	revision uint64,
	previousDigest string,
	sourceAttempt string,
	entries []moduleapi.MemoryEntryV1,
) moduleapi.AgentMemorySnapshotV1 {
	t.Helper()
	frozen, _, err := moduleapi.NewAgentMemorySnapshotV1(moduleapi.AgentMemorySnapshotV1{
		SchemaVersion:          moduleapi.MemorySnapshotSchemaV1,
		TenantID:               "tenant-one",
		AgentID:                "agent-main",
		Revision:               revision,
		PreviousSnapshotDigest: previousDigest,
		SourceAttemptID:        sourceAttempt,
		Entries:                entries,
	})
	if err != nil {
		t.Fatalf("NewAgentMemorySnapshotV1: %v", err)
	}
	return frozen
}

func snapshotRef(snapshot moduleapi.AgentMemorySnapshotV1, digest string) moduleapi.MemorySnapshotRefV1 {
	return moduleapi.MemorySnapshotRefV1{
		TenantID: snapshot.TenantID,
		AgentID:  snapshot.AgentID,
		Revision: snapshot.Revision,
		Digest:   digest,
	}
}

func successfulInput(
	current moduleapi.AgentMemorySnapshotV1,
	currentDigest string,
	attemptID string,
	completedAt uint64,
	workspaceID string,
	taskText string,
	resultText string,
	config moduleapi.MemoryContextBindingV1,
	taskDigestByte byte,
	resultDigestByte byte,
) SuccessfulRevisionInput {
	return SuccessfulRevisionInput{
		CurrentSnapshotRef: snapshotRef(current, currentDigest),
		Source: SuccessfulRevisionSource{
			TaskRef:    digestByte(taskDigestByte),
			TaskText:   taskText,
			ResultRef:  digestByte(resultDigestByte),
			ResultText: resultText,
		},
		Agent:     objectRef("agent-main", 'a'),
		Workspace: objectRef(workspaceID, 'w'),
		Attempt: SuccessfulAttempt{
			ID:                attemptID,
			CompletedAtUnixMS: completedAt,
		},
		Config: config,
	}
}

func objectRef(id string, digestCharacter byte) moduleapi.MemoryObjectRefV1 {
	return moduleapi.MemoryObjectRefV1{
		ID:      id,
		Version: "1",
		Digest:  digestByte(digestCharacter),
	}
}

func seedTextEntry(
	t *testing.T,
	id string,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	text string,
	workspaces []string,
	created uint64,
	expires uint64,
) moduleapi.MemoryEntryV1 {
	t.Helper()
	entry, _, err := moduleapi.NewMemoryEntryV1(moduleapi.MemoryEntryV1{
		EntryID:             id,
		Kind:                kind,
		Key:                 key,
		Text:                text,
		VisibleWorkspaceIDs: workspaces,
		SourceRefs:          []string{digestByte('s')},
		CreatedAtUnixMS:     created,
		ExpiresAtUnixMS:     expires,
	})
	if err != nil {
		t.Fatalf("NewMemoryEntryV1 seed: %v", err)
	}
	return entry
}

func derivedTextEntry(
	t *testing.T,
	id string,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	text string,
	workspace string,
	created uint64,
	expires uint64,
) moduleapi.MemoryEntryV1 {
	t.Helper()
	entry, _, err := moduleapi.NewMemoryEntryV1(moduleapi.MemoryEntryV1{
		EntryID:               id,
		Kind:                  kind,
		Key:                   key,
		Text:                  text,
		VisibleWorkspaceIDs:   []string{workspace},
		SourceRefs:            []string{digestByte('r')},
		AlgorithmVersion:      SuccessfulRevisionAlgorithmV1,
		AlgorithmConfigDigest: digestByte('c'),
		CreatedAtUnixMS:       created,
		ExpiresAtUnixMS:       expires,
	})
	if err != nil {
		t.Fatalf("NewMemoryEntryV1 derived text: %v", err)
	}
	return entry
}

func derivedCountEntry(
	t *testing.T,
	id string,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	count uint64,
	workspace string,
	created uint64,
	expires uint64,
) moduleapi.MemoryEntryV1 {
	t.Helper()
	entry, _, err := moduleapi.NewMemoryEntryV1(moduleapi.MemoryEntryV1{
		EntryID:               id,
		Kind:                  kind,
		Key:                   key,
		Count:                 count,
		VisibleWorkspaceIDs:   []string{workspace},
		SourceRefs:            []string{digestByte('r')},
		AlgorithmVersion:      SuccessfulRevisionAlgorithmV1,
		AlgorithmConfigDigest: digestByte('c'),
		CreatedAtUnixMS:       created,
		ExpiresAtUnixMS:       expires,
	})
	if err != nil {
		t.Fatalf("NewMemoryEntryV1 derived count: %v", err)
	}
	return entry
}

func derivedCountEntryForConfig(
	t *testing.T,
	id string,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	count uint64,
	workspace string,
	created uint64,
	expires uint64,
	config moduleapi.MemoryContextBindingV1,
) moduleapi.MemoryEntryV1 {
	t.Helper()
	entry := derivedCountEntry(
		t,
		id,
		kind,
		key,
		count,
		workspace,
		created,
		expires,
	)
	digest, err := moduleapi.ComputeMemoryContextBindingDigestV1(config)
	if err != nil {
		t.Fatalf("ComputeMemoryContextBindingDigestV1: %v", err)
	}
	entry.AlgorithmConfigDigest = digest
	entry.EntryDigest = ""
	entry, _, err = moduleapi.NewMemoryEntryV1(entry)
	if err != nil {
		t.Fatalf("NewMemoryEntryV1 current-config count: %v", err)
	}
	return entry
}

func entryByIdentity(
	entries []moduleapi.MemoryEntryV1,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	workspace string,
) moduleapi.MemoryEntryV1 {
	for _, entry := range entries {
		if entry.Kind == kind && entry.Key == key && len(entry.VisibleWorkspaceIDs) == 1 && entry.VisibleWorkspaceIDs[0] == workspace {
			return entry
		}
	}
	return moduleapi.MemoryEntryV1{}
}

func hasEntry(
	entries []moduleapi.MemoryEntryV1,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	workspace string,
) bool {
	return entryByIdentity(entries, kind, key, workspace).EntryID != ""
}

func entriesByKind(entries []moduleapi.MemoryEntryV1, kind moduleapi.MemoryEntryKindV1) []moduleapi.MemoryEntryV1 {
	result := make([]moduleapi.MemoryEntryV1, 0)
	for _, entry := range entries {
		if entry.Kind == kind {
			result = append(result, entry)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Key < result[right].Key })
	return result
}

func containsString(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func digestByte(value byte) string {
	if value < '0' || value > '9' && value < 'a' || value > 'f' {
		value = 'a'
	}
	return strings.Repeat(string(value), moduleapi.SHA256HexLength)
}
