package currentbackup

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type conversationSemanticRow struct {
	conversationID string
	tenantID       string
	principalID    string
	workspaceID    string
	agentID        string
	profileID      string
	headRunID      sql.NullString
	revision       int64
}

// inspectConversationClosures proves the complete Core-owned linear history
// carried by one SQLite snapshot. The bundle adds no parallel Conversation
// index: conversations, Run projections, immutable Manifests, and terminal
// Run facts must close directly inside the authoritative Store.
func inspectConversationClosures(
	ctx context.Context,
	database semanticQueryer,
	runs map[string]*coreRunSemanticState,
) error {
	conversations, err := loadConversationSemanticRows(ctx, database)
	if err != nil {
		return err
	}
	turns := make(map[string]map[int64]*coreRunSemanticState, len(conversations))
	for runID, run := range runs {
		turn := run.manifest.ConversationTurn
		hasProjection := run.row.conversationID.Valid ||
			run.row.conversationTurnIndex.Valid ||
			run.row.conversationPrevious.Valid
		if turn == nil {
			if hasProjection {
				return coreIntegrity(
					"non-Conversation Run %q contains Conversation projection",
					runID,
				)
			}
			continue
		}
		if !run.row.conversationID.Valid ||
			!run.row.conversationTurnIndex.Valid ||
			run.row.conversationTurnIndex.Int64 <= 0 ||
			run.row.conversationID.String != turn.ConversationID ||
			uint64(run.row.conversationTurnIndex.Int64) != turn.TurnIndex ||
			run.row.conversationPrevious.String != turn.PredecessorRunID ||
			run.row.conversationPrevious.Valid != (turn.PredecessorRunID != "") {
			return coreIntegrity(
				"Conversation Run %q row/Manifest turn projection differs",
				runID,
			)
		}
		conversation, found := conversations[turn.ConversationID]
		if !found {
			return coreIntegrity(
				"Conversation Run %q names absent Conversation %q",
				runID,
				turn.ConversationID,
			)
		}
		if conversation.tenantID != run.row.tenantID ||
			conversation.workspaceID != run.row.workspaceID ||
			conversation.workspaceID != run.member.Workspace.ID ||
			conversation.agentID != run.member.Agent.ID ||
			conversation.profileID != run.member.Profile.ID ||
			conversation.principalID != turn.PrincipalID {
			return coreIntegrity(
				"Conversation Run %q fixed owner/scope differs",
				runID,
			)
		}
		byIndex := turns[turn.ConversationID]
		if byIndex == nil {
			byIndex = make(map[int64]*coreRunSemanticState)
			turns[turn.ConversationID] = byIndex
		}
		index := run.row.conversationTurnIndex.Int64
		if _, duplicate := byIndex[index]; duplicate {
			return coreIntegrity(
				"Conversation %q contains duplicate turn index %d",
				turn.ConversationID,
				index,
			)
		}
		byIndex[index] = run
	}

	for conversationID, conversation := range conversations {
		if conversation.revision < 0 ||
			(conversation.revision == 0 && conversation.headRunID.Valid) ||
			(conversation.revision > 0 && !conversation.headRunID.Valid) {
			return coreIntegrity(
				"Conversation %q revision/head projection differs",
				conversationID,
			)
		}
		byIndex := turns[conversationID]
		if int64(len(byIndex)) != conversation.revision {
			return coreIntegrity(
				"Conversation %q turn count=%d differs from revision=%d",
				conversationID,
				len(byIndex),
				conversation.revision,
			)
		}
		var predecessor string
		for index := int64(1); index <= conversation.revision; index++ {
			run := byIndex[index]
			if run == nil || run.manifest.ConversationTurn == nil ||
				run.manifest.ConversationTurn.PredecessorRunID != predecessor {
				return coreIntegrity(
					"Conversation %q turn %d predecessor chain differs",
					conversationID,
					index,
				)
			}
			if index < conversation.revision &&
				!coreRunHasSuccessfulTerminalResult(run) {
				return coreIntegrity(
					"Conversation %q predecessor turn %d Run %q is not a complete successful terminal",
					conversationID,
					index,
					run.row.runID,
				)
			}
			predecessor = run.row.runID
		}
		if conversation.revision > 0 &&
			conversation.headRunID.String != predecessor {
			return coreIntegrity(
				"Conversation %q head %q differs from turn %d Run %q",
				conversationID,
				conversation.headRunID.String,
				conversation.revision,
				predecessor,
			)
		}
	}
	return nil
}

