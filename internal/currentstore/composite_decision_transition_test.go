package currentstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompositeRepairActivatedPostTransitionRejectsDrift(t *testing.T) {
	tests := []struct {
		name          string
		runState      string
		eventSequence int64
	}{
		{
			name:          "revision greater than one still validates Run Frame state",
			runState:      "CORRUPTED",
			eventSequence: 1,
		},
		{
			name:          "activation transition remains sequence one",
			runState:      corecontract.InitialRunState,
			eventSequence: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			for _, statement := range []string{
				`CREATE TABLE runs(
					run_id TEXT PRIMARY KEY,
					state TEXT NOT NULL,
					disposition TEXT,
					revision INTEGER NOT NULL
				)`,
				`CREATE TABLE loop_frames(
					run_id TEXT PRIMARY KEY,
					frame_revision INTEGER NOT NULL,
					step TEXT NOT NULL,
					continuation BLOB NOT NULL,
					pending_attempt_id TEXT,
					pending_dispatch_attempt_id TEXT,
					waiting_reason TEXT,
					last_authoritative_event INTEGER NOT NULL
				)`,
				`CREATE TABLE run_events(
					run_id TEXT NOT NULL,
					event_sequence INTEGER NOT NULL,
					event_kind TEXT NOT NULL
				)`,
			} {
				if _, err := database.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			_, continuation, err := corecontract.NewInitialLoopState(
				"repair-run",
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`
				INSERT INTO runs(run_id, state, disposition, revision)
				VALUES('repair-run', ?, NULL, 2)
			`, test.runState); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`
				INSERT INTO loop_frames(
					run_id, frame_revision, step, continuation,
					pending_attempt_id, pending_dispatch_attempt_id,
					waiting_reason, last_authoritative_event
				) VALUES('repair-run', 2, ?, ?, NULL, NULL, NULL, 1)
			`, corecontract.InitialLoopStep, continuation); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`
				INSERT INTO run_events(run_id, event_sequence, event_kind)
				VALUES('repair-run', ?, ?)
			`,
				test.eventSequence,
				corecontract.CompositeRepairActivatedEventKind,
			); err != nil {
				t.Fatal(err)
			}
			connection, err := database.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			err = verifyCompositeRepairPostTransitionProjection(
				context.Background(),
				connection,
				compositeRepairTransition{
					runID: "repair-run",
					role:  corecontract.CompositeRunRoleChildV1,
					kind:  compositeRepairTransitionActivate,
				},
			)
			if !errors.Is(err, ErrCompositeDecisionTransitionIntegrity) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCompositeDecisionFamilyAdmissionIsAtomicIdempotentAndDormant(
	t *testing.T,
) {
	t.Run("complete family and exact retry", func(t *testing.T) {
		fixture := newCompositeDecisionStoreFixture(t, 2)
		before := admissionCommitCounts(t, fixture.store)
		created, err := fixture.store.CommitCompositeRunFamily(
			context.Background(),
			fixture.input,
		)
		if err != nil || !created.Created || created.Reviewer == nil ||
			created.RepairReviewer == nil || len(created.Children) != 2 ||
			len(created.RepairChildren) != 2 {
			t.Fatalf("created=%+v error=%v", created, err)
		}
		familySize := 2*len(fixture.compiled.Children) + 3
		var familyRows int
		if err := fixture.store.db.QueryRow(`
			SELECT COUNT(*) FROM runs WHERE run_id=? OR parent_run_id=?
		`,
			fixture.compiled.Parent.RunManifest.RunID,
			fixture.compiled.Parent.RunManifest.RunID,
		).Scan(&familyRows); err != nil || familyRows != familySize {
			t.Fatalf("family rows=%d want=%d error=%v", familyRows, familySize, err)
		}
		assertDecisionRepairRunsDormant(t, fixture)

		scan, err := fixture.store.ScanStartupRecovery(context.Background())
		if err != nil || len(scan) != familySize {
			t.Fatalf("startup scan=%+v error=%v", scan, err)
		}
		dormant := 0
		for _, run := range scan {
			if run.FrameStep == corecontract.WaitingRepairActivationLoopStep {
				dormant++
			}
		}
		if dormant != len(fixture.compiled.Decision.RepairChildren)+1 {
			t.Fatalf("dormant recovery rows=%d", dormant)
		}

		afterCreate := admissionCommitCounts(t, fixture.store)
		retry, err := fixture.store.CommitCompositeRunFamily(
			context.Background(),
			fixture.input,
		)
		if err != nil || retry.Created {
			t.Fatalf("retry=%+v error=%v", retry, err)
		}
		if afterRetry := admissionCommitCounts(t, fixture.store); afterRetry != afterCreate {
			t.Fatalf("retry changed rows: before=%v after=%v", afterCreate, afterRetry)
		}
		for index := 0; index < 5; index++ {
			if afterCreate[index] != before[index]+familySize {
				t.Fatalf(
					"closure count[%d]=%d want=%d",
					index,
					afterCreate[index],
					before[index]+familySize,
				)
			}
		}
	})

	t.Run("late repair Reviewer failure rolls back every member", func(t *testing.T) {
		fixture := newCompositeDecisionStoreFixture(t, 2)
		before := admissionCommitCounts(t, fixture.store)
		repairReviewerID := fixture.compiled.Decision.
			RepairReviewer.RunManifest.RunID
		statement := fmt.Sprintf(`
			CREATE TRIGGER fail_decision_repair_reviewer
			BEFORE INSERT ON run_events
			WHEN NEW.run_id='%s'
			BEGIN
				SELECT RAISE(ABORT, 'forced repair Reviewer failure');
			END
		`, repairReviewerID)
		if _, err := fixture.store.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.CommitCompositeRunFamily(
			context.Background(),
			fixture.input,
		); err == nil {
			t.Fatal("late repair Reviewer failure committed")
		}
		if after := admissionCommitCounts(t, fixture.store); after != before {
			t.Fatalf("partial family escaped rollback: before=%v after=%v", before, after)
		}
	})
}

func TestCompositeDecisionFamilyCancellationIncludesDormantRepairRuns(
	t *testing.T,
) {
	fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
	root := fixture.compiled.Parent.RunManifest
	_, canonical := newFamilyCancellationRequest(
		t,
		root,
		corecontract.CancellationReasonUserRequestV1,
	)
	result, err := fixture.store.RequestRunCancellation(
		context.Background(),
		RequestRunCancellationInput{Canonical: canonical},
	)
	if err != nil || !result.Created || result.Ref == "" {
		t.Fatalf("cancellation=%+v error=%v", result, err)
	}
	rows, err := fixture.store.db.Query(`
		SELECT cancel_request_ref
		FROM runs
		WHERE run_id=? OR parent_run_id=?
	`, root.RunID, root.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var ref sql.NullString
		if err := rows.Scan(&ref); err != nil || !ref.Valid ||
			ref.String != result.Ref {
			t.Fatalf("family cancellation ref=%v error=%v", ref, err)
		}
		count++
	}
	want := 2*len(fixture.compiled.Children) + 3
	if err := rows.Err(); err != nil || count != want {
		t.Fatalf("canceled family rows=%d want=%d error=%v", count, want, err)
	}
}

func assertDecisionRepairRunsDormant(
	t *testing.T,
	fixture *compositeAdmissionFixture,
) {
	t.Helper()
	runIDs := make([]string, 0, len(fixture.compiled.Decision.RepairChildren)+1)
	for _, child := range fixture.compiled.Decision.RepairChildren {
		runIDs = append(runIDs, child.RunManifest.RunID)
	}
	runIDs = append(
		runIDs,
		fixture.compiled.Decision.RepairReviewer.RunManifest.RunID,
	)
	for _, runID := range runIDs {
		var (
			state         string
			disposition   sql.NullString
			runRevision   int64
			frameRevision int64
			step          string
			waiting       sql.NullString
			events        int64
			models        int64
			dispatches    int64
			usage         int64
			history       int64
		)
		if err := fixture.store.db.QueryRow(`
			SELECT r.state, r.disposition, r.revision,
			       f.frame_revision, f.step, f.waiting_reason,
			       (SELECT COUNT(*) FROM run_events WHERE run_id=r.run_id),
			       (SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=r.run_id),
			       (SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=r.run_id),
			       (SELECT COUNT(*) FROM model_usage WHERE run_id=r.run_id),
			       (SELECT COUNT(*) FROM history_entries WHERE run_id=r.run_id)
			FROM runs AS r
			JOIN loop_frames AS f ON f.run_id=r.run_id
			WHERE r.run_id=?
		`, runID).Scan(
			&state,
			&disposition,
			&runRevision,
			&frameRevision,
			&step,
			&waiting,
			&events,
			&models,
			&dispatches,
			&usage,
			&history,
		); err != nil {
			t.Fatal(err)
		}
		if state != corecontract.InitialRunState || !disposition.Valid ||
			disposition.String != "WAITING_EXTERNAL" || runRevision != 0 ||
			frameRevision != 0 ||
			step != corecontract.WaitingRepairActivationLoopStep ||
			!waiting.Valid || waiting.String != compositeRepairDormantWaitingReason ||
			events != 1 || models != 0 || dispatches != 0 || usage != 0 ||
			history != 0 {
			t.Fatalf(
				"dormant repair %q drifted: state=%q disposition=%v revisions=(%d,%d) step=%q waiting=%v counts=(%d,%d,%d,%d,%d)",
				runID,
				state,
				disposition,
				runRevision,
				frameRevision,
				step,
				waiting,
				events,
				models,
				dispatches,
				usage,
				history,
			)
		}
	}
}

func newCompositeDecisionStoreFixture(
	t *testing.T,
	memberCount int,
) *compositeAdmissionFixture {
	return newCompositeDecisionStoreFixtureWithControlMutator(
		t,
		memberCount,
		nil,
	)
}

func newCompositeDecisionStoreFixtureWithControlMutator(
	t *testing.T,
	memberCount int,
	mutateControl func(*controlcontract.ControlSnapshot),
) *compositeAdmissionFixture {
	t.Helper()
	if memberCount != 2 && memberCount != corecontract.CompositeMaxChildrenV1 {
		t.Fatalf("unsupported decision fixture member count %d", memberCount)
	}
	fixture := newCompositeAdmissionFixture(t)
	enableCompositeReviewerFixture(t, fixture)
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.controlCanonical,
		fixture.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(control.CompositeAgents) != 1 ||
		control.CompositeAgents[0].Reviewer == nil {
		t.Fatal("decision fixture lacks its Reviewer")
	}
	if memberCount == corecontract.CompositeMaxChildrenV1 {
		template := cloneCompositeTestProfile(control.Profiles[1])
		members := make(
			[]controlcontract.CompositeAgentMemberV1,
			memberCount,
		)
		for index := 0; index < memberCount; index++ {
			agent := corecontract.AgentRef{
				ID:      fmt.Sprintf("agent-decision-%02d", index),
				Version: "v1",
				Digest:  decisionTestDigest("agent", index),
			}
			profile := cloneCompositeTestProfile(template)
			profile.Profile = corecontract.ProfileRef{
				ID:      fmt.Sprintf("profile-decision-%02d", index),
				Version: "v1",
				Digest:  decisionTestDigest("profile", index),
			}
			control.Agents = append(control.Agents, agent)
			control.Profiles = append(control.Profiles, profile)
			members[index] = controlcontract.CompositeAgentMemberV1{
				SlotID:    fmt.Sprintf("slot-decision-%02d", index),
				AgentID:   agent.ID,
				ProfileID: profile.Profile.ID,
				FocusID:   fmt.Sprintf("focus-decision-%02d", index),
				WeightBasisPoints: uint32(
					corecontract.CompositeWeightBasisPointsV1 / uint64(memberCount),
				),
			}
		}
		control.CompositeAgents[0].Members = members
	}
	control.SnapshotID = fmt.Sprintf(
		"control-composite-decision-%d",
		memberCount,
	)
	control.Revision++
	control.CompositeAgents[0].Decision =
		&controlcontract.CompositeDecisionDefinitionV1{
			SchemaVersion: controlcontract.CompositeDecisionSchemaVersionV1,
		}
	if mutateControl != nil {
		mutateControl(&control)
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze decision Control: %v", err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		fixture.catalogCanonical,
		fixture.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = fmt.Sprintf(
		"catalog-composite-decision-%d",
		memberCount,
	)
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze decision Catalog: %v", err)
	}
	basis, err := fixture.store.PublishControlCatalog(
		context.Background(),
		PublishControlCatalogInput{
			ExpectedPointerRevision: fixture.basis.PointerRevision,
			NewPointerRevision:      fixture.basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("publish decision Control/Catalog: %v", err)
	}
	fixture.basis = basis
	fixture.controlCanonical = controlCanonical
	fixture.catalogCanonical = catalogCanonical

	intentInput := fixture.parentIntent
	intentInput.AdmissionKey = fmt.Sprintf(
		"admission-composite-decision-%d",
		memberCount,
	)
	intentInput.Deadline = time.Now().UTC().Add(4 * time.Hour).
		Truncate(time.Microsecond)
	parentIntent, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(intentInput)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := (assemblycompiler.Compiler{}).CompileCompositeFamily(
		context.Background(),
		assemblycompiler.CompositeCompileInput{
			Parent: assemblycompiler.CompileInput{
				IntentCanonical: intentCanonical,
				IntentDigest:    intentDigest,
				RunID: fmt.Sprintf(
					"run-composite-decision-%d",
					memberCount,
				),
				MemberID: fmt.Sprintf(
					"member-composite-decision-%d",
					memberCount,
				),
				RecoveryRootRef: fmt.Sprintf(
					"recovery/composite-decision-%d",
					memberCount,
				),
				PublishedBasis:   fixture.basis,
				ControlCanonical: fixture.controlCanonical,
				CatalogCanonical: fixture.catalogCanonical,
			},
		},
	)
	if err != nil || compiled.Reviewer == nil || compiled.Decision == nil {
		t.Fatalf("CompileCompositeFamily decision=%+v error=%v", compiled, err)
	}
	fixture.parentIntent = parentIntent
	fixture.compiled = compiled
	fixture.input = CommitCompositeRunFamilyInput{
		Parent: compositeCommitRunInput(
			fixture,
			compiled.Parent,
			intentCanonical,
			intentDigest,
		),
		Children: make([]CommitRunAdmissionInput, len(compiled.Children)),
		RepairChildren: make(
			[]CommitRunAdmissionInput,
			len(compiled.Decision.RepairChildren),
		),
	}
	for index, child := range compiled.Children {
		fixture.input.Children[index] = compositeCommitRunInput(
			fixture,
			child.CompileOutput,
			child.IntentCanonical,
			child.IntentDigest,
		)
	}
	reviewer := compositeCommitRunInput(
		fixture,
		compiled.Reviewer.CompileOutput,
		compiled.Reviewer.IntentCanonical,
		compiled.Reviewer.IntentDigest,
	)
	fixture.input.Reviewer = &reviewer
	for index, child := range compiled.Decision.RepairChildren {
		fixture.input.RepairChildren[index] = compositeCommitRunInput(
			fixture,
			child.CompileOutput,
			child.IntentCanonical,
			child.IntentDigest,
		)
	}
	repairReviewer := compositeCommitRunInput(
		fixture,
		compiled.Decision.RepairReviewer.CompileOutput,
		compiled.Decision.RepairReviewer.IntentCanonical,
		compiled.Decision.RepairReviewer.IntentDigest,
	)
	fixture.input.RepairReviewer = &repairReviewer
	return fixture
}

func newCommittedCompositeDecisionStoreFixture(
	t *testing.T,
	memberCount int,
) *compositeAdmissionFixture {
	t.Helper()
	fixture := newCompositeDecisionStoreFixture(t, memberCount)
	if _, err := fixture.store.CommitCompositeRunFamily(
		context.Background(),
		fixture.input,
	); err != nil {
		t.Fatalf("CommitCompositeRunFamily decision: %v", err)
	}
	return fixture
}

func decisionTestDigest(kind string, index int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", kind, index)))
	return hex.EncodeToString(digest[:])
}

func TestCompositeDecisionSubsetAndAllRepairReviewerActivationIsDelayedAndIdempotent(
	t *testing.T,
) {
	for _, affectedCount := range []int{1, 2} {
		t.Run(fmt.Sprintf("affected-%d", affectedCount), func(t *testing.T) {
			fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
			for index, child := range fixture.compiled.Children {
				finishDecisionSpecialist(
					t,
					fixture,
					child,
					fmt.Sprintf("initial-%d", index),
				)
			}
			affected := make([]string, affectedCount)
			for index := range affected {
				affected[index] = fixture.compiled.Children[index].Assignment.SlotID
			}
			sort.Strings(affected)
			finishDecisionReviewer(
				t,
				fixture,
				fixture.compiled.Reviewer.RunManifest.RunID,
				corecontract.CollaborationReviewDecisionRepairRequiredV1,
				affected,
				"reviewer-zero",
			)
			rootLease := acquireCurrentDecisionLease(
				t,
				fixture.store,
				fixture.compiled.Parent.RunManifest.RunID,
				"decision-root",
			)
			first, err := fixture.store.ApplyCompositeDecision(
				context.Background(),
				ApplyCompositeDecisionInput{Lease: rootLease},
			)
			if err != nil || !first.Applied ||
				first.Decision !=
					corecontract.CollaborationReviewDecisionRepairRequiredV1 ||
				len(first.ActivatedRunIDs) != affectedCount ||
				len(first.SkippedRunIDs) != len(fixture.compiled.Children)-affectedCount {
				t.Fatalf("first transition=%+v error=%v", first, err)
			}
			assertDecisionRepairTransitionHeads(t, fixture, affected)

			// Reviewer1 must remain dormant until every selected repair has
			// reached a successful terminal result.
			early, err := fixture.store.ApplyCompositeDecision(
				context.Background(),
				ApplyCompositeDecisionInput{Lease: rootLease},
			)
			if err != nil || early.Applied {
				t.Fatalf("early second Apply=%+v error=%v", early, err)
			}
			assertDecisionRepairReviewerStep(
				t,
				fixture,
				corecontract.WaitingRepairActivationLoopStep,
				0,
			)

			for index := 0; index < affectedCount; index++ {
				finishDecisionSpecialist(
					t,
					fixture,
					fixture.compiled.Decision.RepairChildren[index],
					fmt.Sprintf("repair-%d", index),
				)
			}

			type applyOutcome struct {
				result ApplyCompositeDecisionResult
				err    error
			}
			start := make(chan struct{})
			outcomes := make(chan applyOutcome, 2)
			var wait sync.WaitGroup
			for index := 0; index < 2; index++ {
				wait.Add(1)
				go func() {
					defer wait.Done()
					<-start
					result, err := fixture.store.ApplyCompositeDecision(
						context.Background(),
						ApplyCompositeDecisionInput{Lease: rootLease},
					)
					outcomes <- applyOutcome{result: result, err: err}
				}()
			}
			close(start)
			wait.Wait()
			close(outcomes)
			applied := 0
			for outcome := range outcomes {
				if outcome.err != nil {
					t.Fatalf("concurrent second Apply: %v", outcome.err)
				}
				if outcome.result.Applied {
					applied++
				}
			}
			if applied != 1 {
				t.Fatalf("concurrent second Apply applied=%d want=1", applied)
			}
			assertDecisionRepairReviewerStep(
				t,
				fixture,
				corecontract.WaitingChildrenLoopStep,
				1,
			)

			finishDecisionReviewer(
				t,
				fixture,
				fixture.compiled.Decision.RepairReviewer.RunManifest.RunID,
				corecontract.CollaborationReviewDecisionApproveV1,
				[]string{},
				"reviewer-one",
			)
			frontier, err := fixture.store.GetCompositeDecisionFrontier(
				context.Background(),
				fixture.compiled.Parent.RunManifest.RunID,
			)
			if err != nil || frontier.Stage != CompositeFrontierRootTransitionV1 ||
				frontier.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
				len(frontier.RunnableRunIDs) != 1 ||
				frontier.RunnableRunIDs[0] != fixture.compiled.Parent.RunManifest.RunID {
				t.Fatalf("frontier=%+v error=%v", frontier, err)
			}
		})
	}
}

func TestCompositeDecisionStrictActivationEventTamperIsRejected(t *testing.T) {
	fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
	for index, child := range fixture.compiled.Children {
		finishDecisionSpecialist(
			t,
			fixture,
			child,
			fmt.Sprintf("tamper-initial-%d", index),
		)
	}
	affected := []string{fixture.compiled.Children[0].Assignment.SlotID}
	finishDecisionReviewer(
		t,
		fixture,
		fixture.compiled.Reviewer.RunManifest.RunID,
		corecontract.CollaborationReviewDecisionRepairRequiredV1,
		affected,
		"tamper-reviewer-zero",
	)
	rootLease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"tamper-root",
	)
	if _, err := fixture.store.ApplyCompositeDecision(
		context.Background(),
		ApplyCompositeDecisionInput{Lease: rootLease},
	); err != nil {
		t.Fatal(err)
	}
	activatedRunID := fixture.compiled.Decision.RepairChildren[0].RunManifest.RunID
	execClosedFileTamperV1(
		t,
		fixture.store,
		[]string{"run_events_reject_update"},
		`
		UPDATE run_events SET event_kind='CORRUPTED_REPAIR_ACTIVATION'
		WHERE run_id=? AND event_sequence=1
	`,
		activatedRunID,
	)
	if _, err := fixture.store.ApplyCompositeDecision(
		context.Background(),
		ApplyCompositeDecisionInput{Lease: rootLease},
	); !errors.Is(err, ErrCompositeDecisionTransitionIntegrity) {
		t.Fatalf("tampered activation error=%v", err)
	}
	var attempts, usage int64
	if err := fixture.store.db.QueryRow(`
		SELECT
		 (SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
		 (SELECT COUNT(*) FROM model_usage WHERE run_id=?)
	`, activatedRunID, activatedRunID).Scan(&attempts, &usage); err != nil ||
		attempts != 0 || usage != 0 {
		t.Fatalf("tamper path side effects attempts=%d usage=%d error=%v", attempts, usage, err)
	}
}

func TestCompositeDecisionDormantDriftRollsBackWholeTransition(t *testing.T) {
	fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
	for index, child := range fixture.compiled.Children {
		finishDecisionSpecialist(
			t,
			fixture,
			child,
			fmt.Sprintf("dormant-drift-initial-%d", index),
		)
	}
	affected := []string{fixture.compiled.Children[0].Assignment.SlotID}
	finishDecisionReviewer(
		t,
		fixture,
		fixture.compiled.Reviewer.RunManifest.RunID,
		corecontract.CollaborationReviewDecisionRepairRequiredV1,
		affected,
		"dormant-drift-reviewer-zero",
	)
	// Drift the second physical node so the transaction has already attempted
	// the first transition before it detects the bad dormant head. BEGIN
	// IMMEDIATE must roll the first mutation back as well.
	driftedRunID := fixture.compiled.Decision.RepairChildren[1].RunManifest.RunID
	if _, err := fixture.store.db.Exec(`
		UPDATE loop_frames SET waiting_reason='DRIFTED_DORMANT_REASON'
		WHERE run_id=?
	`, driftedRunID); err != nil {
		t.Fatal(err)
	}
	rootLease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"dormant-drift-root",
	)
	if _, err := fixture.store.ApplyCompositeDecision(
		context.Background(),
		ApplyCompositeDecisionInput{Lease: rootLease},
	); err == nil ||
		(!errors.Is(err, ErrCompositeDecisionTransitionConflict) &&
			!errors.Is(err, ErrCompositeDecisionTransitionIntegrity)) {
		t.Fatalf("dormant drift error=%v", err)
	}
	for _, child := range fixture.compiled.Decision.RepairChildren {
		var runRevision, frameRevision, lastEvent, transitionEvents int64
		if err := fixture.store.db.QueryRow(`
			SELECT r.revision, f.frame_revision, f.last_authoritative_event,
			       (SELECT COUNT(*) FROM run_events
			        WHERE run_id=r.run_id AND event_sequence=1)
			FROM runs AS r JOIN loop_frames AS f ON f.run_id=r.run_id
			WHERE r.run_id=?
		`, child.RunManifest.RunID).Scan(
			&runRevision,
			&frameRevision,
			&lastEvent,
			&transitionEvents,
		); err != nil {
			t.Fatal(err)
		}
		if runRevision != 0 || frameRevision != 0 || lastEvent != 0 ||
			transitionEvents != 0 {
			t.Fatalf(
				"failed transition partially mutated %q: (%d,%d,%d,%d)",
				child.RunManifest.RunID,
				runRevision,
				frameRevision,
				lastEvent,
				transitionEvents,
			)
		}
	}
}

func TestCompositeDecisionSkippedEventLineageTamperIsRejected(t *testing.T) {
	fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
	for index, child := range fixture.compiled.Children {
		finishDecisionSpecialist(
			t,
			fixture,
			child,
			fmt.Sprintf("skip-tamper-initial-%d", index),
		)
	}
	affected := []string{fixture.compiled.Children[0].Assignment.SlotID}
	finishDecisionReviewer(
		t,
		fixture,
		fixture.compiled.Reviewer.RunManifest.RunID,
		corecontract.CollaborationReviewDecisionRepairRequiredV1,
		affected,
		"skip-tamper-reviewer-zero",
	)
	rootLease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"skip-tamper-root",
	)
	transition, err := fixture.store.ApplyCompositeDecision(
		context.Background(),
		ApplyCompositeDecisionInput{Lease: rootLease},
	)
	if err != nil {
		t.Fatal(err)
	}
	skipped := fixture.compiled.Decision.RepairChildren[1]
	_, canonical, err := corecontract.NewCompositeRepairSkippedEventV1(
		corecontract.CompositeRepairSkippedEventV1{
			SchemaVersion:      corecontract.CompositeRepairSkippedEventSchemaVersionV1,
			RunID:              skipped.RunManifest.RunID,
			RootRunID:          fixture.compiled.Parent.RunManifest.RunID,
			RootManifestDigest: fixture.compiled.Parent.RunManifest.ManifestDigest,
			ParentSlotID:       skipped.RunManifest.Composite.ParentSlotID,
			RepairRound:        corecontract.CompositeRepairRoundOneV1,
			SourceVerdictRef:   decisionTestDigest("wrong-verdict-ref", 0),
			Reason:             corecontract.CompositeRepairSkippedReasonV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.PutContent(
		context.Background(),
		ContentInput{
			Digest:         digest,
			Kind:           ContentRunEventPayload,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: canonical,
		},
	); err != nil {
		t.Fatal(err)
	}
	execClosedFileTamperV1(
		t,
		fixture.store,
		[]string{"run_events_reject_update"},
		`
		UPDATE run_events SET payload_ref=?, payload_digest=?
		WHERE run_id=? AND event_sequence=1
	`,
		digest,
		digest,
		skipped.RunManifest.RunID,
	)
	if _, err := fixture.store.ApplyCompositeDecision(
		context.Background(),
		ApplyCompositeDecisionInput{Lease: rootLease},
	); !errors.Is(err, ErrCompositeDecisionTransitionIntegrity) {
		t.Fatalf("skipped lineage tamper error=%v transition=%+v", err, transition)
	}
}

func assertDecisionRepairTransitionHeads(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	affected []string,
) {
	t.Helper()
	affectedSet := make(map[string]struct{}, len(affected))
	for _, slotID := range affected {
		affectedSet[slotID] = struct{}{}
	}
	for _, child := range fixture.compiled.Decision.RepairChildren {
		var state, disposition, step string
		var revision, frameRevision, eventCount, attempts, usage int64
		if err := fixture.store.db.QueryRow(`
			SELECT r.state, COALESCE(r.disposition, ''), r.revision,
			       f.step, f.frame_revision,
			       (SELECT COUNT(*) FROM run_events WHERE run_id=r.run_id),
			       (SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=r.run_id),
			       (SELECT COUNT(*) FROM model_usage WHERE run_id=r.run_id)
			FROM runs AS r JOIN loop_frames AS f ON f.run_id=r.run_id
			WHERE r.run_id=?
		`, child.RunManifest.RunID).Scan(
			&state,
			&disposition,
			&revision,
			&step,
			&frameRevision,
			&eventCount,
			&attempts,
			&usage,
		); err != nil {
			t.Fatal(err)
		}
		_, selected := affectedSet[child.Assignment.SlotID]
		if selected {
			if state != corecontract.InitialRunState || disposition != "" ||
				step != corecontract.InitialLoopStep {
				t.Fatalf("activated repair %q head=(%q,%q,%q)", child.RunManifest.RunID, state, disposition, step)
			}
		} else if state != corecontract.TerminatedLoopStep ||
			disposition != corecontract.TerminatedLoopStep ||
			step != corecontract.TerminatedLoopStep {
			t.Fatalf("skipped repair %q head=(%q,%q,%q)", child.RunManifest.RunID, state, disposition, step)
		}
		if revision != 1 || frameRevision != 1 || eventCount != 2 ||
			attempts != 0 || usage != 0 {
			t.Fatalf(
				"repair %q transition counts=(%d,%d,%d,%d,%d)",
				child.RunManifest.RunID,
				revision,
				frameRevision,
				eventCount,
				attempts,
				usage,
			)
		}
	}
}

func assertDecisionRepairReviewerStep(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	wantStep string,
	wantRevision int64,
) {
	t.Helper()
	var step string
	var runRevision, frameRevision, eventCount, attempts, usage int64
	runID := fixture.compiled.Decision.RepairReviewer.RunManifest.RunID
	if err := fixture.store.db.QueryRow(`
		SELECT f.step, r.revision, f.frame_revision,
		       (SELECT COUNT(*) FROM run_events WHERE run_id=r.run_id),
		       (SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=r.run_id),
		       (SELECT COUNT(*) FROM model_usage WHERE run_id=r.run_id)
		FROM runs AS r JOIN loop_frames AS f ON f.run_id=r.run_id
		WHERE r.run_id=?
	`, runID).Scan(
		&step,
		&runRevision,
		&frameRevision,
		&eventCount,
		&attempts,
		&usage,
	); err != nil {
		t.Fatal(err)
	}
	if step != wantStep || runRevision != wantRevision ||
		frameRevision != wantRevision || eventCount != wantRevision+1 ||
		attempts != 0 || usage != 0 {
		t.Fatalf(
			"repair Reviewer head=(%q,%d,%d) counts=(%d,%d,%d)",
			step,
			runRevision,
			frameRevision,
			eventCount,
			attempts,
			usage,
		)
	}
}

func acquireCurrentDecisionLease(
	t *testing.T,
	store *Store,
	runID string,
	ownerID string,
) RunLease {
	t.Helper()
	var runRevision, frameRevision int64
	if err := store.db.QueryRow(`
		SELECT r.revision, f.frame_revision
		FROM runs AS r JOIN loop_frames AS f ON f.run_id=r.run_id
		WHERE r.run_id=?
	`, runID).Scan(&runRevision, &frameRevision); err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 runID,
			OwnerID:               ownerID,
			ExpectedRunRevision:   uint64(runRevision),
			ExpectedFrameRevision: uint64(frameRevision),
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatalf("AcquireRunLease(%s): %v", runID, err)
	}
	return lease
}

func finishDecisionSpecialist(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	child assemblycompiler.CompositeChildCompileOutput,
	suffix string,
) {
	t.Helper()
	runID := child.RunManifest.RunID
	lease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		runID,
		"specialist-owner-"+suffix,
	)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := recompileContextForNewAttempt(
		corecontract.ContextCompilationV1{},
		run,
		run.Frame.Revision,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("compile Specialist %s: %v", runID, err)
	}
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "attempt-" + suffix,
			LogicalStepID:               corecontract.PureChatModelLogicalStepIDV1,
			ContextCompilationCanonical: compiled.CompilationCanonical,
			RequestCanonical:            compiled.RequestCanonical,
			Deadline: run.Manifest.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("BeginModelDispatch(%s)=%+v error=%v", runID, begin, err)
	}
	_, contributionCanonical, _, err :=
		corecontract.NewSpecialistContributionV1(
			corecontract.SpecialistContributionV1{
				SchemaVersion: corecontract.SpecialistContributionSchemaVersionV1,
				Proposal:      "proposal " + suffix,
				Evidence:      []corecontract.SpecialistEvidenceV1{},
				Assumptions:   []string{},
				Risks:         []string{},
				Conflicts:     []string{},
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	providerRequestID := "provider-" + suffix
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     string(contributionCanonical),
			ProviderRequestID: providerRequestID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical: modelUsageOutcomeCanonical(
				t,
				fmt.Sprintf(`{"id":%q,"status":"completed"}`, providerRequestID),
			),
		},
	); err != nil {
		t.Fatalf("CommitModelDispatchOutcome(%s): %v", runID, err)
	}
}

