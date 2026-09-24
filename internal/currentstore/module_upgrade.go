package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidModuleUpgradeReview      = errors.New("currentstore: invalid module upgrade review")
	ErrModuleUpgradeReviewNotFound     = errors.New("currentstore: module upgrade review not found")
	ErrModuleUpgradeReviewConflict     = errors.New("currentstore: module upgrade review conflict")
	ErrModuleUpgradeReviewStale        = errors.New("currentstore: module upgrade review basis is stale")
	ErrModuleUpgradeSuppressed         = errors.New("currentstore: module upgrade candidate is rejected for tenant")
	ErrModuleUpgradeIntegrity          = errors.New("currentstore: module upgrade integrity violation")
	ErrModuleCandidateDecisionConflict = errors.New("currentstore: module candidate already has a different decision")
)

// ReadModuleUpgradeReviewBasisInput selects one exact current binding and one
// exact target observation. PortBindingIndex is the ordinal among bindings of
// the selected exact Port; a channel endpoint always uses ordinal zero.
type ReadModuleUpgradeReviewBasisInput struct {
	SourceID   string
	SnapshotID string
	TenantID   string
	// ArtifactAdmissionID is required by the server-owned W6.6 path. It is
	// optional only for historical caller-owned U3 fixtures.
	ArtifactAdmissionID  string
	BindingTarget        moduleupgrade.BindingTargetV1
	Port                 moduleapi.PortRef
	PortBindingIndex     uint32
	TargetModule         moduleapi.Ref
	TargetArtifactDigest string
	TargetInstanceID     string
}

// ModuleUpgradeReviewBasis is detached pre-I/O evidence. It grants nothing;
// CommitModuleUpgradeReview rebuilds it under BEGIN IMMEDIATE after target
// artifact I/O and rejects any changed current fact.
type ModuleUpgradeReviewBasis struct {
	Selection           ReadModuleUpgradeReviewBasisInput
	Source              ModuleSource
	PublisherKey        *ModulePublisherKey
	Snapshot            ModuleDiscoverySnapshot
	TargetEntry         moduleapi.ModuleDiscoveryEntryV1
	Candidate           moduleapi.ModuleUpgradeCandidateV1
	CandidateCanonical  []byte
	CandidateID         string
	PublishedBasis      controlcontract.PublishedBasis
	Control             controlcontract.ControlSnapshot
	Catalog             controlcontract.CatalogGeneration
	SelectedBinding     controlcontract.BindingSpec
	BindingImpacts      []moduleupgrade.BindingImpactV1
	CurrentActivation   ModuleActivation
	CurrentInstallation ModuleInstallation
}

type ModuleUpgradeCandidateRecord struct {
	CandidateID string
	Candidate   moduleapi.ModuleUpgradeCandidateV1
	Canonical   []byte
	ReviewKey   string
	SourceID    string
	SnapshotID  string
	AdmittedAt  time.Time
}

type ModuleUpgradeReviewRecord struct {
	ReviewID  string
	Review    moduleupgrade.ReviewV1
	Canonical []byte
	Candidate ModuleUpgradeCandidateRecord
	CreatedAt time.Time
}

type CommitModuleUpgradeReviewInput struct {
	Basis                   ModuleUpgradeReviewBasis
	ReviewCanonical         []byte
	TargetManifestCanonical []byte
}

type ModuleCandidateDecisionRecord struct {
	DecisionID string
	Decision   moduleapi.ModuleCandidateDecisionV1
	Canonical  []byte
	TenantID   string
	ReviewID   string
	ReviewKey  string
	DecidedAt  time.Time
}

type DecideModuleCandidateInput struct {
	TenantID                string
	ReviewID                string
	DecisionCanonical       []byte
	ConfirmTenantWideReject bool
}

