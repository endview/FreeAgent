package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const w4LearningCycleLiveEnabledEnvironment = "FREEAGENT_W4_L4_LIVE"

// TestW4L4LiveDeepSeekLearningCycle is deliberately opt-in. It verifies the
// production CLI chain through an independent model-only JSON Profile, one
// explicitly enabled 24-hour Schedule, one explicit Tick, the Universal Loop,
// one DeepSeek Attempt/Usage receipt, Store-only finalization, and a same-slot
// retry with no semantic replay. The test passes only an environment variable
// name to the runtime resolver and never reads, prints, or persists its value.
func TestW4L4LiveDeepSeekLearningCycle(t *testing.T) {
	if os.Getenv(w4LearningCycleLiveEnabledEnvironment) != "1" {
		t.Skip("set FREEAGENT_W4_L4_LIVE=1 to run the explicit live Learning cycle")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	seedPath := filepath.Join(
		filepath.Dir(exampleSeedPath(t)),
		"current-v1.deepseek.bootstrap.seed.json",
	)
	if err := run(ctx, []string{
		"init",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--seed", seedPath,
	}, io.Discard, io.Discard); err != nil {
		t.Fatalf("initialize W4-L4 live data: %v", err)
	}

	_, configCanonical, err := moduleapi.NewModelBindingConfigV1(
		moduleapi.ModelBindingConfigV1{
			SchemaVersion:   moduleapi.ModelBindingConfigSchemaV1,
			Provider:        "deepseek",
			Model:           "deepseek-v4-flash",
			ModelBuildID:    localDeepSeekFlashBuild,
			BillingVersion:  "deepseek-public-price-2026-08-04",
			PriceSnapshotID: "price-deepseek-v4-flash-2026-08-04",
			Parameters: json.RawMessage(
				`{"max_tokens":512,"response_format":{"type":"json_object"},"temperature":0,"thinking":{"type":"disabled"}}`,
			),
		},
	)
	if err != nil {
		t.Fatalf("freeze W4-L4 live JSON model Config: %v", err)
	}
	profileStore, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open W4-L4 live Store for Profile publication: %v", err)
	}
	profileID := publishW4LearningCycleModelOnlyProfile(
		t,
		ctx,
		profileStore,
		"deepseek-chat",
		"w4-l4-live-cycle-profile",
		configCanonical,
	)
	if err := profileStore.Close(); err != nil {
		t.Fatalf("close W4-L4 live Profile Store: %v", err)
	}

	firstDue := time.Now().UTC().Truncate(time.Microsecond)
	schedule := learningCycleCLITestSchedule("w4-l4-live-cycle", firstDue)
	schedule.ProfileID = profileID
	schedule.IntervalSeconds = 0
	schedule.MaxOutputTokens = 512
	schedule.Objective = "Candidate reusable skill content: audit immutable " +
		"module configuration by comparing the exact canonical configuration " +
		"digest before and after review to detect drift."
	inputPath := writeLearningCycleScheduleInput(
		t,
		root,
		"w4-l4-live-cycle.json",
		schedule,
	)
	created := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-create",
		"--db", databasePath,
		"--input", inputPath,
	})
	if !created.Created || created.Enabled || created.Revision != 0 ||
		created.Schedule.IntervalSeconds != learningcontract.DefaultLearningCycleIntervalSecondsV1 {
		t.Fatalf(
			"live Schedule creation state=(created=%t enabled=%t revision=%d interval=%d)",
			created.Created,
			created.Enabled,
			created.Revision,
			created.Schedule.IntervalSeconds,
		)
	}
	enabled := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-enable",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--expected-revision", "0",
	})
	if !enabled.Enabled || !enabled.Applied || enabled.Revision != 1 {
		t.Fatalf(
			"live Schedule enablement state=(enabled=%t applied=%t revision=%d)",
			enabled.Enabled,
			enabled.Applied,
			enabled.Revision,
		)
	}

	tickArgs := []string{
		"learning-cycle-tick",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--observed-at", firstDue.Format(time.RFC3339Nano),
		"--enable-deepseek",
		"--deepseek-api-key-env", defaultDeepSeekAPIKeyEnvironment,
	}
	var firstOutput bytes.Buffer
	if err := run(ctx, tickArgs, &firstOutput, io.Discard); err != nil {
		t.Fatalf("run W4-L4 live Tick: %v", err)
	}
	var first learningCycleTickCommandResult
	if err := json.Unmarshal(firstOutput.Bytes(), &first); err != nil {
		t.Fatalf("decode sanitized W4-L4 live Tick: %v", err)
	}
	if !first.Due || !first.TaskCreated || !first.AdmissionCreated ||
		!first.FinalizeApplied || first.Task == nil ||
		(first.Task.State != string(learningcontract.LearningCycleTaskProposalSubmittedV1) &&
			first.Task.State != string(learningcontract.LearningCycleTaskNoChangeV1)) ||
		first.Task.RunID == "" || first.Task.AttemptID == "" ||
		first.Loop == nil || first.Usage == nil {
		t.Fatalf(
			"live Tick receipts=(due=%t task_created=%t admission_created=%t finalize_applied=%t state=%s)",
			first.Due,
			first.TaskCreated,
			first.AdmissionCreated,
			first.FinalizeApplied,
			learningCycleCommandTaskState(first.Task),
		)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open W4-L4 live Store for verification: %v", err)
	}
	tasks, err := store.ListLearningCycleTasks(ctx, schedule.TenantID, schedule.ScheduleID)
	if err != nil || len(tasks) != 1 || tasks[0].RunID != first.Task.RunID ||
		tasks[0].AttemptID != first.Task.AttemptID {
		_ = store.Close()
		t.Fatalf("live task cardinality=%d error=%v", len(tasks), err)
	}
	dispatch, err := store.GetModelDispatchRecord(ctx, first.Task.AttemptID)
	if err != nil {
		_ = store.Close()
		t.Fatalf("read W4-L4 live Attempt/Usage: %v", err)
	}
	if dispatch.Attempt.RunID != first.Task.RunID ||
		dispatch.Attempt.State != corecontract.ModelAttemptSucceeded ||
		dispatch.Attempt.Model != "deepseek-v4-flash" ||
		dispatch.Attempt.Provider != "deepseek" ||
		dispatch.Attempt.ProviderRequestID == "" ||
		dispatch.Usage.AttemptID != first.Task.AttemptID ||
		dispatch.Usage.Tokens.Input == nil ||
		dispatch.Usage.Tokens.CachedInput == nil ||
		dispatch.Usage.Tokens.UncachedInput == nil ||
		dispatch.Usage.Tokens.Output == nil ||
		*dispatch.Usage.Tokens.Input !=
			*dispatch.Usage.Tokens.CachedInput+*dispatch.Usage.Tokens.UncachedInput ||
		dispatch.Usage.EstimatedCost == nil || dispatch.Usage.RawReceiptRef == "" ||
		dispatch.Usage.ReconciliationStatus != "PROVIDER_REPORTED" {
		_ = store.Close()
		t.Fatalf(
			"live Attempt/Usage state=(attempt=%s provider=%s model=%s usage=%s)",
			dispatch.Attempt.State,
			dispatch.Attempt.Provider,
			dispatch.Attempt.Model,
			dispatch.Usage.ReconciliationStatus,
		)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close verified W4-L4 live Store: %v", err)
	}
	assertW4L4LiveCycleCardinality(t, databasePath)

	var retryOutput bytes.Buffer
	if err := run(ctx, tickArgs, &retryOutput, io.Discard); err != nil {
		t.Fatalf("repeat W4-L4 live same-window Tick: %v", err)
	}
	var retry learningCycleTickCommandResult
	if err := json.Unmarshal(retryOutput.Bytes(), &retry); err != nil {
		t.Fatalf("decode W4-L4 live retry: %v", err)
	}
	if retry.Due || retry.TaskCreated || retry.AdmissionCreated ||
		retry.FinalizeApplied || retry.Task != nil || retry.Loop != nil || retry.Usage != nil {
		t.Fatalf("live same-window Tick replayed semantic work")
	}

	verifyStore, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("reopen W4-L4 live Store after retry: %v", err)
	}
	retryTasks, listErr := verifyStore.ListLearningCycleTasks(
		ctx,
		schedule.TenantID,
		schedule.ScheduleID,
	)
	closeErr := verifyStore.Close()
	if listErr != nil || closeErr != nil || len(retryTasks) != 1 ||
		retryTasks[0].RunID != first.Task.RunID ||
		retryTasks[0].AttemptID != first.Task.AttemptID {
		t.Fatalf(
			"live retry changed task identity/cardinality=%d list=%v close=%v",
			len(retryTasks),
			listErr,
			closeErr,
		)
	}
	assertW4L4LiveCycleCardinality(t, databasePath)

	state := first.Task.State
	runID := first.Task.RunID
	attemptID := first.Task.AttemptID
	usageStatus := dispatch.Usage.ReconciliationStatus
	inputTokens := valueOrZero(dispatch.Usage.Tokens.Input)
	cachedInputTokens := valueOrZero(dispatch.Usage.Tokens.CachedInput)
	uncachedInputTokens := valueOrZero(dispatch.Usage.Tokens.UncachedInput)
	outputTokens := valueOrZero(dispatch.Usage.Tokens.Output)
	reasoningTokens := valueOrUnknownUint64(dispatch.Usage.Tokens.Reasoning)
	estimatedCost := valueOrUnknown(dispatch.Usage.EstimatedCost)
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("remove W4-L4 live temporary assets: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("W4-L4 live temporary root still exists: %v", err)
	}

	t.Logf(
		"W4_L4_LIVE_RESULT state=%s run_id=%s attempt_id=%s usage_status=%s input_tokens=%d cached_input_tokens=%d uncached_input_tokens=%d output_tokens=%d reasoning_tokens=%s estimated_cost=%s exact_retry_no_replay=true",
		state,
		runID,
		attemptID,
		usageStatus,
		inputTokens,
		cachedInputTokens,
		uncachedInputTokens,
		outputTokens,
		reasoningTokens,
		estimatedCost,
	)
}

