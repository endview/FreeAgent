package actionmaterializer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestMaterializeFreezesLocalAuthorityWithoutExecuting(t *testing.T) {
	fixture := newMaterializeFixture(t)
	materializer, err := New(fixture.registry)
	if err != nil {
		t.Fatal(err)
	}
	first, err := materializer.Materialize(context.Background(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := materializer.Materialize(context.Background(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("definitions = %+v", first)
	}
	if first[0].PublicActionID != "alpha.inspect" ||
		first[1].PublicActionID != "text.stats" {
		t.Fatalf("definitions are not sorted by public identity: %+v", first)
	}
	stats := first[1]
	if stats.ProviderActionID != "builtin.text.stats" ||
		stats.BindingIndex != 0 ||
		stats.EffectClass != moduleapi.EffectReadOnly ||
		stats.MaxResultBytes != 512 {
		t.Fatalf("effective text.stats definition = %+v", stats)
	}
	if first[0].DefinitionDigest != second[0].DefinitionDigest ||
		first[1].DefinitionDigest != second[1].DefinitionDigest {
		t.Fatal("same frozen Describe material produced different definitions")
	}
	if fixture.adapter.describeCalls != 2 ||
		fixture.adapter.prepareCalls != 0 ||
		fixture.adapter.invokeCalls != 0 {
		t.Fatalf(
			"Describe=%d Prepare=%d Invoke=%d",
			fixture.adapter.describeCalls,
			fixture.adapter.prepareCalls,
			fixture.adapter.invokeCalls,
		)
	}
	if !bytes.Equal(fixture.adapter.lastDescribe.Parameters, []byte(`{"mode":"stable"}`)) {
		t.Fatalf("Describe parameters = %s", fixture.adapter.lastDescribe.Parameters)
	}
}

func TestMaterializeAcceptsLocalProcessActionProvider(t *testing.T) {
	fixture := newMaterializeFixture(t)
	fixture.input.Plan.Bindings[0].Provider.ExecutionClass =
		moduleapi.ExecutionLocalProcess
	materializer, err := New(fixture.registry)
	if err != nil {
		t.Fatal(err)
	}
	definitions, err := materializer.Materialize(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("Materialize(LOCAL_PROCESS): %v", err)
	}
	if len(definitions) != 2 || fixture.adapter.describeCalls != 1 ||
		fixture.adapter.prepareCalls != 0 || fixture.adapter.invokeCalls != 0 {
		t.Fatalf(
			"definitions/Describe/Prepare/Invoke = %d/%d/%d/%d",
			len(definitions),
			fixture.adapter.describeCalls,
			fixture.adapter.prepareCalls,
			fixture.adapter.invokeCalls,
		)
	}
}

func TestMaterializeFailsClosedBeforeDescribeOrExecution(t *testing.T) {
	t.Run("content digest mismatch", func(t *testing.T) {
		fixture := newMaterializeFixture(t)
		fixture.input.Bindings[0].ConfigCanonical = []byte(`{}`)
		materializer, err := New(fixture.registry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := materializer.Materialize(
			context.Background(),
			fixture.input,
		); !errors.Is(err, ErrInvalidMaterialization) {
			t.Fatalf("digest mismatch error = %v", err)
		}
		if fixture.adapter.describeCalls != 0 ||
			fixture.adapter.prepareCalls != 0 ||
			fixture.adapter.invokeCalls != 0 {
			t.Fatal("invalid immutable material reached the module")
		}
	})

	t.Run("effect exceeds authority", func(t *testing.T) {
		fixture := newMaterializeFixture(t)
		fixture.replaceAuthority(t, moduleapi.EffectNone)
		materializer, err := New(fixture.registry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := materializer.Materialize(
			context.Background(),
			fixture.input,
		); !errors.Is(err, ErrInvalidMaterialization) {
			t.Fatalf("authority error = %v", err)
		}
		if fixture.adapter.describeCalls != 1 ||
			fixture.adapter.prepareCalls != 0 ||
			fixture.adapter.invokeCalls != 0 {
			t.Fatal("authority failure crossed into Prepare or Execute")
		}
	})

	t.Run("adapter has no public Action provider", func(t *testing.T) {
		fixture := newMaterializeFixture(t)
		fixture.registry.invoker = &invokerOnly{}
		materializer, err := New(fixture.registry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := materializer.Materialize(
			context.Background(),
			fixture.input,
		); !errors.Is(err, ErrInvalidMaterialization) {
			t.Fatalf("wrong adapter error = %v", err)
		}
	})

	t.Run("remote public Describe remains admission only", func(t *testing.T) {
		fixture := newMaterializeFixture(t)
		fixture.input.Plan.Bindings[0].Provider.ExecutionClass =
			moduleapi.ExecutionRemote
		materializer, err := New(fixture.registry)
		if err != nil {
			t.Fatal(err)
		}
		definitions, err := materializer.Materialize(
			context.Background(),
			fixture.input,
		)
		if err != nil || len(definitions) != 2 {
			t.Fatalf("remote materialization definitions=%+v error=%v", definitions, err)
		}
		if fixture.adapter.describeCalls != 1 ||
			fixture.adapter.prepareCalls != 0 ||
			fixture.adapter.invokeCalls != 0 {
			t.Fatal("remote admission crossed beyond read-only Describe")
		}
	})

	t.Run("wasm public Describe remains admission only", func(t *testing.T) {
		fixture := newMaterializeFixture(t)
		fixture.input.Plan.Bindings[0].Provider.ExecutionClass =
			moduleapi.ExecutionWASM
		materializer, err := New(fixture.registry)
		if err != nil {
			t.Fatal(err)
		}
		definitions, err := materializer.Materialize(
			context.Background(),
			fixture.input,
		)
		if err != nil || len(definitions) != 2 {
			t.Fatalf("WASM materialization definitions=%+v error=%v", definitions, err)
		}
		if fixture.adapter.describeCalls != 1 ||
			fixture.adapter.prepareCalls != 0 ||
			fixture.adapter.invokeCalls != 0 {
			t.Fatal("WASM admission crossed beyond read-only Describe")
		}
	})
}

type materializeFixture struct {
	input    InputV1
	registry *materializeRegistry
	adapter  *materializeAdapter
	binding  moduleapi.PortBinding
}

func newMaterializeFixture(t *testing.T) *materializeFixture {
	t.Helper()
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           "builtin.action.text",
		Version:            "1.0.0",
		ArtifactDigest:     materializeDigest("a"),
		InstanceID:         "builtin-text-action",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "builtin.action.text.v1",
		ActivationRevision: 1,
	}
	config, configCanonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{
				{
					PublicActionID:   "text.stats",
					ProviderActionID: "builtin.text.stats",
					LocalEffectClass: moduleapi.EffectNone,
					MaxResultBytes:   1024,
				},
				{
					PublicActionID:   "alpha.inspect",
					ProviderActionID: "builtin.alpha.inspect",
					LocalEffectClass: moduleapi.EffectNone,
					MaxResultBytes:   900,
				},
			},
			Parameters: json.RawMessage(`{"mode":"stable"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authority, authorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:       moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:            "tenant-test",
			AllowedWorkspaceIDs: []string{"workspace-test"},
			AllowedProviderActionIDs: []string{
				"builtin.alpha.inspect",
				"builtin.text.stats",
			},
			MaxEffectClass: moduleapi.EffectReversibleWrite,
			MaxResultBytes: 512,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = config
	_ = authority
	configDigest := contentRecordDigestV1(
		"CONFIG",
		"application/json",
		configCanonical,
	)
	authorityDigest := contentRecordDigestV1(
		"AUTHORITY_CEILING",
		"application/json",
		authorityCanonical,
	)
	binding := moduleapi.PortBinding{
		Provider:            provider,
		ConfigRef:           configDigest,
		AuthorityCeilingRef: authorityDigest,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	plan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameActionProvider,
			ExactVersion: moduleapi.PortVersionV1,
		},
		Bindings: []moduleapi.PortBinding{binding},
	})
	if err != nil {
		t.Fatal(err)
	}
	schema := json.RawMessage(
		`{"additionalProperties":false,"properties":{"text":{"maxLength":4096,"type":"string"}},"required":["text"],"type":"object"}`,
	)
	adapter := &materializeAdapter{
		definitions: []moduleapi.ActionDefinitionV1{
			{
				ProviderActionID:        "builtin.text.stats",
				Description:             "Count text statistics.",
				InputSchema:             schema,
				RequestedEffectClass:    moduleapi.EffectReadOnly,
				RequestedMaxResultBytes: 2048,
			},
			{
				ProviderActionID:        "builtin.alpha.inspect",
				Description:             "Inspect text without effects.",
				InputSchema:             schema,
				RequestedEffectClass:    moduleapi.EffectNone,
				RequestedMaxResultBytes: 700,
			},
		},
	}
	registry := &materializeRegistry{
		artifactDigest:  provider.ArtifactDigest,
		adapterIdentity: provider.AdapterIdentity,
		invoker:         adapter,
	}
	return &materializeFixture{
		input: InputV1{
			TenantID:    "tenant-test",
			WorkspaceID: "workspace-test",
			Plan:        plan,
			Bindings: []BindingMaterialV1{{
				ConfigCanonical:    bytes.Clone(configCanonical),
				AuthorityCanonical: bytes.Clone(authorityCanonical),
			}},
		},
		registry: registry,
		adapter:  adapter,
		binding:  binding,
	}
}

func (fixture *materializeFixture) replaceAuthority(
	t *testing.T,
	effect moduleapi.EffectClass,
) {
	t.Helper()
	_, canonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:       moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:            "tenant-test",
			AllowedWorkspaceIDs: []string{"workspace-test"},
			AllowedProviderActionIDs: []string{
				"builtin.alpha.inspect",
				"builtin.text.stats",
			},
			MaxEffectClass: effect,
			MaxResultBytes: 512,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest := contentRecordDigestV1(
		"AUTHORITY_CEILING",
		"application/json",
		canonical,
	)
	fixture.input.Bindings[0].AuthorityCanonical = canonical
	fixture.input.Plan.Bindings[0].AuthorityCeilingRef = digest
}

type materializeRegistry struct {
	artifactDigest  string
	adapterIdentity string
	invoker         modulehost.ModuleInvoker
}

func (registry *materializeRegistry) ResolveExact(
	_ context.Context,
	artifactDigest string,
	adapterIdentity string,
) (modulehost.ModuleInvoker, error) {
	if artifactDigest != registry.artifactDigest ||
		adapterIdentity != registry.adapterIdentity {
		return nil, errors.New("unexpected exact adapter identity")
	}
	return registry.invoker, nil
}

type materializeAdapter struct {
	definitions   []moduleapi.ActionDefinitionV1
	describeCalls int
	prepareCalls  int
	invokeCalls   int
	lastDescribe  moduleapi.ActionDescribeRequestV1
}

func (adapter *materializeAdapter) Describe(
	_ context.Context,
	request moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	adapter.describeCalls++
	adapter.lastDescribe = request
	return append([]moduleapi.ActionDefinitionV1(nil), adapter.definitions...), nil
}

func (adapter *materializeAdapter) Prepare(
	_ context.Context,
	_ moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	adapter.prepareCalls++
	return json.RawMessage(`{}`), nil
}

func (adapter *materializeAdapter) Invoke(
	_ context.Context,
	_ modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	adapter.invokeCalls++
	return modulehost.InvocationResult{}, errors.New("generic Invoke is forbidden")
}

type invokerOnly struct{}

func (*invokerOnly) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return modulehost.InvocationResult{}, nil
}

func materializeDigest(character string) string {
	return string(bytes.Repeat([]byte(character), 64))
}
