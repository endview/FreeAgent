package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/internal/moduledisablecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	maximumStoredControlRequestBytesV1       = 8 << 10
	maximumStoredControlInputBytesV1         = 8 << 10
	maximumStoredControlEvaluationBytesV1    = 64 << 10
	maximumStoredControlReceiptBytesV1       = 16 << 10
	maximumStoredControlBasisBytesV1         = 8 << 10
	maximumStoredControlDomainReceiptBytesV1 = 128 << 10
	maximumStoredControlCanonicalBytesV1     = 256 << 10
	maximumStoredControlReceiptsPerTenantV1  = 1024
	maximumStoredControlReceiptsV1           = 8192
)

var (
	// ErrInvalidControlOperationReceiptV1 identifies malformed resolver or
	// commit input. It never treats a caller-supplied digest as authority.
	ErrInvalidControlOperationReceiptV1 = errors.New(
		"currentstore: invalid Control operation receipt input",
	)

	// ErrControlOperationReceiptNotFoundV1 identifies an exact idempotency
	// identity for which no durable receipt exists.
	ErrControlOperationReceiptNotFoundV1 = errors.New(
		"currentstore: Control operation receipt not found",
	)

	// ErrControlOperationReceiptConflictV1 identifies an existing exact
	// idempotency identity bound to a different Request digest.
	ErrControlOperationReceiptConflictV1 = errors.New(
		"currentstore: Control operation receipt request conflict",
	)

	// ErrControlOperationReceiptIntegrityV1 identifies a row or immutable
	// parent that does not reconstruct the complete MODULE_DISABLE meaning.
	ErrControlOperationReceiptIntegrityV1 = errors.New(
		"currentstore: Control operation receipt integrity violation",
	)

	// ErrControlOperationReceiptCapacityV1 identifies a fail-closed Tenant or
	// Store receipt quota. Historical rows are never evicted.
	ErrControlOperationReceiptCapacityV1 = errors.New(
		"currentstore: Control operation receipt capacity exhausted",
	)
)

// ControlOperationReceiptIdentityV1 is the sole durable idempotency identity.
// Tenant identity is already bound by ScopeDigest and is independently
// recovered from the exact Request; duplicating it here would permit drift.
type ControlOperationReceiptIdentityV1 struct {
	PrincipalID          string
	ScopeDigest          string
	Operation            controlapicontract.ControlOperationV1
	IdempotencyKeyDigest string
}

// Validate rejects every identity outside the first narrow MODULE_DISABLE
// mutation slice.
func (identity ControlOperationReceiptIdentityV1) Validate() error {
	if !validControlReceiptOpaqueIDV1(identity.PrincipalID) ||
		!moduleapi.ValidSHA256(identity.ScopeDigest) ||
		identity.Operation != controlapicontract.OperationModuleDisableV1 ||
		!moduleapi.ValidSHA256(identity.IdempotencyKeyDigest) {
		return ErrInvalidControlOperationReceiptV1
	}
	return nil
}

// ControlOperationReceiptNotFoundErrorV1 is the typed negative lookup result.
type ControlOperationReceiptNotFoundErrorV1 struct {
	Identity ControlOperationReceiptIdentityV1
}

func (failure *ControlOperationReceiptNotFoundErrorV1) Error() string {
	return ErrControlOperationReceiptNotFoundV1.Error()
}

func (failure *ControlOperationReceiptNotFoundErrorV1) Unwrap() error {
	return ErrControlOperationReceiptNotFoundV1
}

// ControlOperationReceiptConflictErrorV1 is returned when one exact durable
// identity has already been consumed by another semantic Request.
type ControlOperationReceiptConflictErrorV1 struct {
	Identity ControlOperationReceiptIdentityV1
}

func (failure *ControlOperationReceiptConflictErrorV1) Error() string {
	return ErrControlOperationReceiptConflictV1.Error()
}

func (failure *ControlOperationReceiptConflictErrorV1) Unwrap() error {
	return ErrControlOperationReceiptConflictV1
}

