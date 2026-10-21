package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileBroker is the per-Run file boundary. It carries the frozen policy into
// every file_* preparation instead of consulting mutable global permissions.
type FileBroker struct {
	tools  *FileTools
	policy FileAccessPolicy
}

func NewFileBroker(tools *FileTools, policy FileAccessPolicy) *FileBroker {
	return &FileBroker{tools: tools, policy: policy}
}

func (broker *FileBroker) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if broker == nil {
		return PreparedTool{}, errors.New("harness: file broker is unavailable")
	}
	return broker.tools.prepareWithPolicy(ctx, call, broker.policy)
}

// PrepareWithPolicy remains the narrow adapter used by RuntimeToolView; the
// actual dispatch is owned by FileBroker so future request receipts have one
// enforcement point.
func (tools *FileTools) PrepareWithPolicy(ctx context.Context, call ToolCall, policy FileAccessPolicy) (PreparedTool, error) {
	return NewFileBroker(tools, policy).Prepare(ctx, call)
}

func (tools *FileTools) prepareWithPolicy(ctx context.Context, call ToolCall, policy FileAccessPolicy) (PreparedTool, error) {
	if tools == nil {
		return PreparedTool{}, errors.New("harness: file runtime is unavailable")
	}
	paths, err := fileCallPaths(call)
	if err != nil || len(paths) == 0 {
		return tools.withBlockedPaths(policy.BlockedPaths).Prepare(ctx, call)
	}
	hasAbsolute := false
	hasRelative := false
	for _, value := range paths {
		if isAbsoluteFilePath(value) {
			hasAbsolute = true
		} else {
			hasRelative = true
		}
	}
	if !hasAbsolute {
		return tools.withBlockedPaths(policy.BlockedPaths).Prepare(ctx, call)
	}
	if hasRelative {
		return PreparedTool{}, errors.New("harness: one file operation cannot mix workspace and computer paths")
	}
	if !policy.ComputerFiles {
		return PreparedTool{}, ErrComputerFilesDisabled
	}
	legacyExternal := policy.Scope == ""
	if policy.Scope == "" {
		policy.Scope = FileScopeWorkspacePlusAllowedRoots
	}
	policy.Scope = canonicalFileScope(policy.Scope)
	if policy.Scope == FileScopeWorkspace {
		return PreparedTool{}, ErrPathNotAllowed
	}
	if err := policy.Scope.Validate(); err != nil {
		return PreparedTool{}, err
	}
	authorizedRoots := append([]string(nil), policy.ComputerRoots...)
	if policy.Scope == FileScopeSelectedRoots && !containsPath(authorizedRoots, tools.workspace) {
		authorizedRoots = append(authorizedRoots, tools.workspace)
	}

	rootPath, labels, rewritten, err := tools.rewriteExternalCall(call, paths, authorizedRoots, policy.Scope, policy.BlockedPaths)
	if err != nil {
		return PreparedTool{}, err
	}
	scoped, err := NewFileTools(rootPath, tools.dataDir)
	if err != nil {
		return PreparedTool{}, err
	}
	scoped.blocked = append([]string(nil), policy.BlockedPaths...)
	prepared, err := scoped.Prepare(ctx, rewritten)
	if err != nil {
		_ = scoped.Close()
		return PreparedTool{}, err
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = scoped.Close() }) }
	prepared.release = release
	prepared.workspaceFile = !legacyExternal && (policy.Scope == FileScopeWorkspacePlusAllowedRoots || policy.Scope == FileScopeAllNonProtected)
	if prepared.Effect == EffectRead {
		prepared.approvalRequired = legacyExternal
		if len(prepared.Items) == 0 {
			first := "external path"
			for _, label := range labels {
				first = label
				break
			}
			prepared.Items = []ApprovalItem{{Kind: "file_read", Target: first}}
		}
		if prepared.Preview == "" {
			prepared.Preview = "Read external file path\n" + prepared.Items[0].Target + "\nThe path is limited to the Run's authorized computer file root."
		}
		prepared.Reject = func() ToolResult {
			return ToolResult{Content: "user declined this external file read; do not repeat it", IsError: true,
				Receipts: []ToolReceipt{{Kind: "file", Path: prepared.Items[0].Target, Status: "rejected"}}}
		}
	}
	for index := range prepared.Items {
		prepared.Items[index].Target = externalLabel(labels, prepared.Items[index].Target, scoped.workspace)
	}
	prepared.Preview = externalPreview(prepared.Preview, labels)
	if prepared.Execute != nil {
		execute := prepared.Execute
		prepared.Execute = func(execCtx context.Context) (ToolResult, error) {
			defer release()
			result, execErr := execute(execCtx)
			return translateExternalResult(result, scoped.workspace), execErr
		}
	}
	if prepared.ExecuteRecorded != nil {
		execute := prepared.ExecuteRecorded
		prepared.ExecuteRecorded = func(execCtx context.Context, record func(ToolResult) error) (ToolResult, error) {
			defer release()
			return execute(execCtx, func(result ToolResult) error {
				return record(translateExternalResult(result, scoped.workspace))
			})
		}
	}
	if prepared.Reject != nil {
		reject := prepared.Reject
		prepared.Reject = func() ToolResult {
			defer release()
			return translateExternalResult(reject(), scoped.workspace)
		}
	}
	return prepared, nil
}

