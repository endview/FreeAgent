package harness

import (
	"context"
	"time"

	"github.com/endview/freeagent/internal/channelwire"
)

type callbackFeedbackKey struct{}

func WithChannelFeedback(ctx context.Context, feedback *string) context.Context {
	return context.WithValue(ctx, callbackFeedbackKey{}, feedback)
}
func channelFeedback(ctx context.Context, text string) {
	if dest, ok := ctx.Value(callbackFeedbackKey{}).(*string); ok {
		*dest = text
	}
}
func channelFeedbackFallback(ctx context.Context, text string) {
	if dest, ok := ctx.Value(callbackFeedbackKey{}).(*string); ok && *dest == "" {
		*dest = text
	}
}

// Diagnostics are a bounded, generation-local view. Durable execution and
// delivery evidence continues to live in the existing journals.
type channelDiagnostic struct {
	Since      string                       `json:"since"`
	Updated    string                       `json:"updated"`
	Counts     map[string]uint64            `json:"counts"`
	CanReadAll *bool                        `json:"can_read_all,omitempty"`
	Groups     map[string]map[string]uint64 `json:"groups"`
	Ingress    *channelIngressDiagnostic    `json:"ingress,omitempty"`
}

type channelIngressDiagnostic struct {
	AdvancedAt   string     `json:"advanced_at,omitempty"`
	PendingSince *time.Time `json:"pending_since,omitempty"`
	Retries      uint64     `json:"retries,omitempty"`
	WaitingMS    int64      `json:"waiting_ms,omitempty"`
}

func (d channelDiagnostic) snapshot(now time.Time) channelDiagnostic {
	if d.Ingress != nil && d.Ingress.PendingSince != nil {
		value := *d.Ingress
		value.WaitingMS = max(0, now.Sub(*value.PendingSince).Milliseconds())
		d.Ingress = &value
	}
	return d
}

func newChannelDiagnostic() channelDiagnostic {
	return channelDiagnostic{Since: nowText(), Counts: map[string]uint64{}, Groups: map[string]map[string]uint64{}}
}

func (b *ChannelBridge) observe(id, chat, stage string, count int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.connections[id]; c != nil {
		c.recordObservation(chat, stage, count)
	}
}

func (c *channelConnection) recordObservation(chat, stage string, count int) {
	if c.diagnostic.Counts == nil {
		c.diagnostic = newChannelDiagnostic()
	}
	now := time.Now().UTC()
	d := &c.diagnostic
	d.Updated = now.Format(time.RFC3339Nano)
	d.Counts[stage] += uint64(count)
	if c.config.Source == SpaceSourceTelegram {
		if d.Ingress == nil {
			d.Ingress = &channelIngressDiagnostic{}
		}
		v := d.Ingress
		switch stage {
		case "received", channelwire.ObservationInboundRetry:
			if v.PendingSince == nil {
				v.PendingSince = &now
			}
			if stage == channelwire.ObservationInboundRetry {
				v.Retries++
			}
		case channelwire.ObservationInboundAdvance:
			v.AdvancedAt = d.Updated
			v.PendingSince, v.Retries = nil, 0
		case "poll_ok":
			// A fresh poll starts only after the previous batch advances.
			// Also clears a stale wait after a missed progress observation.
			v.PendingSince, v.Retries = nil, 0
		}
	}
	if chat != "" {
		g := d.Groups[chat]
		if g == nil && len(d.Groups) < 128 {
			g = map[string]uint64{}
			d.Groups[chat] = g
		}
		if g != nil {
			g[stage] += uint64(count)
		}
	}
}

func (b *ChannelBridge) Observe(id string, o channelwire.Observation) error {
	if o.Count < 0 || o.Count > 100 {
		return ErrPermissionDenied
	}
	switch o.Stage {
	case "connected", "poll_ok", "poll_failed", "received", "filtered", "other_bot_command", "backlog_ignored":
	case channelwire.ObservationInboundRetry, channelwire.ObservationInboundAdvance:
		if o.Count != 1 || o.Target != (channelwire.Target{}) || o.CanReadAll != nil {
			return ErrPermissionDenied
		}
	default:
		return ErrPermissionDenied
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.connections[id]
	valid := c != nil && (o.Target.ChatID == "" || o.Target.Source == c.config.Source && o.Target.ApplicationID == c.config.ApplicationID && validChannelPart(o.Target.ChatID))
	if !valid || (o.Stage == channelwire.ObservationInboundRetry || o.Stage == channelwire.ObservationInboundAdvance) && c.config.Source != SpaceSourceTelegram {
		return ErrPermissionDenied
	}
	if o.Stage == "connected" && o.CanReadAll != nil {
		v := *o.CanReadAll
		c.diagnostic.CanReadAll = &v
	}
	c.lastSeen = time.Now()
	c.recordObservation(o.Target.ChatID, o.Stage, o.Count)
	return nil
}
