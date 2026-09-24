package bootstrapseed_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestZhipuGLMSeedImportsExactProviderClosure(t *testing.T) {
	seedPath := filepath.Join(filepath.Dir(exampleSeedPath(t)), "current-v1.zhipu.bootstrap.seed.json")
	prepared, err := bootstrapseed.PrepareFile(seedPath)
	if err != nil {
		t.Fatalf("PrepareFile: %v", err)
	}
	model := prepared.ModelAssertion()
	if model.ModuleID != "freeagent.builtin.model.zhipu" || model.ExactVersion != "2.0.0" || model.ArtifactDigest != "ee7cc5bf3d5bcb003219b83c2367601a122cfcfc4abcb25065f1de15212bfcfa" || model.ArtifactSizeBytes != 2124 || model.ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess || model.ExpectedAdapterIdentity != "freeagent.adapter.model.zhipu/v1" {
		t.Fatalf("model assertion=%+v", model)
	}
	store := newStore(t)
	result, err := prepared.Import(context.Background(), store, trustedResolverForAssertions(t, prepared.ModuleAssertions()))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.SeedID != "freeagent.local.zhipu-chat" || result.SeedRevision != 1 {
		t.Fatalf("result=%+v", result)
	}
	_, control, catalog, err := store.LoadPublishedBasis(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	entry, found := catalog.FindInstance(model.InstanceID)
	if !found || entry.Activation.AdapterIdentity != "freeagent.adapter.model.zhipu/v1" {
		t.Fatalf("catalog entry=%+v found=%v", entry, found)
	}
	configRef := control.Profiles[0].Bindings[0].ConfigRef
	configRecord, err := store.GetContent(context.Background(), configRef)
	if err != nil || configRecord.Kind != currentstore.ContentConfig {
		t.Fatalf("config=%+v err=%v", configRecord, err)
	}
	config, err := moduleapi.RestoreModelBindingConfigV2(configRecord.CanonicalBytes)
	if err != nil || config.Provider != "zhipu" || config.Model != "glm-4.5" || config.ModelBuildID != "glm-4.5/public-alias-observed-2026-09-22" {
		t.Fatalf("config=%+v err=%v", config, err)
	}
}
