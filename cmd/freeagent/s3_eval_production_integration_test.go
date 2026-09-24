package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/internal/runscheduler"
	"github.com/endview/freeagent/internal/s3eval"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	s3CDeepSeekOfficialURL = "https://api.deepseek.com/chat/completions"
	s3CDeepSeekTestKey     = "not-a-secret"

	s3CAssignmentPrefix          = "COMPOSITE_SPECIALIST_ASSIGNMENT_JSON:\n"
	s3CReviewerPolicyPrefix      = "COMPOSITE_REVIEW_POLICY_JSON:\n"
	s3CReviewerResultPrefix      = "UNTRUSTED_COMPOSITE_SPECIALIST_RESULT_JSON:\n"
	s3CChildResultPrefix         = "UNTRUSTED_COMPOSITE_CHILD_RESULT_JSON:\n"
	s3CReviewVerdictPrefix       = "UNTRUSTED_COMPOSITE_REVIEW_VERDICT_JSON:\n"
	s3CAssignmentSchema          = "composite-specialist-assignment-context/v1"
	s3CReviewerPolicySchema      = "composite-review-policy-context/v1"
	s3CReviewerResultSchema      = "composite-review-specialist-result-context/v1"
	s3CChildResultSchema         = "composite-child-result-context/v1"
	s3CDeepSeekRoleSpecialist    = "SPECIALIST"
	s3CDeepSeekRoleReviewer      = "REVIEWER"
	s3CDeepSeekRoleRoot          = "ROOT"
	s3CReviewerOnCallsPerFamily  = 5
	s3CReviewerOffCallsPerFamily = 4
	s3CFairInitialWindowLimit    = 5
)

