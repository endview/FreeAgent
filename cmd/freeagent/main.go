package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

func main() {
	signals := []os.Signal{os.Interrupt, syscall.SIGTERM}
	if len(os.Args) == 1 || os.Args[1] == "chat" || os.Args[1] == "--data-dir" {
		signals = []os.Signal{syscall.SIGTERM}
	}
	ctx, stop := signal.NotifyContext(context.Background(), signals...)
	defer stop()
	handled, err := showDoubleClickHint(os.Args[1:])
	if !handled && err == nil {
		err = run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	}
	if err != nil {
		reportError(os.Stderr, err)
		code := exitCode(err)
		if code != 0 {
			keepLaunchErrorVisible()
		}
		os.Exit(code)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		args = []string{"chat"}
	} else if args[0] == "--data-dir" {
		args = append([]string{"chat"}, args...)
	}
	if ctx == nil || stdin == nil || stdout == nil || stderr == nil {
		return usageErr("freeagent: context and input/output streams are required")
	}
	switch args[0] {
	case "--version":
		return runVersion(args[1:], stdout, currentBuildInfo())
	case "help", "--help", "-h":
		_, _ = fmt.Fprintln(stdout, "Optional collaboration: /collaboration off|ask|auto|once; /settings advanced; run --collaboration off|ask|auto; config collaboration --mode off|ask|auto. Explicit plan inspection and controls: collab create/continue/inspect/control/adjust/preview/confirm/resolve.")
		_, err := fmt.Fprintln(stdout, `FreeAgent Harness

freeagent [--data-dir <directory>] opens a work session
init --workspace <directory> [--data-dir <directory>]
key list [--json]
key add --name <name> --url <address> [--note <text>] [--preset deepseek] [--key-stdin | --key-env <variable> | --no-key]
  Without a source flag the key is read with hidden input. Keys are never passed as arguments.
key edit <id|name> [--name <name>] [--url <address>] [--note <text>] [--key-prompt | --key-stdin | --key-env <variable> | --no-key]
key remove <id|name> [--force | --disable-models [--new-default <id>]] [--data-dir <directory>]
key test <id|name> [--json]
key models <id|name> [--json]
config show
config model --id <name> [--default] [--key <key profile id|name> --model <name>] [--provider <echo|openai-compatible>]
  --key names a key profile from "key list", not the key itself. --provider echo is for tests only.
  Optional capabilities: --tools <supported|none> --context-bytes <n> --output-tokens <n> --strength <light|standard|strong>
config model --id <name> --default            switches the default model
config model --id <name> --remove [--new-default <id>]
config workspace --path <directory>
config auto --enabled [--default] [--allow-model <id>] [--assessment-model <id>] [--max-candidates 8 --max-assessments 1 --max-upgrades 1]
config commands --enabled [--allow <absolute-path>] [--timeout 120 --output-bytes 32768 --inherit-environment --git-write]
config http --enabled --origin <scheme://host[:port]> [--timeout 20 --output-bytes 32768]
config context --summary-bytes <0-16384>
config memory [--knowledge] [--max-entries 16 --context-bytes 16384 --document-bytes 65536]
memory <scopes|list|read|save|save-result|update|disable|enable|delete|copy|import|search|sources|select|clear|agent> [flags]
chat [--resume <session>]
web [--port <number>]
ext new [--out <directory>] <context|workflow|tool> <id>
ext check [--path <directory>] [<directory>]
ext test [--path <directory>] --fixture <file> [--host mock]
ext run [--data-dir <directory>] [--path <directory>] --workflow <name> [--input <json>|--input-file <file>] [--session <id>] [--request <key>] [--json]
  ext run [--path <directory>] --fixture <file> [--host mock] (fixture compatibility)
ext logs [--data-dir <directory>] [--json] <id>
run [--session <id>] [--model <id>] [--permission <ask|workspace|full>] [--confirm-full-access] [--request <key>] [--template <file>] [--json] [--interactive] <task>
continue --run <id> [--model <id>] [--max-steps <n>] [--max-seconds <n>] [--interactive] [--json]
sessions [--show <id>]
inspect --run <id>
preview --run <id> [--json]
confirm --run <id> --digest <digest> [--approve] [--json]
  Without --approve the pending call is declined.
abandon --run <id>
control <state|catalog|list|plan|confirm|reject|revert-plan|review> [--kind <kind> --arguments <json>] [--operation <id>] [--code <code>]
  Changes the same settings as the GUI and the AI conversation; a model proposal needs its confirmation code.
tools list
tools test <server>
doctor
backup --out <new-directory>
backup-verify --source <backup-directory>
restore --source <backup-directory> --data-dir <new-directory>
--version

Chat commands: /model [<id>] /collaboration /settings [advanced|memory] /memory /more /history /sessions /resume <number> /new /status /continue /abandon /file <path> /begin ... /end /exit
Use --data-dir for runtime commands. Ask confirms modifying operations, Workspace access auto-runs workspace file changes and sandboxed commands, and Full access auto-runs enabled modifying tools. On Windows commands run in an AppContainer sandbox; Full access runs them on the host. MCP and loopback HTTP are not sandboxed.
Exit codes: 0 completed, 1 failed, 2 usage or configuration error, 3 waiting for confirmation, 4 stopped or limited, 5 unknown result.`)
		return err
	case "init":
		return runInit(ctx, args[1:], stdout, stderr)
	case "key":
		return runKey(ctx, args[1:], stdin, stdout, stderr)
	case "config":
		return runConfig(ctx, args[1:], stdin, stdout, stderr)
	case "control":
		return runControl(ctx, args[1:], stdin, stdout, stderr)
	case "memory":
		return runMemory(ctx, args[1:], stdin, stdout, stderr)
	case "sessions":
		return runSessions(ctx, args[1:], stdout, stderr)
	case "inspect":
		return runInspect(ctx, args[1:], stdout, stderr)
	case "preview":
		return runPreview(ctx, args[1:], stdout, stderr)
	case "confirm":
		return runConfirm(ctx, args[1:], stdin, stdout, stderr)
	case "abandon":
		return runAbandon(ctx, args[1:], stdout, stderr)
	case "tools":
		return runTools(ctx, args[1:], stdout, stderr)
	case "collab":
		return runCollaboration(ctx, args[1:], stdin, stdout, stderr)
	case "run":
		return runTask(ctx, args[1:], stdin, stdout, stderr)
	case "continue":
		return runContinue(ctx, args[1:], stdin, stdout, stderr)
	case "chat":
		return runChat(ctx, args[1:], stdin, stdout, stderr)
	case "web":
		return runWeb(ctx, args[1:], stdin, stdout, stderr)
	case "ext":
		return runExtensions(ctx, args[1:], stdin, stdout, stderr)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr)
	case "backup", "backup-verify", "restore":
		return runBackupCommand(ctx, args[0], args[1:], stdout, stderr)
	default:
		return fmt.Errorf("freeagent: unknown command %q; %w", args[0], usageError())
	}
}

