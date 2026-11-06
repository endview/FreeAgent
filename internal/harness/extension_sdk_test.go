package harness

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the checked-in SDKs over real stdio and the normal Core tool runner.
// Discovery alone would not catch a method name or result-shape mismatch.
func TestExtensionSDKToolsUseCoreDispatch(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"python", "node"} {
		t.Run(language, func(t *testing.T) {
			program, err := exec.LookPath(language)
			if err != nil {
				t.Skipf("%s runtime unavailable", language)
			}
			program, err = filepath.Abs(program)
			if err != nil {
				t.Fatal(err)
			}
			service, _, workspace := testHarness(t, EchoModel{})
			id := "com.example.sdk"
			root := filepath.Join(service.Store.DataDir(), "extensions", id)
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			var source, entry string
			if language == "python" {
				entry = "main.py"
				sdk, _ := json.Marshal(filepath.Join(repo, "extension-kit", "sdk", "python"))
				source = "import sys\nsys.path.insert(0, " + string(sdk) + ")\nfrom freeagent_ext import Extension\next = Extension('com.example.sdk')\n@ext.tool('echo', schema={'type':'object'}, read_only=True)\nasync def echo(ctx, args):\n    return {'content': args['text'], 'is_error': False}\nraise SystemExit(ext.serve())\n"
			} else {
				entry = "main.mjs"
				// A file URL also handles spaces, non-ASCII and Windows drive paths.
				source = "import {pathToFileURL} from 'node:url';\nconst {Extension} = await import(pathToFileURL(" + string(mustJSON(filepath.Join(repo, "extension-kit", "sdk", "node", "freeagent-ext.mjs"))) + ").href);\nconst ext = new Extension('com.example.sdk');\next.tool('echo', {schema:{type:'object'}, readOnly:true}, (ctx,args) => ({content:args.text,is_error:false}));\nawait ext.serve();\n"
			}
			if err := os.WriteFile(filepath.Join(root, entry), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			manifest := map[string]any{"protocol": extensionProtocol, "id": id, "version": "0.1.0", "name": "SDK tool", "description": "contract test",
				"run": map[string]any{"command": program, "args": []string{entry}}, "tools": true}
			if err := os.WriteFile(filepath.Join(root, "extension.json"), mustJSON(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			service.Config.Extensions.Enabled = map[string]bool{id: true}
			service.capabilities = &CapabilitySelection{Extensions: []string{id}}
			service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
			t.Cleanup(func() { _ = service.extensions.close() })
			calls := 0
			service.Model = modelFunc(func(_ context.Context, request ModelRequest, _ func(string)) (ModelResponse, error) {
				calls++
				if calls == 1 {
					for _, tool := range request.Tools {
						if strings.HasPrefix(tool.Name, "ext_") {
							return ModelResponse{ToolCalls: []ToolCall{{ID: "sdk-echo", Name: tool.Name, Arguments: mustJSON(map[string]string{"text": "SDK_CORE_OK"})}}}, nil
						}
					}
					t.Fatal("extension tool was not advertised")
				}
				for _, message := range request.Messages {
					if message.Role == "tool" && message.Content == "SDK_CORE_OK" {
						return ModelResponse{Content: "SDK_CORE_OK"}, nil
					}
				}
				t.Fatal("Core did not receive the SDK tool result")
				return ModelResponse{}, nil
			})
			result, err := NewTaskService(service).Submit(t.Context(), conversation(t, service), "sdk-tool", "Use the SDK echo tool")
			if err != nil || result.Run.Status != "completed" || result.Answer != "SDK_CORE_OK" {
				t.Fatalf("SDK tool dispatch: status=%s answer=%q err=%v", result.Run.Status, result.Answer, err)
			}
		})
	}
}
