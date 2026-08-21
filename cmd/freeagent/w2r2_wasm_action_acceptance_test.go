package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/internal/wasmaction"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	w2r2WASMPublicActionID   = "text.stats"
	w2r2WASMProviderActionID = "example.wasm.text-stats"
	w2r2WASMResultV1         = `{"ok":true}`
)

var w2r2WASMInputSchemaV1 = json.RawMessage(
	`{"additionalProperties":false,"properties":{"text":{"maxLength":8192,"type":"string"}},"required":["text"],"type":"object"}`,
)

// TestW2R2WASMActionProductionLongChainBackupRestoreV1 proves that an exact
// Catalog-selected WASM Action remains dormant until the sole Gateway owns a
// persisted PENDING Attempt, executes with the native Host, freezes the local
// usage receipt, survives backup/restore, and never needs the artifact again
// when an already-terminal request is entered exactly.
func TestW2R2WASMActionProductionLongChainBackupRestoreV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	fixture := newW2R2WASMActionFixtureV1(t, filepath.Join(root, "fixture"))
	applyW2R2WASMActionV1(t, root, databasePath, artifactRoot, fixture)
	sourceBasis, sourceControl, sourceCatalog := loadModuleApplyPublishedStateV1(
		t,
		databasePath,
	)

	composition, observation := openW2R2WASMActionCompositionV1(
		t,
		databasePath,
		artifactRoot,
		fixture,
	)
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = composition.Close()
		}
	})
	provider := w2r2WASMProviderFromStoreV1(t, composition.store, fixture)
	assertW2R2WASMRegistryStateV1(t, composition, provider, false)

	input := moduleApplyChatInputV1(
		"w2r2-wasm-success",
		"execute the pure WASM text action exactly once",
	)
	success, err := composition.chat.Chat(ctx, input)
	if err != nil || !success.AdmissionCreated || success.TerminalResult == nil ||
		success.LoopResult.Disposition != loopapi.DispositionTerminated ||
		success.Reply != exactadapter.TextStatsCompletedAssistantTextV1 ||
		success.FailureCode != "" {
		t.Fatalf("WASM success Chat=%+v error=%v", success, err)
	}
	successAction := w2r1ActionForTerminalRunV1(
		t,
		composition.store,
		success.TerminalResult.AttemptID,
	)
	assertW2R2WASMActionClosureV1(
		t,
		composition.store,
		successAction,
		fixture,
		currentstore.ActionDispatchSucceeded,
	)
	assertW2R2WASMSuccessResultV1(t, successAction)
	assertW2R2WASMRunChainV1(
		t,
		composition,
		databasePath,
		success.RunID,
		success.TerminalResult.AttemptID,
		successAction,
	)
	if observation.GuestExecutions() != 1 || observation.SuccessfulReturns() != 1 {
		t.Fatalf(
			"WASM success guest observation executions=%d successful_returns=%d want 1/1",
			observation.GuestExecutions(),
			observation.SuccessfulReturns(),
		)
	}
	assertW2R2WASMRegistryStateV1(t, composition, provider, true)

	executionsBeforeRetry := observation.GuestExecutions()
	successfulReturnsBeforeRetry := observation.SuccessfulReturns()
	retry, err := composition.chat.Chat(ctx, input)
	if err != nil || retry.AdmissionCreated || retry.RunID != success.RunID ||
		retry.TerminalResult == nil ||
		retry.TerminalResult.AttemptID != success.TerminalResult.AttemptID ||
		retry.Reply != success.Reply {
		t.Fatalf("WASM success exact retry=%+v error=%v", retry, err)
	}
	if got := w2r1ActionAttemptCountV1(t, databasePath, success.RunID); got != 1 {
		t.Fatalf("WASM success Action Attempts=%d want 1", got)
	}
	afterRetry := w2r1ActionForTerminalRunV1(
		t,
		composition.store,
		retry.TerminalResult.AttemptID,
	)
	if !reflect.DeepEqual(afterRetry, successAction) {
		t.Fatal("WASM exact retry changed the frozen Action closure")
	}
	if observation.GuestExecutions() != executionsBeforeRetry ||
		observation.SuccessfulReturns() != successfulReturnsBeforeRetry {
		t.Fatalf(
			"WASM exact retry re-executed guest: executions=%d successful_returns=%d",
			observation.GuestExecutions(),
			observation.SuccessfulReturns(),
		)
	}

	if err := composition.Close(); err != nil {
		t.Fatalf("close WASM source composition: %v", err)
	}
	closed = true

	bundlePath := filepath.Join(root, "w2r2-wasm.bundle")
	createAndVerifyModuleApplyBundleV1(
		t,
		databasePath,
		artifactRoot,
		bundlePath,
	)
	restoredDatabase, restoredArtifacts := restoreModuleApplyBundleV1(
		t,
		bundlePath,
		filepath.Join(root, "restored"),
	)
	restoredBasis, restoredControl, restoredCatalog := loadModuleApplyPublishedStateV1(
		t,
		restoredDatabase,
	)
	if restoredBasis != sourceBasis ||
		!reflect.DeepEqual(restoredControl, sourceControl) ||
		!reflect.DeepEqual(restoredCatalog, sourceCatalog) {
		t.Fatal("WASM backup/restore changed Control, Catalog or pointer identity")
	}
	restoredStore, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatalf("open restored WASM Store: %v", err)
	}
	restoredAction, readErr := restoredStore.GetActionDispatchRecord(
		ctx,
		successAction.Attempt.AttemptID,
	)
	installation, installationErr := restoredStore.GetModuleInstallationByIdentity(
		ctx,
		fixture.ModuleID,
		fixture.Version,
	)
	closeErr := restoredStore.Close()
	if joined := errors.Join(readErr, installationErr, closeErr); joined != nil {
		t.Fatalf("read restored WASM closure: %v", joined)
	}
	if !reflect.DeepEqual(restoredAction, successAction) ||
		installation.ArtifactDigest != fixture.ArtifactDigest {
		t.Fatal("WASM backup/restore changed the artifact or Attempt closure")
	}
	assertW2R2WASMArtifactFilesV1(t, restoredArtifacts, fixture.ArtifactDigest)

	// Removing the restored artifact is safe inside this test-only root. An
	// exact terminal retry must use only frozen Store facts and must not invoke
	// the lazy loader, compile a guest, create a Run, or create an Attempt.
	if err := os.RemoveAll(filepath.Join(restoredArtifacts, fixture.ArtifactDigest)); err != nil {
		t.Fatalf("remove restored WASM artifact: %v", err)
	}
	restoredComposition, restoredObservation := openW2R2WASMActionCompositionV1(
		t,
		restoredDatabase,
		restoredArtifacts,
		fixture,
	)
	restoredClosed := false
	t.Cleanup(func() {
		if !restoredClosed {
			_ = restoredComposition.Close()
		}
	})
	restoredProvider := w2r2WASMProviderFromStoreV1(
		t,
		restoredComposition.store,
		fixture,
	)
	assertW2R2WASMRegistryStateV1(
		t,
		restoredComposition,
		restoredProvider,
		false,
	)
	if restoredObservation.GuestExecutions() != 0 ||
		restoredObservation.SuccessfulReturns() != 0 {
		t.Fatalf(
			"restored exact retry executed guest: executions=%d successful_returns=%d",
			restoredObservation.GuestExecutions(),
			restoredObservation.SuccessfulReturns(),
		)
	}
	restoredRetry, err := restoredComposition.chat.Chat(ctx, input)
	if err != nil || restoredRetry.AdmissionCreated ||
		restoredRetry.RunID != success.RunID || restoredRetry.TerminalResult == nil ||
		restoredRetry.TerminalResult.AttemptID != success.TerminalResult.AttemptID ||
		restoredRetry.Reply != success.Reply {
		t.Fatalf("restored WASM exact retry=%+v error=%v", restoredRetry, err)
	}
	assertW2R2WASMRegistryStateV1(
		t,
		restoredComposition,
		restoredProvider,
		false,
	)
	if restoredObservation.GuestExecutions() != 0 ||
		restoredObservation.SuccessfulReturns() != 0 {
		t.Fatalf(
			"restored exact retry executed guest: executions=%d successful_returns=%d",
			restoredObservation.GuestExecutions(),
			restoredObservation.SuccessfulReturns(),
		)
	}
	if got := w2r1ActionAttemptCountV1(
		t,
		restoredDatabase,
		success.RunID,
	); got != 1 {
		t.Fatalf("restored WASM Action Attempts=%d want 1", got)
	}
	if err := restoredComposition.Close(); err != nil {
		t.Fatalf("close restored WASM composition: %v", err)
	}
	restoredClosed = true
}

