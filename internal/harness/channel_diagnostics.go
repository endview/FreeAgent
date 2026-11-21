package harness

import (
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

// Diagnostics are a bounded, generation-local view. Durable execution and
// delivery evidence continues to live in the existing journals.
type channelDiagnostic struct {
	Since      string                       `json:"since"`
	Updated    string                       `json:"updated"`
	Counts     map[string]uint64            `json:"counts"`
	CanReadAll *bool                        `json:"can_read_all,omitempty"`
	Groups     map[string]map[string]uint64 `json:"groups"`
}

func newChannelDiagnostic() channelDiagnostic {
	return channelDiagnostic{Since: nowText(), Counts: map[string]uint64{}, Groups: map[string]map[string]uint64{}}
}

func (b *ChannelBridge) observe(id, chat, stage string, count int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.connections[id]; c != nil {
		if c.diagnostic.Counts == nil {
			c.diagnostic = newChannelDiagnostic()
		}
		c.diagnostic.Updated = nowText()
		c.diagnostic.Counts[stage] += uint64(count)
		if chat != "" {
			g := c.diagnostic.Groups[chat]
			if g == nil && len(c.diagnostic.Groups) < 128 {
				g = map[string]uint64{}
				c.diagnostic.Groups[chat] = g
			}
			if g != nil {
				g[stage] += uint64(count)
			}
		}
	}
}

func (b *ChannelBridge) Observe(id string, o channelwire.Observation) error {
	if o.Count < 0 || o.Count > 100 {
		return ErrPermissionDenied
	}
	switch o.Stage {
	case "connected", "poll_ok", "poll_failed", "received", "filtered", "other_bot_command", "backlog_ignored":
	default:
		return ErrPermissionDenied
	}
	b.mu.Lock()
	c := b.connections[id]
	valid := c != nil && (o.Target.ChatID == "" || o.Target.Source == c.config.Source && o.Target.ApplicationID == c.config.ApplicationID && validChannelPart(o.Target.ChatID))
	if valid && o.Stage == "connected" && o.CanReadAll != nil {
		v := *o.CanReadAll
		c.diagnostic.CanReadAll = &v
	}
	if valid {
		c.lastSeen = time.Now()
	}
	b.mu.Unlock()
	if !valid {
		return ErrPermissionDenied
	}
	b.observe(id, o.Target.ChatID, o.Stage, o.Count)
	return nil
}
