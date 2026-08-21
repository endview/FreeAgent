package mcpstdio

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	preparedPayloadSchemaV1 = "mcp-prepared-tool-call/v1"
	resultEnvelopeSchemaV1  = "freeagent.mcp-tool-result/v1"
	receiptSchemaV1         = "freeagent.mcp-tool-receipt/v1"
	// The official SDK synthesizes this JSON-RPC-shaped error locally when a
	// connection closes while a call is in flight. It is not a server response
	// and therefore cannot prove that tools/call had no effect.
	sdkClientClosingCode = -32003
)

var (
	ErrInvalidAdapter    = errors.New("mcpstdio: invalid adapter")
	ErrGenericInvocation = errors.New("mcpstdio: generic invocation is forbidden")
	ErrDescribe          = errors.New("mcpstdio: Describe failed")
	ErrPrepare           = errors.New("mcpstdio: Prepare failed")
	ErrExecute           = errors.New("mcpstdio: execution failed after dispatch")
)

// Adapter is an immutable, artifact-scoped MCP stdio Tool adapter. It holds no
// Workspace, Run, request or session state; each Describe and execution uses
// one bounded subprocess session, while Prepare remains a pure local function.
type Adapter struct {
	provider moduleapi.ActivatedModuleRef
	plan     launchPlan
}

var _ modulehost.ModuleInvoker = (*Adapter)(nil)
var _ moduleapi.ActionProviderV1 = (*Adapter)(nil)
var _ modulehost.ActionExecutor = (*Adapter)(nil)

// New restores one already digest-verified artifact descriptor and closes it
// over one exact LOCAL_PROCESS provider. Construction performs no process
// launch and no protocol I/O.
func New(
	provider moduleapi.ActivatedModuleRef,
	artifactDirectory string,
	descriptorPath string,
	descriptorCanonical []byte,
) (*Adapter, error) {
	return NewContext(
		context.Background(),
		provider,
		artifactDirectory,
		descriptorPath,
		descriptorCanonical,
	)
}

// NewContext is New with cancellation for the bounded descriptor/executable
// closure check performed by a selected lazy loader.
func NewContext(
	ctx context.Context,
	provider moduleapi.ActivatedModuleRef,
	artifactDirectory string,
	descriptorPath string,
	descriptorCanonical []byte,
) (*Adapter, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidAdapter)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: context: %w", ErrInvalidAdapter, err)
	}
	if err := provider.Validate(); err != nil {
		return nil, fmt.Errorf("%w: provider: %v", ErrInvalidAdapter, err)
	}
	if provider.ExecutionClass != moduleapi.ExecutionLocalProcess ||
		provider.AdapterIdentity != AdapterIdentityV1 {
		return nil, fmt.Errorf("%w: provider is not the exact MCP stdio adapter", ErrInvalidAdapter)
	}
	plan, err := parseLaunchPlan(
		ctx,
		artifactDirectory,
		descriptorPath,
		bytes.Clone(descriptorCanonical),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAdapter, err)
	}
	return &Adapter{provider: provider, plan: plan}, nil
}

// Invoke is deliberately unusable. MCP tools can be called only through the
// private ActionExecutor after Gateway consumes the persisted one-shot permit.
func (*Adapter) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return modulehost.InvocationResult{}, ErrGenericInvocation
}

