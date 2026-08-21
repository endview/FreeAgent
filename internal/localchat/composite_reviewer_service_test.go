package localchat

import (
	"context"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompositeChatServiceReviewerApproveThenRootAndRetryIsExact(
	t *testing.T,
) {
	fixture := newCompositeChatServiceFixtureWithReviewer(
		t,
		false,
		compositeReviewerApproveTest,
	)
	input := fixture.base.input(
		"composite-reviewer-approve-request",
		"review two specialist results before merging",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)

	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if !first.AdmissionCreated || first.Reviewer == nil ||
		first.Reviewer.RunID == "" ||
		first.Reviewer.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.TerminalResult == nil ||
		first.TerminalResult.State != corecontract.ModelAttemptSucceeded ||
		first.Reply != "result for "+first.RootRunID || first.FailureCode != "" {
		fixture.invoker.mu.Lock()
		familyDigest := fixture.invoker.reviewerFamilyDigest
		specialistDigest := fixture.invoker.reviewerSpecialistDigest
		fixture.invoker.mu.Unlock()
		t.Fatalf(
			"first Reviewer-enabled Chat=%+v Reviewer=%+v parsed=%q/%q",
			first,
			first.Reviewer,
			familyDigest,
			specialistDigest,
		)
	}
	if got := fixture.base.invoker.callCount(); got != 4 {
		t.Fatalf("first family model calls=%d want N+2=4", got)
	}
	assertCompositeReviewerApproveClosure(t, fixture, first)
	assertCompositeReviewerInvocationOrder(t, fixture, first)

	retried, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("retry Chat: %v", err)
	}
	if retried.AdmissionCreated || retried.Reviewer == nil ||
		retried.Reviewer.RunID != first.Reviewer.RunID ||
		retried.Reply != first.Reply ||
		retried.LoopResult.Disposition != loopapi.DispositionTerminated {
		t.Fatalf("retry=%+v first=%+v", retried, first)
	}
	if got := fixture.base.invoker.callCount(); got != 4 {
		t.Fatalf("retry added model calls=%d want 4", got)
	}
}

func assertCompositeReviewerInvocationOrder(
	t *testing.T,
	fixture *compositeChatServiceFixture,
	result CompositeChatResult,
) {
	t.Helper()
	runIDs := fixture.invoker.invocationRunIDs()
	if len(runIDs) != 4 || result.Reviewer == nil {
		t.Fatalf("Reviewer invocation order=%v result=%+v", runIDs, result)
	}
	children := map[string]struct{}{}
	for _, child := range result.Children {
		children[child.RunID] = struct{}{}
	}
	if _, ok := children[runIDs[0]]; !ok {
		t.Fatalf("first invocation %q is not a Specialist: %v", runIDs[0], runIDs)
	}
	if _, ok := children[runIDs[1]]; !ok || runIDs[1] == runIDs[0] {
		t.Fatalf("second invocation %q is not the other Specialist: %v", runIDs[1], runIDs)
	}
	if runIDs[2] != result.Reviewer.RunID || runIDs[3] != result.RootRunID {
		t.Fatalf(
			"invocation order=%v want Specialists -> Reviewer %q -> Root %q",
			runIDs,
			result.Reviewer.RunID,
			result.RootRunID,
		)
	}
}

func TestCompositeChatServiceReviewerRejectTerminatesRootWithoutMerge(
	t *testing.T,
) {
	fixture := newCompositeChatServiceFixtureWithReviewer(
		t,
		false,
		compositeReviewerRejectTest,
	)
	input := fixture.base.input(
		"composite-reviewer-reject-request",
		"reject contradictory specialist results",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)

	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if first.Reviewer == nil ||
		first.Reviewer.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.TerminalResult == nil ||
		first.FailureCode != "COMPOSITE_REVIEW_REJECTED" || first.Reply != "" {
		t.Fatalf("REJECT Chat=%+v", first)
	}
	root := loadCompositeChatRun(t, fixture.base.store, first.RootRunID)
	if len(root.ModelDispatches) != 0 || root.CompositeReviewer == nil ||
		root.CompositeReviewer.Verdict == nil ||
		root.CompositeReviewer.Verdict.Decision !=
			corecontract.ReviewDecisionRejectV1 {
		t.Fatalf("REJECT root closure=%+v", root)
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("REJECT family model calls=%d want N+1=3 and no merge", got)
	}

	retried, err := fixture.service.Chat(context.Background(), input)
	if err != nil || retried.FailureCode != first.FailureCode {
		t.Fatalf("REJECT retry=%+v error=%v", retried, err)
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("REJECT retry added model calls=%d want 3", got)
	}
}

