package channels

import (
	"errors"
	"html"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/channelwire"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const TelegramFormatVersion = "markdown-entities/1"

// RenderTelegramText is a pure channel codec. Core can call it before durable
// enqueue, so each exact platform request has its own ordered delivery receipt.
// It opens no platform connection and never modifies the original answer.
func RenderTelegramText(input string, plain bool) ([]channelwire.TextPart, error) {
	if !utf8.ValidString(input) || len(input) > 256<<10 {
		return nil, errors.New("telegram: invalid bounded text")
	}
	part := channelwire.TextPart{Text: input}
	if !plain {
		source := []byte(input)
		md := goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough))
		r := telegramMarkdown{source: source}
		r.node(md.Parser().Parse(text.NewReader(source)), 0)
		if !r.overflow {
			part.Text = strings.TrimRight(r.output.String(), "\n")
			size := channelwire.UTF16Len(part.Text)
			for _, entity := range r.entities {
				entity.Length = min(entity.Length, size-entity.Offset)
				if entity.Length > 0 {
					part.Entities = append(part.Entities, entity)
				}
			}
			sort.SliceStable(part.Entities, func(i, j int) bool {
				if part.Entities[i].Offset != part.Entities[j].Offset {
					return part.Entities[i].Offset < part.Entities[j].Offset
				}
				return part.Entities[i].Length > part.Entities[j].Length
			})
		}
	}
	return channelwire.SplitTextParts(part, 4000), nil
}

type telegramMarkdown struct {
	source   []byte
	output   strings.Builder
	units    int
	entities []channelwire.TextEntity
	nodes    int
	overflow bool
}

func (r *telegramMarkdown) write(s string) {
	r.output.WriteString(s)
	r.units += channelwire.UTF16Len(s)
}
func (r *telegramMarkdown) newline() {
	if r.output.Len() > 0 && !strings.HasSuffix(r.output.String(), "\n") {
		r.write("\n")
	}
}
func (r *telegramMarkdown) children(node ast.Node, depth int) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		r.node(child, depth+1)
	}
}
func (r *telegramMarkdown) span(kind string, start int, target, language string) {
	if r.units <= start || len(r.entities) >= 1024 {
		return
	}
	// Telegram code/pre cannot be nested in another formatting entity.
	for _, e := range r.entities {
		if (e.Type == "code" || e.Type == "pre") && e.Offset < r.units && e.Offset+e.Length > start {
			return
		}
	}
	r.entities = append(r.entities, channelwire.TextEntity{Type: kind, Offset: start, Length: r.units - start, URL: target, Language: language})
}
func (r *telegramMarkdown) lines(node ast.Node) {
	for i := 0; i < node.Lines().Len(); i++ {
		line := node.Lines().At(i)
		r.write(string(line.Value(r.source)))
	}
}
func (r *telegramMarkdown) node(node ast.Node, depth int) {
	r.nodes++
	if depth > 128 || r.nodes > 32768 {
		r.overflow = true
		return
	}
	start := r.units
	switch n := node.(type) {
	case *ast.Text:
		value := n.Segment.Value(r.source)
		if !n.IsRaw() {
			value = util.UnescapePunctuations(value)
			r.write(html.UnescapeString(string(value)))
		} else {
			r.write(string(value))
		}
		if n.SoftLineBreak() || n.HardLineBreak() {
			r.write("\n")
		}
	case *ast.String:
		r.write(string(n.Value))
	case *ast.Emphasis:
		r.children(n, depth)
		kind := "italic"
		if n.Level == 2 {
			kind = "bold"
		}
		r.span(kind, start, "", "")
	case *extast.Strikethrough:
		r.children(n, depth)
		r.span("strikethrough", start, "", "")
	case *ast.CodeSpan:
		var code strings.Builder
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			if t, ok := child.(*ast.Text); ok {
				code.Write(t.Segment.Value(r.source))
			}
		}
		value := strings.ReplaceAll(code.String(), "\n", " ")
		if len(value) >= 2 && strings.HasPrefix(value, " ") && strings.HasSuffix(value, " ") && strings.TrimSpace(value) != "" {
			value = value[1 : len(value)-1]
		}
		r.write(value)
		r.span("code", start, "", "")
	case *ast.FencedCodeBlock:
		r.newline()
		start = r.units
		r.lines(n)
		language := string(n.Language(r.source))
		if len(language) > 64 || strings.ContainsAny(language, "\x00\r\n") {
			language = ""
		}
		r.span("pre", start, "", language)
		r.newline()
	case *ast.CodeBlock:
		r.newline()
		start = r.units
		r.lines(n)
		r.span("pre", start, "", "")
		r.newline()
	case *ast.Heading:
		r.newline()
		start = r.units
		r.children(n, depth)
		r.span("bold", start, "", "")
		r.write("\n")
	case *ast.Paragraph, *ast.TextBlock:
		r.children(n, depth)
		r.newline()
		if _, inList := node.Parent().(*ast.ListItem); !inList && node.NextSibling() != nil {
			r.write("\n")
		}
	case *ast.List:
		r.newline()
		r.children(n, depth)
		r.newline()
	case *ast.ListItem:
		r.newline()
		indent := 0
		for p := n.Parent().Parent(); p != nil; p = p.Parent() {
			if _, ok := p.(*ast.List); ok {
				indent++
			}
		}
		r.write(strings.Repeat("  ", min(indent, 16)))
		if list, ok := n.Parent().(*ast.List); ok && list.IsOrdered() {
			number := list.Start
			for p := n.PreviousSibling(); p != nil; p = p.PreviousSibling() {
				number++
			}
			r.write(strconv.Itoa(number) + ". ")
		} else {
			r.write("• ")
		}
		r.children(n, depth)
		r.newline()
	case *ast.Blockquote:
		r.newline()
		start = r.units
		r.children(n, depth)
		r.span("blockquote", start, "", "")
		r.newline()
	case *ast.Link:
		r.children(n, depth)
		if target := telegramSafeURL(string(n.Destination)); target != "" {
			r.span("text_link", start, target, "")
		}
	case *ast.Image:
		r.children(n, depth)
		if target := telegramSafeURL(string(n.Destination)); target != "" {
			r.write(" (" + target + ")")
		}
	case *ast.AutoLink:
		r.write(string(n.Label(r.source)))
		if target := telegramSafeURL(string(n.URL(r.source))); target != "" {
			r.span("text_link", start, target, "")
		}
	case *ast.RawHTML:
		for i := 0; i < n.Segments.Len(); i++ {
			line := n.Segments.At(i)
			r.write(string(line.Value(r.source)))
		}
	case *ast.HTMLBlock:
		r.lines(n)
		if n.HasClosure() {
			r.write(string(n.ClosureLine.Value(r.source)))
		}
	case *ast.ThematicBreak:
		r.newline()
		r.write("—\n")
	case *extast.TableHeader, *extast.TableRow:
		r.newline()
		for child := node.FirstChild(); child != nil; child = child.NextSibling() {
			if child.PreviousSibling() != nil {
				r.write(" | ")
			}
			r.node(child, depth+1)
		}
		r.newline()
	default:
		r.children(node, depth)
	}
}

func telegramSafeURL(value string) string {
	if len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Hostname() == "" {
			return ""
		}
	case "mailto":
		if u.Opaque == "" {
			return ""
		}
	default:
		return ""
	}
	return value
}
