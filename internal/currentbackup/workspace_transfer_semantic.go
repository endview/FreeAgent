package currentbackup

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// workspaceTransferAuthoritySemanticV1 is reconstructed only from the
// historical ControlSnapshot frozen by the family. Manifest grant refs never
// become authority on their own.
type workspaceTransferAuthoritySemanticV1 struct {
	root        *coreRunSemanticState
	child       *coreRunSemanticState
	planned     corecontract.CompositeChildRunRefV1
	plan        corecontract.WorkspaceTransferPlanV1
	rootGrant   corecontract.WorkspaceTransferGrantV1
	targetGrant corecontract.WorkspaceTransferGrantV1
}

type workspaceTransferExpectedRecordV1 struct {
	envelope          corecontract.WorkspaceTransferEnvelopeV1
	envelopeCanonical []byte
	envelopeDigest    string
	envelopeRef       string
	payloadRef        string
	payloadKind       currentstore.ContentKind
	payloadCanonical  []byte
}

type workspaceTransferExpectedContentV1 struct {
	envelopes map[string]struct{}
	payloads  map[string]struct{}
}

func inspectWorkspaceTransferSemanticClosure(
	ctx context.Context,
	database semanticQueryer,
	states map[string]*coreRunSemanticState,
) error {
	expected := workspaceTransferExpectedContentV1{
		envelopes: make(map[string]struct{}),
		payloads:  make(map[string]struct{}),
	}
	authorityByChild := make(
		map[string]*workspaceTransferAuthoritySemanticV1,
	)
	rootByConsumer := make(map[string]*coreRunSemanticState)

	rootIDs := make([]string, 0)
	for runID, state := range states {
		if state != nil && state.manifest.Composite != nil &&
			state.manifest.Composite.Role == corecontract.CompositeRunRoleRootV1 {
			rootIDs = append(rootIDs, runID)
		}
	}
	sort.Strings(rootIDs)
	for _, rootID := range rootIDs {
		root := states[rootID]
		rootByConsumer[rootID] = root
		plan := root.manifest.Composite.Plan
		if plan == nil {
			return workspaceTransferIntegrity(
				"Composite root %q plan is absent", rootID,
			)
		}
		if plan.Reviewer != nil {
			rootByConsumer[plan.Reviewer.RunID] = root
		}
		if plan.Decision != nil {
			rootByConsumer[plan.Decision.RepairReviewer.RunID] = root
		}
		authorities, err := inspectWorkspaceTransferFamilyAuthorityV1(
			ctx,
			database,
			states,
			root,
		)
		if err != nil {
			return err
		}
		for childID, authority := range authorities {
			authorityByChild[childID] = authority
		}
	}

	runIDs := make([]string, 0, len(states))
	for runID := range states {
		runIDs = append(runIDs, runID)
	}
	sort.Strings(runIDs)
	for _, runID := range runIDs {
		state := states[runID]
		if state == nil {
			continue
		}
		node := state.manifest.Composite
		if node == nil {
			if coreRunCarriesWorkspaceTransferEvidenceV1(state) {
				return workspaceTransferIntegrity(
					"non-Composite Run %q carries transfer evidence", runID,
				)
			}
			continue
		}
		switch node.Role {
		case corecontract.CompositeRunRoleChildV1:
			authority := authorityByChild[runID]
			if authority == nil {
				if coreRunCarriesWorkspaceTransferEvidenceV1(state) {
					return workspaceTransferIntegrity(
						"same-Workspace Child %q carries transfer evidence",
						runID,
					)
				}
				continue
			}
			if err := inspectWorkspaceTransferChildV1(
				ctx,
				database,
				states,
				authority,
				&expected,
			); err != nil {
				return err
			}
		case corecontract.CompositeRunRoleRootV1,
			corecontract.CompositeRunRoleReviewerV1:
			root := rootByConsumer[runID]
			if root == nil {
				if coreRunCarriesWorkspaceTransferEvidenceV1(state) {
					return workspaceTransferIntegrity(
						"Composite consumer %q has no frozen root", runID,
					)
				}
				continue
			}
			if err := inspectWorkspaceTransferConsumerV1(
				ctx,
				database,
				state,
				root,
				authorityByChild,
				&expected,
			); err != nil {
				return err
			}
		default:
			if coreRunCarriesWorkspaceTransferEvidenceV1(state) {
				return workspaceTransferIntegrity(
					"unsupported Composite participant %q carries transfer evidence",
					runID,
				)
			}
		}
	}
	return inspectWorkspaceTransferContentEnumerationV1(
		ctx,
		database,
		expected,
	)
}

