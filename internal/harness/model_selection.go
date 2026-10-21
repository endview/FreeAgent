package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

var (
	ErrModelBoundary      = errors.New("harness: finish the saved model/tool group before switching this run")
	ErrModelCapability    = errors.New("harness: selected model cannot accept the required request")
	ErrAutoDisabled       = errors.New("harness: Auto model selection is disabled")
	ErrSnapshotIncomplete = errors.New("harness: run snapshot is incomplete for this version")
)

type ModelBinding struct {
	ID       string      `json:"id"`
	Revision string      `json:"revision"`
	Config   ModelConfig `json:"config"`
	// BaseURL is the key profile's address when the run was bound. A keyed
	// run never dispatches to a different address.
	BaseURL string `json:"base_url,omitempty"`
}

// Model and limit changes remain bounded data in the existing run snapshot.
// The first binding is immutable; attempts record the actual dispatched binding.
type runSnapshot struct {
	Config           Config
	Tools            []ToolDefinition
	InstructionsHash string
	Bindings         []ModelBinding
	Limits           *ExecutionLimits     `json:",omitempty"`
	Auto             *autoRunPolicy       `json:",omitempty"`
	Routing          []RoutingDecision    `json:",omitempty"`
	Memory           *memoryRunPolicy     `json:",omitempty"`
	Permission       *PermissionSelection `json:",omitempty"`
	Capabilities     *CapabilitySelection `json:",omitempty"`
	ToolGeneration   string               `json:"tool_generation"`
	EnabledTools     BuiltinToolSettings  `json:"enabled_tools"`
	CommandPolicy    CommandConfig        `json:"command_policy"`
	HTTPPolicy       HTTPConfig           `json:"http_policy"`
	FilePolicy       FileAccessPolicy     `json:"file_policy"`
	ExecutionProfile ExecutionProfile     `json:"execution_profile"`
}

type ExecutionLimits struct {
	MaxSteps        int `json:"max_steps"`
	MaxMessages     int `json:"max_messages"`
	MaxContextBytes int `json:"max_context_bytes"`
	MaxOutputTokens int `json:"max_output_tokens"`
	MaxRunSeconds   int `json:"max_run_seconds"`
	MaxToolResult   int `json:"max_tool_result_bytes"`
}

func configLimits(config Config) ExecutionLimits {
	return ExecutionLimits{config.MaxSteps, config.MaxMessages, config.MaxContextBytes,
		config.MaxOutputTokens, config.MaxRunSeconds, config.MaxToolResult}
}

func (limits ExecutionLimits) validate() error {
	if limits.MaxSteps < 1 || limits.MaxSteps > 100 || limits.MaxMessages < 1 || limits.MaxMessages > 1000 ||
		limits.MaxContextBytes < 4096 || limits.MaxContextBytes > 16<<20 ||
		limits.MaxOutputTokens < 1 || limits.MaxOutputTokens > 32768 ||
		limits.MaxRunSeconds < 1 || limits.MaxRunSeconds > 3600 ||
		limits.MaxToolResult < 1 || limits.MaxToolResult > 16<<20 {
		return errors.New("harness: execution limits must be finite and within supported bounds")
	}
	return nil
}

func (service *Service) limits() ExecutionLimits {
	if service.runLimits != nil {
		return *service.runLimits
	}
	return configLimits(service.Config)
}

func modelBinding(config Config, id string) (ModelBinding, error) {
	if id == "" {
		id = config.DefaultModel
	}
	model, exists := config.Models[id]
	if !exists || !validModelID(id) {
		return ModelBinding{}, fmt.Errorf("harness: model %q is not configured", id)
	}
	raw, err := json.Marshal(model)
	if err != nil {
		return ModelBinding{}, err
	}
	return ModelBinding{ID: id, Revision: contentHash(raw), Config: model}, model.Validate()
}

// newBinding is modelBinding plus the key profile's current base URL. It reads
// only the profile metadata; the key itself is resolved at dispatch.
func (service *Service) newBinding(config Config, id string) (ModelBinding, error) {
	binding, err := modelBinding(config, id)
	if err != nil || binding.Config.Key == "" {
		return binding, err
	}
	if service.Keys == nil {
		return ModelBinding{}, fmt.Errorf("%w (model %q uses key %s)", ErrKeyNotFound, binding.ID, binding.Config.Key)
	}
	view, err := service.Keys.Find(binding.Config.Key)
	if errors.Is(err, ErrKeyNotFound) {
		return ModelBinding{}, fmt.Errorf("%w (model %q uses key %s)", ErrKeyNotFound, binding.ID, binding.Config.Key)
	}
	if err != nil {
		return ModelBinding{}, err
	}
	binding.BaseURL = view.BaseURL
	return binding, nil
}

