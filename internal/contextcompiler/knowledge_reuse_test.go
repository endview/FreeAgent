package contextcompiler

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/knowledgecore"
	"github.com/endview/freeagent/internal/memorycore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompileV1ReuseEnabledFreshRetrievalClosesProvenance(t *testing.T) {
	input, fixture := newReusableFreshKnowledgeCompileInput(t)
	before := cloneCompileInput(input)
	first, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Compilation == nil ||
		first.Compilation.StopReason !=
			corecontract.ContextCompilationRetrievalBelowWatermark ||
		len(first.Compilation.KnowledgeRetrievals) != 1 ||
		len(first.Compilation.KnowledgeReuses) != 0 {
		t.Fatalf("reuse-enabled fresh compilation = %+v", first.Compilation)
	}
	evidence := first.Compilation.KnowledgeRetrievals[0]
	if evidence.Provenance == nil ||
		evidence.Provenance.Provider !=
			input.ContextPlan.Bindings[fixture.BindingIndex].Provider ||
		evidence.Provenance.RoutingAlgorithmVersion !=
			knowledgecore.RoutingAlgorithmVersionV1 ||
		!moduleapi.ValidSHA256(evidence.Provenance.DecisionSetDigest) ||
		evidence.Provenance.RetrievedAtUnixMS != 1000 {
		t.Fatalf("fresh provenance = %+v", evidence.Provenance)
	}
	if !bytes.Equal(first.RequestCanonical, second.RequestCanonical) ||
		!bytes.Equal(first.CompilationCanonical, second.CompilationCanonical) {
		t.Fatal("reuse-enabled fresh compilation is not byte-stable")
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatal("CompileV1 mutated caller-owned K3 fresh material")
	}

	input.ContextBindings[fixture.BindingIndex].KnowledgeProvenance.Provider.InstanceID =
		"mutated-instance"
	if first.Compilation.KnowledgeRetrievals[0].Provenance.Provider.InstanceID !=
		"knowledge-instance" {
		t.Fatal("compiled fresh provenance aliases caller-owned material")
	}
	if bytes.Contains(first.RequestCanonical, []byte("knowledge-instance")) ||
		bytes.Contains(first.RequestCanonical, []byte(knowledgecore.RoutingAlgorithmVersionV1)) {
		t.Fatal("Knowledge provenance leaked into the model prompt")
	}
}

func TestCompileV1ReuseEnabledFreshRetrievalRejectsProvenanceTampering(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(*CompileInputV1, uint32)
	}{
		{"absent", func(input *CompileInputV1, index uint32) {
			input.ContextBindings[index].KnowledgeProvenance = nil
		}},
		{"provider", func(input *CompileInputV1, index uint32) {
			input.ContextBindings[index].KnowledgeProvenance.Provider.ActivationRevision++
		}},
		{"routing algorithm", func(input *CompileInputV1, index uint32) {
			input.ContextBindings[index].KnowledgeProvenance.RoutingAlgorithmVersion =
				"freeagent.knowledge-routing/v2"
		}},
		{"decision set", func(input *CompileInputV1, index uint32) {
			input.ContextBindings[index].KnowledgeProvenance.DecisionSetDigest =
				testDigest("0")
		}},
		{"retrieval time", func(input *CompileInputV1, index uint32) {
			input.ContextBindings[index].KnowledgeProvenance.RetrievedAtUnixMS = 0
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, fixture := newReusableFreshKnowledgeCompileInput(t)
			test.mutate(&input, fixture.BindingIndex)
			if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
				t.Fatalf("error=%v, want ErrInvalidContextInput", err)
			}
		})
	}

	t.Run("provenance while reuse disabled", func(t *testing.T) {
		input := newCompileInput(t, "design an API backend")
		fixture := addKnowledgeContext(t, &input, "stable knowledge")
		input.ContextBindings[fixture.BindingIndex].KnowledgeProvenance =
			validCompilerKnowledgeProvenance(
				input.ContextPlan.Bindings[fixture.BindingIndex].Provider,
				testDigest("f"),
			)
		if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
			t.Fatalf("error=%v, want ErrInvalidContextInput", err)
		}
	})
}

