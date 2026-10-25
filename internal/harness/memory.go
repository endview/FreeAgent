package harness

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	ErrMemoryDenied   = errors.New("harness: memory scope or operation is not authorized")
	ErrMemoryConflict = errors.New("harness: memory revision changed; read it again before editing")
	ErrMemoryDeleted  = errors.New("harness: memory reference was deleted")
	ErrMemoryMissing  = errors.New("harness: memory entry is unavailable in the selected scope")
	ErrMemoryDisabled = errors.New("harness: memory entry is disabled")
)

type MemoryConfig struct {
	KnowledgeEnabled bool `json:"knowledge_enabled"`
	MaxEntries       int  `json:"max_entries"`
	MaxContextBytes  int  `json:"max_context_bytes"`
	MaxDocumentBytes int  `json:"max_document_bytes"`
}

func defaultMemoryConfig() MemoryConfig {
	return MemoryConfig{MaxEntries: 16, MaxContextBytes: 16384, MaxDocumentBytes: 65536}
}

func (config MemoryConfig) validate() error {
	if config.MaxEntries < 1 || config.MaxEntries > 64 || config.MaxContextBytes < 1024 ||
		config.MaxContextBytes > 65536 || config.MaxDocumentBytes < 1024 || config.MaxDocumentBytes > 65536 {
		return errors.New("harness: memory limits must be finite")
	}
	return nil
}

func (config MemoryConfig) Validate() error { return config.validate() }

// Owner is an Agent identity, parent Run, or knowledge collection, respectively.
// A private TaskID confines the item to one Run; empty means explicitly retained.
type MemoryScope struct {
	Kind    string `json:"kind"`
	Owner   string `json:"owner"`
	Project string `json:"project"`
	TaskID  string `json:"task_id,omitempty"`
}

func memoryID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return value != "." && value != ".."
}

func (scope MemoryScope) validate() error {
	validOwner := memoryID(scope.Owner)
	if scope.Kind == "knowledge" {
		validOwner = len(scope.Owner) > 0 && len(scope.Owner) <= 128 &&
			strings.TrimSpace(scope.Owner) == scope.Owner && utf8.ValidString(scope.Owner) &&
			!strings.ContainsAny(scope.Owner, "\x00\r\n\t")
	}
	if !slices.Contains([]string{"private", "shared", "knowledge"}, scope.Kind) ||
		!validOwner || scope.TaskID != "" && !memoryID(scope.TaskID) ||
		scope.Project != "" && (!filepath.IsAbs(scope.Project) || filepath.Clean(scope.Project) != scope.Project) ||
		scope.Kind != "private" && scope.TaskID != "" || scope.Kind == "shared" && scope.Project == "" {
		return ErrMemoryDenied
	}
	return nil
}

type MemorySource struct {
	Kind      string `json:"kind"`
	RunID     string `json:"run_id,omitempty"`
	AttemptID string `json:"attempt_id,omitempty"`
	EntryID   string `json:"entry_id,omitempty"`
	Revision  int    `json:"revision,omitempty"`
	Hash      string `json:"sha256,omitempty"`
	Path      string `json:"path,omitempty"`
}

