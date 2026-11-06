package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocolHandshakeAndShutdown(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":"a","method":"initialize","params":{"protocol":"freeagent-ext/1"}}
{"jsonrpc":"2.0","id":"b","method":"context.prepare","params":{"invocation":"inv-1"}}
{"jsonrpc":"2.0","id":"c","method":"shutdown"}
`
	var output bytes.Buffer
	if err := serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	seen := map[string]bool{}
	for decoder.More() {
		var reply map[string]any
		if err := decoder.Decode(&reply); err != nil {
			t.Fatal(err)
		}
		seen[reply["id"].(string)] = true
	}
	if !seen["a"] || !seen["b"] || !seen["c"] {
		t.Fatal("responses dropped", seen)
	}
}
