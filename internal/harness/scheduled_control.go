package harness

import (
	"context"
	"time"
)

func scheduledTargetInputKey(target ChannelTarget) string {
	return "scheduled/target-input/" + contentHash(mustJSON(target))
}

func (tasks *TaskService) cancelWakeTarget(target ChannelTarget) {
	host := tasks.core.extensions
	if host == nil {
		return
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	for token, inv := range host.active {
		if inv.service != nil && inv.service.extensionTrigger != nil && inv.service.channel != nil && inv.service.channel.ChannelTarget == target {
			inv.cancel()
			delete(host.active, token)
		}
	}
}

func (tasks *TaskService) invalidateExtensionWakes(ctx context.Context, id, resource string) error {
	_, err := tasks.core.Store.db.ExecContext(ctx, `UPDATE fa.meta SET value=(document||jsonb_build_object('state','cancelled','reason','extension resource changed'))::text WHERE starts_with(key,$1) AND document#>>'{rule,extension}'=$2 AND ($3='' OR document#>>'{rule,resource}'=$3) AND document->>'state' IN ('queued','generating','generated','delivery_queued')`, wakePrefix, id, resource)
	if err == nil {
		tasks.cancelWakeInvocations(id, resource, "")
	}
	return err
}

func (tasks *TaskService) cancelWakeInvocations(extension, resource, opportunity string) {
	host := tasks.core.extensions
	if host == nil {
		return
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	for token, invocation := range host.active {
		if invocation.service == nil {
			continue
		}
		trigger := invocation.service.extensionTrigger
		if trigger != nil && trigger.Extension == extension && (resource == "" || trigger.Resource == resource) && (opportunity == "" || trigger.Opportunity == opportunity) {
			invocation.cancel()
			delete(host.active, token)
		}
	}
}
func (tasks *TaskService) cancelRevokedWakes(ctx context.Context) error {
	records, err := tasks.pendingWakes(ctx)
	if err != nil {
		return err
	}
	for _, o := range records {
		if o.State != "queued" && o.State != "generating" && o.State != "generated" && o.State != "delivery_queued" {
			continue
		}
		if err := tasks.wakeCurrent(ctx, o, time.Now()); err != nil {
			o.State = "cancelled"
			o.Reason = "authorization revoked or source changed"
			if err = tasks.persistWake(ctx, o); err != nil {
				return err
			}
			tasks.cancelWakeInvocations(o.Rule.Extension, o.Rule.Resource, o.ID)
		}
	}
	return nil
}
func (tasks *TaskService) PreviewScheduledGeneration(ctx context.Context, ruleID, key string) (WakeOpportunity, error) {
	if len(key) < 1 || len(key) > 128 {
		return WakeOpportunity{}, ErrRequestKey
	}
	for _, r := range tasks.Config().Delivery.Rules {
		if r.ID == ruleID && r.Generation && r.Enabled {
			refs := r.SourceRefs
			o, _, err := tasks.ScheduleWake(ctx, r, "preview:"+key, refs, time.Now(), true)
			return o, err
		}
	}
	return WakeOpportunity{}, ErrPermissionDenied
}