func TestCompileV1FreshAndReusePromptCanonicalBytesAreEqual(t *testing.T) {
	freshInput, knowledgeFixture := newReusableFreshKnowledgeCompileInput(t)
	memoryFixture, category, repeated := addKnowledgeReuseMemoryContext(
		t,
		&freshInput,
	)
	fresh, err := CompileV1(freshInput)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Compilation == nil ||
		len(fresh.Compilation.KnowledgeRetrievals) != 1 ||
		fresh.Compilation.KnowledgeRetrievals[0].Provenance == nil {
		t.Fatalf("fresh compilation = %+v", fresh.Compilation)
	}

	reuseInput := cloneCompileInput(freshInput)
	material := &reuseInput.ContextBindings[knowledgeFixture.BindingIndex]
	material.DynamicRequestCanonical = nil
	material.DynamicOutputCanonical = nil
	material.KnowledgeProvenance = nil
	material.KnowledgeReuse = &corecontract.KnowledgeReuseEvidenceV1{
		SourceConversationID: "conversation-main",
		SourceTurnIndex:      1,
		SourceRunID:          "run-source",
		SourceAttemptID:      "attempt-source",
		SourceCompilationRef: testDigest("c"),
		FreshRetrieval:       fresh.Compilation.KnowledgeRetrievals[0],
		MemoryBindingIndex:   memoryFixture.BindingIndex,
		CategoryCounter:      category,
		RepeatedTermCounter:  repeated,
	}
	reuseBefore := cloneCompileInput(reuseInput)
	reused, err := CompileV1(reuseInput)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Compilation == nil ||
		reused.Compilation.StopReason !=
			corecontract.ContextCompilationRetrievalBelowWatermark ||
		len(reused.Compilation.KnowledgeRetrievals) != 0 ||
		len(reused.Compilation.KnowledgeReuses) != 1 {
		t.Fatalf("reuse compilation = %+v", reused.Compilation)
	}
	if !bytes.Equal(fresh.RequestCanonical, reused.RequestCanonical) ||
		fresh.Compilation.FinalRequestDigest !=
			reused.Compilation.FinalRequestDigest ||
		fresh.Compilation.OriginalEstimateTokens !=
			reused.Compilation.OriginalEstimateTokens ||
		fresh.Compilation.FinalEstimateTokens !=
			reused.Compilation.FinalEstimateTokens {
		t.Fatalf(
			"fresh/reuse prompt drift\nfresh=%s\nreuse=%s",
			fresh.RequestCanonical,
			reused.RequestCanonical,
		)
	}
	if bytes.Equal(fresh.CompilationCanonical, reused.CompilationCanonical) {
		t.Fatal("fresh and reuse evidence unexpectedly share canonical bytes")
	}
	if !reflect.DeepEqual(reuseInput, reuseBefore) {
		t.Fatal("CompileV1 mutated caller-owned Knowledge reuse material")
	}
	if len(memoryFixture.Output.SelectedEntryDigests) != 0 {
		t.Fatal("reuse counter fixture accidentally selected counters into prompt")
	}
	if bytes.Contains(reused.RequestCanonical, []byte("attempt-source")) ||
		bytes.Contains(reused.RequestCanonical, []byte("conversation-main")) ||
		bytes.Contains(reused.RequestCanonical, []byte(knowledgecore.RoutingAlgorithmVersionV1)) {
		t.Fatal("reuse proof metadata leaked into the model prompt")
	}
}

func TestCompileV1KnowledgeReuseClosureTamperingFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*testing.T, *CompileInputV1, uint32, uint32)
		wantMessage string
	}{
		{"current Config", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.
				FreshRetrieval.ConfigRef = testDigest("0")
		}, ""},
		{"current Authority", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.
				FreshRetrieval.AuthorityCeilingRef = testDigest("0")
		}, ""},
		{"current Scope", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.
				FreshRetrieval.Scope.Workspace.Version = "2"
		}, ""},
		{"current Source", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.
				FreshRetrieval.Source.Version = "2.0.0"
		}, ""},
		{"current request", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.
				FreshRetrieval.RequestDigest = testDigest("0")
		}, ""},
		{"source output", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.
				FreshRetrieval.Hits[0].Text = "tampered output"
		}, ""},
		{"Provider", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.
				FreshRetrieval.Provenance.Provider.ActivationRevision++
		}, ""},
		{"decision digest", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.
				FreshRetrieval.Provenance.DecisionSetDigest = testDigest("0")
		}, ""},
		{"TTL exact expiry", func(t *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			recloseKnowledgeReuseTTLForTest(t, input, knowledgeIndex, 1)
		}, "TTL"},
		{"counter key", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.CategoryCounter.Key =
				"frontend"
		}, ""},
		{"counter count", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.CategoryCounter.Count = 1
		}, ""},
		{"counter absent from Memory request candidates", func(_ *testing.T, input *CompileInputV1, knowledgeIndex, _ uint32) {
			input.ContextBindings[knowledgeIndex].KnowledgeReuse.
				CategoryCounter.EntryDigest = testDigest("0")
		}, "absent from the exact Memory request candidates"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input, knowledgeIndex, memoryIndex := newKnowledgeReuseCompileInput(t)
			test.mutate(t, &input, knowledgeIndex, memoryIndex)
			_, err := CompileV1(input)
			if !errors.Is(err, ErrInvalidContextInput) {
				t.Fatalf("error=%v, want ErrInvalidContextInput", err)
			}
			if test.wantMessage != "" && !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("error=%v, want message containing %q", err, test.wantMessage)
			}
		})
	}
}

