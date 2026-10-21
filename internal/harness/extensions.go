package harness

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/buildinfo"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const (
	extensionProtocol        = "freeagent-ext/1"
	defaultExtensionTimeout  = 2000
	extensionStartupTimeout  = 10 * time.Second
	defaultExtensionChars    = 2000
	maxExtensionManifest     = 1 << 20
	maxExtensionBlocks       = 128
	maxExtensionBlockChars   = 1 << 20
	maxExtensionFiles        = 256
	maxExtensionTotalBytes   = 8 << 20
	maxExtensionHistoryMsgs  = 128
	maxExtensionHistoryBytes = 64 << 10
	// turn.finished always receives the settled output, cut to this size.
	maxExtensionFinishedOutput = 64 << 10
	// Host turn cap used when the configuration leaves it unset.
	maxExtensionModelCalls = 128
	// Shorter plain environment values are usually flags, not credentials.
	minRedactedEnvValue = 8
)

var (
	errExtensionInvalidInvocation = &jsonrpc.Error{Code: -32005, Message: "invalid invocation"}
)

func workflowOutcomeUnknown(err error) bool {
	if err == nil {
		return false
	}
	var callErr *CallError
	if errors.As(err, &callErr) && !callErr.Certain {
		return true
	}
	if errors.Is(err, ErrUnknownResult) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeLimit) ||
		errors.Is(err, mcpstdio.ErrRPCClosed) {
		return true
	}
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) {
		return rpcErr.Code == -32002 || rpcErr.Code == -32006
	}
	return false
}

type ExtensionManifest struct {
	Protocol       string              `json:"protocol"`
	ID             string              `json:"id"`
	Version        string              `json:"version"`
	Name           string              `json:"name"`
	Description    string              `json:"description"`
	Run            ExtensionRun        `json:"run"`
	Env            []string            `json:"env,omitempty"`
	SettingsSchema json.RawMessage     `json:"settings_schema,omitempty"`
	Needs          []string            `json:"needs,omitempty"`
	Context        ExtensionContext    `json:"context,omitempty"`
	Events         []string            `json:"events,omitempty"`
	Workflows      []ExtensionWorkflow `json:"workflows,omitempty"`
	Tools          bool                `json:"tools,omitempty"`
	Budget         ExtensionBudget     `json:"budget,omitempty"`
	TimeoutMS      int                 `json:"timeout_ms,omitempty"`
	Required       bool                `json:"required,omitempty"`
}

type ExtensionRun struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
}

type ExtensionContext struct {
	Phases    []string `json:"phases,omitempty"`
	Purposes  []string `json:"purposes,omitempty"`
	MaxChars  int      `json:"max_chars,omitempty"`
	Placement []string `json:"placement,omitempty"`
}

type ExtensionWorkflow struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	InputSchema   json.RawMessage `json:"input_schema,omitempty"`
	AgentCallable *bool           `json:"agent_callable,omitempty"`
}

type ExtensionBudget struct {
	ModelCalls   int `json:"model_calls,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

type extensionDefinition struct {
	Manifest ExtensionManifest
	Root     string
	Source   string
}

type extensionRegistry struct {
	Extensions map[string]extensionDefinition
	Errors     map[string]error
}

type ExtensionBlock struct {
	ExtensionID string `json:"extension_id"`
	Version     string `json:"version,omitempty"`
	ID          string `json:"id"`
	Text        string `json:"text"`
	Phase       string `json:"phase,omitempty"`
	Placement   string `json:"placement"`
	Label       string `json:"label,omitempty"`
	Priority    int    `json:"priority"`
	SHA256      string `json:"sha256"`
	Bytes       int    `json:"bytes"`
	Chars       int    `json:"chars"`
}

type extensionPrepareResult struct {
	Blocks []struct {
		ID        string `json:"id"`
		Text      string `json:"text"`
		Placement string `json:"placement"`
		Label     string `json:"label,omitempty"`
		Priority  *int   `json:"priority,omitempty"`
	} `json:"blocks,omitempty"`
}

type extensionOperationCheckResult struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

type extensionInitializeResult struct {
	ID        string   `json:"id"`
	Version   string   `json:"version"`
	Provides  []string `json:"provides,omitempty"`
	Events    []string `json:"events,omitempty"`
	Workflows []string `json:"workflows,omitempty"`
}

type extensionInput struct {
	Invocation     string         `json:"invocation"`
	Phase          string         `json:"phase"`
	Purpose        string         `json:"purpose"`
	Step           *int           `json:"step,omitempty"`
	Message        any            `json:"message,omitempty"`
	History        []ModelMessage `json:"history,omitempty"`
	Workspace      string         `json:"workspace,omitempty"`
	DataDir        string         `json:"data_dir,omitempty"`
	ToolResults    any            `json:"tool_results,omitempty"`
	Settings       map[string]any `json:"settings,omitempty"`
	Output         string         `json:"output,omitempty"`
	Status         string         `json:"status,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
}

type extensionHost struct {
	startMu   sync.Mutex
	mu        sync.Mutex
	dataDir   string
	workspace string
	logDir    string
	config    Config
	keys      *KeyStore
	clients   map[string]*extensionClient
	secrets   []string
	active    map[string]*extensionInvocation
	usage     map[string]*extensionRunUsage
	fixture   *extensionFixtureReplay
	closed    bool
}

type extensionInvocation struct {
	id          string
	extensionID string
	service     *Service
	run         Run
	manifest    ExtensionManifest
	ctx         context.Context
	cancel      context.CancelFunc
	// mu serializes task.delegate within one invocation.
	mu    sync.Mutex
	usage *extensionRunUsage
}

// extensionRunUsage is the model budget one extension has spent in one Run.
// Every hook opens a new invocation, so the counters live on the Host and are
// shared by all hooks of the Run until it settles. The lock is held only for
// bookkeeping, never across a model call.
type extensionRunUsage struct {
	mu     sync.Mutex
	calls  int
	output int
}

// reserve books calls and output when both still fit the limits. A zero
// output limit means the manifest declared no output budget.
func (usage *extensionRunUsage) reserve(calls, output, maxCalls, maxOutput int) bool {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	if usage.calls+calls > maxCalls || maxOutput > 0 && usage.output+output > maxOutput {
		return false
	}
	usage.calls += calls
	usage.output += output
	return true
}

// settle replaces part of a reservation with observed usage and reports
// whether the output budget still holds.
func (usage *extensionRunUsage) settle(calls, output, maxOutput int) bool {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	usage.calls += calls
	usage.output += output
	return maxOutput <= 0 || usage.output <= maxOutput
}

type extensionClient struct {
	definition   ExtensionDefinitionView
	rpc          *mcpstdio.RPCClient
	ready        bool
	signature    string
	registration extensionInitializeResult
	toolsMu      sync.Mutex
	toolsLoaded  bool
	tools        []extensionRuntimeTool
}

// ExtensionDefinitionView is the safe catalog representation used by callers
// and the UI. The process path and private data path are intentionally absent.
type ExtensionDefinitionView struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Provides    []string `json:"provides,omitempty"`
	Enabled     bool     `json:"enabled"`
	Required    bool     `json:"required"`
}

// ExtensionInfo is the catalog representation exposed to local callers and
// the Chat UI. It intentionally omits executable paths, arguments, and
// environment values.
type ExtensionInfo struct {
	ID               string              `json:"id"`
	Version          string              `json:"version"`
	Name             string              `json:"name"`
	Description      string              `json:"description"`
	Source           string              `json:"source"`
	Provides         []string            `json:"provides,omitempty"`
	Workflows        []ExtensionWorkflow `json:"workflows,omitempty"`
	Tools            bool                `json:"tools"`
	SettingsSchema   json.RawMessage     `json:"settings_schema,omitempty"`
	Context          ExtensionContext    `json:"context,omitempty"`
	Enabled          bool                `json:"enabled"`
	Required         bool                `json:"required"`
	Available        bool                `json:"available"`
	Trusted          bool                `json:"trusted"`
	WorkspaceTrusted bool                `json:"workspace_trusted"`
	Error            string              `json:"error,omitempty"`
	Settings         map[string]any      `json:"settings,omitempty"`
	EnvFrom          map[string]string   `json:"env_from,omitempty"`
	Env              []string            `json:"env,omitempty"`
	Budget           ExtensionBudget     `json:"budget"`
	State            string              `json:"state"`
}

// ExtensionCheckResult is the bounded result of validating and probing one
// extension folder. It intentionally exposes no executable arguments or
// environment values.
type ExtensionCheckResult struct {
	Info     ExtensionInfo `json:"extension"`
	Provides []string      `json:"provides,omitempty"`
	Probes   []string      `json:"probes,omitempty"`
}

