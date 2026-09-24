package currentstore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompositeReviewerAdmissionAndLoopClosure(t *testing.T) {
	fixture := newCompositeReviewerRuntimeFixture(t)
	result, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("CommitCompositeRunFamily: %v", err)
	}
	if fixture.compiled.Reviewer == nil || fixture.input.Reviewer == nil ||
		result.Reviewer == nil || !result.Reviewer.Created {
		t.Fatalf("Reviewer Admission result=%+v", result.Reviewer)
	}
	reviewer := fixture.compiled.Reviewer.RunManifest
	var disposition, step, waitingReason string
	var continuation []byte
	if err := fixture.store.db.QueryRow(`
		SELECT r.disposition, f.step, f.waiting_reason, f.continuation
		FROM runs AS r
		JOIN loop_frames AS f ON f.run_id=r.run_id
		WHERE r.run_id=?
	`, reviewer.RunID).Scan(
		&disposition,
		&step,
		&waitingReason,
		&continuation,
	); err != nil {
		t.Fatal(err)
	}
	restored, err := corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil || disposition != "WAITING_EXTERNAL" ||
		step != corecontract.WaitingChildrenLoopStep ||
		waitingReason != compositeChildrenPendingWaitingReason ||
		restored.State != corecontract.WaitingChildrenLoopStep {
		t.Fatalf(
			"Reviewer initial projection=(%q,%q,%q,%+v) error=%v",
			disposition,
			step,
			waitingReason,
			restored,
			err,
		)
	}

	lease := acquireCompositeTestLease(
		t,
		fixture.store,
		reviewer.RunID,
		"reviewer-admission-reader",
	)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop(Reviewer): %v", err)
	}
	root := fixture.compiled.Parent.RunManifest
	if run.Manifest.Composite == nil ||
		run.Manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		run.Frame.Step != corecontract.WaitingChildrenLoopStep ||
		run.CompositeRoot == nil ||
		run.CompositeRoot.ManifestDigest != root.ManifestDigest ||
		run.CompositeRoot.Composite == nil ||
		run.CompositeRoot.Composite.Plan == nil ||
		run.CompositeRoot.Composite.Plan.Reviewer == nil ||
		run.CompositeRoot.Composite.Plan.Reviewer.RunID != reviewer.RunID ||
		len(run.CompositeChildren) != len(root.Composite.Plan.Children) ||
		run.CompositeReviewer != nil {
		t.Fatalf("Reviewer Loop closure=%+v", run)
	}
	originalSlot := run.CompositeRoot.Composite.Plan.Children[0].SlotID
	run.CompositeRoot.Composite.Plan.Children[0].SlotID = "caller-mutation"
	again, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil || again.CompositeRoot.Composite.Plan.Children[0].SlotID !=
		originalSlot {
		t.Fatalf("CompositeRoot was not defensively cloned: error=%v", err)
	}
	resolved, found, err := fixture.store.ResolveCompositeRunFamily(
		context.Background(),
		root.TenantID,
		root.AdmissionKey,
		fixture.input.Parent.IntentDigest,
	)
	if err != nil || !found || resolved.Reviewer == nil ||
		resolved.Reviewer.RunID != reviewer.RunID {
		t.Fatalf("ResolveCompositeRunFamily=%+v found=%v error=%v", resolved, found, err)
	}
	_, cancellationCanonical := newFamilyCancellationRequest(
		t,
		root,
		corecontract.CancellationReasonOperatorRequestV1,
	)
	canceled, err := fixture.store.RequestRunCancellation(
		context.Background(),
		RequestRunCancellationInput{Canonical: cancellationCanonical},
	)
	if err != nil {
		t.Fatalf("RequestRunCancellation Reviewer family: %v", err)
	}
	var latched int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM runs
		WHERE (run_id=? OR parent_run_id=?) AND cancel_request_ref=?
	`, root.RunID, root.RunID, canceled.Ref).Scan(&latched); err != nil {
		t.Fatal(err)
	}
	if latched != len(fixture.compiled.Children)+2 {
		t.Fatalf("Reviewer family cancellation rows=%d", latched)
	}
}

func TestCompositeReviewerPermitBlocksBeforeSpecialists(t *testing.T) {
	fixture := newCommittedCompositeReviewerRuntimeFixture(t)
	child := fixture.compiled.Children[0]
	childLease := acquireCompositeTestLease(
		t,
		fixture.store,
		child.RunManifest.RunID,
		"reviewer-gate-request-source",
	)
	childRun, err := fixture.store.LoadRunForLoop(
		context.Background(),
		childLease,
	)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := recompileContextForNewAttempt(
		corecontract.ContextCompilationV1{},
		childRun,
		childRun.Frame.Revision,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := fixture.compiled.Reviewer.RunManifest
	reviewerLease := acquireCompositeTestLease(
		t,
		fixture.store,
		reviewer.RunID,
		"reviewer-gate-worker",
	)
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       reviewerLease,
			AttemptID:                   "attempt-reviewer-too-early",
			LogicalStepID:               corecontract.CompositeReviewLogicalStepIDV1,
			ContextCompilationCanonical: compiled.CompilationCanonical,
			RequestCanonical:            compiled.RequestCanonical,
			Deadline: reviewer.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	); !errors.Is(err, ErrModelDispatchConflict) {
		t.Fatalf("early Reviewer Begin error=%v", err)
	}
	var count int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*) FROM model_dispatch_attempts WHERE attempt_id=?
	`, "attempt-reviewer-too-early").Scan(&count); err != nil || count != 0 {
		t.Fatalf("early Reviewer attempt count=%d error=%v", count, err)
	}
}