// TestW2R2WASMActionPendingReopenBecomesUnknownWithoutReplayV1 proves that a
// successful guest return followed by terminal transaction failure leaves the
// original Attempt PENDING. Startup recovery changes only that Attempt to
// UNKNOWN; artifact removal and exact re-entry cannot cause semantic replay,
// provider fallback, or a replacement Attempt.
func TestW2R2WASMActionPendingReopenBecomesUnknownWithoutReplayV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newW2R2WASMActionFixtureV1(t, filepath.Join(root, "fixture"))
	applyW2R2WASMActionV1(t, root, databasePath, artifactRoot, fixture)

	composition, observation := openW2R2WASMActionCompositionV1(
		t,
		databasePath,
		artifactRoot,
		fixture,
	)
	provider := w2r2WASMProviderFromStoreV1(t, composition.store, fixture)
	assertW2R2WASMRegistryStateV1(t, composition, provider, false)

	faultDatabase := openModuleApplyRecoveryFaultDBV1(t, databasePath)
	installModuleApplyRecoveryDispatchFaultV1(t, faultDatabase)
	input := moduleApplyChatInputV1(
		"w2r2-wasm-pending-reopen",
		"execute once then lose the terminal persistence transaction",
	)
	failed, chatErr := composition.chat.Chat(ctx, input)
	removeModuleApplyRecoveryDispatchFaultV1(t, faultDatabase)
	if closeErr := faultDatabase.Close(); closeErr != nil {
		_ = composition.Close()
		t.Fatalf("close WASM fault database: %v", closeErr)
	}
	if chatErr == nil || failed.RunID == "" {
		_ = composition.Close()
		t.Fatalf("WASM terminal persistence fault=%+v error=%v", failed, chatErr)
	}
	pending := w2r1OnlyUnsettledActionV1(t, composition.store, failed.RunID)
	if pending.Attempt.State != currentstore.ActionDispatchPending {
		_ = composition.Close()
		t.Fatalf("WASM terminal fault state=%+v", pending)
	}
	if observation.GuestExecutions() != 1 || observation.SuccessfulReturns() != 1 {
		_ = composition.Close()
		t.Fatalf(
			"WASM terminal persistence fault guest observation executions=%d successful_returns=%d want 1/1",
			observation.GuestExecutions(),
			observation.SuccessfulReturns(),
		)
	}
	assertW2R2WASMRegistryStateV1(t, composition, provider, true)
	if got := w2r1ActionAttemptCountV1(t, databasePath, failed.RunID); got != 1 {
		_ = composition.Close()
		t.Fatalf("WASM terminal fault Attempts=%d want 1", got)
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close WASM PENDING composition: %v", err)
	}

	recoveryStore, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("reopen WASM PENDING Store: %v", err)
	}
	if err := runProductionStartupRecovery(ctx, recoveryStore); err != nil {
		_ = recoveryStore.Close()
		t.Fatalf("recover WASM PENDING: %v", err)
	}
	recovered, readErr := recoveryStore.GetActionDispatchRecord(
		ctx,
		pending.Attempt.AttemptID,
	)
	if readErr != nil ||
		recovered.Attempt.AttemptID != pending.Attempt.AttemptID ||
		recovered.Attempt.RunID != pending.Attempt.RunID ||
		recovered.Attempt.State != currentstore.ActionDispatchUnknown ||
		recovered.Attempt.UnknownReason != "RECOVERED_PENDING_AFTER_CRASH" ||
		recovered.Result != nil || recovered.ProviderReceipt != nil {
		t.Fatalf(
			"recovered WASM PENDING=%+v error=%v",
			recovered,
			readErr,
		)
	}
	assertW2R2WASMActionClosureV1(
		t,
		recoveryStore,
		recovered,
		fixture,
		currentstore.ActionDispatchUnknown,
	)
	closeErr := recoveryStore.Close()
	if closeErr != nil {
		t.Fatalf("close recovered WASM Store: %v", closeErr)
	}

	if err := os.RemoveAll(filepath.Join(artifactRoot, fixture.ArtifactDigest)); err != nil {
		t.Fatalf("remove recovered WASM artifact: %v", err)
	}
	reenteredComposition, reenteredObservation := openW2R2WASMActionCompositionV1(
		t,
		databasePath,
		artifactRoot,
		fixture,
	)
	reenteredProvider := w2r2WASMProviderFromStoreV1(
		t,
		reenteredComposition.store,
		fixture,
	)
	assertW2R2WASMRegistryStateV1(
		t,
		reenteredComposition,
		reenteredProvider,
		false,
	)
	reentered, retryErr := reenteredComposition.chat.Chat(ctx, input)
	closeErr = reenteredComposition.Close()
	if retryErr != nil || closeErr != nil || reentered.AdmissionCreated ||
		reentered.RunID != failed.RunID || reentered.TerminalResult != nil ||
		reentered.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation {
		t.Fatalf(
			"re-enter recovered WASM UNKNOWN=%+v error=%v",
			reentered,
			errors.Join(retryErr, closeErr),
		)
	}
	assertW2R2WASMRegistryStateV1(
		t,
		reenteredComposition,
		reenteredProvider,
		false,
	)
	if got := w2r1ActionAttemptCountV1(t, databasePath, failed.RunID); got != 1 {
		t.Fatalf("recovered WASM Action Attempts=%d want 1", got)
	}
	if reenteredObservation.GuestExecutions() != 0 ||
		reenteredObservation.SuccessfulReturns() != 0 {
		t.Fatalf(
			"recovered WASM UNKNOWN re-entry executed guest: executions=%d successful_returns=%d",
			reenteredObservation.GuestExecutions(),
			reenteredObservation.SuccessfulReturns(),
		)
	}
}

