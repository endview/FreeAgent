package localchat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type decisionCompositeScenario string

const (
	decisionCompositeApproveRoundZero    decisionCompositeScenario = "APPROVE_0"
	decisionCompositeRepairOneApprove    decisionCompositeScenario = "REPAIR_ONE_APPROVE"
	decisionCompositeRejectRoundZero     decisionCompositeScenario = "REJECT_0"
	decisionCompositeRepairAllApprove    decisionCompositeScenario = "REPAIR_ALL_APPROVE"
	decisionCompositeRepairOneReject     decisionCompositeScenario = "REPAIR_ONE_REJECT"
	decisionCompositeRepairOneInvalid    decisionCompositeScenario = "REPAIR_ONE_INVALID"
	decisionCompositeRepairOneUnknown    decisionCompositeScenario = "REPAIR_ONE_UNKNOWN"
	decisionCompositeRepairChildFailed   decisionCompositeScenario = "REPAIR_CHILD_FAILED"
	decisionCompositeRepairChildInvalid  decisionCompositeScenario = "REPAIR_CHILD_INVALID"
	decisionCompositeRepairChildUnknown  decisionCompositeScenario = "REPAIR_CHILD_UNKNOWN"
	decisionCompositeInitialMixedFailure decisionCompositeScenario = "INITIAL_FAILED_AND_UNKNOWN"
	decisionCompositeRepairMixedFailure  decisionCompositeScenario = "REPAIR_FAILED_AND_UNKNOWN"
)

type decisionCompositeFixture struct {
	base    *chatServiceFixture
	service *CompositeChatService
	invoker *decisionCompositeInvoker
}

func TestCompositeDecisionChatRoundZeroApproveSkipsRepairAndMergesOnSecondRootAdvance(
	t *testing.T,
) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeApproveRoundZero,
	)
	input := fixture.base.input(
		"decision-approve-round-zero",
		"approve the initial structured contributions",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)
	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !first.AdmissionCreated || first.Reviewer == nil ||
		first.RepairReviewer == nil || len(first.RepairChildren) != 2 ||
		first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.TerminalResult == nil || first.FailureCode != "" ||
		first.Reply != "decision result for "+first.RootRunID {
		t.Fatalf("result=%+v", first)
	}
	if got := fixture.base.invoker.callCount(); got != 4 {
		t.Fatalf("model calls=%d want initial N + Reviewer0 + merge = 4", got)
	}
	runs := fixture.invoker.runIDsSnapshot()
	for _, repair := range first.RepairChildren {
		if containsDecisionRunID(runs, repair.RunID) {
			t.Fatalf("skipped repair Child %q was invoked: %v", repair.RunID, runs)
		}
	}
	if containsDecisionRunID(runs, first.RepairReviewer.RunID) {
		t.Fatalf("skipped repair Reviewer was invoked: %v", runs)
	}

	retry, err := fixture.service.Chat(context.Background(), input)
	if err != nil || retry.Reply != first.Reply || retry.AdmissionCreated {
		t.Fatalf("retry=%+v error=%v", retry, err)
	}
	if got := fixture.base.invoker.callCount(); got != 4 {
		t.Fatalf("retry added model calls=%d", got)
	}
}

func TestCompositeDecisionChatRepairsOnlyAffectedSlotThenReviewsAndMerges(
	t *testing.T,
) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeRepairOneApprove,
	)
	input := fixture.base.input(
		"decision-repair-one",
		"repair one incomplete specialist contribution",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)
	result, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result.LoopResult.Disposition != loopapi.DispositionTerminated ||
		result.TerminalResult == nil || result.FailureCode != "" ||
		result.RepairReviewer == nil ||
		result.RepairReviewer.LoopResult.Disposition !=
			loopapi.DispositionTerminated ||
		result.Reply != "decision result for "+result.RootRunID {
		t.Fatalf("result=%+v", result)
	}
	if got := fixture.base.invoker.callCount(); got != 6 {
		t.Fatalf("model calls=%d want 2 + Reviewer0 + repair1 + Reviewer1 + merge", got)
	}
	runs := fixture.invoker.runIDsSnapshot()
	activated := 0
	for _, repair := range result.RepairChildren {
		if containsDecisionRunID(runs, repair.RunID) {
			activated++
		}
	}
	if activated != 1 || !containsDecisionRunID(
		runs,
		result.RepairReviewer.RunID,
	) {
		t.Fatalf("repair invocation closure=%v result=%+v", runs, result)
	}
}

