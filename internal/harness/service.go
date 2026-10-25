package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

var ErrContextLimit = errors.New("harness: current task exceeds context budget")
var ErrConfigChanged = errors.New("harness: run configuration changed; no call was dispatched")
var ErrTimeLimit = errors.New("harness: run time limit reached")
var errAwaitingApproval = errors.New("harness: waiting for user confirmation")
var errControlChangeApplied = errors.New("harness: control change applied for the next Run")

// StopReasonControlProposal marks a Run waiting on a model-proposed setting
// change, which needs the user's confirmation code as well as approval.
const StopReasonControlProposal = "control proposal requires user confirmation"

type ApprovalFunc func(context.Context, ToolCall, PreparedTool) (bool, error)
type EventFunc func(Event)

type Event struct {
	Seq            uint64         `json:"seq,omitempty"`
	Generation     string         `json:"generation,omitempty"`
	Kind           string         `json:"kind"`
	Message        string         `json:"message,omitempty"`
	Activity       map[string]any `json:"activity,omitempty"`
	Tool           string         `json:"tool,omitempty"`
	Preview        string         `json:"preview,omitempty"`
	Sources        []MemoryUse    `json:"sources,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	RunID          string         `json:"run_id,omitempty"`
	AttemptID      string         `json:"attempt_id,omitempty"`
	Purpose        string         `json:"purpose,omitempty"`
}

type Service struct {
	Store       *Store
	Model       ModelClient
	ModelFor    ModelFactory
	Tools       ToolRunner
	toolManager *ToolRuntimeManager
	toolView    *RuntimeToolView
	toolPolicy  ToolPolicy
	Config      Config
	Approve     ApprovalFunc
	Emit        EventFunc
	Refresh     func() (Config, error)
	// Keys supplies the base URL recorded in each keyed model binding.
	Keys *KeyStore
	// A composition may hold the store's exclusive execution lease while
	// scheduling bounded calls. The normal single-task path never bypasses it.
	ownsLease         bool
	guard             callGuard
	bindings          []ModelBinding
	runLimits         *ExecutionLimits
	selection         string
	autoPolicy        *autoRunPolicy
	routing           []RoutingDecision
	callOverride      *ModelBinding
	activity          *executionActivity
	memory            *memoryRunPolicy
	permission        *PermissionSelection
	capabilities      *CapabilitySelection
	skills            *skillRuntime
	extensions        *extensionHost
	extensionPrepared bool
	extensionBlocks   []ExtensionBlock
	extensionUses     []ExtensionUse
	extensionToolDefs []extensionRuntimeTool
	controlMu         *sync.Mutex
	// configMu guards Config replacement on a core shared by several
	// TaskService values; copies taken for a Run keep their own Config.
	configMu                *sync.RWMutex
	control                 *ControlService
	controlErr              error
	controlToolsDisabled    bool
	controlToolsActive      bool
	controlDecision         *ControlAuthorization
	controlApproved         bool
	allowFrozenContinuation bool
	compactionFailed        bool
	contextToolsDisabled    bool
}

func (service *Service) bindCurrentToolView() {
	if service == nil || service.toolManager == nil || service.toolView != nil {
		return
	}
	if control, err := service.controlService(); err == nil {
		service.toolManager.SetControlGate(&control.gate)
	}
	current := service.toolManager.Acquire().Policy()
	current = service.permissionScopedPolicy(current)
	service.toolView = service.toolManager.build(current)
	service.toolPolicy = service.toolView.Policy()
	service.Tools = service.toolView
}

func (service *Service) permissionScopedPolicy(policy ToolPolicy) ToolPolicy {
	if service == nil {
		return policy
	}
	selection := PermissionSelection{Mode: effectiveExecutionSettings(service.Config).PermissionMode, Source: "default",
		ComputerFiles: service.Config.ComputerFiles, ComputerRoots: append([]string(nil), service.Config.ComputerRoots...)}
	if service.permission != nil {
		selection = *service.permission
	}
	profile, err := buildExecutionProfile(service.Config, selection)
	if err != nil {
		return policy
	}
	return toolPolicyForProfile(service.Config, profile)
}

func (service *Service) activeToolPolicy() ToolPolicy {
	if service.toolView != nil {
		return service.toolView.Policy()
	}
	if service.toolPolicy.Profile.Generation != "" {
		return service.toolPolicy
	}
	if service.toolManager != nil {
		return service.permissionScopedPolicy(service.toolManager.Acquire().Policy())
	}
	selection := PermissionSelection{Mode: effectiveExecutionSettings(service.Config).PermissionMode, Source: "default",
		ComputerFiles: service.Config.ComputerFiles, ComputerRoots: append([]string(nil), service.Config.ComputerRoots...)}
	if service.permission != nil {
		selection = *service.permission
	}
	profile, err := buildExecutionProfile(service.Config, selection)
	if err != nil {
		return ToolPolicy{EnabledTools: effectiveBuiltinToolSettings(service.Config), Commands: service.Config.Commands, HTTP: service.Config.HTTP,
			File:       FileAccessPolicy{ComputerFiles: service.Config.ComputerFiles, ComputerRoots: append([]string(nil), service.Config.ComputerRoots...)},
			Generation: effectiveToolGeneration(service.Config)}
	}
	return toolPolicyForProfile(service.Config, profile)
}

func (service *Service) globalToolPolicy() ToolPolicy {
	if service == nil {
		return ToolPolicy{}
	}
	if service.toolManager != nil {
		return service.toolManager.Acquire().Policy()
	}
	config := service.currentConfig()
	profile, err := executionProfileForConfig(config)
	if err == nil {
		return toolPolicyForProfile(config, profile)
	}
	return ToolPolicy{EnabledTools: effectiveBuiltinToolSettings(config), Commands: config.Commands, HTTP: config.HTTP,
		File: FileAccessPolicy{ComputerFiles: config.ComputerFiles,
			ComputerRoots: append([]string(nil), config.ComputerRoots...)},
		Generation: effectiveToolGeneration(config)}
}

// Writers always install a fresh Config value, so a returned copy stays
// valid after the lock is released.
func (service *Service) currentConfig() Config {
	if service.configMu == nil {
		return service.Config
	}
	service.configMu.RLock()
	defer service.configMu.RUnlock()
	return service.Config
}

func (service *Service) setConfig(config Config) {
	if service.configMu == nil {
		service.Config = config
		return
	}
	service.configMu.Lock()
	service.Config = config
	service.configMu.Unlock()
}

// runCopy returns the per-Run copy of a shared core.
func (service *Service) runCopy() Service {
	if service.configMu != nil {
		service.configMu.RLock()
		defer service.configMu.RUnlock()
	}
	return *service
}

type RunResult struct {
	Run       Run      `json:"run"`
	Answer    string   `json:"answer,omitempty"`
	Duplicate bool     `json:"duplicate"`
	Evidence  Evidence `json:"evidence"`
}

func (service *Service) snapshot() (string, error) {
	return service.snapshotConfig(service.Config)
}

func (service *Service) snapshotConfig(config Config) (string, error) {
	bindings := service.bindings
	if len(bindings) == 0 {
		binding, err := service.newBinding(config, "")
		if err != nil {
			return "", err
		}
		bindings = []ModelBinding{binding}
	}
	permission := service.permission
	if permission == nil {
		mode := effectiveExecutionSettings(config).PermissionMode
		if mode == "" {
			mode = PermissionAsk
		}
		defaultPermission := PermissionSelection{Mode: mode, Source: "default"}
		permission = &defaultPermission
	}
	capabilities := service.capabilities
	if capabilities == nil {
		selected := defaultCapabilities(config)
		capabilities = &selected
	}
	policy := service.activeToolPolicy()
	profile := policy.Profile
	if profile.Generation == "" {
		var profileErr error
		profile, profileErr = buildExecutionProfile(config, *permission)
		if profileErr != nil {
			return "", profileErr
		}
		policy = toolPolicyForProfile(config, profile)
	}
	if err := profile.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(runSnapshot{
		Config: safetyConfig(config), Tools: service.snapshotToolDefinitions(),
		InstructionsHash: contentHash([]byte(config.instructionText)),
		Bindings:         bindings, Limits: service.runLimits,
		Auto: service.autoPolicy, Routing: service.routing,
		Memory: service.memory, Permission: permission, Capabilities: capabilities,
		ToolGeneration: policy.Generation, EnabledTools: policy.EnabledTools, CommandPolicy: policy.Commands, HTTPPolicy: policy.HTTP, FilePolicy: policy.File,
		ExecutionProfile: profile,
	})
	return string(raw), err
}

// snapshotToolDefinitions contains only the deterministic built-in surface.
// Extension, skill and memory tools are discovered from the frozen capability
// selection when the turn is prepared; persisting their process-discovered
// definitions would make a valid turn fail its own dispatch-config check.
func (service *Service) snapshotToolDefinitions() []ToolDefinition {
	base := service.Tools
	if service.capabilities != nil {
		switch runtime := base.(type) {
		case *ToolRuntimeManager:
			base = runtime.Acquire().ForCapabilities(*service.capabilities)
		case *RuntimeToolView:
			base = runtime.ForCapabilities(*service.capabilities)
		case *RuntimeTools:
			base = runtime.ForCapabilities(*service.capabilities)
		}
	} else if manager, ok := base.(*ToolRuntimeManager); ok {
		base = manager.Acquire()
	}
	if base == nil {
		return nil
	}
	definitions := base.Definitions()
	if service.controlToolsEnabled() {
		definitions = append(definitions, (&controlTools{service: service, base: base}).controlDefinitions()...)
		if !service.contextToolsDisabled {
			definitions = append(definitions, contextToolDefinition())
		}
	}
	return definitions
}

func (service *Service) checkConfig(run Run) error {
	if service.allowFrozenContinuation {
		if service.controlToolsActive {
			control, err := service.controlService()
			if err != nil {
				return err
			}
			return control.CheckExternalChanges(context.Background())
		}
		return nil
	}
	return service.checkDispatchConfig(run, false)
}

func (service *Service) checkDispatchConfig(run Run, modelCall bool) error {
	if service.allowFrozenContinuation {
		if service.controlToolsActive {
			control, err := service.controlService()
			if err != nil {
				return err
			}
			return control.CheckExternalChanges(context.Background())
		}
		return nil
	}
	if service.Refresh == nil {
		if (modelCall || service.callOverride != nil) && service.autoPolicy != nil && !service.autoPolicy.authorized(service.Config) {
			return ErrAutoDisabled
		}
		return nil
	}
	current, err := service.Refresh()
	if err != nil {
		return ErrConfigChanged
	}
	binding, err := service.binding()
	if err != nil || current.Models[binding.ID] != binding.Config {
		return ErrConfigChanged
	}
	if (modelCall || service.callOverride != nil) && service.autoPolicy != nil && !service.autoPolicy.authorized(current) {
		return ErrAutoDisabled
	}
	snapshot, err := service.snapshotConfig(current)
	if err != nil || snapshot != run.ConfigJSON {
		return ErrConfigChanged
	}
	return nil
}

func (service *Service) validate(ctx context.Context) error {
	if ctx == nil || service == nil || service.Store == nil || (service.Model == nil && service.ModelFor == nil) || service.Tools == nil {
		return errors.New("harness: service and context are required")
	}
	if err := service.Config.Validate(); err != nil {
		return err
	}
	return ctx.Err()
}

func (service *Service) Run(ctx context.Context, conversationID, requestKey, prompt string) (RunResult, error) {
	if err := service.validate(ctx); err != nil {
		return RunResult{}, err
	}
	if service.controlToolsActive {
		control, err := service.controlService()
		if err != nil {
			return RunResult{}, err
		}
		if err := control.CheckExternalChanges(ctx); err != nil {
			return RunResult{}, err
		}
	}
	if !service.ownsLease {
		if !service.Store.runMu.TryLock() {
			return RunResult{}, ErrActiveRun
		}
		defer service.Store.runMu.Unlock()
	}
	if len(prompt) > service.Config.MaxContextBytes {
		return RunResult{}, ErrContextLimit
	}
	conversation, err := service.Store.GetConversation(ctx, conversationID)
	if err != nil {
		return RunResult{}, err
	}
	if filepath.Clean(conversation.Workspace) != filepath.Clean(service.Config.Workspace) {
		return RunResult{}, errors.New("harness: conversation workspace does not match selected workspace")
	}
	if repeated, exists, err := service.Store.repeatedRun(ctx, conversationID, requestKey, prompt); err != nil {
		return RunResult{}, err
	} else if exists {
		evidence, err := service.Store.Evidence(ctx, repeated.ID)
		return RunResult{Run: repeated, Answer: repeated.Output, Duplicate: true, Evidence: evidence}, err
	}
	if service.guard == nil {
		if err := service.Store.checkUncontrolled(ctx, conversationID); err != nil {
			return RunResult{}, err
		}
	}
	if err := service.selectMemory(ctx, conversationID, nil); err != nil {
		return RunResult{}, err
	}
	if service.capabilities == nil {
		selected := defaultCapabilities(service.Config)
		service.capabilities = &selected
	}
	modelID := conversation.ModelID
	if service.selection != "" {
		modelID = service.selection
	}
	bound, err := service.forTask(ctx, conversationID, modelID, prompt)
	if err != nil {
		return RunResult{}, err
	}
	service = bound
	service.bindCurrentToolView()
	if err := service.loadSkills(ctx, prompt); err != nil {
		return RunResult{}, err
	}
	snapshot, err := service.snapshot()
	if err != nil {
		return RunResult{}, err
	}
	if err := service.checkConfig(Run{ConfigJSON: snapshot}); err != nil {
		return RunResult{}, err
	}
	run, created, err := service.Store.CreateRun(ctx, conversationID, requestKey, prompt, snapshot)
	if err != nil {
		return RunResult{}, err
	}
	if !created {
		evidence, err := service.Store.Evidence(ctx, run.ID)
		return RunResult{Run: run, Answer: run.Output, Duplicate: true, Evidence: evidence}, err
	}
	warning, err := service.unresolvedWarning(ctx)
	if err != nil {
		_ = service.Store.FinishRun(ctx, run.ID, "stopped", "could not check unresolved operations")
		return RunResult{Run: run}, err
	}
	if warning != "" {
		service.event(Event{Kind: "unresolved", Message: warning})
	}
	service.event(Event{Kind: "run_started", Message: run.ID, RunID: run.ID, ConversationID: run.ConversationID})
	return service.process(ctx, run)
}

// RunExtensionWorkflow executes one declared workflow inside an existing
// active Run. It reuses the Run snapshot and history, so Host API model calls
// are accounted for by the same Attempt and budget machinery as the parent
// turn. The method does not create a second task or persistence object.
func (service *Service) RunExtensionWorkflow(ctx context.Context, run Run, extensionID, workflowName string, input any) (string, map[string]any, error) {
	if err := service.validate(ctx); err != nil {
		return "", nil, err
	}
	if run.ID == "" || run.ConversationID == "" || run.Status != "running" {
		return "", nil, errors.New("harness: extension workflows require an active run")
	}
	bound, err := service.forRun(run)
	if err != nil {
		return "", nil, err
	}
	if bound.extensions == nil || bound.capabilities == nil ||
		!contains(bound.capabilities.Extensions, extensionID) {
		return "", nil, errors.New("harness: extension workflow is not enabled for this run")
	}
	history, err := bound.Store.ListMessages(ctx, run.ConversationID)
	if err != nil {
		return "", nil, err
	}
	return bound.extensions.workflow(ctx, extensionID, workflowName, run, input,
		modelHistory(history), bound)
}

func (service *Service) Continue(ctx context.Context, runID string) (RunResult, error) {
	if err := service.validate(ctx); err != nil {
		return RunResult{}, err
	}
	if service.controlToolsActive {
		control, err := service.controlService()
		if err != nil {
			return RunResult{}, err
		}
		if err := control.CheckExternalChanges(ctx); err != nil {
			return RunResult{}, err
		}
	}
	if !service.ownsLease {
		if !service.Store.runMu.TryLock() {
			return RunResult{}, ErrActiveRun
		}
		defer service.Store.runMu.Unlock()
	}
	run, err := service.Store.GetRun(ctx, runID)
	if err != nil {
		return RunResult{}, err
	}
	if service.guard == nil {
		if parent, err := service.Store.controlledParent(ctx, run.ID); err != nil || parent != "" {
			return RunResult{Run: run}, errors.Join(ErrControlledRun, err)
		}
	}
	if run.Status == "completed" {
		evidence, err := service.Store.Evidence(ctx, run.ID)
		return RunResult{Run: run, Answer: run.Output, Duplicate: true, Evidence: evidence}, err
	}
	if run.Status == "unknown" {
		return RunResult{Run: run}, ErrUnknownResult
	}
	bound, err := service.forRun(run)
	if err != nil {
		return RunResult{Run: run}, err
	}
	service = bound
	service.allowFrozenContinuation = run.Status == "waiting_confirmation" && service.hasPendingToolApproval(run.ID)
	limits := service.limits()
	if run.ElapsedMS >= int64(limits.MaxRunSeconds)*1000 {
		return RunResult{Run: run}, ErrTimeLimit
	}
	if run.Steps >= limits.MaxSteps {
		return RunResult{Run: run}, ErrStepLimit
	}
	if run.Status == "failed" || run.Status == "abandoned" {
		return RunResult{Run: run}, errors.New("harness: failed or abandoned runs cannot be continued")
	}
	snapshot, err := service.snapshot()
	if err != nil {
		return RunResult{}, err
	}
	if snapshot != run.ConfigJSON {
		return RunResult{Run: run}, ErrConfigChanged
	}
	if err := service.checkConfig(run); err != nil {
		return RunResult{Run: run}, err
	}
	attempts, err := service.Store.ListAttempts(ctx, runID)
	if err != nil {
		return RunResult{}, err
	}
	for _, attempt := range attempts {
		if attempt.Status == "unknown" || attempt.Status == "dispatched" {
			return RunResult{Run: run}, ErrUnknownResult
		}
	}
	if err := service.Store.FinishRun(ctx, run.ID, "running", ""); err != nil {
		return RunResult{}, err
	}
	run.Status = "running"
	service.event(Event{Kind: "run_started", RunID: run.ID, ConversationID: run.ConversationID})
	return service.process(ctx, run)
}

func (service *Service) process(parent context.Context, run Run) (result RunResult, returnErr error) {
	clock := &runClock{parent: parent, store: service.Store, runID: run.ID,
		limit: time.Duration(service.limits().MaxRunSeconds) * time.Second, activity: service.activity}
	defer func() {
		returnErr = errors.Join(activeTimeout(parent, returnErr), clock.pause())
		persistCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if current, err := service.Store.GetRun(persistCtx, run.ID); err == nil {
			if current.Status == "running" && returnErr != nil {
				status := "stopped"
				if errors.Is(returnErr, ErrStepLimit) || errors.Is(returnErr, ErrContextLimit) || errors.Is(returnErr, ErrTimeLimit) ||
					errors.Is(returnErr, ErrParentBudget) || errors.Is(returnErr, ErrChildBudget) {
					status = "limited"
				}
				if errors.Is(returnErr, ErrUnknownResult) {
					status = "unknown"
				}
				_ = service.Store.FinishRun(persistCtx, run.ID, status, safeError(returnErr))
				current, _ = service.Store.GetRun(persistCtx, run.ID)
			}
			result.Run = current
			result.Answer = current.Output
			evidence, err := service.Store.Evidence(persistCtx, run.ID)
			result.Evidence = evidence
			returnErr = errors.Join(returnErr, err)
			service.event(Event{Kind: "run_settled", Message: current.Status, RunID: run.ID, ConversationID: run.ConversationID})
			if current.Status != "waiting_confirmation" && current.Status != "running" {
				service.finishExtensions(persistCtx, current)
			}
		}
	}()
	if err := clock.resume(); err != nil {
		return result, err
	}
	for {
		ctx := clock.ctx
		if err := ctx.Err(); err != nil {
			return result, err
		}
		attempts, err := service.Store.ListAttempts(ctx, run.ID)
		if err != nil {
			return result, err
		}
		pending := false
		for _, attempt := range attempts {
			switch attempt.Status {
			case "unknown", "dispatched":
				return result, ErrUnknownResult
			case "pending":
				if attempt.Kind != "tool" {
					return result, errors.New("harness: invalid pending attempt")
				}
				pending = true
				if err := service.executeTool(clock, run, attempt); err != nil {
					if errors.Is(err, errControlChangeApplied) {
						return result, nil
					}
					if errors.Is(err, errAwaitingApproval) {
						return result, nil
					}
					return result, err
				}
			}
		}
		if pending {
			continue
		}
		if err := service.checkConfig(run); err != nil {
			return result, err
		}
		if err := service.prepareAuto(ctx, &run, attempts); err != nil {
			return result, err
		}
		request, err := service.buildContext(ctx, run)
		if err != nil {
			return result, err
		}
		_, response, err := service.generate(ctx, run, request)
		if err != nil {
			return result, err
		}
		if len(response.ToolCalls) == 0 {
			service.event(Event{Kind: "run_completed"})
			return result, nil
		}
	}
}

// embed is the model-side boundary for extension Host API embedding calls.
// It deliberately reuses model Attempts and the existing call guard; vectors
// are recorded as an auxiliary response and never become chat messages.
func (service *Service) embed(ctx context.Context, run Run, purpose string, inputs []string) (EmbeddingResponse, error) {
	if len(inputs) == 0 || len(inputs) > 128 {
		return EmbeddingResponse{}, &CallError{Certain: true, Category: "request",
			Message: "embedding input count is out of range"}
	}
	totalBytes := 0
	for _, input := range inputs {
		if input == "" || len(input) > 1<<20 {
			return EmbeddingResponse{}, &CallError{Certain: true, Category: "request",
				Message: "embedding input is out of range"}
		}
		totalBytes += len(input)
		if totalBytes > 4<<20 {
			return EmbeddingResponse{}, &CallError{Certain: true, Category: "request",
				Message: "embedding inputs exceed the limit"}
		}
	}
	limits, binding, err := service.requestLimits()
	if err != nil {
		return EmbeddingResponse{}, err
	}
	if binding.Config.EffectiveCapabilities().Embeddings != "supported" {
		return EmbeddingResponse{}, errExtensionUnavailable
	}
	request := ModelRequest{ModelID: binding.ID, ModelRevision: binding.Revision,
		Purpose: purpose, Inputs: append([]string(nil), inputs...), MaxOutputTokens: 1}
	raw, err := json.Marshal(request)
	if err != nil {
		return EmbeddingResponse{}, err
	}
	if len(raw) > limits.MaxContextBytes {
		return EmbeddingResponse{}, ErrContextLimit
	}
	if err := service.checkDispatchConfig(run, true); err != nil {
		return EmbeddingResponse{}, err
	}
	attempt, err := service.Store.startGuardedModel(ctx, run, string(raw), service.limits().MaxSteps, service.guard)
	if err != nil {
		return EmbeddingResponse{}, err
	}
	client, err := service.modelClient()
	if err != nil {
		return EmbeddingResponse{}, err
	}
	embedder, ok := client.(EmbeddingClient)
	if !ok {
		failure := &CallError{Certain: true, Category: "capability",
			Message: "selected model has no embedding adapter"}
		saveErr := service.Store.failModel(ctx, run, attempt, ModelResponse{Failure: failure}, failure,
			service.guard, 0)
		return EmbeddingResponse{}, errors.Join(failure, saveErr)
	}
	response, callErr := embedder.Embed(ctx, binding.Config.Model, inputs)
	modelResponse := ModelResponse{Vectors: response.Vectors, Usage: response.Usage, Provider: response.Provider}
	responseRaw, _ := json.Marshal(modelResponse)
	outputSize := len(responseRaw)
	if callErr != nil {
		var certainty *CallError
		if errors.As(callErr, &certainty) && certainty.Certain {
			saveErr := service.Store.failModel(ctx, run, attempt, modelResponse, certainty,
				service.guard, outputSize)
			return EmbeddingResponse{}, errors.Join(callErr, saveErr)
		}
		saveErr := service.Store.markUnknown(ctx, attempt, "embedding outcome unknown", string(responseRaw))
		return EmbeddingResponse{}, errors.Join(ErrUnknownResult, callErr, saveErr)
	}
	if len(response.Vectors) != len(inputs) {
		failure := &CallError{Certain: true, Category: "protocol",
			Message: "embedding adapter returned the wrong vector count"}
		saveErr := service.Store.failModel(ctx, run, attempt, modelResponse, failure,
			service.guard, outputSize)
		return EmbeddingResponse{}, errors.Join(failure, saveErr)
	}
	outputErr := service.recordOutput(ctx, attempt, outputSize, false)
	if outputErr != nil && !isOutputLimit(outputErr) {
		saveErr := service.Store.markUnknown(ctx, attempt, "embedding output accounting stopped", string(responseRaw))
		return EmbeddingResponse{}, errors.Join(outputErr, saveErr)
	}
	saveErr := service.Store.saveGuardedModel(ctx, run, attempt, modelResponse, service.guard, outputSize)
	if saveErr != nil && !isOutputLimit(saveErr) {
		return EmbeddingResponse{}, saveErr
	}
	return response, errors.Join(outputErr, saveErr)
}

// All model work, including bounded routing/planning/synthesis, uses this
// dispatch and persistence boundary. Auxiliary responses stay in Attempts.
func (service *Service) generate(ctx context.Context, run Run, request ModelRequest) (Attempt, ModelResponse, error) {
	if request.Purpose != "" && request.Purpose != "compaction" {
		messages, uses, err := service.memoryContext(ctx, run)
		if err != nil {
			return Attempt{}, ModelResponse{}, err
		}
		request.Messages = append(request.Messages, messages...)
		request.Memory = uses
	}
	if request.Purpose != "compaction" {
		var err error
		request, err = service.prepareExtensionStep(ctx, run, request)
		if err != nil {
			return Attempt{}, ModelResponse{}, err
		}
	} else if len(request.Tools)+len(request.Memory)+len(request.Extensions) != 0 {
		return Attempt{}, ModelResponse{}, ErrModelCapability
	}
	limits, binding, err := service.requestLimits()
	if err != nil {
		return Attempt{}, ModelResponse{}, err
	}
	if request.ModelID != binding.ID || request.ModelRevision != binding.Revision ||
		request.MaxOutputTokens < 1 || request.MaxOutputTokens > limits.MaxOutputTokens ||
		len(request.Messages)-1 > limits.MaxMessages ||
		request.Purpose != "" && len(request.Tools) != 0 ||
		binding.Config.Capabilities.Tools == "none" && len(request.Tools) != 0 {
		return Attempt{}, ModelResponse{}, ErrModelCapability
	}
	if err := validateModelRequest(binding.Config, request); err != nil {
		return Attempt{}, ModelResponse{}, err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return Attempt{}, ModelResponse{}, err
	}
	if len(raw)+request.MaxOutputTokens*4 > limits.MaxContextBytes {
		return Attempt{}, ModelResponse{}, ErrContextLimit
	}
	client, err := service.modelClient()
	if err != nil {
		return Attempt{}, ModelResponse{}, err
	}
	if err := service.checkDispatchConfig(run, true); err != nil {
		return Attempt{}, ModelResponse{}, err
	}
	attempt, err := service.Store.startGuardedModel(ctx, run, string(raw), service.limits().MaxSteps, service.guard)
	if err != nil {
		return attempt, ModelResponse{}, err
	}
	if len(request.Memory) != 0 {
		service.event(Event{Kind: "memory_sources", Sources: request.Memory})
	}
	modelEvent := func(kind, text string) {
		service.event(Event{Kind: kind, Message: text, RunID: run.ID,
			ConversationID: run.ConversationID, AttemptID: attempt.ID, Purpose: request.Purpose})
	}
	modelEvent("model_started", request.ModelID)
	streamBytes := 0
	callCtx, cancelCall := context.WithCancel(ctx)
	var streamErr error
	response, callErr := client.Generate(callCtx, request, func(text string) {
		streamBytes += len(text)
		if service.guard != nil {
			persistCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			streamErr = errors.Join(streamErr, service.recordOutput(persistCtx, attempt, streamBytes, false))
			done()
			if streamErr != nil {
				cancelCall()
			}
		}
		if request.Purpose == "" {
			modelEvent("model_text", text)
		}
	})
	cancelCall()
	persistCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	responseRaw, _ := json.Marshal(response)
	outputSize := max(streamBytes, len(responseRaw))
	outputErr := errors.Join(streamErr, service.recordOutput(persistCtx, attempt, outputSize, false))
	if outputErr != nil && !isOutputLimit(outputErr) {
		modelEvent("model_interrupted", "output accounting stopped")
		saveErr := service.Store.markUnknown(persistCtx, attempt, "output accounting stopped; result must be inspected")
		return attempt, response, errors.Join(outputErr, saveErr)
	}
	if callErr != nil {
		modelEvent("model_interrupted", "response was not retained as a final answer")
		var certainty *CallError
		if errors.As(callErr, &certainty) && certainty.Certain {
			saveErr := service.Store.failModel(persistCtx, run, attempt, response, certainty, service.guard, outputSize)
			return attempt, response, errors.Join(callErr, outputErr, saveErr)
		}
		var record []string
		if certainty != nil {
			failure := response
			failure.ToolCalls, failure.Failure = nil, certainty
			if raw, err := json.Marshal(failure); err == nil {
				record = []string{string(raw)}
			}
		}
		saveErr := service.Store.markUnknown(persistCtx, attempt, "model outcome unknown", record...)
		return attempt, response, errors.Join(ErrUnknownResult, outputErr, saveErr)
	}
	responseErr := validateResponse(response)
	if responseErr == nil {
		if controlErr := controlResponseError(response, request.Tools); controlErr != nil {
			responseErr = controlErr
		}
	}
	if responseErr == nil && (len(request.Tools) == 0 && len(response.ToolCalls) != 0 ||
		binding.Config.EffectiveCapabilities().ParallelTools == "none" && len(response.ToolCalls) > 1) {
		responseErr = ErrModelCapability
	}
	if responseErr == nil && binding.Config.EffectiveCapabilities().Usage == "required" && response.Usage == nil {
		responseErr = ErrModelCapability
	}
	if responseErr != nil {
		modelEvent("model_interrupted", "invalid or incomplete response")
		var certain *CallError
		if errors.As(responseErr, &certain) && certain != nil && certain.Certain {
			saveErr := service.Store.failModel(persistCtx, run, attempt, response, certain, service.guard, outputSize)
			return attempt, response, errors.Join(responseErr, saveErr)
		}
		reason := "invalid or incomplete model response"
		if responseErr != nil {
			reason += ": " + safeError(responseErr)
		}
		saveErr := service.Store.markUnknown(persistCtx, attempt, reason)
		return attempt, response, errors.Join(responseErr, saveErr)
	}
	saveErr := service.Store.saveGuardedModel(persistCtx, run, attempt, response, service.guard, outputSize)
	if saveErr != nil && !isOutputLimit(saveErr) {
		modelEvent("model_interrupted", "response persistence failed")
		return attempt, response, saveErr
	}
	attempt.Status, attempt.ResponseJSON = "finished", string(responseRaw)
	modelEvent("model_finished", "")
	outputErr = errors.Join(outputErr, saveErr)
	if outputErr != nil {
		return attempt, response, errors.Join(outputErr, service.Store.FinishRun(persistCtx, run.ID, "limited", safeError(outputErr)))
	}
	return attempt, response, nil
}

func (service *Service) executeTool(clock *runClock, run Run, attempt Attempt) error {
	return service.executeToolWithContext(clock.ctx, clock.parent, run, attempt,
		func() error { return clock.pause() },
		func() error { return clock.resume() },
		func() context.Context { return clock.ctx }, false, true)
}

// executeToolWithContext is shared by the normal model path and Host API
// calls made by an extension. The latter is already inside an active turn, so
// it must not replace or pause the turn clock.
func (service *Service) executeToolWithContext(ctx, parent context.Context, run Run, attempt Attempt,
	pause func() error, resume func() error, current func() context.Context,
	rejectWithoutApproval, recordMessage bool) error {
	if err := service.checkConfig(run); err != nil {
		return err
	}
	var call ToolCall
	if err := json.Unmarshal([]byte(attempt.RequestJSON), &call); err != nil {
		return errors.New("harness: corrupt pending tool call")
	}
	prepared, err := service.runtimeTools(run.ID, attempt.ID).Prepare(ctx, call)
	if err == nil && prepared.release != nil {
		defer prepared.release()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		result := ToolResult{Content: "tool validation error: " + safeError(err), IsError: true}
		if recordMessage {
			return service.Store.saveTool(ctx, run, attempt, call, result)
		}
		return service.Store.saveToolNoMessage(ctx, run, attempt, call, result)
	}
	if prepared.controlProposal {
		if service.controlDecision != nil {
			control, controlErr := service.controlService()
			if controlErr != nil {
				return controlErr
			}
			decision, controlErr := control.decideControlChange(ctx, prepared.controlOperationID, *service.controlDecision, service.controlApproved, true)
			if controlErr != nil {
				return controlErr
			}
			content := "control proposal rejected; no setting was changed"
			if decision.Applied {
				content = "control change applied; it takes effect in the next Run"
			}
			result := ToolResult{Content: content, IsError: !decision.Applied}
			if err := service.Store.settleControlProposal(ctx, run, attempt, result, decision.Applied, "control_change_applied_next_run"); err != nil {
				return err
			}
			if decision.Applied {
				return errControlChangeApplied
			}
			return nil
		}
		if err := pause(); err != nil {
			return err
		}
		service.event(Event{Kind: "control_confirmation", Tool: call.Name, Preview: prepared.Preview,
			RunID: run.ID, ConversationID: run.ConversationID, AttemptID: attempt.ID})
		if err := service.Store.FinishRun(parent, run.ID, "waiting_confirmation", StopReasonControlProposal); err != nil {
			return err
		}
		return errAwaitingApproval
	}
	if (prepared.Execute == nil && prepared.ExecuteRecorded == nil && prepared.executeTransaction == nil) ||
		(prepared.Effect != EffectRead && prepared.Effect != EffectModify) ||
		prepared.preauthorized && prepared.executeTransaction == nil {
		return errors.New("harness: invalid tool execution boundary")
	}
	extensionAsk, extensionDeny, err := service.checkOperation(ctx, run, call, prepared)
	if err != nil {
		return err
	}
	if extensionDeny != "" {
		if prepared.controlProposal {
			if control, controlErr := service.controlService(); controlErr == nil {
				_ = control.withdrawProposal(prepared.controlOperationID, "extension_denied")
			}
		}
		result := ToolResult{Content: "operation denied by extension: " + extensionDeny, IsError: true}
		if recordMessage {
			return service.Store.saveTool(ctx, run, attempt, call, result)
		}
		return service.Store.saveToolNoMessage(ctx, run, attempt, call, result)
	}
	if service.needsApproval(prepared) || extensionAsk {
		if err := pause(); err != nil {
			return err
		}
		service.event(Event{Kind: "confirmation", Tool: call.Name, Preview: prepared.Preview,
			RunID: run.ID, ConversationID: run.ConversationID, AttemptID: attempt.ID})
		if service.Approve == nil {
			if rejectWithoutApproval {
				result := ToolResult{Content: "tool call rejected: approval handler is unavailable; operation was not dispatched", IsError: true}
				if recordMessage {
					return service.Store.saveTool(ctx, run, attempt, call, result)
				}
				return service.Store.saveToolNoMessage(ctx, run, attempt, call, result)
			}
			if err := service.Store.FinishRun(parent, run.ID, "waiting_confirmation", "user confirmation required"); err != nil {
				return err
			}
			return errAwaitingApproval
		}
		approved, err := service.Approve(parent, call, prepared)
		if errors.Is(err, errAwaitingApproval) {
			if err := service.Store.FinishRun(parent, run.ID, "waiting_confirmation", "user confirmation required"); err != nil {
				return err
			}
			return errAwaitingApproval
		}
		if err != nil {
			return err
		}
		if err := parent.Err(); err != nil {
			return err
		}
		if err := resume(); err != nil {
			return err
		}
		ctx = current()
		if !approved {
			result := ToolResult{Content: "user declined this operation; do not repeat it", IsError: true}
			if prepared.Reject != nil {
				result = prepared.Reject()
			}
			if recordMessage {
				return service.Store.saveTool(ctx, run, attempt, call, result)
			}
			return service.Store.saveToolNoMessage(ctx, run, attempt, call, result)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := service.checkConfig(run); err != nil {
		return err
	}
	if err := service.Store.dispatchGuardedTool(ctx, attempt, service.limits().MaxSteps, service.guard); err != nil {
		return err
	}
	toolEvent := func(kind string) {
		service.event(Event{Kind: kind, Tool: call.Name, RunID: run.ID,
			ConversationID: run.ConversationID, AttemptID: attempt.ID})
	}
	toolEvent("tool_started")
	if prepared.executeTransaction != nil {
		var result ToolResult
		var err error
		if recordMessage {
			result, err = service.Store.executeLocalTool(ctx, run, attempt, call, prepared.executeTransaction,
				service.guard, service.limits().MaxToolResult)
		} else {
			result, err = service.Store.executeLocalToolNoMessage(ctx, run, attempt, call, prepared.executeTransaction,
				service.guard, service.limits().MaxToolResult)
		}
		if err != nil && !isOutputLimit(err) {
			persist, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			current, loadErr := service.Store.ListAttempts(persist, run.ID)
			if loadErr != nil {
				return errors.Join(err, loadErr)
			}
			for _, saved := range current {
				if saved.ID == attempt.ID && saved.Status == "dispatched" {
					return errors.Join(err, service.Store.markUnknown(persist, attempt, "local transaction outcome unresolved; inspect saved result"))
				}
			}
			return err
		}
		if len(result.Memory) != 0 {
			service.event(Event{Kind: "memory_sources", Sources: result.Memory})
		}
		toolEvent("tool_finished")
		return err
	}
	var result ToolResult
	var callErr error
	if prepared.ExecuteRecorded != nil {
		result, callErr = prepared.ExecuteRecorded(ctx, func(progress ToolResult) error {
			persistCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			raw, err := json.Marshal(progress)
			if err != nil {
				return err
			}
			if err := service.recordOutput(persistCtx, attempt, len(raw), false); err != nil {
				return err
			}
			_, err = service.Store.db.ExecContext(persistCtx, `UPDATE attempts SET response_json=? WHERE id=? AND status='dispatched'`, string(raw), attempt.ID)
			return err
		})
	} else {
		result, callErr = prepared.Execute(ctx)
	}
	persistCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	rawOutput, _ := json.Marshal(result)
	outputSize := len(rawOutput)
	outputErr := service.recordOutput(persistCtx, attempt, outputSize, false)
	if outputErr != nil && !isOutputLimit(outputErr) {
		saveErr := service.Store.markUnknown(persistCtx, attempt,
			"tool output accounting stopped; effects may have occurred", string(rawOutput))
		return errors.Join(outputErr, saveErr)
	}
	if callErr != nil {
		var certainty *CallError
		if !errors.As(callErr, &certainty) || !certainty.Certain {
			saveErr := service.Store.markUnknown(persistCtx, attempt, "tool outcome unknown", string(rawOutput))
			return errors.Join(ErrUnknownResult, outputErr, saveErr)
		}
		result.Content = "tool error: " + safeError(callErr)
		result.IsError = true
	}
	if len(result.Content) > service.limits().MaxToolResult {
		result.Content = "tool result exceeded configured limit; operation may have completed; text was not retained"
		result.IsError = true
		for index := range result.Receipts {
			result.Receipts[index].Truncated = true
		}
	}
	var saveErr error
	if recordMessage {
		saveErr = service.Store.saveGuardedTool(persistCtx, run, attempt, call, result, service.guard, outputSize, callErr == nil)
	} else {
		saveErr = service.Store.saveGuardedToolNoMessage(persistCtx, run, attempt, call, result, service.guard, outputSize, callErr == nil)
	}
	if saveErr != nil && !isOutputLimit(saveErr) {
		return saveErr
	}
	outputErr = errors.Join(outputErr, saveErr)
	if outputErr != nil {
		return outputErr
	}
	toolEvent("tool_finished")
	return nil
}

func (service *Service) checkOperation(ctx context.Context, run Run, call ToolCall, prepared PreparedTool) (bool, string, error) {
	if prepared.Effect != EffectModify || prepared.preauthorized ||
		service.extensions == nil || service.capabilities == nil {
		return false, "", nil
	}
	selection := service.capabilities.normalize()
	if len(selection.Extensions) == 0 {
		return false, "", nil
	}
	service.extensions.configure(service.Config)
	ask := false
	caller := extensionCaller(ctx)
	for _, id := range selection.Extensions {
		if id == caller {
			continue
		}
		definition, definitionErr := service.extensions.definition(id)
		if definitionErr != nil {
			// A broken required extension may be the one that would deny this
			// operation, so its absence must stop the operation.
			if service.Config.Extensions.Required[id] {
				return false, "", fmt.Errorf("required extension %q is unavailable for operation.check: %w", id, definitionErr)
			}
			service.event(Event{Kind: "extension_error", Message: id + " operation.check: " + safeError(definitionErr),
				RunID: run.ID, ConversationID: run.ConversationID})
			continue
		}
		if !contains(definition.Manifest.Events, "operation.check") {
			continue
		}
		decision, err := service.extensions.checkOperation(ctx, id, run, call, prepared, service)
		if err != nil {
			required := service.Config.Extensions.Required[id] || definition.Manifest.Required
			if required {
				return false, "", fmt.Errorf("required extension %q operation.check failed: %w", id, err)
			}
			service.event(Event{Kind: "extension_error", Message: id + " operation.check: " + safeError(err),
				RunID: run.ID, ConversationID: run.ConversationID})
			continue
		}
		switch decision.Decision {
		case "ask":
			ask = true
		case "deny":
			reason := decision.Reason
			if reason == "" {
				reason = "extension denied this operation"
			}
			return ask, reason, nil
		}
	}
	return ask, "", nil
}

func (service *Service) buildContext(ctx context.Context, run Run) (ModelRequest, error) {
	summary, _, err := service.Store.contextSummary(ctx, run.ConversationID, false)
	if err == nil {
		request, buildErr := service.buildContextWithSummary(ctx, run, summary)
		if buildErr == nil {
			return request, nil
		}
		err = buildErr
	}
	if (errors.Is(err, ErrContextLimit) || errors.Is(err, ErrSummaryStale)) && service.Config.TaskSummaryBytes > 0 && !service.compactionFailed && run.ID != "" {
		plan, planErr := service.planCompactionFor(ctx, run, ContextCommand{Action: "compact"}, true)
		if planErr != nil {
			service.compactionFailed = true
			return ModelRequest{}, planErr
		}
		result, compactErr := service.applyCompaction(ctx, run, plan, true)
		if compactErr != nil {
			service.compactionFailed = true
			return ModelRequest{}, compactErr
		}
		if !result.Changed {
			return ModelRequest{}, ErrContextLimit
		}
		summary, _, loadErr := service.Store.contextSummary(ctx, run.ConversationID, false)
		if loadErr != nil {
			return ModelRequest{}, loadErr
		}
		return service.buildContextWithSummary(ctx, run, summary)
	}
	return ModelRequest{}, err
}

func (service *Service) buildContextWithSummary(ctx context.Context, run Run, summary *ConversationSummary) (ModelRequest, error) {
	if err := service.loadSkills(ctx, run.Prompt); err != nil {
		return ModelRequest{}, err
	}
	limits, binding, err := service.requestLimits()
	if err != nil {
		return ModelRequest{}, err
	}
	through := int64(0)
	if summary != nil {
		through = summary.CoveredThrough
	}
	history, err := service.Store.contextTail(ctx, run, through, limits.MaxMessages, limits.MaxContextBytes)
	if err != nil {
		return ModelRequest{}, err
	}
	if run.ID == "" {
		history = append(history, Message{ModelMessage: ModelMessage{Role: "user", Content: run.Prompt}})
	}
	system := ModelMessage{Role: "system", Content: executionInvariant + "\n" + service.Config.SystemPrompt + "\n" +
		service.Config.instructionText}
	warning, err := service.unresolvedWarning(ctx)
	if err != nil {
		return ModelRequest{}, err
	}
	if warning != "" {
		system.Content += "\n" + warning
	}
	request := ModelRequest{ModelID: binding.ID, ModelRevision: binding.Revision, Tools: service.runtimeTools(run.ID, "").Definitions(), MaxOutputTokens: limits.MaxOutputTokens}
	if binding.Config.Capabilities.Tools == "none" {
		request.Tools = nil
	}
	memoryMessages, memoryUses, err := service.memoryContext(ctx, run)
	if err != nil {
		return ModelRequest{}, err
	}
	request.Memory = memoryUses
	request.Messages = []ModelMessage{system}
	if summary != nil {
		request.Messages = append(request.Messages, summaryInput(summary))
	}
	request.Messages = append(request.Messages, memoryMessages...)
	for _, item := range history {
		request.Messages = append(request.Messages, item.ModelMessage)
	}
	request, err = service.prepareCapabilityRequest(ctx, run, request)
	if err != nil {
		return ModelRequest{}, err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return ModelRequest{}, err
	}
	if len(request.Messages)-1 > limits.MaxMessages || len(raw)+limits.MaxOutputTokens*4 > limits.MaxContextBytes {
		return ModelRequest{}, ErrContextLimit
	}
	if err := completeMessageGroups(request.Messages); err != nil {
		return ModelRequest{}, err
	}
	if binding.Config.Capabilities.Tools == "none" {
		for _, message := range request.Messages {
			if message.Role == "tool" || len(message.ToolCalls) != 0 {
				return ModelRequest{}, ErrModelCapability
			}
		}
	}
	return request, nil
}

func (service *Service) unresolvedWarning(ctx context.Context) (string, error) {
	var count int
	err := service.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM attempts a
		JOIN runs r ON r.id=a.run_id JOIN conversations c ON c.id=r.conversation_id
		WHERE a.status='unknown' AND r.status='abandoned' AND c.workspace=?`, service.Config.Workspace).Scan(&count)
	if err != nil || count == 0 {
		return "", err
	}
	return fmt.Sprintf("%d previous operation(s) in this workspace remain UNKNOWN after their tasks were abandoned. Effects may already have happened. Check inspect before repeating related work; do not repeat them implicitly.", count), nil
}

