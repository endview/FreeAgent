package currentstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controloverview"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestLoadControlOverviewV1BoundedDeterministicAndSafe(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	type runOrderV1 struct {
		id      string
		updated uint64
	}
	allRuns := make([]runOrderV1, 0, 14)
	for index := 0; index < 14; index++ {
		intent := fixture.intent
		intent.AdmissionKey = fmt.Sprintf("overview-admission-%02d", index)
		_, canonical, digest, err := corecontract.NewAdmissionIntentV1(intent)
		if err != nil {
			t.Fatalf("intent %d: %v", index, err)
		}
		runID := fmt.Sprintf("overview-run-%02d", index)
		if _, err := fixture.store.CommitRunAdmission(
			context.Background(), fixture.compileInput(t, canonical, digest, runID),
		); err != nil {
			t.Fatalf("commit Run %d: %v", index, err)
		}
		var updated int64
		if err := fixture.store.db.QueryRow(
			`SELECT updated_at FROM run_observation_heads WHERE run_id=?`, runID,
		).Scan(&updated); err != nil || updated <= 0 {
			t.Fatalf("load Run %d ordering fact: %v", index, err)
		}
		allRuns = append(allRuns, runOrderV1{id: runID, updated: uint64(updated)})
	}
	sort.Slice(allRuns, func(i, j int) bool {
		if allRuns[i].updated != allRuns[j].updated {
			return allRuns[i].updated > allRuns[j].updated
		}
		return allRuns[i].id > allRuns[j].id
	})

	tenant, err := fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.intent.TenantID, "", controloverview.MaximumItemsV1,
	)
	if err != nil {
		t.Fatalf("tenant Overview: %v", err)
	}
	if len(tenant.Runs) != int(controloverview.MaximumItemsV1) || !tenant.RunsTruncated {
		t.Fatalf("tenant Runs=(%d,truncated=%v)", len(tenant.Runs), tenant.RunsTruncated)
	}
	if tenant.Runs[0].RunID != allRuns[0].id ||
		tenant.Runs[len(tenant.Runs)-1].RunID != allRuns[11].id {
		t.Fatalf("unexpected deterministic Run window: first=%q last=%q",
			tenant.Runs[0].RunID, tenant.Runs[len(tenant.Runs)-1].RunID)
	}
	if tenant.SourceUpdatedAtUnixMicros != allRuns[0].updated {
		t.Fatalf("source updated at=%d", tenant.SourceUpdatedAtUnixMicros)
	}
	if tenant.Workspaces == nil || tenant.Runs == nil || tenant.Unknown == nil ||
		tenant.Learning == nil || tenant.ModuleCandidates == nil || tenant.Usage == nil {
		t.Fatal("Overview collection is nil")
	}
	if len(tenant.Unknown) != 0 || tenant.UnknownTruncated || len(tenant.Usage) != 0 ||
		tenant.UsageTruncated {
		t.Fatalf("unavailable safe sections must be empty/non-truncated: %+v", tenant)
	}

	current, err := fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.intent.TenantID, fixture.intent.WorkspaceID,
		controloverview.MaximumItemsV1,
	)
	if err != nil {
		t.Fatalf("current Workspace Overview: %v", err)
	}
	if len(current.Learning) != 0 || current.LearningTruncated {
		t.Fatalf("Workspace Learning must remain unavailable: %+v", current.Learning)
	}
	for _, run := range current.Runs {
		if run.WorkspaceID != fixture.intent.WorkspaceID {
			t.Fatalf("cross-Workspace Run leaked: %+v", run)
		}
	}

	wire, err := json.Marshal(tenant)
	if err != nil {
		t.Fatal(err)
	}
	text := string(wire)
	for _, forbidden := range []string{
		"canonical", "prompt", "result_ref", "raw_receipt", "authority",
		"provider_request", "package_path", "source_policy",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("sensitive field %q present in Overview wire: %s", forbidden, text)
		}
	}
}

