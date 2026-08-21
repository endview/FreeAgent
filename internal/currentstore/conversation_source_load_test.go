package currentstore

import (
	"bytes"
	"context"
	"testing"
)

func TestCloneRunForLoopDetachesSourceContextCompilation(t *testing.T) {
	original := RunForLoop{
		ConversationHistory: []ConversationHistoryTurnRecord{{
			SourceContextCompilationAttemptID: "source-attempt",
			SourceContextCompilation: &ContentRecord{
				Digest:         "source-compilation-digest",
				CanonicalBytes: []byte(`{"schema_version":"context-compilation/v1"}`),
			},
		}},
	}
	cloned := cloneRunForLoop(original)
	if cloned.ConversationHistory[0].SourceContextCompilation == nil {
		t.Fatal("cloned source ContextCompilation is nil")
	}
	cloned.ConversationHistory[0].SourceContextCompilation.Digest = "changed"
	cloned.ConversationHistory[0].SourceContextCompilationAttemptID = "changed-attempt"
	cloned.ConversationHistory[0].SourceContextCompilation.CanonicalBytes[0] = 'x'
	if original.ConversationHistory[0].SourceContextCompilation.Digest !=
		"source-compilation-digest" ||
		original.ConversationHistory[0].SourceContextCompilationAttemptID !=
			"source-attempt" ||
		original.ConversationHistory[0].SourceContextCompilation.CanonicalBytes[0] != '{' {
		t.Fatal("cloneRunForLoop aliases source ContextCompilation")
	}
}

func TestLoadLoopConversationHistoryFollowsLatestExactSourceCompilation(
	t *testing.T,
) {
	fixture := prepareDynamicKnowledgeAdmission(t, nil)
	widenDynamicContextConversationPolicy(t, fixture.admission)
	conversationID := "conversation-source-compilation"
	createConversationForAdmission(t, fixture.admission, conversationID)

	firstInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"conversation-source-compilation-first",
		0,
		"",
		"run-conversation-source-compilation-first",
	)
	if _, err := fixture.admission.store.CommitConversationTurnAdmission(
		context.Background(),
		firstInput,
	); err != nil {
		t.Fatal(err)
	}
	putDynamicContextTestPrice(t, fixture.admission.store)
	firstLease, firstRun := acquireDynamicContextConversationRun(
		t,
		fixture.admission.store,
		"run-conversation-source-compilation-first",
		"conversation-source-compilation-first-worker",
	)
	firstCompiled := compileKnowledgeRequestForRun(t, fixture, firstRun)
	completeDynamicContextConversationAttempt(
		t,
		fixture.admission.store,
		firstRun,
		firstLease,
		"attempt-conversation-source-compilation-first",
		firstCompiled,
	)

	secondInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"conversation-source-compilation-second",
		1,
		"run-conversation-source-compilation-first",
		"run-conversation-source-compilation-second",
	)
	if _, err := fixture.admission.store.CommitConversationTurnAdmission(
		context.Background(),
		secondInput,
	); err != nil {
		t.Fatal(err)
	}
	secondLease, secondRun := acquireDynamicContextConversationRun(
		t,
		fixture.admission.store,
		"run-conversation-source-compilation-second",
		"conversation-source-compilation-second-worker",
	)
	assertOneConversationPredecessor(t, secondRun)
	source := secondRun.ConversationHistory[0].SourceContextCompilation
	if source == nil || !bytes.Equal(
		source.CanonicalBytes,
		firstCompiled.CompilationCanonical,
	) {
		t.Fatal("latest exact source ContextCompilation was not loaded")
	}

	// The public recovery projection must not alias either the query result or
	// a later LoadRunForLoop result.
	source.CanonicalBytes[0] ^= 0xff
	reloaded, err := fixture.admission.store.LoadRunForLoop(
		context.Background(),
		secondLease,
	)
	if err != nil {
		t.Fatal(err)
	}
	reloadedSource := reloaded.ConversationHistory[0].SourceContextCompilation
	if reloadedSource == nil || !bytes.Equal(
		reloadedSource.CanonicalBytes,
		firstCompiled.CompilationCanonical,
	) {
		t.Fatal("source ContextCompilation recovery projection is not detached")
	}
}

func TestLoadLoopConversationHistoryDoesNotCrossLatestExactSourceWithoutCompilation(
	t *testing.T,
) {
	fixture := prepareDynamicKnowledgeAdmission(t, nil)
	widenDynamicContextConversationPolicy(t, fixture.admission)
	conversationID := "conversation-source-no-cross"
	createConversationForAdmission(t, fixture.admission, conversationID)
	putDynamicContextTestPrice(t, fixture.admission.store)

	firstInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"conversation-source-no-cross-first",
		0,
		"",
		"run-conversation-source-no-cross-first",
	)
	if _, err := fixture.admission.store.CommitConversationTurnAdmission(
		context.Background(),
		firstInput,
	); err != nil {
		t.Fatal(err)
	}
	firstLease, firstRun := acquireDynamicContextConversationRun(
		t,
		fixture.admission.store,
		"run-conversation-source-no-cross-first",
		"conversation-source-no-cross-first-worker",
	)
	firstCompiled := compileKnowledgeRequestForRun(t, fixture, firstRun)
	completeDynamicContextConversationAttempt(
		t,
		fixture.admission.store,
		firstRun,
		firstLease,
		"attempt-conversation-source-no-cross-first",
		firstCompiled,
	)

	secondInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"conversation-source-no-cross-second",
		1,
		"run-conversation-source-no-cross-first",
		"run-conversation-source-no-cross-second",
	)
	if _, err := fixture.admission.store.CommitConversationTurnAdmission(
		context.Background(),
		secondInput,
	); err != nil {
		t.Fatal(err)
	}
	secondLease, secondRun := acquireDynamicContextConversationRun(
		t,
		fixture.admission.store,
		"run-conversation-source-no-cross-second",
		"conversation-source-no-cross-second-worker",
	)
	secondCompiled := compileKnowledgeRequestForRun(t, fixture, secondRun)
	const secondAttemptID = "attempt-conversation-source-no-cross-second"
	completeDynamicContextConversationAttempt(
		t,
		fixture.admission.store,
		secondRun,
		secondLease,
		secondAttemptID,
		secondCompiled,
	)
	execClosedFileTamperV1(
		t,
		fixture.admission.store,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`UPDATE model_dispatch_attempts
		 SET context_compilation_ref=NULL
		 WHERE attempt_id=?`,
		secondAttemptID,
	)

	thirdInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"conversation-source-no-cross-third",
		2,
		"run-conversation-source-no-cross-second",
		"run-conversation-source-no-cross-third",
	)
	if _, err := fixture.admission.store.CommitConversationTurnAdmission(
		context.Background(),
		thirdInput,
	); err != nil {
		t.Fatal(err)
	}
	_, thirdRun := acquireDynamicContextConversationRun(
		t,
		fixture.admission.store,
		"run-conversation-source-no-cross-third",
		"conversation-source-no-cross-third-worker",
	)
	if len(thirdRun.ConversationHistory) != 2 {
		t.Fatalf(
			"Conversation History count=%d, want 2",
			len(thirdRun.ConversationHistory),
		)
	}
	for _, history := range thirdRun.ConversationHistory {
		if history.SourceContextCompilation != nil {
			t.Fatalf(
				"source candidate crossed latest exact Attempt: turn=%d attempt=%q",
				history.TurnIndex,
				history.SourceAttemptID,
			)
		}
	}
}
