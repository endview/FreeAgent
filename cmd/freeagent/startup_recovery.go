package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
)

const (
	startupRecoveryMaxDuration   = 2 * time.Minute
	startupRecoveryReleaseWait   = 5 * time.Second
	startupRecoveryOwnerID       = "production-startup-recovery"
	startupRecoveryUnknownReason = "RECOVERED_PENDING_AFTER_CRASH"
)

func runProductionStartupRecovery(
	ctx context.Context,
	store *currentstore.Store,
) error {
	return runProductionRecoveryV1(ctx, store, true)
}

func runProductionRecoveryV1(
	ctx context.Context,
	store *currentstore.Store,
	detachLeaseRelease bool,
) error {
	if ctx == nil || store == nil {
		return errors.New("composition: startup recovery is not initialized")
	}
	before, err := store.ScanStartupRecovery(ctx)
	if err != nil {
		return fmt.Errorf("composition: scan startup recovery: %w", err)
	}
	for _, candidate := range before {
		if err := recoverProductionStartupCandidate(
			ctx,
			store,
			candidate,
			detachLeaseRelease,
		); err != nil {
			return err
		}
	}

	after, err := store.ScanStartupRecovery(ctx)
	if err != nil {
		return fmt.Errorf("composition: verify startup recovery: %w", err)
	}
	if err := verifyProductionStartupRecovery(before, after); err != nil {
		return err
	}
	return nil
}

// runProductionShutdownRecoveryWithinV1 consumes the coordinator's one
// deadline for scanning, recovery, verification, and lease release. It never
// detaches from cancellation and never creates a second timeout.
func runProductionShutdownRecoveryWithinV1(
	ctx context.Context,
	store *currentstore.Store,
) error {
	if ctx == nil || store == nil {
		return errors.New("composition: shutdown recovery is not initialized")
	}
	if err := runProductionRecoveryV1(ctx, store, false); err != nil {
		return fmt.Errorf("composition: shutdown recovery: %w", err)
	}
	return nil
}

// runProductionShutdownRecovery runs only after HTTP admission has closed and
// every admitted handler has drained. It deliberately detaches from the
// canceled lifecycle context, remains bounded, and performs the same
// Store-only PENDING-to-UNKNOWN transition used at startup. It never loads or
// calls an Adapter.
func runProductionShutdownRecovery(
	lifecycle context.Context,
	store *currentstore.Store,
) error {
	if lifecycle == nil || store == nil {
		return errors.New("composition: shutdown recovery is not initialized")
	}
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(lifecycle),
		startupRecoveryMaxDuration,
	)
	defer cancel()
	if err := runProductionStartupRecovery(ctx, store); err != nil {
		return fmt.Errorf("composition: shutdown recovery: %w", err)
	}
	return nil
}

func recoverProductionStartupCandidate(
	ctx context.Context,
	store *currentstore.Store,
	candidate currentstore.StartupRecoveryRun,
	detachLeaseRelease bool,
) (returnErr error) {
	lease, err := store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   candidate.RunID,
			OwnerID: startupRecoveryOwnerID,
			TTL:     startupRecoveryMaxDuration,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"composition: acquire startup lease for Run %q: %w",
			candidate.RunID,
			err,
		)
	}
	currentLease := lease
	defer func() {
		releaseCtx := ctx
		cancel := func() {}
		if detachLeaseRelease {
			releaseCtx, cancel = context.WithTimeout(
				context.WithoutCancel(ctx),
				startupRecoveryReleaseWait,
			)
		}
		defer cancel()
		returnErr = errors.Join(
			returnErr,
			store.ReleaseRunLease(releaseCtx, currentLease),
		)
	}()

	var (
		kind      corecontract.AttemptKindV1
		attemptID string
	)
	switch {
	case candidate.UnsettledChannelAttemptState ==
		currentstore.DispatchPending:
		kind = corecontract.AttemptKindChannel
		attemptID = candidate.UnsettledChannelAttemptID
	case candidate.UnsettledActionAttemptState ==
		currentstore.ActionDispatchPending:
		kind = corecontract.AttemptKindAction
		attemptID = candidate.UnsettledActionAttemptID
	case candidate.UnsettledAttemptState == corecontract.ModelAttemptPending:
		kind = corecontract.AttemptKindModel
		attemptID = candidate.UnsettledAttemptID
	case candidate.UnsettledActionAttemptState ==
		currentstore.ActionDispatchUnknown,
		candidate.UnsettledChannelAttemptState == currentstore.DispatchUnknown,
		candidate.UnsettledAttemptState == corecontract.ModelAttemptUnknown,
		candidate.UnsettledAttemptState == "" &&
			candidate.UnsettledActionAttemptState == "" &&
			candidate.UnsettledChannelAttemptState == "":
		// ScanStartupRecovery has already validated the minimal ledger and
		// continuation projection. Acquiring the lease is the only remaining
		// startup ownership check; UNKNOWN and stable Runs are not reopened.
		return nil
	default:
		return fmt.Errorf(
			"composition: Run %q has unsupported startup Model/Action/Channel states %q/%q/%q",
			candidate.RunID,
			candidate.UnsettledAttemptState,
			candidate.UnsettledActionAttemptState,
			candidate.UnsettledChannelAttemptState,
		)
	}
	recovered, err := store.RecoverStartupPending(
		ctx,
		currentstore.RecoverStartupPendingInput{
			Lease:         currentLease,
			AttemptKind:   kind,
			AttemptID:     attemptID,
			UnknownReason: startupRecoveryUnknownReason,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"composition: recover startup PENDING Run %q: %w",
			candidate.RunID,
			err,
		)
	}
	currentLease = recovered.Lease
	return nil
}