// TestW2R2WASMActionBoundRuntimeDefaultsOffV1 proves that installing and
// binding an exact WASM Action does not silently enable guest execution. The
// ordinary production composition must fail admission before any Run or
// Attempt row when the process-owned runtime switch is absent.
func TestW2R2WASMActionBoundRuntimeDefaultsOffV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newW2R2WASMActionFixtureV1(t, filepath.Join(root, "fixture"))
	applyW2R2WASMActionV1(t, root, databasePath, artifactRoot, fixture)

	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open default-off WASM composition: %v", err)
	}
	defer func() {
		if err := composition.Close(); err != nil {
			t.Errorf("close default-off WASM composition: %v", err)
		}
	}()
	provider := w2r2WASMProviderFromStoreV1(t, composition.store, fixture)
	assertW2R2WASMRegistryStateV1(t, composition, provider, false)
	before := readW2E2RuntimeCountsV1(t, databasePath)

	result, chatErr := composition.chat.Chat(
		ctx,
		moduleApplyChatInputV1(
			"w2r2-wasm-default-off",
			"a bound WASM action must remain disabled by default",
		),
	)
	if chatErr == nil ||
		!strings.Contains(chatErr.Error(), "not explicitly enabled") ||
		result.AdmissionCreated || result.RunID != "" || result.TerminalResult != nil {
		t.Fatalf("default-off WASM Chat=%+v error=%v", result, chatErr)
	}
	if after := readW2E2RuntimeCountsV1(t, databasePath); after != before {
		t.Fatalf(
			"default-off WASM changed Run/Attempt rows: before=%+v after=%+v",
			before,
			after,
		)
	}
	assertW2R2WASMRegistryStateV1(t, composition, provider, false)
}

