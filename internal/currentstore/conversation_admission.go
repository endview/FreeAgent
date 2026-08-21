package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
)

// CommitConversationTurnAdmission atomically publishes one complete Run and
// advances the owning Conversation head. The Conversation compare-and-swap is
// the final write in the same BEGIN IMMEDIATE transaction, so a stale writer
// cannot leave an admitted orphan Run behind.
func (store *Store) CommitConversationTurnAdmission(
	ctx context.Context,
	input CommitRunAdmissionInput,
) (RunAdmissionResult, error) {
	if ctx == nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidAdmission,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return RunAdmissionResult{}, err
	}
	defer unlock()

	prepared, err := prepareCompleteRunAdmission(input)
	if err != nil {
		return RunAdmissionResult{}, err
	}
	if prepared.intent.ConversationTurn == nil ||
		prepared.manifest.ConversationTurn == nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Conversation turn intent and Manifest ref are required",
			ErrInvalidAdmission,
		)
	}
	if prepared.manifest.Composite != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Conversation and composite Admission are mutually exclusive",
			ErrInvalidAdmission,
		)
	}
	if prepared.intent.ChannelEndpointID != "" {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Conversation and Channel Admission are mutually exclusive",
			ErrInvalidAdmission,
		)
	}

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"currentstore: acquire Conversation Admission connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"currentstore: begin Conversation Admission: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	// Historical idempotency must win over current Control/Catalog and current
	// Conversation-head checks. A retry returns the original turn even after
	// later turns have advanced the Conversation.
	existing, found, err := resolveAdmissionWithQueryer(
		ctx,
		connection,
		prepared.intent.TenantID,
		prepared.intent.AdmissionKey,
		prepared.input.IntentDigest,
	)
	if err != nil {
		return RunAdmissionResult{}, err
	}
	if found {
		if err := verifyConversationRunProjection(
			ctx,
			connection,
			existing.RunID,
			prepared.manifest.ConversationTurn,
		); err != nil {
			return RunAdmissionResult{}, err
		}
		if err := verifyCurrentRunObservationV1(ctx, connection, existing.RunID); err != nil {
			return RunAdmissionResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return RunAdmissionResult{}, fmt.Errorf(
				"currentstore: commit idempotent Conversation Admission: %w",
				err,
			)
		}
		committed = true
		return existing, nil
	}

	conversation, err := queryConversation(
		ctx,
		connection,
		prepared.intent.ConversationTurn.ConversationID,
	)
	if err != nil {
		return RunAdmissionResult{}, err
	}
	if !conversationScopeMatchesAdmission(conversation, prepared) {
		return RunAdmissionResult{}, conversationAdmissionConflict(
			"Conversation fixed scope differs from the compiled Run",
		)
	}
	turn := prepared.intent.ConversationTurn
	if conversation.Revision != turn.ExpectedConversationRevision ||
		conversation.HeadRunID != turn.ExpectedHeadRunID {
		return RunAdmissionResult{}, conversationAdmissionConflict(
			"Conversation %q head changed from revision %d Run %q",
			conversation.ConversationID,
			turn.ExpectedConversationRevision,
			turn.ExpectedHeadRunID,
		)
	}
	manifestTurn := prepared.manifest.ConversationTurn
	if manifestTurn.TurnIndex != conversation.Revision+1 ||
		manifestTurn.PredecessorRunID != conversation.HeadRunID {
		return RunAdmissionResult{}, conversationAdmissionConflict(
			"Conversation %q row and compiled predecessor/index do not close",
			conversation.ConversationID,
		)
	}
	if err := verifyConversationPredecessor(
		ctx,
		connection,
		conversation,
	); err != nil {
		return RunAdmissionResult{}, err
	}

	createdAt := nowUnixMicro()
	if createdAt <= 0 {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: invalid Conversation Admission time",
			ErrAdmissionIntegrity,
		)
	}
	result, err := publishPreparedRunAdmission(
		ctx,
		connection,
		prepared,
		createdAt,
	)
	if err != nil {
		return RunAdmissionResult{}, err
	}
	if err := advanceConversationHead(
		ctx,
		connection,
		conversation,
		prepared.manifest.RunID,
		createdAt,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"currentstore: commit Conversation Admission: %w",
			err,
		)
	}
	committed = true
	result.Created = true
	return result, nil
}

