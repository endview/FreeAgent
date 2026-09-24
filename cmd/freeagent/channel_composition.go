package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/channelservice"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/loopbackchannel"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	localLoopbackChannelModuleID   = "freeagent.builtin.channel.loopback-http"
	localLoopbackChannelVersion    = "1.0.0"
	localLoopbackChannelName       = "loopback-http"
	localLoopbackChannelEntrypoint = "freeagent.manifest-request.channel.loopback-http/v1"
)

var productionChannelPort = moduleapi.PortRef{
	Name: moduleapi.PortNameChannelTransport, ExactVersion: moduleapi.PortVersionV1,
}

// productionChannelEndpointInput is deliberately singular. The first
// production slice composes exactly one operator-selected endpoint and never
// discovers or starts every Channel endpoint in a tenant.
type productionChannelEndpointInput struct {
	TenantID       string
	WorkspaceID    string
	EndpointID     string
	SecretResolver loopbackchannel.SecretResolver
}

type productionChannelEndpointRuntime struct {
	tenantID    string
	workspaceID string
	endpoint    controlcontract.ChannelEndpointDefinition
	binding     moduleapi.PortBinding
	adapter     *loopbackchannel.Adapter
	service     *channelservice.Service
	handler     *channelservice.HTTPHandler
}

// openProductionChannelComposition is the explicit opt-in composition path.
// It does not add CLI flags, listeners, handlers or workers. The ordinary
// openProductionComposition path remains separate and never calls any of the
// Channel resolution or artifact functions below.
func openProductionChannelComposition(
	ctx context.Context,
	databasePath string,
	artifactRoot string,
	input productionChannelEndpointInput,
) (*productionComposition, error) {
	return openProductionChannelCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		input,
		productionCompositionOptions{},
	)
}

func openProductionChannelCompositionWithOptions(
	ctx context.Context,
	databasePath string,
	artifactRoot string,
	input productionChannelEndpointInput,
	options productionCompositionOptions,
) (*productionComposition, error) {
	if ctx == nil {
		return nil, errors.New("composition: context is nil")
	}
	if options.FairScheduler != nil {
		return nil, errors.New(
			"composition: Channel composition does not support the fair Scheduler",
		)
	}
	if err := validateProductionChannelEndpointInput(input); err != nil {
		return nil, err
	}
	if _, err := currentstore.VerifyCurrentStoreReadOnly(ctx, databasePath); err != nil {
		return nil, err
	}
	root, err := resolveExistingArtifactRootV1(artifactRoot)
	if err != nil {
		return nil, err
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*productionComposition, error) {
		return nil, errors.Join(cause, store.Close())
	}
	if err := runProductionStartupRecovery(ctx, store); err != nil {
		return fail(err)
	}
	if err := currentbackup.VerifyCurrentStoreSemanticClosure(
		ctx,
		databasePath,
	); err != nil {
		return fail(fmt.Errorf(
			"composition: Channel semantic enablement gate: %w",
			err,
		))
	}
	_, control, catalog, err := store.LoadPublishedBasis(ctx, input.TenantID)
	if err != nil {
		return fail(err)
	}
	registration, channel, err := newProductionChannelEndpointRegistration(
		ctx, store, root, control, catalog, input,
	)
	if err != nil {
		return fail(err)
	}
	registry, err := newProductionAdapterRegistryWithModelRuntimesAndExtras(
		root,
		catalog.Entries,
		options.DeepSeek,
		options.Zhipu,
		options.RemoteAction,
		options.WASMAction,
		registration,
	)
	if err != nil {
		return fail(err)
	}
	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		return fail(err)
	}
	materializer, err := actionmaterializer.New(registry)
	if err != nil {
		return fail(err)
	}
	chat, err := localchat.NewActionChatService(store, loop, materializer)
	if err != nil {
		return fail(err)
	}
	channel.service, err = channelservice.NewActionCapableService(
		store,
		loop,
		materializer,
	)
	if err != nil {
		return fail(err)
	}
	channel.handler, err = channelservice.NewHTTPHandler(
		channel.tenantID,
		channel.workspaceID,
		channel.adapter.InboundPath(),
		channel.adapter,
		channel.service,
	)
	if err != nil {
		return fail(err)
	}
	return &productionComposition{
		store:    store,
		registry: registry,
		loop:     loop,
		chat:     chat,
		channel:  channel,
	}, nil
}