func finishDecisionReviewer(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	runID string,
	decision corecontract.CollaborationReviewDecisionV1,
	affected []string,
	suffix string,
) {
	t.Helper()
	begin, run := beginDecisionReviewer(t, fixture, runID, suffix)
	issues := []corecontract.ReviewIssueCodeV1{}
	slots := append([]string{}, affected...)
	sort.Strings(slots)
	reason := "The contribution set is complete."
	if decision != corecontract.CollaborationReviewDecisionApproveV1 {
		issues = []corecontract.ReviewIssueCodeV1{
			corecontract.ReviewIssueMissingEvidenceV1,
		}
		reason = "The selected contribution slots require a bounded decision."
	}
	root := run.CompositeRoot
	if root == nil || run.CompositeContributionSet == nil {
		t.Fatal("Reviewer lacks collaboration material")
	}
	verdict, _, err := corecontract.NewCollaborationReviewVerdictV1(
		corecontract.CollaborationReviewVerdictV1{
			SchemaVersion: corecontract.CollaborationReviewVerdictSchemaVersionV1,
			FamilyDigest:  root.ManifestDigest,
			ContributionSetDigest: run.
				CompositeContributionSetDigest,
			RepairRound:     run.CompositeRepairRound,
			Decision:        decision,
			IssueCodes:      issues,
			AffectedSlotIDs: slots,
			BoundedReason:   reason,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	modelCanonical, err :=
		corecontract.CanonicalCollaborationReviewModelVerdictV1(verdict)
	if err != nil {
		t.Fatal(err)
	}
	providerRequestID := "provider-" + suffix
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     string(modelCanonical),
			ProviderRequestID: providerRequestID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical: modelUsageOutcomeCanonical(
				t,
				fmt.Sprintf(`{"id":%q,"status":"completed"}`, providerRequestID),
			),
		},
	); err != nil {
		t.Fatalf("Commit Reviewer %s: %v", runID, err)
	}
}