// StoredControlOperationReceiptV1 is a detached immutable-fact DTO. Every
// canonical slice is copied from SQLite, strictly restored, and cross-closed
// before return. Mutating a returned slice cannot alter Store state or a later
// resolution.
type StoredControlOperationReceiptV1 struct {
	ReceiptDigest         string
	TenantID              string
	Identity              ControlOperationReceiptIdentityV1
	AuthorizationRevision uint64
	ScopeSetDigest        string
	Status                controlapicontract.ControlOperationStatusV1

	Request          controlapicontract.ControlOperationRequestV1
	RequestCanonical []byte
	RequestDigest    string

	Input          moduledisablecontract.ModuleDisableDryRunBodyV1
	InputCanonical []byte
	InputDigest    string
	Plan           moduleapplyplan.ProfileContextDisablePlanV1
	PlanCanonical  []byte
	PlanDigest     string

	Evaluation          moduledisablecontract.ModuleDisableEvaluationV1
	EvaluationCanonical []byte
	EvaluationDigest    string

	ControlReceipt          controlapicontract.ControlOperationReceiptV1
	ControlReceiptCanonical []byte

	PreBasis          controlapicontract.PublishedBasisRefV1
	PreBasisCanonical []byte
	PreBasisDigest    string

	PostBasis          controlapicontract.PublishedBasisRefV1
	PostBasisCanonical []byte
	PostBasisDigest    string

	DomainReceipt          *moduledisablecontract.ModuleDisablePublicationReceiptV1
	DomainReceiptCanonical []byte
	DomainReceiptRef       *controlapicontract.DomainReceiptRefV1
}

// CommitModuleDisableControlReceiptInputV1 contains the complete exact facts
// for the first durable NO_CHANGE outcome. Principal, scope, operation,
// idempotency identity, Tenant, status, and parent IDs are derived from these
// canonical contracts rather than accepted as parallel caller assertions.
// APPLIED is intentionally unavailable here: it must be inserted by a future
// Current Store publication method inside the same transaction as its pointer
// CAS, never backfilled after publication.
type CommitModuleDisableControlReceiptInputV1 struct {
	AuthorizationRevision uint64
	ScopeSetDigest        string

	RequestCanonical []byte
	RequestDigest    string
	InputCanonical   []byte
	InputDigest      string

	EvaluationCanonical []byte
	EvaluationDigest    string

	ControlReceiptCanonical []byte
	ControlReceiptDigest    string

	PublishedBasisCanonical []byte
	PublishedBasisDigest    string
}

type controlOperationReceiptMetadataV1 struct {
	receiptDigest        string
	tenantID             string
	principalID          string
	scopeDigest          string
	operation            controlapicontract.ControlOperationV1
	idempotencyKeyDigest string
	policyRevision       int64
	scopeSetDigest       string
	status               controlapicontract.ControlOperationStatusV1

	requestDigest      string
	requestSize        int64
	inputDigest        string
	inputSize          int64
	evaluationDigest   string
	evaluationSize     int64
	controlReceiptSize int64
	preBasisDigest     string
	preBasisSize       int64
	postBasisDigest    string
	postBasisSize      int64

	domainKind   sql.NullString
	domainID     sql.NullString
	domainDigest sql.NullString
	domainSize   sql.NullInt64
	totalSize    int64

	preControlID  string
	preCatalogID  string
	postControlID string
	postCatalogID string
}

type controlOperationReceiptPayloadV1 struct {
	request        []byte
	input          []byte
	evaluation     []byte
	controlReceipt []byte
	preBasis       []byte
	postBasis      []byte
	domainReceipt  []byte
}

type controlReceiptRowScannerV1 interface {
	Scan(...any) error
}

// ResolveControlOperationReceiptV1 derives the sole durable identity from one
// exact canonical Request and returns its exact receipt only when the stored
// Request digest is identical. It accepts no parallel principal/scope/key
// assertions. A different Request at the same identity is a typed conflict;
// absence is a typed not-found result.
func (store *Store) ResolveControlOperationReceiptV1(
	ctx context.Context,
	requestCanonical []byte,
	requestDigest string,
) (record StoredControlOperationReceiptV1, returnErr error) {
	defer func() {
		if store != nil {
			returnErr = classifySQLiteOwnerContention(store.path, returnErr)
		}
	}()
	if ctx == nil {
		return StoredControlOperationReceiptV1{},
			ErrInvalidControlOperationReceiptV1
	}
	_, identity, err := preflightControlOperationReceiptRequestV1(
		requestCanonical,
		requestDigest,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{}, err
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return StoredControlOperationReceiptV1{}, err
	}
	defer unlock()

	metadata, found, err := queryControlOperationReceiptMetadataByIdentityV1(
		ctx,
		store.db,
		identity,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{}, fmt.Errorf(
			"currentstore: resolve Control operation receipt metadata: %w",
			err,
		)
	}
	if !found {
		return StoredControlOperationReceiptV1{},
			&ControlOperationReceiptNotFoundErrorV1{Identity: identity}
	}
	if metadata.requestDigest != requestDigest {
		return StoredControlOperationReceiptV1{},
			&ControlOperationReceiptConflictErrorV1{Identity: identity}
	}
	return restoreStoredControlOperationReceiptV1(ctx, store.db, metadata)
}

