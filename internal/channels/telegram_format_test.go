package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/channelwire"
)

func TestTelegramMarkdownPreservesCodeAndConvertsPresentation(t *testing.T) {
	input := "# 标题\n\n**粗体**、*斜体*、~~删除~~，😀 [网站](https://example.com/a_b?q=1&v=2)。\n\n" +
		"`C:\\work\\file_name.txt`\n\n```powershell\n# literal\nWrite-Output '*_中文😀'\n```\n\n" +
		"- 第一项\n- 第二项\n\n| 名称 | 值 |\n| --- | --- |\n| A | 1 |"
	parts, err := RenderTelegramText(input, false)
	if err != nil || len(parts) != 1 {
		t.Fatal(parts, err)
	}
	p := parts[0]
	for _, want := range []string{"标题", "粗体、斜体、删除", "C:\\work\\file_name.txt", "# literal\nWrite-Output '*_中文😀'", "• 第一项", "名称 | 值", "A | 1"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("missing %q in %q", want, p.Text)
		}
	}
	for _, unwanted := range []string{"# 标题", "**粗体**", "~~删除~~", "```"} {
		if strings.Contains(p.Text, unwanted) {
			t.Errorf("unconverted Markdown %q", unwanted)
		}
	}
	kinds := map[string]bool{}
	for _, entity := range p.Entities {
		kinds[entity.Type] = true
	}
	for _, kind := range []string{"bold", "italic", "strikethrough", "code", "pre", "text_link"} {
		if !kinds[kind] {
			t.Error("missing entity", kind)
		}
	}
	if err := validateTelegramEntities(p.Text, p.Entities); err != nil {
		t.Fatal(err)
	}
}

func TestTelegramMarkdownUnicodeSplitsKeepCodeAndGraphemes(t *testing.T) {
	cluster := "👨‍👩‍👧‍👦"
	content := strings.Repeat(cluster, 1200)
	parts, err := RenderTelegramText("```text\n"+content+"\n```", false)
	if err != nil || len(parts) < 3 {
		t.Fatal(len(parts), err)
	}
	var joined strings.Builder
	for _, part := range parts {
		if channelwire.UTF16Len(part.Text) > 4000 || len(part.Entities) != 1 || part.Entities[0].Type != "pre" {
			t.Fatal("invalid split", part.Entities, channelwire.UTF16Len(part.Text))
		}
		if strings.Trim(part.Text, "\n") != "" && !strings.HasPrefix(part.Text, cluster) {
			t.Fatal("split an emoji family")
		}
		if err := validateTelegramEntities(part.Text, part.Entities); err != nil {
			t.Fatal(err)
		}
		joined.WriteString(part.Text)
	}
	if joined.String() != content {
		t.Fatal("code text lost or duplicated")
	}
}

func TestTelegramMarkdownPlainRawAndUnsafeLinks(t *testing.T) {
	input := "literal **text** 😀"
	parts, err := RenderTelegramText(input, true)
	if err != nil || len(parts) != 1 || parts[0].Text != input || len(parts[0].Entities) != 0 {
		t.Fatal(parts, err)
	}
	parts, err = RenderTelegramText("[bad](javascript:alert(1)) <b>literal</b> \\*星号\\* &amp;", false)
	if err != nil || len(parts) != 1 || !strings.Contains(parts[0].Text, "<b>literal</b>") || !strings.Contains(parts[0].Text, "*星号* &") {
		t.Fatal(parts, err)
	}
	for _, e := range parts[0].Entities {
		if e.Type == "text_link" {
			t.Fatal("unsafe link became clickable")
		}
	}
}

func TestTelegramFormattedFallbackRequiresCertainEntityRejection(t *testing.T) {
	for _, test := range []struct {
		name, description string
		status, requests  int
	}{
		{"format", "Bad Request: can't parse entities", 400, 2},
		{"denied", "Forbidden", 403, 1},
		{"bad-target", "Bad Request: chat not found", 400, 1},
		{"unknown", "server error", 500, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var input map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if calls == 1 {
					if len(input["entities"]) == 0 || input["parse_mode"] != nil {
						t.Error("missing native entities")
					}
					w.WriteHeader(test.status)
					_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": test.status, "description": test.description})
					return
				}
				if input["entities"] != nil || string(input["text"]) != `"😀 hello"` {
					t.Error("fallback changed visible content")
				}
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":10,"chat":{"id":90}}}`)
			}))
			defer server.Close()
			api, _ := NewTelegramAPI(server.URL, telegramFixtureToken, server.Client())
			_, fallback, err := api.SendFormatted(t.Context(), 90, 0, "😀 hello", []channelwire.TextEntity{{Type: "bold", Offset: 3, Length: 5}}, nil)
			if calls != test.requests || fallback != (test.requests == 2) || (err == nil) != (test.requests == 2) {
				t.Fatal(calls, fallback, err)
			}
		})
	}
}

func TestTelegramFormattedRejectsSplitSurrogateBeforeDispatch(t *testing.T) {
	api, _ := NewTelegramAPI("http://127.0.0.1:1", telegramFixtureToken, nil)
	if _, _, err := api.SendFormatted(context.Background(), 90, 0, "😀", []channelwire.TextEntity{{Type: "bold", Offset: 1, Length: 1}}, nil); err == nil || strings.Contains(err.Error(), "API failure") {
		t.Fatal("invalid entity reached network", err)
	}
}