// ExtensionFixtureTurn is the input accepted by the lightweight development
// fixture runner.
type ExtensionFixtureTurn struct {
	Message   string         `json:"message,omitempty"`
	History   []ModelMessage `json:"history,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Model     string         `json:"model,omitempty"`
}

type ExtensionFixture struct {
	Turn     ExtensionFixtureTurn `json:"turn"`
	Workflow string               `json:"workflow,omitempty"`
	Input    map[string]any       `json:"input,omitempty"`
	Host     map[string]any       `json:"host,omitempty"`
	Expect   json.RawMessage      `json:"expect,omitempty"`
}

type ExtensionFixtureResult struct {
	Info     ExtensionInfo    `json:"extension"`
	Blocks   []ExtensionBlock `json:"blocks,omitempty"`
	Output   string           `json:"output,omitempty"`
	Activity map[string]any   `json:"activity,omitempty"`
	Matched  bool             `json:"matched"`
}

type extensionFixtureReplay struct {
	host          map[string]any
	generates     []any
	generateIndex int
}

func (host *extensionHost) fixtureGenerate() (map[string]any, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.fixture == nil {
		return nil, errExtensionUnavailable
	}
	value := host.fixture.host["model.generate"]
	if values, ok := value.([]any); ok {
		if host.fixture.generateIndex >= len(values) {
			return nil, &jsonrpc.Error{Code: -32004, Message: "fixture model.generate responses exhausted"}
		}
		value = values[host.fixture.generateIndex]
		host.fixture.generateIndex++
	}
	if value == nil {
		return map[string]any{"content": "fixture response"}, nil
	}
	if text, ok := value.(string); ok {
		return map[string]any{"content": text}, nil
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, &jsonrpc.Error{Code: -32600, Message: "fixture model.generate response must be an object"}
	}
	return result, nil
}

func (host *extensionHost) fixtureEmbed(inputs []string) (map[string]any, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.fixture == nil {
		return nil, errExtensionUnavailable
	}
	if value, ok := host.fixture.host["model.embed"]; ok {
		if result, ok := value.(map[string]any); ok {
			return result, nil
		}
		if text, ok := value.(string); !ok || text != "deterministic" {
			return nil, &jsonrpc.Error{Code: -32600, Message: "fixture model.embed must be deterministic or an object"}
		}
	}
	vectors := make([][]float64, len(inputs))
	for index, input := range inputs {
		digest := sha256.Sum256([]byte(input))
		vector := make([]float64, 8)
		var length float64
		for dimension := range vector {
			vector[dimension] = float64(digest[dimension]) / 255
			length += vector[dimension] * vector[dimension]
		}
		length = math.Sqrt(length)
		if length != 0 {
			for dimension := range vector {
				vector[dimension] /= length
			}
		}
		vectors[index] = vector
	}
	return map[string]any{"vectors": vectors}, nil
}

func (host *extensionHost) fixtureDelegate() (map[string]any, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.fixture == nil {
		return nil, errExtensionUnavailable
	}
	value := host.fixture.host["task.delegate"]
	result, ok := value.(map[string]any)
	if !ok {
		return nil, &jsonrpc.Error{Code: -32600, Message: "fixture task.delegate response must be an object"}
	}
	return result, nil
}

func (host *extensionHost) fixtureTool(name string) (map[string]any, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.fixture == nil {
		return nil, errExtensionUnavailable
	}
	value, ok := host.fixture.host["tool.call"].(map[string]any)
	if !ok {
		return nil, &jsonrpc.Error{Code: -32600, Message: "fixture tool.call response must be an object"}
	}
	result, ok := value[name].(map[string]any)
	if !ok {
		return nil, &jsonrpc.Error{Code: -32004, Message: "fixture tool response is missing"}
	}
	return result, nil
}

type extensionActivityResult struct {
	Activity map[string]any `json:"activity,omitempty"`
}

type extensionWorkflowResult struct {
	Output   string         `json:"output,omitempty"`
	Activity map[string]any `json:"activity,omitempty"`
}

type extensionToolCallResult struct {
	Content  string         `json:"content,omitempty"`
	IsError  bool           `json:"is_error,omitempty"`
	Activity map[string]any `json:"activity,omitempty"`
}

type extensionHostToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Args      json.RawMessage `json:"args,omitempty"`
}

type extensionToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	ReadOnly    bool            `json:"readOnly,omitempty"`
}

type extensionToolsListResult struct {
	Tools []extensionToolDefinition `json:"tools,omitempty"`
}

type extensionRuntimeTool struct {
	ExtensionID string
	Name        string
	Workflow    string
	Definition  ToolDefinition
	Input       *jsonschema.Resolved
}

func validateExtensionManifest(manifest ExtensionManifest) error {
	if manifest.Protocol != extensionProtocol || !validExtensionID(manifest.ID) ||
		manifest.Version == "" || len(manifest.Version) > 128 ||
		len(manifest.Name) > 256 || len(manifest.Description) > 4096 {
		return errors.New("invalid extension protocol, id, version, name, or description")
	}
	if manifest.Run.Command == "" || strings.ContainsAny(manifest.Run.Command, "\x00\r\n") ||
		len(manifest.Run.Args) > 128 {
		return errors.New("invalid extension run command or args")
	}
	for _, arg := range manifest.Run.Args {
		if strings.ContainsRune(arg, 0) || len(arg) > 8192 {
			return errors.New("invalid extension argument")
		}
	}
	if manifest.Run.Cwd != "" && !safeRelativePath(manifest.Run.Cwd) {
		return errors.New("extension cwd must stay inside the extension directory")
	}
	for _, env := range manifest.Env {
		if !validEnvName(env) {
			return errors.New("invalid extension environment name")
		}
	}
	if len(manifest.SettingsSchema) > 64<<10 {
		return errors.New("extension settings schema is too large")
	}
	if len(manifest.SettingsSchema) != 0 {
		if _, err := resolveToolSchema(manifest.SettingsSchema); err != nil {
			return errors.New("extension settings schema must be a supported local object schema")
		}
	}
	for _, need := range manifest.Needs {
		if need != "message" && need != "workspace" && need != "tool_results" &&
			need != "output" && need != "status" &&
			!strings.HasPrefix(need, "history:") {
			return fmt.Errorf("unsupported extension need %q", need)
		}
		if strings.HasPrefix(need, "history:") {
			n, err := strconv.Atoi(strings.TrimPrefix(need, "history:"))
			if err != nil || n < 1 || n > 64 {
				return errors.New("extension history need is out of range")
			}
		}
	}
	if err := validateExtensionContext(manifest.Context); err != nil {
		return err
	}
	for _, event := range manifest.Events {
		if event != "turn.finished" && event != "operation.check" {
			return fmt.Errorf("unsupported extension event %q", event)
		}
	}
	if len(manifest.Workflows) > 64 {
		return errors.New("too many extension workflows")
	}
	for _, workflow := range manifest.Workflows {
		if !validCapabilityID(workflow.Name) || len(workflow.Description) > 4096 {
			return errors.New("invalid extension workflow")
		}
		if len(workflow.InputSchema) > 64<<10 {
			return errors.New("extension workflow schema is too large")
		}
	}
	if manifest.Budget.ModelCalls < 0 || manifest.Budget.ModelCalls > 128 ||
		manifest.Budget.OutputTokens < 0 || manifest.Budget.OutputTokens > 1<<20 {
		return errors.New("extension budget is out of range")
	}
	if manifest.TimeoutMS == 0 {
		manifest.TimeoutMS = defaultExtensionTimeout
	}
	if manifest.TimeoutMS < 100 || manifest.TimeoutMS > 60000 {
		return errors.New("extension timeout is out of range")
	}
	return nil
}

// ValidateExtensionID exposes the same identifier boundary used by runtime
// discovery and import without exposing the private registry implementation.
func ValidateExtensionID(id string) error {
	if !validExtensionID(id) {
		return errors.New("harness: invalid extension identifier")
	}
	return nil
}

func validateExtensionContext(context ExtensionContext) error {
	if context.MaxChars < 0 || context.MaxChars > maxExtensionBlockChars {
		return errors.New("extension context max_chars is out of range")
	}
	if len(context.Phases) == 0 {
		context.Phases = []string{"turn"}
	}
	for _, phase := range context.Phases {
		if phase != "turn" && phase != "step" {
			return errors.New("extension context phase is invalid")
		}
	}
	for _, purpose := range context.Purposes {
		if purpose != "chat" && purpose != "child" && purpose != "planning" && purpose != "synthesis" {
			return errors.New("extension context purpose is invalid")
		}
	}
	if len(context.Placement) == 0 {
		context.Placement = []string{"tail"}
	}
	for _, placement := range context.Placement {
		if placement != "tail" && placement != "system" {
			return errors.New("extension context placement is invalid")
		}
	}
	return nil
}

func validExtensionID(id string) bool {
	if len(id) < 3 || len(id) > 128 || strings.Count(id, ".") < 1 {
		return false
	}
	parts := strings.Split(id, ".")
	for _, part := range parts {
		if part == "" || !validCapabilityID(part) {
			return false
		}
	}
	return true
}

func safeRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	return filepath.VolumeName(path) == "" && !strings.ContainsRune(path, 0) &&
		clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func scanExtensions(dataDir, workspace string, config Config) (extensionRegistry, error) {
	registry := extensionRegistry{Extensions: map[string]extensionDefinition{}, Errors: map[string]error{}}
	if err := scanExtensionRoot(&registry, filepath.Join(dataDir, "extensions"), "user"); err != nil {
		return registry, err
	}
	// Canonicalizing walks every path component, so skip it when no
	// workspace can be trusted.
	if len(config.Extensions.TrustedWorkspaces) == 0 {
		return registry, nil
	}
	canonicalWorkspace, err := canonicalDir(workspace)
	if err == nil && config.Extensions.TrustedWorkspaces[canonicalWorkspace] {
		if err := scanExtensionRoot(&registry, filepath.Join(canonicalWorkspace, ".freeagent", "extensions"), "workspace"); err != nil {
			return registry, err
		}
	}
	return registry, nil
}

func scanExtensionRoot(registry *extensionRegistry, root, source string) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !validExtensionID(entry.Name()) {
			continue
		}
		manifestPath := filepath.Join(root, entry.Name(), "extension.json")
		raw, err := readBoundedFile(manifestPath, maxExtensionManifest)
		if err != nil {
			registry.Errors[entry.Name()] = fmt.Errorf("extension %q: read manifest: %w", entry.Name(), err)
			delete(registry.Extensions, entry.Name())
			continue
		}
		manifest, err := parseExtensionManifest(raw)
		if err != nil {
			registry.Errors[entry.Name()] = fmt.Errorf("extension %q: %w", entry.Name(), err)
			delete(registry.Extensions, entry.Name())
			continue
		}
		if manifest.ID != entry.Name() {
			registry.Errors[entry.Name()] = fmt.Errorf("extension directory %q does not match id %q", entry.Name(), manifest.ID)
			delete(registry.Extensions, entry.Name())
			continue
		}
		definition := extensionDefinition{Manifest: manifest, Root: filepath.Join(root, entry.Name()), Source: source}
		registry.Extensions[manifest.ID] = definition
		delete(registry.Errors, manifest.ID)
	}
	return nil
}

// extensionManifests memoizes decoding and validation by manifest content.
// Both depend only on the bytes, and every scan still reads the current file,
// so an edited, added, or removed manifest is seen on the next lookup. Callers
// share the cached slices and must treat a manifest as read-only.
var extensionManifests = struct {
	sync.Mutex
	parsed map[[sha256.Size]byte]parsedExtensionManifest
}{parsed: map[[sha256.Size]byte]parsedExtensionManifest{}}

const maxParsedExtensionManifests = 256

type parsedExtensionManifest struct {
	manifest ExtensionManifest
	err      error
}

func parseExtensionManifest(raw []byte) (ExtensionManifest, error) {
	key := sha256.Sum256(raw)
	extensionManifests.Lock()
	cached, ok := extensionManifests.parsed[key]
	extensionManifests.Unlock()
	if ok {
		return cached.manifest, cached.err
	}
	var manifest ExtensionManifest
	err := json.Unmarshal(raw, &manifest)
	if err != nil {
		err = fmt.Errorf("decode manifest: %w", err)
	} else {
		err = validateExtensionManifest(manifest)
	}
	extensionManifests.Lock()
	if len(extensionManifests.parsed) >= maxParsedExtensionManifests {
		clear(extensionManifests.parsed)
	}
	extensionManifests.parsed[key] = parsedExtensionManifest{manifest: manifest, err: err}
	extensionManifests.Unlock()
	return manifest, err
}

func (registry extensionRegistry) views(config Config) []ExtensionDefinitionView {
	result := make([]ExtensionDefinitionView, 0, len(registry.Extensions))
	for id, definition := range registry.Extensions {
		result = append(result, ExtensionDefinitionView{ID: id, Version: definition.Manifest.Version,
			Name: definition.Manifest.Name, Description: definition.Manifest.Description,
			Source: definition.Source, Enabled: config.Extensions.Enabled[id],
			Required: definition.Manifest.Required || config.Extensions.Required[id]})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func extensionInfo(definition extensionDefinition, config Config, available, trusted, workspaceTrusted bool, problem error) ExtensionInfo {
	manifest := definition.Manifest
	info := ExtensionInfo{
		ID: manifest.ID, Version: manifest.Version, Name: manifest.Name,
		Description: manifest.Description, Source: definition.Source,
		Provides: []string{}, Workflows: append([]ExtensionWorkflow(nil), manifest.Workflows...),
		Tools: manifest.Tools, SettingsSchema: append(json.RawMessage(nil), manifest.SettingsSchema...),
		Context: manifest.Context, Enabled: available && config.Extensions.Enabled[manifest.ID],
		Required:  manifest.Required || config.Extensions.Required[manifest.ID],
		Available: available, Trusted: trusted, WorkspaceTrusted: workspaceTrusted,
		Settings: config.Extensions.Settings[manifest.ID], EnvFrom: config.Extensions.EnvFrom[manifest.ID],
		Env: manifest.Env, Budget: manifest.Budget, State: "stopped",
	}
	if len(manifest.Context.Phases) != 0 {
		info.Provides = append(info.Provides, "context")
	}
	if len(manifest.Workflows) != 0 {
		info.Provides = append(info.Provides, "workflow")
	}
	if manifest.Tools {
		info.Provides = append(info.Provides, "tools")
	}
	if problem != nil {
		info.Error, info.State = safeError(problem), "error"
	}
	return info
}

// CheckExtension validates one extension directory without starting it or
// resolving credentials. It is the same manifest validation used by runtime
// discovery and is suitable for the CLI and editor integrations.
func CheckExtension(directory string) (ExtensionInfo, error) {
	if strings.TrimSpace(directory) == "" || !filepath.IsAbs(directory) {
		return ExtensionInfo{}, errors.New("harness: extension path must be absolute")
	}
	root, err := canonicalDir(directory)
	if err != nil {
		return ExtensionInfo{}, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return ExtensionInfo{}, errors.New("harness: extension path is not a directory")
	}
	raw, err := readBoundedFile(filepath.Join(root, "extension.json"), maxExtensionManifest)
	if err != nil {
		return ExtensionInfo{}, fmt.Errorf("harness: extension manifest: %w", err)
	}
	var manifest ExtensionManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ExtensionInfo{}, fmt.Errorf("harness: extension manifest: %w", err)
	}
	if err := validateExtensionManifest(manifest); err != nil {
		return ExtensionInfo{}, err
	}
	if filepath.Base(root) != manifest.ID {
		return ExtensionInfo{}, fmt.Errorf("harness: extension directory %q does not match id %q", filepath.Base(root), manifest.ID)
	}
	definition := extensionDefinition{Manifest: manifest, Root: root, Source: "folder"}
	return extensionInfo(definition, DefaultConfig(root), true, true, true, nil), nil
}

// CheckExtensionProcess checks registration and probes context/tools discovery.
// It never runs workflows, tools, or end-of-turn writeback.
func CheckExtensionProcess(ctx context.Context, directory string) (ExtensionCheckResult, error) {
	if ctx == nil {
		return ExtensionCheckResult{}, errors.New("harness: extension check context is required")
	}
	info, err := CheckExtension(directory)
	if err != nil {
		return ExtensionCheckResult{}, err
	}
	root, err := canonicalDir(directory)
	if err != nil {
		return ExtensionCheckResult{}, err
	}
	raw, err := readBoundedFile(filepath.Join(root, "extension.json"), maxExtensionManifest)
	if err != nil {
		return ExtensionCheckResult{}, err
	}
	var manifest ExtensionManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ExtensionCheckResult{}, err
	}
	dataDir, err := os.MkdirTemp("", "freeagent-ext-check-")
	if err != nil {
		return ExtensionCheckResult{}, err
	}
	defer os.RemoveAll(dataDir)
	config := DefaultConfig(root)
	host := newExtensionHost(dataDir, root, config, nil)
	defer host.close()
	client, err := host.clientFor(ctx, extensionDefinition{
		Manifest: manifest, Root: root, Source: "folder",
	}, config)
	if err != nil {
		return ExtensionCheckResult{}, fmt.Errorf("harness: extension handshake: %w", err)
	}
	result := ExtensionCheckResult{
		Info:     info,
		Provides: append([]string(nil), client.definition.Provides...),
		Probes:   []string{"initialize"},
	}
	expected := make([]string, 0, 3)
	if len(manifest.Context.Phases) != 0 || len(manifest.Context.Purposes) != 0 ||
		manifest.Context.MaxChars != 0 || len(manifest.Context.Placement) != 0 {
		expected = append(expected, "context")
	}
	if len(manifest.Workflows) != 0 {
		expected = append(expected, "workflow")
	}
	if manifest.Tools {
		expected = append(expected, "tools")
	}
	slices.Sort(expected)
	provided := append([]string(nil), result.Provides...)
	slices.Sort(provided)
	if !slices.Equal(expected, provided) {
		return ExtensionCheckResult{}, fmt.Errorf("harness: extension capabilities do not match manifest: manifest=%v process=%v", expected, provided)
	}
	if contains(result.Provides, "context") {
		input := extensionInput{Invocation: newInvocation(), Phase: "turn", Purpose: "chat"}
		applyExtensionNeeds(&input, manifest.Needs, Run{}, nil, root,
			filepath.Join(dataDir, "extensions", manifest.ID, "data"))
		callCtx, cancel := context.WithTimeout(ctx, extensionTimeout(manifest))
		raw, callErr := client.rpc.Call(callCtx, "context.prepare", input)
		cancel()
		if callErr != nil {
			return ExtensionCheckResult{}, fmt.Errorf("harness: context.prepare probe: %w", callErr)
		}
		var probe extensionPrepareResult
		if err := json.Unmarshal(raw, &probe); err != nil {
			return ExtensionCheckResult{}, errors.New("harness: context.prepare probe returned invalid JSON")
		}
		result.Probes = append(result.Probes, "context.prepare")
	}
	if !slices.Equal(uniqueSorted(manifest.Events), uniqueSorted(client.registration.Events)) {
		return ExtensionCheckResult{}, errors.New("harness: extension event registration does not match manifest")
	}
	var workflowNames []string
	for _, workflow := range manifest.Workflows {
		workflowNames = append(workflowNames, workflow.Name)
	}
	if !slices.Equal(uniqueSorted(workflowNames), uniqueSorted(client.registration.Workflows)) {
		return ExtensionCheckResult{}, errors.New("harness: extension workflow registration does not match manifest")
	}
	if manifest.Tools {
		callCtx, cancel := context.WithTimeout(ctx, extensionTimeout(manifest))
		raw, callErr := client.rpc.Call(callCtx, "tools/list", map[string]any{})
		cancel()
		if callErr != nil {
			return ExtensionCheckResult{}, fmt.Errorf("harness: tools/list probe: %w", callErr)
		}
		var tools extensionToolsListResult
		if err := json.Unmarshal(raw, &tools); err != nil {
			return ExtensionCheckResult{}, errors.New("harness: tools/list probe returned invalid JSON")
		}
		seen := map[string]bool{}
		for _, tool := range tools.Tools {
			if !validCapabilityID(tool.Name) || seen[tool.Name] {
				return ExtensionCheckResult{}, errors.New("harness: tools/list probe returned invalid tool names")
			}
			seen[tool.Name] = true
			if len(tool.Description) > 4096 || len(tool.InputSchema) > 64<<10 {
				return ExtensionCheckResult{}, errors.New("harness: tools/list probe returned an oversized tool")
			}
			if _, err := resolveToolSchema(tool.InputSchema); err != nil {
				return ExtensionCheckResult{}, errors.New("harness: tools/list probe returned an invalid schema")
			}
		}
		if len(tools.Tools) == 0 {
			return ExtensionCheckResult{}, errors.New("harness: tools/list probe returned no tools")
		}
		result.Probes = append(result.Probes, "tools/list")
	}
	result.Info.Provides = append([]string(nil), result.Provides...)
	result.Info.State = "checked"
	return result, nil
}

// RunExtensionFixture replays a bounded context or workflow fixture against a
// local extension. Host calls are served by a deterministic, offline replay
// and never reach a configured Provider.
func RunExtensionFixture(ctx context.Context, directory string, fixture ExtensionFixture) (ExtensionFixtureResult, error) {
	if ctx == nil {
		return ExtensionFixtureResult{}, errors.New("harness: extension fixture context is required")
	}
	checked, err := CheckExtension(directory)
	if err != nil {
		return ExtensionFixtureResult{}, err
	}
	root, err := canonicalDir(directory)
	if err != nil {
		return ExtensionFixtureResult{}, err
	}
	raw, err := readBoundedFile(filepath.Join(root, "extension.json"), maxExtensionManifest)
	if err != nil {
		return ExtensionFixtureResult{}, err
	}
	var manifest ExtensionManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ExtensionFixtureResult{}, err
	}
	if len(fixture.Host) != 0 {
		for method := range fixture.Host {
			if method != "model.generate" && method != "model.embed" &&
				method != "task.delegate" && method != "tool.call" &&
				method != "activity.report" {
				return ExtensionFixtureResult{}, fmt.Errorf("harness: unsupported fixture host method %q", method)
			}
		}
	}
	dataDir, err := os.MkdirTemp("", "freeagent-ext-fixture-")
	if err != nil {
		return ExtensionFixtureResult{}, err
	}
	defer os.RemoveAll(dataDir)
	config := DefaultConfig(fixture.Turn.Workspace)
	if config.Workspace == "" {
		config.Workspace = root
	}
	host := newExtensionHost(dataDir, config.Workspace, config, nil)
	host.fixture = &extensionFixtureReplay{host: fixture.Host}
	if values, ok := fixture.Host["model.generate"].([]any); ok {
		host.fixture.generates = values
	}
	defer host.close()
	client, err := host.clientFor(ctx, extensionDefinition{
		Manifest: manifest, Root: root, Source: "folder",
	}, config)
	if err != nil {
		return ExtensionFixtureResult{}, fmt.Errorf("harness: extension handshake: %w", err)
	}
	run := Run{ID: "fixture-run", Prompt: fixture.Turn.Message, Status: "running"}
	input := extensionInput{Invocation: newInvocation(), Phase: "turn", Purpose: "chat"}
	applyExtensionNeeds(&input, manifest.Needs, run, fixture.Turn.History, config.Workspace,
		filepath.Join(dataDir, "extensions", manifest.ID, "data"))
	callCtx, cancel := context.WithTimeout(ctx, extensionTimeout(manifest))
	defer cancel()

	if fixture.Workflow != "" {
		if !contains(client.definition.Provides, "workflow") {
			return ExtensionFixtureResult{}, errors.New("harness: fixture requires a workflow capability")
		}
		invocation, err := host.beginInvocation(callCtx, manifest.ID, nil, run, manifest)
		if err != nil {
			return ExtensionFixtureResult{}, err
		}
		defer host.endInvocation(invocation)
		params := extensionWorkflowParams(invocation.id, fixture.Workflow, fixture.Input, run,
			fixture.Turn.History, manifest.Needs, config.Workspace, nil)
		raw, err = client.rpc.Call(callCtx, "workflow.run", params)
		if err != nil {
			return ExtensionFixtureResult{}, fmt.Errorf("harness: workflow.run: %w", err)
		}
		var workflow extensionWorkflowResult
		if err := json.Unmarshal(raw, &workflow); err != nil {
			return ExtensionFixtureResult{}, errors.New("harness: workflow.run returned invalid JSON")
		}
		matched := matchExtensionFixture(fixture.Expect, map[string]any{
			"output": workflow.Output, "activity": workflow.Activity,
		})
		return ExtensionFixtureResult{Info: checked, Output: workflow.Output,
			Activity: workflow.Activity, Matched: matched}, nil
	}

	if !contains(client.definition.Provides, "context") {
		return ExtensionFixtureResult{}, errors.New("harness: fixture requires a context capability")
	}
	raw, err = client.rpc.Call(callCtx, "context.prepare", input)
	if err != nil {
		return ExtensionFixtureResult{}, fmt.Errorf("harness: context.prepare: %w", err)
	}
	var prepared extensionPrepareResult
	if err := json.Unmarshal(raw, &prepared); err != nil {
		return ExtensionFixtureResult{}, errors.New("harness: context.prepare returned invalid JSON")
	}
	blocks := make([]ExtensionBlock, 0, len(prepared.Blocks))
	seen := map[string]bool{}
	for _, block := range prepared.Blocks {
		if block.ID == "" || seen[block.ID] || block.Text == "" || len(block.Text) > maxExtensionBlockChars {
			return ExtensionFixtureResult{}, errors.New("harness: fixture returned an invalid context block")
		}
		seen[block.ID] = true
		placement := block.Placement
		if placement == "" {
			placement = "tail"
			if len(manifest.Context.Placement) != 0 {
				placement = manifest.Context.Placement[0]
			}
		}
		if !extensionPlacementAllowed(manifest.Context, placement) {
			return ExtensionFixtureResult{}, errors.New("harness: fixture returned an undeclared context placement")
		}
		priority := 50
		if block.Priority != nil {
			priority = *block.Priority
		}
		blocks = append(blocks, ExtensionBlock{
			ExtensionID: manifest.ID, Version: manifest.Version, ID: block.ID,
			Text: block.Text, Placement: placement, Label: block.Label,
			Priority: priority, SHA256: contentHash([]byte(block.Text)),
			Bytes: len(block.Text), Chars: utf8.RuneCountInString(block.Text),
		})
	}
	maxChars := manifest.Context.MaxChars
	if maxChars == 0 {
		maxChars = defaultExtensionChars
	}
	blocks = boundExtensionBlocks(blocks, maxChars)
	matched := matchExtensionFixture(fixture.Expect, map[string]any{"blocks": blocks})
	return ExtensionFixtureResult{Info: checked, Blocks: blocks, Matched: matched}, nil
}

func matchExtensionFixture(raw json.RawMessage, actual map[string]any) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var expected map[string]any
	if json.Unmarshal(raw, &expected) != nil {
		return false
	}
	for key, value := range expected {
		actualValue, ok := actual[key]
		if !ok {
			return false
		}
		expectedRaw, _ := json.Marshal(value)
		actualRaw, _ := json.Marshal(actualValue)
		if string(expectedRaw) != string(actualRaw) {
			return false
		}
	}
	return true
}

func copyExtensionFile(source, target string, mode os.FileMode) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("harness: extension file is not a regular file")
	}
	if info.Size() < 0 || info.Size() > maxExtensionTotalBytes {
		return errors.New("harness: extension file exceeds size limit")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		return err
	}
	if mode&0o111 != 0 {
		_ = os.Chmod(target, mode.Perm())
	}
	return nil
}

func importExtensionDirectory(dataDir, source string) (ExtensionInfo, error) {
	if !filepath.IsAbs(source) {
		return ExtensionInfo{}, errors.New("harness: extension import path must be absolute")
	}
	root, err := canonicalDir(source)
	if err != nil {
		return ExtensionInfo{}, err
	}
	checked, err := CheckExtension(root)
	if err != nil {
		return ExtensionInfo{}, fmt.Errorf("harness: invalid extension folder: %w", err)
	}
	targetRoot := filepath.Join(dataDir, "extensions")
	if err := os.MkdirAll(targetRoot, 0o700); err != nil {
		return ExtensionInfo{}, err
	}
	target := filepath.Join(targetRoot, checked.ID)
	if _, err := os.Lstat(target); err == nil {
		return ExtensionInfo{}, errors.New("harness: extension already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return ExtensionInfo{}, err
	}
	staging, err := os.MkdirTemp(targetRoot, ".extension-import-*")
	if err != nil {
		return ExtensionInfo{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(staging)
		}
	}()
	files := 0
	total := int64(0)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || filepath.IsAbs(relative) || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("harness: extension contains an invalid path")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return errors.New("harness: extension symlinks are not allowed")
		}
		targetPath := filepath.Join(staging, relative)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o700)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("harness: extension contains a non-regular file")
		}
		files++
		if files > maxExtensionFiles {
			return errors.New("harness: extension contains too many files")
		}
		total += info.Size()
		if total > maxExtensionTotalBytes {
			return errors.New("harness: extension exceeds size limit")
		}
		return copyExtensionFile(path, targetPath, info.Mode())
	})
	if err != nil {
		return ExtensionInfo{}, err
	}
	if err := os.Rename(staging, target); err != nil {
		return ExtensionInfo{}, err
	}
	published = true
	return CheckExtension(target)
}

func (tasks *TaskService) ImportExtension(ctx context.Context, source string) (ExtensionInfo, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return ExtensionInfo{}, errors.New("harness: task service and context are required")
	}
	if err := ctx.Err(); err != nil {
		return ExtensionInfo{}, err
	}
	info, err := importExtensionDirectory(tasks.DataDir(), source)
	if err != nil {
		return ExtensionInfo{}, err
	}
	return info, nil
}

func newExtensionHost(dataDir, workspace string, config Config, keys *KeyStore) *extensionHost {
	return &extensionHost{dataDir: dataDir, workspace: workspace, logDir: filepath.Join(dataDir, "logs"),
		config: cloneConfig(config), keys: keys, clients: map[string]*extensionClient{},
		active: map[string]*extensionInvocation{}}
}

func (host *extensionHost) configure(config Config) {
	if host == nil {
		return
	}
	host.mu.Lock()
	host.config = cloneConfig(config)
	host.workspace = config.Workspace
	host.mu.Unlock()
}

func (host *extensionHost) configuration() Config {
	host.mu.Lock()
	defer host.mu.Unlock()
	return cloneConfig(host.config)
}

func (host *extensionHost) close() error {
	if host == nil {
		return nil
	}
	host.startMu.Lock()
	defer host.startMu.Unlock()
	host.mu.Lock()
	host.closed = true
	clients := host.clients
	host.clients = map[string]*extensionClient{}
	for id, invocation := range host.active {
		invocation.cancel()
		delete(host.active, id)
	}
	host.usage = nil
	host.mu.Unlock()
	var result error
	for _, client := range clients {
		if client.rpc != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			_, _ = client.rpc.Call(ctx, "shutdown", nil)
			cancel()
			result = errors.Join(result, client.rpc.Close())
		}
	}
	return result
}

func (host *extensionHost) restart(id string) error {
	if host == nil || !validExtensionID(id) {
		return errors.New("harness: invalid extension identifier")
	}
	host.startMu.Lock()
	defer host.startMu.Unlock()
	host.mu.Lock()
	client := host.clients[id]
	delete(host.clients, id)
	host.mu.Unlock()
	if client == nil || client.rpc == nil {
		return nil
	}
	return client.rpc.Close()
}

func (host *extensionHost) definition(id string) (extensionDefinition, error) {
	return host.definitionFor(id, host.configuration())
}

func (host *extensionHost) definitionFor(id string, config Config) (extensionDefinition, error) {
	if !validExtensionID(id) {
		return extensionDefinition{}, errors.New("invalid extension id")
	}
	registry, err := scanExtensions(host.dataDir, config.Workspace, config)
	if err != nil {
		return extensionDefinition{}, err
	}
	if err := registry.Errors[id]; err != nil {
		return extensionDefinition{}, err
	}
	definition, ok := registry.Extensions[id]
	if !ok {
		return extensionDefinition{}, fmt.Errorf("extension %q is not installed", id)
	}
	return definition, nil
}

func (host *extensionHost) client(ctx context.Context, id string) (*extensionClient, error) {
	config := host.configuration()
	definition, err := host.definitionFor(id, config)
	if err != nil {
		return nil, err
	}
	return host.clientFor(ctx, definition, config)
}

func (host *extensionHost) clientFor(ctx context.Context, definition extensionDefinition, config Config) (*extensionClient, error) {
	host.startMu.Lock()
	defer host.startMu.Unlock()
	id := definition.Manifest.ID
	env, err := host.environment(id, definition.Manifest, config)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(struct {
		Definition extensionDefinition
		Workspace  string
		Settings   map[string]any
		Env        []string
	}{definition, config.Workspace, config.Extensions.Settings[id], env})
	signature := contentHash(raw)
	host.mu.Lock()
	if host.closed {
		host.mu.Unlock()
		return nil, errors.New("extension host is closed")
	}
	previous := host.clients[id]
	if previous != nil && previous.ready && !previous.rpc.Closed() && previous.signature == signature {
		host.mu.Unlock()
		return previous, nil
	}
	delete(host.clients, id)
	host.mu.Unlock()
	if previous != nil {
		_ = previous.rpc.Close()
	}
	manifest := definition.Manifest
	cwd := definition.Root
	if manifest.Run.Cwd != "" {
		cwd = filepath.Join(definition.Root, filepath.Clean(manifest.Run.Cwd))
	}
	if !pathWithin(definition.Root, cwd) {
		return nil, errors.New("extension cwd escapes its directory")
	}
	command := manifest.Run.Command
	if filepath.IsAbs(command) {
		command = filepath.Clean(command)
	} else if strings.ContainsAny(command, `/\`) {
		command = filepath.Join(definition.Root, filepath.Clean(command))
		if !pathWithin(definition.Root, command) {
			return nil, errors.New("extension command escapes its directory")
		}
	} else {
		command, err = exec.LookPath(command)
		if err != nil {
			return nil, errors.New("extension executable was not found on PATH")
		}
		command, err = filepath.Abs(command)
		if err != nil {
			return nil, err
		}
	}
	logPath := filepath.Join(host.logDir, "ext-"+safeLogID(id)+".log")
	instance := &extensionClient{definition: ExtensionDefinitionView{ID: id, Version: manifest.Version,
		Name: manifest.Name, Description: manifest.Description, Source: definition.Source,
		Required: manifest.Required || config.Extensions.Required[id]}, signature: signature}
	client, err := mcpstdio.NewBidirectionalClientWithLog(ctx, command, manifest.Run.Args, cwd, env, logPath,
		host.redact, func(requestCtx context.Context, request *jsonrpc.Request) (any, error) {
			return host.handleHostRequest(requestCtx, id, request)
		})
	if err != nil {
		return nil, err
	}
	instance.rpc = client
	if err := instance.initialize(ctx, host, manifest, config); err != nil {
		_ = client.Close()
		return nil, err
	}
	host.mu.Lock()
	host.clients[id] = instance
	host.mu.Unlock()
	return instance, nil
}

func (host *extensionHost) extensionTools(ctx context.Context, id string) ([]extensionRuntimeTool, error) {
	config := host.configuration()
	definition, err := host.definitionFor(id, config)
	if err != nil {
		return nil, err
	}
	client, err := host.clientFor(ctx, definition, config)
	if err != nil {
		return nil, err
	}
	if !contains(client.definition.Provides, "tools") {
		return nil, errors.New("harness: extension process did not register tools")
	}
	client.toolsMu.Lock()
	defer client.toolsMu.Unlock()
	if client.toolsLoaded {
		return append([]extensionRuntimeTool(nil), client.tools...), nil
	}
	callCtx, cancel := context.WithTimeout(ctx, extensionTimeout(definition.Manifest))
	defer cancel()
	raw, err := client.rpc.Call(callCtx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result extensionToolsListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("extension tools/list returned invalid JSON")
	}
	if len(result.Tools) == 0 {
		return nil, errors.New("extension tools/list returned no tools")
	}
	seen := map[string]bool{}
	tools := make([]extensionRuntimeTool, 0, len(result.Tools))
	for _, tool := range result.Tools {
		if !validCapabilityID(tool.Name) || seen[tool.Name] {
			return nil, errors.New("extension tools/list returned invalid tool names")
		}
		seen[tool.Name] = true
		if len(tool.Description) > 4096 || len(tool.InputSchema) == 0 || len(tool.InputSchema) > 64<<10 {
			return nil, errors.New("extension tools/list returned an oversized tool")
		}
		input, err := resolveToolSchema(tool.InputSchema)
		if err != nil {
			return nil, errors.New("extension tools/list returned an invalid schema")
		}
		description := host.redact(definition.Manifest.Name + "/" + tool.Name + ": " + tool.Description)
		if host.redact(string(tool.InputSchema)) != string(tool.InputSchema) {
			return nil, errors.New("extension tools/list returned a secret-bearing definition")
		}
		alias := extensionToolPrefix + contentHash([]byte(id + "\x00" + tool.Name))[:24]
		effect := EffectModify
		if tool.ReadOnly {
			effect = EffectRead
		}
		tools = append(tools, extensionRuntimeTool{
			ExtensionID: id, Name: tool.Name,
			Definition: ToolDefinition{Name: alias, Description: description,
				InputSchema: append(json.RawMessage(nil), tool.InputSchema...), Effect: effect},
			Input: input,
		})
	}
	client.tools = tools
	client.toolsLoaded = true
	return append([]extensionRuntimeTool(nil), tools...), nil
}

func (host *extensionHost) extensionWorkflowTools(ctx context.Context, id string) ([]extensionRuntimeTool, error) {
	config := host.configuration()
	definition, err := host.definitionFor(id, config)
	if err != nil {
		return nil, err
	}
	client, err := host.clientFor(ctx, definition, config)
	if err != nil {
		return nil, err
	}
	if !contains(client.definition.Provides, "workflow") {
		return nil, errors.New("harness: extension process did not register workflows")
	}
	registered := make(map[string]bool, len(client.registration.Workflows))
	for _, name := range client.registration.Workflows {
		registered[name] = true
	}
	tools := make([]extensionRuntimeTool, 0, len(definition.Manifest.Workflows))
	for _, workflow := range definition.Manifest.Workflows {
		if workflow.AgentCallable != nil && !*workflow.AgentCallable {
			continue
		}
		if !registered[workflow.Name] {
			return nil, errors.New("harness: extension workflow registration does not match manifest")
		}
		schema := workflow.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","additionalProperties":false}`)
		}
		input, err := resolveToolSchema(schema)
		if err != nil {
			return nil, errors.New("harness: extension workflow schema is invalid")
		}
		alias := extensionToolPrefix + contentHash([]byte(id + "\x00workflow\x00" + workflow.Name))[:24]
		description := host.redact(definition.Manifest.Name + "/" + workflow.Name + ": " + workflow.Description)
		tools = append(tools, extensionRuntimeTool{
			ExtensionID: id, Name: workflow.Name, Workflow: workflow.Name,
			Definition: ToolDefinition{Name: alias, Description: description,
				InputSchema: append(json.RawMessage(nil), schema...), Effect: EffectModify},
			Input: input,
		})
	}
	return tools, nil
}

