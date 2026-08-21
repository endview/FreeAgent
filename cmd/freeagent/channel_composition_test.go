package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/channelservice"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/loopbackchannel"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type productionChannelFixtureOptions struct {
	enabled         bool
	seedPath        string
	moduleID        string
	adapterProtocol string
	tamperArtifact  bool
	outboundURL     string
	skipCursor      bool
}

type productionChannelFixture struct {
	databasePath string
	artifactRoot string
	provider     moduleapi.ActivatedModuleRef
	defaults     bootstrapseed.DefaultAssembly
	resolver     *countingChannelSecretResolver
}

type countingChannelSecretResolver struct {
	calls atomic.Uint64
}

func (resolver *countingChannelSecretResolver) ResolveSecret(
	context.Context,
	string,
) ([]byte, error) {
	resolver.calls.Add(1)
	return []byte("not-a-secret"), nil
}

func TestDefaultProductionCompositionNeverTouchesConfiguredChannel(t *testing.T) {
	fixture := newProductionChannelFixture(t, productionChannelFixtureOptions{
		enabled: true,
	})
	if err := os.RemoveAll(filepath.Join(
		fixture.artifactRoot, fixture.provider.ArtifactDigest,
	)); err != nil {
		t.Fatal(err)
	}
	composition, err := openProductionComposition(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("default composition read Channel artifact: %v", err)
	}
	defer composition.Close()
	if fixture.resolver.calls.Load() != 0 {
		t.Fatal("default composition resolved a Channel secret")
	}
	registered, err := composition.registry.IsRegistered(
		context.Background(),
		fixture.provider.ArtifactDigest,
		fixture.provider.AdapterIdentity,
	)
	if err != nil {
		t.Fatal(err)
	}
	if registered || composition.channel != nil {
		t.Fatal("default composition registered a Channel adapter")
	}
}