func TestCloneCompileInputDeepCopiesKnowledgeK3Material(t *testing.T) {
	input, fixture := newReusableFreshKnowledgeCompileInput(t)
	provenance := input.ContextBindings[fixture.BindingIndex].KnowledgeProvenance
	input.ContextBindings[fixture.BindingIndex].KnowledgeReuse =
		&corecontract.KnowledgeReuseEvidenceV1{
			SourceConversationID: "conversation-main",
			SourceTurnIndex:      1,
			SourceRunID:          "run-source",
			SourceAttemptID:      "attempt-source",
			SourceCompilationRef: testDigest("c"),
			FreshRetrieval: corecontract.KnowledgeRetrievalEvidenceV1{
				BindingIndex:        fixture.BindingIndex,
				ConfigRef:           input.ContextPlan.Bindings[fixture.BindingIndex].ConfigRef,
				AuthorityCeilingRef: input.ContextPlan.Bindings[fixture.BindingIndex].AuthorityCeilingRef,
				RequestDigest:       fixture.RequestDigest,
				Scope:               fixture.Request.Scope,
				Source:              fixture.Output.Source,
				Hits:                fixture.Output.Hits,
				OutputDigest:        fixture.OutputDigest,
				Provenance:          provenance,
			},
			MemoryBindingIndex: 1,
			CategoryCounter: moduleapi.MemoryCandidateV1{
				EntryDigest: testDigest("d"), Kind: moduleapi.MemoryEntryCategoryCount,
				Key: "backend", Count: 2,
			},
			RepeatedTermCounter: moduleapi.MemoryCandidateV1{
				EntryDigest: testDigest("e"), Kind: moduleapi.MemoryEntryRepeatedTermCount,
				Key: "api", Count: 3,
			},
		}

	cloned := cloneCompileInput(input)
	input.ContextBindings[fixture.BindingIndex].KnowledgeProvenance.Provider.InstanceID =
		"mutated-provenance"
	input.ContextBindings[fixture.BindingIndex].KnowledgeReuse.
		FreshRetrieval.Provenance.Provider.InstanceID = "mutated-reuse"
	input.ContextBindings[fixture.BindingIndex].KnowledgeReuse.
		FreshRetrieval.Hits[0].Text = "mutated-hit"
	input.ContextBindings[fixture.BindingIndex].KnowledgeReuse.
		FreshRetrieval.Hits[0].VisibleTo[0].AgentID = "mutated-agent"

	got := cloned.ContextBindings[fixture.BindingIndex]
	if got.KnowledgeProvenance.Provider.InstanceID != "knowledge-instance" ||
		got.KnowledgeReuse.FreshRetrieval.Provenance.Provider.InstanceID !=
			"knowledge-instance" ||
		got.KnowledgeReuse.FreshRetrieval.Hits[0].Text != "stable knowledge" ||
		got.KnowledgeReuse.FreshRetrieval.Hits[0].VisibleTo[0].AgentID !=
			input.AgentScope.ID {
		t.Fatalf("cloneCompileInput aliased K3 material: %+v", got)
	}
}

