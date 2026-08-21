package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestReadLearningCycleScheduleInputIsBoundedStrictOrdinaryJSON(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	firstDue := time.Date(2099, time.January, 2, 3, 4, 5, 678_000, time.UTC)
	schedule := learningCycleCLITestSchedule("cycle-input", firstDue)
	schedule.IntervalSeconds = 0
	inputPath := writeLearningCycleScheduleInput(t, root, "schedule.json", schedule)

	got, err := readLearningCycleScheduleInput(inputPath)
	if err != nil {
		t.Fatalf("read ordinary Schedule JSON: %v", err)
	}
	if got.IntervalSeconds != learningcontract.DefaultLearningCycleIntervalSecondsV1 ||
		got.FirstDueAtUnixMicros != firstDue.UnixMicro() ||
		got.ScheduleID != schedule.ScheduleID {
		t.Fatalf("restored Schedule=%+v", got)
	}

	tests := []struct {
		name    string
		payload []byte
	}{
		{
			name: "unknown field",
			payload: []byte(`{
                "schema_version":"learning-cycle-schedule/v1",
                "tenant_id":"default",
                "schedule_id":"cycle-unknown",
                "service_principal_id":"local-operator",
                "workspace_id":"local-chat",
                "agent_id":"assistant",
                "profile_id":"pure-chat",
                "kind":"SKILL",
                "target_module_id":"freeagent.test.skill",
                "objective":"bounded",
                "knowledge_visibility":[],
                "first_due_at_unix_micros":4070999045000678,
                "interval_seconds":86400,
                "max_output_tokens":256,
                "unexpected":true
            }`),
		},
		{
			name: "duplicate field",
			payload: []byte(`{
                "schema_version":"learning-cycle-schedule/v1",
                "tenant_id":"default",
                "tenant_id":"default",
                "schedule_id":"cycle-duplicate"
            }`),
		},
		{
			name:    "multiple JSON values",
			payload: append(mustMarshalLearningCycleSchedule(t, schedule), []byte(` {}`)...),
		},
		{
			name:    "over size limit",
			payload: bytes.Repeat([]byte(" "), maximumLearningCycleScheduleInputBytes+1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-")+".json")
			if err := os.WriteFile(path, test.payload, 0o600); err != nil {
				t.Fatalf("write invalid Schedule: %v", err)
			}
			if _, err := readLearningCycleScheduleInput(path); err == nil {
				t.Fatal("invalid Schedule JSON was accepted")
			}
		})
	}
}

