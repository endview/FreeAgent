package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type patchArguments struct {
	Patch    string            `json:"patch"`
	Expected map[string]string `json:"expected_sha256"`
}
type patchFile struct {
	before, after string
	hunks         []patchHunk
}
type patchHunk struct {
	oldStart, oldCount, newStart, newCount int
	lines                                  []string
	noNewline                              map[int]bool
}

var patchHeader = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(?: .*)?$`)

func patchDefinition() ToolDefinition {
	return ToolDefinition{Name: "apply_patch", Effect: EffectModify, Description: "Apply a strict UTF-8 unified diff (--- a/path, +++ b/path, numbered @@ hunks; /dev/null for create/delete). Workspace-relative paths only; no renames, modes, binaries or fuzzy matching. expected_sha256 must contain every target's current SHA-256, or 'absent' for creation. Exact preview uses normal file permissions. Partial file results are retained; never replay UNKNOWN.", InputSchema: json.RawMessage(`{"type":"object","properties":{"patch":{"type":"string","maxLength":524288},"expected_sha256":{"type":"object","maxProperties":32,"additionalProperties":{"type":"string"}}},"required":["patch","expected_sha256"],"additionalProperties":false}`)}
}

func parsePatch(text string) ([]patchFile, error) {
	if len(text) == 0 || len(text) > 2*maxFileBytes || !utf8.ValidString(text) || strings.ContainsRune(text, 0) || !strings.HasSuffix(text, "\n") {
		return nil, errors.New("patch must be bounded NUL-free UTF-8 ending with a newline")
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	var files []patchFile
	for i := 0; i < len(lines); {
		if len(files) >= 32 || !strings.HasPrefix(lines[i], "--- ") || i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "+++ ") {
			return nil, errors.New("expected unified diff file headers; metadata, renames and binary patches are unsupported")
		}
		f := patchFile{before: strings.TrimPrefix(lines[i], "--- "), after: strings.TrimPrefix(lines[i+1], "+++ ")}
		i += 2
		for i < len(lines) && strings.HasPrefix(lines[i], "@@ ") {
			match := patchHeader.FindStringSubmatch(lines[i])
			if match == nil || len(f.hunks) >= 1024 {
				return nil, errors.New("invalid or excessive patch hunks")
			}
			i++
			values := make([]int, 4)
			for j := range values {
				value := match[j+1]
				if value == "" {
					value = "1"
				}
				n, err := strconv.Atoi(value)
				if err != nil || n > maxFileBytes {
					return nil, errors.New("patch range exceeds limit")
				}
				values[j] = n
			}
			h := patchHunk{oldStart: values[0], oldCount: values[1], newStart: values[2], newCount: values[3], noNewline: map[int]bool{}}
			old, new := 0, 0
			for i < len(lines) && (old < h.oldCount || new < h.newCount) {
				line := lines[i]
				i++
				if len(line) == 0 || !strings.ContainsRune(" +-", rune(line[0])) {
					return nil, errors.New("invalid patch hunk line")
				}
				if line[0] != '+' {
					old++
				}
				if line[0] != '-' {
					new++
				}
				if old > h.oldCount || new > h.newCount {
					return nil, errors.New("patch hunk line counts differ")
				}
				h.lines = append(h.lines, line)
				if i < len(lines) && lines[i] == `\ No newline at end of file` {
					h.noNewline[len(h.lines)-1] = true
					i++
				}
			}
			if old != h.oldCount || new != h.newCount {
				return nil, errors.New("incomplete patch hunk")
			}
			f.hunks = append(f.hunks, h)
		}
		if len(f.hunks) == 0 {
			return nil, errors.New("patch file needs at least one numbered hunk")
		}
		files = append(files, f)
	}
	return files, nil
}

func applyHunks(before string, hunks []patchHunk) (string, error) {
	eol := "\n"
	if strings.Contains(before, "\r\n") {
		eol = "\r\n"
	}
	source := strings.SplitAfter(before, "\n")
	if source[len(source)-1] == "" {
		source = source[:len(source)-1]
	}
	var out strings.Builder
	cursor, newLines := 0, 0
	for _, h := range hunks {
		start := h.oldStart
		if h.oldCount > 0 {
			start--
		}
		next := h.newStart
		if h.newCount > 0 {
			next--
		}
		if start < cursor || start > len(source) || next != newLines+start-cursor {
			return "", errors.New("patch hunks overlap or new ranges differ")
		}
		for cursor < start {
			out.WriteString(source[cursor])
			cursor++
			newLines++
		}
		for i, line := range h.lines {
			text := line[1:]
			ending := eol
			if h.noNewline[i] {
				ending = ""
			}
			if line[0] != '+' {
				if cursor >= len(source) || source[cursor] != text+ending {
					return "", errors.New("patch context differs from current file; no fuzzy matching")
				}
				cursor++
			}
			if line[0] != '-' {
				out.WriteString(text + ending)
				newLines++
			}
		}
	}
	for cursor < len(source) {
		out.WriteString(source[cursor])
		cursor++
	}
	if out.Len() > maxFileBytes {
		return "", errors.New("patched file exceeds 256 KiB")
	}
	return out.String(), nil
}

func (tools *FileTools) preparePatch(ctx context.Context, call ToolCall) (PreparedTool, error) {
	var args patchArguments
	if err := decodeArguments(call.Arguments, &args); err != nil {
		return PreparedTool{}, err
	}
	files, err := parsePatch(args.Patch)
	if err != nil {
		return PreparedTool{}, err
	}
	if len(args.Expected) != len(files) {
		return PreparedTool{}, errors.New("expected_sha256 must cover exactly every patch target")
	}
	changes := make([]preparedChange, 0, len(files))
	items := make([]ApprovalItem, 0, len(files))
	seen := map[string]bool{}
	var preview strings.Builder
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return PreparedTool{}, err
		}
		old, new := strings.TrimPrefix(file.before, "a/"), strings.TrimPrefix(file.after, "b/")
		operation, name := "edit", old
		if file.before == "/dev/null" {
			operation, name = "create", new
		} else if file.after == "/dev/null" {
			operation = "delete"
		} else if old != new {
			return PreparedTool{}, errors.New("patch renames are unsupported")
		}
		if name == "/dev/null" || strings.Contains(name, `\`) || path.Clean(name) != name || name == "." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || tools.protectedConfig(name) {
			return PreparedTool{}, errors.New("patch requires unprotected workspace-relative paths")
		}
		key := strings.ToLower(name)
		if seen[key] {
			return PreparedTool{}, errors.New("duplicate patch target")
		}
		seen[key] = true
		expected, present := args.Expected[name]
		if !present {
			return PreparedTool{}, errors.New("missing expected target hash")
		}
		var raw []byte
		mode := fs.FileMode(0600)
		if operation == "create" {
			if expected != "absent" {
				return PreparedTool{}, errors.New("patch creation requires expected absence")
			}
			if _, err := tools.validateCreatePath(name); err != nil {
				return PreparedTool{}, err
			}
		} else {
			if _, err := tools.validatePath(name); err != nil {
				return PreparedTool{}, err
			}
			raw, mode, err = tools.read(name)
			if err != nil {
				return PreparedTool{}, err
			}
			if len(expected) != 64 || contentHash(raw) != expected {
				return PreparedTool{}, errors.New("patch target hash changed; read again")
			}
		}
		updated, err := applyHunks(string(raw), file.hunks)
		if err != nil {
			return PreparedTool{}, err
		}
		if operation == "delete" && updated != "" {
			return PreparedTool{}, errors.New("delete patch must remove the entire file")
		}
		beforeHash := expected
		if operation == "create" {
			beforeHash = ""
		}
		change := preparedChange{args: changeArguments{Operation: operation, Path: name, Expected: beforeHash}, updated: []byte(updated), mode: mode}
		changes = append(changes, change)
		afterHash := contentHash(change.updated)
		if operation == "delete" {
			afterHash = ""
		}
		item := ApprovalItem{Kind: "file_" + operation, Target: name, Hash: beforeHash, Fields: map[string]string{"operation": operation, "before_sha256": beforeHash, "after_sha256": afterHash, "patch_sha256": contentHash([]byte(args.Patch)), "old_lines": strconv.Itoa(strings.Count(string(raw), "\n")), "new_lines": strconv.Itoa(strings.Count(updated, "\n"))}}
		if len(raw)+len(updated) <= 8192 {
			item.Old, item.New = string(raw), updated
		}
		items = append(items, item)
		fmt.Fprintf(&preview, "%s %s\nBefore: %s\nAfter: %s\n", operation, name, beforeHash, afterHash)
	}
	diff := args.Patch
	if len(diff) > 48<<10 {
		diff = truncateUTF8Bytes(diff, 48<<10) + "\n[patch preview truncated; inspect saved tool arguments for full patch]"
	}
	preview.WriteString(diff)
	return tools.prepareChangeExecution(changes, items, preview.String()), nil
}
