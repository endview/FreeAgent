package currentstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestW5F1WorkspaceTransferResultsKeepPlanOrderAcrossReverseCompletionAndReopen(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	putCompositeModelPrice(t, fixture.store)

	// Complete the physical Specialists in the opposite order. Store consumers
	// must still project results by the frozen plan, never by completion time or
	// Workspace identity.
	for index := len(fixture.compiled.Children) - 1; index >= 0; index-- {
		finishDecisionSpecialist(
			t,
			fixture.compositeAdmissionFixture,
			fixture.compiled.Children[index],
			fmt.Sprintf("w5-f1-reverse-%d", index),
		)
	}

	first := loadW5F1WorkspaceTransferRoot(t, fixture.store, fixture)
	assertW5F1WorkspaceTransferPlanOrder(t, fixture, first)

	databasePath := fixture.store.Path()
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close W5-F1 source Store: %v", err)
	}
	reopened, err := OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("reopen W5-F1 Store: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened W5-F1 Store: %v", err)
		}
	})
	second := loadW5F1WorkspaceTransferRoot(t, reopened, fixture)
	assertW5F1WorkspaceTransferPlanOrder(t, fixture, second)

	if len(first.CompositeChildren) != len(second.CompositeChildren) {
		t.Fatalf(
			"reopen result count=%d want %d",
			len(second.CompositeChildren),
			len(first.CompositeChildren),
		)
	}
	for index := range first.CompositeChildren {
		before := first.CompositeChildren[index]
		after := second.CompositeChildren[index]
		if before.SlotID != after.SlotID || before.RunID != after.RunID ||
			before.AttemptID != after.AttemptID ||
			before.ResultRef != after.ResultRef ||
			before.ContributionDigest != after.ContributionDigest ||
			!bytes.Equal(before.OutputCanonical, after.OutputCanonical) ||
			!bytes.Equal(
				before.ContributionCanonical,
				after.ContributionCanonical,
			) {
			t.Fatalf(
				"result %d changed across reopen: before=%+v after=%+v",
				index,
				before,
				after,
			)
		}
	}
}

func TestW5F1FamilyCancellationCrossesWorkspaceBeforeAnyTransferEffect(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	root := fixture.compiled.Parent.RunManifest
	_, canonical := newFamilyCancellationRequest(
		t,
		root,
		corecontract.CancellationReasonUserRequestV1,
	)
	cancelled, err := fixture.store.RequestRunCancellation(
		context.Background(),
		RequestRunCancellationInput{Canonical: canonical},
	)
	if err != nil || !cancelled.Created || cancelled.Ref == "" {
		t.Fatalf("cancel cross-Workspace family=%+v error=%v", cancelled, err)
	}

	wantPhysical := 2*len(fixture.compiled.Children) + 3
	rows, err := fixture.store.db.Query(`
		SELECT run_id, workspace_id, cancel_request_ref
		FROM runs
		WHERE run_id=? OR parent_run_id=?
		ORDER BY run_id
	`, root.RunID, root.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	targetRuns := 0
	for rows.Next() {
		var runID, workspaceID, cancelRef string
		if err := rows.Scan(&runID, &workspaceID, &cancelRef); err != nil {
			t.Fatal(err)
		}
		count++
		if cancelRef != cancelled.Ref {
			t.Fatalf("Run %q cancel ref=%q want %q", runID, cancelRef, cancelled.Ref)
		}
		if workspaceID == fixture.TargetWorkspace.ID {
			targetRuns++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != wantPhysical || targetRuns != 2 {
		t.Fatalf(
			"canceled physical/target Runs=%d/%d want %d/2",
			count,
			targetRuns,
			wantPhysical,
		)
	}

	putCompositeModelPrice(t, fixture.store)
	beginInput := newCompositeChildBeginInput(
		t,
		fixture.compositeAdmissionFixture,
		0,
		"w5-f1-canceled-transfer-attempt",
		corecontract.PureChatModelLogicalStepIDV1,
	)
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	); !errors.Is(err, ErrRunCanceled) {
		t.Fatalf("canceled cross-Workspace Child Begin error=%v want ErrRunCanceled", err)
	}

	for _, check := range []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "model Attempts",
			query: `SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id IN (SELECT run_id FROM runs WHERE run_id=? OR parent_run_id=?)`,
			args:  []any{root.RunID, root.RunID},
		},
		{
			name:  "transfer payloads",
			query: `SELECT COUNT(*) FROM content_records WHERE kind=?`,
			args:  []any{string(ContentWorkspaceTransferPayload)},
		},
		{
			name:  "transfer envelopes",
			query: `SELECT COUNT(*) FROM content_records WHERE kind=?`,
			args:  []any{string(ContentWorkspaceTransferEnvelope)},
		},
	} {
		var got int
		if err := fixture.store.db.QueryRow(check.query, check.args...).Scan(&got); err != nil || got != 0 {
			t.Fatalf("canceled family %s=%d want 0 error=%v", check.name, got, err)
		}
	}

	retry, err := fixture.store.RequestRunCancellation(
		context.Background(),
		RequestRunCancellationInput{Canonical: canonical},
	)
	if err != nil || retry.Created || retry.Ref != cancelled.Ref {
		t.Fatalf("exact cancellation retry=%+v error=%v", retry, err)
	}
}

