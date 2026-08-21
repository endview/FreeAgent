package s3eval

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/sdk/loopapi"
)

// Run executes exactly three Composite Chat calls behind one start barrier.
// It waits for every call, reads only the existing Current Store projection,
// and returns partial evidence together with a stable errors.Join result.
func Run(
	ctx context.Context,
	input ExperimentInput,
	chat ChatFunc,
	usage CompositeUsageReader,
) (ExperimentReport, error) {
	if ctx == nil {
		return ExperimentReport{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidExperiment,
		)
	}
	if err := ctx.Err(); err != nil {
		return ExperimentReport{}, err
	}
	if chat == nil || isNilUsageReader(usage) {
		return ExperimentReport{}, fmt.Errorf(
			"%w: Chat and Current Store projection are required",
			ErrInvalidExperiment,
		)
	}
	workspaceIDs := make([]string, WorkspaceCount)
	seen := make(map[string]struct{}, WorkspaceCount)
	for index, task := range input.Tasks {
		workspaceID := task.ChatInput.WorkspaceID
		if workspaceID == "" || workspaceID != strings.TrimSpace(workspaceID) {
			return ExperimentReport{}, fmt.Errorf(
				"%w: invalid Workspace ID at task %d",
				ErrInvalidExperiment,
				index,
			)
		}
		if _, exists := seen[workspaceID]; exists {
			return ExperimentReport{}, fmt.Errorf(
				"%w: duplicate Workspace ID %q",
				ErrInvalidExperiment,
				workspaceID,
			)
		}
		seen[workspaceID] = struct{}{}
		workspaceIDs[index] = workspaceID
	}

	report := ExperimentReport{
		Families: make([]FamilyReport, WorkspaceCount),
	}
	familyErrors := make([]error, WorkspaceCount)
	start := make(chan struct{})
	var ready sync.WaitGroup
	var complete sync.WaitGroup
	ready.Add(WorkspaceCount)
	complete.Add(WorkspaceCount)
	for index := range input.Tasks {
		index := index
		go func() {
			defer complete.Done()
			ready.Done()
			<-start
			report.Families[index], familyErrors[index] = runFamily(
				ctx,
				input.Tasks[index].ChatInput,
				chat,
				usage,
			)
		}()
	}
	ready.Wait()
	close(start)
	complete.Wait()

	type attemptLocation struct {
		family  int
		attempt int
	}
	locations := make([]attemptLocation, 0)
	for familyIndex := range report.Families {
		for attemptIndex := range report.Families[familyIndex].Attempts {
			locations = append(locations, attemptLocation{
				family:  familyIndex,
				attempt: attemptIndex,
			})
		}
	}
	sort.SliceStable(locations, func(left int, right int) bool {
		leftAttempt := report.Families[locations[left].family].Attempts[locations[left].attempt]
		rightAttempt := report.Families[locations[right].family].Attempts[locations[right].attempt]
		if !leftAttempt.CreatedAt.Equal(rightAttempt.CreatedAt) {
			return leftAttempt.CreatedAt.Before(rightAttempt.CreatedAt)
		}
		if leftAttempt.AttemptID != rightAttempt.AttemptID {
			return leftAttempt.AttemptID < rightAttempt.AttemptID
		}
		if leftAttempt.WorkspaceID != rightAttempt.WorkspaceID {
			return leftAttempt.WorkspaceID < rightAttempt.WorkspaceID
		}
		if leftAttempt.RootRunID != rightAttempt.RootRunID {
			return leftAttempt.RootRunID < rightAttempt.RootRunID
		}
		return leftAttempt.RunID < rightAttempt.RunID
	})
	report.ServiceOrder = make([]AttemptFact, len(locations))
	serviceWorkspaceOrder := make([]string, len(locations))
	for index, location := range locations {
		attempt := &report.Families[location.family].Attempts[location.attempt]
		attempt.ServiceOrder = uint64(index + 1)
		report.ServiceOrder[index] = cloneAttemptFact(*attempt)
		serviceWorkspaceOrder[index] = attempt.WorkspaceID
	}
	fairness, err := ComputeFairness(workspaceIDs, serviceWorkspaceOrder)
	if err != nil {
		return report, err
	}
	report.Fairness = fairness

	joined := make([]error, 0, WorkspaceCount)
	for index, err := range familyErrors {
		if err != nil {
			joined = append(joined, fmt.Errorf(
				"Workspace %q: %w",
				workspaceIDs[index],
				err,
			))
		}
	}
	return report, errors.Join(joined...)
}