// inspectConversationSummaryClosures closes the existing nested Summary
// evidence directly against raw successful Conversation predecessors. Backup
// does not create a summary index or follow an older summary: every digest and
// byte is rebuilt from the original USER TASK_INPUT and terminal ASSISTANT
// MODEL_RESULT pair at turn indexes 1..N.
func inspectConversationSummaryClosures(
	ctx context.Context,
	database semanticQueryer,
	runs map[string]*coreRunSemanticState,
) error {
	turns := make(map[string]map[uint64]*coreRunSemanticState)
	for _, run := range runs {
		turn := run.manifest.ConversationTurn
		if turn == nil {
			continue
		}
		byIndex := turns[turn.ConversationID]
		if byIndex == nil {
			byIndex = make(map[uint64]*coreRunSemanticState)
			turns[turn.ConversationID] = byIndex
		}
		byIndex[turn.TurnIndex] = run
	}

	runIDs := make([]string, 0, len(runs))
	for runID := range runs {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)
	for _, runID := range runIDs {
		run := runs[runID]
		turn := run.manifest.ConversationTurn
		if turn == nil {
			continue
		}
		attemptIDs := make([]string, 0, len(run.attempts))
		for attemptID := range run.attempts {
			attemptIDs = append(attemptIDs, attemptID)
		}
		sort.Strings(attemptIDs)
		for _, attemptID := range attemptIDs {
			attempt := run.attempts[attemptID]
			if attempt.compilation == nil ||
				attempt.compilation.Summary == nil {
				continue
			}
			summary := attempt.compilation.Summary
			if turn.TurnIndex <= 1 ||
				uint64(len(summary.SourceTurnDigests)) >= turn.TurnIndex {
				return coreIntegrity(
					"Conversation Run %q model Attempt %q Summary range exceeds its raw predecessors",
					runID,
					attemptID,
				)
			}

			messages := make(
				[]moduleapi.ModelMessageV1,
				0,
				len(summary.SourceTurnDigests)*2,
			)
			for index, sourceDigest := range summary.SourceTurnDigests {
				sourceIndex := uint64(index + 1)
				source := turns[turn.ConversationID][sourceIndex]
				if source == nil {
					return coreIntegrity(
						"Conversation Run %q model Attempt %q Summary source turn %d is not a raw successful predecessor",
						runID,
						attemptID,
						sourceIndex,
					)
				}
				terminal := coreSuccessfulTerminalAttempt(source)
				if terminal == nil {
					return coreIntegrity(
						"Conversation Run %q model Attempt %q Summary source turn %d is not a raw successful predecessor",
						runID,
						attemptID,
						sourceIndex,
					)
				}
				taskContent, err := inspectCoreContent(
					ctx,
					database,
					source.manifest.TaskInputRef,
					currentstore.ContentTaskInput,
				)
				if err != nil || taskContent.mediaType != coreJSONMediaType {
					return coreIntegrity(
						"Conversation Run %q model Attempt %q Summary source turn %d TaskInput differs: %v",
						runID,
						attemptID,
						sourceIndex,
						err,
					)
				}
				task, err := corecontract.RestoreTaskInputV1(taskContent.canonical)
				if err != nil {
					return coreIntegrity(
						"Conversation Run %q model Attempt %q Summary source turn %d TaskInput wire differs: %v",
						runID,
						attemptID,
						sourceIndex,
						err,
					)
				}
				expectedDigest, err := corecontract.ContextConversationTurnDigestV1(
					sourceIndex,
					source.manifest.TaskInputRef,
					terminal.resultRef.String,
				)
				if err != nil || sourceDigest != expectedDigest {
					return coreIntegrity(
						"Conversation Run %q model Attempt %q Summary source turn %d digest differs: %v",
						runID,
						attemptID,
						sourceIndex,
						err,
					)
				}
				messages = append(
					messages,
					moduleapi.ModelMessageV1{
						Role: moduleapi.ModelRoleUser, Content: task.Text,
					},
					moduleapi.ModelMessageV1{
						Role:    moduleapi.ModelRoleAssistant,
						Content: terminal.result.AssistantText,
					},
				)
			}
			expectedText, err := corecontract.ContextHeadTailSummaryV1(messages)
			if err != nil || summary.Text != expectedText {
				return coreIntegrity(
					"Conversation Run %q model Attempt %q Summary text differs from raw predecessors: %v",
					runID,
					attemptID,
					err,
				)
			}
			if countCoreModelMessage(
				attempt.request.Messages,
				moduleapi.ModelMessageV1{
					Role: moduleapi.ModelRoleAssistant, Content: expectedText,
				},
			) == 0 {
				return coreIntegrity(
					"Conversation Run %q model Attempt %q request does not contain its exact Summary",
					runID,
					attemptID,
				)
			}
		}
	}
	return nil
}

