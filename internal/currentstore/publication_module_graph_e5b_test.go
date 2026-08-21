package currentstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type e5BDocumentInsightFixture struct {
	base           *e5APublicationFixture
	modelBinding   controlcontract.BindingSpec
	contextBinding controlcontract.BindingSpec
	actionBinding  controlcontract.BindingSpec
	provider       moduleapi.ActivatedModuleRef
}

func TestW2E5BDocumentInsightInstancePermissionAcceptedAssemblies(
	t *testing.T,
) {
	tests := []struct {
		name        string
		bindings    func(*e5BDocumentInsightFixture) []controlcontract.BindingSpec
		wantActions bool
	}{
		{
			name: "dual Manifest with only Context bound",
			bindings: func(fixture *e5BDocumentInsightFixture) []controlcontract.BindingSpec {
				return []controlcontract.BindingSpec{
					fixture.modelBinding,
					fixture.contextBinding,
				}
			},
		},
		{
			name: "Action-first dual binding validates permission once",
			bindings: func(fixture *e5BDocumentInsightFixture) []controlcontract.BindingSpec {
				return []controlcontract.BindingSpec{
					fixture.modelBinding,
					fixture.actionBinding,
					fixture.contextBinding,
				}
			},
			wantActions: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newE5BDocumentInsightFixture(t)
			fixture.base.control.Profiles[0].Bindings = test.bindings(fixture)
			input, control, catalog := fixture.freeze(
				t,
				"accepted-"+strings.ReplaceAll(test.name, " ", "-"),
				1,
				1,
				0,
				1,
			)
			basis, err := fixture.base.publication.store.PublishControlCatalog(
				context.Background(),
				input,
			)
			if err != nil {
				t.Fatalf("initial publication: %v", err)
			}
			beforeRetry := publicationRowCounts(
				t,
				fixture.base.publication.store,
			)
			retried, err := fixture.base.publication.store.PublishControlCatalog(
				context.Background(),
				input,
			)
			if err != nil {
				t.Fatalf("exact retry: %v", err)
			}
			if retried != basis {
				t.Fatalf("exact retry basis=%+v want %+v", retried, basis)
			}
			if after := publicationRowCounts(
				t,
				fixture.base.publication.store,
			); after != beforeRetry {
				t.Fatalf("exact retry wrote rows before=%v after=%v", beforeRetry, after)
			}
			if err := VerifyPublishedControlCatalogClosureV1(
				context.Background(),
				fixture.base.publication.store.db,
				control,
				catalog,
			); err != nil {
				t.Fatalf("public Verify: %v", err)
			}
			if test.wantActions &&
				fixture.base.control.Profiles[0].Bindings[1].Port != e5AActionPort() {
				t.Fatal("acceptance fixture did not exercise Action-first order")
			}
		})
	}
}