func usageError() error {
	return usageErr("usage: freeagent <init|key|config|control|memory|chat|web|ext|run|continue|sessions|inspect|preview|confirm|abandon|tools|collab|doctor|backup|backup-verify|restore> [flags]; use --help")
}

func runInit(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "new harness data directory")
	workspace := flags.String("workspace", "", "workspace directory")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*workspace) == "" {
		return usageErr("freeagent init: --workspace is required")
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	workspacePath, err := filepath.Abs(*workspace)
	if err != nil {
		return err
	}
	config := harness.DefaultConfig(workspacePath)
	if err := config.Validate(); err != nil {
		return err
	}
	if err := harness.InitStore(ctx, *dataDir); err != nil {
		return fmt.Errorf("freeagent init: %w", err)
	}
	if err := harness.WriteConfig(*dataDir, config); err != nil {
		return fmt.Errorf("freeagent init: write config: %w", err)
	}
	if err := writeJSON(stdout, map[string]any{"data_dir": *dataDir, "workspace": workspacePath}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stderr, `Next, add a key and a model that uses it:
  freeagent key add --name <name> --url <address>
  freeagent config model --data-dir %s --id <id> --key <name> --model <model>
`, shellArg(*dataDir))
	return nil
}

func runSessions(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("sessions", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	show := flags.String("show", "", "show session history")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageError()
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	store, err := openStoreForReading(ctx, *dataDir, stderr)
	if err != nil {
		return err
	}
	defer store.Close()
	if *show != "" {
		if _, err := store.GetConversation(ctx, *show); err != nil {
			return err
		}
		history, err := store.ListMessages(ctx, *show)
		if err != nil {
			return err
		}
		return writeJSON(stdout, history)
	}
	sessions, err := store.ListConversations(ctx)
	if err != nil {
		return err
	}
	return writeJSON(stdout, sessions)
}

// openStoreForReading opens the store for a command that only reads. While
// another process owns the data directory it shows a read-only view instead.
func openStoreForReading(ctx context.Context, dataDir string, stderr io.Writer) (*harness.Store, error) {
	store, err := harness.OpenStore(ctx, dataDir)
	if !errors.Is(err, harness.ErrOwnerActive) {
		return store, err
	}
	store, err = harness.OpenStoreReadOnly(ctx, dataDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "The data directory is in use and could not be read. Close the desktop app and try again.")
		return nil, &exitError{code: exitFailure, err: err}
	}
	_, _ = fmt.Fprintln(stderr, "data directory is in use by another FreeAgent process; showing a read-only view")
	return store, nil
}