func TestS3EvalProductionCompositionDeepSeekReviewerLongChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		s3CProductionTestTimeout(),
	)
	defer cancel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	exampleRoot := filepath.Dir(exampleSeedPath(t))
	seedPath := filepath.Join(
		exampleRoot,
		"s3c-deepseek-v4-flash-reviewer-on.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize S3-C production data: %v", err)
	}

	fake := &s3CDeepSeekRoundTripper{model: deepseekmodel.ModelV4Flash}
	schedulerConfig := runscheduler.Config{
		TenantID: defaultTenantID,
		Limits: currentstore.FairSchedulerLimits{
			GlobalWorkers:         2,
			MaxActivePerWorkspace: 1,
			MaxActivePerFamily:    1,
		},
		PollInterval: time.Millisecond,
	}
	keyResolver := deepseekmodel.APIKeyResolverFunc(func(
		ctx context.Context,
		identity deepseekmodel.APIKeyIdentity,
	) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if identity.Provider != deepseekmodel.ProviderNameV1 ||
			identity.Model != deepseekmodel.ModelV4Flash ||
			identity.ModelBuildID != localDeepSeekFlashBuild {
			return nil, fmt.Errorf(
				"unexpected frozen DeepSeek identity: %+v",
				identity,
			)
		}
		return []byte(s3CDeepSeekTestKey), nil
	})
	composition, err := openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
		productionCompositionOptions{
			FairScheduler: &schedulerConfig,
			DeepSeek: &productionDeepSeekRuntimeConfig{
				APIKeyResolver: keyResolver,
				HTTPClient: &http.Client{
					Transport: fake,
					Timeout:   5 * time.Second,
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("open S3-C production composition: %v", err)
	}
	defer func() {
		if closeErr := composition.Close(); closeErr != nil {
			t.Errorf("close S3-C production composition: %v", closeErr)
		}
	}()

	scenario, err := readS3EvalScenario(filepath.Join(
		exampleRoot,
		"s3c-architecture-clean.scenario.json",
	))
	if err != nil {
		t.Fatalf("read S3-C clean scenario: %v", err)
	}
	input, err := s3eval.ScenarioToExperimentInput(
		scenario,
		defaultTenantID,
		defaultPrincipalID,
	)
	if err != nil {
		t.Fatalf("freeze S3-C experiment input: %v", err)
	}
	report, err := s3eval.Run(
		ctx,
		input,
		composition.composite.Chat,
		composition.store,
	)
	if err != nil {
		t.Fatalf("run S3-C production experiment: %v", err)
	}

	calls := fake.snapshot()
	roleCounts := map[string]int{}
	for _, call := range calls {
		roleCounts[call.Role]++
	}
	if len(calls) != 15 ||
		roleCounts[s3CDeepSeekRoleSpecialist] != 9 ||
		roleCounts[s3CDeepSeekRoleReviewer] != 3 ||
		roleCounts[s3CDeepSeekRoleRoot] != 3 {
		t.Fatalf(
			"DeepSeek calls=%d roles=%v, want 15 and Specialist/Reviewer/Root=9/3/3",
			len(calls),
			roleCounts,
		)
	}
	assertS3CProductionExperimentReport(t, report)
	assertS3CDeepSeekCallDataflow(t, calls)
}

func TestS3EvalCLIProductionDeepSeekProReviewerOffTrap(t *testing.T) {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		s3CProductionTestTimeout(),
	)
	defer cancel()

	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	exampleRoot := filepath.Dir(exampleSeedPath(t))
	seedPath := filepath.Join(
		exampleRoot,
		"s3c-deepseek-v4-pro-reviewer-off.bootstrap.seed.json",
	)
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath: databasePath,
		SeedPath:     seedPath,
		ArtifactRoot: artifactRoot,
	}); err != nil {
		t.Fatalf("initialize S3-C Pro Reviewer-off production data: %v", err)
	}

	fake := &s3CDeepSeekRoundTripper{model: deepseekmodel.ModelV4Pro}
	closed := false
	var output bytes.Buffer
	err := runS3EvalWithDependencies(
		ctx,
		[]string{
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--scenario", filepath.Join(exampleRoot, "s3c-architecture-trap.scenario.json"),
			"--repetitions", "2",
			"--enable-fair-scheduler",
			"--scheduler-global-workers", "2",
			"--scheduler-workspace-workers", "1",
			"--scheduler-family-workers", "1",
			"--enable-deepseek",
			"--deepseek-api-key-env", "S3C_OFFLINE_UNUSED_KEY",
		},
		&output,
		io.Discard,
		s3EvalCommandDependencies{
			open: func(
				ctx context.Context,
				databasePath string,
				artifactRoot string,
				tenantID string,
				options productionCompositionOptions,
			) (*s3EvalRuntime, error) {
				if options.FairScheduler == nil || options.DeepSeek == nil ||
					options.FairScheduler.Limits.GlobalWorkers != 2 ||
					options.FairScheduler.Limits.MaxActivePerWorkspace != 1 ||
					options.FairScheduler.Limits.MaxActivePerFamily != 1 {
					return nil, fmt.Errorf("unexpected S3-C CLI production options")
				}
				keyResolver := deepseekmodel.APIKeyResolverFunc(func(
					ctx context.Context,
					identity deepseekmodel.APIKeyIdentity,
				) ([]byte, error) {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if identity.Provider != deepseekmodel.ProviderNameV1 ||
						identity.Model != deepseekmodel.ModelV4Pro ||
						identity.ModelBuildID != localDeepSeekProBuild {
						return nil, fmt.Errorf(
							"unexpected frozen DeepSeek Pro identity: %+v",
							identity,
						)
					}
					return []byte(s3CDeepSeekTestKey), nil
				})
				options.DeepSeek = &productionDeepSeekRuntimeConfig{
					APIKeyResolver: keyResolver,
					HTTPClient: &http.Client{
						Transport: fake,
						Timeout:   5 * time.Second,
					},
				}
				runtime, err := openProductionS3EvalRuntime(
					ctx,
					databasePath,
					artifactRoot,
					tenantID,
					options,
				)
				if err != nil {
					return nil, err
				}
				originalClose := runtime.close
				runtime.close = func() error {
					closed = true
					return originalClose()
				}
				return runtime, nil
			},
			run: s3eval.Run,
		},
	)
	if err != nil {
		t.Fatalf("run S3-C Pro Reviewer-off CLI production experiment: %v\n%s", err, output.String())
	}
	if !closed {
		t.Fatal("S3-C Pro Reviewer-off CLI did not close production composition")
	}
	var commandReport s3EvalCommandReport
	if err := json.Unmarshal(output.Bytes(), &commandReport); err != nil {
		t.Fatalf("decode S3-C Pro Reviewer-off CLI report: %v\n%s", err, output.String())
	}
	if commandReport.SchemaVersion != s3EvalReportSchemaVersionV2 ||
		commandReport.FirstError != "" ||
		commandReport.Experiment.RepetitionsRequested != 2 ||
		commandReport.Experiment.RepetitionsAttempted != 2 ||
		len(commandReport.RepetitionReports) != 2 ||
		!commandReport.Scheduler.Enabled || !commandReport.Runtime.DeepSeekEnabled {
		t.Fatalf("S3-C Pro Reviewer-off CLI report header=%+v", commandReport)
	}

	calls := fake.snapshot()
	roleCounts := map[string]int{}
	for _, call := range calls {
		roleCounts[call.Role]++
	}
	if len(calls) != 24 ||
		roleCounts[s3CDeepSeekRoleSpecialist] != 18 ||
		roleCounts[s3CDeepSeekRoleReviewer] != 0 ||
		roleCounts[s3CDeepSeekRoleRoot] != 6 {
		t.Fatalf(
			"Pro Reviewer-off calls=%d roles=%v, want 24 and Specialist/Reviewer/Root=18/0/6",
			len(calls),
			roleCounts,
		)
	}
	seenRequestIDs := make(map[string]struct{}, 6)
	seenRootRunIDs := make(map[string]struct{}, 6)
	for index, repetition := range commandReport.RepetitionReports {
		assertS3CReviewerOffProductionReport(t, repetition.Report)
		assertS3CDeepSeekReviewerOffDataflow(
			t,
			calls[index*12:(index+1)*12],
		)
		for _, family := range repetition.Report.Families {
			if _, duplicate := seenRequestIDs[family.RequestID]; duplicate {
				t.Fatalf("production repetition replayed Request ID %q", family.RequestID)
			}
			seenRequestIDs[family.RequestID] = struct{}{}
			if _, duplicate := seenRootRunIDs[family.RootRunID]; duplicate {
				t.Fatalf("production repetition replayed root Run ID %q", family.RootRunID)
			}
			seenRootRunIDs[family.RootRunID] = struct{}{}
		}
	}
}

