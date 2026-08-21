package main

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
)

func TestW2E3ChannelUnifiedModuleAssemblyLifecycleV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	identityBasis := publishModuleApplyChannelIdentityV1(t, databasePath)
	assertion, _ := productionLoopbackChannelArtifact(
		t,
		root,
		localLoopbackChannelModuleID,
	)

	value := validModuleApplyChannelPlanValueV1(t)
	value["expected_pointer_revision"] = identityBasis.PointerRevision
	module := value["module"].(map[string]any)
	module["artifact_digest"] = assertion.ArtifactDigest
	module["artifact_size_bytes"] = assertion.ArtifactSizeBytes
	planCanonical := canonicalModuleApplyPlanTestJSON(t, value)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-channel.json"),
		planCanonical,
	)

	beforeDryRun := moduleOperatorStoreHashV1(t, databasePath)
	dryRun, err := runW2E2TrustedModuleCommandV1[moduleDryRunResultV1](
		ctx,
		runModuleDryRun,
		databasePath,
		artifactRoot,
		planPath,
		assertion.ArtifactDirectory,
		assertion.ArtifactDigest,
	)
	if err != nil {
		t.Fatalf("Channel module-dry-run: %v", err)
	}
	if dryRun.Status != moduleApplyStatusWouldApply ||
		dryRun.BindingTarget.Kind != moduleApplyBindingTargetWorkspaceChannelEndpointV1 ||
		dryRun.BindingTarget.WorkspaceID != defaultWorkspaceID ||
		dryRun.BindingTarget.EndpointID != "endpoint-apply" ||
		dryRun.Changes.Binding.Change != moduleDryRunBindingInsertV1 ||
		dryRun.Changes.ChannelCursorSeedRef == "" {
		t.Fatalf("Channel module-dry-run=%+v", dryRun)
	}
	if after := moduleOperatorStoreHashV1(t, databasePath); after != beforeDryRun {
		t.Fatal("Channel module-dry-run changed Current Store")
	}
	assertModuleApplyChannelSeedAbsentV1(t, databasePath)

	applied, err := runW2E2TrustedModuleCommandV1[moduleApplyResultV1](
		ctx,
		runModuleApply,
		databasePath,
		artifactRoot,
		planPath,
		assertion.ArtifactDirectory,
		assertion.ArtifactDigest,
	)
	if err != nil {
		t.Fatalf("Channel module-apply: %v", err)
	}
	if applied.Status != moduleApplyStatusApplied ||
		applied.PointerRevision != identityBasis.PointerRevision+1 ||
		applied.BindingTarget.Kind != moduleApplyBindingTargetWorkspaceChannelEndpointV1 ||
		applied.BindingTarget.WorkspaceID != defaultWorkspaceID ||
		applied.BindingTarget.EndpointID != "endpoint-apply" {
		t.Fatalf("Channel module-apply=%+v", applied)
	}
	assertModuleApplyChannelCurrentClosureV1(t, databasePath, assertion.ArtifactDigest)

	// The exact retry observes both the immutable publication and revision-zero
	// seed. It must not create another pointer, activation, Cursor or receipt.
	retry, err := runW2E2TrustedModuleCommandV1[moduleApplyResultV1](
		ctx,
		runModuleApply,
		databasePath,
		artifactRoot,
		planPath,
		assertion.ArtifactDirectory,
		assertion.ArtifactDigest,
	)
	if err != nil || retry.Status != moduleApplyStatusAlreadyApplied ||
		retry.PointerRevision != applied.PointerRevision {
		t.Fatalf("Channel exact retry=%+v err=%v", retry, err)
	}

	listPayload := runModuleOperatorCommandV1(t, []string{
		"module-list",
		"--db", databasePath,
		"--tenant", defaultTenantID,
		"--workspace", defaultWorkspaceID,
		"--endpoint", "endpoint-apply",
	})
	var listed moduleListResultV1
	decodeModuleOperatorOutputV1(t, listPayload, &listed)
	if listed.BindingTarget == nil ||
		listed.BindingTarget.Kind != moduleApplyBindingTargetWorkspaceChannelEndpointV1 ||
		len(listed.Bindings) != 1 ||
		listed.Bindings[0].BindingTarget != *listed.BindingTarget ||
		listed.Bindings[0].Port != productionChannelPort ||
		listed.Bindings[0].PortBindingIndex != 0 {
		t.Fatalf("Channel module-list=%+v", listed)
	}

	inspectPayload := runModuleOperatorCommandV1(t, []string{
		"module-inspect",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--tenant", defaultTenantID,
		"--instance", "channel-apply",
	})
	var inspected moduleInspectResultV1
	decodeModuleOperatorOutputV1(t, inspectPayload, &inspected)
	if len(inspected.Bindings) != 1 ||
		inspected.Bindings[0].BindingTarget.Kind !=
			moduleApplyBindingTargetWorkspaceChannelEndpointV1 ||
		inspected.Source.ID != localLoopbackChannelModuleID {
		t.Fatalf("Channel module-inspect=%+v", inspected)
	}

	resolver := &countingChannelSecretResolver{}
	composition, err := openProductionChannelComposition(
		ctx,
		databasePath,
		artifactRoot,
		productionChannelEndpointInput{
			TenantID:       defaultTenantID,
			WorkspaceID:    defaultWorkspaceID,
			EndpointID:     "endpoint-apply",
			SecretResolver: resolver,
		},
	)
	if err != nil {
		t.Fatalf("compose applied Channel Endpoint: %v", err)
	}
	if resolver.calls.Load() != 0 {
		t.Fatal("Channel composition resolved a Secret before ingress/egress")
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close Channel composition: %v", err)
	}

	disabledPlan := canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyDisabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": applied.PointerRevision,
		"binding_target": map[string]any{
			"kind":         string(moduleApplyBindingTargetWorkspaceChannelEndpointV1),
			"workspace_id": defaultWorkspaceID,
			"endpoint_id":  "endpoint-apply",
		},
		"instance_id": "channel-apply",
		"port": map[string]any{
			"name": productionChannelPort.Name, "exact_version": productionChannelPort.ExactVersion,
		},
	})
	disabledPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-channel.json"),
		disabledPlan,
	)
	disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disabledPath,
		"",
		"",
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied ||
		disabled.PointerRevision != applied.PointerRevision+1 {
		t.Fatalf("disable Channel=%+v err=%v", disabled, err)
	}
	assertModuleApplyChannelDisabledPreservesSeedV1(t, databasePath)

	historyPayload := runModuleOperatorCommandV1(t, []string{
		"module-history",
		"--db", databasePath,
		"--tenant", defaultTenantID,
		"--control-revision", uintString(applied.PointerRevision),
		"--catalog-generation", uintString(applied.PointerRevision),
		"--workspace", defaultWorkspaceID,
		"--endpoint", "endpoint-apply",
	})
	var history moduleHistoryResultV1
	decodeModuleOperatorOutputV1(t, historyPayload, &history)
	if len(history.Bindings) != 1 ||
		history.Bindings[0].BindingTarget.Kind !=
			moduleApplyBindingTargetWorkspaceChannelEndpointV1 {
		t.Fatalf("historical Channel Binding=%+v", history)
	}
}

