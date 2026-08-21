package currentstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type compositeAdmissionFixture struct {
	store            *Store
	basis            controlcontract.PublishedBasis
	controlCanonical []byte
	catalogCanonical []byte
	parentIntent     corecontract.AdmissionIntentV1
	compiled         assemblycompiler.CompositeCompileOutput
	input            CommitCompositeRunFamilyInput
	task             ContentInput
	staticContext    ContentInput
}

func TestCommitCompositeRunFamilyPublishesProjectionAndIsIdempotent(
	t *testing.T,
) {
	fixture := newCompositeAdmissionFixture(t)
	before := admissionCommitCounts(t, fixture.store)

	created, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("CommitCompositeRunFamily: %v", err)
	}
	assertCompositeAdmissionResult(t, fixture, created, true)
	assertStoredCompositeFamily(t, fixture, created)

	familySize := 1 + len(fixture.input.Children)
	afterCreate := admissionCommitCounts(t, fixture.store)
	for index := 0; index < 5; index++ {
		if afterCreate[index] != before[index]+familySize {
			t.Fatalf(
				"family closure count[%d]=%d want %d (before=%v after=%v)",
				index,
				afterCreate[index],
				before[index]+familySize,
				before,
				afterCreate,
			)
		}
	}
	if afterCreate[5] <= before[5] {
		t.Fatalf(
			"family Admission did not publish content: before=%v after=%v",
			before,
			afterCreate,
		)
	}

	// A same-input retry must resolve the complete original family before it
	// consults a now-advanced current pointer.
	fixture.basis = advancePublishedBasisForTestV1(
		t, fixture.store, fixture.basis, fixture.controlCanonical, fixture.catalogCanonical,
	)
	retried, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	)
	if err != nil {
		t.Fatalf("idempotent CommitCompositeRunFamily: %v", err)
	}
	assertCompositeAdmissionResult(t, fixture, retried, false)
	if afterRetry := admissionCommitCounts(t, fixture.store); afterRetry != afterCreate {
		t.Fatalf(
			"idempotent family retry changed rows: before=%v after=%v",
			afterCreate,
			afterRetry,
		)
	}
}

func TestCommitCompositeRunFamilyRollsBackLateChildFailure(t *testing.T) {
	fixture := newCompositeAdmissionFixture(t)
	conflictingRunID := fixture.compiled.Children[len(fixture.compiled.Children)-1].RunManifest.RunID
	seedCompositeRunIDConflict(t, fixture, conflictingRunID)
	before := admissionCommitCounts(t, fixture.store)

	_, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	)
	if !errors.Is(err, ErrAdmissionConflict) {
		t.Fatalf("late Child conflict error=%v want ErrAdmissionConflict", err)
	}
	if after := admissionCommitCounts(t, fixture.store); after != before {
		t.Fatalf(
			"late Child failure left partial family rows: before=%v after=%v",
			before,
			after,
		)
	}

	parentManifest := restoreCompositeManifest(t, fixture.input.Parent)
	assertAdmissionKeyAbsent(
		t,
		fixture.store,
		parentManifest.TenantID,
		parentManifest.AdmissionKey,
		parentManifest.AdmissionIntentDigest,
	)
	firstChild := restoreCompositeManifest(t, fixture.input.Children[0])
	assertAdmissionKeyAbsent(
		t,
		fixture.store,
		firstChild.TenantID,
		firstChild.AdmissionKey,
		firstChild.AdmissionIntentDigest,
	)
}

func TestCommitCompositeRunFamilyRollsBackEveryAuthoritativeWritePoint(t *testing.T) {
	for _, table := range []string{
		"content_records",
		"runs",
		"member_execution_snapshots",
		"run_manifests",
		"loop_frames",
		"run_events",
	} {
		t.Run(table, func(t *testing.T) {
			fixture := newCompositeAdmissionFixture(t)
			before := admissionCommitCounts(t, fixture.store)
			triggerName := "fail_composite_admission_" + table
			statement := fmt.Sprintf(`
				CREATE TRIGGER %s
				BEFORE INSERT ON %s
				BEGIN
					SELECT RAISE(ABORT, 'forced composite Admission failure');
				END
			`, triggerName, table)
			if _, err := fixture.store.db.Exec(statement); err != nil {
				t.Fatalf("create %s trigger: %v", table, err)
			}

			if _, err := fixture.store.CommitCompositeRunFamily(
				context.Background(),
				fixture.input,
			); err == nil {
				t.Fatalf("%s failure injection unexpectedly committed", table)
			}
			if after := admissionCommitCounts(t, fixture.store); after != before {
				t.Fatalf(
					"%s failure escaped family rollback: before=%v after=%v",
					table,
					before,
					after,
				)
			}
			parent := restoreCompositeManifest(t, fixture.input.Parent)
			assertAdmissionKeyAbsent(
				t,
				fixture.store,
				parent.TenantID,
				parent.AdmissionKey,
				parent.AdmissionIntentDigest,
			)
		})
	}
}

