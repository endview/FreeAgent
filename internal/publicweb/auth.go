package publicweb

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

const SessionCookie = "__Host-FreeAgent"
const DetectionCookie = "__Host-FreeAgent-Login"

type Event = harness.PublicSecurityEvent
type EventStore interface {
	ReadPublicSecurityEvents(context.Context) ([]harness.PublicSecurityEvent, error)
	AppendPublicSecurityEvent(context.Context, harness.PublicSecurityEvent) error
}
type sourceFailures struct {
	Since               time.Time
	Count, Locks        int
	Locked, Blacklisted time.Time
	Seen                time.Time
}
type challenge struct {
	IP    string
	Until time.Time
}
type Session struct {
	ID              string
	CSRF            string
	Created, Active time.Time
	Source, Agent   string
	ctx             context.Context
	cancel          context.CancelFunc
	reads           chan struct{}
	sources         map[string]bool
}

func (s *Session) Context() context.Context { return s.ctx }
func (s *Session) AcquireRead() bool {
	select {
	case s.reads <- struct{}{}:
		return true
	default:
		return false
	}
}
func (s *Session) ReleaseRead() { <-s.reads }

// Auth retains only bounded, ephemeral administrator sessions. Cookie values
// are hashed before indexing; only the non-secret session ID reaches controls.
type Auth struct {
	mu            sync.Mutex
	data          string
	policy        func() harness.PublicWebConfig
	credential    credential
	sessions      map[string]*Session
	failures      map[string]*sourceFailures
	challenges    map[string]challenge
	global        sourceFailures
	events        []Event
	passwordMu    sync.Mutex
	eventLogError bool
	eventStore    EventStore
	hashes        chan struct{}
	ctx           context.Context
	cancel        context.CancelFunc
	done          chan struct{}
	now           func() time.Time
	origin        string
	wasEnabled    bool
}

func NewAuth(ctx context.Context, data string, policy func() harness.PublicWebConfig, stores ...EventStore) (*Auth, error) {
	c, err := loadCredential(data)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	a := &Auth{data: data, policy: policy, credential: c, sessions: map[string]*Session{}, failures: map[string]*sourceFailures{}, challenges: map[string]challenge{}, hashes: make(chan struct{}, 2), ctx: ctx, cancel: cancel, done: make(chan struct{}), now: time.Now}
	p := policy().Effective()
	if len(stores) > 0 {
		a.eventStore = stores[0]
	}
	a.origin = p.Origin
	a.wasEnabled = p.Enabled
	go func() {
		defer close(a.done)
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				a.revokeAll()
				return
			case <-tick.C:
				a.Refresh()
			}
		}
	}()
	return a, nil
}
func (a *Auth) Close()                { a.cancel(); <-a.done }
func (a *Auth) Done() <-chan struct{} { return a.ctx.Done() }
func (a *Auth) eventLocked(source, reason string) {
	a.events = append(a.events, Event{At: a.now().UTC().Format(time.RFC3339Nano), Source: source, Reason: reason})
	if len(a.events) > 256 {
		a.events = a.events[len(a.events)-256:]
	}
	if a.eventStore != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), 2*time.Second)
		a.eventLogError = a.eventStore.AppendPublicSecurityEvent(ctx, a.events[len(a.events)-1]) != nil
		cancel()
	}
}
func (a *Auth) Events() []Event {
	if a.eventStore != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), 2*time.Second)
		defer cancel()
		if events, err := a.eventStore.ReadPublicSecurityEvents(ctx); err == nil {
			return events
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Event(nil), a.events...)
}
func sourceKey(raw string) string {
	ip, err := netip.ParseAddr(raw)
	if err != nil {
		return ""
	}
	ip = ip.Unmap()
	if ip.Is6() {
		return netip.PrefixFrom(ip, 64).Masked().String()
	}
	return ip.String()
}
func (a *Auth) revokeAllLocked() {
	for k, s := range a.sessions {
		s.cancel()
		delete(a.sessions, k)
	}
}
func (a *Auth) revokeAll() { a.mu.Lock(); defer a.mu.Unlock(); a.revokeAllLocked() }