func TestLearningCycleCLIStoreAndLocalTickLongChain(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if err := run(ctx, []string{
		"init",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--seed", exampleSeedPath(t),
	}, io.Discard, io.Discard); err != nil {
		t.Fatalf("initialize Learning cycle CLI fixture: %v", err)
	}
	_, cycleConfigCanonical, err := moduleapi.NewModelBindingConfigV1(
		moduleapi.ModelBindingConfigV1{
			SchemaVersion:   moduleapi.ModelBindingConfigSchemaV1,
			Provider:        "freeagent.local",
			Model:           "freeagent-dev-echo",
			ModelBuildID:    localEchoModuleID + "/" + localEchoModuleVersion,
			BillingVersion:  "local-v1",
			PriceSnapshotID: "price-local-echo-v1",
			Parameters:      json.RawMessage(`{"max_tokens":256}`),
		},
	)
	if err != nil {
		t.Fatalf("freeze local cycle model Config: %v", err)
	}
	profileStore, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Store to publish cycle Profile: %v", err)
	}
	cycleProfileID := publishW4LearningCycleModelOnlyProfile(
		t,
		ctx,
		profileStore,
		defaultProfileID,
		"cycle-cli-model-only",
		cycleConfigCanonical,
	)
	if err := profileStore.Close(); err != nil {
		t.Fatalf("close Store after cycle Profile publication: %v", err)
	}

	firstDue := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	schedule := learningCycleCLITestSchedule("cycle-cli-long", firstDue)
	schedule.ProfileID = cycleProfileID
	schedule.IntervalSeconds = 0
	inputPath := writeLearningCycleScheduleInput(
		t,
		root,
		"cycle-cli-long.json",
		schedule,
	)

	createArgs := []string{
		"learning-cycle-schedule-create",
		"--db", databasePath,
		"--input", inputPath,
	}
	created := runLearningCycleScheduleCommand(t, ctx, createArgs)
	if !created.Created || created.Applied || created.Enabled || created.Revision != 0 ||
		created.Schedule.IntervalSeconds != learningcontract.DefaultLearningCycleIntervalSecondsV1 ||
		created.LastScheduledFor != nil || created.NextDueAt != firstDue.Format(time.RFC3339Nano) ||
		created.ScheduleDigest == "" {
		t.Fatalf("created Schedule=%+v", created)
	}
	retry := runLearningCycleScheduleCommand(t, ctx, createArgs)
	if retry.Created || retry.Applied || retry.ScheduleDigest != created.ScheduleDigest ||
		retry.Revision != created.Revision {
		t.Fatalf("exact create retry=%+v want digest=%s revision=%d", retry, created.ScheduleDigest, created.Revision)
	}

	got := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-get",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
	})
	if got.Created || got.Applied || !reflect.DeepEqual(got.Schedule, retry.Schedule) ||
		got.ScheduleDigest != created.ScheduleDigest {
		t.Fatalf("get Schedule=%+v", got)
	}

	if err := run(ctx, []string{
		"learning-cycle-schedule-enable",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
	}, io.Discard, io.Discard); err == nil {
		t.Fatal("enable accepted an omitted expected revision")
	}
	enabled := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-enable",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--expected-revision", "0",
	})
	if !enabled.Enabled || !enabled.Applied || enabled.Revision != 1 {
		t.Fatalf("enabled Schedule=%+v", enabled)
	}
	exactEnable := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-enable",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--expected-revision", "1",
	})
	if !exactEnable.Enabled || exactEnable.Applied || exactEnable.Revision != 1 {
		t.Fatalf("same-state enable=%+v", exactEnable)
	}
	if err := run(ctx, []string{
		"learning-cycle-schedule-disable",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--expected-revision", "0",
	}, io.Discard, io.Discard); err == nil {
		t.Fatal("disable accepted a stale expected revision")
	}

	tickArgs := []string{
		"learning-cycle-tick",
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--observed-at", firstDue.Format(time.RFC3339Nano),
	}
	var tickOutput bytes.Buffer
	if err := run(ctx, tickArgs, &tickOutput, io.Discard); err != nil {
		t.Fatalf("run explicit Learning cycle Tick: %v", err)
	}
	var tick learningCycleTickCommandResult
	if err := json.Unmarshal(tickOutput.Bytes(), &tick); err != nil {
		t.Fatalf("decode Tick: %v\n%s", err, tickOutput.String())
	}
	if !tick.Due || !tick.TaskCreated || !tick.AdmissionCreated || !tick.FinalizeApplied ||
		tick.ScheduleRevision != 2 ||
		tick.Task == nil || tick.Task.State != string(learningcontract.LearningCycleTaskInvalidResultV1) ||
		tick.Task.RunID == "" || tick.Task.AttemptID == "" || tick.Task.ResultRef == "" ||
		tick.Loop == nil || tick.Loop.RunID != tick.Task.RunID ||
		tick.Usage == nil || tick.Usage.Status != "PROVIDER_REPORTED" {
		t.Fatalf("Tick result=%+v output=%s", tick, tickOutput.String())
	}
	for _, forbidden := range []string{
		schedule.Objective,
		learningcontract.LearningCycleInstructionsV1,
		`"output_schema_version"`,
		`"knowledge_chunks"`,
		`"skill_text"`,
	} {
		if bytes.Contains(tickOutput.Bytes(), []byte(forbidden)) {
			t.Fatalf("Tick output leaked model/request body fragment %q: %s", forbidden, tickOutput.String())
		}
	}

	var secondOutput bytes.Buffer
	if err := run(ctx, tickArgs, &secondOutput, io.Discard); err != nil {
		t.Fatalf("repeat same-window Tick: %v", err)
	}
	var second learningCycleTickCommandResult
	if err := json.Unmarshal(secondOutput.Bytes(), &second); err != nil {
		t.Fatalf("decode repeated Tick: %v\n%s", err, secondOutput.String())
	}
	if second.Due || second.TaskCreated || second.AdmissionCreated ||
		second.FinalizeApplied || second.Task != nil || second.Loop != nil || second.Usage != nil {
		t.Fatalf("same-window Tick replayed work: %+v", second)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("reopen Store after Tick: %v", err)
	}
	tasks, listErr := store.ListLearningCycleTasks(ctx, schedule.TenantID, schedule.ScheduleID)
	if listErr != nil || len(tasks) != 1 || tasks[0].RunID != tick.Task.RunID ||
		tasks[0].AttemptID != tick.Task.AttemptID {
		_ = store.Close()
		t.Fatalf("stored tasks=%+v error=%v", tasks, listErr)
	}
	if _, err := store.GetModelDispatchRecord(ctx, tick.Task.AttemptID); err != nil {
		_ = store.Close()
		t.Fatalf("read sole model Attempt: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close inspected Store: %v", err)
	}

	effectsBeforeOperations := learningCycleCLIEffectCounts(t, databasePath)
	if effectsBeforeOperations.Runs != 1 || effectsBeforeOperations.ModelAttempts != 1 ||
		effectsBeforeOperations.DispatchAttempts != 0 {
		t.Fatalf("terminal Learning cycle effects before Store-only commands=%+v", effectsBeforeOperations)
	}
	report := runLearningCycleReportCommand(t, ctx, []string{
		"learning-cycle-report",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--window-start", firstDue.Format(time.RFC3339Nano),
		"--window-end", firstDue.Add(time.Microsecond).Format(time.RFC3339Nano),
	})
	if len(report.Report.Entries) != 1 ||
		report.Report.Entries[0].TaskID != tick.Task.TaskID ||
		report.Report.Entries[0].RunID != tick.Task.RunID ||
		report.Report.Entries[0].State != learningcontract.LearningCycleTaskInvalidResultV1 ||
		report.ReportDigest == "" {
		t.Fatalf("terminal Learning cycle report=%+v", report)
	}
	reconciled := runLearningCycleReconcileCommand(t, ctx, []string{
		"learning-cycle-reconcile",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
	})
	if reconciled.ScheduleDigest != tick.ScheduleDigest ||
		reconciled.ScheduleRevision != tick.ScheduleRevision ||
		len(reconciled.Entries) != 0 {
		t.Fatalf("terminal Learning cycle reconciliation=%+v", reconciled)
	}
	if effectsAfterOperations := learningCycleCLIEffectCounts(t, databasePath); effectsAfterOperations != effectsBeforeOperations {
		t.Fatalf(
			"terminal report/reconcile changed Run or Attempt facts: before=%+v after=%+v",
			effectsBeforeOperations,
			effectsAfterOperations,
		)
	}

	disabled := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-disable",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--expected-revision", strconv.FormatUint(tick.ScheduleRevision, 10),
	})
	if disabled.Enabled || !disabled.Applied || disabled.Revision != 3 {
		t.Fatalf("disabled Schedule=%+v", disabled)
	}
}

