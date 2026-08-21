package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// TestW2R3UntrustedWASMOfflineDisableRestoreAndPureChatV1 closes the narrow
// R3 lifecycle around a non-builtin, zero-capability WASM Action. Disable is a
// stopped-process Catalog transition: an active composition owns the Store and
// rejects the operation, while a completed offline Disable preserves immutable
// history and removes only future admission. Neither source nor restored Pure
// Chat needs the historical artifact after the last reference is disabled.
func TestW2R3UntrustedWASMOfflineDisableRestoreAndPureChatV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newW2R2WASMActionFixtureV1(t, filepath.Join(root, "fixture"))
	if strings.HasPrefix(fixture.ModuleID, "freeagent.builtin.") {
		t.Fatalf("R3 fixture must be a third-party Module ID: %q", fixture.ModuleID)
	}
	applyW2R2WASMActionV1(t, root, databasePath, artifactRoot, fixture)

	composition, observation := openW2R2WASMActionCompositionV1(
		t,
		databasePath,
		artifactRoot,
		fixture,
	)
	provider := w2r2WASMProviderFromStoreV1(t, composition.store, fixture)
	input := moduleApplyChatInputV1(
		"w2r3-wasm-terminal",
		"execute the untrusted pure-compute module exactly once",
	)
	succeeded, err := composition.chat.Chat(ctx, input)
	if err != nil || succeeded.TerminalResult == nil ||
		observation.GuestExecutions() != 1 ||
		observation.SuccessfulReturns() != 1 {
		_ = composition.Close()
		t.Fatalf(
			"R3 WASM terminal result=%+v guest=%d/%d error=%v",
			succeeded,
			observation.GuestExecutions(),
			observation.SuccessfulReturns(),
			err,
		)
	}

	disablePlanPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "disable-wasm.json"),
		newW2R3WASMDisablePlanV1(t, fixture, 2),
	)
	beforeBasis, beforeControl, beforeCatalog, err :=
		composition.store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("load R3 pre-disable state: %v", err)
	}
	if _, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePlanPath,
		"",
		"",
	); err == nil || !strings.Contains(err.Error(), "STORE_BUSY") {
		_ = composition.Close()
		t.Fatalf("active-process WASM Disable error=%v, want STORE_BUSY", err)
	}
	afterBusyBasis, afterBusyControl, afterBusyCatalog, err :=
		composition.store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil || afterBusyBasis != beforeBasis ||
		!reflect.DeepEqual(afterBusyControl, beforeControl) ||
		!reflect.DeepEqual(afterBusyCatalog, beforeCatalog) {
		_ = composition.Close()
		t.Fatalf("STORE_BUSY changed current publication: basis=%+v error=%v", afterBusyBasis, err)
	}
	if observation.GuestExecutions() != 1 {
		_ = composition.Close()
		t.Fatalf("STORE_BUSY changed guest executions=%d", observation.GuestExecutions())
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close R3 serving composition: %v", err)
	}

	disabled, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePlanPath,
		"",
		"",
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied ||
		disabled.PointerRevision != 3 {
		t.Fatalf("offline WASM Disable=%+v error=%v", disabled, err)
	}
	disabledRetry, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		disablePlanPath,
		"",
		"",
	)
	if err != nil || disabledRetry.Status != moduleApplyStatusAlreadyApplied ||
		disabledRetry.PointerRevision != 3 {
		t.Fatalf("offline WASM Disable retry=%+v error=%v", disabledRetry, err)
	}
	assertW2R3WASMDisabledHistoryV1(t, databasePath, fixture, provider)

	bundlePath := filepath.Join(root, "w2r3-disabled-wasm.bundle")
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
	assertW2R3WASMDisabledHistoryV1(t, restoredDatabase, fixture, provider)

	for _, directory := range []string{
		filepath.Join(artifactRoot, fixture.ArtifactDigest),
		filepath.Join(restoredArtifacts, fixture.ArtifactDigest),
	} {
		if err := os.RemoveAll(directory); err != nil {
			t.Fatalf("remove disabled WASM artifact %q: %v", directory, err)
		}
	}
	assertW2R3DisabledWASMPureChatV1(
		t,
		databasePath,
		artifactRoot,
		provider,
		input,
		succeeded.RunID,
		succeeded.Reply,
		"w2r3-source-pure-chat",
	)
	assertW2R3DisabledWASMPureChatV1(
		t,
		restoredDatabase,
		restoredArtifacts,
		provider,
		input,
		succeeded.RunID,
		succeeded.Reply,
		"w2r3-restored-pure-chat",
	)
}

