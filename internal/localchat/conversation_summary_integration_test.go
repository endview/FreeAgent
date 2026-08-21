package localchat

import (
	"bytes"
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
	_ "modernc.org/sqlite"
)

func TestChatServiceThreeTurnSummaryCandidateMatchesFreshCompilation(
	t *testing.T,
) {
	fixture := newChatServiceFixtureWithContextPolicy(t, 10000, 1000, 0)
	conversationID := "conversation-summary-three-turn"
	createSummaryIntegrationConversation(t, fixture, conversationID)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)

	firstInput := fixture.input(
		"summary-three-turn-1",
		strings.Repeat("old-summary-", 300),
		deadline,
	)
	firstInput.ConversationID = conversationID
	first := runSummaryIntegrationTurn(t, fixture, firstInput)

	secondInput := fixture.input(
		"summary-three-turn-2",
		strings.Repeat("middle-", 70),
		deadline.Add(time.Minute),
	)
	secondInput.ConversationID = conversationID
	secondInput.ExpectedConversationRevision = 1
	secondInput.ExpectedHeadRunID = first.RunID
	second := runSummaryIntegrationTurn(t, fixture, secondInput)
	secondDispatch := summaryIntegrationDispatch(t, fixture, second)
	secondCompilation := restoreSummaryIntegrationCompilation(
		t,
		secondDispatch,
	)
	if secondCompilation.Summary == nil ||
		len(secondCompilation.Summary.SourceTurnDigests) != 1 {
		t.Fatalf("second turn did not create the expected one-pair summary: %+v", secondCompilation)
	}

	thirdInput := fixture.input(
		"summary-three-turn-3",
		strings.Repeat("current-", 70),
		deadline.Add(2*time.Minute),
	)
	thirdInput.ConversationID = conversationID
	thirdInput.ExpectedConversationRevision = 2
	thirdInput.ExpectedHeadRunID = second.RunID
	third := runSummaryIntegrationTurn(t, fixture, thirdInput)
	thirdDispatch := summaryIntegrationDispatch(t, fixture, third)
	thirdCompilation := restoreSummaryIntegrationCompilation(t, thirdDispatch)
	if thirdCompilation.Summary == nil ||
		!slices.Equal(
			thirdCompilation.Summary.SourceTurnDigests,
			secondCompilation.Summary.SourceTurnDigests,
		) ||
		thirdCompilation.Summary.Text != secondCompilation.Summary.Text {
		t.Fatalf(
			"third summary range/text differs from the direct predecessor candidate second=%+v third=%+v",
			secondCompilation.Summary,
			thirdCompilation.Summary,
		)
	}

	fresh := compileThirdSummaryWithoutCandidate(
		t,
		fixture,
		firstInput,
		first,
		secondInput,
		second,
		thirdInput,
		thirdDispatch,
		thirdCompilation,
	)
	if !bytes.Equal(
		fresh.RequestCanonical,
		thirdDispatch.Attempt.Request.CanonicalBytes,
	) || !bytes.Equal(
		fresh.CompilationCanonical,
		thirdDispatch.Attempt.ContextCompilation.CanonicalBytes,
	) {
		t.Fatalf(
			"direct-predecessor candidate changed deterministic output\nfresh request=%s\nactual request=%s\nfresh compilation=%s\nactual compilation=%s",
			fresh.RequestCanonical,
			thirdDispatch.Attempt.Request.CanonicalBytes,
			fresh.CompilationCanonical,
			thirdDispatch.Attempt.ContextCompilation.CanonicalBytes,
		)
	}
	if fixture.invoker.callCount() != 3 {
		t.Fatalf("three-turn model calls=%d, want 3", fixture.invoker.callCount())
	}
}

