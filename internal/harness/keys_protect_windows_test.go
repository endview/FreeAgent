//go:build windows

package harness

import (
	"os"
	"strings"
	"testing"
)

func TestKeyStoreDPAPIRoundTrip(t *testing.T) {
	sentinel := sentinelKey(t)
	keys := testKeyStore(t)
	id := addTestProfile(t, keys, "https://api.example.com", KeySourceSaved, "", sentinel)
	raw, err := os.ReadFile(keys.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), sentinel) {
		t.Fatal("keys file contains the key in plain text")
	}
	fresh := NewKeyStore(keys.Path())
	view, err := fresh.Find(id)
	if err != nil || view.Protection != keyProtectionDPAPI || !view.Encrypted {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	resolved, err := fresh.Resolve(id)
	if err != nil || resolved.secret != sentinel {
		t.Fatalf("decrypted key differs: err=%v", err)
	}
}