// Describe performs admission-time discovery only. Server instructions,
// annotations, titles, icons and output schemas are ignored and cannot grant
// authority. Only descriptor-mapped tools that fit FreeAgent's frozen Action
// schema subset are returned.
func (adapter *Adapter) Describe(
	ctx context.Context,
	request moduleapi.ActionDescribeRequestV1,
) (definitions []moduleapi.ActionDefinitionV1, returnErr error) {
	if err := validateAdapterContext(adapter, ctx, ErrDescribe); err != nil {
		return nil, err
	}
	frozenRequest, _, err := moduleapi.NewActionDescribeRequestV1(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request: %v", ErrDescribe, err)
	}
	if !bytes.Equal(frozenRequest.Parameters, []byte(`{}`)) {
		return nil, fmt.Errorf("%w: parameters must be an empty object in the first MCP slice", ErrDescribe)
	}

	session, err := adapter.connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: initialize: %w", ErrDescribe, err)
	}
	defer func() {
		if cleanupErr := session.Close(); cleanupErr != nil {
			definitions = nil
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("%w: session cleanup: %v", ErrDescribe, cleanupErr),
			)
		}
	}()
	discoveryContext, cancel := context.WithTimeout(ctx, adapter.plan.startupTimeout)
	defer cancel()
	remoteTools, err := listAllTools(discoveryContext, session)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: tools/list: %w",
			ErrDescribe,
			errors.Join(err, discoveryContext.Err()),
		)
	}

	definitions = make([]moduleapi.ActionDefinitionV1, 0, len(adapter.plan.tools))
	for _, mapping := range adapter.plan.tools {
		tool, present := remoteTools[mapping.ProviderActionID]
		if !present {
			return nil, fmt.Errorf("%w: configured MCP tool %q is absent", ErrDescribe, mapping.ProviderActionID)
		}
		schemaJSON, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("%w: tool %q input schema: %v", ErrDescribe, mapping.ProviderActionID, err)
		}
		inputSchema, err := moduleapi.CanonicalizeActionInputSchemaV1(schemaJSON)
		if err != nil {
			return nil, fmt.Errorf("%w: tool %q input schema is outside action.provider/v1: %v", ErrDescribe, mapping.ProviderActionID, err)
		}
		description := tool.Description
		if description == "" {
			description = "Invoke the configured MCP tool " + mapping.ProviderActionID + "."
		}
		if !utf8.ValidString(description) || description != moduleapi.CanonicalText(description) {
			return nil, fmt.Errorf("%w: tool %q description is not canonical UTF-8", ErrDescribe, mapping.ProviderActionID)
		}
		definition, _, err := moduleapi.NewActionDefinitionV1(
			moduleapi.ActionDefinitionV1{
				ProviderActionID:        mapping.ProviderActionID,
				Description:             description,
				InputSchema:             inputSchema,
				RequestedEffectClass:    mapping.RequestedEffectClass,
				RequestedMaxResultBytes: mapping.RequestedMaxResultBytes,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("%w: tool %q definition: %v", ErrDescribe, mapping.ProviderActionID, err)
		}
		definitions = append(definitions, definition)
	}
	return moduleapi.FreezeActionDefinitionsV1(definitions)
}

