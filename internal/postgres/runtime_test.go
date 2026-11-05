package postgres

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestManagedRuntimeAdoptsCrashSurvivorAndRejectsOccupiedPort(t *testing.T) {
	if os.Getenv(BinEnv) == "" {
		t.Skip("bundled PG runtime required; use scripts/test-postgres.py")
	}
	t.Setenv(ServiceFileEnv, "")
	data := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	c, db, release, err := Bootstrap(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	cluster := filepath.Join(data, "postgresql")
	// A postmaster surviving a killed Owner belongs to this exact managed
	// directory. A read-only observer must leave it alone; the new Owner adopts
	// it and stops it on its own final close.
	if err := run(ctx, c, "pg_ctl", "start", "-D", cluster, "-l", filepath.Join(data, "postgresql.log"), "-w", "-t", "20"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = run(context.Background(), c, "pg_ctl", "stop", "-D", cluster, "-m", "immediate", "-w", "-t", "10")
	})
	_, observe, err := Acquire(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := observe(); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, c, "pg_ctl", "status", "-D", cluster); err != nil {
		t.Fatal("observer stopped a running managed dependency", err)
	}
	_, adopt, err := AcquireOwner(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := adopt(); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, c, "pg_ctl", "status", "-D", cluster); err == nil {
		t.Fatal("new Owner left its adopted dependency running")
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
	if err != nil {
		t.Fatal(err)
	}
	_, wrong, err := AcquireOwner(ctx, data)
	_ = listener.Close()
	if err == nil {
		_ = wrong()
		t.Fatal("accepted occupied PG port")
	}
	if _, err := os.Stat(filepath.Join(cluster, "PG_VERSION")); err != nil {
		t.Fatal("failed start damaged managed data", err)
	}
	_, restart, err := AcquireOwner(ctx, data)
	if err != nil {
		t.Fatal("could not restart after freeing the port", err)
	}
	if err := restart(); err != nil {
		t.Fatal(err)
	}
}
