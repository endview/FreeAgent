package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	w2r1RemotePublicActionID = "text.stats"
	w2r1RemoteDescription    = "Count one text value through the W2-R1 REMOTE-shaped acceptance executor."
)

var w2r1RemoteInputSchema = json.RawMessage(
	`{"additionalProperties":false,"properties":{"text":{"maxLength":8192,"type":"string"}},"required":["text"],"type":"object"}`,
)

// TestW2R1RemoteActionProductionLongChainBackupRestoreV1 closes the production
// Store -> Universal Loop -> sole Gateway -> REMOTE-shaped executor boundary
// without adding an injectable HTTP client to the production composition.
// The hardened HTTP transport itself remains covered inside remoteactionhttp.
func TestW2R1RemoteActionProductionLongChainBackupRestoreV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	fixture, definition := newW2R1RemoteActionFixtureV1(t, root)
	applyW2R1RemoteActionV1(t, root, databasePath, artifactRoot, fixture)
	sourceBasis, sourceControl, sourceCatalog := loadModuleApplyPublishedStateV1(
		t,
		databasePath,
	)

	runtimeMaterial := "w2r1-runtime-secret-value-never-persisted-7349"
	executor := &w2r1RemoteActionExecutorV1{
		definition: definition,
		endpoint:   fixture.EndpointURL,
		reference:  fixture.SecretRef,
		material:   runtimeMaterial,
		outcome:    moduleapi.ActionExecutionSucceeded,
	}
	composition := openW2R1RemoteActionTestCompositionV1(
		t,
		databasePath,
		artifactRoot,
		fixture.ArtifactDigest,
		executor,
	)
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = composition.Close()
		}
	})

	if execute, generic, resolutions := executor.counts(); execute != 0 ||
		generic != 0 || resolutions != 0 {
		t.Fatalf(
			"REMOTE executor was touched before a persisted permit: execute=%d generic=%d secret=%d",
			execute,
			generic,
			resolutions,
		)
	}

	successInput := moduleApplyChatInputV1(
		"w2r1-remote-success",
		"execute the REMOTE-shaped text action once",
	)
	success, err := composition.chat.Chat(ctx, successInput)
	if err != nil || !success.AdmissionCreated || success.TerminalResult == nil ||
		success.LoopResult.Disposition != loopapi.DispositionTerminated ||
		success.Reply != exactadapter.TextStatsCompletedAssistantTextV1 ||
		success.FailureCode != "" {
		t.Fatalf("REMOTE success Chat=%+v error=%v", success, err)
	}
	successAction := w2r1ActionForTerminalRunV1(
		t,
		composition.store,
		success.TerminalResult.AttemptID,
	)
	assertW2R1RemoteActionClosureV1(
		t,
		composition.store,
		successAction,
		fixture,
		currentstore.ActionDispatchSucceeded,
	)
	if successAction.Result == nil || successAction.ProviderReceipt == nil {
		t.Fatalf("REMOTE success lacks result/receipt: %+v", successAction)
	}
	if execute, generic, resolutions := executor.counts(); execute != 1 ||
		generic != 0 || resolutions != 1 {
		t.Fatalf(
			"REMOTE success calls execute=%d generic=%d secret=%d",
			execute,
			generic,
			resolutions,
		)
	}

	successRetry, err := composition.chat.Chat(ctx, successInput)
	if err != nil || successRetry.AdmissionCreated ||
		successRetry.RunID != success.RunID ||
		successRetry.TerminalResult == nil ||
		successRetry.TerminalResult.AttemptID != success.TerminalResult.AttemptID {
		t.Fatalf("REMOTE success exact retry=%+v error=%v", successRetry, err)
	}
	if got := w2r1ActionAttemptCountV1(t, databasePath, success.RunID); got != 1 {
		t.Fatalf("REMOTE success Action Attempts=%d want 1", got)
	}
	if execute, generic, resolutions := executor.counts(); execute != 1 ||
		generic != 0 || resolutions != 1 {
		t.Fatalf(
			"REMOTE success retry replayed executor: execute=%d generic=%d secret=%d",
			execute,
			generic,
			resolutions,
		)
	}

	executor.setOutcome(moduleapi.ActionExecutionUnknown)
	unknownInput := moduleApplyChatInputV1(
		"w2r1-remote-unknown",
		"leave the REMOTE-shaped text action uncertain once",
	)
	unknown, err := composition.chat.Chat(ctx, unknownInput)
	if err != nil || !unknown.AdmissionCreated || unknown.TerminalResult != nil ||
		unknown.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		unknown.FailureCode != "" {
		t.Fatalf("REMOTE UNKNOWN Chat=%+v error=%v", unknown, err)
	}
	unknownAction := w2r1OnlyUnsettledActionV1(
		t,
		composition.store,
		unknown.RunID,
	)
	assertW2R1RemoteActionClosureV1(
		t,
		composition.store,
		unknownAction,
		fixture,
		currentstore.ActionDispatchUnknown,
	)
	if unknownAction.Attempt.UnknownReason != "REMOTE_TEST_TRANSPORT_AMBIGUOUS" ||
		unknownAction.Result != nil || unknownAction.ProviderReceipt != nil {
		t.Fatalf("REMOTE UNKNOWN closure=%+v", unknownAction)
	}
	if execute, generic, resolutions := executor.counts(); execute != 2 ||
		generic != 0 || resolutions != 2 {
		t.Fatalf(
			"REMOTE UNKNOWN calls execute=%d generic=%d secret=%d",
			execute,
			generic,
			resolutions,
		)
	}

	unknownRetry, err := composition.chat.Chat(ctx, unknownInput)
	if err != nil || unknownRetry.AdmissionCreated ||
		unknownRetry.RunID != unknown.RunID ||
		unknownRetry.TerminalResult != nil ||
		unknownRetry.LoopResult.Disposition !=
			loopapi.DispositionWaitingReconciliation {
		t.Fatalf("REMOTE UNKNOWN exact retry=%+v error=%v", unknownRetry, err)
	}
	unknownAfterRetry := w2r1OnlyUnsettledActionV1(
		t,
		composition.store,
		unknown.RunID,
	)
	if !reflect.DeepEqual(unknownAfterRetry, unknownAction) ||
		w2r1ActionAttemptCountV1(t, databasePath, unknown.RunID) != 1 {
		t.Fatal("REMOTE UNKNOWN exact retry changed the original Attempt")
	}
	if execute, generic, resolutions := executor.counts(); execute != 2 ||
		generic != 0 || resolutions != 2 {
		t.Fatalf(
			"REMOTE UNKNOWN exact retry replayed executor: execute=%d generic=%d secret=%d",
			execute,
			generic,
			resolutions,
		)
	}

	if err := composition.Close(); err != nil {
		t.Fatalf("close REMOTE source composition: %v", err)
	}
	closed = true

	bundlePath := filepath.Join(root, "w2r1-remote.bundle")
	createAndVerifyModuleApplyBundleV1(
		t,
		databasePath,
		artifactRoot,
		bundlePath,
	)
	assertW2R1PathExcludesBytesV1(t, bundlePath, []byte(runtimeMaterial))
	restoredDatabase, restoredArtifacts := restoreModuleApplyBundleV1(
		t,
		bundlePath,
		filepath.Join(root, "restored"),
	)
	assertW2R1PathExcludesBytesV1(t, restoredDatabase, []byte(runtimeMaterial))
	assertW2R1PathExcludesBytesV1(t, restoredArtifacts, []byte(runtimeMaterial))

	restoredBasis, restoredControl, restoredCatalog := loadModuleApplyPublishedStateV1(
		t,
		restoredDatabase,
	)
	if restoredBasis != sourceBasis ||
		!reflect.DeepEqual(restoredControl, sourceControl) ||
		!reflect.DeepEqual(restoredCatalog, sourceCatalog) {
		t.Fatal("REMOTE backup/restore changed Control, Catalog or pointer identity")
	}
	restoredStore, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatalf("open restored REMOTE Store: %v", err)
	}
	restoredSuccess, successErr := restoredStore.GetActionDispatchRecord(
		ctx,
		successAction.Attempt.AttemptID,
	)
	restoredUnknown, unknownErr := restoredStore.GetActionDispatchRecord(
		ctx,
		unknownAction.Attempt.AttemptID,
	)
	installation, installationErr := restoredStore.GetModuleInstallationByIdentity(
		ctx,
		moduleApplyRemoteModuleIDV1,
		moduleApplyRemoteVersionV1,
	)
	if joined := errors.Join(
		successErr,
		unknownErr,
		installationErr,
	); joined != nil {
		_ = restoredStore.Close()
		t.Fatalf("read restored REMOTE closure: %v", joined)
	}
	assertW2R1RemoteActionClosureV1(
		t,
		restoredStore,
		restoredSuccess,
		fixture,
		currentstore.ActionDispatchSucceeded,
	)
	assertW2R1RemoteActionClosureV1(
		t,
		restoredStore,
		restoredUnknown,
		fixture,
		currentstore.ActionDispatchUnknown,
	)
	closeErr := restoredStore.Close()
	if closeErr != nil {
		t.Fatalf("close restored REMOTE Store: %v", closeErr)
	}
	if !reflect.DeepEqual(restoredSuccess, successAction) ||
		!reflect.DeepEqual(restoredUnknown, unknownAction) ||
		installation.ArtifactDigest != fixture.ArtifactDigest ||
		!bytes.Equal(installation.ManifestBytes, fixture.ManifestCanonical) {
		t.Fatal("REMOTE backup/restore changed artifact, Binding or Attempt closure")
	}
	assertW2R1RemoteArtifactFilesV1(t, fixture, restoredArtifacts)
	if execute, generic, resolutions := executor.counts(); execute != 2 ||
		generic != 0 || resolutions != 2 {
		t.Fatalf(
			"backup/restore touched REMOTE execution capability: execute=%d generic=%d secret=%d",
			execute,
			generic,
			resolutions,
		)
	}
}