func TestW2E5BDocumentInsightInstancePermissionRejectsInvalidAssembliesWithoutWrites(
	t *testing.T,
) {
	tests := []struct {
		name       string
		mutate     func(*testing.T, *e5BDocumentInsightFixture)
		wantDetail string
	}{
		{
			name: "Action only",
			mutate: func(_ *testing.T, fixture *e5BDocumentInsightFixture) {
				fixture.base.control.Profiles[0].Bindings = []controlcontract.BindingSpec{
					fixture.modelBinding,
					fixture.actionBinding,
				}
			},
			wantDetail: "same-instance Binding must exist in the same Profile, found 0",
		},
		{
			name: "Context and Action split across Profiles",
			mutate: func(_ *testing.T, fixture *e5BDocumentInsightFixture) {
				template := fixture.base.control.Profiles[0]
				actionProfile := template
				actionProfile.Profile = corecontract.ProfileRef{
					ID:      "profile-e5b-action",
					Version: "v1",
					Digest:  strings.Repeat("4", 64),
				}
				actionProfile.Bindings = []controlcontract.BindingSpec{
					fixture.modelBinding,
					fixture.actionBinding,
				}
				contextProfile := template
				contextProfile.Profile = corecontract.ProfileRef{
					ID:      "profile-e5b-context",
					Version: "v1",
					Digest:  strings.Repeat("5", 64),
				}
				contextProfile.Bindings = []controlcontract.BindingSpec{
					fixture.modelBinding,
					fixture.contextBinding,
				}
				fixture.base.control.Profiles = []controlcontract.ProfileDefinition{
					actionProfile,
					contextProfile,
				}
			},
			wantDetail: "same-instance Binding must exist in the same Profile, found 0",
		},
		{
			name: "Memory cannot substitute for Knowledge",
			mutate: func(t *testing.T, fixture *e5BDocumentInsightFixture) {
				fixture.contextBinding = e5BMemoryContextBinding(t, fixture)
				fixture.base.control.Profiles[0].Bindings = []controlcontract.BindingSpec{
					fixture.modelBinding,
					fixture.actionBinding,
					fixture.contextBinding,
				}
			},
			wantDetail: "same-instance Binding must exist in the same Profile, found 0",
		},
		{
			name: "multiple same-instance Knowledge Context Bindings",
			mutate: func(t *testing.T, fixture *e5BDocumentInsightFixture) {
				second := fixture.contextBinding
				second.ConfigRef, second.AuthorityCeilingRef =
					e5BKnowledgeBindingMaterials(t, fixture, "second")
				fixture.base.control.Profiles[0].Bindings = []controlcontract.BindingSpec{
					fixture.modelBinding,
					fixture.actionBinding,
					fixture.contextBinding,
					second,
				}
			},
			wantDetail: "same-instance Binding must exist in the same Profile, found 2",
		},
		{
			name: "Knowledge authority outside Control",
			mutate: func(t *testing.T, fixture *e5BDocumentInsightFixture) {
				fixture.contextBinding.AuthorityCeilingRef =
					e5BOutsideTenantKnowledgeAuthority(t, fixture)
				fixture.base.control.Profiles[0].Bindings = []controlcontract.BindingSpec{
					fixture.modelBinding,
					fixture.actionBinding,
					fixture.contextBinding,
				}
			},
			wantDetail: "Tenant scope is outside Control",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newE5BDocumentInsightFixture(t)
			test.mutate(t, fixture)
			input, _, _ := fixture.freeze(
				t,
				"reject-"+strings.ReplaceAll(test.name, " ", "-"),
				1,
				1,
				0,
				1,
			)
			assertE5BPublicationConflictNoWrites(
				t,
				fixture.base.publication.store,
				input,
				test.wantDetail,
			)
		})
	}
}

func TestW2E5BActionOnlyCannotHidePartialDocumentInsightManifest(
	t *testing.T,
) {
	tests := []struct {
		name        string
		requires    []moduleapi.PortRef
		permissions []moduleapi.Permission
		wantDetail  string
	}{
		{
			name:       "missing Require and permission",
			wantDetail: "must require only exact model.generate/v1",
		},
		{
			name:       "missing permission",
			requires:   []moduleapi.PortRef{moduleapi.ExactModelGeneratePortV1()},
			wantDetail: "must request only exact knowledge.read permission",
		},
		{
			name:        "wrong permission",
			requires:    []moduleapi.PortRef{moduleapi.ExactModelGeneratePortV1()},
			permissions: []moduleapi.Permission{"network.http"},
			wantDetail:  "must request only exact knowledge.read permission",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newE5BDocumentInsightFixture(t)
			fixture.base.control.Profiles[0].Bindings = []controlcontract.BindingSpec{
				fixture.modelBinding,
				fixture.actionBinding,
			}
			replaceE5BDocumentInsightManifest(
				t,
				fixture,
				test.requires,
				test.permissions,
			)
			input, _, _ := fixture.freeze(
				t,
				"partial-action-"+strings.ReplaceAll(test.name, " ", "-"),
				1,
				1,
				0,
				1,
			)
			assertE5BPublicationConflictNoWrites(
				t,
				fixture.base.publication.store,
				input,
				test.wantDetail,
			)
		})
	}
}