func inspectWorkspaceTransferFamilyAuthorityV1(
	ctx context.Context,
	database semanticQueryer,
	states map[string]*coreRunSemanticState,
	root *coreRunSemanticState,
) (map[string]*workspaceTransferAuthoritySemanticV1, error) {
	result := make(map[string]*workspaceTransferAuthoritySemanticV1)
	if root == nil || root.manifest.Composite == nil ||
		root.manifest.Composite.Plan == nil {
		return nil, workspaceTransferIntegrity("Composite authority root is absent")
	}
	control, err := loadWorkspaceTransferHistoricalControlV1(
		ctx,
		database,
		root,
	)
	if err != nil {
		return nil, err
	}
	definition, found := control.FindCompositeAgent(root.member.Agent.ID)
	if !found || definition.CoordinatorProfileID != root.member.Profile.ID {
		return nil, workspaceTransferIntegrity(
			"Composite root %q definition is absent from historical Control",
			root.row.runID,
		)
	}
	rootWorkspace, found := control.FindWorkspace(root.manifest.Workspace.ID)
	if !found || rootWorkspace.Workspace != root.manifest.Workspace {
		return nil, workspaceTransferIntegrity(
			"Composite root %q Workspace differs from historical Control",
			root.row.runID,
		)
	}

	initialBySlot := make(
		map[string]corecontract.CompositeChildRunRefV1,
		len(root.manifest.Composite.Plan.Children),
	)
	for _, planned := range root.manifest.Composite.Plan.Children {
		initialBySlot[planned.SlotID] = planned
		authority, authorityErr := inspectWorkspaceTransferPlannedAuthorityV1(
			control,
			definition,
			states,
			root,
			planned,
		)
		if authorityErr != nil {
			return nil, authorityErr
		}
		if authority != nil {
			result[planned.RunID] = authority
		}
	}
	decision := root.manifest.Composite.Plan.Decision
	if decision == nil {
		return result, nil
	}
	for _, planned := range decision.RepairChildren {
		initial, found := initialBySlot[planned.SlotID]
		if !found || !sameWorkspaceTransferPlanPointerV1(
			initial.Transfer,
			planned.Transfer,
		) {
			return nil, workspaceTransferIntegrity(
				"Composite root %q repair slot %q transfer plan differs from round zero",
				root.row.runID,
				planned.SlotID,
			)
		}
		authority, authorityErr := inspectWorkspaceTransferPlannedAuthorityV1(
			control,
			definition,
			states,
			root,
			planned,
		)
		if authorityErr != nil {
			return nil, authorityErr
		}
		if authority != nil {
			result[planned.RunID] = authority
		}
	}
	return result, nil
}

func loadWorkspaceTransferHistoricalControlV1(
	ctx context.Context,
	database semanticQueryer,
	root *coreRunSemanticState,
) (controlcontract.ControlSnapshot, error) {
	var (
		controlID        string
		controlTenant    string
		controlRevision  int64
		controlCanonical []byte
		controlDigest    string
	)
	if root == nil || root.member.Catalog.ID == "" {
		return controlcontract.ControlSnapshot{},
			workspaceTransferIntegrity("historical Control root is absent")
	}
	if err := database.QueryRowContext(ctx, `
		SELECT control.snapshot_id, control.tenant_id, control.revision,
		       control.canonical_json, control.digest
		FROM runtime_catalog_generations AS catalog
		JOIN control_snapshots AS control
		  ON control.snapshot_id=catalog.control_snapshot_id
		WHERE catalog.generation_id=?
	`, root.member.Catalog.ID).Scan(
		&controlID,
		&controlTenant,
		&controlRevision,
		&controlCanonical,
		&controlDigest,
	); err != nil || controlRevision <= 0 ||
		controlTenant != root.row.tenantID {
		return controlcontract.ControlSnapshot{}, workspaceTransferIntegrity(
			"Composite root %q historical Control is absent: %v",
			root.row.runID,
			err,
		)
	}
	control, err := controlcontract.RestoreControlSnapshot(
		controlCanonical,
		controlcontract.ControlSnapshotRef{
			SnapshotID: controlID,
			Revision:   uint64(controlRevision),
			Digest:     controlDigest,
		},
	)
	if err != nil || control.TenantID != root.row.tenantID {
		return controlcontract.ControlSnapshot{}, workspaceTransferIntegrity(
			"Composite root %q historical Control does not close: %v",
			root.row.runID,
			err,
		)
	}
	return control, nil
}

