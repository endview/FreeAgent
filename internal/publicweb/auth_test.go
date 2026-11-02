package publicweb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

type testPolicy struct {
	sync.RWMutex
	config harness.PublicWebConfig
}

func (p *testPolicy) get() harness.PublicWebConfig { p.RLock(); defer p.RUnlock(); return p.config }
func (p *testPolicy) change(f func(*harness.PublicWebConfig)) {
	p.Lock()
	defer p.Unlock()
	f(&p.config)
}
func authFixture(t *testing.T) (*Auth, *testPolicy, []byte) {
	t.Helper()
	data := t.TempDir()
	password := []byte("fixture-only administrator password")
	if err := SetPassword(data, password); err != nil {
		t.Fatal(err)
	}
	p := &testPolicy{config: harness.DefaultPublicWebConfig()}
	p.config.Enabled = true
	p.config.Origin = "https://agent.example.test"
	a, err := NewAuth(t.Context(), data, p.get)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a, p, password
}
func loginFixture(t *testing.T, a *Auth, password []byte, ip string) (string, *Session) {
	t.Helper()
	detect, err := a.Challenge(ip)
	if err != nil {
		t.Fatal(err)
	}
	token, s, err := a.Login(t.Context(), ip, "fixture browser", detect, password)
	if err != nil {
		t.Fatal(err)
	}
	return token, s
}

