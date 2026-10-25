package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxFileBytes = 256 << 10

type FileTools struct {
	root            *os.Root
	workspace       string
	dataDir         string
	denied          string
	blocked         []string
	removeFile      func(string) error
	postDeleteLstat func(string) (fs.FileInfo, error)
}

func NewFileTools(workspace, dataDir string) (*FileTools, error) {
	workspace, err := canonicalDir(workspace)
	if err != nil {
		return nil, err
	}
	dataDir, err = canonicalDir(dataDir)
	if err != nil {
		return nil, err
	}
	if within(dataDir, workspace) {
		return nil, errors.New("harness: workspace cannot be inside the harness data directory")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	denied := ""
	if within(workspace, dataDir) {
		relative, _ := filepath.Rel(workspace, dataDir)
		denied = filepath.ToSlash(relative)
	}
	return &FileTools{root: root, workspace: workspace, dataDir: dataDir, denied: denied}, nil
}

func (tools *FileTools) Close() error { return tools.root.Close() }

func (tools *FileTools) withBlockedPaths(paths []string) *FileTools {
	if len(paths) == 0 {
		return tools
	}
	copy := *tools
	copy.blocked = append([]string(nil), paths...)
	return &copy
}

func (tools *FileTools) Definitions() []ToolDefinition {
	return append([]ToolDefinition{
		{Name: "file_list", Description: "List a workspace directory in sorted pages (at most 10,000 entries). Use next_offset when truncated. Restart from offset 0 if the directory changes. Links and .git are excluded.", Effect: EffectRead,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":0,"maximum":10000},"max_entries":{"type":"integer","minimum":1,"maximum":200}},"required":["path"],"additionalProperties":false}`)},
		{Name: "file_read", Description: "Read a UTF-8 workspace file (up to 256 KiB), optionally in byte-offset pages; returns the full file SHA-256 for edits. A page returns at least one whole character, even if max_bytes is smaller.", Effect: EffectRead,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":0},"max_bytes":{"type":"integer","minimum":1,"maximum":32768}},"required":["path"],"additionalProperties":false}`)},
		{Name: "file_search", Description: "Search literal text in UTF-8 files under a workspace directory. At most 100 matches.", Effect: EffectRead,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"query":{"type":"string"}},"required":["path","query"],"additionalProperties":false}`)},
		{Name: "file_edit", Description: "Replace exactly one occurrence in an existing UTF-8 file. Requires SHA-256 from file_read and user confirmation.", Effect: EffectModify,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"expected_sha256":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["path","expected_sha256","old_text","new_text"],"additionalProperties":false}`)},
		{Name: "file_create", Description: "Create a new UTF-8 file in an existing directory. Exact path and content require confirmation; never overwrite an existing file.", Effect: EffectModify,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)},
		{Name: "file_delete", Description: "Delete one existing regular workspace file. Requires its current SHA-256 and exact approval; never deletes directories, links, .git or Harness data.", Effect: EffectModify,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"expected_sha256":{"type":"string"}},"required":["path","expected_sha256"],"additionalProperties":false}`)},
		{Name: "file_batch_edit", Description: "Preview and confirm 1-32 exact file edits or creations together. Edits require hashes; creations require absent targets. Partial results are recorded individually, never rolled back or replayed.", Effect: EffectModify,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"changes":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"object","properties":{"operation":{"type":"string","enum":["edit","create"]},"path":{"type":"string"},"expected_sha256":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"},"content":{"type":"string"}},"required":["operation","path"],"additionalProperties":false}}},"required":["changes"],"additionalProperties":false}`)},
	}, patchDefinition())
}

