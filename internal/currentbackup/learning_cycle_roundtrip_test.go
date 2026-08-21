package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	cycleBackupPendingObjective         = "Preserve a pending Learning cycle window."
	cycleBackupRunAdmittedObjective     = "Preserve an admitted Learning cycle Run."
	cycleBackupNoChangeObjective        = "Return a bounded no-change Learning result."
	cycleBackupKnowledgeObjective       = "Return one bounded Knowledge proposal."
	cycleBackupSkillObjective           = "Return one bounded static Skill proposal."
	cycleBackupFailedObjective          = "Return a deterministic failed model outcome."
	cycleBackupUnknownObjective         = "Return an uncertain model outcome."
	cycleBackupReconciledCrashObjective = "Leave the same UNKNOWN Attempt reconciled before task projection."
)

func TestLearningCycleBundleRoundTripPreservesEveryTaskShapeWithoutExecution(
	t *testing.T,
) {
	fixture := newLearningCycleBackupFixture(t)
	ctx := context.Background()
	baselineCalls := fixture.invoker.callCount()
	bundle := filepath.Join(t.TempDir(), "learning-cycle.bundle")
	if _, err := CreateBundle(
		ctx,
		fixture.databasePath,
		fixture.artifactRoot,
		bundle,
		"learning-cycle-roundtrip-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle: %v", err)
	}
	if _, err := VerifyBundle(ctx, bundle); err != nil {
		t.Fatalf("VerifyBundle: %v", err)
	}
	if got := fixture.invoker.callCount(); got != baselineCalls {
		t.Fatalf("backup verification invoked model: calls=%d want=%d", got, baselineCalls)
	}

	restoredDatabase := filepath.Join(t.TempDir(), "restored.sqlite")
	restoredArtifacts := filepath.Join(t.TempDir(), "restored-artifacts")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("RestoreBundle: %v", err)
	}
	if got := fixture.invoker.callCount(); got != baselineCalls {
		t.Fatalf("restore invoked model: calls=%d want=%d", got, baselineCalls)
	}

	restored, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatalf("OpenExistingCurrentStore(restored): %v", err)
	}
	defer restored.Close()
	for name, want := range fixture.schedules {
		got, err := restored.GetLearningCycleSchedule(
			ctx,
			want.Schedule.TenantID,
			want.Schedule.ScheduleID,
		)
		if err != nil {
			t.Fatalf("GetLearningCycleSchedule(%s): %v", name, err)
		}
		assertLearningCycleBackupScheduleEqual(t, name, got, want)
	}
	for name, want := range fixture.tasks {
		got, err := restored.GetLearningCycleTask(ctx, want.TenantID, want.TaskID)
		if err != nil {
			t.Fatalf("GetLearningCycleTask(%s): %v", name, err)
		}
		assertLearningCycleBackupTaskEqual(t, name, got, want)
	}
	if got := fixture.invoker.callCount(); got != baselineCalls {
		t.Fatalf("Store open/read invoked model: calls=%d want=%d", got, baselineCalls)
	}

	// Restore/Open is deliberately idle. Only this explicit Store Tick observes
	// the overdue clock, and it coalesces all missed slots into one latest
	// logical window without invoking the model or any other adapter.
	noChange := fixture.tasks["NO_CHANGE"]
	before, err := restored.ListLearningCycleTasks(
		ctx,
		noChange.TenantID,
		noChange.ScheduleID,
	)
	if err != nil || len(before) != 1 {
		t.Fatalf("tasks before explicit Tick=%+v error=%v", before, err)
	}
	observed := fixture.firstDue.Add(10*24*time.Hour + 12*time.Hour)
	due, err := restored.EnsureDueTask(
		ctx,
		noChange.TenantID,
		noChange.ScheduleID,
		observed,
	)
	if err != nil || !due.Due || !due.Created ||
		due.Task.State != learningcontract.LearningCycleTaskPendingV1 ||
		!due.Task.ScheduledFor.Equal(fixture.firstDue.Add(10*24*time.Hour)) {
		t.Fatalf("explicit coalesced Tick=%+v error=%v", due, err)
	}
	after, err := restored.ListLearningCycleTasks(
		ctx,
		noChange.TenantID,
		noChange.ScheduleID,
	)
	if err != nil || len(after) != 2 {
		t.Fatalf("tasks after explicit Tick=%+v error=%v", after, err)
	}
	if got := fixture.invoker.callCount(); got != baselineCalls {
		t.Fatalf("Store-only Tick invoked model: calls=%d want=%d", got, baselineCalls)
	}
}

