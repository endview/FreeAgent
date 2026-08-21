package bootstrapseed_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	localEchoAdapterIdentity = "freeagent.adapter.model.echo/v1"
	declarativeAdapter       = "freeagent.adapter.declarative/v1"
	knowledgeAdapterIdentity = "freeagent.adapter.knowledge.lexical/v1"
	textStatsAdapterIdentity = "freeagent.adapter.action.text-stats/v1"
)

func TestExampleSeedImportsThroughNormalAPIsAndIsIdempotent(t *testing.T) {
	prepared := prepareExample(t)
	assertion := prepared.ModelAssertion()
	if assertion.ModuleID != "freeagent.builtin.model.echo" ||
		assertion.ExactVersion != "1.0.0" ||
		assertion.ArtifactDigest == "" ||
		assertion.ArtifactSizeBytes != 36320 ||
		assertion.ArtifactDirectory == "" ||
		assertion.ExpectedAdapterIdentity != localEchoAdapterIdentity {
		t.Fatalf("ModelAssertion() = %+v", assertion)
	}
	if legacy := prepared.ModuleAssertion(); legacy != assertion {
		t.Fatalf("legacy ModuleAssertion() = %+v, want %+v", legacy, assertion)
	}
	assertions := prepared.ModuleAssertions()
	if len(assertions) != 2 || assertions[0] != assertion ||
		assertions[1].ModuleID != "freeagent.builtin.context.basic" ||
		assertions[1].ExactVersion != "1.0.0" ||
		assertions[1].ArtifactDigest != "5fd284dffdd51e8398d147b38ca220fbb939cec53a750b8189904483ee03d526" ||
		assertions[1].ArtifactSizeBytes != 35430 ||
		assertions[1].ArtifactDirectory == "" ||
		assertions[1].ExpectedExecutionClass != moduleapi.ExecutionDeclarative ||
		assertions[1].ExpectedAdapterIdentity != declarativeAdapter {
		t.Fatalf("ModuleAssertions() = %+v", assertions)
	}
	assertions[0].ModuleID = "mutated"
	if again := prepared.ModuleAssertions(); len(again) != 2 || again[0] != assertion {
		t.Fatalf("ModuleAssertions() leaked returned slice mutation: %+v", again)
	}
	if got := prepared.DefaultAssembly(); got != (bootstrapseed.DefaultAssembly{
		TenantID:    "default",
		WorkspaceID: "local-chat",
		AgentID:     "assistant",
		ProfileID:   "pure-chat",
	}) {
		t.Fatalf("DefaultAssembly() = %+v", got)
	}

	store := newStore(t)
	resolver := localResolver(t, assertion, true)
	first, err := prepared.Import(context.Background(), store, resolver)
	if err != nil {
		t.Fatalf("first Import() error = %v", err)
	}
	second, err := prepared.Import(context.Background(), store, resolver)
	if err != nil {
		t.Fatalf("second Import() error = %v", err)
	}
	if first.SeedID != "freeagent.local.pure-chat" ||
		first.SeedRevision != 1 ||
		first.PublishedBasis != second.PublishedBasis ||
		first.Installation.InstallationID != second.Installation.InstallationID ||
		first.Activation.ActivationID != second.Activation.ActivationID {
		t.Fatalf("idempotent results differ:\nfirst  %+v\nsecond %+v", first, second)
	}

	basis, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		"default",
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis() error = %v", err)
	}
	if basis != first.PublishedBasis || len(control.Agents) != 1 ||
		len(control.Workspaces) != 1 || len(control.Profiles) != 1 ||
		len(catalog.Entries) != 2 {
		t.Fatalf("published closure = %+v / %+v / %+v", basis, control, catalog)
	}
	profile := control.Profiles[0]
	if profile.ModelProfile != nil {
		t.Fatalf("example Pure Chat unexpectedly bound ModelProfile %+v", profile.ModelProfile)
	}
	if len(profile.Bindings) != 2 {
		t.Fatalf("profile Bindings = %+v", profile.Bindings)
	}
	var modelBinding, contextBinding controlcontract.BindingSpec
	for _, binding := range profile.Bindings {
		switch binding.Port.Name {
		case moduleapi.PortNameModelGenerate:
			modelBinding = binding
		case moduleapi.PortNameContextProvide:
			contextBinding = binding
		}
	}
	if modelBinding.InstanceID != "model-dev-echo" ||
		contextBinding.InstanceID != "context-basic" ||
		contextBinding.FailurePolicy != moduleapi.FailureRequired ||
		len(contextBinding.StaticContextRefs) != 1 {
		t.Fatalf("model/context Bindings = %+v", profile.Bindings)
	}
	config, err := store.GetContent(context.Background(), modelBinding.ConfigRef)
	if err != nil || config.Kind != currentstore.ContentConfig {
		t.Fatalf("GetContent(config) = %+v, %v", config, err)
	}
	if _, err := moduleapi.RestoreModelBindingConfigV1(config.CanonicalBytes); err != nil {
		t.Fatalf("RestoreModelBindingConfigV1() error = %v", err)
	}
	contextConfig, err := store.GetContent(
		context.Background(),
		contextBinding.ConfigRef,
	)
	if err != nil || contextConfig.Kind != currentstore.ContentConfig ||
		!bytes.Equal(
			contextConfig.CanonicalBytes,
			[]byte(`{"allow_drop":false,"allow_summary":false,"parameters":{},"placement":"TRUSTED_INSTRUCTION","schema_version":"context-binding-config/v1"}`),
		) {
		t.Fatalf("context config = %+v, %v", contextConfig, err)
	}
	if _, err := moduleapi.RestoreContextBindingConfigV1(
		contextConfig.CanonicalBytes,
	); err != nil {
		t.Fatalf("RestoreContextBindingConfigV1() error = %v", err)
	}
	staticContext, err := store.GetContent(
		context.Background(),
		contextBinding.StaticContextRefs[0],
	)
	if err != nil || staticContext.Kind != currentstore.ContentStaticContext {
		t.Fatalf("static context = %+v, %v", staticContext, err)
	}
	restoredContext, err := corecontract.RestoreStaticContextV1(
		staticContext.CanonicalBytes,
	)
	if err != nil || restoredContext.Text != "You are FreeAgent, a helpful assistant." {
		t.Fatalf("RestoreStaticContextV1() = %+v, %v", restoredContext, err)
	}
	contextCatalogEntry, found := catalog.FindInstance("context-basic")
	if !found || contextCatalogEntry.Activation.ExecutionClass != moduleapi.ExecutionDeclarative ||
		contextCatalogEntry.Activation.AdapterIdentity != declarativeAdapter ||
		len(contextCatalogEntry.Provides) != 1 ||
		contextCatalogEntry.Provides[0] != contextBinding.Port {
		t.Fatalf("context Catalog Entry = %+v, found=%v", contextCatalogEntry, found)
	}
	authority, err := store.GetContent(
		context.Background(),
		modelBinding.AuthorityCeilingRef,
	)
	if err != nil || authority.Kind != currentstore.ContentAuthorityCeiling ||
		!bytes.Equal(
			authority.CanonicalBytes,
			[]byte(`{"effects":[],"filesystem_roots":[],"network_allowlist":[],"schema_version":"authority-ceiling/v1","secret_refs":[]}`),
		) {
		t.Fatalf("deny-all AuthorityCeiling = %s, %v", authority.CanonicalBytes, err)
	}
	if contextBinding.AuthorityCeilingRef != modelBinding.AuthorityCeilingRef {
		t.Fatalf("context AuthorityCeilingRef = %s, model = %s", contextBinding.AuthorityCeilingRef, modelBinding.AuthorityCeilingRef)
	}
	for _, policy := range []corecontract.PolicyRef{
		control.Workspaces[0].BudgetPolicy,
		profile.ContextPolicy,
		profile.CostPolicy,
		profile.SchedulingPolicy,
	} {
		record, getErr := store.GetContent(context.Background(), policy.Digest)
		if getErr != nil || record.Kind != currentstore.ContentPolicy {
			t.Fatalf("GetContent(policy %s) = %+v, %v", policy.ID, record, getErr)
		}
		document, restoreErr := corecontract.RestorePolicyDocument(
			record.CanonicalBytes,
			policy,
		)
		if restoreErr != nil {
			t.Fatalf("RestorePolicyDocument(%s) error = %v", policy.ID, restoreErr)
		}
		if policy == profile.ContextPolicy && !bytes.Equal(
			document.Body,
			[]byte(`{"context_window_tokens":32768,"estimator_version":"canonical-json-utf8-byte-upper-bound/v1","recent_history_turns":8,"reserved_output_tokens":4096,"schema_version":"context-policy/v1"}`),
		) {
			t.Fatalf("ContextPolicy body = %s", document.Body)
		}
		if policy == profile.ContextPolicy {
			if _, contextErr := corecontract.RestoreContextPolicyV1(
				document.Body,
			); contextErr != nil {
				t.Fatalf("RestoreContextPolicyV1() error = %v", contextErr)
			}
		}
	}
	price, err := store.GetModelPriceSnapshot(
		context.Background(),
		"price-local-echo-v1",
	)
	if err != nil || price.Snapshot.PricingStatus != corecontract.PricingKnown {
		t.Fatalf("GetModelPriceSnapshot() = %+v, %v", price, err)
	}
}