func runFamily(
	ctx context.Context,
	input localchat.ChatInput,
	chat ChatFunc,
	usage CompositeUsageReader,
) (FamilyReport, error) {
	startedAt := time.Now()
	chatResult, chatErr := chat(ctx, input)
	wallElapsed := time.Since(startedAt)
	report := FamilyReport{
		WorkspaceID: input.WorkspaceID,
		RequestID:   chatResult.RequestID,
		RootRunID:   chatResult.RootRunID,
		StartedAt:   startedAt,
		FinishedAt:  startedAt.Add(wallElapsed),
		WallElapsed: wallElapsed,
		Children: make(
			[]DispositionFact,
			len(chatResult.Children),
		),
		Root: dispositionFact(
			chatResult.RootRunID,
			chatResult.LoopResult,
		),
		Reply:   chatResult.Reply,
		Failure: chatResult.FailureCode,
	}
	for index, child := range chatResult.Children {
		report.Children[index] = dispositionFact(
			child.RunID,
			child.LoopResult,
		)
	}
	if chatResult.Reviewer != nil {
		reviewer := dispositionFact(
			chatResult.Reviewer.RunID,
			chatResult.Reviewer.LoopResult,
		)
		report.Reviewer = &reviewer
	}

	errorsFound := make([]error, 0, 2)
	if chatErr != nil {
		errorsFound = append(errorsFound, fmt.Errorf("Composite Chat: %w", chatErr))
	}
	if chatResult.RootRunID == "" {
		if chatErr == nil {
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: Composite Chat returned no root Run ID",
				ErrMetricIntegrity,
			))
		}
	} else {
		projection, projectionErr := usage.GetCompositeFamilyUsageProjection(
			ctx,
			chatResult.RootRunID,
		)
		if projectionErr != nil {
			errorsFound = append(errorsFound, fmt.Errorf(
				"family Usage projection: %w",
				projectionErr,
			))
		} else if projection.RootRunID != chatResult.RootRunID {
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: Usage projection belongs to another root Run",
				ErrMetricIntegrity,
			))
		} else {
			if err := applyProjection(&report, projection); err != nil {
				errorsFound = append(errorsFound, err)
			}
			// Terminal evidence is independently useful. A malformed metric on
			// one Attempt must not suppress otherwise valid durable results for
			// the remaining family Runs.
			if err := applyTerminalResults(ctx, &report, projection, usage); err != nil {
				errorsFound = append(errorsFound, err)
			}
		}
	}
	joined := errors.Join(errorsFound...)
	if joined != nil {
		report.Error = joined.Error()
	}
	return report, joined
}

