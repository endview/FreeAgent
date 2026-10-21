package harness

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	probeTimeout     = 15 * time.Second
	probeBodyLimit   = 1 << 20
	probeDetailBytes = 300
)

// ProbeResult is safe to show and store: Detail has the key replaced and is
// at most 300 bytes.
type ProbeResult struct {
	Category string   `json:"category"`
	Status   int      `json:"status,omitempty"`
	Models   []string `json:"models,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// ProbeModels sends one GET <base>/models. Every provider or network outcome
// is a category; an error means invalid input or a cancelled caller.
func ProbeModels(ctx context.Context, key ResolvedKey, client *http.Client) (ProbeResult, error) {
	return ProbeModelsForProtocol(ctx, key, client)
}

func ProbeModelsForProtocol(ctx context.Context, key ResolvedKey, client *http.Client) (ProbeResult, error) {
	protocol := normalizeKeyProtocol(key.Protocol)
	if base, err := NormalizeBaseURLForProtocol(key.BaseURL, protocol); err != nil || base != key.BaseURL {
		return ProbeResult{}, errors.New("harness: probe needs a normalized base URL")
	}
	probe := http.Client{Timeout: probeTimeout}
	if client != nil {
		probe = *client
		if probe.Timeout == 0 {
			probe.Timeout = probeTimeout
		}
	}
	// The key must never follow a redirect.
	probe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	path := key.BaseURL + "/models"
	if protocol == KeyProtocolAnthropic {
		path = key.BaseURL + "/v1/models?limit=1000"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return ProbeResult{}, errors.New("harness: probe request could not be built")
	}
	request.Header.Set("Accept", "application/json")
	if key.secret != "" {
		if protocol == KeyProtocolAnthropic {
			request.Header.Set("x-api-key", key.secret)
			request.Header.Set("anthropic-version", "2023-06-01")
		} else {
			request.Header.Set("Authorization", "Bearer "+key.secret)
		}
	}
	response, err := probe.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return ProbeResult{}, ctx.Err()
		}
		return ProbeResult{Category: probeErrorCategory(err)}, nil
	}
	defer response.Body.Close()
	result := ProbeResult{Status: response.StatusCode}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, probeBodyLimit))
	switch status := response.StatusCode; {
	case status >= 200 && status < 300:
		if readErr != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return ProbeResult{}, ctx.Err()
			}
			result.Category = probeErrorCategory(readErr)
			return result, nil
		}
		if models, ok := decodeModelList(body); ok {
			result.Category, result.Models = "ok", models
			return result, nil
		}
		result.Category = "not_models"
	case status >= 300 && status < 400:
		result.Category = "redirect"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		result.Category = "auth"
	case status == http.StatusNotFound:
		result.Category = "not_found"
	case status == http.StatusTooManyRequests:
		result.Category = "rate_limited"
	case status >= 500:
		result.Category = "server"
	default:
		result.Category = "unexpected"
	}
	result.Detail = probeDetail(body, key.secret)
	return result, nil
}

// decodeModelList accepts {"data":[{"id":...}]}; an empty list is still a
// model list.
func decodeModelList(body []byte) ([]string, bool) {
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &list) != nil || list.Data == nil {
		return nil, false
	}
	models := make([]string, 0, len(list.Data))
	for _, entry := range list.Data {
		if id := strings.TrimSpace(entry.ID); id != "" && validModelName(id) && !hasControl(id) {
			models = append(models, id)
		}
	}
	if len(models) == 0 && len(list.Data) != 0 {
		return nil, false
	}
	slices.Sort(models)
	return slices.Compact(models), true
}

func probeErrorCategory(err error) string {
	var timeout interface{ Timeout() bool }
	var verify *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var record tls.RecordHeaderError
	var alert tls.AlertError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return "timeout"
	case errors.As(err, &verify), errors.As(err, &authority), errors.As(err, &hostname),
		errors.As(err, &invalid), errors.As(err, &record), errors.As(err, &alert):
		return "tls"
	}
	return "network"
}

// probeDetail replaces the key before truncating so no partial key survives
// the cut, and drops control characters that could drive a terminal.
func probeDetail(body []byte, secret string) string {
	text := strings.ToValidUTF8(string(body), "\uFFFD")
	if secret != "" {
		text = strings.ReplaceAll(text, secret, redactedText)
	}
	text = strings.TrimSpace(strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}
		return char
	}, text))
	if len(text) > probeDetailBytes {
		cut := probeDetailBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	return text
}
