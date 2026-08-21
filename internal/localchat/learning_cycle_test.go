package localchat

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestLearningCycleTickIsIdleWhileDisabledAndBeforeDue(t *testing.T) {
	fixture := newLearningCycleServiceFixture(t)
	firstDue := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	schedule := fixture.createSchedule(t, "cycle-idle", firstDue, false)

	disabled, err := fixture.service.Tick(
		context.Background(),
		LearningCycleTickInput{
			TenantID:   fixture.base.tenantID,
			ScheduleID: schedule.Schedule.ScheduleID,
			ObservedAt: firstDue,
		},
	)
	if err != nil || disabled.Due || disabled.Task.TaskID != "" ||
		disabled.LoopResult != nil {
		t.Fatalf("disabled Tick=%+v error=%v", disabled, err)
	}
	fixture.enableSchedule(t, schedule)

	early, err := fixture.service.Tick(
		context.Background(),
		LearningCycleTickInput{
			TenantID:   fixture.base.tenantID,
			ScheduleID: schedule.Schedule.ScheduleID,
			ObservedAt: firstDue.Add(-time.Microsecond),
		},
	)
	if err != nil || early.Due || early.Task.TaskID != "" ||
		early.LoopResult != nil {
		t.Fatalf("early Tick=%+v error=%v", early, err)
	}
	if got := fixture.invoker.callCount(); got != 0 {
		t.Fatalf("idle Tick invoked model %d times", got)
	}
	tasks, err := fixture.base.store.ListLearningCycleTasks(
		context.Background(),
		fixture.base.tenantID,
		schedule.Schedule.ScheduleID,
	)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("idle tasks=%+v error=%v", tasks, err)
	}
}

func TestLearningCycleTickResumesExactRunAdmissionAfterLoopError(
	t *testing.T,
) {
	fixture := newLearningCycleServiceFixture(t)
	firstDue := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	schedule := fixture.createSchedule(t, "cycle-exact-retry", firstDue, true)
	fixture.setResultForWindow(t, schedule, firstDue)

	initialLoop := fixture.chat.loop
	fixture.chat.loop = learningCycleFailLoop{failure: errors.New("injected Loop failure")}
	first, err := fixture.service.Tick(
		context.Background(),
		LearningCycleTickInput{
			TenantID:   fixture.base.tenantID,
			ScheduleID: schedule.Schedule.ScheduleID,
			ObservedAt: firstDue,
		},
	)
	if err == nil || !first.Due || !first.TaskCreated ||
		!first.AdmissionCreated || first.Task.RunID == "" ||
		first.Task.State != learningcontract.LearningCycleTaskRunAdmittedV1 ||
		first.Task.Revision != 1 || first.LoopResult != nil {
		t.Fatalf("first Tick=%+v error=%v", first, err)
	}
	if got := fixture.invoker.callCount(); got != 0 {
		t.Fatalf("failed Loop invoked model %d times", got)
	}

	fixture.chat.loop = initialLoop
	second, err := fixture.service.Tick(
		context.Background(),
		LearningCycleTickInput{
			TenantID:   fixture.base.tenantID,
			ScheduleID: schedule.Schedule.ScheduleID,
			ObservedAt: firstDue,
		},
	)
	if err != nil || !second.Due || second.TaskCreated ||
		second.AdmissionCreated || second.Task.TaskID != first.Task.TaskID ||
		second.Task.RunID != first.Task.RunID ||
		second.Task.State != learningcontract.LearningCycleTaskNoChangeV1 ||
		second.Task.Revision != 2 || !second.FinalizeApplied ||
		second.LoopResult == nil ||
		second.LoopResult.Disposition != loopapi.DispositionTerminated {
		t.Fatalf("second Tick=%+v error=%v", second, err)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("exact retry model calls=%d want 1", got)
	}

	third, err := fixture.service.Tick(
		context.Background(),
		LearningCycleTickInput{
			TenantID:   fixture.base.tenantID,
			ScheduleID: schedule.Schedule.ScheduleID,
			ObservedAt: firstDue,
		},
	)
	if err != nil || third.Due || third.LoopResult != nil ||
		fixture.invoker.callCount() != 1 {
		t.Fatalf("third Tick=%+v calls=%d error=%v", third, fixture.invoker.callCount(), err)
	}

	requestCanonical := fixture.invoker.onlyInput(t)
	modelRequest, err := moduleapi.RestoreModelGenerateRequestV1(requestCanonical)
	if err != nil || len(modelRequest.Messages) == 0 {
		t.Fatalf("restore cycle model request: messages=%d error=%v", len(modelRequest.Messages), err)
	}
	last := modelRequest.Messages[len(modelRequest.Messages)-1]
	if last.Role != moduleapi.ModelRoleUser ||
		!bytes.Equal([]byte(last.Content), first.Task.RequestCanonical) {
		t.Fatalf("cycle model task message=%+v", last)
	}
	for _, forbidden := range []string{first.Task.TaskID, first.Task.RunID} {
		if bytes.Contains(requestCanonical, []byte(forbidden)) {
			t.Fatalf("dynamic cycle identity %q leaked into model request", forbidden)
		}
	}
}