func conversationTurnIntentMatchesManifest(
	intent corecontract.AdmissionIntentV1,
	manifest corecontract.RunManifest,
) bool {
	if (intent.ConversationTurn == nil) !=
		(manifest.ConversationTurn == nil) {
		return false
	}
	if intent.ConversationTurn == nil {
		return true
	}
	want := intent.ConversationTurn
	got := manifest.ConversationTurn
	return got.ConversationID == want.ConversationID &&
		got.PrincipalID == intent.PrincipalID &&
		got.TurnIndex == want.ExpectedConversationRevision+1 &&
		got.PredecessorRunID == want.ExpectedHeadRunID
}

func conversationScopeMatchesAdmission(
	record ConversationRecord,
	prepared preparedRunAdmission,
) bool {
	turn := prepared.manifest.ConversationTurn
	return turn != nil &&
		record.ConversationID == turn.ConversationID &&
		record.TenantID == prepared.intent.TenantID &&
		record.PrincipalID == prepared.intent.PrincipalID &&
		record.PrincipalID == turn.PrincipalID &&
		record.WorkspaceID == prepared.intent.WorkspaceID &&
		record.WorkspaceID == prepared.manifest.Workspace.ID &&
		record.AgentID == prepared.intent.AgentID &&
		record.AgentID == prepared.manifest.PrimaryAgent.ID &&
		record.ProfileID == prepared.intent.ProfileID &&
		record.ProfileID == prepared.member.Profile.ID
}

func verifyConversationPredecessor(
	ctx context.Context,
	connection *sql.Conn,
	conversation ConversationRecord,
) error {
	if conversation.Revision == 0 {
		return nil
	}
	var (
		tenantID       string
		workspaceID    string
		admissionKey   string
		intentDigest   string
		conversationID sql.NullString
		turnIndex      sql.NullInt64
		predecessorID  sql.NullString
	)
	err := connection.QueryRowContext(ctx, `
		SELECT
			tenant_id,
			workspace_id,
			admission_key,
			admission_intent_digest,
			conversation_id,
			conversation_turn_index,
			conversation_predecessor_run_id
		FROM runs
		WHERE run_id=?
	`, conversation.HeadRunID).Scan(
		&tenantID,
		&workspaceID,
		&admissionKey,
		&intentDigest,
		&conversationID,
		&turnIndex,
		&predecessorID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf(
			"%w: Conversation head Run %q is absent",
			ErrAdmissionIntegrity,
			conversation.HeadRunID,
		)
	}
	if err != nil {
		return fmt.Errorf(
			"currentstore: read Conversation predecessor: %w",
			err,
		)
	}
	if tenantID != conversation.TenantID ||
		workspaceID != conversation.WorkspaceID ||
		!conversationID.Valid ||
		conversationID.String != conversation.ConversationID ||
		!turnIndex.Valid || turnIndex.Int64 <= 0 ||
		uint64(turnIndex.Int64) != conversation.Revision {
		return fmt.Errorf(
			"%w: Conversation head Run projection is not the preceding turn",
			ErrAdmissionIntegrity,
		)
	}
	if _, err := loadAdmissionClosure(
		ctx,
		connection,
		conversation.HeadRunID,
		tenantID,
		admissionKey,
		intentDigest,
		workspaceID,
	); err != nil {
		return err
	}
	terminal, err := loadTerminalRunResult(
		ctx,
		connection,
		conversation.HeadRunID,
	)
	if err != nil {
		return conversationAdmissionConflict(
			"Conversation predecessor %q is not a complete successful terminal Run: %v",
			conversation.HeadRunID,
			err,
		)
	}
	if terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.ModelState != corecontract.ModelAttemptSucceeded ||
		terminal.ErrorClassification != "" ||
		len(terminal.OutputCanonical) == 0 ||
		terminal.Output.ActionRequest != nil ||
		terminal.Output.AssistantText == "" {
		return conversationAdmissionConflict(
			"Conversation predecessor %q has no successful final Assistant output",
			conversation.HeadRunID,
		)
	}
	return nil
}

