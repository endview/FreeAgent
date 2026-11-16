package webaccess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

type Page struct {
	URL       string    `json:"url"`
	Title     string    `json:"title,omitempty"`
	At        time.Time `json:"retrieved_at"`
	Content   string    `json:"content"`
	Truncated bool      `json:"truncated,omitempty"`
}

type FetchError struct {
	Unknown bool
	Denied  bool
}

func (e *FetchError) Error() string {
	if e.Denied {
		return ErrDestinationDenied.Error()
	}
	return "web: read failed or destination denied; no automatic retry"
}

// Fetch reads public, unauthenticated text only. Each redirect goes through a
// fresh dial-time DNS check. It never inherits the search connection's client.
func Fetch(ctx context.Context, rawURL string, config Config) (Page, error) {
	return fetchWithClient(ctx, rawURL, config, NewHTTPClient(time.Duration(config.TimeoutSeconds)*time.Second, false))
}

func fetchWithClient(ctx context.Context, rawURL string, config Config, client *http.Client) (Page, error) {
	p := Page{At: time.Now().UTC()}
	if err := config.Validate(); err != nil {
		return p, err
	}
	u, err := PublicURL(rawURL)
	if err != nil {
		return p, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancel()
	for hop := 0; hop <= 3; hop++ {
		p.URL = u.String()
		req, err := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
		if err != nil {
			return p, errors.New("web: invalid request")
		}
		req.Header.Set("Accept", "text/html, text/plain, text/markdown, application/json")
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("User-Agent", "FreeAgent-Web/1")
		var wrote atomic.Bool
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{WroteHeaders: func() { wrote.Store(true) }}))
		resp, err := client.Do(req)
		if err != nil {
			return p, &FetchError{Unknown: wrote.Load(), Denied: errors.Is(err, ErrDestinationDenied)}
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			resp.Body.Close()
			next, e := resp.Location()
			if e != nil || hop == 3 {
				return p, errors.New("web: redirect invalid or exceeds 3 hops")
			}
			u, e = PublicURL(next.String())
			if e != nil {
				return p, e
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return p, &HTTPError{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
		}
		media, params, e := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if e != nil || (media != "text/html" && media != "text/plain" && media != "text/markdown" && media != "application/json") || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") && !strings.EqualFold(params["charset"], "us-ascii") || resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
			resp.Body.Close()
			return p, errors.New("web: only uncompressed UTF-8 HTML/text/JSON is supported; use an explicit attachment reader for other formats")
		}
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
		resp.Body.Close()
		if e != nil || len(raw) > 2<<20 || !utf8.Valid(raw) || strings.ContainsRune(string(raw), 0) {
			return p, errors.New("web: body incomplete, invalid UTF-8 or exceeds 2 MiB")
		}
		content := string(raw)
		if media == "text/html" {
			p.Title, content = extractHTML(content)
		}
		p.Truncated = len(content) > config.MaxOutputBytes
		p.Content = clip(content, config.MaxOutputBytes)
		for {
			raw, _ := json.Marshal(p)
			if len(raw) <= config.MaxOutputBytes {
				break
			}
			if len(p.Content) == 0 {
				return p, errors.New("web: source URL exceeds output budget")
			}
			p.Content = clip(p.Content, len(p.Content)*3/4)
			p.Truncated = true
		}
		return p, nil
	}
	return p, errors.New("web: too many redirects")
}

func extractHTML(raw string) (string, string) {
	z := html.NewTokenizer(strings.NewReader(raw))
	var title, body strings.Builder
	skip := 0
	inTitle := false
	for {
		t := z.Next()
		if t == html.ErrorToken {
			break
		}
		if t == html.StartTagToken || t == html.EndTagToken || t == html.SelfClosingTagToken {
			name, _ := z.TagName()
			tag := string(name)
			if tag == "script" || tag == "style" || tag == "noscript" || tag == "svg" || tag == "template" {
				if t == html.StartTagToken {
					skip++
				} else if t == html.EndTagToken && skip > 0 {
					skip--
				}
				continue
			}
			if tag == "title" {
				inTitle = t == html.StartTagToken
			}
			if skip == 0 && (tag == "p" || tag == "div" || tag == "br" || tag == "li" || tag == "tr" || tag == "h1" || tag == "h2" || tag == "h3" || tag == "pre") {
				body.WriteByte('\n')
			}
		} else if t == html.TextToken && skip == 0 {
			text := strings.TrimSpace(string(z.Text()))
			if text == "" {
				continue
			}
			if inTitle {
				title.WriteString(text)
			} else {
				body.WriteString(text)
				body.WriteByte(' ')
			}
		}
	}
	var lines []string
	for _, line := range strings.Split(body.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return clip(title.String(), 512), strings.Join(lines, "\n")
}
