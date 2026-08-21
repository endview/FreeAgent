package corecontract

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestWorkspaceTransferGrantV1CanonicalRoundTripAndDefensiveCopy(t *testing.T) {
	input := workspaceTransferTestGrant(
		"grant-root",
		workspaceTransferTestWorkspace("workspace-root", "v1", "1"),
		workspaceTransferTestWorkspace("workspace-specialist", "v7", "2"),
	)
	input.SendPayloadKinds = []WorkspaceTransferPayloadKindV1{
		WorkspaceTransferPayloadTaskSummaryV1,
		WorkspaceTransferPayloadSpecialistResultV1,
	}
	input.ReceivePayloadKinds = nil
	input.MaxReceivePayloadBytes = 0

	frozen, canonical, digest, err := NewWorkspaceTransferGrantV1(input)
	if err != nil {
		t.Fatalf("NewWorkspaceTransferGrantV1: %v", err)
	}
	if got, want := frozen.SendPayloadKinds, []WorkspaceTransferPayloadKindV1{
		WorkspaceTransferPayloadSpecialistResultV1,
		WorkspaceTransferPayloadTaskSummaryV1,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("send kinds=%v want %v", got, want)
	}
	if frozen.ReceivePayloadKinds == nil || len(frozen.ReceivePayloadKinds) != 0 {
		t.Fatalf("empty receive kinds were not normalized to []: %#v", frozen.ReceivePayloadKinds)
	}
	if bytes.Contains(canonical, []byte(`"receive_payload_kinds":null`)) ||
		!bytes.Contains(canonical, []byte(`"receive_payload_kinds":[]`)) {
		t.Fatalf("grant did not freeze an explicit empty array: %s", canonical)
	}
	if digest != moduleapi.Digest(workspaceTransferGrantDigestDomainV1, canonical) {
		t.Fatalf("grant digest does not bind canonical bytes: %s", digest)
	}

	// Mutating caller-owned slices must not mutate the frozen value or bytes.
	input.SendPayloadKinds[0] = "MUTATED"
	if frozen.SendPayloadKinds[1] != WorkspaceTransferPayloadTaskSummaryV1 ||
		bytes.Contains(canonical, []byte("MUTATED")) {
		t.Fatal("grant retained caller-owned slice storage")
	}

	restored, err := RestoreWorkspaceTransferGrantV1(canonical, digest)
	if err != nil {
		t.Fatalf("RestoreWorkspaceTransferGrantV1: %v", err)
	}
	if !reflect.DeepEqual(restored, frozen) {
		t.Fatalf("restored=%+v want %+v", restored, frozen)
	}
	if _, err := RestoreWorkspaceTransferGrantV1(
		canonical,
		workspaceTransferDifferentDigest(digest),
	); err == nil {
		t.Fatal("grant accepted a different digest")
	}
	if _, err := RestoreWorkspaceTransferGrantV1(
		append(bytes.Clone(canonical), ' '),
		digest,
	); err == nil {
		t.Fatal("grant accepted non-canonical bytes")
	}

	unknown := workspaceTransferCanonicalWithUnknown(t, canonical)
	if _, err := RestoreWorkspaceTransferGrantV1(
		unknown,
		moduleapi.Digest(workspaceTransferGrantDigestDomainV1, unknown),
	); err == nil {
		t.Fatal("grant accepted an unknown field")
	}

	// RFC 8785 preserves array order. A canonical JSON object whose set is not
	// frozen in binary order must still fail the contract-level canonical test.
	unsorted := bytes.Replace(
		canonical,
		[]byte(`"send_payload_kinds":["SPECIALIST_RESULT","TASK_SUMMARY"]`),
		[]byte(`"send_payload_kinds":["TASK_SUMMARY","SPECIALIST_RESULT"]`),
		1,
	)
	if bytes.Equal(unsorted, canonical) {
		t.Fatal("test did not construct an unsorted payload-kind wire")
	}
	if _, err := RestoreWorkspaceTransferGrantV1(
		unsorted,
		moduleapi.Digest(workspaceTransferGrantDigestDomainV1, unsorted),
	); err == nil {
		t.Fatal("grant accepted a canonically encoded but unfrozen kind order")
	}
}

