package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type CompositeChildStateV1 string

const (
	CompositeChildPendingV1   CompositeChildStateV1 = "PENDING"
	CompositeChildUnknownV1   CompositeChildStateV1 = "UNKNOWN"
	CompositeChildSucceededV1 CompositeChildStateV1 = "SUCCEEDED"
	CompositeChildFailedV1    CompositeChildStateV1 = "FAILED"
	CompositeChildSkippedV1   CompositeChildStateV1 = "SKIPPED"
)

// CompositeChildResultRecordV1 is a read-only projection. The result remains
// owned by the Child Run; the root receives canonical bytes as context and
// never copies them into its History.
type CompositeChildResultRecordV1 struct {
	SlotID                string
	RepairRound           uint32
	RunID                 string
	ManifestDigest        string
	MemberSnapshotDigest  string
	Assignment            corecontract.CompositeAssignmentV1
	RunRevision           uint64
	FrameRevision         uint64
	FrameStep             string
	State                 CompositeChildStateV1
	AttemptID             string
	ResultRef             string
	OutputCanonical       []byte
	ContributionCanonical []byte
	ContributionDigest    string
	Contribution          *corecontract.SpecialistContributionV1
	WorkspaceTransfer     *WorkspaceTransferRecordV1
	ErrorClassification   string
}

type CompositeReviewerStateV1 string

const (
	CompositeReviewerPendingV1   CompositeReviewerStateV1 = "PENDING"
	CompositeReviewerUnknownV1   CompositeReviewerStateV1 = "UNKNOWN"
	CompositeReviewerSucceededV1 CompositeReviewerStateV1 = "SUCCEEDED"
	CompositeReviewerFailedV1    CompositeReviewerStateV1 = "FAILED"
	CompositeReviewerSkippedV1   CompositeReviewerStateV1 = "SKIPPED"
)

// CompositeReviewerResultRecordV1 is the root's read-only projection of its
// optional Reviewer. The ReviewVerdict remains owned by the Reviewer's exact
// MODEL_RESULT; no second result row or queue is introduced.
type CompositeReviewerResultRecordV1 struct {
	RepairRound           uint32
	RunID                 string
	ManifestDigest        string
	MemberSnapshotDigest  string
	RunRevision           uint64
	FrameRevision         uint64
	FrameStep             string
	State                 CompositeReviewerStateV1
	AttemptID             string
	ResultRef             string
	OutputCanonical       []byte
	VerdictCanonical      []byte
	Verdict               *corecontract.ReviewVerdictV1
	ContributionSet       *corecontract.CollaborationContributionSetV1
	ContributionSetDigest string
	CollaborationVerdict  *corecontract.CollaborationReviewVerdictV1
	ErrorClassification   string
}

func loadCompositeChildResults(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
) ([]CompositeChildResultRecordV1, error) {
	if root.Composite == nil || root.Composite.Plan == nil {
		return nil, loopReadIntegrity(
			"composite root plan",
			ErrAdmissionIntegrity,
		)
	}
	return loadCompositeChildResultsForRefs(
		ctx,
		connection,
		root,
		root.Composite.Plan.Children,
		make([]uint32, len(root.Composite.Plan.Children)),
	)
}

