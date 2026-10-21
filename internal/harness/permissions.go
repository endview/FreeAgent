package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

type PermissionMode string

const (
	PermissionAsk       PermissionMode = "ask"
	PermissionWorkspace PermissionMode = "workspace"
	PermissionFull      PermissionMode = "full"
)

var ErrFullAccessConfirmation = errors.New("harness: Full access requires explicit confirmation for this conversation or turn")

func (mode PermissionMode) Validate() error {
	switch mode {
	case PermissionAsk, PermissionWorkspace, PermissionFull:
		return nil
	default:
		return errors.New("harness: permission must be ask, workspace or full")
	}
}

// This is part of the existing run snapshot, not another execution object.
type PermissionSelection struct {
	Mode              PermissionMode `json:"mode"`
	Source            string         `json:"source"`
	ComputerFiles     bool           `json:"computer_files,omitempty"`
	ComputerRoots     []string       `json:"computer_roots,omitempty"`
	FileScope         FileScope      `json:"file_scope,omitempty"`
	AdditionalRoots   []string       `json:"additional_roots,omitempty"`
	BlockedPaths      []string       `json:"blocked_paths,omitempty"`
	CommandEnabled    *bool          `json:"command_enabled,omitempty"`
	ProfileGeneration string         `json:"profile_generation,omitempty"`
}

func (selection PermissionSelection) validate() error {
	if selection.Source != "turn" && selection.Source != "conversation" && selection.Source != "default" {
		return errors.New("harness: invalid permission source")
	}
	if err := selection.Mode.Validate(); err != nil {
		return err
	}
	if selection.FileScope != "" {
		if err := selection.FileScope.Validate(); err != nil {
			return err
		}
	}
	if len(selection.ComputerRoots) > 8 || len(selection.AdditionalRoots) > 8 || len(selection.BlockedPaths) > 64 {
		return errors.New("harness: too many permission roots")
	}
	for _, root := range append(append(append([]string(nil), selection.ComputerRoots...), selection.AdditionalRoots...), selection.BlockedPaths...) {
		if !filepath.IsAbs(root) || strings.ContainsAny(root, "\x00\r\n") {
			return errors.New("harness: permission roots must be absolute")
		}
	}
	if len(selection.ProfileGeneration) > 128 || strings.ContainsAny(selection.ProfileGeneration, "\x00\r\n") {
		return errors.New("harness: invalid permission profile generation")
	}
	return nil
}

func (store *Store) selectedPermission(ctx context.Context, conversationID string) (PermissionSelection, error) {
	if _, err := store.GetConversation(ctx, conversationID); err != nil {
		return PermissionSelection{}, err
	}
	var raw string
	err := store.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "chat/permission/"+conversationID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return PermissionSelection{Mode: PermissionAsk, Source: "default"}, nil
	}
	if err != nil {
		return PermissionSelection{}, err
	}
	var selection PermissionSelection
	if json.Unmarshal([]byte(raw), &selection) != nil || selection.validate() != nil || selection.Source != "conversation" {
		return PermissionSelection{}, ErrStoreFormat
	}
	return selection, nil
}

func (tasks *TaskService) SelectedPermission(ctx context.Context, conversationID string) (PermissionSelection, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return PermissionSelection{}, errors.New("harness: task service and context are required")
	}
	selection, err := tasks.core.Store.selectedPermission(ctx, conversationID)
	if err != nil {
		return PermissionSelection{}, err
	}
	if selection.Source == "default" {
		selection.Mode = effectiveExecutionSettings(tasks.core.currentConfig()).PermissionMode
		if selection.Mode == "" {
			selection.Mode = PermissionAsk
		}
		selection.ProfileGeneration = effectiveToolGeneration(tasks.core.currentConfig())
	}
	return selection, nil
}