func TestW2E5BReservedIdentityRejectsCoordinatedActionOnlyDowngrade(
	t *testing.T,
) {
	fixture := newE5BDocumentInsightFixture(t)
	fixture.base.control.Profiles[0].Bindings = []controlcontract.BindingSpec{
		fixture.modelBinding,
		fixture.actionBinding,
	}
	actionOnly := []moduleapi.PortRef{e5AActionPort()}
	replaceE5BDocumentInsightManifestShape(
		t,
		fixture,
		actionOnly,
		nil,
		nil,
	)
	fixture.base.catalog.Entries[1].Provides = append(
		[]moduleapi.PortRef(nil),
		actionOnly...,
	)
	input, control, catalog := fixture.freeze(
		t,
		"coordinated-action-only-downgrade",
		1,
		1,
		0,
		1,
	)
	before := publicationRowCounts(t, fixture.base.publication.store)
	if _, err := fixture.base.publication.store.PublishControlCatalog(
		context.Background(),
		input,
	); !errors.Is(err, ErrPublicationConflict) ||
		!strings.Contains(
			err.Error(),
			"must provide exact ordered action.provider/v1, context.provide/v1",
		) {
		t.Fatalf("coordinated action-only publication error=%v", err)
	}
	if err := VerifyPublishedControlCatalogClosureV1(
		context.Background(),
		fixture.base.publication.store.db,
		control,
		catalog,
	); !errors.Is(err, ErrPublicationConflict) ||
		!strings.Contains(
			err.Error(),
			"must provide exact ordered action.provider/v1, context.provide/v1",
		) {
		t.Fatalf("coordinated action-only public Verify error=%v", err)
	}
	if after := publicationRowCounts(
		t,
		fixture.base.publication.store,
	); after != before {
		t.Fatalf(
			"coordinated action-only rejection wrote rows: before=%v after=%v",
			before,
			after,
		)
	}
}

func TestW2E5BExactRetryAndPublicVerifyReclassifyDocumentInsightManifest(
	t *testing.T,
) {
	fixture := newE5BDocumentInsightFixture(t)
	fixture.bindBothActionFirst()
	input, control, catalog := fixture.freeze(
		t, "semantic-replay", 1, 1, 0, 1,
	)
	if _, err := fixture.base.publication.store.PublishControlCatalog(
		context.Background(),
		input,
	); err != nil {
		t.Fatalf("initial publication: %v", err)
	}
	replaceE5BDocumentInsightManifest(
		t,
		fixture,
		[]moduleapi.PortRef{moduleapi.ExactModelGeneratePortV1()},
		nil,
	)
	before := publicationRowCounts(t, fixture.base.publication.store)
	if _, err := fixture.base.publication.store.PublishControlCatalog(
		context.Background(),
		input,
	); !errors.Is(err, ErrPublicationConflict) ||
		!strings.Contains(err.Error(), "must request only exact knowledge.read permission") {
		t.Fatalf("exact retry partial Manifest error=%v", err)
	}
	if err := VerifyPublishedControlCatalogClosureV1(
		context.Background(),
		fixture.base.publication.store.db,
		control,
		catalog,
	); !errors.Is(err, ErrPublicationConflict) ||
		!strings.Contains(err.Error(), "must request only exact knowledge.read permission") {
		t.Fatalf("public Verify partial Manifest error=%v", err)
	}
	if after := publicationRowCounts(
		t,
		fixture.base.publication.store,
	); after != before {
		t.Fatalf("semantic replay wrote rows before=%v after=%v", before, after)
	}
}

func TestW2E5BOtherKnowledgeContextManifestFamilyIsRejectedWithoutWrites(
	t *testing.T,
) {
	fixture := newE5BDocumentInsightFixture(t)
	otherProvides := []moduleapi.PortRef{
		{
			Name:         moduleapi.PortNameChannelTransport,
			ExactVersion: moduleapi.PortVersionV1,
		},
		moduleapi.ExactContextProvidePortV1(),
	}
	manifest := canonicalModuleManifest(
		t,
		fixture.base.knowledgeInstallation.ModuleID,
		fixture.base.knowledgeInstallation.ExactVersion,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		map[string]any{
			"provides":              otherProvides,
			"requires":              []moduleapi.PortRef{moduleapi.ExactModelGeneratePortV1()},
			"requested_permissions": []moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
			"runtime": map[string]any{
				"mode":       string(moduleapi.RuntimeModeRequestTrustedInProcess),
				"protocol":   moduleapi.RuntimeProtocolGoInProcessV1,
				"entrypoint": "content/source.json",
			},
		},
	)
	manifestRef := putPublicationJSON(
		t,
		fixture.base.publication.store,
		ContentModuleManifest,
		manifest,
	)
	if _, err := fixture.base.publication.store.db.Exec(`
		UPDATE module_installations
		SET manifest_ref=?
		WHERE installation_id=?
	`, manifestRef, fixture.base.knowledgeInstallation.InstallationID); err != nil {
		t.Fatalf("replace other Knowledge Manifest: %v", err)
	}
	fixture.base.catalog.Entries[1].Provides = otherProvides
	fixture.base.control.Profiles[0].Bindings = []controlcontract.BindingSpec{
		fixture.modelBinding,
		fixture.contextBinding,
	}
	input, _, _ := fixture.freeze(t, "other-family", 1, 1, 0, 1)
	assertE5BPublicationConflictNoWrites(
		t,
		fixture.base.publication.store,
		input,
		"must provide exact ordered action.provider/v1, context.provide/v1",
	)
}

