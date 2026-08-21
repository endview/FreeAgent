package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	channelCursorMediaType   = "application/json"
	channelEnvelopeMediaType = "application/json"

	maxChannelCursorBytes   = moduleapi.MaxChannelCursorBytesV1
	maxChannelEnvelopeBytes = moduleapi.MaxChannelMessageBytesV1 + moduleapi.MaxConfigBytes

	channelIngressKeyDigestDomainV1      = "freeagent.channel-ingress/v1"
	channelProviderEventIDDigestDomainV1 = "freeagent.channel-provider-event-id/v1"
	channelEndpointBindingDigestDomainV1 = "freeagent.channel-endpoint-binding/v1"
)

var (
	ErrInvalidChannelIngress = errors.New(
		"currentstore: invalid Channel ingress",
	)
	ErrChannelIngressNotFound = errors.New(
		"currentstore: Channel ingress not found",
	)
	ErrChannelIngressConflict = errors.New(
		"currentstore: Channel ingress conflict",
	)
	ErrChannelIngressIntegrity = errors.New(
		"currentstore: Channel ingress integrity violation",
	)
)

// ChannelIngressDisposition is the closed set of append-only cursor receipt
// kinds. A CURSOR_SEED is revision zero; all later revisions consume exactly
// one authenticated provider event.
type ChannelIngressDisposition string

const (
	ChannelCursorSeed      ChannelIngressDisposition = "CURSOR_SEED"
	ChannelIngressAccepted ChannelIngressDisposition = "ACCEPTED"
	ChannelIngressRejected ChannelIngressDisposition = "REJECTED"
)

// ChannelIngressReceipt is one verified append-only cursor revision. Empty
// optional strings represent SQL NULL; Created is an API result projection
// and is never persisted.
type ChannelIngressReceipt struct {
	TenantID              string
	WorkspaceID           string
	EndpointID            string
	CursorScopeKey        string
	CursorRevision        uint64
	CursorBeforeRef       string
	CursorAfterRef        string
	EndpointBindingDigest string
	Disposition           ChannelIngressDisposition
	Reason                string
	IngressKey            string
	ProviderEventIDDigest string
	EnvelopeRef           string
	PrincipalID           string
	ACLEpoch              uint64
	AdmissionKey          string
	RunID                 string
	CreatedAt             time.Time
	Created               bool
}

// ChannelCursorSeedInput initializes or explicitly imports revision zero.
// EndpointDisabled is a mandatory caller assertion used only by the explicit
// operator path; normal ingress cannot call this API.
type ChannelCursorSeedInput struct {
	PublishedBasis        controlcontract.PublishedBasis
	TenantID              string
	WorkspaceID           string
	EndpointID            string
	CursorScopeKey        string
	EndpointBindingDigest string
	CursorAfter           ContentInput
	Reason                string
	EndpointDisabled      bool
}

// ChannelIngressEventInput is the common immutable closure for an ACCEPTED
// or REJECTED provider event. CursorBeforeRef must name the previous row's
// exact CursorAfterRef; CursorAfter and Envelope are inserted in the same
// transaction as the receipt.
type ChannelIngressEventInput struct {
	PublishedBasis         controlcontract.PublishedBasis
	TenantID               string
	WorkspaceID            string
	EndpointID             string
	CursorScopeKey         string
	ExpectedCursorRevision uint64
	CursorBeforeRef        string
	CursorAfter            ContentInput
	EndpointBindingDigest  string
	IngressKey             string
	ProviderEventIDDigest  string
	Envelope               ContentInput
	Reason                 string
}

// CommitChannelIngressAdmissionInput binds one accepted ingress revision to
// the existing immutable Run Admission closure. Principal and ACL epoch are
// Core authorization results, not adapter-selected fields.
type CommitChannelIngressAdmissionInput struct {
	Ingress     ChannelIngressEventInput
	PrincipalID string
	ACLEpoch    uint64
	Admission   CommitRunAdmissionInput
}

// ChannelIngressAdmissionResult contains the two facts committed atomically.
// Created is true only when this call inserted both facts.
type ChannelIngressAdmissionResult struct {
	Receipt   ChannelIngressReceipt
	Admission RunAdmissionResult
	Created   bool
}

type preparedChannelIngressEvent struct {
	input        ChannelIngressEventInput
	cursorAfter  preparedAdmissionContent
	envelope     preparedAdmissionContent
	envelopeWire moduleapi.ChannelInboundEnvelopeV1
}

