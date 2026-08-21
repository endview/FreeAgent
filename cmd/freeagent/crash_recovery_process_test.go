package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	crashHelperEnvironment = "FREEAGENT_S1_CRASH_HELPER"
	crashPhaseBeforeModel  = "before-model-call"
	crashPhaseAfterModel   = "after-model-call"
	crashPhaseAfterAction  = "after-action-call"
)

type crashProcessCase struct {
	phase        string
	databasePath string
	artifactRoot string
	markerPath   string
	requestID    string
	message      string
	deadline     time.Time
	runID        string
	attemptID    string
	command      *exec.Cmd
	done         chan error
	output       bytes.Buffer
}

func TestProductionCompositionRecoversRealProcessKillWithoutModelReplay(
	t *testing.T,
) {
	if os.Getenv(crashHelperEnvironment) == "1" {
		t.Fatal("parent crash test entered helper mode")
	}

	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	cases := []*crashProcessCase{
		newCrashProcessCase(t, crashPhaseBeforeModel, deadline),
		newCrashProcessCase(t, crashPhaseAfterModel, deadline),
	}
	for _, test := range cases {
		startCrashHelperProcess(t, test)
	}
	for _, test := range cases {
		waitForCrashMarker(t, test)
		if err := test.command.Process.Kill(); err != nil {
			t.Fatalf("kill %s helper: %v", test.phase, err)
		}
		if err := <-test.done; err == nil {
			t.Fatalf("%s helper exited cleanly after Process.Kill", test.phase)
		}
	}

	for _, test := range cases {
		composition, err := openProductionComposition(
			context.Background(),
			test.databasePath,
			test.artifactRoot,
			defaultTenantID,
		)
		if composition != nil {
			_ = composition.Close()
			t.Fatalf("%s restart bypassed the unexpired crashed lease", test.phase)
		}
		if !errors.Is(err, currentstore.ErrRunLeaseUnavailable) {
			t.Fatalf("%s immediate restart error=%v", test.phase, err)
		}
		expireCrashedRunLease(t, test.databasePath, test.runID)

		composition, err = openProductionComposition(
			context.Background(),
			test.databasePath,
			test.artifactRoot,
			defaultTenantID,
		)
		if err != nil {
			t.Fatalf("%s restart after lease expiry: %v", test.phase, err)
		}
		records, err := composition.store.ScanUnsettledModelDispatchRecords(
			context.Background(),
			test.runID,
		)
		if err != nil {
			_ = composition.Close()
			t.Fatalf("%s scan recovered Attempt: %v", test.phase, err)
		}
		if len(records) != 1 ||
			records[0].Attempt.AttemptID != test.attemptID ||
			records[0].Attempt.State != corecontract.ModelAttemptUnknown {
			_ = composition.Close()
			t.Fatalf("%s recovered records=%+v", test.phase, records)
		}

		retry, err := composition.chat.Chat(
			context.Background(),
			crashChatInput(test),
		)
		if err != nil {
			_ = composition.Close()
			t.Fatalf("%s retry original request: %v", test.phase, err)
		}
		if retry.RunID != test.runID ||
			retry.LoopResult.Disposition !=
				loopapi.DispositionWaitingReconciliation ||
			retry.Reply != "" {
			_ = composition.Close()
			t.Fatalf("%s retry result=%+v", test.phase, retry)
		}
		if err := composition.Close(); err != nil {
			t.Fatalf("%s close recovered composition: %v", test.phase, err)
		}
		assertOnlyOriginalCrashAttempt(t, test)
	}
}

