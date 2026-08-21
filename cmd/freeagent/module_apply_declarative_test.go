package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleApplyRoleID       = "freeagent.example.role.architect"
	moduleApplyRoleInstance = "role-architect-applied"
	moduleApplyRoleText     = "Role: prioritize clear architecture, explicit trade-offs, and coherent system structure."

	moduleApplySkillID       = "freeagent.example.skill.implementation"
	moduleApplySkillInstance = "skill-implementation-applied"
	moduleApplySkillText     = "Skill: implement concrete details precisely and keep changes scoped to the requested work."

	moduleApplyBasicContextInstance = "context-basic"
	moduleApplyBasicContextText     = "You are FreeAgent, a helpful assistant."

	moduleApplyRoleWorkspace  = "workspace-role"
	moduleApplySkillWorkspace = "workspace-skill"
	moduleApplyRoleProfile    = "profile-role"
	moduleApplySkillProfile   = "profile-skill"
)

type moduleApplyDeclarativeFixtureV1 struct {
	ModuleID          string
	InstanceID        string
	ArtifactDirectory string
	ArtifactDigest    string
	ArtifactSizeBytes uint64
}

func TestModuleApplyDeclarativeRoleSkillApplyDisableLifecycle(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	role := newModuleApplyDeclarativeFixtureV1(t, moduleApplyRoleID, moduleApplyRoleInstance)
	skill := newModuleApplyDeclarativeFixtureV1(t, moduleApplySkillID, moduleApplySkillInstance)

	roleEnablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, role, 1, 0),
	)
	roleEnabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		roleEnablePath,
		role.ArtifactDirectory,
		"",
	)
	if err != nil {
		t.Fatalf("apply declarative Role: %v", err)
	}
	assertDeclarativeModuleApplyResultV1(
		t, roleEnabled, role, moduleApplyStatusApplied, moduleApplyEnabledV1, 2,
	)

	skillEnablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-skill.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, skill, 2, 1),
	)
	skillEnabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		skillEnablePath,
		skill.ArtifactDirectory,
		"",
	)
	if err != nil {
		t.Fatalf("apply declarative Skill: %v", err)
	}
	assertDeclarativeModuleApplyResultV1(
		t, skillEnabled, skill, moduleApplyStatusApplied, moduleApplyEnabledV1, 3,
	)
	assertModuleApplyContextStateV1(t, databasePath, []moduleApplyDeclarativeFixtureV1{role, skill})
	roleSkillRunID := assertModuleApplyContextChatV1(
		t,
		databasePath,
		artifactRoot,
		"module-apply-role-skill",
		"build the smallest viable feature",
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyRoleText},
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplySkillText},
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyBasicContextText},
			{Role: moduleapi.ModelRoleUser, Content: "build the smallest viable feature"},
		},
	)

	roleDisablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-role.json"),
		newDisabledDeclarativeModuleApplyPlanV1(t, role.InstanceID, 3),
	)
	roleDisabled, err := runModuleApplyFixtureV1(
		databasePath, artifactRoot, roleDisablePath, "", "",
	)
	if err != nil {
		t.Fatalf("disable declarative Role: %v", err)
	}
	assertDeclarativeModuleApplyResultV1(
		t, roleDisabled, role, moduleApplyStatusApplied, moduleApplyDisabledV1, 4,
	)
	roleDisableRetry, err := runModuleApplyFixtureV1(
		databasePath, artifactRoot, roleDisablePath, "", "",
	)
	if err != nil {
		t.Fatalf("exact declarative Role DISABLE retry: %v", err)
	}
	assertDeclarativeModuleApplyResultV1(
		t,
		roleDisableRetry,
		role,
		moduleApplyStatusAlreadyApplied,
		moduleApplyDisabledV1,
		4,
	)
	if roleDisableRetry.PlanDigest != roleDisabled.PlanDigest ||
		roleDisableRetry.ControlSnapshotID != roleDisabled.ControlSnapshotID ||
		roleDisableRetry.CatalogGenerationID != roleDisabled.CatalogGenerationID {
		t.Fatalf("exact Role DISABLE retry changed publication identity: first=%+v retry=%+v", roleDisabled, roleDisableRetry)
	}
	assertFrozenModuleApplyContextInstancesV1(
		t,
		databasePath,
		roleSkillRunID,
		[]string{moduleApplyRoleInstance, moduleApplySkillInstance, moduleApplyBasicContextInstance},
	)
	assertModuleApplyContextStateV1(t, databasePath, []moduleApplyDeclarativeFixtureV1{skill})
	skillRunID := assertModuleApplyContextChatV1(
		t,
		databasePath,
		artifactRoot,
		"module-apply-skill-only",
		"implement the detail",
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplySkillText},
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyBasicContextText},
			{Role: moduleapi.ModelRoleUser, Content: "implement the detail"},
		},
	)

	skillDisablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-skill.json"),
		newDisabledDeclarativeModuleApplyPlanV1(t, skill.InstanceID, 4),
	)
	skillDisabled, err := runModuleApplyFixtureV1(
		databasePath, artifactRoot, skillDisablePath, "", "",
	)
	if err != nil {
		t.Fatalf("disable declarative Skill: %v", err)
	}
	assertDeclarativeModuleApplyResultV1(
		t, skillDisabled, skill, moduleApplyStatusApplied, moduleApplyDisabledV1, 5,
	)
	assertFrozenModuleApplyContextInstancesV1(
		t,
		databasePath,
		skillRunID,
		[]string{moduleApplySkillInstance, moduleApplyBasicContextInstance},
	)
	assertModuleApplyContextStateV1(t, databasePath, nil)
	assertModuleApplyContextChatV1(
		t,
		databasePath,
		artifactRoot,
		"module-apply-pure-chat-restored",
		"plain chat",
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyBasicContextText},
			{Role: moduleapi.ModelRoleUser, Content: "plain chat"},
		},
	)
	assertDeclarativeModuleHistoryRetainedV1(t, databasePath, artifactRoot, role, skill)
}

