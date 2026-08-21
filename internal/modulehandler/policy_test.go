package modulehandler

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestFrozenHandlerTablesAreExactOrderedAndDetached(t *testing.T) {
	t.Parallel()
	generic := GenericTableV1()
	if len(generic) != 9 {
		t.Fatalf("generic handler rows=%d", len(generic))
	}
	wantKinds := [9]KindV1{
		HandlerDeepSeekModelV1,
		HandlerDeclarativeContextV1,
		HandlerKnowledgeContextV1,
		HandlerMemoryContextV1,
		HandlerMCPActionV1,
		HandlerRemoteActionHTTPV1,
		HandlerWASMActionV1,
		HandlerTextStatsActionV1,
		HandlerLoopbackChannelV1,
	}
	for index, policy := range generic {
		if policy.HandlerKind != wantKinds[index] {
			t.Fatalf("generic row %d kind=%q", index, policy.HandlerKind)
		}
		resolved, err := ResolveGenericV1(policy.KeyV1())
		if err != nil || resolved != policy {
			t.Fatalf("generic row %d resolved=%+v error=%v", index, resolved, err)
		}
	}
	exact := ExactSelectorTableV1()
	if len(exact) != 2 || exact[0].Port != contextPortV1() || exact[1].Port != actionPortV1() {
		t.Fatalf("reserved exact selector rows=%+v", exact)
	}
	for index, policy := range exact {
		resolved, err := ResolveExactSelectorV1(policy.KeyV1())
		if err != nil || resolved != policy {
			t.Fatalf("exact row %d resolved=%+v error=%v", index, resolved, err)
		}
	}

	generic[1].AdapterIdentity = "caller-mutated"
	if GenericTableV1()[1].AdapterIdentity != DeclarativeAdapterIdentityV1 {
		t.Fatal("generic registry retained caller mutation")
	}
	builds := DeepSeekBuildsV1()
	builds[DeepSeekModelV4FlashV1] = "caller-mutated"
	if DeepSeekBuildsV1()[DeepSeekModelV4FlashV1] != DeepSeekFlashBuildV1 {
		t.Fatal("DeepSeek build registry retained caller mutation")
	}
	authority := DenyAllAuthorityCanonicalV1()
	authority[0] = '['
	if !bytes.Equal(DenyAllAuthorityCanonicalV1(), []byte(denyAllAuthorityCanonicalV1)) {
		t.Fatal("deny-all authority retained caller mutation")
	}
}

func TestHandlerTableRejectsDuplicateMalformedAndUnknown(t *testing.T) {
	t.Parallel()
	table := GenericTableV1()
	duplicate := append(table[:], table[0])
	if _, err := ResolveFromTableV1(duplicate, table[1].KeyV1()); err == nil ||
		!strings.Contains(err.Error(), "duplicate exact key") {
		t.Fatalf("duplicate error=%v", err)
	}
	malformed := table
	malformed[5].ExecutionClass = moduleapi.ExecutionLocalProcess
	if err := ValidateTableV1(malformed[:]); err == nil ||
		!strings.Contains(err.Error(), "invalid fixed handler combination") {
		t.Fatalf("malformed error=%v", err)
	}
	unknown := table[1].KeyV1()
	unknown.RuntimeProtocol = "unsupported/v1"
	if _, err := ResolveGenericV1(unknown); !errors.Is(err, ErrProtocolHandlerNotFoundV1) {
		t.Fatalf("unknown error=%v", err)
	}
}