func TestCommitCompositeRunFamilyRejectsDefinitionWeightAndParentDigestDrift(
	t *testing.T,
) {
	t.Run("Control focus definition", func(t *testing.T) {
		fixture := newCompositeAdmissionFixture(t)
		input := rewriteCompositePlanForStoreTest(
			t,
			fixture.input,
			func(children []corecontract.CompositeChildRunRefV1) {
				children[0].Assignment.FocusID = "focus-control-drift"
			},
		)
		assertCompositeAdmissionRejectedWithoutWrites(
			t,
			fixture.store,
			input,
			ErrAdmissionConflict,
		)
	})

	t.Run("Control weight definition", func(t *testing.T) {
		fixture := newCompositeAdmissionFixture(t)
		input := rewriteCompositePlanForStoreTest(
			t,
			fixture.input,
			func(children []corecontract.CompositeChildRunRefV1) {
				children[0].Assignment.WeightBasisPoints += 500
				children[1].Assignment.WeightBasisPoints -= 500
			},
		)
		assertCompositeAdmissionRejectedWithoutWrites(
			t,
			fixture.store,
			input,
			ErrAdmissionConflict,
		)
	})

	t.Run("Child parent Manifest digest", func(t *testing.T) {
		fixture := newCompositeAdmissionFixture(t)
		input := cloneCompositeFamilyInput(fixture.input)
		child := restoreCompositeManifest(t, input.Children[0])
		parent := restoreCompositeManifest(t, input.Parent)
		wrongDigest := strings.Repeat("f", 64)
		if wrongDigest == parent.ManifestDigest {
			wrongDigest = strings.Repeat("e", 64)
		}
		child.Composite.ParentManifestDigest = wrongDigest
		_, childCanonical, err := corecontract.NewRunManifest(child)
		if err != nil {
			t.Fatalf("freeze Child with mismatched Parent digest: %v", err)
		}
		input.Children[0].RunManifestCanonical = childCanonical
		assertCompositeAdmissionRejectedWithoutWrites(
			t,
			fixture.store,
			input,
			ErrInvalidAdmission,
		)
	})
}

