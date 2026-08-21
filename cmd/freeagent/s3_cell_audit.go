package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/endview/freeagent/internal/s3cellaudit"
)

type s3CellAuditFunc func(context.Context, string) (s3cellaudit.Report, error)

// runS3CellAudit is an operator-only observer. It reads immutable evidence,
// invokes the existing read-only backup verifier, and cross-checks the verified
// archive database through the read-only Store auditor. It cannot open a
// writable Store, compose a Runtime, or dispatch a model call.
func runS3CellAudit(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runS3CellAuditWithDependency(
		ctx,
		args,
		stdout,
		stderr,
		s3cellaudit.AuditCell,
	)
}

func runS3CellAuditWithDependency(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	audit s3CellAuditFunc,
) error {
	if ctx == nil {
		return errors.New("freeagent s3-cell-audit: context is nil")
	}
	if audit == nil {
		return errors.New("freeagent s3-cell-audit: audit dependency is nil")
	}
	flags := newFlagSet("s3-cell-audit", stderr)
	cellPath := flags.String("cell", "", "S3-C evidence cell directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*cellPath) == "" {
		return errors.New("freeagent s3-cell-audit: --cell is required")
	}
	report, auditErr := audit(ctx, *cellPath)
	if err := writeCommandJSON(stdout, report); err != nil {
		return fmt.Errorf("freeagent s3-cell-audit: write report: %w", err)
	}
	if auditErr != nil {
		// Source parser and validation errors may contain source-controlled text.
		// Only stable aggregate failure_codes cross the CLI boundary.
		return errors.New(
			"freeagent s3-cell-audit: audit failed; inspect failure_codes",
		)
	}
	return nil
}