// SeedChannelCursor commits revision zero without creating a mutable cursor
// head. An exact retry returns the original row with Created=false.
func (store *Store) SeedChannelCursor(
	ctx context.Context,
	input ChannelCursorSeedInput,
) (ChannelIngressReceipt, error) {
	if ctx == nil {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidChannelIngress,
		)
	}
	preparedCursor, err := prepareChannelContent(
		input.CursorAfter,
		ContentChannelCursor,
		channelCursorMediaType,
		maxChannelCursorBytes,
		true,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	if !input.EndpointDisabled {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: cursor seed/import requires a disabled Endpoint",
			ErrInvalidChannelIngress,
		)
	}
	if err := validateChannelBasisAndIdentity(
		input.PublishedBasis,
		input.TenantID,
		input.WorkspaceID,
		input.EndpointID,
		input.CursorScopeKey,
	); err != nil {
		return ChannelIngressReceipt{}, err
	}
	if !moduleapi.ValidSHA256(input.EndpointBindingDigest) {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: invalid Endpoint Binding digest",
			ErrInvalidChannelIngress,
		)
	}
	if err := validateChannelReason(input.Reason); err != nil {
		return ChannelIngressReceipt{}, err
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	defer unlock()
	connection, err := beginChannelIngressTransaction(ctx, store.db, "seed cursor")
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	defer connection.Close()
	committed := false
	defer rollbackChannelIngress(connection, &committed)

	existing, found, err := queryCurrentChannelReceipt(
		ctx,
		connection,
		input.TenantID,
		input.EndpointID,
		input.CursorScopeKey,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	if found {
		if err := verifyChannelReceipt(ctx, connection, existing); err != nil {
			return ChannelIngressReceipt{}, err
		}
		if existing.CursorRevision != 0 ||
			existing.Disposition != ChannelCursorSeed ||
			existing.WorkspaceID != input.WorkspaceID ||
			existing.EndpointBindingDigest != input.EndpointBindingDigest ||
			existing.CursorAfterRef != preparedCursor.Digest ||
			existing.Reason != input.Reason {
			return ChannelIngressReceipt{}, fmt.Errorf(
				"%w: cursor scope already has another seed or later revision",
				ErrChannelIngressConflict,
			)
		}
		if err := commitChannelIngressTransaction(ctx, connection, "idempotent cursor seed"); err != nil {
			return ChannelIngressReceipt{}, err
		}
		committed = true
		existing.Created = false
		return existing, nil
	}
	control, catalog, err := verifyCurrentAdmissionBasis(
		ctx,
		connection,
		input.PublishedBasis,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	seedClosure := preparedChannelIngressEvent{
		input: ChannelIngressEventInput{
			PublishedBasis:        input.PublishedBasis,
			TenantID:              input.TenantID,
			WorkspaceID:           input.WorkspaceID,
			EndpointID:            input.EndpointID,
			CursorScopeKey:        input.CursorScopeKey,
			EndpointBindingDigest: input.EndpointBindingDigest,
		},
	}
	if _, _, _, err := verifyChannelEndpointAndAuthority(
		ctx,
		connection,
		control,
		catalog,
		seedClosure,
		false,
	); err != nil {
		return ChannelIngressReceipt{}, err
	}
	createdAt := nowUnixMicro()
	if createdAt <= 0 {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: invalid cursor seed time",
			ErrChannelIngressIntegrity,
		)
	}
	if err := putAdmissionContent(ctx, connection, preparedCursor, createdAt); err != nil {
		return ChannelIngressReceipt{}, err
	}
	receipt := ChannelIngressReceipt{
		TenantID:              input.TenantID,
		WorkspaceID:           input.WorkspaceID,
		EndpointID:            input.EndpointID,
		CursorScopeKey:        input.CursorScopeKey,
		CursorRevision:        0,
		CursorAfterRef:        preparedCursor.Digest,
		EndpointBindingDigest: input.EndpointBindingDigest,
		Disposition:           ChannelCursorSeed,
		Reason:                input.Reason,
	}
	if err := insertChannelReceipt(ctx, connection, receipt, createdAt); err != nil {
		return ChannelIngressReceipt{}, err
	}
	stored, err := loadChannelReceiptByRevision(
		ctx,
		connection,
		input.TenantID,
		input.EndpointID,
		input.CursorScopeKey,
		0,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	if err := commitChannelIngressTransaction(ctx, connection, "cursor seed"); err != nil {
		return ChannelIngressReceipt{}, err
	}
	committed = true
	stored.Created = true
	return stored, nil
}

// ResolveChannelIngress performs the pre-compile ingress dedupe lookup. A
// same key with another envelope digest is an integrity conflict.
func (store *Store) ResolveChannelIngress(
	ctx context.Context,
	tenantID string,
	endpointID string,
	ingressKey string,
	envelopeDigest string,
) (ChannelIngressReceipt, bool, error) {
	if ctx == nil {
		return ChannelIngressReceipt{}, false, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidChannelIngress,
		)
	}
	if !validChannelOpaqueID(tenantID) ||
		!validChannelOpaqueID(endpointID) ||
		!moduleapi.ValidSHA256(ingressKey) ||
		!moduleapi.ValidSHA256(envelopeDigest) {
		return ChannelIngressReceipt{}, false, fmt.Errorf(
			"%w: invalid ingress lookup identity",
			ErrInvalidChannelIngress,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ChannelIngressReceipt{}, false, err
	}
	defer unlock()
	receipt, found, err := queryChannelReceiptByIngressKey(
		ctx,
		store.db,
		tenantID,
		endpointID,
		ingressKey,
	)
	if err != nil || !found {
		return ChannelIngressReceipt{}, found, err
	}
	if err := verifyChannelReceipt(ctx, store.db, receipt); err != nil {
		return ChannelIngressReceipt{}, true, err
	}
	if receipt.EnvelopeRef != envelopeDigest {
		return ChannelIngressReceipt{}, true, fmt.Errorf(
			"%w: ingress key already names another envelope",
			ErrChannelIngressConflict,
		)
	}
	return receipt, true, nil
}

// GetCurrentChannelCursor derives the current cursor from MAX(revision). It
// never reads or updates a mutable head row.
func (store *Store) GetCurrentChannelCursor(
	ctx context.Context,
	tenantID string,
	endpointID string,
	cursorScopeKey string,
) (ChannelIngressReceipt, error) {
	if ctx == nil {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidChannelIngress,
		)
	}
	if !validChannelOpaqueID(tenantID) ||
		!validChannelOpaqueID(endpointID) ||
		!validChannelOpaqueID(cursorScopeKey) {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: invalid cursor identity",
			ErrInvalidChannelIngress,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	defer unlock()
	receipt, found, err := queryCurrentChannelReceipt(
		ctx,
		store.db,
		tenantID,
		endpointID,
		cursorScopeKey,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	if !found {
		return ChannelIngressReceipt{}, ErrChannelIngressNotFound
	}
	if err := verifyChannelReceipt(ctx, store.db, receipt); err != nil {
		return ChannelIngressReceipt{}, err
	}
	return receipt, nil
}

// GetChannelCursorSeed reads and verifies the fixed revision-zero receipt for
// one exact cursor identity. It deliberately does not resolve MAX(revision), so
// an Apply retry can still prove the original seed after the cursor advances.
func (store *Store) GetChannelCursorSeed(
	ctx context.Context,
	tenantID string,
	endpointID string,
	cursorScopeKey string,
) (ChannelIngressReceipt, error) {
	if ctx == nil {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidChannelIngress,
		)
	}
	if !validChannelOpaqueID(tenantID) ||
		!validChannelOpaqueID(endpointID) ||
		!validChannelOpaqueID(cursorScopeKey) {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: invalid cursor seed identity",
			ErrInvalidChannelIngress,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	defer unlock()
	receipt, err := loadChannelReceiptByRevision(
		ctx,
		store.db,
		tenantID,
		endpointID,
		cursorScopeKey,
		0,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	if receipt.Disposition != ChannelCursorSeed || receipt.CursorRevision != 0 {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: revision zero is not a CURSOR_SEED",
			ErrChannelIngressIntegrity,
		)
	}
	return receipt, nil
}

// CommitRejectedChannelIngress appends a bounded, authenticated poison-message
// receipt and advances the cursor only after current PublishedBasis, an exact
// identity-denial proof, and cursor CAS pass. The canonical envelope is kept
// for deterministic deduplication and audit; this is not a hash-only path.
func (store *Store) CommitRejectedChannelIngress(
	ctx context.Context,
	input ChannelIngressEventInput,
) (ChannelIngressReceipt, error) {
	if ctx == nil {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidChannelIngress,
		)
	}
	prepared, err := prepareChannelIngressEvent(input)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	defer unlock()
	connection, err := beginChannelIngressTransaction(ctx, store.db, "reject ingress")
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	defer connection.Close()
	committed := false
	defer rollbackChannelIngress(connection, &committed)

	existing, found, err := resolvePreparedChannelIngress(
		ctx,
		connection,
		prepared,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	if found {
		if existing.Disposition != ChannelIngressRejected {
			return ChannelIngressReceipt{}, fmt.Errorf(
				"%w: event already has disposition %s",
				ErrChannelIngressConflict,
				existing.Disposition,
			)
		}
		if err := commitChannelIngressTransaction(ctx, connection, "idempotent rejected ingress"); err != nil {
			return ChannelIngressReceipt{}, err
		}
		committed = true
		existing.Created = false
		return existing, nil
	}
	control, catalog, err := verifyCurrentAdmissionBasis(
		ctx,
		connection,
		prepared.input.PublishedBasis,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	workspace, endpoint, _, err := verifyChannelEndpointAndAuthority(
		ctx,
		connection,
		control,
		catalog,
		prepared,
		true,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	if err := verifyRejectedChannelIdentity(
		workspace,
		endpoint,
		prepared.envelopeWire,
	); err != nil {
		return ChannelIngressReceipt{}, err
	}
	current, err := requireChannelCursorCAS(ctx, connection, prepared.input)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	createdAt := nowUnixMicro()
	if createdAt <= 0 {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: invalid rejected ingress time",
			ErrChannelIngressIntegrity,
		)
	}
	if err := putPreparedChannelEvent(ctx, connection, prepared, createdAt); err != nil {
		return ChannelIngressReceipt{}, err
	}
	receipt := newChannelEventReceipt(
		prepared,
		current.CursorRevision+1,
		ChannelIngressRejected,
	)
	if err := insertChannelReceipt(ctx, connection, receipt, createdAt); err != nil {
		return ChannelIngressReceipt{}, err
	}
	stored, err := loadChannelReceiptByRevision(
		ctx,
		connection,
		receipt.TenantID,
		receipt.EndpointID,
		receipt.CursorScopeKey,
		receipt.CursorRevision,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	if err := commitChannelIngressTransaction(ctx, connection, "rejected ingress"); err != nil {
		return ChannelIngressReceipt{}, err
	}
	committed = true
	stored.Created = true
	return stored, nil
}

func verifyRejectedChannelIdentity(
	workspace controlcontract.WorkspaceDefinition,
	endpoint controlcontract.ChannelEndpointDefinition,
	envelope moduleapi.ChannelInboundEnvelopeV1,
) error {
	for _, identity := range workspace.ChannelIdentities {
		if identity.Channel == endpoint.Channel &&
			identity.AccountID == endpoint.AccountID &&
			identity.ExternalUserID == envelope.ExternalUserID &&
			identity.Active {
			return fmt.Errorf(
				"%w: active authenticated identity cannot be committed as REJECTED",
				ErrChannelIngressConflict,
			)
		}
	}
	return nil
}

// CommitChannelIngressAndRunAdmission inserts an ACCEPTED cursor receipt and
// the existing Run Admission closure in one BEGIN IMMEDIATE transaction.
func (store *Store) CommitChannelIngressAndRunAdmission(
	ctx context.Context,
	input CommitChannelIngressAdmissionInput,
) (ChannelIngressAdmissionResult, error) {
	if ctx == nil {
		return ChannelIngressAdmissionResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidChannelIngress,
		)
	}
	preparedIngress, err := prepareChannelIngressEvent(input.Ingress)
	if err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	if !validChannelOpaqueID(input.PrincipalID) ||
		input.ACLEpoch == 0 || input.ACLEpoch > math.MaxInt64 {
		return ChannelIngressAdmissionResult{}, fmt.Errorf(
			"%w: invalid accepted Principal or ACL epoch",
			ErrInvalidChannelIngress,
		)
	}
	preparedAdmission, err := prepareCompleteRunAdmission(input.Admission)
	if err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	if preparedAdmission.manifest.Composite != nil {
		return ChannelIngressAdmissionResult{}, fmt.Errorf(
			"%w: Channel ingress cannot admit a composite Run family",
			ErrInvalidChannelIngress,
		)
	}
	if preparedAdmission.manifest.ConversationTurn != nil ||
		preparedAdmission.intent.ConversationTurn != nil {
		return ChannelIngressAdmissionResult{}, fmt.Errorf(
			"%w: Channel ingress cannot admit a Conversation Run",
			ErrInvalidChannelIngress,
		)
	}
	if preparedAdmission.input.PublishedBasis != preparedIngress.input.PublishedBasis ||
		preparedAdmission.intent.TenantID != preparedIngress.input.TenantID ||
		preparedAdmission.intent.WorkspaceID != preparedIngress.input.WorkspaceID ||
		preparedAdmission.intent.ChannelEndpointID != preparedIngress.input.EndpointID ||
		preparedAdmission.intent.PrincipalID != input.PrincipalID ||
		preparedAdmission.intent.AdmissionKey != channelAdmissionKey(preparedIngress.input.IngressKey) {
		return ChannelIngressAdmissionResult{}, fmt.Errorf(
			"%w: ingress and Admission do not form one identity closure",
			ErrInvalidChannelIngress,
		)
	}

	unlock, err := store.lockOpen()
	if err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	defer unlock()
	connection, err := beginChannelIngressTransaction(ctx, store.db, "accept ingress")
	if err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	defer connection.Close()
	committed := false
	defer rollbackChannelIngress(connection, &committed)

	existing, found, err := resolvePreparedChannelIngress(
		ctx,
		connection,
		preparedIngress,
	)
	if err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	if found {
		if existing.Disposition != ChannelIngressAccepted {
			return ChannelIngressAdmissionResult{}, fmt.Errorf(
				"%w: event already has disposition %s",
				ErrChannelIngressConflict,
				existing.Disposition,
			)
		}
		admission, err := loadChannelReceiptAdmission(ctx, connection, existing)
		if err != nil {
			return ChannelIngressAdmissionResult{}, err
		}
		if err := verifyCurrentRunObservationV1(ctx, connection, admission.RunID); err != nil {
			return ChannelIngressAdmissionResult{}, err
		}
		if err := commitChannelIngressTransaction(ctx, connection, "idempotent accepted ingress"); err != nil {
			return ChannelIngressAdmissionResult{}, err
		}
		committed = true
		existing.Created = false
		return ChannelIngressAdmissionResult{
			Receipt:   existing,
			Admission: admission,
			Created:   false,
		}, nil
	}
	if _, found, err := resolveAdmissionWithQueryer(
		ctx,
		connection,
		preparedAdmission.intent.TenantID,
		preparedAdmission.intent.AdmissionKey,
		preparedAdmission.input.IntentDigest,
	); err != nil {
		return ChannelIngressAdmissionResult{}, err
	} else if found {
		return ChannelIngressAdmissionResult{}, fmt.Errorf(
			"%w: Channel Admission exists without its ingress receipt",
			ErrChannelIngressIntegrity,
		)
	}
	control, catalog, err := verifyCurrentAdmissionBasis(
		ctx,
		connection,
		preparedIngress.input.PublishedBasis,
	)
	if err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	workspace, endpoint, binding, err := verifyChannelEndpointAndAuthority(
		ctx,
		connection,
		control,
		catalog,
		preparedIngress,
		true,
	)
	if err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	if err := verifyAcceptedChannelAuthorization(
		workspace,
		endpoint,
		binding,
		preparedIngress,
		preparedAdmission,
		input.PrincipalID,
		input.ACLEpoch,
	); err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	current, err := requireChannelCursorCAS(
		ctx,
		connection,
		preparedIngress.input,
	)
	if err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	createdAt := nowUnixMicro()
	if createdAt <= 0 {
		return ChannelIngressAdmissionResult{}, fmt.Errorf(
			"%w: invalid accepted ingress time",
			ErrChannelIngressIntegrity,
		)
	}
	if err := putPreparedChannelEvent(
		ctx,
		connection,
		preparedIngress,
		createdAt,
	); err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	admission, err := publishPreparedRunAdmission(
		ctx,
		connection,
		preparedAdmission,
		createdAt,
	)
	if err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	receipt := newChannelEventReceipt(
		preparedIngress,
		current.CursorRevision+1,
		ChannelIngressAccepted,
	)
	receipt.PrincipalID = input.PrincipalID
	receipt.ACLEpoch = input.ACLEpoch
	receipt.AdmissionKey = preparedAdmission.intent.AdmissionKey
	receipt.RunID = preparedAdmission.manifest.RunID
	if err := insertChannelReceipt(ctx, connection, receipt, createdAt); err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	stored, err := loadChannelReceiptByRevision(
		ctx,
		connection,
		receipt.TenantID,
		receipt.EndpointID,
		receipt.CursorScopeKey,
		receipt.CursorRevision,
	)
	if err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	if err := commitChannelIngressTransaction(ctx, connection, "accepted ingress"); err != nil {
		return ChannelIngressAdmissionResult{}, err
	}
	committed = true
	stored.Created = true
	admission.Created = true
	return ChannelIngressAdmissionResult{
		Receipt:   stored,
		Admission: admission,
		Created:   true,
	}, nil
}

func prepareChannelIngressEvent(
	input ChannelIngressEventInput,
) (preparedChannelIngressEvent, error) {
	if err := validateChannelBasisAndIdentity(
		input.PublishedBasis,
		input.TenantID,
		input.WorkspaceID,
		input.EndpointID,
		input.CursorScopeKey,
	); err != nil {
		return preparedChannelIngressEvent{}, err
	}
	if input.ExpectedCursorRevision > math.MaxInt64-1 ||
		!moduleapi.ValidSHA256(input.CursorBeforeRef) ||
		!moduleapi.ValidSHA256(input.EndpointBindingDigest) {
		return preparedChannelIngressEvent{}, fmt.Errorf(
			"%w: invalid event revision, ref, or digest",
			ErrInvalidChannelIngress,
		)
	}
	if err := validateChannelReason(input.Reason); err != nil {
		return preparedChannelIngressEvent{}, err
	}
	cursorAfter, err := prepareChannelContent(
		input.CursorAfter,
		ContentChannelCursor,
		channelCursorMediaType,
		maxChannelCursorBytes,
		true,
	)
	if err != nil {
		return preparedChannelIngressEvent{}, err
	}
	envelope, err := prepareChannelContent(
		input.Envelope,
		ContentChannelIngressEnvelope,
		channelEnvelopeMediaType,
		maxChannelEnvelopeBytes,
		false,
	)
	if err != nil {
		return preparedChannelIngressEvent{}, err
	}
	envelopeWire, err := moduleapi.RestoreChannelInboundEnvelopeV1(
		envelope.CanonicalBytes,
	)
	if err != nil || envelopeWire.EndpointID != input.EndpointID ||
		!bytes.Equal(envelopeWire.CursorAfter, cursorAfter.CanonicalBytes) {
		return preparedChannelIngressEvent{}, fmt.Errorf(
			"%w: ingress Envelope, Endpoint, and CursorAfter do not close: %v",
			ErrInvalidChannelIngress,
			err,
		)
	}
	wantIngressKey, wantProviderEventDigest, err := ComputeChannelIngressIdentity(
		input.TenantID,
		input.EndpointID,
		envelopeWire.ProviderEventID,
	)
	if err != nil || input.IngressKey != wantIngressKey ||
		input.ProviderEventIDDigest != wantProviderEventDigest {
		return preparedChannelIngressEvent{}, fmt.Errorf(
			"%w: ingress key or Provider Event ID digest is not derived from the Envelope",
			ErrInvalidChannelIngress,
		)
	}
	input.CursorAfter.CanonicalBytes = bytes.Clone(input.CursorAfter.CanonicalBytes)
	input.Envelope.CanonicalBytes = bytes.Clone(input.Envelope.CanonicalBytes)
	return preparedChannelIngressEvent{
		input:        input,
		cursorAfter:  cursorAfter,
		envelope:     envelope,
		envelopeWire: envelopeWire,
	}, nil
}

// ComputeChannelIngressIdentity derives the two stable event identities from
// authenticated provider data. Generated Run IDs, cursor, binding and current
// Control/Catalog revisions are deliberately absent.
func ComputeChannelIngressIdentity(
	tenantID string,
	endpointID string,
	providerEventID string,
) (string, string, error) {
	if !validChannelOpaqueID(tenantID) ||
		!validChannelOpaqueID(endpointID) ||
		!validChannelOpaqueID(providerEventID) {
		return "", "", fmt.Errorf(
			"%w: invalid ingress identity component",
			ErrInvalidChannelIngress,
		)
	}
	identityJSON, err := json.Marshal(struct {
		TenantID        string `json:"tenant_id"`
		EndpointID      string `json:"endpoint_id"`
		ProviderEventID string `json:"provider_event_id"`
	}{
		TenantID:        tenantID,
		EndpointID:      endpointID,
		ProviderEventID: providerEventID,
	})
	if err != nil {
		return "", "", fmt.Errorf(
			"%w: encode ingress identity: %v",
			ErrInvalidChannelIngress,
			err,
		)
	}
	canonical, err := moduleapi.CanonicalJSON(identityJSON)
	if err != nil {
		return "", "", fmt.Errorf(
			"%w: canonicalize ingress identity: %v",
			ErrInvalidChannelIngress,
			err,
		)
	}
	return moduleapi.Digest(channelIngressKeyDigestDomainV1, canonical),
		moduleapi.Digest(
			channelProviderEventIDDigestDomainV1,
			[]byte(providerEventID),
		), nil
}

func prepareChannelContent(
	input ContentInput,
	wantKind ContentKind,
	wantMediaType string,
	maxBytes int,
	allowEmpty bool,
) (preparedAdmissionContent, error) {
	body := bytes.Clone(input.CanonicalBytes)
	if input.Kind != wantKind || input.MediaType != wantMediaType ||
		len(body) > maxBytes || (!allowEmpty && len(body) == 0) {
		return preparedAdmissionContent{}, fmt.Errorf(
			"%w: invalid %s content closure",
			ErrInvalidChannelIngress,
			wantKind,
		)
	}
	digest, err := ComputeContentDigest(input.Kind, input.MediaType, body)
	if err != nil || digest != input.Digest {
		return preparedAdmissionContent{}, fmt.Errorf(
			"%w: %s content does not match its digest: %v",
			ErrInvalidChannelIngress,
			wantKind,
			err,
		)
	}
	return preparedAdmissionContent{
		Digest:         digest,
		Kind:           wantKind,
		MediaType:      wantMediaType,
		CanonicalBytes: body,
	}, nil
}

func validateChannelBasisAndIdentity(
	basis controlcontract.PublishedBasis,
	tenantID string,
	workspaceID string,
	endpointID string,
	cursorScopeKey string,
) error {
	if err := basis.Validate(); err != nil || basis.TenantID != tenantID ||
		basis.PointerRevision > math.MaxInt64 ||
		basis.Control.Revision > math.MaxInt64 ||
		basis.Catalog.Generation > math.MaxInt64 {
		return fmt.Errorf(
			"%w: invalid or mismatched PublishedBasis",
			ErrInvalidChannelIngress,
		)
	}
	for name, value := range map[string]string{
		"tenant ID":        tenantID,
		"workspace ID":     workspaceID,
		"Endpoint ID":      endpointID,
		"cursor scope key": cursorScopeKey,
	} {
		if !validChannelOpaqueID(value) {
			return fmt.Errorf(
				"%w: invalid %s",
				ErrInvalidChannelIngress,
				name,
			)
		}
	}
	return nil
}

func validateChannelReason(reason string) error {
	if !validChannelOpaqueID(reason) {
		return fmt.Errorf(
			"%w: invalid disposition reason",
			ErrInvalidChannelIngress,
		)
	}
	return nil
}

func validChannelOpaqueID(value string) bool {
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

func channelAdmissionKey(ingressKey string) string {
	return "channel/v1/" + ingressKey
}

func resolveChannelEndpointBinding(
	endpoint controlcontract.ChannelEndpointDefinition,
	catalog controlcontract.CatalogGeneration,
) (moduleapi.PortBinding, error) {
	entry, found := catalog.FindInstance(endpoint.Binding.InstanceID)
	if !found || !admissionEntryProvides(entry, endpoint.Binding.Port) {
		return moduleapi.PortBinding{}, fmt.Errorf(
			"%w: Channel Endpoint binding is absent from the frozen Catalog",
			ErrChannelIngressIntegrity,
		)
	}
	binding := moduleapi.PortBinding{
		Provider:            entry.Activation,
		ConfigRef:           endpoint.Binding.ConfigRef,
		AuthorityCeilingRef: endpoint.Binding.AuthorityCeilingRef,
		StaticContextRefs: append(
			[]string(nil),
			endpoint.Binding.StaticContextRefs...,
		),
		FailurePolicy: endpoint.Binding.FailurePolicy,
	}
	if _, _, err := moduleapi.CanonicalChannelEndpointBindingV1(binding); err != nil {
		return moduleapi.PortBinding{}, fmt.Errorf(
			"%w: invalid resolved Channel Endpoint binding: %v",
			ErrChannelIngressIntegrity,
			err,
		)
	}
	return binding, nil
}

func verifyChannelEndpointAndAuthority(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	prepared preparedChannelIngressEvent,
	requireEnabled bool,
) (
	controlcontract.WorkspaceDefinition,
	controlcontract.ChannelEndpointDefinition,
	moduleapi.PortBinding,
	error,
) {
	workspace, found := control.FindWorkspace(prepared.input.WorkspaceID)
	if !found {
		return controlcontract.WorkspaceDefinition{},
			controlcontract.ChannelEndpointDefinition{},
			moduleapi.PortBinding{}, fmt.Errorf(
				"%w: Channel Workspace is absent from current Control",
				ErrChannelIngressConflict,
			)
	}
	endpoint, found := workspace.FindChannelEndpoint(prepared.input.EndpointID)
	if !found || endpoint.CursorScopeKey != prepared.input.CursorScopeKey ||
		endpoint.Enabled != requireEnabled {
		return controlcontract.WorkspaceDefinition{},
			controlcontract.ChannelEndpointDefinition{},
			moduleapi.PortBinding{}, fmt.Errorf(
				"%w: Channel Endpoint state or cursor scope changed",
				ErrChannelIngressConflict,
			)
	}
	binding, err := resolveChannelEndpointBinding(endpoint, catalog)
	if err != nil {
		return controlcontract.WorkspaceDefinition{},
			controlcontract.ChannelEndpointDefinition{},
			moduleapi.PortBinding{}, err
	}
	wantBindingDigest, err :=
		moduleapi.ComputeChannelEndpointBindingDigestV1(binding)
	if err != nil || wantBindingDigest != prepared.input.EndpointBindingDigest {
		return controlcontract.WorkspaceDefinition{},
			controlcontract.ChannelEndpointDefinition{},
			moduleapi.PortBinding{}, fmt.Errorf(
				"%w: Endpoint Binding digest changed",
				ErrChannelIngressConflict,
			)
	}
	config, err := queryContent(ctx, queryer, binding.ConfigRef)
	if err != nil || config.Kind != ContentConfig ||
		config.MediaType != admissionJSONMediaType {
		return controlcontract.WorkspaceDefinition{},
			controlcontract.ChannelEndpointDefinition{},
			moduleapi.PortBinding{}, fmt.Errorf(
				"%w: Channel Config closure is unavailable",
				ErrChannelIngressIntegrity,
			)
	}
	if _, err := moduleapi.RestoreChannelBindingConfigV1(
		config.CanonicalBytes,
	); err != nil {
		return controlcontract.WorkspaceDefinition{},
			controlcontract.ChannelEndpointDefinition{},
			moduleapi.PortBinding{}, fmt.Errorf(
				"%w: invalid Channel Config: %v",
				ErrChannelIngressIntegrity,
				err,
			)
	}
	authorityRecord, err := queryContent(
		ctx,
		queryer,
		binding.AuthorityCeilingRef,
	)
	if err != nil || authorityRecord.Kind != ContentAuthorityCeiling ||
		authorityRecord.MediaType != admissionJSONMediaType {
		return controlcontract.WorkspaceDefinition{},
			controlcontract.ChannelEndpointDefinition{},
			moduleapi.PortBinding{}, fmt.Errorf(
				"%w: Channel Authority closure is unavailable",
				ErrChannelIngressIntegrity,
			)
	}
	authority, err := moduleapi.RestoreChannelAuthorityCeilingV1(
		authorityRecord.CanonicalBytes,
	)
	if err != nil || authority.TenantID != prepared.input.TenantID ||
		!authority.AllowReceive ||
		!containsChannelString(authority.AllowedWorkspaceIDs, prepared.input.WorkspaceID) ||
		!containsChannelString(authority.AllowedEndpointIDs, prepared.input.EndpointID) ||
		uint64(len([]byte(prepared.envelopeWire.Message))) >
			uint64(authority.MaxMessageBytes) {
		return controlcontract.WorkspaceDefinition{},
			controlcontract.ChannelEndpointDefinition{},
			moduleapi.PortBinding{}, fmt.Errorf(
				"%w: channel.receive is not granted by exact Authority",
				ErrChannelIngressConflict,
			)
	}
	return workspace, endpoint, binding, nil
}

func verifyAcceptedChannelAuthorization(
	workspace controlcontract.WorkspaceDefinition,
	endpoint controlcontract.ChannelEndpointDefinition,
	binding moduleapi.PortBinding,
	preparedIngress preparedChannelIngressEvent,
	preparedAdmission preparedRunAdmission,
	principalID string,
	aclEpoch uint64,
) error {
	if preparedAdmission.intent.ChannelEndpointID != endpoint.EndpointID ||
		preparedAdmission.intent.PrincipalID != principalID ||
		preparedAdmission.intent.AgentID != endpoint.TargetAgentID ||
		preparedAdmission.intent.ProfileID != endpoint.TargetProfileID {
		return fmt.Errorf(
			"%w: Channel Endpoint target and Admission identity differ",
			ErrInvalidChannelIngress,
		)
	}
	identityFound := false
	for _, identity := range workspace.ChannelIdentities {
		if identity.Channel == endpoint.Channel &&
			identity.AccountID == endpoint.AccountID &&
			identity.ExternalUserID == preparedIngress.envelopeWire.ExternalUserID {
			if !identity.Active || identity.PrincipalID != principalID ||
				identity.ACLEpoch != aclEpoch {
				return fmt.Errorf(
					"%w: Channel identity is inactive or ACL changed",
					ErrChannelIngressConflict,
				)
			}
			identityFound = true
			break
		}
	}
	if !identityFound {
		return fmt.Errorf(
			"%w: authenticated external identity is not authorized",
			ErrChannelIngressConflict,
		)
	}
	channelPlanFound := false
	for _, plan := range preparedAdmission.member.PortPlans {
		if plan.Port != endpoint.Binding.Port || len(plan.Bindings) != 1 {
			continue
		}
		want, _, wantErr :=
			moduleapi.CanonicalChannelEndpointBindingV1(binding)
		got, _, gotErr :=
			moduleapi.CanonicalChannelEndpointBindingV1(plan.Bindings[0])
		if wantErr == nil && gotErr == nil && bytes.Equal(want, got) {
			channelPlanFound = true
			break
		}
	}
	if !channelPlanFound {
		return fmt.Errorf(
			"%w: exact Endpoint Binding is absent from MemberSnapshot",
			ErrChannelIngressIntegrity,
		)
	}
	return nil
}

func containsChannelString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func beginChannelIngressTransaction(
	ctx context.Context,
	db *sql.DB,
	operation string,
) (*sql.Conn, error) {
	connection, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: acquire Channel %s connection: %w",
			operation,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf(
			"currentstore: begin Channel %s: %w",
			operation,
			err,
		)
	}
	return connection, nil
}

func rollbackChannelIngress(connection *sql.Conn, committed *bool) {
	if connection != nil && committed != nil && !*committed {
		_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
	}
}

func commitChannelIngressTransaction(
	ctx context.Context,
	connection *sql.Conn,
	operation string,
) error {
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf(
			"currentstore: commit Channel %s: %w",
			operation,
			err,
		)
	}
	return nil
}

func resolvePreparedChannelIngress(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	prepared preparedChannelIngressEvent,
) (ChannelIngressReceipt, bool, error) {
	receipt, found, err := queryChannelReceiptByIngressKey(
		ctx,
		queryer,
		prepared.input.TenantID,
		prepared.input.EndpointID,
		prepared.input.IngressKey,
	)
	if err != nil {
		return ChannelIngressReceipt{}, false, err
	}
	if found {
		if err := verifyChannelReceipt(ctx, queryer, receipt); err != nil {
			return ChannelIngressReceipt{}, true, err
		}
		if receipt.EnvelopeRef != prepared.envelope.Digest ||
			receipt.ProviderEventIDDigest != prepared.input.ProviderEventIDDigest {
			return ChannelIngressReceipt{}, true, fmt.Errorf(
				"%w: ingress key already names another event or envelope",
				ErrChannelIngressConflict,
			)
		}
		return receipt, true, nil
	}
	byEvent, eventFound, err := queryChannelReceiptByProviderEvent(
		ctx,
		queryer,
		prepared.input.TenantID,
		prepared.input.EndpointID,
		prepared.input.ProviderEventIDDigest,
	)
	if err != nil {
		return ChannelIngressReceipt{}, false, err
	}
	if eventFound {
		if err := verifyChannelReceipt(ctx, queryer, byEvent); err != nil {
			return ChannelIngressReceipt{}, false, err
		}
		return ChannelIngressReceipt{}, false, fmt.Errorf(
			"%w: provider event already has another ingress key",
			ErrChannelIngressConflict,
		)
	}
	return ChannelIngressReceipt{}, false, nil
}

func requireChannelCursorCAS(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	input ChannelIngressEventInput,
) (ChannelIngressReceipt, error) {
	current, found, err := queryCurrentChannelReceipt(
		ctx,
		queryer,
		input.TenantID,
		input.EndpointID,
		input.CursorScopeKey,
	)
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	if !found {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: cursor scope has no explicit revision-zero seed",
			ErrChannelIngressConflict,
		)
	}
	if err := verifyChannelReceipt(ctx, queryer, current); err != nil {
		return ChannelIngressReceipt{}, err
	}
	if current.WorkspaceID != input.WorkspaceID ||
		current.CursorRevision != input.ExpectedCursorRevision ||
		current.CursorAfterRef != input.CursorBeforeRef {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: stale cursor, broken chain, or Workspace fork",
			ErrChannelIngressConflict,
		)
	}
	before, err := queryContent(ctx, queryer, input.CursorBeforeRef)
	if err != nil || before.Kind != ContentChannelCursor ||
		before.MediaType != channelCursorMediaType {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: current CursorBefore content is unavailable",
			ErrChannelIngressIntegrity,
		)
	}
	// CursorBefore is part of the authenticated adapter envelope. Matching
	// only its supplied digest would let a caller detach cursor CAS from the
	// exact wire it claims to have authenticated.
	wire, err := moduleapi.RestoreChannelInboundEnvelopeV1(
		input.Envelope.CanonicalBytes,
	)
	if err != nil || !bytes.Equal(before.CanonicalBytes, wire.CursorBefore) {
		return ChannelIngressReceipt{}, fmt.Errorf(
			"%w: Envelope CursorBefore does not match the current cursor",
			ErrChannelIngressConflict,
		)
	}
	return current, nil
}

