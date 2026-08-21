package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/memorycore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// appendSuccessfulModelOutcomeMemory is called only after the Attempt CAS has
// made the source Attempt SUCCEEDED, and only from the surrounding model
// outcome BEGIN IMMEDIATE transaction. It deliberately has no transaction of
// its own: Result, Usage, History, Frame, Event, snapshot content, and the
// revision edge either all commit or all roll back.
func appendSuccessfulModelOutcomeMemory(
	ctx context.Context,
	connection *sql.Conn,
	run RunForLoop,
	attempt ModelDispatchAttemptRecord,
	resultText string,
	completedAtMicros int64,
) error {
	binding, enabled, err := modelOutcomeMemoryBinding(
		ctx,
		connection,
		run,
	)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}

	head, err := queryCurrentAgentMemoryRevision(
		ctx,
		connection,
		run.Manifest.TenantID,
		run.Member.Agent.ID,
	)
	if err != nil {
		return modelOutcomeMemoryIntegrity("load current Agent Memory head", err)
	}
	if _, err := moduleapi.ResolveMemoryAuthorityV1(
		binding.Config,
		binding.Authority,
		head.SnapshotRef,
		binding.Scope,
	); err != nil {
		return modelOutcomeMemoryIntegrity("resolve frozen Memory authority", err)
	}
	source, err := modelOutcomeMemorySource(
		ctx,
		connection,
		run,
		attempt.ResultRef,
		resultText,
	)
	if err != nil {
		return err
	}
	completedAtUnixMS, err := modelOutcomeMemoryUnixMS(completedAtMicros)
	if err != nil {
		return err
	}
	_, canonical, err := memorycore.BuildSuccessfulRevision(
		head.Snapshot,
		memorycore.SuccessfulRevisionInput{
			CurrentSnapshotRef: head.SnapshotRef,
			Source:             source,
			Agent:              agentMemoryObjectRef(run.Member.Agent),
			Workspace:          workspaceMemoryObjectRef(run.Member.Workspace),
			Attempt: memorycore.SuccessfulAttempt{
				ID:                attempt.AttemptID,
				CompletedAtUnixMS: completedAtUnixMS,
			},
			Config: binding.Config,
		},
	)
	if err != nil {
		return modelOutcomeMemoryIntegrity("build successful Memory revision", err)
	}
	prepared, digest, err := prepareAgentMemorySnapshot(canonical)
	if err != nil {
		return modelOutcomeMemoryIntegrity("prepare successful Memory revision", err)
	}
	if prepared.Revision != head.SnapshotRef.Revision+1 ||
		prepared.PreviousSnapshotDigest != head.SnapshotRef.Digest ||
		prepared.SourceAttemptID != attempt.AttemptID {
		return modelOutcomeMemoryIntegrity(
			"built successful Memory revision does not close over the current head",
			nil,
		)
	}
	if err := validateAgentMemorySourceAttempt(
		ctx,
		connection,
		prepared.TenantID,
		prepared.AgentID,
		prepared.SourceAttemptID,
		ErrAgentMemoryIntegrity,
	); err != nil {
		return modelOutcomeMemoryIntegrity("validate successful source Attempt", err)
	}
	if err := putAgentMemoryContent(
		ctx,
		connection,
		digest,
		canonical,
		completedAtMicros,
	); err != nil {
		return modelOutcomeMemoryIntegrity("append Memory snapshot content", err)
	}

	result, err := connection.ExecContext(ctx, `
		INSERT INTO agent_memory_revisions(
			tenant_id,
			agent_id,
			revision,
			snapshot_ref,
			source_attempt_id,
			created_at
		)
		SELECT ?, ?, ?, ?, ?, ?
		WHERE EXISTS (
			SELECT 1
			FROM agent_memory_revisions
			WHERE tenant_id=? AND agent_id=?
			  AND revision=? AND snapshot_ref=?
			  AND revision=(
				SELECT MAX(revision)
				FROM agent_memory_revisions
				WHERE tenant_id=? AND agent_id=?
			  )
		)
	`,
		prepared.TenantID,
		prepared.AgentID,
		int64(prepared.Revision),
		digest,
		prepared.SourceAttemptID,
		completedAtMicros,
		prepared.TenantID,
		prepared.AgentID,
		int64(head.SnapshotRef.Revision),
		head.SnapshotRef.Digest,
		prepared.TenantID,
		prepared.AgentID,
	)
	if err != nil {
		return modelOutcomeMemoryIntegrity("insert successful Memory revision", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return modelOutcomeMemoryIntegrity("inspect successful Memory append", err)
	}
	if affected != 1 {
		return modelOutcomeMemoryIntegrity(
			fmt.Sprintf("successful Memory append affected %d rows", affected),
			nil,
		)
	}
	stored, err := queryAgentMemoryRevision(
		ctx,
		connection,
		prepared.TenantID,
		prepared.AgentID,
		prepared.Revision,
	)
	if err != nil {
		return modelOutcomeMemoryIntegrity("reload successful Memory revision", err)
	}
	if stored.SnapshotRef.Digest != digest ||
		stored.SourceAttemptID != attempt.AttemptID ||
		!bytes.Equal(stored.CanonicalBytes, canonical) {
		return modelOutcomeMemoryIntegrity(
			"stored successful Memory revision differs from built bytes",
			nil,
		)
	}
	return nil
}

