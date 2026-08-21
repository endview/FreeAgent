package corecontract

import (
	"bytes"
	"testing"
)

func TestChatContentV1RoundTripsExactCanonicalBytes(t *testing.T) {
	_, taskCanonical, err := NewTaskInputV1(TaskInputV1{
		SchemaVersion: TaskInputSchemaVersionV1,
		Text:          "Implement the parser.",
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := RestoreTaskInputV1(taskCanonical)
	if err != nil || task.Text != "Implement the parser." {
		t.Fatalf("task=%+v error=%v", task, err)
	}

	_, contextCanonical, err := NewStaticContextV1(StaticContextV1{
		SchemaVersion: StaticContextSchemaVersionV1,
		Text:          "You are a careful Go engineer.",
	})
	if err != nil {
		t.Fatal(err)
	}
	context, err := RestoreStaticContextV1(contextCanonical)
	if err != nil || context.Text != "You are a careful Go engineer." {
		t.Fatalf("context=%+v error=%v", context, err)
	}
}

func TestChatContentV1RejectsEmptyUnknownAndTamperedValues(t *testing.T) {
	if _, _, err := NewTaskInputV1(TaskInputV1{
		SchemaVersion: TaskInputSchemaVersionV1,
	}); err == nil {
		t.Fatal("empty task accepted")
	}
	if _, _, err := NewStaticContextV1(StaticContextV1{
		SchemaVersion: "static-context/v2",
		Text:          "x",
	}); err == nil {
		t.Fatal("unknown static context version accepted")
	}
	_, canonical, err := NewTaskInputV1(TaskInputV1{
		SchemaVersion: TaskInputSchemaVersionV1,
		Text:          "one",
	})
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(
		canonical,
		[]byte(`"one"`),
		[]byte(`"two"`),
		1,
	)
	if _, err := RestoreTaskInputV1(tampered); err != nil {
		// A content-addressed digest, not an embedded digest, detects this
		// semantic change. Exact restore should still accept valid canonical
		// bytes, so reaching this branch is a failure.
		t.Fatalf("valid changed content rejected: %v", err)
	}
	unknownField := []byte(
		`{"extra":true,"schema_version":"task-input/v1","text":"one"}`,
	)
	if _, err := RestoreTaskInputV1(unknownField); err == nil {
		t.Fatal("unknown task field accepted")
	}
}
