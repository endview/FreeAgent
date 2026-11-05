// Package channelplugin implements optional platform transport processes. It
// knows only the channel wire protocol, never the Harness or its database.
package channelplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

var ErrCoreUnavailable = errors.New("channel: Core generation or connection authorization changed")

type Client struct {
	Bootstrap channelwire.Bootstrap
	HTTP      *http.Client
}

func ReadBootstrap(path string) (channelwire.Bootstrap, error) {
	var b channelwire.Bootstrap
	f, err := os.Open(path)
	if err != nil {
		return b, errors.New("channel: bootstrap file unavailable; start the configured Core first")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &b) != nil {
		return b, errors.New("channel: invalid bootstrap")
	}
	u, err := url.Parse(b.Origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Port() == "" {
		return b, errors.New("channel: numeric loopback Core origin required")
	}
	ip := net.ParseIP(u.Hostname())
	if !validPart(b.Connection.ID) || !validPart(b.CoreID) || !validPart(b.Generation) || !validPart(b.Connection.ApplicationID) || !strings.Contains("|telegram|lark|qq|", "|"+b.Connection.Source+"|") {
		return b, errors.New("channel: invalid connection binding")
	}
	if ip == nil || !ip.IsLoopback() || b.Version != channelwire.Version || b.CoreID == "" || b.Generation == "" || len(b.Token) != 64 || b.Connection.ID == "" || b.Connection.ApplicationID == "" {
		return b, errors.New("channel: unsupported or incomplete Core bootstrap")
	}
	return b, nil
}
func NewClient(b channelwire.Bootstrap) *Client {
	return &Client{Bootstrap: b, HTTP: &http.Client{Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return errors.New("channel: invalid protocol request")
		}
		body = bytes.NewReader(raw)
	}
	r, err := http.NewRequestWithContext(ctx, method, c.Bootstrap.Origin+"/api/channel-plugin/v1/"+path, body)
	if err != nil {
		return errors.New("channel: invalid Core request")
	}
	r.Header.Set("Authorization", "Bearer "+c.Bootstrap.Token)
	r.Header.Set("X-FreeAgent-Core", c.Bootstrap.CoreID)
	r.Header.Set("X-FreeAgent-Generation", c.Bootstrap.Generation)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", c.Bootstrap.Origin)
	response, err := c.HTTP.Do(r)
	if err != nil {
		return errors.New("channel: Core connection interrupted")
	}
	defer response.Body.Close()
	if response.StatusCode == 403 || response.StatusCode == 404 {
		return ErrCoreUnavailable
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("channel: Core did not accept this protocol request")
	}
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 65536))
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (48<<20)+1))
	if err != nil || len(raw) > 48<<20 || json.Unmarshal(raw, output) != nil {
		return errors.New("channel: invalid bounded Core response")
	}
	return nil
}
func (c *Client) Credential(ctx context.Context) (channelwire.Credential, error) {
	var out channelwire.Credential
	err := c.request(ctx, http.MethodGet, "credential", nil, &out)
	return out, err
}
func (c *Client) Inbound(ctx context.Context, event channelwire.Event) error {
	return c.request(ctx, http.MethodPost, "inbound", event, nil)
}
func (c *Client) Outbox(ctx context.Context) ([]channelwire.Operation, error) {
	var out []channelwire.Operation
	err := c.request(ctx, http.MethodGet, "outbox", nil, &out)
	return out, err
}
func (c *Client) Receipt(ctx context.Context, r channelwire.Receipt) error {
	return c.request(ctx, http.MethodPost, "receipt", r, nil)
}

type cursor struct {
	Cutover     string `json:"cutover,omitempty"`
	DropState   string `json:"drop_state,omitempty"`
	Version     int    `json:"version"`
	Core        string `json:"core"`
	Application string `json:"application"`
	Last        int64  `json:"last"`
}

func loadCursor(path string, b channelwire.Bootstrap) (cursor, error) {
	c := cursor{Version: 1, Core: b.CoreID, Application: b.Connection.ApplicationID}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, errors.New("channel: cursor unavailable")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(raw) > 4096 {
		return c, errors.New("channel: cursor unavailable")
	}
	if json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Core != b.CoreID || c.Application != b.Connection.ApplicationID || c.Last < 0 {
		return c, errors.New("channel: cursor belongs to another Core or application")
	}
	return c, nil
}
func saveCursor(path string, c cursor) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cursor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	_, err = f.Write(mustJSON(c))
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	// Windows Rename cannot replace existing targets. Keep cursor publishing
	// behind the platform's atomic replace primitive.
	return replaceCursor(name, path)
}
func mustJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type Sender func(context.Context, channelwire.Operation) channelwire.Receipt