func TestProductionStartupRecoversKilledActionWithoutExecutorReplay(
	t *testing.T,
) {
	if os.Getenv(crashHelperEnvironment) == "1" {
		t.Fatal("parent Action crash test entered helper mode")
	}

	root := t.TempDir()
	test := &actionCrashProcessCase{
		databasePath: filepath.Join(root, "current.sqlite"),
		artifactRoot: filepath.Join(root, "artifacts"),
		markerPath:   filepath.Join(root, "action-crash.marker"),
		effectPath:   filepath.Join(root, "action-effects.log"),
		requestID:    "crash-after-action-call",
		message:      "count this Action exactly once",
		deadline: time.Now().UTC().Add(time.Hour).
			Truncate(time.Microsecond),
	}
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: test.databasePath,
		SeedPath:     actionExampleSeedPath(t),
		ArtifactRoot: test.artifactRoot,
	}); err != nil {
		t.Fatalf("initialize Action crash fixture: %v", err)
	}
	startActionCrashHelperProcess(t, test)
	waitForActionCrashMarker(t, test)
	if err := test.command.Process.Kill(); err != nil {
		t.Fatalf("kill Action helper: %v", err)
	}
	if err := <-test.done; err == nil {
		t.Fatal("Action helper exited cleanly after Process.Kill")
	}

	test.runID, test.logicalOperationKey = loadPendingActionCrashIdentity(
		t,
		test.databasePath,
		test.attemptID,
	)
	assertActionCrashEffects(t, test.effectPath, test.attemptID)

	// The killed process still owns an unexpired Run lease. Production startup
	// must fail before it can mutate the PENDING Attempt or invoke any adapter.
	opened, err := openProductionComposition(
		context.Background(),
		test.databasePath,
		test.artifactRoot,
		defaultTenantID,
	)
	if opened != nil {
		_ = opened.Close()
		t.Fatal("Action restart bypassed the unexpired crashed lease")
	}
	if !errors.Is(err, currentstore.ErrRunLeaseUnavailable) {
		t.Fatalf("Action immediate restart error=%v", err)
	}
	assertActionCrashEffects(t, test.effectPath, test.attemptID)
	expireCrashedRunLease(t, test.databasePath, test.runID)

	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		test.databasePath,
	)
	if err != nil {
		t.Fatalf("open Action recovery Store: %v", err)
	}
	defer store.Close()
	if err := runProductionStartupRecovery(
		context.Background(),
		store,
	); err != nil {
		t.Fatalf("recover killed Action: %v", err)
	}
	_, _, catalog, err := store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("load post-recovery Action Catalog: %v", err)
	}
	registry, err := newCrashActionRegistry(
		catalog.Entries,
		test.effectPath,
		"",
		false,
	)
	if err != nil {
		t.Fatalf("build post-recovery Action registry: %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatalf("build post-recovery Action Loop: %v", err)
	}

	recovered, err := store.GetActionDispatchRecord(
		context.Background(),
		test.attemptID,
	)
	if err != nil {
		t.Fatalf("load recovered Action Attempt: %v", err)
	}
	if recovered.Attempt.AttemptID != test.attemptID ||
		recovered.Attempt.LogicalOperationKey != test.logicalOperationKey ||
		recovered.Attempt.RunID != test.runID ||
		recovered.Attempt.State != currentstore.ActionDispatchUnknown ||
		recovered.Attempt.UnknownReason != "RECOVERED_PENDING_AFTER_CRASH" {
		t.Fatalf("recovered Action identity/state=%+v", recovered.Attempt)
	}
	scan, err := store.ScanStartupRecovery(context.Background())
	if err != nil {
		t.Fatalf("scan recovered Action Run: %v", err)
	}
	if len(scan) != 1 || scan[0].RunID != test.runID ||
		scan[0].FrameStep != corecontract.WaitingReconciliationLoopStep ||
		scan[0].UnsettledAttemptID != "" ||
		scan[0].UnsettledActionAttemptID != test.attemptID ||
		scan[0].UnsettledActionAttemptState !=
			currentstore.ActionDispatchUnknown {
		t.Fatalf("recovered Action startup projection=%+v", scan)
	}
	reentered, err := loop.Run(context.Background(), loopapi.RunInput{
		RunID:       test.runID,
		MaxSteps:    3,
		MaxDuration: time.Minute,
	})
	if err != nil || reentered.Disposition !=
		loopapi.DispositionWaitingReconciliation {
		t.Fatalf("re-enter recovered Action=%+v error=%v", reentered, err)
	}
	assertActionCrashEffects(t, test.effectPath, test.attemptID)
}