// Prepare is intentionally process-free and side-effect-free. It seals the
// exact descriptor mapping and canonical arguments into the existing Action
// Proposal payload. The surrounding Action contract already owns the frozen
// definition digest.
func (adapter *Adapter) Prepare(
	ctx context.Context,
	request moduleapi.ActionRequestV1,
) (json.RawMessage, error) {
	if err := validateAdapterContext(adapter, ctx, ErrPrepare); err != nil {
		return nil, err
	}
	frozenRequest, _, err := moduleapi.NewActionRequestV1(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request: %v", ErrPrepare, err)
	}
	_, found := adapter.plan.toolByProvider[frozenRequest.ProviderActionID]
	if !found {
		return nil, fmt.Errorf("%w: provider action %q is not configured", ErrPrepare, frozenRequest.ProviderActionID)
	}
	payload, err := json.Marshal(preparedPayloadV1{
		SchemaVersion: preparedPayloadSchemaV1,
		ToolName:      frozenRequest.ProviderActionID,
		Arguments:     bytes.Clone(frozenRequest.CanonicalInput),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: marshal payload: %v", ErrPrepare, err)
	}
	canonical, err := moduleapi.CanonicalizeActionPreparedPayloadV1(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: canonical payload: %v", ErrPrepare, err)
	}
	return bytes.Clone(canonical), nil
}

// ExecutePrepared is called only by Gateway. A failure before tools/call is a
// confirmed no-call FAILED result. Once CallTool may have written the request,
// any non-wire error is returned to Gateway as ambiguous and becomes UNKNOWN.
func (adapter *Adapter) ExecutePrepared(
	ctx context.Context,
	execution modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	if err := validateAdapterContext(adapter, ctx, ErrExecute); err != nil {
		return moduleapi.ActionExecutionResultV1{}, err
	}
	frozenExecution, err := modulehost.NewPreparedActionExecutionV1(execution)
	if err != nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: execution closure: %v",
			ErrExecute,
			err,
		)
	}
	if !sameMCPArtifactProviderV1(
		adapter.provider,
		frozenExecution.Binding.Provider,
	) {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: Provider does not match the frozen MCP artifact adapter",
			ErrExecute,
		)
	}
	frozenRequest := frozenExecution.Request
	prepared, err := restorePreparedPayload(frozenRequest.PreparedPayload)
	if err != nil || prepared.ToolName != frozenRequest.ProviderActionID {
		return failedExecution(frozenRequest.AttemptID, "MCP_PREPARED_PAYLOAD_REJECTED", nil)
	}
	if _, found := adapter.plan.toolByProvider[frozenRequest.ProviderActionID]; !found {
		return failedExecution(frozenRequest.AttemptID, "MCP_PREPARED_PAYLOAD_REJECTED", nil)
	}
	if err := adapter.plan.verifyExecutableMode(); err != nil {
		return failedExecution(frozenRequest.AttemptID, "MCP_EXECUTABLE_DRIFT", nil)
	}
	arguments, err := decodeToolArguments(prepared.Arguments)
	if err != nil {
		return failedExecution(frozenRequest.AttemptID, "MCP_PREPARED_PAYLOAD_REJECTED", nil)
	}
	session, err := adapter.connect(ctx)
	if err != nil {
		if errors.Is(err, ErrArtifactDrift) {
			return failedExecution(frozenRequest.AttemptID, "MCP_ARTIFACT_DRIFT", nil)
		}
		return failedExecution(frozenRequest.AttemptID, "MCP_SESSION_START_FAILED", nil)
	}
	callContext, cancel := context.WithTimeout(ctx, adapter.plan.callTimeout)
	defer cancel()
	result, callErr := session.CallTool(callContext, &mcp.CallToolParams{
		Name:      prepared.ToolName,
		Arguments: arguments,
	})
	cleanupErr := session.Close()
	cleanupDiagnostic := ""
	if cleanupErr != nil {
		cleanupDiagnostic = boundedDiagnostic(cleanupErr.Error())
	}
	if callErr != nil {
		var wireError *jsonrpc.Error
		if errors.As(callErr, &wireError) &&
			wireError.Code != sdkClientClosingCode {
			receipt := canonicalReceipt(
				prepared.ToolName,
				"",
				wireError.Code,
				boundedDiagnostic(wireError.Message),
				cleanupDiagnostic,
			)
			return failedExecution(frozenRequest.AttemptID, "MCP_JSONRPC_ERROR", receipt)
		}
		return moduleapi.ActionExecutionResultV1{}, errors.Join(
			fmt.Errorf(
				"%w: tools/call may have been sent: %w",
				ErrExecute,
				errors.Join(callErr, callContext.Err()),
			),
			cleanupErr,
		)
	}
	if result == nil {
		return moduleapi.ActionExecutionResultV1{}, errors.Join(
			fmt.Errorf("%w: tools/call returned no matching result", ErrExecute),
			cleanupErr,
		)
	}
	rawEnvelope, canonicalEnvelope := encodeResultEnvelope(result)
	diagnostic := ""
	if result.IsError {
		diagnostic = toolErrorDiagnostic(result, rawEnvelope)
	}
	receipt := canonicalReceipt(
		prepared.ToolName,
		digestBytes(rawEnvelope),
		0,
		diagnostic,
		cleanupDiagnostic,
	)
	if result.IsError {
		return failedExecution(frozenRequest.AttemptID, "MCP_TOOL_ERROR", receipt)
	}
	if canonicalEnvelope == nil {
		// A matching successful response confirms the effect. Returning an
		// otherwise-complete success with an invalid body lets Gateway record
		// SUCCEEDED + RESULT_REJECTED instead of corrupting certainty to UNKNOWN.
		return moduleapi.ActionExecutionResultV1{
			SchemaVersion:   moduleapi.ActionExecutionResultSchemaV1,
			AttemptID:       frozenRequest.AttemptID,
			Outcome:         moduleapi.ActionExecutionSucceeded,
			CanonicalResult: json.RawMessage(`{`),
			ProviderReceipt: receipt,
		}, nil
	}
	return moduleapi.ActionExecutionResultV1{
		SchemaVersion:   moduleapi.ActionExecutionResultSchemaV1,
		AttemptID:       frozenRequest.AttemptID,
		Outcome:         moduleapi.ActionExecutionSucceeded,
		CanonicalResult: canonicalEnvelope,
		ProviderReceipt: receipt,
	}, nil
}

