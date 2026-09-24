package currentbackup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type compositeBackupMode string

type compositeBackupReviewerOutcome string

const (
	compositeBackupComplete              compositeBackupMode = "COMPLETE"
	compositeBackupPending               compositeBackupMode = "PENDING"
	compositeBackupChildFailed           compositeBackupMode = "CHILD_FAILED"
	compositeBackupChildResultOverBudget compositeBackupMode = "CHILD_RESULT_OVER_BUDGET"
	compositeBackupDecisionAdmission     compositeBackupMode = "DECISION_ADMISSION"

	compositeBackupReviewerDisabled compositeBackupReviewerOutcome = ""
	compositeBackupReviewerApprove  compositeBackupReviewerOutcome = "APPROVE"
	compositeBackupReviewerReject   compositeBackupReviewerOutcome = "REJECT"
	compositeBackupReviewerFailed   compositeBackupReviewerOutcome = "FAILED"
	compositeBackupReviewerInvalid  compositeBackupReviewerOutcome = "INVALID"
	compositeBackupReviewerUnknown  compositeBackupReviewerOutcome = "UNKNOWN"
	compositeBackupDecisionDormant  compositeBackupReviewerOutcome = "DECISION_DORMANT"
	compositeBackupDecisionApprove  compositeBackupReviewerOutcome = "DECISION_APPROVE"
	compositeBackupDecisionRepair   compositeBackupReviewerOutcome = "DECISION_REPAIR_APPROVE"
)

type compositeBackupFixture struct {
	base                backupFixture
	tenantID            string
	rootRunID           string
	rootManifestDigest  string
	childRunIDs         []string
	childSlotIDs        []string
	reviewerRunID       string
	repairChildRunIDs   []string
	repairReviewerRunID string
	admissionKey        string
	intentDigest        string
	pendingChildID      string
	pendingAttemptID    string
	latchRef            string
	failureReason       string
}

type compositeStoredRun struct {
	RunID                string
	ManifestDigest       string
	ParentRunID          sql.NullString
	ParentManifestDigest sql.NullString
	ParentSlotID         sql.NullString
	CancelRequestRef     sql.NullString
	State                string
	Disposition          sql.NullString
	MemberSnapshotDigest string
}

type compositeStoredAttempt struct {
	RunID     string
	AttemptID string
	State     string
	ResultRef sql.NullString
}

type compositeStoredState struct {
	Runs     []compositeStoredRun
	Attempts []compositeStoredAttempt
}

func TestCompositeDecisionAdmissionBundleRoundTripPreservesDormantFamily(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newCompositeDecisionAdmissionBackupFixture(t)
	if err := VerifyCurrentStoreSemanticClosure(
		ctx,
		fixture.base.databasePath,
	); err != nil {
		t.Fatalf("verify source Decision admission: %v", err)
	}
	wantParticipants := 2*len(fixture.childRunIDs) + 3
	if got := countCompositeFamilyRuns(
		t,
		fixture.base.databasePath,
		fixture.rootRunID,
	); got != wantParticipants {
		t.Fatalf("Decision family cardinality=%d want %d", got, wantParticipants)
	}
	assertCompositeRepairParticipantsDormant(t, fixture.base.databasePath, fixture)

	bundle := filepath.Join(t.TempDir(), "composite-decision-admission.bundle")
	if _, err := CreateBundle(
		ctx,
		fixture.base.databasePath,
		fixture.base.artifactRoot,
		bundle,
		"currentbackup-composite-decision-admission-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle(Decision admission): %v", err)
	}
	if _, err := VerifyBundle(ctx, bundle); err != nil {
		t.Fatalf("VerifyBundle(Decision admission): %v", err)
	}
	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		filepath.Join(restoreRoot, "artifacts"),
	); err != nil {
		t.Fatalf("RestoreBundle(Decision admission): %v", err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
		t.Fatalf("verify restored Decision admission: %v", err)
	}
	if got := countCompositeFamilyRuns(
		t,
		restoredDatabase,
		fixture.rootRunID,
	); got != wantParticipants {
		t.Fatalf("restored Decision family cardinality=%d want %d", got, wantParticipants)
	}
	assertCompositeRepairParticipantsDormant(t, restoredDatabase, fixture)
	store, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatalf("reopen restored Decision admission: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close restored Decision admission: %v", err)
	}
}

func TestCompositeDecisionSemanticClosureRejectsRepairFamilyTampering(
	t *testing.T,
) {
	tests := []struct {
		name   string
		tamper func(*testing.T, compositeBackupFixture)
	}{
		{
			name: "repair Child physical parent slot",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				mustCompositeExec(
					t,
					fixture.base.databasePath,
					`UPDATE runs SET parent_slot_id='tampered-repair-slot' WHERE run_id=?`,
					fixture.repairChildRunIDs[0],
				)
			},
		},
		{
			name: "repair Reviewer detached from family",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				mustCompositeExec(
					t,
					fixture.base.databasePath,
					`UPDATE runs SET parent_run_id=NULL,
					 parent_manifest_digest=NULL, parent_slot_id=NULL WHERE run_id=?`,
					fixture.repairReviewerRunID,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCompositeDecisionAdmissionBackupFixture(t)
			test.tamper(t, fixture)
			err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				fixture.base.databasePath,
			)
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf("tampered Decision family error=%v want ErrIntegrity", err)
			}
		})
	}
}

func TestCompositeDecisionSemanticClosureRejectsRepairTransitionTampering(
	t *testing.T,
) {
	t.Run("READY without activation", func(t *testing.T) {
		fixture := newCompositeDecisionAdmissionBackupFixture(t)
		tamperCompositeRepairReadyWithoutActivation(t, fixture)
		if err := VerifyCurrentStoreSemanticClosure(
			context.Background(),
			fixture.base.databasePath,
		); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("repair without activation error=%v want ErrIntegrity", err)
		}
	})
	t.Run("skip source verdict", func(t *testing.T) {
		fixture := newCompositeDecisionCompleteBackupFixture(
			t,
			compositeBackupDecisionApprove,
		)
		tamperCompositeRepairSkipSourceVerdict(t, fixture)
		if err := VerifyCurrentStoreSemanticClosure(
			context.Background(),
			fixture.base.databasePath,
		); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("repair skip source verdict error=%v want ErrIntegrity", err)
		}
	})
}

func TestCompositeDecisionTerminalBundleRoundTripPreservesEffectiveRound(
	t *testing.T,
) {
	tests := []struct {
		name    string
		outcome compositeBackupReviewerOutcome
	}{
		{name: "round zero approve", outcome: compositeBackupDecisionApprove},
		{name: "one slot repair then round one approve", outcome: compositeBackupDecisionRepair},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := newCompositeDecisionCompleteBackupFixture(t, test.outcome)
			if err := VerifyCurrentStoreSemanticClosure(
				ctx,
				fixture.base.databasePath,
			); err != nil {
				t.Fatalf("verify source terminal Decision family: %v", err)
			}
			bundle := filepath.Join(t.TempDir(), "composite-decision-terminal.bundle")
			if _, err := CreateBundle(
				ctx,
				fixture.base.databasePath,
				fixture.base.artifactRoot,
				bundle,
				"currentbackup-composite-decision-terminal-test/v1",
			); err != nil {
				t.Fatalf("CreateBundle(terminal Decision): %v", err)
			}
			if _, err := VerifyBundle(ctx, bundle); err != nil {
				t.Fatalf("VerifyBundle(terminal Decision): %v", err)
			}
			restoreRoot := t.TempDir()
			restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
			if err := RestoreBundle(
				ctx,
				bundle,
				restoredDatabase,
				filepath.Join(restoreRoot, "artifacts"),
			); err != nil {
				t.Fatalf("RestoreBundle(terminal Decision): %v", err)
			}
			if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
				t.Fatalf("verify restored terminal Decision family: %v", err)
			}
			store, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
			if err != nil {
				t.Fatalf("reopen restored terminal Decision Store: %v", err)
			}
			terminal, terminalErr := store.GetTerminalRunResult(ctx, fixture.rootRunID)
			closeErr := store.Close()
			if err := errors.Join(terminalErr, closeErr); err != nil {
				t.Fatalf("read restored terminal Decision result: %v", err)
			}
			if terminal.State != corecontract.ModelAttemptSucceeded ||
				terminal.Output.AssistantText != "merged collaboration result" {
				t.Fatalf("restored terminal Decision result=%+v", terminal)
			}
		})
	}
}

func TestCompositeBundleRoundTripPreservesFamilyResultsAndCancellation(
	t *testing.T,
) {
	fixture := newCompositeBackupFixture(t, compositeBackupComplete, true)
	ctx := context.Background()
	if err := VerifyCurrentStoreSemanticClosure(ctx, fixture.base.databasePath); err != nil {
		t.Fatalf("verify source Composite Store: %v", err)
	}
	source := loadCompositeStoredState(t, fixture.base.databasePath, fixture.rootRunID)
	assertCompositeStoredState(t, source, fixture, true)

	bundle := filepath.Join(t.TempDir(), "composite.bundle")
	manifest, err := CreateBundle(
		ctx,
		fixture.base.databasePath,
		fixture.base.artifactRoot,
		bundle,
		"currentbackup-composite-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(Composite): %v", err)
	}
	verified, err := VerifyBundle(ctx, bundle)
	if err != nil {
		t.Fatalf("VerifyBundle(Composite): %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf("verified Manifest digest=%q want %q", verified.ManifestDigest, manifest.ManifestDigest)
	}

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
	if err := RestoreBundle(ctx, bundle, restoredDatabase, restoredArtifacts); err != nil {
		t.Fatalf("RestoreBundle(Composite): %v", err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
		t.Fatalf("verify restored Composite Store: %v", err)
	}
	restored := loadCompositeStoredState(t, restoredDatabase, fixture.rootRunID)
	if !reflect.DeepEqual(restored, source) {
		t.Fatalf("restored Composite closure differs:\nsource=%#v\nrestored=%#v", source, restored)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatalf("open restored Composite Store: %v", err)
	}
	terminal, resultErr := store.GetTerminalRunResult(ctx, fixture.rootRunID)
	closeErr := store.Close()
	if err := errors.Join(resultErr, closeErr); err != nil {
		t.Fatalf("read restored root terminal result: %v", err)
	}
	if terminal.State != corecontract.ModelAttemptSucceeded ||
		terminal.Output.AssistantText == "" {
		t.Fatalf("restored root terminal result=%+v", terminal)
	}
}

func TestCompositeReviewerApproveBundleRoundTripPreservesReviewGate(
	t *testing.T,
) {
	fixture := newCompositeReviewerBackupFixture(
		t,
		compositeBackupReviewerApprove,
		true,
	)
	ctx := context.Background()
	if err := VerifyCurrentStoreSemanticClosure(ctx, fixture.base.databasePath); err != nil {
		t.Fatalf("verify source Reviewer-enabled Composite Store: %v", err)
	}
	source := loadCompositeStoredState(t, fixture.base.databasePath, fixture.rootRunID)
	assertCompositeStoredState(t, source, fixture, true)

	bundle := filepath.Join(t.TempDir(), "composite-reviewer.bundle")
	manifest, err := CreateBundle(
		ctx,
		fixture.base.databasePath,
		fixture.base.artifactRoot,
		bundle,
		"currentbackup-composite-reviewer-test/v1",
	)
	if err != nil {
		t.Fatalf("CreateBundle(Reviewer-enabled Composite): %v", err)
	}
	verified, err := VerifyBundle(ctx, bundle)
	if err != nil {
		t.Fatalf("VerifyBundle(Reviewer-enabled Composite): %v", err)
	}
	if verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf(
			"verified Reviewer bundle Manifest digest=%q want %q",
			verified.ManifestDigest,
			manifest.ManifestDigest,
		)
	}

	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		filepath.Join(restoreRoot, "artifacts"),
	); err != nil {
		t.Fatalf("RestoreBundle(Reviewer-enabled Composite): %v", err)
	}
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
		t.Fatalf("verify restored Reviewer-enabled Composite Store: %v", err)
	}
	restored := loadCompositeStoredState(t, restoredDatabase, fixture.rootRunID)
	if !reflect.DeepEqual(restored, source) {
		t.Fatalf(
			"restored Reviewer-enabled Composite closure differs:\nsource=%#v\nrestored=%#v",
			source,
			restored,
		)
	}
}