func assertS3CReviewerOffProductionReport(
	t *testing.T,
	report s3eval.ExperimentReport,
) {
	t.Helper()
	if len(report.Families) != s3eval.WorkspaceCount ||
		len(report.ServiceOrder) != 12 || report.Fairness.Starvation ||
		report.Fairness.JainIndex == nil || *report.Fairness.JainIndex != 1 {
		t.Fatalf("Pro Reviewer-off report=%+v", report)
	}
	assertS3CFairServiceOrder(t, report, s3CReviewerOffCallsPerFamily)
	for _, family := range report.Families {
		if family.Error != "" || family.Failure != "" || family.Reply == "" ||
			len(family.Children) != 3 || family.Reviewer != nil ||
			family.Root.Disposition != loopapi.DispositionTerminated ||
			len(family.Attempts) != 4 || len(family.Results) != 4 {
			t.Fatalf("Pro Reviewer-off family %s=%+v", family.WorkspaceID, family)
		}
		assertS3CToken(t, "Pro input", family.Tokens.Totals.Input, 400)
		assertS3CToken(t, "Pro cached input", family.Tokens.Totals.CachedInput, 160)
		assertS3CToken(t, "Pro uncached input", family.Tokens.Totals.UncachedInput, 240)
		assertS3CToken(t, "Pro output", family.Tokens.Totals.Output, 80)
		assertS3CToken(t, "Pro reasoning", family.Tokens.Totals.Reasoning, 20)
		if family.Tokens.CacheHitRatio == nil || *family.Tokens.CacheHitRatio != 0.4 {
			t.Fatalf("Pro Reviewer-off family %s cache ratio=%v", family.WorkspaceID, family.Tokens.CacheHitRatio)
		}
		attemptRoles := map[corecontract.CompositeRunRoleV1]int{}
		var childLastOrder, rootOrder uint64
		for _, attempt := range family.Attempts {
			attemptRoles[attempt.Role]++
			if attempt.Role == corecontract.CompositeRunRoleChildV1 &&
				attempt.ServiceOrder > childLastOrder {
				childLastOrder = attempt.ServiceOrder
			}
			if attempt.Role == corecontract.CompositeRunRoleRootV1 {
				rootOrder = attempt.ServiceOrder
			}
			if attempt.State != corecontract.ModelAttemptSucceeded ||
				attempt.Model != deepseekmodel.ModelV4Pro ||
				attempt.Provider != deepseekmodel.ProviderNameV1 {
				t.Fatalf("Pro Reviewer-off family %s Attempt=%+v", family.WorkspaceID, attempt)
			}
		}
		if attemptRoles[corecontract.CompositeRunRoleChildV1] != 3 ||
			attemptRoles[corecontract.CompositeRunRoleReviewerV1] != 0 ||
			attemptRoles[corecontract.CompositeRunRoleRootV1] != 1 ||
			childLastOrder == 0 || rootOrder == 0 || childLastOrder >= rootOrder {
			t.Fatalf("Pro Reviewer-off family %s Attempt roles/order=%v %d/%d", family.WorkspaceID, attemptRoles, childLastOrder, rootOrder)
		}

		resultRoles := map[corecontract.CompositeRunRoleV1]int{}
		for _, result := range family.Results {
			resultRoles[result.Role]++
			if result.ReviewerVerdict != nil || result.AssistantText == "" ||
				!moduleapi.ValidSHA256(result.ResultDigest) {
				t.Fatalf("Pro Reviewer-off family %s Result=%+v", family.WorkspaceID, result)
			}
		}
		if resultRoles[corecontract.CompositeRunRoleChildV1] != 3 ||
			resultRoles[corecontract.CompositeRunRoleReviewerV1] != 0 ||
			resultRoles[corecontract.CompositeRunRoleRootV1] != 1 {
			t.Fatalf("Pro Reviewer-off family %s Result roles=%v", family.WorkspaceID, resultRoles)
		}
	}
}

func assertS3CProductionExperimentReport(
	t *testing.T,
	report s3eval.ExperimentReport,
) {
	t.Helper()
	if len(report.Families) != s3eval.WorkspaceCount ||
		len(report.ServiceOrder) != 15 {
		t.Fatalf(
			"family/service counts=%d/%d, want 3/15",
			len(report.Families),
			len(report.ServiceOrder),
		)
	}
	if report.Fairness.Starvation ||
		len(report.Fairness.StarvedWorkspaces) != 0 ||
		report.Fairness.JainIndex == nil ||
		*report.Fairness.JainIndex != 1 {
		t.Fatalf("fairness=%+v", report.Fairness)
	}
	assertS3CFairServiceOrder(t, report, s3CReviewerOnCallsPerFamily)

	for _, family := range report.Families {
		if family.Error != "" || family.Failure != "" || family.Reply == "" ||
			len(family.Children) != 3 || family.Reviewer == nil ||
			family.Reviewer.Disposition != loopapi.DispositionTerminated ||
			family.Root.Disposition != loopapi.DispositionTerminated ||
			len(family.Attempts) != 5 || len(family.Results) != 5 {
			t.Fatalf("family %s terminal report=%+v", family.WorkspaceID, family)
		}
		for _, child := range family.Children {
			if child.Disposition != loopapi.DispositionTerminated {
				t.Fatalf("family %s Child=%+v", family.WorkspaceID, child)
			}
		}

		assertS3CToken(t, "input", family.Tokens.Totals.Input, 500)
		assertS3CToken(t, "cached input", family.Tokens.Totals.CachedInput, 200)
		assertS3CToken(t, "uncached input", family.Tokens.Totals.UncachedInput, 300)
		assertS3CToken(t, "output", family.Tokens.Totals.Output, 100)
		assertS3CToken(t, "reasoning", family.Tokens.Totals.Reasoning, 25)
		if family.Tokens.CacheHitRatio == nil ||
			*family.Tokens.CacheHitRatio != 0.4 {
			t.Fatalf("family %s cache ratio=%v", family.WorkspaceID, family.Tokens.CacheHitRatio)
		}
		roles := map[corecontract.CompositeRunRoleV1]int{}
		var childLastOrder, reviewerOrder, rootOrder uint64
		for _, attempt := range family.Attempts {
			roles[attempt.Role]++
			switch attempt.Role {
			case corecontract.CompositeRunRoleChildV1:
				if attempt.ServiceOrder > childLastOrder {
					childLastOrder = attempt.ServiceOrder
				}
			case corecontract.CompositeRunRoleReviewerV1:
				reviewerOrder = attempt.ServiceOrder
			case corecontract.CompositeRunRoleRootV1:
				rootOrder = attempt.ServiceOrder
			}
			if attempt.State != corecontract.ModelAttemptSucceeded ||
				attempt.Provider != deepseekmodel.ProviderNameV1 ||
				attempt.Model != deepseekmodel.ModelV4Flash {
				t.Fatalf("family %s Attempt=%+v", family.WorkspaceID, attempt)
			}
		}
		if roles[corecontract.CompositeRunRoleChildV1] != 3 ||
			roles[corecontract.CompositeRunRoleReviewerV1] != 1 ||
			roles[corecontract.CompositeRunRoleRootV1] != 1 {
			t.Fatalf("family %s Attempt roles=%v", family.WorkspaceID, roles)
		}
		if childLastOrder == 0 || reviewerOrder == 0 || rootOrder == 0 ||
			childLastOrder >= reviewerOrder || reviewerOrder >= rootOrder {
			t.Fatalf(
				"family %s service dependency order ChildLast/Reviewer/Root=%d/%d/%d",
				family.WorkspaceID,
				childLastOrder,
				reviewerOrder,
				rootOrder,
			)
		}
		resultRoles := map[corecontract.CompositeRunRoleV1]int{}
		for _, result := range family.Results {
			resultRoles[result.Role]++
			if result.AssistantText == "" ||
				!moduleapi.ValidSHA256(result.ResultDigest) {
				t.Fatalf("family %s Result=%+v", family.WorkspaceID, result)
			}
			if result.Role == corecontract.CompositeRunRoleReviewerV1 &&
				(result.ReviewerVerdict == nil ||
					result.ReviewerVerdict.Decision != corecontract.ReviewDecisionApproveV1) {
				t.Fatalf("family %s Reviewer Result=%+v", family.WorkspaceID, result)
			}
		}
		if resultRoles[corecontract.CompositeRunRoleChildV1] != 3 ||
			resultRoles[corecontract.CompositeRunRoleReviewerV1] != 1 ||
			resultRoles[corecontract.CompositeRunRoleRootV1] != 1 {
			t.Fatalf("family %s Result roles=%v", family.WorkspaceID, resultRoles)
		}
	}
}

