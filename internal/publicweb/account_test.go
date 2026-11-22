package publicweb

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/harness"
)

func TestAdministratorLegacyMigrationAndPasswordRoundTrip(t *testing.T) {
	data := t.TempDir()
	policy := harness.DefaultPublicWebConfig()
	policy.Enabled, policy.Origin = true, "https://fixture.example"
	policy.Security.CookieCheck = false
	password := []byte("first fixture password")
	legacy, err := makeCredential(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeProtected(filepath.Join(data, credentialName), legacy); err != nil {
		t.Fatal(err)
	}
	if got := InspectAccount(data).State; got != "legacy" {
		t.Fatal(got)
	}
	a, err := NewAuth(t.Context(), data, func() harness.PublicWebConfig { return policy })
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, before, err := a.Login(t.Context(), "192.0.2.4", "fixture", "", DefaultUsername, password)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Rename(before, "192.0.2.4", "instance-admin"); err != nil {
		t.Fatal(err)
	}
	_, renamed, err := a.Login(t.Context(), "192.0.2.4", "fixture", "", "instance-admin", password)
	if err != nil || renamed.AccountID != before.AccountID {
		t.Fatal("rename changed account or rejected valid login", err)
	}
	next := []byte("second fixture password")
	if err := a.ChangePassword(renamed, "192.0.2.4", password, next); err != nil {
		t.Fatal(err)
	}
	// Legacy writers use the unchanged v1 credential layout and do not
	// preserve any extra fields. Metadata survives their full replacement.
	v1, err := loadCredential(data)
	if err != nil || v1.verify(password) || !v1.verify(next) {
		t.Fatal("old credential format did not accept only the rotated password", err)
	}
	third := []byte("third fixture password")
	legacy, err = makeCredential(third)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	if err := writeProtected(filepath.Join(data, credentialName), legacy); err != nil {
		t.Fatal(err)
	}
	if got := InspectAccount(data).State; got != "password_changed" {
		t.Fatal(got)
	}
	b, err := NewAuth(t.Context(), data, func() harness.PublicWebConfig { return policy })
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, p := range [][]byte{password, next} {
		if _, _, err := b.Login(t.Context(), "192.0.2.4", "fixture", "", "instance-admin", p); err == nil {
			t.Fatal("old password revived")
		}
	}
	_, current, err := b.Login(t.Context(), "192.0.2.4", "fixture", "", "instance-admin", third)
	if err != nil || current.AccountID != before.AccountID {
		t.Fatal("upgrade lost account identity or rotated password", err)
	}
	if _, err := a.BeginDecision(renamed, "192.0.2.4", true); err == nil {
		t.Fatal("a revoked session can still make decisions")
	}
}

func TestAdministratorInitIsOneTimeAndDamageRequiresRecovery(t *testing.T) {
	data := t.TempDir()
	if got := InspectAccount(data).State; got != "uninitialized" {
		t.Fatal(got)
	}
	password := []byte("fixture initialization password")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := InitAccount(data, "fixture", password); results <- err })
	}
	wg.Wait()
	close(results)
	saved := 0
	for err := range results {
		if err == nil {
			saved++
		} else if !errors.Is(err, ErrAlreadyInitialized) {
			t.Fatal(err)
		}
	}
	if saved != 1 {
		t.Fatal("initialization was not exclusive", saved)
	}
	initial := InspectAccount(data)
	if err := os.Remove(filepath.Join(data, credentialName)); err != nil {
		t.Fatal(err)
	}
	if got := InspectAccount(data).State; got != "recovery_required" {
		t.Fatal(got)
	}
	if _, err := InitAccount(data, "next", password); err == nil {
		t.Fatal("damaged instance accepted new initialization")
	}
	if err := ResetPassword(data, []byte("fixture recovery password")); err != nil {
		t.Fatal(err)
	}
	if status := InspectAccount(data); status.State != "ready" || *status.Account != *initial.Account {
		t.Fatal("recovery changed account", status)
	}
	if err := os.Remove(accountPath(data)); err != nil {
		t.Fatal(err)
	}
	if got := InspectAccount(data).State; got != "recovery_required" {
		t.Fatal("missing sidecar treated as a legacy account", got)
	}
	if err := ResetPassword(data, password); !errors.Is(err, ErrAccountRecovery) {
		t.Fatal("password reset replaced damaged account metadata", err)
	}
}

func TestAdministratorReauthenticationAndRevocationAreSessionSpecific(t *testing.T) {
	a, _, password := authFixture(t)
	_, first := loginFixture(t, a, password, "192.0.2.1")
	_, second := loginFixture(t, a, password, "192.0.2.2")
	a.mu.Lock()
	first.verifiedAt = time.Now().Add(-VerificationWindow)
	second.verifiedAt = first.verifiedAt
	a.mu.Unlock()
	if _, err := a.BeginDecision(first, "192.0.2.1", true); !errors.Is(err, harness.ErrPublicReauthentication) {
		t.Fatal("expired verification accepted", err)
	}
	if err := a.Reauthenticate(first, "192.0.2.1", password); err != nil {
		t.Fatal(err)
	}
	release, err := a.BeginDecision(first, "192.0.2.1", true)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := a.BeginDecision(second, "192.0.2.2", true); !errors.Is(err, harness.ErrPublicReauthentication) {
		t.Fatal("verification applied to another browser session", err)
	}
	if err := a.RevokeSession(first, "192.0.2.1", second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.BeginDecision(second, "192.0.2.2", false); !errors.Is(err, harness.ErrControlUserInteractionRequired) {
		t.Fatal("revoked session accepted", err)
	}
}