func (client *extensionClient) initialize(parent context.Context, host *extensionHost, manifest ExtensionManifest, config Config) error {
	// The handshake also covers interpreter startup, which can be slow on a
	// cold or scanned first launch; later calls keep the manifest timeout.
	timeout := max(extensionTimeout(manifest), extensionStartupTimeout)
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	dataDir := filepath.Join(host.dataDir, "extensions", manifest.ID, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	raw, err := client.rpc.Call(ctx, "initialize", map[string]any{
		"protocol": extensionProtocol, "host_version": buildinfo.Version,
		"workspace": config.Workspace, "data_dir": dataDir,
		"settings": config.Extensions.Settings[manifest.ID],
	})
	if err != nil {
		return err
	}
	var result extensionInitializeResult
	if json.Unmarshal(raw, &result) != nil || result.ID != manifest.ID || result.Version != manifest.Version {
		return errors.New("extension initialize returned an invalid identity")
	}
	seen := map[string]bool{}
	for _, provide := range result.Provides {
		if seen[provide] || provide != "context" && provide != "workflow" && provide != "tools" {
			return errors.New("extension initialize returned invalid capabilities")
		}
		seen[provide] = true
	}
	client.definition.Provides = result.Provides
	client.registration = result
	client.ready = true
	return nil
}

func extensionTimeout(manifest ExtensionManifest) time.Duration {
	timeout := manifest.TimeoutMS
	if timeout == 0 {
		timeout = defaultExtensionTimeout
	}
	return time.Duration(timeout) * time.Millisecond
}

var (
	errExtensionBudget         = &jsonrpc.Error{Code: -32001, Message: "extension budget exhausted"}
	errExtensionCancel         = &jsonrpc.Error{Code: -32002, Message: "extension invocation cancelled"}
	errExtensionDenied         = &jsonrpc.Error{Code: -32003, Message: "extension host call denied"}
	errExtensionUnavailable    = &jsonrpc.Error{Code: -32004, Message: "extension host capability unavailable"}
	errExtensionOutcomeUnknown = &jsonrpc.Error{Code: -32006, Message: "extension host call outcome unknown"}
)

type extensionGenerateMessage struct {
	Role       string                  `json:"role"`
	Content    string                  `json:"content,omitempty"`
	Name       string                  `json:"name,omitempty"`
	ToolCallID string                  `json:"tool_call_id,omitempty"`
	ToolCalls  []extensionToolCallWire `json:"tool_calls,omitempty"`
}

type extensionToolCallWire struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type extensionGenerateRequest struct {
	Invocation string                     `json:"invocation"`
	Model      string                     `json:"model,omitempty"`
	Messages   []extensionGenerateMessage `json:"messages"`
	Tools      []extensionToolDefinition  `json:"tools,omitempty"`
	Schema     json.RawMessage            `json:"schema,omitempty"`
	MaxTokens  int                        `json:"max_tokens,omitempty"`
}

type extensionEmbedRequest struct {
	Invocation string   `json:"invocation"`
	Model      string   `json:"model,omitempty"`
	Inputs     []string `json:"inputs"`
}

func (host *extensionHost) beginInvocation(parent context.Context, id string, service *Service, run Run, manifest ExtensionManifest) (*extensionInvocation, error) {
	if host == nil || parent == nil || !validExtensionID(id) {
		return nil, errExtensionUnavailable
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	invocation := &extensionInvocation{id: newInvocation(), extensionID: id, service: service,
		run: run, manifest: manifest, ctx: ctx, cancel: cancel, usage: &extensionRunUsage{}}
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.closed {
		cancel()
		return nil, errExtensionUnavailable
	}
	if host.active == nil {
		host.active = map[string]*extensionInvocation{}
	}
	// Fixture replays have no service and keep an unshared budget.
	if service != nil && run.ID != "" {
		key := extensionUsageKey(run.ID, id)
		if host.usage == nil {
			host.usage = map[string]*extensionRunUsage{}
		}
		if usage := host.usage[key]; usage != nil {
			invocation.usage = usage
		} else {
			host.usage[key] = invocation.usage
		}
	}
	host.active[invocation.id] = invocation
	return invocation, nil
}

func extensionUsageKey(runID, extensionID string) string {
	return runID + "\x00" + extensionID
}

// extensionToolPrefix marks the aliases of extension tools and workflows.
// Only these names can start another extension process, so they are the only
// names the recursion guards need to remove.
const extensionToolPrefix = "ext_"

func extensionToolAlias(name string) bool {
	return strings.HasPrefix(name, extensionToolPrefix)
}

// releaseRun drops the budget counters of a settled Run. A Run waiting for
// confirmation is not settled and keeps its counters for the continuation.
func (host *extensionHost) releaseRun(runID string) {
	if host == nil || runID == "" {
		return
	}
	prefix := runID + "\x00"
	host.mu.Lock()
	defer host.mu.Unlock()
	for key := range host.usage {
		if strings.HasPrefix(key, prefix) {
			delete(host.usage, key)
		}
	}
}

// modelBudget returns the per-Run model call and output limits of one
// extension: the Host turn cap narrowed by the manifest budget.
func (host *extensionHost) modelBudget(manifest ExtensionManifest) (calls, output int) {
	host.mu.Lock()
	calls = host.config.Extensions.MaxModelCallsPerTurn
	host.mu.Unlock()
	if calls <= 0 || calls > maxExtensionModelCalls {
		calls = maxExtensionModelCalls
	}
	if manifest.Budget.ModelCalls > 0 {
		calls = min(calls, manifest.Budget.ModelCalls)
	}
	return calls, manifest.Budget.OutputTokens
}

func (host *extensionHost) endInvocation(invocation *extensionInvocation) {
	if host == nil || invocation == nil {
		return
	}
	invocation.cancel()
	host.mu.Lock()
	delete(host.active, invocation.id)
	host.mu.Unlock()
}

func (host *extensionHost) invocation(id, extensionID string) (*extensionInvocation, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	invocation := host.active[id]
	if invocation == nil || invocation.extensionID != extensionID {
		return nil, errExtensionInvalidInvocation
	}
	if err := invocation.ctx.Err(); err != nil {
		return nil, errExtensionCancel
	}
	return invocation, nil
}

func (host *extensionHost) handleHostRequest(requestCtx context.Context, extensionID string, request *jsonrpc.Request) (any, error) {
	if request == nil || request.Method == "" {
		return nil, &jsonrpc.Error{Code: -32600, Message: "invalid request"}
	}
	var params map[string]json.RawMessage
	if len(request.Params) != 0 && (json.Unmarshal(request.Params, &params) != nil || params == nil) {
		return nil, &jsonrpc.Error{Code: -32600, Message: "invalid params"}
	}
	var invocationID string
	if raw := params["invocation"]; len(raw) == 0 || json.Unmarshal(raw, &invocationID) != nil || invocationID == "" {
		return nil, errExtensionInvalidInvocation
	}
	invocation, err := host.invocation(invocationID, extensionID)
	if err != nil {
		return nil, err
	}
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	select {
	case <-requestCtx.Done():
		return nil, requestCtx.Err()
	case <-invocation.ctx.Done():
		return nil, errExtensionCancel
	default:
	}
	switch request.Method {
	case "activity.report":
		return host.handleActivityReport(invocation, params)
	case "model.generate":
		return host.handleModelGenerate(invocation, params)
	case "model.embed":
		return host.handleModelEmbed(invocation, params)
	case "task.delegate":
		return host.handleTaskDelegate(requestCtx, invocation, params)
	case "tool.call":
		return host.handleToolCall(requestCtx, invocation, params)
	default:
		return nil, &jsonrpc.Error{Code: -32601, Message: "method not found"}
	}
}

func (host *extensionHost) handleTaskDelegate(requestCtx context.Context,
	invocation *extensionInvocation, params map[string]json.RawMessage) (map[string]any, error) {
	if invocation != nil && invocation.service == nil && host.fixture != nil {
		return host.fixtureDelegate()
	}
	if invocation == nil || invocation.service == nil || invocation.run.Status != "running" {
		return nil, errExtensionUnavailable
	}
	raw := params["plan"]
	if len(raw) == 0 || len(raw) > 256<<10 {
		return nil, &jsonrpc.Error{Code: -32600, Message: "invalid task.delegate plan"}
	}
	plan, err := DecodeCollaborationPlan(strings.NewReader(string(raw)))
	if err != nil {
		return nil, &jsonrpc.Error{Code: -32600, Message: safeError(err)}
	}
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	if err := requestCtx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(invocation.ctx)
	defer cancel()
	stop := context.AfterFunc(requestCtx, cancel)
	defer stop()
	invocation.mu.Lock()
	defer invocation.mu.Unlock()
	maxCalls, maxOutput := host.modelBudget(invocation.manifest)
	result, err := runActiveDelegation(ctx, invocation, plan, maxCalls, maxOutput)
	if err != nil {
		if errors.Is(err, errExtensionOutcomeUnknown) {
			return nil, errExtensionOutcomeUnknown
		}
		if errors.Is(err, ErrParentBudget) || errors.Is(err, ErrChildBudget) ||
			errors.Is(err, ErrStepLimit) || errors.Is(err, ErrTimeLimit) {
			return nil, errExtensionBudget
		}
		if errors.Is(err, errExtensionBudget) || errors.Is(err, errExtensionDenied) ||
			errors.Is(err, errExtensionCancel) || errors.Is(err, errExtensionOutcomeUnknown) {
			return nil, err
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, errExtensionCancel
		}
		return nil, &jsonrpc.Error{Code: -32603, Message: safeError(err)}
	}
	return result, nil
}

func (host *extensionHost) handleActivityReport(invocation *extensionInvocation, params map[string]json.RawMessage) (map[string]any, error) {
	if invocation != nil && invocation.service == nil && host.fixture != nil {
		return map[string]any{"ok": true}, nil
	}
	if invocation.service == nil {
		return nil, errExtensionUnavailable
	}
	var activity map[string]any
	if raw := params["activity"]; len(raw) != 0 {
		_ = json.Unmarshal(raw, &activity)
	}
	if activity == nil {
		activity = map[string]any{}
	}
	if raw := params["summary"]; len(raw) != 0 {
		var summary string
		if json.Unmarshal(raw, &summary) == nil {
			activity["summary"] = summary
		}
	}
	if raw := params["items"]; len(raw) != 0 {
		var items []any
		if json.Unmarshal(raw, &items) == nil {
			activity["items"] = items
		}
	}
	activity = redactExtensionValue(host, activity).(map[string]any)
	if err := validateExtensionActivity(activity); err != nil {
		return nil, &jsonrpc.Error{Code: -32600, Message: err.Error()}
	}
	summary, _ := activity["summary"].(string)
	if summary == "" {
		summary = "extension activity"
	}
	invocation.service.event(Event{Kind: "extension_activity", Message: summary,
		Activity: activity, RunID: invocation.run.ID, ConversationID: invocation.run.ConversationID})
	return map[string]any{"ok": true}, nil
}

func (host *extensionHost) handleModelGenerate(invocation *extensionInvocation, params map[string]json.RawMessage) (map[string]any, error) {
	if invocation != nil && invocation.service == nil && host.fixture != nil {
		return host.fixtureGenerate()
	}
	if invocation.service == nil {
		return nil, errExtensionUnavailable
	}
	var request extensionGenerateRequest
	raw, _ := json.Marshal(params)
	if json.Unmarshal(raw, &request) != nil || len(request.Messages) == 0 || len(request.Messages) > 128 {
		return nil, &jsonrpc.Error{Code: -32600, Message: "invalid model.generate request"}
	}
	model := request.Model
	if model == "" || model == "current" {
		model = ""
	}
	service := *invocation.service
	binding, err := service.binding()
	if err != nil {
		return nil, errExtensionUnavailable
	}
	if model != "" && model != binding.ID {
		return nil, errExtensionUnavailable
	}
	limits := service.limits()
	maxTokens := request.MaxTokens
	if maxTokens == 0 {
		maxTokens = limits.MaxOutputTokens
	}
	if maxTokens < 1 || maxTokens > limits.MaxOutputTokens {
		return nil, &jsonrpc.Error{Code: -32600, Message: "invalid max_tokens"}
	}
	messages := make([]ModelMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == "" || len(message.Content) > 1<<20 {
			return nil, &jsonrpc.Error{Code: -32600, Message: "invalid model message"}
		}
		calls := make([]ToolCall, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			calls = append(calls, ToolCall{ID: call.ID, Name: call.Name, Arguments: append(json.RawMessage(nil), call.Arguments...)})
		}
		messages = append(messages, ModelMessage{Role: message.Role, Content: message.Content,
			Name: message.Name, ToolCallID: message.ToolCallID, ToolCalls: calls})
	}
	if len(request.Tools) != 0 {
		return nil, errExtensionUnavailable
	}
	modelRequest := ModelRequest{ModelID: binding.ID, ModelRevision: binding.Revision,
		Purpose:  "ext:" + invocation.extensionID + "/model.generate",
		Messages: messages, MaxOutputTokens: maxTokens}
	if err := invocation.ctx.Err(); err != nil {
		return nil, errExtensionCancel
	}
	// One output unit is booked with the call so an exhausted output budget
	// stops further calls; the observed output replaces it afterwards.
	maxCalls, maxOutput := host.modelBudget(invocation.manifest)
	if !invocation.usage.reserve(1, 1, maxCalls, maxOutput) {
		return nil, errExtensionBudget
	}
	_, response, callErr := service.generate(invocation.ctx, invocation.run, modelRequest)
	if callErr != nil {
		if errors.Is(callErr, ErrUnknownResult) {
			return nil, errExtensionOutcomeUnknown
		}
		if errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) {
			return nil, errExtensionCancel
		}
		return nil, &jsonrpc.Error{Code: -32603, Message: safeError(callErr)}
	}
	output := response.Content
	spent := len(output)
	if response.Usage != nil && response.Usage.OutputTokens > 0 {
		spent = int(response.Usage.OutputTokens)
	}
	if !invocation.usage.settle(0, spent-1, maxOutput) {
		return nil, errExtensionBudget
	}
	result := map[string]any{"content": host.redact(output)}
	if len(response.ToolCalls) != 0 {
		calls := make([]map[string]any, 0, len(response.ToolCalls))
		for _, call := range response.ToolCalls {
			calls = append(calls, map[string]any{"id": call.ID, "name": call.Name, "arguments": json.RawMessage(call.Arguments)})
		}
		result["tool_calls"] = calls
	}
	if len(request.Schema) != 0 {
		var value any
		if json.Unmarshal([]byte(output), &value) == nil {
			result["json"] = redactExtensionValue(host, value)
		}
	}
	if response.Usage != nil {
		result["usage"] = response.Usage
	}
	return result, nil
}

func (host *extensionHost) handleModelEmbed(invocation *extensionInvocation, params map[string]json.RawMessage) (map[string]any, error) {
	if invocation != nil && invocation.service == nil && host.fixture != nil {
		var request extensionEmbedRequest
		raw, _ := json.Marshal(params)
		if json.Unmarshal(raw, &request) != nil || len(request.Inputs) == 0 {
			return nil, &jsonrpc.Error{Code: -32600, Message: "invalid model.embed request"}
		}
		return host.fixtureEmbed(request.Inputs)
	}
	if invocation.service == nil {
		return nil, errExtensionUnavailable
	}
	var request extensionEmbedRequest
	raw, _ := json.Marshal(params)
	if json.Unmarshal(raw, &request) != nil || len(request.Inputs) == 0 ||
		len(request.Inputs) > 128 {
		return nil, &jsonrpc.Error{Code: -32600, Message: "invalid model.embed request"}
	}
	for _, input := range request.Inputs {
		if input == "" || len(input) > 1<<20 {
			return nil, &jsonrpc.Error{Code: -32600, Message: "invalid model.embed input"}
		}
	}
	model := request.Model
	if model == "" || model == "current" {
		model = ""
	}
	service := *invocation.service
	binding, err := service.binding()
	if err != nil || binding.Config.EffectiveCapabilities().Embeddings != "supported" {
		return nil, errExtensionUnavailable
	}
	if model != "" && model != binding.ID && model != binding.Config.Model {
		return nil, errExtensionUnavailable
	}
	if err := invocation.ctx.Err(); err != nil {
		return nil, errExtensionCancel
	}
	maxCalls, _ := host.modelBudget(invocation.manifest)
	if !invocation.usage.reserve(1, 0, maxCalls, 0) {
		return nil, errExtensionBudget
	}
	response, callErr := service.embed(invocation.ctx, invocation.run,
		"ext:"+invocation.extensionID+"/model.embed", request.Inputs)
	if callErr != nil {
		if errors.Is(callErr, ErrUnknownResult) {
			return nil, errExtensionOutcomeUnknown
		}
		if errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) {
			return nil, errExtensionCancel
		}
		if rpcErr, ok := callErr.(*jsonrpc.Error); ok {
			return nil, rpcErr
		}
		return nil, &jsonrpc.Error{Code: -32603, Message: safeError(callErr)}
	}
	result := map[string]any{"vectors": response.Vectors}
	if response.Usage != nil {
		result["usage"] = response.Usage
	}
	return result, nil
}