type MemoryEntry struct {
	ID        string         `json:"id"`
	Scope     MemoryScope    `json:"scope"`
	Revision  int            `json:"revision"`
	State     string         `json:"state"`
	Kind      string         `json:"kind"`
	Title     string         `json:"title"`
	Body      string         `json:"body,omitempty"`
	Author    string         `json:"author"`
	Sources   []MemorySource `json:"sources"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

type MemoryGrant struct {
	Scope   MemoryScope `json:"scope"`
	Read    bool        `json:"read"`
	Append  bool        `json:"append"`
	Modify  bool        `json:"modify"`
	Kinds   []string    `json:"kinds,omitempty"`
	Entries []string    `json:"entries,omitempty"`
}

type MemoryMutation struct {
	RequestKey       string         `json:"request_key"`
	Action           string         `json:"action"`
	Scope            MemoryScope    `json:"scope"`
	EntryID          string         `json:"entry_id,omitempty"`
	ExpectedRevision int            `json:"expected_revision,omitempty"`
	Kind             string         `json:"kind,omitempty"`
	Title            string         `json:"title,omitempty"`
	Body             string         `json:"body,omitempty"`
	Sources          []MemorySource `json:"sources,omitempty"`
	Reason           string         `json:"reason,omitempty"`
}

type MemoryReceipt struct {
	EntryID   string `json:"entry_id"`
	Revision  int    `json:"revision"`
	State     string `json:"state"`
	Action    string `json:"action"`
	Duplicate bool   `json:"duplicate"`
}

type KnowledgeHit struct {
	Source    MemoryUse `json:"source"`
	Excerpt   string    `json:"excerpt"`
	Start     int       `json:"start_byte"`
	End       int       `json:"end_byte"`
	Truncated bool      `json:"truncated"`
}

type memoryAuthority struct {
	user    bool
	agent   string
	runID   string
	parent  string
	project string
	grants  []MemoryGrant
	files   map[string]string
}

func memoryKind(value string) bool {
	return slices.Contains([]string{"fact", "hypothesis", "finding", "decision", "question", "artifact", "preference", "background"}, value)
}

func (grant MemoryGrant) validate() error {
	if grant.Scope.validate() != nil || grant.Modify && !grant.Read || len(grant.Kinds) > 8 ||
		len(grant.Entries) > 64 || grant.Append && len(grant.Entries) != 0 ||
		!grant.Read && !grant.Append && !grant.Modify {
		return ErrMemoryDenied
	}
	seen := map[string]bool{}
	for _, kind := range grant.Kinds {
		if !memoryKind(kind) || seen[kind] {
			return ErrMemoryDenied
		}
		seen[kind] = true
	}
	seen = map[string]bool{}
	for _, id := range grant.Entries {
		if !memoryID(id) || seen[id] {
			return ErrMemoryDenied
		}
		seen[id] = true
	}
	return nil
}

func (authority memoryAuthority) grant(scope MemoryScope, action string) (MemoryGrant, error) {
	if scope.validate() != nil || scope.Project != "" && scope.Project != authority.project {
		return MemoryGrant{}, ErrMemoryDenied
	}
	if authority.user {
		return MemoryGrant{Scope: scope, Read: true, Append: true, Modify: true}, nil
	}
	if !memoryID(authority.agent) || !memoryID(authority.runID) ||
		scope.Kind == "private" && (scope.Owner != authority.agent || scope.TaskID != "" && scope.TaskID != authority.runID) ||
		scope.Kind == "shared" && scope.Owner != authority.parent {
		return MemoryGrant{}, ErrMemoryDenied
	}
	if action != "read" && (scope.Kind == "knowledge" || scope.Kind == "private" && scope.TaskID == "") {
		return MemoryGrant{}, ErrMemoryDenied
	}
	for _, grant := range authority.grants {
		if grant.Scope != scope || grant.validate() != nil {
			continue
		}
		if action == "read" && grant.Read || action == "create" && grant.Append ||
			action != "read" && action != "create" && grant.Modify {
			return grant, nil
		}
	}
	return MemoryGrant{}, ErrMemoryDenied
}

const memoryColumns = `id,scope,owner,project,task_id,revision,state,kind,title,body,author,sources_json,created_at,updated_at`

func scanMemory(row interface{ Scan(...any) error }) (MemoryEntry, error) {
	var entry MemoryEntry
	var sources string
	err := row.Scan(&entry.ID, &entry.Scope.Kind, &entry.Scope.Owner, &entry.Scope.Project, &entry.Scope.TaskID,
		&entry.Revision, &entry.State, &entry.Kind, &entry.Title, &entry.Body, &entry.Author, &sources, &entry.CreatedAt, &entry.UpdatedAt)
	if err == nil && json.Unmarshal([]byte(sources), &entry.Sources) != nil {
		err = ErrStoreFormat
	}
	if err == nil && (!memoryID(entry.ID) || entry.Scope.validate() != nil || entry.Revision < 1 || entry.Revision > 256 ||
		!slices.Contains([]string{"active", "disabled", "deleted"}, entry.State) || !memoryKind(entry.Kind) ||
		len(entry.Title) > 256 || len(entry.Body) > 65536 || len(entry.Sources) > 16 ||
		!utf8.ValidString(entry.Title+entry.Body) || strings.ContainsRune(entry.Title+entry.Body, 0)) {
		err = ErrStoreFormat
	}
	for _, source := range entry.Sources {
		if source.validate() != nil {
			err = ErrStoreFormat
		}
	}
	return entry, err
}

func getMemory(ctx context.Context, reader metaReader, authority memoryAuthority, scope MemoryScope, id, action string) (MemoryEntry, error) {
	grant, err := authority.grant(scope, action)
	if err != nil {
		return MemoryEntry{}, err
	}
	kinds, _ := json.Marshal(grant.Kinds)
	ids, _ := json.Marshal(grant.Entries)
	entry, err := scanMemory(reader.QueryRowContext(ctx, `SELECT `+memoryColumns+` FROM memories
		WHERE id=? AND scope=? AND owner=? AND project=? AND task_id=?
		AND (? OR kind IN (SELECT value FROM json_each(?)))
		AND (? OR id IN (SELECT value FROM json_each(?)))`, id, scope.Kind, scope.Owner, scope.Project, scope.TaskID,
		len(grant.Kinds) == 0, string(kinds), len(grant.Entries) == 0, string(ids)))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrMemoryMissing
	}
	if err == nil && entry.State == "deleted" {
		err = ErrMemoryDeleted
	}
	return entry, err
}

func listMemory(ctx context.Context, reader attemptReader, authority memoryAuthority, scope MemoryScope, limit int) ([]MemoryEntry, error) {
	grant, err := authority.grant(scope, "read")
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("harness: memory list limit must be 1-100")
	}
	kinds, _ := json.Marshal(grant.Kinds)
	ids, _ := json.Marshal(grant.Entries)
	// Listing is metadata only. Full body reads require a separately scoped ID.
	rows, err := reader.QueryContext(ctx, `SELECT id,scope,owner,project,task_id,revision,state,kind,title,'',author,sources_json,created_at,updated_at
		FROM memories WHERE scope=? AND owner=? AND project=? AND task_id=? AND state!='deleted'
		AND (? OR kind IN (SELECT value FROM json_each(?)))
		AND (? OR id IN (SELECT value FROM json_each(?))) ORDER BY updated_at DESC,id LIMIT ?`,
		scope.Kind, scope.Owner, scope.Project, scope.TaskID, len(grant.Kinds) == 0, string(kinds),
		len(grant.Entries) == 0, string(ids), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]MemoryEntry, 0)
	for rows.Next() {
		entry, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func searchKnowledge(ctx context.Context, reader attemptReader, authority memoryAuthority, scope MemoryScope, query string, limit, maxBytes int) ([]KnowledgeHit, error) {
	if scope.Kind != "knowledge" || strings.TrimSpace(query) == "" || len(query) > 1024 ||
		!utf8.ValidString(query) || strings.ContainsRune(query, 0) {
		return nil, ErrMemoryDenied
	}
	if limit < 1 || limit > 16 || maxBytes < 256 || maxBytes > 65536 {
		return nil, ErrContextLimit
	}
	grant, err := authority.grant(scope, "read")
	if err != nil {
		return nil, err
	}
	kinds, _ := json.Marshal(grant.Kinds)
	ids, _ := json.Marshal(grant.Entries)
	query = strings.TrimSpace(query)
	// FTS5 syntax is intentionally reduced to one quoted literal. This keeps
	// imported source data from becoming a query operator.
	term := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
	columns := "m." + strings.ReplaceAll(memoryColumns, ",", ",m.")
	filters := ` AND m.scope='knowledge' AND m.owner=? AND m.project=? AND m.task_id=''
		AND m.state='active' AND (? OR m.kind IN (SELECT value FROM json_each(?)))
		AND (? OR m.id IN (SELECT value FROM json_each(?)))`
	statement := `SELECT ` + columns + ` FROM knowledge_fts JOIN memories m ON m.id=knowledge_fts.entry_id
		WHERE knowledge_fts MATCH ?` + filters + ` ORDER BY bm25(knowledge_fts),m.id LIMIT ?`
	if utf8.RuneCountInString(query) < 3 {
		statement = `SELECT ` + columns + ` FROM memories m WHERE instr(lower(m.body),lower(?))>0` +
			filters + ` ORDER BY m.id LIMIT ?`
		term = query
	}
	rows, err := reader.QueryContext(ctx, statement, term, scope.Owner, scope.Project,
		len(grant.Kinds) == 0, string(kinds), len(grant.Entries) == 0, string(ids), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Match in the original text: case conversion may change UTF-8 byte lengths.
	matcher := regexp.MustCompile("(?i:" + regexp.QuoteMeta(query) + ")")
	hits := make([]KnowledgeHit, 0)
	total := 0
	for rows.Next() {
		if total >= maxBytes {
			break
		}
		entry, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		start := 0
		if match := matcher.FindStringIndex(entry.Body); match != nil {
			start = match[0]
		}
		end, err := excerptEnd(entry.Body, start, maxBytes-total)
		if err != nil {
			if errors.Is(err, ErrContextLimit) && len(hits) != 0 {
				break
			}
			return nil, err
		}
		excerpt := entry.Body[start:end]
		total += len(excerpt)
		if total > maxBytes {
			break
		}
		hits = append(hits, KnowledgeHit{Source: memoryUse(entry), Excerpt: excerpt, Start: start, End: end,
			Truncated: start > 0 || end < len(entry.Body)})
	}
	return hits, rows.Err()
}

func (mutation MemoryMutation) validate() error {
	if mutation.Scope.validate() != nil || mutation.RequestKey == "" || len(mutation.RequestKey) > 256 ||
		!slices.Contains([]string{"create", "update", "disable", "enable", "delete"}, mutation.Action) ||
		len(mutation.Title) > 256 || len(mutation.Body) > 65536 || len(mutation.Reason) > 512 ||
		!utf8.ValidString(mutation.Title+mutation.Body+mutation.Reason+mutation.RequestKey) ||
		strings.ContainsRune(mutation.Title+mutation.Body+mutation.Reason+mutation.RequestKey, 0) || len(mutation.Sources) > 16 {
		return errors.New("harness: invalid bounded memory mutation")
	}
	if mutation.Action == "create" && (mutation.EntryID != "" || mutation.ExpectedRevision != 0) ||
		mutation.Action != "create" && (!memoryID(mutation.EntryID) || mutation.ExpectedRevision < 1) {
		return ErrMemoryConflict
	}
	if mutation.Action == "create" || mutation.Action == "update" {
		if !memoryKind(mutation.Kind) || strings.TrimSpace(mutation.Title) == "" || strings.TrimSpace(mutation.Body) == "" ||
			len(mutation.Sources) == 0 {
			return errors.New("harness: memory needs a type, title, content and explicit source")
		}
	} else if mutation.Title != "" || mutation.Body != "" || mutation.Kind != "" || len(mutation.Sources) != 0 {
		return errors.New("harness: status changes cannot replace memory content")
	}
	return nil
}

func duplicateKnowledgeSource(ctx context.Context, tx *sql.Tx, mutation MemoryMutation) (MemoryReceipt, bool, error) {
	if mutation.Action != "create" || mutation.Scope.Kind != "knowledge" {
		return MemoryReceipt{}, false, nil
	}
	var wanted MemorySource
	for _, source := range mutation.Sources {
		if source.Kind == "file" {
			wanted = source
			break
		}
	}
	if wanted.Kind == "" {
		return MemoryReceipt{}, false, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,revision,state,sources_json FROM memories
		WHERE scope='knowledge' AND owner=? AND project=? AND task_id='' AND state!='deleted'`,
		mutation.Scope.Owner, mutation.Scope.Project)
	if err != nil {
		return MemoryReceipt{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, state, raw string
		var revision int
		if err := rows.Scan(&id, &revision, &state, &raw); err != nil {
			return MemoryReceipt{}, false, err
		}
		var sources []MemorySource
		if json.Unmarshal([]byte(raw), &sources) != nil {
			return MemoryReceipt{}, false, ErrStoreFormat
		}
		for _, source := range sources {
			if source.Kind == "file" {
				if source.Hash == wanted.Hash {
					return MemoryReceipt{EntryID: id, Revision: revision, State: state,
						Action: "create", Duplicate: true}, true, nil
				}
				if source.Path == wanted.Path {
					return MemoryReceipt{}, false, ErrMemoryConflict
				}
			}
		}
	}
	return MemoryReceipt{}, false, rows.Err()
}

