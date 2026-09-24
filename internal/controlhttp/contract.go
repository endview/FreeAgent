package controlhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlsession"
)

const (
	BootstrapPathV1                         = "/control/bootstrap"
	SessionResumePathV1                     = "/control/session/resume"
	StaticUIRootPathV1                      = "/control/ui/"
	StaticUIIndexPathV1                     = "/control/ui/index.html"
	StaticUIAppCSSPathV1                    = "/control/ui/assets/app.css"
	StaticUIAppJSPathV1                     = "/control/ui/assets/app.js"
	StaticUIReactJSPathV1                   = "/control/ui/assets/react.js"
	StaticUITanStackQueryJSPathV1           = "/control/ui/assets/tanstack-query.js"
	OverviewPathV1                          = "/control/api/v1/overview"
	ModulesPathV1                           = "/control/api/v1/modules"
	ModuleUpgradeReviewsPathV1              = "/control/api/v1/module-upgrade-reviews"
	UnknownOutcomesPathV1                   = "/control/api/v1/unknown-outcomes"
	StoreManagementPathV1                   = "/control/api/v1/store-management"
	ModuleDisableDryRunPathV1               = "/control/api/v1/modules/disable/dry-run"
	ModuleDisableConfirmationPathV1         = "/control/api/v1/modules/disable/confirmation"
	ModuleDisableMutatePathV1               = "/control/api/v1/modules/disable/mutate"
	SessionCookieV1                         = "freeagent_control_session"
	CSRFHeaderV1                            = "X-FreeAgent-CSRF"
	ConfirmationHeaderV1                    = "X-FreeAgent-Confirmation"
	OperationEvaluationDigestHeaderV1       = "X-FreeAgent-Operation-Evaluation-Digest"
	ScopeKindHeaderV1                       = "X-FreeAgent-Scope-Kind"
	ScopeIDEncodingHeaderV1                 = "X-FreeAgent-Scope-ID-Encoding"
	ScopeIDEncodingBase64URLUTF8V1          = "base64url-utf8-v1"
	ModuleInstanceIDEncodingHeaderV1        = "X-FreeAgent-Module-Instance-ID-Encoding"
	ModuleInstanceIDEncodingBase64URLUTF8V1 = "base64url-utf8-v1"
	TenantIDHeaderV1                        = "X-FreeAgent-Tenant-ID"
	WorkspaceIDHeaderV1                     = "X-FreeAgent-Workspace-ID"

	BootstrapRequestSchemaV1                       = "control-bootstrap-exchange/v1"
	BootstrapResponseSchemaV1                      = "control-bootstrap-session/v1"
	BootstrapResponseSchemaV2                      = "control-bootstrap-session/v2"
	SessionResumeRequestSchemaV1                   = "control-session-resume/v1"
	SessionResumeResponseSchemaV1                  = "control-session-resumed/v1"
	OverviewHTTPResponseSchemaV1                   = "control-http-overview/v1"
	ModulesHTTPPageSchemaV1                        = "control-http-modules-page/v1"
	ModuleUpgradeReviewListSchemaVersionV1         = "control-module-upgrade-review-list/v1"
	ModuleUpgradeReviewDetailSchemaVersionV1       = "control-module-upgrade-review-detail/v1"
	UnknownOutcomeListSchemaVersionV1              = "control-unknown-outcome-list/v1"
	UnknownOutcomeDetailSchemaVersionV1            = "control-unknown-outcome-detail/v1"
	StoreManagementSchemaVersionV1                 = "control-store-management/v1"
	ModuleDisableConfirmationResultSchemaVersionV1 = "control-module-disable-confirmation-result/v1"
	ModuleDisableMutationResultSchemaVersionV1     = "control-module-disable-mutation-result/v1"

	MaximumRequestTargetBytesV1     = 8 << 10
	MaximumRequestHeaderBytesV1     = 16 << 10
	MaximumRequestHeaderCountV1     = 64
	MaximumBootstrapBodyBytesV1     = 4 << 10
	MaximumSessionResumeBodyBytesV1 = 4 << 10
	MaximumOperationBodyBytesV1     = 1 << 20
	MaximumResponseBodyBytesV1      = 1 << 20
	MaximumStaticAssetBytesV1       = 1 << 20
	MaximumCursorTokenBytesV1       = 4096
	MaximumTransportAdmissionsV1    = 32

	ReadHeaderTimeoutV1 = 5 * time.Second
	RequestTimeoutV1    = 30 * time.Second
	IdleTimeoutV1       = 60 * time.Second

	ConfirmationProofBytesV1           = 32
	MaximumConfirmationProofLifetimeV1 = 2 * time.Minute
)

