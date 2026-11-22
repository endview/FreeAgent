package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

type LocalSettingsApproval struct {
	OperationID    string
	ConversationID string
}

func (e *LocalSettingsApproval) Error() string {
	return fmt.Sprintf("settings proposal %s requires trusted local approval", e.OperationID)
}

func (e *LocalSettingsApproval) Unwrap() error { return ErrPublicLocalConfirmation }

func bindPublicSettings(ctx context.Context, record *controlRecord) {
	if entry, remote := PublicEntryFrom(ctx); remote {
		record.Operation.RemotePrincipal = entry.Principal
	}
	if admin, ok := publicAdministratorFrom(ctx); ok {
		record.Operation.RequestedBy = "account:" + admin.id
		record.Operation.RequestEntry = "public_web"
	}
}

func recordSettingsDecision(ctx context.Context, record *controlRecord, principal string) {
	record.Operation.DecidedBy = principal
	if admin, ok := publicAdministratorFrom(ctx); ok {
		record.Operation.DecidedBy = "account:" + admin.id
		record.Operation.DecisionEntry = "public_web"
	} else if principal == "cli" || principal == "chat-ui" {
		record.Operation.DecisionEntry = "local"
	}
}

func (service *ControlService) beginPublicControlDecision(ctx context.Context, record controlRecord) (func(), error) {
	if _, remote := PublicEntryFrom(ctx); !remote {
		return func() {}, nil
	}
	if err := service.validatePublicOperation(ctx, record); err != nil {
		return nil, err
	}
	return BeginPublicSettingsDecision(ctx, needsRecentPassword(record, service.operations))
}

func needsRecentPassword(record controlRecord, operations map[string]controlRecord) bool {
	switch record.Operation.Kind {
	case ControlSetKeyProfile, ControlRemoveKey, ControlSetPublicWeb, ControlSetChannelPolicy, ControlTelegramAccess,
		ControlSetSpacePermission, ControlPersonPermission, ControlSetPermission, ControlSetExecutionProfile, ControlSetSandbox,
		ControlSetFileScope, ControlSetTool, ControlSetCommandPolicy, ControlSetHTTPPolicy, ControlSetNetwork,
		ControlSetMCPProfile, ControlRemoveMCP, ControlSetMCPPolicy, ControlSetSkillPolicy, ControlMoveConversation, ControlSetDeliveryPolicy:
		return true
	case ControlSetExtensionPolicy:
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(record.Arguments, &fields)
		return fields["settings"] == nil || len(fields) != 2
	case ControlRevert:
		if original, ok := operations[record.Operation.RevertOf]; ok && original.Operation.Kind != ControlRevert {
			return needsRecentPassword(original, operations)
		}
		return true
	default:
		return false
	}
}