// CommitModuleDisableControlReceiptV1 atomically inserts only a fully replayed
// NO_CHANGE receipt. Exact retry returns the original row; another Request at
// the same identity conflicts. This method cannot publish Control/Catalog and
// cannot insert APPLIED, so it cannot create or claim a domain effect.
func (store *Store) CommitModuleDisableControlReceiptV1(
	ctx context.Context,
	input CommitModuleDisableControlReceiptInputV1,
) (
	record StoredControlOperationReceiptV1,
	created bool,
	returnErr error,
) {
	defer func() {
		if store != nil {
			returnErr = classifySQLiteOwnerContention(store.path, returnErr)
		}
	}()
	if ctx == nil {
		return StoredControlOperationReceiptV1{}, false,
			ErrInvalidControlOperationReceiptV1
	}
	frozenInput := cloneCommitModuleDisableControlReceiptInputV1(input)
	request, identity, err := preflightControlOperationReceiptRequestV1(
		frozenInput.RequestCanonical,
		frozenInput.RequestDigest,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
			"currentstore: acquire Control receipt connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
			"currentstore: begin Control receipt commit: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	existing, found, err := queryControlOperationReceiptMetadataByIdentityV1(
		ctx,
		connection,
		identity,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
			"currentstore: inspect Control receipt identity: %w",
			err,
		)
	}
	if found {
		if existing.requestDigest != frozenInput.RequestDigest {
			return StoredControlOperationReceiptV1{}, false,
				&ControlOperationReceiptConflictErrorV1{Identity: identity}
		}
		record, err = restoreStoredControlOperationReceiptV1(
			ctx,
			connection,
			existing,
		)
		if err != nil {
			return StoredControlOperationReceiptV1{}, false, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
				"currentstore: commit Control receipt exact retry: %w",
				err,
			)
		}
		committed = true
		return record, false, nil
	}
	metadata, payload, err := prepareNoChangeControlOperationReceiptV1(
		frozenInput,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}
	if request.PrincipalID != metadata.principalID ||
		request.ScopeDigest != metadata.scopeDigest ||
		request.Operation != metadata.operation ||
		request.IdempotencyKeyDigest != metadata.idempotencyKeyDigest {
		return StoredControlOperationReceiptV1{}, false,
			ErrInvalidControlOperationReceiptV1
	}

	if err := requireCurrentControlReceiptBasisV1(
		ctx,
		connection,
		metadata,
		payload,
	); err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}
	if err := insertPreparedControlOperationReceiptV1(
		ctx,
		connection,
		metadata,
		payload,
	); err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}
	record, err = restoreStoredControlOperationReceiptV1(
		ctx,
		connection,
		metadata,
	)
	if err != nil {
		return StoredControlOperationReceiptV1{}, false, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return StoredControlOperationReceiptV1{}, false, fmt.Errorf(
			"currentstore: commit Control operation receipt: %w",
			err,
		)
	}
	committed = true
	return record, true, nil
}

func preflightControlOperationReceiptRequestV1(
	canonical []byte,
	digest string,
) (
	controlapicontract.ControlOperationRequestV1,
	ControlOperationReceiptIdentityV1,
	error,
) {
	request, err := controlapicontract.RestoreControlOperationRequestV1(
		bytes.Clone(canonical),
		digest,
	)
	if err != nil || request.Operation != controlapicontract.OperationModuleDisableV1 ||
		request.Intent != controlapicontract.OperationIntentMutateV1 {
		return controlapicontract.ControlOperationRequestV1{},
			ControlOperationReceiptIdentityV1{},
			fmt.Errorf("%w: restore Request", ErrInvalidControlOperationReceiptV1)
	}
	identity := ControlOperationReceiptIdentityV1{
		PrincipalID:          request.PrincipalID,
		ScopeDigest:          request.ScopeDigest,
		Operation:            request.Operation,
		IdempotencyKeyDigest: request.IdempotencyKeyDigest,
	}
	if identity.Validate() != nil {
		return controlapicontract.ControlOperationRequestV1{},
			ControlOperationReceiptIdentityV1{},
			ErrInvalidControlOperationReceiptV1
	}
	return request, identity, nil
}

