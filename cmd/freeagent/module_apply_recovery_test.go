package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/channelservice"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleApplyCLIRejectsActiveCurrentStoreOwnerWithoutMutation(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	before := observeModuleApplyStateV1(t, databasePath, artifactRoot)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "busy-no-change.json"),
		newDisabledModuleApplyPlanFixtureV1(t, before.PointerRevision),
	)

	owner, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatalf("open active Current Store owner: %v", err)
	}
	_, applyErr := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		"",
		"",
	)
	if applyErr == nil || !strings.Contains(applyErr.Error(), "(STORE_BUSY)") {
		_ = owner.Close()
		t.Fatalf("active owner module-apply error=%v", applyErr)
	}
	basis, _, _, basisErr := owner.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if basisErr != nil || basis.PointerRevision != before.PointerRevision ||
		basis.Control.SnapshotID != before.ControlSnapshotID ||
		basis.Catalog.GenerationID != before.CatalogGenerationID {
		_ = owner.Close()
		t.Fatalf("STORE_BUSY changed current pointer: basis=%+v, %v", basis, basisErr)
	}
	if counts := moduleApplyMutationRowCountsV1(t, databasePath); !reflect.DeepEqual(counts, before.MutationRowCounts) {
		_ = owner.Close()
		t.Fatalf("STORE_BUSY changed Store rows: before=%v after=%v", before.MutationRowCounts, counts)
	}
	if closeErr := owner.Close(); closeErr != nil {
		t.Fatalf("close active Current Store owner: %v", closeErr)
	}
	after := observeModuleApplyStateV1(t, databasePath, artifactRoot)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("STORE_BUSY changed durable state: before=%+v after=%+v", before, after)
	}
	assertNoModuleApplyStageResidueV1(t, root)
	assertModuleApplyMCPNotStartedV1(t, eventPath)
}

func TestModuleApplyStartupRecoveryClosesPendingAndPreservesUnknown(t *testing.T) {
	t.Run("Model", testModuleApplyModelStartupRecoveryV1)
	t.Run("Action", testModuleApplyActionStartupRecoveryV1)
	t.Run("Channel", testModuleApplyChannelStartupRecoveryV1)
}

func TestModuleApplyPreCancelledCleansOwnerAndStage(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	before := observeModuleApplyStateV1(t, databasePath, artifactRoot)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "cancelled-enable.json"),
		newEnabledModuleApplyPlanFixtureV1(
			t,
			fixture,
			before.PointerRevision,
			0,
		),
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runModuleApply(
		ctx,
		[]string{
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--plan", planPath,
			"--artifact", fixture.ArtifactDirectory,
			"--allow-local-mcp-artifact", fixture.ArtifactDigest,
		},
		&stdout,
		&stderr,
	)
	if err == nil || !strings.Contains(err.Error(), "(CANCELLED)") ||
		stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf(
			"pre-cancelled module-apply error=%v stdout=%q stderr=%q",
			err,
			stdout.String(),
			stderr.String(),
		)
	}
	after := observeModuleApplyStateV1(t, databasePath, artifactRoot)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("pre-cancelled apply changed state: before=%+v after=%+v", before, after)
	}
	if _, err := os.Lstat(filepath.Join(artifactRoot, fixture.ArtifactDigest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-cancelled apply published artifact: %v", err)
	}
	assertNoModuleApplyStageResidueV1(t, root)
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	owner, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatalf("pre-cancelled apply leaked Current Store owner: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("close post-cancellation owner probe: %v", err)
	}
}

func testModuleApplyModelStartupRecoveryV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	preparing, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open Model recovery preparation composition: %v", err)
	}
	runs := admitStartupRecoveryRuns(t, preparing.store)
	pendingID := beginStartupRecoveryPending(t, preparing.store, runs["pending"])
	unknownID := beginStartupRecoveryPending(t, preparing.store, runs["unknown"])
	unknownBefore := commitStartupRecoveryUnknown(
		t,
		preparing.store,
		runs["unknown"],
		unknownID,
	)
	pendingBefore, err := preparing.store.GetModelDispatchRecord(ctx, pendingID)
	if err != nil {
		_ = preparing.Close()
		t.Fatalf("read pre-apply Model PENDING: %v", err)
	}
	if err := preparing.Close(); err != nil {
		t.Fatalf("close Model recovery preparation composition: %v", err)
	}

	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-after-model-recovery.json"),
		newEnabledModuleApplyPlanFixtureV1(t, fixture, 1, 0),
	)
	result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || result.Status != moduleApplyStatusApplied ||
		result.PointerRevision != 2 {
		t.Fatalf("module-apply after Model PENDING=%+v, %v", result, err)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Model recovered Store: %v", err)
	}
	pendingAfter, pendingErr := store.GetModelDispatchRecord(ctx, pendingID)
	unknownAfter, unknownErr := store.GetModelDispatchRecord(ctx, unknownID)
	closeErr := store.Close()
	if pendingErr != nil || unknownErr != nil || closeErr != nil {
		t.Fatalf("read Model recovery results: %v", errors.Join(pendingErr, unknownErr, closeErr))
	}
	assertRecoveredModelAttemptV1(t, pendingBefore, pendingAfter)
	if !reflect.DeepEqual(unknownAfter, unknownBefore) {
		t.Fatalf("module-apply changed existing Model UNKNOWN:\nbefore=%+v\nafter=%+v", unknownBefore, unknownAfter)
	}
}

