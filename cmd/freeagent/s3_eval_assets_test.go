package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/internal/s3eval"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const s3CAssetParameters = `{"max_tokens":2048,"temperature":0.2,"thinking":{"type":"disabled"}}`

type s3CAssetMatrixCase struct {
	name            string
	seedFile        string
	seedID          string
	identitySuffix  string
	model           string
	modelBuildID    string
	modelInstanceID string
	reviewer        bool
}

type s3CAssetScenarioCase struct {
	name     string
	file     string
	caseKind s3eval.CaseKindV1
}

type s3CAssetSeedRoles struct {
	Definitions struct {
		Agent struct {
			ID   string `json:"id"`
			Body struct {
				Kind string `json:"kind"`
			} `json:"body"`
		} `json:"agent"`
		AdditionalAgents []struct {
			ID   string `json:"id"`
			Body struct {
				Kind string `json:"kind"`
			} `json:"body"`
		} `json:"additional_agents"`
	} `json:"definitions"`
}

func TestS3CExampleAssetCompatibilityMatrix(t *testing.T) {
	exampleRoot := filepath.Dir(exampleSeedPath(t))
	assets := []s3CAssetMatrixCase{
		{
			name:            "flash/reviewer-off",
			seedFile:        "s3c-deepseek-v4-flash-reviewer-off.bootstrap.seed.json",
			seedID:          "freeagent.s3c.deepseek-v4-flash.reviewer-off",
			identitySuffix:  "deepseek-v4-flash-reviewer-off",
			model:           deepseekmodel.ModelV4Flash,
			modelBuildID:    localDeepSeekFlashBuild,
			modelInstanceID: "model-deepseek-v4-flash",
		},
		{
			name:            "flash/reviewer-on",
			seedFile:        "s3c-deepseek-v4-flash-reviewer-on.bootstrap.seed.json",
			seedID:          "freeagent.s3c.deepseek-v4-flash.reviewer-on",
			identitySuffix:  "deepseek-v4-flash-reviewer-on",
			model:           deepseekmodel.ModelV4Flash,
			modelBuildID:    localDeepSeekFlashBuild,
			modelInstanceID: "model-deepseek-v4-flash",
			reviewer:        true,
		},
		{
			name:            "pro/reviewer-off",
			seedFile:        "s3c-deepseek-v4-pro-reviewer-off.bootstrap.seed.json",
			seedID:          "freeagent.s3c.deepseek-v4-pro.reviewer-off",
			identitySuffix:  "deepseek-v4-pro-reviewer-off",
			model:           deepseekmodel.ModelV4Pro,
			modelBuildID:    localDeepSeekProBuild,
			modelInstanceID: "model-deepseek-v4-pro",
		},
		{
			name:            "pro/reviewer-on",
			seedFile:        "s3c-deepseek-v4-pro-reviewer-on.bootstrap.seed.json",
			seedID:          "freeagent.s3c.deepseek-v4-pro.reviewer-on",
			identitySuffix:  "deepseek-v4-pro-reviewer-on",
			model:           deepseekmodel.ModelV4Pro,
			modelBuildID:    localDeepSeekProBuild,
			modelInstanceID: "model-deepseek-v4-pro",
			reviewer:        true,
		},
	}
	scenarios := []s3CAssetScenarioCase{
		{
			name:     "clean",
			file:     "s3c-architecture-clean.scenario.json",
			caseKind: s3eval.CaseKindCleanV1,
		},
		{
			name:     "trap",
			file:     "s3c-architecture-trap.scenario.json",
			caseKind: s3eval.CaseKindTrapV1,
		},
	}

	for _, asset := range assets {
		asset := asset
		t.Run(asset.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			seedPath := filepath.Join(exampleRoot, asset.seedFile)

			initialized, err := initializeProductionData(ctx, initInput{
				DatabasePath: databasePath,
				SeedPath:     seedPath,
				ArtifactRoot: artifactRoot,
			})
			if err != nil {
				t.Fatalf("initialize %s: %v", asset.seedFile, err)
			}
			assertS3CAssetInitialization(t, initialized, asset)
			assertS3CAssetSeedRoles(t, seedPath, asset.reviewer)
			assertS3CDeepSeekArtifact(t, artifactRoot)

			store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
			if err != nil {
				t.Fatalf("open initialized Current Store: %v", err)
			}
			defer func() {
				if closeErr := store.Close(); closeErr != nil {
					t.Errorf("close initialized Current Store: %v", closeErr)
				}
			}()

			basis, control, catalog, err := store.LoadPublishedBasis(
				ctx,
				defaultTenantID,
			)
			if err != nil {
				t.Fatalf("load S3-C PublishedBasis: %v", err)
			}
			assertS3CPublishedAssetClosure(
				t,
				ctx,
				store,
				basis,
				control,
				catalog,
				asset,
			)

			for _, scenarioCase := range scenarios {
				scenarioCase := scenarioCase
				t.Run(scenarioCase.name, func(t *testing.T) {
					scenario, err := readS3EvalScenario(filepath.Join(
						exampleRoot,
						scenarioCase.file,
					))
					if err != nil {
						t.Fatalf("read %s: %v", scenarioCase.file, err)
					}
					assertS3CScenarioResolvesAgainstControl(
						t,
						scenario,
						scenarioCase.caseKind,
						control,
					)
				})
			}
		})
	}
}