func TestOptionalModelProfileImportsExactContentAndPublicationRef(
	t *testing.T,
) {
	seedPath := exampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	profiled := seedObject(t, canonical)
	profiled["model_profile"] = exactModelProfileSeed(t, profiled)

	prepared, err := bootstrapseed.Prepare(
		canonicalObject(t, profiled),
		filepath.Dir(seedPath),
	)
	if err != nil {
		t.Fatalf("Prepare() with ModelProfile error = %v", err)
	}
	store := newStore(t)
	if _, err := prepared.Import(
		context.Background(),
		store,
		localResolver(t, prepared.ModelAssertion(), true),
	); err != nil {
		t.Fatalf("Import() with ModelProfile error = %v", err)
	}
	_, control, _, err := store.LoadPublishedBasis(
		context.Background(),
		"default",
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis() error = %v", err)
	}
	if len(control.Profiles) != 1 || control.Profiles[0].ModelProfile == nil {
		t.Fatalf("published Profile has no ModelProfile: %+v", control.Profiles)
	}
	ref := *control.Profiles[0].ModelProfile
	record, err := store.GetContent(context.Background(), ref.Digest)
	if err != nil || record.Kind != currentstore.ContentConfig {
		t.Fatalf("GetContent(ModelProfile) = %+v, %v", record, err)
	}
	restored, err := corecontract.RestoreModelProfileV1(
		record.CanonicalBytes,
		ref,
	)
	if err != nil {
		t.Fatalf("RestoreModelProfileV1() error = %v", err)
	}
	if restored.ModelBuildID != "freeagent.builtin.model.echo/1.0.0" ||
		restored.AdapterArtifactDigest != prepared.ModelAssertion().ArtifactDigest ||
		restored.AdapterIdentity != localEchoAdapterIdentity ||
		restored.ContextWindowTokens != 16384 {
		t.Fatalf("restored ModelProfile = %+v", restored)
	}
}

