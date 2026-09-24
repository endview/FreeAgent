package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const maxLearningCycleOperationTasks = learningcontract.MaxLearningCycleReportEntriesV1

// LearningCycleUsageStatus is the closed, non-executing disposition
// of one task observed by ReconcileLearningCycleTasks.
type LearningCycleUsageStatus string

const (
	LearningCycleReconciliationPendingUnadmitted LearningCycleUsageStatus = "PENDING_UNADMITTED"
	LearningCycleReconciliationOpenNotReady      LearningCycleUsageStatus = "OPEN_NOT_READY"
	LearningCycleReconciliationFinalized         LearningCycleUsageStatus = "FINALIZED"
	LearningCycleReconciliationExactTerminal     LearningCycleUsageStatus = "EXACT_TERMINAL"
)

// LearningCycleReconciliationEntry reports the final Store observation for
// one bounded task. Task records are ordered by logical window then TaskID.
type LearningCycleReconciliationEntry struct {
	Status LearningCycleUsageStatus
	Task   LearningCycleTaskRecord
}

// ReconcileLearningCycleTasksResult is scoped to one exact Schedule. The
// Schedule and every Task record are detached from Store-owned buffers.
type ReconcileLearningCycleTasksResult struct {
	Schedule LearningCycleScheduleRecord
	Entries  []LearningCycleReconciliationEntry
}

// BuildLearningCycleReportResult contains one transient frozen report. No
// report bytes or digest are persisted by BuildLearningCycleReport.
type BuildLearningCycleReportResult struct {
	Report          learningcontract.LearningCycleReportV1
	ReportCanonical []byte
	ReportDigest    string
}