func TestProductionStartupRecoversActionTerminalCommitFailureWithoutReplay(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	effectPath := filepath.Join(root, "action-effects.log")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     actionExampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize Action commit-failure fixture: %v", err)
	}
	composition, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open Action commit-failure fixture: %v", err)
	}
	_, _, catalog, err := composition.store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("load Action commit-failure Catalog: %v", err)
	}
	registry, err := newCrashActionRegistry(
		catalog.Entries,
		effectPath,
		"",
		false,
	)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("build Action commit-failure registry: %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(composition.store, registry)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("build Action commit-failure Loop: %v", err)
	}
	chat, err := newProductionChatService(composition.store, loop, registry)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("build Action commit-failure Chat service: %v", err)
	}

	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
	if err != nil {
		_ = composition.Close()
		t.Fatalf("open Action fault injector: %v", err)
	}
	if _, err := database.Exec(`
		CREATE TRIGGER inject_action_terminal_update_failure
		BEFORE UPDATE OF state ON dispatch_attempts
		WHEN OLD.state='PENDING' AND NEW.state<>'PENDING'
		BEGIN
			SELECT RAISE(ABORT, 'injected Action terminal update failure');
		END
	`); err != nil {
		_ = database.Close()
		_ = composition.Close()
		t.Fatalf("install Action terminal fault: %v", err)
	}
	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-action-terminal-failure",
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "action-chat",
		Message:     "execute once before the terminal transaction fails",
		RequestID:   "action-terminal-transaction-failure-1",
		Deadline: time.Now().UTC().Add(time.Hour).
			Truncate(time.Microsecond),
	}
	failed, chatErr := chat.Chat(context.Background(), input)
	if chatErr == nil || failed.RunID == "" {
		_ = database.Close()
		_ = composition.Close()
		t.Fatalf(
			"Action terminal fault result=%+v error=%v",
			failed,
			chatErr,
		)
	}
	var attemptID string
	var operationKey string
	var state string
	if err := database.QueryRow(`
		SELECT attempt_id, logical_operation_key, state
		FROM dispatch_attempts
		WHERE run_id=?
	`, failed.RunID).Scan(&attemptID, &operationKey, &state); err != nil {
		_ = database.Close()
		_ = composition.Close()
		t.Fatalf("load rolled-back Action Attempt: %v", err)
	}
	if state != string(currentstore.ActionDispatchPending) {
		_ = database.Close()
		_ = composition.Close()
		t.Fatalf("Action terminal fault left state=%q, want PENDING", state)
	}
	var frameStep string
	var pendingDispatch sql.NullString
	var leaseOwner sql.NullString
	if err := database.QueryRow(`
		SELECT step, pending_dispatch_attempt_id, lease_owner
		FROM loop_frames
		WHERE run_id=?
	`, failed.RunID).Scan(
		&frameStep,
		&pendingDispatch,
		&leaseOwner,
	); err != nil {
		_ = database.Close()
		_ = composition.Close()
		t.Fatalf("load rolled-back Action Frame: %v", err)
	}
	if frameStep != corecontract.ActionPendingLoopStep ||
		!pendingDispatch.Valid || pendingDispatch.String != attemptID ||
		leaseOwner.Valid {
		_ = database.Close()
		_ = composition.Close()
		t.Fatalf(
			"Action terminal fault Frame step=%q pending=%+v lease=%+v",
			frameStep,
			pendingDispatch,
			leaseOwner,
		)
	}
	assertActionCrashEffects(t, effectPath, attemptID)
	if _, err := database.Exec(
		`DROP TRIGGER inject_action_terminal_update_failure`,
	); err != nil {
		_ = database.Close()
		_ = composition.Close()
		t.Fatalf("remove Action terminal fault: %v", err)
	}
	if err := database.Close(); err != nil {
		_ = composition.Close()
		t.Fatalf("close Action fault injector: %v", err)
	}
	if err := composition.Close(); err != nil {
		t.Fatalf("close Action commit-failure fixture: %v", err)
	}

	if _, err := currentstore.VerifyCurrentStoreReadOnly(
		context.Background(),
		databasePath,
	); err != nil {
		t.Fatalf("verify Action commit-failure Store: %v", err)
	}
	recoveredStore, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		databasePath,
	)
	if err != nil {
		t.Fatalf("open Action commit-failure recovery Store: %v", err)
	}
	defer recoveredStore.Close()
	if err := runProductionStartupRecovery(
		context.Background(),
		recoveredStore,
	); err != nil {
		t.Fatalf("recover Action terminal commit failure: %v", err)
	}
	_, _, recoveredCatalog, err := recoveredStore.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("load post-recovery Action Catalog: %v", err)
	}
	recoveredRegistry, err := newCrashActionRegistry(
		recoveredCatalog.Entries,
		effectPath,
		"",
		false,
	)
	if err != nil {
		t.Fatalf("build post-recovery Action registry: %v", err)
	}
	recoveredLoop, err := coreloop.NewUniversalLoop(
		recoveredStore,
		recoveredRegistry,
	)
	if err != nil {
		t.Fatalf("build post-recovery Action Loop: %v", err)
	}
	recovered, err := recoveredStore.GetActionDispatchRecord(
		context.Background(),
		attemptID,
	)
	if err != nil {
		t.Fatalf("load recovered Action terminal failure: %v", err)
	}
	if recovered.Attempt.AttemptID != attemptID ||
		recovered.Attempt.LogicalOperationKey != operationKey ||
		recovered.Attempt.RunID != failed.RunID ||
		recovered.Attempt.State != currentstore.ActionDispatchUnknown ||
		recovered.Attempt.UnknownReason != "RECOVERED_PENDING_AFTER_CRASH" {
		t.Fatalf("recovered Action terminal failure=%+v", recovered.Attempt)
	}
	recoveredChat, err := newProductionChatService(
		recoveredStore,
		recoveredLoop,
		recoveredRegistry,
	)
	if err != nil {
		t.Fatalf("build recovered Action Chat service: %v", err)
	}
	retry, err := recoveredChat.Chat(context.Background(), input)
	if err != nil || retry.RunID != failed.RunID || retry.AdmissionCreated ||
		retry.LoopResult.Disposition !=
			loopapi.DispositionWaitingReconciliation || retry.Reply != "" {
		t.Fatalf("retry recovered Action=%+v error=%v", retry, err)
	}
	assertActionCrashEffects(t, effectPath, attemptID)
}