func assertS3CAssetInitialization(
	t *testing.T,
	initialized initResult,
	asset s3CAssetMatrixCase,
) {
	t.Helper()
	if initialized.SeedID != asset.seedID || initialized.SeedRevision != 1 ||
		initialized.Defaults.AgentID != "s3c.architect" ||
		initialized.Defaults.ProfileID != "s3c.coordinator" ||
		initialized.Defaults.WorkspaceID != "s3c-ws-frontend" ||
		len(initialized.ArtifactLocks) != 2 ||
		!containsS3CAssetLock(initialized.ArtifactLocks, localDeepSeekDigest) {
		t.Fatalf("initialized S3-C asset closure=%+v", initialized)
	}
}

func containsS3CAssetLock(locks []string, expected string) bool {
	for _, lock := range locks {
		if lock == expected {
			return true
		}
	}
	return false
}

func assertS3CAssetSeedRoles(t *testing.T, seedPath string, reviewer bool) {
	t.Helper()
	payload, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read S3-C seed roles: %v", err)
	}
	var roles s3CAssetSeedRoles
	if err := json.Unmarshal(payload, &roles); err != nil {
		t.Fatalf("decode S3-C seed roles: %v", err)
	}
	if roles.Definitions.Agent.ID != "s3c.architect" ||
		roles.Definitions.Agent.Body.Kind != "COMPOSITE" {
		t.Fatalf("S3-C root Agent role=%+v", roles.Definitions.Agent)
	}
	want := map[string]string{
		"s3c.backend":  "SPECIALIST",
		"s3c.frontend": "SPECIALIST",
		"s3c.network":  "SPECIALIST",
	}
	if reviewer {
		want["s3c.reviewer"] = "REVIEWER"
	}
	if len(roles.Definitions.AdditionalAgents) != len(want) {
		t.Fatalf(
			"S3-C additional Agent roles=%+v, want=%v",
			roles.Definitions.AdditionalAgents,
			want,
		)
	}
	for _, agent := range roles.Definitions.AdditionalAgents {
		if expected, present := want[agent.ID]; !present || agent.Body.Kind != expected {
			t.Fatalf("unexpected S3-C Agent role=%+v", agent)
		}
		delete(want, agent.ID)
	}
	if len(want) != 0 {
		t.Fatalf("S3-C seed is missing Agent roles=%v", want)
	}
}

func assertS3CDeepSeekArtifact(t *testing.T, artifactRoot string) {
	t.Helper()
	digest, size, err := inspectArtifact(filepath.Join(
		artifactRoot,
		localDeepSeekDigest,
	))
	if err != nil {
		t.Fatalf("inspect initialized DeepSeek artifact: %v", err)
	}
	if digest != localDeepSeekDigest || size != localDeepSeekSize {
		t.Fatalf(
			"initialized DeepSeek artifact=%s/%d, want=%s/%d",
			digest,
			size,
			localDeepSeekDigest,
			localDeepSeekSize,
		)
	}
}