func validateResponse(response ModelResponse) error {
	if response.Failure != nil {
		return errors.New("harness: failed provider output cannot be a final answer")
	}
	if len(response.ToolCalls) > 32 || len(response.Content) > 1<<20 {
		return errors.New("harness: model response exceeded limits")
	}
	if response.Usage != nil && (response.Usage.InputTokens < 0 || response.Usage.OutputTokens < 0 || response.Usage.TotalTokens < 0) {
		return errors.New("harness: invalid model usage")
	}
	if response.Provider != nil {
		meta := response.Provider
		if len(meta.ID) > 256 || len(meta.Model) > 256 || len(meta.SystemFingerprint) > 256 ||
			len(meta.RawFinishReason) > 64 || meta.Created < 0 ||
			(meta.Protocol != "chat" && meta.Protocol != "responses") || (meta.FinishReason != "stop" && meta.FinishReason != "tool_calls" && meta.FinishReason != "output_limit" && meta.FinishReason != "content_filter") ||
			(meta.UsageStatus != "reported" && meta.UsageStatus != "missing") ||
			(meta.UsageStatus == "reported") != (response.Usage != nil) {
			return errors.New("harness: invalid provider response metadata")
		}
		if len(meta.ResponseStatus) > 32 || (meta.ResponseStatus != "" && meta.ResponseStatus != "completed" && meta.ResponseStatus != "incomplete") {
			return errors.New("harness: invalid Responses status")
		}
		if len(meta.WebSearchCalls) > 32 {
			return errors.New("harness: too many web search calls")
		}
		for _, call := range meta.WebSearchCalls {
			if len(call.ID) > 256 || len(call.Status) > 32 || len(call.Action) > 32 ||
				len(call.Query) > 4096 || len(call.URL) > 2048 {
				return errors.New("harness: invalid web search call metadata")
			}
		}
	}
	if len(response.ProviderItems) > 64 {
		return errors.New("harness: too many provider output items")
	}
	for _, item := range response.ProviderItems {
		if len(item) == 0 || len(item) > 1<<20 || !json.Valid(item) {
			return errors.New("harness: invalid provider output item")
		}
	}
	ids := map[string]bool{}
	for _, call := range response.ToolCalls {
		if call.ID == "" || len(call.ID) > 256 || ids[call.ID] || call.Name == "" || len(call.Name) > 128 || len(call.Arguments) > 2*maxFileBytes {
			return errors.New("harness: invalid or duplicate model tool call")
		}
		if call.Provider != nil && (call.Provider.Index < 0 || call.Provider.Index >= 32 || call.Provider.Type != "function") {
			return errors.New("harness: invalid provider tool item")
		}
		ids[call.ID] = true
	}
	return nil
}