// TestW2R1RemoteActionPendingReopenBecomesUnknownWithoutReplayV1 proves that
// a failure after the external executor returns but before terminal outcome
// persistence leaves the original PENDING row. Reopen recovers only that row
// to UNKNOWN and exact re-entry never grants the executor again.
func TestW2R1RemoteActionPendingReopenBecomesUnknownWithoutReplayV1(
	t *testing.T,
) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture, definition := newW2R1RemoteActionFixtureV1(t, root)
	applyW2R1RemoteActionV1(t, root, databasePath, artifactRoot, fixture)

	executor := &w2r1RemoteActionExecutorV1{
		definition: definition,
		endpoint:   fixture.EndpointURL,
		reference:  fixture.SecretRef,
		material:   "w2r1-pending-runtime-secret-never-persisted-9182",
		outcome:    moduleapi.ActionExecutionSucceeded,
	}
	composition := openW2R1RemoteActionTestCompositionV1(
		t,
		databasePath,
		artifactRoot,
		fixture.ArtifactDigest,
		executor,
	)

	faultDatabase := openModuleApplyRecoveryFaultDBV1(t, databasePath)
	installModuleApplyRecoveryDispatchFaultV1(t, faultDatabase)
	input := moduleApplyChatInputV1(
		"w2r1-remote-pending-reopen",
		"execute once then lose the terminal persistence transaction",
	)
	failed, chatErr := composition.chat.Chat(ctx, input)
	removeModuleApplyRecoveryDispatchFaultV1(t, faultDatabase)
	if closeErr := faultDatabase.Close(); closeErr != nil {
		_ = composition.Close()
		t.Fatalf("close REMOTE fault database: %v", closeErr)
	}
	if chatErr == nil || failed.RunID == "" {
		_ = composition.Close()
		t.Fatalf("REMOTE terminal persistence fault=%+v error=%v", failed, chatErr)
	}
	pending := w2r1OnlyUnsettledActionV1(
		t,
		composition.store,
		failed.RunID,
	)
	if pending.Attempt.State != currentstore.ActionDispatchPending {
		_ = composition.Close()
		t.Fatalf("REMOTE terminal fault state=%+v", pending)
	}
	if execute, generic, resolutions := executor.counts(); execute != 1 ||
		generic != 0 || resolutions != 1 {
		_ = composition.Close()
		t.Fatalf(
			"REMOTE terminal fault calls execute=%d generic=%d secret=%d",
			execute,
			generic,
			resolutions,
		)
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close REMOTE PENDING composition: %v", err)
	}

	recoveryStore, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("reopen REMOTE PENDING Store: %v", err)
	}
	if err := runProductionStartupRecovery(ctx, recoveryStore); err != nil {
		_ = recoveryStore.Close()
		t.Fatalf("recover REMOTE PENDING: %v", err)
	}
	recovered, readErr := recoveryStore.GetActionDispatchRecord(
		ctx,
		pending.Attempt.AttemptID,
	)
	closeErr := recoveryStore.Close()
	if readErr != nil || closeErr != nil ||
		recovered.Attempt.AttemptID != pending.Attempt.AttemptID ||
		recovered.Attempt.RunID != pending.Attempt.RunID ||
		recovered.Attempt.State != currentstore.ActionDispatchUnknown ||
		recovered.Attempt.UnknownReason != "RECOVERED_PENDING_AFTER_CRASH" {
		t.Fatalf(
			"recovered REMOTE PENDING=%+v error=%v",
			recovered,
			errors.Join(readErr, closeErr),
		)
	}
	if execute, generic, resolutions := executor.counts(); execute != 1 ||
		generic != 0 || resolutions != 1 {
		t.Fatalf(
			"REMOTE startup recovery replayed executor: execute=%d generic=%d secret=%d",
			execute,
			generic,
			resolutions,
		)
	}

	reenteredComposition := openW2R1RemoteActionTestCompositionV1(
		t,
		databasePath,
		artifactRoot,
		fixture.ArtifactDigest,
		executor,
	)
	reentered, retryErr := reenteredComposition.chat.Chat(ctx, input)
	closeErr = reenteredComposition.Close()
	if retryErr != nil || closeErr != nil || reentered.AdmissionCreated ||
		reentered.RunID != failed.RunID || reentered.TerminalResult != nil ||
		reentered.LoopResult.Disposition !=
			loopapi.DispositionWaitingReconciliation {
		t.Fatalf(
			"re-enter recovered REMOTE UNKNOWN=%+v error=%v",
			reentered,
			errors.Join(retryErr, closeErr),
		)
	}
	if got := w2r1ActionAttemptCountV1(t, databasePath, failed.RunID); got != 1 {
		t.Fatalf("recovered REMOTE Action Attempts=%d want 1", got)
	}
	if execute, generic, resolutions := executor.counts(); execute != 1 ||
		generic != 0 || resolutions != 1 {
		t.Fatalf(
			"REMOTE UNKNOWN exact retry replayed executor: execute=%d generic=%d secret=%d",
			execute,
			generic,
			resolutions,
		)
	}
}

