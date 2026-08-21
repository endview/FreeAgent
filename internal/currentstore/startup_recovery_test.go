package currentstore

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestScanStartupRecoveryIsGlobalDeterministicAndReadOnly(t *testing.T) {
	t.Parallel()

	fixture, _, beginInput := newModelDispatchFixture(t)
	commitStartupRecoveryTestRun(
		t,
		fixture,
		"admission-ready",
		"run-a-ready",
	)
	commitStartupRecoveryTestRun(
		t,
		fixture,
		"admission-unknown",
		"run-z-unknown",
	)

	pending, err := fixture.store.BeginModelDispatch(
		context.Background(),
		beginInput,
	)
	if err != nil {
		t.Fatalf("begin PENDING dispatch: %v", err)
	}
	unknownLease, err := fixture.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 "run-z-unknown",
			OwnerID:               "startup-scan-unknown-owner",
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("acquire UNKNOWN Run: %v", err)
	}
	unknownInput := beginInput
	unknownInput.Lease = unknownLease
	unknownInput.AttemptID = "attempt-startup-unknown"
	unknownBegin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		unknownInput,
	)
	if err != nil {
		t.Fatalf("begin UNKNOWN dispatch: %v", err)
	}
	unknown, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   unknownBegin.Lease,
			AttemptID:               unknownBegin.Attempt.AttemptID,
			InvocationID:            unknownBegin.Attempt.AttemptID,
			Provider:                unknownBegin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: unknownBegin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			UnknownReason:           "test startup unknown",
		},
	)
	if err != nil {
		t.Fatalf("commit UNKNOWN dispatch: %v", err)
	}

	before := startupRecoveryFrameHeads(t, fixture.store)
	first, err := fixture.store.ScanStartupRecovery(context.Background())
	if err != nil {
		t.Fatalf("first startup recovery scan: %v", err)
	}
	second, err := fixture.store.ScanStartupRecovery(context.Background())
	if err != nil {
		t.Fatalf("second startup recovery scan: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("startup scan is not deterministic:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if after := startupRecoveryFrameHeads(t, fixture.store); !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only scan changed Frame heads:\nbefore=%v\nafter=%v", before, after)
	}

	want := []StartupRecoveryRun{
		{
			RunID:         "run-a-ready",
			FrameStep:     corecontract.InitialLoopStep,
			FrameRevision: 0,
		},
		{
			RunID:                 pending.Attempt.RunID,
			FrameStep:             corecontract.ModelPendingLoopStep,
			FrameRevision:         pending.Lease.FrameRevision,
			UnsettledAttemptID:    pending.Attempt.AttemptID,
			UnsettledAttemptState: corecontract.ModelAttemptPending,
		},
		{
			RunID:                 unknown.Record.Attempt.RunID,
			FrameStep:             corecontract.WaitingReconciliationLoopStep,
			FrameRevision:         unknown.Lease.FrameRevision,
			UnknownReason:         unknown.Record.Attempt.UnknownReason,
			UnsettledAttemptID:    unknown.Record.Attempt.AttemptID,
			UnsettledAttemptState: corecontract.ModelAttemptUnknown,
		},
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("startup recovery scan=%+v, want %+v", first, want)
	}
}

func TestScanStartupRecoveryValidatesContextAndStoreLifecycle(t *testing.T) {
	t.Parallel()

	fixture := newAdmissionCommitFixture(t)
	if _, err := fixture.store.ScanStartupRecovery(nil); err == nil {
		t.Fatal("nil context scan unexpectedly succeeded")
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close Store: %v", err)
	}
	if _, err := fixture.store.ScanStartupRecovery(context.Background()); err == nil {
		t.Fatal("closed Store scan unexpectedly succeeded")
	}
}

func TestScanStartupRecoveryAcceptsCompositeRootWaitingForChildren(t *testing.T) {
	t.Parallel()

	fixture := newCompositeAdmissionFixture(t)
	created, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("CommitCompositeRunFamily: %v", err)
	}
	before := startupRecoveryFrameHeads(t, fixture.store)
	runs, err := fixture.store.ScanStartupRecovery(context.Background())
	if err != nil {
		t.Fatalf("ScanStartupRecovery: %v", err)
	}
	if after := startupRecoveryFrameHeads(t, fixture.store); !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only scan changed composite Frame heads:\nbefore=%v\nafter=%v", before, after)
	}

	byID := make(map[string]StartupRecoveryRun, len(runs))
	for _, run := range runs {
		byID[run.RunID] = run
	}
	parent, ok := byID[created.Parent.RunID]
	if !ok {
		t.Fatalf("composite Parent %q absent from startup recovery", created.Parent.RunID)
	}
	if parent.FrameStep != corecontract.WaitingChildrenLoopStep ||
		parent.FrameRevision != 0 ||
		parent.UnsettledAttemptID != "" ||
		parent.UnsettledActionAttemptID != "" ||
		parent.UnsettledChannelAttemptID != "" {
		t.Fatalf("composite Parent recovery projection=%+v", parent)
	}
	for _, child := range created.Children {
		got, ok := byID[child.RunID]
		if !ok {
			t.Fatalf("composite Child %q absent from startup recovery", child.RunID)
		}
		if got.FrameStep != corecontract.InitialLoopStep ||
			got.FrameRevision != 0 ||
			got.UnsettledAttemptID != "" ||
			got.UnsettledActionAttemptID != "" ||
			got.UnsettledChannelAttemptID != "" {
			t.Fatalf("composite Child recovery projection=%+v", got)
		}
	}
}

func commitStartupRecoveryTestRun(
	t *testing.T,
	fixture *admissionCommitFixture,
	admissionKey string,
	runID string,
) {
	t.Helper()
	intent := fixture.intent
	intent.AdmissionKey = admissionKey
	_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatalf("build %s intent: %v", runID, err)
	}
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		fixture.compileInput(t, canonical, digest, runID),
	); err != nil {
		t.Fatalf("commit %s: %v", runID, err)
	}
}

func startupRecoveryFrameHeads(t *testing.T, store *Store) []string {
	t.Helper()
	rows, err := store.db.Query(`
		SELECT
			run_id,
			frame_revision,
			COALESCE(lease_owner, ''),
			lease_epoch,
			COALESCE(lease_expiry, 0)
		FROM loop_frames
		ORDER BY run_id
	`)
	if err != nil {
		t.Fatalf("query Frame heads: %v", err)
	}
	defer rows.Close()
	var heads []string
	for rows.Next() {
		var runID, owner string
		var revision, epoch, expiry int64
		if err := rows.Scan(&runID, &revision, &owner, &epoch, &expiry); err != nil {
			t.Fatalf("scan Frame head: %v", err)
		}
		heads = append(
			heads,
			fmt.Sprintf("%s:%d:%s:%d:%d", runID, revision, owner, epoch, expiry),
		)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate Frame heads: %v", err)
	}
	return heads
}