func TestLearningCycleTickRejectsEmptySuccessfulLoopReceipt(t *testing.T) {
	fixture := newLearningCycleServiceFixture(t)
	firstDue := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	schedule := fixture.createSchedule(t, "cycle-empty-loop-receipt", firstDue, true)
	fixture.chat.loop = learningCycleFailLoop{}

	result, err := fixture.service.Tick(
		context.Background(),
		LearningCycleTickInput{
			TenantID:   fixture.base.tenantID,
			ScheduleID: schedule.Schedule.ScheduleID,
			ObservedAt: firstDue,
		},
	)
	if !errors.Is(err, ErrLearningCycleIntegrity) || !result.Due ||
		!result.TaskCreated || !result.AdmissionCreated ||
		result.Task.State != learningcontract.LearningCycleTaskRunAdmittedV1 ||
		result.Task.Revision != 1 || result.LoopResult != nil {
		t.Fatalf("empty Loop receipt Tick=%+v error=%v", result, err)
	}
	if got := fixture.invoker.callCount(); got != 0 {
		t.Fatalf("empty Loop receipt invoked model %d times", got)
	}
}

func TestLearningCycleTickFinalizesPersistedEffectDespiteLoopError(t *testing.T) {
	fixture := newLearningCycleServiceFixture(t)
	firstDue := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	schedule := fixture.createSchedule(t, "cycle-finalize-after-loop-error", firstDue, true)
	fixture.setResultForWindow(t, schedule, firstDue)
	injected := errors.New("injected post-advance Loop error")
	fixture.chat.loop = learningCycleAfterRunErrorLoop{
		inner:   fixture.loop,
		failure: injected,
	}

	result, err := fixture.service.Tick(
		context.Background(),
		LearningCycleTickInput{
			TenantID:   fixture.base.tenantID,
			ScheduleID: schedule.Schedule.ScheduleID,
			ObservedAt: firstDue,
		},
	)
	if !errors.Is(err, injected) ||
		result.Task.State != learningcontract.LearningCycleTaskNoChangeV1 ||
		result.Task.Revision != 2 || !result.FinalizeApplied ||
		result.LoopResult == nil ||
		result.LoopResult.Disposition != loopapi.DispositionTerminated {
		t.Fatalf("post-effect Loop error Tick=%+v error=%v", result, err)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("post-effect Loop error model calls=%d want 1", got)
	}
}

func TestLearningCycleTickUnknownNeverSemanticallyReplays(t *testing.T) {
	fixture := newLearningCycleServiceFixture(t)
	fixture.invoker.unknown = true
	firstDue := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	schedule := fixture.createSchedule(t, "cycle-unknown", firstDue, true)

	first, err := fixture.service.Tick(
		context.Background(),
		LearningCycleTickInput{
			TenantID:   fixture.base.tenantID,
			ScheduleID: schedule.Schedule.ScheduleID,
			ObservedAt: firstDue,
		},
	)
	if err != nil || first.Task.State != learningcontract.LearningCycleTaskUnknownV1 ||
		first.Task.Revision != 2 || first.Task.AttemptID == "" ||
		!first.FinalizeApplied || first.LoopResult == nil ||
		first.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation {
		t.Fatalf("UNKNOWN Tick=%+v error=%v", first, err)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("UNKNOWN model calls=%d want 1", got)
	}

	retry, err := fixture.service.Tick(
		context.Background(),
		LearningCycleTickInput{
			TenantID:   fixture.base.tenantID,
			ScheduleID: schedule.Schedule.ScheduleID,
			ObservedAt: firstDue,
		},
	)
	if err != nil || retry.Due || retry.LoopResult != nil {
		t.Fatalf("UNKNOWN exact Tick retry=%+v error=%v", retry, err)
	}
	if got := fixture.invoker.callCount(); got != 1 {
		t.Fatalf("UNKNOWN retry replayed model: calls=%d", got)
	}
	dispatch, err := fixture.base.store.GetModelDispatchRecord(
		context.Background(),
		first.Task.AttemptID,
	)
	if err != nil || dispatch.Attempt.State != corecontract.ModelAttemptUnknown ||
		dispatch.Attempt.RunID != first.Task.RunID ||
		dispatch.Attempt.SourceDispatchAttemptID != "" {
		t.Fatalf("UNKNOWN dispatch=%+v error=%v", dispatch, err)
	}
}

