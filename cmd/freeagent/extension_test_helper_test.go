package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// runExtensionCLITestHelper is only used as a child process by CLI extension
// tests. It keeps the test binary self-contained and does not participate in
// the production command.
func runExtensionCLITestHelper() int {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024), 16<<20)
	for scanner.Scan() {
		message, err := jsonrpc.DecodeMessage(scanner.Bytes())
		if err != nil {
			return 2
		}
		request, ok := message.(*jsonrpc.Request)
		if !ok || !request.ID.IsValid() {
			continue
		}
		var params map[string]any
		_ = json.Unmarshal(request.Params, &params)
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{
				"id": "com.example.cli", "version": "1.0.0",
				"provides": []string{"context"},
			}
		case "context.prepare":
			result = map[string]any{"blocks": []map[string]any{
				{"id": "sample", "text": "CLI test extension", "placement": "tail"},
			}}
		case "shutdown":
			result = nil
		default:
			result = map[string]any{}
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return 3
		}
		response := &jsonrpc.Response{ID: request.ID, Result: raw}
		encoded, err := jsonrpc.EncodeMessage(response)
		if err != nil {
			return 4
		}
		if _, err := fmt.Fprintf(os.Stdout, "%s\n", encoded); err != nil {
			return 5
		}
	}
	return 0
}