func TestModuleApplyReconcileCommittedDeclarativeArtifactDriftIsUnknown(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	role := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	planCanonical := newEnabledDeclarativeModuleApplyPlanV1(t, role, 1, 0)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		planCanonical,
	)
	if _, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		role.ArtifactDirectory,
		"",
	); err != nil {
		t.Fatalf("apply declarative Role: %v", err)
	}
	plan, _, planDigest, err := restoreModuleApplyPlanV1(planCanonical)
	if err != nil {
		t.Fatalf("restore declarative Role plan: %v", err)
	}

	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Current Store: %v", err)
	}
	defer store.Close()
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatalf("load applied publication: %v", err)
	}
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze applied Control: %v", err)
	}
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze applied Catalog: %v", err)
	}
	publication := currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: basis.PointerRevision - 1,
		NewPointerRevision:      basis.PointerRevision,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	}

	finalArtifact := filepath.Join(artifactRoot, role.ArtifactDigest)
	manifestCanonical, err := os.ReadFile(filepath.Join(
		finalArtifact,
		moduleapi.ArtifactManifestPath,
	))
	if err != nil {
		t.Fatalf("read published declarative manifest: %v", err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		t.Fatalf("restore published declarative manifest: %v", err)
	}
	entrypoint := filepath.Join(
		finalArtifact,
		filepath.FromSlash(manifest.Runtime.Entrypoint),
	)
	if err := os.WriteFile(
		entrypoint,
		[]byte(`{"schema_version":"static-context/v1","text":"tampered after commit"}`),
		0o600,
	); err != nil {
		t.Fatalf("tamper committed declarative entrypoint: %v", err)
	}

	_, err = reconcileModulePublicationV1(
		ctx,
		store,
		artifactRoot,
		plan,
		planDigest,
		publication,
		errors.New("simulated ambiguous PublishControlCatalog return"),
	)
	if code := moduleApplyFailureCodeOfV1(err); code != moduleApplyFailureOutcomeUnknown {
		t.Fatalf("committed artifact drift reconciliation code=%q error=%v", code, err)
	}
}

