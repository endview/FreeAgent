package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/loopbackchannel"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const moduleApplyChannelCursorSeedReasonV1 = "MODULE_APPLY_INITIAL_CURSOR"

func moduleApplyTargetsChannelV1(plan moduleApplyPlanV1) bool {
	return plan.BindingTarget.Kind ==
		moduleApplyBindingTargetWorkspaceChannelEndpointV1
}

func validateModuleApplyChannelAuthorityTargetV1(
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
) error {
	if !moduleApplyTargetsChannelV1(plan) || plan.Binding == nil ||
		plan.ChannelEndpoint == nil {
		return errors.New("enabled Channel target payload is incomplete")
	}
	if _, found := control.FindWorkspace(plan.BindingTarget.WorkspaceID); !found {
		return errors.New("target Channel Workspace is absent")
	}
	if _, found := control.FindAgent(plan.ChannelEndpoint.TargetAgentID); !found {
		return errors.New("target Channel Agent is absent")
	}
	if _, found := findModuleApplyProfileV1(
		control,
		plan.ChannelEndpoint.TargetProfileID,
	); !found {
		return errors.New("target Channel Profile is absent")
	}
	config, err := moduleapi.RestoreChannelBindingConfigV1(plan.Binding.Config)
	if err != nil || config.AdapterProtocol != loopbackchannel.AdapterProtocolV1 {
		return errors.Join(err, errors.New("Channel Config is not exact loopback-http/v1"))
	}
	if err := loopbackchannel.ValidateBindingConfigV1(config); err != nil {
		return err
	}
	authority, err := moduleapi.RestoreChannelAuthorityCeilingV1(
		plan.Binding.AuthorityCeiling,
	)
	if err != nil {
		return err
	}
	if authority.TenantID != plan.TenantID || !authority.AllowReceive ||
		!authority.AllowSend || len(authority.AllowedWorkspaceIDs) != 1 ||
		authority.AllowedWorkspaceIDs[0] != plan.BindingTarget.WorkspaceID ||
		len(authority.AllowedEndpointIDs) != 1 ||
		authority.AllowedEndpointIDs[0] != plan.BindingTarget.EndpointID {
		return errors.New(
			"Channel Authority must grant exactly the target Tenant, Workspace, and Endpoint for receive and send",
		)
	}
	return nil
}

func addEnabledChannelModuleBindingV1(
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	provider moduleapi.ActivatedModuleRef,
	configRef string,
	authorityRef string,
	staticContextRefs []string,
) (controlcontract.ControlSnapshot, controlcontract.CatalogGeneration, error) {
	if plan.Binding == nil || plan.ChannelEndpoint == nil {
		return control, catalog, errors.New("enabled Channel binding is incomplete")
	}
	if plan.Port != productionChannelPort || plan.Binding.PortBindingIndex != 0 ||
		plan.Binding.FailurePolicy != moduleapi.FailureRequired ||
		len(staticContextRefs) != 0 {
		return control, catalog, errors.New("Channel target requires one REQUIRED index-zero Binding")
	}
	workspaceIndex := -1
	for index := range control.Workspaces {
		workspace := &control.Workspaces[index]
		for _, endpoint := range workspace.ChannelEndpoints {
			if endpoint.EndpointID == plan.BindingTarget.EndpointID {
				return control, catalog, errors.New("target Channel Endpoint already exists")
			}
		}
		if workspace.Workspace.ID == plan.BindingTarget.WorkspaceID {
			workspaceIndex = index
		}
	}
	if workspaceIndex < 0 {
		return control, catalog, errors.New("target Channel Workspace is absent")
	}
	if err := validateModuleApplyChannelAuthorityTargetV1(plan, control); err != nil {
		return control, catalog, err
	}
	endpointPlan := *plan.ChannelEndpoint
	endpoint := controlcontract.ChannelEndpointDefinition{
		SchemaVersion:   controlcontract.ChannelEndpointSchemaVersionV1,
		EndpointID:      plan.BindingTarget.EndpointID,
		Channel:         endpointPlan.Channel,
		AccountID:       endpointPlan.AccountID,
		ConversationID:  endpointPlan.ConversationID,
		TargetAgentID:   endpointPlan.TargetAgentID,
		TargetProfileID: endpointPlan.TargetProfileID,
		CursorScopeKey:  endpointPlan.CursorScopeKey,
		Enabled:         true,
		Binding: controlcontract.BindingSpec{
			Port:                productionChannelPort,
			InstanceID:          plan.InstanceID,
			ConfigRef:           configRef,
			AuthorityCeilingRef: authorityRef,
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		},
	}
	control.Workspaces[workspaceIndex].ChannelEndpoints = append(
		control.Workspaces[workspaceIndex].ChannelEndpoints,
		endpoint,
	)
	if existing, found := catalog.FindInstance(plan.InstanceID); found {
		if existing.Activation != provider || len(existing.Provides) != 1 ||
			existing.Provides[0] != productionChannelPort {
			return control, catalog, errors.New("Catalog instance conflicts with Channel provider")
		}
	} else {
		catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{
			Activation: provider,
			Provides:   []moduleapi.PortRef{productionChannelPort},
		})
	}
	entry, found := catalog.FindInstance(plan.InstanceID)
	if !found {
		return control, catalog, errors.New("Channel provider is absent from candidate Catalog")
	}
	if _, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port: productionChannelPort,
		Bindings: []moduleapi.PortBinding{{
			Provider:            entry.Activation,
			ConfigRef:           configRef,
			AuthorityCeilingRef: authorityRef,
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		}},
	}); err != nil {
		return control, catalog, err
	}
	return control, catalog, nil
}

