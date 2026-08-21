package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidCompositeUsageProjection = errors.New(
		"currentstore: invalid composite usage projection",
	)
	ErrCompositeUsageProjectionIntegrity = errors.New(
		"currentstore: composite usage projection integrity violation",
	)
)

// CompositeFamilyCostStatusV1 makes an absent cost fact distinguishable from
// a known zero and from a set of known values that cannot be added because
// their frozen PriceSnapshots use different currencies.
type CompositeFamilyCostStatusV1 string

const (
	CompositeFamilyCostUnknownV1       CompositeFamilyCostStatusV1 = "UNKNOWN"
	CompositeFamilyCostKnownV1         CompositeFamilyCostStatusV1 = "KNOWN"
	CompositeFamilyCostMixedCurrencyV1 CompositeFamilyCostStatusV1 = "MIXED_CURRENCY"
)

// CompositeFamilyCostTotalV1 is one independently derived cost column.
// Value is present only for KNOWN. Currencies is present only for
// MIXED_CURRENCY and is sorted; Core never performs currency conversion.
type CompositeFamilyCostTotalV1 struct {
	Status     CompositeFamilyCostStatusV1
	Value      *string
	Currency   string
	Currencies []string
}

// CompositeFamilyTokenTotalsV1 preserves the nil-is-UNKNOWN semantics of
// model_usage. A field is present only when every projected Attempt has that
// field, and an empty family therefore remains unknown rather than becoming
// a synthetic zero.
type CompositeFamilyTokenTotalsV1 struct {
	Input         *uint64
	CachedInput   *uint64
	UncachedInput *uint64
	Output        *uint64
	Reasoning     *uint64
}

// CompositeFamilyAttemptUsageFactV1 is a detached fact for one already
// persisted model Attempt. Currency comes from that Attempt's immutable
// PriceSnapshot. PENDING and MODEL_UNKNOWN are retained without rewriting.
type CompositeFamilyAttemptUsageFactV1 struct {
	AttemptID           string
	LogicalStepID       string
	State               corecontract.ModelAttemptState
	Provider            string
	Model               string
	RequestDigest       string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	PriceSnapshotID     string
	PriceSnapshotDigest string
	Currency            string
	Usage               ModelUsageRecord
}

// CompositeFamilyRunUsageFactV1 contains zero or one model Attempt. More than
// one is an integrity error because the frozen Composite family grants exactly
// one model-dispatch slot to every physical Specialist/Reviewer Run and the
// Root merge. A dormant or skipped repair Run remains present with Attempt nil.
type CompositeFamilyRunUsageFactV1 struct {
	RunID   string
	Role    corecontract.CompositeRunRoleV1
	SlotID  string
	Attempt *CompositeFamilyAttemptUsageFactV1
}

// CompositeFamilyUsageAggregateV1 is derived from the same per-Run Usage
// ledger rows; it is not a second ledger, balance, reservation, or write path.
type CompositeFamilyUsageAggregateV1 struct {
	AttemptSlotsUsed     uint32
	TokenTotals          CompositeFamilyTokenTotalsV1
	EstimatedCost        CompositeFamilyCostTotalV1
	ProviderReportedCost CompositeFamilyCostTotalV1
	ReconciledCost       CompositeFamilyCostTotalV1
}

// CompositeFamilyUsageProjectionV1 preserves exact root-plan order: initial
// Specialists, the optional initial Reviewer, Decision repair Specialists,
// the repair Reviewer, then the Root merge Run. Decision families therefore
// retain every frozen physical Run, including dormant/skipped repairs with a
// nil Attempt. Reviewer-disabled and non-Decision families retain their
// original ordering.
type CompositeFamilyUsageProjectionV1 struct {
	RootRunID                string
	RootManifestDigest       string
	FamilyModelDispatchLimit uint32
	Runs                     []CompositeFamilyRunUsageFactV1
	Aggregate                CompositeFamilyUsageAggregateV1
}

