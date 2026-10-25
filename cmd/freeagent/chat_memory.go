package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

func applyChatMemorySelection(ctx context.Context, tasks chatTasks, chat string, selection harness.MemorySelection) error {
	return tasks.ApplyUserMemorySelection(ctx, chat, selection)
}

func memoryLine(ctx context.Context, reader *terminalInput, writer io.Writer, label string) (string, error) {
	_, _ = fmt.Fprint(writer, label)
	line, err := reader.read(ctx)
	return strings.TrimSpace(line), err
}

func memoryNumber(ctx context.Context, reader *terminalInput, writer io.Writer, count int) (int, error) {
	line, err := memoryLine(ctx, reader, writer, "Selection: ")
	if err != nil {
		return 0, err
	}
	number, err := strconv.Atoi(line)
	if err != nil || number < 1 || number > count {
		return 0, errors.New("choose a listed number")
	}
	return number - 1, nil
}

func memoryText(ctx context.Context, reader *terminalInput, writer io.Writer, maxBytes int) (string, error) {
	_, _ = fmt.Fprintln(writer, "Content (. on its own ends):")
	var body strings.Builder
	for {
		line, err := reader.read(ctx)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(line) == "." {
			break
		}
		if body.Len()+len(line) > maxBytes {
			return "", harness.ErrContextLimit
		}
		body.WriteString(line)
	}
	return strings.TrimRight(body.String(), "\r\n"), nil
}

func memoryScopeLabel(ctx context.Context, tasks chatTasks, scope harness.MemoryScope) string {
	project := "project"
	if scope.Project == "" {
		project = "personal"
	}
	switch scope.Kind {
	case "knowledge":
		return scope.Owner + " (" + project + ")"
	case "shared":
		run, _, _, err := tasks.Inspect(ctx, scope.Owner)
		if err == nil {
			return "Shared: " + firstLine(run.Prompt)
		}
		return "Task shared area"
	default:
		if scope.TaskID != "" {
			run, _, _, err := tasks.Inspect(ctx, scope.TaskID)
			if err == nil {
				return "Task-only: " + firstLine(run.Prompt)
			}
			return "Task-only context"
		}
		return "Current Agent (retained, " + project + ")"
	}
}