func TestCompositeReviewerNonApproveStatesNeverContainMergeAttempt(
	t *testing.T,
) {
	tests := []struct {
		name    string
		outcome compositeBackupReviewerOutcome
	}{
		{name: "reject", outcome: compositeBackupReviewerReject},
		{name: "provider failed", outcome: compositeBackupReviewerFailed},
		{name: "invalid output", outcome: compositeBackupReviewerInvalid},
		{name: "unknown", outcome: compositeBackupReviewerUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCompositeReviewerBackupFixture(t, test.outcome, false)
			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				fixture.base.databasePath,
			); err != nil {
				t.Fatalf("verify Reviewer %s Store: %v", test.outcome, err)
			}
			database, err := sql.Open("sqlite", fixture.base.databasePath)
			if err != nil {
				t.Fatal(err)
			}
			var attempts int
			queryErr := database.QueryRow(`
				SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?
			`, fixture.rootRunID).Scan(&attempts)
			closeErr := database.Close()
			if err := errors.Join(queryErr, closeErr); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 {
				t.Fatalf(
					"Reviewer %s persisted %d merge Attempts",
					test.outcome,
					attempts,
				)
			}
		})
	}
}

func TestCompositeReviewerSemanticClosureRejectsTampering(t *testing.T) {
	tests := []struct {
		name                  string
		tamper                func(*testing.T, compositeBackupFixture)
		mutationRejectedEarly bool
	}{
		{
			name: "Reviewer parent relation",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				mustCompositeExec(
					t,
					fixture.base.databasePath,
					`UPDATE runs SET parent_slot_id='tampered-reviewer' WHERE run_id=?`,
					fixture.reviewerRunID,
				)
			},
		},
		{
			name:   "Reviewer frozen Control",
			tamper: tamperCompositeReviewerControl,
		},
		{
			name:   "noncanonical strict verdict",
			tamper: tamperCompositeReviewerVerdictCanonical,
		},
		{
			name:   "Reviewer output ceiling",
			tamper: tamperCompositeReviewerOutputCeiling,
		},
		{
			name:   "root Reviewer evidence",
			tamper: tamperCompositeRootReviewerEvidence,
		},
		{
			name:                  "Reviewer-enabled raw N plus 2 dispatch rejected",
			tamper:                assertCompositeRawDispatchRejected,
			mutationRejectedEarly: true,
		},
		{
			name: "partial family cancellation",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				mustCompositeExec(
					t,
					fixture.base.databasePath,
					`UPDATE runs SET cancel_request_ref=NULL WHERE run_id=?`,
					fixture.reviewerRunID,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCompositeReviewerBackupFixture(
				t,
				compositeBackupReviewerApprove,
				true,
			)
			test.tamper(t, fixture)
			if test.mutationRejectedEarly {
				return
			}
			err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				fixture.base.databasePath,
			)
			if !errors.Is(err, ErrIntegrity) {
				t.Fatalf("tampered Reviewer semantic error=%v want ErrIntegrity", err)
			}
		})
	}
}

func TestCompositeCoreDeterministicFailureBundleRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		mode   compositeBackupMode
		reason string
	}{
		{
			name:   "ALL_REQUIRED_CHILD_FAILED",
			mode:   compositeBackupChildFailed,
			reason: corecontract.AllRequiredChildFailedReasonV1,
		},
		{
			name:   "COMPOSITE_CHILD_RESULT_OVER_BUDGET",
			mode:   compositeBackupChildResultOverBudget,
			reason: corecontract.CompositeChildResultOverBudgetReasonV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := newCompositeBackupFixture(t, test.mode, false)
			if fixture.failureReason != test.reason {
				t.Fatalf("fixture reason=%q want %q", fixture.failureReason, test.reason)
			}
			if err := VerifyCurrentStoreSemanticClosure(
				ctx,
				fixture.base.databasePath,
			); err != nil {
				t.Fatalf("verify deterministic failure source: %v", err)
			}
			bundle := filepath.Join(t.TempDir(), "core-failure.bundle")
			if _, err := CreateBundle(
				ctx,
				fixture.base.databasePath,
				fixture.base.artifactRoot,
				bundle,
				"currentbackup-core-failure-test/v1",
			); err != nil {
				t.Fatalf("CreateBundle(deterministic failure): %v", err)
			}
			if _, err := VerifyBundle(ctx, bundle); err != nil {
				t.Fatalf("VerifyBundle(deterministic failure): %v", err)
			}
			restoreRoot := t.TempDir()
			restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
			if err := RestoreBundle(
				ctx,
				bundle,
				restoredDatabase,
				filepath.Join(restoreRoot, "artifacts"),
			); err != nil {
				t.Fatalf("RestoreBundle(deterministic failure): %v", err)
			}
			if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
				t.Fatalf("verify restored deterministic failure: %v", err)
			}
			store, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
			if err != nil {
				t.Fatalf("reopen deterministic failure Store: %v", err)
			}
			terminal, resultErr := store.GetTerminalRunResult(ctx, fixture.rootRunID)
			closeErr := store.Close()
			if err := errors.Join(resultErr, closeErr); err != nil {
				t.Fatalf("read restored deterministic failure: %v", err)
			}
			if terminal.ReasonCode != test.reason ||
				terminal.ErrorClassification != test.reason ||
				terminal.AttemptKind != "" || terminal.AttemptID != "" ||
				terminal.State != "" || terminal.ModelState != "" {
				t.Fatalf("restored deterministic terminal=%+v", terminal)
			}
		})
	}
}

func TestCompositeCoreDeterministicFailureSemanticTampering(t *testing.T) {
	tests := []struct {
		name   string
		tamper func(*testing.T, compositeBackupFixture)
	}{
		{name: "reason", tamper: tamperCoreFailureReason},
		{name: "event kind", tamper: tamperCoreFailureEventKind},
		{name: "payload ref", tamper: tamperCoreFailurePayloadRef},
		{name: "payload canonical", tamper: tamperCoreFailureCanonical},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCompositeBackupFixture(
				t,
				compositeBackupChildFailed,
				false,
			)
			test.tamper(t, fixture)
			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				fixture.base.databasePath,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("tampered deterministic Core failure error=%v want ErrIntegrity", err)
			}
		})
	}
}

func TestCompositeSemanticClosureRejectsFamilyTampering(t *testing.T) {
	tests := []struct {
		name                  string
		tamper                func(*testing.T, compositeBackupFixture)
		mutationRejectedEarly bool
	}{
		{
			name: "parent slot",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				mustCompositeExec(t, fixture.base.databasePath, `
					UPDATE runs SET parent_slot_id='slot-tampered'
					WHERE run_id=?
				`, fixture.childRunIDs[0])
			},
		},
		{
			name: "parent Manifest digest",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				mustCompositeExec(t, fixture.base.databasePath, `
					UPDATE runs SET parent_manifest_digest=?
					WHERE run_id=?
				`, strings.Repeat("f", 64), fixture.childRunIDs[0])
			},
		},
		{
			name: "member digest",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				mustCompositeClosedFileExec(
					t,
					fixture.base.databasePath,
					[]string{"member_execution_snapshots_reject_update"},
					`
					UPDATE member_execution_snapshots SET digest=?
					WHERE run_id=?
				`,
					strings.Repeat("e", 64),
					fixture.childRunIDs[0],
				)
			},
		},
		{
			name: "Child result",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				tamperCompositeChildResult(t, fixture)
			},
		},
		{
			name: "Composite compilation family evidence",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				tamperCompositeCompilationEvidence(t, fixture)
			},
		},
		{
			name: "partial cancellation latch",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				mustCompositeExec(t, fixture.base.databasePath, `
					UPDATE runs SET cancel_request_ref=NULL WHERE run_id=?
				`, fixture.childRunIDs[0])
			},
		},
		{
			name: "raw N plus 1 dispatch rejected",
			tamper: func(t *testing.T, fixture compositeBackupFixture) {
				assertCompositeRawDispatchRejected(t, fixture)
			},
			mutationRejectedEarly: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCompositeBackupFixture(t, compositeBackupComplete, true)
			test.tamper(t, fixture)
			if test.mutationRejectedEarly {
				return
			}
			if err := VerifyCurrentStoreSemanticClosure(
				context.Background(),
				fixture.base.databasePath,
			); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("tampered Composite semantic error=%v want ErrIntegrity", err)
			}
		})
	}
}