func configuredSelection(config Config, id string) (string, error) {
	if id == "" {
		id = config.DefaultSelection
		if id == "" {
			id = config.DefaultModel
		}
	}
	if id == "auto" {
		if !config.Auto.Enabled {
			return "", ErrAutoDisabled
		}
		return id, nil
	}
	if _, err := modelBinding(config, id); err != nil {
		return "", err
	}
	return id, nil
}

func (service *Service) binding() (ModelBinding, error) {
	if service.callOverride != nil {
		return *service.callOverride, nil
	}
	if len(service.bindings) != 0 {
		return service.bindings[len(service.bindings)-1], nil
	}
	return service.newBinding(service.Config, "")
}

func decodeRunSnapshot(raw string) (runSnapshot, error) {
	var snapshot runSnapshot
	var fields map[string]json.RawMessage
	if len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &fields) != nil ||
		fields == nil || fields["tool_generation"] == nil || fields["command_policy"] == nil || fields["file_policy"] == nil || fields["execution_profile"] == nil ||
		json.Unmarshal([]byte(raw), &snapshot) != nil || strings.TrimSpace(snapshot.ToolGeneration) == "" ||
		snapshot.Config.Version != ConfigVersion || len(snapshot.Bindings) < 1 || len(snapshot.Bindings) > 64 {
		return snapshot, ErrSnapshotIncomplete
	}
	if snapshot.EnabledTools == (BuiltinToolSettings{}) {
		snapshot.EnabledTools = defaultBuiltinToolSettings()
	}
	if err := validateSnapshotToolPolicy(ToolPolicy{EnabledTools: snapshot.EnabledTools, Generation: snapshot.ToolGeneration, Commands: snapshot.CommandPolicy, HTTP: snapshot.HTTPPolicy, File: snapshot.FilePolicy}); err != nil {
		return snapshot, errors.Join(ErrSnapshotIncomplete, err)
	}
	if err := snapshot.ExecutionProfile.Validate(); err != nil ||
		snapshot.ExecutionProfile.Generation != snapshot.ToolGeneration ||
		snapshot.ExecutionProfile.WorkspaceRoot != filepath.Clean(snapshot.Config.Workspace) ||
		snapshot.ExecutionProfile.CommandPolicy.Enabled != snapshot.CommandPolicy.Enabled ||
		snapshot.ExecutionProfile.CommandPolicy.Execution != effectiveCommandExecution(snapshot.CommandPolicy) ||
		!sameCommandRules(snapshot.ExecutionProfile.CommandPolicy.Rules, snapshot.CommandPolicy.Rules) ||
		snapshot.FilePolicy.Scope != snapshot.ExecutionProfile.FileScope ||
		!sameRootList(snapshot.FilePolicy.ComputerRoots, snapshot.ExecutionProfile.AdditionalRoots) ||
		!sameRootList(snapshot.FilePolicy.BlockedPaths, snapshot.ExecutionProfile.BlockedPaths) {
		return snapshot, ErrSnapshotIncomplete
	}
	for _, binding := range snapshot.Bindings {
		if err := validateBinding(binding); err != nil {
			return snapshot, ErrStoreFormat
		}
	}
	if snapshot.Limits != nil && snapshot.Limits.validate() != nil {
		return snapshot, ErrStoreFormat
	}
	if snapshot.Permission != nil && snapshot.Permission.validate() != nil {
		return snapshot, ErrStoreFormat
	}
	if snapshot.Memory != nil && snapshot.Memory.validate(snapshot.Config.Workspace) != nil {
		return snapshot, ErrStoreFormat
	}
	if snapshot.Capabilities != nil && snapshot.Capabilities.validate(snapshot.Config) != nil {
		return snapshot, ErrStoreFormat
	}
	if snapshot.Auto != nil {
		if err := snapshot.Auto.validate(); err != nil {
			return snapshot, err
		}
	}
	if len(snapshot.Routing) > 64 {
		return snapshot, ErrStoreFormat
	}
	for _, decision := range snapshot.Routing {
		if err := decision.validate(); err != nil {
			return snapshot, err
		}
	}
	if len(snapshot.Routing) != 0 {
		bindingIndex := 0
		for index, decision := range snapshot.Routing {
			if index > 0 {
				previous := snapshot.Routing[index-1]
				if decision.Assessments < previous.Assessments || decision.Upgrades < previous.Upgrades ||
					decision.Upgrades > previous.Upgrades+1 {
					return snapshot, ErrStoreFormat
				}
			}
			binding := snapshot.Bindings[bindingIndex]
			if decision.SelectedModel != binding.ID || decision.SelectedRevision != binding.Revision {
				bindingIndex++
				if index == 0 || bindingIndex >= len(snapshot.Bindings) {
					return snapshot, ErrStoreFormat
				}
				binding = snapshot.Bindings[bindingIndex]
				if decision.SelectedModel != binding.ID || decision.SelectedRevision != binding.Revision {
					return snapshot, ErrStoreFormat
				}
			}
		}
		if bindingIndex != len(snapshot.Bindings)-1 {
			return snapshot, ErrStoreFormat
		}
	}
	if snapshot.Auto != nil {
		if len(snapshot.Routing) == 0 {
			return snapshot, ErrStoreFormat
		}
		last := snapshot.Routing[len(snapshot.Routing)-1]
		authorized := slices.ContainsFunc(snapshot.Auto.Models, func(binding ModelBinding) bool {
			return binding.ID == last.SelectedModel && binding.Revision == last.SelectedRevision
		})
		if last.Mode != "auto" || !authorized ||
			last.Assessments > snapshot.Auto.MaxAssessments || last.Upgrades > snapshot.Auto.MaxUpgrades {
			return snapshot, ErrStoreFormat
		}
	}
	return snapshot, nil
}