func (source MemorySource) validate() error {
	if source.Hash != "" {
		hash, err := hex.DecodeString(source.Hash)
		if err != nil || len(hash) != 32 || strings.ToLower(source.Hash) != source.Hash {
			return ErrMemoryDenied
		}
	}
	valid := false
	switch source.Kind {
	case "user":
		valid = source == (MemorySource{Kind: "user"})
	case "request", "run":
		valid = memoryID(source.RunID) && len(source.Hash) == 64 && source.AttemptID == "" &&
			source.EntryID == "" && source.Revision == 0 && source.Path == ""
	case "attempt":
		valid = memoryID(source.RunID) && memoryID(source.AttemptID) && len(source.Hash) == 64 &&
			source.EntryID == "" && source.Revision == 0 && source.Path == ""
	case "memory":
		valid = memoryID(source.EntryID) && source.Revision > 0 && len(source.Hash) == 64 &&
			source.RunID == "" && source.AttemptID == "" && source.Path == ""
	case "file":
		valid = filepath.IsAbs(source.Path) && filepath.Clean(source.Path) == source.Path &&
			len(source.Path) <= 4096 && len(source.Hash) == 64 &&
			source.RunID == "" && source.AttemptID == "" && source.EntryID == "" && source.Revision == 0
	}
	if !valid {
		return ErrMemoryDenied
	}
	return nil
}