func chatMemoryScope(ctx context.Context, tasks chatTasks, chat, kind string, create bool, reader *terminalInput, writer io.Writer) (harness.MemoryScope, error) {
	scopes, err := tasks.MemoryScopes(ctx, chat, kind)
	if err != nil {
		return harness.MemoryScope{}, err
	}
	if kind == "private" {
		agent, err := tasks.AgentIdentity(ctx, chat)
		if err != nil {
			return harness.MemoryScope{}, err
		}
		scopes = slices.DeleteFunc(scopes, func(scope harness.MemoryScope) bool { return scope.Owner != agent })
	}
	for index, scope := range scopes {
		_, _ = fmt.Fprintf(writer, "%d. %s\n", index+1, memoryScopeLabel(ctx, tasks, scope))
	}
	if create {
		_, _ = fmt.Fprintf(writer, "%d. New collection\n", len(scopes)+1)
	}
	if len(scopes) == 0 && !create {
		return harness.MemoryScope{}, errors.New("no saved entries in this area")
	}
	number, err := memoryNumber(ctx, reader, writer, len(scopes)+boolInt(create))
	if err != nil {
		return harness.MemoryScope{}, err
	}
	if number < len(scopes) {
		return scopes[number], nil
	}
	name, err := memoryLine(ctx, reader, writer, "Collection name: ")
	if err != nil {
		return harness.MemoryScope{}, err
	}
	line, err := memoryLine(ctx, reader, writer, "1. Personal 2. Project [2]: ")
	if err != nil {
		return harness.MemoryScope{}, err
	}
	scope := harness.MemoryScope{Kind: "knowledge", Owner: name, Project: tasks.Config().Workspace}
	if line == "1" {
		scope.Project = ""
	} else if line != "" && line != "2" {
		return scope, errors.New("choose personal or project")
	}
	return scope, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func chatMemoryDestination(ctx context.Context, tasks chatTasks, chat string, reader *terminalInput, writer io.Writer) (harness.MemoryScope, bool, error) {
	_, _ = fmt.Fprintln(writer, "Save to:\n1. Current Agent\n2. Current task shared area\n3. Personal or project knowledge\n4. Do not save")
	number, err := memoryNumber(ctx, reader, writer, 4)
	if err != nil {
		return harness.MemoryScope{}, false, err
	}
	if number == 3 {
		return harness.MemoryScope{}, false, nil
	}
	if number == 0 {
		agent, err := tasks.AgentIdentity(ctx, chat)
		if err != nil {
			return harness.MemoryScope{}, false, err
		}
		scope := harness.MemoryScope{Kind: "private", Owner: agent, Project: tasks.Config().Workspace}
		line, err := memoryLine(ctx, reader, writer, "1. Current task only 2. Retain for future tasks: ")
		if err != nil {
			return scope, false, err
		}
		if line == "1" {
			run, err := tasks.Latest(ctx, chat)
			if err != nil {
				return scope, false, errors.New("no current task")
			}
			scope.TaskID = run.ID
		} else if line != "2" {
			return scope, false, errors.New("choose task-only or retained")
		}
		return scope, true, nil
	}
	if number == 1 {
		run, err := tasks.Latest(ctx, chat)
		if err != nil {
			return harness.MemoryScope{}, false, errors.New("no current collaboration task")
		}
		controlled, err := tasks.IsCollaboration(ctx, run.ID)
		if err != nil || !controlled {
			return harness.MemoryScope{}, false, errors.New("current task is not a collaboration")
		}
		return harness.MemoryScope{Kind: "shared", Owner: run.ID, Project: tasks.Config().Workspace}, true, nil
	}
	if !tasks.Config().Memory.KnowledgeEnabled {
		return harness.MemoryScope{}, false, errors.New("knowledge is disabled; enable it in advanced memory settings")
	}
	scope, err := chatMemoryScope(ctx, tasks, chat, "knowledge", true, reader, writer)
	return scope, err == nil, err
}

func chatMemoryCommit(ctx context.Context, tasks chatTasks, chat string, mutation harness.MemoryMutation, reader *terminalInput, writer io.Writer) error {
	_, _ = fmt.Fprintf(writer, "\n%s: %s\nDestination: %s\n", mutation.Action, mutation.Title, memoryScopeLabel(ctx, tasks, mutation.Scope))
	for _, source := range mutation.Sources {
		label := source.Kind
		if source.Kind == "file" {
			label = source.Path + "\nSHA-256: " + source.Hash
		} else if source.Kind == "memory" {
			label = "Explicitly selected memory revision"
		} else if source.Kind == "run" {
			label = "Completed saved task output (not independent verification)"
		}
		_, _ = fmt.Fprintln(writer, "Source:", label)
	}
	if mutation.Body != "" {
		_, _ = fmt.Fprintln(writer, mutation.Body)
	}
	if mutation.Action == "delete" {
		_, _ = fmt.Fprintln(writer, deletionBoundary)
	}
	line, err := memoryLine(ctx, reader, writer, "Confirm this exact change? [y/N] ")
	if err != nil {
		return err
	}
	if !strings.EqualFold(line, "y") {
		_, _ = fmt.Fprintln(writer, "Not saved.")
		return nil
	}
	receipt, err := tasks.SaveMemory(ctx, chat, mutation)
	if err != nil {
		return err
	}
	if receipt.Duplicate {
		_, _ = fmt.Fprintln(writer, "Already recorded; no duplicate was added.")
	} else {
		_, _ = fmt.Fprintln(writer, "Memory change saved.")
	}
	return nil
}

func chatMemoryKind(ctx context.Context, reader *terminalInput, writer io.Writer, retained bool) (string, error) {
	kinds := []string{"fact", "hypothesis", "finding", "decision", "question", "artifact", "background"}
	if retained {
		kinds = append(kinds, "preference")
	}
	for i, kind := range kinds {
		_, _ = fmt.Fprintf(writer, "%d. %s\n", i+1, kind)
	}
	number, err := memoryNumber(ctx, reader, writer, len(kinds))
	if err != nil {
		return "", err
	}
	return kinds[number], nil
}

func chatMemorySave(ctx context.Context, tasks chatTasks, chat string, fromResult bool, reader *terminalInput, writer io.Writer) error {
	scope, save, err := chatMemoryDestination(ctx, tasks, chat, reader, writer)
	if err != nil || !save {
		return err
	}
	title, err := memoryLine(ctx, reader, writer, "Title: ")
	if err != nil {
		return err
	}
	kind, err := chatMemoryKind(ctx, reader, writer, scope.Kind == "private" && scope.TaskID == "")
	if err != nil {
		return err
	}
	key := fmt.Sprintf("user-%x", time.Now().UTC().UnixNano())
	var mutation harness.MemoryMutation
	if fromResult {
		run, err := tasks.Latest(ctx, chat)
		if err != nil {
			return err
		}
		mutation, err = tasks.PreviewRunMemory(ctx, chat, run.ID, scope, kind, title, key)
		if err != nil {
			return err
		}
	} else {
		body, err := memoryText(ctx, reader, writer, tasks.Config().Memory.MaxDocumentBytes)
		if err != nil {
			return err
		}
		mutation = harness.MemoryMutation{RequestKey: key, Action: "create", Scope: scope,
			Title: title, Kind: kind, Body: body, Sources: []harness.MemorySource{{Kind: "user"}}}
	}
	return chatMemoryCommit(ctx, tasks, chat, mutation, reader, writer)
}

func chatMemoryManage(ctx context.Context, tasks chatTasks, chat string, reader *terminalInput, stdout, writer io.Writer) error {
	_, _ = fmt.Fprintln(writer, "1. Current Agent\n2. Task shared areas\n3. Knowledge")
	number, err := memoryNumber(ctx, reader, writer, 3)
	if err != nil {
		return err
	}
	scope, err := chatMemoryScope(ctx, tasks, chat, []string{"private", "shared", "knowledge"}[number], false, reader, writer)
	if err != nil {
		return err
	}
	entries, err := tasks.ListMemory(ctx, chat, scope, 100)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("no entries in this area")
	}
	for index, entry := range entries {
		_, _ = fmt.Fprintf(writer, "%d. %s (%s, %s)\n", index+1, entry.Title, entry.Kind, entry.State)
	}
	number, err = memoryNumber(ctx, reader, writer, len(entries))
	if err != nil {
		return err
	}
	entry, err := tasks.ReadMemory(ctx, chat, scope, entries[number].ID)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "%s\n%s\n", entry.Title, entry.Body)
	_, _ = fmt.Fprintln(writer, "1. Back\n2. Edit\n3. Disable\n4. Enable\n5. Delete\n6. Copy/share explicitly\n7. Update from file")
	number, err = memoryNumber(ctx, reader, writer, 7)
	if err != nil || number == 0 {
		return err
	}
	mutation := harness.MemoryMutation{RequestKey: fmt.Sprintf("user-%x", time.Now().UTC().UnixNano()),
		Scope: scope, EntryID: entry.ID, ExpectedRevision: entry.Revision}
	switch number {
	case 1:
		mutation.Action, mutation.Title, mutation.Kind = "update", entry.Title, entry.Kind
		mutation.Title, err = memoryLine(ctx, reader, writer, "Title: ")
		if err != nil {
			return err
		}
		mutation.Kind, err = chatMemoryKind(ctx, reader, writer, scope.Kind == "private" && scope.TaskID == "")
		if err != nil {
			return err
		}
		mutation.Body, err = memoryText(ctx, reader, writer, tasks.Config().Memory.MaxDocumentBytes)
		mutation.Sources = []harness.MemorySource{{Kind: "user"}}
	case 2:
		mutation.Action = "disable"
	case 3:
		mutation.Action = "enable"
	case 4:
		mutation.Action = "delete"
	case 5:
		destination, save, err := chatMemoryDestination(ctx, tasks, chat, reader, writer)
		if err != nil || !save {
			return err
		}
		kind := entry.Kind
		if destination.Kind == "shared" && (kind == "preference" || kind == "background") {
			kind, err = chatMemoryKind(ctx, reader, writer, false)
			if err != nil {
				return err
			}
		}
		mutation, err = tasks.PreviewMemoryCopy(ctx, chat, harness.MemoryReference{Scope: scope, EntryID: entry.ID},
			destination, kind, entry.Title, mutation.RequestKey)
	case 6:
		if scope.Kind != "knowledge" {
			return errors.New("file update is only for a knowledge document")
		}
		path, err := memoryLine(ctx, reader, writer, "Workspace file: ")
		if err != nil {
			return err
		}
		path, err = memoryImportPath(tasks.Config().Workspace, path)
		if err != nil {
			return err
		}
		mutation, err = tasks.PreviewKnowledgeImport(ctx, chat, scope, path, entry.Title, mutation.RequestKey)
		mutation.Action, mutation.EntryID, mutation.ExpectedRevision = "update", entry.ID, entry.Revision
	}
	if err != nil {
		return err
	}
	return chatMemoryCommit(ctx, tasks, chat, mutation, reader, writer)
}

