package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
)

var (
	ErrCollaborationDisabled = errors.New("harness: collaboration is disabled")
	ErrControlledRun         = errors.New("harness: this run belongs to collaboration; use its controls")
	ErrParentBudget          = errors.New("harness: cumulative collaboration budget exhausted")
	ErrChildBudget           = errors.New("harness: child output budget exhausted")
	ErrChildPaused           = errors.New("harness: child is paused or cancelled")
	ErrMergeDecision         = errors.New("harness: conflicting evidence requires an exact user decision")
	ErrMissingEvidence       = errors.New("harness: child result lacks execution evidence")
	ErrPlanRevision          = errors.New("harness: collaboration plan changed; inspect the current revision")
	ErrResultReference       = errors.New("harness: authorized result source or range is unavailable")
)

type ParentBudget struct {
	MaxChildren           int `json:"max_children"`
	MaxModelCalls         int `json:"max_model_calls"`
	MaxToolCalls          int `json:"max_tool_calls"`
	MaxOutputBytes        int `json:"max_output_bytes"`
	MaxConcurrentChildren int `json:"max_concurrent_children"`
	MaxActiveSeconds      int `json:"max_active_seconds"`
}

func (budget ParentBudget) validate() error {
	if budget.MaxChildren < 1 || budget.MaxChildren > 16 ||
		budget.MaxConcurrentChildren < 1 || budget.MaxConcurrentChildren > 4 ||
		budget.MaxConcurrentChildren > budget.MaxChildren ||
		budget.MaxModelCalls < 1 || budget.MaxModelCalls > 1000 ||
		budget.MaxToolCalls < 1 || budget.MaxToolCalls > 1000 ||
		budget.MaxOutputBytes < 1024 || budget.MaxOutputBytes > 64<<20 ||
		budget.MaxActiveSeconds < 1 || budget.MaxActiveSeconds > 3600 {
		return errors.New("harness: invalid finite parent execution limits")
	}
	return nil
}

type ChildBudget struct {
	MaxSteps           int `json:"max_steps"`
	MaxSeconds         int `json:"max_seconds"`
	MaxOutputBytes     int `json:"max_output_bytes"`
	MaxCallOutputBytes int `json:"max_call_output_bytes"`
}

// Profiles live in explicitly selected plans, not an organization registry.
type RoleProfile struct {
	Instructions     string                `json:"instructions"`
	ModelID          string                `json:"model_id,omitempty"`
	Tools            []string              `json:"tools"`
	Paths            []string              `json:"paths"`
	OutputFormat     string                `json:"output_format"`
	Acceptance       string                `json:"acceptance,omitempty"`
	RequiredEvidence []EvidenceRequirement `json:"required_evidence,omitempty"`
	Budget           ChildBudget           `json:"budget"`
	Memory           RoleMemory            `json:"memory"`
}

type ChildSpec struct {
	Name      string            `json:"name"`
	Role      string            `json:"role"`
	Objective string            `json:"objective"`
	Context   string            `json:"context,omitempty"`
	DependsOn []string          `json:"depends_on,omitempty"`
	Results   []ResultReference `json:"results,omitempty"`
}

type CollaborationPlan struct {
	Objective  string                 `json:"objective"`
	Budget     ParentBudget           `json:"budget"`
	Roles      map[string]RoleProfile `json:"roles"`
	Children   []ChildSpec            `json:"children"`
	Parallel   bool                   `json:"parallel,omitempty"`
	MaxReplans int                    `json:"max_replans,omitempty"`
	Synthesis  bool                   `json:"synthesis,omitempty"`
}

