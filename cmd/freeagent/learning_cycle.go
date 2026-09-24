package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const maximumLearningCycleScheduleInputBytes = learningcontract.MaxLearningCycleScheduleWireBytesV1

type learningCycleScheduleCommandResult struct {
	Schedule         learningcontract.LearningCycleScheduleV1 `json:"schedule"`
	ScheduleDigest   string                                   `json:"schedule_digest"`
	Enabled          bool                                     `json:"enabled"`
	Revision         uint64                                   `json:"revision"`
	LastScheduledFor *string                                  `json:"last_scheduled_for"`
	NextDueAt        string                                   `json:"next_due_at"`
	CreatedAt        string                                   `json:"created_at"`
	UpdatedAt        string                                   `json:"updated_at"`
	Created          bool                                     `json:"created"`
	Applied          bool                                     `json:"applied"`
}

type learningCycleTaskCommandResult struct {
	TaskID            string `json:"task_id"`
	ScheduledFor      string `json:"scheduled_for"`
	RequestDigest     string `json:"request_digest"`
	State             string `json:"state"`
	Revision          uint64 `json:"revision"`
	RunID             string `json:"run_id,omitempty"`
	RunManifestDigest string `json:"run_manifest_digest,omitempty"`
	AttemptID         string `json:"attempt_id,omitempty"`
	ResultRef         string `json:"result_ref,omitempty"`
	ProposalID        string `json:"proposal_id,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

type learningCycleLoopCommandResult struct {
	RunID       string `json:"run_id"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
}

type learningCycleTickCommandResult struct {
	TenantID         string                          `json:"tenant_id"`
	ScheduleID       string                          `json:"schedule_id"`
	ScheduleDigest   string                          `json:"schedule_digest"`
	ScheduleRevision uint64                          `json:"schedule_revision"`
	ObservedAt       string                          `json:"observed_at"`
	Due              bool                            `json:"due"`
	TaskCreated      bool                            `json:"task_created"`
	AdmissionCreated bool                            `json:"admission_created"`
	FinalizeApplied  bool                            `json:"finalize_applied"`
	Task             *learningCycleTaskCommandResult `json:"task,omitempty"`
	Loop             *learningCycleLoopCommandResult `json:"loop,omitempty"`
	Usage            *chatCommandUsage               `json:"usage,omitempty"`
}

type learningCycleReconciliationCommandEntry struct {
	Status string                         `json:"status"`
	Task   learningCycleTaskCommandResult `json:"task"`
	Usage  *chatCommandUsage              `json:"usage"`
}

type learningCycleReconciliationCommandResult struct {
	TenantID         string                                    `json:"tenant_id"`
	ScheduleID       string                                    `json:"schedule_id"`
	ScheduleDigest   string                                    `json:"schedule_digest"`
	ScheduleRevision uint64                                    `json:"schedule_revision"`
	Entries          []learningCycleReconciliationCommandEntry `json:"entries"`
}

type learningCycleReportCommandResult struct {
	Report       learningcontract.LearningCycleReportV1 `json:"report"`
	ReportDigest string                                 `json:"report_digest"`
}

func runLearningCycleScheduleCreate(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) (returnErr error) {
	flags := newFlagSet("learning-cycle-schedule-create", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	inputPath := flags.String("input", "", "bounded Learning cycle Schedule JSON file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*inputPath) == "" {
		return errors.New(
			"freeagent learning-cycle-schedule-create: --db and --input are required",
		)
	}
	schedule, err := readLearningCycleScheduleInput(*inputPath)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-schedule-create: %w", err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, *databasePath)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-schedule-create: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	created, err := store.CreateLearningCycleSchedule(ctx, schedule)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-schedule-create: %w", err)
	}
	return writeCommandJSON(
		stdout,
		newLearningCycleScheduleCommandResult(created.Record, created.Created, false),
	)
}

