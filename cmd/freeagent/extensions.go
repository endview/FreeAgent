package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

func runExtensions(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("usage: freeagent ext <new|check|test|run|logs> [flags]")
	}
	switch args[0] {
	case "new":
		return runExtensionNew(ctx, args[1:], stdout, stderr)
	case "check":
		return runExtensionCheck(ctx, args[1:], stdout, stderr)
	case "test":
		return runExtensionFixture(ctx, args[1:], stdout, stderr)
	case "run":
		return runExtensionRun(ctx, args[1:], stdout, stderr)
	case "logs":
		return runExtensionLogs(ctx, args[1:], stdout, stderr)
	default:
		return usagef("freeagent ext: unknown command %q; use new, check, run or logs", args[0])
	}
}

func runExtensionNew(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ext new", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", ".", "directory in which to create the extension")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		return usageErr("usage: freeagent ext new <context|workflow|tool> <id> [--out <directory>]")
	}
	kind, id := strings.TrimSpace(flags.Arg(0)), strings.TrimSpace(flags.Arg(1))
	switch kind {
	case "context", "workflow", "tool":
	default:
		return usagef("ext new: unsupported extension kind %q", kind)
	}
	if err := harness.ValidateExtensionID(id); err != nil {
		return err
	}
	root, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	target := filepath.Join(root, id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("ext new: target already exists: %s", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return err
	}
	manifest, source := extensionTemplate(kind, id)
	if err := os.WriteFile(filepath.Join(target, "extension.json"), []byte(manifest), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(target, "main.py"), []byte(source), 0o600); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"ok": true, "kind": kind, "id": id, "directory": target,
		"manifest":   filepath.Join(target, "extension.json"),
		"entrypoint": filepath.Join(target, "main.py"),
	})
}

func runExtensionCheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ext check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("path", "", "extension directory; defaults to the current directory")
	jsonOutput := flags.Bool("json", true, "write structured output")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return usageErr("ext check: at most one extension directory is allowed")
	}
	if *path == "" {
		*path = "."
		if flags.NArg() == 1 {
			*path = flags.Arg(0)
		}
	}
	resolved, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	result, err := harness.CheckExtensionProcess(ctx, resolved)
	if err != nil {
		return err
	}
	if !*jsonOutput {
		_, err = fmt.Fprintf(stdout, "%s %s: checked (%s)\n", result.Info.ID, result.Info.Version, strings.Join(result.Probes, ", "))
		return err
	}
	return writeJSON(stdout, result)
}

func runExtensionFixture(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ext run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("path", "", "extension directory; defaults to the current directory")
	fixturePath := flags.String("fixture", "", "fixture JSON file")
	host := flags.String("host", "mock", "fixture host: mock")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return usageErr("ext run: at most one extension directory is allowed")
	}
	if *path == "" {
		*path = "."
		if flags.NArg() == 1 {
			*path = flags.Arg(0)
		}
	}
	if strings.TrimSpace(*fixturePath) == "" {
		return usageErr("ext run: --fixture is required")
	}
	if *host != "mock" {
		return usageErr("ext run: live host is not implemented yet; use --host mock")
	}
	fixtureRaw, err := os.ReadFile(*fixturePath)
	if err != nil {
		return err
	}
	if len(fixtureRaw) > 1<<20 {
		return errors.New("ext run: fixture exceeds 1 MiB")
	}
	var fixture harness.ExtensionFixture
	if err := json.Unmarshal(fixtureRaw, &fixture); err != nil {
		return fmt.Errorf("ext run: fixture JSON: %w", err)
	}
	resolved, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	result, err := harness.RunExtensionFixture(ctx, resolved, fixture)
	if err != nil {
		return err
	}
	if err := writeJSON(stdout, result); err != nil {
		return err
	}
	if !result.Matched {
		return &exitError{code: exitFailure, err: errors.New("ext run: fixture expectations did not match")}
	}
	return nil
}

type extensionRunOutput struct {
	harness.RunResult
	Extension string `json:"extension"`
	Workflow  string `json:"workflow"`
	Session   string `json:"session"`
	Request   string `json:"request"`
}