func validateModelRequest(config ModelConfig, request ModelRequest) error {
	caps := config.EffectiveCapabilities()
	if config.Provider == "anthropic" && caps.Protocol != "chat" {
		return ErrModelCapability
	}
	if caps.Tools == "none" && len(request.Tools) != 0 {
		return ErrModelCapability
	}
	for _, message := range request.Messages {
		providerCalls := 0
		for _, raw := range message.ProviderItems {
			var item struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &item) == nil && item.Type == "function_call" {
				providerCalls++
			}
		}
		toolCalls := len(message.ToolCalls)
		if len(message.ProviderItems) != 0 {
			toolCalls = providerCalls
		}
		if caps.Tools == "none" && (message.Role == "tool" || toolCalls != 0) ||
			caps.ParallelTools == "none" && toolCalls > 1 {
			return ErrModelCapability
		}
	}
	return completeMessageGroups(request.Messages)
}

func (service *Service) event(event Event) {
	if service.Emit != nil {
		service.Emit(event)
	}
}

func (service *Service) finishExtensions(ctx context.Context, run Run) {
	if service.extensions == nil {
		return
	}
	// The Run is settled; its per-extension model budget is no longer needed.
	defer service.extensions.releaseRun(run.ID)
	if service.capabilities == nil {
		return
	}
	selection := service.capabilities.normalize()
	if len(selection.Extensions) == 0 {
		return
	}
	history, err := service.Store.contextMessages(ctx, run, 64, 64<<10)
	if err != nil {
		service.event(Event{Kind: "extension_error", Message: "turn.finished history: " + safeError(err),
			RunID: run.ID, ConversationID: run.ConversationID})
		return
	}
	for _, id := range selection.Extensions {
		activity, err := service.extensions.finish(ctx, id, run, run.Status, modelHistory(history), service)
		if err != nil {
			service.event(Event{Kind: "extension_error", Message: id + " turn.finished: " + safeError(err),
				RunID: run.ID, ConversationID: run.ConversationID})
			continue
		}
		if len(activity) != 0 {
			summary, _ := activity["summary"].(string)
			if summary == "" {
				summary = "extension activity"
			}
			service.event(Event{Kind: "extension_activity", Message: summary, Activity: activity,
				RunID: run.ID, ConversationID: run.ConversationID})
		}
	}
}

func modelHistory(messages []Message) []ModelMessage {
	result := make([]ModelMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, message.ModelMessage)
	}
	return result
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	var callErr *CallError
	if errors.As(err, &callErr) && callErr == nil {
		return ""
	}
	return fmt.Sprintf("%.512s", err.Error())
}
