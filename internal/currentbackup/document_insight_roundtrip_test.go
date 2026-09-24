package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	documentInsightBackupInstallationID = "installation-backup-document-insight-v1"
	documentInsightBackupActivationID   = "activation-backup-document-insight-v1"
	documentInsightBackupInstanceID     = "document-insight-backup-v1"
)

type documentInsightBackupFixture struct {
	base           ragBackupFixture
	moduleID       string
	exactVersion   string
	artifactDigest string
	tenantID       string
	controlID      string
	catalogID      string
}

type documentInsightBackupClosure struct {
	Basis        controlcontract.PublishedBasis
	Control      controlcontract.ControlSnapshot
	Catalog      controlcontract.CatalogGeneration
	Installation currentstore.ModuleInstallation
	Activation   currentstore.ModuleActivation
}

type documentInsightPublicationReader interface {
	LoadPublishedBasis(
		context.Context,
		string,
	) (
		controlcontract.PublishedBasis,
		controlcontract.ControlSnapshot,
		controlcontract.CatalogGeneration,
		error,
	)
	VerifyPublishedControlCatalogClosureV1(
		context.Context,
		controlcontract.ControlSnapshot,
		controlcontract.CatalogGeneration,
	) error
	GetModuleInstallationByIdentity(
		context.Context,
		string,
		string,
	) (currentstore.ModuleInstallation, error)
	GetModuleActivationByIdentity(
		context.Context,
		string,
		string,
		uint64,
	) (currentstore.ModuleActivation, error)
}

func TestW2E5BDocumentInsightDualPortBundleRoundTrip(t *testing.T) {
	ctx := context.Background()
	fixture := newDocumentInsightBackupFixture(t)
	source := captureDocumentInsightBackupClosure(
		t,
		openDocumentInsightBackupObserver(t, fixture.base.databasePath),
		fixture,
	)
	sourceRAG := loadContextCompilationClosure(
		t,
		fixture.base.databasePath,
		fixture.base.attemptID,
	)
	assertHistoricalRAGEvidenceUnchanged(
		t,
		fixture.base.sourceClosure,
		sourceRAG,
	)
	assertDocumentInsightDatabaseCardinality(t, fixture.base.databasePath, fixture)
	assertDocumentInsightArtifact(t, fixture.base.artifactRoot, fixture, source)

	bundle := filepath.Join(t.TempDir(), "document-insight.bundle")
	manifest, err := CreateBundle(
		ctx,
		fixture.base.databasePath,
		fixture.base.artifactRoot,
		bundle,
		"currentbackup-document-insight-e5b-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(Document Insight): %v", err)
	}
	artifactCopies := 0
	for _, artifact := range manifest.Artifacts {
		if artifact.Digest == fixture.artifactDigest {
			artifactCopies++
		}
	}
	if artifactCopies != 1 {
		t.Fatalf(
			"Document Insight artifact bundle copies=%d want 1: %+v",
			artifactCopies,
			manifest.Artifacts,
		)
	}
	verified, err := VerifyBundle(ctx, bundle)
	if err != nil {
		t.Fatalf("VerifyBundle(Document Insight): %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest ||
		verified.ArtifactCount != manifest.ArtifactCount {
		t.Fatalf(
			"verified Document Insight bundle=%+v want %+v",
			verified,
			manifest,
		)
	}

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle(Document Insight): %v", err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
		t.Fatalf("restored Document Insight semantic closure: %v", err)
	}

	// Reopen through the supported writer path after restore. This proves that
	// the restored graph is not merely readable by the backup verifier.
	reopened, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatalf("reopen restored Document Insight Store: %v", err)
	}
	restored := captureDocumentInsightBackupClosure(t, reopened, fixture)
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened Document Insight Store: %v", err)
	}
	if !reflect.DeepEqual(restored, source) {
		t.Fatalf(
			"restored Document Insight closure differs:\nsource=%#v\nrestored=%#v",
			source,
			restored,
		)
	}
	assertDocumentInsightDatabaseCardinality(t, restoredDatabase, fixture)
	assertDocumentInsightArtifact(t, restoredArtifacts, fixture, restored)

	// The new current publication must not rewrite the historical RAG evidence
	// created under the preceding immutable Control/Catalog revision.
	restoredRAG := loadContextCompilationClosure(
		t,
		restoredDatabase,
		fixture.base.attemptID,
	)
	if !reflect.DeepEqual(restoredRAG, sourceRAG) {
		t.Fatalf(
			"historical RAG evidence changed across Document Insight restore:\nsource=%#v\nrestored=%#v",
			sourceRAG,
			restoredRAG,
		)
	}
}

