// This is a bounded example, not a production extension SDK. stdout is NDJSON.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
)

type request struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type parameters struct {
	Invocation string `json:"invocation"`
	Protocol   string `json:"protocol"`
	Message    struct {
		Content string `json:"content"`
	} `json:"message"`
}

func serve(parent context.Context, input io.Reader, output io.Writer) error {
	ctx, cancelAll := context.WithCancel(parent)
	defer cancelAll()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	encoder := json.NewEncoder(output)
	var outputMu, activeMu sync.Mutex
	active := map[string]context.CancelFunc{}
	slots := make(chan struct{}, 4)
	var work sync.WaitGroup
	respond := func(id json.RawMessage, result any, code int, message string) {
		reply := map[string]any{"jsonrpc": "2.0", "id": id}
		if code != 0 {
			reply["error"] = map[string]any{"code": code, "message": message}
		} else {
			reply["result"] = result
		}
		outputMu.Lock()
		defer outputMu.Unlock()
		_ = encoder.Encode(reply)
	}
	defer func() { cancelAll(); work.Wait() }()
	for scanner.Scan() {
		var item request
		if json.Unmarshal(scanner.Bytes(), &item) != nil || item.Version != "2.0" {
			respond(json.RawMessage("null"), nil, -32700, "invalid JSON-RPC frame")
			continue
		}
		var params parameters
		if len(item.Params) > 0 && string(item.Params) != "null" && json.Unmarshal(item.Params, &params) != nil {
			respond(item.ID, nil, -32602, "invalid parameters")
			continue
		}
		if item.Method == "$/cancel" {
			activeMu.Lock()
			if cancel := active[params.Invocation]; cancel != nil {
				cancel()
			}
			activeMu.Unlock()
			continue
		}
		if len(item.ID) == 0 {
			continue
		}
		if item.Method == "initialize" {
			if params.Protocol != "freeagent-ext/1" {
				respond(item.ID, nil, -32602, "unsupported protocol")
			} else {
				respond(item.ID, map[string]any{"id": "com.example.go-context", "version": "0.1.0", "provides": []string{"context"}, "events": []string{}, "workflows": []string{}}, 0, "")
			}
			continue
		}
		if item.Method == "shutdown" {
			respond(item.ID, map[string]any{"ok": true}, 0, "")
			return nil
		}
		if item.Method != "context.prepare" || params.Invocation == "" {
			respond(item.ID, nil, -32601, "method unavailable")
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			respond(item.ID, nil, -32000, "extension busy")
			continue
		}
		invocation, cancel := context.WithCancel(ctx)
		activeMu.Lock()
		if active[params.Invocation] != nil {
			activeMu.Unlock()
			cancel()
			<-slots
			respond(item.ID, nil, -32602, "duplicate active invocation")
			continue
		}
		active[params.Invocation] = cancel
		activeMu.Unlock()
		work.Add(1)
		go func(item request, params parameters) {
			defer work.Done()
			defer func() { activeMu.Lock(); delete(active, params.Invocation); activeMu.Unlock(); cancel(); <-slots }()
			if invocation.Err() != nil {
				respond(item.ID, nil, -32800, "cancelled")
				return
			}
			text := "GO_CONTEXT_READY: source data only; no tool or permission changes."
			respond(item.ID, map[string]any{"blocks": []any{map[string]any{"id": "go-example", "text": text, "placement": "tail", "label": "Go example", "priority": 10}}}, 0, "")
		}(item, params)
	}
	if err := scanner.Err(); err != nil {
		return errors.New("stdio frame exceeded limit or input failed")
	}
	return nil
}
func main() {
	if serve(context.Background(), os.Stdin, os.Stdout) != nil {
		_, _ = os.Stderr.WriteString("Go extension transport failed.\n")
		os.Exit(1)
	}
}
