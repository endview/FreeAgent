package mcpstdio

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const helperArgumentPrefix = "--freeagent-mcpstdio-helper="

func TestCommandTransportHelperProcess(t *testing.T) {
	for _, argument := range os.Args[1:] {
		if strings.HasPrefix(argument, helperArgumentPrefix) {
			os.Exit(runTransportHelper(strings.TrimPrefix(argument, helperArgumentPrefix)))
		}
	}
}

func runTransportHelper(mode string) int {
	switch mode {
	case "echo":
		// This is intentionally much larger than a typical pipe buffer. The
		// protocol round trip can finish only if stderr is drained continuously.
		chunk := strings.Repeat("e", 32<<10)
		for range 128 {
			_, _ = io.WriteString(os.Stderr, chunk)
		}
		line, err := bufio.NewReader(os.Stdin).ReadBytes('\n')
		if err != nil {
			return 2
		}
		message, err := jsonrpc.DecodeMessage(bytes.TrimSpace(line))
		if err != nil {
			return 3
		}
		request, ok := message.(*jsonrpc.Request)
		if !ok || !request.ID.IsValid() {
			return 3
		}
		result, err := json.Marshal(map[string]string{"method": request.Method})
		if err != nil {
			return 3
		}
		response := &jsonrpc.Response{ID: request.ID, Result: result}
		encoded, err := jsonrpc.EncodeMessage(response)
		if err == nil {
			encoded = append(encoded, '\n')
			_, err = os.Stdout.Write(encoded)
		}
		if err != nil {
			return 3
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	case "environment":
		method := fmt.Sprintf(
			"allowed=%s;inherited=%s;gorace=%s;cwd=%s",
			os.Getenv("FREEAGENT_ALLOWED"),
			os.Getenv("FREEAGENT_MUST_NOT_INHERIT"),
			os.Getenv("GORACE"),
			mustGetwd(),
		)
		_, _ = fmt.Fprintf(
			os.Stdout,
			"{\"jsonrpc\":\"2.0\",\"id\":\"environment\",\"result\":%q}\n",
			method,
		)
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	case "oversize":
		_, _ = fmt.Fprintf(os.Stdout, "{\"jsonrpc\":\"2.0\",\"method\":%q}\n", strings.Repeat("x", 1024))
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	case "invalid-utf8":
		_, _ = os.Stdout.Write([]byte{0xff, '\n'})
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	case "unterminated":
		_, _ = io.WriteString(os.Stdout, `{"jsonrpc":"2.0","method":"event"}`)
		return 0
	case "graceful":
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	case "stubborn":
		ignoreTerminationForTest()
		time.Sleep(30 * time.Second)
		return 0
	case "block-stdin":
		marker := os.Getenv("FREEAGENT_BLOCKED_WRITER_MARKER")
		_ = os.WriteFile(marker, []byte("started"), 0o600)
		// Deliberately never read stdin. A frame larger than the anonymous-pipe
		// buffer must block until the caller's context closes the connection.
		time.Sleep(30 * time.Second)
		return 0
	case "spawn-pipe-holder":
		cmd := exec.Command(os.Args[0], helperArguments("pipe-holder")...)
		cmd.Env = []string{
			"FREEAGENT_HOLDER_MARKER=" + os.Getenv("FREEAGENT_HOLDER_MARKER"),
			helperGORACEEnvironment(),
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "start pipe holder: %v", err)
			return 5
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	case "pipe-holder":
		marker := os.Getenv("FREEAGENT_HOLDER_MARKER")
		_ = os.WriteFile(marker+".started", []byte("started"), 0o600)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				_ = os.WriteFile(marker+".done", []byte("done"), 0o600)
				return 0
			}
			time.Sleep(10 * time.Millisecond)
		}
		return 6
	default:
		return 64
	}
}

func mustGetwd() string {
	dir, err := os.Getwd()
	if err != nil {
		return "ERROR"
	}
	return dir
}