func testModuleApplyActionStartupRecoveryV1(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	eventPath := filepath.Join(root, "mcp-events.log")
	fixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     actionExampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize Action recovery fixture: %v", err)
	}
	firstPending := createModuleApplyActionPendingV1(
		t,
		databasePath,
		artifactRoot,
		"module-apply-action-existing-unknown",
	)
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Action Store for existing UNKNOWN: %v", err)
	}
	if err := runProductionStartupRecovery(ctx, store); err != nil {
		_ = store.Close()
		t.Fatalf("prepare existing Action UNKNOWN: %v", err)
	}
	unknownBefore, unknownErr := store.GetActionDispatchRecord(
		ctx,
		firstPending.Attempt.AttemptID,
	)
	closeErr := store.Close()
	if unknownErr != nil || closeErr != nil ||
		unknownBefore.Attempt.State != currentstore.ActionDispatchUnknown {
		t.Fatalf("existing Action UNKNOWN=%+v, %v", unknownBefore, errors.Join(unknownErr, closeErr))
	}
	secondPending := createModuleApplyActionPendingV1(
		t,
		databasePath,
		artifactRoot,
		"module-apply-action-pending",
	)

	plan := moduleApplyRecoveryEnabledPlanV1(
		t,
		fixture,
		1,
		"action-chat",
		1,
		"mcp.text.stats",
	)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-after-action-recovery.json"),
		plan,
	)
	result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
	)
	if err != nil || result.Status != moduleApplyStatusApplied ||
		result.BindingTarget.ProfileID != "action-chat" || result.PointerRevision != 2 {
		t.Fatalf("module-apply after Action PENDING=%+v, %v", result, err)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	store, err = currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Action recovered Store: %v", err)
	}
	pendingAfter, pendingErr := store.GetActionDispatchRecord(
		ctx,
		secondPending.Attempt.AttemptID,
	)
	unknownAfter, unknownErr := store.GetActionDispatchRecord(
		ctx,
		unknownBefore.Attempt.AttemptID,
	)
	closeErr = store.Close()
	if pendingErr != nil || unknownErr != nil || closeErr != nil {
		t.Fatalf("read Action recovery results: %v", errors.Join(pendingErr, unknownErr, closeErr))
	}
	assertRecoveredActionAttemptV1(t, secondPending, pendingAfter)
	if !reflect.DeepEqual(unknownAfter, unknownBefore) {
		t.Fatalf("module-apply changed existing Action UNKNOWN:\nbefore=%+v\nafter=%+v", unknownBefore, unknownAfter)
	}
}