func TestReservedDocumentInsightDriftIsConflictWithoutFallback(t *testing.T) {
	t.Parallel()
	config, authority := knowledgeBindingV1(t, "tenant-a")
	input := BindingV1{
		TenantID: "tenant-a", Port: contextPortV1(),
		ConfigCanonical: config, AuthorityCanonical: authority,
		FailurePolicy: moduleapi.FailureRequired,
		RuntimeRequest: RuntimeRequestV1{
			Mode:     moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol: moduleapi.RuntimeProtocolGoInProcessV1,
		},
		Module: &ModuleIdentityV1{
			ID:             moduleapi.DocumentInsightModuleIDV1,
			ExactVersion:   moduleapi.DocumentInsightVersionV1,
			ArtifactDigest: strings.Repeat("f", moduleapi.SHA256HexLength),
		},
	}
	assessment, err := AssessBindingV1(input)
	if err != nil || assessment.Status != AssessmentConflictV1 ||
		assessment.Policy.HandlerKind != HandlerDocumentInsightV1 {
		t.Fatalf("reserved drift assessment=%+v error=%v", assessment, err)
	}
	if _, err := ResolveBindingV1(input); !errors.Is(err, ErrProtocolHandlerNotFoundV1) {
		t.Fatalf("reserved drift Apply error=%v", err)
	}
}

func TestRequiredGrantsIncludeRemoteAndModelDigestReferences(t *testing.T) {
	t.Parallel()
	remoteConfig, remoteAuthority := remoteBindingV1(t, "tenant-a")
	remoteInput := BindingV1{
		TenantID: "tenant-a", Port: actionPortV1(),
		ConfigCanonical: remoteConfig, AuthorityCanonical: remoteAuthority,
		FailurePolicy: moduleapi.FailureRequired,
		RuntimeRequest: RuntimeRequestV1{
			Mode:     moduleapi.RuntimeModeRequestRemote,
			Protocol: moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
		},
	}
	remotePolicy, err := ResolveBindingV1(remoteInput)
	if err != nil {
		t.Fatal(err)
	}
	remoteGrants := RequiredGrantsV1(remotePolicy, remoteInput, strings.Repeat("a", 64))
	wantRemote := []GrantRequirementV1{
		{Kind: GrantRemoteActionArtifactV1, ReferenceDigest: strings.Repeat("a", 64)},
		{Kind: GrantRemoteEndpointDigestV1, ReferenceDigest: grantReferenceDigestV1(GrantRemoteEndpointDigestV1, "https://api.example.com/actions")},
		{Kind: GrantRemoteCredentialDigestV1, ReferenceDigest: grantReferenceDigestV1(GrantRemoteCredentialDigestV1, "placeholder")},
	}
	if !reflect.DeepEqual(remoteGrants, wantRemote) {
		t.Fatalf("remote grants=%+v want=%+v", remoteGrants, wantRemote)
	}

	modelConfig, modelAuthority := modelBindingV1(t, "tenant-a")
	modelInput := BindingV1{
		TenantID: "tenant-a", Port: modelPortV1(),
		ConfigCanonical: modelConfig, AuthorityCanonical: modelAuthority,
		FailurePolicy: moduleapi.FailureRequired,
		RuntimeRequest: RuntimeRequestV1{
			Mode:     moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol: moduleapi.RuntimeProtocolGoInProcessV1,
		},
		Module: &ModuleIdentityV1{ID: DeepSeekModuleIDV1, ExactVersion: DeepSeekVersionV1, ArtifactDigest: DeepSeekArtifactDigestV1},
	}
	modelPolicy, err := ResolveBindingV1(modelInput)
	if err != nil {
		t.Fatal(err)
	}
	modelGrants := RequiredGrantsV1(modelPolicy, modelInput, DeepSeekArtifactDigestV1)
	wantModel := []GrantRequirementV1{
		{Kind: GrantTrustedInProcessArtifactV1, ReferenceDigest: DeepSeekArtifactDigestV1},
		{Kind: GrantModelCredentialDigestV1, ReferenceDigest: grantReferenceDigestV1(GrantModelCredentialDigestV1, "placeholder")},
	}
	if !reflect.DeepEqual(modelGrants, wantModel) {
		t.Fatalf("model grants=%+v want=%+v", modelGrants, wantModel)
	}
}