func TestCompositeChatServiceReviewerUnknownNeverMergesOrReplays(
	t *testing.T,
) {
	fixture := newCompositeChatServiceFixtureWithReviewer(
		t,
		false,
		compositeReviewerUnknownTest,
	)
	input := fixture.base.input(
		"composite-reviewer-unknown-request",
		"preserve an uncertain reviewer outcome",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)

	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if first.Reviewer == nil ||
		first.Reviewer.LoopResult.Disposition !=
			loopapi.DispositionWaitingReconciliation ||
		first.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		first.LoopResult.ReasonCode != "COMPOSITE_REVIEW_UNKNOWN" ||
		first.TerminalResult != nil || first.Reply != "" || first.FailureCode != "" {
		t.Fatalf("UNKNOWN Chat=%+v", first)
	}
	root := loadCompositeChatRun(t, fixture.base.store, first.RootRunID)
	if len(root.ModelDispatches) != 0 || root.CompositeReviewer == nil ||
		root.CompositeReviewer.State != currentstore.CompositeReviewerUnknownV1 {
		t.Fatalf("UNKNOWN root closure=%+v", root)
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("UNKNOWN family model calls=%d want N+1=3 and no merge", got)
	}

	retried, err := fixture.service.Chat(context.Background(), input)
	if err != nil || retried.Reviewer == nil ||
		retried.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation {
		t.Fatalf("UNKNOWN retry=%+v error=%v", retried, err)
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("UNKNOWN retry replayed a model call: got %d want 3", got)
	}
}

func TestCompositeChatServiceInvalidReviewerOutputFailsClosedWithoutMerge(
	t *testing.T,
) {
	fixture := newCompositeChatServiceFixtureWithReviewer(
		t,
		false,
		compositeReviewerInvalidTest,
	)
	input := fixture.base.input(
		"composite-reviewer-invalid-request",
		"fail closed on an invalid reviewer verdict",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)

	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if first.Reviewer == nil ||
		first.Reviewer.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.TerminalResult == nil ||
		first.FailureCode != "COMPOSITE_REVIEW_OUTPUT_INVALID" ||
		first.Reply != "" {
		t.Fatalf("INVALID Chat=%+v", first)
	}
	root := loadCompositeChatRun(t, fixture.base.store, first.RootRunID)
	if len(root.ModelDispatches) != 0 || root.CompositeReviewer == nil ||
		root.CompositeReviewer.State != currentstore.CompositeReviewerFailedV1 ||
		root.CompositeReviewer.ErrorClassification !=
			"COMPOSITE_REVIEW_OUTPUT_INVALID" {
		t.Fatalf("INVALID root closure=%+v", root)
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("INVALID family model calls=%d want N+1=3 and no merge", got)
	}

	retried, err := fixture.service.Chat(context.Background(), input)
	if err != nil || retried.FailureCode != first.FailureCode {
		t.Fatalf("INVALID retry=%+v error=%v", retried, err)
	}
	if got := fixture.base.invoker.callCount(); got != 3 {
		t.Fatalf("INVALID retry added model calls=%d want 3", got)
	}
}

func TestCompositeChatServiceAdvancesReviewerWithoutModelBeforeEverySpecialistSucceeds(
	t *testing.T,
) {
	fixture := newCompositeChatServiceFixtureWithReviewer(
		t,
		true,
		compositeReviewerApproveTest,
	)
	input := fixture.base.input(
		"composite-reviewer-child-unknown-request",
		"do not review an incomplete specialist result set",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)

	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if first.Reviewer == nil || first.Reviewer.RunID == "" ||
		first.Reviewer.LoopResult.RunID != first.Reviewer.RunID ||
		first.Reviewer.LoopResult.Disposition !=
			loopapi.DispositionWaitingReconciliation ||
		first.Reviewer.LoopResult.ReasonCode != "COMPOSITE_CHILD_UNKNOWN" ||
		first.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		first.LoopResult.ReasonCode != "COMPOSITE_CHILD_UNKNOWN" ||
		first.TerminalResult != nil {
		t.Fatalf("Child UNKNOWN Reviewer-enabled Chat=%+v", first)
	}
	reviewer := loadCompositeChatRun(t, fixture.base.store, first.Reviewer.RunID)
	if reviewer.Frame.Step != corecontract.WaitingChildrenLoopStep ||
		len(reviewer.ModelDispatches) != 0 {
		t.Fatalf("premature Reviewer state=%+v", reviewer)
	}
	if got := fixture.base.invoker.callCount(); got != 2 {
		t.Fatalf("Child UNKNOWN model calls=%d want only two Specialists", got)
	}

	retried, err := fixture.service.Chat(context.Background(), input)
	if err != nil || retried.LoopResult.ReasonCode != first.LoopResult.ReasonCode {
		t.Fatalf("Child UNKNOWN retry=%+v error=%v", retried, err)
	}
	if got := fixture.base.invoker.callCount(); got != 2 {
		t.Fatalf("Child UNKNOWN retry added model calls=%d want 2", got)
	}
}