func assertW4L4LiveCycleCardinality(t *testing.T, databasePath string) {
	t.Helper()
	database := openLongChainReadOnly(t, databasePath)
	defer database.Close()
	var (
		tasks            int
		runs             int
		attempts         int
		distinctRuns     int
		distinctAttempts int
		usage            int
	)
	if err := database.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM learning_cycle_tasks),
			(SELECT COUNT(*) FROM runs),
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(DISTINCT run_id) FROM model_dispatch_attempts),
			(SELECT COUNT(DISTINCT attempt_id) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM model_usage)
	`).Scan(
		&tasks,
		&runs,
		&attempts,
		&distinctRuns,
		&distinctAttempts,
		&usage,
	); err != nil {
		t.Fatalf("read W4-L4 live cardinality: %v", err)
	}
	if tasks != 1 || runs != 1 || attempts != 1 || distinctRuns != 1 ||
		distinctAttempts != 1 || usage != 1 {
		t.Fatalf(
			"W4-L4 live cardinality=(tasks=%d runs=%d attempts=%d distinct_runs=%d distinct_attempts=%d usage=%d)",
			tasks,
			runs,
			attempts,
			distinctRuns,
			distinctAttempts,
			usage,
		)
	}
}

func learningCycleCommandTaskState(task *learningCycleTaskCommandResult) string {
	if task == nil {
		return ""
	}
	return task.State
}

func valueOrZero(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

func valueOrUnknown(value *string) string {
	if value == nil {
		return "UNKNOWN"
	}
	return *value
}

func valueOrUnknownUint64(value *uint64) string {
	if value == nil {
		return "UNKNOWN"
	}
	return fmt.Sprintf("%d", *value)
}
