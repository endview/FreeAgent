package controlapp

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const maximumVisibleBindingsV1 = 1 << 17

type ModulesServiceV1 struct {
	reader PublishedBasisReaderV1
}

func NewModulesServiceV1(
	reader PublishedBasisReaderV1,
) (*ModulesServiceV1, error) {
	if nilInterfaceV1(reader) {
		return nil, ErrInvalidRequest
	}
	return &ModulesServiceV1{reader: reader}, nil
}

type authorizedRequestV1 struct {
	session     controlapicontract.ControlSessionV1
	scope       controlapicontract.ControlScopeV1
	scopeDigest string
	observedAt  uint64
}

type visibleModulesViewV1 struct {
	publishedPointer   controlapicontract.ExpectedResourceRefV1
	basis              controlapicontract.PublishedBasisRefV1
	view               controlapicontract.ControlViewSnapshotV1
	viewSnapshotDigest string
	sourceRevision     uint64
	sourceDigest       string
	modules            []ModuleDetailV1
}

// ListModulesV1 returns a bounded, scope-filtered keyset page. It performs no
// effect, opens no connection, and treats a decoded cursor only as consistency
// metadata after repeating live authorization.
func (service *ModulesServiceV1) ListModulesV1(
	ctx context.Context,
	input ListModulesInputV1,
) (ModulesPageV1, error) {
	if service == nil || nilInterfaceV1(service.reader) || ctx == nil {
		return ModulesPageV1{}, ErrInvalidRequest
	}
	authorized, err := authorizeModulesRequestV1(
		input.Authorization,
		input.Scope,
		input.ObservedAt,
	)
	if err != nil {
		return ModulesPageV1{}, err
	}
	limit, err := normalizeModulesPageV1(input.Page)
	if err != nil {
		return ModulesPageV1{}, err
	}
	if input.Cursor != nil {
		if err := validateCursorRequestV1(*input.Cursor, authorized); err != nil {
			return ModulesPageV1{}, err
		}
	}
	viewObservedAt := authorized.observedAt
	if input.Cursor != nil {
		viewObservedAt = input.Cursor.ObservedAtUnixMicros
	}
	view, err := service.loadVisibleModulesV1(
		ctx,
		input.Authorization,
		authorized,
		authorized.scope,
		authorized.scopeDigest,
		viewObservedAt,
	)
	if err != nil {
		return ModulesPageV1{}, err
	}
	start := 0
	if input.Cursor != nil {
		if input.Cursor.SourceRevision != view.sourceRevision ||
			input.Cursor.SourceDigest != view.sourceDigest ||
			input.Cursor.ViewSnapshotDigest != view.viewSnapshotDigest {
			return ModulesPageV1{}, ErrCursorStale
		}
		index, found := findModuleV1(view.modules, input.Cursor.LastInstanceID)
		if !found {
			return ModulesPageV1{}, ErrCursorInvalid
		}
		start = index + 1
	}
	windowEnd := start + int(limit) + 1
	if windowEnd > len(view.modules) {
		windowEnd = len(view.modules)
	}
	window := view.modules[start:windowEnd]
	hasMore := len(window) > int(limit)
	if hasMore {
		window = window[:limit]
	}
	items := make([]ModuleSummaryV1, len(window))
	for index := range window {
		items[index] = cloneModuleSummaryV1(window[index].Summary)
	}

	var nextCursor *DecodedModulesCursorV1
	if hasMore {
		cursor, cursorErr := newDecodedModulesCursorV1(
			authorized.session,
			authorized.scope,
			authorized.scopeDigest,
			view.sourceRevision,
			view.sourceDigest,
			view.viewSnapshotDigest,
			viewObservedAt,
			items[len(items)-1].InstanceID,
		)
		if cursorErr != nil {
			return ModulesPageV1{}, cursorErr
		}
		nextCursor = &cursor
	}
	page := ModulesPageV1{
		SchemaVersion:      ModulesPageSchemaVersionV1,
		PublishedPointer:   view.publishedPointer,
		Basis:              view.basis,
		View:               cloneControlViewV1(view.view),
		ViewSnapshotDigest: view.viewSnapshotDigest,
		SourceRevision:     view.sourceRevision,
		SourceDigest:       view.sourceDigest,
		SortVersion:        ModulesSortVersionV1,
		FilterDigest:       modulesEmptyFilterDigestV1(),
		Items:              items,
		HasMore:            hasMore,
		NextCursor:         nextCursor,
	}
	projectionDigest, err := digestModulesPageProjectionV1(
		page,
		limit,
		cursorLastInstanceIDV1(input.Cursor),
	)
	if err != nil {
		return ModulesPageV1{}, ErrIntegrityFailure
	}
	page.ProjectionDigest = projectionDigest
	page.StrongETag, err = digestStrongETagV1(
		modulesPageETagDomainV1,
		authorized,
		projectionDigest,
	)
	if err != nil {
		return ModulesPageV1{}, ErrIntegrityFailure
	}
	if err := reauthorizeModulesRequestV1(
		input.Authorization,
		authorized,
	); err != nil {
		return ModulesPageV1{}, err
	}
	return cloneModulesPageV1(page), nil
}

