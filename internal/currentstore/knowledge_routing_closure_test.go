package currentstore

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestKnowledgeRoutingMixedRetrievalShortcutClosesAndRecompilesExactly(t *testing.T) {
	fixture := newMixedKnowledgeRoutingClosure(t)

	if err := validateKnowledgeRetrievalsForRun(
		fixture.compilation,
		fixture.run,
		fixture.request,
		fixture.bindings,
	); err != nil {
		t.Fatalf("valid mixed Knowledge routing closure: %v", err)
	}

	plan, materials, err := knowledgeCompilerBindingsForRun(
		fixture.compilation,
		fixture.run,
		fixture.taskText,
		fixture.bindings,
		nil,
	)
	if err != nil {
		t.Fatalf("rebuild mixed Knowledge compiler materials: %v", err)
	}
	if plan == nil || len(materials) != 2 {
		t.Fatalf("rebuilt plan/materials=%+v/%d", plan, len(materials))
	}
	if len(materials[0].DynamicRequestCanonical) == 0 ||
		len(materials[0].DynamicOutputCanonical) == 0 {
		t.Fatal("selected Knowledge Binding lost its exact request/output")
	}
	if len(materials[1].DynamicStateCanonical) != 0 ||
		len(materials[1].DynamicRequestCanonical) != 0 ||
		len(materials[1].DynamicOutputCanonical) != 0 {
		t.Fatal("NOT_SELECTED Knowledge Binding acquired dynamic material")
	}

	recompileInput := fixture.compileInput
	recompileInput.ContextPlan = plan
	recompileInput.ContextBindings = materials
	recompiled, err := contextcompiler.CompileV1(recompileInput)
	if err != nil {
		t.Fatalf("recompile mixed Knowledge routing closure: %v", err)
	}
	if !bytes.Equal(recompiled.RequestCanonical, fixture.requestCanonical) {
		t.Fatal("Store-recompiled model request bytes changed")
	}
	if !bytes.Equal(
		recompiled.CompilationCanonical,
		fixture.compilationCanonical,
	) {
		t.Fatal("Store-recompiled ContextCompilation bytes changed")
	}
}

func TestKnowledgeRoutingMixedClosureRejectsTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*mixedKnowledgeRoutingClosure)
	}{
		{
			name: "deleted shortcut",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts = nil
			},
		},
		{
			name: "retrieval and shortcut share BindingIndex",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeRetrievals[0].BindingIndex =
					value.compilation.KnowledgeShortcuts[0].BindingIndex
			},
		},
		{
			name: "zero hit retrieval disguises NOT_SELECTED",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				skipped := value.bindings[1]
				_, _, requestDigest, err :=
					moduleapi.NewKnowledgeContextRequestV1(
						moduleapi.KnowledgeContextRequestV1{
							SchemaVersion: moduleapi.
								KnowledgeContextRequestSchemaV1,
							Source:            skipped.Config.Source,
							Scope:             skipped.Scope,
							QueryText:         value.taskText,
							MaxHits:           skipped.MaxHits,
							MaxTotalTextBytes: skipped.MaxTextBytes,
						},
					)
				if err != nil {
					t.Fatal(err)
				}
				output, _, outputDigest, err :=
					moduleapi.NewKnowledgeContextOutputV1(
						moduleapi.KnowledgeContextOutputV1{
							SchemaVersion: moduleapi.
								KnowledgeContextOutputSchemaV1,
							RequestDigest: requestDigest,
							Source:        skipped.Config.Source,
							Hits:          []moduleapi.KnowledgeHitV1{},
						},
					)
				if err != nil {
					t.Fatal(err)
				}
				value.compilation.KnowledgeShortcuts = nil
				value.compilation.KnowledgeRetrievals = append(
					value.compilation.KnowledgeRetrievals,
					corecontract.KnowledgeRetrievalEvidenceV1{
						BindingIndex:        skipped.BindingIndex,
						ConfigRef:           skipped.Binding.ConfigRef,
						AuthorityCeilingRef: skipped.Binding.AuthorityCeilingRef,
						RequestDigest:       requestDigest,
						Scope:               skipped.Scope,
						Source:              output.Source,
						Hits:                output.Hits,
						OutputDigest:        outputDigest,
					},
				)
			},
		},
		{
			name: "decision set digest",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts[0].DecisionSetDigest =
					strings.Repeat("a", 64)
			},
		},
		{
			name: "exact question fingerprint",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts[0].ExactQuestionFingerprint =
					strings.Repeat("b", 64)
			},
		},
		{
			name: "config ref",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts[0].ConfigRef =
					strings.Repeat("c", 64)
			},
		},
		{
			name: "authority ref",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts[0].AuthorityCeilingRef =
					strings.Repeat("d", 64)
			},
		},
		{
			name: "scope",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts[0].Scope.Workspace.ID =
					"other-workspace"
			},
		},
		{
			name: "source",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts[0].Source.ID = "other-source"
			},
		},
		{
			name: "collection tags",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts[0].CollectionTags =
					[]string{"other"}
			},
		},
		{
			name: "matched terms",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts[0].MatchedTerms =
					[]string{"css"}
			},
		},
		{
			name: "minimum match terms",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts[0].MinMatchTerms++
			},
		},
		{
			name: "binding index",
			mutate: func(value *mixedKnowledgeRoutingClosure) {
				value.compilation.KnowledgeShortcuts[0].BindingIndex++
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMixedKnowledgeRoutingClosure(t)
			test.mutate(&fixture)
			if err := validateKnowledgeRetrievalsForRun(
				fixture.compilation,
				fixture.run,
				fixture.request,
				fixture.bindings,
			); err == nil {
				t.Fatal("accepted tampered mixed Knowledge routing closure")
			}
		})
	}
}