func cloneCommitModuleDisableControlReceiptInputV1(
	input CommitModuleDisableControlReceiptInputV1,
) CommitModuleDisableControlReceiptInputV1 {
	input.RequestCanonical = bytes.Clone(input.RequestCanonical)
	input.InputCanonical = bytes.Clone(input.InputCanonical)
	input.EvaluationCanonical = bytes.Clone(input.EvaluationCanonical)
	input.ControlReceiptCanonical = bytes.Clone(input.ControlReceiptCanonical)
	input.PublishedBasisCanonical = bytes.Clone(input.PublishedBasisCanonical)
	return input
}

func prepareNoChangeControlOperationReceiptV1(
	input CommitModuleDisableControlReceiptInputV1,
) (controlOperationReceiptMetadataV1, controlOperationReceiptPayloadV1, error) {
	payload := controlOperationReceiptPayloadV1{
		request:        bytes.Clone(input.RequestCanonical),
		input:          bytes.Clone(input.InputCanonical),
		evaluation:     bytes.Clone(input.EvaluationCanonical),
		controlReceipt: bytes.Clone(input.ControlReceiptCanonical),
		preBasis:       bytes.Clone(input.PublishedBasisCanonical),
		postBasis:      bytes.Clone(input.PublishedBasisCanonical),
	}
	request, err := controlapicontract.RestoreControlOperationRequestV1(
		payload.request,
		input.RequestDigest,
	)
	if err != nil {
		return controlOperationReceiptMetadataV1{},
			controlOperationReceiptPayloadV1{},
			fmt.Errorf("%w: restore Request", ErrInvalidControlOperationReceiptV1)
	}
	receipt, err := controlapicontract.RestoreControlOperationReceiptV1(
		payload.controlReceipt,
		input.ControlReceiptDigest,
	)
	if err != nil ||
		receipt.Status != controlapicontract.OperationStatusNoChangeV1 {
		return controlOperationReceiptMetadataV1{},
			controlOperationReceiptPayloadV1{},
			fmt.Errorf("%w: receipt is not exact NO_CHANGE", ErrInvalidControlOperationReceiptV1)
	}
	basis, err := controlapicontract.RestorePublishedBasisRefV1(
		payload.preBasis,
		input.PublishedBasisDigest,
	)
	if err != nil {
		return controlOperationReceiptMetadataV1{},
			controlOperationReceiptPayloadV1{},
			fmt.Errorf("%w: restore PublishedBasis", ErrInvalidControlOperationReceiptV1)
	}
	metadata := controlOperationReceiptMetadataV1{
		receiptDigest:        input.ControlReceiptDigest,
		tenantID:             request.Scope.TenantID,
		principalID:          request.PrincipalID,
		scopeDigest:          request.ScopeDigest,
		operation:            request.Operation,
		idempotencyKeyDigest: request.IdempotencyKeyDigest,
		policyRevision:       int64(input.AuthorizationRevision),
		scopeSetDigest:       input.ScopeSetDigest,
		status:               receipt.Status,
		requestDigest:        input.RequestDigest,
		requestSize:          int64(len(payload.request)),
		inputDigest:          input.InputDigest,
		inputSize:            int64(len(payload.input)),
		evaluationDigest:     input.EvaluationDigest,
		evaluationSize:       int64(len(payload.evaluation)),
		controlReceiptSize:   int64(len(payload.controlReceipt)),
		preBasisDigest:       input.PublishedBasisDigest,
		preBasisSize:         int64(len(payload.preBasis)),
		postBasisDigest:      input.PublishedBasisDigest,
		postBasisSize:        int64(len(payload.postBasis)),
		preControlID:         basis.Control.ID,
		preCatalogID:         basis.Catalog.ID,
		postControlID:        basis.Control.ID,
		postCatalogID:        basis.Catalog.ID,
	}
	metadata.totalSize = metadata.requestSize + metadata.inputSize +
		metadata.evaluationSize + metadata.controlReceiptSize +
		metadata.preBasisSize + metadata.postBasisSize
	if err := validateControlOperationReceiptMetadataV1(metadata); err != nil {
		return controlOperationReceiptMetadataV1{},
			controlOperationReceiptPayloadV1{},
			err
	}
	return metadata, payload, nil
}

