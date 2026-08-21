package bootstrapseed_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const memoryAdapterIdentity = "freeagent.adapter.memory.deterministic/v1"

func TestDefaultSeedHasNoMemoryArtifactBindingOrGenesis(t *testing.T) {
	seedPath := exampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	seed := seedObject(t, canonical)
	if _, present := seed["memory_context_providers"]; present {
		t.Fatal("default Pure Chat seed unexpectedly contains Memory")
	}
	prepared, err := bootstrapseed.Prepare(canonical, filepath.Dir(seedPath))
	if err != nil {
		t.Fatalf("Prepare default seed: %v", err)
	}
	assertions := prepared.ModuleAssertions()
	if len(assertions) != 2 {
		t.Fatalf("default assertions=%+v, want model and declarative context only", assertions)
	}
	store := newStore(t)
	if _, err := prepared.Import(
		context.Background(),
		store,
		trustedResolverForAssertions(t, assertions),
	); err != nil {
		t.Fatalf("Import default seed: %v", err)
	}
	if _, err := store.GetCurrentAgentMemory(
		context.Background(),
		"default",
		"assistant",
	); !errors.Is(err, currentstore.ErrAgentMemoryNotFound) {
		t.Fatalf("default seed Memory head error=%v, want not found", err)
	}
}

func TestMemorySeedImportsExactBindingAuthorityArtifactAndIdempotentGenesis(
	t *testing.T,
) {
	prepared, err := bootstrapseed.PrepareFile(memoryExampleSeedPath(t))
	if err != nil {
		t.Fatalf("PrepareFile Memory example: %v", err)
	}
	assertions := prepared.ModuleAssertions()
	if len(assertions) != 3 ||
		assertions[0] != prepared.ModelAssertion() ||
		assertions[1].ExpectedExecutionClass != moduleapi.ExecutionDeclarative ||
		assertions[2].ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		assertions[2].ExpectedAdapterIdentity != memoryAdapterIdentity {
		t.Fatalf("Memory assertions=%+v", assertions)
	}
	store := newStore(t)
	resolver := trustedResolverForAssertions(t, assertions)
	first, err := prepared.Import(context.Background(), store, resolver)
	if err != nil {
		t.Fatalf("Import Memory seed: %v", err)
	}
	if first.DefaultAssembly.ProfileID != "memory-chat" {
		t.Fatalf("Memory default assembly=%+v", first.DefaultAssembly)
	}
	// Re-import must verify/reuse the exact module publication and genesis; it
	// must not append a second empty revision.
	if _, err := prepared.Import(context.Background(), store, resolver); err != nil {
		t.Fatalf("idempotent Memory import: %v", err)
	}
	head, err := store.GetCurrentAgentMemory(
		context.Background(),
		"default",
		"assistant",
	)
	if err != nil {
		t.Fatalf("GetCurrentAgentMemory: %v", err)
	}
	if head.SnapshotRef.Revision != 1 || len(head.Snapshot.Entries) != 0 ||
		head.Snapshot.SourceAttemptID != "" {
		t.Fatalf("Memory genesis=%+v", head)
	}

	_, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		"default",
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis: %v", err)
	}
	if len(control.Profiles) != 1 || len(control.Profiles[0].Bindings) != 3 ||
		len(catalog.Entries) != 3 {
		t.Fatalf("Memory publication=%+v / %+v", control, catalog)
	}
	binding := control.Profiles[0].Bindings[1]
	if binding.Port != (moduleapi.PortRef{
		Name: moduleapi.PortNameContextProvide, ExactVersion: moduleapi.PortVersionV1,
	}) || binding.FailurePolicy != moduleapi.FailureRequired ||
		len(binding.StaticContextRefs) != 0 {
		t.Fatalf("Memory Binding=%+v", binding)
	}
	configRecord, err := store.GetContent(context.Background(), binding.ConfigRef)
	if err != nil || configRecord.Kind != currentstore.ContentConfig {
		t.Fatalf("Memory CONFIG=%+v, %v", configRecord, err)
	}
	contextConfig, err := moduleapi.RestoreContextBindingConfigV1(
		configRecord.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	memoryConfig, _, err := moduleapi.RestoreMemoryContextBindingParametersV1(
		contextConfig,
	)
	if err != nil || memoryConfig.SummaryMaxTextBytes < 4 {
		t.Fatalf("Memory parameters=%+v, %v", memoryConfig, err)
	}
	authorityRecord, err := store.GetContent(
		context.Background(),
		binding.AuthorityCeilingRef,
	)
	if err != nil || authorityRecord.Kind != currentstore.ContentAuthorityCeiling {
		t.Fatalf("Memory authority record=%+v, %v", authorityRecord, err)
	}
	authority, err := moduleapi.RestoreMemoryAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	)
	if err != nil || authority.TenantID != "default" ||
		authority.AgentID != "assistant" ||
		len(authority.AllowedWorkspaceIDs) != 1 ||
		authority.AllowedWorkspaceIDs[0] != "local-chat" {
		t.Fatalf("Memory authority=%+v, %v", authority, err)
	}
	entry, found := catalog.FindInstance("memory-deterministic")
	if !found || entry.Activation.AdapterIdentity != memoryAdapterIdentity ||
		entry.Activation.ArtifactDigest != assertions[2].ArtifactDigest ||
		len(entry.Provides) != 1 || entry.Provides[0] != binding.Port {
		t.Fatalf("Memory Catalog Entry=%+v, found=%v", entry, found)
	}
}