func TestModelProfileMismatchFailsDuringPrepare(t *testing.T) {
	seedPath := exampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "model config ref",
			mutate: func(profile map[string]any) {
				profile["model_config_ref"] = strings.Repeat("a", 64)
			},
		},
		{
			name: "provider",
			mutate: func(profile map[string]any) {
				profile["provider"] = "another.provider"
			},
		},
		{
			name: "model build",
			mutate: func(profile map[string]any) {
				profile["model_build_id"] = "another-build"
			},
		},
		{
			name: "adapter artifact",
			mutate: func(profile map[string]any) {
				profile["adapter_artifact_digest"] = strings.Repeat("b", 64)
			},
		},
		{
			name: "adapter identity",
			mutate: func(profile map[string]any) {
				profile["adapter_identity"] = "another.adapter/v1"
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mismatched := seedObject(t, canonical)
			profile := exactModelProfileSeed(t, mismatched)
			test.mutate(profile)
			mismatched["model_profile"] = profile
			if _, err := bootstrapseed.Prepare(
				canonicalObject(t, mismatched),
				filepath.Dir(seedPath),
			); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
				t.Fatalf("Prepare() mismatch error = %v", err)
			}
		})
	}
}

func TestPrepareRejectsNoncanonicalUnknownSecretAndArtifactDrift(t *testing.T) {
	seedPath := exampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	seedHash := sha256.Sum256(canonical)
	if got, want := hex.EncodeToString(seedHash[:]),
		"34f712989fd31120c2c4ccc026d36ed9e365813747e6de8bf2ca85af9ddb5a3d"; got != want {
		t.Fatalf("canonical example seed SHA-256 = %s, want %s", got, want)
	}
	artifactBase := filepath.Dir(seedPath)

	if _, err := bootstrapseed.Prepare(
		append(append([]byte(nil), canonical...), '\n'),
		artifactBase,
	); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
		t.Fatalf("noncanonical Prepare() error = %v", err)
	}

	unknown := seedObject(t, canonical)
	unknown["unexpected"] = true
	if _, err := bootstrapseed.Prepare(
		canonicalObject(t, unknown),
		artifactBase,
	); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
		t.Fatalf("unknown-field Prepare() error = %v", err)
	}

	withForbiddenField := seedObject(t, canonical)
	definitions := withForbiddenField["definitions"].(map[string]any)
	agent := definitions["agent"].(map[string]any)
	body := agent["body"].(map[string]any)
	credentialField := "api_" + "key"
	body[credentialField] = "must-not-enter-seed"
	if _, err := bootstrapseed.Prepare(
		canonicalObject(t, withForbiddenField),
		artifactBase,
	); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
		t.Fatalf("secret-value Prepare() error = %v", err)
	}

	drift := seedObject(t, canonical)
	module := drift["module"].(map[string]any)
	module["artifact_digest"] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := bootstrapseed.Prepare(
		canonicalObject(t, drift),
		artifactBase,
	); !errors.Is(err, bootstrapseed.ErrArtifact) {
		t.Fatalf("artifact-drift Prepare() error = %v", err)
	}

	contextDrift := seedObject(t, canonical)
	providers := contextDrift["declarative_context_providers"].([]any)
	contextModule := providers[0].(map[string]any)["module"].(map[string]any)
	contextModule["artifact_digest"] = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, err := bootstrapseed.Prepare(
		canonicalObject(t, contextDrift),
		artifactBase,
	); !errors.Is(err, bootstrapseed.ErrArtifact) {
		t.Fatalf("context artifact-drift Prepare() error = %v", err)
	}

	wrongPort := seedObject(t, canonical)
	providers = wrongPort["declarative_context_providers"].([]any)
	port := providers[0].(map[string]any)["port"].(map[string]any)
	port["exact_version"] = "v2"
	if _, err := bootstrapseed.Prepare(
		canonicalObject(t, wrongPort),
		artifactBase,
	); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
		t.Fatalf("context wrong-port Prepare() error = %v", err)
	}

	nonminimalConfig := seedObject(t, canonical)
	providers = nonminimalConfig["declarative_context_providers"].([]any)
	providers[0].(map[string]any)["config"] = map[string]any{"enabled": true}
	if _, err := bootstrapseed.Prepare(
		canonicalObject(t, nonminimalConfig),
		artifactBase,
	); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
		t.Fatalf("context nonminimal-config Prepare() error = %v", err)
	}

	invalidContextPolicy := seedObject(t, canonical)
	policies := invalidContextPolicy["policies"].([]any)
	for _, raw := range policies {
		policy := raw.(map[string]any)
		if policy["alias"] == "context-pure-chat" {
			policy["body"] = map[string]any{"optional_repository_reads": false}
		}
	}
	if _, err := bootstrapseed.Prepare(
		canonicalObject(t, invalidContextPolicy),
		artifactBase,
	); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
		t.Fatalf("invalid ContextPolicy Prepare() error = %v", err)
	}
}