// ReadModuleUpgradeReviewBasis captures a coherent pre-I/O basis. It never
// reads a package path, downloads a package, or writes Candidate/Review state.
func (store *Store) ReadModuleUpgradeReviewBasis(ctx context.Context, input ReadModuleUpgradeReviewBasisInput) (ModuleUpgradeReviewBasis, error) {
	if ctx == nil {
		return ModuleUpgradeReviewBasis{}, fmt.Errorf("%w: context is nil", ErrInvalidModuleUpgradeReview)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	basis, err := rebuildModuleUpgradeReviewBasis(ctx, connection, input)
	if err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	committed = true
	return detachModuleUpgradeReviewBasis(basis), nil
}

// CommitModuleUpgradeReview is the post-I/O linearization point. Exact retry
// is checked before current basis so a lost response remains retrievable.
func (store *Store) CommitModuleUpgradeReview(ctx context.Context, input CommitModuleUpgradeReviewInput) (result ModuleUpgradeReviewRecord, returnErr error) {
	if ctx == nil || len(input.ReviewCanonical) == 0 || len(input.TargetManifestCanonical) == 0 || input.Basis.CandidateID == "" {
		return result, fmt.Errorf("%w: incomplete review commit", ErrInvalidModuleUpgradeReview)
	}
	targetManifestCanonical := bytes.Clone(input.TargetManifestCanonical)
	review, err := restoreReviewAgainstBasis(input.ReviewCanonical, input.Basis)
	if err != nil {
		return result, err
	}
	reviewID := moduleapi.Digest(moduleupgrade.ReviewIDDigestDomainV1, input.ReviewCanonical)
	if err := requireReviewMatchesBasis(review, input.Basis); err != nil {
		return result, err
	}
	if err := requireTargetManifestMatchesReview(review, targetManifestCanonical); err != nil {
		return result, err
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, err
	}
	defer connection.Close()
	if _, err = connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	if existing, found, queryErr := queryModuleUpgradeReview(ctx, connection, reviewID); queryErr != nil {
		return result, queryErr
	} else if found {
		if !bytes.Equal(existing.Canonical, input.ReviewCanonical) {
			return result, fmt.Errorf("%w: ReviewID collision", ErrModuleUpgradeIntegrity)
		}
		if err := VerifyModuleUpgradeSemanticClosureV1(ctx, connection); err != nil {
			return result, fmt.Errorf("%w: exact Review retry closure: %v", ErrModuleUpgradeIntegrity, err)
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceModuleReviewV1, reviewID,
		); err != nil {
			return result, err
		}
		if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, err
		}
		committed = true
		return detachModuleUpgradeReviewRecord(existing), nil
	}

	live, err := rebuildModuleUpgradeReviewBasis(ctx, connection, input.Basis.Selection)
	if err != nil {
		return result, mapUpgradeBasisCommitError(err)
	}
	if !equalModuleUpgradeBasis(input.Basis, live) {
		return result, ErrModuleUpgradeReviewStale
	}
	if err := requireReviewMatchesBasis(review, live); err != nil {
		return result, err
	}
	if err := requireTargetManifestMatchesReview(review, targetManifestCanonical); err != nil {
		return result, err
	}
	now := nowUnixMicro()
	if err := insertModuleUpgradeCandidate(ctx, connection, live, now); err != nil {
		return result, err
	}
	if err := ensureModuleUpgradeTargetManifestContent(ctx, connection, review.Target.ManifestRef, targetManifestCanonical, now); err != nil {
		return result, err
	}
	target := review.BindingTarget
	var profile, workspace, endpoint any
	if target.Kind == moduleupgrade.BindingTargetProfileV1 {
		profile = target.ProfileID
	} else {
		workspace, endpoint = target.WorkspaceID, target.EndpointID
	}
	_, err = connection.ExecContext(ctx, `
		INSERT INTO module_upgrade_reviews(
			review_id,review_canonical,review_size_bytes,tenant_id,candidate_id,
			review_key,binding_target_kind,profile_id,workspace_id,endpoint_id,
			current_instance_id,target_instance_id,target_manifest_ref,current_installation_id,
			current_activation_id,current_activation_revision,pointer_revision,
			control_snapshot_id,control_revision,control_digest,
			catalog_generation_id,catalog_generation,catalog_digest,conclusion,created_at
			,artifact_admission_id,operator_principal_id,review_request_digest
		) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, reviewID, input.ReviewCanonical, len(input.ReviewCanonical), review.TenantID,
		review.CandidateID, review.ReviewKey, string(target.Kind), profile, workspace,
		endpoint, review.Current.Activation.InstanceID, review.TargetInstanceID, review.Target.ManifestRef,
		review.Current.InstallationID, live.CurrentActivation.ActivationID,
		int64(review.Current.Activation.ActivationRevision), int64(review.PublishedBasis.PointerRevision),
		review.PublishedBasis.Control.SnapshotID, int64(review.PublishedBasis.Control.Revision),
		review.PublishedBasis.Control.Digest, review.PublishedBasis.Catalog.GenerationID,
		int64(review.PublishedBasis.Catalog.Generation), review.PublishedBasis.Catalog.Digest,
		string(review.Conclusion), now, nullableModuleUpgradeValue(review.ArtifactAdmissionID),
		nullableModuleUpgradeValue(review.OperatorPrincipalID), nullableModuleUpgradeValue(review.ReviewRequestDigest))
	if err != nil {
		return result, fmt.Errorf("currentstore: insert module upgrade Review: %w", err)
	}
	if err := VerifyModuleUpgradeSemanticClosureV1(ctx, connection); err != nil {
		return result, fmt.Errorf("%w: pre-commit closure: %v", ErrModuleUpgradeIntegrity, err)
	}
	if err := appendModuleReviewResourceObservationV1(ctx, connection, reviewID); err != nil {
		return result, err
	}
	result, _, err = queryModuleUpgradeReview(ctx, connection, reviewID)
	if err != nil {
		return result, err
	}
	if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, err
	}
	committed = true
	return detachModuleUpgradeReviewRecord(result), nil
}

func (store *Store) DecideModuleCandidate(ctx context.Context, input DecideModuleCandidateInput) (result ModuleCandidateDecisionRecord, returnErr error) {
	if ctx == nil || !moduleapi.ValidSHA256(input.ReviewID) || len(input.DecisionCanonical) == 0 {
		return result, fmt.Errorf("%w: invalid decision input", ErrInvalidModuleUpgradeReview)
	}
	decision, _, decisionID, err := parseCandidateDecision(input.DecisionCanonical)
	if err != nil {
		return result, err
	}
	if err := validatePublishedBasisTenantID(input.TenantID); err != nil {
		return result, fmt.Errorf("%w: %v", ErrInvalidModuleUpgradeReview, err)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return result, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return result, err
	}
	defer connection.Close()
	if _, err = connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := connection.ExecContext(context.Background(), `ROLLBACK`)
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	if existing, found, queryErr := queryModuleCandidateDecisionByReview(ctx, connection, input.ReviewID); queryErr != nil {
		return result, queryErr
	} else if found {
		if existing.DecisionID != decisionID || !bytes.Equal(existing.Canonical, input.DecisionCanonical) || existing.TenantID != input.TenantID {
			return result, ErrModuleCandidateDecisionConflict
		}
		if err := VerifyModuleUpgradeSemanticClosureV1(ctx, connection); err != nil {
			return result, fmt.Errorf("%w: exact Decision retry closure: %v", ErrModuleUpgradeIntegrity, err)
		}
		if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
			return result, err
		}
		committed = true
		return detachModuleCandidateDecisionRecord(existing), nil
	}
	if (decision.Decision == moduleapi.ModuleCandidateDecisionRejectV1) != input.ConfirmTenantWideReject {
		return result, fmt.Errorf("%w: tenant-wide rejection confirmation must be true only for a new REJECT", ErrInvalidModuleUpgradeReview)
	}
	review, found, err := queryModuleUpgradeReview(ctx, connection, input.ReviewID)
	if err != nil {
		return result, err
	}
	if !found {
		return result, ErrModuleUpgradeReviewNotFound
	}
	if review.Review.TenantID != input.TenantID || decision.CandidateID != review.Review.CandidateID {
		return result, fmt.Errorf("%w: decision parent differs", ErrInvalidModuleUpgradeReview)
	}
	if decision.Decision == moduleapi.ModuleCandidateDecisionApproveV1 {
		var rejected int
		if err := connection.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM module_candidate_decisions
			WHERE tenant_id=? AND review_key=? AND decision='REJECT'
		`, input.TenantID, review.Review.ReviewKey).Scan(&rejected); err != nil {
			return result, err
		}
		if rejected != 0 {
			return result, ErrModuleUpgradeSuppressed
		}
		if review.Review.Conclusion != moduleupgrade.ConclusionWouldApplyV1 {
			return result, fmt.Errorf("%w: only WOULD_APPLY may be approved", ErrModuleUpgradeReviewConflict)
		}
		selection := selectionFromReview(review.Review)
		live, basisErr := rebuildModuleUpgradeReviewBasis(ctx, connection, selection)
		if basisErr != nil {
			return result, mapUpgradeBasisCommitError(basisErr)
		}
		if !reviewMatchesLiveBasis(review.Review, live) {
			return result, ErrModuleUpgradeReviewStale
		}
	}
	_, err = connection.ExecContext(ctx, `
		INSERT INTO module_candidate_decisions(
			decision_id,decision_canonical,decision_size_bytes,tenant_id,
			candidate_id,review_id,review_key,decision,decided_at
		) VALUES(?,?,?,?,?,?,?,?,?)
	`, decisionID, input.DecisionCanonical, len(input.DecisionCanonical), input.TenantID,
		decision.CandidateID, input.ReviewID, review.Review.ReviewKey,
		string(decision.Decision), nowUnixMicro())
	if err != nil {
		if decision.Decision == moduleapi.ModuleCandidateDecisionRejectV1 {
			var prior int
			if countErr := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM module_candidate_decisions WHERE tenant_id=? AND review_key=? AND decision='REJECT'`, input.TenantID, review.Review.ReviewKey).Scan(&prior); countErr == nil && prior > 0 {
				return result, ErrModuleUpgradeSuppressed
			}
		}
		return result, fmt.Errorf("currentstore: insert module candidate Decision: %w", err)
	}
	if err := VerifyModuleUpgradeSemanticClosureV1(ctx, connection); err != nil {
		return result, fmt.Errorf("%w: pre-commit decision closure: %v", ErrModuleUpgradeIntegrity, err)
	}
	result, _, err = queryModuleCandidateDecisionByReview(ctx, connection, input.ReviewID)
	if err != nil {
		return result, err
	}
	if _, err = connection.ExecContext(ctx, `COMMIT`); err != nil {
		return result, err
	}
	committed = true
	return detachModuleCandidateDecisionRecord(result), nil
}

func (store *Store) GetModuleUpgradeCandidate(ctx context.Context, candidateID string) (ModuleUpgradeCandidateRecord, error) {
	if ctx == nil || !moduleapi.ValidSHA256(candidateID) {
		return ModuleUpgradeCandidateRecord{}, ErrInvalidModuleUpgradeReview
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleUpgradeCandidateRecord{}, err
	}
	defer unlock()
	value, found, err := queryModuleUpgradeCandidate(ctx, store.db, candidateID)
	if err != nil {
		return ModuleUpgradeCandidateRecord{}, err
	}
	if !found {
		return ModuleUpgradeCandidateRecord{}, ErrModuleUpgradeReviewNotFound
	}
	return detachModuleUpgradeCandidateRecord(value), nil
}

func (store *Store) GetModuleUpgradeReview(ctx context.Context, reviewID string) (ModuleUpgradeReviewRecord, error) {
	if ctx == nil || !moduleapi.ValidSHA256(reviewID) {
		return ModuleUpgradeReviewRecord{}, ErrInvalidModuleUpgradeReview
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleUpgradeReviewRecord{}, err
	}
	defer unlock()
	value, found, err := queryModuleUpgradeReview(ctx, store.db, reviewID)
	if err != nil {
		return ModuleUpgradeReviewRecord{}, err
	}
	if !found {
		return ModuleUpgradeReviewRecord{}, ErrModuleUpgradeReviewNotFound
	}
	return detachModuleUpgradeReviewRecord(value), nil
}

// ListModuleUpgradeCandidates is an unbounded trusted-local/offline audit API.
// It must not be exposed by W6; an application service must add tenant-scoped
// pagination before any HTTP surface (global Candidates have no Tenant owner).
func (store *Store) ListModuleUpgradeCandidates(ctx context.Context) ([]ModuleUpgradeCandidateRecord, error) {
	if ctx == nil {
		return nil, ErrInvalidModuleUpgradeReview
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()
	rows, err := store.db.QueryContext(ctx, `SELECT candidate_id FROM module_upgrade_candidates ORDER BY candidate_id COLLATE BINARY`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]ModuleUpgradeCandidateRecord, 0, len(ids))
	for _, id := range ids {
		value, found, err := queryModuleUpgradeCandidate(ctx, store.db, id)
		if err != nil || !found {
			return nil, errors.Join(err, ErrModuleUpgradeIntegrity)
		}
		out = append(out, detachModuleUpgradeCandidateRecord(value))
	}
	return out, nil
}

func (store *Store) GetModuleCandidateDecision(ctx context.Context, reviewID string) (ModuleCandidateDecisionRecord, error) {
	if ctx == nil || !moduleapi.ValidSHA256(reviewID) {
		return ModuleCandidateDecisionRecord{}, ErrInvalidModuleUpgradeReview
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleCandidateDecisionRecord{}, err
	}
	defer unlock()
	value, found, err := queryModuleCandidateDecisionByReview(ctx, store.db, reviewID)
	if err != nil {
		return ModuleCandidateDecisionRecord{}, err
	}
	if !found {
		return ModuleCandidateDecisionRecord{}, ErrModuleUpgradeReviewNotFound
	}
	return detachModuleCandidateDecisionRecord(value), nil
}

// ListModuleUpgradeReviews is an unbounded trusted-local/offline audit API.
// U3 is disabled by default and assumes a trusted local Operator; W6 must use a
// tenant-scoped paginated application service and enforce its own growth limit.
func (store *Store) ListModuleUpgradeReviews(ctx context.Context, tenantID string) ([]ModuleUpgradeReviewRecord, error) {
	if ctx == nil {
		return nil, ErrInvalidModuleUpgradeReview
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()
	query := `SELECT review_id FROM module_upgrade_reviews`
	var args []any
	if tenantID != "" {
		if err := validatePublishedBasisTenantID(tenantID); err != nil {
			return nil, err
		}
		query += ` WHERE tenant_id=?`
		args = append(args, tenantID)
	}
	query += ` ORDER BY tenant_id COLLATE BINARY,candidate_id COLLATE BINARY,review_id COLLATE BINARY`
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]ModuleUpgradeReviewRecord, 0, len(ids))
	for _, id := range ids {
		value, found, err := queryModuleUpgradeReview(ctx, store.db, id)
		if err != nil || !found {
			return nil, errors.Join(err, ErrModuleUpgradeIntegrity)
		}
		out = append(out, detachModuleUpgradeReviewRecord(value))
	}
	return out, nil
}

// ListModuleUpgradeReviewsBounded is the online-read variant. Unlike the
// trusted-local audit API above, it always applies a caller-independent hard
// LIMIT before materializing Review records for a control projection.
func (store *Store) ListModuleUpgradeReviewsBounded(
	ctx context.Context,
	tenantID string,
	limit int,
) ([]ModuleUpgradeReviewRecord, error) {
	if ctx == nil || limit < 1 || limit > 1025 {
		return nil, ErrInvalidModuleUpgradeReview
	}
	if err := validatePublishedBasisTenantID(tenantID); err != nil {
		return nil, err
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()
	rows, err := store.db.QueryContext(ctx, `
		SELECT review_id FROM module_upgrade_reviews
		WHERE tenant_id=?
		ORDER BY tenant_id COLLATE BINARY,candidate_id COLLATE BINARY,review_id COLLATE BINARY
		LIMIT ?
	`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]ModuleUpgradeReviewRecord, 0, len(ids))
	for _, id := range ids {
		value, found, err := queryModuleUpgradeReview(ctx, store.db, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, ErrModuleUpgradeIntegrity
		}
		result = append(result, detachModuleUpgradeReviewRecord(value))
	}
	return result, nil
}

// ListModuleCandidateDecisions is an unbounded trusted-local/offline audit API.
// W6 must not expose it directly and must supply tenant-scoped pagination.
func (store *Store) ListModuleCandidateDecisions(ctx context.Context, tenantID string) ([]ModuleCandidateDecisionRecord, error) {
	if ctx == nil {
		return nil, ErrInvalidModuleUpgradeReview
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return nil, err
	}
	defer unlock()
	query := `SELECT review_id FROM module_candidate_decisions`
	var args []any
	if tenantID != "" {
		if err := validatePublishedBasisTenantID(tenantID); err != nil {
			return nil, err
		}
		query += ` WHERE tenant_id=?`
		args = append(args, tenantID)
	}
	query += ` ORDER BY tenant_id COLLATE BINARY,review_key COLLATE BINARY,review_id COLLATE BINARY`
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]ModuleCandidateDecisionRecord, 0, len(ids))
	for _, id := range ids {
		value, found, err := queryModuleCandidateDecisionByReview(ctx, store.db, id)
		if err != nil || !found {
			return nil, errors.Join(err, ErrModuleUpgradeIntegrity)
		}
		out = append(out, detachModuleCandidateDecisionRecord(value))
	}
	return out, nil
}

func rebuildModuleUpgradeReviewBasis(ctx context.Context, q moduleDiscoveryQueryer, input ReadModuleUpgradeReviewBasisInput) (ModuleUpgradeReviewBasis, error) {
	if err := validateUpgradeSelection(input); err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	source, found, err := queryModuleSource(ctx, q, input.SourceID)
	if err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	if !found {
		return ModuleUpgradeReviewBasis{}, ErrModuleSourceNotFound
	}
	if input.ArtifactAdmissionID != "" {
		admission, admissionFound, admissionErr := queryModuleArtifactAdmissionV1(ctx, q, input.ArtifactAdmissionID)
		if admissionErr != nil {
			return ModuleUpgradeReviewBasis{}, admissionErr
		}
		if !admissionFound || admission.Record.SourceID != input.SourceID ||
			admission.Record.SnapshotID != input.SnapshotID ||
			admission.Record.Module != input.TargetModule ||
			admission.Record.ArtifactDigest != input.TargetArtifactDigest {
			return ModuleUpgradeReviewBasis{}, fmt.Errorf("%w: Artifact Admission differs from exact upgrade selection", ErrModuleUpgradeReviewConflict)
		}
	}
	if source.CurrentSnapshotID != input.SnapshotID {
		return ModuleUpgradeReviewBasis{}, ErrModuleUpgradeReviewStale
	}
	snapshot, found, err := queryModuleDiscoverySnapshot(ctx, q, input.SnapshotID)
	if err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	if !found {
		return ModuleUpgradeReviewBasis{}, ErrModuleDiscoverySnapshotNotFound
	}
	if snapshot.SourceID != source.SourceID || snapshot.SourcePolicyID != source.PolicyID || snapshot.SourcePolicyRevision != source.PolicyRevision || snapshot.ObservationRevision != source.ObservationRevision {
		return ModuleUpgradeReviewBasis{}, ErrModuleUpgradeReviewStale
	}
	var publisher *ModulePublisherKey
	if source.Policy.SignatureRequired {
		key, found, err := queryModulePublisherKey(ctx, q, source.Policy.PublisherKeyID)
		if err != nil {
			return ModuleUpgradeReviewBasis{}, err
		}
		if !found {
			return ModuleUpgradeReviewBasis{}, ErrModulePublisherKeyNotFound
		}
		if key.RevokedAt != nil {
			return ModuleUpgradeReviewBasis{}, ErrModulePublisherKeyRevoked
		}
		if snapshot.PublisherKeyID != key.PublisherKeyID || snapshot.PublisherKeyRevision != key.Revision {
			return ModuleUpgradeReviewBasis{}, ErrModuleUpgradeReviewStale
		}
		detached := detachModulePublisherKey(key)
		publisher = &detached
	}
	target, found := findUpgradeTargetEntry(snapshot.Snapshot.Entries, input.TargetModule, input.TargetArtifactDigest)
	if !found {
		return ModuleUpgradeReviewBasis{}, fmt.Errorf("%w: exact target absent from Snapshot", ErrInvalidModuleUpgradeReview)
	}
	current, found, err := queryCurrentControlPointer(ctx, q, input.TenantID)
	if err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	if !found {
		return ModuleUpgradeReviewBasis{}, ErrPublishedBasisNotFound
	}
	control, catalog, err := loadAdmissionControlCatalog(ctx, q, input.TenantID, current.SnapshotID, current.CatalogGenerationID)
	if err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	selected, err := selectUpgradeBinding(control, input.BindingTarget, input.Port, input.PortBindingIndex)
	if err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	entry, found := catalog.FindInstance(selected.InstanceID)
	if !found || entry.Activation.InstanceID != selected.InstanceID || !currentCatalogProvidesPort(entry.Provides, input.Port) {
		return ModuleUpgradeReviewBasis{}, fmt.Errorf("%w: selected binding is absent from current Catalog", ErrModuleUpgradeReviewConflict)
	}
	activation, err := queryModuleActivationByIdentity(ctx, q, input.TenantID, entry.Activation.InstanceID, entry.Activation.ActivationRevision)
	if err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	if activation.ActivationID == "" || activation.ExecutionClass != entry.Activation.ExecutionClass || activation.AdapterIdentity != entry.Activation.AdapterIdentity {
		return ModuleUpgradeReviewBasis{}, ErrModuleUpgradeIntegrity
	}
	installation, err := queryModuleInstallationByID(ctx, q, activation.InstallationID)
	if err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	if installation.ModuleID != entry.Activation.ModuleID || installation.ExactVersion != entry.Activation.Version || installation.ArtifactDigest != entry.Activation.ArtifactDigest || installation.ModuleID != target.Module.ID {
		return ModuleUpgradeReviewBasis{}, fmt.Errorf("%w: current Catalog/Installation/target module closure differs", ErrModuleUpgradeReviewConflict)
	}
	candidate, canonical, candidateID, err := moduleapi.NewModuleUpgradeCandidateV1(moduleapi.ModuleUpgradeCandidateV1{SchemaVersion: moduleapi.ModuleUpgradeCandidateSchemaVersionV1, Change: moduleapi.ModuleCandidateChangeExactVersionV1, Current: &moduleapi.ExactModuleArtifactV1{Module: moduleapi.Ref{ID: installation.ModuleID, Version: installation.ExactVersion}, ArtifactDigest: installation.ArtifactDigest}, Target: target}, snapshot.SnapshotCanonical, snapshot.SnapshotID)
	if err != nil {
		return ModuleUpgradeReviewBasis{}, fmt.Errorf("%w: rebuild Candidate: %v", ErrInvalidModuleUpgradeReview, err)
	}
	var suppressed int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM module_candidate_decisions WHERE tenant_id=? AND review_key=? AND decision='REJECT'`, input.TenantID, candidate.ReviewKey).Scan(&suppressed); err != nil {
		return ModuleUpgradeReviewBasis{}, err
	}
	if suppressed != 0 {
		return ModuleUpgradeReviewBasis{}, ErrModuleUpgradeSuppressed
	}
	published := controlcontract.PublishedBasis{TenantID: input.TenantID, PointerRevision: current.PointerRevision, Control: controlcontract.ControlSnapshotRef{SnapshotID: control.SnapshotID, Revision: control.Revision, Digest: control.Digest}, Catalog: controlcontract.CatalogGenerationRef{GenerationID: catalog.GenerationID, Generation: catalog.Generation, Digest: catalog.Digest}}
	impacts := allUpgradeBindingImpacts(control, selected.InstanceID, input.TargetInstanceID)
	return ModuleUpgradeReviewBasis{Selection: input, Source: source, PublisherKey: publisher, Snapshot: snapshot, TargetEntry: target, Candidate: candidate, CandidateCanonical: canonical, CandidateID: candidateID, PublishedBasis: published, Control: control, Catalog: catalog, SelectedBinding: selected, BindingImpacts: impacts, CurrentActivation: activation, CurrentInstallation: installation}, nil
}

