package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Collaboration is an optional, local composition over the same task service.
// Execution happens only inside Continue; it has no worker or background loop.
type Collaboration struct {
	tasks        *TaskService
	mu           sync.Mutex
	eventMu      sync.Mutex
	active       map[string]context.CancelFunc
	parentCancel context.CancelFunc
	activeParent string
	ModelFor     func(ModelConfig) (ModelClient, error)
}

func NewCollaboration(tasks *TaskService) *Collaboration {
	return &Collaboration{tasks: tasks, active: map[string]context.CancelFunc{}}
}

func (collab *Collaboration) core() (*Service, error) {
	if collab == nil || collab.tasks == nil || collab.tasks.core == nil {
		return nil, errors.New("harness: collaboration requires task service")
	}
	collab.tasks.mu.Lock()
	core := *collab.tasks.core
	core.Approve, core.Emit = collab.tasks.approve, collab.tasks.emit
	collab.tasks.mu.Unlock()
	return &core, nil
}

func (collab *Collaboration) emit(core *Service, child, kind string) {
	collab.eventMu.Lock()
	defer collab.eventMu.Unlock()
	core.event(Event{Kind: "collaboration", Tool: child, Message: kind})
}

func (collab *Collaboration) childService(core *Service, record collaborationRecord, child childRecord) (*Service, error) {
	if record.Control != "running" || child.Control != "running" {
		return nil, ErrChildPaused
	}
	role := record.Plan.Roles[child.Spec.Role]
	config, err := childConfig(core.Config, role)
	if err != nil {
		return nil, err
	}
	tools, err := newAssignedTools(core.Tools, role)
	if err != nil {
		return nil, err
	}
	model := core.Model
	factory := core.ModelFor
	if config.DefaultModelConfig() != core.Config.DefaultModelConfig() {
		if collab.ModelFor != nil {
			factory = collab.ModelFor
		} else if factory == nil {
			factory = keyedModelClient(core.Keys)
		}
	}
	service := NewService(core.Store, config, Adapters{Model: model, ModelFor: factory, Tools: tools, Approve: core.Approve, Keys: core.Keys})
	service.memory, err = role.Memory.policy(record.ParentID, config.Workspace,
		collaborationAgentID(record.ParentID, child.Spec.Name))
	if err != nil {
		return nil, err
	}
	service.permission = core.permission
	service.ownsLease = true
	service.controlToolsDisabled = true
	service.activity = core.activity
	service.guard = collaborationGuard{parentID: record.ParentID, childName: child.Spec.Name}
	service.Emit = func(event Event) {
		if event.Kind == "memory_sources" {
			collab.eventMu.Lock()
			defer collab.eventMu.Unlock()
			event.Tool = child.Spec.Name
			core.event(event)
		} else if event.Kind != "model_text" {
			collab.emit(core, child.Spec.Name, event.Kind)
		}
	}
	service.Refresh = func() (Config, error) {
		if core.Refresh == nil {
			return config, nil
		}
		current, err := core.Refresh()
		if err != nil {
			return Config{}, err
		}
		snapshot, err := core.snapshotConfig(current)
		if err != nil || snapshot != record.ConfigJSON {
			return Config{}, ErrConfigChanged
		}
		return childConfig(current, role)
	}
	return service, nil
}