// verifyIdempotentSuccessfulModelOutcomeMemory never appends or repairs. An
// exact terminal replay must prove that its source Attempt already identifies
// one byte-for-byte reproducible revision built from that revision's exact
// parent, even when a later Run has since advanced the Agent head.
func verifyIdempotentSuccessfulModelOutcomeMemory(
	ctx context.Context,
	connection *sql.Conn,
	run RunForLoop,
	attempt ModelDispatchAttemptRecord,
	resultText string,
) error {
	binding, enabled, err := modelOutcomeMemoryBinding(
		ctx,
		connection,
		run,
	)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	stored, err := queryAgentMemoryRevisionBySource(
		ctx,
		connection,
		attempt.AttemptID,
	)
	if err != nil {
		return modelOutcomeMemoryIntegrity(
			"successful replay has no closed Memory revision",
			err,
		)
	}
	if stored.SnapshotRef.TenantID != run.Manifest.TenantID ||
		stored.SnapshotRef.AgentID != run.Member.Agent.ID ||
		stored.SnapshotRef.Revision <= 1 ||
		stored.SourceAttemptID != attempt.AttemptID {
		return modelOutcomeMemoryIntegrity(
			"successful replay Memory revision has a different owner or source",
			nil,
		)
	}
	parent, err := queryAgentMemoryRevision(
		ctx,
		connection,
		stored.SnapshotRef.TenantID,
		stored.SnapshotRef.AgentID,
		stored.SnapshotRef.Revision-1,
	)
	if err != nil {
		return modelOutcomeMemoryIntegrity(
			"successful replay Memory revision has no exact parent",
			err,
		)
	}
	if _, err := moduleapi.ResolveMemoryAuthorityV1(
		binding.Config,
		binding.Authority,
		parent.SnapshotRef,
		binding.Scope,
	); err != nil {
		return modelOutcomeMemoryIntegrity("resolve replay Memory authority", err)
	}
	source, err := modelOutcomeMemorySource(
		ctx,
		connection,
		run,
		attempt.ResultRef,
		resultText,
	)
	if err != nil {
		return err
	}
	completedAtUnixMS, err := modelOutcomeMemoryUnixMS(
		attempt.UpdatedAt.UnixMicro(),
	)
	if err != nil {
		return err
	}
	_, canonical, err := memorycore.BuildSuccessfulRevision(
		parent.Snapshot,
		memorycore.SuccessfulRevisionInput{
			CurrentSnapshotRef: parent.SnapshotRef,
			Source:             source,
			Agent:              agentMemoryObjectRef(run.Member.Agent),
			Workspace:          workspaceMemoryObjectRef(run.Member.Workspace),
			Attempt: memorycore.SuccessfulAttempt{
				ID:                attempt.AttemptID,
				CompletedAtUnixMS: completedAtUnixMS,
			},
			Config: binding.Config,
		},
	)
	if err != nil {
		return modelOutcomeMemoryIntegrity("rebuild successful Memory revision", err)
	}
	_, digest, err := prepareAgentMemorySnapshot(canonical)
	if err != nil {
		return modelOutcomeMemoryIntegrity("digest rebuilt Memory revision", err)
	}
	if digest != stored.SnapshotRef.Digest ||
		!bytes.Equal(canonical, stored.CanonicalBytes) {
		return modelOutcomeMemoryIntegrity(
			"successful replay Memory revision cannot be reproduced byte-for-byte",
			nil,
		)
	}
	return nil
}