func validateUpgradeSelection(input ReadModuleUpgradeReviewBasisInput) error {
	if input.SourceID == "" || !moduleapi.ValidSHA256(input.SnapshotID) || !moduleapi.ValidSHA256(input.TargetArtifactDigest) {
		return fmt.Errorf("%w: invalid source, Snapshot, or target digest", ErrInvalidModuleUpgradeReview)
	}
	if input.ArtifactAdmissionID != "" && !moduleapi.ValidSHA256(input.ArtifactAdmissionID) {
		return fmt.Errorf("%w: invalid Artifact Admission ID", ErrInvalidModuleUpgradeReview)
	}
	if err := validatePublishedBasisTenantID(input.TenantID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidModuleUpgradeReview, err)
	}
	if err := input.Port.Validate(); err != nil {
		return fmt.Errorf("%w: invalid Port: %v", ErrInvalidModuleUpgradeReview, err)
	}
	if err := input.TargetModule.Validate(); err != nil {
		return fmt.Errorf("%w: target ModuleRef: %v", ErrInvalidModuleUpgradeReview, err)
	}
	if err := validateOpaqueModuleValue("target instance ID", input.TargetInstanceID, 256); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidModuleUpgradeReview, err)
	}
	return nil
}

func selectUpgradeBinding(control controlcontract.ControlSnapshot, target moduleupgrade.BindingTargetV1, port moduleapi.PortRef, ordinal uint32) (controlcontract.BindingSpec, error) {
	switch target.Kind {
	case moduleupgrade.BindingTargetProfileV1:
		if target.ProfileID == "" || target.WorkspaceID != "" || target.EndpointID != "" {
			return controlcontract.BindingSpec{}, ErrInvalidModuleUpgradeReview
		}
		profile, found := control.FindProfile(target.ProfileID)
		if !found {
			return controlcontract.BindingSpec{}, fmt.Errorf("%w: Profile absent", ErrModuleUpgradeReviewConflict)
		}
		var index uint32
		for _, binding := range profile.Bindings {
			if binding.Port != port {
				continue
			}
			if index == ordinal {
				return binding, nil
			}
			index++
		}
	case moduleupgrade.BindingTargetWorkspaceChannelEndpointV1:
		if target.ProfileID != "" || target.WorkspaceID == "" || target.EndpointID == "" || ordinal != 0 {
			return controlcontract.BindingSpec{}, ErrInvalidModuleUpgradeReview
		}
		workspace, found := control.FindWorkspace(target.WorkspaceID)
		if !found {
			break
		}
		endpoint, found := workspace.FindChannelEndpoint(target.EndpointID)
		if found && endpoint.Binding.Port == port {
			return endpoint.Binding, nil
		}
	default:
		return controlcontract.BindingSpec{}, ErrInvalidModuleUpgradeReview
	}
	return controlcontract.BindingSpec{}, fmt.Errorf("%w: exact selected Binding absent", ErrModuleUpgradeReviewConflict)
}

