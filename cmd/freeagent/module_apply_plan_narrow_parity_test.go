package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestProfileContextDisableNarrowPlanMatchesBroadApplyPlanExactlyV1(t *testing.T) {
	input := moduleapplyplan.ProfileContextDisableInputV1{
		TenantID:                "tenant-1",
		ExpectedPointerRevision: 7,
		ProfileID:               "profile-1",
		InstanceID:              "instance-1",
	}
	narrow, narrowCanonical, narrowDigest, err :=
		moduleapplyplan.FreezeProfileContextDisableV1(input)
	if err != nil {
		t.Fatal(err)
	}
	broad, broadCanonical, broadDigest, err := freezeModuleApplyPlanValueV1(
		moduleApplyPlanV1{
			SchemaVersion:           moduleApplyPlanSchemaV1,
			DesiredState:            moduleApplyDisabledV1,
			TenantID:                input.TenantID,
			ExpectedPointerRevision: input.ExpectedPointerRevision,
			BindingTarget: moduleApplyBindingTargetV1{
				Kind:      moduleApplyBindingTargetProfileV1,
				ProfileID: input.ProfileID,
			},
			InstanceID: input.InstanceID,
			Port: moduleapi.PortRef{
				Name:         moduleapi.PortNameContextProvide,
				ExactVersion: moduleapi.PortVersionV1,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(narrowCanonical, broadCanonical) || narrowDigest != broadDigest {
		t.Fatalf(
			"narrow and broad identity differ\nnarrow=%s %s\nbroad=%s %s",
			narrowCanonical,
			narrowDigest,
			broadCanonical,
			broadDigest,
		)
	}
	if narrow.TenantID != broad.TenantID ||
		narrow.ExpectedPointerRevision != broad.ExpectedPointerRevision ||
		narrow.BindingTarget.ProfileID != broad.BindingTarget.ProfileID ||
		narrow.InstanceID != broad.InstanceID || narrow.Port != broad.Port {
		t.Fatalf("narrow=%+v broad=%+v", narrow, broad)
	}
	if narrowDigest != "6918ee2da16a405e6e5f2f9947c9c56435a5797e224fc7314d452d79124b29ab" {
		t.Fatalf("digest canary = %q", narrowDigest)
	}
	ids, err := moduleapplyplan.DeriveCandidateIDsV1(narrowDigest)
	if err != nil {
		t.Fatal(err)
	}
	if ids.ControlSnapshotID != moduleApplyControlIDPrefixV1+broadDigest ||
		ids.CatalogGenerationID != moduleApplyCatalogIDPrefixV1+broadDigest {
		t.Fatalf("narrow candidate IDs = %+v", ids)
	}
	if moduleApplyControlIDPrefixV1 !=
		moduleapplyplan.CandidateControlSnapshotIDPrefixV1 ||
		moduleApplyCatalogIDPrefixV1 !=
			moduleapplyplan.CandidateCatalogGenerationIDPrefixV1 {
		t.Fatal("cmd candidate ID prefixes drifted from the shared policy")
	}

	disableInput, err := moduleDisableDryRunInputV1(broad, broadDigest)
	if err != nil {
		t.Fatal(err)
	}
	if disableInput.CandidateControlSnapshotID != ids.ControlSnapshotID ||
		disableInput.CandidateCatalogGenerationID != ids.CatalogGenerationID {
		t.Fatalf("disable projection candidate IDs = %+v want %+v", disableInput, ids)
	}

	publication, err := buildModuleApplyPublicationV1(
		broadDigest,
		controlcontract.PublishedBasis{PointerRevision: input.ExpectedPointerRevision},
		controlcontract.ControlSnapshot{
			SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV2,
			SnapshotID:    "current-control",
			TenantID:      input.TenantID,
			Revision:      input.ExpectedPointerRevision,
		},
		controlcontract.CatalogGeneration{
			SchemaVersion: controlcontract.CatalogGenerationSchemaVersionV1,
			GenerationID:  "current-catalog",
			Generation:    input.ExpectedPointerRevision,
			TenantID:      input.TenantID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if publication.ControlRef.SnapshotID != ids.ControlSnapshotID ||
		publication.CatalogRef.GenerationID != ids.CatalogGenerationID {
		t.Fatalf("publication candidate IDs = %+v want %+v", publication, ids)
	}

	const invalidDigest = "not-a-lowercase-sha256"
	if _, err := moduleDisableDryRunInputV1(broad, invalidDigest); err == nil ||
		moduleApplyFailureCodeOfV1(err) != moduleApplyFailurePlanInvalid {
		t.Fatalf("disable invalid candidate digest error = %v", err)
	}
	if _, err := buildModuleApplyPublicationV1(
		invalidDigest,
		controlcontract.PublishedBasis{},
		controlcontract.ControlSnapshot{},
		controlcontract.CatalogGeneration{},
	); err == nil {
		t.Fatal("publication accepted invalid candidate digest")
	}
	if _, err := evaluateObservedModuleApplyV1(
		context.Background(),
		nil,
		"",
		moduleApplyPlanV1{DesiredState: moduleApplyEnabledV1},
		invalidDigest,
		controlcontract.PublishedBasis{},
		controlcontract.ControlSnapshot{},
		controlcontract.CatalogGeneration{},
	); err == nil || moduleApplyFailureCodeOfV1(err) != moduleApplyFailurePlanInvalid {
		t.Fatalf("evaluator invalid candidate digest error = %v", err)
	}
}
