// Package s3eval contains the opt-in S3-C real-use experiment harness.
//
// It is an application-side observer only: callers inject the existing
// Composite Chat entry point and Current Store projection. The package owns no
// Runtime, Store, Scheduler, queue, model authority, retry, or production
// registration.
package s3eval

import (
	"context"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/loopapi"
)

const WorkspaceCount = 3

// ChatFunc is the already-composed Composite Chat entry point supplied by the
// caller. S3 evaluation cannot construct or replace the production Runtime.
type ChatFunc func(
	context.Context,
	localchat.ChatInput,
) (localchat.CompositeChatResult, error)

// CompositeUsageReader exposes the two read-only Current Store projections
// needed by the experiment. Implementations must not synthesize missing Usage
// or terminal-result facts.
type CompositeUsageReader interface {
	GetCompositeFamilyUsageProjection(
		context.Context,
		string,
	) (currentstore.CompositeFamilyUsageProjectionV1, error)
	GetTerminalRunResult(
		context.Context,
		string,
	) (currentstore.TerminalRunResult, error)
}

// WorkspaceTask identifies one of the exactly three concurrently submitted
// Workspace tasks. Workspace identity remains the one in ChatInput.
type WorkspaceTask struct {
	ChatInput localchat.ChatInput
}

// ExperimentInput is deliberately fixed at three Workspaces so an accidental
// partial run cannot be reported as the S3-C fairness experiment.
type ExperimentInput struct {
	Tasks [WorkspaceCount]WorkspaceTask
}

// DispositionFact is a detached application view of one bounded Loop result.
type DispositionFact struct {
	RunID         string              `json:"run_id"`
	Disposition   loopapi.Disposition `json:"disposition"`
	FrameRevision uint64              `json:"frame_revision"`
	ReasonCode    string              `json:"reason_code"`
}

// TokenTotalsReport is the stable JSON view of family token totals. Each nil
// field remains UNKNOWN; a non-nil zero is an observed zero.
type TokenTotalsReport struct {
	Input         *uint64 `json:"input_tokens"`
	CachedInput   *uint64 `json:"cached_input_tokens"`
	UncachedInput *uint64 `json:"uncached_input_tokens"`
	Output        *uint64 `json:"output_tokens"`
	Reasoning     *uint64 `json:"reasoning_tokens"`
}

// TokenReport preserves the Current Store nil-is-UNKNOWN token semantics.
// CacheHitRatio is present only when a non-zero denominator is known.
type TokenReport struct {
	Totals        TokenTotalsReport `json:"totals"`
	CacheHitRatio *float64          `json:"cache_hit_ratio"`
}

// AttemptFact contains only already-persisted dispatch facts and derived
// timing/order. Elapsed is UpdatedAt-CreatedAt; it is not provider latency.
type AttemptFact struct {
	ServiceOrder  uint64                          `json:"service_order"`
	WorkspaceID   string                          `json:"workspace_id"`
	RootRunID     string                          `json:"root_run_id"`
	RunID         string                          `json:"run_id"`
	Role          corecontract.CompositeRunRoleV1 `json:"role"`
	SlotID        string                          `json:"slot_id"`
	AttemptID     string                          `json:"attempt_id"`
	LogicalStepID string                          `json:"logical_step_id"`
	State         corecontract.ModelAttemptState  `json:"state"`
	Provider      string                          `json:"provider"`
	Model         string                          `json:"model"`
	RequestDigest string                          `json:"request_digest"`
	CreatedAt     time.Time                       `json:"created_at"`
	UpdatedAt     time.Time                       `json:"updated_at"`
	Elapsed       time.Duration                   `json:"elapsed"`
	Tokens        corecontract.UsageTokens        `json:"tokens"`
}

// ResultFact is the content-verified successful MODEL_RESULT for one
// Composite Run. AssistantText is the normalized provider answer only; raw
// receipts, request headers and reasoning traces are never copied into S3-C
// evidence. ReviewerVerdict is present only for the Reviewer role and is
// restored from the canonical assistant text already accepted by Core.
type ResultFact struct {
	RunID           string                          `json:"run_id"`
	Role            corecontract.CompositeRunRoleV1 `json:"role"`
	SlotID          string                          `json:"slot_id"`
	AttemptID       string                          `json:"attempt_id"`
	ResultDigest    string                          `json:"result_digest"`
	AssistantText   string                          `json:"assistant_text"`
	ReviewerVerdict *corecontract.ReviewVerdictV1   `json:"reviewer_verdict,omitempty"`
}

// FamilyReport records one concurrent call. WallElapsed is measured outside
// Chat with Go's monotonic clock component and remains distinct from persisted
// Attempt timestamps. WallElapsed and AttemptFact.Elapsed serialize as integer
// nanoseconds in v1; changing units requires a new report schema.
type FamilyReport struct {
	WorkspaceID string            `json:"workspace_id"`
	RequestID   string            `json:"request_id"`
	RootRunID   string            `json:"root_run_id"`
	StartedAt   time.Time         `json:"started_at"`
	FinishedAt  time.Time         `json:"finished_at"`
	WallElapsed time.Duration     `json:"wall_elapsed"`
	Children    []DispositionFact `json:"children"`
	Reviewer    *DispositionFact  `json:"reviewer"`
	Root        DispositionFact   `json:"root"`
	Reply       string            `json:"reply"`
	Failure     string            `json:"failure"`
	Tokens      TokenReport       `json:"tokens"`
	Attempts    []AttemptFact     `json:"attempts"`
	Results     []ResultFact      `json:"results"`
	Error       string            `json:"error"`
}

// WorkspaceFairnessFact is kept in ExperimentInput order. A nil first order
// means that no persisted model Attempt served that Workspace.
type WorkspaceFairnessFact struct {
	WorkspaceID       string  `json:"workspace_id"`
	ServiceCount      uint64  `json:"service_count"`
	FirstServiceOrder *uint64 `json:"first_service_order"`
}

// FairnessReport describes observed dispatch service only. It does not judge
// response quality or infer work that has no persisted Attempt.
type FairnessReport struct {
	Workspaces                  []WorkspaceFairnessFact `json:"workspaces"`
	JainIndex                   *float64                `json:"jain_index"`
	FirstServedWorkspace        string                  `json:"first_served_workspace"`
	LongestConsecutiveWorkspace string                  `json:"longest_consecutive_workspace"`
	LongestConsecutiveCount     uint64                  `json:"longest_consecutive_count"`
	Starvation                  bool                    `json:"starvation"`
	StarvedWorkspaces           []string                `json:"starved_workspaces"`
}

// ExperimentReport contains family reports in input order and attempts in
// stable persisted service order (CreatedAt, AttemptID, WorkspaceID).
type ExperimentReport struct {
	Families     []FamilyReport `json:"families"`
	ServiceOrder []AttemptFact  `json:"service_order"`
	Fairness     FairnessReport `json:"fairness"`
}