func newCompositeAdmissionFixture(t *testing.T) *compositeAdmissionFixture {
	t.Helper()
	base := newAdmissionCommitFixture(t)
	control, err := controlcontract.RestoreControlSnapshot(
		base.controlCanonical,
		base.basis.Control,
	)
	if err != nil {
		t.Fatalf("restore base Control: %v", err)
	}
	if len(control.Profiles) != 1 {
		t.Fatalf("base profile count=%d want 1", len(control.Profiles))
	}

	analysisAgent := corecontract.AgentRef{
		ID: "agent-analysis", Version: "v1", Digest: strings.Repeat("d", 64),
	}
	reviewAgent := corecontract.AgentRef{
		ID: "agent-review", Version: "v1", Digest: strings.Repeat("e", 64),
	}
	// The shared Admission fixture carries a declarative Context Binding whose
	// ConfigRef intentionally points at a model config because those tests never
	// execute a request. Composite runtime tests need a real executable minimal
	// family, so keep this fixture model-only instead of weakening compiler
	// closure validation for malformed optional-module data.
	coordinatorProfile := cloneCompositeTestProfile(control.Profiles[0])
	coordinatorProfile.Bindings = compositeModelOnlyBindings(
		coordinatorProfile.Bindings,
	)
	control.Profiles[0] = coordinatorProfile
	analysisProfile := cloneCompositeTestProfile(coordinatorProfile)
	analysisProfile.Profile = corecontract.ProfileRef{
		ID: "profile-analysis", Version: "v1", Digest: strings.Repeat("f", 64),
	}
	reviewProfile := cloneCompositeTestProfile(coordinatorProfile)
	reviewProfile.Profile = corecontract.ProfileRef{
		ID: "profile-review", Version: "v1", Digest: strings.Repeat("0", 64),
	}
	control.SnapshotID = "control-composite-admission"
	control.Revision++
	control.Agents = append(control.Agents, analysisAgent, reviewAgent)
	control.Profiles = append(control.Profiles, analysisProfile, reviewProfile)
	control.CompositeAgents = []controlcontract.CompositeAgentDefinitionV1{{
		SchemaVersion:        controlcontract.CompositeAgentSchemaVersionV1,
		AgentID:              base.intent.AgentID,
		CoordinatorProfileID: base.intent.ProfileID,
		Members: []controlcontract.CompositeAgentMemberV1{
			{
				SlotID:            "slot-analysis",
				AgentID:           analysisAgent.ID,
				ProfileID:         analysisProfile.Profile.ID,
				FocusID:           "analysis",
				WeightBasisPoints: 6000,
			},
			{
				SlotID:            "slot-review",
				AgentID:           reviewAgent.ID,
				ProfileID:         reviewProfile.Profile.ID,
				FocusID:           "review",
				WeightBasisPoints: 4000,
			},
		},
	}}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze composite Control: %v", err)
	}

	catalog, err := controlcontract.RestoreCatalogGeneration(
		base.catalogCanonical,
		base.basis.Catalog,
	)
	if err != nil {
		t.Fatalf("restore base Catalog: %v", err)
	}
	catalog.GenerationID = "catalog-composite-admission"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze composite Catalog: %v", err)
	}
	basis, err := base.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: base.basis.PointerRevision,
			NewPointerRevision:      base.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("publish composite Control/Catalog: %v", err)
	}

	parentIntentInput := base.intent
	parentIntentInput.CancellationScope = corecontract.CancellationScopeFamilyV1
	parentIntent, parentIntentCanonical, parentIntentDigest, err :=
		corecontract.NewAdmissionIntentV1(parentIntentInput)
	if err != nil {
		t.Fatalf("freeze composite Parent intent: %v", err)
	}
	compiled, err := (assemblycompiler.Compiler{}).CompileCompositeFamily(
		context.Background(),
		assemblycompiler.CompositeCompileInput{
			Parent: assemblycompiler.CompileInput{
				IntentCanonical:  parentIntentCanonical,
				IntentDigest:     parentIntentDigest,
				RunID:            "run-composite-parent",
				MemberID:         "member-composite-parent",
				RecoveryRootRef:  "recovery/run-composite-parent",
				PublishedBasis:   basis,
				ControlCanonical: controlCanonical,
				CatalogCanonical: catalogCanonical,
			},
		},
	)
	if err != nil {
		t.Fatalf("CompileCompositeFamily: %v", err)
	}

	fixture := &compositeAdmissionFixture{
		store:            base.store,
		basis:            basis,
		controlCanonical: controlCanonical,
		catalogCanonical: catalogCanonical,
		parentIntent:     parentIntent,
		compiled:         compiled,
		task:             base.task,
		staticContext:    base.staticContext,
	}
	fixture.input.Parent = compositeCommitRunInput(
		fixture,
		compiled.Parent,
		parentIntentCanonical,
		parentIntentDigest,
	)
	fixture.input.Children = make(
		[]CommitRunAdmissionInput,
		len(compiled.Children),
	)
	for index, child := range compiled.Children {
		fixture.input.Children[index] = compositeCommitRunInput(
			fixture,
			child.CompileOutput,
			child.IntentCanonical,
			child.IntentDigest,
		)
	}
	return fixture
}

func compositeModelOnlyBindings(
	bindings []controlcontract.BindingSpec,
) []controlcontract.BindingSpec {
	selected := make([]controlcontract.BindingSpec, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Port.Name == moduleapi.PortNameModelGenerate &&
			binding.Port.ExactVersion == moduleapi.PortVersionV1 {
			selected = append(selected, binding)
		}
	}
	return selected
}

func cloneCompositeTestProfile(
	input controlcontract.ProfileDefinition,
) controlcontract.ProfileDefinition {
	cloned := input
	if input.ModelProfile != nil {
		modelProfile := *input.ModelProfile
		cloned.ModelProfile = &modelProfile
	}
	cloned.Bindings = append([]controlcontract.BindingSpec(nil), input.Bindings...)
	for index := range cloned.Bindings {
		cloned.Bindings[index].StaticContextRefs = append(
			[]string(nil),
			input.Bindings[index].StaticContextRefs...,
		)
	}
	return cloned
}