// GetModuleV1 returns one exact scoped module projection. An absent Catalog
// entry and an entry outside the requested Workspace are intentionally
// indistinguishable.
func (service *ModulesServiceV1) GetModuleV1(
	ctx context.Context,
	input GetModuleInputV1,
) (ModuleDetailResultV1, error) {
	if service == nil || nilInterfaceV1(service.reader) || ctx == nil ||
		!validModuleInstanceIDV1(input.InstanceID) {
		return ModuleDetailResultV1{}, ErrInvalidRequest
	}
	authorized, err := authorizeModulesRequestV1(
		input.Authorization,
		input.Scope,
		input.ObservedAt,
	)
	if err != nil {
		return ModuleDetailResultV1{}, err
	}
	view, err := service.loadVisibleModulesV1(
		ctx,
		input.Authorization,
		authorized,
		authorized.scope,
		authorized.scopeDigest,
		authorized.observedAt,
	)
	if err != nil {
		return ModuleDetailResultV1{}, err
	}
	index, found := findModuleV1(view.modules, input.InstanceID)
	if !found {
		return ModuleDetailResultV1{}, ErrNotFound
	}
	result := ModuleDetailResultV1{
		SchemaVersion:      ModuleDetailSchemaVersionV1,
		PublishedPointer:   view.publishedPointer,
		Basis:              view.basis,
		View:               cloneControlViewV1(view.view),
		ViewSnapshotDigest: view.viewSnapshotDigest,
		SourceRevision:     view.sourceRevision,
		SourceDigest:       view.sourceDigest,
		Module:             cloneModuleDetailV1(view.modules[index]),
	}
	result.ProjectionDigest, err = digestModuleDetailProjectionV1(result)
	if err != nil {
		return ModuleDetailResultV1{}, ErrIntegrityFailure
	}
	result.StrongETag, err = digestStrongETagV1(
		moduleDetailETagDomainV1,
		authorized,
		result.ProjectionDigest,
	)
	if err != nil {
		return ModuleDetailResultV1{}, ErrIntegrityFailure
	}
	if err := reauthorizeModulesRequestV1(
		input.Authorization,
		authorized,
	); err != nil {
		return ModuleDetailResultV1{}, err
	}
	return cloneModuleDetailResultV1(result), nil
}

