package currentstore

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestConversationKnowledgeNewAttemptRecompilesAndReloadsExactHistory(
	t *testing.T,
) {
	fixture := prepareDynamicKnowledgeAdmission(t, nil)
	widenDynamicContextConversationPolicy(t, fixture.admission)
	conversationID := "conversation-dynamic-knowledge"
	createConversationForAdmission(t, fixture.admission, conversationID)

	firstInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"conversation-dynamic-knowledge-first",
		0,
		"",
		"run-conversation-dynamic-knowledge-first",
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
		"run-conversation-dynamic-knowledge-first",
		"conversation-knowledge-first-worker",
	)
	firstCompiled := compileKnowledgeRequestForRun(t, fixture, firstRun)
	completeDynamicContextConversationAttempt(
		t,
		fixture.admission.store,
		firstRun,
		firstLease,
		"attempt-conversation-dynamic-knowledge-first",
		firstCompiled,
	)

	secondInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"conversation-dynamic-knowledge-second",
		1,
		"run-conversation-dynamic-knowledge-first",
		"run-conversation-dynamic-knowledge-second",
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
		"run-conversation-dynamic-knowledge-second",
		"conversation-knowledge-second-worker",
	)
	assertOneConversationPredecessor(t, secondRun)
	compiled := compileKnowledgeRequestForRun(t, fixture, secondRun)
	assertConversationPairPrecedesCurrentTask(t, compiled.Request)

	compilation := mustRestoreDynamicContextCompilation(t, compiled)
	knowledgeBindings, err := frozenKnowledgeBindingsForRun(
		secondRun.Manifest,
		secondRun.Member,
		runKnowledgeContentGetter(secondRun),
	)
	if err != nil {
		t.Fatal(err)
	}
	assertConversationRequestTamperRejected(
		t,
		compilation,
		compiled,
		secondRun,
		knowledgeBindings,
		nil,
	)

	begin, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		memoryBeginInput(
			secondRun,
			secondLease,
			"attempt-conversation-dynamic-knowledge-second",
			compiled,
		),
	)
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("Begin Conversation Knowledge Attempt=%+v error=%v", begin, err)
	}
	reopened := reopenDynamicContextTestStore(t, fixture.admission.store)
	fixture.admission.store = reopened
	assertReloadedConversationDynamicAttempt(
		t,
		reopened,
		begin.Lease,
		compiled,
	)
}

