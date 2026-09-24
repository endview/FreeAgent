package currentstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/memorycore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestSuccessfulModelOutcomeAtomicallyAdvancesAgentMemory(t *testing.T) {
	harness := newMemoryOutcomeHarness(t)
	run := harness.beginRun(
		t,
		"run-memory-success",
		"admission-memory-success",
		"attempt-memory-success",
		harness.primaryWorkspaceID,
		"backend backend request",
	)
	outcome := successfulMemoryOutcomeInput(
		t,
		run.begin,
		"backend backend backend result",
	)
	committed, err := harness.store.CommitModelDispatchOutcome(
		context.Background(),
		outcome,
	)
	if err != nil {
		t.Fatalf("CommitModelDispatchOutcome: %v", err)
	}
	if !committed.Applied {
		t.Fatal("first successful outcome was not applied")
	}

	head := currentMemoryHead(t, harness)
	if head.SnapshotRef.Revision != 2 ||
		head.SourceAttemptID != run.begin.Attempt.AttemptID ||
		head.Snapshot.PreviousSnapshotDigest != harness.genesisDigest {
		t.Fatalf("Memory head=%+v", head)
	}
	category := findMemoryEntry(
		t,
		head.Snapshot.Entries,
		moduleapi.MemoryEntryCategoryCount,
		"backend",
		harness.primaryWorkspaceID,
	)
	repeated := findMemoryEntry(
		t,
		head.Snapshot.Entries,
		moduleapi.MemoryEntryRepeatedTermCount,
		"backend",
		harness.primaryWorkspaceID,
	)
	summary := findMemoryEntry(
		t,
		head.Snapshot.Entries,
		moduleapi.MemoryEntryTaskSummary,
		run.begin.Attempt.AttemptID,
		harness.primaryWorkspaceID,
	)
	if category.Count != 1 || repeated.Count != 2 {
		t.Fatalf(
			"task-only counters category=%d repeated=%d",
			category.Count,
			repeated.Count,
		)
	}
	if len(summary.SourceRefs) != 2 ||
		!containsMemoryString(summary.SourceRefs, run.taskRef) ||
		!containsMemoryString(summary.SourceRefs, committed.Record.Attempt.ResultRef) {
		t.Fatalf("summary source closure=%v", summary.SourceRefs)
	}
	if len(summary.VisibleWorkspaceIDs) != 1 ||
		summary.VisibleWorkspaceIDs[0] != harness.primaryWorkspaceID {
		t.Fatalf("summary visibility=%v", summary.VisibleWorkspaceIDs)
	}
	if _, err := harness.store.GetContent(
		context.Background(),
		head.SnapshotRef.Digest,
	); err != nil {
		t.Fatalf("Memory snapshot ContentRecord: %v", err)
	}
}

func TestSmallUTF8SummaryBudgetCommitsWholeSuccessfulTerminalState(
	t *testing.T,
) {
	tests := []struct {
		name       string
		taskText   string
		resultText string
	}{
		{name: "CJK", taskText: "你", resultText: "好"},
		{name: "emoji", taskText: "😀", resultText: "🚀"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newMemoryOutcomeHarnessWithSummaryBytes(t, 4)
			run := harness.beginRun(
				t,
				"run-memory-small-summary-"+strings.ToLower(test.name),
				"admission-memory-small-summary-"+strings.ToLower(test.name),
				"attempt-memory-small-summary-"+strings.ToLower(test.name),
				harness.primaryWorkspaceID,
				test.taskText,
			)
			input := successfulMemoryOutcomeInput(t, run.begin, test.resultText)
			committed, err := harness.store.CommitModelDispatchOutcome(
				context.Background(),
				input,
			)
			if err != nil {
				t.Fatalf("CommitModelDispatchOutcome: %v", err)
			}
			if !committed.Applied ||
				committed.Record.Attempt.State != corecontract.ModelAttemptSucceeded {
				t.Fatalf("successful terminal result=%+v", committed)
			}
			head := currentMemoryHead(t, harness)
			if head.SnapshotRef.Revision != 2 ||
				head.SourceAttemptID != run.begin.Attempt.AttemptID {
				t.Fatalf("small-budget Memory head=%+v", head)
			}
			summary := findMemoryEntry(
				t,
				head.Snapshot.Entries,
				moduleapi.MemoryEntryTaskSummary,
				run.begin.Attempt.AttemptID,
				harness.primaryWorkspaceID,
			)
			if summary.Text == "" || !utf8.ValidString(summary.Text) ||
				len(summary.Text) > 4 {
				t.Fatalf(
					"terminal summary=%q bytes=%d",
					summary.Text,
					len(summary.Text),
				)
			}
			storedRun, err := harness.store.LoadRunForLoop(
				context.Background(),
				committed.Lease,
			)
			if err != nil {
				t.Fatal(err)
			}
			if storedRun.State != corecontract.TerminatedLoopStep ||
				storedRun.Frame.Step != corecontract.TerminatedLoopStep ||
				len(storedRun.History) != 1 ||
				storedRun.History[0].SourceAttemptID != run.begin.Attempt.AttemptID {
				t.Fatalf("terminal Run closure=%+v", storedRun)
			}
		})
	}
}