func inspectCurrentChannelModuleApplyStateV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	artifactRoot string,
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (bool, bool, error) {
	workspace, found := control.FindWorkspace(plan.BindingTarget.WorkspaceID)
	if !found {
		return false, true, nil
	}
	endpoint, endpointFound := workspace.FindChannelEndpoint(
		plan.BindingTarget.EndpointID,
	)
	if plan.DesiredState == moduleApplyDisabledV1 {
		if endpointFound {
			if endpoint.Binding.InstanceID != plan.InstanceID ||
				endpoint.Binding.Port != plan.Port {
				return false, true, nil
			}
			return false, false, nil
		}
		_, catalogHasInstance := catalog.FindInstance(plan.InstanceID)
		controlStillReferences := controlReferencesInstanceV1(control, plan.InstanceID)
		if controlStillReferences != catalogHasInstance {
			return false, true, nil
		}
		return true, false, nil
	}
	if plan.Module == nil || plan.Binding == nil || plan.ChannelEndpoint == nil {
		return false, true, nil
	}
	if !endpointFound {
		if entry, exists := catalog.FindInstance(plan.InstanceID); exists &&
			!moduleApplyCatalogEntryMatchesV1(entry, plan) {
			return false, true, nil
		}
		return false, false, nil
	}
	wantedEndpoint := *plan.ChannelEndpoint
	if endpoint.SchemaVersion != controlcontract.ChannelEndpointSchemaVersionV1 ||
		!endpoint.Enabled || endpoint.EndpointID != plan.BindingTarget.EndpointID ||
		endpoint.Channel != wantedEndpoint.Channel ||
		endpoint.AccountID != wantedEndpoint.AccountID ||
		endpoint.ConversationID != wantedEndpoint.ConversationID ||
		endpoint.TargetAgentID != wantedEndpoint.TargetAgentID ||
		endpoint.TargetProfileID != wantedEndpoint.TargetProfileID ||
		endpoint.CursorScopeKey != wantedEndpoint.CursorScopeKey {
		return false, true, nil
	}
	configRef, authorityRef, err := moduleApplyContentRefsV1(*plan.Binding)
	if err != nil {
		return false, false, err
	}
	if endpoint.Binding.Port != productionChannelPort ||
		endpoint.Binding.InstanceID != plan.InstanceID ||
		endpoint.Binding.ConfigRef != configRef ||
		endpoint.Binding.AuthorityCeilingRef != authorityRef ||
		len(endpoint.Binding.StaticContextRefs) != 0 ||
		endpoint.Binding.FailurePolicy != moduleapi.FailureRequired {
		return false, true, nil
	}
	entry, found := catalog.FindInstance(plan.InstanceID)
	if !found || !moduleApplyCatalogEntryMatchesV1(entry, plan) {
		return false, true, nil
	}
	for _, wanted := range []struct {
		ref   string
		kind  currentstore.ContentKind
		bytes []byte
	}{
		{ref: configRef, kind: currentstore.ContentConfig, bytes: plan.Binding.Config},
		{ref: authorityRef, kind: currentstore.ContentAuthorityCeiling, bytes: plan.Binding.AuthorityCeiling},
	} {
		record, err := store.GetContent(ctx, wanted.ref)
		if err != nil {
			return false, false, err
		}
		if record.Kind != wanted.kind || record.MediaType != moduleApplyJSONMediaType ||
			!bytes.Equal(record.CanonicalBytes, wanted.bytes) {
			return false, true, nil
		}
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		filepath.Join(artifactRoot, plan.Module.ArtifactDigest),
		moduleapi.ArtifactMetadataPaths{},
		plan.Module.ArtifactDigest,
		plan.Module.ArtifactSizeBytes,
	); err != nil {
		return false, true, nil
	}
	if err := verifyExactModuleApplyChannelCursorSeedV1(
		ctx,
		store,
		plan,
		endpoint,
		entry,
	); err != nil {
		return false, true, nil
	}
	return true, false, nil
}