func (host *extensionHost) handleToolCall(requestCtx context.Context, invocation *extensionInvocation,
	params map[string]json.RawMessage) (map[string]any, error) {
	if invocation != nil && invocation.service == nil && host.fixture != nil {
		var request extensionHostToolCall
		raw, _ := json.Marshal(params)
		if json.Unmarshal(raw, &request) != nil || !validCapabilityID(request.Name) {
			return nil, &jsonrpc.Error{Code: -32600, Message: "invalid tool.call request"}
		}
		return host.fixtureTool(request.Name)
	}
	if invocation == nil || invocation.service == nil {
		return nil, errExtensionUnavailable
	}
	var request extensionHostToolCall
	raw, _ := json.Marshal(params)
	if json.Unmarshal(raw, &request) != nil || !validCapabilityID(request.Name) {
		return nil, &jsonrpc.Error{Code: -32600, Message: "invalid tool.call request"}
	}
	arguments := request.Arguments
	if len(arguments) == 0 {
		arguments = request.Args
	}
	if len(arguments) == 0 {
		arguments = json.RawMessage(`{}`)
	}
	var object map[string]any
	if json.Unmarshal(arguments, &object) != nil || object == nil {
		return nil, &jsonrpc.Error{Code: -32600, Message: "tool.call arguments must be a JSON object"}
	}
	arguments, err := json.Marshal(object)
	if err != nil || len(arguments) > 64<<10 ||
		host.redact(string(arguments)) != string(arguments) {
		return nil, &jsonrpc.Error{Code: -32600, Message: "invalid or secret-bearing tool.call arguments"}
	}
	// An extension cannot call another extension through the Host. This keeps
	// the capability graph finite and prevents recursive process invocation.
	if extensionToolAlias(request.Name) {
		return nil, &jsonrpc.Error{Code: -32004, Message: "extension tool recursion is not allowed"}
	}
	ctx, cancel := context.WithCancel(context.WithValue(invocation.ctx, extensionCallerKey{}, invocation.extensionID))
	defer cancel()
	if requestCtx != nil {
		stopRequest := context.AfterFunc(requestCtx, cancel)
		defer stopRequest()
	}
	if err := ctx.Err(); err != nil {
		return nil, errExtensionCancel
	}
	call := ToolCall{ID: "host-" + newID(), Name: request.Name, Arguments: arguments}
	attempt, err := invocation.service.Store.startTool(ctx, invocation.run, call)
	if err != nil {
		return nil, &jsonrpc.Error{Code: -32004, Message: safeError(err)}
	}
	executeErr := invocation.service.executeToolWithContext(ctx, ctx, invocation.run, attempt,
		func() error { return nil },
		func() error { return ctx.Err() },
		func() context.Context { return ctx }, true, false)
	saved, loadErr := invocation.service.Store.GetAttempt(context.Background(), attempt.ID)
	if loadErr != nil {
		return nil, loadErr
	}
	if saved.ResponseJSON == "" {
		if executeErr != nil {
			return nil, &jsonrpc.Error{Code: -32006, Message: safeError(executeErr)}
		}
		return nil, &jsonrpc.Error{Code: -32006, Message: "tool call did not produce a recorded result"}
	}
	var result ToolResult
	if err := json.Unmarshal([]byte(saved.ResponseJSON), &result); err != nil {
		return nil, errors.New("harness: tool call result is not valid JSON")
	}
	if executeErr != nil {
		if errors.Is(executeErr, ErrUnknownResult) {
			return nil, &jsonrpc.Error{Code: -32006, Message: "tool outcome is unknown; do not replay automatically"}
		}
		if errors.Is(executeErr, errAwaitingApproval) {
			return nil, &jsonrpc.Error{Code: -32003, Message: "tool approval is required"}
		}
		if result.Content == "" {
			return nil, &jsonrpc.Error{Code: -32004, Message: safeError(executeErr)}
		}
	}
	reply := map[string]any{"content": host.redact(result.Content), "is_error": result.IsError}
	if len(result.Receipts) != 0 {
		reply["receipts"] = redactExtensionValue(host, result.Receipts)
	}
	if len(result.Memory) != 0 {
		reply["memory"] = redactExtensionValue(host, result.Memory)
	}
	return reply, nil
}

