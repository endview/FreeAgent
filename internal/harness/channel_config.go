package harness

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"

	"github.com/endview/freeagent/internal/channels"
)

type ChannelsConfig struct {
	Telegram TelegramConfig   `json:"telegram"`
	Lark     AppChannelConfig `json:"lark"`
	QQ       AppChannelConfig `json:"qq"`
}

type TelegramConfig struct {
	Enabled bool   `json:"enabled"`
	Key     string `json:"key,omitempty"`
}

type AppChannelConfig struct {
	Enabled bool   `json:"enabled"`
	Key     string `json:"key,omitempty"`
	AppID   string `json:"app_id,omitempty"`
	Listen  string `json:"listen,omitempty"` // QQ: numeric loopback host:port only
}

func (config ChannelsConfig) validate() error {
	if config.Telegram.Key != "" && !validKeyID(config.Telegram.Key) || config.Telegram.Enabled && config.Telegram.Key == "" {
		return errors.New("harness: Telegram requires a saved key profile reference")
	}
	for _, item := range []AppChannelConfig{config.Lark, config.QQ} {
		if item.Key != "" && !validKeyID(item.Key) || item.AppID != "" && !validChannelPart(item.AppID) || item.Enabled && (item.Key == "" || item.AppID == "") {
			return errors.New("harness: app channel requires saved key and app ID")
		}
	}
	if config.QQ.Listen != "" {
		host, port, err := net.SplitHostPort(config.QQ.Listen)
		ip := net.ParseIP(host)
		number, e := strconv.Atoi(port)
		if err != nil || ip == nil || !ip.IsLoopback() || e != nil || number < 1 || number > 65535 {
			return errors.New("harness: QQ webhook needs a numeric loopback listener and nonzero port")
		}
	}
	if config.QQ.Enabled && config.QQ.Listen == "" {
		return errors.New("harness: QQ webhook listen address required")
	}
	return nil
}
func validateChannelKeys(keys *KeyStore, config ChannelsConfig) error {
	for _, item := range []struct{ key, protocol string }{{config.Telegram.Key, KeyProtocolTelegram}, {config.Lark.Key, KeyProtocolLark}, {config.QQ.Key, KeyProtocolQQ}} {
		if item.key == "" {
			continue
		}
		if keys == nil {
			return ErrKeyNotFound
		}
		profile, err := keys.Find(item.key)
		if err != nil {
			return err
		}
		if profile.Protocol != item.protocol {
			return errors.New("harness: channel credential protocol mismatch")
		}
	}
	return nil
}

func (tasks *TaskService) TelegramAPI(client *http.Client) (*channels.TelegramAPI, error) {
	config := tasks.Config().Channels.Telegram
	if tasks.Keys() == nil || config.Key == "" {
		return nil, ErrKeyNotFound
	}
	key, err := tasks.Keys().Resolve(config.Key)
	if err != nil {
		return nil, err
	}
	if key.Protocol != KeyProtocolTelegram {
		return nil, errors.New("harness: choose a Telegram credential profile")
	}
	return channels.NewTelegramAPI(key.BaseURL, key.secret, client)
}

// ConfigureChannels changes only optional connections. It must not wait for a
// model call or prevent the user from disabling a faulty transport.
func (tasks *TaskService) ConfigureChannels(ctx context.Context, config ChannelsConfig) error {
	if err := config.validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateChannelKeys(tasks.Keys(), config); err != nil {
		return err
	}
	tasks.mu.Lock()
	defer tasks.mu.Unlock()
	next := cloneConfig(tasks.core.currentConfig())
	next.Channels = config
	if err := WriteConfig(tasks.DataDir(), next); err != nil {
		return err
	}
	tasks.core.setConfig(next)
	if tasks.core.control != nil {
		return tasks.core.control.refreshBaseline()
	}
	return nil
}