func TestCommandTransportRoundTripAndContinuousStderrDrain(t *testing.T) {
	conn := connectHelper(t, "echo", t.TempDir(), []string{"FREEAGENT_ALLOWED=yes"}, 1024, time.Second)

	id, err := jsonrpc.MakeID("request-1")
	if err != nil {
		t.Fatal(err)
	}
	want := &jsonrpc.Request{ID: id, Method: "ping"}

	done := make(chan error, 1)
	go func() {
		if err := conn.Write(context.Background(), want); err != nil {
			done <- err
			return
		}
		got, err := conn.Read(context.Background())
		if err != nil {
			done <- err
			return
		}
		response, ok := got.(*jsonrpc.Response)
		if !ok || response.ID.Raw() != want.ID.Raw() ||
			responseResultString(response, "method") != want.Method {
			done <- fmt.Errorf("unexpected round-trip message %#v", got)
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		_ = conn.Close()
		t.Fatal("round trip blocked while helper filled stderr")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}

func TestCommandTransportUsesOnlySuppliedEnvironmentAndDirectory(t *testing.T) {
	t.Setenv("FREEAGENT_MUST_NOT_INHERIT", "secret-parent-value")
	dir := t.TempDir()
	conn := connectHelper(t, "environment", dir, []string{"FREEAGENT_ALLOWED=yes"}, 2048, time.Second)

	message, err := conn.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	response, ok := message.(*jsonrpc.Response)
	if !ok {
		t.Fatalf("Read() type = %T, want *jsonrpc.Response", message)
	}
	method := responseResultString(response, "")
	if !strings.Contains(method, "allowed=yes") {
		t.Fatalf("child did not receive supplied environment: %q", method)
	}
	if !strings.Contains(method, "inherited=") || strings.Contains(method, "secret-parent-value") {
		t.Fatalf("child inherited parent environment: %q", method)
	}
	if !strings.Contains(method, "gorace="+strings.TrimPrefix(helperGORACEEnvironment(), "GORACE=")) {
		t.Fatalf("child did not receive the bounded race-detector environment: %q", method)
	}
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotDir := strings.TrimPrefix(strings.Split(method, ";cwd=")[1], "cwd=")
	gotDir, err = filepath.EvalSymlinks(gotDir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(filepath.Clean(gotDir), filepath.Clean(wantDir)) {
		t.Fatalf("child cwd = %q, want %q", gotDir, wantDir)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCommandTransportReadBoundaries(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want error
	}{
		{name: "oversize", mode: "oversize", want: errFrameTooLarge},
		{name: "invalid UTF-8", mode: "invalid-utf8", want: errInvalidUTF8},
		{name: "unterminated", mode: "unterminated", want: errUnterminatedFrame},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn := connectHelper(t, test.mode, t.TempDir(), []string{"FREEAGENT_ALLOWED=yes"}, 128, time.Second)
			_, err := conn.Read(context.Background())
			if !errors.Is(err, test.want) {
				t.Fatalf("Read() error = %v, want %v", err, test.want)
			}
			if closeErr := conn.Close(); closeErr != nil {
				t.Fatalf("Close() = %v, want nil", closeErr)
			}
		})
	}
}

func TestCommandTransportRejectsOversizeWriteWithoutSending(t *testing.T) {
	conn := connectHelper(t, "graceful", t.TempDir(), []string{"FREEAGENT_ALLOWED=yes"}, 128, time.Second)
	id, err := jsonrpc.MakeID("request-1")
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Write(context.Background(), &jsonrpc.Request{
		ID:     id,
		Method: strings.Repeat("x", 1024),
	})
	if !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("Write() error = %v, want %v", err, errFrameTooLarge)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCommandTransportCanceledBlockedWriteClosesAndReaps(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "blocked-writer-started")
	conn := connectHelper(
		t,
		"block-stdin",
		dir,
		[]string{"FREEAGENT_BLOCKED_WRITER_MARKER=" + marker},
		maxCommandFrameBytes,
		100*time.Millisecond,
	)
	concrete := conn.(*commandConnection)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("blocked-writer helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	id, err := jsonrpc.MakeID("blocked-write")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = conn.Write(ctx, &jsonrpc.Request{
		ID:     id,
		Method: strings.Repeat("x", 8<<20),
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked Write() error = %v, want %v", err, context.DeadlineExceeded)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("blocked Write() cancellation took %v, want bounded shutdown", elapsed)
	}
	if concrete.cmd.ProcessState == nil {
		t.Fatal("blocked-write subprocess was not reaped")
	}
}

func TestCommandTransportCloseIsConcurrentAndIdempotent(t *testing.T) {
	conn := connectHelper(t, "graceful", t.TempDir(), []string{"FREEAGENT_ALLOWED=yes"}, 1024, time.Second)

	const callers = 8
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- conn.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Close() = %v, want nil", err)
		}
	}
	if _, err := conn.Read(context.Background()); !errors.Is(err, mcp.ErrConnectionClosed) {
		t.Fatalf("Read() after Close = %v, want %v", err, mcp.ErrConnectionClosed)
	}
	if err := conn.Write(context.Background(), &jsonrpc.Request{Method: "late"}); !errors.Is(err, mcp.ErrConnectionClosed) {
		t.Fatalf("Write() after Close = %v, want %v", err, mcp.ErrConnectionClosed)
	}
}

func TestCommandTransportTerminatesOrKillsAndReapsUncooperativeChild(t *testing.T) {
	conn := connectHelper(t, "stubborn", t.TempDir(), []string{"FREEAGENT_ALLOWED=yes"}, 1024, 50*time.Millisecond)
	concrete := conn.(*commandConnection)

	started := time.Now()
	err := conn.Close()
	if err != nil && !errors.Is(err, errForcedProcessKill) {
		t.Fatalf("Close() error = %v, want clean termination or %v", err, errForcedProcessKill)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("Close() took %v, want bounded shutdown", elapsed)
	}
	// A non-nil ProcessState is set by Wait and proves the child was reaped.
	// Exited reports false for a process terminated by signal on Unix.
	if concrete.cmd.ProcessState == nil {
		t.Fatalf("subprocess was not reaped: state=%v", concrete.cmd.ProcessState)
	}
}

