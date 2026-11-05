package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/winlaunch"
)

// The desktop launcher manages only the optional bundled channel process.
// Linux service deployment uses its separate systemd unit instead.
type channelProcess struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func startChannelProcess(parent context.Context, data string) *channelProcess {
	ctx, cancel := context.WithCancel(parent)
	manager := &channelProcess{cancel: cancel, done: make(chan struct{})}
	go func() { defer close(manager.done); manageChannelProcess(ctx, data) }()
	return manager
}
func (m *channelProcess) Close() {
	if m != nil {
		m.cancel()
		<-m.done
	}
}

func channelBootstraps(data string) ([]string, string) {
	paths, _ := filepath.Glob(filepath.Join(data, "channel-runtime", "*.json"))
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, ""
		}
		h.Write([]byte(path))
		h.Write(raw)
	}
	return paths, hex.EncodeToString(h.Sum(nil))
}
func manageChannelProcess(ctx context.Context, data string) {
	executable, err := os.Executable()
	if err != nil {
		return
	}
	name := "freeagent-channel"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	program := filepath.Join(filepath.Dir(executable), name)
	var child *winlaunch.Process
	var exited chan struct{}
	var signature string
	stop := func() {
		if child == nil {
			return
		}
		child.Terminate()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			_ = child.Kill()
			<-exited
		}
		_ = child.Close()
		child = nil
	}
	defer stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	nextStart := time.Time{}
	for {
		paths, wanted := channelBootstraps(data)
		if child != nil {
			select {
			case <-exited:
				_ = child.Close()
				child = nil
				nextStart = time.Now().Add(3 * time.Second)
			default:
			}
		}
		if child != nil && wanted != signature {
			stop()
		}
		if len(paths) > 0 && child == nil && !time.Now().Before(nextStart) {
			args := []string{"--state-dir", filepath.Join(data, "channel-runtime", "cursors")}
			for _, p := range paths {
				args = append(args, "--bootstrap-file", p)
			}
			child, err = winlaunch.Start(ctx, winlaunch.Spec{Command: program, Args: args, Env: channelEnvironment(), Mode: winlaunch.ModeHost})
			if err != nil {
				nextStart = time.Now().Add(10 * time.Second)
			} else {
				signature = wanted
				process := child
				exited = make(chan struct{})
				ended := exited
				log, _ := os.OpenFile(filepath.Join(data, "channel.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
				output := io.Writer(io.Discard)
				if log != nil {
					output = &channelLog{file: log, left: 1 << 20}
				}
				var readers sync.WaitGroup
				readers.Add(2)
				go func() { defer readers.Done(); _, _ = io.Copy(output, process.Stdout()) }()
				go func() { defer readers.Done(); _, _ = io.Copy(output, process.Stderr()) }()
				go func() {
					_, _ = process.Wait(context.Background())
					readers.Wait()
					if log != nil {
						_ = log.Close()
					}
					close(ended)
				}()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func channelEnvironment() []string {
	var env []string
	// The transport receives its own credential through the private bootstrap,
	// not the model's credential variables inherited by the Owner.
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "TEMP", "TMP", "TMPDIR", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "LANG", "LC_ALL", "TZ", "XDG_CACHE_HOME", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

type channelLog struct {
	mu   sync.Mutex
	file *os.File
	left int
}

func (w *channelLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if w.left > 0 {
		take := min(len(p), w.left)
		_, err := w.file.Write(p[:take])
		w.left -= take
		if err != nil {
			return 0, err
		}
	}
	return n, nil
}