var (
	ErrInvalidConfiguration = errors.New("controlhttp: invalid configuration")

	// ErrModuleDisableConfirmationRejectedV1 is deliberately non-enumerating:
	// adapters map a missing, expired, consumed, or differently bound proof to
	// this one transport-neutral classification.
	ErrModuleDisableConfirmationRejectedV1 = errors.New(
		"controlhttp: module Disable confirmation rejected",
	)

	// ErrModuleDisableIdempotencyConflictV1 identifies one durable identity
	// already bound to a different exact semantic Request. Implementations must
	// not include the raw key, Request, proof, or Store detail in the error.
	ErrModuleDisableIdempotencyConflictV1 = errors.New(
		"controlhttp: module Disable idempotency conflict",
	)
)

// ModulesServiceV1 is the complete application dependency of the first read
// transport. Implementations remain responsible for resource-level scope
// filtering and repeated authorization checks.
type ModulesServiceV1 interface {
	ListModulesV1(
		context.Context,
		controlapp.ListModulesInputV1,
	) (controlapp.ModulesPageV1, error)
	GetModuleV1(
		context.Context,
		controlapp.GetModuleInputV1,
	) (controlapp.ModuleDetailResultV1, error)
}

// ModuleUpgradeReviewServiceV1 is the P4 read-only dependency. It exposes
// only bounded, tenant-scoped projections; it has no mutation or Apply method.
type ModuleUpgradeReviewServiceV1 interface {
	ListModuleUpgradeReviewsV1(
		context.Context,
		controlapp.ModuleUpgradeReviewListInputV1,
	) (controlapp.ModuleUpgradeReviewListResultV1, error)
	GetModuleUpgradeReviewV1(
		context.Context,
		controlapp.GetModuleUpgradeReviewInputV1,
	) (controlapp.ModuleUpgradeReviewDetailResultV1, error)
}

type ControlManagementServiceV1 interface {
	ListUnknownOutcomesV1(
		context.Context,
		controlapp.UnknownOutcomeListInputV1,
	) (controlapp.UnknownOutcomeListResultV1, error)
	GetUnknownOutcomeV1(
		context.Context,
		controlapp.UnknownOutcomeDetailInputV1,
	) (controlapp.UnknownOutcomeDetailResultV1, error)
	GetStoreManagementV1(
		context.Context,
		controlapp.StoreManagementInputV1,
	) (controlapp.StoreManagementResultV1, error)
}

// OverviewServiceV1 is the complete application dependency of the first Web
// shell read. Implementations retain responsibility for per-resource scope
// filtering and entry/resource/final live authorization checks.
type OverviewServiceV1 interface {
	GetOverviewV1(
		context.Context,
		controlapp.GetOverviewInputV1,
	) (controlapp.OverviewResultV1, error)
}

// StaticAssetV1 is the detached result of resolving one exact Web shell
// asset. Bytes is caller-owned and Size and SHA256 describe those exact bytes.
// The HTTP package intentionally does not depend on the embedding package.
type StaticAssetV1 struct {
	Path      string
	MediaType string
	Size      uint64
	SHA256    string
	Bytes     []byte
}

// StaticAssetResolverV1 resolves only the canonical relative names supplied
// by the HTTP route allowlist. Implementations must not clean paths, redirect,
// synthesize an SPA fallback, or perform authorization.
type StaticAssetResolverV1 interface {
	ResolveStaticAssetV1(string) (StaticAssetV1, bool, error)
}

// ModuleDisableDryRunServiceV1 is the sole effect-free MODULE_DISABLE
// application dependency exposed by this transport. Implementations must not
// mutate the Store, stage artifacts, reserve an operation, or dispatch an
// external effect.
type ModuleDisableDryRunServiceV1 interface {
	DryRunModuleDisableV1(
		context.Context,
		controlapp.ModuleDisableDryRunInputV1,
	) (controlapp.ModuleDisableDryRunResultV1, error)
}

// IssueModuleDisableConfirmationInputV1 contains the complete stable
// transport facts for the first narrow confirmation issue. Authorization is
// the exact live Permit admitted for this request; implementations must use
// its AuthorizeCurrentV1 boundary rather than retain or replace it. Raw
// idempotency material is never passed beyond HTTP.
type IssueModuleDisableConfirmationInputV1 struct {
	Authorization        *controlsession.PermitV1
	Scope                controlapicontract.ControlScopeV1
	ExpectedPointer      controlapicontract.ExpectedResourceRefV1
	Body                 controlapp.ModuleDisableDryRunBodyV1
	IdempotencyKeyDigest string
}

// IssueModuleDisableConfirmationResultV1 is process-local service output.
// ConfirmationProof is the sole raw proof copy and is encoded as canonical
// base64url only by the HTTP response adapter; it must never be persisted or
// included in an error.
type IssueModuleDisableConfirmationResultV1 struct {
	SchemaVersion string

	Request       controlapicontract.ControlOperationRequestV1
	RequestDigest string

	Evaluation       controlapp.ModuleDisableEvaluationV1
	EvaluationDigest string

	Statement       controlapicontract.ControlConfirmationStatementV1
	StatementDigest string

	ConfirmationProof   []byte
	ExpiresAtUnixMicros uint64
}

