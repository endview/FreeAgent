package currentbackup

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestKnowledgeReuseSourceClosureAcceptsExactLatestFreshRetrieval(
	t *testing.T,
) {
	runs, _ := newKnowledgeReuseSemanticFixture()
	if err := inspectKnowledgeReuseSourceClosures(runs); err != nil {
		t.Fatalf("exact latest fresh retrieval was rejected: %v", err)
	}
}

func TestKnowledgeReuseSourceClosureRejectsBrokenEdges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]*coreRunSemanticState, *corecontract.KnowledgeReuseEvidenceV1)
	}{
		{
			name: "non Conversation current Run",
			mutate: func(runs map[string]*coreRunSemanticState, _ *corecontract.KnowledgeReuseEvidenceV1) {
				runs["run-current"].manifest.ConversationTurn = nil
			},
		},
		{
			name: "wrong Conversation",
			mutate: func(_ map[string]*coreRunSemanticState, reuse *corecontract.KnowledgeReuseEvidenceV1) {
				reuse.SourceConversationID = "conversation-other"
			},
		},
		{
			name: "wrong turn",
			mutate: func(_ map[string]*coreRunSemanticState, reuse *corecontract.KnowledgeReuseEvidenceV1) {
				reuse.SourceTurnIndex++
			},
		},
		{
			name: "wrong Run",
			mutate: func(_ map[string]*coreRunSemanticState, reuse *corecontract.KnowledgeReuseEvidenceV1) {
				reuse.SourceRunID = "run-other"
			},
		},
		{
			name: "wrong Attempt",
			mutate: func(_ map[string]*coreRunSemanticState, reuse *corecontract.KnowledgeReuseEvidenceV1) {
				reuse.SourceAttemptID = "attempt-other"
			},
		},
		{
			name: "source Attempt not SUCCEEDED",
			mutate: func(runs map[string]*coreRunSemanticState, _ *corecontract.KnowledgeReuseEvidenceV1) {
				runs["run-source"].attempts["attempt-source"].state =
					corecontract.ModelAttemptFailed
			},
		},
		{
			name: "source Attempt not terminal",
			mutate: func(runs map[string]*coreRunSemanticState, _ *corecontract.KnowledgeReuseEvidenceV1) {
				runs["run-source"].frame.continuation.AttemptID = "attempt-other"
			},
		},
		{
			name: "wrong Compilation ref",
			mutate: func(_ map[string]*coreRunSemanticState, reuse *corecontract.KnowledgeReuseEvidenceV1) {
				reuse.SourceCompilationRef = strings.Repeat("e", 64)
			},
		},
		{
			name: "missing source Compilation",
			mutate: func(runs map[string]*coreRunSemanticState, _ *corecontract.KnowledgeReuseEvidenceV1) {
				runs["run-source"].attempts["attempt-source"].compilation = nil
			},
		},
		{
			name: "nested fresh retrieval differs",
			mutate: func(_ map[string]*coreRunSemanticState, reuse *corecontract.KnowledgeReuseEvidenceV1) {
				reuse.FreshRetrieval.OutputDigest = strings.Repeat("f", 64)
			},
		},
		{
			name: "source reuse cannot substitute for fresh retrieval",
			mutate: func(runs map[string]*coreRunSemanticState, reuse *corecontract.KnowledgeReuseEvidenceV1) {
				source := runs["run-source"].attempts["attempt-source"].compilation
				source.KnowledgeRetrievals = nil
				source.KnowledgeReuses = []corecontract.KnowledgeReuseEvidenceV1{*reuse}
			},
		},
		{
			name: "source shortcut cannot substitute for fresh retrieval",
			mutate: func(runs map[string]*coreRunSemanticState, _ *corecontract.KnowledgeReuseEvidenceV1) {
				source := runs["run-source"].attempts["attempt-source"].compilation
				source.KnowledgeRetrievals = nil
				source.KnowledgeShortcuts = []corecontract.KnowledgeShortcutEvidenceV1{{
					BindingIndex: 2,
					Mode:         corecontract.KnowledgeShortcutNotSelectedV1,
				}}
			},
		},
		{
			name: "no earlier exact TaskInput",
			mutate: func(runs map[string]*coreRunSemanticState, _ *corecontract.KnowledgeReuseEvidenceV1) {
				runs["run-current"].manifest.TaskInputRef = strings.Repeat("9", 64)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runs, reuse := newKnowledgeReuseSemanticFixture()
			test.mutate(runs, reuse)
			err := inspectKnowledgeReuseSourceClosures(runs)
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf("broken reuse edge error=%v, want ErrIntegrity", err)
			}
		})
	}
}