func runLearningCycleScheduleGet(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) (returnErr error) {
	flags := newFlagSet("learning-cycle-schedule-get", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	tenantID := flags.String("tenant", "", "exact tenant identity")
	scheduleID := flags.String("schedule", "", "exact Learning cycle Schedule identity")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*scheduleID) == "" {
		return errors.New(
			"freeagent learning-cycle-schedule-get: --db, --tenant, and --schedule are required",
		)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, *databasePath)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-schedule-get: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	record, err := store.GetLearningCycleSchedule(ctx, *tenantID, *scheduleID)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-schedule-get: %w", err)
	}
	return writeCommandJSON(
		stdout,
		newLearningCycleScheduleCommandResult(record, false, false),
	)
}

func runLearningCycleScheduleEnable(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runLearningCycleScheduleEnablement(
		ctx,
		"learning-cycle-schedule-enable",
		args,
		stdout,
		stderr,
		true,
	)
}

func runLearningCycleScheduleDisable(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runLearningCycleScheduleEnablement(
		ctx,
		"learning-cycle-schedule-disable",
		args,
		stdout,
		stderr,
		false,
	)
}

func runLearningCycleScheduleEnablement(
	ctx context.Context,
	command string,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	enabled bool,
) (returnErr error) {
	flags := newFlagSet(command, stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	tenantID := flags.String("tenant", "", "exact tenant identity")
	scheduleID := flags.String("schedule", "", "exact Learning cycle Schedule identity")
	expectedRevision := flags.Uint64(
		"expected-revision",
		0,
		"exact Schedule revision observed before this policy change",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*scheduleID) == "" {
		return fmt.Errorf(
			"freeagent %s: --db, --tenant, --schedule, and --expected-revision are required",
			command,
		)
	}
	if !flagWasExplicitlySet(flags, "expected-revision") {
		return fmt.Errorf("freeagent %s: --expected-revision is required", command)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, *databasePath)
	if err != nil {
		return fmt.Errorf("freeagent %s: %w", command, err)
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	result, err := store.SetLearningCycleScheduleEnabled(
		ctx,
		currentstore.SetLearningCycleScheduleEnabledInput{
			TenantID:         *tenantID,
			ScheduleID:       *scheduleID,
			ExpectedRevision: *expectedRevision,
			Enabled:          enabled,
		},
	)
	if err != nil {
		return fmt.Errorf("freeagent %s: %w", command, err)
	}
	return writeCommandJSON(
		stdout,
		newLearningCycleScheduleCommandResult(result.Record, false, result.Applied),
	)
}

func runLearningCycleTick(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) (returnErr error) {
	flags := newFlagSet("learning-cycle-tick", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "content-addressed artifact root")
	tenantID := flags.String("tenant", "", "exact tenant identity")
	scheduleID := flags.String("schedule", "", "exact Learning cycle Schedule identity")
	observedAtText := flags.String(
		"observed-at",
		"",
		"explicit UTC RFC3339 observation used for this Tick",
	)
	deepSeekFlags := bindDeepSeekRuntimeFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*scheduleID) == "" ||
		strings.TrimSpace(*observedAtText) == "" {
		return errors.New(
			"freeagent learning-cycle-tick: --db, --tenant, --schedule, and --observed-at are required",
		)
	}
	observedAt, err := parseRequiredLearningCycleObservedAt(*observedAtText)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-tick: %w", err)
	}
	deepSeekConfig, err := deepSeekFlags.config(flags)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-tick: %w", err)
	}
	composition, err := openProductionCompositionWithOptions(
		ctx,
		*databasePath,
		resolvedArtifactRoot(*databasePath, *artifactRoot),
		*tenantID,
		productionCompositionOptions{DeepSeek: deepSeekConfig},
	)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-tick: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, composition.Close()) }()
	service, err := localchat.NewLearningCycleService(composition.chat)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-tick: %w", err)
	}
	ticked, err := service.Tick(ctx, localchat.LearningCycleTickInput{
		TenantID:   *tenantID,
		ScheduleID: *scheduleID,
		ObservedAt: observedAt,
	})
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-tick: %w", err)
	}
	result := newLearningCycleTickCommandResult(ticked, observedAt)
	if result.Task != nil && result.Task.AttemptID != "" {
		result.Usage, err = readLearningCycleAttemptUsage(
			ctx,
			composition.store,
			result.Task.RunID,
			result.Task.AttemptID,
		)
		if err != nil {
			return fmt.Errorf(
				"freeagent learning-cycle-tick: read authoritative Usage: %w",
				err,
			)
		}
	}
	return writeCommandJSON(stdout, result)
}

