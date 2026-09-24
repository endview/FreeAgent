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

type e5APublicationFixture struct {
	publication           *publicationFixture
	control               controlcontract.ControlSnapshot
	catalog               controlcontract.CatalogGeneration
	knowledgeInstallation ModuleInstallation
}

func TestW2E5APublicationClosureInitialRetryAndPublicVerify(t *testing.T) {
	fixture := newE5APublicationFixture(
		t,
		[]moduleapi.PortRef{e5AModelPort()},
		[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
		nil,
		nil,
		nil,
	)
	input, control, catalog := fixture.freeze(t, "shared-closure")

	basis, err := fixture.publication.store.PublishControlCatalog(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("initial E5-A PublishControlCatalog: %v", err)
	}
	beforeRetry := publicationRowCounts(t, fixture.publication.store)
	retried, err := fixture.publication.store.PublishControlCatalog(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("exact E5-A retry: %v", err)
	}
	if retried != basis {
		t.Fatalf("exact retry basis=%+v want %+v", retried, basis)
	}
	if after := publicationRowCounts(t, fixture.publication.store); after != beforeRetry {
		t.Fatalf("exact retry wrote rows before=%v after=%v", beforeRetry, after)
	}
	if err := VerifyPublishedControlCatalogClosureV1(
		context.Background(),
		fixture.publication.store.db,
		control,
		catalog,
	); err != nil {
		t.Fatalf("VerifyPublishedControlCatalogClosureV1: %v", err)
	}
}

func TestW2E5APublicationRejectsRequiresAndPermissionFailuresWithoutWrites(
	t *testing.T,
) {
	tests := []struct {
		name       string
		build      func(*testing.T) *e5APublicationFixture
		mutate     func(*testing.T, *e5APublicationFixture)
		wantDetail string
	}{
		{
			name: "permission without exact Require",
			build: func(t *testing.T) *e5APublicationFixture {
				return newE5APublicationFixture(
					t,
					nil,
					[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
					nil,
					nil,
					nil,
				)
			},
			wantDetail: "must be exactly legacy permissionless or governed",
		},
		{
			name: "Require without exact permission",
			build: func(t *testing.T) *e5APublicationFixture {
				return newE5APublicationFixture(
					t,
					[]moduleapi.PortRef{e5AModelPort()},
					nil,
					nil,
					nil,
					nil,
				)
			},
			wantDetail: "must be exactly legacy permissionless or governed",
		},
		{
			name: "missing exact Require",
			build: func(t *testing.T) *e5APublicationFixture {
				return newE5APublicationFixture(
					t,
					[]moduleapi.PortRef{e5AActionPort()},
					[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
					nil,
					nil,
					nil,
				)
			},
			wantDetail: "missing exact Require",
		},
		{
			name: "ambiguous same Profile Require",
			build: func(t *testing.T) *e5APublicationFixture {
				return newE5APublicationFixture(
					t,
					[]moduleapi.PortRef{e5AModelPort()},
					[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
					nil,
					nil,
					nil,
				)
			},
			mutate: func(t *testing.T, fixture *e5APublicationFixture) {
				modelBinding := fixture.control.Profiles[0].Bindings[0]
				changed := fixture.publication.modelConfig
				changed.Parameters = []byte(`{"temperature":1}`)
				_, canonical, err := moduleapi.NewModelBindingConfigV2(changed)
				if err != nil {
					t.Fatal(err)
				}
				modelBinding.ConfigRef = putPublicationJSON(
					t,
					fixture.publication.store,
					ContentConfig,
					canonical,
				)
				fixture.control.Profiles[0].Bindings = append(
					fixture.control.Profiles[0].Bindings,
					modelBinding,
				)
			},
			wantDetail: "ambiguous exact Require",
		},
		{
			name: "cross Profile Require",
			build: func(t *testing.T) *e5APublicationFixture {
				return newE5APublicationFixture(
					t,
					[]moduleapi.PortRef{e5AModelPort()},
					[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
					nil,
					nil,
					nil,
				)
			},
			mutate: func(_ *testing.T, fixture *e5APublicationFixture) {
				profile := fixture.control.Profiles[0]
				modelBinding := profile.Bindings[0]
				profile.Bindings = profile.Bindings[1:]
				fixture.control.Profiles[0] = profile
				modelProfile := profile
				modelProfile.Profile = corecontract.ProfileRef{
					ID:      "profile-model-only",
					Version: "v1",
					Digest:  strings.Repeat("4", 64),
				}
				modelProfile.Bindings = []controlcontract.BindingSpec{modelBinding}
				fixture.control.Profiles = append(
					fixture.control.Profiles,
					modelProfile,
				)
			},
			wantDetail: "cross-Profile Require",
		},
		{
			name: "dependency cycle",
			build: func(t *testing.T) *e5APublicationFixture {
				return newE5APublicationFixture(
					t,
					[]moduleapi.PortRef{e5AModelPort()},
					[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
					[]moduleapi.PortRef{e5AContextPort()},
					nil,
					nil,
				)
			},
			wantDetail: "dependency cycle",
		},
		{
			name: "unknown permission",
			build: func(t *testing.T) *e5APublicationFixture {
				return newE5APublicationFixture(
					t,
					[]moduleapi.PortRef{e5AModelPort()},
					[]moduleapi.Permission{"network.http"},
					nil,
					nil,
					nil,
				)
			},
			wantDetail: "unsupported requested permission",
		},
		{
			name: "knowledge permission on non Knowledge Binding",
			build: func(t *testing.T) *e5APublicationFixture {
				return newE5APublicationFixture(
					t,
					[]moduleapi.PortRef{e5AModelPort()},
					[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
					nil,
					[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
					nil,
				)
			},
			wantDetail: "knowledge.read requires a Knowledge context.provide/v1 Binding",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := test.build(t)
			if test.mutate != nil {
				test.mutate(t, fixture)
			}
			input, _, _ := fixture.freeze(t, strings.ReplaceAll(test.name, " ", "-"))
			before := publicationRowCounts(t, fixture.publication.store)
			_, firstErr := fixture.publication.store.PublishControlCatalog(
				context.Background(),
				input,
			)
			if !errors.Is(firstErr, ErrPublicationConflict) ||
				!strings.Contains(firstErr.Error(), test.wantDetail) {
				t.Fatalf("publication error=%v, want conflict containing %q", firstErr, test.wantDetail)
			}
			_, secondErr := fixture.publication.store.PublishControlCatalog(
				context.Background(),
				input,
			)
			if secondErr == nil || secondErr.Error() != firstErr.Error() {
				t.Fatalf("deterministic retry errors=(%v,%v)", firstErr, secondErr)
			}
			if after := publicationRowCounts(t, fixture.publication.store); after != before {
				t.Fatalf("invalid publication wrote rows before=%v after=%v", before, after)
			}
		})
	}
}

func TestW2E5AKnowledgeReadGrantRequiresControlContainedScope(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*moduleapi.KnowledgeScopeRuleV1)
		wantDetail string
	}{
		{
			name: "tenant",
			mutate: func(rule *moduleapi.KnowledgeScopeRuleV1) {
				rule.TenantID = "tenant-outside"
			},
			wantDetail: "Tenant scope is outside Control",
		},
		{
			name: "workspace",
			mutate: func(rule *moduleapi.KnowledgeScopeRuleV1) {
				rule.WorkspaceID = "workspace-outside"
			},
			wantDetail: "Workspace scope is outside Control",
		},
		{
			name: "agent",
			mutate: func(rule *moduleapi.KnowledgeScopeRuleV1) {
				rule.AgentID = "agent-outside"
			},
			wantDetail: "Agent scope is outside Control",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newE5APublicationFixture(
				t,
				[]moduleapi.PortRef{e5AModelPort()},
				[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
				nil,
				nil,
				test.mutate,
			)
			input, _, _ := fixture.freeze(t, "scope-"+test.name)
			before := publicationRowCounts(t, fixture.publication.store)
			_, err := fixture.publication.store.PublishControlCatalog(
				context.Background(),
				input,
			)
			if !errors.Is(err, ErrPublicationConflict) ||
				!strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf("scope publication error=%v, want %q", err, test.wantDetail)
			}
			if after := publicationRowCounts(t, fixture.publication.store); after != before {
				t.Fatalf("invalid scope wrote rows before=%v after=%v", before, after)
			}
		})
	}
}

func TestW2E5AExactRetryAndPublicVerifyRecomputeManifestPermission(t *testing.T) {
	fixture := newE5APublicationFixture(
		t,
		[]moduleapi.PortRef{e5AModelPort()},
		[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
		nil,
		nil,
		nil,
	)
	input, control, catalog := fixture.freeze(t, "semantic-replay")
	if _, err := fixture.publication.store.PublishControlCatalog(
		context.Background(),
		input,
	); err != nil {
		t.Fatalf("initial publication: %v", err)
	}

	badManifest := e5AManifest(
		t,
		fixture.knowledgeInstallation.ModuleID,
		fixture.knowledgeInstallation.ExactVersion,
		e5AContextPort(),
		[]moduleapi.PortRef{e5AModelPort()},
		[]moduleapi.Permission{"network.http"},
	)
	badManifestRef := putPublicationJSON(
		t,
		fixture.publication.store,
		ContentModuleManifest,
		badManifest,
	)
	if _, err := fixture.publication.store.db.Exec(`
		UPDATE module_installations
		SET manifest_ref=?
		WHERE installation_id=?
	`, badManifestRef, fixture.knowledgeInstallation.InstallationID); err != nil {
		t.Fatalf("replace semantic test manifest: %v", err)
	}
	before := publicationRowCounts(t, fixture.publication.store)
	if _, err := fixture.publication.store.PublishControlCatalog(
		context.Background(),
		input,
	); !errors.Is(err, ErrPublicationConflict) ||
		!strings.Contains(err.Error(), "unsupported requested permission") {
		t.Fatalf("exact retry semantic replay error=%v", err)
	}
	if err := VerifyPublishedControlCatalogClosureV1(
		context.Background(),
		fixture.publication.store.db,
		control,
		catalog,
	); !errors.Is(err, ErrPublicationConflict) ||
		!strings.Contains(err.Error(), "unsupported requested permission") {
		t.Fatalf("public semantic replay error=%v", err)
	}
	if after := publicationRowCounts(t, fixture.publication.store); after != before {
		t.Fatalf("semantic replay wrote rows before=%v after=%v", before, after)
	}
}

func TestW2E5AExactRetryAndPublicVerifyRejectPartialKnowledgeManifestShapes(
	t *testing.T,
) {
	tests := []struct {
		name        string
		requires    []moduleapi.PortRef
		permissions []moduleapi.Permission
	}{
		{
			name:        "permission only",
			permissions: []moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
		},
		{
			name:     "require only",
			requires: []moduleapi.PortRef{e5AModelPort()},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newE5APublicationFixture(
				t,
				[]moduleapi.PortRef{e5AModelPort()},
				[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
				nil,
				nil,
				nil,
			)
			input, control, catalog := fixture.freeze(t, "partial-shape")
			if _, err := fixture.publication.store.PublishControlCatalog(
				context.Background(),
				input,
			); err != nil {
				t.Fatalf("initial publication: %v", err)
			}

			partial := e5AManifest(
				t,
				fixture.knowledgeInstallation.ModuleID,
				fixture.knowledgeInstallation.ExactVersion,
				e5AContextPort(),
				test.requires,
				test.permissions,
			)
			partialRef := putPublicationJSON(
				t,
				fixture.publication.store,
				ContentModuleManifest,
				partial,
			)
			if _, err := fixture.publication.store.db.Exec(`
				UPDATE module_installations
				SET manifest_ref=?
				WHERE installation_id=?
			`, partialRef, fixture.knowledgeInstallation.InstallationID); err != nil {
				t.Fatalf("replace partial Knowledge Manifest: %v", err)
			}
			before := publicationRowCounts(t, fixture.publication.store)
			if _, err := fixture.publication.store.PublishControlCatalog(
				context.Background(),
				input,
			); !errors.Is(err, ErrPublicationConflict) ||
				!strings.Contains(err.Error(), "must be exactly legacy permissionless or governed") {
				t.Fatalf("exact retry partial shape error=%v", err)
			}
			if err := VerifyPublishedControlCatalogClosureV1(
				context.Background(),
				fixture.publication.store.db,
				control,
				catalog,
			); !errors.Is(err, ErrPublicationConflict) ||
				!strings.Contains(err.Error(), "must be exactly legacy permissionless or governed") {
				t.Fatalf("public Verify partial shape error=%v", err)
			}
			if after := publicationRowCounts(t, fixture.publication.store); after != before {
				t.Fatalf("partial shape verification wrote rows before=%v after=%v", before, after)
			}
		})
	}
}

func TestW2E5AUnboundCatalogActivationDoesNotRequestGrant(t *testing.T) {
	fixture := newE5APublicationFixture(
		t,
		[]moduleapi.PortRef{e5AModelPort()},
		[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
		nil,
		nil,
		nil,
	)
	manifest := e5AManifest(
		t,
		"test.unbound.e5a",
		"v1",
		e5AActionPort(),
		[]moduleapi.PortRef{e5AContextPort()},
		[]moduleapi.Permission{"network.http"},
	)
	provider, _ := installActivateE5A(
		t,
		fixture.publication.store,
		fixture.publication.tenantID,
		"unbound-e5a",
		manifest,
		strings.Repeat("d", 64),
	)
	fixture.catalog.Entries = append(
		fixture.catalog.Entries,
		controlcontract.CatalogEntry{
			Activation: provider,
			Provides:   []moduleapi.PortRef{e5AActionPort()},
		},
	)
	input, _, _ := fixture.freeze(t, "unbound")
	if _, err := fixture.publication.store.PublishControlCatalog(
		context.Background(),
		input,
	); err != nil {
		t.Fatalf("unbound Catalog activation affected PROFILE graph: %v", err)
	}
}

func TestW2E5ALegacyPermissionlessManifestStillPublishes(t *testing.T) {
	fixture := newPublicationFixture(t)
	input := fixture.input(t, "control-e5a-legacy", 1, "catalog-e5a-legacy", 1, 0, 1, nil)
	if _, err := fixture.store.PublishControlCatalog(context.Background(), input); err != nil {
		t.Fatalf("legacy permissionless publication: %v", err)
	}
}

func newE5APublicationFixture(
	t *testing.T,
	knowledgeRequires []moduleapi.PortRef,
	knowledgePermissions []moduleapi.Permission,
	modelRequires []moduleapi.PortRef,
	modelPermissions []moduleapi.Permission,
	mutateRule func(*moduleapi.KnowledgeScopeRuleV1),
) *e5APublicationFixture {
	t.Helper()
	publication := newPublicationFixture(t)

	if len(modelRequires) != 0 || len(modelPermissions) != 0 {
		manifest := e5AManifest(
			t,
			"test.model.e5a",
			"v1",
			e5AModelPort(),
			modelRequires,
			modelPermissions,
		)
		provider, _ := installActivateE5A(
			t,
			publication.store,
			publication.tenantID,
			"model-e5a",
			manifest,
			strings.Repeat("7", 64),
		)
		publication.activation = provider
	}

	agent := corecontract.AgentRef{
		ID:      "agent-e5a",
		Version: "v1",
		Digest:  strings.Repeat("2", 64),
	}
	workspace := corecontract.WorkspaceRef{
		ID:      "workspace-e5a",
		Version: "v1",
		Digest:  strings.Repeat("3", 64),
	}
	source := moduleapi.KnowledgeSourceRefV1{
		ID:      "shared.docs.e5a",
		Version: "1.1.0",
		Digest:  strings.Repeat("9", 64),
	}
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            source,
			MaxHits:           4,
			MaxTotalTextBytes: 4096,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, configCanonical, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementUntrustedData,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    parameters,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configRef := putPublicationJSON(
		t,
		publication.store,
		ContentConfig,
		configCanonical,
	)
	rule := moduleapi.KnowledgeScopeRuleV1{
		TenantID:     publication.tenantID,
		WorkspaceID:  workspace.ID,
		AgentID:      agent.ID,
		TaskInputRef: "*",
	}
	if mutateRule != nil {
		mutateRule(&rule)
	}
	_, authorityCanonical, err := moduleapi.NewKnowledgeAuthorityCeilingV1(
		moduleapi.KnowledgeAuthorityCeilingV1{
			SchemaVersion:     moduleapi.KnowledgeAuthorityCeilingSchemaV1,
			Source:            source,
			AllowedScopes:     []moduleapi.KnowledgeScopeRuleV1{rule},
			MaxHits:           2,
			MaxTotalTextBytes: 2048,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorityRef := putPublicationJSON(
		t,
		publication.store,
		ContentAuthorityCeiling,
		authorityCanonical,
	)

	knowledgeManifest := e5AManifest(
		t,
		"test.knowledge.e5a",
		"1.1.0",
		e5AContextPort(),
		knowledgeRequires,
		knowledgePermissions,
	)
	knowledgeProvider, knowledgeInstallation := installActivateE5A(
		t,
		publication.store,
		publication.tenantID,
		"knowledge-e5a",
		knowledgeManifest,
		strings.Repeat("8", 64),
	)

	control := controlcontract.ControlSnapshot{
		SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV2,
		SnapshotID:    "control-e5a",
		TenantID:      publication.tenantID,
		Revision:      1,
		Agents:        []corecontract.AgentRef{agent},
		Workspaces: []controlcontract.WorkspaceDefinition{{
			Workspace: workspace,
		}},
		Profiles: []controlcontract.ProfileDefinition{{
			Profile: corecontract.ProfileRef{
				ID:      "profile-e5a",
				Version: "v1",
				Digest:  strings.Repeat("1", 64),
			},
			ContextPolicy:    publication.context,
			SchedulingPolicy: publication.scheduling,
			Bindings: []controlcontract.BindingSpec{
				{
					Port:                e5AModelPort(),
					InstanceID:          publication.activation.InstanceID,
					ConfigRef:           publication.config,
					AuthorityCeilingRef: publication.authority,
					StaticContextRefs:   []string{},
					FailurePolicy:       moduleapi.FailureRequired,
				},
				{
					Port:                e5AContextPort(),
					InstanceID:          knowledgeProvider.InstanceID,
					ConfigRef:           configRef,
					AuthorityCeilingRef: authorityRef,
					StaticContextRefs:   []string{},
					FailurePolicy:       moduleapi.FailureRequired,
				},
			},
		}},
	}
	catalog := controlcontract.CatalogGeneration{
		SchemaVersion: controlcontract.CatalogGenerationSchemaVersionV1,
		GenerationID:  "catalog-e5a",
		Generation:    1,
		TenantID:      publication.tenantID,
		Entries: []controlcontract.CatalogEntry{
			{
				Activation: publication.activation,
				Provides:   []moduleapi.PortRef{e5AModelPort()},
			},
			{
				Activation: knowledgeProvider,
				Provides:   []moduleapi.PortRef{e5AContextPort()},
			},
		},
	}
	return &e5APublicationFixture{
		publication:           publication,
		control:               control,
		catalog:               catalog,
		knowledgeInstallation: knowledgeInstallation,
	}
}

func (fixture *e5APublicationFixture) freeze(
	t *testing.T,
	suffix string,
) (
	PublishControlCatalogInput,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
) {
	t.Helper()
	control := fixture.control
	control.SnapshotID = "control-e5a-" + suffix
	control.Revision = 1
	control.Digest = ""
	frozenControl, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("NewControlSnapshot E5-A: %v", err)
	}
	catalog := fixture.catalog
	catalog.GenerationID = "catalog-e5a-" + suffix
	catalog.Generation = 1
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	frozenCatalog, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("NewCatalogGeneration E5-A: %v", err)
	}
	return PublishControlCatalogInput{
		ExpectedPointerRevision: 0,
		NewPointerRevision:      1,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	}, frozenControl, frozenCatalog
}

func e5AManifest(
	t *testing.T,
	moduleID string,
	version string,
	provides moduleapi.PortRef,
	requires []moduleapi.PortRef,
	permissions []moduleapi.Permission,
) []byte {
	t.Helper()
	extra := map[string]any{
		"provides": []moduleapi.PortRef{provides},
		"runtime": map[string]any{
			"mode":       string(moduleapi.RuntimeModeRequestTrustedInProcess),
			"protocol":   moduleapi.RuntimeProtocolGoInProcessV1,
			"entrypoint": "content/source.json",
		},
	}
	if len(requires) != 0 {
		extra["requires"] = append([]moduleapi.PortRef(nil), requires...)
	}
	if len(permissions) != 0 {
		extra["requested_permissions"] = append(
			[]moduleapi.Permission(nil),
			permissions...,
		)
	}
	return canonicalModuleManifest(
		t,
		moduleID,
		version,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		extra,
	)
}

func installActivateE5A(
	t *testing.T,
	store *Store,
	tenantID string,
	identity string,
	manifest []byte,
	artifactDigest string,
) (moduleapi.ActivatedModuleRef, ModuleInstallation) {
	t.Helper()
	installation, err := store.InstallModule(
		context.Background(),
		installInput(t, "installation-"+identity, manifest, artifactDigest),
	)
	if err != nil {
		t.Fatalf("InstallModule %s: %v", identity, err)
	}
	activation, err := store.ActivateModule(
		context.Background(),
		ActivateModuleInput{
			ActivationID:       "activation-" + identity,
			TenantID:           tenantID,
			InstanceID:         "instance-" + identity,
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "core." + identity + ".adapter",
		},
	)
	if err != nil {
		t.Fatalf("ActivateModule %s: %v", identity, err)
	}
	return moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}, installation
}

func e5AModelPort() moduleapi.PortRef {
	return moduleapi.PortRef{
		Name:         moduleapi.PortNameModelGenerate,
		ExactVersion: moduleapi.PortVersionV2,
	}
}

func e5AContextPort() moduleapi.PortRef {
	return moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
}

func e5AActionPort() moduleapi.PortRef {
	return moduleapi.PortRef{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}
}