func TestLearningCycleScheduleCLICommandsAreStoreOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "store-only.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatalf("initialize Store-only Learning fixture: %v", err)
	}
	firstDue := time.Date(2099, time.February, 3, 4, 5, 6, 0, time.UTC)
	schedule := learningCycleCLITestSchedule("cycle-store-only", firstDue)
	inputPath := writeLearningCycleScheduleInput(
		t,
		root,
		"cycle-store-only.json",
		schedule,
	)
	created := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-create",
		"--db", databasePath,
		"--input", inputPath,
	})
	if !created.Created || created.Enabled || created.Revision != 0 {
		t.Fatalf("Store-only create=%+v", created)
	}
	got := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-get",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
	})
	if got.ScheduleDigest != created.ScheduleDigest || got.Enabled || got.Revision != 0 {
		t.Fatalf("Store-only get=%+v", got)
	}
	enabled := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-enable",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--expected-revision", "0",
	})
	if !enabled.Enabled || !enabled.Applied || enabled.Revision != 1 {
		t.Fatalf("Store-only enable=%+v", enabled)
	}
	disabled := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-disable",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--expected-revision", "1",
	})
	if disabled.Enabled || !disabled.Applied || disabled.Revision != 2 {
		t.Fatalf("Store-only disable=%+v", disabled)
	}
	if _, err := os.Stat(databasePath + ".artifacts"); !os.IsNotExist(err) {
		t.Fatalf("Store-only commands touched default artifact root: %v", err)
	}
}

