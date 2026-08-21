package corecontract

import (
	"bytes"
	"testing"
)

func TestContextCompilationV1CompositeChildBelowWatermarkRoundTrip(
	t *testing.T,
) {
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationCompositeBelowWatermark
	value.Composite = &CompositeContextEvidenceV1{
		Role: CompositeRunRoleChildV1,
		Assignment: &CompositeAssignmentV1{
			SlotID: "analysis", FocusID: "risk", WeightBasisPoints: 6500,
		},
	}
	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	value.Composite.Assignment.FocusID = "mutated"
	if frozen.Composite.Assignment.FocusID != "risk" {
		t.Fatal("NewContextCompilationV1 aliased Composite assignment")
	}
	restored, err := RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuilt, err := NewContextCompilationV1(restored)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		t.Fatalf("Composite CHILD round trip error=%v", err)
	}
}

func TestContextCompilationV1CompositeRootBudgetRoundTrip(t *testing.T) {
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationCompositeBelowWatermark
	budget, err := CompositeChildResultBudgetTokensV1(value.InputBudgetTokens)
	if err != nil {
		t.Fatal(err)
	}
	assignments := []CompositeAssignmentV1{
		{SlotID: "a", FocusID: "risk", WeightBasisPoints: 7000},
		{SlotID: "b", FocusID: "delivery", WeightBasisPoints: 3000},
	}
	allocations, err := CompositeChildResultAllocationsTokensV1(
		budget,
		assignments,
	)
	if err != nil {
		t.Fatal(err)
	}
	value.Composite = &CompositeContextEvidenceV1{
		Role:                    CompositeRunRoleRootV1,
		ChildResultBudgetTokens: budget,
		ChildResults: []CompositeChildResultEvidenceV1{
			{
				RunID:                "child-a",
				ChildManifestDigest:  compilationDigest("a"),
				MemberSnapshotDigest: compilationDigest("d"),
				ResultRef:            compilationDigest("e"),
				TerminalRevision:     4,
				Assignment:           assignments[0],
				AllocatedTokens:      allocations[0],
				EstimatedTokens:      150,
				OriginalBytes:        100,
				RetainedBytes:        100,
			},
			{
				RunID:                "child-b",
				ChildManifestDigest:  compilationDigest("b"),
				MemberSnapshotDigest: compilationDigest("f"),
				ResultRef:            compilationDigest("1"),
				TerminalRevision:     5,
				Assignment:           assignments[1],
				AllocatedTokens:      allocations[1],
				EstimatedTokens:      100,
				OriginalBytes:        50,
				RetainedBytes:        50,
			},
		},
	}
	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	value.Composite.ChildResults[0].Assignment.FocusID = "mutated"
	if frozen.Composite.ChildResults[0].Assignment.FocusID != "risk" {
		t.Fatal("NewContextCompilationV1 aliased Composite Child results")
	}
	restored, err := RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuilt, err := NewContextCompilationV1(restored)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		t.Fatalf("Composite ROOT round trip error=%v", err)
	}
}

func TestContextCompilationV1WorkspaceTransferEvidenceRoundTripAndOrder(
	t *testing.T,
) {
	value := validWorkspaceTransferRootCompilationV1(t)
	frozen, canonical, err := NewContextCompilationV1(value)
	if err != nil {
		t.Fatal(err)
	}
	value.WorkspaceTransfers[0].SlotID = "mutated"
	if frozen.WorkspaceTransfers[0].SlotID != "a" {
		t.Fatal("NewContextCompilationV1 aliased Workspace transfer evidence")
	}
	restored, err := RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	_, rebuilt, err := NewContextCompilationV1(restored)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		t.Fatalf("Workspace transfer round trip error=%v", err)
	}

	reordered := validWorkspaceTransferRootCompilationV1(t)
	reordered.WorkspaceTransfers[0], reordered.WorkspaceTransfers[1] =
		reordered.WorkspaceTransfers[1], reordered.WorkspaceTransfers[0]
	if _, _, err := NewContextCompilationV1(reordered); err == nil {
		t.Fatal("accepted Workspace transfer evidence outside Child-result order")
	}
}

