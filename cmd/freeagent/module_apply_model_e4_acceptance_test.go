package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleApplyDeepSeekModelPreflightFailsClosedBeforeMutation(t *testing.T) {
	tests := []struct {
		name                string
		price               corecontract.ModelPriceSnapshotV1
		grant               string
		profileMutation     func(*corecontract.ModelProfileV1)
		wantDryFailure      moduleApplyFailureCodeV1
		wantApplyFailure    moduleApplyFailureCodeV1
		includeModelProfile bool
	}{
		{
			name:             "missing SecretRef grant",
			price:            moduleApplyProPriceV1(),
			wantDryFailure:   moduleApplyFailureGrantRequired,
			wantApplyFailure: moduleApplyFailureGrantRequired,
		},
		{
			name:             "mismatched SecretRef grant",
			price:            moduleApplyProPriceV1(),
			grant:            "env:OTHER_DEEPSEEK_API_KEY",
			wantDryFailure:   moduleApplyFailureGrantRequired,
			wantApplyFailure: moduleApplyFailureGrantRequired,
		},
		{
			name: "PriceSnapshot provider mismatch",
			price: moduleApplyE4PriceV1(
				"price-deepseek-v4-pro-provider-mismatch",
				"other-provider",
				deepseekmodel.ModelV4Pro,
				"deepseek-public-price-2026-08-04",
			),
			grant:            moduleApplyModelSecretRefV1,
			wantDryFailure:   moduleApplyFailureStore,
			wantApplyFailure: moduleApplyFailureTarget,
		},
		{
			name: "PriceSnapshot model mismatch",
			price: moduleApplyE4PriceV1(
				"price-deepseek-v4-pro-model-mismatch",
				deepseekmodel.ProviderNameV1,
				deepseekmodel.ModelV4Flash,
				"deepseek-public-price-2026-08-04",
			),
			grant:            moduleApplyModelSecretRefV1,
			wantDryFailure:   moduleApplyFailureStore,
			wantApplyFailure: moduleApplyFailureTarget,
		},
		{
			name: "PriceSnapshot billing mismatch",
			price: moduleApplyE4PriceV1(
				"price-deepseek-v4-pro-billing-mismatch",
				deepseekmodel.ProviderNameV1,
				deepseekmodel.ModelV4Pro,
				"other-billing-version",
			),
			grant:            moduleApplyModelSecretRefV1,
			wantDryFailure:   moduleApplyFailureStore,
			wantApplyFailure: moduleApplyFailureTarget,
		},
		{
			name:                "ModelProfile provider mismatch",
			price:               moduleApplyProPriceV1(),
			grant:               moduleApplyModelSecretRefV1,
			includeModelProfile: true,
			profileMutation: func(profile *corecontract.ModelProfileV1) {
				profile.Provider = "other-provider"
			},
			wantDryFailure:   moduleApplyFailureStore,
			wantApplyFailure: moduleApplyFailureTarget,
		},
		{
			name:                "ModelProfile model mismatch",
			price:               moduleApplyProPriceV1(),
			grant:               moduleApplyModelSecretRefV1,
			includeModelProfile: true,
			profileMutation: func(profile *corecontract.ModelProfileV1) {
				profile.Model = deepseekmodel.ModelV4Flash
			},
			wantDryFailure:   moduleApplyFailureStore,
			wantApplyFailure: moduleApplyFailureTarget,
		},
		{
			name:                "ModelProfile build mismatch",
			price:               moduleApplyProPriceV1(),
			grant:               moduleApplyModelSecretRefV1,
			includeModelProfile: true,
			profileMutation: func(profile *corecontract.ModelProfileV1) {
				profile.ModelBuildID = localDeepSeekFlashBuild
			},
			wantDryFailure:   moduleApplyFailureStore,
			wantApplyFailure: moduleApplyFailureTarget,
		},
		{
			name:                "ModelProfile config ref mismatch",
			price:               moduleApplyProPriceV1(),
			grant:               moduleApplyModelSecretRefV1,
			includeModelProfile: true,
			profileMutation: func(profile *corecontract.ModelProfileV1) {
				profile.ModelConfigRef = strings.Repeat("1", 64)
			},
			wantDryFailure:   moduleApplyFailureStore,
			wantApplyFailure: moduleApplyFailureTarget,
		},
		{
			name:                "ModelProfile artifact mismatch",
			price:               moduleApplyProPriceV1(),
			grant:               moduleApplyModelSecretRefV1,
			includeModelProfile: true,
			profileMutation: func(profile *corecontract.ModelProfileV1) {
				profile.AdapterArtifactDigest = strings.Repeat("2", 64)
			},
			wantDryFailure:   moduleApplyFailureStore,
			wantApplyFailure: moduleApplyFailureTarget,
		},
		{
			name:                "ModelProfile adapter mismatch",
			price:               moduleApplyProPriceV1(),
			grant:               moduleApplyModelSecretRefV1,
			includeModelProfile: true,
			profileMutation: func(profile *corecontract.ModelProfileV1) {
				profile.AdapterIdentity = "other.model.adapter/v1"
			},
			wantDryFailure:   moduleApplyFailureStore,
			wantApplyFailure: moduleApplyFailureTarget,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newModuleApplyModelE4FixtureV1(
				t,
				"current-v1.deepseek.bootstrap.seed.json",
				test.price,
			)
			config := moduleApplyModelConfigV1(
				t,
				deepseekmodel.ModelV4Pro,
				localDeepSeekProBuild,
				test.price.PriceSnapshotID,
				json.RawMessage(`{"max_tokens":1024,"temperature":0.2,"thinking":{"type":"disabled"}}`),
			)
			var modelProfile []byte
			if test.includeModelProfile {
				modelProfile = moduleApplyE4ModelProfileV1(
					t,
					config,
					test.profileMutation,
				)
			}
			planCanonical := moduleApplyE4ModelPlanV1(
				t,
				fixture.basis.PointerRevision,
				"deepseek-chat",
				config,
				moduleApplyModelAuthorityV1(t),
				modelProfile,
			)
			input := fixture.commandInputV1(t, planCanonical, test.grant)
			before := moduleApplyMutationRowCountsV1(t, fixture.databasePath)

			if _, err := dryRunModulePlanV1(context.Background(), input); err == nil ||
				moduleApplyFailureCodeOfV1(err) != test.wantDryFailure {
				t.Fatalf("dry-run failure=%v want=%s", err, test.wantDryFailure)
			}
			if after := moduleApplyMutationRowCountsV1(t, fixture.databasePath); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed dry-run changed Store rows: before=%v after=%v", before, after)
			}

			if _, err := applyModulePlanV1(context.Background(), input); err == nil ||
				moduleApplyFailureCodeOfV1(err) != test.wantApplyFailure {
				t.Fatalf("apply failure=%v want=%s", err, test.wantApplyFailure)
			}
			if after := moduleApplyMutationRowCountsV1(t, fixture.databasePath); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed apply changed Store rows: before=%v after=%v", before, after)
			}
			assertNoModuleApplyStageResidueV1(t, fixture.root)
		})
	}
}

