package corecontract

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompositeRepairActivatedEventV1StrictRoundTrip(t *testing.T) {
	input := validCompositeRepairActivatedEventV1()
	frozen, canonical, err := NewCompositeRepairActivatedEventV1(input)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"parent_slot_id":"__repair_child_r1__slot","repair_round":1,"root_manifest_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","root_run_id":"root-run","run_id":"repair-run","schema_version":"composite-repair-activated-event/v1","source_verdict_ref":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`
	if string(canonical) != want {
		t.Fatalf("canonical activation wire = %s", canonical)
	}
	restored, err := RestoreCompositeRepairActivatedEventV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(frozen, restored) {
		t.Fatalf("activation round trip changed payload: %+v -> %+v", frozen, restored)
	}
	if CompositeRepairActivatedEventKind != "COMPOSITE_REPAIR_ACTIVATED" {
		t.Fatalf("activation event kind = %q", CompositeRepairActivatedEventKind)
	}
}

func TestCompositeRepairActivatedEventV1RejectsInvalidIdentityAndRound(t *testing.T) {
	tests := map[string]func(*CompositeRepairActivatedEventV1){
		"schema": func(value *CompositeRepairActivatedEventV1) {
			value.SchemaVersion = "v2"
		},
		"RunID": func(value *CompositeRepairActivatedEventV1) {
			value.RunID = ""
		},
		"RootRunID": func(value *CompositeRepairActivatedEventV1) {
			value.RootRunID = ""
		},
		"same Run": func(value *CompositeRepairActivatedEventV1) {
			value.RunID = value.RootRunID
		},
		"root digest": func(value *CompositeRepairActivatedEventV1) {
			value.RootManifestDigest = "bad"
		},
		"physical slot": func(value *CompositeRepairActivatedEventV1) {
			value.ParentSlotID = ""
		},
		"initial Reviewer slot": func(value *CompositeRepairActivatedEventV1) {
			value.ParentSlotID = CompositeReviewerParentSlotIDV1
		},
		"round zero": func(value *CompositeRepairActivatedEventV1) {
			value.RepairRound = 0
		},
		"round two": func(value *CompositeRepairActivatedEventV1) {
			value.RepairRound = 2
		},
		"verdict ref": func(value *CompositeRepairActivatedEventV1) {
			value.SourceVerdictRef = "bad"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := validCompositeRepairActivatedEventV1()
			mutate(&input)
			if _, _, err := NewCompositeRepairActivatedEventV1(input); err == nil {
				t.Fatal("invalid repair activation was accepted")
			}
		})
	}
}

func TestRestoreCompositeRepairActivatedEventV1RejectsNonCanonicalAndUnknown(t *testing.T) {
	_, canonical, err := NewCompositeRepairActivatedEventV1(
		validCompositeRepairActivatedEventV1(),
	)
	if err != nil {
		t.Fatal(err)
	}
	tampered := [][]byte{
		append(append([]byte(nil), canonical...), '\n'),
		[]byte(strings.Replace(
			string(canonical),
			`{"parent_slot_id":`,
			`{"extra":true,"parent_slot_id":`,
			1,
		)),
		[]byte(strings.Replace(
			string(canonical),
			`"repair_round":1`,
			`"repair_round":0`,
			1,
		)),
	}
	for index, wire := range tampered {
		if _, err := RestoreCompositeRepairActivatedEventV1(wire); err == nil {
			t.Fatalf("tampered activation wire %d was accepted: %s", index, wire)
		}
	}
}

func validCompositeRepairActivatedEventV1() CompositeRepairActivatedEventV1 {
	return CompositeRepairActivatedEventV1{
		SchemaVersion:      CompositeRepairActivatedEventSchemaVersionV1,
		RunID:              "repair-run",
		RootRunID:          "root-run",
		RootManifestDigest: strings.Repeat("a", 64),
		ParentSlotID:       "__repair_child_r1__slot",
		RepairRound:        CompositeRepairRoundOneV1,
		SourceVerdictRef:   strings.Repeat("b", 64),
	}
}