// ModuleDisableConfirmationServiceV1 evaluates and issues one short-lived
// proof. It cannot publish Control/Catalog or create a durable receipt.
type ModuleDisableConfirmationServiceV1 interface {
	IssueModuleDisableConfirmationV1(
		context.Context,
		IssueModuleDisableConfirmationInputV1,
	) (IssueModuleDisableConfirmationResultV1, error)
}

// MutateModuleDisableInputV1 contains only the exact stable Request inputs and
// an optional current proof. A nil proof is intentional: the implementation
// must construct the final Request and perform durable receipt resolution
// before deciding whether a proof is required. A proof may be retained only
// by the process-local confirmation authority for the duration of Claim.
type MutateModuleDisableInputV1 struct {
	Authorization             *controlsession.PermitV1
	Scope                     controlapicontract.ControlScopeV1
	ExpectedPointer           controlapicontract.ExpectedResourceRefV1
	Body                      controlapp.ModuleDisableDryRunBodyV1
	IdempotencyKeyDigest      string
	OperationEvaluationDigest string
	ConfirmationProof         []byte
}

// MutateModuleDisableResultV1 is the stable safe success projection. The
// first SQLite-only slice admits only NO_CHANGE or APPLIED; HTTP validates
// that closed matrix and never serializes UNKNOWN as success.
type MutateModuleDisableResultV1 struct {
	SchemaVersion string                                       `json:"schema_version"`
	Request       controlapicontract.ControlOperationRequestV1 `json:"request"`
	RequestDigest string                                       `json:"request_digest"`
	Receipt       controlapicontract.ControlOperationReceiptV1 `json:"receipt"`
	ReceiptDigest string                                       `json:"receipt_digest"`
}

// ModuleDisableMutationServiceV1 owns lookup-before-proof and the sole atomic
// Store mutation boundary. It must return an exact durable receipt for a hit,
// claim and finally reauthorize only on a miss, and never surface UNKNOWN for
// this pure SQLite slice.
type ModuleDisableMutationServiceV1 interface {
	MutateModuleDisableV1(
		context.Context,
		MutateModuleDisableInputV1,
	) (MutateModuleDisableResultV1, error)
}

// ModulesCursorCodecV1 authenticates opaque page tokens. Decode must reject
// modified, wrong-boot, or otherwise invalid tokens; a decoded cursor alone
// never grants authority. Encode must be deterministic for one cursor and
// process boot.
type ModulesCursorCodecV1 interface {
	EncodeModulesCursorV1(controlapp.DecodedModulesCursorV1) (string, error)
	DecodeModulesCursorV1(string) (controlapp.DecodedModulesCursorV1, error)
	// IsStaleModulesCursorErrorV1 classifies structurally valid tokens whose
	// bounded envelope names a boot that is no longer current. This
	// non-authorizing hint returns no payload; same-boot authentication,
	// format, and semantic failures are invalid.
	IsStaleModulesCursorErrorV1(error) bool
}

// ConfigV1 contains only composition-established dependencies. NewHandlerV1
// does not bind or open a listener.
type ConfigV1 struct {
	Authority                 string
	Registry                  *controlsession.RegistryV1
	StaticAssets              StaticAssetResolverV1
	Overview                  OverviewServiceV1
	Modules                   ModulesServiceV1
	ModuleUpgradeReviews      ModuleUpgradeReviewServiceV1
	Management                ControlManagementServiceV1
	ModuleDisableDryRun       ModuleDisableDryRunServiceV1
	ModuleDisableConfirmation ModuleDisableConfirmationServiceV1
	ModuleDisableMutation     ModuleDisableMutationServiceV1
	Cursor                    ModulesCursorCodecV1
	Now                       func() time.Time
	Entropy                   io.Reader
}

// ServerPolicyV1 is the exact server-side timeout/header policy that the
// composition root must copy onto the dedicated http.Server.
type ServerPolicyV1 struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
}

func RecommendedServerPolicyV1() ServerPolicyV1 {
	return ServerPolicyV1{
		ReadHeaderTimeout: ReadHeaderTimeoutV1,
		ReadTimeout:       RequestTimeoutV1,
		WriteTimeout:      RequestTimeoutV1,
		IdleTimeout:       IdleTimeoutV1,
		MaxHeaderBytes:    MaximumRequestHeaderBytesV1,
	}
}

// ApplyTo copies the frozen policy onto an otherwise composition-owned server.
// It deliberately does not set Handler, Addr, TLS, listeners, or lifecycle
// callbacks.
func (policy ServerPolicyV1) ApplyTo(server *http.Server) error {
	if server == nil || policy != RecommendedServerPolicyV1() {
		return ErrInvalidConfiguration
	}
	server.ReadHeaderTimeout = policy.ReadHeaderTimeout
	server.ReadTimeout = policy.ReadTimeout
	server.WriteTimeout = policy.WriteTimeout
	server.IdleTimeout = policy.IdleTimeout
	server.MaxHeaderBytes = policy.MaxHeaderBytes
	return nil
}