func runTask(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	conversationID := flags.String("session", "", "session id; created when omitted")
	requestKey := flags.String("request", "", "stable idempotency key")
	interactive := flags.Bool("interactive", false, "prompt to approve file edits")
	asJSON := flags.Bool("json", false, "write only structured output to stdout")
	template := flags.String("template", "", "explicit ordinary workspace template file")
	modelID := flags.String("model", "", "explicit local model profile for this task")
	permission := flags.String("permission", "", "this task only: ask, workspace or full")
	confirmFullAccess := flags.Bool("confirm-full-access", false, "confirm Full access for this task")
	collaborationMode := flags.String("collaboration", "", "this task only: off, ask or auto")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	task := strings.Join(flags.Args(), " ")
	if strings.TrimSpace(task) == "" {
		return usageErr("freeagent run: task text is required")
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	if err := requireModels(*dataDir, stderr); err != nil {
		return err
	}
	options := harness.RunOptions{ModelID: *modelID, Permission: harness.PermissionMode(*permission), ConfirmFullAccess: *confirmFullAccess}
	if *permission != "" {
		if err := options.Permission.Validate(); err != nil {
			return usageErr(err.Error())
		}
	}
	if *requestKey == "" {
		*requestKey = fmt.Sprintf("%x", time.Now().UTC().UnixNano())
	}
	_, _, service, closeAll, err := openRuntime(ctx, *dataDir)
	if errors.Is(err, harness.ErrOwnerActive) {
		if err := rejectAttachedOptions(*template, *modelID, 0, 0); err != nil {
			return err
		}
		if *collaborationMode != "" {
			settings := harness.DefaultCollaborationSettings()
			settings.Mode = *collaborationMode
			options.Collaboration = &settings
		}
		client, attachErr := attachedOwner(ctx, *dataDir)
		if attachErr != nil {
			return fmt.Errorf("%w: %v", harness.ErrOwnerActive, attachErr)
		}
		return runAttachedSubmit(ctx, client, *dataDir, *conversationID, *requestKey, task, options, *interactive, stdin, stdout, stderr, *asJSON)
	}
	if err != nil {
		return err
	}
	defer closeAll()
	tasks := harness.NewTaskService(service)
	if *collaborationMode != "" {
		settings, err := tasks.CollaborationSettings(ctx, *conversationID)
		if err != nil {
			return err
		}
		settings.Mode = *collaborationMode
		options.Collaboration = &settings
	}
	if *template != "" {
		task, err = tasks.Template(ctx, *template, task)
		if err != nil {
			return err
		}
	}
	var reader *terminalInput
	if *interactive {
		reader = newTerminalInput(ctx, stdin)
		tasks.SetApproval(terminalApproval(reader, stderr))
		options.Review = reviewChatSuggestion(reader, stderr)
	}
	streamed := false
	tasks.SetEvents(terminalEvents(stdout, stderr, *asJSON, &streamed))
	result, runErr := tasks.SubmitWithOptions(ctx, *conversationID, *requestKey, task, options)
	if reader != nil && runErr == nil {
		result, runErr = settleLocalControlProposals(ctx, tasks, result, reader.read, stderr)
	}
	if err := printResult(stdout, stderr, result, *asJSON, streamed); err != nil {
		return errors.Join(err, runErr)
	}
	return runOutcome(stderr, result, runErr)
}

func runContinue(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("continue", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	runID := flags.String("run", "", "run id")
	interactive := flags.Bool("interactive", false, "prompt to approve file edits")
	asJSON := flags.Bool("json", false, "write structured output")
	modelID := flags.String("model", "", "switch at a complete saved model/tool group before continuing")
	maxSteps := flags.Int("max-steps", 0, "explicitly increase this run's finite step limit")
	maxSeconds := flags.Int("max-seconds", 0, "explicitly increase this run's effective execution time limit")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*runID) == "" {
		return usageErr("freeagent continue: --run is required")
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	if err := requireModels(*dataDir, stderr); err != nil {
		return err
	}
	_, _, service, closeAll, err := openRuntime(ctx, *dataDir)
	if errors.Is(err, harness.ErrOwnerActive) {
		if err := rejectAttachedOptions("", *modelID, *maxSteps, *maxSeconds); err != nil {
			return err
		}
		client, attachErr := attachedOwner(ctx, *dataDir)
		if attachErr != nil {
			return fmt.Errorf("%w: %v", harness.ErrOwnerActive, attachErr)
		}
		run, _, _, inspectErr := client.Inspect(ctx, *runID)
		if inspectErr != nil {
			return inspectErr
		}
		return runAttachedContinue(ctx, client, *dataDir, run.ConversationID, *runID, *interactive, stdin, stdout, stderr, *asJSON)
	}
	if err != nil {
		return err
	}
	defer closeAll()
	tasks := harness.NewTaskService(service)
	if *modelID != "" {
		if err := tasks.SwitchModel(ctx, *runID, *modelID); err != nil {
			return err
		}
	}
	if *maxSteps != 0 || *maxSeconds != 0 {
		limits, err := tasks.Limits(ctx, *runID)
		if err != nil {
			return err
		}
		if *maxSteps != 0 {
			limits.MaxSteps = *maxSteps
		}
		if *maxSeconds != 0 {
			limits.MaxRunSeconds = *maxSeconds
		}
		if err := tasks.IncreaseLimits(ctx, *runID, limits); err != nil {
			return err
		}
	}
	options := harness.RunOptions{}
	var reader *terminalInput
	if *interactive {
		reader = newTerminalInput(ctx, stdin)
		tasks.SetApproval(terminalApproval(reader, stderr))
		options.Review = reviewChatSuggestion(reader, stderr)
	}
	streamed := false
	tasks.SetEvents(terminalEvents(stdout, stderr, *asJSON, &streamed))
	result, runErr := tasks.ContinueWithOptions(ctx, *runID, options)
	if reader != nil && runErr == nil {
		result, runErr = settleLocalControlProposals(ctx, tasks, result, reader.read, stderr)
	}
	if err := printResult(stdout, stderr, result, *asJSON, streamed); err != nil {
		return errors.Join(err, runErr)
	}
	return runOutcome(stderr, result, runErr)
}

func runChat(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	ctx, stopChat := context.WithCancel(ctx)
	defer stopChat()
	controller := &chatCancellation{idle: stopChat}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	go func() {
		for {
			select {
			case <-interrupts:
				controller.cancel()
			case <-ctx.Done():
				return
			}
		}
	}()
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	conversationID := flags.String("resume", "", "session id; created when omitted")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageError()
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(*dataDir, "config.json")); errors.Is(err, os.ErrNotExist) {
		return usagef("select a workspace first: freeagent init --workspace <directory> --data-dir %q", *dataDir)
	}
	if err := requireModels(*dataDir, stderr); err != nil {
		return err
	}
	local, closeAll, err := harness.OpenTasks(ctx, *dataDir, harness.Adapters{})
	var client *handoff.Client
	if errors.Is(err, harness.ErrOwnerActive) {
		client, err = interactiveOwner(ctx, *dataDir)
	} else if err == nil {
		defer closeAll()
		var owner *localOwner
		owner, err = startLocalOwner(ctx, local, *dataDir, 0, false)
		if err == nil {
			defer owner.Close()
			client = owner.client
			go func() {
				select {
				case <-owner.handler.StopRequested():
					stopChat()
				case <-ctx.Done():
				}
			}()
		}
	}
	if err != nil {
		return err
	}
	reader := newTerminalInput(ctx, stdin)
	tasks := &remoteChat{Client: client, data: *dataDir, reader: reader, stderr: stderr}
	if err := tasks.refresh(ctx); err != nil {
		return err
	}
	config := tasks.Config()
	if *conversationID == "" {
		conversation, err := tasks.NewConversation(ctx, "Interactive chat")
		if err != nil {
			return err
		}
		*conversationID = conversation.ID
		_, _ = fmt.Fprintln(stderr, "New conversation")
	} else {
		if _, err := tasks.Chat(ctx, *conversationID); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(stderr, "Workspace: %s\n/history /sessions /resume <number> /new /model /collaboration /settings /memory /status /continue /more /abandon /file <path> /begin ... /end /exit\n", config.Workspace)
	var references []string
	var nextCollaboration *harness.CollaborationSettings
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tasks.refresh(ctx); err != nil {
			return err
		}
		config = tasks.Config()
		_, _ = io.WriteString(stdout, "> ")
		line, readErr := reader.read(ctx)
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
		prompt := strings.TrimSpace(line)
		if prompt == "" {
			continue
		}
		if prompt == "/exit" || prompt == "/quit" {
			return nil
		}
		if prompt == "/history" {
			messages, err := tasks.Messages(ctx, *conversationID)
			if err != nil {
				return err
			}
			for _, item := range messages {
				_, _ = fmt.Fprintf(stdout, "%s: %s\n", item.Role, item.Content)
			}
			continue
		}
		if prompt == "/sessions" || strings.HasPrefix(prompt, "/resume ") {
			sessions, err := tasks.Conversations(ctx)
			if err != nil {
				return err
			}
			if prompt == "/sessions" {
				for index, session := range sessions {
					_, _ = fmt.Fprintf(stdout, "%d. %s\n", index+1, session.Title)
				}
			} else {
				number, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(prompt, "/resume ")))
				if err != nil || number < 1 || number > len(sessions) || sessions[number-1].Workspace != config.Workspace {
					_, _ = fmt.Fprintln(stderr, "Choose a listed conversation in this workspace.")
				} else {
					*conversationID = sessions[number-1].ID
					_, _ = fmt.Fprintln(stderr, "Conversation:", sessions[number-1].Title)
					references = nil
					nextCollaboration = nil
				}
			}
			continue
		}
		if prompt == "/new" {
			conversation, err := tasks.NewConversation(ctx, "Interactive chat")
			if err != nil {
				return err
			}
			*conversationID = conversation.ID
			references = nil
			nextCollaboration = nil
			_, _ = fmt.Fprintln(stderr, "New conversation")
			continue
		}
		if prompt == "/status" {
			last, err := tasks.Latest(ctx, *conversationID)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, "No task yet.")
			} else {
				_, _, evidence, err := tasks.Inspect(ctx, last.ID)
				if err != nil {
					return err
				}
				_ = printChatResult(stdout, stderr, harness.RunResult{Run: last, Answer: last.Output, Evidence: evidence}, false)
			}
			continue
		}
		if prompt == "/model" || strings.HasPrefix(prompt, "/model ") {
			err := chatModelSelection(ctx, tasks, *conversationID, strings.TrimSpace(strings.TrimPrefix(prompt, "/model")), stdout, stderr)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
			}
			continue
		}
		if prompt == "/collaboration" || strings.HasPrefix(prompt, "/collaboration ") {
			nextCollaboration, err = chatCollaboration(ctx, tasks, *conversationID,
				strings.TrimSpace(strings.TrimPrefix(prompt, "/collaboration")), reader, stderr)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
			}
			continue
		}
		if prompt == "/settings" || strings.HasPrefix(prompt, "/settings ") {
			if err := chatSettings(ctx, tasks, *conversationID,
				strings.TrimSpace(strings.TrimPrefix(prompt, "/settings")), reader, stdout, stderr); err != nil {
				_, _ = fmt.Fprintln(stderr, err)
			}
			continue
		}
		if prompt == "/memory" || strings.HasPrefix(prompt, "/memory ") {
			if err := chatMemory(ctx, tasks, *conversationID,
				strings.TrimSpace(strings.TrimPrefix(prompt, "/memory")), reader, stdout, stderr); err != nil {
				_, _ = fmt.Fprintln(stderr, err)
			}
			continue
		}
		if prompt == "/more" {
			err := chatIncreaseLimits(ctx, tasks, *conversationID, reader, stderr)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				continue
			}
			prompt = "/continue"
		}
		if strings.HasPrefix(prompt, "/file ") {
			name := strings.TrimSpace(strings.TrimPrefix(prompt, "/file "))
			if len(references) >= 16 {
				_, _ = fmt.Fprintln(stderr, "At most 16 file references per input.")
			} else {
				references = append(references, name)
				_, _ = fmt.Fprintln(stderr, "Reference selected:", name)
			}
			continue
		}
		if prompt == "/begin" {
			var text strings.Builder
			for {
				line, err := reader.read(ctx)
				if err != nil {
					return err
				}
				if strings.TrimSpace(line) == "/end" {
					break
				}
				if text.Len()+len(line) > config.MaxContextBytes {
					return harness.ErrContextLimit
				}
				text.WriteString(line)
			}
			prompt = strings.TrimSpace(text.String())
			if prompt == "" {
				continue
			}
		}
		if prompt == "/abandon" {
			last, err := tasks.Latest(ctx, *conversationID)
			if err == nil {
				err = tasks.Abandon(ctx, last.ID)
			}
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
			} else {
				_, _ = fmt.Fprintln(stderr, "Task ended. Unresolved attempt records, if any, were retained.")
			}
			continue
		}
		if strings.HasPrefix(prompt, "/") && prompt != "/continue" {
			_, _ = fmt.Fprintln(stderr, "Unknown session command.")
			continue
		}
		if prompt != "/continue" && len(references) > 0 {
			prompt, err = tasks.ExpandReferences(ctx, prompt, references)
			if err != nil {
				_, _ = fmt.Fprintln(stderr, err)
				continue
			}
			references = nil
		}
		requestKey := fmt.Sprintf("%x", time.Now().UTC().UnixNano())
		streamed := false
		tasks.emit = terminalEvents(stdout, stderr, false, &streamed)
		state, stateErr := tasks.State(ctx)
		if stateErr != nil {
			return stateErr
		}
		tasks.cursor = state.LastSeq
		tasks.requestError = ""
		options := harness.RunOptions{Collaboration: nextCollaboration, Review: reviewChatSuggestion(reader, stderr)}
		var result harness.RunResult
		binding := requestKey
		if prompt == "/continue" {
			if last, latestErr := tasks.Latest(ctx, *conversationID); latestErr != nil {
				err = latestErr
			} else {
				binding = last.ID
				err = tasks.Continue(ctx, *conversationID, last.ID)
				result.Run = last
			}
		} else {
			if options.Collaboration == nil {
				settings, settingsErr := tasks.CollaborationSettings(ctx, *conversationID)
				err = settingsErr
				options.Collaboration = &settings
			}
			if err == nil {
				err = tasks.SubmitWithOptions(ctx, *conversationID, requestKey, prompt, options)
			}
			nextCollaboration = nil
		}
		if err == nil {
			tasks.bind(binding)
			taskCtx, cancelTask := context.WithCancel(ctx)
			controller.set(func() {
				tasks.interrupt(*conversationID)
				cancelTask()
			})
			result, err = tasks.wait(taskCtx, *conversationID, requestKey, result.Run.ID)
			if err == nil {
				result, err = tasks.settle(taskCtx, result, options)
			}
			controller.set(nil)
			cancelTask()
		}
		if tasks.emit == nil {
			streamed = false
		}
		_ = printChatResult(stdout, stderr, result, streamed)
		if result.Run.ID != "" {
			if sources, sourceErr := tasks.MemorySources(ctx, result.Run.ID); sourceErr == nil && len(sources) != 0 {
				_, _ = fmt.Fprintf(stderr, "Memory sources: %d; /memory sources\n", len(sources))
			}
		}
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "error:", err)
			continue
		}
	}
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageError()
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	config, err := harness.LoadConfig(*dataDir)
	if err != nil {
		return err
	}
	if err := harness.VerifyData(ctx, *dataDir); err != nil {
		return err
	}
	report := map[string]any{"ok": true, "data_dir": *dataDir, "workspace": config.Workspace, "models": config.Models,
		"default_model": config.DefaultModel, "network_tested": false, "encrypted": harness.KeysEncrypted()}
	// Key profiles are listed without secrets; a broken keys file is reported
	// instead of hiding the rest of the diagnosis.
	var keysErr error
	store, err := keyStore()
	if err == nil {
		report["keys_file"] = store.Path()
		var views []harness.KeyProfileView
		views, keysErr = store.List()
		keys := make([]map[string]any, 0, len(views))
		for _, view := range views {
			keys = append(keys, map[string]any{"name": view.Name, "source": keySourceText(view), "last_check": view.LastCheck})
		}
		report["keys"] = keys
	} else {
		keysErr = err
	}
	if keysErr != nil {
		report["ok"], report["keys_error"] = false, keysErr.Error()
	}
	// The sandbox only matters when command_run would use it.
	sandbox := harness.CheckSandbox(ctx)
	report["sandbox"] = sandbox
	sandboxNeeded := config.Commands.Enabled && config.Commands.Execution != harness.CommandExecutionHostExplicit
	sandboxFailed := sandboxNeeded && sandbox.Status != "ready"
	if sandboxFailed {
		report["ok"] = false
	}
	if err := writeJSON(stdout, report); err != nil {
		return err
	}
	if keysErr != nil {
		return &exitError{code: exitCode(keysErr)}
	}
	if sandboxFailed {
		return &exitError{code: 1}
	}
	return nil
}

