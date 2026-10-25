//go:build windows

package winlaunch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfileLeaseOverlapKeepsGrantsAndProtection(t *testing.T) {
	program, _ := os.Executable()
	root := t.TempDir()
	protected := filepath.Join(root, "protected")
	if err := os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(protected, "guard.txt")
	if err := os.WriteFile(marker, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := Spec{Command: program, Args: []string{"-test.run=TestServiceNetworkHelper", "--", "--service-network-helper", "server"}, Dir: root, Env: os.Environ(), Mode: ModeAppContainer, Network: NetworkOff, AppContainerName: fmt.Sprintf("FreeAgent.Lease.%d", time.Now().UnixNano()), Grants: []Grant{{Root: root, Access: AccessWrite}, {Root: filepath.Dir(program), Access: AccessRead}}, ProtectedPaths: []ProtectedPath{{Path: protected, Mode: ProtectReadOnly}}}
	launch := func() *Process {
		t.Helper()
		p, err := Start(t.Context(), spec)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = p.Close() })
		line := make(chan string, 1)
		go func() { text, _ := bufio.NewReader(p.Stdout()).ReadString('\n'); line <- text }()
		select {
		case text := <-line:
			if !strings.HasPrefix(text, "http://") {
				t.Fatal(text)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("launch never observed")
		}
		return p
	}
	first, second := launch(), launch()
	if err := ReleaseKept(spec.AppContainerName); !errors.Is(err, ErrProfileBusy) {
		t.Fatalf("live grants released %v", err)
	}
	incompatible := spec
	incompatible.Network = NetworkInternet
	if _, err := Start(t.Context(), incompatible); !errors.Is(err, ErrProfileBusy) {
		t.Fatalf("mixed capabilities accepted %v", err)
	}
	broken := spec
	broken.Command = filepath.Join(root, "missing.exe")
	if _, err := Start(t.Context(), broken); err == nil {
		t.Fatal("missing executable started")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	probe := spec
	probe.Args = []string{"-test.run=TestAppContainerHelper", "--", "--appcontainer-probe-helper", "workspace|write|" + filepath.Join(root, "still-writable.txt"), "protected|write|" + marker}
	p, err := Start(t.Context(), probe)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := p.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(p.Stdout())
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "workspace=ok") || !strings.Contains(string(raw), "protected=fail:") {
		t.Fatalf("grant lost or protection released %s", raw)
	}
	if raw, _ := os.ReadFile(marker); string(raw) != "original" {
		t.Fatal("protected file changed")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	profileLeases.Lock()
	remaining := profileLeases.items[strings.ToLower(spec.AppContainerName)]
	profileLeases.Unlock()
	if remaining != nil {
		t.Fatal("last confirmed exit retained a lease")
	}
	if err := ReleaseKept(spec.AppContainerName); err != nil {
		t.Fatal(err)
	}
	t.Log("interleaved close, failed start and incompatible network retained exact shared grants/protection")
}

func TestProfileCleanupFailureIsReportedAndIdempotent(t *testing.T) {
	want := errors.New("fixture ACL restoration failed")
	calls := 0
	p := &profile{restore: []func() error{func() error { calls++; return want }}}
	for range 2 {
		if err := cleanupProfile(p); !errors.Is(err, want) {
			t.Fatalf("cleanup failure hidden: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("profile reference released %d times", calls)
	}
}
