package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const maxExtensionDelegationResponse = 256 << 10

// delegationToolRunner exposes the tools frozen into the active turn while
// removing extension tools. A child may use the parent's built-in/MCP surface,
// but it cannot start another extension process. task.delegate is a Host
// method, not a tool, so removing extension aliases also stops recursion.
type delegationToolRunner struct {
	base ToolRunner
	defs []ToolDefinition
}

func (tools *delegationToolRunner) Definitions() []ToolDefinition {
	return slices.Clone(tools.defs)
}

func (tools *delegationToolRunner) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if !slices.ContainsFunc(tools.defs, func(definition ToolDefinition) bool {
		return definition.Name == call.Name
	}) {
		return PreparedTool{}, errors.New("harness: delegation tool was not authorized for this turn")
	}
	return tools.base.Prepare(ctx, call)
}

func delegationTools(service *Service, runID string) (ToolRunner, error) {
	if service == nil || service.Tools == nil {
		return nil, errors.New("harness: delegation requires configured tools")
	}
	base := service.Tools
	if runtime, ok := base.(*RuntimeTools); ok && service.capabilities != nil {
		base = runtime.ForCapabilities(*service.capabilities)
	}
	defs := base.Definitions()
	filtered := make([]ToolDefinition, 0, len(defs))
	for _, definition := range defs {
		if extensionToolAlias(definition.Name) {
			continue
		}
		filtered = append(filtered, definition)
	}
	return &delegationToolRunner{base: base, defs: filtered}, nil
}

func normalizeDelegationPlan(plan CollaborationPlan, core *Service) (CollaborationPlan, error) {
	if core == nil {
		return plan, errors.New("harness: delegation runtime is unavailable")
	}
	binding, err := core.binding()
	if err != nil {
		return plan, err
	}
	allowedModels := map[string]bool{binding.ID: true}
	for _, saved := range core.bindings {
		allowedModels[saved.ID] = true
	}
	for name, role := range plan.Roles {
		if role.ModelID == "" {
			role.ModelID = binding.ID
		}
		if !allowedModels[role.ModelID] {
			return plan, fmt.Errorf("harness: delegated role model %q is outside the active turn", role.ModelID)
		}
		plan.Roles[name] = role
	}
	return plan, nil
}

func validateActiveDelegationPlan(plan CollaborationPlan, core *Service, invocation *extensionInvocation) (CollaborationPlan, error) {
	if core == nil || invocation == nil {
		return plan, errors.New("harness: delegation invocation is unavailable")
	}
	if plan.Synthesis || plan.MaxReplans != 0 {
		return plan, errors.New("harness: task.delegate does not support synthesis or replanning inside an active turn")
	}
	var err error
	plan, err = normalizeDelegationPlan(plan, core)
	if err != nil {
		return plan, err
	}
	if err := plan.validate(core); err != nil {
		return plan, err
	}
	limits := core.limits()
	if limits.MaxSteps < 1 {
		return plan, ErrStepLimit
	}
	maxActive := int(max(int64(1), int64(limits.MaxRunSeconds)-activeElapsed(invocation.run)/1000))
	if plan.Budget.MaxActiveSeconds > maxActive {
		return plan, fmt.Errorf("harness: delegated active time exceeds the remaining turn budget")
	}
	maxModelCalls := plan.Budget.MaxChildren * limits.MaxSteps
	maxToolCalls := plan.Budget.MaxChildren * limits.MaxSteps
	if plan.Budget.MaxModelCalls > maxModelCalls || plan.Budget.MaxToolCalls > maxToolCalls {
		return plan, errors.New("harness: delegated call budget exceeds finite child step capacity")
	}
	for _, role := range plan.Roles {
		if role.Budget.MaxSeconds > maxActive {
			return plan, errors.New("harness: delegated child time exceeds the remaining turn budget")
		}
	}
	return plan, nil
}

func insertActiveDelegation(ctx context.Context, core *Service, run Run, plan CollaborationPlan) (collaborationRecord, error) {
	if _, err := loadCollaboration(ctx, core.Store.db, run.ID); err == nil {
		return collaborationRecord{}, errors.New("harness: an active run may delegate only once")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return collaborationRecord{}, err
	}
	snapshot, err := core.snapshot()
	if err != nil {
		return collaborationRecord{}, err
	}
	record := collaborationRecord{
		ParentID: run.ID, Plan: plan, ConfigJSON: snapshot, Control: "running",
		Usage:        CollaborationUsage{Children: len(plan.Children)},
		Reservations: map[string]outputReservation{}, Revision: 1,
	}
	tx, err := core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return record, err
	}
	defer tx.Rollback()
	for _, spec := range plan.Children {
		role := plan.Roles[spec.Role]
		childSnapshot, prompt, err := assignmentSnapshot(core, spec, role, run.ID, "")
		if err != nil {
			return record, err
		}
		child, err := insertCollaborationRun(ctx, tx, core.Config.Workspace,
			"Child: "+spec.Name, run.RequestKey+"/"+spec.Name, prompt, childSnapshot)
		if err != nil {
			return record, err
		}
		record.Children = append(record.Children, childRecord{
			Spec: spec, RunID: child.ID, Control: "running",
		})
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta VALUES(?,?)`,
			"collaboration-run/"+child.ID, run.ID); err != nil {
			return record, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta VALUES(?,?)`,
		"collaboration-run/"+run.ID, run.ID); err != nil {
		return record, err
	}
	if err := saveCollaboration(ctx, tx, record); err != nil {
		return record, err
	}
	if err := savePlanRevision(ctx, tx, record, "extension",
		"active Run extension delegation", ""); err != nil {
		return record, err
	}
	return record, tx.Commit()
}

