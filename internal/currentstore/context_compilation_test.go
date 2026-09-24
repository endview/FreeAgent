package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestBeginModelDispatchWithoutContextCompilationKeepsNullableClosureEmpty(
	t *testing.T,
) {
	fixture, lease, input := newModelDispatchFixture(t)
	if input.ContextCompilationCanonical != nil {
		t.Fatal("fixture unexpectedly supplied a context compilation")
	}
	estimate, watermark := modelRequestEstimateAndWatermark(
		t,
		fixture,
		lease,
		input,
	)
	if estimate >= watermark {
		t.Fatalf(
			"low-watermark fixture estimate=%d watermark=%d",
			estimate,
			watermark,
		)
	}
	result, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempt.ContextCompilation != nil {
		t.Fatalf("context compilation=%+v, want nil", result.Attempt.ContextCompilation)
	}
	var reference sql.NullString
	if err := fixture.store.db.QueryRow(`
		SELECT context_compilation_ref
		FROM model_dispatch_attempts
		WHERE attempt_id=?
	`, input.AttemptID).Scan(&reference); err != nil {
		t.Fatal(err)
	}
	if reference.Valid {
		t.Fatalf("context_compilation_ref=%q, want NULL", reference.String)
	}
	if got := contextCompilationContentCount(t, fixture.store); got != 0 {
		t.Fatalf("CONTEXT_COMPILATION rows=%d want 0", got)
	}
}

