package controlapicontract

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// RevisionedDigestRefV1 identifies immutable, already-authoritative metadata.
// It contains neither the referenced body nor any authority material.
type RevisionedDigestRefV1 struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
	Digest   string `json:"digest"`
}

func (ref RevisionedDigestRefV1) Validate() error {
	if !validOpaqueIDV1(ref.ID) {
		return fmt.Errorf("controlapicontract: invalid revisioned reference ID")
	}
	if err := validateRevisionV1("reference revision", ref.Revision); err != nil {
		return err
	}
	if !moduleapi.ValidSHA256(ref.Digest) {
		return fmt.Errorf("controlapicontract: invalid revisioned reference digest")
	}
	return nil
}

// PublishedBasisRefV1 mirrors only the exact Current pointer identities needed
// for consistency checks. It is not the existing authoritative
// controlcontract.ControlSnapshotV1 and cannot publish a generation.
type PublishedBasisRefV1 struct {
	TenantID        string                `json:"tenant_id"`
	PointerRevision uint64                `json:"pointer_revision"`
	Control         RevisionedDigestRefV1 `json:"control"`
	Catalog         RevisionedDigestRefV1 `json:"catalog"`
}

func (basis PublishedBasisRefV1) Validate() error {
	if !validOpaqueIDV1(basis.TenantID) {
		return fmt.Errorf("controlapicontract: invalid published-basis tenant ID")
	}
	if err := validateRevisionV1(
		"published-basis pointer revision",
		basis.PointerRevision,
	); err != nil {
		return err
	}
	if err := basis.Control.Validate(); err != nil {
		return fmt.Errorf("controlapicontract: control reference: %w", err)
	}
	if err := basis.Catalog.Validate(); err != nil {
		return fmt.Errorf("controlapicontract: catalog reference: %w", err)
	}
	return nil
}

type ControlViewSectionKindV1 string

const (
	ViewSectionRunsV1       ControlViewSectionKindV1 = "RUNS"
	ViewSectionModulesV1    ControlViewSectionKindV1 = "MODULES"
	ViewSectionLearningV1   ControlViewSectionKindV1 = "LEARNING"
	ViewSectionUsageV1      ControlViewSectionKindV1 = "USAGE"
	ViewSectionUnknownV1    ControlViewSectionKindV1 = "UNKNOWN"
	ViewSectionAuditV1      ControlViewSectionKindV1 = "AUDIT"
	ViewSectionWorkspacesV1 ControlViewSectionKindV1 = "WORKSPACES"
)

func (kind ControlViewSectionKindV1) validate() error {
	switch kind {
	case ViewSectionRunsV1, ViewSectionModulesV1, ViewSectionLearningV1,
		ViewSectionUsageV1, ViewSectionUnknownV1, ViewSectionAuditV1,
		ViewSectionWorkspacesV1:
		return nil
	default:
		return fmt.Errorf(
			"controlapicontract: unsupported view section %q",
			kind,
		)
	}
}

// ControlViewSectionV1 is consistency and cardinality metadata only. The
// section body is obtained through separately authorized, bounded queries.
type ControlViewSectionV1 struct {
	Kind           ControlViewSectionKindV1 `json:"kind"`
	SourceRevision uint64                   `json:"source_revision"`
	SourceDigest   string                   `json:"source_digest"`
	ItemCount      uint32                   `json:"item_count"`
	Truncated      bool                     `json:"truncated"`
}

func (section ControlViewSectionV1) validate() error {
	if err := section.Kind.validate(); err != nil {
		return err
	}
	if err := validateRevisionV1(
		"view section source revision",
		section.SourceRevision,
	); err != nil {
		return err
	}
	if !moduleapi.ValidSHA256(section.SourceDigest) {
		return fmt.Errorf("controlapicontract: invalid view section digest")
	}
	return nil
}

// ControlViewSnapshotV1 is a scope-filtered, sanitized view identity. The
// distinct schema name intentionally avoids collision with the existing
// authoritative control-snapshot/v1 configuration wire.
type ControlViewSnapshotV1 struct {
	SchemaVersion        string                 `json:"schema_version"`
	Scope                ControlScopeV1         `json:"scope"`
	ScopeDigest          string                 `json:"scope_digest"`
	ObservedAtUnixMicros uint64                 `json:"observed_at_unix_micros"`
	Basis                PublishedBasisRefV1    `json:"basis"`
	Sections             []ControlViewSectionV1 `json:"sections"`
}