func runExtensionRun(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ext run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	path := flags.String("path", "", "extension directory; defaults to the current directory")
	session := flags.String("session", "", "conversation id; created when omitted")
	request := flags.String("request", "", "stable idempotency key")
	workflow := flags.String("workflow", "", "declared workflow name")
	input := flags.String("input", "", "workflow input as a JSON object")
	inputFile := flags.String("input-file", "", "file containing workflow input JSON")
	fixturePath := flags.String("fixture", "", "fixture JSON file (use --host mock)")
	host := flags.String("host", "", "workflow host: live or mock")
	asJSON := flags.Bool("json", false, "write structured output")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return usageErr("ext run: at most one extension directory is allowed")
	}
	if *path == "" {
		*path = "."
		if flags.NArg() == 1 {
			*path = flags.Arg(0)
		}
	}
	if strings.TrimSpace(*fixturePath) != "" {
		if strings.TrimSpace(*workflow) != "" || strings.TrimSpace(*input) != "" ||
			strings.TrimSpace(*inputFile) != "" || strings.TrimSpace(*session) != "" ||
			strings.TrimSpace(*request) != "" || (*host != "" && *host != "mock") {
			return usageErr("ext run: fixture mode only supports --path, --fixture and --host mock")
		}
		return runExtensionFixture(ctx, args, stdout, stderr)
	}
	if *host != "" && *host != "live" {
		return usageErr("ext run: live workflow execution requires --host live")
	}
	if strings.TrimSpace(*workflow) == "" {
		return usageErr("ext run: --workflow is required for live execution")
	}
	if strings.TrimSpace(*input) != "" && strings.TrimSpace(*inputFile) != "" {
		return usageErr("ext run: choose only one of --input or --input-file")
	}
	rawInput := []byte("{}")
	var err error
	switch {
	case strings.TrimSpace(*input) != "":
		rawInput = []byte(*input)
	case strings.TrimSpace(*inputFile) != "":
		rawInput, err = os.ReadFile(*inputFile)
		if err != nil {
			return err
		}
	}
	if len(rawInput) > 64<<10 {
		return errors.New("ext run: workflow input exceeds 64 KiB")
	}
	var workflowInput any
	if err := json.Unmarshal(rawInput, &workflowInput); err != nil {
		return fmt.Errorf("ext run: workflow input JSON: %w", err)
	}

	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	resolvedPath, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	checked, err := harness.CheckExtension(resolvedPath)
	if err != nil {
		return err
	}
	if err := requireModels(*dataDir, stderr); err != nil {
		return err
	}
	_, _, service, closeAll, err := openRuntime(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer closeAll()
	tasks := harness.NewTaskService(service)
	item, lookupErr := tasks.Extension(ctx, checked.ID)
	if errors.Is(lookupErr, os.ErrNotExist) {
		control, controlErr := tasks.Control()
		if controlErr != nil {
			return fmt.Errorf("ext run: extension control: %w", controlErr)
		}
		if _, err := control.ApplyDirectControlChange(ctx, harness.ControlImportExtension, map[string]string{"source": resolvedPath}, "", "cli"); err != nil {
			return fmt.Errorf("ext run: import extension: %w", err)
		}
		item, lookupErr = tasks.Extension(ctx, checked.ID)
	}
	if lookupErr != nil {
		return fmt.Errorf("ext run: load extension: %w", lookupErr)
	}
	if item.Error != "" {
		return fmt.Errorf("ext run: extension %s: %s", checked.ID, item.Error)
	}
	if !item.Enabled {
		control, controlErr := tasks.Control()
		if controlErr != nil {
			return fmt.Errorf("ext run: extension control: %w", controlErr)
		}
		if _, controlErr = control.ApplyDirectControlChange(ctx, harness.ControlSetExtensionPolicy, map[string]any{
			"id": checked.ID, "enabled": true,
		}, "", "cli"); controlErr != nil {
			return fmt.Errorf("ext run: enable extension: %w", controlErr)
		}
	}
	if item, err = tasks.Extension(ctx, checked.ID); err != nil {
		return fmt.Errorf("ext run: reload extension: %w", err)
	}
	var sessionID string
	if strings.TrimSpace(*session) == "" {
		conversation, err := tasks.NewConversation(ctx, "Extension "+checked.ID+"/"+*workflow)
		if err != nil {
			return fmt.Errorf("ext run: create session: %w", err)
		}
		sessionID = conversation.ID
	} else {
		sessionID = strings.TrimSpace(*session)
	}
	requestID := strings.TrimSpace(*request)
	if requestID == "" {
		requestID = fmt.Sprintf("ext-%x", time.Now().UTC().UnixNano())
	}
	result, runErr := tasks.RunWorkflow(ctx, sessionID, requestID, checked.ID,
		strings.TrimSpace(*workflow), workflowInput)
	output := extensionRunOutput{
		RunResult: result,
		Extension: checked.ID,
		Workflow:  strings.TrimSpace(*workflow),
		Session:   sessionID,
		Request:   requestID,
	}
	if *asJSON {
		if err := writeJSON(stdout, output); err != nil {
			return errors.Join(err, runErr)
		}
	} else {
		if result.Answer != "" {
			_, _ = fmt.Fprintln(stdout, result.Answer)
		}
		_, _ = fmt.Fprintf(stderr, "extension=%s workflow=%s session=%s status=%s\n",
			output.Extension, output.Workflow, output.Session, result.Run.Status)
	}
	return runOutcome(stderr, result, runErr)
}

