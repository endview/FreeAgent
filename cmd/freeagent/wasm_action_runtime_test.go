package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/internal/wasmaction"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestWASMActionRuntimeFlagsRequireExactArtifactAllowlist(t *testing.T) {
	t.Parallel()

	parse := func(arguments ...string) (*productionWASMActionRuntimeConfig, error) {
		flags := flag.NewFlagSet("wasm-action-runtime-test", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		values := bindWASMActionRuntimeFlags(flags)
		if err := flags.Parse(arguments); err != nil {
			return nil, err
		}
		return values.config(flags)
	}
	disabled, err := parse()
	if err != nil || disabled != nil {
		t.Fatalf("default config = %#v, %v; want disabled", disabled, err)
	}

	digestA := strings.Repeat("a", moduleapi.SHA256HexLength)
	digestB := strings.Repeat("b", moduleapi.SHA256HexLength)
	for _, test := range []struct {
		name      string
		arguments []string
		contains  string
	}{
		{
			name: "allowlist without enable",
			arguments: []string{
				"--allow-wasm-runtime-artifact", digestA,
			},
			contains: "requires explicit --enable-wasm-actions",
		},
		{
			name:      "enable without allowlist",
			arguments: []string{"--enable-wasm-actions"},
			contains:  "requires at least one",
		},
		{
			name: "non-canonical digest",
			arguments: []string{
				"--enable-wasm-actions",
				"--allow-wasm-runtime-artifact", strings.ToUpper(digestA),
			},
			contains: "canonical lowercase SHA-256",
		},
		{
			name: "duplicate digest",
			arguments: []string{
				"--enable-wasm-actions",
				"--allow-wasm-runtime-artifact", digestA,
				"--allow-wasm-runtime-artifact", digestA,
			},
			contains: "duplicate digest",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config, err := parse(test.arguments...)
			if err == nil || config != nil ||
				!strings.Contains(err.Error(), test.contains) {
				t.Fatalf("config=%#v error=%v want %q", config, err, test.contains)
			}
		})
	}

	enabled, err := parse(
		"--enable-wasm-actions",
		"--allow-wasm-runtime-artifact", digestB,
		"--allow-wasm-runtime-artifact", digestA,
	)
	if err != nil || enabled == nil || !enabled.Enabled ||
		len(enabled.AllowedArtifactDigests) != 2 ||
		enabled.AllowedArtifactDigests[0] != digestA ||
		enabled.AllowedArtifactDigests[1] != digestB {
		t.Fatalf("enabled config = %#v, %v", enabled, err)
	}
}

func TestChatAndServeExposeFailClosedWASMRuntimeAllowlist(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("a", moduleapi.SHA256HexLength)
	for _, test := range []struct {
		name string
		run  func(context.Context, []string, io.Writer, io.Writer) error
		args []string
		want string
	}{
		{
			name: "chat enabled without allowlist",
			run:  runChat,
			args: []string{
				"--db", filepath.Join(t.TempDir(), "absent.sqlite"),
				"--message", "hello",
				"--enable-wasm-actions",
			},
			want: "requires at least one",
		},
		{
			name: "serve allowlist without enable",
			run:  runServe,
			args: []string{
				"--db", filepath.Join(t.TempDir(), "absent.sqlite"),
				"--allow-wasm-runtime-artifact", digest,
			},
			want: "requires explicit --enable-wasm-actions",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.run(context.Background(), test.args, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CLI error=%v want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "open") ||
				strings.Contains(err.Error(), "sqlite") {
				t.Fatalf("CLI touched state before flag rejection: %v", err)
			}
		})
	}
}