func TestW2E5BDocumentInsightBackupSemanticTamperingFailsClosed(t *testing.T) {
	fixture := newDocumentInsightBackupFixture(t)
	tests := []struct {
		name       string
		mutate     func(*testing.T, string, documentInsightBackupFixture)
		wantDetail string
	}{
		{
			name: "Manifest missing model Require",
			mutate: func(t *testing.T, databasePath string, fixture documentInsightBackupFixture) {
				tamperDocumentInsightBackupManifest(t, databasePath, fixture, func(manifest *moduleapi.ModuleManifestV1) {
					manifest.Requires = nil
				})
			},
			wantDetail: "must require only exact model.generate/v2",
		},
		{
			name: "Manifest missing knowledge permission",
			mutate: func(t *testing.T, databasePath string, fixture documentInsightBackupFixture) {
				tamperDocumentInsightBackupManifest(t, databasePath, fixture, func(manifest *moduleapi.ModuleManifestV1) {
					manifest.RequestedPermissions = nil
				})
			},
			wantDetail: "must request only exact knowledge.read permission",
		},
		{
			name: "Catalog expands Provides beyond Manifest",
			mutate: func(t *testing.T, databasePath string, fixture documentInsightBackupFixture) {
				tamperDocumentInsightBackupCatalogProvides(t, databasePath, fixture)
			},
			wantDetail: "foreign_key_check reported a violation",
		},
		{
			name: "Manifest and Catalog downgrade to Action only",
			mutate: func(t *testing.T, databasePath string, fixture documentInsightBackupFixture) {
				actionOnly := []moduleapi.PortRef{
					{Name: moduleapi.PortNameActionProvider, ExactVersion: moduleapi.PortVersionV1},
				}
				tamperDocumentInsightBackupManifest(t, databasePath, fixture, func(manifest *moduleapi.ModuleManifestV1) {
					manifest.Provides = append([]moduleapi.PortRef(nil), actionOnly...)
					manifest.Requires = nil
					manifest.RequestedPermissions = nil
				})
				controlDigest := tamperDocumentInsightBackupControlRemoveContext(
					t,
					databasePath,
					fixture,
				)
				tamperDocumentInsightBackupCatalogProvidesWithControlDigest(
					t,
					databasePath,
					fixture,
					actionOnly,
					controlDigest,
				)
			},
			wantDetail: "foreign_key_check reported a violation",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyPath := filepath.Join(t.TempDir(), "tampered.sqlite")
			copyTestFile(t, fixture.base.databasePath, copyPath)
			test.mutate(t, copyPath, fixture)
			before := knowledgePublicationRowCounts(t, copyPath)

			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				copyPath,
			); !errors.Is(err, ErrIntegrity) ||
				!strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf(
					"VerifyCurrentStoreSemanticClosure(%s) error=%v, want ErrIntegrity containing %q",
					test.name,
					err,
					test.wantDetail,
				)
			}
			bundle := filepath.Join(t.TempDir(), "rejected.bundle")
			if _, err := CreateBundle(
				context.Background(),
				copyPath,
				fixture.base.artifactRoot,
				bundle,
				"currentbackup-document-insight-e5b-tamper-test/v1",
			); !errors.Is(err, ErrIntegrity) ||
				!strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf(
					"CreateBundle(%s) error=%v, want ErrIntegrity containing %q",
					test.name,
					err,
					test.wantDetail,
				)
			}
			if _, err := os.Stat(bundle); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected Document Insight backup published destination: %v", err)
			}
			if after := knowledgePublicationRowCounts(t, copyPath); after != before {
				t.Fatalf(
					"failed Document Insight verification wrote rows: before=%v after=%v",
					before,
					after,
				)
			}
		})
	}
}

