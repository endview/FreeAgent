package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleApplyDualModuleID   = "freeagent.test.dual.action_channel"
	moduleApplyDualVersion    = "1.0.0"
	moduleApplyDualInstanceID = "dual-action-channel"
	moduleApplyDualEndpointID = "endpoint-dual-action-channel"
)

func TestModuleApplyDisableRetainsCatalogForWorkspaceChannelReference(
	t *testing.T,
) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	seedDualActionChannelPublicationV1(t, databasePath, artifactRoot)

	disablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-shared-channel.json"),
		newNamedDisabledModuleApplyPlanV1(
			t,
			moduleApplyTestProfileID,
			moduleApplyDualInstanceID,
			2,
		),
	)
	disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePlan,
		"",
		"",
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied ||
		disabled.PointerRevision != 3 {
		t.Fatalf("DISABLE shared Channel reference=%+v, %v", disabled, err)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, loadErr := store.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	closeErr := store.Close()
	if loadErr != nil || closeErr != nil {
		t.Fatalf("load disabled shared-reference publication: %v", errors.Join(loadErr, closeErr))
	}
	if basis.PointerRevision != 3 {
		t.Fatalf("disabled shared-reference basis=%+v", basis)
	}
	entry, found := catalog.FindInstance(moduleApplyDualInstanceID)
	if !found || entry.Activation.InstanceID != moduleApplyDualInstanceID ||
		!moduleApplyDisableProvidesPortV1(entry.Provides, productionActionPort) ||
		!moduleApplyDisableProvidesPortV1(entry.Provides, productionChannelPort) {
		t.Fatalf("shared Channel Catalog entry was removed or changed: found=%v entry=%+v", found, entry)
	}
	profile, found := control.FindProfile(moduleApplyTestProfileID)
	if !found {
		t.Fatal("target Profile is absent after DISABLE")
	}
	for _, binding := range profile.Bindings {
		if binding.Port == productionActionPort &&
			binding.InstanceID == moduleApplyDualInstanceID {
			t.Fatalf("DISABLE retained target Profile Action Binding: %+v", binding)
		}
	}
	workspace, found := control.FindWorkspace(defaultWorkspaceID)
	if !found {
		t.Fatal("default Workspace is absent after DISABLE")
	}
	endpoint, found := workspace.FindChannelEndpoint(moduleApplyDualEndpointID)
	if !found || endpoint.Enabled ||
		endpoint.Binding.Port != productionChannelPort ||
		endpoint.Binding.InstanceID != moduleApplyDualInstanceID {
		t.Fatalf("DISABLE changed shared Workspace Channel reference: found=%v endpoint=%+v", found, endpoint)
	}
}

