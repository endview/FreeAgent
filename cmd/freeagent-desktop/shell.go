package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	engineLogLimit       = 1 << 20
	engineTokenMinLength = 32
)

type engineLaunch struct {
	Origin  string
	Address string
	Token   string
	Port    int
}

func parseEngineFirstLine(line string) (engineLaunch, error) {
	line = strings.TrimSpace(line)
	parsed, err := url.Parse(line)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" ||
		parsed.Path != "/" || parsed.RawQuery != "" || parsed.User != nil {
		return engineLaunch{}, errors.New("engine launch address must be a loopback HTTP root")
	}
	port := parsed.Port()
	if port == "" {
		return engineLaunch{}, errors.New("engine launch address must include a port")
	}
	var portNumber int
	if _, err := fmt.Sscanf(port, "%d", &portNumber); err != nil || portNumber < 1 || portNumber > 65535 {
		return engineLaunch{}, errors.New("engine launch address has an invalid port")
	}
	const prefix = "key="
	if !strings.HasPrefix(parsed.Fragment, prefix) {
		return engineLaunch{}, errors.New("engine launch address is missing its key")
	}
	token := strings.TrimPrefix(parsed.Fragment, prefix)
	if len(token) < engineTokenMinLength || strings.ContainsAny(token, "\r\n") {
		return engineLaunch{}, errors.New("engine launch key is too short")
	}
	origin := "http://" + net.JoinHostPort(parsed.Hostname(), port)
	return engineLaunch{Origin: origin, Address: origin + "/#key=" + token, Token: token, Port: portNumber}, nil
}

func instanceName(dataDir string) (string, error) {
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		absolute = resolved
	}
	absolute = strings.TrimRight(filepath.Clean(absolute), `\/`)
	if runtime.GOOS == "windows" {
		absolute = strings.ToLower(absolute)
	}
	sum := sha256.Sum256([]byte(absolute))
	return `Local\FreeAgent-` + hex.EncodeToString(sum[:])[:16], nil
}

func externalURLAllowed(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Scheme == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed.Hostname() != ""
	case "mailto":
		return parsed.Opaque != "" || parsed.Path != ""
	default:
		return false
	}
}

type boundedLogWriter struct {
	mu        sync.Mutex
	dst       io.Writer
	max       int
	written   int
	truncated bool
}

func newBoundedLogWriter(dst io.Writer, max int) *boundedLogWriter {
	return &boundedLogWriter{dst: dst, max: max}
}

func (w *boundedLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.max < 0 {
		w.max = 0
	}
	if w.written >= w.max {
		if !w.truncated {
			w.truncated = true
			_, _ = io.WriteString(w.dst, "\n[engine log truncated]\n")
		}
		return len(p), nil
	}
	allowed := len(p)
	if remaining := w.max - w.written; allowed > remaining {
		allowed = remaining
	}
	if allowed > 0 {
		n, err := w.dst.Write(p[:allowed])
		w.written += n
		if err != nil {
			return n, err
		}
	}
	if allowed < len(p) && !w.truncated {
		w.truncated = true
		_, _ = io.WriteString(w.dst, "\n[engine log truncated]\n")
	}
	return len(p), nil
}

func pickPort(previous int, available func(int) bool) int {
	if previous > 0 && available != nil && available(previous) {
		return previous
	}
	return 0
}

func needsInit(dataDir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dataDir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func initCLIArgs(workspace, dataDir string) []string {
	return []string{"init", "--workspace", workspace, "--data-dir", dataDir}
}

func workspaceCLIArgs(workspace, dataDir string) []string {
	return []string{"config", "workspace", "--path", workspace, "--data-dir", dataDir}
}

func ensureWritableDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	probe, err := os.CreateTemp(path, ".freeagent-write-probe-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func darkFromRegistry(value uint64, err error) bool {
	return err == nil && value == 0
}