func runLearningCycleReconcile(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) (returnErr error) {
	flags := newFlagSet("learning-cycle-reconcile", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	tenantID := flags.String("tenant", "", "exact tenant identity")
	scheduleID := flags.String("schedule", "", "exact Learning cycle Schedule identity")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*scheduleID) == "" {
		return errors.New(
			"freeagent learning-cycle-reconcile: --db, --tenant, and --schedule are required",
		)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, *databasePath)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-reconcile: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	reconciled, err := store.ReconcileLearningCycleTasks(ctx, *tenantID, *scheduleID)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-reconcile: %w", err)
	}
	result := learningCycleReconciliationCommandResult{
		TenantID:         reconciled.Schedule.Schedule.TenantID,
		ScheduleID:       reconciled.Schedule.Schedule.ScheduleID,
		ScheduleDigest:   reconciled.Schedule.ScheduleDigest,
		ScheduleRevision: reconciled.Schedule.Revision,
		Entries: make(
			[]learningCycleReconciliationCommandEntry,
			0,
			len(reconciled.Entries),
		),
	}
	for _, entry := range reconciled.Entries {
		projected := learningCycleReconciliationCommandEntry{
			Status: string(entry.Status),
			Task:   newLearningCycleTaskCommandResult(entry.Task),
		}
		if entry.Task.AttemptID != "" {
			projected.Usage, err = readLearningCycleAttemptUsage(
				ctx,
				store,
				entry.Task.RunID,
				entry.Task.AttemptID,
			)
			if err != nil {
				return fmt.Errorf(
					"freeagent learning-cycle-reconcile: read authoritative Usage: %w",
					err,
				)
			}
		}
		result.Entries = append(result.Entries, projected)
	}
	return writeCommandJSON(stdout, result)
}

func runLearningCycleReport(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) (returnErr error) {
	flags := newFlagSet("learning-cycle-report", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	tenantID := flags.String("tenant", "", "exact tenant identity")
	scheduleID := flags.String("schedule", "", "exact Learning cycle Schedule identity")
	windowStartText := flags.String(
		"window-start",
		"",
		"inclusive UTC RFC3339 report window start",
	)
	windowEndText := flags.String(
		"window-end",
		"",
		"exclusive UTC RFC3339 report window end",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*scheduleID) == "" ||
		strings.TrimSpace(*windowStartText) == "" || strings.TrimSpace(*windowEndText) == "" {
		return errors.New(
			"freeagent learning-cycle-report: --db, --tenant, --schedule, --window-start, and --window-end are required",
		)
	}
	windowStart, err := parseRequiredLearningCycleUTC(
		"--window-start",
		*windowStartText,
		true,
	)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-report: %w", err)
	}
	windowEnd, err := parseRequiredLearningCycleUTC(
		"--window-end",
		*windowEndText,
		false,
	)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-report: %w", err)
	}
	if !windowEnd.After(windowStart) {
		return errors.New(
			"freeagent learning-cycle-report: --window-end must be after --window-start",
		)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, *databasePath)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-report: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	report, err := store.BuildLearningCycleReport(
		ctx,
		*tenantID,
		*scheduleID,
		windowStart,
		windowEnd,
	)
	if err != nil {
		return fmt.Errorf("freeagent learning-cycle-report: %w", err)
	}
	return writeCommandJSON(stdout, learningCycleReportCommandResult{
		Report:       report.Report,
		ReportDigest: report.ReportDigest,
	})
}