func assertS3CPublishedAssetClosure(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	asset s3CAssetMatrixCase,
) {
	t.Helper()
	wantControlID := "control-s3c-" + asset.identitySuffix
	wantCatalogID := "catalog-s3c-" + asset.identitySuffix
	if basis.TenantID != defaultTenantID ||
		basis.PointerRevision != 1 ||
		basis.Control.SnapshotID != wantControlID ||
		basis.Control.Revision != 1 ||
		basis.Catalog.GenerationID != wantCatalogID ||
		basis.Catalog.Generation != 1 ||
		basis.Control.SnapshotID != control.SnapshotID ||
		basis.Control.Revision != control.Revision ||
		basis.Control.Digest != control.Digest ||
		basis.Catalog.GenerationID != catalog.GenerationID ||
		basis.Catalog.Generation != catalog.Generation ||
		basis.Catalog.Digest != catalog.Digest ||
		control.TenantID != defaultTenantID ||
		catalog.TenantID != defaultTenantID ||
		catalog.ControlSnapshotID != control.SnapshotID ||
		catalog.ControlSnapshotDigest != control.Digest ||
		len(control.Agents) != 4+s3CBoolCount(asset.reviewer) ||
		len(control.Profiles) != 4+s3CBoolCount(asset.reviewer) ||
		len(control.Workspaces) != s3eval.WorkspaceCount ||
		len(control.CompositeAgents) != 1 ||
		!moduleapi.ValidSHA256(control.Digest) ||
		!moduleapi.ValidSHA256(catalog.Digest) {
		t.Fatalf(
			"S3-C PublishedBasis closure=%+v control=%+v catalog=%+v",
			basis,
			control,
			catalog,
		)
	}

	provider, found := catalog.FindInstance(asset.modelInstanceID)
	if !found {
		t.Fatalf("Catalog has no DeepSeek instance %q", asset.modelInstanceID)
	}
	wantProvider := moduleapi.ActivatedModuleRef{
		ModuleID:           localDeepSeekModuleID,
		Version:            localDeepSeekVersion,
		ArtifactDigest:     localDeepSeekDigest,
		InstanceID:         asset.modelInstanceID,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    deepseekmodel.AdapterIdentityV1,
		ActivationRevision: 1,
	}
	if provider.Activation != wantProvider ||
		len(provider.Provides) != 1 || provider.Provides[0] != productionModelPort {
		t.Fatalf("DeepSeek Catalog provider=%+v, want=%+v", provider, wantProvider)
	}

	installation, err := store.GetModuleInstallation(
		ctx,
		"installation-freeagent-builtin-model-deepseek-1",
	)
	if err != nil {
		t.Fatalf("load DeepSeek installation: %v", err)
	}
	manifest, manifestCanonical, err := moduleapi.ParseModuleManifestV1(
		installation.ManifestBytes,
	)
	if err != nil {
		t.Fatalf("restore DeepSeek installation manifest: %v", err)
	}
	if installation.ModuleID != localDeepSeekModuleID ||
		installation.ExactVersion != localDeepSeekVersion ||
		installation.ArtifactDigest != localDeepSeekDigest ||
		!moduleapi.ValidSHA256(installation.ManifestRef) ||
		!bytes.Equal(installation.ManifestBytes, manifestCanonical) ||
		manifest.ID != localDeepSeekModuleID ||
		manifest.Version != localDeepSeekVersion {
		t.Fatalf("DeepSeek installation=%+v manifest=%+v", installation, manifest)
	}

	assertS3CCompositeDefinition(t, control, asset.reviewer)
	configRef := assertS3CProfileModelBindings(t, control, asset)
	assertS3CModelConfig(t, ctx, store, configRef, asset)
}

