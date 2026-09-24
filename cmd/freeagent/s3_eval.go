package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/runscheduler"
	"github.com/endview/freeagent/internal/s3eval"
	"github.com/endview/freeagent/sdk/loopapi"
)

const (
	s3EvalReportSchemaVersionV2 = "freeagent.s3-eval-report/v2"
	maximumS3ScenarioBytes      = 1 << 20
	maximumS3Repetitions        = 100
)

var errS3EvalUnsafeSemanticRepetition = errors.New(
	"S3-C outcome forbids semantic repetition",
)

type s3EvalCommandDependencies struct {
	open s3EvalOpenFunc
	run  s3EvalRunFunc
}

type s3EvalOpenFunc func(
	context.Context,
	string,
	string,
	string,
	productionCompositionOptions,
) (*s3EvalRuntime, error)

type s3EvalRunFunc func(
	context.Context,
	s3eval.ExperimentInput,
	s3eval.ChatFunc,
	s3eval.CompositeUsageReader,
) (s3eval.ExperimentReport, error)

// s3EvalRuntime is only a detached view over one productionComposition. Its
// Chat and Usage dependencies always originate from that same composition.
type s3EvalRuntime struct {
	close func() error
	chat  s3eval.ChatFunc
	usage s3eval.CompositeUsageReader
}

type s3EvalExperimentCommandFact struct {
	ID                   string `json:"id"`
	RepetitionsRequested uint64 `json:"repetitions_requested"`
	RepetitionsAttempted uint64 `json:"repetitions_attempted"`
}

type s3EvalSchedulerCommandFact struct {
	Enabled                bool   `json:"enabled"`
	GlobalWorkers          uint32 `json:"global_workers"`
	WorkspaceWorkers       uint32 `json:"workspace_workers"`
	CompositeFamilyWorkers uint32 `json:"composite_family_workers"`
}

type s3EvalRuntimeCommandFact struct {
	DeepSeekEnabled bool `json:"deepseek_enabled"`
}

type s3EvalRepetitionCommandReport struct {
	Repetition uint64                  `json:"repetition"`
	Report     s3eval.ExperimentReport `json:"report"`
	Error      string                  `json:"error"`
}

type s3EvalCommandReport struct {
	SchemaVersion     string                          `json:"schema_version"`
	Experiment        s3EvalExperimentCommandFact     `json:"experiment"`
	Scenario          s3eval.ScenarioV1               `json:"scenario"`
	Scheduler         s3EvalSchedulerCommandFact      `json:"scheduler"`
	Runtime           s3EvalRuntimeCommandFact        `json:"runtime"`
	RepetitionReports []s3EvalRepetitionCommandReport `json:"repetition_reports"`
	FirstError        string                          `json:"first_error"`
}

func runS3Eval(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runS3EvalWithDependencies(
		ctx,
		args,
		stdout,
		stderr,
		s3EvalCommandDependencies{
			open: openProductionS3EvalRuntime,
			run:  s3eval.Run,
		},
	)
}