func TestContextBindingConfigRejectsUnsupportedRetentionGrants(t *testing.T) {
	seedPath := exampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name         string
		allowSummary bool
		allowDrop    bool
	}{
		{name: "summary", allowSummary: true},
		{name: "drop", allowDrop: true},
		{name: "summary_and_drop", allowSummary: true, allowDrop: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			configured := seedObject(t, canonical)
			providers := configured["declarative_context_providers"].([]any)
			providers[0].(map[string]any)["config"] = map[string]any{
				"allow_drop":     test.allowDrop,
				"allow_summary":  test.allowSummary,
				"parameters":     map[string]any{"namespace": "shared"},
				"placement":      "UNTRUSTED_DATA",
				"schema_version": "context-binding-config/v1",
			}
			if _, err := bootstrapseed.Prepare(
				canonicalObject(t, configured),
				filepath.Dir(seedPath),
			); !errors.Is(err, bootstrapseed.ErrInvalidSeed) {
				t.Fatalf("Prepare() retention-grant error = %v", err)
			}
		})
	}
}

func TestDeclarativeContextProvidersAreOptional(t *testing.T) {
	seedPath := exampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	withoutContext := seedObject(t, canonical)
	delete(withoutContext, "declarative_context_providers")
	prepared, err := bootstrapseed.Prepare(
		canonicalObject(t, withoutContext),
		filepath.Dir(seedPath),
	)
	if err != nil {
		t.Fatalf("Prepare() without declarative context error = %v", err)
	}
	assertions := prepared.ModuleAssertions()
	if len(assertions) != 1 || assertions[0] != prepared.ModelAssertion() {
		t.Fatalf("ModuleAssertions() without context = %+v", assertions)
	}
	store := newStore(t)
	if _, err := prepared.Import(
		context.Background(),
		store,
		localResolver(t, prepared.ModelAssertion(), true),
	); err != nil {
		t.Fatalf("Import() without declarative context error = %v", err)
	}
	_, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		"default",
	)
	if err != nil || len(control.Profiles) != 1 ||
		len(control.Profiles[0].Bindings) != 1 || len(catalog.Entries) != 1 {
		t.Fatalf("publication without context = %+v / %+v, error=%v", control, catalog, err)
	}
}

func TestKnowledgeContextProviderImportsThroughExistingModuleAndStorePaths(
	t *testing.T,
) {
	fixture := newKnowledgeSeedFixture(t)
	prepared, err := bootstrapseed.Prepare(
		canonicalObject(t, fixture.seed),
		fixture.artifactBase,
	)
	if err != nil {
		t.Fatalf("Prepare() with knowledge provider error = %v", err)
	}
	assertions := prepared.ModuleAssertions()
	if len(assertions) != 3 ||
		assertions[0] != prepared.ModelAssertion() ||
		assertions[1].ExpectedExecutionClass != moduleapi.ExecutionDeclarative ||
		assertions[2].ModuleID != "freeagent.builtin.knowledge.test" ||
		assertions[2].ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		assertions[2].ExpectedAdapterIdentity != knowledgeAdapterIdentity {
		t.Fatalf("ModuleAssertions() = %+v", assertions)
	}

	store := newStore(t)
	resolver := trustedResolverForAssertions(t, assertions)
	if _, err := prepared.Import(context.Background(), store, resolver); err != nil {
		t.Fatalf("Import() with knowledge provider error = %v", err)
	}
	_, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		"default",
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis() error = %v", err)
	}
	if len(control.Profiles) != 1 ||
		len(control.Profiles[0].Bindings) != 3 ||
		len(catalog.Entries) != 3 {
		t.Fatalf("knowledge publication = %+v / %+v", control, catalog)
	}
	bindings := control.Profiles[0].Bindings
	if bindings[0].Port.Name != moduleapi.PortNameContextProvide ||
		len(bindings[0].StaticContextRefs) != 1 ||
		bindings[1].Port.Name != moduleapi.PortNameContextProvide ||
		len(bindings[1].StaticContextRefs) != 0 ||
		bindings[1].FailurePolicy != moduleapi.FailureRequired ||
		bindings[2].Port.Name != moduleapi.PortNameModelGenerate {
		t.Fatalf("ordered Profile bindings = %+v", bindings)
	}

	dynamic := bindings[1]
	configRecord, err := store.GetContent(context.Background(), dynamic.ConfigRef)
	if err != nil || configRecord.Kind != currentstore.ContentConfig {
		t.Fatalf("knowledge config = %+v, %v", configRecord, err)
	}
	config, err := moduleapi.RestoreContextBindingConfigV1(
		configRecord.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("RestoreContextBindingConfigV1() error = %v", err)
	}
	binding, err := moduleapi.RestoreKnowledgeContextBindingParametersV1(config)
	if err != nil || binding.Source != fixture.sourceRef {
		t.Fatalf("knowledge binding = %+v, %v", binding, err)
	}
	authorityRecord, err := store.GetContent(
		context.Background(),
		dynamic.AuthorityCeilingRef,
	)
	if err != nil || authorityRecord.Kind != currentstore.ContentAuthorityCeiling {
		t.Fatalf("knowledge authority = %+v, %v", authorityRecord, err)
	}
	authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	)
	if err != nil || authority.Source != fixture.sourceRef ||
		len(authority.AllowedScopes) != 1 ||
		authority.AllowedScopes[0].TenantID != "default" {
		t.Fatalf("restored knowledge authority = %+v, %v", authority, err)
	}
	entry, found := catalog.FindInstance("knowledge-test")
	if !found ||
		entry.Activation.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		entry.Activation.AdapterIdentity != knowledgeAdapterIdentity ||
		len(entry.Provides) != 1 || entry.Provides[0] != dynamic.Port {
		t.Fatalf("knowledge Catalog Entry = %+v, found=%v", entry, found)
	}
}