func TestContextCompilationV1WorkspaceTransferRequestAndTampering(
	t *testing.T,
) {
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationCompositeBelowWatermark
	value.Composite = &CompositeContextEvidenceV1{
		Role: CompositeRunRoleChildV1,
		Assignment: &CompositeAssignmentV1{
			SlotID: "a", FocusID: "risk", WeightBasisPoints: 7000,
		},
	}
	value.WorkspaceTransfers = []WorkspaceTransferEvidenceV1{{
		Direction:      WorkspaceTransferDirectionRequestV1,
		PayloadKind:    WorkspaceTransferPayloadTaskSummaryV1,
		EnvelopeRef:    compilationDigest("2"),
		EnvelopeDigest: compilationDigest("3"),
		PayloadRef:     compilationDigest("4"),
		RootRunID:      "root-run",
		ChildRunID:     "child-a",
		SlotID:         "a",
	}}
	if _, _, err := NewContextCompilationV1(value); err != nil {
		t.Fatalf("valid Workspace transfer REQUEST: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*ContextCompilationV1)
	}{
		{"wrong slot", func(value *ContextCompilationV1) {
			value.WorkspaceTransfers[0].SlotID = "b"
		}},
		{"wrong kind", func(value *ContextCompilationV1) {
			value.WorkspaceTransfers[0].PayloadKind = WorkspaceTransferPayloadSpecialistResultV1
		}},
		{"invalid envelope ref", func(value *ContextCompilationV1) {
			value.WorkspaceTransfers[0].EnvelopeRef = "bad"
		}},
		{"extra request", func(value *ContextCompilationV1) {
			second := value.WorkspaceTransfers[0]
			second.EnvelopeRef = compilationDigest("5")
			second.EnvelopeDigest = compilationDigest("6")
			second.PayloadRef = compilationDigest("7")
			second.ChildRunID = "child-b"
			second.SlotID = "b"
			value.WorkspaceTransfers = append(value.WorkspaceTransfers, second)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := value
			candidate.Composite = &CompositeContextEvidenceV1{
				Role:       value.Composite.Role,
				Assignment: &CompositeAssignmentV1{SlotID: "a", FocusID: "risk", WeightBasisPoints: 7000},
			}
			candidate.WorkspaceTransfers = append(
				[]WorkspaceTransferEvidenceV1(nil),
				value.WorkspaceTransfers...,
			)
			test.mutate(&candidate)
			if _, _, err := NewContextCompilationV1(candidate); err == nil {
				t.Fatal("accepted tampered Workspace transfer evidence")
			}
		})
	}
}

func validWorkspaceTransferRootCompilationV1(
	t *testing.T,
) ContextCompilationV1 {
	t.Helper()
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationCompositeBelowWatermark
	budget, err := CompositeChildResultBudgetTokensV1(value.InputBudgetTokens)
	if err != nil {
		t.Fatal(err)
	}
	assignments := []CompositeAssignmentV1{
		{SlotID: "a", FocusID: "risk", WeightBasisPoints: 7000},
		{SlotID: "b", FocusID: "delivery", WeightBasisPoints: 3000},
	}
	allocations, err := CompositeChildResultAllocationsTokensV1(
		budget,
		assignments,
	)
	if err != nil {
		t.Fatal(err)
	}
	value.Composite = &CompositeContextEvidenceV1{
		Role:                    CompositeRunRoleRootV1,
		ChildResultBudgetTokens: budget,
		ChildResults: []CompositeChildResultEvidenceV1{
			{
				RunID: "child-a", ChildManifestDigest: compilationDigest("a"),
				MemberSnapshotDigest: compilationDigest("d"), ResultRef: compilationDigest("e"),
				TerminalRevision: 4, Assignment: assignments[0], AllocatedTokens: allocations[0],
				EstimatedTokens: 150, OriginalBytes: 100, RetainedBytes: 100,
			},
			{
				RunID: "child-b", ChildManifestDigest: compilationDigest("b"),
				MemberSnapshotDigest: compilationDigest("f"), ResultRef: compilationDigest("1"),
				TerminalRevision: 5, Assignment: assignments[1], AllocatedTokens: allocations[1],
				EstimatedTokens: 100, OriginalBytes: 50, RetainedBytes: 50,
			},
		},
	}
	value.WorkspaceTransfers = []WorkspaceTransferEvidenceV1{
		{
			Direction: WorkspaceTransferDirectionResultV1, PayloadKind: WorkspaceTransferPayloadSpecialistResultV1,
			EnvelopeRef: compilationDigest("2"), EnvelopeDigest: compilationDigest("3"),
			PayloadRef: compilationDigest("e"), RootRunID: "root-run", ChildRunID: "child-a", SlotID: "a",
		},
		{
			Direction: WorkspaceTransferDirectionResultV1, PayloadKind: WorkspaceTransferPayloadSpecialistResultV1,
			EnvelopeRef: compilationDigest("4"), EnvelopeDigest: compilationDigest("5"),
			PayloadRef: compilationDigest("1"), RootRunID: "root-run", ChildRunID: "child-b", SlotID: "b",
		},
	}
	return value
}

func TestContextCompilationV1CompositeEvidenceFailsClosed(t *testing.T) {
	valid := func() ContextCompilationV1 {
		value := validCompilationBase()
		value.OriginalEstimateTokens = 700
		value.FinalEstimateTokens = 700
		value.StopReason = ContextCompilationCompositeBelowWatermark
		value.Composite = &CompositeContextEvidenceV1{
			Role:                    CompositeRunRoleRootV1,
			ChildResultBudgetTokens: 500,
			ChildResults: []CompositeChildResultEvidenceV1{
				{
					RunID:                "child-a",
					ChildManifestDigest:  compilationDigest("a"),
					MemberSnapshotDigest: compilationDigest("d"),
					ResultRef:            compilationDigest("e"),
					TerminalRevision:     4,
					Assignment: CompositeAssignmentV1{
						SlotID: "a", FocusID: "risk", WeightBasisPoints: 7000,
					},
					AllocatedTokens: 350,
					EstimatedTokens: 150,
					OriginalBytes:   100,
					RetainedBytes:   100,
				},
				{
					RunID:                "child-b",
					ChildManifestDigest:  compilationDigest("b"),
					MemberSnapshotDigest: compilationDigest("f"),
					ResultRef:            compilationDigest("1"),
					TerminalRevision:     5,
					Assignment: CompositeAssignmentV1{
						SlotID: "b", FocusID: "delivery", WeightBasisPoints: 3000,
					},
					AllocatedTokens: 150,
					EstimatedTokens: 100,
					OriginalBytes:   50,
					RetainedBytes:   50,
				},
			},
		}
		return value
	}
	tests := []struct {
		name   string
		mutate func(*ContextCompilationV1)
	}{
		{
			name: "wrong half budget",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResultBudgetTokens++
			},
		},
		{
			name: "result order",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResults[0], value.Composite.ChildResults[1] =
					value.Composite.ChildResults[1], value.Composite.ChildResults[0]
			},
		},
		{
			name: "weight closure",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResults[1].Assignment.WeightBasisPoints = 2999
			},
		},
		{
			name: "allocation",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResults[0].AllocatedTokens--
			},
		},
		{
			name: "Child Manifest digest",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResults[0].ChildManifestDigest = ""
			},
		},
		{
			name: "duplicate Child Manifest digest",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResults[1].ChildManifestDigest =
					value.Composite.ChildResults[0].ChildManifestDigest
			},
		},
		{
			name: "duplicate Member snapshot digest",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResults[1].MemberSnapshotDigest =
					value.Composite.ChildResults[0].MemberSnapshotDigest
			},
		},
		{
			name: "terminal revision",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResults[0].TerminalRevision = 0
			},
		},
		{
			name: "estimated tokens exceed allocation",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResults[0].EstimatedTokens = 351
			},
		},
		{
			name: "truncation flag",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResults[0].Truncated = true
			},
		},
		{
			name: "retained bytes differ",
			mutate: func(value *ContextCompilationV1) {
				value.Composite.ChildResults[0].RetainedBytes--
			},
		},
		{
			name: "wrong below-watermark reason",
			mutate: func(value *ContextCompilationV1) {
				value.StopReason = ContextCompilationNoEligibleSummary
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid()
			test.mutate(&value)
			if _, _, err := NewContextCompilationV1(value); err == nil {
				t.Fatal("accepted invalid Composite compilation evidence")
			}
		})
	}
}