func openRuntime(ctx context.Context, dataDir string) (harness.Config, *harness.Store, *harness.Service, func(), error) {
	return harness.OpenRuntime(ctx, dataDir, harness.Adapters{})
}

// requireModels stops chat, run and continue before they take the owner lock
// when no model is configured, and says how to add one.
func requireModels(dataDir string, stderr io.Writer) error {
	config, err := harness.LoadConfig(dataDir)
	if err != nil {
		return err
	}
	if len(config.Models) != 0 {
		return nil
	}
	_, _ = fmt.Fprintln(stderr, `No model is configured. Add a key, then a model that uses it:
  freeagent key add --name <name> --url <address>
  freeagent config model --id <id> --key <name> --model <model>`)
	return &exitError{code: exitUsage}
}

func terminalApproval(reader *terminalInput, stdout io.Writer) harness.ApprovalFunc {
	return func(ctx context.Context, call harness.ToolCall, prepared harness.PreparedTool) (bool, error) {
		_, _ = fmt.Fprintf(stdout, "\nApprove %s?\n%s\n[y/N] ", call.Name, prepared.Preview)
		line, err := reader.read(ctx)
		if err != nil {
			return false, err
		}
		return strings.EqualFold(strings.TrimSpace(line), "y"), nil
	}
}

