package harness

import (
	"context"
	"errors"
	"testing"
)

func TestExtensionConfigurationRevokesExistingInvocation(t *testing.T) {
	cfg := Config{}
	cfg.Extensions.Enabled = map[string]bool{"com.example.test": true}
	host := newExtensionHost(t.TempDir(), t.TempDir(), cfg, nil)
	defer host.close()
	inv, err := host.beginInvocation(context.Background(), "com.example.test", nil, Run{}, ExtensionManifest{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Extensions.Enabled["com.example.test"] = false
	host.configure(cfg)
	if !errors.Is(inv.ctx.Err(), context.Canceled) {
		t.Fatal("existing invocation survived disable")
	}
	if _, err = host.invocation(inv.id, inv.extensionID); err == nil {
		t.Fatal("old invocation still authorized")
	}
}