func TestModuleApplyDeepSeekModelProfileBackupRestoreExactClosure(t *testing.T) {
	ctx := context.Background()
	fixture := newModuleApplyModelE4FixtureV1(
		t,
		"current-v1.deepseek.bootstrap.seed.json",
		moduleApplyProPriceV1(),
	)
	beforeProfile, found := fixture.control.FindProfile("deepseek-chat")
	if !found {
		t.Fatal("DeepSeek profile is absent before Model replacement")
	}
	config := moduleApplyModelConfigV1(
		t,
		deepseekmodel.ModelV4Pro,
		localDeepSeekProBuild,
		moduleApplyProPriceV1().PriceSnapshotID,
		json.RawMessage(`{"max_tokens":1024,"temperature":0.2,"thinking":{"type":"disabled"}}`),
	)
	modelProfile := moduleApplyE4ModelProfileV1(t, config, nil)
	planCanonical := moduleApplyE4ModelPlanV1(
		t,
		fixture.basis.PointerRevision,
		"deepseek-chat",
		config,
		moduleApplyModelAuthorityV1(t),
		modelProfile,
	)
	result, err := applyModulePlanV1(
		ctx,
		fixture.commandInputV1(t, planCanonical, moduleApplyModelSecretRefV1),
	)
	if err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("apply profiled Model replacement: result=%+v error=%v", result, err)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	afterProfile, found := control.FindProfile("deepseek-chat")
	if !found || afterProfile.ModelProfile == nil ||
		afterProfile.ContextPolicy != beforeProfile.ContextPolicy ||
		afterProfile.CostPolicy != beforeProfile.CostPolicy ||
		afterProfile.SchedulingPolicy != beforeProfile.SchedulingPolicy {
		_ = store.Close()
		t.Fatalf("profiled replacement changed unrelated Profile policy refs: before=%+v after=%+v", beforeProfile, afterProfile)
	}
	binding := moduleApplyExactModelBindingV1(t, control, "deepseek-chat")
	configRecord, configErr := store.GetContent(ctx, binding.ConfigRef)
	authorityRecord, authorityErr := store.GetContent(ctx, binding.AuthorityCeilingRef)
	profileRecord, profileErr := store.GetContent(ctx, afterProfile.ModelProfile.Digest)
	priceRecord, priceErr := store.GetModelPriceSnapshot(
		ctx,
		moduleApplyProPriceV1().PriceSnapshotID,
	)
	closeErr := store.Close()
	if err := errors.Join(configErr, authorityErr, profileErr, priceErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configRecord.CanonicalBytes, config) ||
		!bytes.Equal(profileRecord.CanonicalBytes, modelProfile) {
		t.Fatal("profiled replacement did not publish exact Config/ModelProfile bytes")
	}
	authority, err := moduleapi.RestoreModelAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	)
	if err != nil || authority.SecretRef != moduleApplyModelSecretRefV1 {
		t.Fatalf("published Model authority=%+v error=%v", authority, err)
	}

	bundlePath := filepath.Join(fixture.root, "w2e4-model.bundle")
	createAndVerifyModuleApplyBundleV1(
		t,
		fixture.databasePath,
		fixture.artifactRoot,
		bundlePath,
	)
	restoredDatabase, _ := restoreModuleApplyBundleV1(
		t,
		bundlePath,
		filepath.Join(fixture.root, "restored"),
	)
	restored, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatal(err)
	}
	restoredBasis, restoredControl, restoredCatalog, err := restored.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	if err != nil {
		_ = restored.Close()
		t.Fatal(err)
	}
	restoredProfile, found := restoredControl.FindProfile("deepseek-chat")
	if !found || restoredProfile.ModelProfile == nil {
		_ = restored.Close()
		t.Fatal("restored profiled Model binding is absent")
	}
	restoredBinding := moduleApplyExactModelBindingV1(t, restoredControl, "deepseek-chat")
	restoredConfig, configErr := restored.GetContent(ctx, restoredBinding.ConfigRef)
	restoredAuthority, authorityErr := restored.GetContent(ctx, restoredBinding.AuthorityCeilingRef)
	restoredModelProfile, profileErr := restored.GetContent(ctx, restoredProfile.ModelProfile.Digest)
	restoredPrice, priceErr := restored.GetModelPriceSnapshot(
		ctx,
		moduleApplyProPriceV1().PriceSnapshotID,
	)
	closeErr = restored.Close()
	if err := errors.Join(configErr, authorityErr, profileErr, priceErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if restoredBasis != basis || !reflect.DeepEqual(restoredControl, control) ||
		!reflect.DeepEqual(restoredCatalog, catalog) ||
		!reflect.DeepEqual(restoredBinding, binding) ||
		!reflect.DeepEqual(restoredProfile.ModelProfile, afterProfile.ModelProfile) ||
		!bytes.Equal(restoredConfig.CanonicalBytes, configRecord.CanonicalBytes) ||
		!bytes.Equal(restoredAuthority.CanonicalBytes, authorityRecord.CanonicalBytes) ||
		!bytes.Equal(restoredModelProfile.CanonicalBytes, profileRecord.CanonicalBytes) ||
		!reflect.DeepEqual(restoredPrice, priceRecord) {
		t.Fatal("Model Config/Authority/SecretRef/PriceSnapshot/ModelProfile drifted across backup/restore")
	}
}