func TestW5F1OrdinaryCompositeKeepsTransferMaterialEmpty(t *testing.T) {
	fixture := newCompositeAdmissionFixture(t)
	created, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	)
	if err != nil || !created.Created {
		t.Fatalf("commit ordinary Composite=%+v error=%v", created, err)
	}
	if fixture.compiled.Parent.RunManifest.Composite == nil ||
		fixture.compiled.Parent.RunManifest.Composite.Plan == nil {
		t.Fatal("ordinary Composite plan is absent")
	}
	for _, child := range fixture.compiled.Parent.RunManifest.Composite.Plan.Children {
		if child.Transfer != nil {
			t.Fatalf("ordinary Composite Child %q gained Transfer", child.RunID)
		}
	}
	for _, child := range fixture.compiled.Children {
		lease := acquireCompositeTestLease(
			t,
			fixture.store,
			child.RunManifest.RunID,
			"w5-f1-legacy-"+child.Assignment.SlotID,
		)
		run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
		if err != nil {
			t.Fatal(err)
		}
		if run.WorkspaceTransfer != nil {
			t.Fatalf("ordinary Composite Child %q gained transfer material", run.RunID)
		}
		if err := fixture.store.ReleaseRunLease(context.Background(), lease); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []ContentKind{
		ContentWorkspaceTransferPayload,
		ContentWorkspaceTransferEnvelope,
	} {
		var count int
		if err := fixture.store.db.QueryRow(
			`SELECT COUNT(*) FROM content_records WHERE kind=?`,
			string(kind),
		).Scan(&count); err != nil || count != 0 {
			t.Fatalf("ordinary Composite %s count=%d error=%v", kind, count, err)
		}
	}
}

func loadW5F1WorkspaceTransferRoot(
	t *testing.T,
	store *Store,
	fixture *workspaceTransferStoreFixture,
) RunForLoop {
	t.Helper()
	lease := acquireCurrentDecisionLease(
		t,
		store,
		fixture.compiled.Parent.RunManifest.RunID,
		"w5-f1-root-order-reader",
	)
	run, err := store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatalf("load W5-F1 root: %v", err)
	}
	if err := store.ReleaseRunLease(context.Background(), lease); err != nil {
		t.Fatalf("release W5-F1 root: %v", err)
	}
	return run
}

func assertW5F1WorkspaceTransferPlanOrder(
	t *testing.T,
	fixture *workspaceTransferStoreFixture,
	run RunForLoop,
) {
	t.Helper()
	plan := fixture.compiled.Parent.RunManifest.Composite.Plan.Children
	if len(run.CompositeChildren) != len(plan) {
		t.Fatalf("root Child result count=%d want %d", len(run.CompositeChildren), len(plan))
	}
	for index, planned := range plan {
		result := run.CompositeChildren[index]
		if result.SlotID != planned.SlotID || result.RunID != planned.RunID ||
			result.State != CompositeChildSucceededV1 || result.AttemptID == "" ||
			result.ResultRef == "" || result.ContributionDigest == "" {
			t.Fatalf(
				"plan-ordered result %d=%+v want slot/run %s/%s",
				index,
				result,
				planned.SlotID,
				planned.RunID,
			)
		}
		if (planned.Transfer != nil) != (result.WorkspaceTransfer != nil) {
			t.Fatalf(
				"plan-ordered result %d transfer presence=%t want %t",
				index,
				result.WorkspaceTransfer != nil,
				planned.Transfer != nil,
			)
		}
		if result.WorkspaceTransfer != nil &&
			(result.WorkspaceTransfer.Envelope.Direction !=
				corecontract.WorkspaceTransferDirectionResultV1 ||
				result.WorkspaceTransfer.Envelope.PayloadRef != result.ResultRef) {
			t.Fatalf("plan-ordered result %d transfer=%+v", index, result.WorkspaceTransfer)
		}
	}
}
