package harness

import (
	"context"
	"time"

	"github.com/endview/freeagent/internal/winlaunch"
)

// CommandReadiness describes the execution boundary, not whether an arbitrary
// executable or argument list will pass its individual checks.
type CommandReadiness struct {
	Enabled       bool                 `json:"enabled"`
	Backend       CommandExecutionMode `json:"backend"`
	Available     bool                 `json:"available"`
	Status        string               `json:"status"`
	Reason        string               `json:"reason"`
	UnmatchedRule string               `json:"unmatched_rule"`
}

func commandReadiness(profile ExecutionProfile, tools BuiltinToolSettings) CommandReadiness {
	r := CommandReadiness{Enabled: tools.CommandRun && profile.CommandPolicy.Enabled,
		Backend: profile.CommandPolicy.Execution, Status: "ready", UnmatchedRule: "use_permission_mode"}
	r.Available = r.Backend == CommandExecutionHostExplicit || winlaunch.SandboxSupported()
	switch {
	case profile.PermissionMode == PermissionNone:
		r.Status, r.Reason = "permission_denied", "None permits chat only"
	case !r.Enabled:
		r.Status, r.Reason = "disabled", "enable command_run for a new Run"
	case r.Backend == CommandExecutionHostExplicit && profile.PermissionMode != PermissionFull:
		r.Status, r.Reason = "permission_denied", "host execution requires Full access"
	case !r.Available:
		r.Status, r.Reason = "backend_unavailable", "the required OS sandbox is unavailable; host fallback is forbidden"
	default:
		r.Reason = "backend available; each command still requires executable, path, rule and current-authority checks"
	}
	return r
}

// RuntimeFacts contains no paths or credentials and can be shared with a model.
// Saved defaults and a frozen Run are intentionally separate observations.
type RuntimeFacts struct {
	ObservedAt      string           `json:"observed_at"`
	RunID           string           `json:"run_id,omitempty"`
	Entry           string           `json:"entry"`
	PersonID        string           `json:"person_id,omitempty"`
	SpaceID         string           `json:"space_id,omitempty"`
	Permission      PermissionMode   `json:"permission"`
	SpaceLimit      PermissionMode   `json:"space_limit,omitempty"`
	PersonLimit     PermissionMode   `json:"person_limit,omitempty"`
	RemoteLimit     PermissionMode   `json:"remote_limit,omitempty"`
	Sandbox         SandboxMode      `json:"sandbox"`
	FileScope       FileScope        `json:"file_scope"`
	Network         NetworkMode      `json:"command_network"`
	Generation      string           `json:"generation"`
	Commands        CommandReadiness `json:"commands"`
	DispatchBlocked string           `json:"dispatch_blocked,omitempty"`
	Tools           []string         `json:"tools,omitempty"`
}

func factsForProfile(profile ExecutionProfile, tools BuiltinToolSettings, selection PermissionSelection) RuntimeFacts {
	entry := selection.Entry
	if selection.PersonID == "" {
		entry = "local_administrator"
		if selection.RemoteLimit != "" {
			entry = "web_administrator"
		}
	}
	return RuntimeFacts{ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Entry: entry,
		PersonID: selection.PersonID, SpaceID: selection.SpaceID, Permission: profile.PermissionMode,
		SpaceLimit: selection.SpaceLimit, PersonLimit: selection.PersonLimit, RemoteLimit: selection.RemoteLimit,
		Sandbox: profile.SandboxMode, FileScope: profile.FileScope, Network: profile.NetworkPolicy.Mode,
		Generation: profile.Generation, Commands: commandReadiness(profile, tools)}
}

func DefaultRuntimeFacts(config Config) (RuntimeFacts, error) {
	profile, err := executionProfileForConfig(config)
	if err != nil {
		return RuntimeFacts{}, err
	}
	return factsForProfile(profile, effectiveBuiltinToolSettings(config), PermissionSelection{Mode: profile.PermissionMode, Source: "default"}), nil
}

func (tasks *TaskService) RuntimeFacts(ctx context.Context, chat, runID string) (RuntimeFacts, error) {
	if runID == "" {
		profile, selection, err := tasks.EffectiveExecutionProfile(ctx, chat)
		if err != nil {
			return RuntimeFacts{}, err
		}
		if chat != "" {
			core := tasks.core.runCopy()
			if err := core.resolvePermission(ctx, chat, "", false); err != nil {
				return RuntimeFacts{}, err
			}
			selection = *core.permission
			profile, err = buildExecutionProfile(tasks.Config(), selection)
			if err != nil {
				return RuntimeFacts{}, err
			}
		}
		return factsForProfile(profile, effectiveBuiltinToolSettings(tasks.Config()), selection), nil
	}
	run, err := tasks.core.Store.GetRun(ctx, runID)
	if err != nil {
		return RuntimeFacts{}, err
	}
	snapshot, err := decodeRunSnapshot(run.ConfigJSON)
	if err != nil {
		return RuntimeFacts{}, err
	}
	selection := PermissionSelection{}
	if snapshot.Permission != nil {
		selection = *snapshot.Permission
	}
	facts := factsForProfile(snapshot.ExecutionProfile, snapshot.EnabledTools, selection)
	facts.RunID = runID
	for _, tool := range snapshot.Tools {
		facts.Tools = append(facts.Tools, tool.Name)
	}
	core := tasks.core.runCopy()
	core.permission, core.channel = snapshot.Permission, snapshot.Channel
	if err := core.checkCurrentPermission(ctx, run); err != nil {
		facts.DispatchBlocked = safeError(err)
	}
	if facts.DispatchBlocked == "" && (run.Status == "running" || run.Status == "waiting_confirmation" || run.Status == "stopped" || run.Status == "limited") {
		if _, err := core.forRun(run); err != nil {
			facts.DispatchBlocked = safeError(err)
		}
	}
	return facts, nil
}