func TestLoadControlOverviewV1TamperedRunIsTypedIntegrity(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	if _, err := fixture.store.CommitRunAdmission(context.Background(), fixture.input); err != nil {
		t.Fatal(err)
	}
	execOverviewTamperV1(t, fixture.store,
		"",
		`UPDATE runs SET state='TERMINATED',disposition='TERMINATED' WHERE run_id='run-admitted'`,
	)
	_, err := fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.intent.TenantID, "", controloverview.MaximumItemsV1,
	)
	if !errors.Is(err, controloverview.ErrIntegrity) {
		t.Fatalf("tampered Overview error=%v", err)
	}
}

func TestLoadControlOverviewV1RejectsCoordinatedFakeTerminalRun(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	if _, err := fixture.store.CommitRunAdmission(context.Background(), fixture.input); err != nil {
		t.Fatal(err)
	}
	continuation, err := corecontract.NewCoreFailureLoopContinuationV1(
		corecontract.AllRequiredChildFailedReasonV1,
	)
	if err != nil {
		t.Fatal(err)
	}
	execOverviewTamperV1(t, fixture.store,
		"",
		`UPDATE runs SET state='TERMINATED',disposition='TERMINATED' WHERE run_id='run-admitted'`,
	)
	if _, err := fixture.store.db.Exec(
		`UPDATE loop_frames SET step='TERMINATED',continuation=? WHERE run_id='run-admitted'`,
		continuation,
	); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.intent.TenantID, "", controloverview.MaximumItemsV1,
	)
	if !errors.Is(err, controloverview.ErrIntegrity) {
		t.Fatalf("fake terminal Overview error=%v", err)
	}
}

func TestLoadControlOverviewV1RejectsPureModelRunForgedReadyAfterAction(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	if _, err := fixture.store.CommitRunAdmission(context.Background(), fixture.input); err != nil {
		t.Fatal(err)
	}
	continuation, err := corecontract.NewLoopContinuationForAttemptV1(
		corecontract.ModelReadyAfterActionLoopStep,
		corecontract.AttemptKindAction,
		"forged-action-logical-step",
		"forged-action-attempt",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.Exec(
		`UPDATE loop_frames
		 SET step='MODEL_READY_AFTER_ACTION',continuation=?,
		     pending_attempt_id=NULL,pending_dispatch_attempt_id=NULL
		 WHERE run_id='run-admitted'`,
		continuation,
	); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.intent.TenantID, "", controloverview.MaximumItemsV1,
	)
	if !errors.Is(err, controloverview.ErrIntegrity) {
		t.Fatalf("forged pure Model MODEL_READY_AFTER_ACTION error=%v", err)
	}
}

func TestLoadControlOverviewV1RejectsHiddenDisabledDispatchFamilies(t *testing.T) {
	for _, kind := range []string{"ACTION", "CHANNEL_SEND"} {
		t.Run("pure Model "+kind, func(t *testing.T) {
			fixture := newAdmissionCommitFixture(t)
			if _, err := fixture.store.CommitRunAdmission(
				context.Background(), fixture.input,
			); err != nil {
				t.Fatal(err)
			}
			insertHiddenDisabledDispatchAttemptV1(
				t, fixture.store, kind, "run-admitted",
				fixture.intent.TenantID, fixture.intent.WorkspaceID, "member-primary",
			)
			_, err := fixture.store.LoadControlOverviewV1(
				context.Background(), fixture.intent.TenantID, "",
				controloverview.MaximumItemsV1,
			)
			if !errors.Is(err, controloverview.ErrIntegrity) {
				t.Fatalf("hidden disabled %s error=%v", kind, err)
			}
		})
	}

	t.Run("Action-only hidden Channel", func(t *testing.T) {
		harness := newActionStoreHarness(t)
		begin := harness.beginAction(t, "overview-action-before-hidden-channel")
		attempt := begin.Action.Attempt
		insertHiddenDisabledDispatchAttemptV1(
			t, harness.store, "CHANNEL_SEND", attempt.RunID,
			attempt.TenantID, attempt.WorkspaceID, attempt.MemberID,
		)
		_, err := harness.store.LoadControlOverviewV1(
			context.Background(), attempt.TenantID, attempt.WorkspaceID,
			controloverview.MaximumItemsV1,
		)
		if !errors.Is(err, controloverview.ErrIntegrity) {
			t.Fatalf("hidden disabled Channel error=%v", err)
		}
	})
}