func putPreparedChannelEvent(
	ctx context.Context,
	connection *sql.Conn,
	prepared preparedChannelIngressEvent,
	createdAt int64,
) error {
	for _, content := range []preparedAdmissionContent{
		prepared.cursorAfter,
		prepared.envelope,
	} {
		if err := putAdmissionContent(ctx, connection, content, createdAt); err != nil {
			return err
		}
	}
	return nil
}

func newChannelEventReceipt(
	prepared preparedChannelIngressEvent,
	revision uint64,
	disposition ChannelIngressDisposition,
) ChannelIngressReceipt {
	return ChannelIngressReceipt{
		TenantID:              prepared.input.TenantID,
		WorkspaceID:           prepared.input.WorkspaceID,
		EndpointID:            prepared.input.EndpointID,
		CursorScopeKey:        prepared.input.CursorScopeKey,
		CursorRevision:        revision,
		CursorBeforeRef:       prepared.input.CursorBeforeRef,
		CursorAfterRef:        prepared.cursorAfter.Digest,
		EndpointBindingDigest: prepared.input.EndpointBindingDigest,
		Disposition:           disposition,
		Reason:                prepared.input.Reason,
		IngressKey:            prepared.input.IngressKey,
		ProviderEventIDDigest: prepared.input.ProviderEventIDDigest,
		EnvelopeRef:           prepared.envelope.Digest,
	}
}