func TestProductionWASMActionRegistryIsDefaultOffCatalogExactAndLazy(
	t *testing.T,
) {
	t.Parallel()

	model, modelEntry := productionRemoteActionTestModel(t)
	provider, manifest, descriptor, wasm := productionWASMActionTestArtifact(t)
	entry := controlcontract.CatalogEntry{
		Activation: provider,
		Provides:   []moduleapi.PortRef{productionActionPort},
	}
	entries := []controlcontract.CatalogEntry{modelEntry, entry}
	root := t.TempDir()
	if err := stageArtifact(root, model); err != nil {
		t.Fatalf("stage model artifact: %v", err)
	}

	// The artifact is deliberately absent. Disabled Registry construction and
	// exact resolution must not read, compile, instantiate, or execute it.
	disabledRegistry, err := newProductionAdapterRegistry(root, entries)
	if err != nil {
		t.Fatalf("construct Registry with dormant WASM entry: %v", err)
	}
	if _, err := disabledRegistry.ResolveExact(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	); !errors.Is(err, exactadapter.ErrAdapterNotFound) ||
		!strings.Contains(err.Error(), "not explicitly enabled") {
		t.Fatalf("disabled WASM consumption error = %v", err)
	}

	explicitlyDisabledRegistry, err :=
		newProductionAdapterRegistryWithActionRuntimesAndExtras(
			root,
			entries,
			nil,
			nil,
			&productionWASMActionRuntimeConfig{Enabled: false},
		)
	if err != nil {
		t.Fatalf("construct explicitly disabled WASM Registry: %v", err)
	}
	if _, err := explicitlyDisabledRegistry.ResolveExact(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	); !errors.Is(err, exactadapter.ErrAdapterNotFound) {
		t.Fatalf("explicitly disabled WASM consumption error = %v", err)
	}

	otherDigest := strings.Repeat("f", moduleapi.SHA256HexLength)
	if otherDigest == provider.ArtifactDigest {
		otherDigest = strings.Repeat("e", moduleapi.SHA256HexLength)
	}
	for _, test := range []struct {
		name     string
		config   *productionWASMActionRuntimeConfig
		contains string
	}{
		{
			name:     "enabled without allowlist",
			config:   &productionWASMActionRuntimeConfig{Enabled: true},
			contains: "requires an exact artifact allowlist",
		},
		{
			name: "disabled with allowlist",
			config: &productionWASMActionRuntimeConfig{
				AllowedArtifactDigests: []string{provider.ArtifactDigest},
			},
			contains: "disabled WASM Action runtime",
		},
		{
			name: "non-canonical allowlist",
			config: &productionWASMActionRuntimeConfig{
				Enabled:                true,
				AllowedArtifactDigests: []string{"not-a-digest"},
			},
			contains: "non-canonical artifact digest",
		},
		{
			name: "duplicate allowlist",
			config: &productionWASMActionRuntimeConfig{
				Enabled: true,
				AllowedArtifactDigests: []string{
					provider.ArtifactDigest,
					provider.ArtifactDigest,
				},
			},
			contains: "duplicate artifact digest",
		},
		{
			name: "non-Catalog allowlist",
			config: &productionWASMActionRuntimeConfig{
				Enabled:                true,
				AllowedArtifactDigests: []string{otherDigest},
			},
			contains: "non-Catalog artifact digest",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry, err := newProductionAdapterRegistryWithActionRuntimesAndExtras(
				root,
				entries,
				nil,
				nil,
				test.config,
			)
			if err == nil || registry != nil ||
				!strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Registry=%#v error=%v want %q", registry, err, test.contains)
			}
		})
	}

	registry, err := newProductionAdapterRegistryWithActionRuntimesAndExtras(
		root,
		entries,
		nil,
		nil,
		&productionWASMActionRuntimeConfig{
			Enabled:                true,
			AllowedArtifactDigests: []string{provider.ArtifactDigest},
		},
	)
	if err != nil {
		t.Fatalf("construct enabled Registry before WASM artifact exists: %v", err)
	}
	registered, err := registry.IsRegistered(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	)
	if err != nil || registered {
		t.Fatalf("pre-resolution state registered=%v err=%v", registered, err)
	}
	productionWASMActionWriteArtifact(
		t,
		root,
		provider,
		manifest,
		descriptor,
		wasm,
	)
	invoker, err := registry.ResolveExact(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	)
	if err != nil {
		t.Fatalf("lazy resolve WASM Action: %v", err)
	}
	adapter, ok := invoker.(*wasmaction.Adapter)
	if !ok {
		t.Fatalf("resolved invoker = %T, want *wasmaction.Adapter", invoker)
	}
	if _, err := adapter.Invoke(
		context.Background(),
		modulehost.PreparedInvocation{},
	); !errors.Is(err, wasmaction.ErrGenericInvocation) {
		t.Fatalf("generic WASM invocation error = %v", err)
	}

	unknownDigest := strings.Repeat("f", moduleapi.SHA256HexLength)
	if unknownDigest == provider.ArtifactDigest {
		unknownDigest = strings.Repeat("e", moduleapi.SHA256HexLength)
	}
	if _, err := registry.ResolveExact(
		context.Background(),
		unknownDigest,
		wasmaction.AdapterIdentityV1,
	); !errors.Is(err, exactadapter.ErrAdapterNotFound) {
		t.Fatalf("non-Catalog WASM exact key error = %v", err)
	}
}

func TestExactWASMActionCatalogProvidersRejectsMixedIdentityOrPort(
	t *testing.T,
) {
	t.Parallel()

	provider, _, _, _ := productionWASMActionTestArtifact(t)
	for _, test := range []struct {
		name  string
		entry controlcontract.CatalogEntry
	}{
		{
			name: "wrong adapter",
			entry: controlcontract.CatalogEntry{
				Activation: func() moduleapi.ActivatedModuleRef {
					value := provider
					value.AdapterIdentity = localTextStatsAdapterID
					return value
				}(),
				Provides: []moduleapi.PortRef{productionActionPort},
			},
		},
		{
			name: "wrong port",
			entry: controlcontract.CatalogEntry{
				Activation: provider,
				Provides:   []moduleapi.PortRef{productionContextPort},
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := exactWASMActionCatalogProviders(
				[]controlcontract.CatalogEntry{test.entry},
			); err == nil || !strings.Contains(err.Error(), "exact") {
				t.Fatalf("Catalog filter error = %v", err)
			}
		})
	}
}

