package s3eval

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestRunThreeWorkspacesReportsReviewerUsageAndServiceOrder(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	reader := &fakeUsageReader{projections: map[string]currentstore.CompositeFamilyUsageProjectionV1{
		"root-a": fakeProjection(
			"root-a",
			[]fakeAttemptSpec{
				{runID: "child-a", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-01-a-child", createdAt: base.Add(time.Millisecond)},
				{runID: "reviewer-a", role: corecontract.CompositeRunRoleReviewerV1, attemptID: "attempt-04-a-reviewer", createdAt: base.Add(4 * time.Millisecond)},
				{runID: "root-a", role: corecontract.CompositeRunRoleRootV1, attemptID: "attempt-07-a-root", createdAt: base.Add(7 * time.Millisecond)},
			},
			30, 10, 20,
		),
		"root-b": fakeProjection(
			"root-b",
			[]fakeAttemptSpec{
				{runID: "child-b", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-02-b-child", createdAt: base.Add(2 * time.Millisecond)},
				{runID: "root-b", role: corecontract.CompositeRunRoleRootV1, attemptID: "attempt-05-b-root", createdAt: base.Add(5 * time.Millisecond), state: corecontract.ModelAttemptFailed},
			},
			20, 5, 15,
		),
		"root-c": fakeProjection(
			"root-c",
			[]fakeAttemptSpec{
				{runID: "child-c", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-03-c-child", createdAt: base.Add(3 * time.Millisecond)},
				{runID: "root-c", role: corecontract.CompositeRunRoleRootV1, attemptID: "attempt-06-c-root", createdAt: base.Add(6 * time.Millisecond)},
			},
			20, 0, 20,
		),
	}}
	chat, maxActive := concurrentFakeChat(t, nil)

	report, err := Run(context.Background(), threeWorkspaceInput(), chat, reader)
	if err != nil {
		t.Fatal(err)
	}
	if maxActive.Load() != WorkspaceCount {
		t.Fatalf("maximum concurrent Chat calls=%d", maxActive.Load())
	}
	if len(report.Families) != WorkspaceCount ||
		report.Families[0].WorkspaceID != "workspace-a" ||
		report.Families[1].WorkspaceID != "workspace-b" ||
		report.Families[2].WorkspaceID != "workspace-c" {
		t.Fatalf("family order=%+v", report.Families)
	}
	if report.Families[0].Reviewer == nil ||
		report.Families[1].Reviewer != nil ||
		report.Families[2].Reviewer != nil {
		t.Fatalf("Reviewer enabled/disabled report=%+v", report.Families)
	}
	if len(report.Families[0].Results) != 3 ||
		len(report.Families[1].Results) != 1 ||
		len(report.Families[2].Results) != 2 ||
		report.Families[0].Results[1].Role != corecontract.CompositeRunRoleReviewerV1 ||
		report.Families[0].Results[1].ReviewerVerdict == nil ||
		report.Families[0].Results[1].ReviewerVerdict.Decision !=
			corecontract.ReviewDecisionApproveV1 {
		t.Fatalf("content-verified result evidence=%+v", report.Families)
	}
	for _, family := range report.Families {
		for _, result := range family.Results {
			if result.AssistantText == "" ||
				!moduleapi.ValidSHA256(result.ResultDigest) {
				t.Fatalf("invalid result evidence=%+v", result)
			}
		}
	}
	if report.Families[0].Reply != "reply-a" ||
		report.Families[0].Failure != "" ||
		report.Families[1].Reply != "" ||
		report.Families[1].Failure != "failure-b" {
		t.Fatalf("root reply/failure report=%+v", report.Families)
	}
	if report.Families[0].Tokens.CacheHitRatio == nil ||
		math.Abs(*report.Families[0].Tokens.CacheHitRatio-(1.0/3.0)) > 1e-12 ||
		report.Families[2].Tokens.CacheHitRatio == nil ||
		*report.Families[2].Tokens.CacheHitRatio != 0 {
		t.Fatalf("cache ratios=%+v", report.Families)
	}
	if len(report.ServiceOrder) != 7 {
		t.Fatalf("service order=%+v", report.ServiceOrder)
	}
	wantWorkspaceOrder := []string{
		"workspace-a", "workspace-b", "workspace-c", "workspace-a",
		"workspace-b", "workspace-c", "workspace-a",
	}
	for index, wantWorkspace := range wantWorkspaceOrder {
		attempt := report.ServiceOrder[index]
		if attempt.ServiceOrder != uint64(index+1) ||
			attempt.WorkspaceID != wantWorkspace ||
			attempt.Elapsed != 2*time.Millisecond ||
			attempt.Provider != "deepseek" ||
			attempt.Model != "deepseek-v4-pro" ||
			attempt.RequestDigest == "" {
			t.Fatalf("service Attempt %d=%+v", index, attempt)
		}
	}
	if report.Fairness.JainIndex == nil ||
		math.Abs(*report.Fairness.JainIndex-(49.0/51.0)) > 1e-12 ||
		report.Fairness.Starvation ||
		report.Fairness.FirstServedWorkspace != "workspace-a" ||
		report.Fairness.LongestConsecutiveCount != 1 {
		t.Fatalf("fairness=%+v", report.Fairness)
	}
	for _, family := range report.Families {
		if family.WallElapsed < 0 ||
			family.FinishedAt.Sub(family.StartedAt) != family.WallElapsed ||
			family.Root.Disposition != loopapi.DispositionTerminated ||
			len(family.Children) != 1 {
			t.Fatalf("family timing/disposition=%+v", family)
		}
	}
}

func TestRunPreservesUnknownTokensAndReturnsConcurrentErrors(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.August, 4, 13, 0, 0, 0, time.UTC)
	reader := &fakeUsageReader{projections: map[string]currentstore.CompositeFamilyUsageProjectionV1{
		"root-a": fakeProjection("root-a", []fakeAttemptSpec{
			{runID: "child-a", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-a-child", createdAt: base},
			{runID: "reviewer-a", role: corecontract.CompositeRunRoleReviewerV1, attemptID: "attempt-a-reviewer", createdAt: base.Add(3 * time.Millisecond)},
			{runID: "root-a", role: corecontract.CompositeRunRoleRootV1, attemptID: "attempt-a-root", createdAt: base.Add(6 * time.Millisecond)},
		}, 10, 1, 9),
		"root-b": fakeProjection("root-b", []fakeAttemptSpec{
			{runID: "child-b", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-b-child", createdAt: base.Add(time.Millisecond)},
			{runID: "root-b", role: corecontract.CompositeRunRoleRootV1, attemptID: "attempt-b-root", createdAt: base.Add(4 * time.Millisecond), state: corecontract.ModelAttemptFailed},
		}, 10, 1, 9),
		"root-c": fakeProjection("root-c", []fakeAttemptSpec{
			{runID: "child-c", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-c-child", createdAt: base.Add(2 * time.Millisecond)},
			{runID: "root-c", role: corecontract.CompositeRunRoleRootV1, attemptID: "attempt-c-root", createdAt: base.Add(5 * time.Millisecond)},
		}, 10, 1, 9),
	}}
	unknownProjection := reader.projections["root-a"]
	unknownProjection.Aggregate.TokenTotals.CachedInput = nil
	unknownProjection.Aggregate.TokenTotals.UncachedInput = nil
	unknownProjection.Aggregate.TokenTotals.Output = nil
	reader.projections["root-a"] = unknownProjection
	sentinel := errors.New("synthetic Workspace failure")
	chat, maxActive := concurrentFakeChat(t, map[string]error{
		"workspace-b": sentinel,
	})

	report, err := Run(context.Background(), threeWorkspaceInput(), chat, reader)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run error=%v", err)
	}
	if maxActive.Load() != WorkspaceCount || reader.callCount() != WorkspaceCount {
		t.Fatalf(
			"concurrent completion max=%d projection calls=%d",
			maxActive.Load(),
			reader.callCount(),
		)
	}
	if report.Families[0].Tokens.Totals.CachedInput != nil ||
		report.Families[0].Tokens.Totals.UncachedInput != nil ||
		report.Families[0].Tokens.Totals.Output != nil ||
		report.Families[0].Tokens.CacheHitRatio != nil {
		t.Fatalf("UNKNOWN token fields were fabricated: %+v", report.Families[0].Tokens)
	}
	if report.Families[1].Error == "" ||
		report.Families[0].Error != "" ||
		report.Families[2].Error != "" ||
		len(report.ServiceOrder) != 7 ||
		report.Fairness.Starvation {
		t.Fatalf("partial concurrent report=%+v", report)
	}
}

func TestRunRejectsSuccessfulAttemptWithoutClosedTerminalResult(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.August, 4, 14, 0, 0, 0, time.UTC)
	reader := &fakeUsageReader{
		projections: map[string]currentstore.CompositeFamilyUsageProjectionV1{
			"root-a": fakeProjection("root-a", []fakeAttemptSpec{
				{runID: "child-a", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-a-child", createdAt: base},
				{runID: "reviewer-a", role: corecontract.CompositeRunRoleReviewerV1, attemptID: "attempt-a-reviewer", createdAt: base.Add(3 * time.Millisecond)},
				{runID: "root-a", role: corecontract.CompositeRunRoleRootV1, attemptID: "attempt-a-root", createdAt: base.Add(6 * time.Millisecond)},
			}, 3, 0, 3),
			"root-b": fakeProjection("root-b", []fakeAttemptSpec{
				{runID: "child-b", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-b-child", createdAt: base.Add(time.Millisecond)},
				{runID: "root-b", role: corecontract.CompositeRunRoleRootV1, attemptID: "attempt-b-root", createdAt: base.Add(4 * time.Millisecond), state: corecontract.ModelAttemptFailed},
			}, 2, 0, 2),
			"root-c": fakeProjection("root-c", []fakeAttemptSpec{
				{runID: "child-c", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-c-child", createdAt: base.Add(2 * time.Millisecond)},
				{runID: "root-c", role: corecontract.CompositeRunRoleRootV1, attemptID: "attempt-c-root", createdAt: base.Add(5 * time.Millisecond)},
			}, 2, 0, 2),
		},
		terminalErrors: map[string]error{
			"root-b": errors.New("terminal projection unavailable"),
		},
	}
	chat, _ := concurrentFakeChat(t, nil)
	report, err := Run(context.Background(), threeWorkspaceInput(), chat, reader)
	if !errors.Is(err, ErrMetricIntegrity) {
		t.Fatalf("terminal result integrity error=%v", err)
	}
	if report.Families[1].Error == "" || len(report.Families[1].Results) != 1 ||
		report.Families[1].Results[0].RunID != "child-b" {
		t.Fatalf("invalid terminal result was reported as evidence: %+v", report.Families[1])
	}
}

func TestApplyTerminalResultsRejectsReviewerVerdictForAnotherFamily(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.August, 4, 14, 30, 0, 0, time.UTC)
	projection := fakeProjection("root-family", []fakeAttemptSpec{
		{runID: "child-family", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-child", createdAt: base},
		{runID: "reviewer-family", role: corecontract.CompositeRunRoleReviewerV1, attemptID: "attempt-reviewer", createdAt: base.Add(time.Millisecond)},
		{runID: "root-family", role: corecontract.CompositeRunRoleRootV1, attemptID: "attempt-root", createdAt: base.Add(2 * time.Millisecond)},
	}, 30, 0, 30)
	projection.RootManifestDigest = strings.Repeat("f", 64)
	reviewer := DispositionFact{
		RunID:       "reviewer-family",
		Disposition: loopapi.DispositionTerminated,
	}
	report := FamilyReport{
		RootRunID: "root-family",
		Children: []DispositionFact{{
			RunID:       "child-family",
			Disposition: loopapi.DispositionTerminated,
		}},
		Reviewer: &reviewer,
		Root: DispositionFact{
			RunID:       "root-family",
			Disposition: loopapi.DispositionTerminated,
		},
	}
	reader := &fakeUsageReader{
		projections: map[string]currentstore.CompositeFamilyUsageProjectionV1{
			"root-family": projection,
		},
	}

	err := applyTerminalResults(context.Background(), &report, projection, reader)
	if !errors.Is(err, ErrMetricIntegrity) ||
		!strings.Contains(err.Error(), "does not bind root manifest") {
		t.Fatalf("Reviewer family binding error=%v", err)
	}
	if len(report.Results) != 2 ||
		report.Results[0].RunID != "child-family" ||
		report.Results[1].RunID != "root-family" {
		t.Fatalf("mismatched Reviewer verdict became evidence: %+v", report.Results)
	}
}

func TestRunFamilyVerifiesAttemptFreeFailureAndKeepsLaterTerminalEvidence(
	t *testing.T,
) {
	t.Parallel()
	base := time.Date(2026, time.August, 4, 15, 0, 0, 0, time.UTC)
	projection := fakeProjection(
		"root-durable",
		[]fakeAttemptSpec{
			{runID: "child-bad", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-child-bad", createdAt: base},
			{runID: "child-good", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-child-good", createdAt: base.Add(time.Millisecond)},
			{runID: "reviewer-durable", role: corecontract.CompositeRunRoleReviewerV1, attemptID: "attempt-reviewer", createdAt: base.Add(2 * time.Millisecond)},
			{runID: "root-durable", role: corecontract.CompositeRunRoleRootV1, attemptID: "unused-root-attempt", createdAt: base.Add(3 * time.Millisecond)},
		},
		40,
		10,
		30,
	)
	projection.RootManifestDigest = strings.Repeat("c", 64)
	projection.Runs[3].Attempt = nil
	projection.Aggregate.AttemptSlotsUsed = 3

	_, verdictCanonical, err := corecontract.NewReviewVerdictV1(
		corecontract.ReviewVerdictV1{
			SchemaVersion:          corecontract.ReviewVerdictSchemaVersionV1,
			FamilyDigest:           strings.Repeat("c", 64),
			SpecialistResultDigest: strings.Repeat("d", 64),
			Decision:               corecontract.ReviewDecisionRejectV1,
			IssueCodes: []corecontract.ReviewIssueCodeV1{
				corecontract.ReviewIssueContradictionV1,
			},
			AffectedSlotIDs: []string{"backend"},
			BoundedReason:   "The specialist results contradict the frozen boundary.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reviewerOutput, reviewerOutputCanonical, err :=
		moduleapi.NewModelGenerateOutputV1(moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(verdictCanonical),
		})
	if err != nil {
		t.Fatal(err)
	}
	reader := &fakeUsageReader{
		projections: map[string]currentstore.CompositeFamilyUsageProjectionV1{
			"root-durable": projection,
		},
		terminalErrors: map[string]error{
			"child-bad": errors.New("synthetic first terminal read failure"),
		},
		terminals: map[string]currentstore.TerminalRunResult{
			"reviewer-durable": {
				RunID:           "reviewer-durable",
				FrameRevision:   17,
				ReasonCode:      "MODEL_SUCCEEDED",
				AttemptID:       "attempt-reviewer",
				AttemptKind:     corecontract.AttemptKindModel,
				State:           corecontract.ModelAttemptSucceeded,
				ModelState:      corecontract.ModelAttemptSucceeded,
				Output:          reviewerOutput,
				OutputCanonical: reviewerOutputCanonical,
			},
			"root-durable": {
				RunID:               "root-durable",
				FrameRevision:       19,
				ReasonCode:          "COMPOSITE_REVIEW_REJECTED",
				ErrorClassification: "COMPOSITE_REVIEW_REJECTED",
			},
		},
	}
	chat := func(
		context.Context,
		localchat.ChatInput,
	) (localchat.CompositeChatResult, error) {
		return localchat.CompositeChatResult{
			RequestID: "request-durable",
			RootRunID: "root-durable",
			Children: []localchat.CompositeChildChatResult{
				{RunID: "child-bad", LoopResult: loopapi.RunResult{RunID: "child-bad", Disposition: loopapi.DispositionTerminated, ReasonCode: "TRANSIENT"}},
				{RunID: "child-good", LoopResult: loopapi.RunResult{RunID: "child-good", Disposition: loopapi.DispositionTerminated, ReasonCode: "TRANSIENT"}},
			},
			Reviewer: &localchat.CompositeReviewerChatResult{
				RunID: "reviewer-durable",
				LoopResult: loopapi.RunResult{
					RunID:       "reviewer-durable",
					Disposition: loopapi.DispositionTerminated,
					ReasonCode:  "TRANSIENT",
				},
			},
			LoopResult: loopapi.RunResult{
				RunID:       "root-durable",
				Disposition: loopapi.DispositionTerminated,
				ReasonCode:  "TRANSIENT",
			},
			FailureCode: "COMPOSITE_REVIEW_REJECTED",
		}, nil
	}

	report, err := runFamily(
		context.Background(),
		localchat.ChatInput{WorkspaceID: "workspace-durable"},
		chat,
		reader,
	)
	if !errors.Is(err, ErrMetricIntegrity) ||
		!strings.Contains(err.Error(), "child-bad") {
		t.Fatalf("terminal extraction error=%v", err)
	}
	if len(report.Results) != 2 ||
		report.Results[0].RunID != "child-good" ||
		report.Results[1].RunID != "reviewer-durable" ||
		report.Results[1].ReviewerVerdict == nil ||
		report.Results[1].ReviewerVerdict.Decision !=
			corecontract.ReviewDecisionRejectV1 {
		t.Fatalf("later durable results were not preserved: %+v", report.Results)
	}
	for _, runID := range []string{
		"child-bad", "child-good", "reviewer-durable", "root-durable",
	} {
		if reader.terminalCallCount(runID) != 1 {
			t.Fatalf("terminal calls for %s=%d, want 1", runID, reader.terminalCallCount(runID))
		}
	}
	if report.Root.FrameRevision != 19 ||
		report.Root.ReasonCode != "COMPOSITE_REVIEW_REJECTED" ||
		report.Failure != "COMPOSITE_REVIEW_REJECTED" || report.Reply != "" {
		t.Fatalf("durable root failure was not applied: %+v", report)
	}
}

func TestRunFamilyDoesNotReadNonTerminalUnknownOrPendingRuns(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.August, 4, 16, 0, 0, 0, time.UTC)
	projection := fakeProjection(
		"root-waiting",
		[]fakeAttemptSpec{
			{runID: "child-unknown", role: corecontract.CompositeRunRoleChildV1, attemptID: "attempt-child-unknown", createdAt: base, state: corecontract.ModelAttemptUnknown},
			{runID: "root-waiting", role: corecontract.CompositeRunRoleRootV1, attemptID: "unused-root-attempt", createdAt: base.Add(time.Millisecond)},
		},
		10,
		0,
		10,
	)
	projection.Runs[1].Attempt = nil
	projection.Aggregate.AttemptSlotsUsed = 1
	reader := &fakeUsageReader{
		projections: map[string]currentstore.CompositeFamilyUsageProjectionV1{
			"root-waiting": projection,
		},
		terminalErrors: map[string]error{
			"child-unknown": errors.New("must not read UNKNOWN as terminal"),
			"root-waiting":  errors.New("must not read PENDING root as terminal"),
		},
	}
	chat := func(
		context.Context,
		localchat.ChatInput,
	) (localchat.CompositeChatResult, error) {
		return localchat.CompositeChatResult{
			RequestID: "request-waiting",
			RootRunID: "root-waiting",
			Children: []localchat.CompositeChildChatResult{{
				RunID: "child-unknown",
				LoopResult: loopapi.RunResult{
					RunID:       "child-unknown",
					Disposition: loopapi.DispositionWaitingReconciliation,
					ReasonCode:  "MODEL_UNKNOWN",
				},
			}},
			LoopResult: loopapi.RunResult{
				RunID:       "root-waiting",
				Disposition: loopapi.DispositionWaitingReconciliation,
				ReasonCode:  "COMPOSITE_CHILD_UNKNOWN",
			},
		}, nil
	}

	report, err := runFamily(
		context.Background(),
		localchat.ChatInput{WorkspaceID: "workspace-waiting"},
		chat,
		reader,
	)
	if err != nil {
		t.Fatalf("non-terminal evidence: %v", err)
	}
	if reader.terminalCallCount("child-unknown") != 0 ||
		reader.terminalCallCount("root-waiting") != 0 ||
		len(report.Results) != 0 {
		t.Fatalf("non-terminal Run was read as terminal: report=%+v", report)
	}
}

type fakeUsageReader struct {
	mu             sync.Mutex
	projections    map[string]currentstore.CompositeFamilyUsageProjectionV1
	terminalErrors map[string]error
	terminals      map[string]currentstore.TerminalRunResult
	terminalCalls  map[string]int
	calls          int
}

func (reader *fakeUsageReader) GetCompositeFamilyUsageProjection(
	_ context.Context,
	rootRunID string,
) (currentstore.CompositeFamilyUsageProjectionV1, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.calls++
	return reader.projections[rootRunID], nil
}

func (reader *fakeUsageReader) GetTerminalRunResult(
	_ context.Context,
	runID string,
) (currentstore.TerminalRunResult, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.terminalCalls == nil {
		reader.terminalCalls = make(map[string]int)
	}
	reader.terminalCalls[runID]++
	if err := reader.terminalErrors[runID]; err != nil {
		return currentstore.TerminalRunResult{}, err
	}
	if terminal, found := reader.terminals[runID]; found {
		terminal.OutputCanonical = append([]byte(nil), terminal.OutputCanonical...)
		return terminal, nil
	}
	for _, projection := range reader.projections {
		for _, run := range projection.Runs {
			if run.RunID != runID || run.Attempt == nil {
				continue
			}
			if run.Attempt.State == corecontract.ModelAttemptFailed {
				classification := "failure-" + strings.TrimPrefix(runID, "root-")
				return currentstore.TerminalRunResult{
					RunID:               runID,
					FrameRevision:       8,
					ReasonCode:          "MODEL_FAILED",
					AttemptID:           run.Attempt.AttemptID,
					AttemptKind:         corecontract.AttemptKindModel,
					State:               corecontract.ModelAttemptFailed,
					ModelState:          corecontract.ModelAttemptFailed,
					ErrorClassification: classification,
				}, nil
			}
			assistantText := "result for " + runID
			if run.Role == corecontract.CompositeRunRoleRootV1 {
				assistantText = "reply-" + strings.TrimPrefix(runID, "root-")
			}
			if run.Role == corecontract.CompositeRunRoleReviewerV1 {
				_, canonical, err := corecontract.NewReviewVerdictV1(
					corecontract.ReviewVerdictV1{
						SchemaVersion:          corecontract.ReviewVerdictSchemaVersionV1,
						FamilyDigest:           strings.Repeat("a", 64),
						SpecialistResultDigest: strings.Repeat("b", 64),
						Decision:               corecontract.ReviewDecisionApproveV1,
						IssueCodes:             []corecontract.ReviewIssueCodeV1{},
						AffectedSlotIDs:        []string{},
						BoundedReason:          "The frozen specialist set is complete.",
					},
				)
				if err != nil {
					return currentstore.TerminalRunResult{}, err
				}
				assistantText = string(canonical)
			}
			output, canonical, err := moduleapi.NewModelGenerateOutputV1(
				moduleapi.ModelGenerateOutputV1{
					SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
					AssistantText: assistantText,
				},
			)
			if err != nil {
				return currentstore.TerminalRunResult{}, err
			}
			return currentstore.TerminalRunResult{
				RunID:           runID,
				FrameRevision:   7,
				ReasonCode:      "MODEL_SUCCEEDED",
				AttemptID:       run.Attempt.AttemptID,
				AttemptKind:     corecontract.AttemptKindModel,
				State:           corecontract.ModelAttemptSucceeded,
				ModelState:      corecontract.ModelAttemptSucceeded,
				Output:          output,
				OutputCanonical: canonical,
			}, nil
		}
	}
	return currentstore.TerminalRunResult{}, errors.New("terminal result not found")
}

func (reader *fakeUsageReader) callCount() int {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.calls
}

func (reader *fakeUsageReader) terminalCallCount(runID string) int {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.terminalCalls[runID]
}

type fakeAttemptSpec struct {
	runID     string
	role      corecontract.CompositeRunRoleV1
	attemptID string
	createdAt time.Time
	state     corecontract.ModelAttemptState
}

func fakeProjection(
	rootRunID string,
	specs []fakeAttemptSpec,
	input uint64,
	cached uint64,
	uncached uint64,
) currentstore.CompositeFamilyUsageProjectionV1 {
	output := uint64(len(specs))
	reasoning := uint64(0)
	runs := make([]currentstore.CompositeFamilyRunUsageFactV1, len(specs))
	for index, spec := range specs {
		state := spec.state
		if state == "" {
			state = corecontract.ModelAttemptSucceeded
		}
		count := uint64(len(specs))
		attemptInput := input / count
		if uint64(index) < input%count {
			attemptInput++
		}
		attemptCached := cached / count
		if uint64(index) < cached%count {
			attemptCached++
		}
		attemptUncached := attemptInput - attemptCached
		attemptOutput := uint64(1)
		attemptReasoning := uint64(0)
		runs[index] = currentstore.CompositeFamilyRunUsageFactV1{
			RunID: spec.runID,
			Role:  spec.role,
			Attempt: &currentstore.CompositeFamilyAttemptUsageFactV1{
				AttemptID:     spec.attemptID,
				LogicalStepID: "model.generate/v2",
				State:         state,
				Provider:      "deepseek",
				Model:         "deepseek-v4-pro",
				RequestDigest: "digest-" + spec.attemptID,
				CreatedAt:     spec.createdAt,
				UpdatedAt:     spec.createdAt.Add(2 * time.Millisecond),
				Usage: currentstore.ModelUsageRecord{
					Tokens: corecontract.UsageTokens{
						Input:         &attemptInput,
						CachedInput:   &attemptCached,
						UncachedInput: &attemptUncached,
						Output:        &attemptOutput,
						Reasoning:     &attemptReasoning,
					},
				},
			},
		}
	}
	return currentstore.CompositeFamilyUsageProjectionV1{
		RootRunID:          rootRunID,
		RootManifestDigest: strings.Repeat("a", 64),
		Runs:               runs,
		Aggregate: currentstore.CompositeFamilyUsageAggregateV1{
			AttemptSlotsUsed: uint32(len(specs)),
			TokenTotals: currentstore.CompositeFamilyTokenTotalsV1{
				Input:         &input,
				CachedInput:   &cached,
				UncachedInput: &uncached,
				Output:        &output,
				Reasoning:     &reasoning,
			},
		},
	}
}

func threeWorkspaceInput() ExperimentInput {
	return ExperimentInput{Tasks: [WorkspaceCount]WorkspaceTask{
		{ChatInput: localchat.ChatInput{WorkspaceID: "workspace-a"}},
		{ChatInput: localchat.ChatInput{WorkspaceID: "workspace-b"}},
		{ChatInput: localchat.ChatInput{WorkspaceID: "workspace-c"}},
	}}
}

func concurrentFakeChat(
	t *testing.T,
	errorsByWorkspace map[string]error,
) (ChatFunc, *atomic.Int32) {
	t.Helper()
	var ready sync.WaitGroup
	ready.Add(WorkspaceCount)
	var active atomic.Int32
	maxActive := &atomic.Int32{}
	chat := func(
		_ context.Context,
		input localchat.ChatInput,
	) (localchat.CompositeChatResult, error) {
		current := active.Add(1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		defer active.Add(-1)
		ready.Done()
		ready.Wait()

		suffix := string(input.WorkspaceID[len(input.WorkspaceID)-1])
		rootRunID := "root-" + suffix
		result := localchat.CompositeChatResult{
			RequestID: "request-" + suffix,
			RootRunID: rootRunID,
			Children: []localchat.CompositeChildChatResult{{
				RunID: "child-" + suffix,
				LoopResult: loopapi.RunResult{
					RunID:       "child-" + suffix,
					Disposition: loopapi.DispositionTerminated,
					ReasonCode:  "DONE",
				},
			}},
			LoopResult: loopapi.RunResult{
				RunID:       rootRunID,
				Disposition: loopapi.DispositionTerminated,
				ReasonCode:  "DONE",
			},
		}
		switch input.WorkspaceID {
		case "workspace-a":
			result.Reply = "reply-a"
		case "workspace-b":
			result.FailureCode = "failure-b"
		case "workspace-c":
			result.Reply = "reply-c"
		}
		if input.WorkspaceID == "workspace-a" {
			result.Reviewer = &localchat.CompositeReviewerChatResult{
				RunID: "reviewer-a",
				LoopResult: loopapi.RunResult{
					RunID:       "reviewer-a",
					Disposition: loopapi.DispositionTerminated,
					ReasonCode:  "DONE",
				},
			}
		}
		return result, errorsByWorkspace[input.WorkspaceID]
	}
	return chat, maxActive
}
