package currentstore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
)

// workspaceTransferStoreFixture is shared by the X1 Admission, Attempt and
// recovery tests. Slot zero and its repair Child execute in TargetWorkspace;
// every other Specialist, both Reviewers and Root remain in RootWorkspace.
type workspaceTransferStoreFixture struct {
	*compositeAdmissionFixture
	RootWorkspace   corecontract.WorkspaceRef
	TargetWorkspace corecontract.WorkspaceRef
	RootGrant       corecontract.WorkspaceTransferGrantV1
	TargetGrant     corecontract.WorkspaceTransferGrantV1
}

func newWorkspaceTransferStoreFixture(
	t *testing.T,
) *workspaceTransferStoreFixture {
	t.Helper()
	var rootWorkspace corecontract.WorkspaceRef
	var targetWorkspace corecontract.WorkspaceRef
	var rootGrant corecontract.WorkspaceTransferGrantV1
	var targetGrant corecontract.WorkspaceTransferGrantV1
	base := newCompositeDecisionStoreFixtureWithControlMutator(
		t,
		2,
		func(control *controlcontract.ControlSnapshot) {
			if len(control.Workspaces) != 1 ||
				len(control.CompositeAgents) != 1 ||
				len(control.CompositeAgents[0].Members) != 2 {
				t.Fatal("unexpected base decision Control shape")
			}
			rootWorkspace = control.Workspaces[0].Workspace
			targetWorkspace = corecontract.WorkspaceRef{
				ID:      "workspace-transfer-target",
				Version: "v1",
				Digest:  decisionTestDigest("workspace-transfer-target", 1),
			}
			rootGrant = workspaceTransferTestGrantV1(
				"grant-root-target",
				control.TenantID,
				rootWorkspace,
				targetWorkspace,
				true,
			)
			targetGrant = workspaceTransferTestGrantV1(
				"grant-target-root",
				control.TenantID,
				targetWorkspace,
				rootWorkspace,
				false,
			)
			control.Workspaces[0].TransferGrants =
				[]corecontract.WorkspaceTransferGrantV1{rootGrant}
			control.Workspaces = append(
				control.Workspaces,
				controlcontract.WorkspaceDefinition{
					Workspace:      targetWorkspace,
					BudgetPolicy:   control.Workspaces[0].BudgetPolicy,
					TransferGrants: []corecontract.WorkspaceTransferGrantV1{targetGrant},
				},
			)
			control.CompositeAgents[0].Members[0].TargetWorkspaceID =
				targetWorkspace.ID
		},
	)
	if base.compiled.Parent.RunManifest.Workspace != rootWorkspace ||
		base.compiled.Children[0].RunManifest.Workspace != targetWorkspace ||
		base.compiled.Parent.RunManifest.Composite.Plan.Children[0].Transfer == nil ||
		base.compiled.Parent.RunManifest.Composite.Plan.Children[1].Transfer != nil ||
		base.compiled.Decision.RepairChildren[0].RunManifest.Workspace !=
			targetWorkspace ||
		base.compiled.Parent.RunManifest.Composite.Plan.Decision.
			RepairChildren[0].Transfer == nil ||
		base.compiled.Parent.RunManifest.Composite.Plan.Decision.
			RepairChildren[1].Transfer != nil ||
		base.compiled.Reviewer.RunManifest.Workspace != rootWorkspace ||
		base.compiled.Decision.RepairReviewer.RunManifest.Workspace != rootWorkspace {
		t.Fatal("compiled Workspace transfer family routing differs")
	}
	return &workspaceTransferStoreFixture{
		compositeAdmissionFixture: base,
		RootWorkspace:             rootWorkspace,
		TargetWorkspace:           targetWorkspace,
		RootGrant:                 rootGrant,
		TargetGrant:               targetGrant,
	}
}

func newCommittedWorkspaceTransferStoreFixture(
	t *testing.T,
) *workspaceTransferStoreFixture {
	t.Helper()
	fixture := newWorkspaceTransferStoreFixture(t)
	if _, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatalf("CommitCompositeRunFamily Workspace transfer: %v", err)
	}
	return fixture
}

