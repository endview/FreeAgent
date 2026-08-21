// Package mcpstdio adapts one exact, operator-approved local MCP stdio
// process to FreeAgent's existing action.provider/v1 and private Action
// executor boundaries. It does not add a second runtime or effect ledger.
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
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	// AdapterIdentityV1 is the only compiled Host adapter identity for the
	// first local MCP Tool slice.
	AdapterIdentityV1 = "freeagent.adapter.mcp.stdio-tools/v1"

	HostDescriptorSchemaV1   = "freeagent.mcp-stdio-host/v1"
	ProtocolVersionV1        = "2025-11-25"
	MaxHostDescriptorBytesV1 = 64 << 10

	defaultStartupTimeout = 10 * time.Second
	defaultCallTimeout    = 60 * time.Second
	defaultCloseTimeout   = 5 * time.Second
	defaultMaxFrameBytes  = 1 << 20

	// The executable is an ordinary artifact file and therefore shares the
	// package scanner's hard per-file ceiling.
	maxExecutableBytes  = moduleapi.DefaultArtifactMaxFileBytes
	maxServerTools      = 256
	maxCursorBytes      = 4 << 10
	maxCursorTotalBytes = 64 << 10
	maxArguments        = 64
	maxArgumentBytes    = 4096
)

var (
	ErrInvalidDescriptor = errors.New("mcpstdio: invalid host descriptor")
	ErrArtifactDrift     = errors.New("mcpstdio: artifact drift")
)

// HostDescriptorV1 is immutable artifact-owned launch metadata. It may
// request effects, but local Action Config and Authority remain authoritative.
// Secrets and inherited environment are deliberately absent from this slice.
type HostDescriptorV1 struct {
	SchemaVersion        string          `json:"schema_version"`
	ProtocolVersion      string          `json:"protocol_version"`
	Executable           string          `json:"executable"`
	ExecutableSHA256     string          `json:"executable_sha256"`
	Arguments            []string        `json:"arguments,omitempty"`
	WorkingDirectory     string          `json:"working_directory"`
	Tools                []ToolBindingV1 `json:"tools"`
	StartupTimeoutMillis uint32          `json:"startup_timeout_ms,omitempty"`
	CallTimeoutMillis    uint32          `json:"call_timeout_ms,omitempty"`
	CloseTimeoutMillis   uint32          `json:"close_timeout_ms,omitempty"`
	MaxFrameBytes        uint32          `json:"max_frame_bytes,omitempty"`
}

// ToolBindingV1 gives one stable FreeAgent provider ID to one exact MCP tool
// name. Tool annotations cannot change these requested bounds.
type ToolBindingV1 struct {
	ProviderActionID        string                `json:"provider_action_id"`
	RequestedEffectClass    moduleapi.EffectClass `json:"requested_effect_class"`
	RequestedMaxResultBytes uint32                `json:"requested_max_result_bytes"`
}

type launchPlan struct {
	artifactDirectory string
	executable        string
	executableDigest  string
	arguments         []string
	workingDirectory  string
	tools             []ToolBindingV1
	toolByProvider    map[string]ToolBindingV1
	startupTimeout    time.Duration
	callTimeout       time.Duration
	closeTimeout      time.Duration
	maxFrameBytes     int
}