func TestExplicitProductionChannelCompositionRegistersExactEndpoint(t *testing.T) {
	fixture := newProductionChannelFixture(t, productionChannelFixtureOptions{
		enabled: true,
	})
	composition, err := openProductionChannelComposition(
		context.Background(),
		fixture.databasePath,
		fixture.artifactRoot,
		productionChannelEndpointInput{
			TenantID:       defaultTenantID,
			WorkspaceID:    defaultWorkspaceID,
			EndpointID:     "endpoint-loopback",
			SecretResolver: fixture.resolver,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer composition.Close()
	if composition.loop == nil || composition.channel == nil ||
		composition.channel.adapter == nil ||
		composition.channel.service == nil ||
		composition.channel.handler == nil ||
		composition.channel.handler.Path() != composition.channel.adapter.InboundPath() ||
		composition.channel.endpoint.EndpointID != "endpoint-loopback" ||
		composition.channel.workspaceID != defaultWorkspaceID {
		t.Fatalf("explicit Channel runtime=%+v", composition.channel)
	}
	registered, err := composition.registry.IsRegistered(
		context.Background(),
		fixture.provider.ArtifactDigest,
		loopbackchannel.AdapterIdentityV1,
	)
	if err != nil || !registered {
		t.Fatalf("Channel registered=%v err=%v", registered, err)
	}
	invoker, err := composition.registry.ResolveExact(
		context.Background(),
		fixture.provider.ArtifactDigest,
		loopbackchannel.AdapterIdentityV1,
	)
	if err != nil || invoker != composition.channel.adapter {
		t.Fatalf("resolved=%T err=%v", invoker, err)
	}
	if fixture.resolver.calls.Load() != 0 {
		t.Fatal("composition resolved secret before an inbound/outbound operation")
	}
}

func TestExplicitProductionChannelCompositionFailsClosed(t *testing.T) {
	tests := []struct {
		name      string
		options   productionChannelFixtureOptions
		workspace string
	}{
		{
			name:      "disabled endpoint",
			options:   productionChannelFixtureOptions{enabled: false},
			workspace: defaultWorkspaceID,
		},
		{
			name:      "wrong workspace",
			options:   productionChannelFixtureOptions{enabled: true},
			workspace: "workspace-not-selected",
		},
		{
			name: "wrong provider",
			options: productionChannelFixtureOptions{
				enabled:  true,
				moduleID: "freeagent.thirdparty.channel.loopback-http",
			},
			workspace: defaultWorkspaceID,
		},
		{
			name: "wrong config",
			options: productionChannelFixtureOptions{
				enabled:         true,
				adapterProtocol: "remote-http/v1",
			},
			workspace: defaultWorkspaceID,
		},
		{
			name: "missing operator cursor",
			options: productionChannelFixtureOptions{
				enabled:    true,
				skipCursor: true,
			},
			workspace: defaultWorkspaceID,
		},
		{
			name: "wrong artifact",
			options: productionChannelFixtureOptions{
				enabled:        true,
				tamperArtifact: true,
			},
			workspace: defaultWorkspaceID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProductionChannelFixture(t, test.options)
			composition, err := openProductionChannelComposition(
				context.Background(),
				fixture.databasePath,
				fixture.artifactRoot,
				productionChannelEndpointInput{
					TenantID:       defaultTenantID,
					WorkspaceID:    test.workspace,
					EndpointID:     "endpoint-loopback",
					SecretResolver: fixture.resolver,
				},
			)
			if err == nil || composition != nil {
				if composition != nil {
					_ = composition.Close()
				}
				t.Fatalf("composition=%+v err=%v", composition, err)
			}
			if fixture.resolver.calls.Load() != 0 {
				t.Fatal("failed composition resolved a secret")
			}
		})
	}
}

func TestExplicitProductionChannelCompositionRejectsUnreconciledUnknown(
	t *testing.T,
) {
	outbound := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, _ *http.Request) {
			hijacker, ok := writer.(http.Hijacker)
			if !ok {
				t.Error("ResponseWriter does not implement Hijacker")
				return
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("Hijack: %v", err)
				return
			}
			_ = connection.Close()
		},
	))
	defer outbound.Close()
	fixture := newProductionChannelFixture(t, productionChannelFixtureOptions{
		enabled:     true,
		outboundURL: outbound.URL + "/channel/outbound",
	})
	input := productionChannelEndpointInput{
		TenantID:       defaultTenantID,
		WorkspaceID:    defaultWorkspaceID,
		EndpointID:     "endpoint-loopback",
		SecretResolver: fixture.resolver,
	}
	composition, err := openProductionChannelComposition(
		context.Background(), fixture.databasePath, fixture.artifactRoot, input,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := composition.channel.service.AdmitAndRun(
		context.Background(),
		channelservice.IngressInput{
			TenantID: defaultTenantID, WorkspaceID: defaultWorkspaceID,
			Deadline: time.Now().UTC().Add(time.Minute),
			Envelope: moduleapi.ChannelInboundEnvelopeV1{
				SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
				EndpointID:      "endpoint-loopback",
				ProviderEventID: "provider-event-unknown",
				ExternalUserID:  "external-user",
				Message:         "persist one ambiguous delivery",
				ReplyTarget:     json.RawMessage(`{"account_id":"account-loopback","conversation_id":"conversation-loopback"}`),
				CursorBefore:    json.RawMessage(`{"offset":0}`),
				CursorAfter:     json.RawMessage(`{"offset":1}`),
			},
		},
	)
	if runErr != nil || result.RunID == "" || !result.AdmissionCreated {
		t.Fatalf("create UNKNOWN Channel run = %+v, %v", result, runErr)
	}
	if err := composition.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := openProductionChannelComposition(
		context.Background(), fixture.databasePath, fixture.artifactRoot, input,
	)
	if err == nil || reopened != nil {
		if reopened != nil {
			_ = reopened.Close()
		}
		t.Fatalf("composition=%+v err=%v; want unreconciled UNKNOWN rejection", reopened, err)
	}
}

func TestExplicitProductionChannelCompositionRequiresAllIdentities(t *testing.T) {
	if composition, err := openProductionChannelComposition(
		context.Background(),
		"missing.sqlite",
		"missing-artifacts",
		productionChannelEndpointInput{},
	); err == nil || composition != nil {
		t.Fatalf("composition=%+v err=%v", composition, err)
	}
}