func TestExactWASMActionCatalogProvidersShareArtifactAcrossInstances(
	t *testing.T,
) {
	t.Parallel()

	provider, _, _, _ := productionWASMActionTestArtifact(t)
	second := provider
	second.InstanceID = "wasm-action-instance-secondary"
	second.ActivationRevision = 9
	providers, err := exactWASMActionCatalogProviders(
		[]controlcontract.CatalogEntry{
			{Activation: provider, Provides: []moduleapi.PortRef{productionActionPort}},
			{Activation: second, Provides: []moduleapi.PortRef{productionActionPort}},
		},
	)
	if err != nil || len(providers) != 1 {
		t.Fatalf("shared WASM provider map=%v error=%v", providers, err)
	}
	key := provider.ArtifactDigest + "\x00" + provider.AdapterIdentity
	selected, found := providers[key]
	if !found || !sameProviderArtifactAdapter(selected, second) {
		t.Fatalf("shared WASM provider=%+v found=%v", selected, found)
	}
}

func productionWASMActionTestArtifact(
	t *testing.T,
) (moduleapi.ActivatedModuleRef, []byte, []byte, []byte) {
	t.Helper()
	_, descriptor, err := wasmaction.NewDescriptorV1(wasmaction.DescriptorV1{
		SchemaVersion: wasmaction.DescriptorSchemaV1,
		ABIVersion:    wasmaction.ABIVersionV1,
		ModulePath:    "content/action.wasm",
		Actions: []moduleapi.ActionDefinitionV1{{
			ProviderActionID:        "example.wasm.echo",
			Description:             "Return one deterministic local value.",
			InputSchema:             json.RawMessage(`{"additionalProperties":false,"properties":{"value":{"maxLength":128,"type":"string"}},"required":["value"],"type":"object"}`),
			RequestedEffectClass:    moduleapi.EffectNone,
			RequestedMaxResultBytes: 256,
		}},
	})
	if err != nil {
		t.Fatalf("build WASM descriptor: %v", err)
	}
	wasm := productionWASMActionMinimalModuleV1()
	if err := wasmaction.ValidateModuleV1(context.Background(), wasm); err != nil {
		t.Fatalf("test Wasm module is invalid: %v", err)
	}
	manifestValue := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "example.wasm.action",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestWASM,
			Protocol:   moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
			Entrypoint: "content/actions.json",
		},
		Provides: []moduleapi.PortRef{productionActionPort},
	}
	encoded, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(
		manifest,
		[]moduleapi.ArtifactFile{
			{Path: "content/action.wasm", Content: wasm},
			{Path: "content/actions.json", Content: descriptor},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return moduleapi.ActivatedModuleRef{
		ModuleID:           manifestValue.ID,
		Version:            manifestValue.Version,
		ArtifactDigest:     digest,
		InstanceID:         "wasm-action-instance",
		ExecutionClass:     moduleapi.ExecutionWASM,
		AdapterIdentity:    wasmaction.AdapterIdentityV1,
		ActivationRevision: 1,
	}, manifest, descriptor, wasm
}

func productionWASMActionWriteArtifact(
	t *testing.T,
	artifactRoot string,
	provider moduleapi.ActivatedModuleRef,
	manifest []byte,
	descriptor []byte,
	wasm []byte,
) {
	t.Helper()
	directory := filepath.Join(artifactRoot, provider.ArtifactDigest)
	if err := os.MkdirAll(filepath.Join(directory, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{
		moduleapi.ArtifactManifestPath: manifest,
		"content/actions.json":         descriptor,
		"content/action.wasm":          wasm,
	} {
		if err := os.WriteFile(filepath.Join(directory, path), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func productionWASMActionMinimalModuleV1() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x0c, 0x02,
		0x60, 0x01, 0x7f, 0x01, 0x7f,
		0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7e,
		0x03, 0x03, 0x02, 0x00, 0x01,
		0x05, 0x04, 0x01, 0x01, 0x01, 0x02,
		0x07, 0x36, 0x03,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x12, 'f', 'r', 'e', 'e', 'a', 'g', 'e', 'n', 't', '_', 'a', 'l', 'l', 'o', 'c', '_', 'v', '1', 0x00, 0x00,
		0x14, 'f', 'r', 'e', 'e', 'a', 'g', 'e', 'n', 't', '_', 'e', 'x', 'e', 'c', 'u', 't', 'e', '_', 'v', '1', 0x00, 0x01,
		0x0a, 0x0c, 0x02,
		0x05, 0x00, 0x41, 0x80, 0x08, 0x0b,
		0x04, 0x00, 0x42, 0x00, 0x0b,
	}
}
