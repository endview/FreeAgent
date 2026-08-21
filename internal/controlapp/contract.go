package controlapp

import (
	"context"
	"errors"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/moduledisablecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ModulesCursorSchemaVersionV1 = "control-modules-cursor/v1"
	ModulesPageSchemaVersionV1   = "control-modules-page/v1"
	ModuleDetailSchemaVersionV1  = "control-module-detail/v1"
	ModulesSortVersionV1         = "control-modules-instance-id-binary/v1"
	DefaultModulesPageLimitV1    = uint16(50)
	MaximumModulesPageLimitV1    = uint16(controlapicontract.MaxControlPageSizeV1)

	ModuleBindingTargetProfileV1                  = moduledisablecontract.ModuleBindingTargetProfileV1
	ModuleBindingTargetWorkspaceChannelEndpointV1 = moduledisablecontract.ModuleBindingTargetWorkspaceChannelEndpointV1
)

var (
	ErrInvalidRequest       = errors.New("controlapp: invalid request")
	ErrSessionExpired       = errors.New("controlapp: session expired")
	ErrForbidden            = errors.New("controlapp: forbidden")
	ErrNotFound             = errors.New("controlapp: not found")
	ErrCursorInvalid        = errors.New("controlapp: cursor invalid")
	ErrCursorStale          = errors.New("controlapp: cursor stale")
	ErrPreconditionRequired = errors.New("controlapp: precondition required")
	ErrRevisionConflict     = errors.New("controlapp: revision conflict")
	ErrConflict             = errors.New("controlapp: conflict")
	ErrStoreBusy            = errors.New("controlapp: store busy")
	ErrStoreUnavailable     = errors.New("controlapp: store unavailable")
	ErrIntegrityFailure     = errors.New("controlapp: integrity failure")
	ErrResourceExhausted    = errors.New("controlapp: resource exhausted")
	ErrCancelled            = errors.New("controlapp: cancelled")
)

// PublishedBasisReaderV1 is the complete Current Store dependency of W6-1A.
// An online wiring adapter may delegate only these two methods to the already
// open Current Store and map Store-specific failures to the neutral sentinels
// above. The interface exposes no SQL handle, observer, transaction, writer,
// or private executor.
type PublishedBasisReaderV1 interface {
	LoadPublishedBasis(
		ctx context.Context,
		tenantID string,
	) (
		controlcontract.PublishedBasis,
		controlcontract.ControlSnapshot,
		controlcontract.CatalogGeneration,
		error,
	)
	VerifyPublishedControlCatalogClosureV1(
		ctx context.Context,
		control controlcontract.ControlSnapshot,
		catalog controlcontract.CatalogGeneration,
	) error
}

// AuthorizationContextV1 is the single server-owned authorization fact passed
// to an application service. A successful controlsession.RegistryV1.Admit
// permit implements this interface directly. Keeping Session, Digest, and
// Allows on one value prevents callers from accidentally pairing metadata from
// one session with the scope ceiling of another. A cursor never implements
// this interface and never grants authority.
type AuthorizationContextV1 interface {
	Session() controlapicontract.ControlSessionV1
	Digest() string
	Allows(controlapicontract.ControlScopeV1) bool
}

type ListModulesInputV1 struct {
	Authorization AuthorizationContextV1
	Scope         controlapicontract.ControlScopeV1
	Page          controlapicontract.PageQueryV1
	Cursor        *DecodedModulesCursorV1
	ObservedAt    uint64
}

type GetModuleInputV1 struct {
	Authorization AuthorizationContextV1
	Scope         controlapicontract.ControlScopeV1
	InstanceID    string
	ObservedAt    uint64
}

type ModuleBindingTargetKindV1 = moduledisablecontract.ModuleBindingTargetKindV1

// ModuleBindingTargetV1 identifies only the consumer-owned target. A PROFILE
// deliberately has no inferred Workspace ownership.
type ModuleBindingTargetV1 = moduledisablecontract.ModuleBindingTargetV1

// ModuleBindingSummaryV1 contains immutable references, never their content.
// PortBindingIndex is the semantic ordinal inside the target's exact Port
// subsequence. A Channel Endpoint has exactly one binding and index zero.
type ModuleBindingSummaryV1 struct {
	Target              ModuleBindingTargetV1   `json:"target"`
	Port                moduleapi.PortRef       `json:"port"`
	PortBindingIndex    uint16                  `json:"port_binding_index"`
	ConfigRef           string                  `json:"config_ref"`
	AuthorityCeilingRef string                  `json:"authority_ceiling_ref"`
	StaticContextRefs   []string                `json:"static_context_refs"`
	FailurePolicy       moduleapi.FailurePolicy `json:"failure_policy"`
}