func newProductionChannelFixture(
	t *testing.T,
	options productionChannelFixtureOptions,
) productionChannelFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := options.seedPath
	if seedPath == "" {
		seedPath = exampleSeedPath(t)
	}
	initialized, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	})
	if err != nil {
		t.Fatalf("initialize Channel fixture: %v", err)
	}
	moduleID := options.moduleID
	if moduleID == "" {
		moduleID = localLoopbackChannelModuleID
	}
	protocol := options.adapterProtocol
	if protocol == "" {
		protocol = loopbackchannel.AdapterProtocolV1
	}
	assertion, manifestCanonical := productionLoopbackChannelArtifact(
		t, root, moduleID,
	)
	if err := stageArtifact(artifactRoot, assertion); err != nil {
		t.Fatalf("stage Channel artifact: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		manifestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := store.InstallModule(
		ctx,
		currentstore.InstallModuleInput{
			InstallationID:      "installation-channel-loopback",
			ModuleID:            moduleID,
			ExactVersion:        localLoopbackChannelVersion,
			ExpectedManifestRef: manifestRef,
			ManifestBytes:       manifestCanonical,
			ArtifactDigest:      assertion.ArtifactDigest,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := store.ActivateModule(
		ctx,
		currentstore.ActivateModuleInput{
			ActivationID:       "activation-channel-loopback",
			TenantID:           defaultTenantID,
			InstanceID:         "channel-loopback",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    loopbackchannel.AdapterIdentityV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	outboundURL := options.outboundURL
	if outboundURL == "" {
		outboundURL = "http://127.0.0.1:18080/channel/outbound"
	}
	parametersRaw, err := json.Marshal(map[string]any{
		"schema_version":     "loopback-http-parameters/v1",
		"inbound_path":       "/channel/inbound",
		"outbound_url":       outboundURL,
		"request_timeout_ms": 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := moduleapi.CanonicalJSON(parametersRaw)
	if err != nil {
		t.Fatal(err)
	}
	_, configCanonical, err := moduleapi.NewChannelBindingConfigV1(
		moduleapi.ChannelBindingConfigV1{
			SchemaVersion:   moduleapi.ChannelBindingConfigSchemaV1,
			AdapterProtocol: protocol,
			SecretRef:       "placeholder",
			Parameters:      json.RawMessage(parameters),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	config := putProductionChannelContent(
		t, store, currentstore.ContentConfig, configCanonical,
	)
	_, authorityCanonical, err := moduleapi.NewChannelAuthorityCeilingV1(
		moduleapi.ChannelAuthorityCeilingV1{
			SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
			TenantID:            defaultTenantID,
			AllowedWorkspaceIDs: []string{defaultWorkspaceID},
			AllowedEndpointIDs:  []string{"endpoint-loopback"},
			AllowReceive:        true,
			AllowSend:           true,
			MaxMessageBytes:     moduleapi.MaxChannelMessageBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authority := putProductionChannelContent(
		t, store, currentstore.ContentAuthorityCeiling, authorityCanonical,
	)
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	workspaceFound := false
	for index := range control.Workspaces {
		if control.Workspaces[index].Workspace.ID != defaultWorkspaceID {
			continue
		}
		workspaceFound = true
		control.Workspaces[index].ChannelEndpoints = append(
			control.Workspaces[index].ChannelEndpoints,
			controlcontract.ChannelEndpointDefinition{
				SchemaVersion:   controlcontract.ChannelEndpointSchemaVersionV1,
				EndpointID:      "endpoint-loopback",
				Channel:         localLoopbackChannelName,
				AccountID:       "account-loopback",
				ConversationID:  "conversation-loopback",
				TargetAgentID:   initialized.Defaults.AgentID,
				TargetProfileID: initialized.Defaults.ProfileID,
				CursorScopeKey:  "cursor/endpoint-loopback",
				Enabled:         false,
				Binding: controlcontract.BindingSpec{
					Port:                productionChannelPort,
					InstanceID:          activation.InstanceID,
					ConfigRef:           config.Digest,
					AuthorityCeilingRef: authority.Digest,
					StaticContextRefs:   []string{},
					FailurePolicy:       moduleapi.FailureRequired,
				},
			},
		)
		control.Workspaces[index].ChannelIdentities = append(
			control.Workspaces[index].ChannelIdentities,
			controlcontract.ChannelIdentityDefinition{
				Channel:        localLoopbackChannelName,
				AccountID:      "account-loopback",
				ExternalUserID: "external-user",
				PrincipalID:    defaultPrincipalID,
				ACLEpoch:       1,
				Active:         true,
			},
		)
	}
	if !workspaceFound {
		t.Fatal("default Workspace missing")
	}
	control.SnapshotID = "control-with-channel-loopback"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           moduleID,
		Version:            localLoopbackChannelVersion,
		ArtifactDigest:     assertion.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}
	catalog.GenerationID = "catalog-with-channel-loopback"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{
		Activation: provider,
		Provides:   []moduleapi.PortRef{productionChannelPort},
	})
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	published, err := store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	bindingDigest, err := moduleapi.ComputeChannelEndpointBindingDigestV1(
		moduleapi.PortBinding{
			Provider: provider, ConfigRef: config.Digest,
			AuthorityCeilingRef: authority.Digest,
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	cursorCanonical, err := moduleapi.CanonicalJSON([]byte(`{"offset":0}`))
	if err != nil {
		t.Fatal(err)
	}
	cursorDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentChannelCursor,
		"application/json",
		cursorCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !options.skipCursor {
		if _, err := store.SeedChannelCursor(
			ctx,
			currentstore.ChannelCursorSeedInput{
				PublishedBasis:        published,
				TenantID:              defaultTenantID,
				WorkspaceID:           defaultWorkspaceID,
				EndpointID:            "endpoint-loopback",
				CursorScopeKey:        "cursor/endpoint-loopback",
				EndpointBindingDigest: bindingDigest,
				CursorAfter: currentstore.ContentInput{
					Digest: cursorDigest, Kind: currentstore.ContentChannelCursor,
					MediaType: "application/json", CanonicalBytes: cursorCanonical,
				},
				Reason:           "OPERATOR_SEED",
				EndpointDisabled: true,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	if options.enabled {
		for index := range control.Workspaces {
			if control.Workspaces[index].Workspace.ID == defaultWorkspaceID {
				control.Workspaces[index].ChannelEndpoints[0].Enabled = true
			}
		}
		control.SnapshotID = "control-with-channel-loopback-enabled"
		control.Revision++
		control.Digest = ""
		_, enabledControlRef, enabledControlCanonical, err :=
			controlcontract.NewControlSnapshot(control)
		if err != nil {
			t.Fatal(err)
		}
		catalog.GenerationID = "catalog-with-channel-loopback-enabled"
		catalog.Generation++
		catalog.ControlSnapshotID = enabledControlRef.SnapshotID
		catalog.ControlSnapshotDigest = enabledControlRef.Digest
		catalog.Digest = ""
		_, enabledCatalogRef, enabledCatalogCanonical, err :=
			controlcontract.NewCatalogGeneration(catalog)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishControlCatalog(
			ctx,
			currentstore.PublishControlCatalogInput{
				ExpectedPointerRevision: published.PointerRevision,
				NewPointerRevision:      published.PointerRevision + 1,
				ControlRef:              enabledControlRef,
				ControlCanonical:        enabledControlCanonical,
				CatalogRef:              enabledCatalogRef,
				CatalogCanonical:        enabledCatalogCanonical,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	if options.tamperArtifact {
		if err := os.WriteFile(
			filepath.Join(artifactRoot, assertion.ArtifactDigest, "tampered.txt"),
			[]byte("tampered"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	return productionChannelFixture{
		databasePath: databasePath,
		artifactRoot: artifactRoot,
		provider:     provider,
		defaults:     initialized.Defaults,
		resolver:     &countingChannelSecretResolver{},
	}
}

func productionLoopbackChannelArtifact(
	t *testing.T,
	root string,
	moduleID string,
) (bootstrapseed.ModuleAssertion, []byte) {
	t.Helper()
	directory := filepath.Join(root, "channel-artifact-source")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"api_version":"freeagent.module/v1","id":"` + moduleID + `","provides":[{"exact_version":"v1","name":"channel.transport"}],"runtime":{"entrypoint":"` + localLoopbackChannelEntrypoint + `","mode":"TRUSTED_IN_PROCESS","protocol":"go-in-process/v1"},"version":"` + localLoopbackChannelVersion + `"}`)
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, manifestCanonical, err := moduleapi.ParseModuleManifestV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		directory, moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		t.Fatal(err)
	}
	size := uint64(len(manifestCanonical))
	for _, file := range files {
		size += uint64(len(file.Content))
	}
	return bootstrapseed.ModuleAssertion{
		ModuleID:                moduleID,
		ExactVersion:            localLoopbackChannelVersion,
		ArtifactDigest:          digest,
		ArtifactSizeBytes:       size,
		ArtifactDirectory:       directory,
		InstanceID:              "channel-loopback",
		ActivationRevision:      1,
		ExpectedExecutionClass:  moduleapi.ExecutionTrustedInProcess,
		ExpectedAdapterIdentity: loopbackchannel.AdapterIdentityV1,
	}, manifestCanonical
}

func putProductionChannelContent(
	t *testing.T,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	canonical []byte,
) currentstore.ContentRecord {
	t.Helper()
	digest, err := currentstore.ComputeContentDigest(
		kind, "application/json", canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.PutContent(
		context.Background(),
		currentstore.ContentInput{
			Digest:         digest,
			Kind:           kind,
			MediaType:      "application/json",
			CanonicalBytes: canonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