// ReconcileLearningCycleTasks compensates only missing Store projections for
// one exact Schedule. It never creates or executes a Run or Attempt and never
// calls a Loop, Provider, Gateway, module, network, or Secret surface.
func (store *Store) ReconcileLearningCycleTasks(
	ctx context.Context,
	tenantID string,
	scheduleID string,
) (ReconcileLearningCycleTasksResult, error) {
	if ctx == nil || !validLeaseOpaqueID(tenantID) || !validLeaseOpaqueID(scheduleID) {
		return ReconcileLearningCycleTasksResult{}, fmt.Errorf(
			"%w: invalid reconciliation scope",
			ErrInvalidLearningCycle,
		)
	}
	schedule, tasks, err := store.probeLearningCycleReconciliationTasks(
		ctx,
		tenantID,
		scheduleID,
	)
	if err != nil {
		return ReconcileLearningCycleTasksResult{}, err
	}
	result := ReconcileLearningCycleTasksResult{
		Schedule: schedule,
		Entries:  make([]LearningCycleReconciliationEntry, 0, len(tasks)),
	}
	for _, task := range tasks {
		entry, err := store.reconcileLearningCycleTask(ctx, task)
		if err != nil {
			return ReconcileLearningCycleTasksResult{}, err
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, nil
}

func (store *Store) probeLearningCycleReconciliationTasks(
	ctx context.Context,
	tenantID string,
	scheduleID string,
) (LearningCycleScheduleRecord, []LearningCycleTaskRecord, error) {
	unlock, err := store.lockOpen()
	if err != nil {
		return LearningCycleScheduleRecord{}, nil, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return LearningCycleScheduleRecord{}, nil, fmt.Errorf(
			"currentstore: acquire Learning cycle reconciliation connection: %w",
			err,
		)
	}
	defer connection.Close()
	schedule, found, err := queryLearningCycleSchedule(ctx, connection, tenantID, scheduleID)
	if err != nil {
		return LearningCycleScheduleRecord{}, nil, err
	}
	if !found {
		return LearningCycleScheduleRecord{}, nil, ErrLearningCycleNotFound
	}
	rows, err := connection.QueryContext(ctx, `
		SELECT task_id
		FROM learning_cycle_tasks
		WHERE tenant_id=? AND schedule_id=?
		  AND state IN ('PENDING', 'RUN_ADMITTED', 'UNKNOWN')
		ORDER BY scheduled_for, task_id COLLATE BINARY
		LIMIT ?
	`, tenantID, scheduleID, maxLearningCycleOperationTasks+1)
	if err != nil {
		return LearningCycleScheduleRecord{}, nil, fmt.Errorf(
			"currentstore: probe Learning cycle reconciliation tasks: %w",
			err,
		)
	}
	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			_ = rows.Close()
			return LearningCycleScheduleRecord{}, nil, fmt.Errorf(
				"currentstore: scan Learning cycle reconciliation TaskID: %w",
				err,
			)
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return LearningCycleScheduleRecord{}, nil, fmt.Errorf(
			"currentstore: iterate Learning cycle reconciliation TaskIDs: %w",
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return LearningCycleScheduleRecord{}, nil, fmt.Errorf(
			"currentstore: close Learning cycle reconciliation TaskIDs: %w",
			err,
		)
	}
	if len(taskIDs) > maxLearningCycleOperationTasks {
		return LearningCycleScheduleRecord{}, nil, fmt.Errorf(
			"%w: reconciliation observed more than %d tasks",
			ErrLearningCycleLimit,
			maxLearningCycleOperationTasks,
		)
	}
	tasks := make([]LearningCycleTaskRecord, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		task, found, err := queryLearningCycleTask(ctx, connection, tenantID, taskID)
		if err != nil {
			return LearningCycleScheduleRecord{}, nil, err
		}
		if !found || task.ScheduleID != scheduleID ||
			(task.State != learningcontract.LearningCycleTaskPendingV1 &&
				task.State != learningcontract.LearningCycleTaskRunAdmittedV1 &&
				task.State != learningcontract.LearningCycleTaskUnknownV1) {
			return LearningCycleScheduleRecord{}, nil, fmt.Errorf(
				"%w: reconciliation probe changed beneath its Store lock",
				ErrLearningCycleIntegrity,
			)
		}
		tasks = append(tasks, detachLearningCycleTaskRecord(task))
	}
	return detachLearningCycleScheduleRecord(schedule), tasks, nil
}

func (store *Store) reconcileLearningCycleTask(
	ctx context.Context,
	task LearningCycleTaskRecord,
) (LearningCycleReconciliationEntry, error) {
	current := task
	for retry := 0; retry < 3; retry++ {
		switch current.State {
		case learningcontract.LearningCycleTaskPendingV1:
			return LearningCycleReconciliationEntry{
				Status: LearningCycleReconciliationPendingUnadmitted,
				Task:   detachLearningCycleTaskRecord(current),
			}, nil
		case learningcontract.LearningCycleTaskRunAdmittedV1,
			learningcontract.LearningCycleTaskUnknownV1:
		case learningcontract.LearningCycleTaskProposalSubmittedV1,
			learningcontract.LearningCycleTaskNoChangeV1,
			learningcontract.LearningCycleTaskSourceOccupiedV1,
			learningcontract.LearningCycleTaskContentOccupiedV1,
			learningcontract.LearningCycleTaskTargetOccupiedV1,
			learningcontract.LearningCycleTaskFailedV1,
			learningcontract.LearningCycleTaskInvalidResultV1:
			return LearningCycleReconciliationEntry{
				Status: LearningCycleReconciliationExactTerminal,
				Task:   detachLearningCycleTaskRecord(current),
			}, nil
		default:
			return LearningCycleReconciliationEntry{}, fmt.Errorf(
				"%w: unsupported reconciliation task state %q",
				ErrLearningCycleIntegrity,
				current.State,
			)
		}

		finalized, err := store.FinalizeLearningCycleTask(
			ctx,
			FinalizeLearningCycleTaskInput{
				TenantID:             current.TenantID,
				TaskID:               current.TaskID,
				ExpectedTaskRevision: current.Revision,
			},
		)
		if err == nil {
			if finalized.Applied {
				return LearningCycleReconciliationEntry{
					Status: LearningCycleReconciliationFinalized,
					Task:   detachLearningCycleTaskRecord(finalized.Task),
				}, nil
			}
			if learningCycleClosedState(finalized.Task.State) {
				return LearningCycleReconciliationEntry{
					Status: LearningCycleReconciliationExactTerminal,
					Task:   detachLearningCycleTaskRecord(finalized.Task),
				}, nil
			}
			if finalized.Task.State == learningcontract.LearningCycleTaskUnknownV1 &&
				(finalized.Task.Revision != current.Revision ||
					current.State != finalized.Task.State) {
				current = finalized.Task
				continue
			}
			return LearningCycleReconciliationEntry{
				Status: LearningCycleReconciliationOpenNotReady,
				Task:   detachLearningCycleTaskRecord(finalized.Task),
			}, nil
		}
		if !errors.Is(err, ErrLearningCycleNotReady) &&
			!errors.Is(err, ErrLearningCycleConflict) {
			return LearningCycleReconciliationEntry{}, err
		}
		observed, readErr := store.GetLearningCycleTask(ctx, current.TenantID, current.TaskID)
		if readErr != nil {
			return LearningCycleReconciliationEntry{}, errors.Join(err, readErr)
		}
		if learningCycleClosedState(observed.State) {
			return LearningCycleReconciliationEntry{
				Status: LearningCycleReconciliationExactTerminal,
				Task:   observed,
			}, nil
		}
		if observed.State == learningcontract.LearningCycleTaskPendingV1 {
			return LearningCycleReconciliationEntry{
				Status: LearningCycleReconciliationPendingUnadmitted,
				Task:   observed,
			}, nil
		}
		if observed.State != current.State || observed.Revision != current.Revision {
			current = observed
			continue
		}
		if errors.Is(err, ErrLearningCycleNotReady) {
			return LearningCycleReconciliationEntry{
				Status: LearningCycleReconciliationOpenNotReady,
				Task:   observed,
			}, nil
		}
		return LearningCycleReconciliationEntry{}, err
	}
	return LearningCycleReconciliationEntry{}, fmt.Errorf(
		"%w: reconciliation did not reach a stable task observation",
		ErrLearningCycleConflict,
	)
}

// BuildLearningCycleReport freezes one read-only half-open [start,end) report
// for an exact Schedule. It probes 1025 rows and rejects overflow instead of
// silently truncating the 1024-entry contract.
func (store *Store) BuildLearningCycleReport(
	ctx context.Context,
	tenantID string,
	scheduleID string,
	start time.Time,
	end time.Time,
) (BuildLearningCycleReportResult, error) {
	if ctx == nil || !validLeaseOpaqueID(tenantID) || !validLeaseOpaqueID(scheduleID) {
		return BuildLearningCycleReportResult{}, fmt.Errorf(
			"%w: invalid report scope",
			ErrInvalidLearningCycle,
		)
	}
	startMicros := start.UTC().UnixMicro()
	endMicros := end.UTC().UnixMicro()
	if startMicros < 0 || endMicros <= 0 ||
		startMicros > learningcontract.MaxLearningCycleUnixMicrosV1 ||
		endMicros > learningcontract.MaxLearningCycleUnixMicrosV1 ||
		endMicros <= startMicros {
		return BuildLearningCycleReportResult{}, fmt.Errorf(
			"%w: report requires a bounded non-empty half-open time window",
			ErrInvalidLearningCycle,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return BuildLearningCycleReportResult{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return BuildLearningCycleReportResult{}, fmt.Errorf(
			"currentstore: acquire Learning cycle report connection: %w",
			err,
		)
	}
	defer connection.Close()
	schedule, found, err := queryLearningCycleSchedule(ctx, connection, tenantID, scheduleID)
	if err != nil {
		return BuildLearningCycleReportResult{}, err
	}
	if !found {
		return BuildLearningCycleReportResult{}, ErrLearningCycleNotFound
	}
	taskIDs, err := probeLearningCycleReportTaskIDs(
		ctx,
		connection,
		tenantID,
		scheduleID,
		startMicros,
		endMicros,
	)
	if err != nil {
		return BuildLearningCycleReportResult{}, err
	}
	entries := make([]learningcontract.LearningCycleReportEntryV1, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		task, found, err := queryLearningCycleTask(ctx, connection, tenantID, taskID)
		if err != nil {
			return BuildLearningCycleReportResult{}, err
		}
		if !found || task.ScheduleID != scheduleID ||
			task.ScheduleDigest != schedule.ScheduleDigest ||
			task.ScheduledFor.UnixMicro() < startMicros ||
			task.ScheduledFor.UnixMicro() >= endMicros {
			return BuildLearningCycleReportResult{}, fmt.Errorf(
				"%w: report Task differs from exact Schedule/window",
				ErrLearningCycleIntegrity,
			)
		}
		entries = append(entries, learningcontract.LearningCycleReportEntryV1{
			ScheduledForMicros: task.ScheduledFor.UnixMicro(),
			TaskID:             task.TaskID,
			RunID:              task.RunID,
			State:              task.State,
			ProposalID:         task.ProposalID,
		})
	}
	report, canonical, digest, err := learningcontract.NewLearningCycleReportV1(
		learningcontract.LearningCycleReportV1{
			SchemaVersion:     learningcontract.LearningCycleReportSchemaVersionV1,
			TenantID:          tenantID,
			ScheduleID:        scheduleID,
			ScheduleDigest:    schedule.ScheduleDigest,
			WindowStartMicros: startMicros,
			WindowEndMicros:   endMicros,
			Entries:           entries,
		},
	)
	if err != nil {
		return BuildLearningCycleReportResult{}, fmt.Errorf(
			"%w: freeze report: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	return BuildLearningCycleReportResult{
		Report:          report,
		ReportCanonical: bytes.Clone(canonical),
		ReportDigest:    digest,
	}, nil
}

func probeLearningCycleReportTaskIDs(
	ctx context.Context,
	connection *sql.Conn,
	tenantID string,
	scheduleID string,
	startMicros int64,
	endMicros int64,
) ([]string, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT task_id
		FROM learning_cycle_tasks
		WHERE tenant_id=? AND schedule_id=?
		  AND scheduled_for>=? AND scheduled_for<?
		ORDER BY scheduled_for, task_id COLLATE BINARY
		LIMIT ?
	`, tenantID, scheduleID, startMicros, endMicros, maxLearningCycleOperationTasks+1)
	if err != nil {
		return nil, fmt.Errorf("currentstore: probe Learning cycle report tasks: %w", err)
	}
	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("currentstore: scan Learning cycle report TaskID: %w", err)
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("currentstore: iterate Learning cycle report TaskIDs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("currentstore: close Learning cycle report TaskIDs: %w", err)
	}
	if len(taskIDs) > maxLearningCycleOperationTasks {
		return nil, fmt.Errorf(
			"%w: report observed more than %d tasks",
			ErrLearningCycleLimit,
			maxLearningCycleOperationTasks,
		)
	}
	for _, taskID := range taskIDs {
		if !moduleapi.ValidSHA256(taskID) {
			return nil, fmt.Errorf("%w: report probe returned invalid TaskID", ErrLearningCycleIntegrity)
		}
	}
	return taskIDs, nil
}
