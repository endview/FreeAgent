package harness

import (
	"encoding/json"
	"reflect"
	"strings"
)

// These schemas describe the existing typed control handlers. They are read
// on demand, never embedded in every model request. Runtime validation remains
// authoritative; credentials are excluded from model-facing profile types.
var controlInputSchemas = buildControlInputSchemas()

func buildControlInputSchemas() map[string]json.RawMessage {
	schemas := map[string]json.RawMessage{}
	schemas[ControlBranchConversation] = controlTypeSchema[branchControlArgs]("conversation_id", "message_id", "mode")
	schemas[ControlChangeExtensionState] = controlTypeSchema[struct {
		ID               string              `json:"id"`
		Scope            ExtensionStateScope `json:"scope"`
		Action           string              `json:"action"`
		ExpectedRevision int64               `json:"expected_revision"`
		Payload          json.RawMessage     `json:"payload"`
	}]("id", "scope", "action", "expected_revision", "payload")
	schemas[ControlClearExtensionData] = controlTypeSchema[struct {
		ID string `json:"id"`
	}]("id")
	schemas[ControlCreateSpace] = controlTypeSchema[SpaceControlArguments]("title")
	schemas[ControlDeleteAttachment] = controlTypeSchema[struct {
		ConversationID string `json:"conversation_id"`
		ID             string `json:"id"`
		SHA256         string `json:"sha256"`
	}]()
	schemas[ControlDeleteSpace] = controlTypeSchema[SpaceControlArguments]("space_id")
	schemas[ControlImportExtension] = controlTypeSchema[struct {
		Source string `json:"source"`
	}]("source")
	schemas[ControlImportSkill] = controlTypeSchema[struct {
		Source string `json:"source"`
	}]("source")
	schemas[ControlInitExtensionStorage] = controlTypeSchema[struct {
		ID string `json:"id"`
	}]("id")
	schemas[ControlMoveConversation] = controlTypeSchema[SpaceControlArguments]("space_id", "conversation_id")
	schemas[ControlPersonPermission] = controlTypeSchema[SpaceControlArguments]("person_id", "mode")
	schemas[ControlPinConversation] = controlTypeSchema[struct {
		ConversationID string `json:"conversation_id"`
		Pinned         bool   `json:"pinned"`
	}]("conversation_id", "pinned")
	schemas[ControlProbeKey] = controlTypeSchema[struct {
		ID string `json:"id"`
	}]("id")
	schemas[ControlRemoveExtension] = controlTypeSchema[struct {
		ID string `json:"id"`
	}]("id")
	schemas[ControlRemoveKey] = controlTypeSchema[struct {
		ID            string `json:"id"`
		DisableModels bool   `json:"disable_models,omitempty"`
		NewDefault    string `json:"new_default,omitempty"`
	}]("id")
	schemas[ControlRemoveMCP] = controlTypeSchema[struct {
		ID string `json:"id"`
	}]("id")
	schemas[ControlRemoveModel] = controlTypeSchema[struct {
		ID         string `json:"id"`
		NewDefault string `json:"new_default,omitempty"`
	}]("id")
	schemas[ControlRemoveSkill] = controlTypeSchema[struct {
		ID string `json:"id"`
	}]("id")
	schemas[ControlRenameConversation] = controlTypeSchema[struct {
		ConversationID string `json:"conversation_id"`
		Title          string `json:"title"`
	}]("conversation_id", "title")
	schemas[ControlRenameSpace] = controlTypeSchema[SpaceControlArguments]("space_id", "title")
	schemas[ControlRestartExtension] = controlTypeSchema[struct {
		ID string `json:"id"`
	}]("id")
	schemas[ControlRestartMCP] = controlTypeSchema[struct{}]()
	schemas[ControlSetAttachmentPolicy] = controlTypeSchema[AttachmentPolicy]()
	schemas[ControlSetAutoPolicy] = controlTypeSchema[autoControlArguments]()
	schemas[ControlSetChannelPolicy] = controlTypeSchema[ChannelsConfig]()
	schemas[ControlSetCollaboration] = controlTypeSchema[CollaborationSettings]()
	schemas[ControlSetCommandPolicy] = controlTypeSchema[CommandConfig]()
	schemas[ControlSetConversationCapabilities] = controlTypeSchema[struct {
		ConversationID string              `json:"conversation_id"`
		Capabilities   CapabilitySelection `json:"capabilities"`
	}]("conversation_id", "capabilities")
	schemas[ControlSetConversationModel] = controlTypeSchema[struct {
		ConversationID string `json:"conversation_id"`
		ModelID        string `json:"model_id"`
	}]("conversation_id", "model_id")
	schemas[ControlSetDefaultModel] = controlTypeSchema[struct {
		ModelID string `json:"model_id"`
	}]("model_id")
	schemas[ControlSetDeliveryPolicy] = controlTypeSchema[DeliveryConfig]()
	schemas[ControlSetExecutionProfile] = controlTypeSchema[struct {
		Mode          PermissionMode `json:"mode"`
		Scope         FileScope      `json:"scope"`
		AllowedRoots  []string       `json:"allowed_roots,omitempty"`
		BlockedPaths  []string       `json:"blocked_paths,omitempty"`
		NetworkPolicy NetworkPolicy  `json:"network_policy"`
	}]("mode", "scope", "network_policy")
	schemas[ControlSetExtensionPolicy] = controlTypeSchema[struct {
		ID               string            `json:"id"`
		Enabled          *bool             `json:"enabled"`
		Required         *bool             `json:"required"`
		TrustedWorkspace string            `json:"trusted_workspace,omitempty"`
		Trusted          *bool             `json:"trusted"`
		Settings         map[string]any    `json:"settings,omitempty"`
		EnvFrom          map[string]string `json:"env_from,omitempty"`
	}]()
	schemas[ControlSetFileScope] = controlTypeSchema[struct {
		Scope        FileScope `json:"scope"`
		AllowedRoots []string  `json:"allowed_roots,omitempty"`
		BlockedPaths []string  `json:"blocked_paths,omitempty"`
	}]()
	schemas[ControlSetHTTPPolicy] = controlTypeSchema[HTTPConfig]()
	schemas[ControlSetKeyProfile] = controlTypeSchema[safeKeyMetadataArguments]("id")
	schemas[ControlSetMCPPolicy] = controlTypeSchema[struct {
		ID                string   `json:"id"`
		Enabled           *bool    `json:"enabled"`
		TrustReadOnlyHint *bool    `json:"trust_read_only_hint"`
		HiddenTools       []string `json:"hidden_tools"`
	}]("id")
	schemas[ControlSetMCPProfile] = controlTypeSchema[safeMCPProfileArguments]("id", "config")
	schemas[ControlSetMemoryPin] = controlTypeSchema[memoryPinArguments]()
	schemas[ControlSetMemoryPolicy] = controlTypeSchema[MemoryConfig]()
	schemas[ControlSetMemorySelection] = controlTypeSchema[struct {
		ConversationID string          `json:"conversation_id"`
		Selection      MemorySelection `json:"selection"`
	}]("conversation_id", "selection")
	schemas[ControlSetModelProfile] = controlTypeSchema[modelProfileControlArguments]()
	schemas[ControlSetNetwork] = controlTypeSchema[NetworkPolicy]("mode")
	schemas[ControlSetPermission] = controlTypeSchema[struct {
		Mode PermissionMode `json:"mode"`
	}]("mode")
	schemas[ControlSetPromptPolicy] = controlTypeSchema[struct {
		SystemPrompt string   `json:"system_prompt"`
		Instructions []string `json:"instructions"`
	}]()
	schemas[ControlSetProviderEnabled] = controlTypeSchema[struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}]("id", "enabled")
	schemas[ControlSetPublicWeb] = controlTypeSchema[PublicWebConfig]()
	schemas[ControlSetRuntimeLimits] = controlTypeSchema[struct {
		ModelRetries          *int `json:"model_retries"`
		AutoCompactAt         *int `json:"auto_compact_percent"`
		HistoryPageSize       *int `json:"history_page_size"`
		MaxContextBytes       *int `json:"max_context_bytes"`
		MaxOutputTokens       *int `json:"max_output_tokens"`
		MaxToolResult         *int `json:"max_tool_result_bytes"`
		TaskSummaryBytes      *int `json:"task_summary_bytes"`
		ExtensionContextChars *int `json:"extension_max_context_chars"`
		ExtensionModelCalls   *int `json:"extension_max_model_calls_per_turn"`
	}]()
	schemas[ControlSetSandbox] = controlTypeSchema[struct {
		Mode SandboxMode `json:"mode"`
	}]("mode")
	schemas[ControlSetServerPolicy] = controlTypeSchema[ServerConfig]()
	schemas[ControlSetSkillPolicy] = controlTypeSchema[struct {
		ID               string `json:"id"`
		Enabled          *bool  `json:"enabled"`
		TrustedWorkspace string `json:"trusted_workspace,omitempty"`
		Trusted          *bool  `json:"trusted"`
	}]()
	schemas[ControlSetSpacePermission] = controlTypeSchema[SpaceControlArguments]("space_id", "mode")
	schemas[ControlSetTool] = controlTypeSchema[struct {
		Tool    string `json:"tool"`
		Enabled bool   `json:"enabled"`
	}]("tool", "enabled")
	schemas[ControlSetWorkspace] = controlTypeSchema[struct {
		Workspace string `json:"workspace"`
	}]("workspace")
	schemas[ControlSpaceActive] = controlTypeSchema[SpaceControlArguments]("space_id", "conversation_id")
	schemas[ControlSpaceDefault] = controlTypeSchema[SpaceControlArguments]("space_id", "conversation_id")
	schemas[ControlSpaceMulti] = controlTypeSchema[SpaceControlArguments]("space_id", "enabled")
	schemas[ControlTelegramAccess] = controlTypeSchema[TelegramAccessChange]()
	schemas[ControlPublishSkill] = controlTypeSchema[SkillPublication]("id", "document")
	schemas[ControlScheduleDelivery] = controlTypeSchema[scheduleDeliveryArguments]("run_id", "candidate")
	return schemas
}
func controlTypeSchema[T any](required ...string) json.RawMessage {
	s := controlJSONType(reflect.TypeFor[T]())
	if len(required) > 0 {
		s["required"] = required
	}
	return mustJSON(s)
}
func controlJSONType(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		return controlJSONType(t.Elem())
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		return map[string]any{"type": "object"}
	}
	var values []string
	switch t {
	case reflect.TypeFor[PermissionMode]():
		values = []string{"none", "ask", "workspace", "full"}
	case reflect.TypeFor[SandboxMode]():
		values = []string{"read_only", "workspace_write", "selected_roots", "host_full"}
	case reflect.TypeFor[FileScope]():
		values = []string{"workspace", "workspace_plus_allowed_roots", "all_non_protected"}
	case reflect.TypeFor[NetworkMode]():
		values = []string{"off", "internet", "allowlist", "host_inherited"}
	case reflect.TypeFor[CommandExecutionMode]():
		values = []string{string(CommandExecutionSandboxed), string(CommandExecutionHostExplicit)}
	}
	if values != nil {
		return map[string]any{"type": "string", "enum": values}
	}
	switch t.Kind() {
	case reflect.Struct:
		properties := map[string]any{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if f.Anonymous && name == "" {
				nested := controlJSONType(f.Type)
				if p, ok := nested["properties"].(map[string]any); ok {
					for k, v := range p {
						properties[k] = v
					}
				}
				continue
			}
			if name == "" {
				name = f.Name
			}
			properties[name] = controlJSONType(f.Type)
		}
		return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": controlJSONType(t.Elem())}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": controlJSONType(t.Elem())}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Interface:
		return map[string]any{}
	default:
		return map[string]any{"type": "integer"}
	}
}
