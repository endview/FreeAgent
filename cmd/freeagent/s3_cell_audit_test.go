package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/s3cellaudit"
)

func TestS3CellAuditCommandWritesAggregateReport(t *testing.T) {
	var gotPath string
	var stdout bytes.Buffer
	err := runS3CellAuditWithDependency(
		context.Background(),
		[]string{"--cell", "completed-cell"},
		&stdout,
		io.Discard,
		func(_ context.Context, path string) (s3cellaudit.Report, error) {
			gotPath = path
			return s3cellaudit.Report{
				SchemaVersion: s3cellaudit.ReportSchemaVersionV2,
				Status:        "PASS", FailureCodes: []string{},
			}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "completed-cell" {
		t.Fatalf("path=%q", gotPath)
	}
	var report s3cellaudit.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "PASS" || report.SchemaVersion != s3cellaudit.ReportSchemaVersionV2 {
		t.Fatalf("report=%+v", report)
	}
}

func TestS3CellAuditCommandDoesNotExposeUnderlyingFailure(t *testing.T) {
	private := "private-id prompt reply result raw-receipt Authorization"
	var stdout bytes.Buffer
	err := runS3CellAuditWithDependency(
		context.Background(),
		[]string{"--cell", "completed-cell"},
		&stdout,
		io.Discard,
		func(context.Context, string) (s3cellaudit.Report, error) {
			return s3cellaudit.Report{
				SchemaVersion: s3cellaudit.ReportSchemaVersionV2,
				Status:        "FAIL", FailureCodes: []string{"REPORT_JSON_INVALID"},
			}, errors.New(private)
		},
	)
	if err == nil || strings.Contains(err.Error(), private) || strings.Contains(stdout.String(), private) {
		t.Fatalf("error=%v output=%s", err, stdout.String())
	}
}

func TestS3CellAuditCommandRejectsInvalidArgumentsBeforeAudit(t *testing.T) {
	for _, args := range [][]string{nil, {"--cell", "completed-cell", "trailing"}} {
		called := false
		err := runS3CellAuditWithDependency(
			context.Background(), args, io.Discard, io.Discard,
			func(context.Context, string) (s3cellaudit.Report, error) {
				called = true
				return s3cellaudit.Report{}, nil
			},
		)
		if err == nil || called {
			t.Fatalf("args=%v error=%v called=%v", args, err, called)
		}
	}
}