func sameMCPArtifactProviderV1(
	adapter moduleapi.ActivatedModuleRef,
	binding moduleapi.ActivatedModuleRef,
) bool {
	if err := binding.Validate(); err != nil {
		return false
	}
	return binding.ModuleID == adapter.ModuleID &&
		binding.Version == adapter.Version &&
		binding.ArtifactDigest == adapter.ArtifactDigest &&
		binding.ExecutionClass == adapter.ExecutionClass &&
		binding.AdapterIdentity == adapter.AdapterIdentity
}

// decodeToolArguments converts the already-canonical Action object into the
// value form required by the official SDK. Passing json.RawMessage through
// CallToolParams would be encoded as a byte slice by the SDK's JSON encoder.
// UseNumber preserves the exact binary64-compatible number spelling selected
// by FreeAgent's RFC 8785 canonicalizer.
func decodeToolArguments(canonical []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var arguments map[string]any
	if err := decoder.Decode(&arguments); err != nil {
		return nil, err
	}
	if arguments == nil {
		return nil, errors.New("tool arguments must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("tool arguments contain trailing JSON")
		}
		return nil, err
	}
	return arguments, nil
}

type preparedPayloadV1 struct {
	SchemaVersion string          `json:"schema_version"`
	ToolName      string          `json:"tool_name"`
	Arguments     json.RawMessage `json:"arguments"`
}

func restorePreparedPayload(canonical []byte) (preparedPayloadV1, error) {
	owned, err := moduleapi.CanonicalizeActionPreparedPayloadV1(canonical)
	if err != nil || !bytes.Equal(owned, canonical) {
		return preparedPayloadV1{}, errors.New("prepared payload is not canonical")
	}
	decoder := json.NewDecoder(bytes.NewReader(owned))
	decoder.DisallowUnknownFields()
	var prepared preparedPayloadV1
	if err := decoder.Decode(&prepared); err != nil {
		return preparedPayloadV1{}, err
	}
	if prepared.SchemaVersion != preparedPayloadSchemaV1 ||
		!validMCPToolName(prepared.ToolName) {
		return preparedPayloadV1{}, errors.New("prepared payload identity is invalid")
	}
	if _, err := decodeToolArguments(prepared.Arguments); err != nil {
		return preparedPayloadV1{}, err
	}
	return prepared, nil
}

