package harness

import (
	"context"
	"time"
)

type ExtensionWakeHint struct {
	RuleID    string   `json:"rule_id"`
	Sources   []string `json:"source_refs"`
	NotBefore string   `json:"not_before"`
	NotAfter  string   `json:"not_after"`
}

func (service *Service) acceptWakeHint(ctx context.Context, run Run, id string, h ExtensionWakeHint) error {
	if len(h.Sources) < 1 || len(h.Sources) > 3 {
		return ErrPermissionDenied
	}
	after, err := time.Parse(time.RFC3339Nano, h.NotBefore)
	if err != nil {
		return err
	}
	until, err := time.Parse(time.RFC3339Nano, h.NotAfter)
	if err != nil || !until.After(after) || until.Sub(after) > time.Hour || !after.After(time.Now()) {
		return ErrPermissionDenied
	}
	tasks := NewTaskService(service)
	for _, r := range tasks.Config().Delivery.Rules {
		if r.ID != h.RuleID || !r.Generation || !r.Enabled || r.Extension != id || r.ConversationID != run.ConversationID {
			continue
		}
		if after.Sub(time.Now()) > time.Duration(r.MaxDelaySeconds)*time.Second {
			return ErrPermissionDenied
		}
		for _, ref := range h.Sources {
			if !contains(r.SourceRefs, ref) {
				return ErrPermissionDenied
			}
		}
		o, created, err := tasks.ScheduleWake(ctx, r, "matter:"+run.ID+":"+contentHash(mustJSON(h.Sources)), h.Sources, time.Now(), false, after)
		if err != nil || !created {
			return err
		}
		o.Expires = until.UTC().Format(time.RFC3339Nano)
		return tasks.persistWake(ctx, o)
	}
	return ErrPermissionDenied
}
