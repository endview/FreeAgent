package mcpstdio

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var helperInputSchema = json.RawMessage(
	`{"additionalProperties":false,"properties":{"text":{"maxLength":8192,"type":"string"}},"required":["text"],"type":"object"}`,
)

func TestMCPHelperProcess(t *testing.T) {
	mode, counterPath, expectedGORACE, helper := mcpHelperArguments(os.Args)
	if !helper {
		return
	}
	if os.Getenv("GORACE") != expectedGORACE {
		os.Exit(97)
	}
	if runtime.GOOS != "windows" {
		environment := os.Environ()
		if len(environment) != 1 || environment[0] != "GORACE="+expectedGORACE {
			os.Exit(96)
		}
	}
	os.Exit(runMCPHelper(mode, counterPath))
}

func TestAdapterDescribePrepareExecuteRealStdio(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "events.log")
	adapter := newTestAdapter(t, "success", counter)
	definitions, err := adapter.Describe(
		context.Background(),
		moduleapi.ActionDescribeRequestV1{
			SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
			Parameters:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(definitions) != 1 ||
		definitions[0].ProviderActionID != "text.stats" ||
		!jsonEqual(definitions[0].InputSchema, helperInputSchema) {
		t.Fatalf("unexpected definitions: %+v", definitions)
	}
	digest := strings.Repeat("d", 64)
	prepared, err := adapter.Prepare(
		context.Background(),
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   "text.stats",
			ProviderActionID: "text.stats",
			DefinitionDigest: digest,
			CanonicalInput:   json.RawMessage(`{"text":"hello world"}`),
		},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	executed, err := adapter.ExecutePrepared(
		context.Background(),
		preparedMCPExecution(t, adapter, moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        "attempt-mcp-success",
			PublicActionID:   "text.stats",
			ProviderActionID: "text.stats",
			DefinitionDigest: digest,
			MaxResultBytes:   4096,
			PreparedPayload:  prepared,
		}),
	)
	if err != nil {
		t.Fatalf("ExecutePrepared: %v", err)
	}
	if executed.Outcome != moduleapi.ActionExecutionSucceeded ||
		len(executed.ProviderReceipt) == 0 {
		t.Fatalf("unexpected execution: %+v receipt=%s", executed, executed.ProviderReceipt)
	}
	var envelope struct {
		SchemaVersion string `json:"schema_version"`
		Content       []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(executed.CanonicalResult, &envelope); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if envelope.SchemaVersion != resultEnvelopeSchemaV1 ||
		len(envelope.Content) != 1 ||
		envelope.Content[0].Type != "text" ||
		envelope.Content[0].Text != "hello world" {
		t.Fatalf("unexpected result envelope: %+v", envelope)
	}
	assertMCPHelperEvents(t, counter, 2, 1)
}

func TestNewContextPreservesDescriptorCancellationCause(t *testing.T) {
	t.Parallel()

	ctx, cancel := newCancelAfterFirstMCPContextCheck()
	t.Cleanup(cancel)
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           "example.mcp.cancel",
		Version:            "1.0.0",
		ArtifactDigest:     strings.Repeat("a", moduleapi.SHA256HexLength),
		InstanceID:         "mcp-cancel",
		ExecutionClass:     moduleapi.ExecutionLocalProcess,
		AdapterIdentity:    AdapterIdentityV1,
		ActivationRevision: 1,
	}
	adapter, err := NewContext(
		ctx,
		provider,
		t.TempDir(),
		"content/mcp.json",
		[]byte(`{}`),
	)
	if adapter != nil {
		t.Fatalf("NewContext() returned adapter = %T", adapter)
	}
	for _, target := range []error{
		ErrInvalidAdapter,
		ErrInvalidDescriptor,
		context.Canceled,
	} {
		if !errors.Is(err, target) {
			t.Fatalf("NewContext() error = %v, want %v", err, target)
		}
	}
}

func TestVerifyExecutableContextPreservesMidReadCancellationCause(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(t.TempDir(), "mcp-helper.bin")
	if err := os.WriteFile(
		executable,
		bytes.Repeat([]byte("x"), 96<<10),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := newCancelAfterFirstMCPContextCheck()
	t.Cleanup(cancel)
	err := (launchPlan{
		executable:       executable,
		executableDigest: strings.Repeat("0", moduleapi.SHA256HexLength),
	}).verifyExecutableContext(ctx)
	if !errors.Is(err, ErrArtifactDrift) || !errors.Is(err, context.Canceled) {
		t.Fatalf("verifyExecutableContext() error = %v", err)
	}
}

func TestValidateAdapterContextPreservesCancellationCause(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := validateAdapterContext(&Adapter{}, ctx, ErrInvalidAdapter)
	if !errors.Is(err, ErrInvalidAdapter) || !errors.Is(err, context.Canceled) {
		t.Fatalf("validateAdapterContext() error = %v", err)
	}
}

func TestAdapterPreservesCanonicalNumericArguments(t *testing.T) {
	adapter := newTestAdapter(t, "success", "")
	digest := strings.Repeat("c", 64)
	prepared, err := adapter.Prepare(
		context.Background(),
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   "text.stats",
			ProviderActionID: "text.stats",
			DefinitionDigest: digest,
			CanonicalInput:   json.RawMessage(`{"count":9007199254740991,"ratio":0.000001}`),
		},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	executed, err := adapter.ExecutePrepared(
		context.Background(),
		preparedMCPExecution(t, adapter, moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        "attempt-mcp-numbers",
			PublicActionID:   "text.stats",
			ProviderActionID: "text.stats",
			DefinitionDigest: digest,
			MaxResultBytes:   4096,
			PreparedPayload:  prepared,
		}),
	)
	if err != nil {
		t.Fatalf("ExecutePrepared: %v", err)
	}
	var envelope struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(executed.CanonicalResult, &envelope); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(envelope.Content) != 1 ||
		envelope.Content[0].Text != `{"count":9007199254740991,"ratio":0.000001}` {
		t.Fatalf("numeric arguments changed on the wire: %+v", envelope)
	}
}