func TestFailedAndUnknownModelOutcomesDoNotAdvanceMemory(t *testing.T) {
	harness := newMemoryOutcomeHarness(t)
	failedRun := harness.beginRun(
		t,
		"run-memory-failed",
		"admission-memory-failed",
		"attempt-memory-failed",
		harness.primaryWorkspaceID,
		"backend failure",
	)
	failed, err := harness.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   failedRun.begin.Lease,
			AttemptID:               failedRun.begin.Attempt.AttemptID,
			InvocationID:            failedRun.begin.Attempt.AttemptID,
			Provider:                failedRun.begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: failedRun.begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptFailed,
			ErrorClassification:     "provider_error",
		},
	)
	if err != nil || failed.Record.Attempt.State != corecontract.ModelAttemptFailed {
		t.Fatalf("FAILED outcome=%+v error=%v", failed, err)
	}
	if got := currentMemoryHead(t, harness).SnapshotRef.Revision; got != 1 {
		t.Fatalf("FAILED advanced Memory to revision %d", got)
	}

	unknownRun := harness.beginRun(
		t,
		"run-memory-unknown",
		"admission-memory-unknown",
		"attempt-memory-unknown",
		harness.primaryWorkspaceID,
		"backend backend reconciliation",
	)
	requestID := "provider-" + unknownRun.begin.Attempt.AttemptID
	unknown, err := harness.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   unknownRun.begin.Lease,
			AttemptID:               unknownRun.begin.Attempt.AttemptID,
			InvocationID:            unknownRun.begin.Attempt.AttemptID,
			Provider:                unknownRun.begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: unknownRun.begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptUnknown,
			ProviderRequestID:       requestID,
			UnknownReason:           "response not observed",
		},
	)
	if err != nil || unknown.Record.Attempt.State != corecontract.ModelAttemptUnknown {
		t.Fatalf("UNKNOWN outcome=%+v error=%v", unknown, err)
	}
	if got := currentMemoryHead(t, harness).SnapshotRef.Revision; got != 1 {
		t.Fatalf("MODEL_UNKNOWN advanced Memory to revision %d", got)
	}

	reconciledInput := successfulMemoryOutcomeInputWithRevision(
		t,
		unknownRun.begin,
		unknown.Lease,
		unknown.Record.Attempt.Revision,
		"reconciled backend result",
	)
	reconciledInput.ReconciliationEvidenceCanonical = []byte(
		`{"kind":"provider_lookup","status":"succeeded"}`,
	)
	reconciled, err := harness.store.CommitModelDispatchOutcome(
		context.Background(),
		reconciledInput,
	)
	if err != nil || !reconciled.Applied {
		t.Fatalf("UNKNOWN->SUCCEEDED outcome=%+v error=%v", reconciled, err)
	}
	head := currentMemoryHead(t, harness)
	if head.SnapshotRef.Revision != 2 ||
		head.SourceAttemptID != unknownRun.begin.Attempt.AttemptID {
		t.Fatalf("reconciled Memory head=%+v", head)
	}
}