func (host *extensionHost) callTool(ctx context.Context, tool extensionRuntimeTool, service *Service, run Run, arguments json.RawMessage) (ToolResult, error) {
	if host == nil || service == nil {
		return ToolResult{}, errExtensionUnavailable
	}
	config := host.configuration()
	definition, err := host.definitionFor(tool.ExtensionID, config)
	if err != nil {
		return ToolResult{}, err
	}
	client, err := host.clientFor(ctx, definition, config)
	if err != nil {
		return ToolResult{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, extensionTimeout(definition.Manifest))
	defer cancel()
	invocation, err := host.beginInvocation(callCtx, tool.ExtensionID, service, run, definition.Manifest)
	if err != nil {
		return ToolResult{}, err
	}
	defer host.endInvocation(invocation)
	var object map[string]any
	if json.Unmarshal(arguments, &object) != nil || object == nil || tool.Input == nil || tool.Input.Validate(object) != nil {
		return ToolResult{}, errors.New("harness: extension tool arguments do not satisfy the input schema")
	}
	raw, err := client.rpc.Call(callCtx, "tool.call", map[string]any{
		"invocation": invocation.id, "name": tool.Name, "arguments": object,
	})
	if err != nil {
		return ToolResult{}, &CallError{Message: "extension tool call interrupted; outcome unknown"}
	}
	var result extensionToolCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return ToolResult{}, &CallError{Message: "extension tool returned invalid JSON; outcome unknown"}
	}
	if len(result.Content) > maxExtensionBlockChars {
		return ToolResult{}, &CallError{Message: "extension tool output exceeded the limit; outcome unknown"}
	}
	if len(result.Activity) != 0 {
		result.Activity = redactExtensionValue(host, result.Activity).(map[string]any)
		if err := validateExtensionActivity(result.Activity); err != nil {
			return ToolResult{}, err
		}
		summary, _ := result.Activity["summary"].(string)
		if summary == "" {
			summary = "extension activity"
		}
		service.event(Event{Kind: "extension_activity", Message: summary,
			Activity: result.Activity, RunID: run.ID, ConversationID: run.ConversationID})
	}
	return ToolResult{Content: host.redact(result.Content), IsError: result.IsError,
		Extensions: []ExtensionUse{{Source: "extension", Name: tool.ExtensionID,
			Version: definition.Manifest.Version, Phase: "tool", BlockID: tool.Name,
			SHA256: contentHash(tool.Definition.InputSchema), Bytes: len(tool.Definition.InputSchema)}},
	}, nil
}

