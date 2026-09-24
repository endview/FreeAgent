package bootstrapseed_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	deepSeekAdapterIdentity = "freeagent.adapter.model.deepseek/v1"
	deepSeekSeedSHA256      = "c4420be8fc6e5805ab8169f5e6c55f17b5a75f59a2378e370effe29b067917b5"
)

func TestDeepSeekPureChatSeedImportsExactSingleProviderClosure(t *testing.T) {
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.deepseek.bootstrap.seed.json",
	)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	if got := hex.EncodeToString(digest[:]); got != deepSeekSeedSHA256 {
		t.Fatalf("DeepSeek seed SHA-256=%s, want %s", got, deepSeekSeedSHA256)
	}

	prepared, err := bootstrapseed.PrepareFile(seedPath)
	if err != nil {
		t.Fatalf("PrepareFile DeepSeek seed: %v", err)
	}
	if got := prepared.DefaultAssembly(); got != (bootstrapseed.DefaultAssembly{
		TenantID:    "default",
		WorkspaceID: "local-chat",
		AgentID:     "assistant",
		ProfileID:   "deepseek-chat",
	}) {
		t.Fatalf("DeepSeek default assembly=%+v", got)
	}
	assertions := prepared.ModuleAssertions()
	if len(assertions) != 1 {
		t.Fatalf("DeepSeek Pure Chat assertions=%+v, want model only", assertions)
	}
	model := assertions[0]
	if model != prepared.ModelAssertion() ||
		model.ModuleID != "freeagent.builtin.model.deepseek" ||
		model.ExactVersion != "2.0.0" ||
		model.ArtifactDigest != "ca4c08b5070f006652df78863c86285d4c6f842c623fa9408acd173754f89d71" ||
		model.ArtifactSizeBytes != 2762 ||
		model.InstanceID != "model-deepseek-v4-flash" ||
		model.ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		model.ExpectedAdapterIdentity != deepSeekAdapterIdentity {
		t.Fatalf("DeepSeek model assertion=%+v", model)
	}

	store := newStore(t)
	result, err := prepared.Import(
		context.Background(),
		store,
		trustedResolverForAssertions(t, assertions),
	)
	if err != nil {
		t.Fatalf("Import DeepSeek seed: %v", err)
	}
	if result.SeedID != "freeagent.local.deepseek-chat" ||
		result.SeedRevision != 1 {
		t.Fatalf("DeepSeek import identity=%+v", result)
	}

	_, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		"default",
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis DeepSeek: %v", err)
	}
	if len(control.Agents) != 1 || len(control.Workspaces) != 1 ||
		len(control.Profiles) != 1 || len(catalog.Entries) != 1 {
		t.Fatalf("DeepSeek publication=%+v / %+v", control, catalog)
	}
	profile := control.Profiles[0]
	if profile.Profile.ID != "deepseek-chat" || profile.ModelProfile != nil ||
		len(profile.Bindings) != 1 ||
		profile.Bindings[0].Port.Name != moduleapi.PortNameModelGenerate ||
		profile.Bindings[0].InstanceID != model.InstanceID {
		t.Fatalf("DeepSeek Profile closure=%+v", profile)
	}
	entry, found := catalog.FindInstance(model.InstanceID)
	if !found || entry.Activation.ModuleID != model.ModuleID ||
		entry.Activation.ArtifactDigest != model.ArtifactDigest ||
		entry.Activation.AdapterIdentity != deepSeekAdapterIdentity ||
		len(entry.Provides) != 1 || entry.Provides[0] != profile.Bindings[0].Port {
		t.Fatalf("DeepSeek Catalog entry=%+v found=%v", entry, found)
	}
	configRecord, err := store.GetContent(
		context.Background(),
		profile.Bindings[0].ConfigRef,
	)
	if err != nil || configRecord.Kind != currentstore.ContentConfig {
		t.Fatalf("DeepSeek Config=%+v err=%v", configRecord, err)
	}
	config, err := moduleapi.RestoreModelBindingConfigV2(
		configRecord.CanonicalBytes,
	)
	if err != nil || config.Provider != "deepseek" ||
		config.Model != "deepseek-v4-flash" ||
		config.ModelBuildID != "deepseek-v4-flash/public-alias-observed-2026-08-04" {
		t.Fatalf("DeepSeek restored Config=%+v err=%v", config, err)
	}
}