func fileCallPaths(call ToolCall) ([]string, error) {
	if !isFileToolName(call.Name) {
		return nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &object); err != nil || object == nil {
		return nil, err
	}
	if call.Name != "file_batch_edit" {
		raw, ok := object["path"]
		if !ok {
			return nil, nil
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, nil
		}
		return []string{value}, nil
	}
	var changes []map[string]json.RawMessage
	if json.Unmarshal(object["changes"], &changes) != nil {
		return nil, nil
	}
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		var value string
		if json.Unmarshal(change["path"], &value) != nil {
			return nil, nil
		}
		paths = append(paths, value)
	}
	return paths, nil
}

func isFileToolName(name string) bool {
	switch name {
	case "file_list", "file_read", "file_search", "file_edit", "file_create", "file_delete", "file_batch_edit":
		return true
	default:
		return false
	}
}

func isAbsoluteFilePath(value string) bool {
	value = strings.TrimSpace(value)
	return filepath.IsAbs(value) || len(value) >= 3 && value[1] == ':' && (value[2] == '\\' || value[2] == '/') || strings.HasPrefix(value, `\\`)
}

func (tools *FileTools) rewriteExternalCall(call ToolCall, paths, roots []string, scope FileScope, blocked []string) (string, map[string]string, ToolCall, error) {
	if len(paths) == 0 {
		return "", nil, call, errors.New("harness: file path is required")
	}
	resolved := make([]string, len(paths))
	rootPath := ""
	labels := map[string]string{}
	for index, raw := range paths {
		validationRoots := roots
		if scope == FileScopeAllNonProtected {
			validationRoots = []string{filesystemRoot(raw)}
		}
		value, err := validateExternalPathWithBlocked(tools.dataDir, validationRoots, blocked, raw)
		if err != nil {
			return "", nil, call, err
		}
		root := externalRootFor(value, validationRoots)
		if root == "" && scope == FileScopeAllNonProtected {
			root = filesystemRoot(value)
		}
		if root == "" {
			return "", nil, call, ErrPathNotAllowed
		}
		if rootPath == "" {
			rootPath = root
		} else if !samePath(rootPath, root) {
			return "", nil, call, errors.New("harness: batch file paths must use one authorized root")
		}
		relative, err := filepath.Rel(root, value)
		if err != nil || !filepath.IsLocal(relative) {
			return "", nil, call, ErrPathNotAllowed
		}
		logical := path.Clean(filepath.ToSlash(relative))
		resolved[index] = logical
		labels[logical] = filepath.Clean(value)
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &object); err != nil || object == nil {
		return "", nil, call, errors.New("invalid file tool arguments")
	}
	if call.Name != "file_batch_edit" {
		encoded, _ := json.Marshal(resolved[0])
		object["path"] = encoded
	} else {
		var changes []map[string]json.RawMessage
		if err := json.Unmarshal(object["changes"], &changes); err != nil {
			return "", nil, call, errors.New("invalid batch file arguments")
		}
		for index := range changes {
			encoded, _ := json.Marshal(resolved[index])
			changes[index]["path"] = encoded
		}
		encoded, _ := json.Marshal(changes)
		object["changes"] = encoded
	}
	rewritten, err := json.Marshal(object)
	if err != nil {
		return "", nil, call, err
	}
	call.Arguments = rewritten
	return rootPath, labels, call, nil
}

func filesystemRoot(target string) string {
	clean := filepath.Clean(target)
	volume := filepath.VolumeName(clean)
	if volume != "" {
		return filepath.Join(volume+string(filepath.Separator), "")
	}
	return string(filepath.Separator)
}

func externalRootFor(target string, roots []string) string {
	candidates := make([]string, 0, len(roots))
	for _, root := range roots {
		if samePath(root, target) {
			// os.Root opens directories. A file authorization is represented by
			// its parent root and remains restricted by the exact validation above.
			if info, err := os.Stat(root); err == nil && info.Mode().IsRegular() {
				candidates = append(candidates, filepath.Dir(root))
				continue
			}
		}
		if within(root, target) {
			candidates = append(candidates, root)
		}
	}
	sort.Slice(candidates, func(left, right int) bool { return len(candidates[left]) > len(candidates[right]) })
	if len(candidates) == 0 {
		return ""
	}
	return filepath.Clean(candidates[0])
}

func samePath(left, right string) bool {
	if filepath.VolumeName(left) != "" || filepath.VolumeName(right) != "" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func externalLabel(labels map[string]string, value, root string) string {
	logical := path.Clean(filepath.ToSlash(value))
	if label, ok := labels[logical]; ok {
		return label
	}
	if isAbsoluteFilePath(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(root, filepath.FromSlash(logical)))
}

func externalPreview(preview string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for logical := range labels {
		keys = append(keys, logical)
	}
	sort.Slice(keys, func(left, right int) bool { return len(keys[left]) > len(keys[right]) })
	for _, logical := range keys {
		preview = strings.ReplaceAll(preview, logical, labels[logical])
	}
	return preview
}

func translateExternalResult(result ToolResult, root string) ToolResult {
	for index := range result.Receipts {
		if result.Receipts[index].Path != "" {
			result.Receipts[index].Path = externalLabel(nil, result.Receipts[index].Path, root)
		}
	}
	var value any
	if json.Unmarshal([]byte(result.Content), &value) != nil {
		return result
	}
	translateJSONPaths(value, root, "")
	encoded, err := json.Marshal(value)
	if err == nil {
		result.Content = string(encoded)
	}
	return result
}

func translateJSONPaths(value any, root, key string) {
	switch current := value.(type) {
	case map[string]any:
		for name, item := range current {
			translateJSONPaths(item, root, name)
			if name == "path" {
				if text, ok := current[name].(string); ok {
					current[name] = externalLabel(nil, text, root)
				}
			}
		}
	case []any:
		for _, item := range current {
			translateJSONPaths(item, root, key)
		}
	}
}
