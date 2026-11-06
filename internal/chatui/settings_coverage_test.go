package chatui

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/harness"
)

type frontendSettingsMap struct {
	Fields     map[string]string `json:"config_fields"`
	Operations map[string]string `json:"control_operations"`
}

func configFieldPaths(typ reflect.Type, prefix string, result map[string]bool) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Map && typ.Elem().Kind() == reflect.Struct {
		configFieldPaths(typ.Elem(), prefix+".$id", result)
		return
	}
	if typ.Kind() != reflect.Struct {
		result[prefix] = true
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		configFieldPaths(field.Type, path, result)
	}
}

// Explicitly updating this small map is a review action. Ordinary tests fail
// when Core adds an adjustable field/operation without a frontend destination.
func TestFrontendSettingsCoverage(t *testing.T) {
	paths := map[string]bool{}
	configFieldPaths(reflect.TypeFor[harness.Config](), "", paths)
	catalog := harness.ControlOperationCatalog()
	const path = "frontend/settings-map.json"
	var mapping frontendSettingsMap
	if os.Getenv("FREEAGENT_UPDATE_FRONTEND_SETTINGS") == "1" {
		mapping = frontendSettingsMap{Fields: map[string]string{}, Operations: map[string]string{}}
		for field := range paths {
			page := ""
			root := strings.Split(field, ".")[0]
			switch root {
			case "version", "tool_generation":
				page = "metadata"
			case "models", "default_model", "providers":
				page = "providers"
			case "default_selection", "auto", "http", "collaboration", "memory", "system_prompt", "instructions", "history_page_size", "max_tool_result_bytes":
				page = "advanced"
			case "workspace":
				page = "general"
			case "public_web":
				page = "public"
			case "attachments":
				page = "attachments"
			case "server":
				page = "data"
			case "max_context_bytes", "max_output_tokens", "model_retries", "task_summary_bytes", "auto_compact_percent":
				page = "context"
			case "commands":
				page = "commands"
			case "tools":
				page = "tools"
			case "computer_files", "computer_roots":
				page = "files"
			case "execution":
				page = "permissions"
			case "mcpServers":
				page = "mcp"
			case "skills":
				page = "skills"
			case "extensions":
				page = "extensions"
			case "channels":
				page = "channels"
			}

			if strings.HasPrefix(field, "execution.") {
				page = "files"
				if field == "execution.permission_mode" {
					page = "permissions"
				}
				if field == "execution.sandbox_mode" || strings.Contains(field, "network_policy") {
					page = "advanced"
				}
			}
			if strings.HasPrefix(field, "extensions.max_") {
				page = "advanced"
			}
			mapping.Fields[field] = page
		}
		for _, entry := range catalog {
			page := ""
			switch strings.Split(entry.Kind, ".")[0] {
			case "execution":
				page = "permissions"
			case "tools":
				page = "tools"
			case "commands":
				page = "commands"
			case "config":
				page = "general"
			case "model", "providers", "keys":
				page = "providers"
			case "server":
				page = "data"
			case "auto", "prompt", "http", "runtime", "collaboration":
				page = "advanced"
			case "channels", "telegram":
				page = "channels"
			case "memory", "knowledge":
				page = "memory"
			case "skills":
				page = "skills"
			case "extensions":
				page = "extensions"
			case "mcp":
				page = "mcp"
			case "control":
				page = "diagnosis"
			case "conversation":
				page = "conversation"
			case "space", "person":
				page = "spaces"
			case "attachments":
				page = "attachments"
			case "public", "public_web":
				page = "public"
			}
			if entry.Kind == harness.ControlPersonPermission {
				page = "channels"
			}
			if entry.Kind == harness.ControlSetMemoryPolicy || entry.Kind == harness.ControlSetSandbox || entry.Kind == harness.ControlSetNetwork {
				page = "advanced"
			}
			if entry.Kind == harness.ControlSetFileScope {
				page = "files"
			}
			mapping.Operations[entry.Kind] = page
		}
		raw, err := json.MarshalIndent(mapping, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &mapping) != nil {
		t.Fatal("frontend settings map missing or invalid", err)
	}
	if len(paths) != len(mapping.Fields) || len(catalog) != len(mapping.Operations) {
		t.Error("Core settings catalog changed: review the frontend field/operation mapping")
	}
	for path := range paths {
		if mapping.Fields[path] == "" {
			t.Errorf("no frontend destination for config %s", path)
		}
	}
	for _, entry := range catalog {
		if mapping.Operations[entry.Kind] == "" {
			t.Errorf("no frontend destination for control %s", entry.Kind)
		}
	}
}