// GetCompositeFamilyUsageProjection derives a coherent family usage/cost
// view in one SQLite read transaction. It accepts only an intact Composite
// ROOT. Ordinary Runs and Child Run IDs are rejected, and no table is written.
func (store *Store) GetCompositeFamilyUsageProjection(
	ctx context.Context,
	rootRunID string,
) (CompositeFamilyUsageProjectionV1, error) {
	if ctx == nil {
		return CompositeFamilyUsageProjectionV1{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidCompositeUsageProjection,
		)
	}
	if !validLeaseOpaqueID(rootRunID) {
		return CompositeFamilyUsageProjectionV1{}, fmt.Errorf(
			"%w: invalid root Run ID",
			ErrInvalidCompositeUsageProjection,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return CompositeFamilyUsageProjectionV1{}, err
	}
	defer unlock()

	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CompositeFamilyUsageProjectionV1{}, fmt.Errorf(
			"currentstore: begin composite usage projection: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	root, err := loadCompositeUsageManifest(ctx, tx, rootRunID)
	if err != nil {
		return CompositeFamilyUsageProjectionV1{}, err
	}
	if root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil {
		return CompositeFamilyUsageProjectionV1{}, fmt.Errorf(
			"%w: Run %q is not a Composite ROOT",
			ErrInvalidCompositeUsageProjection,
			rootRunID,
		)
	}
	plan := root.Composite.Plan
	if plan.FamilyModelDispatchLimit != uint32(compositeFamilyRunCount(plan)) {
		return CompositeFamilyUsageProjectionV1{}, compositeUsageIntegrity(
			"family model-dispatch limit does not equal the frozen family size",
		)
	}
	if err := verifyCompositeUsageGraph(ctx, tx, root); err != nil {
		return CompositeFamilyUsageProjectionV1{}, err
	}

	runs := make(
		[]CompositeFamilyRunUsageFactV1,
		0,
		compositeFamilyRunCount(plan),
	)
	manifests := make([]corecontract.RunManifest, 0, cap(runs))
	expectedLogicalSteps := make([]string, 0, cap(runs))
	for _, childRef := range plan.Children {
		child, fact, loadErr := loadCompositeUsageChild(
			ctx,
			tx,
			root,
			childRef,
			0,
		)
		if loadErr != nil {
			return CompositeFamilyUsageProjectionV1{}, loadErr
		}
		manifests = append(manifests, child)
		runs = append(runs, fact)
		expectedLogicalStep := ""
		if plan.Decision != nil {
			expectedLogicalStep = corecontract.PureChatModelLogicalStepIDV1
		}
		expectedLogicalSteps = append(expectedLogicalSteps, expectedLogicalStep)
	}
	if reviewerRef := plan.Reviewer; reviewerRef != nil {
		reviewer, fact, loadErr := loadCompositeUsageReviewer(
			ctx,
			tx,
			root,
			*reviewerRef,
			0,
		)
		if loadErr != nil {
			return CompositeFamilyUsageProjectionV1{}, loadErr
		}
		manifests = append(manifests, reviewer)
		runs = append(runs, fact)
		expectedLogicalSteps = append(
			expectedLogicalSteps,
			reviewerRef.ReviewLogicalStepID,
		)
	}
	if decision := plan.Decision; decision != nil {
		for _, childRef := range decision.RepairChildren {
			child, fact, loadErr := loadCompositeUsageChild(
				ctx,
				tx,
				root,
				childRef,
				corecontract.CompositeRepairRoundOneV1,
			)
			if loadErr != nil {
				return CompositeFamilyUsageProjectionV1{}, loadErr
			}
			manifests = append(manifests, child)
			runs = append(runs, fact)
			expectedLogicalSteps = append(
				expectedLogicalSteps,
				corecontract.PureChatModelLogicalStepIDV1,
			)
		}
		reviewer, fact, loadErr := loadCompositeUsageReviewer(
			ctx,
			tx,
			root,
			decision.RepairReviewer,
			corecontract.CompositeRepairRoundOneV1,
		)
		if loadErr != nil {
			return CompositeFamilyUsageProjectionV1{}, loadErr
		}
		manifests = append(manifests, reviewer)
		runs = append(runs, fact)
		expectedLogicalSteps = append(
			expectedLogicalSteps,
			decision.RepairReviewer.ReviewLogicalStepID,
		)
	}
	manifests = append(manifests, root)
	runs = append(runs, CompositeFamilyRunUsageFactV1{
		RunID: root.RunID,
		Role:  corecontract.CompositeRunRoleRootV1,
	})
	expectedLogicalSteps = append(expectedLogicalSteps, plan.MergeLogicalStepID)

	allAttempts := make([]CompositeFamilyAttemptUsageFactV1, 0, len(runs))
	for index := range runs {
		attempt, found, loadErr := loadCompositeRunUsageAttempt(
			ctx,
			tx,
			manifests[index],
			expectedLogicalSteps[index],
		)
		if loadErr != nil {
			return CompositeFamilyUsageProjectionV1{}, loadErr
		}
		if found {
			attemptCopy := attempt
			runs[index].Attempt = &attemptCopy
			allAttempts = append(allAttempts, attempt)
		}
	}
	if len(allAttempts) > int(plan.FamilyModelDispatchLimit) {
		return CompositeFamilyUsageProjectionV1{}, compositeUsageIntegrity(
			"persisted model Attempts exceed the frozen family cap",
		)
	}
	if err := verifyCompositeUsageRowCount(ctx, tx, root, len(allAttempts)); err != nil {
		return CompositeFamilyUsageProjectionV1{}, err
	}
	aggregate, err := aggregateCompositeFamilyUsage(allAttempts)
	if err != nil {
		return CompositeFamilyUsageProjectionV1{}, err
	}

	result := CompositeFamilyUsageProjectionV1{
		RootRunID:                root.RunID,
		RootManifestDigest:       root.ManifestDigest,
		FamilyModelDispatchLimit: plan.FamilyModelDispatchLimit,
		Runs:                     runs,
		Aggregate:                aggregate,
	}
	if err := tx.Commit(); err != nil {
		return CompositeFamilyUsageProjectionV1{}, fmt.Errorf(
			"currentstore: commit composite usage projection: %w",
			err,
		)
	}
	committed = true
	return result, nil
}

func loadCompositeUsageManifest(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
) (corecontract.RunManifest, error) {
	var tenantID, workspaceID, admissionKey, intentDigest string
	if err := queryer.QueryRowContext(ctx, `
		SELECT tenant_id, workspace_id, admission_key, admission_intent_digest
		FROM runs
		WHERE run_id=?
	`, runID).Scan(
		&tenantID,
		&workspaceID,
		&admissionKey,
		&intentDigest,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return corecontract.RunManifest{}, fmt.Errorf(
				"%w: Run %q does not exist",
				ErrInvalidCompositeUsageProjection,
				runID,
			)
		}
		return corecontract.RunManifest{}, fmt.Errorf(
			"currentstore: load composite usage Run identity: %w",
			err,
		)
	}
	closure, err := loadAdmissionClosure(
		ctx,
		queryer,
		runID,
		tenantID,
		admissionKey,
		intentDigest,
		workspaceID,
	)
	if err != nil {
		return corecontract.RunManifest{}, compositeUsageIntegrityError(
			"Run Admission closure",
			err,
		)
	}
	var canonical []byte
	var digest string
	if err := queryer.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM run_manifests
		WHERE run_id=?
	`, runID).Scan(&canonical, &digest); err != nil {
		return corecontract.RunManifest{}, compositeUsageIntegrityError(
			"Run Manifest",
			err,
		)
	}
	manifest, err := corecontract.RestoreRunManifest(canonical)
	if err != nil || manifest.RunID != runID ||
		manifest.ManifestDigest != digest ||
		closure.ManifestDigest != digest ||
		closure.MemberSnapshotDigest != manifest.Members[0].Digest {
		return corecontract.RunManifest{}, compositeUsageIntegrityError(
			"Run Manifest projection",
			err,
		)
	}
	return manifest, nil
}

func loadCompositeUsageMember(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	digest string,
) (corecontract.MemberExecutionSnapshot, error) {
	var canonical []byte
	if err := queryer.QueryRowContext(ctx, `
		SELECT canonical_json
		FROM member_execution_snapshots
		WHERE run_id=? AND digest=?
	`, runID, digest).Scan(&canonical); err != nil {
		return corecontract.MemberExecutionSnapshot{},
			compositeUsageIntegrityError("participant member snapshot", err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(canonical)
	if err != nil || member.MemberSnapshotDigest != digest {
		return corecontract.MemberExecutionSnapshot{},
			compositeUsageIntegrityError("participant member snapshot", err)
	}
	return member, nil
}

func loadCompositeUsageChild(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	root corecontract.RunManifest,
	planned corecontract.CompositeChildRunRefV1,
	repairRound uint32,
) (
	corecontract.RunManifest,
	CompositeFamilyRunUsageFactV1,
	error,
) {
	child, err := loadCompositeUsageManifest(ctx, queryer, planned.RunID)
	if err != nil {
		return corecontract.RunManifest{}, CompositeFamilyRunUsageFactV1{}, err
	}
	member, err := loadCompositeUsageMember(
		ctx,
		queryer,
		planned.RunID,
		planned.MemberSnapshotDigest,
	)
	if err != nil {
		return corecontract.RunManifest{}, CompositeFamilyRunUsageFactV1{}, err
	}
	parentSlotID := planned.SlotID
	if repairRound == corecontract.CompositeRepairRoundOneV1 {
		parentSlotID = planned.ParentSlotID
	}
	if child.Composite == nil ||
		child.Composite.Role != corecontract.CompositeRunRoleChildV1 ||
		child.Composite.RepairRound != repairRound ||
		child.Composite.RootRunID != root.RunID ||
		child.ParentRunID != root.RunID ||
		child.Composite.ParentManifestDigest != root.ManifestDigest ||
		child.Composite.ParentSlotID != parentSlotID ||
		child.Composite.Assignment == nil ||
		*child.Composite.Assignment != planned.Assignment ||
		child.Composite.Plan != nil ||
		child.RunID != planned.RunID ||
		child.AdmissionKey != planned.AdmissionKey ||
		child.Members[0].Digest != planned.MemberSnapshotDigest ||
		child.PrimaryMemberID != member.MemberID ||
		child.PrimaryAgent != planned.Agent ||
		member.Agent != planned.Agent ||
		member.Profile != planned.Profile ||
		child.TaskInputRef != planned.TaskInputRef {
		return corecontract.RunManifest{}, CompositeFamilyRunUsageFactV1{},
			compositeUsageIntegrity(
				fmt.Sprintf(
					"Child %q does not close the frozen root plan",
					planned.RunID,
				),
			)
	}
	return child, CompositeFamilyRunUsageFactV1{
		RunID:  child.RunID,
		Role:   corecontract.CompositeRunRoleChildV1,
		SlotID: planned.SlotID,
	}, nil
}

func loadCompositeUsageReviewer(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	root corecontract.RunManifest,
	planned corecontract.CompositeReviewerRunRefV1,
	repairRound uint32,
) (
	corecontract.RunManifest,
	CompositeFamilyRunUsageFactV1,
	error,
) {
	reviewer, err := loadCompositeUsageManifest(ctx, queryer, planned.RunID)
	if err != nil {
		return corecontract.RunManifest{}, CompositeFamilyRunUsageFactV1{}, err
	}
	member, err := loadCompositeUsageMember(
		ctx,
		queryer,
		planned.RunID,
		planned.MemberSnapshotDigest,
	)
	if err != nil {
		return corecontract.RunManifest{}, CompositeFamilyRunUsageFactV1{}, err
	}
	parentSlotID := corecontract.CompositeReviewerParentSlotIDV1
	if repairRound == corecontract.CompositeRepairRoundOneV1 {
		parentSlotID = planned.ParentSlotID
	}
	if reviewer.Composite == nil ||
		reviewer.Composite.Role != corecontract.CompositeRunRoleReviewerV1 ||
		reviewer.Composite.RepairRound != repairRound ||
		reviewer.Composite.RootRunID != root.RunID ||
		reviewer.ParentRunID != root.RunID ||
		reviewer.Composite.ParentManifestDigest != root.ManifestDigest ||
		reviewer.Composite.ParentSlotID != parentSlotID ||
		reviewer.Composite.Assignment != nil ||
		reviewer.Composite.Plan != nil ||
		reviewer.RunID != planned.RunID ||
		reviewer.AdmissionKey != planned.AdmissionKey ||
		reviewer.Members[0].Digest != planned.MemberSnapshotDigest ||
		reviewer.PrimaryMemberID != member.MemberID ||
		reviewer.PrimaryAgent != planned.Agent ||
		member.Agent != planned.Agent ||
		member.Profile != planned.Profile ||
		reviewer.TaskInputRef != planned.TaskInputRef {
		return corecontract.RunManifest{}, CompositeFamilyRunUsageFactV1{},
			compositeUsageIntegrity(
				fmt.Sprintf(
					"Reviewer %q does not close the frozen root plan",
					planned.RunID,
				),
			)
	}
	return reviewer, CompositeFamilyRunUsageFactV1{
		RunID:  reviewer.RunID,
		Role:   corecontract.CompositeRunRoleReviewerV1,
		SlotID: corecontract.CompositeReviewerParentSlotIDV1,
	}, nil
}

func verifyCompositeUsageGraph(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	root corecontract.RunManifest,
) error {
	var children int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM runs
		WHERE parent_run_id=?
	`, root.RunID).Scan(&children); err != nil {
		return compositeUsageIntegrityError("family cardinality", err)
	}
	expectedChildren := compositeFamilyRunCount(root.Composite.Plan) - 1
	if children != expectedChildren {
		return compositeUsageIntegrity(
			"persisted family cardinality differs from the root plan",
		)
	}
	var nested int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM runs AS nested
		JOIN runs AS child ON child.run_id=nested.parent_run_id
		WHERE child.parent_run_id=?
	`, root.RunID).Scan(&nested); err != nil {
		return compositeUsageIntegrityError("nested family scan", err)
	}
	if nested != 0 {
		return compositeUsageIntegrity("Composite family is not depth one")
	}
	return nil
}

func loadCompositeRunUsageAttempt(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	manifest corecontract.RunManifest,
	expectedLogicalStepID string,
) (CompositeFamilyAttemptUsageFactV1, bool, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT attempt_id
		FROM model_dispatch_attempts
		WHERE run_id=?
		ORDER BY created_at, attempt_id
	`, manifest.RunID)
	if err != nil {
		return CompositeFamilyAttemptUsageFactV1{}, false,
			compositeUsageIntegrityError("model Attempt scan", err)
	}
	defer rows.Close()
	var attemptIDs []string
	for rows.Next() {
		var attemptID string
		if err := rows.Scan(&attemptID); err != nil {
			return CompositeFamilyAttemptUsageFactV1{}, false,
				compositeUsageIntegrityError("model Attempt row", err)
		}
		attemptIDs = append(attemptIDs, attemptID)
	}
	if err := rows.Err(); err != nil {
		return CompositeFamilyAttemptUsageFactV1{}, false,
			compositeUsageIntegrityError("model Attempt iteration", err)
	}
	if len(attemptIDs) == 0 {
		return CompositeFamilyAttemptUsageFactV1{}, false, nil
	}
	if len(attemptIDs) != 1 {
		return CompositeFamilyAttemptUsageFactV1{}, false,
			compositeUsageIntegrity(
				fmt.Sprintf("Run %q has more than one model Attempt", manifest.RunID),
			)
	}
	record, err := queryModelDispatchRecord(ctx, queryer, attemptIDs[0])
	if err != nil {
		return CompositeFamilyAttemptUsageFactV1{}, false,
			compositeUsageIntegrityError("model Attempt/Usage closure", err)
	}
	if record.Attempt.RunID != manifest.RunID ||
		record.Attempt.MemberID != manifest.PrimaryMemberID ||
		record.Attempt.MemberSnapshotDigest != manifest.Members[0].Digest {
		return CompositeFamilyAttemptUsageFactV1{}, false,
			compositeUsageIntegrity("model Attempt does not belong to the frozen Run member")
	}
	if expectedLogicalStepID != "" &&
		record.Attempt.LogicalStepID != expectedLogicalStepID {
		return CompositeFamilyAttemptUsageFactV1{}, false,
			compositeUsageIntegrity(
				fmt.Sprintf(
					"Run %q model Attempt is not its frozen logical step",
					manifest.RunID,
				),
			)
	}
	if expectedLogicalStepID == "" &&
		(record.Attempt.LogicalStepID == corecontract.CompositeMergeLogicalStepIDV1 ||
			record.Attempt.LogicalStepID == corecontract.CompositeReviewLogicalStepIDV1) {
		return CompositeFamilyAttemptUsageFactV1{}, false,
			compositeUsageIntegrity(
				"legacy Specialist model Attempt uses a family coordination step",
			)
	}
	price, err := queryModelPriceSnapshot(ctx, queryer, record.Attempt.PriceSnapshotID)
	if err != nil {
		return CompositeFamilyAttemptUsageFactV1{}, false,
			compositeUsageIntegrityError("Attempt PriceSnapshot", err)
	}
	return CompositeFamilyAttemptUsageFactV1{
		AttemptID:           record.Attempt.AttemptID,
		LogicalStepID:       record.Attempt.LogicalStepID,
		State:               record.Attempt.State,
		Provider:            record.Attempt.Provider,
		Model:               record.Attempt.Model,
		RequestDigest:       record.Attempt.Request.Digest,
		CreatedAt:           record.Attempt.CreatedAt,
		UpdatedAt:           record.Attempt.UpdatedAt,
		PriceSnapshotID:     price.Snapshot.PriceSnapshotID,
		PriceSnapshotDigest: price.Snapshot.Digest,
		Currency:            price.Snapshot.Currency,
		Usage:               cloneModelUsageRecord(record.Usage),
	}, true, nil
}