func insertHiddenDisabledDispatchAttemptV1(
	t *testing.T,
	store *Store,
	kind, runID, tenantID, workspaceID, memberID string,
) {
	t.Helper()
	ctx := context.Background()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
			t.Errorf("restore foreign_keys: %v", err)
		}
	}()
	var publicActionID, providerActionID, definitionDigest, proposalRef any
	var channelEndpointID, channelIngressKey, channelProposalRef any
	keyDigit := "a"
	if kind == "ACTION" {
		publicActionID = "hidden.action"
		providerActionID = "hidden.action.provider"
		definitionDigest = strings.Repeat("c", 64)
		proposalRef = strings.Repeat("d", 64)
	} else {
		keyDigit = "b"
		channelEndpointID = "hidden.channel"
		channelIngressKey = strings.Repeat("e", 64)
		channelProposalRef = strings.Repeat("f", 64)
	}
	_, err = connection.ExecContext(ctx, `INSERT INTO dispatch_attempts(
		attempt_id,dispatch_kind,logical_operation_key,run_id,tenant_id,workspace_id,
		member_id,logical_step_id,source_model_attempt_id,frame_revision,
		member_snapshot_digest,binding_index,binding_json,
		public_action_id,provider_action_id,definition_digest,proposal_ref,
		channel_endpoint_id,channel_ingress_key,channel_proposal_ref,
		effect_class,max_result_bytes,deadline,usage_ledger_ref,state,
		revision,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"hidden-disabled-"+strings.ToLower(kind), kind, strings.Repeat(keyDigit, 64),
		runID, tenantID, workspaceID, memberID, "hidden-disabled-step-"+strings.ToLower(kind),
		"hidden-disabled-model-"+strings.ToLower(kind), 0, strings.Repeat("9", 64),
		0, []byte(`{}`), publicActionID, providerActionID, definitionDigest, proposalRef,
		channelEndpointID, channelIngressKey, channelProposalRef,
		"none", 1, 1, "hidden-budget", "PENDING", 0, 1, 1,
	)
	if err != nil {
		t.Fatal(err)
	}
}

// execOverviewTamperV1 models a closed-file or out-of-process SQLite writer
// that has bypassed the process-owned schema guards. Production mutations can
// never drop these triggers or disable FKs; the point of these tests is to
// prove the bounded online semantic comparison still fails closed.
func execOverviewTamperV1(
	t *testing.T,
	store *Store,
	guard string,
	statement string,
	args ...any,
) {
	t.Helper()
	ctx := context.Background()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if guard != "" {
		if _, err := connection.ExecContext(ctx, `DROP TRIGGER `+guard); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := connection.ExecContext(ctx, statement, args...); err != nil {
		t.Fatal(err)
	}
}

func TestLoadControlOverviewV1AcceptsDormantRepairRun(t *testing.T) {
	fixture := newCommittedCompositeDecisionStoreFixture(t, 2)
	snapshot, err := fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.parentIntent.TenantID, "", controloverview.MaximumItemsV1,
	)
	if err != nil {
		t.Fatalf("dormant repair Overview: %v", err)
	}
	found := false
	for _, run := range snapshot.Runs {
		if run.RunID == fixture.compiled.Decision.RepairReviewer.RunManifest.RunID {
			found = true
			if run.State != corecontract.InitialRunState || run.Disposition != "WAITING_EXTERNAL" {
				t.Fatalf("dormant repair projection=%+v", run)
			}
		}
	}
	if !found {
		t.Fatal("dormant repair Run missing from Overview")
	}
}

func TestLoadControlOverviewV1ProjectsModelUnknownAndSafeUsage(t *testing.T) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(context.Background(), beginInput)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(), CommitModelDispatchOutcomeInput{
			Lease: begin.Lease, AttemptID: begin.Attempt.AttemptID,
			InvocationID: begin.Attempt.AttemptID, Provider: begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			ProviderRequestID:       "overview-provider-request",
			UnknownReason:           "must never be projected",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.intent.TenantID, fixture.intent.WorkspaceID,
		controloverview.MaximumItemsV1,
	)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(snapshot.Unknown) != 1 || snapshot.Unknown[0].Kind != controloverview.UnknownKindModelV1 ||
		snapshot.Unknown[0].ResourceID != unknown.Record.Attempt.AttemptID ||
		len(snapshot.Usage) != 1 || snapshot.Usage[0].AttemptID != unknown.Record.Attempt.AttemptID ||
		snapshot.Usage[0].UsageStatus != modelUsageStatusReconciliationPending {
		t.Fatalf("unknown=%+v usage=%+v", snapshot.Unknown, snapshot.Usage)
	}
	wire, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"overview-provider-request", "must never be projected", "provider_request_id", "unknown_reason"} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatalf("sensitive %q leaked: %s", forbidden, wire)
		}
	}
}

func TestLoadControlOverviewV1RejectsAttemptScopeProjectionTamper(t *testing.T) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(context.Background(), beginInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(), CommitModelDispatchOutcomeInput{
			Lease: begin.Lease, AttemptID: begin.Attempt.AttemptID,
			InvocationID: begin.Attempt.AttemptID, Provider: begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown, UnknownReason: "fixture timeout",
		},
	); err != nil {
		t.Fatal(err)
	}
	execOverviewTamperV1(t, fixture.store,
		"model_dispatch_attempts_observation_update_guard",
		`UPDATE model_dispatch_attempts SET workspace_id='workspace-tampered'
		 WHERE attempt_id=?`, begin.Attempt.AttemptID,
	)
	_, err = fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.intent.TenantID, "",
		controloverview.MaximumItemsV1,
	)
	if !errors.Is(err, controloverview.ErrIntegrity) {
		t.Fatalf("Attempt scope tamper error=%v", err)
	}
}

func TestLoadControlOverviewV1RejectsUsageSemanticTamper(t *testing.T) {
	fixture, _, beginInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(context.Background(), beginInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(), CommitModelDispatchOutcomeInput{
			Lease: begin.Lease, AttemptID: begin.Attempt.AttemptID,
			InvocationID: begin.Attempt.AttemptID, Provider: begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown, UnknownReason: "fixture timeout",
		},
	); err != nil {
		t.Fatal(err)
	}
	execOverviewTamperV1(t, fixture.store,
		"model_usage_observation_update_guard",
		`UPDATE model_usage SET usage_status='NO_USAGE_REPORTED'
		 WHERE attempt_id=?`, begin.Attempt.AttemptID,
	)
	_, err = fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.intent.TenantID, fixture.intent.WorkspaceID,
		controloverview.MaximumItemsV1,
	)
	if !errors.Is(err, controloverview.ErrIntegrity) {
		t.Fatalf("Usage semantic tamper error=%v", err)
	}
}

func TestLoadControlOverviewV1ProjectsSemanticallyClosedLearningReview(t *testing.T) {
	fixture := newLearningReviewStoreFixture(t, json.RawMessage(`{"max_tokens":512}`))
	fixture.commitAdmission(t)
	begin := fixture.beginAttempt(t)
	fixture.commitAttemptOutcome(t, begin, corecontract.ModelAttemptUnknown, nil, false, nil)
	finalized, err := fixture.store.FinalizeLearningReview(
		context.Background(), FinalizeLearningReviewInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.proposal.Proposal.TenantID,
		fixture.proposal.Proposal.Workspace.ID, controloverview.MaximumItemsV1,
	)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(snapshot.Learning) != 1 ||
		snapshot.Learning[0].ProposalID != finalized.Proposal.ProposalID ||
		snapshot.Learning[0].State != string(LearningProposalReviewUnknown) {
		t.Fatalf("Learning=%+v", snapshot.Learning)
	}
	foundProposalUnknown := false
	for _, item := range snapshot.Unknown {
		if item.Kind == controloverview.UnknownKindLearningProposalV1 &&
			item.ResourceID == finalized.Proposal.ProposalID {
			foundProposalUnknown = true
		}
	}
	if !foundProposalUnknown {
		t.Fatalf("UNKNOWN=%+v", snapshot.Unknown)
	}
}

func TestLoadControlOverviewV1RejectsLearningFakeApproval(t *testing.T) {
	fixture := newLearningReviewStoreFixture(t, json.RawMessage(`{"max_tokens":512}`))
	fixture.commitAdmission(t)
	begin := fixture.beginAttempt(t)
	fixture.commitAttemptOutcome(t, begin, corecontract.ModelAttemptUnknown, nil, false, nil)
	finalized, err := fixture.store.FinalizeLearningReview(
		context.Background(), FinalizeLearningReviewInput{
			TenantID:                 fixture.proposal.Proposal.TenantID,
			ProposalID:               fixture.proposal.ProposalID,
			ExpectedProposalRevision: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if finalized.Proposal.State != LearningProposalReviewUnknown {
		t.Fatalf("fixture state=%q", finalized.Proposal.State)
	}
	execOverviewTamperV1(t, fixture.store,
		"learning_proposals_observation_update_guard",
		`UPDATE learning_proposals SET state='APPROVED' WHERE proposal_id=?`,
		finalized.Proposal.ProposalID,
	)
	_, err = fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.proposal.Proposal.TenantID,
		fixture.proposal.Proposal.Workspace.ID, controloverview.MaximumItemsV1,
	)
	if !errors.Is(err, controloverview.ErrIntegrity) {
		t.Fatalf("fake Learning approval error=%v", err)
	}
}

func TestLoadControlOverviewV1ProjectsSemanticallyClosedLearningTaskUnknown(t *testing.T) {
	fixture := newLearningCycleExecutionFixture(
		t, learningcontract.ProposalKindSkillV1,
		json.RawMessage(`{"max_tokens":512}`), 512, nil,
	)
	fixture.admit(t)
	begin := fixture.review.beginAttempt(t)
	fixture.review.commitAttemptOutcome(
		t, begin, corecontract.ModelAttemptUnknown, nil, false, nil,
	)
	projected, err := fixture.store.FinalizeLearningCycleTask(
		context.Background(), FinalizeLearningCycleTaskInput{
			TenantID: fixture.task.TenantID, TaskID: fixture.task.TaskID,
			ExpectedTaskRevision: 1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.store.LoadControlOverviewV1(
		context.Background(), fixture.task.TenantID, fixture.task.WorkspaceID,
		controloverview.MaximumItemsV1,
	)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	found := false
	for _, item := range snapshot.Unknown {
		if item.Kind == controloverview.UnknownKindLearningTaskV1 &&
			item.ResourceID == projected.Task.TaskID && item.RunID == projected.Task.RunID {
			found = true
		}
	}
	if !found {
		t.Fatalf("UNKNOWN=%+v", snapshot.Unknown)
	}
}

func TestLoadControlOverviewV1ProjectsActionAndChannelUnknown(t *testing.T) {
	t.Run("Action", func(t *testing.T) {
		harness := newActionStoreHarness(t)
		begin := harness.beginAction(t, "overview-action-unknown")
		unknown, err := harness.store.CommitActionDispatchOutcome(
			context.Background(), actionOutcomeInput(
				begin.Action, begin.Lease, moduleapi.ActionExecutionUnknown,
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		attempt := unknown.Record.Attempt
		snapshot, err := harness.store.LoadControlOverviewV1(
			context.Background(), attempt.TenantID, attempt.WorkspaceID,
			controloverview.MaximumItemsV1,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !containsOverviewUnknownV1(snapshot.Unknown,
			controloverview.UnknownKindActionV1, attempt.AttemptID) {
			t.Fatalf("UNKNOWN=%+v", snapshot.Unknown)
		}
	})
	t.Run("Channel", func(t *testing.T) {
		harness := newChannelDispatchHarness(t, "run-overview-channel-unknown", "event-overview-channel-unknown")
		begin := harness.mustBegin(t)
		unknown, err := harness.store.CommitChannelDispatchOutcome(
			context.Background(), channelOutcomeFixture(begin, moduleapi.ChannelExecutionUnknown),
		)
		if err != nil {
			t.Fatal(err)
		}
		attempt := unknown.Record.Attempt
		snapshot, err := harness.store.LoadControlOverviewV1(
			context.Background(), attempt.TenantID, attempt.WorkspaceID,
			controloverview.MaximumItemsV1,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !containsOverviewUnknownV1(snapshot.Unknown,
			controloverview.UnknownKindChannelSendV1, attempt.AttemptID) {
			t.Fatalf("UNKNOWN=%+v", snapshot.Unknown)
		}
	})
}

func containsOverviewUnknownV1(
	items []controloverview.UnknownV1,
	kind, resourceID string,
) bool {
	for _, item := range items {
		if item.Kind == kind && item.ResourceID == resourceID {
			return true
		}
	}
	return false
}

func TestControlOverviewRecentQueriesUseCoveringOrderIndexes(t *testing.T) {
	fixture := newAdmissionCommitFixture(t)
	tests := []struct {
		name, index, query string
		args               []any
	}{
		{
			name: "Runs tenant", index: "run_observation_heads_overview_tenant_recent_idx",
			query: `SELECT tenant_id,workspace_id,run_id,run_state,run_revision,created_at,updated_at
				FROM run_observation_heads INDEXED BY run_observation_heads_overview_tenant_recent_idx
				WHERE tenant_id=? ORDER BY updated_at DESC,run_id COLLATE BINARY DESC LIMIT ?`,
			args: []any{fixture.intent.TenantID, 13},
		},
		{
			name: "Runs Workspace", index: "run_observation_heads_overview_workspace_recent_idx",
			query: `SELECT tenant_id,workspace_id,run_id,run_state,run_revision,created_at,updated_at
				FROM run_observation_heads INDEXED BY run_observation_heads_overview_workspace_recent_idx
				WHERE tenant_id=? AND workspace_id=?
				ORDER BY updated_at DESC,run_id COLLATE BINARY DESC LIMIT ?`,
			args: []any{fixture.intent.TenantID, fixture.intent.WorkspaceID, 13},
		},
		{
			name: "UNKNOWN tenant", index: "overview_resource_heads_tenant_state_recent_idx",
			query: `SELECT resource_id,updated_at FROM overview_resource_heads
				INDEXED BY overview_resource_heads_tenant_state_recent_idx
				WHERE resource_kind=? AND tenant_id=? AND state=?
				ORDER BY updated_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`,
			args: []any{overviewResourceModelV1, fixture.intent.TenantID, "MODEL_UNKNOWN", 13},
		},
		{
			name: "UNKNOWN Workspace", index: "overview_resource_heads_workspace_state_recent_idx",
			query: `SELECT resource_id,updated_at FROM overview_resource_heads
				INDEXED BY overview_resource_heads_workspace_state_recent_idx
				WHERE resource_kind=? AND tenant_id=? AND workspace_id=? AND state=?
				ORDER BY updated_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`,
			args: []any{overviewResourceModelV1, fixture.intent.TenantID,
				fixture.intent.WorkspaceID, "MODEL_UNKNOWN", 13},
		},
		{
			name: "Learning and Usage tenant", index: "overview_resource_heads_tenant_recent_idx",
			query: `SELECT resource_id FROM overview_resource_heads
				INDEXED BY overview_resource_heads_tenant_recent_idx
				WHERE resource_kind=? AND tenant_id=?
				ORDER BY updated_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`,
			args: []any{overviewResourceLearningProposalV1, fixture.intent.TenantID, 13},
		},
		{
			name: "Learning and Usage Workspace", index: "overview_resource_heads_workspace_recent_idx",
			query: `SELECT resource_id FROM overview_resource_heads
				INDEXED BY overview_resource_heads_workspace_recent_idx
				WHERE resource_kind=? AND tenant_id=? AND workspace_id=?
				ORDER BY updated_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`,
			args: []any{overviewResourceLearningProposalV1, fixture.intent.TenantID,
				fixture.intent.WorkspaceID, 13},
		},
		{
			name: "Module tenant", index: "overview_resource_heads_module_tenant_recent_idx",
			query: `SELECT resource_id FROM overview_resource_heads
				INDEXED BY overview_resource_heads_module_tenant_recent_idx
				WHERE resource_kind='MODULE_REVIEW' AND tenant_id=?
				ORDER BY created_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`,
			args: []any{fixture.intent.TenantID, 13},
		},
		{
			name: "Module Workspace", index: "overview_resource_heads_module_workspace_recent_idx",
			query: `SELECT resource_id FROM overview_resource_heads
				INDEXED BY overview_resource_heads_module_workspace_recent_idx
				WHERE resource_kind='MODULE_REVIEW' AND tenant_id=?
				AND binding_target_kind='WORKSPACE_CHANNEL_ENDPOINT' AND workspace_id=?
				ORDER BY created_at DESC,resource_id COLLATE BINARY DESC LIMIT ?`,
			args: []any{fixture.intent.TenantID, fixture.intent.WorkspaceID, 13},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertOverviewQueryPlanV1(t, fixture.store.db, test.query, test.index, test.args...)
		})
	}
}

func TestControlOverviewOnlinePathUsesOnlyBoundedObservationProjections(t *testing.T) {
	overviewSource, err := os.ReadFile("control_overview.go")
	if err != nil {
		t.Fatal(err)
	}
	normalizedOverview := strings.ToLower(strings.Join(strings.Fields(string(overviewSource)), " "))
	for _, forbidden := range []string{
		"queryModelDispatchRecord", "queryActionDispatchRecord", "queryChannelDispatchRecord",
		"verifyLearningReviewProjection", "loadTerminalRunResult", "queryModuleUpgradeReview",
		"verifyLearningCycleTaskExecutionClosure", "loadAdmissionControlCatalog",
		"VerifyPublishedControlCatalogClosure",
	} {
		if strings.Contains(string(overviewSource), forbidden) {
			t.Fatalf("online Overview source contains forbidden unbounded/raw path %q", forbidden)
		}
	}
	for _, rawCandidate := range []string{
		"from runs ", "from learning_proposals ", "from model_dispatch_attempts ",
		"from dispatch_attempts ", "from learning_cycle_tasks ",
		"from module_upgrade_reviews ",
	} {
		if strings.Contains(normalizedOverview, rawCandidate) {
			t.Fatalf("online Overview source contains forbidden raw candidate table %q", rawCandidate)
		}
	}

	resourceSource, err := os.ReadFile("overview_resource_observation.go")
	if err != nil {
		t.Fatal(err)
	}
	const onlineStart = "func verifyOverviewResourceRawProjectionOnlineV1("
	const onlineEnd = "func overviewRunRefIDV1("
	start := strings.Index(string(resourceSource), onlineStart)
	end := strings.Index(string(resourceSource), onlineEnd)
	if start < 0 || end <= start {
		t.Fatal("cannot isolate online resource raw verifier")
	}
	online := string(resourceSource)[start:end]
	for _, forbidden := range []string{
		"queryModelDispatchRecord", "queryActionDispatchRecord", "queryChannelDispatchRecord",
		"queryLearningProposalByID", "queryLearningCycleTask", "queryModuleUpgradeReview",
		"version_canonical,", "SELECT version_canonical",
	} {
		if strings.Contains(online, forbidden) {
			t.Fatalf("online resource verifier contains forbidden large/generic path %q", forbidden)
		}
	}
}

func assertOverviewQueryPlanV1(
	t *testing.T,
	db *sql.DB,
	query, index string,
	args ...any,
) {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(details, "\n")
	upper := strings.ToUpper(plan)
	if !strings.Contains(plan, index) || !strings.Contains(upper, "SEARCH ") ||
		strings.Contains(upper, "SCAN ") || strings.Contains(upper, "USE TEMP B-TREE") {
		t.Fatalf("unsafe query plan:\n%s", plan)
	}
}