func memoryImportPath(workspace, path string) (string, error) {
	if path == "" {
		return "", errors.New("select a workspace text/Markdown file")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	return filepath.Abs(path)
}

func selectedNumbers(text string, count int) ([]int, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	seen := map[int]bool{}
	var numbers []int
	for _, part := range strings.Split(text, ",") {
		number, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || number < 1 || number > count || seen[number] {
			return nil, errors.New("choose distinct listed numbers, comma-separated")
		}
		seen[number] = true
		numbers = append(numbers, number-1)
	}
	return numbers, nil
}

func chatMemorySelection(ctx context.Context, tasks chatTasks, chat string, reader *terminalInput, writer io.Writer) error {
	agent, err := tasks.AgentIdentity(ctx, chat)
	if err != nil {
		return err
	}
	scopes, err := tasks.MemoryScopes(ctx, chat, "private")
	if err != nil {
		return err
	}
	selection, err := tasks.SelectedMemory(ctx, chat)
	if err != nil {
		return err
	}
	var references []harness.MemoryReference
	for _, scope := range scopes {
		if scope.Owner != agent || scope.TaskID != "" {
			continue
		}
		entries, err := tasks.ListMemory(ctx, chat, scope, 100)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.State != "active" {
				continue
			}
			ref := harness.MemoryReference{Scope: scope, EntryID: entry.ID}
			references = append(references, ref)
			marker := ""
			if slices.Contains(selection.Entries, ref) {
				marker = " (selected)"
			}
			_, _ = fmt.Fprintf(writer, "%d. %s%s\n", len(references), entry.Title, marker)
		}
	}
	line, err := memoryLine(ctx, reader, writer, "Retained Agent entries, comma-separated numbers (blank selects none): ")
	if err != nil {
		return err
	}
	numbers, err := selectedNumbers(line, len(references))
	if err != nil {
		return err
	}
	selection.Entries = nil
	for _, index := range numbers {
		selection.Entries = append(selection.Entries, references[index])
	}
	if tasks.Config().Memory.KnowledgeEnabled {
		scopes, err := tasks.MemoryScopes(ctx, chat, "knowledge")
		if err != nil {
			return err
		}
		for index, scope := range scopes {
			marker := ""
			if slices.Contains(selection.Knowledge, scope) {
				marker = " (selected)"
			}
			_, _ = fmt.Fprintf(writer, "%d. %s%s\n", index+1, memoryScopeLabel(ctx, tasks, scope), marker)
		}
		line, err = memoryLine(ctx, reader, writer, "Knowledge collections, comma-separated numbers (blank selects none): ")
		if err != nil {
			return err
		}
		numbers, err = selectedNumbers(line, len(scopes))
		if err != nil {
			return err
		}
		selection.Knowledge = nil
		for _, index := range numbers {
			selection.Knowledge = append(selection.Knowledge, scopes[index])
		}
	} else {
		selection.Knowledge = nil
	}
	line, err = memoryLine(ctx, reader, writer, "Permit current-task-only private notes? [y/N] ")
	if err != nil {
		return err
	}
	selection.TaskPrivate = strings.EqualFold(line, "y")
	if err := applyChatMemorySelection(ctx, tasks, chat, selection); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(writer, "Future Chat inputs selected. Existing tasks keep their saved grants.")
	return nil
}