func workspaceTransferTestGrantV1(
	id string,
	tenantID string,
	owner corecontract.WorkspaceRef,
	peer corecontract.WorkspaceRef,
	root bool,
) corecontract.WorkspaceTransferGrantV1 {
	grant := corecontract.WorkspaceTransferGrantV1{
		SchemaVersion: corecontract.WorkspaceTransferGrantSchemaVersionV1,
		GrantID:       id,
		TenantID:      tenantID,
		Workspace:     owner,
		PeerWorkspace: peer,
		Revision:      1,
		Enabled:       true,
	}
	if root {
		grant.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadTaskSummaryV1,
		}
		grant.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadSpecialistResultV1,
		}
		grant.MaxSendPayloadBytes =
			corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1
		grant.MaxReceivePayloadBytes =
			corecontract.WorkspaceTransferMaximumPayloadBytesV1
	} else {
		grant.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadSpecialistResultV1,
		}
		grant.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadTaskSummaryV1,
		}
		grant.MaxSendPayloadBytes =
			corecontract.WorkspaceTransferMaximumPayloadBytesV1
		grant.MaxReceivePayloadBytes =
			corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1
	}
	return grant
}

func TestCommitCompositeWorkspaceTransferFamilyClosesTargetAndHistoricalGrants(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	resolved, found, err := fixture.store.ResolveCompositeRunFamily(
		context.Background(),
		fixture.parentIntent.TenantID,
		fixture.parentIntent.AdmissionKey,
		fixture.input.Parent.IntentDigest,
	)
	if err != nil || !found {
		t.Fatalf("ResolveCompositeRunFamily found=%v error=%v", found, err)
	}
	if resolved.Children[0].RunID != fixture.compiled.Children[0].RunManifest.RunID ||
		resolved.RepairChildren[0].RunID !=
			fixture.compiled.Decision.RepairChildren[0].RunManifest.RunID {
		t.Fatal("resolved cross-Workspace Child family differs")
	}
	for _, runID := range []string{
		fixture.compiled.Children[0].RunManifest.RunID,
		fixture.compiled.Decision.RepairChildren[0].RunManifest.RunID,
	} {
		var workspaceID string
		if err := fixture.store.db.QueryRow(
			`SELECT workspace_id FROM runs WHERE run_id=?`,
			runID,
		).Scan(&workspaceID); err != nil || workspaceID != fixture.TargetWorkspace.ID {
			t.Fatalf("Run %s workspace=%q error=%v", runID, workspaceID, err)
		}
	}
	for _, runID := range []string{
		fixture.compiled.Parent.RunManifest.RunID,
		fixture.compiled.Children[1].RunManifest.RunID,
		fixture.compiled.Reviewer.RunManifest.RunID,
		fixture.compiled.Decision.RepairChildren[1].RunManifest.RunID,
		fixture.compiled.Decision.RepairReviewer.RunManifest.RunID,
	} {
		var workspaceID string
		if err := fixture.store.db.QueryRow(
			`SELECT workspace_id FROM runs WHERE run_id=?`,
			runID,
		).Scan(&workspaceID); err != nil || workspaceID != fixture.RootWorkspace.ID {
			t.Fatalf("Run %s workspace=%q error=%v", runID, workspaceID, err)
		}
	}
}