func TestCompositeTerminalEvidenceIgnoresPostTerminalLeaseRevision(t *testing.T) {
	fixture := newCommittedCompositeReviewerRuntimeFixture(t)
	terminalLease := finishCompositeReviewerSpecialist(t, fixture, 0, false)
	root := fixture.compiled.Parent.RunManifest
	rootLease := acquireCompositeTestLease(
		t,
		fixture.store,
		root.RunID,
		"terminal-evidence-root-reader",
	)
	before, err := fixture.store.LoadRunForLoop(context.Background(), rootLease)
	if err != nil {
		t.Fatal(err)
	}
	terminalRevision := before.CompositeChildren[0].FrameRevision
	if _, err := fixture.store.RenewRunLease(
		context.Background(),
		RenewRunLeaseInput{
			Lease: terminalLease,
			TTL:   time.Hour,
		},
	); err != nil {
		t.Fatalf("post-terminal RenewRunLease: %v", err)
	}
	after, err := fixture.store.LoadRunForLoop(context.Background(), rootLease)
	if err != nil ||
		after.CompositeChildren[0].FrameRevision != terminalRevision {
		t.Fatalf(
			"terminal evidence revision drifted: before=%d after=%d error=%v",
			terminalRevision,
			after.CompositeChildren[0].FrameRevision,
			err,
		)
	}
}