func requireCurrentControlReceiptBasisV1(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	metadata controlOperationReceiptMetadataV1,
	payload controlOperationReceiptPayloadV1,
) error {
	var controlID, catalogID string
	var pointerRevision int64
	err := q.QueryRowContext(ctx, `
		SELECT snapshot_id, catalog_generation_id, pointer_revision
		FROM control_current
		WHERE tenant_id=?
	`, metadata.tenantID).Scan(&controlID, &catalogID, &pointerRevision)
	if err != nil || controlID != metadata.preControlID ||
		catalogID != metadata.preCatalogID || pointerRevision <= 0 {
		return fmt.Errorf(
			"%w: NO_CHANGE basis is not current",
			ErrControlOperationReceiptIntegrityV1,
		)
	}
	basis, err := controlapicontract.RestorePublishedBasisRefV1(
		payload.preBasis,
		metadata.preBasisDigest,
	)
	if err != nil || uint64(pointerRevision) != basis.PointerRevision {
		return fmt.Errorf(
			"%w: NO_CHANGE pointer revision differs",
			ErrControlOperationReceiptIntegrityV1,
		)
	}
	return nil
}

func insertPreparedControlOperationReceiptV1(
	ctx context.Context,
	connection *sql.Conn,
	metadata controlOperationReceiptMetadataV1,
	payload controlOperationReceiptPayloadV1,
) error {
	// Keep the raw INSERT private and inseparable from complete semantic
	// validation. Future APPLIED publication wiring may call this helper only
	// after its immutable parents and pointer CAS are visible in the same
	// transaction; malformed prepared rows can never reach SQLite.
	if _, err := restoreControlOperationReceiptFactsV1(
		ctx,
		connection,
		metadata,
		cloneControlOperationReceiptPayloadV1(payload),
	); err != nil {
		return err
	}
	var domainKind, domainID, domainDigest, domainCanonical, domainSize any
	if metadata.domainKind.Valid {
		domainKind = metadata.domainKind.String
		domainID = metadata.domainID.String
		domainDigest = metadata.domainDigest.String
		domainCanonical = payload.domainReceipt
		domainSize = metadata.domainSize.Int64
	}
	_, err := connection.ExecContext(ctx, `
		INSERT INTO control_operation_receipts(
			receipt_digest, tenant_id, principal_id, scope_digest,
			operation, idempotency_key_digest, authorization_revision,
			scope_set_digest, status,
			request_digest, request_canonical, request_size_bytes,
			input_digest, input_canonical, input_size_bytes,
			evaluation_digest, evaluation_canonical, evaluation_size_bytes,
			control_receipt_canonical, control_receipt_size_bytes,
			pre_basis_digest, pre_basis_canonical, pre_basis_size_bytes,
			post_basis_digest, post_basis_canonical, post_basis_size_bytes,
			domain_receipt_kind, domain_receipt_id, domain_receipt_digest,
			domain_receipt_canonical, domain_receipt_size_bytes,
			canonical_total_size_bytes,
			pre_control_snapshot_id, pre_catalog_generation_id,
			post_control_snapshot_id, post_catalog_generation_id
		) VALUES(
			?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?, ?,?,?,?,?, ?,?,?,?,?,?,
			?,?,?,?,?, ?,?,?,?,?
		)
	`,
		metadata.receiptDigest,
		metadata.tenantID,
		metadata.principalID,
		metadata.scopeDigest,
		string(metadata.operation),
		metadata.idempotencyKeyDigest,
		metadata.policyRevision,
		metadata.scopeSetDigest,
		string(metadata.status),
		metadata.requestDigest,
		payload.request,
		metadata.requestSize,
		metadata.inputDigest,
		payload.input,
		metadata.inputSize,
		metadata.evaluationDigest,
		payload.evaluation,
		metadata.evaluationSize,
		payload.controlReceipt,
		metadata.controlReceiptSize,
		metadata.preBasisDigest,
		payload.preBasis,
		metadata.preBasisSize,
		metadata.postBasisDigest,
		payload.postBasis,
		metadata.postBasisSize,
		domainKind,
		domainID,
		domainDigest,
		domainCanonical,
		domainSize,
		metadata.totalSize,
		metadata.preControlID,
		metadata.preCatalogID,
		metadata.postControlID,
		metadata.postCatalogID,
	)
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.Contains(message, "quota exceeded") {
		return fmt.Errorf("%w: %v", ErrControlOperationReceiptCapacityV1, err)
	}
	if strings.Contains(message, "append-only") {
		return fmt.Errorf("%w: %v", ErrControlOperationReceiptConflictV1, err)
	}
	return fmt.Errorf(
		"%w: insert exact row: %v",
		ErrControlOperationReceiptIntegrityV1,
		err,
	)
}