func TestLearningCycleBackupSemanticGateRejectsCoordinatedTamper(t *testing.T) {
	fixture := newLearningCycleBackupFixture(t)
	tests := []struct {
		name   string
		tamper func(*testing.T, *sql.DB, learningCycleBackupFixture)
	}{
		{name: "Schedule Request and TaskID", tamper: tamperLearningCycleScheduleRequestTask},
		{name: "Run binding", tamper: tamperLearningCycleRunBinding},
		{name: "Attempt binding", tamper: tamperLearningCycleAttemptBinding},
		{name: "Result binding", tamper: tamperLearningCycleResultBinding},
		{name: "Proposal binding", tamper: tamperLearningCycleProposalBinding},
		{name: "revision without reconciliation", tamper: tamperLearningCycleRevision},
		{name: "reconciliation evidence", tamper: tamperLearningCycleEvidence},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "tampered.sqlite")
			copyTestFile(t, fixture.databasePath, databasePath)
			database, err := sql.Open(
				"sqlite",
				sqliteFileURI(databasePath, "rw", "foreign_keys(0)"),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			test.tamper(t, database, fixture)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				databasePath,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("semantic gate error=%v, want ErrIntegrity", err)
			}
			if _, err := CreateBundle(
				context.Background(),
				databasePath,
				fixture.artifactRoot,
				filepath.Join(t.TempDir(), "rejected.bundle"),
				"learning-cycle-tamper-test/v1",
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("CreateBundle tamper error=%v, want ErrIntegrity", err)
			}
		})
	}
}

type learningCycleBackupFixture struct {
	databasePath string
	artifactRoot string
	firstDue     time.Time
	tasks        map[string]currentstore.LearningCycleTaskRecord
	schedules    map[string]currentstore.LearningCycleScheduleRecord
	review       learningReviewBackupFixture
	invoker      *learningCycleBackupInvoker
}