type w2r1RemoteActionExecutorV1 struct {
	mu          sync.Mutex
	store       *currentstore.Store
	provider    moduleapi.ActivatedModuleRef
	definition  moduleapi.ActionDefinitionV1
	endpoint    string
	reference   string
	material    string
	outcome     moduleapi.ActionExecutionOutcomeV1
	execute     int
	generic     int
	resolutions int
}

var _ modulehost.ModuleInvoker = (*w2r1RemoteActionExecutorV1)(nil)
var _ moduleapi.ActionProviderV1 = (*w2r1RemoteActionExecutorV1)(nil)
var _ modulehost.ActionExecutor = (*w2r1RemoteActionExecutorV1)(nil)

func (executor *w2r1RemoteActionExecutorV1) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	executor.mu.Lock()
	executor.generic++
	executor.mu.Unlock()
	return modulehost.InvocationResult{}, errors.New(
		"W2-R1 REMOTE generic invocation is forbidden",
	)
}

func (executor *w2r1RemoteActionExecutorV1) Describe(
	ctx context.Context,
	request moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	if ctx == nil {
		return nil, errors.New("W2-R1 REMOTE Describe context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frozenRequest, _, err := moduleapi.NewActionDescribeRequestV1(request)
	if err != nil {
		return nil, err
	}
	parameters, err := moduleapi.RestoreRemoteActionHTTPBindingParametersV1(
		frozenRequest.Parameters,
	)
	if err != nil || parameters.EndpointURL != executor.endpoint ||
		parameters.SecretRef != executor.reference {
		return nil, errors.New("W2-R1 REMOTE Describe parameters differ")
	}
	definition, _, err := moduleapi.NewActionDefinitionV1(executor.definition)
	if err != nil {
		return nil, err
	}
	return []moduleapi.ActionDefinitionV1{definition}, nil
}

func (executor *w2r1RemoteActionExecutorV1) Prepare(
	ctx context.Context,
	request moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	if ctx == nil {
		return nil, errors.New("W2-R1 REMOTE Prepare context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frozen, _, err := moduleapi.NewActionRequestV1(request)
	if err != nil {
		return nil, err
	}
	if frozen.PublicActionID != w2r1RemotePublicActionID ||
		frozen.ProviderActionID != executor.definition.ProviderActionID {
		return nil, errors.New("W2-R1 REMOTE Prepare identity differs")
	}
	return moduleapi.CanonicalizeAndValidateActionInputV1(
		executor.definition.InputSchema,
		frozen.CanonicalInput,
	)
}

func (executor *w2r1RemoteActionExecutorV1) ExecutePrepared(
	ctx context.Context,
	execution modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	if ctx == nil {
		return moduleapi.ActionExecutionResultV1{}, errors.New(
			"W2-R1 REMOTE Execute context is nil",
		)
	}
	if err := ctx.Err(); err != nil {
		return moduleapi.ActionExecutionResultV1{}, err
	}
	frozen, err := modulehost.NewPreparedActionExecutionV1(execution)
	if err != nil {
		return moduleapi.ActionExecutionResultV1{}, err
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(
		frozen.ConfigCanonical,
	)
	if err != nil {
		return moduleapi.ActionExecutionResultV1{}, err
	}
	parameters, err := moduleapi.RestoreRemoteActionHTTPBindingParametersV1(
		config.Parameters,
	)
	executor.mu.Lock()
	store := executor.store
	provider := executor.provider
	endpoint := executor.endpoint
	reference := executor.reference
	material := executor.material
	executor.mu.Unlock()
	if err != nil || store == nil || frozen.Binding.Provider != provider ||
		parameters.EndpointURL != endpoint ||
		parameters.SecretRef != reference || material == "" ||
		frozen.Request.ProviderActionID != executor.definition.ProviderActionID {
		return moduleapi.ActionExecutionResultV1{}, errors.New(
			"W2-R1 REMOTE private execution closure differs",
		)
	}
	pending, err := store.GetActionDispatchRecord(ctx, frozen.Request.AttemptID)
	if err != nil || pending.Attempt.State != currentstore.ActionDispatchPending ||
		pending.Attempt.Binding.Provider != provider {
		return moduleapi.ActionExecutionResultV1{}, errors.New(
			"W2-R1 REMOTE executor was reached without the persisted PENDING Attempt",
		)
	}
	if _, err := moduleapi.CanonicalizeAndValidateActionInputV1(
		executor.definition.InputSchema,
		frozen.Request.PreparedPayload,
	); err != nil {
		return moduleapi.ActionExecutionResultV1{}, err
	}

	executor.mu.Lock()
	executor.execute++
	executor.resolutions++
	outcome := executor.outcome
	executor.mu.Unlock()

	result := moduleapi.ActionExecutionResultV1{
		SchemaVersion: moduleapi.ActionExecutionResultSchemaV1,
		AttemptID:     frozen.Request.AttemptID,
		Outcome:       outcome,
	}
	switch outcome {
	case moduleapi.ActionExecutionSucceeded:
		result.CanonicalResult = json.RawMessage(`{"ok":true}`)
		result.ProviderReceipt = json.RawMessage(`{"transport":"remote-shaped-test"}`)
		result.ExternalOperationID = "remote-operation-" + frozen.Request.AttemptID
	case moduleapi.ActionExecutionUnknown:
		result.UnknownReason = "REMOTE_TEST_TRANSPORT_AMBIGUOUS"
	default:
		return moduleapi.ActionExecutionResultV1{}, errors.New(
			"W2-R1 REMOTE test outcome is unsupported",
		)
	}
	frozenResult, _, err := moduleapi.NewActionExecutionResultV1(result)
	return frozenResult, err
}

func (executor *w2r1RemoteActionExecutorV1) setOutcome(
	outcome moduleapi.ActionExecutionOutcomeV1,
) {
	executor.mu.Lock()
	executor.outcome = outcome
	executor.mu.Unlock()
}

func (executor *w2r1RemoteActionExecutorV1) counts() (
	execute int,
	generic int,
	resolutions int,
) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return executor.execute, executor.generic, executor.resolutions
}

type w2r1RemoteActionFixtureV1 struct {
	moduleApplyRemoteFixtureV1
	ManifestCanonical   []byte
	DescriptorCanonical []byte
}

func newW2R1RemoteActionFixtureV1(
	t *testing.T,
	root string,
) (w2r1RemoteActionFixtureV1, moduleapi.ActionDefinitionV1) {
	t.Helper()
	definition, _, err := moduleapi.NewActionDefinitionV1(
		moduleapi.ActionDefinitionV1{
			ProviderActionID:        moduleApplyRemoteActionIDV1,
			Description:             w2r1RemoteDescription,
			InputSchema:             bytes.Clone(w2r1RemoteInputSchema),
			RequestedEffectClass:    moduleapi.EffectReadOnly,
			RequestedMaxResultBytes: exactadapter.TextStatsMaxResultBytesV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, descriptorCanonical, err := remoteactionhttp.NewDescriptorV1(
		remoteactionhttp.DescriptorV1{
			SchemaVersion: remoteactionhttp.DescriptorSchemaV1,
			Actions:       []moduleapi.ActionDefinitionV1{definition},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	manifestValue := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         moduleApplyRemoteModuleIDV1,
		Version:    moduleApplyRemoteVersionV1,
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestRemote,
			Protocol:   moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
			Entrypoint: "content/actions.json",
		},
		Provides: []moduleapi.PortRef{productionActionPort},
	}
	encodedManifest, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifestCanonical, err := moduleapi.CanonicalJSON(encodedManifest)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		[]moduleapi.ArtifactFile{{
			Path:    "content/actions.json",
			Content: descriptorCanonical,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	artifactDirectory := filepath.Join(root, "w2r1-remote-source-"+digest[:12])
	if err := os.MkdirAll(filepath.Join(artifactDirectory, "content"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(artifactDirectory, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(artifactDirectory, "content", "actions.json"),
		descriptorCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return w2r1RemoteActionFixtureV1{
		moduleApplyRemoteFixtureV1: moduleApplyRemoteFixtureV1{
			ArtifactDirectory: artifactDirectory,
			ArtifactDigest:    digest,
			ArtifactSizeBytes: uint64(len(manifestCanonical) + len(descriptorCanonical)),
			EndpointURL:       moduleApplyRemoteEndpointV1,
			SecretRef:         moduleApplyRemoteAuthorityReferenceV1,
		},
		ManifestCanonical:   manifestCanonical,
		DescriptorCanonical: descriptorCanonical,
	}, definition
}

func applyW2R1RemoteActionV1(
	t *testing.T,
	root string,
	databasePath string,
	artifactRoot string,
	fixture w2r1RemoteActionFixtureV1,
) {
	t.Helper()
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "w2r1-remote-enable.json"),
		newW2R1RemoteActionPlanV1(t, fixture, 1),
	)
	payload, result, err := runModuleApplyRemoteFixtureV1(
		false,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		moduleApplyRemoteGrantsV1{
			Artifact: fixture.ArtifactDigest,
			Endpoint: fixture.EndpointURL,
			Secret:   fixture.SecretRef,
		},
	)
	if err != nil || result.Status != moduleApplyStatusApplied ||
		result.PointerRevision != 2 {
		t.Fatalf("apply W2-R1 REMOTE module=%+v error=%v", result, err)
	}
	assertModuleApplyRemoteGrantsNotOutputV1(
		t,
		payload,
		fixture.moduleApplyRemoteFixtureV1,
	)
}

func newW2R1RemoteActionPlanV1(
	t *testing.T,
	fixture w2r1RemoteActionFixtureV1,
	expectedPointer uint64,
) []byte {
	t.Helper()
	_, parameters, err := moduleapi.NewRemoteActionHTTPBindingParametersV1(
		moduleapi.RemoteActionHTTPBindingParametersV1{
			SchemaVersion: moduleapi.RemoteActionHTTPBindingParametersSchemaV1,
			EndpointURL:   fixture.EndpointURL,
			SecretRef:     fixture.SecretRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, config, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   w2r1RemotePublicActionID,
				ProviderActionID: moduleApplyRemoteActionIDV1,
				LocalEffectClass: moduleapi.EffectReadOnly,
				MaxResultBytes:   exactadapter.TextStatsMaxResultBytesV1,
			}},
			Parameters: parameters,
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
			AllowedProviderActionIDs: []string{moduleApplyRemoteActionIDV1},
			MaxEffectClass:           moduleapi.EffectReadOnly,
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
		"instance_id": moduleApplyRemoteInstanceIDV1,
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  moduleApplyRemoteModuleIDV1,
			"exact_version":       moduleApplyRemoteVersionV1,
			"artifact_digest":     fixture.ArtifactDigest,
			"artifact_size_bytes": fixture.ArtifactSizeBytes,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestRemote),
				"protocol": moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
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

func openW2R1RemoteActionTestCompositionV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	artifactDigest string,
	executor *w2r1RemoteActionExecutorV1,
) *productionComposition {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open W2-R1 REMOTE Store: %v", err)
	}
	fail := func(cause error) {
		_ = store.Close()
		t.Fatal(cause)
	}
	_, _, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		fail(fmt.Errorf("load W2-R1 REMOTE Catalog: %w", err))
	}
	var provider moduleapi.ActivatedModuleRef
	for _, entry := range catalog.Entries {
		if entry.Activation.ArtifactDigest == artifactDigest &&
			entry.Activation.AdapterIdentity == remoteactionhttp.AdapterIdentityV1 {
			if provider.InstanceID != "" {
				fail(errors.New("duplicate W2-R1 REMOTE provider"))
			}
			provider = entry.Activation
		}
	}
	if provider.InstanceID == "" ||
		provider.ExecutionClass != moduleapi.ExecutionRemote {
		fail(errors.New("exact W2-R1 REMOTE provider is absent"))
	}
	executor.mu.Lock()
	executor.store = store
	executor.provider = provider
	executor.mu.Unlock()
	registry, err := newProductionAdapterRegistryWithExtras(
		artifactRoot,
		catalog.Entries,
		exactadapter.Registration{
			ArtifactDigest:  provider.ArtifactDigest,
			AdapterIdentity: provider.AdapterIdentity,
			Invoker:         executor,
		},
	)
	if err != nil {
		fail(fmt.Errorf("build W2-R1 REMOTE test Registry: %w", err))
	}
	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		fail(fmt.Errorf("build W2-R1 REMOTE Loop: %w", err))
	}
	chat, err := newProductionChatService(store, loop, registry)
	if err != nil {
		fail(fmt.Errorf("build W2-R1 REMOTE Chat: %w", err))
	}
	return &productionComposition{
		store:    store,
		registry: registry,
		coreLoop: loop,
		loop:     loop,
		chat:     chat,
	}
}

func w2r1ActionForTerminalRunV1(
	t *testing.T,
	store *currentstore.Store,
	terminalModelAttemptID string,
) currentstore.ActionDispatchRecord {
	t.Helper()
	model, err := store.GetModelDispatchRecord(
		context.Background(),
		terminalModelAttemptID,
	)
	if err != nil || model.Attempt.SourceDispatchAttemptID == "" {
		t.Fatalf("read W2-R1 terminal Model dispatch=%+v error=%v", model, err)
	}
	action, err := store.GetActionDispatchRecord(
		context.Background(),
		model.Attempt.SourceDispatchAttemptID,
	)
	if err != nil {
		t.Fatalf("read W2-R1 terminal Action dispatch: %v", err)
	}
	return action
}

func w2r1OnlyUnsettledActionV1(
	t *testing.T,
	store *currentstore.Store,
	runID string,
) currentstore.ActionDispatchRecord {
	t.Helper()
	records, err := store.ScanUnsettledActionDispatchRecords(
		context.Background(),
		runID,
	)
	if err != nil || len(records) != 1 {
		t.Fatalf("unsettled W2-R1 REMOTE Actions=%+v error=%v", records, err)
	}
	return records[0]
}

func assertW2R1RemoteActionClosureV1(
	t *testing.T,
	store *currentstore.Store,
	record currentstore.ActionDispatchRecord,
	fixture w2r1RemoteActionFixtureV1,
	wantState currentstore.ActionDispatchState,
) {
	t.Helper()
	provider := record.Attempt.Binding.Provider
	if record.Attempt.State != wantState ||
		record.Attempt.PublicActionID != w2r1RemotePublicActionID ||
		record.Attempt.ProviderActionID != moduleApplyRemoteActionIDV1 ||
		provider.ArtifactDigest != fixture.ArtifactDigest ||
		provider.ExecutionClass != moduleapi.ExecutionRemote ||
		provider.AdapterIdentity != remoteactionhttp.AdapterIdentityV1 {
		t.Fatalf("W2-R1 REMOTE Attempt closure=%+v", record)
	}
	content, err := store.GetContent(
		context.Background(),
		record.Attempt.Binding.ConfigRef,
	)
	if err != nil {
		t.Fatalf("read W2-R1 REMOTE Config: %v", err)
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(content.CanonicalBytes)
	if err != nil {
		t.Fatalf("restore W2-R1 REMOTE Config: %v", err)
	}
	parameters, err := moduleapi.RestoreRemoteActionHTTPBindingParametersV1(
		config.Parameters,
	)
	if err != nil || parameters.EndpointURL != fixture.EndpointURL ||
		parameters.SecretRef != fixture.SecretRef {
		t.Fatalf("W2-R1 REMOTE Binding parameters=%+v error=%v", parameters, err)
	}
}

func w2r1ActionAttemptCountV1(
	t *testing.T,
	databasePath string,
	runID string,
) int {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	queryErr := database.QueryRow(`
		SELECT COUNT(*)
		FROM dispatch_attempts
		WHERE run_id=? AND dispatch_kind='ACTION'
	`, runID).Scan(&count)
	closeErr := database.Close()
	if queryErr != nil || closeErr != nil {
		t.Fatalf("count W2-R1 REMOTE Attempts: %v", errors.Join(queryErr, closeErr))
	}
	return count
}

func assertW2R1PathExcludesBytesV1(
	t *testing.T,
	root string,
	forbidden []byte,
) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(body, forbidden) {
			return fmt.Errorf("forbidden runtime Secret bytes found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertW2R1RemoteArtifactFilesV1(
	t *testing.T,
	fixture w2r1RemoteActionFixtureV1,
	restoredArtifactRoot string,
) {
	t.Helper()
	for _, item := range []struct {
		relative string
		want     []byte
	}{
		{moduleapi.ArtifactManifestPath, fixture.ManifestCanonical},
		{"content/actions.json", fixture.DescriptorCanonical},
	} {
		got, err := os.ReadFile(filepath.Join(
			restoredArtifactRoot,
			fixture.ArtifactDigest,
			filepath.FromSlash(item.relative),
		))
		if err != nil || !bytes.Equal(got, item.want) {
			t.Fatalf("restored REMOTE artifact %s differs: %v", item.relative, err)
		}
	}
}