func TestMemorySeedRejectsNonExactAuthorityConfigClassAndMultiplicity(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "tenant",
			mutate: func(provider map[string]any) {
				provider["authority_ceiling"].(map[string]any)["tenant_id"] = "other"
			},
		},
		{
			name: "agent",
			mutate: func(provider map[string]any) {
				provider["authority_ceiling"].(map[string]any)["agent_id"] = "other"
			},
		},
		{
			name: "workspace wildcard",
			mutate: func(provider map[string]any) {
				provider["authority_ceiling"].(map[string]any)["allowed_workspace_ids"] = []any{"*"}
			},
		},
		{
			name: "optional",
			mutate: func(provider map[string]any) {
				provider["failure_policy"] = string(moduleapi.FailureOptional)
			},
		},
		{
			name: "declarative class",
			mutate: func(provider map[string]any) {
				provider["module"].(map[string]any)["expected_execution_class"] = string(moduleapi.ExecutionDeclarative)
			},
		},
		{
			name: "wrong protocol",
			mutate: func(provider map[string]any) {
				provider["config"].(map[string]any)["parameters"].(map[string]any)["schema_version"] = moduleapi.KnowledgeContextBindingSchemaV1
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			seedPath := memoryExampleSeedPath(t)
			canonical, err := os.ReadFile(seedPath)
			if err != nil {
				t.Fatal(err)
			}
			seed := seedObject(t, canonical)
			providers := seed["memory_context_providers"].([]any)
			test.mutate(providers[0].(map[string]any))
			if _, err := bootstrapseed.Prepare(
				canonicalObject(t, seed),
				filepath.Dir(seedPath),
			); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
				t.Fatalf("Prepare mutation error=%v, want ErrInvalidSeed", err)
			}
		})
	}

	t.Run("multiple", func(t *testing.T) {
		seedPath := memoryExampleSeedPath(t)
		canonical, err := os.ReadFile(seedPath)
		if err != nil {
			t.Fatal(err)
		}
		seed := seedObject(t, canonical)
		providers := seed["memory_context_providers"].([]any)
		seed["memory_context_providers"] = append(providers, providers[0])
		if _, err := bootstrapseed.Prepare(
			canonicalObject(t, seed),
			filepath.Dir(seedPath),
		); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
			t.Fatalf("multiple Memory error=%v", err)
		}
	})
}

func memoryExampleSeedPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(
		filepath.Dir(source),
		"..",
		"..",
		"examples",
		"current-v1.memory.bootstrap.seed.json",
	))
}
