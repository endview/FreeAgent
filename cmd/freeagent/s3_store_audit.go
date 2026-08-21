package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/endview/freeagent/internal/s3audit"
)

const maximumS3StoreAuditExpectedCount = 1_000_000

type s3StoreAuditFunc func(
	context.Context,
	string,
	s3audit.Expectations,
) (s3audit.Report, error)

// runS3StoreAudit is an operator-only recovery observer. It opens no Runtime,
// owns no writer, and cannot submit or replay a model request.
func runS3StoreAudit(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runS3StoreAuditWithDependency(
		ctx,
		args,
		stdout,
		stderr,
		s3audit.AuditClosedStore,
	)
}

func runS3StoreAuditWithDependency(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	audit s3StoreAuditFunc,
) error {
	if ctx == nil {
		return errors.New("freeagent s3-store-audit: context is nil")
	}
	if audit == nil {
		return errors.New("freeagent s3-store-audit: audit dependency is nil")
	}
	flags := newFlagSet("s3-store-audit", stderr)
	databasePath := flags.String(
		"db",
		"",
		"closed Current Store database path",
	)
	expectedFamilies := flags.Uint64(
		"expect-families",
		0,
		"optional exact Composite family count",
	)
	expectedRuns := flags.Uint64(
		"expect-runs",
		0,
		"optional exact Composite Run count",
	)
	expectedAttempts := flags.Uint64(
		"expect-attempts",
		0,
		"optional exact model Attempt count",
	)
	requireAllTerminal := flags.Bool(
		"require-all-terminal",
		false,
		"require every observed Composite Run to be terminal",
	)
	requireAllSucceeded := flags.Bool(
		"require-all-succeeded",
		false,
		"require every observed model Attempt to be SUCCEEDED",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" {
		return errors.New("freeagent s3-store-audit: --db is required")
	}
	for name, value := range map[string]uint64{
		"--expect-families": *expectedFamilies,
		"--expect-runs":     *expectedRuns,
		"--expect-attempts": *expectedAttempts,
	} {
		if value > maximumS3StoreAuditExpectedCount {
			return fmt.Errorf(
				"freeagent s3-store-audit: %s exceeds %d",
				name,
				maximumS3StoreAuditExpectedCount,
			)
		}
	}
	report, auditErr := audit(
		ctx,
		*databasePath,
		s3audit.Expectations{
			Families:            *expectedFamilies,
			Runs:                *expectedRuns,
			Attempts:            *expectedAttempts,
			RequireAllTerminal:  *requireAllTerminal,
			RequireAllSucceeded: *requireAllSucceeded,
		},
	)
	if err := writeCommandJSON(stdout, report); err != nil {
		return fmt.Errorf("freeagent s3-store-audit: write report: %w", err)
	}
	if auditErr != nil {
		// Deliberately do not forward an underlying Store error: it may contain
		// an internal identity. Stable failure_codes are the public diagnostic.
		return errors.New(
			"freeagent s3-store-audit: audit failed; inspect failure_codes",
		)
	}
	return nil
}