func allUpgradeBindingImpacts(control controlcontract.ControlSnapshot, currentInstance, targetInstance string) []moduleupgrade.BindingImpactV1 {
	var impacts []moduleupgrade.BindingImpactV1
	for _, profile := range control.Profiles {
		ordinals := map[moduleapi.PortRef]uint32{}
		for _, binding := range profile.Bindings {
			ordinal := ordinals[binding.Port]
			ordinals[binding.Port]++
			if binding.InstanceID != currentInstance {
				continue
			}
			impacts = append(impacts, upgradeImpact(moduleupgrade.BindingTargetV1{Kind: moduleupgrade.BindingTargetProfileV1, ProfileID: profile.Profile.ID}, binding, ordinal, currentInstance, targetInstance))
		}
	}
	for _, workspace := range control.Workspaces {
		for _, endpoint := range workspace.ChannelEndpoints {
			if endpoint.Binding.InstanceID == currentInstance {
				impacts = append(impacts, upgradeImpact(moduleupgrade.BindingTargetV1{Kind: moduleupgrade.BindingTargetWorkspaceChannelEndpointV1, WorkspaceID: workspace.Workspace.ID, EndpointID: endpoint.EndpointID}, endpoint.Binding, 0, currentInstance, targetInstance))
			}
		}
	}
	sort.Slice(impacts, func(i, j int) bool { return upgradeImpactSortKey(impacts[i]) < upgradeImpactSortKey(impacts[j]) })
	if impacts == nil {
		impacts = []moduleupgrade.BindingImpactV1{}
	}
	return impacts
}