func compositeCommitRunInput(
	fixture *compositeAdmissionFixture,
	compiled assemblycompiler.CompileOutput,
	intentCanonical []byte,
	intentDigest string,
) CommitRunAdmissionInput {
	return CommitRunAdmissionInput{
		PublishedBasis:          fixture.basis,
		IntentCanonical:         intentCanonical,
		IntentDigest:            intentDigest,
		MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
		RunManifestCanonical:    compiled.RunManifestCanonical,
		Contents: compositeNewContents(
			fixture.task,
			fixture.staticContext,
			compiled.MemberSnapshot,
		),
	}
}

func compositeNewContents(
	task ContentInput,
	staticContext ContentInput,
	member corecontract.MemberExecutionSnapshot,
) []ContentInput {
	contents := []ContentInput{task}
	for _, plan := range member.PortPlans {
		for _, binding := range plan.Bindings {
			for _, digest := range binding.StaticContextRefs {
				if digest == staticContext.Digest {
					return append(contents, staticContext)
				}
			}
		}
	}
	return contents
}

func assertCompositeAdmissionResult(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	result CompositeRunFamilyAdmissionResult,
	created bool,
) {
	t.Helper()
	parentManifest := fixture.compiled.Parent.RunManifest
	if result.Created != created || result.Parent.Created != created ||
		result.Parent.RunID != parentManifest.RunID ||
		result.Parent.AdmissionIntentDigest != fixture.input.Parent.IntentDigest ||
		result.Parent.ManifestDigest != parentManifest.ManifestDigest ||
		result.Parent.MemberSnapshotDigest !=
			fixture.compiled.Parent.MemberSnapshot.MemberSnapshotDigest {
		t.Fatalf("Parent result=%+v family Created=%v", result.Parent, result.Created)
	}
	if len(result.Children) != len(fixture.compiled.Children) {
		t.Fatalf(
			"Child result count=%d want %d",
			len(result.Children),
			len(fixture.compiled.Children),
		)
	}
	for index, child := range fixture.compiled.Children {
		got := result.Children[index]
		if got.Created != created || got.RunID != child.RunManifest.RunID ||
			got.AdmissionIntentDigest != child.IntentDigest ||
			got.ManifestDigest != child.RunManifest.ManifestDigest ||
			got.MemberSnapshotDigest != child.MemberSnapshot.MemberSnapshotDigest {
			t.Fatalf("Child %d result=%+v", index, got)
		}
	}
}