func TestCompositeReviewerSchedulerWaitsForExactSpecialists(t *testing.T) {
	t.Run("all successful Specialists release only Reviewer", func(t *testing.T) {
		fixture := newCommittedCompositeReviewerRuntimeFixture(t)
		for index := range fixture.compiled.Children {
			finishCompositeReviewerSpecialist(t, fixture, index, false)
		}
		claim, err := fixture.store.ClaimFairRun(
			context.Background(),
			ClaimFairRunInput{
				TenantID: fixture.parentIntent.TenantID,
				OwnerID:  "reviewer-fair-worker",
				TTL:      time.Hour,
				Limits: FairSchedulerLimits{
					GlobalWorkers:         16,
					MaxActivePerWorkspace: 16,
					MaxActivePerFamily:    16,
				},
			},
		)
		if err != nil || claim.Status != FairSchedulerClaimed ||
			claim.Lease.RunID != fixture.compiled.Reviewer.RunManifest.RunID {
			t.Fatalf("Reviewer fair claim=%+v error=%v", claim, err)
		}
	})

	t.Run("UNKNOWN Specialist keeps Reviewer and Root closed", func(t *testing.T) {
		fixture := newCommittedCompositeReviewerRuntimeFixture(t)
		finishCompositeReviewerSpecialist(t, fixture, 0, true)
		finishCompositeReviewerSpecialist(t, fixture, 1, false)
		claim, err := fixture.store.ClaimFairRun(
			context.Background(),
			ClaimFairRunInput{
				TenantID: fixture.parentIntent.TenantID,
				OwnerID:  "reviewer-unknown-worker",
				TTL:      time.Hour,
				Limits: FairSchedulerLimits{
					GlobalWorkers:         16,
					MaxActivePerWorkspace: 16,
					MaxActivePerFamily:    16,
				},
			},
		)
		if err != nil || claim.Status != FairSchedulerNoRunnable {
			t.Fatalf("UNKNOWN Specialist claim=%+v error=%v", claim, err)
		}
	})
}

