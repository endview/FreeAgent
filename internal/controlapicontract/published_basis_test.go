package controlapicontract

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestPublishedBasisRefCanonicalRoundTripV1(t *testing.T) {
	input := publishedBasisRefFixtureV1()
	frozen, canonical, digest, err := NewPublishedBasisRefV1(input)
	if err != nil {
		t.Fatalf("NewPublishedBasisRefV1: %v", err)
	}
	const wantCanonical = `{"catalog":{"digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","id":"catalog-basis-9","revision":9},"control":{"digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","id":"control-basis-9","revision":9},"pointer_revision":9,"tenant_id":"tenant-basis"}`
	if string(canonical) != wantCanonical {
		t.Fatalf("canonical=%s", canonical)
	}
	if digest != moduleapi.Digest(
		"freeagent.control-published-pointer-ref/v1",
		canonical,
	) || digest != "40181ee21a6ef36ef7447880f6898eba0bc2eca6c03a262e34681062cb3ce376" {
		t.Fatalf("published basis domain digest=%q", digest)
	}
	if !reflect.DeepEqual(frozen, input) {
		t.Fatalf("frozen=%+v", frozen)
	}
	restored, err := RestorePublishedBasisRefV1(canonical, digest)
	if err != nil {
		t.Fatalf("RestorePublishedBasisRefV1: %v", err)
	}
	if !reflect.DeepEqual(restored, input) {
		t.Fatalf("restored=%+v", restored)
	}

	callerCanonical := bytes.Clone(canonical)
	callerCanonical[0] = '['
	again, err := RestorePublishedBasisRefV1(canonical, digest)
	if err != nil || !reflect.DeepEqual(again, input) {
		t.Fatalf("canonical aliases prior restore input: restored=%+v err=%v", again, err)
	}
}

func TestRestorePublishedBasisRefRejectsNonExactWireV1(t *testing.T) {
	_, canonical, digest, err := NewPublishedBasisRefV1(publishedBasisRefFixtureV1())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutated := range map[string][]byte{
		"empty":         nil,
		"unknown field": bytes.Replace(canonical, []byte(`{"catalog"`), []byte(`{"automatic":true,"catalog"`), 1),
		"trailing JSON": append(bytes.Clone(canonical), []byte(`{}`)...),
		"non canonical": append([]byte(" "), canonical...),
		"oversize":      bytes.Repeat([]byte{'x'}, MaxPublishedBasisRefWireBytesV1+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RestorePublishedBasisRefV1(mutated, digest); err == nil {
				t.Fatal("mutated published basis was accepted")
			}
		})
	}
	if _, err := RestorePublishedBasisRefV1(canonical, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong published basis digest was accepted")
	}
}

func TestPublishedBasisRefRejectsInvalidIdentityMatrixV1(t *testing.T) {
	base := publishedBasisRefFixtureV1()
	tests := map[string]func(*PublishedBasisRefV1){
		"tenant":           func(value *PublishedBasisRefV1) { value.TenantID = " tenant" },
		"pointer revision": func(value *PublishedBasisRefV1) { value.PointerRevision = 0 },
		"control ID":       func(value *PublishedBasisRefV1) { value.Control.ID = "" },
		"control revision": func(value *PublishedBasisRefV1) { value.Control.Revision = 0 },
		"control digest":   func(value *PublishedBasisRefV1) { value.Control.Digest = "bad" },
		"catalog ID":       func(value *PublishedBasisRefV1) { value.Catalog.ID = "" },
		"catalog revision": func(value *PublishedBasisRefV1) { value.Catalog.Revision = 0 },
		"catalog digest":   func(value *PublishedBasisRefV1) { value.Catalog.Digest = "bad" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			if _, _, _, err := NewPublishedBasisRefV1(input); err == nil {
				t.Fatal("invalid published basis was accepted")
			}
		})
	}
}

func publishedBasisRefFixtureV1() PublishedBasisRefV1 {
	return PublishedBasisRefV1{
		TenantID:        "tenant-basis",
		PointerRevision: 9,
		Control: RevisionedDigestRefV1{
			ID: "control-basis-9", Revision: 9, Digest: strings.Repeat("a", 64),
		},
		Catalog: RevisionedDigestRefV1{
			ID: "catalog-basis-9", Revision: 9, Digest: strings.Repeat("b", 64),
		},
	}
}