func TestModuleApplyDeclarativeBindingsAreIsolatedByWorkspaceProfileAssembly(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializeMultiProfileForModuleApplyV1(t, root, databasePath, artifactRoot)

	role := newModuleApplyDeclarativeFixtureV1(t, moduleApplyRoleID, moduleApplyRoleInstance)
	skill := newModuleApplyDeclarativeFixtureV1(t, moduleApplySkillID, moduleApplySkillInstance)
	rolePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role-profile.json"),
		newNamedEnabledDeclarativeModuleApplyPlanV1(
			t, role, moduleApplyRoleProfile, 1, 0,
		),
	)
	roleResult, err := runModuleApplyFixtureV1(
		databasePath, artifactRoot, rolePlan, role.ArtifactDirectory, "",
	)
	if err != nil || roleResult.Status != moduleApplyStatusApplied ||
		roleResult.BindingTarget.ProfileID != moduleApplyRoleProfile || roleResult.PointerRevision != 2 {
		t.Fatalf("apply Role to isolated Profile: result=%+v err=%v", roleResult, err)
	}

	skillPlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-skill-profile.json"),
		newNamedEnabledDeclarativeModuleApplyPlanV1(
			t, skill, moduleApplySkillProfile, 2, 0,
		),
	)
	skillResult, err := runModuleApplyFixtureV1(
		databasePath, artifactRoot, skillPlan, skill.ArtifactDirectory, "",
	)
	if err != nil || skillResult.Status != moduleApplyStatusApplied ||
		skillResult.BindingTarget.ProfileID != moduleApplySkillProfile || skillResult.PointerRevision != 3 {
		t.Fatalf("apply Skill to isolated Profile: result=%+v err=%v", skillResult, err)
	}

	assertModuleApplyProfileContextInstancesV1(
		t,
		databasePath,
		moduleApplyRoleProfile,
		[]string{moduleApplyRoleInstance, moduleApplyBasicContextInstance},
	)
	assertModuleApplyProfileContextInstancesV1(
		t,
		databasePath,
		moduleApplySkillProfile,
		[]string{moduleApplySkillInstance, moduleApplyBasicContextInstance},
	)
	assertModuleApplyProfileContextInstancesV1(
		t,
		databasePath,
		moduleApplyTestProfileID,
		[]string{moduleApplyBasicContextInstance},
	)

	assertNamedModuleApplyContextChatV1(
		t,
		databasePath,
		artifactRoot,
		moduleApplyRoleWorkspace,
		moduleApplyRoleProfile,
		"module-apply-role-workspace",
		"review the architecture",
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyRoleText},
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyBasicContextText},
			{Role: moduleapi.ModelRoleUser, Content: "review the architecture"},
		},
	)
	assertNamedModuleApplyContextChatV1(
		t,
		databasePath,
		artifactRoot,
		moduleApplySkillWorkspace,
		moduleApplySkillProfile,
		"module-apply-skill-workspace",
		"implement the feature",
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplySkillText},
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyBasicContextText},
			{Role: moduleapi.ModelRoleUser, Content: "implement the feature"},
		},
	)
}

func TestModuleApplyDeclarativeBackupRestorePreservesBindingsAndStaticContext(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	role := newModuleApplyDeclarativeFixtureV1(t, moduleApplyRoleID, moduleApplyRoleInstance)
	skill := newModuleApplyDeclarativeFixtureV1(t, moduleApplySkillID, moduleApplySkillInstance)
	for index, fixture := range []moduleApplyDeclarativeFixtureV1{role, skill} {
		planPath := writeModuleApplyPlanFixtureV1(
			t,
			filepath.Join(root, "enable-backup-"+fixture.InstanceID+".json"),
			newEnabledDeclarativeModuleApplyPlanV1(
				t, fixture, uint64(index+1), uint32(index),
			),
		)
		if _, err := runModuleApplyFixtureV1(
			databasePath, artifactRoot, planPath, fixture.ArtifactDirectory, "",
		); err != nil {
			t.Fatalf("apply declarative fixture %q before backup: %v", fixture.ModuleID, err)
		}
	}

	beforeBasis, beforeControl, beforeCatalog := loadModuleApplyPublishedStateV1(
		t, databasePath,
	)
	beforeStatic := loadModuleApplyStaticContextBytesV1(
		t, databasePath, beforeControl, moduleApplyTestProfileID,
	)
	bundlePath := filepath.Join(root, "declarative.bundle")
	createAndVerifyModuleApplyBundleV1(
		t, databasePath, artifactRoot, bundlePath,
	)
	restoredDatabase, restoredArtifacts := restoreModuleApplyBundleV1(
		t,
		bundlePath,
		filepath.Join(root, "declarative-restored"),
	)
	afterBasis, afterControl, afterCatalog := loadModuleApplyPublishedStateV1(
		t, restoredDatabase,
	)
	afterStatic := loadModuleApplyStaticContextBytesV1(
		t, restoredDatabase, afterControl, moduleApplyTestProfileID,
	)
	if beforeBasis != afterBasis ||
		!reflect.DeepEqual(beforeControl, afterControl) ||
		!reflect.DeepEqual(beforeCatalog, afterCatalog) ||
		!reflect.DeepEqual(beforeStatic, afterStatic) {
		t.Fatalf(
			"declarative backup/restore drifted: basis before=%+v after=%+v static before=%v after=%v",
			beforeBasis,
			afterBasis,
			beforeStatic,
			afterStatic,
		)
	}
	assertModuleApplyContextStateV1(
		t,
		restoredDatabase,
		[]moduleApplyDeclarativeFixtureV1{role, skill},
	)
	assertModuleApplyContextChatV1(
		t,
		restoredDatabase,
		restoredArtifacts,
		"module-apply-declarative-restored",
		"verify restored context",
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyRoleText},
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplySkillText},
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyBasicContextText},
			{Role: moduleapi.ModelRoleUser, Content: "verify restored context"},
		},
	)
}