func assertS3CFairServiceOrder(
	t *testing.T,
	report s3eval.ExperimentReport,
	expectedCallsPerWorkspace uint64,
) {
	t.Helper()
	if len(report.Fairness.Workspaces) != s3eval.WorkspaceCount {
		t.Fatalf("fairness Workspaces=%+v", report.Fairness.Workspaces)
	}
	counts := make(map[string]uint64, s3eval.WorkspaceCount)
	firstOrders := make(map[string]uint64, s3eval.WorkspaceCount)
	for _, workspace := range report.Fairness.Workspaces {
		if workspace.ServiceCount != expectedCallsPerWorkspace ||
			workspace.FirstServiceOrder == nil ||
			*workspace.FirstServiceOrder == 0 ||
			*workspace.FirstServiceOrder > s3CFairInitialWindowLimit {
			t.Fatalf(
				"Workspace fairness=%+v first_service_order=%v",
				workspace,
				workspace.FirstServiceOrder,
			)
		}
		counts[workspace.WorkspaceID] = 0
		firstOrders[workspace.WorkspaceID] = *workspace.FirstServiceOrder
	}

	// The three concurrent Chat calls cross independent atomic Admissions.
	// With two workers, one admitted family may receive a bounded head start
	// before the third family becomes visible. The initial window and prefix
	// bounds below reject family monopoly without pretending Admission commit
	// order is itself a Scheduler fairness decision.
	initialWindow := make(map[string]struct{}, s3eval.WorkspaceCount)
	activeWorkspace := ""
	var activeCount, longestCount uint64
	longestWorkspace := ""
	for index, attempt := range report.ServiceOrder {
		if _, known := counts[attempt.WorkspaceID]; !known {
			t.Fatalf("service order references unknown Workspace %q", attempt.WorkspaceID)
		}
		counts[attempt.WorkspaceID]++
		if counts[attempt.WorkspaceID] == 1 &&
			firstOrders[attempt.WorkspaceID] != uint64(index+1) {
			t.Fatalf(
				"Workspace %s first service=%d, report says %d",
				attempt.WorkspaceID,
				index+1,
				firstOrders[attempt.WorkspaceID],
			)
		}
		if index < s3CFairInitialWindowLimit {
			initialWindow[attempt.WorkspaceID] = struct{}{}
		}
		if attempt.WorkspaceID == activeWorkspace {
			activeCount++
		} else {
			activeWorkspace = attempt.WorkspaceID
			activeCount = 1
		}
		if activeCount > longestCount {
			longestCount = activeCount
			longestWorkspace = activeWorkspace
		}

		minimum := ^uint64(0)
		var maximum uint64
		for _, count := range counts {
			if count < minimum {
				minimum = count
			}
			if count > maximum {
				maximum = count
			}
		}
		if maximum-minimum > 2 {
			t.Fatalf(
				"unfair service prefix %d counts=%v: max-min=%d",
				index+1,
				counts,
				maximum-minimum,
			)
		}
	}
	if len(initialWindow) != s3eval.WorkspaceCount {
		t.Fatalf(
			"initial service window did not cover every Workspace: %v",
			initialWindow,
		)
	}
	if report.Fairness.FirstServedWorkspace != report.ServiceOrder[0].WorkspaceID ||
		report.Fairness.LongestConsecutiveCount != longestCount ||
		report.Fairness.LongestConsecutiveWorkspace != longestWorkspace ||
		longestCount > 2 {
		t.Fatalf(
			"fair service burst=%s/%d, recomputed=%s/%d, first=%q",
			report.Fairness.LongestConsecutiveWorkspace,
			report.Fairness.LongestConsecutiveCount,
			longestWorkspace,
			longestCount,
			report.Fairness.FirstServedWorkspace,
		)
	}
}