func inspectWorkspaceTransferPlannedAuthorityV1(
	control controlcontract.ControlSnapshot,
	definition controlcontract.CompositeAgentDefinitionV1,
	states map[string]*coreRunSemanticState,
	root *coreRunSemanticState,
	planned corecontract.CompositeChildRunRefV1,
) (*workspaceTransferAuthoritySemanticV1, error) {
	child := states[planned.RunID]
	configured, found := definition.FindMember(planned.SlotID)
	if child == nil || !found || configured.AgentID != planned.Agent.ID ||
		configured.ProfileID != planned.Profile.ID ||
		configured.FocusID != planned.Assignment.FocusID ||
		configured.WeightBasisPoints != planned.Assignment.WeightBasisPoints ||
		child.member.Catalog != root.member.Catalog {
		return nil, workspaceTransferIntegrity(
			"Composite root %q slot %q differs from historical Control",
			root.row.runID,
			planned.SlotID,
		)
	}
	targetID := configured.TargetWorkspaceID
	if targetID == "" || targetID == root.manifest.Workspace.ID {
		if planned.Transfer != nil ||
			child.manifest.Workspace != root.manifest.Workspace ||
			child.member.Workspace != root.manifest.Workspace {
			return nil, workspaceTransferIntegrity(
				"Composite root %q same-Workspace slot %q transfer closure differs",
				root.row.runID,
				planned.SlotID,
			)
		}
		return nil, nil
	}
	if definition.Decision == nil || planned.Transfer == nil {
		return nil, workspaceTransferIntegrity(
			"Composite root %q slot %q lacks an explicit transfer plan",
			root.row.runID,
			planned.SlotID,
		)
	}
	targetWorkspace, found := control.FindWorkspace(targetID)
	if !found || targetWorkspace.Workspace != planned.Transfer.TargetWorkspace ||
		planned.Transfer.RootWorkspace != root.manifest.Workspace ||
		child.manifest.Workspace != targetWorkspace.Workspace ||
		child.member.Workspace != targetWorkspace.Workspace ||
		child.row.workspaceID != targetWorkspace.Workspace.ID {
		return nil, workspaceTransferIntegrity(
			"Composite root %q target Workspace for slot %q differs",
			root.row.runID,
			planned.SlotID,
		)
	}
	rootWorkspace, found := control.FindWorkspace(root.manifest.Workspace.ID)
	if !found || rootWorkspace.Workspace != root.manifest.Workspace {
		return nil, workspaceTransferIntegrity(
			"Composite root %q source Workspace is absent", root.row.runID,
		)
	}
	rootGrant, found := rootWorkspace.FindTransferGrantForPeer(
		targetWorkspace.Workspace,
	)
	if !found {
		return nil, workspaceTransferIntegrity(
			"Composite root %q source grant for slot %q is absent",
			root.row.runID,
			planned.SlotID,
		)
	}
	targetGrant, found := targetWorkspace.FindTransferGrantForPeer(
		rootWorkspace.Workspace,
	)
	if !found || planned.Transfer.ValidateAgainstGrantsV1(
		rootGrant,
		targetGrant,
	) != nil {
		return nil, workspaceTransferIntegrity(
			"Composite root %q bilateral grants for slot %q differ",
			root.row.runID,
			planned.SlotID,
		)
	}
	return &workspaceTransferAuthoritySemanticV1{
		root:        root,
		child:       child,
		planned:     planned,
		plan:        *planned.Transfer,
		rootGrant:   rootGrant,
		targetGrant: targetGrant,
	}, nil
}