func initializeMultiProfileForModuleApplyV1(
	t *testing.T,
	root string,
	databasePath string,
	artifactRoot string,
) {
	t.Helper()
	sourceSeedPath := exampleSeedPath(t)
	payload, err := os.ReadFile(sourceSeedPath)
	if err != nil {
		t.Fatal(err)
	}
	var seed map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&seed); err != nil {
		t.Fatal(err)
	}
	definitions, ok := seed["definitions"].(map[string]any)
	if !ok {
		t.Fatal("example seed definitions are absent")
	}
	definitions["additional_workspaces"] = []any{
		map[string]any{
			"id":                  moduleApplyRoleWorkspace,
			"version":             "1",
			"body":                map[string]any{"name": "Role Workspace"},
			"budget_policy_alias": "budget-local-pure-chat",
		},
		map[string]any{
			"id":                  moduleApplySkillWorkspace,
			"version":             "1",
			"body":                map[string]any{"name": "Skill Workspace"},
			"budget_policy_alias": "budget-local-pure-chat",
		},
	}
	definitions["additional_profiles"] = []any{
		map[string]any{
			"id":                      moduleApplyRoleProfile,
			"version":                 "1",
			"body":                    map[string]any{"mode": "PURE_CHAT", "name": "Role Profile"},
			"context_policy_alias":    "context-pure-chat",
			"cost_policy_alias":       "cost-local-echo",
			"scheduling_policy_alias": "scheduling-single-member",
		},
		map[string]any{
			"id":                      moduleApplySkillProfile,
			"version":                 "1",
			"body":                    map[string]any{"mode": "PURE_CHAT", "name": "Skill Profile"},
			"context_policy_alias":    "context-pure-chat",
			"cost_policy_alias":       "cost-local-echo",
			"scheduling_policy_alias": "scheduling-single-member",
		},
	}
	encoded, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	seedRoot := filepath.Join(root, "multi-profile-seed")
	for _, moduleID := range []string{
		"freeagent.builtin.model.echo",
		"freeagent.builtin.context.basic",
	} {
		copyModuleApplyTestTreeV1(
			t,
			filepath.Join(filepath.Dir(sourceSeedPath), "bootstrap-artifacts", moduleID, "1.0.0"),
			filepath.Join(seedRoot, "bootstrap-artifacts", moduleID, "1.0.0"),
		)
	}
	seedPath := filepath.Join(seedRoot, "current-v1.bootstrap.seed.json")
	if err := os.WriteFile(seedPath, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	initialized, err := initializeProductionData(
		context.Background(),
		initInput{
			DatabasePath: databasePath,
			SeedPath:     seedPath,
			ArtifactRoot: artifactRoot,
		},
	)
	if err != nil {
		t.Fatalf("initialize multi-Profile module-apply fixture: %v", err)
	}
	if initialized.Defaults.AgentID != defaultAgentID || len(initialized.ArtifactLocks) != 2 {
		t.Fatalf("multi-Profile initialization=%+v", initialized)
	}
}

func copyModuleApplyTestTreeV1(t *testing.T, source string, target string) {
	t.Helper()
	err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		if !info.Mode().IsRegular() {
			return errors.New("module-apply test seed contains a non-ordinary file")
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o600)
	})
	if err != nil {
		t.Fatalf("copy module-apply test artifact tree: %v", err)
	}
}