func TestCompositeDecisionChatRepairsAllAffectedSlotsWithinFrozenDispatchCap(
	t *testing.T,
) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeRepairAllApprove,
	)
	input := fixture.base.input(
		"decision-repair-all",
		"repair every incomplete specialist contribution",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)
	result, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result.LoopResult.Disposition != loopapi.DispositionTerminated ||
		result.TerminalResult == nil || result.FailureCode != "" ||
		result.RepairReviewer == nil ||
		result.RepairReviewer.LoopResult.Disposition !=
			loopapi.DispositionTerminated ||
		result.Reply != "decision result for "+result.RootRunID {
		t.Fatalf("result=%+v", result)
	}
	if got := fixture.base.invoker.callCount(); got != 7 {
		t.Fatalf("model calls=%d want 2N+3 frozen cap=7", got)
	}
	runs := fixture.invoker.runIDsSnapshot()
	for _, repair := range result.RepairChildren {
		if !containsDecisionRunID(runs, repair.RunID) {
			t.Fatalf("affected repair Child %q was not invoked: %v", repair.RunID, runs)
		}
	}
	if !containsDecisionRunID(runs, result.RepairReviewer.RunID) {
		t.Fatalf("round-one Reviewer was not invoked: %v", runs)
	}
}

func TestCompositeDecisionChatRepairChildTerminalOutcomesCloseFamily(
	t *testing.T,
) {
	tests := []struct {
		name               string
		scenario           decisionCompositeScenario
		wantDisposition    loopapi.Disposition
		wantRootFailure    string
		wantChildReason    string
		wantClassification string
	}{
		{
			name:               "provider failed",
			scenario:           decisionCompositeRepairChildFailed,
			wantDisposition:    loopapi.DispositionTerminated,
			wantRootFailure:    corecontract.AllRequiredChildFailedReasonV1,
			wantChildReason:    "MODEL_FAILED",
			wantClassification: "PROVIDER_REPORTED_FAILURE",
		},
		{
			name:               "structured contribution invalid",
			scenario:           decisionCompositeRepairChildInvalid,
			wantDisposition:    loopapi.DispositionTerminated,
			wantRootFailure:    corecontract.AllRequiredChildFailedReasonV1,
			wantChildReason:    "MODEL_FAILED",
			wantClassification: "COLLABORATION_SPECIALIST_OUTPUT_INVALID",
		},
		{
			name:            "provider unknown",
			scenario:        decisionCompositeRepairChildUnknown,
			wantDisposition: loopapi.DispositionWaitingReconciliation,
			wantChildReason: "MODEL_UNKNOWN",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDecisionCompositeFixture(t, test.scenario)
			input := fixture.base.input(
				"decision-repair-child-"+string(test.scenario),
				"preserve the exact repair child outcome",
				time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
			)
			result, err := fixture.service.Chat(context.Background(), input)
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if result.LoopResult.Disposition != test.wantDisposition ||
				result.FailureCode != test.wantRootFailure ||
				result.Reply != "" {
				t.Fatalf("result=%+v", result)
			}
			if test.wantDisposition == loopapi.DispositionTerminated &&
				result.TerminalResult == nil {
				t.Fatalf("terminal repair failure lacks Root terminal result: %+v", result)
			}
			if test.wantDisposition == loopapi.DispositionWaitingReconciliation &&
				result.TerminalResult != nil {
				t.Fatalf("UNKNOWN repair Child created Root terminal result: %+v", result)
			}
			if got := fixture.base.invoker.callCount(); got != 4 {
				t.Fatalf("model calls=%d want initial N + Reviewer0 + one repair=4", got)
			}

			runs := fixture.invoker.runIDsSnapshot()
			var affected *CompositeChildChatResult
			for index := range result.RepairChildren {
				if containsDecisionRunID(runs, result.RepairChildren[index].RunID) {
					affected = &result.RepairChildren[index]
				}
			}
			if affected == nil || affected.LoopResult.ReasonCode != test.wantChildReason {
				t.Fatalf("affected repair Child=%+v runs=%v", affected, runs)
			}
			if containsDecisionRunID(runs, result.RepairReviewer.RunID) ||
				containsDecisionRunID(runs, result.RootRunID) {
				t.Fatalf("repair failure/UNKNOWN invoked Reviewer1 or Root merge: %v", runs)
			}
			if test.wantClassification != "" {
				terminal, terminalErr := fixture.base.store.GetTerminalRunResult(
					context.Background(),
					affected.RunID,
				)
				if terminalErr != nil ||
					terminal.ErrorClassification != test.wantClassification {
					t.Fatalf("repair Child terminal=%+v error=%v", terminal, terminalErr)
				}
			}

			retry, retryErr := fixture.service.Chat(context.Background(), input)
			if retryErr != nil ||
				retry.LoopResult.Disposition != test.wantDisposition ||
				retry.FailureCode != test.wantRootFailure {
				t.Fatalf("retry=%+v error=%v", retry, retryErr)
			}
			if got := fixture.base.invoker.callCount(); got != 4 {
				t.Fatalf("retry replayed provider call: %d", got)
			}
		})
	}
}

