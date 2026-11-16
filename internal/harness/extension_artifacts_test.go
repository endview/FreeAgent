package harness

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtensionArtifactsScopeValidationAndNoModelFallback(t *testing.T) {
	service, _, _ := testHarness(t, EchoModel{})
	chat := conversation(t, service)
	tasks := NewTaskService(service)
	result, err := tasks.Submit(t.Context(), chat, "artifact-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	run := result.Run
	image := base64.StdEncoding.EncodeToString(testPNG(t))
	refs, err := service.extensionArtifacts(t.Context(), run, "com.example.artifact", []extensionToolArtifact{{Name: "test.png", Base64: image, Image: true}})
	if err != nil || len(refs) != 1 || refs[0].Source != "tool_export" || refs[0].Public {
		t.Fatal("no-vision artifact gained image authority", refs, err)
	}
	for _, bad := range []extensionToolArtifact{{Name: "bad.png", Base64: "broken", Image: true}, {Name: "bad.png", Base64: base64.StdEncoding.EncodeToString([]byte("not an image")), Image: true}} {
		if _, err := service.extensionArtifacts(t.Context(), run, "com.example.artifact", []extensionToolArtifact{bad}); err == nil {
			t.Fatal("invalid artifact accepted")
		}
	}
	scope, err := service.extensionToolScope(t.Context(), run)
	if err != nil || !scope.Local || scope.ConversationID != chat {
		t.Fatal("local scope unavailable", scope, err)
	}
	if _, err = service.Store.db.ExecContext(t.Context(), `INSERT INTO fa.meta(key,value) VALUES($1,$2)`, "run/remote/"+run.ID, `{"id":"public-test"}`); err != nil {
		t.Fatal(err)
	}
	scope, err = service.extensionToolScope(t.Context(), run)
	if err != nil || scope.Local {
		t.Fatal("persisted remote entry gained local authority", scope, err)
	}
	service.extensions = newExtensionHost(service.Store.DataDir(), service.Config.Workspace, service.Config, nil)
	defer service.extensions.close()
	service.extensionToolDefs = []extensionRuntimeTool{{LocalOnly: true, Definition: ToolDefinition{Name: "local_fixture", Effect: EffectRead}}}
	runner := service.extensionToolRunner(service.Tools, run.ID)
	for _, d := range runner.Definitions() {
		if d.Name == "local_fixture" {
			t.Fatal("remote Run exposed local-only tools")
		}
	}
	if _, err := runner.Prepare(t.Context(), makeCall("denied", "local_fixture", map[string]any{})); err != ErrPermissionDenied {
		t.Fatal("remote Run dispatched local-only tool", err)
	}
}

func TestToolObservationImagesKeepOnlyLatestWithoutChangingStoredRecords(t *testing.T) {
	image := AttachmentRef{ID: strings.Repeat("a", 32), Source: "tool_image", Name: "old.png"}
	newImage := image
	newImage.ID = strings.Repeat("b", 32)
	newImage.Name = "new.png"
	messages := []Message{{RunID: "current", ModelMessage: ModelMessage{Role: "assistant", ToolCalls: []ToolCall{makeCall("a", "observe", map[string]any{})}}}, {RunID: "current", ModelMessage: ModelMessage{Role: "tool", ToolCallID: "a", Attachments: []AttachmentRef{image}}}, {RunID: "current", ModelMessage: ModelMessage{Role: "assistant", ToolCalls: []ToolCall{makeCall("b", "observe", map[string]any{})}}}, {RunID: "current", ModelMessage: ModelMessage{Role: "tool", ToolCallID: "b", Attachments: []AttachmentRef{newImage}}}}
	before := string(mustJSON(messages))
	projected, err := projectContextMessages(messages, "", "current", true)
	if err != nil || len(projected) != 4 || len(projected[1].Attachments) != 0 || len(projected[3].Attachments) != 1 || string(mustJSON(messages)) != before {
		t.Fatal("image projection changed records or lost protocol", err)
	}
	if !strings.Contains(projected[1].Content, image.ID) {
		t.Fatal("old image reference lost")
	}
}

func TestExtensionImageRPCResultReachesNextModelRequest(t *testing.T) {
	program, err := exec.LookPath("python")
	if err != nil {
		t.Skip("Python test runtime unavailable")
	}
	service, _, workspace := testHarness(t, EchoModel{})
	id := "com.example.images"
	root := filepath.Join(service.Store.DataDir(), "extensions", id)
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	repo, _ := filepath.Abs(filepath.Join("..", ".."))
	sdk, _ := json.Marshal(filepath.Join(repo, "extension-kit", "sdk", "python"))
	encoded := base64.StdEncoding.EncodeToString(testPNG(t))
	source := "import sys\nsys.path.insert(0," + string(sdk) + ")\nfrom freeagent_ext import Extension\next=Extension('" + id + "')\n@ext.tool('image',schema={'type':'object'},read_only=True)\nasync def image(ctx,args):\n return {'content':'Untrusted test image','artifacts':[{'name':'test.png','base64':'" + encoded + "','image':True}]}\nraise SystemExit(ext.serve())\n"
	if err = os.WriteFile(filepath.Join(root, "main.py"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"protocol": extensionProtocol, "id": id, "version": "0.1.0", "name": "Image fixture", "description": "Image artifact contract", "run": map[string]any{"command": program, "args": []string{"main.py"}}, "tools": true}
	if err = os.WriteFile(filepath.Join(root, "extension.json"), mustJSON(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	model := service.Config.Models["default"]
	model.Capabilities.Vision = channelPreviewVision()
	service.Config.Models["default"] = model
	service.Config.Extensions.Enabled = map[string]bool{id: true}
	service.capabilities = &CapabilitySelection{Extensions: []string{id}}
	service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
	t.Cleanup(func() { _ = service.extensions.close() })
	step := 0
	service.Model = modelFunc(func(_ context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
		step++
		if step == 1 {
			for _, tool := range r.Tools {
				if strings.HasPrefix(tool.Name, "ext_") {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("image", tool.Name, map[string]any{})}}, nil
				}
			}
			t.Fatal("extension image tool missing")
		}
		for _, m := range r.Messages {
			if m.ToolCallID == "image" && len(m.Images) == 1 && m.Images[0].Base64 == encoded && len(m.Attachments) == 1 {
				return ModelResponse{Content: "IMAGE_CORE_OK"}, nil
			}
		}
		t.Fatal("RPC artifact lost before model dispatch")
		return ModelResponse{}, nil
	})
	outcome, err := NewTaskService(service).Submit(t.Context(), conversation(t, service), "rpc-image", "Observe test image")
	if err != nil || outcome.Answer != "IMAGE_CORE_OK" {
		t.Fatal("image RPC workflow failed", err, outcome.Run.Status)
	}
}

func TestAutomationExtensionsLiveCoreImageRoundTrip(t *testing.T) {
	root := os.Getenv("FREEAGENT_AUTOMATION_PACKAGES")
	if root == "" {
		t.Skip("build packages and explicitly select real automation acceptance")
	}
	for _, kind := range []string{"browser", "computer-use"} {
		t.Run(kind, func(t *testing.T) {
			source := filepath.Join(root, "cc.endview."+kind)
			manifestBytes, err := os.ReadFile(filepath.Join(source, "extension.json"))
			if err != nil {
				t.Skip("platform package unavailable")
			}
			var manifest ExtensionManifest
			if json.Unmarshal(manifestBytes, &manifest) != nil {
				t.Fatal("bad manifest")
			}
			service, _, workspace := testHarness(t, EchoModel{})
			id := manifest.ID
			dest := filepath.Join(service.Store.DataDir(), "extensions", id)
			if err = os.MkdirAll(dest, 0700); err != nil {
				t.Fatal(err)
			}
			entries, _ := os.ReadDir(source)
			for _, entry := range entries {
				if !entry.IsDir() {
					raw, e := os.ReadFile(filepath.Join(source, entry.Name()))
					if e != nil {
						t.Fatal(e)
					}
					if e = os.WriteFile(filepath.Join(dest, entry.Name()), raw, 0700); e != nil {
						t.Fatal(e)
					}
				}
			}
			config := map[string]any{}
			fixtureURL := ""
			if kind == "browser" {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					_, _ = w.Write([]byte("<title>Core browser test</title><h1>AUTOMATION_PAGE_OK</h1>"))
				}))
				defer server.Close()
				fixtureURL = server.URL
				config["allowed_origins"] = []string{fixtureURL}
			}
			if kind == "computer-use" {
				binary := filepath.Join(source, filepath.Base(manifest.Run.Command))
				fixture := exec.Command(binary, "--fixture")
				if err = fixture.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = fixture.Process.Kill(); _ = fixture.Wait() })
				config["allowed_executables"] = []string{binary}
			}
			service.Config.Extensions.Enabled = map[string]bool{id: true}
			service.Config.Extensions.Settings = map[string]map[string]any{id: config}
			service.Config.MaxContextBytes = 2 << 20
			service.Config.ContextByteLimitMode = ""
			model := service.Config.Models["default"]
			model.Capabilities.Vision = VisionCapabilities{Enabled: true, PNG: true, JPEG: true, MaxImageBytes: 4 << 20, MaxSide: 4096, MaxImages: 4, MaxRequestBytes: 8 << 20}
			service.Config.Models["default"] = model
			service.capabilities = &CapabilitySelection{Extensions: []string{id}}
			service.extensions = newExtensionHost(service.Store.DataDir(), workspace, service.Config, nil)
			t.Cleanup(func() { _ = service.extensions.close() })
			step := 0
			var tabID string
			if kind == "browser" {
				if err = NewTaskService(service).SetSpacePermission(t.Context(), LocalSpaceID, PermissionFull, true); err != nil {
					t.Fatal(err)
				}
			}
			service.Model = modelFunc(func(ctx context.Context, r ModelRequest, _ func(string)) (ModelResponse, error) {
				step++
				alias, actAlias := "", ""
				for _, tool := range r.Tools {
					if strings.Contains(tool.Description, "/observe:") {
						alias = tool.Name
					}
					if strings.Contains(tool.Description, "/act:") {
						actAlias = tool.Name
					}
				}
				if alias == "" {
					t.Fatal("actual extension tools were not declared to model")
				}
				if step == 1 {
					return ModelResponse{ToolCalls: []ToolCall{makeCall("automation-status", alias, map[string]any{"action": "status"})}}, nil
				}
				if step == 2 {
					found := false
					for _, m := range r.Messages {
						found = found || m.ToolCallID == "automation-status" && strings.Contains(m.Content, "state")
					}
					if !found {
						t.Fatal("extension result did not reach Core")
					}
					if kind == "browser" {
						return ModelResponse{ToolCalls: []ToolCall{makeCall("open", actAlias, map[string]any{"action": "open", "url": fixtureURL})}}, nil
					}
					return ModelResponse{Content: "AUTOMATION_CORE_OK"}, nil
				}
				if kind == "browser" {
					if step == 3 {
						for _, m := range r.Messages {
							if m.ToolCallID == "open" {
								var opened struct{ Tab string }
								_ = json.Unmarshal([]byte(m.Content), &opened)
								tabID = opened.Tab
							}
						}
						if tabID == "" {
							t.Fatalf("Core browser navigation did not return a tab: %s", string(mustJSON(r.Messages)))
						}
						return ModelResponse{ToolCalls: []ToolCall{makeCall("page", alias, map[string]any{"action": "observe", "tab": tabID})}}, nil
					}
					if step == 4 {
						return ModelResponse{ToolCalls: []ToolCall{makeCall("screenshot", alias, map[string]any{"action": "screenshot", "tab": tabID})}}, nil
					}
					if step == 5 {
						found := false
						for _, m := range r.Messages {
							found = found || m.ToolCallID == "screenshot" && len(m.Images) == 1 && len(m.Attachments) == 1
						}
						if !found {
							t.Fatal("real browser screenshot did not traverse RPC, persistence and next model request")
						}
						return ModelResponse{Content: "AUTOMATION_CORE_OK"}, nil
					}
				}
				return ModelResponse{Content: "continued"}, nil
			})
			outcome, err := NewTaskService(service).Submit(t.Context(), conversation(t, service), "automation-core", "Read automation status")
			wantSteps := 2
			if kind == "browser" {
				wantSteps = 5
			}
			if err != nil || outcome.Answer != "AUTOMATION_CORE_OK" || step != wantSteps {
				t.Fatalf("Core roundtrip status=%s err=%v answer=%s", outcome.Run.Status, err, outcome.Answer)
			}
			scope, err := service.extensionToolScope(t.Context(), outcome.Run)
			if err != nil || !scope.Local || !scope.Vision {
				t.Fatal("trusted scope mismatch", scope, err)
			}
			// Validate the generic image publication and every supported wire adapter
			// against an actual Core Run, independently of the extension's app strategy.
			refs, err := service.extensionArtifacts(t.Context(), outcome.Run, id, []extensionToolArtifact{{Name: "fixture.png", Base64: base64.StdEncoding.EncodeToString(testPNG(t)), Image: true}})
			if err != nil {
				t.Fatal(err)
			}
			r := ModelRequest{Messages: []ModelMessage{{Role: "assistant", ToolCalls: []ToolCall{makeCall("preview-frame", "observe", map[string]any{}), makeCall("following-result", "observe", map[string]any{})}}, {Role: "tool", ToolCallID: "preview-frame", Content: "image", Attachments: refs}, {Role: "tool", ToolCallID: "following-result", Content: "following"}}}
			r, err = service.prepareAttachmentRequest(t.Context(), outcome.Run, model, r)
			if err != nil || len(r.Messages[1].Images) != 1 {
				t.Fatal("published image did not reach prepared request", err)
			}
			for _, protocol := range []string{"chat", "responses", "anthropic"} {
				assertChannelPreviewProviderWire(t, protocol, r, r.Messages[1].Images[0].Base64)
			}
		})
	}
}
