package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// MemoryCommand is the transport-neutral shape used by the CLI and the
// attached owner API. It contains user-selected arguments only; the owner
// derives the current workspace and Agent identity before executing it.
type MemoryCommand struct {
	Command          string   `json:"command"`
	ConversationID   string   `json:"conversation_id,omitempty"`
	ScopeKind        string   `json:"scope_kind,omitempty"`
	Owner            string   `json:"owner,omitempty"`
	TaskID           string   `json:"task_id,omitempty"`
	Personal         bool     `json:"personal,omitempty"`
	Entries          []string `json:"entries,omitempty"`
	Collections      []string `json:"collections,omitempty"`
	TaskPrivate      bool     `json:"task_private,omitempty"`
	RunID            string   `json:"run_id,omitempty"`
	Title            string   `json:"title,omitempty"`
	Kind             string   `json:"kind,omitempty"`
	Body             string   `json:"body,omitempty"`
	RequestKey       string   `json:"request_key,omitempty"`
	ExpectedRevision int      `json:"expected_revision,omitempty"`
	File             string   `json:"file,omitempty"`
	Query            string   `json:"query,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	MaxBytes         int      `json:"max_bytes,omitempty"`
	Yes              bool     `json:"yes,omitempty"`
	Update           bool     `json:"update,omitempty"`
	TitleProvided    bool     `json:"title_provided,omitempty"`
	KindProvided     bool     `json:"kind_provided,omitempty"`
	FromScopeKind    string   `json:"from_scope_kind,omitempty"`
	FromOwner        string   `json:"from_owner,omitempty"`
	FromTaskID       string   `json:"from_task_id,omitempty"`
	FromPersonal     bool     `json:"from_personal,omitempty"`
	FromEntry        string   `json:"from_entry,omitempty"`
	ReuseSession     string   `json:"reuse_session,omitempty"`
}

// ExecuteMemoryCommand keeps memory management semantics in the Harness so
// local and attached CLI calls cannot drift into separate persistence paths.
// The result is one of the existing typed records or a bounded preview map.
func ExecuteMemoryCommand(ctx context.Context, tasks *TaskService, request MemoryCommand) (any, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return nil, errors.New("harness: memory command requires a task service and context")
	}
	if request.Command == "sources" {
		if request.RunID == "" {
			return nil, errors.New("memory sources: run_id is required")
		}
		return tasks.MemorySources(ctx, request.RunID)
	}
	if request.ConversationID == "" {
		return nil, errors.New("memory: conversation_id is required")
	}
	agent, err := tasks.AgentIdentity(ctx, request.ConversationID)
	if err != nil {
		return nil, err
	}
	if request.Command == "agent" {
		if request.ReuseSession != "" {
			agent, err = tasks.AgentIdentity(ctx, request.ReuseSession)
			if err == nil {
				err = tasks.SelectAgent(ctx, request.ConversationID, agent)
			}
			if err != nil {
				return nil, err
			}
		}
		return map[string]string{"agent_id": agent}, nil
	}
	project := tasks.Config().Workspace
	if request.Personal {
		project = ""
	}
	if request.Owner == "" && request.ScopeKind == "private" {
		request.Owner = agent
	}
	scope := MemoryScope{Kind: request.ScopeKind, Owner: request.Owner, Project: project, TaskID: request.TaskID}
	if request.Command == "select" || request.Command == "clear" {
		selection := MemorySelection{}
		if request.Command == "select" {
			selection.TaskPrivate = request.TaskPrivate
			for _, id := range request.Entries {
				selection.Entries = append(selection.Entries, MemoryReference{
					Scope: MemoryScope{Kind: "private", Owner: agent, Project: project}, EntryID: id,
				})
			}
			for _, name := range request.Collections {
				selection.Knowledge = append(selection.Knowledge, MemoryScope{Kind: "knowledge", Owner: name, Project: project})
			}
		}
		control, err := tasks.Control()
		if err != nil {
			return nil, err
		}
		if err := control.ApplyMemorySelection(ctx, request.ConversationID, selection, "cli"); err != nil {
			return nil, err
		}
		return selection, nil
	}
	switch request.Command {
	case "scopes":
		return tasks.MemoryScopes(ctx, request.ConversationID, request.ScopeKind)
	case "list":
		return tasks.ListMemory(ctx, request.ConversationID, scope, request.Limit)
	case "search":
		return tasks.SearchKnowledge(ctx, request.ConversationID, scope, request.Query, min(request.Limit, 16), request.MaxBytes)
	case "read":
		return tasks.ReadMemory(ctx, request.ConversationID, scope, firstMemoryEntry(request.Entries))
	}
	if request.RequestKey == "" {
		request.RequestKey = "user-" + newID()
	}
	entry := firstMemoryEntry(request.Entries)
	mutation := MemoryMutation{RequestKey: request.RequestKey, Action: request.Command, Scope: scope, EntryID: entry,
		ExpectedRevision: request.ExpectedRevision, Kind: request.Kind, Title: request.Title, Body: request.Body,
		Sources: []MemorySource{{Kind: "user"}}}
	switch request.Command {
	case "save":
		mutation.Action = "create"
	case "update":
		current, err := tasks.ReadMemory(ctx, request.ConversationID, scope, entry)
		if err != nil {
			return nil, err
		}
		if !request.TitleProvided {
			mutation.Title = current.Title
		}
		if !request.KindProvided {
			mutation.Kind = current.Kind
		}
	case "disable", "enable", "delete":
		mutation.Kind, mutation.Title, mutation.Body, mutation.Sources = "", "", "", nil
	case "import":
		path, err := filepath.Abs(request.File)
		if err != nil || request.File == "" {
			return nil, errors.New("memory import: file is required")
		}
		mutation, err = tasks.PreviewKnowledgeImport(ctx, request.ConversationID, scope, path, request.Title, request.RequestKey)
		if err != nil {
			return nil, err
		}
		if request.Update {
			mutation.Action, mutation.EntryID, mutation.ExpectedRevision = "update", entry, request.ExpectedRevision
		}
	case "copy":
		sourceProject := tasks.Config().Workspace
		if request.FromPersonal {
			sourceProject = ""
		}
		if request.FromOwner == "" && request.FromScopeKind == "private" {
			request.FromOwner = agent
		}
		source := MemoryReference{Scope: MemoryScope{Kind: request.FromScopeKind, Owner: request.FromOwner,
			Project: sourceProject, TaskID: request.FromTaskID}, EntryID: request.FromEntry}
		mutation, err = tasks.PreviewMemoryCopy(ctx, request.ConversationID, source, scope, request.Kind, request.Title, request.RequestKey)
		if err != nil {
			return nil, err
		}
	case "save-result":
		mutation, err = tasks.PreviewRunMemory(ctx, request.ConversationID, request.RunID, scope, request.Kind, request.Title, request.RequestKey)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("memory: unknown operation %q", request.Command)
	}
	if !request.Yes {
		return map[string]any{"preview": mutation, "saved": false}, nil
	}
	return tasks.SaveMemory(ctx, request.ConversationID, mutation)
}

func firstMemoryEntry(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[0]
}