func TestConversationMemoryNewAttemptRecompilesAndReloadsExactHistory(
	t *testing.T,
) {
	fixture := prepareDynamicMemoryAdmission(t)
	widenDynamicContextConversationPolicy(t, fixture.admission)
	tenantID := fixture.admission.intent.TenantID
	agentID := mustRestoreMemberSnapshot(t, fixture.admission).Agent.ID
	genesisCanonical := canonicalAgentMemorySnapshot(
		t,
		tenantID,
		agentID,
		1,
		"",
		"",
		[]moduleapi.MemoryEntryV1{memoryFactEntry(t, "conversation memory")},
	)
	genesis, err := fixture.admission.store.PutAgentMemoryGenesis(
		context.Background(),
		genesisCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	conversationID := "conversation-dynamic-memory"
	createConversationForAdmission(t, fixture.admission, conversationID)

	firstInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"conversation-dynamic-memory-first",
		0,
		"",
		"run-conversation-dynamic-memory-first",
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
		"run-conversation-dynamic-memory-first",
		"conversation-memory-first-worker",
	)
	firstCompiled := compileMemoryRequestForRun(t, firstRun, genesis.Record)
	completeDynamicContextConversationAttempt(
		t,
		fixture.admission.store,
		firstRun,
		firstLease,
		"attempt-conversation-dynamic-memory-first",
		firstCompiled,
	)

	secondInput := dynamicConversationAdmissionInput(
		t,
		fixture.admission,
		conversationID,
		"conversation-dynamic-memory-second",
		1,
		"run-conversation-dynamic-memory-first",
		"run-conversation-dynamic-memory-second",
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
		"run-conversation-dynamic-memory-second",
		"conversation-memory-second-worker",
	)
	assertOneConversationPredecessor(t, secondRun)
	current, err := fixture.admission.store.GetCurrentAgentMemory(
		context.Background(),
		tenantID,
		agentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled := compileMemoryRequestForRun(t, secondRun, current)
	assertConversationPairPrecedesCurrentTask(t, compiled.Request)

	compilation := mustRestoreDynamicContextCompilation(t, compiled)
	memoryBindings, err := frozenMemoryBindingsForRun(
		secondRun.Manifest,
		secondRun.Member,
		runKnowledgeContentGetter(secondRun),
	)
	if err != nil {
		t.Fatal(err)
	}
	secondRunWithMemory, err := runWithAdditionalContent(
		secondRun,
		ContentRecord{
			Digest:         current.SnapshotRef.Digest,
			Kind:           ContentMemorySnapshot,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: current.CanonicalBytes,
			SizeBytes:      int64(len(current.CanonicalBytes)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertConversationRequestTamperRejected(
		t,
		compilation,
		compiled,
		secondRunWithMemory,
		nil,
		memoryBindings,
	)

	begin, err := fixture.admission.store.BeginModelDispatch(
		context.Background(),
		memoryBeginInput(
			secondRun,
			secondLease,
			"attempt-conversation-dynamic-memory-second",
			compiled,
		),
	)
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("Begin Conversation Memory Attempt=%+v error=%v", begin, err)
	}
	reopened := reopenDynamicContextTestStore(t, fixture.admission.store)
	fixture.admission.store = reopened
	assertReloadedConversationDynamicAttempt(
		t,
		reopened,
		begin.Lease,
		compiled,
	)
}

func dynamicConversationAdmissionInput(
	t *testing.T,
	fixture *admissionCommitFixture,
	conversationID string,
	admissionKey string,
	expectedRevision uint64,
	expectedHeadRunID string,
	runID string,
) CommitRunAdmissionInput {
	t.Helper()
	input := compileConversationTurnAdmission(
		t,
		fixture,
		conversationID,
		admissionKey,
		expectedRevision,
		expectedHeadRunID,
		runID,
	)
	input.Contents = []ContentInput{fixture.task}
	return input
}

func widenDynamicContextConversationPolicy(
	t *testing.T,
	fixture *admissionCommitFixture,
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
	policy := putPublicationContextPolicyWithLimits(
		t,
		fixture.store,
		4000,
		64,
	)
	for index := range control.Profiles {
		control.Profiles[index].ContextPolicy = policy
	}
	control.SnapshotID += "-conversation-context"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID += "-conversation-context"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
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

func acquireDynamicContextConversationRun(
	t *testing.T,
	store *Store,
	runID string,
	ownerID string,
) (RunLease, RunForLoop) {
	t.Helper()
	lease, err := store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 runID,
			OwnerID:               ownerID,
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	return lease, run
}

func completeDynamicContextConversationAttempt(
	t *testing.T,
	store *Store,
	run RunForLoop,
	lease RunLease,
	attemptID string,
	compiled contextcompiler.CompileResultV1,
) {
	t.Helper()
	begin, err := store.BeginModelDispatch(
		context.Background(),
		memoryBeginInput(run, lease, attemptID, compiled),
	)
	if err != nil {
		t.Fatal(err)
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	if _, err := store.CommitModelDispatchOutcome(
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

func dynamicContextCompilerHistoryForTest(
	t *testing.T,
	run RunForLoop,
) (
	[]contextcompiler.HistoryTurnV1,
	[]contextcompiler.ConversationHistoryTurnV1,
) {
	t.Helper()
	if run.Manifest.ConversationTurn == nil {
		return nil, nil
	}
	if len(run.History) != 0 {
		t.Fatal("Conversation Run unexpectedly contains local History")
	}
	want := run.Manifest.ConversationTurn.TurnIndex - 1
	if uint64(len(run.ConversationHistory)) != want {
		t.Fatalf(
			"Conversation History count=%d, want %d",
			len(run.ConversationHistory),
			want,
		)
	}
	history := make(
		[]contextcompiler.ConversationHistoryTurnV1,
		len(run.ConversationHistory),
	)
	for index, entry := range run.ConversationHistory {
		user, err := corecontract.RestoreTaskInputV1(
			entry.UserContent.CanonicalBytes,
		)
		if err != nil {
			t.Fatal(err)
		}
		assistant, err := moduleapi.RestoreModelGenerateOutputV1(
			entry.AssistantContent.CanonicalBytes,
		)
		if err != nil || assistant.ActionRequest != nil ||
			assistant.AssistantText == "" {
			t.Fatalf("restore predecessor Assistant: %+v error=%v", assistant, err)
		}
		history[index] = contextcompiler.ConversationHistoryTurnV1{
			TurnIndex:                    entry.TurnIndex,
			UserSourceContentDigest:      entry.UserContent.Digest,
			AssistantSourceContentDigest: entry.AssistantContent.Digest,
			UserMessage: moduleapi.ModelMessageV1{
				Role: moduleapi.ModelRoleUser, Content: user.Text,
			},
			AssistantMessage: moduleapi.ModelMessageV1{
				Role: moduleapi.ModelRoleAssistant, Content: assistant.AssistantText,
			},
		}
	}
	return nil, history
}

func assertOneConversationPredecessor(t *testing.T, run RunForLoop) {
	t.Helper()
	if run.Manifest.ConversationTurn == nil ||
		run.Manifest.ConversationTurn.TurnIndex != 2 ||
		len(run.ConversationHistory) != 1 {
		t.Fatalf(
			"Conversation closure turn=%+v history=%+v",
			run.Manifest.ConversationTurn,
			run.ConversationHistory,
		)
	}
}

func assertConversationPairPrecedesCurrentTask(
	t *testing.T,
	request moduleapi.ModelGenerateRequestV1,
) {
	t.Helper()
	var roles []moduleapi.ModelMessageRole
	for _, message := range request.Messages {
		if message.Role == moduleapi.ModelRoleUser ||
			message.Role == moduleapi.ModelRoleAssistant {
			roles = append(roles, message.Role)
		}
	}
	want := []moduleapi.ModelMessageRole{
		moduleapi.ModelRoleUser,
		moduleapi.ModelRoleAssistant,
		moduleapi.ModelRoleUser,
	}
	if len(roles) < len(want) {
		t.Fatalf("Conversation request roles=%v, want suffix %v", roles, want)
	}
	roles = roles[len(roles)-len(want):]
	for index := range want {
		if roles[index] != want[index] {
			t.Fatalf("Conversation request role suffix=%v, want %v", roles, want)
		}
	}
}

func mustRestoreDynamicContextCompilation(
	t *testing.T,
	compiled contextcompiler.CompileResultV1,
) corecontract.ContextCompilationV1 {
	t.Helper()
	if compiled.Compilation == nil || len(compiled.CompilationCanonical) == 0 {
		t.Fatal("dynamic context compilation is absent")
	}
	value, err := corecontract.RestoreContextCompilationV1(
		compiled.CompilationCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func assertConversationRequestTamperRejected(
	t *testing.T,
	compilation corecontract.ContextCompilationV1,
	compiled contextcompiler.CompileResultV1,
	run RunForLoop,
	knowledgeBindings []frozenKnowledgeBinding,
	memoryBindings []frozenMemoryBinding,
) {
	t.Helper()
	tampered := compiled.Request
	tampered.Messages = append(
		[]moduleapi.ModelMessageV1(nil),
		compiled.Request.Messages...,
	)
	changed := false
	for index := range tampered.Messages {
		if tampered.Messages[index].Role == moduleapi.ModelRoleAssistant {
			tampered.Messages[index].Content = "tampered predecessor answer"
			changed = true
			break
		}
	}
	if !changed {
		t.Fatal("compiled Conversation request has no predecessor Assistant")
	}
	err := validateCompilerOutputForNewAttempt(
		compilation,
		compiled.CompilationCanonical,
		run,
		tampered,
		run.Frame.Revision,
		knowledgeBindings,
		memoryBindings,
	)
	if err == nil || !strings.Contains(
		err.Error(),
		"exact Context Compiler output",
	) {
		t.Fatalf("tampered Conversation request error=%v", err)
	}
}

func reopenDynamicContextTestStore(t *testing.T, store *Store) *Store {
	t.Helper()
	path := store.Path()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return reopened
}

func assertReloadedConversationDynamicAttempt(
	t *testing.T,
	store *Store,
	lease RunLease,
	compiled contextcompiler.CompileResultV1,
) {
	t.Helper()
	reloaded, err := store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	assertOneConversationPredecessor(t, reloaded)
	if len(reloaded.ModelDispatches) != 1 {
		t.Fatalf("reloaded Model dispatches=%d", len(reloaded.ModelDispatches))
	}
	attempt := reloaded.ModelDispatches[0].Attempt
	if attempt.ContextCompilation == nil ||
		!bytes.Equal(
			attempt.ContextCompilation.CanonicalBytes,
			compiled.CompilationCanonical,
		) || !bytes.Equal(
		attempt.Request.CanonicalBytes,
		compiled.RequestCanonical,
	) {
		t.Fatal("reloaded dynamic Conversation Attempt differs from exact compiler output")
	}
}