// A remote entry may narrow existing policy. For mixed or unprovable
// changes, require the local decision before saving anything.
func hostSettingsWiden(record controlRecord, config Config) bool {
	before, after := record.Before, record.After
	kind := record.Operation.Kind
	switch kind {
	case ControlRestartMCP, ControlRestartExtension, ControlImportExtension, ControlRemoveExtension, ControlClearExtensionData,
		ControlInitExtensionStorage, ControlImportSkill, ControlSetWorkspace:
		return true
	case ControlSetPublicWeb:
		var a, b PublicWebConfig
		if json.Unmarshal(before["config.public_web"], &a) != nil || json.Unmarshal(after["config.public_web"], &b) != nil {
			return true
		}
		a, b = a.Effective(), b.Effective()
		return !a.Enabled && b.Enabled || a.Origin != b.Origin || a.Socket != b.Socket || a.SocketGroup != b.SocketGroup ||
			permissionRank(b.MaxPermission) > permissionRank(a.MaxPermission)
	case ControlSetPermission:
		var a, b PermissionMode
		key := "config." + kind
		return json.Unmarshal(before[key], &a) != nil || json.Unmarshal(after[key], &b) != nil || permissionRank(b) > permissionRank(a)
	case ControlSetTool:
		var a, b BuiltinToolSettings
		key := "config." + kind
		return json.Unmarshal(before[key], &a) != nil || json.Unmarshal(after[key], &b) != nil ||
			!a.CommandRun && b.CommandRun || !a.File && b.File || !a.Network && b.Network
	case ControlSetSpacePermission, ControlPersonPermission:
		var current struct {
			Permissions map[string]PermissionSelection
			Person      *PersonPermission
		}
		var args SpaceControlArguments
		if json.Unmarshal(before["spaces"], &current) != nil || json.Unmarshal(record.Arguments, &args) != nil {
			return true
		}
		if kind == ControlPersonPermission {
			return current.Person == nil || permissionRank(args.Mode) > permissionRank(current.Person.Mode)
		}
		prev, ok := current.Permissions[args.SpaceID]
		next := args.Selection
		if !ok || next == nil {
			return true
		}
		a, b := prev, *next
		if permissionRank(b.Mode) > permissionRank(a.Mode) || !a.OwnerPrivate && b.OwnerPrivate {
			return true
		}
		// Space permissions written before profile normalization can omit
		// these fields. Comparing their canonical values avoids treating
		// normalization of an unchanged selection as a new grant.
		if a.FileScope == "" {
			a.FileScope = FileScopeWorkspace
			if len(a.ComputerRoots)+len(a.AdditionalRoots) != 0 {
				a.FileScope = FileScopeSelectedRoots
			}
		}
		a.ComputerFiles = a.ComputerFiles || a.FileScope != FileScopeWorkspace
		// Ignore only the mode, audit bindings, and removal of Owner access.
		a.Mode, a.OwnerPrivate, a.ProfileGeneration = b.Mode, b.OwnerPrivate, b.ProfileGeneration
		return !bytes.Equal(mustJSON(a), mustJSON(b))
	case ControlSetFileScope, ControlSetExecutionProfile:
		key := "config." + kind
		var a, b struct {
			Mode         PermissionMode `json:"mode"`
			Scope        FileScope      `json:"scope"`
			Roots        []string       `json:"roots"`
			Blocked      []string       `json:"blocked"`
			AllowedRoots []string       `json:"allowed_roots"`
			BlockedPaths []string       `json:"blocked_paths"`
			Network      NetworkPolicy  `json:"network_policy"`
		}
		if json.Unmarshal(before[key], &a) != nil || json.Unmarshal(after[key], &b) != nil {
			return true
		}
		return (a.Scope != b.Scope && b.Scope != FileScopeWorkspace) ||
			!subsetOf(append(b.Roots, b.AllowedRoots...), append(a.Roots, a.AllowedRoots...)) ||
			!subsetOf(append(a.Blocked, a.BlockedPaths...), append(b.Blocked, b.BlockedPaths...)) ||
			permissionRank(b.Mode) > permissionRank(a.Mode) || !reflect.DeepEqual(a.Network, b.Network)
	case ControlSetCommandPolicy:
		var a, b CommandConfig
		key := "config." + kind
		if json.Unmarshal(before[key], &a) != nil || json.Unmarshal(after[key], &b) != nil || !a.Enabled && b.Enabled {
			return true
		}
		a.Enabled, a.TimeoutSecs, a.MaxOutput = b.Enabled, b.TimeoutSecs, b.MaxOutput
		return !reflect.DeepEqual(a, b)
	case ControlSetHTTPPolicy, ControlSetNetwork, ControlSetSandbox:
		for key, value := range after {
			if !bytes.Equal(before[key], value) {
				return true
			}
		}
	case ControlSetSkillPolicy, ControlSetExtensionPolicy, ControlSetMCPPolicy:
		var a struct {
			ID                string
			Enabled           *bool
			Required          *bool
			Trusted           *bool
			TrustReadOnlyHint *bool             `json:"trust_read_only_hint"`
			EnvFrom           map[string]string `json:"env_from"`
			HiddenTools       []string          `json:"hidden_tools"`
		}
		if json.Unmarshal(record.Arguments, &a) != nil || a.EnvFrom != nil || a.Trusted != nil && *a.Trusted ||
			a.Required != nil && *a.Required || a.TrustReadOnlyHint != nil && *a.TrustReadOnlyHint {
			return true
		}
		if a.Enabled == nil || !*a.Enabled {
			return kind == ControlSetMCPPolicy && a.HiddenTools != nil && !subsetOf(config.MCPServers[a.ID].HiddenTools, a.HiddenTools)
		}
		switch kind {
		case ControlSetSkillPolicy:
			return !config.Skills.Enabled[a.ID]
		case ControlSetExtensionPolicy:
			return !config.Extensions.Enabled[a.ID]
		default:
			return !config.MCPServers[a.ID].Enabled || a.HiddenTools != nil && !subsetOf(config.MCPServers[a.ID].HiddenTools, a.HiddenTools)
		}
	case ControlRevert:
		for key := range after {
			if strings.HasPrefix(key, "config.mcp:") || strings.HasPrefix(key, "config.skill") || strings.HasPrefix(key, "config.extension") ||
				strings.HasPrefix(key, "config.execution.") || strings.HasPrefix(key, "config.tools.") || strings.HasPrefix(key, "config.commands.") ||
				key == "config.workspace" || key == "config.http" || key == "config.public_web" || key == "spaces" {
				return true
			}
		}
	}
	return false
}

func subsetOf(values, scope []string) bool {
	for _, value := range values {
		if !slices.Contains(scope, value) {
			return false
		}
	}
	return true
}

func mcpProfileNeedsLocal(before, after MCPServerConfig, exists bool) bool {
	return !exists || !before.Enabled && after.Enabled || !before.TrustReadOnlyHint && after.TrustReadOnlyHint ||
		before.Command != after.Command || before.Cwd != after.Cwd || !slices.Equal(before.Args, after.Args) ||
		!reflect.DeepEqual(before.Env, after.Env) || !reflect.DeepEqual(before.EnvFrom, after.EnvFrom) || !subsetOf(before.HiddenTools, after.HiddenTools)
}
