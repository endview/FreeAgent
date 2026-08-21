package currentstore

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestConversationSummaryCandidateForCompilerV1ReturnsSanitizedClone(
	t *testing.T,
) {
	fixture := newConversationSummarySelectorFixture(t)

	candidate, err := ConversationSummaryCandidateForCompilerV1(fixture.run)
	if err != nil {
		t.Fatal(err)
	}
	if candidate == nil ||
		len(candidate.SourceTurnDigests) != len(fixture.sourceDigests) ||
		candidate.Text != fixture.summaryText {
		t.Fatalf("candidate=%+v", candidate)
	}
	if candidate.BeforeEstimateTokens != 0 || candidate.AfterEstimateTokens != 0 {
		t.Fatalf("source token estimates leaked into candidate: %+v", candidate)
	}
	for index := range fixture.sourceDigests {
		if candidate.SourceTurnDigests[index] != fixture.sourceDigests[index] {
			t.Fatalf("candidate digests=%v, want %v", candidate.SourceTurnDigests, fixture.sourceDigests)
		}
	}

	candidate.SourceTurnDigests[0] = strings.Repeat("f", 64)
	repeated, err := ConversationSummaryCandidateForCompilerV1(fixture.run)
	if err != nil || repeated == nil ||
		repeated.SourceTurnDigests[0] != fixture.sourceDigests[0] {
		t.Fatalf("candidate was not defensively cloned: %+v error=%v", repeated, err)
	}
}

func TestConversationSummaryCandidateForCompilerV1DoesNotScanOlderTurns(
	t *testing.T,
) {
	fixture := newConversationSummarySelectorFixture(t)
	run := cloneRunForLoop(fixture.run)
	older := cloneContentRecord(*run.ConversationHistory[2].SourceContextCompilation)
	run.ConversationHistory[0].SourceContextCompilation = &older
	run.ConversationHistory[2].SourceContextCompilation = nil

	candidate, err := ConversationSummaryCandidateForCompilerV1(run)
	if err != nil || candidate != nil {
		t.Fatalf("older candidate result=%+v error=%v", candidate, err)
	}
}

func TestConversationSummaryCandidateForCompilerV1TreatsValidSourceAsInapplicable(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		mutate func(*corecontract.ContextCompilationV1)
	}{
		{
			name: "Workspace version",
			mutate: func(value *corecontract.ContextCompilationV1) {
				value.WorkspaceScope.Version = "2"
				value.WorkspaceScope.Digest = strings.Repeat("8", 64)
			},
		},
		{
			name: "ContextPolicy version",
			mutate: func(value *corecontract.ContextCompilationV1) {
				value.ContextPolicy.Version = "2"
				value.ContextPolicy.Digest = strings.Repeat("9", 64)
			},
		},
		{
			name: "no Summary",
			mutate: func(value *corecontract.ContextCompilationV1) {
				value.Summary = nil
				value.FinalEstimateTokens = value.OriginalEstimateTokens
				value.StopReason = corecontract.ContextCompilationNoEligibleSummary
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConversationSummarySelectorFixture(t)
			value := fixture.compilation
			test.mutate(&value)
			setConversationSummaryCompilation(t, &fixture.run, value)

			candidate, err := ConversationSummaryCandidateForCompilerV1(fixture.run)
			if err != nil || candidate != nil {
				t.Fatalf("mismatched candidate=%+v error=%v", candidate, err)
			}
		})
	}
}