func TestW2E5BDocumentInsightPortRemovalOrder(t *testing.T) {
	t.Run("removing Context while Action remains is rejected", func(t *testing.T) {
		fixture := newE5BDocumentInsightFixture(t)
		fixture.bindBothActionFirst()
		first, firstControl, firstCatalog := fixture.freeze(
			t, "remove-context-first", 1, 1, 0, 1,
		)
		if _, err := fixture.base.publication.store.PublishControlCatalog(
			context.Background(), first,
		); err != nil {
			t.Fatalf("initial dual publication: %v", err)
		}
		fixture.base.control.Profiles[0].Bindings = []controlcontract.BindingSpec{
			fixture.modelBinding,
			fixture.actionBinding,
		}
		second, _, _ := fixture.freeze(
			t, "remove-context-second", 2, 2, 1, 2,
		)
		assertE5BPublicationConflictNoWrites(
			t,
			fixture.base.publication.store,
			second,
			"same-instance Binding must exist in the same Profile, found 0",
		)
		if err := VerifyPublishedControlCatalogClosureV1(
			context.Background(),
			fixture.base.publication.store.db,
			firstControl,
			firstCatalog,
		); err != nil {
			t.Fatalf("rejected update damaged current publication: %v", err)
		}
	})

	t.Run("removing Action first leaves governed Context valid", func(t *testing.T) {
		fixture := newE5BDocumentInsightFixture(t)
		fixture.bindBothActionFirst()
		first, _, _ := fixture.freeze(
			t, "remove-action-first", 1, 1, 0, 1,
		)
		if _, err := fixture.base.publication.store.PublishControlCatalog(
			context.Background(), first,
		); err != nil {
			t.Fatalf("initial dual publication: %v", err)
		}
		fixture.base.control.Profiles[0].Bindings = []controlcontract.BindingSpec{
			fixture.modelBinding,
			fixture.contextBinding,
		}
		second, control, catalog := fixture.freeze(
			t, "remove-action-second", 2, 2, 1, 2,
		)
		if _, err := fixture.base.publication.store.PublishControlCatalog(
			context.Background(), second,
		); err != nil {
			t.Fatalf("Context-only update: %v", err)
		}
		if err := VerifyPublishedControlCatalogClosureV1(
			context.Background(),
			fixture.base.publication.store.db,
			control,
			catalog,
		); err != nil {
			t.Fatalf("Context-only public Verify: %v", err)
		}
	})
}

func TestW2E5BDocumentInsightPublicationCASCompetitionHasOneWinner(
	t *testing.T,
) {
	fixture := newE5BDocumentInsightFixture(t)
	fixture.bindBothActionFirst()
	first, _, _ := fixture.freeze(t, "cas-first", 1, 1, 0, 1)
	second, _, _ := fixture.freeze(t, "cas-second", 1, 1, 0, 1)
	inputs := []PublishControlCatalogInput{first, second}

	start := make(chan struct{})
	errorsByIndex := make([]error, len(inputs))
	var group sync.WaitGroup
	for index := range inputs {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, errorsByIndex[index] = fixture.base.publication.store.PublishControlCatalog(
				context.Background(),
				inputs[index],
			)
		}(index)
	}
	close(start)
	group.Wait()

	successes := 0
	conflicts := 0
	for _, err := range errorsByIndex {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrPublicationConflict):
			conflicts++
		default:
			t.Fatalf("unexpected E5-B CAS outcome: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf(
			"E5-B CAS competition successes=%d conflicts=%d errors=%v",
			successes,
			conflicts,
			errorsByIndex,
		)
	}
}