func assertS3CToken(t *testing.T, name string, value *uint64, want uint64) {
	t.Helper()
	if value == nil || *value != want {
		t.Fatalf("%s tokens=%v, want %d", name, value, want)
	}
}

func assertS3CDeepSeekCallDataflow(
	t *testing.T,
	calls []s3CDeepSeekCallFact,
) {
	t.Helper()
	type expectedSpecialist struct {
		focus  string
		weight uint32
	}
	expected := map[string]expectedSpecialist{
		"backend":  {focus: "backend", weight: 3333},
		"frontend": {focus: "frontend", weight: 3334},
		"network":  {focus: "network", weight: 3333},
	}
	specialistsByOutput := make(map[string]s3CDeepSeekCallFact, 9)
	reviewersByVerdict := make(map[string]s3CDeepSeekCallFact, 3)
	roots := make([]s3CDeepSeekCallFact, 0, 3)
	for _, call := range calls {
		switch call.Role {
		case s3CDeepSeekRoleSpecialist:
			want, present := expected[call.SlotID]
			if !present || call.FocusID != want.focus || call.Output == "" {
				t.Fatalf("invalid Specialist call=%+v", call)
			}
			if _, duplicate := specialistsByOutput[call.Output]; duplicate {
				t.Fatalf("duplicate Specialist output %q", call.Output)
			}
			specialistsByOutput[call.Output] = call
		case s3CDeepSeekRoleReviewer:
			if call.Output == "" || call.ReviewerPolicy == nil {
				t.Fatalf("incomplete Reviewer call=%+v", call)
			}
			if _, duplicate := reviewersByVerdict[call.Output]; duplicate {
				t.Fatalf("duplicate Reviewer verdict %q", call.Output)
			}
			reviewersByVerdict[call.Output] = call
		case s3CDeepSeekRoleRoot:
			roots = append(roots, call)
		default:
			t.Fatalf("unknown DeepSeek call role=%q", call.Role)
		}
	}
	if len(specialistsByOutput) != 9 || len(reviewersByVerdict) != 3 ||
		len(roots) != 3 {
		t.Fatalf(
			"DeepSeek call topology Specialists/Reviewers/Roots=%d/%d/%d",
			len(specialistsByOutput),
			len(reviewersByVerdict),
			len(roots),
		)
	}

	specialistReviewerUses := make(map[string]int, len(specialistsByOutput))
	specialistRootUses := make(map[string]int, len(specialistsByOutput))
	for verdictCanonical, reviewer := range reviewersByVerdict {
		verdict, canonical, err := corecontract.ParseReviewVerdictV1(
			[]byte(verdictCanonical),
		)
		if err != nil || string(canonical) != verdictCanonical ||
			verdict.Decision != corecontract.ReviewDecisionApproveV1 ||
			verdict.FamilyDigest != reviewer.ReviewerPolicy.FamilyDigest ||
			verdict.SpecialistResultDigest !=
				reviewer.ReviewerPolicy.SpecialistResultDigest {
			t.Fatalf("Reviewer verdict does not close policy: call=%+v err=%v", reviewer, err)
		}
		seenSlots := make(map[string]struct{}, len(expected))
		for _, result := range reviewer.ReviewerResults {
			want, present := expected[result.SlotID]
			if !present || result.SchemaVersion != s3CReviewerResultSchema ||
				result.FocusID != want.focus ||
				result.WeightBasisPoints != want.weight || result.Result == "" ||
				result.RunID == "" || !moduleapi.ValidSHA256(result.ManifestDigest) ||
				!moduleapi.ValidSHA256(result.MemberSnapshotDigest) ||
				!moduleapi.ValidSHA256(result.ResultRef) ||
				result.TerminalRunRevision == 0 ||
				result.TerminalFrameRevision == 0 {
				t.Fatalf("Reviewer Specialist envelope=%+v", result)
			}
			if _, duplicate := seenSlots[result.SlotID]; duplicate {
				t.Fatalf("Reviewer duplicated Specialist slot %q", result.SlotID)
			}
			seenSlots[result.SlotID] = struct{}{}
			specialist, present := specialistsByOutput[result.Result]
			if !present || specialist.SlotID != result.SlotID ||
				specialist.FocusID != result.FocusID ||
				specialist.Sequence >= reviewer.Sequence {
				t.Fatalf(
					"Reviewer call %d does not follow exact Specialist result=%+v specialist=%+v",
					reviewer.Sequence,
					result,
					specialist,
				)
			}
			specialistReviewerUses[result.Result]++
		}
		if len(seenSlots) != len(expected) {
			t.Fatalf("Reviewer call %d slots=%v", reviewer.Sequence, seenSlots)
		}
	}

	reviewerRootUses := make(map[int]int, len(reviewersByVerdict))
	for _, root := range roots {
		if root.RootVerdict == nil || root.RootVerdictCanonical == "" ||
			root.Output == "" {
			t.Fatalf("incomplete Root call=%+v", root)
		}
		reviewer, present := reviewersByVerdict[root.RootVerdictCanonical]
		if !present || reviewer.Sequence >= root.Sequence ||
			root.RootVerdict.FamilyDigest != reviewer.ReviewerPolicy.FamilyDigest ||
			root.RootVerdict.SpecialistResultDigest !=
				reviewer.ReviewerPolicy.SpecialistResultDigest {
			t.Fatalf(
				"Root call %d does not follow exact Reviewer verdict: reviewer=%+v root=%+v",
				root.Sequence,
				reviewer,
				root,
			)
		}
		reviewerRootUses[reviewer.Sequence]++
		reviewerBySlot := make(map[string]s3CReviewerResultEnvelope, len(expected))
		for _, result := range reviewer.ReviewerResults {
			reviewerBySlot[result.SlotID] = result
		}
		seenSlots := make(map[string]struct{}, len(expected))
		for _, result := range root.RootResults {
			want, present := expected[result.SlotID]
			reviewed, reviewedPresent := reviewerBySlot[result.SlotID]
			specialist, specialistPresent := specialistsByOutput[result.Result]
			if !present || result.SchemaVersion != s3CChildResultSchema ||
				result.FocusID != want.focus || result.Result == "" ||
				!reviewedPresent || reviewed.FocusID != result.FocusID ||
				reviewed.Result != result.Result || !specialistPresent ||
				specialist.Sequence >= reviewer.Sequence ||
				reviewer.Sequence >= root.Sequence {
				t.Fatalf(
					"Root call %d Child result does not close Reviewer/Specialist data: root=%+v reviewed=%+v specialist=%+v",
					root.Sequence,
					result,
					reviewed,
					specialist,
				)
			}
			if _, duplicate := seenSlots[result.SlotID]; duplicate {
				t.Fatalf("Root duplicated Specialist slot %q", result.SlotID)
			}
			seenSlots[result.SlotID] = struct{}{}
			specialistRootUses[result.Result]++
		}
		if len(seenSlots) != len(expected) {
			t.Fatalf("Root call %d slots=%v", root.Sequence, seenSlots)
		}
	}
	for output := range specialistsByOutput {
		if specialistReviewerUses[output] != 1 || specialistRootUses[output] != 1 {
			t.Fatalf(
				"Specialist output %q Reviewer/Root uses=%d/%d",
				output,
				specialistReviewerUses[output],
				specialistRootUses[output],
			)
		}
	}
	for _, reviewer := range reviewersByVerdict {
		if reviewerRootUses[reviewer.Sequence] != 1 {
			t.Fatalf(
				"Reviewer call %d Root uses=%d",
				reviewer.Sequence,
				reviewerRootUses[reviewer.Sequence],
			)
		}
	}
}