func newW2R2WASMActionFixtureV1(
	t *testing.T,
	root string,
) moduleApplyWASMFixtureV1 {
	t.Helper()
	definition, _, err := moduleapi.NewActionDefinitionV1(
		moduleapi.ActionDefinitionV1{
			ProviderActionID:        w2r2WASMProviderActionID,
			Description:             "Return one deterministic pure-compute WASM value.",
			InputSchema:             bytes.Clone(w2r2WASMInputSchemaV1),
			RequestedEffectClass:    moduleapi.EffectNone,
			RequestedMaxResultBytes: exactadapter.TextStatsMaxResultBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, descriptor, err := wasmaction.NewDescriptorV1(wasmaction.DescriptorV1{
		SchemaVersion: wasmaction.DescriptorSchemaV1,
		ABIVersion:    wasmaction.ABIVersionV1,
		ModulePath:    "content/action.wasm",
		Actions:       []moduleapi.ActionDefinitionV1{definition},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestValue := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "example.wasm.text-stats",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestWASM,
			Protocol:   moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
			Entrypoint: "content/actions.json",
		},
		Provides: []moduleapi.PortRef{productionActionPort},
	}
	encodedManifest, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := moduleapi.CanonicalJSON(encodedManifest)
	if err != nil {
		t.Fatal(err)
	}
	wasm := w2r2WASMStaticResultModuleV1(t, []byte(w2r2WASMResultV1))
	digest, err := moduleapi.ComputeArtifactDigest(
		manifest,
		[]moduleapi.ArtifactFile{
			{Path: "content/action.wasm", Content: wasm},
			{Path: "content/actions.json", Content: descriptor},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           manifestValue.ID,
		Version:            manifestValue.Version,
		ArtifactDigest:     digest,
		InstanceID:         "w2r2-wasm-text-stats-instance",
		ExecutionClass:     moduleapi.ExecutionWASM,
		AdapterIdentity:    wasmaction.AdapterIdentityV1,
		ActivationRevision: 1,
	}
	productionWASMActionWriteArtifact(
		t,
		root,
		provider,
		manifest,
		descriptor,
		wasm,
	)
	return moduleApplyWASMFixtureV1{
		ArtifactDirectory: filepath.Join(root, digest),
		ArtifactDigest:    digest,
		ArtifactSizeBytes: uint64(len(manifest) + len(descriptor) + len(wasm)),
		ModuleID:          provider.ModuleID,
		Version:           provider.Version,
		InstanceID:        provider.InstanceID,
	}
}

func applyW2R2WASMActionV1(
	t *testing.T,
	root string,
	databasePath string,
	artifactRoot string,
	fixture moduleApplyWASMFixtureV1,
) {
	t.Helper()
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "w2r2-wasm-enable.json"),
		newW2R2WASMActionPlanV1(t, fixture, 1),
	)
	result, err := runModuleApplyWASMFixtureV1(
		false,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
		false,
	)
	if err != nil || result.Status != moduleApplyStatusApplied ||
		result.PointerRevision != 2 {
		t.Fatalf("apply W2-R2 WASM module=%+v error=%v", result, err)
	}
}

func newW2R2WASMActionPlanV1(
	t *testing.T,
	fixture moduleApplyWASMFixtureV1,
	expectedPointer uint64,
) []byte {
	t.Helper()
	_, config, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   w2r2WASMPublicActionID,
				ProviderActionID: w2r2WASMProviderActionID,
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
			AllowedProviderActionIDs: []string{w2r2WASMProviderActionID},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           exactadapter.TextStatsMaxResultBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target": moduleApplyProfileBindingTargetTestValue(
			moduleApplyTestProfileID,
		),
		"instance_id": fixture.InstanceID,
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  fixture.ModuleID,
			"exact_version":       fixture.Version,
			"artifact_digest":     fixture.ArtifactDigest,
			"artifact_size_bytes": fixture.ArtifactSizeBytes,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestWASM),
				"protocol": moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
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