func TestChatServiceDirectPredecessorCompilationTamperStopsBeforeModelDispatch(
	t *testing.T,
) {
	fixture := newChatServiceFixtureWithContextPolicy(t, 10000, 1000, 0)
	ctx := context.Background()
	conversationID := "conversation-summary-tamper"
	createSummaryIntegrationConversation(t, fixture, conversationID)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)

	firstInput := fixture.input(
		"summary-tamper-1",
		strings.Repeat("old-summary-", 300),
		deadline,
	)
	firstInput.ConversationID = conversationID
	first := runSummaryIntegrationTurn(t, fixture, firstInput)
	secondInput := fixture.input(
		"summary-tamper-2",
		strings.Repeat("middle-", 70),
		deadline.Add(time.Minute),
	)
	secondInput.ConversationID = conversationID
	secondInput.ExpectedConversationRevision = 1
	secondInput.ExpectedHeadRunID = first.RunID
	second := runSummaryIntegrationTurn(t, fixture, secondInput)
	secondDispatch := summaryIntegrationDispatch(t, fixture, second)
	if restoreSummaryIntegrationCompilation(t, secondDispatch).Summary == nil {
		t.Fatal("direct predecessor has no summary to protect")
	}

	raw := openSummaryIntegrationDatabase(t, fixture.store.Path())
	result := execSummaryIntegrationClosedFileTamper(
		t,
		raw,
		`
		UPDATE model_dispatch_attempts
		SET context_compilation_ref=request_ref
		WHERE attempt_id=?
		  AND state='SUCCEEDED'
		  AND context_compilation_ref IS NOT NULL
	`,
		second.TerminalResult.AttemptID,
	)
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("tamper direct predecessor affected=%d error=%v", affected, err)
	}
	before := summaryTamperModelBoundaryFootprint(t, raw)

	thirdInput := fixture.input(
		"summary-tamper-3",
		strings.Repeat("current-", 70),
		deadline.Add(2*time.Minute),
	)
	thirdInput.ConversationID = conversationID
	thirdInput.ExpectedConversationRevision = 2
	thirdInput.ExpectedHeadRunID = second.RunID
	third, err := fixture.service.Chat(ctx, thirdInput)
	if err == nil || third.RunID != "" || third.AdmissionCreated {
		t.Fatalf("tampered direct predecessor result=%+v error=%v", third, err)
	}
	if got := fixture.invoker.callCount(); got != 2 {
		t.Fatalf("tampered predecessor reached model: calls=%d want 2", got)
	}
	after := summaryTamperModelBoundaryFootprint(t, raw)
	if after != before {
		t.Fatalf("tampered predecessor changed model boundary before=%+v after=%+v", before, after)
	}
}

func createSummaryIntegrationConversation(
	t *testing.T,
	fixture *chatServiceFixture,
	conversationID string,
) {
	t.Helper()
	result, err := fixture.store.CreateConversation(
		context.Background(),
		currentstore.CreateConversationInput{
			ConversationID: conversationID,
			TenantID:       fixture.tenantID,
			PrincipalID:    "principal-chat",
			WorkspaceID:    fixture.workspaceID,
			AgentID:        fixture.agentID,
			ProfileID:      fixture.profileID,
		},
	)
	if err != nil || !result.Created {
		t.Fatalf("CreateConversation result=%+v error=%v", result, err)
	}
}

func runSummaryIntegrationTurn(
	t *testing.T,
	fixture *chatServiceFixture,
	input ChatInput,
) ChatResult {
	t.Helper()
	result, err := fixture.service.Chat(context.Background(), input)
	if err != nil || !result.AdmissionCreated || result.TerminalResult == nil ||
		result.TerminalResult.State != corecontract.ModelAttemptSucceeded {
		t.Fatalf("summary integration Chat result=%+v error=%v", result, err)
	}
	return result
}

func summaryIntegrationDispatch(
	t *testing.T,
	fixture *chatServiceFixture,
	result ChatResult,
) currentstore.ModelDispatchRecord {
	t.Helper()
	dispatch, err := fixture.store.GetModelDispatchRecord(
		context.Background(),
		result.TerminalResult.AttemptID,
	)
	if err != nil || dispatch.Attempt.ContextCompilation == nil {
		t.Fatalf("GetModelDispatchRecord dispatch=%+v error=%v", dispatch, err)
	}
	return dispatch
}