func (a *Auth) Refresh() {
	p := a.policy().Effective()
	c, err := loadCredential(a.data)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil || c.Generation != a.credential.Generation || !p.Enabled || p.Origin != a.origin {
		if len(a.sessions) > 0 {
			a.eventLocked("local", "sessions_revoked")
		}
		a.revokeAllLocked()
	}
	if err == nil {
		a.credential = c
	}
	a.origin = p.Origin
	a.wasEnabled = p.Enabled
	if !p.Enabled {
		a.cancel()
	}
	now := a.now()
	for k, s := range a.sessions {
		denied := false
		for ip := range s.sources {
			f := a.failures[sourceKey(ip)]
			if !p.Security.AllowsIP(ip) || p.Security.AutoBlacklist && f != nil && now.Before(f.Blacklisted) {
				denied = true
				break
			}
		}
		if now.Sub(s.Active) >= time.Duration(p.IdleMinutes)*time.Minute || now.Sub(s.Created) >= time.Duration(p.AbsoluteHours)*time.Hour || denied {
			s.cancel()
			delete(a.sessions, k)
			a.eventLocked(sourceKey(s.Source), "session_expired_or_policy_revoked")
		}
	}
	for k, c := range a.challenges {
		if !now.Before(c.Until) {
			delete(a.challenges, k)
		}
	}
	for k, f := range a.failures {
		if now.Sub(f.Seen) > 24*time.Hour && !now.Before(f.Locked) && !now.Before(f.Blacklisted) {
			delete(a.failures, k)
		}
	}
	for len(a.sessions) > p.MaxSessions {
		a.evictLocked()
	}
}