func newLearningCycleBackupFixture(t *testing.T) learningCycleBackupFixture {
	t.Helper()
	ctx := context.Background()
	review := newLearningReviewBackupFixture(t)
	databasePath := review.base.base.databasePath
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()
	providerAttempt, err := store.GetModelDispatchRecord(
		ctx,
		review.records["APPROVED"].ReviewerAttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := providerAttempt.Attempt.Binding.Provider
	invoker := &learningCycleBackupInvoker{provider: provider}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         invoker,
	})
	if err != nil {
		t.Fatal(err)
	}
	realLoop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		t.Fatal(err)
	}
	realChat, err := localchat.NewChatService(store, realLoop)
	if err != nil {
		t.Fatal(err)
	}
	realService, err := localchat.NewLearningCycleService(realChat)
	if err != nil {
		t.Fatal(err)
	}
	failingChat, err := localchat.NewChatService(
		store,
		learningCycleBackupFailLoop{failure: errors.New("injected pre-execution Loop failure")},
	)
	if err != nil {
		t.Fatal(err)
	}
	failingService, err := localchat.NewLearningCycleService(failingChat)
	if err != nil {
		t.Fatal(err)
	}

	firstDue := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	fixture := learningCycleBackupFixture{
		databasePath: databasePath,
		artifactRoot: review.base.base.artifactRoot,
		firstDue:     firstDue,
		tasks:        make(map[string]currentstore.LearningCycleTaskRecord),
		schedules:    make(map[string]currentstore.LearningCycleScheduleRecord),
		review:       review,
		invoker:      invoker,
	}
	create := func(
		name string,
		kind learningcontract.ProposalKindV1,
		objective string,
	) currentstore.LearningCycleScheduleRecord {
		t.Helper()
		visibility := []moduleapi.KnowledgeScopeRuleV1{}
		if kind == learningcontract.ProposalKindKnowledgeV1 {
			visibility = []moduleapi.KnowledgeScopeRuleV1{{
				TenantID:    review.base.record.Proposal.TenantID,
				WorkspaceID: "*", AgentID: "*", TaskInputRef: "*",
			}}
		}
		created, err := store.CreateLearningCycleSchedule(
			ctx,
			learningcontract.LearningCycleScheduleV1{
				SchemaVersion:        learningcontract.LearningCycleScheduleSchemaVersionV1,
				TenantID:             review.base.record.Proposal.TenantID,
				ScheduleID:           "backup-cycle-" + strings.ToLower(name),
				ServicePrincipalID:   "backup-test-principal",
				WorkspaceID:          review.base.record.Proposal.Workspace.ID,
				AgentID:              review.reviewerAgent.ID,
				ProfileID:            review.reviewerProfile.ID,
				Kind:                 kind,
				TargetModuleID:       "freeagent.backup.cycle." + strings.ToLower(strings.ReplaceAll(name, "_", "-")),
				Objective:            objective,
				KnowledgeVisibility:  visibility,
				FirstDueAtUnixMicros: firstDue.UnixMicro(),
				MaxOutputTokens:      512,
			},
		)
		if err != nil || !created.Created {
			t.Fatalf("CreateLearningCycleSchedule(%s)=%+v error=%v", name, created, err)
		}
		enabled, err := store.SetLearningCycleScheduleEnabled(
			ctx,
			currentstore.SetLearningCycleScheduleEnabledInput{
				TenantID:         created.Record.Schedule.TenantID,
				ScheduleID:       created.Record.Schedule.ScheduleID,
				ExpectedRevision: created.Record.Revision,
				Enabled:          true,
			},
		)
		if err != nil || !enabled.Applied {
			t.Fatalf("enable Learning Schedule(%s)=%+v error=%v", name, enabled, err)
		}
		fixture.schedules[name] = enabled.Record
		return enabled.Record
	}

	pending := create("PENDING", learningcontract.ProposalKindSkillV1, cycleBackupPendingObjective)
	pendingDue, err := store.EnsureDueTask(
		ctx, pending.Schedule.TenantID, pending.Schedule.ScheduleID, firstDue,
	)
	if err != nil || !pendingDue.Created ||
		pendingDue.Task.State != learningcontract.LearningCycleTaskPendingV1 {
		t.Fatalf("PENDING due=%+v error=%v", pendingDue, err)
	}
	fixture.tasks["PENDING"] = pendingDue.Task
	fixture.schedules["PENDING"] = pendingDue.Schedule

	admitted := create(
		"RUN_ADMITTED",
		learningcontract.ProposalKindSkillV1,
		cycleBackupRunAdmittedObjective,
	)
	admittedTick, err := failingService.Tick(ctx, localchat.LearningCycleTickInput{
		TenantID: admitted.Schedule.TenantID, ScheduleID: admitted.Schedule.ScheduleID,
		ObservedAt: firstDue,
	})
	if err == nil || admittedTick.Task.State != learningcontract.LearningCycleTaskRunAdmittedV1 {
		t.Fatalf("RUN_ADMITTED Tick=%+v error=%v", admittedTick, err)
	}
	fixture.tasks["RUN_ADMITTED"] = admittedTick.Task
	fixture.schedules["RUN_ADMITTED"] = admittedTick.Schedule

	terminalCases := []struct {
		name      string
		kind      learningcontract.ProposalKindV1
		objective string
		want      learningcontract.LearningCycleTaskStateV1
	}{
		{"NO_CHANGE", learningcontract.ProposalKindSkillV1, cycleBackupNoChangeObjective, learningcontract.LearningCycleTaskNoChangeV1},
		{"KNOWLEDGE", learningcontract.ProposalKindKnowledgeV1, cycleBackupKnowledgeObjective, learningcontract.LearningCycleTaskProposalSubmittedV1},
		{"SKILL", learningcontract.ProposalKindSkillV1, cycleBackupSkillObjective, learningcontract.LearningCycleTaskProposalSubmittedV1},
		{"FAILED", learningcontract.ProposalKindSkillV1, cycleBackupFailedObjective, learningcontract.LearningCycleTaskFailedV1},
		{"UNKNOWN", learningcontract.ProposalKindSkillV1, cycleBackupUnknownObjective, learningcontract.LearningCycleTaskUnknownV1},
		{"RECONCILED_CRASH", learningcontract.ProposalKindSkillV1, cycleBackupReconciledCrashObjective, learningcontract.LearningCycleTaskUnknownV1},
	}
	for _, test := range terminalCases {
		schedule := create(test.name, test.kind, test.objective)
		tick, tickErr := realService.Tick(ctx, localchat.LearningCycleTickInput{
			TenantID:   schedule.Schedule.TenantID,
			ScheduleID: schedule.Schedule.ScheduleID,
			ObservedAt: firstDue,
		})
		if tickErr != nil || tick.Task.State != test.want || tick.Task.Revision != 2 {
			t.Fatalf("Tick(%s)=%+v error=%v", test.name, tick, tickErr)
		}
		fixture.tasks[test.name] = tick.Task
		fixture.schedules[test.name] = tick.Schedule
	}

	fixture.tasks["RECONCILED_CRASH"] = reconcileLearningCycleBackupAttempt(
		t,
		store,
		databasePath,
		fixture.tasks["RECONCILED_CRASH"],
	)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return fixture
}