func TestCompositeBackupRestoreStartupRecoveryKeepsUnknownNonReplayable(
	t *testing.T,
) {
	fixture := newCompositeBackupFixture(t, compositeBackupPending, false)
	ctx := context.Background()
	// Model the same persisted lease after its wall-clock TTL elapsed. The
	// unsettled PENDING Attempt remains authoritative, but a restarted worker
	// can obtain a new fencing epoch without reusing the pre-crash token.
	mustCompositeExec(t, fixture.base.databasePath, `
		UPDATE loop_frames SET lease_expiry=1 WHERE run_id=?
	`, fixture.pendingChildID)
	if err := VerifyCurrentStoreSemanticClosure(ctx, fixture.base.databasePath); err != nil {
		t.Fatalf("verify source pending Composite Store: %v", err)
	}
	bundle := filepath.Join(t.TempDir(), "composite-pending.bundle")
	if _, err := CreateBundle(
		ctx,
		fixture.base.databasePath,
		fixture.base.artifactRoot,
		bundle,
		"currentbackup-composite-restart-test/v1",
	); err != nil {
		t.Fatalf("CreateBundle(pending Composite): %v", err)
	}
	restoreRoot := t.TempDir()
	restoredDatabase := filepath.Join(restoreRoot, "restored.sqlite")
	if err := RestoreBundle(
		ctx,
		bundle,
		restoredDatabase,
		filepath.Join(restoreRoot, "artifacts"),
	); err != nil {
		t.Fatalf("RestoreBundle(pending Composite): %v", err)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, restoredDatabase)
	if err != nil {
		t.Fatalf("open restored pending Composite Store: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()
	recovery, err := store.ScanStartupRecovery(ctx)
	if err != nil {
		t.Fatalf("ScanStartupRecovery: %v", err)
	}
	pending, found := findStartupRecoveryRun(recovery, fixture.pendingChildID)
	if !found || pending.UnsettledAttemptID != fixture.pendingAttemptID ||
		pending.UnsettledAttemptState != corecontract.ModelAttemptPending {
		t.Fatalf("restored pending Child recovery projection=%+v found=%v", pending, found)
	}
	childRunRevision := readCompositeRunRevision(t, restoredDatabase, fixture.pendingChildID)
	childLease, err := store.AcquireRunLease(ctx, currentstore.AcquireRunLeaseInput{
		RunID:                 fixture.pendingChildID,
		OwnerID:               "composite-restart-child",
		ExpectedRunRevision:   childRunRevision,
		ExpectedFrameRevision: pending.FrameRevision,
		TTL:                   time.Hour,
	})
	if err != nil {
		t.Fatalf("acquire restored Child lease: %v", err)
	}
	recovered, err := store.RecoverStartupPending(
		ctx,
		currentstore.RecoverStartupPendingInput{
			Lease:         childLease,
			AttemptKind:   corecontract.AttemptKindModel,
			AttemptID:     fixture.pendingAttemptID,
			UnknownReason: "CRASH_RECOVERY",
		},
	)
	if err != nil {
		t.Fatalf("RecoverStartupPending(Composite Child): %v", err)
	}
	if err := store.ReleaseRunLease(ctx, recovered.Lease); err != nil {
		t.Fatalf("release recovered Child lease: %v", err)
	}

	recovery, err = store.ScanStartupRecovery(ctx)
	if err != nil {
		t.Fatalf("ScanStartupRecovery after recovery: %v", err)
	}
	unknown, found := findStartupRecoveryRun(recovery, fixture.pendingChildID)
	if !found || unknown.UnsettledAttemptID != fixture.pendingAttemptID ||
		unknown.UnsettledAttemptState != corecontract.ModelAttemptUnknown {
		t.Fatalf("recovered Child projection=%+v found=%v", unknown, found)
	}
	rootRecovery, found := findStartupRecoveryRun(recovery, fixture.rootRunID)
	if !found {
		t.Fatalf("Composite root absent from recovery scan: %+v", recovery)
	}
	rootRunRevision := readCompositeRunRevision(t, restoredDatabase, fixture.rootRunID)
	rootLease, err := store.AcquireRunLease(ctx, currentstore.AcquireRunLeaseInput{
		RunID:                 fixture.rootRunID,
		OwnerID:               "composite-restart-root",
		ExpectedRunRevision:   rootRunRevision,
		ExpectedFrameRevision: rootRecovery.FrameRevision,
		TTL:                   time.Hour,
	})
	if err != nil {
		t.Fatalf("acquire restored root lease: %v", err)
	}
	root, err := store.LoadRunForLoop(ctx, rootLease)
	if err != nil {
		t.Fatalf("LoadRunForLoop(restored root): %v", err)
	}
	if err := store.ReleaseRunLease(ctx, rootLease); err != nil {
		t.Fatalf("release restored root lease: %v", err)
	}
	unknownSeen := false
	for _, child := range root.CompositeChildren {
		if child.RunID == fixture.pendingChildID {
			unknownSeen = child.State == currentstore.CompositeChildUnknownV1 &&
				child.ErrorClassification == "MODEL_UNKNOWN" &&
				child.ResultRef == "" && len(child.OutputCanonical) == 0
		}
	}
	if !unknownSeen {
		t.Fatalf("root does not expose restored UNKNOWN Child without replay: %+v", root.CompositeChildren)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	if err := VerifyCurrentStoreSemanticClosure(ctx, restoredDatabase); err != nil {
		t.Fatalf("verify recovered Composite Store: %v", err)
	}
}

func newCompositeBackupFixture(
	t *testing.T,
	mode compositeBackupMode,
	installCancellation bool,
) compositeBackupFixture {
	return newCompositeBackupFixtureWithReviewer(
		t,
		mode,
		installCancellation,
		compositeBackupReviewerDisabled,
	)
}

func newCompositeReviewerBackupFixture(
	t *testing.T,
	outcome compositeBackupReviewerOutcome,
	installCancellation bool,
) compositeBackupFixture {
	t.Helper()
	if outcome == compositeBackupReviewerDisabled {
		t.Fatal("Reviewer backup fixture requires an enabled outcome")
	}
	return newCompositeBackupFixtureWithReviewer(
		t,
		compositeBackupComplete,
		installCancellation,
		outcome,
	)
}

func newCompositeDecisionAdmissionBackupFixture(
	t *testing.T,
) compositeBackupFixture {
	t.Helper()
	return newCompositeBackupFixtureWithReviewer(
		t,
		compositeBackupDecisionAdmission,
		false,
		compositeBackupDecisionDormant,
	)
}

func newCompositeDecisionCompleteBackupFixture(
	t *testing.T,
	outcome compositeBackupReviewerOutcome,
) compositeBackupFixture {
	t.Helper()
	if outcome != compositeBackupDecisionApprove &&
		outcome != compositeBackupDecisionRepair {
		t.Fatalf("unsupported Decision completion outcome %q", outcome)
	}
	return newCompositeBackupFixtureWithReviewer(
		t,
		compositeBackupComplete,
		false,
		outcome,
	)
}

func compositeBackupDecisionEnabled(
	outcome compositeBackupReviewerOutcome,
) bool {
	switch outcome {
	case compositeBackupDecisionDormant,
		compositeBackupDecisionApprove,
		compositeBackupDecisionRepair:
		return true
	default:
		return false
	}
}

func countCompositeFamilyRuns(
	t *testing.T,
	databasePath string,
	rootRunID string,
) int {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	queryErr := database.QueryRow(`
		SELECT COUNT(*) FROM runs
		WHERE run_id=? OR parent_run_id=?
	`, rootRunID, rootRunID).Scan(&count)
	closeErr := database.Close()
	if err := errors.Join(queryErr, closeErr); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertCompositeRepairParticipantsDormant(
	t *testing.T,
	databasePath string,
	fixture compositeBackupFixture,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	participants := append(
		append([]string(nil), fixture.repairChildRunIDs...),
		fixture.repairReviewerRunID,
	)
	for _, runID := range participants {
		var (
			state         string
			disposition   sql.NullString
			runRevision   int64
			frameRevision int64
			step          string
			waiting       sql.NullString
			attempts      int
			events        int
		)
		if err := database.QueryRow(`
			SELECT r.state, r.disposition, r.revision,
			       f.frame_revision, f.step, f.waiting_reason,
			       (SELECT COUNT(*) FROM model_dispatch_attempts AS a WHERE a.run_id=r.run_id),
			       (SELECT COUNT(*) FROM run_events AS e WHERE e.run_id=r.run_id)
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
			&attempts,
			&events,
		); err != nil {
			t.Fatalf("read dormant repair participant %q: %v", runID, err)
		}
		if state != corecontract.InitialRunState || !disposition.Valid ||
			disposition.String != "WAITING_EXTERNAL" || runRevision != 0 ||
			frameRevision != 0 ||
			step != corecontract.WaitingRepairActivationLoopStep ||
			!waiting.Valid || waiting.String != "COMPOSITE_REPAIR_DORMANT" ||
			attempts != 0 || events != 1 {
			t.Fatalf(
				"repair participant %q is not dormant: state=%q disposition=%v run/frame=%d/%d step=%q waiting=%v attempts/events=%d/%d",
				runID,
				state,
				disposition,
				runRevision,
				frameRevision,
				step,
				waiting,
				attempts,
				events,
			)
		}
	}
}

func newCompositeBackupFixtureWithReviewer(
	t *testing.T,
	mode compositeBackupMode,
	installCancellation bool,
	reviewerOutcome compositeBackupReviewerOutcome,
) compositeBackupFixture {
	return newCompositeBackupFixtureWithReviewerAndTransfer(
		t,
		mode,
		installCancellation,
		reviewerOutcome,
		false,
	)
}

func newCompositeBackupFixtureWithReviewerAndTransfer(
	t *testing.T,
	mode compositeBackupMode,
	installCancellation bool,
	reviewerOutcome compositeBackupReviewerOutcome,
	workspaceTransfer bool,
) compositeBackupFixture {
	t.Helper()
	ctx := context.Background()
	base := newBackupFixture(t)
	prepared := prepareProfiledExampleSeed(t, exampleSeedPath(t))
	assembly := prepared.DefaultAssembly()
	store, err := currentstore.OpenExistingCurrentStore(ctx, base.databasePath)
	if err != nil {
		t.Fatalf("open Store for Composite fixture: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, assembly.TenantID)
	if err != nil {
		t.Fatalf("LoadPublishedBasis for Composite: %v", err)
	}
	if len(control.Profiles) != 1 || len(control.Agents) != 1 {
		t.Fatalf("unexpected current Control for Composite: agents=%d profiles=%d", len(control.Agents), len(control.Profiles))
	}
	coordinatorProfile := cloneCompositeBackupProfile(control.Profiles[0])
	coordinatorProfile.Bindings = filterCompositeModelBinding(coordinatorProfile.Bindings)
	control.Profiles[0] = coordinatorProfile
	analysisAgent := corecontract.AgentRef{
		ID: "backup-analysis", Version: "1", Digest: strings.Repeat("a", 64),
	}
	reviewAgent := corecontract.AgentRef{
		ID: "backup-review", Version: "1", Digest: strings.Repeat("b", 64),
	}
	analysisProfile := cloneCompositeBackupProfile(coordinatorProfile)
	analysisProfile.Profile = corecontract.ProfileRef{
		ID: "backup-analysis", Version: "1", Digest: strings.Repeat("c", 64),
	}
	reviewProfile := cloneCompositeBackupProfile(coordinatorProfile)
	reviewProfile.Profile = corecontract.ProfileRef{
		ID: "backup-review", Version: "1", Digest: strings.Repeat("d", 64),
	}
	reviewerAgent := corecontract.AgentRef{
		ID: "backup-reviewer-gate", Version: "1", Digest: strings.Repeat("e", 64),
	}
	reviewerProfile := cloneCompositeBackupProfile(coordinatorProfile)
	reviewerProfile.Profile = corecontract.ProfileRef{
		ID: "backup-reviewer-gate", Version: "1", Digest: strings.Repeat("f", 64),
	}
	control.SnapshotID = "control-composite-backup"
	control.Revision++
	control.Agents = append(control.Agents, analysisAgent, reviewAgent)
	control.Profiles = append(control.Profiles, analysisProfile, reviewProfile)
	definition := controlcontract.CompositeAgentDefinitionV1{
		SchemaVersion:        controlcontract.CompositeAgentSchemaVersionV1,
		AgentID:              assembly.AgentID,
		CoordinatorProfileID: coordinatorProfile.Profile.ID,
		Members: []controlcontract.CompositeAgentMemberV1{
			{
				SlotID: "analysis", AgentID: analysisAgent.ID,
				ProfileID: analysisProfile.Profile.ID, FocusID: "analysis",
				WeightBasisPoints: 6000,
			},
			{
				SlotID: "review", AgentID: reviewAgent.ID,
				ProfileID: reviewProfile.Profile.ID, FocusID: "review",
				WeightBasisPoints: 4000,
			},
		},
	}
	if reviewerOutcome != compositeBackupReviewerDisabled {
		definition.Reviewer = &controlcontract.CompositeReviewerDefinitionV1{
			SchemaVersion:   controlcontract.CompositeReviewerSchemaVersionV1,
			AgentID:         reviewerAgent.ID,
			ProfileID:       reviewerProfile.Profile.ID,
			MaxOutputTokens: 64,
			Policy:          controlcontract.CompositeReviewerPolicyResultsGateV1,
		}
		control.Agents = append(control.Agents, reviewerAgent)
		control.Profiles = append(control.Profiles, reviewerProfile)
	}
	if compositeBackupDecisionEnabled(reviewerOutcome) {
		definition.Decision = &controlcontract.CompositeDecisionDefinitionV1{
			SchemaVersion: controlcontract.CompositeDecisionSchemaVersionV1,
		}
	}
	if workspaceTransfer {
		if definition.Decision == nil || len(control.Workspaces) != 1 ||
			len(definition.Members) == 0 {
			t.Fatal("Workspace transfer fixture requires one Decision root Workspace")
		}
		rootWorkspace := control.Workspaces[0].Workspace
		targetWorkspace := corecontract.WorkspaceRef{
			ID:      "workspace-composite-backup-target",
			Version: "1",
			Digest:  strings.Repeat("9", 64),
		}
		rootGrant := compositeBackupTransferGrantV1(
			"grant-composite-backup-root-target",
			control.TenantID,
			rootWorkspace,
			targetWorkspace,
			true,
		)
		targetGrant := compositeBackupTransferGrantV1(
			"grant-composite-backup-target-root",
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
				TransferGrants: []corecontract.WorkspaceTransferGrantV1{targetGrant},
			},
		)
		definition.Members[0].TargetWorkspaceID = targetWorkspace.ID
	}
	control.CompositeAgents = []controlcontract.CompositeAgentDefinitionV1{definition}
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze Composite Control: %v", err)
	}
	catalog.GenerationID = "catalog-composite-backup"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze Composite Catalog: %v", err)
	}
	basis, err = store.PublishControlCatalog(ctx, currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: basis.PointerRevision,
		NewPointerRevision:      basis.PointerRevision + 1,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	})
	if err != nil {
		t.Fatalf("publish Composite Control/Catalog: %v", err)
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
		SchemaVersion: corecontract.TaskInputSchemaVersionV1,
		Text:          "design and review the Composite backup path",
	})
	if err != nil {
		t.Fatal(err)
	}
	taskDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentTaskInput,
		coreJSONMediaType,
		taskCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	intent, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:          assembly.TenantID,
			AdmissionKey:      "composite-backup-admission",
			PrincipalID:       "composite-backup-principal",
			WorkspaceID:       assembly.WorkspaceID,
			AgentID:           assembly.AgentID,
			ProfileID:         coordinatorProfile.Profile.ID,
			TaskInputRef:      taskDigest,
			RequestedPorts:    []moduleapi.PortRef{{Name: moduleapi.PortNameModelGenerate, ExactVersion: moduleapi.PortVersionV2}},
			Deadline:          deadline,
			CancellationScope: corecontract.CancellationScopeFamilyV1,
			ExplicitLimits:    []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("freeze Composite intent: %v", err)
	}
	compiled, err := (assemblycompiler.Compiler{}).CompileCompositeFamily(
		ctx,
		assemblycompiler.CompositeCompileInput{
			Parent: assemblycompiler.CompileInput{
				IntentCanonical:  intentCanonical,
				IntentDigest:     intentDigest,
				RunID:            "run-composite-backup-root",
				MemberID:         "member-composite-backup-root",
				RecoveryRootRef:  "recovery/composite-backup-root",
				PublishedBasis:   basis,
				ControlCanonical: controlCanonical,
				CatalogCanonical: catalogCanonical,
			},
		},
	)
	if err != nil {
		t.Fatalf("CompileCompositeFamily for backup: %v", err)
	}
	task := currentstore.ContentInput{
		Digest: taskDigest, Kind: currentstore.ContentTaskInput,
		MediaType: coreJSONMediaType, CanonicalBytes: taskCanonical,
	}
	commit := currentstore.CommitCompositeRunFamilyInput{
		Parent: currentstore.CommitRunAdmissionInput{
			PublishedBasis: basis, IntentCanonical: intentCanonical,
			IntentDigest:            intentDigest,
			MemberSnapshotCanonical: compiled.Parent.MemberSnapshotCanonical,
			RunManifestCanonical:    compiled.Parent.RunManifestCanonical,
			Contents:                []currentstore.ContentInput{task},
		},
		Children: make([]currentstore.CommitRunAdmissionInput, len(compiled.Children)),
	}
	for index, child := range compiled.Children {
		commit.Children[index] = currentstore.CommitRunAdmissionInput{
			PublishedBasis: basis, IntentCanonical: child.IntentCanonical,
			IntentDigest:            child.IntentDigest,
			MemberSnapshotCanonical: child.MemberSnapshotCanonical,
			RunManifestCanonical:    child.RunManifestCanonical,
			Contents:                []currentstore.ContentInput{task},
		}
	}
	if compiled.Reviewer != nil {
		reviewer := currentstore.CommitRunAdmissionInput{
			PublishedBasis: basis, IntentCanonical: compiled.Reviewer.IntentCanonical,
			IntentDigest:            compiled.Reviewer.IntentDigest,
			MemberSnapshotCanonical: compiled.Reviewer.MemberSnapshotCanonical,
			RunManifestCanonical:    compiled.Reviewer.RunManifestCanonical,
			Contents:                []currentstore.ContentInput{task},
		}
		commit.Reviewer = &reviewer
	}
	if compiled.Decision != nil {
		commit.RepairChildren = make(
			[]currentstore.CommitRunAdmissionInput,
			len(compiled.Decision.RepairChildren),
		)
		for index, repair := range compiled.Decision.RepairChildren {
			commit.RepairChildren[index] = currentstore.CommitRunAdmissionInput{
				PublishedBasis:          basis,
				IntentCanonical:         repair.IntentCanonical,
				IntentDigest:            repair.IntentDigest,
				MemberSnapshotCanonical: repair.MemberSnapshotCanonical,
				RunManifestCanonical:    repair.RunManifestCanonical,
				Contents:                []currentstore.ContentInput{task},
			}
		}
		repair := compiled.Decision.RepairReviewer
		commit.RepairReviewer = &currentstore.CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         repair.IntentCanonical,
			IntentDigest:            repair.IntentDigest,
			MemberSnapshotCanonical: repair.MemberSnapshotCanonical,
			RunManifestCanonical:    repair.RunManifestCanonical,
			Contents:                []currentstore.ContentInput{task},
		}
	}
	committed, err := store.CommitCompositeRunFamily(ctx, commit)
	if err != nil {
		t.Fatalf("CommitCompositeRunFamily for backup: %v", err)
	}
	fixture := compositeBackupFixture{
		base:               base,
		tenantID:           intent.TenantID,
		rootRunID:          committed.Parent.RunID,
		rootManifestDigest: compiled.Parent.RunManifest.ManifestDigest,
		admissionKey:       intent.AdmissionKey,
		intentDigest:       intentDigest,
		childRunIDs:        make([]string, len(committed.Children)),
		childSlotIDs:       make([]string, len(committed.Children)),
	}
	for index, child := range committed.Children {
		fixture.childRunIDs[index] = child.RunID
		fixture.childSlotIDs[index] = compiled.Parent.RunManifest.Composite.Plan.Children[index].SlotID
	}
	if committed.Reviewer != nil {
		fixture.reviewerRunID = committed.Reviewer.RunID
	}
	fixture.repairChildRunIDs = make([]string, len(committed.RepairChildren))
	for index, repair := range committed.RepairChildren {
		fixture.repairChildRunIDs[index] = repair.RunID
	}
	if committed.RepairReviewer != nil {
		fixture.repairReviewerRunID = committed.RepairReviewer.RunID
	}

	switch mode {
	case compositeBackupDecisionAdmission:
		if len(fixture.repairChildRunIDs) != len(fixture.childRunIDs) ||
			fixture.repairReviewerRunID == "" {
			t.Fatalf(
				"Decision admission lacks repair family: initial=%d repair=%d reviewer=%q",
				len(fixture.childRunIDs),
				len(fixture.repairChildRunIDs),
				fixture.repairReviewerRunID,
			)
		}
	case compositeBackupComplete:
		registry := newCompositeBackupRegistry(t, prepared)
		if compositeBackupDecisionEnabled(reviewerOutcome) {
			registry = newCompositeBackupRegistryWithInvoker(
				t,
				prepared,
				&compositeBackupDecisionInvoker{
					provider: compositeBackupProvider(prepared),
					repair:   reviewerOutcome == compositeBackupDecisionRepair,
				},
			)
		} else if reviewerOutcome != compositeBackupReviewerDisabled {
			registry = newCompositeReviewerBackupRegistry(
				t,
				prepared,
				reviewerOutcome,
			)
		}
		loop, err := coreloop.NewUniversalLoop(store, registry)
		if err != nil {
			t.Fatal(err)
		}
		if compositeBackupDecisionEnabled(reviewerOutcome) {
			runCompositeDecisionBackupFixture(
				t,
				store,
				loop,
				base.databasePath,
				&fixture,
				reviewerOutcome == compositeBackupDecisionRepair,
			)
			break
		}
		for _, childRunID := range fixture.childRunIDs {
			result, err := loop.Run(ctx, loopapi.RunInput{
				RunID: childRunID, MaxSteps: 1, MaxDuration: time.Minute,
			})
			if err != nil || result.Disposition != loopapi.DispositionTerminated {
				t.Fatalf("run Composite Child %q: result=%+v err=%v", childRunID, result, err)
			}
		}
		if fixture.reviewerRunID != "" {
			reviewed, reviewErr := loop.Run(ctx, loopapi.RunInput{
				RunID: fixture.reviewerRunID, MaxSteps: 1, MaxDuration: time.Minute,
			})
			if reviewErr != nil {
				t.Fatalf("run Composite Reviewer: result=%+v err=%v", reviewed, reviewErr)
			}
			switch reviewerOutcome {
			case compositeBackupReviewerApprove, compositeBackupReviewerReject,
				compositeBackupReviewerFailed, compositeBackupReviewerInvalid:
				if reviewed.Disposition != loopapi.DispositionTerminated {
					t.Fatalf("terminal Composite Reviewer result=%+v", reviewed)
				}
			case compositeBackupReviewerUnknown:
				if reviewed.Disposition != loopapi.DispositionWaitingReconciliation {
					t.Fatalf("UNKNOWN Composite Reviewer result=%+v", reviewed)
				}
			default:
				t.Fatalf("unsupported Composite Reviewer outcome %q", reviewerOutcome)
			}
			if reviewerOutcome == compositeBackupReviewerApprove {
				// A terminal observation may advance only the local LoopFrame. The
				// frozen result-set/evidence revision must remain the final
				// authoritative RunEvent revision across this retry.
				beforeFrame, beforeEvent := readCompositeFrameAndEventRevision(
					t,
					base.databasePath,
					fixture.reviewerRunID,
				)
				retried, retryErr := loop.Run(ctx, loopapi.RunInput{
					RunID:       fixture.reviewerRunID,
					MaxSteps:    1,
					MaxDuration: time.Minute,
				})
				if retryErr != nil || retried.Disposition != loopapi.DispositionTerminated {
					t.Fatalf("retry terminal Composite Reviewer: result=%+v err=%v", retried, retryErr)
				}
				afterFrame, afterEvent := readCompositeFrameAndEventRevision(
					t,
					base.databasePath,
					fixture.reviewerRunID,
				)
				if afterFrame <= beforeFrame || afterEvent != beforeEvent ||
					afterFrame <= afterEvent {
					t.Fatalf(
						"terminal Reviewer retry revisions before=%d/%d after=%d/%d",
						beforeFrame,
						beforeEvent,
						afterFrame,
						afterEvent,
					)
				}
			}
		}
		result, err := loop.Run(ctx, loopapi.RunInput{
			RunID: fixture.rootRunID, MaxSteps: 1, MaxDuration: time.Minute,
		})
		if err != nil {
			t.Fatalf("run Composite root: result=%+v err=%v", result, err)
		}
		switch reviewerOutcome {
		case compositeBackupReviewerDisabled, compositeBackupReviewerApprove:
			if result.Disposition != loopapi.DispositionTerminated {
				t.Fatalf("run Composite root: result=%+v", result)
			}
		case compositeBackupReviewerReject:
			fixture.failureReason = corecontract.CompositeReviewRejectedReasonV1
		case compositeBackupReviewerFailed:
			fixture.failureReason = corecontract.CompositeReviewFailedReasonV1
		case compositeBackupReviewerInvalid:
			fixture.failureReason = corecontract.CompositeReviewOutputInvalidReasonV1
		case compositeBackupReviewerUnknown:
			if result.Disposition == loopapi.DispositionTerminated {
				t.Fatalf("UNKNOWN Reviewer permitted Composite merge: result=%+v", result)
			}
		}
		if fixture.failureReason != "" &&
			(result.Disposition != loopapi.DispositionTerminated ||
				result.ReasonCode != fixture.failureReason) {
			t.Fatalf("review-gated Composite root: result=%+v want reason=%q", result, fixture.failureReason)
		}
	case compositeBackupPending:
		fixture.pendingChildID = fixture.childRunIDs[0]
		fixture.pendingAttemptID = "composite-backup-pending-attempt"
		beginCompositePendingChild(t, store, fixture.pendingChildID, fixture.pendingAttemptID, deadline)
	case compositeBackupChildFailed:
		fixture.failureReason = corecontract.AllRequiredChildFailedReasonV1
		registry := newCompositeBackupRegistryWithInvoker(
			t,
			prepared,
			&compositeBackupFixedInvoker{
				provider: compositeBackupProvider(prepared),
				outcome:  modulehost.InvocationFailed,
			},
		)
		loop, err := coreloop.NewUniversalLoop(store, registry)
		if err != nil {
			t.Fatal(err)
		}
		child, err := loop.Run(ctx, loopapi.RunInput{
			RunID: fixture.childRunIDs[0], MaxSteps: 1, MaxDuration: time.Minute,
		})
		if err != nil || child.Disposition != loopapi.DispositionTerminated {
			t.Fatalf("run failed Composite Child: result=%+v err=%v", child, err)
		}
		root, err := loop.Run(ctx, loopapi.RunInput{
			RunID: fixture.rootRunID, MaxSteps: 1, MaxDuration: time.Minute,
		})
		if err != nil || root.Disposition != loopapi.DispositionTerminated ||
			root.ReasonCode != fixture.failureReason {
			t.Fatalf("run failed-child Composite root: result=%+v err=%v", root, err)
		}
	case compositeBackupChildResultOverBudget:
		fixture.failureReason = corecontract.CompositeChildResultOverBudgetReasonV1
		registry := newCompositeBackupRegistryWithInvoker(
			t,
			prepared,
			&compositeBackupFixedInvoker{
				provider:      compositeBackupProvider(prepared),
				outcome:       modulehost.InvocationSucceeded,
				assistantText: strings.Repeat("x", 200_000),
			},
		)
		loop, err := coreloop.NewUniversalLoop(store, registry)
		if err != nil {
			t.Fatal(err)
		}
		for _, childRunID := range fixture.childRunIDs {
			child, err := loop.Run(ctx, loopapi.RunInput{
				RunID: childRunID, MaxSteps: 1, MaxDuration: time.Minute,
			})
			if err != nil || child.Disposition != loopapi.DispositionTerminated {
				t.Fatalf("run over-budget Composite Child %q: result=%+v err=%v", childRunID, child, err)
			}
		}
		root, err := loop.Run(ctx, loopapi.RunInput{
			RunID: fixture.rootRunID, MaxSteps: 1, MaxDuration: time.Minute,
		})
		if err != nil || root.Disposition != loopapi.DispositionTerminated ||
			root.ReasonCode != fixture.failureReason {
			t.Fatalf("run over-budget Composite root: result=%+v err=%v", root, err)
		}
	default:
		t.Fatalf("unsupported Composite backup mode %q", mode)
	}
	if installCancellation {
		_, cancellationCanonical, err := corecontract.NewRunCancellationRequestV1(
			corecontract.RunCancellationRequestV1{
				SchemaVersion:      corecontract.RunCancellationRequestSchemaVersionV1,
				RootRunID:          fixture.rootRunID,
				RootManifestDigest: fixture.rootManifestDigest,
				Scope:              corecontract.CancellationScopeFamilyV1,
				ReasonCode:         corecontract.CancellationReasonShutdownV1,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		cancelled, err := store.RequestRunCancellation(
			ctx,
			currentstore.RequestRunCancellationInput{Canonical: cancellationCanonical},
		)
		if err != nil {
			t.Fatalf("RequestRunCancellation: %v", err)
		}
		fixture.latchRef = cancelled.Ref
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	return fixture
}

func compositeBackupTransferGrantV1(
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
		return grant
	}
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
	return grant
}

func cloneCompositeBackupProfile(
	input controlcontract.ProfileDefinition,
) controlcontract.ProfileDefinition {
	cloned := input
	if input.ModelProfile != nil {
		profile := *input.ModelProfile
		cloned.ModelProfile = &profile
	}
	cloned.Bindings = append([]controlcontract.BindingSpec(nil), input.Bindings...)
	for index := range cloned.Bindings {
		cloned.Bindings[index].StaticContextRefs = append(
			[]string(nil), input.Bindings[index].StaticContextRefs...,
		)
	}
	return cloned
}

func filterCompositeModelBinding(
	bindings []controlcontract.BindingSpec,
) []controlcontract.BindingSpec {
	filtered := make([]controlcontract.BindingSpec, 0, 1)
	for _, binding := range bindings {
		if binding.Port.Name == moduleapi.PortNameModelGenerate &&
			binding.Port.ExactVersion == moduleapi.PortVersionV2 {
			filtered = append(filtered, binding)
		}
	}
	return filtered
}

func newCompositeBackupRegistry(
	t *testing.T,
	prepared *bootstrapseed.Prepared,
) *exactadapter.Registry {
	t.Helper()
	provider := compositeBackupProvider(prepared)
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatal(err)
	}
	return newCompositeBackupRegistryWithInvoker(t, prepared, echo)
}

func newCompositeReviewerBackupRegistry(
	t *testing.T,
	prepared *bootstrapseed.Prepared,
	outcome compositeBackupReviewerOutcome,
) *exactadapter.Registry {
	t.Helper()
	provider := compositeBackupProvider(prepared)
	echo, err := exactadapter.NewDeterministicEcho(provider)
	if err != nil {
		t.Fatal(err)
	}
	return newCompositeBackupRegistryWithInvoker(
		t,
		prepared,
		&compositeBackupReviewerInvoker{
			provider: provider,
			echo:     echo,
			outcome:  outcome,
		},
	)
}

func compositeBackupProvider(
	prepared *bootstrapseed.Prepared,
) moduleapi.ActivatedModuleRef {
	model := prepared.ModelAssertion()
	return moduleapi.ActivatedModuleRef{
		ModuleID:           model.ModuleID,
		Version:            model.ExactVersion,
		ArtifactDigest:     model.ArtifactDigest,
		InstanceID:         model.InstanceID,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    model.ExpectedAdapterIdentity,
		ActivationRevision: model.ActivationRevision,
	}
}

func newCompositeBackupRegistryWithInvoker(
	t *testing.T,
	prepared *bootstrapseed.Prepared,
	invoker modulehost.ModuleInvoker,
) *exactadapter.Registry {
	t.Helper()
	model := prepared.ModelAssertion()
	registry, err := exactadapter.NewRegistry(exactadapter.Registration{
		ArtifactDigest:  model.ArtifactDigest,
		AdapterIdentity: model.ExpectedAdapterIdentity,
		Invoker:         invoker,
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

type compositeBackupFixedInvoker struct {
	provider      moduleapi.ActivatedModuleRef
	outcome       modulehost.InvocationOutcome
	assistantText string
}

type compositeBackupReviewerInvoker struct {
	provider moduleapi.ActivatedModuleRef
	echo     modulehost.ModuleInvoker
	outcome  compositeBackupReviewerOutcome
}

type compositeBackupDecisionInvoker struct {
	provider moduleapi.ActivatedModuleRef
	repair   bool
}

func (invoker *compositeBackupDecisionInvoker) Invoke(
	_ context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		prepared.Invocation.Input,
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	assignment := ""
	firstContributionSlot := ""
	repairSpecialist := false
	reviewRound := -1
	rootMerge := false
	for _, message := range request.Messages {
		switch message.Content {
		case coreCollaborationReviewPolicyRoundZeroV1:
			reviewRound = 0
		case coreCollaborationReviewPolicyRoundOneV1:
			reviewRound = 1
		case coreCollaborationRootMergeInstructionV1:
			rootMerge = true
		}
		if strings.HasPrefix(
			message.Content,
			coreCompositeAssignmentPrefixV1,
		) {
			var envelope coreCompositeAssignmentEnvelopeV1
			if err := json.Unmarshal(
				[]byte(strings.TrimPrefix(
					message.Content,
					coreCompositeAssignmentPrefixV1,
				)),
				&envelope,
			); err != nil {
				return modulehost.InvocationResult{}, err
			}
			assignment = envelope.SlotID
		}
		if strings.HasPrefix(
			message.Content,
			coreCollaborationRepairBasisPrefixV1,
		) {
			repairSpecialist = true
		}
		if firstContributionSlot == "" && strings.HasPrefix(
			message.Content,
			coreCollaborationContributionPrefixV1,
		) {
			var envelope coreCollaborationContributionEnvelopeV1
			if err := json.Unmarshal(
				[]byte(strings.TrimPrefix(
					message.Content,
					coreCollaborationContributionPrefixV1,
				)),
				&envelope,
			); err != nil {
				return modulehost.InvocationResult{}, err
			}
			firstContributionSlot = envelope.SlotID
		}
	}
	if reviewRound >= 0 {
		decision := corecontract.CollaborationReviewDecisionApproveV1
		issues := []corecontract.ReviewIssueCodeV1{}
		affected := []string{}
		reason := "the structured contributions satisfy the review contract"
		if invoker.repair && reviewRound == 0 {
			decision = corecontract.CollaborationReviewDecisionRepairRequiredV1
			issues = []corecontract.ReviewIssueCodeV1{
				corecontract.ReviewIssueIncompleteCoverageV1,
			}
			affected = []string{firstContributionSlot}
			reason = "the highest-weight Specialist requires one bounded repair"
		}
		verdict := corecontract.CollaborationReviewVerdictV1{
			SchemaVersion:         corecontract.CollaborationReviewVerdictSchemaVersionV1,
			FamilyDigest:          strings.Repeat("a", 64),
			ContributionSetDigest: strings.Repeat("b", 64),
			RepairRound:           uint32(reviewRound),
			Decision:              decision,
			IssueCodes:            issues,
			AffectedSlotIDs:       affected,
			BoundedReason:         reason,
		}
		canonical, err :=
			corecontract.CanonicalCollaborationReviewModelVerdictV1(verdict)
		if err != nil {
			return modulehost.InvocationResult{}, err
		}
		return compositeBackupReviewerOutput(invoker.provider, string(canonical))
	}
	if rootMerge {
		return compositeBackupReviewerOutput(
			invoker.provider,
			"merged collaboration result",
		)
	}
	if assignment != "" {
		proposal := "initial contribution for " + assignment
		if repairSpecialist {
			proposal = "repaired contribution for " + assignment
		}
		_, canonical, _, err := corecontract.NewSpecialistContributionV1(
			corecontract.SpecialistContributionV1{
				SchemaVersion: corecontract.SpecialistContributionSchemaVersionV1,
				Proposal:      proposal,
				Evidence:      []corecontract.SpecialistEvidenceV1{},
				Assumptions:   []string{},
				Risks:         []string{},
				Conflicts:     []string{},
			},
		)
		if err != nil {
			return modulehost.InvocationResult{}, err
		}
		return compositeBackupReviewerOutput(invoker.provider, string(canonical))
	}
	return modulehost.InvocationResult{}, errors.New(
		"currentbackup: unrecognized Collaboration test request",
	)
}

func runCompositeDecisionBackupFixture(
	t *testing.T,
	_ *currentstore.Store,
	loop *coreloop.UniversalLoop,
	_ string,
	fixture *compositeBackupFixture,
	repair bool,
) {
	t.Helper()
	ctx := context.Background()
	runTerminal := func(label string, runID string) {
		t.Helper()
		result, err := loop.Run(ctx, loopapi.RunInput{
			RunID: runID, MaxSteps: 1, MaxDuration: time.Minute,
		})
		if err != nil || result.Disposition != loopapi.DispositionTerminated {
			t.Fatalf("run %s %q: result=%+v err=%v", label, runID, result, err)
		}
	}
	runWaiting := func(label string) {
		t.Helper()
		result, err := loop.Run(ctx, loopapi.RunInput{
			RunID: fixture.rootRunID, MaxSteps: 1, MaxDuration: time.Minute,
		})
		if err != nil || result.Disposition != loopapi.DispositionWaitingExternal {
			t.Fatalf("run %s root transition: result=%+v err=%v", label, result, err)
		}
	}
	for _, runID := range fixture.childRunIDs {
		runTerminal("initial Collaboration Specialist", runID)
	}
	runTerminal("round-zero Collaboration Reviewer", fixture.reviewerRunID)
	runWaiting("round-zero")
	if repair {
		if len(fixture.repairChildRunIDs) == 0 {
			t.Fatal("repair Decision fixture lacks repair Children")
		}
		runTerminal(
			"round-one Collaboration Specialist",
			fixture.repairChildRunIDs[0],
		)
		runWaiting("repair Reviewer activation")
		runTerminal(
			"round-one Collaboration Reviewer",
			fixture.repairReviewerRunID,
		)
	}
	runTerminal("Collaboration root merge", fixture.rootRunID)
}

func (invoker *compositeBackupReviewerInvoker) Invoke(
	ctx context.Context,
	prepared modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	familyDigest, specialistDigest, affectedSlot, reviewer, err :=
		parseCompositeBackupReviewerRequest(prepared.Invocation.Input)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	if !reviewer {
		return invoker.echo.Invoke(ctx, prepared)
	}
	switch invoker.outcome {
	case compositeBackupReviewerUnknown:
		return modulehost.InvocationResult{
			Provider: invoker.provider,
			Outcome:  modulehost.InvocationUnknown,
		}, nil
	case compositeBackupReviewerFailed:
		return modulehost.InvocationResult{
			Provider: invoker.provider,
			Outcome:  modulehost.InvocationFailed,
		}, nil
	case compositeBackupReviewerInvalid:
		return compositeBackupReviewerOutput(
			invoker.provider,
			`{"schema_version":"review-verdict/v1"}`,
		)
	case compositeBackupReviewerApprove, compositeBackupReviewerReject:
		verdict := corecontract.ReviewVerdictV1{
			SchemaVersion:          corecontract.ReviewVerdictSchemaVersionV1,
			FamilyDigest:           familyDigest,
			SpecialistResultDigest: specialistDigest,
			Decision:               corecontract.ReviewDecisionApproveV1,
			BoundedReason:          "specialist results satisfy the frozen review gate",
		}
		if invoker.outcome == compositeBackupReviewerReject {
			verdict.Decision = corecontract.ReviewDecisionRejectV1
			verdict.IssueCodes = []corecontract.ReviewIssueCodeV1{
				corecontract.ReviewIssueContradictionV1,
			}
			verdict.AffectedSlotIDs = []string{affectedSlot}
			verdict.BoundedReason = "specialist results contradict each other"
		}
		_, canonical, freezeErr := corecontract.NewReviewVerdictV1(verdict)
		if freezeErr != nil {
			return modulehost.InvocationResult{}, freezeErr
		}
		return compositeBackupReviewerOutput(invoker.provider, string(canonical))
	default:
		return modulehost.InvocationResult{}, errors.New(
			"currentbackup: unsupported Reviewer test outcome",
		)
	}
}

func compositeBackupReviewerOutput(
	provider moduleapi.ActivatedModuleRef,
	assistantText string,
) (modulehost.InvocationResult, error) {
	_, canonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: assistantText,
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	return modulehost.InvocationResult{
		Provider: provider,
		Outcome:  modulehost.InvocationSucceeded,
		Output:   canonical,
	}, nil
}

func parseCompositeBackupReviewerRequest(
	canonical []byte,
) (string, string, string, bool, error) {
	request, err := moduleapi.RestoreModelGenerateRequestV1(canonical)
	if err != nil {
		return "", "", "", false, err
	}
	const (
		policyPrefix = "COMPOSITE_REVIEW_POLICY_JSON:\n"
		resultPrefix = "UNTRUSTED_COMPOSITE_SPECIALIST_RESULT_JSON:\n"
	)
	type policyEnvelope struct {
		FamilyDigest           string `json:"family_digest"`
		SpecialistResultDigest string `json:"specialist_result_digest"`
	}
	type resultEnvelope struct {
		SlotID string `json:"slot_id"`
	}
	var policy *policyEnvelope
	affectedSlot := ""
	for _, message := range request.Messages {
		if strings.HasPrefix(message.Content, policyPrefix) {
			if policy != nil {
				return "", "", "", false, errors.New(
					"currentbackup: duplicate Reviewer policy message",
				)
			}
			decoded := policyEnvelope{}
			if err := json.Unmarshal(
				[]byte(strings.TrimPrefix(message.Content, policyPrefix)),
				&decoded,
			); err != nil {
				return "", "", "", false, err
			}
			policy = &decoded
		}
		if affectedSlot == "" && strings.HasPrefix(message.Content, resultPrefix) {
			decoded := resultEnvelope{}
			if err := json.Unmarshal(
				[]byte(strings.TrimPrefix(message.Content, resultPrefix)),
				&decoded,
			); err != nil {
				return "", "", "", false, err
			}
			affectedSlot = decoded.SlotID
		}
	}
	if policy == nil {
		return "", "", "", false, nil
	}
	if !moduleapi.ValidSHA256(policy.FamilyDigest) ||
		!moduleapi.ValidSHA256(policy.SpecialistResultDigest) ||
		affectedSlot == "" {
		return "", "", "", false, errors.New(
			"currentbackup: Reviewer request lacks exact digest/slot bindings",
		)
	}
	return policy.FamilyDigest,
		policy.SpecialistResultDigest,
		affectedSlot,
		true,
		nil
}

func (invoker *compositeBackupFixedInvoker) Invoke(
	_ context.Context,
	_ modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	result := modulehost.InvocationResult{
		Provider: invoker.provider,
		Outcome:  invoker.outcome,
	}
	if invoker.outcome != modulehost.InvocationSucceeded {
		return result, nil
	}
	_, canonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: invoker.assistantText,
		},
	)
	if err != nil {
		return modulehost.InvocationResult{}, err
	}
	result.Output = canonical
	return result, nil
}

func beginCompositePendingChild(
	t *testing.T,
	store *currentstore.Store,
	runID string,
	attemptID string,
	deadline time.Time,
) {
	t.Helper()
	ctx := context.Background()
	lease, err := store.AcquireRunLease(ctx, currentstore.AcquireRunLeaseInput{
		RunID:                 runID,
		OwnerID:               "composite-backup-pending",
		ExpectedRunRevision:   0,
		ExpectedFrameRevision: 0,
		TTL:                   time.Hour,
	})
	if err != nil {
		t.Fatalf("AcquireRunLease(pending Composite Child): %v", err)
	}
	run, err := store.LoadRunForLoop(ctx, lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop(pending Composite Child): %v", err)
	}
	var modelBinding *moduleapi.PortBinding
	for _, plan := range run.Member.PortPlans {
		if plan.Port.Name == moduleapi.PortNameModelGenerate &&
			plan.Port.ExactVersion == moduleapi.PortVersionV2 &&
			len(plan.Bindings) == 1 {
			binding := plan.Bindings[0]
			modelBinding = &binding
			break
		}
	}
	if modelBinding == nil {
		t.Fatal("pending Composite Child has no exact model Binding")
	}
	modelConfigContent, found := run.FindContent(modelBinding.ConfigRef)
	if !found || modelConfigContent.Kind != currentstore.ContentConfig {
		t.Fatalf("pending Composite model config=%+v found=%v", modelConfigContent, found)
	}
	modelConfig, err := moduleapi.RestoreModelBindingConfigV2(modelConfigContent.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	contextPolicy, found := run.FindContent(run.Member.ContextPolicy.Digest)
	if !found || contextPolicy.Kind != currentstore.ContentPolicy {
		t.Fatalf("pending Composite ContextPolicy=%+v found=%v", contextPolicy, found)
	}
	task, found := run.FindContent(run.Manifest.TaskInputRef)
	if !found || task.Kind != currentstore.ContentTaskInput {
		t.Fatalf("pending Composite Task=%+v found=%v", task, found)
	}
	var modelProfileCanonical []byte
	if run.Member.ModelProfile != nil {
		content, found := run.FindContent(run.Member.ModelProfile.Digest)
		if !found || content.Kind != currentstore.ContentConfig {
			t.Fatalf("pending Composite ModelProfile=%+v found=%v", content, found)
		}
		modelProfileCanonical = content.CanonicalBytes
	}
	compiled, err := contextcompiler.CompileV1(contextcompiler.CompileInputV1{
		TenantID:                       run.Manifest.TenantID,
		WorkspaceScope:                 run.Member.Workspace,
		AgentScope:                     run.Member.Agent,
		ContextPolicyRef:               run.Member.ContextPolicy,
		ContextPolicyDocumentCanonical: contextPolicy.CanonicalBytes,
		ModelProfileRef:                run.Member.ModelProfile,
		ModelProfileCanonical:          modelProfileCanonical,
		ModelParameters:                modelConfig.Parameters,
		TaskInputRef:                   run.Manifest.TaskInputRef,
		TaskInputCanonical:             task.CanonicalBytes,
		Composite:                      run.Manifest.Composite,
	})
	if err != nil {
		t.Fatalf("CompileV1(pending Composite Child): %v", err)
	}
	if len(compiled.CompilationCanonical) == 0 {
		t.Fatal("pending Composite Child compilation did not preserve assignment evidence")
	}
	begin, err := store.BeginModelDispatch(ctx, currentstore.BeginModelDispatchInput{
		Lease:                       lease,
		AttemptID:                   attemptID,
		LogicalStepID:               "composite.child.reply/v1",
		ContextCompilationCanonical: compiled.CompilationCanonical,
		RequestCanonical:            compiled.RequestCanonical,
		Deadline:                    deadline.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("BeginModelDispatch(pending Composite Child): %v", err)
	}
	if begin.Attempt.State != corecontract.ModelAttemptPending {
		t.Fatalf("pending Composite Attempt=%+v", begin.Attempt)
	}
}

func loadCompositeStoredState(
	t *testing.T,
	databasePath string,
	rootRunID string,
) compositeStoredState {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state := compositeStoredState{Runs: make([]compositeStoredRun, 0), Attempts: make([]compositeStoredAttempt, 0)}
	rows, err := database.Query(`
		SELECT
			r.run_id, manifest.digest,
			r.parent_run_id, r.parent_manifest_digest, r.parent_slot_id,
			r.cancel_request_ref, r.state, r.disposition, member.digest
		FROM runs AS r
		JOIN run_manifests AS manifest ON manifest.run_id=r.run_id
		JOIN member_execution_snapshots AS member ON member.run_id=r.run_id
		WHERE r.run_id=? OR r.parent_run_id=?
		ORDER BY r.run_id
	`, rootRunID, rootRunID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var run compositeStoredRun
		if err := rows.Scan(
			&run.RunID,
			&run.ManifestDigest,
			&run.ParentRunID,
			&run.ParentManifestDigest,
			&run.ParentSlotID,
			&run.CancelRequestRef,
			&run.State,
			&run.Disposition,
			&run.MemberSnapshotDigest,
		); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		state.Runs = append(state.Runs, run)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	rows, err = database.Query(`
		SELECT attempt.run_id, attempt.attempt_id, attempt.state, attempt.result_ref
		FROM model_dispatch_attempts AS attempt
		JOIN runs AS r ON r.run_id=attempt.run_id
		WHERE r.run_id=? OR r.parent_run_id=?
		ORDER BY attempt.run_id, attempt.attempt_id
	`, rootRunID, rootRunID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var attempt compositeStoredAttempt
		if err := rows.Scan(
			&attempt.RunID,
			&attempt.AttemptID,
			&attempt.State,
			&attempt.ResultRef,
		); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		state.Attempts = append(state.Attempts, attempt)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertCompositeStoredState(
	t *testing.T,
	state compositeStoredState,
	fixture compositeBackupFixture,
	terminal bool,
) {
	t.Helper()
	expectedRuns := 1 + len(fixture.childRunIDs)
	if fixture.reviewerRunID != "" {
		expectedRuns++
	}
	if len(state.Runs) != expectedRuns {
		t.Fatalf("Composite stored Run count=%d state=%+v", len(state.Runs), state)
	}
	byID := make(map[string]compositeStoredRun, len(state.Runs))
	for _, run := range state.Runs {
		byID[run.RunID] = run
		if fixture.latchRef != "" &&
			(!run.CancelRequestRef.Valid || run.CancelRequestRef.String != fixture.latchRef) {
			t.Fatalf("Composite Run %q cancellation latch=%v want %q", run.RunID, run.CancelRequestRef, fixture.latchRef)
		}
	}
	root := byID[fixture.rootRunID]
	if root.RunID == "" || root.ParentRunID.Valid || root.ParentManifestDigest.Valid || root.ParentSlotID.Valid {
		t.Fatalf("stored Composite root=%+v", root)
	}
	for index, childID := range fixture.childRunIDs {
		child := byID[childID]
		if child.RunID == "" || child.ParentRunID.String != fixture.rootRunID ||
			child.ParentManifestDigest.String != root.ManifestDigest ||
			child.ParentSlotID.String != fixture.childSlotIDs[index] {
			t.Fatalf("stored Composite Child %d=%+v root=%+v", index, child, root)
		}
	}
	if fixture.reviewerRunID != "" {
		reviewer := byID[fixture.reviewerRunID]
		if reviewer.RunID == "" || reviewer.ParentRunID.String != fixture.rootRunID ||
			reviewer.ParentManifestDigest.String != root.ManifestDigest ||
			reviewer.ParentSlotID.String != corecontract.CompositeReviewerParentSlotIDV1 {
			t.Fatalf("stored Composite Reviewer=%+v root=%+v", reviewer, root)
		}
	}
	if terminal {
		if len(state.Attempts) != len(state.Runs) {
			t.Fatalf("Composite terminal Attempt count=%d want %d", len(state.Attempts), len(state.Runs))
		}
		for _, attempt := range state.Attempts {
			if attempt.State != string(corecontract.ModelAttemptSucceeded) || !attempt.ResultRef.Valid {
				t.Fatalf("Composite terminal Attempt=%+v", attempt)
			}
		}
	}
}

func tamperCompositeChildResult(t *testing.T, fixture compositeBackupFixture) {
	t.Helper()
	_, canonical, err := moduleapi.NewModelGenerateOutputV1(moduleapi.ModelGenerateOutputV1{
		SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
		AssistantText: "tampered Child result",
	})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := currentstore.ComputeContentDigest(
		currentstore.ContentModelResult,
		coreJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, insertErr := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, 'MODEL_RESULT', 'application/json', ?, ?, ?)
	`, digest, canonical, len(canonical), time.Now().UTC().UnixMicro())
	execClosedFileTamperV1(
		t,
		database,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`
		UPDATE model_dispatch_attempts SET result_ref=? WHERE run_id=?
	`,
		digest,
		fixture.childRunIDs[0],
	)
	closeErr := database.Close()
	if err := errors.Join(insertErr, closeErr); err != nil {
		t.Fatal(err)
	}
}

func tamperCompositeRepairReadyWithoutActivation(
	t *testing.T,
	fixture compositeBackupFixture,
) {
	t.Helper()
	if len(fixture.repairChildRunIDs) == 0 {
		t.Fatal("Decision fixture lacks a repair Child")
	}
	runID := fixture.repairChildRunIDs[0]
	budgetRef, continuation, err := corecontract.NewInitialLoopState(runID)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := database.Exec(`
		UPDATE runs SET disposition=NULL WHERE run_id=?
	`, runID)
	_, frameErr := database.Exec(`
		UPDATE loop_frames
		SET step=?, usage_ledger_ref=?, continuation=?,
		    pending_attempt_id=NULL, pending_dispatch_attempt_id=NULL,
		    waiting_reason=NULL
		WHERE run_id=?
	`, corecontract.InitialLoopStep, budgetRef, continuation, runID)
	closeErr := database.Close()
	if err := errors.Join(runErr, frameErr, closeErr); err != nil {
		t.Fatal(err)
	}
}

func tamperCompositeRepairSkipSourceVerdict(
	t *testing.T,
	fixture compositeBackupFixture,
) {
	t.Helper()
	if len(fixture.repairChildRunIDs) == 0 {
		t.Fatal("Decision fixture lacks a skipped repair Child")
	}
	runID := fixture.repairChildRunIDs[0]
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var canonical []byte
	if err := database.QueryRow(`
		SELECT content.canonical_bytes
		FROM run_events AS event
		JOIN content_records AS content
		  ON content.content_digest=event.payload_ref
		WHERE event.run_id=? AND event.event_sequence=1
	`, runID).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	event, err := corecontract.RestoreCompositeRepairSkippedEventV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	event.SourceVerdictRef = strings.Repeat("0", 64)
	_, rewritten, err := corecontract.NewCompositeRepairSkippedEventV1(event)
	if err != nil {
		t.Fatal(err)
	}
	ref := insertCompositeContent(
		t,
		database,
		currentstore.ContentRunEventPayload,
		rewritten,
	)
	execClosedFileTamperV1(
		t,
		database,
		[]string{"run_events_reject_update"},
		`
		UPDATE run_events SET payload_ref=?, payload_digest=?
		WHERE run_id=? AND event_sequence=1
	`,
		ref,
		ref,
		runID,
	)
}

func tamperCompositeReviewerVerdictCanonical(
	t *testing.T,
	fixture compositeBackupFixture,
) {
	t.Helper()
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var attemptID, resultRef string
	if err := database.QueryRow(`
		SELECT attempt_id, result_ref
		FROM model_dispatch_attempts
		WHERE run_id=? AND state='SUCCEEDED'
	`, fixture.reviewerRunID).Scan(&attemptID, &resultRef); err != nil {
		t.Fatal(err)
	}
	var canonical []byte
	if err := database.QueryRow(`
		SELECT canonical_bytes FROM content_records WHERE content_digest=?
	`, resultRef).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	output.AssistantText = " " + output.AssistantText
	_, rewritten, err := moduleapi.NewModelGenerateOutputV1(output)
	if err != nil {
		t.Fatal(err)
	}
	rewrittenRef := insertCompositeContent(
		t,
		database,
		currentstore.ContentModelResult,
		rewritten,
	)
	execClosedFileTamperV1(
		t,
		database,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`
		UPDATE model_dispatch_attempts SET result_ref=? WHERE attempt_id=?
	`,
		rewrittenRef,
		attemptID,
	)
	_, historyErr := database.Exec(`
		UPDATE history_entries
		SET content_ref=?, content_digest=?
		WHERE run_id=? AND source_attempt_id=?
	`, rewrittenRef, rewrittenRef, fixture.reviewerRunID, attemptID)
	if historyErr != nil {
		t.Fatal(historyErr)
	}
}

func tamperCompositeReviewerControl(
	t *testing.T,
	fixture compositeBackupFixture,
) {
	t.Helper()
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var snapshotID, digest string
	var revision uint64
	var canonical []byte
	if err := database.QueryRow(`
		SELECT snapshot_id, revision, digest, canonical_json
		FROM control_snapshots
		WHERE snapshot_id=(
			SELECT catalog.control_snapshot_id
			FROM member_execution_snapshots AS member
			JOIN runtime_catalog_generations AS catalog
			  ON catalog.generation_id=member.catalog_generation_id
			WHERE member.run_id=?
		)
	`, fixture.reviewerRunID).Scan(
		&snapshotID,
		&revision,
		&digest,
		&canonical,
	); err != nil {
		t.Fatal(err)
	}
	control, err := controlcontract.RestoreControlSnapshot(
		canonical,
		controlcontract.ControlSnapshotRef{
			SnapshotID: snapshotID,
			Revision:   revision,
			Digest:     digest,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(control.CompositeAgents) != 1 ||
		control.CompositeAgents[0].Reviewer == nil {
		t.Fatal("Reviewer-enabled Control definition is absent")
	}
	control.CompositeAgents[0].Reviewer.MaxOutputTokens++
	_, rewrittenRef, rewritten, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	if rewrittenRef.Digest == digest {
		t.Fatal("Reviewer Control tamper did not change digest")
	}
	execClosedFileTamperV1(
		t,
		database,
		[]string{"control_snapshots_reject_update"},
		`
		UPDATE control_snapshots
		SET canonical_json=?, digest=?
		WHERE snapshot_id=?
	`,
		rewritten,
		rewrittenRef.Digest,
		snapshotID,
	)
}

func tamperCompositeReviewerOutputCeiling(
	t *testing.T,
	fixture compositeBackupFixture,
) {
	t.Helper()
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var attemptID, requestRef, compilationRef string
	if err := database.QueryRow(`
		SELECT attempt_id, request_ref, context_compilation_ref
		FROM model_dispatch_attempts WHERE run_id=?
	`, fixture.reviewerRunID).Scan(
		&attemptID,
		&requestRef,
		&compilationRef,
	); err != nil {
		t.Fatal(err)
	}
	var requestCanonical, compilationCanonical []byte
	if err := database.QueryRow(`
		SELECT canonical_bytes FROM content_records WHERE content_digest=?
	`, requestRef).Scan(&requestCanonical); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
		SELECT canonical_bytes FROM content_records WHERE content_digest=?
	`, compilationRef).Scan(&compilationCanonical); err != nil {
		t.Fatal(err)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(requestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	request.Parameters = []byte(`{"max_tokens":65}`)
	_, rewrittenRequest, err := moduleapi.NewModelGenerateRequestV1(request)
	if err != nil {
		t.Fatal(err)
	}
	rewrittenRequestRef := insertCompositeContent(
		t,
		database,
		currentstore.ContentModelRequest,
		rewrittenRequest,
	)
	compilation, err := corecontract.RestoreContextCompilationV1(compilationCanonical)
	if err != nil {
		t.Fatal(err)
	}
	compilation.FinalRequestDigest = rewrittenRequestRef
	_, rewrittenCompilation, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil {
		t.Fatal(err)
	}
	rewrittenCompilationRef := insertCompositeContent(
		t,
		database,
		currentstore.ContentContextCompilation,
		rewrittenCompilation,
	)
	execClosedFileTamperV1(
		t,
		database,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`
		UPDATE model_dispatch_attempts
		SET request_ref=?, request_digest=?, parameters_json=?,
			context_compilation_ref=?
		WHERE attempt_id=?
	`,
		rewrittenRequestRef,
		rewrittenRequestRef,
		request.Parameters,
		rewrittenCompilationRef,
		attemptID,
	)
}

func tamperCompositeRootReviewerEvidence(
	t *testing.T,
	fixture compositeBackupFixture,
) {
	t.Helper()
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var attemptID, compilationRef string
	if err := database.QueryRow(`
		SELECT attempt_id, context_compilation_ref
		FROM model_dispatch_attempts WHERE run_id=?
	`, fixture.rootRunID).Scan(&attemptID, &compilationRef); err != nil {
		t.Fatal(err)
	}
	var canonical []byte
	if err := database.QueryRow(`
		SELECT canonical_bytes FROM content_records WHERE content_digest=?
	`, compilationRef).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if compilation.Composite == nil || compilation.Composite.ReviewVerdict == nil {
		t.Fatal("root compilation lacks Reviewer evidence")
	}
	detached := strings.Repeat("0", 64)
	if detached == compilation.Composite.ReviewVerdict.MemberSnapshotDigest {
		detached = strings.Repeat("1", 64)
	}
	compilation.Composite.ReviewVerdict.MemberSnapshotDigest = detached
	_, rewritten, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil {
		t.Fatal(err)
	}
	rewrittenRef := insertCompositeContent(
		t,
		database,
		currentstore.ContentContextCompilation,
		rewritten,
	)
	execClosedFileTamperV1(
		t,
		database,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`
		UPDATE model_dispatch_attempts
		SET context_compilation_ref=? WHERE attempt_id=?
	`,
		rewrittenRef,
		attemptID,
	)
}

func insertCompositeContent(
	t *testing.T,
	database *sql.DB,
	kind currentstore.ContentKind,
	canonical []byte,
) string {
	t.Helper()
	digest, err := currentstore.ComputeContentDigest(
		kind,
		coreJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type,
			canonical_bytes, size_bytes, created_at
		) VALUES(?, ?, ?, ?, ?, ?)
	`, digest, string(kind), coreJSONMediaType, canonical, len(canonical),
		time.Now().UTC().UnixMicro()); err != nil {
		t.Fatal(err)
	}
	return digest
}

func tamperCoreFailureReason(t *testing.T, fixture compositeBackupFixture) {
	t.Helper()
	reason := corecontract.CompositeChildResultOverBudgetReasonV1
	continuation, err := corecontract.NewCoreFailureLoopContinuationV1(reason)
	if err != nil {
		t.Fatal(err)
	}
	_, canonical, err := corecontract.NewCoreDeterministicFailureEventV1(
		fixture.rootRunID,
		reason,
	)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := currentstore.ComputeContentDigest(
		currentstore.ContentRunEventPayload,
		coreJSONMediaType,
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, insertErr := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, 'RUN_EVENT_PAYLOAD', 'application/json', ?, ?, ?)
	`, ref, canonical, len(canonical), time.Now().UTC().UnixMicro())
	_, frameErr := database.Exec(`
		UPDATE loop_frames SET continuation=? WHERE run_id=?
	`, continuation, fixture.rootRunID)
	execClosedFileTamperV1(
		t,
		database,
		[]string{"run_events_reject_update"},
		`
		UPDATE run_events SET payload_ref=?, payload_digest=?
		WHERE run_id=? AND event_sequence=1
	`,
		ref,
		ref,
		fixture.rootRunID,
	)
	closeErr := database.Close()
	if err := errors.Join(insertErr, frameErr, closeErr); err != nil {
		t.Fatal(err)
	}
}

func tamperCoreFailureEventKind(t *testing.T, fixture compositeBackupFixture) {
	t.Helper()
	mustCompositeClosedFileExec(
		t,
		fixture.base.databasePath,
		[]string{"run_events_reject_update"},
		`
		UPDATE run_events SET event_kind='RUN_ADMITTED'
		WHERE run_id=? AND event_sequence=1
	`,
		fixture.rootRunID,
	)
}

func tamperCoreFailurePayloadRef(t *testing.T, fixture compositeBackupFixture) {
	t.Helper()
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var admittedRef string
	queryErr := database.QueryRow(`
		SELECT payload_ref FROM run_events
		WHERE run_id=? AND event_sequence=0
	`, fixture.rootRunID).Scan(&admittedRef)
	execClosedFileTamperV1(
		t,
		database,
		[]string{"run_events_reject_update"},
		`
		UPDATE run_events SET payload_ref=?, payload_digest=?
		WHERE run_id=? AND event_sequence=1
	`,
		admittedRef,
		admittedRef,
		fixture.rootRunID,
	)
	closeErr := database.Close()
	if err := errors.Join(queryErr, closeErr); err != nil {
		t.Fatal(err)
	}
}

func tamperCoreFailureCanonical(t *testing.T, fixture compositeBackupFixture) {
	t.Helper()
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var canonical []byte
	queryErr := database.QueryRow(`
		SELECT content.canonical_bytes
		FROM run_events AS event
		JOIN content_records AS content
		  ON content.content_digest=event.payload_ref
		WHERE event.run_id=? AND event.event_sequence=1
	`, fixture.rootRunID).Scan(&canonical)
	if len(canonical) == 0 || canonical[len(canonical)-1] != '}' {
		_ = database.Close()
		t.Fatalf("unexpected deterministic failure canonical=%q", canonical)
	}
	tamperedCanonical := append([]byte(nil), canonical[:len(canonical)-1]...)
	tamperedCanonical = append(tamperedCanonical, []byte(`,"tampered":true}`)...)
	ref, digestErr := currentstore.ComputeContentDigest(
		currentstore.ContentRunEventPayload,
		coreJSONMediaType,
		tamperedCanonical,
	)
	_, insertErr := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, 'RUN_EVENT_PAYLOAD', 'application/json', ?, ?, ?)
	`, ref, tamperedCanonical, len(tamperedCanonical), time.Now().UTC().UnixMicro())
	execClosedFileTamperV1(
		t,
		database,
		[]string{"run_events_reject_update"},
		`
		UPDATE run_events SET payload_ref=?, payload_digest=?
		WHERE run_id=? AND event_sequence=1
	`,
		ref,
		ref,
		fixture.rootRunID,
	)
	closeErr := database.Close()
	if err := errors.Join(queryErr, digestErr, insertErr, closeErr); err != nil {
		t.Fatal(err)
	}
}

// tamperCompositeCompilationEvidence keeps the compilation record and its
// content-addressed reference internally valid while detaching it from the
// immutable Child Manifest. Generic digest validation alone must not accept
// this jointly rewritten record.
func tamperCompositeCompilationEvidence(
	t *testing.T,
	fixture compositeBackupFixture,
) {
	t.Helper()
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var compilationRef string
	if err := database.QueryRow(`
		SELECT context_compilation_ref
		FROM model_dispatch_attempts
		WHERE run_id=?
	`, fixture.rootRunID).Scan(&compilationRef); err != nil {
		t.Fatal(err)
	}
	var canonical []byte
	if err := database.QueryRow(`
		SELECT canonical_bytes
		FROM content_records
		WHERE content_digest=?
	`, compilationRef).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if compilation.Composite == nil || len(compilation.Composite.ChildResults) == 0 {
		t.Fatal("root compilation has no Composite Child evidence")
	}
	detachedDigest := strings.Repeat("f", 64)
	if detachedDigest == compilation.Composite.ChildResults[0].ChildManifestDigest {
		detachedDigest = strings.Repeat("e", 64)
	}
	compilation.Composite.ChildResults[0].ChildManifestDigest = detachedDigest
	_, rewritten, err := corecontract.NewContextCompilationV1(compilation)
	if err != nil {
		t.Fatalf("freeze detached Composite compilation: %v", err)
	}
	rewrittenRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentContextCompilation,
		coreJSONMediaType,
		rewritten,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, insertErr := database.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, 'CONTEXT_COMPILATION', 'application/json', ?, ?, ?)
	`, rewrittenRef, rewritten, len(rewritten), time.Now().UTC().UnixMicro())
	execClosedFileTamperV1(
		t,
		database,
		[]string{"model_dispatch_attempts_observation_update_guard"},
		`
		UPDATE model_dispatch_attempts
		SET context_compilation_ref=?
		WHERE run_id=?
	`,
		rewrittenRef,
		fixture.rootRunID,
	)
	if insertErr != nil {
		t.Fatal(insertErr)
	}
}

func assertCompositeRawDispatchRejected(t *testing.T, fixture compositeBackupFixture) {
	t.Helper()
	const logicalStepID = "composite.tampered.extra/v1"
	database, err := sql.Open("sqlite", fixture.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var memberID string
	if err := database.QueryRow(`
		SELECT member_id FROM member_execution_snapshots WHERE run_id=?
	`, fixture.childRunIDs[0]).Scan(&memberID); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	operationKey, err := corecontract.ModelLogicalOperationKey(
		fixture.childRunIDs[0], memberID, logicalStepID,
	)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	const attemptID = "composite-tampered-extra-attempt"
	_, insertAttemptErr := database.Exec(`
		INSERT INTO model_dispatch_attempts(
			attempt_id, logical_operation_key, run_id, member_id,
			logical_step_id, frame_revision, member_snapshot_digest,
			binding_json, context_compilation_ref, request_ref, request_digest,
			provider, model, parameters_json, deadline, usage_ledger_ref,
			source_dispatch_attempt_id,
			state, provider_request_id, provider_receipt_ref, result_ref,
			error_classification, reconciliation_evidence_ref, unknown_reason,
			revision, created_at, updated_at
		)
		SELECT
			?, ?, run_id, member_id, ?, frame_revision, member_snapshot_digest,
			binding_json, context_compilation_ref, request_ref, request_digest,
			provider, model, parameters_json, deadline, usage_ledger_ref, NULL,
			'PENDING', NULL, NULL, NULL, NULL, NULL, NULL,
			0, created_at+1, updated_at+1
		FROM model_dispatch_attempts
		WHERE run_id=?
	`, attemptID, operationKey, logicalStepID, fixture.childRunIDs[0])
	closeErr := database.Close()
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if insertAttemptErr == nil {
		t.Fatal("schema accepted a raw Composite Model Attempt without its observation closure")
	}
}

func mustCompositeExec(
	t *testing.T,
	databasePath string,
	statement string,
	arguments ...any,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	execClosedFileTamperV1(t, database, nil, statement, arguments...)
	closeErr := database.Close()
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func mustCompositeClosedFileExec(
	t *testing.T,
	databasePath string,
	triggers []string,
	statement string,
	arguments ...any,
) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	execClosedFileTamperV1(t, database, triggers, statement, arguments...)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func findStartupRecoveryRun(
	runs []currentstore.StartupRecoveryRun,
	runID string,
) (currentstore.StartupRecoveryRun, bool) {
	for _, run := range runs {
		if run.RunID == runID {
			return run, true
		}
	}
	return currentstore.StartupRecoveryRun{}, false
}

func readCompositeRunRevision(
	t *testing.T,
	databasePath string,
	runID string,
) uint64 {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	queryErr := database.QueryRow(`SELECT revision FROM runs WHERE run_id=?`, runID).Scan(&revision)
	closeErr := database.Close()
	if err := errors.Join(queryErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if revision < 0 {
		t.Fatalf("Run %q revision=%d", runID, revision)
	}
	return uint64(revision)
}

func readCompositeFrameAndEventRevision(
	t *testing.T,
	databasePath string,
	runID string,
) (uint64, uint64) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var frameRevision, eventRevision int64
	queryErr := database.QueryRow(`
		SELECT frame.frame_revision, MAX(event.to_revision)
		FROM loop_frames AS frame
		JOIN run_events AS event ON event.run_id=frame.run_id
		WHERE frame.run_id=?
		GROUP BY frame.frame_revision
	`, runID).Scan(&frameRevision, &eventRevision)
	closeErr := database.Close()
	if err := errors.Join(queryErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if frameRevision < 0 || eventRevision < 0 {
		t.Fatalf(
			"Run %q frame/event revisions=%d/%d",
			runID,
			frameRevision,
			eventRevision,
		)
	}
	return uint64(frameRevision), uint64(eventRevision)
}
