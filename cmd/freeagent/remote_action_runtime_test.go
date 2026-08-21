package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestRemoteActionRuntimeFlagsDefaultOffAndFailClosed(t *testing.T) {
	t.Parallel()

	parse := func(t *testing.T, arguments ...string) (
		*productionRemoteActionRuntimeConfig,
		error,
	) {
		t.Helper()
		flags := flag.NewFlagSet("remote-action-runtime-test", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		values := bindRemoteActionRuntimeFlags(flags)
		if err := flags.Parse(arguments); err != nil {
			return nil, err
		}
		return values.config(flags)
	}

	config, err := parse(t)
	if err != nil || config != nil {
		t.Fatalf("default config = %#v, %v; want disabled", config, err)
	}
	for _, test := range []struct {
		name      string
		arguments []string
		contains  string
	}{
		{
			name: "mapping without enable",
			arguments: []string{
				"--remote-action-secret-env", "secret.remote=REMOTE_SECRET",
			},
			contains: "requires explicit --enable-remote-actions",
		},
		{
			name:      "enable without mapping",
			arguments: []string{"--enable-remote-actions"},
			contains:  "requires at least one",
		},
		{
			name: "duplicate ref",
			arguments: []string{
				"--enable-remote-actions",
				"--remote-action-secret-env", "secret.remote=REMOTE_ONE",
				"--remote-action-secret-env", "secret.remote=REMOTE_TWO",
			},
			contains: "duplicate secret-ref",
		},
		{
			name: "empty ref",
			arguments: []string{
				"--enable-remote-actions",
				"--remote-action-secret-env", "=REMOTE_SECRET",
			},
			contains: "non-empty",
		},
		{
			name: "empty environment",
			arguments: []string{
				"--enable-remote-actions",
				"--remote-action-secret-env", "secret.remote=",
			},
			contains: "portable environment",
		},
		{
			name: "nonportable environment",
			arguments: []string{
				"--enable-remote-actions",
				"--remote-action-secret-env", "secret.remote=NOT-PORTABLE",
			},
			contains: "portable environment",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config, err := parse(t, test.arguments...)
			if err == nil || config != nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("config = %#v, error = %v; want %q", config, err, test.contains)
			}
		})
	}

	config, err = parse(
		t,
		"--enable-remote-actions",
		"--remote-action-secret-env", "secret.remote=REMOTE_SECRET",
	)
	if err != nil || config == nil || config.SecretResolver == nil {
		t.Fatalf("enabled config = %#v, %v", config, err)
	}
}

func TestEnvironmentRemoteActionSecretResolverIsExactLateBoundAndOwned(
	t *testing.T,
) {
	t.Parallel()

	reference := "secret.remote"
	otherReference := "secret.other"
	values := map[string]string{"REMOTE_MATERIAL": "first-material"}
	lookupCalls := 0
	resolver := &environmentRemoteActionSecretResolver{
		environmentByReference: map[string]string{
			reference: "REMOTE_MATERIAL",
		},
		lookup: func(name string) (string, bool) {
			lookupCalls++
			value, found := values[name]
			return value, found
		},
	}
	identity := remoteactionhttp.SecretIdentityV1{SecretRef: reference}
	first, err := resolver.ResolveSecret(context.Background(), identity)
	if err != nil || !bytes.Equal(first, []byte("first-material")) {
		t.Fatalf("first resolve = %q, %v", first, err)
	}
	first[0] = 'X'
	values["REMOTE_MATERIAL"] = "rotated-material"
	second, err := resolver.ResolveSecret(context.Background(), identity)
	if err != nil || !bytes.Equal(second, []byte("rotated-material")) {
		t.Fatalf("rotated resolve = %q, %v", second, err)
	}
	if lookupCalls != 2 {
		t.Fatalf("lookup calls = %d, want one per resolve", lookupCalls)
	}

	_, err = resolver.ResolveSecret(
		context.Background(),
		remoteactionhttp.SecretIdentityV1{SecretRef: otherReference},
	)
	if !errors.Is(err, errRemoteActionCredentialUnavailable) || lookupCalls != 2 {
		t.Fatalf("mismatched ref error = %v, lookup calls = %d", err, lookupCalls)
	}
	if strings.Contains(err.Error(), "rotated-material") ||
		strings.Contains(err.Error(), otherReference) {
		t.Fatalf("resolver error exposed dynamic identity or secret: %v", err)
	}
}