func TestLearningCycleReconcileAndReportCLICommandsAreStoreOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "cycle-operations-store-only.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatalf("initialize Store-only cycle operations fixture: %v", err)
	}
	firstDue := time.Date(2099, time.March, 4, 5, 6, 7, 8_000, time.UTC)
	schedule := learningCycleCLITestSchedule("cycle-operations-store-only", firstDue)
	inputPath := writeLearningCycleScheduleInput(t, root, "cycle-operations.json", schedule)
	created := runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-create",
		"--db", databasePath,
		"--input", inputPath,
	})
	runLearningCycleScheduleCommand(t, ctx, []string{
		"learning-cycle-schedule-enable",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--expected-revision", "0",
	})
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open Store-only cycle operations fixture: %v", err)
	}
	ensured, err := store.EnsureDueTask(ctx, schedule.TenantID, schedule.ScheduleID, firstDue)
	if err != nil || !ensured.Due || !ensured.Created ||
		ensured.Task.State != learningcontract.LearningCycleTaskPendingV1 {
		_ = store.Close()
		t.Fatalf("ensure pending Store-only cycle Task=%+v error=%v", ensured, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close Store-only cycle operations fixture: %v", err)
	}

	before := learningCycleCLIEffectCounts(t, databasePath)
	if before != (learningCycleCLIEffects{}) {
		t.Fatalf("pending Store-only fixture already has execution/effect facts: %+v", before)
	}
	var reconcileOutput bytes.Buffer
	if err := run(ctx, []string{
		"learning-cycle-reconcile",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
	}, &reconcileOutput, io.Discard); err != nil {
		t.Fatalf("run Store-only Learning cycle reconciliation: %v", err)
	}
	var reconciled learningCycleReconciliationCommandResult
	if err := json.Unmarshal(reconcileOutput.Bytes(), &reconciled); err != nil {
		t.Fatalf("decode Store-only reconciliation: %v\n%s", err, reconcileOutput.String())
	}
	if reconciled.TenantID != schedule.TenantID ||
		reconciled.ScheduleID != schedule.ScheduleID ||
		reconciled.ScheduleDigest != created.ScheduleDigest ||
		len(reconciled.Entries) != 1 ||
		reconciled.Entries[0].Status != string(currentstore.LearningCycleReconciliationPendingUnadmitted) ||
		reconciled.Entries[0].Task.TaskID != ensured.Task.TaskID ||
		reconciled.Entries[0].Task.State != string(learningcontract.LearningCycleTaskPendingV1) ||
		reconciled.Entries[0].Usage != nil {
		t.Fatalf("Store-only reconciliation=%+v", reconciled)
	}
	if !bytes.Contains(reconcileOutput.Bytes(), []byte(`"usage":null`)) {
		t.Fatalf("pending reconciliation did not explicitly encode null Usage: %s", reconcileOutput.String())
	}
	for _, forbidden := range []string{
		schedule.Objective,
		learningcontract.LearningCycleInstructionsV1,
		`"request"`,
		`"result"`,
	} {
		if bytes.Contains(reconcileOutput.Bytes(), []byte(forbidden)) {
			t.Fatalf("Store-only reconciliation leaked private body fragment %q: %s", forbidden, reconcileOutput.String())
		}
	}

	reportArgs := []string{
		"learning-cycle-report",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--window-start", firstDue.Format(time.RFC3339Nano),
		"--window-end", firstDue.Add(time.Microsecond).Format(time.RFC3339Nano),
	}
	var firstReportOutput bytes.Buffer
	if err := run(ctx, reportArgs, &firstReportOutput, io.Discard); err != nil {
		t.Fatalf("run Store-only Learning cycle report: %v", err)
	}
	var report learningCycleReportCommandResult
	if err := json.Unmarshal(firstReportOutput.Bytes(), &report); err != nil {
		t.Fatalf("decode Store-only report: %v\n%s", err, firstReportOutput.String())
	}
	if len(report.Report.Entries) != 1 ||
		report.Report.Entries[0].ScheduledForMicros != firstDue.UnixMicro() ||
		report.Report.Entries[0].TaskID != ensured.Task.TaskID ||
		report.Report.Entries[0].State != learningcontract.LearningCycleTaskPendingV1 ||
		report.ReportDigest == "" {
		t.Fatalf("Store-only report=%+v", report)
	}
	_, _, rebuiltDigest, err := learningcontract.NewLearningCycleReportV1(report.Report)
	if err != nil || rebuiltDigest != report.ReportDigest {
		t.Fatalf("rebuild Store-only report digest=%q want=%q error=%v", rebuiltDigest, report.ReportDigest, err)
	}
	var repeatedReportOutput bytes.Buffer
	if err := run(ctx, reportArgs, &repeatedReportOutput, io.Discard); err != nil {
		t.Fatalf("repeat Store-only Learning cycle report: %v", err)
	}
	if !bytes.Equal(firstReportOutput.Bytes(), repeatedReportOutput.Bytes()) {
		t.Fatalf("deterministic report bytes drifted:\nfirst=%s\nsecond=%s", firstReportOutput.String(), repeatedReportOutput.String())
	}
	left := runLearningCycleReportCommand(t, ctx, []string{
		"learning-cycle-report",
		"--db", databasePath,
		"--tenant", schedule.TenantID,
		"--schedule", schedule.ScheduleID,
		"--window-start", firstDue.Add(-time.Microsecond).Format(time.RFC3339Nano),
		"--window-end", firstDue.Format(time.RFC3339Nano),
	})
	if len(left.Report.Entries) != 0 {
		t.Fatalf("half-open report included its exclusive end boundary: %+v", left)
	}
	if after := learningCycleCLIEffectCounts(t, databasePath); after != before {
		t.Fatalf("Store-only reconcile/report created execution/effect facts: before=%+v after=%+v", before, after)
	}
	if _, err := os.Stat(databasePath + ".artifacts"); !os.IsNotExist(err) {
		t.Fatalf("Store-only reconcile/report touched default artifact root: %v", err)
	}
}

