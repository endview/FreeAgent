package contextcompiler

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompileV1ConversationHistoryPreservesCompletePairOrder(
	t *testing.T,
) {
	input := newCompileInput(t, "current question")
	addConversationHistoryTurn(t, &input, "first question", "first answer")
	addConversationHistoryTurn(t, &input, "second question", "second answer")
	before := cloneCompileInput(input)

	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compilation != nil {
		t.Fatalf("below-watermark Conversation compiled=%+v", result.Compilation)
	}
	want := []moduleapi.ModelMessageV1{
		{Role: moduleapi.ModelRoleUser, Content: "first question"},
		{Role: moduleapi.ModelRoleAssistant, Content: "first answer"},
		{Role: moduleapi.ModelRoleUser, Content: "second question"},
		{Role: moduleapi.ModelRoleAssistant, Content: "second answer"},
		{Role: moduleapi.ModelRoleUser, Content: "current question"},
	}
	assertExactMessages(t, result.Request.Messages, want)
	if !reflect.DeepEqual(input, before) {
		t.Fatal("CompileV1 mutated caller-owned Conversation History")
	}
}

func TestCompileV1NilConversationSummaryCandidatePreservesCanonicalCanary(
	t *testing.T,
) {
	input := newCompileInput(t, "current")
	addConversationHistoryTurn(t, &input, "question", "answer")
	input.ConversationSummaryCandidate = nil

	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"messages":[{"content":"question","role":"USER"},{"content":"answer","role":"ASSISTANT"},{"content":"current","role":"USER"}],"parameters":{"temperature":0},"schema_version":"model-generate-request/v1"}`)
	if result.Compilation != nil || !bytes.Equal(result.RequestCanonical, want) {
		t.Fatalf(
			"nil-candidate Conversation canonical changed\ngot  %s\nwant %s",
			result.RequestCanonical,
			want,
		)
	}
}

func TestCompileV1ConversationAt85SummarizesOldestCompletePair(
	t *testing.T,
) {
	input := newCompileInput(t, strings.Repeat("current-task-", 40))
	firstUser, firstAssistant := addConversationHistoryTurn(
		t,
		&input,
		strings.Repeat("first-question-", 240),
		strings.Repeat("first-answer-", 240),
	)
	addConversationHistoryTurn(
		t,
		&input,
		"second question",
		"second answer",
	)
	estimate := estimateCompileInput(t, input)
	setContextPolicy(t, &input, budgetForExactWatermark(t, estimate), 0)

	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compilation == nil || result.Compilation.Summary == nil ||
		len(result.Compilation.Drops) != 0 ||
		result.Compilation.OriginalEstimateTokens != estimate ||
		result.Compilation.RestoreWatermarkTokens != estimate {
		t.Fatalf("Conversation 85%% compilation=%+v", result.Compilation)
	}
	firstTurn, err := corecontract.ContextConversationTurnDigestV1(
		1,
		firstUser,
		firstAssistant,
	)
	if err != nil {
		t.Fatal(err)
	}
	if sources := result.Compilation.Summary.SourceTurnDigests; len(sources) != 1 || sources[0] != firstTurn {
		t.Fatalf("Conversation summary sources=%v, want [%s]", sources, firstTurn)
	}
	if len(result.Request.Messages) != 4 ||
		result.Request.Messages[0].Role != moduleapi.ModelRoleAssistant ||
		!strings.HasPrefix(
			result.Request.Messages[0].Content,
			"Earlier History (deterministic extract):\n",
		) {
		t.Fatalf("Conversation summary request=%+v", result.Request.Messages)
	}
	assertExactMessages(
		t,
		result.Request.Messages[1:],
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleUser, Content: "second question"},
			{Role: moduleapi.ModelRoleAssistant, Content: "second answer"},
			{Role: moduleapi.ModelRoleUser, Content: strings.Repeat("current-task-", 40)},
		},
	)
}

func TestCompileV1ConversationAt100DropsMinimalCompletePairPrefix(
	t *testing.T,
) {
	input := newCompileInput(t, strings.Repeat("protected-task-", 360))
	firstUser, firstAssistant := addConversationHistoryTurn(
		t,
		&input,
		strings.Repeat("old-user-one-", 130),
		strings.Repeat("old-assistant-one-", 130),
	)
	secondUser, secondAssistant := addConversationHistoryTurn(
		t,
		&input,
		strings.Repeat("old-user-two-", 130),
		strings.Repeat("old-assistant-two-", 130),
	)
	addConversationHistoryTurn(
		t,
		&input,
		"retained third user",
		"retained third assistant",
	)

	policy, err := restorePolicy(input)
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := canonicalParameters(input.ModelParameters)
	if err != nil {
		t.Fatal(err)
	}
	units, _, _, _, _, err := restoreUnits(input, policy)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := estimateUnits(units, parameters)
	if err != nil {
		t.Fatal(err)
	}
	afterOneUnits := removeUnit(units, historyUnitIndexes(units)[0])
	afterOne, err := estimateUnits(afterOneUnits, parameters)
	if err != nil {
		t.Fatal(err)
	}
	afterTwoUnits := removeUnit(
		afterOneUnits,
		historyUnitIndexes(afterOneUnits)[0],
	)
	afterTwo, err := estimateUnits(afterTwoUnits, parameters)
	if err != nil {
		t.Fatal(err)
	}
	budget := budgetWhoseWatermarkSeparates(t, afterTwo, afterOne, initial)
	setContextPolicy(t, &input, budget, 0)

	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Compilation == nil || result.Compilation.Summary != nil ||
		result.Compilation.StopReason != corecontract.ContextCompilationDropToWatermark ||
		len(result.Compilation.Drops) != 2 ||
		result.Compilation.Drops[0].BeforeEstimateTokens != initial ||
		result.Compilation.Drops[0].AfterEstimateTokens != afterOne ||
		result.Compilation.Drops[1].BeforeEstimateTokens != afterOne ||
		result.Compilation.Drops[1].AfterEstimateTokens != afterTwo ||
		afterOne <= result.Compilation.RestoreWatermarkTokens ||
		afterTwo > result.Compilation.RestoreWatermarkTokens {
		t.Fatalf("Conversation minimal Drop compilation=%+v", result.Compilation)
	}
	firstTurn, _ := corecontract.ContextConversationTurnDigestV1(
		1,
		firstUser,
		firstAssistant,
	)
	secondTurn, _ := corecontract.ContextConversationTurnDigestV1(
		2,
		secondUser,
		secondAssistant,
	)
	if result.Compilation.Drops[0].UnitDigest != firstTurn ||
		result.Compilation.Drops[1].UnitDigest != secondTurn {
		t.Fatalf("Conversation Drop order=%+v", result.Compilation.Drops)
	}
	assertExactMessages(
		t,
		result.Request.Messages,
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleUser, Content: "retained third user"},
			{Role: moduleapi.ModelRoleAssistant, Content: "retained third assistant"},
			{Role: moduleapi.ModelRoleUser, Content: strings.Repeat("protected-task-", 360)},
		},
	)
}

func TestCompileV1ConversationSummaryCandidateReusesOnlyExactSoftPrefix(
	t *testing.T,
) {
	t.Run("exact candidate", func(t *testing.T) {
		input, fresh := conversationSoftSummaryFixture(t, 1)
		candidate := conversationSummaryCandidateForPrefix(t, input, 1)
		// These are deliberately stale and even internally inconsistent. M2
		// inherits only source/text and recomputes the token chain for this Run.
		candidate.BeforeEstimateTokens = 1
		candidate.AfterEstimateTokens = 2
		input.ConversationSummaryCandidate = candidate
		candidateBefore := *candidate
		candidateBefore.SourceTurnDigests = append(
			[]string(nil),
			candidate.SourceTurnDigests...,
		)

		got, err := CompileV1(input)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.RequestCanonical, fresh.RequestCanonical) ||
			!bytes.Equal(got.CompilationCanonical, fresh.CompilationCanonical) {
			t.Fatalf(
				"exact candidate changed fresh bytes\nrequest got  %s\nrequest want %s\ncompilation got  %s\ncompilation want %s",
				got.RequestCanonical,
				fresh.RequestCanonical,
				got.CompilationCanonical,
				fresh.CompilationCanonical,
			)
		}
		if got.Compilation == nil || got.Compilation.Summary == nil ||
			got.Compilation.Summary.BeforeEstimateTokens !=
				fresh.Compilation.Summary.BeforeEstimateTokens ||
			got.Compilation.Summary.AfterEstimateTokens !=
				fresh.Compilation.Summary.AfterEstimateTokens {
			t.Fatalf("current estimate chain was not recomputed: %+v", got.Compilation)
		}
		if !reflect.DeepEqual(*candidate, candidateBefore) {
			t.Fatalf("CompileV1 mutated caller candidate: got=%+v want=%+v", *candidate, candidateBefore)
		}

		frozenDigest := got.Compilation.Summary.SourceTurnDigests[0]
		candidate.SourceTurnDigests[0] = testDigest("f")
		if got.Compilation.Summary.SourceTurnDigests[0] != frozenDigest {
			t.Fatal("result Summary aliases the caller-owned candidate digest slice")
		}
		candidate.SourceTurnDigests[0] = candidateBefore.SourceTurnDigests[0]
		got.Compilation.Summary.SourceTurnDigests[0] = testDigest("e")
		if candidate.SourceTurnDigests[0] != candidateBefore.SourceTurnDigests[0] {
			t.Fatal("caller-owned candidate aliases the returned Summary digest slice")
		}
	})

	t.Run("larger candidate falls back fresh", func(t *testing.T) {
		input, fresh := conversationSoftSummaryFixture(t, 1)
		candidate := conversationSummaryCandidateForPrefix(t, input, 2)
		input.ConversationSummaryCandidate = candidate

		got, err := CompileV1(input)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.RequestCanonical, fresh.RequestCanonical) ||
			!bytes.Equal(got.CompilationCanonical, fresh.CompilationCanonical) {
			t.Fatal("larger valid candidate changed the fresh deterministic result")
		}
		if len(got.Compilation.Summary.SourceTurnDigests) != 1 ||
			got.Compilation.Summary.Text == candidate.Text {
			t.Fatalf("larger candidate was used for a smaller selected range: %+v", got.Compilation.Summary)
		}
	})

	t.Run("smaller candidate falls back fresh", func(t *testing.T) {
		input, fresh := conversationSoftSummaryFixture(t, 2)
		candidate := conversationSummaryCandidateForPrefix(t, input, 1)
		input.ConversationSummaryCandidate = candidate

		got, err := CompileV1(input)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.RequestCanonical, fresh.RequestCanonical) ||
			!bytes.Equal(got.CompilationCanonical, fresh.CompilationCanonical) {
			t.Fatal("smaller valid candidate changed the fresh deterministic result")
		}
		if len(got.Compilation.Summary.SourceTurnDigests) != 2 ||
			got.Compilation.Summary.Text == candidate.Text {
			t.Fatalf("smaller candidate was used for a larger selected range: %+v", got.Compilation.Summary)
		}
	})
}

func TestCompileV1ConversationSummaryCandidateThresholdIsolation(
	t *testing.T,
) {
	invalid := &corecontract.ContextCompilationSummaryV1{
		SourceTurnDigests: []string{},
		Text:              "not a deterministic summary",
	}

	t.Run("below 85 percent ignores candidate", func(t *testing.T) {
		input := newCompileInput(t, "current")
		addConversationHistoryTurn(t, &input, "question", "answer")
		input.ConversationSummaryCandidate = invalid
		result, err := CompileV1(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Compilation != nil {
			t.Fatalf("below-watermark candidate caused compression: %+v", result.Compilation)
		}
	})

	t.Run("at 100 percent ignores candidate and drops", func(t *testing.T) {
		input := newCompileInput(t, "current")
		addConversationHistoryTurn(
			t,
			&input,
			strings.Repeat("old-user-", 220),
			strings.Repeat("old-assistant-", 220),
		)
		addConversationHistoryTurn(
			t,
			&input,
			strings.Repeat("second-user-", 100),
			strings.Repeat("second-assistant-", 100),
		)
		initial := estimateCompileInput(t, input)
		setContextPolicy(t, &input, initial, 0)
		input.ConversationSummaryCandidate = invalid

		result, err := CompileV1(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Compilation == nil || result.Compilation.Summary != nil ||
			result.Compilation.StopReason !=
				corecontract.ContextCompilationDropToWatermark ||
			len(result.Compilation.Drops) != 1 ||
			result.Compilation.Drops[0].BeforeEstimateTokens != initial ||
			result.Compilation.Drops[0].AfterEstimateTokens >
				result.Compilation.RestoreWatermarkTokens {
			t.Fatalf("full-budget candidate changed Drop semantics: %+v", result.Compilation)
		}
	})
}

func TestCompileV1ConversationSummaryCandidateRejectsInvalidSoftMaterial(
	t *testing.T,
) {
	input, _ := conversationSoftSummaryFixture(t, 1)
	valid := conversationSummaryCandidateForPrefix(t, input, 1)

	tests := []struct {
		name   string
		mutate func(*corecontract.ContextCompilationSummaryV1)
	}{
		{
			name: "empty source range",
			mutate: func(candidate *corecontract.ContextCompilationSummaryV1) {
				candidate.SourceTurnDigests = []string{}
			},
		},
		{
			name: "non-prefix source",
			mutate: func(candidate *corecontract.ContextCompilationSummaryV1) {
				second, err := freezeConversationHistoryTurn(
					input.ConversationHistoryTurns[1],
				)
				if err != nil {
					t.Fatal(err)
				}
				candidate.SourceTurnDigests[0] = second.digest
			},
		},
		{
			name: "non-deterministic text",
			mutate: func(candidate *corecontract.ContextCompilationSummaryV1) {
				candidate.Text += "tampered"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := *valid
			candidate.SourceTurnDigests = append(
				[]string(nil),
				valid.SourceTurnDigests...,
			)
			test.mutate(&candidate)
			trial := cloneCompileInput(input)
			trial.ConversationSummaryCandidate = &candidate
			if _, err := CompileV1(trial); !errors.Is(err, ErrInvalidContextInput) {
				t.Fatalf("invalid candidate error=%v", err)
			}
		})
	}
}

func TestCompileV1ConversationSummaryCandidateRejectsLegacyHistory(
	t *testing.T,
) {
	input := newCompileInput(t, "current")
	addHistoryTurn(t, &input, "legacy answer")
	input.ConversationSummaryCandidate = &corecontract.ContextCompilationSummaryV1{}
	if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
		t.Fatalf("legacy candidate error=%v", err)
	}
}

func TestCompileV1ConversationRecentHistoryTurnsProtectsWholePair(
	t *testing.T,
) {
	input := newCompileInput(t, "current")
	firstUser, firstAssistant := addConversationHistoryTurn(
		t,
		&input,
		strings.Repeat("old-user-", 220),
		strings.Repeat("old-assistant-", 220),
	)
	addConversationHistoryTurn(
		t,
		&input,
		strings.Repeat("recent-user-", 220),
		strings.Repeat("recent-assistant-", 220),
	)
	initial := estimateCompileInput(t, input)
	setContextPolicy(t, &input, initial, 1)

	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	firstTurn, _ := corecontract.ContextConversationTurnDigestV1(
		1,
		firstUser,
		firstAssistant,
	)
	if result.Compilation == nil || len(result.Compilation.Drops) != 1 ||
		result.Compilation.Drops[0].UnitDigest != firstTurn {
		t.Fatalf("Conversation recent-pair compilation=%+v", result.Compilation)
	}
	assertExactMessages(
		t,
		result.Request.Messages,
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleUser, Content: strings.Repeat("recent-user-", 220)},
			{Role: moduleapi.ModelRoleAssistant, Content: strings.Repeat("recent-assistant-", 220)},
			{Role: moduleapi.ModelRoleUser, Content: "current"},
		},
	)
}

func TestCompileV1ConversationRejectsMixedOrIncompleteHistory(t *testing.T) {
	t.Run("legacy and Conversation", func(t *testing.T) {
		input := newCompileInput(t, "current")
		addHistoryTurn(t, &input, "legacy answer")
		addConversationHistoryTurn(t, &input, "question", "answer")
		if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
			t.Fatalf("mixed History error=%v", err)
		}
	})

	t.Run("message cardinality", func(t *testing.T) {
		input := newCompileInput(t, "current")
		for index := 0; index < maxConversationHistoryTurnsV1; index++ {
			addConversationHistoryTurn(t, &input, "question", "answer")
		}
		if _, err := CompileV1(input); err != nil {
			t.Fatalf("maximum Conversation History turns were rejected: %v", err)
		}
		addConversationHistoryTurn(t, &input, "question", "answer")
		if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
			t.Fatalf("Conversation message-cardinality error=%v", err)
		}
	})

	t.Run("explicit empty legacy representation", func(t *testing.T) {
		input := newCompileInput(t, "current")
		input.HistoryTurns = []HistoryTurnV1{}
		addConversationHistoryTurn(t, &input, "question", "answer")
		if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
			t.Fatalf("explicit dual History representation error=%v", err)
		}
	})

	t.Run("half pair role", func(t *testing.T) {
		input := newCompileInput(t, "current")
		addConversationHistoryTurn(t, &input, "question", "answer")
		input.ConversationHistoryTurns[0].AssistantMessage.Role =
			moduleapi.ModelRoleUser
		if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
			t.Fatalf("half-pair role error=%v", err)
		}
	})

	t.Run("non-contiguous index", func(t *testing.T) {
		input := newCompileInput(t, "current")
		addConversationHistoryTurn(t, &input, "question", "answer")
		input.ConversationHistoryTurns[0].TurnIndex = 2
		if _, err := CompileV1(input); !errors.Is(err, ErrInvalidContextInput) {
			t.Fatalf("non-contiguous turn error=%v", err)
		}
	})
}

func TestCompileV1LegacyHistoryRequestBytesRemainUnchanged(t *testing.T) {
	input := newCompileInput(t, "current")
	addHistoryTurn(t, &input, "legacy answer")
	result, err := CompileV1(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"messages":[{"content":"legacy answer","role":"ASSISTANT"},{"content":"current","role":"USER"}],"parameters":{"temperature":0},"schema_version":"model-generate-request/v1"}`)
	if result.Compilation != nil || !bytes.Equal(result.RequestCanonical, want) {
		t.Fatalf("legacy request bytes changed\ngot  %s\nwant %s", result.RequestCanonical, want)
	}
}