func validateMemorySources(ctx context.Context, tx *sql.Tx, authority memoryAuthority, mutation MemoryMutation) error {
	for _, source := range mutation.Sources {
		if err := source.validate(); err != nil {
			return err
		}
		switch source.Kind {
		case "user":
			if !authority.user {
				return ErrMemoryDenied
			}
		case "request":
			var prompt, project string
			err := tx.QueryRowContext(ctx, `SELECT r.prompt,c.workspace FROM runs r JOIN conversations c
				ON c.id=r.conversation_id WHERE r.id=?`, source.RunID).Scan(&prompt, &project)
			if err != nil || project != authority.project || source.Hash != contentHash([]byte(prompt)) ||
				!authority.user && source.RunID != authority.runID {
				return ErrMemoryDenied
			}
		case "run":
			var output, project, state string
			err := tx.QueryRowContext(ctx, `SELECT r.output,c.workspace,r.status FROM runs r JOIN conversations c ON c.id=r.conversation_id WHERE r.id=?`, source.RunID).Scan(&output, &project, &state)
			if err != nil || project != authority.project || state != "completed" || source.Hash != contentHash([]byte(output)) ||
				!authority.user && source.RunID != authority.runID {
				return ErrMemoryDenied
			}
		case "attempt":
			var body, runID, project, state string
			err := tx.QueryRowContext(ctx, `SELECT a.response_json,a.run_id,c.workspace,a.status FROM attempts a
				JOIN runs r ON r.id=a.run_id JOIN conversations c ON c.id=r.conversation_id WHERE a.id=?`, source.AttemptID).Scan(&body, &runID, &project, &state)
			if err != nil || project != authority.project || state != "finished" || source.Hash != contentHash([]byte(body)) ||
				source.RunID != runID || !authority.user && runID != authority.runID {
				return ErrMemoryDenied
			}
		case "memory":
			scope := mutation.Scope
			if authority.user {
				err := tx.QueryRowContext(ctx, `SELECT scope,owner,project,task_id FROM memories
					WHERE id=? AND (project='' OR project=?)`, source.EntryID, authority.project).
					Scan(&scope.Kind, &scope.Owner, &scope.Project, &scope.TaskID)
				if err != nil {
					return ErrMemoryMissing
				}
			}
			entry, err := getMemory(ctx, tx, authority, scope, source.EntryID, "read")
			if err != nil || entry.State != "active" || entry.Revision != source.Revision || source.Hash != contentHash([]byte(entry.Body)) {
				return ErrMemoryConflict
			}
		case "file":
			// A file source binds the complete imported text, not a caller's
			// unverified claim about a path or a generated summary.
			if !authority.user || source.Hash != contentHash([]byte(mutation.Body)) ||
				authority.files[source.Path] != source.Hash {
				return ErrMemoryDenied
			}
		default:
			return errors.New("harness: unsupported memory source")
		}
	}
	return nil
}