func assertS3CDeepSeekReviewerOffDataflow(
	t *testing.T,
	calls []s3CDeepSeekCallFact,
) {
	t.Helper()
	expectedFocus := map[string]string{
		"backend":  "backend",
		"frontend": "frontend",
		"network":  "network",
	}
	specialistsByOutput := make(map[string]s3CDeepSeekCallFact, 9)
	roots := make([]s3CDeepSeekCallFact, 0, 3)
	for _, call := range calls {
		switch call.Role {
		case s3CDeepSeekRoleSpecialist:
			if expectedFocus[call.SlotID] != call.FocusID || call.Output == "" {
				t.Fatalf("Reviewer-off Specialist call=%+v", call)
			}
			if _, duplicate := specialistsByOutput[call.Output]; duplicate {
				t.Fatalf("duplicate Reviewer-off Specialist output %q", call.Output)
			}
			specialistsByOutput[call.Output] = call
		case s3CDeepSeekRoleRoot:
			if call.RootVerdict != nil || call.RootVerdictCanonical != "" ||
				len(call.RootResults) != 3 || call.Output == "" {
				t.Fatalf("Reviewer-off Root call=%+v", call)
			}
			roots = append(roots, call)
		case s3CDeepSeekRoleReviewer:
			t.Fatalf("Reviewer-off flow invoked Reviewer: %+v", call)
		default:
			t.Fatalf("Reviewer-off flow has unknown role %q", call.Role)
		}
	}
	if len(specialistsByOutput) != 9 || len(roots) != 3 {
		t.Fatalf(
			"Reviewer-off topology Specialists/Roots=%d/%d",
			len(specialistsByOutput),
			len(roots),
		)
	}
	uses := make(map[string]int, len(specialistsByOutput))
	for _, root := range roots {
		seenSlots := make(map[string]struct{}, 3)
		for _, result := range root.RootResults {
			specialist, present := specialistsByOutput[result.Result]
			if !present || result.SchemaVersion != s3CChildResultSchema ||
				result.SlotID != specialist.SlotID ||
				result.FocusID != specialist.FocusID ||
				specialist.Sequence >= root.Sequence {
				t.Fatalf(
					"Reviewer-off Root result does not close Specialist data: root=%+v result=%+v specialist=%+v",
					root,
					result,
					specialist,
				)
			}
			if _, duplicate := seenSlots[result.SlotID]; duplicate {
				t.Fatalf("Reviewer-off Root duplicated slot %q", result.SlotID)
			}
			seenSlots[result.SlotID] = struct{}{}
			uses[result.Result]++
		}
		if len(seenSlots) != len(expectedFocus) {
			t.Fatalf("Reviewer-off Root slots=%v", seenSlots)
		}
	}
	for output := range specialistsByOutput {
		if uses[output] != 1 {
			t.Fatalf("Reviewer-off Specialist output %q Root uses=%d", output, uses[output])
		}
	}
}

type s3CDeepSeekRoundTripper struct {
	mu           sync.Mutex
	model        string
	nextSequence int
	records      []s3CDeepSeekCallFact
}

type s3CDeepSeekWireRequest struct {
	Model    string                   `json:"model"`
	Messages []s3CDeepSeekWireMessage `json:"messages"`
}

type s3CDeepSeekWireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type s3CAssignmentEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	SlotID        string `json:"slot_id"`
	FocusID       string `json:"focus_id"`
}