func validModuleInstanceIDV1(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func authorizeModulesRequestV1(
	authorization AuthorizationContextV1,
	scope controlapicontract.ControlScopeV1,
	observedAt uint64,
) (authorizedRequestV1, error) {
	if nilInterfaceV1(authorization) || !validPositiveJSONIntegerV1(observedAt) {
		return authorizedRequestV1{}, ErrInvalidRequest
	}
	session := authorization.Session()
	frozenSession, sessionCanonical, _, err :=
		controlapicontract.NewControlSessionV1(session)
	if err != nil {
		return authorizedRequestV1{}, ErrInvalidRequest
	}
	inputSessionCanonical, _, err := canonicalDigestV1(
		"freeagent.controlapp-session-canonical-check/v1",
		session,
	)
	if err != nil || !bytes.Equal(inputSessionCanonical, sessionCanonical) ||
		!reflect.DeepEqual(frozenSession, session) {
		return authorizedRequestV1{}, ErrInvalidRequest
	}
	if observedAt < session.IssuedAtUnixMicros ||
		observedAt >= session.ExpiresAtUnixMicros {
		return authorizedRequestV1{}, ErrSessionExpired
	}
	hasObserve := false
	for _, capability := range session.Capabilities {
		if capability == controlapicontract.CapabilityObserveV1 {
			hasObserve = true
			break
		}
	}
	if !hasObserve {
		return authorizedRequestV1{}, ErrForbidden
	}
	frozenScope, scopeCanonical, scopeDigest, err :=
		controlapicontract.NewControlScopeV1(scope)
	if err != nil {
		return authorizedRequestV1{}, ErrInvalidRequest
	}
	inputScopeCanonical, _, err := canonicalDigestV1(
		"freeagent.controlapp-scope-canonical-check/v1",
		scope,
	)
	if err != nil || !bytes.Equal(inputScopeCanonical, scopeCanonical) ||
		!equalScopeV1(frozenScope, scope) {
		return authorizedRequestV1{}, ErrInvalidRequest
	}
	authorizerDigest := authorization.Digest()
	if !moduleapi.ValidSHA256(authorizerDigest) ||
		authorizerDigest != session.ScopeSetDigest ||
		!authorization.Allows(frozenScope) {
		return authorizedRequestV1{}, ErrForbidden
	}
	request := authorizedRequestV1{
		session:     frozenSession,
		scope:       frozenScope,
		scopeDigest: scopeDigest,
		observedAt:  observedAt,
	}
	if err := reauthorizeModulesRequestV1(authorization, request); err != nil {
		return authorizedRequestV1{}, err
	}
	return request, nil
}

// reauthorizeModulesRequestV1 repeats the complete immutable authorization
// binding. It is used before Store access, once per projected resource, and
// immediately before returning a result. This makes mutable or mismatched
// authorization implementations fail closed while PermitV1 remains a cheap,
// immutable implementation.
func reauthorizeModulesRequestV1(
	authorization AuthorizationContextV1,
	request authorizedRequestV1,
) error {
	if nilInterfaceV1(authorization) {
		return ErrForbidden
	}
	session := authorization.Session()
	frozen, canonical, _, err := controlapicontract.NewControlSessionV1(session)
	if err != nil || !reflect.DeepEqual(frozen, session) ||
		!reflect.DeepEqual(frozen, request.session) {
		return ErrForbidden
	}
	inputCanonical, _, err := canonicalDigestV1(
		"freeagent.controlapp-session-canonical-check/v1",
		session,
	)
	if err != nil || !bytes.Equal(inputCanonical, canonical) ||
		request.observedAt < session.IssuedAtUnixMicros ||
		request.observedAt >= session.ExpiresAtUnixMicros {
		return ErrForbidden
	}
	digest := authorization.Digest()
	if !moduleapi.ValidSHA256(digest) ||
		digest != request.session.ScopeSetDigest ||
		!authorization.Allows(request.scope) {
		return ErrForbidden
	}
	return nil
}

func normalizeModulesPageV1(
	input controlapicontract.PageQueryV1,
) (uint16, error) {
	if input.After != nil || input.FilterDigest != "" {
		return 0, ErrInvalidRequest
	}
	limit := input.Limit
	if limit == 0 {
		limit = DefaultModulesPageLimitV1
	}
	if limit == 0 || limit > MaximumModulesPageLimitV1 {
		return 0, ErrInvalidRequest
	}
	return limit, nil
}

func validateCursorRequestV1(
	cursor DecodedModulesCursorV1,
	request authorizedRequestV1,
) error {
	if err := cursor.Validate(); err != nil {
		return ErrCursorInvalid
	}
	if cursor.BootID != request.session.BootID ||
		cursor.AuthorizationRevision != request.session.AuthorizationRevision {
		return ErrCursorStale
	}
	if cursor.PrincipalID != request.session.PrincipalID ||
		cursor.ScopeSetDigest != request.session.ScopeSetDigest ||
		!equalScopeV1(cursor.Scope, request.scope) ||
		cursor.ScopeDigest != request.scopeDigest {
		return ErrCursorInvalid
	}
	return nil
}

func (service *ModulesServiceV1) loadVisibleModulesV1(
	ctx context.Context,
	authorization AuthorizationContextV1,
	authorized authorizedRequestV1,
	scope controlapicontract.ControlScopeV1,
	scopeDigest string,
	observedAt uint64,
) (visibleModulesViewV1, error) {
	if err := contextErrorV1(ctx); err != nil {
		return visibleModulesViewV1{}, err
	}
	basis, control, catalog, err := service.reader.LoadPublishedBasis(
		ctx,
		scope.TenantID,
	)
	if err != nil {
		if contextErr := contextErrorV1(ctx); contextErr != nil {
			return visibleModulesViewV1{}, contextErr
		}
		return visibleModulesViewV1{}, classifyReaderErrorV1(
			err,
			ErrStoreUnavailable,
		)
	}
	if err := validateLoadedBasisV1(basis, control, catalog); err != nil {
		return visibleModulesViewV1{}, err
	}
	if basis.TenantID != scope.TenantID {
		return visibleModulesViewV1{}, ErrIntegrityFailure
	}
	if err := service.reader.VerifyPublishedControlCatalogClosureV1(
		ctx,
		control,
		catalog,
	); err != nil {
		if contextErr := contextErrorV1(ctx); contextErr != nil {
			return visibleModulesViewV1{}, contextErr
		}
		return visibleModulesViewV1{}, classifyReaderErrorV1(
			err,
			ErrIntegrityFailure,
		)
	}
	modules, err := projectVisibleModulesV1(
		scope,
		control,
		catalog,
		func() error {
			return reauthorizeModulesRequestV1(authorization, authorized)
		},
	)
	if err != nil {
		return visibleModulesViewV1{}, err
	}
	basisRef := PublishedBasisRefV1(basis)
	if err := basisRef.Validate(); err != nil {
		return visibleModulesViewV1{}, ErrIntegrityFailure
	}
	publishedPointer, err := PublishedPointerRefV1(basisRef)
	if err != nil {
		return visibleModulesViewV1{}, err
	}
	sourceWire := modulesSourceWireV1{
		SchemaVersion: "control-modules-source/v1",
		ScopeDigest:   scopeDigest,
		Basis:         basisRef,
		Modules:       cloneModuleDetailsV1(modules),
	}
	_, sourceDigest, err := canonicalDigestV1(
		modulesSourceDigestDomainV1,
		sourceWire,
	)
	if err != nil {
		return visibleModulesViewV1{}, ErrResourceExhausted
	}
	view, _, viewDigest, err := controlapicontract.NewControlViewSnapshotV1(
		controlapicontract.ControlViewSnapshotV1{
			SchemaVersion:        controlapicontract.ControlViewSnapshotSchemaVersionV1,
			Scope:                scope,
			ScopeDigest:          scopeDigest,
			ObservedAtUnixMicros: observedAt,
			Basis:                basisRef,
			Sections: []controlapicontract.ControlViewSectionV1{
				{
					Kind:           controlapicontract.ViewSectionModulesV1,
					SourceRevision: basis.PointerRevision,
					SourceDigest:   sourceDigest,
					ItemCount:      uint32(len(modules)),
					Truncated:      false,
				},
			},
		},
	)
	if err != nil {
		return visibleModulesViewV1{}, ErrIntegrityFailure
	}
	return visibleModulesViewV1{
		publishedPointer:   publishedPointer,
		basis:              basisRef,
		view:               view,
		viewSnapshotDigest: viewDigest,
		sourceRevision:     basis.PointerRevision,
		sourceDigest:       sourceDigest,
		modules:            modules,
	}, nil
}

func classifyReaderErrorV1(input error, fallback error) error {
	if errors.Is(input, context.Canceled) ||
		errors.Is(input, context.DeadlineExceeded) {
		return ErrCancelled
	}
	for _, classified := range []error{
		ErrNotFound,
		ErrStoreBusy,
		ErrStoreUnavailable,
		ErrIntegrityFailure,
		ErrResourceExhausted,
		ErrCancelled,
	} {
		if errors.Is(input, classified) {
			return classified
		}
	}
	return fallback
}

func nilInterfaceV1(value any) bool {
	if value == nil {
		return true
	}
	reflection := reflect.ValueOf(value)
	switch reflection.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflection.IsNil()
	default:
		return false
	}
}