func assertModuleApplyProfileContextInstancesV1(
	t *testing.T,
	databasePath string,
	profileID string,
	want []string,
) {
	t.Helper()
	bindings := loadModuleApplyProfileBindingsV1(t, databasePath, profileID)
	got := make([]string, 0)
	for _, binding := range bindings {
		if binding.Port == productionContextPort {
			got = append(got, binding.InstanceID)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Profile %q Context instances=%v want=%v", profileID, got, want)
	}
}

func loadModuleApplyPublishedStateV1(
	t *testing.T,
	databasePath string,
) (
	controlcontract.PublishedBasis,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, readErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("load module-apply published state: %v", errors.Join(readErr, closeErr))
	}
	return basis, control, catalog
}

func loadModuleApplyStaticContextBytesV1(
	t *testing.T,
	databasePath string,
	control controlcontract.ControlSnapshot,
	profileID string,
) map[string]string {
	t.Helper()
	profile, found := findModuleApplyProfileV1(control, profileID)
	if !found {
		t.Fatalf("Profile %q is absent", profileID)
	}
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	contents := make(map[string]string)
	for _, binding := range profile.Bindings {
		if binding.Port != productionContextPort {
			continue
		}
		for _, ref := range binding.StaticContextRefs {
			record, err := store.GetContent(ctx, ref)
			if err != nil || record.Kind != currentstore.ContentStaticContext ||
				record.MediaType != moduleApplyJSONMediaType {
				t.Fatalf("load STATIC_CONTEXT %q: record=%+v err=%v", ref, record, err)
			}
			contents[ref] = string(record.CanonicalBytes)
		}
	}
	return contents
}

func newModuleApplyDeclarativeFixtureV1(
	t *testing.T,
	moduleID string,
	instanceID string,
) moduleApplyDeclarativeFixtureV1 {
	t.Helper()
	directory := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"bootstrap-artifacts",
		moduleID,
		"1.0.0",
	)
	manifest, err := os.ReadFile(filepath.Join(directory, moduleapi.ArtifactManifestPath))
	if err != nil {
		t.Fatalf("read declarative artifact manifest: %v", err)
	}
	_, canonical, err := moduleapi.ParseModuleManifestV1(manifest)
	if err != nil || !bytes.Equal(canonical, manifest) {
		t.Fatalf("declarative artifact manifest is not exact canonical JSON: %v", err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		directory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatalf("scan declarative artifact: %v", err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(canonical, files)
	if err != nil {
		t.Fatalf("digest declarative artifact: %v", err)
	}
	size := uint64(len(canonical))
	for _, file := range files {
		size += uint64(len(file.Content))
	}
	return moduleApplyDeclarativeFixtureV1{
		ModuleID:          moduleID,
		InstanceID:        instanceID,
		ArtifactDirectory: directory,
		ArtifactDigest:    digest,
		ArtifactSizeBytes: size,
	}
}

func newEnabledDeclarativeModuleApplyPlanV1(
	t *testing.T,
	fixture moduleApplyDeclarativeFixtureV1,
	expectedPointer uint64,
	portBindingIndex uint32,
) []byte {
	t.Helper()
	return newNamedEnabledDeclarativeModuleApplyPlanV1(
		t,
		fixture,
		moduleApplyTestProfileID,
		expectedPointer,
		portBindingIndex,
	)
}

func newNamedEnabledDeclarativeModuleApplyPlanV1(
	t *testing.T,
	fixture moduleApplyDeclarativeFixtureV1,
	profileID string,
	expectedPointer uint64,
	portBindingIndex uint32,
) []byte {
	t.Helper()
	_, config, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementTrustedInstruction,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("freeze declarative Context config: %v", err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(profileID),
		"instance_id":               fixture.InstanceID,
		"port": map[string]any{
			"name":          productionContextPort.Name,
			"exact_version": productionContextPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  fixture.ModuleID,
			"exact_version":       moduleApplyTestVersion,
			"artifact_digest":     fixture.ArtifactDigest,
			"artifact_size_bytes": fixture.ArtifactSizeBytes,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestDeclarative),
				"protocol": moduleapi.RuntimeProtocolStaticV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": portBindingIndex,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(moduleApplyDenyAllAuthorityCanonicalV1),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
	})
}

func newDisabledDeclarativeModuleApplyPlanV1(
	t *testing.T,
	instanceID string,
	expectedPointer uint64,
) []byte {
	t.Helper()
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyDisabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(moduleApplyTestProfileID),
		"instance_id":               instanceID,
		"port": map[string]any{
			"name":          productionContextPort.Name,
			"exact_version": productionContextPort.ExactVersion,
		},
	})
}

