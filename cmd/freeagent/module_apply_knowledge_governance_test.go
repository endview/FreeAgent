package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestGovernedKnowledgeFastPathsRejectDamagedCurrentPublicationWithoutWrites(
	t *testing.T,
) {
	tests := []struct {
		name            string
		expectedPointer uint64
	}{
		{name: "ALREADY_APPLIED", expectedPointer: 1},
		{name: "NO_CHANGE", expectedPointer: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			fixture := newModuleApplyKnowledgeFixtureVersionV1(t, "2.0.0")
			initialPlanPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "initial-governed-knowledge.json"),
				newEnabledModuleApplyKnowledgePlanV1(
					t,
					fixture,
					1,
					moduleapi.KnowledgeScopeRuleV1{
						TenantID:     defaultTenantID,
						WorkspaceID:  defaultWorkspaceID,
						AgentID:      defaultAgentID,
						TaskInputRef: "*",
					},
				),
			)
			initial, err := runModuleApplyFixtureV1(
				databasePath,
				artifactRoot,
				initialPlanPath,
				fixture.ArtifactDirectory,
				"",
			)
			if err != nil || initial.Status != moduleApplyStatusApplied ||
				initial.PointerRevision != 2 {
				t.Fatalf("initial governed Knowledge Apply=%+v error=%v", initial, err)
			}

			planPath := initialPlanPath
			if test.expectedPointer != 1 {
				planPath = writeModuleApplyPlanFixtureV1(
					t,
					filepath.Join(root, "current-governed-knowledge.json"),
					newEnabledModuleApplyKnowledgePlanV1(
						t,
						fixture,
						test.expectedPointer,
						moduleapi.KnowledgeScopeRuleV1{
							TenantID:     defaultTenantID,
							WorkspaceID:  defaultWorkspaceID,
							AgentID:      defaultAgentID,
							TaskInputRef: "*",
						},
					),
				)
			}
			tamperGovernedKnowledgeCurrentManifestToPermissionOnlyV1(
				t,
				databasePath,
				fixture,
			)
			before := observeGovernedKnowledgeApplyStateV1(
				t,
				databasePath,
				artifactRoot,
				fixture,
			)

			if result, err := runModuleDryRunFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			); err == nil ||
				!strings.Contains(err.Error(), string(moduleApplyFailureStore)) {
				t.Fatalf("damaged %s Dry-run=%+v error=%v", test.name, result, err)
			}
			if after := observeGovernedKnowledgeApplyStateV1(
				t,
				databasePath,
				artifactRoot,
				fixture,
			); !reflect.DeepEqual(after, before) {
				t.Fatalf("damaged %s Dry-run wrote state\nbefore=%+v\nafter=%+v", test.name, before, after)
			}

			if result, err := runModuleApplyFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			); err == nil ||
				!strings.Contains(err.Error(), string(moduleApplyFailureStore)) {
				t.Fatalf("damaged %s Apply=%+v error=%v", test.name, result, err)
			}
			if after := observeGovernedKnowledgeApplyStateV1(
				t,
				databasePath,
				artifactRoot,
				fixture,
			); !reflect.DeepEqual(after, before) {
				t.Fatalf("damaged %s Apply wrote state\nbefore=%+v\nafter=%+v", test.name, before, after)
			}
			assertNoModuleApplyStageResidueV1(t, artifactRoot)
		})
	}
}

