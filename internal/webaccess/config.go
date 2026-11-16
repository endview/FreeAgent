// Package webaccess implements bounded search and public-page retrieval. It has
// no task loop, model client, database, or extension business state.
package webaccess

import (
	"errors"
	"net/url"
	"strings"
)

type Connection struct {
	Provider string `json:"provider"`
	Key      string `json:"key"`
}

type Config struct {
	Mode              string                `json:"mode"`
	DefaultConnection string                `json:"default_connection,omitempty"`
	Connections       map[string]Connection `json:"connections,omitempty"`
	TimeoutSeconds    int                   `json:"timeout_seconds"`
	MaxResults        int                   `json:"max_results"`
	MaxOutputBytes    int                   `json:"max_output_bytes"`
}

func DefaultConfig() Config {
	return Config{Mode: "off", TimeoutSeconds: 20, MaxResults: 5, MaxOutputBytes: 16384}
}

type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

// Every adapter is explicit: endpoint and authentication are never inferred
// from a query, model alias, search result, or arbitrary response field.
func Providers() []Provider {
	return []Provider{
		{"deepseek", "DeepSeek Search", "https://api.deepseek.com"},
		{"tavily", "Tavily", "https://api.tavily.com"},
		{"brave", "Brave Search", "https://api.search.brave.com/res/v1"},
		{"bocha", "博查", "https://api.bochaai.com/v1"},
		{"searxng", "SearXNG（自托管）", ""},
		{"exa", "Exa", "https://api.exa.ai"},
		{"perplexity", "Perplexity Search", "https://api.perplexity.ai"},
		{"jina", "Jina Search", "https://s.jina.ai"},
		{"zhipu", "智谱 Web Search", "https://open.bigmodel.cn/api/paas/v4"},
		{"serper", "Serper", "https://google.serper.dev"},
		{"serpapi", "SerpApi", "https://serpapi.com"},
	}
}

func KnownProvider(id string) bool {
	for _, p := range Providers() {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (c Config) Validate() error {
	if c.Mode != "off" && c.Mode != "auto" && c.Mode != "api" && c.Mode != "native" {
		return errors.New("search: mode must be off, auto, api or native")
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 60 || c.MaxResults < 1 || c.MaxResults > 10 || c.MaxOutputBytes < 1024 || c.MaxOutputBytes > 65536 {
		return errors.New("search: timeout 1–60s, results 1–10 and output 1024–65536 bytes required")
	}
	if len(c.Connections) > 16 {
		return errors.New("search: at most 16 connections")
	}
	for id, p := range c.Connections {
		if id == "" || len(id) > 64 || strings.ContainsAny(id, " /\\\r\n\t") || !KnownProvider(p.Provider) || p.Key == "" {
			return errors.New("search: invalid connection id, provider or Key reference")
		}
	}
	if c.DefaultConnection != "" {
		if _, ok := c.Connections[c.DefaultConnection]; !ok {
			return errors.New("search: default connection does not exist")
		}
	}
	if c.Mode == "api" && c.DefaultConnection == "" {
		return errors.New("search: API mode requires a default connection")
	}
	return nil
}

func PublicURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || u.User != nil || u.Hostname() == "" || u.Scheme != "https" && u.Scheme != "http" || strings.ContainsAny(raw, "\x00\r\n\\") {
		return nil, errors.New("web: invalid HTTP(S) URL")
	}
	u.Fragment = ""
	return u, nil
}