func openW2R2WASMActionCompositionV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	fixture moduleApplyWASMFixtureV1,
) (*productionComposition, *w2r2WASMExecutionObservationV1) {
	t.Helper()
	composition, err := openProductionCompositionWithOptions(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{
			WASMAction: &productionWASMActionRuntimeConfig{
				Enabled:                true,
				AllowedArtifactDigests: []string{fixture.ArtifactDigest},
			},
		},
	)
	if err != nil {
		t.Fatalf("open W2-R2 WASM composition: %v", err)
	}
	provider := w2r2WASMProviderFromStoreV1(t, composition.store, fixture)
	observation := &w2r2WASMExecutionObservationV1{}
	observedRegistry := &w2r2WASMObservedRegistryV1{
		delegate:    composition.registry,
		provider:    provider,
		observation: observation,
	}
	loop, err := coreloop.NewUniversalLoop(composition.store, observedRegistry)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("construct observed W2-R2 Universal Loop: %v", err)
	}
	chat, err := newProductionChatService(composition.store, loop, observedRegistry)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("construct observed W2-R2 Chat service: %v", err)
	}
	composition.coreLoop = loop
	composition.loop = loop
	composition.chat = chat
	return composition, observation
}

// w2r2WASMExecutionObservationV1 is a test-only, process-local observation of
// the private native ExecutePrepared boundary. It is deliberately outside the
// production Host, Store, Usage receipt and public module contract.
type w2r2WASMExecutionObservationV1 struct {
	guestExecutions   atomic.Uint64
	successfulReturns atomic.Uint64
}

func (observation *w2r2WASMExecutionObservationV1) GuestExecutions() uint64 {
	if observation == nil {
		return 0
	}
	return observation.guestExecutions.Load()
}

func (observation *w2r2WASMExecutionObservationV1) SuccessfulReturns() uint64 {
	if observation == nil {
		return 0
	}
	return observation.successfulReturns.Load()
}

// w2r2WASMObservedRegistryV1 delegates exact selection and lazy loading to the
// production Registry. It wraps only the one frozen WASM provider after that
// selection, so the acceptance test can observe private execution without
// adding a production hook, alternate loader, or execution path.
type w2r2WASMObservedRegistryV1 struct {
	delegate    modulehost.ExactAdapterRegistry
	provider    moduleapi.ActivatedModuleRef
	observation *w2r2WASMExecutionObservationV1
}

func (registry *w2r2WASMObservedRegistryV1) ResolveExact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
) (modulehost.ModuleInvoker, error) {
	invoker, err := registry.delegate.ResolveExact(
		ctx,
		artifactDigest,
		adapterIdentity,
	)
	if err != nil || artifactDigest != registry.provider.ArtifactDigest ||
		adapterIdentity != registry.provider.AdapterIdentity {
		return invoker, err
	}
	return &w2r2WASMObservedInvokerV1{
		delegate:    invoker,
		observation: registry.observation,
	}, nil
}