func validateMemoryScopeOwner(ctx context.Context, tx *sql.Tx, scope MemoryScope) error {
	if scope.Kind == "shared" || scope.TaskID != "" {
		id := scope.Owner
		if scope.TaskID != "" {
			id = scope.TaskID
		}
		var project string
		if err := tx.QueryRowContext(ctx, `SELECT c.workspace FROM runs r JOIN conversations c ON c.id=r.conversation_id WHERE r.id=?`, id).Scan(&project); err != nil || project != scope.Project {
			return ErrMemoryDenied
		}
		if scope.Kind == "shared" {
			record, err := loadCollaboration(ctx, tx, id)
			if err != nil || record.ParentID != scope.Owner {
				return ErrMemoryDenied
			}
		}
	}
	return nil
}

func memoryOperation(authority memoryAuthority, mutation MemoryMutation) (string, string) {
	author := authority.agent
	if authority.user {
		author = "user"
	}
	raw, _ := json.Marshal(mutation)
	scopeRaw, _ := json.Marshal(mutation.Scope)
	key := contentHash([]byte(author + "\x00" + authority.runID + "\x00" + string(scopeRaw) + "\x00" + mutation.RequestKey))
	return key, contentHash(raw)
}

func savedMemoryReceipt(ctx context.Context, reader metaReader, key, hash string) (MemoryReceipt, bool, error) {
	var previousHash, raw string
	err := reader.QueryRowContext(ctx, `SELECT request_hash,receipt_json FROM memory_operations WHERE id=?`, key).Scan(&previousHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return MemoryReceipt{}, false, nil
	}
	if err != nil {
		return MemoryReceipt{}, false, err
	}
	if previousHash != hash {
		return MemoryReceipt{}, true, ErrRequestKey
	}
	var receipt MemoryReceipt
	if json.Unmarshal([]byte(raw), &receipt) != nil {
		return receipt, true, ErrStoreFormat
	}
	receipt.Duplicate = true
	return receipt, true, nil
}