func TestConversationSummaryCandidateForCompilerV1FailsClosedOnCorruption(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *conversationSummarySelectorFixture)
	}{
		{
			name: "Compilation kind",
			mutate: func(_ *testing.T, fixture *conversationSummarySelectorFixture) {
				fixture.run.ConversationHistory[2].SourceContextCompilation.Kind = ContentModelRequest
			},
		},
		{
			name: "Compilation media",
			mutate: func(_ *testing.T, fixture *conversationSummarySelectorFixture) {
				fixture.run.ConversationHistory[2].SourceContextCompilation.MediaType = "application/problem+json"
			},
		},
		{
			name: "Compilation digest",
			mutate: func(_ *testing.T, fixture *conversationSummarySelectorFixture) {
				fixture.run.ConversationHistory[2].SourceContextCompilation.Digest = strings.Repeat("f", 64)
			},
		},
		{
			name: "Compilation canonical wire",
			mutate: func(_ *testing.T, fixture *conversationSummarySelectorFixture) {
				record := fixture.run.ConversationHistory[2].SourceContextCompilation
				record.CanonicalBytes = bytes.Replace(
					record.CanonicalBytes,
					[]byte(corecontract.ContextSummaryHeadTailExtractiveV1),
					[]byte("freeagent.context-summary/unsupported"),
					1,
				)
				record.SizeBytes = int64(len(record.CanonicalBytes))
				record.Digest = mustConversationSummaryContentDigest(
					t,
					record.Kind,
					record.MediaType,
					record.CanonicalBytes,
				)
			},
		},
		{
			name: "raw USER digest",
			mutate: func(_ *testing.T, fixture *conversationSummarySelectorFixture) {
				fixture.run.ConversationHistory[0].UserContent.Digest = strings.Repeat("e", 64)
			},
		},
		{
			name: "Agent identity",
			mutate: func(_ *testing.T, fixture *conversationSummarySelectorFixture) {
				fixture.run.Member.Agent.Version = "2"
			},
		},
		{
			name: "direct predecessor",
			mutate: func(_ *testing.T, fixture *conversationSummarySelectorFixture) {
				fixture.run.Manifest.ConversationTurn.PredecessorRunID = "run-other"
			},
		},
		{
			name: "non-prefix digest",
			mutate: func(t *testing.T, fixture *conversationSummarySelectorFixture) {
				value := fixture.compilation
				value.Summary.SourceTurnDigests = append(
					[]string(nil), value.Summary.SourceTurnDigests...,
				)
				value.Summary.SourceTurnDigests[0] = strings.Repeat("d", 64)
				setConversationSummaryCompilation(t, &fixture.run, value)
			},
		},
		{
			name: "non-deterministic text",
			mutate: func(t *testing.T, fixture *conversationSummarySelectorFixture) {
				value := fixture.compilation
				summary := *value.Summary
				summary.Text = "not the deterministic raw-pair summary"
				value.Summary = &summary
				setConversationSummaryCompilation(t, &fixture.run, value)
			},
		},
		{
			name: "claims direct predecessor",
			mutate: func(t *testing.T, fixture *conversationSummarySelectorFixture) {
				value := fixture.compilation
				direct := fixture.run.ConversationHistory[2]
				digest, err := corecontract.ContextConversationTurnDigestV1(
					direct.TurnIndex,
					direct.UserContent.Digest,
					direct.AssistantContent.Digest,
				)
				if err != nil {
					t.Fatal(err)
				}
				summary := *value.Summary
				summary.SourceTurnDigests = append(
					append([]string(nil), summary.SourceTurnDigests...),
					digest,
				)
				value.Summary = &summary
				setConversationSummaryCompilation(t, &fixture.run, value)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConversationSummarySelectorFixture(t)
			fixture.run = cloneRunForLoop(fixture.run)
			test.mutate(t, &fixture)
			candidate, err := ConversationSummaryCandidateForCompilerV1(fixture.run)
			if err == nil || candidate != nil {
				t.Fatalf("corrupt candidate=%+v error=%v", candidate, err)
			}
		})
	}
}

