package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const moduleApplyModelSecretRefV1 = "placeholder"

func TestRestoreModuleApplyPlanV1ModelReplacementBoundaries(t *testing.T) {
	config := moduleApplyModelConfigV1(
		t,
		deepseekmodel.ModelV4Pro,
		localDeepSeekProBuild,
		"price-deepseek-v4-pro-module-apply",
		json.RawMessage(`{}`),
	)
	authority := moduleApplyModelAuthorityV1(t)
	valid := moduleApplyModelPlanV1(t, 1, config, authority)
	plan, _, _, err := restoreModuleApplyPlanV1(valid)
	if err != nil || plan.Port != productionModelPort || plan.Binding == nil {
		t.Fatalf("valid Model plan=%+v error=%v", plan, err)
	}
	if err := validateModuleApplyModelSecretGrantV1(plan, "wrong-ref"); err == nil {
		t.Fatal("mismatched Model SecretRef grant was accepted")
	}
	if err := validateModuleApplyModelSecretGrantV1(
		plan,
		moduleApplyModelSecretRefV1,
	); err != nil {
		t.Fatalf("exact Model SecretRef grant: %v", err)
	}

	mutate := func(edit func(map[string]any)) []byte {
		var value map[string]any
		if err := json.Unmarshal(valid, &value); err != nil {
			t.Fatal(err)
		}
		edit(value)
		return canonicalModuleApplyPlanTestJSON(t, value)
	}
	for _, test := range []struct {
		name    string
		payload []byte
	}{
		{
			name: "nonzero binding index",
			payload: mutate(func(value map[string]any) {
				value["binding"].(map[string]any)["port_binding_index"] = float64(1)
			}),
		},
		{
			name: "optional Model binding",
			payload: mutate(func(value map[string]any) {
				value["binding"].(map[string]any)["failure_policy"] = string(moduleapi.FailureOptional)
			}),
		},
		{
			name: "Model disable",
			payload: canonicalModuleApplyPlanTestJSON(t, map[string]any{
				"schema_version":            moduleApplyPlanSchemaV1,
				"desired_state":             string(moduleApplyDisabledV1),
				"tenant_id":                 defaultTenantID,
				"expected_pointer_revision": 1,
				"binding_target":            moduleApplyProfileBindingTargetTestValue("deepseek-chat"),
				"instance_id":               "model-deepseek-v4-flash",
				"port": map[string]any{
					"name":          productionModelPort.Name,
					"exact_version": productionModelPort.ExactVersion,
				},
			}),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := restoreModuleApplyPlanV1(test.payload); err == nil {
				t.Fatal("invalid Model plan was accepted")
			}
		})
	}
}

func TestModuleApplyDeepSeekModelMissingPriceFailsBeforeStoreWrites(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.deepseek.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatal(err)
	}
	beforeRows := moduleApplyMutationRowCountsV1(t, databasePath)
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basisBefore, _, _, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	closeErr := store.Close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	config := moduleApplyModelConfigV1(
		t,
		deepseekmodel.ModelV4Pro,
		localDeepSeekProBuild,
		"price-that-does-not-exist",
		json.RawMessage(`{}`),
	)
	canonical := moduleApplyModelPlanV1(
		t,
		basisBefore.PointerRevision,
		config,
		moduleApplyModelAuthorityV1(t),
	)
	plan, frozen, digest, err := restoreModuleApplyPlanV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, err = applyModulePlanV1(ctx, moduleApplyCommandInputV1{
		DatabasePath:                  databasePath,
		ArtifactRoot:                  artifactRoot,
		ArtifactDirectory:             filepath.Join(filepath.Dir(seedPath), "bootstrap-artifacts", localDeepSeekModuleID, localDeepSeekVersion),
		TrustedInProcessArtifactGrant: localDeepSeekDigest,
		ModelSecretRefGrant:           moduleApplyModelSecretRefV1,
		Plan:                          plan,
		PlanCanonical:                 frozen,
		PlanDigest:                    digest,
	})
	if err == nil || moduleApplyFailureCodeOfV1(err) != moduleApplyFailureTarget {
		t.Fatalf("missing PriceSnapshot error=%v", err)
	}
	afterRows := moduleApplyMutationRowCountsV1(t, databasePath)
	store, openErr := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	basisAfter, _, _, readErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	closeErr = store.Close()
	if readErr != nil || closeErr != nil || basisAfter != basisBefore ||
		!reflect.DeepEqual(afterRows, beforeRows) {
		t.Fatalf(
			"missing price changed Store: basis=%+v/%+v rows=%v/%v errors=%v",
			basisBefore,
			basisAfter,
			beforeRows,
			afterRows,
			errors.Join(readErr, closeErr),
		)
	}
}

