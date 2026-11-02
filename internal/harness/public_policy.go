package harness

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const ControlSetPublicWeb = "public_web.set_policy"

var ErrPublicLocalConfirmation = errors.New("harness: increasing the remote permission ceiling requires a local confirmation")
var ErrPublicSourceBlocked = errors.New("harness: this change would block the current remote source; add its IP to the allowlist first")

// PublicWebConfig describes one optional administrator entrance. It contains
// no password, cookie, proxy private key or local Owner capability.
type PublicWebConfig struct {
	Enabled       bool                 `json:"enabled"`
	Origin        string               `json:"origin"`
	Socket        string               `json:"socket"`
	SocketGroup   string               `json:"socket_group"`
	MaxPermission PermissionMode       `json:"max_permission"`
	MaxSessions   int                  `json:"max_sessions"`
	IdleMinutes   int                  `json:"idle_minutes"`
	AbsoluteHours int                  `json:"absolute_hours"`
	Security      PublicSecurityPolicy `json:"security"`
}

type PublicSecurityPolicy struct {
	FailureLock          bool     `json:"failure_lock"`
	FailureLimit         int      `json:"failure_limit"`
	FailureWindowMinutes int      `json:"failure_window_minutes"`
	LockMinutes          int      `json:"lock_minutes"`
	AutoBlacklist        bool     `json:"auto_blacklist"`
	BlacklistAfterLocks  int      `json:"blacklist_after_locks"`
	BlacklistMinutes     int      `json:"blacklist_minutes"`
	GlobalGuard          bool     `json:"global_guard"`
	GlobalFailureLimit   int      `json:"global_failure_limit"`
	GlobalWindowMinutes  int      `json:"global_window_minutes"`
	GlobalLockMinutes    int      `json:"global_lock_minutes"`
	AllowlistEnabled     bool     `json:"allowlist_enabled"`
	Allowlist            []string `json:"allowlist"`
	DenylistEnabled      bool     `json:"denylist_enabled"`
	Denylist             []string `json:"denylist"`
	CookieCheck          bool     `json:"cookie_check"`
	SessionBinding       bool     `json:"session_binding"`
}

func DefaultPublicWebConfig() PublicWebConfig {
	return PublicWebConfig{Socket: "/run/freeagent-public/web.sock", SocketGroup: "freeagent-public", MaxPermission: PermissionFull,
		MaxSessions: 8, IdleMinutes: 30, AbsoluteHours: 12, Security: PublicSecurityPolicy{
			FailureLock: true, FailureLimit: 5, FailureWindowMinutes: 15, LockMinutes: 30,
			BlacklistAfterLocks: 3, BlacklistMinutes: 1440,
			GlobalFailureLimit: 100, GlobalWindowMinutes: 15, GlobalLockMinutes: 15, CookieCheck: true,
			Allowlist: []string{}, Denylist: []string{},
		}}
}

func (c PublicWebConfig) Effective() PublicWebConfig {
	// An entirely absent optional setting has product defaults. Partially
	// configured values are validated, never silently repaired.
	if reflect.ValueOf(c).IsZero() {
		return DefaultPublicWebConfig()
	}
	return c
}