func replaceE5BDocumentInsightManifest(
	t *testing.T,
	fixture *e5BDocumentInsightFixture,
	requires []moduleapi.PortRef,
	permissions []moduleapi.Permission,
) {
	replaceE5BDocumentInsightManifestShape(
		t,
		fixture,
		moduleapi.ExactDocumentInsightProvidesV1(),
		requires,
		permissions,
	)
}

func replaceE5BDocumentInsightManifestShape(
	t *testing.T,
	fixture *e5BDocumentInsightFixture,
	provides []moduleapi.PortRef,
	requires []moduleapi.PortRef,
	permissions []moduleapi.Permission,
) {
	t.Helper()
	extra := map[string]any{
		"provides": append([]moduleapi.PortRef(nil), provides...),
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
	manifest := canonicalModuleManifest(
		t,
		fixture.base.knowledgeInstallation.ModuleID,
		fixture.base.knowledgeInstallation.ExactVersion,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		extra,
	)
	manifestRef := putPublicationJSON(
		t,
		fixture.base.publication.store,
		ContentModuleManifest,
		manifest,
	)
	if _, err := fixture.base.publication.store.db.Exec(`
		UPDATE module_installations
		SET manifest_ref=?
		WHERE installation_id=?
	`, manifestRef, fixture.base.knowledgeInstallation.InstallationID); err != nil {
		t.Fatalf("replace Document Insight Manifest: %v", err)
	}
}

func newE5BDocumentInsightFixture(t *testing.T) *e5BDocumentInsightFixture {
	t.Helper()
	base := newE5APublicationFixture(
		t,
		[]moduleapi.PortRef{e5AModelPort()},
		[]moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
		nil,
		nil,
		nil,
	)
	manifest := canonicalModuleManifest(
		t,
		moduleapi.DocumentInsightModuleIDV1,
		moduleapi.DocumentInsightVersionV1,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		map[string]any{
			"provides":              moduleapi.ExactDocumentInsightProvidesV1(),
			"requires":              []moduleapi.PortRef{moduleapi.ExactModelGeneratePortV1()},
			"requested_permissions": []moduleapi.Permission{moduleapi.PermissionKnowledgeReadV1},
			"runtime": map[string]any{
				"mode":       string(moduleapi.RuntimeModeRequestTrustedInProcess),
				"protocol":   moduleapi.RuntimeProtocolGoInProcessV1,
				"entrypoint": "content/source.json",
			},
		},
	)
	provider, installation := installActivateExactDocumentInsightE5B(
		t,
		base.publication.store,
		base.publication.tenantID,
		manifest,
	)
	if parsed, _, err := moduleapi.ParseModuleManifestV1(manifest); err != nil {
		t.Fatalf("parse Document Insight Manifest: %v", err)
	} else if err := moduleapi.ClassifyExactDocumentInsightManifestV1(parsed); err != nil {
		t.Fatalf("classify Document Insight Manifest: %v", err)
	}

	modelBinding := base.control.Profiles[0].Bindings[0]
	contextBinding := base.control.Profiles[0].Bindings[1]
	contextBinding.InstanceID = provider.InstanceID
	actionConfig, actionAuthority := e5BActionBindingMaterials(t, base)
	actionBinding := controlcontract.BindingSpec{
		Port:                e5AActionPort(),
		InstanceID:          provider.InstanceID,
		ConfigRef:           actionConfig,
		AuthorityCeilingRef: actionAuthority,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	base.knowledgeInstallation = installation
	base.catalog.Entries[1] = controlcontract.CatalogEntry{
		Activation: provider,
		Provides:   moduleapi.ExactDocumentInsightProvidesV1(),
	}
	fixture := &e5BDocumentInsightFixture{
		base:           base,
		modelBinding:   modelBinding,
		contextBinding: contextBinding,
		actionBinding:  actionBinding,
		provider:       provider,
	}
	fixture.bindBothActionFirst()
	return fixture
}

func installActivateExactDocumentInsightE5B(
	t *testing.T,
	store *Store,
	tenantID string,
	manifest []byte,
) (moduleapi.ActivatedModuleRef, ModuleInstallation) {
	t.Helper()
	installation, err := store.InstallModule(
		context.Background(),
		installInput(
			t,
			"installation-document-insight-e5b",
			manifest,
			moduleapi.DocumentInsightArtifactDigestV1,
		),
	)
	if err != nil {
		t.Fatalf("InstallModule Document Insight: %v", err)
	}
	activation, err := store.ActivateModule(
		context.Background(),
		ActivateModuleInput{
			ActivationID:       "activation-document-insight-e5b",
			TenantID:           tenantID,
			InstanceID:         "instance-document-insight-e5b",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    moduleapi.DocumentInsightAdapterIdentityV1,
		},
	)
	if err != nil {
		t.Fatalf("ActivateModule Document Insight: %v", err)
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

func (fixture *e5BDocumentInsightFixture) bindBothActionFirst() {
	fixture.base.control.Profiles[0].Bindings = []controlcontract.BindingSpec{
		fixture.modelBinding,
		fixture.actionBinding,
		fixture.contextBinding,
	}
}

func (fixture *e5BDocumentInsightFixture) freeze(
	t *testing.T,
	suffix string,
	controlRevision uint64,
	catalogGeneration uint64,
	expectedPointerRevision uint64,
	newPointerRevision uint64,
) (
	PublishControlCatalogInput,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
) {
	t.Helper()
	control := fixture.base.control
	control.SnapshotID = "control-e5b-" + suffix
	control.Revision = controlRevision
	control.Digest = ""
	frozenControl, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("NewControlSnapshot E5-B: %v", err)
	}
	catalog := fixture.base.catalog
	catalog.GenerationID = "catalog-e5b-" + suffix
	catalog.Generation = catalogGeneration
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	frozenCatalog, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("NewCatalogGeneration E5-B: %v", err)
	}
	return PublishControlCatalogInput{
		ExpectedPointerRevision: expectedPointerRevision,
		NewPointerRevision:      newPointerRevision,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	}, frozenControl, frozenCatalog
}

func e5BActionBindingMaterials(
	t *testing.T,
	base *e5APublicationFixture,
) (string, string) {
	t.Helper()
	_, configCanonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "document.text-stats",
				ProviderActionID: "text.stats",
				LocalEffectClass: moduleapi.EffectReadOnly,
				MaxResultBytes:   4096,
			}},
			Parameters: []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 base.publication.tenantID,
			AllowedWorkspaceIDs:      []string{base.control.Workspaces[0].Workspace.ID},
			AllowedProviderActionIDs: []string{"text.stats"},
			MaxEffectClass:           moduleapi.EffectReadOnly,
			MaxResultBytes:           4096,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return putPublicationJSON(
			t, base.publication.store, ContentConfig, configCanonical,
		), putPublicationJSON(
			t, base.publication.store, ContentAuthorityCeiling, authorityCanonical,
		)
}