// EffectiveExecutionProfile returns the profile that the next Run would
// freeze. It is a read-only projection; active Runs keep their own snapshot.
func (tasks *TaskService) EffectiveExecutionProfile(ctx context.Context, conversationID string) (ExecutionProfile, PermissionSelection, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return ExecutionProfile{}, PermissionSelection{}, errors.New("harness: task service and context are required")
	}
	selection := PermissionSelection{Mode: effectiveExecutionSettings(tasks.core.currentConfig()).PermissionMode, Source: "default"}
	if strings.TrimSpace(conversationID) != "" {
		stored, err := tasks.SelectedPermission(ctx, conversationID)
		if err != nil {
			return ExecutionProfile{}, PermissionSelection{}, err
		}
		selection = stored
		if selection.Source == "default" {
			selection.Mode = effectiveExecutionSettings(tasks.core.currentConfig()).PermissionMode
		}
	}
	selection.ProfileGeneration = effectiveToolGeneration(tasks.core.currentConfig())
	profile, err := buildExecutionProfile(tasks.core.currentConfig(), selection)
	if err != nil {
		return ExecutionProfile{}, selection, err
	}
	return profile, selection, nil
}

// SelectPermission changes only subsequent turns. Selecting a lower mode
// removes the saved Full access grant; entering Full access again confirms it.
func (tasks *TaskService) SelectPermission(ctx context.Context, conversationID string, mode PermissionMode, confirmed bool) error {
	if err := mode.Validate(); err != nil {
		return err
	}
	selected, err := tasks.SelectedPermission(ctx, conversationID)
	if err != nil {
		return err
	}
	if mode == PermissionFull && selected.Mode != PermissionFull && !confirmed {
		return ErrFullAccessConfirmation
	}
	next := selected
	next.Mode, next.Source = mode, "conversation"
	next.ProfileGeneration = effectiveToolGeneration(tasks.Config())
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	_, err = tasks.core.Store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "chat/permission/"+conversationID, string(raw))
	return err
}

func (tasks *TaskService) SelectPermissionWithFiles(ctx context.Context, conversationID string, mode PermissionMode, confirmed, computerFiles bool, roots []string) error {
	if err := mode.Validate(); err != nil {
		return err
	}
	selected, err := tasks.SelectedPermission(ctx, conversationID)
	if err != nil {
		return err
	}
	if mode == PermissionFull && selected.Mode != PermissionFull && !confirmed {
		return ErrFullAccessConfirmation
	}
	config := tasks.Config()
	if computerFiles {
		if !config.ComputerFiles {
			return ErrComputerFilesDisabled
		}
		if len(roots) == 0 {
			roots = config.ComputerRoots
		}
		if len(roots) == 0 {
			return ErrComputerRootsRequired
		}
		if err := validateComputerRoots(ctx, tasks.DataDir(), roots); err != nil {
			return err
		}
		for _, root := range roots {
			allowed := false
			for _, configured := range config.ComputerRoots {
				if strings.EqualFold(filepath.Clean(root), filepath.Clean(configured)) {
					allowed = true
					break
				}
			}
			if !allowed {
				return ErrPathNotAllowed
			}
		}
	}
	raw, err := json.Marshal(PermissionSelection{Mode: mode, Source: "conversation", ComputerFiles: computerFiles,
		ComputerRoots: append([]string(nil), roots...), FileScope: map[bool]FileScope{false: FileScopeWorkspace, true: FileScopeSelectedRoots}[computerFiles],
		ProfileGeneration: effectiveToolGeneration(config)})
	if err != nil {
		return err
	}
	_, err = tasks.core.Store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "chat/permission/"+conversationID, string(raw))
	return err
}

// SelectExecutionProfile persists the conversation's next-run selection in
// the existing metadata table. It is intentionally a thin selection contract;
// the immutable effective profile is built only when a Run starts.
func (tasks *TaskService) SelectExecutionProfile(ctx context.Context, conversationID string, selection PermissionSelection, confirmed bool) error {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return errors.New("harness: task service and context are required")
	}
	previous, err := tasks.SelectedPermission(ctx, conversationID)
	if err != nil {
		return err
	}
	if selection.Mode == PermissionFull && previous.Mode != PermissionFull && !confirmed {
		return ErrFullAccessConfirmation
	}
	selection, err = prepareExecutionProfileSelection(ctx, tasks.DataDir(), tasks.Config(), selection)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	_, err = tasks.core.Store.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "chat/permission/"+conversationID, string(raw))
	return err
}