func TestCompositeDecisionChatMixedFailedAndUnknownPrefersDeterministicFailure(
	t *testing.T,
) {
	tests := []struct {
		name      string
		scenario  decisionCompositeScenario
		wantCalls int
	}{
		{
			name:      "initial specialists",
			scenario:  decisionCompositeInitialMixedFailure,
			wantCalls: 2,
		},
		{
			name:      "repair specialists",
			scenario:  decisionCompositeRepairMixedFailure,
			wantCalls: 5,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDecisionCompositeFixture(t, test.scenario)
			input := fixture.base.input(
				"decision-mixed-"+string(test.scenario),
				"a failed required specialist determines the family outcome",
				time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
			)
			result, err := fixture.service.Chat(context.Background(), input)
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if result.LoopResult.Disposition != loopapi.DispositionTerminated ||
				result.TerminalResult == nil || result.Reply != "" ||
				result.FailureCode != corecontract.AllRequiredChildFailedReasonV1 {
				t.Fatalf("result=%+v", result)
			}
			if got := fixture.base.invoker.callCount(); got != test.wantCalls {
				t.Fatalf("model calls=%d want=%d", got, test.wantCalls)
			}
			runs := fixture.invoker.runIDsSnapshot()
			if containsDecisionRunID(runs, result.RootRunID) {
				t.Fatalf("mixed terminal Child outcomes invoked Root merge: %v", runs)
			}
			if result.RepairReviewer != nil && containsDecisionRunID(
				runs,
				result.RepairReviewer.RunID,
			) {
				t.Fatalf("mixed repair outcomes invoked Reviewer1: %v", runs)
			}

			retry, retryErr := fixture.service.Chat(context.Background(), input)
			if retryErr != nil || retry.LoopResult.Disposition !=
				loopapi.DispositionTerminated ||
				retry.FailureCode != corecontract.AllRequiredChildFailedReasonV1 {
				t.Fatalf("retry=%+v error=%v", retry, retryErr)
			}
			if got := fixture.base.invoker.callCount(); got != test.wantCalls {
				t.Fatalf("retry replayed provider call: %d", got)
			}
		})
	}
}

