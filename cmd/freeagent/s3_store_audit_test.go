package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/s3audit"
)

func TestS3StoreAuditCommandForwardsOnlyBoundedExpectations(t *testing.T) {
	var gotPath string
	var got s3audit.Expectations
	var stdout bytes.Buffer
	err := runS3StoreAuditWithDependency(
		context.Background(),
		[]string{
			"--db", "closed.sqlite",
			"--expect-families", "3",
			"--expect-runs", "12",
			"--expect-attempts", "12",
			"--require-all-terminal",
			"--require-all-succeeded",
		},
		&stdout,
		io.Discard,
		func(
			_ context.Context,
			path string,
			expectations s3audit.Expectations,
		) (s3audit.Report, error) {
			gotPath = path
			got = expectations
			return s3audit.Report{
				SchemaVersion: s3audit.ReportSchemaVersionV2,
				Status:        "PASS",
				Observed: s3audit.ObservedReport{
					RoleCache: map[string]s3audit.RoleCacheObservation{
						"CHILD":    {},
						"REVIEWER": {},
						"ROOT":     {},
					},
				},
				FailureCodes: []string{},
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "closed.sqlite" || got.Families != 3 || got.Runs != 12 ||
		got.Attempts != 12 || !got.RequireAllTerminal ||
		!got.RequireAllSucceeded {
		t.Fatalf("path=%q expectations=%+v", gotPath, got)
	}
	var report s3audit.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != s3audit.ReportSchemaVersionV2 ||
		report.Status != "PASS" || len(report.Observed.RoleCache) != 3 {
		t.Fatalf("report=%+v", report)
	}
	for _, role := range []string{"CHILD", "REVIEWER", "ROOT"} {
		if _, found := report.Observed.RoleCache[role]; !found {
			t.Fatalf("report role_cache missing %s: %+v", role, report)
		}
	}
}

func TestS3StoreAuditCommandDoesNotExposeUnderlyingFailure(t *testing.T) {
	const privateDiagnostic = "private-run-id private-attempt-id private-payload"
	var stdout bytes.Buffer
	err := runS3StoreAuditWithDependency(
		context.Background(),
		[]string{"--db", "closed.sqlite"},
		&stdout,
		io.Discard,
		func(
			context.Context,
			string,
			s3audit.Expectations,
		) (s3audit.Report, error) {
			return s3audit.Report{
				SchemaVersion: s3audit.ReportSchemaVersionV2,
				Status:        "FAIL",
				FailureCodes:  []string{"FAMILY_PROJECTION_INVALID"},
			}, errors.New(privateDiagnostic)
		},
	)
	if err == nil || strings.Contains(err.Error(), privateDiagnostic) ||
		strings.Contains(stdout.String(), privateDiagnostic) {
		t.Fatalf("error=%v output=%s", err, stdout.String())
	}
}

func TestS3StoreAuditCommandRejectsInvalidArgumentsBeforeAudit(t *testing.T) {
	called := false
	for _, args := range [][]string{
		nil,
		{"--db", "closed.sqlite", "trailing"},
		{"--db", "closed.sqlite", "--expect-attempts", "1000001"},
	} {
		called = false
		err := runS3StoreAuditWithDependency(
			context.Background(),
			args,
			io.Discard,
			io.Discard,
			func(
				context.Context,
				string,
				s3audit.Expectations,
			) (s3audit.Report, error) {
				called = true
				return s3audit.Report{}, nil
			},
		)
		if err == nil || called {
			t.Fatalf("args=%v error=%v called=%v", args, err, called)
		}
	}
}