func TestCommitCompositeWorkspaceTransferFamilyRejectsWrongWorkspaceAndPlan(
	t *testing.T,
) {
	t.Run("Child Workspace", func(t *testing.T) {
		fixture := newWorkspaceTransferStoreFixture(t)
		input := cloneWorkspaceTransferFamilyInput(fixture.input)
		child := restoreCompositeManifest(t, input.Children[0])
		child.Workspace = fixture.RootWorkspace
		_, canonical, err := corecontract.NewRunManifest(child)
		if err != nil {
			t.Fatalf("freeze wrong-Workspace Child: %v", err)
		}
		input.Children[0].RunManifestCanonical = canonical
		assertCompositeAdmissionRejectedWithoutWrites(
			t,
			fixture.store,
			input,
			ErrInvalidAdmission,
		)
	})

	t.Run("Grant plan", func(t *testing.T) {
		fixture := newWorkspaceTransferStoreFixture(t)
		input := cloneWorkspaceTransferFamilyInput(fixture.input)
		parent := restoreCompositeManifest(t, input.Parent)
		parent.Composite.Plan.Children[0].Transfer.RootGrantID =
			"grant-root-target-wrong"
		parent.Composite.Plan.Decision.RepairChildren[0].Transfer.RootGrantID =
			"grant-root-target-wrong"
		frozenParent, parentCanonical, err :=
			corecontract.NewRunManifest(parent)
		if err != nil {
			t.Fatalf("freeze wrong transfer plan: %v", err)
		}
		input.Parent.RunManifestCanonical = parentCanonical
		rewriteParentDigest := func(run *CommitRunAdmissionInput) {
			manifest := restoreCompositeManifest(t, *run)
			manifest.Composite.ParentManifestDigest = frozenParent.ManifestDigest
			_, canonical, freezeErr := corecontract.NewRunManifest(manifest)
			if freezeErr != nil {
				t.Fatalf("freeze transfer participant: %v", freezeErr)
			}
			run.RunManifestCanonical = canonical
		}
		for index := range input.Children {
			rewriteParentDigest(&input.Children[index])
		}
		rewriteParentDigest(input.Reviewer)
		for index := range input.RepairChildren {
			rewriteParentDigest(&input.RepairChildren[index])
		}
		rewriteParentDigest(input.RepairReviewer)
		assertCompositeAdmissionRejectedWithoutWrites(
			t,
			fixture.store,
			input,
			ErrAdmissionConflict,
		)
	})
}

func cloneWorkspaceTransferFamilyInput(
	input CommitCompositeRunFamilyInput,
) CommitCompositeRunFamilyInput {
	cloned := input
	cloneRun := func(value CommitRunAdmissionInput) CommitRunAdmissionInput {
		value.IntentCanonical = bytes.Clone(value.IntentCanonical)
		value.MemberSnapshotCanonical = bytes.Clone(value.MemberSnapshotCanonical)
		value.RunManifestCanonical = bytes.Clone(value.RunManifestCanonical)
		value.Contents = append([]ContentInput(nil), value.Contents...)
		return value
	}
	cloned.Parent = cloneRun(input.Parent)
	cloned.Children = make([]CommitRunAdmissionInput, len(input.Children))
	for index := range input.Children {
		cloned.Children[index] = cloneRun(input.Children[index])
	}
	if input.Reviewer != nil {
		reviewer := cloneRun(*input.Reviewer)
		cloned.Reviewer = &reviewer
	}
	cloned.RepairChildren = make(
		[]CommitRunAdmissionInput,
		len(input.RepairChildren),
	)
	for index := range input.RepairChildren {
		cloned.RepairChildren[index] = cloneRun(input.RepairChildren[index])
	}
	if input.RepairReviewer != nil {
		reviewer := cloneRun(*input.RepairReviewer)
		cloned.RepairReviewer = &reviewer
	}
	return cloned
}

func TestCompositeWorkspaceTransferRetryUsesHistoricalControlAfterRevocation(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	publishWorkspaceTransferRevocation(t, fixture)
	resolved, found, err := fixture.store.ResolveCompositeRunFamily(
		context.Background(),
		fixture.parentIntent.TenantID,
		fixture.parentIntent.AdmissionKey,
		fixture.input.Parent.IntentDigest,
	)
	if err != nil || !found || resolved.Created {
		t.Fatalf("historical Resolve found=%v result=%+v error=%v", found, resolved, err)
	}
	retried, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	)
	if err != nil || retried.Created {
		t.Fatalf("historical retry result=%+v error=%v", retried, err)
	}
}