func TestModuleApplyDeepSeekModelProfileCanBeClearedByExplicitReplacement(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newModuleApplyModelE4FixtureV1(
		t,
		"current-v1.deepseek.bootstrap.seed.json",
		moduleApplyProPriceV1(),
	)
	proConfig := moduleApplyModelConfigV1(
		t,
		deepseekmodel.ModelV4Pro,
		localDeepSeekProBuild,
		moduleApplyProPriceV1().PriceSnapshotID,
		json.RawMessage(`{"max_tokens":1024,"temperature":0.2,"thinking":{"type":"disabled"}}`),
	)
	profiledPlan := moduleApplyE4ModelPlanV1(
		t,
		fixture.basis.PointerRevision,
		"deepseek-chat",
		proConfig,
		moduleApplyModelAuthorityV1(t),
		moduleApplyE4ModelProfileV1(t, proConfig, nil),
	)
	if result, err := applyModulePlanV1(
		ctx,
		fixture.commandInputV1(t, profiledPlan, moduleApplyModelSecretRefV1),
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("apply profiled Model: result=%+v error=%v", result, err)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	profiledBasis, profiledControl, _, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	initialBinding := moduleApplyExactModelBindingV1(t, fixture.control, "deepseek-chat")
	initialConfig, configErr := store.GetContent(ctx, initialBinding.ConfigRef)
	closeErr := store.Close()
	if err != nil || configErr != nil || closeErr != nil {
		t.Fatal(errors.Join(err, configErr, closeErr))
	}
	profiled, found := profiledControl.FindProfile("deepseek-chat")
	if !found || profiled.ModelProfile == nil {
		t.Fatal("first replacement did not attach ModelProfile")
	}

	clearPlan := moduleApplyE4ModelPlanV1(
		t,
		profiledBasis.PointerRevision,
		"deepseek-chat",
		initialConfig.CanonicalBytes,
		moduleApplyModelAuthorityV1(t),
		nil,
	)
	if result, err := applyModulePlanV1(
		ctx,
		fixture.commandInputV1(t, clearPlan, moduleApplyModelSecretRefV1),
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("clear ModelProfile by explicit replacement: result=%+v error=%v", result, err)
	}
	store, err = currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, clearedControl, _, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	cleared, found := clearedControl.FindProfile("deepseek-chat")
	clearedBinding := moduleApplyExactModelBindingV1(t, clearedControl, "deepseek-chat")
	clearedConfig, configErr := store.GetContent(ctx, clearedBinding.ConfigRef)
	closeErr = store.Close()
	if err != nil || configErr != nil || closeErr != nil {
		t.Fatal(errors.Join(err, configErr, closeErr))
	}
	if !found || cleared.ModelProfile != nil ||
		cleared.ContextPolicy != profiled.ContextPolicy ||
		cleared.CostPolicy != profiled.CostPolicy ||
		cleared.SchedulingPolicy != profiled.SchedulingPolicy ||
		!bytes.Equal(clearedConfig.CanonicalBytes, initialConfig.CanonicalBytes) {
		t.Fatalf("explicit no-Profile target drifted: profile=%+v config=%s", cleared, clearedConfig.CanonicalBytes)
	}
}

func TestModuleApplyDeepSeekModelSameBindingProfileDesiredStateTransitions(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newModuleApplyModelE4FixtureV1(
		t,
		"current-v1.deepseek.bootstrap.seed.json",
		moduleApplyProPriceV1(),
	)
	config := moduleApplyModelConfigV1(
		t,
		deepseekmodel.ModelV4Pro,
		localDeepSeekProBuild,
		moduleApplyProPriceV1().PriceSnapshotID,
		json.RawMessage(`{"max_tokens":1024,"temperature":0.2,"thinking":{"type":"disabled"}}`),
	)
	authority := moduleApplyModelAuthorityV1(t)
	normalizePlan := moduleApplyE4ModelPlanV1(
		t,
		fixture.basis.PointerRevision,
		"deepseek-chat",
		config,
		authority,
		nil,
	)
	normalizeInput := fixture.commandInputV1(
		t,
		normalizePlan,
		moduleApplyModelSecretRefV1,
	)
	normalized, err := applyModulePlanV1(
		ctx,
		normalizeInput,
	)
	if err != nil || normalized.Status != moduleApplyStatusApplied {
		t.Fatalf("normalize same-Binding Profile fixture: result=%+v error=%v", normalized, err)
	}

	baselineProfile, found := fixture.control.FindProfile("deepseek-chat")
	if !found || baselineProfile.ModelProfile != nil {
		t.Fatalf("fixture Profile is absent or unexpectedly profiled: %+v", baselineProfile)
	}
	baselineBinding := moduleApplyExactModelBindingV1(t, fixture.control, "deepseek-chat")
	configRef, authorityRef, err := moduleApplyContentRefsV1(*normalizeInput.Plan.Binding)
	if err != nil {
		t.Fatal(err)
	}
	baselineBinding.ConfigRef = configRef
	baselineBinding.AuthorityCeilingRef = authorityRef
	baselineProfile.Bindings = append(
		[]controlcontract.BindingSpec{},
		baselineProfile.Bindings...,
	)
	for index := range baselineProfile.Bindings {
		if baselineProfile.Bindings[index].Port == productionModelPort {
			baselineProfile.Bindings[index] = baselineBinding
		}
	}
	baselineEntry, found := fixture.catalog.FindInstance(baselineBinding.InstanceID)
	if !found {
		t.Fatal("normalized Model Catalog entry is absent")
	}

	profileA := moduleApplyE4ModelProfileV1(t, config, nil)
	profileB := moduleApplyE4ModelProfileV1(
		t,
		config,
		func(profile *corecontract.ModelProfileV1) {
			profile.Version = "1.0.1"
			profile.EvaluationVersion = "1.0.1"
			profile.EvaluationResultDigest = strings.Repeat("4", 64)
		},
	)
	transitions := []struct {
		name    string
		profile []byte
	}{
		{name: "add", profile: profileA},
		{name: "change", profile: profileB},
		{name: "clear"},
	}
	previousProfile := baselineProfile.ModelProfile
	currentPointer := normalized.PointerRevision
	currentControlID := normalized.ControlSnapshotID
	currentCatalogID := normalized.CatalogGenerationID
	if _, err := currentstore.PrepareClosedCurrentStoreForPublication(
		ctx,
		fixture.databasePath,
	); err != nil {
		t.Fatalf("prepare normalized Store for same-Binding Profile dry-run: %v", err)
	}
	for _, transition := range transitions {
		t.Run(transition.name, func(t *testing.T) {
			planCanonical := moduleApplyE4ModelPlanV1(
				t,
				currentPointer,
				"deepseek-chat",
				config,
				authority,
				transition.profile,
			)
			input := fixture.commandInputV1(
				t,
				planCanonical,
				moduleApplyModelSecretRefV1,
			)
			wantProfile, _, err := moduleApplyPlannedModelProfileV1(
				input.Plan,
				baselineEntry.Activation,
			)
			if err != nil {
				t.Fatalf("derive desired ModelProfile: %v", err)
			}
			if reflect.DeepEqual(previousProfile, wantProfile) {
				t.Fatal("test transition does not change the ModelProfile ref")
			}

			beforeDryRun := moduleApplyMutationRowCountsV1(t, fixture.databasePath)
			dry, err := dryRunModulePlanV1(ctx, input)
			if err != nil || dry.Status != moduleApplyStatusWouldApply ||
				dry.Changes.Binding.Change != moduleDryRunBindingReplaceV1 ||
				dry.ObservedBasis.PointerRevision != currentPointer ||
				dry.ObservedBasis.Control.SnapshotID != currentControlID ||
				dry.ObservedBasis.Catalog.GenerationID != currentCatalogID ||
				dry.CandidateBasis.PointerRevision != currentPointer+1 {
				t.Fatalf(
					"same-Binding Profile dry-run=%+v error=%v cause=%v",
					dry,
					err,
					errors.Unwrap(err),
				)
			}
			if afterDryRun := moduleApplyMutationRowCountsV1(
				t,
				fixture.databasePath,
			); !reflect.DeepEqual(afterDryRun, beforeDryRun) {
				t.Fatalf(
					"same-Binding Profile dry-run changed Store rows: before=%v after=%v",
					beforeDryRun,
					afterDryRun,
				)
			}

			applied, err := applyModulePlanV1(ctx, input)
			if err != nil || applied.Status != moduleApplyStatusApplied ||
				applied.PointerRevision != currentPointer+1 {
				t.Fatalf("same-Binding Profile Apply=%+v error=%v", applied, err)
			}
			retried, err := applyModulePlanV1(ctx, input)
			if err != nil || retried.Status != moduleApplyStatusAlreadyApplied ||
				retried.PointerRevision != applied.PointerRevision {
				t.Fatalf("same-Binding Profile exact retry=%+v error=%v", retried, err)
			}
			if _, err := currentstore.PrepareClosedCurrentStoreForPublication(
				ctx,
				fixture.databasePath,
			); err != nil {
				t.Fatalf("prepare applied same-Binding Profile Store: %v", err)
			}
			observer, err := currentstore.OpenReadOnlyObserver(ctx, fixture.databasePath)
			if err != nil {
				t.Fatal(err)
			}
			nextBasis, nextControl, nextCatalog, err := observer.LoadPublishedBasis(
				ctx,
				defaultTenantID,
			)
			closeErr := observer.Close()
			if err != nil || closeErr != nil {
				t.Fatal(errors.Join(err, closeErr))
			}
			nextProfile, found := nextControl.FindProfile("deepseek-chat")
			if !found {
				t.Fatal("same-Binding target Profile disappeared")
			}
			nextBinding := moduleApplyExactModelBindingV1(
				t,
				nextControl,
				"deepseek-chat",
			)
			nextEntry, found := nextCatalog.FindInstance(nextBinding.InstanceID)
			if !found {
				t.Fatal("same-Binding Model Catalog entry disappeared")
			}
			nextStableProfile := nextProfile
			nextStableProfile.ModelProfile = nil
			if nextBasis.PointerRevision != applied.PointerRevision ||
				nextBasis.Control.SnapshotID != applied.ControlSnapshotID ||
				nextBasis.Catalog.GenerationID != applied.CatalogGenerationID ||
				!reflect.DeepEqual(nextBinding, baselineBinding) ||
				nextEntry.Activation != baselineEntry.Activation ||
				!reflect.DeepEqual(nextStableProfile, baselineProfile) ||
				!reflect.DeepEqual(nextProfile.ModelProfile, wantProfile) {
				t.Fatalf(
					"same-Binding Profile transition drifted another fact: basis=%+v binding=%+v profile=%+v entry=%+v",
					nextBasis,
					nextBinding,
					nextProfile,
					nextEntry,
				)
			}
			assertNoModuleApplyStageResidueV1(t, fixture.root)
			previousProfile = wantProfile
			currentPointer = nextBasis.PointerRevision
			currentControlID = nextBasis.Control.SnapshotID
			currentCatalogID = nextBasis.Catalog.GenerationID
		})
	}
}

func TestModuleApplyDeepSeekModelSameBindingProfileCorruptionFailsClosed(
	t *testing.T,
) {
	tests := []struct {
		name       string
		transition string
		target     string
		damage     string
	}{
		{
			name:       "add rejects missing current Config",
			transition: "add",
			target:     "config",
			damage:     "missing",
		},
		{
			name:       "add rejects damaged current Authority",
			transition: "add",
			target:     "authority",
			damage:     "damaged",
		},
		{
			name:       "change rejects missing current Profile",
			transition: "change",
			target:     "profile",
			damage:     "missing",
		},
		{
			name:       "change rejects damaged current Profile",
			transition: "change",
			target:     "profile",
			damage:     "damaged",
		},
		{
			name:       "clear rejects missing current Profile",
			transition: "clear",
			target:     "profile",
			damage:     "missing",
		},
		{
			name:       "clear rejects damaged current Profile",
			transition: "clear",
			target:     "profile",
			damage:     "damaged",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := newModuleApplyModelE4FixtureV1(
				t,
				"current-v1.deepseek.bootstrap.seed.json",
				moduleApplyProPriceV1(),
			)
			config := moduleApplyModelConfigV1(
				t,
				deepseekmodel.ModelV4Pro,
				localDeepSeekProBuild,
				moduleApplyProPriceV1().PriceSnapshotID,
				json.RawMessage(`{"max_tokens":1024,"temperature":0.2,"thinking":{"type":"disabled"}}`),
			)
			authority := moduleApplyModelAuthorityV1(t)
			profileA := moduleApplyE4ModelProfileV1(t, config, nil)
			profileB := moduleApplyE4ModelProfileV1(
				t,
				config,
				func(profile *corecontract.ModelProfileV1) {
					profile.Version = "1.0.1"
					profile.EvaluationVersion = "1.0.1"
					profile.EvaluationResultDigest = strings.Repeat("5", 64)
				},
			)
			var currentProfile []byte
			var desiredProfile []byte
			switch test.transition {
			case "add":
				desiredProfile = profileA
			case "change":
				currentProfile = profileA
				desiredProfile = profileB
			case "clear":
				currentProfile = profileA
			default:
				t.Fatalf("unknown Profile transition %q", test.transition)
			}

			setupPlan := moduleApplyE4ModelPlanV1(
				t,
				fixture.basis.PointerRevision,
				"deepseek-chat",
				config,
				authority,
				currentProfile,
			)
			setup, err := applyModulePlanV1(
				ctx,
				fixture.commandInputV1(t, setupPlan, moduleApplyModelSecretRefV1),
			)
			if err != nil || setup.Status != moduleApplyStatusApplied {
				t.Fatalf("publish same-Binding corruption fixture: result=%+v error=%v", setup, err)
			}
			if _, err := currentstore.PrepareClosedCurrentStoreForPublication(
				ctx,
				fixture.databasePath,
			); err != nil {
				t.Fatalf("prepare same-Binding corruption fixture: %v", err)
			}

			beforeBasis, beforeControl, beforeCatalog := moduleApplyE4PublishedBasisV1(
				t,
				fixture.databasePath,
			)
			currentDefinition, found := beforeControl.FindProfile("deepseek-chat")
			if !found {
				t.Fatal("current same-Binding Profile is absent")
			}
			currentBinding := moduleApplyExactModelBindingV1(
				t,
				beforeControl,
				"deepseek-chat",
			)
			currentEntry, found := beforeCatalog.FindInstance(currentBinding.InstanceID)
			if !found {
				t.Fatal("current same-Binding Model Catalog entry is absent")
			}
			if beforeBasis.PointerRevision != setup.PointerRevision ||
				beforeBasis.Control.SnapshotID != setup.ControlSnapshotID ||
				beforeBasis.Catalog.GenerationID != setup.CatalogGenerationID {
				t.Fatalf("same-Binding corruption fixture basis drifted: %+v", beforeBasis)
			}

			planCanonical := moduleApplyE4ModelPlanV1(
				t,
				beforeBasis.PointerRevision,
				"deepseek-chat",
				config,
				authority,
				desiredProfile,
			)
			input := fixture.commandInputV1(
				t,
				planCanonical,
				moduleApplyModelSecretRefV1,
			)
			candidateProfile, _, err := moduleApplyPlannedModelProfileV1(
				input.Plan,
				currentEntry.Activation,
			)
			if err != nil {
				t.Fatalf("derive candidate same-Binding ModelProfile: %v", err)
			}
			if reflect.DeepEqual(currentDefinition.ModelProfile, candidateProfile) {
				t.Fatal("corruption test does not change the ModelProfile desired state")
			}

			targetDigest := ""
			switch test.target {
			case "config":
				targetDigest = currentBinding.ConfigRef
			case "authority":
				targetDigest = currentBinding.AuthorityCeilingRef
			case "profile":
				if currentDefinition.ModelProfile == nil {
					t.Fatal("old ModelProfile is absent before corruption")
				}
				targetDigest = currentDefinition.ModelProfile.Digest
			default:
				t.Fatalf("unknown corruption target %q", test.target)
			}
			moduleApplyE4TamperContentV1(
				t,
				fixture.databasePath,
				targetDigest,
				test.damage,
			)
			beforeCounts := moduleApplyMutationRowCountsV1(t, fixture.databasePath)

			if result, err := dryRunModulePlanV1(ctx, input); err == nil ||
				moduleApplyFailureCodeOfV1(err) != moduleApplyFailureStore {
				t.Fatalf(
					"same-Binding Profile dry-run accepted corrupted current closure: result=%+v error=%v code=%s",
					result,
					err,
					moduleApplyFailureCodeOfV1(err),
				)
			}
			moduleApplyE4AssertFailedProfileCandidateV1(
				t,
				fixture,
				beforeBasis,
				beforeCounts,
				targetDigest,
				test.damage,
				candidateProfile,
			)

			if result, err := applyModulePlanV1(ctx, input); err == nil ||
				moduleApplyFailureCodeOfV1(err) != moduleApplyFailureStore {
				t.Fatalf(
					"same-Binding Profile Apply accepted corrupted current closure: result=%+v error=%v code=%s",
					result,
					err,
					moduleApplyFailureCodeOfV1(err),
				)
			}
			moduleApplyE4AssertFailedProfileCandidateV1(
				t,
				fixture,
				beforeBasis,
				beforeCounts,
				targetDigest,
				test.damage,
				candidateProfile,
			)
			assertNoModuleApplyStageResidueV1(t, fixture.root)
		})
	}
}

