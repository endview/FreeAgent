package contextcompiler

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompileV1KnowledgeRoutingSelectsAndHonestlySkipsPromptUnit(t *testing.T) {
	input := newCompileInput(t, "design an API gateway")
	selected := addKnowledgeContext(t, &input, "selected backend evidence")
	routeKnowledgeFixture(t, &input, selected.BindingIndex, []string{"backend"}, []string{"api"}, 1, true)
	skipped := addKnowledgeContext(t, &input, "skipped frontend evidence")
	routeKnowledgeFixture(t, &input, skipped.BindingIndex, []string{"frontend"}, []string{"css"}, 1, false)

	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compilation == nil ||
		len(result.Compilation.KnowledgeRetrievals) != 1 ||
		len(result.Compilation.KnowledgeShortcuts) != 1 {
		t.Fatalf("routing evidence=%+v", result.Compilation)
	}
	if result.Compilation.KnowledgeRetrievals[0].BindingIndex != selected.BindingIndex {
		t.Fatalf("selected retrieval=%+v", result.Compilation.KnowledgeRetrievals)
	}
	shortcut := result.Compilation.KnowledgeShortcuts[0]
	wantBinding := input.ContextPlan.Bindings[skipped.BindingIndex]
	if shortcut.BindingIndex != skipped.BindingIndex ||
		shortcut.ConfigRef != wantBinding.ConfigRef ||
		shortcut.AuthorityCeilingRef != wantBinding.AuthorityCeilingRef ||
		shortcut.Scope != skipped.Request.Scope || shortcut.Source != skipped.Request.Source ||
		shortcut.Mode != corecontract.KnowledgeShortcutNotSelectedV1 ||
		!moduleapi.ValidSHA256(shortcut.DecisionSetDigest) ||
		!moduleapi.ValidSHA256(shortcut.ExactQuestionFingerprint) ||
		!reflect.DeepEqual(shortcut.CollectionTags, []string{"frontend"}) ||
		len(shortcut.MatchedTerms) != 0 || shortcut.MinMatchTerms != 1 {
		t.Fatalf("shortcut did not close exact inputs: %+v", shortcut)
	}
	if len(result.Request.Messages) != 3 ||
		!strings.Contains(result.Request.Messages[1].Content, "selected backend evidence") ||
		bytes.Contains(result.RequestCanonical, []byte("skipped frontend evidence")) {
		t.Fatalf("selected/skipped prompt=%+v", result.Request.Messages)
	}
	if bytes.Contains(result.CompilationCanonical, []byte(`"hits":[]`)) {
		t.Fatal("NOT_SELECTED was disguised as a zero-hit retrieval")
	}
}

func TestCompileV1KnowledgeRoutingNoMatchFallsBackToAllFresh(t *testing.T) {
	input := newCompileInput(t, "write a poem")
	first := addKnowledgeContext(t, &input, "backend fallback")
	routeKnowledgeFixture(t, &input, first.BindingIndex, []string{"backend"}, []string{"api"}, 1, true)
	second := addKnowledgeContext(t, &input, "frontend fallback")
	routeKnowledgeFixture(t, &input, second.BindingIndex, []string{"frontend"}, []string{"css"}, 1, true)
	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compilation == nil ||
		len(result.Compilation.KnowledgeRetrievals) != 2 ||
		len(result.Compilation.KnowledgeShortcuts) != 0 ||
		!bytes.Contains(result.RequestCanonical, []byte("backend fallback")) ||
		!bytes.Contains(result.RequestCanonical, []byte("frontend fallback")) {
		t.Fatalf("low-confidence fallback=%+v %+v", result.Compilation, result.Request.Messages)
	}
}

func TestCompileV1KnowledgeRoutingAmbiguitySelectsEveryMatch(t *testing.T) {
	input := newCompileInput(t, "API architecture")
	first := addKnowledgeContext(t, &input, "backend match")
	routeKnowledgeFixture(t, &input, first.BindingIndex, []string{"backend"}, []string{"api"}, 1, true)
	second := addKnowledgeContext(t, &input, "network match")
	routeKnowledgeFixture(t, &input, second.BindingIndex, []string{"network"}, []string{"api"}, 1, true)
	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compilation == nil ||
		len(result.Compilation.KnowledgeRetrievals) != 2 ||
		len(result.Compilation.KnowledgeShortcuts) != 0 {
		t.Fatalf("ambiguous matches=%+v", result.Compilation)
	}
}

func TestCompileV1KnowledgeShortcutMaterialsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *CompileInputV1, uint32)
	}{
		{"request present", func(_ *testing.T, input *CompileInputV1, index uint32) {
			input.ContextBindings[index].DynamicRequestCanonical = []byte(`{}`)
		}},
		{"output present", func(_ *testing.T, input *CompileInputV1, index uint32) {
			input.ContextBindings[index].DynamicOutputCanonical = []byte(`{}`)
		}},
		{"state present", func(_ *testing.T, input *CompileInputV1, index uint32) {
			input.ContextBindings[index].DynamicStateCanonical = []byte(`{}`)
		}},
		{"authority denied", func(t *testing.T, input *CompileInputV1, index uint32) {
			authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(
				input.ContextBindings[index].AuthorityCanonical,
			)
			if err != nil {
				t.Fatal(err)
			}
			authority.AllowedScopes[0].AgentID = "other-agent"
			_, canonical, err := moduleapi.NewKnowledgeAuthorityCeilingV1(authority)
			if err != nil {
				t.Fatal(err)
			}
			input.ContextBindings[index].AuthorityCanonical = canonical
			input.ContextPlan.Bindings[index].AuthorityCeilingRef = contentDigest(
				"AUTHORITY_CEILING", jsonMediaType, canonical,
			)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := newCompileInput(t, "API design")
			selected := addKnowledgeContext(t, &input, "selected")
			routeKnowledgeFixture(t, &input, selected.BindingIndex, []string{"backend"}, []string{"api"}, 1, true)
			skipped := addKnowledgeContext(t, &input, "skipped")
			routeKnowledgeFixture(t, &input, skipped.BindingIndex, []string{"frontend"}, []string{"css"}, 1, false)
			test.mutate(t, &input, skipped.BindingIndex)
			if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
				t.Fatalf("error=%v, want ErrInvalidContextInput", err)
			}
		})
	}
}

func routeKnowledgeFixture(
	t *testing.T,
	input *CompileInputV1,
	index uint32,
	tags []string,
	terms []string,
	minimum uint32,
	keepDynamic bool,
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
	binding.Routing = &moduleapi.KnowledgeRoutingPolicyV1{
		SchemaVersion:  moduleapi.KnowledgeRoutingPolicySchemaV1,
		CollectionTags: append([]string(nil), tags...),
		MatchTerms:     append([]string(nil), terms...),
		MinMatchTerms:  minimum,
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
		"CONFIG", jsonMediaType, canonical,
	)
	if !keepDynamic {
		input.ContextBindings[index].DynamicStateCanonical = nil
		input.ContextBindings[index].DynamicRequestCanonical = nil
		input.ContextBindings[index].DynamicOutputCanonical = nil
	}
}