func newLearningCycleScheduleCommandResult(
	record currentstore.LearningCycleScheduleRecord,
	created bool,
	applied bool,
) learningCycleScheduleCommandResult {
	result := learningCycleScheduleCommandResult{
		Schedule:       record.Schedule,
		ScheduleDigest: record.ScheduleDigest,
		Enabled:        record.Enabled,
		Revision:       record.Revision,
		NextDueAt:      formatLearningCycleTime(record.NextDueAt),
		CreatedAt:      formatLearningCycleTime(record.CreatedAt),
		UpdatedAt:      formatLearningCycleTime(record.UpdatedAt),
		Created:        created,
		Applied:        applied,
	}
	if !record.LastScheduledFor.IsZero() {
		formatted := formatLearningCycleTime(record.LastScheduledFor)
		result.LastScheduledFor = &formatted
	}
	return result
}

func newLearningCycleTickCommandResult(
	result localchat.LearningCycleTickResult,
	observedAt time.Time,
) learningCycleTickCommandResult {
	command := learningCycleTickCommandResult{
		TenantID:         result.Schedule.Schedule.TenantID,
		ScheduleID:       result.Schedule.Schedule.ScheduleID,
		ScheduleDigest:   result.Schedule.ScheduleDigest,
		ScheduleRevision: result.Schedule.Revision,
		ObservedAt:       formatLearningCycleTime(observedAt),
		Due:              result.Due,
		TaskCreated:      result.TaskCreated,
		AdmissionCreated: result.AdmissionCreated,
		FinalizeApplied:  result.FinalizeApplied,
	}
	if result.Task.TaskID != "" {
		projected := newLearningCycleTaskCommandResult(result.Task)
		command.Task = &projected
	}
	if result.LoopResult != nil {
		command.Loop = &learningCycleLoopCommandResult{
			RunID:       result.LoopResult.RunID,
			Disposition: string(result.LoopResult.Disposition),
			Reason:      result.LoopResult.ReasonCode,
		}
	}
	return command
}

func newLearningCycleTaskCommandResult(
	record currentstore.LearningCycleTaskRecord,
) learningCycleTaskCommandResult {
	return learningCycleTaskCommandResult{
		TaskID:            record.TaskID,
		ScheduledFor:      formatLearningCycleTime(record.ScheduledFor),
		RequestDigest:     record.RequestDigest,
		State:             string(record.State),
		Revision:          record.Revision,
		RunID:             record.RunID,
		RunManifestDigest: record.RunManifestDigest,
		AttemptID:         record.AttemptID,
		ResultRef:         record.ResultRef,
		ProposalID:        record.ProposalID,
		CreatedAt:         formatLearningCycleTime(record.CreatedAt),
		UpdatedAt:         formatLearningCycleTime(record.UpdatedAt),
	}
}

func readLearningCycleAttemptUsage(
	ctx context.Context,
	store *currentstore.Store,
	runID string,
	attemptID string,
) (*chatCommandUsage, error) {
	if store == nil {
		return nil, errors.New("Current Store is unavailable")
	}
	record, err := store.GetModelDispatchRecord(ctx, attemptID)
	if err != nil {
		return nil, err
	}
	if record.Attempt.RunID != runID || record.Attempt.AttemptID != attemptID ||
		record.Usage.RunID != runID || record.Usage.AttemptID != attemptID {
		return nil, errors.New("Learning cycle model Attempt identity drift")
	}
	return &chatCommandUsage{
		InputTokens:         record.Usage.Tokens.Input,
		CachedInputTokens:   record.Usage.Tokens.CachedInput,
		UncachedInputTokens: record.Usage.Tokens.UncachedInput,
		OutputTokens:        record.Usage.Tokens.Output,
		ReasoningTokens:     record.Usage.Tokens.Reasoning,
		Status:              record.Usage.UsageStatus,
	}, nil
}