// inspectKnowledgeReuseSourceClosures follows only immutable facts already
// carried by the snapshot. In particular, it does not re-evaluate reuse TTL,
// read a current Memory head, inspect current activation, or invoke a module.
// The newest earlier turn with the same exact TaskInputRef is authoritative:
// an unusable newest match must fail closed instead of falling through to an
// older fresh retrieval.
func inspectKnowledgeReuseSourceClosures(
	runs map[string]*coreRunSemanticState,
) error {
	runIDs := make([]string, 0, len(runs))
	for runID := range runs {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)

	for _, runID := range runIDs {
		run := runs[runID]
		attemptIDs := make([]string, 0, len(run.attempts))
		for attemptID := range run.attempts {
			attemptIDs = append(attemptIDs, attemptID)
		}
		sort.Strings(attemptIDs)
		for _, attemptID := range attemptIDs {
			attempt := run.attempts[attemptID]
			if attempt.compilation == nil ||
				len(attempt.compilation.KnowledgeReuses) == 0 {
				continue
			}
			turn := run.manifest.ConversationTurn
			if turn == nil {
				return coreIntegrity(
					"Run %q model Attempt %q Knowledge reuse lacks a Conversation turn",
					runID,
					attemptID,
				)
			}
			source := latestEarlierExactTaskInputRun(run, runs)
			for index, reuse := range attempt.compilation.KnowledgeReuses {
				if source == nil {
					return coreIntegrity(
						"Run %q model Attempt %q Knowledge reuse %d has no earlier exact TaskInput source",
						runID,
						attemptID,
						index,
					)
				}
				sourceTurn := source.manifest.ConversationTurn
				if sourceTurn == nil ||
					reuse.SourceConversationID != turn.ConversationID ||
					reuse.SourceConversationID != sourceTurn.ConversationID ||
					reuse.SourceTurnIndex != sourceTurn.TurnIndex ||
					reuse.SourceTurnIndex >= turn.TurnIndex ||
					reuse.SourceRunID != source.row.runID {
					return coreIntegrity(
						"Run %q model Attempt %q Knowledge reuse %d source turn differs from the latest earlier exact TaskInput turn",
						runID,
						attemptID,
						index,
					)
				}

				sourceAttempt := source.attempts[reuse.SourceAttemptID]
				terminalAttempt := coreSuccessfulTerminalAttempt(source)
				if sourceAttempt == nil || terminalAttempt == nil ||
					sourceAttempt != terminalAttempt ||
					sourceAttempt.state != corecontract.ModelAttemptSucceeded {
					return coreIntegrity(
						"Run %q model Attempt %q Knowledge reuse %d source Attempt is not the exact successful terminal",
						runID,
						attemptID,
						index,
					)
				}
				if !sourceAttempt.contextCompilationRef.Valid ||
					sourceAttempt.contextCompilationRef.String !=
						reuse.SourceCompilationRef ||
					sourceAttempt.compilation == nil {
					return coreIntegrity(
						"Run %q model Attempt %q Knowledge reuse %d source Compilation ref differs",
						runID,
						attemptID,
						index,
					)
				}
				matchedFreshRetrieval := false
				for _, retrieval := range sourceAttempt.compilation.KnowledgeRetrievals {
					if reflect.DeepEqual(retrieval, reuse.FreshRetrieval) {
						matchedFreshRetrieval = true
						break
					}
				}
				if !matchedFreshRetrieval {
					return coreIntegrity(
						"Run %q model Attempt %q Knowledge reuse %d source Compilation lacks the exact direct fresh retrieval",
						runID,
						attemptID,
						index,
					)
				}
			}
		}
	}
	return nil
}

