package corecontract

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunCancellationRequestV1RoundTrip(t *testing.T) {
	request, canonical, err := NewRunCancellationRequestV1(
		RunCancellationRequestV1{
			SchemaVersion:      RunCancellationRequestSchemaVersionV1,
			RootRunID:          "root-1",
			RootManifestDigest: strings.Repeat("a", 64),
			Scope:              CancellationScopeFamilyV1,
			ReasonCode:         CancellationReasonUserRequestV1,
		},
	)
	if err != nil {
		t.Fatalf("NewRunCancellationRequestV1: %v", err)
	}
	const want = `{"reason_code":"USER_REQUEST","root_manifest_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","root_run_id":"root-1","schema_version":"cancel-request/v1","scope":"family"}`
	if string(canonical) != want {
		t.Fatalf("canonical=%s want %s", canonical, want)
	}
	restored, err := RestoreRunCancellationRequestV1(canonical)
	if err != nil {
		t.Fatalf("RestoreRunCancellationRequestV1: %v", err)
	}
	if restored != request {
		t.Fatalf("restored=%+v want %+v", restored, request)
	}
	if _, err := RestoreRunCancellationRequestV1(
		append(bytes.Clone(canonical), ' '),
	); err == nil {
		t.Fatal("non-canonical cancellation was accepted")
	}
}

func TestRunCancellationRequestV1RejectsOldOrUnboundedWire(t *testing.T) {
	valid := RunCancellationRequestV1{
		SchemaVersion:      RunCancellationRequestSchemaVersionV1,
		RootRunID:          "run-1",
		RootManifestDigest: strings.Repeat("b", 64),
		Scope:              CancellationScopeRunV1,
		ReasonCode:         CancellationReasonOperatorRequestV1,
	}
	for _, test := range []struct {
		name   string
		mutate func(*RunCancellationRequestV1)
	}{
		{"old schema", func(value *RunCancellationRequestV1) { value.SchemaVersion = "composite-cancellation-request/v1" }},
		{"missing root", func(value *RunCancellationRequestV1) { value.RootRunID = "" }},
		{"invalid digest", func(value *RunCancellationRequestV1) { value.RootManifestDigest = "digest" }},
		{"inherited scope", func(value *RunCancellationRequestV1) { value.Scope = CancellationScopeInheritedV1 }},
		{"unknown reason", func(value *RunCancellationRequestV1) { value.ReasonCode = "FREE_FORM_REASON" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			if _, _, err := NewRunCancellationRequestV1(input); err == nil {
				t.Fatal("invalid cancellation request was accepted")
			}
		})
	}

	for _, oldWire := range []string{
		`{"reason_code":"USER_REQUEST","request_id":"legacy","root_manifest_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","root_run_id":"run-1","schema_version":"cancel-request/v1","scope":"run"}`,
		`{"reason_code":"USER_REQUEST","requested_at":"2026-08-04T00:00:00Z","root_manifest_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","root_run_id":"run-1","schema_version":"cancel-request/v1","scope":"run"}`,
	} {
		if _, err := RestoreRunCancellationRequestV1([]byte(oldWire)); err == nil {
			t.Fatalf("wire with forbidden dynamic metadata was accepted: %s", oldWire)
		}
	}
}