func newDocumentInsightBackupFixture(t *testing.T) documentInsightBackupFixture {
	t.Helper()
	ctx := context.Background()
	base := newRAGBackupFixture(t)
	store, err := currentstore.OpenExistingCurrentStore(ctx, base.databasePath)
	if err != nil {
		t.Fatalf("open RAG Store for Document Insight publication: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()

	basis, control, catalog, err := store.LoadPublishedBasis(ctx, "default")
	if err != nil {
		t.Fatalf("load RAG PublishedBasis: %v", err)
	}
	if len(control.Workspaces) != 1 || len(control.Agents) != 1 {
		t.Fatalf("unexpected RAG control shape: %+v", control)
	}

	artifactDirectory := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"bootstrap-artifacts",
		"freeagent.builtin.document-insight",
		"2.0.0",
	)
	manifestBytes, err := os.ReadFile(filepath.Join(
		artifactDirectory,
		moduleapi.ArtifactManifestPath,
	))
	if err != nil {
		t.Fatalf("read Document Insight Manifest: %v", err)
	}
	manifest, canonicalManifest, err := moduleapi.ParseModuleManifestV1(manifestBytes)
	if err != nil || !bytes.Equal(manifestBytes, canonicalManifest) {
		t.Fatalf("parse canonical Document Insight Manifest: %v", err)
	}
	if err := moduleapi.ClassifyExactDocumentInsightManifestV1(manifest); err != nil {
		t.Fatalf("classify Document Insight Manifest: %v", err)
	}
	sourceCanonical, err := os.ReadFile(filepath.Join(
		artifactDirectory,
		filepath.FromSlash(manifest.Runtime.Entrypoint),
	))
	if err != nil {
		t.Fatalf("read Document Insight source: %v", err)
	}
	_, sourceRef, err := moduleapi.RestoreKnowledgeSourceV1(sourceCanonical)
	if err != nil {
		t.Fatalf("restore Document Insight source: %v", err)
	}
	if sourceRef != base.sourceRef {
		t.Fatalf(
			"Document Insight source=%+v, want existing RAG source %+v",
			sourceRef,
			base.sourceRef,
		)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatalf("scan Document Insight artifact: %v", err)
	}
	artifactDigest, err := moduleapi.ComputeArtifactDigest(canonicalManifest, files)
	if err != nil {
		t.Fatalf("compute Document Insight artifact digest: %v", err)
	}
	verifiedArtifact, err := verifyArtifactDirectory(
		artifactDirectory,
		artifactDigest,
	)
	if err != nil {
		t.Fatalf("verify Document Insight artifact: %v", err)
	}
	if err := copyVerifiedArtifact(
		ctx,
		artifactDirectory,
		filepath.Join(base.artifactRoot, artifactDigest),
		verifiedArtifact,
	); err != nil {
		t.Fatalf("copy Document Insight artifact: %v", err)
	}

	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		canonicalManifest,
	)
	if err != nil {
		t.Fatalf("compute Document Insight manifest ref: %v", err)
	}
	installation, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
		InstallationID:      documentInsightBackupInstallationID,
		ModuleID:            manifest.ID,
		ExactVersion:        manifest.Version,
		ExpectedManifestRef: manifestRef,
		ManifestBytes:       canonicalManifest,
		ArtifactDigest:      artifactDigest,
	})
	if err != nil {
		t.Fatalf("install Document Insight: %v", err)
	}
	activation, err := store.ActivateModule(ctx, currentstore.ActivateModuleInput{
		ActivationID:       documentInsightBackupActivationID,
		TenantID:           control.TenantID,
		InstanceID:         documentInsightBackupInstanceID,
		InstallationID:     installation.InstallationID,
		ActivationRevision: 1,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    exactadapter.DocumentInsightAdapterIdentityV1,
	})
	if err != nil {
		t.Fatalf("activate Document Insight: %v", err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}

	actionConfigRef, actionAuthorityRef := putDocumentInsightActionMaterials(
		t,
		store,
		control.TenantID,
		control.Workspaces[0].Workspace.ID,
	)
	targetProfile := -1
	contextBindings := 0
	for profileIndex := range control.Profiles {
		bindings := control.Profiles[profileIndex].Bindings
		for bindingIndex := range bindings {
			binding := &bindings[bindingIndex]
			if binding.Port != moduleapi.ExactContextProvidePortV1() ||
				binding.InstanceID != base.knowledgeAssertion.InstanceID {
				continue
			}
			binding.InstanceID = provider.InstanceID
			targetProfile = profileIndex
			contextBindings++
		}
		control.Profiles[profileIndex].Bindings = bindings
	}
	if contextBindings != 1 || targetProfile < 0 {
		t.Fatalf("RAG Context Binding count=%d want 1", contextBindings)
	}
	actionPort := moduleapi.ExactDocumentInsightProvidesV1()[0]
	control.Profiles[targetProfile].Bindings = append(
		control.Profiles[targetProfile].Bindings,
		controlcontract.BindingSpec{
			Port:                actionPort,
			InstanceID:          provider.InstanceID,
			ConfigRef:           actionConfigRef,
			AuthorityCeilingRef: actionAuthorityRef,
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		},
	)
	control.SnapshotID = "control-backup-document-insight-v1"
	control.Revision = basis.Control.Revision + 1
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze Document Insight Control: %v", err)
	}

	entries := make([]controlcontract.CatalogEntry, 0, len(catalog.Entries))
	removedKnowledge := 0
	for _, entry := range catalog.Entries {
		if entry.Activation.InstanceID == base.knowledgeAssertion.InstanceID {
			removedKnowledge++
			continue
		}
		entries = append(entries, entry)
	}
	if removedKnowledge != 1 {
		t.Fatalf("RAG Catalog Knowledge entry count=%d want 1", removedKnowledge)
	}
	entries = append(entries, controlcontract.CatalogEntry{
		Activation: provider,
		Provides:   moduleapi.ExactDocumentInsightProvidesV1(),
	})
	catalog.GenerationID = "catalog-backup-document-insight-v1"
	catalog.Generation = basis.Catalog.Generation + 1
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Entries = entries
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze Document Insight Catalog: %v", err)
	}
	if _, err := store.PublishControlCatalog(ctx, currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: basis.PointerRevision,
		NewPointerRevision:      basis.PointerRevision + 1,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	}); err != nil {
		t.Fatalf("publish Document Insight Control/Catalog: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close Document Insight Store: %v", err)
	}
	closed = true

	return documentInsightBackupFixture{
		base:           base,
		moduleID:       manifest.ID,
		exactVersion:   manifest.Version,
		artifactDigest: artifactDigest,
		tenantID:       control.TenantID,
		controlID:      controlRef.SnapshotID,
		catalogID:      catalogRef.GenerationID,
	}
}

