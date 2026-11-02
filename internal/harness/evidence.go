package harness

import (
	"context"
	"encoding/json"
	"fmt"
)

// Evidence projects existing Attempts and delivery receipts, not a new entity.
type Evidence struct {
	Attachments  []ToolReceipt `json:"attachments,omitempty"`
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
	evidence := attemptEvidence(attempts)
	notes, err := channelDeliveryNotes(ctx, store.db, runID)
	evidence.Uncertainty = append(evidence.Uncertainty, notes...)
	return evidence, err
}

// Delivery is a projection of the existing send receipts. A completed tool or
// model Run stays completed even when its channel reply could not be delivered.
func channelDeliveryNotes(ctx context.Context, reader attemptReader, runID string) ([]string, error) {
	prefix := "channel/send/" + runID + "/"
	rows, err := reader.QueryContext(ctx, `SELECT json_extract(value,'$.state'),count(*) FROM meta WHERE (key>=? AND key<?) OR (key>=? AND key<?) GROUP BY json_extract(value,'$.state') LIMIT 16`, prefix, prefix+"\uffff", "channel/file-send/"+runID+"/", "channel/file-send/"+runID+"/\uffff")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []string
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		switch state {
		case "unknown", "dispatching":
			notes = append(notes, fmt.Sprintf("channel delivery UNKNOWN for %d part(s); execution was not replayed; inspect saved result before a new request", count))
		case "failed":
			notes = append(notes, fmt.Sprintf("channel delivery failed for %d part(s); saved execution result retained; use an explicit status query", count))
		}
	}
	return notes, rows.Err()
}

func attemptEvidence(attempts []Attempt) Evidence {
	var evidence Evidence
	for _, attempt := range attempts {
		var result ToolResult
		if attempt.Kind == "tool" {
			_ = json.Unmarshal([]byte(attempt.ResponseJSON), &result)
			for _, receipt := range result.Receipts {
				switch receipt.Kind {
				case "attachment":
					evidence.Attachments = append(evidence.Attachments, receipt)
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
