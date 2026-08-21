package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// VerifyLearningCycleSemanticClosureV1 validates every Learning-cycle fact in
// one caller-owned read transaction. It accepts the durable crash windows
// before Attempt creation, before task projection, and after evidence-backed
// UNKNOWN reconciliation. It performs no write, Proposal admission, model,
// Loop, module, Provider, Gateway, network, Secret, or external-effect call.
func VerifyLearningCycleSemanticClosureV1(
	ctx context.Context,
	connection *sql.Conn,
) error {
	if ctx == nil || connection == nil {
		return fmt.Errorf(
			"%w: semantic verifier requires context and connection",
			ErrLearningCycleIntegrity,
		)
	}
	schedules, err := loadAllLearningCycleSchedulesForSemanticVerification(ctx, connection)
	if err != nil {
		return err
	}
	taskIdentities, err := loadAllLearningCycleTaskIDsForSemanticVerification(ctx, connection)
	if err != nil {
		return err
	}
	taskCount := make(map[string]int, len(schedules))
	latestWindow := make(map[string]int64, len(schedules))
	for _, identity := range taskIdentities {
		task, found, err := queryLearningCycleTask(
			ctx,
			connection,
			identity.tenantID,
			identity.taskID,
		)
		if err != nil || !found {
			return fmt.Errorf(
				"%w: load Task %s: %v",
				ErrLearningCycleIntegrity,
				identity.taskID,
				err,
			)
		}
		key := learningCycleScheduleSemanticKey(task.TenantID, task.ScheduleID)
		schedule, found := schedules[key]
		if !found || task.ScheduleDigest != schedule.ScheduleDigest {
			return fmt.Errorf(
				"%w: Task %s has no exact Schedule",
				ErrLearningCycleIntegrity,
				task.TaskID,
			)
		}
		if task.CreatedAt.Before(schedule.CreatedAt) {
			return fmt.Errorf(
				"%w: Task %s predates its Schedule",
				ErrLearningCycleIntegrity,
				task.TaskID,
			)
		}
		if err := verifyLearningCycleTaskRequestClosure(schedule, task); err != nil {
			return err
		}
		if err := verifyLearningCycleTaskExecutionClosure(ctx, connection, schedule, task); err != nil {
			return err
		}
		taskCount[key]++
		if task.ScheduledFor.UnixMicro() > latestWindow[key] {
			latestWindow[key] = task.ScheduledFor.UnixMicro()
		}
	}
	for key, schedule := range schedules {
		if taskCount[key] == 0 {
			if !schedule.LastScheduledFor.IsZero() {
				return fmt.Errorf(
					"%w: Schedule %s has a watermark without a Task",
					ErrLearningCycleIntegrity,
					schedule.Schedule.ScheduleID,
				)
			}
			continue
		}
		if schedule.LastScheduledFor.IsZero() ||
			schedule.LastScheduledFor.UnixMicro() != latestWindow[key] {
			return fmt.Errorf(
				"%w: Schedule %s watermark does not bind its latest Task",
				ErrLearningCycleIntegrity,
				schedule.Schedule.ScheduleID,
			)
		}
	}
	return nil
}