func TestAdapterToolErrorIsConfirmedFailed(t *testing.T) {
	adapter := newTestAdapter(t, "tool-error", "")
	request := preparedExecutionRequest(t, adapter, "attempt-mcp-tool-error")
	result, err := adapter.ExecutePrepared(
		context.Background(),
		preparedMCPExecution(t, adapter, request),
	)
	if err != nil {
		t.Fatalf("ExecutePrepared: %v", err)
	}
	if result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != "MCP_TOOL_ERROR" ||
		len(result.ProviderReceipt) == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	var receipt struct {
		Diagnostic string `json:"diagnostic"`
	}
	if err := json.Unmarshal(result.ProviderReceipt, &receipt); err != nil ||
		receipt.Diagnostic != "expected tool failure" {
		t.Fatalf("tool error receipt=%s, %v", result.ProviderReceipt, err)
	}
}

func TestAdapterJSONRPCErrorIsConfirmedFailedWithBoundedDiagnostic(t *testing.T) {
	adapter := newTestAdapter(t, "jsonrpc-error", "")
	request := preparedExecutionRequest(t, adapter, "attempt-mcp-jsonrpc-error")
	result, err := adapter.ExecutePrepared(
		context.Background(),
		preparedMCPExecution(t, adapter, request),
	)
	if err != nil {
		t.Fatalf("ExecutePrepared: %v", err)
	}
	if result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != "MCP_JSONRPC_ERROR" {
		t.Fatalf("unexpected result: %+v", result)
	}
	var receipt struct {
		Code       int64  `json:"jsonrpc_error_code"`
		Diagnostic string `json:"diagnostic"`
	}
	if err := json.Unmarshal(result.ProviderReceipt, &receipt); err != nil ||
		receipt.Code != jsonrpc.CodeInvalidParams ||
		receipt.Diagnostic != "expected invalid params" {
		t.Fatalf("JSON-RPC error receipt=%s, %v", result.ProviderReceipt, err)
	}
}

func TestAdapterCrashAfterCallIsAmbiguousAndNotRetried(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "calls.txt")
	adapter := newTestAdapter(t, "crash-after-call", counter)
	request := preparedExecutionRequest(t, adapter, "attempt-mcp-crash")
	result, err := adapter.ExecutePrepared(
		context.Background(),
		preparedMCPExecution(t, adapter, request),
	)
	if err == nil || !errors.Is(err, ErrExecute) {
		t.Fatalf("ExecutePrepared error=%v result=%+v", err, result)
	}
	content, readErr := os.ReadFile(counter)
	if readErr != nil {
		t.Fatalf("read call counter: %v", readErr)
	}
	if strings.Count(string(content), "process_start\n") != 1 ||
		strings.Count(string(content), "tool_call\n") != 1 {
		t.Fatalf("MCP process events=%q, want one start and one tools/call", content)
	}
}

func TestAdapterPostCallWireCorruptionIsAmbiguousAndNotRetried(t *testing.T) {
	for _, mode := range []string{
		"post-call-id-mismatch",
		"post-call-malformed",
		"post-call-oversize",
	} {
		t.Run(mode, func(t *testing.T) {
			events := filepath.Join(t.TempDir(), "events.log")
			adapter := newTestAdapter(t, mode, events)
			request := preparedExecutionRequest(t, adapter, "attempt-mcp-wire-corruption")
			result, err := adapter.ExecutePrepared(
				context.Background(),
				preparedMCPExecution(t, adapter, request),
			)
			if err == nil || !errors.Is(err, ErrExecute) {
				t.Fatalf("ExecutePrepared result=%+v error=%v", result, err)
			}
			assertMCPHelperEvents(t, events, 1, 1)
		})
	}
}