func runS3EvalWithDependencies(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	dependencies s3EvalCommandDependencies,
) error {
	if ctx == nil {
		return errors.New("freeagent s3-eval: context is nil")
	}
	if dependencies.open == nil || dependencies.run == nil {
		return errors.New("freeagent s3-eval: dependencies are incomplete")
	}
	flags := newFlagSet("s3-eval", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "content-addressed artifact root")
	scenarioPath := flags.String("scenario", "", "strict S3-C scenario JSON file")
	repetitions := flags.Uint64(
		"repetitions",
		1,
		"number of sequential three-Workspace experiment repetitions (1-100)",
	)
	tenantID := flags.String("tenant", defaultTenantID, "tenant identity")
	principalID := flags.String(
		"principal",
		defaultPrincipalID,
		"principal identity",
	)
	schedulerFlags := bindFairSchedulerFlags(flags)
	deepSeekFlags := bindDeepSeekRuntimeFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*scenarioPath) == "" {
		return errors.New("freeagent s3-eval: --db and --scenario are required")
	}
	if *repetitions == 0 || *repetitions > maximumS3Repetitions {
		return fmt.Errorf(
			"freeagent s3-eval: --repetitions must be between 1 and %d",
			maximumS3Repetitions,
		)
	}
	scenario, err := readS3EvalScenario(*scenarioPath)
	if err != nil {
		return fmt.Errorf("freeagent s3-eval: %w", err)
	}
	input, err := s3eval.ScenarioToExperimentInput(
		scenario,
		*tenantID,
		*principalID,
	)
	if err != nil {
		return fmt.Errorf("freeagent s3-eval: scenario scope: %w", err)
	}
	schedulerConfig, err := schedulerFlags.config(flags, *tenantID)
	if err != nil {
		return fmt.Errorf("freeagent s3-eval: %w", err)
	}
	deepSeekConfig, err := deepSeekFlags.config(flags)
	if err != nil {
		return fmt.Errorf("freeagent s3-eval: %w", err)
	}

	report := newS3EvalCommandReport(
		scenario,
		*repetitions,
		schedulerConfig,
		deepSeekConfig != nil,
	)
	runtime, openErr := dependencies.open(
		ctx,
		*databasePath,
		resolvedArtifactRoot(*databasePath, *artifactRoot),
		*tenantID,
		productionCompositionOptions{
			FairScheduler: schedulerConfig,
			DeepSeek:      deepSeekConfig,
		},
	)
	if openErr != nil {
		report.FirstError = openErr.Error()
		return writeS3EvalReportBeforeError(stdout, report, openErr)
	}
	if runtime == nil || runtime.close == nil || runtime.chat == nil ||
		runtime.usage == nil {
		invalidRuntimeErr := errors.New(
			"production composition returned an incomplete S3-C runtime view",
		)
		if runtime != nil && runtime.close != nil {
			invalidRuntimeErr = errors.Join(invalidRuntimeErr, runtime.close())
		}
		report.FirstError = invalidRuntimeErr.Error()
		return writeS3EvalReportBeforeError(stdout, report, invalidRuntimeErr)
	}

	var executionErr error
	identities := newS3EvalExperimentIdentityGuard()
	for repetition := uint64(1); repetition <= *repetitions; repetition++ {
		experimentReport, runErr := dependencies.run(
			ctx,
			input,
			runtime.chat,
			runtime.usage,
		)
		repetitionReport := s3EvalRepetitionCommandReport{
			Repetition: repetition,
			Report:     experimentReport,
		}
		if identityErr := identities.Observe(experimentReport); identityErr != nil {
			runErr = errors.Join(runErr, identityErr)
		}
		if outcomeErr := s3EvalSemanticRepetitionBlocker(experimentReport); outcomeErr != nil {
			runErr = errors.Join(runErr, outcomeErr)
		}
		if runErr != nil {
			repetitionReport.Error = runErr.Error()
			report.FirstError = runErr.Error()
			executionErr = runErr
		}
		report.RepetitionReports = append(
			report.RepetitionReports,
			repetitionReport,
		)
		report.Experiment.RepetitionsAttempted = repetition
		if runErr != nil {
			// A failed/UNKNOWN model family is durable evidence, not permission
			// to submit a semantically equivalent automatic repetition.
			break
		}
	}
	closeErr := runtime.close()
	if closeErr != nil && report.FirstError == "" {
		report.FirstError = closeErr.Error()
	}
	executionErr = errors.Join(executionErr, closeErr)
	if executionErr != nil {
		return writeS3EvalReportBeforeError(stdout, report, executionErr)
	}
	if err := writeCommandJSON(stdout, report); err != nil {
		return fmt.Errorf("freeagent s3-eval: %w", err)
	}
	return nil
}

// s3EvalExperimentIdentityGuard prevents one exact admission or an accidental
// report replay from being counted as multiple experimental repetitions. It is
// observer-only: identities are read from the returned report and never used
// to create, retry, or mutate a Run.
type s3EvalExperimentIdentityGuard struct {
	requestIDs map[string]struct{}
	rootRunIDs map[string]struct{}
}

func newS3EvalExperimentIdentityGuard() *s3EvalExperimentIdentityGuard {
	return &s3EvalExperimentIdentityGuard{
		requestIDs: make(map[string]struct{}),
		rootRunIDs: make(map[string]struct{}),
	}
}