type s3CReviewerPolicyEnvelope struct {
	SchemaVersion          string   `json:"schema_version"`
	Policy                 string   `json:"policy"`
	FamilyDigest           string   `json:"family_digest"`
	SpecialistResultDigest string   `json:"specialist_result_digest"`
	OutputSchemaVersion    string   `json:"output_schema_version"`
	RequiredFields         []string `json:"required_fields"`
	AllowedDecisions       []string `json:"allowed_decisions"`
	AllowedIssueCodes      []string `json:"allowed_issue_codes"`
	OutputRules            []string `json:"output_rules"`
}

type s3CReviewerResultEnvelope struct {
	SchemaVersion         string `json:"schema_version"`
	SlotID                string `json:"slot_id"`
	FocusID               string `json:"focus_id"`
	WeightBasisPoints     uint32 `json:"weight_basis_points"`
	RunID                 string `json:"run_id"`
	ManifestDigest        string `json:"manifest_digest"`
	MemberSnapshotDigest  string `json:"member_snapshot_digest"`
	ResultRef             string `json:"result_ref"`
	TerminalRunRevision   uint64 `json:"terminal_run_revision"`
	TerminalFrameRevision uint64 `json:"terminal_frame_revision"`
	Result                string `json:"result"`
}

type s3CChildResultEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	SlotID        string `json:"slot_id"`
	FocusID       string `json:"focus_id"`
	Result        string `json:"result"`
}

type s3CDeepSeekCallFact struct {
	Sequence             int
	Role                 string
	SlotID               string
	FocusID              string
	Output               string
	ReviewerPolicy       *s3CReviewerPolicyEnvelope
	ReviewerResults      []s3CReviewerResultEnvelope
	RootResults          []s3CChildResultEnvelope
	RootVerdict          *corecontract.ReviewVerdictV1
	RootVerdictCanonical string
}

func (fake *s3CDeepSeekRoundTripper) snapshot() []s3CDeepSeekCallFact {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	result := make([]s3CDeepSeekCallFact, len(fake.records))
	for index, record := range fake.records {
		result[index] = record
		result[index].ReviewerResults = append(
			[]s3CReviewerResultEnvelope(nil),
			record.ReviewerResults...,
		)
		result[index].RootResults = append(
			[]s3CChildResultEnvelope(nil),
			record.RootResults...,
		)
	}
	return result
}

func (fake *s3CDeepSeekRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	fake.mu.Lock()
	fake.nextSequence++
	sequence := fake.nextSequence
	fake.mu.Unlock()

	if request == nil || request.Method != http.MethodPost ||
		request.URL.String() != s3CDeepSeekOfficialURL {
		return nil, fmt.Errorf("unexpected DeepSeek request target")
	}
	if request.Header.Get("Authorization") != "Bearer "+s3CDeepSeekTestKey ||
		request.Header.Get("Accept") != "application/json" ||
		request.Header.Get("Content-Type") != "application/json" {
		return nil, fmt.Errorf("unexpected DeepSeek request headers")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, fmt.Errorf("read DeepSeek request: %w", err)
	}
	if err := request.Body.Close(); err != nil {
		return nil, fmt.Errorf("close DeepSeek request: %w", err)
	}
	var wire s3CDeepSeekWireRequest
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("decode DeepSeek request: %w", err)
	}
	if wire.Model != fake.model || len(wire.Messages) == 0 {
		return nil, fmt.Errorf("unexpected DeepSeek model or empty messages")
	}

	call, assistantText, err := classifyS3CDeepSeekCall(sequence, wire.Messages)
	if err != nil {
		return nil, err
	}

	responseBody, err := json.Marshal(map[string]any{
		"id":                 fmt.Sprintf("s3c-offline-%d", sequence),
		"object":             "chat.completion",
		"created":            int64(1785800000),
		"model":              wire.Model,
		"system_fingerprint": "s3c-offline-fingerprint",
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": assistantText,
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":            100,
			"prompt_cache_hit_tokens":  40,
			"prompt_cache_miss_tokens": 60,
			"completion_tokens":        20,
			"completion_tokens_details": map[string]any{
				"reasoning_tokens": 5,
			},
			"total_tokens": 120,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode DeepSeek response: %w", err)
	}
	fake.mu.Lock()
	fake.records = append(fake.records, call)
	fake.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:          io.NopCloser(bytes.NewReader(responseBody)),
		ContentLength: int64(len(responseBody)),
		Request:       request,
	}, nil
}