type ModuleSummaryV1 struct {
	InstanceID          string                   `json:"instance_id"`
	ModuleID            string                   `json:"module_id"`
	ExactVersion        string                   `json:"exact_version"`
	ArtifactDigest      string                   `json:"artifact_digest"`
	ExecutionClass      moduleapi.ExecutionClass `json:"execution_class"`
	AdapterIdentity     string                   `json:"adapter_identity"`
	ActivationRevision  uint64                   `json:"activation_revision"`
	Provides            []moduleapi.PortRef      `json:"provides"`
	VisibleBindingCount uint32                   `json:"visible_binding_count"`
}

type ModuleDetailV1 struct {
	Summary  ModuleSummaryV1          `json:"summary"`
	Bindings []ModuleBindingSummaryV1 `json:"bindings"`
}

// DecodedModulesCursorV1 is authenticated metadata consumed and produced by
// the application service. W6-1B owns opaque token serialization and HMAC.
// This type is not a bearer credential and never grants scope by itself.
type DecodedModulesCursorV1 struct {
	SchemaVersion         string                                     `json:"schema_version"`
	BootID                string                                     `json:"boot_id"`
	PrincipalID           string                                     `json:"principal_id"`
	AuthorizationRevision uint64                                     `json:"authorization_revision"`
	ScopeSetDigest        string                                     `json:"scope_set_digest"`
	Scope                 controlapicontract.ControlScopeV1          `json:"scope"`
	ScopeDigest           string                                     `json:"scope_digest"`
	Collection            controlapicontract.ControlPageCollectionV1 `json:"collection"`
	FilterDigest          string                                     `json:"filter_digest"`
	SortVersion           string                                     `json:"sort_version"`
	SourceRevision        uint64                                     `json:"source_revision"`
	SourceDigest          string                                     `json:"source_digest"`
	ViewSnapshotDigest    string                                     `json:"view_snapshot_digest"`
	ObservedAtUnixMicros  uint64                                     `json:"observed_at_unix_micros"`
	LastInstanceID        string                                     `json:"last_instance_id"`
	PositionDigest        string                                     `json:"position_digest"`
}

type ModulesPageV1 struct {
	// SchemaVersion identifies this transport-neutral application projection;
	// W6-1B still owns the distinct HTTP response envelope.
	SchemaVersion      string                                   `json:"schema_version"`
	PublishedPointer   controlapicontract.ExpectedResourceRefV1 `json:"published_pointer"`
	Basis              controlapicontract.PublishedBasisRefV1   `json:"basis"`
	View               controlapicontract.ControlViewSnapshotV1 `json:"view"`
	ViewSnapshotDigest string                                   `json:"view_snapshot_digest"`
	SourceRevision     uint64                                   `json:"source_revision"`
	SourceDigest       string                                   `json:"source_digest"`
	SortVersion        string                                   `json:"sort_version"`
	FilterDigest       string                                   `json:"filter_digest"`
	Items              []ModuleSummaryV1                        `json:"items"`
	HasMore            bool                                     `json:"has_more"`
	NextCursor         *DecodedModulesCursorV1                  `json:"next_cursor,omitempty"`
	ProjectionDigest   string                                   `json:"projection_digest"`
	StrongETag         string                                   `json:"strong_etag"`
}

type ModuleDetailResultV1 struct {
	// SchemaVersion identifies this transport-neutral application projection;
	// W6-1B still owns the distinct HTTP response envelope.
	SchemaVersion      string                                   `json:"schema_version"`
	PublishedPointer   controlapicontract.ExpectedResourceRefV1 `json:"published_pointer"`
	Basis              controlapicontract.PublishedBasisRefV1   `json:"basis"`
	View               controlapicontract.ControlViewSnapshotV1 `json:"view"`
	ViewSnapshotDigest string                                   `json:"view_snapshot_digest"`
	SourceRevision     uint64                                   `json:"source_revision"`
	SourceDigest       string                                   `json:"source_digest"`
	Module             ModuleDetailV1                           `json:"module"`
	ProjectionDigest   string                                   `json:"projection_digest"`
	StrongETag         string                                   `json:"strong_etag"`
}