func TestAdapterBlockedToolCallWriteReachesDeadlineWithoutRetry(t *testing.T) {
	events := filepath.Join(t.TempDir(), "events.log")
	adapter := newTestAdapter(t, "block-tool-call-write", events)
	if _, err := adapter.Describe(
		context.Background(),
		moduleapi.ActionDescribeRequestV1{
			SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
			Parameters:    json.RawMessage(`{}`),
		},
	); err != nil {
		t.Fatalf("Describe: %v", err)
	}

	digest := strings.Repeat("b", 64)
	var prepared json.RawMessage
	for textBytes := moduleapi.MaxActionPreparedPayloadBytesV1; textBytes > 0; textBytes -= 32 {
		candidate, err := adapter.Prepare(
			context.Background(),
			moduleapi.ActionRequestV1{
				SchemaVersion:    moduleapi.ActionRequestSchemaV1,
				PublicActionID:   "text.stats",
				ProviderActionID: "text.stats",
				DefinitionDigest: digest,
				CanonicalInput: json.RawMessage(
					`{"text":"` + strings.Repeat("x", textBytes) + `"}`,
				),
			},
		)
		if err == nil {
			prepared = candidate
			break
		}
	}
	if len(prepared) < 60<<10 {
		t.Fatalf("prepared blocked-write payload is too small: %d bytes", len(prepared))
	}

	started := time.Now()
	result, err := adapter.ExecutePrepared(
		context.Background(),
		preparedMCPExecution(t, adapter, moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        "attempt-mcp-blocked-write",
			PublicActionID:   "text.stats",
			ProviderActionID: "text.stats",
			DefinitionDigest: digest,
			MaxResultBytes:   4096,
			PreparedPayload:  prepared,
		}),
	)
	if err == nil ||
		!errors.Is(err, ErrExecute) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ExecutePrepared error=%v result=%+v, want ambiguous execution", err, result)
	}
	if elapsed := time.Since(started); elapsed > 7*time.Second {
		t.Fatalf("blocked MCP tools/call took %v, want bounded deadline and cleanup", elapsed)
	}
	content, readErr := os.ReadFile(events)
	if readErr != nil {
		t.Fatalf("read blocked-write events: %v", readErr)
	}
	if strings.Count(string(content), "process_start\n") != 2 ||
		strings.Count(string(content), "blocked_write_ready\n") != 1 ||
		strings.Count(string(content), "tool_call\n") != 0 {
		t.Fatalf("blocked tools/call was retried or helper did not reach the boundary: %q", content)
	}
}

func TestAdapterDescribeTimeoutPreservesCauseAndReapsProcess(t *testing.T) {
	for _, test := range []struct {
		mode  string
		ready string
	}{
		{mode: "stall-initialize", ready: "stall_initialize_ready\n"},
		{mode: "stall-tools-list", ready: "stall_tools_list_ready\n"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			events := filepath.Join(t.TempDir(), "events.log")
			adapter := newTestAdapter(t, test.mode, events)
			ctx, cancel := context.WithTimeout(
				context.Background(),
				3*time.Second,
			)
			defer cancel()
			started := time.Now()
			definitions, err := adapter.Describe(
				ctx,
				moduleapi.ActionDescribeRequestV1{
					SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
					Parameters:    json.RawMessage(`{}`),
				},
			)
			if err == nil ||
				!errors.Is(err, ErrDescribe) ||
				!errors.Is(err, context.DeadlineExceeded) ||
				len(definitions) != 0 {
				t.Fatalf("Describe definitions=%+v error=%v", definitions, err)
			}
			if elapsed := time.Since(started); elapsed > 10*time.Second {
				t.Fatalf("Describe timeout and cleanup took %v", elapsed)
			}
			assertMCPHelperEvents(t, events, 1, 0)
			content, readErr := os.ReadFile(events)
			if readErr != nil || strings.Count(string(content), test.ready) != 1 {
				t.Fatalf("Describe did not reach %q: events=%q error=%v", test.ready, content, readErr)
			}
		})
	}
}

func TestAdapterRejectsInvalidDiscoveryAndStdoutPollution(t *testing.T) {
	for _, mode := range []string{
		"invalid-schema",
		"stdout-pollution",
		"aggregate-overflow",
		"cursor-overflow",
		"unsolicited-notification",
		"unsolicited-request",
	} {
		t.Run(mode, func(t *testing.T) {
			adapter := newTestAdapter(t, mode, "")
			if _, err := adapter.Describe(
				context.Background(),
				moduleapi.ActionDescribeRequestV1{
					SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
					Parameters:    json.RawMessage(`{}`),
				},
			); err == nil {
				t.Fatal("Describe accepted invalid MCP server")
			}
		})
	}
}

func TestAdapterReportsConnectAndDescribeCleanupFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		mode string
		want string
	}{
		{
			name: "connect negotiation cleanup",
			mode: "unsupported-protocol-cleanup-error",
			want: "exit status 77",
		},
		{
			name: "describe cleanup",
			mode: "describe-close-error",
			want: "session cleanup",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := filepath.Join(t.TempDir(), "events.log")
			adapter := newTestAdapter(t, test.mode, events)
			definitions, err := adapter.Describe(
				context.Background(),
				moduleapi.ActionDescribeRequestV1{
					SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
					Parameters:    json.RawMessage(`{}`),
				},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) ||
				len(definitions) != 0 {
				t.Fatalf("Describe definitions=%+v error=%v, want %q", definitions, err, test.want)
			}
			assertMCPHelperEvents(t, events, 1, 0)
		})
	}
}