func TestCompositeWorkspaceTransferRevocationRejectsUncommittedAdmission(
	t *testing.T,
) {
	fixture := newWorkspaceTransferStoreFixture(t)
	publishWorkspaceTransferRevocation(t, fixture)
	before := admissionCommitCounts(t, fixture.store)

	if _, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	); !errors.Is(err, ErrAdmissionConflict) {
		t.Fatalf("uncommitted Admission after grant revocation error=%v want ErrAdmissionConflict", err)
	}
	if after := admissionCommitCounts(t, fixture.store); after != before {
		t.Fatalf(
			"rejected Admission after grant revocation changed rows: before=%v after=%v",
			before,
			after,
		)
	}
	assertAdmissionKeyAbsent(
		t,
		fixture.store,
		fixture.parentIntent.TenantID,
		fixture.parentIntent.AdmissionKey,
		fixture.input.Parent.IntentDigest,
	)
}

func publishWorkspaceTransferRevocation(
	t *testing.T,
	fixture *workspaceTransferStoreFixture,
) {
	t.Helper()
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.controlCanonical,
		fixture.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	control.SnapshotID = "control-workspace-transfer-revoked"
	control.Revision++
	for index := range control.Workspaces {
		control.Workspaces[index].TransferGrants = nil
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze revoked Control: %v", err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		fixture.catalogCanonical,
		fixture.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-workspace-transfer-revoked"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze revoked Catalog: %v", err)
	}
	if _, err := fixture.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: fixture.basis.PointerRevision,
			NewPointerRevision:      fixture.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatalf("publish revoked Control/Catalog: %v", err)
	}
}

func TestCompositeWorkspaceTransferRecoveryRejectsWorkspaceMemberAndGrantDrift(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(*workspaceTransferStoreFixture)
	}{
		{
			name: "Run Workspace",
			mutate: func(fixture *workspaceTransferStoreFixture) {
				execClosedFileTamperV1(t, fixture.store, nil,
					`UPDATE runs SET workspace_id=? WHERE run_id=?`,
					fixture.RootWorkspace.ID,
					fixture.compiled.Children[0].RunManifest.RunID,
				)
			},
		},
		{
			name: "Member historical Control",
			mutate: func(fixture *workspaceTransferStoreFixture) {
				var canonical []byte
				if err := fixture.store.db.QueryRow(
					`SELECT canonical_json FROM member_execution_snapshots WHERE run_id=?`,
					fixture.compiled.Children[0].RunManifest.RunID,
				).Scan(&canonical); err != nil {
					t.Fatal(err)
				}
				changed := bytes.Replace(
					canonical,
					[]byte(fixture.TargetWorkspace.ID),
					[]byte(strings.Repeat("x", len(fixture.TargetWorkspace.ID))),
					1,
				)
				if bytes.Equal(changed, canonical) {
					t.Fatal("target Workspace absent from member canonical")
				}
				execClosedFileTamperV1(t, fixture.store,
					[]string{"member_execution_snapshots_reject_update"},
					`UPDATE member_execution_snapshots SET canonical_json=? WHERE run_id=?`,
					changed,
					fixture.compiled.Children[0].RunManifest.RunID,
				)
			},
		},
		{
			name: "Grant canonical",
			mutate: func(fixture *workspaceTransferStoreFixture) {
				var canonical []byte
				var digest string
				if err := fixture.store.db.QueryRow(
					`SELECT canonical_json,digest FROM control_snapshots WHERE snapshot_id=?`,
					fixture.basis.Control.SnapshotID,
				).Scan(&canonical, &digest); err != nil {
					t.Fatal(err)
				}
				changed := bytes.Replace(
					canonical,
					[]byte(`"grant-root-target"`),
					[]byte(`"grant-root-targeu"`),
					1,
				)
				if bytes.Equal(changed, canonical) {
					t.Fatal("grant fixture bytes absent")
				}
				execClosedFileTamperV1(t, fixture.store,
					[]string{"control_snapshots_reject_update"},
					`UPDATE control_snapshots SET canonical_json=? WHERE snapshot_id=?`,
					changed,
					fixture.basis.Control.SnapshotID,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCommittedWorkspaceTransferStoreFixture(t)
			test.mutate(fixture)
			_, found, err := fixture.store.ResolveCompositeRunFamily(
				context.Background(),
				fixture.parentIntent.TenantID,
				fixture.parentIntent.AdmissionKey,
				fixture.input.Parent.IntentDigest,
			)
			if !found || err == nil ||
				(!errors.Is(err, ErrAdmissionIntegrity) &&
					!strings.Contains(err.Error(), "integrity")) {
				t.Fatalf("tampered Resolve found=%v error=%v", found, err)
			}
		})
	}
}

