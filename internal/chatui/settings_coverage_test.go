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

type settingsContractGroup struct {
	ID, Owner, Scope, Entry, Save, Read, Revision, Effect string
	Telegram, Why, Sensitive, Declared                    string
	Fields, Operations, Extras                            []string
}

func TestSettingsContractCoverage(t *testing.T) {
	paths := map[string]bool{}
	configFieldPaths(reflect.TypeFor[harness.Config](), "", paths)
	var mapping frontendSettingsMap
	raw, err := os.ReadFile("frontend/settings-map.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &mapping); err != nil {
		t.Fatal(err)
	}
	for field := range mapping.Fields {
		paths[field] = true
	}
	ops := map[string]bool{}
	for _, op := range harness.ControlOperationCatalog() {
		ops[op.Kind] = true
	}
	for op := range mapping.Operations {
		ops[op] = true
	}
	var contract struct{ Groups []settingsContractGroup }
	raw, err = os.ReadFile("frontend/settings-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contract); err != nil {
		t.Fatal(err)
	}
	groups, extras := map[string]bool{}, map[string]bool{}
	for _, row := range contract.Groups {
		if groups[row.ID] {
			t.Errorf("duplicated settings contract group %s", row.ID)
		}
		groups[row.ID] = true
		for field, value := range map[string]string{"id": row.ID, "owner": row.Owner, "scope": row.Scope, "entry": row.Entry,
			"save": row.Save, "read": row.Read, "revision": row.Revision, "effect": row.Effect} {
			if strings.TrimSpace(value) == "" {
				t.Errorf("%s: missing %s", row.ID, field)
			}
		}
		if len(row.Fields)+len(row.Operations)+len(row.Extras) == 0 {
			t.Errorf("%s: empty settings contract", row.ID)
		}
		if (row.Entry == "read-only" || row.Save == "none") && (row.Why == "" || row.Save != "none" || len(row.Operations) != 0) {
			t.Errorf("%s: read-only entry must explain why and specify no write operation", row.ID)
		}
		if strings.Contains(row.Entry, "dedicated") && (row.Sensitive == "" || row.Why == "") {
			t.Errorf("%s: dedicated entry must explain its separate save path and secret handling", row.ID)
		}
		for _, field := range row.Fields {
			if !paths[field] {
				t.Errorf("%s: duplicate or unknown field %s", row.ID, field)
			}
			delete(paths, field)
		}
		for _, op := range row.Operations {
			if !ops[op] {
				t.Errorf("%s: duplicate or unknown operation %s", row.ID, op)
			}
			delete(ops, op)
			rule := harness.SettingsEntryRules(op)
			want := rule.TelegramDecide
			if want == "" && rule.TelegramPropose {
				want = "handoff"
			}
			if row.Telegram != want {
				t.Errorf("%s: TG policy %q does not match Core %q", op, row.Telegram, want)
			}
		}
		for _, extra := range row.Extras {
			if extra == "" || extras[extra] {
				t.Errorf("%s: invalid or duplicate dedicated setting %q", row.ID, extra)
			}
			extras[extra] = true
		}
		if (row.ID == "extension-state" || row.ID == "connector-state") && row.Declared == "" {
			t.Errorf("%s: missing module declaration entry point", row.ID)
		}
	}
	for path := range paths {
		t.Errorf("settings contract missing field %s", path)
	}
	for op := range ops {
		t.Errorf("settings contract missing operation %s", op)
	}
	for _, extra := range []string{"account.identity", "account.password", "account.sessions", "connector.credentials",
		"telegram.group_settings", "telegram.confirmers", "telegram.admin_binding", "control.result", "control.revert"} {
		if !extras[extra] {
			t.Errorf("settings contract missing dedicated entry %s", extra)
		}
	}
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
			case "web_search":
				page = "search"
			case "delivery":
				page = "delivery"
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
			case "max_context_bytes", "context_byte_limit_mode", "max_output_tokens", "model_retries", "task_summary_bytes", "auto_compact_percent", "automatic_compaction", "summary_target_mode":
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
			case "search":
				page = "search"
			case "delivery":
				page = "delivery"
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
			if entry.Kind == harness.ControlSetStickerSets {
				page = "conversation"
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