func queryControlOperationReceiptMetadataByIdentityV1(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	identity ControlOperationReceiptIdentityV1,
) (controlOperationReceiptMetadataV1, bool, error) {
	return scanControlOperationReceiptMetadataOptionalV1(q.QueryRowContext(ctx, `
		SELECT
			receipt_digest, tenant_id, principal_id, scope_digest,
			operation, idempotency_key_digest, authorization_revision,
			scope_set_digest, status,
			request_digest, request_size_bytes,
			input_digest, input_size_bytes,
			evaluation_digest, evaluation_size_bytes,
			control_receipt_size_bytes,
			pre_basis_digest, pre_basis_size_bytes,
			post_basis_digest, post_basis_size_bytes,
			domain_receipt_kind, domain_receipt_id, domain_receipt_digest,
			domain_receipt_size_bytes, canonical_total_size_bytes,
			pre_control_snapshot_id, pre_catalog_generation_id,
			post_control_snapshot_id, post_catalog_generation_id
		FROM control_operation_receipts
		WHERE principal_id=? AND scope_digest=? AND operation=?
		  AND idempotency_key_digest=?
	`,
		identity.PrincipalID,
		identity.ScopeDigest,
		string(identity.Operation),
		identity.IdempotencyKeyDigest,
	))
}

func queryControlOperationReceiptMetadataByDigestV1(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	receiptDigest string,
) (controlOperationReceiptMetadataV1, bool, error) {
	return scanControlOperationReceiptMetadataOptionalV1(q.QueryRowContext(ctx, `
		SELECT
			receipt_digest, tenant_id, principal_id, scope_digest,
			operation, idempotency_key_digest, authorization_revision,
			scope_set_digest, status,
			request_digest, request_size_bytes,
			input_digest, input_size_bytes,
			evaluation_digest, evaluation_size_bytes,
			control_receipt_size_bytes,
			pre_basis_digest, pre_basis_size_bytes,
			post_basis_digest, post_basis_size_bytes,
			domain_receipt_kind, domain_receipt_id, domain_receipt_digest,
			domain_receipt_size_bytes, canonical_total_size_bytes,
			pre_control_snapshot_id, pre_catalog_generation_id,
			post_control_snapshot_id, post_catalog_generation_id
		FROM control_operation_receipts
		WHERE receipt_digest=?
	`, receiptDigest))
}

func scanControlOperationReceiptMetadataOptionalV1(
	scanner controlReceiptRowScannerV1,
) (controlOperationReceiptMetadataV1, bool, error) {
	var metadata controlOperationReceiptMetadataV1
	var operation, status string
	err := scanner.Scan(
		&metadata.receiptDigest,
		&metadata.tenantID,
		&metadata.principalID,
		&metadata.scopeDigest,
		&operation,
		&metadata.idempotencyKeyDigest,
		&metadata.policyRevision,
		&metadata.scopeSetDigest,
		&status,
		&metadata.requestDigest,
		&metadata.requestSize,
		&metadata.inputDigest,
		&metadata.inputSize,
		&metadata.evaluationDigest,
		&metadata.evaluationSize,
		&metadata.controlReceiptSize,
		&metadata.preBasisDigest,
		&metadata.preBasisSize,
		&metadata.postBasisDigest,
		&metadata.postBasisSize,
		&metadata.domainKind,
		&metadata.domainID,
		&metadata.domainDigest,
		&metadata.domainSize,
		&metadata.totalSize,
		&metadata.preControlID,
		&metadata.preCatalogID,
		&metadata.postControlID,
		&metadata.postCatalogID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return controlOperationReceiptMetadataV1{}, false, nil
	}
	if err != nil {
		return controlOperationReceiptMetadataV1{}, false, err
	}
	metadata.operation = controlapicontract.ControlOperationV1(operation)
	metadata.status = controlapicontract.ControlOperationStatusV1(status)
	if err := validateControlOperationReceiptMetadataV1(metadata); err != nil {
		return controlOperationReceiptMetadataV1{}, false, err
	}
	return metadata, true, nil
}

