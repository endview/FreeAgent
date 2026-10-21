package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/endview/freeagent/internal/handoff"
	"github.com/endview/freeagent/internal/harness"
)

const deletionBoundary = "Deletion removes this memory's current body, body revisions and search index. It does not retract sent Provider requests, historical Chat snippets or old backups."

// Management is offline, including when MCP programs are configured.
func openMemoryTasks(ctx context.Context, data string) (*harness.TaskService, func(), error) {
	config, err := harness.LoadConfig(data)
	if err != nil {
		return nil, func() {}, err
	}
	store, err := harness.OpenStore(ctx, data)
	if err != nil {
		return nil, func() {}, err
	}
	return harness.NewTaskService(&harness.Service{Store: store, Config: config}), func() { _ = store.Close() }, nil
}

func interactiveMemoryOwner(ctx context.Context, dataDir string) (*handoff.Client, bool, error) {
	_, _, _, err := handoff.Read(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("owner discovery is unavailable: %w", err)
	}
	client, err := interactiveOwner(ctx, dataDir)
	if err != nil {
		return nil, true, err
	}
	return client, true, nil
}

func runMemory(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageErr("usage: memory <scopes|list|read|save|save-result|update|disable|enable|delete|copy|import|search|sources|select|clear|agent> [flags]")
	}
	command := args[0]
	flags := flag.NewFlagSet("memory "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	data := flags.String("data-dir", defaultDataDir(), "current Harness data directory")
	chat := flags.String("session", "", "explicit current session")
	scopeKind := flags.String("scope", "private", "private, shared or knowledge")
	owner := flags.String("owner", "", "Agent identity, shared parent or collection name; private defaults to current Agent")
	task := flags.String("task", "", "private task-local scope")
	personal := flags.Bool("personal", false, "personal retained memory/collection instead of workspace scope")
	var entries, collections repeatedStrings
	flags.Var(&entries, "entry", "selected entry ID; repeat only for select")
	flags.Var(&collections, "collection", "explicitly selected collection name; repeat")
	taskPrivate := flags.Bool("task-private", false, "explicitly permit task-local private tools for future tasks")
	runID := flags.String("run", "", "saved task output or source inspection")
	title := flags.String("title", "", "memory/document title")
	kind := flags.String("kind", "finding", "entry type, not execution evidence")
	text := flags.String("text", "", "exact user text to save")
	fromStdin := flags.Bool("stdin", false, "read bounded exact text from stdin")
	key := flags.String("request", "", "stable memory operation key")
	revision := flags.Int("revision", 0, "expected revision for a change")
	file := flags.String("file", "", "explicit workspace UTF-8 text/Markdown document")
	query := flags.String("query", "", "literal search text")
	limit := flags.Int("limit", 16, "bounded listing/search result count")
	maxBytes := flags.Int("max-bytes", 8192, "bounded search result bytes")
	yes := flags.Bool("yes", false, "explicitly save this exact preview; otherwise preview only")
	update := flags.Bool("update", false, "explicitly update an imported document; needs entry and revision")
	fromKind := flags.String("from-scope", "private", "copy source scope")
	fromOwner := flags.String("from-owner", "", "copy source owner; private defaults to current Agent")
	fromTask := flags.String("from-task", "", "copy source private task")
	fromPersonal := flags.Bool("from-personal", false, "copy from a personal source")
	fromEntry := flags.String("from-entry", "", "exact entry to copy")
	reuse := flags.String("reuse-session", "", "explicitly reuse the Agent identity from this session")
	if err := parseFlags(flags, args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("memory: unexpected arguments")
	}
	if err := resolveDataDir(data); err != nil {
		return err
	}
	configForMemory, err := harness.LoadConfig(*data)
	if err != nil {
		return err
	}
	if *fromStdin {
		raw, err := io.ReadAll(io.LimitReader(stdin, int64(configForMemory.Memory.MaxDocumentBytes)+1))
		if err != nil || len(raw) > configForMemory.Memory.MaxDocumentBytes {
			return harness.ErrContextLimit
		}
		*text = string(raw)
	}
	if *file != "" {
		absoluteFile, err := filepath.Abs(*file)
		if err != nil {
			return err
		}
		*file = absoluteFile
	}
	if command == "delete" {
		_, _ = fmt.Fprintln(stderr, deletionBoundary)
	}
	if client, attached, attachErr := interactiveMemoryOwner(ctx, *data); attached {
		if attachErr != nil {
			return attachErr
		}
		request := harness.MemoryCommand{
			Command: command, ConversationID: *chat, ScopeKind: *scopeKind, Owner: *owner, TaskID: *task,
			Personal: *personal, Entries: append([]string(nil), entries...), Collections: append([]string(nil), collections...),
			TaskPrivate: *taskPrivate, RunID: *runID, Title: *title, Kind: *kind, Body: *text, RequestKey: *key,
			ExpectedRevision: *revision, File: *file, Query: *query, Limit: *limit, MaxBytes: *maxBytes, Yes: *yes,
			Update: *update, FromScopeKind: *fromKind, FromOwner: *fromOwner, FromTaskID: *fromTask,
			FromPersonal: *fromPersonal, FromEntry: *fromEntry, ReuseSession: *reuse,
		}
		flags.Visit(func(item *flag.Flag) {
			switch item.Name {
			case "title":
				request.TitleProvided = true
			case "kind":
				request.KindProvided = true
			}
		})
		raw, err := client.Memory(ctx, request)
		if err != nil {
			return err
		}
		_, err = stdout.Write(raw)
		if err == nil && (len(raw) == 0 || raw[len(raw)-1] != '\n') {
			_, err = io.WriteString(stdout, "\n")
		}
		return err
	}
	tasks, closeAll, err := openMemoryTasks(ctx, *data)
	if err != nil {
		return err
	}
	defer closeAll()
	if command == "sources" {
		if *runID == "" {
			return usageErr("memory sources: --run is required")
		}
		sources, err := tasks.MemorySources(ctx, *runID)
		if err != nil {
			return err
		}
		return writeJSON(stdout, sources)
	}
	if *chat == "" {
		return usageErr("memory: --session is required")
	}
	agent, err := tasks.AgentIdentity(ctx, *chat)
	if err != nil {
		return err
	}
	if command == "agent" {
		if *reuse != "" {
			agent, err = tasks.AgentIdentity(ctx, *reuse)
			if err == nil {
				err = tasks.SelectAgent(ctx, *chat, agent)
			}
			if err != nil {
				return err
			}
		}
		return writeJSON(stdout, map[string]string{"agent_id": agent})
	}
	project := tasks.Config().Workspace
	if *personal {
		project = ""
	}
	if *owner == "" && *scopeKind == "private" {
		*owner = agent
	}
	scope := harness.MemoryScope{Kind: *scopeKind, Owner: *owner, Project: project, TaskID: *task}
	if command == "select" || command == "clear" {
		selection := harness.MemorySelection{}
		if command == "select" {
			selection.TaskPrivate = *taskPrivate
			for _, id := range entries {
				selection.Entries = append(selection.Entries, harness.MemoryReference{
					Scope: harness.MemoryScope{Kind: "private", Owner: agent, Project: project}, EntryID: id})
			}
			for _, name := range collections {
				selection.Knowledge = append(selection.Knowledge, harness.MemoryScope{Kind: "knowledge", Owner: name, Project: project})
			}
		}
		control, err := tasks.Control()
		if err != nil {
			return err
		}
		if err := control.ApplyMemorySelection(ctx, *chat, selection, "cli"); err != nil {
			return err
		}
		return writeJSON(stdout, selection)
	}
	if command == "scopes" {
		scopes, err := tasks.MemoryScopes(ctx, *chat, *scopeKind)
		if err != nil {
			return err
		}
		return writeJSON(stdout, scopes)
	}
	if command == "list" {
		items, err := tasks.ListMemory(ctx, *chat, scope, *limit)
		if err != nil {
			return err
		}
		return writeJSON(stdout, items)
	}
	if command == "search" {
		hits, err := tasks.SearchKnowledge(ctx, *chat, scope, *query, min(*limit, 16), *maxBytes)
		if err != nil {
			return err
		}
		return writeJSON(stdout, hits)
	}
	entry := ""
	if len(entries) == 1 {
		entry = entries[0]
	} else if len(entries) > 1 {
		return errors.New("memory: at most one --entry outside select")
	}
	if command == "read" {
		item, err := tasks.ReadMemory(ctx, *chat, scope, entry)
		if err != nil {
			return err
		}
		return writeJSON(stdout, item)
	}
	if *key == "" {
		*key = fmt.Sprintf("user-%x", time.Now().UTC().UnixNano())
	}
	mutation := harness.MemoryMutation{RequestKey: *key, Action: command, Scope: scope, EntryID: entry,
		ExpectedRevision: *revision, Kind: *kind, Title: *title, Body: *text,
		Sources: []harness.MemorySource{{Kind: "user"}}}
	switch command {
	case "save":
		mutation.Action = "create"
	case "update":
		current, err := tasks.ReadMemory(ctx, *chat, scope, entry)
		if err != nil {
			return err
		}
		provided := map[string]bool{}
		flags.Visit(func(item *flag.Flag) { provided[item.Name] = true })
		if !provided["title"] {
			mutation.Title = current.Title
		}
		if !provided["kind"] {
			mutation.Kind = current.Kind
		}
	case "disable", "enable", "delete":
		mutation.Kind, mutation.Title, mutation.Body, mutation.Sources = "", "", "", nil
	case "import":
		path, err := filepath.Abs(*file)
		if err != nil || *file == "" {
			return usageErr("memory import: --file is required")
		}
		mutation, err = tasks.PreviewKnowledgeImport(ctx, *chat, scope, path, *title, *key)
		if err != nil {
			return err
		}
		if *update {
			mutation.Action, mutation.EntryID, mutation.ExpectedRevision = "update", entry, *revision
		}
	case "copy":
		sourceProject := tasks.Config().Workspace
		if *fromPersonal {
			sourceProject = ""
		}
		if *fromOwner == "" && *fromKind == "private" {
			*fromOwner = agent
		}
		source := harness.MemoryReference{Scope: harness.MemoryScope{Kind: *fromKind, Owner: *fromOwner,
			Project: sourceProject, TaskID: *fromTask}, EntryID: *fromEntry}
		mutation, err = tasks.PreviewMemoryCopy(ctx, *chat, source, scope, *kind, *title, *key)
		if err != nil {
			return err
		}
	case "save-result":
		mutation, err = tasks.PreviewRunMemory(ctx, *chat, *runID, scope, *kind, *title, *key)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("memory: unknown operation %q", command)
	}
	if !*yes {
		return writeJSON(stdout, map[string]any{"preview": mutation, "saved": false})
	}
	receipt, err := tasks.SaveMemory(ctx, *chat, mutation)
	if err != nil {
		return err
	}
	return writeJSON(stdout, receipt)
}