func TestLoadRunForLoopSeparatesWorkspaceTransferTaskFromModelVisibleContents(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	crossLease := acquireCompositeTestLease(
		t,
		fixture.store,
		fixture.compiled.Children[0].RunManifest.RunID,
		"owner-workspace-transfer-cross",
	)
	cross, err := fixture.store.LoadRunForLoop(
		context.Background(),
		crossLease,
	)
	if err != nil {
		t.Fatalf("LoadRunForLoop cross Workspace: %v", err)
	}
	if cross.WorkspaceTransfer == nil {
		t.Fatal("cross-Workspace Child lacks Host transfer material")
	}
	material := cross.WorkspaceTransfer
	if material.RootManifest.ManifestDigest !=
		fixture.compiled.Parent.RunManifest.ManifestDigest ||
		material.ChildManifest.ManifestDigest !=
			fixture.compiled.Children[0].RunManifest.ManifestDigest ||
		material.Plan != *fixture.compiled.Parent.RunManifest.Composite.Plan.
			Children[0].Transfer ||
		material.RootTaskInput.Digest != cross.Manifest.TaskInputRef ||
		material.RootTaskInput.Kind != ContentTaskInput ||
		material.RepairRound != 0 || material.PreviousContributionSet != nil ||
		material.RepairVerdict != nil || material.RepairBasis != nil {
		t.Fatalf("cross Workspace material differs: %+v", material)
	}
	if _, found := cross.FindContent(cross.Manifest.TaskInputRef); found {
		t.Fatal("cross-Workspace TASK_INPUT leaked into generic Contents")
	}
	if _, err := corecontract.RestoreTaskInputV1(
		material.RootTaskInput.CanonicalBytes,
	); err != nil {
		t.Fatalf("Host-only TASK_INPUT: %v", err)
	}

	sameLease := acquireCompositeTestLease(
		t,
		fixture.store,
		fixture.compiled.Children[1].RunManifest.RunID,
		"owner-workspace-transfer-same",
	)
	same, err := fixture.store.LoadRunForLoop(
		context.Background(),
		sameLease,
	)
	if err != nil {
		t.Fatalf("LoadRunForLoop same Workspace: %v", err)
	}
	if same.WorkspaceTransfer != nil {
		t.Fatal("same-Workspace Child gained transfer material")
	}
	if task, found := same.FindContent(same.Manifest.TaskInputRef); !found || task.Kind != ContentTaskInput {
		t.Fatal("same-Workspace legacy TASK_INPUT path changed")
	}

	material.RootGrant.SendPayloadKinds[0] =
		corecontract.WorkspaceTransferPayloadSpecialistResultV1
	material.RootTaskInput.CanonicalBytes[0] ^= 0x01
	reloaded, err := fixture.store.LoadRunForLoop(
		context.Background(),
		crossLease,
	)
	if err != nil || reloaded.WorkspaceTransfer == nil ||
		reloaded.WorkspaceTransfer.RootGrant.SendPayloadKinds[0] !=
			corecontract.WorkspaceTransferPayloadTaskSummaryV1 ||
		bytes.Equal(
			reloaded.WorkspaceTransfer.RootTaskInput.CanonicalBytes,
			material.RootTaskInput.CanonicalBytes,
		) {
		t.Fatalf("Host transfer material aliases prior read error=%v", err)
	}
}