func activeDelegationReady(record collaborationRecord, report CollaborationReport) []childRecord {
	statuses := make(map[string]string, len(report.Children))
	for _, child := range report.Children {
		statuses[child.Spec.Name] = child.Run.Status
	}
	var ready []childRecord
	for _, child := range record.Children {
		if child.Retired || child.Control != "running" {
			continue
		}
		status := statuses[child.Spec.Name]
		if status == "completed" || status == "failed" || status == "abandoned" || status == "limited" ||
			status == "unknown" || status == "waiting_confirmation" {
			continue
		}
		ok := true
		for _, dependency := range child.Spec.DependsOn {
			if statuses[dependency] != "completed" {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, child)
		}
	}
	return ready
}

func activeDelegationBatch(core *Service, record collaborationRecord, report CollaborationReport) []childRecord {
	ready := activeDelegationReady(record, report)
	if len(ready) == 0 {
		return nil
	}
	limit := min(len(ready), record.Plan.Budget.MaxConcurrentChildren)
	batch := make([]childRecord, 0, limit)
	for _, child := range ready {
		if len(batch) == 0 {
			batch = append(batch, child)
			if len(batch) >= limit || !record.Plan.Parallel || !parallelSafe(core.Tools, record.Plan.Roles[child.Spec.Role]) {
				break
			}
			continue
		}
		if !parallelSafe(core.Tools, record.Plan.Roles[child.Spec.Role]) {
			break
		}
		batch = append(batch, child)
		if len(batch) == limit {
			break
		}
	}
	return batch
}

func finishActiveDelegationRecord(ctx context.Context, collab *Collaboration,
	parentID, control string) error {
	return collab.change(ctx, parentID, func(record *collaborationRecord, _ *sql.Tx) error {
		if record.Control == "cancelled" {
			return nil
		}
		record.Control = control
		return nil
	})
}