func TestCompositeDecisionChatRejectsWithoutRootMerge(t *testing.T) {
	tests := []struct {
		name         string
		scenario     decisionCompositeScenario
		wantCalls    int
		wantFailure  string
		wantRepairer bool
	}{
		{
			name:        "round zero reject",
			scenario:    decisionCompositeRejectRoundZero,
			wantCalls:   3,
			wantFailure: "COLLABORATION_REVIEW_REJECTED",
		},
		{
			name:         "round one reject",
			scenario:     decisionCompositeRepairOneReject,
			wantCalls:    5,
			wantFailure:  "COLLABORATION_REVIEW_REJECTED",
			wantRepairer: true,
		},
		{
			name:         "round one second repair is invalid",
			scenario:     decisionCompositeRepairOneInvalid,
			wantCalls:    5,
			wantFailure:  "COLLABORATION_REVIEWER_OUTPUT_INVALID",
			wantRepairer: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDecisionCompositeFixture(t, test.scenario)
			input := fixture.base.input(
				"decision-reject-"+string(test.scenario),
				"reject the bounded contribution set",
				time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
			)
			result, err := fixture.service.Chat(context.Background(), input)
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if result.LoopResult.Disposition != loopapi.DispositionTerminated ||
				result.TerminalResult == nil || result.Reply != "" ||
				result.FailureCode != test.wantFailure {
				t.Fatalf("result=%+v", result)
			}
			if got := fixture.base.invoker.callCount(); got != test.wantCalls {
				t.Fatalf("model calls=%d want %d", got, test.wantCalls)
			}
			runs := fixture.invoker.runIDsSnapshot()
			if containsDecisionRunID(runs, result.RootRunID) {
				t.Fatalf("REJECT/invalid invoked Root merge: %v", runs)
			}
			if test.wantRepairer != containsDecisionRunID(
				runs,
				result.RepairReviewer.RunID,
			) {
				t.Fatalf("repair Reviewer presence differs: %v", runs)
			}
		})
	}
}

func TestCompositeDecisionChatRoundOneUnknownNeverReplaysOrMerges(
	t *testing.T,
) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeRepairOneUnknown,
	)
	input := fixture.base.input(
		"decision-repair-unknown",
		"preserve an uncertain round one review",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)
	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if first.LoopResult.Disposition !=
		loopapi.DispositionWaitingReconciliation ||
		first.TerminalResult != nil || first.Reply != "" ||
		first.FailureCode != "" {
		t.Fatalf("first=%+v", first)
	}
	if got := fixture.base.invoker.callCount(); got != 5 {
		t.Fatalf("model calls=%d want 5", got)
	}
	if containsDecisionRunID(
		fixture.invoker.runIDsSnapshot(),
		first.RootRunID,
	) {
		t.Fatalf("UNKNOWN invoked Root merge")
	}
	retry, err := fixture.service.Chat(context.Background(), input)
	if err != nil || retry.LoopResult.Disposition !=
		loopapi.DispositionWaitingReconciliation {
		t.Fatalf("retry=%+v error=%v", retry, err)
	}
	if got := fixture.base.invoker.callCount(); got != 5 {
		t.Fatalf("UNKNOWN retry replayed provider call: %d", got)
	}
}

func TestCompositeDecisionChatResumesAfterCommittedDecisionResultIsLost(
	t *testing.T,
) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeRepairOneApprove,
	)
	lost := &loseFirstDecisionAppliedResultLoop{
		delegate: fixture.service.loop,
	}
	fixture.service.loop = lost
	input := fixture.base.input(
		"decision-result-lost",
		"resume from the exact committed decision transition",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)
	first, err := fixture.service.Chat(context.Background(), input)
	if !errors.Is(err, errDecisionAppliedResultLost) {
		t.Fatalf("first=%+v error=%v", first, err)
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("calls before lost response=%d want initial N + Reviewer0=3", got)
	}

	resumed, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("resumed Chat: %v", err)
	}
	if resumed.AdmissionCreated ||
		resumed.LoopResult.Disposition != loopapi.DispositionTerminated ||
		resumed.TerminalResult == nil || resumed.FailureCode != "" ||
		resumed.Reply != "decision result for "+resumed.RootRunID {
		t.Fatalf("resumed=%+v", resumed)
	}
	if got := fixture.base.invoker.callCount(); got != 6 {
		t.Fatalf("resumed model calls=%d want exact one-slot path=6", got)
	}
	if got := lost.lossCount(); got != 1 {
		t.Fatalf("lost decision results=%d want 1", got)
	}
}

func TestCompositeDecisionChatConcurrentSameRequestNeverDuplicatesProviderCall(
	t *testing.T,
) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeRepairOneApprove,
	)
	input := fixture.base.input(
		"decision-concurrent-exact-retry",
		"coordinate one bounded repair under concurrent retry",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)
	start := make(chan struct{})
	results := make([]CompositeChatResult, 2)
	errs := make([]error, 2)
	var wait sync.WaitGroup
	for index := range results {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results[index], errs[index] = fixture.service.Chat(
				context.Background(),
				input,
			)
		}()
	}
	close(start)
	wait.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Chat %d: %v result=%+v", index, err, results[index])
		}
	}

	settled, err := fixture.service.Chat(context.Background(), input)
	if err != nil || settled.LoopResult.Disposition !=
		loopapi.DispositionTerminated || settled.TerminalResult == nil ||
		settled.FailureCode != "" ||
		settled.Reply != "decision result for "+settled.RootRunID {
		t.Fatalf("settled=%+v error=%v", settled, err)
	}
	if got := fixture.base.invoker.callCount(); got != 6 {
		t.Fatalf("concurrent exact retry provider calls=%d want 6", got)
	}
	created := 0
	for _, result := range results {
		if result.AdmissionCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("concurrent AdmissionCreated count=%d want 1", created)
	}
}

