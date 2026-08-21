package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const moduleApplyDocumentInsightInstanceIDV1 = "document-insight-module-apply"

type moduleApplyDocumentInsightFixtureV1 struct {
	ArtifactDirectory string
	ArtifactDigest    string
	ArtifactSizeBytes uint64
	Source            moduleapi.KnowledgeSourceRefV1
}

func TestW2E5BDocumentInsightExactSelectorPrecedesGenericFallback(t *testing.T) {
	t.Parallel()

	table := moduleApplyExactSelectorProtocolHandlerTableV1()
	if err := validateModuleApplyProtocolHandlerTableV1(table[:]); err != nil {
		t.Fatalf("validate Document Insight handler table: %v", err)
	}
	if table[0].Port != productionContextPort ||
		table[0].ConsumerSchema != moduleapi.KnowledgeContextBindingSchemaV1 ||
		table[1].Port != productionActionPort ||
		table[1].ConsumerSchema != moduleapi.ActionBindingConfigSchemaV1 {
		t.Fatalf("Document Insight exact handlers=%+v", table)
	}
	for index, expected := range table {
		resolved, err := resolveModuleApplyExactSelectorProtocolHandlerV1(
			expected.protocolHandlerKeyV1(),
		)
		if err != nil || !reflect.DeepEqual(resolved, expected) {
			t.Fatalf("resolve exact handler %d=%+v error=%v", index, resolved, err)
		}
	}

	wrong := table[0].protocolHandlerKeyV1()
	wrong.ArtifactDigest = strings.Repeat("0", moduleapi.SHA256HexLength)
	if _, err := resolveModuleApplyExactSelectorProtocolHandlerV1(wrong); !errors.Is(
		err,
		errModuleApplyProtocolHandlerNotFoundV1,
	) {
		t.Fatalf("drifted exact selector error=%v", err)
	}

	// The old generic Knowledge key remains byte-for-byte selectorless and
	// resolves through the original table, not the reserved module family.
	generic := moduleApplyProtocolHandlerKeyV1{
		Port:            productionContextPort,
		RuntimeMode:     moduleapi.RuntimeModeRequestTrustedInProcess,
		RuntimeProtocol: moduleapi.RuntimeProtocolGoInProcessV1,
		ConsumerSchema:  moduleapi.KnowledgeContextBindingSchemaV1,
	}
	resolved, err := resolveModuleApplyProtocolHandlerV1(generic)
	if err != nil || resolved.HandlerKind != moduleApplyHandlerKnowledgeContextV1 {
		t.Fatalf("generic Knowledge fallback=%+v error=%v", resolved, err)
	}

	fixture := newModuleApplyDocumentInsightFixtureV1(t)
	plan, _, _, err := restoreModuleApplyPlanV1(
		newEnabledModuleApplyDocumentInsightContextPlanV1(t, fixture, 1, 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	plan.Module.ArtifactDigest = strings.Repeat("0", moduleapi.SHA256HexLength)
	if policy, err := resolveEnabledModuleApplyPolicyV1(plan); err == nil ||
		policy.HandlerKind == moduleApplyHandlerKnowledgeContextV1 {
		t.Fatalf("reserved drift fell through to generic Knowledge: policy=%+v error=%v", policy, err)
	}
}

func TestW2E5BDocumentInsightBothHandlersRequireExactCompleteManifestReport(
	t *testing.T,
) {
	t.Parallel()
	fixture := newModuleApplyDocumentInsightFixtureV1(t)
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectory(
		fixture.ArtifactDirectory,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	plans := []moduleApplyPlanV1{}
	for _, canonical := range [][]byte{
		newEnabledModuleApplyDocumentInsightContextPlanV1(t, fixture, 1, 1),
		newEnabledModuleApplyDocumentInsightActionPlanV1(t, fixture, 1, 0),
	} {
		plan, _, _, err := restoreModuleApplyPlanV1(canonical)
		if err != nil {
			t.Fatal(err)
		}
		plans = append(plans, plan)
	}
	for _, plan := range plans {
		policy, err := resolveEnabledModuleApplyPolicyV1(plan)
		if err != nil {
			t.Fatal(err)
		}
		validate := func(
			entrypoint string,
			provides []moduleapi.PortRef,
			requires []moduleapi.PortRef,
			permissions []moduleapi.Permission,
		) error {
			_, err := validateModuleApplyVerificationReportV1(
				plan,
				policy,
				manifest.ID,
				manifest.Version,
				fixture.ArtifactDigest,
				fixture.ArtifactSizeBytes,
				string(manifest.Runtime.Mode),
				manifest.Runtime.Protocol,
				entrypoint,
				provides,
				requires,
				permissions,
			)
			return err
		}
		if err := validate(
			manifest.Runtime.Entrypoint,
			manifest.Provides,
			manifest.Requires,
			manifest.RequestedPermissions,
		); err != nil {
			t.Fatalf("exact %s report rejected: %v", plan.Port.Name, err)
		}
		negative := []struct {
			name        string
			entrypoint  string
			provides    []moduleapi.PortRef
			requires    []moduleapi.PortRef
			permissions []moduleapi.Permission
		}{
			{
				name:        "partial Provides",
				entrypoint:  manifest.Runtime.Entrypoint,
				provides:    manifest.Provides[:1],
				requires:    manifest.Requires,
				permissions: manifest.RequestedPermissions,
			},
			{
				name:        "missing Require",
				entrypoint:  manifest.Runtime.Entrypoint,
				provides:    manifest.Provides,
				requires:    []moduleapi.PortRef{},
				permissions: manifest.RequestedPermissions,
			},
			{
				name:        "missing permission",
				entrypoint:  manifest.Runtime.Entrypoint,
				provides:    manifest.Provides,
				requires:    manifest.Requires,
				permissions: []moduleapi.Permission{},
			},
			{
				name:        "drifted entrypoint",
				entrypoint:  "content/other.json",
				provides:    manifest.Provides,
				requires:    manifest.Requires,
				permissions: manifest.RequestedPermissions,
			},
		}
		for _, test := range negative {
			if err := validate(
				test.entrypoint,
				test.provides,
				test.requires,
				test.permissions,
			); err == nil {
				t.Fatalf("%s handler accepted %s", plan.Port.Name, test.name)
			}
		}
	}
}

func TestW2E5BDocumentInsightActionFirstFailsBeforeStageTempOrStoreWrites(
	t *testing.T,
) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDocumentInsightFixtureV1(t)
	input := newModuleApplyDocumentInsightInputV1(
		t,
		databasePath,
		artifactRoot,
		fixture,
		newEnabledModuleApplyDocumentInsightActionPlanV1(t, fixture, 1, 0),
	)
	before := observeModuleApplyStateV1(t, databasePath, artifactRoot)

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	stageCalled := false
	_, applyErr := prepareAndStageEnabledModuleV1(
		ctx,
		store,
		artifactRoot,
		moduleApplyStageFilesystemV1{
			mkdirTemp: func(string, string) (string, error) {
				stageCalled = true
				return "", errors.New("stage must not be reached")
			},
			removeAll: func(string) error {
				t.Fatal("stage cleanup was reached")
				return nil
			},
			syncDirectory: func(string) error {
				t.Fatal("stage sync was reached")
				return nil
			},
		},
		input,
		basis,
		control,
		catalog,
	)
	if closeErr := store.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if moduleApplyFailureCodeOfV1(applyErr) != moduleApplyFailureTarget || stageCalled {
		t.Fatalf("Action-first Apply error=%v stageCalled=%v", applyErr, stageCalled)
	}

	observer, err := currentstore.OpenReadOnlyObserver(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	tempCalled := false
	_, dryRunErr := prepareEnabledModuleDryRunWithFilesystemV1(
		ctx,
		observer,
		artifactRoot,
		input,
		basis,
		control,
		catalog,
		moduleDryRunTempFilesystemV1{
			mkdirTemp: func(string, string) (string, error) {
				tempCalled = true
				return "", errors.New("TEMP must not be reached")
			},
			removeAll: func(string) error {
				t.Fatal("TEMP cleanup was reached")
				return nil
			},
			lstat: func(string) (os.FileInfo, error) {
				t.Fatal("TEMP lstat was reached")
				return nil, os.ErrNotExist
			},
		},
	)
	if closeErr := observer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if moduleApplyFailureCodeOfV1(dryRunErr) != moduleApplyFailureTarget || tempCalled {
		t.Fatalf("Action-first Dry-run error=%v tempCalled=%v", dryRunErr, tempCalled)
	}
	if _, err := applyModulePlanV1(ctx, input); moduleApplyFailureCodeOfV1(err) !=
		moduleApplyFailureTarget {
		t.Fatalf("top-level Action-first Apply error=%v", err)
	}
	if _, err := dryRunModulePlanV1(ctx, input); moduleApplyFailureCodeOfV1(err) !=
		moduleApplyFailureTarget {
		t.Fatalf("top-level Action-first Dry-run error=%v", err)
	}
	after := observeModuleApplyStateV1(t, databasePath, artifactRoot)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("Action-first rejection mutated state:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func TestW2E5BDocumentInsightContextThenActionReusesOneModuleIdentity(
	t *testing.T,
) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDocumentInsightFixtureV1(t)

	contextInput := newModuleApplyDocumentInsightInputV1(
		t,
		databasePath,
		artifactRoot,
		fixture,
		newEnabledModuleApplyDocumentInsightContextPlanV1(t, fixture, 1, 1),
	)
	contextResult, err := applyModulePlanV1(ctx, contextInput)
	if err != nil || contextResult.Status != moduleApplyStatusApplied ||
		contextResult.PointerRevision != 2 {
		t.Fatalf("apply Document Insight Context=%+v error=%v", contextResult, err)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, _, beforeCatalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	beforeEntry, found := beforeCatalog.FindInstance(moduleApplyDocumentInsightInstanceIDV1)
	if !found {
		_ = store.Close()
		t.Fatal("Document Insight Catalog entry is absent after Context")
	}
	beforeInstallation, err := store.GetModuleInstallationByIdentity(
		ctx,
		localDocumentInsightModuleID,
		localDocumentInsightVersion,
	)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if closeErr := store.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	actionInput := newModuleApplyDocumentInsightInputV1(
		t,
		databasePath,
		artifactRoot,
		fixture,
		newEnabledModuleApplyDocumentInsightActionPlanV1(t, fixture, 2, 0),
	)
	dryRun, err := dryRunModulePlanV1(ctx, actionInput)
	if err != nil || dryRun.Status != moduleApplyStatusWouldApply ||
		dryRun.Changes.Installation != moduleDryRunInstallationReuseV1 ||
		dryRun.Changes.Activation != moduleDryRunActivationReuseCurrentV1 ||
		dryRun.Changes.Catalog != moduleDryRunCatalogRetainInstanceV1 {
		t.Fatalf("Document Insight Action Dry-run=%+v error=%v", dryRun, err)
	}
	actionResult, err := applyModulePlanV1(ctx, actionInput)
	if err != nil || actionResult.Status != moduleApplyStatusApplied ||
		actionResult.PointerRevision != 3 {
		t.Fatalf("apply Document Insight Action=%+v error=%v", actionResult, err)
	}
	retry, err := applyModulePlanV1(ctx, actionInput)
	if err != nil || retry.Status != moduleApplyStatusAlreadyApplied ||
		retry.PointerRevision != 3 {
		t.Fatalf("retry Document Insight Action=%+v error=%v", retry, err)
	}
	noChangeInput := newModuleApplyDocumentInsightInputV1(
		t,
		databasePath,
		artifactRoot,
		fixture,
		newEnabledModuleApplyDocumentInsightActionPlanV1(t, fixture, 3, 0),
	)
	noChange, err := applyModulePlanV1(ctx, noChangeInput)
	if err != nil || noChange.Status != moduleApplyStatusNoChange ||
		noChange.PointerRevision != 3 {
		t.Fatalf("no-change Document Insight Action=%+v error=%v", noChange, err)
	}

	store, err = currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, afterCatalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	afterEntry, found := afterCatalog.FindInstance(moduleApplyDocumentInsightInstanceIDV1)
	if !found || afterEntry.Activation != beforeEntry.Activation ||
		!reflect.DeepEqual(afterEntry.Provides, moduleapi.ExactDocumentInsightProvidesV1()) {
		_ = store.Close()
		t.Fatalf("Document Insight Catalog was not retained exactly: before=%+v after=%+v", beforeEntry, afterEntry)
	}
	afterInstallation, err := store.GetModuleInstallationByIdentity(
		ctx,
		localDocumentInsightModuleID,
		localDocumentInsightVersion,
	)
	if err != nil || afterInstallation.InstallationID != beforeInstallation.InstallationID {
		_ = store.Close()
		t.Fatalf("Document Insight Installation was not reused: before=%+v after=%+v error=%v", beforeInstallation, afterInstallation, err)
	}
	latest, err := store.GetLatestModuleActivationForInstance(
		ctx,
		defaultTenantID,
		moduleApplyDocumentInsightInstanceIDV1,
	)
	if err != nil || latest.ActivationRevision != beforeEntry.Activation.ActivationRevision {
		_ = store.Close()
		t.Fatalf("Document Insight Activation was not reused: latest=%+v error=%v", latest, err)
	}
	if closeErr := store.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	profile, found := control.FindProfile(moduleApplyTestProfileID)
	if basis.PointerRevision != 3 || !found {
		t.Fatalf("Document Insight final basis=%+v Profile found=%v", basis, found)
	}
	ports := make(map[moduleapi.PortRef]int)
	for _, binding := range profile.Bindings {
		if binding.InstanceID == moduleApplyDocumentInsightInstanceIDV1 {
			ports[binding.Port]++
		}
	}
	if ports[productionContextPort] != 1 || ports[productionActionPort] != 1 ||
		len(ports) != 2 {
		t.Fatalf("Document Insight Profile bindings=%v", ports)
	}
}

func newModuleApplyDocumentInsightFixtureV1(
	t *testing.T,
) moduleApplyDocumentInsightFixtureV1 {
	t.Helper()
	directory := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"bootstrap-artifacts",
		localDocumentInsightModuleID,
		localDocumentInsightVersion,
	)
	manifest, sourceCanonical, sourceRef, err :=
		readModuleApplyDocumentInsightMetadataV1(context.Background(), directory)
	if err != nil {
		t.Fatalf("read Document Insight artifact: %v", err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		directory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifest, files)
	if err != nil {
		t.Fatal(err)
	}
	size := uint64(len(manifest))
	for _, file := range files {
		size += uint64(len(file.Content))
	}
	if digest != localDocumentInsightDigest || size != 1097 ||
		len(sourceCanonical) == 0 {
		t.Fatalf("Document Insight fixture identity digest=%s size=%d", digest, size)
	}
	return moduleApplyDocumentInsightFixtureV1{
		ArtifactDirectory: directory,
		ArtifactDigest:    digest,
		ArtifactSizeBytes: size,
		Source:            sourceRef,
	}
}

func newModuleApplyDocumentInsightInputV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	fixture moduleApplyDocumentInsightFixtureV1,
	planCanonical []byte,
) moduleApplyCommandInputV1 {
	t.Helper()
	plan, canonical, digest, err := restoreModuleApplyPlanV1(planCanonical)
	if err != nil {
		t.Fatal(err)
	}
	return moduleApplyCommandInputV1{
		DatabasePath:                  databasePath,
		ArtifactRoot:                  artifactRoot,
		ArtifactDirectory:             fixture.ArtifactDirectory,
		TrustedInProcessArtifactGrant: fixture.ArtifactDigest,
		Plan:                          plan,
		PlanCanonical:                 canonical,
		PlanDigest:                    digest,
	}
}

func newEnabledModuleApplyDocumentInsightContextPlanV1(
	t *testing.T,
	fixture moduleApplyDocumentInsightFixtureV1,
	expectedPointer uint64,
	portIndex uint32,
) []byte {
	t.Helper()
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            fixture.Source,
			MaxHits:           4,
			MaxTotalTextBytes: 4096,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, config, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementUntrustedData,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    json.RawMessage(parameters),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewKnowledgeAuthorityCeilingV1(
		moduleapi.KnowledgeAuthorityCeilingV1{
			SchemaVersion: moduleapi.KnowledgeAuthorityCeilingSchemaV1,
			Source:        fixture.Source,
			AllowedScopes: []moduleapi.KnowledgeScopeRuleV1{{
				TenantID:     defaultTenantID,
				WorkspaceID:  defaultWorkspaceID,
				AgentID:      defaultAgentID,
				TaskInputRef: "*",
			}},
			MaxHits:           4,
			MaxTotalTextBytes: 4096,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return newEnabledModuleApplyDocumentInsightPlanV1(
		t,
		fixture,
		expectedPointer,
		productionContextPort,
		portIndex,
		config,
		authority,
	)
}

func newEnabledModuleApplyDocumentInsightActionPlanV1(
	t *testing.T,
	fixture moduleApplyDocumentInsightFixtureV1,
	expectedPointer uint64,
	portIndex uint32,
) []byte {
	t.Helper()
	_, config, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   exactadapter.TextStatsActionIDV1,
				ProviderActionID: exactadapter.TextStatsActionIDV1,
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   exactadapter.TextStatsMaxResultBytesV1,
			}},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 defaultTenantID,
			AllowedWorkspaceIDs:      []string{defaultWorkspaceID},
			AllowedProviderActionIDs: []string{exactadapter.TextStatsActionIDV1},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           exactadapter.TextStatsMaxResultBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return newEnabledModuleApplyDocumentInsightPlanV1(
		t,
		fixture,
		expectedPointer,
		productionActionPort,
		portIndex,
		config,
		authority,
	)
}

func newEnabledModuleApplyDocumentInsightPlanV1(
	t *testing.T,
	fixture moduleApplyDocumentInsightFixtureV1,
	expectedPointer uint64,
	port moduleapi.PortRef,
	portIndex uint32,
	config []byte,
	authority []byte,
) []byte {
	t.Helper()
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target": moduleApplyProfileBindingTargetTestValue(
			moduleApplyTestProfileID,
		),
		"instance_id": moduleApplyDocumentInsightInstanceIDV1,
		"port": map[string]any{
			"name":          port.Name,
			"exact_version": port.ExactVersion,
		},
		"module": map[string]any{
			"id":                  localDocumentInsightModuleID,
			"exact_version":       localDocumentInsightVersion,
			"artifact_digest":     fixture.ArtifactDigest,
			"artifact_size_bytes": fixture.ArtifactSizeBytes,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestTrustedInProcess),
				"protocol": moduleapi.RuntimeProtocolGoInProcessV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": portIndex,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
	})
}