func TestModuleApplyDeepSeekModelReplacementDryRunRetryRollbackAndOldRunFreeze(
	t *testing.T,
) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.deepseek.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize DeepSeek Model Apply fixture: %v", err)
	}
	artifactDirectory := filepath.Join(
		filepath.Dir(seedPath),
		"bootstrap-artifacts",
		localDeepSeekModuleID,
		localDeepSeekVersion,
	)

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutModelPriceSnapshot(ctx, moduleApplyProPriceV1()); err != nil {
		_ = store.Close()
		t.Fatalf("put Pro PriceSnapshot: %v", err)
	}
	basisBefore, controlBefore, catalogBefore, err := store.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	initialBinding := moduleApplyExactModelBindingV1(t, controlBefore, "deepseek-chat")
	initialConfigRecord, err := store.GetContent(ctx, initialBinding.ConfigRef)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	oldRunCanonical := moduleApplyCommitFrozenRunV1(
		t,
		store,
		basisBefore,
		controlBefore,
		catalogBefore,
	)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	proConfig := moduleApplyModelConfigV1(
		t,
		deepseekmodel.ModelV4Pro,
		localDeepSeekProBuild,
		"price-deepseek-v4-pro-module-apply",
		json.RawMessage(`{"max_tokens":1024,"temperature":0.2,"thinking":{"type":"disabled"}}`),
	)
	authority := moduleApplyModelAuthorityV1(t)
	proPlanCanonical := moduleApplyModelPlanV1(
		t,
		basisBefore.PointerRevision,
		proConfig,
		authority,
	)
	proPlan, proCanonical, proDigest, err := restoreModuleApplyPlanV1(proPlanCanonical)
	if err != nil {
		t.Fatalf("restore Pro Model Apply plan: %v", err)
	}
	input := moduleApplyCommandInputV1{
		DatabasePath:                  databasePath,
		ArtifactRoot:                  artifactRoot,
		ArtifactDirectory:             artifactDirectory,
		TrustedInProcessArtifactGrant: localDeepSeekDigest,
		ModelSecretRefGrant:           moduleApplyModelSecretRefV1,
		Plan:                          proPlan,
		PlanCanonical:                 proCanonical,
		PlanDigest:                    proDigest,
	}
	dry, err := dryRunModulePlanV1(ctx, input)
	if err != nil {
		t.Fatalf("dry-run Pro replacement: %v", err)
	}
	if dry.Status != moduleApplyStatusWouldApply ||
		dry.Changes.Binding.Change != moduleDryRunBindingReplaceV1 ||
		dry.ObservedBasis.PointerRevision != basisBefore.PointerRevision {
		t.Fatalf("Model replacement dry-run=%+v", dry)
	}
	store, err = currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basisAfterDry, _, _, dryReadErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	closeErr := store.Close()
	if dryReadErr != nil || closeErr != nil || basisAfterDry != basisBefore {
		t.Fatalf("dry-run changed published basis: after=%+v error=%v", basisAfterDry, errors.Join(dryReadErr, closeErr))
	}

	applied, err := applyModulePlanV1(ctx, input)
	if err != nil || applied.Status != moduleApplyStatusApplied ||
		applied.PointerRevision != basisBefore.PointerRevision+1 {
		t.Fatalf("apply Pro replacement: result=%+v error=%v", applied, err)
	}
	retried, err := applyModulePlanV1(ctx, input)
	if err != nil || retried.Status != moduleApplyStatusAlreadyApplied ||
		retried.PointerRevision != applied.PointerRevision {
		t.Fatalf("exact Model replacement retry: result=%+v error=%v", retried, err)
	}

	store, err = currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basisPro, controlPro, catalogPro, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	proBinding := moduleApplyExactModelBindingV1(t, controlPro, "deepseek-chat")
	proRecord, err := store.GetContent(ctx, proBinding.ConfigRef)
	if err != nil || !bytes.Equal(proRecord.CanonicalBytes, proConfig) ||
		proBinding.InstanceID != initialBinding.InstanceID ||
		proBinding.AuthorityCeilingRef == initialBinding.AuthorityCeilingRef {
		_ = store.Close()
		t.Fatalf("published Pro Binding=%+v config=%s error=%v", proBinding, proRecord.CanonicalBytes, err)
	}
	beforeEntry, _ := catalogBefore.FindInstance(initialBinding.InstanceID)
	proEntry, _ := catalogPro.FindInstance(proBinding.InstanceID)
	if beforeEntry.Activation != proEntry.Activation {
		_ = store.Close()
		t.Fatalf("Model replacement changed provider activation: before=%+v after=%+v", beforeEntry, proEntry)
	}
	recovery, err := store.ScanStartupRecovery(ctx)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	oldFrameRevision := uint64(0)
	for _, item := range recovery {
		if item.RunID == "run-model-before-apply" {
			oldFrameRevision = item.FrameRevision
			break
		}
	}
	if oldFrameRevision == 0 {
		_ = store.Close()
		t.Fatalf("old Run is absent from recovery projection: %+v", recovery)
	}
	lease, err := store.AcquireRunLease(ctx, currentstore.AcquireRunLeaseInput{
		RunID:                 "run-model-before-apply",
		OwnerID:               "model-apply-old-run-reader",
		ExpectedRunRevision:   0,
		ExpectedFrameRevision: oldFrameRevision,
		TTL:                   time.Hour,
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("acquire old Run: %v", err)
	}
	oldRun, err := store.LoadRunForLoop(ctx, lease)
	if err != nil || !bytes.Equal(oldRun.MemberCanonical, oldRunCanonical) ||
		moduleApplySnapshotModelBindingV1(t, oldRun.Member).ConfigRef != initialBinding.ConfigRef {
		_ = store.ReleaseRunLease(ctx, lease)
		_ = store.Close()
		t.Fatalf("old Run changed after Model replacement: error=%v", err)
	}
	if err := store.ReleaseRunLease(ctx, lease); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	staleRollback := moduleApplyModelPlanV1(
		t,
		basisBefore.PointerRevision,
		initialConfigRecord.CanonicalBytes,
		authority,
	)
	stalePlan, staleCanonical, staleDigest, err := restoreModuleApplyPlanV1(staleRollback)
	if err != nil {
		t.Fatal(err)
	}
	staleInput := input
	staleInput.Plan = stalePlan
	staleInput.PlanCanonical = staleCanonical
	staleInput.PlanDigest = staleDigest
	if _, err := applyModulePlanV1(ctx, staleInput); err == nil ||
		moduleApplyFailureCodeOfV1(err) != moduleApplyFailurePointer {
		t.Fatalf("stale rollback error=%v", err)
	}

	rollbackCanonical := moduleApplyModelPlanV1(
		t,
		basisPro.PointerRevision,
		initialConfigRecord.CanonicalBytes,
		authority,
	)
	rollbackPlan, rollbackBytes, rollbackDigest, err := restoreModuleApplyPlanV1(rollbackCanonical)
	if err != nil {
		t.Fatal(err)
	}
	rollbackInput := input
	rollbackInput.Plan = rollbackPlan
	rollbackInput.PlanCanonical = rollbackBytes
	rollbackInput.PlanDigest = rollbackDigest
	rolledBack, err := applyModulePlanV1(ctx, rollbackInput)
	if err != nil || rolledBack.Status != moduleApplyStatusApplied {
		t.Fatalf("explicit Model rollback: result=%+v error=%v", rolledBack, err)
	}
	store, err = currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, controlRollback, _, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	rollbackBinding := moduleApplyExactModelBindingV1(t, controlRollback, "deepseek-chat")
	rollbackConfig, configErr := store.GetContent(ctx, rollbackBinding.ConfigRef)
	closeErr = store.Close()
	if err != nil || configErr != nil || closeErr != nil ||
		!bytes.Equal(rollbackConfig.CanonicalBytes, initialConfigRecord.CanonicalBytes) {
		t.Fatalf("rollback Config mismatch: errors=%v", errors.Join(err, configErr, closeErr))
	}
}

