package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidLoopRead = errors.New("currentstore: invalid Loop read")
	ErrLoopIntegrity   = errors.New(
		"currentstore: Loop recovery integrity violation",
	)
)

// LoopFrameRecord is the persisted continuation owned by Current Store.
type LoopFrameRecord struct {
	RunID                    string
	Revision                 uint64
	Step                     string
	BudgetStateRef           string
	Continuation             []byte
	PendingAttemptID         string
	PendingDispatchAttemptID string
	WaitingReason            string
	LastAuthoritativeEvent   uint64
	Lease                    RunLease
}

// HistoryEntryRecord is one detached, content-verified History row.
type HistoryEntryRecord struct {
	Sequence        uint64
	MemberID        string
	Role            string
	Content         ContentRecord
	SourceAttemptID string
	CreatedAt       time.Time
}

// ConversationHistoryTurnRecord is a read-only recovery projection derived
// from one successful predecessor Run. It does not copy predecessor History
// rows into the current Run and is never a second History fact source.
type ConversationHistoryTurnRecord struct {
	TurnIndex        uint64
	SourceRunID      string
	UserContent      ContentRecord
	AssistantContent ContentRecord
	// SourceAttemptID is always the successful terminal model Attempt that
	// produced AssistantContent. SourceContextCompilationAttemptID identifies
	// the compilation-bearing Attempt when SourceContextCompilation is loaded;
	// the two differ only for a terminal Action chain (model-2 vs model-1).
	SourceAttemptID                   string
	SourceContextCompilationAttemptID string
	SourceContextCompilation          *ContentRecord
}

// WorkspaceTransferRecoveryMaterialV1 is a Host-only recovery closure for
// one cross-Workspace Specialist Run. It is rebuilt exclusively from the
// Admission-frozen root/Child family, both Members' historical
// ControlSnapshot, and immutable Store content. CoreLoop may hand it only to
// the trusted transfer compiler/Store boundary; it is never itself model
// context, and RootTaskInput must not be read through RunForLoop.Contents.
type WorkspaceTransferRecoveryMaterialV1 struct {
	RootManifest                  corecontract.RunManifest
	ChildManifest                 corecontract.RunManifest
	SlotID                        string
	RepairRound                   uint32
	Plan                          corecontract.WorkspaceTransferPlanV1
	RootGrant                     corecontract.WorkspaceTransferGrantV1
	RootGrantCanonical            []byte
	TargetGrant                   corecontract.WorkspaceTransferGrantV1
	TargetGrantCanonical          []byte
	RootTaskInput                 ContentRecord
	PreviousContributionSet       *corecontract.CollaborationContributionSetV1
	PreviousContributionSetDigest string
	RepairVerdict                 *corecontract.CollaborationReviewVerdictV1
	RepairVerdictRef              string
	RepairBasis                   *corecontract.WorkspaceTaskSummaryV1
	RepairBasisCanonical          []byte
}

// RunForLoop is the exact recovery closure needed by the S1 Universal Loop.
// Optional module categories remain represented only by Member.PortPlans and
// generic content records; this type never grows Role/Skill/RAG-specific
// fields.
type RunForLoop struct {
	RunID                  string
	State                  string
	Disposition            string
	RunRevision            uint64
	Manifest               corecontract.RunManifest
	ManifestCanonical      []byte
	Member                 corecontract.MemberExecutionSnapshot
	MemberCanonical        []byte
	Frame                  LoopFrameRecord
	Contents               []ContentRecord
	ModelDispatches        []ModelDispatchRecord
	ActionDispatches       []ActionDispatchRecord
	ChannelIngress         *ChannelIngressReceipt
	ChannelIngressEnvelope *ContentRecord
	ChannelDispatches      []ChannelDispatchRecord
	History                []HistoryEntryRecord
	ConversationHistory    []ConversationHistoryTurnRecord
	CompositeChildren      []CompositeChildResultRecordV1
	// CompositeRoot is present for a Reviewer and every Decision-enabled
	// Specialist. It exposes the immutable root plan but grants no permit;
	// legacy S3-B Children retain nil.
	CompositeRoot                    *corecontract.RunManifest
	CompositeReviewer                *CompositeReviewerResultRecordV1
	CompositeDecisionFrontier        *CompositeDecisionFrontierV1
	CompositeRepairRound             uint32
	CompositeContributionSet         *corecontract.CollaborationContributionSetV1
	CompositeContributionSetDigest   string
	CompositePreviousContributionSet *corecontract.CollaborationContributionSetV1
	CompositeRepairVerdict           *corecontract.CollaborationReviewVerdictV1
	CompositeRepairVerdictRef        string
	CompositeRepairVerdictResult     *CompositeReviewerResultRecordV1
	CompositeRepairBasis             *corecontract.WorkspaceTaskSummaryV1
	CompositeRepairBasisCanonical    []byte
	WorkspaceTransfer                *WorkspaceTransferRecoveryMaterialV1
	CancellationRef                  string
	CancellationRequest              *corecontract.RunCancellationRequestV1
}

// FindContent returns a defensive copy of one recovery content record.
func (run RunForLoop) FindContent(
	digest string,
) (ContentRecord, bool) {
	index := sort.Search(len(run.Contents), func(index int) bool {
		return run.Contents[index].Digest >= digest
	})
	if index == len(run.Contents) ||
		run.Contents[index].Digest != digest {
		return ContentRecord{}, false
	}
	return cloneContentRecord(run.Contents[index]), true
}

