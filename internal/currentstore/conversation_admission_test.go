package currentstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestCommitConversationTurnAdmissionPublishesFirstTurnAndProjection(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	createConversationForAdmission(t, fixture, "conversation-first")
	input := compileConversationTurnAdmission(
		t,
		fixture,
		"conversation-first",
		"conversation-first-key",
		0,
		"",
		"run-conversation-first",
	)

	result, err := fixture.store.CommitConversationTurnAdmission(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("CommitConversationTurnAdmission: %v", err)
	}
	if !result.Created || result.RunID != "run-conversation-first" {
		t.Fatalf("result=%+v", result)
	}
	conversation, err := fixture.store.GetConversation(
		context.Background(),
		fixture.intent.TenantID,
		"conversation-first",
	)
	if err != nil {
		t.Fatal(err)
	}
	if conversation.Revision != 1 ||
		conversation.HeadRunID != result.RunID {
		t.Fatalf("conversation=%+v", conversation)
	}
	var conversationID, predecessor string
	var turnIndex uint64
	if err := fixture.store.db.QueryRow(`
		SELECT
			conversation_id,
			conversation_turn_index,
			COALESCE(conversation_predecessor_run_id, '')
		FROM runs
		WHERE run_id=?
	`, result.RunID).Scan(
		&conversationID,
		&turnIndex,
		&predecessor,
	); err != nil {
		t.Fatal(err)
	}
	if conversationID != conversation.ConversationID ||
		turnIndex != 1 || predecessor != "" {
		t.Fatalf(
			"Conversation projection=%q/%d/%q",
			conversationID,
			turnIndex,
			predecessor,
		)
	}
}

func TestCommitRunAdmissionRejectsConversationSlice(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	createConversationForAdmission(t, fixture, "conversation-direct")
	input := compileConversationTurnAdmission(
		t,
		fixture,
		"conversation-direct",
		"conversation-direct-key",
		0,
		"",
		"run-conversation-direct",
	)
	before := admissionCommitCounts(t, fixture.store)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		input,
	); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("ordinary Conversation Admission error=%v", err)
	}
	if after := admissionCommitCounts(t, fixture.store); after != before {
		t.Fatalf("rejected direct path changed rows: before=%v after=%v", before, after)
	}
}