func testModuleApplyChannelStartupRecoveryV1(t *testing.T) {
	ctx := context.Background()
	outbound := newModuleApplyRecoveryChannelServerV1(t)
	defer outbound.Close()
	channelFixture := newProductionChannelFixture(t, productionChannelFixtureOptions{
		enabled:     true,
		outboundURL: outbound.URL + "/channel/outbound",
	})
	root := t.TempDir()
	eventPath := filepath.Join(root, "mcp-events.log")
	mcpFixture := newModuleApplyMCPFixtureV1(
		t,
		filepath.Join(root, "fixture"),
		eventPath,
	)
	composition, err := openProductionChannelComposition(
		ctx,
		channelFixture.databasePath,
		channelFixture.artifactRoot,
		productionChannelEndpointInput{
			TenantID:       defaultTenantID,
			WorkspaceID:    defaultWorkspaceID,
			EndpointID:     "endpoint-loopback",
			SecretResolver: channelFixture.resolver,
		},
	)
	if err != nil {
		t.Fatalf("open Channel recovery preparation composition: %v", err)
	}
	firstPending := createModuleApplyChannelPendingV1(
		t,
		composition,
		channelFixture.databasePath,
		"module-apply-channel-existing-unknown",
		0,
		1,
	)
	if err := runProductionStartupRecovery(ctx, composition.store); err != nil {
		_ = composition.Close()
		t.Fatalf("prepare existing Channel UNKNOWN: %v", err)
	}
	unknownBefore, err := composition.store.GetChannelDispatchRecord(
		ctx,
		firstPending.Attempt.AttemptID,
	)
	if err != nil || unknownBefore.Attempt.State != currentstore.DispatchUnknown {
		_ = composition.Close()
		t.Fatalf("existing Channel UNKNOWN=%+v, %v", unknownBefore, err)
	}
	secondPending := createModuleApplyChannelPendingV1(
		t,
		composition,
		channelFixture.databasePath,
		"module-apply-channel-pending",
		1,
		2,
	)
	if err := composition.Close(); err != nil {
		t.Fatalf("close Channel recovery preparation composition: %v", err)
	}
	basis := readModuleApplyRecoveryBasisV1(t, channelFixture.databasePath)
	plan := moduleApplyRecoveryEnabledPlanV1(
		t,
		mcpFixture,
		basis.PointerRevision,
		channelFixture.defaults.ProfileID,
		0,
		"text.stats",
	)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-after-channel-recovery.json"),
		plan,
	)
	result, err := runModuleApplyFixtureV1(
		channelFixture.databasePath,
		channelFixture.artifactRoot,
		planPath,
		mcpFixture.ArtifactDirectory,
		mcpFixture.ArtifactDigest,
	)
	if err != nil || result.Status != moduleApplyStatusApplied ||
		result.PointerRevision != basis.PointerRevision+1 {
		t.Fatalf("module-apply after Channel PENDING=%+v, %v", result, err)
	}
	assertModuleApplyMCPNotStartedV1(t, eventPath)

	store, err := currentstore.OpenExistingCurrentStore(
		ctx,
		channelFixture.databasePath,
	)
	if err != nil {
		t.Fatalf("open Channel recovered Store: %v", err)
	}
	pendingAfter, pendingErr := store.GetChannelDispatchRecord(
		ctx,
		secondPending.Attempt.AttemptID,
	)
	unknownAfter, unknownErr := store.GetChannelDispatchRecord(
		ctx,
		unknownBefore.Attempt.AttemptID,
	)
	closeErr := store.Close()
	if pendingErr != nil || unknownErr != nil || closeErr != nil {
		t.Fatalf("read Channel recovery results: %v", errors.Join(pendingErr, unknownErr, closeErr))
	}
	assertRecoveredChannelAttemptV1(t, secondPending, pendingAfter)
	if !reflect.DeepEqual(unknownAfter, unknownBefore) {
		t.Fatalf("module-apply changed existing Channel UNKNOWN:\nbefore=%+v\nafter=%+v", unknownBefore, unknownAfter)
	}
}

func createModuleApplyActionPendingV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	requestID string,
) currentstore.ActionDispatchRecord {
	t.Helper()
	ctx := context.Background()
	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open Action PENDING composition: %v", err)
	}
	database := openModuleApplyRecoveryFaultDBV1(t, databasePath)
	installModuleApplyRecoveryDispatchFaultV1(t, database)
	failed, chatErr := composition.chat.Chat(ctx, localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-" + requestID,
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "action-chat",
		Message:     "leave one Action PENDING for " + requestID,
		RequestID:   requestID,
		Deadline:    moduleApplyRecoveryDeadlineV1(),
	})
	removeModuleApplyRecoveryDispatchFaultV1(t, database)
	if closeErr := database.Close(); closeErr != nil {
		_ = composition.Close()
		t.Fatalf("close Action fault DB: %v", closeErr)
	}
	if chatErr == nil || failed.RunID == "" {
		_ = composition.Close()
		t.Fatalf("Action terminal fault result=%+v, %v", failed, chatErr)
	}
	records, readErr := composition.store.ScanUnsettledActionDispatchRecords(
		ctx,
		failed.RunID,
	)
	closeErr := composition.Close()
	if readErr != nil || closeErr != nil || len(records) != 1 ||
		records[0].Attempt.State != currentstore.ActionDispatchPending {
		t.Fatalf("Action PENDING records=%+v, %v", records, errors.Join(readErr, closeErr))
	}
	return records[0]
}