func TestLoginCookieDetectionIsOneUseAndSeparateFromBinding(t *testing.T) {
	a, p, password := authFixture(t)
	if _, _, err := a.Login(t.Context(), "192.0.2.4", "agent", "", password); err == nil {
		t.Fatal("missing detection cookie entered password validation")
	}
	detect, err := a.Challenge("192.0.2.4")
	if err != nil {
		t.Fatal(err)
	}
	token, s, err := a.Login(t.Context(), "192.0.2.4", "agent", detect, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Login(t.Context(), "192.0.2.4", "agent", detect, password); err == nil {
		t.Fatal("detection cookie reused")
	}
	if a.Lookup(token, "192.0.2.5", "changed browser", false) != s {
		t.Fatal("login detection implicitly enabled session binding")
	}
	p.change(func(c *harness.PublicWebConfig) { c.Security.SessionBinding = true })
	if a.Lookup(token, "192.0.2.5", "changed browser", false) != nil {
		t.Fatal("binding change retained the session")
	}
	select {
	case <-s.Context().Done():
	default:
		t.Fatal("revocation did not cancel readers")
	}
	p.change(func(c *harness.PublicWebConfig) { c.Security.CookieCheck = false })
	if _, _, err := a.Login(t.Context(), "192.0.2.6", "browser", "", password); err != nil {
		t.Fatal("cookie detection switch ineffective", err)
	}
}

func TestSourceLockPreservesAuthenticatedSessionAndCanRecoverLocally(t *testing.T) {
	a, p, password := authFixture(t)
	token, s := loginFixture(t, a, password, "2001:db8::1")
	p.change(func(c *harness.PublicWebConfig) { c.Security.FailureLimit = 2; c.Security.AutoBlacklist = false })
	for range 2 {
		detect, _ := a.Challenge("2001:db8::abcd")
		_, _, err := a.Login(t.Context(), "2001:db8::abcd", "agent", detect, []byte("wrong fixture password"))
		if err == nil {
			t.Fatal("wrong password accepted")
		}
	}
	if a.Allowed("2001:db8::9", false) {
		t.Fatal("IPv6 /64 counter did not lock same source")
	}
	if a.Lookup(token, "2001:db8::1", "fixture browser", false) != s {
		t.Fatal("failure lock canceled an existing valid session")
	}
	a.ResetLocks()
	if !a.Allowed("2001:db8::9", false) {
		t.Fatal("local recovery ineffective")
	}
	p.change(func(c *harness.PublicWebConfig) {
		c.Security.DenylistEnabled = true
		c.Security.Denylist = []string{"2001:db8::/64"}
		c.Security.AllowlistEnabled = true
		c.Security.Allowlist = []string{"2001:db8::1"}
	})
	if a.Allowed("2001:db8::1", true) {
		t.Fatal("allowlist overrode denylist")
	}
	a.Refresh()
	select {
	case <-s.Context().Done():
	default:
		t.Fatal("IP policy did not close readers")
	}
}

func TestSessionLRUIdleAbsoluteRotationAndDisable(t *testing.T) {
	a, p, password := authFixture(t)
	p.change(func(c *harness.PublicWebConfig) { c.MaxSessions = 2 })
	one, s1 := loginFixture(t, a, password, "192.0.2.1")
	_, s2 := loginFixture(t, a, password, "192.0.2.2")
	_, _ = loginFixture(t, a, password, "192.0.2.3")
	if a.Lookup(one, "192.0.2.1", "fixture browser", false) != nil {
		t.Fatal("oldest session was not evicted")
	}
	select {
	case <-s1.Context().Done():
	default:
		t.Fatal("evicted session reader stayed open")
	}
	a.mu.Lock()
	s2.Active = time.Now().Add(-31 * time.Minute)
	a.mu.Unlock()
	a.Refresh()
	select {
	case <-s2.Context().Done():
	default:
		t.Fatal("idle session did not expire")
	}
	_, s3 := loginFixture(t, a, password, "192.0.2.4")
	a.mu.Lock()
	s3.Created = time.Now().Add(-13 * time.Hour)
	a.mu.Unlock()
	a.Refresh()
	select {
	case <-s3.Context().Done():
	default:
		t.Fatal("absolute expiry ineffective")
	}
	_, s4 := loginFixture(t, a, password, "192.0.2.5")
	if err := SetPassword(a.data, []byte("new fixture administrator password")); err != nil {
		t.Fatal(err)
	}
	a.Refresh()
	select {
	case <-s4.Context().Done():
	default:
		t.Fatal("password rotation retained session")
	}
	_, s5 := loginFixture(t, a, []byte("new fixture administrator password"), "192.0.2.6")
	p.change(func(c *harness.PublicWebConfig) { c.Enabled = false })
	a.Refresh()
	select {
	case <-s5.Context().Done():
	default:
		t.Fatal("disabled entrance retained session")
	}
}

func TestPasswordAndSourceStateHaveHardBoundsAndNoSecretLog(t *testing.T) {
	a, p, password := authFixture(t)
	p.change(func(c *harness.PublicWebConfig) { c.Security.CookieCheck = false })
	a.hashes <- struct{}{}
	a.hashes <- struct{}{}
	if _, _, err := a.Login(context.Background(), "192.0.2.8", "browser", "", password); err == nil {
		t.Fatal("unbounded hash concurrency")
	}
	<-a.hashes
	<-a.hashes
	for index := range 128 {
		if _, err := a.Challenge(fmt.Sprintf("192.0.2.%d", index+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Challenge("198.51.100.1"); err == nil {
		t.Fatal("unbounded login challenge state")
	}
	if _, _, err := a.Login(t.Context(), "192.0.2.11", "browser", "", password); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(a.Events())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), string(password)) || strings.Contains(string(raw), "csrf") {
		t.Fatal("credential leaked into security events")
	}
	admin, err := os.ReadFile(filepath.Join(a.data, credentialName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(admin), string(password)) {
		t.Fatal("plaintext administrator password stored")
	}
}

func TestGlobalGuardAndAutomaticBlacklistAreIndependentOptionalPolicies(t *testing.T) {
	a, p, password := authFixture(t)
	p.change(func(c *harness.PublicWebConfig) {
		c.Security.CookieCheck = false
		c.Security.FailureLock = false
		c.Security.GlobalGuard = true
		c.Security.GlobalFailureLimit = 2
		c.Security.Allowlist = []string{"192.0.2.100"}
	})
	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		_, _, _ = a.Login(t.Context(), ip, "browser", "", []byte("incorrect fixture password"))
	}
	if _, _, err := a.Login(t.Context(), "192.0.2.3", "browser", "", password); err == nil {
		t.Fatal("distributed failures did not pause password login")
	}
	if _, _, err := a.Login(t.Context(), "192.0.2.100", "browser", "", password); err != nil {
		t.Fatal("global guard blocked allowlist recovery", err)
	}
	a.ResetLocks()
	p.change(func(c *harness.PublicWebConfig) {
		c.Security.GlobalGuard = false
		c.Security.FailureLock = true
		c.Security.FailureLimit = 1
		c.Security.AutoBlacklist = true
		c.Security.BlacklistAfterLocks = 1
	})
	_, s, err := a.Login(t.Context(), "192.0.2.4", "browser", "", password)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = a.Login(t.Context(), "192.0.2.4", "browser", "", []byte("incorrect fixture password"))
	if a.Allowed("192.0.2.4", true) {
		t.Fatal("automatic blacklist was not enforced on authenticated access")
	}
	a.Refresh()
	select {
	case <-s.Context().Done():
	default:
		t.Fatal("blacklist did not revoke readers")
	}
}