func TestDeepSeekBindingRequiresExactOfficialEndpointAuthority(t *testing.T) {
	t.Parallel()
	config, authority := modelBindingV1(t, "tenant-a")
	if err := ValidateDeepSeekBindingCanonicalV1("tenant-a", config, authority); err != nil {
		t.Fatalf("valid DeepSeek binding: %v", err)
	}
	denied, err := moduleapi.CanonicalJSON(bytes.Replace(
		authority,
		[]byte(`"allow_official_provider_endpoint":true`),
		[]byte(`"allow_official_provider_endpoint":false`),
		1,
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDeepSeekBindingCanonicalV1("tenant-a", config, denied); err == nil ||
		!strings.Contains(err.Error(), "allow_official_provider_endpoint") {
		t.Fatalf("denied endpoint authority error=%v", err)
	}
}

func knowledgeBindingV1(t *testing.T, tenant string) ([]byte, []byte) {
	t.Helper()
	source := moduleapi.KnowledgeSourceRefV1{
		ID:      "source-a",
		Version: "v1",
		Digest:  strings.Repeat("1", moduleapi.SHA256HexLength),
	}
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            source,
			MaxHits:           1,
			MaxTotalTextBytes: 1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, config, err := moduleapi.NewContextBindingConfigV1(moduleapi.ContextBindingConfigV1{
		SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
		Placement:     moduleapi.ContextPlacementUntrustedData,
		AllowSummary:  false, AllowDrop: false, Parameters: parameters,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewKnowledgeAuthorityCeilingV1(moduleapi.KnowledgeAuthorityCeilingV1{
		SchemaVersion: moduleapi.KnowledgeAuthorityCeilingSchemaV1,
		Source:        source,
		AllowedScopes: []moduleapi.KnowledgeScopeRuleV1{{
			TenantID: tenant, WorkspaceID: "*", AgentID: "*", TaskInputRef: "*",
		}},
		MaxHits:           2,
		MaxTotalTextBytes: 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	return config, authority
}

func remoteBindingV1(t *testing.T, tenant string) ([]byte, []byte) {
	t.Helper()
	_, parameters, err := moduleapi.NewRemoteActionHTTPBindingParametersV1(moduleapi.RemoteActionHTTPBindingParametersV1{
		SchemaVersion: moduleapi.RemoteActionHTTPBindingParametersSchemaV1,
		EndpointURL:   "https://api.example.com/actions", SecretRef: "placeholder",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, config, err := moduleapi.NewActionBindingConfigV1(moduleapi.ActionBindingConfigV1{
		SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
		Actions: []moduleapi.ActionBindingMappingV1{{
			PublicActionID: "remote.echo", ProviderActionID: "example.echo",
			LocalEffectClass: moduleapi.EffectReadOnly, MaxResultBytes: 256,
		}},
		Parameters: parameters,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(moduleapi.ActionAuthorityCeilingV1{
		SchemaVersion: moduleapi.ActionAuthorityCeilingSchemaV1,
		TenantID:      tenant, AllowedWorkspaceIDs: []string{"workspace-a"},
		AllowedProviderActionIDs: []string{"example.echo"},
		MaxEffectClass:           moduleapi.EffectReadOnly, MaxResultBytes: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	return config, authority
}

func modelBindingV1(t *testing.T, tenant string) ([]byte, []byte) {
	t.Helper()
	_, config, err := moduleapi.NewModelBindingConfigV1(moduleapi.ModelBindingConfigV1{
		SchemaVersion: moduleapi.ModelBindingConfigSchemaV1,
		Provider:      DeepSeekProviderNameV1, Model: DeepSeekModelV4ProV1,
		ModelBuildID: DeepSeekProBuildV1, BillingVersion: "billing-v1",
		PriceSnapshotID: "price-v1", Parameters: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewModelAuthorityCeilingV1(moduleapi.ModelAuthorityCeilingV1{
		SchemaVersion: moduleapi.ModelAuthorityCeilingSchemaV1,
		TenantID:      tenant, Provider: DeepSeekProviderNameV1,
		SecretRef: "placeholder", AllowOfficialProviderEndpoint: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return config, authority
}