func validateSnapshotToolPolicy(policy ToolPolicy) error {
	if strings.TrimSpace(policy.Generation) == "" {
		return errors.New("missing tool generation")
	}
	if err := validateCommandAllowlist(policy.Commands.AllowedExecutables); err != nil {
		return err
	}
	if policy.Commands.Execution != "" {
		if err := policy.Commands.Execution.Validate(); err != nil {
			return err
		}
	}
	if len(policy.Commands.Rules) > 128 {
		return errors.New("invalid command rules")
	}
	for _, rule := range policy.Commands.Rules {
		if err := rule.Validate(); err != nil {
			return err
		}
	}
	if err := policy.HTTP.Validate(); err != nil {
		return err
	}
	if policy.Commands.Enabled && (policy.Commands.TimeoutSecs < 1 || policy.Commands.TimeoutSecs > 600 ||
		policy.Commands.MaxOutput < 1024 || policy.Commands.MaxOutput > 1<<20) {
		return errors.New("invalid command policy")
	}
	if len(policy.File.ComputerRoots) > 8 {
		return errors.New("invalid computer file policy")
	}
	if len(policy.File.BlockedPaths) > 64 {
		return errors.New("invalid file blocked paths")
	}
	for _, root := range policy.File.ComputerRoots {
		if !filepath.IsAbs(root) || strings.ContainsAny(root, "\x00\r\n") || root != filepath.Clean(root) {
			return errors.New("invalid computer file root")
		}
	}
	for _, path := range policy.File.BlockedPaths {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") || path != filepath.Clean(path) {
			return errors.New("invalid file blocked path")
		}
	}
	if policy.File.Scope != "" {
		if err := policy.File.Scope.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func withModelFacts(run Run) Run {
	if snapshot, err := decodeRunSnapshot(run.ConfigJSON); err == nil {
		run.InitialModel = snapshot.Bindings[0].ID
		binding := snapshot.Bindings[len(snapshot.Bindings)-1]
		run.ModelID, run.ModelRevision = binding.ID, binding.Revision
		run.Routing = snapshot.Routing
		run.Permission = snapshot.Permission
		profile := snapshot.ExecutionProfile
		run.ExecutionProfile = &profile
	}
	return run
}

func (service *Service) forSelection(id string) (*Service, error) {
	binding, err := service.newBinding(service.Config, id)
	if err != nil {
		return nil, err
	}
	copy := *service
	copy.bindings, copy.runLimits, copy.routing, copy.autoPolicy =
		[]ModelBinding{binding}, nil, nil, nil
	return &copy, nil
}

func (service *Service) hasPendingToolApproval(runID string) bool {
	if service == nil || service.Store == nil || runID == "" {
		return false
	}
	attempts, err := service.Store.ListAttempts(context.Background(), runID)
	if err != nil {
		return false
	}
	for _, attempt := range attempts {
		if attempt.Kind == "tool" && attempt.Status == "pending" {
			return true
		}
	}
	return false
}

func (service *Service) forRun(run Run) (*Service, error) {
	snapshot, err := decodeRunSnapshot(run.ConfigJSON)
	if err != nil {
		return nil, err
	}
	copy := *service
	if run.Status == "waiting_confirmation" && service.hasPendingToolApproval(run.ID) {
		// The run owns its configuration snapshot. Rebuild the minimal model
		// registry from frozen bindings so future-only global changes do not
		// alter continuation identity or the exact snapshot comparison.
		copy.Config = snapshot.Config
		copy.Config.Models = make(map[string]ModelConfig, len(snapshot.Bindings))
		for _, binding := range snapshot.Bindings {
			copy.Config.Models[binding.ID] = binding.Config
		}
		if len(snapshot.Bindings) != 0 {
			copy.Config.DefaultModel = snapshot.Bindings[len(snapshot.Bindings)-1].ID
			copy.Config.DefaultSelection = copy.Config.DefaultModel
		}
	}
	copy.controlToolsDisabled = true
	for _, definition := range snapshot.Tools {
		if isControlToolName(definition.Name) {
			copy.controlToolsDisabled = false
			break
		}
	}
	copy.bindings, copy.runLimits, copy.autoPolicy, copy.routing =
		snapshot.Bindings, snapshot.Limits, snapshot.Auto, snapshot.Routing
	copy.memory = snapshot.Memory
	copy.permission = snapshot.Permission
	copy.capabilities = snapshot.Capabilities
	if snapshot.FilePolicy.ComputerFiles {
		roots, rootErr := normalizeComputerRoots(context.Background(), copy.Store.DataDir(), snapshot.FilePolicy.ComputerRoots)
		if rootErr != nil {
			return nil, errors.Join(ErrSnapshotIncomplete, rootErr)
		}
		snapshot.FilePolicy.ComputerRoots = roots
	}
	if snapshot.Permission != nil && snapshot.Permission.ComputerFiles {
		if !snapshot.FilePolicy.ComputerFiles {
			return nil, ErrSnapshotIncomplete
		}
		roots, rootErr := normalizeComputerRoots(context.Background(), copy.Store.DataDir(), snapshot.Permission.ComputerRoots)
		if rootErr != nil || !sameRootSet(roots, snapshot.FilePolicy.ComputerRoots) {
			return nil, errors.Join(ErrSnapshotIncomplete, rootErr)
		}
	}
	copy.toolPolicy = ToolPolicy{EnabledTools: snapshot.EnabledTools, Generation: snapshot.ToolGeneration, Commands: snapshot.CommandPolicy, File: snapshot.FilePolicy,
		HTTP: snapshot.HTTPPolicy, Profile: snapshot.ExecutionProfile}
	if copy.toolManager != nil {
		copy.toolView = copy.toolManager.build(copy.toolPolicy)
		copy.Tools = copy.toolView
	}
	if err := copy.loadSkills(context.Background(), run.Prompt); err != nil {
		return nil, err
	}
	actual, err := copy.snapshot()
	if err != nil || actual != run.ConfigJSON {
		return nil, ErrConfigChanged
	}
	return &copy, nil
}

func (service *Service) requestLimits() (ExecutionLimits, ModelBinding, error) {
	limits := service.limits()
	binding, err := service.binding()
	if err != nil {
		return limits, binding, err
	}
	caps := binding.Config.Capabilities
	if caps.ContextBytes > 0 {
		limits.MaxContextBytes = min(limits.MaxContextBytes, caps.ContextBytes)
	}
	if caps.OutputTokens > 0 {
		limits.MaxOutputTokens = min(limits.MaxOutputTokens, caps.OutputTokens)
	}
	return limits, binding, nil
}

func (service *Service) modelClient() (ModelClient, error) {
	binding, err := service.binding()
	if err != nil {
		return nil, err
	}
	client := service.Model
	if service.ModelFor != nil {
		client, err = service.ModelFor(binding.Config)
		if err != nil {
			if binding.BaseURL != "" && errors.Is(err, ErrKeyNotFound) {
				return nil, fmt.Errorf("%w (the key profile of model %q was removed)", ErrConfigChanged, binding.ID)
			}
			return nil, err
		}
		if client == nil {
			return nil, errors.New("harness: model factory returned no adapter")
		}
	} else if client == nil {
		return nil, errors.New("harness: no model adapter")
	}
	// A changed key alone dispatches; a changed address never does.
	if target, ok := client.(interface{ BaseURL() string }); ok && binding.BaseURL != "" && target.BaseURL() != binding.BaseURL {
		return nil, fmt.Errorf("%w (the key profile of model %q now points to another address)", ErrConfigChanged, binding.ID)
	}
	return client, nil
}

// SelectModel affects subsequent tasks only, including when a current call is
// streaming or a saved operation awaits approval. It never continues a run.
func (tasks *TaskService) SelectModel(ctx context.Context, conversationID, modelID string) error {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return errors.New("harness: task service and context are required")
	}
	tasks.mu.Lock()
	config, store := tasks.core.currentConfig(), tasks.core.Store
	tasks.mu.Unlock()
	if _, err := configuredSelection(config, modelID); err != nil {
		return err
	}
	conversation, err := store.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conversation.Workspace != config.Workspace {
		return errors.New("harness: conversation workspace does not match")
	}
	_, err = store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "chat/model/"+conversationID, modelID)
	return err
}