func loadCompositeChildResultsForRefs(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
	plannedChildren []corecontract.CompositeChildRunRefV1,
	repairRounds []uint32,
) ([]CompositeChildResultRecordV1, error) {
	if root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil ||
		len(plannedChildren) != len(root.Composite.Plan.Children) ||
		len(repairRounds) != len(plannedChildren) {
		return nil, loopReadIntegrity(
			"composite root plan",
			ErrAdmissionIntegrity,
		)
	}
	if _, err := loadCompositeFamilyLatchRows(ctx, connection, root); err != nil {
		return nil, err
	}
	if err := validatePersistedCompositeWorkspaceTransferClosure(
		ctx,
		connection,
		root,
	); err != nil {
		return nil, loopReadIntegrity(
			"composite Workspace transfer Admission closure",
			err,
		)
	}
	results := make(
		[]CompositeChildResultRecordV1,
		len(plannedChildren),
	)
	for index, planned := range plannedChildren {
		repairRound := repairRounds[index]
		if repairRound > corecontract.CompositeRepairRoundOneV1 {
			return nil, loopReadIntegrity(
				"composite Child repair round",
				ErrAdmissionIntegrity,
			)
		}
		parentSlotID := planned.SlotID
		if repairRound == corecontract.CompositeRepairRoundOneV1 {
			parentSlotID = planned.ParentSlotID
		}
		expectedWorkspace, workspaceErr := compositeChildWorkspaceForPlan(
			root,
			planned,
		)
		if workspaceErr != nil {
			return nil, loopReadIntegrity(
				"composite Child Workspace plan",
				workspaceErr,
			)
		}
		var (
			tenantID           string
			workspaceID        string
			admissionKey       string
			intentDigest       string
			parentRunID        sql.NullString
			parentManifest     sql.NullString
			parentSlot         sql.NullString
			runState           string
			runDisposition     sql.NullString
			runRevision        int64
			manifestCanonical  []byte
			manifestDigest     string
			memberDigest       string
			memberCount        int
			frameRevision      int64
			frameStep          string
			frameWaitingReason sql.NullString
		)
		if err := connection.QueryRowContext(ctx, `
			SELECT
				tenant_id, workspace_id, admission_key,
				admission_intent_digest,
				parent_run_id, parent_manifest_digest, parent_slot_id,
				state, disposition, revision
			FROM runs
			WHERE run_id=?
		`, planned.RunID).Scan(
			&tenantID,
			&workspaceID,
			&admissionKey,
			&intentDigest,
			&parentRunID,
			&parentManifest,
			&parentSlot,
			&runState,
			&runDisposition,
			&runRevision,
		); err != nil {
			return nil, loopReadIntegrity("composite Child Run", err)
		}
		if tenantID != root.TenantID || workspaceID != expectedWorkspace.ID ||
			admissionKey != planned.AdmissionKey ||
			!parentRunID.Valid || parentRunID.String != root.RunID ||
			!parentManifest.Valid || parentManifest.String != root.ManifestDigest ||
			!parentSlot.Valid || parentSlot.String != parentSlotID ||
			runRevision < 0 {
			return nil, loopReadIntegrity(
				"composite Child Run projection",
				ErrAdmissionIntegrity,
			)
		}
		closure, err := loadAdmissionClosure(
			ctx,
			connection,
			planned.RunID,
			tenantID,
			admissionKey,
			intentDigest,
			workspaceID,
		)
		if err != nil {
			return nil, err
		}
		if err := connection.QueryRowContext(ctx, `
			SELECT canonical_json, digest
			FROM run_manifests
			WHERE run_id=?
		`, planned.RunID).Scan(
			&manifestCanonical,
			&manifestDigest,
		); err != nil {
			return nil, loopReadIntegrity("composite Child Manifest", err)
		}
		manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
		if err != nil || manifest.ManifestDigest != manifestDigest ||
			closure.ManifestDigest != manifestDigest ||
			manifest.Composite == nil ||
			manifest.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
			manifest.Composite.RepairRound != repairRound ||
			manifest.Composite.RootRunID != root.RunID ||
			manifest.Composite.ParentManifestDigest != root.ManifestDigest ||
			manifest.Composite.ParentSlotID != parentSlotID ||
			manifest.Composite.Assignment == nil ||
			*manifest.Composite.Assignment != planned.Assignment ||
			manifest.PrimaryAgent != planned.Agent ||
			manifest.TaskInputRef != planned.TaskInputRef ||
			manifest.Workspace != expectedWorkspace {
			return nil, loopReadIntegrity(
				"composite Child Manifest closure",
				ErrAdmissionIntegrity,
			)
		}
		if err := connection.QueryRowContext(ctx, `
			SELECT COUNT(*), MIN(digest)
			FROM member_execution_snapshots
			WHERE run_id=?
		`, planned.RunID).Scan(&memberCount, &memberDigest); err != nil ||
			memberCount != 1 ||
			memberDigest != planned.MemberSnapshotDigest ||
			closure.MemberSnapshotDigest != memberDigest {
			return nil, loopReadIntegrity(
				"composite Child member closure",
				err,
			)
		}
		if err := connection.QueryRowContext(ctx, `
			SELECT frame_revision, step, waiting_reason
			FROM loop_frames
			WHERE run_id=?
		`, planned.RunID).Scan(
			&frameRevision,
			&frameStep,
			&frameWaitingReason,
		); err != nil || frameRevision < 0 {
			return nil, loopReadIntegrity("composite Child Frame", err)
		}

		result := CompositeChildResultRecordV1{
			SlotID:               planned.SlotID,
			RepairRound:          repairRound,
			RunID:                planned.RunID,
			ManifestDigest:       manifestDigest,
			MemberSnapshotDigest: memberDigest,
			Assignment:           planned.Assignment,
			RunRevision:          uint64(runRevision),
			FrameRevision:        uint64(frameRevision),
			FrameStep:            frameStep,
		}
		switch frameStep {
		case corecontract.InitialLoopStep,
			corecontract.ModelPendingLoopStep:
			if runState != corecontract.InitialRunState || runDisposition.Valid ||
				frameWaitingReason.Valid {
				return nil, loopReadIntegrity(
					"active composite Child projection",
					ErrAdmissionIntegrity,
				)
			}
			result.State = CompositeChildPendingV1
		case corecontract.WaitingRepairActivationLoopStep:
			if repairRound != corecontract.CompositeRepairRoundOneV1 ||
				runState != corecontract.InitialRunState ||
				!runDisposition.Valid ||
				runDisposition.String != "WAITING_EXTERNAL" ||
				!frameWaitingReason.Valid ||
				frameWaitingReason.String != compositeRepairDormantWaitingReason {
				return nil, loopReadIntegrity(
					"dormant composite repair Child projection",
					ErrAdmissionIntegrity,
				)
			}
			result.State = CompositeChildPendingV1
		case corecontract.WaitingReconciliationLoopStep:
			if runState != corecontract.WaitingReconciliationLoopStep ||
				!runDisposition.Valid ||
				runDisposition.String != corecontract.WaitingReconciliationLoopStep ||
				!frameWaitingReason.Valid {
				return nil, loopReadIntegrity(
					"unknown composite Child projection",
					ErrAdmissionIntegrity,
				)
			}
			result.State = CompositeChildUnknownV1
			result.ErrorClassification = frameWaitingReason.String
		case corecontract.TerminatedLoopStep:
			terminal, err := loadTerminalRunResult(ctx, connection, planned.RunID)
			if err != nil {
				return nil, err
			}
			result.AttemptID = terminal.AttemptID
			result.FrameRevision = terminal.FrameRevision
			result.ErrorClassification = terminal.ErrorClassification
			if terminal.AttemptKind == "" &&
				terminal.ErrorClassification ==
					corecontract.CompositeRepairSkippedReasonV1 {
				result.State = CompositeChildSkippedV1
				break
			}
			switch terminal.State {
			case corecontract.ModelAttemptSucceeded:
				if terminal.AttemptKind != corecontract.AttemptKindModel ||
					len(terminal.OutputCanonical) == 0 ||
					terminal.Output.ActionRequest != nil {
					result.State = CompositeChildFailedV1
					result.ErrorClassification =
						"COMPOSITE_CHILD_OUTPUT_NOT_MERGEABLE"
					break
				}
				resultRef, err := ComputeContentDigest(
					ContentModelResult,
					admissionJSONMediaType,
					terminal.OutputCanonical,
				)
				if err != nil {
					return nil, loopReadIntegrity(
						"composite Child result digest",
						err,
					)
				}
				stored, err := queryContent(ctx, connection, resultRef)
				if err != nil || stored.Kind != ContentModelResult ||
					!bytes.Equal(stored.CanonicalBytes, terminal.OutputCanonical) {
					return nil, loopReadIntegrity(
						"composite Child result content",
						err,
					)
				}
				result.State = CompositeChildSucceededV1
				result.ResultRef = resultRef
				result.OutputCanonical = bytes.Clone(terminal.OutputCanonical)
				if root.Composite.Plan.Decision != nil {
					contribution, canonical, digest, parseErr :=
						corecontract.ParseSpecialistContributionV1(
							[]byte(terminal.Output.AssistantText),
						)
					if parseErr != nil ||
						!bytes.Equal(
							canonical,
							[]byte(terminal.Output.AssistantText),
						) || validateSpecialistEvidenceClosure(
						contribution,
						terminal.AttemptID,
						ctx,
						connection,
					) != nil {
						result.State = CompositeChildFailedV1
						result.ErrorClassification =
							"COMPOSITE_SPECIALIST_CONTRIBUTION_INVALID"
						break
					}
					result.Contribution = &contribution
					result.ContributionCanonical = bytes.Clone(canonical)
					result.ContributionDigest = digest
					if planned.Transfer != nil {
						transfer, transferErr :=
							loadCompositeChildWorkspaceTransferResultV1(
								ctx,
								connection,
								root,
								manifest,
								stored,
							)
						if transferErr != nil || transfer == nil {
							return nil, loopReadIntegrity(
								"composite Child Workspace transfer RESULT",
								transferErr,
							)
						}
						result.WorkspaceTransfer = transfer
					}
				}
			case corecontract.ModelAttemptFailed:
				result.State = CompositeChildFailedV1
			default:
				return nil, loopReadIntegrity(
					"terminal composite Child state",
					ErrAdmissionIntegrity,
				)
			}
		default:
			return nil, loopReadIntegrity(
				"unsupported composite Child Frame",
				ErrAdmissionIntegrity,
			)
		}
		results[index] = result
	}
	return results, nil
}

