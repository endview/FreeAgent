package harness

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/winlaunch"
)

type CommandCatalogEntry struct {
	Program   string `json:"program"`
	Path      string `json:"path,omitempty"`
	Available bool   `json:"available"`
	Risk      string `json:"risk"`
	Sandbox   string `json:"sandbox"`
	Decision  string `json:"decision,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// CommandCatalog is discovery only. It never launches a program and never
// grants authority; rules and the Run profile remain the execution boundary.
func (tasks *TaskService) CommandCatalog(ctx context.Context) ([]CommandCatalogEntry, error) {
	if ctx == nil || tasks == nil || tasks.core == nil {
		return nil, context.Canceled
	}
	config := tasks.Config()
	programs := map[string]bool{
		"git": true, "go": true, "node": true, "npm": true, "python": true,
		"python3": true, "pwsh": true, "powershell": true, "cmd": true,
	}
	for _, rule := range config.Commands.Rules {
		programs[strings.TrimSpace(rule.Program)] = true
	}
	for _, path := range config.Commands.AllowedExecutables {
		programs[filepath.Base(path)] = true
	}
	entries := make([]CommandCatalogEntry, 0, len(programs))
	for program := range programs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if program == "" {
			continue
		}
		entry := CommandCatalogEntry{Program: program, Risk: commandRisk(program), Decision: commandCatalogDecision(config.Commands.Rules, program)}
		path, err := exec.LookPath(program)
		if err != nil {
			entry.Sandbox = "unavailable"
			entry.Reason = "program was not found on PATH"
		} else {
			entry.Available, entry.Path = true, filepath.Clean(path)
			switch {
			case config.Commands.Execution == CommandExecutionHostExplicit:
				entry.Sandbox = "host_explicit"
			case winlaunch.SandboxSupported():
				entry.Sandbox = "sandboxed"
			default:
				entry.Sandbox = "unsupported"
				entry.Reason = "the command sandbox requires Windows"
			}
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Program) < strings.ToLower(entries[j].Program)
	})
	return entries, nil
}

func commandCatalogDecision(rules []CommandRule, program string) string {
	decision := commandRuleDecision(rules, program, program, commandArguments{})
	if decision == "" {
		return string(CommandRuleDeny)
	}
	return string(decision)
}

func commandRisk(program string) string {
	name := strings.ToLower(filepath.Base(program))
	for _, high := range []string{"powershell", "pwsh", "cmd", "bash", "sh", "python", "python3", "node", "java", "wscript", "cscript"} {
		if name == high || name == high+".exe" {
			return "high"
		}
	}
	return "standard"
}