func moduleApplyE4PublishedBasisV1(
	t *testing.T,
	databasePath string,
) (
	controlcontract.PublishedBasis,
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
) {
	t.Helper()
	observer, err := currentstore.OpenReadOnlyObserver(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open same-Binding Profile observer: %v", err)
	}
	basis, control, catalog, loadErr := observer.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	closeErr := observer.Close()
	if loadErr != nil || closeErr != nil {
		t.Fatalf("load same-Binding Profile basis: %v", errors.Join(loadErr, closeErr))
	}
	return basis, control, catalog
}

func moduleApplyE4TamperContentV1(
	t *testing.T,
	databasePath string,
	digest string,
	damage string,
) {
	t.Helper()
	var result sql.Result
	switch damage {
	case "missing":
		result = execCmdClosedFileTamperV1(
			t,
			databasePath,
			[]string{"content_records_reject_delete"},
			`DELETE FROM content_records WHERE content_digest=?`,
			digest,
		)
	case "damaged":
		result = execCmdClosedFileTamperV1(
			t,
			databasePath,
			[]string{"content_records_reject_update"},
			`UPDATE content_records SET canonical_bytes=x'7b7d', size_bytes=2 WHERE content_digest=?`,
			digest,
		)
	default:
		t.Fatalf("unknown content damage %q", damage)
	}
	affected, rowsErr := result.RowsAffected()
	if rowsErr != nil || affected != 1 {
		t.Fatalf(
			"tamper same-Binding Profile content: affected=%d error=%v",
			affected,
			rowsErr,
		)
	}
}

