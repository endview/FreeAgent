package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type APIError struct {
	Platform     string
	Status, Code int
	Uncertain    bool
	RetryAfter   time.Duration
}

func (e *APIError) DispatchCertain() bool     { return !e.Uncertain }
func (e *APIError) RetryDelay() time.Duration { return e.RetryAfter }

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: API failure status=%d code=%d delivery_unknown=%t", e.Platform, e.Status, e.Code, e.Uncertain)
}

type appHTTP struct {
	base, platform string
	http           *http.Client
}

func newAppHTTP(base, platform string, hosts []string, client *http.Client) (appHTTP, error) {
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return appHTTP{}, errors.New("channel: invalid API origin")
	}
	allowed := false
	for _, host := range hosts {
		if u.Host == host && u.Scheme == "https" {
			allowed = true
		}
	}
	ip := net.ParseIP(u.Hostname())
	if ip != nil && ip.IsLoopback() && u.Scheme == "http" {
		allowed = true
	}
	if !allowed {
		return appHTTP{}, errors.New("channel: official HTTPS origin or numeric loopback fixture required")
	}
	var c http.Client
	if client != nil {
		c = *client
	}
	c.Timeout = 15 * time.Second
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return appHTTP{strings.TrimRight(base, "/"), platform, &c}, nil
}
func (h appHTTP) request(ctx context.Context, path, auth string, input, output any, delivery bool) error {
	return h.requestMethod(ctx, http.MethodPost, path, auth, input, output, delivery)
}
func (h appHTTP) requestMethod(ctx context.Context, method, path, auth string, input, output any, delivery bool) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return errors.New("channel: invalid request")
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, bytes.NewReader(raw))
	if err != nil {
		return &APIError{Platform: h.platform}
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return &APIError{Platform: h.platform, Uncertain: delivery}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return &APIError{Platform: h.platform, Status: resp.StatusCode, Uncertain: delivery}
	}
	var envelope struct {
		Code int `json:"code"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return &APIError{Platform: h.platform, Status: resp.StatusCode, Uncertain: delivery}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Code != 0 {
		return &APIError{Platform: h.platform, Status: resp.StatusCode, Code: envelope.Code, Uncertain: delivery && resp.StatusCode >= 500}
	}
	if output != nil && json.Unmarshal(body, output) != nil {
		return &APIError{Platform: h.platform, Status: resp.StatusCode, Uncertain: delivery}
	}
	return nil
}
func validID(id string) bool {
	if len(id) < 1 || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == ':' || c == '.') {
			return false
		}
	}
	return true
}