func newW2R3WASMDisablePlanV1(
	t *testing.T,
	fixture moduleApplyWASMFixtureV1,
	expectedPointer uint64,
) []byte {
	t.Helper()
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyDisabledV1),
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
	})
}

func assertW2R3WASMDisabledHistoryV1(
	t *testing.T,
	databasePath string,
	fixture moduleApplyWASMFixtureV1,
	provider moduleapi.ActivatedModuleRef,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open disabled WASM Store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("close disabled WASM Store: %v", err)
		}
	}()
	basis, _, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil || basis.PointerRevision != 3 {
		t.Fatalf("load disabled WASM publication=%+v error=%v", basis, err)
	}
	if _, found := catalog.FindInstance(fixture.InstanceID); found {
		t.Fatal("disabled WASM Instance remains in current Catalog")
	}
	installation, installErr := store.GetModuleInstallationByIdentity(
		ctx,
		fixture.ModuleID,
		fixture.Version,
	)
	activation, activationErr := store.GetModuleActivationByIdentity(
		ctx,
		defaultTenantID,
		fixture.InstanceID,
		provider.ActivationRevision,
	)
	if joined := errors.Join(installErr, activationErr); joined != nil {
		t.Fatalf("read immutable disabled WASM history: %v", joined)
	}
	if installation.ArtifactDigest != fixture.ArtifactDigest ||
		activation.InstallationID != installation.InstallationID ||
		activation.ExecutionClass != moduleapi.ExecutionWASM ||
		activation.AdapterIdentity != provider.AdapterIdentity {
		t.Fatalf(
			"disabled WASM immutable history installation=%+v activation=%+v",
			installation,
			activation,
		)
	}
}

func assertW2R3DisabledWASMPureChatV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	provider moduleapi.ActivatedModuleRef,
	terminalInput localchat.ChatInput,
	terminalRunID string,
	terminalReply string,
	newRequestID string,
) {
	t.Helper()
	ctx := context.Background()
	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open disabled WASM Pure Chat composition: %v", err)
	}
	defer func() {
		if err := composition.Close(); err != nil {
			t.Errorf("close disabled WASM Pure Chat composition: %v", err)
		}
	}()
	registered, err := composition.registry.IsRegistered(
		ctx,
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	)
	if err != nil || registered {
		t.Fatalf("disabled WASM Registry registered=%v error=%v", registered, err)
	}
	if _, err := composition.registry.ResolveExact(
		ctx,
		provider.ArtifactDigest,
		provider.AdapterIdentity,
	); !errors.Is(err, exactadapter.ErrAdapterNotFound) {
		t.Fatalf("disabled WASM exact resolution error=%v", err)
	}

	retry, err := composition.chat.Chat(ctx, terminalInput)
	if err != nil || retry.AdmissionCreated || retry.RunID != terminalRunID ||
		retry.TerminalResult == nil || retry.Reply != terminalReply {
		t.Fatalf("disabled WASM terminal retry=%+v error=%v", retry, err)
	}
	if got := w2r1ActionAttemptCountV1(t, databasePath, terminalRunID); got != 1 {
		t.Fatalf("disabled WASM terminal retry Attempts=%d want 1", got)
	}

	pure, err := composition.chat.Chat(
		ctx,
		moduleApplyChatInputV1(
			newRequestID,
			"continue as Pure Chat after the WASM module was disabled",
		),
	)
	if err != nil || !pure.AdmissionCreated || pure.RunID == "" ||
		pure.RunID == terminalRunID || pure.TerminalResult == nil {
		t.Fatalf("post-disable Pure Chat=%+v error=%v", pure, err)
	}
	if got := w2r1ActionAttemptCountV1(t, databasePath, pure.RunID); got != 0 {
		t.Fatalf("post-disable Pure Chat Action Attempts=%d want 0", got)
	}
}
