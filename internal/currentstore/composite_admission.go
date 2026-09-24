package currentstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	compositeChildrenPendingWaitingReason = "COMPOSITE_CHILDREN_PENDING"
	compositeRepairDormantWaitingReason   = "COMPOSITE_REPAIR_DORMANT"
)

// CommitCompositeRunFamilyInput is the complete immutable closure for one
// bounded, one-level composite family. Parent and all Children are published
// by one SQLite transaction or none of them are visible.
type CommitCompositeRunFamilyInput struct {
	Parent         CommitRunAdmissionInput
	Children       []CommitRunAdmissionInput
	Reviewer       *CommitRunAdmissionInput
	RepairChildren []CommitRunAdmissionInput
	RepairReviewer *CommitRunAdmissionInput
}

// CompositeRunFamilyAdmissionResult preserves the Parent plan order.
type CompositeRunFamilyAdmissionResult struct {
	Parent         RunAdmissionResult
	Children       []RunAdmissionResult
	Reviewer       *RunAdmissionResult
	RepairChildren []RunAdmissionResult
	RepairReviewer *RunAdmissionResult
	Created        bool
}

// ResolveCompositeRunFamily is the retry-safe pre-compile lookup. It reads the
// already-frozen root plan and exact Child admission closures; it never
// recompiles the family from current Control.
func (store *Store) ResolveCompositeRunFamily(
	ctx context.Context,
	tenantID string,
	admissionKey string,
	intentDigest string,
) (CompositeRunFamilyAdmissionResult, bool, error) {
	if ctx == nil {
		return CompositeRunFamilyAdmissionResult{}, false, fmt.Errorf(
			"%w: context is nil", ErrInvalidAdmission,
		)
	}
	if err := validateAdmissionIdentity(
		tenantID, admissionKey, intentDigest,
	); err != nil {
		return CompositeRunFamilyAdmissionResult{}, false, err
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return CompositeRunFamilyAdmissionResult{}, false, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CompositeRunFamilyAdmissionResult{}, false, fmt.Errorf(
			"currentstore: acquire composite family resolution connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return CompositeRunFamilyAdmissionResult{}, false, fmt.Errorf(
			"currentstore: begin composite family resolution: %w", err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	parent, found, err := resolveAdmissionWithQueryer(
		ctx, connection, tenantID, admissionKey, intentDigest,
	)
	if err != nil || !found {
		if err == nil {
			if _, commitErr := connection.ExecContext(ctx, `COMMIT`); commitErr != nil {
				return CompositeRunFamilyAdmissionResult{}, false, fmt.Errorf(
					"currentstore: commit empty composite family resolution: %w",
					commitErr,
				)
			}
			committed = true
		}
		return CompositeRunFamilyAdmissionResult{}, found, err
	}
	root, err := loadCompositeRootManifest(ctx, connection, parent.RunID)
	if err != nil || root.TenantID != tenantID ||
		root.ManifestDigest != parent.ManifestDigest {
		return CompositeRunFamilyAdmissionResult{}, true, fmt.Errorf(
			"%w: resolved Parent is not the exact composite root",
			ErrAdmissionIntegrity,
		)
	}
	if _, err := loadCompositeFamilyLatchRows(ctx, connection, root); err != nil {
		return CompositeRunFamilyAdmissionResult{}, true, err
	}
	children := make(
		[]RunAdmissionResult,
		len(root.Composite.Plan.Children),
	)
	for index, planned := range root.Composite.Plan.Children {
		expectedWorkspace, workspaceErr := compositeChildWorkspaceForPlan(
			root,
			planned,
		)
		var (
			childTenant    string
			childAdmission string
			childIntent    string
			childWorkspace string
		)
		if err := connection.QueryRowContext(ctx, `
			SELECT tenant_id, admission_key, admission_intent_digest, workspace_id
			FROM runs
			WHERE run_id=?
		`, planned.RunID).Scan(
			&childTenant,
			&childAdmission,
			&childIntent,
			&childWorkspace,
		); err != nil || workspaceErr != nil || childTenant != tenantID ||
			childAdmission != planned.AdmissionKey ||
			childWorkspace != expectedWorkspace.ID {
			return CompositeRunFamilyAdmissionResult{}, true, fmt.Errorf(
				"%w: composite Child %q identity projection",
				ErrAdmissionIntegrity,
				planned.RunID,
			)
		}
		child, err := loadAdmissionClosure(
			ctx,
			connection,
			planned.RunID,
			childTenant,
			childAdmission,
			childIntent,
			childWorkspace,
		)
		if err != nil ||
			child.MemberSnapshotDigest != planned.MemberSnapshotDigest {
			return CompositeRunFamilyAdmissionResult{}, true, fmt.Errorf(
				"%w: composite Child %q closure: %v",
				ErrAdmissionIntegrity,
				planned.RunID,
				err,
			)
		}
		children[index] = child
	}
	var reviewer *RunAdmissionResult
	if planned := root.Composite.Plan.Reviewer; planned != nil {
		var (
			reviewerTenant    string
			reviewerAdmission string
			reviewerIntent    string
			reviewerWorkspace string
		)
		if err := connection.QueryRowContext(ctx, `
			SELECT tenant_id, admission_key, admission_intent_digest, workspace_id
			FROM runs
			WHERE run_id=?
		`, planned.RunID).Scan(
			&reviewerTenant,
			&reviewerAdmission,
			&reviewerIntent,
			&reviewerWorkspace,
		); err != nil || reviewerTenant != tenantID ||
			reviewerAdmission != planned.AdmissionKey ||
			reviewerWorkspace != root.Workspace.ID {
			return CompositeRunFamilyAdmissionResult{}, true, fmt.Errorf(
				"%w: composite Reviewer %q identity projection",
				ErrAdmissionIntegrity,
				planned.RunID,
			)
		}
		loaded, err := loadAdmissionClosure(
			ctx,
			connection,
			planned.RunID,
			reviewerTenant,
			reviewerAdmission,
			reviewerIntent,
			reviewerWorkspace,
		)
		if err != nil ||
			loaded.MemberSnapshotDigest != planned.MemberSnapshotDigest {
			return CompositeRunFamilyAdmissionResult{}, true, fmt.Errorf(
				"%w: composite Reviewer %q closure: %v",
				ErrAdmissionIntegrity,
				planned.RunID,
				err,
			)
		}
		reviewer = &loaded
	}
	var repairChildren []RunAdmissionResult
	var repairReviewer *RunAdmissionResult
	if decision := root.Composite.Plan.Decision; decision != nil {
		repairChildren = make(
			[]RunAdmissionResult,
			len(decision.RepairChildren),
		)
		for index, planned := range decision.RepairChildren {
			expectedWorkspace, workspaceErr := compositeChildWorkspaceForPlan(
				root,
				planned,
			)
			if workspaceErr != nil {
				return CompositeRunFamilyAdmissionResult{}, true, fmt.Errorf(
					"%w: composite repair Child %q Workspace plan: %v",
					ErrAdmissionIntegrity,
					planned.RunID,
					workspaceErr,
				)
			}
			loaded, loadErr := loadCompositePlannedAdmissionResult(
				ctx,
				connection,
				root,
				planned.RunID,
				planned.AdmissionKey,
				planned.MemberSnapshotDigest,
				expectedWorkspace,
			)
			if loadErr != nil {
				return CompositeRunFamilyAdmissionResult{}, true, fmt.Errorf(
					"%w: composite repair Child %q closure: %v",
					ErrAdmissionIntegrity,
					planned.RunID,
					loadErr,
				)
			}
			repairChildren[index] = loaded
		}
		planned := decision.RepairReviewer
		loaded, loadErr := loadCompositePlannedAdmissionResult(
			ctx,
			connection,
			root,
			planned.RunID,
			planned.AdmissionKey,
			planned.MemberSnapshotDigest,
			root.Workspace,
		)
		if loadErr != nil {
			return CompositeRunFamilyAdmissionResult{}, true, fmt.Errorf(
				"%w: composite repair Reviewer %q closure: %v",
				ErrAdmissionIntegrity,
				planned.RunID,
				loadErr,
			)
		}
		repairReviewer = &loaded
	}
	if err := validatePersistedCompositeWorkspaceTransferClosure(
		ctx,
		connection,
		root,
	); err != nil {
		return CompositeRunFamilyAdmissionResult{}, true, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CompositeRunFamilyAdmissionResult{}, true, fmt.Errorf(
			"currentstore: commit composite family resolution: %w", err,
		)
	}
	committed = true
	return CompositeRunFamilyAdmissionResult{
		Parent:         parent,
		Children:       children,
		Reviewer:       reviewer,
		RepairChildren: repairChildren,
		RepairReviewer: repairReviewer,
		Created:        false,
	}, true, nil
}

// CommitCompositeRunFamily is the only Admission API allowed to publish a
// composite-run-node/v1. It deliberately does not schedule or execute Runs.
func (store *Store) CommitCompositeRunFamily(
	ctx context.Context,
	input CommitCompositeRunFamilyInput,
) (CompositeRunFamilyAdmissionResult, error) {
	if ctx == nil {
		return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidAdmission,
		)
	}
	if len(input.Children) < corecontract.CompositeMinChildrenV1 ||
		len(input.Children) > corecontract.CompositeMaxChildrenV1 {
		return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
			"%w: composite family requires between %d and %d Children",
			ErrInvalidAdmission,
			corecontract.CompositeMinChildrenV1,
			corecontract.CompositeMaxChildrenV1,
		)
	}

	parent, err := prepareCompleteRunAdmission(input.Parent)
	if err != nil {
		return CompositeRunFamilyAdmissionResult{}, err
	}
	children := make([]preparedRunAdmission, len(input.Children))
	for index, childInput := range input.Children {
		child, prepareErr := prepareCompleteRunAdmission(childInput)
		if prepareErr != nil {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: prepare composite Child %d: %v",
				ErrInvalidAdmission,
				index,
				prepareErr,
			)
		}
		children[index] = child
	}
	var reviewer *preparedRunAdmission
	if input.Reviewer != nil {
		prepared, prepareErr := prepareCompleteRunAdmission(*input.Reviewer)
		if prepareErr != nil {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: prepare composite Reviewer: %v",
				ErrInvalidAdmission,
				prepareErr,
			)
		}
		reviewer = &prepared
	}
	repairChildren := make(
		[]preparedRunAdmission,
		len(input.RepairChildren),
	)
	for index, childInput := range input.RepairChildren {
		child, prepareErr := prepareCompleteRunAdmission(childInput)
		if prepareErr != nil {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: prepare composite repair Child %d: %v",
				ErrInvalidAdmission,
				index,
				prepareErr,
			)
		}
		repairChildren[index] = child
	}
	var repairReviewer *preparedRunAdmission
	if input.RepairReviewer != nil {
		prepared, prepareErr := prepareCompleteRunAdmission(*input.RepairReviewer)
		if prepareErr != nil {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: prepare composite repair Reviewer: %v",
				ErrInvalidAdmission,
				prepareErr,
			)
		}
		repairReviewer = &prepared
	}
	if err := validatePreparedCompositeFamily(
		parent,
		children,
		reviewer,
		repairChildren,
		repairReviewer,
	); err != nil {
		return CompositeRunFamilyAdmissionResult{}, err
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return CompositeRunFamilyAdmissionResult{}, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
			"currentstore: acquire composite Admission connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
			"currentstore: begin composite Admission: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	existing, found, err := resolveAdmissionWithQueryer(
		ctx,
		connection,
		parent.intent.TenantID,
		parent.intent.AdmissionKey,
		parent.input.IntentDigest,
	)
	if err != nil {
		return CompositeRunFamilyAdmissionResult{}, err
	}
	if found {
		result, err := loadExactCompositeFamily(
			ctx,
			connection,
			existing,
			parent,
			children,
			reviewer,
			repairChildren,
			repairReviewer,
		)
		if err != nil {
			return CompositeRunFamilyAdmissionResult{}, err
		}
		if err := validatePersistedCompositeWorkspaceTransferClosure(
			ctx,
			connection,
			parent.manifest,
		); err != nil {
			return CompositeRunFamilyAdmissionResult{}, err
		}
		if err := verifyCompositeRunFamilyObservationsV1(ctx, connection, result); err != nil {
			return CompositeRunFamilyAdmissionResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"currentstore: commit idempotent composite Admission: %w",
				err,
			)
		}
		committed = true
		return result, nil
	}

	control, _, err := verifyCurrentAdmissionBasis(
		ctx,
		connection,
		parent.input.PublishedBasis,
	)
	if err != nil {
		return CompositeRunFamilyAdmissionResult{}, err
	}
	if err := validateCompositeControlAndCapabilities(
		ctx,
		connection,
		control,
		parent,
		children,
		reviewer,
		repairChildren,
		repairReviewer,
	); err != nil {
		return CompositeRunFamilyAdmissionResult{}, err
	}
	for index := range children {
		_, childFound, resolveErr := resolveAdmissionWithQueryer(
			ctx,
			connection,
			children[index].intent.TenantID,
			children[index].intent.AdmissionKey,
			children[index].input.IntentDigest,
		)
		if resolveErr != nil {
			return CompositeRunFamilyAdmissionResult{}, resolveErr
		}
		if childFound {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: composite Child %d exists without its Parent Admission",
				ErrAdmissionIntegrity,
				index,
			)
		}
	}
	if reviewer != nil {
		_, reviewerFound, resolveErr := resolveAdmissionWithQueryer(
			ctx,
			connection,
			reviewer.intent.TenantID,
			reviewer.intent.AdmissionKey,
			reviewer.input.IntentDigest,
		)
		if resolveErr != nil {
			return CompositeRunFamilyAdmissionResult{}, resolveErr
		}
		if reviewerFound {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: composite Reviewer exists without its Parent Admission",
				ErrAdmissionIntegrity,
			)
		}
	}
	for index := range repairChildren {
		_, foundRepair, resolveErr := resolveAdmissionWithQueryer(
			ctx,
			connection,
			repairChildren[index].intent.TenantID,
			repairChildren[index].intent.AdmissionKey,
			repairChildren[index].input.IntentDigest,
		)
		if resolveErr != nil {
			return CompositeRunFamilyAdmissionResult{}, resolveErr
		}
		if foundRepair {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: composite repair Child %d exists without its Parent Admission",
				ErrAdmissionIntegrity,
				index,
			)
		}
	}
	if repairReviewer != nil {
		_, foundRepairReviewer, resolveErr := resolveAdmissionWithQueryer(
			ctx,
			connection,
			repairReviewer.intent.TenantID,
			repairReviewer.intent.AdmissionKey,
			repairReviewer.input.IntentDigest,
		)
		if resolveErr != nil {
			return CompositeRunFamilyAdmissionResult{}, resolveErr
		}
		if foundRepairReviewer {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: composite repair Reviewer exists without its Parent Admission",
				ErrAdmissionIntegrity,
			)
		}
	}

	createdAt := nowUnixMicro()
	if createdAt <= 0 {
		return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
			"%w: invalid composite Admission time",
			ErrAdmissionIntegrity,
		)
	}
	parentResult, err := publishPreparedRunAdmission(
		ctx,
		connection,
		parent,
		createdAt,
	)
	if err != nil {
		return CompositeRunFamilyAdmissionResult{}, err
	}
	childResults := make([]RunAdmissionResult, len(children))
	for index, child := range children {
		childResult, publishErr := publishPreparedRunAdmission(
			ctx,
			connection,
			child,
			createdAt,
		)
		if publishErr != nil {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"currentstore: publish composite Child %d: %w",
				index,
				publishErr,
			)
		}
		childResult.Created = true
		childResults[index] = childResult
	}
	var reviewerResult *RunAdmissionResult
	if reviewer != nil {
		published, publishErr := publishPreparedRunAdmission(
			ctx,
			connection,
			*reviewer,
			createdAt,
		)
		if publishErr != nil {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"currentstore: publish composite Reviewer: %w",
				publishErr,
			)
		}
		published.Created = true
		reviewerResult = &published
	}
	repairChildResults := make([]RunAdmissionResult, len(repairChildren))
	for index, child := range repairChildren {
		published, publishErr := publishPreparedRunAdmission(
			ctx,
			connection,
			child,
			createdAt,
		)
		if publishErr != nil {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"currentstore: publish composite repair Child %d: %w",
				index,
				publishErr,
			)
		}
		published.Created = true
		repairChildResults[index] = published
	}
	var repairReviewerResult *RunAdmissionResult
	if repairReviewer != nil {
		published, publishErr := publishPreparedRunAdmission(
			ctx,
			connection,
			*repairReviewer,
			createdAt,
		)
		if publishErr != nil {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"currentstore: publish composite repair Reviewer: %w",
				publishErr,
			)
		}
		published.Created = true
		repairReviewerResult = &published
	}
	if err := verifyPersistedCompositeFamily(
		ctx,
		connection,
		parent,
		children,
		reviewer,
		repairChildren,
		repairReviewer,
	); err != nil {
		return CompositeRunFamilyAdmissionResult{}, err
	}
	if err := validatePersistedCompositeWorkspaceTransferClosure(
		ctx,
		connection,
		parent.manifest,
	); err != nil {
		return CompositeRunFamilyAdmissionResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
			"currentstore: commit composite Admission: %w",
			err,
		)
	}
	committed = true
	parentResult.Created = true
	return CompositeRunFamilyAdmissionResult{
		Parent:         parentResult,
		Children:       childResults,
		Reviewer:       reviewerResult,
		RepairChildren: repairChildResults,
		RepairReviewer: repairReviewerResult,
		Created:        true,
	}, nil
}

