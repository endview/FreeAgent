package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type remoteTool struct {
	server  string
	name    string
	session *mcp.ClientSession
	input   *jsonschema.Resolved
	effect  ToolEffect
}

// RuntimeTools uses the same preparation/dispatch boundary for built-in and MCP tools.
type RuntimeTools struct {
	Files       *FileTools
	Commands    *CommandTools
	HTTP        *HTTPTools
	definitions []ToolDefinition
	remote      map[string]remoteTool
	connected   map[string]bool
	sessions    []*mcp.ClientSession
	cleanups    []func() error
	secrets     []string
	Rejected    map[string]string
	allowedMCP  map[string]bool
	mcpSelected bool
	keys        *KeyStore
	logDir      string
	dataDir     string
}

func NewRuntimeTools(files *FileTools) *RuntimeTools {
	return &RuntimeTools{Files: files, definitions: files.Definitions(), remote: map[string]remoteTool{},
		connected: map[string]bool{}, Rejected: map[string]string{}}
}

func (tools *RuntimeTools) SetKeyStore(keys *KeyStore) { tools.keys = keys }
func (tools *RuntimeTools) SetLogDir(path string)      { tools.logDir = path }
func (tools *RuntimeTools) SetDataDir(path string)     { tools.dataDir = path }

func (tools *RuntimeTools) Definitions() []ToolDefinition {
	result := append([]ToolDefinition(nil), tools.definitions...)
	if tools.mcpSelected {
		filtered := result[:0]
		for _, definition := range result {
			if remote, ok := tools.remote[definition.Name]; ok && !tools.allowedMCP[remote.server] {
				continue
			}
			filtered = append(filtered, definition)
		}
		result = filtered
	}
	if tools.Commands != nil {
		result = append(result, tools.Commands.Definitions()...)
	}
	if tools.HTTP != nil {
		result = append(result, tools.HTTP.Definitions()...)
	}
	return result
}

// ForCapabilities returns a shallow view that shares live MCP sessions and
// cleanup ownership with the base runtime, while exposing only the servers
// frozen into the current turn.
func (tools *RuntimeTools) ForCapabilities(selection CapabilitySelection) *RuntimeTools {
	copy := *tools
	copy.allowedMCP = map[string]bool{}
	copy.mcpSelected = true
	for _, name := range selection.MCPServers {
		copy.allowedMCP[name] = true
	}
	return &copy
}

func (tools *RuntimeTools) Close() error {
	result := tools.closeMCP()
	return errors.Join(result, tools.Files.Close())
}

func (tools *RuntimeTools) closeMCP() error {
	var result error
	for _, session := range tools.sessions {
		if err := session.Close(); err != nil {
			result = errors.Join(result, errors.New("harness: MCP session cleanup failed"))
		}
	}
	for _, cleanup := range tools.cleanups {
		if err := cleanup(); err != nil {
			result = errors.Join(result, errors.New("harness: MCP process cleanup failed"))
		}
	}
	tools.sessions = nil
	tools.cleanups = nil
	tools.definitions = tools.Files.Definitions()
	tools.remote = map[string]remoteTool{}
	tools.connected = map[string]bool{}
	tools.secrets = nil
	tools.Rejected = map[string]string{}
	return result
}

// RestartMCP replaces only the MCP process/session slice. File, command and
// HTTP tools remain owned by the existing runtime and are not recreated.
func (tools *RuntimeTools) RestartMCP(ctx context.Context, config Config, only string) error {
	if err := tools.closeMCP(); err != nil {
		return err
	}
	return tools.Connect(ctx, config, only)
}

type MCPToolStatus struct {
	Alias  string     `json:"alias"`
	Name   string     `json:"name"`
	Effect ToolEffect `json:"effect"`
}

type MCPServerStatus struct {
	ID          string          `json:"id"`
	Enabled     bool            `json:"enabled"`
	Connected   bool            `json:"connected"`
	ToolCount   int             `json:"tool_count"`
	Tools       []MCPToolStatus `json:"tools,omitempty"`
	Rejected    []string        `json:"rejected,omitempty"`
	HiddenTools []string        `json:"hidden_tools,omitempty"`
}

