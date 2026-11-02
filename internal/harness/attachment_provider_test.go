package harness

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVisionProtocolEncodingAndUnsupportedRefusal(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			calls := 0
			imageRaw := testPNG(t)
			encoded := base64.StdEncoding.EncodeToString(imageRaw)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				calls++
				var wire map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
				}
				body := string(mustJSON(wire))
				if !strings.Contains(body, encoded) {
					t.Error("wire lost image bytes")
				}
				switch protocol {
				case "chat":
					if !strings.Contains(body, `"type":"image_url"`) || !strings.Contains(body, "data:image/png;base64,") {
						t.Error(body)
					}
					fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"seen"},"finish_reason":"stop"}]}`)
				case "responses":
					if !strings.Contains(body, `"type":"input_image"`) || !strings.Contains(body, `"type":"input_text"`) {
						t.Error(body)
					}
					fmt.Fprint(w, `{"id":"resp_1","object":"response","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"seen"}]}]}`)
				case "anthropic":
					if !strings.Contains(body, `"media_type":"image/png"`) || !strings.Contains(body, `"type":"base64"`) {
						t.Error(body)
					}
					fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"fixture","content":[{"type":"text","text":"seen"}],"stop_reason":"end_turn"}`)
				}
			}))
			defer server.Close()
			caps := ModelCapabilities{Streaming: "none", Vision: VisionCapabilities{true, true, true, 1 << 20, 8000, 8, 4 << 20}}
			provider := "openai-compatible"
			if protocol == "anthropic" {
				provider = "anthropic"
			} else {
				caps.Protocol = protocol
			}
			config := ModelConfig{Provider: provider, Model: "fixture", Capabilities: caps}
			key := ResolvedKey{BaseURL: server.URL}
			var client ModelClient
			var err error
			switch protocol {
			case "chat":
				client, err = NewOpenAICompatibleClient(config, key, server.Client())
			case "responses":
				client, err = NewResponsesClient(config, key, server.Client())
			case "anthropic":
				client, err = NewAnthropicClient(config, key, server.Client())
			}
			if err != nil {
				t.Fatal(err)
			}
			request := ModelRequest{ModelID: "fixture", MaxOutputTokens: 64, Messages: []ModelMessage{{Role: "system", Content: "system"}, {Role: "user", Content: "look", Images: []ModelImage{{"image", "image/png", encoded, 4, 3}}}}}
			response, err := client.Generate(context.Background(), request, nil)
			if err != nil || response.Content != "seen" || calls != 1 {
				t.Fatalf("%+v %v calls=%d", response, err, calls)
			}
			config.Capabilities.Vision = VisionCapabilities{}
			if err := validateModelRequest(config, request); err == nil {
				t.Fatal("undeclared vision accepted")
			}
		})
	}
}

func TestAnthropicHardImageBoundsCannotBeWidened(t *testing.T) {
	config := ModelConfig{Provider: "anthropic", Capabilities: ModelCapabilities{Vision: VisionCapabilities{true, true, true, 32 << 20, 16000, 32, 64 << 20}}}
	image := ModelImage{MIME: "image/png", Base64: "AAAA", Width: 8001, Height: 1}
	request := ModelRequest{Messages: []ModelMessage{{Role: "user", Images: []ModelImage{image}}}}
	if err := validateModelRequest(config, request); err == nil {
		t.Fatal("declared limits widened native maximum side")
	}
	image.Width = 2001
	request.Messages[0].Images = nil
	for range 21 {
		request.Messages[0].Images = append(request.Messages[0].Images, image)
	}
	if err := validateModelRequest(config, request); err == nil {
		t.Fatal("more than 20 images bypassed stricter side limit")
	}
	request.Messages[0].Images = []ModelImage{{MIME: "image/png", Base64: strings.Repeat("A", 10_000_004), Width: 4, Height: 3}}
	if err := validateModelRequest(config, request); err == nil {
		t.Fatal("declared limits widened native encoded-byte maximum")
	}
}
