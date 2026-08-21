package currentbackup

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// inspectSchedulerSemanticClosure verifies the restart-safe fairness state
// without inventing a second scheduling history. A current row must identify
// a Workspace that has actually admitted at least one Run, and the two counters
// must advance together under the only authorized claim transaction.
func inspectSchedulerSemanticClosure(
	ctx context.Context,
	database semanticQueryer,
) error {
	rows, err := database.QueryContext(ctx, `
		SELECT
			s.tenant_id,
			s.workspace_id,
			s.served_units,
			s.revision,
			s.updated_at,
			EXISTS(
			    SELECT 1
			    FROM runs AS r
			    WHERE r.tenant_id=s.tenant_id
			      AND r.workspace_id=s.workspace_id
			)
		FROM workspace_scheduler_state AS s
		ORDER BY s.tenant_id COLLATE BINARY, s.workspace_id COLLATE BINARY
	`)
	if err != nil {
		return fmt.Errorf("currentbackup: read Scheduler state: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tenantID string
		var workspaceID string
		var servedUnits int64
		var revision int64
		var updatedAt int64
		var workspaceHasRun int
		if err := rows.Scan(
			&tenantID,
			&workspaceID,
			&servedUnits,
			&revision,
			&updatedAt,
			&workspaceHasRun,
		); err != nil {
			return fmt.Errorf("currentbackup: scan Scheduler state: %w", err)
		}
		if !validSchedulerOpaqueID(tenantID) ||
			!validSchedulerOpaqueID(workspaceID) {
			return schedulerIntegrity(
				"invalid Tenant/Workspace identity %q/%q",
				tenantID,
				workspaceID,
			)
		}
		if servedUnits <= 0 || revision <= 0 || updatedAt <= 0 {
			return schedulerIntegrity(
				"Workspace %q/%q has invalid counters or timestamp",
				tenantID,
				workspaceID,
			)
		}
		if servedUnits != revision {
			return schedulerIntegrity(
				"Workspace %q/%q counter/revision differ (%d/%d)",
				tenantID,
				workspaceID,
				servedUnits,
				revision,
			)
		}
		if workspaceHasRun != 1 {
			return schedulerIntegrity(
				"Workspace %q/%q has no admitted Run",
				tenantID,
				workspaceID,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("currentbackup: iterate Scheduler state: %w", err)
	}
	return nil
}

func validSchedulerOpaqueID(value string) bool {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) || value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func schedulerIntegrity(format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: Scheduler semantic closure: %s",
		ErrIntegrity,
		fmt.Sprintf(format, arguments...),
	)
}