func upgradeImpact(target moduleupgrade.BindingTargetV1, binding controlcontract.BindingSpec, ordinal uint32, currentInstance, targetInstance string) moduleupgrade.BindingImpactV1 {
	return moduleupgrade.BindingImpactV1{BindingTarget: target, Port: binding.Port, PortBindingIndex: ordinal, CurrentInstanceID: currentInstance, TargetInstanceID: targetInstance, ConfigRef: binding.ConfigRef, AuthorityCeilingRef: binding.AuthorityCeilingRef, StaticContextRefs: append([]string(nil), binding.StaticContextRefs...), FailurePolicy: binding.FailurePolicy}
}
func upgradeImpactSortKey(v moduleupgrade.BindingImpactV1) string {
	return string(v.BindingTarget.Kind) + "\x00" + v.BindingTarget.ProfileID + "\x00" + v.BindingTarget.WorkspaceID + "\x00" + v.BindingTarget.EndpointID + "\x00" + v.Port.Name + "\x00" + v.Port.ExactVersion + fmt.Sprintf("\x00%010d", v.PortBindingIndex)
}

func findUpgradeTargetEntry(entries []moduleapi.ModuleDiscoveryEntryV1, ref moduleapi.Ref, digest string) (moduleapi.ModuleDiscoveryEntryV1, bool) {
	for _, entry := range entries {
		if entry.Module == ref && entry.ArtifactDigest == digest {
			return entry, true
		}
	}
	return moduleapi.ModuleDiscoveryEntryV1{}, false
}

func restoreReviewAgainstBasis(canonical []byte, basis ModuleUpgradeReviewBasis) (moduleupgrade.ReviewV1, error) {
	id := moduleapi.Digest(moduleupgrade.ReviewIDDigestDomainV1, canonical)
	review, err := moduleupgrade.RestoreReviewV1(bytes.Clone(canonical), id, bytes.Clone(basis.CandidateCanonical), bytes.Clone(basis.Snapshot.SnapshotCanonical))
	if err != nil {
		return moduleupgrade.ReviewV1{}, fmt.Errorf("%w: restore Review: %v", ErrInvalidModuleUpgradeReview, err)
	}
	return review, nil
}