func beginDecisionReviewer(
	t *testing.T,
	fixture *compositeAdmissionFixture,
	runID string,
	suffix string,
) (BeginModelDispatchResult, RunForLoop) {
	t.Helper()
	lease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		runID,
		"reviewer-owner-"+suffix,
	)
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := recompileContextForNewAttempt(
		corecontract.ContextCompilationV1{},
		run,
		run.Frame.Revision,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("compile Reviewer %s: %v", runID, err)
	}
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   "attempt-" + suffix,
			LogicalStepID:               corecontract.CompositeReviewLogicalStepIDV1,
			ContextCompilationCanonical: compiled.CompilationCanonical,
			RequestCanonical:            compiled.RequestCanonical,
			Deadline: run.Manifest.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("Begin Reviewer %s=%+v error=%v", runID, begin, err)
	}
	return begin, run
}

func TestCompositeDecisionFamilyDispatchCapReachedByProductionLifecycle(
	t *testing.T,
) {
	for _, memberCount := range []int{2, corecontract.CompositeMaxChildrenV1} {
		t.Run(fmt.Sprintf("N-%d", memberCount), func(t *testing.T) {
			fixture := newCommittedCompositeDecisionStoreFixture(t, memberCount)
			limit := int(
				fixture.compiled.Parent.RunManifest.Composite.Plan.
					FamilyModelDispatchLimit,
			)
			if limit != 2*memberCount+3 {
				t.Fatalf("family cap=%d want=%d", limit, 2*memberCount+3)
			}
			for index, child := range fixture.compiled.Children {
				finishDecisionSpecialist(
					t,
					fixture,
					child,
					fmt.Sprintf("cap-initial-%d-%02d", memberCount, index),
				)
			}
			affected := make([]string, memberCount)
			for index, child := range fixture.compiled.Children {
				affected[index] = child.Assignment.SlotID
			}
			sort.Strings(affected)
			finishDecisionReviewer(
				t,
				fixture,
				fixture.compiled.Reviewer.RunManifest.RunID,
				corecontract.CollaborationReviewDecisionRepairRequiredV1,
				affected,
				fmt.Sprintf("cap-reviewer-zero-%d", memberCount),
			)
			rootLease := acquireCurrentDecisionLease(
				t,
				fixture.store,
				fixture.compiled.Parent.RunManifest.RunID,
				fmt.Sprintf("cap-root-%d", memberCount),
			)
			first, err := fixture.store.ApplyCompositeDecision(
				context.Background(),
				ApplyCompositeDecisionInput{Lease: rootLease},
			)
			if err != nil || !first.Applied ||
				len(first.ActivatedRunIDs) != memberCount ||
				len(first.SkippedRunIDs) != 0 {
				t.Fatalf("activate all repairs=%+v error=%v", first, err)
			}
			for index, child := range fixture.compiled.Decision.RepairChildren {
				finishDecisionSpecialist(
					t,
					fixture,
					child,
					fmt.Sprintf("cap-repair-%d-%02d", memberCount, index),
				)
			}
			second, err := fixture.store.ApplyCompositeDecision(
				context.Background(),
				ApplyCompositeDecisionInput{Lease: rootLease},
			)
			if err != nil || !second.Applied ||
				len(second.ActivatedRunIDs) != memberCount+1 ||
				second.ActivatedRunIDs[len(second.ActivatedRunIDs)-1] !=
					fixture.compiled.Decision.RepairReviewer.RunManifest.RunID {
				t.Fatalf("activate repair Reviewer=%+v error=%v", second, err)
			}
			finishDecisionReviewer(
				t,
				fixture,
				fixture.compiled.Decision.RepairReviewer.RunManifest.RunID,
				corecontract.CollaborationReviewDecisionApproveV1,
				[]string{},
				fmt.Sprintf("cap-reviewer-one-%d", memberCount),
			)
			run, err := fixture.store.LoadRunForLoop(
				context.Background(),
				rootLease,
			)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := recompileContextForNewAttempt(
				corecontract.ContextCompilationV1{},
				run,
				run.Frame.Revision,
				nil,
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			input := BeginModelDispatchInput{
				Lease:         rootLease,
				AttemptID:     fmt.Sprintf("attempt-cap-root-%d", memberCount),
				LogicalStepID: corecontract.CompositeMergeLogicalStepIDV1,
				ContextCompilationCanonical: compiled.
					CompilationCanonical,
				RequestCanonical: compiled.RequestCanonical,
				Deadline: run.Manifest.Deadline.Add(-time.Hour).
					Truncate(time.Microsecond),
			}
			begin, err := fixture.store.BeginModelDispatch(
				context.Background(),
				input,
			)
			if err != nil || !begin.Created || !begin.InvokeAllowed {
				t.Fatalf("cap-th root Begin=%+v error=%v", begin, err)
			}
			var contentBefore, attemptsBefore, usageBefore int64
			if err := fixture.store.db.QueryRow(`
				SELECT
				 (SELECT COUNT(*) FROM content_records),
				 (SELECT COUNT(*) FROM model_dispatch_attempts),
				 (SELECT COUNT(*) FROM model_usage)
			`).Scan(&contentBefore, &attemptsBefore, &usageBefore); err != nil {
				t.Fatal(err)
			}
			if attemptsBefore != int64(limit) || usageBefore != int64(limit) {
				t.Fatalf(
					"production family closure attempts=%d usage=%d want=%d",
					attemptsBefore,
					usageBefore,
					limit,
				)
			}
			retryInput := input
			retryInput.Lease = begin.Lease
			retry, err := fixture.store.BeginModelDispatch(
				context.Background(),
				retryInput,
			)
			if err != nil || retry.Created || retry.InvokeAllowed ||
				retry.ConsumeModelInvocationPermit() ||
				retry.Attempt.AttemptID != begin.Attempt.AttemptID {
				t.Fatalf("exact retry at Decision cap=%+v error=%v", retry, err)
			}
			var contentAfter, attemptsAfter, usageAfter int64
			if err := fixture.store.db.QueryRow(`
				SELECT
				 (SELECT COUNT(*) FROM content_records),
				 (SELECT COUNT(*) FROM model_dispatch_attempts),
				 (SELECT COUNT(*) FROM model_usage)
			`).Scan(
				&contentAfter,
				&attemptsAfter,
				&usageAfter,
			); err != nil {
				t.Fatal(err)
			}
			if contentAfter != contentBefore || attemptsAfter != attemptsBefore ||
				usageAfter != usageBefore || attemptsAfter != int64(limit) {
				t.Fatalf(
					"cap retry side effects content=(%d,%d) attempts=(%d,%d,want=%d) usage=(%d,%d)",
					contentBefore,
					contentAfter,
					attemptsBefore,
					attemptsAfter,
					limit,
					usageBefore,
					usageAfter,
				)
			}
		})
	}
}

func TestCompositeDecisionReviewerUnknownCannotCreateSemanticReplay(
	t *testing.T,
) {
	fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
	for index, child := range fixture.compiled.Children {
		finishDecisionSpecialist(
			t,
			fixture,
			child,
			fmt.Sprintf("unknown-initial-%d", index),
		)
	}
	affected := []string{fixture.compiled.Children[0].Assignment.SlotID}
	finishDecisionReviewer(
		t,
		fixture,
		fixture.compiled.Reviewer.RunManifest.RunID,
		corecontract.CollaborationReviewDecisionRepairRequiredV1,
		affected,
		"unknown-reviewer-zero",
	)
	rootLease := acquireCurrentDecisionLease(
		t,
		fixture.store,
		fixture.compiled.Parent.RunManifest.RunID,
		"unknown-root",
	)
	if _, err := fixture.store.ApplyCompositeDecision(
		context.Background(),
		ApplyCompositeDecisionInput{Lease: rootLease},
	); err != nil {
		t.Fatal(err)
	}
	finishDecisionSpecialist(
		t,
		fixture,
		fixture.compiled.Decision.RepairChildren[0],
		"unknown-repair",
	)
	if result, err := fixture.store.ApplyCompositeDecision(
		context.Background(),
		ApplyCompositeDecisionInput{Lease: rootLease},
	); err != nil || !result.Applied {
		t.Fatalf("activate Reviewer1=%+v error=%v", result, err)
	}
	reviewerID := fixture.compiled.Decision.RepairReviewer.RunManifest.RunID
	begin, _ := beginDecisionReviewer(
		t,
		fixture,
		reviewerID,
		"unknown-reviewer-one",
	)
	outcome, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			ProviderRequestID:       "provider-unknown-reviewer-one",
			UnknownReason:           "ambiguous Reviewer completion",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	frontier, err := fixture.store.GetCompositeDecisionFrontier(
		context.Background(),
		fixture.compiled.Parent.RunManifest.RunID,
	)
	if err != nil || frontier.Stage != CompositeFrontierWaitingReconciliationV1 ||
		len(frontier.RunnableRunIDs) != 0 {
		t.Fatalf("UNKNOWN frontier=%+v error=%v", frontier, err)
	}
	if begin.Attempt.ContextCompilation == nil {
		t.Fatal("Reviewer Attempt lacks ContextCompilation")
	}
	retry, err := fixture.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       outcome.Lease,
			AttemptID:                   begin.Attempt.AttemptID,
			LogicalStepID:               begin.Attempt.LogicalStepID,
			ContextCompilationCanonical: begin.Attempt.ContextCompilation.CanonicalBytes,
			RequestCanonical:            begin.Attempt.Request.CanonicalBytes,
			Deadline:                    begin.Attempt.Deadline,
		},
	)
	if err != nil || retry.Created || retry.InvokeAllowed ||
		retry.Attempt.State != corecontract.ModelAttemptUnknown {
		t.Fatalf("UNKNOWN retry=%+v error=%v", retry, err)
	}
	var attempts, usage int64
	if err := fixture.store.db.QueryRow(`
		SELECT
		 (SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
		 (SELECT COUNT(*) FROM model_usage WHERE run_id=?)
	`, reviewerID, reviewerID).Scan(&attempts, &usage); err != nil ||
		attempts != 1 || usage != 1 {
		t.Fatalf("UNKNOWN ledger attempts=%d usage=%d error=%v", attempts, usage, err)
	}
}