func verifyCompositeRunFamilyObservationsV1(
	ctx context.Context,
	q readQueryerV1,
	result CompositeRunFamilyAdmissionResult,
) error {
	ids := make([]string, 0, 1+len(result.Children)+len(result.RepairChildren)+2)
	ids = append(ids, result.Parent.RunID)
	for _, child := range result.Children {
		ids = append(ids, child.RunID)
	}
	if result.Reviewer != nil {
		ids = append(ids, result.Reviewer.RunID)
	}
	for _, child := range result.RepairChildren {
		ids = append(ids, child.RunID)
	}
	if result.RepairReviewer != nil {
		ids = append(ids, result.RepairReviewer.RunID)
	}
	for _, runID := range ids {
		if err := verifyCurrentRunObservationV1(ctx, q, runID); err != nil {
			return err
		}
	}
	return nil
}

func validatePreparedCompositeFamily(
	parent preparedRunAdmission,
	children []preparedRunAdmission,
	reviewer *preparedRunAdmission,
	repairChildren []preparedRunAdmission,
	repairReviewer *preparedRunAdmission,
) error {
	allRuns := append([]preparedRunAdmission{parent}, children...)
	if reviewer != nil {
		allRuns = append(allRuns, *reviewer)
	}
	allRuns = append(allRuns, repairChildren...)
	if repairReviewer != nil {
		allRuns = append(allRuns, *repairReviewer)
	}
	for index, run := range allRuns {
		if run.intent.ConversationTurn != nil ||
			run.manifest.ConversationTurn != nil {
			return fmt.Errorf(
				"%w: composite participant %d cannot be a Conversation Run",
				ErrInvalidAdmission,
				index,
			)
		}
	}
	root := parent.manifest.Composite
	if root == nil || root.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Plan == nil || len(root.Plan.Children) != len(children) {
		return fmt.Errorf(
			"%w: Parent does not freeze the supplied composite Child set",
			ErrInvalidAdmission,
		)
	}
	if parent.intent.CancellationScope != corecontract.CancellationScopeFamilyV1 ||
		parent.manifest.CancellationScope != corecontract.CancellationScopeFamilyV1 {
		return fmt.Errorf(
			"%w: composite Parent requires family cancellation scope",
			ErrInvalidAdmission,
		)
	}
	for index, child := range children {
		planned := root.Plan.Children[index]
		node := child.manifest.Composite
		expectedWorkspace, workspaceErr := compositeChildWorkspaceForPlan(
			parent.manifest,
			planned,
		)
		if child.input.PublishedBasis != parent.input.PublishedBasis ||
			workspaceErr != nil ||
			node == nil || node.Role != corecontract.CompositeRunRoleChildV1 ||
			child.manifest.TenantID != parent.manifest.TenantID ||
			child.manifest.Workspace != expectedWorkspace ||
			child.member.Workspace != expectedWorkspace ||
			!child.manifest.Deadline.Equal(parent.manifest.Deadline) ||
			child.manifest.ParentRunID != parent.manifest.RunID ||
			node.RootRunID != parent.manifest.RunID ||
			node.ParentManifestDigest != parent.manifest.ManifestDigest ||
			node.ParentSlotID != planned.SlotID || node.Assignment == nil ||
			*node.Assignment != planned.Assignment ||
			child.manifest.RunID != planned.RunID ||
			child.manifest.AdmissionKey != planned.AdmissionKey ||
			child.member.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
			child.member.Agent != planned.Agent ||
			child.member.Profile != planned.Profile ||
			child.manifest.TaskInputRef != planned.TaskInputRef ||
			child.manifest.TaskInputRef != parent.manifest.TaskInputRef ||
			child.intent.CancellationScope != corecontract.CancellationScopeInheritedV1 ||
			child.manifest.CancellationScope != corecontract.CancellationScopeInheritedV1 {
			return fmt.Errorf(
				"%w: composite Child %d does not close to the Parent plan",
				ErrInvalidAdmission,
				index,
			)
		}
	}
	plannedReviewer := root.Plan.Reviewer
	if (plannedReviewer == nil) != (reviewer == nil) {
		return fmt.Errorf(
			"%w: Parent Reviewer plan and supplied Reviewer differ",
			ErrInvalidAdmission,
		)
	}
	if plannedReviewer != nil {
		node := reviewer.manifest.Composite
		if reviewer.input.PublishedBasis != parent.input.PublishedBasis ||
			node == nil ||
			node.Role != corecontract.CompositeRunRoleReviewerV1 ||
			reviewer.manifest.TenantID != parent.manifest.TenantID ||
			reviewer.manifest.Workspace != parent.manifest.Workspace ||
			!reviewer.manifest.Deadline.Equal(parent.manifest.Deadline) ||
			reviewer.manifest.ParentRunID != parent.manifest.RunID ||
			node.RootRunID != parent.manifest.RunID ||
			node.ParentManifestDigest != parent.manifest.ManifestDigest ||
			node.ParentSlotID != corecontract.CompositeReviewerParentSlotIDV1 ||
			node.Assignment != nil || node.Plan != nil ||
			reviewer.manifest.RunID != plannedReviewer.RunID ||
			reviewer.manifest.AdmissionKey != plannedReviewer.AdmissionKey ||
			reviewer.member.MemberSnapshotDigest != plannedReviewer.MemberSnapshotDigest ||
			reviewer.member.Agent != plannedReviewer.Agent ||
			reviewer.member.Profile != plannedReviewer.Profile ||
			reviewer.manifest.TaskInputRef != plannedReviewer.TaskInputRef ||
			reviewer.manifest.TaskInputRef != parent.manifest.TaskInputRef ||
			reviewer.intent.CancellationScope != corecontract.CancellationScopeInheritedV1 ||
			reviewer.manifest.CancellationScope != corecontract.CancellationScopeInheritedV1 {
			return fmt.Errorf(
				"%w: composite Reviewer does not close to the Parent plan",
				ErrInvalidAdmission,
			)
		}
	}
	plannedDecision := root.Plan.Decision
	if (plannedDecision == nil) != (len(repairChildren) == 0) ||
		(plannedDecision == nil) != (repairReviewer == nil) {
		return fmt.Errorf(
			"%w: Parent decision plan and supplied repair Runs differ",
			ErrInvalidAdmission,
		)
	}
	if plannedDecision == nil {
		return nil
	}
	if len(plannedDecision.RepairChildren) != len(repairChildren) {
		return fmt.Errorf(
			"%w: Parent decision plan repair cardinality differs",
			ErrInvalidAdmission,
		)
	}
	for index, child := range repairChildren {
		planned := plannedDecision.RepairChildren[index]
		node := child.manifest.Composite
		expectedWorkspace, workspaceErr := compositeChildWorkspaceForPlan(
			parent.manifest,
			planned,
		)
		if child.input.PublishedBasis != parent.input.PublishedBasis ||
			workspaceErr != nil ||
			node == nil || node.Role != corecontract.CompositeRunRoleChildV1 ||
			node.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
			child.manifest.TenantID != parent.manifest.TenantID ||
			child.manifest.Workspace != expectedWorkspace ||
			child.member.Workspace != expectedWorkspace ||
			!child.manifest.Deadline.Equal(parent.manifest.Deadline) ||
			child.manifest.ParentRunID != parent.manifest.RunID ||
			node.RootRunID != parent.manifest.RunID ||
			node.ParentManifestDigest != parent.manifest.ManifestDigest ||
			node.ParentSlotID != planned.ParentSlotID ||
			node.Assignment == nil || *node.Assignment != planned.Assignment ||
			child.manifest.RunID != planned.RunID ||
			child.manifest.AdmissionKey != planned.AdmissionKey ||
			child.member.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
			child.member.Agent != planned.Agent ||
			child.member.Profile != planned.Profile ||
			child.manifest.TaskInputRef != planned.TaskInputRef ||
			child.manifest.TaskInputRef != parent.manifest.TaskInputRef ||
			child.intent.CancellationScope != corecontract.CancellationScopeInheritedV1 ||
			child.manifest.CancellationScope != corecontract.CancellationScopeInheritedV1 {
			return fmt.Errorf(
				"%w: composite repair Child %d does not close to the Parent decision plan",
				ErrInvalidAdmission,
				index,
			)
		}
	}
	plannedRepairReviewer := plannedDecision.RepairReviewer
	node := repairReviewer.manifest.Composite
	if repairReviewer.input.PublishedBasis != parent.input.PublishedBasis ||
		node == nil || node.Role != corecontract.CompositeRunRoleReviewerV1 ||
		node.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
		repairReviewer.manifest.TenantID != parent.manifest.TenantID ||
		repairReviewer.manifest.Workspace != parent.manifest.Workspace ||
		!repairReviewer.manifest.Deadline.Equal(parent.manifest.Deadline) ||
		repairReviewer.manifest.ParentRunID != parent.manifest.RunID ||
		node.RootRunID != parent.manifest.RunID ||
		node.ParentManifestDigest != parent.manifest.ManifestDigest ||
		node.ParentSlotID != plannedRepairReviewer.ParentSlotID ||
		node.Assignment != nil || node.Plan != nil ||
		repairReviewer.manifest.RunID != plannedRepairReviewer.RunID ||
		repairReviewer.manifest.AdmissionKey != plannedRepairReviewer.AdmissionKey ||
		repairReviewer.member.MemberSnapshotDigest != plannedRepairReviewer.MemberSnapshotDigest ||
		repairReviewer.member.Agent != plannedRepairReviewer.Agent ||
		repairReviewer.member.Profile != plannedRepairReviewer.Profile ||
		repairReviewer.manifest.TaskInputRef != plannedRepairReviewer.TaskInputRef ||
		repairReviewer.manifest.TaskInputRef != parent.manifest.TaskInputRef ||
		repairReviewer.intent.CancellationScope != corecontract.CancellationScopeInheritedV1 ||
		repairReviewer.manifest.CancellationScope != corecontract.CancellationScopeInheritedV1 {
		return fmt.Errorf(
			"%w: composite repair Reviewer does not close to the Parent decision plan",
			ErrInvalidAdmission,
		)
	}
	return nil
}