func newProductionChannelEndpointRegistration(
	ctx context.Context,
	store *currentstore.Store,
	artifactRoot string,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	input productionChannelEndpointInput,
) (exactadapter.Registration, *productionChannelEndpointRuntime, error) {
	workspace, found := control.FindWorkspace(input.WorkspaceID)
	if !found || workspace.Workspace.ID != input.WorkspaceID {
		return exactadapter.Registration{}, nil, errors.New(
			"composition: selected Channel Workspace is absent",
		)
	}
	var endpoint controlcontract.ChannelEndpointDefinition
	matches := 0
	for _, candidate := range workspace.ChannelEndpoints {
		if candidate.EndpointID == input.EndpointID {
			endpoint = candidate
			matches++
		}
	}
	if matches != 1 || !endpoint.Enabled {
		return exactadapter.Registration{}, nil, errors.New(
			"composition: selected Channel Endpoint is absent, ambiguous, or disabled",
		)
	}
	if endpoint.Channel != localLoopbackChannelName ||
		endpoint.Binding.Port != productionChannelPort ||
		endpoint.Binding.FailurePolicy != moduleapi.FailureRequired ||
		len(endpoint.Binding.StaticContextRefs) != 0 {
		return exactadapter.Registration{}, nil, errors.New(
			"composition: selected Endpoint is not one exact required loopback channel.transport/v1 Binding",
		)
	}
	entry, found := catalog.FindInstance(endpoint.Binding.InstanceID)
	if !found || len(entry.Provides) != 1 || entry.Provides[0] != productionChannelPort {
		return exactadapter.Registration{}, nil, errors.New(
			"composition: selected Endpoint has no unambiguous exact Catalog provider",
		)
	}
	provider := entry.Activation
	if provider.ModuleID != localLoopbackChannelModuleID ||
		provider.Version != localLoopbackChannelVersion ||
		provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		provider.AdapterIdentity != loopbackchannel.AdapterIdentityV1 {
		return exactadapter.Registration{}, nil, errors.New(
			"composition: selected Channel provider is outside the compiled first-party loopback trust set",
		)
	}
	providerMatches := 0
	for _, candidate := range catalog.Entries {
		if candidate.Activation.ArtifactDigest == provider.ArtifactDigest &&
			candidate.Activation.AdapterIdentity == provider.AdapterIdentity {
			for _, provided := range candidate.Provides {
				if provided == productionChannelPort {
					providerMatches++
					break
				}
			}
		}
	}
	if providerMatches != 1 {
		return exactadapter.Registration{}, nil, errors.New(
			"composition: selected Channel provider key is ambiguous in the current Catalog",
		)
	}
	binding := moduleapi.PortBinding{
		Provider:            provider,
		ConfigRef:           endpoint.Binding.ConfigRef,
		AuthorityCeilingRef: endpoint.Binding.AuthorityCeilingRef,
		StaticContextRefs:   []string{},
		FailurePolicy:       endpoint.Binding.FailurePolicy,
	}
	if _, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port: productionChannelPort, Bindings: []moduleapi.PortBinding{binding},
	}); err != nil {
		return exactadapter.Registration{}, nil, fmt.Errorf(
			"composition: invalid selected Channel Binding: %w", err,
		)
	}
	bindingDigest, err := moduleapi.ComputeChannelEndpointBindingDigestV1(binding)
	if err != nil {
		return exactadapter.Registration{}, nil, fmt.Errorf(
			"composition: compute selected Channel Binding identity: %w", err,
		)
	}
	cursor, err := store.GetCurrentChannelCursor(
		ctx,
		input.TenantID,
		input.EndpointID,
		endpoint.CursorScopeKey,
	)
	if err != nil {
		return exactadapter.Registration{}, nil, fmt.Errorf(
			"composition: selected Channel Endpoint has no matching operator-initialized Cursor: %w",
			err,
		)
	}
	if cursor.WorkspaceID != input.WorkspaceID ||
		cursor.EndpointBindingDigest != bindingDigest {
		return exactadapter.Registration{}, nil, errors.New(
			"composition: selected Channel Cursor does not match the current Workspace Binding",
		)
	}
	startup, err := store.ScanStartupRecovery(ctx)
	if err != nil {
		return exactadapter.Registration{}, nil, fmt.Errorf(
			"composition: scan Channel reconciliation gate: %w", err,
		)
	}
	for _, candidate := range startup {
		if candidate.UnsettledChannelAttemptID == "" ||
			candidate.UnsettledChannelAttemptState != currentstore.DispatchUnknown {
			continue
		}
		record, recordErr := store.GetChannelDispatchRecord(
			ctx,
			candidate.UnsettledChannelAttemptID,
		)
		if recordErr != nil {
			return exactadapter.Registration{}, nil, fmt.Errorf(
				"composition: verify Channel reconciliation gate: %w", recordErr,
			)
		}
		if record.Attempt.EndpointID == input.EndpointID {
			return exactadapter.Registration{}, nil, errors.New(
				"composition: selected Channel Endpoint has an unreconciled UNKNOWN Attempt",
			)
		}
	}

	configRecord, err := store.GetContent(ctx, binding.ConfigRef)
	if err != nil || configRecord.Kind != currentstore.ContentConfig ||
		configRecord.MediaType != "application/json" {
		return exactadapter.Registration{}, nil, fmt.Errorf(
			"composition: selected Channel CONFIG closure is unavailable: %w", err,
		)
	}
	config, err := moduleapi.RestoreChannelBindingConfigV1(configRecord.CanonicalBytes)
	if err != nil || config.AdapterProtocol != loopbackchannel.AdapterProtocolV1 {
		return exactadapter.Registration{}, nil, fmt.Errorf(
			"composition: selected Channel CONFIG is not loopback-only: %w", err,
		)
	}
	authorityRecord, err := store.GetContent(ctx, binding.AuthorityCeilingRef)
	if err != nil || authorityRecord.Kind != currentstore.ContentAuthorityCeiling ||
		authorityRecord.MediaType != "application/json" {
		return exactadapter.Registration{}, nil, fmt.Errorf(
			"composition: selected Channel AUTHORITY closure is unavailable: %w", err,
		)
	}
	authority, err := moduleapi.RestoreChannelAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	)
	if err != nil || authority.TenantID != input.TenantID ||
		!authority.AllowReceive || !authority.AllowSend ||
		!productionChannelSetContains(authority.AllowedWorkspaceIDs, input.WorkspaceID) ||
		!productionChannelSetContains(authority.AllowedEndpointIDs, input.EndpointID) {
		return exactadapter.Registration{}, nil, fmt.Errorf(
			"composition: selected Channel AUTHORITY does not grant the exact endpoint: %w", err,
		)
	}
	if err := verifyProductionLoopbackChannelArtifact(
		ctx, artifactRoot, provider,
	); err != nil {
		return exactadapter.Registration{}, nil, err
	}
	adapter, err := loopbackchannel.New(
		provider,
		endpoint.EndpointID,
		endpoint.AccountID,
		endpoint.ConversationID,
		config,
		input.SecretResolver,
	)
	if err != nil {
		return exactadapter.Registration{}, nil, fmt.Errorf(
			"composition: construct selected loopback Channel adapter: %w", err,
		)
	}
	registration := exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         adapter,
	}
	return registration, &productionChannelEndpointRuntime{
		tenantID:    input.TenantID,
		workspaceID: input.WorkspaceID,
		endpoint:    endpoint,
		binding:     binding,
		adapter:     adapter,
	}, nil
}