func redactExtensionValue(host *extensionHost, value any) any {
	switch item := value.(type) {
	case string:
		return host.redact(item)
	case []any:
		result := make([]any, len(item))
		for index := range item {
			result[index] = redactExtensionValue(host, item[index])
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(item))
		for key, value := range item {
			result[host.redact(key)] = redactExtensionValue(host, value)
		}
		return result
	default:
		return value
	}
}

func (host *extensionHost) prepare(ctx context.Context, id string, run Run, purpose, phase string, step *int, history []ModelMessage, services ...*Service) ([]ExtensionBlock, error) {
	config := host.configuration()
	definition, err := host.definitionFor(id, config)
	if err != nil {
		return nil, err
	}
	manifest := definition.Manifest
	if !extensionAllows(manifest.Context, phase, purpose) {
		return nil, nil
	}
	client, err := host.clientFor(ctx, definition, config)
	if err != nil {
		return nil, err
	}
	if !contains(client.definition.Provides, "context") {
		return nil, nil
	}
	var service *Service
	if len(services) != 0 {
		service = services[0]
	}
	callCtx, cancel := context.WithTimeout(ctx, extensionTimeout(manifest))
	defer cancel()
	invocationState, err := host.beginInvocation(callCtx, id, service, run, manifest)
	if err != nil {
		return nil, err
	}
	defer host.endInvocation(invocationState)
	invocation := invocationState.id
	input := extensionInput{Invocation: invocation, Phase: phase, Purpose: purpose, Step: step,
		Settings: config.Extensions.Settings[id],
		DataDir:  filepath.Join(host.dataDir, "extensions", manifest.ID, "data")}
	applyExtensionNeeds(&input, manifest.Needs, run, history, config.Workspace, input.DataDir)
	raw, err := client.rpc.Call(callCtx, "context.prepare", input)
	if err != nil {
		abandonExtensionCall(client, invocation, err)
		return nil, err
	}
	var result extensionPrepareResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("extension context.prepare returned invalid JSON")
	}
	maxChars := manifest.Context.MaxChars
	if maxChars == 0 {
		maxChars = defaultExtensionChars
	}
	blocks := make([]ExtensionBlock, 0, len(result.Blocks))
	if len(result.Blocks) > maxExtensionBlocks {
		return nil, errors.New("extension returned too many blocks")
	}
	seen := map[string]bool{}
	for _, block := range result.Blocks {
		if block.ID == "" || len(block.ID) > 128 || seen[block.ID] ||
			len(block.Label) > 256 || block.Text == "" || !utf8.ValidString(block.Text) ||
			len(block.Text) > maxExtensionBlockChars {
			return nil, errors.New("extension returned an invalid context block")
		}
		seen[block.ID] = true
		placement := block.Placement
		if placement == "" {
			placement = "tail"
			if len(manifest.Context.Placement) != 0 {
				placement = manifest.Context.Placement[0]
			}
		}
		if !extensionPlacementAllowed(manifest.Context, placement) {
			return nil, errors.New("extension returned an undeclared context placement")
		}
		priority := 50
		if block.Priority != nil {
			priority = *block.Priority
		}
		text := host.redact(block.Text)
		blocks = append(blocks, ExtensionBlock{ExtensionID: id, Version: manifest.Version, Phase: phase,
			ID: block.ID, Text: text, Placement: placement, Label: host.redact(block.Label),
			Priority: priority, SHA256: contentHash([]byte(text)), Bytes: len(text), Chars: utf8.RuneCountInString(text)})
	}
	return boundExtensionBlocks(blocks, maxChars), nil
}