func putDocumentInsightActionMaterials(
	t *testing.T,
	store *currentstore.Store,
	tenantID string,
	workspaceID string,
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
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("freeze Document Insight Action config: %v", err)
	}
	_, authorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 tenantID,
			AllowedWorkspaceIDs:      []string{workspaceID},
			AllowedProviderActionIDs: []string{"text.stats"},
			MaxEffectClass:           moduleapi.EffectReadOnly,
			MaxResultBytes:           4096,
		},
	)
	if err != nil {
		t.Fatalf("freeze Document Insight Action authority: %v", err)
	}
	return putDocumentInsightContent(t, store, currentstore.ContentConfig, configCanonical),
		putDocumentInsightContent(
			t,
			store,
			currentstore.ContentAuthorityCeiling,
			authorityCanonical,
		)
}

func putDocumentInsightContent(
	t *testing.T,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	canonical []byte,
) string {
	t.Helper()
	digest, err := currentstore.ComputeContentDigest(
		kind,
		"application/json",
		canonical,
	)
	if err != nil {
		t.Fatalf("compute Document Insight %s content digest: %v", kind, err)
	}
	if _, err := store.PutContent(context.Background(), currentstore.ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      "application/json",
		CanonicalBytes: canonical,
	}); err != nil {
		t.Fatalf("put Document Insight %s content: %v", kind, err)
	}
	return digest
}

func openDocumentInsightBackupObserver(
	t *testing.T,
	databasePath string,
) *currentstore.ReadOnlyObserver {
	t.Helper()
	observer, err := currentstore.OpenReadOnlyObserver(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatalf("open Document Insight read-only observer: %v", err)
	}
	t.Cleanup(func() {
		if err := observer.Close(); err != nil {
			t.Errorf("close Document Insight read-only observer: %v", err)
		}
	})
	return observer
}