func applyTerminalResults(
	ctx context.Context,
	report *FamilyReport,
	projection currentstore.CompositeFamilyUsageProjectionV1,
	reader CompositeUsageReader,
) error {
	report.Results = make([]ResultFact, 0, len(projection.Runs))
	dispositions, dispositionErrors := familyDispositionFacts(report, projection)
	errorsFound := append([]error(nil), dispositionErrors...)
	for _, run := range projection.Runs {
		disposition := dispositions[run.RunID]
		if disposition == nil {
			// The topology error was already retained by familyDispositionFacts.
			// Without an exact in-memory disposition, the observer cannot decide
			// whether a terminal Store read is permitted.
			continue
		}
		if disposition.Disposition != loopapi.DispositionTerminated {
			// WAITING/PENDING/MODEL_UNKNOWN are deliberately not presented to
			// GetTerminalRunResult. They are durable non-terminal states and a
			// terminal read would turn normal reconciliation into false evidence.
			continue
		}
		terminal, err := reader.GetTerminalRunResult(ctx, run.RunID)
		if err != nil {
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: terminal result for Run %q: %v",
				ErrMetricIntegrity,
				run.RunID,
				err,
			))
			continue
		}
		if err := validateDurableTerminalClosure(run, terminal); err != nil {
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: terminal result for Run %q: %v",
				ErrMetricIntegrity,
				run.RunID,
				err,
			))
			continue
		}

		// Replace the transient Loop view with the immutable terminal
		// transition. A later exact re-entry may advance the mutable lease head,
		// while GetTerminalRunResult deliberately returns the authoritative
		// terminal Frame revision and reason.
		disposition.FrameRevision = terminal.FrameRevision
		disposition.ReasonCode = terminal.ReasonCode
		if run.Role == corecontract.CompositeRunRoleRootV1 {
			if err := applyDurableRootPresentation(report, terminal); err != nil {
				errorsFound = append(errorsFound, fmt.Errorf(
					"%w: terminal root Run %q: %v",
					ErrMetricIntegrity,
					run.RunID,
					err,
				))
			}
		}
		if run.Attempt == nil ||
			run.Attempt.State != corecontract.ModelAttemptSucceeded {
			// FAILED and attempt-free deterministic failures are verified above,
			// but only a successful MODEL_RESULT can become ResultFact evidence.
			continue
		}
		if len(terminal.OutputCanonical) == 0 ||
			terminal.Output.ActionRequest != nil {
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: terminal result for Run %q has no closed model output",
				ErrMetricIntegrity,
				run.RunID,
			))
			continue
		}
		resultDigest, err := currentstore.ComputeContentDigest(
			currentstore.ContentModelResult,
			"application/json",
			terminal.OutputCanonical,
		)
		if err != nil {
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: terminal result digest for Run %q: %v",
				ErrMetricIntegrity,
				run.RunID,
				err,
			))
			continue
		}
		fact := ResultFact{
			RunID:         run.RunID,
			Role:          run.Role,
			SlotID:        run.SlotID,
			AttemptID:     terminal.AttemptID,
			ResultDigest:  resultDigest,
			AssistantText: terminal.Output.AssistantText,
		}
		if run.Role == corecontract.CompositeRunRoleReviewerV1 {
			verdict, err := corecontract.RestoreReviewVerdictV1(
				[]byte(terminal.Output.AssistantText),
			)
			if err != nil {
				errorsFound = append(errorsFound, fmt.Errorf(
					"%w: Reviewer Run %q result is not a canonical ReviewVerdict: %v",
					ErrMetricIntegrity,
					run.RunID,
					err,
				))
				continue
			}
			if verdict.FamilyDigest != projection.RootManifestDigest {
				errorsFound = append(errorsFound, fmt.Errorf(
					"%w: Reviewer Run %q verdict family digest %q does not bind root manifest %q",
					ErrMetricIntegrity,
					run.RunID,
					verdict.FamilyDigest,
					projection.RootManifestDigest,
				))
				continue
			}
			fact.ReviewerVerdict = &verdict
		}
		report.Results = append(report.Results, fact)
	}
	return errors.Join(errorsFound...)
}