func TestModuleApplyDisableExactRetryRejectsAlteredPredecessorPublication(
	t *testing.T,
) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	enablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	if enabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePlan,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	); err != nil || enabled.Status != moduleApplyStatusApplied ||
		enabled.PointerRevision != 2 {
		t.Fatalf("prepare ENABLE publication=%+v, %v", enabled, err)
	}
	disablePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable.json"),
		newDisabledModuleApplyPlanFixtureV1(t, 2),
	)
	disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePlan,
		"",
		"",
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied ||
		disabled.PointerRevision != 3 {
		t.Fatalf("prepare DISABLE publication=%+v, %v", disabled, err)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	currentBasis, currentControl, _, currentErr := store.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	predecessorControl, predecessorCatalog, predecessorErr :=
		store.LoadControlCatalogRevision(ctx, defaultTenantID, 2, 2)
	closeErr := store.Close()
	if currentErr != nil || predecessorErr != nil || closeErr != nil {
		t.Fatalf(
			"load DISABLE current/predecessor: %v",
			errors.Join(currentErr, predecessorErr, closeErr),
		)
	}
	if len(predecessorControl.Agents) == 0 || len(currentControl.Agents) == 0 {
		t.Fatal("publication has no Agent available for non-target mutation")
	}
	predecessorControl.Agents[0].Digest = moduleApplyDisableDifferentDigestV1(
		predecessorControl.Agents[0].Digest,
	)
	predecessorControl.Digest = ""
	_, alteredControlRef, alteredControlCanonical, err :=
		controlcontract.NewControlSnapshot(predecessorControl)
	if err != nil {
		t.Fatalf("freeze altered predecessor Control: %v", err)
	}
	if alteredControlRef.SnapshotID != predecessorControl.SnapshotID ||
		alteredControlRef.Revision != 2 ||
		alteredControlRef.Digest == predecessorCatalog.ControlSnapshotDigest {
		t.Fatalf("altered predecessor Control identity=%+v", alteredControlRef)
	}
	predecessorCatalog.ControlSnapshotDigest = alteredControlRef.Digest
	predecessorCatalog.Digest = ""
	_, alteredCatalogRef, alteredCatalogCanonical, err :=
		controlcontract.NewCatalogGeneration(predecessorCatalog)
	if err != nil {
		t.Fatalf("freeze altered predecessor Catalog: %v", err)
	}
	if alteredCatalogRef.GenerationID != predecessorCatalog.GenerationID ||
		alteredCatalogRef.Generation != 2 {
		t.Fatalf("altered predecessor Catalog identity=%+v", alteredCatalogRef)
	}

	var controlResult, catalogResult sql.Result
	withCmdClosedFileTamperV1(
		t,
		databasePath,
		[]string{
			"control_snapshots_reject_update",
			"runtime_catalog_generations_reject_update",
		},
		func(ctx context.Context, connection *sql.Conn) error {
			var controlUpdateErr, catalogUpdateErr error
			controlResult, controlUpdateErr = connection.ExecContext(ctx, `
				UPDATE control_snapshots
				SET canonical_json=?, digest=?
				WHERE snapshot_id=? AND revision=?
			`, alteredControlCanonical, alteredControlRef.Digest, alteredControlRef.SnapshotID, 2)
			catalogResult, catalogUpdateErr = connection.ExecContext(ctx, `
				UPDATE runtime_catalog_generations
				SET canonical_json=?, digest=?
				WHERE generation_id=? AND generation=?
			`, alteredCatalogCanonical, alteredCatalogRef.Digest, alteredCatalogRef.GenerationID, 2)
			return errors.Join(controlUpdateErr, catalogUpdateErr)
		},
	)
	controlRows := moduleApplyDisableRowsAffectedV1(controlResult, nil)
	catalogRows := moduleApplyDisableRowsAffectedV1(catalogResult, nil)
	if controlRows != 1 || catalogRows != 1 {
		t.Fatalf(
			"alter predecessor publication rows: control=%d catalog=%d",
			controlRows,
			catalogRows,
		)
	}

	store, err = currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err == nil || store != nil {
		if store != nil {
			_ = store.Close()
		}
		t.Fatalf("altered predecessor escaped full publication gate: store=%v error=%v", store, err)
	}
	if errors.Is(err, currentstore.ErrOwnerActive) {
		t.Fatalf("altered predecessor retained a Store owner: %v", err)
	}
	if got := loadCmdRawPublishedBasisV1(t, databasePath); got != currentBasis {
		t.Fatalf("historical mutation changed current pointer: got=%+v want=%+v", got, currentBasis)
	}
	rowsBeforeRetry := moduleApplyMutationRowCountsV1(t, databasePath)

	_, err = runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePlan,
		"",
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "(STORE_INVALID)") {
		t.Fatalf("altered predecessor exact DISABLE retry error=%v", err)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)
	if rowsAfterRetry := moduleApplyMutationRowCountsV1(t, databasePath); !reflect.DeepEqual(
		rowsAfterRetry,
		rowsBeforeRetry,
	) {
		t.Fatalf(
			"rejected exact retry changed Store rows: before=%v after=%v",
			rowsBeforeRetry,
			rowsAfterRetry,
		)
	}
	if got := loadCmdRawPublishedBasisV1(t, databasePath); got != currentBasis {
		t.Fatalf("rejected exact retry changed current pointer: got=%+v want=%+v", got, currentBasis)
	}
}