func chatMemorySources(ctx context.Context, tasks chatTasks, chat string, writer io.Writer) error {
	run, err := tasks.Latest(ctx, chat)
	if err != nil {
		return err
	}
	sources, err := tasks.MemorySources(ctx, run.ID)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		_, _ = fmt.Fprintln(writer, "No persistent memory or knowledge sources used.")
	}
	for index, source := range sources {
		_, _ = fmt.Fprintf(writer, "%d. %s (%s); current: %s; used revision: %d\n", index+1,
			source.Use.Title, memoryScopeLabel(ctx, tasks, source.Use.Scope), source.Current, source.Use.Revision)
		_, _ = fmt.Fprintln(writer, "   SHA-256:", source.Use.Hash)
		if excerpt := source.Use.Excerpt; excerpt != nil {
			_, _ = fmt.Fprintf(writer, "   Bytes %d..%d; excerpt SHA-256: %s\n", excerpt.Start, excerpt.End, excerpt.Hash)
		}
		for _, origin := range source.Use.Sources {
			label := origin.Kind
			if origin.Path != "" {
				label = origin.Path
			}
			_, _ = fmt.Fprintln(writer, "   Origin:", label)
		}
	}
	return nil
}

func chatMemoryAgent(ctx context.Context, tasks chatTasks, chat string, reader *terminalInput, writer io.Writer) error {
	conversations, err := tasks.Conversations(ctx)
	if err != nil {
		return err
	}
	conversations = slices.DeleteFunc(conversations, func(item harness.Conversation) bool {
		return item.ID == chat || item.Workspace != tasks.Config().Workspace
	})
	_, _ = fmt.Fprintln(writer, "Reuse an Agent from a session:")
	for index, item := range conversations {
		_, _ = fmt.Fprintf(writer, "%d. %s\n", index+1, item.Title)
	}
	if len(conversations) == 0 {
		return errors.New("no other session Agent to reuse")
	}
	number, err := memoryNumber(ctx, reader, writer, len(conversations))
	if err != nil {
		return err
	}
	agent, err := tasks.AgentIdentity(ctx, conversations[number].ID)
	if err != nil {
		return err
	}
	line, err := memoryLine(ctx, reader, writer, "Reuse this Agent? Previous private selections will be cleared. [y/N] ")
	if err != nil || !strings.EqualFold(line, "y") {
		return err
	}
	if err := tasks.SelectAgent(ctx, chat, agent); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(writer, "Agent selected. Model selection and existing tasks are unchanged.")
	return nil
}