func TestKnowledgeContextProvidersRemainOptionalForTheOldSeed(t *testing.T) {
	seedPath := exampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	oldSeed := seedObject(t, canonical)
	if _, present := oldSeed["knowledge_context_providers"]; present {
		t.Fatal("old example unexpectedly contains knowledge_context_providers")
	}
	if _, present := oldSeed["action_providers"]; present {
		t.Fatal("old example unexpectedly contains action_providers")
	}
	prepared, err := bootstrapseed.Prepare(canonical, filepath.Dir(seedPath))
	if err != nil {
		t.Fatalf("Prepare() old seed error = %v", err)
	}
	if assertions := prepared.ModuleAssertions(); len(assertions) != 2 {
		t.Fatalf("old seed assertions changed = %+v", assertions)
	}
}

func TestActionProviderImportsAsOptionalProfileBinding(t *testing.T) {
	prepared, err := bootstrapseed.PrepareFile(actionExampleSeedPath(t))
	if err != nil {
		t.Fatalf("PrepareFile(Action) error = %v", err)
	}
	assertions := prepared.ModuleAssertions()
	if len(assertions) != 3 || assertions[0] != prepared.ModelAssertion() ||
		assertions[1].ExpectedExecutionClass != moduleapi.ExecutionDeclarative ||
		assertions[2].ModuleID != "freeagent.builtin.action.text_stats" ||
		assertions[2].ArtifactDigest !=
			"2331b8b5209f4acffd7fef96f55ce1e0841bc3c4e39d90581efb941ecae9b955" ||
		assertions[2].ArtifactSizeBytes != 1936 ||
		assertions[2].ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		assertions[2].ExpectedAdapterIdentity != textStatsAdapterIdentity {
		t.Fatalf("Action ModuleAssertions() = %+v", assertions)
	}

	store := newStore(t)
	if _, err := prepared.Import(
		context.Background(),
		store,
		trustedResolverForAssertions(t, assertions),
	); err != nil {
		t.Fatalf("Import(Action) error = %v", err)
	}
	_, control, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		"default",
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis(Action) error = %v", err)
	}
	if len(control.Profiles) != 1 || len(control.Profiles[0].Bindings) != 3 ||
		len(catalog.Entries) != 3 {
		t.Fatalf("Action publication = %+v / %+v", control, catalog)
	}
	bindings := control.Profiles[0].Bindings
	if bindings[0].Port.Name != moduleapi.PortNameContextProvide ||
		bindings[1].Port.Name != moduleapi.PortNameActionProvider ||
		bindings[2].Port.Name != moduleapi.PortNameModelGenerate ||
		bindings[1].InstanceID != "action-text-stats" ||
		bindings[1].FailurePolicy != moduleapi.FailureRequired ||
		len(bindings[1].StaticContextRefs) != 0 {
		t.Fatalf("ordered Action Profile bindings = %+v", bindings)
	}
	actionConfigRecord, err := store.GetContent(
		context.Background(),
		bindings[1].ConfigRef,
	)
	if err != nil || actionConfigRecord.Kind != currentstore.ContentConfig {
		t.Fatalf("Action config = %+v, %v", actionConfigRecord, err)
	}
	actionConfig, err := moduleapi.RestoreActionBindingConfigV1(
		actionConfigRecord.CanonicalBytes,
	)
	if err != nil || len(actionConfig.Actions) != 1 ||
		actionConfig.Actions[0].PublicActionID != exactadapter.TextStatsActionIDV1 {
		t.Fatalf("restored Action config = %+v, %v", actionConfig, err)
	}
	authorityRecord, err := store.GetContent(
		context.Background(),
		bindings[1].AuthorityCeilingRef,
	)
	if err != nil || authorityRecord.Kind != currentstore.ContentAuthorityCeiling {
		t.Fatalf("Action authority = %+v, %v", authorityRecord, err)
	}
	authority, err := moduleapi.RestoreActionAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	)
	if err != nil || authority.TenantID != "default" ||
		len(authority.AllowedWorkspaceIDs) != 1 ||
		authority.AllowedWorkspaceIDs[0] != "local-chat" ||
		len(authority.AllowedProviderActionIDs) != 1 ||
		authority.AllowedProviderActionIDs[0] != exactadapter.TextStatsActionIDV1 {
		t.Fatalf("restored Action authority = %+v, %v", authority, err)
	}
	entry, found := catalog.FindInstance("action-text-stats")
	if !found || entry.Activation.AdapterIdentity != textStatsAdapterIdentity ||
		len(entry.Provides) != 1 || entry.Provides[0] != bindings[1].Port {
		t.Fatalf("Action Catalog Entry = %+v, found=%v", entry, found)
	}
}