type w2r2WASMObservedInvokerV1 struct {
	delegate    modulehost.ModuleInvoker
	observation *w2r2WASMExecutionObservationV1
}

func (invoker *w2r2WASMObservedInvokerV1) Invoke(
	ctx context.Context,
	invocation modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return invoker.delegate.Invoke(ctx, invocation)
}

func (invoker *w2r2WASMObservedInvokerV1) Describe(
	ctx context.Context,
	request moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	provider, ok := invoker.delegate.(moduleapi.ActionProviderV1)
	if !ok {
		return nil, errors.New("W2-R2 observed invoker lacks ActionProviderV1")
	}
	return provider.Describe(ctx, request)
}

func (invoker *w2r2WASMObservedInvokerV1) Prepare(
	ctx context.Context,
	request moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	provider, ok := invoker.delegate.(moduleapi.ActionProviderV1)
	if !ok {
		return nil, errors.New("W2-R2 observed invoker lacks ActionProviderV1")
	}
	return provider.Prepare(ctx, request)
}

func (invoker *w2r2WASMObservedInvokerV1) ExecutePrepared(
	ctx context.Context,
	execution modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	executor, ok := invoker.delegate.(modulehost.ActionExecutor)
	if !ok {
		return moduleapi.ActionExecutionResultV1{}, errors.New(
			"W2-R2 observed invoker lacks private ActionExecutor",
		)
	}
	invoker.observation.guestExecutions.Add(1)
	result, err := executor.ExecutePrepared(ctx, execution)
	if err == nil && result.Outcome == moduleapi.ActionExecutionSucceeded {
		invoker.observation.successfulReturns.Add(1)
	}
	return result, err
}

func w2r2WASMProviderFromStoreV1(
	t *testing.T,
	store *currentstore.Store,
	fixture moduleApplyWASMFixtureV1,
) moduleapi.ActivatedModuleRef {
	t.Helper()
	_, _, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("load W2-R2 WASM Catalog: %v", err)
	}
	entry, found := catalog.FindInstance(fixture.InstanceID)
	if !found || entry.Activation.ArtifactDigest != fixture.ArtifactDigest ||
		entry.Activation.ExecutionClass != moduleapi.ExecutionWASM ||
		entry.Activation.AdapterIdentity != wasmaction.AdapterIdentityV1 {
		t.Fatalf("exact W2-R2 WASM provider=%+v found=%v", entry, found)
	}
	return entry.Activation
}

func assertW2R2WASMRegistryStateV1(
	t *testing.T,
	composition *productionComposition,
	provider moduleapi.ActivatedModuleRef,
	want bool,
) {
	t.Helper()
	registered, err := composition.registry.IsRegistered(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	)
	if err != nil || registered != want {
		t.Fatalf("WASM Registry state=%v want=%v error=%v", registered, want, err)
	}
}

func assertW2R2WASMRunChainV1(
	t *testing.T,
	composition *productionComposition,
	databasePath string,
	runID string,
	terminalModelAttemptID string,
	action currentstore.ActionDispatchRecord,
) {
	t.Helper()
	models, actions := readW2E2RunAttemptCountsV1(t, databasePath, runID)
	if models != 2 || actions != 1 {
		t.Fatalf(
			"W2-R2 chain has Model/Action Attempts=%d/%d want 2/1",
			models,
			actions,
		)
	}
	terminal, err := composition.store.GetModelDispatchRecord(
		context.Background(),
		terminalModelAttemptID,
	)
	if err != nil || terminal.Attempt.RunID != runID ||
		terminal.Attempt.LogicalStepID != corecontract.SecondModelLogicalStepIDV1 ||
		terminal.Attempt.SourceDispatchAttemptID != action.Attempt.AttemptID {
		t.Fatalf("W2-R2 terminal Model does not reference Action=%+v error=%v", terminal, err)
	}
	source, err := composition.store.GetModelDispatchRecord(
		context.Background(),
		action.Attempt.SourceModelAttemptID,
	)
	if err != nil || source.Attempt.RunID != runID ||
		source.Attempt.LogicalStepID != corecontract.FirstModelLogicalStepIDV1 ||
		source.Attempt.SourceDispatchAttemptID != "" {
		t.Fatalf("W2-R2 Action source Model=%+v error=%v", source, err)
	}
}