func terminalEvents(stdout, stderr io.Writer, asJSON bool, streamed *bool) harness.EventFunc {
	return func(event harness.Event) {
		switch event.Kind {
		case "model_text":
			if !asJSON {
				_, _ = io.WriteString(stdout, event.Message)
				*streamed = true
			}
		case "tool_started", "tool_finished":
			if event.Tool != "" {
				_, _ = fmt.Fprintf(stderr, "[%s] %s\n", event.Kind, event.Tool)
			}
		case "model_selected":
			_, _ = fmt.Fprintln(stderr, event.Message)
		case "confirmation":
			if asJSON {
				_, _ = fmt.Fprintf(stderr, "[confirmation] %s\n%s\n", event.Tool, event.Preview)
			}
		case "unresolved":
			_, _ = fmt.Fprintln(stderr, "[unresolved]", event.Message)
		}
	}
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// defaultDataDir is the --data-dir default. When the location cannot be
// determined it is empty, and dataDirOrError reports why after parsing.
func defaultDataDir() string {
	directory, err := apppaths.DataDir()
	if err != nil {
		return ""
	}
	return directory
}

// dataDirOrError rejects an empty data directory with the apppaths error, so a
// bad portable manifest never falls back to another location.
func dataDirOrError(value string) (string, error) {
	if value != "" {
		return value, nil
	}
	_, err := apppaths.DataDir()
	if err == nil {
		err = errors.New("data directory is empty")
	}
	return "", &exitError{code: exitUsage, err: fmt.Errorf("freeagent: %w; pass --data-dir <directory>", err)}
}

func resolveDataDir(value *string) error {
	resolved, err := dataDirOrError(*value)
	*value = resolved
	return err
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		value = value[:index]
	}
	if len(value) > 80 {
		value = string([]rune(value)[:min(len([]rune(value)), 40)])
	}
	return value
}