func (host *extensionHost) finish(ctx context.Context, id string, run Run, status string, history []ModelMessage, services ...*Service) (map[string]any, error) {
	if host == nil || ctx == nil {
		return nil, errors.New("harness: extension finish context is required")
	}
	config := host.configuration()
	definition, err := host.definitionFor(id, config)
	if err != nil {
		return nil, err
	}
	if !contains(definition.Manifest.Events, "turn.finished") {
		return nil, nil
	}
	client, err := host.clientFor(ctx, definition, config)
	if err != nil {
		return nil, err
	}
	var service *Service
	if len(services) != 0 {
		service = services[0]
	}
	callCtx, cancel := context.WithTimeout(ctx, extensionTimeout(definition.Manifest))
	defer cancel()
	invocationState, err := host.beginInvocation(callCtx, id, service, run, definition.Manifest)
	if err != nil {
		return nil, err
	}
	defer host.endInvocation(invocationState)
	invocation := invocationState.id
	dataDir := filepath.Join(host.dataDir, "extensions", definition.Manifest.ID, "data")
	input := extensionInput{
		Invocation: invocation,
		Phase:      "turn",
		Purpose:    "chat",
		Settings:   config.Extensions.Settings[id],
		DataDir:    dataDir,
		RunID:      run.ID, ConversationID: run.ConversationID,
	}
	applyExtensionNeeds(&input, definition.Manifest.Needs, run, history, config.Workspace, dataDir)
	// The settled outcome is the point of this event, so it does not depend
	// on needs; needs only select the surrounding context.
	input.Status = status
	if input.Status == "" {
		input.Status = run.Status
	}
	input.Output = truncateUTF8Bytes(run.Output, maxExtensionFinishedOutput)
	raw, err := client.rpc.Call(callCtx, "turn.finished", input)
	if err != nil {
		return nil, err
	}
	var result extensionActivityResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("extension turn.finished returned invalid JSON")
	}
	if len(result.Activity) == 0 {
		return nil, nil
	}
	if err := validateExtensionActivity(result.Activity); err != nil {
		return nil, err
	}
	return result.Activity, nil
}

type operationCheckInput struct {
	Invocation string         `json:"invocation"`
	Kind       string         `json:"kind"`
	Target     string         `json:"target"`
	Preview    string         `json:"preview"`
	Parameters any            `json:"parameters,omitempty"`
	Items      []ApprovalItem `json:"items,omitempty"`
}

type operationCheckDecision struct {
	Decision string
	Reason   string
}

// extensionCallerKey marks a host tool.call issued by an extension. That
// extension is blocked on the call, so its own operation.check is skipped.
type extensionCallerKey struct{}

func extensionCaller(ctx context.Context) string {
	caller, _ := ctx.Value(extensionCallerKey{}).(string)
	return caller
}

// abandonExtensionCall stops a failed host-to-extension call. A JSON-RPC
// error reply proves the process is responsive, so only transport failures and
// timeouts restart it.
func abandonExtensionCall(client *extensionClient, invocation string, err error) {
	var reply *jsonrpc.Error
	if errors.As(err, &reply) {
		return
	}
	cancelCtx, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	_ = client.rpc.Notify(cancelCtx, "$/cancel", map[string]string{"invocation": invocation})
	stop()
	_ = client.rpc.Close()
}

func (host *extensionHost) checkOperation(ctx context.Context, id string, run Run, call ToolCall,
	prepared PreparedTool, services ...*Service) (operationCheckDecision, error) {
	if host == nil || ctx == nil {
		return operationCheckDecision{}, errors.New("harness: operation check context is required")
	}
	config := host.configuration()
	definition, err := host.definitionFor(id, config)
	if err != nil {
		return operationCheckDecision{}, err
	}
	if !contains(definition.Manifest.Events, "operation.check") {
		return operationCheckDecision{Decision: "allow"}, nil
	}
	client, err := host.clientFor(ctx, definition, config)
	if err != nil {
		return operationCheckDecision{}, err
	}
	if !contains(client.registration.Events, "operation.check") {
		return operationCheckDecision{}, errors.New("extension process did not register operation.check")
	}
	var service *Service
	if len(services) != 0 {
		service = services[0]
	}
	callCtx, cancel := context.WithTimeout(ctx, extensionTimeout(definition.Manifest))
	defer cancel()
	invocationState, err := host.beginInvocation(callCtx, id, service, run, definition.Manifest)
	if err != nil {
		return operationCheckDecision{}, err
	}
	defer host.endInvocation(invocationState)

	parameters := any(map[string]any{})
	if len(call.Arguments) != 0 {
		if err := json.Unmarshal(call.Arguments, &parameters); err != nil {
			return operationCheckDecision{}, errors.New("harness: operation check arguments are not valid JSON")
		}
	}
	parameters, err = redactExtensionJSONValue(host, parameters, 64<<10)
	if err != nil {
		return operationCheckDecision{}, err
	}
	items, err := redactExtensionJSONValue(host, prepared.Items, 64<<10)
	if err != nil {
		return operationCheckDecision{}, err
	}
	var safeItems []ApprovalItem
	if len(prepared.Items) != 0 {
		raw, marshalErr := json.Marshal(items)
		if marshalErr != nil || json.Unmarshal(raw, &safeItems) != nil {
			return operationCheckDecision{}, errors.New("harness: operation check items are not valid JSON")
		}
	}
	input := operationCheckInput{
		Invocation: invocationState.id,
		Kind:       "tool_call",
		Target:     host.redact(call.Name),
		Preview:    host.redact(prepared.Preview),
		Parameters: parameters,
		Items:      safeItems,
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 128<<10 {
		return operationCheckDecision{}, errors.New("harness: operation check input exceeds the limit")
	}
	var wire map[string]any
	if json.Unmarshal(raw, &wire) != nil {
		return operationCheckDecision{}, errors.New("harness: operation check input is invalid JSON")
	}
	raw, err = json.Marshal(wire)
	if err != nil {
		return operationCheckDecision{}, err
	}
	response, err := client.rpc.Call(callCtx, "operation.check", json.RawMessage(raw))
	if err != nil {
		abandonExtensionCall(client, invocationState.id, err)
		return operationCheckDecision{}, err
	}
	var result extensionOperationCheckResult
	if err := json.Unmarshal(response, &result); err != nil {
		return operationCheckDecision{}, errors.New("extension operation.check returned invalid JSON")
	}
	if result.Decision != "allow" && result.Decision != "ask" && result.Decision != "deny" {
		return operationCheckDecision{}, errors.New("extension operation.check returned an invalid decision")
	}
	if len(result.Reason) > 4096 || !utf8.ValidString(result.Reason) {
		return operationCheckDecision{}, errors.New("extension operation.check returned an invalid reason")
	}
	return operationCheckDecision{Decision: result.Decision, Reason: host.redact(result.Reason)}, nil
}

func redactExtensionJSONValue(host *extensionHost, value any, limit int) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > limit {
		return nil, errors.New("harness: extension operation data exceeds the limit")
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, errors.New("harness: extension operation data is not valid JSON")
	}
	// Redacting decoded strings finds secrets that JSON escaping would hide
	// and cannot break the document structure.
	return redactExtensionValue(host, decoded), nil
}