func verifyCompositeUsageRowCount(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	root corecontract.RunManifest,
	want int,
) error {
	var attempts, usageRows int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM model_dispatch_attempts AS attempt
		JOIN runs AS family_run ON family_run.run_id=attempt.run_id
		WHERE family_run.run_id=? OR family_run.parent_run_id=?
	`, root.RunID, root.RunID).Scan(&attempts); err != nil {
		return compositeUsageIntegrityError("family model Attempt count", err)
	}
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM model_usage AS usage
		JOIN runs AS family_run ON family_run.run_id=usage.run_id
		WHERE family_run.run_id=? OR family_run.parent_run_id=?
	`, root.RunID, root.RunID).Scan(&usageRows); err != nil {
		return compositeUsageIntegrityError("family model Usage count", err)
	}
	if attempts != want || usageRows != want ||
		attempts > int(root.Composite.Plan.FamilyModelDispatchLimit) {
		return compositeUsageIntegrity(
			"family Attempt/Usage rows do not close or exceed the frozen cap",
		)
	}
	return nil
}

func aggregateCompositeFamilyUsage(
	attempts []CompositeFamilyAttemptUsageFactV1,
) (CompositeFamilyUsageAggregateV1, error) {
	result := CompositeFamilyUsageAggregateV1{
		AttemptSlotsUsed: uint32(len(attempts)),
		EstimatedCost: CompositeFamilyCostTotalV1{
			Status: CompositeFamilyCostUnknownV1,
		},
		ProviderReportedCost: CompositeFamilyCostTotalV1{
			Status: CompositeFamilyCostUnknownV1,
		},
		ReconciledCost: CompositeFamilyCostTotalV1{
			Status: CompositeFamilyCostUnknownV1,
		},
	}
	if len(attempts) == 0 {
		return result, nil
	}
	var err error
	result.TokenTotals.Input, err = sumCompositeTokenField(attempts, func(
		fact CompositeFamilyAttemptUsageFactV1,
	) *uint64 {
		return fact.Usage.Tokens.Input
	})
	if err != nil {
		return CompositeFamilyUsageAggregateV1{}, err
	}
	result.TokenTotals.CachedInput, err = sumCompositeTokenField(attempts, func(
		fact CompositeFamilyAttemptUsageFactV1,
	) *uint64 {
		return fact.Usage.Tokens.CachedInput
	})
	if err != nil {
		return CompositeFamilyUsageAggregateV1{}, err
	}
	result.TokenTotals.UncachedInput, err = sumCompositeTokenField(attempts, func(
		fact CompositeFamilyAttemptUsageFactV1,
	) *uint64 {
		return fact.Usage.Tokens.UncachedInput
	})
	if err != nil {
		return CompositeFamilyUsageAggregateV1{}, err
	}
	result.TokenTotals.Output, err = sumCompositeTokenField(attempts, func(
		fact CompositeFamilyAttemptUsageFactV1,
	) *uint64 {
		return fact.Usage.Tokens.Output
	})
	if err != nil {
		return CompositeFamilyUsageAggregateV1{}, err
	}
	result.TokenTotals.Reasoning, err = sumCompositeTokenField(attempts, func(
		fact CompositeFamilyAttemptUsageFactV1,
	) *uint64 {
		return fact.Usage.Tokens.Reasoning
	})
	if err != nil {
		return CompositeFamilyUsageAggregateV1{}, err
	}
	result.EstimatedCost, err = aggregateCompositeCost(attempts, func(
		fact CompositeFamilyAttemptUsageFactV1,
	) *string {
		return fact.Usage.EstimatedCost
	})
	if err != nil {
		return CompositeFamilyUsageAggregateV1{}, err
	}
	result.ProviderReportedCost, err = aggregateCompositeCost(attempts, func(
		fact CompositeFamilyAttemptUsageFactV1,
	) *string {
		return fact.Usage.ProviderReportedCost
	})
	if err != nil {
		return CompositeFamilyUsageAggregateV1{}, err
	}
	result.ReconciledCost, err = aggregateCompositeCost(attempts, func(
		fact CompositeFamilyAttemptUsageFactV1,
	) *string {
		return fact.Usage.ReconciledCost
	})
	if err != nil {
		return CompositeFamilyUsageAggregateV1{}, err
	}
	return result, nil
}