func TestLearningCycleCLIRequiresExplicitUTCObservation(t *testing.T) {
	t.Parallel()
	valid, err := parseRequiredLearningCycleObservedAt("2099-01-02T03:04:05.000006Z")
	if err != nil || valid.Location() != time.UTC || valid.Nanosecond() != 6_000 {
		t.Fatalf("valid UTC observation=%s error=%v", valid, err)
	}
	for _, invalid := range []string{
		"",
		"2099-01-02T11:04:05+08:00",
		"2099-01-02T03:04:05.0000001Z",
		" 2099-01-02T03:04:05Z",
	} {
		if _, err := parseRequiredLearningCycleObservedAt(invalid); err == nil {
			t.Fatalf("invalid observation %q was accepted", invalid)
		}
	}
}

func TestLearningCycleReconcileReportCLIRequireStrictScopeAndUTCWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if parsed, err := parseRequiredLearningCycleUTC(
		"--window-start",
		"1970-01-01T00:00:00Z",
		true,
	); err != nil || parsed.UnixMicro() != 0 {
		t.Fatalf("valid epoch report start=%s error=%v", parsed, err)
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{
			name: "reconcile missing exact scope",
			args: []string{"learning-cycle-reconcile", "--db", "unused.sqlite", "--tenant", "default"},
		},
		{
			name: "reconcile positional trailer",
			args: []string{
				"learning-cycle-reconcile", "--db", "unused.sqlite", "--tenant", "default",
				"--schedule", "cycle", "unexpected",
			},
		},
		{
			name: "report missing end",
			args: []string{
				"learning-cycle-report", "--db", "unused.sqlite", "--tenant", "default",
				"--schedule", "cycle", "--window-start", "2099-01-01T00:00:00Z",
			},
		},
		{
			name: "report non UTC start",
			args: []string{
				"learning-cycle-report", "--db", "unused.sqlite", "--tenant", "default",
				"--schedule", "cycle", "--window-start", "2099-01-01T08:00:00+08:00",
				"--window-end", "2099-01-02T00:00:00Z",
			},
		},
		{
			name: "report sub microsecond start",
			args: []string{
				"learning-cycle-report", "--db", "unused.sqlite", "--tenant", "default",
				"--schedule", "cycle", "--window-start", "2099-01-01T00:00:00.0000001Z",
				"--window-end", "2099-01-02T00:00:00Z",
			},
		},
		{
			name: "report reversed window",
			args: []string{
				"learning-cycle-report", "--db", "unused.sqlite", "--tenant", "default",
				"--schedule", "cycle", "--window-start", "2099-01-02T00:00:00Z",
				"--window-end", "2099-01-01T00:00:00Z",
			},
		},
		{
			name: "report positional trailer",
			args: []string{
				"learning-cycle-report", "--db", "unused.sqlite", "--tenant", "default",
				"--schedule", "cycle", "--window-start", "2099-01-01T00:00:00Z",
				"--window-end", "2099-01-02T00:00:00Z", "unexpected",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := run(ctx, test.args, io.Discard, io.Discard); err == nil {
				t.Fatalf("invalid CLI arguments were accepted: %v", test.args)
			}
		})
	}
	if _, err := parseRequiredLearningCycleUTC(
		"--window-end",
		"1970-01-01T00:00:00Z",
		false,
	); err == nil {
		t.Fatal("exclusive report end accepted the zero epoch")
	}
}