func classifyS3CDeepSeekCall(
	sequence int,
	messages []s3CDeepSeekWireMessage,
) (s3CDeepSeekCallFact, string, error) {
	var assignment *s3CAssignmentEnvelope
	var policy *s3CReviewerPolicyEnvelope
	reviewerResults := make([]s3CReviewerResultEnvelope, 0, 3)
	rootResults := make([]s3CChildResultEnvelope, 0, 3)
	var rootVerdict *corecontract.ReviewVerdictV1
	rootVerdictCanonical := ""
	for _, message := range messages {
		switch {
		case strings.HasPrefix(message.Content, s3CAssignmentPrefix):
			if assignment != nil || message.Role != "system" {
				return s3CDeepSeekCallFact{}, "", fmt.Errorf(
					"malformed or duplicate Composite Specialist assignment",
				)
			}
			var decoded s3CAssignmentEnvelope
			if err := decodeS3CEnvelope(
				message.Content[len(s3CAssignmentPrefix):],
				&decoded,
			); err != nil {
				return s3CDeepSeekCallFact{}, "", fmt.Errorf(
					"decode Composite Specialist assignment: %w",
					err,
				)
			}
			assignment = &decoded
		case strings.HasPrefix(message.Content, s3CReviewerPolicyPrefix):
			if policy != nil || message.Role != "system" {
				return s3CDeepSeekCallFact{}, "", fmt.Errorf(
					"malformed or duplicate Composite Reviewer policy",
				)
			}
			var decoded s3CReviewerPolicyEnvelope
			if err := decodeS3CEnvelope(
				message.Content[len(s3CReviewerPolicyPrefix):],
				&decoded,
			); err != nil {
				return s3CDeepSeekCallFact{}, "", fmt.Errorf(
					"decode Composite Reviewer policy: %w",
					err,
				)
			}
			policy = &decoded
		case strings.HasPrefix(message.Content, s3CReviewerResultPrefix):
			if message.Role != "user" {
				return s3CDeepSeekCallFact{}, "", fmt.Errorf(
					"Composite Reviewer Specialist result has wrong role",
				)
			}
			var decoded s3CReviewerResultEnvelope
			if err := decodeS3CEnvelope(
				message.Content[len(s3CReviewerResultPrefix):],
				&decoded,
			); err != nil {
				return s3CDeepSeekCallFact{}, "", fmt.Errorf(
					"decode Composite Reviewer Specialist result: %w",
					err,
				)
			}
			reviewerResults = append(reviewerResults, decoded)
		case strings.HasPrefix(message.Content, s3CChildResultPrefix):
			if message.Role != "user" {
				return s3CDeepSeekCallFact{}, "", fmt.Errorf(
					"Composite Root Child result has wrong role",
				)
			}
			var decoded s3CChildResultEnvelope
			if err := decodeS3CEnvelope(
				message.Content[len(s3CChildResultPrefix):],
				&decoded,
			); err != nil {
				return s3CDeepSeekCallFact{}, "", fmt.Errorf(
					"decode Composite Root Child result: %w",
					err,
				)
			}
			rootResults = append(rootResults, decoded)
		case strings.HasPrefix(message.Content, s3CReviewVerdictPrefix):
			if rootVerdict != nil || message.Role != "user" {
				return s3CDeepSeekCallFact{}, "", fmt.Errorf(
					"malformed or duplicate Composite Root ReviewVerdict",
				)
			}
			body := []byte(message.Content[len(s3CReviewVerdictPrefix):])
			verdict, canonical, err := corecontract.ParseReviewVerdictV1(body)
			if err != nil || !bytes.Equal(body, canonical) {
				return s3CDeepSeekCallFact{}, "", fmt.Errorf(
					"decode canonical Composite Root ReviewVerdict: %v",
					err,
				)
			}
			rootVerdict = &verdict
			rootVerdictCanonical = string(canonical)
		}
	}

	switch {
	case assignment != nil && policy == nil && len(reviewerResults) == 0 &&
		len(rootResults) == 0 && rootVerdict == nil:
		if assignment.SchemaVersion != s3CAssignmentSchema ||
			assignment.SlotID == "" || assignment.FocusID == "" {
			return s3CDeepSeekCallFact{}, "", fmt.Errorf(
				"invalid Composite Specialist assignment: %+v",
				*assignment,
			)
		}
		output := fmt.Sprintf(
			"S3-C offline Specialist result slot=%s call=%d",
			assignment.SlotID,
			sequence,
		)
		return s3CDeepSeekCallFact{
			Sequence: sequence,
			Role:     s3CDeepSeekRoleSpecialist,
			SlotID:   assignment.SlotID,
			FocusID:  assignment.FocusID,
			Output:   output,
		}, output, nil
	case assignment == nil && policy != nil && len(reviewerResults) == 3 &&
		len(rootResults) == 0 && rootVerdict == nil:
		if policy.SchemaVersion != s3CReviewerPolicySchema ||
			policy.Policy != corecontract.CompositeReviewerPolicyResultsGateV1 ||
			policy.OutputSchemaVersion != corecontract.ReviewVerdictSchemaVersionV1 {
			return s3CDeepSeekCallFact{}, "", fmt.Errorf(
				"invalid Composite Reviewer policy: %+v",
				*policy,
			)
		}
		_, canonical, err := corecontract.NewReviewVerdictV1(
			corecontract.ReviewVerdictV1{
				SchemaVersion:          corecontract.ReviewVerdictSchemaVersionV1,
				FamilyDigest:           policy.FamilyDigest,
				SpecialistResultDigest: policy.SpecialistResultDigest,
				Decision:               corecontract.ReviewDecisionApproveV1,
				IssueCodes:             []corecontract.ReviewIssueCodeV1{},
				AffectedSlotIDs:        []string{},
				BoundedReason:          "Specialist results satisfy the frozen review gate.",
			},
		)
		if err != nil {
			return s3CDeepSeekCallFact{}, "", fmt.Errorf(
				"freeze Composite Reviewer verdict: %w",
				err,
			)
		}
		output := string(canonical)
		return s3CDeepSeekCallFact{
			Sequence:        sequence,
			Role:            s3CDeepSeekRoleReviewer,
			Output:          output,
			ReviewerPolicy:  policy,
			ReviewerResults: reviewerResults,
		}, output, nil
	case assignment == nil && policy == nil && len(reviewerResults) == 0 &&
		len(rootResults) == 3:
		output := fmt.Sprintf("S3-C offline Root synthesis call=%d", sequence)
		return s3CDeepSeekCallFact{
			Sequence:             sequence,
			Role:                 s3CDeepSeekRoleRoot,
			Output:               output,
			RootResults:          rootResults,
			RootVerdict:          rootVerdict,
			RootVerdictCanonical: rootVerdictCanonical,
		}, output, nil
	default:
		return s3CDeepSeekCallFact{}, "", fmt.Errorf(
			"DeepSeek request does not close exactly one Composite role: assignment=%t policy=%t reviewer_results=%d root_results=%d verdict=%t",
			assignment != nil,
			policy != nil,
			len(reviewerResults),
			len(rootResults),
			rootVerdict != nil,
		)
	}
}

func decodeS3CEnvelope(payload string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