func (c PublicWebConfig) Validate() error {
	c = c.Effective()
	if c.MaxPermission.Validate() != nil || c.MaxSessions < 1 || c.MaxSessions > 8 || c.IdleMinutes < 1 || c.IdleMinutes > 120 || c.AbsoluteHours < 1 || c.AbsoluteHours > 24 || c.IdleMinutes > c.AbsoluteHours*60 {
		return errors.New("harness: invalid public Web permission or session limits")
	}
	if !filepath.IsAbs(c.Socket) && !strings.HasPrefix(c.Socket, "/") || !strings.HasPrefix(c.Socket, "/run/") || strings.ContainsAny(c.Socket, "\x00\r\n\\") || strings.Contains(c.Socket, "..") || len(c.Socket) > 100 || c.SocketGroup == "" || len(c.SocketGroup) > 64 || strings.ContainsAny(c.SocketGroup, " /\\\t\r\n:") {
		return errors.New("harness: public Web requires a bounded /run Unix socket and dedicated proxy group")
	}
	if c.Enabled || c.Origin != "" {
		u, err := url.Parse(c.Origin)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || strings.ToLower(u.Host) != u.Host || strings.ContainsAny(u.Host, "\x00\r\n ") {
			return errors.New("harness: public origin must be one lowercase HTTPS origin without a path")
		}
	}
	s := c.Security
	if s.FailureLimit < 1 || s.FailureLimit > 100 || s.FailureWindowMinutes < 1 || s.FailureWindowMinutes > 1440 || s.LockMinutes < 1 || s.LockMinutes > 1440 || s.BlacklistAfterLocks < 1 || s.BlacklistAfterLocks > 100 || s.BlacklistMinutes < 1 || s.BlacklistMinutes > 43200 || s.GlobalFailureLimit < 1 || s.GlobalFailureLimit > 10000 || s.GlobalWindowMinutes < 1 || s.GlobalWindowMinutes > 1440 || s.GlobalLockMinutes < 1 || s.GlobalLockMinutes > 1440 || len(s.Allowlist) > 128 || len(s.Denylist) > 128 || s.AllowlistEnabled && len(s.Allowlist) == 0 {
		return errors.New("harness: public security settings require finite thresholds and a populated enabled allowlist")
	}
	for _, list := range [][]string{s.Allowlist, s.Denylist} {
		for _, raw := range list {
			if _, err := parsePublicPrefix(raw); err != nil {
				return errors.New("harness: public IP list requires numeric addresses or CIDR prefixes")
			}
		}
	}
	return nil
}

func parsePublicPrefix(raw string) (netip.Prefix, error) {
	if ip, err := netip.ParseAddr(raw); err == nil {
		return netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen()), nil
	}
	p, err := netip.ParsePrefix(raw)
	if err != nil || p.Addr().Is4In6() {
		return netip.Prefix{}, errors.New("invalid prefix")
	}
	return p.Masked(), nil
}

func PublicIPMatches(raw string, list []string) bool {
	ip, err := netip.ParseAddr(raw)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, item := range list {
		p, err := parsePublicPrefix(item)
		if err == nil && p.Contains(ip) {
			return true
		}
	}
	return false
}

func (s PublicSecurityPolicy) AllowsIP(ip string) bool {
	if _, err := netip.ParseAddr(ip); err != nil {
		return false
	}
	return !(s.DenylistEnabled && PublicIPMatches(ip, s.Denylist)) && (!s.AllowlistEnabled || PublicIPMatches(ip, s.Allowlist))
}

type publicEntryKey struct{}
type PublicEntry struct{ Principal, IP string }

// WithPublicEntry is used only by the authenticated Unix-socket adapter. The
// value cannot be supplied through RunOptions or a model argument.
func WithPublicEntry(ctx context.Context, principal, ip string) context.Context {
	return context.WithValue(ctx, publicEntryKey{}, PublicEntry{principal, ip})
}
func PublicEntryFrom(ctx context.Context) (PublicEntry, bool) {
	entry, ok := ctx.Value(publicEntryKey{}).(PublicEntry)
	return entry, ok && entry.Principal != ""
}
func InteractivePrincipal(ctx context.Context, local string) string {
	if e, ok := PublicEntryFrom(ctx); ok {
		return e.Principal
	}
	return local
}

func (service *Service) currentPublicPolicy() PublicWebConfig {
	if service.liveConfig != nil {
		return service.liveConfig().PublicWeb.Effective()
	}
	return service.currentConfig().PublicWeb.Effective()
}