func validateCompositeControlAndCapabilities(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	control controlcontract.ControlSnapshot,
	parent preparedRunAdmission,
	children []preparedRunAdmission,
	reviewer *preparedRunAdmission,
	repairChildren []preparedRunAdmission,
	repairReviewer *preparedRunAdmission,
) error {
	definition, found := control.FindCompositeAgent(parent.member.Agent.ID)
	if !found || definition.CoordinatorProfileID != parent.member.Profile.ID ||
		len(definition.Members) != len(children) {
		return fmt.Errorf(
			"%w: Parent is not the selected frozen Composite Agent",
			ErrAdmissionConflict,
		)
	}
	bySlot := make(map[string]controlcontract.CompositeAgentMemberV1, len(definition.Members))
	for _, member := range definition.Members {
		bySlot[member.SlotID] = member
	}
	all := append([]preparedRunAdmission{parent}, children...)
	if reviewer != nil {
		all = append(all, *reviewer)
	}
	all = append(all, repairChildren...)
	if repairReviewer != nil {
		all = append(all, *repairReviewer)
	}
	for index, run := range all {
		if len(run.member.Actions) != 0 ||
			memberHasCompositeForbiddenPort(run.member) {
			return fmt.Errorf(
				"%w: composite participant %d binds Action, MCP, or Channel",
				ErrInvalidAdmission,
				index,
			)
		}
		memory, err := frozenMemoryBindingsForRun(
			run.manifest,
			run.member,
			func(digest string) (ContentRecord, error) {
				return prospectiveAdmissionContent(
					ctx,
					queryer,
					run.contents,
					digest,
				)
			},
		)
		if err != nil {
			return fmt.Errorf(
				"%w: composite participant %d Memory closure: %v",
				ErrAdmissionIntegrity,
				index,
				err,
			)
		}
		if len(memory) != 0 {
			return fmt.Errorf(
				"%w: composite participant %d binds mutable Memory",
				ErrInvalidAdmission,
				index,
			)
		}
	}
	for _, planned := range parent.manifest.Composite.Plan.Children {
		configured, ok := bySlot[planned.SlotID]
		if !ok || configured.AgentID != planned.Agent.ID ||
			configured.ProfileID != planned.Profile.ID ||
			configured.FocusID != planned.Assignment.FocusID ||
			configured.WeightBasisPoints != planned.Assignment.WeightBasisPoints ||
			validateCompositePlannedWorkspaceTransfer(
				control,
				definition,
				parent.manifest,
				configured,
				planned,
			) != nil {
			return fmt.Errorf(
				"%w: Parent plan differs from frozen Composite Agent slot %q",
				ErrAdmissionConflict,
				planned.SlotID,
			)
		}
	}
	plannedReviewer := parent.manifest.Composite.Plan.Reviewer
	if (definition.Reviewer == nil) != (plannedReviewer == nil) ||
		(plannedReviewer == nil) != (reviewer == nil) {
		return fmt.Errorf(
			"%w: frozen Composite Reviewer selection differs",
			ErrAdmissionConflict,
		)
	}
	if plannedReviewer != nil {
		configured := definition.Reviewer
		if configured.AgentID != plannedReviewer.Agent.ID ||
			configured.ProfileID != plannedReviewer.Profile.ID ||
			configured.Policy != plannedReviewer.Policy ||
			configured.MaxOutputTokens != plannedReviewer.MaxOutputTokens ||
			plannedReviewer.ReviewLogicalStepID !=
				corecontract.CompositeReviewLogicalStepIDV1 ||
			reviewer.member.Agent != plannedReviewer.Agent ||
			reviewer.member.Profile != plannedReviewer.Profile {
			return fmt.Errorf(
				"%w: Parent plan differs from frozen Composite Reviewer",
				ErrAdmissionConflict,
			)
		}
	}
	plannedDecision := parent.manifest.Composite.Plan.Decision
	if (definition.Decision == nil) != (plannedDecision == nil) ||
		(plannedDecision == nil) != (len(repairChildren) == 0) ||
		(plannedDecision == nil) != (repairReviewer == nil) {
		return fmt.Errorf(
			"%w: frozen Composite decision selection differs",
			ErrAdmissionConflict,
		)
	}
	if plannedDecision != nil {
		if definition.Decision.SchemaVersion !=
			controlcontract.CompositeDecisionSchemaVersionV1 ||
			plannedDecision.SchemaVersion !=
				corecontract.CompositeDecisionPlanSchemaVersionV1 ||
			len(plannedDecision.RepairChildren) != len(children) ||
			len(repairChildren) != len(children) {
			return fmt.Errorf(
				"%w: frozen Composite decision plan differs",
				ErrAdmissionConflict,
			)
		}
		for index, repair := range repairChildren {
			planned := plannedDecision.RepairChildren[index]
			configured, ok := bySlot[planned.SlotID]
			if !ok || configured.AgentID != planned.Agent.ID ||
				configured.ProfileID != planned.Profile.ID ||
				repair.member.Agent != planned.Agent ||
				repair.member.Profile != planned.Profile ||
				validateCompositePlannedWorkspaceTransfer(
					control,
					definition,
					parent.manifest,
					configured,
					planned,
				) != nil {
				return fmt.Errorf(
					"%w: frozen Composite repair slot %q differs",
					ErrAdmissionConflict,
					planned.SlotID,
				)
			}
		}
		planned := plannedDecision.RepairReviewer
		if definition.Reviewer == nil ||
			repairReviewer.member.Agent != planned.Agent ||
			repairReviewer.member.Profile != planned.Profile ||
			definition.Reviewer.AgentID != planned.Agent.ID ||
			definition.Reviewer.ProfileID != planned.Profile.ID {
			return fmt.Errorf(
				"%w: frozen Composite repair Reviewer differs",
				ErrAdmissionConflict,
			)
		}
	}
	return nil
}

