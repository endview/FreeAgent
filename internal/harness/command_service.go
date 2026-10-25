package harness

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const serviceHardLimit = 28800

type ServicePolicy struct {
	Enabled          bool `json:"enabled"`
	DefaultSeconds   int  `json:"default_seconds"`
	MaxTotalSeconds  int  `json:"max_total_seconds"`
	AllowLocalListen bool `json:"allow_local_listen"`
}

func DefaultServicePolicy() ServicePolicy {
	return ServicePolicy{Enabled: true, DefaultSeconds: 1800, MaxTotalSeconds: serviceHardLimit}
}
func (p ServicePolicy) Validate() error {
	if p.DefaultSeconds < 0 || p.MaxTotalSeconds < 0 || p.MaxTotalSeconds > serviceHardLimit {
		return errors.New("service policy cannot exceed the 28800 second product hard limit")
	}
	if p.Enabled && (p.DefaultSeconds < 1 || p.DefaultSeconds > p.MaxTotalSeconds || p.MaxTotalSeconds > serviceHardLimit) {
		return errors.New("service duration must be finite: 1 <= default <= maximum <= 28800 seconds")
	}
	return nil
}

func (tools *CommandTools) serviceArguments(args *commandArguments) error {
	if args.Mode != "" && args.Mode != "background" && args.Mode != "service" {
		return errors.New("invalid command mode")
	}
	if args.Mode == "background" {
		args.Background = true
	}
	if args.Mode != "service" {
		if args.DurationSeconds != 0 || args.ReadyText != "" || args.ReadyTimeout != 0 {
			return errors.New("duration/readiness require service mode")
		}
		return nil
	}
	p := tools.Config.Services
	if !p.Enabled {
		return errors.New("service mode is disabled")
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if !p.AllowLocalListen {
		return errors.New("service mode requires explicit commands.services.allow_local_listen; Full does not enforce a loopback-only listener")
	}
	if tools.executionMode() != CommandExecutionHostExplicit {
		return fmt.Errorf("NOT_SUPPORTED: sandbox service host loopback is unavailable; explicitly select Full/host to run a service: %w", ErrSandboxUnsupported)
	}
	if args.DurationSeconds == 0 {
		args.DurationSeconds = p.DefaultSeconds
	}
	if args.DurationSeconds < 1 || args.DurationSeconds > p.MaxTotalSeconds {
		return errors.New("service duration exceeds finite total limit")
	}
	if len(args.ReadyText) > 1024 || !utf8.ValidString(args.ReadyText) || strings.ContainsRune(args.ReadyText, 0) {
		return errors.New("ready_text must be a UTF-8 literal of at most 1024 bytes")
	}
	if args.ReadyText != "" {
		if args.ReadyTimeout == 0 {
			args.ReadyTimeout = min(60, args.DurationSeconds)
		}
		if args.ReadyTimeout < 1 || args.ReadyTimeout > 300 || args.ReadyTimeout > args.DurationSeconds {
			return errors.New("readiness timeout must be 1..300 and within service duration")
		}
	} else if args.ReadyTimeout != 0 {
		return errors.New("ready timeout requires literal ready text")
	}
	args.Background = true
	return nil
}

// Resolve the complete finite installation set before granting a concurrent
// group any access. A subsequent request must match this same set exactly.
func (tools *CommandTools) executionIdentity(args *commandArguments) (string, error) {
	paths := append([]string(nil), tools.Config.AllowedExecutables...)
	for _, rule := range tools.Config.Rules {
		if rule.Decision != CommandRuleDeny {
			paths = append(paths, rule.Program)
		}
	}
	paths = append(paths, args.Command)
	var roots []string
	for _, program := range paths {
		if !filepath.IsAbs(program) {
			var err error
			program, err = exec.LookPath(program)
			if err != nil {
				continue
			}
		}
		program, err := normalizeCommandPath(program)
		if err != nil {
			continue
		}
		target, err := resolveCommandTarget(program, nil)
		if err != nil {
			continue
		}
		root := target.root
		if root == "" {
			root = filepath.Dir(target.program)
		}
		if systemInstallPath(root) {
			continue
		}
		if !slices.ContainsFunc(roots, func(r string) bool { return samePath(r, root) }) {
			roots = append(roots, root)
		}
	}
	if args.installationRoot != "" && !slices.ContainsFunc(roots, func(r string) bool { return samePath(r, args.installationRoot) }) {
		roots = append(roots, args.installationRoot)
	}
	sort.Strings(roots)
	args.executionRoots = roots
	profile := tools.Profile
	profile.Generation = ""
	return contentHash(mustJSON(struct {
		Profile  ExecutionProfile
		GitWrite bool
		Roots    []string
	}{profile, tools.Config.GitWrite, roots})), nil
}

type CommandExtension struct {
	ID         string `json:"id"`
	Revision   int64  `json:"revision"`
	ExpiresAt  string `json:"expires_at"`
	RequestKey string `json:"request_key"`
}

func (s *commandSupervisor) extend(ctx context.Context, request CommandExtension) (CommandRecord, error) {
	if request.ID == "" || request.Revision < 1 || request.RequestKey == "" || len(request.RequestKey) > 128 {
		return CommandRecord{}, errors.New("extension requires id, revision, target expiry and bounded request key")
	}
	target, err := time.Parse(time.RFC3339Nano, request.ExpiresAt)
	if err != nil {
		return CommandRecord{}, err
	}
	s.mu.Lock()
	p := s.active[request.ID]
	s.mu.Unlock()
	if p == nil {
		r, err := s.poll(ctx, request.ID, 0, 16384)
		if err != nil {
			return CommandRecord{}, err
		}
		for _, saved := range r.Extensions {
			if saved.RequestKey == request.RequestKey {
				if saved != request {
					return CommandRecord{}, errors.New("extension key content mismatch")
				}
				return r, nil
			}
		}
		return CommandRecord{}, errors.New("service is not owned by this owner")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, saved := range p.record.Extensions {
		if saved.RequestKey == request.RequestKey {
			if saved != request {
				return CommandRecord{}, errors.New("extension request key reused with different content")
			}
			return p.record, nil
		}
	}
	now := time.Now()
	if p.record.Mode != "service" || p.record.State != "running" || !now.Before(p.deadline) {
		return CommandRecord{}, errors.New("service expiry or stopping already began")
	}
	if request.Revision != p.record.Revision {
		return CommandRecord{}, errors.New("stale command revision; read and preview again")
	}
	if len(p.record.Extensions) >= 64 {
		return CommandRecord{}, errors.New("finite extension receipt limit reached (64); existing deadline unchanged")
	}
	old, _ := time.Parse(time.RFC3339Nano, p.record.ExpiresAt)
	maxWall, _ := time.Parse(time.RFC3339Nano, p.record.MaxExpiresAt)
	deadline, err := finiteExtensionDeadline(now, p.deadline, old, maxWall, target)
	if err != nil {
		return CommandRecord{}, err
	}
	next := p.record
	next.Revision++
	next.ExpiresAt = target.UTC().Format(time.RFC3339Nano)
	next.Extension = &request
	next.Extensions = append(append([]CommandExtension(nil), next.Extensions...), request)
	next.ObservedAt = nowText()
	if err := s.save(ctx, next); err != nil {
		return CommandRecord{}, err
	}
	p.record = next
	p.deadline = deadline
	p.expiry.Reset(max(time.Nanosecond, time.Until(p.deadline)))
	s.event(next)
	return next, nil
}

// Wall timestamps identify the requested change; only their delta changes the
// existing monotonic deadline. Moving the wall clock cannot renew the budget.
func finiteExtensionDeadline(now, deadline, old, maxWall, target time.Time) (time.Time, error) {
	if !now.Before(deadline) {
		return time.Time{}, errors.New("service deadline already reached")
	}
	if !target.After(old) || target.After(maxWall) {
		return time.Time{}, errors.New("extension must increase expiry within original total limit")
	}
	return deadline.Add(target.Sub(old)), nil
}

func (tasks *TaskService) ExtendCommand(ctx context.Context, request CommandExtension) (CommandRecord, error) {
	if tasks.core.commands == nil {
		return CommandRecord{}, errors.New("owner has no command supervisor")
	}
	return tasks.core.commands.extend(ctx, request)
}