func captureDocumentInsightBackupClosure(
	t *testing.T,
	reader documentInsightPublicationReader,
	fixture documentInsightBackupFixture,
) documentInsightBackupClosure {
	t.Helper()
	ctx := context.Background()
	basis, control, catalog, err := reader.LoadPublishedBasis(ctx, fixture.tenantID)
	if err != nil {
		t.Fatalf("LoadPublishedBasis(Document Insight): %v", err)
	}
	if err := reader.VerifyPublishedControlCatalogClosureV1(
		ctx,
		control,
		catalog,
	); err != nil {
		t.Fatalf("verify Document Insight published closure: %v", err)
	}
	installation, err := reader.GetModuleInstallationByIdentity(
		ctx,
		fixture.moduleID,
		fixture.exactVersion,
	)
	if err != nil {
		t.Fatalf("get Document Insight Installation: %v", err)
	}
	activation, err := reader.GetModuleActivationByIdentity(
		ctx,
		fixture.tenantID,
		documentInsightBackupInstanceID,
		1,
	)
	if err != nil {
		t.Fatalf("get Document Insight Activation: %v", err)
	}
	if installation.InstallationID != documentInsightBackupInstallationID ||
		installation.ArtifactDigest != fixture.artifactDigest ||
		activation.ActivationID != documentInsightBackupActivationID ||
		activation.InstallationID != installation.InstallationID ||
		basis.Control.SnapshotID != fixture.controlID ||
		basis.Catalog.GenerationID != fixture.catalogID {
		t.Fatalf(
			"Document Insight immutable identity differs: basis=%+v installation=%+v activation=%+v",
			basis,
			installation,
			activation,
		)
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(
		installation.ManifestBytes,
	)
	if err != nil || !bytes.Equal(canonical, installation.ManifestBytes) {
		t.Fatalf("restore canonical installed Document Insight Manifest: %v", err)
	}
	if err := moduleapi.ClassifyExactDocumentInsightManifestV1(manifest); err != nil {
		t.Fatalf("installed Document Insight Manifest classification: %v", err)
	}
	entry, found := catalog.FindInstance(documentInsightBackupInstanceID)
	if !found || !slices.Equal(
		entry.Provides,
		moduleapi.ExactDocumentInsightProvidesV1(),
	) || entry.Activation.ModuleID != installation.ModuleID ||
		entry.Activation.Version != installation.ExactVersion ||
		entry.Activation.ArtifactDigest != installation.ArtifactDigest ||
		entry.Activation.InstanceID != activation.InstanceID ||
		entry.Activation.ActivationRevision != activation.ActivationRevision ||
		entry.Activation.ExecutionClass != activation.ExecutionClass ||
		entry.Activation.AdapterIdentity != activation.AdapterIdentity {
		t.Fatalf("Document Insight Catalog entry differs: %+v found=%v", entry, found)
	}

	matchingProfiles := 0
	matchingBindings := 0
	actionBindings := 0
	contextBindings := 0
	for _, profile := range control.Profiles {
		profileMatches := 0
		for _, binding := range profile.Bindings {
			if binding.InstanceID != documentInsightBackupInstanceID {
				continue
			}
			matchingBindings++
			profileMatches++
			switch binding.Port {
			case moduleapi.ExactDocumentInsightProvidesV1()[0]:
				actionBindings++
			case moduleapi.ExactContextProvidePortV1():
				contextBindings++
			default:
				t.Fatalf("unexpected Document Insight Binding port: %+v", binding.Port)
			}
		}
		if profileMatches != 0 {
			matchingProfiles++
		}
	}
	if matchingProfiles != 1 || matchingBindings != 2 ||
		actionBindings != 1 || contextBindings != 1 {
		t.Fatalf(
			"Document Insight bindings profiles=%d total=%d action=%d context=%d",
			matchingProfiles,
			matchingBindings,
			actionBindings,
			contextBindings,
		)
	}
	return documentInsightBackupClosure{
		Basis:        basis,
		Control:      control,
		Catalog:      catalog,
		Installation: installation,
		Activation:   activation,
	}
}

func assertDocumentInsightDatabaseCardinality(
	t *testing.T,
	databasePath string,
	fixture documentInsightBackupFixture,
) {
	t.Helper()
	database, err := sql.Open("sqlite", sqliteFileURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var installations, activations int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM module_installations
		WHERE module_id=? AND exact_version=? AND artifact_digest=?
	`, fixture.moduleID, fixture.exactVersion, fixture.artifactDigest).Scan(
		&installations,
	); err != nil {
		t.Fatalf("count Document Insight Installations: %v", err)
	}
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM module_activations
		WHERE tenant_id=? AND instance_id=? AND activation_revision=1
	`, fixture.tenantID, documentInsightBackupInstanceID).Scan(&activations); err != nil {
		t.Fatalf("count Document Insight Activations: %v", err)
	}
	if installations != 1 || activations != 1 {
		t.Fatalf(
			"Document Insight row cardinality installations=%d activations=%d want 1/1",
			installations,
			activations,
		)
	}
}

func assertDocumentInsightArtifact(
	t *testing.T,
	artifactRoot string,
	fixture documentInsightBackupFixture,
	closure documentInsightBackupClosure,
) {
	t.Helper()
	verified, err := verifyArtifactDirectory(
		filepath.Join(artifactRoot, fixture.artifactDigest),
		fixture.artifactDigest,
	)
	if err != nil {
		t.Fatalf("verify restored Document Insight artifact: %v", err)
	}
	if verified.manifest.ID != fixture.moduleID ||
		verified.manifest.Version != fixture.exactVersion ||
		!bytes.Equal(
			verified.canonicalManifest,
			closure.Installation.ManifestBytes,
		) {
		t.Fatalf(
			"Document Insight artifact/Installation Manifest differs: artifact=%+v installation=%+v",
			verified.manifest,
			closure.Installation,
		)
	}
}

func assertHistoricalRAGEvidenceUnchanged(
	t *testing.T,
	before documentInsightRAGClosureAlias,
	after documentInsightRAGClosureAlias,
) {
	t.Helper()
	// ContentRows is an observation of the entire content_records table, so it
	// is expected to grow when the new Manifest and Action Binding materials
	// are installed. The immutable Attempt projections and their exact evidence
	// bytes must remain unchanged.
	after.ContentRows = before.ContentRows
	if !reflect.DeepEqual(after, before) {
		t.Fatalf(
			"Document Insight publication rewrote historical RAG evidence:\nbefore=%#v\nafter=%#v",
			before,
			after,
		)
	}
}

// Keep the helper signature visually distinct from the large fixture type
// while retaining its exact package-local representation.
type documentInsightRAGClosureAlias = contextCompilationClosureSnapshot

func tamperDocumentInsightBackupManifest(
	t *testing.T,
	databasePath string,
	fixture documentInsightBackupFixture,
	mutate func(*moduleapi.ModuleManifestV1),
) {
	t.Helper()
	database, err := sql.Open("sqlite", sqliteFileURI(databasePath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var original []byte
	if err := database.QueryRow(`
		SELECT content.canonical_bytes
		FROM module_installations AS installation
		JOIN content_records AS content
		  ON content.content_digest=installation.manifest_ref
		WHERE installation.installation_id=?
	`, documentInsightBackupInstallationID).Scan(&original); err != nil {
		t.Fatalf("read Document Insight Manifest for tamper: %v", err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(original)
	if err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(canonical); err != nil {
		t.Fatalf("tampered Document Insight Manifest is not structurally valid: %v", err)
	}
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		"application/json",
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, ?, ?, ?, ?, ?)
	`, digest, string(currentstore.ContentModuleManifest), "application/json", canonical, len(canonical), time.Now().UTC().UnixMicro()); err != nil {
		t.Fatalf("insert tampered Document Insight Manifest: %v", err)
	}
	result, err := database.Exec(`
		UPDATE module_installations SET manifest_ref=?
		WHERE installation_id=? AND module_id=? AND exact_version=? AND artifact_digest=?
	`, digest, documentInsightBackupInstallationID, fixture.moduleID, fixture.exactVersion, fixture.artifactDigest)
	if err != nil {
		t.Fatalf("replace Document Insight Manifest: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("replace Document Insight Manifest affected=%d error=%v", affected, err)
	}
}

func tamperDocumentInsightBackupCatalogProvides(
	t *testing.T,
	databasePath string,
	fixture documentInsightBackupFixture,
) {
	tamperDocumentInsightBackupCatalogProvidesTo(
		t,
		databasePath,
		fixture,
		append(
			moduleapi.ExactDocumentInsightProvidesV1(),
			moduleapi.ExactModelGeneratePortV1(),
		),
	)
}

func tamperDocumentInsightBackupCatalogProvidesTo(
	t *testing.T,
	databasePath string,
	fixture documentInsightBackupFixture,
	provides []moduleapi.PortRef,
) {
	tamperDocumentInsightBackupCatalogProvidesWithControlDigest(
		t,
		databasePath,
		fixture,
		provides,
		"",
	)
}

func tamperDocumentInsightBackupCatalogProvidesWithControlDigest(
	t *testing.T,
	databasePath string,
	fixture documentInsightBackupFixture,
	provides []moduleapi.PortRef,
	controlDigest string,
) {
	t.Helper()
	database, err := sql.Open("sqlite", sqliteFileURI(databasePath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var generation int64
	var digest string
	var canonical []byte
	if err := database.QueryRow(`
		SELECT catalog.generation, catalog.digest, catalog.canonical_json
		FROM control_current AS current
		JOIN runtime_catalog_generations AS catalog
		  ON catalog.generation_id=current.catalog_generation_id
		WHERE current.tenant_id=? AND catalog.generation_id=?
	`, fixture.tenantID, fixture.catalogID).Scan(
		&generation,
		&digest,
		&canonical,
	); err != nil {
		t.Fatalf("read Document Insight Catalog for tamper: %v", err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		canonical,
		controlcontract.CatalogGenerationRef{
			GenerationID: fixture.catalogID,
			Generation:   uint64(generation),
			Digest:       digest,
		},
	)
	if err != nil {
		t.Fatalf("restore Document Insight Catalog for tamper: %v", err)
	}
	found := 0
	for index := range catalog.Entries {
		if catalog.Entries[index].Activation.InstanceID !=
			documentInsightBackupInstanceID {
			continue
		}
		catalog.Entries[index].Provides = append(
			[]moduleapi.PortRef(nil),
			provides...,
		)
		found++
	}
	if found != 1 {
		t.Fatalf("Document Insight Catalog entry count=%d want 1", found)
	}
	if controlDigest != "" {
		catalog.ControlSnapshotDigest = controlDigest
	}
	catalog.Digest = ""
	_, rebuiltRef, rebuiltCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze tampered Document Insight Catalog: %v", err)
	}
	result := execClosedFileTamperV1(t, database,
		[]string{"runtime_catalog_generations_reject_update"}, `
		UPDATE runtime_catalog_generations
		SET digest=?, canonical_json=?
		WHERE generation_id=?
	`, rebuiltRef.Digest, rebuiltCanonical, fixture.catalogID)
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("replace Document Insight Catalog affected=%d error=%v", affected, err)
	}
}

func tamperDocumentInsightBackupControlRemoveContext(
	t *testing.T,
	databasePath string,
	fixture documentInsightBackupFixture,
) string {
	t.Helper()
	database, err := sql.Open("sqlite", sqliteFileURI(databasePath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var revision uint64
	var digest string
	var canonical []byte
	if err := database.QueryRow(`
		SELECT revision, digest, canonical_json
		FROM control_snapshots
		WHERE snapshot_id=?
	`, fixture.controlID).Scan(&revision, &digest, &canonical); err != nil {
		t.Fatalf("read Document Insight Control for tamper: %v", err)
	}
	control, err := controlcontract.RestoreControlSnapshot(
		canonical,
		controlcontract.ControlSnapshotRef{
			SnapshotID: fixture.controlID,
			Revision:   revision,
			Digest:     digest,
		},
	)
	if err != nil {
		t.Fatalf("restore Document Insight Control for tamper: %v", err)
	}
	removed := 0
	for profileIndex := range control.Profiles {
		bindings := control.Profiles[profileIndex].Bindings[:0]
		for _, binding := range control.Profiles[profileIndex].Bindings {
			if binding.InstanceID == documentInsightBackupInstanceID &&
				binding.Port == moduleapi.ExactContextProvidePortV1() {
				removed++
				continue
			}
			bindings = append(bindings, binding)
		}
		control.Profiles[profileIndex].Bindings = bindings
	}
	if removed != 1 {
		t.Fatalf("Document Insight Context Binding removed=%d want 1", removed)
	}
	_, rewrittenRef, rewritten, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze tampered Document Insight Control: %v", err)
	}
	result := execClosedFileTamperV1(t, database,
		[]string{"control_snapshots_reject_update"}, `
		UPDATE control_snapshots
		SET canonical_json=?, digest=?
		WHERE snapshot_id=?
	`, rewritten, rewrittenRef.Digest, fixture.controlID)
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("replace Document Insight Control affected=%d error=%v", affected, err)
	}
	return rewrittenRef.Digest
}