func addConversationHistoryTurn(
	t *testing.T,
	input *CompileInputV1,
	userText string,
	assistantText string,
) (string, string) {
	t.Helper()
	_, userCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          userText,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, assistantCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: assistantText,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	userDigest := contentDigest("TASK_INPUT", jsonMediaType, userCanonical)
	assistantDigest := contentDigest(
		"MODEL_RESULT",
		jsonMediaType,
		assistantCanonical,
	)
	input.ConversationHistoryTurns = append(
		input.ConversationHistoryTurns,
		ConversationHistoryTurnV1{
			TurnIndex:                    uint64(len(input.ConversationHistoryTurns) + 1),
			UserSourceContentDigest:      userDigest,
			AssistantSourceContentDigest: assistantDigest,
			UserMessage: moduleapi.ModelMessageV1{
				Role: moduleapi.ModelRoleUser, Content: userText,
			},
			AssistantMessage: moduleapi.ModelMessageV1{
				Role: moduleapi.ModelRoleAssistant, Content: assistantText,
			},
		},
	)
	return userDigest, assistantDigest
}

func conversationSoftSummaryFixture(
	t *testing.T,
	wantSourceTurns int,
) (CompileInputV1, CompileResultV1) {
	t.Helper()
	input := newCompileInput(t, strings.Repeat("current-task-", 40))
	if wantSourceTurns == 1 {
		addConversationHistoryTurn(
			t,
			&input,
			strings.Repeat("first-question-", 240),
			strings.Repeat("first-answer-", 240),
		)
		addConversationHistoryTurn(t, &input, "second question", "second answer")
	} else {
		for index := 0; index < 3; index++ {
			addConversationHistoryTurn(
				t,
				&input,
				strings.Repeat(fmt.Sprintf("user-%d-", index), 100),
				strings.Repeat(fmt.Sprintf("assistant-%d-", index), 100),
			)
		}
	}

	original := estimateCompileInput(t, input)
	for budget := original + 1; ; budget++ {
		watermark, err := corecontract.ContextRestoreWatermarkTokensV1(budget)
		if err != nil {
			t.Fatal(err)
		}
		if watermark > original {
			break
		}
		trial := cloneCompileInput(input)
		setContextPolicy(t, &trial, budget, 0)
		result, err := CompileV1(trial)
		if err != nil {
			continue
		}
		if result.Compilation != nil && result.Compilation.Summary != nil &&
			len(result.Compilation.Summary.SourceTurnDigests) == wantSourceTurns {
			return trial, result
		}
	}
	t.Fatalf("no soft-watermark budget selected %d source turns", wantSourceTurns)
	return CompileInputV1{}, CompileResultV1{}
}

func conversationSummaryCandidateForPrefix(
	t *testing.T,
	input CompileInputV1,
	count int,
) *corecontract.ContextCompilationSummaryV1 {
	t.Helper()
	if count <= 0 || count > len(input.ConversationHistoryTurns) {
		t.Fatalf("invalid candidate prefix count %d", count)
	}
	digests := make([]string, 0, count)
	messages := make([]moduleapi.ModelMessageV1, 0, count*2)
	for index := 0; index < count; index++ {
		unit, err := freezeConversationHistoryTurn(
			input.ConversationHistoryTurns[index],
		)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, unit.digest)
		messages = append(messages, unit.messages...)
	}
	text, err := corecontract.ContextHeadTailSummaryV1(messages)
	if err != nil {
		t.Fatal(err)
	}
	return &corecontract.ContextCompilationSummaryV1{
		SourceTurnDigests: digests,
		Text:              text,
	}
}

func assertExactMessages(
	t *testing.T,
	got []moduleapi.ModelMessageV1,
	want []moduleapi.ModelMessageV1,
) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("message count=%d, want %d: %+v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("message %d=%+v, want %+v", index, got[index], want[index])
		}
	}
}
