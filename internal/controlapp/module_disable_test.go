package controlapp

import (
	"bytes"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/moduledisablecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleDisableDryRunBodyCanonicalAndStrictRestoreV1(t *testing.T) {
	input := testModuleDisableBodyV1()
	frozen, canonical, digest, err := NewModuleDisableDryRunBodyV1(input)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical := `{"binding_target":{"kind":"PROFILE","profile_id":"profile-a"},"expected_pointer_revision":7,"instance_id":"instance-a","port":{"exact_version":"v1","name":"action.provider"},"schema_version":"control-module-disable-dry-run-input/v1"}`
	if string(canonical) != wantCanonical {
		t.Fatalf("canonical=%s", canonical)
	}
	wantDigest := "7fa5d6357cad71962d0f17a51ea7e36d41551391c30a0015c920f5df9647d0d5"
	if digest != wantDigest {
		t.Fatalf("digest=%s want=%s", digest, wantDigest)
	}
	restored, err := RestoreModuleDisableDryRunBodyV1(canonical, digest)
	if err != nil || restored != frozen {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	canonical[0] ^= 0xff
	if restored.SchemaVersion != ModuleDisableDryRunBodySchemaVersionV1 {
		t.Fatal("restored body aliases canonical bytes")
	}

	for name, wire := range map[string][]byte{
		"unknown":      []byte(`{"binding_target":{"kind":"PROFILE","profile_id":"profile-a"},"expected_pointer_revision":7,"instance_id":"instance-a","port":{"exact_version":"v1","name":"action.provider"},"schema_version":"control-module-disable-dry-run-input/v1","unknown":true}`),
		"duplicate":    []byte(`{"binding_target":{"kind":"PROFILE","profile_id":"profile-a"},"expected_pointer_revision":7,"expected_pointer_revision":7,"instance_id":"instance-a","port":{"exact_version":"v1","name":"action.provider"},"schema_version":"control-module-disable-dry-run-input/v1"}`),
		"noncanonical": []byte(" " + wantCanonical),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RestoreModuleDisableDryRunBodyV1(
				wire,
				moduleapi.Digest(
					moduledisablecontract.ModuleDisableDryRunInputDigestDomainV1,
					wire,
				),
			); err == nil {
				t.Fatal("invalid wire restored")
			}
		})
	}
}

func TestModuleDisableDryRunBodyTargetMatrixV1(t *testing.T) {
	base := testModuleDisableBodyV1()
	tests := []func(*ModuleDisableDryRunBodyV1){
		func(value *ModuleDisableDryRunBodyV1) { value.SchemaVersion = "v2" },
		func(value *ModuleDisableDryRunBodyV1) { value.ExpectedPointerRevision = 0 },
		func(value *ModuleDisableDryRunBodyV1) { value.InstanceID = "" },
		func(value *ModuleDisableDryRunBodyV1) { value.BindingTarget.ProfileID = "" },
		func(value *ModuleDisableDryRunBodyV1) { value.BindingTarget.WorkspaceID = "workspace-a" },
		func(value *ModuleDisableDryRunBodyV1) { value.Port = moduleapi.PortRef{} },
		func(value *ModuleDisableDryRunBodyV1) {
			value.BindingTarget = ModuleBindingTargetV1{
				Kind:        ModuleBindingTargetWorkspaceChannelEndpointV1,
				WorkspaceID: "workspace-a",
				EndpointID:  "endpoint-a",
			}
		},
	}
	for index, mutate := range tests {
		value := base
		mutate(&value)
		if _, _, _, err := NewModuleDisableDryRunBodyV1(value); err == nil {
			t.Fatalf("case %d accepted invalid body: %+v", index, value)
		}
	}
	channel := base
	channel.BindingTarget = ModuleBindingTargetV1{
		Kind:        ModuleBindingTargetWorkspaceChannelEndpointV1,
		WorkspaceID: "workspace-a",
		EndpointID:  "endpoint-a",
	}
	channel.Port = moduleapi.PortRef{
		Name:         moduleapi.PortNameChannelTransport,
		ExactVersion: moduleapi.PortVersionV1,
	}
	if _, _, _, err := NewModuleDisableDryRunBodyV1(channel); err != nil {
		t.Fatalf("valid Channel body: %v", err)
	}
}

func TestModuleDisableDryRunReceiptIsDerivedFromExactRequestV1(t *testing.T) {
	body, _, inputDigest, err := NewModuleDisableDryRunBodyV1(testModuleDisableBodyV1())
	if err != nil || body.SchemaVersion == "" {
		t.Fatal(err)
	}
	scope := controlapicontract.ControlScopeV1{
		SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
		Kind:          controlapicontract.ScopeTenantV1,
		TenantID:      "tenant-a",
	}
	request, requestCanonical, requestDigest, err := controlapicontract.NewControlOperationRequestV1(
		controlapicontract.ControlOperationRequestV1{
			SchemaVersion: controlapicontract.ControlOperationRequestSchemaVersionV1,
			PrincipalID:   "operator-local",
			Capability:    controlapicontract.CapabilityOperateModulesV1,
			Scope:         scope,
			Operation:     controlapicontract.OperationModuleDisableV1,
			Intent:        controlapicontract.OperationIntentDryRunV1,
			InputDigest:   inputDigest,
			ExpectedRef: controlapicontract.ExpectedResourceRefV1{
				Kind:       controlapicontract.ResourcePublishedPointerV1,
				ResourceID: "tenant-a",
				Revision:   7,
				Digest:     moduleapi.Digest("test-pointer", []byte("7")),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, canonical, digest, err := NewModuleDisableDryRunReceiptV1(
		requestCanonical,
		requestDigest,
		1_786_595_200_000_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RequestDigest != requestDigest || receipt.PrincipalID != request.PrincipalID ||
		receipt.ScopeDigest != request.ScopeDigest || receipt.Operation != request.Operation ||
		receipt.Intent != request.Intent || receipt.Status != controlapicontract.OperationStatusDryRunV1 ||
		receipt.PreRef == nil || *receipt.PreRef != request.ExpectedRef ||
		receipt.PostRef != nil || receipt.DomainReceipt != nil ||
		receipt.ReplayDisposition != controlapicontract.ReplayNoRetryV1 {
		t.Fatalf("receipt drifted from request: %+v", receipt)
	}
	if _, err := controlapicontract.RestoreControlOperationReceiptV1(canonical, digest); err != nil {
		t.Fatal(err)
	}
	mutated := bytes.Clone(requestCanonical)
	mutated[len(mutated)-1] ^= 1
	if _, _, _, err := NewModuleDisableDryRunReceiptV1(
		mutated,
		requestDigest,
		1_786_595_200_000_000,
	); err == nil {
		t.Fatal("receipt accepted request bytes that differ from RequestDigest")
	}
}

func testModuleDisableBodyV1() ModuleDisableDryRunBodyV1 {
	return ModuleDisableDryRunBodyV1{
		SchemaVersion:           ModuleDisableDryRunBodySchemaVersionV1,
		ExpectedPointerRevision: 7,
		BindingTarget: ModuleBindingTargetV1{
			Kind:      ModuleBindingTargetProfileV1,
			ProfileID: "profile-a",
		},
		InstanceID: "instance-a",
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameActionProvider,
			ExactVersion: moduleapi.PortVersionV1,
		},
	}
}