func TestAdapterConfirmedExecutionKeepsTerminalWhenCleanupFails(t *testing.T) {
	events := filepath.Join(t.TempDir(), "events.log")
	adapter := newTestAdapter(t, "execute-close-error", events)
	request := preparedExecutionRequest(t, adapter, "attempt-mcp-cleanup-confirmed")
	result, err := adapter.ExecutePrepared(
		context.Background(),
		preparedMCPExecution(t, adapter, request),
	)
	if err != nil || result.Outcome != moduleapi.ActionExecutionSucceeded {
		t.Fatalf("confirmed execution result=%+v error=%v", result, err)
	}
	var receipt struct {
		CleanupDiagnostic string `json:"cleanup_diagnostic"`
	}
	if err := json.Unmarshal(result.ProviderReceipt, &receipt); err != nil ||
		!strings.Contains(receipt.CleanupDiagnostic, "exit status 79") {
		t.Fatalf("confirmed cleanup receipt=%s error=%v", result.ProviderReceipt, err)
	}
	assertMCPHelperEvents(t, events, 1, 1)
}

func TestAdapterRejectsUnsupportedProtocolAndReapsStartedProcess(t *testing.T) {
	events := filepath.Join(t.TempDir(), "events.log")
	adapter := newTestAdapter(t, "unsupported-protocol", events)
	if _, err := adapter.Describe(
		context.Background(),
		moduleapi.ActionDescribeRequestV1{
			SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
			Parameters:    json.RawMessage(`{}`),
		},
	); err == nil {
		t.Fatal("Describe accepted an unsupported negotiated protocol")
	}
	content, err := os.ReadFile(events)
	if err != nil {
		t.Fatalf("read unsupported-protocol events: %v", err)
	}
	if strings.Count(string(content), "process_start\n") != 1 ||
		strings.Count(string(content), "process_exit\n") != 1 ||
		strings.Count(string(content), "tool_call\n") != 0 {
		t.Fatalf("unsupported-protocol process was not reaped exactly once: %q", content)
	}
}

func TestAdapterPrepareAndGenericInvokeNeverStartProcess(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "calls.txt")
	adapter := newTestAdapter(t, "crash-after-call", counter)
	if _, err := adapter.Prepare(
		context.Background(),
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   "text.stats",
			ProviderActionID: "text.stats",
			DefinitionDigest: strings.Repeat("e", 64),
			CanonicalInput:   json.RawMessage(`{"text":"pure"}`),
		},
	); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := adapter.Invoke(
		context.Background(),
		modulehost.PreparedInvocation{},
	); !errors.Is(err, ErrGenericInvocation) {
		t.Fatalf("Invoke error=%v", err)
	}
	if _, err := os.Stat(counter); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Prepare or generic Invoke started MCP: %v", err)
	}
}

func TestAdapterRejectsPreparedToolTamperWithoutStartingProcess(t *testing.T) {
	events := filepath.Join(t.TempDir(), "events.log")
	adapter := newTestAdapter(t, "success", events)
	request := preparedExecutionRequest(t, adapter, "attempt-mcp-tampered-tool")
	var payload map[string]any
	if err := json.Unmarshal(request.PreparedPayload, &payload); err != nil {
		t.Fatal(err)
	}
	payload["tool_name"] = "other.tool"
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request.PreparedPayload, err = moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.ExecutePrepared(
		context.Background(),
		preparedMCPExecution(t, adapter, request),
	)
	if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != "MCP_PREPARED_PAYLOAD_REJECTED" {
		t.Fatalf("tampered prepared payload result=%+v, %v", result, err)
	}
	if _, err := os.Lstat(events); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tampered payload started MCP: %v", err)
	}
}

func TestAdapterRejectsExecutableDriftWithoutStartingProcess(t *testing.T) {
	events := filepath.Join(t.TempDir(), "events.log")
	adapter := newTestAdapter(t, "success", events)
	request := preparedExecutionRequest(t, adapter, "attempt-mcp-executable-drift")
	if err := os.WriteFile(adapter.plan.executable, []byte("drift"), 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := adapter.ExecutePrepared(
		context.Background(),
		preparedMCPExecution(t, adapter, request),
	)
	if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != "MCP_ARTIFACT_DRIFT" {
		t.Fatalf("executable drift result=%+v, %v", result, err)
	}
	if _, err := os.Lstat(events); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("executable drift started MCP: %v", err)
	}
}