func verifyProductionStartupRecovery(
	before []currentstore.StartupRecoveryRun,
	after []currentstore.StartupRecoveryRun,
) error {
	if len(after) != len(before) {
		return errors.New(
			"composition: startup recovery changed the Run set",
		)
	}
	for index := range before {
		previous := before[index]
		current := after[index]
		if current.RunID != previous.RunID ||
			current.UnsettledAttemptID != previous.UnsettledAttemptID ||
			current.UnsettledActionAttemptID !=
				previous.UnsettledActionAttemptID ||
			current.UnsettledChannelAttemptID !=
				previous.UnsettledChannelAttemptID {
			return errors.New(
				"composition: startup recovery changed Run or Attempt identity",
			)
		}
		if current.UnsettledAttemptState == corecontract.ModelAttemptPending {
			return fmt.Errorf(
				"composition: startup recovery left Run %q PENDING",
				current.RunID,
			)
		}
		if current.UnsettledActionAttemptState ==
			currentstore.ActionDispatchPending {
			return fmt.Errorf(
				"composition: startup recovery left Action Run %q PENDING",
				current.RunID,
			)
		}
		if current.UnsettledChannelAttemptState == currentstore.DispatchPending {
			return fmt.Errorf(
				"composition: startup recovery left Channel Run %q PENDING",
				current.RunID,
			)
		}
		switch {
		case previous.UnsettledChannelAttemptState == currentstore.DispatchPending ||
			previous.UnsettledChannelAttemptState == currentstore.DispatchUnknown:
			if current.UnsettledChannelAttemptState != currentstore.DispatchUnknown ||
				current.UnsettledAttemptState != "" ||
				current.UnsettledActionAttemptState != "" ||
				current.FrameStep != corecontract.WaitingReconciliationLoopStep {
				return fmt.Errorf(
					"composition: unsettled Channel Run %q did not close to UNKNOWN waiting",
					current.RunID,
				)
			}
		case previous.UnsettledActionAttemptState ==
			currentstore.ActionDispatchPending ||
			previous.UnsettledActionAttemptState ==
				currentstore.ActionDispatchUnknown:
			if current.UnsettledActionAttemptState !=
				currentstore.ActionDispatchUnknown ||
				current.UnsettledAttemptState != "" ||
				current.UnsettledChannelAttemptState != "" ||
				current.FrameStep !=
					corecontract.WaitingReconciliationLoopStep {
				return fmt.Errorf(
					"composition: unsettled Action Run %q did not close to UNKNOWN waiting",
					current.RunID,
				)
			}
		case previous.UnsettledAttemptState == corecontract.ModelAttemptPending ||
			previous.UnsettledAttemptState == corecontract.ModelAttemptUnknown:
			if current.UnsettledAttemptState !=
				corecontract.ModelAttemptUnknown ||
				current.UnsettledActionAttemptState != "" ||
				current.UnsettledChannelAttemptState != "" ||
				current.FrameStep !=
					corecontract.WaitingReconciliationLoopStep {
				return fmt.Errorf(
					"composition: unsettled Run %q did not close to MODEL_UNKNOWN waiting",
					current.RunID,
				)
			}
		case previous.UnsettledAttemptState == "" &&
			previous.UnsettledActionAttemptState == "" &&
			previous.UnsettledChannelAttemptState == "":
			if current.UnsettledAttemptState != "" ||
				current.UnsettledActionAttemptState != "" ||
				current.UnsettledChannelAttemptState != "" ||
				current.FrameStep != previous.FrameStep {
				return fmt.Errorf(
					"composition: stable Run %q changed during startup recovery",
					current.RunID,
				)
			}
		default:
			return fmt.Errorf(
				"composition: unsupported pre-recovery Model/Action/Channel states %q/%q/%q",
				previous.UnsettledAttemptState,
				previous.UnsettledActionAttemptState,
				previous.UnsettledChannelAttemptState,
			)
		}
	}
	return nil
}