func validateLoadedBasisV1(
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	if err := basis.Validate(); err != nil {
		return ErrIntegrityFailure
	}
	frozenControl, controlRef, _, err := controlcontract.NewControlSnapshot(control)
	if err != nil || !reflect.DeepEqual(frozenControl, control) ||
		controlRef != basis.Control {
		return ErrIntegrityFailure
	}
	frozenCatalog, catalogRef, _, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil || !reflect.DeepEqual(frozenCatalog, catalog) ||
		catalogRef != basis.Catalog ||
		basis.TenantID != control.TenantID ||
		basis.TenantID != catalog.TenantID ||
		catalog.ControlSnapshotID != control.SnapshotID ||
		catalog.ControlSnapshotDigest != control.Digest {
		return ErrIntegrityFailure
	}
	return nil
}

type modulesSourceWireV1 struct {
	SchemaVersion string                                 `json:"schema_version"`
	ScopeDigest   string                                 `json:"scope_digest"`
	Basis         controlapicontract.PublishedBasisRefV1 `json:"basis"`
	Modules       []ModuleDetailV1                       `json:"modules"`
}

// PublishedBasisRefV1 projects the unique Current Store basis into the safe
// Control API reference shared by read views and effect-free operation
// preconditions. It performs no read and grants no authority.
func PublishedBasisRefV1(
	basis controlcontract.PublishedBasis,
) controlapicontract.PublishedBasisRefV1 {
	return controlapicontract.PublishedBasisRefV1{
		TenantID:        basis.TenantID,
		PointerRevision: basis.PointerRevision,
		Control: controlapicontract.RevisionedDigestRefV1{
			ID:       basis.Control.SnapshotID,
			Revision: basis.Control.Revision,
			Digest:   basis.Control.Digest,
		},
		Catalog: controlapicontract.RevisionedDigestRefV1{
			ID:       basis.Catalog.GenerationID,
			Revision: basis.Catalog.Generation,
			Digest:   basis.Catalog.Digest,
		},
	}
}

// PublishedPointerRefV1 derives the sole strong digest used for MODULE
// operation If-Match preconditions. Consumers must not duplicate its domain
// or substitute an HTTP projection ETag.
func PublishedPointerRefV1(
	basis controlapicontract.PublishedBasisRefV1,
) (controlapicontract.ExpectedResourceRefV1, error) {
	frozen, _, digest, err := controlapicontract.NewPublishedBasisRefV1(basis)
	if err != nil {
		return controlapicontract.ExpectedResourceRefV1{}, ErrIntegrityFailure
	}
	ref := controlapicontract.ExpectedResourceRefV1{
		Kind:       controlapicontract.ResourcePublishedPointerV1,
		ResourceID: frozen.TenantID,
		Revision:   frozen.PointerRevision,
		Digest:     digest,
	}
	if err := ref.Validate(); err != nil {
		return controlapicontract.ExpectedResourceRefV1{}, ErrIntegrityFailure
	}
	return ref, nil
}

func contextErrorV1(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return ErrCancelled
		}
		return ErrCancelled
	}
	return nil
}
