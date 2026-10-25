package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type ModelMessage struct {
	Role       string
	Content    string
	Name       string
	ToolCallID string
	ToolCalls  []ToolCall
	// ProviderItems preserves protocol-specific assistant output needed by
	// stateless APIs such as Responses. Chat adapters ignore it.
	ProviderItems []json.RawMessage `json:",omitempty"`
}

type ModelRequest struct {
	ModelID         string
	ModelRevision   string
	Purpose         string `json:",omitempty"`
	Messages        []ModelMessage
	Inputs          []string `json:",omitempty"`
	Tools           []ToolDefinition
	MaxOutputTokens int
	Memory          []MemoryUse    `json:",omitempty"`
	Extensions      []ExtensionUse `json:",omitempty"`
}

type ExtensionUse struct {
	Source    string `json:"source"`
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Phase     string `json:"phase,omitempty"`
	BlockID   string `json:"block_id,omitempty"`
	Placement string `json:"placement,omitempty"`
	Label     string `json:"label,omitempty"`
	SHA256    string `json:"sha256"`
	Bytes     int    `json:"bytes"`
	Chars     int    `json:"chars,omitempty"`
}

type ModelResponse struct {
	Content   string
	ToolCalls []ToolCall
	// ProviderItems is the bounded output-item sequence returned by a
	// Responses provider. It is replayed as input on the next request without
	// turning server-side activities into local tool calls.
	ProviderItems []json.RawMessage `json:",omitempty"`
	Vectors       [][]float64       `json:",omitempty"`
	Usage         *ModelUsage       `json:",omitempty"`
	Provider      *ProviderResponse `json:",omitempty"`
	Failure       *CallError        `json:",omitempty"`
}

// Provider metadata is bounded, credential-redacted protocol data, not evidence
// that any tool or external effect executed.
type ProviderResponse struct {
	Protocol          string                  `json:"protocol"`
	ID                string                  `json:"id,omitempty"`
	Model             string                  `json:"model,omitempty"`
	Created           int64                   `json:"created,omitempty"`
	SystemFingerprint string                  `json:"system_fingerprint,omitempty"`
	Streamed          bool                    `json:"streamed"`
	FinishReason      string                  `json:"finish_reason,omitempty"`
	RawFinishReason   string                  `json:"raw_finish_reason,omitempty"`
	UsageStatus       string                  `json:"usage_status"`
	ResponseStatus    string                  `json:"response_status,omitempty"`
	WebSearchCalls    []ProviderWebSearchCall `json:"web_search_calls,omitempty"`
}

type ProviderWebSearchCall struct {
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
	Action string `json:"action,omitempty"`
	Query  string `json:"query,omitempty"`
	URL    string `json:"url,omitempty"`
}

type ModelUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type ModelClient interface {
	Generate(context.Context, ModelRequest, func(string)) (ModelResponse, error)
}

// EmbeddingClient is optional. A model must explicitly declare embedding
// support before an extension can use this boundary.
type EmbeddingClient interface {
	Embed(context.Context, string, []string) (EmbeddingResponse, error)
}

type EmbeddingResponse struct {
	Vectors  [][]float64
	Usage    *ModelUsage
	Provider *ProviderResponse
}

type ModelFactory func(ModelConfig) (ModelClient, error)

type ToolEffect string

const (
	EffectRead   ToolEffect = "read"
	EffectModify ToolEffect = "modify"
)

type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Effect      ToolEffect
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
	Provider  *ProviderToolItem `json:",omitempty"`
}

type ProviderToolItem struct {
	Index int    `json:"index"`
	Type  string `json:"type"`
}

type ToolResult struct {
	Content    string
	IsError    bool
	Receipts   []ToolReceipt  `json:",omitempty"`
	Memory     []MemoryUse    `json:",omitempty"`
	Extensions []ExtensionUse `json:",omitempty"`
}

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

// Receipts are supplied by tool execution, never parsed from model text.
type ToolReceipt struct {
	Kind             string `json:"kind"`
	Path             string `json:"path,omitempty"`
	Status           string `json:"status"`
	BeforeHash       string `json:"before_hash,omitempty"`
	AfterHash        string `json:"after_hash,omitempty"`
	Command          string `json:"command,omitempty"`
	ResolvedFrom     string `json:"resolved_from,omitempty"`
	InstallationRoot string `json:"installation_root,omitempty"`
	WorkingDir       string `json:"working_dir,omitempty"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	Check            bool   `json:"check,omitempty"`
	Truncated        bool   `json:"truncated,omitempty"`
	Detail           string `json:"detail,omitempty"`
	URL              string `json:"url,omitempty"`
	HTTPStatus       int    `json:"http_status,omitempty"`
	Backend          string `json:"backend,omitempty"`
	NetworkMode      string `json:"network_mode,omitempty"`
	HostEscapeHatch  bool   `json:"host_escape_hatch,omitempty"`
	EnforcementState string `json:"enforcement_state,omitempty"`
	// GitMetadataCreated marks a sandboxed command that created .git in a
	// root that had none. Later host git runs may load what it wrote.
	GitMetadataCreated bool `json:"git_metadata_created,omitempty"`
}

type ToolRunner interface {
	Definitions() []ToolDefinition
	Prepare(context.Context, ToolCall) (PreparedTool, error)
}

type ApprovalItem struct {
	Kind   string            `json:"kind"`
	Target string            `json:"target"`
	Hash   string            `json:"sha256,omitempty"`
	Old    string            `json:"old,omitempty"`
	New    string            `json:"new,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

type PreparedTool struct {
	Effect  ToolEffect
	Preview string
	Items   []ApprovalItem
	Execute func(context.Context) (ToolResult, error)
	// Compound operations persist progress before dispatching the next effect.
	ExecuteRecorded func(context.Context, func(ToolResult) error) (ToolResult, error)
	Reject          func() ToolResult
	// Preauthorized local data operations have no external effects. Their
	// mutation and runtime result must commit in the same store transaction.
	executeTransaction func(context.Context, *sql.Tx) (ToolResult, error)
	preauthorized      bool
	workspaceFile      bool
	approvalRequired   bool
	userConfirmation   bool
	ruleAllowed        bool
	commandDecision    CommandRuleDecision
	controlProposal    bool
	controlOperationID string
	// release is used by scoped runtime tools that hold an OS root between
	// preparation and execution/approval. It is intentionally not persisted.
	release func()
}

type CallError struct {
	// Certain means a definitive response or proof of no dispatch was obtained.
	Certain    bool
	Message    string
	Category   string `json:"category,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Code       string `json:"code,omitempty"`
	// Retryable is diagnostic advice for a new, explicit request. It never
	// authorizes replay of this Attempt, especially if Certain is false.
	Retryable bool `json:"retryable"`
}

func (err *CallError) Error() string { return err.Message }

type EchoModel struct{}

func (EchoModel) Generate(ctx context.Context, request ModelRequest, emit func(string)) (ModelResponse, error) {
	if err := ctx.Err(); err != nil {
		return ModelResponse{}, err
	}
	for index := len(request.Messages) - 1; index >= 0; index-- {
		message := request.Messages[index]
		if message.Role == "user" {
			answer := "Echo: " + message.Content
			if emit != nil {
				emit(answer)
			}
			return ModelResponse{Content: answer}, nil
		}
	}
	return ModelResponse{}, errors.New("harness: echo model received no user message")
}
