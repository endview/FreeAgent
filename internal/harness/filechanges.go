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
	"strings"
	"unicode/utf8"
)

type changeArguments struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Expected  string `json:"expected_sha256,omitempty"`
	Old       string `json:"old_text,omitempty"`
	New       string `json:"new_text,omitempty"`
	Content   string `json:"content,omitempty"`
}

type preparedChange struct {
	args    changeArguments
	updated []byte
	mode    fs.FileMode
}

func decodeArguments(raw json.RawMessage, target any) error {
	if len(raw) == 0 || len(raw) > 2*maxFileBytes {
		return errors.New("tool arguments exceed limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid tool arguments")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("arguments must contain one JSON object")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return errors.New("arguments must be an object")
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("arguments cannot be null")
		}
	}
	return nil
}

func (tools *FileTools) prepareChanges(ctx context.Context, call ToolCall) (PreparedTool, error) {
	var rawChanges []json.RawMessage
	if call.Name == "file_create" {
		var args struct {
			Path    string  `json:"path"`
			Content *string `json:"content"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return PreparedTool{}, err
		}
		if args.Content == nil {
			return PreparedTool{}, errors.New("creation requires content")
		}
		raw, _ := json.Marshal(changeArguments{Operation: "create", Path: args.Path, Content: *args.Content})
		rawChanges = []json.RawMessage{raw}
	} else {
		var args struct {
			Changes []json.RawMessage `json:"changes"`
		}
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return PreparedTool{}, err
		}
		if len(args.Changes) < 1 || len(args.Changes) > 32 {
			return PreparedTool{}, errors.New("batch requires 1 to 32 concrete changes")
		}
		rawChanges = args.Changes
	}
	changes := make([]preparedChange, 0, len(rawChanges))
	items := make([]ApprovalItem, 0, len(rawChanges))
	seen := map[string]bool{}
	var preview strings.Builder
	for _, raw := range rawChanges {
		if err := ctx.Err(); err != nil {
			return PreparedTool{}, err
		}
		var args changeArguments
		if err := decodeArguments(raw, &args); err != nil {
			return PreparedTool{}, err
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		var change preparedChange
		switch args.Operation {
		case "create":
			if _, ok := fields["content"]; !ok && call.Name != "file_create" {
				return PreparedTool{}, errors.New("creation requires content, even when empty")
			}
			for _, field := range []string{"expected_sha256", "old_text", "new_text"} {
				if _, exists := fields[field]; exists {
					return PreparedTool{}, errors.New("creation cannot contain edit arguments")
				}
			}
			name, err := tools.validateCreatePath(args.Path)
			if err != nil {
				return PreparedTool{}, err
			}
			args.Path = name
			change = preparedChange{args: args, updated: []byte(args.Content), mode: 0o600}
			items = append(items, ApprovalItem{Kind: "file_create", Target: name,
				Hash: contentHash(change.updated), New: args.Content})
			fmt.Fprintf(&preview, "%s\nCREATE (must not exist)\nSHA-256: %s\n+++ content\n%s\n\n", name, contentHash(change.updated), args.Content)
		case "edit":
			if _, exists := fields["content"]; exists {
				return PreparedTool{}, errors.New("edit cannot contain creation content")
			}
			if _, exists := fields["new_text"]; !exists {
				return PreparedTool{}, errors.New("edit requires new_text, even when empty")
			}
			name, err := tools.validatePath(args.Path)
			if err != nil {
				return PreparedTool{}, err
			}
			raw, mode, err := tools.read(name)
			if err != nil || contentHash(raw) != args.Expected || args.Old == "" ||
				strings.Count(string(raw), args.Old) != 1 {
				return PreparedTool{}, errors.New("edit requires current hash and exactly one old_text match")
			}
			args.Path = name
			change = preparedChange{args: args, mode: mode, updated: []byte(strings.Replace(string(raw), args.Old, args.New, 1))}
			items = append(items, ApprovalItem{Kind: "file_edit", Target: name,
				Hash: args.Expected, Old: args.Old, New: args.New})
			fmt.Fprintf(&preview, "%s\nSHA-256: %s\n--- replace\n%s\n+++ with\n%s\n\n", name, args.Expected, args.Old, args.New)
		default:
			return PreparedTool{}, errors.New("batch operation must be edit or create")
		}
		if len(change.updated) > maxFileBytes || !utf8.Valid(change.updated) || bytes.ContainsRune(change.updated, 0) {
			return PreparedTool{}, errors.New("change exceeds limit or is not NUL-free UTF-8")
		}
		key := strings.ToLower(args.Path)
		if seen[key] {
			return PreparedTool{}, errors.New("batch paths must be distinct")
		}
		seen[key] = true
		changes = append(changes, change)
	}
	return tools.prepareChangeExecution(changes, items, preview.String()), nil
}

func (tools *FileTools) prepareChangeExecution(changes []preparedChange, items []ApprovalItem, preview string) PreparedTool {
	initial := func(status string) ToolResult {
		result := ToolResult{IsError: status != "applied"}
		for _, change := range changes {
			result.Receipts = append(result.Receipts, ToolReceipt{Kind: "file", Path: change.args.Path,
				Status: status, BeforeHash: change.args.Expected})
		}
		return changesResult(result)
	}
	execute := func(ctx context.Context, record func(ToolResult) error) (ToolResult, error) {
		result := initial("skipped")
		// Invalidate the whole approval before the first write if any target drifted.
		for index, change := range changes {
			if err := tools.checkChange(change); err != nil {
				result.Receipts[index].Status = "rejected"
				result.Receipts[index].Detail = "preview invalidated: " + safeError(err)
				return changesResult(result), nil
			}
		}
		for index, change := range changes {
			if ctx.Err() != nil {
				return changesResult(result), nil
			}
			result.Receipts[index].Status = "unknown"
			if err := record(changesResult(result)); err != nil {
				return result, &CallError{Message: "could not persist batch dispatch progress"}
			}
			var item ToolResult
			var err error
			if change.args.Operation == "delete" {
				item, err = tools.delete(ctx, change.args.Path, change.args.Expected, func(progress ToolResult) error {
					if len(progress.Receipts) != 0 {
						result.Receipts[index] = progress.Receipts[0]
					}
					return record(changesResult(result))
				})
			} else if change.args.Operation == "create" {
				item, err = tools.create(ctx, change.args.Path, change.updated)
			} else {
				item, err = tools.edit(ctx, change.args.Path, change.args.Expected, change.updated, change.mode)
			}
			if err != nil {
				var certainty *CallError
				if !errors.As(err, &certainty) || !certainty.Certain {
					result.Receipts[index].Detail = safeError(err)
					if saveErr := record(changesResult(result)); saveErr != nil {
						err = errors.Join(err, saveErr)
					}
					return result, err
				}
				result.Receipts[index].Status = "failed"
				result.Receipts[index].Detail = safeError(err)
			} else {
				result.Receipts[index] = item.Receipts[0]
			}
			result = changesResult(result)
			if err := record(result); err != nil {
				return result, &CallError{Message: "could not persist batch result; no subsequent file was dispatched"}
			}
			if err != nil {
				break
			}
		}
		return changesResult(result), nil
	}
	workspaceFile := true
	for _, change := range changes {
		if tools.protectedConfig(change.args.Path) {
			workspaceFile = false
		}
	}
	return PreparedTool{Effect: EffectModify, workspaceFile: workspaceFile, Preview: preview, Items: items, ExecuteRecorded: execute,
		Execute: func(ctx context.Context) (ToolResult, error) {
			return execute(ctx, func(ToolResult) error { return nil })
		},
		Reject: func() ToolResult { return initial("rejected") }}
}

func changesResult(result ToolResult) ToolResult {
	result.IsError = false
	for _, receipt := range result.Receipts {
		if receipt.Status != "applied" {
			result.IsError = true
		}
	}
	raw, _ := json.Marshal(map[string]any{"files": result.Receipts, "partial": result.IsError})
	result.Content = string(raw)
	return result
}

func (tools *FileTools) checkChange(change preparedChange) error {
	if change.args.Operation == "create" {
		_, err := tools.validateCreatePath(change.args.Path)
		return err
	}
	raw, _, err := tools.read(change.args.Path)
	if err != nil || contentHash(raw) != change.args.Expected {
		return errors.New("file changed; read again and request a new confirmation")
	}
	return nil
}

func (tools *FileTools) validateCreatePath(input string) (string, error) {
	input = strings.ReplaceAll(input, `\`, "/")
	if input == "" || !filepath.IsLocal(filepath.FromSlash(input)) || strings.ContainsAny(input, ":\x00") {
		return "", errors.New("creation requires a relative workspace path")
	}
	name := path.Clean(input)
	if name == "." || strings.EqualFold(path.Base(name), ".git") {
		return "", errors.New("protected creation path")
	}
	if _, err := tools.validatePath(path.Dir(name)); err != nil {
		return "", err
	}
	if tools.denied != "" && (strings.EqualFold(name, tools.denied) || strings.HasPrefix(strings.ToLower(name), strings.ToLower(tools.denied)+"/")) {
		return "", errors.New("harness data is not accessible")
	}
	if _, err := tools.root.Lstat(filepath.FromSlash(name)); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("creation target already exists or cannot be inspected")
	}
	return name, nil
}

func (tools *FileTools) create(ctx context.Context, name string, content []byte) (ToolResult, error) {
	if ctx.Err() != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cancelled before creation"}
	}
	if _, err := tools.validateCreatePath(name); err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "creation target changed after preview"}
	}
	parent, err := tools.root.OpenRoot(filepath.FromSlash(path.Dir(name)))
	if err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "cannot open creation directory"}
	}
	defer parent.Close()
	file, err := parent.OpenFile(path.Base(name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ToolResult{}, &CallError{Certain: true, Message: "creation target exists or cannot be opened"}
	}
	// Once the final path exists, a failed write may leave a partial file.
	_, writeErr := file.Write(content)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return ToolResult{}, &CallError{Message: "creation may have left a partial file; inspect before retrying"}
	}
	result, _ := jsonResult(map[string]any{"path": name, "sha256": contentHash(content), "changed": true})
	result.Receipts = []ToolReceipt{{Kind: "file", Path: name, Status: "applied", AfterHash: contentHash(content)}}
	return result, nil
}