func (a *Auth) Allowed(ip string, authenticated bool) bool {
	p := a.policy().Effective()
	if !p.Enabled || !p.Security.AllowsIP(ip) {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.failures[sourceKey(ip)]
	now := a.now()
	if f != nil && (p.Security.AutoBlacklist && now.Before(f.Blacklisted) || !authenticated && p.Security.FailureLock && now.Before(f.Locked)) {
		return false
	}
	return true
}
func (a *Auth) Challenge(ip string) (string, error) {
	if !a.Allowed(ip, false) {
		return "", errors.New("login denied")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for k, c := range a.challenges {
		if !now.Before(c.Until) {
			delete(a.challenges, k)
		}
	}
	// One source cannot occupy the entire login-page nonce budget.
	count, oldest, until := 0, "", time.Time{}
	for key, c := range a.challenges {
		if c.IP == sourceKey(ip) {
			count++
			if oldest == "" || c.Until.Before(until) {
				oldest, until = key, c.Until
			}
		}
	}
	if count >= 4 {
		delete(a.challenges, oldest)
	}
	if len(a.challenges) >= 128 {
		return "", errors.New("login busy")
	}
	token := randomToken()
	a.challenges[tokenHash(token)] = challenge{sourceKey(ip), now.Add(2 * time.Minute)}
	return token, nil
}
func (a *Auth) consumeChallengeLocked(token, ip string) bool {
	key := tokenHash(token)
	c, ok := a.challenges[key]
	delete(a.challenges, key)
	return ok && a.now().Before(c.Until) && c.IP == sourceKey(ip)
}

func (a *Auth) failureLocked(ip string, p harness.PublicSecurityPolicy) {
	now := a.now()
	key := sourceKey(ip)
	f := a.failures[key]
	if f == nil {
		if len(a.failures) >= 512 {
			return
		}
		f = &sourceFailures{}
		a.failures[key] = f
	}
	f.Seen = now
	if now.Sub(f.Since) >= time.Duration(p.FailureWindowMinutes)*time.Minute {
		f.Since = now
		f.Count = 0
	}
	f.Count++
	a.eventLocked(key, "login_failed")
	if p.FailureLock && f.Count >= p.FailureLimit && !now.Before(f.Locked) {
		f.Locked = now.Add(time.Duration(p.LockMinutes) * time.Minute)
		f.Locks++
		f.Count = 0
		a.eventLocked(key, "source_locked")
		if p.AutoBlacklist && f.Locks >= p.BlacklistAfterLocks {
			f.Blacklisted = now.Add(time.Duration(p.BlacklistMinutes) * time.Minute)
			a.eventLocked(key, "source_blacklisted")
		}
	}
	if now.Sub(a.global.Since) >= time.Duration(p.GlobalWindowMinutes)*time.Minute {
		a.global.Since = now
		a.global.Count = 0
	}
	a.global.Count++
	if p.GlobalGuard && a.global.Count >= p.GlobalFailureLimit {
		a.global.Locked = now.Add(time.Duration(p.GlobalLockMinutes) * time.Minute)
		a.global.Count = 0
		a.eventLocked("global", "global_login_paused")
	}
}

func (a *Auth) Login(ctx context.Context, ip, agent, detection string, password []byte) (string, *Session, error) {
	p := a.policy().Effective()
	if !a.Allowed(ip, false) {
		return "", nil, errors.New("login denied")
	}
	a.mu.Lock()
	if len(a.failures) >= 512 && a.failures[sourceKey(ip)] == nil || p.Security.GlobalGuard && a.now().Before(a.global.Locked) && !harness.PublicIPMatches(ip, p.Security.Allowlist) || p.Security.CookieCheck && !a.consumeChallengeLocked(detection, ip) {
		a.mu.Unlock()
		return "", nil, errors.New("login denied")
	}
	c := a.credential
	a.mu.Unlock()
	select {
	case a.hashes <- struct{}{}:
		defer func() { <-a.hashes }()
	default:
		return "", nil, errors.New("login busy")
	}
	valid := c.verify(password)
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !valid {
		a.failureLocked(ip, p.Security)
		return "", nil, errors.New("login denied")
	}
	// Re-read live policy after the expensive hash. A reset or disable cannot
	// issue a session from a stale credential generation.
	next := a.policy().Effective()
	if !next.Enabled || !next.Security.AllowsIP(ip) || c.Generation != a.credential.Generation {
		return "", nil, errors.New("login denied")
	}
	if next.Security.GlobalGuard && a.now().Before(a.global.Locked) && !harness.PublicIPMatches(ip, next.Security.Allowlist) {
		return "", nil, errors.New("login denied")
	}
	if f := a.failures[sourceKey(ip)]; f != nil && (next.Security.FailureLock && a.now().Before(f.Locked) || next.Security.AutoBlacklist && a.now().Before(f.Blacklisted)) {
		return "", nil, errors.New("login denied")
	}
	for len(a.sessions) >= next.MaxSessions {
		a.evictLocked()
	}
	token := randomToken()
	lifetime, cancel := context.WithCancel(a.ctx)
	now := a.now()
	s := &Session{ID: randomToken()[:32], CSRF: randomToken(), Created: now, Active: now, Source: ip, Agent: tokenHash(agent), ctx: lifetime, cancel: cancel, reads: make(chan struct{}, 4), sources: map[string]bool{ip: true}}
	a.sessions[tokenHash(token)] = s
	a.eventLocked(sourceKey(ip), "login_succeeded")
	return token, s, nil
}
func (a *Auth) evictLocked() {
	var oldest string
	var at time.Time
	for key, s := range a.sessions {
		if oldest == "" || s.Active.Before(at) {
			oldest, at = key, s.Active
		}
	}
	if oldest != "" {
		a.sessions[oldest].cancel()
		delete(a.sessions, oldest)
		a.eventLocked("local", "session_lru_evicted")
	}
}
func (a *Auth) Lookup(token, ip, agent string, touch bool) *Session {
	if len(token) != 64 {
		return nil
	}
	p := a.policy().Effective()
	if !a.Allowed(ip, true) {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := tokenHash(token)
	s := a.sessions[key]
	if s == nil {
		return nil
	}
	now := a.now()
	if now.Sub(s.Active) >= time.Duration(p.IdleMinutes)*time.Minute || now.Sub(s.Created) >= time.Duration(p.AbsoluteHours)*time.Hour || p.Security.SessionBinding && (sourceKey(s.Source) != sourceKey(ip) || s.Agent != tokenHash(agent)) {
		s.cancel()
		delete(a.sessions, key)
		a.eventLocked(sourceKey(ip), "session_expired_or_binding_changed")
		return nil
	}
	if touch {
		s.Active = now
	}
	if !s.sources[ip] && len(s.sources) >= 16 {
		s.cancel()
		delete(a.sessions, key)
		a.eventLocked(sourceKey(ip), "session_source_capacity")
		return nil
	}
	s.sources[ip] = true
	return s
}
func (a *Auth) Logout(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := tokenHash(token)
	if s := a.sessions[key]; s != nil {
		s.cancel()
		delete(a.sessions, key)
		a.eventLocked(sourceKey(s.Source), "logout")
	}
}
func (a *Auth) ResetLocks() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures = map[string]*sourceFailures{}
	a.global = sourceFailures{}
	a.eventLocked("local", "locks_reset")
}
func (a *Auth) State() map[string]any {
	events := a.Events()
	a.mu.Lock()
	defer a.mu.Unlock()
	sources := make([]string, 0)
	now := a.now()
	for key, f := range a.failures {
		if now.Before(f.Locked) || now.Before(f.Blacklisted) {
			sources = append(sources, key)
		}
	}
	sort.Strings(sources)
	return map[string]any{"sessions": len(a.sessions), "locked_sources": sources, "global_paused_until": a.global.Locked, "events": events, "event_log_error": a.eventLogError}
}

func (a *Auth) VerifyCurrent(password []byte) bool {
	select {
	case a.hashes <- struct{}{}:
		defer func() { <-a.hashes }()
	default:
		return false
	}
	a.mu.Lock()
	c := a.credential
	a.mu.Unlock()
	return c.verify(password)
}

func (a *Auth) ChangePassword(current, next []byte) error {
	select {
	case a.hashes <- struct{}{}:
		defer func() { <-a.hashes }()
	default:
		return errors.New("password operation busy")
	}
	a.passwordMu.Lock()
	defer a.passwordMu.Unlock()
	a.mu.Lock()
	c := a.credential
	a.mu.Unlock()
	if !c.verify(current) {
		return errors.New("password verification failed")
	}
	if err := SetPassword(a.data, next); err != nil {
		return err
	}
	return nil
}