func TestCompositeChildResultBudgetV1UsesHalfAndDistributesRemainder(
	t *testing.T,
) {
	budget, err := CompositeChildResultBudgetTokensV1(1001)
	if err != nil || budget != 500 {
		t.Fatalf("half budget=%d error=%v", budget, err)
	}
	allocations, err := CompositeChildResultAllocationsTokensV1(
		5,
		[]CompositeAssignmentV1{
			{SlotID: "a", FocusID: "first", WeightBasisPoints: 3334},
			{SlotID: "b", FocusID: "second", WeightBasisPoints: 3333},
			{SlotID: "c", FocusID: "third", WeightBasisPoints: 3333},
		},
	)
	if err != nil || !equalUint64s(allocations, []uint64{2, 2, 1}) {
		t.Fatalf("allocations=%v error=%v", allocations, err)
	}
	if _, err := CompositeChildResultAllocationsTokensV1(
		budget,
		[]CompositeAssignmentV1{
			{SlotID: "b", FocusID: "second", WeightBasisPoints: 5000},
			{SlotID: "a", FocusID: "first", WeightBasisPoints: 5000},
		},
	); err == nil {
		t.Fatal("accepted non-canonical Composite assignment order")
	}
}

func TestRestoreContextCompilationV1RejectsCompositeTruncation(t *testing.T) {
	value := validCompilationBase()
	value.OriginalEstimateTokens = 700
	value.FinalEstimateTokens = 700
	value.StopReason = ContextCompilationCompositeBelowWatermark
	value.Composite = &CompositeContextEvidenceV1{
		Role:                    CompositeRunRoleRootV1,
		ChildResultBudgetTokens: 500,
		ChildResults: []CompositeChildResultEvidenceV1{
			{
				RunID:                "child-a",
				ChildManifestDigest:  compilationDigest("a"),
				MemberSnapshotDigest: compilationDigest("d"),
				ResultRef:            compilationDigest("e"),
				TerminalRevision:     4,
				Assignment: CompositeAssignmentV1{
					SlotID: "a", FocusID: "risk", WeightBasisPoints: 7000,
				},
				AllocatedTokens: 350,
				EstimatedTokens: 150,
				OriginalBytes:   100,
				RetainedBytes:   99,
				Truncated:       true,
			},
			{
				RunID:                "child-b",
				ChildManifestDigest:  compilationDigest("b"),
				MemberSnapshotDigest: compilationDigest("f"),
				ResultRef:            compilationDigest("1"),
				TerminalRevision:     5,
				Assignment: CompositeAssignmentV1{
					SlotID: "b", FocusID: "delivery", WeightBasisPoints: 3000,
				},
				AllocatedTokens: 150,
				EstimatedTokens: 100,
				OriginalBytes:   50,
				RetainedBytes:   50,
			},
		},
	}
	canonical, err := canonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreContextCompilationV1(canonical); err == nil {
		t.Fatal("Restore accepted truncated Composite Child-result evidence")
	}
}

func equalUint64s(left []uint64, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