func transportOutbox(ctx context.Context, c *Client, send Sender) error {
	for ctx.Err() == nil {
		ops, err := c.Outbox(ctx)
		if errors.Is(err, ErrCoreUnavailable) {
			return err
		}
		if err != nil {
			if !wait(ctx, time.Second) {
				break
			}
			continue
		}
		for _, op := range ops {
			if op.Target.Source != c.Bootstrap.Connection.Source || op.Target.ApplicationID != c.Bootstrap.Connection.ApplicationID {
				return errors.New("channel: Core offered an operation outside this connection")
			}
			r := send(ctx, op)
			r.ID, r.Dispatch = op.ID, op.Dispatch
			// Retry the receipt, never repeat the platform operation to obtain it.
			for n := 0; n < 3; n++ {
				err = c.Receipt(ctx, r)
				if err == nil {
					break
				}
				if errors.Is(err, ErrCoreUnavailable) {
					return err
				}
				if !wait(ctx, time.Second) {
					return ctx.Err()
				}
			}
		}
		if !wait(ctx, 250*time.Millisecond) {
			break
		}
	}
	return ctx.Err()
}

func Run(ctx context.Context, bootstrap channelwire.Bootstrap, stateDir string) error {
	c := NewClient(bootstrap)
	credential, err := c.Credential(ctx)
	if err != nil {
		return err
	}
	lock, err := claimConnection(bootstrap.Connection.Source, bootstrap.Connection.ApplicationID)
	if err != nil {
		return err
	}
	defer lock()
	switch bootstrap.Connection.Source {
	case "telegram":
		return runTelegram(ctx, c, credential, filepath.Join(stateDir, bootstrap.Connection.ID+".cursor.json"))
	case "lark":
		return runLark(ctx, c, credential)
	case "qq":
		return runQQ(ctx, c, credential)
	default:
		return errors.New("channel: unsupported transport")
	}
}

func RunAll(ctx context.Context, paths []string, stateDir string) error {
	if len(paths) == 0 || len(paths) > 16 {
		return errors.New("channel: provide 1-16 explicit bootstrap files")
	}
	var configs []channelwire.Bootstrap
	for _, path := range paths {
		b, err := ReadBootstrap(path)
		if err != nil {
			return err
		}
		if len(configs) > 0 && (configs[0].CoreID != b.CoreID || configs[0].Generation != b.Generation || configs[0].Origin != b.Origin) {
			return errors.New("channel: one process can serve only one Core")
		}
		for _, prior := range configs {
			if prior.Connection.Source == b.Connection.Source && prior.Connection.ApplicationID == b.Connection.ApplicationID {
				return errors.New("channel: duplicate platform account in one process")
			}
		}
		configs = append(configs, b)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsOut := make(chan error, len(configs))
	var wg sync.WaitGroup
	for i, b := range configs {
		dir := stateDir
		if dir == "" {
			dir = filepath.Join(filepath.Dir(paths[i]), "cursors")
		}
		wg.Add(1)
		go func() { defer wg.Done(); errorsOut <- Run(ctx, b, dir) }()
	}
	err := <-errorsOut
	cancel()
	wg.Wait()
	return err
}

func receiptForError(err error) channelwire.Receipt {
	r := channelwire.Receipt{State: "unknown"}
	if err == nil {
		r.State = "sent"
		return r
	}
	var certain interface{ DispatchCertain() bool }
	if errors.As(err, &certain) && certain.DispatchCertain() {
		r.State = "failed"
	}
	var retry interface{ RetryDelay() time.Duration }
	if errors.As(err, &retry) {
		r.RetryAfterMS = retry.RetryDelay().Milliseconds()
	}
	return r
}
func messageID(n int64) string {
	if n <= 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

func validPart(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 512 || strings.ContainsAny(s, "/\\\x00\r\n\t ") {
		return false
	}
	return true
}