func saveMemoryReceipt(ctx context.Context, tx *sql.Tx, key, hash string, receipt MemoryReceipt) error {
	raw, _ := json.Marshal(receipt)
	_, err := tx.ExecContext(ctx, `INSERT INTO memory_operations(id,request_hash,receipt_json,created_at) VALUES(?,?,?,?)`,
		key, hash, string(raw), nowText())
	return err
}

func mutateMemoryTx(ctx context.Context, tx *sql.Tx, authority memoryAuthority, mutation MemoryMutation) (MemoryReceipt, error) {
	if err := mutation.validate(); err != nil {
		return MemoryReceipt{}, err
	}
	grant, err := authority.grant(mutation.Scope, mutation.Action)
	if err != nil {
		return MemoryReceipt{}, err
	}
	if err := validateMemoryScopeOwner(ctx, tx, mutation.Scope); err != nil {
		return MemoryReceipt{}, err
	}
	author := authority.agent
	if authority.user {
		author = "user"
	}
	key, hash := memoryOperation(authority, mutation)
	if receipt, found, err := savedMemoryReceipt(ctx, tx, key, hash); err != nil || found {
		return receipt, err
	}
	entry := MemoryEntry{ID: newID(), Scope: mutation.Scope, Revision: 1, State: "active",
		CreatedAt: nowText(), UpdatedAt: nowText(), Author: author}
	if mutation.Action != "create" {
		entry, err = getMemory(ctx, tx, authority, mutation.Scope, mutation.EntryID, mutation.Action)
		if err != nil {
			return MemoryReceipt{}, err
		}
		if entry.Revision != mutation.ExpectedRevision {
			return MemoryReceipt{}, ErrMemoryConflict
		}
		entry.Revision++
		entry.UpdatedAt, entry.Author = nowText(), author
	}
	if entry.Revision > 256 {
		return MemoryReceipt{}, errors.New("harness: memory revision limit reached")
	}
	if mutation.Action == "create" || mutation.Action == "update" {
		if len(grant.Kinds) != 0 && !slices.Contains(grant.Kinds, mutation.Kind) {
			return MemoryReceipt{}, ErrMemoryDenied
		}
		if !authority.user && (mutation.Kind == "preference" || mutation.Scope.Kind == "knowledge" ||
			mutation.Scope.Kind == "private" && mutation.Scope.TaskID == "") {
			return MemoryReceipt{}, ErrMemoryDenied
		}
		if err := validateMemorySources(ctx, tx, authority, mutation); err != nil {
			return MemoryReceipt{}, err
		}
		if receipt, duplicate, err := duplicateKnowledgeSource(ctx, tx, mutation); err != nil {
			return MemoryReceipt{}, err
		} else if duplicate {
			return receipt, saveMemoryReceipt(ctx, tx, key, hash, receipt)
		}
		entry.Kind, entry.Title, entry.Body, entry.Sources = mutation.Kind, mutation.Title, mutation.Body, mutation.Sources
	}
	if mutation.Action == "disable" && entry.State != "active" ||
		mutation.Action == "enable" && entry.State != "disabled" {
		return MemoryReceipt{}, ErrMemoryConflict
	}
	switch mutation.Action {
	case "disable":
		entry.State = "disabled"
	case "enable":
		entry.State = "active"
	case "delete":
		entry.State, entry.Title, entry.Body, entry.Sources = "deleted", "", "", []MemorySource{}
	}
	sources, _ := json.Marshal(entry.Sources)
	if _, err := tx.ExecContext(ctx, `INSERT INTO memories(`+memoryColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,state=excluded.state,kind=excluded.kind,title=excluded.title,body=excluded.body,
		author=excluded.author,sources_json=excluded.sources_json,updated_at=excluded.updated_at`,
		entry.ID, entry.Scope.Kind, entry.Scope.Owner, entry.Scope.Project, entry.Scope.TaskID, entry.Revision, entry.State,
		entry.Kind, entry.Title, entry.Body, entry.Author, string(sources), entry.CreatedAt, entry.UpdatedAt); err != nil {
		return MemoryReceipt{}, err
	}
	if mutation.Action == "delete" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM memory_revisions WHERE entry_id=?`, entry.ID); err != nil {
			return MemoryReceipt{}, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_revisions(entry_id,revision,state,kind,title,body,author,sources_json,reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			entry.ID, entry.Revision, entry.State, entry.Kind, entry.Title, entry.Body, author, string(sources), mutation.Reason, entry.UpdatedAt); err != nil {
			return MemoryReceipt{}, err
		}
	}
	if entry.Scope.Kind == "knowledge" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_fts WHERE entry_id=?`, entry.ID); err != nil {
			return MemoryReceipt{}, err
		}
		if entry.State == "active" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO knowledge_fts(entry_id,body) VALUES(?,?)`, entry.ID, entry.Body); err != nil {
				return MemoryReceipt{}, err
			}
		}
	}
	receipt := MemoryReceipt{EntryID: entry.ID, Revision: entry.Revision, State: entry.State, Action: mutation.Action}
	return receipt, saveMemoryReceipt(ctx, tx, key, hash, receipt)
}