func publishModuleApplyChannelIdentityV1(
	t *testing.T,
	databasePath string,
) controlcontract.PublishedBasis {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
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
		control.Workspaces[index].ChannelIdentities = append(
			control.Workspaces[index].ChannelIdentities,
			controlcontract.ChannelIdentityDefinition{
				Channel:        localLoopbackChannelName,
				AccountID:      "account-apply",
				ExternalUserID: "external-user-apply",
				PrincipalID:    defaultPrincipalID,
				ACLEpoch:       1,
				Active:         true,
			},
		)
	}
	if !workspaceFound {
		t.Fatal("default Workspace is absent")
	}
	control.SnapshotID = "module-apply-channel-identity-control"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "module-apply-channel-identity-catalog"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
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
	if err != nil {
		t.Fatal(err)
	}
	return published
}

func assertModuleApplyChannelSeedAbsentV1(t *testing.T, databasePath string) {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.GetChannelCursorSeed(
		context.Background(), defaultTenantID, "endpoint-apply", "cursor/channel-apply",
	); !errors.Is(err, currentstore.ErrChannelIngressNotFound) {
		t.Fatalf("dry-run Channel Cursor seed error=%v", err)
	}
}

func assertModuleApplyChannelCurrentClosureV1(
	t *testing.T,
	databasePath string,
	artifactDigest string,
) {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, control, catalog, err := store.LoadPublishedBasis(context.Background(), defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, found := control.FindWorkspace(defaultWorkspaceID)
	if !found {
		t.Fatal("applied Channel Workspace absent")
	}
	endpoint, found := workspace.FindChannelEndpoint("endpoint-apply")
	if !found || !endpoint.Enabled || endpoint.CursorScopeKey != "cursor/channel-apply" {
		t.Fatalf("applied Channel Endpoint=%+v found=%v", endpoint, found)
	}
	entry, found := catalog.FindInstance("channel-apply")
	if !found || entry.Activation.ArtifactDigest != artifactDigest {
		t.Fatalf("applied Channel Catalog entry=%+v found=%v", entry, found)
	}
	seed, err := store.GetChannelCursorSeed(
		context.Background(), defaultTenantID, "endpoint-apply", endpoint.CursorScopeKey,
	)
	if err != nil || seed.CursorRevision != 0 || seed.Disposition != currentstore.ChannelCursorSeed ||
		seed.Reason != moduleApplyChannelCursorSeedReasonV1 {
		t.Fatalf("applied Channel Cursor seed=%+v err=%v", seed, err)
	}
}

func assertModuleApplyChannelDisabledPreservesSeedV1(t *testing.T, databasePath string) {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, control, catalog, err := store.LoadPublishedBasis(context.Background(), defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, found := control.FindWorkspace(defaultWorkspaceID)
	if !found {
		t.Fatal("disabled Channel Workspace absent")
	}
	if _, found := workspace.FindChannelEndpoint("endpoint-apply"); found {
		t.Fatal("disabled Channel Endpoint remains current")
	}
	if _, found := catalog.FindInstance("channel-apply"); found {
		t.Fatal("last-reference Channel activation remains current")
	}
	seed, err := store.GetChannelCursorSeed(
		context.Background(), defaultTenantID, "endpoint-apply", "cursor/channel-apply",
	)
	if err != nil || seed.CursorRevision != 0 {
		t.Fatalf("disabled Channel lost historical Cursor seed=%+v err=%v", seed, err)
	}
}

func uintString(value uint64) string {
	return strconv.FormatUint(value, 10)
}
