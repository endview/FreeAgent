package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const APIOrigin = "https://api.telegram.org"

type BotIdentity struct {
	ApplicationID string `json:"application_id"`
	Username      string `json:"username"`
}

func TokenBotID(token string) (string, error) {
	id, secret, ok := strings.Cut(token, ":")
	number, err := strconv.ParseInt(id, 10, 64)
	if !ok || number <= 0 || err != nil || strconv.FormatInt(number, 10) != id || len(secret) < 8 || len(token) > 256 {
		return "", errors.New("telegram: invalid Bot Token")
	}
	for _, r := range secret {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return "", errors.New("telegram: invalid Bot Token")
		}
	}
	return id, nil
}

// ProbeBot only reads Bot identity. It neither consumes updates nor enables
// a connection. Errors deliberately exclude the token-bearing request URL.
func ProbeBot(ctx context.Context, token string, transport http.RoundTripper) (BotIdentity, error) {
	id, err := TokenBotID(token)
	if err != nil {
		return BotIdentity{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, APIOrigin+"/bot"+token+"/getMe", nil)
	if err != nil {
		return BotIdentity{}, errors.New("telegram: could not prepare identity check")
	}
	client := http.Client{Timeout: 15 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return BotIdentity{}, errors.New("telegram: Bot identity check unavailable")
	}
	defer res.Body.Close()
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			ID       int64  `json:"id"`
			Bot      bool   `json:"is_bot"`
			Username string `json:"username"`
		} `json:"result"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 16<<10)).Decode(&envelope) != nil ||
		!envelope.OK || !envelope.Result.Bot || strconv.FormatInt(envelope.Result.ID, 10) != id || len(envelope.Result.Username) > 64 {
		return BotIdentity{}, errors.New("telegram: could not verify Bot identity")
	}
	return BotIdentity{ApplicationID: id, Username: envelope.Result.Username}, nil
}