// LoadRunForLoop reads a coherent, frozen recovery closure for one exact,
// unexpired lease. It never consults control_current and never recompiles a
// published Run from current Agent/Profile/Workspace state.
func (store *Store) LoadRunForLoop(
	ctx context.Context,
	lease RunLease,
) (RunForLoop, error) {
	if ctx == nil {
		return RunForLoop{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidLoopRead,
		)
	}
	if err := validateRunLeaseToken(lease); err != nil {
		return RunForLoop{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidLoopRead,
			err,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return RunForLoop{}, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return RunForLoop{}, fmt.Errorf(
			"currentstore: acquire Loop read connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return RunForLoop{}, fmt.Errorf(
			"currentstore: begin LoadRunForLoop: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	run, err := loadRunForLoop(ctx, connection, lease)
	if err != nil {
		return RunForLoop{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return RunForLoop{}, fmt.Errorf(
			"currentstore: commit LoadRunForLoop: %w",
			err,
		)
	}
	committed = true
	return cloneRunForLoop(run), nil
}

func loadRunForLoop(
	ctx context.Context,
	connection *sql.Conn,
	lease RunLease,
) (RunForLoop, error) {
	return loadRunClosureV1(ctx, connection, lease.RunID, &lease)
}

// loadRunForObservationV1 restores the same authoritative Run closure as
// Loop recovery from an existing read transaction, but deliberately grants no
// lease and therefore never treats a lease token as observation authority.
func loadRunForObservationV1(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
) (RunForLoop, error) {
	return loadRunClosureV1(ctx, connection, runID, nil)
}

func loadRunClosureV1(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	lease *RunLease,
) (RunForLoop, error) {
	var (
		tenantID     string
		workspaceID  string
		admissionKey string
		intentDigest string
		state        string
		disposition  sql.NullString
		runRevision  int64
		cancelRef    sql.NullString
	)
	err := connection.QueryRowContext(ctx, `
		SELECT
			tenant_id,
			workspace_id,
			admission_key,
			admission_intent_digest,
			state,
			disposition,
			revision,
			cancel_request_ref
		FROM runs
		WHERE run_id=?
	`, runID).Scan(
		&tenantID,
		&workspaceID,
		&admissionKey,
		&intentDigest,
		&state,
		&disposition,
		&runRevision,
		&cancelRef,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RunForLoop{}, fmt.Errorf(
			"%w: Run %q does not exist",
			ErrRunLeaseUnavailable,
			runID,
		)
	}
	if err != nil {
		return RunForLoop{}, fmt.Errorf(
			"currentstore: load Loop Run: %w",
			err,
		)
	}
	if runRevision < 0 ||
		(lease != nil && uint64(runRevision) != lease.RunRevision) {
		return RunForLoop{}, fmt.Errorf(
			"%w: Run revision changed before Loop recovery",
			ErrRunLeaseConflict,
		)
	}

	if _, err := loadAdmissionClosure(
		ctx,
		connection,
		runID,
		tenantID,
		admissionKey,
		intentDigest,
		workspaceID,
	); err != nil {
		return RunForLoop{}, fmt.Errorf(
			"%w: %v",
			ErrLoopIntegrity,
			err,
		)
	}

	var (
		manifestCanonical []byte
		manifestDigest    string
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM run_manifests
		WHERE run_id=?
	`, runID).Scan(
		&manifestCanonical,
		&manifestDigest,
	); err != nil {
		return RunForLoop{}, loopReadIntegrity("manifest", err)
	}
	manifest, err :=
		corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil || manifest.ManifestDigest != manifestDigest {
		return RunForLoop{}, loopReadIntegrity(
			"manifest",
			ErrAdmissionIntegrity,
		)
	}
	var cancellationRequest *corecontract.RunCancellationRequestV1
	if cancelRef.Valid {
		record, queryErr := queryContent(ctx, connection, cancelRef.String)
		if queryErr != nil || record.Kind != ContentRunCancellation ||
			record.MediaType != admissionJSONMediaType {
			return RunForLoop{}, loopReadIntegrity(
				"Run cancellation content",
				queryErr,
			)
		}
		request, restoreErr :=
			corecontract.RestoreRunCancellationRequestV1(
				record.CanonicalBytes,
			)
		if restoreErr != nil {
			return RunForLoop{}, loopReadIntegrity(
				"Run cancellation wire",
				restoreErr,
			)
		}
		if err := verifyRunCancellationContent(
			ctx,
			connection,
			cancelRef.String,
			record.CanonicalBytes,
			request,
		); err != nil {
			return RunForLoop{}, loopReadIntegrity(
				"Run cancellation content",
				err,
			)
		}
		if err := validateLoadedRunCancellation(
			ctx,
			connection,
			manifest,
			cancelRef.String,
			request,
		); err != nil {
			return RunForLoop{}, loopReadIntegrity(
				"Run cancellation closure",
				err,
			)
		}
		cancellationRequest = &request
	}

	memberID := manifest.PrimaryMemberID
	var (
		memberCanonical []byte
		memberDigest    string
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM member_execution_snapshots
		WHERE run_id=? AND member_id=?
	`, runID, memberID).Scan(
		&memberCanonical,
		&memberDigest,
	); err != nil {
		return RunForLoop{}, loopReadIntegrity("member snapshot", err)
	}
	member, err :=
		corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil ||
		member.MemberSnapshotDigest != memberDigest ||
		manifest.ValidateAgainstMember(member) != nil {
		return RunForLoop{}, loopReadIntegrity(
			"member snapshot",
			ErrAdmissionIntegrity,
		)
	}

	var frame LoopFrameRecord
	if lease != nil {
		frame, err = loadLoopFrame(ctx, connection, *lease)
	} else {
		frame, err = loadLoopFrameForObservationV1(ctx, connection, runID)
	}
	if err != nil {
		return RunForLoop{}, err
	}
	if err := verifyRunEventHead(
		ctx,
		connection,
		runID,
		frame.LastAuthoritativeEvent,
	); err != nil {
		return RunForLoop{}, err
	}
	if frame.Step == corecontract.WaitingRepairActivationLoopStep {
		expectedBudget, expectedContinuation, stateErr :=
			corecontract.NewWaitingRepairActivationLoopState(runID)
		if stateErr != nil || manifest.Composite == nil ||
			manifest.Composite.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
			state != corecontract.InitialRunState ||
			disposition.String != "WAITING_EXTERNAL" || !disposition.Valid ||
			runRevision != 0 || cancelRef.Valid || frame.Revision != 0 ||
			frame.BudgetStateRef != expectedBudget ||
			!bytes.Equal(frame.Continuation, expectedContinuation) ||
			frame.PendingAttemptID != "" || frame.PendingDispatchAttemptID != "" ||
			frame.WaitingReason != compositeRepairDormantWaitingReason ||
			frame.LastAuthoritativeEvent != 0 || frame.Lease.OwnerID != "" {
			return RunForLoop{}, loopReadIntegrity(
				"dormant repair exact head", ErrAdmissionIntegrity,
			)
		}
		var modelAttempts, dispatchAttempts, usageRows, historyRows int64
		if err := connection.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
			(SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?),
			(SELECT COUNT(*) FROM model_usage WHERE run_id=?),
			(SELECT COUNT(*) FROM history_entries WHERE run_id=?)`,
			runID, runID, runID, runID,
		).Scan(&modelAttempts, &dispatchAttempts, &usageRows, &historyRows); err != nil ||
			modelAttempts != 0 || dispatchAttempts != 0 || usageRows != 0 || historyRows != 0 {
			return RunForLoop{}, loopReadIntegrity("dormant repair hidden facts", err)
		}
		contents, err := loadLoopRecoveryContents(
			ctx, connection, manifest, member,
		)
		if err != nil {
			return RunForLoop{}, err
		}
		profileRun := RunForLoop{
			RunID: runID, Manifest: manifest, Member: member, Contents: contents,
		}
		if err := validateMemberModelProfileClosure(
			member, runContentGetter(profileRun),
		); err != nil {
			return RunForLoop{}, loopReadIntegrity(
				"dormant frozen ModelProfile closure", err,
			)
		}
		return RunForLoop{
			RunID: runID, State: state, Disposition: disposition.String,
			RunRevision: uint64(runRevision), Manifest: manifest,
			ManifestCanonical: bytes.Clone(manifestCanonical), Member: member,
			MemberCanonical: bytes.Clone(memberCanonical), Frame: frame,
			Contents: contents,
		}, nil
	}
	contents, err := loadLoopRecoveryContents(
		ctx,
		connection,
		manifest,
		member,
	)
	if err != nil {
		return RunForLoop{}, err
	}
	profileRun := RunForLoop{
		RunID:    runID,
		Manifest: manifest,
		Member:   member,
		Contents: contents,
	}
	if err := validateMemberModelProfileClosure(
		member,
		runContentGetter(profileRun),
	); err != nil {
		return RunForLoop{}, loopReadIntegrity(
			"frozen ModelProfile closure",
			err,
		)
	}
	actionEnabled := memberHasActionPort(member)
	channelEnabled := memberHasChannelPort(member)
	if !actionEnabled && !channelEnabled && frame.PendingDispatchAttemptID != "" {
		return RunForLoop{}, loopReadIntegrity(
			"Pure Chat dispatch pending pointer",
			ErrAdmissionIntegrity,
		)
	}
	if !actionEnabled || !channelEnabled {
		var hiddenAction, hiddenChannel int64
		if err := connection.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM dispatch_attempts
			 WHERE run_id=? AND dispatch_kind='ACTION'),
			(SELECT COUNT(*) FROM dispatch_attempts
			 WHERE run_id=? AND dispatch_kind='CHANNEL_SEND')`,
			runID, runID,
		).Scan(&hiddenAction, &hiddenChannel); err != nil {
			return RunForLoop{}, loopReadIntegrity(
				"disabled dispatch family cardinality", err,
			)
		}
		if (!actionEnabled && hiddenAction != 0) ||
			(!channelEnabled && hiddenChannel != 0) {
			return RunForLoop{}, loopReadIntegrity(
				"disabled dispatch family hidden Attempt",
				ErrAdmissionIntegrity,
			)
		}
	}
	dispatches, err := loadLoopModelDispatches(
		ctx,
		connection,
		manifest,
		member,
		frame,
		contents,
		!actionEnabled && !channelEnabled,
	)
	if err != nil {
		return RunForLoop{}, err
	}
	actionDispatches := []ActionDispatchRecord(nil)
	if actionEnabled {
		actionDispatches, err = loadLoopActionDispatches(
			ctx,
			connection,
			member,
			frame,
			dispatches,
		)
		if err != nil {
			return RunForLoop{}, err
		}
	}
	var channelIngress *ChannelIngressReceipt
	var channelEnvelope *ContentRecord
	channelDispatches := []ChannelDispatchRecord(nil)
	if channelEnabled {
		channelIngress, channelEnvelope, err = loadLoopChannelIngress(
			ctx,
			connection,
			manifest,
			member,
		)
		if err != nil {
			return RunForLoop{}, err
		}
		channelDispatches, err = loadLoopChannelDispatches(
			ctx,
			connection,
			member,
			frame,
			dispatches,
			*channelIngress,
		)
		if err != nil {
			return RunForLoop{}, err
		}
	}
	if actionEnabled || channelEnabled {
		if err := verifyLoopAttemptProjection(
			frame,
			dispatches,
			actionDispatches,
			channelDispatches,
		); err != nil {
			return RunForLoop{}, err
		}
	}
	if err := verifyLoopAttemptEventClosuresV1(
		ctx, connection, frame, dispatches, actionDispatches, channelDispatches,
	); err != nil {
		return RunForLoop{}, err
	}
	for _, dispatch := range dispatches {
		if dispatch.Attempt.TenantID != manifest.TenantID ||
			dispatch.Attempt.WorkspaceID != manifest.Workspace.ID {
			return RunForLoop{}, loopReadIntegrity(
				"Model Attempt scope projection", ErrAdmissionIntegrity,
			)
		}
	}
	for _, dispatch := range actionDispatches {
		if dispatch.Attempt.TenantID != manifest.TenantID ||
			dispatch.Attempt.WorkspaceID != manifest.Workspace.ID {
			return RunForLoop{}, loopReadIntegrity(
				"Action Attempt scope projection", ErrAdmissionIntegrity,
			)
		}
	}
	for _, dispatch := range channelDispatches {
		if dispatch.Attempt.TenantID != manifest.TenantID ||
			dispatch.Attempt.WorkspaceID != manifest.Workspace.ID {
			return RunForLoop{}, loopReadIntegrity(
				"Channel Attempt scope projection", ErrAdmissionIntegrity,
			)
		}
	}
	contents, err = loadLoopMemoryEvidenceContents(
		ctx,
		connection,
		contents,
		dispatches,
	)
	if err != nil {
		return RunForLoop{}, err
	}
	history, err := loadLoopHistory(
		ctx,
		connection,
		runID,
		member.MemberID,
		dispatches,
	)
	if err != nil {
		return RunForLoop{}, err
	}
	conversationHistory, err := loadLoopConversationHistory(
		ctx,
		connection,
		manifest,
		member,
	)
	if err != nil {
		return RunForLoop{}, err
	}
	compositeChildren := []CompositeChildResultRecordV1(nil)
	var compositeReviewer *CompositeReviewerResultRecordV1
	var compositeRoot *corecontract.RunManifest
	var participantRoot *corecontract.RunManifest
	var compositeFrontier *CompositeDecisionFrontierV1
	var compositeRound uint32
	var compositeSet *corecontract.CollaborationContributionSetV1
	var compositeSetDigest string
	var previousSet *corecontract.CollaborationContributionSetV1
	var previousSetDigest string
	var repairVerdict *corecontract.CollaborationReviewVerdictV1
	var repairVerdictRef string
	var repairVerdictResult *CompositeReviewerResultRecordV1
	var repairBasis *corecontract.WorkspaceTaskSummaryV1
	var repairBasisCanonical []byte
	if manifest.Composite != nil {
		switch manifest.Composite.Role {
		case corecontract.CompositeRunRoleRootV1:
			root := manifest
			compositeRoot = &root
		case corecontract.CompositeRunRoleChildV1:
			root, loadErr := loadCompositeRootManifest(
				ctx,
				connection,
				manifest.Composite.RootRunID,
			)
			if loadErr != nil || validateCompositeChildRunAgainstRoot(
				root,
				manifest,
				member,
			) != nil {
				return RunForLoop{}, loopReadIntegrity(
					"composite Child root closure",
					loadErr,
				)
			}
			if root.Composite.Plan.Decision != nil {
				participantRoot = &root
			}
		case corecontract.CompositeRunRoleReviewerV1:
			root, loadErr := loadCompositeRootManifest(
				ctx,
				connection,
				manifest.Composite.RootRunID,
			)
			if loadErr != nil ||
				manifest.Composite.ParentManifestDigest != root.ManifestDigest ||
				validateCompositeReviewerRunAgainstRoot(
					root,
					manifest,
					member,
				) != nil {
				return RunForLoop{}, loopReadIntegrity(
					"composite Reviewer root closure",
					loadErr,
				)
			}
			compositeRoot = &root
			participantRoot = &root
		}
	}
	if compositeRoot != nil {
		if compositeRoot.Composite.Plan.Decision != nil &&
			(manifest.Composite.Role == corecontract.CompositeRunRoleRootV1 ||
				manifest.Composite.RepairRound ==
					corecontract.CompositeRepairRoundOneV1) {
			compositeChildren, compositeRound, err =
				loadCompositeEffectiveChildResults(
					ctx,
					connection,
					*compositeRoot,
				)
		} else {
			compositeChildren, err = loadCompositeChildResults(
				ctx,
				connection,
				*compositeRoot,
			)
		}
		if err != nil {
			return RunForLoop{}, err
		}
		if manifest.Composite.Role == corecontract.CompositeRunRoleRootV1 {
			compositeReviewer, err = loadCompositeReviewerResultForRound(
				ctx,
				connection,
				manifest,
				compositeChildren,
				compositeRound,
			)
			if err != nil {
				return RunForLoop{}, err
			}
		}
		if compositeRoot.Composite.Plan.Decision != nil {
			loadedFrontier, loadErr := loadCompositeDecisionFrontier(
				ctx,
				connection,
				*compositeRoot,
			)
			if loadErr != nil {
				return RunForLoop{}, loadErr
			}
			compositeFrontier = &loadedFrontier
			if allCompositeChildrenSucceeded(compositeChildren) {
				set, digest, setErr := buildCollaborationContributionSet(
					ctx,
					connection,
					*compositeRoot,
					compositeChildren,
					compositeRound,
				)
				if setErr != nil {
					return RunForLoop{}, setErr
				}
				compositeSet = &set
				compositeSetDigest = digest
			}
			if compositeRound == corecontract.CompositeRepairRoundOneV1 {
				initial, loadErr := loadCompositeChildResults(
					ctx,
					connection,
					*compositeRoot,
				)
				if loadErr != nil {
					return RunForLoop{}, loadErr
				}
				set, digest, setErr := buildCollaborationContributionSet(
					ctx,
					connection,
					*compositeRoot,
					initial,
					0,
				)
				if setErr != nil {
					return RunForLoop{}, setErr
				}
				previousSet = &set
				previousSetDigest = digest
				reviewerZero, reviewerErr :=
					loadCompositeReviewerResultForRound(
						ctx,
						connection,
						*compositeRoot,
						initial,
						0,
					)
				if reviewerErr != nil || reviewerZero == nil ||
					reviewerZero.CollaborationVerdict == nil ||
					reviewerZero.ResultRef == "" {
					return RunForLoop{}, loopReadIntegrity(
						"composite repair lineage projection",
						reviewerErr,
					)
				}
				verdict := *reviewerZero.CollaborationVerdict
				repairVerdict = &verdict
				repairVerdictRef = reviewerZero.ResultRef
				repairVerdictResult = reviewerZero
			}
		}
	}
	if participantRoot != nil &&
		manifest.Composite.RepairRound ==
			corecontract.CompositeRepairRoundOneV1 &&
		repairVerdictResult == nil {
		initial, loadErr := loadCompositeChildResults(
			ctx,
			connection,
			*participantRoot,
		)
		if loadErr != nil {
			return RunForLoop{}, loadErr
		}
		set, digest, setErr := buildCollaborationContributionSet(
			ctx,
			connection,
			*participantRoot,
			initial,
			0,
		)
		if setErr != nil {
			return RunForLoop{}, setErr
		}
		previousSet = &set
		previousSetDigest = digest
		reviewerZero, reviewerErr := loadCompositeReviewerResultForRound(
			ctx,
			connection,
			*participantRoot,
			initial,
			0,
		)
		if reviewerErr != nil || reviewerZero == nil ||
			reviewerZero.CollaborationVerdict == nil ||
			reviewerZero.ResultRef == "" {
			return RunForLoop{}, loopReadIntegrity(
				"composite repair participant lineage",
				reviewerErr,
			)
		}
		verdict := *reviewerZero.CollaborationVerdict
		repairVerdict = &verdict
		repairVerdictRef = reviewerZero.ResultRef
		repairVerdictResult = reviewerZero
		compositeRound = corecontract.CompositeRepairRoundOneV1
	}
	if participantRoot != nil &&
		manifest.Composite.Role == corecontract.CompositeRunRoleChildV1 &&
		manifest.Composite.RepairRound ==
			corecontract.CompositeRepairRoundOneV1 &&
		compositeRepairVerdictAffectsSlotV1(
			repairVerdict, manifest.Composite.Assignment.SlotID,
		) {
		basis, canonical, basisErr := buildCompositeRepairTaskSummary(
			ctx,
			connection,
			*participantRoot,
			manifest.Composite.Assignment.SlotID,
		)
		if basisErr != nil {
			return RunForLoop{}, basisErr
		}
		repairBasis = &basis
		repairBasisCanonical = canonical
	}
	var workspaceTransfer *WorkspaceTransferRecoveryMaterialV1
	if participantRoot != nil && manifest.Composite != nil &&
		manifest.Composite.Role == corecontract.CompositeRunRoleChildV1 {
		planned, planErr := plannedCompositeChildForManifest(
			*participantRoot,
			manifest,
		)
		if planErr != nil {
			return RunForLoop{}, loopReadIntegrity(
				"Workspace transfer Child plan",
				planErr,
			)
		}
		// A repair-round Transfer is executable only for a slot selected by the
		// immutable REPAIR_REQUIRED verdict. Dormant or permanently skipped
		// repair children deliberately have no repair task basis/lineage and
		// must not be exposed as runnable Workspace-transfer recovery work.
		// Initial-round children always carry the ordinary transfer closure.
		if planned.Transfer != nil &&
			(manifest.Composite.RepairRound == 0 || repairBasis != nil) {
			rootClosure, loadErr := loadCompositeParticipantClosureV1(
				ctx,
				connection,
				participantRoot.RunID,
			)
			if loadErr != nil ||
				rootClosure.Manifest.ManifestDigest != participantRoot.ManifestDigest {
				return RunForLoop{}, loopReadIntegrity(
					"Workspace transfer root closure",
					loadErr,
				)
			}
			childClosure, loadErr := loadCompositeParticipantClosureV1(
				ctx,
				connection,
				manifest.RunID,
			)
			if loadErr != nil ||
				childClosure.Manifest.ManifestDigest != manifest.ManifestDigest ||
				childClosure.Member.MemberSnapshotDigest != member.MemberSnapshotDigest {
				return RunForLoop{}, loopReadIntegrity(
					"Workspace transfer Child closure",
					loadErr,
				)
			}
			workspaceTransfer, loadErr = loadWorkspaceTransferRecoveryMaterialV1(
				ctx,
				connection,
				rootClosure,
				childClosure,
				previousSet,
				previousSetDigest,
				repairVerdict,
				repairVerdictRef,
				repairBasis,
				repairBasisCanonical,
			)
			if loadErr != nil {
				return RunForLoop{}, loopReadIntegrity(
					"Workspace transfer recovery material",
					loadErr,
				)
			}
			if workspaceTransfer != nil {
				contents = omitLoopRecoveryContentV1(
					contents,
					manifest.TaskInputRef,
				)
			}
		}
	}
	if err := validateLoopRunFrameProjection(
		state,
		disposition.String,
		frame,
	); err != nil {
		return RunForLoop{}, err
	}

	run := RunForLoop{
		RunID:                            runID,
		State:                            state,
		Disposition:                      disposition.String,
		RunRevision:                      uint64(runRevision),
		Manifest:                         manifest,
		ManifestCanonical:                bytes.Clone(manifestCanonical),
		Member:                           member,
		MemberCanonical:                  bytes.Clone(memberCanonical),
		Frame:                            frame,
		Contents:                         contents,
		ModelDispatches:                  dispatches,
		ActionDispatches:                 actionDispatches,
		ChannelIngress:                   channelIngress,
		ChannelIngressEnvelope:           channelEnvelope,
		ChannelDispatches:                channelDispatches,
		History:                          history,
		ConversationHistory:              conversationHistory,
		CompositeChildren:                compositeChildren,
		CompositeRoot:                    participantRoot,
		CompositeReviewer:                compositeReviewer,
		CompositeDecisionFrontier:        cloneCompositeDecisionFrontier(compositeFrontier),
		CompositeRepairRound:             compositeRound,
		CompositeContributionSet:         compositeSet,
		CompositeContributionSetDigest:   compositeSetDigest,
		CompositePreviousContributionSet: previousSet,
		CompositeRepairVerdict:           repairVerdict,
		CompositeRepairVerdictRef:        repairVerdictRef,
		CompositeRepairVerdictResult:     repairVerdictResult,
		CompositeRepairBasis:             repairBasis,
		CompositeRepairBasisCanonical:    bytes.Clone(repairBasisCanonical),
		WorkspaceTransfer:                workspaceTransfer,
		CancellationRef:                  cancelRef.String,
		CancellationRequest:              cancellationRequest,
	}
	for _, dispatch := range run.ModelDispatches {
		if dispatch.Attempt.SourceDispatchAttemptID != "" {
			var source *ActionDispatchRecord
			for index := range run.ActionDispatches {
				if run.ActionDispatches[index].Attempt.AttemptID ==
					dispatch.Attempt.SourceDispatchAttemptID {
					source = &run.ActionDispatches[index]
					break
				}
			}
			if source == nil {
				return RunForLoop{}, loopReadIntegrity(
					"model-2 Action source",
					ErrAdmissionIntegrity,
				)
			}
			if err := validatePersistedSecondModelRequestClosure(
				ctx,
				connection,
				dispatch.Attempt,
				*source,
			); err != nil {
				return RunForLoop{}, err
			}
			continue
		}
		request, err := moduleapi.RestoreModelGenerateRequestV1(
			dispatch.Attempt.Request.CanonicalBytes,
		)
		if err != nil {
			return RunForLoop{}, loopReadIntegrity(
				"model Attempt request",
				err,
			)
		}
		if err := validateFrozenContextCompilationForRun(
			dispatch.Attempt.ContextCompilation,
			run,
			request,
			dispatch.Attempt.Request.Digest,
			dispatch.Attempt.FrameRevision,
		); err != nil {
			return RunForLoop{}, loopReadIntegrity(
				"model Attempt context compilation",
				err,
			)
		}
	}
	if err := validateLoopLegalActionRejection(
		ctx,
		connection,
		run,
	); err != nil {
		return RunForLoop{}, err
	}
	if frame.Step == corecontract.TerminatedLoopStep {
		terminal, err := loadTerminalRunResult(ctx, connection, runID)
		if err != nil || terminal.RunID != runID {
			return RunForLoop{}, loopReadIntegrity("terminal Run result", err)
		}
	}
	return run, nil
}

func verifyLoopAttemptEventClosuresV1(
	ctx context.Context,
	connection readQueryerV1,
	frame LoopFrameRecord,
	models []ModelDispatchRecord,
	actions []ActionDispatchRecord,
	channels []ChannelDispatchRecord,
) error {
	type eventRecordV1 struct {
		kind, payloadRef, payloadDigest, contentKind, mediaType string
		canonical                                               []byte
	}
	rows, err := connection.QueryContext(ctx, `SELECT e.event_kind,e.payload_ref,
		e.payload_digest,c.kind,c.media_type,c.canonical_bytes
		FROM run_events AS e JOIN content_records AS c
		ON c.content_digest=e.payload_ref WHERE e.run_id=? AND e.event_sequence>0
		ORDER BY e.event_sequence`, frame.RunID)
	if err != nil {
		return loopReadIntegrity("Attempt event rows", err)
	}
	events := make([]eventRecordV1, 0, frame.LastAuthoritativeEvent)
	for rows.Next() {
		var event eventRecordV1
		if err := rows.Scan(&event.kind, &event.payloadRef, &event.payloadDigest,
			&event.contentKind, &event.mediaType, &event.canonical); err != nil {
			_ = rows.Close()
			return loopReadIntegrity("Attempt event row", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return loopReadIntegrity("Attempt event rows", err)
	}
	if err := rows.Close(); err != nil {
		return loopReadIntegrity("Attempt event rows close", err)
	}
	modelByID := make(map[string]ModelDispatchAttemptRecord, len(models))
	actionByID := make(map[string]ActionDispatchAttemptRecord, len(actions))
	channelByID := make(map[string]ChannelDispatchAttemptRecord, len(channels))
	for _, record := range models {
		modelByID[record.Attempt.AttemptID] = record.Attempt
	}
	for _, record := range actions {
		actionByID[record.Attempt.AttemptID] = record.Attempt
	}
	for _, record := range channels {
		channelByID[record.Attempt.AttemptID] = record.Attempt
	}
	modelEvents := make(map[string][]corecontract.ModelAttemptState, len(models))
	actionEvents := make(map[string][]ActionDispatchState, len(actions))
	channelEvents := make(map[string][]DispatchState, len(channels))
	for _, stored := range events {
		computed, err := ComputeContentDigest(
			ContentRunEventPayload, admissionJSONMediaType, stored.canonical,
		)
		if err != nil || stored.payloadRef != stored.payloadDigest ||
			stored.payloadDigest != computed || stored.contentKind != string(ContentRunEventPayload) ||
			stored.mediaType != admissionJSONMediaType {
			return loopReadIntegrity("Attempt event content", err)
		}
		switch stored.kind {
		case corecontract.ModelDispatchPendingEventKind,
			corecontract.ModelDispatchTerminalEventKind:
			event, err := corecontract.RestoreModelDispatchEventV1(stored.canonical)
			attempt, found := modelByID[event.AttemptID]
			if err != nil || !found || event.RunID != attempt.RunID ||
				(stored.kind == corecontract.ModelDispatchPendingEventKind) !=
					(event.State == corecontract.ModelAttemptPending) ||
				event.LogicalStepID != attempt.LogicalStepID ||
				event.LogicalOperationKey != attempt.LogicalOperationKey ||
				event.RequestDigest != attempt.Request.Digest ||
				(event.State == corecontract.ModelAttemptSucceeded &&
					event.ResultDigest != attempt.ResultRef) {
				return loopReadIntegrity("Model Attempt event", err)
			}
			modelEvents[event.AttemptID] = append(modelEvents[event.AttemptID], event.State)
		case actionDispatchPendingEvent, actionDispatchTerminalEvent:
			var event actionDispatchEventV1
			if err := json.Unmarshal(stored.canonical, &event); err != nil {
				return loopReadIntegrity("Action Attempt event wire", err)
			}
			rebuilt, digest, err := prepareActionDispatchEvent(event)
			attempt, found := actionByID[event.AttemptID]
			if err != nil || !bytes.Equal(rebuilt, stored.canonical) || digest != stored.payloadDigest ||
				!found || event.RunID != attempt.RunID || event.LogicalStepID != attempt.LogicalStepID ||
				(stored.kind == actionDispatchPendingEvent) !=
					(event.State == ActionDispatchPending) ||
				event.LogicalOperationKey != attempt.LogicalOperationKey ||
				event.ProposalDigest != attempt.ProposalRef ||
				(event.State == ActionDispatchSucceeded && event.ResultDigest != attempt.ResultRef) {
				return loopReadIntegrity("Action Attempt event", err)
			}
			actionEvents[event.AttemptID] = append(actionEvents[event.AttemptID], event.State)
			if stored.kind == actionDispatchPendingEvent {
				source, sourceFound := modelByID[attempt.SourceModelAttemptID]
				if !sourceFound || source.State != corecontract.ModelAttemptSucceeded ||
					source.ResultRef == "" {
					return loopReadIntegrity("Action source Model event", ErrAdmissionIntegrity)
				}
				modelEvents[source.AttemptID] = append(
					modelEvents[source.AttemptID], corecontract.ModelAttemptSucceeded,
				)
			}
		case channelDispatchPendingEvent, channelDispatchTerminalEvent:
			var event channelDispatchEventV1
			if err := json.Unmarshal(stored.canonical, &event); err != nil {
				return loopReadIntegrity("Channel Attempt event wire", err)
			}
			rebuilt, digest, err := prepareChannelDispatchEvent(event)
			attempt, found := channelByID[event.AttemptID]
			if err != nil || !bytes.Equal(rebuilt, stored.canonical) || digest != stored.payloadDigest ||
				!found || event.RunID != attempt.RunID || event.LogicalStepID != attempt.LogicalStepID ||
				(stored.kind == channelDispatchPendingEvent) !=
					(event.State == DispatchPending) ||
				event.LogicalOperationKey != attempt.LogicalOperationKey ||
				event.ProposalDigest != attempt.ProposalRef ||
				(event.State == DispatchSucceeded && event.ResultDigest != attempt.ResultRef) {
				return loopReadIntegrity("Channel Attempt event", err)
			}
			channelEvents[event.AttemptID] = append(channelEvents[event.AttemptID], event.State)
			if stored.kind == channelDispatchPendingEvent {
				source, sourceFound := modelByID[attempt.SourceModelAttemptID]
				if !sourceFound || source.State != corecontract.ModelAttemptSucceeded ||
					source.ResultRef == "" {
					return loopReadIntegrity("Channel source Model event", ErrAdmissionIntegrity)
				}
				modelEvents[source.AttemptID] = append(
					modelEvents[source.AttemptID], corecontract.ModelAttemptSucceeded,
				)
			}
		case modelActionRejectionEventKind:
			var event modelActionRejectionEventV1
			if err := json.Unmarshal(stored.canonical, &event); err != nil {
				return loopReadIntegrity("legal Action rejection event wire", err)
			}
			rebuilt, digest, err := prepareModelActionRejectionEvent(event)
			attempt, found := modelByID[event.ModelAttemptID]
			if err != nil || !bytes.Equal(rebuilt, stored.canonical) ||
				digest != stored.payloadDigest || !found ||
				event.RunID != attempt.RunID ||
				event.LogicalStepID != attempt.LogicalStepID ||
				event.LogicalOperationKey != attempt.LogicalOperationKey ||
				event.ResultDigest != attempt.ResultRef ||
				attempt.State != corecontract.ModelAttemptSucceeded {
				return loopReadIntegrity("legal Action rejection Model event", err)
			}
			modelEvents[event.ModelAttemptID] = append(
				modelEvents[event.ModelAttemptID], corecontract.ModelAttemptSucceeded,
			)
		}
	}
	for _, record := range models {
		states := modelEvents[record.Attempt.AttemptID]
		if uint64(len(states)) != record.Attempt.Revision+1 || len(states) == 0 ||
			states[len(states)-1] != record.Attempt.State ||
			(len(states) > 1 && states[0] != corecontract.ModelAttemptPending) {
			return loopReadIntegrity("Model Attempt event cardinality", ErrAdmissionIntegrity)
		}
		for index := 1; index+1 < len(states); index++ {
			if states[index] != corecontract.ModelAttemptUnknown {
				return loopReadIntegrity("Model Attempt event transition", ErrAdmissionIntegrity)
			}
		}
	}
	for _, record := range actions {
		states := actionEvents[record.Attempt.AttemptID]
		if uint64(len(states)) != record.Attempt.Revision+1 || len(states) == 0 ||
			states[len(states)-1] != record.Attempt.State || states[0] != ActionDispatchPending {
			return loopReadIntegrity("Action Attempt event cardinality", ErrAdmissionIntegrity)
		}
		for index := 1; index+1 < len(states); index++ {
			if states[index] != ActionDispatchUnknown {
				return loopReadIntegrity("Action Attempt event transition", ErrAdmissionIntegrity)
			}
		}
	}
	for _, record := range channels {
		states := channelEvents[record.Attempt.AttemptID]
		if uint64(len(states)) != record.Attempt.Revision+1 || len(states) == 0 ||
			states[len(states)-1] != record.Attempt.State || states[0] != DispatchPending {
			return loopReadIntegrity("Channel Attempt event cardinality", ErrAdmissionIntegrity)
		}
		for index := 1; index+1 < len(states); index++ {
			if states[index] != DispatchUnknown {
				return loopReadIntegrity("Channel Attempt event transition", ErrAdmissionIntegrity)
			}
		}
	}
	return nil
}

func validateLoadedRunCancellation(
	ctx context.Context,
	connection readQueryerV1,
	manifest corecontract.RunManifest,
	cancelRef string,
	request corecontract.RunCancellationRequestV1,
) error {
	if manifest.Composite == nil {
		if manifest.CancellationScope != corecontract.CancellationScopeRunV1 ||
			request.Scope != corecontract.CancellationScopeRunV1 ||
			request.RootRunID != manifest.RunID ||
			request.RootManifestDigest != manifest.ManifestDigest {
			return ErrAdmissionIntegrity
		}
		return nil
	}

	var root corecontract.RunManifest
	switch manifest.Composite.Role {
	case corecontract.CompositeRunRoleRootV1:
		root = manifest
	case corecontract.CompositeRunRoleChildV1,
		corecontract.CompositeRunRoleReviewerV1:
		loaded, err := loadCompositeRootManifest(
			ctx,
			connection,
			manifest.Composite.RootRunID,
		)
		if err != nil ||
			manifest.Composite.ParentManifestDigest != loaded.ManifestDigest {
			return ErrAdmissionIntegrity
		}
		root = loaded
	default:
		return ErrAdmissionIntegrity
	}
	if request.Scope != corecontract.CancellationScopeFamilyV1 ||
		request.RootRunID != root.RunID ||
		request.RootManifestDigest != root.ManifestDigest {
		return ErrAdmissionIntegrity
	}
	members, err := loadCompositeFamilyLatchRows(ctx, connection, root)
	if err != nil {
		return err
	}
	for _, member := range members {
		if !member.cancelRef.Valid || member.cancelRef.String != cancelRef {
			return ErrAdmissionIntegrity
		}
	}
	return nil
}

func validateLoopLegalActionRejection(
	ctx context.Context,
	connection readQueryerV1,
	run RunForLoop,
) error {
	if run.Frame.Step != corecontract.TerminatedLoopStep {
		return nil
	}
	continuation, err := corecontract.RestoreLoopContinuationV1(
		run.Frame.Continuation,
	)
	if err != nil {
		return loopReadIntegrity("terminal continuation", err)
	}
	if continuation.AttemptKind != corecontract.AttemptKindModel {
		return nil
	}
	var model *ModelDispatchRecord
	for index := range run.ModelDispatches {
		if run.ModelDispatches[index].Attempt.AttemptID == continuation.AttemptID {
			model = &run.ModelDispatches[index]
			break
		}
	}
	if model == nil || model.Attempt.State != corecontract.ModelAttemptSucceeded ||
		model.Attempt.ResultRef == "" {
		return nil
	}
	content, err := queryContent(ctx, connection, model.Attempt.ResultRef)
	if err != nil || content.Kind != ContentModelResult {
		return loopReadIntegrity("terminal Model result", err)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(content.CanonicalBytes)
	if err != nil {
		return loopReadIntegrity("terminal Model result wire", err)
	}
	if output.ActionRequest == nil {
		return nil
	}
	event, err := loadModelActionRejectionEvent(
		ctx,
		connection,
		run.RunID,
		run.Frame.LastAuthoritativeEvent,
	)
	if err != nil || event.ModelAttemptID != model.Attempt.AttemptID ||
		event.LogicalStepID != model.Attempt.LogicalStepID ||
		event.LogicalOperationKey != model.Attempt.LogicalOperationKey ||
		event.ResultDigest != model.Attempt.ResultRef {
		return loopReadIntegrity("legal Action rejection event", err)
	}
	return nil
}

// loadLoopMemoryEvidenceContents follows only refs already frozen in an
// Attempt's ContextCompilation. It never reads MAX(revision), so recovery of a
// PENDING/UNKNOWN/terminal Attempt cannot drift when a newer Memory revision
// exists. The exact revision row and its semantic closure must still exist.
func loadLoopMemoryEvidenceContents(
	ctx context.Context,
	connection readQueryerV1,
	contents []ContentRecord,
	dispatches []ModelDispatchRecord,
) ([]ContentRecord, error) {
	additional := make([]ContentRecord, 0)
	seen := make(map[string]struct{})
	for _, dispatch := range dispatches {
		if dispatch.Attempt.ContextCompilation == nil {
			continue
		}
		compilation, err := corecontract.RestoreContextCompilationV1(
			dispatch.Attempt.ContextCompilation.CanonicalBytes,
		)
		if err != nil {
			return nil, loopReadIntegrity(
				"Memory evidence compilation",
				err,
			)
		}
		for _, evidence := range compilation.MemoryReads {
			if _, found := seen[evidence.Snapshot.Digest]; found {
				continue
			}
			revision, err := queryAgentMemoryRevision(
				ctx,
				connection,
				evidence.Snapshot.TenantID,
				evidence.Snapshot.AgentID,
				evidence.Snapshot.Revision,
			)
			if err != nil || revision.SnapshotRef != evidence.Snapshot {
				return nil, loopReadIntegrity(
					"exact Agent Memory evidence revision",
					err,
				)
			}
			content, err := queryContent(
				ctx,
				connection,
				evidence.Snapshot.Digest,
			)
			if err != nil || content.Kind != ContentMemorySnapshot ||
				content.MediaType != admissionJSONMediaType ||
				!bytes.Equal(
					content.CanonicalBytes,
					revision.CanonicalBytes,
				) {
				return nil, loopReadIntegrity(
					"Agent Memory evidence content",
					err,
				)
			}
			seen[evidence.Snapshot.Digest] = struct{}{}
			additional = append(additional, content)
		}
	}
	if len(additional) == 0 {
		return contents, nil
	}
	merged, err := runWithAdditionalContent(
		RunForLoop{Contents: contents},
		additional...,
	)
	if err != nil {
		return nil, loopReadIntegrity("Memory recovery contents", err)
	}
	return merged.Contents, nil
}

func loadLoopFrame(
	ctx context.Context,
	connection readQueryerV1,
	lease RunLease,
) (LoopFrameRecord, error) {
	var (
		frameRevision   int64
		step            string
		budgetRef       string
		continuation    []byte
		pending         sql.NullString
		pendingDispatch sql.NullString
		waiting         sql.NullString
		lastEvent       int64
		owner           sql.NullString
		epoch           int64
		expiry          sql.NullInt64
	)
	err := connection.QueryRowContext(ctx, `
		SELECT
			frame_revision,
			step,
			budget_state_ref,
			continuation,
			pending_attempt_id,
			pending_dispatch_attempt_id,
			waiting_reason,
			last_authoritative_event,
			lease_owner,
			lease_epoch,
			lease_expiry
		FROM loop_frames
		WHERE run_id=?
	`, lease.RunID).Scan(
		&frameRevision,
		&step,
		&budgetRef,
		&continuation,
		&pending,
		&pendingDispatch,
		&waiting,
		&lastEvent,
		&owner,
		&epoch,
		&expiry,
	)
	if err != nil {
		return LoopFrameRecord{}, loopReadIntegrity("LoopFrame", err)
	}
	if frameRevision < 0 ||
		lastEvent < 0 ||
		(pending.Valid && pendingDispatch.Valid) ||
		uint64(frameRevision) != lease.FrameRevision ||
		!owner.Valid ||
		owner.String != lease.OwnerID ||
		epoch <= 0 ||
		uint64(epoch) != lease.LeaseEpoch ||
		!expiry.Valid ||
		expiry.Int64 <= nowUnixMicro() {
		return LoopFrameRecord{}, fmt.Errorf(
			"%w: exact unexpired Loop lease is no longer current",
			ErrRunLeaseConflict,
		)
	}
	continuationValue, err :=
		corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil || continuationValue.State != step {
		return LoopFrameRecord{}, loopReadIntegrity(
			"Loop continuation",
			ErrAdmissionIntegrity,
		)
	}
	switch continuationValue.State {
	case corecontract.InitialLoopStep:
		if pending.Valid || pendingDispatch.Valid {
			return LoopFrameRecord{}, loopReadIntegrity(
				"READY pending attempt",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.WaitingChildrenLoopStep:
		if pending.Valid || pendingDispatch.Valid ||
			continuationValue.AttemptKind != "" {
			return LoopFrameRecord{}, loopReadIntegrity(
				"WAITING_CHILDREN pending attempt",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.ModelPendingLoopStep:
		if !pending.Valid || pendingDispatch.Valid ||
			pending.String != continuationValue.AttemptID {
			return LoopFrameRecord{}, loopReadIntegrity(
				"pending attempt",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.ActionPendingLoopStep:
		if pending.Valid || !pendingDispatch.Valid ||
			pendingDispatch.String != continuationValue.AttemptID {
			return LoopFrameRecord{}, loopReadIntegrity(
				"pending Action attempt",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.ChannelPendingLoopStep:
		if pending.Valid || !pendingDispatch.Valid ||
			pendingDispatch.String != continuationValue.AttemptID ||
			continuationValue.AttemptKind != corecontract.AttemptKindChannel {
			return LoopFrameRecord{}, loopReadIntegrity(
				"pending Channel attempt",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.WaitingReconciliationLoopStep:
		switch continuationValue.AttemptKind {
		case corecontract.AttemptKindModel:
			if !pending.Valid || pendingDispatch.Valid ||
				pending.String != continuationValue.AttemptID {
				return LoopFrameRecord{}, loopReadIntegrity(
					"reconciling model attempt",
					ErrAdmissionIntegrity,
				)
			}
		case corecontract.AttemptKindAction:
			if pending.Valid || pendingDispatch.Valid {
				return LoopFrameRecord{}, loopReadIntegrity(
					"reconciling Action attempt",
					ErrAdmissionIntegrity,
				)
			}
		case corecontract.AttemptKindChannel:
			if pending.Valid || pendingDispatch.Valid {
				return LoopFrameRecord{}, loopReadIntegrity(
					"reconciling Channel attempt",
					ErrAdmissionIntegrity,
				)
			}
		default:
			return LoopFrameRecord{}, loopReadIntegrity(
				"reconciliation AttemptKind",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.ModelReadyAfterActionLoopStep:
		if pending.Valid || pendingDispatch.Valid ||
			continuationValue.AttemptKind != corecontract.AttemptKindAction {
			return LoopFrameRecord{}, loopReadIntegrity(
				"MODEL_READY_AFTER_ACTION pending attempt",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.TerminatedLoopStep:
		if pending.Valid || pendingDispatch.Valid {
			return LoopFrameRecord{}, loopReadIntegrity(
				"terminated pending attempt",
				ErrAdmissionIntegrity,
			)
		}
	default:
		return LoopFrameRecord{}, loopReadIntegrity(
			"Loop continuation state",
			ErrAdmissionIntegrity,
		)
	}
	expiresAt, err := timeFromUnixMicro(expiry.Int64)
	if err != nil {
		return LoopFrameRecord{}, loopReadIntegrity("lease expiry", err)
	}
	return LoopFrameRecord{
		RunID:                    lease.RunID,
		Revision:                 uint64(frameRevision),
		Step:                     step,
		BudgetStateRef:           budgetRef,
		Continuation:             bytes.Clone(continuation),
		PendingAttemptID:         pending.String,
		PendingDispatchAttemptID: pendingDispatch.String,
		WaitingReason:            waiting.String,
		LastAuthoritativeEvent:   uint64(lastEvent),
		Lease: RunLease{
			RunID:         lease.RunID,
			OwnerID:       owner.String,
			LeaseEpoch:    uint64(epoch),
			RunRevision:   lease.RunRevision,
			FrameRevision: uint64(frameRevision),
			ExpiresAt:     expiresAt,
		},
	}, nil
}

func loadLoopFrameForObservationV1(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
) (LoopFrameRecord, error) {
	var (
		frameRevision   int64
		step            string
		budgetRef       string
		continuation    []byte
		pending         sql.NullString
		pendingDispatch sql.NullString
		waiting         sql.NullString
		lastEvent       int64
		owner           sql.NullString
		epoch           int64
		expiry          sql.NullInt64
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT frame_revision,step,budget_state_ref,continuation,
			pending_attempt_id,pending_dispatch_attempt_id,waiting_reason,
			last_authoritative_event,lease_owner,lease_epoch,lease_expiry
		FROM loop_frames WHERE run_id=?
	`, runID).Scan(&frameRevision, &step, &budgetRef, &continuation,
		&pending, &pendingDispatch, &waiting, &lastEvent, &owner, &epoch,
		&expiry); err != nil {
		return LoopFrameRecord{}, loopReadIntegrity("LoopFrame observation", err)
	}
	if frameRevision < 0 || lastEvent < 0 || epoch < 0 ||
		(pending.Valid && pendingDispatch.Valid) || owner.Valid != expiry.Valid ||
		(owner.Valid && (!validLeaseOpaqueID(owner.String) || epoch <= 0 ||
			expiry.Int64 <= 0)) {
		return LoopFrameRecord{}, loopReadIntegrity(
			"LoopFrame observation projection", ErrAdmissionIntegrity,
		)
	}
	continuationValue, err := corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil || continuationValue.State != step {
		return LoopFrameRecord{}, loopReadIntegrity("Loop continuation", err)
	}
	if err := validateFairTargetContinuationPointers(
		continuationValue, pending, pendingDispatch,
	); err != nil {
		return LoopFrameRecord{}, loopReadIntegrity("Loop pending projection", err)
	}
	frame := LoopFrameRecord{
		RunID: runID, Revision: uint64(frameRevision), Step: step,
		BudgetStateRef: budgetRef, Continuation: bytes.Clone(continuation),
		PendingAttemptID:         pending.String,
		PendingDispatchAttemptID: pendingDispatch.String,
		WaitingReason:            waiting.String,
		LastAuthoritativeEvent:   uint64(lastEvent),
	}
	if owner.Valid {
		expiresAt, err := timeFromUnixMicro(expiry.Int64)
		if err != nil {
			return LoopFrameRecord{}, loopReadIntegrity("lease expiry", err)
		}
		frame.Lease = RunLease{RunID: runID, OwnerID: owner.String,
			LeaseEpoch: uint64(epoch), FrameRevision: uint64(frameRevision),
			ExpiresAt: expiresAt}
	}
	return frame, nil
}

func verifyRunEventHead(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	lastEvent uint64,
) error {
	var count int64
	var maximum sql.NullInt64
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(event_sequence)
		FROM run_events
		WHERE run_id=?
	`, runID).Scan(&count, &maximum); err != nil {
		return loopReadIntegrity("RunEvent head", err)
	}
	if !maximum.Valid ||
		count <= 0 ||
		uint64(count) != lastEvent+1 ||
		uint64(maximum.Int64) != lastEvent {
		return loopReadIntegrity(
			"RunEvent sequence",
			ErrAdmissionIntegrity,
		)
	}
	return nil
}

func loadLoopRecoveryContents(
	ctx context.Context,
	connection readQueryerV1,
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
) ([]ContentRecord, error) {
	required := map[string]ContentKind{
		manifest.TaskInputRef: ContentTaskInput,
	}
	for _, policy := range []corecontract.PolicyRef{
		manifest.BudgetPolicy,
		member.ContextPolicy,
		member.CostPolicy,
		member.SchedulingPolicy,
	} {
		required[policy.Digest] = ContentPolicy
	}
	if member.ModelProfile != nil {
		required[member.ModelProfile.Digest] = ContentConfig
	}
	for _, plan := range member.PortPlans {
		for _, binding := range plan.Bindings {
			required[binding.ConfigRef] = ContentConfig
			required[binding.AuthorityCeilingRef] =
				ContentAuthorityCeiling
			for _, digest := range binding.StaticContextRefs {
				required[digest] = ContentStaticContext
			}
		}
	}
	digests := make([]string, 0, len(required))
	for digest := range required {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	records := make([]ContentRecord, 0, len(digests))
	for _, digest := range digests {
		record, err := queryContent(ctx, connection, digest)
		if err != nil || record.Kind != required[digest] {
			return nil, loopReadIntegrity(
				"recovery content "+digest,
				err,
			)
		}
		records = append(records, cloneContentRecord(record))
	}
	return records, nil
}

func omitLoopRecoveryContentV1(
	records []ContentRecord,
	digest string,
) []ContentRecord {
	filtered := make([]ContentRecord, 0, len(records))
	for _, record := range records {
		if record.Digest == digest {
			continue
		}
		filtered = append(filtered, cloneContentRecord(record))
	}
	return filtered
}

func loadWorkspaceTransferRecoveryMaterialV1(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	root compositeParticipantClosureV1,
	child compositeParticipantClosureV1,
	previousSet *corecontract.CollaborationContributionSetV1,
	previousSetDigest string,
	repairVerdict *corecontract.CollaborationReviewVerdictV1,
	repairVerdictRef string,
	repairBasis *corecontract.WorkspaceTaskSummaryV1,
	repairBasisCanonical []byte,
) (*WorkspaceTransferRecoveryMaterialV1, error) {
	authority, err := loadWorkspaceTransferAuthorityV1(
		ctx,
		queryer,
		root,
		child,
	)
	if err != nil || authority == nil {
		return nil, err
	}
	rootTask, err := queryContent(
		ctx,
		queryer,
		root.Manifest.TaskInputRef,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"Workspace transfer root TASK_INPUT metadata: %w",
			err,
		)
	}
	if rootTask.Kind != ContentTaskInput ||
		rootTask.MediaType != admissionJSONMediaType {
		return nil, fmt.Errorf(
			"Workspace transfer root TASK_INPUT metadata: %w",
			ErrAdmissionIntegrity,
		)
	}
	if err := verifyS1ChatContent(rootTask); err != nil {
		return nil, fmt.Errorf(
			"Workspace transfer root TASK_INPUT closure: %w",
			err,
		)
	}
	material := &WorkspaceTransferRecoveryMaterialV1{
		RootManifest:                  root.Manifest,
		ChildManifest:                 child.Manifest,
		SlotID:                        child.Manifest.Composite.Assignment.SlotID,
		RepairRound:                   child.Manifest.Composite.RepairRound,
		Plan:                          authority.Plan,
		RootGrant:                     authority.RootGrant,
		RootGrantCanonical:            bytes.Clone(authority.RootGrantCanonical),
		TargetGrant:                   authority.TargetGrant,
		TargetGrantCanonical:          bytes.Clone(authority.TargetGrantCanonical),
		RootTaskInput:                 cloneContentRecord(rootTask),
		PreviousContributionSet:       previousSet,
		PreviousContributionSetDigest: previousSetDigest,
		RepairVerdict:                 repairVerdict,
		RepairVerdictRef:              repairVerdictRef,
		RepairBasis:                   repairBasis,
		RepairBasisCanonical:          bytes.Clone(repairBasisCanonical),
	}
	if err := validateWorkspaceTransferRecoveryMaterialV1(
		RunForLoop{
			RunID:    child.Manifest.RunID,
			Manifest: child.Manifest,
		},
		material,
	); err != nil {
		return nil, err
	}
	cloned := cloneWorkspaceTransferRecoveryMaterialV1(*material)
	return &cloned, nil
}

func loadLoopModelDispatches(
	ctx context.Context,
	connection readQueryerV1,
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
	frame LoopFrameRecord,
	contents []ContentRecord,
	verifyPending bool,
) ([]ModelDispatchRecord, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT attempt_id
		FROM model_dispatch_attempts
		WHERE run_id=?
		ORDER BY created_at, attempt_id
	`, frame.RunID)
	if err != nil {
		return nil, loopReadIntegrity("model Attempts", err)
	}
	attemptIDs := make([]string, 0)
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			_ = rows.Close()
			return nil, loopReadIntegrity("model Attempt ID", err)
		}
		attemptIDs = append(attemptIDs, attemptID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, loopReadIntegrity("model Attempt rows", err)
	}
	if err := rows.Close(); err != nil {
		return nil, loopReadIntegrity("model Attempt rows close", err)
	}

	ledgerHead, err := validateModelUsageLedgerHead(
		ctx,
		connection,
		frame.RunID,
		frame.BudgetStateRef,
	)
	if err != nil {
		return nil, loopReadIntegrity("Usage ledger", err)
	}
	expectedBinding, err := exactModelBinding(member)
	if err != nil {
		return nil, loopReadIntegrity("model Binding", err)
	}
	expectedBindingCanonical, err := canonicalModelBinding(expectedBinding)
	if err != nil {
		return nil, loopReadIntegrity("model Binding", err)
	}
	recoveryRun := RunForLoop{
		RunID:    frame.RunID,
		Manifest: manifest,
		Member:   member,
		Contents: contents,
	}
	if manifest.Composite != nil &&
		manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1 {
		root, loadErr := loadCompositeRootManifest(
			ctx,
			connection,
			manifest.Composite.RootRunID,
		)
		if loadErr != nil ||
			manifest.Composite.ParentManifestDigest != root.ManifestDigest {
			return nil, loopReadIntegrity(
				"Reviewer model parameter root closure",
				loadErr,
			)
		}
		recoveryRun.CompositeRoot = &root
	}
	config, err := frozenModelBindingConfig(recoveryRun, expectedBinding)
	if err != nil {
		return nil, loopReadIntegrity("model Binding config", err)
	}
	expectedParameters, err := expectedModelParametersForRun(
		recoveryRun,
		config.Parameters,
	)
	if err != nil {
		return nil, loopReadIntegrity("model parameters", err)
	}

	dispatches := make([]ModelDispatchRecord, 0, len(attemptIDs))
	byID := make(map[string]ModelDispatchRecord, len(attemptIDs))
	unsettled := make([]ModelDispatchRecord, 0, 1)
	for _, attemptID := range attemptIDs {
		record, err := queryModelDispatchRecord(ctx, connection, attemptID)
		if err != nil {
			return nil, loopReadIntegrity("model Attempt closure", err)
		}
		attempt := record.Attempt
		if attempt.RunID != frame.RunID ||
			record.Usage.RunID != frame.RunID ||
			attempt.MemberID != member.MemberID ||
			attempt.MemberSnapshotDigest != member.MemberSnapshotDigest ||
			attempt.FrameRevision >= frame.Revision ||
			!bytes.Equal(
				attempt.BindingCanonical,
				expectedBindingCanonical,
			) ||
			attempt.Provider != config.Provider ||
			attempt.Model != config.Model ||
			attempt.BillingVersion != config.BillingVersion ||
			attempt.PriceSnapshotID != config.PriceSnapshotID ||
			!bytes.Equal(
				attempt.ParametersCanonical,
				expectedParameters,
			) {
			return nil, loopReadIntegrity(
				"model Attempt frozen projection",
				ErrAdmissionIntegrity,
			)
		}
		budget, err := restoreModelBudget(
			attempt.BudgetCanonical,
			frame.RunID,
		)
		if err != nil ||
			budget.BudgetPolicy != manifest.BudgetPolicy ||
			budget.LedgerSequence > ledgerHead {
			return nil, loopReadIntegrity(
				"model Attempt budget",
				ErrAdmissionIntegrity,
			)
		}
		if attempt.State == corecontract.ModelAttemptPending ||
			attempt.State == corecontract.ModelAttemptUnknown {
			unsettled = append(unsettled, record)
		}
		dispatches = append(
			dispatches,
			cloneModelDispatchRecord(record),
		)
		byID[attempt.AttemptID] = record
	}
	if err := verifyLoopUsageIdentities(
		ctx,
		connection,
		frame.RunID,
		byID,
	); err != nil {
		return nil, err
	}
	if verifyPending {
		if err := verifyLoopPendingProjection(frame, byID, unsettled); err != nil {
			return nil, err
		}
	}
	return dispatches, nil
}

func memberHasActionPort(
	member corecontract.MemberExecutionSnapshot,
) bool {
	for _, plan := range member.PortPlans {
		if plan.Port.Name == moduleapi.PortNameActionProvider &&
			plan.Port.ExactVersion == moduleapi.PortVersionV1 {
			return true
		}
	}
	return false
}

func exactActionPlan(
	member corecontract.MemberExecutionSnapshot,
) (moduleapi.PortPlan, error) {
	for _, plan := range member.PortPlans {
		if plan.Port.Name == moduleapi.PortNameActionProvider &&
			plan.Port.ExactVersion == moduleapi.PortVersionV1 {
			return plan, nil
		}
	}
	return moduleapi.PortPlan{}, fmt.Errorf(
		"%w: frozen member lacks action.provider/v1",
		ErrActionDispatchIntegrity,
	)
}

func loadLoopActionDispatches(
	ctx context.Context,
	connection readQueryerV1,
	member corecontract.MemberExecutionSnapshot,
	frame LoopFrameRecord,
	modelDispatches []ModelDispatchRecord,
) ([]ActionDispatchRecord, error) {
	plan, err := exactActionPlan(member)
	if err != nil {
		return nil, loopReadIntegrity("Action PortPlan", err)
	}
	rows, err := connection.QueryContext(ctx, `
		SELECT attempt_id
		FROM dispatch_attempts
		WHERE run_id=? AND dispatch_kind='ACTION'
		ORDER BY created_at, attempt_id
	`, frame.RunID)
	if err != nil {
		return nil, loopReadIntegrity("Action Attempts", err)
	}
	attemptIDs := make([]string, 0)
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			_ = rows.Close()
			return nil, loopReadIntegrity("Action Attempt ID", err)
		}
		attemptIDs = append(attemptIDs, attemptID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, loopReadIntegrity("Action Attempt rows", err)
	}
	if err := rows.Close(); err != nil {
		return nil, loopReadIntegrity("Action Attempt rows close", err)
	}
	if len(attemptIDs) > 1 {
		return nil, loopReadIntegrity(
			"first-slice Action cardinality",
			ErrAdmissionIntegrity,
		)
	}

	modelByID := make(map[string]ModelDispatchRecord, len(modelDispatches))
	for _, dispatch := range modelDispatches {
		modelByID[dispatch.Attempt.AttemptID] = dispatch
	}
	actions := make([]ActionDispatchRecord, 0, len(attemptIDs))
	actionByID := make(map[string]ActionDispatchRecord, len(attemptIDs))
	for _, attemptID := range attemptIDs {
		record, err := queryActionDispatchRecord(ctx, connection, attemptID)
		if err != nil {
			return nil, loopReadIntegrity("Action Attempt closure", err)
		}
		attempt := record.Attempt
		if attempt.RunID != frame.RunID ||
			attempt.MemberID != member.MemberID ||
			attempt.MemberSnapshotDigest != member.MemberSnapshotDigest ||
			attempt.FrameRevision >= frame.Revision ||
			int(attempt.BindingIndex) >= len(plan.Bindings) {
			return nil, loopReadIntegrity(
				"Action Attempt frozen projection",
				ErrAdmissionIntegrity,
			)
		}
		expectedBinding, err := canonicalModelBinding(
			plan.Bindings[attempt.BindingIndex],
		)
		if err != nil || !bytes.Equal(
			expectedBinding,
			attempt.BindingCanonical,
		) {
			return nil, loopReadIntegrity(
				"Action Attempt Binding",
				err,
			)
		}
		source, found := modelByID[attempt.SourceModelAttemptID]
		if !found || source.Attempt.RunID != attempt.RunID ||
			source.Attempt.MemberID != attempt.MemberID ||
			source.Attempt.State != corecontract.ModelAttemptSucceeded ||
			source.Attempt.SourceDispatchAttemptID != "" ||
			source.Attempt.ResultRef == "" {
			return nil, loopReadIntegrity(
				"Action source Model Attempt",
				ErrAdmissionIntegrity,
			)
		}
		sequence, err := corecontract.ParseBudgetStateRefV1(
			attempt.BudgetStateRef,
			attempt.RunID,
		)
		if err != nil {
			return nil, loopReadIntegrity("Action budget", err)
		}
		ledgerHead, err := corecontract.ParseBudgetStateRefV1(
			frame.BudgetStateRef,
			frame.RunID,
		)
		if err != nil || sequence > ledgerHead {
			return nil, loopReadIntegrity(
				"Action budget head",
				ErrAdmissionIntegrity,
			)
		}
		actions = append(actions, cloneActionDispatchRecord(record))
		actionByID[attempt.AttemptID] = record
	}
	for _, dispatch := range modelDispatches {
		sourceID := dispatch.Attempt.SourceDispatchAttemptID
		if sourceID == "" {
			continue
		}
		source, found := actionByID[sourceID]
		if !found || source.Attempt.RunID != dispatch.Attempt.RunID ||
			source.Attempt.MemberID != dispatch.Attempt.MemberID ||
			source.Attempt.State != ActionDispatchSucceeded ||
			source.Result == nil ||
			actionRecordResultStatus(source) != corecontract.ActionResultAvailable {
			return nil, loopReadIntegrity(
				"model-after-Action source closure",
				ErrAdmissionIntegrity,
			)
		}
	}
	return actions, nil
}

func loadLoopChannelIngress(
	ctx context.Context,
	connection readQueryerV1,
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
) (*ChannelIngressReceipt, *ContentRecord, error) {
	receipt, found, err := scanChannelReceipt(connection.QueryRowContext(ctx, `
		SELECT
			tenant_id, workspace_id, endpoint_id, cursor_scope_key,
			cursor_revision, cursor_before_ref, cursor_after_ref,
			endpoint_binding_digest, disposition, reason,
			ingress_key, provider_event_id_digest,
			envelope_ref, envelope_digest,
			principal_id, acl_epoch, admission_key, run_id, created_at
		FROM channel_ingress_receipts
		WHERE run_id=?
	`, manifest.RunID))
	if err != nil || !found {
		if err == nil {
			err = ErrChannelIngressNotFound
		}
		return nil, nil, loopReadIntegrity("Channel ingress receipt", err)
	}
	if err := verifyChannelReceipt(ctx, connection, receipt); err != nil {
		return nil, nil, loopReadIntegrity("Channel ingress receipt closure", err)
	}
	if receipt.Disposition != ChannelIngressAccepted ||
		receipt.RunID != manifest.RunID ||
		receipt.TenantID != manifest.TenantID ||
		receipt.WorkspaceID != member.Workspace.ID {
		return nil, nil, loopReadIntegrity(
			"Channel ingress Run projection",
			ErrAdmissionIntegrity,
		)
	}
	plan, err := exactChannelPlan(member)
	if err != nil || len(plan.Bindings) != 1 {
		return nil, nil, loopReadIntegrity("Channel PortPlan", err)
	}
	bindingDigest, err := moduleapi.ComputeChannelEndpointBindingDigestV1(
		plan.Bindings[0],
	)
	if err != nil || bindingDigest != receipt.EndpointBindingDigest {
		return nil, nil, loopReadIntegrity(
			"Channel Endpoint Binding digest",
			err,
		)
	}
	envelope, err := queryContent(ctx, connection, receipt.EnvelopeRef)
	if err != nil || envelope.Kind != ContentChannelIngressEnvelope ||
		envelope.MediaType != channelEnvelopeMediaType {
		return nil, nil, loopReadIntegrity("Channel ingress envelope", err)
	}
	wire, err := moduleapi.RestoreChannelInboundEnvelopeV1(envelope.CanonicalBytes)
	if err != nil || wire.EndpointID != receipt.EndpointID {
		return nil, nil, loopReadIntegrity("Channel ingress envelope closure", err)
	}
	copy := receipt
	envelopeCopy := cloneContentRecord(envelope)
	return &copy, &envelopeCopy, nil
}

func loadLoopChannelDispatches(
	ctx context.Context,
	connection readQueryerV1,
	member corecontract.MemberExecutionSnapshot,
	frame LoopFrameRecord,
	modelDispatches []ModelDispatchRecord,
	receipt ChannelIngressReceipt,
) ([]ChannelDispatchRecord, error) {
	plan, err := exactChannelPlan(member)
	if err != nil {
		return nil, loopReadIntegrity("Channel PortPlan", err)
	}
	rows, err := connection.QueryContext(ctx, `
		SELECT attempt_id
		FROM dispatch_attempts
		WHERE run_id=? AND dispatch_kind='CHANNEL_SEND'
		ORDER BY created_at, attempt_id
	`, frame.RunID)
	if err != nil {
		return nil, loopReadIntegrity("Channel Attempts", err)
	}
	attemptIDs := make([]string, 0, 1)
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			_ = rows.Close()
			return nil, loopReadIntegrity("Channel Attempt ID", err)
		}
		attemptIDs = append(attemptIDs, attemptID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, loopReadIntegrity("Channel Attempt rows", err)
	}
	if err := rows.Close(); err != nil {
		return nil, loopReadIntegrity("Channel Attempt rows close", err)
	}
	if len(attemptIDs) > 1 {
		return nil, loopReadIntegrity(
			"Channel send cardinality",
			ErrAdmissionIntegrity,
		)
	}
	modelByID := make(map[string]ModelDispatchRecord, len(modelDispatches))
	for _, dispatch := range modelDispatches {
		modelByID[dispatch.Attempt.AttemptID] = dispatch
	}
	channels := make([]ChannelDispatchRecord, 0, len(attemptIDs))
	for _, attemptID := range attemptIDs {
		record, err := queryChannelDispatchRecord(ctx, connection, attemptID)
		if err != nil {
			return nil, loopReadIntegrity("Channel Attempt closure", err)
		}
		attempt := record.Attempt
		if attempt.RunID != frame.RunID ||
			attempt.MemberID != member.MemberID ||
			attempt.MemberSnapshotDigest != member.MemberSnapshotDigest ||
			attempt.FrameRevision >= frame.Revision ||
			uint64(attempt.BindingIndex) >= uint64(len(plan.Bindings)) ||
			attempt.EndpointID != receipt.EndpointID ||
			attempt.IngressKey != receipt.IngressKey ||
			attempt.EffectClass != moduleapi.EffectIrreversibleWrite ||
			attempt.MaxResultBytes > moduleapi.MaxChannelProviderReceiptBytesV1 {
			return nil, loopReadIntegrity(
				"Channel Attempt frozen projection",
				ErrAdmissionIntegrity,
			)
		}
		expectedBinding, err := canonicalModelBinding(
			plan.Bindings[attempt.BindingIndex],
		)
		if err != nil || !bytes.Equal(expectedBinding, attempt.BindingCanonical) {
			return nil, loopReadIntegrity("Channel Attempt Binding", err)
		}
		source, found := modelByID[attempt.SourceModelAttemptID]
		if !found || source.Attempt.RunID != attempt.RunID ||
			source.Attempt.MemberID != attempt.MemberID ||
			source.Attempt.State != corecontract.ModelAttemptSucceeded ||
			source.Attempt.ResultRef == "" {
			return nil, loopReadIntegrity(
				"Channel source Model Attempt",
				ErrAdmissionIntegrity,
			)
		}
		sequence, err := corecontract.ParseBudgetStateRefV1(
			attempt.BudgetStateRef,
			attempt.RunID,
		)
		if err != nil {
			return nil, loopReadIntegrity("Channel budget", err)
		}
		ledgerHead, err := corecontract.ParseBudgetStateRefV1(
			frame.BudgetStateRef,
			frame.RunID,
		)
		if err != nil || sequence > ledgerHead {
			return nil, loopReadIntegrity(
				"Channel budget head",
				ErrAdmissionIntegrity,
			)
		}
		channels = append(channels, cloneChannelDispatchRecord(record))
	}
	return channels, nil
}

func verifyLoopAttemptProjection(
	frame LoopFrameRecord,
	models []ModelDispatchRecord,
	actions []ActionDispatchRecord,
	channels []ChannelDispatchRecord,
) error {
	continuation, err := corecontract.RestoreLoopContinuationV1(
		frame.Continuation,
	)
	if err != nil {
		return loopReadIntegrity("Loop continuation", err)
	}
	modelByID := make(map[string]ModelDispatchRecord, len(models))
	actionByID := make(map[string]ActionDispatchRecord, len(actions))
	channelByID := make(map[string]ChannelDispatchRecord, len(channels))
	unsettledModels := make([]ModelDispatchRecord, 0, 1)
	unsettledActions := make([]ActionDispatchRecord, 0, 1)
	unsettledChannels := make([]ChannelDispatchRecord, 0, 1)
	for _, record := range models {
		modelByID[record.Attempt.AttemptID] = record
		if record.Attempt.State == corecontract.ModelAttemptPending ||
			record.Attempt.State == corecontract.ModelAttemptUnknown {
			unsettledModels = append(unsettledModels, record)
		}
	}
	for _, record := range actions {
		actionByID[record.Attempt.AttemptID] = record
		if record.Attempt.State == ActionDispatchPending ||
			record.Attempt.State == ActionDispatchUnknown {
			unsettledActions = append(unsettledActions, record)
		}
	}
	for _, record := range channels {
		channelByID[record.Attempt.AttemptID] = record
		if record.Attempt.State == DispatchPending ||
			record.Attempt.State == DispatchUnknown {
			unsettledChannels = append(unsettledChannels, record)
		}
	}
	if len(unsettledModels)+len(unsettledActions)+len(unsettledChannels) > 1 {
		return loopReadIntegrity(
			"cross-family unsettled Attempt cardinality",
			ErrAdmissionIntegrity,
		)
	}
	requireModel := func(state corecontract.ModelAttemptState) error {
		if len(unsettledModels) != 1 || len(unsettledActions) != 0 ||
			len(unsettledChannels) != 0 {
			return loopReadIntegrity(
				"unsettled Model Attempt cardinality",
				ErrAdmissionIntegrity,
			)
		}
		attempt := unsettledModels[0].Attempt
		if attempt.State != state ||
			attempt.AttemptID != frame.PendingAttemptID ||
			frame.PendingDispatchAttemptID != "" ||
			continuation.AttemptKind != corecontract.AttemptKindModel ||
			attempt.AttemptID != continuation.AttemptID ||
			attempt.LogicalStepID != continuation.LogicalStepID {
			return loopReadIntegrity(
				"pending Model Attempt projection",
				ErrAdmissionIntegrity,
			)
		}
		return nil
	}
	requireAction := func(state ActionDispatchState, pendingPointer bool) error {
		if len(unsettledActions) != 1 || len(unsettledModels) != 0 ||
			len(unsettledChannels) != 0 {
			return loopReadIntegrity(
				"unsettled Action Attempt cardinality",
				ErrAdmissionIntegrity,
			)
		}
		attempt := unsettledActions[0].Attempt
		expectedPendingID := ""
		if pendingPointer {
			expectedPendingID = attempt.AttemptID
		}
		if attempt.State != state || frame.PendingAttemptID != "" ||
			frame.PendingDispatchAttemptID != expectedPendingID ||
			continuation.AttemptKind != corecontract.AttemptKindAction ||
			attempt.AttemptID != continuation.AttemptID ||
			attempt.LogicalStepID != continuation.LogicalStepID {
			return loopReadIntegrity(
				"pending Action Attempt projection",
				ErrAdmissionIntegrity,
			)
		}
		return nil
	}
	requireChannel := func(state DispatchState, pendingPointer bool) error {
		if len(unsettledChannels) != 1 || len(unsettledModels) != 0 ||
			len(unsettledActions) != 0 {
			return loopReadIntegrity(
				"unsettled Channel Attempt cardinality",
				ErrAdmissionIntegrity,
			)
		}
		attempt := unsettledChannels[0].Attempt
		expectedPendingID := ""
		if pendingPointer {
			expectedPendingID = attempt.AttemptID
		}
		if attempt.State != state || frame.PendingAttemptID != "" ||
			frame.PendingDispatchAttemptID != expectedPendingID ||
			continuation.AttemptKind != corecontract.AttemptKindChannel ||
			attempt.AttemptID != continuation.AttemptID ||
			attempt.LogicalStepID != continuation.LogicalStepID {
			return loopReadIntegrity(
				"pending Channel Attempt projection",
				ErrAdmissionIntegrity,
			)
		}
		return nil
	}

	switch frame.Step {
	case corecontract.WaitingRepairActivationLoopStep:
		if len(models) != 0 || len(actions) != 0 || len(channels) != 0 ||
			frame.PendingAttemptID != "" || frame.PendingDispatchAttemptID != "" {
			return loopReadIntegrity(
				"dormant repair hidden Attempt", ErrAdmissionIntegrity,
			)
		}
	case corecontract.WaitingChildrenLoopStep:
		if len(models) != 0 || len(actions) != 0 || len(channels) != 0 ||
			frame.PendingAttemptID != "" || frame.PendingDispatchAttemptID != "" {
			return loopReadIntegrity(
				"WAITING_CHILDREN hidden Attempt", ErrAdmissionIntegrity,
			)
		}
	case corecontract.InitialLoopStep:
		if len(unsettledModels) != 0 || len(unsettledActions) != 0 ||
			len(unsettledChannels) != 0 ||
			frame.PendingAttemptID != "" ||
			frame.PendingDispatchAttemptID != "" {
			return loopReadIntegrity(
				"READY hidden Attempt",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.ModelPendingLoopStep:
		return requireModel(corecontract.ModelAttemptPending)
	case corecontract.ActionPendingLoopStep:
		return requireAction(ActionDispatchPending, true)
	case corecontract.ChannelPendingLoopStep:
		return requireChannel(DispatchPending, true)
	case corecontract.WaitingReconciliationLoopStep:
		switch continuation.AttemptKind {
		case corecontract.AttemptKindModel:
			return requireModel(corecontract.ModelAttemptUnknown)
		case corecontract.AttemptKindAction:
			return requireAction(ActionDispatchUnknown, false)
		case corecontract.AttemptKindChannel:
			return requireChannel(DispatchUnknown, false)
		default:
			return loopReadIntegrity(
				"reconciliation AttemptKind",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.ModelReadyAfterActionLoopStep:
		if len(unsettledModels) != 0 || len(unsettledActions) != 0 ||
			len(unsettledChannels) != 0 ||
			frame.PendingAttemptID != "" ||
			frame.PendingDispatchAttemptID != "" ||
			continuation.AttemptKind != corecontract.AttemptKindAction {
			return loopReadIntegrity(
				"MODEL_READY_AFTER_ACTION projection",
				ErrAdmissionIntegrity,
			)
		}
		record, found := actionByID[continuation.AttemptID]
		if !found || record.Attempt.State != ActionDispatchSucceeded ||
			record.Result == nil ||
			actionRecordResultStatus(record) != corecontract.ActionResultAvailable ||
			record.Attempt.LogicalStepID != continuation.LogicalStepID {
			return loopReadIntegrity(
				"MODEL_READY_AFTER_ACTION source",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.TerminatedLoopStep:
		if len(unsettledModels) != 0 || len(unsettledActions) != 0 ||
			len(unsettledChannels) != 0 ||
			frame.PendingAttemptID != "" ||
			frame.PendingDispatchAttemptID != "" {
			return loopReadIntegrity(
				"TERMINATED hidden unsettled Attempt",
				ErrAdmissionIntegrity,
			)
		}
		if continuation.CoreFailureReason != "" {
			return nil
		}
		switch continuation.AttemptKind {
		case corecontract.AttemptKindModel:
			record, found := modelByID[continuation.AttemptID]
			if !found ||
				record.Attempt.LogicalStepID != continuation.LogicalStepID ||
				(record.Attempt.State != corecontract.ModelAttemptSucceeded &&
					record.Attempt.State != corecontract.ModelAttemptFailed) {
				return loopReadIntegrity(
					"TERMINATED Model Attempt",
					ErrAdmissionIntegrity,
				)
			}
		case corecontract.AttemptKindAction:
			record, found := actionByID[continuation.AttemptID]
			if !found ||
				record.Attempt.LogicalStepID != continuation.LogicalStepID ||
				(record.Attempt.State != ActionDispatchFailed &&
					(record.Attempt.State != ActionDispatchSucceeded ||
						actionRecordResultStatus(record) != corecontract.ActionResultRejected)) {
				return loopReadIntegrity(
					"TERMINATED Action Attempt",
					ErrAdmissionIntegrity,
				)
			}
		case corecontract.AttemptKindChannel:
			record, found := channelByID[continuation.AttemptID]
			if !found ||
				record.Attempt.LogicalStepID != continuation.LogicalStepID ||
				(record.Attempt.State != DispatchSucceeded &&
					record.Attempt.State != DispatchFailed) {
				return loopReadIntegrity(
					"TERMINATED Channel Attempt",
					ErrAdmissionIntegrity,
				)
			}
		default:
			return loopReadIntegrity(
				"TERMINATED AttemptKind",
				ErrAdmissionIntegrity,
			)
		}
	}
	return nil
}

func actionRecordResultStatus(
	record ActionDispatchRecord,
) corecontract.ActionResultStatusV1 {
	if record.Result == nil {
		return ""
	}
	var result corecontract.ActionResultV1
	if err := json.Unmarshal(record.Result.CanonicalBytes, &result); err != nil {
		return ""
	}
	return result.Status
}

func verifyLoopUsageIdentities(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	attempts map[string]ModelDispatchRecord,
) error {
	rows, err := connection.QueryContext(ctx, `
		SELECT attempt_id
		FROM model_usage
		WHERE run_id=?
		ORDER BY attempt_id
	`, runID)
	if err != nil {
		return loopReadIntegrity("Usage identities", err)
	}
	seen := make(map[string]struct{}, len(attempts))
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			_ = rows.Close()
			return loopReadIntegrity("Usage identity", err)
		}
		if _, found := attempts[attemptID]; !found {
			_ = rows.Close()
			return loopReadIntegrity(
				"cross-Run Usage identity",
				ErrAdmissionIntegrity,
			)
		}
		seen[attemptID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return loopReadIntegrity("Usage identity rows", err)
	}
	if err := rows.Close(); err != nil {
		return loopReadIntegrity("Usage identity rows close", err)
	}
	if len(seen) != len(attempts) {
		return loopReadIntegrity(
			"Attempt/Usage cardinality",
			ErrAdmissionIntegrity,
		)
	}
	return nil
}

func verifyLoopPendingProjection(
	frame LoopFrameRecord,
	attempts map[string]ModelDispatchRecord,
	unsettled []ModelDispatchRecord,
) error {
	continuation, err := corecontract.RestoreLoopContinuationV1(
		frame.Continuation,
	)
	if err != nil {
		return loopReadIntegrity("Loop continuation", err)
	}
	requireCurrent := func(
		state corecontract.ModelAttemptState,
	) error {
		if len(unsettled) != 1 {
			return loopReadIntegrity(
				"unsettled model Attempt cardinality",
				ErrAdmissionIntegrity,
			)
		}
		attempt := unsettled[0].Attempt
		if attempt.State != state ||
			attempt.AttemptID != frame.PendingAttemptID ||
			attempt.AttemptID != continuation.AttemptID ||
			attempt.LogicalStepID != continuation.LogicalStepID {
			return loopReadIntegrity(
				"pending model Attempt projection",
				ErrAdmissionIntegrity,
			)
		}
		return nil
	}

	switch frame.Step {
	case corecontract.WaitingRepairActivationLoopStep:
		if len(attempts) != 0 || frame.PendingAttemptID != "" ||
			frame.PendingDispatchAttemptID != "" {
			return loopReadIntegrity(
				"dormant repair hidden model Attempt",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.WaitingChildrenLoopStep:
		if len(attempts) != 0 || frame.PendingAttemptID != "" ||
			frame.PendingDispatchAttemptID != "" {
			return loopReadIntegrity(
				"WAITING_CHILDREN hidden model Attempt",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.InitialLoopStep:
		if len(unsettled) != 0 {
			return loopReadIntegrity(
				"READY hidden model Attempt",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.ModelPendingLoopStep:
		return requireCurrent(corecontract.ModelAttemptPending)
	case corecontract.WaitingReconciliationLoopStep:
		return requireCurrent(corecontract.ModelAttemptUnknown)
	case corecontract.ActionPendingLoopStep,
		corecontract.ChannelPendingLoopStep,
		corecontract.ModelReadyAfterActionLoopStep:
		return loopReadIntegrity(
			"pure Model Run contains a dispatch-only Frame state",
			ErrAdmissionIntegrity,
		)
	case corecontract.TerminatedLoopStep:
		if len(unsettled) != 0 {
			return loopReadIntegrity(
				"TERMINATED hidden unsettled model Attempt",
				ErrAdmissionIntegrity,
			)
		}
		if continuation.CoreFailureReason != "" {
			return nil
		}
		if continuation.AttemptKind != corecontract.AttemptKindModel {
			return loopReadIntegrity(
				"pure Model terminal AttemptKind",
				ErrAdmissionIntegrity,
			)
		}
		record, found := attempts[continuation.AttemptID]
		if !found ||
			record.Attempt.LogicalStepID != continuation.LogicalStepID ||
			(record.Attempt.State != corecontract.ModelAttemptSucceeded &&
				record.Attempt.State != corecontract.ModelAttemptFailed) {
			return loopReadIntegrity(
				"TERMINATED model Attempt",
				ErrAdmissionIntegrity,
			)
		}
	default:
		return loopReadIntegrity(
			"pure Model Run Frame state",
			ErrAdmissionIntegrity,
		)
	}
	return nil
}

func validateLoopRunFrameProjection(
	runState string,
	disposition string,
	frame LoopFrameRecord,
) error {
	switch frame.Step {
	case corecontract.WaitingRepairActivationLoopStep:
		if runState != corecontract.InitialRunState ||
			disposition != "WAITING_EXTERNAL" ||
			frame.WaitingReason != compositeRepairDormantWaitingReason {
			return loopReadIntegrity(
				"dormant repair Run/Frame projection", ErrAdmissionIntegrity,
			)
		}
	case corecontract.WaitingChildrenLoopStep:
		if runState != corecontract.InitialRunState ||
			disposition != "WAITING_EXTERNAL" ||
			frame.WaitingReason != compositeChildrenPendingWaitingReason {
			return loopReadIntegrity(
				"WAITING_CHILDREN Run/Frame projection",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.InitialLoopStep,
		corecontract.ModelPendingLoopStep,
		corecontract.ActionPendingLoopStep,
		corecontract.ChannelPendingLoopStep,
		corecontract.ModelReadyAfterActionLoopStep:
		if runState != corecontract.InitialRunState ||
			disposition != "" ||
			frame.WaitingReason != "" {
			return loopReadIntegrity(
				"active Run/Frame projection",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.WaitingReconciliationLoopStep:
		if runState != corecontract.WaitingReconciliationLoopStep ||
			disposition !=
				corecontract.WaitingReconciliationLoopStep ||
			(frame.WaitingReason != modelUnknownWaitingReason &&
				frame.WaitingReason != actionUnknownWaitingReason &&
				frame.WaitingReason != channelUnknownWaitingReason) {
			return loopReadIntegrity(
				"reconciliation Run/Frame projection",
				ErrAdmissionIntegrity,
			)
		}
	case corecontract.TerminatedLoopStep:
		if runState != corecontract.TerminatedLoopStep ||
			disposition != corecontract.TerminatedLoopStep ||
			frame.WaitingReason != "" {
			return loopReadIntegrity(
				"terminal Run/Frame projection",
				ErrAdmissionIntegrity,
			)
		}
	}
	return nil
}

func loadLoopConversationHistory(
	ctx context.Context,
	connection readQueryerV1,
	currentManifest corecontract.RunManifest,
	currentMember corecontract.MemberExecutionSnapshot,
) ([]ConversationHistoryTurnRecord, error) {
	turn := currentManifest.ConversationTurn
	if turn == nil {
		return nil, nil
	}
	if turn.TurnIndex == 1 {
		if turn.PredecessorRunID != "" {
			return nil, loopReadIntegrity(
				"Conversation first-turn predecessor",
				ErrAdmissionIntegrity,
			)
		}
		return nil, nil
	}
	if turn.PredecessorRunID == "" {
		return nil, loopReadIntegrity(
			"Conversation predecessor",
			ErrAdmissionIntegrity,
		)
	}
	// Every predecessor contributes two model messages and the current task
	// contributes one. Enforce the protocol's bounded request cardinality
	// before allocating or traversing an unbounded chain.
	maxPredecessors := uint64((moduleapi.MaxManifestEntries - 1) / 2)
	if turn.TurnIndex-1 > maxPredecessors {
		return nil, loopReadIntegrity(
			"Conversation predecessor count",
			ErrAdmissionIntegrity,
		)
	}

	reversed := make(
		[]ConversationHistoryTurnRecord,
		0,
		int(turn.TurnIndex-1),
	)
	seen := make(map[string]struct{}, cap(reversed))
	sourceCandidateMatched := false
	nextRunID := turn.PredecessorRunID
	for expectedIndex := turn.TurnIndex - 1; expectedIndex > 0; expectedIndex-- {
		if _, duplicate := seen[nextRunID]; duplicate {
			return nil, loopReadIntegrity(
				"Conversation predecessor cycle",
				ErrAdmissionIntegrity,
			)
		}
		seen[nextRunID] = struct{}{}

		var (
			tenantID       string
			workspaceID    string
			admissionKey   string
			intentDigest   string
			conversationID sql.NullString
			turnIndex      sql.NullInt64
			predecessorID  sql.NullString
		)
		if err := connection.QueryRowContext(ctx, `
			SELECT
				tenant_id,
				workspace_id,
				admission_key,
				admission_intent_digest,
				conversation_id,
				conversation_turn_index,
				conversation_predecessor_run_id
			FROM runs
			WHERE run_id=?
		`, nextRunID).Scan(
			&tenantID,
			&workspaceID,
			&admissionKey,
			&intentDigest,
			&conversationID,
			&turnIndex,
			&predecessorID,
		); err != nil {
			return nil, loopReadIntegrity(
				"Conversation predecessor Run",
				err,
			)
		}
		if tenantID != currentManifest.TenantID ||
			workspaceID != currentManifest.Workspace.ID ||
			!conversationID.Valid ||
			conversationID.String != turn.ConversationID ||
			!turnIndex.Valid || turnIndex.Int64 <= 0 ||
			uint64(turnIndex.Int64) != expectedIndex {
			return nil, loopReadIntegrity(
				"Conversation predecessor projection",
				ErrAdmissionIntegrity,
			)
		}
		if _, err := loadAdmissionClosure(
			ctx,
			connection,
			nextRunID,
			tenantID,
			admissionKey,
			intentDigest,
			workspaceID,
		); err != nil {
			return nil, loopReadIntegrity(
				"Conversation predecessor Admission",
				err,
			)
		}

		predecessorManifest, predecessorMember, err :=
			loadConversationRunIdentity(ctx, connection, nextRunID)
		if err != nil {
			return nil, err
		}
		predecessorTurn := predecessorManifest.ConversationTurn
		if predecessorTurn == nil ||
			predecessorManifest.TenantID != currentManifest.TenantID ||
			predecessorManifest.Workspace.ID != currentManifest.Workspace.ID ||
			predecessorManifest.PrimaryAgent.ID != currentManifest.PrimaryAgent.ID ||
			predecessorMember.Profile.ID != currentMember.Profile.ID ||
			predecessorTurn.ConversationID != turn.ConversationID ||
			predecessorTurn.PrincipalID != turn.PrincipalID ||
			predecessorTurn.TurnIndex != expectedIndex ||
			predecessorTurn.PredecessorRunID != predecessorID.String ||
			predecessorID.Valid != (predecessorTurn.PredecessorRunID != "") {
			return nil, loopReadIntegrity(
				"Conversation predecessor frozen identity",
				ErrAdmissionIntegrity,
			)
		}
		if expectedIndex == 1 && predecessorID.Valid ||
			expectedIndex > 1 && !predecessorID.Valid {
			return nil, loopReadIntegrity(
				"Conversation predecessor continuity",
				ErrAdmissionIntegrity,
			)
		}

		terminal, err := loadTerminalRunResult(ctx, connection, nextRunID)
		if err != nil || !successfulConversationTerminal(terminal) {
			return nil, loopReadIntegrity(
				"Conversation predecessor terminal result",
				err,
			)
		}
		userContent, err := queryContent(
			ctx,
			connection,
			predecessorManifest.TaskInputRef,
		)
		if err != nil || userContent.Kind != ContentTaskInput {
			return nil, loopReadIntegrity(
				"Conversation predecessor USER content",
				err,
			)
		}
		assistantContent, err := loadConversationAssistantContent(
			ctx,
			connection,
			nextRunID,
			terminal,
		)
		if err != nil {
			return nil, err
		}
		sourceContextCompilationAttemptID := ""
		var sourceContextCompilation *ContentRecord
		isDirectPredecessor := expectedIndex == turn.TurnIndex-1
		isLatestExactQuestion := !sourceCandidateMatched &&
			userContent.Digest == currentManifest.TaskInputRef
		if isLatestExactQuestion {
			// K3 must never fall through an exact-question predecessor whose
			// successful model Attempt has no ContextCompilation. M2 may load
			// the direct predecessor independently, but it does not relax that
			// latest-exact boundary.
			sourceCandidateMatched = true
		}
		// Same Agent ID keeps the Conversation linear, but a new immutable
		// Agent version/digest makes the earlier Compilation inapplicable to
		// the current Agent. Keep the raw turn and latest-exact boundary while
		// withholding the candidate instead of treating the version change as
		// corruption.
		if (isDirectPredecessor || isLatestExactQuestion) &&
			predecessorMember.Agent == currentMember.Agent {
			sourceContextCompilationAttemptID, sourceContextCompilation, err =
				loadConversationCompilationSource(
					ctx,
					connection,
					terminal,
					nextRunID,
					predecessorMember.MemberID,
					predecessorMember.MemberSnapshotDigest,
					assistantContent.Digest,
				)
			if err != nil {
				return nil, err
			}
		}
		reversed = append(reversed, ConversationHistoryTurnRecord{
			TurnIndex:                         expectedIndex,
			SourceRunID:                       nextRunID,
			UserContent:                       cloneContentRecord(userContent),
			AssistantContent:                  cloneContentRecord(assistantContent),
			SourceAttemptID:                   terminal.AttemptID,
			SourceContextCompilationAttemptID: sourceContextCompilationAttemptID,
			SourceContextCompilation:          sourceContextCompilation,
		})
		nextRunID = predecessorTurn.PredecessorRunID
	}
	if nextRunID != "" {
		return nil, loopReadIntegrity(
			"Conversation predecessor root",
			ErrAdmissionIntegrity,
		)
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed, nil
}

// loadConversationCompilationSource follows only the already-frozen model-2
// -> Action -> model-1 chain. It never scans Attempts. model-1 is the sole
// compilation-bearing successful Attempt in an Action Run because model-2 is
// required by contract to persist without a ContextCompilation.
func loadConversationCompilationSource(
	ctx context.Context,
	connection readQueryerV1,
	terminal TerminalRunResult,
	runID string,
	memberID string,
	memberSnapshotDigest string,
	assistantDigest string,
) (string, *ContentRecord, error) {
	if terminal.RunID != runID || terminal.MemberID != memberID ||
		terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.ModelState != corecontract.ModelAttemptSucceeded ||
		terminal.AttemptID == "" {
		return "", nil, loopReadIntegrity(
			"Conversation terminal model identity",
			ErrAdmissionIntegrity,
		)
	}
	terminalSource, err := queryModelDispatchRecord(
		ctx,
		connection,
		terminal.AttemptID,
	)
	if err != nil ||
		terminalSource.Attempt.AttemptID != terminal.AttemptID ||
		terminalSource.Attempt.RunID != runID ||
		terminalSource.Attempt.MemberID != memberID ||
		terminalSource.Attempt.MemberSnapshotDigest != memberSnapshotDigest ||
		terminalSource.Attempt.State != corecontract.ModelAttemptSucceeded ||
		terminalSource.Attempt.ResultRef != assistantDigest {
		return "", nil, loopReadIntegrity(
			"Conversation terminal model Attempt",
			err,
		)
	}
	if terminalSource.Attempt.ContextCompilation != nil {
		if terminalSource.Attempt.SourceDispatchAttemptID != "" {
			return "", nil, loopReadIntegrity(
				"Conversation compilation-bearing terminal model Attempt",
				ErrAdmissionIntegrity,
			)
		}
		compilation := cloneContentRecord(
			*terminalSource.Attempt.ContextCompilation,
		)
		return terminalSource.Attempt.AttemptID, &compilation, nil
	}
	if terminalSource.Attempt.SourceDispatchAttemptID == "" {
		return "", nil, nil
	}
	if terminalSource.Attempt.LogicalStepID != secondModelLogicalStepID {
		return "", nil, loopReadIntegrity(
			"Conversation model-2 logical step",
			ErrAdmissionIntegrity,
		)
	}
	action, err := queryActionDispatchRecord(
		ctx,
		connection,
		terminalSource.Attempt.SourceDispatchAttemptID,
	)
	if err != nil ||
		action.Attempt.AttemptID !=
			terminalSource.Attempt.SourceDispatchAttemptID ||
		action.Attempt.RunID != runID ||
		action.Attempt.MemberID != memberID ||
		action.Attempt.MemberSnapshotDigest != memberSnapshotDigest ||
		action.Attempt.LogicalStepID != firstActionLogicalStepID ||
		action.Attempt.State != ActionDispatchSucceeded ||
		action.Attempt.SourceModelAttemptID == "" {
		return "", nil, loopReadIntegrity(
			"Conversation model-2 source Action",
			err,
		)
	}
	modelOne, err := queryModelDispatchRecord(
		ctx,
		connection,
		action.Attempt.SourceModelAttemptID,
	)
	if err != nil ||
		modelOne.Attempt.AttemptID != action.Attempt.SourceModelAttemptID ||
		modelOne.Attempt.RunID != runID ||
		modelOne.Attempt.MemberID != memberID ||
		modelOne.Attempt.MemberSnapshotDigest != memberSnapshotDigest ||
		modelOne.Attempt.LogicalStepID != firstModelLogicalStepID ||
		modelOne.Attempt.SourceDispatchAttemptID != "" ||
		modelOne.Attempt.State != corecontract.ModelAttemptSucceeded ||
		modelOne.Attempt.ContextCompilation == nil {
		return "", nil, loopReadIntegrity(
			"Conversation Action source model-1",
			err,
		)
	}
	compilation := cloneContentRecord(*modelOne.Attempt.ContextCompilation)
	return modelOne.Attempt.AttemptID, &compilation, nil
}

func loadConversationRunIdentity(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
) (corecontract.RunManifest, corecontract.MemberExecutionSnapshot, error) {
	var manifestCanonical []byte
	var manifestDigest string
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM run_manifests
		WHERE run_id=?
	`, runID).Scan(&manifestCanonical, &manifestDigest); err != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{},
			loopReadIntegrity("Conversation predecessor Manifest", err)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil || manifest.RunID != runID ||
		manifest.ManifestDigest != manifestDigest {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{},
			loopReadIntegrity(
				"Conversation predecessor Manifest",
				ErrAdmissionIntegrity,
			)
	}
	var memberCanonical []byte
	var memberDigest string
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM member_execution_snapshots
		WHERE run_id=? AND member_id=?
	`, runID, manifest.PrimaryMemberID).Scan(
		&memberCanonical,
		&memberDigest,
	); err != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{},
			loopReadIntegrity("Conversation predecessor Member", err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil || member.MemberSnapshotDigest != memberDigest ||
		manifest.ValidateAgainstMember(member) != nil {
		return corecontract.RunManifest{}, corecontract.MemberExecutionSnapshot{},
			loopReadIntegrity(
				"Conversation predecessor Member",
				ErrAdmissionIntegrity,
			)
	}
	return manifest, member, nil
}

func successfulConversationTerminal(result TerminalRunResult) bool {
	return result.AttemptKind == corecontract.AttemptKindModel &&
		result.ModelState == corecontract.ModelAttemptSucceeded &&
		result.ErrorClassification == "" &&
		len(result.OutputCanonical) != 0 &&
		result.Output.ActionRequest == nil &&
		result.Output.AssistantText != ""
}

func loadConversationAssistantContent(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	terminal TerminalRunResult,
) (ContentRecord, error) {
	var contentRef, contentDigest string
	if err := connection.QueryRowContext(ctx, `
		SELECT content_ref, content_digest
		FROM history_entries
		WHERE run_id=? AND source_attempt_id=? AND role=?
	`, runID, terminal.AttemptID, string(moduleapi.ModelRoleAssistant)).Scan(
		&contentRef,
		&contentDigest,
	); err != nil {
		return ContentRecord{}, loopReadIntegrity(
			"Conversation predecessor ASSISTANT History",
			err,
		)
	}
	if contentRef != contentDigest {
		return ContentRecord{}, loopReadIntegrity(
			"Conversation predecessor ASSISTANT identity",
			ErrAdmissionIntegrity,
		)
	}
	content, err := queryContent(ctx, connection, contentRef)
	if err != nil || content.Kind != ContentModelResult ||
		!bytes.Equal(content.CanonicalBytes, terminal.OutputCanonical) {
		return ContentRecord{}, loopReadIntegrity(
			"Conversation predecessor ASSISTANT content",
			err,
		)
	}
	return content, nil
}

func loadLoopHistory(
	ctx context.Context,
	connection readQueryerV1,
	runID string,
	memberID string,
	dispatches []ModelDispatchRecord,
) ([]HistoryEntryRecord, error) {
	rows, err := connection.QueryContext(ctx, `
		SELECT
			history_sequence,
			member_id,
			role,
			content_ref,
			content_digest,
			source_attempt_id,
			created_at
		FROM history_entries
		WHERE run_id=?
		ORDER BY history_sequence
	`, runID)
	if err != nil {
		return nil, loopReadIntegrity("History", err)
	}
	type pendingHistory struct {
		sequence      int64
		memberID      string
		role          string
		contentRef    string
		contentDigest string
		sourceAttempt sql.NullString
		createdAt     int64
	}
	pending := make([]pendingHistory, 0)
	for rows.Next() {
		var entry pendingHistory
		if err := rows.Scan(
			&entry.sequence,
			&entry.memberID,
			&entry.role,
			&entry.contentRef,
			&entry.contentDigest,
			&entry.sourceAttempt,
			&entry.createdAt,
		); err != nil {
			_ = rows.Close()
			return nil, loopReadIntegrity("History row", err)
		}
		pending = append(pending, entry)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, loopReadIntegrity("History rows", err)
	}
	if err := rows.Close(); err != nil {
		return nil, loopReadIntegrity("History rows close", err)
	}

	attempts := make(
		map[string]ModelDispatchRecord,
		len(dispatches),
	)
	for _, dispatch := range dispatches {
		attempts[dispatch.Attempt.AttemptID] = dispatch
	}
	historyByAttempt := make(map[string]uint64, len(dispatches))
	history := make([]HistoryEntryRecord, 0, len(pending))
	for index, entry := range pending {
		if entry.sequence != int64(index+1) ||
			entry.memberID != memberID ||
			entry.contentRef != entry.contentDigest ||
			entry.role != string(moduleapi.ModelRoleAssistant) ||
			!entry.sourceAttempt.Valid {
			return nil, loopReadIntegrity(
				"History sequence, role, or identity",
				ErrAdmissionIntegrity,
			)
		}
		content, err := queryContent(ctx, connection, entry.contentRef)
		if err != nil || content.Kind != ContentModelResult {
			return nil, loopReadIntegrity("History content", err)
		}
		source, found := attempts[entry.sourceAttempt.String]
		if !found ||
			source.Attempt.RunID != runID ||
			source.Attempt.MemberID != memberID ||
			source.Attempt.State != corecontract.ModelAttemptSucceeded ||
			source.Attempt.ResultRef != entry.contentRef {
			return nil, loopReadIntegrity(
				"History source model Attempt",
				ErrAdmissionIntegrity,
			)
		}
		historyByAttempt[entry.sourceAttempt.String]++
		createdAt, err := timeFromUnixMicro(entry.createdAt)
		if err != nil {
			return nil, loopReadIntegrity("History timestamp", err)
		}
		history = append(history, HistoryEntryRecord{
			Sequence:        uint64(entry.sequence),
			MemberID:        entry.memberID,
			Role:            entry.role,
			Content:         cloneContentRecord(content),
			SourceAttemptID: entry.sourceAttempt.String,
			CreatedAt:       createdAt,
		})
	}
	for _, dispatch := range dispatches {
		count := historyByAttempt[dispatch.Attempt.AttemptID]
		expected := uint64(0)
		if dispatch.Attempt.State == corecontract.ModelAttemptSucceeded {
			if dispatch.Attempt.ResultRef == "" {
				return nil, loopReadIntegrity(
					"successful model Attempt result",
					ErrAdmissionIntegrity,
				)
			}
			result, err := queryContent(
				ctx,
				connection,
				dispatch.Attempt.ResultRef,
			)
			if err != nil || result.Kind != ContentModelResult {
				return nil, loopReadIntegrity("model result content", err)
			}
			output, err := moduleapi.RestoreModelGenerateOutputV1(
				result.CanonicalBytes,
			)
			if err != nil {
				return nil, loopReadIntegrity("model result wire", err)
			}
			if output.ActionRequest == nil {
				expected = 1
			}
		}
		if count != expected {
			return nil, loopReadIntegrity(
				"model Attempt History cardinality",
				ErrAdmissionIntegrity,
			)
		}
	}
	return history, nil
}

func loopReadIntegrity(subject string, err error) error {
	if err == nil {
		err = ErrAdmissionIntegrity
	}
	return fmt.Errorf("%w: %s: %w", ErrLoopIntegrity, subject, err)
}

func cloneRunForLoop(run RunForLoop) RunForLoop {
	run.Manifest = cloneRunManifestForLoop(run.Manifest)
	run.ManifestCanonical = bytes.Clone(run.ManifestCanonical)
	run.MemberCanonical = bytes.Clone(run.MemberCanonical)
	run.Member.PortPlans = cloneLoopPortPlans(run.Member.PortPlans)
	run.Contents = append([]ContentRecord(nil), run.Contents...)
	for index := range run.Contents {
		run.Contents[index] = cloneContentRecord(run.Contents[index])
	}
	run.History = append([]HistoryEntryRecord(nil), run.History...)
	for index := range run.History {
		run.History[index].Content =
			cloneContentRecord(run.History[index].Content)
	}
	run.ConversationHistory = append(
		[]ConversationHistoryTurnRecord(nil),
		run.ConversationHistory...,
	)
	for index := range run.ConversationHistory {
		run.ConversationHistory[index].UserContent = cloneContentRecord(
			run.ConversationHistory[index].UserContent,
		)
		run.ConversationHistory[index].AssistantContent = cloneContentRecord(
			run.ConversationHistory[index].AssistantContent,
		)
		if run.ConversationHistory[index].SourceContextCompilation != nil {
			compilation := cloneContentRecord(
				*run.ConversationHistory[index].SourceContextCompilation,
			)
			run.ConversationHistory[index].SourceContextCompilation =
				&compilation
		}
	}
	run.ModelDispatches = append(
		[]ModelDispatchRecord(nil),
		run.ModelDispatches...,
	)
	for index := range run.ModelDispatches {
		run.ModelDispatches[index] =
			cloneModelDispatchRecord(run.ModelDispatches[index])
	}
	run.ActionDispatches = append(
		[]ActionDispatchRecord(nil),
		run.ActionDispatches...,
	)
	for index := range run.ActionDispatches {
		run.ActionDispatches[index] =
			cloneActionDispatchRecord(run.ActionDispatches[index])
	}
	if run.ChannelIngress != nil {
		receipt := *run.ChannelIngress
		run.ChannelIngress = &receipt
	}
	if run.ChannelIngressEnvelope != nil {
		envelope := cloneContentRecord(*run.ChannelIngressEnvelope)
		run.ChannelIngressEnvelope = &envelope
	}
	run.ChannelDispatches = append(
		[]ChannelDispatchRecord(nil),
		run.ChannelDispatches...,
	)
	for index := range run.ChannelDispatches {
		run.ChannelDispatches[index] =
			cloneChannelDispatchRecord(run.ChannelDispatches[index])
	}
	run.CompositeChildren = append(
		[]CompositeChildResultRecordV1(nil),
		run.CompositeChildren...,
	)
	for index := range run.CompositeChildren {
		run.CompositeChildren[index].OutputCanonical = bytes.Clone(
			run.CompositeChildren[index].OutputCanonical,
		)
		run.CompositeChildren[index].ContributionCanonical = bytes.Clone(
			run.CompositeChildren[index].ContributionCanonical,
		)
		if run.CompositeChildren[index].Contribution != nil {
			contribution := cloneSpecialistContributionForLoop(
				*run.CompositeChildren[index].Contribution,
			)
			run.CompositeChildren[index].Contribution = &contribution
		}
		if run.CompositeChildren[index].WorkspaceTransfer != nil {
			transfer := cloneWorkspaceTransferRecordV1(
				*run.CompositeChildren[index].WorkspaceTransfer,
			)
			run.CompositeChildren[index].WorkspaceTransfer = &transfer
		}
	}
	if run.CompositeRoot != nil {
		root := cloneRunManifestForLoop(*run.CompositeRoot)
		run.CompositeRoot = &root
	}
	if run.CompositeReviewer != nil {
		reviewer := cloneCompositeReviewerResultForLoop(
			*run.CompositeReviewer,
		)
		run.CompositeReviewer = &reviewer
	}
	run.CompositeDecisionFrontier = cloneCompositeDecisionFrontier(
		run.CompositeDecisionFrontier,
	)
	if run.CompositeContributionSet != nil {
		set := cloneCollaborationContributionSetForLoop(
			*run.CompositeContributionSet,
		)
		run.CompositeContributionSet = &set
	}
	if run.CompositePreviousContributionSet != nil {
		set := cloneCollaborationContributionSetForLoop(
			*run.CompositePreviousContributionSet,
		)
		run.CompositePreviousContributionSet = &set
	}
	if run.CompositeRepairVerdict != nil {
		verdict := cloneCollaborationReviewVerdictForLoop(
			*run.CompositeRepairVerdict,
		)
		run.CompositeRepairVerdict = &verdict
	}
	if run.CompositeRepairVerdictResult != nil {
		reviewer := cloneCompositeReviewerResultForLoop(
			*run.CompositeRepairVerdictResult,
		)
		run.CompositeRepairVerdictResult = &reviewer
	}
	if run.CompositeRepairBasis != nil {
		basis := *run.CompositeRepairBasis
		run.CompositeRepairBasis = &basis
	}
	run.CompositeRepairBasisCanonical = bytes.Clone(
		run.CompositeRepairBasisCanonical,
	)
	if run.WorkspaceTransfer != nil {
		material := cloneWorkspaceTransferRecoveryMaterialV1(
			*run.WorkspaceTransfer,
		)
		run.WorkspaceTransfer = &material
	}
	if run.CancellationRequest != nil {
		request := *run.CancellationRequest
		run.CancellationRequest = &request
	}
	run.Frame.Continuation = bytes.Clone(run.Frame.Continuation)
	return run
}

func cloneSpecialistContributionForLoop(
	input corecontract.SpecialistContributionV1,
) corecontract.SpecialistContributionV1 {
	input.Evidence = append(
		[]corecontract.SpecialistEvidenceV1(nil),
		input.Evidence...,
	)
	input.Assumptions = append([]string(nil), input.Assumptions...)
	input.Risks = append([]string(nil), input.Risks...)
	input.Conflicts = append([]string(nil), input.Conflicts...)
	return input
}

func cloneCollaborationContributionSetForLoop(
	input corecontract.CollaborationContributionSetV1,
) corecontract.CollaborationContributionSetV1 {
	input.Contributions = append(
		[]corecontract.CollaborationContributionEntryV1(nil),
		input.Contributions...,
	)
	return input
}

func cloneCollaborationReviewVerdictForLoop(
	input corecontract.CollaborationReviewVerdictV1,
) corecontract.CollaborationReviewVerdictV1 {
	input.IssueCodes = append(
		[]corecontract.ReviewIssueCodeV1{},
		input.IssueCodes...,
	)
	input.AffectedSlotIDs = append([]string{}, input.AffectedSlotIDs...)
	return input
}

func cloneCompositeReviewerResultForLoop(
	input CompositeReviewerResultRecordV1,
) CompositeReviewerResultRecordV1 {
	input.OutputCanonical = bytes.Clone(input.OutputCanonical)
	input.VerdictCanonical = bytes.Clone(input.VerdictCanonical)
	if input.Verdict != nil {
		verdict := *input.Verdict
		verdict.IssueCodes = append(
			[]corecontract.ReviewIssueCodeV1(nil),
			verdict.IssueCodes...,
		)
		verdict.AffectedSlotIDs = append(
			[]string(nil),
			verdict.AffectedSlotIDs...,
		)
		input.Verdict = &verdict
	}
	if input.ContributionSet != nil {
		set := cloneCollaborationContributionSetForLoop(
			*input.ContributionSet,
		)
		input.ContributionSet = &set
	}
	if input.CollaborationVerdict != nil {
		verdict := cloneCollaborationReviewVerdictForLoop(
			*input.CollaborationVerdict,
		)
		input.CollaborationVerdict = &verdict
	}
	return input
}

func cloneWorkspaceTransferRecoveryMaterialV1(
	input WorkspaceTransferRecoveryMaterialV1,
) WorkspaceTransferRecoveryMaterialV1 {
	input.RootManifest = cloneRunManifestForLoop(input.RootManifest)
	input.ChildManifest = cloneRunManifestForLoop(input.ChildManifest)
	input.RootGrant.SendPayloadKinds = append(
		[]corecontract.WorkspaceTransferPayloadKindV1(nil),
		input.RootGrant.SendPayloadKinds...,
	)
	input.RootGrant.ReceivePayloadKinds = append(
		[]corecontract.WorkspaceTransferPayloadKindV1(nil),
		input.RootGrant.ReceivePayloadKinds...,
	)
	input.TargetGrant.SendPayloadKinds = append(
		[]corecontract.WorkspaceTransferPayloadKindV1(nil),
		input.TargetGrant.SendPayloadKinds...,
	)
	input.TargetGrant.ReceivePayloadKinds = append(
		[]corecontract.WorkspaceTransferPayloadKindV1(nil),
		input.TargetGrant.ReceivePayloadKinds...,
	)
	input.RootGrantCanonical = bytes.Clone(input.RootGrantCanonical)
	input.TargetGrantCanonical = bytes.Clone(input.TargetGrantCanonical)
	input.RootTaskInput = cloneContentRecord(input.RootTaskInput)
	if input.PreviousContributionSet != nil {
		set := cloneCollaborationContributionSetForLoop(
			*input.PreviousContributionSet,
		)
		input.PreviousContributionSet = &set
	}
	if input.RepairVerdict != nil {
		verdict := cloneCollaborationReviewVerdictForLoop(
			*input.RepairVerdict,
		)
		input.RepairVerdict = &verdict
	}
	if input.RepairBasis != nil {
		basis := *input.RepairBasis
		input.RepairBasis = &basis
	}
	input.RepairBasisCanonical = bytes.Clone(input.RepairBasisCanonical)
	return input
}

func cloneRunManifestForLoop(
	manifest corecontract.RunManifest,
) corecontract.RunManifest {
	cloned := manifest
	cloned.Members = append(
		[]corecontract.MemberSnapshotRef(nil),
		manifest.Members...,
	)
	if manifest.Composite != nil {
		node := *manifest.Composite
		if manifest.Composite.Assignment != nil {
			assignment := *manifest.Composite.Assignment
			node.Assignment = &assignment
		}
		if manifest.Composite.Plan != nil {
			plan := *manifest.Composite.Plan
			plan.Children = append(
				[]corecontract.CompositeChildRunRefV1(nil),
				manifest.Composite.Plan.Children...,
			)
			for index := range plan.Children {
				if plan.Children[index].Transfer != nil {
					transfer := *plan.Children[index].Transfer
					plan.Children[index].Transfer = &transfer
				}
			}
			if manifest.Composite.Plan.Reviewer != nil {
				reviewer := *manifest.Composite.Plan.Reviewer
				plan.Reviewer = &reviewer
			}
			if manifest.Composite.Plan.Decision != nil {
				decision := *manifest.Composite.Plan.Decision
				decision.RepairChildren = append(
					[]corecontract.CompositeChildRunRefV1(nil),
					manifest.Composite.Plan.Decision.RepairChildren...,
				)
				for index := range decision.RepairChildren {
					if decision.RepairChildren[index].Transfer != nil {
						transfer := *decision.RepairChildren[index].Transfer
						decision.RepairChildren[index].Transfer = &transfer
					}
				}
				plan.Decision = &decision
			}
			node.Plan = &plan
		}
		cloned.Composite = &node
	}
	return cloned
}

func cloneLoopPortPlans(
	plans []moduleapi.PortPlan,
) []moduleapi.PortPlan {
	cloned := make([]moduleapi.PortPlan, len(plans))
	for planIndex, plan := range plans {
		cloned[planIndex] = plan
		cloned[planIndex].Bindings = make(
			[]moduleapi.PortBinding,
			len(plan.Bindings),
		)
		for bindingIndex, binding := range plan.Bindings {
			binding.StaticContextRefs = append(
				[]string{},
				binding.StaticContextRefs...,
			)
			cloned[planIndex].Bindings[bindingIndex] = binding
		}
	}
	return cloned
}
