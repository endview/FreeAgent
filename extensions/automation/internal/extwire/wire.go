// Package extwire implements only the local extension transport. It owns no
// model calls, task queue, permissions database, or execution scheduler.
package extwire

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

type Scope struct {
	CoreID         string `json:"core_id"`
	SpaceID        string `json:"space_id"`
	ConversationID string `json:"conversation_id"`
	RunID          string `json:"run_id"`
	Local          bool   `json:"local"`
	Vision         bool   `json:"vision"`
}
type Artifact struct {
	Name  string `json:"name"`
	Data  string `json:"base64"`
	Image bool   `json:"image,omitempty"`
}
type Result struct {
	Content   string     `json:"content"`
	IsError   bool       `json:"is_error,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

func Value(v any) Result       { b, _ := json.Marshal(v); return Result{Content: string(b)} }
func Failure(err error) Result { return Result{Content: err.Error(), IsError: true} }

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"inputSchema"`
	ReadOnly    bool            `json:"readOnly"`
	LocalOnly   bool            `json:"localOnly,omitempty"`
}
type Init struct {
	Protocol     string          `json:"protocol"`
	DataDir      string          `json:"data_dir"`
	Settings     json.RawMessage `json:"settings"`
	Capabilities []string        `json:"capabilities"`
}
type Call struct {
	Invocation string          `json:"invocation"`
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
	Scope      Scope           `json:"scope"`
}
type Extension interface {
	Initialize(Init) error
	Tools() []Tool
	Call(context.Context, Call) (Result, error)
	Close() error
}
type UnknownError struct{ Message string }

func (e *UnknownError) Error() string { return e.Message }
func Serve(id string, extension Extension) error {
	defer extension.Close()
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	var output sync.Mutex
	send := func(v any) { output.Lock(); defer output.Unlock(); _ = json.NewEncoder(os.Stdout).Encode(v) }
	var activeMu sync.Mutex
	active := map[string]context.CancelFunc{}
	var calls sync.WaitGroup
	var serial sync.Mutex
	initialized := false
	for scanner.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.JSONRPC != "2.0" {
			return errors.New("invalid RPC frame")
		}
		if req.Method == "$/cancel" {
			var p struct {
				Invocation string `json:"invocation"`
			}
			_ = json.Unmarshal(req.Params, &p)
			activeMu.Lock()
			if cancel := active[p.Invocation]; cancel != nil {
				cancel()
			}
			activeMu.Unlock()
			continue
		}
		if req.Method == "shutdown" {
			activeMu.Lock()
			for _, c := range active {
				c()
			}
			activeMu.Unlock()
			calls.Wait()
			send(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			return nil
		}
		reply := func(result any, err error) {
			v := map[string]any{"jsonrpc": "2.0", "id": req.ID}
			if err != nil {
				code := -32602
				var u *UnknownError
				if errors.As(err, &u) {
					code = -32006
				}
				v["error"] = map[string]any{"code": code, "message": err.Error()}
			} else {
				v["result"] = result
			}
			send(v)
		}
		switch req.Method {
		case "initialize":
			var p Init
			err := json.Unmarshal(req.Params, &p)
			if err == nil {
				for _, cap := range []string{"tool_scope", "tool_artifacts"} {
					found := false
					for _, c := range p.Capabilities {
						found = found || c == cap
					}
					if !found {
						err = fmt.Errorf("host requires tool_scope and tool_artifacts (FreeAgent v8.9.0-dev.2 or newer)")
					}
				}
			}
			if err == nil && initialized {
				err = errors.New("already initialized")
			}
			if err == nil {
				err = extension.Initialize(p)
				initialized = err == nil
			}
			reply(map[string]any{"id": id, "version": "0.1.0", "provides": []string{"tools"}}, err)
		case "tools/list":
			if !initialized {
				reply(nil, errors.New("not initialized"))
			} else {
				reply(map[string]any{"tools": extension.Tools()}, nil)
			}
		case "tool.call":
			var p Call
			err := json.Unmarshal(req.Params, &p)
			if err != nil || !initialized || p.Invocation == "" || p.Scope.CoreID == "" || p.Scope.SpaceID == "" || p.Scope.ConversationID == "" || p.Scope.RunID == "" {
				reply(nil, errors.New("missing trusted host scope"))
				continue
			}
			ctx, cancel := context.WithCancel(context.Background())
			activeMu.Lock()
			active[p.Invocation] = cancel
			activeMu.Unlock()
			calls.Add(1)
			go func(p Call, reply func(any, error)) {
				defer calls.Done()
				defer cancel()
				defer func() { activeMu.Lock(); delete(active, p.Invocation); activeMu.Unlock() }()
				serial.Lock()
				defer serial.Unlock()
				if ctx.Err() != nil {
					reply(Failure(ctx.Err()), nil)
					return
				}
				value, err := extension.Call(ctx, p)
				reply(value, err)
			}(p, reply)
		default:
			reply(nil, errors.New("method unavailable"))
		}
	}
	activeMu.Lock()
	for _, c := range active {
		c()
	}
	activeMu.Unlock()
	calls.Wait()
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