func (guard *s3EvalExperimentIdentityGuard) Observe(
	report s3eval.ExperimentReport,
) error {
	if guard == nil {
		return errors.New("S3-C experiment identity guard is nil")
	}
	localRequests := make(map[string]struct{}, len(report.Families))
	localRoots := make(map[string]struct{}, len(report.Families))
	var found []error
	for _, family := range report.Families {
		workspaceID := family.WorkspaceID
		if family.RequestID == "" {
			found = append(found, fmt.Errorf(
				"S3-C evidence integrity: Workspace %q has no Request ID",
				workspaceID,
			))
		} else if _, duplicate := localRequests[family.RequestID]; duplicate {
			found = append(found, fmt.Errorf(
				"S3-C evidence integrity: Request ID %q is duplicated within one repetition",
				family.RequestID,
			))
		} else if _, replayed := guard.requestIDs[family.RequestID]; replayed {
			found = append(found, fmt.Errorf(
				"S3-C evidence integrity: Request ID %q was replayed across repetitions",
				family.RequestID,
			))
		} else {
			localRequests[family.RequestID] = struct{}{}
		}

		if family.RootRunID == "" {
			found = append(found, fmt.Errorf(
				"S3-C evidence integrity: Workspace %q has no root Run ID",
				workspaceID,
			))
		} else if _, duplicate := localRoots[family.RootRunID]; duplicate {
			found = append(found, fmt.Errorf(
				"S3-C evidence integrity: root Run ID %q is duplicated within one repetition",
				family.RootRunID,
			))
		} else if _, replayed := guard.rootRunIDs[family.RootRunID]; replayed {
			found = append(found, fmt.Errorf(
				"S3-C evidence integrity: root Run ID %q was replayed across repetitions",
				family.RootRunID,
			))
		} else {
			localRoots[family.RootRunID] = struct{}{}
		}
	}
	if err := errors.Join(found...); err != nil {
		return err
	}
	for identity := range localRequests {
		guard.requestIDs[identity] = struct{}{}
	}
	for identity := range localRoots {
		guard.rootRunIDs[identity] = struct{}{}
	}
	return nil
}