func NewControlViewSnapshotV1(
	input ControlViewSnapshotV1,
) (ControlViewSnapshotV1, []byte, string, error) {
	if input.SchemaVersion != ControlViewSnapshotSchemaVersionV1 {
		return ControlViewSnapshotV1{}, nil, "", fmt.Errorf(
			"controlapicontract: view snapshot schema_version must be %q",
			ControlViewSnapshotSchemaVersionV1,
		)
	}
	scope, _, scopeDigest, err := NewControlScopeV1(input.Scope)
	if err != nil {
		return ControlViewSnapshotV1{}, nil, "", err
	}
	if input.ScopeDigest != "" && input.ScopeDigest != scopeDigest {
		return ControlViewSnapshotV1{}, nil, "", fmt.Errorf(
			"controlapicontract: view snapshot scope digest mismatch",
		)
	}
	if err := validateTimeV1(
		"view observation time",
		input.ObservedAtUnixMicros,
	); err != nil {
		return ControlViewSnapshotV1{}, nil, "", err
	}
	if err := input.Basis.Validate(); err != nil {
		return ControlViewSnapshotV1{}, nil, "", err
	}
	if input.Basis.TenantID != scope.TenantID {
		return ControlViewSnapshotV1{}, nil, "", fmt.Errorf(
			"controlapicontract: view basis is outside its scope tenant",
		)
	}
	if len(input.Sections) == 0 || len(input.Sections) > MaxControlViewSectionsV1 {
		return ControlViewSnapshotV1{}, nil, "", fmt.Errorf(
			"controlapicontract: view must carry between 1 and %d section summaries",
			MaxControlViewSectionsV1,
		)
	}
	sections := append([]ControlViewSectionV1(nil), input.Sections...)
	sort.Slice(sections, func(left, right int) bool {
		return sections[left].Kind < sections[right].Kind
	})
	for index, section := range sections {
		if err := section.validate(); err != nil {
			return ControlViewSnapshotV1{}, nil, "", fmt.Errorf(
				"controlapicontract: view section %d: %w",
				index,
				err,
			)
		}
		if index > 0 && sections[index-1].Kind == section.Kind {
			return ControlViewSnapshotV1{}, nil, "", fmt.Errorf(
				"controlapicontract: duplicate view section %q",
				section.Kind,
			)
		}
	}
	frozen := input
	frozen.Scope = scope
	frozen.ScopeDigest = scopeDigest
	frozen.Sections = sections
	canonical, digest, err := freezeV1(
		frozen,
		controlViewSnapshotDigestDomainV1,
		MaxControlViewSnapshotWireBytesV1,
		512,
	)
	if err != nil {
		return ControlViewSnapshotV1{}, nil, "", err
	}
	return frozen, canonical, digest, nil
}

func RestoreControlViewSnapshotV1(
	canonical []byte,
	expectedDigest string,
) (ControlViewSnapshotV1, error) {
	if err := requireExactCanonicalV1(
		canonical,
		MaxControlViewSnapshotWireBytesV1,
		512,
	); err != nil {
		return ControlViewSnapshotV1{}, err
	}
	if err := verifyDigestV1(
		controlViewSnapshotDigestDomainV1,
		canonical,
		expectedDigest,
	); err != nil {
		return ControlViewSnapshotV1{}, err
	}
	var decoded ControlViewSnapshotV1
	if err := decodeStrictV1(canonical, &decoded); err != nil {
		return ControlViewSnapshotV1{}, err
	}
	restored, rebuilt, digest, err := NewControlViewSnapshotV1(decoded)
	if err != nil {
		return ControlViewSnapshotV1{}, err
	}
	if digest != expectedDigest || !bytes.Equal(rebuilt, canonical) {
		return ControlViewSnapshotV1{}, fmt.Errorf(
			"controlapicontract: view snapshot is not frozen canonically",
		)
	}
	return restored, nil
}

type ControlEventSourceV1 string

const (
	EventSourcePublishedBasisV1 ControlEventSourceV1 = "PUBLISHED_BASIS"
	EventSourceRunsV1           ControlEventSourceV1 = "RUNS"
	EventSourceModulesV1        ControlEventSourceV1 = "MODULES"
	EventSourceLearningV1       ControlEventSourceV1 = "LEARNING"
	EventSourceUsageV1          ControlEventSourceV1 = "USAGE"
	EventSourceAuditV1          ControlEventSourceV1 = "AUDIT"
)

func (source ControlEventSourceV1) validate() error {
	switch source {
	case EventSourcePublishedBasisV1, EventSourceRunsV1,
		EventSourceModulesV1, EventSourceLearningV1,
		EventSourceUsageV1, EventSourceAuditV1:
		return nil
	default:
		return fmt.Errorf(
			"controlapicontract: unsupported event source %q",
			source,
		)
	}
}

type ControlSourceRevisionV1 struct {
	Source   ControlEventSourceV1 `json:"source"`
	Revision uint64               `json:"revision"`
	Digest   string               `json:"digest"`
}