type actionCrashProcessCase struct {
	databasePath        string
	artifactRoot        string
	markerPath          string
	effectPath          string
	requestID           string
	message             string
	deadline            time.Time
	runID               string
	attemptID           string
	logicalOperationKey string
	command             *exec.Cmd
	done                chan error
	output              bytes.Buffer
}

func TestProductionCompositionRecoversTerminalTransactionFailureWithoutReplay(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatal(err)
	}
	composition, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
	if err != nil {
		_ = composition.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TRIGGER inject_terminal_history_failure
		BEFORE INSERT ON history_entries
		BEGIN
			SELECT RAISE(ABORT, 'injected terminal transaction failure');
		END
	`); err != nil {
		_ = database.Close()
		_ = composition.Close()
		t.Fatal(err)
	}
	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: defaultPrincipalID,
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   defaultProfileID,
		Message:     "terminal transaction rollback",
		RequestID:   "terminal-transaction-rollback-1",
		Deadline: time.Now().UTC().
			Add(time.Hour).
			Truncate(time.Microsecond),
	}
	failed, chatErr := composition.chat.Chat(context.Background(), input)
	if chatErr == nil || failed.RunID == "" {
		_ = database.Close()
		_ = composition.Close()
		t.Fatalf("terminal transaction failure result=%+v error=%v", failed, chatErr)
	}
	if err := composition.Close(); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`DROP TRIGGER inject_terminal_history_failure`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := openProductionComposition(
		context.Background(),
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("restart after terminal transaction failure: %v", err)
	}
	records, err := recovered.store.ScanUnsettledModelDispatchRecords(
		context.Background(),
		failed.RunID,
	)
	if err != nil {
		_ = recovered.Close()
		t.Fatal(err)
	}
	if len(records) != 1 ||
		records[0].Attempt.State != corecontract.ModelAttemptUnknown {
		_ = recovered.Close()
		t.Fatalf("terminal transaction recovery records=%+v", records)
	}
	retry, err := recovered.chat.Chat(context.Background(), input)
	if err != nil {
		_ = recovered.Close()
		t.Fatal(err)
	}
	if retry.RunID != failed.RunID ||
		retry.LoopResult.Disposition !=
			loopapi.DispositionWaitingReconciliation {
		_ = recovered.Close()
		t.Fatalf("terminal transaction retry=%+v", retry)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProductionCrashBoundaryHelper(t *testing.T) {
	if os.Getenv(crashHelperEnvironment) != "1" {
		return
	}
	if err := runProductionCrashBoundaryHelper(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash helper returned without being killed")
}

func newCrashProcessCase(
	t *testing.T,
	phase string,
	deadline time.Time,
) *crashProcessCase {
	t.Helper()
	root := t.TempDir()
	test := &crashProcessCase{
		phase:        phase,
		databasePath: filepath.Join(root, "current.sqlite"),
		artifactRoot: filepath.Join(root, "artifacts"),
		markerPath:   filepath.Join(root, "crash.marker"),
		requestID:    "crash-" + phase,
		message:      "crash boundary " + phase,
		deadline:     deadline,
	}
	if _, err := initializeProductionData(context.Background(), initInput{
		DatabasePath: test.databasePath,
		SeedPath:     exampleSeedPath(t),
		ArtifactRoot: test.artifactRoot,
	}); err != nil {
		t.Fatalf("initialize %s fixture: %v", phase, err)
	}
	return test
}

func startCrashHelperProcess(t *testing.T, test *crashProcessCase) {
	t.Helper()
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestProductionCrashBoundaryHelper$",
		"-test.v",
	)
	command.Env = append(os.Environ(),
		crashHelperEnvironment+"=1",
		"FREEAGENT_S1_CRASH_PHASE="+test.phase,
		"FREEAGENT_S1_CRASH_DB="+test.databasePath,
		"FREEAGENT_S1_CRASH_ARTIFACTS="+test.artifactRoot,
		"FREEAGENT_S1_CRASH_MARKER="+test.markerPath,
		"FREEAGENT_S1_CRASH_REQUEST="+test.requestID,
		"FREEAGENT_S1_CRASH_MESSAGE="+test.message,
		"FREEAGENT_S1_CRASH_DEADLINE="+
			test.deadline.Format(time.RFC3339Nano),
	)
	command.Stdout = &test.output
	command.Stderr = &test.output
	if err := command.Start(); err != nil {
		t.Fatalf("start %s helper: %v", test.phase, err)
	}
	test.command = command
	test.done = make(chan error, 1)
	go func() {
		defer close(test.done)
		test.done <- command.Wait()
	}()
	registerCrashHelperCleanup(t, test.phase, command, test.done)
}

func startActionCrashHelperProcess(
	t *testing.T,
	test *actionCrashProcessCase,
) {
	t.Helper()
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestProductionCrashBoundaryHelper$",
		"-test.v",
	)
	command.Env = append(os.Environ(),
		crashHelperEnvironment+"=1",
		"FREEAGENT_S1_CRASH_PHASE="+crashPhaseAfterAction,
		"FREEAGENT_S1_CRASH_DB="+test.databasePath,
		"FREEAGENT_S1_CRASH_ARTIFACTS="+test.artifactRoot,
		"FREEAGENT_S1_CRASH_MARKER="+test.markerPath,
		"FREEAGENT_S2_ACTION_EFFECTS="+test.effectPath,
		"FREEAGENT_S1_CRASH_REQUEST="+test.requestID,
		"FREEAGENT_S1_CRASH_MESSAGE="+test.message,
		"FREEAGENT_S1_CRASH_DEADLINE="+
			test.deadline.Format(time.RFC3339Nano),
	)
	command.Stdout = &test.output
	command.Stderr = &test.output
	if err := command.Start(); err != nil {
		t.Fatalf("start Action crash helper: %v", err)
	}
	test.command = command
	test.done = make(chan error, 1)
	go func() {
		defer close(test.done)
		test.done <- command.Wait()
	}()
	registerCrashHelperCleanup(t, "Action", command, test.done)
}

func registerCrashHelperCleanup(
	t *testing.T,
	label string,
	command *exec.Cmd,
	done <-chan error,
) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		var killErr error
		if command.Process != nil {
			killErr = command.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf(
				"wait for %s crash helper cleanup timed out (kill error: %v)",
				label,
				killErr,
			)
		}
	})
}

func waitForCrashMarker(t *testing.T, test *crashProcessCase) {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var lastMarkerErr error
	for {
		select {
		case err := <-test.done:
			t.Fatalf(
				"%s helper exited before marker: %v\n%s",
				test.phase,
				err,
				test.output.String(),
			)
		case <-ticker.C:
			content, err := os.ReadFile(test.markerPath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				// Windows can briefly reject a read while the atomic rename is
				// settling. The marker deadline remains the source of failure.
				lastMarkerErr = err
				continue
			}
			fields := strings.Fields(string(content))
			if len(fields) != 3 || fields[0] != test.phase {
				t.Fatalf("invalid %s marker %q", test.phase, content)
			}
			test.runID = fields[1]
			test.attemptID = fields[2]
			return
		case <-timer.C:
			_ = test.command.Process.Kill()
			<-test.done
			t.Fatalf(
				"%s helper marker timed out (last read error: %v)\n%s",
				test.phase,
				lastMarkerErr,
				test.output.String(),
			)
		}
	}
}

func waitForActionCrashMarker(
	t *testing.T,
	test *actionCrashProcessCase,
) {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var lastMarkerErr error
	for {
		select {
		case err := <-test.done:
			t.Fatalf(
				"Action helper exited before marker: %v\n%s",
				err,
				test.output.String(),
			)
		case <-ticker.C:
			content, err := os.ReadFile(test.markerPath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				lastMarkerErr = err
				continue
			}
			fields := strings.Fields(string(content))
			if len(fields) != 2 || fields[0] != crashPhaseAfterAction {
				t.Fatalf("invalid Action marker %q", content)
			}
			test.attemptID = fields[1]
			return
		case <-timer.C:
			_ = test.command.Process.Kill()
			<-test.done
			t.Fatalf(
				"Action helper marker timed out (last read error: %v)\n%s",
				lastMarkerErr,
				test.output.String(),
			)
		}
	}
}

func runProductionCrashBoundaryHelper() (returnErr error) {
	phase := os.Getenv("FREEAGENT_S1_CRASH_PHASE")
	if phase == crashPhaseAfterAction {
		return runProductionActionCrashBoundaryHelper()
	}
	if phase != crashPhaseBeforeModel && phase != crashPhaseAfterModel {
		return fmt.Errorf("unsupported crash phase %q", phase)
	}
	deadline, err := time.Parse(
		time.RFC3339Nano,
		os.Getenv("FREEAGENT_S1_CRASH_DEADLINE"),
	)
	if err != nil {
		return fmt.Errorf("parse crash deadline: %w", err)
	}
	composition, err := openProductionComposition(
		context.Background(),
		os.Getenv("FREEAGENT_S1_CRASH_DB"),
		os.Getenv("FREEAGENT_S1_CRASH_ARTIFACTS"),
		defaultTenantID,
	)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, composition.Close()) }()
	_, _, catalog, err := composition.store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if err != nil {
		return err
	}
	provider, err := exactLocalModelProvider(catalog.Entries)
	if err != nil {
		return err
	}
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		return err
	}
	invoker := &crashBoundaryInvoker{
		phase:      phase,
		markerPath: os.Getenv("FREEAGENT_S1_CRASH_MARKER"),
		delegate:   echo,
	}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         invoker,
	})
	if err != nil {
		return err
	}
	loop, err := coreloop.NewUniversalLoop(composition.store, registry)
	if err != nil {
		return err
	}
	chat, err := localchat.NewChatService(composition.store, loop)
	if err != nil {
		return err
	}
	composition.chat = chat
	_, err = composition.chat.Chat(
		context.Background(),
		localchat.ChatInput{
			TenantID:    defaultTenantID,
			PrincipalID: defaultPrincipalID,
			WorkspaceID: defaultWorkspaceID,
			AgentID:     defaultAgentID,
			ProfileID:   defaultProfileID,
			Message:     os.Getenv("FREEAGENT_S1_CRASH_MESSAGE"),
			RequestID:   os.Getenv("FREEAGENT_S1_CRASH_REQUEST"),
			Deadline:    deadline.UTC(),
		},
	)
	return err
}

func runProductionActionCrashBoundaryHelper() (returnErr error) {
	deadline, err := time.Parse(
		time.RFC3339Nano,
		os.Getenv("FREEAGENT_S1_CRASH_DEADLINE"),
	)
	if err != nil {
		return fmt.Errorf("parse Action crash deadline: %w", err)
	}
	composition, err := openProductionComposition(
		context.Background(),
		os.Getenv("FREEAGENT_S1_CRASH_DB"),
		os.Getenv("FREEAGENT_S1_CRASH_ARTIFACTS"),
		defaultTenantID,
	)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, composition.Close()) }()
	_, _, catalog, err := composition.store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if err != nil {
		return err
	}
	registry, err := newCrashActionRegistry(
		catalog.Entries,
		os.Getenv("FREEAGENT_S2_ACTION_EFFECTS"),
		os.Getenv("FREEAGENT_S1_CRASH_MARKER"),
		true,
	)
	if err != nil {
		return err
	}
	loop, err := coreloop.NewUniversalLoop(composition.store, registry)
	if err != nil {
		return err
	}
	chat, err := newProductionChatService(composition.store, loop, registry)
	if err != nil {
		return err
	}
	_, err = chat.Chat(context.Background(), localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-action-crash",
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "action-chat",
		Message:     os.Getenv("FREEAGENT_S1_CRASH_MESSAGE"),
		RequestID:   os.Getenv("FREEAGENT_S1_CRASH_REQUEST"),
		Deadline:    deadline.UTC(),
	})
	return err
}

func newCrashActionRegistry(
	entries []controlcontract.CatalogEntry,
	effectPath string,
	markerPath string,
	blockAfterExecution bool,
) (*exactadapter.Registry, error) {
	modelProvider, err := exactLocalModelProvider(entries)
	if err != nil {
		return nil, err
	}
	actionProvider, err := exactCrashActionProvider(entries)
	if err != nil {
		return nil, err
	}
	echo, err := exactadapter.NewDeterministicEcho(modelProvider)
	if err != nil {
		return nil, err
	}
	action, err := exactadapter.NewTextStatsAction(actionProvider)
	if err != nil {
		return nil, err
	}
	counted := &crashBoundaryActionInvoker{
		delegate:            action,
		effectPath:          effectPath,
		markerPath:          markerPath,
		blockAfterExecution: blockAfterExecution,
	}
	return exactadapter.NewRegistry(
		exactadapter.Registration{
			ArtifactDigest:  modelProvider.ArtifactDigest,
			AdapterIdentity: modelProvider.AdapterIdentity,
			Invoker:         echo,
		},
		exactadapter.Registration{
			ArtifactDigest:  actionProvider.ArtifactDigest,
			AdapterIdentity: actionProvider.AdapterIdentity,
			Invoker:         counted,
		},
	)
}

func exactCrashActionProvider(
	entries []controlcontract.CatalogEntry,
) (moduleapi.ActivatedModuleRef, error) {
	var provider moduleapi.ActivatedModuleRef
	count := 0
	for _, entry := range entries {
		for _, port := range entry.Provides {
			if port != productionActionPort {
				continue
			}
			candidate := entry.Activation
			if candidate.ModuleID != localTextStatsModuleID ||
				candidate.Version != localTextStatsVersion ||
				candidate.ArtifactDigest != localTextStatsDigest ||
				candidate.ExecutionClass !=
					moduleapi.ExecutionTrustedInProcess ||
				candidate.AdapterIdentity != localTextStatsAdapterID {
				return moduleapi.ActivatedModuleRef{}, errors.New(
					"Action crash fixture provider is outside the local trust set",
				)
			}
			if count == 0 {
				provider = candidate
			}
			count++
			break
		}
	}
	if count != 1 {
		return moduleapi.ActivatedModuleRef{}, fmt.Errorf(
			"Action crash fixture found %d action.provider/v1 providers",
			count,
		)
	}
	return provider, nil
}

type crashBoundaryActionInvoker struct {
	delegate            *exactadapter.TextStatsAction
	effectPath          string
	markerPath          string
	blockAfterExecution bool
}

func (invoker *crashBoundaryActionInvoker) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return invoker.delegate.Invoke(ctx, prepared)
}

func (invoker *crashBoundaryActionInvoker) Describe(
	ctx context.Context,
	request moduleapi.ActionDescribeRequestV1,
) ([]moduleapi.ActionDefinitionV1, error) {
	return invoker.delegate.Describe(ctx, request)
}

func (invoker *crashBoundaryActionInvoker) Prepare(
	ctx context.Context,
	request moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	return invoker.delegate.Prepare(ctx, request)
}

func (invoker *crashBoundaryActionInvoker) ExecutePrepared(
	ctx context.Context,
	execution modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	result, err := invoker.delegate.ExecutePrepared(ctx, execution)
	if err != nil {
		return moduleapi.ActionExecutionResultV1{}, err
	}
	if err := appendActionCrashEffect(
		invoker.effectPath,
		execution.Request.AttemptID,
	); err != nil {
		return moduleapi.ActionExecutionResultV1{}, err
	}
	if !invoker.blockAfterExecution {
		return result, nil
	}
	if err := publishCrashMarker(
		invoker.markerPath,
		[]byte(fmt.Sprintf(
			"%s\n%s\n",
			crashPhaseAfterAction,
			execution.Request.AttemptID,
		)),
	); err != nil {
		return moduleapi.ActionExecutionResultV1{}, err
	}
	select {}
}

func appendActionCrashEffect(path string, attemptID string) (returnErr error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	if _, err := fmt.Fprintln(file, attemptID); err != nil {
		return err
	}
	return file.Sync()
}

type crashBoundaryInvoker struct {
	phase      string
	markerPath string
	delegate   modulehost.ModuleInvoker
}

func (invoker *crashBoundaryInvoker) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	if invoker.phase == crashPhaseBeforeModel {
		if err := invoker.writeMarker(prepared); err != nil {
			return modulehost.InvocationResult{}, err
		}
		select {}
	}
	if _, err := invoker.delegate.Invoke(ctx, prepared); err != nil {
		return modulehost.InvocationResult{}, err
	}
	if invoker.phase != crashPhaseAfterModel {
		return modulehost.InvocationResult{}, fmt.Errorf(
			"unsupported crash invoker phase %q",
			invoker.phase,
		)
	}
	if err := invoker.writeMarker(prepared); err != nil {
		return modulehost.InvocationResult{}, err
	}
	select {}
}

func (invoker *crashBoundaryInvoker) writeMarker(
	prepared modulehost.PreparedInvocation,
) error {
	return publishCrashMarker(
		invoker.markerPath,
		[]byte(fmt.Sprintf(
			"%s\n%s\n%s\n",
			invoker.phase,
			prepared.Invocation.RunID,
			prepared.Invocation.InvocationID,
		)),
	)
}

// publishCrashMarker exposes the marker only after its complete contents are
// durable. In particular, Windows readers must never observe the empty file
// created by os.WriteFile before the helper has written the marker body.
func publishCrashMarker(path string, content []byte) (returnErr error) {
	temporary, err := os.CreateTemp(
		filepath.Dir(path),
		"."+filepath.Base(path)+".tmp-*",
	)
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if removeErr := os.Remove(temporaryPath); removeErr != nil &&
			!errors.Is(removeErr, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, removeErr)
		}
	}()
	if _, err := temporary.Write(content); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if err := temporary.Sync(); err != nil {
		return errors.Join(err, temporary.Close())
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func crashChatInput(test *crashProcessCase) localchat.ChatInput {
	return localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: defaultPrincipalID,
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   defaultProfileID,
		Message:     test.message,
		RequestID:   test.requestID,
		Deadline:    test.deadline,
	}
}

func expireCrashedRunLease(
	t *testing.T,
	databasePath string,
	runID string,
) {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	result, err := database.Exec(`
		UPDATE loop_frames
		SET lease_expiry=1
		WHERE run_id=? AND lease_owner IS NOT NULL
	`, runID)
	if err != nil {
		t.Fatalf("expire crashed lease: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("expired lease rows=%d error=%v", affected, err)
	}
}

func assertOnlyOriginalCrashAttempt(
	t *testing.T,
	test *crashProcessCase,
) {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(test.databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	var attemptID string
	var state string
	if err := database.QueryRow(`
		SELECT COUNT(*), MIN(attempt_id), MIN(state)
		FROM model_dispatch_attempts
		WHERE run_id=?
	`, test.runID).Scan(&count, &attemptID, &state); err != nil {
		t.Fatal(err)
	}
	if count != 1 ||
		attemptID != test.attemptID ||
		state != string(corecontract.ModelAttemptUnknown) {
		t.Fatalf(
			"%s Attempts count=%d id=%q state=%q",
			test.phase,
			count,
			attemptID,
			state,
		)
	}
}

func loadPendingActionCrashIdentity(
	t *testing.T,
	databasePath string,
	attemptID string,
) (string, string) {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var runID string
	var operationKey string
	var state string
	if err := database.QueryRow(`
		SELECT run_id, logical_operation_key, state
		FROM dispatch_attempts
		WHERE attempt_id=?
	`, attemptID).Scan(&runID, &operationKey, &state); err != nil {
		t.Fatalf("load killed Action Attempt: %v", err)
	}
	if state != string(currentstore.ActionDispatchPending) {
		t.Fatalf("killed Action state=%q, want PENDING", state)
	}
	return runID, operationKey
}

func assertActionCrashEffects(
	t *testing.T,
	path string,
	attemptID string,
) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Action effect ledger: %v", err)
	}
	effects := strings.Fields(string(content))
	if len(effects) != 1 || effects[0] != attemptID {
		t.Fatalf(
			"Action executor effects=%q, want one effect for %q",
			effects,
			attemptID,
		)
	}
}

func crashSQLiteURI(path string, mode string) string {
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := &url.URL{Scheme: "file", Path: uriPath}
	query := uri.Query()
	query.Set("mode", mode)
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	uri.RawQuery = query.Encode()
	return uri.String()
}