func (tools *RuntimeTools) MCPStatuses(config Config) []MCPServerStatus {
	result := make([]MCPServerStatus, 0, len(config.MCPServers))
	for name, server := range config.MCPServers {
		status := MCPServerStatus{ID: name, Enabled: server.Enabled, Connected: tools.connected[name],
			HiddenTools: append([]string(nil), server.HiddenTools...)}
		for alias, remote := range tools.remote {
			if remote.server == name {
				status.Tools = append(status.Tools, MCPToolStatus{Alias: alias, Name: remote.name, Effect: remote.effect})
			}
		}
		for rejected := range tools.Rejected {
			if strings.HasPrefix(rejected, name+"/") {
				status.Rejected = append(status.Rejected, strings.TrimPrefix(rejected, name+"/"))
			}
		}
		sort.Slice(status.Tools, func(i, j int) bool { return status.Tools[i].Name < status.Tools[j].Name })
		sort.Strings(status.Rejected)
		status.ToolCount = len(status.Tools)
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (tools *RuntimeTools) Connect(ctx context.Context, config Config, only string) error {
	names := make([]string, 0, len(config.MCPServers))
	for name, server := range config.MCPServers {
		if server.Enabled && (only == "" || name == only) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if only != "" && len(names) != 1 {
		return errors.New("harness: selected MCP server is missing or disabled")
	}
	for _, name := range names {
		if err := tools.connectServer(ctx, name, config.MCPServers[name], config.Workspace); err != nil {
			return fmt.Errorf("harness: MCP server %q: %w", tools.Redact(name), err)
		}
	}
	return nil
}

func (tools *RuntimeTools) connectServer(parent context.Context, name string, config MCPServerConfig, workspace string) error {
	env, secrets, err := mcpEnvironment(config, tools.keys)
	if err != nil {
		return err
	}
	tools.secrets = append(tools.secrets, secrets...)
	cwd := config.Cwd
	if cwd == "" {
		cwd = workspace
	}
	var logPath string
	if tools.logDir != "" {
		safeName := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(name)
		logPath = filepath.Join(tools.logDir, "mcp-"+safeName+".log")
	}
	var transport mcp.Transport
	var closeTransport func() error
	if logPath != "" {
		logged, transportErr := mcpstdio.NewTransportWithLog(config.Command, config.Args, cwd, env, logPath, tools.Redact)
		if transportErr != nil {
			return errors.New("invalid process configuration")
		}
		transport, closeTransport = logged, logged.Close
	} else {
		plain, transportErr := mcpstdio.NewTransport(config.Command, config.Args, cwd, env)
		if transportErr != nil {
			return errors.New("invalid process configuration")
		}
		transport, closeTransport = plain, plain.Close
	}
	tools.cleanups = append(tools.cleanups, closeTransport)
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "freeagent-harness", Version: "4.0.0-dev.1"},
		&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return errors.New("initialization failed or timed out")
	}
	tools.sessions = append(tools.sessions, session)
	initialized := session.InitializeResult()
	if initialized == nil || initialized.Capabilities == nil || initialized.Capabilities.Tools == nil {
		return errors.New("server did not advertise tools")
	}
	switch initialized.ProtocolVersion {
	case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
	default:
		return errors.New("unsupported negotiated MCP version")
	}
	cursor := ""
	cursors := map[string]bool{}
	names := map[string]bool{}
	totalBytes := 0
	for page := 0; page < 32; page++ {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil || result == nil {
			return errors.New("tools/list failed or timed out")
		}
		hidden := map[string]bool{}
		for _, hiddenTool := range config.HiddenTools {
			hidden[hiddenTool] = true
		}
		for _, tool := range result.Tools {
			if tool == nil || tool.Name == "" || len(tool.Name) > 256 || names[tool.Name] || tools.Redact(tool.Name) != tool.Name {
				return errors.New("invalid or duplicate remote tool name")
			}
			names[tool.Name] = true
			if hidden[tool.Name] {
				continue
			}
			raw, err := json.Marshal(tool)
			totalBytes += len(raw)
			if err != nil || totalBytes > 1<<20 || len(names) > 128 {
				return errors.New("tool discovery exceeded limits")
			}
			definition, input, err := tools.describe(name, config, tool)
			if err != nil {
				tools.Rejected[name+"/"+tool.Name] = "unsupported or unsafe tool schema/definition"
				continue
			}
			if _, exists := tools.remote[definition.Name]; exists {
				return errors.New("tool alias conflict")
			}
			tools.definitions = append(tools.definitions, definition)
			tools.remote[definition.Name] = remoteTool{server: name, name: tool.Name, input: input, session: session, effect: definition.Effect}
		}
		if result.NextCursor == "" {
			tools.connected[name] = true
			return nil
		}
		if len(result.NextCursor) > 1024 || cursors[result.NextCursor] {
			return errors.New("invalid or repeated tools/list cursor")
		}
		cursors[result.NextCursor] = true
		cursor = result.NextCursor
	}
	return errors.New("tools/list exceeded page limit")
}

func (tools *RuntimeTools) describe(server string, config MCPServerConfig, tool *mcp.Tool) (ToolDefinition, *jsonschema.Resolved, error) {
	raw, err := json.Marshal(tool.InputSchema)
	if err != nil || len(raw) > 64<<10 {
		return ToolDefinition{}, nil, errors.New("schema exceeded limits")
	}
	input, err := resolveToolSchema(raw)
	if err != nil {
		return ToolDefinition{}, nil, err
	}
	description := tools.Redact(server + "/" + tool.Name + ": " + tool.Description)
	if len(description) > 8192 || tools.Redact(string(raw)) != string(raw) {
		return ToolDefinition{}, nil, errors.New("unsafe definition")
	}
	alias := "mcp_" + contentHash([]byte(server + "\x00" + tool.Name))[:24]
	effect := EffectModify
	if config.TrustReadOnlyHint && tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
		effect = EffectRead
	}
	return ToolDefinition{Name: alias, Description: description, InputSchema: raw, Effect: effect}, input, nil
}