func latestEarlierExactTaskInputRun(
	current *coreRunSemanticState,
	runs map[string]*coreRunSemanticState,
) *coreRunSemanticState {
	turn := current.manifest.ConversationTurn
	if turn == nil || turn.TurnIndex <= 1 {
		return nil
	}
	var latest *coreRunSemanticState
	for _, candidate := range runs {
		candidateTurn := candidate.manifest.ConversationTurn
		if candidateTurn == nil ||
			candidateTurn.ConversationID != turn.ConversationID ||
			candidateTurn.TurnIndex >= turn.TurnIndex ||
			candidate.manifest.TaskInputRef != current.manifest.TaskInputRef {
			continue
		}
		if latest == nil || candidateTurn.TurnIndex >
			latest.manifest.ConversationTurn.TurnIndex {
			latest = candidate
		}
	}
	return latest
}

func loadConversationSemanticRows(
	ctx context.Context,
	database semanticQueryer,
) (map[string]conversationSemanticRow, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT
			conversation_id,
			tenant_id,
			principal_id,
			workspace_id,
			agent_id,
			profile_id,
			head_run_id,
			revision
		FROM conversations
		ORDER BY conversation_id
	`)
	if err != nil {
		return nil, fmt.Errorf("currentbackup: read Conversations: %w", err)
	}
	defer rows.Close()
	result := make(map[string]conversationSemanticRow)
	for rows.Next() {
		var row conversationSemanticRow
		if err := rows.Scan(
			&row.conversationID,
			&row.tenantID,
			&row.principalID,
			&row.workspaceID,
			&row.agentID,
			&row.profileID,
			&row.headRunID,
			&row.revision,
		); err != nil {
			return nil, fmt.Errorf("currentbackup: scan Conversation: %w", err)
		}
		if _, duplicate := result[row.conversationID]; duplicate {
			return nil, coreIntegrity(
				"duplicate Conversation identity %q",
				row.conversationID,
			)
		}
		for name, value := range map[string]string{
			"conversation_id": row.conversationID,
			"tenant_id":       row.tenantID,
			"principal_id":    row.principalID,
			"workspace_id":    row.workspaceID,
			"agent_id":        row.agentID,
			"profile_id":      row.profileID,
		} {
			if !validConversationSemanticID(value) {
				return nil, coreIntegrity(
					"Conversation %q contains invalid %s",
					row.conversationID,
					name,
				)
			}
		}
		if row.headRunID.Valid &&
			!validConversationSemanticID(row.headRunID.String) {
			return nil, coreIntegrity(
				"Conversation %q contains invalid head Run ID",
				row.conversationID,
			)
		}
		result[row.conversationID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("currentbackup: iterate Conversations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("currentbackup: close Conversations: %w", err)
	}
	return result, nil
}

func validConversationSemanticID(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