func sumCompositeTokenField(
	attempts []CompositeFamilyAttemptUsageFactV1,
	field func(CompositeFamilyAttemptUsageFactV1) *uint64,
) (*uint64, error) {
	var total uint64
	for _, attempt := range attempts {
		value := field(attempt)
		if value == nil {
			return nil, nil
		}
		if ^uint64(0)-total < *value {
			return nil, compositeUsageIntegrity("family token sum overflows uint64")
		}
		total += *value
	}
	return &total, nil
}

func aggregateCompositeCost(
	attempts []CompositeFamilyAttemptUsageFactV1,
	field func(CompositeFamilyAttemptUsageFactV1) *string,
) (CompositeFamilyCostTotalV1, error) {
	unknown := CompositeFamilyCostTotalV1{Status: CompositeFamilyCostUnknownV1}
	values := make([]string, len(attempts))
	currencySet := make(map[string]struct{}, len(attempts))
	missing := false
	for index, attempt := range attempts {
		value := field(attempt)
		if value == nil {
			missing = true
			continue
		}
		if _, _, err := parseCanonicalNonNegativeDecimal(*value); err != nil {
			return CompositeFamilyCostTotalV1{}, compositeUsageIntegrityError(
				"non-canonical cost fact",
				err,
			)
		}
		values[index] = *value
		currencySet[attempt.Currency] = struct{}{}
	}
	if missing {
		return unknown, nil
	}
	if len(currencySet) != 1 {
		currencies := make([]string, 0, len(currencySet))
		for currency := range currencySet {
			currencies = append(currencies, currency)
		}
		sort.Strings(currencies)
		return CompositeFamilyCostTotalV1{
			Status:     CompositeFamilyCostMixedCurrencyV1,
			Currencies: currencies,
		}, nil
	}
	var currency string
	for value := range currencySet {
		currency = value
	}
	sum, err := sumCanonicalNonNegativeDecimals(values)
	if err != nil {
		return CompositeFamilyCostTotalV1{}, compositeUsageIntegrityError(
			"cost aggregation",
			err,
		)
	}
	return CompositeFamilyCostTotalV1{
		Status:   CompositeFamilyCostKnownV1,
		Value:    &sum,
		Currency: currency,
	}, nil
}