func (tasks *TaskService) AgentIdentity(ctx context.Context, conversationID string) (string, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return "", errors.New("harness: task service and context are required")
	}
	chat, err := tasks.core.Store.GetConversation(ctx, conversationID)
	if err != nil || chat.Workspace != tasks.Config().Workspace {
		return "", errors.Join(ErrMemoryDenied, err)
	}
	var agent string
	err = tasks.core.Store.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, "chat/agent/"+chat.ID).Scan(&agent)
	if errors.Is(err, sql.ErrNoRows) {
		return "chat-" + chat.ID, nil
	}
	if err == nil && !memoryID(agent) {
		err = ErrStoreFormat
	}
	return agent, err
}

func (tasks *TaskService) SelectAgent(ctx context.Context, conversationID, agentID string) error {
	if !memoryID(agentID) {
		return ErrMemoryDenied
	}
	previous, err := tasks.AgentIdentity(ctx, conversationID)
	if err != nil {
		return err
	}
	if previous == agentID {
		return nil
	}
	selection, err := tasks.SelectedMemory(ctx, conversationID)
	if err != nil {
		return err
	}
	// Identity reuse is explicit. Inputs owned by the previous Agent are not
	// carried across; existing Run snapshots retain their original authority.
	selection.Entries = nil
	raw, _ := json.Marshal(selection)
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		"chat/agent/"+conversationID, agentID)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		"chat/memory/"+conversationID, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func (tasks *TaskService) userMemory(ctx context.Context, conversationID string) (memoryAuthority, error) {
	agent, err := tasks.AgentIdentity(ctx, conversationID)
	return memoryAuthority{user: true, agent: agent, project: tasks.Config().Workspace}, err
}

func (tasks *TaskService) SaveMemory(ctx context.Context, conversationID string, mutation MemoryMutation) (MemoryReceipt, error) {
	authority, err := tasks.userMemory(ctx, conversationID)
	if err != nil {
		return MemoryReceipt{}, err
	}
	if err := mutation.validate(); err != nil {
		return MemoryReceipt{}, err
	}
	if _, err := authority.grant(mutation.Scope, mutation.Action); err != nil {
		return MemoryReceipt{}, err
	}
	// A lost response returns the exact saved receipt without reimporting a
	// changed/deleted file or resurrecting deleted memory.
	key, hash := memoryOperation(authority, mutation)
	if receipt, found, err := savedMemoryReceipt(ctx, tasks.core.Store.db, key, hash); err != nil || found {
		return receipt, err
	}
	if len(mutation.Body) > tasks.Config().Memory.MaxDocumentBytes {
		return MemoryReceipt{}, errors.New("harness: memory content exceeds the configured document limit")
	}
	if mutation.Scope.Kind == "knowledge" && !tasks.Config().Memory.KnowledgeEnabled &&
		(mutation.Action == "create" || mutation.Action == "update" || mutation.Action == "enable") {
		return MemoryReceipt{}, errors.New("harness: knowledge is disabled")
	}
	for _, source := range mutation.Sources {
		if source.Kind != "file" {
			continue
		}
		if source.validate() != nil {
			return MemoryReceipt{}, ErrMemoryDenied
		}
		files, err := NewFileTools(authority.project, tasks.core.Store.DataDir())
		if err != nil {
			return MemoryReceipt{}, err
		}
		relative, err := filepath.Rel(authority.project, source.Path)
		var raw []byte
		if err == nil {
			raw, _, err = files.read(filepath.ToSlash(relative))
		}
		_ = files.Close()
		if err != nil || string(raw) != mutation.Body || contentHash(raw) != source.Hash {
			return MemoryReceipt{}, ErrMemoryConflict
		}
		if authority.files == nil {
			authority.files = map[string]string{}
		}
		authority.files[source.Path] = source.Hash
	}
	tx, err := tasks.core.Store.db.BeginTx(ctx, nil)
	if err != nil {
		return MemoryReceipt{}, err
	}
	defer tx.Rollback()
	receipt, err := mutateMemoryTx(ctx, tx, authority, mutation)
	if err != nil {
		return MemoryReceipt{}, err
	}
	return receipt, tx.Commit()
}

