package coreloop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const reviewerPolicyPrefixForTest = "COMPOSITE_REVIEW_POLICY_JSON:\n"

func TestCompositeReviewerApproveGatesAndEnrichesUniqueRootMerge(t *testing.T) {
	fixture := newCompositeReviewerLoopFixture(t, "review-approve")
	invoker := &integrationInvoker{delegate: &compositeReviewerScenarioInvoker{
		provider:      fixture.base.provider,
		mode:          reviewerScenarioApprove,
		reviewerRunID: fixture.reviewerRunID(),
	}}
	loop := newIntegrationLoop(t, fixture.base, invoker)
	runCompositeSpecialists(t, loop, fixture)

	beforeReview, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if beforeReview.Disposition != loopapi.DispositionWaitingExternal ||
		beforeReview.ReasonCode != reasonCompositeReviewPending {
		t.Fatalf("Root before review=%+v", beforeReview)
	}
	assertNoCompositeModelAttempt(
		t,
		fixture,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)

	reviewed, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.reviewerRunID()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.Disposition != loopapi.DispositionTerminated ||
		reviewed.ReasonCode != reasonModelSucceeded {
		t.Fatalf("Reviewer result=%+v", reviewed)
	}
	reviewAttempt := reviewerAttemptRecord(t, fixture)
	if reviewAttempt.Attempt.LogicalStepID != corecontract.CompositeReviewLogicalStepIDV1 ||
		reviewAttempt.Attempt.State != corecontract.ModelAttemptSucceeded {
		t.Fatalf("Reviewer Attempt=%+v", reviewAttempt.Attempt)
	}
	reviewRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		reviewAttempt.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	var reviewParameters map[string]json.RawMessage
	if err := json.Unmarshal(reviewRequest.Parameters, &reviewParameters); err != nil ||
		string(reviewParameters["max_tokens"]) != "512" {
		t.Fatalf("Reviewer Attempt parameters=%s error=%v", reviewRequest.Parameters, err)
	}

	root, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if root.Disposition != loopapi.DispositionTerminated ||
		root.ReasonCode != reasonModelSucceeded ||
		invoker.callCount() != int32(len(fixture.compiled.Children)+2) {
		t.Fatalf("Root=%+v calls=%d", root, invoker.callCount())
	}
	mergeAttemptID := compositeModelAttemptID(
		t,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)
	merge, err := fixture.base.store.GetModelDispatchRecord(
		context.Background(),
		mergeAttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		merge.Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	foundVerdict := false
	for _, message := range request.Messages {
		if strings.HasPrefix(
			message.Content,
			"UNTRUSTED_COMPOSITE_REVIEW_VERDICT_JSON:\n",
		) {
			foundVerdict = true
		}
	}
	if !foundVerdict {
		t.Fatalf("Root merge request lacks APPROVE verdict: %+v", request.Messages)
	}
}

func TestReviewerMaxTokensUsesStrictMinimumAndRejectsInvalidValues(t *testing.T) {
	fixture := newCompositeReviewerLoopFixture(t, "review-max-tokens")
	run := loadCompositeRunForLoop(t, fixture, fixture.reviewerRunID())
	for _, test := range []struct {
		name       string
		parameters json.RawMessage
		want       string
		wantError  bool
	}{
		{name: "insert ceiling", parameters: json.RawMessage(`{}`), want: `{"max_tokens":512}`},
		{name: "keep tighter", parameters: json.RawMessage(`{"max_tokens":128,"temperature":0}`), want: `{"max_tokens":128,"temperature":0}`},
		{name: "tighten larger", parameters: json.RawMessage(`{"max_tokens":2048}`), want: `{"max_tokens":512}`},
		{name: "zero", parameters: json.RawMessage(`{"max_tokens":0}`), wantError: true},
		{name: "fraction", parameters: json.RawMessage(`{"max_tokens":1.5}`), wantError: true},
		{name: "string", parameters: json.RawMessage(`{"max_tokens":"128"}`), wantError: true},
		{name: "overflow", parameters: json.RawMessage(`{"max_tokens":18446744073709551616}`), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := reviewerTightenedModelParametersV1(run, test.parameters)
			if test.wantError {
				if err == nil {
					t.Fatalf("invalid parameters accepted: %s", got)
				}
				return
			}
			if err != nil || string(got) != test.want {
				t.Fatalf("parameters=%s want=%s error=%v", got, test.want, err)
			}
		})
	}
}

