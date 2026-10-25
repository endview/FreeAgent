package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Adapters are the intentionally small replacement points around the core
// runtime. Persistence, budgets, approvals and UNKNOWN handling stay here.
type Adapters struct {
	Model    ModelClient
	ModelFor ModelFactory
	Tools    ToolRunner
	Approve  ApprovalFunc
	Emit     EventFunc
	Refresh  func() (Config, error)
	// Keys defaults to the keys file chosen by apppaths.
	Keys *KeyStore
}

func NewService(store *Store, config Config, adapters Adapters) *Service {
	var extensions *extensionHost
	if store != nil {
		extensions = newExtensionHost(store.DataDir(), config.Workspace, config, adapters.Keys)
	}
	return &Service{
		Store: store, Model: adapters.Model, ModelFor: adapters.ModelFor, Tools: adapters.Tools, Config: config,
		Approve: adapters.Approve, Emit: adapters.Emit, Refresh: adapters.Refresh, Keys: adapters.Keys,
		extensions: extensions, controlMu: &sync.Mutex{}, configMu: &sync.RWMutex{},
	}
}

// TaskService is the local product boundary used by callers. It deliberately
// exposes task operations, not persistence tables or provider wire details.
type TaskService struct {
	mu         sync.Mutex
	core       *Service
	approve    ApprovalFunc
	emit       EventFunc
	cancel     context.CancelFunc
	activeChat string
}

func NewTaskService(core *Service) *TaskService {
	tasks := &TaskService{core: core}
	if core != nil {
		tasks.approve, tasks.emit = core.Approve, core.Emit
	}
	return tasks
}

func (tasks *TaskService) Config() Config {
	if tasks == nil || tasks.core == nil {
		return Config{}
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	return cloneConfig(tasks.core.currentConfig())
}

func cloneConfig(config Config) Config {
	raw, _ := json.Marshal(config)
	var cloned Config
	_ = json.Unmarshal(raw, &cloned)
	cloned.instructionText = config.instructionText
	return cloned
}

// Keys returns the key store used for dispatch so a local UI can manage key
// profiles. It is nil only for callers that supplied their own model adapter.
func (tasks *TaskService) Keys() *KeyStore {
	if tasks == nil || tasks.core == nil {
		return nil
	}
	return tasks.core.Keys
}

func (tasks *TaskService) DataDir() string {
	if tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return ""
	}
	return tasks.core.Store.DataDir()
}

func (tasks *TaskService) Extensions(ctx context.Context) ([]ExtensionInfo, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return nil, errors.New("harness: task service and context are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config := tasks.Config()
	user := extensionRegistry{Extensions: map[string]extensionDefinition{}, Errors: map[string]error{}}
	if err := scanExtensionRoot(&user, filepath.Join(tasks.DataDir(), "extensions"), "user"); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	workspace, err := canonicalDir(config.Workspace)
	if err != nil {
		return nil, err
	}
	workspaceRegistry := extensionRegistry{Extensions: map[string]extensionDefinition{}, Errors: map[string]error{}}
	if err := scanExtensionRoot(&workspaceRegistry, filepath.Join(workspace, ".freeagent", "extensions"), "workspace"); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	workspaceTrusted := config.Extensions.TrustedWorkspaces[workspace]
	result := make([]ExtensionInfo, 0, len(user.Extensions)+len(workspaceRegistry.Extensions))
	effective := make(map[string]extensionDefinition, len(user.Extensions)+len(workspaceRegistry.Extensions))
	for id, definition := range user.Extensions {
		effective[id] = definition
	}
	for id, definition := range workspaceRegistry.Extensions {
		if !workspaceTrusted && effective[id].Manifest.ID != "" {
			continue
		}
		effective[id] = definition
	}
	for _, definition := range effective {
		available := definition.Source == "user" || workspaceTrusted
		result = append(result, extensionInfo(definition, config, available, available, workspaceTrusted, nil))
	}
	for id, problem := range user.Errors {
		result = append(result, ExtensionInfo{ID: id, Source: "user", Error: safeError(problem), State: "error"})
	}
	if workspaceTrusted {
		for id, problem := range workspaceRegistry.Errors {
			result = append(result, ExtensionInfo{ID: id, Source: "workspace", Error: safeError(problem), State: "error",
				Trusted: true, WorkspaceTrusted: true})
		}
	}
	if tasks.core.extensions != nil {
		tasks.core.extensions.mu.Lock()
		for index := range result {
			if client := tasks.core.extensions.clients[result[index].ID]; client != nil && client.ready && !client.rpc.Closed() {
				result[index].Provides = append([]string(nil), client.definition.Provides...)
				result[index].State = "running"
			}
		}
		tasks.core.extensions.mu.Unlock()
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID != result[j].ID {
			return result[i].ID < result[j].ID
		}
		return result[i].Source < result[j].Source
	})
	return result, nil
}