func insertCollaborationRun(ctx context.Context, tx *sql.Tx, workspace, title, request, prompt, snapshot string) (Run, error) {
	conversation := Conversation{ID: newID(), Title: title, Workspace: workspace, CreatedAt: nowText(), UpdatedAt: nowText()}
	if _, err := tx.ExecContext(ctx, `INSERT INTO conversations VALUES(?,?,?,?,?)`, conversation.ID, title, workspace, conversation.CreatedAt, conversation.UpdatedAt); err != nil {
		return Run{}, err
	}
	run, err := insertPlannedRun(ctx, tx, conversation.ID, request, prompt, snapshot)
	if err != nil {
		return run, err
	}
	saved, err := decodeRunSnapshot(snapshot)
	if err != nil {
		return run, err
	}
	if saved.Memory != nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)`,
			"chat/agent/"+conversation.ID, saved.Memory.AgentID)
	}
	return run, err
}

func insertPlannedRun(ctx context.Context, tx *sql.Tx, conversationID, request, prompt, snapshot string) (Run, error) {
	run := Run{ID: newID(), ConversationID: conversationID, RequestKey: request, Prompt: prompt, ConfigJSON: snapshot, Status: "stopped", CreatedAt: nowText(), UpdatedAt: nowText()}
	_, err := tx.ExecContext(ctx, `INSERT INTO runs(`+runColumns+`) VALUES(?,?,?,?,?,'stopped','explicit collaboration continuation required',0,0,'',?,?,'')`,
		run.ID, conversationID, request, prompt, snapshot, run.CreatedAt, run.UpdatedAt)
	if err != nil {
		return Run{}, err
	}
	return run, insertMessage(ctx, tx, run, ModelMessage{Role: "user", Content: prompt})
}

// Create records the entire authorized plan and its bounded child runs in one
// transaction before any model or tool call. Repeating the same key is safe.
func (collab *Collaboration) Create(ctx context.Context, conversationID, requestKey string, plan CollaborationPlan) (string, error) {
	core, err := collab.core()
	if err != nil {
		return "", err
	}
	if ctx == nil {
		return "", errors.New("harness: context is required")
	}
	settings, preference, err := collab.tasks.collaborationSettings(ctx, conversationID)
	if err != nil {
		return "", err
	}
	if settings.Mode != "ask" && settings.Mode != "auto" {
		return "", ErrCollaborationDisabled
	}
	if err := plan.validate(core); err != nil {
		return "", err
	}
	if requestKey == "" {
		requestKey = newID()
	}
	// Detach caller-owned slices and maps from the durable plan.
	rawPlan, err := json.Marshal(plan)
	if err != nil || len(rawPlan) > 256<<10 {
		return "", errors.New("harness: plan exceeds limit")
	}
	if err := json.Unmarshal(rawPlan, &plan); err != nil {
		return "", err
	}
	if !core.Store.runMu.TryLock() {
		return "", ErrActiveRun
	}
	defer core.Store.runMu.Unlock()
	if conversationID != "" {
		if err := core.selectMemory(ctx, conversationID, nil); err != nil {
			return "", err
		}
	}
	core.controlToolsDisabled = true
	snapshot, err := core.snapshot()
	if err != nil {
		return "", err
	}
	if err := core.checkConfig(Run{ConfigJSON: snapshot}); err != nil {
		return "", err
	}
	tx, err := core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var parent Run
	if conversationID != "" {
		var currentPreference string
		err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "chat/collaboration/"+conversationID).Scan(&currentPreference)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		if currentPreference != preference {
			return "", ErrConfigChanged
		}
		var workspace string
		if err := tx.QueryRowContext(ctx, `SELECT workspace FROM conversations WHERE id=?`, conversationID).Scan(&workspace); err != nil {
			return "", err
		}
		if workspace != core.Config.Workspace {
			return "", errors.New("harness: parent workspace mismatch")
		}
		existing, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE conversation_id=? AND request_key=?`, conversationID, requestKey))
		if err == nil {
			saved, err := loadCollaboration(ctx, tx, existing.ID)
			if err != nil {
				return "", ErrRequestKey
			}
			if saved.InitialPlan != string(rawPlan) {
				return "", ErrRequestKey
			}
			return existing.ID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		var unfinished int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM runs WHERE conversation_id=? AND status NOT IN ('completed','failed','abandoned')`, conversationID).Scan(&unfinished); err != nil {
			return "", err
		}
		if unfinished > 0 {
			return "", ErrActiveRun
		}
		parent, err = insertPlannedRun(ctx, tx, conversationID, requestKey, plan.Objective, snapshot)
	} else {
		parent, err = insertCollaborationRun(ctx, tx, core.Config.Workspace, "Collaboration: "+plan.Objective[:min(len(plan.Objective), 60)], requestKey, plan.Objective, snapshot)
	}
	if err != nil {
		return "", err
	}
	record := collaborationRecord{ParentID: parent.ID, Plan: plan, ConfigJSON: snapshot, Control: "running",
		Usage: CollaborationUsage{Children: len(plan.Children)}, Reservations: map[string]outputReservation{},
		Revision: 1, InitialPlan: string(rawPlan)}
	for _, spec := range plan.Children {
		role := plan.Roles[spec.Role]
		snapshot, prompt, err := assignmentSnapshot(core, spec, role, parent.ID, "")
		if err != nil {
			return "", err
		}
		run, err := insertCollaborationRun(ctx, tx, core.Config.Workspace, "Child: "+spec.Name, requestKey+"/"+spec.Name, prompt, snapshot)
		if err != nil {
			return "", err
		}
		record.Children = append(record.Children, childRecord{Spec: spec, RunID: run.ID, Control: "running"})
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta VALUES(?,?)`, "collaboration-run/"+run.ID, parent.ID); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta VALUES(?,?)`, "collaboration-run/"+parent.ID, parent.ID); err != nil {
		return "", err
	}
	if err := saveCollaboration(ctx, tx, record); err != nil {
		return "", err
	}
	if err := savePlanRevision(ctx, tx, record, "user", "initial authorized plan", ""); err != nil {
		return "", err
	}
	return parent.ID, tx.Commit()
}

func (collab *Collaboration) change(ctx context.Context, parentID string, edit func(*collaborationRecord, *sql.Tx) error) error {
	core, err := collab.core()
	if err != nil {
		return err
	}
	tx, err := core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	record, err := loadCollaboration(ctx, tx, parentID)
	if err != nil {
		return err
	}
	if err := edit(&record, tx); err != nil {
		return err
	}
	if err := saveCollaboration(ctx, tx, record); err != nil {
		return err
	}
	return tx.Commit()
}

func (collab *Collaboration) Control(ctx context.Context, parentID, childName, action string) error {
	if action != "pause" && action != "resume" && action != "cancel" {
		return errors.New("harness: control must be pause, resume or cancel")
	}
	err := collab.change(ctx, parentID, func(record *collaborationRecord, tx *sql.Tx) error {
		parent, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id=?`, parentID))
		if err != nil {
			return err
		}
		if record.Control == "cancelled" && action == "cancel" {
			return nil
		}
		if parent.Status == "completed" || record.Control == "cancelled" {
			return errors.New("harness: completed or cancelled collaboration is immutable")
		}
		next := map[string]string{"pause": "paused", "resume": "running", "cancel": "cancelled"}[action]
		if childName == "" {
			if action == "resume" && parent.Status == "unknown" {
				return ErrUnknownResult
			}
			record.Control = next
		} else {
			found := false
			for index := range record.Children {
				child := &record.Children[index]
				if child.Spec.Name != childName {
					continue
				}
				found = true
				if child.Retired {
					return ErrControlledRun
				}
				run, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id=?`, child.RunID))
				if err != nil {
					return err
				}
				if run.Status == "completed" || child.Control == "cancelled" {
					if child.Control == "cancelled" && action == "cancel" {
						return nil
					}
					return errors.New("harness: completed or cancelled child is immutable")
				}
				if action == "resume" && run.Status == "unknown" {
					return ErrUnknownResult
				}
				child.Control = next
			}
			if !found {
				return errors.New("harness: child not found")
			}
		}
		record.AcceptedDigest, record.Decision = "", ""
		return nil
	})
	if err != nil {
		return err
	}
	if action != "resume" {
		collab.mu.Lock()
		if collab.activeParent == parentID && childName == "" {
			if collab.parentCancel != nil {
				collab.parentCancel()
			}
			for _, cancel := range collab.active {
				cancel()
			}
		} else if cancel := collab.active[childName]; collab.activeParent == parentID && cancel != nil {
			cancel()
		}
		collab.mu.Unlock()
	}
	if action == "cancel" {
		core, err := collab.core()
		if err != nil {
			return err
		}
		if core.Store.runMu.TryLock() {
			defer core.Store.runMu.Unlock()
			return collab.drainCancelled(ctx, core, parentID)
		}
	}
	return nil
}

func (collab *Collaboration) drainCancelled(ctx context.Context, core *Service, parentID string) error {
	record, err := loadCollaboration(ctx, core.Store.db, parentID)
	if err != nil {
		return err
	}
	for _, child := range record.Children {
		if child.Control != "cancelled" && record.Control != "cancelled" {
			continue
		}
		run, err := core.Store.GetRun(ctx, child.RunID)
		if err != nil {
			return err
		}
		if run.Status != "completed" && run.Status != "failed" {
			if err := core.Store.abandonRun(ctx, child.RunID); err != nil {
				return err
			}
		}
	}
	if record.Control == "cancelled" {
		return core.Store.abandonRun(ctx, parentID)
	}
	return nil
}

// Adjust only changes work that has never been dispatched. In-flight effects
// and UNKNOWN outcomes cannot be replaced by an apparently fresh child.
func (collab *Collaboration) Adjust(ctx context.Context, parentID, name string, spec ChildSpec) error {
	if spec.Name != name {
		return errors.New("harness: adjustment cannot change child identity")
	}
	report, err := collab.Inspect(ctx, parentID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(report.Children, func(child ChildReport) bool { return child.Spec.Name == name }) {
		return errors.New("harness: child not found")
	}
	return collab.Revise(ctx, parentID, CollaborationEdit{ExpectedRevision: report.Revision,
		Reason: "adjust undispatched child", Upsert: []ChildSpec{spec}})
}

func (collab *Collaboration) activate(ctx context.Context, parentID string, names []string, revision ...int) error {
	return collab.change(ctx, parentID, func(record *collaborationRecord, _ *sql.Tx) error {
		if len(revision) != 0 && revision[0] != record.Revision {
			return ErrPlanRevision
		}
		if record.Control != "running" {
			return ErrChildPaused
		}
		if len(record.Active)+len(names) > record.Plan.Budget.MaxConcurrentChildren {
			return ErrParentBudget
		}
		if record.Usage.ModelCalls >= record.Plan.Budget.MaxModelCalls || record.Usage.OutputBytes >= record.Plan.Budget.MaxOutputBytes {
			return ErrParentBudget
		}
		for _, name := range names {
			child := recordChild(*record, name)
			if child.RunID == "" || child.Control != "running" || child.Retired || slices.Contains(record.Active, name) {
				return ErrChildPaused
			}
		}
		record.Active = append(record.Active, names...)
		record.Usage.ActiveChildren = len(record.Active)
		return nil
	})
}

func (collab *Collaboration) deactivate(parentID, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return collab.change(ctx, parentID, func(record *collaborationRecord, _ *sql.Tx) error {
		record.Active = slices.DeleteFunc(record.Active, func(value string) bool { return value == name })
		record.Usage.ActiveChildren = len(record.Active)
		return nil
	})
}

func (collab *Collaboration) runChild(ctx context.Context, core *Service, record collaborationRecord, child childRecord) error {
	childCtx, cancel := context.WithCancel(ctx)
	collab.mu.Lock()
	collab.active[child.Spec.Name] = cancel
	collab.mu.Unlock()
	defer func() {
		cancel()
		collab.mu.Lock()
		delete(collab.active, child.Spec.Name)
		collab.mu.Unlock()
	}()
	var err error
	if len(child.Spec.Results) != 0 {
		err = collab.deliverResults(childCtx, core, record.ParentID, child.Spec.Name, record.Revision)
	}
	var service *Service
	if err == nil {
		service, err = collab.childService(core, record, child)
	}
	if err == nil {
		collab.emit(core, child.Spec.Name, "started")
		_, err = service.Continue(childCtx, child.RunID)
	}
	return errors.Join(err, collab.deactivate(record.ParentID, child.Spec.Name))
}

// Continue owns the workspace lease for the whole foreground operation.
// Parallel batches contain only independent built-in read-only children.
func (collab *Collaboration) Continue(ctx context.Context, parentID string) (report CollaborationReport, returnErr error) {
	core, err := collab.core()
	if err != nil {
		return report, err
	}
	if ctx == nil {
		return report, errors.New("harness: context is required")
	}
	collab.mu.Lock()
	if collab.parentCancel != nil {
		collab.mu.Unlock()
		return report, ErrActiveRun
	}
	parentCtx, cancel := context.WithCancel(ctx)
	collab.parentCancel = cancel
	collab.activeParent = parentID
	collab.mu.Unlock()
	defer func() {
		cancel()
		collab.mu.Lock()
		collab.parentCancel = nil
		collab.activeParent = ""
		collab.mu.Unlock()
	}()
	if !core.Store.runMu.TryLock() {
		return report, ErrActiveRun
	}
	defer core.Store.runMu.Unlock()
	record, err := loadCollaboration(parentCtx, core.Store.db, parentID)
	if err != nil {
		return report, err
	}
	parent, err := core.Store.GetRun(parentCtx, parentID)
	if err != nil {
		return report, err
	}
	core, err = core.forRun(parent)
	if err != nil {
		return report, err
	}
	core.ownsLease = true
	core.guard = collaborationGuard{parentID: parentID}
	if parent.Status == "completed" {
		return collab.Inspect(parentCtx, parentID)
	}
	if parent.Status == "unknown" {
		return collab.InspectWithError(parentCtx, parentID, ErrUnknownResult)
	}
	if parent.Status == "failed" && record.SynthesisAttempt != "" && record.Control == "running" {
		saved, err := collab.Inspect(parentCtx, parentID)
		if err != nil {
			return report, err
		}
		if saved.Synthesis.Status == "failed" {
			// Recover only the deterministic fallback of a saved synthesis
			// failure. The failed model attempt is never dispatched again.
			if err := core.Store.FinishRun(parentCtx, parentID, "stopped", "saved synthesis failed; child outcomes retained"); err != nil {
				return saved, err
			}
			parent.Status = "stopped"
		}
	}
	if parent.Status == "failed" || parent.Status == "abandoned" {
		return collab.InspectWithError(parentCtx, parentID, errors.New("harness: failed or abandoned runs cannot be continued"))
	}
	if record.Control != "running" {
		if record.Control == "cancelled" {
			if err := collab.drainCancelled(parentCtx, core, parentID); err != nil {
				return report, err
			}
		}
		return collab.InspectWithError(parentCtx, parentID, ErrChildPaused)
	}
	snapshot, err := core.snapshot()
	if err != nil || snapshot != record.ConfigJSON {
		if err != nil {
			return report, err
		}
		return report, ErrConfigChanged
	}
	if err := core.checkConfig(Run{ConfigJSON: record.ConfigJSON}); err != nil {
		return report, err
	}
	// Any previously active batch was interrupted. Its call markers, not the
	// scheduler's memory, decide whether explicit continuation is safe.
	if err := collab.change(parentCtx, parentID, func(record *collaborationRecord, _ *sql.Tx) error {
		record.Active = nil
		record.Usage.ActiveChildren = 0
		return nil
	}); err != nil {
		return report, err
	}
	if err := core.Store.FinishRun(parentCtx, parentID, "running", ""); err != nil {
		return report, err
	}
	activity := newExecutionActivity(parentCtx, core.Store, parentID, record.Plan.Budget.MaxActiveSeconds)
	core.activity, parentCtx = activity, activity.ctx
	defer func() {
		returnErr = errors.Join(activeTimeout(parentCtx, returnErr), activity.close())
		persist, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		returnErr = errors.Join(returnErr, collab.drainCancelled(persist, core, parentID))
		current, inspectErr := collab.Inspect(persist, parentID)
		if inspectErr == nil {
			status, reason := "stopped", "explicit continuation required"
			if errors.Is(returnErr, ErrParentBudget) || errors.Is(returnErr, ErrTimeLimit) ||
				errors.Is(returnErr, ErrStepLimit) || errors.Is(returnErr, ErrContextLimit) || errors.Is(returnErr, ErrChildBudget) {
				status, reason = "limited", safeError(returnErr)
			}
			for _, child := range current.Children {
				if child.Run.Status == "unknown" {
					status, reason = "unknown", "child outcome UNKNOWN; no automatic replay"
				}
			}
			saved, loadErr := loadCollaboration(persist, core.Store.db, parentID)
			if loadErr == nil && saved.Control == "cancelled" {
				status, reason = "abandoned", "parent cancelled; child evidence and UNKNOWN retained"
			}
			if current.Parent.Status == "running" {
				inspectErr = core.Store.FinishRun(persist, parentID, status, reason)
			}
			report, loadErr = collab.Inspect(persist, parentID)
			inspectErr = errors.Join(inspectErr, loadErr)
		}
		returnErr = errors.Join(returnErr, inspectErr)
	}()
	for {
		if err := parentCtx.Err(); err != nil {
			return report, err
		}
		record, err = loadCollaboration(parentCtx, core.Store.db, parentID)
		if err != nil {
			return report, err
		}
		current, err := collab.Inspect(parentCtx, parentID)
		if err != nil {
			return report, err
		}
		statuses := map[string]string{}
		for _, child := range current.Children {
			statuses[child.Spec.Name] = child.Run.Status
			if child.Run.Status == "unknown" {
				return current, ErrUnknownResult
			}
		}
		if record.ReplanAttempt != "" {
			if err := collab.replan(parentCtx, core, parentID, current); err != nil {
				return current, err
			}
			continue
		}
		var batch []childRecord
		for _, child := range record.Children {
			status := statuses[child.Spec.Name]
			if status == "completed" || child.Retired {
				continue
			}
			if child.Control != "running" || status == "failed" || status == "abandoned" || status == "waiting_confirmation" {
				continue
			}
			ready := true
			for _, dependency := range child.Spec.DependsOn {
				if statuses[dependency] != "completed" {
					ready = false
				}
			}
			if !ready {
				continue
			}
			if len(batch) == 0 {
				batch = append(batch, child)
				if !record.Plan.Parallel || !parallelSafe(core.Tools, record.Plan.Roles[child.Spec.Role]) {
					break
				}
			} else if parallelSafe(core.Tools, record.Plan.Roles[child.Spec.Role]) {
				batch = append(batch, child)
			} else {
				break
			}
			if len(batch) >= record.Plan.Budget.MaxConcurrentChildren {
				break
			}
		}
		if len(batch) == 0 {
			if shouldReplan(record, current) && record.Usage.Replans < record.Plan.MaxReplans {
				if err := collab.replan(parentCtx, core, parentID, current); err != nil {
					return current, err
				}
				continue
			}
			return collab.finalize(parentCtx, core, parentID, current)
		}
		names := make([]string, len(batch))
		for index, child := range batch {
			names[index] = child.Spec.Name
		}
		if err := collab.activate(parentCtx, parentID, names, record.Revision); err != nil {
			return current, err
		}
		results := make(chan error, len(batch))
		for _, child := range batch {
			go func(child childRecord) { results <- collab.runChild(parentCtx, core, record, child) }(child)
		}
		var batchErr error
		for range batch {
			batchErr = errors.Join(batchErr, <-results)
		}
		if batchErr != nil {
			after, inspectErr := collab.Inspect(parentCtx, parentID)
			if inspectErr == nil && !errors.Is(batchErr, ErrUnknownResult) &&
				shouldReplan(record, after) && record.Usage.Replans < record.Plan.MaxReplans {
				if err := collab.replan(parentCtx, core, parentID, after); err != nil {
					return after, errors.Join(batchErr, err)
				}
				continue
			}
			return report, batchErr
		}
		// Waiting or paused work must not be retried by the scheduling loop.
		after, err := collab.Inspect(parentCtx, parentID)
		if err != nil {
			return report, err
		}
		for _, child := range after.Children {
			if slices.Contains(names, child.Spec.Name) && child.Run.Status != "completed" {
				return after, nil
			}
		}
	}
}

func (collab *Collaboration) InspectWithError(ctx context.Context, parentID string, reason error) (CollaborationReport, error) {
	report, err := collab.Inspect(ctx, parentID)
	return report, errors.Join(reason, err)
}

// ChildPreview / ChildConfirm use the same exact saved-operation approval.
func (collab *Collaboration) ChildPreview(ctx context.Context, parentID, name string) (Confirmation, error) {
	core, err := collab.core()
	if err != nil {
		return Confirmation{}, err
	}
	parent, err := core.Store.GetRun(ctx, parentID)
	if err != nil {
		return Confirmation{}, err
	}
	core, err = core.forRun(parent)
	if err != nil {
		return Confirmation{}, err
	}
	record, err := loadCollaboration(ctx, core.Store.db, parentID)
	if err != nil {
		return Confirmation{}, err
	}
	child := recordChild(record, name)
	if child.RunID == "" {
		return Confirmation{}, errors.New("harness: child not found")
	}
	service, err := collab.childService(core, record, child)
	if err != nil {
		return Confirmation{}, err
	}
	return previewConfirmation(ctx, service, child.RunID)
}

func (collab *Collaboration) ChildConfirm(ctx context.Context, parentID, name string, decision Confirmation, approved bool) (result RunResult, returnErr error) {
	core, err := collab.core()
	if err != nil {
		return RunResult{}, err
	}
	childCtx, cancel := context.WithCancel(ctx)
	collab.mu.Lock()
	if collab.parentCancel != nil {
		collab.mu.Unlock()
		cancel()
		return RunResult{}, ErrActiveRun
	}
	collab.active[name] = cancel
	collab.activeParent, collab.parentCancel = parentID, cancel
	collab.mu.Unlock()
	defer func() {
		cancel()
		collab.mu.Lock()
		delete(collab.active, name)
		collab.activeParent, collab.parentCancel = "", nil
		collab.mu.Unlock()
	}()
	if !core.Store.runMu.TryLock() {
		return RunResult{}, ErrActiveRun
	}
	defer core.Store.runMu.Unlock()
	parent, err := core.Store.GetRun(ctx, parentID)
	if err != nil {
		return RunResult{}, err
	}
	core, err = core.forRun(parent)
	if err != nil {
		return RunResult{}, err
	}
	record, err := loadCollaboration(ctx, core.Store.db, parentID)
	if err != nil {
		return RunResult{}, err
	}
	child := recordChild(record, name)
	if child.RunID == "" {
		return RunResult{}, errors.New("harness: child not found")
	}
	service, err := collab.childService(core, record, child)
	if err != nil {
		return RunResult{}, err
	}
	activity := newExecutionActivity(childCtx, core.Store, parentID, record.Plan.Budget.MaxActiveSeconds)
	service.activity, childCtx = activity, activity.ctx
	defer func() { returnErr = errors.Join(activeTimeout(childCtx, returnErr), activity.close()) }()
	actual, err := previewConfirmation(ctx, service, child.RunID)
	if err != nil || !sameConfirmation(actual, decision) {
		return RunResult{}, errors.Join(ErrApprovalChanged, err)
	}
	if err := collab.activate(ctx, parentID, []string{name}); err != nil {
		return RunResult{}, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, collab.deactivate(parentID, name))
		persist, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		returnErr = errors.Join(returnErr, collab.drainCancelled(persist, core, parentID))
	}()
	tasks := NewTaskService(service)
	return tasks.Confirm(childCtx, child.RunID, decision, approved)
}

func sameConfirmation(left, right Confirmation) bool {
	return left.RunID == right.RunID && left.Attempt == right.Attempt &&
		left.CallID == right.CallID && left.Digest == right.Digest &&
		left.Tool == right.Tool && left.Preview == right.Preview
}

func (collab *Collaboration) Resolve(ctx context.Context, parentID, digest, decision string) error {
	if strings.TrimSpace(decision) == "" || len(decision) > 4096 {
		return errors.New("harness: a bounded explicit conflict decision is required")
	}
	core, err := collab.core()
	if err != nil {
		return err
	}
	if !core.Store.runMu.TryLock() {
		return ErrActiveRun
	}
	defer core.Store.runMu.Unlock()
	report, err := collab.Inspect(ctx, parentID)
	if err != nil {
		return err
	}
	if digest != report.Digest || len(report.Conflicts) == 0 {
		return ErrApprovalChanged
	}
	return collab.change(ctx, parentID, func(record *collaborationRecord, tx *sql.Tx) error {
		if len(record.Active) != 0 || record.Control != "running" {
			return ErrActiveRun
		}
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, parentID).Scan(&status); err != nil {
			return err
		}
		if status == "completed" {
			return errors.New("harness: merge already completed")
		}
		record.AcceptedDigest, record.Decision = digest, decision
		return nil
	})
}

func (collab *Collaboration) finalize(ctx context.Context, core *Service, parentID string, report CollaborationReport) (CollaborationReport, error) {
	for _, child := range report.Children {
		if child.Retired {
			continue
		}
		if child.Run.Status != "completed" || child.Control != "running" {
			return report, nil
		}
	}
	if len(report.Gaps) != 0 {
		return report, ErrMissingEvidence
	}
	record, err := loadCollaboration(ctx, core.Store.db, parentID)
	if err != nil {
		return report, err
	}
	if len(report.Conflicts) != 0 && record.AcceptedDigest != report.Digest {
		if err := core.Store.FinishRun(ctx, parentID, "waiting_confirmation", "conflicting runtime evidence; inspect and resolve exact digest"); err != nil {
			return report, err
		}
		return report, ErrMergeDecision
	}
	report.Decision = record.Decision
	if err := collab.saveDraftAnswer(ctx, core, parentID, report); err != nil {
		return report, err
	}
	var synthesisErr error
	if record.Plan.Synthesis {
		synthesisErr = collab.synthesize(ctx, core, parentID, report)
		report, err = collab.Inspect(ctx, parentID)
		if err != nil {
			return report, errors.Join(synthesisErr, err)
		}
		if report.Parent.Status == "unknown" || report.Parent.Status == "limited" || report.Synthesis.Status == "UNKNOWN" ||
			ctx.Err() != nil || errors.Is(synthesisErr, ErrChildPaused) || errors.Is(synthesisErr, ErrConfigChanged) ||
			errors.Is(synthesisErr, ErrParentBudget) || errors.Is(synthesisErr, ErrChildBudget) ||
			errors.Is(synthesisErr, ErrStepLimit) || errors.Is(synthesisErr, ErrContextLimit) || errors.Is(synthesisErr, ErrTimeLimit) {
			return report, synthesisErr
		}
	}
	answer := report.Answer()
	tx, err := core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	// A user control racing with synthesis must win before completion.
	current, err := loadCollaboration(ctx, tx, parentID)
	if err != nil {
		return report, err
	}
	if current.Control != "running" {
		return report, ErrChildPaused
	}
	if current.Revision != report.Revision {
		return report, ErrPlanRevision
	}
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='completed',output=?,stop_reason='',updated_at=? WHERE id=?`, answer, nowText(), parentID); err != nil {
		return report, err
	}
	parent, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id=?`, parentID))
	if err != nil {
		return report, err
	}
	if err := insertMessage(ctx, tx, parent, ModelMessage{Role: "assistant", Content: answer}); err != nil {
		return report, err
	}
	if err := tx.Commit(); err != nil {
		return report, err
	}
	collab.emit(core, "", fmt.Sprintf("merged %d children", len(report.Children)))
	return report, synthesisErr
}
