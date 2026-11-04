package harness

import (
	"context"
	"errors"
)

func (service *ControlService) invalidateProposalLocked(ctx context.Context, record *controlRecord, code string) error {
	record.Operation.Status, record.Operation.ErrorCode = ControlStale, code
	record.Operation.Recovery = "This proposal is no longer applicable. Start a new request with the current settings."
	if err := service.appendLocked("stale", *record); err != nil {
		return err
	}
	service.operations[record.Operation.ID] = *record
	delete(service.confirmations, record.Operation.ID)
	service.clearKeySecretLocked(record.Operation.ID)
	service.clearMCPProfileLocked(record.Operation.ID)
	return service.settleInvalidProposal(ctx, *record)
}

// A terminal setting proposal also closes its pending tool group. A stale
// control card must not leave a conversation permanently waiting for approval.
func (service *ControlService) settleInvalidProposal(ctx context.Context, record controlRecord) error {
	if record.Operation.AttemptID == "" {
		return nil
	}
	attempt, err := service.core.Store.GetAttempt(ctx, record.Operation.AttemptID)
	if err != nil {
		return err
	}
	if attempt.Status != "pending" {
		return nil
	}
	run, err := service.core.Store.GetRun(ctx, attempt.RunID)
	if err != nil {
		return err
	}
	result := ToolResult{Content: "control proposal ended without applying a setting: " + record.Operation.ErrorCode + "; start a new request if needed", IsError: true}
	err = service.core.Store.settleControlProposal(ctx, run, attempt, result, false, record.Operation.ErrorCode, "failed")
	if errors.Is(err, ErrControlAlreadyUsed) {
		return nil
	}
	return err
}