func TestLoadLoopConversationHistoryLoadsDirectAndLatestExactCompilations(
	t *testing.T,
) {
	fixture := prepareDynamicKnowledgeAdmission(t, nil)
	widenDynamicContextConversationPolicy(t, fixture.admission)
	secondTask := conversationSummaryTaskInput(t, "a distinct direct-predecessor question")
	allowConversationSummaryKnowledgeTasks(
		t,
		&fixture,
		fixture.admission.task.Digest,
		secondTask.Digest,
	)
	conversationID := "conversation-summary-loader"
	createConversationForAdmission(t, fixture.admission, conversationID)

	firstTask := fixture.admission.task
	selectConversationSummaryKnowledgeScope(t, &fixture, firstTask.Digest)
	firstRunID := "run-conversation-summary-loader-first"
	firstInput := dynamicConversationAdmissionInput(
		t, fixture.admission, conversationID, "summary-loader-first", 0, "", firstRunID,
	)
	if _, err := fixture.admission.store.CommitConversationTurnAdmission(
		context.Background(), firstInput,
	); err != nil {
		t.Fatal(err)
	}
	putDynamicContextTestPrice(t, fixture.admission.store)
	firstLease, firstRun := acquireDynamicContextConversationRun(
		t, fixture.admission.store, firstRunID, "summary-loader-first-worker",
	)
	firstCompiled := compileKnowledgeRequestForRun(t, fixture, firstRun)
	completeDynamicContextConversationAttempt(
		t, fixture.admission.store, firstRun, firstLease,
		"attempt-conversation-summary-loader-first", firstCompiled,
	)

	fixture.admission.task = secondTask
	fixture.admission.intent.TaskInputRef = secondTask.Digest
	selectConversationSummaryKnowledgeScope(t, &fixture, secondTask.Digest)
	secondRunID := "run-conversation-summary-loader-second"
	secondInput := dynamicConversationAdmissionInput(
		t, fixture.admission, conversationID, "summary-loader-second", 1, firstRunID, secondRunID,
	)
	if _, err := fixture.admission.store.CommitConversationTurnAdmission(
		context.Background(), secondInput,
	); err != nil {
		t.Fatal(err)
	}
	secondLease, secondRun := acquireDynamicContextConversationRun(
		t, fixture.admission.store, secondRunID, "summary-loader-second-worker",
	)
	if len(secondRun.ConversationHistory) != 1 ||
		secondRun.ConversationHistory[0].SourceContextCompilation == nil {
		t.Fatalf("different-question direct predecessor did not load Compilation: %+v", secondRun.ConversationHistory)
	}
	secondCompiled := compileKnowledgeRequestForRun(t, fixture, secondRun)
	completeDynamicContextConversationAttempt(
		t, fixture.admission.store, secondRun, secondLease,
		"attempt-conversation-summary-loader-second", secondCompiled,
	)

	fixture.admission.task = firstTask
	fixture.admission.intent.TaskInputRef = firstTask.Digest
	selectConversationSummaryKnowledgeScope(t, &fixture, firstTask.Digest)
	thirdRunID := "run-conversation-summary-loader-third"
	thirdInput := dynamicConversationAdmissionInput(
		t, fixture.admission, conversationID, "summary-loader-third", 2, secondRunID, thirdRunID,
	)
	if _, err := fixture.admission.store.CommitConversationTurnAdmission(
		context.Background(), thirdInput,
	); err != nil {
		t.Fatal(err)
	}
	_, thirdRun := acquireDynamicContextConversationRun(
		t, fixture.admission.store, thirdRunID, "summary-loader-third-worker",
	)
	if len(thirdRun.ConversationHistory) != 2 ||
		thirdRun.ConversationHistory[0].SourceContextCompilation == nil ||
		thirdRun.ConversationHistory[1].SourceContextCompilation == nil {
		t.Fatalf("direct/latest-exact Compilation closure=%+v", thirdRun.ConversationHistory)
	}
	loaded := 0
	for _, entry := range thirdRun.ConversationHistory {
		if entry.SourceContextCompilation != nil {
			loaded++
		}
	}
	if loaded != 2 {
		t.Fatalf("loaded Compilation count=%d, want 2", loaded)
	}
}