func TestKnowledgeReuseSourceClosureDoesNotFallThroughLatestExactTurn(
	t *testing.T,
) {
	runs, reuse := newKnowledgeReuseSemanticFixture()
	latest := newKnowledgeReuseSemanticTurn(
		"run-latest",
		2,
		"run-source",
		runs["run-current"].manifest.TaskInputRef,
	)
	latest.frame.continuation = corecontract.LoopContinuationV1{
		State:         corecontract.TerminatedLoopStep,
		AttemptKind:   corecontract.AttemptKindModel,
		AttemptID:     "attempt-latest",
		LogicalStepID: "model.generate/latest",
	}
	latest.attempts["attempt-latest"] = &coreModelAttemptState{
		attemptID: "attempt-latest",
		state:     corecontract.ModelAttemptSucceeded,
		resultRef: sql.NullString{String: strings.Repeat("8", 64), Valid: true},
		result:    &moduleapi.ModelGenerateOutputV1{},
	}
	latest.historyByAttempt["attempt-latest"] = 1
	runs["run-latest"] = latest
	runs["run-current"].manifest.ConversationTurn.TurnIndex = 3
	runs["run-current"].manifest.ConversationTurn.PredecessorRunID = "run-latest"
	runs["run-current"].row.conversationTurnIndex.Int64 = 3
	runs["run-current"].row.conversationPrevious = sql.NullString{
		String: "run-latest",
		Valid:  true,
	}

	// The evidence still points to the older valid fresh retrieval. The newer
	// exact-question turn has no Compilation, so verification must fail closed.
	if reuse.SourceRunID != "run-source" {
		t.Fatal("fixture no longer points to the older source")
	}
	err := inspectKnowledgeReuseSourceClosures(runs)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("fallback to older exact turn error=%v, want ErrIntegrity", err)
	}
}

func newKnowledgeReuseSemanticFixture() (
	map[string]*coreRunSemanticState,
	*corecontract.KnowledgeReuseEvidenceV1,
) {
	taskInputRef := strings.Repeat("1", 64)
	compilationRef := strings.Repeat("2", 64)
	retrieval := corecontract.KnowledgeRetrievalEvidenceV1{
		BindingIndex:        2,
		ConfigRef:           strings.Repeat("3", 64),
		AuthorityCeilingRef: strings.Repeat("4", 64),
		RequestDigest:       strings.Repeat("5", 64),
		Hits: []moduleapi.KnowledgeHitV1{{
			ChunkID:     "chunk-1",
			ChunkDigest: strings.Repeat("6", 64),
			Text:        "frozen knowledge",
		}},
		OutputDigest: strings.Repeat("7", 64),
		Provenance: &corecontract.KnowledgeRetrievalProvenanceV1{
			RoutingAlgorithmVersion: "routing.test/v1",
			DecisionSetDigest:       strings.Repeat("a", 64),
			RetrievedAtUnixMS:       1,
		},
	}
	source := newKnowledgeReuseSemanticTurn(
		"run-source",
		1,
		"",
		taskInputRef,
	)
	sourceAttempt := &coreModelAttemptState{
		attemptID: "attempt-source",
		state:     corecontract.ModelAttemptSucceeded,
		contextCompilationRef: sql.NullString{
			String: compilationRef,
			Valid:  true,
		},
		resultRef: sql.NullString{String: strings.Repeat("b", 64), Valid: true},
		result:    &moduleapi.ModelGenerateOutputV1{},
		compilation: &corecontract.ContextCompilationV1{
			KnowledgeRetrievals: []corecontract.KnowledgeRetrievalEvidenceV1{
				retrieval,
			},
		},
	}
	source.attempts[sourceAttempt.attemptID] = sourceAttempt
	source.historyByAttempt[sourceAttempt.attemptID] = 1
	source.frame.continuation = corecontract.LoopContinuationV1{
		State:         corecontract.TerminatedLoopStep,
		AttemptKind:   corecontract.AttemptKindModel,
		AttemptID:     sourceAttempt.attemptID,
		LogicalStepID: "model.generate/source",
	}

	current := newKnowledgeReuseSemanticTurn(
		"run-current",
		2,
		"run-source",
		taskInputRef,
	)
	reuse := corecontract.KnowledgeReuseEvidenceV1{
		SourceConversationID: "conversation-unit",
		SourceTurnIndex:      1,
		SourceRunID:          "run-source",
		SourceAttemptID:      sourceAttempt.attemptID,
		SourceCompilationRef: compilationRef,
		FreshRetrieval:       retrieval,
	}
	current.attempts["attempt-current"] = &coreModelAttemptState{
		attemptID: "attempt-current",
		state:     corecontract.ModelAttemptPending,
		compilation: &corecontract.ContextCompilationV1{
			KnowledgeReuses: []corecontract.KnowledgeReuseEvidenceV1{reuse},
		},
	}
	storedReuse := &current.attempts["attempt-current"].compilation.KnowledgeReuses[0]
	return map[string]*coreRunSemanticState{
		"run-source":  source,
		"run-current": current,
	}, storedReuse
}

func newKnowledgeReuseSemanticTurn(
	runID string,
	turnIndex uint64,
	predecessor string,
	taskInputRef string,
) *coreRunSemanticState {
	run := newConversationSemanticUnitRun(
		runID,
		int64(turnIndex),
		predecessor,
		corecontract.InitialLoopStep,
	)
	run.manifest.TaskInputRef = taskInputRef
	return run
}