type fileArguments struct {
	Path       string `json:"path"`
	Query      string `json:"query,omitempty"`
	Expected   string `json:"expected_sha256,omitempty"`
	Old        string `json:"old_text,omitempty"`
	New        string `json:"new_text,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	MaxBytes   int    `json:"max_bytes,omitempty"`
	MaxEntries int    `json:"max_entries,omitempty"`
}

func (tools *FileTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if err := ctx.Err(); err != nil {
		return PreparedTool{}, err
	}
	if call.Name == "apply_patch" {
		return tools.preparePatch(ctx, call)
	}
	if call.Name == "file_create" || call.Name == "file_batch_edit" {
		return tools.prepareChanges(ctx, call)
	}
	var args fileArguments
	decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
	decoder.DisallowUnknownFields()
	if len(call.Arguments) > 2*maxFileBytes || decoder.Decode(&args) != nil {
		return PreparedTool{}, errors.New("invalid file tool arguments")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return PreparedTool{}, errors.New("arguments must be one JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &fields); err != nil || fields == nil {
		return PreparedTool{}, errors.New("arguments must be an object")
	}
	allowed := map[string]bool{"path": true}
	switch call.Name {
	case "file_list":
		allowed["offset"], allowed["max_entries"] = true, true
	case "file_read":
		allowed["offset"], allowed["max_bytes"] = true, true
	case "file_search":
		allowed["query"] = true
	case "file_edit":
		for _, key := range []string{"expected_sha256", "old_text", "new_text"} {
			allowed[key] = true
			if _, ok := fields[key]; !ok {
				return PreparedTool{}, errors.New("edit is missing a required argument")
			}
		}
	case "file_delete":
		allowed["expected_sha256"] = true
		if _, ok := fields["expected_sha256"]; !ok {
			return PreparedTool{}, errors.New("delete is missing expected_sha256")
		}
	default:
		return PreparedTool{}, errors.New("unknown file tool")
	}
	for key := range fields {
		if !allowed[key] {
			return PreparedTool{}, errors.New("argument is not allowed for this tool")
		}
		if bytes.Equal(bytes.TrimSpace(fields[key]), []byte("null")) {
			return PreparedTool{}, errors.New("file tool arguments cannot be null")
		}
	}
	name, err := tools.validatePath(args.Path)
	if err != nil {
		return PreparedTool{}, err
	}
	switch call.Name {
	case "file_list":
		if args.Offset < 0 || args.Offset > 10000 || args.MaxEntries < 0 || args.MaxEntries > 200 {
			return PreparedTool{}, errors.New("invalid file list page")
		}
		if _, present := fields["max_entries"]; present && args.MaxEntries == 0 {
			return PreparedTool{}, errors.New("max_entries must be between 1 and 200")
		}
		pageSize := args.MaxEntries
		if pageSize == 0 {
			pageSize = 100
		}
		return PreparedTool{Effect: EffectRead, Execute: func(ctx context.Context) (ToolResult, error) {
			return tools.list(ctx, name, args.Offset, pageSize)
		}}, nil
	case "file_read":
		if args.Offset < 0 || args.MaxBytes < 0 || args.MaxBytes > 32768 {
			return PreparedTool{}, errors.New("invalid file read offset or size")
		}
		if _, present := fields["max_bytes"]; present && args.MaxBytes == 0 {
			return PreparedTool{}, errors.New("max_bytes must be between 1 and 32768")
		}
		limit := args.MaxBytes
		if limit == 0 {
			limit = 4096
		}
		return PreparedTool{Effect: EffectRead, Execute: func(ctx context.Context) (ToolResult, error) {
			if err := ctx.Err(); err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: "cancelled before read"}
			}
			raw, _, err := tools.read(name)
			if err != nil {
				return ToolResult{}, &CallError{Certain: true, Message: err.Error()}
			}
			start := min(args.Offset, len(raw))
			for start > 0 && start < len(raw) && !utf8.RuneStart(raw[start]) {
				start--
			}
			end := min(start+limit, len(raw))
			for end > start && !utf8.Valid(raw[start:end]) {
				end--
			}
			if end == start && start < len(raw) {
				_, size := utf8.DecodeRune(raw[start:])
				end = min(start+size, len(raw))
			}
			return jsonResult(map[string]any{"path": name, "sha256": contentHash(raw), "content": string(raw[start:end]), "offset": start, "next_offset": end, "truncated": end < len(raw)})
		}}, nil
	case "file_search":
		if args.Query == "" || len(args.Query) > 4096 {
			return PreparedTool{}, errors.New("search query must contain 1 to 4096 bytes")
		}
		return PreparedTool{Effect: EffectRead, Execute: func(ctx context.Context) (ToolResult, error) {
			return tools.search(ctx, name, args.Query)
		}}, nil
	case "file_edit":
		if len(args.Expected) != 64 || args.Old == "" {
			return PreparedTool{}, errors.New("edit requires expected_sha256 and nonempty old_text")
		}
		raw, mode, err := tools.read(name)
		if err != nil {
			return PreparedTool{}, err
		}
		if contentHash(raw) != args.Expected {
			return PreparedTool{}, errors.New("file content changed; read it again before editing")
		}
		if strings.Count(string(raw), args.Old) != 1 {
			return PreparedTool{}, errors.New("old_text must match exactly once")
		}
		updated := []byte(strings.Replace(string(raw), args.Old, args.New, 1))
		if len(updated) > maxFileBytes || !utf8.Valid(updated) || bytes.ContainsRune(updated, 0) {
			return PreparedTool{}, errors.New("edited text exceeds limit or is not NUL-free UTF-8")
		}
		return PreparedTool{
			Effect:        EffectModify,
			workspaceFile: !tools.protectedConfig(name),
			Preview:       fmt.Sprintf("%s\nSHA-256: %s\n--- replace\n%s\n+++ with\n%s", name, args.Expected, args.Old, args.New),
			Items:         []ApprovalItem{{Kind: "file_edit", Target: name, Hash: args.Expected, Old: args.Old, New: args.New}},
			Execute: func(ctx context.Context) (ToolResult, error) {
				return tools.edit(ctx, name, args.Expected, updated, mode)
			},
			Reject: func() ToolResult {
				return ToolResult{Content: "user declined this edit; do not repeat it", IsError: true,
					Receipts: []ToolReceipt{{Kind: "file", Path: name, Status: "rejected", BeforeHash: args.Expected}}}
			},
		}, nil
	case "file_delete":
		if len(args.Expected) != 64 {
			return PreparedTool{}, errors.New("delete requires expected_sha256")
		}
		raw, err := tools.hash(name)
		if err != nil {
			return PreparedTool{}, err
		}
		if contentHash(raw) != args.Expected {
			return PreparedTool{}, errors.New("file content changed; read it again before deleting")
		}
		execute := func(ctx context.Context, record func(ToolResult) error) (ToolResult, error) {
			return tools.delete(ctx, name, args.Expected, record)
		}
		return PreparedTool{
			Effect:          EffectModify,
			workspaceFile:   !tools.protectedConfig(name),
			Preview:         fmt.Sprintf("%s\nDELETE (must still match)\nSHA-256: %s\nThe target is checked after the delete; uncertain outcomes are not replayed.", name, args.Expected),
			Items:           []ApprovalItem{{Kind: "file_delete", Target: name, Hash: args.Expected}},
			ExecuteRecorded: execute,
			Execute: func(ctx context.Context) (ToolResult, error) {
				return execute(ctx, func(ToolResult) error { return nil })
			},
			Reject: func() ToolResult {
				return ToolResult{Content: "user declined this deletion; do not repeat it", IsError: true,
					Receipts: []ToolReceipt{{Kind: "file", Path: name, Status: "rejected", BeforeHash: args.Expected}}}
			},
		}, nil
	default:
		return PreparedTool{}, fmt.Errorf("unknown tool %q", call.Name)
	}
}

func within(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

// protectedConfig reports paths under the workspace .freeagent directory.
// Trusted workspace skills and extensions load from there, so Workspace mode
// must not auto-approve changes that could alter what later runs as code.
func (tools *FileTools) protectedConfig(name string) bool {
	first, _, _ := strings.Cut(name, "/")
	if strings.EqualFold(strings.TrimRight(first, ". "), ".freeagent") {
		return true
	}
	// Windows short names can alias the same directory under another spelling.
	config, err := tools.root.Lstat(".freeagent")
	if err != nil {
		return false
	}
	info, err := tools.root.Lstat(filepath.FromSlash(first))
	return err == nil && os.SameFile(config, info)
}

func (tools *FileTools) validatePath(input string) (string, error) {
	input = strings.ReplaceAll(input, `\`, "/")
	if input == "" || !filepath.IsLocal(filepath.FromSlash(input)) || strings.Contains(input, ":") {
		return "", errors.New("file path must be relative to the workspace")
	}
	name := path.Clean(input)
	if tools.denied != "" && (strings.EqualFold(name, tools.denied) || strings.HasPrefix(strings.ToLower(name), strings.ToLower(tools.denied)+"/")) {
		return "", errors.New("harness data is not accessible to file tools")
	}
	absolute := filepath.Clean(filepath.Join(tools.workspace, filepath.FromSlash(name)))
	for _, blocked := range tools.blocked {
		if within(blocked, absolute) {
			return "", ErrPathBlocked
		}
	}
	current := ""
	for _, part := range strings.Split(name, "/") {
		if strings.EqualFold(part, ".git") || part == ".." {
			return "", errors.New("protected path")
		}
		current = path.Join(current, part)
		info, err := tools.root.Lstat(filepath.FromSlash(current))
		if err != nil {
			return "", errors.New("file path does not exist")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("links are not allowed")
		}
		if err := rejectReparsePoint(filepath.Join(tools.workspace, filepath.FromSlash(current))); err != nil {
			return "", errors.New("reparse points are not allowed")
		}
	}
	return name, nil
}

func (tools *FileTools) read(name string) ([]byte, fs.FileMode, error) {
	if _, err := tools.validatePath(name); err != nil {
		return nil, 0, err
	}
	file, err := openWorkspaceFile(tools.root, filepath.FromSlash(name))
	if err != nil {
		return nil, 0, errors.New("cannot open workspace file")
	}
	defer file.Close()
	if err := checkRegularHandle(file); err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() > maxFileBytes {
		return nil, 0, errors.New("file exceeds 256 KiB limit")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil || len(raw) > maxFileBytes || !utf8.Valid(raw) || bytes.Contains(raw, []byte{0}) {
		return nil, 0, errors.New("file is too large or not UTF-8 text")
	}
	return raw, info.Mode().Perm(), nil
}

// hash verifies a bounded regular file without requiring it to be UTF-8 text.
// Deletion only needs an exact content binding; it does not expose the bytes.
func (tools *FileTools) hash(name string) ([]byte, error) {
	if _, err := tools.validatePath(name); err != nil {
		return nil, err
	}
	file, err := openWorkspaceFile(tools.root, filepath.FromSlash(name))
	if err != nil {
		return nil, errors.New("cannot open workspace file")
	}
	defer file.Close()
	if err := checkRegularHandle(file); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() > maxFileBytes {
		return nil, errors.New("file exceeds 256 KiB limit")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil || len(raw) > maxFileBytes {
		return nil, errors.New("file is too large to delete safely")
	}
	return raw, nil
}

func (tools *FileTools) delete(ctx context.Context, name, expected string, record func(ToolResult) error) (ToolResult, error) {
	result := ToolResult{IsError: true, Receipts: []ToolReceipt{{Kind: "file", Path: name, Status: "unknown", BeforeHash: expected}}}
	if err := ctx.Err(); err != nil {
		result.Receipts[0].Status = "not_launched"
		result.Content = "file deletion not launched: cancelled"
		return result, nil
	}
	raw, err := tools.hash(name)
	if err != nil || contentHash(raw) != expected {
		result.Receipts[0].Status = "failed"
		result.Receipts[0].Detail = "delete target changed after confirmation"
		result.Content = "file deletion failed: target changed after confirmation"
		return result, &CallError{Certain: true, Message: result.Content}
	}
	result.Receipts[0].Detail = "delete dispatch recorded; final target state not yet confirmed"
	result.Content = "file deletion dispatched; final target state not yet confirmed"
	if err := record(result); err != nil {
		result.Receipts[0].Status = "not_launched"
		result.Receipts[0].Detail = "delete was not launched because its dispatch receipt could not be persisted"
		result.Content = "file deletion not launched: dispatch receipt could not be persisted"
		return result, &CallError{Certain: true, Message: result.Content}
	}
	removeErr := tools.remove(name)
	_, verifyErr := tools.deletedTargetLstat(name)
	absent := errors.Is(verifyErr, os.ErrNotExist)
	targetRemains := verifyErr == nil
	if removeErr == nil && absent {
		result.IsError = false
		result.Receipts[0].Status = "applied"
		encoded, _ := jsonResult(map[string]any{"path": name, "deleted": true})
		result.Content = encoded.Content
		return result, nil
	}
	if removeErr != nil && absent {
		result.IsError = false
		result.Receipts[0].Status = "applied"
		result.Receipts[0].Detail = "delete returned an error, but post-check confirmed the target is absent"
		encoded, _ := jsonResult(map[string]any{"path": name, "deleted": true, "verified": true})
		result.Content = encoded.Content
		return result, nil
	}
	if removeErr != nil && targetRemains {
		result.Receipts[0].Status = "failed"
		result.Receipts[0].Detail = "delete failed and the target is still present"
		result.Content = "file deletion failed; target remains present"
		return result, &CallError{Certain: true, Message: result.Content}
	}
	result.Receipts[0].Detail = "delete outcome could not be confirmed; inspect before repeating"
	result.Content = "file deletion outcome UNKNOWN; inspect before repeating"
	recordErr := record(result)
	return result, errors.Join(&CallError{Message: result.Content}, recordErr)
}

func (tools *FileTools) remove(name string) error {
	if tools.removeFile != nil {
		return tools.removeFile(name)
	}
	parent, err := tools.root.OpenRoot(filepath.FromSlash(path.Dir(name)))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Remove(path.Base(name))
}

func (tools *FileTools) deletedTargetLstat(name string) (fs.FileInfo, error) {
	if tools.postDeleteLstat != nil {
		return tools.postDeleteLstat(name)
	}
	return tools.root.Lstat(filepath.FromSlash(name))
}

func (tools *FileTools) list(ctx context.Context, name string, offset, pageSize int) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cancelled before list"}
	}
	if _, err := tools.validatePath(name); err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "directory changed after preparation"}
	}
	directory, err := tools.root.Open(filepath.FromSlash(name))
	if err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cannot open directory"}
	}
	defer directory.Close()
	entries, err := directory.ReadDir(10001)
	if err != nil && err != io.EOF {
		return ToolResult{}, &CallError{Certain: true, Message: "cannot list directory"}
	}
	if len(entries) > 10000 {
		return ToolResult{}, &CallError{Certain: true, Message: "directory exceeds 10000 entries; select a narrower directory"}
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	result := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return ToolResult{}, &CallError{Certain: true, Message: "directory listing cancelled"}
		}
		if _, err := tools.validatePath(path.Join(name, entry.Name())); err != nil {
			continue
		}
		result = append(result, map[string]any{"name": entry.Name(), "directory": entry.IsDir()})
	}
	if offset > len(result) {
		offset = len(result)
	}
	end := min(offset+pageSize, len(result))
	page := result[offset:end]
	return jsonResult(map[string]any{"path": name, "entries": page, "offset": offset, "next_offset": end, "truncated": end < len(result)})
}

func (tools *FileTools) search(ctx context.Context, name, query string) (ToolResult, error) {
	matches := make([]map[string]any, 0)
	visited := 0
	bytesRead := 0
	truncated := false
	err := fs.WalkDir(tools.root.FS(), name, func(current string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > 10000 || bytesRead > 8<<20 || len(matches) >= 100 {
			truncated = true
			return fs.SkipAll
		}
		if walkErr != nil {
			return nil
		}
		if _, err := tools.validatePath(current); err != nil {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		raw, _, err := tools.read(current)
		if err != nil {
			return nil
		}
		bytesRead += len(raw)
		for line, text := range strings.Split(string(raw), "\n") {
			if strings.Contains(text, query) {
				if len(text) > 1000 {
					text = string([]rune(text)[:min(len([]rune(text)), 250)])
				}
				matches = append(matches, map[string]any{"path": current, "line": line + 1, "text": text})
				if len(matches) >= 100 {
					truncated = true
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "search stopped"}
	}
	return jsonResult(map[string]any{"matches": matches, "truncated": truncated})
}

func (tools *FileTools) edit(ctx context.Context, name, expected string, updated []byte, mode fs.FileMode) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cancelled before edit"}
	}
	if _, err := tools.validatePath(name); err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "edit target changed after preparation"}
	}
	parent, err := tools.root.OpenRoot(filepath.FromSlash(path.Dir(name)))
	if err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cannot open edit directory"}
	}
	defer parent.Close()
	base := path.Base(name)
	current, err := openWorkspaceFile(parent, base)
	if err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cannot reopen edit target"}
	}
	checkErr := checkRegularHandle(current)
	raw, readErr := io.ReadAll(io.LimitReader(current, maxFileBytes+1))
	_ = current.Close()
	if checkErr != nil || readErr != nil || contentHash(raw) != expected {
		return ToolResult{}, &CallError{Certain: true, Message: "edit conflict; file changed after confirmation"}
	}
	tempName := ".freeagent-edit-" + newID()
	temp, err := parent.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cannot stage edit"}
	}
	defer parent.Remove(tempName)
	_, writeErr := temp.Write(updated)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if errors.Join(writeErr, syncErr, closeErr) != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cannot flush staged edit"}
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cancelled before edit publication"}
	}
	current, err = openWorkspaceFile(parent, base)
	if err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "edit target changed during staging"}
	}
	checkErr = checkRegularHandle(current)
	raw, readErr = io.ReadAll(io.LimitReader(current, maxFileBytes+1))
	_ = current.Close()
	if checkErr != nil || readErr != nil || contentHash(raw) != expected {
		return ToolResult{}, &CallError{Certain: true, Message: "edit conflict; file changed during staging"}
	}
	if err := parent.Rename(tempName, base); err != nil {
		return ToolResult{}, &CallError{Message: "edit publication outcome unknown"}
	}
	result, err := jsonResult(map[string]any{"path": name, "sha256": contentHash(updated), "changed": true})
	result.Receipts = []ToolReceipt{{Kind: "file", Path: name, Status: "applied", BeforeHash: expected, AfterHash: contentHash(updated)}}
	return result, err
}

func jsonResult(value any) (ToolResult, error) {
	raw, err := json.Marshal(value)
	result := ToolResult{Content: string(raw)}
	if object, ok := value.(map[string]any); ok {
		result.Truncated, _ = object["truncated"].(bool)
	}
	return result, err
}