var errDecisionAppliedResultLost = errors.New(
	"test: committed collaboration decision result lost",
)

type loseFirstDecisionAppliedResultLoop struct {
	delegate loopapi.Loop

	mu     sync.Mutex
	lost   bool
	losses int
}

func (loop *loseFirstDecisionAppliedResultLoop) Run(
	ctx context.Context,
	input loopapi.RunInput,
) (loopapi.RunResult, error) {
	result, err := loop.delegate.Run(ctx, input)
	if err != nil || result.ReasonCode != "COLLABORATION_DECISION_APPLIED" {
		return result, err
	}
	loop.mu.Lock()
	defer loop.mu.Unlock()
	if loop.lost {
		return result, nil
	}
	loop.lost = true
	loop.losses++
	return loopapi.RunResult{}, errDecisionAppliedResultLost
}

func (loop *loseFirstDecisionAppliedResultLoop) lossCount() int {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return loop.losses
}

func newDecisionCompositeFixture(
	t *testing.T,
	scenario decisionCompositeScenario,
) *decisionCompositeFixture {
	t.Helper()
	legacy := newCompositeChatServiceFixtureWithReviewer(
		t,
		false,
		compositeReviewerApproveTest,
	)
	ctx := context.Background()
	basis, control, catalog, err := legacy.base.store.LoadPublishedBasis(
		ctx,
		legacy.base.tenantID,
	)
	if err != nil {
		t.Fatalf("LoadPublishedBasis: %v", err)
	}
	if len(control.CompositeAgents) != 1 ||
		control.CompositeAgents[0].Reviewer == nil {
		t.Fatalf("legacy Reviewer fixture lacks Composite definition")
	}
	control.SnapshotID = "control-chat-decision-" + string(scenario)
	control.Revision++
	control.CompositeAgents[0].Decision =
		&controlcontract.CompositeDecisionDefinitionV1{
			SchemaVersion: controlcontract.CompositeDecisionSchemaVersionV1,
		}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("NewControlSnapshot(decision): %v", err)
	}
	catalog.GenerationID = "catalog-chat-decision-" + string(scenario)
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("NewCatalogGeneration(decision): %v", err)
	}
	if _, err := legacy.base.store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatalf("PublishControlCatalog(decision): %v", err)
	}
	provider := catalog.Entries[0].Activation
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatalf("NewDeterministicEcho: %v", err)
	}
	invoker := &decisionCompositeInvoker{
		provider:     provider,
		echo:         echo,
		scenario:     scenario,
		initialReady: make(chan struct{}),
	}
	legacy.base.invoker.delegate = invoker
	return &decisionCompositeFixture{
		base:    legacy.base,
		service: legacy.service,
		invoker: invoker,
	}
}

type decisionCompositeInvoker struct {
	provider moduleapi.ActivatedModuleRef
	echo     modulehost.ModuleInvoker
	scenario decisionCompositeScenario

	mu           sync.Mutex
	runIDs       []string
	initialCalls int
	repairCalls  int
	initialReady chan struct{}
	initialOnce  sync.Once
}

type decisionRequestKind int

const (
	decisionRequestRoot decisionRequestKind = iota
	decisionRequestSpecialist
	decisionRequestReviewer
)

