package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	// ErrInvalidLearningCycle identifies malformed schedule, task, fencing, or
	// transition input before any authoritative Store write.
	ErrInvalidLearningCycle = errors.New(
		"currentstore: invalid Learning cycle request",
	)

	// ErrLearningCycleNotFound is tenant-scoped so schedule and task reads do
	// not become cross-tenant identity oracles.
	ErrLearningCycleNotFound = errors.New(
		"currentstore: Learning cycle fact not found",
	)

	// ErrLearningCycleConflict identifies a stale CAS fence or an attempt to
	// bind a different Run or terminal projection to an existing logical task.
	ErrLearningCycleConflict = errors.New(
		"currentstore: Learning cycle state conflict",
	)

	// ErrLearningCycleIntegrity identifies a persisted schedule/task projection
	// that cannot be reconstructed from the canonical Learning contracts and
	// the existing Run, Attempt, Result, and Proposal ledgers.
	ErrLearningCycleIntegrity = errors.New(
		"currentstore: Learning cycle integrity violation",
	)

	// ErrLearningCycleNotReady means the exact admitted Run has not produced an
	// authoritative terminal Attempt that can be projected without replay.
	ErrLearningCycleNotReady = errors.New(
		"currentstore: Learning cycle task is not ready to close",
	)

	// ErrLearningCycleLimit means a bounded reconciliation or report probe
	// observed more facts than its frozen contract permits in one call.
	ErrLearningCycleLimit = errors.New(
		"currentstore: Learning cycle bounded scan limit exceeded",
	)
)