type learningCycleBackupInvoker struct {
	mu       sync.Mutex
	provider moduleapi.ActivatedModuleRef
	calls    int
}

func (invoker *learningCycleBackupInvoker) Invoke(
	_ context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	invoker.mu.Lock()
	invoker.calls++
	invoker.mu.Unlock()
	request, err := restoreLearningCycleBackupRequest(prepared.Invocation.Input)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	result := modulehost.InvocationResult{
		InvocationID: prepared.Invocation.InvocationID,
		Provider:     invoker.provider,
	}
	switch request.Objective {
	case cycleBackupFailedObjective:
		result.Outcome = modulehost.InvocationFailed
		return result, nil
	case cycleBackupUnknownObjective, cycleBackupReconciledCrashObjective:
		result.Outcome = modulehost.InvocationUnknown
		result.UnknownClass = modulehost.UnknownClassResponseBodyReadIncomplete
		return result, nil
	}
	cycleResult := learningcontract.LearningCycleResultV1{
		SchemaVersion:   learningcontract.LearningCycleResultSchemaVersionV1,
		RequestDigest:   request.RequestDigest,
		Decision:        learningcontract.LearningCycleResultNoChangeV1,
		KnowledgeChunks: []string{},
	}
	switch request.Objective {
	case cycleBackupNoChangeObjective:
	case cycleBackupKnowledgeObjective:
		cycleResult.Decision = learningcontract.LearningCycleResultProposeV1
		cycleResult.KnowledgeChunks = []string{
			"Backup verification preserves this bounded Knowledge candidate.",
		}
	case cycleBackupSkillObjective:
		cycleResult.Decision = learningcontract.LearningCycleResultProposeV1
		cycleResult.SkillText = "Use the exact restored Learning cycle closure."
	default:
		return modulehost.InvocationResult{}, fmt.Errorf(
			"currentbackup: unsupported Learning cycle objective %q",
			request.Objective,
		)
	}
	output, err := learningCycleBackupModelOutput(cycleResult)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	result.Outcome = modulehost.InvocationSucceeded
	result.Output = output
	return result, nil
}

func (invoker *learningCycleBackupInvoker) callCount() int {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	return invoker.calls
}

func restoreLearningCycleBackupRequest(
	canonical []byte,
) (learningcontract.LearningCycleRequestV1, error) {
	request, err := moduleapi.RestoreModelGenerateRequestV1(canonical)
	if err != nil {
		return learningcontract.LearningCycleRequestV1{}, err
	}
	for index := len(request.Messages) - 1; index >= 0; index-- {
		var candidate learningcontract.LearningCycleRequestV1
		if err := json.Unmarshal([]byte(request.Messages[index].Content), &candidate); err != nil ||
			candidate.SchemaVersion != learningcontract.LearningCycleRequestSchemaVersionV1 {
			continue
		}
		return learningcontract.RestoreLearningCycleRequestV1(
			[]byte(request.Messages[index].Content),
			candidate.RequestDigest,
		)
	}
	return learningcontract.LearningCycleRequestV1{}, errors.New(
		"currentbackup: model request contains no Learning cycle request",
	)
}

func learningCycleBackupModelOutput(
	result learningcontract.LearningCycleResultV1,
) ([]byte, error) {
	_, resultCanonical, _, err := learningcontract.NewLearningCycleResultV1(result)
	if err != nil {
		return nil, err
	}
	_, output, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(resultCanonical),
		},
	)
	return output, err
}

type learningCycleBackupFailLoop struct{ failure error }

func (loop learningCycleBackupFailLoop) Run(
	context.Context,
	loopapi.RunInput,
) (loopapi.RunResult, error) {
	return loopapi.RunResult{}, loop.failure
}