func moduleApplyModelPlanV1(
	t *testing.T,
	expectedPointer uint64,
	config []byte,
	authority []byte,
) []byte {
	t.Helper()
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue("deepseek-chat"),
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
	})
}

func moduleApplyModelConfigV1(
	t *testing.T,
	model string,
	build string,
	priceID string,
	parameters json.RawMessage,
) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewModelBindingConfigV1(
		moduleapi.ModelBindingConfigV1{
			SchemaVersion:   moduleapi.ModelBindingConfigSchemaV1,
			Provider:        deepseekmodel.ProviderNameV1,
			Model:           model,
			ModelBuildID:    build,
			BillingVersion:  "deepseek-public-price-2026-08-04",
			PriceSnapshotID: priceID,
			Parameters:      parameters,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func moduleApplyModelAuthorityV1(t *testing.T) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewModelAuthorityCeilingV1(
		moduleapi.ModelAuthorityCeilingV1{
			SchemaVersion:                 moduleapi.ModelAuthorityCeilingSchemaV1,
			TenantID:                      defaultTenantID,
			Provider:                      deepseekmodel.ProviderNameV1,
			SecretRef:                     moduleApplyModelSecretRefV1,
			AllowOfficialProviderEndpoint: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func moduleApplyProPriceV1() corecontract.ModelPriceSnapshotV1 {
	return corecontract.ModelPriceSnapshotV1{
		SchemaVersion:   corecontract.ModelPriceSnapshotSchemaVersionV1,
		PriceSnapshotID: "price-deepseek-v4-pro-module-apply",
		Provider:        deepseekmodel.ProviderNameV1,
		Model:           deepseekmodel.ModelV4Pro,
		BillingVersion:  "deepseek-public-price-2026-08-04",
		Currency:        "CNY",
		PricingStatus:   corecontract.PricingKnown,
		Pricing: json.RawMessage(
			`{"cached_input_per_million_microunits":20000,"output_per_million_microunits":2000000,"schema_version":"deepseek-token-pricing/v1","uncached_input_per_million_microunits":1000000}`,
		),
	}
}

func moduleApplyExactModelBindingV1(
	t *testing.T,
	control controlcontract.ControlSnapshot,
	profileID string,
) controlcontract.BindingSpec {
	t.Helper()
	profile, found := control.FindProfile(profileID)
	if !found {
		t.Fatalf("Profile %q is absent", profileID)
	}
	var result *controlcontract.BindingSpec
	for index := range profile.Bindings {
		if profile.Bindings[index].Port != productionModelPort {
			continue
		}
		if result != nil {
			t.Fatal("Profile has duplicate Model Bindings")
		}
		copy := profile.Bindings[index]
		result = &copy
	}
	if result == nil {
		t.Fatal("Profile has no Model Binding")
	}
	return *result
}

func moduleApplySnapshotModelBindingV1(
	t *testing.T,
	snapshot corecontract.MemberExecutionSnapshot,
) moduleapi.PortBinding {
	t.Helper()
	for _, plan := range snapshot.PortPlans {
		if plan.Port == productionModelPort && len(plan.Bindings) == 1 {
			return plan.Bindings[0]
		}
	}
	t.Fatal("snapshot has no exact Model Binding")
	return moduleapi.PortBinding{}
}

func moduleApplyCommitFrozenRunV1(
	t *testing.T,
	store *currentstore.Store,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) []byte {
	t.Helper()
	ctx := context.Background()
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil || controlRef != basis.Control {
		t.Fatalf("freeze current Control: ref=%+v error=%v", controlRef, err)
	}
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil || catalogRef != basis.Catalog {
		t.Fatalf("freeze current Catalog: ref=%+v error=%v", catalogRef, err)
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          "freeze before model replacement",
		},
	)
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
			AdmissionKey:      "model-apply-old-run-admission",
			PrincipalID:       "model-apply-test-principal",
			WorkspaceID:       "local-chat",
			AgentID:           "assistant",
			ProfileID:         "deepseek-chat",
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
			RunID:            "run-model-before-apply",
			MemberID:         "member-model-before-apply",
			RecoveryRootRef:  "recovery/run-model-before-apply",
			PublishedBasis:   basis,
			ControlCanonical: controlCanonical,
			CatalogCanonical: catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := store.CommitRunAdmission(
		ctx,
		currentstore.CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         intentCanonical,
			IntentDigest:            intentDigest,
			MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
			RunManifestCanonical:    compiled.RunManifestCanonical,
			Contents: []currentstore.ContentInput{{
				Digest:         taskRef,
				Kind:           currentstore.ContentTaskInput,
				MediaType:      "application/json",
				CanonicalBytes: taskCanonical,
			}},
		},
	)
	if err != nil || !admitted.Created {
		t.Fatalf("commit old Run: result=%+v error=%v", admitted, err)
	}
	return bytes.Clone(compiled.MemberSnapshotCanonical)
}