// familyDispositionFacts closes the transient Chat topology against the
// immutable family projection while retaining every independently usable Run.
// A malformed Run does not suppress terminal evidence for later valid Runs.
func familyDispositionFacts(
	report *FamilyReport,
	projection currentstore.CompositeFamilyUsageProjectionV1,
) (map[string]*DispositionFact, []error) {
	result := make(map[string]*DispositionFact, len(projection.Runs))
	errorsFound := make([]error, 0)
	children := make(map[string]*DispositionFact, len(report.Children))
	for index := range report.Children {
		fact := &report.Children[index]
		if fact.RunID == "" {
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: Child disposition %d has no Run ID",
				ErrMetricIntegrity,
				index,
			))
			continue
		}
		if _, duplicate := children[fact.RunID]; duplicate {
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: duplicate Child disposition for Run %q",
				ErrMetricIntegrity,
				fact.RunID,
			))
			continue
		}
		children[fact.RunID] = fact
	}

	projectedChildren := 0
	projectedReviewer := false
	for _, run := range projection.Runs {
		var fact *DispositionFact
		switch run.Role {
		case corecontract.CompositeRunRoleChildV1:
			projectedChildren++
			fact = children[run.RunID]
		case corecontract.CompositeRunRoleReviewerV1:
			projectedReviewer = true
			if report.Reviewer != nil && report.Reviewer.RunID == run.RunID {
				fact = report.Reviewer
			}
		case corecontract.CompositeRunRoleRootV1:
			if report.Root.RunID == run.RunID && report.RootRunID == run.RunID {
				fact = &report.Root
			}
		default:
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: projected Run %q has unsupported Composite role %q",
				ErrMetricIntegrity,
				run.RunID,
				run.Role,
			))
			continue
		}
		if fact == nil {
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: projected %s Run %q has no exact Chat disposition",
				ErrMetricIntegrity,
				run.Role,
				run.RunID,
			))
			continue
		}
		if _, duplicate := result[run.RunID]; duplicate {
			errorsFound = append(errorsFound, fmt.Errorf(
				"%w: projected Run %q appears more than once",
				ErrMetricIntegrity,
				run.RunID,
			))
			continue
		}
		result[run.RunID] = fact
	}
	if len(report.Children) != projectedChildren {
		errorsFound = append(errorsFound, fmt.Errorf(
			"%w: Chat/Store Child disposition count differs",
			ErrMetricIntegrity,
		))
	}
	if (report.Reviewer != nil) != projectedReviewer {
		errorsFound = append(errorsFound, fmt.Errorf(
			"%w: Chat/Store Reviewer disposition presence differs",
			ErrMetricIntegrity,
		))
	}
	return result, errorsFound
}

func validateDurableTerminalClosure(
	run currentstore.CompositeFamilyRunUsageFactV1,
	terminal currentstore.TerminalRunResult,
) error {
	if terminal.RunID != run.RunID || terminal.ReasonCode == "" {
		return errors.New("terminal identity or reason does not close the Run")
	}
	if run.Attempt == nil {
		if terminal.AttemptID != "" || terminal.AttemptKind != "" ||
			terminal.ErrorClassification == "" ||
			terminal.ReasonCode != terminal.ErrorClassification {
			return errors.New("attempt-free terminal Run is not a deterministic failure")
		}
		return nil
	}
	attempt := run.Attempt
	if terminal.AttemptID != attempt.AttemptID ||
		terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.State != attempt.State || terminal.ModelState != attempt.State {
		return errors.New("terminal model Attempt does not match persisted Usage projection")
	}
	switch attempt.State {
	case corecontract.ModelAttemptSucceeded:
		if terminal.ErrorClassification != "" ||
			terminal.ReasonCode != "MODEL_SUCCEEDED" {
			return errors.New("successful model Attempt lacks its closed success projection")
		}
	case corecontract.ModelAttemptFailed:
		if terminal.ErrorClassification == "" ||
			len(terminal.OutputCanonical) != 0 ||
			terminal.ReasonCode != "MODEL_FAILED" {
			return errors.New("failed model Attempt lacks its closed failure projection")
		}
	default:
		return fmt.Errorf(
			"terminated Run retains non-terminal model state %q",
			attempt.State,
		)
	}
	return nil
}

// applyDurableRootPresentation keeps the existing report schema while making
// Reply/Failure authoritative Store facts. Any disagreement with the transient
// Chat result is still surfaced as metric-integrity evidence.
func applyDurableRootPresentation(
	report *FamilyReport,
	terminal currentstore.TerminalRunResult,
) error {
	wantReply := ""
	wantFailure := terminal.ErrorClassification
	if terminal.AttemptKind == corecontract.AttemptKindModel &&
		terminal.ModelState == corecontract.ModelAttemptSucceeded {
		wantReply = terminal.Output.AssistantText
		wantFailure = ""
	}
	differs := report.Reply != wantReply || report.Failure != wantFailure
	report.Reply = wantReply
	report.Failure = wantFailure
	if differs {
		return errors.New("transient Chat reply/failure differs from durable root terminal result")
	}
	return nil
}