func moduleApplyE4AssertFailedProfileCandidateV1(
	t *testing.T,
	fixture moduleApplyModelE4FixtureV1,
	wantBasis controlcontract.PublishedBasis,
	wantCounts map[string]int64,
	tamperedDigest string,
	damage string,
	candidateProfile *corecontract.ModelProfileRef,
) {
	t.Helper()
	gotBasis := loadCmdRawPublishedBasisV1(t, fixture.databasePath)
	if !reflect.DeepEqual(gotBasis, wantBasis) {
		t.Fatalf("failed same-Binding Profile candidate published a new basis: got=%+v want=%+v", gotBasis, wantBasis)
	}
	if gotCounts := moduleApplyMutationRowCountsV1(
		t,
		fixture.databasePath,
	); !reflect.DeepEqual(gotCounts, wantCounts) {
		t.Fatalf(
			"failed same-Binding Profile candidate changed Store rows: got=%v want=%v",
			gotCounts,
			wantCounts,
		)
	}

	database, err := sql.Open("sqlite", crashSQLiteURI(fixture.databasePath, "ro"))
	if err != nil {
		t.Fatalf("open Store to verify same-Binding Profile corruption: %v", err)
	}
	var tamperedBytes []byte
	var tamperedSize int64
	tamperedErr := database.QueryRowContext(
		context.Background(),
		"SELECT canonical_bytes, size_bytes FROM content_records WHERE content_digest=?",
		tamperedDigest,
	).Scan(&tamperedBytes, &tamperedSize)
	switch damage {
	case "missing":
		if !errors.Is(tamperedErr, sql.ErrNoRows) {
			_ = database.Close()
			t.Fatalf("failed candidate repaired missing current content: %v", tamperedErr)
		}
	case "damaged":
		if tamperedErr != nil || !bytes.Equal(tamperedBytes, []byte("{}")) || tamperedSize != 2 {
			_ = database.Close()
			t.Fatalf(
				"failed candidate rewrote damaged current content: bytes=%q size=%d error=%v",
				tamperedBytes,
				tamperedSize,
				tamperedErr,
			)
		}
	default:
		_ = database.Close()
		t.Fatalf("unknown content damage %q", damage)
	}
	if candidateProfile != nil {
		var count int64
		if err := database.QueryRowContext(
			context.Background(),
			"SELECT COUNT(*) FROM content_records WHERE content_digest=?",
			candidateProfile.Digest,
		).Scan(&count); err != nil || count != 0 {
			_ = database.Close()
			t.Fatalf("failed candidate persisted new ModelProfile: count=%d error=%v", count, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close same-Binding Profile corruption observer: %v", err)
	}
}

func TestModuleApplyDeepSeekModelBackupGateRejectsCurrentMissingPriceWithoutRun(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newModuleApplyModelE4FixtureV1(
		t,
		"current-v1.deepseek.bootstrap.seed.json",
		moduleApplyProPriceV1(),
	)
	config := moduleApplyModelConfigV1(
		t,
		deepseekmodel.ModelV4Pro,
		localDeepSeekProBuild,
		moduleApplyProPriceV1().PriceSnapshotID,
		json.RawMessage(`{"max_tokens":1024,"temperature":0.2,"thinking":{"type":"disabled"}}`),
	)
	plan := moduleApplyE4ModelPlanV1(
		t,
		fixture.basis.PointerRevision,
		"deepseek-chat",
		config,
		moduleApplyModelAuthorityV1(t),
		nil,
	)
	if result, err := applyModulePlanV1(
		ctx,
		fixture.commandInputV1(t, plan, moduleApplyModelSecretRefV1),
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("apply Model before backup tamper: result=%+v error=%v", result, err)
	}

	database, err := sql.Open("sqlite", fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var runs int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM runs").Scan(&runs); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	result, err := database.ExecContext(
		ctx,
		"DELETE FROM model_price_snapshots WHERE price_snapshot_id=?",
		moduleApplyProPriceV1().PriceSnapshotID,
	)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	affected, rowsErr := result.RowsAffected()
	closeErr := database.Close()
	if rowsErr != nil || closeErr != nil || affected != 1 || runs != 0 {
		t.Fatalf(
			"prepare no-Run missing-price tamper: runs=%d affected=%d error=%v",
			runs,
			affected,
			errors.Join(rowsErr, closeErr),
		)
	}
	if err := currentbackup.VerifyCurrentStoreSemanticClosure(
		ctx,
		fixture.databasePath,
	); err == nil {
		t.Fatal("backup semantic gate accepted current Model binding with missing PriceSnapshot")
	}
}

func TestModuleApplyDeepSeekModelNewRunUsageAndUnknownNeverSubstitute(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := newModuleApplyModelE4FixtureV1(
		t,
		"current-v1.deepseek.bootstrap.seed.json",
		moduleApplyProPriceV1(),
	)
	config := moduleApplyModelConfigV1(
		t,
		deepseekmodel.ModelV4Pro,
		localDeepSeekProBuild,
		moduleApplyProPriceV1().PriceSnapshotID,
		json.RawMessage(`{"max_tokens":1024,"temperature":0.2,"thinking":{"type":"disabled"}}`),
	)
	planCanonical := moduleApplyE4ModelPlanV1(
		t,
		fixture.basis.PointerRevision,
		"deepseek-chat",
		config,
		moduleApplyModelAuthorityV1(t),
		nil,
	)
	if result, err := applyModulePlanV1(
		ctx,
		fixture.commandInputV1(t, planCanonical, moduleApplyModelSecretRefV1),
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("apply Model replacement before dispatch: result=%+v error=%v", result, err)
	}

	transport := &moduleApplyE4DeepSeekTransportV1{failOnCall: 2}
	var resolverMu sync.Mutex
	resolverCalls := 0
	resolver := deepseekmodel.APIKeyResolverFunc(func(
		ctx context.Context,
		identity deepseekmodel.APIKeyIdentity,
	) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resolverMu.Lock()
		resolverCalls++
		resolverMu.Unlock()
		if identity.Provider != deepseekmodel.ProviderNameV1 ||
			identity.Model != deepseekmodel.ModelV4Pro ||
			identity.ModelBuildID != localDeepSeekProBuild ||
			identity.SecretRef != moduleApplyModelSecretRefV1 {
			return nil, fmt.Errorf("unexpected replaced Model identity: %+v", identity)
		}
		return []byte(deepSeekPureChatTestKey), nil
	})
	composition, err := openProductionCompositionWithOptions(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		defaultTenantID,
		productionCompositionOptions{DeepSeek: &productionDeepSeekRuntimeConfig{
			APIKeyResolver: resolver,
			HTTPClient: &http.Client{
				Transport: transport,
				Timeout:   5 * time.Second,
			},
		}},
	)
	if err != nil {
		t.Fatalf("open replaced Model composition: %v", err)
	}
	defer func() {
		if err := composition.Close(); err != nil {
			t.Errorf("close replaced Model composition: %v", err)
		}
	}()

	successInput := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: defaultPrincipalID,
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "deepseek-chat",
		Message:     "Use the newly applied model binding.",
		RequestID:   "w2e4-model-success",
		Deadline:    time.Now().UTC().Round(0).Add(5 * time.Minute),
	}
	success, err := composition.chat.Chat(ctx, successInput)
	if err != nil || success.TerminalResult == nil ||
		success.TerminalResult.State != corecontract.ModelAttemptSucceeded {
		t.Fatalf("replaced Model success=%+v error=%v", success, err)
	}
	successRetry, err := composition.chat.Chat(ctx, successInput)
	if err != nil || successRetry.RunID != success.RunID ||
		successRetry.AdmissionCreated || successRetry.TerminalResult == nil ||
		successRetry.TerminalResult.AttemptID != success.TerminalResult.AttemptID {
		t.Fatalf("replaced Model exact retry=%+v error=%v", successRetry, err)
	}
	record, err := composition.store.GetModelDispatchRecord(
		ctx,
		success.TerminalResult.AttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.Attempt.Provider != deepseekmodel.ProviderNameV1 ||
		record.Attempt.Model != deepseekmodel.ModelV4Pro ||
		record.Attempt.PriceSnapshotID != moduleApplyProPriceV1().PriceSnapshotID ||
		record.Usage.EstimatedCost == nil || *record.Usage.EstimatedCost != "0.0001008" ||
		!moduleApplyE4TokenEquals(record.Usage.Tokens.Input, 100) ||
		!moduleApplyE4TokenEquals(record.Usage.Tokens.CachedInput, 40) ||
		!moduleApplyE4TokenEquals(record.Usage.Tokens.UncachedInput, 60) ||
		!moduleApplyE4TokenEquals(record.Usage.Tokens.Output, 20) ||
		!moduleApplyE4TokenEquals(record.Usage.Tokens.Reasoning, 5) {
		t.Fatalf("replaced Model Attempt/Usage=%+v", record)
	}

	unknownInput := successInput
	unknownInput.Message = "This post-dispatch ambiguity must never switch models."
	unknownInput.RequestID = "w2e4-model-unknown"
	unknown, err := composition.chat.Chat(ctx, unknownInput)
	if err != nil || unknown.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		unknown.LoopResult.ReasonCode != string(modulehost.UnknownClassInvokeReturnedError) ||
		unknown.TerminalResult != nil {
		t.Fatalf("replaced Model UNKNOWN=%+v error=%v", unknown, err)
	}
	unknownRetry, err := composition.chat.Chat(ctx, unknownInput)
	if err != nil || unknownRetry.RunID != unknown.RunID ||
		unknownRetry.AdmissionCreated ||
		unknownRetry.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation {
		t.Fatalf("replaced Model UNKNOWN retry=%+v error=%v", unknownRetry, err)
	}
	unsettled, err := composition.store.ScanUnsettledModelDispatchRecords(ctx, unknown.RunID)
	if err != nil || len(unsettled) != 1 ||
		unsettled[0].Attempt.Model != deepseekmodel.ModelV4Pro ||
		unsettled[0].Attempt.State != corecontract.ModelAttemptUnknown {
		t.Fatalf("replaced Model UNKNOWN ledger=%+v error=%v", unsettled, err)
	}
	models := transport.models()
	resolverMu.Lock()
	gotResolverCalls := resolverCalls
	resolverMu.Unlock()
	if !reflect.DeepEqual(models, []string{
		deepseekmodel.ModelV4Pro,
		deepseekmodel.ModelV4Pro,
	}) || gotResolverCalls != 2 {
		t.Fatalf("Model replacement replayed or substituted: models=%v resolver_calls=%d", models, gotResolverCalls)
	}
}

func TestModuleApplyDeepSeekModelSameAgentDifferentWorkspaceProfiles(t *testing.T) {
	ctx := context.Background()
	fixture := newModuleApplyModelE4FixtureV1(
		t,
		"s3c-deepseek-v4-flash-reviewer-on.bootstrap.seed.json",
		moduleApplyProPriceV1(),
	)
	config := moduleApplyModelConfigV1(
		t,
		deepseekmodel.ModelV4Pro,
		localDeepSeekProBuild,
		moduleApplyProPriceV1().PriceSnapshotID,
		json.RawMessage(`{"max_tokens":1024,"temperature":0.2,"thinking":{"type":"disabled"}}`),
	)
	planCanonical := moduleApplyE4ModelPlanV1(
		t,
		fixture.basis.PointerRevision,
		"s3c.coordinator",
		config,
		moduleApplyModelAuthorityV1(t),
		nil,
	)
	if result, err := applyModulePlanV1(
		ctx,
		fixture.commandInputV1(t, planCanonical, moduleApplyModelSecretRefV1),
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("apply one Profile Model replacement: result=%+v error=%v", result, err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	coordinator := moduleApplyE4CompileSnapshotV1(
		t,
		basis,
		control,
		catalog,
		"s3c-ws-frontend",
		"s3c.architect",
		"s3c.coordinator",
		"coordinator",
	)
	backend := moduleApplyE4CompileSnapshotV1(
		t,
		basis,
		control,
		catalog,
		"s3c-ws-backend",
		"s3c.architect",
		"s3c.backend",
		"backend",
	)
	coordinatorBinding := moduleApplySnapshotModelBindingV1(t, coordinator)
	backendBinding := moduleApplySnapshotModelBindingV1(t, backend)
	coordinatorRecord, coordinatorErr := store.GetContent(ctx, coordinatorBinding.ConfigRef)
	backendRecord, backendErr := store.GetContent(ctx, backendBinding.ConfigRef)
	closeErr := store.Close()
	if err := errors.Join(coordinatorErr, backendErr, closeErr); err != nil {
		t.Fatal(err)
	}
	coordinatorConfig, err := moduleapi.RestoreModelBindingConfigV1(
		coordinatorRecord.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	backendConfig, err := moduleapi.RestoreModelBindingConfigV1(backendRecord.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	if coordinator.Agent.ID != backend.Agent.ID ||
		coordinator.Agent.ID != "s3c.architect" ||
		coordinator.Workspace.ID == backend.Workspace.ID ||
		coordinator.Profile.ID == backend.Profile.ID ||
		coordinatorBinding.ConfigRef == backendBinding.ConfigRef ||
		coordinatorConfig.Model != deepseekmodel.ModelV4Pro ||
		backendConfig.Model != deepseekmodel.ModelV4Flash {
		t.Fatalf(
			"same Agent Workspace/Profile Model isolation failed: coordinator=%+v/%+v backend=%+v/%+v",
			coordinator,
			coordinatorConfig,
			backend,
			backendConfig,
		)
	}
}

type moduleApplyModelE4FixtureV1 struct {
	root              string
	databasePath      string
	artifactRoot      string
	artifactDirectory string
	basis             controlcontract.PublishedBasis
	control           controlcontract.ControlSnapshot
	catalog           controlcontract.CatalogGeneration
}

func newModuleApplyModelE4FixtureV1(
	t *testing.T,
	seedFile string,
	prices ...corecontract.ModelPriceSnapshotV1,
) moduleApplyModelE4FixtureV1 {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(filepath.Dir(exampleSeedPath(t)), seedFile)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize W2-E4 Model fixture: %v", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, price := range prices {
		if _, err := store.PutModelPriceSnapshot(ctx, price); err != nil {
			_ = store.Close()
			t.Fatalf("put W2-E4 PriceSnapshot %q: %v", price.PriceSnapshotID, err)
		}
	}
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	closeErr := store.Close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	return moduleApplyModelE4FixtureV1{
		root:         root,
		databasePath: databasePath,
		artifactRoot: artifactRoot,
		artifactDirectory: filepath.Join(
			filepath.Dir(seedPath),
			"bootstrap-artifacts",
			localDeepSeekModuleID,
			localDeepSeekVersion,
		),
		basis:   basis,
		control: control,
		catalog: catalog,
	}
}

func (fixture moduleApplyModelE4FixtureV1) commandInputV1(
	t *testing.T,
	planCanonical []byte,
	secretRefGrant string,
) moduleApplyCommandInputV1 {
	t.Helper()
	plan, canonical, digest, err := restoreModuleApplyPlanV1(planCanonical)
	if err != nil {
		t.Fatalf("restore W2-E4 Model Apply plan: %v", err)
	}
	return moduleApplyCommandInputV1{
		DatabasePath:                  fixture.databasePath,
		ArtifactRoot:                  fixture.artifactRoot,
		ArtifactDirectory:             fixture.artifactDirectory,
		TrustedInProcessArtifactGrant: localDeepSeekDigest,
		ModelSecretRefGrant:           secretRefGrant,
		Plan:                          plan,
		PlanCanonical:                 canonical,
		PlanDigest:                    digest,
	}
}

func moduleApplyE4ModelPlanV1(
	t *testing.T,
	expectedPointer uint64,
	profileID string,
	config []byte,
	authority []byte,
	modelProfile []byte,
) []byte {
	t.Helper()
	value := map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(profileID),
		"instance_id":               "model-deepseek-v4-flash",
		"port": map[string]any{
			"name":          productionModelPort.Name,
			"exact_version": productionModelPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  localDeepSeekModuleID,
			"exact_version":       localDeepSeekVersion,
			"artifact_digest":     localDeepSeekDigest,
			"artifact_size_bytes": localDeepSeekSize,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestTrustedInProcess),
				"protocol": moduleapi.RuntimeProtocolGoInProcessV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": 0,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
	}
	if len(modelProfile) != 0 {
		value["model_profile"] = json.RawMessage(modelProfile)
	}
	return canonicalModuleApplyPlanTestJSON(t, value)
}

func moduleApplyE4ModelProfileV1(
	t *testing.T,
	config []byte,
	mutate func(*corecontract.ModelProfileV1),
) []byte {
	t.Helper()
	configRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentConfig,
		"application/json",
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	profile := corecontract.ModelProfileV1{
		SchemaVersion:          corecontract.ModelProfileSchemaVersionV1,
		ID:                     "deepseek-v4-pro-profile",
		Version:                "1.0.0",
		Provider:               deepseekmodel.ProviderNameV1,
		Model:                  deepseekmodel.ModelV4Pro,
		ModelBuildID:           localDeepSeekProBuild,
		ModelConfigRef:         configRef,
		AdapterArtifactDigest:  localDeepSeekDigest,
		AdapterIdentity:        deepseekmodel.AdapterIdentityV1,
		ContextWindowTokens:    131072,
		EvaluationSuite:        "freeagent-w2e4-model-eval",
		EvaluationVersion:      "1.0.0",
		EvaluationResultDigest: strings.Repeat("3", 64),
		CapabilityTendencies: []corecontract.ModelTendencyV1{{
			MetricID:         "coding",
			ScoreBasisPoints: 8500,
		}},
		ReliabilityTendencies: []corecontract.ModelTendencyV1{{
			MetricID:         "instruction-following",
			ScoreBasisPoints: 8000,
		}},
	}
	if mutate != nil {
		mutate(&profile)
	}
	_, _, canonical, err := corecontract.NewModelProfileV1(profile)
	if err != nil {
		t.Fatalf("build W2-E4 ModelProfile: %v", err)
	}
	return canonical
}

func moduleApplyE4PriceV1(
	id string,
	provider string,
	model string,
	billing string,
) corecontract.ModelPriceSnapshotV1 {
	price := moduleApplyProPriceV1()
	price.PriceSnapshotID = id
	price.Provider = provider
	price.Model = model
	price.BillingVersion = billing
	return price
}

func moduleApplyE4CompileSnapshotV1(
	t *testing.T,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	workspaceID string,
	agentID string,
	profileID string,
	suffix string,
) corecontract.MemberExecutionSnapshot {
	t.Helper()
	ctx := context.Background()
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil || controlRef != basis.Control {
		t.Fatalf("freeze W2-E4 Control: ref=%+v error=%v", controlRef, err)
	}
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil || catalogRef != basis.Catalog {
		t.Fatalf("freeze W2-E4 Catalog: ref=%+v error=%v", catalogRef, err)
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
		SchemaVersion: corecontract.TaskInputSchemaVersionV1,
		Text:          "compile isolated Model binding " + suffix,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentTaskInput,
		"application/json",
		taskCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:          defaultTenantID,
			AdmissionKey:      "w2e4-workspace-profile-" + suffix,
			PrincipalID:       "w2e4-principal",
			WorkspaceID:       workspaceID,
			AgentID:           agentID,
			ProfileID:         profileID,
			TaskInputRef:      taskRef,
			RequestedPorts:    []moduleapi.PortRef{productionModelPort},
			Deadline:          time.Date(2099, time.January, 2, 3, 4, 5, 0, time.UTC),
			CancellationScope: "run",
			ExplicitLimits:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		ctx,
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            "run-w2e4-workspace-profile-" + suffix,
			MemberID:         "member-w2e4-workspace-profile-" + suffix,
			RecoveryRootRef:  "recovery/w2e4-workspace-profile-" + suffix,
			PublishedBasis:   basis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("compile W2-E4 Workspace/Profile %s: %v", suffix, err)
	}
	snapshot, err := corecontract.RestoreMemberExecutionSnapshot(
		compiled.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

type moduleApplyE4DeepSeekTransportV1 struct {
	mu         sync.Mutex
	failOnCall int
	calls      []string
}

func (transport *moduleApplyE4DeepSeekTransportV1) models() []string {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]string(nil), transport.calls...)
}

func (transport *moduleApplyE4DeepSeekTransportV1) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	if request == nil || request.Method != http.MethodPost ||
		request.URL.String() != deepSeekPureChatOfficialURL ||
		request.Header.Get("Authorization") != "Bearer "+deepSeekPureChatTestKey {
		return nil, errors.New("unexpected W2-E4 DeepSeek request")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	if err := request.Body.Close(); err != nil {
		return nil, err
	}
	var wire struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || wire.Model == "" {
		return nil, errors.Join(err, errors.New("invalid W2-E4 DeepSeek request body"))
	}
	transport.mu.Lock()
	transport.calls = append(transport.calls, wire.Model)
	callNumber := len(transport.calls)
	failOnCall := transport.failOnCall
	transport.mu.Unlock()
	if failOnCall != 0 && callNumber == failOnCall {
		return nil, errors.New("synthetic W2-E4 post-dispatch ambiguity")
	}
	responseBody, err := json.Marshal(map[string]any{
		"id":                 fmt.Sprintf("w2e4-deepseek-response-%d", callNumber),
		"object":             "chat.completion",
		"created":            int64(1785800000),
		"model":              wire.Model,
		"system_fingerprint": "w2e4-model-apply-test-fingerprint",
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": "W2-E4 replaced Model response",
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":            100,
			"prompt_cache_hit_tokens":  40,
			"prompt_cache_miss_tokens": 60,
			"completion_tokens":        20,
			"completion_tokens_details": map[string]any{
				"reasoning_tokens": 5,
			},
			"total_tokens": 120,
		},
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:          io.NopCloser(bytes.NewReader(responseBody)),
		ContentLength: int64(len(responseBody)),
		Request:       request,
	}, nil
}

func moduleApplyE4TokenEquals(value *uint64, want uint64) bool {
	return value != nil && *value == want
}