func TestKnowledgeRoutingCompilerRejectsSelectedMissingAndSkippedDynamicMaterial(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]contextcompiler.BindingMaterialV1)
	}{
		{
			name: "selected missing request",
			mutate: func(materials []contextcompiler.BindingMaterialV1) {
				materials[0].DynamicRequestCanonical = nil
			},
		},
		{
			name: "selected missing output",
			mutate: func(materials []contextcompiler.BindingMaterialV1) {
				materials[0].DynamicOutputCanonical = nil
			},
		},
		{
			name: "skipped carries request",
			mutate: func(materials []contextcompiler.BindingMaterialV1) {
				materials[1].DynamicRequestCanonical =
					bytes.Clone(materials[0].DynamicRequestCanonical)
			},
		},
		{
			name: "skipped carries output",
			mutate: func(materials []contextcompiler.BindingMaterialV1) {
				materials[1].DynamicOutputCanonical =
					bytes.Clone(materials[0].DynamicOutputCanonical)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newMixedKnowledgeRoutingClosure(t)
			input := fixture.compileInput
			input.ContextBindings = append(
				[]contextcompiler.BindingMaterialV1(nil),
				fixture.compileInput.ContextBindings...,
			)
			test.mutate(input.ContextBindings)
			if _, err := contextcompiler.CompileV1(input); err == nil {
				t.Fatal("compiler accepted invalid selected/skipped dynamic material")
			}
		})
	}
}

type mixedKnowledgeRoutingClosure struct {
	run                  RunForLoop
	bindings             []frozenKnowledgeBinding
	taskText             string
	compileInput         contextcompiler.CompileInputV1
	request              moduleapi.ModelGenerateRequestV1
	requestCanonical     []byte
	compilation          corecontract.ContextCompilationV1
	compilationCanonical []byte
}