func TestCompositeReviewerRejectTerminatesRootWithoutMergeAttempt(t *testing.T) {
	fixture := newCompositeReviewerLoopFixture(t, "review-reject")
	invoker := &integrationInvoker{delegate: &compositeReviewerScenarioInvoker{
		provider:      fixture.base.provider,
		mode:          reviewerScenarioReject,
		reviewerRunID: fixture.reviewerRunID(),
	}}
	loop := newIntegrationLoop(t, fixture.base, invoker)
	runCompositeSpecialists(t, loop, fixture)
	if result, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.reviewerRunID()),
	); err != nil || result.ReasonCode != reasonModelSucceeded {
		t.Fatalf("Reviewer=%+v error=%v", result, err)
	}

	root, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if root.Disposition != loopapi.DispositionTerminated ||
		root.ReasonCode != reasonCompositeReviewRejected {
		t.Fatalf("Rejected Root=%+v", root)
	}
	assertAttemptFreeReviewerRootTerminal(
		t,
		fixture,
		reasonCompositeReviewRejected,
	)
}

func TestCompositeReviewerFailedAndInvalidTerminateRootAttemptFree(t *testing.T) {
	tests := []struct {
		name               string
		mode               reviewerScenarioMode
		wantRootReason     string
		wantClassification string
	}{
		{
			name:               "invalid verdict",
			mode:               reviewerScenarioInvalid,
			wantRootReason:     reasonCompositeReviewOutputInvalid,
			wantClassification: reasonCompositeReviewOutputInvalid,
		},
		{
			name:               "provider failure",
			mode:               reviewerScenarioFailed,
			wantRootReason:     reasonCompositeReviewFailed,
			wantClassification: "PROVIDER_REPORTED_FAILURE",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCompositeReviewerLoopFixture(t, "review-"+strings.ReplaceAll(test.name, " ", "-"))
			invoker := &integrationInvoker{delegate: &compositeReviewerScenarioInvoker{
				provider:      fixture.base.provider,
				mode:          test.mode,
				reviewerRunID: fixture.reviewerRunID(),
			}}
			loop := newIntegrationLoop(t, fixture.base, invoker)
			runCompositeSpecialists(t, loop, fixture)
			if _, err := loop.Run(
				context.Background(),
				integrationRunInput(fixture.reviewerRunID()),
			); err != nil {
				t.Fatal(err)
			}
			review := reviewerAttemptRecord(t, fixture)
			if review.Attempt.State != corecontract.ModelAttemptFailed ||
				review.Attempt.ErrorClassification != test.wantClassification {
				t.Fatalf("Reviewer Attempt=%+v", review.Attempt)
			}
			if test.mode == reviewerScenarioInvalid &&
				(review.Usage.Tokens.Output == nil ||
					*review.Usage.Tokens.Output != 7 ||
					review.Usage.RawReceiptRef == "") {
				t.Fatalf("invalid-verdict Usage was not retained: %+v", review.Usage)
			}
			rootView := loadCompositeRunForLoop(
				t,
				fixture,
				fixture.rootRunID(),
			)
			if rootView.CompositeReviewer == nil ||
				rootView.CompositeReviewer.State != currentstore.CompositeReviewerFailedV1 ||
				rootView.CompositeReviewer.ErrorClassification != test.wantClassification {
				t.Fatalf("Root Reviewer projection=%+v", rootView.CompositeReviewer)
			}
			root, err := loop.Run(
				context.Background(),
				integrationRunInput(fixture.rootRunID()),
			)
			if err != nil {
				t.Fatal(err)
			}
			if root.Disposition != loopapi.DispositionTerminated ||
				root.ReasonCode != test.wantRootReason {
				t.Fatalf("Root=%+v", root)
			}
			assertAttemptFreeReviewerRootTerminal(
				t,
				fixture,
				test.wantRootReason,
			)
		})
	}
}