func delegateResult(report CollaborationReport) (map[string]any, error) {
	children := make([]map[string]any, 0, len(report.Children))
	for _, child := range report.Children {
		children = append(children, map[string]any{
			"name": child.Spec.Name, "status": child.Run.Status,
			"objective": child.Spec.Objective, "output": boundedText(child.Run.Output, 64<<10),
			"result": child.Result, "evidence": child.Evidence,
			"observed_results": child.Observed,
		})
	}
	result := map[string]any{
		"status": report.Control, "control": report.Control, "parent_status": report.Parent.Status,
		"children": children, "usage": report.Usage,
		"remaining": report.Remaining, "gaps": report.Gaps,
		"conflicts": report.Conflicts,
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > maxExtensionDelegationResponse {
		return nil, errors.New("harness: delegated result exceeds the limit")
	}
	return result, nil
}

// runActiveDelegation executes children while the foreground parent already
// owns Store.runMu. It intentionally does not call Collaboration.Continue,
// because that method settles its parent Run and is for standalone plans. The
// plan's full call and output budget is booked against the extension's
// per-Run limits before any child starts.
func runActiveDelegation(ctx context.Context, invocation *extensionInvocation,
	plan CollaborationPlan, maxCalls, maxOutput int) (result map[string]any, returnErr error) {
	if invocation == nil || invocation.service == nil || invocation.usage == nil {
		return nil, errExtensionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, errExtensionCancel
	}
	parent, err := invocation.service.Store.GetRun(ctx, invocation.run.ID)
	if err != nil {
		return nil, err
	}
	if parent.Status != "running" {
		return nil, errExtensionUnavailable
	}
	if owner, err := invocation.service.Store.controlledParent(ctx, parent.ID); err != nil || owner != "" {
		return nil, errors.Join(errExtensionDenied, err)
	}
	if err := invocation.service.checkConfig(parent); err != nil {
		return nil, err
	}
	core := *invocation.service
	tools, err := delegationTools(&core, invocation.run.ID)
	if err != nil {
		return nil, errExtensionUnavailable
	}
	core.Config = delegationConfig(core.Config)
	core.Tools = tools
	core.controlToolsDisabled = true
	core.extensions = nil
	core.skills = nil
	if core.capabilities != nil {
		selected := core.capabilities.normalize()
		selected.Extensions = nil
		selected.Skills = nil
		core.capabilities = &selected
	}
	// Validate against the original frozen parent, then derive the same
	// stripped configuration that was recorded for delegated children.
	core.Refresh = func() (Config, error) {
		if err := invocation.service.checkConfig(parent); err != nil {
			return Config{}, err
		}
		return core.Config, nil
	}
	plan, err = validateActiveDelegationPlan(plan, &core, invocation)
	if err != nil {
		return nil, err
	}
	reservedCalls, reservedOutput := plan.Budget.MaxModelCalls, max(1, plan.Budget.MaxOutputBytes/4)
	if !invocation.usage.reserve(reservedCalls, reservedOutput, maxCalls, maxOutput) {
		return nil, errExtensionBudget
	}
	if _, err := insertActiveDelegation(ctx, &core, parent, plan); err != nil {
		invocation.usage.settle(-reservedCalls, -reservedOutput, maxOutput)
		return nil, err
	}
	tasks := NewTaskService(&core)
	collab := NewCollaboration(tasks)
	defer func() {
		persist, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		record, err := loadCollaboration(persist, core.Store.db, parent.ID)
		if err != nil {
			returnErr = errors.Join(returnErr, err)
			return
		}
		// Final observations replace the reservation; uncertain calls retain
		// their reserved output, including when the Host request fails.
		invocation.usage.settle(record.Usage.ModelCalls-reservedCalls,
			max(0, record.Usage.OutputBytes/4)-reservedOutput, maxOutput)
		report, err := collab.Inspect(persist, parent.ID)
		if err != nil {
			returnErr = errors.Join(returnErr, err)
			return
		}
		for _, child := range report.Children {
			if child.Run.Status == "unknown" {
				returnErr = errors.Join(errExtensionOutcomeUnknown, returnErr,
					core.Store.FinishRun(persist, parent.ID, "unknown", "delegated child outcome UNKNOWN; no automatic replay"))
				result = nil
				break
			}
		}
		if returnErr != nil {
			control := "paused"
			if ctx.Err() != nil {
				control = "cancelled"
			}
			returnErr = errors.Join(returnErr, finishActiveDelegationRecord(persist, collab, parent.ID, control))
		}
	}()
	remaining := time.Duration(plan.Budget.MaxActiveSeconds)*time.Second - time.Duration(activeElapsed(parent))*time.Millisecond
	if remaining <= 0 {
		return nil, ErrTimeLimit
	}
	ctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			persist, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = finishActiveDelegationRecord(persist, collab, invocation.run.ID, "cancelled")
			cancel()
			return nil, errExtensionCancel
		}
		report, err := collab.Inspect(ctx, invocation.run.ID)
		if err != nil {
			return nil, err
		}
		for _, child := range report.Children {
			if child.Run.Status == "unknown" {
				return nil, errExtensionOutcomeUnknown
			}
			if child.Run.Status == "failed" || child.Run.Status == "abandoned" {
				_ = finishActiveDelegationRecord(ctx, collab, invocation.run.ID, "paused")
				return nil, &jsonrpc.Error{Code: -32603, Message: "delegated child failed"}
			}
			if child.Run.Status == "waiting_confirmation" {
				_ = finishActiveDelegationRecord(ctx, collab, invocation.run.ID, "paused")
				return nil, errExtensionDenied
			}
			if child.Run.Status == "limited" {
				return nil, errExtensionBudget
			}
		}
		record, err := loadCollaboration(ctx, core.Store.db, invocation.run.ID)
		if err != nil {
			return nil, err
		}
		batch := activeDelegationBatch(&core, record, report)
		if len(batch) == 0 {
			allDone := len(report.Children) != 0
			for _, child := range report.Children {
				allDone = allDone && (child.Run.Status == "completed" || child.Retired)
			}
			if !allDone {
				_ = finishActiveDelegationRecord(ctx, collab, invocation.run.ID, "paused")
				return nil, errors.New("harness: delegated plan has no runnable child")
			}
			if err := finishActiveDelegationRecord(ctx, collab, invocation.run.ID, "completed"); err != nil {
				return nil, err
			}
			// The control transition happens after the first inspection. Read
			// the final report so the extension sees completed, not running.
			report, err = collab.Inspect(ctx, invocation.run.ID)
			if err != nil {
				return nil, err
			}
			return delegateResult(report)
		}
		names := make([]string, len(batch))
		for i := range batch {
			names[i] = batch[i].Spec.Name
		}
		if err := collab.activate(ctx, invocation.run.ID, names, record.Revision); err != nil {
			return nil, err
		}
		errs := make(chan error, len(batch))
		for _, child := range batch {
			child := child
			go func() {
				errs <- collab.runChild(ctx, &core, record, child)
			}()
		}
		var batchErr error
		for range batch {
			batchErr = errors.Join(batchErr, <-errs)
		}
		if batchErr != nil {
			if errors.Is(batchErr, ErrUnknownResult) || errors.Is(batchErr, context.Canceled) ||
				errors.Is(batchErr, context.DeadlineExceeded) {
				return nil, errExtensionOutcomeUnknown
			}
			return nil, batchErr
		}
	}
}

func delegationConfig(config Config) Config {
	config.Extensions = ExtensionsConfig{}
	config.Skills = SkillsConfig{}
	return config
}