func TestCompositeChatServiceSpecialistFailureTerminatesReviewerAttemptFree(
	t *testing.T,
) {
	fixture := newCompositeChatServiceFixtureWithReviewer(
		t,
		false,
		compositeReviewerApproveTest,
	)
	fixture.invoker.failFirstChild = true
	input := fixture.base.input(
		"composite-reviewer-child-failed-request",
		"close the reviewer family when one specialist fails",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)

	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil {
		t.Fatalf("first Chat: %v", err)
	}
	if first.Reviewer == nil ||
		first.Reviewer.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.Reviewer.LoopResult.ReasonCode !=
			corecontract.AllRequiredChildFailedReasonV1 ||
		first.LoopResult.Disposition != loopapi.DispositionTerminated ||
		first.LoopResult.ReasonCode !=
			corecontract.AllRequiredChildFailedReasonV1 ||
		first.FailureCode != corecontract.AllRequiredChildFailedReasonV1 ||
		first.TerminalResult == nil {
		t.Fatalf("Specialist FAILED Reviewer-enabled Chat=%+v", first)
	}
	reviewer := loadCompositeChatRun(t, fixture.base.store, first.Reviewer.RunID)
	root := loadCompositeChatRun(t, fixture.base.store, first.RootRunID)
	reviewerContinuation, reviewerContinuationErr :=
		corecontract.RestoreLoopContinuationV1(reviewer.Frame.Continuation)
	rootContinuation, rootContinuationErr :=
		corecontract.RestoreLoopContinuationV1(root.Frame.Continuation)
	if reviewer.Frame.Step != corecontract.TerminatedLoopStep ||
		reviewerContinuationErr != nil ||
		reviewerContinuation.CoreFailureReason !=
			corecontract.AllRequiredChildFailedReasonV1 ||
		len(reviewer.ModelDispatches) != 0 ||
		root.Frame.Step != corecontract.TerminatedLoopStep ||
		rootContinuationErr != nil ||
		rootContinuation.CoreFailureReason !=
			corecontract.AllRequiredChildFailedReasonV1 ||
		len(root.ModelDispatches) != 0 {
		t.Fatalf("Specialist FAILED family did not close: reviewer=%+v root=%+v", reviewer, root)
	}
	if got := fixture.base.invoker.callCount(); got != 2 {
		t.Fatalf("Specialist FAILED model calls=%d want only two Specialists", got)
	}

	retried, err := fixture.service.Chat(context.Background(), input)
	if err != nil || retried.FailureCode != first.FailureCode {
		t.Fatalf("Specialist FAILED retry=%+v error=%v", retried, err)
	}
	if got := fixture.base.invoker.callCount(); got != 2 {
		t.Fatalf("Specialist FAILED retry added model calls=%d want 2", got)
	}
}

func assertCompositeReviewerApproveClosure(
	t *testing.T,
	fixture *compositeChatServiceFixture,
	result CompositeChatResult,
) {
	t.Helper()
	root := loadCompositeChatRun(t, fixture.base.store, result.RootRunID)
	if root.Manifest.Composite == nil || root.Manifest.Composite.Plan == nil ||
		root.Manifest.Composite.Plan.Reviewer == nil ||
		result.Reviewer.RunID != root.Manifest.Composite.Plan.Reviewer.RunID ||
		root.CompositeReviewer == nil || root.CompositeReviewer.Verdict == nil ||
		root.CompositeReviewer.State != currentstore.CompositeReviewerSucceededV1 ||
		root.CompositeReviewer.Verdict.Decision !=
			corecontract.ReviewDecisionApproveV1 ||
		len(root.ModelDispatches) != 1 {
		t.Fatalf("APPROVE root closure=%+v result=%+v", root, result)
	}
	reviewer := loadCompositeChatRun(t, fixture.base.store, result.Reviewer.RunID)
	if reviewer.Manifest.Composite == nil ||
		reviewer.Manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		len(reviewer.ModelDispatches) != 1 ||
		reviewer.ModelDispatches[0].Attempt.LogicalStepID !=
			corecontract.CompositeReviewLogicalStepIDV1 ||
		reviewer.ModelDispatches[0].Attempt.State !=
			corecontract.ModelAttemptSucceeded {
		t.Fatalf("APPROVE Reviewer closure=%+v", reviewer)
	}
	fixture.invoker.mu.Lock()
	parsedFamily := fixture.invoker.reviewerFamilyDigest
	parsedSpecialists := fixture.invoker.reviewerSpecialistDigest
	fixture.invoker.mu.Unlock()
	if parsedFamily != root.Manifest.ManifestDigest ||
		parsedSpecialists == "" ||
		!moduleapi.ValidSHA256(parsedSpecialists) ||
		root.CompositeReviewer.Verdict.FamilyDigest != parsedFamily ||
		root.CompositeReviewer.Verdict.SpecialistResultDigest != parsedSpecialists {
		t.Fatalf(
			"Reviewer request bindings family=%q specialists=%q verdict=%+v root=%q",
			parsedFamily,
			parsedSpecialists,
			root.CompositeReviewer.Verdict,
			root.Manifest.ManifestDigest,
		)
	}
}