type inputResult struct {
	line string
	err  error
}

type terminalInput struct {
	results chan inputResult
}

func newTerminalInput(lifetime context.Context, source io.Reader) *terminalInput {
	input := &terminalInput{results: make(chan inputResult, 1)}
	reader := bufio.NewReaderSize(source, 4096)
	// One reader owns the input even when a per-run approval deadline expires.
	go func() {
		defer close(input.results)
		for {
			var line strings.Builder
			var readErr error
			for {
				part, err := reader.ReadSlice('\n')
				if line.Len()+len(part) > 1<<20 {
					readErr = errors.New("input exceeds 1 MiB")
					break
				}
				line.Write(part)
				if err == bufio.ErrBufferFull {
					continue
				}
				readErr = err
				if err == io.EOF && line.Len() > 0 {
					readErr = nil
				}
				break
			}
			select {
			case input.results <- inputResult{line.String(), readErr}:
			case <-lifetime.Done():
				return
			}
			if readErr != nil {
				return
			}
		}
	}()
	return input
}

func (input *terminalInput) read(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case result, ok := <-input.results:
		if !ok {
			return "", io.EOF
		}
		return result.line, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func printResult(stdout, stderr io.Writer, result harness.RunResult, asJSON, streamed bool) error {
	if asJSON {
		return writeJSON(stdout, result)
	}
	return printChatResult(stdout, stderr, result, streamed)
}

func runTools(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "list" && args[0] != "test") {
		return usageErr("usage: freeagent tools list | tools test [--data-dir <directory>] <server>")
	}
	flags := flag.NewFlagSet("tools list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	server := ""
	arguments := args[1:]
	if args[0] == "test" && len(arguments) > 0 && !strings.HasPrefix(arguments[0], "-") {
		server, arguments = arguments[0], arguments[1:]
	}
	if err := parseFlags(flags, arguments); err != nil {
		return err
	}
	if args[0] == "test" && server == "" && flags.NArg() == 1 {
		server = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return usageError()
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	config, err := harness.LoadConfig(*dataDir)
	if err != nil {
		return err
	}
	if err := harness.VerifyData(ctx, *dataDir); err != nil {
		return err
	}
	tools, err := harness.NewFileTools(config.Workspace, *dataDir)
	if err != nil {
		return err
	}
	defer tools.Close()
	if args[0] == "list" {
		definitions := tools.Definitions()
		definitions = append(definitions, (&harness.CommandTools{Config: config.Commands}).Definitions()...)
		definitions = append(definitions, (&harness.HTTPTools{Config: config.HTTP}).Definitions()...)
		servers := map[string]any{}
		for name, server := range config.MCPServers {
			servers[name] = map[string]any{"enabled": server.Enabled, "probed": false}
		}
		return writeJSON(stdout, map[string]any{"builtin": definitions, "mcp": servers, "network_tested": false})
	}
	if server == "" {
		return usageErr("tools test: server name is required")
	}
	_, _ = fmt.Fprintln(stderr, "Starting the selected trusted MCP program for handshake and tool discovery. No tools/call will be sent.")
	runtimeTools := harness.NewRuntimeTools(tools)
	defer runtimeTools.Close()
	if err := runtimeTools.Connect(ctx, config, server); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{"server": server, "tools": runtimeTools.Definitions(), "rejected": runtimeTools.Rejected, "tool_calls": 0})
}

func runBackupCommand(ctx context.Context, command string, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory; restore requires a new target")
	source := flags.String("source", "", "current-format backup directory")
	target := flags.String("out", "", "new backup directory")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageError()
	}
	switch command {
	case "restore":
		if *source == "" || *dataDir == "" || *dataDir == defaultDataDir() {
			return usageErr("restore: --source and an explicit new --data-dir are required")
		}
		if err := harness.Restore(ctx, *source, *dataDir); err != nil {
			return err
		}
	case "backup-verify":
		if *source == "" {
			return usageErr("backup-verify: --source is required")
		}
		if err := harness.VerifyBackup(ctx, *source); err != nil {
			return err
		}
	case "backup":
		if *target == "" {
			return usageErr("backup: --out is required")
		}
		if err := resolveDataDir(dataDir); err != nil {
			return err
		}
		store, err := harness.OpenStore(ctx, *dataDir)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.Backup(ctx, *target); err != nil {
			return err
		}
	}
	return writeJSON(stdout, map[string]any{"ok": true, "command": command, "data_dir": *dataDir, "source": *source, "out": *target})
}

func runInspect(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	runID := flags.String("run", "", "run id")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *runID == "" {
		return usageErr("inspect: --run is required")
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	store, err := openStoreForReading(ctx, *dataDir, stderr)
	if err != nil {
		return err
	}
	defer store.Close()
	tasks := harness.NewTaskService(&harness.Service{Store: store})
	run, attempts, evidence, err := tasks.Inspect(ctx, *runID)
	if err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{"run": run, "attempts": attempts, "evidence": evidence})
}

func runAbandon(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("abandon", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultDataDir(), "harness data directory")
	runID := flags.String("run", "", "run id")
	if err := parseFlags(flags, args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *runID == "" {
		return usageErr("abandon: --run is required")
	}
	if err := resolveDataDir(dataDir); err != nil {
		return err
	}
	store, err := harness.OpenStore(ctx, *dataDir)
	if err != nil {
		return err
	}
	defer store.Close()
	tasks := harness.NewTaskService(&harness.Service{Store: store})
	if err := tasks.Abandon(ctx, *runID); err != nil {
		return err
	}
	item, _, _, err := tasks.Inspect(ctx, *runID)
	if err != nil {
		return err
	}
	return writeJSON(stdout, item)
}