func s3CBoolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func assertS3CCompositeDefinition(
	t *testing.T,
	control controlcontract.ControlSnapshot,
	reviewer bool,
) {
	t.Helper()
	definition, found := control.FindCompositeAgent("s3c.architect")
	if !found || definition.CoordinatorProfileID != "s3c.coordinator" ||
		len(definition.Members) != 3 {
		t.Fatalf("S3-C Composite definition=%+v found=%t", definition, found)
	}
	wantMembers := map[string]controlcontract.CompositeAgentMemberV1{
		"backend": {
			SlotID:            "backend",
			AgentID:           "s3c.backend",
			ProfileID:         "s3c.backend",
			FocusID:           "backend",
			WeightBasisPoints: 3333,
		},
		"frontend": {
			SlotID:            "frontend",
			AgentID:           "s3c.frontend",
			ProfileID:         "s3c.frontend",
			FocusID:           "frontend",
			WeightBasisPoints: 3334,
		},
		"network": {
			SlotID:            "network",
			AgentID:           "s3c.network",
			ProfileID:         "s3c.network",
			FocusID:           "network",
			WeightBasisPoints: 3333,
		},
	}
	var totalWeight uint32
	for _, member := range definition.Members {
		want, present := wantMembers[member.SlotID]
		if !present || member != want {
			t.Fatalf("unexpected S3-C Specialist member=%+v", member)
		}
		if _, found := control.FindAgent(member.AgentID); !found {
			t.Fatalf("Specialist Agent %q is not published", member.AgentID)
		}
		if _, found := control.FindProfile(member.ProfileID); !found {
			t.Fatalf("Specialist Profile %q is not published", member.ProfileID)
		}
		totalWeight += member.WeightBasisPoints
		delete(wantMembers, member.SlotID)
	}
	if len(wantMembers) != 0 || totalWeight != 10000 {
		t.Fatalf("S3-C Specialist closure missing=%v weight=%d", wantMembers, totalWeight)
	}

	if reviewer {
		if definition.Reviewer == nil ||
			definition.Reviewer.SchemaVersion !=
				controlcontract.CompositeReviewerSchemaVersionV1 ||
			definition.Reviewer.AgentID != "s3c.reviewer" ||
			definition.Reviewer.ProfileID != "s3c.reviewer" ||
			definition.Reviewer.MaxOutputTokens !=
				controlcontract.CompositeReviewerMaxOutputTokensV1 ||
			definition.Reviewer.Policy !=
				controlcontract.CompositeReviewerPolicyResultsGateV1 {
			t.Fatalf("S3-C Reviewer definition=%+v", definition.Reviewer)
		}
		if _, found := control.FindAgent("s3c.reviewer"); !found {
			t.Fatal("Reviewer-on seed did not publish Reviewer Agent")
		}
		if _, found := control.FindProfile("s3c.reviewer"); !found {
			t.Fatal("Reviewer-on seed did not publish Reviewer Profile")
		}
		return
	}
	if definition.Reviewer != nil {
		t.Fatalf("Reviewer-off seed published Reviewer=%+v", definition.Reviewer)
	}
	if _, found := control.FindAgent("s3c.reviewer"); found {
		t.Fatal("Reviewer-off seed published hidden Reviewer Agent")
	}
	if _, found := control.FindProfile("s3c.reviewer"); found {
		t.Fatal("Reviewer-off seed published hidden Reviewer Profile")
	}
}

func assertS3CProfileModelBindings(
	t *testing.T,
	control controlcontract.ControlSnapshot,
	asset s3CAssetMatrixCase,
) string {
	t.Helper()
	profileIDs := []string{
		"s3c.backend",
		"s3c.coordinator",
		"s3c.frontend",
		"s3c.network",
	}
	if asset.reviewer {
		profileIDs = append(profileIDs, "s3c.reviewer")
	}
	configRef := ""
	for _, profileID := range profileIDs {
		profile, found := control.FindProfile(profileID)
		if !found {
			t.Fatalf("published Profile %q is absent", profileID)
		}
		if profile.ModelProfile != nil {
			t.Fatalf("S3-C Profile %q unexpectedly binds ModelProfile", profileID)
		}
		binding, found := s3CAssetModelBinding(profile)
		if !found || binding.InstanceID != asset.modelInstanceID ||
			binding.FailurePolicy != moduleapi.FailureRequired ||
			!moduleapi.ValidSHA256(binding.ConfigRef) {
			t.Fatalf("Profile %q model Binding=%+v found=%t", profileID, binding, found)
		}
		if configRef == "" {
			configRef = binding.ConfigRef
		} else if binding.ConfigRef != configRef {
			t.Fatalf(
				"Profile %q ConfigRef=%q differs from %q",
				profileID,
				binding.ConfigRef,
				configRef,
			)
		}
	}
	return configRef
}

func s3CAssetModelBinding(
	profile controlcontract.ProfileDefinition,
) (controlcontract.BindingSpec, bool) {
	var result controlcontract.BindingSpec
	found := false
	for _, binding := range profile.Bindings {
		if binding.Port != productionModelPort {
			continue
		}
		if found {
			return controlcontract.BindingSpec{}, false
		}
		result = binding
		found = true
	}
	return result, found
}