func TestCompositeWorkspaceTransferResultProjectionUsesPersistedEnvelope(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	putCompositeModelPrice(t, fixture.store)
	finishDecisionSpecialist(
		t,
		fixture.compositeAdmissionFixture,
		fixture.compiled.Children[0],
		"workspace-transfer-result",
	)
	publishWorkspaceTransferRevocation(t, fixture)
	rootLease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"owner-workspace-transfer-root",
	)
	run, err := fixture.store.LoadRunForLoop(
		context.Background(),
		rootLease,
	)
	if err != nil {
		t.Fatalf("Load root transfer RESULT: %v", err)
	}
	if len(run.CompositeChildren) != 2 ||
		run.CompositeChildren[0].State != CompositeChildSucceededV1 ||
		run.CompositeChildren[0].WorkspaceTransfer == nil ||
		run.CompositeChildren[1].WorkspaceTransfer != nil {
		t.Fatalf("root transfer RESULT projection=%+v", run.CompositeChildren)
	}
	record := run.CompositeChildren[0].WorkspaceTransfer
	if record.Envelope.Direction !=
		corecontract.WorkspaceTransferDirectionResultV1 ||
		record.Payload.Digest != run.CompositeChildren[0].ResultRef ||
		record.RootManifest.ManifestDigest != run.Manifest.ManifestDigest ||
		record.ChildManifest.ManifestDigest !=
			fixture.compiled.Children[0].RunManifest.ManifestDigest ||
		record.Envelope.PayloadRef != record.Payload.Digest {
		t.Fatalf("RESULT transfer record differs: %+v", record)
	}
	execClosedFileTamperV1(t, fixture.store,
		[]string{"content_records_reject_delete"},
		`DELETE FROM content_records WHERE content_digest=?`,
		record.EnvelopeRef,
	)
	if _, err := fixture.store.LoadRunForLoop(
		context.Background(),
		rootLease,
	); !errors.Is(err, ErrLoopIntegrity) {
		t.Fatalf("missing atomic RESULT envelope error=%v", err)
	}
}

func TestLoadRunForLoopWorkspaceTransferRepairCarriesExactLineage(
	t *testing.T,
) {
	fixture := newCommittedWorkspaceTransferStoreFixture(t)
	putCompositeModelPrice(t, fixture.store)
	for index, child := range fixture.compiled.Children {
		finishDecisionSpecialist(
			t,
			fixture.compositeAdmissionFixture,
			child,
			"workspace-transfer-initial-"+string(rune('0'+index)),
		)
	}
	affected := []string{
		fixture.compiled.Children[0].Assignment.SlotID,
	}
	finishDecisionReviewer(
		t,
		fixture.compositeAdmissionFixture,
		fixture.compiled.Reviewer.RunManifest.RunID,
		corecontract.CollaborationReviewDecisionRepairRequiredV1,
		affected,
		"workspace-transfer-reviewer-zero",
	)
	rootLease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"owner-workspace-transfer-repair-root",
	)
	transition, err := fixture.store.ApplyCompositeDecision(
		context.Background(),
		ApplyCompositeDecisionInput{Lease: rootLease},
	)
	if err != nil || !transition.Applied || len(transition.ActivatedRunIDs) != 1 ||
		transition.ActivatedRunIDs[0] !=
			fixture.compiled.Decision.RepairChildren[0].RunManifest.RunID {
		t.Fatalf("activate cross-Workspace repair=%+v error=%v", transition, err)
	}
	repairLease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		fixture.compiled.Decision.RepairChildren[0].RunManifest.RunID,
		"owner-workspace-transfer-repair-child",
	)
	repair, err := fixture.store.LoadRunForLoop(
		context.Background(),
		repairLease,
	)
	if err != nil {
		t.Fatalf("Load cross-Workspace repair Child: %v", err)
	}
	material := repair.WorkspaceTransfer
	if material == nil ||
		material.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
		material.PreviousContributionSet == nil ||
		material.PreviousContributionSetDigest == "" ||
		material.RepairVerdict == nil ||
		material.RepairVerdict.Decision !=
			corecontract.CollaborationReviewDecisionRepairRequiredV1 ||
		material.RepairVerdictRef == "" || material.RepairBasis == nil ||
		len(material.RepairBasisCanonical) == 0 ||
		material.RepairBasis.PreviousSetDigest !=
			material.PreviousContributionSetDigest ||
		material.RepairBasis.VerdictRef != material.RepairVerdictRef {
		t.Fatalf("repair transfer lineage differs: %+v", material)
	}
	if _, found := repair.FindContent(repair.Manifest.TaskInputRef); found {
		t.Fatal("repair cross-Workspace TASK_INPUT leaked into generic Contents")
	}
}