func (tasks *TaskService) ReadMemory(ctx context.Context, conversationID string, scope MemoryScope, id string) (MemoryEntry, error) {
	authority, err := tasks.userMemory(ctx, conversationID)
	if err != nil {
		return MemoryEntry{}, err
	}
	return getMemory(ctx, tasks.core.Store.db, authority, scope, id, "read")
}

func (tasks *TaskService) ListMemory(ctx context.Context, conversationID string, scope MemoryScope, limit int) ([]MemoryEntry, error) {
	authority, err := tasks.userMemory(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return listMemory(ctx, tasks.core.Store.db, authority, scope, limit)
}

func (tasks *TaskService) SearchKnowledge(ctx context.Context, conversationID string, scope MemoryScope, query string, limit, maxBytes int) ([]KnowledgeHit, error) {
	config := tasks.Config().Memory
	if !config.KnowledgeEnabled {
		return nil, errors.New("harness: knowledge is disabled")
	}
	if limit > config.MaxEntries || maxBytes > config.MaxContextBytes {
		return nil, ErrContextLimit
	}
	authority, err := tasks.userMemory(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return searchKnowledge(ctx, tasks.core.Store.db, authority, scope, query, limit, maxBytes)
}

func (tasks *TaskService) PreviewKnowledgeImport(ctx context.Context, conversationID string, scope MemoryScope, path, title, requestKey string) (MemoryMutation, error) {
	if !tasks.Config().Memory.KnowledgeEnabled || scope.Kind != "knowledge" {
		return MemoryMutation{}, errors.New("harness: knowledge import is disabled or scope is not knowledge")
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".txt" && extension != ".md" && extension != ".markdown" {
		return MemoryMutation{}, errors.New("harness: knowledge import accepts only .txt, .md or .markdown")
	}
	authority, err := tasks.userMemory(ctx, conversationID)
	if err != nil {
		return MemoryMutation{}, err
	}
	if _, err := authority.grant(scope, "create"); err != nil {
		return MemoryMutation{}, err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return MemoryMutation{}, ErrMemoryDenied
	}
	files, err := NewFileTools(authority.project, tasks.core.Store.DataDir())
	if err != nil {
		return MemoryMutation{}, err
	}
	relative, err := filepath.Rel(authority.project, path)
	var raw []byte
	if err == nil {
		raw, _, err = files.read(filepath.ToSlash(relative))
	}
	_ = files.Close()
	if err != nil {
		return MemoryMutation{}, err
	}
	if len(raw) > tasks.Config().Memory.MaxDocumentBytes {
		return MemoryMutation{}, errors.New("harness: imported knowledge exceeds the configured document limit")
	}
	if title == "" {
		title = filepath.Base(path)
	}
	if requestKey == "" {
		requestKey = "import-" + newID()
	}
	mutation := MemoryMutation{
		RequestKey: requestKey, Action: "create", Scope: scope, Kind: "background",
		Title: title, Body: string(raw), Sources: []MemorySource{{Kind: "file", Path: path, Hash: contentHash(raw)}},
	}
	return mutation, mutation.validate()
}

func (tasks *TaskService) ImportKnowledgeFile(ctx context.Context, conversationID string, scope MemoryScope, path, title, requestKey string) (MemoryReceipt, error) {
	mutation, err := tasks.PreviewKnowledgeImport(ctx, conversationID, scope, path, title, requestKey)
	if err != nil {
		return MemoryReceipt{}, err
	}
	return tasks.SaveMemory(ctx, conversationID, mutation)
}

func (entry MemoryEntry) String() string {
	return fmt.Sprintf("%s (%s, %s)", entry.Title, entry.Kind, entry.State)
}