func assertDeclarativeModuleApplyResultV1(
	t *testing.T,
	result moduleApplyResultV1,
	fixture moduleApplyDeclarativeFixtureV1,
	status moduleApplyStatusV1,
	desired moduleApplyDesiredStateV1,
	pointer uint64,
) {
	t.Helper()
	if result.SchemaVersion != moduleApplyResultSchemaV1 ||
		result.Status != status || result.DesiredState != desired ||
		result.TenantID != defaultTenantID ||
		result.BindingTarget.ProfileID != moduleApplyTestProfileID ||
		result.InstanceID != fixture.InstanceID ||
		result.Port != productionContextPort ||
		result.PointerRevision != pointer ||
		result.ControlSnapshotID == "" || result.CatalogGenerationID == "" ||
		!moduleapi.ValidSHA256(result.PlanDigest) {
		t.Fatalf("declarative module-apply result=%+v", result)
	}
	if desired == moduleApplyEnabledV1 {
		if result.Module == nil || result.Module.ID != fixture.ModuleID ||
			result.Module.ExactVersion != moduleApplyTestVersion ||
			result.Module.ArtifactDigest != fixture.ArtifactDigest {
			t.Fatalf("enabled declarative module-apply result=%+v", result)
		}
	} else if result.Module != nil {
		t.Fatalf("disabled declarative module-apply result carries module=%+v", result)
	}
}

func assertModuleApplyContextStateV1(
	t *testing.T,
	databasePath string,
	want []moduleApplyDeclarativeFixtureV1,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	profile, found := findModuleApplyProfileV1(control, moduleApplyTestProfileID)
	if !found {
		t.Fatal("Pure Chat Profile is absent")
	}
	bindings := make([]controlcontract.BindingSpec, 0)
	for _, binding := range profile.Bindings {
		if binding.Port == productionContextPort {
			bindings = append(bindings, binding)
		}
	}
	if len(bindings) != len(want)+1 {
		t.Fatalf("current Context bindings=%+v want=%+v", bindings, want)
	}
	for index, fixture := range want {
		binding := bindings[index]
		if binding.InstanceID != fixture.InstanceID ||
			binding.FailurePolicy != moduleapi.FailureRequired ||
			len(binding.StaticContextRefs) != 1 {
			t.Fatalf("Context binding %d=%+v", index, binding)
		}
		entry, found := catalog.FindInstance(fixture.InstanceID)
		if !found || entry.Activation.ModuleID != fixture.ModuleID ||
			entry.Activation.ExecutionClass != moduleapi.ExecutionDeclarative ||
			entry.Activation.AdapterIdentity != declarativeAdapterID ||
			len(entry.Provides) != 1 || entry.Provides[0] != productionContextPort {
			t.Fatalf("Context Catalog entry %d=%+v found=%v", index, entry, found)
		}
		staticContent, err := store.GetContent(ctx, binding.StaticContextRefs[0])
		if err != nil || staticContent.Kind != currentstore.ContentStaticContext {
			t.Fatalf("Context static content %d=%+v err=%v", index, staticContent, err)
		}
		if _, err := corecontract.RestoreStaticContextV1(staticContent.CanonicalBytes); err != nil {
			t.Fatalf("restore Context static content %d: %v", index, err)
		}
	}
	baseline := bindings[len(bindings)-1]
	if baseline.InstanceID != moduleApplyBasicContextInstance ||
		baseline.FailurePolicy != moduleapi.FailureRequired ||
		len(baseline.StaticContextRefs) != 1 {
		t.Fatalf("Pure Chat baseline Context binding changed: %+v", baseline)
	}
}

func assertModuleApplyContextChatV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	requestID string,
	message string,
	want []moduleapi.ModelMessageV1,
) string {
	t.Helper()
	return assertNamedModuleApplyContextChatV1(
		t,
		databasePath,
		artifactRoot,
		defaultWorkspaceID,
		moduleApplyTestProfileID,
		requestID,
		message,
		want,
	)
}