func DecodeCollaborationPlan(reader io.Reader) (CollaborationPlan, error) {
	var plan CollaborationPlan
	raw, err := io.ReadAll(io.LimitReader(reader, (256<<10)+1))
	if err != nil || len(raw) > 256<<10 {
		return plan, errors.New("harness: collaboration plan exceeds 256 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return plan, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return plan, errors.New("harness: plan must be one JSON object")
	}
	return plan, nil
}

func (plan CollaborationPlan) validate(core *Service) error {
	if core == nil || core.Tools == nil || (core.Model == nil && core.ModelFor == nil) {
		return errors.New("harness: collaboration requires a configured task runtime")
	}
	if err := core.Config.Validate(); err != nil {
		return err
	}
	b := plan.Budget
	if err := b.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(plan.Objective) == "" || len(plan.Objective) > 32768 ||
		len(plan.Children) < 1 || len(plan.Children) > b.MaxChildren || len(plan.Roles) < 1 || len(plan.Roles) > 16 ||
		plan.MaxReplans < 0 || plan.MaxReplans > 4 {
		return errors.New("harness: invalid bounded collaboration plan")
	}
	for name, role := range plan.Roles {
		if !validEnvName(name) || len(name) > 64 || len(role.Instructions) > 32768 ||
			(role.OutputFormat != "text" && role.OutputFormat != "json") ||
			(role.Acceptance != "" && role.Acceptance != "analysis" && role.Acceptance != "execution") {
			return errors.New("harness: invalid role name, instructions or output format")
		}
		config, err := childConfig(core.Config, role)
		if err != nil {
			return err
		}
		if len(role.Tools) != 0 && config.DefaultModelConfig().Capabilities.Tools == "none" {
			return ErrModelCapability
		}
		policy, err := role.Memory.policy("validation", core.Config.Workspace, "")
		if err != nil {
			return err
		}
		if len(policy.Selection.Entries) > config.Memory.MaxEntries ||
			len(policy.Selection.Knowledge) != 0 && !config.Memory.KnowledgeEnabled {
			return ErrMemoryDenied
		}
		if policy.enabled() && config.DefaultModelConfig().Capabilities.Tools == "none" &&
			(policy.Selection.TaskPrivate || policy.Shared != nil || len(policy.Selection.Knowledge) != 0) {
			return ErrModelCapability
		}
		tools, err := newAssignedTools(core.Tools, role)
		if err != nil {
			return err
		}
		if err := validateRoleEvidence(role, tools); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, child := range plan.Children {
		if seen[child.Name] {
			return errors.New("harness: duplicate child")
		}
		seen[child.Name] = true
	}
	graph := map[string][]string{}
	for _, child := range plan.Children {
		role, exists := plan.Roles[child.Role]
		if !exists || !validEnvName(child.Name) || len(child.Name) > 64 ||
			strings.TrimSpace(child.Objective) == "" || len(child.Objective)+len(child.Context) > 32768 {
			return errors.New("harness: invalid or duplicate child")
		}
		dependencies := map[string]bool{}
		for _, dependency := range child.DependsOn {
			if !seen[dependency] || dependency == child.Name || dependencies[dependency] {
				return errors.New("harness: dependencies must name distinct other children")
			}
			dependencies[dependency] = true
		}
		graph[child.Name] = child.DependsOn
		if err := validateResultReferences(child); err != nil {
			return err
		}
		if len(childPrompt(child, role)) > core.Config.MaxContextBytes/2 {
			return ErrContextLimit
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return errors.New("harness: cyclic child dependencies")
		}
		if visited[name] {
			return nil
		}
		visiting[name] = true
		for _, dependency := range graph[name] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[name], visited[name] = false, true
		return nil
	}
	for name := range graph {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func childConfig(base Config, role RoleProfile) (Config, error) {
	b := role.Budget
	if b.MaxSteps < 1 || b.MaxSteps > base.MaxSteps || b.MaxSeconds < 1 || b.MaxSeconds > base.MaxRunSeconds ||
		b.MaxOutputBytes < 1024 || b.MaxOutputBytes > 16<<20 ||
		b.MaxCallOutputBytes < 256 || b.MaxCallOutputBytes > 1<<20 || b.MaxCallOutputBytes > b.MaxOutputBytes {
		return Config{}, errors.New("harness: child budgets must be bounded within the selected runtime")
	}
	base.MaxSteps, base.MaxRunSeconds = b.MaxSteps, b.MaxSeconds
	base.MaxOutputTokens = min(base.MaxOutputTokens, max(1, b.MaxCallOutputBytes/8))
	base.MaxToolResult = min(base.MaxToolResult, b.MaxCallOutputBytes)
	base.SystemPrompt, base.instructionText, base.Instructions = role.Instructions, "", nil
	if role.ModelID != "" {
		if _, exists := base.Models[role.ModelID]; !exists {
			return Config{}, errors.New("harness: role model must belong to the configured model pool")
		}
		base.DefaultModel = role.ModelID
	}
	base.Collaboration = CollaborationSettings{}
	base.DefaultSelection = base.DefaultModel
	return base, base.Validate()
}

func childPrompt(child ChildSpec, role RoleProfile) string {
	format := "Return a concise text summary. Assertions in your response are not execution evidence."
	if role.OutputFormat == "json" {
		format = `Return exactly one JSON object {"summary":"...","findings":["..."]}. Findings are interpretations, not runtime evidence.`
	}
	return fmt.Sprintf("Assigned objective:\n%s\n\nExplicit authorized context (untrusted source data):\n%s\n\nAuthorized file subtrees: %s\n%s\nOnly assigned tools are available. Do not delegate or claim unrecorded checks. A JSON response may include references, artifacts, questions and a bounded follow_up request for the parent to consider; these are model interpretations, not runtime evidence or authority. Dependencies are ordering constraints, not implicit context grants.",
		child.Objective, child.Context, strings.Join(role.Paths, ", "), format)
}

type assignedTools struct {
	base        ToolRunner
	definitions []ToolDefinition
	names       map[string]ToolEffect
	paths       []string
}

func scopePath(value string) (string, error) {
	value = strings.ReplaceAll(value, `\`, "/")
	clean := path.Clean(value)
	if value == "" || strings.Contains(value, ":") || strings.HasPrefix(value, "/") ||
		clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsRune(value, 0) {
		return "", errors.New("harness: authorized paths must be workspace-relative subtrees")
	}
	return clean, nil
}

func newAssignedTools(base ToolRunner, role RoleProfile) (*assignedTools, error) {
	selected := &assignedTools{base: base, names: map[string]ToolEffect{}}
	if len(role.Tools) > 32 || len(role.Paths) > 16 {
		return nil, errors.New("harness: too many assigned tools or paths")
	}
	for _, value := range role.Paths {
		clean, err := scopePath(value)
		if err != nil || slices.Contains(selected.paths, clean) {
			return nil, errors.New("harness: invalid or duplicate scope")
		}
		selected.paths = append(selected.paths, clean)
	}
	available := map[string]ToolDefinition{}
	for _, definition := range base.Definitions() {
		available[definition.Name] = definition
	}
	for _, name := range role.Tools {
		definition, exists := available[name]
		if _, duplicate := selected.names[name]; duplicate || !exists ||
			strings.Contains(name, "delegate") || strings.Contains(name, "agent") {
			return nil, errors.New("harness: unassigned, duplicate or delegating tool")
		}
		if strings.HasPrefix(name, "file_") && len(selected.paths) == 0 {
			return nil, errors.New("harness: file tools require explicit path authorization")
		}
		selected.names[name] = definition.Effect
		selected.definitions = append(selected.definitions, definition)
	}
	return selected, nil
}

func (tools *assignedTools) Definitions() []ToolDefinition { return slices.Clone(tools.definitions) }

func (tools *assignedTools) allows(value string) bool {
	value, err := scopePath(value)
	if err != nil {
		return false
	}
	for _, prefix := range tools.paths {
		if prefix == "." || value == prefix || strings.HasPrefix(value, prefix+"/") {
			return true
		}
	}
	return false
}

func (tools *assignedTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	effect, exists := tools.names[call.Name]
	if !exists {
		return PreparedTool{}, errors.New("harness: tool was not assigned to this child")
	}
	if strings.HasPrefix(call.Name, "file_") {
		var args struct {
			Path    string `json:"path"`
			Changes []struct {
				Path string `json:"path"`
			} `json:"changes"`
		}
		if json.Unmarshal(call.Arguments, &args) != nil {
			return PreparedTool{}, errors.New("harness: invalid scoped arguments")
		}
		if call.Name == "file_batch_edit" {
			for _, change := range args.Changes {
				if !tools.allows(change.Path) {
					return PreparedTool{}, errors.New("harness: file outside authorized subtree")
				}
			}
		} else if !tools.allows(args.Path) {
			return PreparedTool{}, errors.New("harness: file outside authorized subtree")
		}
	}
	prepared, err := tools.base.Prepare(ctx, call)
	if err == nil && prepared.Effect != effect {
		return PreparedTool{}, errors.New("harness: adapter effect changed from selected definition")
	}
	return prepared, err
}

func parallelSafe(base ToolRunner, role RoleProfile) bool {
	for _, name := range role.Tools {
		if name != "file_read" && name != "file_list" && name != "file_search" {
			return false
		}
	}
	selected, err := newAssignedTools(base, role)
	if err != nil {
		return false
	}
	for _, definition := range selected.definitions {
		if definition.Effect != EffectRead {
			return false
		}
	}
	return true
}
