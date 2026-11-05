// Package postgres manages only the local PostgreSQL dependency. It does not
// schedule tasks, own application records, or discover other Core instances.
package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const ConfigFile = "postgres.json"
const ServiceFileEnv = "FREEAGENT_PG_SERVICE_FILE"
const BinEnv = "FREEAGENT_POSTGRES_BIN"

// Connection is private local configuration, never model input or public API
// output. The service file is an explicit initialization/maintenance input;
// ordinary Core operations use only the separate, restricted runtime role.
type Connection struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	CoreID   string `json:"core_id,omitempty"`
	Bin      string `json:"bin,omitempty"`
	Managed  bool   `json:"managed,omitempty"`
}

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func Read(path string) (Connection, error) {
	var c Connection
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16384 {
		return c, errors.New("postgres: private connection file unavailable")
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &c) != nil {
		return c, errors.New("postgres: invalid private connection file")
	}
	ip := net.ParseIP(c.Host)
	if ip == nil || !ip.IsLoopback() || c.Port < 1 || c.Port > 65535 || c.User == "" || c.Database == "" || c.Password == "" {
		return c, errors.New("postgres: authenticated numeric loopback connection required")
	}
	return c, nil
}
func Write(path string, c Connection) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = protect(path, false)
	}
	return errors.Join(err, f.Sync(), f.Close())
}
func SQL(c Connection, generation string, readOnly bool) (*sql.DB, error) {
	config, err := pgx.ParseConfig("host=127.0.0.1 sslmode=disable")
	if err != nil {
		return nil, err
	}
	config.Host, config.Port, config.User, config.Password, config.Database = c.Host, uint16(c.Port), c.User, c.Password, c.Database
	config.ConnectTimeout = 5 * time.Second
	config.Tracer = queryTrace{}
	config.RuntimeParams["application_name"] = "FreeAgent"
	config.RuntimeParams["search_path"] = "fa,pg_catalog,public"
	config.RuntimeParams["statement_timeout"] = "15000"
	config.RuntimeParams["lock_timeout"] = "5000"
	config.RuntimeParams["idle_in_transaction_session_timeout"] = "30000"
	if generation != "" {
		config.RuntimeParams["freeagent.generation"] = generation
	}
	if readOnly {
		config.RuntimeParams["default_transaction_read_only"] = "on"
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(30 * time.Second)
	return db, nil
}
func Binary(c Connection, name string) (string, error) {
	bin := c.Bin
	if bin == "" {
		bin = os.Getenv(BinEnv)
	}
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		for _, candidate := range []string{filepath.Join(filepath.Dir(exe), "runtime", "postgresql", "bin"), filepath.Join(filepath.Dir(exe), "..", "runtime", "postgresql", "bin")} {
			if _, err := os.Stat(filepath.Join(candidate, executable("pg_ctl"))); err == nil {
				bin = candidate
				break
			}
		}
	}
	path := filepath.Join(bin, executable(name))
	if !filepath.IsAbs(path) {
		return "", errors.New("postgres: bundled runtime unavailable; set FREEAGENT_POSTGRES_BIN to a supported PG 15 bin directory")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("postgres: required runtime program %s unavailable", name)
	}
	return path, nil
}
func executable(s string) string {
	if runtime.GOOS == "windows" {
		return s + ".exe"
	}
	return s
}
func quote(s string) string {
	if runtime.GOOS != "windows" {
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
func run(ctx context.Context, c Connection, name string, args ...string) error {
	path, err := Binary(c, name)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, path, args...)
	hide(command)
	command.Env = dependencyEnvironment(c)
	// pg_ctl's detached Windows children can inherit stdout. A regular file
	// avoids waiting forever for an inherited pipe after pg_ctl itself exits.
	diagnostic, err := os.CreateTemp("", "freeagent-pg-diagnostic-*")
	if err != nil {
		return err
	}
	defer os.Remove(diagnostic.Name())
	command.Stdout, command.Stderr = diagnostic, diagnostic
	err = command.Run()
	_ = diagnostic.Close()
	out, _ := os.ReadFile(diagnostic.Name())
	if err != nil {
		// No credentials are passed as arguments. Still return a bounded,
		// redacted diagnostic if a dependency reports its configuration.
		message := strings.ReplaceAll(string(out), c.Password, "[redacted]")
		if len(message) > 2048 {
			message = message[len(message)-2048:]
		}
		return fmt.Errorf("postgres: %s failed: %w: %s", name, err, message)
	}
	return nil
}

type managedRuntime struct {
	refs    int
	started bool
	c       Connection
}

var runtimeMu sync.Mutex
var runtimes = map[string]*managedRuntime{}

// Acquire starts only this directory's configured managed dependency. An
// externally supplied service is never stopped when a Core disconnects.
func Acquire(ctx context.Context, data string) (Connection, func() error, error) {
	return acquireConfigured(ctx, data, false)
}

// AcquireOwner is used only while the caller holds this Core's exclusive local
// Owner lock. It also adopts this managed cluster after an Owner crash, so the
// next clean exit does not leave the dependency running indefinitely.
func AcquireOwner(ctx context.Context, data string) (Connection, func() error, error) {
	return acquireConfigured(ctx, data, true)
}
func acquireConfigured(ctx context.Context, data string, owner bool) (Connection, func() error, error) {
	c, err := Read(filepath.Join(data, ConfigFile))
	if err != nil {
		return c, nil, err
	}
	release, err := acquire(ctx, data, c, owner)
	return c, release, err
}
func acquire(ctx context.Context, data string, c Connection, owner bool) (func() error, error) {
	if !c.Managed {
		return func() error { return nil }, nil
	}
	runtimeMu.Lock()
	defer runtimeMu.Unlock()
	r := runtimes[data]
	if r != nil {
		r.refs++
		r.started = r.started || owner
		return releaseRuntime(data), nil
	}
	cluster := filepath.Join(data, "postgresql")
	started := owner
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := run(check, c, "pg_ctl", "status", "-D", cluster)
	cancel()
	if err != nil {
		start, stop := context.WithTimeout(ctx, 30*time.Second)
		err = run(start, c, "pg_ctl", "start", "-D", cluster, "-l", filepath.Join(data, "postgresql.log"), "-w", "-t", "20")
		stop()
		if err != nil {
			if owner {
				// pg_ctl may time out after launching the postmaster. The local
				// Owner lock excludes another Owner, and only this exact managed
				// cluster can be stopped. Preserve its files for diagnosis.
				cleanup, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				_ = run(cleanup, c, "pg_ctl", "stop", "-D", cluster, "-m", "fast", "-w", "-t", "20")
				cancel()
			}
			return nil, err
		}
		started = true
	}
	runtimes[data] = &managedRuntime{refs: 1, started: started, c: c}
	return releaseRuntime(data), nil
}
func releaseRuntime(data string) func() error {
	var once sync.Once
	var result error
	return func() error {
		once.Do(func() {
			runtimeMu.Lock()
			defer runtimeMu.Unlock()
			r := runtimes[data]
			if r == nil {
				return
			}
			r.refs--
			if r.refs > 0 {
				return
			}
			delete(runtimes, data)
			if r.started {
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				result = run(ctx, r.c, "pg_ctl", "stop", "-D", filepath.Join(data, "postgresql"), "-m", "fast", "-w", "-t", "20")
			}
		})
		return result
	}
}

// Bootstrap creates a distinct DB and runtime role. No old data or foreign
// schema is accepted. The returned admin connection is initialization-only.
func Bootstrap(ctx context.Context, data string) (Connection, *sql.DB, func() error, error) {
	if err := protect(data, true); err != nil {
		return Connection{}, nil, nil, err
	}
	var admin Connection
	var release = func() error { return nil }
	if service := os.Getenv(ServiceFileEnv); service != "" {
		var err error
		admin, err = Read(service)
		if err != nil {
			return Connection{}, nil, nil, err
		}
	} else {
		if runtime.GOOS != "windows" && os.Geteuid() == 0 {
			return Connection{}, nil, nil, errors.New("postgres: run FreeAgent as a dedicated non-root user; use install-server")
		}
		admin = Connection{Host: "127.0.0.1", User: "fa_admin", Password: ID() + ID(), Database: "postgres", Managed: true}
		bin, err := Binary(admin, "pg_ctl")
		if err != nil {
			return Connection{}, nil, nil, err
		}
		admin.Bin = filepath.Dir(bin)
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return Connection{}, nil, nil, err
		}
		admin.Port = listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()
		pw := filepath.Join(data, ".pg-init-password")
		if err := os.WriteFile(pw, []byte(admin.Password), 0600); err != nil {
			return Connection{}, nil, nil, err
		}
		locale := "C.UTF-8"
		if runtime.GOOS == "windows" {
			locale = "en-US.utf8"
		}
		init, stop := context.WithTimeout(ctx, 45*time.Second)
		err = run(init, admin, "pg_ctl", "initdb", "-D", filepath.Join(data, "postgresql"), "-o", "-U fa_admin --pwfile="+quote(pw)+" --auth=scram-sha-256 --encoding=UTF8 --locale="+locale)
		stop()
		_ = os.Remove(pw)
		if err != nil {
			return Connection{}, nil, nil, err
		}
		settings := fmt.Sprintf("\nlisten_addresses = '127.0.0.1'\nport = %d\nmax_connections = 24\nshared_buffers = '64MB'\npassword_encryption = 'scram-sha-256'\nlogging_collector = off\nlog_statement = 'none'\n", admin.Port)
		f, err := os.OpenFile(filepath.Join(data, "postgresql", "postgresql.conf"), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return Connection{}, nil, nil, err
		}
		_, err = f.WriteString(settings)
		err = errors.Join(err, f.Close())
		if err != nil {
			return Connection{}, nil, nil, err
		}
		persistedAdmin := admin
		persistedAdmin.Bin = ""
		if err := Write(filepath.Join(data, "postgres-maintenance.json"), persistedAdmin); err != nil {
			return Connection{}, nil, nil, err
		}
		release, err = acquire(ctx, data, admin, true)
		if err != nil {
			return Connection{}, nil, nil, err
		}
	}
	adminDB, err := SQL(admin, "", false)
	if err != nil {
		_ = release()
		return Connection{}, nil, nil, err
	}
	fail := func(err error) (Connection, *sql.DB, func() error, error) {
		_ = adminDB.Close()
		_ = release()
		return Connection{}, nil, nil, err
	}
	var version int
	if err := adminDB.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil || version < 150000 || version >= 160000 {
		return fail(errors.Join(errors.New("postgres: supported server major version is 15"), err))
	}
	id := ID()
	role := "fa_" + id
	database := "fa_" + id
	password := ID() + ID()
	// Identifiers and password are generated lowercase hexadecimal, never user SQL.
	if _, err := adminDB.ExecContext(ctx, `CREATE ROLE `+role+` LOGIN PASSWORD '`+password+`' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT`); err != nil {
		return fail(err)
	}
	if _, err := adminDB.ExecContext(ctx, `CREATE DATABASE `+database+` TEMPLATE template0 ENCODING 'UTF8'`); err != nil {
		return fail(err)
	}
	if _, err := adminDB.ExecContext(ctx, `REVOKE ALL ON DATABASE `+database+` FROM PUBLIC`); err != nil {
		return fail(err)
	}
	if _, err := adminDB.ExecContext(ctx, `GRANT CONNECT ON DATABASE `+database+` TO `+role); err != nil {
		return fail(err)
	}
	_ = adminDB.Close()
	admin.Database = database
	adminDB, err = SQL(admin, "", false)
	if err != nil {
		return fail(err)
	}
	if _, err := adminDB.ExecContext(ctx, `REVOKE CREATE ON SCHEMA public FROM PUBLIC`); err != nil {
		return fail(err)
	}
	c := Connection{Host: admin.Host, Port: admin.Port, User: role, Password: password, Database: database, CoreID: id, Bin: admin.Bin, Managed: admin.Managed}
	persisted := c
	if c.Managed {
		persisted.Bin = ""
	}
	if err := Write(filepath.Join(data, ConfigFile), persisted); err != nil {
		return fail(err)
	}
	return c, adminDB, release, nil
}

// Command runs a PG utility with credentials in the child environment, never
// its arguments, diagnostics, model context, or shell command text.
func Command(ctx context.Context, c Connection, name string, args ...string) (*exec.Cmd, error) {
	path, err := Binary(c, name)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	hide(cmd)
	cmd.Env = append(dependencyEnvironment(c), "PGHOST="+c.Host, "PGPORT="+strconv.Itoa(c.Port), "PGUSER="+c.User, "PGPASSWORD="+c.Password, "PGDATABASE="+c.Database, "PGCONNECT_TIMEOUT=5", "PGSSLMODE=disable")
	return cmd, nil
}

func dependencyEnvironment(c Connection) []string {
	var env []string
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "TEMP", "TMP", "TMPDIR", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "LANG", "LC_ALL", "TZ"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	if runtime.GOOS != "windows" {
		if bin, err := Binary(c, "pg_ctl"); err == nil {
			env = append(env, "LD_LIBRARY_PATH="+filepath.Join(filepath.Dir(bin), "..", "lib"))
		}
	}
	return env
}