func verifyProductionLoopbackChannelArtifact(
	ctx context.Context,
	artifactRoot string,
	provider moduleapi.ActivatedModuleRef,
) error {
	directory := filepath.Join(artifactRoot, provider.ArtifactDigest)
	digest, _, err := inspectArtifact(directory)
	if err != nil {
		return fmt.Errorf("composition: inspect loopback Channel artifact: %w", err)
	}
	if digest != provider.ArtifactDigest {
		return errors.New("composition: installed loopback Channel artifact digest mismatch")
	}
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx, directory,
	)
	if err != nil {
		return fmt.Errorf("composition: read loopback Channel manifest: %w", err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		return fmt.Errorf("composition: restore loopback Channel manifest: %w", err)
	}
	if manifest.ID != provider.ModuleID || manifest.Version != provider.Version ||
		manifest.ID != localLoopbackChannelModuleID ||
		manifest.Version != localLoopbackChannelVersion ||
		manifest.Runtime.Mode != moduleapi.RuntimeModeRequestTrustedInProcess ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolGoInProcessV1 ||
		manifest.Runtime.Entrypoint != localLoopbackChannelEntrypoint ||
		len(manifest.Provides) != 1 || manifest.Provides[0] != productionChannelPort ||
		len(manifest.Requires) != 0 || len(manifest.RequestedPermissions) != 0 {
		return errors.New(
			"composition: loopback Channel artifact manifest does not close the exact first-party provider",
		)
	}
	return nil
}

func validateProductionChannelEndpointInput(
	input productionChannelEndpointInput,
) error {
	for name, value := range map[string]string{
		"tenant":    input.TenantID,
		"workspace": input.WorkspaceID,
		"endpoint":  input.EndpointID,
	} {
		if !validProductionChannelOpaqueID(value) {
			return fmt.Errorf(
				"composition: explicit Channel %s identity is required and must be canonical",
				name,
			)
		}
	}
	return nil
}

func validProductionChannelOpaqueID(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func productionChannelSetContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