func TestCommandTransportCloseKillsManagedDescendants(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "release-pipe-holder")
	conn := connectHelper(
		t,
		"spawn-pipe-holder",
		dir,
		[]string{"FREEAGENT_HOLDER_MARKER=" + marker},
		1024,
		200*time.Millisecond,
	)
	startedDeadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker + ".started"); err == nil {
			break
		}
		if time.Now().After(startedDeadline) {
			t.Fatal("pipe-holder helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
	case <-time.After(time.Second):
		// Release the holder so a broken implementation does not leave the test
		// process blocked, then report the bounded-close failure.
		_ = os.WriteFile(marker, []byte("release"), 0o600)
		<-closeDone
		t.Fatal("Close waited for a grandchild that inherited stderr")
	}

	if err := os.WriteFile(marker, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker + ".done"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed descendant survived Close: %v", err)
	}
}

func TestNewCommandTransportValidationAndDefensiveCopies(t *testing.T) {
	exe := helperExecutable(t)
	dir := t.TempDir()

	tests := []struct {
		name    string
		command string
		dir     string
		env     []string
		max     int
		timeout time.Duration
	}{
		{name: "relative command", command: "server", dir: dir, env: []string{"A=B"}, max: 1, timeout: time.Second},
		{name: "relative directory", command: exe, dir: "relative", env: []string{"A=B"}, max: 1, timeout: time.Second},
		{name: "bad environment", command: exe, dir: dir, env: []string{"A"}, max: 1, timeout: time.Second},
		{name: "zero frame limit", command: exe, dir: dir, env: []string{"A=B"}, max: 0, timeout: time.Second},
		{name: "excessive frame limit", command: exe, dir: dir, env: []string{"A=B"}, max: maxCommandFrameBytes + 1, timeout: time.Second},
		{name: "zero timeout", command: exe, dir: dir, env: []string{"A=B"}, max: 1, timeout: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := newCommandTransport(test.command, nil, test.dir, test.env, test.max, test.timeout)
			if !errors.Is(err, errInvalidTransportArg) {
				t.Fatalf("newCommandTransport() error = %v, want %v", err, errInvalidTransportArg)
			}
		})
	}

	args := helperArguments("environment")
	env := []string{"FREEAGENT_ALLOWED=yes", helperGORACEEnvironment()}
	transport, err := newCommandTransport(exe, args, dir, env, 2048, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = helperArgumentPrefix + "stubborn"
	env[0] = "FREEAGENT_ALLOWED=mutated"
	conn, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	message, err := conn.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if method := responseResultString(message.(*jsonrpc.Response), ""); !strings.Contains(method, "allowed=yes") {
		t.Fatalf("constructor did not copy launch inputs: %q", method)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Connect(context.Background()); err == nil {
		t.Fatal("second Connect() succeeded, want single-use rejection")
	}
}

func responseResultString(response *jsonrpc.Response, key string) string {
	if response == nil {
		return ""
	}
	if key == "" {
		var value string
		_ = json.Unmarshal(response.Result, &value)
		return value
	}
	var value map[string]string
	_ = json.Unmarshal(response.Result, &value)
	return value[key]
}

func TestCommandTransportCanceledConnectDoesNotStartProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport, err := newCommandTransport(
		helperExecutable(t),
		helperArguments("stubborn"),
		t.TempDir(),
		[]string{"FREEAGENT_ALLOWED=yes"},
		1024,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Connect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect() error = %v, want %v", err, context.Canceled)
	}
}

func connectHelper(
	t *testing.T,
	mode string,
	dir string,
	env []string,
	maxFrameBytes int,
	terminateAfter time.Duration,
) mcp.Connection {
	t.Helper()
	transport, err := newCommandTransport(
		helperExecutable(t),
		helperArguments(mode),
		dir,
		append(append([]string(nil), env...), helperGORACEEnvironment()),
		maxFrameBytes,
		terminateAfter,
	)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := transport.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func helperGORACEEnvironment() string {
	const boundedExit = "atexit_sleep_ms=0"
	inherited := strings.TrimSpace(os.Getenv("GORACE"))
	options := make([]string, 0, len(strings.Fields(inherited))+1)
	for _, option := range strings.Fields(inherited) {
		if strings.HasPrefix(option, "atexit_sleep_ms=") {
			continue
		}
		options = append(options, option)
	}
	options = append(options, boundedExit)
	return "GORACE=" + strings.Join(options, " ")
}

func helperArguments(mode string) []string {
	return []string{
		"-test.run=^TestCommandTransportHelperProcess$",
		"--",
		helperArgumentPrefix + mode,
	}
}

func helperExecutable(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "windows", "linux", "darwin":
	default:
		t.Skip("subprocess transport is unsupported on this platform")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		t.Fatal(err)
	}
	return exe
}
