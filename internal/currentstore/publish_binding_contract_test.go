package currentstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestPublishControlCatalogRestoresExactDeclarativeAndActionBindings(
	t *testing.T,
) {
	contextConfig := validDeclarativeContextConfig(t, false, false)
	staticContext := validStaticContext(t)
	actionConfig, actionAuthority := validActionBindingMaterials(t)
	tests := []struct {
		name       string
		mode       moduleapi.RuntimeModeRequest
		class      moduleapi.ExecutionClass
		port       moduleapi.PortRef
		config     []byte
		authority  []byte
		staticJSON [][]byte
	}{
		{
			name:  "declarative context",
			mode:  moduleapi.RuntimeModeRequestDeclarative,
			class: moduleapi.ExecutionDeclarative,
			port: moduleapi.PortRef{
				Name:         moduleapi.PortNameContextProvide,
				ExactVersion: moduleapi.PortVersionV1,
			},
			config:     contextConfig,
			authority:  []byte(declarativeDenyAllAuthorityCeilingCanonicalV1),
			staticJSON: [][]byte{staticContext},
		},
		{
			name:  "action",
			mode:  moduleapi.RuntimeModeRequestTrustedInProcess,
			class: moduleapi.ExecutionTrustedInProcess,
			port: moduleapi.PortRef{
				Name:         moduleapi.PortNameActionProvider,
				ExactVersion: moduleapi.PortVersionV1,
			},
			config:    actionConfig,
			authority: actionAuthority,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBindingContractPublication(
				t,
				test.mode,
				test.class,
				test.port,
				test.config,
				test.authority,
				test.staticJSON,
			)
			basis, err := fixture.store.PublishControlCatalog(
				context.Background(),
				fixture.input,
			)
			if err != nil {
				t.Fatalf("PublishControlCatalog: %v", err)
			}
			if basis.PointerRevision != 1 || basis.TenantID != fixture.tenantID {
				t.Fatalf("basis=%+v", basis)
			}
			if _, err := fixture.store.PublishControlCatalog(
				context.Background(),
				fixture.input,
			); err != nil {
				t.Fatalf("exact publication retry: %v", err)
			}
		})
	}
}