func TestBeginModelDispatchRejectsNilCompilationAfterProfileTightening(
	t *testing.T,
) {
	fixture, lease, input := newModelDispatchFixtureWithModelProfile(t, 100)
	if input.ContextCompilationCanonical != nil {
		t.Fatal("profiled fixture unexpectedly supplied a context compilation")
	}
	estimate, watermark := modelRequestEstimateAndWatermark(
		t,
		fixture,
		lease,
		input,
	)
	if estimate < watermark {
		t.Fatalf(
			"profiled fixture does not reach gate: estimate=%d watermark=%d",
			estimate,
			watermark,
		)
	}

	result, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	)
	if !errors.Is(err, ErrInvalidModelDispatch) {
		t.Fatalf("nil compilation above effective watermark error=%v", err)
	}
	if result.Created || result.InvokeAllowed ||
		result.ConsumeModelInvocationPermit() {
		t.Fatalf("rejected nil compilation returned a permit: %+v", result)
	}
	var attempts, requests, usage, frameRevision, events int
	var pending sql.NullString
	var step string
	if err := fixture.store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM content_records WHERE kind='MODEL_REQUEST'),
			(SELECT COUNT(*) FROM model_usage),
			frame_revision,
			step,
			pending_attempt_id,
			(SELECT COUNT(*) FROM run_events WHERE run_id=?)
		FROM loop_frames
		WHERE run_id=?
	`, lease.RunID, lease.RunID).Scan(
		&attempts,
		&requests,
		&usage,
		&frameRevision,
		&step,
		&pending,
		&events,
	); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 ||
		requests != 0 ||
		usage != 0 ||
		frameRevision != int(lease.FrameRevision) ||
		step != corecontract.InitialLoopStep ||
		pending.Valid ||
		events != 1 ||
		contextCompilationContentCount(t, fixture.store) != 0 {
		t.Fatalf(
			"rejected nil compilation wrote state attempts=%d requests=%d usage=%d frame=%d step=%s pending=%+v events=%d",
			attempts,
			requests,
			usage,
			frameRevision,
			step,
			pending,
			events,
		)
	}
}

func TestBeginModelDispatchPersistsAndLoadsExactContextCompilation(
	t *testing.T,
) {
	fixture, _, input := newModelDispatchFixture(t)
	input.ContextCompilationCanonical = validContextCompilationCanonical(
		t,
		fixture,
		input,
		nil,
	)
	want := bytes.Clone(input.ContextCompilationCanonical)
	result, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempt.ContextCompilation == nil ||
		result.Attempt.ContextCompilation.Kind != ContentContextCompilation ||
		!bytes.Equal(
			result.Attempt.ContextCompilation.CanonicalBytes,
			want,
		) {
		t.Fatalf("stored context compilation=%+v", result.Attempt.ContextCompilation)
	}
	if got := contextCompilationContentCount(t, fixture.store); got != 1 {
		t.Fatalf("CONTEXT_COMPILATION rows=%d want 1", got)
	}
	loaded, err := fixture.store.LoadRunForLoop(
		context.Background(),
		result.Lease,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ModelDispatches) != 1 ||
		loaded.ModelDispatches[0].Attempt.ContextCompilation == nil ||
		!bytes.Equal(
			loaded.ModelDispatches[0].Attempt.ContextCompilation.CanonicalBytes,
			want,
		) {
		t.Fatalf("loaded dispatches=%+v", loaded.ModelDispatches)
	}
	changed := result
	changed.Attempt = cloneModelDispatchAttempt(result.Attempt)
	changed.Attempt.ContextCompilation.Kind = ContentPolicy
	if changed.ConsumeModelInvocationPermit() {
		t.Fatal("mutated context compilation consumed invocation permit")
	}
	if !result.ConsumeModelInvocationPermit() {
		t.Fatal("exact persisted context closure did not consume invocation permit")
	}

	input.ContextCompilationCanonical[0] = 'x'
	result.Attempt.ContextCompilation.CanonicalBytes[0] = 'x'
	stored, err := queryModelDispatchAttempt(
		context.Background(),
		fixture.store.db,
		input.AttemptID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ContextCompilation == nil ||
		!bytes.Equal(stored.ContextCompilation.CanonicalBytes, want) {
		t.Fatal("caller mutation changed the persisted context compilation")
	}
}

func TestBeginModelDispatchContextCompilationExactRetryIsByteExact(
	t *testing.T,
) {
	fixture, _, input := newModelDispatchFixture(t)
	input.ContextCompilationCanonical = validContextCompilationCanonical(
		t,
		fixture,
		input,
		nil,
	)
	first, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Created || retry.InvokeAllowed ||
		retry.Attempt.ContextCompilation == nil ||
		!bytes.Equal(
			retry.Attempt.ContextCompilation.CanonicalBytes,
			input.ContextCompilationCanonical,
		) {
		t.Fatalf("exact retry=%+v", retry)
	}
	if !first.InvokeAllowed || retry.ConsumeModelInvocationPermit() {
		t.Fatal("exact retry changed invocation permit semantics")
	}

	missing := input
	missing.ContextCompilationCanonical = nil
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		missing,
	); !errors.Is(err, ErrModelDispatchConflict) {
		t.Fatalf("missing compilation retry error=%v", err)
	}

	different := input
	different.ContextCompilationCanonical = validContextCompilationCanonical(
		t,
		fixture,
		input,
		func(value *corecontract.ContextCompilationV1) {
			value.OriginalEstimateTokens++
			value.FinalEstimateTokens++
		},
	)
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		different,
	); !errors.Is(err, ErrModelDispatchConflict) {
		t.Fatalf("different compilation retry error=%v", err)
	}
	if got := contextCompilationContentCount(t, fixture.store); got != 1 {
		t.Fatalf("conflicting retries changed compilation rows to %d", got)
	}

	lowFixture, _, lowInput := newModelDispatchFixture(t)
	if _, err := lowFixture.store.BeginModelDispatch(
		context.Background(),
		lowInput,
	); err != nil {
		t.Fatal(err)
	}
	late := lowInput
	late.ContextCompilationCanonical = validContextCompilationCanonical(
		t,
		lowFixture,
		lowInput,
		nil,
	)
	if _, err := lowFixture.store.BeginModelDispatch(
		context.Background(),
		late,
	); !errors.Is(err, ErrModelDispatchConflict) {
		t.Fatalf("new compilation on low-path retry error=%v", err)
	}
}

func TestBeginModelDispatchRejectsUnclosedContextCompilationAtomically(
	t *testing.T,
) {
	tests := []struct {
		name   string
		mutate func(*corecontract.ContextCompilationV1)
		raw    func([]byte) []byte
	}{
		{
			name: "Workspace scope",
			mutate: func(value *corecontract.ContextCompilationV1) {
				value.WorkspaceScope.ID = "workspace-other"
			},
		},
		{
			name: "context policy",
			mutate: func(value *corecontract.ContextCompilationV1) {
				value.ContextPolicy.ID = "context-policy-other"
			},
		},
		{
			name: "request digest",
			mutate: func(value *corecontract.ContextCompilationV1) {
				value.FinalRequestDigest = strings.Repeat("f", 64)
			},
		},
		{
			name: "policy budget",
			mutate: func(value *corecontract.ContextCompilationV1) {
				value.InputBudgetTokens = 2000
				value.RestoreWatermarkTokens = 1700
				value.OriginalEstimateTokens = 1800
				value.FinalEstimateTokens = 1800
			},
		},
		{
			name: "foreign Drop digest",
			mutate: func(value *corecontract.ContextCompilationV1) {
				setContextCompilationDrop(
					value,
					corecontract.ContextCompilationUnitHistoryTurn,
					strings.Repeat("e", 64),
				)
			},
		},
		{
			name: "noncanonical bytes",
			raw: func(canonical []byte) []byte {
				return append(bytes.Clone(canonical), '\n')
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture, lease, input := newModelDispatchFixture(t)
			canonical := validContextCompilationCanonical(
				t,
				fixture,
				input,
				test.mutate,
			)
			if test.raw != nil {
				canonical = test.raw(canonical)
			}
			input.ContextCompilationCanonical = canonical
			if _, err := fixture.store.BeginModelDispatch(
				context.Background(),
				input,
			); err == nil {
				t.Fatal("unclosed context compilation was accepted")
			}
			var attempts int
			var frameRevision int64
			var pending sql.NullString
			if err := fixture.store.db.QueryRow(`
				SELECT
					(SELECT COUNT(*) FROM model_dispatch_attempts),
					frame_revision,
					pending_attempt_id
				FROM loop_frames
				WHERE run_id=?
			`, lease.RunID).Scan(
				&attempts,
				&frameRevision,
				&pending,
			); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 ||
				frameRevision != int64(lease.FrameRevision) ||
				pending.Valid ||
				contextCompilationContentCount(t, fixture.store) != 0 {
				t.Fatalf(
					"partial write attempts=%d frame=%d pending=%+v",
					attempts,
					frameRevision,
					pending,
				)
			}
		})
	}
}

func TestExpiredModelDispatchPersistsContextCompilationWithoutPermit(
	t *testing.T,
) {
	fixture, _, input := newModelDispatchFixture(t)
	input.ContextCompilationCanonical = validContextCompilationCanonical(
		t,
		fixture,
		input,
		nil,
	)
	input.Deadline = time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	result, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempt.State != corecontract.ModelAttemptFailed ||
		result.Attempt.ContextCompilation == nil ||
		result.InvokeAllowed ||
		result.ConsumeModelInvocationPermit() {
		t.Fatalf("expired result=%+v", result)
	}
	retry, err := fixture.store.BeginModelDispatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Created || retry.InvokeAllowed ||
		retry.Attempt.ContextCompilation == nil {
		t.Fatalf("expired retry=%+v", retry)
	}
}

func TestContextCompilationRollsBackWithBeginModelDispatchTransaction(
	t *testing.T,
) {
	fixture, lease, input := newModelDispatchFixture(t)
	input.ContextCompilationCanonical = validContextCompilationCanonical(
		t,
		fixture,
		input,
		nil,
	)
	if _, err := fixture.store.db.Exec(`
		CREATE TRIGGER reject_compiled_attempt
		BEFORE INSERT ON model_dispatch_attempts
		BEGIN
			SELECT RAISE(ABORT, 'injected compiled Attempt failure');
		END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.BeginModelDispatch(
		context.Background(),
		input,
	); err == nil {
		t.Fatal("injected Attempt failure was accepted")
	}
	var attempts, requests, frameRevision int
	var pending sql.NullString
	if err := fixture.store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM content_records WHERE kind='MODEL_REQUEST'),
			frame_revision,
			pending_attempt_id
		FROM loop_frames
		WHERE run_id=?
	`, lease.RunID).Scan(
		&attempts,
		&requests,
		&frameRevision,
		&pending,
	); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 ||
		requests != 0 ||
		contextCompilationContentCount(t, fixture.store) != 0 ||
		frameRevision != int(lease.FrameRevision) ||
		pending.Valid {
		t.Fatalf(
			"partial attempts=%d requests=%d frame=%d pending=%+v",
			attempts,
			requests,
			frameRevision,
			pending,
		)
	}
}

func TestBeginModelDispatchRejectsForgedSummaryTextBeforePendingWrite(
	t *testing.T,
) {
	fixture, _, firstInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(
		context.Background(),
		firstInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	outcome, err := fixture.store.CommitModelDispatchOutcome(
		context.Background(),
		CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	readyContinuation, err := corecontract.NewLoopContinuationV1(
		corecontract.InitialLoopStep,
		"",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	execClosedFileTamperV1(t, fixture.store, nil, `
		UPDATE runs
		SET state=?, disposition=NULL
		WHERE run_id=?
	`, corecontract.InitialRunState, outcome.Lease.RunID)
	if _, err := fixture.store.db.Exec(`
		UPDATE loop_frames
		SET
			step=?,
			continuation=?,
			pending_attempt_id=NULL,
			waiting_reason=NULL
		WHERE run_id=?
	`,
		corecontract.InitialLoopStep,
		readyContinuation,
		outcome.Lease.RunID,
	); err != nil {
		t.Fatal(err)
	}

	second := firstInput
	second.Lease = outcome.Lease
	second.AttemptID = "attempt-reply-2"
	second.LogicalStepID = "reply-2"
	requestDigest, err := ComputeContentDigest(
		ContentModelRequest,
		admissionJSONMediaType,
		second.RequestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	turnDigest, err := corecontract.ContextHistoryTurnDigestV1(
		1,
		outcome.Record.Attempt.ResultRef,
	)
	if err != nil {
		t.Fatal(err)
	}
	member := mustRestoreMemberSnapshot(t, fixture)
	_, compilationCanonical, err := corecontract.NewContextCompilationV1(
		corecontract.ContextCompilationV1{
			SchemaVersion:  corecontract.ContextCompilationSchemaVersionV1,
			WorkspaceScope: member.Workspace,
			ContextPolicy:  member.ContextPolicy,
			EstimatorVersion: corecontract.
				ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
			SummaryAlgorithmVersion: corecontract.
				ContextSummaryHeadTailExtractiveV1,
			InputBudgetTokens:      1000,
			RestoreWatermarkTokens: 850,
			OriginalEstimateTokens: 900,
			Summary: &corecontract.ContextCompilationSummaryV1{
				SourceTurnDigests:    []string{turnDigest},
				Text:                 "forged deterministic summary",
				BeforeEstimateTokens: 900,
				AfterEstimateTokens:  800,
			},
			Drops:               []corecontract.ContextCompilationDropV1{},
			FinalEstimateTokens: 800,
			StopReason: corecontract.
				ContextCompilationSummaryToWatermark,
			FinalRequestDigest: requestDigest,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	second.ContextCompilationCanonical = compilationCanonical
	failed, err := fixture.store.BeginModelDispatch(
		context.Background(),
		second,
	)
	if !errors.Is(err, ErrInvalidModelDispatch) {
		t.Fatalf("forged summary error=%v", err)
	}
	if failed.ConsumeModelInvocationPermit() {
		t.Fatal("rejected forged summary received an invocation permit")
	}
	var attempts, requests, frameRevision int
	var step string
	var pending sql.NullString
	if err := fixture.store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM content_records WHERE kind='MODEL_REQUEST'),
			frame_revision,
			step,
			pending_attempt_id
		FROM loop_frames
		WHERE run_id=?
	`, outcome.Lease.RunID).Scan(
		&attempts,
		&requests,
		&frameRevision,
		&step,
		&pending,
	); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 ||
		requests != 1 ||
		contextCompilationContentCount(t, fixture.store) != 0 ||
		frameRevision != int(outcome.Lease.FrameRevision) ||
		step != corecontract.InitialLoopStep ||
		pending.Valid {
		t.Fatalf(
			"forged summary wrote state attempts=%d requests=%d frame=%d step=%s pending=%+v",
			attempts,
			requests,
			frameRevision,
			step,
			pending,
		)
	}
}