func TestProductionRemoteActionRegistryIsDefaultOffAndLazy(t *testing.T) {
	t.Parallel()

	model, modelEntry := productionRemoteActionTestModel(t)
	remoteProvider, manifest, descriptor := productionRemoteActionTestArtifact(t)
	remoteEntry := controlcontract.CatalogEntry{
		Activation: remoteProvider,
		Provides:   []moduleapi.PortRef{productionActionPort},
	}
	entries := []controlcontract.CatalogEntry{modelEntry, remoteEntry}

	root := t.TempDir()
	if err := stageArtifact(root, model); err != nil {
		t.Fatalf("stage model artifact: %v", err)
	}
	// A dormant Catalog entry must not break Pure Chat. The REMOTE artifact is
	// deliberately absent, proving Registry construction did not read it.
	disabledRegistry, err := newProductionAdapterRegistry(root, entries)
	if err != nil {
		t.Fatalf("construct Registry with dormant disabled REMOTE entry: %v", err)
	}
	if _, err := disabledRegistry.ResolveExact(
		context.Background(),
		remoteProvider.ArtifactDigest,
		remoteProvider.AdapterIdentity,
	); !errors.Is(err, exactadapter.ErrAdapterNotFound) ||
		!strings.Contains(err.Error(), "not explicitly enabled") {
		t.Fatalf("disabled REMOTE consumption error = %v", err)
	}
	var typedNilResolver *countingProductionRemoteActionResolver
	typedNilRegistry, err := newProductionAdapterRegistryWithRemoteRuntimeAndExtras(
		root,
		entries,
		nil,
		&productionRemoteActionRuntimeConfig{SecretResolver: typedNilResolver},
	)
	if err != nil {
		t.Fatalf("construct Registry with typed-nil resolver: %v", err)
	}
	if _, err := typedNilRegistry.ResolveExact(
		context.Background(),
		remoteProvider.ArtifactDigest,
		remoteProvider.AdapterIdentity,
	); !errors.Is(err, exactadapter.ErrAdapterNotFound) {
		t.Fatalf("typed-nil resolver consumption error = %v", err)
	}

	resolver := &countingProductionRemoteActionResolver{}
	registry, err := newProductionAdapterRegistryWithRemoteRuntimeAndExtras(
		root,
		entries,
		nil,
		&productionRemoteActionRuntimeConfig{SecretResolver: resolver},
	)
	if err != nil {
		t.Fatalf("construct enabled Registry before REMOTE artifact exists: %v", err)
	}
	registered, err := registry.IsRegistered(
		context.Background(),
		remoteProvider.ArtifactDigest,
		remoteProvider.AdapterIdentity,
	)
	if err != nil || registered || resolver.calls != 0 {
		t.Fatalf(
			"pre-resolution state registered=%v resolver=%d err=%v",
			registered,
			resolver.calls,
			err,
		)
	}
	productionRemoteActionWriteArtifact(t, root, remoteProvider, manifest, descriptor)
	invoker, err := registry.ResolveExact(
		context.Background(),
		remoteProvider.ArtifactDigest,
		remoteProvider.AdapterIdentity,
	)
	if err != nil {
		t.Fatalf("lazy resolve REMOTE Action: %v", err)
	}
	if _, ok := invoker.(*remoteactionhttp.Adapter); !ok {
		t.Fatalf("resolved invoker = %T, want *remoteactionhttp.Adapter", invoker)
	}
	if resolver.calls != 0 {
		t.Fatalf("adapter construction resolved a Secret %d times", resolver.calls)
	}

	unknownDigest := strings.Repeat("f", moduleapi.SHA256HexLength)
	if unknownDigest == remoteProvider.ArtifactDigest {
		unknownDigest = strings.Repeat("e", moduleapi.SHA256HexLength)
	}
	if _, err := registry.ResolveExact(
		context.Background(),
		unknownDigest,
		remoteactionhttp.AdapterIdentityV1,
	); !errors.Is(err, exactadapter.ErrAdapterNotFound) {
		t.Fatalf("non-Catalog REMOTE exact key error = %v", err)
	}
}