func TestAdapterRejectsNonExecutableArtifactWithoutChangingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use Unix execute permission bits")
	}
	events := filepath.Join(t.TempDir(), "events.log")
	adapter := newTestAdapter(t, "success", events)
	request := preparedExecutionRequest(t, adapter, "attempt-mcp-non-executable")
	if err := os.Chmod(adapter.plan.executable, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := adapter.ExecutePrepared(
		context.Background(),
		preparedMCPExecution(t, adapter, request),
	)
	if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
		result.ErrorClassification != "MCP_EXECUTABLE_DRIFT" {
		t.Fatalf("non-executable artifact result=%+v error=%v", result, err)
	}
	info, statErr := os.Stat(adapter.plan.executable)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("adapter changed artifact mode=%v", info.Mode())
	}
	if _, err := os.Lstat(events); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-executable artifact started MCP: %v", err)
	}
}

func TestAdapterRejectsDescriptorAndSiblingDriftWithoutStartingProcess(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *Adapter)
	}{
		{
			name: "descriptor",
			mutate: func(t *testing.T, adapter *Adapter) {
				t.Helper()
				if err := os.WriteFile(
					filepath.Join(adapter.plan.artifactDirectory, "content", "mcp.json"),
					[]byte(`{}`),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "working directory sibling",
			mutate: func(t *testing.T, adapter *Adapter) {
				t.Helper()
				if err := os.WriteFile(
					filepath.Join(adapter.plan.workingDirectory, "late-sibling.txt"),
					[]byte("drift"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := filepath.Join(t.TempDir(), "events.log")
			adapter := newTestAdapter(t, "success", events)
			request := preparedExecutionRequest(t, adapter, "attempt-mcp-package-drift")
			test.mutate(t, adapter)
			result, err := adapter.ExecutePrepared(
				context.Background(),
				preparedMCPExecution(t, adapter, request),
			)
			if err != nil || result.Outcome != moduleapi.ActionExecutionFailed ||
				result.ErrorClassification != "MCP_ARTIFACT_DRIFT" {
				t.Fatalf("package drift result=%+v error=%v", result, err)
			}
			if _, err := os.Lstat(events); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("package drift started MCP: %v", err)
			}
		})
	}
}

func newTestAdapter(t *testing.T, mode, counterPath string) *Adapter {
	t.Helper()
	root := t.TempDir()
	contentDirectory := filepath.Join(root, "content")
	if err := os.MkdirAll(contentDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	currentExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	currentExecutableBytes, err := os.ReadFile(currentExecutable)
	if err != nil {
		t.Fatal(err)
	}
	executableName := "mcp-helper"
	expectedGORACE := ""
	arguments := []string{
		"-test.run=^TestMCPHelperProcess$",
		"--",
		"mcp-helper",
		mode,
		counterPath,
		expectedGORACE,
	}
	executableBytes := currentExecutableBytes
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	} else {
		expectedGORACE = strings.TrimPrefix(helperGORACEEnvironment(), "GORACE=")
		arguments[len(arguments)-1] = expectedGORACE
		arguments = append([]string{expectedGORACE}, arguments...)
		helperBinaryPath := filepath.Join(contentDirectory, executableName+".bin")
		if err := os.WriteFile(helperBinaryPath, currentExecutableBytes, 0o700); err != nil {
			t.Fatal(err)
		}
		executableBytes = []byte("#!/bin/sh\nGORACE=\"$1\"\nshift\n/bin/chmod 0700 \"$0.bin\" || exit 125\nexec /usr/bin/env -i \"GORACE=$GORACE\" \"$0.bin\" \"$@\"\n")
	}
	executablePath := filepath.Join(contentDirectory, executableName)
	if err := os.WriteFile(executablePath, executableBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	executableDigest := sha256.Sum256(executableBytes)
	descriptor := HostDescriptorV1{
		SchemaVersion:    HostDescriptorSchemaV1,
		ProtocolVersion:  ProtocolVersionV1,
		Executable:       "content/" + executableName,
		ExecutableSHA256: hex.EncodeToString(executableDigest[:]),
		Arguments:        arguments,
		WorkingDirectory: "content",
		Tools: []ToolBindingV1{{
			ProviderActionID:        "text.stats",
			RequestedEffectClass:    moduleapi.EffectNone,
			RequestedMaxResultBytes: 4096,
		}},
		// The helper is a copied Go test executable. A cold Windows linker/cache
		// run can leave the machine busy enough that process initialization takes
		// more than five seconds even though the adapter is healthy. Keep this
		// fixture below the production maximum while avoiding a load-dependent
		// full-suite failure; deadline-specific tests still supply their own
		// shorter parent context.
		StartupTimeoutMillis: 30000,
		CallTimeoutMillis:    2000,
		CloseTimeoutMillis:   1000,
		MaxFrameBytes:        256 << 10,
	}
	raw, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	descriptorPath := filepath.Join(contentDirectory, "mcp.json")
	if err := os.WriteFile(descriptorPath, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := json.Marshal(moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "example.mcp.tools",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestLocalProcess,
			Protocol:   moduleapi.RuntimeProtocolMCPStdio20251125,
			Entrypoint: "content/mcp.json",
		},
		Provides: []moduleapi.PortRef{{
			Name:         "action.provider",
			ExactVersion: "v1",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestCanonical, err := moduleapi.CanonicalJSON(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical); err != nil {
		t.Fatalf("test manifest: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		root,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		files,
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           "example.mcp.tools",
		Version:            "1.0.0",
		ArtifactDigest:     artifactDigest,
		InstanceID:         "mcp-tools",
		ExecutionClass:     moduleapi.ExecutionLocalProcess,
		AdapterIdentity:    AdapterIdentityV1,
		ActivationRevision: 1,
	}
	adapter, err := New(provider, root, "content/mcp.json", canonical)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return adapter
}

func preparedExecutionRequest(
	t *testing.T,
	adapter *Adapter,
	attemptID string,
) moduleapi.ActionExecutionRequestV1 {
	t.Helper()
	digest := strings.Repeat("f", 64)
	prepared, err := adapter.Prepare(
		context.Background(),
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   "text.stats",
			ProviderActionID: "text.stats",
			DefinitionDigest: digest,
			CanonicalInput:   json.RawMessage(`{"text":"hello world"}`),
		},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return moduleapi.ActionExecutionRequestV1{
		SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
		AttemptID:        attemptID,
		PublicActionID:   "text.stats",
		ProviderActionID: "text.stats",
		DefinitionDigest: digest,
		MaxResultBytes:   4096,
		PreparedPayload:  prepared,
	}
}

func preparedMCPExecution(
	t *testing.T,
	adapter *Adapter,
	request moduleapi.ActionExecutionRequestV1,
) modulehost.PreparedActionExecutionV1 {
	t.Helper()
	_, configCanonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   request.PublicActionID,
				ProviderActionID: request.ProviderActionID,
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   request.MaxResultBytes,
			}},
			Parameters: json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authorityCanonical, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 "mcp-test-tenant",
			AllowedWorkspaceIDs:      []string{"*"},
			AllowedProviderActionIDs: []string{request.ProviderActionID},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           request.MaxResultBytes,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := modulehost.NewPreparedActionExecutionV1(
		modulehost.PreparedActionExecutionV1{
			Request: request,
			Binding: moduleapi.PortBinding{
				Provider:            adapter.provider,
				ConfigRef:           mcpTestContentDigest("CONFIG", configCanonical),
				AuthorityCeilingRef: mcpTestContentDigest("AUTHORITY_CEILING", authorityCanonical),
				FailurePolicy:       moduleapi.FailureRequired,
			},
			ConfigCanonical:    configCanonical,
			AuthorityCanonical: authorityCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return execution
}

func mcpTestContentDigest(kind string, canonical []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("freeagent.content-record/v1\x00"))
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte("application/json"))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonical)
	return hex.EncodeToString(digest.Sum(nil))
}

type cancelAfterFirstMCPContextCheck struct {
	context.Context

	mu        sync.Mutex
	cancelled bool
	cancel    context.CancelFunc
}

func newCancelAfterFirstMCPContextCheck() (
	*cancelAfterFirstMCPContextCheck,
	context.CancelFunc,
) {
	ctx, cancel := context.WithCancel(context.Background())
	return &cancelAfterFirstMCPContextCheck{
		Context: ctx,
		cancel:  cancel,
	}, cancel
}

func (ctx *cancelAfterFirstMCPContextCheck) Err() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	err := ctx.Context.Err()
	if err == nil && !ctx.cancelled {
		ctx.cancelled = true
		ctx.cancel()
	}
	return err
}

func mcpHelperArguments(arguments []string) (string, string, string, bool) {
	for index, argument := range arguments {
		if argument != "mcp-helper" || index+3 >= len(arguments) {
			continue
		}
		return arguments[index+1], arguments[index+2], arguments[index+3], true
	}
	return "", "", "", false
}

func runMCPHelper(mode, counterPath string) int {
	if counterPath != "" {
		if err := appendMCPHelperEvent(counterPath, "process_start\n"); err != nil {
			return 3
		}
	}
	if mode == "unsupported-protocol" {
		return runUnsupportedProtocolHelper(counterPath, 0)
	}
	if mode == "unsupported-protocol-cleanup-error" {
		return runUnsupportedProtocolHelper(counterPath, 77)
	}
	if mode == "cursor-overflow" ||
		mode == "unsolicited-notification" ||
		mode == "unsolicited-request" ||
		mode == "stall-initialize" ||
		mode == "stall-tools-list" ||
		mode == "describe-close-error" ||
		mode == "execute-close-error" ||
		strings.HasPrefix(mode, "post-call-") {
		return runRawMCPMode(mode, counterPath)
	}
	if mode == "block-tool-call-write" &&
		mcpHelperEventCount(counterPath, "process_start\n") >= 2 {
		return runBlockedToolCallWriteHelper(counterPath)
	}
	if mode == "stdout-pollution" {
		_, _ = io.WriteString(os.Stdout, "not-json\n")
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "freeagent-test-mcp", Version: "1.0.0"},
		&mcp.ServerOptions{PageSize: 1},
	)
	schema := any(helperInputSchema)
	if mode == "invalid-schema" {
		schema = json.RawMessage(`{"$ref":"https://example.invalid/schema","type":"object"}`)
	}
	server.AddTool(
		&mcp.Tool{
			Name:        "text.stats",
			Description: "Return the supplied text through an MCP Tool call.",
			InputSchema: schema,
		},
		func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if counterPath != "" {
				if err := appendMCPHelperEvent(counterPath, "tool_call\n"); err != nil {
					return nil, err
				}
			}
			if mode == "crash-after-call" {
				os.Exit(91)
			}
			if mode == "tool-error" {
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: "expected tool failure"}},
					IsError: true,
				}, nil
			}
			if mode == "jsonrpc-error" {
				return nil, &jsonrpc.Error{
					Code:    jsonrpc.CodeInvalidParams,
					Message: "expected invalid params",
				}
			}
			var arguments map[string]any
			if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
				return nil, err
			}
			text := string(request.Params.Arguments)
			if supplied, present := arguments["text"].(string); present {
				text = supplied
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{
					Text: text,
				}},
				StructuredContent: map[string]any{
					"arguments": arguments,
				},
			}, nil
		},
	)
	if mode == "aggregate-overflow" {
		for index := 0; index < 80; index++ {
			server.AddTool(
				&mcp.Tool{
					Name:        fmt.Sprintf("overflow_%03d", index),
					Description: strings.Repeat("d", 4096),
					InputSchema: json.RawMessage(
						`{"additionalProperties":false,"properties":{},"type":"object"}`,
					),
				},
				func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return &mcp.CallToolResult{Content: []mcp.Content{}}, nil
				},
			)
		}
	}
	server.AddTool(
		&mcp.Tool{
			Name:        "unconfigured_tool",
			Description: "Force tools/list pagination without granting an Action.",
			InputSchema: json.RawMessage(`{"additionalProperties":false,"properties":{},"type":"object"}`),
		},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{}}, nil
		},
	)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil &&
		!errors.Is(err, io.EOF) {
		return 2
	}
	return 0
}