func TestContextCompilationEvidenceRequiresFrozenOrderedRunUnits(t *testing.T) {
	staticA := strings.Repeat("a", 64)
	staticB := strings.Repeat("b", 64)
	historyC := strings.Repeat("c", 64)
	historyD := strings.Repeat("d", 64)
	turnC, err := corecontract.ContextHistoryTurnDigestV1(1, historyC)
	if err != nil {
		t.Fatal(err)
	}
	turnD, err := corecontract.ContextHistoryTurnDigestV1(2, historyD)
	if err != nil {
		t.Fatal(err)
	}
	_, outputC, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: "history C",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, outputD, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion: moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText: "history D",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	summaryText, err := corecontract.ContextHeadTailSummaryV1(
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleAssistant, Content: "history C"},
			{Role: moduleapi.ModelRoleAssistant, Content: "history D"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	run := RunForLoop{
		Member: corecontract.MemberExecutionSnapshot{
			PortPlans: []moduleapi.PortPlan{{
				Port: moduleapi.PortRef{
					Name:         moduleapi.PortNameContextProvide,
					ExactVersion: moduleapi.PortVersionV1,
				},
				Bindings: []moduleapi.PortBinding{{
					StaticContextRefs: []string{staticB, staticA},
				}},
			}},
		},
		Contents: []ContentRecord{
			{Digest: staticA, Kind: ContentStaticContext},
			{Digest: staticB, Kind: ContentStaticContext},
		},
		ModelDispatches: []ModelDispatchRecord{
			{Attempt: ModelDispatchAttemptRecord{AttemptID: "a1", FrameRevision: 1}},
			{Attempt: ModelDispatchAttemptRecord{AttemptID: "a2", FrameRevision: 2}},
		},
		History: []HistoryEntryRecord{
			{
				Sequence:        1,
				SourceAttemptID: "a1",
				Content: ContentRecord{
					Digest: historyC, CanonicalBytes: outputC,
				},
			},
			{
				Sequence:        2,
				SourceAttemptID: "a2",
				Content: ContentRecord{
					Digest: historyD, CanonicalBytes: outputD,
				},
			},
		},
	}
	summary := corecontract.ContextCompilationV1{
		Summary: &corecontract.ContextCompilationSummaryV1{
			SourceTurnDigests: []string{turnC, turnD},
			Text:              summaryText,
		},
	}
	if err := validateContextCompilationEvidence(summary, run, 3, 0); err != nil {
		t.Fatalf("valid ordered summary evidence: %v", err)
	}

	valid := corecontract.ContextCompilationV1{
		Drops: []corecontract.ContextCompilationDropV1{
			{UnitKind: corecontract.ContextCompilationUnitHistoryTurn, UnitDigest: turnC},
			{UnitKind: corecontract.ContextCompilationUnitHistoryTurn, UnitDigest: turnD},
		},
	}
	if err := validateContextCompilationEvidence(valid, run, 3, 0); err != nil {
		t.Fatalf("valid ordered Drop evidence: %v", err)
	}

	reversed := valid
	reversed.Drops = append([]corecontract.ContextCompilationDropV1{}, valid.Drops...)
	reversed.Drops[0], reversed.Drops[1] = reversed.Drops[1], reversed.Drops[0]
	if err := validateContextCompilationEvidence(reversed, run, 3, 0); err == nil {
		t.Fatal("reversed Drop order was accepted")
	}
	foreign := valid
	foreign.Drops = []corecontract.ContextCompilationDropV1{{
		UnitKind:   corecontract.ContextCompilationUnitHistoryTurn,
		UnitDigest: strings.Repeat("e", 64),
	}}
	if err := validateContextCompilationEvidence(foreign, run, 3, 0); err == nil {
		t.Fatal("foreign Drop was accepted")
	}
	staticDrop := valid
	staticDrop.Drops = []corecontract.ContextCompilationDropV1{{
		UnitKind: corecontract.ContextCompilationUnitKindV1(
			"STATIC_CONTEXT",
		),
		UnitDigest: staticB,
	}}
	if err := validateContextCompilationEvidence(staticDrop, run, 3, 0); err == nil {
		t.Fatal("static context Drop was accepted")
	}
	future := summary
	if err := validateContextCompilationEvidence(future, run, 2, 0); err == nil {
		t.Fatal("summary containing future History was accepted")
	}
	if err := validateContextCompilationEvidence(summary, run, 3, 1); err == nil {
		t.Fatal("summary consumed the protected recent History window")
	}
}

func validContextCompilationCanonical(
	t *testing.T,
	fixture *admissionCommitFixture,
	input BeginModelDispatchInput,
	mutate func(*corecontract.ContextCompilationV1),
) []byte {
	t.Helper()
	member := mustRestoreMemberSnapshot(t, fixture)
	requestDigest, err := ComputeContentDigest(
		ContentModelRequest,
		admissionJSONMediaType,
		input.RequestCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	value := corecontract.ContextCompilationV1{
		SchemaVersion:  corecontract.ContextCompilationSchemaVersionV1,
		WorkspaceScope: member.Workspace,
		ContextPolicy:  member.ContextPolicy,
		EstimatorVersion: corecontract.
			ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1,
		SummaryAlgorithmVersion: corecontract.
			ContextSummaryHeadTailExtractiveV1,
		InputBudgetTokens:      936,
		RestoreWatermarkTokens: 795,
		OriginalEstimateTokens: 900,
		Drops:                  []corecontract.ContextCompilationDropV1{},
		FinalEstimateTokens:    900,
		StopReason: corecontract.
			ContextCompilationNoEligibleSummary,
		FinalRequestDigest: requestDigest,
	}
	if mutate != nil {
		mutate(&value)
	}
	_, canonical, err := corecontract.NewContextCompilationV1(value)
	if err != nil {
		t.Fatalf("NewContextCompilationV1: %v", err)
	}
	return canonical
}

func setContextCompilationDrop(
	value *corecontract.ContextCompilationV1,
	kind corecontract.ContextCompilationUnitKindV1,
	digest string,
) {
	value.OriginalEstimateTokens = 1100
	value.Drops = []corecontract.ContextCompilationDropV1{{
		UnitKind:             kind,
		UnitDigest:           digest,
		BeforeEstimateTokens: 1100,
		AfterEstimateTokens:  700,
	}}
	value.FinalEstimateTokens = 700
	value.StopReason = corecontract.ContextCompilationDropToWatermark
}

func contextCompilationContentCount(t *testing.T, store *Store) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`
		SELECT COUNT(*)
		FROM content_records
		WHERE kind='CONTEXT_COMPILATION'
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func modelRequestEstimateAndWatermark(
	t *testing.T,
	fixture *admissionCommitFixture,
	lease RunLease,
	input BeginModelDispatchInput,
) (uint64, uint64) {
	t.Helper()
	run, err := fixture.store.LoadRunForLoop(context.Background(), lease)
	if err != nil {
		t.Fatalf("LoadRunForLoop for context gate: %v", err)
	}
	policy, err := frozenEffectiveContextPolicyForRun(run)
	if err != nil {
		t.Fatalf("frozenEffectiveContextPolicyForRun: %v", err)
	}
	watermark, err := policy.RestoreWatermarkTokens()
	if err != nil {
		t.Fatalf("RestoreWatermarkTokens: %v", err)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		input.RequestCanonical,
	)
	if err != nil {
		t.Fatalf("RestoreModelGenerateRequestV1: %v", err)
	}
	estimate, err := contextcompiler.EstimateModelGenerateRequestV1(request)
	if err != nil {
		t.Fatalf("EstimateModelGenerateRequestV1: %v", err)
	}
	return estimate, watermark
}