func queryControlOperationReceiptPayloadV1(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	metadata controlOperationReceiptMetadataV1,
) (controlOperationReceiptPayloadV1, error) {
	var payload controlOperationReceiptPayloadV1
	err := q.QueryRowContext(ctx, `
		SELECT request_canonical, input_canonical, evaluation_canonical,
		       control_receipt_canonical, pre_basis_canonical,
		       post_basis_canonical, domain_receipt_canonical
		FROM control_operation_receipts
		WHERE receipt_digest=?
	`, metadata.receiptDigest).Scan(
		&payload.request,
		&payload.input,
		&payload.evaluation,
		&payload.controlReceipt,
		&payload.preBasis,
		&payload.postBasis,
		&payload.domainReceipt,
	)
	if err != nil {
		return controlOperationReceiptPayloadV1{}, err
	}
	payload = cloneControlOperationReceiptPayloadV1(payload)
	if int64(len(payload.request)) != metadata.requestSize ||
		int64(len(payload.input)) != metadata.inputSize ||
		int64(len(payload.evaluation)) != metadata.evaluationSize ||
		int64(len(payload.controlReceipt)) != metadata.controlReceiptSize ||
		int64(len(payload.preBasis)) != metadata.preBasisSize ||
		int64(len(payload.postBasis)) != metadata.postBasisSize ||
		(metadata.domainSize.Valid != metadata.domainKind.Valid) ||
		(metadata.domainSize.Valid &&
			int64(len(payload.domainReceipt)) != metadata.domainSize.Int64) ||
		(!metadata.domainSize.Valid && payload.domainReceipt != nil) {
		return controlOperationReceiptPayloadV1{}, fmt.Errorf(
			"%w: canonical size projection differs",
			ErrControlOperationReceiptIntegrityV1,
		)
	}
	return payload, nil
}

func restoreStoredControlOperationReceiptV1(
	ctx context.Context,
	q interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	metadata controlOperationReceiptMetadataV1,
) (StoredControlOperationReceiptV1, error) {
	payload, err := queryControlOperationReceiptPayloadV1(ctx, q, metadata)
	if err != nil {
		return StoredControlOperationReceiptV1{}, fmt.Errorf(
			"%w: load exact canonical payload: %v",
			ErrControlOperationReceiptIntegrityV1,
			err,
		)
	}
	return restoreControlOperationReceiptFactsV1(ctx, q, metadata, payload)
}

func controlOperationReceiptIdentityFromMetadataV1(
	metadata controlOperationReceiptMetadataV1,
) ControlOperationReceiptIdentityV1 {
	return ControlOperationReceiptIdentityV1{
		PrincipalID:          metadata.principalID,
		ScopeDigest:          metadata.scopeDigest,
		Operation:            metadata.operation,
		IdempotencyKeyDigest: metadata.idempotencyKeyDigest,
	}
}

