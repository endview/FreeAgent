// Package controloverview defines the detached, transport-neutral facts used
// by the read-only Control Overview.  The package contains no authority,
// storage handle, canonical content, provider material, or executable input.
package controloverview

import (
	"errors"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
)

const (
	MaximumItemsV1      = uint16(12)
	MaximumWorkspacesV1 = uint16(256)

	UnknownKindModelV1            = "MODEL"
	UnknownKindActionV1           = "ACTION"
	UnknownKindChannelSendV1      = "CHANNEL_SEND"
	UnknownKindLearningProposalV1 = "LEARNING_PROPOSAL"
	UnknownKindLearningTaskV1     = "LEARNING_TASK"
)

var (
	ErrInvalidRequest = errors.New("controloverview: invalid request")
	ErrNotFound       = errors.New("controloverview: not found")
	ErrIntegrity      = errors.New("controloverview: integrity failure")
)

// WorkspaceRefV1 is the safe immutable identity needed by the selector.  It
// deliberately excludes policies, bindings, endpoints, identities and grants.
type WorkspaceRefV1 = corecontract.WorkspaceRef

type RunV1 struct {
	TenantID            string `json:"tenant_id"`
	WorkspaceID         string `json:"workspace_id"`
	RunID               string `json:"run_id"`
	State               string `json:"state"`
	Disposition         string `json:"disposition,omitempty"`
	Revision            uint64 `json:"revision"`
	CreatedAtUnixMicros uint64 `json:"created_at_unix_micros"`
	UpdatedAtUnixMicros uint64 `json:"updated_at_unix_micros"`
}

type UnknownV1 struct {
	Kind                string `json:"kind"`
	ResourceID          string `json:"resource_id"`
	TenantID            string `json:"tenant_id"`
	WorkspaceID         string `json:"workspace_id"`
	RunID               string `json:"run_id"`
	Revision            uint64 `json:"revision"`
	UpdatedAtUnixMicros uint64 `json:"updated_at_unix_micros"`
}

type LearningV1 struct {
	ProposalID          string `json:"proposal_id"`
	TenantID            string `json:"tenant_id"`
	WorkspaceID         string `json:"workspace_id"`
	Kind                string `json:"kind"`
	State               string `json:"state"`
	Revision            uint64 `json:"revision"`
	CreatedAtUnixMicros uint64 `json:"created_at_unix_micros"`
	UpdatedAtUnixMicros uint64 `json:"updated_at_unix_micros"`
}

type ModuleCandidateV1 struct {
	ReviewID              string `json:"review_id"`
	CandidateID           string `json:"candidate_id"`
	TenantID              string `json:"tenant_id"`
	WorkspaceID           string `json:"workspace_id,omitempty"`
	BindingTargetKind     string `json:"binding_target_kind"`
	CurrentInstanceID     string `json:"current_instance_id"`
	TargetInstanceID      string `json:"target_instance_id"`
	CurrentModuleID       string `json:"current_module_id"`
	CurrentExactVersion   string `json:"current_exact_version"`
	CurrentArtifactDigest string `json:"current_artifact_digest"`
	TargetModuleID        string `json:"target_module_id"`
	TargetExactVersion    string `json:"target_exact_version"`
	TargetArtifactDigest  string `json:"target_artifact_digest"`
	Conclusion            string `json:"conclusion"`
	CreatedAtUnixMicros   uint64 `json:"created_at_unix_micros"`
}

// UsageV1 preserves nil-is-unknown token and cost semantics.  It contains no
// raw receipt reference and no provider/model identity.
type UsageV1 struct {
	AttemptID            string  `json:"attempt_id"`
	RunID                string  `json:"run_id"`
	TenantID             string  `json:"tenant_id"`
	WorkspaceID          string  `json:"workspace_id"`
	Revision             uint64  `json:"revision"`
	InputTokens          *uint64 `json:"input_tokens"`
	CachedInputTokens    *uint64 `json:"cached_input_tokens"`
	UncachedInputTokens  *uint64 `json:"uncached_input_tokens"`
	OutputTokens         *uint64 `json:"output_tokens"`
	ReasoningTokens      *uint64 `json:"reasoning_tokens"`
	ReconciliationStatus string  `json:"reconciliation_status"`
	UpdatedAtUnixMicros  uint64  `json:"updated_at_unix_micros"`
}

// SnapshotV1 is created from one coherent Current Store read transaction.
// Every collection is non-nil, bounded and already scope-filtered; application
// authorization is still repeated for every projected resource.
type SnapshotV1 struct {
	Basis controlcontract.PublishedBasis
	// BasisSourceUpdatedAtUnixMicros is the exact immutable publication
	// projection clock before any rendered section item is considered. It is
	// application-only evidence and is not part of the HTTP response.
	BasisSourceUpdatedAtUnixMicros uint64 `json:"-"`
	SourceUpdatedAtUnixMicros      uint64

	Workspaces                []WorkspaceRefV1
	WorkspacesTruncated       bool
	Runs                      []RunV1
	RunsTruncated             bool
	Unknown                   []UnknownV1
	UnknownTruncated          bool
	Learning                  []LearningV1
	LearningTruncated         bool
	ModuleCandidates          []ModuleCandidateV1
	ModuleCandidatesTruncated bool
	Usage                     []UsageV1
	UsageTruncated            bool
}