func assertStoredCompositeFamily(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	result CompositeRunFamilyAdmissionResult,
) {
	t.Helper()
	parent := restoreCompositeManifest(t, fixture.input.Parent)
	if parent.Composite == nil || parent.Composite.Plan == nil {
		t.Fatal("compiled Parent has no composite plan")
	}
	var (
		parentRunID           sql.NullString
		parentManifestDigest  sql.NullString
		parentSlotID          sql.NullString
		parentState           string
		parentStep            string
		parentContinuation    []byte
		parentPendingModel    sql.NullString
		parentPendingDispatch sql.NullString
		parentWaitingReason   sql.NullString
	)
	if err := fixture.store.db.QueryRow(`
		SELECT
			r.parent_run_id, r.parent_manifest_digest, r.parent_slot_id,
			r.state, f.step, f.continuation,
			f.pending_attempt_id, f.pending_dispatch_attempt_id,
			f.waiting_reason
		FROM runs AS r
		JOIN loop_frames AS f ON f.run_id=r.run_id
		WHERE r.run_id=?
	`, parent.RunID).Scan(
		&parentRunID,
		&parentManifestDigest,
		&parentSlotID,
		&parentState,
		&parentStep,
		&parentContinuation,
		&parentPendingModel,
		&parentPendingDispatch,
		&parentWaitingReason,
	); err != nil {
		t.Fatalf("load stored Parent projection: %v", err)
	}
	parentRestored, err := corecontract.RestoreLoopContinuationV1(
		parentContinuation,
	)
	if err != nil {
		t.Fatalf("restore Parent continuation: %v", err)
	}
	if parentRunID.Valid || parentManifestDigest.Valid || parentSlotID.Valid ||
		parentState != corecontract.InitialRunState ||
		parentStep != corecontract.WaitingChildrenLoopStep ||
		parentRestored.State != corecontract.WaitingChildrenLoopStep ||
		parentRestored.AttemptKind != "" ||
		parentRestored.LogicalStepID != "" || parentRestored.AttemptID != "" ||
		parentPendingModel.Valid || parentPendingDispatch.Valid ||
		!parentWaitingReason.Valid ||
		parentWaitingReason.String != "COMPOSITE_CHILDREN_PENDING" {
		t.Fatalf(
			"stored Parent projection/state drifted: family=(%v,%v,%v) state=%q step=%q continuation=%+v pending=(%v,%v) waiting=%v",
			parentRunID,
			parentManifestDigest,
			parentSlotID,
			parentState,
			parentStep,
			parentRestored,
			parentPendingModel,
			parentPendingDispatch,
			parentWaitingReason,
		)
	}

	for index, childInput := range fixture.input.Children {
		child := restoreCompositeManifest(t, childInput)
		planned := parent.Composite.Plan.Children[index]
		if child.Composite == nil || child.Composite.Assignment == nil ||
			child.RunID != planned.RunID ||
			child.AdmissionKey != planned.AdmissionKey ||
			child.Composite.ParentManifestDigest != parent.ManifestDigest ||
			child.Composite.ParentSlotID != planned.SlotID ||
			*child.Composite.Assignment != planned.Assignment ||
			result.Children[index].RunID != planned.RunID {
			t.Fatalf(
				"Child %d Manifest does not match Parent plan: child=%+v plan=%+v",
				index,
				child.Composite,
				planned,
			)
		}

		var (
			storedParentRunID  string
			storedParentDigest string
			storedParentSlot   string
			state              string
			step               string
			continuation       []byte
			pendingModel       sql.NullString
			pendingDispatch    sql.NullString
			waitingReason      sql.NullString
			memberDigest       string
		)
		if err := fixture.store.db.QueryRow(`
			SELECT
				r.parent_run_id, r.parent_manifest_digest, r.parent_slot_id,
				r.state, f.step, f.continuation,
				f.pending_attempt_id, f.pending_dispatch_attempt_id,
				f.waiting_reason, m.digest
			FROM runs AS r
			JOIN loop_frames AS f ON f.run_id=r.run_id
			JOIN member_execution_snapshots AS m ON m.run_id=r.run_id
			WHERE r.run_id=?
		`, child.RunID).Scan(
			&storedParentRunID,
			&storedParentDigest,
			&storedParentSlot,
			&state,
			&step,
			&continuation,
			&pendingModel,
			&pendingDispatch,
			&waitingReason,
			&memberDigest,
		); err != nil {
			t.Fatalf("load stored Child %d projection: %v", index, err)
		}
		restored, err := corecontract.RestoreLoopContinuationV1(continuation)
		if err != nil {
			t.Fatalf("restore Child %d continuation: %v", index, err)
		}
		if storedParentRunID != parent.RunID ||
			storedParentDigest != parent.ManifestDigest ||
			storedParentSlot != planned.SlotID ||
			state != corecontract.InitialRunState ||
			step != corecontract.InitialLoopStep ||
			restored.State != corecontract.InitialLoopStep ||
			restored.AttemptKind != "" || restored.LogicalStepID != "" ||
			restored.AttemptID != "" || pendingModel.Valid ||
			pendingDispatch.Valid || waitingReason.Valid ||
			memberDigest != planned.MemberSnapshotDigest {
			t.Fatalf(
				"stored Child %d projection/state drifted: parent=(%q,%q,%q) state=%q step=%q continuation=%+v pending=(%v,%v) waiting=%v member=%q",
				index,
				storedParentRunID,
				storedParentDigest,
				storedParentSlot,
				state,
				step,
				restored,
				pendingModel,
				pendingDispatch,
				waitingReason,
				memberDigest,
			)
		}
	}
}