func runExtensionLogs(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("ext logs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	jsonOutput := flags.Bool("json", false, "write the log as a JSON string")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return usageErr("usage: freeagent ext logs <id> [--data-dir <directory>]")
	}
	id := strings.TrimSpace(flags.Arg(0))
	if err := harness.ValidateExtensionID(id); err != nil {
		return err
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	config, err := harness.LoadConfig(*dataDir)
	if err != nil {
		return err
	}
	store, err := openStoreForReading(ctx, *dataDir, stderr)
	if err != nil {
		return err
	}
	defer store.Close()
	tasks := harness.NewTaskService(&harness.Service{Store: store, Config: config})
	if _, err := tasks.Extension(ctx, id); err != nil {
		return fmt.Errorf("ext logs: %w", err)
	}
	raw, err := tasks.ExtensionLog(id)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, map[string]any{"id": id, "log": string(raw)})
	}
	_, err = stdout.Write(raw)
	return err
}

func extensionTemplate(kind, id string) (string, string) {
	provides := "context"
	context := `,"context":{"phases":["turn"],"purposes":["chat"],"placement":["tail"]}`
	workflows := ""
	tools := ""
	registeredWorkflows := "[]"
	switch kind {
	case "workflow":
		provides = "workflow"
		context = ""
		workflows = `,"workflows":[{"name":"run","description":"Run the sample workflow","agent_callable":true,"input_schema":{"type":"object","properties":{"text":{"type":"string"}},"additionalProperties":false}}]`
		registeredWorkflows = `["run"]`
	case "tool":
		provides = "tools"
		context = ""
		tools = `,"tools":true`
	}
	manifest := fmt.Sprintf(`{
  "protocol": "freeagent-ext/1",
  "id": %q,
  "version": "0.1.0",
  "name": %q,
  "description": "FreeAgent extension generated by ext new",
  "run": {"command": "python", "args": ["main.py"]}%s%s%s
}
`, id, id, context, workflows, tools)
	source := fmt.Sprintf(`import json
import sys

EXTENSION_ID = %q
VERSION = "0.1.0"
PROVIDES = %q
WORKFLOWS = %s

def reply(request, result):
    if "id" not in request:
        return
    sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    try:
        request = json.loads(line)
        method = request.get("method")
        if method == "initialize":
            reply(request, {"id": EXTENSION_ID, "version": VERSION, "provides": [PROVIDES], "workflows": WORKFLOWS})
        elif method == "context.prepare":
            reply(request, {"blocks": [{"id": "sample", "text": "Replace this generated context block.", "placement": "tail", "label": "Sample"}]})
        elif method == "workflow.run":
            input_value = request.get("params", {}).get("input", {})
            reply(request, {"output": "Replace this generated workflow: " + str(input_value.get("text", ""))})
        elif method == "tools/list":
            reply(request, {"tools": [{"name": "echo", "description": "Replace this generated tool", "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"], "additionalProperties": False}, "readOnly": True}]})
        elif method == "tool.call":
            arguments = request.get("params", {}).get("arguments", {})
            reply(request, {"content": "Replace this generated tool: " + str(arguments.get("text", ""))})
        elif method == "shutdown":
            reply(request, {})
            break
        else:
            reply(request, {})
    except Exception as error:
        sys.stderr.write(str(error) + "\n")
        sys.stderr.flush()
`, id, provides, registeredWorkflows)
	return manifest, source
}