func TestKnowledgeContextProviderRejectsWrongScopeSourceClassAndConfig(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "scope",
			mutate: func(provider map[string]any) {
				authority := provider["authority_ceiling"].(map[string]any)
				rules := authority["allowed_scopes"].([]any)
				rules[0].(map[string]any)["tenant_id"] = "other-tenant"
			},
		},
		{
			name: "source",
			mutate: func(provider map[string]any) {
				config := provider["config"].(map[string]any)
				parameters := config["parameters"].(map[string]any)
				source := parameters["source"].(map[string]any)
				source["digest"] = strings.Repeat("f", moduleapi.SHA256HexLength)
			},
		},
		{
			name: "class",
			mutate: func(provider map[string]any) {
				module := provider["module"].(map[string]any)
				module["expected_execution_class"] = string(moduleapi.ExecutionDeclarative)
			},
		},
		{
			name: "config",
			mutate: func(provider map[string]any) {
				config := provider["config"].(map[string]any)
				config["allow_drop"] = true
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newKnowledgeSeedFixture(t)
			providers := fixture.seed["knowledge_context_providers"].([]any)
			test.mutate(providers[0].(map[string]any))
			if _, err := bootstrapseed.Prepare(
				canonicalObject(t, fixture.seed),
				fixture.artifactBase,
			); err == nil {
				t.Fatalf("Prepare() accepted wrong knowledge %s", test.name)
			}
		})
	}
}

func TestSeedAssertionsCannotAuthorizeAnUnallowlistedAdapter(t *testing.T) {
	prepared := prepareExample(t)
	assertion := prepared.ModelAssertion()
	store := newStore(t)
	unallowlisted := localResolver(t, assertion, false)

	_, err := prepared.Import(context.Background(), store, unallowlisted)
	if !errors.Is(err, activationresolver.ErrTrustedModuleNotAllowlisted) {
		t.Fatalf("Import() error = %v, want local allowlist rejection", err)
	}
	if _, _, _, err := store.LoadPublishedBasis(
		context.Background(),
		"default",
	); !errors.Is(err, currentstore.ErrPublishedBasisNotFound) {
		t.Fatalf("LoadPublishedBasis() error = %v, want no publication", err)
	}

	// Content, price and installation staging are harmless until Catalog
	// publication. A later explicit import with real local authority can reuse
	// that exact staging without repair SQL or an alternate Store path.
	if _, err := prepared.Import(
		context.Background(),
		store,
		localResolver(t, assertion, true),
	); err != nil {
		t.Fatalf("authorized Import() after staged rejection error = %v", err)
	}
}

func prepareExample(t *testing.T) *bootstrapseed.Prepared {
	t.Helper()
	prepared, err := bootstrapseed.PrepareFile(exampleSeedPath(t))
	if err != nil {
		t.Fatalf("PrepareFile() error = %v", err)
	}
	return prepared
}

func exampleSeedPath(t *testing.T) string {
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
		"current-v1.bootstrap.seed.json",
	))
}

func actionExampleSeedPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Dir(exampleSeedPath(t)), "current-v1.action.bootstrap.seed.json")
}

func exactModelProfileSeed(
	t *testing.T,
	seed map[string]any,
) map[string]any {
	t.Helper()
	binding := seed["model_binding"].(map[string]any)
	configObject := binding["config"].(map[string]any)
	configInput := canonicalObject(t, configObject)
	var config moduleapi.ModelBindingConfigV1
	if err := json.Unmarshal(configInput, &config); err != nil {
		t.Fatalf("decode model Binding config: %v", err)
	}
	_, configCanonical, err := moduleapi.NewModelBindingConfigV1(config)
	if err != nil {
		t.Fatalf("NewModelBindingConfigV1() error = %v", err)
	}
	configRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentConfig,
		"application/json",
		configCanonical,
	)
	if err != nil {
		t.Fatalf("ComputeContentDigest(model config) error = %v", err)
	}
	module := seed["module"].(map[string]any)
	return map[string]any{
		"adapter_artifact_digest": module["artifact_digest"],
		"adapter_identity":        module["expected_adapter_identity"],
		"capability_tendencies": []any{
			map[string]any{
				"metric_id":          "instruction-following",
				"score_basis_points": 9000,
			},
		},
		"context_window_tokens":    16384,
		"evaluation_result_digest": strings.Repeat("e", 64),
		"evaluation_suite":         "freeagent.eval.echo",
		"evaluation_version":       "1",
		"id":                       "freeagent.model-profile.echo",
		"model":                    configObject["model"],
		"model_build_id":           configObject["model_build_id"],
		"model_config_ref":         configRef,
		"provider":                 configObject["provider"],
		"reliability_tendencies": []any{
			map[string]any{
				"metric_id":          "determinism",
				"score_basis_points": 10000,
			},
		},
		"schema_version": corecontract.ModelProfileSchemaVersionV1,
		"version":        "1",
	}
}