func createModuleApplyChannelPendingV1(
	t *testing.T,
	composition *productionComposition,
	databasePath string,
	providerEventID string,
	cursorBefore int,
	cursorAfter int,
) currentstore.ChannelDispatchRecord {
	t.Helper()
	database := openModuleApplyRecoveryFaultDBV1(t, databasePath)
	installModuleApplyRecoveryDispatchFaultV1(t, database)
	result, runErr := composition.channel.service.AdmitAndRun(
		context.Background(),
		channelservice.IngressInput{
			TenantID:    defaultTenantID,
			WorkspaceID: defaultWorkspaceID,
			Deadline:    moduleApplyRecoveryDeadlineV1(),
			Envelope: moduleapi.ChannelInboundEnvelopeV1{
				SchemaVersion:   moduleapi.ChannelInboundEnvelopeSchemaV1,
				EndpointID:      "endpoint-loopback",
				ProviderEventID: providerEventID,
				ExternalUserID:  "external-user",
				Message:         "leave one Channel PENDING for " + providerEventID,
				ReplyTarget:     json.RawMessage(`{"account_id":"account-loopback","conversation_id":"conversation-loopback"}`),
				CursorBefore:    json.RawMessage([]byte(`{"offset":` + strconv.Itoa(cursorBefore) + `}`)),
				CursorAfter:     json.RawMessage([]byte(`{"offset":` + strconv.Itoa(cursorAfter) + `}`)),
			},
		},
	)
	removeModuleApplyRecoveryDispatchFaultV1(t, database)
	if runErr == nil || result.RunID == "" {
		_ = database.Close()
		t.Fatalf("Channel terminal fault result=%+v, %v", result, runErr)
	}
	var attemptID string
	if err := database.QueryRowContext(
		context.Background(),
		`SELECT attempt_id FROM dispatch_attempts WHERE run_id=? AND dispatch_kind='CHANNEL_SEND'`,
		result.RunID,
	).Scan(&attemptID); err != nil {
		_ = database.Close()
		t.Fatalf("read Channel PENDING identity: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close Channel fault DB: %v", err)
	}
	record, err := composition.store.GetChannelDispatchRecord(
		context.Background(),
		attemptID,
	)
	if err != nil || record.Attempt.State != currentstore.DispatchPending {
		t.Fatalf("Channel PENDING record=%+v, %v", record, err)
	}
	return record
}

func openModuleApplyRecoveryFaultDBV1(
	t *testing.T,
	databasePath string,
) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
	if err != nil {
		t.Fatalf("open module-apply recovery fault DB: %v", err)
	}
	return database
}

func installModuleApplyRecoveryDispatchFaultV1(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`
		CREATE TRIGGER module_apply_recovery_terminal_fault
		BEFORE UPDATE OF state ON dispatch_attempts
		WHEN OLD.state='PENDING' AND NEW.state<>'PENDING'
		BEGIN
			SELECT RAISE(ABORT, 'module-apply recovery terminal fault');
		END
	`); err != nil {
		t.Fatalf("install module-apply recovery terminal fault: %v", err)
	}
}

func removeModuleApplyRecoveryDispatchFaultV1(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`DROP TRIGGER module_apply_recovery_terminal_fault`); err != nil {
		t.Fatalf("remove module-apply recovery terminal fault: %v", err)
	}
}

func moduleApplyRecoveryEnabledPlanV1(
	t *testing.T,
	fixture moduleApplyMCPFixtureV1,
	expectedPointer uint64,
	profileID string,
	actionIndex uint32,
	publicActionID string,
) []byte {
	t.Helper()
	canonical := newEnabledModuleApplyPlanFixtureV1(
		t,
		fixture,
		expectedPointer,
		actionIndex,
	)
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		t.Fatalf("decode module-apply recovery plan: %v", err)
	}
	value["binding_target"] = moduleApplyProfileBindingTargetTestValue(profileID)
	binding := value["binding"].(map[string]any)
	config := binding["config"].(map[string]any)
	actions := config["actions"].([]any)
	action := actions[0].(map[string]any)
	action["public_action_id"] = publicActionID
	return canonicalModuleApplyPlanTestJSON(t, value)
}

func readModuleApplyRecoveryBasisV1(
	t *testing.T,
	databasePath string,
) moduleApplyRecoveryBasisV1 {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatalf("open Store for module-apply recovery basis: %v", err)
	}
	basis, _, _, readErr := store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read module-apply recovery basis: %v", errors.Join(readErr, closeErr))
	}
	return moduleApplyRecoveryBasisV1{PointerRevision: basis.PointerRevision}
}