func TestPureChatDoesNotTouchEnabledDueLearningCycle(t *testing.T) {
	chat := newChatServiceFixture(t)
	firstDue := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	schedule := createLearningCycleScheduleForTest(
		t,
		chat.store,
		learningcontract.LearningCycleScheduleV1{
			SchemaVersion:        learningcontract.LearningCycleScheduleSchemaVersionV1,
			TenantID:             chat.tenantID,
			ScheduleID:           "cycle-pure-chat-zero-touch",
			ServicePrincipalID:   "learning-cycle-service",
			WorkspaceID:          chat.workspaceID,
			AgentID:              chat.agentID,
			ProfileID:            chat.profileID,
			Kind:                 learningcontract.ProposalKindSkillV1,
			TargetModuleID:       "freeagent.test.cycle-pure-chat",
			Objective:            "Produce a bounded reusable static context only when Tick is explicit.",
			FirstDueAtUnixMicros: firstDue.UnixMicro(),
			MaxOutputTokens:      512,
		},
	)
	enabled, err := chat.store.SetLearningCycleScheduleEnabled(
		context.Background(),
		currentstore.SetLearningCycleScheduleEnabledInput{
			TenantID:         chat.tenantID,
			ScheduleID:       schedule.Schedule.ScheduleID,
			ExpectedRevision: schedule.Revision,
			Enabled:          true,
		},
	)
	if err != nil || !enabled.Applied {
		t.Fatalf("enable due schedule=%+v error=%v", enabled, err)
	}
	before := enabled.Record

	chatResult, err := chat.service.Chat(
		context.Background(),
		chat.input(
			"pure-chat-with-due-cycle",
			"ordinary chat must not Tick Learning",
			time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
		),
	)
	if err != nil || chatResult.TerminalResult == nil {
		t.Fatalf("Pure Chat=%+v error=%v", chatResult, err)
	}
	after, err := chat.store.GetLearningCycleSchedule(
		context.Background(),
		chat.tenantID,
		before.Schedule.ScheduleID,
	)
	if err != nil || after.Revision != before.Revision ||
		after.Enabled != before.Enabled ||
		!after.NextDueAt.Equal(before.NextDueAt) ||
		!after.LastScheduledFor.Equal(before.LastScheduledFor) {
		t.Fatalf("Pure Chat changed Schedule: before=%+v after=%+v error=%v", before, after, err)
	}
	tasks, err := chat.store.ListLearningCycleTasks(
		context.Background(),
		chat.tenantID,
		before.Schedule.ScheduleID,
	)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("Pure Chat cycle tasks=%+v error=%v", tasks, err)
	}
}

type learningCycleServiceFixture struct {
	base      *chatServiceFixture
	chat      *ChatService
	service   *LearningCycleService
	loop      *coreloop.UniversalLoop
	invoker   *learningCycleTestInvoker
	agentID   string
	profileID string
}

func newLearningCycleServiceFixture(t *testing.T) *learningCycleServiceFixture {
	t.Helper()
	base := newChatServiceFixture(t)
	agentID, profileID, provider := publishLearningReviewerControl(t, base)
	invoker := &learningCycleTestInvoker{}
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         invoker,
	})
	if err != nil {
		t.Fatalf("New cycle Registry: %v", err)
	}
	loop, err := coreloop.NewUniversalLoop(base.store, registry)
	if err != nil {
		t.Fatalf("New cycle UniversalLoop: %v", err)
	}
	chat, err := NewChatService(base.store, loop)
	if err != nil {
		t.Fatalf("New cycle ChatService: %v", err)
	}
	service, err := NewLearningCycleService(chat)
	if err != nil {
		t.Fatalf("NewLearningCycleService: %v", err)
	}
	return &learningCycleServiceFixture{
		base: base, chat: chat, service: service, loop: loop,
		invoker: invoker, agentID: agentID, profileID: profileID,
	}
}

func (fixture *learningCycleServiceFixture) createSchedule(
	t *testing.T,
	scheduleID string,
	firstDue time.Time,
	enabled bool,
) currentstore.LearningCycleScheduleRecord {
	t.Helper()
	record := createLearningCycleScheduleForTest(
		t,
		fixture.base.store,
		learningcontract.LearningCycleScheduleV1{
			SchemaVersion:        learningcontract.LearningCycleScheduleSchemaVersionV1,
			TenantID:             fixture.base.tenantID,
			ScheduleID:           scheduleID,
			ServicePrincipalID:   "learning-cycle-service",
			WorkspaceID:          fixture.base.workspaceID,
			AgentID:              fixture.agentID,
			ProfileID:            fixture.profileID,
			Kind:                 learningcontract.ProposalKindSkillV1,
			TargetModuleID:       "freeagent.test." + scheduleID,
			Objective:            "Produce one bounded reusable static context candidate.",
			FirstDueAtUnixMicros: firstDue.UnixMicro(),
			MaxOutputTokens:      512,
		},
	)
	if enabled {
		return fixture.enableSchedule(t, record)
	}
	return record
}