func insertChannelReceipt(
	ctx context.Context,
	connection *sql.Conn,
	receipt ChannelIngressReceipt,
	createdAt int64,
) error {
	var before, ingress, providerEvent, envelope, envelopeDigest any
	var principal, aclEpoch, admissionKey, runID any
	if receipt.CursorBeforeRef != "" {
		before = receipt.CursorBeforeRef
	}
	if receipt.IngressKey != "" {
		ingress = receipt.IngressKey
	}
	if receipt.ProviderEventIDDigest != "" {
		providerEvent = receipt.ProviderEventIDDigest
	}
	if receipt.EnvelopeRef != "" {
		envelope = receipt.EnvelopeRef
		envelopeDigest = receipt.EnvelopeRef
	}
	if receipt.PrincipalID != "" {
		principal = receipt.PrincipalID
	}
	if receipt.ACLEpoch != 0 {
		aclEpoch = int64(receipt.ACLEpoch)
	}
	if receipt.AdmissionKey != "" {
		admissionKey = receipt.AdmissionKey
	}
	if receipt.RunID != "" {
		runID = receipt.RunID
	}
	result, err := connection.ExecContext(ctx, `
		INSERT INTO channel_ingress_receipts(
			tenant_id, workspace_id, endpoint_id, cursor_scope_key,
			cursor_revision, cursor_before_ref, cursor_after_ref,
			endpoint_binding_digest, disposition, reason,
			ingress_key, provider_event_id_digest,
			envelope_ref, envelope_digest,
			principal_id, acl_epoch, admission_key, run_id, created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		receipt.TenantID,
		receipt.WorkspaceID,
		receipt.EndpointID,
		receipt.CursorScopeKey,
		int64(receipt.CursorRevision),
		before,
		receipt.CursorAfterRef,
		receipt.EndpointBindingDigest,
		string(receipt.Disposition),
		receipt.Reason,
		ingress,
		providerEvent,
		envelope,
		envelopeDigest,
		principal,
		aclEpoch,
		admissionKey,
		runID,
		createdAt,
	)
	if err != nil {
		return fmt.Errorf("currentstore: insert Channel ingress receipt: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf(
			"%w: receipt insert affected %d rows: %v",
			ErrChannelIngressIntegrity,
			affected,
			err,
		)
	}
	return nil
}

func queryCurrentChannelReceipt(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	endpointID string,
	cursorScopeKey string,
) (ChannelIngressReceipt, bool, error) {
	return scanChannelReceipt(queryer.QueryRowContext(ctx, `
		SELECT
			tenant_id, workspace_id, endpoint_id, cursor_scope_key,
			cursor_revision, cursor_before_ref, cursor_after_ref,
			endpoint_binding_digest, disposition, reason,
			ingress_key, provider_event_id_digest,
			envelope_ref, envelope_digest,
			principal_id, acl_epoch, admission_key, run_id, created_at
		FROM channel_ingress_receipts
		WHERE tenant_id=? AND endpoint_id=? AND cursor_scope_key=?
		ORDER BY cursor_revision DESC
		LIMIT 1
	`, tenantID, endpointID, cursorScopeKey))
}

func queryChannelReceiptByIngressKey(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	endpointID string,
	ingressKey string,
) (ChannelIngressReceipt, bool, error) {
	return scanChannelReceipt(queryer.QueryRowContext(ctx, `
		SELECT
			tenant_id, workspace_id, endpoint_id, cursor_scope_key,
			cursor_revision, cursor_before_ref, cursor_after_ref,
			endpoint_binding_digest, disposition, reason,
			ingress_key, provider_event_id_digest,
			envelope_ref, envelope_digest,
			principal_id, acl_epoch, admission_key, run_id, created_at
		FROM channel_ingress_receipts
		WHERE tenant_id=? AND endpoint_id=? AND ingress_key=?
	`, tenantID, endpointID, ingressKey))
}

func queryChannelReceiptByProviderEvent(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	endpointID string,
	providerEventDigest string,
) (ChannelIngressReceipt, bool, error) {
	return scanChannelReceipt(queryer.QueryRowContext(ctx, `
		SELECT
			tenant_id, workspace_id, endpoint_id, cursor_scope_key,
			cursor_revision, cursor_before_ref, cursor_after_ref,
			endpoint_binding_digest, disposition, reason,
			ingress_key, provider_event_id_digest,
			envelope_ref, envelope_digest,
			principal_id, acl_epoch, admission_key, run_id, created_at
		FROM channel_ingress_receipts
		WHERE tenant_id=? AND endpoint_id=? AND provider_event_id_digest=?
	`, tenantID, endpointID, providerEventDigest))
}

func loadChannelReceiptByRevision(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	endpointID string,
	cursorScopeKey string,
	revision uint64,
) (ChannelIngressReceipt, error) {
	receipt, found, err := scanChannelReceipt(queryer.QueryRowContext(ctx, `
		SELECT
			tenant_id, workspace_id, endpoint_id, cursor_scope_key,
			cursor_revision, cursor_before_ref, cursor_after_ref,
			endpoint_binding_digest, disposition, reason,
			ingress_key, provider_event_id_digest,
			envelope_ref, envelope_digest,
			principal_id, acl_epoch, admission_key, run_id, created_at
		FROM channel_ingress_receipts
		WHERE tenant_id=? AND endpoint_id=? AND cursor_scope_key=?
		  AND cursor_revision=?
	`, tenantID, endpointID, cursorScopeKey, int64(revision)))
	if err != nil {
		return ChannelIngressReceipt{}, err
	}
	if !found {
		return ChannelIngressReceipt{}, ErrChannelIngressNotFound
	}
	if err := verifyChannelReceipt(ctx, queryer, receipt); err != nil {
		return ChannelIngressReceipt{}, err
	}
	return receipt, nil
}

func scanChannelReceipt(row *sql.Row) (ChannelIngressReceipt, bool, error) {
	var receipt ChannelIngressReceipt
	var revision int64
	var disposition string
	var before, ingress, providerEvent, envelope, envelopeDigest sql.NullString
	var principal, admissionKey, runID sql.NullString
	var aclEpoch sql.NullInt64
	var createdAt int64
	err := row.Scan(
		&receipt.TenantID,
		&receipt.WorkspaceID,
		&receipt.EndpointID,
		&receipt.CursorScopeKey,
		&revision,
		&before,
		&receipt.CursorAfterRef,
		&receipt.EndpointBindingDigest,
		&disposition,
		&receipt.Reason,
		&ingress,
		&providerEvent,
		&envelope,
		&envelopeDigest,
		&principal,
		&aclEpoch,
		&admissionKey,
		&runID,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ChannelIngressReceipt{}, false, nil
	}
	if err != nil {
		return ChannelIngressReceipt{}, false, fmt.Errorf(
			"currentstore: read Channel ingress receipt: %w",
			err,
		)
	}
	if revision < 0 || aclEpoch.Int64 < 0 {
		return ChannelIngressReceipt{}, true, fmt.Errorf(
			"%w: negative receipt revision or ACL epoch",
			ErrChannelIngressIntegrity,
		)
	}
	parsedTime, err := timeFromUnixMicro(createdAt)
	if err != nil {
		return ChannelIngressReceipt{}, true, fmt.Errorf(
			"%w: invalid receipt created_at",
			ErrChannelIngressIntegrity,
		)
	}
	receipt.CursorRevision = uint64(revision)
	receipt.CursorBeforeRef = before.String
	receipt.Disposition = ChannelIngressDisposition(disposition)
	receipt.IngressKey = ingress.String
	receipt.ProviderEventIDDigest = providerEvent.String
	receipt.EnvelopeRef = envelope.String
	if envelope.Valid != envelopeDigest.Valid || envelope.String != envelopeDigest.String {
		return ChannelIngressReceipt{}, true, fmt.Errorf(
			"%w: envelope ref/digest differ",
			ErrChannelIngressIntegrity,
		)
	}
	receipt.PrincipalID = principal.String
	if aclEpoch.Valid {
		receipt.ACLEpoch = uint64(aclEpoch.Int64)
	}
	receipt.AdmissionKey = admissionKey.String
	receipt.RunID = runID.String
	receipt.CreatedAt = parsedTime
	return receipt, true, nil
}

func verifyChannelReceipt(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	receipt ChannelIngressReceipt,
) error {
	if !validChannelOpaqueID(receipt.TenantID) ||
		!validChannelOpaqueID(receipt.WorkspaceID) ||
		!validChannelOpaqueID(receipt.EndpointID) ||
		!validChannelOpaqueID(receipt.CursorScopeKey) ||
		!validChannelOpaqueID(receipt.Reason) ||
		!moduleapi.ValidSHA256(receipt.CursorAfterRef) ||
		!moduleapi.ValidSHA256(receipt.EndpointBindingDigest) ||
		receipt.CreatedAt.IsZero() {
		return fmt.Errorf(
			"%w: malformed receipt identity",
			ErrChannelIngressIntegrity,
		)
	}
	after, err := queryContent(ctx, queryer, receipt.CursorAfterRef)
	if err != nil || after.Kind != ContentChannelCursor ||
		after.MediaType != channelCursorMediaType ||
		len(after.CanonicalBytes) > maxChannelCursorBytes {
		return fmt.Errorf(
			"%w: invalid CursorAfter closure",
			ErrChannelIngressIntegrity,
		)
	}
	switch receipt.Disposition {
	case ChannelCursorSeed:
		if receipt.CursorRevision != 0 || receipt.CursorBeforeRef != "" ||
			receipt.IngressKey != "" || receipt.ProviderEventIDDigest != "" ||
			receipt.EnvelopeRef != "" || receipt.PrincipalID != "" ||
			receipt.ACLEpoch != 0 || receipt.AdmissionKey != "" || receipt.RunID != "" {
			return fmt.Errorf(
				"%w: invalid CURSOR_SEED shape",
				ErrChannelIngressIntegrity,
			)
		}
		return nil
	case ChannelIngressRejected, ChannelIngressAccepted:
		if receipt.CursorRevision == 0 ||
			!moduleapi.ValidSHA256(receipt.CursorBeforeRef) ||
			!moduleapi.ValidSHA256(receipt.IngressKey) ||
			!moduleapi.ValidSHA256(receipt.ProviderEventIDDigest) ||
			!moduleapi.ValidSHA256(receipt.EnvelopeRef) {
			return fmt.Errorf(
				"%w: invalid event receipt shape",
				ErrChannelIngressIntegrity,
			)
		}
	default:
		return fmt.Errorf(
			"%w: unknown disposition %q",
			ErrChannelIngressIntegrity,
			receipt.Disposition,
		)
	}
	before, err := queryContent(ctx, queryer, receipt.CursorBeforeRef)
	if err != nil || before.Kind != ContentChannelCursor ||
		before.MediaType != channelCursorMediaType ||
		len(before.CanonicalBytes) > maxChannelCursorBytes {
		return fmt.Errorf(
			"%w: invalid CursorBefore closure",
			ErrChannelIngressIntegrity,
		)
	}
	envelope, err := queryContent(ctx, queryer, receipt.EnvelopeRef)
	if err != nil || envelope.Kind != ContentChannelIngressEnvelope ||
		envelope.MediaType != channelEnvelopeMediaType ||
		len(envelope.CanonicalBytes) == 0 ||
		len(envelope.CanonicalBytes) > maxChannelEnvelopeBytes {
		return fmt.Errorf(
			"%w: invalid ingress Envelope closure",
			ErrChannelIngressIntegrity,
		)
	}
	previous, found, err := queryChannelReceiptByRevisionRaw(
		ctx,
		queryer,
		receipt.TenantID,
		receipt.EndpointID,
		receipt.CursorScopeKey,
		receipt.CursorRevision-1,
	)
	if err != nil {
		return err
	}
	if !found || previous.CursorAfterRef != receipt.CursorBeforeRef ||
		previous.WorkspaceID != receipt.WorkspaceID {
		return fmt.Errorf(
			"%w: cursor revision chain is broken",
			ErrChannelIngressIntegrity,
		)
	}
	if receipt.Disposition == ChannelIngressRejected {
		if receipt.PrincipalID != "" || receipt.ACLEpoch != 0 ||
			receipt.AdmissionKey != "" || receipt.RunID != "" {
			return fmt.Errorf(
				"%w: REJECTED receipt carries accepted identity",
				ErrChannelIngressIntegrity,
			)
		}
		return nil
	}
	if !validChannelOpaqueID(receipt.PrincipalID) || receipt.ACLEpoch == 0 ||
		receipt.AdmissionKey != channelAdmissionKey(receipt.IngressKey) ||
		!validChannelOpaqueID(receipt.RunID) {
		return fmt.Errorf(
			"%w: ACCEPTED receipt lacks Principal, ACL, Admission, or Run",
			ErrChannelIngressIntegrity,
		)
	}
	return nil
}

func queryChannelReceiptByRevisionRaw(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	endpointID string,
	cursorScopeKey string,
	revision uint64,
) (ChannelIngressReceipt, bool, error) {
	return scanChannelReceipt(queryer.QueryRowContext(ctx, `
		SELECT
			tenant_id, workspace_id, endpoint_id, cursor_scope_key,
			cursor_revision, cursor_before_ref, cursor_after_ref,
			endpoint_binding_digest, disposition, reason,
			ingress_key, provider_event_id_digest,
			envelope_ref, envelope_digest,
			principal_id, acl_epoch, admission_key, run_id, created_at
		FROM channel_ingress_receipts
		WHERE tenant_id=? AND endpoint_id=? AND cursor_scope_key=?
		  AND cursor_revision=?
	`, tenantID, endpointID, cursorScopeKey, int64(revision)))
}

func loadChannelReceiptAdmission(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	receipt ChannelIngressReceipt,
) (RunAdmissionResult, error) {
	if receipt.Disposition != ChannelIngressAccepted {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: receipt is not ACCEPTED",
			ErrChannelIngressIntegrity,
		)
	}
	var runID, intentDigest, workspaceID string
	if err := queryer.QueryRowContext(ctx, `
		SELECT run_id, admission_intent_digest, workspace_id
		FROM runs
		WHERE tenant_id=? AND admission_key=?
	`, receipt.TenantID, receipt.AdmissionKey).Scan(
		&runID,
		&intentDigest,
		&workspaceID,
	); err != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: ACCEPTED receipt Run is unavailable: %v",
			ErrChannelIngressIntegrity,
			err,
		)
	}
	if runID != receipt.RunID || workspaceID != receipt.WorkspaceID {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: ACCEPTED receipt does not close to its Run",
			ErrChannelIngressIntegrity,
		)
	}
	return loadAdmissionClosure(
		ctx,
		queryer,
		runID,
		receipt.TenantID,
		receipt.AdmissionKey,
		intentDigest,
		workspaceID,
	)
}