func requireReviewMatchesBasis(review moduleupgrade.ReviewV1, basis ModuleUpgradeReviewBasis) error {
	if review.CandidateID != basis.CandidateID || review.ReviewKey != basis.Candidate.ReviewKey || review.TenantID != basis.Selection.TenantID || review.ArtifactAdmissionID != basis.Selection.ArtifactAdmissionID || review.BindingTarget != basis.Selection.BindingTarget || review.Port != basis.Selection.Port || review.PortBindingIndex != basis.Selection.PortBindingIndex || review.TargetInstanceID != basis.Selection.TargetInstanceID || review.PublishedBasis != basis.PublishedBasis || review.Target.Module != basis.TargetEntry.Module || review.Target.ArtifactDigest != basis.TargetEntry.ArtifactDigest || review.Target.ArtifactSizeBytes != basis.TargetEntry.ArtifactSizeBytes || review.Target.SignatureID != basis.TargetEntry.SignatureID || !reflect.DeepEqual(review.BindingImpacts, basis.BindingImpacts) {
		return fmt.Errorf("%w: Review differs from Store-owned basis", ErrInvalidModuleUpgradeReview)
	}
	wantSupply := supplyBasisFromStore(basis)
	if review.SupplyBasis != wantSupply {
		return fmt.Errorf("%w: supply basis differs", ErrInvalidModuleUpgradeReview)
	}
	wantActivation := activatedRefFromStore(basis.CurrentActivation, basis.CurrentInstallation)
	if review.Current.Activation != wantActivation || review.Current.InstallationID != basis.CurrentInstallation.InstallationID || review.Current.ManifestRef != basis.CurrentInstallation.ManifestRef {
		return fmt.Errorf("%w: current closure differs", ErrInvalidModuleUpgradeReview)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(basis.CurrentInstallation.ManifestBytes)
	if err != nil || !reflect.DeepEqual(review.Current.Manifest, manifestSummary(manifest)) {
		return fmt.Errorf("%w: current Manifest summary differs", ErrInvalidModuleUpgradeReview)
	}
	_, targetAlreadyExists := basis.Catalog.FindInstance(review.TargetInstanceID)
	hasTargetConflictReason := reviewHasReason(review, moduleupgrade.ReasonTargetInstanceConflictV1)
	if targetAlreadyExists {
		if review.Conclusion != moduleupgrade.ConclusionConflictV1 || !hasTargetConflictReason {
			return fmt.Errorf("%w: existing target Instance requires exact CONFLICT projection", ErrInvalidModuleUpgradeReview)
		}
	} else if hasTargetConflictReason {
		return fmt.Errorf("%w: target Instance conflict reason has no current Catalog fact", ErrInvalidModuleUpgradeReview)
	}
	return nil
}

func requireTargetManifestMatchesReview(review moduleupgrade.ReviewV1, canonical []byte) error {
	manifest, owned, err := moduleapi.ParseModuleManifestV1(bytes.Clone(canonical))
	if err != nil || !bytes.Equal(owned, canonical) {
		return fmt.Errorf("%w: target Manifest is not exact canonical: %v", ErrInvalidModuleUpgradeReview, err)
	}
	digest, err := ComputeContentDigest(ContentModuleManifest, moduleManifestMediaType, owned)
	if err != nil || digest != review.Target.ManifestRef ||
		manifest.ID != review.Target.Module.ID || manifest.Version != review.Target.Module.Version ||
		!reflect.DeepEqual(manifestSummary(manifest), review.Target.Manifest) {
		return fmt.Errorf("%w: target Manifest evidence differs from Review", ErrInvalidModuleUpgradeReview)
	}
	return nil
}

func ensureModuleUpgradeTargetManifestContent(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, digest string, canonical []byte, createdAt int64) error {
	result, err := q.ExecContext(ctx, `
		INSERT INTO content_records(content_digest,kind,media_type,canonical_bytes,size_bytes,created_at)
		VALUES(?,?,?,?,?,?) ON CONFLICT(content_digest) DO NOTHING
	`, digest, string(ContentModuleManifest), moduleManifestMediaType, canonical, len(canonical), createdAt)
	if err != nil {
		return fmt.Errorf("currentstore: insert module upgrade target Manifest evidence: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 0 && affected != 1 {
		return fmt.Errorf("%w: target Manifest insert affected %d rows", ErrModuleUpgradeIntegrity, affected)
	}
	record, err := queryContent(ctx, q, digest)
	if err != nil || record.Kind != ContentModuleManifest || record.MediaType != moduleManifestMediaType || !bytes.Equal(record.CanonicalBytes, canonical) {
		return fmt.Errorf("%w: target Manifest ContentRecord differs: %v", ErrModuleUpgradeIntegrity, err)
	}
	return nil
}

func reviewHasReason(review moduleupgrade.ReviewV1, want moduleupgrade.ReasonCodeV1) bool {
	for _, reason := range review.ReasonCodes {
		if reason == want {
			return true
		}
	}
	return false
}

func supplyBasisFromStore(b ModuleUpgradeReviewBasis) moduleupgrade.SupplyBasisV1 {
	value := moduleupgrade.SupplyBasisV1{SourceID: b.Source.SourceID, SourcePolicyID: b.Source.PolicyID, SourcePolicyRevision: b.Source.PolicyRevision, SnapshotID: b.Snapshot.SnapshotID, IndexID: b.Snapshot.IndexID, ObservationRevision: b.Snapshot.ObservationRevision, SignatureStatus: moduleupgrade.SignatureNotRequiredV1}
	if b.PublisherKey != nil {
		value.PublisherKeyID = b.PublisherKey.PublisherKeyID
		value.PublisherKeyRevision = b.PublisherKey.Revision
		value.SignatureStatus = moduleupgrade.SignatureVerifiedV1
	}
	return value
}
func manifestSummary(m moduleapi.ModuleManifestV1) moduleupgrade.ManifestSummaryV1 {
	return moduleupgrade.ManifestSummaryV1{Module: moduleapi.Ref{ID: m.ID, Version: m.Version}, Runtime: moduleupgrade.RuntimeSummaryV1{Mode: m.Runtime.Mode, Protocol: m.Runtime.Protocol, Entrypoint: m.Runtime.Entrypoint}, Provides: append([]moduleapi.PortRef(nil), m.Provides...), Requires: append([]moduleapi.PortRef(nil), m.Requires...), RequestedPermissions: append([]moduleapi.Permission(nil), m.RequestedPermissions...)}
}
func activatedRefFromStore(a ModuleActivation, i ModuleInstallation) moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{ModuleID: i.ModuleID, Version: i.ExactVersion, ArtifactDigest: i.ArtifactDigest, InstanceID: a.InstanceID, ExecutionClass: a.ExecutionClass, AdapterIdentity: a.AdapterIdentity, ActivationRevision: a.ActivationRevision}
}

func equalModuleUpgradeBasis(a, b ModuleUpgradeReviewBasis) bool {
	return a.CandidateID == b.CandidateID && a.Selection.ArtifactAdmissionID == b.Selection.ArtifactAdmissionID && bytes.Equal(a.CandidateCanonical, b.CandidateCanonical) && a.PublishedBasis == b.PublishedBasis && a.Source.PolicyID == b.Source.PolicyID && a.Source.PolicyRevision == b.Source.PolicyRevision && a.Source.CurrentSnapshotID == b.Source.CurrentSnapshotID && a.Source.ObservationRevision == b.Source.ObservationRevision && a.Snapshot.SnapshotID == b.Snapshot.SnapshotID && a.CurrentActivation == b.CurrentActivation && a.CurrentInstallation.InstallationID == b.CurrentInstallation.InstallationID && a.CurrentInstallation.ManifestRef == b.CurrentInstallation.ManifestRef && bytes.Equal(a.CurrentInstallation.ManifestBytes, b.CurrentInstallation.ManifestBytes) && reflect.DeepEqual(a.BindingImpacts, b.BindingImpacts)
}
func reviewMatchesLiveBasis(r moduleupgrade.ReviewV1, b ModuleUpgradeReviewBasis) bool {
	return requireReviewMatchesBasis(r, b) == nil
}
func selectionFromReview(r moduleupgrade.ReviewV1) ReadModuleUpgradeReviewBasisInput {
	return ReadModuleUpgradeReviewBasisInput{SourceID: r.SupplyBasis.SourceID, SnapshotID: r.SupplyBasis.SnapshotID, TenantID: r.TenantID, ArtifactAdmissionID: r.ArtifactAdmissionID, BindingTarget: r.BindingTarget, Port: r.Port, PortBindingIndex: r.PortBindingIndex, TargetModule: r.Target.Module, TargetArtifactDigest: r.Target.ArtifactDigest, TargetInstanceID: r.TargetInstanceID}
}
func mapUpgradeBasisCommitError(err error) error {
	if errors.Is(err, ErrModuleUpgradeSuppressed) {
		return err
	}
	if errors.Is(err, ErrInvalidModuleUpgradeReview) {
		return err
	}
	return errors.Join(ErrModuleUpgradeReviewStale, err)
}

func insertModuleUpgradeCandidate(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, b ModuleUpgradeReviewBasis, now int64) error {
	result, err := q.ExecContext(ctx, `INSERT INTO module_upgrade_candidates(candidate_id,candidate_canonical,candidate_size_bytes,review_key,source_id,source_policy_id,snapshot_id,target_module_id,target_exact_version,target_artifact_digest,current_module_id,current_exact_version,current_artifact_digest,admitted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(candidate_id) DO NOTHING`, b.CandidateID, b.CandidateCanonical, len(b.CandidateCanonical), b.Candidate.ReviewKey, b.Source.SourceID, b.Source.PolicyID, b.Snapshot.SnapshotID, b.Candidate.Target.Module.ID, b.Candidate.Target.Module.Version, b.Candidate.Target.ArtifactDigest, b.Candidate.Current.Module.ID, b.Candidate.Current.Module.Version, b.Candidate.Current.ArtifactDigest, now)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		existing, found, err := queryModuleUpgradeCandidate(ctx, q, b.CandidateID)
		if err != nil {
			return err
		}
		if !found || !bytes.Equal(existing.Canonical, b.CandidateCanonical) {
			return ErrModuleUpgradeIntegrity
		}
	}
	return nil
}