// compositeChildWorkspaceForPlan resolves the only Workspace that one
// planned Child may bind. A nil Transfer preserves the legacy root Workspace;
// a non-nil Transfer must name the exact root and one distinct target.
func compositeChildWorkspaceForPlan(
	root corecontract.RunManifest,
	planned corecontract.CompositeChildRunRefV1,
) (corecontract.WorkspaceRef, error) {
	if planned.Transfer == nil {
		return root.Workspace, nil
	}
	if err := planned.Transfer.Validate(); err != nil ||
		planned.Transfer.RootWorkspace != root.Workspace {
		return corecontract.WorkspaceRef{}, fmt.Errorf(
			"invalid composite Workspace transfer plan",
		)
	}
	return planned.Transfer.TargetWorkspace, nil
}

// validateCompositePlannedWorkspaceTransfer closes a planned slot to the
// same immutable ControlSnapshot used by Admission. Grant refs in a Manifest
// never grant authority by themselves.
func validateCompositePlannedWorkspaceTransfer(
	control controlcontract.ControlSnapshot,
	definition controlcontract.CompositeAgentDefinitionV1,
	root corecontract.RunManifest,
	configured controlcontract.CompositeAgentMemberV1,
	planned corecontract.CompositeChildRunRefV1,
) error {
	expectedWorkspace, err := compositeChildWorkspaceForPlan(root, planned)
	if err != nil {
		return err
	}
	targetID := configured.TargetWorkspaceID
	if targetID == "" || targetID == root.Workspace.ID {
		if planned.Transfer != nil || expectedWorkspace != root.Workspace {
			return fmt.Errorf("unexpected composite Workspace transfer")
		}
		return nil
	}
	if definition.Decision == nil || planned.Transfer == nil ||
		targetID != expectedWorkspace.ID {
		return fmt.Errorf("composite Workspace transfer is not explicitly configured")
	}
	rootWorkspace, found := control.FindWorkspace(root.Workspace.ID)
	if !found || rootWorkspace.Workspace != root.Workspace {
		return fmt.Errorf("composite root Workspace is absent from frozen Control")
	}
	targetWorkspace, found := control.FindWorkspace(targetID)
	if !found || targetWorkspace.Workspace != expectedWorkspace {
		return fmt.Errorf("composite target Workspace is absent from frozen Control")
	}
	rootGrant, found := rootWorkspace.FindTransferGrantForPeer(
		targetWorkspace.Workspace,
	)
	if !found {
		return fmt.Errorf("composite root Workspace transfer grant is absent")
	}
	targetGrant, found := targetWorkspace.FindTransferGrantForPeer(
		rootWorkspace.Workspace,
	)
	if !found {
		return fmt.Errorf("composite target Workspace transfer grant is absent")
	}
	if err := planned.Transfer.ValidateAgainstGrantsV1(
		rootGrant,
		targetGrant,
	); err != nil {
		return err
	}
	return nil
}