func (host *extensionHost) workflow(ctx context.Context, id, name string, run Run, input any, history []ModelMessage, services ...*Service) (string, map[string]any, error) {
	if host == nil || ctx == nil {
		return "", nil, errors.New("harness: extension workflow context is required")
	}
	config := host.configuration()
	definition, err := host.definitionFor(id, config)
	if err != nil {
		return "", nil, err
	}
	var workflow ExtensionWorkflow
	found := false
	for _, candidate := range definition.Manifest.Workflows {
		if candidate.Name == name {
			workflow, found = candidate, true
			break
		}
	}
	if !found {
		return "", nil, errors.New("harness: extension workflow is not declared")
	}
	if len(name) > 128 {
		return "", nil, errors.New("harness: extension workflow name is too long")
	}
	rawInput, err := json.Marshal(input)
	if err != nil || len(rawInput) > 64<<10 {
		return "", nil, errors.New("harness: extension workflow input exceeds the limit")
	}
	if len(workflow.InputSchema) != 0 {
		schema, err := resolveToolSchema(workflow.InputSchema)
		if err != nil {
			return "", nil, err
		}
		var object map[string]any
		if json.Unmarshal(rawInput, &object) != nil || object == nil || schema.Validate(object) != nil {
			return "", nil, errors.New("harness: extension workflow input does not satisfy the input schema")
		}
	}
	client, err := host.clientFor(ctx, definition, config)
	if err != nil {
		return "", nil, err
	}
	if !contains(client.definition.Provides, "workflow") ||
		!contains(client.registration.Workflows, name) {
		return "", nil, errors.New("harness: extension process did not register workflow")
	}
	var service *Service
	if len(services) != 0 {
		service = services[0]
	}
	callCtx, cancel := context.WithTimeout(ctx, extensionTimeout(definition.Manifest))
	defer cancel()
	invocation, err := host.beginInvocation(callCtx, id, service, run, definition.Manifest)
	if err != nil {
		return "", nil, err
	}
	defer host.endInvocation(invocation)
	params := extensionWorkflowParams(invocation.id, name, input, run, history,
		definition.Manifest.Needs, config.Workspace, config.Extensions.Settings[id])
	raw, err := client.rpc.Call(callCtx, "workflow.run", params)
	if err != nil {
		return "", nil, &CallError{Certain: false, Message: "extension workflow call interrupted; outcome unknown"}
	}
	var result extensionWorkflowResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", nil, &CallError{Certain: false, Message: "extension workflow returned invalid JSON; outcome unknown"}
	}
	if len(result.Output) > maxExtensionBlockChars {
		return "", nil, &CallError{Certain: false, Message: "extension workflow output exceeds the limit; outcome unknown"}
	}
	if len(result.Activity) != 0 {
		result.Activity = redactExtensionValue(host, result.Activity).(map[string]any)
		if err := validateExtensionActivity(result.Activity); err != nil {
			return "", nil, &CallError{Certain: false, Message: "extension workflow activity was invalid; outcome unknown"}
		}
	}
	return host.redact(result.Output), result.Activity, nil
}

func validateExtensionActivity(activity map[string]any) error {
	raw, err := json.Marshal(activity)
	if err != nil || len(raw) > 64<<10 {
		return errors.New("extension activity exceeds the limit")
	}
	if summary, ok := activity["summary"].(string); ok && len(summary) > 4096 {
		return errors.New("extension activity summary exceeds the limit")
	}
	if items, ok := activity["items"].([]any); ok && len(items) > 128 {
		return errors.New("extension activity items exceed the limit")
	}
	return nil
}

func applyExtensionNeeds(input *extensionInput, needs []string, run Run, history []ModelMessage, workspace, dataDir string) {
	input.Message, input.History, input.Workspace, input.ToolResults = nil, nil, "", nil
	input.Output, input.Status = "", ""
	input.DataDir = dataDir
	if len(needs) == 0 {
		input.Message = nil
		input.History = nil
		input.Workspace = ""
		return
	}
	for _, need := range needs {
		switch {
		case need == "message":
			input.Message = map[string]any{"role": "user", "content": run.Prompt}
		case need == "workspace":
			input.Workspace = workspace
		case strings.HasPrefix(need, "history:"):
			n, _ := strconv.Atoi(strings.TrimPrefix(need, "history:"))
			input.History = selectExtensionHistory(history, n)
		case need == "tool_results":
			var results []ModelMessage
			for _, message := range history {
				if message.Role == "tool" {
					results = append(results, message)
				}
			}
			input.ToolResults = boundExtensionHistory(results)
		case need == "output":
			input.Output = run.Output
		case need == "status":
			input.Status = run.Status
		}
	}
}

func extensionWorkflowParams(invocation, name string, workflowInput any, run Run,
	history []ModelMessage, needs []string, workspace string, settings map[string]any) map[string]any {
	selection := extensionInput{
		Settings: settings,
	}
	applyExtensionNeeds(&selection, needs, run, history, workspace, "")
	params := map[string]any{
		"invocation": invocation,
		"name":       name,
		"input":      workflowInput,
		"settings":   settings,
	}
	if selection.Message != nil {
		params["message"] = selection.Message
	}
	if len(selection.History) != 0 {
		params["history"] = selection.History
	}
	if selection.Workspace != "" {
		params["workspace"] = selection.Workspace
	}
	if selection.ToolResults != nil {
		params["tool_results"] = selection.ToolResults
	}
	if selection.Output != "" {
		params["output"] = selection.Output
	}
	if selection.Status != "" {
		params["status"] = selection.Status
	}
	return params
}

func selectExtensionHistory(history []ModelMessage, turns int) []ModelMessage {
	if turns <= 0 || len(history) == 0 {
		return nil
	}
	start := len(history)
	remaining := turns
	for start > 0 && remaining > 0 {
		start--
		if history[start].Role == "user" {
			remaining--
		}
	}
	return boundExtensionHistory(history[start:])
}

func boundExtensionHistory(history []ModelMessage) []ModelMessage {
	if len(history) == 0 {
		return nil
	}
	result := make([]ModelMessage, 0, min(len(history), maxExtensionHistoryMsgs))
	total := 0
	for index := len(history) - 1; index >= 0 && len(result) < maxExtensionHistoryMsgs; index-- {
		message, ok := fitExtensionHistoryMessage(history[index], maxExtensionHistoryBytes-total)
		if !ok {
			continue
		}
		raw, _ := json.Marshal(message)
		total += len(raw)
		result = append(result, message)
	}
	slices.Reverse(result)
	return result
}

func fitExtensionHistoryMessage(message ModelMessage, budget int) (ModelMessage, bool) {
	if budget <= 0 {
		return ModelMessage{}, false
	}
	raw, err := json.Marshal(message)
	if err == nil && len(raw) <= budget {
		return message, true
	}

	// History is context, not an execution record. If one stored message is
	// unusually large, keep its role and a bounded content prefix rather than
	// allowing one tool call or provider payload to consume the whole envelope.
	candidate := message
	candidate.Name = ""
	candidate.ToolCallID = ""
	candidate.ToolCalls = nil
	contentBudget := budget
	if contentBudget > 512 {
		contentBudget -= 512
	}
	for contentBudget >= 0 {
		candidate.Content = truncateUTF8Bytes(message.Content, contentBudget)
		raw, err = json.Marshal(candidate)
		if err == nil && len(raw) <= budget {
			return candidate, true
		}
		if contentBudget == 0 {
			break
		}
		step := contentBudget / 8
		if step < 1 {
			step = 1
		}
		contentBudget -= step
	}
	return ModelMessage{}, false
}

func truncateUTF8Bytes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit]
}

func extensionAllows(context ExtensionContext, phase, purpose string) bool {
	if len(context.Phases) == 0 {
		context.Phases = []string{"turn"}
	}
	if len(context.Purposes) == 0 {
		context.Purposes = []string{"chat"}
	}
	if len(context.Phases) != 0 && !contains(context.Phases, phase) {
		return false
	}
	if len(context.Purposes) != 0 && !contains(context.Purposes, purpose) {
		return false
	}
	return true
}

func extensionPlacementAllowed(context ExtensionContext, placement string) bool {
	if placement == "" {
		return false
	}
	if len(context.Placement) == 0 {
		return placement == "tail"
	}
	return contains(context.Placement, placement)
}

func boundExtensionBlocks(blocks []ExtensionBlock, maxChars int) []ExtensionBlock {
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].Priority != blocks[j].Priority {
			return blocks[i].Priority > blocks[j].Priority
		}
		if blocks[i].ExtensionID != blocks[j].ExtensionID {
			return blocks[i].ExtensionID < blocks[j].ExtensionID
		}
		return blocks[i].ID < blocks[j].ID
	})
	kept := make([]ExtensionBlock, 0, len(blocks))
	total := 0
	for _, block := range blocks {
		if total+block.Chars > maxChars || len(kept) >= maxExtensionBlocks {
			continue
		}
		total += block.Chars
		kept = append(kept, block)
	}
	return kept
}

func extensionBlockUses(blocks []ExtensionBlock) []ExtensionUse {
	var uses []ExtensionUse
	for _, block := range blocks {
		phase := block.Phase
		if phase == "" {
			phase = "turn"
		}
		uses = append(uses, ExtensionUse{Source: "extension", Name: block.ExtensionID,
			Version: block.Version, Phase: phase, Placement: block.Placement, BlockID: block.ID,
			Label: block.Label, SHA256: block.SHA256, Bytes: block.Bytes, Chars: block.Chars})
	}
	return uses
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (host *extensionHost) environment(id string, manifest ExtensionManifest, config Config) ([]string, error) {
	values := map[string]string{}
	// Windows names are case-insensitive; a manifest "windir" replaces the
	// inherited "WINDIR" instead of producing a duplicate launcher entry.
	set := func(name, value string) {
		if runtime.GOOS == "windows" {
			for existing := range values {
				if existing != name && strings.EqualFold(existing, name) {
					delete(values, existing)
				}
			}
		}
		values[name] = value
	}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(name); ok {
			set(name, value)
		}
	}
	for _, name := range manifest.Env {
		source := name
		if mapping := config.Extensions.EnvFrom[id]; mapping != nil && mapping[name] != "" {
			source = mapping[name]
		}
		if strings.HasPrefix(source, "key:") {
			if host.keys == nil {
				return nil, ErrKeyNotFound
			}
			profile, err := host.keys.Resolve(strings.TrimPrefix(source, "key:"))
			if err != nil {
				return nil, err
			}
			set(name, profile.secret)
			host.mu.Lock()
			if profile.secret != "" && !contains(host.secrets, profile.secret) {
				host.secrets = append(host.secrets, profile.secret)
			}
			host.mu.Unlock()
		} else if value, ok := os.LookupEnv(source); ok {
			if len(value) > 8192 || strings.ContainsRune(value, 0) {
				return nil, errors.New("extension environment value exceeds the limit")
			}
			set(name, value)
			// Plain variables also carry flags such as "1" or "true";
			// redacting those would corrupt unrelated text.
			if len(value) >= minRedactedEnvValue {
				host.mu.Lock()
				if !contains(host.secrets, value) {
					host.secrets = append(host.secrets, value)
				}
				host.mu.Unlock()
			}
		}
	}
	result := make([]string, 0, len(values))
	for name, value := range values {
		result = append(result, name+"="+value)
	}
	sort.Strings(result)
	return result, nil
}

func (host *extensionHost) redact(text string) string {
	host.mu.Lock()
	defer host.mu.Unlock()
	for _, secret := range host.secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, redactedText)
		}
	}
	return text
}

func extensionLogPath(dataDir, id string) (string, error) {
	if !validExtensionID(id) {
		return "", errors.New("harness: invalid extension identifier")
	}
	root, err := canonicalDir(dataDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "logs", "ext-"+safeLogID(id)+".log"), nil
}

func newInvocation() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("invocation-%d", time.Now().UnixNano())
}

func safeLogID(id string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(id)
}

func pathWithin(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
