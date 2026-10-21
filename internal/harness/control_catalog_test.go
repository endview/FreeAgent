package harness

import (
	"reflect"
	"strings"
	"testing"
)

func TestControlCatalogCoversCurrentMutableFamilies(t *testing.T) {
	entries := ControlOperationCatalog()
	seenKinds := make(map[string]bool, len(entries))
	seenFamilies := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Kind == "" || entry.ResourceFamily == "" || entry.Description == "" {
			t.Fatalf("incomplete catalog entry: %+v", entry)
		}
		if seenKinds[entry.Kind] {
			t.Fatalf("duplicate catalog operation %q", entry.Kind)
		}
		seenKinds[entry.Kind] = true
		seenFamilies[entry.ResourceFamily] = true
	}
	for _, family := range []string{
		"config.execution", "config.tools", "config.commands", "config.workspace", "config.models",
		"config.auto", "config.http", "config.memory", "config.prompt", "config.collaboration", "config.limits",
		"config.skills", "config.extensions", "config.mcp", "conversation.model",
		"conversation.permission", "conversation.metadata", "keys.profile",
	} {
		if !seenFamilies[family] {
			t.Fatalf("persistent mutable family %q has no typed control operation", family)
		}
	}
}

func TestControlCatalogCoversPersistentConfigFields(t *testing.T) {
	seen := make(map[string]bool)
	for _, entry := range ControlOperationCatalog() {
		seen[entry.ResourceFamily] = true
	}
	fieldFamilies := map[string]string{
		"models": "config.models", "default_model": "config.models", "default_selection": "config.models",
		"auto": "config.auto", "workspace": "config.workspace",
		"max_steps": "config.limits", "max_messages": "config.limits", "max_context_bytes": "config.limits",
		"max_output_tokens": "config.limits", "max_run_seconds": "config.limits", "max_tool_result_bytes": "config.limits",
		"task_summary_bytes": "config.limits", "system_prompt": "config.prompt", "instructions": "config.prompt",
		"mcpServers": "config.mcp", "skills": "config.skills", "extensions": "config.extensions",
		"tools": "config.tools", "commands": "config.commands", "execution": "config.execution",
		"computer_files": "config.execution", "computer_roots": "config.execution", "http": "config.http",
		"collaboration": "config.collaboration", "memory": "config.memory",
	}
	typ := reflect.TypeOf(Config{})
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		if field.PkgPath != "" { // unexported derived cache
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "version" || name == "tool_generation" {
			continue
		}
		family, ok := fieldFamilies[name]
		if !ok {
			t.Fatalf("persistent Config field %q has no catalog family", name)
		}
		if !seen[family] {
			t.Fatalf("persistent Config field %q maps to uncovered family %q", name, family)
		}
	}
}

func TestControlCatalogCoversNestedPersistentConfigFields(t *testing.T) {
	seen := make(map[string]bool)
	for _, entry := range ControlOperationCatalog() {
		seen[entry.ResourceFamily] = true
	}
	types := map[reflect.Type]string{
		reflect.TypeOf(ModelConfig{}):           "config.models",
		reflect.TypeOf(ModelCapabilities{}):     "config.models",
		reflect.TypeOf(AutoConfig{}):            "config.auto",
		reflect.TypeOf(CommandConfig{}):         "config.commands",
		reflect.TypeOf(CommandRule{}):           "config.commands",
		reflect.TypeOf(HTTPConfig{}):            "config.http",
		reflect.TypeOf(MemoryConfig{}):          "config.memory",
		reflect.TypeOf(ExecutionSettings{}):     "config.execution",
		reflect.TypeOf(NetworkPolicy{}):         "config.execution",
		reflect.TypeOf(BuiltinToolSettings{}):   "config.tools",
		reflect.TypeOf(MCPServerConfig{}):       "config.mcp",
		reflect.TypeOf(SkillsConfig{}):          "config.skills",
		reflect.TypeOf(ExtensionsConfig{}):      "config.extensions",
		reflect.TypeOf(CollaborationSettings{}): "config.collaboration",
		reflect.TypeOf(ParentBudget{}):          "config.collaboration",
		reflect.TypeOf(ChildBudget{}):           "config.collaboration",
		reflect.TypeOf(SharedMemoryAccess{}):    "config.collaboration",
	}
	for typ, family := range types {
		if !seen[family] {
			t.Fatalf("nested persistent type %s maps to uncovered family %q", typ, family)
		}
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if field.PkgPath != "" { // unexported runtime-only state
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				continue
			}
			// Every field in these structs is persisted beneath the parent
			// Config field and therefore must remain reachable through its
			// typed control family.
			if !seen[family] {
				t.Fatalf("persistent field %s.%s maps to uncovered family %q", typ, name, family)
			}
		}
	}
}