func (tasks *TaskService) Extension(ctx context.Context, id string) (ExtensionInfo, error) {
	if !validExtensionID(id) {
		return ExtensionInfo{}, errors.New("harness: invalid extension identifier")
	}
	items, err := tasks.Extensions(ctx)
	if err != nil {
		return ExtensionInfo{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return ExtensionInfo{}, os.ErrNotExist
}

func (tasks *TaskService) SetExtensionEnabled(ctx context.Context, id string, enabled bool) error {
	item, err := tasks.Extension(ctx, id)
	if err != nil {
		return err
	}
	if !item.Available {
		return errors.New("harness: workspace extension must be trusted before enabling")
	}
	return tasks.ConfigureModels(ctx, func(config *Config) error {
		if config.Extensions.Enabled == nil {
			config.Extensions.Enabled = map[string]bool{}
		}
		if !enabled && (item.Required || config.Extensions.Required[id]) {
			return errors.New("harness: required extension cannot be disabled")
		}
		config.Extensions.Enabled[id] = enabled
		return nil
	})
}

func (tasks *TaskService) SetExtensionRequired(ctx context.Context, id string, required bool) error {
	item, err := tasks.Extension(ctx, id)
	if err != nil {
		return err
	}
	if !item.Available {
		return errors.New("harness: extension is unavailable")
	}
	return tasks.ConfigureModels(ctx, func(config *Config) error {
		if required && !config.Extensions.Enabled[id] {
			return errors.New("harness: required extension must be enabled")
		}
		if config.Extensions.Required == nil {
			config.Extensions.Required = map[string]bool{}
		}
		if required {
			config.Extensions.Required[id] = true
		} else {
			delete(config.Extensions.Required, id)
		}
		return nil
	})
}

func (tasks *TaskService) SetExtensionSettings(ctx context.Context, id string, settings map[string]any) error {
	item, err := tasks.Extension(ctx, id)
	if err != nil {
		return err
	}
	if !item.Available {
		return errors.New("harness: extension is unavailable")
	}
	raw, err := json.Marshal(settings)
	if err != nil || len(raw) > 64<<10 {
		return errors.New("harness: extension settings exceed the limit")
	}
	return tasks.ConfigureModels(ctx, func(config *Config) error {
		if config.Extensions.Settings == nil {
			config.Extensions.Settings = map[string]map[string]any{}
		}
		if settings == nil {
			delete(config.Extensions.Settings, id)
		} else {
			config.Extensions.Settings[id] = settings
		}
		return nil
	})
}

func (tasks *TaskService) SetExtensionEnvFrom(ctx context.Context, id string, values map[string]string) error {
	item, err := tasks.Extension(ctx, id)
	if err != nil {
		return err
	}
	if !item.Available {
		return errors.New("harness: extension is unavailable")
	}
	raw, err := json.Marshal(values)
	if err != nil || len(raw) > 64<<10 {
		return errors.New("harness: extension environment mapping exceeds the limit")
	}
	return tasks.ConfigureModels(ctx, func(config *Config) error {
		if config.Extensions.EnvFrom == nil {
			config.Extensions.EnvFrom = map[string]map[string]string{}
		}
		if values == nil {
			delete(config.Extensions.EnvFrom, id)
		} else {
			config.Extensions.EnvFrom[id] = values
		}
		return nil
	})
}

func (tasks *TaskService) TrustWorkspaceExtensions(ctx context.Context, trusted bool) error {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return errors.New("harness: task service and context are required")
	}
	workspace, err := canonicalDir(tasks.Config().Workspace)
	if err != nil {
		return err
	}
	return tasks.ConfigureModels(ctx, func(config *Config) error {
		if config.Extensions.TrustedWorkspaces == nil {
			config.Extensions.TrustedWorkspaces = map[string]bool{}
		}
		if trusted {
			config.Extensions.TrustedWorkspaces[workspace] = true
		} else {
			delete(config.Extensions.TrustedWorkspaces, workspace)
		}
		return nil
	})
}

func (tasks *TaskService) RestartExtension(ctx context.Context, id string) error {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.extensions == nil {
		return errors.New("harness: extension runtime is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tasks.core.extensions.restart(id)
}

func (tasks *TaskService) ExtensionLog(id string) ([]byte, error) {
	path, err := extensionLogPath(tasks.DataDir(), id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 1<<20 {
		raw = raw[len(raw)-(1<<20):]
	}
	return raw, nil
}

func (tasks *TaskService) ClearExtensionData(ctx context.Context, id string) error {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return errors.New("harness: task service and context are required")
	}
	if _, err := tasks.Extension(ctx, id); err != nil {
		return err
	}
	root, err := canonicalDir(tasks.DataDir())
	if err != nil {
		return err
	}
	target := filepath.Join(root, "extensions", id, "data")
	parent := filepath.Join(root, "extensions", id)
	if !pathWithin(parent, target) || filepath.Clean(target) == filepath.Clean(parent) {
		return errors.New("harness: invalid extension data path")
	}
	return os.RemoveAll(target)
}

// DeleteExtension removes only user-installed extensions. Workspace
// extensions remain owned by the workspace and must be changed there.
func (tasks *TaskService) DeleteExtension(ctx context.Context, id string) error {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return errors.New("harness: task service and context are required")
	}
	if !validExtensionID(id) {
		return errors.New("harness: invalid extension identifier")
	}
	path := filepath.Join(tasks.DataDir(), "extensions", id)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("harness: user extension directory is invalid")
	}
	if err := tasks.ConfigureModels(ctx, func(config *Config) error {
		delete(config.Extensions.Enabled, id)
		delete(config.Extensions.Required, id)
		delete(config.Extensions.Settings, id)
		delete(config.Extensions.EnvFrom, id)
		return nil
	}); err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func (tasks *TaskService) MCPServers() []MCPServerStatus {
	if tasks == nil || tasks.core == nil {
		return nil
	}
	config := tasks.Config()
	if runtime, ok := tasks.core.Tools.(*RuntimeTools); ok {
		return runtime.MCPStatuses(config)
	}
	if manager, ok := tasks.core.Tools.(*ToolRuntimeManager); ok {
		return manager.MCPStatuses(config)
	}
	result := make([]MCPServerStatus, 0, len(config.MCPServers))
	for name, server := range config.MCPServers {
		result = append(result, MCPServerStatus{ID: name, Enabled: server.Enabled})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (tasks *TaskService) TestMCP(ctx context.Context, name string, override *MCPServerConfig) (MCPServerStatus, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return MCPServerStatus{}, errors.New("harness: task service and context are required")
	}
	config := tasks.Config()
	server, ok := config.MCPServers[name]
	if override != nil {
		server = *override
		ok = true
	}
	if !ok {
		return MCPServerStatus{}, errors.New("harness: MCP server not found")
	}
	server.Enabled = true
	config.MCPServers = map[string]MCPServerConfig{name: server}
	if err := config.Validate(); err != nil {
		return MCPServerStatus{}, err
	}
	files, err := NewFileTools(config.Workspace, tasks.DataDir())
	if err != nil {
		return MCPServerStatus{}, err
	}
	runtime := NewRuntimeTools(files)
	runtime.SetKeyStore(tasks.Keys())
	defer runtime.Close()
	if err := runtime.Connect(ctx, config, name); err != nil {
		return MCPServerStatus{}, err
	}
	for _, status := range runtime.MCPStatuses(config) {
		return status, nil
	}
	return MCPServerStatus{}, errors.New("harness: MCP server test returned no status")
}

func (tasks *TaskService) RestartMCP(ctx context.Context) error {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return errors.New("harness: task service and context are required")
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	if tasks.cancel != nil || !tasks.core.Store.runMu.TryLock() {
		return ErrActiveRun
	}
	defer tasks.core.Store.runMu.Unlock()
	runtime, ok := tasks.core.Tools.(*RuntimeTools)
	if ok {
		return runtime.RestartMCP(ctx, cloneConfig(tasks.core.currentConfig()), "")
	}
	if manager, ok := tasks.core.Tools.(*ToolRuntimeManager); ok {
		return manager.RestartMCP(ctx, cloneConfig(tasks.core.currentConfig()))
	}
	return errors.New("harness: MCP runtime is not managed by this service")
}

func (tasks *TaskService) MCPLog(name string) ([]byte, error) {
	if tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return nil, errors.New("harness: task service is required")
	}
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, "\x00/\\") {
		return nil, errors.New("harness: invalid MCP server name")
	}
	path := filepath.Join(tasks.DataDir(), "logs", "mcp-"+name+".log")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 1<<20 {
		raw = raw[len(raw)-(1<<20):]
	}
	return raw, nil
}

// ConfigureModels edits the configuration through the store this service
// already owns; a second owner lock in the same process would fail. Like
// Configure, it never rewrites run bindings and refuses safety changes while
// tasks are unfinished.
func (tasks *TaskService) ConfigureModels(ctx context.Context, edit func(*Config) error) error {
	if ctx == nil || tasks == nil || tasks.core == nil || edit == nil {
		return errors.New("harness: task service, context and edit are required")
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	if tasks.cancel != nil || !tasks.core.Store.runMu.TryLock() {
		return ErrActiveRun
	}
	defer tasks.core.Store.runMu.Unlock()
	config := cloneConfig(tasks.core.currentConfig())
	before, err := safetyConfigJSON(config)
	if err != nil {
		return err
	}
	if err := edit(&config); err != nil {
		return err
	}
	if err := config.Validate(); err != nil {
		return err
	}
	after, err := safetyConfigJSON(config)
	if err != nil {
		return err
	}
	if before != after {
		var unfinished int
		if err := tasks.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM runs
			WHERE status IN ('running','waiting_confirmation','stopped','unknown','limited')`).Scan(&unfinished); err != nil {
			return err
		}
		if unfinished != 0 {
			return fmt.Errorf("%w; finish or abandon unfinished tasks before changing configuration", ErrActiveRun)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := WriteConfig(tasks.core.Store.DataDir(), config); err != nil {
		return err
	}
	tasks.core.setConfig(config)
	if tasks.core.extensions != nil {
		tasks.core.extensions.configure(config)
	}
	if tasks.core.control != nil {
		if err := tasks.core.control.refreshBaseline(); err != nil {
			return err
		}
	}
	return nil
}

func (tasks *TaskService) RuntimePolicy() ToolPolicy {
	if tasks == nil || tasks.core == nil {
		return ToolPolicy{}
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	return tasks.core.globalToolPolicy()
}

// ConfigureRuntimePolicy changes only future-run tool policy. It deliberately
// does not acquire runMu or reject an active Run; existing Service copies keep
// their immutable tool view and Continue reconstructs it from its snapshot.
func (tasks *TaskService) ConfigureRuntimePolicy(ctx context.Context, baseGeneration string, edit func(*Config) error) (ToolPolicy, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || edit == nil {
		return ToolPolicy{}, errors.New("harness: runtime policy, context and edit are required")
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	current := tasks.core.currentConfig()
	oldPolicy := tasks.core.globalToolPolicy()
	if baseGeneration != "" && baseGeneration != oldPolicy.Generation {
		return oldPolicy, ErrGenerationConflict
	}
	candidate := cloneConfig(current)
	if err := edit(&candidate); err != nil {
		return oldPolicy, err
	}
	normalizedExecutables, err := normalizeCommandAllowlist(candidate.Commands.AllowedExecutables)
	if err != nil {
		return oldPolicy, err
	}
	candidate.Commands.AllowedExecutables = normalizedExecutables
	roots, err := normalizeComputerRoots(ctx, tasks.DataDir(), candidate.ComputerRoots)
	if err != nil {
		return oldPolicy, err
	}
	candidate.ComputerRoots = roots
	executionRoots, err := normalizeComputerRoots(ctx, tasks.DataDir(), candidate.Execution.AdditionalRoots)
	if err != nil {
		return oldPolicy, err
	}
	candidate.Execution.AdditionalRoots = executionRoots
	blocked, err := normalizeBlockedPaths(ctx, candidate.Execution.BlockedPaths)
	if err != nil {
		return oldPolicy, err
	}
	candidate.Execution.BlockedPaths = blocked
	if canonicalFileScope(candidate.Execution.FileScope) == FileScopeWorkspacePlusAllowedRoots && len(candidate.ComputerRoots) == 0 {
		return oldPolicy, ErrComputerRootsRequired
	}
	if err := validateCommandPolicy(candidate.Commands); err != nil {
		return oldPolicy, err
	}
	if err := candidate.Validate(); err != nil {
		return oldPolicy, err
	}
	profile, err := executionProfileForConfig(candidate)
	if err != nil {
		return oldPolicy, err
	}
	newPolicy := toolPolicyForProfile(candidate, profile)
	// The global policy also exposes roots that are waiting for a conversation
	// selection. They remain configuration state even when the default Ask
	// profile does not expose them to the current Run.
	newPolicy.File.ComputerFiles = candidate.ComputerFiles
	newPolicy.File.ComputerRoots = append([]string(nil), roots...)
	newPolicy.Profile.Generation = oldPolicy.Generation
	if !policyEqual(oldPolicy, newPolicy) {
		candidate.ToolGeneration = nextToolGeneration(oldPolicy.Generation)
		newPolicy.Generation = candidate.ToolGeneration
		newPolicy.Profile.Generation = candidate.ToolGeneration
	}
	var view *RuntimeToolView
	if tasks.core.toolManager != nil {
		view = tasks.core.toolManager.Build(candidate)
	}
	if err := WriteConfig(tasks.core.Store.DataDir(), candidate); err != nil {
		return oldPolicy, err
	}
	tasks.core.setConfig(candidate)
	if tasks.core.toolManager != nil && view != nil {
		tasks.core.toolManager.Replace(view)
	}
	if tasks.core.control != nil {
		if err := tasks.core.control.refreshBaseline(); err != nil {
			return oldPolicy, err
		}
	}
	return newPolicy, nil
}

func validateCommandPolicy(config CommandConfig) error {
	if err := validateCommandAllowlist(config.AllowedExecutables); err != nil {
		return err
	}
	if !config.Enabled {
		return nil
	}
	for _, path := range config.AllowedExecutables {
		if err := verifyRegularFile(path); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidExecutable, filepath.Clean(path))
		}
	}
	return nil
}

func (tasks *TaskService) SetApproval(approve ApprovalFunc) {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	tasks.approve = approve
}

func (tasks *TaskService) SetEvents(emit EventFunc) {
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	tasks.emit = emit
	if tasks.core != nil && tasks.core.commands != nil {
		tasks.core.commands.mu.Lock()
		tasks.core.commands.emit = emit
		tasks.core.commands.mu.Unlock()
	}
}

func (tasks *TaskService) execute(ctx context.Context, call func(context.Context, *Service) (RunResult, error)) (RunResult, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return RunResult{}, errors.New("harness: task service and context are required")
	}
	tasks.mu.Lock()
	if tasks.cancel != nil {
		tasks.mu.Unlock()
		return RunResult{}, ErrActiveRun
	}
	child, cancel := context.WithCancel(ctx)
	core := tasks.core.runCopy()
	core.Approve, core.Emit = tasks.approve, tasks.emit
	tasks.cancel = cancel
	tasks.mu.Unlock()
	defer func() {
		cancel()
		tasks.mu.Lock()
		tasks.cancel = nil
		tasks.activeChat = ""
		tasks.mu.Unlock()
	}()
	return call(child, &core)
}

// Cancel stops new dispatch and propagates to the active provider/tool.
// A dispatched outcome may remain UNKNOWN; cancellation never promises undo.
func (tasks *TaskService) Cancel() bool {
	if tasks == nil {
		return false
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	if tasks.cancel == nil {
		return false
	}
	tasks.cancel()
	return true
}

type RunOptions struct {
	ModelID           string                 `json:"model_id,omitempty"`
	Collaboration     *CollaborationSettings `json:"collaboration,omitempty"`
	Review            SuggestionReview       `json:"-"`
	Memory            *MemorySelection       `json:"memory,omitempty"`
	Permission        PermissionMode         `json:"permission,omitempty"`
	ConfirmFullAccess bool                   `json:"confirm_full_access,omitempty"`
	Capabilities      *CapabilitySelection   `json:"capabilities,omitempty"`
}

func (tasks *TaskService) Submit(ctx context.Context, conversationID, requestKey, prompt string) (RunResult, error) {
	return tasks.SubmitWithOptions(ctx, conversationID, requestKey, prompt, RunOptions{})
}

func (tasks *TaskService) SubmitWithOptions(ctx context.Context, conversationID, requestKey, prompt string, options RunOptions) (RunResult, error) {
	if strings.TrimSpace(prompt) == "" {
		return RunResult{}, errors.New("harness: task prompt is required")
	}
	return tasks.execute(ctx, func(ctx context.Context, core *Service) (RunResult, error) {
		core.selection = options.ModelID
		if err := core.validate(ctx); err != nil {
			return RunResult{}, err
		}
		createdConversation := conversationID == ""
		if createdConversation {
			title := strings.SplitN(strings.TrimSpace(prompt), "\n", 2)[0]
			runes := []rune(title)
			if len(runes) > 80 {
				title = string(runes[:80])
			}
			conversation, err := core.Store.CreateConversation(ctx, title, core.Config.Workspace)
			if err != nil {
				return RunResult{}, err
			}
			conversationID = conversation.ID
		}
		if createdConversation && options.Permission != "" {
			if err := NewTaskService(core).SelectPermission(ctx, conversationID, options.Permission, options.ConfirmFullAccess); err != nil {
				return RunResult{}, err
			}
		}
		if requestKey == "" {
			requestKey = newID()
		}
		if err := core.resolvePermission(ctx, conversationID, options.Permission, options.ConfirmFullAccess); err != nil {
			return RunResult{}, err
		}
		if options.Capabilities == nil {
			selected, err := core.Store.selectedCapabilities(ctx, conversationID, core.Config)
			if err != nil {
				return RunResult{}, err
			}
			core.capabilities = &selected
		} else {
			selected := options.Capabilities.normalize()
			if err := selected.validate(core.Config); err != nil {
				return RunResult{}, err
			}
			core.capabilities = &selected
		}
		tasks.mu.Lock()
		tasks.activeChat = conversationID
		tasks.mu.Unlock()
		if err := core.selectMemory(ctx, conversationID, options.Memory); err != nil {
			return RunResult{}, err
		}
		settings, preference, err := NewTaskService(core).collaborationSettings(ctx, conversationID)
		if err != nil {
			return RunResult{}, err
		}
		if options.Collaboration != nil {
			settings = *options.Collaboration
			if err := settings.validate(core.Config, core.Tools); err != nil {
				return RunResult{}, err
			}
		}
		if settings.Mode == "ask" || settings.Mode == "auto" {
			modelID := options.ModelID
			if modelID == "" {
				chat, err := core.Store.GetConversation(ctx, conversationID)
				if err != nil {
					return RunResult{}, err
				}
				modelID = chat.ModelID
			}
			collab := NewCollaboration(NewTaskService(core))
			result, err := collab.startGoal(ctx, conversationID, requestKey, prompt, modelID, preference, settings, options.Review)
			if err != nil || result.Run.Status != "stopped" || result.Duplicate {
				return result, err
			}
			return tasks.continueComposition(ctx, core, result.Run, options.Review)
		}
		return core.Run(ctx, conversationID, requestKey, prompt)
	})
}

// RunWorkflow invokes one explicitly selected extension workflow as a normal
// foreground turn. The turn is persisted through the existing Run, Message
// and Attempt records; no workflow-specific task object is created.
func (tasks *TaskService) RunWorkflow(ctx context.Context, conversationID, requestKey, extensionID, workflowName string, input any) (RunResult, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return RunResult{}, errors.New("harness: task service and context are required")
	}
	if strings.TrimSpace(conversationID) == "" || strings.TrimSpace(requestKey) == "" {
		return RunResult{}, errors.New("harness: workflow conversation and request key are required")
	}
	if !validExtensionID(extensionID) || strings.TrimSpace(workflowName) == "" || len(workflowName) > 128 {
		return RunResult{}, errors.New("harness: invalid extension workflow")
	}
	rawInput, err := json.Marshal(input)
	if err != nil || len(rawInput) > 64<<10 {
		return RunResult{}, errors.New("harness: extension workflow input exceeds the limit")
	}
	prompt := "workflow " + extensionID + "/" + workflowName + "\ninput: " + string(rawInput)
	if len(prompt) > tasks.Config().MaxContextBytes {
		return RunResult{}, ErrContextLimit
	}
	return tasks.execute(ctx, func(ctx context.Context, core *Service) (RunResult, error) {
		core.controlToolsDisabled = true
		if err := core.validate(ctx); err != nil {
			return RunResult{}, err
		}
		conversation, err := core.Store.GetConversation(ctx, conversationID)
		if err != nil {
			return RunResult{}, err
		}
		if filepath.Clean(conversation.Workspace) != filepath.Clean(core.Config.Workspace) {
			return RunResult{}, errors.New("harness: conversation workspace does not match selected workspace")
		}
		if repeated, exists, err := core.Store.repeatedRun(ctx, conversationID, requestKey, prompt); err != nil {
			return RunResult{}, err
		} else if exists {
			evidence, evidenceErr := core.Store.Evidence(ctx, repeated.ID)
			return RunResult{Run: repeated, Answer: repeated.Output, Duplicate: true, Evidence: evidence}, evidenceErr
		}
		if err := core.Store.checkUncontrolled(ctx, conversationID); err != nil {
			return RunResult{}, err
		}
		if !core.Config.Extensions.Enabled[extensionID] {
			return RunResult{}, errors.New("harness: extension is not enabled")
		}
		item, err := tasks.Extension(ctx, extensionID)
		if err != nil {
			return RunResult{}, err
		}
		var declared bool
		for _, workflow := range item.Workflows {
			if workflow.Name == workflowName {
				declared = true
				break
			}
		}
		if !declared {
			return RunResult{}, errors.New("harness: extension workflow is not declared")
		}
		if err := core.resolvePermission(ctx, conversationID, "", false); err != nil {
			return RunResult{}, err
		}
		selected, err := core.Store.selectedCapabilities(ctx, conversationID, core.Config)
		if err != nil {
			return RunResult{}, err
		}
		if !contains(selected.Extensions, extensionID) {
			selected.Extensions = append(selected.Extensions, extensionID)
			selected = selected.normalize()
		}
		if err := selected.validate(core.Config); err != nil {
			return RunResult{}, err
		}
		core.capabilities = &selected
		if err := core.selectMemory(ctx, conversationID, nil); err != nil {
			return RunResult{}, err
		}
		bound, err := core.forTask(ctx, conversationID, conversation.ModelID, prompt)
		if err != nil {
			return RunResult{}, err
		}
		snapshot, err := bound.snapshot()
		if err != nil {
			return RunResult{}, err
		}
		if err := bound.checkConfig(Run{ConfigJSON: snapshot}); err != nil {
			return RunResult{}, err
		}
		if !core.Store.runMu.TryLock() {
			return RunResult{}, ErrActiveRun
		}
		defer core.Store.runMu.Unlock()
		run, created, err := bound.Store.CreateRun(ctx, conversationID, requestKey, prompt, snapshot)
		if err != nil {
			return RunResult{}, err
		}
		if !created {
			evidence, evidenceErr := bound.Store.Evidence(ctx, run.ID)
			return RunResult{Run: run, Answer: run.Output, Duplicate: true, Evidence: evidence}, evidenceErr
		}
		tasks.mu.Lock()
		tasks.activeChat = conversationID
		tasks.mu.Unlock()
		bound.event(Event{Kind: "run_started", Message: run.ID, RunID: run.ID, ConversationID: run.ConversationID})
		clock := &runClock{parent: ctx, store: bound.Store, runID: run.ID,
			limit: time.Duration(bound.limits().MaxRunSeconds) * time.Second, activity: bound.activity}
		if err := clock.resume(); err != nil {
			persistCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_ = bound.Store.FinishRun(persistCtx, run.ID, "limited", safeError(err))
			return RunResult{Run: run}, err
		}
		attempt, err := bound.Store.startWorkflow(clock.ctx, run, extensionID, workflowName, input,
			bound.limits().MaxSteps, nil)
		if err != nil {
			_ = clock.pause()
			persistCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_ = bound.Store.FinishRun(persistCtx, run.ID, "failed", safeError(err))
			return RunResult{Run: run}, err
		}
		output, activity, workflowErr := bound.RunExtensionWorkflow(clock.ctx, run, extensionID, workflowName, input)
		clockErr := clock.pause()
		workflowErr = errors.Join(workflowErr, clockErr)
		if len(activity) != 0 {
			summary, _ := activity["summary"].(string)
			if summary == "" {
				summary = "workflow activity"
			}
			bound.event(Event{Kind: "extension_activity", Message: summary, Activity: activity,
				RunID: run.ID, ConversationID: run.ConversationID})
		}
		persistCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		status, reason := "completed", ""
		if workflowErr != nil {
			status, reason = "failed", safeError(workflowErr)
			if workflowOutcomeUnknown(workflowErr) {
				status, reason = "unknown", "workflow outcome unknown; inspect saved attempt"
			}
		}
		// A workflow may catch a Host API error and still return output. Existing
		// persisted UNKNOWN facts must dominate that output; otherwise a
		// swallowed nested call would be presented as a successful workflow.
		if current, loadErr := bound.Store.GetRun(persistCtx, run.ID); loadErr == nil {
			if current.Status == "unknown" {
				status, reason = "unknown", "workflow outcome unknown; inspect saved attempt"
				workflowErr = errors.Join(workflowErr, ErrUnknownResult)
			} else if attempts, attemptsErr := bound.Store.ListAttempts(persistCtx, run.ID); attemptsErr == nil {
				for _, saved := range attempts {
					if saved.ID != attempt.ID && (saved.Status == "unknown" || saved.Status == "dispatched") {
						status, reason = "unknown", "workflow contains an unresolved Host operation"
						workflowErr = errors.Join(workflowErr, ErrUnknownResult)
						break
					}
				}
			} else {
				workflowErr = errors.Join(workflowErr, attemptsErr)
				status, reason = "unknown", "could not verify workflow child operations"
			}
		} else {
			workflowErr = errors.Join(workflowErr, loadErr)
			status, reason = "unknown", "could not verify workflow run state"
		}
		var settleErr error
		if status == "unknown" {
			record, _ := json.Marshal(ToolResult{Content: safeError(workflowErr), IsError: true})
			settleErr = bound.Store.finishWorkflowUnknown(persistCtx, run, attempt, reason, string(record))
			workflowErr = errors.Join(ErrUnknownResult, workflowErr)
		} else if status == "failed" {
			settleErr = bound.Store.finishWorkflow(persistCtx, run, attempt,
				ToolResult{Content: "workflow error: " + safeError(workflowErr), IsError: true},
				"failed", reason, "", nil, 0)
		} else {
			settleErr = bound.Store.finishWorkflow(persistCtx, run, attempt,
				ToolResult{Content: output, Extensions: []ExtensionUse{{Source: "extension",
					Name: extensionID, BlockID: workflowName}}},
				status, reason, output, nil, len(output))
		}
		if settleErr != nil {
			current, loadErr := bound.Store.GetRun(persistCtx, run.ID)
			if loadErr != nil {
				return RunResult{Run: run}, errors.Join(workflowErr, settleErr, loadErr)
			}
			evidence, evidenceErr := bound.Store.Evidence(persistCtx, run.ID)
			return RunResult{Run: current, Answer: current.Output, Evidence: evidence},
				errors.Join(workflowErr, settleErr, evidenceErr)
		}
		run, err = bound.Store.GetRun(persistCtx, run.ID)
		if err != nil {
			return RunResult{}, errors.Join(workflowErr, err)
		}
		evidence, evidenceErr := bound.Store.Evidence(persistCtx, run.ID)
		return RunResult{Run: run, Answer: output, Evidence: evidence}, errors.Join(workflowErr, evidenceErr)
	})
}

func (tasks *TaskService) Continue(ctx context.Context, runID string) (RunResult, error) {
	return tasks.ContinueWithOptions(ctx, runID, RunOptions{})
}

func (tasks *TaskService) ContinueWithOptions(ctx context.Context, runID string, options RunOptions) (RunResult, error) {
	if strings.TrimSpace(runID) == "" {
		return RunResult{}, errors.New("harness: run id is required")
	}
	return tasks.execute(ctx, func(ctx context.Context, core *Service) (RunResult, error) {
		run, err := core.Store.GetRun(ctx, runID)
		if err != nil {
			return RunResult{}, err
		}
		tasks.mu.Lock()
		tasks.activeChat = run.ConversationID
		tasks.mu.Unlock()
		if controlled, err := core.Store.controlledParent(ctx, runID); err != nil {
			return RunResult{}, err
		} else if controlled == runID {
			return tasks.continueComposition(ctx, core, run, options.Review)
		}
		return core.Continue(ctx, runID)
	})
}

func (tasks *TaskService) ContinueLatest(ctx context.Context, conversationID string) (RunResult, error) {
	if tasks == nil || tasks.core == nil {
		return RunResult{}, errors.New("harness: task service is not configured")
	}
	run, err := tasks.core.Store.GetLatestRun(ctx, conversationID)
	if err != nil {
		return RunResult{}, err
	}
	return tasks.Continue(ctx, run.ID)
}

var ErrApprovalChanged = errors.New("harness: confirmation no longer matches the exact operation; preview again")

type Confirmation struct {
	RunID              string         `json:"run_id"`
	Attempt            string         `json:"attempt_id"`
	CallID             string         `json:"call_id"`
	Digest             string         `json:"digest"`
	Tool               string         `json:"tool"`
	Preview            string         `json:"preview"`
	Items              []ApprovalItem `json:"items,omitempty"`
	ControlOperationID string         `json:"control_operation_id,omitempty"`
}

func confirmationFor(runID, attemptID string, call ToolCall, prepared PreparedTool) Confirmation {
	raw, _ := json.Marshal(struct {
		RunID   string
		Attempt string
		Call    ToolCall
		Preview string
		Items   []ApprovalItem
	}{runID, attemptID, call, prepared.Preview, prepared.Items})
	return Confirmation{RunID: runID, Attempt: attemptID, CallID: call.ID, Digest: contentHash(raw), Tool: call.Name, Preview: prepared.Preview, Items: prepared.Items, ControlOperationID: prepared.controlOperationID}
}

// Preview binds a caller's choice to the saved call and current exact preview.
func (tasks *TaskService) Preview(ctx context.Context, runID string) (Confirmation, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Tools == nil {
		return Confirmation{}, errors.New("harness: task service, tools and context are required")
	}
	return previewConfirmation(ctx, tasks.core, runID)
}

func previewConfirmation(ctx context.Context, core *Service, runID string) (Confirmation, error) {
	run, err := core.Store.GetRun(ctx, runID)
	if err != nil {
		return Confirmation{}, err
	}
	core, err = core.forRun(run)
	if err != nil {
		return Confirmation{}, err
	}
	core.allowFrozenContinuation = run.Status == "waiting_confirmation" && core.hasPendingToolApproval(run.ID)
	if run.Status == "unknown" {
		return Confirmation{}, ErrUnknownResult
	}
	if run.Status != "waiting_confirmation" && run.Status != "stopped" {
		return Confirmation{}, errors.New("harness: task is not awaiting confirmation")
	}
	snapshot, err := core.snapshot()
	if err != nil || snapshot != run.ConfigJSON {
		return Confirmation{}, ErrConfigChanged
	}
	if err := core.checkConfig(run); err != nil {
		return Confirmation{}, err
	}
	attempts, err := core.Store.ListAttempts(ctx, runID)
	if err != nil {
		return Confirmation{}, err
	}
	for _, attempt := range attempts {
		if attempt.Status == "unknown" || attempt.Status == "dispatched" {
			return Confirmation{}, ErrUnknownResult
		}
		if attempt.Status != "pending" || attempt.Kind != "tool" {
			continue
		}
		var call ToolCall
		if json.Unmarshal([]byte(attempt.RequestJSON), &call) != nil {
			return Confirmation{}, errors.New("harness: invalid pending tool call")
		}
		prepared, err := core.runtimeTools(run.ID, attempt.ID).Prepare(ctx, call)
		if err != nil {
			return Confirmation{}, err
		}
		if prepared.release != nil {
			defer prepared.release()
		}
		extensionAsk, extensionDeny, err := core.checkOperation(ctx, run, call, prepared)
		if err != nil {
			return Confirmation{}, err
		}
		if extensionDeny != "" {
			return Confirmation{}, errors.New("harness: operation was denied by an extension")
		}
		if prepared.controlProposal || core.needsApproval(prepared) || extensionAsk {
			return confirmationFor(runID, attempt.ID, call, prepared), nil
		}
	}
	return Confirmation{}, errors.New("harness: no consequential operation awaiting confirmation")
}

// Confirm grants exactly one decision, never authority for later model edits.
func (tasks *TaskService) Confirm(ctx context.Context, runID string, decision Confirmation, approved bool) (RunResult, error) {
	return tasks.execute(ctx, func(ctx context.Context, core *Service) (RunResult, error) {
		run, err := core.Store.GetRun(ctx, runID)
		if err != nil {
			return RunResult{}, err
		}
		tasks.mu.Lock()
		tasks.activeChat = run.ConversationID
		tasks.mu.Unlock()
		actual, err := previewConfirmation(ctx, core, runID)
		if err != nil {
			return RunResult{}, errors.Join(ErrApprovalChanged, err)
		}
		if decision.RunID != actual.RunID || decision.Attempt != actual.Attempt ||
			decision.CallID != actual.CallID || decision.Digest != actual.Digest {
			return RunResult{}, ErrApprovalChanged
		}
		used := false
		core.Approve = func(_ context.Context, call ToolCall, prepared PreparedTool) (bool, error) {
			if used {
				return false, errAwaitingApproval
			}
			actual := confirmationFor(runID, decision.Attempt, call, prepared)
			if decision.CallID != actual.CallID || decision.Digest != actual.Digest {
				return false, ErrApprovalChanged
			}
			used = true
			return approved, nil
		}
		if parent, err := core.Store.controlledParent(ctx, runID); err != nil {
			return RunResult{}, err
		} else if parent == runID {
			return tasks.continueComposition(ctx, core, run, nil)
		}
		return core.Continue(ctx, runID)
	})
}

// ConfirmControl applies or rejects a control proposal through the same
// pending tool Attempt used by ordinary approvals. The capability and code
// are supplied by the interactive GUI card or CLI prompt; the model never
// receives either value.
func (tasks *TaskService) ConfirmControl(ctx context.Context, runID string, decision Confirmation, auth ControlAuthorization, approved bool) (RunResult, error) {
	return tasks.execute(ctx, func(ctx context.Context, core *Service) (RunResult, error) {
		run, err := core.Store.GetRun(ctx, runID)
		if err != nil {
			return RunResult{}, err
		}
		actual, err := previewConfirmation(ctx, core, runID)
		if err != nil {
			return RunResult{}, errors.Join(ErrApprovalChanged, err)
		}
		if actual.ControlOperationID == "" || decision.ControlOperationID != actual.ControlOperationID ||
			decision.RunID != actual.RunID || decision.Attempt != actual.Attempt ||
			decision.CallID != actual.CallID || decision.Digest != actual.Digest {
			return RunResult{}, ErrApprovalChanged
		}
		control, err := core.controlService()
		if err != nil {
			return RunResult{}, err
		}
		auth.ConversationID = run.ConversationID
		var result ControlResult
		if approved {
			result, err = control.confirmRunProposal(ctx, actual.ControlOperationID, auth)
		} else {
			result, err = control.RejectControlChange(ctx, actual.ControlOperationID, auth)
		}
		if err != nil {
			return RunResult{Run: run}, err
		}
		attempt, err := core.Store.GetAttempt(ctx, actual.Attempt)
		if err != nil {
			return RunResult{}, err
		}
		content := "control proposal rejected; no setting was changed"
		if result.Applied {
			content = "control change applied; it takes effect in the next Run"
		}
		toolResult := ToolResult{Content: content, IsError: !result.Applied}
		if err := core.Store.settleControlProposal(ctx, run, attempt, toolResult, result.Applied, "control_change_applied_next_run"); err != nil {
			return RunResult{}, err
		}
		if result.Applied {
			updated, loadErr := core.Store.GetRun(ctx, runID)
			if loadErr != nil {
				return RunResult{}, loadErr
			}
			evidence, evidenceErr := core.Store.Evidence(ctx, runID)
			return RunResult{Run: updated, Answer: updated.Output, Evidence: evidence}, evidenceErr
		}
		return core.Continue(ctx, runID)
	})
}

func (tasks *TaskService) Inspect(ctx context.Context, runID string) (Run, []Attempt, Evidence, error) {
	if tasks == nil || tasks.core == nil {
		return Run{}, nil, Evidence{}, errors.New("harness: task service is not configured")
	}
	run, err := tasks.core.Store.GetRun(ctx, runID)
	if err != nil {
		return Run{}, nil, Evidence{}, err
	}
	attempts, err := tasks.core.Store.ListAttempts(ctx, runID)
	if err != nil {
		return Run{}, nil, Evidence{}, err
	}
	evidence, err := tasks.core.Store.Evidence(ctx, runID)
	return run, attempts, evidence, err
}

func (tasks *TaskService) Abandon(ctx context.Context, runID string) error {
	if tasks == nil || tasks.core == nil {
		return errors.New("harness: task service is not configured")
	}
	if err := tasks.core.Store.Abandon(ctx, runID); err != nil {
		return err
	}
	// A control proposal waits on its Run's Attempt. Once the Run is ended
	// nothing can resume it, so the card and CLI prompt must not apply it.
	control, err := tasks.core.controlService()
	if err != nil {
		return nil
	}
	attempts, err := tasks.core.Store.ListAttempts(ctx, runID)
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if record, ok := control.operationForAttempt(attempt.ID); ok {
			if err := control.withdrawProposal(record.Operation.ID, "run_abandoned"); err != nil {
				return fmt.Errorf("harness: run ended but its control proposal was not withdrawn: %w", err)
			}
		}
	}
	return nil
}

func (tasks *TaskService) Conversations(ctx context.Context) ([]Conversation, error) {
	if tasks == nil || tasks.core == nil {
		return nil, errors.New("harness: task service is not configured")
	}
	return tasks.core.Store.ListConversations(ctx)
}

func (tasks *TaskService) Messages(ctx context.Context, conversationID string) ([]Message, error) {
	if tasks == nil || tasks.core == nil {
		return nil, errors.New("harness: task service is not configured")
	}
	return tasks.core.Store.ListMessages(ctx, conversationID)
}

func (tasks *TaskService) NewConversation(ctx context.Context, title string) (Conversation, error) {
	if tasks == nil || tasks.core == nil {
		return Conversation{}, errors.New("harness: task service is not configured")
	}
	return tasks.core.Store.CreateConversation(ctx, title, tasks.core.currentConfig().Workspace)
}

func (tasks *TaskService) RenameConversation(ctx context.Context, conversationID, title string) error {
	if tasks == nil || tasks.core == nil {
		return errors.New("harness: task service is not configured")
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return errors.New("harness: conversation workspace does not match selected workspace")
	}
	return tasks.core.Store.RenameConversation(ctx, conversationID, title)
}

func (tasks *TaskService) PinConversation(ctx context.Context, conversationID string, pinned bool) error {
	if tasks == nil || tasks.core == nil {
		return errors.New("harness: task service is not configured")
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conversation.Workspace != tasks.Config().Workspace {
		return errors.New("harness: conversation workspace does not match selected workspace")
	}
	return tasks.core.Store.PinConversation(ctx, conversationID, pinned)
}

func (tasks *TaskService) Latest(ctx context.Context, conversationID string) (Run, error) {
	if tasks == nil || tasks.core == nil {
		return Run{}, errors.New("harness: task service is not configured")
	}
	return tasks.core.Store.GetLatestRun(ctx, conversationID)
}

func (tasks *TaskService) RequestStatus(ctx context.Context, conversationID, requestKey string) (Run, bool, error) {
	if tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return Run{}, false, errors.New("harness: task service is not configured")
	}
	run, err := tasks.core.Store.GetRunByRequest(ctx, conversationID, requestKey)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	return run, true, nil
}

// Template is an explicitly selected ordinary workspace file, not a package.
func (tasks *TaskService) Template(ctx context.Context, path, prompt string) (string, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Tools == nil {
		return "", errors.New("harness: task service is not configured")
	}
	raw, _ := json.Marshal(map[string]any{"path": path, "max_bytes": 32768})
	prepared, err := tasks.core.Tools.Prepare(ctx, ToolCall{Name: "file_read", Arguments: raw})
	if err != nil {
		return "", err
	}
	if prepared.Effect != EffectRead || prepared.Execute == nil {
		return "", errors.New("harness: templates require a read-only file adapter")
	}
	result, err := prepared.Execute(ctx)
	if err != nil {
		return "", err
	}
	var file struct {
		Path      string `json:"path"`
		SHA256    string `json:"sha256"`
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	}
	if result.IsError || json.Unmarshal([]byte(result.Content), &file) != nil || file.Truncated {
		return "", errors.New("harness: template must be a complete UTF-8 file of at most 32 KiB")
	}
	combined := "Explicit user-selected task template: " + file.Path + "\nSHA-256: " + file.SHA256 + "\n" + file.Content + "\n\nTask input:\n" + prompt
	if len(combined) > tasks.core.currentConfig().MaxContextBytes {
		return "", ErrContextLimit
	}
	return combined, nil
}
func (tasks *TaskService) ExpandReferences(ctx context.Context, prompt string, paths []string) (string, error) {
	if tasks == nil || tasks.core == nil {
		return "", errors.New("harness: task service is not configured")
	}
	return expandFileReferences(ctx, tasks.core, prompt, paths)
}

func expandFileReferences(ctx context.Context, core *Service, prompt string, paths []string) (string, error) {
	if ctx == nil || core == nil {
		return "", errors.New("harness: file references require a service and context")
	}
	if len(paths) > 16 {
		return "", errors.New("harness: at most 16 explicit file references")
	}
	if core.Tools == nil {
		return "", errors.New("harness: file references require a file_read adapter")
	}
	var combined strings.Builder
	combined.WriteString(prompt)
	for _, name := range paths {
		args, _ := json.Marshal(map[string]any{"path": name, "max_bytes": 32768})
		prepared, err := core.Tools.Prepare(ctx, ToolCall{Name: "file_read", Arguments: args})
		if err != nil {
			return "", err
		}
		if prepared.Effect != EffectRead || prepared.Execute == nil {
			return "", errors.New("harness: file references require a read-only file adapter")
		}
		result, err := prepared.Execute(ctx)
		if err != nil {
			return "", err
		}
		if result.IsError {
			return "", errors.New("harness: explicit file reference could not be read")
		}
		combined.WriteString("\n\nExplicit file reference (untrusted source data; page and SHA-256 shown):\n")
		combined.WriteString(result.Content)
		if combined.Len() > core.Config.MaxContextBytes {
			return "", ErrContextLimit
		}
	}
	return combined.String(), nil
}