func (fixture *learningCycleServiceFixture) enableSchedule(
	t *testing.T,
	record currentstore.LearningCycleScheduleRecord,
) currentstore.LearningCycleScheduleRecord {
	t.Helper()
	enabled, err := fixture.base.store.SetLearningCycleScheduleEnabled(
		context.Background(),
		currentstore.SetLearningCycleScheduleEnabledInput{
			TenantID:         fixture.base.tenantID,
			ScheduleID:       record.Schedule.ScheduleID,
			ExpectedRevision: record.Revision,
			Enabled:          true,
		},
	)
	if err != nil || !enabled.Applied {
		t.Fatalf("enable cycle Schedule=%+v error=%v", enabled, err)
	}
	return enabled.Record
}

func (fixture *learningCycleServiceFixture) setResultForWindow(
	t *testing.T,
	schedule currentstore.LearningCycleScheduleRecord,
	scheduledFor time.Time,
) {
	t.Helper()
	frozenSchedule, err := learningcontract.RestoreLearningCycleScheduleV1(
		schedule.ScheduleCanonical,
		schedule.ScheduleDigest,
	)
	if err != nil {
		t.Fatalf("RestoreLearningCycleScheduleV1: %v", err)
	}
	_, _, digest, err := learningcontract.NewLearningCycleRequestV1(
		frozenSchedule,
		schedule.ScheduleCanonical,
		schedule.ScheduleDigest,
		scheduledFor.UnixMicro(),
	)
	if err != nil {
		t.Fatalf("NewLearningCycleRequestV1: %v", err)
	}
	fixture.invoker.setRequestDigest(digest)
}

func createLearningCycleScheduleForTest(
	t *testing.T,
	store *currentstore.Store,
	schedule learningcontract.LearningCycleScheduleV1,
) currentstore.LearningCycleScheduleRecord {
	t.Helper()
	created, err := store.CreateLearningCycleSchedule(context.Background(), schedule)
	if err != nil || !created.Created {
		t.Fatalf("CreateLearningCycleSchedule=%+v error=%v", created, err)
	}
	return created.Record
}

type learningCycleTestInvoker struct {
	mu            sync.Mutex
	inputs        [][]byte
	requestDigest string
	unknown       bool
}

func (invoker *learningCycleTestInvoker) Invoke(
	_ context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	invoker.mu.Lock()
	invoker.inputs = append(invoker.inputs, bytes.Clone(prepared.Invocation.Input))
	requestDigest := invoker.requestDigest
	unknown := invoker.unknown
	invoker.mu.Unlock()
	if unknown {
		return modulehost.InvocationResult{
			InvocationID: prepared.Invocation.InvocationID,
			Provider:     prepared.Binding.Provider,
			Outcome:      modulehost.InvocationUnknown,
			UnknownClass: modulehost.UnknownClassResponseBodyReadIncomplete,
		}, nil
	}
	_, resultCanonical, _, err := learningcontract.NewLearningCycleResultV1(
		learningcontract.LearningCycleResultV1{
			SchemaVersion:   learningcontract.LearningCycleResultSchemaVersionV1,
			RequestDigest:   requestDigest,
			Decision:        learningcontract.LearningCycleResultNoChangeV1,
			KnowledgeChunks: []string{},
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(resultCanonical),
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	return modulehost.InvocationResult{
		InvocationID: prepared.Invocation.InvocationID,
		Provider:     prepared.Binding.Provider,
		Outcome:      modulehost.InvocationSucceeded,
		Output:       outputCanonical,
	}, nil
}

func (invoker *learningCycleTestInvoker) setRequestDigest(digest string) {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	invoker.requestDigest = digest
}

func (invoker *learningCycleTestInvoker) callCount() int {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	return len(invoker.inputs)
}

func (invoker *learningCycleTestInvoker) onlyInput(t *testing.T) []byte {
	t.Helper()
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	if len(invoker.inputs) != 1 {
		t.Fatalf("cycle model inputs=%d want 1", len(invoker.inputs))
	}
	return bytes.Clone(invoker.inputs[0])
}

type learningCycleFailLoop struct {
	failure error
}

func (loop learningCycleFailLoop) Run(
	context.Context,
	loopapi.RunInput,
) (loopapi.RunResult, error) {
	return loopapi.RunResult{}, loop.failure
}

type learningCycleAfterRunErrorLoop struct {
	inner   loopapi.Loop
	failure error
}

func (loop learningCycleAfterRunErrorLoop) Run(
	ctx context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	result, err := loop.inner.Run(ctx, input)
	return result, errors.Join(err, loop.failure)
}