func TestCommitConversationTurnAdmissionExactRetryPrecedesCurrentAndHeadChecks(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	createConversationForAdmission(t, fixture, "conversation-retry")
	input := compileConversationTurnAdmission(
		t,
		fixture,
		"conversation-retry",
		"conversation-retry-key",
		0,
		"",
		"run-conversation-retry",
	)
	created, err := fixture.store.CommitConversationTurnAdmission(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.basis = advancePublishedBasisForTestV1(
		t, fixture.store, fixture.basis, fixture.controlCanonical, fixture.catalogCanonical,
	)
	before := admissionCommitCounts(t, fixture.store)
	retried, err := fixture.store.CommitConversationTurnAdmission(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("historical exact retry: %v", err)
	}
	created.Created = false
	if retried != created {
		t.Fatalf("retried=%+v want=%+v", retried, created)
	}
	if after := admissionCommitCounts(t, fixture.store); after != before {
		t.Fatalf("retry changed rows: before=%v after=%v", before, after)
	}
	conversation, err := fixture.store.GetConversation(
		context.Background(),
		fixture.intent.TenantID,
		"conversation-retry",
	)
	if err != nil || conversation.Revision != 1 ||
		conversation.HeadRunID != retried.RunID {
		t.Fatalf("conversation=%+v error=%v", conversation, err)
	}
}

func TestCommitConversationTurnAdmissionRejectsStaleHeadWithoutRun(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	createConversationForAdmission(t, fixture, "conversation-stale")
	first := compileConversationTurnAdmission(
		t,
		fixture,
		"conversation-stale",
		"conversation-stale-first-key",
		0,
		"",
		"run-conversation-stale-first",
	)
	if _, err := fixture.store.CommitConversationTurnAdmission(
		context.Background(),
		first,
	); err != nil {
		t.Fatal(err)
	}
	stale := compileConversationTurnAdmission(
		t,
		fixture,
		"conversation-stale",
		"conversation-stale-loser-key",
		0,
		"",
		"run-conversation-stale-loser",
	)
	before := admissionCommitCounts(t, fixture.store)
	if _, err := fixture.store.CommitConversationTurnAdmission(
		context.Background(),
		stale,
	); !errors.Is(err, ErrAdmissionConflict) ||
		!errors.Is(err, ErrConversationConflict) {
		t.Fatalf("stale head error=%v", err)
	}
	if after := admissionCommitCounts(t, fixture.store); after != before {
		t.Fatalf("stale CAS changed rows: before=%v after=%v", before, after)
	}
}

func TestCommitConversationTurnAdmissionBlocksPendingPredecessor(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	createConversationForAdmission(t, fixture, "conversation-pending")
	first := compileConversationTurnAdmission(
		t,
		fixture,
		"conversation-pending",
		"conversation-pending-first-key",
		0,
		"",
		"run-conversation-pending-first",
	)
	if _, err := fixture.store.CommitConversationTurnAdmission(
		context.Background(),
		first,
	); err != nil {
		t.Fatal(err)
	}
	second := compileConversationTurnAdmission(
		t,
		fixture,
		"conversation-pending",
		"conversation-pending-second-key",
		1,
		"run-conversation-pending-first",
		"run-conversation-pending-second",
	)
	before := admissionCommitCounts(t, fixture.store)
	if _, err := fixture.store.CommitConversationTurnAdmission(
		context.Background(),
		second,
	); !errors.Is(err, ErrAdmissionConflict) ||
		!errors.Is(err, ErrConversationConflict) {
		t.Fatalf("pending predecessor error=%v", err)
	}
	if after := admissionCommitCounts(t, fixture.store); after != before {
		t.Fatalf("pending predecessor changed rows: before=%v after=%v", before, after)
	}
	conversation, err := fixture.store.GetConversation(
		context.Background(),
		fixture.intent.TenantID,
		"conversation-pending",
	)
	if err != nil || conversation.Revision != 1 ||
		conversation.HeadRunID != "run-conversation-pending-first" {
		t.Fatalf("conversation=%+v error=%v", conversation, err)
	}
}

func TestCommitConversationTurnAdmissionAdmissionKeyConflictStaysDistinct(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	createConversationForAdmission(t, fixture, "conversation-key-conflict")
	first := compileConversationTurnAdmission(
		t,
		fixture,
		"conversation-key-conflict",
		"conversation-shared-key",
		0,
		"",
		"run-conversation-key-first",
	)
	if _, err := fixture.store.CommitConversationTurnAdmission(
		context.Background(),
		first,
	); err != nil {
		t.Fatal(err)
	}
	// Use a distinct Deadline so the shared Admission key has another digest.
	intent := fixture.intent
	intent.AdmissionKey = "conversation-shared-key"
	intent.Deadline = time.Now().UTC().Add(3 * time.Hour).Truncate(time.Microsecond)
	intent.ConversationTurn = &corecontract.ConversationTurnIntentV1{
		SchemaVersion:  corecontract.ConversationTurnIntentSchemaVersionV1,
		ConversationID: "conversation-key-conflict",
	}
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	changed := fixture.compileInput(
		t,
		canonical,
		digest,
		"run-conversation-key-changed",
	)
	if _, err := fixture.store.CommitConversationTurnAdmission(
		context.Background(),
		changed,
	); !errors.Is(err, ErrAdmissionConflict) ||
		errors.Is(err, ErrConversationConflict) {
		t.Fatalf("Admission key conflict classification=%v", err)
	}
}

func TestCommitConversationTurnAdmissionConcurrentCASHasOneWinner(
	t *testing.T,
) {
	fixture := newAdmissionCommitFixture(t)
	createConversationForAdmission(t, fixture, "conversation-race")
	first := compileConversationTurnAdmission(
		t,
		fixture,
		"conversation-race",
		"conversation-race-first-key",
		0,
		"",
		"run-conversation-race-first",
	)
	if _, err := fixture.store.CommitConversationTurnAdmission(
		context.Background(),
		first,
	); err != nil {
		t.Fatal(err)
	}
	completeConversationRunSuccessfully(
		t,
		fixture,
		first,
		"run-conversation-race-first",
	)

	inputs := []CommitRunAdmissionInput{
		compileConversationTurnAdmission(
			t,
			fixture,
			"conversation-race",
			"conversation-race-a-key",
			1,
			"run-conversation-race-first",
			"run-conversation-race-a",
		),
		compileConversationTurnAdmission(
			t,
			fixture,
			"conversation-race",
			"conversation-race-b-key",
			1,
			"run-conversation-race-first",
			"run-conversation-race-b",
		),
	}
	type outcome struct {
		result RunAdmissionResult
		err    error
	}
	outcomes := make(chan outcome, len(inputs))
	var workers sync.WaitGroup
	for _, input := range inputs {
		input := input
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := fixture.store.CommitConversationTurnAdmission(
				context.Background(),
				input,
			)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	workers.Wait()
	close(outcomes)

	var winner RunAdmissionResult
	var successes, conflicts int
	for outcome := range outcomes {
		switch {
		case outcome.err == nil:
			successes++
			winner = outcome.result
		case errors.Is(outcome.err, ErrAdmissionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent outcome: %+v", outcome)
		}
	}
	if successes != 1 || conflicts != 1 || !winner.Created {
		t.Fatalf(
			"successes=%d conflicts=%d winner=%+v",
			successes,
			conflicts,
			winner,
		)
	}
	conversation, err := fixture.store.GetConversation(
		context.Background(),
		fixture.intent.TenantID,
		"conversation-race",
	)
	if err != nil || conversation.Revision != 2 ||
		conversation.HeadRunID != winner.RunID {
		t.Fatalf("conversation=%+v winner=%+v error=%v", conversation, winner, err)
	}
	var runCount int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM runs WHERE conversation_id=?
	`, conversation.ConversationID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 2 {
		t.Fatalf("Conversation Run count=%d, want 2", runCount)
	}
}

func createConversationForAdmission(
	t *testing.T,
	fixture *admissionCommitFixture,
	conversationID string,
) {
	t.Helper()
	result, err := fixture.store.CreateConversation(
		context.Background(),
		CreateConversationInput{
			ConversationID: conversationID,
			TenantID:       fixture.intent.TenantID,
			PrincipalID:    fixture.intent.PrincipalID,
			WorkspaceID:    fixture.intent.WorkspaceID,
			AgentID:        fixture.intent.AgentID,
			ProfileID:      fixture.intent.ProfileID,
		},
	)
	if err != nil || !result.Created {
		t.Fatalf("CreateConversation result=%+v error=%v", result, err)
	}
}

func compileConversationTurnAdmission(
	t *testing.T,
	fixture *admissionCommitFixture,
	conversationID string,
	admissionKey string,
	expectedRevision uint64,
	expectedHeadRunID string,
	runID string,
) CommitRunAdmissionInput {
	t.Helper()
	intent := fixture.intent
	intent.AdmissionKey = admissionKey
	intent.Deadline = time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	intent.ConversationTurn = &corecontract.ConversationTurnIntentV1{
		SchemaVersion:                corecontract.ConversationTurnIntentSchemaVersionV1,
		ConversationID:               conversationID,
		ExpectedConversationRevision: expectedRevision,
		ExpectedHeadRunID:            expectedHeadRunID,
	}
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	return fixture.compileInput(t, canonical, digest, runID)
}

func completeConversationRunSuccessfully(
	t *testing.T,
	fixture *admissionCommitFixture,
	input CommitRunAdmissionInput,
	runID string,
) {
	t.Helper()
	if _, err := fixture.store.PutModelPriceSnapshot(
		context.Background(),
		testModelPriceSnapshot(),
	); err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 runID,
			OwnerID:               "conversation-loop-worker",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := fixture.store.LoadRunForLoop(
		context.Background(),
		lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := recompileContextForNewAttempt(
		corecontract.ContextCompilationV1{},
		run,
		run.Frame.Revision,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "attempt-conversation-success",
			LogicalStepID:               "conversation-reply-1",
			ContextCompilationCanonical: compiled.CompilationCanonical,
			RequestCanonical:            compiled.RequestCanonical,
			Deadline: run.Manifest.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
		},
	); err != nil {
		t.Fatal(err)
	}
}