func chatMemory(ctx context.Context, tasks chatTasks, chat, choice string, reader *terminalInput, stdout, writer io.Writer) error {
	if choice == "" {
		_, _ = fmt.Fprintln(writer, "Memory\n1. View/edit entries\n2. Save selected text\n3. Save current completed result\n4. Select future Chat inputs\n5. Import knowledge file\n6. Sources\n7. Clear future selection\n8. Reuse an Agent\n9. Do not save")
		number, err := memoryNumber(ctx, reader, writer, 9)
		if err != nil {
			return err
		}
		choice = []string{"manage", "save", "result", "select", "import", "sources", "clear", "agent", "back"}[number]
	}
	switch choice {
	case "manage":
		return chatMemoryManage(ctx, tasks, chat, reader, stdout, writer)
	case "save", "result":
		return chatMemorySave(ctx, tasks, chat, choice == "result", reader, writer)
	case "select":
		return chatMemorySelection(ctx, tasks, chat, reader, writer)
	case "import":
		if !tasks.Config().Memory.KnowledgeEnabled {
			return errors.New("knowledge is disabled; enable it in advanced memory settings")
		}
		scope, err := chatMemoryScope(ctx, tasks, chat, "knowledge", true, reader, writer)
		if err != nil {
			return err
		}
		path, err := memoryLine(ctx, reader, writer, "Workspace file: ")
		if err != nil {
			return err
		}
		path, err = memoryImportPath(tasks.Config().Workspace, path)
		if err != nil {
			return err
		}
		mutation, err := tasks.PreviewKnowledgeImport(ctx, chat, scope, path, "", "")
		if err != nil {
			return err
		}
		return chatMemoryCommit(ctx, tasks, chat, mutation, reader, writer)
	case "sources":
		return chatMemorySources(ctx, tasks, chat, writer)
	case "clear":
		if err := applyChatMemorySelection(ctx, tasks, chat, harness.MemorySelection{}); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(writer, "Future memory selection cleared; saved entries and existing tasks are unchanged.")
		return nil
	case "agent":
		return chatMemoryAgent(ctx, tasks, chat, reader, writer)
	case "back":
		return nil
	default:
		return errors.New("choose a listed memory operation")
	}
}

func chatMemorySettings(ctx context.Context, tasks chatTasks, reader *terminalInput, writer io.Writer) error {
	memory := tasks.Config().Memory
	line, err := memoryLine(ctx, reader, writer, fmt.Sprintf("Knowledge enabled [%t]; y/n, blank keeps current: ", memory.KnowledgeEnabled))
	if err != nil {
		return err
	}
	switch strings.ToLower(line) {
	case "y":
		memory.KnowledgeEnabled = true
	case "n":
		memory.KnowledgeEnabled = false
	case "":
	default:
		return errors.New("choose y, n or keep current")
	}
	for _, limit := range []struct {
		name  string
		value *int
	}{{"Selected entries", &memory.MaxEntries}, {"Memory/search bytes", &memory.MaxContextBytes}, {"Document bytes", &memory.MaxDocumentBytes}} {
		line, err = memoryLine(ctx, reader, writer, fmt.Sprintf("%s [%d]: ", limit.name, *limit.value))
		if err != nil {
			return err
		}
		if line != "" {
			number, err := strconv.Atoi(line)
			if err != nil {
				return errors.New("limits require finite integers")
			}
			*limit.value = number
		}
	}
	if err := tasks.ApplyUserMemoryConfig(ctx, memory); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(writer, "Memory settings saved. No background work was started.")
	return nil
}