func (invoker *decisionCompositeInvoker) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	kind, round, repair, err := classifyDecisionRequest(
		prepared.Invocation.Input,
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	invoker.mu.Lock()
	invoker.runIDs = append(invoker.runIDs, prepared.Invocation.RunID)
	initialCall := 0
	repairCall := 0
	if kind == decisionRequestSpecialist && !repair {
		invoker.initialCalls++
		initialCall = invoker.initialCalls
		if invoker.initialCalls == 2 {
			invoker.initialOnce.Do(func() { close(invoker.initialReady) })
		}
	} else if kind == decisionRequestSpecialist && repair {
		invoker.repairCalls++
		repairCall = invoker.repairCalls
	}
	invoker.mu.Unlock()
	if kind == decisionRequestSpecialist && !repair {
		select {
		case <-invoker.initialReady:
		case <-ctx.Done():
			return modulehost.InvocationResult{}, ctx.Err()
		}
	}
	if kind == decisionRequestSpecialist && !repair &&
		invoker.scenario == decisionCompositeInitialMixedFailure {
		outcome := modulehost.InvocationFailed
		if initialCall > 1 {
			outcome = modulehost.InvocationUnknown
		}
		return modulehost.InvocationResult{
			Provider: invoker.provider,
			Outcome:  outcome,
		}, nil
	}
	if kind == decisionRequestReviewer && round == 1 &&
		invoker.scenario == decisionCompositeRepairOneUnknown {
		return modulehost.InvocationResult{
			Provider: invoker.provider,
			Outcome:  modulehost.InvocationUnknown,
		}, nil
	}
	if kind == decisionRequestSpecialist && repair {
		switch invoker.scenario {
		case decisionCompositeRepairChildFailed:
			return modulehost.InvocationResult{
				Provider: invoker.provider,
				Outcome:  modulehost.InvocationFailed,
			}, nil
		case decisionCompositeRepairChildUnknown:
			return modulehost.InvocationResult{
				Provider: invoker.provider,
				Outcome:  modulehost.InvocationUnknown,
			}, nil
		case decisionCompositeRepairMixedFailure:
			outcome := modulehost.InvocationFailed
			if repairCall > 1 {
				outcome = modulehost.InvocationUnknown
			}
			return modulehost.InvocationResult{
				Provider: invoker.provider,
				Outcome:  outcome,
			}, nil
		case decisionCompositeRepairChildInvalid:
			result, invokeErr := invoker.echo.Invoke(ctx, prepared)
			if invokeErr != nil {
				return modulehost.InvocationResult{}, invokeErr
			}
			output, restoreErr := moduleapi.RestoreModelGenerateOutputV1(
				result.Output,
			)
			if restoreErr != nil {
				return modulehost.InvocationResult{}, restoreErr
			}
			output.AssistantText = `{"schema_version":"specialist-contribution/v1"}`
			_, result.Output, restoreErr = moduleapi.NewModelGenerateOutputV1(output)
			if restoreErr != nil {
				return modulehost.InvocationResult{}, restoreErr
			}
			return result, nil
		}
	}

	result, err := invoker.echo.Invoke(ctx, prepared)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	var assistant string
	switch kind {
	case decisionRequestSpecialist:
		_, canonical, _, freezeErr :=
			corecontract.NewSpecialistContributionV1(
				corecontract.SpecialistContributionV1{
					SchemaVersion: corecontract.SpecialistContributionSchemaVersionV1,
					Proposal: "bounded proposal from " +
						prepared.Invocation.RunID,
					Evidence:    []corecontract.SpecialistEvidenceV1{},
					Assumptions: []string{},
					Risks:       []string{},
					Conflicts:   []string{},
				},
			)
		if freezeErr != nil {
			return modulehost.InvocationResult{}, freezeErr
		}
		assistant = string(canonical)
	case decisionRequestReviewer:
		decision := corecontract.CollaborationReviewDecisionApproveV1
		issues := []corecontract.ReviewIssueCodeV1{}
		slots := []string{}
		reason := "The structured contribution set is complete."
		if round == 0 && decisionScenarioRequiresRepair(invoker.scenario) {
			decision = corecontract.CollaborationReviewDecisionRepairRequiredV1
			issues = []corecontract.ReviewIssueCodeV1{
				corecontract.ReviewIssueMissingEvidenceV1,
			}
			slots = []string{"slot-analysis"}
			reason = "The analysis contribution requires one bounded repair."
			if invoker.scenario == decisionCompositeRepairAllApprove ||
				invoker.scenario == decisionCompositeRepairMixedFailure {
				slots = []string{"slot-analysis", "slot-review"}
				reason = "Every specialist contribution requires one bounded repair."
			}
		} else if (round == 0 &&
			invoker.scenario == decisionCompositeRejectRoundZero) ||
			(round == 1 &&
				invoker.scenario == decisionCompositeRepairOneReject) {
			decision = corecontract.CollaborationReviewDecisionRejectV1
			issues = []corecontract.ReviewIssueCodeV1{
				corecontract.ReviewIssueContradictionV1,
			}
			slots = []string{"slot-analysis"}
			reason = "The contribution set must be rejected."
		} else if round == 1 &&
			invoker.scenario == decisionCompositeRepairOneInvalid {
			assistant = `{"schema_version":"collaboration-review-verdict/v1","decision":"REPAIR_REQUIRED","issue_codes":["MISSING_EVIDENCE"],"affected_slot_ids":["slot-analysis"],"bounded_reason":"A second repair is not allowed."}`
			break
		}
		verdict, _, freezeErr :=
			corecontract.NewCollaborationReviewVerdictV1(
				corecontract.CollaborationReviewVerdictV1{
					SchemaVersion: corecontract.CollaborationReviewVerdictSchemaVersionV1,
					FamilyDigest: strings.Repeat(
						"a",
						moduleapi.SHA256HexLength,
					),
					ContributionSetDigest: strings.Repeat(
						"b",
						moduleapi.SHA256HexLength,
					),
					RepairRound:     round,
					Decision:        decision,
					IssueCodes:      issues,
					AffectedSlotIDs: slots,
					BoundedReason:   reason,
				},
			)
		if freezeErr != nil {
			return modulehost.InvocationResult{}, freezeErr
		}
		canonical, freezeErr :=
			corecontract.CanonicalCollaborationReviewModelVerdictV1(verdict)
		if freezeErr != nil {
			return modulehost.InvocationResult{}, freezeErr
		}
		assistant = string(canonical)
	case decisionRequestRoot:
		assistant = "decision result for " + prepared.Invocation.RunID
	default:
		return modulehost.InvocationResult{}, fmt.Errorf(
			"unsupported decision request kind",
		)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(result.Output)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	output.AssistantText = assistant
	_, result.Output, err = moduleapi.NewModelGenerateOutputV1(output)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	return result, nil
}

func classifyDecisionRequest(
	canonical []byte,
) (decisionRequestKind, uint32, bool, error) {
	request, err := moduleapi.RestoreModelGenerateRequestV1(canonical)
	if err != nil {
		return 0, 0, false, err
	}
	kind := decisionRequestRoot
	var round uint32
	repair := false
	for _, message := range request.Messages {
		switch {
		case strings.HasPrefix(
			message.Content,
			"COLLABORATION_SPECIALIST_OUTPUT_CONTRACT:",
		):
			kind = decisionRequestSpecialist
		case strings.HasPrefix(
			message.Content,
			"COLLABORATION_REVIEW_OUTPUT_CONTRACT_ROUND_0:",
		):
			kind = decisionRequestReviewer
			round = 0
		case strings.HasPrefix(
			message.Content,
			"COLLABORATION_REVIEW_OUTPUT_CONTRACT_ROUND_1:",
		):
			kind = decisionRequestReviewer
			round = corecontract.CompositeRepairRoundOneV1
		case strings.HasPrefix(
			message.Content,
			"COLLABORATION_REPAIR_BASIS_JSON:",
		):
			repair = true
		}
	}
	return kind, round, repair, nil
}

func (invoker *decisionCompositeInvoker) runIDsSnapshot() []string {
	invoker.mu.Lock()
	defer invoker.mu.Unlock()
	return append([]string(nil), invoker.runIDs...)
}

func containsDecisionRunID(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func decisionScenarioRequiresRepair(scenario decisionCompositeScenario) bool {
	switch scenario {
	case decisionCompositeRepairOneApprove,
		decisionCompositeRepairAllApprove,
		decisionCompositeRepairOneReject,
		decisionCompositeRepairOneInvalid,
		decisionCompositeRepairOneUnknown,
		decisionCompositeRepairChildFailed,
		decisionCompositeRepairChildInvalid,
		decisionCompositeRepairChildUnknown,
		decisionCompositeRepairMixedFailure:
		return true
	default:
		return false
	}
}