func parseLaunchPlan(
	ctx context.Context,
	artifactDirectory string,
	descriptorPath string,
	descriptorCanonical []byte,
) (launchPlan, error) {
	if ctx == nil {
		return launchPlan{}, fmt.Errorf("%w: context is nil", ErrInvalidDescriptor)
	}
	if err := ctx.Err(); err != nil {
		return launchPlan{}, fmt.Errorf("%w: context: %w", ErrInvalidDescriptor, err)
	}
	if len(descriptorCanonical) == 0 || len(descriptorCanonical) > MaxHostDescriptorBytesV1 {
		return launchPlan{}, fmt.Errorf("%w: descriptor size is outside bounds", ErrInvalidDescriptor)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		descriptorCanonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaxHostDescriptorBytesV1,
			MaxDepth: 32,
			MaxNodes: 4096,
		},
	)
	if err != nil || !bytes.Equal(canonical, descriptorCanonical) || canonical[0] != '{' {
		return launchPlan{}, fmt.Errorf("%w: descriptor must be exact canonical JSON", ErrInvalidDescriptor)
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var descriptor HostDescriptorV1
	if err := decoder.Decode(&descriptor); err != nil {
		return launchPlan{}, fmt.Errorf("%w: decode: %v", ErrInvalidDescriptor, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return launchPlan{}, fmt.Errorf("%w: trailing JSON", ErrInvalidDescriptor)
	}
	if descriptor.SchemaVersion != HostDescriptorSchemaV1 {
		return launchPlan{}, fmt.Errorf("%w: schema_version must be %q", ErrInvalidDescriptor, HostDescriptorSchemaV1)
	}
	if descriptor.ProtocolVersion != ProtocolVersionV1 {
		return launchPlan{}, fmt.Errorf("%w: protocol_version must be %q", ErrInvalidDescriptor, ProtocolVersionV1)
	}
	if descriptorPath == "" {
		return launchPlan{}, fmt.Errorf("%w: descriptor path is absent", ErrInvalidDescriptor)
	}
	if normalized, err := moduleapi.NormalizeArtifactPath(descriptorPath); err != nil ||
		normalized != descriptorPath || !strings.HasPrefix(normalized, "content/") {
		return launchPlan{}, fmt.Errorf("%w: descriptor path is not canonical content/", ErrInvalidDescriptor)
	}
	artifactDirectory, err = resolveArtifactRoot(artifactDirectory)
	if err != nil {
		return launchPlan{}, fmt.Errorf("%w: artifact root: %v", ErrInvalidDescriptor, err)
	}
	executable, err := resolveArtifactPath(artifactDirectory, descriptor.Executable, false)
	if err != nil {
		return launchPlan{}, fmt.Errorf("%w: executable: %v", ErrInvalidDescriptor, err)
	}
	workingDirectory, err := resolveArtifactPath(artifactDirectory, descriptor.WorkingDirectory, true)
	if err != nil {
		return launchPlan{}, fmt.Errorf("%w: working directory: %v", ErrInvalidDescriptor, err)
	}
	if !moduleapi.ValidSHA256(descriptor.ExecutableSHA256) {
		return launchPlan{}, fmt.Errorf("%w: executable_sha256 is invalid", ErrInvalidDescriptor)
	}
	if len(descriptor.Arguments) > maxArguments {
		return launchPlan{}, fmt.Errorf("%w: too many arguments", ErrInvalidDescriptor)
	}
	arguments := append([]string(nil), descriptor.Arguments...)
	for index, argument := range arguments {
		if !validLaunchText(argument, maxArgumentBytes, true) {
			return launchPlan{}, fmt.Errorf("%w: argument %d is invalid", ErrInvalidDescriptor, index)
		}
	}
	if len(descriptor.Tools) == 0 || len(descriptor.Tools) > moduleapi.MaxActionsPerMemberV1 {
		return launchPlan{}, fmt.Errorf("%w: tools must contain between 1 and %d entries", ErrInvalidDescriptor, moduleapi.MaxActionsPerMemberV1)
	}
	tools := append([]ToolBindingV1(nil), descriptor.Tools...)
	byProvider := make(map[string]ToolBindingV1, len(tools))
	for index, tool := range tools {
		probe := moduleapi.ActionBindingMappingV1{
			PublicActionID:   tool.ProviderActionID,
			ProviderActionID: tool.ProviderActionID,
			LocalEffectClass: tool.RequestedEffectClass,
			MaxResultBytes:   tool.RequestedMaxResultBytes,
		}
		if err := probe.Validate(); err != nil {
			return launchPlan{}, fmt.Errorf("%w: tool %d: %v", ErrInvalidDescriptor, index, err)
		}
		if !validMCPToolName(tool.ProviderActionID) {
			return launchPlan{}, fmt.Errorf("%w: tool %d provider action ID is not a valid MCP tool name", ErrInvalidDescriptor, index)
		}
		if _, duplicate := byProvider[tool.ProviderActionID]; duplicate {
			return launchPlan{}, fmt.Errorf("%w: duplicate provider action ID %q", ErrInvalidDescriptor, tool.ProviderActionID)
		}
		byProvider[tool.ProviderActionID] = tool
	}
	sort.Slice(tools, func(left, right int) bool {
		return tools[left].ProviderActionID < tools[right].ProviderActionID
	})
	startupTimeout, err := boundedDuration(descriptor.StartupTimeoutMillis, defaultStartupTimeout, 60*time.Second)
	if err != nil {
		return launchPlan{}, fmt.Errorf("%w: startup timeout: %v", ErrInvalidDescriptor, err)
	}
	callTimeout, err := boundedDuration(descriptor.CallTimeoutMillis, defaultCallTimeout, 5*time.Minute)
	if err != nil {
		return launchPlan{}, fmt.Errorf("%w: call timeout: %v", ErrInvalidDescriptor, err)
	}
	closeTimeout, err := boundedDuration(descriptor.CloseTimeoutMillis, defaultCloseTimeout, 30*time.Second)
	if err != nil {
		return launchPlan{}, fmt.Errorf("%w: close timeout: %v", ErrInvalidDescriptor, err)
	}
	maxFrame := int(descriptor.MaxFrameBytes)
	if maxFrame == 0 {
		maxFrame = defaultMaxFrameBytes
	}
	if maxFrame < 4096 || maxFrame > defaultMaxFrameBytes {
		return launchPlan{}, fmt.Errorf("%w: max_frame_bytes must be between 4096 and %d", ErrInvalidDescriptor, defaultMaxFrameBytes)
	}
	plan := launchPlan{
		artifactDirectory: artifactDirectory,
		executable:        executable,
		executableDigest:  descriptor.ExecutableSHA256,
		arguments:         arguments,
		workingDirectory:  workingDirectory,
		tools:             tools,
		toolByProvider:    byProvider,
		startupTimeout:    startupTimeout,
		callTimeout:       callTimeout,
		closeTimeout:      closeTimeout,
		maxFrameBytes:     maxFrame,
	}
	if err := plan.verifyExecutableContext(ctx); err != nil {
		return launchPlan{}, err
	}
	return plan, nil
}

func (plan launchPlan) prepareExecutable() error {
	return plan.verifyExecutableMode()
}

func (plan launchPlan) verifyExecutableContext(ctx context.Context) (returnErr error) {
	if ctx == nil {
		return fmt.Errorf("%w: executable verification context is nil", ErrArtifactDrift)
	}
	if err := plan.verifyExecutableMode(); err != nil {
		return err
	}
	file, err := os.Open(plan.executable)
	if err != nil {
		return fmt.Errorf("%w: open executable: %v", ErrArtifactDrift, err)
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	digest := sha256.New()
	reader := io.LimitReader(file, maxExecutableBytes+1)
	buffer := make([]byte, 32<<10)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: hash executable: %w", ErrArtifactDrift, err)
		}
		count, readErr := reader.Read(buffer)
		if count > 0 {
			written += int64(count)
			_, _ = digest.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("%w: hash executable: %v", ErrArtifactDrift, readErr)
		}
	}
	if written > maxExecutableBytes {
		return fmt.Errorf("%w: executable exceeds %d bytes", ErrArtifactDrift, maxExecutableBytes)
	}
	if hex.EncodeToString(digest.Sum(nil)) != plan.executableDigest {
		return fmt.Errorf("%w: executable digest mismatch", ErrArtifactDrift)
	}
	return nil
}