func (service *Service) resolvePermission(ctx context.Context, conversationID string, mode PermissionMode, confirmed bool) error {
	selected, err := service.Store.selectedPermission(ctx, conversationID)
	if err != nil {
		return err
	}
	if mode != "" {
		if err := mode.Validate(); err != nil {
			return err
		}
		if mode == PermissionFull && selected.Mode != PermissionFull && !confirmed {
			return ErrFullAccessConfirmation
		}
		selected.Mode, selected.Source = mode, "turn"
	}
	if selected.Source == "default" {
		selected.Mode = effectiveExecutionSettings(service.Config).PermissionMode
		if selected.Mode == "" {
			selected.Mode = PermissionAsk
		}
	}
	if selected.ComputerFiles {
		if err := validateComputerRoots(ctx, service.Store.DataDir(), selected.ComputerRoots); err != nil {
			return err
		}
		config := service.Config
		if !config.ComputerFiles || !sameRootSet(selected.ComputerRoots, config.ComputerRoots) {
			return ErrPathNotAllowed
		}
	}
	if selected.FileScope != "" && selected.FileScope != FileScopeWorkspace {
		selected.ComputerFiles = true
	}
	if _, err := buildExecutionProfile(service.Config, selected); err != nil {
		return err
	}
	selected.ProfileGeneration = effectiveToolGeneration(service.Config)
	service.permission = &selected
	return nil
}

// prepareExecutionProfileSelection validates and normalizes a conversation's
// future-run permission without persisting it. Control proposals use this
// before confirmation so preview has no side effects; the task method above
// persists the same normalized value after its legacy direct caller checks.
func prepareExecutionProfileSelection(ctx context.Context, dataDir string, config Config, selection PermissionSelection) (PermissionSelection, error) {
	if selection.Source == "" {
		selection.Source = "conversation"
	}
	selection.Source = "conversation"
	if err := selection.validate(); err != nil {
		return PermissionSelection{}, err
	}
	allowedRoots := append([]string(nil), config.ComputerRoots...)
	allowedRoots = append(allowedRoots, effectiveExecutionSettings(config).AdditionalRoots...)
	requestedRoots := append([]string(nil), selection.ComputerRoots...)
	requestedRoots = append(requestedRoots, selection.AdditionalRoots...)
	if len(requestedRoots) != 0 {
		if err := validateComputerRoots(ctx, dataDir, requestedRoots); err != nil {
			return PermissionSelection{}, err
		}
		for _, root := range requestedRoots {
			if !containsPath(allowedRoots, root) {
				return PermissionSelection{}, ErrPathNotAllowed
			}
		}
	}
	blocked, err := normalizeBlockedPaths(ctx, selection.BlockedPaths)
	if err != nil {
		return PermissionSelection{}, err
	}
	selection.BlockedPaths = blocked
	selection.ProfileGeneration = effectiveToolGeneration(config)
	if selection.FileScope == "" {
		if len(requestedRoots) != 0 {
			selection.FileScope = FileScopeSelectedRoots
		} else {
			selection.FileScope = FileScopeWorkspace
		}
	}
	if selection.FileScope != FileScopeWorkspace {
		selection.ComputerFiles = true
	}
	if _, err := buildExecutionProfile(config, selection); err != nil {
		return PermissionSelection{}, err
	}
	return selection, nil
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if samePath(path, target) {
			return true
		}
	}
	return false
}

func (service *Service) needsApproval(prepared PreparedTool) bool {
	if prepared.controlProposal || prepared.preauthorized || prepared.Effect != EffectModify && !prepared.approvalRequired {
		return false
	}
	if prepared.ruleAllowed {
		return false
	}
	if prepared.commandDecision == CommandRuleAsk {
		return true
	}
	if service.permission == nil {
		return true
	}
	switch service.permission.Mode {
	case PermissionFull:
		return false
	case PermissionWorkspace:
		// Only the built-in workspace executor can set this internal marker.
		return prepared.Effect == EffectModify && !prepared.workspaceFile
	default:
		return true
	}
}
