package harness

import (
	"context"
	"errors"
	"time"
)

func (service *ControlService) ReadControlOperation(ctx context.Context, id string) (ControlOperation, error) {
	if err := ctx.Err(); err != nil {
		return ControlOperation{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.expireDirectLocked(time.Now()); err != nil {
		return ControlOperation{}, err
	}
	record, ok := service.operations[id]
	if !ok {
		return ControlOperation{}, ErrControlOperationNotFound
	}
	if record.Operation.Status == ControlPending && record.Operation.Detached {
		if err := service.checkDetachedExpiryLocked(ctx, &record); err != nil && !errors.Is(err, ErrControlExpired) {
			return ControlOperation{}, err
		}
	}
	return cloneControlOperation(record.Operation), nil
}

// RepreviewTelegramHandoff uses the original intent but never extends or
// applies the old proposal.
func (service *ControlService) RepreviewTelegramHandoff(ctx context.Context, id string) (ControlOperation, error) {
	op, err := service.ReadControlOperation(ctx, id)
	if err != nil {
		return ControlOperation{}, err
	}
	if op.RequestEntry != telegramAdminEntry || op.Status != ControlStale || op.ErrorCode != "expired" && op.ErrorCode != "stale_revision" {
		return ControlOperation{}, ErrControlAlreadyUsed
	}
	service.mu.Lock()
	record := service.operations[id]
	session, err := BeginPublicSettingsDecision(ctx, op.LocalApproval)
	if err != nil {
		service.mu.Unlock()
		return ControlOperation{}, err
	}
	authority, err := service.lockTelegramAdminDecision(ctx, record, false, false)
	if err == nil {
		authority()
	}
	session()
	service.mu.Unlock()
	if err != nil {
		return ControlOperation{}, err
	}
	return service.PlanControlChange(ctx, ControlPlanRequest{Kind: op.Kind, Arguments: record.Arguments,
		Authorization: ControlUserDirect, ConversationID: op.ConversationID, replaces: id})
}