func (tasks *TaskService) SelectedModel(ctx context.Context, conversationID string) (string, error) {
	if tasks == nil || tasks.core == nil {
		return "", errors.New("harness: task service is required")
	}
	conversation, err := tasks.core.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return "", err
	}
	id := conversation.ModelID
	if id == "" {
		id = tasks.Config().DefaultSelection
		if id == "" {
			id = tasks.Config().DefaultModel
		}
	}
	return id, nil
}

func (tasks *TaskService) editRun(ctx context.Context, runID string, edit func(*Service, Run) (string, error)) error {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return errors.New("harness: task service and context are required")
	}
	tasks.mu.Lock()
	core := tasks.core.runCopy()
	active := tasks.cancel != nil
	tasks.mu.Unlock()
	if active || !core.Store.runMu.TryLock() {
		return ErrActiveRun
	}
	defer core.Store.runMu.Unlock()
	if parent, err := core.Store.controlledParent(ctx, runID); err != nil || parent != "" {
		return errors.Join(ErrControlledRun, err)
	}
	run, err := core.Store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == "completed" || run.Status == "failed" || run.Status == "abandoned" || run.ActiveSince != "" {
		return errors.New("harness: only an inactive unfinished run can be adjusted")
	}
	bound, err := core.forRun(run)
	if err != nil {
		return err
	}
	if bound.Refresh != nil {
		current, err := bound.Refresh()
		if err != nil {
			return ErrConfigChanged
		}
		snapshot, err := bound.snapshotConfig(current)
		if err != nil || snapshot != run.ConfigJSON {
			return ErrConfigChanged
		}
		// An explicit safe-boundary switch may adopt a new model revision,
		// but neither kind of edit may change the saved execution authority.
		bound.Config = current
	}
	snapshot, err := edit(bound, run)
	if err != nil {
		return err
	}
	// Both the binding and limits change are atomic; consumption is untouched.
	_, err = core.Store.db.ExecContext(ctx, `UPDATE runs SET config_json=?,updated_at=? WHERE id=?`, snapshot, nowText(), runID)
	return err
}

