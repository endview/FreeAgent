package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Only existing profile metadata is model-writable. Creating/replacing a
// credential or changing its destination still uses the dedicated secret UI.
type safeKeyMetadataArguments struct {
	ID   string  `json:"id"`
	Name *string `json:"name,omitempty"`
	Note *string `json:"note,omitempty"`
}
type safeMCPProfileArguments struct {
	ID     string `json:"id"`
	Config struct {
		Enabled           bool              `json:"enabled"`
		Command           string            `json:"command"`
		Args              []string          `json:"args,omitempty"`
		Cwd               string            `json:"cwd"`
		EnvFrom           map[string]string `json:"envFrom,omitempty"`
		HiddenTools       []string          `json:"hiddenTools,omitempty"`
		TrustReadOnlyHint bool              `json:"trustReadOnlyHint"`
	} `json:"config"`
}

func isSafeProfileControl(kind string) bool {
	return kind == ControlSetKeyProfile || kind == ControlRemoveKey || kind == ControlProbeKey || kind == ControlSetMCPProfile || kind == ControlRemoveMCP
}

func (service *ControlService) buildSafeProfileControl(ctx context.Context, record *controlRecord) error {
	kind := record.Operation.Kind
	record.Operation.RevertClass = "none"
	if kind == ControlSetMCPProfile || kind == ControlRemoveMCP {
		var args safeMCPProfileArguments
		if err := decodeControlJSON(record.Arguments, &args); err != nil || !validMCPControlID(args.ID) {
			return errors.New("harness: valid MCP id and non-secret profile required")
		}
		config := cloneConfig(service.core.currentConfig())
		before, exists := config.MCPServers[args.ID]
		if !exists && kind == ControlRemoveMCP {
			return ErrToolDisabled
		}
		after := MCPServerConfig{Enabled: args.Config.Enabled, Command: args.Config.Command, Args: args.Config.Args, Cwd: args.Config.Cwd,
			EnvFrom: args.Config.EnvFrom, HiddenTools: args.Config.HiddenTools, TrustReadOnlyHint: args.Config.TrustReadOnlyHint, Env: cloneStringMap(before.Env)}
		if kind == ControlRemoveMCP {
			delete(config.MCPServers, args.ID)
		} else {
			if config.MCPServers == nil {
				config.MCPServers = map[string]MCPServerConfig{}
			}
			config.MCPServers[args.ID] = after
		}
		if err := config.Validate(); err != nil {
			return err
		}
		resource := "config.mcp:" + args.ID
		record.Operation.ResourceIDs = []string{resource}
		record.Before = map[string]json.RawMessage{resource: mustJSON(mcpControlValue(args.ID, before, exists))}
		record.After = map[string]json.RawMessage{resource: mustJSON(mcpControlValue(args.ID, after, kind != ControlRemoveMCP))}
		record.Operation.Preview = fmt.Sprintf("%s %s. Existing secret environment values are preserved and never shown.", kind, args.ID)
		if kind == ControlSetMCPProfile {
			record.Operation.Preview += "\nNon-secret profile: " + string(mustJSON(args.Config))
			record.pendingMCP = &after
		}
		record.Arguments = mustJSON(map[string]any{"id": args.ID, "remove": kind == ControlRemoveMCP})
		return nil
	}
	if service.core.Keys == nil {
		return ErrKeyNotFound
	}
	var args struct {
		safeKeyMetadataArguments
		DisableModels bool   `json:"disable_models,omitempty"`
		NewDefault    string `json:"new_default,omitempty"`
	}
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	profile, err := service.core.Keys.Find(args.ID)
	if err != nil {
		return err
	}
	views, err := service.core.Keys.List()
	if err != nil {
		return err
	}
	resource := "keys.profiles"
	record.Operation.ResourceIDs = []string{resource}
	record.Before = map[string]json.RawMessage{resource: mustJSON(views)}
	switch kind {
	case ControlRemoveKey:
		users := keyModelUsers(service.core.currentConfig(), profile.ID)
		if len(users) > 0 && !args.DisableModels {
			return ErrControlBusy
		}
		candidate := cloneConfig(service.core.currentConfig())
		if len(users) > 0 {
			if _, err := RemoveModels(&candidate, users, args.NewDefault); err != nil {
				return err
			}
		}
		record.Arguments = mustJSON(map[string]any{"id": profile.ID, "remove": true, "disable_models": args.DisableModels, "new_default": args.NewDefault})
		record.After = map[string]json.RawMessage{resource: mustJSON(removeKeyView(views, profile.ID))}
		record.Operation.Preview = fmt.Sprintf("Remove Provider credential %s. Remove referencing models: %t. Models: %v.", profile.Name, args.DisableModels, users)
		if err := service.bindKeyRemovalModels(record, profile.ID, args.DisableModels, args.NewDefault); err != nil {
			return err
		}
	case ControlProbeKey:
		record.Arguments = mustJSON(map[string]string{"id": profile.ID})
		record.Operation.Preview = "Test the existing saved Provider credential " + profile.Name + "; the secret stays inside Core."
	case ControlSetKeyProfile:
		if args.Name == nil && args.Note == nil {
			return errors.New("harness: provide name or note; secret and endpoint changes use the Provider form")
		}
		if args.Name != nil {
			profile.Name = *args.Name
		}
		if args.Note != nil {
			profile.Note = *args.Note
		}
		if len(profile.Name) == 0 || len(profile.Name) > 256 || len(profile.Note) > 4096 {
			return errors.New("harness: invalid Provider metadata")
		}
		record.Arguments = mustJSON(map[string]any{"id": profile.ID, "name": profile.Name, "note": profile.Note, "base_url": profile.BaseURL, "protocol": profile.Protocol, "source": profile.Source, "env_name": profile.EnvName})
		for i := range views {
			if views[i].ID == profile.ID {
				views[i] = profile
			}
		}
		record.After = map[string]json.RawMessage{resource: mustJSON(views)}
		record.Operation.Preview = "Update saved Provider name/note; credentials and destination remain unchanged."
	}
	return nil
}

// Bind deletion to the registry reviewed by the user, including references
// created through other operation families. This uses the existing journal.
func (service *ControlService) bindKeyRemovalModels(record *controlRecord, id string, remove bool, newDefault string) error {
	before := service.core.currentConfig()
	after := cloneConfig(before)
	users := keyModelUsers(before, id)
	if remove && len(users) > 0 {
		if _, err := RemoveModels(&after, users, newDefault); err != nil {
			return err
		}
	}
	resources := []string{"config.models"}
	if remove {
		for _, id := range users {
			resources = append(resources, "config.model:"+id)
		}
	}
	for _, resource := range resources {
		// An unused credential has a read dependency on model references, but
		// does not modify the model registry or require stopping unrelated Runs.
		if remove && len(users) > 0 {
			record.Operation.ResourceIDs = append(record.Operation.ResourceIDs, resource)
		}
		record.Operation.ExpectedRevisions[resource] = service.revisionLocked(resource)
		record.Before[resource] = mustJSON(configResourceValue(before, resource))
		record.After[resource] = mustJSON(configResourceValue(after, resource))
	}
	return nil
}