type compositeParticipantClosureV1 struct {
	Manifest            corecontract.RunManifest
	ManifestCanonical   []byte
	Member              corecontract.MemberExecutionSnapshot
	MemberCanonical     []byte
	ControlSnapshotID   string
	CatalogGenerationID string
}

// workspaceTransferAuthorityV1 is a Host-only projection rebuilt from the
// historical ControlSnapshot selected at Admission. It is not a model
// context item and contains no current Control state.
type workspaceTransferAuthorityV1 struct {
	Plan                 corecontract.WorkspaceTransferPlanV1
	RootGrant            corecontract.WorkspaceTransferGrantV1
	RootGrantCanonical   []byte
	TargetGrant          corecontract.WorkspaceTransferGrantV1
	TargetGrantCanonical []byte
}

func loadCompositeParticipantClosureV1(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
) (compositeParticipantClosureV1, error) {
	var manifestCanonical []byte
	var manifestDigest string
	if err := queryer.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM run_manifests
		WHERE run_id=?
	`, runID).Scan(&manifestCanonical, &manifestDigest); err != nil {
		return compositeParticipantClosureV1{}, fmt.Errorf(
			"load composite participant Manifest %q: %w",
			runID,
			err,
		)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil || manifest.RunID != runID ||
		manifest.ManifestDigest != manifestDigest {
		return compositeParticipantClosureV1{}, fmt.Errorf(
			"%w: composite participant %q Manifest closure",
			ErrAdmissionIntegrity,
			runID,
		)
	}
	var memberCanonical []byte
	var memberDigest string
	var controlID string
	var catalogID string
	if err := queryer.QueryRowContext(ctx, `
		SELECT canonical_json, digest, control_snapshot_id,
		       catalog_generation_id
		FROM member_execution_snapshots
		WHERE run_id=? AND member_id=?
	`, runID, manifest.PrimaryMemberID).Scan(
		&memberCanonical,
		&memberDigest,
		&controlID,
		&catalogID,
	); err != nil {
		return compositeParticipantClosureV1{}, fmt.Errorf(
			"load composite participant member %q: %w",
			runID,
			err,
		)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil || member.MemberSnapshotDigest != memberDigest ||
		manifest.ValidateAgainstMember(member) != nil {
		return compositeParticipantClosureV1{}, fmt.Errorf(
			"%w: composite participant %q member closure",
			ErrAdmissionIntegrity,
			runID,
		)
	}
	return compositeParticipantClosureV1{
		Manifest:            manifest,
		ManifestCanonical:   append([]byte(nil), manifestCanonical...),
		Member:              member,
		MemberCanonical:     append([]byte(nil), memberCanonical...),
		ControlSnapshotID:   controlID,
		CatalogGenerationID: catalogID,
	}, nil
}

func plannedCompositeChildForManifest(
	root corecontract.RunManifest,
	child corecontract.RunManifest,
) (corecontract.CompositeChildRunRefV1, error) {
	if root.Composite == nil || root.Composite.Plan == nil ||
		child.Composite == nil || child.Composite.Assignment == nil {
		return corecontract.CompositeChildRunRefV1{}, ErrAdmissionIntegrity
	}
	var candidates []corecontract.CompositeChildRunRefV1
	switch child.Composite.RepairRound {
	case 0:
		candidates = root.Composite.Plan.Children
	case corecontract.CompositeRepairRoundOneV1:
		if root.Composite.Plan.Decision == nil {
			return corecontract.CompositeChildRunRefV1{}, ErrAdmissionIntegrity
		}
		candidates = root.Composite.Plan.Decision.RepairChildren
	default:
		return corecontract.CompositeChildRunRefV1{}, ErrAdmissionIntegrity
	}
	for _, planned := range candidates {
		if planned.RunID == child.RunID &&
			planned.SlotID == child.Composite.Assignment.SlotID {
			return planned, nil
		}
	}
	return corecontract.CompositeChildRunRefV1{}, ErrAdmissionIntegrity
}

// loadWorkspaceTransferAuthorityV1 closes one persisted Child against the
// exact ControlSnapshot IDs frozen by both MemberSnapshots. It deliberately
// never consults control_current, so later revocation affects only new Runs.
func loadWorkspaceTransferAuthorityV1(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	root compositeParticipantClosureV1,
	child compositeParticipantClosureV1,
) (*workspaceTransferAuthorityV1, error) {
	if root.Manifest.Composite == nil ||
		root.Manifest.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		child.Manifest.Composite == nil ||
		child.Manifest.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
		root.Manifest.TenantID != child.Manifest.TenantID ||
		root.ControlSnapshotID == "" || root.CatalogGenerationID == "" ||
		child.ControlSnapshotID != root.ControlSnapshotID ||
		child.CatalogGenerationID != root.CatalogGenerationID {
		return nil, fmt.Errorf(
			"%w: composite participants do not share one historical PublishedBasis",
			ErrAdmissionIntegrity,
		)
	}
	planned, err := plannedCompositeChildForManifest(
		root.Manifest,
		child.Manifest,
	)
	if err != nil || validateCompositeChildRunAgainstRoot(
		root.Manifest,
		child.Manifest,
		child.Member,
	) != nil {
		return nil, fmt.Errorf(
			"%w: persisted composite Child plan closure",
			ErrAdmissionIntegrity,
		)
	}
	expectedWorkspace, err := compositeChildWorkspaceForPlan(
		root.Manifest,
		planned,
	)
	if err != nil || child.Manifest.Workspace != expectedWorkspace ||
		child.Member.Workspace != expectedWorkspace {
		return nil, fmt.Errorf(
			"%w: persisted composite Child Workspace closure",
			ErrAdmissionIntegrity,
		)
	}
	control, catalog, err := loadAdmissionControlCatalog(
		ctx,
		queryer,
		root.Manifest.TenantID,
		root.ControlSnapshotID,
		root.CatalogGenerationID,
	)
	if err != nil {
		return nil, err
	}
	if err := verifyAdmissionControlMember(
		control,
		catalog,
		root.Member,
		root.Manifest,
	); err != nil {
		return nil, err
	}
	if err := verifyAdmissionControlMember(
		control,
		catalog,
		child.Member,
		child.Manifest,
	); err != nil {
		return nil, err
	}
	definition, found := control.FindCompositeAgent(root.Member.Agent.ID)
	if !found || definition.CoordinatorProfileID != root.Member.Profile.ID {
		return nil, fmt.Errorf(
			"%w: persisted composite definition is absent from historical Control",
			ErrAdmissionIntegrity,
		)
	}
	configured, found := definition.FindMember(planned.SlotID)
	if !found || configured.AgentID != planned.Agent.ID ||
		configured.ProfileID != planned.Profile.ID ||
		configured.FocusID != planned.Assignment.FocusID ||
		configured.WeightBasisPoints != planned.Assignment.WeightBasisPoints ||
		validateCompositePlannedWorkspaceTransfer(
			control,
			definition,
			root.Manifest,
			configured,
			planned,
		) != nil {
		return nil, fmt.Errorf(
			"%w: persisted composite transfer differs from historical Control",
			ErrAdmissionIntegrity,
		)
	}
	if planned.Transfer == nil {
		return nil, nil
	}
	rootWorkspace, _ := control.FindWorkspace(
		planned.Transfer.RootWorkspace.ID,
	)
	targetWorkspace, _ := control.FindWorkspace(
		planned.Transfer.TargetWorkspace.ID,
	)
	rootGrant, found := rootWorkspace.FindTransferGrantForPeer(
		targetWorkspace.Workspace,
	)
	if !found {
		return nil, fmt.Errorf("%w: historical root transfer grant", ErrAdmissionIntegrity)
	}
	targetGrant, found := targetWorkspace.FindTransferGrantForPeer(
		rootWorkspace.Workspace,
	)
	if !found {
		return nil, fmt.Errorf("%w: historical target transfer grant", ErrAdmissionIntegrity)
	}
	_, rootCanonical, rootDigest, err :=
		corecontract.NewWorkspaceTransferGrantV1(rootGrant)
	if err != nil || rootDigest != planned.Transfer.RootGrantDigest {
		return nil, fmt.Errorf("%w: historical root transfer grant digest", ErrAdmissionIntegrity)
	}
	_, targetCanonical, targetDigest, err :=
		corecontract.NewWorkspaceTransferGrantV1(targetGrant)
	if err != nil || targetDigest != planned.Transfer.TargetGrantDigest {
		return nil, fmt.Errorf("%w: historical target transfer grant digest", ErrAdmissionIntegrity)
	}
	return &workspaceTransferAuthorityV1{
		Plan:                 *planned.Transfer,
		RootGrant:            rootGrant,
		RootGrantCanonical:   append([]byte(nil), rootCanonical...),
		TargetGrant:          targetGrant,
		TargetGrantCanonical: append([]byte(nil), targetCanonical...),
	}, nil
}

func validatePersistedCompositeWorkspaceTransferClosure(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	rootManifest corecontract.RunManifest,
) error {
	if rootManifest.Composite == nil || rootManifest.Composite.Plan == nil {
		return fmt.Errorf(
			"%w: persisted composite root transfer plan",
			ErrAdmissionIntegrity,
		)
	}
	hasTransfer := false
	for _, planned := range rootManifest.Composite.Plan.Children {
		hasTransfer = hasTransfer || planned.Transfer != nil
	}
	if rootManifest.Composite.Plan.Decision != nil {
		for _, planned := range rootManifest.Composite.Plan.Decision.RepairChildren {
			hasTransfer = hasTransfer || planned.Transfer != nil
		}
	}
	if !hasTransfer {
		return nil
	}
	root, err := loadCompositeParticipantClosureV1(
		ctx,
		queryer,
		rootManifest.RunID,
	)
	if err != nil || root.Manifest.ManifestDigest != rootManifest.ManifestDigest {
		return fmt.Errorf(
			"%w: persisted composite root transfer closure: %v",
			ErrAdmissionIntegrity,
			err,
		)
	}
	children := append(
		[]corecontract.CompositeChildRunRefV1(nil),
		root.Manifest.Composite.Plan.Children...,
	)
	if root.Manifest.Composite.Plan.Decision != nil {
		children = append(
			children,
			root.Manifest.Composite.Plan.Decision.RepairChildren...,
		)
	}
	for _, planned := range children {
		child, loadErr := loadCompositeParticipantClosureV1(
			ctx,
			queryer,
			planned.RunID,
		)
		if loadErr != nil {
			return fmt.Errorf(
				"%w: persisted composite Child transfer closure: %v",
				ErrAdmissionIntegrity,
				loadErr,
			)
		}
		if _, loadErr = loadWorkspaceTransferAuthorityV1(
			ctx,
			queryer,
			root,
			child,
		); loadErr != nil {
			return loadErr
		}
	}
	return nil
}

func memberHasCompositeForbiddenPort(member corecontract.MemberExecutionSnapshot) bool {
	for _, plan := range member.PortPlans {
		switch plan.Port.Name {
		case moduleapi.PortNameActionProvider,
			moduleapi.PortNameChannelTransport:
			return true
		}
	}
	return false
}

func loadCompositePlannedAdmissionResult(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	root corecontract.RunManifest,
	runID string,
	admissionKey string,
	memberSnapshotDigest string,
	expectedWorkspace corecontract.WorkspaceRef,
) (RunAdmissionResult, error) {
	var (
		tenantID     string
		storedKey    string
		intentDigest string
		workspaceID  string
	)
	if err := queryer.QueryRowContext(ctx, `
		SELECT tenant_id, admission_key, admission_intent_digest, workspace_id
		FROM runs
		WHERE run_id=?
	`, runID).Scan(
		&tenantID,
		&storedKey,
		&intentDigest,
		&workspaceID,
	); err != nil || tenantID != root.TenantID ||
		storedKey != admissionKey || workspaceID != expectedWorkspace.ID {
		return RunAdmissionResult{}, fmt.Errorf(
			"composite participant %q identity projection: %w",
			runID,
			err,
		)
	}
	loaded, err := loadAdmissionClosure(
		ctx,
		queryer,
		runID,
		tenantID,
		storedKey,
		intentDigest,
		workspaceID,
	)
	if err != nil || loaded.MemberSnapshotDigest != memberSnapshotDigest {
		return RunAdmissionResult{}, fmt.Errorf(
			"composite participant %q admission closure: %w",
			runID,
			err,
		)
	}
	return loaded, nil
}

func loadExactCompositeFamily(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	existing RunAdmissionResult,
	parent preparedRunAdmission,
	children []preparedRunAdmission,
	reviewer *preparedRunAdmission,
	repairChildren []preparedRunAdmission,
	repairReviewer *preparedRunAdmission,
) (CompositeRunFamilyAdmissionResult, error) {
	if existing.RunID != parent.manifest.RunID ||
		existing.ManifestDigest != parent.manifest.ManifestDigest ||
		existing.MemberSnapshotDigest != parent.member.MemberSnapshotDigest {
		return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
			"%w: existing Parent differs from the composite retry",
			ErrAdmissionConflict,
		)
	}
	results := make([]RunAdmissionResult, len(children))
	for index, child := range children {
		result, found, err := resolveAdmissionWithQueryer(
			ctx,
			queryer,
			child.intent.TenantID,
			child.intent.AdmissionKey,
			child.input.IntentDigest,
		)
		if err != nil || !found {
			if err != nil {
				return CompositeRunFamilyAdmissionResult{}, err
			}
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: existing Parent is missing composite Child %d",
				ErrAdmissionIntegrity,
				index,
			)
		}
		if result.RunID != child.manifest.RunID ||
			result.ManifestDigest != child.manifest.ManifestDigest ||
			result.MemberSnapshotDigest != child.member.MemberSnapshotDigest {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: existing composite Child %d differs from the retry",
				ErrAdmissionConflict,
				index,
			)
		}
		results[index] = result
	}
	var reviewerResult *RunAdmissionResult
	if reviewer != nil {
		result, found, err := resolveAdmissionWithQueryer(
			ctx,
			queryer,
			reviewer.intent.TenantID,
			reviewer.intent.AdmissionKey,
			reviewer.input.IntentDigest,
		)
		if err != nil || !found {
			if err != nil {
				return CompositeRunFamilyAdmissionResult{}, err
			}
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: existing Parent is missing composite Reviewer",
				ErrAdmissionIntegrity,
			)
		}
		if result.RunID != reviewer.manifest.RunID ||
			result.ManifestDigest != reviewer.manifest.ManifestDigest ||
			result.MemberSnapshotDigest != reviewer.member.MemberSnapshotDigest {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: existing composite Reviewer differs from the retry",
				ErrAdmissionConflict,
			)
		}
		reviewerResult = &result
	}
	repairResults := make([]RunAdmissionResult, len(repairChildren))
	for index, child := range repairChildren {
		result, found, err := resolveAdmissionWithQueryer(
			ctx,
			queryer,
			child.intent.TenantID,
			child.intent.AdmissionKey,
			child.input.IntentDigest,
		)
		if err != nil || !found {
			if err != nil {
				return CompositeRunFamilyAdmissionResult{}, err
			}
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: existing Parent is missing composite repair Child %d",
				ErrAdmissionIntegrity,
				index,
			)
		}
		if result.RunID != child.manifest.RunID ||
			result.ManifestDigest != child.manifest.ManifestDigest ||
			result.MemberSnapshotDigest != child.member.MemberSnapshotDigest {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: existing composite repair Child %d differs from the retry",
				ErrAdmissionConflict,
				index,
			)
		}
		repairResults[index] = result
	}
	var repairReviewerResult *RunAdmissionResult
	if repairReviewer != nil {
		result, found, err := resolveAdmissionWithQueryer(
			ctx,
			queryer,
			repairReviewer.intent.TenantID,
			repairReviewer.intent.AdmissionKey,
			repairReviewer.input.IntentDigest,
		)
		if err != nil || !found {
			if err != nil {
				return CompositeRunFamilyAdmissionResult{}, err
			}
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: existing Parent is missing composite repair Reviewer",
				ErrAdmissionIntegrity,
			)
		}
		if result.RunID != repairReviewer.manifest.RunID ||
			result.ManifestDigest != repairReviewer.manifest.ManifestDigest ||
			result.MemberSnapshotDigest != repairReviewer.member.MemberSnapshotDigest {
			return CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
				"%w: existing composite repair Reviewer differs from the retry",
				ErrAdmissionConflict,
			)
		}
		repairReviewerResult = &result
	}
	return CompositeRunFamilyAdmissionResult{
		Parent:         existing,
		Children:       results,
		Reviewer:       reviewerResult,
		RepairChildren: repairResults,
		RepairReviewer: repairReviewerResult,
		Created:        false,
	}, nil
}

func verifyPersistedCompositeFamily(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	parent preparedRunAdmission,
	children []preparedRunAdmission,
	reviewer *preparedRunAdmission,
	repairChildren []preparedRunAdmission,
	repairReviewer *preparedRunAdmission,
) error {
	var count int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM runs
		WHERE parent_run_id=? AND parent_manifest_digest=?
	`, parent.manifest.RunID, parent.manifest.ManifestDigest).Scan(&count); err != nil {
		return fmt.Errorf(
			"currentstore: verify composite Child count: %w",
			err,
		)
	}
	wantCount := len(children)
	if reviewer != nil {
		wantCount++
	}
	wantCount += len(repairChildren)
	if repairReviewer != nil {
		wantCount++
	}
	if count != wantCount {
		return fmt.Errorf(
			"%w: persisted composite Child set has cardinality %d, want %d",
			ErrAdmissionIntegrity,
			count,
			wantCount,
		)
	}
	for _, child := range children {
		var runID string
		if err := queryer.QueryRowContext(ctx, `
			SELECT run_id
			FROM runs
			WHERE parent_run_id=?
			  AND parent_manifest_digest=?
			  AND parent_slot_id=?
		`,
			parent.manifest.RunID,
			parent.manifest.ManifestDigest,
			child.manifest.Composite.ParentSlotID,
		).Scan(&runID); err != nil || runID != child.manifest.RunID {
			return fmt.Errorf(
				"%w: persisted composite slot %q does not close",
				ErrAdmissionIntegrity,
				child.manifest.Composite.ParentSlotID,
			)
		}
	}
	if reviewer != nil {
		var runID string
		if err := queryer.QueryRowContext(ctx, `
			SELECT run_id
			FROM runs
			WHERE parent_run_id=?
			  AND parent_manifest_digest=?
			  AND parent_slot_id=?
		`,
			parent.manifest.RunID,
			parent.manifest.ManifestDigest,
			corecontract.CompositeReviewerParentSlotIDV1,
		).Scan(&runID); err != nil || runID != reviewer.manifest.RunID {
			return fmt.Errorf(
				"%w: persisted composite Reviewer does not close",
				ErrAdmissionIntegrity,
			)
		}
	}
	for _, child := range repairChildren {
		var runID string
		if err := queryer.QueryRowContext(ctx, `
			SELECT run_id
			FROM runs
			WHERE parent_run_id=?
			  AND parent_manifest_digest=?
			  AND parent_slot_id=?
		`,
			parent.manifest.RunID,
			parent.manifest.ManifestDigest,
			child.manifest.Composite.ParentSlotID,
		).Scan(&runID); err != nil || runID != child.manifest.RunID {
			return fmt.Errorf(
				"%w: persisted composite repair slot %q does not close",
				ErrAdmissionIntegrity,
				child.manifest.Composite.ParentSlotID,
			)
		}
	}
	if repairReviewer != nil {
		var runID string
		if err := queryer.QueryRowContext(ctx, `
			SELECT run_id
			FROM runs
			WHERE parent_run_id=?
			  AND parent_manifest_digest=?
			  AND parent_slot_id=?
		`,
			parent.manifest.RunID,
			parent.manifest.ManifestDigest,
			repairReviewer.manifest.Composite.ParentSlotID,
		).Scan(&runID); err != nil || runID != repairReviewer.manifest.RunID {
			return fmt.Errorf(
				"%w: persisted composite repair Reviewer does not close",
				ErrAdmissionIntegrity,
			)
		}
	}
	return nil
}