func assertS3CModelConfig(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	configRef string,
	asset s3CAssetMatrixCase,
) {
	t.Helper()
	configRecord, err := store.GetContent(ctx, configRef)
	if err != nil {
		t.Fatalf("load DeepSeek model Config: %v", err)
	}
	wantConfig, wantCanonical, err := moduleapi.NewModelBindingConfigV2(
		moduleapi.ModelBindingConfigV2{
			SchemaVersion: moduleapi.ModelBindingConfigSchemaV2,
			Provider:      deepseekmodel.ProviderNameV1,
			Model:         asset.model,
			ModelBuildID:  asset.modelBuildID,
			Parameters:    json.RawMessage(s3CAssetParameters),
		},
	)
	if err != nil {
		t.Fatalf("build expected DeepSeek Config: %v", err)
	}
	restoredConfig, err := moduleapi.RestoreModelBindingConfigV2(
		configRecord.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("restore persisted DeepSeek Config: %v", err)
	}
	if configRecord.Digest != configRef ||
		configRecord.Kind != currentstore.ContentConfig ||
		!bytes.Equal(configRecord.CanonicalBytes, wantCanonical) ||
		restoredConfig.Provider != wantConfig.Provider ||
		restoredConfig.Model != wantConfig.Model ||
		restoredConfig.ModelBuildID != wantConfig.ModelBuildID ||
		!bytes.Equal(restoredConfig.Parameters, wantConfig.Parameters) {
		t.Fatalf(
			"persisted DeepSeek Config=%+v bytes=%s want=%+v bytes=%s",
			restoredConfig,
			configRecord.CanonicalBytes,
			wantConfig,
			wantCanonical,
		)
	}
}

func assertS3CScenarioResolvesAgainstControl(
	t *testing.T,
	scenario s3eval.ScenarioV1,
	wantCaseKind s3eval.CaseKindV1,
	control controlcontract.ControlSnapshot,
) {
	t.Helper()
	if scenario.AgentID != "s3c.architect" ||
		scenario.ProfileID != "s3c.coordinator" ||
		len(scenario.Tasks) != s3eval.WorkspaceCount {
		t.Fatalf("S3-C scenario scope=%+v", scenario)
	}
	if _, found := control.FindAgent(scenario.AgentID); !found {
		t.Fatalf("scenario Agent %q is not published", scenario.AgentID)
	}
	if _, found := control.FindProfile(scenario.ProfileID); !found {
		t.Fatalf("scenario Profile %q is not published", scenario.ProfileID)
	}
	definition, found := control.FindCompositeAgent(scenario.AgentID)
	if !found || definition.CoordinatorProfileID != scenario.ProfileID {
		t.Fatalf(
			"scenario Composite scope does not resolve: definition=%+v found=%t",
			definition,
			found,
		)
	}
	for _, task := range scenario.Tasks {
		if task.CaseKind != wantCaseKind {
			t.Fatalf("scenario task %q case_kind=%q", task.TaskID, task.CaseKind)
		}
		if _, found := control.FindWorkspace(task.WorkspaceID); !found {
			t.Fatalf("scenario Workspace %q is not published", task.WorkspaceID)
		}
	}
	input, err := s3eval.ScenarioToExperimentInput(
		scenario,
		defaultTenantID,
		defaultPrincipalID,
	)
	if err != nil {
		t.Fatalf("freeze scenario ExperimentInput: %v", err)
	}
	for index, task := range input.Tasks {
		want := scenario.Tasks[index]
		if task.ChatInput.TenantID != defaultTenantID ||
			task.ChatInput.PrincipalID != defaultPrincipalID ||
			task.ChatInput.AgentID != scenario.AgentID ||
			task.ChatInput.ProfileID != scenario.ProfileID ||
			task.ChatInput.WorkspaceID != want.WorkspaceID ||
			task.ChatInput.Message != want.Message ||
			task.ChatInput.RequestID != "" ||
			!task.ChatInput.Deadline.IsZero() {
			t.Fatalf("scenario task %d ExperimentInput=%+v", index, task.ChatInput)
		}
	}
}
