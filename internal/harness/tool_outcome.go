package harness

import (
	"context"
	"errors"
	"time"
)

// ToolOutcome describes dispatch/completion facts; backend receipts retain
// their file, HTTP and process details. Cancellation cannot erase completion.
type ToolOutcome struct {
	Status          string `json:"status"`
	Dispatched      bool   `json:"dispatched"`
	CompletionKnown bool   `json:"completion_known"`
	Cancelled       bool   `json:"cancelled,omitempty"`
	TimedOut        bool   `json:"timed_out,omitempty"`
	Truncated       bool   `json:"truncated,omitempty"`
	DurationMS      int64  `json:"duration_ms"`
}

func normalizeToolOutcome(ctx context.Context, result ToolResult, callErr error, started time.Time, limit int) ToolResult {
	var failure *CallError
	known := callErr == nil || errors.As(callErr, &failure) && failure.Certain
	for _, receipt := range result.Receipts {
		if receipt.Status == "unknown" {
			known = false
		}
		result.Truncated = result.Truncated || receipt.Truncated
	}
	if len(result.Content) > limit {
		result.Content = truncateUTF8Bytes(result.Content, limit)
		result.Truncated = true
		result.IsError = true
	}
	status := "completed"
	if result.IsError || callErr != nil {
		status = "failed"
	}
	if !known {
		status = "unknown"
	}
	outcome := &ToolOutcome{Status: status, Dispatched: true, CompletionKnown: known, DurationMS: max(0, time.Since(started).Milliseconds()), Truncated: result.Truncated}
	outcome.Cancelled = errors.Is(ctx.Err(), context.Canceled)
	outcome.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	if result.Truncated {
		if known {
			outcome.Status = "truncated"
		}
		result.IsError = true
		for i := range result.Receipts {
			result.Receipts[i].Truncated = true
		}
	}
	result.Outcome = outcome
	return result
}