func TestCompositeReviewerApproveIsRequiredForRootMerge(t *testing.T) {
	fixture := newCommittedCompositeReviewerRuntimeFixture(t)
	for index := range fixture.compiled.Children {
		finishCompositeReviewerSpecialist(t, fixture, index, false)
	}
	reviewerBegin := beginCompositeReviewer(t, fixture)
	root := fixture.compiled.Parent.RunManifest
	rootLease := acquireCompositeTestLease(
		t,
		fixture.store,
		root.RunID,
		"root-before-reviewer-terminal",
	)
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:            rootLease,
			AttemptID:        "attempt-root-before-review",
			LogicalStepID:    corecontract.CompositeMergeLogicalStepIDV1,
			RequestCanonical: reviewerBegin.Attempt.Request.CanonicalBytes,
			Deadline: root.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	); !errors.Is(err, ErrModelDispatchConflict) {
		t.Fatalf("root merge while Reviewer PENDING error=%v", err)
	}

	verdictCanonical := compositeReviewerVerdictCanonical(
		t,
		fixture,
		reviewerBegin.Lease,
		corecontract.ReviewDecisionApproveV1,
	)
	outputCanonical := compositeReviewerOutputCanonical(t, verdictCanonical)
	_, usageCanonical := modelSuccessOutcomeCanonical(t)
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   reviewerBegin.Lease,
			AttemptID:               reviewerBegin.Attempt.AttemptID,
			InvocationID:            reviewerBegin.Attempt.AttemptID,
			Provider:                reviewerBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: reviewerBegin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
		},
	); err != nil {
		t.Fatalf("commit Reviewer APPROVE: %v", err)
	}
	rootRun, err := fixture.store.LoadRunForLoop(context.Background(), rootLease)
	if err != nil || rootRun.CompositeReviewer == nil ||
		rootRun.CompositeReviewer.State != CompositeReviewerSucceededV1 ||
		rootRun.CompositeReviewer.Verdict == nil ||
		rootRun.CompositeReviewer.Verdict.Decision !=
			corecontract.ReviewDecisionApproveV1 ||
		!bytes.Equal(rootRun.CompositeReviewer.VerdictCanonical, verdictCanonical) {
		t.Fatalf("root Reviewer projection=%+v error=%v", rootRun.CompositeReviewer, err)
	}
	compiled, err := recompileContextForNewAttempt(
		corecontract.ContextCompilationV1{},
		rootRun,
		rootRun.Frame.Revision,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("compile approved root merge: %v", err)
	}
	merge, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       rootLease,
			AttemptID:                   "attempt-root-approved-merge",
			LogicalStepID:               corecontract.CompositeMergeLogicalStepIDV1,
			ContextCompilationCanonical: compiled.CompilationCanonical,
			RequestCanonical:            compiled.RequestCanonical,
			Deadline: root.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if err != nil || !merge.Created || !merge.InvokeAllowed {
		t.Fatalf("approved root merge=%+v error=%v", merge, err)
	}
	var attempts int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*)
		FROM model_dispatch_attempts AS attempt
		JOIN runs AS family_run ON family_run.run_id=attempt.run_id
		WHERE family_run.run_id=? OR family_run.parent_run_id=?
	`, root.RunID, root.RunID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != int(root.Composite.Plan.FamilyModelDispatchLimit) ||
		attempts != len(fixture.compiled.Children)+2 {
		t.Fatalf(
			"family Attempts=%d limit=%d",
			attempts,
			root.Composite.Plan.FamilyModelDispatchLimit,
		)
	}
	usage, err := fixture.store.GetCompositeFamilyUsageProjection(
		context.Background(),
		root.RunID,
	)
	if err != nil || len(usage.Runs) != len(fixture.compiled.Children)+2 ||
		usage.Runs[len(usage.Runs)-2].Role !=
			corecontract.CompositeRunRoleReviewerV1 ||
		usage.Runs[len(usage.Runs)-1].Role !=
			corecontract.CompositeRunRoleRootV1 ||
		usage.Aggregate.AttemptSlotsUsed != uint32(attempts) {
		t.Fatalf("Reviewer family Usage=%+v error=%v", usage, err)
	}
}

func TestCompositeReviewRejectPersistsAttemptFreeRootFailure(t *testing.T) {
	fixture := newCommittedCompositeReviewerRuntimeFixture(t)
	for index := range fixture.compiled.Children {
		finishCompositeReviewerSpecialist(t, fixture, index, false)
	}
	reviewerBegin := beginCompositeReviewer(t, fixture)
	verdictCanonical := compositeReviewerVerdictCanonical(
		t,
		fixture,
		reviewerBegin.Lease,
		corecontract.ReviewDecisionRejectV1,
	)
	_, usageCanonical := modelSuccessOutcomeCanonical(t)
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   reviewerBegin.Lease,
			AttemptID:               reviewerBegin.Attempt.AttemptID,
			InvocationID:            reviewerBegin.Attempt.AttemptID,
			Provider:                reviewerBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: reviewerBegin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical: compositeReviewerOutputCanonical(
				t,
				verdictCanonical,
			),
			UsageReceiptCanonical: usageCanonical,
		},
	); err != nil {
		t.Fatal(err)
	}
	root := fixture.compiled.Parent.RunManifest
	rootLease := acquireCompositeTestLease(
		t,
		fixture.store,
		root.RunID,
		"root-review-reject",
	)
	failed, err := fixture.store.CommitCoreDeterministicFailure(
		context.Background(),
		CommitCoreDeterministicFailureInput{
			Lease:  rootLease,
			Reason: corecontract.CompositeReviewRejectedReasonV1,
		},
	)
	if err != nil || !failed.Applied {
		t.Fatalf("CommitCoreDeterministicFailure=%+v error=%v", failed, err)
	}
	terminal, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		root.RunID,
	)
	if err != nil || terminal.AttemptID != "" ||
		terminal.ReasonCode != corecontract.CompositeReviewRejectedReasonV1 {
		t.Fatalf("terminal review reject=%+v error=%v", terminal, err)
	}
}

func TestCompositeReviewerAttemptFreeFailurePropagatesToRoot(t *testing.T) {
	fixture := newCommittedCompositeReviewerRuntimeFixture(t)
	for index := range fixture.compiled.Children {
		finishCompositeReviewerSpecialist(t, fixture, index, false)
	}

	reviewer := fixture.compiled.Reviewer.RunManifest
	reviewerLease := acquireCompositeTestLease(
		t,
		fixture.store,
		reviewer.RunID,
		"reviewer-context-budget-failure",
	)
	reviewerFailure, err := fixture.store.CommitCoreDeterministicFailure(
		context.Background(),
		CommitCoreDeterministicFailureInput{
			Lease:  reviewerLease,
			Reason: corecontract.CompositeReviewFailedReasonV1,
		},
	)
	if err != nil || !reviewerFailure.Applied {
		t.Fatalf(
			"commit attempt-free Reviewer failure=%+v error=%v",
			reviewerFailure,
			err,
		)
	}
	reviewerTerminal, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		reviewer.RunID,
	)
	if err != nil || reviewerTerminal.AttemptID != "" ||
		reviewerTerminal.ReasonCode !=
			corecontract.CompositeReviewFailedReasonV1 {
		t.Fatalf(
			"terminal attempt-free Reviewer failure=%+v error=%v",
			reviewerTerminal,
			err,
		)
	}

	root := fixture.compiled.Parent.RunManifest
	rootLease := acquireCompositeTestLease(
		t,
		fixture.store,
		root.RunID,
		"root-reviewer-failure-reader",
	)
	rootRun, err := fixture.store.LoadRunForLoop(
		context.Background(),
		rootLease,
	)
	if err != nil || rootRun.CompositeReviewer == nil ||
		rootRun.CompositeReviewer.State != CompositeReviewerFailedV1 ||
		rootRun.CompositeReviewer.ErrorClassification !=
			corecontract.CompositeReviewFailedReasonV1 {
		t.Fatalf(
			"root attempt-free Reviewer projection=%+v error=%v",
			rootRun.CompositeReviewer,
			err,
		)
	}
	rootFailure, err := fixture.store.CommitCoreDeterministicFailure(
		context.Background(),
		CommitCoreDeterministicFailureInput{
			Lease:  rootLease,
			Reason: corecontract.CompositeReviewFailedReasonV1,
		},
	)
	if err != nil || !rootFailure.Applied {
		t.Fatalf(
			"commit propagated root review failure=%+v error=%v",
			rootFailure,
			err,
		)
	}
	rootTerminal, err := fixture.store.GetTerminalRunResult(
		context.Background(),
		root.RunID,
	)
	if err != nil || rootTerminal.AttemptID != "" ||
		rootTerminal.ReasonCode !=
			corecontract.CompositeReviewFailedReasonV1 {
		t.Fatalf(
			"terminal propagated root review failure=%+v error=%v",
			rootTerminal,
			err,
		)
	}
	var familyAttempts int
	if err := fixture.store.db.QueryRow(`
		SELECT COUNT(*)
		FROM model_dispatch_attempts AS attempt
		JOIN runs AS family_run ON family_run.run_id=attempt.run_id
		WHERE family_run.run_id=? OR family_run.parent_run_id=?
	`, root.RunID, root.RunID).Scan(&familyAttempts); err != nil {
		t.Fatal(err)
	}
	if familyAttempts != len(fixture.compiled.Children) {
		t.Fatalf(
			"attempt-free review failures created Attempts: got=%d want=%d",
			familyAttempts,
			len(fixture.compiled.Children),
		)
	}
}

func newCompositeReviewerRuntimeFixture(
	t *testing.T,
) *compositeAdmissionFixture {
	t.Helper()
	fixture := newCompositeAdmissionFixture(t)
	enableCompositeReviewerFixture(t, fixture)
	intentInput := fixture.parentIntent
	intentInput.Deadline = time.Now().UTC().Add(4 * time.Hour).
		Truncate(time.Microsecond)
	parentIntent, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(intentInput)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).CompileCompositeFamily(
		context.Background(),
		assemblycompiler.CompositeCompileInput{
			Parent: assemblycompiler.CompileInput{
				IntentCanonical:  intentCanonical,
				IntentDigest:     intentDigest,
				RunID:            "run-composite-reviewer-parent",
				MemberID:         "member-composite-reviewer-parent",
				RecoveryRootRef:  "recovery/run-composite-reviewer-parent",
				PublishedBasis:   fixture.basis,
				ControlCanonical: fixture.controlCanonical,
				CatalogCanonical: fixture.catalogCanonical,
			},
		},
	)
	if err != nil {
		t.Fatalf("CompileCompositeFamily Reviewer fixture: %v", err)
	}
	if compiled.Reviewer == nil {
		t.Fatal("compiled Reviewer is absent")
	}
	fixture.parentIntent = parentIntent
	fixture.compiled = compiled
	fixture.input = CommitCompositeRunFamilyInput{
		Parent: compositeCommitRunInput(
			fixture,
			compiled.Parent,
			intentCanonical,
			intentDigest,
		),
		Children: make([]CommitRunAdmissionInput, len(compiled.Children)),
	}
	for index, child := range compiled.Children {
		fixture.input.Children[index] = compositeCommitRunInput(
			fixture,
			child.CompileOutput,
			child.IntentCanonical,
			child.IntentDigest,
		)
	}
	reviewerInput := compositeCommitRunInput(
		fixture,
		compiled.Reviewer.CompileOutput,
		compiled.Reviewer.IntentCanonical,
		compiled.Reviewer.IntentDigest,
	)
	fixture.input.Reviewer = &reviewerInput
	return fixture
}

func newCommittedCompositeReviewerRuntimeFixture(
	t *testing.T,
) *compositeAdmissionFixture {
	t.Helper()
	fixture := newCompositeReviewerRuntimeFixture(t)
	if _, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatalf("CommitCompositeRunFamily Reviewer fixture: %v", err)
	}
	return fixture
}

func enableCompositeReviewerFixture(
	t *testing.T,
	fixture *compositeAdmissionFixture,
) {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.controlCanonical,
		fixture.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	reviewerAgent := corecontract.AgentRef{
		ID: "agent-reviewer-gate", Version: "v1", Digest: strings.Repeat("1", 64),
	}
	reviewerProfile := cloneCompositeTestProfile(control.Profiles[0])
	reviewerProfile.Profile = corecontract.ProfileRef{
		ID: "profile-reviewer-gate", Version: "v1", Digest: strings.Repeat("2", 64),
	}
	_, contextBody, err := corecontract.NewContextPolicyV1(
		corecontract.ContextPolicyV1{
			SchemaVersion:        corecontract.ContextPolicySchemaVersionV1,
			ContextWindowTokens:  10000,
			ReservedOutputTokens: 64,
			RecentHistoryTurns:   0,
			EstimatorVersion: corecontract.
				ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, contextRef, contextCanonical, err := corecontract.NewPolicyDocument(
		"context-policy-composite-reviewer",
		"v1",
		corecontract.PolicyContext,
		contextBody,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.PutContent(
		context.Background(),
		ContentInput{
			Digest:         contextRef.Digest,
			Kind:           ContentPolicy,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: contextCanonical,
		},
	); err != nil {
		t.Fatalf("put Reviewer ContextPolicy: %v", err)
	}
	for index := range control.Profiles {
		control.Profiles[index].ContextPolicy = contextRef
	}
	reviewerProfile.ContextPolicy = contextRef
	control.SnapshotID = "control-composite-reviewer"
	control.Revision++
	control.Agents = append(control.Agents, reviewerAgent)
	control.Profiles = append(control.Profiles, reviewerProfile)
	definition := &control.CompositeAgents[0]
	definition.Reviewer = &controlcontract.CompositeReviewerDefinitionV1{
		SchemaVersion:   controlcontract.CompositeReviewerSchemaVersionV1,
		AgentID:         reviewerAgent.ID,
		ProfileID:       reviewerProfile.Profile.ID,
		MaxOutputTokens: 64,
		Policy:          corecontract.CompositeReviewerPolicyResultsGateV1,
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze Reviewer Control: %v", err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		fixture.catalogCanonical,
		fixture.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-composite-reviewer"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze Reviewer Catalog: %v", err)
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
		t.Fatalf("publish Reviewer Control/Catalog: %v", err)
	}
	fixture.basis = basis
	fixture.controlCanonical = controlCanonical
	fixture.catalogCanonical = catalogCanonical
}

func finishCompositeReviewerSpecialist(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	index int,
	unknown bool,
) RunLease {
	t.Helper()
	beginInput := newCompositeChildBeginInput(
		t,
		fixture,
		index,
		"attempt-reviewer-specialist-"+string(rune('1'+index)),
		"reviewer-specialist-step-"+string(rune('1'+index)),
	)
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	if unknown {
		outcome, err := fixture.store.CommitModelDispatchOutcome(
			context.Background(),
			CommitModelDispatchOutcomeInput{
				Lease:                   begin.Lease,
				AttemptID:               begin.Attempt.AttemptID,
				InvocationID:            begin.Attempt.AttemptID,
				Provider:                begin.Attempt.Binding.Provider,
				ExpectedAttemptRevision: begin.Attempt.Revision,
				State:                   corecontract.ModelAttemptUnknown,
				ProviderRequestID:       "provider-reviewer-unknown",
				UnknownReason:           "ambiguous Specialist completion",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return outcome.Lease
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	outcome, err := fixture.store.CommitModelDispatchOutcome(
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
	)
	if err != nil {
		t.Fatal(err)
	}
	return outcome.Lease
}

func beginCompositeReviewer(
	t *testing.T,
	fixture *compositeAdmissionFixture,
) BeginModelDispatchResult {
	t.Helper()
	reviewer := fixture.compiled.Reviewer.RunManifest
	lease := acquireCompositeTestLease(
		t,
		fixture.store,
		reviewer.RunID,
		"composite-reviewer-worker",
	)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := recompileContextForNewAttempt(
		corecontract.ContextCompilationV1{},
		run,
		run.Frame.Revision,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("compile Reviewer request: %v", err)
	}
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "attempt-composite-reviewer",
			LogicalStepID:               corecontract.CompositeReviewLogicalStepIDV1,
			ContextCompilationCanonical: compiled.CompilationCanonical,
			RequestCanonical:            compiled.RequestCanonical,
			Deadline: reviewer.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("Begin Reviewer=%+v error=%v", begin, err)
	}
	return begin
}

func compositeReviewerVerdictCanonical(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	reviewerLease RunLease,
	decision corecontract.ReviewDecisionV1,
) []byte {
	t.Helper()
	run, err := fixture.store.LoadRunForLoop(
		context.Background(),
		reviewerLease,
	)
	if err != nil {
		t.Fatal(err)
	}
	if run.CompositeRoot == nil {
		t.Fatal("Reviewer has no frozen root projection")
	}
	_, _, digest, err := buildCompositeSpecialistResultSet(
		*run.CompositeRoot,
		run.CompositeChildren,
	)
	if err != nil {
		t.Fatal(err)
	}
	input := corecontract.ReviewVerdictV1{
		SchemaVersion:          corecontract.ReviewVerdictSchemaVersionV1,
		FamilyDigest:           run.CompositeRoot.ManifestDigest,
		SpecialistResultDigest: digest,
		Decision:               decision,
		BoundedReason:          "reviewed exact Specialist results",
	}
	if decision == corecontract.ReviewDecisionRejectV1 {
		input.IssueCodes = []corecontract.ReviewIssueCodeV1{
			corecontract.ReviewIssueIncompleteCoverageV1,
		}
		input.AffectedSlotIDs = []string{
			run.CompositeRoot.Composite.Plan.Children[0].SlotID,
		}
	}
	_, canonical, err := corecontract.NewReviewVerdictV1(input)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func compositeReviewerOutputCanonical(
	t *testing.T,
	verdictCanonical []byte,
) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: string(verdictCanonical),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