func inspectWorkspaceTransferChildV1(
	ctx context.Context,
	database semanticQueryer,
	states map[string]*coreRunSemanticState,
	authority *workspaceTransferAuthoritySemanticV1,
	expected *workspaceTransferExpectedContentV1,
) error {
	if authority == nil || authority.root == nil || authority.child == nil {
		return workspaceTransferIntegrity("Workspace transfer Child authority is absent")
	}
	child := authority.child
	attemptIDs := sortedCoreAttemptIDsV1(child)
	for _, attemptID := range attemptIDs {
		attempt := child.attempts[attemptID]
		repairBasis, err := workspaceTransferRepairBasisForChildV1(
			ctx,
			database,
			states,
			authority.root,
			child,
		)
		if err != nil {
			return err
		}
		task, err := inspectCoreContent(
			ctx,
			database,
			authority.root.manifest.TaskInputRef,
			currentstore.ContentTaskInput,
		)
		if err != nil || task.mediaType != coreJSONMediaType {
			return workspaceTransferIntegrity(
				"Child %q root TASK_INPUT differs: %v", child.row.runID, err,
			)
		}
		summary, summaryCanonical, err :=
			contextcompiler.CompileWorkspaceTaskSummaryV1(
				authority.root.manifest.TaskInputRef,
				task.canonical,
				repairBasis,
			)
		if err != nil {
			return workspaceTransferIntegrity(
				"Child %q trusted REQUEST summary differs: %v",
				child.row.runID,
				err,
			)
		}
		payloadRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentWorkspaceTransferPayload,
			coreJSONMediaType,
			summaryCanonical,
		)
		if err != nil {
			return workspaceTransferIntegrity(
				"Child %q REQUEST payload digest: %v", child.row.runID, err,
			)
		}
		record, err := newWorkspaceTransferExpectedRecordV1(
			authority,
			corecontract.WorkspaceTransferDirectionRequestV1,
			corecontract.WorkspaceTransferPayloadTaskSummaryV1,
			corecontract.WorkspaceTaskSummarySchemaVersionV1,
			payloadRef,
			currentstore.ContentWorkspaceTransferPayload,
			summaryCanonical,
		)
		if err != nil {
			return err
		}
		if attempt == nil || attempt.compilation == nil ||
			len(attempt.compilation.WorkspaceTransfers) != 1 {
			return workspaceTransferIntegrity(
				"Child %q Attempt %q requires exactly one REQUEST evidence",
				child.row.runID,
				attemptID,
			)
		}
		if err := inspectWorkspaceTransferExpectedRecordV1(
			ctx,
			database,
			record,
			attempt.compilation.WorkspaceTransfers[0],
			expected,
		); err != nil {
			return err
		}
		wantMessage := moduleapi.ModelMessageV1{
			Role: moduleapi.ModelRoleUser, Content: summary.Summary,
		}
		if countCoreModelMessage(attempt.request.Messages, wantMessage) != 1 {
			return workspaceTransferIntegrity(
				"Child %q Attempt %q does not use the trusted task summary",
				child.row.runID,
				attemptID,
			)
		}
		taskValue, restoreErr := corecontract.RestoreTaskInputV1(task.canonical)
		if restoreErr != nil {
			return workspaceTransferIntegrity(
				"Child %q root TASK_INPUT wire differs: %v",
				child.row.runID,
				restoreErr,
			)
		}
		if taskValue.Text != summary.Summary {
			for _, message := range attempt.request.Messages {
				if message.Content == taskValue.Text {
					return workspaceTransferIntegrity(
						"Child %q Attempt %q exposes the raw TASK_INPUT",
						child.row.runID,
						attemptID,
					)
				}
			}
		}
	}

	terminal := coreSuccessfulTerminalAttempt(child)
	if !workspaceTransferStrictSpecialistResultV1(terminal) {
		return nil
	}
	resultContent, err := inspectCoreContent(
		ctx,
		database,
		terminal.resultRef.String,
		currentstore.ContentModelResult,
	)
	if err != nil || resultContent.mediaType != coreJSONMediaType {
		return workspaceTransferIntegrity(
			"Child %q terminal MODEL_RESULT differs: %v",
			child.row.runID,
			err,
		)
	}
	record, err := newWorkspaceTransferExpectedRecordV1(
		authority,
		corecontract.WorkspaceTransferDirectionResultV1,
		corecontract.WorkspaceTransferPayloadSpecialistResultV1,
		corecontract.SpecialistContributionSchemaVersionV1,
		terminal.resultRef.String,
		currentstore.ContentModelResult,
		resultContent.canonical,
	)
	if err != nil {
		return err
	}
	return inspectWorkspaceTransferExpectedRecordWithoutEvidenceV1(
		ctx,
		database,
		record,
		expected,
	)
}