func newReusableFreshKnowledgeCompileInput(
	t *testing.T,
) (CompileInputV1, knowledgeCompileFixtureV1) {
	t.Helper()
	input := newCompileInput(t, "design an API backend")
	fixture := addKnowledgeContext(t, &input, "stable knowledge")
	routeKnowledgeFixture(
		t,
		&input,
		fixture.BindingIndex,
		[]string{"backend"},
		[]string{"api"},
		1,
		true,
	)
	enableKnowledgeReusePolicyFixture(t, &input, fixture.BindingIndex)
	task, err := corecontract.RestoreTaskInputV1(input.TaskInputCanonical)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := moduleapi.NewPortPlan(*input.ContextPlan)
	if err != nil {
		t.Fatal(err)
	}
	_, decisionSetDigest, err := decideKnowledgeBindings(input, plan, task.Text)
	if err != nil {
		t.Fatal(err)
	}
	input.ContextBindings[fixture.BindingIndex].KnowledgeProvenance =
		validCompilerKnowledgeProvenance(
			plan.Bindings[fixture.BindingIndex].Provider,
			decisionSetDigest,
		)
	return input, fixture
}

func newKnowledgeReuseCompileInput(
	t *testing.T,
) (CompileInputV1, uint32, uint32) {
	t.Helper()
	freshInput, knowledgeFixture := newReusableFreshKnowledgeCompileInput(t)
	memoryFixture, category, repeated := addKnowledgeReuseMemoryContext(
		t,
		&freshInput,
	)
	fresh, err := CompileV1(freshInput)
	if err != nil {
		t.Fatal(err)
	}
	input := cloneCompileInput(freshInput)
	material := &input.ContextBindings[knowledgeFixture.BindingIndex]
	material.DynamicRequestCanonical = nil
	material.DynamicOutputCanonical = nil
	material.KnowledgeProvenance = nil
	material.KnowledgeReuse = &corecontract.KnowledgeReuseEvidenceV1{
		SourceConversationID: "conversation-main",
		SourceTurnIndex:      1,
		SourceRunID:          "run-source",
		SourceAttemptID:      "attempt-source",
		SourceCompilationRef: testDigest("c"),
		FreshRetrieval:       fresh.Compilation.KnowledgeRetrievals[0],
		MemoryBindingIndex:   memoryFixture.BindingIndex,
		CategoryCounter:      category,
		RepeatedTermCounter:  repeated,
	}
	return input, knowledgeFixture.BindingIndex, memoryFixture.BindingIndex
}