func assertW2R2WASMActionClosureV1(
	t *testing.T,
	store *currentstore.Store,
	record currentstore.ActionDispatchRecord,
	fixture moduleApplyWASMFixtureV1,
	wantState currentstore.ActionDispatchState,
) {
	t.Helper()
	provider := record.Attempt.Binding.Provider
	if record.Attempt.State != wantState ||
		record.Attempt.PublicActionID != w2r2WASMPublicActionID ||
		record.Attempt.ProviderActionID != w2r2WASMProviderActionID ||
		provider.ArtifactDigest != fixture.ArtifactDigest ||
		provider.ExecutionClass != moduleapi.ExecutionWASM ||
		provider.AdapterIdentity != wasmaction.AdapterIdentityV1 {
		t.Fatalf("W2-R2 WASM Attempt closure=%+v", record)
	}
	content, err := store.GetContent(
		context.Background(),
		record.Attempt.Binding.ConfigRef,
	)
	if err != nil {
		t.Fatalf("read W2-R2 WASM Config: %v", err)
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(content.CanonicalBytes)
	if err != nil || config.SchemaVersion != moduleapi.ActionBindingConfigSchemaV1 ||
		len(config.Actions) != 1 ||
		config.Actions[0].PublicActionID != w2r2WASMPublicActionID ||
		config.Actions[0].ProviderActionID != w2r2WASMProviderActionID ||
		config.Actions[0].LocalEffectClass != moduleapi.EffectNone ||
		config.Actions[0].MaxResultBytes != exactadapter.TextStatsMaxResultBytesV1 ||
		!bytes.Equal(config.Parameters, []byte(`{}`)) {
		t.Fatalf("restore W2-R2 WASM Config=%+v error=%v", config, err)
	}
	authorityContent, err := store.GetContent(
		context.Background(),
		record.Attempt.Binding.AuthorityCeilingRef,
	)
	if err != nil {
		t.Fatalf("read W2-R2 WASM Authority: %v", err)
	}
	authority, err := moduleapi.RestoreActionAuthorityCeilingV1(
		authorityContent.CanonicalBytes,
	)
	if err != nil || authority.SchemaVersion != moduleapi.ActionAuthorityCeilingSchemaV1 ||
		authority.TenantID != defaultTenantID ||
		!reflect.DeepEqual(authority.AllowedWorkspaceIDs, []string{defaultWorkspaceID}) ||
		!reflect.DeepEqual(
			authority.AllowedProviderActionIDs,
			[]string{w2r2WASMProviderActionID},
		) || authority.MaxEffectClass != moduleapi.EffectNone ||
		authority.MaxResultBytes != exactadapter.TextStatsMaxResultBytesV1 {
		t.Fatalf("restore W2-R2 WASM Authority=%+v error=%v", authority, err)
	}
}

func assertW2R2WASMSuccessResultV1(
	t *testing.T,
	record currentstore.ActionDispatchRecord,
) {
	t.Helper()
	if record.Result == nil || record.ProviderReceipt == nil {
		t.Fatalf("WASM success lacks result or receipt: %+v", record)
	}
	var envelope struct {
		SchemaVersion    string          `json:"schema_version"`
		PublicActionID   string          `json:"public_action_id"`
		DefinitionDigest string          `json:"definition_digest"`
		Status           string          `json:"status"`
		Result           json.RawMessage `json:"result"`
	}
	resultDecoder := json.NewDecoder(bytes.NewReader(record.Result.CanonicalBytes))
	resultDecoder.DisallowUnknownFields()
	if err := resultDecoder.Decode(&envelope); err != nil ||
		envelope.SchemaVersion != "action-result/v1" ||
		envelope.PublicActionID != w2r2WASMPublicActionID ||
		envelope.DefinitionDigest != record.Attempt.DefinitionDigest ||
		envelope.Status != "AVAILABLE" ||
		!bytes.Equal(envelope.Result, []byte(w2r2WASMResultV1)) {
		t.Fatalf("WASM success result=%s error=%v", record.Result.CanonicalBytes, err)
	}
	receiptCanonical := record.ProviderReceipt.CanonicalBytes
	canonical, err := moduleapi.CanonicalJSON(receiptCanonical)
	if err != nil || !bytes.Equal(canonical, receiptCanonical) {
		t.Fatalf("WASM usage receipt is not canonical: %s error=%v", receiptCanonical, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(receiptCanonical))
	decoder.DisallowUnknownFields()
	var receipt wasmaction.UsageReceiptV1
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatalf("decode WASM usage receipt: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("WASM usage receipt has trailing JSON: %v", err)
	}
	if receipt.SchemaVersion != wasmaction.UsageReceiptSchemaV1 ||
		wasmaction.EngineIdentityV1 != "wazero-interpreter/v1.12.0" ||
		receipt.EngineIdentity != "wazero-interpreter/v1.12.0" ||
		receipt.ABIVersion != wasmaction.ABIVersionV1 ||
		receipt.InputBytes == 0 ||
		receipt.OutputBytes != uint32(len(w2r2WASMResultV1)) ||
		receipt.ObservedMemoryPages == 0 ||
		receipt.InstructionMetering != "UNSUPPORTED" {
		t.Fatalf("WASM usage receipt=%+v", receipt)
	}
	lower := strings.ToLower(string(receiptCanonical))
	for _, forbidden := range []string{"token", "cost", "price"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("WASM usage receipt contains forbidden %q field: %s", forbidden, lower)
		}
	}
}

func assertW2R2WASMArtifactFilesV1(
	t *testing.T,
	artifactRoot string,
	digest string,
) {
	t.Helper()
	for _, relative := range []string{
		moduleapi.ArtifactManifestPath,
		"content/actions.json",
		"content/action.wasm",
	} {
		info, err := os.Stat(filepath.Join(artifactRoot, digest, relative))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("restored WASM file %q info=%v error=%v", relative, info, err)
		}
	}
}