func advanceConversationHead(
	ctx context.Context,
	connection *sql.Conn,
	conversation ConversationRecord,
	newHeadRunID string,
	updatedAt int64,
) error {
	var (
		result sql.Result
		err    error
	)
	if conversation.Revision == 0 {
		result, err = connection.ExecContext(ctx, `
			UPDATE conversations
			SET head_run_id=?, revision=1, updated_at=?
			WHERE conversation_id=?
			  AND tenant_id=?
			  AND principal_id=?
			  AND workspace_id=?
			  AND agent_id=?
			  AND profile_id=?
			  AND revision=0
			  AND head_run_id IS NULL
		`,
			newHeadRunID,
			updatedAt,
			conversation.ConversationID,
			conversation.TenantID,
			conversation.PrincipalID,
			conversation.WorkspaceID,
			conversation.AgentID,
			conversation.ProfileID,
		)
	} else {
		result, err = connection.ExecContext(ctx, `
			UPDATE conversations
			SET head_run_id=?, revision=revision+1, updated_at=?
			WHERE conversation_id=?
			  AND tenant_id=?
			  AND principal_id=?
			  AND workspace_id=?
			  AND agent_id=?
			  AND profile_id=?
			  AND revision=?
			  AND head_run_id=?
		`,
			newHeadRunID,
			updatedAt,
			conversation.ConversationID,
			conversation.TenantID,
			conversation.PrincipalID,
			conversation.WorkspaceID,
			conversation.AgentID,
			conversation.ProfileID,
			conversation.Revision,
			conversation.HeadRunID,
		)
	}
	if err != nil {
		return fmt.Errorf(
			"currentstore: advance Conversation head: %w",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect Conversation head CAS: %w",
			err,
		)
	}
	if affected != 1 {
		return conversationAdmissionConflict(
			"Conversation head CAS affected %d rows",
			affected,
		)
	}
	return nil
}

func conversationAdmissionConflict(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %w: %s",
		ErrConversationConflict,
		ErrAdmissionConflict,
		fmt.Sprintf(format, arguments...),
	)
}

func verifyConversationRunProjection(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	turn *corecontract.ConversationTurnRefV1,
) error {
	var conversationID, predecessorRunID sql.NullString
	var turnIndex sql.NullInt64
	if err := queryer.QueryRowContext(ctx, `
		SELECT
			conversation_id,
			conversation_turn_index,
			conversation_predecessor_run_id
		FROM runs
		WHERE run_id=?
	`, runID).Scan(
		&conversationID,
		&turnIndex,
		&predecessorRunID,
	); err != nil {
		return admissionClosureReadError(
			"Conversation Run projection",
			err,
		)
	}
	if !runConversationProjectionMatchesManifest(
		turn,
		conversationID,
		turnIndex,
		predecessorRunID,
	) {
		return fmt.Errorf(
			"%w: Run %q Conversation projection differs from the resolved turn",
			ErrAdmissionIntegrity,
			runID,
		)
	}
	return nil
}

func runConversationProjectionMatchesManifest(
	turn *corecontract.ConversationTurnRefV1,
	conversationID sql.NullString,
	turnIndex sql.NullInt64,
	predecessorRunID sql.NullString,
) bool {
	if turn == nil {
		return !conversationID.Valid && !turnIndex.Valid &&
			!predecessorRunID.Valid
	}
	if !conversationID.Valid || conversationID.String != turn.ConversationID ||
		!turnIndex.Valid || turnIndex.Int64 <= 0 ||
		uint64(turnIndex.Int64) != turn.TurnIndex {
		return false
	}
	if turn.PredecessorRunID == "" {
		return !predecessorRunID.Valid
	}
	return predecessorRunID.Valid &&
		predecessorRunID.String == turn.PredecessorRunID
}
