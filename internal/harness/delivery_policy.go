package harness

import (
	"errors"
	"time"
	_ "time/tzdata"
)

const ControlSetDeliveryPolicy = "delivery.set_policy"

// DeliveryConfig contains transport bounds and grants, never expression policy.
type DeliveryConfig struct {
	GenerationEnabled map[string]bool `json:"generation_enabled,omitempty"`
	StickerLimit      int             `json:"sticker_limit,omitempty"`
	IntervalMS        int             `json:"interval_ms,omitempty"`
	Enabled           bool            `json:"enabled,omitempty"`
	Rules             []FollowupRule  `json:"rules,omitempty"`
}
type FollowupRule struct {
	Generation                bool     `json:"generation,omitempty"`
	Resource                  string   `json:"resource,omitempty"`
	FreeTopics                bool     `json:"free_topics,omitempty"`
	SourceRefs                []string `json:"source_refs,omitempty"`
	Slots                     []string `json:"slots,omitempty"`
	IdleHours                 int      `json:"idle_hours,omitempty"`
	GenerationDeadlineSeconds int      `json:"generation_deadline_seconds,omitempty"`
	ID                        string   `json:"id"`
	Extension                 string   `json:"extension"`
	ConversationID            string   `json:"conversation_id"`
	Enabled                   bool     `json:"enabled"`
	Timezone                  string   `json:"timezone"`
	QuietStart                string   `json:"quiet_start,omitempty"`
	QuietEnd                  string   `json:"quiet_end,omitempty"`
	DailyMax                  int      `json:"daily_max"`
	CooldownHours             int      `json:"cooldown_hours"`
	AllowSticker              bool     `json:"allow_sticker"`
	CancelOnInput             bool     `json:"cancel_on_input"`
	MaxDelaySeconds           int      `json:"max_delay_seconds"`
}

func (config DeliveryConfig) effective() DeliveryConfig {
	if config.StickerLimit == 0 {
		config.StickerLimit = 1
	}
	return config
}
func (config DeliveryConfig) validate() error {
	c := config.effective()
	if c.StickerLimit < 1 || c.StickerLimit > 3 || c.IntervalMS < 0 || c.IntervalMS > 2000 || len(c.Rules) > 64 {
		return errors.New("harness: invalid bounded delivery policy")
	}
	if len(c.GenerationEnabled) > 64 {
		return errors.New("harness: excessive generation grants")
	}
	for id := range c.GenerationEnabled {
		if !validExtensionID(id) {
			return ErrPermissionDenied
		}
	}
	seen := map[string]bool{}
	for _, r := range c.Rules {
		if !validCapabilityID(r.ID) || seen[r.ID] || !validExtensionID(r.Extension) || !memoryID(r.ConversationID) || r.DailyMax < 1 || r.DailyMax > 24 || r.CooldownHours < 1 || r.CooldownHours > 168 || r.MaxDelaySeconds < 1 || r.MaxDelaySeconds > 7*86400 {
			return errors.New("harness: invalid follow-up rule")
		}
		if r.Generation {
			if !validCapabilityID(r.Resource) || len(r.Slots) > 4 || len(r.SourceRefs) < 1 || len(r.SourceRefs) > 3 || r.IdleHours < 0 || r.IdleHours > 168 || r.GenerationDeadlineSeconds != 0 && (r.GenerationDeadlineSeconds < 5 || r.GenerationDeadlineSeconds > 120) || !r.CancelOnInput {
				return errors.New("harness: invalid scheduled generation rule")
			}
			for _, slot := range r.Slots {
				if _, err := time.Parse("15:04", slot); err != nil {
					return err
				}
			}
			for _, ref := range r.SourceRefs {
				if ref == "" || len(ref) > 256 {
					return ErrPermissionDenied
				}
			}
		}
		seen[r.ID] = true
		if _, err := time.LoadLocation(r.Timezone); err != nil || r.Timezone == "" {
			return errors.New("harness: follow-up needs an explicit valid timezone")
		}
		if (r.QuietStart == "") != (r.QuietEnd == "") {
			return errors.New("harness: specify both quiet time boundaries")
		}
		if r.QuietStart != "" {
			a, err := time.Parse("15:04", r.QuietStart)
			if err != nil {
				return err
			}
			b, err := time.Parse("15:04", r.QuietEnd)
			if err != nil || a.Equal(b) {
				return errors.New("harness: invalid quiet interval")
			}
		}
	}
	return nil
}