// s3EvalSemanticRepetitionBlocker permits another requested repetition only
// after the previous three-Workspace outcome is completely and observably
// terminal. In particular, MODEL_UNKNOWN is a durable ambiguity and never an
// implicit invitation to submit the same scenario under fresh Request IDs.
func s3EvalSemanticRepetitionBlocker(report s3eval.ExperimentReport) error {
	if len(report.Families) != s3eval.WorkspaceCount {
		return fmt.Errorf(
			"%w: report contains %d Workspace families, want %d",
			errS3EvalUnsafeSemanticRepetition,
			len(report.Families),
			s3eval.WorkspaceCount,
		)
	}
	for _, family := range report.Families {
		if family.Error != "" || family.Failure != "" {
			return fmt.Errorf(
				"%w: Workspace %q has a failed or incomplete family",
				errS3EvalUnsafeSemanticRepetition,
				family.WorkspaceID,
			)
		}
		if len(family.Children) < 2 || len(family.Children) > 8 {
			return fmt.Errorf(
				"%w: Workspace %q contains %d Specialist children, want 2-8",
				errS3EvalUnsafeSemanticRepetition,
				family.WorkspaceID,
				len(family.Children),
			)
		}
		if family.RootRunID == "" || family.Root.RunID != family.RootRunID ||
			family.Reply == "" ||
			family.Root.Disposition != loopapi.DispositionTerminated {
			return fmt.Errorf(
				"%w: Workspace %q root is not a complete terminal reply",
				errS3EvalUnsafeSemanticRepetition,
				family.WorkspaceID,
			)
		}

		expectedRoles := map[string]corecontract.CompositeRunRoleV1{
			family.RootRunID: corecontract.CompositeRunRoleRootV1,
		}
		for _, child := range family.Children {
			if child.RunID == "" || child.Disposition != loopapi.DispositionTerminated {
				return fmt.Errorf(
					"%w: Workspace %q child Run %q is %q",
					errS3EvalUnsafeSemanticRepetition,
					family.WorkspaceID,
					child.RunID,
					child.Disposition,
				)
			}
			if _, duplicate := expectedRoles[child.RunID]; duplicate {
				return fmt.Errorf(
					"%w: Workspace %q repeats Composite Run %q",
					errS3EvalUnsafeSemanticRepetition,
					family.WorkspaceID,
					child.RunID,
				)
			}
			expectedRoles[child.RunID] = corecontract.CompositeRunRoleChildV1
		}
		if family.Reviewer != nil {
			if family.Reviewer.RunID == "" ||
				family.Reviewer.Disposition != loopapi.DispositionTerminated {
				return fmt.Errorf(
					"%w: Workspace %q Reviewer Run %q is %q",
					errS3EvalUnsafeSemanticRepetition,
					family.WorkspaceID,
					family.Reviewer.RunID,
					family.Reviewer.Disposition,
				)
			}
			if _, duplicate := expectedRoles[family.Reviewer.RunID]; duplicate {
				return fmt.Errorf(
					"%w: Workspace %q repeats Composite Run %q",
					errS3EvalUnsafeSemanticRepetition,
					family.WorkspaceID,
					family.Reviewer.RunID,
				)
			}
			expectedRoles[family.Reviewer.RunID] =
				corecontract.CompositeRunRoleReviewerV1
		}
		if len(family.Attempts) != len(expectedRoles) {
			return fmt.Errorf(
				"%w: Workspace %q contains %d model Attempts for %d Composite Runs",
				errS3EvalUnsafeSemanticRepetition,
				family.WorkspaceID,
				len(family.Attempts),
				len(expectedRoles),
			)
		}
		attemptsByRun := make(map[string]s3eval.AttemptFact, len(family.Attempts))
		for _, attempt := range family.Attempts {
			expectedRole, expected := expectedRoles[attempt.RunID]
			if attempt.State != corecontract.ModelAttemptSucceeded {
				return fmt.Errorf(
					"%w: Workspace %q model Attempt %q is %q",
					errS3EvalUnsafeSemanticRepetition,
					family.WorkspaceID,
					attempt.AttemptID,
					attempt.State,
				)
			}
			if attempt.AttemptID == "" || !expected || attempt.Role != expectedRole {
				return fmt.Errorf(
					"%w: Workspace %q model Attempt %q does not close Run %q",
					errS3EvalUnsafeSemanticRepetition,
					family.WorkspaceID,
					attempt.AttemptID,
					attempt.RunID,
				)
			}
			if _, duplicate := attemptsByRun[attempt.RunID]; duplicate {
				return fmt.Errorf(
					"%w: Workspace %q repeats the model Attempt for Run %q",
					errS3EvalUnsafeSemanticRepetition,
					family.WorkspaceID,
					attempt.RunID,
				)
			}
			attemptsByRun[attempt.RunID] = attempt
		}
		if len(family.Results) != len(expectedRoles) {
			return fmt.Errorf(
				"%w: Workspace %q contains %d terminal results for %d Composite Runs",
				errS3EvalUnsafeSemanticRepetition,
				family.WorkspaceID,
				len(family.Results),
				len(expectedRoles),
			)
		}
		resultsByRun := make(map[string]struct{}, len(family.Results))
		for _, result := range family.Results {
			expectedRole, expected := expectedRoles[result.RunID]
			attempt, hasAttempt := attemptsByRun[result.RunID]
			if !expected || !hasAttempt || result.Role != expectedRole ||
				result.AttemptID != attempt.AttemptID || result.ResultDigest == "" ||
				result.AssistantText == "" ||
				(expectedRole == corecontract.CompositeRunRoleReviewerV1) !=
					(result.ReviewerVerdict != nil) {
				return fmt.Errorf(
					"%w: Workspace %q terminal result does not close Run %q",
					errS3EvalUnsafeSemanticRepetition,
					family.WorkspaceID,
					result.RunID,
				)
			}
			if expectedRole == corecontract.CompositeRunRoleReviewerV1 &&
				result.ReviewerVerdict.Decision != corecontract.ReviewDecisionApproveV1 {
				return fmt.Errorf(
					"%w: Workspace %q Reviewer Run %q did not APPROVE",
					errS3EvalUnsafeSemanticRepetition,
					family.WorkspaceID,
					result.RunID,
				)
			}
			if _, duplicate := resultsByRun[result.RunID]; duplicate {
				return fmt.Errorf(
					"%w: Workspace %q repeats the terminal result for Run %q",
					errS3EvalUnsafeSemanticRepetition,
					family.WorkspaceID,
					result.RunID,
				)
			}
			resultsByRun[result.RunID] = struct{}{}
		}
	}
	return nil
}

