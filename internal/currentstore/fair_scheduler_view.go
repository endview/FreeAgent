package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
)

// FairRunTargetView is a read-only, snapshot-consistent projection used by the
// Scheduler facade to observe a specific caller target that another worker may
// have advanced. It is not a queue and grants no execution authority.
type FairRunTargetView struct {
	RunID                    string
	TenantID                 string
	State                    string
	Disposition              string
	FrameRevision            uint64
	FrameStep                string
	WaitingReason            string
	LeaseActive              bool
	Cancellation             *corecontract.RunCancellationRequestV1
	IsComposite              bool
	IsChannel                bool
	CompositeChildCount      uint32
	CompositeChildrenPending bool
	CompositeChildUnknown    bool
}

// GetFairRunTargetView reads one target Run without acquiring a Run lease. It
// exists only so a Scheduler caller can return a terminal or stable waiting
// projection after another worker executed the target.
func (store *Store) GetFairRunTargetView(
	ctx context.Context,
	runID string,
) (FairRunTargetView, error) {
	if ctx == nil || !validLeaseOpaqueID(runID) {
		return FairRunTargetView{}, fmt.Errorf(
			"%w: invalid fair Scheduler target read",
			ErrInvalidFairScheduler,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return FairRunTargetView{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return FairRunTargetView{}, fmt.Errorf(
			"currentstore: fair Scheduler target connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return FairRunTargetView{}, fmt.Errorf(
			"currentstore: begin fair Scheduler target read: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	var view FairRunTargetView
	var runRevision int64
	var frameRevision int64
	var disposition sql.NullString
	var waitingReason sql.NullString
	var cancellationRef sql.NullString
	var continuationCanonical []byte
	var pendingAttempt sql.NullString
	var pendingDispatch sql.NullString
	var lastEvent int64
	var leaseOwner sql.NullString
	var leaseEpoch int64
	var leaseExpiry sql.NullInt64
	var compositeFlag int
	var channelFlag int
	err = connection.QueryRowContext(ctx, `
		SELECT
			r.run_id,
			r.tenant_id,
			r.state,
			r.disposition,
			r.revision,
			f.frame_revision,
			f.step,
			f.waiting_reason,
			f.continuation,
			f.pending_attempt_id,
			f.pending_dispatch_attempt_id,
			f.last_authoritative_event,
			f.lease_owner,
			f.lease_epoch,
			f.lease_expiry,
			r.cancel_request_ref,
			EXISTS(
			    SELECT 1 FROM runs AS child
			    WHERE child.parent_run_id=r.run_id
			) OR r.parent_run_id IS NOT NULL,
			EXISTS(
			    SELECT 1 FROM channel_ingress_receipts AS ingress
			    WHERE ingress.run_id=r.run_id
			)
		FROM runs AS r
		JOIN loop_frames AS f ON f.run_id=r.run_id
		WHERE r.run_id=?
	`, runID).Scan(
		&view.RunID,
		&view.TenantID,
		&view.State,
		&disposition,
		&runRevision,
		&frameRevision,
		&view.FrameStep,
		&waitingReason,
		&continuationCanonical,
		&pendingAttempt,
		&pendingDispatch,
		&lastEvent,
		&leaseOwner,
		&leaseEpoch,
		&leaseExpiry,
		&cancellationRef,
		&compositeFlag,
		&channelFlag,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return FairRunTargetView{}, fmt.Errorf(
			"%w: target Run %q is absent",
			ErrRunLeaseUnavailable,
			runID,
		)
	}
	if err != nil {
		return FairRunTargetView{}, fmt.Errorf(
			"currentstore: read fair Scheduler target: %w",
			err,
		)
	}
	if runRevision < 0 || frameRevision < 0 || lastEvent < 0 ||
		leaseEpoch < 0 || (compositeFlag != 0 && compositeFlag != 1) ||
		(channelFlag != 0 && channelFlag != 1) {
		return FairRunTargetView{}, fmt.Errorf(
			"%w: invalid target projection",
			ErrFairSchedulerIntegrity,
		)
	}
	view.FrameRevision = uint64(frameRevision)
	view.Disposition = disposition.String
	view.WaitingReason = waitingReason.String
	if leaseOwner.Valid != leaseExpiry.Valid ||
		(leaseOwner.Valid && (!validLeaseOpaqueID(leaseOwner.String) ||
			leaseEpoch <= 0 || leaseExpiry.Int64 <= 0)) {
		return FairRunTargetView{}, fmt.Errorf(
			"%w: partial target lease projection",
			ErrFairSchedulerIntegrity,
		)
	}
	view.LeaseActive = leaseOwner.Valid && leaseExpiry.Int64 > nowUnixMicro()
	view.IsComposite = compositeFlag == 1
	view.IsChannel = channelFlag == 1
	if err := validateLoopRunFrameProjection(
		view.State,
		view.Disposition,
		LoopFrameRecord{
			RunID:         view.RunID,
			Revision:      view.FrameRevision,
			Step:          view.FrameStep,
			WaitingReason: view.WaitingReason,
		},
	); err != nil {
		return FairRunTargetView{}, err
	}
	continuation, err := corecontract.RestoreLoopContinuationV1(
		continuationCanonical,
	)
	if err != nil || continuation.State != view.FrameStep {
		return FairRunTargetView{}, fmt.Errorf(
			"%w: invalid target continuation",
			ErrFairSchedulerIntegrity,
		)
	}
	if err := validateFairTargetContinuationPointers(
		continuation,
		pendingAttempt,
		pendingDispatch,
	); err != nil {
		return FairRunTargetView{}, err
	}
	if err := verifyRunEventHead(
		ctx,
		connection,
		view.RunID,
		uint64(lastEvent),
	); err != nil {
		return FairRunTargetView{}, err
	}
	if err := verifyFairTargetAttemptProjection(
		ctx,
		connection,
		view.RunID,
		continuation,
	); err != nil {
		return FairRunTargetView{}, err
	}
	if cancellationRef.Valid {
		var manifestCanonical []byte
		var manifestDigest string
		if err := connection.QueryRowContext(ctx, `
			SELECT canonical_json, digest
			FROM run_manifests
			WHERE run_id=?
		`, view.RunID).Scan(&manifestCanonical, &manifestDigest); err != nil {
			return FairRunTargetView{}, fmt.Errorf(
				"%w: cancellation manifest is absent",
				ErrFairSchedulerIntegrity,
			)
		}
		manifest, restoreErr := corecontract.RestoreRunManifest(manifestCanonical)
		if restoreErr != nil || manifest.ManifestDigest != manifestDigest {
			return FairRunTargetView{}, fmt.Errorf(
				"%w: cancellation manifest is invalid",
				ErrFairSchedulerIntegrity,
			)
		}
		record, queryErr := queryContent(ctx, connection, cancellationRef.String)
		if queryErr != nil || record.Kind != ContentRunCancellation ||
			record.MediaType != admissionJSONMediaType {
			return FairRunTargetView{}, fmt.Errorf(
				"%w: cancellation content is invalid",
				ErrFairSchedulerIntegrity,
			)
		}
		request, restoreErr := corecontract.RestoreRunCancellationRequestV1(
			record.CanonicalBytes,
		)
		if restoreErr != nil {
			return FairRunTargetView{}, fmt.Errorf(
				"%w: cancellation content: %v",
				ErrFairSchedulerIntegrity,
				restoreErr,
			)
		}
		if err := verifyRunCancellationContent(
			ctx,
			connection,
			cancellationRef.String,
			record.CanonicalBytes,
			request,
		); err != nil {
			return FairRunTargetView{}, fmt.Errorf(
				"%w: cancellation content closure: %v",
				ErrFairSchedulerIntegrity,
				err,
			)
		}
		if err := validateLoadedRunCancellation(
			ctx,
			connection,
			manifest,
			cancellationRef.String,
			request,
		); err != nil {
			return FairRunTargetView{}, fmt.Errorf(
				"%w: cancellation scope closure: %v",
				ErrFairSchedulerIntegrity,
				err,
			)
		}
		view.Cancellation = &request
	}

	if view.FrameStep == corecontract.WaitingChildrenLoopStep {
		var manifestCanonical []byte
		if err := connection.QueryRowContext(ctx, `
			SELECT canonical_json FROM run_manifests WHERE run_id=?
		`, view.RunID).Scan(&manifestCanonical); err != nil {
			return FairRunTargetView{}, fmt.Errorf(
				"%w: WAITING_CHILDREN Manifest is absent",
				ErrFairSchedulerIntegrity,
			)
		}
		manifest, restoreErr :=
			corecontract.RestoreRunManifest(manifestCanonical)
		if restoreErr != nil || manifest.Composite == nil {
			return FairRunTargetView{}, fmt.Errorf(
				"%w: WAITING_CHILDREN Manifest is invalid",
				ErrFairSchedulerIntegrity,
			)
		}
		decisionHandled, decisionErr := projectFairDecisionWaitingChildren(
			ctx,
			connection,
			manifest,
			&view,
		)
		if decisionErr != nil {
			return FairRunTargetView{}, decisionErr
		}
		if decisionHandled {
			if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
				return FairRunTargetView{}, fmt.Errorf(
					"currentstore: commit fair Scheduler target read: %w",
					err,
				)
			}
			committed = true
			return view, nil
		}
		familyRootRunID := view.RunID
		excludedSlotID := ""
		expectedCount := 0
		switch manifest.Composite.Role {
		case corecontract.CompositeRunRoleRootV1:
			if manifest.Composite.Plan == nil {
				return FairRunTargetView{}, fmt.Errorf(
					"%w: WAITING_CHILDREN root has no plan",
					ErrFairSchedulerIntegrity,
				)
			}
			expectedCount = compositeFamilyRunCount(manifest.Composite.Plan) - 1
		case corecontract.CompositeRunRoleReviewerV1:
			root, loadErr := loadCompositeRootManifest(
				ctx,
				connection,
				manifest.Composite.RootRunID,
			)
			if loadErr != nil || root.Composite == nil ||
				root.Composite.Plan == nil || root.Composite.Plan.Reviewer == nil ||
				root.Composite.Plan.Reviewer.RunID != manifest.RunID ||
				root.Composite.Plan.Reviewer.AdmissionKey != manifest.AdmissionKey ||
				root.Composite.Plan.Reviewer.MemberSnapshotDigest !=
					manifest.Members[0].Digest ||
				root.Composite.Plan.Reviewer.Agent != manifest.PrimaryAgent ||
				manifest.Composite.ParentManifestDigest != root.ManifestDigest {
				return FairRunTargetView{}, fmt.Errorf(
					"%w: WAITING_CHILDREN Reviewer does not close its root",
					ErrFairSchedulerIntegrity,
				)
			}
			familyRootRunID = root.RunID
			excludedSlotID = corecontract.CompositeReviewerParentSlotIDV1
			expectedCount = len(root.Composite.Plan.Children)
		default:
			return FairRunTargetView{}, fmt.Errorf(
				"%w: WAITING_CHILDREN role is not a coordinator",
				ErrFairSchedulerIntegrity,
			)
		}
		var childCount int64
		var pendingCount int64
		var unknownCount int64
		if err := connection.QueryRowContext(ctx, `
			SELECT
				COUNT(*),
				COALESCE(SUM(CASE
				    WHEN child_frame.step NOT IN (?, ?) THEN 1 ELSE 0
				END), 0),
				COALESCE(SUM(CASE
				    WHEN child_frame.step=? THEN 1 ELSE 0
				END), 0)
			FROM runs AS child
			JOIN loop_frames AS child_frame ON child_frame.run_id=child.run_id
			WHERE child.parent_run_id=?
			  AND (?='' OR child.parent_slot_id<>?)
		`,
			corecontract.TerminatedLoopStep,
			corecontract.WaitingReconciliationLoopStep,
			corecontract.WaitingReconciliationLoopStep,
			familyRootRunID,
			excludedSlotID,
			excludedSlotID,
		).Scan(&childCount, &pendingCount, &unknownCount); err != nil {
			return FairRunTargetView{}, fmt.Errorf(
				"currentstore: read Scheduler target Children: %w",
				err,
			)
		}
		if childCount <= 0 || childCount != int64(expectedCount) ||
			childCount > int64(^uint32(0)) ||
			pendingCount < 0 || unknownCount < 0 ||
			pendingCount+unknownCount > childCount {
			return FairRunTargetView{}, fmt.Errorf(
				"%w: invalid Scheduler Child projection",
				ErrFairSchedulerIntegrity,
			)
		}
		view.CompositeChildCount = uint32(childCount)
		view.CompositeChildrenPending = pendingCount != 0
		view.CompositeChildUnknown = unknownCount != 0
	}

	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return FairRunTargetView{}, fmt.Errorf(
			"currentstore: commit fair Scheduler target read: %w",
			err,
		)
	}
	committed = true
	return view, nil
}

func projectFairDecisionWaitingChildren(
	ctx context.Context,
	connection *sql.Conn,
	manifest corecontract.RunManifest,
	view *FairRunTargetView,
) (bool, error) {
	if view == nil || manifest.Composite == nil || manifest.RunID != view.RunID {
		return false, fmt.Errorf(
			"%w: invalid Decision target projection",
			ErrFairSchedulerIntegrity,
		)
	}
	var root corecontract.RunManifest
	switch manifest.Composite.Role {
	case corecontract.CompositeRunRoleRootV1:
		if manifest.Composite.Plan == nil ||
			manifest.Composite.Plan.Decision == nil {
			return false, nil
		}
		root = manifest
	case corecontract.CompositeRunRoleReviewerV1:
		loaded, err := loadCompositeRootManifest(
			ctx,
			connection,
			manifest.Composite.RootRunID,
		)
		if err != nil {
			return false, fmt.Errorf(
				"%w: WAITING_CHILDREN Decision root is absent: %v",
				ErrFairSchedulerIntegrity,
				err,
			)
		}
		if loaded.Composite == nil || loaded.Composite.Plan == nil ||
			loaded.Composite.Plan.Decision == nil {
			return false, nil
		}
		if !compositeReviewerManifestInPlan(loaded, manifest) {
			return false, fmt.Errorf(
				"%w: WAITING_CHILDREN Decision Reviewer does not close its root",
				ErrFairSchedulerIntegrity,
			)
		}
		root = loaded
	default:
		return false, nil
	}
	frontier, err := loadCompositeDecisionFrontier(
		ctx,
		connection,
		root,
	)
	if err != nil {
		return false, fmt.Errorf(
			"%w: WAITING_CHILDREN Decision frontier: %v",
			ErrFairSchedulerIntegrity,
			err,
		)
	}
	childCount := len(root.Composite.Plan.Children)
	if childCount <= 0 || uint64(childCount) > uint64(^uint32(0)) {
		return false, fmt.Errorf(
			"%w: invalid Decision Child count",
			ErrFairSchedulerIntegrity,
		)
	}
	view.CompositeChildCount = uint32(childCount)
	if frontier.Stage == CompositeFrontierWaitingReconciliationV1 {
		view.CompositeChildUnknown = true
		return true, nil
	}
	for _, runnableRunID := range frontier.RunnableRunIDs {
		if runnableRunID == manifest.RunID {
			return true, nil
		}
	}
	view.CompositeChildrenPending = true
	return true, nil
}

func validateFairTargetContinuationPointers(
	continuation corecontract.LoopContinuationV1,
	pendingAttempt sql.NullString,
	pendingDispatch sql.NullString,
) error {
	invalid := func() error {
		return fmt.Errorf(
			"%w: target continuation/pending projection differs",
			ErrFairSchedulerIntegrity,
		)
	}
	if pendingAttempt.Valid && pendingDispatch.Valid {
		return invalid()
	}
	switch continuation.State {
	case corecontract.InitialLoopStep,
		corecontract.WaitingRepairActivationLoopStep,
		corecontract.WaitingChildrenLoopStep,
		corecontract.ModelReadyAfterActionLoopStep,
		corecontract.TerminatedLoopStep:
		if pendingAttempt.Valid || pendingDispatch.Valid {
			return invalid()
		}
	case corecontract.ModelPendingLoopStep:
		if !pendingAttempt.Valid || pendingDispatch.Valid ||
			pendingAttempt.String != continuation.AttemptID {
			return invalid()
		}
	case corecontract.ActionPendingLoopStep,
		corecontract.ChannelPendingLoopStep:
		if pendingAttempt.Valid || !pendingDispatch.Valid ||
			pendingDispatch.String != continuation.AttemptID {
			return invalid()
		}
	case corecontract.WaitingReconciliationLoopStep:
		if continuation.AttemptKind == corecontract.AttemptKindModel {
			if !pendingAttempt.Valid || pendingDispatch.Valid ||
				pendingAttempt.String != continuation.AttemptID {
				return invalid()
			}
		} else if pendingAttempt.Valid || pendingDispatch.Valid {
			return invalid()
		}
	default:
		return invalid()
	}
	return nil
}

func verifyFairTargetAttemptProjection(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	continuation corecontract.LoopContinuationV1,
) error {
	var table string
	var dispatchKind string
	var expectedState string
	switch continuation.State {
	case corecontract.ModelPendingLoopStep:
		table = "model_dispatch_attempts"
		expectedState = string(corecontract.ModelAttemptPending)
	case corecontract.ActionPendingLoopStep:
		table = "dispatch_attempts"
		dispatchKind = string(DispatchKindAction)
		expectedState = string(DispatchPending)
	case corecontract.ChannelPendingLoopStep:
		table = "dispatch_attempts"
		dispatchKind = string(DispatchKindChannelSend)
		expectedState = string(DispatchPending)
	case corecontract.WaitingReconciliationLoopStep:
		switch continuation.AttemptKind {
		case corecontract.AttemptKindModel:
			table = "model_dispatch_attempts"
			expectedState = string(corecontract.ModelAttemptUnknown)
		case corecontract.AttemptKindAction:
			table = "dispatch_attempts"
			dispatchKind = string(DispatchKindAction)
			expectedState = string(DispatchUnknown)
		case corecontract.AttemptKindChannel:
			table = "dispatch_attempts"
			dispatchKind = string(DispatchKindChannelSend)
			expectedState = string(DispatchUnknown)
		default:
			return fmt.Errorf(
				"%w: target reconciliation kind is invalid",
				ErrFairSchedulerIntegrity,
			)
		}
	default:
		return nil
	}
	var state string
	var logicalStepID string
	var queryErr error
	if table == "model_dispatch_attempts" {
		queryErr = queryer.QueryRowContext(ctx, `
			SELECT state, logical_step_id
			FROM model_dispatch_attempts
			WHERE attempt_id=? AND run_id=?
		`, continuation.AttemptID, runID).Scan(&state, &logicalStepID)
	} else {
		queryErr = queryer.QueryRowContext(ctx, `
			SELECT state, logical_step_id
			FROM dispatch_attempts
			WHERE attempt_id=? AND run_id=? AND dispatch_kind=?
		`, continuation.AttemptID, runID, dispatchKind).Scan(
			&state,
			&logicalStepID,
		)
	}
	if queryErr != nil || state != expectedState ||
		logicalStepID != continuation.LogicalStepID {
		return fmt.Errorf(
			"%w: target Attempt projection differs",
			ErrFairSchedulerIntegrity,
		)
	}
	return nil
}