func TestSuccessfulModelOutcomeReplayVerifiesExactMemoryRevision(t *testing.T) {
	t.Run("replay after a later head", func(t *testing.T) {
		harness := newMemoryOutcomeHarness(t)
		firstRun := harness.beginRun(
			t,
			"run-memory-replay-first",
			"admission-memory-replay-first",
			"attempt-memory-replay-first",
			harness.primaryWorkspaceID,
			"backend backend first",
		)
		firstInput := successfulMemoryOutcomeInput(
			t,
			firstRun.begin,
			"first result",
		)
		first, err := harness.store.CommitModelDispatchOutcome(
			context.Background(),
			firstInput,
		)
		if err != nil {
			t.Fatal(err)
		}
		immediateReplay, err := harness.store.CommitModelDispatchOutcome(
			context.Background(),
			firstInput,
		)
		if err != nil || immediateReplay.Applied {
			t.Fatalf("immediate replay=%+v error=%v", immediateReplay, err)
		}
		if got := currentMemoryHead(t, harness).SnapshotRef.Revision; got != 2 {
			t.Fatalf("immediate replay advanced Memory to revision %d", got)
		}

		secondRun := harness.beginRun(
			t,
			"run-memory-replay-second",
			"admission-memory-replay-second",
			"attempt-memory-replay-second",
			harness.primaryWorkspaceID,
			"frontend frontend second",
		)
		if _, err := harness.store.CommitModelDispatchOutcome(
			context.Background(),
			successfulMemoryOutcomeInput(t, secondRun.begin, "second result"),
		); err != nil {
			t.Fatal(err)
		}
		if got := currentMemoryHead(t, harness).SnapshotRef.Revision; got != 3 {
			t.Fatalf("second success head revision=%d", got)
		}
		replayAfterLaterHead, err := harness.store.CommitModelDispatchOutcome(
			context.Background(),
			firstInput,
		)
		if err != nil || replayAfterLaterHead.Applied ||
			replayAfterLaterHead.Record.Attempt.ResultRef !=
				first.Record.Attempt.ResultRef {
			t.Fatalf(
				"replay after later head=%+v error=%v",
				replayAfterLaterHead,
				err,
			)
		}
		if got := currentMemoryHead(t, harness).SnapshotRef.Revision; got != 3 {
			t.Fatalf("later replay advanced Memory to revision %d", got)
		}
	})

	t.Run("missing source revision fails closed", func(t *testing.T) {
		harness := newMemoryOutcomeHarness(t)
		run := harness.beginRun(
			t,
			"run-memory-missing-replay",
			"admission-memory-missing-replay",
			"attempt-memory-missing-replay",
			harness.primaryWorkspaceID,
			"backend backend",
		)
		input := successfulMemoryOutcomeInput(t, run.begin, "result")
		if _, err := harness.store.CommitModelDispatchOutcome(
			context.Background(),
			input,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.store.db.Exec(`
			DELETE FROM agent_memory_revisions WHERE source_attempt_id=?
		`, run.begin.Attempt.AttemptID); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.store.CommitModelDispatchOutcome(
			context.Background(),
			input,
		); !errors.Is(err, ErrModelDispatchIntegrity) {
			t.Fatalf("replay without Memory closure error=%v", err)
		}
	})

	t.Run("different reproducible bytes fail closed", func(t *testing.T) {
		harness := newMemoryOutcomeHarness(t)
		run := harness.beginRun(
			t,
			"run-memory-byte-replay",
			"admission-memory-byte-replay",
			"attempt-memory-byte-replay",
			harness.primaryWorkspaceID,
			"backend backend",
		)
		input := successfulMemoryOutcomeInput(t, run.begin, "result")
		if _, err := harness.store.CommitModelDispatchOutcome(
			context.Background(),
			input,
		); err != nil {
			t.Fatal(err)
		}
		stored := currentMemoryHead(t, harness)
		_, alternateCanonical, err := moduleapi.NewAgentMemorySnapshotV1(
			moduleapi.AgentMemorySnapshotV1{
				SchemaVersion:          moduleapi.MemorySnapshotSchemaV1,
				TenantID:               stored.Snapshot.TenantID,
				AgentID:                stored.Snapshot.AgentID,
				Revision:               stored.Snapshot.Revision,
				PreviousSnapshotDigest: stored.Snapshot.PreviousSnapshotDigest,
				SourceAttemptID:        stored.SourceAttemptID,
				Entries:                []moduleapi.MemoryEntryV1{},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		alternateDigest, err := ComputeContentDigest(
			ContentMemorySnapshot,
			agentMemoryJSONMediaType,
			alternateCanonical,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := harness.store.PutContent(
			context.Background(),
			ContentInput{
				Digest:         alternateDigest,
				Kind:           ContentMemorySnapshot,
				MediaType:      agentMemoryJSONMediaType,
				CanonicalBytes: alternateCanonical,
			},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.store.db.Exec(`
			UPDATE agent_memory_revisions
			SET snapshot_ref=?
			WHERE source_attempt_id=?
		`, alternateDigest, run.begin.Attempt.AttemptID); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.store.CommitModelDispatchOutcome(
			context.Background(),
			input,
		); !errors.Is(err, ErrModelDispatchIntegrity) ||
			!strings.Contains(err.Error(), "cannot be reproduced byte-for-byte") {
			t.Fatalf("replay with different Memory bytes error=%v", err)
		}
	})
}

func TestConcurrentSuccessfulRunsRebaseOnCommitTimeMemoryHead(t *testing.T) {
	harness := newMemoryOutcomeHarness(t)
	firstRun := harness.beginRun(
		t,
		"run-memory-concurrent-a",
		"admission-memory-concurrent-a",
		"attempt-memory-concurrent-a",
		harness.primaryWorkspaceID,
		"backend backend first",
	)
	secondRun := harness.beginRun(
		t,
		"run-memory-concurrent-b",
		"admission-memory-concurrent-b",
		"attempt-memory-concurrent-b",
		harness.primaryWorkspaceID,
		"backend backend second",
	)
	inputs := []CommitModelDispatchOutcomeInput{
		successfulMemoryOutcomeInput(t, firstRun.begin, "backend in output must not count"),
		successfulMemoryOutcomeInput(t, secondRun.begin, "backend backend output must not count"),
	}
	type response struct {
		result CommitModelDispatchOutcomeResult
		err    error
	}
	responses := make(chan response, len(inputs))
	var workers sync.WaitGroup
	for _, input := range inputs {
		input := input
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := harness.store.CommitModelDispatchOutcome(
				context.Background(),
				input,
			)
			responses <- response{result: result, err: err}
		}()
	}
	workers.Wait()
	close(responses)
	for response := range responses {
		if response.err != nil || !response.result.Applied {
			t.Fatalf("concurrent commit=%+v error=%v", response.result, response.err)
		}
	}

	head := currentMemoryHead(t, harness)
	if head.SnapshotRef.Revision != 3 {
		t.Fatalf("concurrent Memory head=%+v", head.SnapshotRef)
	}
	revisionTwo, err := harness.store.GetAgentMemoryRevision(
		context.Background(),
		harness.tenantID,
		harness.agentID,
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if head.Snapshot.PreviousSnapshotDigest != revisionTwo.SnapshotRef.Digest {
		t.Fatalf(
			"revision 3 parent=%s want revision 2 digest=%s",
			head.Snapshot.PreviousSnapshotDigest,
			revisionTwo.SnapshotRef.Digest,
		)
	}
	sources := map[string]bool{
		revisionTwo.SourceAttemptID: true,
		head.SourceAttemptID:        true,
	}
	if !sources[firstRun.begin.Attempt.AttemptID] ||
		!sources[secondRun.begin.Attempt.AttemptID] {
		t.Fatalf("concurrent revision sources=%v", sources)
	}
	category := findMemoryEntry(
		t,
		head.Snapshot.Entries,
		moduleapi.MemoryEntryCategoryCount,
		"backend",
		harness.primaryWorkspaceID,
	)
	repeated := findMemoryEntry(
		t,
		head.Snapshot.Entries,
		moduleapi.MemoryEntryRepeatedTermCount,
		"backend",
		harness.primaryWorkspaceID,
	)
	if category.Count != 2 || repeated.Count != 4 {
		t.Fatalf(
			"rebased task-only counters category=%d repeated=%d",
			category.Count,
			repeated.Count,
		)
	}
}

func TestMemoryAppendFailureRollsBackWholeModelOutcome(t *testing.T) {
	harness := newMemoryOutcomeHarness(t)
	run := harness.beginRun(
		t,
		"run-memory-rollback",
		"admission-memory-rollback",
		"attempt-memory-rollback",
		harness.primaryWorkspaceID,
		"backend backend rollback",
	)
	before := modelMemoryAtomicCounts(t, harness.store)
	if _, err := harness.store.db.Exec(`
		CREATE TRIGGER fail_model_memory_append
		BEFORE INSERT ON agent_memory_revisions
		WHEN NEW.revision > 1
		BEGIN
			SELECT RAISE(ABORT, 'forced Memory append failure');
		END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.store.CommitModelDispatchOutcome(
		context.Background(),
		successfulMemoryOutcomeInput(t, run.begin, "rollback result"),
	); err == nil {
		t.Fatal("forced Memory append failure was accepted")
	}
	if after := modelMemoryAtomicCounts(t, harness.store); after != before {
		t.Fatalf("failed outcome changed rows before=%v after=%v", before, after)
	}
	record, err := harness.store.GetModelDispatchRecord(
		context.Background(),
		run.begin.Attempt.AttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if record.Attempt.State != corecontract.ModelAttemptPending ||
		record.Attempt.Revision != run.begin.Attempt.Revision ||
		record.Attempt.ResultRef != "" ||
		record.Usage.Revision != run.begin.Attempt.Revision {
		t.Fatalf("rolled-back Attempt/Usage=%+v", record)
	}
	if got := currentMemoryHead(t, harness).SnapshotRef.Revision; got != 1 {
		t.Fatalf("failed append left Memory at revision %d", got)
	}
}

func TestAgentMemoryKeepsWorkspaceDerivedStateIsolated(t *testing.T) {
	harness := newMemoryOutcomeHarness(t)
	primary := harness.beginRun(
		t,
		"run-memory-workspace-primary",
		"admission-memory-workspace-primary",
		"attempt-memory-workspace-primary",
		harness.primaryWorkspaceID,
		"backend backend primary",
	)
	if _, err := harness.store.CommitModelDispatchOutcome(
		context.Background(),
		successfulMemoryOutcomeInput(t, primary.begin, "primary result"),
	); err != nil {
		t.Fatal(err)
	}
	secondary := harness.beginRun(
		t,
		"run-memory-workspace-secondary",
		"admission-memory-workspace-secondary",
		"attempt-memory-workspace-secondary",
		harness.secondaryWorkspaceID,
		"backend backend secondary",
	)
	if _, err := harness.store.CommitModelDispatchOutcome(
		context.Background(),
		successfulMemoryOutcomeInput(t, secondary.begin, "secondary result"),
	); err != nil {
		t.Fatal(err)
	}

	head := currentMemoryHead(t, harness)
	if head.SnapshotRef.Revision != 3 {
		t.Fatalf("workspace-isolated head revision=%d", head.SnapshotRef.Revision)
	}
	for _, workspaceID := range []string{
		harness.primaryWorkspaceID,
		harness.secondaryWorkspaceID,
	} {
		category := findMemoryEntry(
			t,
			head.Snapshot.Entries,
			moduleapi.MemoryEntryCategoryCount,
			"backend",
			workspaceID,
		)
		repeated := findMemoryEntry(
			t,
			head.Snapshot.Entries,
			moduleapi.MemoryEntryRepeatedTermCount,
			"backend",
			workspaceID,
		)
		if category.Count != 1 || repeated.Count != 2 {
			t.Fatalf(
				"workspace %s counters category=%d repeated=%d",
				workspaceID,
				category.Count,
				repeated.Count,
			)
		}
	}
	for _, entry := range head.Snapshot.Entries {
		if len(entry.VisibleWorkspaceIDs) != 1 ||
			(entry.VisibleWorkspaceIDs[0] != harness.primaryWorkspaceID &&
				entry.VisibleWorkspaceIDs[0] != harness.secondaryWorkspaceID) {
			t.Fatalf("cross-Workspace Memory entry=%+v", entry)
		}
	}
}

type memoryOutcomeHarness struct {
	store                *Store
	fixture              *admissionCommitFixture
	tenantID             string
	agentID              string
	primaryWorkspaceID   string
	secondaryWorkspaceID string
	config               moduleapi.MemoryContextBindingV1
	configCanonical      []byte
	authority            moduleapi.MemoryAuthorityCeilingV1
	authorityCanonical   []byte
	contextPort          moduleapi.PortRef
	genesisDigest        string
}

type begunMemoryOutcomeRun struct {
	begin   BeginModelDispatchResult
	taskRef string
}

func newMemoryOutcomeHarness(t *testing.T) *memoryOutcomeHarness {
	return newMemoryOutcomeHarnessWithSummaryBytes(t, 64)
}

func newMemoryOutcomeHarnessWithSummaryBytes(
	t *testing.T,
	summaryMaxTextBytes uint32,
) *memoryOutcomeHarness {
	t.Helper()
	fixture := newAdmissionCommitFixtureWithModelProfile(t, 8192)
	control, err := controlcontract.RestoreControlSnapshot(
		fixture.controlCanonical,
		fixture.basis.Control,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		fixture.catalogCanonical,
		fixture.basis.Catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	contextPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	manifestCanonical := canonicalModuleManifest(
		t,
		"test.memory.outcome",
		"v1",
		moduleapi.RuntimeModeRequestTrustedInProcess,
		map[string]any{
			"provides": []any{map[string]any{
				"name":          contextPort.Name,
				"exact_version": contextPort.ExactVersion,
			}},
		},
	)
	installation, err := fixture.store.InstallModule(
		context.Background(),
		installInput(
			t,
			"installation-memory-outcome",
			manifestCanonical,
			strings.Repeat("7", 64),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := fixture.store.ActivateModule(
		context.Background(),
		ActivateModuleInput{
			ActivationID:       "activation-memory-outcome",
			TenantID:           control.TenantID,
			InstanceID:         "instance-memory-outcome",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "core.memory.adapter",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}
	config, parameters, _, err := moduleapi.NewMemoryContextBindingV1(
		moduleapi.MemoryContextBindingV1{
			SchemaVersion: moduleapi.MemoryContextBindingSchemaV1,
			Kinds: []moduleapi.MemoryEntryKindV1{
				moduleapi.MemoryEntryTaskSummary,
				moduleapi.MemoryEntryCategoryCount,
				moduleapi.MemoryEntryRepeatedTermCount,
			},
			MaxItems:          32,
			MaxTotalTextBytes: 4096,
			CategoryRules: []moduleapi.MemoryCategoryRuleV1{
				{Key: "backend", Terms: []string{"backend"}},
				{Key: "frontend", Terms: []string{"frontend"}},
			},
			StopTerms:           []string{},
			SummaryMaxTextBytes: summaryMaxTextBytes,
			EntryTTLSeconds:     86400,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, configCanonical, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementUntrustedData,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    parameters,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	configRef := putPublicationJSON(
		t,
		fixture.store,
		ContentConfig,
		configCanonical,
	)
	secondaryWorkspace := corecontract.WorkspaceRef{
		ID:      "workspace-secondary",
		Version: "v1",
		Digest:  strings.Repeat("4", 64),
	}
	control.Workspaces = append(
		control.Workspaces,
		controlcontract.WorkspaceDefinition{
			Workspace: secondaryWorkspace,
		},
	)
	authority, authorityCanonical, err :=
		moduleapi.NewMemoryAuthorityCeilingV1(
			moduleapi.MemoryAuthorityCeilingV1{
				SchemaVersion:       moduleapi.MemoryAuthorityCeilingSchemaV1,
				TenantID:            control.TenantID,
				AgentID:             control.Agents[0].ID,
				AllowedWorkspaceIDs: []string{"*"},
				AllowedKinds:        append([]moduleapi.MemoryEntryKindV1(nil), config.Kinds...),
				MaxItems:            32,
				MaxTotalTextBytes:   4096,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	authorityRef := putPublicationJSON(
		t,
		fixture.store,
		ContentAuthorityCeiling,
		authorityCanonical,
	)
	dynamicSpec := controlcontract.BindingSpec{
		Port:                contextPort,
		InstanceID:          provider.InstanceID,
		ConfigRef:           configRef,
		AuthorityCeilingRef: authorityRef,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	replaced := false
	for profileIndex := range control.Profiles {
		for bindingIndex := range control.Profiles[profileIndex].Bindings {
			binding := &control.Profiles[profileIndex].Bindings[bindingIndex]
			if binding.Port == contextPort {
				*binding = dynamicSpec
				replaced = true
			}
		}
	}
	if !replaced {
		t.Fatal("fixture Control lacks context.provide/v1 Binding")
	}
	control.SnapshotID = "control-memory-outcome"
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	catalog.GenerationID = "catalog-memory-outcome"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	catalog.Entries = append(
		catalog.Entries,
		controlcontract.CatalogEntry{
			Activation: provider,
			Provides:   []moduleapi.PortRef{contextPort},
		},
	)
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatal(err)
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
		t.Fatalf("PublishControlCatalog Memory: %v", err)
	}
	fixture.basis = basis
	fixture.controlCanonical = controlCanonical
	fixture.catalogCanonical = catalogCanonical

	_, genesisCanonical, err := moduleapi.NewAgentMemorySnapshotV1(
		moduleapi.AgentMemorySnapshotV1{
			SchemaVersion: moduleapi.MemorySnapshotSchemaV1,
			TenantID:      control.TenantID,
			AgentID:       control.Agents[0].ID,
			Revision:      1,
			Entries:       []moduleapi.MemoryEntryV1{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := fixture.store.PutAgentMemoryGenesis(
		context.Background(),
		genesisCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return &memoryOutcomeHarness{
		store:                fixture.store,
		fixture:              fixture,
		tenantID:             control.TenantID,
		agentID:              control.Agents[0].ID,
		primaryWorkspaceID:   control.Workspaces[0].Workspace.ID,
		secondaryWorkspaceID: secondaryWorkspace.ID,
		config:               config,
		configCanonical:      configCanonical,
		authority:            authority,
		authorityCanonical:   authorityCanonical,
		contextPort:          contextPort,
		genesisDigest:        genesis.Record.SnapshotRef.Digest,
	}
}

func (harness *memoryOutcomeHarness) beginRun(
	t *testing.T,
	runID string,
	admissionKey string,
	attemptID string,
	workspaceID string,
	taskText string,
) begunMemoryOutcomeRun {
	t.Helper()
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          taskText,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	task := newAdmissionContent(t, ContentTaskInput, taskCanonical)
	intent := harness.fixture.intent
	intent.AdmissionKey = admissionKey
	intent.WorkspaceID = workspaceID
	intent.TaskInputRef = task.Digest
	intent.RequestedPorts = []moduleapi.PortRef{
		{
			Name:         moduleapi.PortNameModelGenerate,
			ExactVersion: moduleapi.PortVersionV2,
		},
		harness.contextPort,
	}
	intent.Deadline = time.Now().UTC().Add(3 * time.Hour).
		Truncate(time.Microsecond)
	intent, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatal(err)
	}
	harness.fixture.task = task
	input := harness.fixture.compileInput(
		t,
		intentCanonical,
		intentDigest,
		runID,
	)
	input.Contents = []ContentInput{task}
	if _, err := harness.store.CommitRunAdmission(
		context.Background(),
		input,
	); err != nil {
		t.Fatalf("CommitRunAdmission(%s): %v", runID, err)
	}
	lease, err := harness.store.AcquireRunLease(
		context.Background(),
		AcquireRunLeaseInput{
			RunID:                 runID,
			OwnerID:               "memory-worker-" + runID,
			ExpectedRunRevision:   0,
			ExpectedFrameRevision: 0,
			TTL:                   time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run, err := harness.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatal(err)
	}
	compiled := harness.compileRequest(t, run)
	begin, err := harness.store.BeginModelDispatch(
		context.Background(),
		BeginModelDispatchInput{
			Lease:                       lease,
			AttemptID:                   attemptID,
			LogicalStepID:               "reply-" + attemptID,
			ContextCompilationCanonical: compiled.CompilationCanonical,
			RequestCanonical:            compiled.RequestCanonical,
			Deadline: run.Manifest.Deadline.Add(-time.Hour).
				Truncate(time.Microsecond),
		},
	)
	if err != nil {
		t.Fatalf("BeginModelDispatch(%s): %v", runID, err)
	}
	return begunMemoryOutcomeRun{begin: begin, taskRef: task.Digest}
}

func (harness *memoryOutcomeHarness) compileRequest(
	t *testing.T,
	run RunForLoop,
) contextcompiler.CompileResultV1 {
	t.Helper()
	var contextPlan *moduleapi.PortPlan
	for index := range run.Member.PortPlans {
		if run.Member.PortPlans[index].Port == harness.contextPort {
			plan := run.Member.PortPlans[index]
			contextPlan = &plan
			break
		}
	}
	if contextPlan == nil || len(contextPlan.Bindings) != 1 {
		t.Fatalf("Memory context plan=%+v", contextPlan)
	}
	bindings, err := frozenMemoryBindingsForRun(
		run.Manifest,
		run.Member,
		func(digest string) (ContentRecord, error) {
			record, found := run.FindContent(digest)
			if !found {
				return ContentRecord{}, fmt.Errorf("missing Run content %s", digest)
			}
			return record, nil
		},
	)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("frozen Memory Bindings=%+v error=%v", bindings, err)
	}
	binding := bindings[0]
	taskRecord, found := run.FindContent(run.Manifest.TaskInputRef)
	if !found {
		t.Fatal("TaskInput missing from Run closure")
	}
	task, err := corecontract.RestoreTaskInputV1(taskRecord.CanonicalBytes)
	if err != nil {
		t.Fatal(err)
	}
	head, err := harness.store.GetCurrentAgentMemory(
		context.Background(),
		run.Manifest.TenantID,
		run.Member.Agent.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	evaluatedAt := uint64(time.Now().UTC().UnixMilli())
	candidates, resolved, err := memorycore.FilterCandidates(
		head.Snapshot,
		head.SnapshotRef,
		binding.Scope,
		binding.Config,
		binding.Authority,
		evaluatedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	request, requestCanonical, requestDigest, err :=
		moduleapi.NewMemoryContextRequestV1(
			moduleapi.MemoryContextRequestV1{
				SchemaVersion:     moduleapi.MemoryContextRequestSchemaV1,
				Snapshot:          head.SnapshotRef,
				Scope:             binding.Scope,
				QueryText:         task.Text,
				EvaluatedAtUnixMS: evaluatedAt,
				Candidates:        candidates,
				MaxItems:          resolved.MaxItems,
				MaxTotalTextBytes: resolved.MaxTotalTextBytes,
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	selected := make([]string, len(request.Candidates))
	for index, candidate := range request.Candidates {
		selected[index] = candidate.EntryDigest
	}
	_, outputCanonical, _, err := moduleapi.NewMemoryContextOutputV1(
		moduleapi.MemoryContextOutputV1{
			SchemaVersion:        moduleapi.MemoryContextOutputSchemaV1,
			RequestDigest:        requestDigest,
			Snapshot:             head.SnapshotRef,
			SelectedEntryDigests: selected,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	policy, found := run.FindContent(run.Member.ContextPolicy.Digest)
	if !found {
		t.Fatal("ContextPolicy missing from Run closure")
	}
	modelBinding, err := exactModelBinding(run.Member)
	if err != nil {
		t.Fatal(err)
	}
	modelConfig, err := frozenModelBindingConfig(run, modelBinding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := contextcompiler.CompileV1(
		contextcompiler.CompileInputV1{
			TenantID:                       run.Manifest.TenantID,
			WorkspaceScope:                 run.Member.Workspace,
			AgentScope:                     run.Member.Agent,
			ContextPolicyRef:               run.Member.ContextPolicy,
			ContextPolicyDocumentCanonical: policy.CanonicalBytes,
			ModelParameters:                modelConfig.Parameters,
			ContextPlan:                    contextPlan,
			ContextBindings: []contextcompiler.BindingMaterialV1{{
				ConfigCanonical:         harness.configCanonical,
				AuthorityCanonical:      harness.authorityCanonical,
				StaticContextCanonicals: [][]byte{},
				DynamicStateCanonical:   head.CanonicalBytes,
				DynamicRequestCanonical: requestCanonical,
				DynamicOutputCanonical:  outputCanonical,
			}},
			TaskInputRef:       run.Manifest.TaskInputRef,
			TaskInputCanonical: taskRecord.CanonicalBytes,
		},
	)
	if err != nil {
		t.Fatalf("CompileV1 Memory: %v", err)
	}
	return result
}

func successfulMemoryOutcomeInput(
	t *testing.T,
	begin BeginModelDispatchResult,
	text string,
) CommitModelDispatchOutcomeInput {
	return successfulMemoryOutcomeInputWithRevision(
		t,
		begin,
		begin.Lease,
		begin.Attempt.Revision,
		text,
	)
}

func successfulMemoryOutcomeInputWithRevision(
	t *testing.T,
	begin BeginModelDispatchResult,
	lease RunLease,
	revision uint64,
	text string,
) CommitModelDispatchOutcomeInput {
	t.Helper()
	requestID := "provider-" + begin.Attempt.AttemptID
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     text,
			ProviderRequestID: requestID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return CommitModelDispatchOutcomeInput{
		Lease:                   lease,
		AttemptID:               begin.Attempt.AttemptID,
		InvocationID:            begin.Attempt.AttemptID,
		Provider:                begin.Attempt.Binding.Provider,
		ExpectedAttemptRevision: revision,
		State:                   corecontract.ModelAttemptSucceeded,
		OutputCanonical:         outputCanonical,
		UsageReceiptCanonical: modelUsageOutcomeCanonical(
			t,
			string(mustJSON(t, map[string]any{
				"id":     requestID,
				"status": "completed",
			})),
		),
	}
}

func currentMemoryHead(
	t *testing.T,
	harness *memoryOutcomeHarness,
) AgentMemoryRevisionRecord {
	t.Helper()
	head, err := harness.store.GetCurrentAgentMemory(
		context.Background(),
		harness.tenantID,
		harness.agentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

func findMemoryEntry(
	t *testing.T,
	entries []moduleapi.MemoryEntryV1,
	kind moduleapi.MemoryEntryKindV1,
	key string,
	workspaceID string,
) moduleapi.MemoryEntryV1 {
	t.Helper()
	for _, entry := range entries {
		if entry.Kind == kind && entry.Key == key &&
			len(entry.VisibleWorkspaceIDs) == 1 &&
			entry.VisibleWorkspaceIDs[0] == workspaceID {
			return entry
		}
	}
	t.Fatalf(
		"Memory entry kind=%s key=%s workspace=%s not found in %+v",
		kind,
		key,
		workspaceID,
		entries,
	)
	return moduleapi.MemoryEntryV1{}
}

func containsMemoryString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func modelMemoryAtomicCounts(t *testing.T, store *Store) [6]int {
	t.Helper()
	var counts [6]int
	for index, table := range []string{
		"content_records",
		"agent_memory_revisions",
		"history_entries",
		"run_events",
		"model_dispatch_attempts",
		"model_usage",
	} {
		if err := store.db.QueryRow(
			"SELECT COUNT(*) FROM " + table,
		).Scan(&counts[index]); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	return counts
}