func applyProjection(
	report *FamilyReport,
	projection currentstore.CompositeFamilyUsageProjectionV1,
) error {
	tokens := cloneTokenTotals(projection.Aggregate.TokenTotals)
	ratio, err := ComputeKnownCacheHitRatio(tokens)
	if err != nil {
		return err
	}
	report.Tokens = TokenReport{
		Totals:        tokenTotalsReport(tokens),
		CacheHitRatio: ratio,
	}
	report.Costs = CostReport{
		Estimated: costTotalReport(projection.Aggregate.EstimatedCost),
		ProviderReported: costTotalReport(
			projection.Aggregate.ProviderReportedCost,
		),
		Reconciled: costTotalReport(projection.Aggregate.ReconciledCost),
	}
	report.Attempts = make([]AttemptFact, 0, projection.Aggregate.AttemptSlotsUsed)
	for _, run := range projection.Runs {
		if run.Attempt == nil {
			continue
		}
		attempt := run.Attempt
		if attempt.CreatedAt.IsZero() || attempt.UpdatedAt.IsZero() ||
			attempt.UpdatedAt.Before(attempt.CreatedAt) {
			return fmt.Errorf(
				"%w: Attempt %q has invalid persisted timestamps",
				ErrMetricIntegrity,
				attempt.AttemptID,
			)
		}
		report.Attempts = append(report.Attempts, AttemptFact{
			WorkspaceID:         report.WorkspaceID,
			RootRunID:           report.RootRunID,
			RunID:               run.RunID,
			Role:                run.Role,
			SlotID:              run.SlotID,
			AttemptID:           attempt.AttemptID,
			LogicalStepID:       attempt.LogicalStepID,
			State:               attempt.State,
			Provider:            attempt.Provider,
			Model:               attempt.Model,
			RequestDigest:       attempt.RequestDigest,
			PriceSnapshotID:     attempt.PriceSnapshotID,
			PriceSnapshotDigest: attempt.PriceSnapshotDigest,
			Currency:            attempt.Currency,
			CreatedAt:           attempt.CreatedAt,
			UpdatedAt:           attempt.UpdatedAt,
			Elapsed:             attempt.UpdatedAt.Sub(attempt.CreatedAt),
			Tokens:              attempt.Usage.Tokens.Clone(),
		})
	}
	if uint32(len(report.Attempts)) != projection.Aggregate.AttemptSlotsUsed {
		return fmt.Errorf(
			"%w: Attempt slot aggregate differs from projected facts",
			ErrMetricIntegrity,
		)
	}
	return nil
}

func dispositionFact(
	fallbackRunID string,
	result loopapi.RunResult,
) DispositionFact {
	runID := result.RunID
	if runID == "" {
		runID = fallbackRunID
	}
	return DispositionFact{
		RunID:         runID,
		Disposition:   result.Disposition,
		FrameRevision: result.FrameRevision,
		ReasonCode:    result.ReasonCode,
	}
}

func isNilUsageReader(reader CompositeUsageReader) bool {
	if reader == nil {
		return true
	}
	value := reflect.ValueOf(reader)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func cloneAttemptFact(fact AttemptFact) AttemptFact {
	fact.Tokens = fact.Tokens.Clone()
	return fact
}

func tokenTotalsReport(
	tokens currentstore.CompositeFamilyTokenTotalsV1,
) TokenTotalsReport {
	return TokenTotalsReport{
		Input:         cloneUint64(tokens.Input),
		CachedInput:   cloneUint64(tokens.CachedInput),
		UncachedInput: cloneUint64(tokens.UncachedInput),
		Output:        cloneUint64(tokens.Output),
		Reasoning:     cloneUint64(tokens.Reasoning),
	}
}

func costTotalReport(
	fact currentstore.CompositeFamilyCostTotalV1,
) CostTotalReport {
	result := CostTotalReport{
		Status:     fact.Status,
		Currency:   fact.Currency,
		Currencies: append([]string(nil), fact.Currencies...),
	}
	if fact.Value != nil {
		value := *fact.Value
		result.Value = &value
	}
	return result
}
