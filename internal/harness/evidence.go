package harness

import (
	"context"
	"encoding/json"
	"fmt"
)

// Evidence is a projection of existing attempts, not another durable entity.
type Evidence struct {
	Files        []ToolReceipt `json:"files,omitempty"`
	Commands     []ToolReceipt `json:"commands_run,omitempty"`
	HTTP         []ToolReceipt `json:"http_requests,omitempty"`
	ChecksPassed []string      `json:"checks_passed,omitempty"`
	ChecksNotRun []string      `json:"checks_not_run,omitempty"`
	Uncertainty  []string      `json:"remaining_uncertainty,omitempty"`
}

func (store *Store) Evidence(ctx context.Context, runID string) (Evidence, error) {
	attempts, err := store.ListAttempts(ctx, runID)
	if err != nil {
		return Evidence{}, err
	}
	return attemptEvidence(attempts), nil
}

func attemptEvidence(attempts []Attempt) Evidence {
	var evidence Evidence
	for _, attempt := range attempts {
		var result ToolResult
		if attempt.Kind == "tool" {
			_ = json.Unmarshal([]byte(attempt.ResponseJSON), &result)
			for _, receipt := range result.Receipts {
				switch receipt.Kind {
				case "file":
					evidence.Files = append(evidence.Files, receipt)
					if receipt.Status == "unknown" || receipt.Truncated || receipt.Detail != "" {
						evidence.Uncertainty = append(evidence.Uncertainty,
							receipt.Path+": "+receipt.Status+"; "+receipt.Detail)
					}
				case "command":
					if receipt.Status != "not_launched" && receipt.Status != "failed_to_start" {
						evidence.Commands = append(evidence.Commands, receipt)
					}
					if receipt.Check {
						if attempt.Status == "finished" && receipt.Status == "completed" &&
							receipt.ExitCode != nil && *receipt.ExitCode == 0 && !receipt.Truncated {
							evidence.ChecksPassed = append(evidence.ChecksPassed, receipt.Command)
						} else if receipt.Status == "not_launched" || receipt.Status == "failed_to_start" {
							evidence.ChecksNotRun = append(evidence.ChecksNotRun, receipt.Command)
						}
					}
					if receipt.Status == "interrupted" || receipt.Status == "timed_out" || receipt.Status == "unknown" || receipt.Truncated {
						evidence.Uncertainty = append(evidence.Uncertainty, receipt.Command+": "+receipt.Status+"; no claim of unchanged side effects")
					}
					if receipt.Detail != "" {
						evidence.Uncertainty = append(evidence.Uncertainty, receipt.Command+": "+receipt.Detail)
					}
				case "observation":
					evidence.Uncertainty = append(evidence.Uncertainty, receipt.Detail)
				case "http":
					evidence.HTTP = append(evidence.HTTP, receipt)
					if receipt.Status == "unknown" || receipt.Truncated || receipt.Detail != "" {
						evidence.Uncertainty = append(evidence.Uncertainty, receipt.URL+": "+receipt.Status+"; "+receipt.Detail)
					}
				}
			}
			if attempt.Status == "pending" || (attempt.Status == "unknown" && len(result.Receipts) == 0) {
				var call ToolCall
				if json.Unmarshal([]byte(attempt.RequestJSON), &call) == nil && call.Name == "command_run" {
					var args commandArguments
					if json.Unmarshal(call.Arguments, &args) == nil && args.Check {
						if attempt.Status == "pending" {
							evidence.ChecksNotRun = append(evidence.ChecksNotRun, args.Command)
						}
					}
				}
			}
		}
		if attempt.Status == "unknown" || attempt.Status == "dispatched" {
			evidence.Uncertainty = append(evidence.Uncertainty, fmt.Sprintf("%s attempt %s has UNKNOWN outcome; not automatically replayable", attempt.Kind, attempt.ID))
		}
	}
	return evidence
}