func TestCompositeReviewerUnknownNeverReplaysOrPermitsMerge(t *testing.T) {
	fixture := newCompositeReviewerLoopFixture(t, "review-unknown")
	invoker := &integrationInvoker{delegate: &compositeReviewerScenarioInvoker{
		provider:      fixture.base.provider,
		mode:          reviewerScenarioUnknown,
		reviewerRunID: fixture.reviewerRunID(),
	}}
	loop := newIntegrationLoop(t, fixture.base, invoker)
	runCompositeSpecialists(t, loop, fixture)

	first, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.reviewerRunID()),
	)
	if err != nil || first.Disposition != loopapi.DispositionWaitingReconciliation {
		t.Fatalf("first Reviewer=%+v error=%v", first, err)
	}
	calls := invoker.callCount()
	second, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.reviewerRunID()),
	)
	if err != nil || second.Disposition != loopapi.DispositionWaitingReconciliation ||
		invoker.callCount() != calls {
		t.Fatalf("second Reviewer=%+v error=%v calls=%d want=%d", second, err, invoker.callCount(), calls)
	}
	root, err := loop.Run(
		context.Background(),
		integrationRunInput(fixture.rootRunID()),
	)
	if err != nil || root.Disposition != loopapi.DispositionWaitingReconciliation ||
		root.ReasonCode != reasonCompositeReviewUnknown ||
		invoker.callCount() != calls {
		t.Fatalf("Root=%+v error=%v calls=%d want=%d", root, err, invoker.callCount(), calls)
	}
	assertNoCompositeModelAttempt(
		t,
		fixture,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)
}

type reviewerScenarioMode string

const (
	reviewerScenarioApprove reviewerScenarioMode = "approve"
	reviewerScenarioReject  reviewerScenarioMode = "reject"
	reviewerScenarioInvalid reviewerScenarioMode = "invalid"
	reviewerScenarioUnknown reviewerScenarioMode = "unknown"
	reviewerScenarioFailed  reviewerScenarioMode = "failed"
)

type compositeReviewerScenarioInvoker struct {
	provider      moduleapi.ActivatedModuleRef
	mode          reviewerScenarioMode
	reviewerRunID string
}