func runUnsupportedProtocolHelper(eventPath string, exitCode int) int {
	line, err := bufio.NewReader(os.Stdin).ReadBytes('\n')
	if err != nil {
		return 4
	}
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(line, &request); err != nil || len(request.ID) == 0 {
		return 5
	}
	response, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result: map[string]any{
			"protocolVersion": "2099-01-01",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "unsupported-protocol-test",
				"version": "1.0.0",
			},
		},
	})
	if err != nil {
		return 6
	}
	if _, err := os.Stdout.Write(append(response, '\n')); err != nil {
		return 7
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if eventPath != "" {
		if err := appendMCPHelperEvent(eventPath, "process_exit\n"); err != nil {
			return 8
		}
	}
	return exitCode
}

func runRawMCPMode(mode, eventPath string) int {
	reader := bufio.NewReader(os.Stdin)
	initialize, err := readRawMCPRequest(reader)
	if err != nil || initialize.Method != "initialize" || len(initialize.ID) == 0 {
		return 20
	}
	if mode == "stall-initialize" {
		if err := appendMCPHelperEvent(eventPath, "stall_initialize_ready\n"); err != nil {
			return 30
		}
		time.Sleep(30 * time.Second)
		return 0
	}
	if err := writeRawMCPResult(initialize.ID, map[string]any{
		"protocolVersion": ProtocolVersionV1,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "raw-mcp-test",
			"version": "1.0.0",
		},
	}); err != nil {
		return 21
	}
	initialized, err := readRawMCPRequest(reader)
	if err != nil || initialized.Method != "notifications/initialized" {
		return 22
	}

	switch mode {
	case "stall-tools-list":
		request, err := readRawMCPRequest(reader)
		if err != nil || request.Method != "tools/list" || len(request.ID) == 0 {
			return 31
		}
		if err := appendMCPHelperEvent(eventPath, "stall_tools_list_ready\n"); err != nil {
			return 32
		}
		time.Sleep(30 * time.Second)
		return 0
	case "unsolicited-notification":
		if _, err := io.WriteString(
			os.Stdout,
			`{"jsonrpc":"2.0","method":"notifications/tools/list_changed","params":{}}`+"\n",
		); err != nil {
			return 23
		}
	case "unsolicited-request":
		if _, err := io.WriteString(
			os.Stdout,
			`{"jsonrpc":"2.0","id":"server-1","method":"sampling/createMessage","params":{}}`+"\n",
		); err != nil {
			return 24
		}
	case "cursor-overflow":
		for page := 0; page < 20; page++ {
			request, err := readRawMCPRequest(reader)
			if err != nil || request.Method != "tools/list" || len(request.ID) == 0 {
				return 25
			}
			cursor := strings.Repeat("c", 4080) + fmt.Sprintf("%04d", page)
			if err := writeRawMCPResult(request.ID, map[string]any{
				"tools":      []any{},
				"nextCursor": cursor,
			}); err != nil {
				return 26
			}
		}
	case "describe-close-error":
		request, err := readRawMCPRequest(reader)
		if err != nil || request.Method != "tools/list" || len(request.ID) == 0 {
			return 27
		}
		if err := writeRawMCPResult(request.ID, map[string]any{
			"tools": []any{map[string]any{
				"name":        "text.stats",
				"description": "raw describe cleanup test",
				"inputSchema": map[string]any{
					"type":                 "object",
					"properties":           map[string]any{},
					"additionalProperties": false,
				},
			}},
		}); err != nil {
			return 28
		}
	case "execute-close-error":
		request, err := readRawMCPRequest(reader)
		if err != nil || request.Method != "tools/call" || len(request.ID) == 0 {
			return 29
		}
		if eventPath != "" {
			if err := appendMCPHelperEvent(eventPath, "tool_call\n"); err != nil {
				return 30
			}
		}
		if err := writeRawMCPResult(request.ID, map[string]any{
			"content": []any{map[string]any{
				"type": "text",
				"text": "confirmed",
			}},
			"isError": false,
		}); err != nil {
			return 31
		}
	case "post-call-id-mismatch", "post-call-malformed", "post-call-oversize":
		request, err := readRawMCPRequest(reader)
		if err != nil || request.Method != "tools/call" || len(request.ID) == 0 {
			return 34
		}
		if eventPath != "" {
			if err := appendMCPHelperEvent(eventPath, "tool_call\n"); err != nil {
				return 35
			}
		}
		switch mode {
		case "post-call-id-mismatch":
			if err := writeRawMCPResult(json.RawMessage(`"wrong-id"`), map[string]any{
				"content": []any{},
			}); err != nil {
				return 36
			}
		case "post-call-malformed":
			if _, err := io.WriteString(os.Stdout, "not-json\n"); err != nil {
				return 37
			}
		case "post-call-oversize":
			if err := writeRawMCPResult(request.ID, map[string]any{
				"content": []any{map[string]any{
					"type": "text",
					"text": strings.Repeat("x", 300<<10),
				}},
			}); err != nil {
				return 38
			}
		}
	default:
		return 32
	}

	_, _ = io.Copy(io.Discard, reader)
	if eventPath != "" {
		if err := appendMCPHelperEvent(eventPath, "process_exit\n"); err != nil {
			return 33
		}
	}
	if mode == "describe-close-error" {
		return 78
	}
	if mode == "execute-close-error" {
		return 79
	}
	return 0
}

type rawMCPRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func readRawMCPRequest(reader *bufio.Reader) (rawMCPRequest, error) {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return rawMCPRequest{}, err
	}
	var request rawMCPRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return rawMCPRequest{}, err
	}
	return request, nil
}

func writeRawMCPResult(id json.RawMessage, result any) error {
	response, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(response, '\n'))
	return err
}

func runBlockedToolCallWriteHelper(eventPath string) int {
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return 9
	}
	var initialize struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	if err := json.Unmarshal(line, &initialize); err != nil ||
		initialize.Method != "initialize" || len(initialize.ID) == 0 {
		return 10
	}
	response, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{
		JSONRPC: "2.0",
		ID:      initialize.ID,
		Result: map[string]any{
			"protocolVersion": ProtocolVersionV1,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "blocked-write-test",
				"version": "1.0.0",
			},
		},
	})
	if err != nil {
		return 11
	}
	if _, err := os.Stdout.Write(append(response, '\n')); err != nil {
		return 12
	}

	line, err = reader.ReadBytes('\n')
	if err != nil {
		return 13
	}
	var initialized struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(line, &initialized); err != nil ||
		initialized.Method != "notifications/initialized" {
		return 14
	}
	if err := appendMCPHelperEvent(eventPath, "blocked_write_ready\n"); err != nil {
		return 15
	}
	// The server has completed initialization but deliberately never reads the
	// following tools/call frame. The client deadline must still be reachable.
	time.Sleep(30 * time.Second)
	return 0
}

func mcpHelperEventCount(path, event string) int {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(content), event)
}

func appendMCPHelperEvent(path, event string) (returnErr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, file.Close())
	}()
	if _, err := file.WriteString(event); err != nil {
		return err
	}
	return file.Sync()
}

func assertMCPHelperEvents(t *testing.T, path string, wantStarts, wantCalls int) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read MCP helper events: %v", err)
	}
	if starts := strings.Count(string(content), "process_start\n"); starts != wantStarts {
		t.Fatalf("MCP helper starts=%d want=%d; events=%q", starts, wantStarts, content)
	}
	if calls := strings.Count(string(content), "tool_call\n"); calls != wantCalls {
		t.Fatalf("MCP helper calls=%d want=%d; events=%q", calls, wantCalls, content)
	}
}

func jsonEqual(left, right []byte) bool {
	var leftValue any
	var rightValue any
	return json.Unmarshal(left, &leftValue) == nil &&
		json.Unmarshal(right, &rightValue) == nil &&
		strings.TrimSpace(string(left)) == strings.TrimSpace(string(right))
}
