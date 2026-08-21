package bootstrapseed_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	deepSeekAdapterIdentity = "freeagent.adapter.model.deepseek/v1"
	deepSeekSeedSHA256      = "0103b85f0513900a09c6fd04b38fd94f3a8d50185493d84010b6c60f4f462d48"
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
		model.ExactVersion != "1.0.0" ||
		model.ArtifactDigest != "e7864f4478a588dad4de9fff53b0c4dcecc17420e80420018bcc5fe5502887c3" ||
		model.ArtifactSizeBytes != 2914 ||
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
	config, err := moduleapi.RestoreModelBindingConfigV1(
		configRecord.CanonicalBytes,
	)
	if err != nil || config.Provider != "deepseek" ||
		config.Model != "deepseek-v4-flash" ||
		config.ModelBuildID != "deepseek-v4-flash/public-alias-observed-2026-08-04" ||
		config.PriceSnapshotID != "price-deepseek-v4-flash-2026-08-04" {
		t.Fatalf("DeepSeek restored Config=%+v err=%v", config, err)
	}
	price, err := store.GetModelPriceSnapshot(
		context.Background(),
		config.PriceSnapshotID,
	)
	if err != nil || price.Snapshot.Provider != config.Provider ||
		price.Snapshot.Model != config.Model ||
		price.Snapshot.Currency != "CNY" ||
		price.Snapshot.PricingStatus != corecontract.PricingKnown {
		t.Fatalf("DeepSeek PriceSnapshot=%+v err=%v", price, err)
	}
}