func (adapter *Adapter) connect(ctx context.Context) (*mcp.ClientSession, error) {
	connectContext, cancel := context.WithTimeout(ctx, adapter.plan.startupTimeout)
	defer cancel()
	if err := moduleapi.VerifyArtifactDirectoryDigestContext(
		connectContext,
		adapter.plan.artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
		adapter.provider.ArtifactDigest,
	); err != nil {
		return nil, fmt.Errorf("%w: full package verification: %w", ErrArtifactDrift, err)
	}
	if err := adapter.plan.prepareExecutable(); err != nil {
		return nil, err
	}
	transport, err := newCommandTransport(
		adapter.plan.executable,
		adapter.plan.arguments,
		adapter.plan.workingDirectory,
		[]string{},
		adapter.plan.maxFrameBytes,
		adapter.plan.closeTimeout,
	)
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "freeagent-mcp-host", Version: "1.0.0"},
		&mcp.ClientOptions{
			Capabilities: &mcp.ClientCapabilities{},
			Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	)
	session, err := client.Connect(connectContext, transport, nil)
	if err != nil {
		return nil, errors.Join(
			err,
			connectContext.Err(),
			transport.closeStartedConnection(),
		)
	}
	initialized := session.InitializeResult()
	if initialized == nil || initialized.ProtocolVersion != ProtocolVersionV1 ||
		initialized.Capabilities == nil || initialized.Capabilities.Tools == nil {
		return nil, errors.Join(
			errors.New("server did not negotiate exact MCP 2025-11-25 Tool capability"),
			session.Close(),
		)
	}
	return session, nil
}

func listAllTools(
	ctx context.Context,
	session *mcp.ClientSession,
) (map[string]*mcp.Tool, error) {
	tools := make(map[string]*mcp.Tool)
	seenCursors := make(map[string]struct{})
	cursor := ""
	aggregateBytes := 0
	cursorBytes := 0
	for page := 0; page < maxServerTools; page++ {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, errors.New("server returned no tools/list result")
		}
		for _, tool := range result.Tools {
			if tool == nil || !validMCPToolName(tool.Name) {
				return nil, errors.New("server returned a nil or invalid tool")
			}
			if _, duplicate := tools[tool.Name]; duplicate {
				return nil, fmt.Errorf("server returned duplicate tool %q", tool.Name)
			}
			rawDefinition, err := json.Marshal(tool)
			if err != nil || len(rawDefinition) > defaultMaxFrameBytes {
				return nil, fmt.Errorf("server tool %q definition is outside discovery bounds", tool.Name)
			}
			canonicalDefinition, err := moduleapi.CanonicalJSONWithLimits(
				rawDefinition,
				moduleapi.CanonicalJSONLimits{
					MaxBytes: defaultMaxFrameBytes,
					MaxDepth: 128,
					MaxNodes: 64 << 10,
				},
			)
			if err != nil || len(canonicalDefinition) >
				moduleapi.MaxActionDefinitionAggregateBytesV1-aggregateBytes {
				return nil, fmt.Errorf(
					"server exceeds %d aggregate discovery bytes",
					moduleapi.MaxActionDefinitionAggregateBytesV1,
				)
			}
			aggregateBytes += len(canonicalDefinition)
			tools[tool.Name] = tool
			if len(tools) > maxServerTools {
				return nil, fmt.Errorf("server exceeds %d discovered tools", maxServerTools)
			}
		}
		if result.NextCursor == "" {
			return tools, nil
		}
		if len(result.NextCursor) > maxCursorBytes ||
			len(result.NextCursor) > maxCursorTotalBytes-cursorBytes {
			return nil, fmt.Errorf(
				"server exceeds %d aggregate tools/list cursor bytes",
				maxCursorTotalBytes,
			)
		}
		if _, duplicate := seenCursors[result.NextCursor]; duplicate {
			return nil, errors.New("server repeated tools/list cursor")
		}
		seenCursors[result.NextCursor] = struct{}{}
		cursorBytes += len(result.NextCursor)
		cursor = result.NextCursor
	}
	return nil, errors.New("server exceeds tools/list page bound")
}