func queryModuleUpgradeCandidate(ctx context.Context, q moduleDiscoveryQueryer, id string) (ModuleUpgradeCandidateRecord, bool, error) {
	var r ModuleUpgradeCandidateRecord
	var canonical []byte
	var sourcePolicyID, targetID, targetVersion, targetDigest, currentID, currentVersion, currentDigest string
	var admitted int64
	err := q.QueryRowContext(ctx, `SELECT candidate_id,candidate_canonical,review_key,source_id,source_policy_id,snapshot_id,target_module_id,target_exact_version,target_artifact_digest,current_module_id,current_exact_version,current_artifact_digest,admitted_at FROM module_upgrade_candidates WHERE candidate_id=?`, id).Scan(&r.CandidateID, &canonical, &r.ReviewKey, &r.SourceID, &sourcePolicyID, &r.SnapshotID, &targetID, &targetVersion, &targetDigest, &currentID, &currentVersion, &currentDigest, &admitted)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	snapshot, found, err := queryModuleDiscoverySnapshot(ctx, q, r.SnapshotID)
	if err != nil || !found {
		return r, false, errors.Join(err, ErrModuleUpgradeIntegrity)
	}
	candidate, err := moduleapi.RestoreModuleUpgradeCandidateV1(canonical, r.CandidateID, snapshot.SnapshotCanonical, snapshot.SnapshotID)
	if err != nil || candidate.ReviewKey != r.ReviewKey || candidate.SourceID != r.SourceID || candidate.SourcePolicyID != sourcePolicyID || candidate.Target.Module.ID != targetID || candidate.Target.Module.Version != targetVersion || candidate.Target.ArtifactDigest != targetDigest || candidate.Current == nil || candidate.Current.Module.ID != currentID || candidate.Current.Module.Version != currentVersion || candidate.Current.ArtifactDigest != currentDigest {
		return r, false, fmt.Errorf("%w: Candidate %s projection differs", ErrModuleUpgradeIntegrity, id)
	}
	r.Candidate = candidate
	r.Canonical = bytes.Clone(canonical)
	r.AdmittedAt, err = timeFromUnixMicro(admitted)
	return r, true, err
}