func w2r2WASMStaticResultModuleV1(t *testing.T, output []byte) []byte {
	t.Helper()
	const outputPointer = uint32(2048)
	packed := uint64(outputPointer)<<32 | uint64(len(output))
	module := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	typeSection := []byte{
		0x02,
		0x60, 0x01, 0x7f, 0x01, 0x7f,
		0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7e,
	}
	module = w2r2AppendWASMSectionV1(module, 1, typeSection)
	module = w2r2AppendWASMSectionV1(module, 3, []byte{0x02, 0x00, 0x01})
	module = w2r2AppendWASMSectionV1(module, 5, []byte{0x01, 0x01, 0x01, 0x02})
	exports := w2r2AppendVarUint32V1(nil, 3)
	exports = w2r2AppendWASMExportV1(exports, wasmaction.MemoryExportV1, 0x02, 0)
	exports = w2r2AppendWASMExportV1(exports, wasmaction.AllocateExportV1, 0x00, 0)
	exports = w2r2AppendWASMExportV1(exports, wasmaction.ExecuteExportV1, 0x00, 1)
	module = w2r2AppendWASMSectionV1(module, 7, exports)
	allocateBody := w2r2AppendVarInt64V1([]byte{0x00, 0x41}, 1024)
	allocateBody = append(allocateBody, 0x0b)
	executeBody := w2r2AppendVarInt64V1([]byte{0x00, 0x42}, int64(packed))
	executeBody = append(executeBody, 0x0b)
	code := []byte{0x02}
	code = w2r2AppendVarUint32V1(code, uint32(len(allocateBody)))
	code = append(code, allocateBody...)
	code = w2r2AppendVarUint32V1(code, uint32(len(executeBody)))
	code = append(code, executeBody...)
	module = w2r2AppendWASMSectionV1(module, 10, code)
	data := []byte{0x01, 0x00, 0x41}
	data = w2r2AppendVarInt64V1(data, int64(outputPointer))
	data = append(data, 0x0b)
	data = w2r2AppendVarUint32V1(data, uint32(len(output)))
	data = append(data, output...)
	module = w2r2AppendWASMSectionV1(module, 11, data)
	if err := wasmaction.ValidateModuleV1(context.Background(), module); err != nil {
		t.Fatalf("W2-R2 static result WASM is invalid: %v", err)
	}
	return module
}

func w2r2AppendWASMSectionV1(module []byte, id byte, payload []byte) []byte {
	module = append(module, id)
	module = w2r2AppendVarUint32V1(module, uint32(len(payload)))
	return append(module, payload...)
}

func w2r2AppendWASMExportV1(
	payload []byte,
	name string,
	kind byte,
	index uint32,
) []byte {
	payload = w2r2AppendVarUint32V1(payload, uint32(len(name)))
	payload = append(payload, name...)
	payload = append(payload, kind)
	return w2r2AppendVarUint32V1(payload, index)
}

func w2r2AppendVarUint32V1(output []byte, value uint32) []byte {
	for {
		current := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			current |= 0x80
		}
		output = append(output, current)
		if value == 0 {
			return output
		}
	}
}

func w2r2AppendVarInt64V1(output []byte, value int64) []byte {
	for {
		current := byte(value & 0x7f)
		value >>= 7
		signSet := current&0x40 != 0
		done := (value == 0 && !signSet) || (value == -1 && signSet)
		if !done {
			current |= 0x80
		}
		output = append(output, current)
		if done {
			return output
		}
	}
}