// LearningCycleScheduleRecord is one immutable canonical schedule plus its
// narrow local enablement and monotonic scheduling-watermark projection.
// ScheduleCanonical and Schedule.KnowledgeVisibility are detached on return.
type LearningCycleScheduleRecord struct {
	Schedule          learningcontract.LearningCycleScheduleV1
	ScheduleCanonical []byte
	ScheduleDigest    string
	Enabled           bool
	Revision          uint64
	LastScheduledFor  time.Time
	NextDueAt         time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CreateLearningCycleScheduleResult distinguishes the sole creator from an
// exact retry. Every new Schedule is disabled at revision zero.
type CreateLearningCycleScheduleResult struct {
	Record  LearningCycleScheduleRecord
	Created bool
}

// SetLearningCycleScheduleEnabledInput changes only the Store-owned local
// policy bit. The immutable canonical Schedule is never patched or rebased.
type SetLearningCycleScheduleEnabledInput struct {
	TenantID         string
	ScheduleID       string
	ExpectedRevision uint64
	Enabled          bool
}

// SetLearningCycleScheduleEnabledResult reports whether the CAS changed the
// local policy. Exact same-state calls at the current revision are zero-write.
type SetLearningCycleScheduleEnabledResult struct {
	Record  LearningCycleScheduleRecord
	Applied bool
}

// LearningCycleTaskRecord is one immutable logical schedule window. Run,
// Attempt, Result, and Proposal fields are projections into the existing
// authoritative ledgers; this record is not an execution or effect ledger.
type LearningCycleTaskRecord struct {
	TaskID            string
	TenantID          string
	WorkspaceID       string
	ScheduleID        string
	ScheduleDigest    string
	ScheduledFor      time.Time
	Request           learningcontract.LearningCycleRequestV1
	RequestCanonical  []byte
	RequestDigest     string
	State             learningcontract.LearningCycleTaskStateV1
	Revision          uint64
	RunID             string
	RunManifestDigest string
	AttemptID         string
	ResultRef         string
	ProposalID        string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// EnsureLearningCycleTaskResult returns an already-open task or the one task
// created for the latest due logical window. Due is false before the boundary
// and for a disabled Schedule with no already-open task.
type EnsureLearningCycleTaskResult struct {
	Schedule LearningCycleScheduleRecord
	Task     LearningCycleTaskRecord
	Due      bool
	Created  bool
}

// CommitLearningCycleRunAdmissionInput binds one exact PENDING task to one
// ordinary Run. ExpectedTaskRevision is normally zero.
type CommitLearningCycleRunAdmissionInput struct {
	TenantID             string
	TaskID               string
	ExpectedTaskRevision uint64
	Run                  CommitRunAdmissionInput
}

// CommitLearningCycleRunAdmissionResult contains the same ordinary Run
// admission receipt used by every other execution path and the bound task.
type CommitLearningCycleRunAdmissionResult struct {
	Task    LearningCycleTaskRecord
	Run     RunAdmissionResult
	Created bool
}

// FinalizeLearningCycleTaskInput supplies only the task CAS fence. The Store
// derives the unique authoritative Attempt, result classification, Proposal,
// and any occupied axis; callers cannot select terminal facts.
type FinalizeLearningCycleTaskInput struct {
	TenantID             string
	TaskID               string
	ExpectedTaskRevision uint64
}

// FinalizeLearningCycleTaskResult distinguishes the sole Store projection
// from a no-write exact retry. UNKNOWN reconciliation retains the same Attempt.
type FinalizeLearningCycleTaskResult struct {
	Task    LearningCycleTaskRecord
	Applied bool
}

// CreateLearningCycleSchedule freezes and persists one immutable schedule.
// Enabled is always false and Revision is always zero for a new row.
func (store *Store) CreateLearningCycleSchedule(
	ctx context.Context,
	schedule learningcontract.LearningCycleScheduleV1,
) (result CreateLearningCycleScheduleResult, returnErr error) {
	if ctx == nil {
		return result, fmt.Errorf("%w: context is nil", ErrInvalidLearningCycle)
	}
	frozen, canonical, digest, err :=
		learningcontract.NewLearningCycleScheduleV1(schedule)
	if err != nil {
		return result, fmt.Errorf("%w: Schedule: %v", ErrInvalidLearningCycle, err)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("currentstore: acquire Learning cycle Schedule connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, fmt.Errorf("currentstore: begin Learning cycle Schedule creation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()

	existing, found, err := queryLearningCycleSchedule(
		ctx,
		connection,
		frozen.TenantID,
		frozen.ScheduleID,
	)
	if err != nil {
		return result, err
	}
	if found {
		if existing.ScheduleDigest != digest ||
			!bytes.Equal(existing.ScheduleCanonical, canonical) {
			return result, fmt.Errorf(
				"%w: ScheduleID already has different immutable policy",
				ErrLearningCycleConflict,
			)
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf("currentstore: commit Learning cycle Schedule retry: %w", err)
		}
		committed = true
		return CreateLearningCycleScheduleResult{
			Record: detachLearningCycleScheduleRecord(existing),
		}, nil
	}

	createdAt := nowUnixMicro()
	if createdAt <= 0 {
		return result, fmt.Errorf("%w: invalid creation time", ErrLearningCycleIntegrity)
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO learning_cycle_schedules(
			tenant_id, workspace_id, schedule_id, schedule_digest,
			schedule_canonical, schedule_size_bytes,
			enabled, revision, last_scheduled_for, next_due_at,
			created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, 0, 0, NULL, ?, ?, ?)
	`,
		frozen.TenantID,
		frozen.WorkspaceID,
		frozen.ScheduleID,
		digest,
		canonical,
		len(canonical),
		frozen.FirstDueAtUnixMicros,
		createdAt,
		createdAt,
	); err != nil {
		return result, fmt.Errorf("currentstore: insert Learning cycle Schedule: %w", err)
	}
	stored, found, err := queryLearningCycleSchedule(
		ctx,
		connection,
		frozen.TenantID,
		frozen.ScheduleID,
	)
	if err != nil || !found || stored.ScheduleDigest != digest {
		return result, fmt.Errorf(
			"%w: inserted Schedule did not round-trip: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, fmt.Errorf("currentstore: commit Learning cycle Schedule: %w", err)
	}
	committed = true
	return CreateLearningCycleScheduleResult{
		Record:  detachLearningCycleScheduleRecord(stored),
		Created: true,
	}, nil
}

// SetLearningCycleScheduleEnabled applies one explicit local-policy CAS. A
// disable never deletes or rewrites an already-created task or admitted Run.
func (store *Store) SetLearningCycleScheduleEnabled(
	ctx context.Context,
	input SetLearningCycleScheduleEnabledInput,
) (result SetLearningCycleScheduleEnabledResult, returnErr error) {
	if ctx == nil || !validLeaseOpaqueID(input.TenantID) ||
		!validLeaseOpaqueID(input.ScheduleID) || input.ExpectedRevision > math.MaxInt64 {
		return result, fmt.Errorf("%w: invalid Schedule enablement fence", ErrInvalidLearningCycle)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("currentstore: acquire Learning cycle enablement connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, fmt.Errorf("currentstore: begin Learning cycle enablement: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	record, found, err := queryLearningCycleSchedule(
		ctx,
		connection,
		input.TenantID,
		input.ScheduleID,
	)
	if err != nil {
		return result, err
	}
	if !found {
		return result, ErrLearningCycleNotFound
	}
	if record.Revision != input.ExpectedRevision {
		return result, fmt.Errorf(
			"%w: Schedule revision is %d, want %d",
			ErrLearningCycleConflict,
			record.Revision,
			input.ExpectedRevision,
		)
	}
	if record.Enabled == input.Enabled {
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf("currentstore: commit unchanged Learning cycle enablement: %w", err)
		}
		committed = true
		return SetLearningCycleScheduleEnabledResult{
			Record: detachLearningCycleScheduleRecord(record),
		}, nil
	}
	if record.Revision == math.MaxInt64 {
		return result, fmt.Errorf("%w: Schedule revision cannot advance", ErrLearningCycleConflict)
	}
	updatedAt := learningCycleTransitionMicros(record.UpdatedAt.UnixMicro())
	enabled := 0
	if input.Enabled {
		enabled = 1
	}
	update, err := connection.ExecContext(ctx, `
		UPDATE learning_cycle_schedules
		SET enabled=?, revision=revision+1, updated_at=?
		WHERE tenant_id=? AND schedule_id=? AND revision=? AND enabled=?
	`,
		enabled,
		updatedAt,
		input.TenantID,
		input.ScheduleID,
		int64(input.ExpectedRevision),
		boolToSQLite(!input.Enabled),
	)
	if err != nil {
		return result, fmt.Errorf("currentstore: update Learning cycle enablement: %w", err)
	}
	if err := requireLearningCycleCASRow(update, "enable Schedule"); err != nil {
		return result, err
	}
	record, found, err = queryLearningCycleSchedule(
		ctx,
		connection,
		input.TenantID,
		input.ScheduleID,
	)
	if err != nil || !found || record.Enabled != input.Enabled ||
		record.Revision != input.ExpectedRevision+1 {
		return result, fmt.Errorf(
			"%w: enabled Schedule did not round-trip: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, fmt.Errorf("currentstore: commit Learning cycle enablement: %w", err)
	}
	committed = true
	return SetLearningCycleScheduleEnabledResult{
		Record:  detachLearningCycleScheduleRecord(record),
		Applied: true,
	}, nil
}

// EnsureDueTask atomically freezes at most one PENDING Learning-cycle task for the
// latest due Schedule slot. Missed slots are coalesced; a clock rollback never
// moves either persisted watermark backward.
func (store *Store) EnsureDueTask(
	ctx context.Context,
	tenantID string,
	scheduleID string,
	observedUTC time.Time,
) (result EnsureLearningCycleTaskResult, returnErr error) {
	if ctx == nil || !validLeaseOpaqueID(tenantID) ||
		!validLeaseOpaqueID(scheduleID) || observedUTC.IsZero() {
		return result, fmt.Errorf("%w: invalid due-task request", ErrInvalidLearningCycle)
	}
	observedMicros := observedUTC.UTC().UnixMicro()
	if observedMicros <= 0 {
		return result, fmt.Errorf("%w: observed UTC time must be positive", ErrInvalidLearningCycle)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("currentstore: acquire Learning cycle Tick connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, fmt.Errorf("currentstore: begin Learning cycle Tick: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	schedule, found, err := queryLearningCycleSchedule(
		ctx,
		connection,
		tenantID,
		scheduleID,
	)
	if err != nil {
		return result, err
	}
	if !found {
		return result, ErrLearningCycleNotFound
	}
	open, found, err := queryOpenLearningCycleTask(
		ctx,
		connection,
		tenantID,
		scheduleID,
	)
	if err != nil {
		return result, err
	}
	if found {
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceLearningTaskV1, open.TaskID,
		); err != nil {
			return result, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf("currentstore: commit existing Learning cycle task read: %w", err)
		}
		committed = true
		return EnsureLearningCycleTaskResult{
			Schedule: detachLearningCycleScheduleRecord(schedule),
			Task:     detachLearningCycleTaskRecord(open),
			Due:      true,
		}, nil
	}
	if !schedule.Enabled || observedMicros < schedule.NextDueAt.UnixMicro() {
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf("currentstore: commit idle Learning cycle Tick: %w", err)
		}
		committed = true
		return EnsureLearningCycleTaskResult{
			Schedule: detachLearningCycleScheduleRecord(schedule),
		}, nil
	}
	if schedule.Revision == math.MaxInt64 {
		return result, fmt.Errorf("%w: Schedule revision cannot advance", ErrLearningCycleConflict)
	}
	intervalMicros, err := learningCycleIntervalMicros(schedule.Schedule)
	if err != nil {
		return result, err
	}
	nextDueMicros := schedule.NextDueAt.UnixMicro()
	elapsed := observedMicros - nextDueMicros
	missedIntervals := elapsed / intervalMicros
	if missedIntervals > (math.MaxInt64-nextDueMicros)/intervalMicros {
		return result, fmt.Errorf("%w: coalesced due window overflows", ErrLearningCycleConflict)
	}
	scheduledFor := nextDueMicros + missedIntervals*intervalMicros
	if scheduledFor > math.MaxInt64-intervalMicros {
		return result, fmt.Errorf("%w: next due time overflows", ErrLearningCycleConflict)
	}
	nextDue := scheduledFor + intervalMicros
	request, requestCanonical, requestDigest, err :=
		learningcontract.NewLearningCycleRequestV1(
			schedule.Schedule,
			schedule.ScheduleCanonical,
			schedule.ScheduleDigest,
			scheduledFor,
		)
	if err != nil {
		return result, fmt.Errorf("%w: derive due request: %v", ErrLearningCycleIntegrity, err)
	}
	taskID, err := learningcontract.DeriveLearningCycleTaskIDV1(
		schedule.ScheduleDigest,
		scheduledFor,
		requestDigest,
	)
	if err != nil {
		return result, fmt.Errorf("%w: derive TaskID: %v", ErrLearningCycleIntegrity, err)
	}
	createdAt := nowUnixMicro()
	if createdAt <= 0 {
		return result, fmt.Errorf("%w: invalid task creation time", ErrLearningCycleIntegrity)
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO learning_cycle_tasks(
			task_id, tenant_id, workspace_id, schedule_id, schedule_digest,
			scheduled_for, request_digest, request_canonical,
			request_size_bytes, state, revision,
			run_id, run_manifest_digest, attempt_id, result_ref, proposal_id,
			created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 'PENDING', 0,
			NULL, NULL, NULL, NULL, NULL, ?, ?)
	`,
		taskID,
		tenantID,
		schedule.Schedule.WorkspaceID,
		scheduleID,
		schedule.ScheduleDigest,
		scheduledFor,
		requestDigest,
		requestCanonical,
		len(requestCanonical),
		createdAt,
		createdAt,
	); err != nil {
		return result, fmt.Errorf("currentstore: insert due Learning cycle task: %w", err)
	}
	if err := appendTaskResourceObservationV1(
		ctx, connection, taskID, overviewTransitionTaskCreateV1,
	); err != nil {
		return result, err
	}
	updatedAt := learningCycleTransitionMicros(schedule.UpdatedAt.UnixMicro())
	update, err := connection.ExecContext(ctx, `
		UPDATE learning_cycle_schedules
		SET last_scheduled_for=?, next_due_at=?,
			revision=revision+1, updated_at=?
		WHERE tenant_id=? AND schedule_id=? AND enabled=1
		  AND revision=? AND next_due_at=?
	`,
		scheduledFor,
		nextDue,
		updatedAt,
		tenantID,
		scheduleID,
		int64(schedule.Revision),
		nextDueMicros,
	)
	if err != nil {
		return result, fmt.Errorf("currentstore: advance Learning cycle Schedule watermark: %w", err)
	}
	if err := requireLearningCycleCASRow(update, "advance Schedule watermark"); err != nil {
		return result, err
	}
	storedTask, found, err := queryLearningCycleTask(ctx, connection, tenantID, taskID)
	if err != nil || !found || storedTask.Request != request {
		return result, fmt.Errorf(
			"%w: due task did not round-trip: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	schedule, found, err = queryLearningCycleSchedule(ctx, connection, tenantID, scheduleID)
	if err != nil || !found || schedule.LastScheduledFor.UnixMicro() != scheduledFor ||
		schedule.NextDueAt.UnixMicro() != nextDue {
		return result, fmt.Errorf(
			"%w: advanced Schedule did not round-trip: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, fmt.Errorf("currentstore: commit Learning cycle Tick: %w", err)
	}
	committed = true
	return EnsureLearningCycleTaskResult{
		Schedule: detachLearningCycleScheduleRecord(schedule),
		Task:     detachLearningCycleTaskRecord(storedTask),
		Due:      true,
		Created:  true,
	}, nil
}

// CommitLearningCycleRunAdmission publishes the existing ordinary Run closure
// and binds it to one PENDING task in the same BEGIN IMMEDIATE transaction.
func (store *Store) CommitLearningCycleRunAdmission(
	ctx context.Context,
	input CommitLearningCycleRunAdmissionInput,
) (result CommitLearningCycleRunAdmissionResult, returnErr error) {
	if ctx == nil || !validLeaseOpaqueID(input.TenantID) ||
		!moduleapi.ValidSHA256(input.TaskID) || input.ExpectedTaskRevision > math.MaxInt64 {
		return result, fmt.Errorf("%w: invalid Run admission fence", ErrInvalidLearningCycle)
	}
	prepared, err := prepareCompleteRunAdmission(input.Run)
	if err != nil {
		return result, err
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("currentstore: acquire Learning cycle Run Admission connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, fmt.Errorf("currentstore: begin Learning cycle Run Admission: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	task, found, err := queryLearningCycleTask(ctx, connection, input.TenantID, input.TaskID)
	if err != nil {
		return result, err
	}
	if !found {
		return result, ErrLearningCycleNotFound
	}
	schedule, found, err := queryLearningCycleSchedule(
		ctx,
		connection,
		input.TenantID,
		task.ScheduleID,
	)
	if err != nil {
		return result, err
	}
	if !found {
		return result, fmt.Errorf("%w: Task Schedule is absent", ErrLearningCycleIntegrity)
	}
	if err := verifyLearningCyclePreparedAdmission(
		ctx,
		connection,
		prepared,
		schedule,
		task,
	); err != nil {
		return result, err
	}
	if task.State == learningcontract.LearningCycleTaskRunAdmittedV1 {
		if task.RunID != prepared.manifest.RunID ||
			task.RunManifestDigest != prepared.manifest.ManifestDigest {
			return result, fmt.Errorf("%w: task already binds another Run", ErrLearningCycleConflict)
		}
		run, found, err := resolveAdmissionWithQueryer(
			ctx,
			connection,
			prepared.intent.TenantID,
			prepared.intent.AdmissionKey,
			prepared.input.IntentDigest,
		)
		if err != nil || !found || run.RunID != task.RunID {
			return result, fmt.Errorf("%w: bound Run closure: %v", ErrLearningCycleIntegrity, err)
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceLearningTaskV1, input.TaskID,
		); err != nil {
			return result, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf("currentstore: commit Learning cycle Run retry: %w", err)
		}
		committed = true
		return CommitLearningCycleRunAdmissionResult{
			Task: detachLearningCycleTaskRecord(task),
			Run:  run,
		}, nil
	}
	if task.State != learningcontract.LearningCycleTaskPendingV1 ||
		task.Revision != input.ExpectedTaskRevision {
		return result, fmt.Errorf(
			"%w: task state/revision is %s/%d, want PENDING/%d",
			ErrLearningCycleConflict,
			task.State,
			task.Revision,
			input.ExpectedTaskRevision,
		)
	}
	if _, found, err := resolveAdmissionWithQueryer(
		ctx,
		connection,
		prepared.intent.TenantID,
		prepared.intent.AdmissionKey,
		prepared.input.IntentDigest,
	); err != nil {
		return result, err
	} else if found {
		return result, fmt.Errorf(
			"%w: unbound ordinary Run already occupies the Admission identity",
			ErrLearningCycleConflict,
		)
	}
	createdAt := nowUnixMicro()
	run, err := publishPreparedRunAdmission(ctx, connection, prepared, createdAt)
	if err != nil {
		return result, err
	}
	updatedAt := learningCycleTransitionMicros(task.UpdatedAt.UnixMicro())
	update, err := connection.ExecContext(ctx, `
		UPDATE learning_cycle_tasks
		SET state='RUN_ADMITTED', revision=1,
			run_id=?, run_manifest_digest=?, updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE task_id=? AND tenant_id=? AND state='PENDING' AND revision=?
	`,
		run.RunID,
		run.ManifestDigest,
		updatedAt,
		input.TaskID,
		input.TenantID,
		int64(input.ExpectedTaskRevision),
	)
	if err != nil {
		return result, fmt.Errorf("currentstore: bind Learning cycle Run: %w", err)
	}
	if err := requireLearningCycleCASRow(update, "bind task Run"); err != nil {
		return result, err
	}
	if err := appendTaskResourceObservationV1(
		ctx, connection, input.TaskID, overviewTransitionTaskAdmissionV1,
	); err != nil {
		return result, err
	}
	task, found, err = queryLearningCycleTask(ctx, connection, input.TenantID, input.TaskID)
	if err != nil || !found || task.RunID != run.RunID || task.Revision != 1 {
		return result, fmt.Errorf("%w: admitted task did not round-trip: %v", ErrLearningCycleIntegrity, err)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, fmt.Errorf("currentstore: commit Learning cycle Run Admission: %w", err)
	}
	committed = true
	run.Created = true
	return CommitLearningCycleRunAdmissionResult{
		Task:    detachLearningCycleTaskRecord(task),
		Run:     run,
		Created: true,
	}, nil
}

// FinalizeLearningCycleTask projects the sole authoritative terminal outcome
// of an admitted proposer Run. It performs no execution or Provider call.
// Proposal admission and the task projection share this transaction.
func (store *Store) FinalizeLearningCycleTask(
	ctx context.Context,
	input FinalizeLearningCycleTaskInput,
) (result FinalizeLearningCycleTaskResult, returnErr error) {
	if ctx == nil || !validLeaseOpaqueID(input.TenantID) ||
		!moduleapi.ValidSHA256(input.TaskID) ||
		input.ExpectedTaskRevision > math.MaxInt64 {
		return result, fmt.Errorf("%w: invalid terminal fence", ErrInvalidLearningCycle)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, fmt.Errorf("currentstore: acquire Learning cycle finalization connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, fmt.Errorf("currentstore: begin Learning cycle finalization: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()

	task, found, err := queryLearningCycleTask(
		ctx,
		connection,
		input.TenantID,
		input.TaskID,
	)
	if err != nil {
		return result, err
	}
	if !found {
		return result, ErrLearningCycleNotFound
	}
	if learningCycleClosedState(task.State) {
		if task.Revision != input.ExpectedTaskRevision &&
			(input.ExpectedTaskRevision == math.MaxUint64 ||
				task.Revision != input.ExpectedTaskRevision+1) {
			return result, fmt.Errorf(
				"%w: task revision is %d, want %d",
				ErrLearningCycleConflict,
				task.Revision,
				input.ExpectedTaskRevision,
			)
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceLearningTaskV1, input.TaskID,
		); err != nil {
			return result, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf("currentstore: commit Learning cycle finalization retry: %w", err)
		}
		committed = true
		return FinalizeLearningCycleTaskResult{
			Task: detachLearningCycleTaskRecord(task),
		}, nil
	}
	if task.State != learningcontract.LearningCycleTaskRunAdmittedV1 &&
		task.State != learningcontract.LearningCycleTaskUnknownV1 {
		return result, fmt.Errorf("%w: task has no admitted Run", ErrLearningCycleConflict)
	}
	if task.Revision != input.ExpectedTaskRevision {
		// UNKNOWN/2 is also the exact no-write result of a revision-1 finalize.
		if task.State == learningcontract.LearningCycleTaskUnknownV1 &&
			input.ExpectedTaskRevision == 1 && task.Revision == 2 {
			if err := verifyCurrentOverviewResourceObservationV1(
				ctx, connection, overviewResourceLearningTaskV1, input.TaskID,
			); err != nil {
				return result, err
			}
			if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
				return result, fmt.Errorf("currentstore: commit Learning cycle UNKNOWN retry: %w", err)
			}
			committed = true
			return FinalizeLearningCycleTaskResult{
				Task: detachLearningCycleTaskRecord(task),
			}, nil
		}
		return result, fmt.Errorf(
			"%w: task revision is %d, want %d",
			ErrLearningCycleConflict,
			task.Revision,
			input.ExpectedTaskRevision,
		)
	}

	schedule, found, err := queryLearningCycleSchedule(
		ctx,
		connection,
		input.TenantID,
		task.ScheduleID,
	)
	if err != nil {
		return result, err
	}
	if !found || schedule.ScheduleDigest != task.ScheduleDigest {
		return result, fmt.Errorf("%w: task Schedule is absent or changed", ErrLearningCycleIntegrity)
	}
	projection, err := deriveLearningCycleTerminalProjection(
		ctx,
		connection,
		schedule,
		task,
	)
	if err != nil {
		return result, err
	}
	if task.State == learningcontract.LearningCycleTaskUnknownV1 &&
		projection.State == learningcontract.LearningCycleTaskUnknownV1 {
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceLearningTaskV1, input.TaskID,
		); err != nil {
			return result, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, fmt.Errorf("currentstore: commit unchanged Learning cycle UNKNOWN: %w", err)
		}
		committed = true
		return FinalizeLearningCycleTaskResult{
			Task: detachLearningCycleTaskRecord(task),
		}, nil
	}
	nextRevision := uint64(2)
	if task.State == learningcontract.LearningCycleTaskUnknownV1 {
		nextRevision = 3
	}
	updatedAt := learningCycleTransitionMicros(task.UpdatedAt.UnixMicro())
	update, err := connection.ExecContext(ctx, `
		UPDATE learning_cycle_tasks
		SET state=?, revision=?, attempt_id=?, result_ref=?, proposal_id=?, updated_at=?,
			overview_observation_sequence=overview_observation_sequence+1
		WHERE task_id=? AND tenant_id=? AND state=? AND revision=?
	`,
		string(projection.State),
		int64(nextRevision),
		projection.AttemptID,
		nullableLearningCycleRef(projection.ResultRef),
		nullableLearningCycleRef(projection.ProposalID),
		updatedAt,
		input.TaskID,
		input.TenantID,
		string(task.State),
		int64(task.Revision),
	)
	if err != nil {
		return result, fmt.Errorf("currentstore: project Learning cycle terminal: %w", err)
	}
	if err := requireLearningCycleCASRow(update, "project task terminal"); err != nil {
		return result, err
	}
	if err := appendTaskResourceObservationV1(
		ctx, connection, input.TaskID, overviewTransitionTaskFinalizeV1,
	); err != nil {
		return result, err
	}
	task, found, err = queryLearningCycleTask(ctx, connection, input.TenantID, input.TaskID)
	if err != nil || !found || task.State != projection.State ||
		task.Revision != nextRevision || task.AttemptID != projection.AttemptID ||
		task.ResultRef != projection.ResultRef || task.ProposalID != projection.ProposalID {
		return result, fmt.Errorf("%w: terminal task did not round-trip: %v", ErrLearningCycleIntegrity, err)
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, fmt.Errorf("currentstore: commit Learning cycle finalization: %w", err)
	}
	committed = true
	return FinalizeLearningCycleTaskResult{
		Task:    detachLearningCycleTaskRecord(task),
		Applied: true,
	}, nil
}

// GetLearningCycleSchedule returns one tenant-scoped, detached Schedule fact.
func (store *Store) GetLearningCycleSchedule(
	ctx context.Context,
	tenantID string,
	scheduleID string,
) (LearningCycleScheduleRecord, error) {
	if ctx == nil || !validLeaseOpaqueID(tenantID) || !validLeaseOpaqueID(scheduleID) {
		return LearningCycleScheduleRecord{}, fmt.Errorf("%w: invalid Schedule read", ErrInvalidLearningCycle)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return LearningCycleScheduleRecord{}, err
	}
	defer unlock()
	record, found, err := queryLearningCycleSchedule(ctx, store.db, tenantID, scheduleID)
	if err != nil {
		return LearningCycleScheduleRecord{}, err
	}
	if !found {
		return LearningCycleScheduleRecord{}, ErrLearningCycleNotFound
	}
	return detachLearningCycleScheduleRecord(record), nil
}

// ListLearningCycleSchedules returns all tenant schedules in binary ID order.
func (store *Store) ListLearningCycleSchedules(
	ctx context.Context,
	tenantID string,
) ([]LearningCycleScheduleRecord, error) {
	if ctx == nil || !validLeaseOpaqueID(tenantID) {
		return nil, fmt.Errorf("%w: invalid Schedule list", ErrInvalidLearningCycle)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()
	rows, err := store.db.QueryContext(ctx, `
		SELECT
			tenant_id, workspace_id, schedule_id, schedule_digest,
			schedule_canonical, schedule_size_bytes,
			enabled, revision, last_scheduled_for, next_due_at,
			created_at, updated_at
		FROM learning_cycle_schedules
		WHERE tenant_id=?
		ORDER BY schedule_id COLLATE BINARY
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("currentstore: list Learning cycle Schedules: %w", err)
	}
	defer rows.Close()
	records := make([]LearningCycleScheduleRecord, 0)
	for rows.Next() {
		record, err := scanLearningCycleSchedule(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, detachLearningCycleScheduleRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("currentstore: iterate Learning cycle Schedules: %w", err)
	}
	return records, nil
}

// GetLearningCycleTask returns one tenant-scoped, detached logical task.
func (store *Store) GetLearningCycleTask(
	ctx context.Context,
	tenantID string,
	taskID string,
) (LearningCycleTaskRecord, error) {
	if ctx == nil || !validLeaseOpaqueID(tenantID) || !moduleapi.ValidSHA256(taskID) {
		return LearningCycleTaskRecord{}, fmt.Errorf("%w: invalid task read", ErrInvalidLearningCycle)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return LearningCycleTaskRecord{}, err
	}
	defer unlock()
	record, found, err := queryLearningCycleTask(ctx, store.db, tenantID, taskID)
	if err != nil {
		return LearningCycleTaskRecord{}, err
	}
	if !found {
		return LearningCycleTaskRecord{}, ErrLearningCycleNotFound
	}
	return detachLearningCycleTaskRecord(record), nil
}

// ListLearningCycleTasks returns one Schedule's tasks in logical-window order.
func (store *Store) ListLearningCycleTasks(
	ctx context.Context,
	tenantID string,
	scheduleID string,
) ([]LearningCycleTaskRecord, error) {
	if ctx == nil || !validLeaseOpaqueID(tenantID) || !validLeaseOpaqueID(scheduleID) {
		return nil, fmt.Errorf("%w: invalid task list", ErrInvalidLearningCycle)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()
	rows, err := store.db.QueryContext(ctx, `
		SELECT
			task_id, tenant_id, workspace_id, schedule_id, schedule_digest,
			scheduled_for, request_digest, request_canonical,
			request_size_bytes, state, revision,
			run_id, run_manifest_digest, attempt_id, result_ref, proposal_id,
			created_at, updated_at
		FROM learning_cycle_tasks
		WHERE tenant_id=? AND schedule_id=?
		ORDER BY scheduled_for, task_id COLLATE BINARY
	`, tenantID, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("currentstore: list Learning cycle tasks: %w", err)
	}
	defer rows.Close()
	records := make([]LearningCycleTaskRecord, 0)
	for rows.Next() {
		record, err := scanLearningCycleTask(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, detachLearningCycleTaskRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("currentstore: iterate Learning cycle tasks: %w", err)
	}
	return records, nil
}

type learningCycleScanner interface {
	Scan(...any) error
}

func queryLearningCycleSchedule(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	scheduleID string,
) (LearningCycleScheduleRecord, bool, error) {
	row := queryer.QueryRowContext(ctx, `
		SELECT
			tenant_id, workspace_id, schedule_id, schedule_digest,
			schedule_canonical, schedule_size_bytes,
			enabled, revision, last_scheduled_for, next_due_at,
			created_at, updated_at
		FROM learning_cycle_schedules
		WHERE tenant_id=? AND schedule_id=?
	`, tenantID, scheduleID)
	record, err := scanLearningCycleSchedule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LearningCycleScheduleRecord{}, false, nil
	}
	if err != nil {
		return LearningCycleScheduleRecord{}, false, err
	}
	return record, true, nil
}

func scanLearningCycleSchedule(
	scanner learningCycleScanner,
) (LearningCycleScheduleRecord, error) {
	var (
		tenantID         string
		workspaceID      string
		scheduleID       string
		digest           string
		canonical        []byte
		size             int64
		enabled          int64
		revision         int64
		lastScheduledFor sql.NullInt64
		nextDueAt        int64
		createdAt        int64
		updatedAt        int64
	)
	if err := scanner.Scan(
		&tenantID,
		&workspaceID,
		&scheduleID,
		&digest,
		&canonical,
		&size,
		&enabled,
		&revision,
		&lastScheduledFor,
		&nextDueAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return LearningCycleScheduleRecord{}, err
	}
	schedule, err := learningcontract.RestoreLearningCycleScheduleV1(canonical, digest)
	if err != nil || schedule.TenantID != tenantID || schedule.WorkspaceID != workspaceID ||
		schedule.ScheduleID != scheduleID ||
		size != int64(len(canonical)) || (enabled != 0 && enabled != 1) ||
		revision < 0 || updatedAt < createdAt {
		return LearningCycleScheduleRecord{}, fmt.Errorf(
			"%w: invalid Schedule canonical or SQL projection: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	created, err := timeFromUnixMicro(createdAt)
	if err != nil {
		return LearningCycleScheduleRecord{}, fmt.Errorf("%w: Schedule created_at", ErrLearningCycleIntegrity)
	}
	updated, err := timeFromUnixMicro(updatedAt)
	if err != nil {
		return LearningCycleScheduleRecord{}, fmt.Errorf("%w: Schedule updated_at", ErrLearningCycleIntegrity)
	}
	next, err := timeFromUnixMicro(nextDueAt)
	if err != nil {
		return LearningCycleScheduleRecord{}, fmt.Errorf("%w: Schedule next_due_at", ErrLearningCycleIntegrity)
	}
	intervalMicros, err := learningCycleIntervalMicros(schedule)
	if err != nil {
		return LearningCycleScheduleRecord{}, err
	}
	record := LearningCycleScheduleRecord{
		Schedule:          schedule,
		ScheduleCanonical: bytes.Clone(canonical),
		ScheduleDigest:    digest,
		Enabled:           enabled == 1,
		Revision:          uint64(revision),
		NextDueAt:         next,
		CreatedAt:         created,
		UpdatedAt:         updated,
	}
	if lastScheduledFor.Valid {
		last, err := timeFromUnixMicro(lastScheduledFor.Int64)
		if err != nil || lastScheduledFor.Int64 < schedule.FirstDueAtUnixMicros ||
			(lastScheduledFor.Int64-schedule.FirstDueAtUnixMicros)%intervalMicros != 0 ||
			nextDueAt-lastScheduledFor.Int64 != intervalMicros {
			return LearningCycleScheduleRecord{}, fmt.Errorf(
				"%w: Schedule watermarks are not on the immutable anchor grid",
				ErrLearningCycleIntegrity,
			)
		}
		record.LastScheduledFor = last
	} else if nextDueAt != schedule.FirstDueAtUnixMicros {
		return LearningCycleScheduleRecord{}, fmt.Errorf(
			"%w: untouched Schedule next_due_at differs from first_due_at",
			ErrLearningCycleIntegrity,
		)
	}
	return record, nil
}

func queryLearningCycleTask(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	taskID string,
) (LearningCycleTaskRecord, bool, error) {
	row := queryer.QueryRowContext(ctx, `
		SELECT
			task_id, tenant_id, workspace_id, schedule_id, schedule_digest,
			scheduled_for, request_digest, request_canonical,
			request_size_bytes, state, revision,
			run_id, run_manifest_digest, attempt_id, result_ref, proposal_id,
			created_at, updated_at
		FROM learning_cycle_tasks
		WHERE tenant_id=? AND task_id=?
	`, tenantID, taskID)
	record, err := scanLearningCycleTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LearningCycleTaskRecord{}, false, nil
	}
	if err != nil {
		return LearningCycleTaskRecord{}, false, err
	}
	return record, true, nil
}

func queryOpenLearningCycleTask(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	scheduleID string,
) (LearningCycleTaskRecord, bool, error) {
	row := queryer.QueryRowContext(ctx, `
		SELECT
			task_id, tenant_id, workspace_id, schedule_id, schedule_digest,
			scheduled_for, request_digest, request_canonical,
			request_size_bytes, state, revision,
			run_id, run_manifest_digest, attempt_id, result_ref, proposal_id,
			created_at, updated_at
		FROM learning_cycle_tasks
		WHERE tenant_id=? AND schedule_id=?
		  AND state IN ('PENDING', 'RUN_ADMITTED')
		ORDER BY scheduled_for, task_id COLLATE BINARY
		LIMIT 1
	`, tenantID, scheduleID)
	record, err := scanLearningCycleTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LearningCycleTaskRecord{}, false, nil
	}
	if err != nil {
		return LearningCycleTaskRecord{}, false, err
	}
	return record, true, nil
}

func scanLearningCycleTask(
	scanner learningCycleScanner,
) (LearningCycleTaskRecord, error) {
	var (
		taskID            string
		tenantID          string
		workspaceID       string
		scheduleID        string
		scheduleDigest    string
		scheduledFor      int64
		requestDigest     string
		requestCanonical  []byte
		requestSize       int64
		state             string
		revision          int64
		runID             sql.NullString
		runManifestDigest sql.NullString
		attemptID         sql.NullString
		resultRef         sql.NullString
		proposalID        sql.NullString
		createdAt         int64
		updatedAt         int64
	)
	if err := scanner.Scan(
		&taskID,
		&tenantID,
		&workspaceID,
		&scheduleID,
		&scheduleDigest,
		&scheduledFor,
		&requestDigest,
		&requestCanonical,
		&requestSize,
		&state,
		&revision,
		&runID,
		&runManifestDigest,
		&attemptID,
		&resultRef,
		&proposalID,
		&createdAt,
		&updatedAt,
	); err != nil {
		return LearningCycleTaskRecord{}, err
	}
	request, err := learningcontract.RestoreLearningCycleRequestV1(
		requestCanonical,
		requestDigest,
	)
	if err != nil || !validLeaseOpaqueID(workspaceID) || request.ScheduleDigest != scheduleDigest ||
		request.ScheduledForMicros != scheduledFor ||
		requestSize != int64(len(requestCanonical)) || revision < 0 || revision > 3 ||
		updatedAt < createdAt {
		return LearningCycleTaskRecord{}, fmt.Errorf(
			"%w: invalid task request or SQL projection: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	derivedTaskID, err := learningcontract.DeriveLearningCycleTaskIDV1(
		scheduleDigest,
		scheduledFor,
		requestDigest,
	)
	if err != nil || derivedTaskID != taskID {
		return LearningCycleTaskRecord{}, fmt.Errorf("%w: task identity mismatch", ErrLearningCycleIntegrity)
	}
	created, err := timeFromUnixMicro(createdAt)
	if err != nil {
		return LearningCycleTaskRecord{}, fmt.Errorf("%w: task created_at", ErrLearningCycleIntegrity)
	}
	updated, err := timeFromUnixMicro(updatedAt)
	if err != nil {
		return LearningCycleTaskRecord{}, fmt.Errorf("%w: task updated_at", ErrLearningCycleIntegrity)
	}
	record := LearningCycleTaskRecord{
		TaskID:            taskID,
		TenantID:          tenantID,
		WorkspaceID:       workspaceID,
		ScheduleID:        scheduleID,
		ScheduleDigest:    scheduleDigest,
		ScheduledFor:      time.UnixMicro(scheduledFor).UTC(),
		Request:           request,
		RequestCanonical:  bytes.Clone(requestCanonical),
		RequestDigest:     requestDigest,
		State:             learningcontract.LearningCycleTaskStateV1(state),
		Revision:          uint64(revision),
		RunID:             runID.String,
		RunManifestDigest: runManifestDigest.String,
		AttemptID:         attemptID.String,
		ResultRef:         resultRef.String,
		ProposalID:        proposalID.String,
		CreatedAt:         created,
		UpdatedAt:         updated,
	}
	if !validLearningCycleTaskProjection(record) {
		return LearningCycleTaskRecord{}, fmt.Errorf("%w: invalid task state/ref projection", ErrLearningCycleIntegrity)
	}
	return record, nil
}

func verifyLearningCyclePreparedAdmission(
	ctx context.Context,
	connection *sql.Conn,
	prepared preparedRunAdmission,
	schedule LearningCycleScheduleRecord,
	task LearningCycleTaskRecord,
) error {
	manifest := prepared.manifest
	member := prepared.member
	intent := prepared.intent
	executionIdentity, err := learningcontract.DeriveLearningCycleExecutionIdentityV1(
		task.TaskID,
		task.RequestDigest,
	)
	if err != nil {
		return fmt.Errorf(
			"%w: derive proposer execution identity: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if task.ScheduleDigest != schedule.ScheduleDigest ||
		task.ScheduleID != schedule.Schedule.ScheduleID ||
		intent.AdmissionKey != executionIdentity.AdmissionKey ||
		manifest.AdmissionKey != executionIdentity.AdmissionKey ||
		manifest.RunID != executionIdentity.RunID ||
		member.MemberID != executionIdentity.MemberID ||
		manifest.RecoveryRootRef != executionIdentity.RecoveryRootRef ||
		manifest.TenantID != schedule.Schedule.TenantID ||
		intent.TenantID != schedule.Schedule.TenantID ||
		intent.PrincipalID != schedule.Schedule.ServicePrincipalID ||
		intent.WorkspaceID != schedule.Schedule.WorkspaceID ||
		intent.AgentID != schedule.Schedule.AgentID ||
		intent.ProfileID != schedule.Schedule.ProfileID ||
		manifest.Workspace.ID != schedule.Schedule.WorkspaceID ||
		member.Workspace.ID != schedule.Schedule.WorkspaceID ||
		manifest.PrimaryAgent.ID != schedule.Schedule.AgentID ||
		member.Agent.ID != schedule.Schedule.AgentID ||
		member.Profile.ID != schedule.Schedule.ProfileID ||
		!intent.Deadline.Equal(time.UnixMicro(task.Request.WindowEndMicros).UTC()) ||
		!manifest.Deadline.Equal(time.UnixMicro(task.Request.WindowEndMicros).UTC()) ||
		manifest.Composite != nil || manifest.ConversationTurn != nil ||
		intent.ConversationTurn != nil ||
		manifest.ParentRunID != "" || len(manifest.Members) != 1 ||
		manifest.PrimaryMemberID != member.MemberID ||
		manifest.Members[0].MemberID != member.MemberID ||
		manifest.TaskInputRef != intent.TaskInputRef ||
		intent.ChannelEndpointID != "" || len(member.Actions) != 0 ||
		len(member.PortPlans) != 1 || len(member.PortPlans[0].Bindings) != 1 ||
		len(intent.RequestedPorts) != 1 {
		return fmt.Errorf("%w: proposer Run is not exact standalone model-only scope", ErrInvalidLearningCycle)
	}
	plan := member.PortPlans[0]
	if plan.Port.Name != moduleapi.PortNameModelGenerate ||
		plan.Port.ExactVersion != moduleapi.PortVersionV2 ||
		plan.Bindings[0].FailurePolicy != moduleapi.FailureRequired ||
		intent.RequestedPorts[0] != plan.Port {
		return fmt.Errorf("%w: proposer requires another or optional Port", ErrInvalidLearningCycle)
	}
	taskContent, found := prepared.contents[manifest.TaskInputRef]
	if !found || taskContent.Kind != ContentTaskInput ||
		taskContent.MediaType != admissionJSONMediaType {
		return fmt.Errorf("%w: exact cycle TASK_INPUT is absent", ErrInvalidLearningCycle)
	}
	taskInput, err := corecontract.RestoreTaskInputV1(taskContent.CanonicalBytes)
	if err != nil || !bytes.Equal([]byte(taskInput.Text), task.RequestCanonical) {
		return fmt.Errorf("%w: TASK_INPUT does not carry the exact cycle request", ErrInvalidLearningCycle)
	}
	restored, err := learningcontract.RestoreLearningCycleRequestV1(
		[]byte(taskInput.Text),
		task.RequestDigest,
	)
	if err != nil || restored != task.Request {
		return fmt.Errorf("%w: restore exact cycle request: %v", ErrInvalidLearningCycle, err)
	}
	expected, expectedCanonical, expectedDigest, err :=
		learningcontract.NewLearningCycleRequestV1(
			schedule.Schedule,
			schedule.ScheduleCanonical,
			schedule.ScheduleDigest,
			task.ScheduledFor.UnixMicro(),
		)
	if err != nil || expected != restored || expectedDigest != task.RequestDigest ||
		!bytes.Equal(expectedCanonical, task.RequestCanonical) {
		return fmt.Errorf("%w: request differs from immutable Schedule/window: %v", ErrLearningCycleIntegrity, err)
	}
	configContent, err := prospectiveAdmissionContent(
		ctx,
		connection,
		prepared.contents,
		plan.Bindings[0].ConfigRef,
	)
	if err != nil || configContent.Kind != ContentConfig ||
		configContent.MediaType != admissionJSONMediaType {
		return fmt.Errorf("%w: proposer model config: %v", ErrInvalidLearningCycle, err)
	}
	config, err := moduleapi.RestoreModelBindingConfigV2(configContent.CanonicalBytes)
	if err != nil {
		return fmt.Errorf("%w: proposer model config: %v", ErrInvalidLearningCycle, err)
	}
	if err := verifyLearningCycleMaxTokens(
		config.Parameters,
		restored.MaxOutputTokens,
	); err != nil {
		return fmt.Errorf(
			"%w: proposer max_tokens must be explicit and bounded by %d: %v",
			ErrInvalidLearningCycle,
			restored.MaxOutputTokens,
			err,
		)
	}
	return nil
}

func verifyLearningCycleMaxTokens(
	parameters json.RawMessage,
	ceiling uint32,
) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(parameters, &object); err != nil || object == nil {
		return errors.New("model parameters must be a canonical JSON object")
	}
	raw, found := object["max_tokens"]
	if !found || len(raw) == 0 {
		return errors.New("max_tokens is absent")
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return errors.New("max_tokens is not a positive integer")
		}
	}
	value, err := strconv.ParseUint(string(raw), 10, 32)
	if err != nil || value == 0 || value > uint64(ceiling) {
		return errors.New("max_tokens is zero, overflows, or exceeds the request")
	}
	return nil
}

type learningCycleTerminalProjection struct {
	State      learningcontract.LearningCycleTaskStateV1
	AttemptID  string
	ResultRef  string
	ProposalID string
}

func deriveLearningCycleTerminalProjection(
	ctx context.Context,
	connection *sql.Conn,
	schedule LearningCycleScheduleRecord,
	task LearningCycleTaskRecord,
) (learningCycleTerminalProjection, error) {
	var attemptCount int
	var attemptID string
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MIN(attempt_id), '')
		FROM model_dispatch_attempts WHERE run_id=?
	`, task.RunID).Scan(&attemptCount, &attemptID); err != nil {
		return learningCycleTerminalProjection{}, fmt.Errorf(
			"%w: enumerate proposer Attempts: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if attemptCount == 0 {
		return learningCycleTerminalProjection{}, ErrLearningCycleNotReady
	}
	if attemptCount != 1 || !validLeaseOpaqueID(attemptID) {
		return learningCycleTerminalProjection{}, fmt.Errorf(
			"%w: proposer Run has %d Model Attempts",
			ErrLearningCycleIntegrity,
			attemptCount,
		)
	}
	attempt, err := queryModelDispatchAttempt(ctx, connection, attemptID)
	if err != nil {
		return learningCycleTerminalProjection{}, fmt.Errorf(
			"%w: load proposer Attempt: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if attempt.RunID != task.RunID ||
		attempt.LogicalStepID != corecontract.PureChatModelLogicalStepIDV1 ||
		attempt.SourceDispatchAttemptID != "" {
		return learningCycleTerminalProjection{}, fmt.Errorf(
			"%w: Attempt differs from the task's model-only Run",
			ErrLearningCycleIntegrity,
		)
	}
	var dispatchCount int
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?
	`, task.RunID).Scan(&dispatchCount); err != nil || dispatchCount != 0 {
		return learningCycleTerminalProjection{}, fmt.Errorf(
			"%w: proposer Run has Action or Channel dispatch: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	manifest, member, err := loadLearningCycleRunClosure(
		ctx,
		connection,
		schedule,
		task,
		attempt,
	)
	if err != nil {
		return learningCycleTerminalProjection{}, err
	}
	reconciled := task.State == learningcontract.LearningCycleTaskUnknownV1
	if reconciled {
		if task.Revision != 2 || task.AttemptID != attempt.AttemptID {
			return learningCycleTerminalProjection{}, fmt.Errorf(
				"%w: UNKNOWN does not bind the same reconciled Attempt",
				ErrLearningCycleIntegrity,
			)
		}
		if attempt.State == corecontract.ModelAttemptUnknown {
			return learningCycleTerminalProjection{
				State:     learningcontract.LearningCycleTaskUnknownV1,
				AttemptID: attempt.AttemptID,
			}, nil
		}
		if attempt.ReconciliationEvidenceRef == "" {
			return learningCycleTerminalProjection{}, fmt.Errorf(
				"%w: reconciled Attempt has no evidence",
				ErrLearningCycleIntegrity,
			)
		}
	}

	projection := learningCycleTerminalProjection{AttemptID: attempt.AttemptID}
	switch attempt.State {
	case corecontract.ModelAttemptPending:
		return learningCycleTerminalProjection{}, ErrLearningCycleNotReady
	case corecontract.ModelAttemptUnknown:
		projection.State = learningcontract.LearningCycleTaskUnknownV1
		return projection, nil
	case corecontract.ModelAttemptFailed:
		if err := requireLearningCycleTerminal(
			ctx,
			connection,
			task.RunID,
			attempt,
		); err != nil {
			return learningCycleTerminalProjection{}, err
		}
		projection.State = learningcontract.LearningCycleTaskFailedV1
		return projection, nil
	case corecontract.ModelAttemptSucceeded:
		if err := requireLearningCycleTerminal(
			ctx,
			connection,
			task.RunID,
			attempt,
		); err != nil {
			return learningCycleTerminalProjection{}, err
		}
		if attempt.ResultRef == "" || !moduleapi.ValidSHA256(attempt.ResultRef) {
			return learningCycleTerminalProjection{}, fmt.Errorf(
				"%w: successful proposer Attempt lacks MODEL_RESULT",
				ErrLearningCycleIntegrity,
			)
		}
	default:
		return learningCycleTerminalProjection{}, fmt.Errorf(
			"%w: unsupported proposer Attempt state %q",
			ErrLearningCycleIntegrity,
			attempt.State,
		)
	}
	projection.ResultRef = attempt.ResultRef
	cycleResult, valid, err := restoreLearningCycleModelResult(
		ctx,
		connection,
		task,
		attempt.ResultRef,
	)
	if err != nil {
		return learningCycleTerminalProjection{}, err
	}
	if !valid {
		projection.State = learningcontract.LearningCycleTaskInvalidResultV1
		return projection, nil
	}
	if cycleResult.Decision == learningcontract.LearningCycleResultNoChangeV1 {
		projection.State = learningcontract.LearningCycleTaskNoChangeV1
		return projection, nil
	}
	state, proposalID, err := admitLearningCycleProposal(
		ctx,
		connection,
		schedule,
		task,
		attempt,
		manifest,
		member,
		cycleResult,
	)
	if err != nil {
		return learningCycleTerminalProjection{}, err
	}
	projection.State = state
	projection.ProposalID = proposalID
	return projection, nil
}

func requireLearningCycleTerminal(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	attempt ModelDispatchAttemptRecord,
) error {
	terminal, err := loadTerminalRunResult(ctx, connection, runID)
	if err != nil || terminal.RunID != runID ||
		terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.AttemptID != attempt.AttemptID ||
		terminal.MemberID != attempt.MemberID || terminal.State != attempt.State ||
		terminal.ModelState != attempt.State {
		return fmt.Errorf(
			"%w: proposer Run terminal does not bind exact Attempt: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	return nil
}

func loadLearningCycleRunClosure(
	ctx context.Context,
	connection *sql.Conn,
	schedule LearningCycleScheduleRecord,
	task LearningCycleTaskRecord,
	attempt ModelDispatchAttemptRecord,
) (corecontract.RunManifest, corecontract.MemberExecutionSnapshot, error) {
	var manifestCanonical, memberCanonical []byte
	var manifestDigest, memberDigest string
	err := connection.QueryRowContext(ctx, `
		SELECT manifest.canonical_json, manifest.digest,
			member.canonical_json, member.digest
		FROM run_manifests AS manifest
		JOIN member_execution_snapshots AS member
		  ON member.run_id=manifest.run_id AND member.member_id=?
		WHERE manifest.run_id=?
	`, attempt.MemberID, task.RunID).Scan(
		&manifestCanonical,
		&manifestDigest,
		&memberCanonical,
		&memberDigest,
	)
	if err != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: load proposer Run closure: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: restore proposer Manifest: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: restore proposer Member: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if manifestDigest != task.RunManifestDigest ||
		manifest.ManifestDigest != task.RunManifestDigest ||
		manifest.RunID != task.RunID || manifest.TenantID != task.TenantID ||
		manifest.Workspace.ID != schedule.Schedule.WorkspaceID ||
		manifest.PrimaryAgent.ID != schedule.Schedule.AgentID ||
		manifest.PrimaryMemberID != attempt.MemberID || len(manifest.Members) != 1 ||
		manifest.Members[0].MemberID != attempt.MemberID ||
		manifest.Members[0].Digest != memberDigest ||
		member.MemberSnapshotDigest != memberDigest ||
		member.MemberID != attempt.MemberID ||
		member.Agent.ID != schedule.Schedule.AgentID ||
		member.Profile.ID != schedule.Schedule.ProfileID ||
		member.Workspace.ID != schedule.Schedule.WorkspaceID ||
		attempt.MemberSnapshotDigest != memberDigest || len(member.Actions) != 0 ||
		len(member.PortPlans) != 1 || len(member.PortPlans[0].Bindings) != 1 {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: proposer Run closure differs from Schedule or Attempt",
			ErrLearningCycleIntegrity,
		)
	}
	plan := member.PortPlans[0]
	binding := plan.Bindings[0]
	if plan.Port.Name != moduleapi.PortNameModelGenerate ||
		plan.Port.ExactVersion != moduleapi.PortVersionV2 ||
		binding.FailurePolicy != moduleapi.FailureRequired ||
		attempt.Binding.Provider != binding.Provider ||
		attempt.Binding.ConfigRef != binding.ConfigRef ||
		attempt.Binding.AuthorityCeilingRef != binding.AuthorityCeilingRef ||
		attempt.Binding.FailurePolicy != binding.FailurePolicy ||
		!equalLearningCycleRefs(
			attempt.Binding.StaticContextRefs,
			binding.StaticContextRefs,
		) {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: proposer Attempt binding differs from frozen Member",
			ErrLearningCycleIntegrity,
		)
	}
	return manifest, member, nil
}

func restoreLearningCycleModelResult(
	ctx context.Context,
	connection readQueryerV1,
	task LearningCycleTaskRecord,
	resultRef string,
) (learningcontract.LearningCycleResultV1, bool, error) {
	content, err := queryContent(ctx, connection, resultRef)
	if err != nil || content.Kind != ContentModelResult ||
		content.MediaType != admissionJSONMediaType {
		return learningcontract.LearningCycleResultV1{}, false, fmt.Errorf(
			"%w: task MODEL_RESULT: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(content.CanonicalBytes)
	if err != nil {
		return learningcontract.LearningCycleResultV1{}, false, fmt.Errorf(
			"%w: restore task MODEL_RESULT: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if output.ActionRequest != nil || output.AssistantText == "" {
		return learningcontract.LearningCycleResultV1{}, false, nil
	}
	cycleResult, _, _, err := learningcontract.ParseLearningCycleResultV1(
		[]byte(output.AssistantText),
	)
	if err != nil || cycleResult.ValidateForRequestV1(
		task.Request,
		task.RequestDigest,
	) != nil {
		return learningcontract.LearningCycleResultV1{}, false, nil
	}
	return cycleResult, true, nil
}

func admitLearningCycleProposal(
	ctx context.Context,
	connection *sql.Conn,
	schedule LearningCycleScheduleRecord,
	task LearningCycleTaskRecord,
	attempt ModelDispatchAttemptRecord,
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
	cycleResult learningcontract.LearningCycleResultV1,
) (learningcontract.LearningCycleTaskStateV1, string, error) {
	candidate, err := buildLearningCycleProposalCandidate(
		schedule,
		task,
		attempt,
		manifest,
		member,
		cycleResult,
	)
	if err != nil {
		return "", "", err
	}
	proposal := candidate.Proposal
	proposalCanonical := candidate.ProposalCanonical
	draftCanonical := candidate.DraftCanonical
	proposalID := candidate.ProposalID
	existing, found, err := queryLearningProposalByID(ctx, connection, proposalID)
	if err != nil {
		return "", "", err
	}
	if found {
		if !bytes.Equal(existing.ProposalCanonical, proposalCanonical) ||
			!bytes.Equal(existing.DraftCanonical, draftCanonical) ||
			existing.ProposerAttemptID != attempt.AttemptID {
			return "", "", fmt.Errorf(
				"%w: exact cycle Proposal identity changed",
				ErrLearningCycleIntegrity,
			)
		}
		provedAttemptID, err := proveLearningProposerLineage(
			ctx,
			connection,
			existing.Proposal,
		)
		if err != nil || provedAttemptID != attempt.AttemptID {
			return "", "", fmt.Errorf(
				"%w: exact cycle Proposal lineage: %v",
				ErrLearningCycleIntegrity,
				err,
			)
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceLearningProposalV1, proposalID,
		); err != nil {
			return "", "", err
		}
		return learningcontract.LearningCycleTaskProposalSubmittedV1, proposalID, nil
	}
	provedAttemptID, err := proveLearningProposerLineage(ctx, connection, proposal)
	if err != nil || provedAttemptID != attempt.AttemptID {
		return "", "", fmt.Errorf(
			"%w: cycle Proposal lineage: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	duplicateChecks := []struct {
		axis  string
		state learningcontract.LearningCycleTaskStateV1
		value string
	}{
		{"source_fingerprint", learningcontract.LearningCycleTaskSourceOccupiedV1, proposal.SourceFingerprint},
		{"content_fingerprint", learningcontract.LearningCycleTaskContentOccupiedV1, proposal.ContentFingerprint},
	}
	for _, check := range duplicateChecks {
		duplicateID, found, err := queryLearningDuplicateID(
			ctx,
			connection,
			check.axis,
			proposal.TenantID,
			string(proposal.Kind),
			check.value,
		)
		if err != nil {
			return "", "", err
		}
		if found {
			if err := verifyLearningCycleDuplicate(
				ctx,
				connection,
				proposal,
				duplicateID,
				check.state,
			); err != nil {
				return "", "", err
			}
			if err := verifyCurrentOverviewResourceObservationV1(
				ctx, connection, overviewResourceLearningProposalV1, duplicateID,
			); err != nil {
				return "", "", err
			}
			return check.state, duplicateID, nil
		}
	}
	duplicateID, found, err := queryLearningTargetDuplicateID(
		ctx,
		connection,
		proposal,
	)
	if err != nil {
		return "", "", err
	}
	if found {
		if err := verifyLearningCycleDuplicate(
			ctx,
			connection,
			proposal,
			duplicateID,
			learningcontract.LearningCycleTaskTargetOccupiedV1,
		); err != nil {
			return "", "", err
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceLearningProposalV1, duplicateID,
		); err != nil {
			return "", "", err
		}
		return learningcontract.LearningCycleTaskTargetOccupiedV1, duplicateID, nil
	}
	createdAt := nowUnixMicro()
	if createdAt <= 0 {
		return "", "", fmt.Errorf("%w: invalid Proposal creation time", ErrLearningCycleIntegrity)
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO learning_proposals(
			proposal_id, tenant_id, workspace_id, proposal_kind,
			source_fingerprint, content_fingerprint, draft_digest,
			target_id, target_version,
			proposal_canonical, proposal_size_bytes,
			draft_canonical, draft_size_bytes,
			proposer_run_id, proposer_manifest_digest,
			proposer_member_id, proposer_member_digest,
			proposer_attempt_id, proposer_result_ref,
			state, revision, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
	`,
		proposalID,
		proposal.TenantID,
		proposal.Workspace.ID,
		string(proposal.Kind),
		proposal.SourceFingerprint,
		proposal.ContentFingerprint,
		proposal.DraftDigest,
		proposal.Target.ID,
		proposal.Target.Version,
		proposalCanonical,
		len(proposalCanonical),
		draftCanonical,
		len(draftCanonical),
		proposal.ProposerRunID,
		proposal.ProposerManifestDigest,
		proposal.ProposerMember.MemberID,
		proposal.ProposerMember.Digest,
		attempt.AttemptID,
		proposal.ProposerResultRef,
		string(LearningProposalSubmitted),
		createdAt,
		createdAt,
	); err != nil {
		return "", "", fmt.Errorf("currentstore: insert cycle Proposal: %w", err)
	}
	if err := appendProposalResourceObservationV1(
		ctx, connection, proposalID, overviewTransitionProposalSubmitV1,
	); err != nil {
		return "", "", err
	}
	stored, found, err := queryLearningProposalByID(ctx, connection, proposalID)
	if err != nil || !found || stored.ProposerAttemptID != attempt.AttemptID ||
		!bytes.Equal(stored.ProposalCanonical, proposalCanonical) ||
		!bytes.Equal(stored.DraftCanonical, draftCanonical) {
		return "", "", fmt.Errorf(
			"%w: inserted cycle Proposal did not round-trip: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	return learningcontract.LearningCycleTaskProposalSubmittedV1, proposalID, nil
}

type learningCycleProposalCandidate struct {
	Proposal          learningcontract.ProposalV1
	ProposalCanonical []byte
	DraftCanonical    []byte
	ProposalID        string
}

func buildLearningCycleProposalCandidate(
	schedule LearningCycleScheduleRecord,
	task LearningCycleTaskRecord,
	attempt ModelDispatchAttemptRecord,
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
	cycleResult learningcontract.LearningCycleResultV1,
) (learningCycleProposalCandidate, error) {
	draftCanonical, err := buildLearningCycleDraft(
		schedule,
		task,
		attempt.ResultRef,
		cycleResult,
	)
	if err != nil {
		return learningCycleProposalCandidate{}, err
	}
	proposal, proposalCanonical, proposalID, err := learningcontract.NewProposalV1(
		learningcontract.ProposalV1{
			SchemaVersion:          learningcontract.ProposalSchemaVersionV1,
			Kind:                   task.Request.Kind,
			TenantID:               task.TenantID,
			Workspace:              manifest.Workspace,
			ProposerAgent:          manifest.PrimaryAgent,
			ProposerProfile:        member.Profile,
			ProposerRunID:          task.RunID,
			ProposerManifestDigest: task.RunManifestDigest,
			ProposerMember:         manifest.Members[0],
			ProposerResultRef:      attempt.ResultRef,
			Target:                 task.Request.Target,
		},
		draftCanonical,
		learningcontract.SourceEvidenceV1{
			Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
		},
	)
	if err != nil {
		return learningCycleProposalCandidate{}, fmt.Errorf(
			"%w: derive cycle Proposal: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	return learningCycleProposalCandidate{
		Proposal:          proposal,
		ProposalCanonical: bytes.Clone(proposalCanonical),
		DraftCanonical:    bytes.Clone(draftCanonical),
		ProposalID:        proposalID,
	}, nil
}

func buildLearningCycleDraft(
	schedule LearningCycleScheduleRecord,
	task LearningCycleTaskRecord,
	resultRef string,
	cycleResult learningcontract.LearningCycleResultV1,
) ([]byte, error) {
	switch task.Request.Kind {
	case learningcontract.ProposalKindKnowledgeV1:
		document := moduleapi.KnowledgeDocumentRefV1{
			ID:      task.Request.Target.ID,
			Version: task.Request.Target.Version,
			Digest:  resultRef,
		}
		chunks := make([]moduleapi.KnowledgeChunkV1, len(cycleResult.KnowledgeChunks))
		for index, text := range cycleResult.KnowledgeChunks {
			chunks[index] = moduleapi.KnowledgeChunkV1{
				Document:  document,
				ChunkID:   fmt.Sprintf("cycle-chunk-%06d", index+1),
				Text:      text,
				VisibleTo: append([]moduleapi.KnowledgeScopeRuleV1(nil), schedule.Schedule.KnowledgeVisibility...),
			}
		}
		_, canonical, ref, err := moduleapi.NewKnowledgeSourceV1(
			moduleapi.KnowledgeSourceV1{
				SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
				ID:            task.Request.Target.ID,
				Version:       task.Request.Target.Version,
				Chunks:        chunks,
			},
		)
		if err != nil || ref.ID != task.Request.Target.ID ||
			ref.Version != task.Request.Target.Version {
			return nil, fmt.Errorf(
				"%w: rebuild cycle Knowledge draft: %v",
				ErrLearningCycleIntegrity,
				err,
			)
		}
		return canonical, nil
	case learningcontract.ProposalKindSkillV1:
		_, canonical, err := corecontract.NewStaticContextV1(
			corecontract.StaticContextV1{
				SchemaVersion: corecontract.StaticContextSchemaVersionV1,
				Text:          cycleResult.SkillText,
			},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: rebuild cycle static Skill draft: %v",
				ErrLearningCycleIntegrity,
				err,
			)
		}
		return canonical, nil
	default:
		return nil, fmt.Errorf(
			"%w: unsupported cycle Proposal kind %q",
			ErrLearningCycleIntegrity,
			task.Request.Kind,
		)
	}
}

func verifyLearningCycleDuplicate(
	ctx context.Context,
	connection readQueryerV1,
	candidate learningcontract.ProposalV1,
	proposalID string,
	state learningcontract.LearningCycleTaskStateV1,
) error {
	existing, found, err := queryLearningProposalByID(ctx, connection, proposalID)
	if err != nil || !found {
		return fmt.Errorf("%w: occupied Proposal is absent: %v", ErrLearningCycleIntegrity, err)
	}
	if existing.Proposal.TenantID != candidate.TenantID ||
		existing.Proposal.Kind != candidate.Kind {
		return fmt.Errorf("%w: occupied Proposal crosses tenant or kind", ErrLearningCycleIntegrity)
	}
	matched := false
	switch state {
	case learningcontract.LearningCycleTaskSourceOccupiedV1:
		matched = existing.Proposal.SourceFingerprint == candidate.SourceFingerprint
	case learningcontract.LearningCycleTaskContentOccupiedV1:
		matched = existing.Proposal.ContentFingerprint == candidate.ContentFingerprint
	case learningcontract.LearningCycleTaskTargetOccupiedV1:
		matched = existing.Proposal.Target == candidate.Target
	}
	if !matched {
		return fmt.Errorf("%w: occupied Proposal does not prove exact duplicate axis", ErrLearningCycleIntegrity)
	}
	attemptID, err := proveLearningProposerLineage(ctx, connection, existing.Proposal)
	if err != nil || attemptID != existing.ProposerAttemptID {
		return fmt.Errorf("%w: occupied Proposal lineage: %v", ErrLearningCycleIntegrity, err)
	}
	return nil
}

func equalLearningCycleRefs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validLearningCycleTaskProjection(record LearningCycleTaskRecord) bool {
	hasRun := record.RunID != "" && record.RunManifestDigest != ""
	hasAttempt := record.AttemptID != ""
	hasResult := record.ResultRef != ""
	hasProposal := record.ProposalID != ""
	switch record.State {
	case learningcontract.LearningCycleTaskPendingV1:
		return record.Revision == 0 && !hasRun && !hasAttempt && !hasResult &&
			!hasProposal && record.UpdatedAt.Equal(record.CreatedAt)
	case learningcontract.LearningCycleTaskRunAdmittedV1:
		return record.Revision == 1 && hasRun && !hasAttempt && !hasResult && !hasProposal
	case learningcontract.LearningCycleTaskUnknownV1:
		return record.Revision == 2 && hasRun && hasAttempt && !hasResult && !hasProposal
	case learningcontract.LearningCycleTaskFailedV1:
		return (record.Revision == 2 || record.Revision == 3) && hasRun &&
			hasAttempt && !hasResult && !hasProposal
	case learningcontract.LearningCycleTaskProposalSubmittedV1:
		return (record.Revision == 2 || record.Revision == 3) && hasRun &&
			hasAttempt && hasResult && hasProposal
	case learningcontract.LearningCycleTaskNoChangeV1,
		learningcontract.LearningCycleTaskInvalidResultV1:
		return (record.Revision == 2 || record.Revision == 3) && hasRun &&
			hasAttempt && hasResult && !hasProposal
	case learningcontract.LearningCycleTaskSourceOccupiedV1,
		learningcontract.LearningCycleTaskContentOccupiedV1,
		learningcontract.LearningCycleTaskTargetOccupiedV1:
		return (record.Revision == 2 || record.Revision == 3) && hasRun &&
			hasAttempt && hasResult && hasProposal
	default:
		return false
	}
}

func learningCycleClosedState(
	state learningcontract.LearningCycleTaskStateV1,
) bool {
	switch state {
	case learningcontract.LearningCycleTaskProposalSubmittedV1,
		learningcontract.LearningCycleTaskNoChangeV1,
		learningcontract.LearningCycleTaskSourceOccupiedV1,
		learningcontract.LearningCycleTaskContentOccupiedV1,
		learningcontract.LearningCycleTaskTargetOccupiedV1,
		learningcontract.LearningCycleTaskFailedV1,
		learningcontract.LearningCycleTaskInvalidResultV1:
		return true
	default:
		return false
	}
}

func learningCycleIntervalMicros(
	schedule learningcontract.LearningCycleScheduleV1,
) (int64, error) {
	if schedule.IntervalSeconds == 0 ||
		schedule.IntervalSeconds > uint64(math.MaxInt64/1_000_000) {
		return 0, fmt.Errorf("%w: Schedule interval overflows", ErrLearningCycleIntegrity)
	}
	return int64(schedule.IntervalSeconds * 1_000_000), nil
}

func learningCycleTransitionMicros(previous int64) int64 {
	now := nowUnixMicro()
	if now <= previous {
		return previous + 1
	}
	return now
}

func requireLearningCycleCASRow(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("currentstore: inspect Learning cycle %s CAS: %w", operation, err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: Learning cycle %s affected %d rows", ErrLearningCycleConflict, operation, affected)
	}
	return nil
}

func detachLearningCycleScheduleRecord(
	record LearningCycleScheduleRecord,
) LearningCycleScheduleRecord {
	record.ScheduleCanonical = bytes.Clone(record.ScheduleCanonical)
	record.Schedule.KnowledgeVisibility = append(
		[]moduleapi.KnowledgeScopeRuleV1(nil),
		record.Schedule.KnowledgeVisibility...,
	)
	if record.Schedule.KnowledgeVisibility == nil {
		record.Schedule.KnowledgeVisibility = []moduleapi.KnowledgeScopeRuleV1{}
	}
	return record
}

func detachLearningCycleTaskRecord(
	record LearningCycleTaskRecord,
) LearningCycleTaskRecord {
	record.RequestCanonical = bytes.Clone(record.RequestCanonical)
	return record
}

func boolToSQLite(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableLearningCycleRef(value string) any {
	if value == "" {
		return nil
	}
	return value
}