func inspectWorkspaceTransferConsumerV1(
	ctx context.Context,
	database semanticQueryer,
	consumer *coreRunSemanticState,
	root *coreRunSemanticState,
	authorityByChild map[string]*workspaceTransferAuthoritySemanticV1,
	expected *workspaceTransferExpectedContentV1,
) error {
	for _, attemptID := range sortedCoreAttemptIDsV1(consumer) {
		attempt := consumer.attempts[attemptID]
		if attempt == nil || attempt.compilation == nil ||
			attempt.compilation.Composite == nil {
			return workspaceTransferIntegrity(
				"Composite consumer %q Attempt %q compilation is absent",
				consumer.row.runID,
				attemptID,
			)
		}
		want := make([]workspaceTransferExpectedRecordV1, 0)
		for index, result := range attempt.compilation.Composite.ChildResults {
			authority := authorityByChild[result.RunID]
			if authority == nil {
				continue
			}
			if authority.root != root || authority.planned.SlotID != result.Assignment.SlotID ||
				authority.child.manifest.ManifestDigest != result.ChildManifestDigest {
				return workspaceTransferIntegrity(
					"Composite consumer %q contribution %d transfer family differs",
					consumer.row.runID,
					index,
				)
			}
			terminal := coreSuccessfulTerminalAttempt(authority.child)
			if !workspaceTransferStrictSpecialistResultV1(terminal) ||
				terminal.resultRef.String != result.ResultRef {
				return workspaceTransferIntegrity(
					"Composite consumer %q contribution %d lacks a strict terminal RESULT",
					consumer.row.runID,
					index,
				)
			}
			resultContent, err := inspectCoreContent(
				ctx,
				database,
				terminal.resultRef.String,
				currentstore.ContentModelResult,
			)
			if err != nil {
				return workspaceTransferIntegrity(
					"Composite consumer %q contribution %d payload: %v",
					consumer.row.runID,
					index,
					err,
				)
			}
			record, err := newWorkspaceTransferExpectedRecordV1(
				authority,
				corecontract.WorkspaceTransferDirectionResultV1,
				corecontract.WorkspaceTransferPayloadSpecialistResultV1,
				corecontract.SpecialistContributionSchemaVersionV1,
				terminal.resultRef.String,
				currentstore.ContentModelResult,
				resultContent.canonical,
			)
			if err != nil {
				return err
			}
			want = append(want, record)
		}
		if len(attempt.compilation.WorkspaceTransfers) != len(want) {
			return workspaceTransferIntegrity(
				"Composite consumer %q Attempt %q RESULT evidence count is %d, want %d",
				consumer.row.runID,
				attemptID,
				len(attempt.compilation.WorkspaceTransfers),
				len(want),
			)
		}
		for index := range want {
			if err := inspectWorkspaceTransferExpectedRecordV1(
				ctx,
				database,
				want[index],
				attempt.compilation.WorkspaceTransfers[index],
				expected,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func workspaceTransferRepairBasisForChildV1(
	ctx context.Context,
	database semanticQueryer,
	states map[string]*coreRunSemanticState,
	root *coreRunSemanticState,
	child *coreRunSemanticState,
) ([]byte, error) {
	if child == nil || child.manifest.Composite == nil {
		return nil, workspaceTransferIntegrity("repair Child is absent")
	}
	if child.manifest.Composite.RepairRound == 0 {
		return nil, nil
	}
	if child.manifest.Composite.RepairRound !=
		corecontract.CompositeRepairRoundOneV1 || root == nil ||
		root.manifest.Composite == nil || root.manifest.Composite.Plan == nil ||
		root.manifest.Composite.Plan.Decision == nil ||
		root.manifest.Composite.Plan.Reviewer == nil {
		return nil, workspaceTransferIntegrity(
			"repair Child %q lineage root is absent", child.row.runID,
		)
	}
	plan := root.manifest.Composite.Plan
	contributions := make(
		[]coreCollaborationContributionState,
		len(plan.Children),
	)
	for index, planned := range plan.Children {
		initial := states[planned.RunID]
		value, err := inspectCollaborationSpecialist(
			ctx,
			database,
			root,
			initial,
			nil,
			nil,
			nil,
		)
		if err != nil {
			return nil, err
		}
		contributions[index] = value
	}
	set, setDigest, ready, err := coreCollaborationContributionSet(
		root,
		contributions,
		0,
		corecontract.CollaborationContributionSetV1{},
		corecontract.CollaborationReviewVerdictV1{},
		"",
	)
	if err != nil || !ready {
		return nil, workspaceTransferIntegrity(
			"repair Child %q previous contribution set is absent: %v",
			child.row.runID,
			err,
		)
	}
	reviewer := states[plan.Reviewer.RunID]
	review, err := inspectCollaborationReviewer(
		root,
		reviewer,
		contributions,
		set,
		setDigest,
		true,
		0,
	)
	if err != nil || !review.valid {
		return nil, workspaceTransferIntegrity(
			"repair Child %q source verdict is absent: %v",
			child.row.runID,
			err,
		)
	}
	var previous *coreCollaborationContributionState
	for index, planned := range plan.Children {
		if planned.SlotID == child.manifest.Composite.Assignment.SlotID {
			previous = &contributions[index]
			break
		}
	}
	if previous == nil {
		return nil, workspaceTransferIntegrity(
			"repair Child %q previous slot is absent", child.row.runID,
		)
	}
	return coreCollaborationRepairBasis(
		root,
		*child.manifest.Composite.Assignment,
		set,
		review,
		*previous,
	)
}

func newWorkspaceTransferExpectedRecordV1(
	authority *workspaceTransferAuthoritySemanticV1,
	direction corecontract.WorkspaceTransferDirectionV1,
	payloadKind corecontract.WorkspaceTransferPayloadKindV1,
	payloadSchemaVersion string,
	payloadRef string,
	contentKind currentstore.ContentKind,
	payloadCanonical []byte,
) (workspaceTransferExpectedRecordV1, error) {
	if authority == nil || authority.root == nil || authority.child == nil ||
		len(payloadCanonical) == 0 || len(payloadCanonical) > int(^uint32(0)) {
		return workspaceTransferExpectedRecordV1{},
			workspaceTransferIntegrity("expected transfer record is invalid")
	}
	sourceGrant := authority.rootGrant
	targetGrant := authority.targetGrant
	if direction == corecontract.WorkspaceTransferDirectionResultV1 {
		sourceGrant, targetGrant = targetGrant, sourceGrant
	}
	_, _, sourceDigest, sourceErr :=
		corecontract.NewWorkspaceTransferGrantV1(sourceGrant)
	_, _, targetDigest, targetErr :=
		corecontract.NewWorkspaceTransferGrantV1(targetGrant)
	if sourceErr != nil || targetErr != nil {
		return workspaceTransferExpectedRecordV1{}, workspaceTransferIntegrity(
			"historical transfer grant canonical form differs: %v / %v",
			sourceErr,
			targetErr,
		)
	}
	envelope, canonical, digest, err :=
		corecontract.NewWorkspaceTransferEnvelopeV1(
			corecontract.WorkspaceTransferEnvelopeV1{
				SchemaVersion:        corecontract.WorkspaceTransferEnvelopeSchemaVersionV1,
				TenantID:             authority.root.manifest.TenantID,
				SourceGrantID:        sourceGrant.GrantID,
				TargetGrantID:        targetGrant.GrantID,
				Direction:            direction,
				PayloadKind:          payloadKind,
				PayloadSchemaVersion: payloadSchemaVersion,
				TaskInputRef:         authority.root.manifest.TaskInputRef,
				SourceWorkspace:      sourceGrant.Workspace,
				TargetWorkspace:      targetGrant.Workspace,
				SourceGrantDigest:    sourceDigest,
				TargetGrantDigest:    targetDigest,
				RootRunID:            authority.root.row.runID,
				ChildRunID:           authority.child.row.runID,
				SlotID:               authority.planned.SlotID,
				PayloadRef:           payloadRef,
				PayloadSizeBytes:     uint32(len(payloadCanonical)),
			},
		)
	if err != nil || envelope.ValidateAgainstGrantsV1(
		sourceGrant,
		targetGrant,
	) != nil || envelope.ValidateAgainstPlanV1(authority.plan) != nil ||
		envelope.ValidateForCompositeFamilyV1(
			authority.root.manifest,
			authority.child.manifest,
		) != nil || envelope.ValidateResolvedPayloadV1(
		corecontract.WorkspaceTransferResolvedPayloadV1{
			PayloadRef:     payloadRef,
			ContentKind:    string(contentKind),
			MediaType:      coreJSONMediaType,
			CanonicalBytes: bytes.Clone(payloadCanonical),
		},
	) != nil {
		return workspaceTransferExpectedRecordV1{}, workspaceTransferIntegrity(
			"expected transfer envelope does not close: %v", err,
		)
	}
	envelopeRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentWorkspaceTransferEnvelope,
		coreJSONMediaType,
		canonical,
	)
	if err != nil {
		return workspaceTransferExpectedRecordV1{}, workspaceTransferIntegrity(
			"expected transfer envelope ref: %v", err,
		)
	}
	return workspaceTransferExpectedRecordV1{
		envelope:          envelope,
		envelopeCanonical: bytes.Clone(canonical),
		envelopeDigest:    digest,
		envelopeRef:       envelopeRef,
		payloadRef:        payloadRef,
		payloadKind:       contentKind,
		payloadCanonical:  bytes.Clone(payloadCanonical),
	}, nil
}

func inspectWorkspaceTransferExpectedRecordV1(
	ctx context.Context,
	database semanticQueryer,
	record workspaceTransferExpectedRecordV1,
	evidence corecontract.WorkspaceTransferEvidenceV1,
	expected *workspaceTransferExpectedContentV1,
) error {
	wantEvidence := corecontract.WorkspaceTransferEvidenceV1{
		Direction:      record.envelope.Direction,
		PayloadKind:    record.envelope.PayloadKind,
		EnvelopeRef:    record.envelopeRef,
		EnvelopeDigest: record.envelopeDigest,
		PayloadRef:     record.payloadRef,
		RootRunID:      record.envelope.RootRunID,
		ChildRunID:     record.envelope.ChildRunID,
		SlotID:         record.envelope.SlotID,
	}
	if evidence != wantEvidence {
		return workspaceTransferIntegrity(
			"transfer evidence for Child %q slot %q differs",
			record.envelope.ChildRunID,
			record.envelope.SlotID,
		)
	}
	return inspectWorkspaceTransferExpectedRecordWithoutEvidenceV1(
		ctx,
		database,
		record,
		expected,
	)
}

func inspectWorkspaceTransferExpectedRecordWithoutEvidenceV1(
	ctx context.Context,
	database semanticQueryer,
	record workspaceTransferExpectedRecordV1,
	expected *workspaceTransferExpectedContentV1,
) error {
	envelopeContent, err := inspectCoreContent(
		ctx,
		database,
		record.envelopeRef,
		currentstore.ContentWorkspaceTransferEnvelope,
	)
	if err != nil || envelopeContent.mediaType != coreJSONMediaType ||
		!bytes.Equal(envelopeContent.canonical, record.envelopeCanonical) {
		return workspaceTransferIntegrity(
			"transfer envelope %q differs: %v", record.envelopeRef, err,
		)
	}
	envelope, err := corecontract.RestoreWorkspaceTransferEnvelopeV1(
		envelopeContent.canonical,
		record.envelopeDigest,
	)
	if err != nil || envelope != record.envelope {
		return workspaceTransferIntegrity(
			"transfer envelope %q protocol differs: %v",
			record.envelopeRef,
			err,
		)
	}
	payload, err := inspectCoreContent(
		ctx,
		database,
		record.payloadRef,
		record.payloadKind,
	)
	if err != nil || payload.mediaType != coreJSONMediaType ||
		!bytes.Equal(payload.canonical, record.payloadCanonical) ||
		envelope.ValidateResolvedPayloadV1(
			corecontract.WorkspaceTransferResolvedPayloadV1{
				PayloadRef:     record.payloadRef,
				ContentKind:    string(payload.kind),
				MediaType:      payload.mediaType,
				CanonicalBytes: bytes.Clone(payload.canonical),
			},
		) != nil {
		return workspaceTransferIntegrity(
			"transfer payload %q differs: %v", record.payloadRef, err,
		)
	}
	if expected != nil {
		expected.envelopes[record.envelopeRef] = struct{}{}
		if record.payloadKind == currentstore.ContentWorkspaceTransferPayload {
			expected.payloads[record.payloadRef] = struct{}{}
		}
	}
	return nil
}

func inspectWorkspaceTransferContentEnumerationV1(
	ctx context.Context,
	database semanticQueryer,
	expected workspaceTransferExpectedContentV1,
) error {
	rows, err := database.QueryContext(ctx, `
		SELECT content_digest, kind
		FROM content_records
		WHERE kind IN ('WORKSPACE_TRANSFER_PAYLOAD', 'WORKSPACE_TRANSFER_ENVELOPE')
		ORDER BY kind, content_digest
	`)
	if err != nil {
		return workspaceTransferIntegrity("enumerate transfer content: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var digest, kind string
		if err := rows.Scan(&digest, &kind); err != nil {
			return workspaceTransferIntegrity("scan transfer content: %v", err)
		}
		switch currentstore.ContentKind(kind) {
		case currentstore.ContentWorkspaceTransferEnvelope:
			if _, ok := expected.envelopes[digest]; !ok {
				return workspaceTransferIntegrity(
					"orphan transfer envelope %q", digest,
				)
			}
		case currentstore.ContentWorkspaceTransferPayload:
			if _, ok := expected.payloads[digest]; !ok {
				return workspaceTransferIntegrity(
					"orphan transfer payload %q", digest,
				)
			}
		default:
			return workspaceTransferIntegrity(
				"unsupported enumerated transfer content kind %q", kind,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return workspaceTransferIntegrity("iterate transfer content: %v", err)
	}
	return nil
}

func workspaceTransferStrictSpecialistResultV1(
	attempt *coreModelAttemptState,
) bool {
	if attempt == nil || attempt.result == nil ||
		attempt.result.ActionRequest != nil || !attempt.resultRef.Valid {
		return false
	}
	raw := []byte(attempt.result.AssistantText)
	_, canonical, _, err := corecontract.ParseSpecialistContributionV1(raw)
	return err == nil && bytes.Equal(canonical, raw) &&
		coreCollaborationEvidenceIsClosed(
			mustWorkspaceTransferContributionV1(raw),
			attempt,
		)
}

func mustWorkspaceTransferContributionV1(
	raw []byte,
) corecontract.SpecialistContributionV1 {
	value, _, _, _ := corecontract.ParseSpecialistContributionV1(raw)
	return value
}

func coreRunCarriesWorkspaceTransferEvidenceV1(
	run *coreRunSemanticState,
) bool {
	if run == nil {
		return false
	}
	for _, attempt := range run.attempts {
		if attempt != nil && attempt.compilation != nil &&
			len(attempt.compilation.WorkspaceTransfers) != 0 {
			return true
		}
	}
	return false
}

func sortedCoreAttemptIDsV1(run *coreRunSemanticState) []string {
	if run == nil {
		return nil
	}
	result := make([]string, 0, len(run.attempts))
	for attemptID := range run.attempts {
		result = append(result, attemptID)
	}
	sort.Strings(result)
	return result
}

func sameWorkspaceTransferPlanPointerV1(
	left *corecontract.WorkspaceTransferPlanV1,
	right *corecontract.WorkspaceTransferPlanV1,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func workspaceTransferIntegrity(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: Workspace transfer semantic closure: %s",
		ErrIntegrity,
		fmt.Sprintf(format, arguments...),
	)
}
