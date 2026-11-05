package harness

import (
	"context"
	"errors"
	"github.com/endview/freeagent/internal/channelwire"
	"net"
	"path/filepath"
	"strconv"
	"time"
)

type ChannelsConfig struct {
	Connections []channelwire.Connection `json:"connections,omitempty"`
}

func (config ChannelsConfig) validate() error {
	if len(config.Connections) > 16 {
		return errors.New("harness: too many optional channel connections")
	}
	seen := map[string]bool{}
	apps := map[string]bool{}
	for _, c := range config.Connections {
		if !validCapabilityID(c.ID) || seen[c.ID] || c.Source != "telegram" && c.Source != "lark" && c.Source != "qq" || !validChannelPart(c.ApplicationID) || !validKeyID(c.Credential) {
			return errors.New("harness: channel connection identity or credential is invalid")
		}
		if c.DropPendingBefore != "" {
			if c.Source != "telegram" {
				return errors.New("harness: cutover purge is Telegram only")
			}
			if _, err := time.Parse(time.RFC3339Nano, c.DropPendingBefore); err != nil {
				return errors.New("harness: invalid cutover time")
			}
		}
		seen[c.ID] = true
		identity := c.Source + "/" + c.ApplicationID
		if apps[identity] {
			return errors.New("harness: duplicate channel application binding")
		}
		apps[identity] = true
		if c.Source == "qq" {
			host, port, err := net.SplitHostPort(c.Listen)
			n, e := strconv.Atoi(port)
			ip := net.ParseIP(host)
			if err != nil || ip == nil || !ip.IsLoopback() || e != nil || n < 1 || n > 65535 {
				return errors.New("harness: QQ needs a numeric loopback listener")
			}
		}
	}
	return nil
}
func validateChannelKeys(keys *KeyStore, config ChannelsConfig) error {
	for _, c := range config.Connections {
		if keys == nil {
			return ErrKeyNotFound
		}
		profile, err := keys.Find(c.Credential)
		if err != nil {
			return err
		}
		if profile.Protocol != c.Source {
			return errors.New("harness: channel credential source mismatch")
		}
	}
	return nil
}

func (tasks *TaskService) ChannelKeys() *KeyStore {
	return NewChannelKeyStore(filepath.Join(tasks.DataDir(), "channel-keys.json"))
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
	keys := tasks.ChannelKeys()
	if err := validateChannelKeys(keys, config); err != nil {
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