func e5BKnowledgeBindingMaterials(
	t *testing.T,
	fixture *e5BDocumentInsightFixture,
	suffix string,
) (string, string) {
	t.Helper()
	source := moduleapi.KnowledgeSourceRefV1{
		ID:      "shared.docs.e5b." + suffix,
		Version: "1.0.0",
		Digest:  strings.Repeat("c", 64),
	}
	_, parameters, err := moduleapi.NewKnowledgeContextBindingV1(
		moduleapi.KnowledgeContextBindingV1{
			SchemaVersion:     moduleapi.KnowledgeContextBindingSchemaV1,
			Source:            source,
			MaxHits:           3,
			MaxTotalTextBytes: 3072,
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
	_, authorityCanonical, err := moduleapi.NewKnowledgeAuthorityCeilingV1(
		moduleapi.KnowledgeAuthorityCeilingV1{
			SchemaVersion: moduleapi.KnowledgeAuthorityCeilingSchemaV1,
			Source:        source,
			AllowedScopes: []moduleapi.KnowledgeScopeRuleV1{{
				TenantID:     fixture.base.publication.tenantID,
				WorkspaceID:  fixture.base.control.Workspaces[0].Workspace.ID,
				AgentID:      fixture.base.control.Agents[0].ID,
				TaskInputRef: "*",
			}},
			MaxHits:           2,
			MaxTotalTextBytes: 2048,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return putPublicationJSON(
			t, fixture.base.publication.store, ContentConfig, configCanonical,
		), putPublicationJSON(
			t, fixture.base.publication.store, ContentAuthorityCeiling, authorityCanonical,
		)
}

func e5BMemoryContextBinding(
	t *testing.T,
	fixture *e5BDocumentInsightFixture,
) controlcontract.BindingSpec {
	t.Helper()
	_, parameters, _, err := moduleapi.NewMemoryContextBindingV1(
		moduleapi.MemoryContextBindingV1{
			SchemaVersion:     moduleapi.MemoryContextBindingSchemaV1,
			Kinds:             []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryFact},
			MaxItems:          2,
			MaxTotalTextBytes: 2048,
			CategoryRules:     []moduleapi.MemoryCategoryRuleV1{},
			StopTerms:         []string{},
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
	_, authorityCanonical, err := moduleapi.NewMemoryAuthorityCeilingV1(
		moduleapi.MemoryAuthorityCeilingV1{
			SchemaVersion:       moduleapi.MemoryAuthorityCeilingSchemaV1,
			TenantID:            fixture.base.publication.tenantID,
			AgentID:             fixture.base.control.Agents[0].ID,
			AllowedWorkspaceIDs: []string{fixture.base.control.Workspaces[0].Workspace.ID},
			AllowedKinds:        []moduleapi.MemoryEntryKindV1{moduleapi.MemoryEntryFact},
			MaxItems:            2,
			MaxTotalTextBytes:   2048,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := fixture.contextBinding
	binding.ConfigRef = putPublicationJSON(
		t, fixture.base.publication.store, ContentConfig, configCanonical,
	)
	binding.AuthorityCeilingRef = putPublicationJSON(
		t,
		fixture.base.publication.store,
		ContentAuthorityCeiling,
		authorityCanonical,
	)
	return binding
}

func e5BOutsideTenantKnowledgeAuthority(
	t *testing.T,
	fixture *e5BDocumentInsightFixture,
) string {
	t.Helper()
	configRecord, err := queryContent(
		context.Background(),
		fixture.base.publication.store.db,
		fixture.contextBinding.ConfigRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	config, err := moduleapi.RestoreContextBindingConfigV1(
		configRecord.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	knowledge, err := moduleapi.RestoreKnowledgeContextBindingParametersV1(config)
	if err != nil {
		t.Fatal(err)
	}
	_, authorityCanonical, err := moduleapi.NewKnowledgeAuthorityCeilingV1(
		moduleapi.KnowledgeAuthorityCeilingV1{
			SchemaVersion: moduleapi.KnowledgeAuthorityCeilingSchemaV1,
			Source:        knowledge.Source,
			AllowedScopes: []moduleapi.KnowledgeScopeRuleV1{{
				TenantID:     "tenant-outside-e5b",
				WorkspaceID:  fixture.base.control.Workspaces[0].Workspace.ID,
				AgentID:      fixture.base.control.Agents[0].ID,
				TaskInputRef: "*",
			}},
			MaxHits:           2,
			MaxTotalTextBytes: 2048,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return putPublicationJSON(
		t,
		fixture.base.publication.store,
		ContentAuthorityCeiling,
		authorityCanonical,
	)
}

func assertE5BPublicationConflictNoWrites(
	t *testing.T,
	store *Store,
	input PublishControlCatalogInput,
	wantDetail string,
) {
	t.Helper()
	before := publicationRowCounts(t, store)
	_, firstErr := store.PublishControlCatalog(context.Background(), input)
	if !errors.Is(firstErr, ErrPublicationConflict) ||
		!strings.Contains(firstErr.Error(), wantDetail) {
		t.Fatalf("publication error=%v, want conflict containing %q", firstErr, wantDetail)
	}
	_, secondErr := store.PublishControlCatalog(context.Background(), input)
	if secondErr == nil || secondErr.Error() != firstErr.Error() {
		t.Fatalf("deterministic retry errors=(%v,%v)", firstErr, secondErr)
	}
	if after := publicationRowCounts(t, store); after != before {
		t.Fatalf("invalid publication wrote rows before=%v after=%v", before, after)
	}
}