func failedExecution(
	attemptID string,
	classification string,
	receipt json.RawMessage,
) (moduleapi.ActionExecutionResultV1, error) {
	result := moduleapi.ActionExecutionResultV1{
		SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
		AttemptID:           attemptID,
		Outcome:             moduleapi.ActionExecutionFailed,
		ProviderReceipt:     bytes.Clone(receipt),
		ErrorClassification: classification,
	}
	frozen, _, err := moduleapi.NewActionExecutionResultV1(result)
	return frozen, err
}

func encodeResultEnvelope(result *mcp.CallToolResult) ([]byte, json.RawMessage) {
	raw, err := json.Marshal(struct {
		SchemaVersion     string        `json:"schema_version"`
		Content           []mcp.Content `json:"content"`
		StructuredContent any           `json:"structured_content,omitempty"`
	}{
		SchemaVersion:     resultEnvelopeSchemaV1,
		Content:           result.Content,
		StructuredContent: result.StructuredContent,
	})
	if err != nil {
		return []byte("result-marshal-failed"), nil
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		raw,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: defaultMaxFrameBytes,
			MaxDepth: 128,
			MaxNodes: 64 << 10,
		},
	)
	if err != nil || len(canonical) == 0 || canonical[0] != '{' {
		return raw, nil
	}
	return raw, canonical
}

func canonicalReceipt(
	toolName string,
	resultDigest string,
	errorCode int64,
	diagnostic string,
	cleanupDiagnostic string,
) json.RawMessage {
	type receiptV1 struct {
		SchemaVersion     string `json:"schema_version"`
		ProtocolVersion   string `json:"protocol_version"`
		ToolName          string `json:"tool_name"`
		ResultDigest      string `json:"result_digest,omitempty"`
		JSONRPCErrorCode  int64  `json:"jsonrpc_error_code,omitempty"`
		Diagnostic        string `json:"diagnostic,omitempty"`
		CleanupDiagnostic string `json:"cleanup_diagnostic,omitempty"`
	}
	raw, err := json.Marshal(receiptV1{
		SchemaVersion:     receiptSchemaV1,
		ProtocolVersion:   ProtocolVersionV1,
		ToolName:          toolName,
		ResultDigest:      resultDigest,
		JSONRPCErrorCode:  errorCode,
		Diagnostic:        diagnostic,
		CleanupDiagnostic: cleanupDiagnostic,
	})
	if err != nil {
		return nil
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		return nil
	}
	return canonical
}

func toolErrorDiagnostic(result *mcp.CallToolResult, rawEnvelope []byte) string {
	var textParts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && text != nil && text.Text != "" {
			textParts = append(textParts, text.Text)
		}
	}
	if len(textParts) != 0 {
		return boundedDiagnostic(strings.Join(textParts, "\n"))
	}
	return boundedDiagnostic(string(rawEnvelope))
}

func boundedDiagnostic(value string) string {
	const maximumBytes = 2048
	value = strings.ToValidUTF8(value, "�")
	value = moduleapi.CanonicalText(value)
	value = strings.Map(func(character rune) rune {
		if character < 0x20 && character != '\n' && character != '\r' && character != '\t' {
			return ' '
		}
		if character == 0x7f {
			return ' '
		}
		return character
	}, value)
	value = strings.TrimSpace(value)
	if len(value) <= maximumBytes {
		return value
	}
	value = value[:maximumBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func validateAdapterContext(adapter *Adapter, ctx context.Context, kind error) error {
	if adapter == nil {
		return fmt.Errorf("%w: adapter is nil", kind)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", kind)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: context: %w", kind, err)
	}
	return nil
}

// StableProviderIDs exposes a defensive, sorted view only for tests and
// composition validation; it grants no execution capability.
func (adapter *Adapter) StableProviderIDs() []string {
	if adapter == nil {
		return nil
	}
	values := make([]string, 0, len(adapter.plan.toolByProvider))
	for value := range adapter.plan.toolByProvider {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
