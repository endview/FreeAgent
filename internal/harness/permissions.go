package harness

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

type PermissionMode string

const (
	PermissionNone      PermissionMode = "none"
	PermissionAsk       PermissionMode = "ask"
	PermissionWorkspace PermissionMode = "workspace"
	PermissionFull      PermissionMode = "full"
)

var ErrFullAccessConfirmation = errors.New("harness: Full access requires explicit confirmation for this conversation or turn")

func (mode PermissionMode) Validate() error {
	switch mode {
	case PermissionNone, PermissionAsk, PermissionWorkspace, PermissionFull:
		return nil
	default:
		return errors.New("harness: permission must be none, ask, workspace or full")
	}
}

// This is part of the existing run snapshot, not another execution object.
type PermissionSelection struct {
	SpaceID           string         `json:"space_id,omitempty"`
	PersonID          string         `json:"person_id,omitempty"`
	Entry             string         `json:"entry,omitempty"`
	SpaceLimit        PermissionMode `json:"space_limit,omitempty"`
	PersonLimit       PermissionMode `json:"person_limit,omitempty"`
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
	if len(selection.SpaceID) > 128 || strings.ContainsAny(selection.SpaceID, "\x00\r\n") {
		return ErrInvalidSpace
	}
	if selection.PersonID != "" && !validPersonID(selection.PersonID) {
		return ErrPermissionDenied
	}
	if selection.SpaceLimit != "" && selection.SpaceLimit.Validate() != nil {
		return ErrPermissionDenied
	}
	if selection.PersonLimit != "" && selection.PersonLimit.Validate() != nil {
		return ErrPermissionDenied
	}
	if selection.Source != "turn" && selection.Source != "space" && selection.Source != "default" {
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
	chat, err := store.GetConversation(ctx, conversationID)
	if err != nil {
		return PermissionSelection{}, err
	}
	return store.spacePermission(ctx, chat.SpaceID)
}

func (tasks *TaskService) SelectedPermission(ctx context.Context, conversationID string) (PermissionSelection, error) {
	if ctx == nil || tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return PermissionSelection{}, errors.New("harness: task service and context are required")
	}
	selection, err := tasks.core.Store.selectedPermission(ctx, conversationID)
	if err != nil {
		return PermissionSelection{}, err
	}
	selection.ProfileGeneration = effectiveToolGeneration(tasks.core.currentConfig())
	return selection, nil
}

// EffectiveExecutionProfile returns the profile that the next Run would
// freeze. It is a read-only projection; active Runs keep their own snapshot.
func (tasks *TaskService) EffectiveExecutionProfile(ctx context.Context, conversationID string) (ExecutionProfile, PermissionSelection, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return ExecutionProfile{}, PermissionSelection{}, errors.New("harness: task service and context are required")
	}
	selection, err := tasks.core.Store.spacePermission(ctx, LocalSpaceID)
	if err != nil {
		return ExecutionProfile{}, PermissionSelection{}, err
	}
	if strings.TrimSpace(conversationID) != "" {
		stored, err := tasks.SelectedPermission(ctx, conversationID)
		if err != nil {
			return ExecutionProfile{}, PermissionSelection{}, err
		}
		selection = stored
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
	next.Mode, next.Source = mode, "space"
	next.ProfileGeneration = effectiveToolGeneration(tasks.Config())
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	return tasks.persistSpacePermission(ctx, next.SpaceID, raw)
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
	raw, err := json.Marshal(PermissionSelection{Mode: mode, Source: "space", SpaceID: selected.SpaceID, ComputerFiles: computerFiles,
		ComputerRoots: append([]string(nil), roots...), FileScope: map[bool]FileScope{false: FileScopeWorkspace, true: FileScopeSelectedRoots}[computerFiles],
		ProfileGeneration: effectiveToolGeneration(config)})
	if err != nil {
		return err
	}
	return tasks.persistSpacePermission(ctx, selected.SpaceID, raw)
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
	selection.SpaceID = previous.SpaceID
	raw, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	return tasks.persistSpacePermission(ctx, previous.SpaceID, raw)
}

func (service *Service) resolvePermission(ctx context.Context, conversationID string, mode PermissionMode, confirmed bool) error {
	selected, err := service.Store.selectedPermission(ctx, conversationID)
	if err != nil {
		return err
	}
	selected.SpaceLimit = selected.Mode
	space, err := service.Store.GetSpace(ctx, selected.SpaceID)
	if err != nil {
		return err
	}
	selected.Entry = space.Source
	if mode != "" {
		if err := mode.Validate(); err != nil {
			return err
		}
		if mode == PermissionFull && selected.Mode != PermissionFull && !confirmed {
			return ErrFullAccessConfirmation
		}
		// A local per-turn choice can narrow the space grant. Increasing it
		// requires changing the space explicitly through the control surface.
		selected.Mode, selected.Source = lowerPermission(selected.Mode, mode), "turn"
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
		selection.Source = "space"
	}
	selection.Source = "space"
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
	if prepared.userConfirmation {
		return true
	}
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
