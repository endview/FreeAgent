package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	telegramPairTTL      = 10 * time.Minute
	telegramPairAttempts = 5
	telegramCallbackTTL  = 10 * time.Minute
)

type telegramPair struct {
	CodeHash [32]byte
	Code     string
	Expires  time.Time
	Attempts int
	Used     bool
}
type TelegramChannelStatus struct {
	Key          string `json:"key,omitempty"`
	Enabled      bool   `json:"enabled"`
	Running      bool   `json:"running"`
	BotID        int64  `json:"bot_id,omitempty"`
	BotUsername  string `json:"bot_username,omitempty"`
	LastUpdateID int64  `json:"last_update_id,omitempty"`
	LastError    string `json:"last_error,omitempty"`
}

func channelDelay(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func shortDeliveryID(run string, index int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", run, index)))
	return hex.EncodeToString(h[:8])
}
