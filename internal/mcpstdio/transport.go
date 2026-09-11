package mcpstdio

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxCommandFrameBytes  = 16 << 20
	stderrDrainBufferSize = 32 << 10
	stdioReadBufferSize   = 32 << 10
)

var (
	errEmptyFrame          = errors.New("mcp stdio: empty frame")
	errFrameTooLarge       = errors.New("mcp stdio: frame exceeds configured limit")
	errInvalidUTF8         = errors.New("mcp stdio: frame is not valid UTF-8")
	errUnterminatedFrame   = errors.New("mcp stdio: frame is not newline terminated")
	errForcedProcessKill   = errors.New("mcp stdio: subprocess did not exit after stdin closed and was killed")
	errProcessReapTimeout  = errors.New("mcp stdio: subprocess wait did not finish after force kill")
	errStderrDrainTimeout  = errors.New("mcp stdio: stderr drain did not stop after its read pipe was closed")
	errInvalidTransportArg = errors.New("mcp stdio: invalid command transport configuration")
)

// commandTransport starts one exact local executable without involving a
// shell. A transport instance is single-use, as required by mcp.Transport.
// The argument and environment slices are copied during construction so the
// caller cannot change the launch authority after validation.
type commandTransport struct {
	command        string
	args           []string
	dir            string
	env            []string
	maxFrameBytes  int
	terminateAfter time.Duration

	connectMu  sync.Mutex
	connected  bool
	connection *commandConnection
}

// newCommandTransport constructs the private LOCAL_PROCESS transport used by
// the MCP adapter. command must be an absolute executable path, dir is either
// empty or absolute, and env is the complete environment passed to the child.
// In particular, env is never expanded with os.Environ.
func newCommandTransport(
	command string,
	args []string,
	dir string,
	env []string,
	maxFrameBytes int,
	terminateAfter time.Duration,
) (*commandTransport, error) {
	if command == "" || !filepath.IsAbs(command) {
		return nil, fmt.Errorf("%w: command must be an absolute path", errInvalidTransportArg)
	}
	if dir != "" && !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("%w: working directory must be empty or absolute", errInvalidTransportArg)
	}
	if maxFrameBytes <= 0 || maxFrameBytes > maxCommandFrameBytes {
		return nil, fmt.Errorf(
			"%w: max frame bytes must be between 1 and %d",
			errInvalidTransportArg,
			maxCommandFrameBytes,
		)
	}
	if terminateAfter <= 0 {
		return nil, fmt.Errorf("%w: terminate timeout must be positive", errInvalidTransportArg)
	}
	for i, entry := range env {
		if entry == "" || bytes.IndexByte([]byte(entry), '=') <= 0 {
			return nil, fmt.Errorf("%w: environment entry %d must be KEY=VALUE", errInvalidTransportArg, i)
		}
		if bytes.IndexByte([]byte(entry), 0) >= 0 {
			return nil, fmt.Errorf("%w: environment entry %d contains NUL", errInvalidTransportArg, i)
		}
	}

	return &commandTransport{
		command:        command,
		args:           append([]string(nil), args...),
		dir:            dir,
		env:            append([]string{}, env...),
		maxFrameBytes:  maxFrameBytes,
		terminateAfter: terminateAfter,
	}, nil
}