func loadCompositeChildWorkspaceTransferResultV1(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
	child corecontract.RunManifest,
	payload ContentRecord,
) (*WorkspaceTransferRecordV1, error) {
	rootClosure, err := loadCompositeParticipantClosureV1(
		ctx,
		connection,
		root.RunID,
	)
	if err != nil {
		return nil, fmt.Errorf("Workspace transfer root closure: %w", err)
	}
	if rootClosure.Manifest.ManifestDigest != root.ManifestDigest {
		return nil, fmt.Errorf(
			"Workspace transfer root closure: %w",
			ErrAdmissionIntegrity,
		)
	}
	childClosure, err := loadCompositeParticipantClosureV1(
		ctx,
		connection,
		child.RunID,
	)
	if err != nil {
		return nil, fmt.Errorf("Workspace transfer Child closure: %w", err)
	}
	if childClosure.Manifest.ManifestDigest != child.ManifestDigest {
		return nil, fmt.Errorf(
			"Workspace transfer Child closure: %w",
			ErrAdmissionIntegrity,
		)
	}
	var previousSet *corecontract.CollaborationContributionSetV1
	var previousSetDigest string
	var repairVerdict *corecontract.CollaborationReviewVerdictV1
	var repairVerdictRef string
	var repairBasis *corecontract.WorkspaceTaskSummaryV1
	var repairBasisCanonical []byte
	if child.Composite.RepairRound == corecontract.CompositeRepairRoundOneV1 {
		initial, loadErr := loadCompositeChildResults(ctx, connection, root)
		if loadErr != nil {
			return nil, loadErr
		}
		set, setDigest, setErr := buildCollaborationContributionSet(
			ctx,
			connection,
			root,
			initial,
			0,
		)
		if setErr != nil {
			return nil, setErr
		}
		previousSet = &set
		previousSetDigest = setDigest
		reviewer, reviewerErr := loadCompositeReviewerResultForRound(
			ctx,
			connection,
			root,
			initial,
			0,
		)
		if reviewerErr != nil {
			return nil, fmt.Errorf(
				"Workspace transfer repair verdict closure: %w",
				reviewerErr,
			)
		}
		if reviewer == nil ||
			reviewer.CollaborationVerdict == nil || reviewer.ResultRef == "" {
			return nil, fmt.Errorf(
				"Workspace transfer repair verdict closure: %w",
				ErrAdmissionIntegrity,
			)
		}
		verdict := *reviewer.CollaborationVerdict
		repairVerdict = &verdict
		repairVerdictRef = reviewer.ResultRef
		basis, canonical, basisErr := buildCompositeRepairTaskSummary(
			ctx,
			connection,
			root,
			child.Composite.Assignment.SlotID,
		)
		if basisErr != nil {
			return nil, basisErr
		}
		repairBasis = &basis
		repairBasisCanonical = canonical
	}
	material, err := loadWorkspaceTransferRecoveryMaterialV1(
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
	if err != nil || material == nil {
		return nil, err
	}
	return loadWorkspaceTransferResultV1(
		ctx,
		connection,
		RunForLoop{
			RunID:             child.RunID,
			Manifest:          child,
			WorkspaceTransfer: material,
		},
		payload,
	)
}

func allCompositeChildrenSucceeded(
	children []CompositeChildResultRecordV1,
) bool {
	if len(children) < corecontract.CompositeMinChildrenV1 {
		return false
	}
	for _, child := range children {
		if child.State != CompositeChildSucceededV1 {
			return false
		}
	}
	return true
}

func loadCompositeEffectiveChildResults(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
) ([]CompositeChildResultRecordV1, uint32, error) {
	initial, err := loadCompositeChildResults(ctx, connection, root)
	if err != nil || root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil {
		return initial, 0, err
	}
	reviewer, err := loadCompositeReviewerResultForRound(
		ctx,
		connection,
		root,
		initial,
		0,
	)
	if err != nil || reviewer == nil ||
		reviewer.State != CompositeReviewerSucceededV1 ||
		reviewer.CollaborationVerdict == nil || reviewer.ResultRef == "" {
		return initial, 0, err
	}
	transitions, _, err := compositeRepairTransitions(
		root,
		*reviewer.CollaborationVerdict,
		reviewer.ResultRef,
	)
	if err != nil {
		return nil, 0, err
	}
	applied, err := inspectCompositeRepairTransitionEvents(
		ctx,
		connection,
		root,
		reviewer.ResultRef,
		transitions,
	)
	if err != nil || !applied ||
		reviewer.CollaborationVerdict.Decision !=
			corecontract.CollaborationReviewDecisionRepairRequiredV1 {
		return initial, 0, err
	}
	affected := make(
		map[string]struct{},
		len(reviewer.CollaborationVerdict.AffectedSlotIDs),
	)
	for _, slotID := range reviewer.CollaborationVerdict.AffectedSlotIDs {
		affected[slotID] = struct{}{}
	}
	selected := append(
		[]corecontract.CompositeChildRunRefV1(nil),
		root.Composite.Plan.Children...,
	)
	rounds := make([]uint32, len(selected))
	for index, repair := range root.Composite.Plan.Decision.RepairChildren {
		if _, ok := affected[repair.SlotID]; !ok {
			continue
		}
		selected[index] = repair
		rounds[index] = corecontract.CompositeRepairRoundOneV1
	}
	effective, err := loadCompositeChildResultsForRefs(
		ctx,
		connection,
		root,
		selected,
		rounds,
	)
	if err != nil {
		return nil, 0, err
	}
	return effective, corecontract.CompositeRepairRoundOneV1, nil
}

func validateSpecialistEvidenceClosure(
	contribution corecontract.SpecialistContributionV1,
	attemptID string,
	ctx context.Context,
	connection readQueryerV1,
) error {
	var (
		compilationRef sql.NullString
		requestRef     string
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT context_compilation_ref, request_ref
		FROM model_dispatch_attempts
		WHERE attempt_id=? AND state='SUCCEEDED'
	`, attemptID).Scan(&compilationRef, &requestRef); err != nil {
		return loopReadIntegrity(
			"Specialist contribution Attempt compilation",
			err,
		)
	}
	if !compilationRef.Valid {
		if len(contribution.Evidence) != 0 {
			return loopReadIntegrity(
				"Specialist contribution evidence without compilation",
				ErrAdmissionIntegrity,
			)
		}
		return nil
	}
	record, err := queryContent(ctx, connection, compilationRef.String)
	if err != nil || record.Kind != ContentContextCompilation ||
		record.MediaType != admissionJSONMediaType {
		return loopReadIntegrity(
			"Specialist contribution compilation content",
			err,
		)
	}
	computed, err := ComputeContentDigest(
		record.Kind,
		record.MediaType,
		record.CanonicalBytes,
	)
	if err != nil || computed != compilationRef.String {
		return loopReadIntegrity(
			"Specialist contribution compilation digest",
			err,
		)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		record.CanonicalBytes,
	)
	if err != nil {
		return loopReadIntegrity(
			"Specialist contribution compilation wire",
			err,
		)
	}
	requestRecord, err := queryContent(ctx, connection, requestRef)
	if err != nil || requestRecord.Kind != ContentModelRequest ||
		requestRecord.MediaType != admissionJSONMediaType ||
		compilation.FinalRequestDigest != requestRef {
		return loopReadIntegrity(
			"Specialist contribution exact model request",
			err,
		)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		requestRecord.CanonicalBytes,
	)
	if err != nil {
		return loopReadIntegrity(
			"Specialist contribution Knowledge prompt projection",
			err,
		)
	}
	if err := validateDynamicContextMessageOrder(
		compilation,
		request,
	); err != nil {
		return loopReadIntegrity(
			"Specialist contribution Knowledge prompt projection",
			err,
		)
	}
	allowed := make(map[string]struct{})
	addRetrieval := func(evidence corecontract.KnowledgeRetrievalEvidenceV1) {
		allowed[evidence.Source.Digest] = struct{}{}
		for _, hit := range evidence.Hits {
			allowed[hit.Document.Digest] = struct{}{}
			allowed[hit.ChunkDigest] = struct{}{}
		}
	}
	for _, evidence := range compilation.KnowledgeRetrievals {
		addRetrieval(evidence)
	}
	for _, evidence := range compilation.KnowledgeReuses {
		addRetrieval(evidence.FreshRetrieval)
	}
	for _, evidence := range contribution.Evidence {
		if _, ok := allowed[evidence.Ref]; !ok {
			return loopReadIntegrity(
				"Specialist contribution evidence provenance",
				ErrAdmissionIntegrity,
			)
		}
	}
	return nil
}

func buildCompositeRepairTaskSummary(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
	slotID string,
) (corecontract.WorkspaceTaskSummaryV1, []byte, error) {
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil {
		return corecontract.WorkspaceTaskSummaryV1{}, nil,
			loopReadIntegrity(
				"Composite repair task-summary root",
				ErrAdmissionIntegrity,
			)
	}
	initial, err := loadCompositeChildResults(ctx, connection, root)
	if err != nil || !allCompositeChildrenSucceeded(initial) {
		return corecontract.WorkspaceTaskSummaryV1{}, nil,
			loopReadIntegrity(
				"Composite repair task-summary contributions",
				err,
			)
	}
	set, setDigest, err := buildCollaborationContributionSet(
		ctx,
		connection,
		root,
		initial,
		0,
	)
	if err != nil {
		return corecontract.WorkspaceTaskSummaryV1{}, nil, err
	}
	reviewer, err := loadCompositeReviewerResultForRound(
		ctx,
		connection,
		root,
		initial,
		0,
	)
	if err != nil || reviewer == nil || reviewer.ResultRef == "" ||
		reviewer.CollaborationVerdict == nil ||
		reviewer.CollaborationVerdict.Decision !=
			corecontract.CollaborationReviewDecisionRepairRequiredV1 {
		return corecontract.WorkspaceTaskSummaryV1{}, nil,
			loopReadIntegrity(
				"Composite repair task-summary verdict",
				err,
			)
	}
	var contribution *corecontract.SpecialistContributionV1
	for index := range initial {
		if initial[index].SlotID == slotID {
			contribution = initial[index].Contribution
			break
		}
	}
	affected := false
	for _, candidate := range reviewer.CollaborationVerdict.AffectedSlotIDs {
		if candidate == slotID {
			affected = true
			break
		}
	}
	if contribution == nil || !affected {
		return corecontract.WorkspaceTaskSummaryV1{}, nil,
			loopReadIntegrity(
				"Composite repair task-summary affected slot",
				ErrAdmissionIntegrity,
			)
	}
	type repairSummaryEnvelope struct {
		SchemaVersion        string                                `json:"schema_version"`
		SlotID               string                                `json:"slot_id"`
		PreviousContribution corecontract.SpecialistContributionV1 `json:"previous_contribution"`
		IssueCodes           []corecontract.ReviewIssueCodeV1      `json:"issue_codes"`
		BoundedReason        string                                `json:"bounded_reason"`
	}
	raw, err := json.Marshal(repairSummaryEnvelope{
		SchemaVersion:        "composite-repair-basis-summary/v1",
		SlotID:               slotID,
		PreviousContribution: *contribution,
		IssueCodes: append(
			[]corecontract.ReviewIssueCodeV1(nil),
			reviewer.CollaborationVerdict.IssueCodes...,
		),
		BoundedReason: reviewer.CollaborationVerdict.BoundedReason,
	})
	if err != nil {
		return corecontract.WorkspaceTaskSummaryV1{}, nil,
			loopReadIntegrity("Composite repair summary marshal", err)
	}
	summaryCanonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		return corecontract.WorkspaceTaskSummaryV1{}, nil,
			loopReadIntegrity("Composite repair summary canonical", err)
	}
	frozen, canonical, err := corecontract.NewWorkspaceTaskSummaryV1(
		corecontract.WorkspaceTaskSummaryV1{
			SchemaVersion:      corecontract.WorkspaceTaskSummarySchemaVersionV1,
			SourceTaskInputRef: root.TaskInputRef,
			RepairRound:        corecontract.CompositeRepairRoundOneV1,
			PreviousSetDigest:  setDigest,
			VerdictRef:         reviewer.ResultRef,
			Summary:            string(summaryCanonical),
		},
	)
	if err != nil || frozen.PreviousSetDigest != setDigest ||
		frozen.PreviousSetDigest == "" || set.RepairRound != 0 {
		return corecontract.WorkspaceTaskSummaryV1{}, nil,
			loopReadIntegrity("Composite repair task-summary wire", err)
	}
	return frozen, canonical, nil
}

func compositeRepairVerdictAffectsSlotV1(
	verdict *corecontract.CollaborationReviewVerdictV1,
	slotID string,
) bool {
	if verdict == nil || verdict.Decision !=
		corecontract.CollaborationReviewDecisionRepairRequiredV1 || slotID == "" {
		return false
	}
	for _, affected := range verdict.AffectedSlotIDs {
		if affected == slotID {
			return true
		}
	}
	return false
}

func buildCompositeSpecialistResultSet(
	root corecontract.RunManifest,
	children []CompositeChildResultRecordV1,
) (corecontract.CompositeSpecialistResultSetV1, []byte, string, error) {
	if root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil ||
		len(children) != len(root.Composite.Plan.Children) {
		return corecontract.CompositeSpecialistResultSetV1{}, nil, "",
			loopReadIntegrity(
				"composite Specialist result-set root",
				ErrAdmissionIntegrity,
			)
	}
	results := make(
		[]corecontract.CompositeSpecialistResultV1,
		len(children),
	)
	for index, planned := range root.Composite.Plan.Children {
		child := children[index]
		if child.State != CompositeChildSucceededV1 ||
			child.SlotID != planned.SlotID ||
			child.RunID != planned.RunID ||
			child.ManifestDigest == "" ||
			child.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
			child.Assignment != planned.Assignment ||
			child.ResultRef == "" ||
			child.RunRevision == 0 || child.FrameRevision == 0 {
			return corecontract.CompositeSpecialistResultSetV1{}, nil, "",
				loopReadIntegrity(
					"composite Specialist result-set input",
					ErrAdmissionIntegrity,
				)
		}
		results[index] = corecontract.CompositeSpecialistResultV1{
			SlotID:                planned.SlotID,
			FocusID:               planned.Assignment.FocusID,
			WeightBasisPoints:     planned.Assignment.WeightBasisPoints,
			RunID:                 planned.RunID,
			ManifestDigest:        child.ManifestDigest,
			MemberSnapshotDigest:  planned.MemberSnapshotDigest,
			ResultRef:             child.ResultRef,
			TerminalRunRevision:   child.RunRevision,
			TerminalFrameRevision: child.FrameRevision,
		}
	}
	frozen, canonical, digest, err :=
		corecontract.NewCompositeSpecialistResultSetV1(
			corecontract.CompositeSpecialistResultSetV1{
				SchemaVersion: corecontract.CompositeSpecialistResultSetSchemaVersionV1,
				FamilyDigest:  root.ManifestDigest,
				TaskInputRef:  root.TaskInputRef,
				Results:       results,
			},
		)
	if err != nil {
		return corecontract.CompositeSpecialistResultSetV1{}, nil, "",
			loopReadIntegrity("composite Specialist result set", err)
	}
	return frozen, canonical, digest, nil
}

func buildCollaborationContributionSet(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
	children []CompositeChildResultRecordV1,
	repairRound uint32,
) (corecontract.CollaborationContributionSetV1, string, error) {
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil || repairRound > 1 ||
		len(children) != len(root.Composite.Plan.Children) {
		return corecontract.CollaborationContributionSetV1{}, "",
			loopReadIntegrity(
				"collaboration contribution-set root",
				ErrAdmissionIntegrity,
			)
	}
	entries := make(
		[]corecontract.CollaborationContributionEntryV1,
		len(children),
	)
	for index, planned := range root.Composite.Plan.Children {
		child := children[index]
		if child.State != CompositeChildSucceededV1 ||
			child.SlotID != planned.SlotID ||
			child.RepairRound > repairRound ||
			child.ResultRef == "" || child.Contribution == nil ||
			child.ContributionDigest == "" {
			return corecontract.CollaborationContributionSetV1{}, "",
				loopReadIntegrity(
					"collaboration contribution-set child",
					ErrAdmissionIntegrity,
				)
		}
		entries[index] = corecontract.CollaborationContributionEntryV1{
			SlotID:             child.SlotID,
			RunID:              child.RunID,
			ResultRef:          child.ResultRef,
			ContributionDigest: child.ContributionDigest,
		}
	}
	input := corecontract.CollaborationContributionSetV1{
		SchemaVersion: corecontract.CollaborationContributionSetSchemaVersionV1,
		FamilyDigest:  root.ManifestDigest,
		RepairRound:   repairRound,
		Contributions: entries,
	}
	var (
		previous   corecontract.CollaborationContributionSetV1
		verdict    corecontract.CollaborationReviewVerdictV1
		verdictRef string
	)
	if repairRound == corecontract.CompositeRepairRoundOneV1 {
		initialChildren, err := loadCompositeChildResultsForRefs(
			ctx,
			connection,
			root,
			root.Composite.Plan.Children,
			make([]uint32, len(root.Composite.Plan.Children)),
		)
		if err != nil {
			return corecontract.CollaborationContributionSetV1{}, "", err
		}
		previous, _, err = buildCollaborationContributionSet(
			ctx,
			connection,
			root,
			initialChildren,
			0,
		)
		if err != nil {
			return corecontract.CollaborationContributionSetV1{}, "", err
		}
		_, _, previousDigest, err :=
			corecontract.NewCollaborationContributionSetV1(previous)
		if err != nil {
			return corecontract.CollaborationContributionSetV1{}, "",
				loopReadIntegrity(
					"initial collaboration contribution set",
					err,
				)
		}
		initialReviewer, err := loadCompositeReviewerResultForRound(
			ctx,
			connection,
			root,
			initialChildren,
			0,
		)
		if err != nil || initialReviewer == nil ||
			initialReviewer.State != CompositeReviewerSucceededV1 ||
			initialReviewer.CollaborationVerdict == nil ||
			initialReviewer.ResultRef == "" ||
			initialReviewer.CollaborationVerdict.Decision !=
				corecontract.CollaborationReviewDecisionRepairRequiredV1 {
			return corecontract.CollaborationContributionSetV1{}, "",
				loopReadIntegrity(
					"collaboration repair verdict lineage",
					err,
				)
		}
		verdict = *initialReviewer.CollaborationVerdict
		verdictRef = initialReviewer.ResultRef
		input.PreviousSetDigest = previousDigest
		input.VerdictRef = verdictRef
	}
	frozen, _, digest, err :=
		corecontract.NewCollaborationContributionSetV1(input)
	if err != nil {
		return corecontract.CollaborationContributionSetV1{}, "",
			loopReadIntegrity("collaboration contribution set", err)
	}
	if repairRound == 0 {
		err = frozen.ValidateForCollaborationRootV1(root)
	} else {
		err = frozen.ValidateForCollaborationRepairPlanV1(
			root,
			previous,
			verdict,
			verdictRef,
		)
	}
	if err != nil {
		return corecontract.CollaborationContributionSetV1{}, "",
			loopReadIntegrity(
				"collaboration contribution-set lineage",
				err,
			)
	}
	return frozen, digest, nil
}

func plannedReviewerParentSlotID(
	planned corecontract.CompositeReviewerRunRefV1,
	repairRound uint32,
) string {
	if repairRound == corecontract.CompositeRepairRoundOneV1 {
		return planned.ParentSlotID
	}
	return corecontract.CompositeReviewerParentSlotIDV1
}

func collaborationModelVerdictMatchesCanonical(
	raw []byte,
	verdict corecontract.CollaborationReviewVerdictV1,
) bool {
	canonical, err :=
		corecontract.CanonicalCollaborationReviewModelVerdictV1(verdict)
	return err == nil && bytes.Equal(raw, canonical)
}

func loadCompositeReviewerResult(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
	children []CompositeChildResultRecordV1,
) (*CompositeReviewerResultRecordV1, error) {
	return loadCompositeReviewerResultForRound(
		ctx,
		connection,
		root,
		children,
		0,
	)
}

func loadCompositeReviewerResultForRound(
	ctx context.Context,
	connection readQueryerV1,
	root corecontract.RunManifest,
	children []CompositeChildResultRecordV1,
	repairRound uint32,
) (*CompositeReviewerResultRecordV1, error) {
	if root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil || repairRound > 1 {
		return nil, loopReadIntegrity(
			"composite Reviewer root plan",
			ErrAdmissionIntegrity,
		)
	}
	var planned *corecontract.CompositeReviewerRunRefV1
	if repairRound == 0 {
		planned = root.Composite.Plan.Reviewer
	} else if root.Composite.Plan.Decision != nil {
		repair := root.Composite.Plan.Decision.RepairReviewer
		planned = &repair
	}
	if planned == nil {
		return nil, nil
	}
	if _, err := loadCompositeFamilyLatchRows(ctx, connection, root); err != nil {
		return nil, err
	}

	var (
		tenantID           string
		workspaceID        string
		admissionKey       string
		intentDigest       string
		parentRunID        sql.NullString
		parentManifest     sql.NullString
		parentSlot         sql.NullString
		runState           string
		runDisposition     sql.NullString
		runRevision        int64
		manifestCanonical  []byte
		manifestDigest     string
		memberDigest       string
		memberCount        int
		frameRevision      int64
		frameStep          string
		frameWaitingReason sql.NullString
	)
	if err := connection.QueryRowContext(ctx, `
		SELECT
			tenant_id, workspace_id, admission_key,
			admission_intent_digest,
			parent_run_id, parent_manifest_digest, parent_slot_id,
			state, disposition, revision
		FROM runs
		WHERE run_id=?
	`, planned.RunID).Scan(
		&tenantID,
		&workspaceID,
		&admissionKey,
		&intentDigest,
		&parentRunID,
		&parentManifest,
		&parentSlot,
		&runState,
		&runDisposition,
		&runRevision,
	); err != nil {
		return nil, loopReadIntegrity("composite Reviewer Run", err)
	}
	if tenantID != root.TenantID || workspaceID != root.Workspace.ID ||
		admissionKey != planned.AdmissionKey ||
		!parentRunID.Valid || parentRunID.String != root.RunID ||
		!parentManifest.Valid || parentManifest.String != root.ManifestDigest ||
		!parentSlot.Valid ||
		parentSlot.String != plannedReviewerParentSlotID(*planned, repairRound) ||
		runRevision < 0 {
		return nil, loopReadIntegrity(
			"composite Reviewer Run projection",
			ErrAdmissionIntegrity,
		)
	}
	closure, err := loadAdmissionClosure(
		ctx,
		connection,
		planned.RunID,
		tenantID,
		admissionKey,
		intentDigest,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM run_manifests
		WHERE run_id=?
	`, planned.RunID).Scan(
		&manifestCanonical,
		&manifestDigest,
	); err != nil {
		return nil, loopReadIntegrity("composite Reviewer Manifest", err)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil || manifest.ManifestDigest != manifestDigest ||
		closure.ManifestDigest != manifestDigest ||
		manifest.Composite == nil ||
		manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		manifest.Composite.RepairRound != repairRound ||
		manifest.Composite.RootRunID != root.RunID ||
		manifest.Composite.ParentManifestDigest != root.ManifestDigest ||
		manifest.Composite.ParentSlotID !=
			plannedReviewerParentSlotID(*planned, repairRound) ||
		manifest.Composite.Assignment != nil || manifest.Composite.Plan != nil ||
		manifest.PrimaryAgent != planned.Agent ||
		manifest.TaskInputRef != planned.TaskInputRef {
		return nil, loopReadIntegrity(
			"composite Reviewer Manifest closure",
			ErrAdmissionIntegrity,
		)
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(digest)
		FROM member_execution_snapshots
		WHERE run_id=?
	`, planned.RunID).Scan(&memberCount, &memberDigest); err != nil ||
		memberCount != 1 || memberDigest != planned.MemberSnapshotDigest ||
		closure.MemberSnapshotDigest != memberDigest {
		return nil, loopReadIntegrity(
			"composite Reviewer member closure",
			err,
		)
	}
	if err := connection.QueryRowContext(ctx, `
		SELECT frame_revision, step, waiting_reason
		FROM loop_frames
		WHERE run_id=?
	`, planned.RunID).Scan(
		&frameRevision,
		&frameStep,
		&frameWaitingReason,
	); err != nil || frameRevision < 0 {
		return nil, loopReadIntegrity("composite Reviewer Frame", err)
	}

	result := &CompositeReviewerResultRecordV1{
		RepairRound:          repairRound,
		RunID:                planned.RunID,
		ManifestDigest:       manifestDigest,
		MemberSnapshotDigest: memberDigest,
		RunRevision:          uint64(runRevision),
		FrameRevision:        uint64(frameRevision),
		FrameStep:            frameStep,
	}
	switch frameStep {
	case corecontract.WaitingRepairActivationLoopStep:
		if repairRound != corecontract.CompositeRepairRoundOneV1 ||
			runState != corecontract.InitialRunState ||
			!runDisposition.Valid ||
			runDisposition.String != "WAITING_EXTERNAL" ||
			!frameWaitingReason.Valid ||
			frameWaitingReason.String != compositeRepairDormantWaitingReason {
			return nil, loopReadIntegrity(
				"dormant composite repair Reviewer projection",
				ErrAdmissionIntegrity,
			)
		}
		result.State = CompositeReviewerPendingV1
	case corecontract.WaitingChildrenLoopStep:
		if runState != corecontract.InitialRunState ||
			!runDisposition.Valid ||
			runDisposition.String != "WAITING_EXTERNAL" ||
			!frameWaitingReason.Valid ||
			frameWaitingReason.String != compositeChildrenPendingWaitingReason {
			return nil, loopReadIntegrity(
				"waiting composite Reviewer projection",
				ErrAdmissionIntegrity,
			)
		}
		result.State = CompositeReviewerPendingV1
	case corecontract.InitialLoopStep,
		corecontract.ModelPendingLoopStep:
		if runState != corecontract.InitialRunState || runDisposition.Valid ||
			frameWaitingReason.Valid {
			return nil, loopReadIntegrity(
				"active composite Reviewer projection",
				ErrAdmissionIntegrity,
			)
		}
		result.State = CompositeReviewerPendingV1
	case corecontract.WaitingReconciliationLoopStep:
		if runState != corecontract.WaitingReconciliationLoopStep ||
			!runDisposition.Valid ||
			runDisposition.String != corecontract.WaitingReconciliationLoopStep ||
			!frameWaitingReason.Valid {
			return nil, loopReadIntegrity(
				"unknown composite Reviewer projection",
				ErrAdmissionIntegrity,
			)
		}
		result.State = CompositeReviewerUnknownV1
		result.ErrorClassification = frameWaitingReason.String
	case corecontract.TerminatedLoopStep:
		terminal, err := loadTerminalRunResult(ctx, connection, planned.RunID)
		if err != nil {
			return nil, err
		}
		result.AttemptID = terminal.AttemptID
		result.FrameRevision = terminal.FrameRevision
		result.ErrorClassification = terminal.ErrorClassification
		if terminal.AttemptKind == "" &&
			terminal.ErrorClassification ==
				corecontract.CompositeRepairSkippedReasonV1 {
			result.State = CompositeReviewerSkippedV1
			break
		}
		if terminal.AttemptKind == "" &&
			(terminal.ErrorClassification ==
				corecontract.AllRequiredChildFailedReasonV1 ||
				terminal.ErrorClassification ==
					corecontract.CompositeReviewFailedReasonV1) {
			result.State = CompositeReviewerFailedV1
			break
		}
		switch terminal.State {
		case corecontract.ModelAttemptSucceeded:
			if terminal.AttemptKind != corecontract.AttemptKindModel ||
				len(terminal.OutputCanonical) == 0 ||
				terminal.Output.ActionRequest != nil {
				result.State = CompositeReviewerFailedV1
				result.ErrorClassification =
					"COMPOSITE_REVIEWER_OUTPUT_INVALID"
				break
			}
			resultRef, err := ComputeContentDigest(
				ContentModelResult,
				admissionJSONMediaType,
				terminal.OutputCanonical,
			)
			if err != nil {
				return nil, loopReadIntegrity(
					"composite Reviewer result digest",
					err,
				)
			}
			stored, err := queryContent(ctx, connection, resultRef)
			if err != nil || stored.Kind != ContentModelResult ||
				!bytes.Equal(stored.CanonicalBytes, terminal.OutputCanonical) {
				return nil, loopReadIntegrity(
					"composite Reviewer result content",
					err,
				)
			}
			if root.Composite.Plan.Decision == nil {
				_, _, specialistDigest, err :=
					buildCompositeSpecialistResultSet(root, children)
				if err != nil {
					return nil, err
				}
				verdict, verdictCanonical, err :=
					corecontract.ParseReviewVerdictV1(
						[]byte(terminal.Output.AssistantText),
					)
				if err != nil || verdict.ValidateForCompositeReviewV1(
					root,
					specialistDigest,
				) != nil {
					result.State = CompositeReviewerFailedV1
					result.ErrorClassification =
						"COMPOSITE_REVIEWER_VERDICT_INVALID"
					break
				}
				result.State = CompositeReviewerSucceededV1
				result.ResultRef = resultRef
				result.OutputCanonical = bytes.Clone(terminal.OutputCanonical)
				result.VerdictCanonical = bytes.Clone(verdictCanonical)
				result.Verdict = &verdict
				break
			}
			set, setDigest, setErr := buildCollaborationContributionSet(
				ctx,
				connection,
				root,
				children,
				repairRound,
			)
			if setErr != nil {
				return nil, setErr
			}
			verdict, verdictCanonical, err :=
				corecontract.ParseCollaborationReviewVerdictV1(
					[]byte(terminal.Output.AssistantText),
					root.ManifestDigest,
					setDigest,
					repairRound,
				)
			if err != nil || verdict.ValidateForCollaborationReviewV1(
				root,
				setDigest,
				repairRound,
			) != nil || !collaborationModelVerdictMatchesCanonical(
				[]byte(terminal.Output.AssistantText),
				verdict,
			) {
				result.State = CompositeReviewerFailedV1
				result.ErrorClassification =
					"COMPOSITE_REVIEWER_VERDICT_INVALID"
				break
			}
			result.State = CompositeReviewerSucceededV1
			result.ResultRef = resultRef
			result.OutputCanonical = bytes.Clone(terminal.OutputCanonical)
			result.VerdictCanonical = bytes.Clone(verdictCanonical)
			result.ContributionSet = &set
			result.ContributionSetDigest = setDigest
			result.CollaborationVerdict = &verdict
		case corecontract.ModelAttemptFailed:
			result.State = CompositeReviewerFailedV1
		default:
			return nil, loopReadIntegrity(
				"terminal composite Reviewer state",
				ErrAdmissionIntegrity,
			)
		}
	default:
		return nil, loopReadIntegrity(
			"unsupported composite Reviewer Frame",
			ErrAdmissionIntegrity,
		)
	}
	return result, nil
}

func validateCompositeReviewerRunAgainstRoot(
	root corecontract.RunManifest,
	reviewer corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
) error {
	if root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil || root.Composite.Plan.Reviewer == nil ||
		reviewer.Composite == nil ||
		reviewer.Composite.Role != corecontract.CompositeRunRoleReviewerV1 {
		return ErrAdmissionIntegrity
	}
	var planned *corecontract.CompositeReviewerRunRefV1
	if reviewer.Composite.RepairRound == 0 {
		planned = root.Composite.Plan.Reviewer
	} else if root.Composite.Plan.Decision != nil {
		repair := root.Composite.Plan.Decision.RepairReviewer
		planned = &repair
	}
	if planned == nil {
		return ErrAdmissionIntegrity
	}
	if reviewer.RunID != planned.RunID ||
		reviewer.AdmissionKey != planned.AdmissionKey ||
		reviewer.ParentRunID != root.RunID ||
		reviewer.Composite.RootRunID != root.RunID ||
		reviewer.Composite.ParentManifestDigest != root.ManifestDigest ||
		reviewer.Composite.ParentSlotID != plannedReviewerParentSlotID(
			*planned,
			reviewer.Composite.RepairRound,
		) ||
		reviewer.Composite.Assignment != nil || reviewer.Composite.Plan != nil ||
		reviewer.TaskInputRef != planned.TaskInputRef ||
		reviewer.TaskInputRef != root.TaskInputRef ||
		member.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
		member.Agent != planned.Agent || member.Profile != planned.Profile ||
		reviewer.PrimaryAgent != planned.Agent {
		return ErrAdmissionIntegrity
	}
	return nil
}

// compositeReviewerManifestInPlan verifies the immutable plan edge using only
// fields carried by the Reviewer Manifest. Member/Profile closure is checked
// separately by admission loading; this helper is for recovery paths that must
// distinguish the round-zero and round-one Reviewer without assuming the
// legacy Plan.Reviewer edge is always current.
func compositeReviewerManifestInPlan(
	root corecontract.RunManifest,
	reviewer corecontract.RunManifest,
) bool {
	if root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil || reviewer.Composite == nil ||
		reviewer.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		len(reviewer.Members) != 1 {
		return false
	}
	var planned *corecontract.CompositeReviewerRunRefV1
	switch reviewer.Composite.RepairRound {
	case 0:
		planned = root.Composite.Plan.Reviewer
	case corecontract.CompositeRepairRoundOneV1:
		if root.Composite.Plan.Decision != nil {
			repair := root.Composite.Plan.Decision.RepairReviewer
			planned = &repair
		}
	default:
		return false
	}
	if planned == nil {
		return false
	}
	return reviewer.RunID == planned.RunID &&
		reviewer.AdmissionKey == planned.AdmissionKey &&
		reviewer.TenantID == root.TenantID &&
		reviewer.Workspace == root.Workspace &&
		reviewer.BudgetPolicy == root.BudgetPolicy &&
		reviewer.Deadline.Equal(root.Deadline) &&
		reviewer.ParentRunID == root.RunID &&
		reviewer.Composite.RootRunID == root.RunID &&
		reviewer.Composite.ParentManifestDigest == root.ManifestDigest &&
		reviewer.Composite.ParentSlotID == plannedReviewerParentSlotID(
			*planned,
			reviewer.Composite.RepairRound,
		) &&
		reviewer.Composite.Assignment == nil &&
		reviewer.Composite.Plan == nil &&
		reviewer.PrimaryAgent == planned.Agent &&
		reviewer.TaskInputRef == planned.TaskInputRef &&
		reviewer.TaskInputRef == root.TaskInputRef &&
		reviewer.Members[0].Digest == planned.MemberSnapshotDigest &&
		reviewer.CancellationScope ==
			corecontract.CancellationScopeInheritedV1 &&
		reviewer.ConversationTurn == nil
}

// compositeRepairManifestInDecisionPlan verifies that a round-one dormant
// participant is one of the exact pre-admitted Decision plan edges.
func compositeRepairManifestInDecisionPlan(
	root corecontract.RunManifest,
	repair corecontract.RunManifest,
) bool {
	if root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil || root.Composite.Plan.Decision == nil ||
		repair.Composite == nil ||
		repair.Composite.RepairRound !=
			corecontract.CompositeRepairRoundOneV1 ||
		len(repair.Members) != 1 {
		return false
	}
	if repair.Composite.Role == corecontract.CompositeRunRoleReviewerV1 {
		return compositeReviewerManifestInPlan(root, repair)
	}
	if repair.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
		repair.Composite.Assignment == nil || repair.Composite.Plan != nil {
		return false
	}
	for index := range root.Composite.Plan.Decision.RepairChildren {
		planned := root.Composite.Plan.Decision.RepairChildren[index]
		if repair.RunID != planned.RunID {
			continue
		}
		expectedWorkspace, err := compositeChildWorkspaceForPlan(root, planned)
		if err != nil {
			return false
		}
		return repair.AdmissionKey == planned.AdmissionKey &&
			repair.TenantID == root.TenantID &&
			repair.Workspace == expectedWorkspace &&
			(planned.Transfer != nil ||
				repair.BudgetPolicy == root.BudgetPolicy) &&
			repair.Deadline.Equal(root.Deadline) &&
			repair.ParentRunID == root.RunID &&
			repair.Composite.RootRunID == root.RunID &&
			repair.Composite.ParentManifestDigest == root.ManifestDigest &&
			repair.Composite.ParentSlotID == planned.ParentSlotID &&
			*repair.Composite.Assignment == planned.Assignment &&
			repair.PrimaryAgent == planned.Agent &&
			repair.TaskInputRef == planned.TaskInputRef &&
			repair.TaskInputRef == root.TaskInputRef &&
			repair.Members[0].Digest == planned.MemberSnapshotDigest &&
			repair.CancellationScope ==
				corecontract.CancellationScopeInheritedV1 &&
			repair.ConversationTurn == nil
	}
	return false
}

func validateCompositeChildRunAgainstRoot(
	root corecontract.RunManifest,
	child corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
) error {
	if root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil || child.Composite == nil ||
		child.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
		child.Composite.Assignment == nil || child.Composite.Plan != nil {
		return ErrAdmissionIntegrity
	}
	var planned *corecontract.CompositeChildRunRefV1
	if child.Composite.RepairRound == 0 {
		for index := range root.Composite.Plan.Children {
			candidate := &root.Composite.Plan.Children[index]
			if candidate.SlotID == child.Composite.Assignment.SlotID {
				planned = candidate
				break
			}
		}
	} else if child.Composite.RepairRound ==
		corecontract.CompositeRepairRoundOneV1 &&
		root.Composite.Plan.Decision != nil {
		for index := range root.Composite.Plan.Decision.RepairChildren {
			candidate := &root.Composite.Plan.Decision.RepairChildren[index]
			if candidate.SlotID == child.Composite.Assignment.SlotID {
				planned = candidate
				break
			}
		}
	}
	if planned == nil {
		return ErrAdmissionIntegrity
	}
	expectedWorkspace, err := compositeChildWorkspaceForPlan(root, *planned)
	if err != nil {
		return ErrAdmissionIntegrity
	}
	parentSlotID := planned.SlotID
	if child.Composite.RepairRound == corecontract.CompositeRepairRoundOneV1 {
		parentSlotID = planned.ParentSlotID
	}
	if child.RunID != planned.RunID ||
		child.AdmissionKey != planned.AdmissionKey ||
		child.ParentRunID != root.RunID ||
		child.Composite.RootRunID != root.RunID ||
		child.Composite.ParentManifestDigest != root.ManifestDigest ||
		child.Composite.ParentSlotID != parentSlotID ||
		*child.Composite.Assignment != planned.Assignment ||
		child.TaskInputRef != planned.TaskInputRef ||
		child.TaskInputRef != root.TaskInputRef ||
		child.Workspace != expectedWorkspace ||
		member.Workspace != expectedWorkspace ||
		(planned.Transfer == nil &&
			child.BudgetPolicy != root.BudgetPolicy) ||
		member.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
		member.Agent != planned.Agent || member.Profile != planned.Profile ||
		child.PrimaryAgent != planned.Agent {
		return ErrAdmissionIntegrity
	}
	return nil
}