// SwitchModel is allowed only between complete model/tool groups. An active
// call retains its old model, and UNKNOWN is never made replayable.
func (tasks *TaskService) SwitchModel(ctx context.Context, runID, modelID string) error {
	return tasks.editRun(ctx, runID, func(core *Service, run Run) (string, error) {
		if run.Status == "unknown" {
			return "", ErrUnknownResult
		}
		attempts, err := core.Store.ListAttempts(ctx, runID)
		if err != nil {
			return "", err
		}
		for _, attempt := range attempts {
			if attempt.Status == "unknown" || attempt.Status == "dispatched" {
				return "", ErrUnknownResult
			}
			if attempt.Status == "pending" {
				return "", ErrModelBoundary
			}
		}
		binding, err := core.newBinding(core.Config, modelID)
		if err != nil {
			return "", err
		}
		last := core.bindings[len(core.bindings)-1]
		// Consecutive bindings are told apart by id and revision only.
		if binding.ID == last.ID && binding.Revision == last.Revision && binding.BaseURL != last.BaseURL {
			return "", fmt.Errorf("%w (the key profile of model %q now points to another address)", ErrConfigChanged, binding.ID)
		}
		if binding == last && core.autoPolicy == nil {
			return run.ConfigJSON, nil
		}
		if len(core.bindings) >= 64 || len(core.routing) >= 64 {
			return "", errors.New("harness: model binding change limit reached")
		}
		if binding != last {
			core.bindings = append(slices.Clone(core.bindings), binding)
		}
		core.autoPolicy = nil
		decision := routingDecision(binding, "manual",
			"user explicitly switched at a complete saved call boundary; Auto was disabled for this Run")
		if len(core.routing) != 0 {
			last := core.routing[len(core.routing)-1]
			decision.Assessments, decision.Upgrades = last.Assessments, last.Upgrades
		}
		for _, attempt := range attempts {
			if attempt.Kind != "model" {
				continue
			}
			var request ModelRequest
			if json.Unmarshal([]byte(attempt.RequestJSON), &request) != nil {
				return "", ErrStoreFormat
			}
			if request.Purpose == "assessment" {
				decision.Assessments = 1
			}
		}
		core.routing = append(slices.Clone(core.routing), decision)
		if _, err := core.buildContext(ctx, run); err != nil {
			return "", err
		}
		return core.snapshot()
	})
}