func recloseKnowledgeReuseTTLForTest(
	t *testing.T,
	input *CompileInputV1,
	knowledgeIndex uint32,
	ttlSeconds uint64,
) {
	t.Helper()
	config, err := moduleapi.RestoreContextBindingConfigV1(
		input.ContextBindings[knowledgeIndex].ConfigCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := moduleapi.RestoreKnowledgeContextBindingParametersV1(config)
	if err != nil {
		t.Fatal(err)
	}
	binding.Routing.Reuse.ReuseTTLSeconds = ttlSeconds
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(binding)
	if err != nil {
		t.Fatal(err)
	}
	config.Parameters = parameters
	_, configCanonical, err := moduleapi.NewContextBindingConfigV1(config)
	if err != nil {
		t.Fatal(err)
	}
	input.ContextBindings[knowledgeIndex].ConfigCanonical = configCanonical
	configRef := contentDigest("CONFIG", jsonMediaType, configCanonical)
	input.ContextPlan.Bindings[knowledgeIndex].ConfigRef = configRef
	input.ContextBindings[knowledgeIndex].KnowledgeReuse.FreshRetrieval.ConfigRef =
		configRef

	task, err := corecontract.RestoreTaskInputV1(input.TaskInputCanonical)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := moduleapi.NewPortPlan(*input.ContextPlan)
	if err != nil {
		t.Fatal(err)
	}
	_, decisionSetDigest, err := decideKnowledgeBindings(*input, plan, task.Text)
	if err != nil {
		t.Fatal(err)
	}
	input.ContextBindings[knowledgeIndex].KnowledgeReuse.FreshRetrieval.
		Provenance.DecisionSetDigest = decisionSetDigest
}

func enableKnowledgeReusePolicyFixture(
	t *testing.T,
	input *CompileInputV1,
	index uint32,
) {
	t.Helper()
	config, err := moduleapi.RestoreContextBindingConfigV1(
		input.ContextBindings[index].ConfigCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := moduleapi.RestoreKnowledgeContextBindingParametersV1(config)
	if err != nil {
		t.Fatal(err)
	}
	binding.Routing.Reuse = &moduleapi.KnowledgeReusePolicyV1{
		ExactQuestionOnly:    true,
		MinCategoryCount:     2,
		MinRepeatedTermCount: 3,
		MaxLookbackTurns:     8,
		ReuseTTLSeconds:      2,
	}
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(binding)
	if err != nil {
		t.Fatal(err)
	}
	config.Parameters = parameters
	_, canonical, err := moduleapi.NewContextBindingConfigV1(config)
	if err != nil {
		t.Fatal(err)
	}
	input.ContextBindings[index].ConfigCanonical = canonical
	input.ContextPlan.Bindings[index].ConfigRef = contentDigest(
		"CONFIG",
		jsonMediaType,
		canonical,
	)
}

func validCompilerKnowledgeProvenance(
	provider moduleapi.ActivatedModuleRef,
	decisionSetDigest string,
) *corecontract.KnowledgeRetrievalProvenanceV1 {
	return &corecontract.KnowledgeRetrievalProvenanceV1{
		Provider:                provider,
		RoutingAlgorithmVersion: knowledgecore.RoutingAlgorithmVersionV1,
		DecisionSetDigest:       decisionSetDigest,
		RetrievedAtUnixMS:       1000,
	}
}

func addKnowledgeReuseMemoryContext(
	t *testing.T,
	input *CompileInputV1,
) (
	memoryCompileFixtureV1,
	moduleapi.MemoryCandidateV1,
	moduleapi.MemoryCandidateV1,
) {
	t.Helper()
	binding, bindingCanonical, bindingDigest, err :=
		moduleapi.NewMemoryContextBindingV1(moduleapi.MemoryContextBindingV1{
			SchemaVersion: moduleapi.MemoryContextBindingSchemaV1,
			Kinds: []moduleapi.MemoryEntryKindV1{
				moduleapi.MemoryEntryCategoryCount,
				moduleapi.MemoryEntryRepeatedTermCount,
			},
			MaxItems:            4,
			MaxTotalTextBytes:   4096,
			CategoryRules:       []moduleapi.MemoryCategoryRuleV1{{Key: "backend", Terms: []string{"backend"}}},
			StopTerms:           []string{},
			SummaryMaxTextBytes: 0,
			EntryTTLSeconds:     10,
		})
	if err != nil {
		t.Fatal(err)
	}
	_, configCanonical, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementUntrustedData,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    bindingCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authority, authorityCanonical, err := moduleapi.NewMemoryAuthorityCeilingV1(
		moduleapi.MemoryAuthorityCeilingV1{
			SchemaVersion:       moduleapi.MemoryAuthorityCeilingSchemaV1,
			TenantID:            input.TenantID,
			AgentID:             input.AgentScope.ID,
			AllowedWorkspaceIDs: []string{input.WorkspaceScope.ID},
			AllowedKinds: []moduleapi.MemoryEntryKindV1{
				moduleapi.MemoryEntryCategoryCount,
				moduleapi.MemoryEntryRepeatedTermCount,
			},
			MaxItems:          4,
			MaxTotalTextBytes: 4096,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	newCount := func(
		entryID string,
		kind moduleapi.MemoryEntryKindV1,
		key string,
		count uint64,
		source string,
	) moduleapi.MemoryEntryV1 {
		entry, _, err := moduleapi.NewMemoryEntryV1(moduleapi.MemoryEntryV1{
			EntryID:               entryID,
			Kind:                  kind,
			Key:                   key,
			Count:                 count,
			VisibleWorkspaceIDs:   []string{input.WorkspaceScope.ID},
			SourceRefs:            []string{source},
			AlgorithmVersion:      memorycore.SuccessfulRevisionAlgorithmV1,
			AlgorithmConfigDigest: bindingDigest,
			CreatedAtUnixMS:       1000,
			ExpiresAtUnixMS:       11000,
		})
		if err != nil {
			t.Fatal(err)
		}
		return entry
	}
	categoryEntry := newCount(
		"count-backend",
		moduleapi.MemoryEntryCategoryCount,
		"backend",
		2,
		testDigest("3"),
	)
	repeatedEntry := newCount(
		"count-api",
		moduleapi.MemoryEntryRepeatedTermCount,
		"api",
		3,
		testDigest("4"),
	)
	snapshot, snapshotCanonical, err := moduleapi.NewAgentMemorySnapshotV1(
		moduleapi.AgentMemorySnapshotV1{
			SchemaVersion: moduleapi.MemorySnapshotSchemaV1,
			TenantID:      input.TenantID,
			AgentID:       input.AgentScope.ID,
			Revision:      1,
			Entries:       []moduleapi.MemoryEntryV1{categoryEntry, repeatedEntry},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshotRef := moduleapi.MemorySnapshotRefV1{
		TenantID: snapshot.TenantID,
		AgentID:  snapshot.AgentID,
		Revision: snapshot.Revision,
		Digest: contentDigest(
			"MEMORY_SNAPSHOT",
			jsonMediaType,
			snapshotCanonical,
		),
	}
	scope := moduleapi.MemoryQueryScopeV1{
		TenantID: input.TenantID,
		Workspace: moduleapi.MemoryObjectRefV1{
			ID: input.WorkspaceScope.ID, Version: input.WorkspaceScope.Version,
			Digest: input.WorkspaceScope.Digest,
		},
		Agent: moduleapi.MemoryObjectRefV1{
			ID: input.AgentScope.ID, Version: input.AgentScope.Version,
			Digest: input.AgentScope.Digest,
		},
		TaskInputRef: input.TaskInputRef,
	}
	candidates, resolved, err := memorycore.FilterCandidates(
		snapshot,
		snapshotRef,
		scope,
		binding,
		authority,
		2000,
	)
	if err != nil {
		t.Fatal(err)
	}
	task, err := corecontract.RestoreTaskInputV1(input.TaskInputCanonical)
	if err != nil {
		t.Fatal(err)
	}
	request, requestCanonical, requestDigest, err :=
		moduleapi.NewMemoryContextRequestV1(moduleapi.MemoryContextRequestV1{
			SchemaVersion:     moduleapi.MemoryContextRequestSchemaV1,
			Snapshot:          snapshotRef,
			Scope:             scope,
			QueryText:         task.Text,
			EvaluatedAtUnixMS: 2000,
			Candidates:        candidates,
			MaxItems:          resolved.MaxItems,
			MaxTotalTextBytes: resolved.MaxTotalTextBytes,
		})
	if err != nil {
		t.Fatal(err)
	}
	output, outputCanonical, outputDigest, err :=
		moduleapi.NewMemoryContextOutputV1(moduleapi.MemoryContextOutputV1{
			SchemaVersion:        moduleapi.MemoryContextOutputSchemaV1,
			RequestDigest:        requestDigest,
			Snapshot:             snapshotRef,
			SelectedEntryDigests: []string{},
		})
	if err != nil {
		t.Fatal(err)
	}
	bindingIndex := uint32(len(input.ContextBindings))
	portBinding := moduleapi.PortBinding{
		Provider: moduleapi.ActivatedModuleRef{
			ModuleID:           "test.memory",
			Version:            "1.0.0",
			ArtifactDigest:     testDigest("6"),
			InstanceID:         "memory-reuse-instance",
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "test.memory.adapter.v1",
			ActivationRevision: 1,
		},
		ConfigRef: contentDigest("CONFIG", jsonMediaType, configCanonical),
		AuthorityCeilingRef: contentDigest(
			"AUTHORITY_CEILING",
			jsonMediaType,
			authorityCanonical,
		),
		StaticContextRefs: []string{},
		FailurePolicy:     moduleapi.FailureRequired,
	}
	input.ContextPlan.Bindings = append(input.ContextPlan.Bindings, portBinding)
	input.ContextBindings = append(input.ContextBindings, BindingMaterialV1{
		ConfigCanonical:         configCanonical,
		AuthorityCanonical:      authorityCanonical,
		StaticContextCanonicals: [][]byte{},
		DynamicStateCanonical:   snapshotCanonical,
		DynamicRequestCanonical: requestCanonical,
		DynamicOutputCanonical:  outputCanonical,
	})
	var category, repeated moduleapi.MemoryCandidateV1
	for _, candidate := range request.Candidates {
		switch candidate.Kind {
		case moduleapi.MemoryEntryCategoryCount:
			category = candidate
		case moduleapi.MemoryEntryRepeatedTermCount:
			repeated = candidate
		}
	}
	if category.EntryDigest == "" || repeated.EntryDigest == "" {
		t.Fatalf("Memory reuse counters missing from candidates: %+v", request.Candidates)
	}
	return memoryCompileFixtureV1{
		BindingIndex:  bindingIndex,
		Binding:       binding,
		Authority:     authority,
		Snapshot:      snapshot,
		SnapshotRef:   snapshotRef,
		Request:       request,
		RequestDigest: requestDigest,
		Candidates:    append([]moduleapi.MemoryCandidateV1(nil), request.Candidates...),
		Output:        output,
		OutputDigest:  outputDigest,
	}, category, repeated
}
