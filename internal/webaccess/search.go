package webaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type Result struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

type SearchResult struct {
	Provider  string    `json:"provider"`
	Query     string    `json:"query"`
	At        time.Time `json:"retrieved_at"`
	Results   []Result  `json:"results"`
	Truncated bool      `json:"truncated,omitempty"`
}

// HTTPError deliberately excludes response text and request URLs: both may
// contain the credential (notably APIs requiring a key in a query string).
type HTTPError struct {
	Status     int
	RetryAfter string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("search: HTTP %d (no automatic retry)", e.Status)
}

type Client struct {
	HTTP                      *http.Client
	BaseURL, Secret, Provider string
	Config                    Config
}

func (c Client) Search(ctx context.Context, query string, count int) (SearchResult, error) {
	result := SearchResult{Provider: c.Provider, Query: query, At: time.Now().UTC(), Results: []Result{}}
	if err := c.Config.Validate(); err != nil {
		return result, err
	}
	if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 128 {
		return result, errors.New("search: query must contain 1–128 Unicode characters")
	}
	if c.Secret != "" && strings.Contains(query, c.Secret) {
		return result, errors.New("search: query contains the connection credential; not dispatched")
	}
	if count == 0 {
		count = c.Config.MaxResults
	}
	if count < 1 || count > c.Config.MaxResults {
		return result, errors.New("search: result count exceeds configured bound")
	}
	base, err := PublicURL(c.BaseURL)
	if err != nil || base.RawQuery != "" {
		return result, errors.New("search: invalid configured endpoint")
	}
	if base.Scheme == "http" && base.Hostname() != "localhost" && base.Hostname() != "127.0.0.1" && base.Hostname() != "::1" {
		return result, errors.New("search: plain HTTP only permitted for explicit loopback endpoint")
	}
	method, path, body, headers, params, err := searchRequest(c.Provider, query, count, c.Secret)
	if err != nil {
		return result, err
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	if c.Provider == "deepseek" {
		base.Path = strings.Replace(base.Path, "/v1/v1/tools/search", "/v1/tools/search", 1)
	}
	base.RawQuery = params.Encode()
	raw, _ := json.Marshal(body)
	var reader io.Reader
	if method == http.MethodPost {
		reader = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.Config.TimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, base.String(), reader)
	if err != nil {
		return result, errors.New("search: invalid request")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = NewHTTPClient(time.Duration(c.Config.TimeoutSeconds)*time.Second, true)
	}
	// A copied client prevents callers' redirect policies or jars from leaking keys.
	safe := *httpClient
	safe.Jar = nil
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := safe.Do(req)
	if err != nil {
		return result, errors.New("search: request interrupted; no automatic retry, usage may be charged")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, &HTTPError{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After")}
	}
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		return result, errors.New("search: response incomplete or exceeds 2 MiB")
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return result, errors.New("search: invalid JSON response")
	}
	items, err := searchItems(c.Provider, doc)
	if err != nil {
		return result, err
	}
	budget := c.Config.MaxOutputBytes - 768
	seen := map[string]bool{}
	for _, item := range items {
		v, ok := item.(map[string]any)
		if !ok {
			continue
		}
		u := stringField(v, "url", "link")
		parsed, e := PublicURL(u)
		if e != nil {
			continue
		}
		u = parsed.String()
		if seen[u] {
			continue
		}
		seen[u] = true
		r := Result{Title: clip(stringField(v, "title", "name"), 512), URL: u, Snippet: clip(stringField(v, "summary", "content", "snippet", "description", "text"), 2048), PublishedAt: clip(stringField(v, "published_at", "publishedDate", "datePublished", "publish_date", "date"), 128)}
		if c.Secret != "" {
			r.Title = strings.ReplaceAll(r.Title, c.Secret, "[redacted]")
			r.Snippet = strings.ReplaceAll(r.Snippet, c.Secret, "[redacted]")
			r.PublishedAt = strings.ReplaceAll(r.PublishedAt, c.Secret, "[redacted]")
			if strings.Contains(r.URL, c.Secret) {
				continue
			}
		}
		encoded, _ := json.Marshal(r)
		for len(encoded) > budget && r.Snippet != "" {
			result.Truncated = true
			r.Snippet = clip(r.Snippet, len(r.Snippet)*3/4)
			encoded, _ = json.Marshal(r)
		}
		if len(encoded) > budget {
			result.Truncated = true
			continue
		}
		budget -= len(encoded) + 1
		result.Results = append(result.Results, r)
		if len(result.Results) == count {
			break
		}
	}
	return result, nil
}

func searchRequest(provider, query string, count int, secret string) (string, string, map[string]any, map[string]string, url.Values, error) {
	b := map[string]any{"query": query}
	h := map[string]string{}
	q := url.Values{}
	method, path := "POST", ""
	if provider != "searxng" && secret == "" {
		return "", "", nil, nil, nil, errors.New("search: credential required")
	}
	switch provider {
	case "deepseek":
		path = "/v1/tools/search"
		b["count"] = count
	case "tavily":
		path = "/search"
		b["max_results"] = count
		b["include_answer"] = false
		b["include_raw_content"] = false
	case "brave":
		method = "GET"
		path = "/web/search"
		q.Set("q", query)
		q.Set("count", fmt.Sprint(count))
		h["X-Subscription-Token"] = secret
	case "bocha":
		path = "/web-search"
		b["count"] = count
		b["summary"] = true
	case "searxng":
		method = "GET"
		path = "/search"
		q.Set("q", query)
		q.Set("format", "json")
	case "exa":
		path = "/search"
		b["numResults"] = count
		b["contents"] = map[string]any{"text": map[string]any{"maxCharacters": 1500}}
		h["x-api-key"] = secret
	case "perplexity":
		path = "/search"
		b["max_results"] = count
	case "jina":
		method = "GET"
		path = "/"
		q.Set("q", query)
		q.Set("num", fmt.Sprint(count))
		h["X-Respond-With"] = "no-content"
	case "zhipu":
		path = "/web_search"
		b = map[string]any{"search_engine": "search_pro", "search_query": query, "count": count, "content_size": "medium"}
	case "serper":
		path = "/search"
		b = map[string]any{"q": query, "num": count}
		h["X-API-KEY"] = secret
	case "serpapi":
		method = "GET"
		path = "/search.json"
		q.Set("engine", "google")
		q.Set("q", query)
		q.Set("num", fmt.Sprint(count))
		q.Set("api_key", secret)
	default:
		return "", "", nil, nil, nil, errors.New("search: unsupported provider")
	}
	if secret != "" && h["X-Subscription-Token"] == "" && h["x-api-key"] == "" && h["X-API-KEY"] == "" && provider != "serpapi" {
		h["Authorization"] = "Bearer " + secret
	}
	return method, path, b, h, q, nil
}

func searchItems(provider string, doc map[string]any) ([]any, error) {
	if code, ok := doc["code"].(float64); ok && code != 0 && code != 200 {
		return nil, errors.New("search: provider returned an error")
	}
	if doc["error"] != nil {
		return nil, errors.New("search: provider returned an error")
	}
	var items any
	switch provider {
	case "brave":
		if v, ok := doc["web"].(map[string]any); ok {
			items = v["results"]
		}
	case "bocha":
		if d, ok := doc["data"].(map[string]any); ok {
			if w, ok := d["webPages"].(map[string]any); ok {
				items = w["value"]
			}
		}
	case "deepseek":
		items = doc["results"]
		if items == nil {
			if d, ok := doc["data"].(map[string]any); ok {
				items = d["results"]
			}
		}
	case "jina":
		items = doc["data"]
	case "zhipu":
		items = doc["search_result"]
	case "serper":
		items = doc["organic"]
	case "serpapi":
		items = doc["organic_results"]
	default:
		items = doc["results"]
	}
	if items == nil {
		return nil, errors.New("search: missing results array")
	}
	a, ok := items.([]any)
	if !ok {
		return nil, errors.New("search: invalid results array")
	}
	return a, nil
}

func stringField(v map[string]any, names ...string) string {
	for _, name := range names {
		if s, ok := v[name].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