func assertNamedModuleApplyContextChatV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	workspaceID string,
	profileID string,
	requestID string,
	message string,
	want []moduleapi.ModelMessageV1,
) string {
	t.Helper()
	ctx := context.Background()
	composition, err := openProductionComposition(
		ctx, databasePath, artifactRoot, defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open declarative production composition: %v", err)
	}
	input := moduleApplyChatInputV1(requestID, message)
	input.WorkspaceID = workspaceID
	input.ProfileID = profileID
	result, chatErr := composition.chat.Chat(ctx, input)
	if chatErr != nil {
		_ = composition.Close()
		t.Fatalf("chat through declarative module apply: %v", chatErr)
	}
	if !result.AdmissionCreated || result.TerminalResult == nil ||
		result.Reply != message || result.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("declarative module-apply chat result=%+v", result)
	}
	record, readErr := composition.store.GetModelDispatchRecord(
		ctx,
		result.TerminalResult.AttemptID,
	)
	if readErr != nil {
		_ = composition.Close()
		t.Fatalf("read declarative model dispatch: %v", readErr)
	}
	request, restoreErr := moduleapi.RestoreModelGenerateRequestV1(
		record.Attempt.Request.CanonicalBytes,
	)
	closeErr := composition.Close()
	if restoreErr != nil || closeErr != nil {
		t.Fatalf("restore declarative model request: %v", errors.Join(restoreErr, closeErr))
	}
	if len(request.Messages) != len(want) {
		t.Fatalf("model messages=%+v want=%+v", request.Messages, want)
	}
	for index := range want {
		if request.Messages[index] != want[index] {
			t.Fatalf("model message %d=%+v want=%+v", index, request.Messages[index], want[index])
		}
	}
	return result.RunID
}

func assertFrozenModuleApplyContextInstancesV1(
	t *testing.T,
	databasePath string,
	runID string,
	want []string,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	lease, err := store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID: runID, OwnerID: "module-apply-declarative-inspector", TTL: moduleApplyReconcileTimeout,
		},
	)
	if err != nil {
		t.Fatalf("acquire frozen declarative Run lease: %v", err)
	}
	defer func() {
		if err := store.ReleaseRunLease(ctx, lease); err != nil {
			t.Errorf("release frozen declarative Run lease: %v", err)
		}
	}()
	run, err := store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatalf("load frozen declarative Run: %v", err)
	}
	for _, plan := range run.Member.PortPlans {
		if plan.Port != productionContextPort {
			continue
		}
		if len(plan.Bindings) != len(want) {
			t.Fatalf("frozen Context PortPlan=%+v want instances=%v", plan, want)
		}
		for index, instanceID := range want {
			if plan.Bindings[index].Provider.InstanceID != instanceID {
				t.Fatalf("frozen Context binding %d=%+v want instance=%q", index, plan.Bindings[index], instanceID)
			}
		}
		return
	}
	t.Fatal("frozen Run has no context.provide/v1 PortPlan")
}

func assertDeclarativeModuleHistoryRetainedV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	fixtures ...moduleApplyDeclarativeFixtureV1,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, _, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		if _, found := catalog.FindInstance(fixture.InstanceID); found {
			t.Fatalf("disabled instance %q remains in current Catalog", fixture.InstanceID)
		}
		installation, err := store.GetModuleInstallationByIdentity(
			ctx,
			fixture.ModuleID,
			moduleApplyTestVersion,
		)
		if err != nil || installation.ArtifactDigest != fixture.ArtifactDigest {
			t.Fatalf("retained installation for %q=%+v err=%v", fixture.ModuleID, installation, err)
		}
		activation, err := store.GetLatestModuleActivationForInstance(
			ctx,
			defaultTenantID,
			fixture.InstanceID,
		)
		if err != nil || activation.InstallationID != installation.InstallationID ||
			activation.ExecutionClass != moduleapi.ExecutionDeclarative ||
			activation.AdapterIdentity != declarativeAdapterID {
			t.Fatalf("retained activation for %q=%+v err=%v", fixture.InstanceID, activation, err)
		}
		if _, err := os.Stat(filepath.Join(artifactRoot, fixture.ArtifactDigest)); err != nil {
			t.Fatalf("retained artifact for %q: %v", fixture.ModuleID, err)
		}
	}
}