func newStore(t *testing.T) *currentstore.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(
		context.Background(),
		path,
	); err != nil {
		t.Fatalf("InitFreshCurrentStore() error = %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		path,
	)
	if err != nil {
		t.Fatalf("OpenExistingCurrentStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})
	return store
}

func localResolver(
	t *testing.T,
	assertion bootstrapseed.ModuleAssertion,
	allow bool,
) *activationresolver.Resolver {
	t.Helper()
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           assertion.ModuleID,
		Version:            assertion.ExactVersion,
		ArtifactDigest:     assertion.ArtifactDigest,
		InstanceID:         assertion.InstanceID,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    localEchoAdapterIdentity,
		ActivationRevision: assertion.ActivationRevision,
	}
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatalf("NewDeterministicEcho() error = %v", err)
	}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  assertion.ArtifactDigest,
		AdapterIdentity: localEchoAdapterIdentity,
		Invoker:         echo,
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	var allowlist []activationresolver.TrustedInProcessAllowlistEntry
	if allow {
		allowlist = []activationresolver.TrustedInProcessAllowlistEntry{{
			ModuleID:        assertion.ModuleID,
			ExactVersion:    assertion.ExactVersion,
			ArtifactDigest:  assertion.ArtifactDigest,
			AdapterIdentity: localEchoAdapterIdentity,
		}}
	}
	resolver, err := activationresolver.New(
		activationresolver.Config{
			DeclarativeAdapterIdentity: declarativeAdapter,
			TrustedInProcessAllowlist:  allowlist,
		},
		registry,
	)
	if err != nil {
		t.Fatalf("activationresolver.New() error = %v", err)
	}
	return resolver
}

type knowledgeSeedFixture struct {
	seed         map[string]any
	artifactBase string
	sourceRef    moduleapi.KnowledgeSourceRefV1
}