func TestWorkspaceTransferGrantV1RejectsInvalidAndUnboundedValues(t *testing.T) {
	root := workspaceTransferTestWorkspace("workspace-root", "v1", "1")
	peer := workspaceTransferTestWorkspace("workspace-specialist", "v1", "2")
	valid := func() WorkspaceTransferGrantV1 {
		return workspaceTransferTestGrant("grant-root", root, peer)
	}
	for _, test := range []struct {
		name   string
		mutate func(*WorkspaceTransferGrantV1)
	}{
		{"schema", func(value *WorkspaceTransferGrantV1) { value.SchemaVersion = "workspace-transfer-grant/v2" }},
		{"grant ID", func(value *WorkspaceTransferGrantV1) { value.GrantID = "" }},
		{"grant ID overflow", func(value *WorkspaceTransferGrantV1) { value.GrantID = strings.Repeat("g", maxOpaqueIDBytes+1) }},
		{"tenant ID", func(value *WorkspaceTransferGrantV1) { value.TenantID = "" }},
		{"tenant ID overflow", func(value *WorkspaceTransferGrantV1) { value.TenantID = strings.Repeat("t", maxOpaqueIDBytes+1) }},
		{"owner ref", func(value *WorkspaceTransferGrantV1) { value.Workspace.Digest = "bad" }},
		{"peer ref", func(value *WorkspaceTransferGrantV1) { value.PeerWorkspace.Version = "" }},
		{"same Workspace ID", func(value *WorkspaceTransferGrantV1) { value.PeerWorkspace.ID = value.Workspace.ID }},
		{"zero revision", func(value *WorkspaceTransferGrantV1) { value.Revision = 0 }},
		{"unsafe revision", func(value *WorkspaceTransferGrantV1) { value.Revision = maximumJSONSafeIntegerV1 + 1 }},
		{"zero send limit", func(value *WorkspaceTransferGrantV1) { value.MaxSendPayloadBytes = 0 }},
		{"send limit overflow", func(value *WorkspaceTransferGrantV1) {
			value.MaxSendPayloadBytes = WorkspaceTransferMaximumPayloadBytesV1 + 1
		}},
		{"zero receive limit", func(value *WorkspaceTransferGrantV1) { value.MaxReceivePayloadBytes = 0 }},
		{"receive limit overflow", func(value *WorkspaceTransferGrantV1) {
			value.MaxReceivePayloadBytes = WorkspaceTransferMaximumPayloadBytesV1 + 1
		}},
		{"both directions denied", func(value *WorkspaceTransferGrantV1) {
			value.SendPayloadKinds = nil
			value.ReceivePayloadKinds = nil
			value.MaxSendPayloadBytes = 0
			value.MaxReceivePayloadBytes = 0
		}},
		{"empty send with nonzero limit", func(value *WorkspaceTransferGrantV1) { value.SendPayloadKinds = nil }},
		{"empty receive with nonzero limit", func(value *WorkspaceTransferGrantV1) { value.ReceivePayloadKinds = nil }},
		{"duplicate send kind", func(value *WorkspaceTransferGrantV1) {
			value.SendPayloadKinds = []WorkspaceTransferPayloadKindV1{
				WorkspaceTransferPayloadTaskSummaryV1,
				WorkspaceTransferPayloadTaskSummaryV1,
			}
		}},
		{"unsupported receive kind", func(value *WorkspaceTransferGrantV1) {
			value.ReceivePayloadKinds = []WorkspaceTransferPayloadKindV1{"HISTORY"}
		}},
		{"kind list overflow", func(value *WorkspaceTransferGrantV1) {
			value.SendPayloadKinds = []WorkspaceTransferPayloadKindV1{
				WorkspaceTransferPayloadTaskSummaryV1,
				WorkspaceTransferPayloadSpecialistResultV1,
				WorkspaceTransferPayloadTaskSummaryV1,
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := valid()
			test.mutate(&input)
			if _, _, _, err := NewWorkspaceTransferGrantV1(input); err == nil {
				t.Fatal("invalid Workspace transfer grant was accepted")
			}
		})
	}
}

func TestWorkspaceTransferPlanV1RequiresCompleteBilateralAuthorization(
	t *testing.T,
) {
	rootWorkspace := workspaceTransferTestWorkspace("workspace-root", "v1", "1")
	targetWorkspace := workspaceTransferTestWorkspace("workspace-target", "v2", "2")
	root := workspaceTransferTestGrant(
		"grant-root-target",
		rootWorkspace,
		targetWorkspace,
	)
	target := workspaceTransferTestGrant(
		"grant-target-root",
		targetWorkspace,
		rootWorkspace,
	)
	plan, err := NewWorkspaceTransferPlanV1(root, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.ValidateAgainstGrantsV1(root, target); err != nil {
		t.Fatalf("plan/grant closure: %v", err)
	}
	if plan.RootWorkspace != rootWorkspace ||
		plan.TargetWorkspace != targetWorkspace ||
		plan.RootGrantID != root.GrantID ||
		plan.TargetGrantID != target.GrantID {
		t.Fatalf("transfer plan=%+v", plan)
	}

	for _, test := range []struct {
		name   string
		mutate func(*WorkspaceTransferGrantV1, *WorkspaceTransferGrantV1)
	}{
		{
			name: "root send task",
			mutate: func(root, _ *WorkspaceTransferGrantV1) {
				root.SendPayloadKinds = []WorkspaceTransferPayloadKindV1{
					WorkspaceTransferPayloadSpecialistResultV1,
				}
			},
		},
		{
			name: "target receive task",
			mutate: func(_, target *WorkspaceTransferGrantV1) {
				target.ReceivePayloadKinds = []WorkspaceTransferPayloadKindV1{
					WorkspaceTransferPayloadSpecialistResultV1,
				}
			},
		},
		{
			name: "target send result",
			mutate: func(_, target *WorkspaceTransferGrantV1) {
				target.SendPayloadKinds = []WorkspaceTransferPayloadKindV1{
					WorkspaceTransferPayloadTaskSummaryV1,
				}
			},
		},
		{
			name: "root receive result",
			mutate: func(root, _ *WorkspaceTransferGrantV1) {
				root.ReceivePayloadKinds = []WorkspaceTransferPayloadKindV1{
					WorkspaceTransferPayloadTaskSummaryV1,
				}
			},
		},
		{
			name: "disabled root",
			mutate: func(root, _ *WorkspaceTransferGrantV1) {
				root.Enabled = false
			},
		},
		{
			name: "wrong peer",
			mutate: func(_, target *WorkspaceTransferGrantV1) {
				target.PeerWorkspace = workspaceTransferTestWorkspace(
					"workspace-stale",
					"v1",
					"3",
				)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidateRoot := root
			candidateRoot.SendPayloadKinds = append(
				[]WorkspaceTransferPayloadKindV1(nil),
				root.SendPayloadKinds...,
			)
			candidateRoot.ReceivePayloadKinds = append(
				[]WorkspaceTransferPayloadKindV1(nil),
				root.ReceivePayloadKinds...,
			)
			candidateTarget := target
			candidateTarget.SendPayloadKinds = append(
				[]WorkspaceTransferPayloadKindV1(nil),
				target.SendPayloadKinds...,
			)
			candidateTarget.ReceivePayloadKinds = append(
				[]WorkspaceTransferPayloadKindV1(nil),
				target.ReceivePayloadKinds...,
			)
			test.mutate(&candidateRoot, &candidateTarget)
			if _, err := NewWorkspaceTransferPlanV1(
				candidateRoot,
				candidateTarget,
			); err == nil {
				t.Fatal("incomplete Workspace transfer plan was accepted")
			}
		})
	}

	tampered := plan
	tampered.TargetGrantDigest = workspaceTransferDifferentDigest(
		tampered.TargetGrantDigest,
	)
	if err := tampered.ValidateAgainstGrantsV1(root, target); err == nil {
		t.Fatal("tampered Workspace transfer plan matched the grants")
	}
}

func TestWorkspaceTransferGrantV1AllowsExplicitlyEmptyDisabledGrant(t *testing.T) {
	input := workspaceTransferTestGrant(
		"grant-disabled",
		workspaceTransferTestWorkspace("workspace-root", "v1", "1"),
		workspaceTransferTestWorkspace("workspace-specialist", "v1", "2"),
	)
	input.Enabled = false
	input.SendPayloadKinds = nil
	input.ReceivePayloadKinds = nil
	input.MaxSendPayloadBytes = 0
	input.MaxReceivePayloadBytes = 0
	frozen, canonical, digest, err := NewWorkspaceTransferGrantV1(input)
	if err != nil {
		t.Fatalf("disabled empty grant: %v", err)
	}
	if frozen.SendPayloadKinds == nil || frozen.ReceivePayloadKinds == nil ||
		!bytes.Contains(canonical, []byte(`"send_payload_kinds":[]`)) ||
		!bytes.Contains(canonical, []byte(`"receive_payload_kinds":[]`)) {
		t.Fatalf("disabled grant did not freeze explicit arrays: %s", canonical)
	}
	if _, err := RestoreWorkspaceTransferGrantV1(canonical, digest); err != nil {
		t.Fatalf("restore disabled empty grant: %v", err)
	}

	nullWire := bytes.Replace(
		canonical,
		[]byte(`"send_payload_kinds":[]`),
		[]byte(`"send_payload_kinds":null`),
		1,
	)
	if _, err := RestoreWorkspaceTransferGrantV1(
		nullWire,
		moduleapi.Digest(workspaceTransferGrantDigestDomainV1, nullWire),
	); err == nil {
		t.Fatal("grant accepted null instead of its required empty array")
	}
}

func TestWorkspaceTaskSummaryV1StrictCanonicalAndBounds(t *testing.T) {
	input := WorkspaceTaskSummaryV1{
		SchemaVersion:      WorkspaceTaskSummarySchemaVersionV1,
		SourceTaskInputRef: strings.Repeat("c", moduleapi.SHA256HexLength),
		Summary:            "Use the bounded backend interface only.",
	}
	frozen, canonical, err := NewWorkspaceTaskSummaryV1(input)
	if err != nil {
		t.Fatalf("NewWorkspaceTaskSummaryV1: %v", err)
	}
	const wantCanonical = `{"previous_set_digest":"","repair_round":0,"schema_version":"workspace-task-summary/v1","source_task_input_ref":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","summary":"Use the bounded backend interface only.","verdict_ref":""}`
	if string(canonical) != wantCanonical {
		t.Fatalf("canonical=%s want %s", canonical, wantCanonical)
	}
	restored, err := RestoreWorkspaceTaskSummaryV1(canonical)
	if err != nil {
		t.Fatalf("RestoreWorkspaceTaskSummaryV1: %v", err)
	}
	if restored != frozen {
		t.Fatalf("restored=%+v want %+v", restored, frozen)
	}
	if _, err := RestoreWorkspaceTaskSummaryV1(
		append(bytes.Clone(canonical), ' '),
	); err == nil {
		t.Fatal("Workspace task summary accepted non-canonical bytes")
	}
	unknown := workspaceTransferCanonicalWithUnknown(t, canonical)
	if _, err := RestoreWorkspaceTaskSummaryV1(unknown); err == nil {
		t.Fatal("Workspace task summary accepted an unknown field")
	}
	missingRepairBasis := bytes.Replace(
		canonical,
		[]byte(`"previous_set_digest":"",`),
		nil,
		1,
	)
	if _, err := RestoreWorkspaceTaskSummaryV1(missingRepairBasis); err == nil {
		t.Fatal("Workspace task summary accepted an omitted repair-basis field")
	}

	for _, invalid := range []WorkspaceTaskSummaryV1{
		{SchemaVersion: "workspace-task-summary/v2", SourceTaskInputRef: input.SourceTaskInputRef, Summary: input.Summary},
		{SchemaVersion: input.SchemaVersion, SourceTaskInputRef: "bad", Summary: input.Summary},
		{SchemaVersion: input.SchemaVersion, SourceTaskInputRef: input.SourceTaskInputRef, Summary: ""},
		{SchemaVersion: input.SchemaVersion, SourceTaskInputRef: input.SourceTaskInputRef, Summary: " leading"},
		{SchemaVersion: input.SchemaVersion, SourceTaskInputRef: input.SourceTaskInputRef, Summary: strings.Repeat("x", int(WorkspaceTaskSummaryMaximumPayloadBytesV1))},
		{SchemaVersion: input.SchemaVersion, SourceTaskInputRef: input.SourceTaskInputRef, RepairRound: 0, PreviousSetDigest: strings.Repeat("a", moduleapi.SHA256HexLength), Summary: input.Summary},
		{SchemaVersion: input.SchemaVersion, SourceTaskInputRef: input.SourceTaskInputRef, RepairRound: 0, VerdictRef: strings.Repeat("b", moduleapi.SHA256HexLength), Summary: input.Summary},
		{SchemaVersion: input.SchemaVersion, SourceTaskInputRef: input.SourceTaskInputRef, RepairRound: 1, VerdictRef: strings.Repeat("b", moduleapi.SHA256HexLength), Summary: input.Summary},
		{SchemaVersion: input.SchemaVersion, SourceTaskInputRef: input.SourceTaskInputRef, RepairRound: 1, PreviousSetDigest: strings.Repeat("a", moduleapi.SHA256HexLength), Summary: input.Summary},
		{SchemaVersion: input.SchemaVersion, SourceTaskInputRef: input.SourceTaskInputRef, RepairRound: 1, PreviousSetDigest: "bad", VerdictRef: strings.Repeat("b", moduleapi.SHA256HexLength), Summary: input.Summary},
		{SchemaVersion: input.SchemaVersion, SourceTaskInputRef: input.SourceTaskInputRef, RepairRound: 2, PreviousSetDigest: strings.Repeat("a", moduleapi.SHA256HexLength), VerdictRef: strings.Repeat("b", moduleapi.SHA256HexLength), Summary: input.Summary},
	} {
		if _, _, err := NewWorkspaceTaskSummaryV1(invalid); err == nil {
			t.Fatalf("invalid Workspace task summary was accepted: %+v", invalid)
		}
	}

	roundOne := input
	roundOne.RepairRound = 1
	roundOne.PreviousSetDigest = strings.Repeat("a", moduleapi.SHA256HexLength)
	roundOne.VerdictRef = strings.Repeat("b", moduleapi.SHA256HexLength)
	frozenRoundOne, roundOneCanonical, err := NewWorkspaceTaskSummaryV1(roundOne)
	if err != nil {
		t.Fatalf("round-1 Workspace task summary: %v", err)
	}
	restoredRoundOne, err := RestoreWorkspaceTaskSummaryV1(roundOneCanonical)
	if err != nil || restoredRoundOne != frozenRoundOne {
		t.Fatalf("restore round-1 Workspace task summary: %+v, %v", restoredRoundOne, err)
	}
}

func TestWorkspaceTransferContentDigestV1MatchesFrozenContentRecordAlgorithm(t *testing.T) {
	canonical := []byte(`{"previous_set_digest":"","repair_round":0,"schema_version":"workspace-task-summary/v1","source_task_input_ref":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","summary":"Bounded backend task summary.","verdict_ref":""}`)
	const want = "4593afcc974a6a75039affd93b74ae81a901c6b36d710fe25d11f0fc16837247"
	if got := workspaceTransferContentDigestV1(
		workspaceTransferPayloadRecordKindV1,
		workspaceTransferJSONMediaTypeV1,
		canonical,
	); got != want {
		t.Fatalf("content digest=%s want %s", got, want)
	}
}

func TestWorkspaceTransferEnvelopeV1CanonicalRoundTripAndIndependentGrantIDs(t *testing.T) {
	request := workspaceTransferTestRequest(t)
	envelope, canonical, digest, err := NewWorkspaceTransferEnvelopeV1(
		request.envelope,
	)
	if err != nil {
		t.Fatalf("NewWorkspaceTransferEnvelopeV1: %v", err)
	}
	if envelope.SourceGrantID == envelope.TargetGrantID {
		t.Fatal("test did not exercise independent bilateral grant IDs")
	}
	if digest != moduleapi.Digest(workspaceTransferEnvelopeDigestDomainV1, canonical) {
		t.Fatalf("envelope digest does not bind canonical bytes: %s", digest)
	}
	if !bytes.Contains(canonical, []byte(`"payload_schema_version":"workspace-task-summary/v1"`)) ||
		!bytes.Contains(canonical, []byte(`"source_grant_id":"grant-root"`)) ||
		!bytes.Contains(canonical, []byte(`"target_grant_id":"grant-specialist"`)) {
		t.Fatalf("envelope omitted independent grants or payload schema: %s", canonical)
	}
	restored, err := RestoreWorkspaceTransferEnvelopeV1(canonical, digest)
	if err != nil {
		t.Fatalf("RestoreWorkspaceTransferEnvelopeV1: %v", err)
	}
	if restored != envelope {
		t.Fatalf("restored=%+v want %+v", restored, envelope)
	}
	if _, err := RestoreWorkspaceTransferEnvelopeV1(
		canonical,
		workspaceTransferDifferentDigest(digest),
	); err == nil {
		t.Fatal("envelope accepted a different digest")
	}
	if _, err := RestoreWorkspaceTransferEnvelopeV1(
		append(bytes.Clone(canonical), '\n'),
		digest,
	); err == nil {
		t.Fatal("envelope accepted non-canonical bytes")
	}
	unknown := workspaceTransferCanonicalWithUnknown(t, canonical)
	if _, err := RestoreWorkspaceTransferEnvelopeV1(
		unknown,
		moduleapi.Digest(workspaceTransferEnvelopeDigestDomainV1, unknown),
	); err == nil {
		t.Fatal("envelope accepted an unknown field")
	}
}

func TestWorkspaceTransferEnvelopeV1RejectsIdentityDirectionSchemaAndBounds(t *testing.T) {
	valid := func() WorkspaceTransferEnvelopeV1 {
		return workspaceTransferTestRequest(t).envelope
	}
	for _, test := range []struct {
		name   string
		mutate func(*WorkspaceTransferEnvelopeV1)
	}{
		{"schema", func(value *WorkspaceTransferEnvelopeV1) { value.SchemaVersion = "workspace-transfer-envelope/v2" }},
		{"tenant", func(value *WorkspaceTransferEnvelopeV1) { value.TenantID = "" }},
		{"source grant ID", func(value *WorkspaceTransferEnvelopeV1) { value.SourceGrantID = "" }},
		{"target grant ID", func(value *WorkspaceTransferEnvelopeV1) { value.TargetGrantID = "" }},
		{"same grant ID", func(value *WorkspaceTransferEnvelopeV1) { value.TargetGrantID = value.SourceGrantID }},
		{"root Run", func(value *WorkspaceTransferEnvelopeV1) { value.RootRunID = "" }},
		{"root Run overflow", func(value *WorkspaceTransferEnvelopeV1) { value.RootRunID = strings.Repeat("r", maxOpaqueIDBytes+1) }},
		{"child Run", func(value *WorkspaceTransferEnvelopeV1) { value.ChildRunID = value.RootRunID }},
		{"slot", func(value *WorkspaceTransferEnvelopeV1) { value.SlotID = "" }},
		{"slot overflow", func(value *WorkspaceTransferEnvelopeV1) { value.SlotID = strings.Repeat("s", maxOpaqueIDBytes+1) }},
		{"Reviewer slot", func(value *WorkspaceTransferEnvelopeV1) { value.SlotID = CompositeReviewerParentSlotIDV1 }},
		{"source grant digest", func(value *WorkspaceTransferEnvelopeV1) { value.SourceGrantDigest = "bad" }},
		{"target grant digest", func(value *WorkspaceTransferEnvelopeV1) { value.TargetGrantDigest = "bad" }},
		{"same grant digest", func(value *WorkspaceTransferEnvelopeV1) { value.TargetGrantDigest = value.SourceGrantDigest }},
		{"payload ref", func(value *WorkspaceTransferEnvelopeV1) { value.PayloadRef = "bad" }},
		{"zero payload", func(value *WorkspaceTransferEnvelopeV1) { value.PayloadSizeBytes = 0 }},
		{"payload overflow", func(value *WorkspaceTransferEnvelopeV1) {
			value.PayloadSizeBytes = WorkspaceTransferMaximumPayloadBytesV1 + 1
		}},
		{"source Workspace", func(value *WorkspaceTransferEnvelopeV1) { value.SourceWorkspace.Digest = "bad" }},
		{"target Workspace", func(value *WorkspaceTransferEnvelopeV1) { value.TargetWorkspace.Version = "" }},
		{"same Workspace ID", func(value *WorkspaceTransferEnvelopeV1) { value.TargetWorkspace.ID = value.SourceWorkspace.ID }},
		{"unknown direction", func(value *WorkspaceTransferEnvelopeV1) { value.Direction = "SIDEWAYS" }},
		{"request result payload", func(value *WorkspaceTransferEnvelopeV1) {
			value.PayloadKind = WorkspaceTransferPayloadSpecialistResultV1
			value.PayloadSchemaVersion = SpecialistContributionSchemaVersionV1
		}},
		{"result request payload", func(value *WorkspaceTransferEnvelopeV1) {
			value.Direction = WorkspaceTransferDirectionResultV1
		}},
		{"payload schema mismatch", func(value *WorkspaceTransferEnvelopeV1) {
			value.PayloadSchemaVersion = SpecialistContributionSchemaVersionV1
		}},
		{"task input ref", func(value *WorkspaceTransferEnvelopeV1) { value.TaskInputRef = "bad" }},
		{"unknown payload kind", func(value *WorkspaceTransferEnvelopeV1) { value.PayloadKind = "MEMORY" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := valid()
			test.mutate(&input)
			if _, _, _, err := NewWorkspaceTransferEnvelopeV1(input); err == nil {
				t.Fatal("invalid Workspace transfer envelope was accepted")
			}
		})
	}
}

func TestWorkspaceTransferEnvelopeV1FreezesPerKindPayloadCeilings(t *testing.T) {
	request := workspaceTransferTestRequest(t).envelope
	request.PayloadSizeBytes = WorkspaceTaskSummaryMaximumPayloadBytesV1
	if _, _, _, err := NewWorkspaceTransferEnvelopeV1(request); err != nil {
		t.Fatalf("TASK_SUMMARY maximum: %v", err)
	}
	request.PayloadSizeBytes = WorkspaceTaskSummaryMaximumPayloadBytesV1 + 1
	if _, _, _, err := NewWorkspaceTransferEnvelopeV1(request); err == nil {
		t.Fatal("TASK_SUMMARY accepted more than 32 KiB")
	}

	result := workspaceTransferTestResult(t).envelope
	result.PayloadSizeBytes = WorkspaceTransferMaximumPayloadBytesV1
	if _, _, _, err := NewWorkspaceTransferEnvelopeV1(result); err != nil {
		t.Fatalf("SPECIALIST_RESULT maximum: %v", err)
	}
	result.PayloadSizeBytes = WorkspaceTransferMaximumPayloadBytesV1 + 1
	if _, _, _, err := NewWorkspaceTransferEnvelopeV1(result); err == nil {
		t.Fatal("SPECIALIST_RESULT accepted more than 48 KiB")
	}
}

func TestWorkspaceTransferEnvelopeV1ValidatesBothDirectionsAndExactGrantIntersection(t *testing.T) {
	request := workspaceTransferTestRequest(t)
	if err := request.envelope.ValidateAgainstGrantsV1(
		request.source,
		request.target,
	); err != nil {
		t.Fatalf("request authorization: %v", err)
	}

	result := workspaceTransferTestResult(t)
	if err := result.envelope.ValidateAgainstGrantsV1(
		result.source,
		result.target,
	); err != nil {
		t.Fatalf("result authorization: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*workspaceTransferAuthorizationFixture)
	}{
		{"source disabled", func(value *workspaceTransferAuthorizationFixture) { value.source.Enabled = false }},
		{"target disabled", func(value *workspaceTransferAuthorizationFixture) { value.target.Enabled = false }},
		{"source grant ID", func(value *workspaceTransferAuthorizationFixture) { value.envelope.SourceGrantID += "-stale" }},
		{"target grant ID", func(value *workspaceTransferAuthorizationFixture) { value.envelope.TargetGrantID += "-stale" }},
		{"source grant digest", func(value *workspaceTransferAuthorizationFixture) {
			value.envelope.SourceGrantDigest = workspaceTransferDifferentDigest(value.envelope.SourceGrantDigest)
		}},
		{"target grant digest", func(value *workspaceTransferAuthorizationFixture) {
			value.envelope.TargetGrantDigest = workspaceTransferDifferentDigest(value.envelope.TargetGrantDigest)
		}},
		{"source exact Workspace version", func(value *workspaceTransferAuthorizationFixture) { value.source.Workspace.Version = "v-stale" }},
		{"source exact peer version", func(value *workspaceTransferAuthorizationFixture) { value.source.PeerWorkspace.Version = "v-stale" }},
		{"target exact Workspace version", func(value *workspaceTransferAuthorizationFixture) { value.target.Workspace.Version = "v-stale" }},
		{"target exact peer version", func(value *workspaceTransferAuthorizationFixture) { value.target.PeerWorkspace.Version = "v-stale" }},
		{"source send intersection", func(value *workspaceTransferAuthorizationFixture) {
			value.source.SendPayloadKinds = []WorkspaceTransferPayloadKindV1{WorkspaceTransferPayloadSpecialistResultV1}
		}},
		{"target receive intersection", func(value *workspaceTransferAuthorizationFixture) {
			value.target.ReceivePayloadKinds = []WorkspaceTransferPayloadKindV1{WorkspaceTransferPayloadSpecialistResultV1}
		}},
		{"source byte ceiling", func(value *workspaceTransferAuthorizationFixture) {
			value.source.MaxSendPayloadBytes = value.envelope.PayloadSizeBytes - 1
		}},
		{"target byte ceiling", func(value *workspaceTransferAuthorizationFixture) {
			value.target.MaxReceivePayloadBytes = value.envelope.PayloadSizeBytes - 1
		}},
		{"stale source revision", func(value *workspaceTransferAuthorizationFixture) { value.source.Revision++ }},
		{"stale target revision", func(value *workspaceTransferAuthorizationFixture) { value.target.Revision++ }},
		{"hand-built illegal direction payload", func(value *workspaceTransferAuthorizationFixture) {
			value.envelope.Direction = WorkspaceTransferDirectionResultV1
		}},
		{"hand-built oversized envelope", func(value *workspaceTransferAuthorizationFixture) {
			value.envelope.PayloadSizeBytes = WorkspaceTransferMaximumPayloadBytesV1 + 1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := workspaceTransferTestRequest(t)
			test.mutate(&fixture)
			if err := fixture.envelope.ValidateAgainstGrantsV1(
				fixture.source,
				fixture.target,
			); err == nil {
				t.Fatal("Workspace transfer authorization mismatch was accepted")
			}
		})
	}
}

func TestWorkspaceTransferEnvelopeV1RejectsExactWorkspaceOrTenantMismatchWithCurrentGrantDigest(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*workspaceTransferAuthorizationFixture)
		source bool
	}{
		{"source owner", func(value *workspaceTransferAuthorizationFixture) { value.source.Workspace.Version = "v-stale" }, true},
		{"source peer", func(value *workspaceTransferAuthorizationFixture) { value.source.PeerWorkspace.Version = "v-stale" }, true},
		{"target owner", func(value *workspaceTransferAuthorizationFixture) { value.target.Workspace.Version = "v-stale" }, false},
		{"target peer", func(value *workspaceTransferAuthorizationFixture) { value.target.PeerWorkspace.Version = "v-stale" }, false},
		{"source tenant", func(value *workspaceTransferAuthorizationFixture) { value.source.TenantID = "tenant-stale" }, true},
		{"target tenant", func(value *workspaceTransferAuthorizationFixture) { value.target.TenantID = "tenant-stale" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := workspaceTransferTestRequest(t)
			test.mutate(&fixture)
			if test.source {
				fixture.envelope.SourceGrantDigest = workspaceTransferTestGrantDigest(
					t,
					fixture.source,
				)
			} else {
				fixture.envelope.TargetGrantDigest = workspaceTransferTestGrantDigest(
					t,
					fixture.target,
				)
			}
			if err := fixture.envelope.ValidateAgainstGrantsV1(
				fixture.source,
				fixture.target,
			); err == nil {
				t.Fatal("exact Workspace or tenant mismatch was accepted")
			}
		})
	}
}

func TestWorkspaceTransferEnvelopeV1ClosesActualTaskAndSpecialistPayloads(t *testing.T) {
	request := workspaceTransferTestRequest(t)
	if err := request.envelope.ValidateResolvedPayloadV1(request.resolved); err != nil {
		t.Fatalf("task summary closure: %v", err)
	}
	result := workspaceTransferTestResult(t)
	if err := result.envelope.ValidateResolvedPayloadV1(result.resolved); err != nil {
		t.Fatalf("Specialist result closure: %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*workspaceTransferAuthorizationFixture)
	}{
		{"resolved ref", func(value *workspaceTransferAuthorizationFixture) {
			value.resolved.PayloadRef = workspaceTransferDifferentDigest(value.resolved.PayloadRef)
		}},
		{"content kind", func(value *workspaceTransferAuthorizationFixture) {
			value.resolved.ContentKind = workspaceTransferModelResultKindV1
		}},
		{"media type", func(value *workspaceTransferAuthorizationFixture) { value.resolved.MediaType = "text/plain" }},
		{"empty bytes", func(value *workspaceTransferAuthorizationFixture) { value.resolved.CanonicalBytes = nil }},
		{"actual bytes", func(value *workspaceTransferAuthorizationFixture) {
			value.resolved.CanonicalBytes = bytes.Clone(value.resolved.CanonicalBytes)
			value.resolved.CanonicalBytes[len(value.resolved.CanonicalBytes)-2] ^= 1
		}},
		{"declared size", func(value *workspaceTransferAuthorizationFixture) { value.envelope.PayloadSizeBytes++ }},
		{"hand-built schema mismatch", func(value *workspaceTransferAuthorizationFixture) {
			value.envelope.PayloadSchemaVersion = SpecialistContributionSchemaVersionV1
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := workspaceTransferTestRequest(t)
			test.mutate(&fixture)
			if err := fixture.envelope.ValidateResolvedPayloadV1(
				fixture.resolved,
			); err == nil {
				t.Fatal("resolved payload mismatch was accepted")
			}
		})
	}
}

func TestWorkspaceTransferEnvelopeV1RejectsActualPayloadOverCeilingDespiteSafeDeclaration(
	t *testing.T,
) {
	fixture := workspaceTransferTestResult(t)
	actual := bytes.Repeat(
		[]byte{'x'},
		int(WorkspaceTransferMaximumPayloadBytesV1)+1,
	)
	ref := workspaceTransferContentDigestV1(
		workspaceTransferModelResultKindV1,
		workspaceTransferJSONMediaTypeV1,
		actual,
	)
	fixture.envelope.PayloadRef = ref
	fixture.envelope.PayloadSizeBytes = WorkspaceTransferMaximumPayloadBytesV1
	fixture.resolved.PayloadRef = ref
	fixture.resolved.CanonicalBytes = actual
	if err := fixture.envelope.ValidateResolvedPayloadV1(
		fixture.resolved,
	); err == nil {
		t.Fatal("actual payload over 48 KiB bypassed the declared-size ceiling")
	}
}

func TestWorkspaceTransferEnvelopeV1RejectsSelfConsistentWrongOrUnknownPayloadSchema(t *testing.T) {
	request := workspaceTransferTestRequest(t)

	// The envelope, digest and byte count can all be internally consistent and
	// still be semantically wrong for TASK_SUMMARY. Exact typed restore closes
	// the actual bytes rather than trusting self-declared metadata.
	_, specialistCanonical, _, err := NewSpecialistContributionV1(
		workspaceTransferTestContribution(),
	)
	if err != nil {
		t.Fatal(err)
	}
	wrongSchema := workspaceTransferFixtureWithPayload(
		t,
		request,
		workspaceTransferPayloadRecordKindV1,
		specialistCanonical,
	)
	if err := wrongSchema.envelope.ValidateResolvedPayloadV1(
		wrongSchema.resolved,
	); err == nil {
		t.Fatal("TASK_SUMMARY accepted specialist-contribution/v1 bytes")
	}
	result := workspaceTransferTestResult(t)
	wrongResultSchema := workspaceTransferFixtureWithPayload(
		t,
		result,
		workspaceTransferModelResultKindV1,
		request.resolved.CanonicalBytes,
	)
	if err := wrongResultSchema.envelope.ValidateResolvedPayloadV1(
		wrongResultSchema.resolved,
	); err == nil {
		t.Fatal("SPECIALIST_RESULT accepted workspace-task-summary/v1 bytes")
	}
	_, staleTaskCanonical, err := NewWorkspaceTaskSummaryV1(
		WorkspaceTaskSummaryV1{
			SchemaVersion:      WorkspaceTaskSummarySchemaVersionV1,
			SourceTaskInputRef: strings.Repeat("e", moduleapi.SHA256HexLength),
			Summary:            "A valid summary for a different task.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	staleTask := workspaceTransferFixtureWithPayload(
		t,
		request,
		workspaceTransferPayloadRecordKindV1,
		staleTaskCanonical,
	)
	if err := staleTask.envelope.ValidateResolvedPayloadV1(
		staleTask.resolved,
	); err == nil {
		t.Fatal("TASK_SUMMARY accepted a different source_task_input_ref")
	}

	unknown := workspaceTransferCanonicalWithUnknown(
		t,
		request.resolved.CanonicalBytes,
	)
	unknownFixture := workspaceTransferFixtureWithPayload(
		t,
		request,
		workspaceTransferPayloadRecordKindV1,
		unknown,
	)
	if err := unknownFixture.envelope.ValidateResolvedPayloadV1(
		unknownFixture.resolved,
	); err == nil {
		t.Fatal("TASK_SUMMARY accepted an unknown payload field")
	}

	nonCanonical := append(bytes.Clone(request.resolved.CanonicalBytes), ' ')
	nonCanonicalFixture := workspaceTransferFixtureWithPayload(
		t,
		request,
		workspaceTransferPayloadRecordKindV1,
		nonCanonical,
	)
	if err := nonCanonicalFixture.envelope.ValidateResolvedPayloadV1(
		nonCanonicalFixture.resolved,
	); err == nil {
		t.Fatal("TASK_SUMMARY accepted non-canonical payload bytes")
	}
}

func TestWorkspaceTransferEnvelopeV1SpecialistResultRequiresExactModelOutputProjection(t *testing.T) {
	result := workspaceTransferTestResult(t)
	if err := result.envelope.ValidateResolvedPayloadV1(result.resolved); err != nil {
		t.Fatalf("valid MODEL_RESULT projection: %v", err)
	}
	_, contributionCanonical, _, err := NewSpecialistContributionV1(
		workspaceTransferTestContribution(),
	)
	if err != nil {
		t.Fatal(err)
	}

	bareContribution := workspaceTransferFixtureWithPayload(
		t,
		result,
		workspaceTransferModelResultKindV1,
		contributionCanonical,
	)
	if err := bareContribution.envelope.ValidateResolvedPayloadV1(
		bareContribution.resolved,
	); err == nil {
		t.Fatal("bare specialist-contribution/v1 bytes were accepted as MODEL_RESULT")
	}

	for _, test := range []struct {
		name          string
		assistantText string
	}{
		{"ordinary text", "This is not a Specialist contribution."},
		{"non-canonical contribution", string(contributionCanonical) + " "},
		{"wrong contribution schema", strings.Replace(
			string(contributionCanonical),
			SpecialistContributionSchemaVersionV1,
			"specialist-contribution/v2",
			1,
		)},
		{"unknown contribution field", string(workspaceTransferCanonicalWithUnknown(
			t,
			contributionCanonical,
		))},
	} {
		t.Run(test.name, func(t *testing.T) {
			outer := workspaceTransferTestModelResult(t, test.assistantText)
			fixture := workspaceTransferFixtureWithPayload(
				t,
				workspaceTransferTestResult(t),
				workspaceTransferModelResultKindV1,
				outer,
			)
			if err := fixture.envelope.ValidateResolvedPayloadV1(
				fixture.resolved,
			); err == nil {
				t.Fatal("invalid AssistantText contribution was accepted")
			}
		})
	}

	_, actionOuter, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			ActionRequest: &moduleapi.ModelActionRequestV1{
				ActionID:       "workspace.inspect",
				CanonicalInput: []byte(`{}`),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	actionFixture := workspaceTransferFixtureWithPayload(
		t,
		result,
		workspaceTransferModelResultKindV1,
		actionOuter,
	)
	if err := actionFixture.envelope.ValidateResolvedPayloadV1(
		actionFixture.resolved,
	); err == nil {
		t.Fatal("MODEL_RESULT ActionRequest was accepted as a Specialist result")
	}

	unknownOuter := workspaceTransferCanonicalWithUnknown(
		t,
		result.resolved.CanonicalBytes,
	)
	unknownOuterFixture := workspaceTransferFixtureWithPayload(
		t,
		result,
		workspaceTransferModelResultKindV1,
		unknownOuter,
	)
	if err := unknownOuterFixture.envelope.ValidateResolvedPayloadV1(
		unknownOuterFixture.resolved,
	); err == nil {
		t.Fatal("MODEL_RESULT with an unknown outer field was accepted")
	}

	innerRef := workspaceTransferContentDigestV1(
		workspaceTransferModelResultKindV1,
		workspaceTransferJSONMediaTypeV1,
		contributionCanonical,
	)
	innerIdentity := result
	innerIdentity.envelope.PayloadRef = innerRef
	innerIdentity.envelope.PayloadSizeBytes = uint32(len(contributionCanonical))
	innerIdentity.resolved.PayloadRef = innerRef
	if err := innerIdentity.envelope.ValidateResolvedPayloadV1(
		innerIdentity.resolved,
	); err == nil {
		t.Fatal("SPECIALIST_RESULT accepted ref/size computed from inner contribution bytes")
	}
}

func TestWorkspaceTransferEnvelopeV1BindsExactCompositeFamilyInBothDirections(t *testing.T) {
	root, child, request, result := workspaceTransferTestCompositeFamily(t)
	if err := request.envelope.ValidateForCompositeFamilyV1(root, child); err != nil {
		t.Fatalf("request family closure: %v", err)
	}
	if err := result.envelope.ValidateForCompositeFamilyV1(root, child); err != nil {
		t.Fatalf("result family closure: %v", err)
	}

	for _, test := range []struct {
		name                 string
		mutate               func(*RunManifest, *RunManifest, *WorkspaceTransferEnvelopeV1)
		rebindParentManifest bool
	}{
		{"tenant", func(_ *RunManifest, _ *RunManifest, envelope *WorkspaceTransferEnvelopeV1) {
			envelope.TenantID = "tenant-stale"
		}, false},
		{"root Run", func(_ *RunManifest, _ *RunManifest, envelope *WorkspaceTransferEnvelopeV1) {
			envelope.RootRunID = "run-stale"
		}, false},
		{"Child Run", func(_ *RunManifest, _ *RunManifest, envelope *WorkspaceTransferEnvelopeV1) {
			envelope.ChildRunID = "run-stale"
		}, false},
		{"slot", func(_ *RunManifest, _ *RunManifest, envelope *WorkspaceTransferEnvelopeV1) {
			envelope.SlotID = "slot.stale"
		}, false},
		{"TaskInputRef", func(_ *RunManifest, _ *RunManifest, envelope *WorkspaceTransferEnvelopeV1) {
			envelope.TaskInputRef = strings.Repeat("e", moduleapi.SHA256HexLength)
		}, false},
		{"source Workspace version", func(_ *RunManifest, _ *RunManifest, envelope *WorkspaceTransferEnvelopeV1) {
			envelope.SourceWorkspace.Version = "v-stale"
		}, false},
		{"target Workspace version", func(_ *RunManifest, _ *RunManifest, envelope *WorkspaceTransferEnvelopeV1) {
			envelope.TargetWorkspace.Version = "v-stale"
		}, false},
		{"reversed request Workspaces", func(_ *RunManifest, _ *RunManifest, envelope *WorkspaceTransferEnvelopeV1) {
			envelope.SourceWorkspace, envelope.TargetWorkspace = envelope.TargetWorkspace, envelope.SourceWorkspace
		}, false},
		{"Child tenant", func(_ *RunManifest, child *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			child.TenantID = "tenant-stale"
		}, false},
		{"Child AdmissionKey", func(_ *RunManifest, child *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			child.AdmissionKey = "admission-stale"
		}, false},
		{"Child parent digest", func(_ *RunManifest, child *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			child.Composite.ParentManifestDigest = strings.Repeat("e", moduleapi.SHA256HexLength)
		}, false},
		{"Child planned member", func(_ *RunManifest, child *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			child.Members[0].Digest = strings.Repeat("e", moduleapi.SHA256HexLength)
		}, false},
		{"root Grant ID", func(root *RunManifest, _ *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			workspaceTransferMutateBothPlans(t, root, func(plan *WorkspaceTransferPlanV1) {
				plan.RootGrantID = "grant-family-root-stale"
			})
		}, true},
		{"target Grant ID", func(root *RunManifest, _ *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			workspaceTransferMutateBothPlans(t, root, func(plan *WorkspaceTransferPlanV1) {
				plan.TargetGrantID = "grant-family-child-stale"
			})
		}, true},
		{"root Grant digest", func(root *RunManifest, _ *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			workspaceTransferMutateBothPlans(t, root, func(plan *WorkspaceTransferPlanV1) {
				plan.RootGrantDigest = strings.Repeat("e", moduleapi.SHA256HexLength)
			})
		}, true},
		{"target Grant digest", func(root *RunManifest, _ *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			workspaceTransferMutateBothPlans(t, root, func(plan *WorkspaceTransferPlanV1) {
				plan.TargetGrantDigest = strings.Repeat("f", moduleapi.SHA256HexLength)
			})
		}, true},
		{"root Workspace ref", func(root *RunManifest, _ *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			root.Workspace.Version = "v-root-stale"
			workspaceTransferMutateBothPlans(t, root, func(plan *WorkspaceTransferPlanV1) {
				plan.RootWorkspace = root.Workspace
			})
		}, true},
		{"target Workspace ref", func(root *RunManifest, _ *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			workspaceTransferMutateBothPlans(t, root, func(plan *WorkspaceTransferPlanV1) {
				plan.TargetWorkspace.Version = "v-target-stale"
			})
		}, true},
		{"missing transfer plan", func(root *RunManifest, _ *RunManifest, _ *WorkspaceTransferEnvelopeV1) {
			root.Composite.Plan.Children[0].Transfer = nil
			root.Composite.Plan.Decision.RepairChildren[0].Transfer = nil
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidateRoot, candidateChild, candidateRequest, candidateResult :=
				workspaceTransferTestCompositeFamily(t)
			envelope := candidateRequest.envelope
			test.mutate(&candidateRoot, &candidateChild, &envelope)
			candidateRoot = workspaceTransferRefreezeManifest(t, candidateRoot)
			if test.rebindParentManifest {
				candidateChild.Composite.ParentManifestDigest = candidateRoot.ManifestDigest
			}
			candidateChild = workspaceTransferRefreezeManifest(t, candidateChild)
			if err := envelope.ValidateForCompositeFamilyV1(
				candidateRoot,
				candidateChild,
			); err == nil {
				t.Fatal("composite family mismatch was accepted")
			}
			if test.rebindParentManifest {
				if err := candidateResult.envelope.ValidateForCompositeFamilyV1(
					candidateRoot,
					candidateChild,
				); err == nil {
					t.Fatal("result envelope accepted a tampered transfer plan")
				}
			}
		})
	}
}

func TestWorkspaceTransferEnvelopeV1BindsRepairChildByLogicalSlotAndPhysicalParent(t *testing.T) {
	root, child, request, result := workspaceTransferTestCompositeRepairFamily(t)
	planned := root.Composite.Plan.Decision.RepairChildren[0]
	if child.Composite.RepairRound != CompositeRepairRoundOneV1 ||
		child.Composite.Assignment == nil ||
		child.Composite.Assignment.SlotID != planned.SlotID ||
		request.envelope.SlotID != planned.SlotID ||
		result.envelope.SlotID != planned.SlotID {
		t.Fatalf(
			"repair envelope did not preserve the logical Assignment slot: planned=%+v child=%+v request=%+v result=%+v",
			planned,
			child.Composite,
			request.envelope,
			result.envelope,
		)
	}
	if child.Composite.ParentSlotID != planned.ParentSlotID ||
		child.Composite.ParentSlotID == planned.SlotID {
		t.Fatalf(
			"repair Child did not close its distinct physical parent slot: planned=%+v child=%+v",
			planned,
			child.Composite,
		)
	}
	if err := request.envelope.ValidateForCompositeFamilyV1(root, child); err != nil {
		t.Fatalf("repair request family closure: %v", err)
	}
	if err := result.envelope.ValidateForCompositeFamilyV1(root, child); err != nil {
		t.Fatalf("repair result family closure: %v", err)
	}

	physicalSlotEnvelope := request.envelope
	physicalSlotEnvelope.SlotID = planned.ParentSlotID
	if err := physicalSlotEnvelope.ValidateForCompositeFamilyV1(root, child); err == nil {
		t.Fatal("repair envelope used the physical parent slot as its logical slot")
	}
	wrongPhysicalParent := child
	wrongPhysicalParent.Composite.ParentSlotID = "repair.slot.stale"
	wrongPhysicalParent = workspaceTransferRefreezeManifest(t, wrongPhysicalParent)
	if err := request.envelope.ValidateForCompositeFamilyV1(
		root,
		wrongPhysicalParent,
	); err == nil {
		t.Fatal("repair Child with the wrong physical parent slot was accepted")
	}
}

func TestWorkspaceTransferPlanV1RejectsInitialRepairDrift(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*WorkspaceTransferPlanV1)
	}{
		{"Grant ID", func(plan *WorkspaceTransferPlanV1) {
			plan.TargetGrantID = "grant-family-child-stale"
		}},
		{"Grant digest", func(plan *WorkspaceTransferPlanV1) {
			plan.TargetGrantDigest = strings.Repeat("f", moduleapi.SHA256HexLength)
		}},
		{"Workspace ref", func(plan *WorkspaceTransferPlanV1) {
			plan.TargetWorkspace.Version = "v-target-stale"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _, _, _ := workspaceTransferTestCompositeFamily(t)
			test.mutate(root.Composite.Plan.Decision.RepairChildren[0].Transfer)
			if _, _, err := NewRunManifest(root); err == nil {
				t.Fatal("initial/repair Workspace transfer plan drift was accepted")
			}
		})
	}

	t.Run("missing repair plan", func(t *testing.T) {
		root, _, _, _ := workspaceTransferTestCompositeFamily(t)
		root.Composite.Plan.Decision.RepairChildren[0].Transfer = nil
		if _, _, err := NewRunManifest(root); err == nil {
			t.Fatal("repair Child omitted the initial Workspace transfer plan")
		}
	})
}

func TestWorkspaceTransferPlanV1RequiresDecisionAtCoreBoundary(t *testing.T) {
	root := reviewerTestRootManifest(t, true)
	targetWorkspace := workspaceTransferTestWorkspace(
		"workspace-specialist-external",
		"v9",
		"9",
	)
	rootGrant := workspaceTransferTestGrant(
		"grant-family-root",
		root.Workspace,
		targetWorkspace,
	)
	targetGrant := workspaceTransferTestGrant(
		"grant-family-child",
		targetWorkspace,
		root.Workspace,
	)
	rootGrant.TenantID = root.TenantID
	targetGrant.TenantID = root.TenantID
	transfer, err := NewWorkspaceTransferPlanV1(rootGrant, targetGrant)
	if err != nil {
		t.Fatal(err)
	}
	root.Composite.Plan.Children[0].Transfer = &transfer
	if _, _, err := NewRunManifest(root); err == nil {
		t.Fatal("Workspace transfer without a composite decision plan was accepted")
	}
}

type workspaceTransferAuthorizationFixture struct {
	source   WorkspaceTransferGrantV1
	target   WorkspaceTransferGrantV1
	envelope WorkspaceTransferEnvelopeV1
	resolved WorkspaceTransferResolvedPayloadV1
}

func workspaceTransferTestCompositeFamily(
	t *testing.T,
) (
	RunManifest,
	RunManifest,
	workspaceTransferAuthorizationFixture,
	workspaceTransferAuthorizationFixture,
) {
	return workspaceTransferTestCompositeFamilyAtRound(t, 0)
}

func workspaceTransferTestCompositeRepairFamily(
	t *testing.T,
) (
	RunManifest,
	RunManifest,
	workspaceTransferAuthorizationFixture,
	workspaceTransferAuthorizationFixture,
) {
	return workspaceTransferTestCompositeFamilyAtRound(
		t,
		CompositeRepairRoundOneV1,
	)
}

func workspaceTransferTestCompositeFamilyAtRound(
	t *testing.T,
	repairRound uint32,
) (
	RunManifest,
	RunManifest,
	workspaceTransferAuthorizationFixture,
	workspaceTransferAuthorizationFixture,
) {
	t.Helper()
	if repairRound > CompositeRepairRoundOneV1 {
		t.Fatalf("unsupported test repair round %d", repairRound)
	}
	root := reviewerTestRootManifest(t, true, true)
	childWorkspace := workspaceTransferTestWorkspace(
		"workspace-specialist-external",
		"v9",
		"9",
	)
	rootGrant := workspaceTransferTestGrant(
		"grant-family-root",
		root.Workspace,
		childWorkspace,
	)
	childGrant := workspaceTransferTestGrant(
		"grant-family-child",
		childWorkspace,
		root.Workspace,
	)
	rootGrant.TenantID = root.TenantID
	childGrant.TenantID = root.TenantID
	transfer, err := NewWorkspaceTransferPlanV1(rootGrant, childGrant)
	if err != nil {
		t.Fatalf("family transfer plan: %v", err)
	}
	root.Composite.Plan.Children[0].Transfer = &transfer
	root.Composite.Plan.Decision.RepairChildren[0].Transfer = &transfer
	root = workspaceTransferRefreezeManifest(t, root)
	planned := root.Composite.Plan.Children[0]
	if repairRound == CompositeRepairRoundOneV1 {
		planned = root.Composite.Plan.Decision.RepairChildren[0]
	}
	memberInput := validMemberSnapshotInput()
	memberInput.Agent = planned.Agent
	memberInput.Profile = planned.Profile
	memberInput.Workspace = childWorkspace
	member, _, err := NewMemberExecutionSnapshot(memberInput)
	if err != nil {
		t.Fatalf("family Child member: %v", err)
	}
	childInput := validRunManifestInput(member)
	childInput.AdmissionKey = planned.AdmissionKey
	childInput.RunID = planned.RunID
	childInput.TenantID = root.TenantID
	childInput.Workspace = childWorkspace
	childInput.PrimaryAgent = planned.Agent
	childInput.Members = []MemberSnapshotRef{
		{MemberID: member.MemberID, Digest: planned.MemberSnapshotDigest},
	}
	childInput.PrimaryMemberID = member.MemberID
	childInput.TaskInputRef = root.TaskInputRef
	childInput.TaskInputDigest = root.TaskInputDigest
	childInput.ParentRunID = root.RunID
	childInput.CancellationScope = CancellationScopeInheritedV1
	childInput.RecoveryRootRef = "recovery/transfer-child"
	assignment := planned.Assignment
	childInput.Composite = &CompositeRunNodeV1{
		SchemaVersion:        CompositeRunNodeSchemaVersionV1,
		Role:                 CompositeRunRoleChildV1,
		RepairRound:          repairRound,
		RootRunID:            root.RunID,
		ParentManifestDigest: root.ManifestDigest,
		ParentSlotID:         planned.SlotID,
		Assignment:           &assignment,
	}
	if repairRound == CompositeRepairRoundOneV1 {
		childInput.Composite.ParentSlotID = planned.ParentSlotID
	}
	child, _, err := NewRunManifest(childInput)
	if err != nil {
		t.Fatalf("family Child Manifest: %v", err)
	}

	summary := WorkspaceTaskSummaryV1{
		SchemaVersion:      WorkspaceTaskSummarySchemaVersionV1,
		SourceTaskInputRef: root.TaskInputRef,
		RepairRound:        repairRound,
		Summary:            "Bounded task summary for the external Specialist.",
	}
	if repairRound == CompositeRepairRoundOneV1 {
		summary.PreviousSetDigest = strings.Repeat("a", moduleapi.SHA256HexLength)
		summary.VerdictRef = strings.Repeat("b", moduleapi.SHA256HexLength)
	}
	_, summaryCanonical, err := NewWorkspaceTaskSummaryV1(summary)
	if err != nil {
		t.Fatal(err)
	}
	request := workspaceTransferTestFixture(
		t,
		rootGrant,
		childGrant,
		WorkspaceTransferDirectionRequestV1,
		WorkspaceTransferPayloadTaskSummaryV1,
		WorkspaceTaskSummarySchemaVersionV1,
		root.TaskInputRef,
		workspaceTransferPayloadRecordKindV1,
		summaryCanonical,
	)
	request.envelope.RootRunID = root.RunID
	request.envelope.ChildRunID = child.RunID
	request.envelope.SlotID = planned.SlotID

	_, contributionCanonical, _, err := NewSpecialistContributionV1(
		workspaceTransferTestContribution(),
	)
	if err != nil {
		t.Fatal(err)
	}
	modelResultCanonical := workspaceTransferTestModelResult(
		t,
		string(contributionCanonical),
	)
	result := workspaceTransferTestFixture(
		t,
		childGrant,
		rootGrant,
		WorkspaceTransferDirectionResultV1,
		WorkspaceTransferPayloadSpecialistResultV1,
		SpecialistContributionSchemaVersionV1,
		root.TaskInputRef,
		workspaceTransferModelResultKindV1,
		modelResultCanonical,
	)
	result.envelope.RootRunID = root.RunID
	result.envelope.ChildRunID = child.RunID
	result.envelope.SlotID = planned.SlotID
	return root, child, request, result
}

func workspaceTransferMutateBothPlans(
	t *testing.T,
	root *RunManifest,
	mutate func(*WorkspaceTransferPlanV1),
) {
	t.Helper()
	if root.Composite == nil || root.Composite.Plan == nil ||
		root.Composite.Plan.Decision == nil ||
		root.Composite.Plan.Children[0].Transfer == nil ||
		root.Composite.Plan.Decision.RepairChildren[0].Transfer == nil {
		t.Fatal("test composite family lacks paired Workspace transfer plans")
	}
	mutate(root.Composite.Plan.Children[0].Transfer)
	mutate(root.Composite.Plan.Decision.RepairChildren[0].Transfer)
}

func workspaceTransferTestRequest(t *testing.T) workspaceTransferAuthorizationFixture {
	t.Helper()
	root := workspaceTransferTestWorkspace("workspace-root", "v3", "1")
	specialist := workspaceTransferTestWorkspace("workspace-specialist", "v7", "2")
	source := workspaceTransferTestGrant("grant-root", root, specialist)
	target := workspaceTransferTestGrant("grant-specialist", specialist, root)
	taskInputRef := strings.Repeat("c", moduleapi.SHA256HexLength)
	_, canonical, err := NewWorkspaceTaskSummaryV1(WorkspaceTaskSummaryV1{
		SchemaVersion:      WorkspaceTaskSummarySchemaVersionV1,
		SourceTaskInputRef: taskInputRef,
		Summary:            "Bounded backend task summary.",
	})
	if err != nil {
		t.Fatal(err)
	}
	return workspaceTransferTestFixture(
		t,
		source,
		target,
		WorkspaceTransferDirectionRequestV1,
		WorkspaceTransferPayloadTaskSummaryV1,
		WorkspaceTaskSummarySchemaVersionV1,
		taskInputRef,
		workspaceTransferPayloadRecordKindV1,
		canonical,
	)
}

func workspaceTransferTestResult(t *testing.T) workspaceTransferAuthorizationFixture {
	t.Helper()
	root := workspaceTransferTestWorkspace("workspace-root", "v3", "1")
	specialist := workspaceTransferTestWorkspace("workspace-specialist", "v7", "2")
	rootGrant := workspaceTransferTestGrant("grant-root", root, specialist)
	specialistGrant := workspaceTransferTestGrant(
		"grant-specialist",
		specialist,
		root,
	)
	_, contributionCanonical, _, err := NewSpecialistContributionV1(
		workspaceTransferTestContribution(),
	)
	if err != nil {
		t.Fatal(err)
	}
	modelResultCanonical := workspaceTransferTestModelResult(
		t,
		string(contributionCanonical),
	)
	return workspaceTransferTestFixture(
		t,
		specialistGrant,
		rootGrant,
		WorkspaceTransferDirectionResultV1,
		WorkspaceTransferPayloadSpecialistResultV1,
		SpecialistContributionSchemaVersionV1,
		strings.Repeat("c", moduleapi.SHA256HexLength),
		workspaceTransferModelResultKindV1,
		modelResultCanonical,
	)
}

func workspaceTransferTestFixture(
	t *testing.T,
	source WorkspaceTransferGrantV1,
	target WorkspaceTransferGrantV1,
	direction WorkspaceTransferDirectionV1,
	payloadKind WorkspaceTransferPayloadKindV1,
	payloadSchemaVersion string,
	taskInputRef string,
	contentKind string,
	canonical []byte,
) workspaceTransferAuthorizationFixture {
	t.Helper()
	_, _, sourceDigest, err := NewWorkspaceTransferGrantV1(source)
	if err != nil {
		t.Fatal(err)
	}
	_, _, targetDigest, err := NewWorkspaceTransferGrantV1(target)
	if err != nil {
		t.Fatal(err)
	}
	payloadRef := workspaceTransferContentDigestV1(
		contentKind,
		workspaceTransferJSONMediaTypeV1,
		canonical,
	)
	envelope := WorkspaceTransferEnvelopeV1{
		SchemaVersion:        WorkspaceTransferEnvelopeSchemaVersionV1,
		TenantID:             source.TenantID,
		SourceGrantID:        source.GrantID,
		TargetGrantID:        target.GrantID,
		Direction:            direction,
		PayloadKind:          payloadKind,
		PayloadSchemaVersion: payloadSchemaVersion,
		TaskInputRef:         taskInputRef,
		SourceWorkspace:      source.Workspace,
		TargetWorkspace:      target.Workspace,
		SourceGrantDigest:    sourceDigest,
		TargetGrantDigest:    targetDigest,
		RootRunID:            "run-root",
		ChildRunID:           "run-child",
		SlotID:               "slot.backend",
		PayloadRef:           payloadRef,
		PayloadSizeBytes:     uint32(len(canonical)),
	}
	if _, _, _, err := NewWorkspaceTransferEnvelopeV1(envelope); err != nil {
		t.Fatalf("test envelope: %v", err)
	}
	return workspaceTransferAuthorizationFixture{
		source:   source,
		target:   target,
		envelope: envelope,
		resolved: WorkspaceTransferResolvedPayloadV1{
			PayloadRef:     payloadRef,
			ContentKind:    contentKind,
			MediaType:      workspaceTransferJSONMediaTypeV1,
			CanonicalBytes: bytes.Clone(canonical),
		},
	}
}

func workspaceTransferFixtureWithPayload(
	t *testing.T,
	base workspaceTransferAuthorizationFixture,
	contentKind string,
	canonical []byte,
) workspaceTransferAuthorizationFixture {
	t.Helper()
	payloadRef := workspaceTransferContentDigestV1(
		contentKind,
		workspaceTransferJSONMediaTypeV1,
		canonical,
	)
	base.envelope.PayloadRef = payloadRef
	base.envelope.PayloadSizeBytes = uint32(len(canonical))
	base.resolved = WorkspaceTransferResolvedPayloadV1{
		PayloadRef:     payloadRef,
		ContentKind:    contentKind,
		MediaType:      workspaceTransferJSONMediaTypeV1,
		CanonicalBytes: bytes.Clone(canonical),
	}
	if _, _, _, err := NewWorkspaceTransferEnvelopeV1(base.envelope); err != nil {
		t.Fatalf("test envelope with replacement payload: %v", err)
	}
	return base
}

func workspaceTransferTestGrant(
	grantID string,
	workspace WorkspaceRef,
	peer WorkspaceRef,
) WorkspaceTransferGrantV1 {
	return WorkspaceTransferGrantV1{
		SchemaVersion: WorkspaceTransferGrantSchemaVersionV1,
		GrantID:       grantID,
		TenantID:      "tenant-a",
		Workspace:     workspace,
		PeerWorkspace: peer,
		Revision:      3,
		Enabled:       true,
		SendPayloadKinds: []WorkspaceTransferPayloadKindV1{
			WorkspaceTransferPayloadTaskSummaryV1,
			WorkspaceTransferPayloadSpecialistResultV1,
		},
		ReceivePayloadKinds: []WorkspaceTransferPayloadKindV1{
			WorkspaceTransferPayloadTaskSummaryV1,
			WorkspaceTransferPayloadSpecialistResultV1,
		},
		MaxSendPayloadBytes:    4096,
		MaxReceivePayloadBytes: 4096,
	}
}

func workspaceTransferTestGrantDigest(
	t *testing.T,
	grant WorkspaceTransferGrantV1,
) string {
	t.Helper()
	_, _, digest, err := NewWorkspaceTransferGrantV1(grant)
	if err != nil {
		t.Fatalf("test grant digest: %v", err)
	}
	return digest
}

func workspaceTransferRefreezeManifest(
	t *testing.T,
	manifest RunManifest,
) RunManifest {
	t.Helper()
	frozen, _, err := NewRunManifest(manifest)
	if err != nil {
		t.Fatalf("refreeze test Manifest: %v", err)
	}
	return frozen
}

func workspaceTransferTestWorkspace(id string, version string, fill string) WorkspaceRef {
	return WorkspaceRef{
		ID:      id,
		Version: version,
		Digest:  strings.Repeat(fill, moduleapi.SHA256HexLength),
	}
}

func workspaceTransferTestContribution() SpecialistContributionV1 {
	return SpecialistContributionV1{
		SchemaVersion: SpecialistContributionSchemaVersionV1,
		Proposal:      "Use an idempotent bounded worker.",
		Evidence: []SpecialistEvidenceV1{
			{
				Ref:          strings.Repeat("d", moduleapi.SHA256HexLength),
				BoundedClaim: "The current protocol binds one immutable attempt.",
			},
		},
		Assumptions: []string{},
		Risks:       []string{"A stale Workspace version must fail closed."},
		Conflicts:   []string{},
	}
}

func workspaceTransferTestModelResult(
	t *testing.T,
	assistantText string,
) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: assistantText,
		},
	)
	if err != nil {
		t.Fatalf("test MODEL_RESULT: %v", err)
	}
	return canonical
}

func workspaceTransferCanonicalWithUnknown(t *testing.T, canonical []byte) []byte {
	t.Helper()
	if len(canonical) < 2 || canonical[0] != '{' {
		t.Fatalf("test input is not a JSON object: %q", canonical)
	}
	raw := append([]byte(`{"unknown_workspace_transfer_field":true,`), canonical[1:]...)
	withUnknown, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		t.Fatalf("canonicalize unknown-field fixture: %v", err)
	}
	return withUnknown
}

func workspaceTransferDifferentDigest(input string) string {
	candidate := strings.Repeat("f", moduleapi.SHA256HexLength)
	if input == candidate {
		return strings.Repeat("e", moduleapi.SHA256HexLength)
	}
	return candidate
}