func reconcileLearningCycleBackupAttempt(
	t *testing.T,
	store *currentstore.Store,
	databasePath string,
	task currentstore.LearningCycleTaskRecord,
) currentstore.LearningCycleTaskRecord {
	t.Helper()
	ctx := context.Background()
	attempt, err := store.GetModelDispatchRecord(ctx, task.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	var runRevision, frameRevision uint64
	database, err := sql.Open("sqlite", sqliteFileURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
		SELECT run.revision, frame.frame_revision
		FROM runs AS run
		JOIN loop_frames AS frame ON frame.run_id=run.run_id
		WHERE run.run_id=?
	`, task.RunID).Scan(&runRevision, &frameRevision); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireRunLease(ctx, currentstore.AcquireRunLeaseInput{
		RunID: task.RunID, OwnerID: "backup-cycle-reconciliation",
		ExpectedRunRevision: runRevision, ExpectedFrameRevision: frameRevision,
		TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := learningCycleBackupModelOutput(
		learningcontract.LearningCycleResultV1{
			SchemaVersion:   learningcontract.LearningCycleResultSchemaVersionV1,
			RequestDigest:   task.RequestDigest,
			Decision:        learningcontract.LearningCycleResultNoChangeV1,
			KnowledgeChunks: []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := store.CommitModelDispatchOutcome(
		ctx,
		currentstore.CommitModelDispatchOutcomeInput{
			Lease:                           lease,
			AttemptID:                       task.AttemptID,
			InvocationID:                    task.AttemptID,
			Provider:                        attempt.Attempt.Binding.Provider,
			ExpectedAttemptRevision:         attempt.Attempt.Revision,
			State:                           corecontract.ModelAttemptSucceeded,
			OutputCanonical:                 output,
			ReconciliationEvidenceCanonical: []byte(`{"kind":"provider_lookup","request_id":"backup-cycle"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseRunLease(ctx, reconciled.Lease); err != nil {
		t.Fatal(err)
	}
	stillUnknown, err := store.GetLearningCycleTask(ctx, task.TenantID, task.TaskID)
	if err != nil || stillUnknown.State != learningcontract.LearningCycleTaskUnknownV1 ||
		stillUnknown.Revision != 2 {
		t.Fatalf("reconciled crash task=%+v error=%v", stillUnknown, err)
	}
	return stillUnknown
}

func assertLearningCycleBackupTaskEqual(
	t *testing.T,
	name string,
	got currentstore.LearningCycleTaskRecord,
	want currentstore.LearningCycleTaskRecord,
) {
	t.Helper()
	if got.TaskID != want.TaskID || got.TenantID != want.TenantID ||
		got.ScheduleID != want.ScheduleID || got.ScheduleDigest != want.ScheduleDigest ||
		!got.ScheduledFor.Equal(want.ScheduledFor) || got.RequestDigest != want.RequestDigest ||
		!bytes.Equal(got.RequestCanonical, want.RequestCanonical) || got.State != want.State ||
		got.Revision != want.Revision || got.RunID != want.RunID ||
		got.RunManifestDigest != want.RunManifestDigest || got.AttemptID != want.AttemptID ||
		got.ResultRef != want.ResultRef || got.ProposalID != want.ProposalID ||
		!got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("restored task %s=%+v want=%+v", name, got, want)
	}
}

func assertLearningCycleBackupScheduleEqual(
	t *testing.T,
	name string,
	got currentstore.LearningCycleScheduleRecord,
	want currentstore.LearningCycleScheduleRecord,
) {
	t.Helper()
	if got.ScheduleDigest != want.ScheduleDigest ||
		!bytes.Equal(got.ScheduleCanonical, want.ScheduleCanonical) ||
		got.Enabled != want.Enabled || got.Revision != want.Revision ||
		!got.LastScheduledFor.Equal(want.LastScheduledFor) ||
		!got.NextDueAt.Equal(want.NextDueAt) ||
		!got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("restored Schedule %s=%+v want=%+v", name, got, want)
	}
}

func tamperLearningCycleScheduleRequestTask(
	t *testing.T,
	database *sql.DB,
	fixture learningCycleBackupFixture,
) {
	t.Helper()
	task := fixture.tasks["NO_CHANGE"]
	schedule := fixture.schedules["NO_CHANGE"]
	changed := schedule.Schedule
	changed.Objective = "Coordinated attacker-rewritten Learning objective."
	_, scheduleCanonical, scheduleDigest, err := learningcontract.NewLearningCycleScheduleV1(changed)
	if err != nil {
		t.Fatal(err)
	}
	_, requestCanonical, requestDigest, err := learningcontract.NewLearningCycleRequestV1(
		changed,
		scheduleCanonical,
		scheduleDigest,
		task.ScheduledFor.UnixMicro(),
	)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := learningcontract.DeriveLearningCycleTaskIDV1(
		scheduleDigest,
		task.ScheduledFor.UnixMicro(),
		requestDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	execClosedFileTamperV1(
		t,
		database,
		[]string{"learning_cycle_schedules_frozen_projection_guard"},
		`
		UPDATE learning_cycle_schedules
		SET schedule_digest=?, schedule_canonical=?, schedule_size_bytes=?
		WHERE tenant_id=? AND schedule_id=?
	`,
		scheduleDigest,
		scheduleCanonical,
		len(scheduleCanonical),
		task.TenantID,
		task.ScheduleID,
	)
	execClosedFileTamperV1(
		t,
		database,
		[]string{"learning_cycle_tasks_observation_update_guard"},
		`
		UPDATE learning_cycle_tasks
		SET task_id=?, schedule_digest=?, request_digest=?,
			request_canonical=?, request_size_bytes=?
		WHERE task_id=?
	`,
		taskID,
		scheduleDigest,
		requestDigest,
		requestCanonical,
		len(requestCanonical),
		task.TaskID,
	)
}

func tamperLearningCycleRunBinding(
	t *testing.T,
	database *sql.DB,
	fixture learningCycleBackupFixture,
) {
	t.Helper()
	task := fixture.tasks["RUN_ADMITTED"]
	var manifestDigest string
	if err := database.QueryRow(
		`SELECT digest FROM run_manifests WHERE run_id=?`,
		fixture.review.base.base.successfulRunID,
	).Scan(&manifestDigest); err != nil {
		t.Fatal(err)
	}
	execClosedFileTamperV1(
		t,
		database,
		[]string{"learning_cycle_tasks_observation_update_guard"},
		`
		UPDATE learning_cycle_tasks SET run_id=?, run_manifest_digest=? WHERE task_id=?
	`,
		fixture.review.base.base.successfulRunID,
		manifestDigest,
		task.TaskID,
	)
}

func tamperLearningCycleAttemptBinding(
	t *testing.T,
	database *sql.DB,
	fixture learningCycleBackupFixture,
) {
	t.Helper()
	task := fixture.tasks["NO_CHANGE"]
	execClosedFileTamperV1(
		t,
		database,
		[]string{"learning_cycle_tasks_observation_update_guard"},
		`
		UPDATE learning_cycle_tasks SET attempt_id=? WHERE task_id=?
	`,
		fixture.review.base.base.pendingAttemptID,
		task.TaskID,
	)
}

func tamperLearningCycleResultBinding(
	t *testing.T,
	database *sql.DB,
	fixture learningCycleBackupFixture,
) {
	t.Helper()
	task := fixture.tasks["NO_CHANGE"]
	var resultRef string
	if err := database.QueryRow(`
		SELECT result_ref FROM model_dispatch_attempts
		WHERE run_id=? AND state='SUCCEEDED'
	`, fixture.review.base.base.successfulRunID).Scan(&resultRef); err != nil {
		t.Fatal(err)
	}
	execClosedFileTamperV1(
		t,
		database,
		[]string{"learning_cycle_tasks_observation_update_guard"},
		`
		UPDATE learning_cycle_tasks SET result_ref=? WHERE task_id=?
	`,
		resultRef,
		task.TaskID,
	)
}

func tamperLearningCycleProposalBinding(
	t *testing.T,
	database *sql.DB,
	fixture learningCycleBackupFixture,
) {
	t.Helper()
	task := fixture.tasks["SKILL"]
	execClosedFileTamperV1(
		t,
		database,
		[]string{"learning_cycle_tasks_observation_update_guard"},
		`
		UPDATE learning_cycle_tasks SET proposal_id=? WHERE task_id=?
	`,
		fixture.review.records["REJECTED"].ProposalID,
		task.TaskID,
	)
}

func tamperLearningCycleRevision(
	t *testing.T,
	database *sql.DB,
	fixture learningCycleBackupFixture,
) {
	t.Helper()
	execClosedFileTamperV1(
		t,
		database,
		[]string{"learning_cycle_tasks_observation_update_guard"},
		`
		UPDATE learning_cycle_tasks SET revision=3 WHERE task_id=?
	`,
		fixture.tasks["NO_CHANGE"].TaskID,
	)
}

func tamperLearningCycleEvidence(
	t *testing.T,
	database *sql.DB,
	fixture learningCycleBackupFixture,
) {
	t.Helper()
	execClosedFileTamperV1(
		t,
		database,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`
		UPDATE model_dispatch_attempts SET reconciliation_evidence_ref=NULL
		WHERE attempt_id=?
	`,
		fixture.tasks["RECONCILED_CRASH"].AttemptID,
	)
}
