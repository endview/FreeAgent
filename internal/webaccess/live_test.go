package webaccess

import (
	"os"
	"testing"
)

// Opt-in only: validates the real public DNS/TLS/fetch path without a search
// account. Never sends a model key, cookie, or private conversation.
func TestPublicFetchLive(t *testing.T) {
	u := os.Getenv("FREEAGENT_WEB_LIVE_URL")
	if u == "" {
		t.Skip("set FREEAGENT_WEB_LIVE_URL to an explicitly selected public page")
	}
	page, err := Fetch(t.Context(), u, DefaultConfig())
	if err != nil || page.Content == "" {
		t.Fatalf("public fetch failed: %v", err)
	}
	t.Logf("public page fetched: %d UTF-8 bytes, truncated=%v", len(page.Content), page.Truncated)
}