func resolveToolSchema(raw []byte) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if json.Unmarshal(raw, &schema) != nil || schema.Type != "object" {
		return nil, errors.New("tool schema must describe an object")
	}
	if err := checkSchema(&schema, 0); err != nil {
		return nil, err
	}
	return schema.Resolve(&jsonschema.ResolveOptions{Loader: func(*url.URL) (*jsonschema.Schema, error) {
		return nil, errors.New("external schema references are disabled")
	}})
}

func checkSchema(schema *jsonschema.Schema, depth int) error {
	if schema == nil {
		return nil
	}
	if depth > 32 || len(schema.Extra) != 0 || schema.Format != "" {
		return errors.New("unsupported schema keyword, format, or depth")
	}
	value := reflect.ValueOf(schema).Elem()
	pointer := reflect.TypeFor[*jsonschema.Schema]()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		switch {
		case field.Type() == pointer:
			if err := checkSchema(field.Interface().(*jsonschema.Schema), depth+1); err != nil {
				return err
			}
		case field.Kind() == reflect.Slice && field.Type().Elem() == pointer:
			for j := 0; j < field.Len(); j++ {
				if err := checkSchema(field.Index(j).Interface().(*jsonschema.Schema), depth+1); err != nil {
					return err
				}
			}
		case field.Kind() == reflect.Map && field.Type().Elem() == pointer:
			iter := field.MapRange()
			for iter.Next() {
				if err := checkSchema(iter.Value().Interface().(*jsonschema.Schema), depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (tools *RuntimeTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if call.Name == "command_run" && tools.Commands != nil {
		return tools.Commands.Prepare(ctx, call)
	}
	if call.Name == "http_get" && tools.HTTP != nil {
		return tools.HTTP.Prepare(ctx, call)
	}
	tool, exists := tools.remote[call.Name]
	if !exists {
		return tools.Files.Prepare(ctx, call)
	}
	if tools.mcpSelected && !tools.allowedMCP[tool.server] {
		return PreparedTool{}, errors.New("harness: MCP server is not enabled for this turn")
	}
	if err := ctx.Err(); err != nil {
		return PreparedTool{}, err
	}
	if len(call.Arguments) > 64<<10 || tools.Redact(string(call.Arguments)) != string(call.Arguments) {
		return PreparedTool{}, errors.New("invalid or secret-bearing MCP arguments")
	}
	var args map[string]any
	if json.Unmarshal(call.Arguments, &args) != nil || args == nil || tool.input.Validate(args) != nil {
		return PreparedTool{}, errors.New("MCP arguments do not satisfy the input schema")
	}
	return PreparedTool{Effect: tool.effect, Preview: tool.server + "/" + tool.name + "\n" + string(call.Arguments),
		Items: []ApprovalItem{{Kind: "mcp", Target: tool.server + "/" + tool.name,
			Fields: map[string]string{"arguments": string(call.Arguments)}}},
		Execute: func(parent context.Context) (ToolResult, error) {
			if err := parent.Err(); err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: "cancelled before MCP call"}
			}
			ctx, cancel := context.WithTimeout(parent, 30*time.Second)
			defer cancel()
			result, err := tool.session.CallTool(ctx, &mcp.CallToolParams{Name: tool.name, Arguments: args})
			if err != nil || result == nil {
				return ToolResult{}, &CallError{Message: "MCP call interrupted; outcome unknown"}
			}
			var text strings.Builder
			unsupported := false
			for _, content := range result.Content {
				if part, ok := content.(*mcp.TextContent); ok {
					text.WriteString(part.Text)
					text.WriteByte('\n')
				} else {
					unsupported = true
				}
			}
			if result.StructuredContent != nil {
				raw, err := json.Marshal(result.StructuredContent)
				if err != nil {
					return ToolResult{IsError: true, Content: "operation responded but structured content was rejected; do not repeat automatically"}, nil
				}
				text.Write(raw)
			}
			if unsupported {
				text.WriteString("\nUnsupported non-text content was omitted. Operation may have completed; do not repeat automatically.")
			}
			return ToolResult{Content: tools.Redact(text.String()), IsError: result.IsError || unsupported}, nil
		}}, nil
}

func (tools *RuntimeTools) Redact(text string) string {
	for _, secret := range tools.secrets {
		text = redact(text, secret)
	}
	return text
}

func mcpEnvironment(config MCPServerConfig, keys *KeyStore) ([]string, []string, error) {
	values := map[string]string{}
	key := func(name string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(name)
		}
		return name
	}
	for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(name); ok {
			values[key(name)] = value
		}
	}
	for name, value := range config.Env {
		values[key(name)] = value
	}
	var secrets []string
	for name, source := range config.EnvFrom {
		var value string
		switch {
		case strings.HasPrefix(source, "key:"):
			if keys == nil {
				return nil, nil, errors.New("MCP key reference requires the key store")
			}
			resolved, err := keys.Resolve(strings.TrimPrefix(source, "key:"))
			if err != nil {
				return nil, nil, errors.New("MCP key reference is unavailable")
			}
			value = resolved.secret
		default:
			var ok bool
			value, ok = os.LookupEnv(source)
			if !ok {
				return nil, nil, errors.New("required MCP environment mapping is missing or invalid")
			}
		}
		if value == "" || len(value) > 32768 || strings.ContainsRune(value, 0) {
			return nil, nil, errors.New("required MCP environment mapping is missing or invalid")
		}
		values[key(name)] = value
		secrets = append(secrets, value)
	}
	env := make([]string, 0, len(values))
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	sort.Strings(env)
	return env, secrets, nil
}