func openProductionS3EvalRuntime(
	ctx context.Context,
	databasePath string,
	artifactRoot string,
	tenantID string,
	options productionCompositionOptions,
) (*s3EvalRuntime, error) {
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		tenantID,
		options,
	)
	if err != nil {
		return nil, err
	}
	if composition.composite == nil || composition.store == nil {
		return nil, errors.Join(
			errors.New("production Composite service is unavailable"),
			composition.Close(),
		)
	}
	return &s3EvalRuntime{
		close: composition.Close,
		chat:  composition.composite.Chat,
		usage: composition.store,
	}, nil
}

func readS3EvalScenario(path string) (s3eval.ScenarioV1, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return s3eval.ScenarioV1{}, fmt.Errorf("resolve scenario: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return s3eval.ScenarioV1{}, fmt.Errorf("resolve scenario: %w", err)
	}
	if !samePath(absolute, resolved) {
		return s3eval.ScenarioV1{}, errors.New(
			"scenario path must not traverse symbolic links",
		)
	}
	linkInformation, err := os.Lstat(resolved)
	if err != nil {
		return s3eval.ScenarioV1{}, fmt.Errorf("inspect scenario: %w", err)
	}
	if linkInformation.Mode()&os.ModeSymlink != 0 ||
		!linkInformation.Mode().IsRegular() {
		return s3eval.ScenarioV1{}, errors.New("scenario must be a regular file")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return s3eval.ScenarioV1{}, fmt.Errorf("open scenario: %w", err)
	}
	information, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return s3eval.ScenarioV1{}, fmt.Errorf("inspect scenario: %w", statErr)
	}
	if !information.Mode().IsRegular() ||
		!os.SameFile(linkInformation, information) {
		_ = file.Close()
		return s3eval.ScenarioV1{}, errors.New("scenario must be a regular file")
	}
	if information.Size() <= 0 || information.Size() > maximumS3ScenarioBytes {
		_ = file.Close()
		return s3eval.ScenarioV1{}, fmt.Errorf(
			"scenario must contain 1-%d bytes",
			maximumS3ScenarioBytes,
		)
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, maximumS3ScenarioBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return s3eval.ScenarioV1{}, fmt.Errorf("read scenario: %w", err)
	}
	if len(payload) == 0 || len(payload) > maximumS3ScenarioBytes {
		return s3eval.ScenarioV1{}, fmt.Errorf(
			"scenario must contain 1-%d bytes",
			maximumS3ScenarioBytes,
		)
	}
	scenario, err := s3eval.ParseScenarioV1(payload)
	if err != nil {
		return s3eval.ScenarioV1{}, fmt.Errorf("parse scenario: %w", err)
	}
	return scenario, nil
}

func newS3EvalCommandReport(
	scenario s3eval.ScenarioV1,
	repetitions uint64,
	schedulerConfig *runscheduler.Config,
	deepSeekEnabled bool,
) s3EvalCommandReport {
	report := s3EvalCommandReport{
		SchemaVersion: s3EvalReportSchemaVersionV2,
		Experiment: s3EvalExperimentCommandFact{
			ID:                   scenario.ExperimentID,
			RepetitionsRequested: repetitions,
		},
		Scenario:          scenario,
		Runtime:           s3EvalRuntimeCommandFact{DeepSeekEnabled: deepSeekEnabled},
		RepetitionReports: make([]s3EvalRepetitionCommandReport, 0, repetitions),
	}
	if schedulerConfig != nil {
		report.Scheduler = s3EvalSchedulerCommandFact{
			Enabled:                true,
			GlobalWorkers:          schedulerConfig.Limits.GlobalWorkers,
			WorkspaceWorkers:       schedulerConfig.Limits.MaxActivePerWorkspace,
			CompositeFamilyWorkers: schedulerConfig.Limits.MaxActivePerFamily,
		}
	}
	return report
}

func writeS3EvalReportBeforeError(
	stdout io.Writer,
	report s3EvalCommandReport,
	cause error,
) error {
	writeErr := writeCommandJSON(stdout, report)
	return fmt.Errorf(
		"freeagent s3-eval: %w",
		errors.Join(cause, writeErr),
	)
}