type moduleApplyRecoveryBasisV1 struct {
	PointerRevision uint64
}

func assertRecoveredModelAttemptV1(
	t *testing.T,
	before currentstore.ModelDispatchRecord,
	after currentstore.ModelDispatchRecord,
) {
	t.Helper()
	if after.Attempt.State != corecontract.ModelAttemptUnknown ||
		after.Attempt.UnknownReason != startupRecoveryUnknownReason ||
		after.Attempt.Revision != before.Attempt.Revision+1 ||
		after.Attempt.AttemptID != before.Attempt.AttemptID ||
		after.Attempt.LogicalOperationKey != before.Attempt.LogicalOperationKey ||
		after.Attempt.Provider != before.Attempt.Provider ||
		after.Attempt.Model != before.Attempt.Model ||
		!reflect.DeepEqual(after.Attempt.Binding, before.Attempt.Binding) ||
		!bytes.Equal(after.Attempt.BindingCanonical, before.Attempt.BindingCanonical) ||
		!bytes.Equal(after.Attempt.ModelConfigCanonical, before.Attempt.ModelConfigCanonical) ||
		!reflect.DeepEqual(after.Attempt.Request, before.Attempt.Request) {
		t.Fatalf("Model PENDING was not recovered in place:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func assertRecoveredActionAttemptV1(
	t *testing.T,
	before currentstore.ActionDispatchRecord,
	after currentstore.ActionDispatchRecord,
) {
	t.Helper()
	if after.Attempt.State != currentstore.ActionDispatchUnknown ||
		after.Attempt.UnknownReason != startupRecoveryUnknownReason ||
		after.Attempt.Revision != before.Attempt.Revision+1 ||
		after.Attempt.AttemptID != before.Attempt.AttemptID ||
		after.Attempt.LogicalOperationKey != before.Attempt.LogicalOperationKey ||
		after.Attempt.ProviderActionID != before.Attempt.ProviderActionID ||
		!reflect.DeepEqual(after.Attempt.Binding, before.Attempt.Binding) ||
		!bytes.Equal(after.Attempt.BindingCanonical, before.Attempt.BindingCanonical) ||
		!reflect.DeepEqual(after.Proposal, before.Proposal) {
		t.Fatalf("Action PENDING was not recovered in place:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func assertRecoveredChannelAttemptV1(
	t *testing.T,
	before currentstore.ChannelDispatchRecord,
	after currentstore.ChannelDispatchRecord,
) {
	t.Helper()
	if after.Attempt.State != currentstore.DispatchUnknown ||
		after.Attempt.UnknownReason != startupRecoveryUnknownReason ||
		after.Attempt.Revision != before.Attempt.Revision+1 ||
		after.Attempt.AttemptID != before.Attempt.AttemptID ||
		after.Attempt.LogicalOperationKey != before.Attempt.LogicalOperationKey ||
		after.Attempt.EndpointID != before.Attempt.EndpointID ||
		!reflect.DeepEqual(after.Attempt.Binding, before.Attempt.Binding) ||
		!bytes.Equal(after.Attempt.BindingCanonical, before.Attempt.BindingCanonical) ||
		!reflect.DeepEqual(after.Proposal, before.Proposal) {
		t.Fatalf("Channel PENDING was not recovered in place:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func newModuleApplyRecoveryChannelServerV1(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(
		func(response http.ResponseWriter, request *http.Request) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read module-apply recovery Channel request: %v", err)
				return
			}
			var delivery struct {
				AttemptID string `json:"attempt_id"`
			}
			if err := json.Unmarshal(body, &delivery); err != nil || delivery.AttemptID == "" {
				t.Errorf("decode module-apply recovery Channel delivery=%+v, %v", delivery, err)
				return
			}
			wire, err := json.Marshal(map[string]any{
				"schema_version":        "loopback-channel-delivery-result/v1",
				"attempt_id":            delivery.AttemptID,
				"outcome":               "SUCCEEDED",
				"external_operation_id": "external-" + delivery.AttemptID,
			})
			if err != nil {
				t.Errorf("marshal module-apply recovery Channel result: %v", err)
				return
			}
			canonical, err := moduleapi.CanonicalJSON(wire)
			if err != nil {
				t.Errorf("canonicalize module-apply recovery Channel result: %v", err)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write(canonical)
		},
	))
}

func moduleApplyRecoveryDeadlineV1() time.Time {
	return time.Date(2099, time.January, 2, 3, 4, 5, 0, time.UTC)
}