func sumCanonicalNonNegativeDecimals(values []string) (string, error) {
	maxScale := 0
	coefficients := make([]*big.Int, len(values))
	scales := make([]int, len(values))
	for index, value := range values {
		coefficient, scale, err := parseCanonicalNonNegativeDecimal(value)
		if err != nil {
			return "", err
		}
		coefficients[index] = coefficient
		scales[index] = scale
		if scale > maxScale {
			maxScale = scale
		}
	}
	total := new(big.Int)
	ten := big.NewInt(10)
	for index, coefficient := range coefficients {
		scaled := new(big.Int).Set(coefficient)
		if difference := maxScale - scales[index]; difference > 0 {
			scaled.Mul(scaled, new(big.Int).Exp(ten, big.NewInt(int64(difference)), nil))
		}
		total.Add(total, scaled)
	}
	return formatCanonicalNonNegativeDecimal(total, maxScale), nil
}

func parseCanonicalNonNegativeDecimal(value string) (*big.Int, int, error) {
	if value == "0" {
		return new(big.Int), 0, nil
	}
	if value == "" || len(value) > moduleapi.MaxIdentifierBytes ||
		strings.HasPrefix(value, "+") ||
		strings.HasPrefix(value, "-") || strings.HasSuffix(value, ".") {
		return nil, 0, fmt.Errorf("invalid non-negative decimal %q", value)
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" ||
		(len(parts[0]) > 1 && parts[0][0] == '0') {
		return nil, 0, fmt.Errorf("invalid non-negative decimal %q", value)
	}
	for _, character := range parts[0] {
		if character < '0' || character > '9' {
			return nil, 0, fmt.Errorf("invalid non-negative decimal %q", value)
		}
	}
	scale := 0
	digits := parts[0]
	if len(parts) == 2 {
		fraction := parts[1]
		if fraction == "" || fraction[len(fraction)-1] == '0' {
			return nil, 0, fmt.Errorf("invalid non-negative decimal %q", value)
		}
		for _, character := range fraction {
			if character < '0' || character > '9' {
				return nil, 0, fmt.Errorf("invalid non-negative decimal %q", value)
			}
		}
		scale = len(fraction)
		digits += fraction
	}
	coefficient, ok := new(big.Int).SetString(digits, 10)
	if !ok || coefficient.Sign() < 0 || coefficient.Sign() == 0 {
		return nil, 0, fmt.Errorf("invalid non-negative decimal %q", value)
	}
	return coefficient, scale, nil
}

func formatCanonicalNonNegativeDecimal(coefficient *big.Int, scale int) string {
	if coefficient.Sign() == 0 {
		return "0"
	}
	digits := coefficient.String()
	if scale == 0 {
		return digits
	}
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	integer := digits[:len(digits)-scale]
	fraction := strings.TrimRight(digits[len(digits)-scale:], "0")
	if fraction == "" {
		return integer
	}
	return integer + "." + fraction
}

func compositeUsageIntegrity(subject string) error {
	return fmt.Errorf("%w: %s", ErrCompositeUsageProjectionIntegrity, subject)
}

func compositeUsageIntegrityError(subject string, cause error) error {
	if cause == nil {
		return compositeUsageIntegrity(subject)
	}
	return fmt.Errorf(
		"%w: %s: %v",
		ErrCompositeUsageProjectionIntegrity,
		subject,
		cause,
	)
}