func modelOutcomeMemoryBinding(
	ctx context.Context,
	connection *sql.Conn,
	run RunForLoop,
) (frozenMemoryBinding, bool, error) {
	bindings, err := frozenMemoryBindingsForRun(
		run.Manifest,
		run.Member,
		func(digest string) (ContentRecord, error) {
			return queryContent(ctx, connection, digest)
		},
	)
	if err != nil {
		return frozenMemoryBinding{}, false,
			modelOutcomeMemoryIntegrity("load frozen Memory Binding", err)
	}
	switch len(bindings) {
	case 0:
		return frozenMemoryBinding{}, false, nil
	case 1:
		return bindings[0], true, nil
	default:
		return frozenMemoryBinding{}, false,
			modelOutcomeMemoryIntegrity(
				fmt.Sprintf("frozen member contains %d Memory Bindings", len(bindings)),
				nil,
			)
	}
}

func modelOutcomeMemorySource(
	ctx context.Context,
	connection *sql.Conn,
	run RunForLoop,
	resultRef string,
	resultText string,
) (memorycore.SuccessfulRevisionSource, error) {
	taskRecord, err := queryContent(
		ctx,
		connection,
		run.Manifest.TaskInputRef,
	)
	if err != nil {
		return memorycore.SuccessfulRevisionSource{},
			modelOutcomeMemoryIntegrity("load Memory source TaskInput", err)
	}
	if taskRecord.Kind != ContentTaskInput ||
		taskRecord.MediaType != admissionJSONMediaType {
		return memorycore.SuccessfulRevisionSource{},
			modelOutcomeMemoryIntegrity(
				"Memory source TaskInput has a different kind or media type",
				nil,
			)
	}
	task, err := corecontract.RestoreTaskInputV1(taskRecord.CanonicalBytes)
	if err != nil {
		return memorycore.SuccessfulRevisionSource{},
			modelOutcomeMemoryIntegrity("restore Memory source TaskInput", err)
	}
	if !moduleapi.ValidSHA256(resultRef) {
		return memorycore.SuccessfulRevisionSource{},
			modelOutcomeMemoryIntegrity("successful Attempt has no exact result ref", nil)
	}
	return memorycore.SuccessfulRevisionSource{
		TaskRef:    run.Manifest.TaskInputRef,
		TaskText:   task.Text,
		ResultRef:  resultRef,
		ResultText: resultText,
	}, nil
}

func agentMemoryObjectRef(input corecontract.AgentRef) moduleapi.MemoryObjectRefV1 {
	return moduleapi.MemoryObjectRefV1{
		ID:      input.ID,
		Version: input.Version,
		Digest:  input.Digest,
	}
}

func workspaceMemoryObjectRef(
	input corecontract.WorkspaceRef,
) moduleapi.MemoryObjectRefV1 {
	return moduleapi.MemoryObjectRefV1{
		ID:      input.ID,
		Version: input.Version,
		Digest:  input.Digest,
	}
}

func modelOutcomeMemoryUnixMS(micros int64) (uint64, error) {
	if micros <= 0 {
		return 0, modelOutcomeMemoryIntegrity(
			"successful Attempt completion time is not positive",
			nil,
		)
	}
	milliseconds := uint64(micros / 1000)
	if milliseconds == 0 ||
		milliseconds > moduleapi.MaxMemorySafeIntegerV1 ||
		milliseconds > math.MaxInt64 {
		return 0, modelOutcomeMemoryIntegrity(
			"successful Attempt completion time is outside Memory wire bounds",
			nil,
		)
	}
	return milliseconds, nil
}

func modelOutcomeMemoryIntegrity(action string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrModelDispatchIntegrity, action)
	}
	if errors.Is(cause, ErrModelDispatchIntegrity) {
		return cause
	}
	return fmt.Errorf("%w: %s: %v", ErrModelDispatchIntegrity, action, cause)
}