func (plan launchPlan) verifyExecutableMode() error {
	info, err := os.Lstat(plan.executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: exact executable is absent or not a regular file", ErrArtifactDrift)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%w: exact executable has no execute permission", ErrArtifactDrift)
	}
	return nil
}

func resolveArtifactPath(root, artifactPath string, wantDirectory bool) (string, error) {
	normalized, err := moduleapi.NormalizeArtifactPath(artifactPath)
	insideContent := strings.HasPrefix(normalized, "content/") ||
		(wantDirectory && normalized == "content")
	if err != nil || normalized != artifactPath || !insideContent {
		return "", errors.New("path must be canonical content/ relative path")
	}
	resolvedRoot, err := resolveArtifactRoot(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(resolvedRoot, filepath.FromSlash(normalized))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || !pathWithin(resolvedRoot, resolved) {
		return "", errors.New("path escapes artifact root or traverses a symlink")
	}
	if !samePath(candidate, resolved) {
		return "", errors.New("path must not traverse a symlink")
	}
	info, err := os.Stat(resolved)
	if err != nil || (wantDirectory && !info.IsDir()) || (!wantDirectory && !info.Mode().IsRegular()) {
		return "", errors.New("path has the wrong file type")
	}
	return resolved, nil
}

func resolveArtifactRoot(root string) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil || !samePath(absoluteRoot, resolvedRoot) {
		return "", errors.New("artifact root must not traverse a symlink")
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil || !info.IsDir() {
		return "", errors.New("artifact root must be an existing directory")
	}
	return resolvedRoot, nil
}

func boundedDuration(milliseconds uint32, fallback, maximum time.Duration) (time.Duration, error) {
	if milliseconds == 0 {
		return fallback, nil
	}
	value := time.Duration(milliseconds) * time.Millisecond
	if value < 100*time.Millisecond || value > maximum {
		return 0, errors.New("duration is outside bounds")
	}
	return value, nil
}

func validMCPToolName(value string) bool {
	if !validLaunchText(value, 128, false) {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validLaunchText(value string, maximum int, allowEmpty bool) bool {
	if (!allowEmpty && value == "") || len(value) > maximum ||
		!utf8.ValidString(value) || value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