func channelEndpointDefinitionMatchesPlanV1(
	endpoint controlcontract.ChannelEndpointDefinition,
	plan moduleApplyPlanV1,
) bool {
	if plan.ChannelEndpoint == nil {
		return false
	}
	wanted := *plan.ChannelEndpoint
	return endpoint.EndpointID == plan.BindingTarget.EndpointID &&
		endpoint.Channel == wanted.Channel && endpoint.AccountID == wanted.AccountID &&
		endpoint.ConversationID == wanted.ConversationID &&
		endpoint.TargetAgentID == wanted.TargetAgentID &&
		endpoint.TargetProfileID == wanted.TargetProfileID &&
		endpoint.CursorScopeKey == wanted.CursorScopeKey && endpoint.Enabled &&
		len(endpoint.Binding.StaticContextRefs) == 0
}

func verifyEnabledChannelModuleApplySemanticsV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	artifactRoot string,
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	if err := validateModuleApplyChannelAuthorityTargetV1(plan, control); err != nil {
		return err
	}
	workspace, found := control.FindWorkspace(plan.BindingTarget.WorkspaceID)
	if !found {
		return errors.New("target Channel Workspace is absent")
	}
	endpoint, found := workspace.FindChannelEndpoint(plan.BindingTarget.EndpointID)
	if !found || !channelEndpointDefinitionMatchesPlanV1(endpoint, plan) {
		return errors.New("enabled Channel Endpoint differs from plan")
	}
	entry, found := catalog.FindInstance(plan.InstanceID)
	if !found || !moduleApplyCatalogEntryMatchesV1(entry, plan) {
		return errors.New("enabled Channel Catalog entry differs from plan")
	}
	manifest, err := readModuleApplyLoopbackChannelMetadataV1(
		ctx,
		filepath.Join(artifactRoot, entry.Activation.ArtifactDigest),
	)
	if err != nil || len(manifest) == 0 {
		return errors.Join(err, errors.New("loopback Channel artifact is unavailable"))
	}
	return verifyExactModuleApplyChannelCursorSeedV1(
		ctx,
		store,
		plan,
		endpoint,
		entry,
	)
}