func (t *commandTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", errInvalidTransportArg)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	t.connectMu.Lock()
	defer t.connectMu.Unlock()
	if t.connected {
		return nil, fmt.Errorf("mcp stdio: transport already connected")
	}
	// Claim this single-use transport before starting. A failed launch must not
	// become a mutable retry boundary for the same transport value.
	t.connected = true

	cmd := exec.Command(launchPath(t.command), t.args...)
	cmd.Dir = launchDirPath(t.dir)
	// A non-nil empty slice is materially different from nil to os/exec: it
	// means an empty environment rather than inheritance from the parent.
	cmd.Env = append([]string{}, t.env...)
	configureManagedCommand(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: create stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, fmt.Errorf("mcp stdio: create stdin pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("mcp stdio: create stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stderr.Close()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("mcp stdio: start subprocess: %w", err)
	}
	processTree, err := attachManagedProcessTree(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = stderr.Close()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("mcp stdio: establish managed process tree: %w", err)
	}

	c := &commandConnection{
		cmd:             cmd,
		stdin:           stdin,
		stdout:          stdout,
		stderr:          stderr,
		terminateAfter:  t.terminateAfter,
		maxFrameBytes:   t.maxFrameBytes,
		incoming:        make(chan readResult, 1),
		closing:         make(chan struct{}),
		stderrDrainDone: make(chan struct{}),
		processTree:     processTree,
	}
	t.connection = c
	go c.drainStderr(stderr)
	go c.readLoop()
	return c, nil
}

// closeStartedConnection is the adapter's cleanup hook for an SDK Connect
// failure. Some SDK negotiation errors occur after Transport.Connect has
// started the child but before a ClientSession is returned to the caller.
func (t *commandTransport) closeStartedConnection() error {
	if t == nil {
		return nil
	}
	t.connectMu.Lock()
	connection := t.connection
	t.connectMu.Unlock()
	if connection == nil {
		return nil
	}
	return connection.Close()
}

type readResult struct {
	message jsonrpc.Message
	err     error
}

type commandConnection struct {
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	stderr          io.ReadCloser
	terminateAfter  time.Duration
	maxFrameBytes   int
	incoming        chan readResult
	closing         chan struct{}
	stderrDrainDone chan struct{}
	processTree     managedProcessTree

	readMu    sync.Mutex
	writeMu   sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

func (c *commandConnection) SessionID() string { return "" }

func (c *commandConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	if ctx == nil {
		return nil, fmt.Errorf("mcp stdio: nil read context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// The SDK serializes reads. Keeping the serialization here also makes the
	// connection safe when it is exercised directly in tests or future hosts.
	c.readMu.Lock()
	defer c.readMu.Unlock()
	select {
	case <-c.closing:
		return nil, mcp.ErrConnectionClosed
	default:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closing:
		return nil, mcp.ErrConnectionClosed
	case result := <-c.incoming:
		return result.message, result.err
	}
}

func (c *commandConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if ctx == nil {
		return fmt.Errorf("mcp stdio: nil write context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-c.closing:
		return mcp.ErrConnectionClosed
	default:
	}
	if message == nil {
		return fmt.Errorf("mcp stdio: cannot write nil JSON-RPC message")
	}

	data, err := jsonrpc.EncodeMessage(message)
	if err != nil {
		return fmt.Errorf("mcp stdio: encode JSON-RPC message: %w", err)
	}
	if len(data) > c.maxFrameBytes {
		return fmt.Errorf("%w: write size %d, limit %d", errFrameTooLarge, len(data), c.maxFrameBytes)
	}
	if !utf8.Valid(data) {
		return errInvalidUTF8
	}
	if bytes.IndexAny(data, "\r\n") >= 0 {
		return fmt.Errorf("mcp stdio: encoded JSON-RPC message contains an unescaped line break")
	}
	data = append(data, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.closing:
		return mcp.ErrConnectionClosed
	default:
	}
	// An anonymous-pipe Write can block forever when a server stops reading.
	// Run the physical write separately so cancellation can close the entire
	// connection and managed process tree. The buffered result prevents a
	// writer that is released by Close from being stranded on delivery.
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- writeCommandFrame(c.stdin, data)
	}()
	select {
	case err := <-writeDone:
		return err
	case <-ctx.Done():
		// A partial or complete frame may already have reached the server. Close
		// the session before returning so callers can safely classify the call as
		// ambiguous without leaving a blocked writer or child process behind.
		return errors.Join(ctx.Err(), c.Close())
	case <-c.closing:
		return mcp.ErrConnectionClosed
	}
}

func writeCommandFrame(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return fmt.Errorf("mcp stdio: write frame: %w", err)
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (c *commandConnection) Close() error {
	c.closeOnce.Do(func() {
		close(c.closing)

		var closeErrs []error
		if err := c.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			closeErrs = append(closeErrs, fmt.Errorf("mcp stdio: close subprocess stdin: %w", err))
		}

		waitDone := make(chan error, 1)
		go func() {
			waitDone <- c.cmd.Wait()
		}()

		graceTimer := time.NewTimer(c.terminateAfter)
		select {
		case err := <-waitDone:
			if err != nil {
				closeErrs = append(closeErrs, fmt.Errorf("mcp stdio: wait for subprocess: %w", err))
			}
			if err := c.processTree.kill(); err != nil {
				closeErrs = append(closeErrs, fmt.Errorf("mcp stdio: kill remaining process tree: %w", err))
			}
			graceTimer.Stop()
		case <-graceTimer.C:
			// General local processes have no MCP shutdown RPC. First request
			// platform termination for the entire managed tree, then reserve a
			// second bounded phase for an unconditional tree kill.
			c.processTree.terminate()
			terminateTimer := time.NewTimer(c.terminateAfter)
			select {
			case <-waitDone:
				terminateTimer.Stop()
				if err := c.processTree.kill(); err != nil {
					closeErrs = append(closeErrs, fmt.Errorf("mcp stdio: kill remaining process tree: %w", err))
				}
			case <-terminateTimer.C:
				killErr := c.processTree.kill()
				if killErr != nil {
					killErr = errors.Join(killErr, c.cmd.Process.Kill())
				}
				// Force-kill must always be followed by a bounded Wait attempt. An
				// operating-system/exec defect must not turn Close into a new
				// unbounded shutdown path.
				reapTimer := time.NewTimer(c.terminateAfter)
				var waitErr error
				select {
				case waitErr = <-waitDone:
					reapTimer.Stop()
				case <-reapTimer.C:
					closeErrs = append(closeErrs, errProcessReapTimeout)
				}
				if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
					closeErrs = append(closeErrs, fmt.Errorf("mcp stdio: kill subprocess tree: %w", killErr))
				}
				if killErr == nil {
					closeErrs = append(closeErrs, errForcedProcessKill)
				}
				if waitErr != nil && killErr != nil {
					closeErrs = append(closeErrs, fmt.Errorf("mcp stdio: wait for subprocess: %w", waitErr))
				}
			}
		}
		if err := c.processTree.close(); err != nil {
			closeErrs = append(closeErrs, fmt.Errorf("mcp stdio: close managed process tree: %w", err))
		}

		// Wait normally closes the process pipes. Close both read ends explicitly
		// so a grandchild that inherited stdout/stderr cannot keep either drain
		// alive after the direct child has been reaped.
		_ = c.stdout.Close()
		_ = c.stderr.Close()
		drainTimer := time.NewTimer(c.terminateAfter)
		select {
		case <-c.stderrDrainDone:
			drainTimer.Stop()
		case <-drainTimer.C:
			closeErrs = append(closeErrs, errStderrDrainTimeout)
		}
		c.closeErr = errors.Join(closeErrs...)
	})
	return c.closeErr
}

func (c *commandConnection) readLoop() {
	reader := bufio.NewReaderSize(c.stdout, min(c.maxFrameBytes+2, stdioReadBufferSize))
	for {
		frame, err := readNDJSONFrame(reader, c.maxFrameBytes)
		var message jsonrpc.Message
		if err == nil {
			message, err = jsonrpc.DecodeMessage(frame)
			if err != nil {
				err = fmt.Errorf("mcp stdio: decode JSON-RPC frame: %w", err)
			} else if _, serverOriginated := message.(*jsonrpc.Request); serverOriginated {
				// The first MCP slice is strictly Tool-only and client-initiated.
				// Reject both server calls and notifications before the SDK can
				// dispatch Sampling, Elicitation, listChanged, or future methods.
				message = nil
				err = errors.New(
					"mcp stdio: server-originated requests and notifications are forbidden",
				)
			} else if _, response := message.(*jsonrpc.Response); !response {
				message = nil
				err = errors.New("mcp stdio: decoded an unsupported JSON-RPC message type")
			}
		}
		select {
		case c.incoming <- readResult{message: message, err: err}:
		case <-c.closing:
			return
		}
		if err != nil {
			return
		}
	}
}

func (c *commandConnection) drainStderr(stderr io.ReadCloser) {
	defer close(c.stderrDrainDone)
	defer stderr.Close()
	buffer := make([]byte, stderrDrainBufferSize)
	_, _ = io.CopyBuffer(io.Discard, stderr, buffer)
}

func readNDJSONFrame(reader *bufio.Reader, maxFrameBytes int) ([]byte, error) {
	frame := make([]byte, 0, min(maxFrameBytes, stdioReadBufferSize))
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(frame)+len(fragment) > maxFrameBytes+2 {
			return nil, fmt.Errorf("%w: read exceeds limit %d", errFrameTooLarge, maxFrameBytes)
		}
		frame = append(frame, fragment...)
		switch {
		case err == nil:
			frame = frame[:len(frame)-1]
			if len(frame) > 0 && frame[len(frame)-1] == '\r' {
				frame = frame[:len(frame)-1]
			}
			if len(frame) == 0 {
				return nil, errEmptyFrame
			}
			if len(frame) > maxFrameBytes {
				return nil, fmt.Errorf("%w: read size %d, limit %d", errFrameTooLarge, len(frame), maxFrameBytes)
			}
			if !utf8.Valid(frame) {
				return nil, errInvalidUTF8
			}
			return frame, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(frame) == 0 {
				return nil, io.EOF
			}
			return nil, errUnterminatedFrame
		default:
			return nil, fmt.Errorf("mcp stdio: read frame: %w", err)
		}
	}
}