func runLearningCycleScheduleCommand(
	t *testing.T,
	ctx context.Context,
	args []string,
) learningCycleScheduleCommandResult {
	t.Helper()
	var output bytes.Buffer
	if err := run(ctx, args, &output, io.Discard); err != nil {
		t.Fatalf("run %s: %v", args[0], err)
	}
	var result learningCycleScheduleCommandResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode %s: %v\n%s", args[0], err, output.String())
	}
	return result
}

func runLearningCycleReconcileCommand(
	t *testing.T,
	ctx context.Context,
	args []string,
) learningCycleReconciliationCommandResult {
	t.Helper()
	var output bytes.Buffer
	if err := run(ctx, args, &output, io.Discard); err != nil {
		t.Fatalf("run %s: %v", args[0], err)
	}
	var result learningCycleReconciliationCommandResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode %s: %v\n%s", args[0], err, output.String())
	}
	return result
}

func runLearningCycleReportCommand(
	t *testing.T,
	ctx context.Context,
	args []string,
) learningCycleReportCommandResult {
	t.Helper()
	var output bytes.Buffer
	if err := run(ctx, args, &output, io.Discard); err != nil {
		t.Fatalf("run %s: %v", args[0], err)
	}
	var result learningCycleReportCommandResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode %s: %v\n%s", args[0], err, output.String())
	}
	return result
}

type learningCycleCLIEffects struct {
	Runs             int
	ModelAttempts    int
	DispatchAttempts int
	Proposals        int
}

func learningCycleCLIEffectCounts(t *testing.T, databasePath string) learningCycleCLIEffects {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open Learning cycle CLI effect counts: %v", err)
	}
	defer database.Close()
	result := learningCycleCLIEffects{}
	for _, item := range []struct {
		table       string
		destination *int
	}{
		{table: "runs", destination: &result.Runs},
		{table: "model_dispatch_attempts", destination: &result.ModelAttempts},
		{table: "dispatch_attempts", destination: &result.DispatchAttempts},
		{table: "learning_proposals", destination: &result.Proposals},
	} {
		if err := database.QueryRow("SELECT COUNT(*) FROM " + item.table).Scan(item.destination); err != nil {
			t.Fatalf("count Learning cycle CLI %s: %v", item.table, err)
		}
	}
	return result
}

func learningCycleCLITestSchedule(
	scheduleID string,
	firstDue time.Time,
) learningcontract.LearningCycleScheduleV1 {
	return learningcontract.LearningCycleScheduleV1{
		SchemaVersion:        learningcontract.LearningCycleScheduleSchemaVersionV1,
		TenantID:             defaultTenantID,
		ScheduleID:           scheduleID,
		ServicePrincipalID:   defaultPrincipalID,
		WorkspaceID:          defaultWorkspaceID,
		AgentID:              defaultAgentID,
		ProfileID:            defaultProfileID,
		Kind:                 learningcontract.ProposalKindSkillV1,
		TargetModuleID:       "freeagent.test." + scheduleID,
		Objective:            "cli-private-objective-" + scheduleID,
		KnowledgeVisibility:  nil,
		FirstDueAtUnixMicros: firstDue.UnixMicro(),
		IntervalSeconds:      learningcontract.DefaultLearningCycleIntervalSecondsV1,
		MaxOutputTokens:      256,
	}
}

