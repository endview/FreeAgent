package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/winlaunch"
)

type commandArguments struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Cwd     string   `json:"cwd"`
	Check   bool     `json:"check,omitempty"`
}

var ErrInvalidExecutable = errors.New("harness: invalid command executable")
var ErrSandboxUnsupported = errors.New("harness: sandboxed command worker is unsupported on this host")
var ErrHostExecutionRequiresFull = errors.New("harness: host command execution requires Full access")

type CommandTools struct {
	Files     *FileTools
	Workspace string
	DataDir   string
	Config    CommandConfig
	Profile   ExecutionProfile
	Gate      *controlDecisionGate
}

func (tools *CommandTools) Definitions() []ToolDefinition {
	if !tools.Config.Enabled {
		return nil
	}
	description := "Run one explicitly approved local executable (absolute path), with exact arguments and relative working directory. Commands use the Run's execution profile; host execution is available only in Full access. Set check=true for an actual project check. Stopping cannot undo effects."
	if len(tools.Config.AllowedExecutables) > 0 {
		paths, _ := json.Marshal(tools.Config.AllowedExecutables)
		description += " Allowed exact executable paths: " + string(paths)
	}
	return []ToolDefinition{{Name: "command_run", Effect: EffectModify,
		Description: description,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"args":{"type":"array","maxItems":128,"items":{"type":"string"}},"cwd":{"type":"string"},"check":{"type":"boolean"}},"required":["command","cwd"],"additionalProperties":false}`)}}
}

func (tools *CommandTools) Prepare(ctx context.Context, call ToolCall) (PreparedTool, error) {
	if !tools.Config.Enabled || call.Name != "command_run" {
		return PreparedTool{}, errors.New("local commands are disabled")
	}
	if err := ctx.Err(); err != nil {
		return PreparedTool{}, err
	}
	if err := tools.validateExecutionBackend(); err != nil {
		return PreparedTool{}, err
	}
	var args commandArguments
	if err := decodeArguments(call.Arguments, &args); err != nil {
		return PreparedTool{}, err
	}
	requested := strings.TrimSpace(args.Command)
	command := requested
	var err error
	if !filepath.IsAbs(command) {
		command, err = exec.LookPath(command)
	}
	command, err = normalizeCommandPath(command)
	if err != nil || len(args.Args) > 128 {
		return PreparedTool{}, errors.New("command must name an executable path or PATH program with at most 128 arguments")
	}
	args.Command = command
	decision := commandRuleDecision(tools.Config.Rules, requested, command, args)
	if decision == "" && tools.Profile.Generation != "" && len(tools.Config.Rules) != 0 {
		return PreparedTool{}, errors.New("command denied by command rules")
	}
	if decision == CommandRuleDeny {
		return PreparedTool{}, errors.New("command denied by command rules")
	}
	for _, arg := range args.Args {
		if strings.ContainsRune(arg, 0) || len(arg) > 32768 {
			return PreparedTool{}, errors.New("invalid command argument")
		}
	}
	if !commandAllowed(args.Command, tools.Config.AllowedExecutables) {
		return PreparedTool{}, errors.New("command is not in the configured executable allowlist")
	}
	if err := verifyRegularFile(args.Command); err != nil {
		return PreparedTool{}, errors.New("command executable is not a safe regular file")
	}
	name, err := tools.Files.validatePath(args.Cwd)
	if err != nil {
		return PreparedTool{}, err
	}
	cwd := filepath.Join(tools.Workspace, filepath.FromSlash(name))
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return PreparedTool{}, errors.New("command working directory is not a workspace directory")
	}
	argv, _ := json.Marshal(append([]string{args.Command}, args.Args...))
	environment := "minimal filtered host environment"
	if tools.Config.InheritEnv {
		environment = "minimal filtered environment (full inheritance is disabled)"
	}
	receipt := ToolReceipt{Kind: "command", Command: string(argv), WorkingDir: cwd, Status: "not_launched", Check: args.Check}
	if tools.executionMode() == CommandExecutionHostExplicit {
		receipt.Backend = string(CommandExecutionHostExplicit)
		receipt.HostEscapeHatch = true
		receipt.NetworkMode = string(NetworkHostInherited)
		receipt.EnforcementState = "same_user_trusted"
	} else {
		receipt.Backend = "appcontainer"
		receipt.NetworkMode = string(tools.Profile.NetworkPolicy.Mode)
		receipt.EnforcementState = "os_enforced"
	}
	preview := fmt.Sprintf("Command: %s\nWorking directory: %s\nEnvironment: %s\nTimeout: %ds\nOutput: at most %d bytes\nExecution: %s. Commands may modify files and external systems. Cleanup does not undo effects.",
		argv, cwd, environment, tools.Config.TimeoutSecs, tools.Config.MaxOutput, tools.executionMode())
	if tools.executionMode() == CommandExecutionHostExplicit {
		preview += "\nNOT a sandbox. Host access is enabled only by explicit Full access."
	} else {
		preview += "\nSandboxed worker availability is checked before dispatch."
	}
	if len(tools.Config.AllowedExecutables) != 0 {
		preview += "\nExecutable policy: exact configured path; arguments still require approval"
	}
	execute := func(ctx context.Context, record func(ToolResult) error) (ToolResult, error) {
		return tools.execute(ctx, args, receipt, record)
	}
	autoInSandbox := tools.Profile.Generation != "" && tools.Profile.PermissionMode == PermissionWorkspace && tools.executionMode() == CommandExecutionSandboxed
	return PreparedTool{Effect: EffectModify, Preview: preview, workspaceFile: autoInSandbox, ruleAllowed: decision == CommandRuleAllow, commandDecision: decision, Items: []ApprovalItem{{
		Kind: "command", Target: args.Command,
		Fields: map[string]string{
			"command": string(argv), "working_dir": cwd, "environment": environment,
			"timeout_s":    strconv.Itoa(tools.Config.TimeoutSecs),
			"output_bytes": strconv.Itoa(tools.Config.MaxOutput),
		},
	}}, ExecuteRecorded: execute,
		Execute: func(ctx context.Context) (ToolResult, error) {
			return execute(ctx, func(ToolResult) error { return nil })
		},
		Reject: func() ToolResult {
			return ToolResult{Content: "user declined the command; do not repeat it", IsError: true, Receipts: []ToolReceipt{receipt}}
		}}, nil
}

func commandRuleDecision(rules []CommandRule, requested, resolved string, args commandArguments) CommandRuleDecision {
	decision := CommandRuleDecision("")
	for _, rule := range rules {
		if !commandRuleMatches(rule, requested, resolved, args) {
			continue
		}
		switch rule.Decision {
		case CommandRuleDeny:
			return CommandRuleDeny
		case CommandRuleAsk:
			decision = CommandRuleAsk
		case CommandRuleAllow:
			if decision == "" {
				decision = CommandRuleAllow
			}
		}
	}
	return decision
}

func commandRuleMatches(rule CommandRule, requested, resolved string, args commandArguments) bool {
	program := strings.TrimSpace(rule.Program)
	if filepath.IsAbs(program) {
		if !samePath(program, resolved) {
			return false
		}
	} else if !strings.EqualFold(program, filepath.Base(requested)) &&
		!strings.EqualFold(program, filepath.Base(resolved)) {
		return false
	}
	if rule.Cwd == "workspace" && args.Cwd != "" && args.Cwd != "." {
		return false
	}
	if rule.Cwd == "selected_roots" {
		return false
	}
	return len(rule.Args) == 0 || slices.Equal(rule.Args, args.Args)
}

func (tools *CommandTools) executionMode() CommandExecutionMode {
	if tools == nil {
		return CommandExecutionSandboxed
	}
	if tools.Config.Execution == "" && tools.Profile.Generation == "" {
		// Direct CommandTools fixtures from the pre-profile API are intentionally
		// treated as explicit host calls. Runtime views always carry a profile.
		return CommandExecutionHostExplicit
	}
	return effectiveCommandExecution(tools.Config)
}

func (tools *CommandTools) validateExecutionBackend() error {
	switch tools.executionMode() {
	case CommandExecutionHostExplicit:
		if tools.Profile.Generation != "" && tools.Profile.SandboxMode != SandboxHostFull {
			return ErrHostExecutionRequiresFull
		}
		return nil
	case CommandExecutionSandboxed:
		// Availability is checked after the attempt is created and before the
		// child is launched so the persisted receipt can say unsupported rather
		// than looking like a validation failure or a host fallback.
		return nil
	default:
		return errors.New("harness: invalid command execution backend")
	}
}

func (tools *CommandTools) execute(parent context.Context, args commandArguments, receipt ToolReceipt, record func(ToolResult) error) (ToolResult, error) {
	result := ToolResult{IsError: true, Receipts: []ToolReceipt{receipt}}
	if parent.Err() != nil {
		result.Content = "command not launched: cancelled"
		return result, nil
	}
	if !commandAllowed(args.Command, tools.Config.AllowedExecutables) {
		result.Content = "command not launched: executable policy changed"
		return result, nil
	}
	if _, err := tools.Files.validatePath(args.Cwd); err != nil {
		result.Content = "command not launched: working directory changed"
		return result, nil
	}
	before, beforeComplete := tools.observe(parent)
	ctx, cancel := context.WithTimeout(parent, time.Duration(tools.Config.TimeoutSecs)*time.Second)
	defer cancel()
	output := &boundedCapture{limit: tools.Config.MaxOutput}
	if ctx.Err() != nil {
		result.Content = "command not launched: cancelled before start"
		return result, nil
	}
	if tools.executionMode() == CommandExecutionHostExplicit && tools.Gate != nil {
		if err := tools.Gate.beginHost(); err != nil {
			result.Receipts[0].Status = "blocked"
			result.Receipts[0].Detail = "host_execution_busy"
			result.Content = "command not launched: host execution is busy"
			return result, nil
		}
		defer tools.Gate.endHost()
	}
	var missingGit []string
	if tools.executionMode() == CommandExecutionSandboxed && tools.sandboxAccess() == winlaunch.AccessWrite {
		for _, root := range tools.sandboxRoots() {
			if _, err := os.Lstat(filepath.Join(root, ".git")); errors.Is(err, os.ErrNotExist) {
				missingGit = append(missingGit, root)
			}
		}
	}
	process, err := tools.startProcess(ctx, args, receipt)
	if err != nil {
		if errors.Is(err, winlaunch.ErrUnsupported) || errors.Is(err, ErrSandboxUnsupported) {
			result.Receipts[0].Status = "unsupported"
			result.Receipts[0].Detail = err.Error()
			result.Content = "command not launched: " + err.Error()
			return result, nil
		}
		result.Receipts[0].Status = "failed_to_start"
		result.Content = "command failed to start: " + safeError(err)
		return result, nil
	}
	defer process.Close()
	go func() { _, _ = io.Copy(output, process.Stdout()) }()
	go func() { _, _ = io.Copy(output, process.Stderr()) }()
	_ = process.Stdin().Close()
	result.Receipts[0].Status = "unknown"
	if err := record(result); err != nil {
		_ = process.Kill()
		return result, &CallError{Message: "command dispatched but receipt could not be persisted"}
	}
	exit, waitErr := process.Wait(ctx)
	var cleanupErr error
	if waitErr == nil {
		result.Receipts[0].Status = "completed"
	} else if errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, context.Canceled) {
		result.Receipts[0].Status = "timed_out"
		if parent.Err() != nil {
			result.Receipts[0].Status = "interrupted"
		}
		process.Terminate()
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-func() chan struct{} {
			completed := make(chan struct{})
			go func() {
				_, _ = process.Wait(context.Background())
				close(completed)
			}()
			return completed
		}():
			timer.Stop()
		case <-timer.C:
			cleanupErr = process.Kill()
			reap := time.NewTimer(2 * time.Second)
			select {
			case <-func() chan struct{} {
				completed := make(chan struct{})
				go func() {
					_, _ = process.Wait(context.Background())
					close(completed)
				}()
				return completed
			}():
				reap.Stop()
			case <-reap.C:
				result.Receipts[0].Status = "unknown"
				cleanupErr = errors.Join(cleanupErr, errors.New("process wait did not complete"))
			}
		}
	} else {
		result.Receipts[0].Status = "unknown"
		cleanupErr = waitErr
	}
	if result.Receipts[0].Status != "unknown" {
		result.Receipts[0].ExitCode = &exit.Code
	}
	text, truncated := output.snapshot()
	result.Receipts[0].Truncated = truncated
	if cleanupErr != nil {
		result.Receipts[0].Detail += " cleanup not confirmed"
	}
	for _, root := range missingGit {
		if _, err := os.Lstat(filepath.Join(root, ".git")); err == nil && !result.Receipts[0].GitMetadataCreated {
			result.Receipts[0].GitMetadataCreated = true
			result.Receipts[0].Detail = strings.TrimSpace(result.Receipts[0].Detail + " created .git; host git will load its hooks and config")
		}
	}
	if err := record(result); err != nil {
		return result, &CallError{Message: "command finished but result receipt could not be persisted"}
	}
	observeCtx, done := context.WithTimeout(context.Background(), time.Second)
	after, afterComplete := tools.observe(observeCtx)
	done()
	names := make([]string, 0, len(before)+len(after))
	union := map[string]bool{}
	for name := range before {
		union[name] = true
	}
	for name := range after {
		union[name] = true
	}
	for name := range union {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if before[name] == after[name] {
			continue
		}
		result.Receipts = append(result.Receipts, ToolReceipt{Kind: "file", Path: name, Status: "observed_change",
			BeforeHash: before[name], AfterHash: after[name], Detail: "observed during command; not proof of sole attribution"})
	}
	if !beforeComplete || !afterComplete {
		result.Receipts = append(result.Receipts, ToolReceipt{Kind: "observation", Status: "limited",
			Detail: "file observation excludes protected, linked, binary, unreadable and oversized files; scan limit reached or files omitted"})
	}
	result.IsError = result.Receipts[0].Status != "completed" || result.Receipts[0].ExitCode == nil ||
		*result.Receipts[0].ExitCode != 0 || result.Receipts[0].Truncated
	body, _ := json.Marshal(map[string]any{"command": result.Receipts[0], "output": text, "observations": result.Receipts[1:]})
	result.Content = string(body)
	if result.Receipts[0].Status == "unknown" {
		_ = record(result)
		return result, &CallError{Message: "command completion unknown; inspect before repeating"}
	}
	return result, nil
}

func (tools *CommandTools) startProcess(ctx context.Context, args commandArguments, receipt ToolReceipt) (*winlaunch.Process, error) {
	spec := winlaunch.Spec{Command: args.Command, Args: args.Args, Dir: receipt.WorkingDir,
		Env: commandEnvironment(tools.Config.InheritEnv), Mode: winlaunch.ModeHost, Network: winlaunch.NetworkOff}
	if tools.executionMode() == CommandExecutionSandboxed {
		if err := tools.sandboxSpec(&spec); err != nil {
			return nil, err
		}
	}
	return winlaunch.Start(ctx, spec)
}

// sandboxSpec turns the Run profile into AppContainer grants. Root grants are
// kept for the owner's lifetime so a large workspace is not re-propagated for
// every command; protected paths are applied for each launch.
func (tools *CommandTools) sandboxSpec(spec *winlaunch.Spec) error {
	spec.Mode = winlaunch.ModeAppContainer
	switch tools.Profile.NetworkPolicy.Mode {
	case NetworkOff:
		spec.Network = winlaunch.NetworkOff
	case NetworkInternet:
		spec.Network = winlaunch.NetworkInternet
	default:
		return fmt.Errorf("%w: sandbox network mode %q", ErrSandboxUnsupported, tools.Profile.NetworkPolicy.Mode)
	}
	access := tools.sandboxAccess()
	keep := tools.DataDir != ""
	if keep {
		spec.AppContainerName = sandboxProfileName(tools.DataDir, access)
	}
	roots := tools.sandboxRoots()
	for _, root := range roots {
		spec.Grants = append(spec.Grants, winlaunch.Grant{Root: root, Access: access, Keep: keep})
	}
	if directory := filepath.Dir(spec.Command); !systemInstallPath(directory) && !grantRootPresent(spec.Grants, directory) {
		spec.Grants = append(spec.Grants, winlaunch.Grant{Root: directory, Access: winlaunch.AccessRead})
	}
	if tools.DataDir != "" {
		dataRoot, err := filepath.Abs(filepath.Clean(tools.DataDir))
		if err != nil {
			return fmt.Errorf("%w: data directory: %v", ErrSandboxUnsupported, err)
		}
		for _, grant := range spec.Grants {
			root, err := filepath.Abs(filepath.Clean(grant.Root))
			if err != nil || within(dataRoot, root) || within(root, dataRoot) {
				return fmt.Errorf("%w: grant overlaps the data directory", ErrSandboxUnsupported)
			}
		}
	}
	if access == winlaunch.AccessWrite {
		spec.ProtectedPaths = append(spec.ProtectedPaths, winlaunch.ProtectedPath{
			Path: filepath.Join(tools.Workspace, ".freeagent"), Mode: winlaunch.ProtectReadOnly, Create: true, Dir: true})
		for _, root := range roots {
			spec.ProtectedPaths = append(spec.ProtectedPaths, gitProtection(root, tools.Config.GitWrite)...)
		}
	}
	return nil
}

func (tools *CommandTools) sandboxAccess() winlaunch.AccessMode {
	if tools.Profile.SandboxMode == SandboxReadOnly {
		return winlaunch.AccessRead
	}
	return winlaunch.AccessWrite
}

// sandboxRoots lists the directories a sandboxed command may use: the
// workspace and, only when the Run's file scope includes them, the selected
// directories.
func (tools *CommandTools) sandboxRoots() []string {
	roots := []string{tools.Workspace}
	if canonicalFileScope(tools.Profile.FileScope) == FileScopeWorkspace {
		return roots
	}
	for _, root := range tools.Profile.AdditionalRoots {
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		if !slices.ContainsFunc(roots, func(existing string) bool { return samePath(existing, root) }) {
			roots = append(roots, root)
		}
	}
	return roots
}

// gitProtection keeps repository metadata readable so git status and log
// work. With git_write the repository is writable, but hooks and config,
// which later host git commands run or obey, stay read-only.
func gitProtection(root string, gitWrite bool) []winlaunch.ProtectedPath {
	gitDir := filepath.Join(root, ".git")
	info, err := os.Lstat(gitDir)
	if err != nil {
		return nil
	}
	if !gitWrite || !info.IsDir() {
		// A .git file points at metadata elsewhere, which is never granted.
		return []winlaunch.ProtectedPath{{Path: gitDir, Mode: winlaunch.ProtectReadOnly}}
	}
	return []winlaunch.ProtectedPath{
		{Path: gitDir, Mode: winlaunch.ProtectGitWrite},
		{Path: filepath.Join(gitDir, "hooks"), Mode: winlaunch.ProtectReadOnly, Create: true, Dir: true},
		{Path: filepath.Join(gitDir, "config"), Mode: winlaunch.ProtectReadOnly, Create: true},
		{Path: filepath.Join(gitDir, "config.worktree"), Mode: winlaunch.ProtectReadOnly, Create: true},
		{Path: filepath.Join(gitDir, "modules"), Mode: winlaunch.ProtectReadOnly},
		{Path: filepath.Join(gitDir, "worktrees"), Mode: winlaunch.ProtectReadOnly},
	}
}

// sandboxProfileName is stable for one data directory, so grants left by a
// crashed owner are reused and removed by the next clean shutdown.
func sandboxProfileName(dataDir string, access winlaunch.AccessMode) string {
	path, err := filepath.Abs(filepath.Clean(dataDir))
	if err != nil {
		path = filepath.Clean(dataDir)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(path)))
	suffix := "ro"
	if access == winlaunch.AccessWrite {
		suffix = "rw"
	}
	return "FreeAgent.Sandbox." + hex.EncodeToString(sum[:6]) + "." + suffix
}

// releaseSandboxGrants removes the root grants kept for this data directory.
func releaseSandboxGrants(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	return errors.Join(winlaunch.ReleaseKept(sandboxProfileName(dataDir, winlaunch.AccessRead)),
		winlaunch.ReleaseKept(sandboxProfileName(dataDir, winlaunch.AccessWrite)))
}

func grantRootPresent(grants []winlaunch.Grant, path string) bool {
	for _, grant := range grants {
		if samePath(grant.Root, path) {
			return true
		}
	}
	return false
}

func systemInstallPath(path string) bool {
	path, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, raw := range []string{os.Getenv("SystemRoot"), os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if raw == "" {
			continue
		}
		base, err := filepath.Abs(raw)
		if err != nil {
			continue
		}
		if samePath(path, base) || strings.HasPrefix(strings.ToLower(path), strings.ToLower(base)+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func validateCommandAllowlist(paths []string) error {
	_, err := normalizeCommandAllowlist(paths)
	return err
}

func normalizeCommandAllowlist(paths []string) ([]string, error) {
	if len(paths) > 32 {
		return nil, errors.New("harness: at most 32 allowed executables")
	}
	result := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		normalized, err := normalizeCommandPath(path)
		if err != nil || len(path) > 4096 {
			return nil, errors.New("harness: allowed executables must be unique, clean absolute paths")
		}
		key := normalized
		if filepath.VolumeName(normalized) != "" {
			key = strings.ToLower(normalized)
		}
		if seen[key] {
			return nil, errors.New("harness: allowed executables must be unique, clean absolute paths")
		}
		seen[key] = true
		result = append(result, normalized)
	}
	return result, nil
}

func normalizeCommandPath(raw string) (string, error) {
	if strings.ContainsAny(raw, "\x00\r\n") || !filepath.IsAbs(raw) {
		return "", errors.New("command path must be absolute")
	}
	normalized := filepath.Clean(raw)
	if normalized == "." || len(normalized) > 4096 {
		return "", errors.New("command path is invalid")
	}
	return normalized, nil
}

func commandAllowed(command string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, path := range allowed {
		if samePath(command, path) {
			return true
		}
	}
	return false
}

func commandEnvironment(inherit bool) []string {
	// A Windows AppContainer needs the system's complete environment block,
	// including hidden drive-current-directory entries and shell variables.
	// Filter likely credential-bearing names and all FreeAgent-private names
	// before passing it to the child. The inherit flag remains a product
	// setting, but full unfiltered inheritance is never allowed.
	values := make([]string, 0, len(os.Environ()))
	seen := map[string]bool{}
	for _, entry := range os.Environ() {
		index := strings.IndexByte(entry, '=')
		if index <= 0 {
			continue
		}
		if strings.HasPrefix(entry, "=") {
			values = append(values, entry)
			continue
		}
		name := strings.ToLower(entry[:index])
		if !sensitiveCommandEnvironmentName(name) && !seen[name] {
			values = append(values, entry)
			seen[name] = true
		}
	}
	return values
}

func sensitiveCommandEnvironmentName(name string) bool {
	name = strings.ToLower(name)
	for _, marker := range []string{"freeagent_", "api_key", "apikey", "token", "secret", "password", "credential", "private_key", "authorization"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

type boundedCapture struct {
	mu        sync.Mutex
	data      bytes.Buffer
	limit     int
	truncated bool
}

func (capture *boundedCapture) Write(raw []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	size := min(len(raw), max(0, capture.limit-capture.data.Len()))
	_, _ = capture.data.Write(raw[:size])
	capture.truncated = capture.truncated || size < len(raw)
	return len(raw), nil
}

func (capture *boundedCapture) snapshot() (string, bool) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return strings.ToValidUTF8(capture.data.String(), "\uFFFD"), capture.truncated
}

func (tools *CommandTools) observe(ctx context.Context) (map[string]string, bool) {
	result := map[string]string{}
	complete := true
	count, size := 0, 0
	err := fs.WalkDir(tools.Files.root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		count++
		if ctx.Err() != nil || count > 10000 || size > 8<<20 {
			complete = false
			return fs.SkipAll
		}
		if walkErr != nil {
			complete = false
			return nil
		}
		if _, err := tools.Files.validatePath(name); err != nil {
			if entry.IsDir() {
				return fs.SkipDir
			}
			complete = false
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		raw, _, err := tools.Files.read(name)
		if err != nil {
			complete = false
			return nil
		}
		size += len(raw)
		result[name] = contentHash(raw)
		return nil
	})
	return result, complete && err == nil
}