func (source ControlSourceRevisionV1) validate() error {
	if err := source.Source.validate(); err != nil {
		return err
	}
	if err := validateRevisionV1("event source revision", source.Revision); err != nil {
		return err
	}
	if !moduleapi.ValidSHA256(source.Digest) {
		return fmt.Errorf("controlapicontract: invalid event source digest")
	}
	return nil
}

// ControlEventCursorV1 is an invalidation-stream consistency cursor. It is
// never accepted as a list page token; gaps or boot changes require refetch.
type ControlEventCursorV1 struct {
	SchemaVersion      string                    `json:"schema_version"`
	BootID             string                    `json:"boot_id"`
	ScopeDigest        string                    `json:"scope_digest"`
	EventRevision      uint64                    `json:"event_revision"`
	ViewSnapshotDigest string                    `json:"view_snapshot_digest"`
	Sources            []ControlSourceRevisionV1 `json:"sources"`
	IssuedAtUnixMicros uint64                    `json:"issued_at_unix_micros"`
}

func NewControlEventCursorV1(
	input ControlEventCursorV1,
) (ControlEventCursorV1, []byte, string, error) {
	if input.SchemaVersion != ControlEventCursorSchemaVersionV1 {
		return ControlEventCursorV1{}, nil, "", fmt.Errorf(
			"controlapicontract: event cursor schema_version must be %q",
			ControlEventCursorSchemaVersionV1,
		)
	}
	if !validOpaqueIDV1(input.BootID) {
		return ControlEventCursorV1{}, nil, "", fmt.Errorf(
			"controlapicontract: invalid event cursor boot ID",
		)
	}
	if !moduleapi.ValidSHA256(input.ScopeDigest) ||
		!moduleapi.ValidSHA256(input.ViewSnapshotDigest) {
		return ControlEventCursorV1{}, nil, "", fmt.Errorf(
			"controlapicontract: invalid event cursor scope or view digest",
		)
	}
	if err := validateRevisionV1("event revision", input.EventRevision); err != nil {
		return ControlEventCursorV1{}, nil, "", err
	}
	if err := validateTimeV1("event issue time", input.IssuedAtUnixMicros); err != nil {
		return ControlEventCursorV1{}, nil, "", err
	}
	if len(input.Sources) == 0 ||
		len(input.Sources) > MaxControlSourceRevisionsV1 {
		return ControlEventCursorV1{}, nil, "", fmt.Errorf(
			"controlapicontract: event cursor must carry between 1 and %d source revisions",
			MaxControlSourceRevisionsV1,
		)
	}
	sources := append([]ControlSourceRevisionV1(nil), input.Sources...)
	sort.Slice(sources, func(left, right int) bool {
		return sources[left].Source < sources[right].Source
	})
	for index, source := range sources {
		if err := source.validate(); err != nil {
			return ControlEventCursorV1{}, nil, "", fmt.Errorf(
				"controlapicontract: event source %d: %w",
				index,
				err,
			)
		}
		if index > 0 && sources[index-1].Source == source.Source {
			return ControlEventCursorV1{}, nil, "", fmt.Errorf(
				"controlapicontract: duplicate event source %q",
				source.Source,
			)
		}
	}
	frozen := input
	frozen.Sources = sources
	canonical, digest, err := freezeV1(
		frozen,
		controlEventCursorDigestDomainV1,
		MaxControlEventCursorWireBytesV1,
		512,
	)
	if err != nil {
		return ControlEventCursorV1{}, nil, "", err
	}
	return frozen, canonical, digest, nil
}

func RestoreControlEventCursorV1(
	canonical []byte,
	expectedDigest string,
) (ControlEventCursorV1, error) {
	if err := requireExactCanonicalV1(
		canonical,
		MaxControlEventCursorWireBytesV1,
		512,
	); err != nil {
		return ControlEventCursorV1{}, err
	}
	if err := verifyDigestV1(
		controlEventCursorDigestDomainV1,
		canonical,
		expectedDigest,
	); err != nil {
		return ControlEventCursorV1{}, err
	}
	var decoded ControlEventCursorV1
	if err := decodeStrictV1(canonical, &decoded); err != nil {
		return ControlEventCursorV1{}, err
	}
	restored, rebuilt, digest, err := NewControlEventCursorV1(decoded)
	if err != nil {
		return ControlEventCursorV1{}, err
	}
	if digest != expectedDigest || !bytes.Equal(rebuilt, canonical) {
		return ControlEventCursorV1{}, fmt.Errorf(
			"controlapicontract: event cursor is not frozen canonically",
		)
	}
	return restored, nil
}