func TestPublishControlCatalogRejectsInvalidDeclarativeAndActionContracts(
	t *testing.T,
) {
	validContextConfig := validDeclarativeContextConfig(t, false, false)
	validStatic := validStaticContext(t)
	validActionConfig, validActionAuthority := validActionBindingMaterials(t)
	contextPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	actionPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}
	tests := []struct {
		name       string
		mode       moduleapi.RuntimeModeRequest
		class      moduleapi.ExecutionClass
		port       moduleapi.PortRef
		config     []byte
		authority  []byte
		staticJSON [][]byte
	}{
		{
			name:       "declarative summary grant",
			mode:       moduleapi.RuntimeModeRequestDeclarative,
			class:      moduleapi.ExecutionDeclarative,
			port:       contextPort,
			config:     validDeclarativeContextConfig(t, true, false),
			authority:  []byte(declarativeDenyAllAuthorityCeilingCanonicalV1),
			staticJSON: [][]byte{validStatic},
		},
		{
			name:       "declarative non-deny authority",
			mode:       moduleapi.RuntimeModeRequestDeclarative,
			class:      moduleapi.ExecutionDeclarative,
			port:       contextPort,
			config:     validContextConfig,
			authority:  []byte(`{"network":false}`),
			staticJSON: [][]byte{validStatic},
		},
		{
			name:      "declarative missing static context",
			mode:      moduleapi.RuntimeModeRequestDeclarative,
			class:     moduleapi.ExecutionDeclarative,
			port:      contextPort,
			config:    validContextConfig,
			authority: []byte(declarativeDenyAllAuthorityCeilingCanonicalV1),
		},
		{
			name:       "declarative invalid static context schema",
			mode:       moduleapi.RuntimeModeRequestDeclarative,
			class:      moduleapi.ExecutionDeclarative,
			port:       contextPort,
			config:     validContextConfig,
			authority:  []byte(declarativeDenyAllAuthorityCeilingCanonicalV1),
			staticJSON: [][]byte{[]byte(`{"text":"not versioned"}`)},
		},
		{
			name:      "action invalid config schema",
			mode:      moduleapi.RuntimeModeRequestTrustedInProcess,
			class:     moduleapi.ExecutionTrustedInProcess,
			port:      actionPort,
			config:    []byte(`{"schema_version":"wrong/v1"}`),
			authority: validActionAuthority,
		},
		{
			name:      "action invalid authority schema",
			mode:      moduleapi.RuntimeModeRequestTrustedInProcess,
			class:     moduleapi.ExecutionTrustedInProcess,
			port:      actionPort,
			config:    validActionConfig,
			authority: []byte(`{"schema_version":"wrong/v1"}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBindingContractPublication(
				t,
				test.mode,
				test.class,
				test.port,
				test.config,
				test.authority,
				test.staticJSON,
			)
			_, err := fixture.store.PublishControlCatalog(
				context.Background(),
				fixture.input,
			)
			if !errors.Is(err, ErrPublicationConflict) {
				t.Fatalf("PublishControlCatalog error=%v", err)
			}
			if counts := publicationRowCounts(t, fixture.store); counts != [3]int{} {
				t.Fatalf("rejected publication changed rows: %v", counts)
			}
		})
	}
}

type bindingContractPublication struct {
	store    *Store
	tenantID string
	input    PublishControlCatalogInput
}

func newBindingContractPublication(
	t *testing.T,
	mode moduleapi.RuntimeModeRequest,
	class moduleapi.ExecutionClass,
	port moduleapi.PortRef,
	configCanonical []byte,
	authorityCanonical []byte,
	staticContextCanonicals [][]byte,
) bindingContractPublication {
	t.Helper()
	store := openModuleTestStore(t)
	tenantID := "tenant-binding-contract"
	config := putPublicationJSON(
		t,
		store,
		ContentConfig,
		configCanonical,
	)
	authority := putPublicationJSON(
		t,
		store,
		ContentAuthorityCeiling,
		authorityCanonical,
	)
	staticRefs := make([]string, 0, len(staticContextCanonicals))
	for _, canonical := range staticContextCanonicals {
		staticRefs = append(
			staticRefs,
			putPublicationJSON(t, store, ContentStaticContext, canonical),
		)
	}

	manifest := canonicalModuleManifest(
		t,
		"test.binding.contract",
		"v1",
		mode,
		map[string]any{
			"provides": []any{
				map[string]any{
					"name":          port.Name,
					"exact_version": port.ExactVersion,
				},
			},
		},
	)
	installation, err := store.InstallModule(
		context.Background(),
		installInput(
			t,
			"installation-binding-contract",
			manifest,
			strings.Repeat("a", 64),
		),
	)
	if err != nil {
		t.Fatalf("InstallModule: %v", err)
	}
	activation, err := store.ActivateModule(
		context.Background(),
		ActivateModuleInput{
			ActivationID:       "activation-binding-contract",
			TenantID:           tenantID,
			InstanceID:         "instance-binding-contract",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     class,
			AdapterIdentity:    "core.binding.adapter",
		},
	)
	if err != nil {
		t.Fatalf("ActivateModule: %v", err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}
	contextPolicy := putPublicationContextPolicy(t, store)
	schedulingPolicy := putPublicationPolicy(
		t,
		store,
		"binding-scheduling-policy",
		corecontract.PolicyScheduling,
	)
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(
		controlcontract.ControlSnapshot{
			SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV2,
			SnapshotID:    "control-binding-contract",
			TenantID:      tenantID,
			Revision:      1,
			Profiles: []controlcontract.ProfileDefinition{
				{
					Profile: corecontract.ProfileRef{
						ID:      "profile-binding-contract",
						Version: "v1",
						Digest:  strings.Repeat("1", 64),
					},
					ContextPolicy:    contextPolicy,
					SchedulingPolicy: schedulingPolicy,
					Bindings: []controlcontract.BindingSpec{
						{
							Port:                port,
							InstanceID:          provider.InstanceID,
							ConfigRef:           config,
							AuthorityCeilingRef: authority,
							StaticContextRefs:   staticRefs,
							FailurePolicy:       moduleapi.FailureRequired,
						},
					},
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewControlSnapshot: %v", err)
	}
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(
			controlcontract.CatalogGeneration{
				SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
				GenerationID:          "catalog-binding-contract",
				Generation:            1,
				TenantID:              tenantID,
				ControlSnapshotID:     controlRef.SnapshotID,
				ControlSnapshotDigest: controlRef.Digest,
				Entries: []controlcontract.CatalogEntry{
					{Activation: provider, Provides: []moduleapi.PortRef{port}},
				},
			},
		)
	if err != nil {
		t.Fatalf("NewCatalogGeneration: %v", err)
	}
	return bindingContractPublication{
		store:    store,
		tenantID: tenantID,
		input: PublishControlCatalogInput{
			ExpectedPointerRevision: 0,
			NewPointerRevision:      1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	}
}

func validDeclarativeContextConfig(
	t *testing.T,
	allowSummary bool,
	allowDrop bool,
) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementTrustedInstruction,
			AllowSummary:  allowSummary,
			AllowDrop:     allowDrop,
			Parameters:    []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("NewContextBindingConfigV1: %v", err)
	}
	return canonical
}

func validStaticContext(t *testing.T) []byte {
	t.Helper()
	_, canonical, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text:          "immutable declarative context",
		},
	)
	if err != nil {
		t.Fatalf("NewStaticContextV1: %v", err)
	}
	return canonical
}

func validActionBindingMaterials(t *testing.T) ([]byte, []byte) {
	t.Helper()
	_, configCanonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{
				{
					PublicActionID:   "action.read",
					ProviderActionID: "provider.read",
					LocalEffectClass: moduleapi.EffectReadOnly,
					MaxResultBytes:   1024,
				},
			},
			Parameters: []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("NewActionBindingConfigV1: %v", err)
	}
	_, authorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 "tenant-binding-contract",
			AllowedWorkspaceIDs:      []string{"workspace-default"},
			AllowedProviderActionIDs: []string{"provider.read"},
			MaxEffectClass:           moduleapi.EffectReadOnly,
			MaxResultBytes:           1024,
		},
	)
	if err != nil {
		t.Fatalf("NewActionAuthorityCeilingV1: %v", err)
	}
	return configCanonical, authorityCanonical
}