func validateControlOperationReceiptMetadataV1(
	metadata controlOperationReceiptMetadataV1,
) error {
	identity := controlOperationReceiptIdentityFromMetadataV1(metadata)
	if identity.Validate() != nil ||
		!moduleapi.ValidSHA256(metadata.receiptDigest) ||
		!validControlReceiptOpaqueIDV1(metadata.tenantID) ||
		metadata.policyRevision <= 0 ||
		uint64(metadata.policyRevision) > uint64(1<<53-1) ||
		!moduleapi.ValidSHA256(metadata.scopeSetDigest) ||
		!moduleapi.ValidSHA256(metadata.requestDigest) ||
		!moduleapi.ValidSHA256(metadata.inputDigest) ||
		!moduleapi.ValidSHA256(metadata.evaluationDigest) ||
		!moduleapi.ValidSHA256(metadata.preBasisDigest) ||
		!moduleapi.ValidSHA256(metadata.postBasisDigest) ||
		!validControlReceiptOpaqueIDV1(metadata.preControlID) ||
		!validControlReceiptOpaqueIDV1(metadata.preCatalogID) ||
		!validControlReceiptOpaqueIDV1(metadata.postControlID) ||
		!validControlReceiptOpaqueIDV1(metadata.postCatalogID) {
		return fmt.Errorf(
			"%w: invalid metadata identity",
			ErrControlOperationReceiptIntegrityV1,
		)
	}
	if !validStoredControlReceiptSizeV1(
		metadata.requestSize,
		maximumStoredControlRequestBytesV1,
	) || !validStoredControlReceiptSizeV1(
		metadata.inputSize,
		maximumStoredControlInputBytesV1,
	) || !validStoredControlReceiptSizeV1(
		metadata.evaluationSize,
		maximumStoredControlEvaluationBytesV1,
	) || !validStoredControlReceiptSizeV1(
		metadata.controlReceiptSize,
		maximumStoredControlReceiptBytesV1,
	) || !validStoredControlReceiptSizeV1(
		metadata.preBasisSize,
		maximumStoredControlBasisBytesV1,
	) || !validStoredControlReceiptSizeV1(
		metadata.postBasisSize,
		maximumStoredControlBasisBytesV1,
	) {
		return fmt.Errorf(
			"%w: canonical size is outside its ceiling",
			ErrControlOperationReceiptIntegrityV1,
		)
	}
	expectedTotal := metadata.requestSize + metadata.inputSize +
		metadata.evaluationSize + metadata.controlReceiptSize +
		metadata.preBasisSize + metadata.postBasisSize
	allDomainNull := !metadata.domainKind.Valid && !metadata.domainID.Valid &&
		!metadata.domainDigest.Valid && !metadata.domainSize.Valid
	allDomainPresent := metadata.domainKind.Valid && metadata.domainID.Valid &&
		metadata.domainDigest.Valid && metadata.domainSize.Valid
	if allDomainPresent {
		expectedTotal += metadata.domainSize.Int64
	}
	if metadata.totalSize != expectedTotal || metadata.totalSize <= 0 ||
		metadata.totalSize > maximumStoredControlCanonicalBytesV1 {
		return fmt.Errorf(
			"%w: canonical total size differs",
			ErrControlOperationReceiptIntegrityV1,
		)
	}
	switch metadata.status {
	case controlapicontract.OperationStatusNoChangeV1:
		if !allDomainNull || metadata.preBasisDigest != metadata.postBasisDigest ||
			metadata.preControlID != metadata.postControlID ||
			metadata.preCatalogID != metadata.postCatalogID {
			return fmt.Errorf(
				"%w: invalid NO_CHANGE metadata",
				ErrControlOperationReceiptIntegrityV1,
			)
		}
	case controlapicontract.OperationStatusAppliedV1:
		if !allDomainPresent ||
			metadata.domainKind.String != string(controlapicontract.DomainReceiptModuleDisableV1) ||
			!moduleapi.ValidSHA256(metadata.domainID.String) ||
			!moduleapi.ValidSHA256(metadata.domainDigest.String) ||
			!validStoredControlReceiptSizeV1(
				metadata.domainSize.Int64,
				maximumStoredControlDomainReceiptBytesV1,
			) || metadata.preBasisDigest == metadata.postBasisDigest ||
			metadata.preControlID == metadata.postControlID ||
			metadata.preCatalogID == metadata.postCatalogID {
			return fmt.Errorf(
				"%w: invalid APPLIED metadata",
				ErrControlOperationReceiptIntegrityV1,
			)
		}
	default:
		return fmt.Errorf(
			"%w: unsupported durable status %q",
			ErrControlOperationReceiptIntegrityV1,
			metadata.status,
		)
	}
	return nil
}

func validStoredControlReceiptSizeV1(size int64, maximum int) bool {
	return size > 0 && size <= int64(maximum)
}

func validControlReceiptOpaqueIDV1(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func cloneControlOperationReceiptPayloadV1(
	payload controlOperationReceiptPayloadV1,
) controlOperationReceiptPayloadV1 {
	payload.request = bytes.Clone(payload.request)
	payload.input = bytes.Clone(payload.input)
	payload.evaluation = bytes.Clone(payload.evaluation)
	payload.controlReceipt = bytes.Clone(payload.controlReceipt)
	payload.preBasis = bytes.Clone(payload.preBasis)
	payload.postBasis = bytes.Clone(payload.postBasis)
	payload.domainReceipt = bytes.Clone(payload.domainReceipt)
	return payload
}