func (invoker *compositeReviewerScenarioInvoker) Invoke(
	_ context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	if prepared.Invocation.RunID != invoker.reviewerRunID {
		return reviewerScenarioOutput(
			invoker.provider,
			"result for "+prepared.Invocation.RunID,
		)
	}
	switch invoker.mode {
	case reviewerScenarioUnknown:
		return modulehost.InvocationResult{
			Provider: invoker.provider,
			Outcome:  modulehost.InvocationUnknown,
		}, nil
	case reviewerScenarioFailed:
		return modulehost.InvocationResult{
			Provider: invoker.provider,
			Outcome:  modulehost.InvocationFailed,
		}, nil
	case reviewerScenarioInvalid:
		result, err := reviewerScenarioOutput(
			invoker.provider,
			"```json\n{\"decision\":\"APPROVE\"}\n```",
		)
		if err != nil {
			return modulehost.InvocationResult{}, err
		}
		outputTokens := uint64(7)
		_, usageCanonical, err := moduleapi.NewModelUsageReceiptV2(
			moduleapi.ModelUsageReceiptV2{
				SchemaVersion: moduleapi.ModelUsageReceiptSchemaV2,
				OutputTokens:  &outputTokens,
				RawReceipt:    json.RawMessage(`{"output_tokens":7}`),
			},
		)
		if err != nil {
			return modulehost.InvocationResult{}, err
		}
		result.UsageReceipt = usageCanonical
		return result, nil
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		prepared.Invocation.Input,
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	var policy struct {
		FamilyDigest           string `json:"family_digest"`
		SpecialistResultDigest string `json:"specialist_result_digest"`
	}
	found := false
	for _, message := range request.Messages {
		if !strings.HasPrefix(message.Content, reviewerPolicyPrefixForTest) {
			continue
		}
		if err := json.Unmarshal(
			[]byte(strings.TrimPrefix(message.Content, reviewerPolicyPrefixForTest)),
			&policy,
		); err != nil {
			return modulehost.InvocationResult{}, err
		}
		found = true
		break
	}
	if !found {
		return modulehost.InvocationResult{}, context.Canceled
	}
	verdict := corecontract.ReviewVerdictV1{
		SchemaVersion:          corecontract.ReviewVerdictSchemaVersionV1,
		FamilyDigest:           policy.FamilyDigest,
		SpecialistResultDigest: policy.SpecialistResultDigest,
		Decision:               corecontract.ReviewDecisionApproveV1,
		IssueCodes:             []corecontract.ReviewIssueCodeV1{},
		AffectedSlotIDs:        []string{},
		BoundedReason:          "The specialist results pass the frozen review gate.",
	}
	if invoker.mode == reviewerScenarioReject {
		verdict.Decision = corecontract.ReviewDecisionRejectV1
		verdict.IssueCodes = []corecontract.ReviewIssueCodeV1{
			corecontract.ReviewIssueMissingEvidenceV1,
		}
		verdict.AffectedSlotIDs = []string{"slot-analysis"}
		verdict.BoundedReason = "The risk result lacks required evidence."
	}
	_, canonical, err := corecontract.NewReviewVerdictV1(verdict)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	return reviewerScenarioOutput(invoker.provider, string(canonical))
}

func reviewerScenarioOutput(
	provider moduleapi.ActivatedModuleRef,
	text string,
) (modulehost.InvocationResult, error) {
	_, canonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: text,
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	return modulehost.InvocationResult{
		Provider: provider,
		Outcome:  modulehost.InvocationSucceeded,
		Output:   canonical,
	}, nil
}

func runCompositeSpecialists(
	t *testing.T,
	loop *UniversalLoop,
	fixture *compositeLoopFixture,
) {
	t.Helper()
	for index := range fixture.compiled.Children {
		result, err := loop.Run(
			context.Background(),
			integrationRunInput(fixture.childRunID(index)),
		)
		if err != nil || result.Disposition != loopapi.DispositionTerminated ||
			result.ReasonCode != reasonModelSucceeded {
			t.Fatalf("Specialist %d=%+v error=%v", index, result, err)
		}
	}
}

func reviewerAttemptRecord(
	t *testing.T,
	fixture *compositeLoopFixture,
) currentstore.ModelDispatchRecord {
	t.Helper()
	attemptID := compositeModelAttemptID(
		t,
		fixture.reviewerRunID(),
		fixture.compiled.Reviewer.MemberSnapshot.MemberID,
		corecontract.CompositeReviewLogicalStepIDV1,
	)
	record, err := fixture.base.store.GetModelDispatchRecord(
		context.Background(),
		attemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func assertAttemptFreeReviewerRootTerminal(
	t *testing.T,
	fixture *compositeLoopFixture,
	wantReason string,
) {
	t.Helper()
	assertNoCompositeModelAttempt(
		t,
		fixture,
		fixture.rootRunID(),
		fixture.compiled.Parent.MemberSnapshot.MemberID,
		corecontract.CompositeMergeLogicalStepIDV1,
	)
	terminal, err := fixture.base.store.GetTerminalRunResult(
		context.Background(),
		fixture.rootRunID(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.ReasonCode != wantReason || terminal.AttemptID != "" ||
		terminal.AttemptKind != "" || terminal.State != "" ||
		terminal.ModelState != "" {
		t.Fatalf("attempt-free Root terminal=%+v", terminal)
	}
}