func seedDualActionChannelPublicationV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
) {
	t.Helper()
	ctx := context.Background()
	manifestInput, err := json.Marshal(moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         moduleApplyDualModuleID,
		Version:    moduleApplyDualVersion,
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestTrustedInProcess,
			Protocol:   moduleapi.RuntimeProtocolGoInProcessV1,
			Entrypoint: "test.dual-action-channel/v1",
		},
		Provides: []moduleapi.PortRef{
			productionActionPort,
			productionChannelPort,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestCanonical, err := moduleapi.CanonicalJSON(manifestInput)
	if err != nil {
		t.Fatal(err)
	}
	_, manifestCanonical, err = moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	temporaryArtifact := filepath.Join(filepath.Dir(artifactRoot), "dual-artifact")
	if err := os.MkdirAll(temporaryArtifact, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(temporaryArtifact, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := moduleapi.ComputeArtifactDigestFromDirectory(
		manifestCanonical,
		temporaryArtifact,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatal(err)
	}
	finalArtifact := filepath.Join(artifactRoot, artifactDigest)
	if err := os.Rename(temporaryArtifact, finalArtifact); err != nil {
		t.Fatal(err)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Fatalf("close dual Action/Channel Store: %v", closeErr)
		}
	}()
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		moduleApplyJSONMediaType,
		manifestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
		InstallationID:      "installation-dual-action-channel",
		ModuleID:            moduleApplyDualModuleID,
		ExactVersion:        moduleApplyDualVersion,
		ExpectedManifestRef: manifestRef,
		ManifestBytes:       manifestCanonical,
		ArtifactDigest:      artifactDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := store.ActivateModule(ctx, currentstore.ActivateModuleInput{
		ActivationID:       "activation-dual-action-channel",
		TenantID:           defaultTenantID,
		InstanceID:         moduleApplyDualInstanceID,
		InstallationID:     installation.InstallationID,
		ActivationRevision: 1,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    "test.dual-action-channel/v1",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, actionConfigCanonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "dual.test",
				ProviderActionID: "dual.test",
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   256,
			}},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, actionAuthorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 defaultTenantID,
			AllowedWorkspaceIDs:      []string{defaultWorkspaceID},
			AllowedProviderActionIDs: []string{"dual.test"},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           256,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, channelConfigCanonical, err := moduleapi.NewChannelBindingConfigV1(
		moduleapi.ChannelBindingConfigV1{
			SchemaVersion:   moduleapi.ChannelBindingConfigSchemaV1,
			AdapterProtocol: "test-channel/v1",
			SecretRef:       "placeholder",
			Parameters:      json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, channelAuthorityCanonical, err := moduleapi.NewChannelAuthorityCeilingV1(
		moduleapi.ChannelAuthorityCeilingV1{
			SchemaVersion:       moduleapi.ChannelAuthorityCeilingSchemaV1,
			TenantID:            defaultTenantID,
			AllowedWorkspaceIDs: []string{defaultWorkspaceID},
			AllowedEndpointIDs:  []string{moduleApplyDualEndpointID},
			AllowReceive:        true,
			AllowSend:           true,
			MaxMessageBytes:     1024,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	actionConfigRef := putModuleApplyDisableContentV1(
		t,
		store,
		currentstore.ContentConfig,
		actionConfigCanonical,
	)
	actionAuthorityRef := putModuleApplyDisableContentV1(
		t,
		store,
		currentstore.ContentAuthorityCeiling,
		actionAuthorityCanonical,
	)
	channelConfigRef := putModuleApplyDisableContentV1(
		t,
		store,
		currentstore.ContentConfig,
		channelConfigCanonical,
	)
	channelAuthorityRef := putModuleApplyDisableContentV1(
		t,
		store,
		currentstore.ContentAuthorityCeiling,
		channelAuthorityCanonical,
	)

	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	profileFound := false
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID != moduleApplyTestProfileID {
			continue
		}
		profileFound = true
		control.Profiles[index].Bindings = append(
			control.Profiles[index].Bindings,
			controlcontract.BindingSpec{
				Port:                productionActionPort,
				InstanceID:          moduleApplyDualInstanceID,
				ConfigRef:           actionConfigRef,
				AuthorityCeilingRef: actionAuthorityRef,
				StaticContextRefs:   []string{},
				FailurePolicy:       moduleapi.FailureRequired,
			},
		)
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
				EndpointID:      moduleApplyDualEndpointID,
				Channel:         "test-channel",
				AccountID:       "test-account",
				ConversationID:  "test-conversation",
				TargetAgentID:   defaultAgentID,
				TargetProfileID: moduleApplyTestProfileID,
				CursorScopeKey:  "cursor/dual-action-channel",
				Enabled:         false,
				Binding: controlcontract.BindingSpec{
					Port:                productionChannelPort,
					InstanceID:          moduleApplyDualInstanceID,
					ConfigRef:           channelConfigRef,
					AuthorityCeilingRef: channelAuthorityRef,
					StaticContextRefs:   []string{},
					FailurePolicy:       moduleapi.FailureRequired,
				},
			},
		)
	}
	if !profileFound || !workspaceFound {
		t.Fatalf("dual publication target missing: Profile=%v Workspace=%v", profileFound, workspaceFound)
	}
	control.SnapshotID = "control-dual-action-channel"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           moduleApplyDualModuleID,
		Version:            moduleApplyDualVersion,
		ArtifactDigest:     artifactDigest,
		InstanceID:         moduleApplyDualInstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}
	catalog.GenerationID = "catalog-dual-action-channel"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{
		Activation: provider,
		Provides: []moduleapi.PortRef{
			productionActionPort,
			productionChannelPort,
		},
	})
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	published, err := store.PublishControlCatalog(ctx, currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: basis.PointerRevision,
		NewPointerRevision:      basis.PointerRevision + 1,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	})
	if err != nil || published.PointerRevision != 2 {
		t.Fatalf("publish dual Action/Channel fixture=%+v, %v", published, err)
	}
}

func putModuleApplyDisableContentV1(
	t *testing.T,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	canonical []byte,
) string {
	t.Helper()
	digest, err := currentstore.ComputeContentDigest(
		kind,
		moduleApplyJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.PutContent(context.Background(), currentstore.ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      moduleApplyJSONMediaType,
		CanonicalBytes: canonical,
	})
	if err != nil || record.Digest != digest {
		t.Fatalf("put dual Action/Channel content=%+v, %v", record, err)
	}
	return digest
}

func moduleApplyDisableProvidesPortV1(
	ports []moduleapi.PortRef,
	want moduleapi.PortRef,
) bool {
	for _, port := range ports {
		if port == want {
			return true
		}
	}
	return false
}

func moduleApplyDisableDifferentDigestV1(current string) string {
	for _, value := range []string{strings.Repeat("f", 64), strings.Repeat("e", 64)} {
		if value != current {
			return value
		}
	}
	panic("unreachable digest choice")
}

func moduleApplyDisableRowsAffectedV1(result sql.Result, err error) int64 {
	if err != nil || result == nil {
		return -1
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return -1
	}
	return rows
}