func tamperGovernedKnowledgeCurrentManifestToPermissionOnlyV1(
	t *testing.T,
	databasePath string,
	fixture moduleApplyKnowledgeFixtureV1,
) {
	t.Helper()
	original, err := os.ReadFile(filepath.Join(fixture.ArtifactDirectory, "module.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(original)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Requires = nil
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(canonical); err != nil {
		t.Fatalf("permission-only Knowledge Manifest is structurally invalid: %v", err)
	}
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		moduleApplyJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if _, err := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, ?, ?, ?, ?, ?)
	`,
		digest,
		string(currentstore.ContentModuleManifest),
		moduleApplyJSONMediaType,
		canonical,
		len(canonical),
		time.Now().UTC().UnixMicro(),
	); err != nil {
		_ = database.Close()
		t.Fatalf("insert permission-only Knowledge Manifest: %v", err)
	}
	result, err := database.Exec(`
		UPDATE module_installations SET manifest_ref=?
		WHERE module_id=? AND exact_version=? AND artifact_digest=?
	`, digest, fixture.ModuleID, fixture.ExactVersion, fixture.ArtifactDigest)
	assertGovernedKnowledgeCorruptionRowsV1(t, result, err)
	var journalMode string
	journalErr := database.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&journalMode)
	closeErr := database.Close()
	if journalErr != nil || journalMode != "delete" || closeErr != nil {
		t.Fatalf("close permission-only Knowledge corruption: journal=%q/%v close=%v", journalMode, journalErr, closeErr)
	}
}

type governedKnowledgeApplyObservedStateV1 struct {
	PointerRevision     uint64
	ControlSnapshotID   string
	CatalogGenerationID string
	ArtifactEntries     []string
	StoreRows           map[string][]string
	InstallationExists  bool
}

func TestGovernedKnowledgeDependencyPreflightRejectsWithoutWrites(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*controlcontract.ProfileDefinition)
		wantCode moduleApplyFailureCodeV1
	}{
		{
			name:     "missing same Profile Model",
			wantCode: moduleApplyFailureStore,
			mutate: func(profile *controlcontract.ProfileDefinition) {
				bindings := make([]controlcontract.BindingSpec, 0, len(profile.Bindings))
				for _, binding := range profile.Bindings {
					if binding.Port != productionModelPort {
						bindings = append(bindings, binding)
					}
				}
				profile.Bindings = bindings
			},
		},
		{
			name:     "ambiguous same Profile Model",
			wantCode: moduleApplyFailureStore,
			mutate: func(profile *controlcontract.ProfileDefinition) {
				for _, binding := range profile.Bindings {
					if binding.Port != productionModelPort {
						continue
					}
					duplicate := binding
					duplicate.ConfigRef = moduleapi.Digest(
						"freeagent.test.ambiguous-model-config/v1",
						[]byte("ambiguous"),
					)
					profile.Bindings = append(profile.Bindings, duplicate)
					return
				}
				panic("test Profile has no Model Binding to duplicate")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			corruptGovernedKnowledgeTargetModelBindingsV1(
				t,
				databasePath,
				test.mutate,
			)

			fixture := newModuleApplyKnowledgeFixtureVersionV1(t, "2.0.0")
			planCanonical := newEnabledModuleApplyKnowledgePlanV1(
				t,
				fixture,
				1,
				moduleapi.KnowledgeScopeRuleV1{
					TenantID:     defaultTenantID,
					WorkspaceID:  defaultWorkspaceID,
					AgentID:      defaultAgentID,
					TaskInputRef: "*",
				},
			)
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "governed-knowledge.json"),
				planCanonical,
			)
			before := observeGovernedKnowledgeApplyStateV1(
				t,
				databasePath,
				artifactRoot,
				fixture,
			)

			if result, err := runModuleDryRunFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			); err == nil || !strings.Contains(err.Error(), string(test.wantCode)) {
				t.Fatalf("invalid dependency dry-run=%+v error=%v", result, err)
			}
			afterDryRun := observeGovernedKnowledgeApplyStateV1(
				t,
				databasePath,
				artifactRoot,
				fixture,
			)
			if !reflect.DeepEqual(afterDryRun, before) {
				t.Fatalf(
					"invalid dependency Dry-run wrote state:\nbefore=%+v\nafter=%+v",
					before,
					afterDryRun,
				)
			}

			if result, err := runModuleApplyFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			); err == nil || !strings.Contains(err.Error(), string(test.wantCode)) {
				t.Fatalf("invalid dependency Apply=%+v error=%v", result, err)
			}
			afterApply := observeGovernedKnowledgeApplyStateV1(
				t,
				databasePath,
				artifactRoot,
				fixture,
			)
			if !reflect.DeepEqual(afterApply, before) {
				t.Fatalf(
					"invalid dependency Apply wrote state:\nbefore=%+v\nafter=%+v",
					before,
					afterApply,
				)
			}
			assertNoModuleApplyStageResidueV1(t, artifactRoot)
		})
	}
}

func TestGovernedKnowledgeDependencyIdentityChainRejectsWithoutWrites(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *sql.DB, governedKnowledgeModelDependencyFactsV1)
	}{
		{
			name: "Catalog differs from exact Activation",
			mutate: func(
				t *testing.T,
				database *sql.DB,
				facts governedKnowledgeModelDependencyFactsV1,
			) {
				result, err := database.Exec(`
					UPDATE module_activations
					SET adapter_identity=?
					WHERE activation_id=?
				`, facts.Activation.AdapterIdentity+".corrupt", facts.Activation.ActivationID)
				assertGovernedKnowledgeCorruptionRowsV1(t, result, err)
			},
		},
		{
			name: "Activation differs from Installation",
			mutate: func(
				t *testing.T,
				database *sql.DB,
				facts governedKnowledgeModelDependencyFactsV1,
			) {
				otherInstallationID, _ := governedKnowledgeOtherInstallationV1(
					t,
					database,
					facts.Installation.InstallationID,
				)
				result, err := database.Exec(`
					UPDATE module_activations
					SET installation_id=?
					WHERE activation_id=?
				`, otherInstallationID, facts.Activation.ActivationID)
				assertGovernedKnowledgeCorruptionRowsV1(t, result, err)
			},
		},
		{
			name: "Installation differs from stored Manifest",
			mutate: func(
				t *testing.T,
				database *sql.DB,
				facts governedKnowledgeModelDependencyFactsV1,
			) {
				_, otherManifestRef := governedKnowledgeOtherInstallationV1(
					t,
					database,
					facts.Installation.InstallationID,
				)
				result, err := database.Exec(`
					UPDATE module_installations
					SET manifest_ref=?
					WHERE installation_id=?
				`, otherManifestRef, facts.Installation.InstallationID)
				assertGovernedKnowledgeCorruptionRowsV1(t, result, err)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			facts := loadGovernedKnowledgeModelDependencyFactsV1(t, databasePath)
			corruptGovernedKnowledgeModelDependencyChainV1(
				t,
				databasePath,
				facts,
				test.mutate,
			)

			fixture := newModuleApplyKnowledgeFixtureVersionV1(t, "2.0.0")
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "governed-knowledge.json"),
				newEnabledModuleApplyKnowledgePlanV1(
					t,
					fixture,
					1,
					moduleapi.KnowledgeScopeRuleV1{
						TenantID:     defaultTenantID,
						WorkspaceID:  defaultWorkspaceID,
						AgentID:      defaultAgentID,
						TaskInputRef: "*",
					},
				),
			)
			before := observeGovernedKnowledgeApplyStateV1(
				t,
				databasePath,
				artifactRoot,
				fixture,
			)

			if result, err := runModuleDryRunFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			); err == nil || !strings.Contains(err.Error(), string(moduleApplyFailureStore)) {
				t.Fatalf("invalid identity dependency dry-run=%+v error=%v", result, err)
			}
			afterDryRun := observeGovernedKnowledgeApplyStateV1(
				t,
				databasePath,
				artifactRoot,
				fixture,
			)
			if !reflect.DeepEqual(afterDryRun, before) {
				t.Fatalf(
					"invalid identity dependency Dry-run wrote state:\nbefore=%+v\nafter=%+v",
					before,
					afterDryRun,
				)
			}

			if result, err := runModuleApplyFixtureV1(
				databasePath,
				artifactRoot,
				planPath,
				fixture.ArtifactDirectory,
				"",
			); err == nil || !strings.Contains(err.Error(), string(moduleApplyFailureStore)) {
				t.Fatalf("invalid identity dependency Apply=%+v error=%v", result, err)
			}
			afterApply := observeGovernedKnowledgeApplyStateV1(
				t,
				databasePath,
				artifactRoot,
				fixture,
			)
			if !reflect.DeepEqual(afterApply, before) {
				t.Fatalf(
					"invalid identity dependency Apply wrote state:\nbefore=%+v\nafter=%+v",
					before,
					afterApply,
				)
			}
			assertNoModuleApplyStageResidueV1(t, artifactRoot)
		})
	}
}

func TestGovernedKnowledgeDependencyIgnoresNewerUnboundActivation(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	facts := loadGovernedKnowledgeModelDependencyFactsV1(t, databasePath)

	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open governed Knowledge orphan Activation Store: %v", err)
	}
	orphan, activationErr := store.ActivateModule(
		ctx,
		currentstore.ActivateModuleInput{
			ActivationID:       facts.Activation.ActivationID + "-unbound-2",
			TenantID:           facts.Activation.TenantID,
			InstanceID:         facts.Activation.InstanceID,
			InstallationID:     facts.Installation.InstallationID,
			ActivationRevision: facts.Activation.ActivationRevision + 1,
			ExecutionClass:     facts.Activation.ExecutionClass,
			AdapterIdentity:    facts.Activation.AdapterIdentity,
		},
	)
	closeErr := store.Close()
	if activationErr != nil || closeErr != nil ||
		orphan.ActivationRevision != facts.Activation.ActivationRevision+1 {
		t.Fatalf(
			"create governed Knowledge orphan Activation=%+v error=%v close=%v",
			orphan,
			activationErr,
			closeErr,
		)
	}
	closeGovernedKnowledgeSQLiteForObserverV1(t, databasePath)

	fixture := newModuleApplyKnowledgeFixtureVersionV1(t, "2.0.0")
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "governed-knowledge.json"),
		newEnabledModuleApplyKnowledgePlanV1(
			t,
			fixture,
			1,
			moduleapi.KnowledgeScopeRuleV1{
				TenantID:     defaultTenantID,
				WorkspaceID:  defaultWorkspaceID,
				AgentID:      defaultAgentID,
				TaskInputRef: "*",
			},
		),
	)
	dryRun, err := runModuleDryRunFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || dryRun.Status != moduleApplyStatusWouldApply {
		t.Fatalf("governed Knowledge with orphan Activation dry-run=%+v error=%v", dryRun, err)
	}
	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
	)
	if err != nil || applied.Status != moduleApplyStatusApplied || applied.PointerRevision != 2 {
		t.Fatalf("governed Knowledge with orphan Activation Apply=%+v error=%v", applied, err)
	}

	store, err = currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("reopen governed Knowledge orphan Activation Store: %v", err)
	}
	_, _, catalog, loadErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	entry, found := catalog.FindInstance(facts.Activation.InstanceID)
	var exact currentstore.ModuleActivation
	var exactErr error
	if found {
		exact, exactErr = store.GetModuleActivationByIdentity(
			ctx,
			defaultTenantID,
			facts.Activation.InstanceID,
			entry.Activation.ActivationRevision,
		)
	}
	latest, latestErr := store.GetLatestModuleActivationForInstance(
		ctx,
		defaultTenantID,
		facts.Activation.InstanceID,
	)
	closeErr = store.Close()
	if loadErr != nil || !found || exactErr != nil || latestErr != nil || closeErr != nil ||
		entry.Activation.ActivationRevision != facts.Activation.ActivationRevision ||
		exact.ActivationID != facts.Activation.ActivationID ||
		latest.ActivationID != orphan.ActivationID {
		t.Fatalf(
			"governed Knowledge exact/orphan closure: entry=%+v found=%v exact=%+v latest=%+v errors=%v",
			entry,
			found,
			exact,
			latest,
			errors.Join(loadErr, exactErr, latestErr, closeErr),
		)
	}
}

func closeGovernedKnowledgeSQLiteForObserverV1(
	t *testing.T,
	databasePath string,
) {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
	if err != nil {
		t.Fatalf("open governed Knowledge Store for observer handoff: %v", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	var journalMode string
	journalModeErr := database.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&journalMode)
	closeErr := database.Close()
	if journalModeErr != nil || journalMode != "delete" || closeErr != nil {
		t.Fatalf(
			"close governed Knowledge Store for observer: journal=%q/%v close=%v",
			journalMode,
			journalModeErr,
			closeErr,
		)
	}
}

func TestGovernedKnowledgeDependencyPreflightRunsBeforeAnyStaging(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyKnowledgeFixtureVersionV1(t, "2.0.0")
	plan, canonical, digest, err := restoreModuleApplyPlanV1(
		newEnabledModuleApplyKnowledgePlanV1(
			t,
			fixture,
			1,
			moduleapi.KnowledgeScopeRuleV1{
				TenantID:     defaultTenantID,
				WorkspaceID:  defaultWorkspaceID,
				AgentID:      defaultAgentID,
				TaskInputRef: "*",
			},
		),
	)
	if err != nil {
		t.Fatalf("restore governed Knowledge staging-order plan: %v", err)
	}
	input := moduleApplyCommandInputV1{
		DatabasePath:      databasePath,
		ArtifactRoot:      artifactRoot,
		ArtifactDirectory: fixture.ArtifactDirectory,
		Plan:              plan,
		PlanCanonical:     canonical,
		PlanDigest:        digest,
	}

	ctx := context.Background()
	observer, err := currentstore.OpenReadOnlyObserver(ctx, databasePath)
	if err != nil {
		t.Fatalf("open governed Knowledge staging-order observer: %v", err)
	}
	basis, control, catalog, err := observer.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		_ = observer.Close()
		t.Fatalf("load governed Knowledge staging-order observer basis: %v", err)
	}
	removeGovernedKnowledgeTargetModelBindingV1(t, &control)
	dryRunTempCalled := false
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
				dryRunTempCalled = true
				return "", errors.New("unexpected governed Knowledge TEMP creation")
			},
			removeAll: func(string) error {
				return errors.New("unexpected governed Knowledge TEMP cleanup")
			},
			lstat: func(string) (os.FileInfo, error) {
				return nil, errors.New("unexpected governed Knowledge TEMP inspection")
			},
		},
	)
	observerCloseErr := observer.Close()
	if moduleApplyFailureCodeOfV1(dryRunErr) != moduleApplyFailureTarget ||
		dryRunTempCalled || observerCloseErr != nil {
		t.Fatalf(
			"governed Knowledge Dry-run staging order: code=%s temp_called=%v error=%v close=%v",
			moduleApplyFailureCodeOfV1(dryRunErr),
			dryRunTempCalled,
			dryRunErr,
			observerCloseErr,
		)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open governed Knowledge staging-order writer: %v", err)
	}
	basis, control, catalog, err = store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		_ = store.Close()
		t.Fatalf("load governed Knowledge staging-order writer basis: %v", err)
	}
	removeGovernedKnowledgeTargetModelBindingV1(t, &control)
	applyStageCalled := false
	_, applyErr := prepareAndStageEnabledModuleV1(
		ctx,
		store,
		artifactRoot,
		moduleApplyStageFilesystemV1{
			mkdirTemp: func(string, string) (string, error) {
				applyStageCalled = true
				return "", errors.New("unexpected governed Knowledge stage creation")
			},
			removeAll: func(string) error {
				return errors.New("unexpected governed Knowledge stage cleanup")
			},
			syncDirectory: func(string) error {
				return errors.New("unexpected governed Knowledge artifact-root sync")
			},
		},
		input,
		basis,
		control,
		catalog,
	)
	storeCloseErr := store.Close()
	if moduleApplyFailureCodeOfV1(applyErr) != moduleApplyFailureTarget ||
		applyStageCalled || storeCloseErr != nil {
		t.Fatalf(
			"governed Knowledge Apply staging order: code=%s stage_called=%v error=%v close=%v",
			moduleApplyFailureCodeOfV1(applyErr),
			applyStageCalled,
			applyErr,
			storeCloseErr,
		)
	}
}

func removeGovernedKnowledgeTargetModelBindingV1(
	t *testing.T,
	control *controlcontract.ControlSnapshot,
) {
	t.Helper()
	for profileIndex := range control.Profiles {
		if control.Profiles[profileIndex].Profile.ID != moduleApplyTestProfileID {
			continue
		}
		bindings := make(
			[]controlcontract.BindingSpec,
			0,
			len(control.Profiles[profileIndex].Bindings),
		)
		for _, binding := range control.Profiles[profileIndex].Bindings {
			if binding.Port != productionModelPort {
				bindings = append(bindings, binding)
			}
		}
		if len(bindings) == len(control.Profiles[profileIndex].Bindings) {
			t.Fatal("governed Knowledge staging-order Profile has no Model Binding")
		}
		control.Profiles[profileIndex].Bindings = bindings
		return
	}
	t.Fatal("governed Knowledge staging-order target Profile is absent")
}

type governedKnowledgeModelDependencyFactsV1 struct {
	Activation   currentstore.ModuleActivation
	Installation currentstore.ModuleInstallation
}

func loadGovernedKnowledgeModelDependencyFactsV1(
	t *testing.T,
	databasePath string,
) governedKnowledgeModelDependencyFactsV1 {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open governed Knowledge dependency Store: %v", err)
	}
	_, control, catalog, loadErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	profile, found := control.FindProfile(moduleApplyTestProfileID)
	if loadErr != nil || !found {
		_ = store.Close()
		t.Fatalf("load governed Knowledge target Profile: found=%v error=%v", found, loadErr)
	}
	var modelBinding *controlcontract.BindingSpec
	for index := range profile.Bindings {
		if profile.Bindings[index].Port == productionModelPort {
			copy := profile.Bindings[index]
			modelBinding = &copy
			break
		}
	}
	if modelBinding == nil {
		_ = store.Close()
		t.Fatal("governed Knowledge target Profile has no Model Binding")
	}
	entry, found := catalog.FindInstance(modelBinding.InstanceID)
	if !found {
		_ = store.Close()
		t.Fatal("governed Knowledge Model Binding has no Catalog entry")
	}
	activation, activationErr := store.GetLatestModuleActivationForInstance(
		ctx,
		defaultTenantID,
		modelBinding.InstanceID,
	)
	installation, installationErr := store.GetModuleInstallationByIdentity(
		ctx,
		entry.Activation.ModuleID,
		entry.Activation.Version,
	)
	closeErr := store.Close()
	if activationErr != nil || installationErr != nil || closeErr != nil {
		t.Fatalf(
			"load governed Knowledge Model dependency: %v",
			errors.Join(activationErr, installationErr, closeErr),
		)
	}
	return governedKnowledgeModelDependencyFactsV1{
		Activation:   activation,
		Installation: installation,
	}
}

func corruptGovernedKnowledgeModelDependencyChainV1(
	t *testing.T,
	databasePath string,
	facts governedKnowledgeModelDependencyFactsV1,
	mutate func(*testing.T, *sql.DB, governedKnowledgeModelDependencyFactsV1),
) {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
	if err != nil {
		t.Fatalf("open governed Knowledge dependency corruption Store: %v", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	mutate(t, database, facts)
	var journalMode string
	journalModeErr := database.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&journalMode)
	closeErr := database.Close()
	if journalModeErr != nil || journalMode != "delete" || closeErr != nil {
		t.Fatalf(
			"close governed Knowledge dependency corruption: journal=%q/%v close=%v",
			journalMode,
			journalModeErr,
			closeErr,
		)
	}
}

func governedKnowledgeOtherInstallationV1(
	t *testing.T,
	database *sql.DB,
	excludedInstallationID string,
) (string, string) {
	t.Helper()
	var installationID, manifestRef string
	if err := database.QueryRow(`
		SELECT installation_id, manifest_ref
		FROM module_installations
		WHERE installation_id<>?
		ORDER BY installation_id
		LIMIT 1
	`, excludedInstallationID).Scan(&installationID, &manifestRef); err != nil {
		t.Fatalf("read alternate governed Knowledge dependency Installation: %v", err)
	}
	return installationID, manifestRef
}

func assertGovernedKnowledgeCorruptionRowsV1(
	t *testing.T,
	result sql.Result,
	err error,
) {
	t.Helper()
	if rows := governedKnowledgeRowsAffectedV1(result, err); rows != 1 {
		t.Fatalf("governed Knowledge dependency corruption error=%v rows=%d", err, rows)
	}
}

func observeGovernedKnowledgeApplyStateV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	fixture moduleApplyKnowledgeFixtureV1,
) governedKnowledgeApplyObservedStateV1 {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatalf("open governed Knowledge Store observation: %v", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	var state governedKnowledgeApplyObservedStateV1
	if err := database.QueryRowContext(context.Background(), `
		SELECT pointer_revision, snapshot_id, catalog_generation_id
		FROM control_current
		WHERE tenant_id=?
	`, defaultTenantID).Scan(
		&state.PointerRevision,
		&state.ControlSnapshotID,
		&state.CatalogGenerationID,
	); err != nil {
		_ = database.Close()
		t.Fatalf("read governed Knowledge publication identity: %v", err)
	}
	var installationCount int64
	if err := database.QueryRowContext(context.Background(), `
		SELECT COUNT(*)
		FROM module_installations
		WHERE module_id=? AND exact_version=?
	`, fixture.ModuleID, fixture.ExactVersion).Scan(&installationCount); err != nil {
		_ = database.Close()
		t.Fatalf("read governed Knowledge Installation count: %v", err)
	}
	if installationCount < 0 || installationCount > 1 {
		_ = database.Close()
		t.Fatalf("governed Knowledge Installation count=%d", installationCount)
	}
	state.InstallationExists = installationCount == 1
	state.StoreRows = snapshotGovernedKnowledgeStoreRowsV1(t, database)
	if err := database.Close(); err != nil {
		t.Fatalf("close governed Knowledge Store observation: %v", err)
	}
	entries, err := os.ReadDir(artifactRoot)
	if err != nil {
		t.Fatalf("read governed Knowledge artifact root: %v", err)
	}
	state.ArtifactEntries = make([]string, 0, len(entries))
	for _, entry := range entries {
		state.ArtifactEntries = append(state.ArtifactEntries, entry.Name())
	}
	return state
}

func snapshotGovernedKnowledgeStoreRowsV1(
	t *testing.T,
	database *sql.DB,
) map[string][]string {
	t.Helper()
	tableRows, err := database.QueryContext(context.Background(), `
		SELECT name
		FROM sqlite_schema
		WHERE type='table' AND name NOT GLOB 'sqlite_*'
		ORDER BY name
	`)
	if err != nil {
		t.Fatalf("list governed Knowledge Store tables: %v", err)
	}
	tables := make([]string, 0)
	for tableRows.Next() {
		var table string
		if err := tableRows.Scan(&table); err != nil {
			_ = tableRows.Close()
			t.Fatalf("scan governed Knowledge Store table: %v", err)
		}
		tables = append(tables, table)
	}
	if err := tableRows.Err(); err != nil {
		_ = tableRows.Close()
		t.Fatalf("iterate governed Knowledge Store tables: %v", err)
	}
	if err := tableRows.Close(); err != nil {
		t.Fatalf("close governed Knowledge Store table list: %v", err)
	}

	snapshot := make(map[string][]string, len(tables))
	for _, table := range tables {
		quotedTable := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		rows, err := database.QueryContext(
			context.Background(),
			"SELECT * FROM "+quotedTable,
		)
		if err != nil {
			t.Fatalf("read governed Knowledge Store table %s: %v", table, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			t.Fatalf("read governed Knowledge Store columns %s: %v", table, err)
		}
		encodedRows := make([]string, 0)
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(values))
			for index := range values {
				destinations[index] = &values[index]
			}
			if err := rows.Scan(destinations...); err != nil {
				_ = rows.Close()
				t.Fatalf("scan governed Knowledge Store row %s: %v", table, err)
			}
			fields := make([]string, len(values))
			for index, value := range values {
				fields[index] = encodeGovernedKnowledgeStoreFieldV1(value)
			}
			encodedRows = append(encodedRows, strings.Join(fields, "\x1f"))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatalf("iterate governed Knowledge Store table %s: %v", table, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close governed Knowledge Store table %s: %v", table, err)
		}
		sort.Strings(encodedRows)
		snapshot[table] = encodedRows
	}
	return snapshot
}

func encodeGovernedKnowledgeStoreFieldV1(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case int64:
		return fmt.Sprintf("int64:%d", typed)
	case float64:
		return fmt.Sprintf("float64:%016x", math.Float64bits(typed))
	case bool:
		return fmt.Sprintf("bool:%t", typed)
	case string:
		return "string:" + hex.EncodeToString([]byte(typed))
	case []byte:
		return "bytes:" + hex.EncodeToString(typed)
	default:
		return fmt.Sprintf("%T:%v", value, value)
	}
}

func corruptGovernedKnowledgeTargetModelBindingsV1(
	t *testing.T,
	databasePath string,
	mutate func(*controlcontract.ProfileDefinition),
) {
	t.Helper()
	ctx := context.Background()
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
		t.Fatalf("load target publication: %v", loadErr)
	}
	profileIndex := -1
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID == moduleApplyTestProfileID {
			profileIndex = index
			break
		}
	}
	if profileIndex < 0 {
		t.Fatal("target Profile is absent")
	}
	mutate(&control.Profiles[profileIndex])
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze corrupted dependency Control: %v", err)
	}
	if controlRef.SnapshotID != basis.Control.SnapshotID ||
		controlRef.Revision != basis.Control.Revision ||
		controlRef.Digest == basis.Control.Digest {
		t.Fatalf("corrupted Control ref=%+v current=%+v", controlRef, basis.Control)
	}
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze corrupted dependency Catalog: %v", err)
	}
	if catalogRef.GenerationID != basis.Catalog.GenerationID ||
		catalogRef.Generation != basis.Catalog.Generation ||
		catalogRef.Digest == basis.Catalog.Digest {
		t.Fatalf("corrupted Catalog ref=%+v current=%+v", catalogRef, basis.Catalog)
	}

	var controlResult, catalogResult sql.Result
	var journalMode string
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
			`, controlCanonical, controlRef.Digest, controlRef.SnapshotID, controlRef.Revision)
			catalogResult, catalogUpdateErr = connection.ExecContext(ctx, `
				UPDATE runtime_catalog_generations
				SET canonical_json=?, digest=?
				WHERE generation_id=? AND generation=?
			`, catalogCanonical, catalogRef.Digest, catalogRef.GenerationID, catalogRef.Generation)
			journalModeErr := connection.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&journalMode)
			return errors.Join(controlUpdateErr, catalogUpdateErr, journalModeErr)
		},
	)
	controlRows := governedKnowledgeRowsAffectedV1(controlResult, nil)
	catalogRows := governedKnowledgeRowsAffectedV1(catalogResult, nil)
	if journalMode != "delete" || controlRows != 1 || catalogRows != 1 {
		t.Fatalf(
			"corrupt dependency publication rows: control=%d catalog=%d journal=%q",
			controlRows,
			catalogRows,
			journalMode,
		)
	}
}

func governedKnowledgeRowsAffectedV1(result sql.Result, err error) int64 {
	if err != nil || result == nil {
		return -1
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return -1
	}
	return rows
}