func readLearningCycleScheduleInput(
	path string,
) (learningcontract.LearningCycleScheduleV1, error) {
	payload, err := readBoundedLearningCycleInputFile(path)
	if err != nil {
		return learningcontract.LearningCycleScheduleV1{}, err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		payload,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumLearningCycleScheduleInputBytes,
			MaxDepth: 64,
			MaxNodes: maximumLearningCycleScheduleInputBytes,
		},
	)
	if err != nil {
		return learningcontract.LearningCycleScheduleV1{}, fmt.Errorf(
			"validate Schedule JSON: %w",
			err,
		)
	}
	var schedule learningcontract.LearningCycleScheduleV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schedule); err != nil {
		return learningcontract.LearningCycleScheduleV1{}, fmt.Errorf(
			"decode Schedule JSON: %w",
			err,
		)
	}
	if trailingElement, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return learningcontract.LearningCycleScheduleV1{}, fmt.Errorf(
				"Schedule JSON has trailing token %v",
				trailingElement,
			)
		}
		return learningcontract.LearningCycleScheduleV1{}, fmt.Errorf(
			"decode Schedule JSON trailer: %w",
			err,
		)
	}
	frozen, _, _, err := learningcontract.NewLearningCycleScheduleV1(schedule)
	if err != nil {
		return learningcontract.LearningCycleScheduleV1{}, err
	}
	return frozen, nil
}

func readBoundedLearningCycleInputFile(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("Schedule input path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve Schedule input: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve Schedule input: %w", err)
	}
	if !sameModuleApplyPlanPath(absolute, resolved) {
		return nil, errors.New("Schedule input path must not traverse symbolic links")
	}
	before, err := os.Lstat(resolved)
	if err != nil {
		return nil, fmt.Errorf("inspect Schedule input: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, errors.New("Schedule input must be an ordinary non-symlink file")
	}
	if before.Size() <= 0 || before.Size() > maximumLearningCycleScheduleInputBytes {
		return nil, fmt.Errorf(
			"Schedule input must contain 1-%d bytes",
			maximumLearningCycleScheduleInputBytes,
		)
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("open Schedule input: %w", err)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() ||
		!os.SameFile(before, opened) || opened.Size() != before.Size() {
		_ = file.Close()
		return nil, errors.New("Schedule input changed or is not an ordinary file")
	}
	payload, readErr := io.ReadAll(io.LimitReader(
		file,
		maximumLearningCycleScheduleInputBytes+1,
	))
	after, afterErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(readErr, afterErr, closeErr); err != nil {
		return nil, fmt.Errorf("read Schedule input: %w", err)
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() ||
		opened.ModTime() != after.ModTime() || len(payload) == 0 ||
		len(payload) > maximumLearningCycleScheduleInputBytes ||
		int64(len(payload)) != opened.Size() {
		return nil, errors.New("Schedule input changed or exceeds its size limit")
	}
	return bytes.Clone(payload), nil
}

func parseRequiredLearningCycleObservedAt(value string) (time.Time, error) {
	return parseRequiredLearningCycleUTC("--observed-at", value, false)
}

func parseRequiredLearningCycleUTC(
	name string,
	value string,
	allowEpoch bool,
) (time.Time, error) {
	if value == "" || value != strings.TrimSpace(value) {
		return time.Time{}, fmt.Errorf(
			"%s must be a trimmed UTC RFC3339 timestamp",
			name,
		)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be a UTC RFC3339 timestamp", name)
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return time.Time{}, fmt.Errorf("%s must use UTC", name)
	}
	if parsed.Nanosecond()%1_000 != 0 {
		return time.Time{}, fmt.Errorf(
			"%s must not exceed microsecond precision",
			name,
		)
	}
	micros := parsed.UnixMicro()
	if micros < 0 || (!allowEpoch && micros == 0) ||
		micros > learningcontract.MaxLearningCycleUnixMicrosV1 {
		return time.Time{}, fmt.Errorf("%s is outside the supported range", name)
	}
	return time.UnixMicro(micros).UTC(), nil
}

func formatLearningCycleTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return time.UnixMicro(value.UnixMicro()).UTC().Format(time.RFC3339Nano)
}

func flagWasExplicitlySet(flags interface{ Visit(func(*flag.Flag)) }, name string) bool {
	set := false
	flags.Visit(func(value *flag.Flag) {
		if value.Name == name {
			set = true
		}
	})
	return set
}