func publicPolicyPreview(before, after PublicWebConfig) string {
	fields := func(c PublicWebConfig) map[string]json.RawMessage {
		var values, security map[string]json.RawMessage
		_ = json.Unmarshal(mustJSON(c.Effective()), &values)
		_ = json.Unmarshal(values["security"], &security)
		delete(values, "security")
		for k, v := range security {
			values["security."+k] = v
		}
		return values
	}
	a, b := fields(before), fields(after)
	keys := make([]string, 0, len(b))
	for k := range b {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var text strings.Builder
	text.WriteString("Public Web configuration changes:\n")
	for _, k := range keys {
		if string(a[k]) != string(b[k]) {
			fmt.Fprintf(&text, "%s: %s -> %s\n", k, a[k], b[k])
		}
	}
	text.WriteString("Reduced permissions/access rules apply to subsequent dispatch immediately. Disable/logout closes readers and retains accepted tasks. Origin/socket changes and first enable require an explicit Owner restart. Raising the remote ceiling requires local confirmation. Credentials and TLS proxy permissions must be configured separately.")
	return text.String()
}

func validatePublicControl(ctx context.Context, before, after PublicWebConfig) error {
	entry, remote := PublicEntryFrom(ctx)
	if !remote {
		return nil
	}
	before, after = before.Effective(), after.Effective()
	if permissionRank(after.MaxPermission) > permissionRank(before.MaxPermission) {
		return ErrPublicLocalConfirmation
	}
	if after.Enabled && !after.Security.AllowsIP(entry.IP) {
		return ErrPublicSourceBlocked
	}
	return nil
}

func (service *ControlService) validatePublicOperation(ctx context.Context, record controlRecord) error {
	if entry, remote := PublicEntryFrom(ctx); remote && record.Operation.RemotePrincipal != "" && record.Operation.RemotePrincipal != entry.Principal {
		return ErrControlUserInteractionRequired
	}
	if raw, ok := record.After["config.public_web"]; ok {
		var after PublicWebConfig
		if json.Unmarshal(raw, &after) != nil {
			return ErrControlStaleRevision
		}
		return validatePublicControl(ctx, service.core.currentConfig().PublicWeb, after)
	}
	return nil
}

func (service *Service) publicConfirmationMAC(c Confirmation) string {
	mac := hmac.New(sha256.New, []byte(service.approvalBinding))
	mac.Write(mustJSON([]string{c.RunID, c.Attempt, c.CallID, c.Digest, c.RemotePrincipal}))
	return hex.EncodeToString(mac.Sum(nil))
}
func (service *Service) bindPublicConfirmation(ctx context.Context, c *Confirmation) {
	if e, ok := PublicEntryFrom(ctx); ok {
		c.RemotePrincipal = e.Principal
		c.RemoteMAC = service.publicConfirmationMAC(*c)
	}
}
func (service *Service) validatePublicConfirmation(ctx context.Context, c Confirmation) error {
	if e, ok := PublicEntryFrom(ctx); ok {
		if c.RemotePrincipal != e.Principal || service.approvalBinding == "" || !hmac.Equal([]byte(c.RemoteMAC), []byte(service.publicConfirmationMAC(c))) {
			return ErrApprovalChanged
		}
	}
	return nil
}

func (tasks *TaskService) stopPublicCommands(ctx context.Context) error {
	if tasks.core.commands == nil {
		return nil
	}
	commands, err := tasks.Commands(ctx)
	if err != nil {
		return err
	}
	policy := tasks.core.currentPublicPolicy()
	for _, command := range commands {
		if command.State != "running" && command.State != "stopping" {
			continue
		}
		run, err := tasks.core.Store.GetRun(ctx, command.RunID)
		if err != nil {
			return err
		}
		snapshot, err := decodeRunSnapshot(run.ConfigJSON)
		if err != nil {
			return err
		}
		if p := snapshot.Permission; p != nil && p.RemoteLimit != "" && permissionRank(policy.MaxPermission) < permissionRank(p.Mode) {
			if _, err := tasks.StopCommand(ctx, command.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