func loadAllLearningCycleSchedulesForSemanticVerification(
	ctx context.Context,
	connection *sql.Conn,
) (map[string]LearningCycleScheduleRecord, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT tenant_id, schedule_id
		FROM learning_cycle_schedules
		ORDER BY tenant_id COLLATE BINARY, schedule_id COLLATE BINARY
	`)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: enumerate Schedules: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	type scheduleIdentity struct{ tenantID, scheduleID string }
	var identities []scheduleIdentity
	for rows.Next() {
		var identity scheduleIdentity
		if err := rows.Scan(&identity.tenantID, &identity.scheduleID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf(
				"%w: scan Schedule identity: %v",
				ErrLearningCycleIntegrity,
				err,
			)
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf(
			"%w: iterate Schedule identities: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf(
			"%w: close Schedule identities: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	schedules := make(map[string]LearningCycleScheduleRecord, len(identities))
	for _, identity := range identities {
		schedule, found, err := queryLearningCycleSchedule(
			ctx,
			connection,
			identity.tenantID,
			identity.scheduleID,
		)
		if err != nil || !found {
			return nil, fmt.Errorf(
				"%w: load Schedule %s/%s: %v",
				ErrLearningCycleIntegrity,
				identity.tenantID,
				identity.scheduleID,
				err,
			)
		}
		key := learningCycleScheduleSemanticKey(identity.tenantID, identity.scheduleID)
		if _, duplicate := schedules[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate Schedule identity", ErrLearningCycleIntegrity)
		}
		schedules[key] = schedule
	}
	return schedules, nil
}

type learningCycleTaskSemanticIdentity struct {
	tenantID string
	taskID   string
}

func loadAllLearningCycleTaskIDsForSemanticVerification(
	ctx context.Context,
	connection *sql.Conn,
) ([]learningCycleTaskSemanticIdentity, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT tenant_id, task_id
		FROM learning_cycle_tasks
		ORDER BY tenant_id COLLATE BINARY, schedule_id COLLATE BINARY,
			scheduled_for, task_id COLLATE BINARY
	`)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: enumerate Tasks: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	var identities []learningCycleTaskSemanticIdentity
	for rows.Next() {
		var identity learningCycleTaskSemanticIdentity
		if err := rows.Scan(&identity.tenantID, &identity.taskID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf(
				"%w: scan Task identity: %v",
				ErrLearningCycleIntegrity,
				err,
			)
		}
		if !validLeaseOpaqueID(identity.tenantID) || !moduleapi.ValidSHA256(identity.taskID) {
			_ = rows.Close()
			return nil, fmt.Errorf("%w: invalid TaskID", ErrLearningCycleIntegrity)
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf(
			"%w: iterate Task identities: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf(
			"%w: close Task identities: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	return identities, nil
}

func learningCycleScheduleSemanticKey(tenantID, scheduleID string) string {
	return tenantID + "\x00" + scheduleID
}

func verifyLearningCycleTaskRequestClosure(
	schedule LearningCycleScheduleRecord,
	task LearningCycleTaskRecord,
) error {
	if task.WorkspaceID != schedule.Schedule.WorkspaceID {
		return fmt.Errorf(
			"%w: Task %s Workspace differs from its Schedule",
			ErrLearningCycleIntegrity,
			task.TaskID,
		)
	}
	expected, canonical, digest, err := learningcontract.NewLearningCycleRequestV1(
		schedule.Schedule,
		schedule.ScheduleCanonical,
		schedule.ScheduleDigest,
		task.ScheduledFor.UnixMicro(),
	)
	if err != nil || expected != task.Request || digest != task.RequestDigest ||
		!bytes.Equal(canonical, task.RequestCanonical) {
		return fmt.Errorf(
			"%w: Task %s Request differs from its immutable Schedule/window: %v",
			ErrLearningCycleIntegrity,
			task.TaskID,
			err,
		)
	}
	derivedTaskID, err := learningcontract.DeriveLearningCycleTaskIDV1(
		schedule.ScheduleDigest,
		task.ScheduledFor.UnixMicro(),
		task.RequestDigest,
	)
	if err != nil || derivedTaskID != task.TaskID {
		return fmt.Errorf(
			"%w: Task %s identity is not derived from Schedule/Request",
			ErrLearningCycleIntegrity,
			task.TaskID,
		)
	}
	return nil
}

func verifyLearningCycleTaskExecutionClosure(
	ctx context.Context,
	connection readQueryerV1,
	schedule LearningCycleScheduleRecord,
	task LearningCycleTaskRecord,
) error {
	identity, err := learningcontract.DeriveLearningCycleExecutionIdentityV1(
		task.TaskID,
		task.RequestDigest,
	)
	if err != nil {
		return fmt.Errorf("%w: derive Task execution identity: %v", ErrLearningCycleIntegrity, err)
	}
	if task.State == learningcontract.LearningCycleTaskPendingV1 {
		var count int
		if err := connection.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM runs
			WHERE run_id=? OR (tenant_id=? AND admission_key=?)
		`, identity.RunID, task.TenantID, identity.AdmissionKey).Scan(&count); err != nil {
			return fmt.Errorf("%w: inspect PENDING Admission: %v", ErrLearningCycleIntegrity, err)
		}
		if count != 0 {
			return fmt.Errorf(
				"%w: PENDING Task %s has an occupied deterministic Admission",
				ErrLearningCycleIntegrity,
				task.TaskID,
			)
		}
		return nil
	}
	manifest, member, err := verifyLearningCyclePersistedRunClosure(
		ctx,
		connection,
		schedule,
		task,
		identity,
	)
	if err != nil {
		return err
	}
	attempt, found, err := loadSoleLearningCycleAttempt(ctx, connection, task.RunID)
	if err != nil {
		return err
	}
	if !found {
		if task.State == learningcontract.LearningCycleTaskRunAdmittedV1 {
			return nil
		}
		return fmt.Errorf(
			"%w: Task %s state %s has no authoritative Attempt",
			ErrLearningCycleIntegrity,
			task.TaskID,
			task.State,
		)
	}
	if err := verifyLearningCycleAttemptClosure(task, manifest, member, attempt); err != nil {
		return err
	}
	switch task.State {
	case learningcontract.LearningCycleTaskRunAdmittedV1:
		if attempt.State == corecontract.ModelAttemptFailed ||
			attempt.State == corecontract.ModelAttemptSucceeded {
			if err := requireLearningCycleTerminal(ctx, connection, task.RunID, attempt); err != nil {
				return fmt.Errorf("%w: unprojected terminal Attempt: %v", ErrLearningCycleIntegrity, err)
			}
		}
		return nil
	case learningcontract.LearningCycleTaskUnknownV1:
		if task.AttemptID != attempt.AttemptID {
			return fmt.Errorf("%w: UNKNOWN Task binds another Attempt", ErrLearningCycleIntegrity)
		}
		if attempt.State == corecontract.ModelAttemptUnknown {
			return nil
		}
		if (attempt.State == corecontract.ModelAttemptFailed ||
			attempt.State == corecontract.ModelAttemptSucceeded) &&
			attempt.ReconciliationEvidenceRef != "" {
			if err := requireLearningCycleTerminal(ctx, connection, task.RunID, attempt); err != nil {
				return fmt.Errorf("%w: reconciled UNKNOWN terminal: %v", ErrLearningCycleIntegrity, err)
			}
			return nil
		}
		return fmt.Errorf(
			"%w: UNKNOWN Task is not UNKNOWN or evidence-reconciled on the same Attempt",
			ErrLearningCycleIntegrity,
		)
	default:
		return verifyLearningCycleTerminalTaskClosure(
			ctx,
			connection,
			schedule,
			task,
			manifest,
			member,
			attempt,
		)
	}
}

func verifyLearningCyclePersistedRunClosure(
	ctx context.Context,
	connection readQueryerV1,
	schedule LearningCycleScheduleRecord,
	task LearningCycleTaskRecord,
	identity learningcontract.LearningCycleExecutionIdentityV1,
) (corecontract.RunManifest, corecontract.MemberExecutionSnapshot, error) {
	var manifestCanonical []byte
	var manifestDigest string
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json, digest FROM run_manifests WHERE run_id=?
	`, task.RunID).Scan(&manifestCanonical, &manifestDigest); err != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: load Task RunManifest: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: restore Task RunManifest: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if task.RunID != identity.RunID || task.RunManifestDigest != manifestDigest ||
		manifest.RunID != identity.RunID || manifest.ManifestDigest != manifestDigest ||
		manifest.AdmissionKey != identity.AdmissionKey ||
		manifest.RecoveryRootRef != identity.RecoveryRootRef ||
		manifest.TenantID != task.TenantID ||
		manifest.Workspace.ID != schedule.Schedule.WorkspaceID ||
		manifest.PrimaryAgent.ID != schedule.Schedule.AgentID ||
		!manifest.Deadline.Equal(time.UnixMicro(task.Request.WindowEndMicros).UTC()) ||
		manifest.Composite != nil || manifest.ConversationTurn != nil ||
		manifest.ParentRunID != "" || len(manifest.Members) != 1 ||
		manifest.PrimaryMemberID != identity.MemberID ||
		manifest.Members[0].MemberID != identity.MemberID {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: Task RunManifest differs from deterministic Schedule execution",
			ErrLearningCycleIntegrity,
		)
	}
	resolved, found, err := resolveAdmissionWithQueryer(
		ctx,
		connection,
		task.TenantID,
		identity.AdmissionKey,
		manifest.AdmissionIntentDigest,
	)
	if err != nil || !found || resolved.RunID != identity.RunID ||
		resolved.ManifestDigest != manifestDigest ||
		resolved.MemberSnapshotDigest != manifest.Members[0].Digest {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: deterministic Run Admission closure: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	var memberCanonical []byte
	var memberDigest string
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM member_execution_snapshots
		WHERE run_id=? AND member_id=?
	`, identity.RunID, identity.MemberID).Scan(&memberCanonical, &memberDigest); err != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: load deterministic Member: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil || member.MemberSnapshotDigest != memberDigest ||
		memberDigest != manifest.Members[0].Digest ||
		member.MemberID != identity.MemberID ||
		member.Agent.ID != schedule.Schedule.AgentID ||
		member.Profile.ID != schedule.Schedule.ProfileID ||
		member.Workspace.ID != schedule.Schedule.WorkspaceID ||
		len(member.Actions) != 0 || len(member.PortPlans) != 1 ||
		len(member.PortPlans[0].Bindings) != 1 {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: deterministic Member differs from Schedule/model-only scope: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	plan := member.PortPlans[0]
	binding := plan.Bindings[0]
	if plan.Port.Name != moduleapi.PortNameModelGenerate ||
		plan.Port.ExactVersion != moduleapi.PortVersionV1 ||
		binding.FailurePolicy != moduleapi.FailureRequired {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: deterministic Member is not required model-only scope",
			ErrLearningCycleIntegrity,
		)
	}
	taskContent, err := queryContent(ctx, connection, manifest.TaskInputRef)
	if err != nil || taskContent.Kind != ContentTaskInput ||
		taskContent.MediaType != admissionJSONMediaType {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: deterministic TASK_INPUT: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	taskInput, err := corecontract.RestoreTaskInputV1(taskContent.CanonicalBytes)
	if err != nil || manifest.TaskInputDigest != taskContent.Digest ||
		!bytes.Equal([]byte(taskInput.Text), task.RequestCanonical) {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: deterministic TASK_INPUT differs from Request: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	configContent, err := queryContent(ctx, connection, binding.ConfigRef)
	if err != nil || configContent.Kind != ContentConfig ||
		configContent.MediaType != admissionJSONMediaType {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: deterministic model Config: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	config, err := moduleapi.RestoreModelBindingConfigV1(configContent.CanonicalBytes)
	if err != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: restore deterministic model Config: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if err := verifyLearningCycleMaxTokens(
		config.Parameters,
		task.Request.MaxOutputTokens,
	); err != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{}, fmt.Errorf(
			"%w: deterministic model Config exceeds Request: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	return manifest, member, nil
}

func loadSoleLearningCycleAttempt(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
) (ModelDispatchAttemptRecord, bool, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT attempt_id FROM model_dispatch_attempts
		WHERE run_id=? ORDER BY attempt_id COLLATE BINARY LIMIT 2
	`, runID)
	if err != nil {
		return ModelDispatchAttemptRecord{}, false, fmt.Errorf(
			"%w: enumerate Task Attempts: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	var attemptIDs []string
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			_ = rows.Close()
			return ModelDispatchAttemptRecord{}, false, fmt.Errorf(
				"%w: scan Task Attempt: %v",
				ErrLearningCycleIntegrity,
				err,
			)
		}
		attemptIDs = append(attemptIDs, attemptID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ModelDispatchAttemptRecord{}, false, fmt.Errorf(
			"%w: iterate Task Attempts: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return ModelDispatchAttemptRecord{}, false, fmt.Errorf(
			"%w: close Task Attempts: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	if len(attemptIDs) == 0 {
		return ModelDispatchAttemptRecord{}, false, nil
	}
	if len(attemptIDs) != 1 {
		return ModelDispatchAttemptRecord{}, false, fmt.Errorf(
			"%w: Task Run has more than one Model Attempt",
			ErrLearningCycleIntegrity,
		)
	}
	attempt, err := queryModelDispatchAttempt(ctx, connection, attemptIDs[0])
	if err != nil {
		return ModelDispatchAttemptRecord{}, false, fmt.Errorf(
			"%w: load Task Attempt: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	var dispatchCount int
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?
	`, runID).Scan(&dispatchCount); err != nil || dispatchCount != 0 {
		return ModelDispatchAttemptRecord{}, false, fmt.Errorf(
			"%w: Task Run has Action or Channel dispatch: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	return attempt, true, nil
}

func verifyLearningCycleAttemptClosure(
	task LearningCycleTaskRecord,
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
	attempt ModelDispatchAttemptRecord,
) error {
	binding := member.PortPlans[0].Bindings[0]
	if attempt.RunID != task.RunID || attempt.MemberID != member.MemberID ||
		attempt.MemberSnapshotDigest != member.MemberSnapshotDigest ||
		attempt.LogicalStepID != corecontract.PureChatModelLogicalStepIDV1 ||
		attempt.SourceDispatchAttemptID != "" ||
		attempt.Deadline.After(manifest.Deadline) ||
		attempt.Binding.Provider != binding.Provider ||
		attempt.Binding.ConfigRef != binding.ConfigRef ||
		attempt.Binding.AuthorityCeilingRef != binding.AuthorityCeilingRef ||
		attempt.Binding.FailurePolicy != binding.FailurePolicy ||
		!equalLearningCycleRefs(attempt.Binding.StaticContextRefs, binding.StaticContextRefs) {
		return fmt.Errorf(
			"%w: Task Attempt differs from deterministic model-only Member",
			ErrLearningCycleIntegrity,
		)
	}
	return nil
}

func verifyLearningCycleTerminalTaskClosure(
	ctx context.Context,
	connection readQueryerV1,
	schedule LearningCycleScheduleRecord,
	task LearningCycleTaskRecord,
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
	attempt ModelDispatchAttemptRecord,
) error {
	if task.AttemptID != attempt.AttemptID {
		return fmt.Errorf("%w: terminal Task binds another Attempt", ErrLearningCycleIntegrity)
	}
	if task.Revision == 3 && attempt.ReconciliationEvidenceRef == "" {
		return fmt.Errorf("%w: revision-3 Task has no reconciliation evidence", ErrLearningCycleIntegrity)
	}
	if err := requireLearningCycleTerminal(ctx, connection, task.RunID, attempt); err != nil {
		return fmt.Errorf("%w: terminal Task Run closure: %v", ErrLearningCycleIntegrity, err)
	}
	switch attempt.State {
	case corecontract.ModelAttemptFailed:
		if task.State != learningcontract.LearningCycleTaskFailedV1 ||
			task.ResultRef != "" || task.ProposalID != "" {
			return fmt.Errorf("%w: FAILED Task differs from Attempt", ErrLearningCycleIntegrity)
		}
		return nil
	case corecontract.ModelAttemptSucceeded:
	default:
		return fmt.Errorf(
			"%w: terminal Task binds nonterminal Attempt state %q",
			ErrLearningCycleIntegrity,
			attempt.State,
		)
	}
	if task.ResultRef != attempt.ResultRef {
		return fmt.Errorf("%w: terminal Task ResultRef differs from Attempt", ErrLearningCycleIntegrity)
	}
	cycleResult, valid, err := restoreLearningCycleModelResult(
		ctx,
		connection,
		task,
		attempt.ResultRef,
	)
	if err != nil {
		return err
	}
	if !valid {
		if task.State != learningcontract.LearningCycleTaskInvalidResultV1 || task.ProposalID != "" {
			return fmt.Errorf("%w: INVALID_RESULT Task differs from model output", ErrLearningCycleIntegrity)
		}
		return nil
	}
	if cycleResult.Decision == learningcontract.LearningCycleResultNoChangeV1 {
		if task.State != learningcontract.LearningCycleTaskNoChangeV1 || task.ProposalID != "" {
			return fmt.Errorf("%w: NO_CHANGE Task differs from model output", ErrLearningCycleIntegrity)
		}
		return nil
	}
	candidate, err := buildLearningCycleProposalCandidate(
		schedule,
		task,
		attempt,
		manifest,
		member,
		cycleResult,
	)
	if err != nil {
		return err
	}
	switch task.State {
	case learningcontract.LearningCycleTaskProposalSubmittedV1:
		if task.ProposalID != candidate.ProposalID {
			return fmt.Errorf("%w: submitted Task binds another Proposal", ErrLearningCycleIntegrity)
		}
		proposal, found, err := queryLearningProposalByID(ctx, connection, candidate.ProposalID)
		if err != nil || !found || proposal.ProposerAttemptID != attempt.AttemptID ||
			!bytes.Equal(proposal.ProposalCanonical, candidate.ProposalCanonical) ||
			!bytes.Equal(proposal.DraftCanonical, candidate.DraftCanonical) {
			return fmt.Errorf(
				"%w: submitted Task Proposal closure: %v",
				ErrLearningCycleIntegrity,
				err,
			)
		}
		provedAttemptID, err := proveLearningProposerLineage(ctx, connection, proposal.Proposal)
		if err != nil || provedAttemptID != attempt.AttemptID {
			return fmt.Errorf("%w: submitted Task Proposal lineage: %v", ErrLearningCycleIntegrity, err)
		}
		return nil
	case learningcontract.LearningCycleTaskSourceOccupiedV1,
		learningcontract.LearningCycleTaskContentOccupiedV1,
		learningcontract.LearningCycleTaskTargetOccupiedV1:
		if task.ProposalID == candidate.ProposalID {
			return fmt.Errorf("%w: occupied Task binds its exact candidate", ErrLearningCycleIntegrity)
		}
		if err := verifyLearningCycleDuplicate(
			ctx,
			connection,
			candidate.Proposal,
			task.ProposalID,
			task.State,
		); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf(
			"%w: PROPOSE output differs from terminal Task state %s",
			ErrLearningCycleIntegrity,
			task.State,
		)
	}
}
