package controlapp

import (
	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type modulesPositionWireV1 struct {
	SortVersion    string `json:"sort_version"`
	LastInstanceID string `json:"last_instance_id"`
}

func modulesPositionDigestV1(sortVersion, lastInstanceID string) (string, error) {
	_, digest, err := canonicalDigestV1(
		modulesPositionDigestDomainV1,
		modulesPositionWireV1{
			SortVersion:    sortVersion,
			LastInstanceID: lastInstanceID,
		},
	)
	return digest, err
}

// Validate verifies the canonical application metadata and its internal
// position binding. Authentication of its opaque transport token is outside
// W6-1A and must happen before this value reaches the service.
func (cursor DecodedModulesCursorV1) Validate() error {
	if cursor.SchemaVersion != ModulesCursorSchemaVersionV1 ||
		!validOpaqueIDV1(cursor.BootID) ||
		!validOpaqueIDV1(cursor.PrincipalID) ||
		!validPositiveJSONIntegerV1(cursor.AuthorizationRevision) ||
		!moduleapi.ValidSHA256(cursor.ScopeSetDigest) ||
		cursor.Collection != controlapicontract.PageCollectionModulesV1 ||
		cursor.FilterDigest != modulesEmptyFilterDigestV1() ||
		cursor.SortVersion != ModulesSortVersionV1 ||
		!validPositiveJSONIntegerV1(cursor.SourceRevision) ||
		!moduleapi.ValidSHA256(cursor.SourceDigest) ||
		!moduleapi.ValidSHA256(cursor.ViewSnapshotDigest) ||
		!validPositiveJSONIntegerV1(cursor.ObservedAtUnixMicros) ||
		!validOpaqueIDV1(cursor.LastInstanceID) ||
		!moduleapi.ValidSHA256(cursor.PositionDigest) {
		return ErrCursorInvalid
	}
	frozenScope, _, scopeDigest, err := controlapicontract.NewControlScopeV1(
		cursor.Scope,
	)
	if err != nil || !equalScopeV1(frozenScope, cursor.Scope) ||
		cursor.ScopeDigest != scopeDigest {
		return ErrCursorInvalid
	}
	positionDigest, err := modulesPositionDigestV1(
		cursor.SortVersion,
		cursor.LastInstanceID,
	)
	if err != nil || cursor.PositionDigest != positionDigest {
		return ErrCursorInvalid
	}
	if _, _, err := canonicalDigestV1(
		"freeagent.control-modules-decoded-cursor/v1",
		cursor,
	); err != nil {
		return ErrCursorInvalid
	}
	return nil
}

func newDecodedModulesCursorV1(
	session controlapicontract.ControlSessionV1,
	scope controlapicontract.ControlScopeV1,
	scopeDigest string,
	sourceRevision uint64,
	sourceDigest string,
	viewSnapshotDigest string,
	observedAt uint64,
	lastInstanceID string,
) (DecodedModulesCursorV1, error) {
	positionDigest, err := modulesPositionDigestV1(
		ModulesSortVersionV1,
		lastInstanceID,
	)
	if err != nil {
		return DecodedModulesCursorV1{}, ErrIntegrityFailure
	}
	cursor := DecodedModulesCursorV1{
		SchemaVersion:         ModulesCursorSchemaVersionV1,
		BootID:                session.BootID,
		PrincipalID:           session.PrincipalID,
		AuthorizationRevision: session.AuthorizationRevision,
		ScopeSetDigest:        session.ScopeSetDigest,
		Scope:                 scope,
		ScopeDigest:           scopeDigest,
		Collection:            controlapicontract.PageCollectionModulesV1,
		FilterDigest:          modulesEmptyFilterDigestV1(),
		SortVersion:           ModulesSortVersionV1,
		SourceRevision:        sourceRevision,
		SourceDigest:          sourceDigest,
		ViewSnapshotDigest:    viewSnapshotDigest,
		ObservedAtUnixMicros:  observedAt,
		LastInstanceID:        lastInstanceID,
		PositionDigest:        positionDigest,
	}
	if err := cursor.Validate(); err != nil {
		return DecodedModulesCursorV1{}, ErrIntegrityFailure
	}
	return cursor, nil
}
