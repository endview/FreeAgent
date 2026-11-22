package harness

import (
	"context"
	"encoding/json"
)

func (tasks *TaskService) detachedSettingConfirmation(ctx context.Context, run Run) (Confirmation, bool, error) {
	control, err := tasks.Control()
	if err != nil {
		return Confirmation{}, false, err
	}
	attempts, err := tasks.core.Store.ListAttempts(ctx, run.ID)
	if err != nil {
		return Confirmation{}, false, err
	}
	for _, attempt := range attempts {
		record, ok := control.operationForAttempt(attempt.ID)
		if !ok || !record.Operation.Detached || record.Operation.Status != ControlPending {
			continue
		}
		var call ToolCall
		if json.Unmarshal([]byte(attempt.RequestJSON), &call) != nil {
			return Confirmation{}, false, ErrStoreFormat
		}
		prepared := (&controlTools{}).preparedProposal(record.Operation)
		return confirmationFor(run.ID, attempt.ID, call, prepared), true, nil
	}
	return Confirmation{}, false, nil
}