func newMixedKnowledgeRoutingClosure(t *testing.T) mixedKnowledgeRoutingClosure {
	t.Helper()
	base := newKnowledgeClosureFixture(t)
	firstConfig, firstConfigCanonical := routedKnowledgeConfigRecord(
		t,
		base.source,
		"backend",
		"shared",
	)
	base.contextConfig = firstConfig
	base.binding.ConfigRef = firstConfig.Digest
	base.member.PortPlans[0].Bindings[0] = base.binding

	secondSource := moduleapi.KnowledgeSourceRefV1{
		ID:      "frontend.docs",
		Version: "v1",
		Digest:  strings.Repeat("8", 64),
	}
	secondConfig, secondConfigCanonical := routedKnowledgeConfigRecord(
		t,
		secondSource,
		"frontend",
		"css",
	)
	secondAuthorityValue := base.authorityValue
	secondAuthorityValue.Source = secondSource
	_, secondAuthorityCanonical, err :=
		moduleapi.NewKnowledgeAuthorityCeilingV1(secondAuthorityValue)
	if err != nil {
		t.Fatal(err)
	}
	secondAuthority := knowledgeContentRecord(
		t,
		ContentAuthorityCeiling,
		secondAuthorityCanonical,
	)
	secondBinding := base.binding
	secondBinding.Provider.ModuleID = "test.knowledge.frontend"
	secondBinding.Provider.ArtifactDigest = strings.Repeat("9", 64)
	secondBinding.Provider.InstanceID = "knowledge-frontend-instance"
	secondBinding.ConfigRef = secondConfig.Digest
	secondBinding.AuthorityCeilingRef = secondAuthority.Digest
	base.member.PortPlans[0].Bindings = append(
		base.member.PortPlans[0].Bindings,
		secondBinding,
	)

	run := knowledgeRunForValidation(base)
	run.Contents = append(run.Contents, secondConfig, secondAuthority)
	sort.Slice(run.Contents, func(left, right int) bool {
		return run.Contents[left].Digest < run.Contents[right].Digest
	})
	bindings, err := frozenKnowledgeBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		t.Fatalf("freeze mixed Knowledge Bindings: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("mixed frozen Knowledge Bindings=%d", len(bindings))
	}
	task, err := corecontract.RestoreTaskInputV1(base.task.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	requestCanonical, outputCanonical := selectedKnowledgeMaterialForRoutingTest(
		t,
		bindings[0],
		task.Text,
		base.authorityValue.AllowedScopes[0],
	)
	plan := run.Member.PortPlans[0]
	input := contextcompiler.CompileInputV1{
		TenantID:                       run.Manifest.TenantID,
		WorkspaceScope:                 run.Member.Workspace,
		AgentScope:                     run.Member.Agent,
		ContextPolicyRef:               run.Member.ContextPolicy,
		ContextPolicyDocumentCanonical: base.contextPolicy.CanonicalBytes,
		ModelParameters:                json.RawMessage(`{}`),
		ContextPlan:                    &plan,
		ContextBindings: []contextcompiler.BindingMaterialV1{
			{
				ConfigCanonical:         firstConfigCanonical,
				AuthorityCanonical:      base.authority.CanonicalBytes,
				DynamicRequestCanonical: requestCanonical,
				DynamicOutputCanonical:  outputCanonical,
			},
			{
				ConfigCanonical:    secondConfigCanonical,
				AuthorityCanonical: secondAuthorityCanonical,
			},
		},
		TaskInputRef:       run.Manifest.TaskInputRef,
		TaskInputCanonical: base.task.CanonicalBytes,
	}
	compiled, err := contextcompiler.CompileV1(input)
	if err != nil {
		t.Fatalf("compile mixed Knowledge routing closure: %v", err)
	}
	if compiled.Compilation == nil ||
		len(compiled.Compilation.KnowledgeRetrievals) != 1 ||
		len(compiled.Compilation.KnowledgeShortcuts) != 1 {
		t.Fatalf("mixed Knowledge compilation=%+v", compiled.Compilation)
	}
	return mixedKnowledgeRoutingClosure{
		run:                  run,
		bindings:             bindings,
		taskText:             task.Text,
		compileInput:         input,
		request:              compiled.Request,
		requestCanonical:     bytes.Clone(compiled.RequestCanonical),
		compilation:          *compiled.Compilation,
		compilationCanonical: bytes.Clone(compiled.CompilationCanonical),
	}
}

func routedKnowledgeConfigRecord(
	t *testing.T,
	source moduleapi.KnowledgeSourceRefV1,
	tag string,
	term string,
) (ContentRecord, []byte) {
	t.Helper()
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            source,
			MaxHits:           4,
			MaxTotalTextBytes: 4096,
			Routing: &moduleapi.KnowledgeRoutingPolicyV1{
				SchemaVersion:  moduleapi.KnowledgeRoutingPolicySchemaV1,
				CollectionTags: []string{tag},
				MatchTerms:     []string{term},
				MinMatchTerms:  1,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, canonical, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementUntrustedData,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    parameters,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return knowledgeContentRecord(t, ContentConfig, canonical), canonical
}

func selectedKnowledgeMaterialForRoutingTest(
	t *testing.T,
	binding frozenKnowledgeBinding,
	query string,
	rule moduleapi.KnowledgeScopeRuleV1,
) ([]byte, []byte) {
	t.Helper()
	request, requestCanonical, requestDigest, err :=
		moduleapi.NewKnowledgeContextRequestV1(
			moduleapi.KnowledgeContextRequestV1{
				SchemaVersion:     moduleapi.KnowledgeContextRequestSchemaV1,
				Source:            binding.Config.Source,
				Scope:             binding.Scope,
				QueryText:         query,
				MaxHits:           binding.MaxHits,
				MaxTotalTextBytes: binding.MaxTextBytes,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	chunk, _, err := moduleapi.NewKnowledgeChunkV1(
		moduleapi.KnowledgeChunkV1{
			Document: moduleapi.KnowledgeDocumentRefV1{
				ID:      "mixed-doc",
				Version: "v1",
				Digest:  strings.Repeat("e", 64),
			},
			ChunkID:   "mixed-chunk",
			Text:      "Shared source answer for the selected collection.",
			VisibleTo: []moduleapi.KnowledgeScopeRuleV1{rule},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, outputCanonical, _, err := moduleapi.NewKnowledgeContextOutputV1(
		moduleapi.KnowledgeContextOutputV1{
			SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
			RequestDigest: requestDigest,
			Source:        request.Source,
			Hits: []moduleapi.KnowledgeHitV1{{
				Rank:        1,
				Document:    chunk.Document,
				ChunkID:     chunk.ChunkID,
				ChunkDigest: chunk.ChunkDigest,
				Text:        chunk.Text,
				VisibleTo:   chunk.VisibleTo,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return requestCanonical, outputCanonical
}
