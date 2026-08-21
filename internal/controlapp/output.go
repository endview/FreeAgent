package controlapp

import (
	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type modulesPageProjectionWireV1 struct {
	SchemaVersion      string                                   `json:"schema_version"`
	PublishedPointer   controlapicontract.ExpectedResourceRefV1 `json:"published_pointer"`
	Basis              controlapicontract.PublishedBasisRefV1   `json:"basis"`
	View               controlapicontract.ControlViewSnapshotV1 `json:"view"`
	ViewSnapshotDigest string                                   `json:"view_snapshot_digest"`
	SourceRevision     uint64                                   `json:"source_revision"`
	SourceDigest       string                                   `json:"source_digest"`
	SortVersion        string                                   `json:"sort_version"`
	FilterDigest       string                                   `json:"filter_digest"`
	Limit              uint16                                   `json:"limit"`
	AfterInstanceID    string                                   `json:"after_instance_id,omitempty"`
	Items              []ModuleSummaryV1                        `json:"items"`
	HasMore            bool                                     `json:"has_more"`
	NextCursor         *DecodedModulesCursorV1                  `json:"next_cursor,omitempty"`
}

type moduleDetailProjectionWireV1 struct {
	SchemaVersion      string                                   `json:"schema_version"`
	PublishedPointer   controlapicontract.ExpectedResourceRefV1 `json:"published_pointer"`
	Basis              controlapicontract.PublishedBasisRefV1   `json:"basis"`
	View               controlapicontract.ControlViewSnapshotV1 `json:"view"`
	ViewSnapshotDigest string                                   `json:"view_snapshot_digest"`
	SourceRevision     uint64                                   `json:"source_revision"`
	SourceDigest       string                                   `json:"source_digest"`
	Module             ModuleDetailV1                           `json:"module"`
}

type strongETagWireV1 struct {
	BootID                string `json:"boot_id"`
	PrincipalID           string `json:"principal_id"`
	AuthorizationRevision uint64 `json:"authorization_revision"`
	ScopeSetDigest        string `json:"scope_set_digest"`
	ScopeDigest           string `json:"scope_digest"`
	ProjectionDigest      string `json:"projection_digest"`
}

func digestModulesPageProjectionV1(
	page ModulesPageV1,
	limit uint16,
	afterInstanceID string,
) (string, error) {
	if page.SchemaVersion != ModulesPageSchemaVersionV1 {
		return "", ErrIntegrityFailure
	}
	_, digest, err := canonicalDigestV1(
		modulesPageProjectionDomainV1,
		modulesPageProjectionWireV1{
			SchemaVersion:      page.SchemaVersion,
			PublishedPointer:   page.PublishedPointer,
			Basis:              page.Basis,
			View:               cloneControlViewV1(page.View),
			ViewSnapshotDigest: page.ViewSnapshotDigest,
			SourceRevision:     page.SourceRevision,
			SourceDigest:       page.SourceDigest,
			SortVersion:        page.SortVersion,
			FilterDigest:       page.FilterDigest,
			Limit:              limit,
			AfterInstanceID:    afterInstanceID,
			Items:              cloneModuleSummariesV1(page.Items),
			HasMore:            page.HasMore,
			NextCursor:         cloneCursorPointerV1(page.NextCursor),
		},
	)
	return digest, err
}

func digestModuleDetailProjectionV1(
	result ModuleDetailResultV1,
) (string, error) {
	if result.SchemaVersion != ModuleDetailSchemaVersionV1 {
		return "", ErrIntegrityFailure
	}
	_, digest, err := canonicalDigestV1(
		moduleDetailProjectionDomainV1,
		moduleDetailProjectionWireV1{
			SchemaVersion:      result.SchemaVersion,
			PublishedPointer:   result.PublishedPointer,
			Basis:              result.Basis,
			View:               cloneControlViewV1(result.View),
			ViewSnapshotDigest: result.ViewSnapshotDigest,
			SourceRevision:     result.SourceRevision,
			SourceDigest:       result.SourceDigest,
			Module:             cloneModuleDetailV1(result.Module),
		},
	)
	return digest, err
}

func digestStrongETagV1(
	domain string,
	request authorizedRequestV1,
	projectionDigest string,
) (string, error) {
	_, digest, err := canonicalDigestV1(
		domain,
		strongETagWireV1{
			BootID:                request.session.BootID,
			PrincipalID:           request.session.PrincipalID,
			AuthorizationRevision: request.session.AuthorizationRevision,
			ScopeSetDigest:        request.session.ScopeSetDigest,
			ScopeDigest:           request.scopeDigest,
			ProjectionDigest:      projectionDigest,
		},
	)
	if err != nil {
		return "", err
	}
	return `"` + digest + `"`, nil
}

func cursorLastInstanceIDV1(cursor *DecodedModulesCursorV1) string {
	if cursor == nil {
		return ""
	}
	return cursor.LastInstanceID
}

func cloneModuleSummaryV1(input ModuleSummaryV1) ModuleSummaryV1 {
	result := input
	result.Provides = append(result.Provides[:0:0], input.Provides...)
	if result.Provides == nil {
		result.Provides = []moduleapi.PortRef{}
	}
	return result
}

func cloneModuleSummariesV1(input []ModuleSummaryV1) []ModuleSummaryV1 {
	result := make([]ModuleSummaryV1, len(input))
	for index := range input {
		result[index] = cloneModuleSummaryV1(input[index])
	}
	return result
}

func cloneModuleBindingV1(input ModuleBindingSummaryV1) ModuleBindingSummaryV1 {
	result := input
	result.StaticContextRefs = append(
		result.StaticContextRefs[:0:0],
		input.StaticContextRefs...,
	)
	if result.StaticContextRefs == nil {
		result.StaticContextRefs = []string{}
	}
	return result
}

func cloneModuleDetailV1(input ModuleDetailV1) ModuleDetailV1 {
	result := ModuleDetailV1{
		Summary:  cloneModuleSummaryV1(input.Summary),
		Bindings: make([]ModuleBindingSummaryV1, len(input.Bindings)),
	}
	for index := range input.Bindings {
		result.Bindings[index] = cloneModuleBindingV1(input.Bindings[index])
	}
	return result
}

func cloneModuleDetailsV1(input []ModuleDetailV1) []ModuleDetailV1 {
	result := make([]ModuleDetailV1, len(input))
	for index := range input {
		result[index] = cloneModuleDetailV1(input[index])
	}
	return result
}

func cloneControlViewV1(
	input controlapicontract.ControlViewSnapshotV1,
) controlapicontract.ControlViewSnapshotV1 {
	result := input
	result.Sections = append(
		result.Sections[:0:0],
		input.Sections...,
	)
	return result
}

func cloneCursorPointerV1(
	input *DecodedModulesCursorV1,
) *DecodedModulesCursorV1 {
	if input == nil {
		return nil
	}
	result := *input
	return &result
}

func cloneModulesPageV1(input ModulesPageV1) ModulesPageV1 {
	result := input
	result.View = cloneControlViewV1(input.View)
	result.Items = cloneModuleSummariesV1(input.Items)
	result.NextCursor = cloneCursorPointerV1(input.NextCursor)
	return result
}

func cloneModuleDetailResultV1(
	input ModuleDetailResultV1,
) ModuleDetailResultV1 {
	result := input
	result.View = cloneControlViewV1(input.View)
	result.Module = cloneModuleDetailV1(input.Module)
	return result
}