func buildModuleApplyChannelCursorPublicationV1(
	plan moduleApplyPlanV1,
	publication currentstore.PublishControlCatalogInput,
) (currentstore.PublishControlCatalogWithChannelCursorSeedInput, error) {
	if !moduleApplyTargetsChannelV1(plan) || plan.DesiredState != moduleApplyEnabledV1 ||
		plan.ChannelEndpoint == nil || plan.Binding == nil || len(plan.CursorSeed) == 0 {
		return currentstore.PublishControlCatalogWithChannelCursorSeedInput{},
			errors.New("enabled Channel cursor publication payload is incomplete")
	}
	control, err := controlcontract.RestoreControlSnapshot(
		publication.ControlCanonical,
		publication.ControlRef,
	)
	if err != nil {
		return currentstore.PublishControlCatalogWithChannelCursorSeedInput{}, err
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		publication.CatalogCanonical,
		publication.CatalogRef,
	)
	if err != nil {
		return currentstore.PublishControlCatalogWithChannelCursorSeedInput{}, err
	}
	workspace, found := control.FindWorkspace(plan.BindingTarget.WorkspaceID)
	if !found {
		return currentstore.PublishControlCatalogWithChannelCursorSeedInput{},
			errors.New("candidate Channel Workspace is absent")
	}
	endpoint, found := workspace.FindChannelEndpoint(plan.BindingTarget.EndpointID)
	if !found || !channelEndpointDefinitionMatchesPlanV1(endpoint, plan) {
		return currentstore.PublishControlCatalogWithChannelCursorSeedInput{},
			errors.New("candidate Channel Endpoint differs from plan")
	}
	entry, found := catalog.FindInstance(plan.InstanceID)
	if !found || !moduleApplyCatalogEntryMatchesV1(entry, plan) {
		return currentstore.PublishControlCatalogWithChannelCursorSeedInput{},
			errors.New("candidate Channel provider differs from plan")
	}
	bindingDigest, err := moduleApplyChannelBindingDigestV1(endpoint, entry)
	if err != nil {
		return currentstore.PublishControlCatalogWithChannelCursorSeedInput{}, err
	}
	cursorRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentChannelCursor,
		moduleApplyJSONMediaType,
		plan.CursorSeed,
	)
	if err != nil {
		return currentstore.PublishControlCatalogWithChannelCursorSeedInput{}, err
	}
	basis, err := newModuleApplyCandidateBasisV1(plan.TenantID, publication)
	if err != nil {
		return currentstore.PublishControlCatalogWithChannelCursorSeedInput{}, err
	}
	return currentstore.PublishControlCatalogWithChannelCursorSeedInput{
		Publication: publication,
		CursorSeed: currentstore.ChannelCursorSeedInput{
			PublishedBasis:        basis,
			TenantID:              plan.TenantID,
			WorkspaceID:           plan.BindingTarget.WorkspaceID,
			EndpointID:            plan.BindingTarget.EndpointID,
			CursorScopeKey:        endpoint.CursorScopeKey,
			EndpointBindingDigest: bindingDigest,
			CursorAfter: currentstore.ContentInput{
				Digest:         cursorRef,
				Kind:           currentstore.ContentChannelCursor,
				MediaType:      moduleApplyJSONMediaType,
				CanonicalBytes: bytes.Clone(plan.CursorSeed),
			},
			Reason: moduleApplyChannelCursorSeedReasonV1,
		},
	}, nil
}

func verifyExactModuleApplyChannelCursorSeedV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	plan moduleApplyPlanV1,
	endpoint controlcontract.ChannelEndpointDefinition,
	entry controlcontract.CatalogEntry,
) error {
	if len(plan.CursorSeed) == 0 {
		return errors.New("Channel plan cursor seed is absent")
	}
	bindingDigest, err := moduleApplyChannelBindingDigestV1(endpoint, entry)
	if err != nil {
		return err
	}
	cursorRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentChannelCursor,
		moduleApplyJSONMediaType,
		plan.CursorSeed,
	)
	if err != nil {
		return err
	}
	seed, err := store.GetChannelCursorSeed(
		ctx,
		plan.TenantID,
		plan.BindingTarget.EndpointID,
		endpoint.CursorScopeKey,
	)
	if err != nil {
		return err
	}
	if seed.TenantID != plan.TenantID ||
		seed.WorkspaceID != plan.BindingTarget.WorkspaceID ||
		seed.EndpointID != plan.BindingTarget.EndpointID ||
		seed.CursorScopeKey != endpoint.CursorScopeKey || seed.CursorRevision != 0 ||
		seed.CursorBeforeRef != "" || seed.CursorAfterRef != cursorRef ||
		seed.EndpointBindingDigest != bindingDigest ||
		seed.Disposition != currentstore.ChannelCursorSeed ||
		seed.Reason != moduleApplyChannelCursorSeedReasonV1 {
		return errors.New("revision-zero Channel Cursor seed differs from plan")
	}
	return nil
}

func moduleApplyChannelBindingDigestV1(
	endpoint controlcontract.ChannelEndpointDefinition,
	entry controlcontract.CatalogEntry,
) (string, error) {
	if endpoint.Binding.InstanceID != entry.Activation.InstanceID ||
		endpoint.Binding.Port != productionChannelPort {
		return "", errors.New("Channel Endpoint and Catalog provider differ")
	}
	return moduleapi.ComputeChannelEndpointBindingDigestV1(moduleapi.PortBinding{
		Provider:            entry.Activation,
		ConfigRef:           endpoint.Binding.ConfigRef,
		AuthorityCeilingRef: endpoint.Binding.AuthorityCeilingRef,
		StaticContextRefs:   []string{},
		FailurePolicy:       endpoint.Binding.FailurePolicy,
	})
}