func newKnowledgeSeedFixture(t *testing.T) knowledgeSeedFixture {
	t.Helper()
	seedPath := exampleSeedPath(t)
	canonical, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	artifactBase := t.TempDir()
	copyBootstrapTestArtifacts(
		t,
		filepath.Join(filepath.Dir(seedPath), "bootstrap-artifacts"),
		filepath.Join(artifactBase, "bootstrap-artifacts"),
	)

	rule := moduleapi.KnowledgeScopeRuleV1{
		TenantID:     "default",
		WorkspaceID:  "local-chat",
		AgentID:      "assistant",
		TaskInputRef: "*",
	}
	chunk, _, err := moduleapi.NewKnowledgeChunkV1(moduleapi.KnowledgeChunkV1{
		Document: moduleapi.KnowledgeDocumentRefV1{
			ID:      "freeagent.test.guide",
			Version: "1",
			Digest: moduleapi.Digest(
				"freeagent.test.knowledge-document/v1",
				[]byte("guide-v1"),
			),
		},
		ChunkID:   "overview",
		Text:      "FreeAgent keeps shared knowledge independent from Agent and Workspace definitions.",
		VisibleTo: []moduleapi.KnowledgeScopeRuleV1{rule},
	})
	if err != nil {
		t.Fatalf("NewKnowledgeChunkV1() error = %v", err)
	}
	_, sourceCanonical, sourceRef, err := moduleapi.NewKnowledgeSourceV1(
		moduleapi.KnowledgeSourceV1{
			SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
			ID:            "freeagent.test.shared-knowledge",
			Version:       "1.0.0",
			Chunks:        []moduleapi.KnowledgeChunkV1{chunk},
		},
	)
	if err != nil {
		t.Fatalf("NewKnowledgeSourceV1() error = %v", err)
	}
	binding, bindingCanonical, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            sourceRef,
			MaxHits:           4,
			MaxTotalTextBytes: 4096,
		},
	)
	if err != nil {
		t.Fatalf("NewKnowledgeContextBindingV1() error = %v", err)
	}
	config, _, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementUntrustedData,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    bindingCanonical,
		},
	)
	if err != nil {
		t.Fatalf("NewContextBindingConfigV1() error = %v", err)
	}
	authority, _, err := moduleapi.NewKnowledgeAuthorityCeilingV1(
		moduleapi.KnowledgeAuthorityCeilingV1{
			SchemaVersion:     moduleapi.KnowledgeAuthorityCeilingSchemaV1,
			Source:            binding.Source,
			AllowedScopes:     []moduleapi.KnowledgeScopeRuleV1{rule},
			MaxHits:           8,
			MaxTotalTextBytes: 8192,
		},
	)
	if err != nil {
		t.Fatalf("NewKnowledgeAuthorityCeilingV1() error = %v", err)
	}

	relative := filepath.ToSlash(filepath.Join(
		"bootstrap-artifacts",
		"freeagent.builtin.knowledge.test",
		"1.0.0",
	))
	artifactDirectory := filepath.Join(
		artifactBase,
		filepath.FromSlash(relative),
	)
	if err := os.MkdirAll(
		filepath.Join(artifactDirectory, "content"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	manifestInput := canonicalObject(t, map[string]any{
		"api_version": moduleapi.ModuleManifestAPIVersionV1,
		"id":          "freeagent.builtin.knowledge.test",
		"provides": []any{map[string]any{
			"exact_version": moduleapi.PortVersionV1,
			"name":          moduleapi.PortNameContextProvide,
		}},
		"runtime": map[string]any{
			"entrypoint": "content/source.json",
			"mode":       string(moduleapi.RuntimeModeRequestTrustedInProcess),
			"protocol":   moduleapi.RuntimeProtocolGoInProcessV1,
		},
		"version": "1.0.0",
	})
	_, manifestCanonical, err := moduleapi.ParseModuleManifestV1(manifestInput)
	if err != nil {
		t.Fatalf("ParseModuleManifestV1() error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(artifactDirectory, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(artifactDirectory, "content", "source.json"),
		sourceCanonical,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatalf("ScanArtifactDirectory() error = %v", err)
	}
	artifactDigest, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		files,
	)
	if err != nil {
		t.Fatalf("ComputeArtifactDigest() error = %v", err)
	}
	artifactSize := uint64(len(manifestCanonical))
	for _, file := range files {
		artifactSize += uint64(len(file.Content))
	}

	seed := seedObject(t, canonical)
	seed["knowledge_context_providers"] = []any{map[string]any{
		"authority_ceiling": jsonTestValue(t, authority),
		"config":            jsonTestValue(t, config),
		"failure_policy":    string(moduleapi.FailureRequired),
		"module": map[string]any{
			"activation_revision":       1,
			"artifact_digest":           artifactDigest,
			"artifact_relative_path":    relative,
			"artifact_size_bytes":       artifactSize,
			"exact_version":             "1.0.0",
			"expected_adapter_identity": knowledgeAdapterIdentity,
			"expected_execution_class":  string(moduleapi.ExecutionTrustedInProcess),
			"installation_id":           "installation-freeagent-builtin-knowledge-test-1",
			"instance_id":               "knowledge-test",
			"module_id":                 "freeagent.builtin.knowledge.test",
		},
		"port": map[string]any{
			"exact_version": moduleapi.PortVersionV1,
			"name":          moduleapi.PortNameContextProvide,
		},
	}}
	return knowledgeSeedFixture{
		seed:         seed,
		artifactBase: artifactBase,
		sourceRef:    sourceRef,
	}
}

func trustedResolverForAssertions(
	t *testing.T,
	assertions []bootstrapseed.ModuleAssertion,
) *activationresolver.Resolver {
	t.Helper()
	var registrations []exactadapter.Registration
	var allowlist []activationresolver.TrustedInProcessAllowlistEntry
	for _, assertion := range assertions {
		if assertion.ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess {
			continue
		}
		provider := moduleapi.ActivatedModuleRef{
			ModuleID:           assertion.ModuleID,
			Version:            assertion.ExactVersion,
			ArtifactDigest:     assertion.ArtifactDigest,
			InstanceID:         assertion.InstanceID,
			ExecutionClass:     assertion.ExpectedExecutionClass,
			AdapterIdentity:    assertion.ExpectedAdapterIdentity,
			ActivationRevision: assertion.ActivationRevision,
		}
		var invoker modulehost.ModuleInvoker
		var err error
		if assertion.ExpectedAdapterIdentity == textStatsAdapterIdentity {
			invoker, err = exactadapter.NewTextStatsAction(provider)
		} else {
			invoker, err = exactadapter.NewDeterministicEcho(provider)
		}
		if err != nil {
			t.Fatalf("build test adapter for %s: %v", assertion.ModuleID, err)
		}
		registrations = append(registrations, exactadapter.Registration{
			ArtifactDigest:  assertion.ArtifactDigest,
			AdapterIdentity: assertion.ExpectedAdapterIdentity,
			Invoker:         invoker,
		})
		allowlist = append(
			allowlist,
			activationresolver.TrustedInProcessAllowlistEntry{
				ModuleID:        assertion.ModuleID,
				ExactVersion:    assertion.ExactVersion,
				ArtifactDigest:  assertion.ArtifactDigest,
				AdapterIdentity: assertion.ExpectedAdapterIdentity,
			},
		)
	}
	registry, err := exactadapter.NewRegistry(registrations...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	resolver, err := activationresolver.New(
		activationresolver.Config{
			DeclarativeAdapterIdentity: declarativeAdapter,
			TrustedInProcessAllowlist:  allowlist,
		},
		registry,
	)
	if err != nil {
		t.Fatalf("activationresolver.New() error = %v", err)
	}
	return resolver
}

func copyBootstrapTestArtifacts(t *testing.T, sourceRoot, targetRoot string) {
	t.Helper()
	if err := filepath.Walk(
		sourceRoot,
		func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(sourceRoot, path)
			if err != nil {
				return err
			}
			target := filepath.Join(targetRoot, relative)
			if info.IsDir() {
				return os.MkdirAll(target, info.Mode().Perm())
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, content, info.Mode().Perm())
		},
	); err != nil {
		t.Fatalf("copy bootstrap test artifacts: %v", err)
	}
}

func jsonTestValue(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func seedObject(t *testing.T, canonical []byte) map[string]any {
	t.Helper()
	var result map[string]any
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func canonicalObject(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