func queryModuleUpgradeReview(ctx context.Context, q moduleDiscoveryQueryer, id string) (ModuleUpgradeReviewRecord, bool, error) {
	var r ModuleUpgradeReviewRecord
	var canonical []byte
	var candidateID, reviewKey, tenant, kind string
	var profile, workspace, endpoint sql.NullString
	var admissionID, operatorPrincipalID, reviewRequestDigest sql.NullString
	var currentInstance, targetInstance, targetManifestRef, installationID, activationID, controlID, controlDigest, catalogID, catalogDigest, conclusion string
	var activationRev, pointerRev, controlRev, catalogRev, created int64
	err := q.QueryRowContext(ctx, `SELECT review_id,review_canonical,tenant_id,candidate_id,review_key,binding_target_kind,profile_id,workspace_id,endpoint_id,current_instance_id,target_instance_id,target_manifest_ref,current_installation_id,current_activation_id,current_activation_revision,pointer_revision,control_snapshot_id,control_revision,control_digest,catalog_generation_id,catalog_generation,catalog_digest,conclusion,created_at,artifact_admission_id,operator_principal_id,review_request_digest FROM module_upgrade_reviews WHERE review_id=?`, id).Scan(&r.ReviewID, &canonical, &tenant, &candidateID, &reviewKey, &kind, &profile, &workspace, &endpoint, &currentInstance, &targetInstance, &targetManifestRef, &installationID, &activationID, &activationRev, &pointerRev, &controlID, &controlRev, &controlDigest, &catalogID, &catalogRev, &catalogDigest, &conclusion, &created, &admissionID, &operatorPrincipalID, &reviewRequestDigest)
	if err != nil && strings.Contains(err.Error(), "no such column: artifact_admission_id") {
		// FAC2 v1 remains a known read-only backup source. Its caller-owned U3
		// Reviews predate the W6.6 relational closure and therefore restore the
		// three new projections as NULL/empty values.
		err = q.QueryRowContext(ctx, `SELECT review_id,review_canonical,tenant_id,candidate_id,review_key,binding_target_kind,profile_id,workspace_id,endpoint_id,current_instance_id,target_instance_id,target_manifest_ref,current_installation_id,current_activation_id,current_activation_revision,pointer_revision,control_snapshot_id,control_revision,control_digest,catalog_generation_id,catalog_generation,catalog_digest,conclusion,created_at FROM module_upgrade_reviews WHERE review_id=?`, id).Scan(&r.ReviewID, &canonical, &tenant, &candidateID, &reviewKey, &kind, &profile, &workspace, &endpoint, &currentInstance, &targetInstance, &targetManifestRef, &installationID, &activationID, &activationRev, &pointerRev, &controlID, &controlRev, &controlDigest, &catalogID, &catalogRev, &catalogDigest, &conclusion, &created)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	candidate, found, err := queryModuleUpgradeCandidate(ctx, q, candidateID)
	if err != nil || !found {
		return r, false, errors.Join(err, ErrModuleUpgradeIntegrity)
	}
	snapshot, found, err := queryModuleDiscoverySnapshot(ctx, q, candidate.SnapshotID)
	if err != nil || !found {
		return r, false, errors.Join(err, ErrModuleUpgradeIntegrity)
	}
	review, err := moduleupgrade.RestoreReviewV1(canonical, id, candidate.Canonical, snapshot.SnapshotCanonical)
	if err != nil {
		return r, false, fmt.Errorf("%w: Review %s: %v", ErrModuleUpgradeIntegrity, id, err)
	}
	t := review.BindingTarget
	if tenant != review.TenantID || candidateID != review.CandidateID || reviewKey != review.ReviewKey || kind != string(t.Kind) || (profile.Valid && profile.String != t.ProfileID) || profile.Valid != (t.ProfileID != "") || (workspace.Valid && workspace.String != t.WorkspaceID) || workspace.Valid != (t.WorkspaceID != "") || (endpoint.Valid && endpoint.String != t.EndpointID) || endpoint.Valid != (t.EndpointID != "") || currentInstance != review.Current.Activation.InstanceID || targetInstance != review.TargetInstanceID || targetManifestRef != review.Target.ManifestRef || installationID != review.Current.InstallationID || activationRev != int64(review.Current.Activation.ActivationRevision) || pointerRev != int64(review.PublishedBasis.PointerRevision) || controlID != review.PublishedBasis.Control.SnapshotID || controlRev != int64(review.PublishedBasis.Control.Revision) || controlDigest != review.PublishedBasis.Control.Digest || catalogID != review.PublishedBasis.Catalog.GenerationID || catalogRev != int64(review.PublishedBasis.Catalog.Generation) || catalogDigest != review.PublishedBasis.Catalog.Digest || conclusion != string(review.Conclusion) || nullableModuleUpgradeString(admissionID) != review.ArtifactAdmissionID || nullableModuleUpgradeString(operatorPrincipalID) != review.OperatorPrincipalID || nullableModuleUpgradeString(reviewRequestDigest) != review.ReviewRequestDigest {
		return r, false, fmt.Errorf("%w: Review %s projection differs", ErrModuleUpgradeIntegrity, id)
	}
	activation, err := queryModuleActivationByID(ctx, q, activationID)
	if err != nil ||
		activation.TenantID != review.TenantID ||
		activation.InstanceID != currentInstance ||
		activation.InstallationID != installationID ||
		activation.ActivationRevision != review.Current.Activation.ActivationRevision ||
		activation.ExecutionClass != review.Current.Activation.ExecutionClass ||
		activation.AdapterIdentity != review.Current.Activation.AdapterIdentity {
		return r, false, errors.Join(err, ErrModuleUpgradeIntegrity)
	}
	r.Review = review
	r.Canonical = bytes.Clone(canonical)
	r.Candidate = candidate
	r.CreatedAt, err = timeFromUnixMicro(created)
	return r, true, err
}

func nullableModuleUpgradeValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableModuleUpgradeString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func parseCandidateDecision(canonical []byte) (moduleapi.ModuleCandidateDecisionV1, []byte, string, error) {
	var probe moduleapi.ModuleCandidateDecisionV1
	checked, err := moduleapi.CanonicalJSON(canonical)
	if err != nil || !bytes.Equal(checked, canonical) {
		return probe, nil, "", fmt.Errorf("%w: Decision is not exact canonical", ErrInvalidModuleUpgradeReview)
	}
	// Derive the content ID using the same frozen domain, then let Restore do
	// strict unknown-field and canonical validation.
	decisionID := moduleapi.Digest("freeagent.module-candidate-decision/v1", canonical)
	decision, err := moduleapi.RestoreModuleCandidateDecisionV1(canonical, decisionID)
	if err != nil {
		return probe, nil, "", fmt.Errorf("%w: Decision: %v", ErrInvalidModuleUpgradeReview, err)
	}
	return decision, bytes.Clone(canonical), decisionID, nil
}

func queryModuleCandidateDecisionByReview(ctx context.Context, q moduleDiscoveryQueryer, reviewID string) (ModuleCandidateDecisionRecord, bool, error) {
	var r ModuleCandidateDecisionRecord
	var canonical []byte
	var decided int64
	err := q.QueryRowContext(ctx, `SELECT decision_id,decision_canonical,tenant_id,review_id,review_key,decided_at FROM module_candidate_decisions WHERE review_id=?`, reviewID).Scan(&r.DecisionID, &canonical, &r.TenantID, &r.ReviewID, &r.ReviewKey, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	decision, err := moduleapi.RestoreModuleCandidateDecisionV1(canonical, r.DecisionID)
	if err != nil {
		return r, false, fmt.Errorf("%w: Decision %s: %v", ErrModuleUpgradeIntegrity, r.DecisionID, err)
	}
	var candidateID, decisionValue string
	if err := q.QueryRowContext(ctx, `SELECT candidate_id,decision FROM module_candidate_decisions WHERE review_id=?`, reviewID).Scan(&candidateID, &decisionValue); err != nil {
		return r, false, err
	}
	if candidateID != decision.CandidateID || decisionValue != string(decision.Decision) {
		return r, false, ErrModuleUpgradeIntegrity
	}
	r.Decision = decision
	r.Canonical = bytes.Clone(canonical)
	r.DecidedAt, err = timeFromUnixMicro(decided)
	return r, true, err
}

func detachModuleUpgradeReviewBasis(v ModuleUpgradeReviewBasis) ModuleUpgradeReviewBasis {
	v.Source = detachModuleSource(v.Source)
	if v.PublisherKey != nil {
		x := detachModulePublisherKey(*v.PublisherKey)
		v.PublisherKey = &x
	}
	v.Snapshot = detachModuleDiscoverySnapshot(v.Snapshot)
	v.CandidateCanonical = bytes.Clone(v.CandidateCanonical)
	v.SelectedBinding.StaticContextRefs = append([]string(nil), v.SelectedBinding.StaticContextRefs...)
	v.BindingImpacts = cloneUpgradeImpacts(v.BindingImpacts)
	v.CurrentInstallation = cloneModuleInstallation(v.CurrentInstallation)
	return v
}
func cloneUpgradeImpacts(v []moduleupgrade.BindingImpactV1) []moduleupgrade.BindingImpactV1 {
	out := append([]moduleupgrade.BindingImpactV1(nil), v...)
	for i := range out {
		out[i].StaticContextRefs = append([]string(nil), out[i].StaticContextRefs...)
	}
	return out
}
func detachModuleUpgradeCandidateRecord(v ModuleUpgradeCandidateRecord) ModuleUpgradeCandidateRecord {
	v.Canonical = bytes.Clone(v.Canonical)
	return v
}
func detachModuleUpgradeReviewRecord(v ModuleUpgradeReviewRecord) ModuleUpgradeReviewRecord {
	v.Canonical = bytes.Clone(v.Canonical)
	v.Candidate = detachModuleUpgradeCandidateRecord(v.Candidate)
	return v
}
func detachModuleCandidateDecisionRecord(v ModuleCandidateDecisionRecord) ModuleCandidateDecisionRecord {
	v.Canonical = bytes.Clone(v.Canonical)
	return v
}
