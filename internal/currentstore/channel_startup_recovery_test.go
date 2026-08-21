package currentstore

import (
	"context"
	"errors"
	"testing"

	"github.com/endview/freeagent/internal/corecontract"
)

func TestStartupRecoveryClosesOriginalChannelPendingWithoutReplay(t *testing.T) {
	t.Parallel()

	harness := newChannelDispatchHarness(
		t,
		"run-channel-startup-recovery",
		"event-channel-startup-recovery",
	)
	begin := harness.mustBegin(t)

	before, err := harness.store.ScanStartupRecovery(context.Background())
	if err != nil {
		t.Fatalf("ScanStartupRecovery before recovery: %v", err)
	}
	if len(before) != 1 ||
		before[0].RunID != begin.Channel.Attempt.RunID ||
		before[0].UnsettledChannelAttemptID != begin.Channel.Attempt.AttemptID ||
		before[0].UnsettledChannelAttemptState != DispatchPending ||
		before[0].UnsettledAttemptID != "" ||
		before[0].UnsettledActionAttemptID != "" {
		t.Fatalf("pre-recovery projection=%+v", before)
	}

	recovered, err := harness.store.RecoverStartupPending(
		context.Background(),
		RecoverStartupPendingInput{
			Lease:         begin.Lease,
			AttemptKind:   corecontract.AttemptKindChannel,
			AttemptID:     begin.Channel.Attempt.AttemptID,
			UnknownReason: "STARTUP_CHANNEL_PENDING_UNKNOWN",
		},
	)
	if err != nil {
		t.Fatalf("RecoverStartupPending Channel: %v", err)
	}

	record, err := harness.store.GetChannelDispatchRecord(
		context.Background(),
		begin.Channel.Attempt.AttemptID,
	)
	if err != nil {
		t.Fatalf("GetChannelDispatchRecord: %v", err)
	}
	if record.Attempt.State != DispatchUnknown ||
		record.Attempt.UnknownReason != "STARTUP_CHANNEL_PENDING_UNKNOWN" ||
		record.Attempt.AttemptID != begin.Channel.Attempt.AttemptID {
		t.Fatalf("recovered Channel Attempt=%+v", record.Attempt)
	}

	run, err := harness.store.LoadRunForLoop(
		context.Background(),
		recovered.Lease,
	)
	if err != nil {
		t.Fatalf("LoadRunForLoop after Channel recovery: %v", err)
	}
	if run.Frame.Step != corecontract.WaitingReconciliationLoopStep ||
		run.Frame.PendingAttemptID != "" ||
		run.Frame.PendingDispatchAttemptID != "" ||
		run.Frame.WaitingReason != channelUnknownWaitingReason {
		t.Fatalf("post-recovery Frame=%+v", run.Frame)
	}

	if _, err := harness.store.RecoverStartupPending(
		context.Background(),
		RecoverStartupPendingInput{
			Lease:         recovered.Lease,
			AttemptKind:   corecontract.AttemptKindChannel,
			AttemptID:     begin.Channel.Attempt.AttemptID,
			UnknownReason: "STARTUP_CHANNEL_PENDING_UNKNOWN",
		},
	); !errors.Is(err, ErrStartupRecoveryIntegrity) {
		t.Fatalf("second recovery error=%v, want integrity rejection", err)
	}
}