func TestPureChatWithRemoteRuntimeDoesNotTouchRemoteCapability(t *testing.T) {
	t.Parallel()

	model, modelEntry := productionRemoteActionTestModel(t)
	root := t.TempDir()
	if err := stageArtifact(root, model); err != nil {
		t.Fatalf("stage model artifact: %v", err)
	}
	resolver := &countingProductionRemoteActionResolver{}
	registry, err := newProductionAdapterRegistryWithRemoteRuntimeAndExtras(
		root,
		[]controlcontract.CatalogEntry{modelEntry},
		nil,
		&productionRemoteActionRuntimeConfig{SecretResolver: resolver},
	)
	if err != nil {
		t.Fatalf("construct Pure Chat Registry: %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("Pure Chat resolved a REMOTE Secret %d times", resolver.calls)
	}
	registered, err := registry.IsRegistered(
		context.Background(),
		strings.Repeat("d", moduleapi.SHA256HexLength),
		remoteactionhttp.AdapterIdentityV1,
	)
	if err != nil || registered || resolver.calls != 0 {
		t.Fatalf(
			"Pure Chat REMOTE state registered=%v resolver=%d err=%v",
			registered,
			resolver.calls,
			err,
		)
	}
}

func TestChatAndServeExposeFailClosedRemoteActionFlags(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		run  func(context.Context, []string, io.Writer, io.Writer) error
		args []string
		want string
	}{
		{
			name: "chat enabled without mapping",
			run:  runChat,
			args: []string{
				"--db", filepath.Join(t.TempDir(), "absent.sqlite"),
				"--message", "hello",
				"--enable-remote-actions",
			},
			want: "requires at least one",
		},
		{
			name: "serve mapping without enable",
			run:  runServe,
			args: []string{
				"--db", filepath.Join(t.TempDir(), "absent.sqlite"),
				"--remote-action-secret-env", "secret.remote=REMOTE_SECRET",
			},
			want: "requires explicit --enable-remote-actions",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.run(context.Background(), test.args, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CLI error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "open") || strings.Contains(err.Error(), "sqlite") {
				t.Fatalf("CLI touched state before flag rejection: %v", err)
			}
		})
	}
}

type countingProductionRemoteActionResolver struct {
	calls int
}

func (resolver *countingProductionRemoteActionResolver) ResolveSecret(
	context.Context,
	remoteactionhttp.SecretIdentityV1,
) ([]byte, error) {
	resolver.calls++
	return []byte("test-only-secret"), nil
}

func productionRemoteActionTestModel(
	t *testing.T,
) (bootstrapseed.ModuleAssertion, controlcontract.CatalogEntry) {
	t.Helper()
	prepared, err := bootstrapseed.PrepareFile(exampleSeedPath(t))
	if err != nil {
		t.Fatalf("prepare test seed: %v", err)
	}
	model := prepared.ModelAssertion()
	return model, controlcontract.CatalogEntry{
		Activation: providerFromAssertion(model),
		Provides:   []moduleapi.PortRef{productionModelPort},
	}
}

func productionRemoteActionTestArtifact(
	t *testing.T,
) (moduleapi.ActivatedModuleRef, []byte, []byte) {
	t.Helper()
	_, descriptor, err := remoteactionhttp.NewDescriptorV1(
		remoteactionhttp.DescriptorV1{
			SchemaVersion: remoteactionhttp.DescriptorSchemaV1,
			Actions: []moduleapi.ActionDefinitionV1{{
				ProviderActionID:        "example.echo",
				Description:             "Return one value through the remote endpoint.",
				InputSchema:             json.RawMessage(`{"additionalProperties":false,"properties":{"value":{"maxLength":128,"type":"string"}},"required":["value"],"type":"object"}`),
				RequestedEffectClass:    moduleapi.EffectReadOnly,
				RequestedMaxResultBytes: 256,
			}},
		},
	)
	if err != nil {
		t.Fatalf("build REMOTE descriptor: %v", err)
	}
	manifestValue := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "example.remote.action",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestRemote,
			Protocol:   moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
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
		[]moduleapi.ArtifactFile{{
			Path:    "content/actions.json",
			Content: descriptor,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return moduleapi.ActivatedModuleRef{
		ModuleID:           manifestValue.ID,
		Version:            manifestValue.Version,
		ArtifactDigest:     digest,
		InstanceID:         "remote-action-instance",
		ExecutionClass:     moduleapi.ExecutionRemote,
		AdapterIdentity:    remoteactionhttp.AdapterIdentityV1,
		ActivationRevision: 1,
	}, manifest, descriptor
}

func productionRemoteActionWriteArtifact(
	t *testing.T,
	artifactRoot string,
	provider moduleapi.ActivatedModuleRef,
	manifest []byte,
	descriptor []byte,
) {
	t.Helper()
	directory := filepath.Join(artifactRoot, provider.ArtifactDigest)
	if err := os.MkdirAll(filepath.Join(directory, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, moduleapi.ArtifactManifestPath),
		manifest,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "content", "actions.json"),
		descriptor,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}