func (tasks *TaskService) Limits(ctx context.Context, runID string) (ExecutionLimits, error) {
	if tasks == nil || tasks.core == nil {
		return ExecutionLimits{}, errors.New("harness: task service is required")
	}
	run, err := tasks.core.Store.GetRun(ctx, runID)
	if err != nil {
		return ExecutionLimits{}, err
	}
	core, err := tasks.core.forRun(run)
	if err != nil {
		return ExecutionLimits{}, err
	}
	return core.limits(), nil
}

// IncreaseLimits does not reset steps, elapsed time, results, reservations or
// UNKNOWN. The caller still needs an explicit Continue operation.
func (tasks *TaskService) IncreaseLimits(ctx context.Context, runID string, limits ExecutionLimits) error {
	if err := limits.validate(); err != nil {
		return err
	}
	return tasks.editRun(ctx, runID, func(core *Service, run Run) (string, error) {
		old := core.limits()
		if limits.MaxSteps < old.MaxSteps || limits.MaxMessages < old.MaxMessages ||
			limits.MaxContextBytes < old.MaxContextBytes || limits.MaxOutputTokens < old.MaxOutputTokens ||
			limits.MaxRunSeconds < old.MaxRunSeconds || limits.MaxToolResult < old.MaxToolResult {
			return "", errors.New("harness: limits can only increase without resetting consumption")
		}
		core.runLimits = &limits
		return core.snapshot()
	})
}

func completeMessageGroups(messages []ModelMessage) error {
	pending := map[string]bool{}
	for _, message := range messages {
		if message.Role == "tool" {
			if !pending[message.ToolCallID] {
				return ErrModelBoundary
			}
			delete(pending, message.ToolCallID)
			continue
		}
		if len(pending) != 0 {
			return ErrModelBoundary
		}
		if len(message.ProviderItems) == 0 {
			for _, call := range message.ToolCalls {
				if pending[call.ID] {
					return ErrModelBoundary
				}
				pending[call.ID] = true
			}
		} else {
			for _, raw := range message.ProviderItems {
				var item struct {
					Type   string `json:"type"`
					CallID string `json:"call_id"`
				}
				if json.Unmarshal(raw, &item) != nil {
					return ErrModelBoundary
				}
				if item.Type != "function_call" {
					continue
				}
				if item.CallID == "" || pending[item.CallID] {
					return ErrModelBoundary
				}
				pending[item.CallID] = true
			}
		}
	}
	if len(pending) != 0 {
		return ErrModelBoundary
	}
	return nil
}

func validateBinding(binding ModelBinding) error {
	body, err := json.Marshal(binding.Config)
	if err != nil || !validModelID(binding.ID) || binding.Revision != contentHash(body) {
		return ErrStoreFormat
	}
	if binding.Config.Key == "" {
		if binding.BaseURL != "" {
			return ErrStoreFormat
		}
	} else if base, err := NormalizeBaseURL(binding.BaseURL); err != nil || base != binding.BaseURL {
		return ErrStoreFormat
	}
	return binding.Config.Validate()
}