func writeLearningCycleScheduleInput(
	t *testing.T,
	root string,
	name string,
	schedule learningcontract.LearningCycleScheduleV1,
) string {
	t.Helper()
	payload, err := json.MarshalIndent(schedule, "", "  ")
	if err != nil {
		t.Fatalf("marshal Schedule input: %v", err)
	}
	payload = append(payload, '\n')
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write Schedule input: %v", err)
	}
	return path
}

func mustMarshalLearningCycleSchedule(
	t *testing.T,
	schedule learningcontract.LearningCycleScheduleV1,
) []byte {
	t.Helper()
	payload, err := json.Marshal(schedule)
	if err != nil {
		t.Fatalf("marshal Schedule: %v", err)
	}
	return payload
}

// publishW4LearningCycleModelOnlyProfile publishes an independent Profile
// whose sole Port is model.generate/v1. An optional exact Config replaces the
// source model Binding Config so the live test can freeze JSON-only output.
// The helper never constructs a Provider or reads a credential.
func publishW4LearningCycleModelOnlyProfile(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	baseProfileID string,
	profileID string,
	modelConfigCanonical []byte,
) string {
	t.Helper()
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatalf("load basis for Learning cycle Profile: %v", err)
	}
	var source *controlcontract.ProfileDefinition
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID == baseProfileID {
			copy := control.Profiles[index]
			source = &copy
			break
		}
	}
	if source == nil {
		t.Fatalf("base Learning cycle Profile %q not found", baseProfileID)
	}
	modelBindings := make([]controlcontract.BindingSpec, 0, 1)
	for _, binding := range source.Bindings {
		if binding.Port == productionModelPort {
			binding.StaticContextRefs = append([]string(nil), binding.StaticContextRefs...)
			modelBindings = append(modelBindings, binding)
		}
	}
	if len(modelBindings) != 1 {
		t.Fatalf("base Profile %q model bindings=%+v", baseProfileID, modelBindings)
	}
	if len(modelConfigCanonical) != 0 {
		modelBindings[0].ConfigRef = putW4LearningCycleTestContent(
			t,
			ctx,
			store,
			currentstore.ContentConfig,
			modelConfigCanonical,
		)
	}
	profile := *source
	profile.Profile = corecontract.ProfileRef{
		ID: profileID, Version: "v1", Digest: strings.Repeat("7", 64),
	}
	profile.Bindings = modelBindings
	control.SnapshotID = "control-" + profileID
	control.Revision++
	control.Digest = ""
	control.Profiles = append(control.Profiles, profile)
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze Learning cycle Control: %v", err)
	}
	catalog.GenerationID = "catalog-" + profileID
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze Learning cycle Catalog: %v", err)
	}
	if _, err := store.PublishControlCatalog(ctx, currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: basis.PointerRevision,
		NewPointerRevision:      basis.PointerRevision + 1,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	}); err != nil {
		t.Fatalf("publish Learning cycle Profile: %v", err)
	}
	return profileID
}

func putW4LearningCycleTestContent(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	body []byte,
) string {
	t.Helper()
	canonical, err := moduleapi.CanonicalJSON(body)
	if err != nil {
		t.Fatalf("canonicalize Learning cycle %s: %v", kind, err)
	}
	const mediaType = "application/json"
	digest, err := currentstore.ComputeContentDigest(kind, mediaType, canonical)
	if err != nil {
		t.Fatalf("digest Learning cycle %s: %v", kind, err)
	}
	if _, err := store.PutContent(ctx, currentstore.ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      mediaType,
		CanonicalBytes: canonical,
	}); err != nil {
		t.Fatalf("put Learning cycle %s: %v", kind, err)
	}
	return digest
}