func seedCompositeRunIDConflict(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	runID string,
) {
	t.Helper()
	intentInput := fixture.parentIntent
	intentInput.AdmissionKey = "admission-seeded-run-id-conflict"
	intentInput.CancellationScope = corecontract.CancellationScopeRunV1
	_, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(intentInput)
	if err != nil {
		t.Fatalf("freeze conflict seed intent: %v", err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		context.Background(),
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            runID,
			MemberID:         "member-seeded-run-id-conflict",
			RecoveryRootRef:  "recovery/seeded-run-id-conflict",
			PublishedBasis:   fixture.basis,
			ControlCanonical: fixture.controlCanonical,
			CatalogCanonical: fixture.catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("compile conflict seed Run: %v", err)
	}
	seed := compositeCommitRunInput(
		fixture,
		compiled,
		intentCanonical,
		intentDigest,
	)
	if _, err := fixture.store.CommitRunAdmission(
		context.Background(),
		seed,
	); err != nil {
		t.Fatalf("commit conflict seed Run: %v", err)
	}
}

func rewriteCompositePlanForStoreTest(
	t *testing.T,
	input CommitCompositeRunFamilyInput,
	mutate func([]corecontract.CompositeChildRunRefV1),
) CommitCompositeRunFamilyInput {
	t.Helper()
	input = cloneCompositeFamilyInput(input)
	parent := restoreCompositeManifest(t, input.Parent)
	if parent.Composite == nil || parent.Composite.Plan == nil {
		t.Fatal("Parent has no composite plan")
	}
	mutate(parent.Composite.Plan.Children)
	frozenParent, parentCanonical, err := corecontract.NewRunManifest(parent)
	if err != nil {
		t.Fatalf("freeze rewritten Parent plan: %v", err)
	}
	input.Parent.RunManifestCanonical = parentCanonical

	for index := range input.Children {
		child := restoreCompositeManifest(t, input.Children[index])
		if child.Composite == nil {
			t.Fatalf("Child %d has no composite node", index)
		}
		planned, found := compositePlannedChildBySlot(
			frozenParent.Composite.Plan.Children,
			child.Composite.ParentSlotID,
		)
		if !found {
			t.Fatalf("Child %d slot is absent from rewritten Parent", index)
		}
		assignment := planned.Assignment
		child.Composite.ParentManifestDigest = frozenParent.ManifestDigest
		child.Composite.Assignment = &assignment
		_, childCanonical, err := corecontract.NewRunManifest(child)
		if err != nil {
			t.Fatalf("freeze rewritten Child %d: %v", index, err)
		}
		input.Children[index].RunManifestCanonical = childCanonical
	}
	return input
}

func compositePlannedChildBySlot(
	children []corecontract.CompositeChildRunRefV1,
	slotID string,
) (corecontract.CompositeChildRunRefV1, bool) {
	for _, child := range children {
		if child.SlotID == slotID {
			return child, true
		}
	}
	return corecontract.CompositeChildRunRefV1{}, false
}

func cloneCompositeFamilyInput(
	input CommitCompositeRunFamilyInput,
) CommitCompositeRunFamilyInput {
	cloned := input
	cloned.Parent.RunManifestCanonical = append(
		[]byte(nil),
		input.Parent.RunManifestCanonical...,
	)
	cloned.Children = append([]CommitRunAdmissionInput(nil), input.Children...)
	for index := range cloned.Children {
		cloned.Children[index].RunManifestCanonical = append(
			[]byte(nil),
			input.Children[index].RunManifestCanonical...,
		)
	}
	return cloned
}

func restoreCompositeManifest(
	t *testing.T,
	input CommitRunAdmissionInput,
) corecontract.RunManifest {
	t.Helper()
	manifest, err := corecontract.RestoreRunManifest(input.RunManifestCanonical)
	if err != nil {
		t.Fatalf("restore composite Manifest: %v", err)
	}
	return manifest
}

func assertCompositeAdmissionRejectedWithoutWrites(
	t *testing.T,
	store *Store,
	input CommitCompositeRunFamilyInput,
	want error,
) {
	t.Helper()
	before := admissionCommitCounts(t, store)
	_, err := store.CommitCompositeRunFamily(context.Background(), input)
	if !errors.Is(err, want) {
		t.Fatalf("CommitCompositeRunFamily error=%v want %v", err, want)
	}
	if after := admissionCommitCounts(t, store); after != before {
		t.Fatalf(
			"rejected composite family changed rows: before=%v after=%v",
			before,
			after,
		)
	}
}

func assertAdmissionKeyAbsent(
	t *testing.T,
	store *Store,
	tenantID string,
	admissionKey string,
	intentDigest string,
) {
	t.Helper()
	_, found, err := store.ResolveAdmission(
		context.Background(),
		tenantID,
		admissionKey,
		intentDigest,
	)
	if err != nil || found {
		t.Fatalf(
			"rolled-back Admission key %q found=%v error=%v",
			admissionKey,
			found,
			err,
		)
	}
}