func TestLoadConversationCompilationSourceFollowsFrozenActionChain(
	t *testing.T,
) {
	harness := newActionStoreHarness(t)
	actionBegin := harness.beginAction(t, "action-attempt-summary-source")
	actionDone, err := harness.store.CommitActionDispatchOutcome(
		context.Background(),
		actionOutcomeInput(
			actionBegin.Action,
			actionBegin.Lease,
			moduleapi.ActionExecutionSucceeded,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	modelTwo, err := harness.store.BeginModelDispatch(
		context.Background(),
		harness.secondModelInput(t, actionDone),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !modelTwo.ConsumeModelInvocationPermit() {
		t.Fatal("model-2 invocation permit is absent")
	}
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     "The action-backed answer is complete.",
			ProviderRequestID: "provider-request-summary-model-2",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := harness.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   modelTwo.Lease,
			AttemptID:               modelTwo.Attempt.AttemptID,
			InvocationID:            modelTwo.Attempt.AttemptID,
			Provider:                modelTwo.Attempt.Binding.Provider,
			ExpectedAttemptRevision: modelTwo.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical: modelUsageOutcomeCanonical(
				t,
				`{"id":"provider-request-summary-model-2","status":"completed"}`,
			),
			ProviderRequestID: "provider-request-summary-model-2",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := harness.store.GetTerminalRunResult(
		context.Background(),
		committed.Record.Attempt.RunID,
	)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := harness.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	attemptID, compilation, err := loadConversationCompilationSource(
		context.Background(),
		connection,
		terminal,
		harness.modelBegin.Attempt.RunID,
		harness.modelBegin.Attempt.MemberID,
		harness.modelBegin.Attempt.MemberSnapshotDigest,
		committed.Record.Attempt.ResultRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	if attemptID != harness.modelBegin.Attempt.AttemptID ||
		compilation == nil || harness.modelBegin.Attempt.ContextCompilation == nil ||
		!bytes.Equal(
			compilation.CanonicalBytes,
			harness.modelBegin.Attempt.ContextCompilation.CanonicalBytes,
		) {
		t.Fatalf(
			"Action compilation source attempt=%q compilation=%+v",
			attemptID,
			compilation,
		)
	}
	if _, _, err := loadConversationCompilationSource(
		context.Background(),
		connection,
		terminal,
		harness.modelBegin.Attempt.RunID,
		harness.modelBegin.Attempt.MemberID,
		strings.Repeat("f", 64),
		committed.Record.Attempt.ResultRef,
	); err == nil {
		t.Fatal("Action compilation source accepted a different member snapshot")
	}
}

func TestLoadLoopConversationHistoryAgentVersionChangeIsFresh(
	t *testing.T,
) {
	fixture := prepareDynamicKnowledgeAdmission(t, nil)
	widenDynamicContextConversationPolicy(t, fixture.admission)
	conversationID := "conversation-summary-agent-version"
	createConversationForAdmission(t, fixture.admission, conversationID)
	putDynamicContextTestPrice(t, fixture.admission.store)

	firstRunID := "run-conversation-summary-agent-v1"
	firstInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"summary-agent-v1",
		0,
		"",
		firstRunID,
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
		firstRunID,
		"summary-agent-v1-worker",
	)
	firstCompiled := compileKnowledgeRequestForRun(t, fixture, firstRun)
	const firstAttemptID = "attempt-conversation-summary-agent-v1"
	completeDynamicContextConversationAttempt(
		t,
		fixture.admission.store,
		firstRun,
		firstLease,
		firstAttemptID,
		firstCompiled,
	)

	publishConversationSummaryAgentVersion(
		t,
		fixture.admission,
		"2",
		strings.Repeat("e", 64),
	)
	secondRunID := "run-conversation-summary-agent-v2"
	secondInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"summary-agent-v2",
		1,
		firstRunID,
		secondRunID,
	)
	if _, err := fixture.admission.store.CommitConversationTurnAdmission(
		context.Background(),
		secondInput,
	); err != nil {
		t.Fatal(err)
	}
	_, secondRun := acquireDynamicContextConversationRun(
		t,
		fixture.admission.store,
		secondRunID,
		"summary-agent-v2-worker",
	)
	if len(secondRun.ConversationHistory) != 1 {
		t.Fatalf("Agent-version History=%+v", secondRun.ConversationHistory)
	}
	source := secondRun.ConversationHistory[0]
	if source.SourceAttemptID != firstAttemptID ||
		source.SourceContextCompilationAttemptID != "" ||
		source.SourceContextCompilation != nil {
		t.Fatalf("Agent-version source did not fall back fresh: %+v", source)
	}
	candidate, err := ConversationSummaryCandidateForCompilerV1(secondRun)
	if err != nil || candidate != nil {
		t.Fatalf("Agent-version candidate=%+v error=%v", candidate, err)
	}
}

type conversationSummarySelectorFixture struct {
	run           RunForLoop
	compilation   corecontract.ContextCompilationV1
	sourceDigests []string
	summaryText   string
}

func newConversationSummarySelectorFixture(
	t *testing.T,
) conversationSummarySelectorFixture {
	t.Helper()
	workspace := corecontract.WorkspaceRef{
		ID: "workspace-summary", Version: "1", Digest: strings.Repeat("1", 64),
	}
	agent := corecontract.AgentRef{
		ID: "agent-summary", Version: "1", Digest: strings.Repeat("2", 64),
	}
	policy := corecontract.PolicyRef{
		ID: "policy-summary", Version: "1", Digest: strings.Repeat("3", 64),
	}
	memberDigest := strings.Repeat("4", 64)
	run := RunForLoop{
		Manifest: corecontract.RunManifest{
			Workspace:       workspace,
			PrimaryAgent:    agent,
			PrimaryMemberID: "member-summary",
			Members: []corecontract.MemberSnapshotRef{{
				MemberID: "member-summary", Digest: memberDigest,
			}},
			ConversationTurn: &corecontract.ConversationTurnRefV1{
				SchemaVersion:    corecontract.ConversationTurnRefSchemaVersionV1,
				ConversationID:   "conversation-summary",
				PrincipalID:      "principal-summary",
				TurnIndex:        4,
				PredecessorRunID: "run-summary-3",
			},
		},
		Member: corecontract.MemberExecutionSnapshot{
			MemberID:             "member-summary",
			MemberSnapshotDigest: memberDigest,
			Agent:                agent,
			Workspace:            workspace,
			ContextPolicy:        policy,
		},
	}
	for index := 1; index <= 3; index++ {
		run.ConversationHistory = append(
			run.ConversationHistory,
			ConversationHistoryTurnRecord{
				TurnIndex:        uint64(index),
				SourceRunID:      "run-summary-" + string(rune('0'+index)),
				SourceAttemptID:  "attempt-summary-" + string(rune('0'+index)),
				UserContent:      conversationSummaryTaskRecord(t, "question "+string(rune('0'+index))),
				AssistantContent: conversationSummaryOutputRecord(t, "answer "+string(rune('0'+index))),
			},
		)
	}
	sourceDigests := make([]string, 2)
	messages := make([]moduleapi.ModelMessageV1, 0, 4)
	for index := 0; index < 2; index++ {
		entry := run.ConversationHistory[index]
		digest, err := corecontract.ContextConversationTurnDigestV1(
			entry.TurnIndex,
			entry.UserContent.Digest,
			entry.AssistantContent.Digest,
		)
		if err != nil {
			t.Fatal(err)
		}
		sourceDigests[index] = digest
		user, _ := corecontract.RestoreTaskInputV1(entry.UserContent.CanonicalBytes)
		assistant, _ := moduleapi.RestoreModelGenerateOutputV1(entry.AssistantContent.CanonicalBytes)
		messages = append(messages,
			moduleapi.ModelMessageV1{Role: moduleapi.ModelRoleUser, Content: user.Text},
			moduleapi.ModelMessageV1{Role: moduleapi.ModelRoleAssistant, Content: assistant.AssistantText},
		)
	}
	summaryText, err := corecontract.ContextHeadTailSummaryV1(messages)
	if err != nil {
		t.Fatal(err)
	}
	compilation := corecontract.ContextCompilationV1{
		SchemaVersion:           corecontract.ContextCompilationSchemaVersionV1,
		WorkspaceScope:          workspace,
		ContextPolicy:           policy,
		EstimatorVersion:        corecontract.ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
		SummaryAlgorithmVersion: corecontract.ContextSummaryHeadTailExtractiveV1,
		InputBudgetTokens:       1000,
		RestoreWatermarkTokens:  850,
		OriginalEstimateTokens:  900,
		Summary: &corecontract.ContextCompilationSummaryV1{
			SourceTurnDigests:    sourceDigests,
			Text:                 summaryText,
			BeforeEstimateTokens: 900,
			AfterEstimateTokens:  800,
		},
		Drops:               []corecontract.ContextCompilationDropV1{},
		FinalEstimateTokens: 800,
		StopReason:          corecontract.ContextCompilationSummaryToWatermark,
		FinalRequestDigest:  strings.Repeat("5", 64),
	}
	setConversationSummaryCompilation(t, &run, compilation)
	return conversationSummarySelectorFixture{
		run: run, compilation: compilation,
		sourceDigests: append([]string(nil), sourceDigests...),
		summaryText:   summaryText,
	}
}

func setConversationSummaryCompilation(
	t *testing.T,
	run *RunForLoop,
	compilation corecontract.ContextCompilationV1,
) {
	t.Helper()
	_, canonical, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil {
		t.Fatal(err)
	}
	record := ContentRecord{
		Kind: ContentContextCompilation, MediaType: admissionJSONMediaType,
		CanonicalBytes: canonical, SizeBytes: int64(len(canonical)),
	}
	record.Digest = mustConversationSummaryContentDigest(
		t, record.Kind, record.MediaType, canonical,
	)
	run.ConversationHistory[len(run.ConversationHistory)-1].SourceContextCompilation = &record
	run.ConversationHistory[len(run.ConversationHistory)-1].SourceContextCompilationAttemptID =
		run.ConversationHistory[len(run.ConversationHistory)-1].SourceAttemptID
}

func conversationSummaryTaskRecord(t *testing.T, text string) ContentRecord {
	t.Helper()
	input := conversationSummaryTaskInput(t, text)
	return ContentRecord{
		Digest: input.Digest, Kind: input.Kind, MediaType: input.MediaType,
		CanonicalBytes: bytes.Clone(input.CanonicalBytes),
		SizeBytes:      int64(len(input.CanonicalBytes)),
	}
}

func conversationSummaryTaskInput(t *testing.T, text string) ContentInput {
	t.Helper()
	_, canonical, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
		SchemaVersion: corecontract.TaskInputSchemaVersionV1,
		Text:          text,
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := mustConversationSummaryContentDigest(
		t, ContentTaskInput, admissionJSONMediaType, canonical,
	)
	return ContentInput{
		Digest: digest, Kind: ContentTaskInput, MediaType: admissionJSONMediaType,
		CanonicalBytes: canonical,
	}
}

func conversationSummaryOutputRecord(t *testing.T, text string) ContentRecord {
	t.Helper()
	_, canonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: text,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest := mustConversationSummaryContentDigest(
		t, ContentModelResult, admissionJSONMediaType, canonical,
	)
	return ContentRecord{
		Digest: digest, Kind: ContentModelResult, MediaType: admissionJSONMediaType,
		CanonicalBytes: canonical, SizeBytes: int64(len(canonical)),
	}
}

func mustConversationSummaryContentDigest(
	t *testing.T,
	kind ContentKind,
	mediaType string,
	canonical []byte,
) string {
	t.Helper()
	digest, err := ComputeContentDigest(kind, mediaType, canonical)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func allowConversationSummaryKnowledgeTasks(
	t *testing.T,
	fixture *admittedKnowledgeFixture,
	taskRefs ...string,
) {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.admission.controlCanonical,
		fixture.admission.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		fixture.admission.catalogCanonical,
		fixture.admission.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	base := fixture.authorityValue.AllowedScopes[0]
	rules := make([]moduleapi.KnowledgeScopeRuleV1, len(taskRefs))
	for index, taskRef := range taskRefs {
		rules[index] = base
		rules[index].TaskInputRef = taskRef
	}
	authority, authorityCanonical, err := moduleapi.NewKnowledgeAuthorityCeilingV1(
		moduleapi.KnowledgeAuthorityCeilingV1{
			SchemaVersion:     fixture.authorityValue.SchemaVersion,
			Source:            fixture.authorityValue.Source,
			AllowedScopes:     rules,
			MaxHits:           fixture.authorityValue.MaxHits,
			MaxTotalTextBytes: fixture.authorityValue.MaxTotalTextBytes,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authorityRef := putPublicationJSON(
		t,
		fixture.admission.store,
		ContentAuthorityCeiling,
		authorityCanonical,
	)
	found := false
	for index := range control.Profiles[0].Bindings {
		if control.Profiles[0].Bindings[index].Port == fixture.contextPort {
			control.Profiles[0].Bindings[index].AuthorityCeilingRef = authorityRef
			found = true
		}
	}
	if !found {
		t.Fatal("dynamic Knowledge Binding is absent")
	}
	control.SnapshotID = "control-conversation-summary-loader"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-conversation-summary-loader"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := fixture.admission.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: fixture.admission.basis.PointerRevision,
			NewPointerRevision:      fixture.admission.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.admission.basis = basis
	fixture.admission.controlCanonical = controlCanonical
	fixture.admission.catalogCanonical = catalogCanonical
	fixture.authority = authorityCanonical
	fixture.authorityValue = authority
}

func selectConversationSummaryKnowledgeScope(
	t *testing.T,
	fixture *admittedKnowledgeFixture,
	taskRef string,
) {
	t.Helper()
	for index := range fixture.authorityValue.AllowedScopes {
		if fixture.authorityValue.AllowedScopes[index].TaskInputRef == taskRef {
			fixture.authorityValue.AllowedScopes[0],
				fixture.authorityValue.AllowedScopes[index] =
				fixture.authorityValue.AllowedScopes[index],
				fixture.authorityValue.AllowedScopes[0]
			return
		}
	}
	t.Fatalf("Knowledge authority does not allow task %s", taskRef)
}

func publishConversationSummaryAgentVersion(
	t *testing.T,
	fixture *admissionCommitFixture,
	version string,
	digest string,
) {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.controlCanonical,
		fixture.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		fixture.catalogCanonical,
		fixture.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(control.Agents) != 1 {
		t.Fatalf("Agent cardinality=%d, want 1", len(control.Agents))
	}
	control.Agents[0].Version = version
	control.Agents[0].Digest = digest
	control.SnapshotID = "control-conversation-summary-agent-" + version
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-conversation-summary-agent-" + version
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := fixture.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: fixture.basis.PointerRevision,
			NewPointerRevision:      fixture.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture.basis = basis
	fixture.controlCanonical = controlCanonical
	fixture.catalogCanonical = catalogCanonical
}
