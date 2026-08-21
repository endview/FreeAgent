package corecontract

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTypedRefsAreDistinctAndStrict(t *testing.T) {
	digest := strings.Repeat("a", 64)
	refs := []interface{ Validate() error }{
		AgentRef{"agent", "1", digest},
		ProfileRef{"profile", "1", digest},
		ModelProfileRef{"model-profile", "1", digest},
		WorkspaceRef{"workspace", "1", digest},
		TaskRef{"task", "1", digest},
		PolicyRef{"policy", "1", digest},
		CatalogSnapshotRef{"catalog", "1", digest},
		MemberSnapshotRef{"member", digest},
	}
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			t.Fatalf("%T rejected: %v", ref, err)
		}
	}
	if reflect.TypeOf(AgentRef{}) == reflect.TypeOf(WorkspaceRef{}) {
		t.Fatal("typed references collapsed to one interchangeable type")
	}
	if (AgentRef{" agent ", "1", digest}).Validate() == nil {
		t.Fatal("padded opaque ID accepted")
	}
	if (PolicyRef{"policy", "1", strings.Repeat("A", 64)}).Validate() == nil {
		t.Fatal("non-canonical digest accepted")
	}
	if (AgentRef{"Cafe\u0301", "1", digest}).Validate() == nil {
		t.Fatal("non-NFC typed identity accepted")
	}
}

func TestPolicyDocumentCanonicalReferenceAndRestore(t *testing.T) {
	document, ref, canonical, err := NewPolicyDocument(
		"freeagent.policy.pure-chat",
		"1",
		PolicyContext,
		json.RawMessage(`{"optional_repository_reads":false,"max_blocks":4}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	const wantCanonical = `{"body":{"max_blocks":4,"optional_repository_reads":false},"id":"freeagent.policy.pure-chat","policy_type":"CONTEXT","version":"1"}`
	if string(canonical) != wantCanonical {
		t.Fatalf("canonical=%s", canonical)
	}
	if ref.ID != document.ID ||
		ref.Version != document.Version ||
		len(ref.Digest) != 64 {
		t.Fatalf("ref=%+v document=%+v", ref, document)
	}
	restored, err := RestorePolicyDocument(canonical, ref)
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != document.ID ||
		!bytes.Equal(restored.Body, document.Body) {
		t.Fatalf("restored=%+v", restored)
	}
}

func TestPolicyDocumentRejectsAliasAndReferenceDrift(t *testing.T) {
	if _, _, _, err := NewPolicyDocument(
		"freeagent.policy.invalid",
		"1",
		PolicyContext,
		json.RawMessage(`[]`),
	); err == nil {
		t.Fatal("non-object policy body accepted")
	}
	_, ref, canonical, err := NewPolicyDocument(
		"freeagent.policy.cost",
		"1",
		PolicyCost,
		json.RawMessage(`{"currency":"USD"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	ref.Digest = strings.Repeat("b", 64)
	if _, err := RestorePolicyDocument(canonical, ref); err == nil {
		t.Fatal("drifted policy reference accepted")
	}
	unknown := bytes.Replace(
		canonical,
		[]byte(`"version":"1"`),
		[]byte(`"alias":"cost","version":"1"`),
		1,
	)
	if _, err := RestorePolicyDocument(unknown, ref); err == nil {
		t.Fatal("alias-bearing policy document accepted")
	}
}