func restoreSummaryIntegrationCompilation(
	t *testing.T,
	dispatch currentstore.ModelDispatchRecord,
) corecontract.ContextCompilationV1 {
	t.Helper()
	compilation, err := corecontract.RestoreContextCompilationV1(
		dispatch.Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	return compilation
}

func compileThirdSummaryWithoutCandidate(
	t *testing.T,
	fixture *chatServiceFixture,
	firstInput ChatInput,
	first ChatResult,
	secondInput ChatInput,
	second ChatResult,
	thirdInput ChatInput,
	thirdDispatch currentstore.ModelDispatchRecord,
	thirdCompilation corecontract.ContextCompilationV1,
) contextcompiler.CompileResultV1 {
	t.Helper()
	_, control, _, err := fixture.store.LoadPublishedBasis(
		context.Background(),
		fixture.tenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	var agent corecontract.AgentRef
	for _, candidate := range control.Agents {
		if candidate.ID == fixture.agentID {
			agent = candidate
			break
		}
	}
	if agent.ID == "" {
		t.Fatal("published Agent is absent")
	}
	policy, err := fixture.store.GetContent(
		context.Background(),
		thirdCompilation.ContextPolicy.Digest,
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		thirdDispatch.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstHistory := summaryIntegrationHistoryTurn(t, 1, firstInput, first)
	secondHistory := summaryIntegrationHistoryTurn(t, 2, secondInput, second)
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          thirdInput.Message,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	taskDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentTaskInput,
		chatJSONMediaType,
		taskCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := contextcompiler.CompileV1(contextcompiler.CompileInputV1{
		TenantID:                       fixture.tenantID,
		WorkspaceScope:                 thirdCompilation.WorkspaceScope,
		AgentScope:                     agent,
		ContextPolicyRef:               thirdCompilation.ContextPolicy,
		ContextPolicyDocumentCanonical: policy.CanonicalBytes,
		ModelParameters:                request.Parameters,
		ConversationHistoryTurns: []contextcompiler.ConversationHistoryTurnV1{
			firstHistory,
			secondHistory,
		},
		ConversationSummaryCandidate: nil,
		TaskInputRef:                 taskDigest,
		TaskInputCanonical:           taskCanonical,
	})
	if err != nil {
		t.Fatalf("fresh third-turn CompileV1: %v", err)
	}
	return compiled
}

func summaryIntegrationHistoryTurn(
	t *testing.T,
	turnIndex uint64,
	input ChatInput,
	result ChatResult,
) contextcompiler.ConversationHistoryTurnV1 {
	t.Helper()
	_, userCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          input.Message,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	userDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentTaskInput,
		chatJSONMediaType,
		userCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	assistantDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModelResult,
		chatJSONMediaType,
		result.TerminalResult.OutputCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return contextcompiler.ConversationHistoryTurnV1{
		TurnIndex:                    turnIndex,
		UserSourceContentDigest:      userDigest,
		AssistantSourceContentDigest: assistantDigest,
		UserMessage: moduleapi.ModelMessageV1{
			Role: moduleapi.ModelRoleUser, Content: input.Message,
		},
		AssistantMessage: moduleapi.ModelMessageV1{
			Role: moduleapi.ModelRoleAssistant, Content: result.Reply,
		},
	}
}

func openSummaryIntegrationDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	return database
}

func execSummaryIntegrationClosedFileTamper(
	t *testing.T,
	database *sql.DB,
	statement string,
	arguments ...any,
) sql.Result {
	t.Helper()
	const trigger = `model_dispatch_attempts_observation_update_guard`
	ctx := context.Background()
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	var triggerDefinition string
	if err := connection.QueryRowContext(
		ctx,
		`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name=?`,
		trigger,
	).Scan(&triggerDefinition); err != nil {
		t.Fatalf("load closed-file trigger %s: %v", trigger, err)
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(ctx, `PRAGMA ignore_check_constraints=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(
		ctx,
		`DROP TRIGGER model_dispatch_attempts_observation_update_guard`,
	); err != nil {
		t.Fatalf("drop closed-file trigger %s: %v", trigger, err)
	}

	result, mutationErr := connection.ExecContext(ctx, statement, arguments...)
	_, restoreTriggerErr := connection.ExecContext(ctx, triggerDefinition)
	_, restoreChecksErr := connection.ExecContext(ctx, `PRAGMA ignore_check_constraints=OFF`)
	_, restoreForeignKeysErr := connection.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
	if restoreTriggerErr != nil {
		t.Fatalf("restore closed-file trigger %s: %v", trigger, restoreTriggerErr)
	}
	if restoreChecksErr != nil {
		t.Fatalf("restore check constraints: %v", restoreChecksErr)
	}
	if restoreForeignKeysErr != nil {
		t.Fatalf("restore foreign keys: %v", restoreForeignKeysErr)
	}
	if mutationErr != nil {
		t.Fatalf("apply closed-file tamper: %v", mutationErr)
	}
	return result
}

type summaryTamperModelFootprint struct {
	Runs          int
	Frames        int
	Attempts      int
	Usage         int
	Pending       int
	DispatchEvent int
}

func summaryTamperModelBoundaryFootprint(
	t *testing.T,
	database *sql.DB,
) summaryTamperModelFootprint {
	t.Helper()
	var footprint summaryTamperModelFootprint
	if err := database.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM runs),
			(SELECT COUNT(*) FROM loop_frames),
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM model_usage),
			(SELECT COUNT(*) FROM model_dispatch_attempts
			 WHERE state='PENDING'),
			(SELECT COUNT(*) FROM run_events
			 WHERE event_kind=?)
	`,
		corecontract.ModelDispatchPendingEventKind,
	).Scan(
		&footprint.Runs,
		&footprint.Frames,
		&footprint.Attempts,
		&footprint.Usage,
		&footprint.Pending,
		&footprint.DispatchEvent,
	); err != nil {
		t.Fatal(err)
	}
	return footprint
}
