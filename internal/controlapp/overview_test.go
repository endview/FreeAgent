package controlapp

import (
	"context"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/controloverview"
	"github.com/endview/freeagent/internal/corecontract"
)

type overviewReaderFixtureV1 struct {
	snapshot controloverview.SnapshotV1
	err      error
	after    func()
}

func (reader *overviewReaderFixtureV1) LoadControlOverviewV1(
	_ context.Context, _ string, _ string, _ uint16,
) (controloverview.SnapshotV1, error) {
	if reader.after != nil {
		reader.after()
	}
	return reader.snapshot, reader.err
}

func TestOverviewServiceEmptySafeProjectionAndAuthorizationDrift(t *testing.T) {
	scope := testTenantScopeV1()
	modulesReader := newTestPublishedBasisReaderV1()
	reader := &overviewReaderFixtureV1{snapshot: controloverview.SnapshotV1{
		Basis: modulesReader.basis, BasisSourceUpdatedAtUnixMicros: 1_500,
		SourceUpdatedAtUnixMicros: 1_500,
		Workspaces:                []controloverview.WorkspaceRefV1{}, Runs: []controloverview.RunV1{},
		Unknown: []controloverview.UnknownV1{}, Learning: []controloverview.LearningV1{},
		ModuleCandidates: []controloverview.ModuleCandidateV1{}, Usage: []controloverview.UsageV1{},
	}}
	service, err := NewOverviewServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	accessContext := newTestAuthorizationV1("principal-a", scope)
	result, err := service.GetOverviewV1(context.Background(), GetOverviewInputV1{
		Authorization: accessContext, Scope: scope, ObservedAt: testObservedAtV1,
	})
	if err != nil {
		t.Fatalf("GetOverviewV1: %v", err)
	}
	if result.SchemaVersion != OverviewSchemaVersionV1 || len(result.View.Sections) != 6 ||
		result.Workspaces == nil || result.Runs == nil || result.Unknown == nil ||
		result.Learning == nil || result.ModuleCandidates == nil || result.Usage == nil ||
		!validStrongETagApplicationTestV1(result.StrongETag) {
		t.Fatalf("empty Overview=%+v", result)
	}
	foundWorkspaces := false
	for _, section := range result.View.Sections {
		if section.Kind == controlapicontract.ViewSectionWorkspacesV1 {
			foundWorkspaces = true
		}
	}
	if !foundWorkspaces {
		t.Fatal("Overview omitted WORKSPACES consistency section")
	}
	secondAccessContext := newTestAuthorizationV1("principal-a", scope)
	second, err := service.GetOverviewV1(context.Background(), GetOverviewInputV1{
		Authorization: secondAccessContext,
		Scope:         scope, ObservedAt: testObservedAtV1 + 100,
	})
	if err != nil {
		t.Fatalf("second stable Overview: %v", err)
	}
	if second.View.ObservedAtUnixMicros != result.View.ObservedAtUnixMicros ||
		second.ProjectionDigest != result.ProjectionDigest || second.StrongETag != result.StrongETag {
		t.Fatalf("request clock changed stable Overview: first=%+v second=%+v", result, second)
	}
	reader.snapshot.BasisSourceUpdatedAtUnixMicros++
	reader.snapshot.SourceUpdatedAtUnixMicros++
	changedAccessContext := newTestAuthorizationV1("principal-a", scope)
	changed, err := service.GetOverviewV1(context.Background(), GetOverviewInputV1{
		Authorization: changedAccessContext,
		Scope:         scope, ObservedAt: testObservedAtV1 + 100,
	})
	if err != nil {
		t.Fatalf("changed-source Overview: %v", err)
	}
	if changed.ProjectionDigest == result.ProjectionDigest || changed.StrongETag == result.StrongETag {
		t.Fatal("source timestamp change did not change projection digest/ETag")
	}
	reader.snapshot.BasisSourceUpdatedAtUnixMicros = 1_500
	reader.snapshot.SourceUpdatedAtUnixMicros = 1_500

	drift := newTestAuthorizationV1("principal-a", scope)
	drift.denyAtAllow = 3
	if _, err := service.GetOverviewV1(context.Background(), GetOverviewInputV1{
		Authorization: drift, Scope: scope, ObservedAt: testObservedAtV1,
	}); err != ErrForbidden {
		t.Fatalf("authorization drift error=%v", err)
	}
}

func TestOverviewServiceAllowsConcurrentSourceAfterRequestClockButCompletionVerifierBoundsIt(
	t *testing.T,
) {
	scope := testTenantScopeV1()
	basis := newTestPublishedBasisReaderV1().basis
	reader := &overviewReaderFixtureV1{snapshot: controloverview.SnapshotV1{
		Basis: basis, BasisSourceUpdatedAtUnixMicros: testObservedAtV1 + 1,
		SourceUpdatedAtUnixMicros: testObservedAtV1 + 1,
		Workspaces:                []controloverview.WorkspaceRefV1{}, Runs: []controloverview.RunV1{},
		Unknown: []controloverview.UnknownV1{}, Learning: []controloverview.LearningV1{},
		ModuleCandidates: []controloverview.ModuleCandidateV1{}, Usage: []controloverview.UsageV1{},
	}}
	service, err := NewOverviewServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	accessContext := newTestAuthorizationV1("principal-a", scope)
	result, err := service.GetOverviewV1(context.Background(), GetOverviewInputV1{
		Authorization: accessContext,
		Scope:         scope, ObservedAt: testObservedAtV1,
	})
	if err != nil {
		t.Fatalf("concurrent source rejected by application: %v", err)
	}
	if err := ValidateOverviewResultV1(
		newTestAuthorizationV1("principal-a", scope), scope,
		testObservedAtV1+1, result,
	); err != nil {
		t.Fatalf("source within completion time rejected: %v", err)
	}
	if err := ValidateOverviewResultV1(
		newTestAuthorizationV1("principal-a", scope), scope,
		testObservedAtV1, result,
	); err != ErrIntegrityFailure {
		t.Fatalf("source beyond completion time error=%v", err)
	}
}

func validStrongETagApplicationTestV1(value string) bool {
	return len(value) == 66 && value[0] == '"' && value[len(value)-1] == '"'
}

func TestValidateOverviewResultV1RejectsDetachedResultDrift(t *testing.T) {
	scope := testTenantScopeV1()
	basis := newTestPublishedBasisReaderV1().basis
	reader := &overviewReaderFixtureV1{snapshot: controloverview.SnapshotV1{
		Basis: basis, BasisSourceUpdatedAtUnixMicros: 1_500,
		SourceUpdatedAtUnixMicros: 1_500,
		Workspaces:                []controloverview.WorkspaceRefV1{},
		Runs: []controloverview.RunV1{{
			TenantID: testTenantIDV1, WorkspaceID: testWorkspaceAV1,
			RunID: "overview-run-a", State: corecontract.InitialRunState, Revision: 0,
			CreatedAtUnixMicros: 1_000, UpdatedAtUnixMicros: 1_100,
		}},
		Unknown: []controloverview.UnknownV1{}, Learning: []controloverview.LearningV1{},
		ModuleCandidates: []controloverview.ModuleCandidateV1{}, Usage: []controloverview.UsageV1{},
	}}
	service, err := NewOverviewServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	accessContext := newTestAuthorizationV1("principal-a", scope)
	result, err := service.GetOverviewV1(context.Background(), GetOverviewInputV1{
		Authorization: accessContext,
		Scope:         scope, ObservedAt: testObservedAtV1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOverviewResultV1(
		newTestAuthorizationV1("principal-a", scope), scope, testObservedAtV1, result,
	); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*OverviewResultV1)
	}{
		{"item", func(value *OverviewResultV1) { value.Runs[0].TenantID = "tenant-drift" }},
		{"section", func(value *OverviewResultV1) { value.View.Sections[0].SourceDigest = "" }},
		{"view digest", func(value *OverviewResultV1) { value.ViewSnapshotDigest = "" }},
		{"projection digest", func(value *OverviewResultV1) { value.ProjectionDigest = "" }},
		{"strong ETag", func(value *OverviewResultV1) { value.StrongETag = `"` + strings.Repeat("0", 64) + `"` }},
		{"published pointer", func(value *OverviewResultV1) { value.PublishedPointer.Revision++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneOverviewResultV1(result)
			test.mutate(&changed)
			if err := ValidateOverviewResultV1(
				newTestAuthorizationV1("principal-a", scope), scope,
				testObservedAtV1, changed,
			); err != ErrIntegrityFailure {
				t.Fatalf("error=%v", err)
			}
		})
	}
	selfConsistent := []struct {
		name   string
		mutate func(*OverviewResultV1)
	}{
		{"forged Run state", func(value *OverviewResultV1) { value.Runs[0].State = "FORGED" }},
		{"terminal Run revision zero", func(value *OverviewResultV1) {
			value.Runs[0].State = corecontract.TerminatedLoopStep
			value.Runs[0].Disposition = corecontract.TerminatedLoopStep
			value.Runs[0].Revision = 0
		}},
		{"reconciling Run revision zero", func(value *OverviewResultV1) {
			value.Runs[0].State = corecontract.WaitingReconciliationLoopStep
			value.Runs[0].Disposition = corecontract.WaitingReconciliationLoopStep
			value.Runs[0].Revision = 0
		}},
		{"future item", func(value *OverviewResultV1) {
			value.Runs[0].UpdatedAtUnixMicros = value.View.ObservedAtUnixMicros + 1
		}},
		{"false truncation proof", func(value *OverviewResultV1) { value.RunsTruncated = true }},
		{"forged source clock", func(value *OverviewResultV1) {
			value.View.ObservedAtUnixMicros++
		}},
	}
	for _, test := range selfConsistent {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneOverviewResultV1(result)
			test.mutate(&changed)
			resignOverviewResultTestV1(t, scope, testObservedAtV1, &changed)
			if err := ValidateOverviewResultV1(
				newTestAuthorizationV1("principal-a", scope), scope,
				testObservedAtV1, changed,
			); err != ErrIntegrityFailure {
				t.Fatalf("error=%v", err)
			}
		})
	}
	drift := newTestAuthorizationV1("principal-a", scope)
	drift.denyAtAllow = 3
	if err := ValidateOverviewResultV1(
		drift, scope, testObservedAtV1, result,
	); err != ErrForbidden {
		t.Fatalf("final authorization drift error=%v", err)
	}
}

func TestValidateOverviewResultV1RejectsMissingPrivateBasisSourceEvidence(t *testing.T) {
	scope, result := newCompleteOverviewResultTestV1(t)
	result.basisSourceUpdatedAtUnixMicros = 0
	resignOverviewResultTestV1(t, scope, testObservedAtV1, &result)
	if err := ValidateOverviewResultV1(
		newTestAuthorizationV1("principal-a", scope), scope, testObservedAtV1, result,
	); err != ErrIntegrityFailure {
		t.Fatalf("missing private source evidence error=%v", err)
	}
}

func TestValidateOverviewResultV1RejectsSelfConsistentImpossibleItems(t *testing.T) {
	scope, result := newCompleteOverviewResultTestV1(t)
	validate := func(t *testing.T, changed OverviewResultV1, want error) {
		t.Helper()
		resignOverviewResultTestV1(t, scope, testObservedAtV1, &changed)
		if err := ValidateOverviewResultV1(
			newTestAuthorizationV1("principal-a", scope), scope,
			testObservedAtV1, changed,
		); err != want {
			t.Fatalf("error=%v want=%v", err, want)
		}
	}

	maximumSafe := uint64(1<<53 - 1)
	changed := cloneOverviewResultV1(result)
	changed.Usage[0].OutputTokens = &maximumSafe
	validate(t, changed, nil)
	overflow := maximumSafe + 1
	for _, usageFact := range []struct {
		name string
		set  func(*controloverview.UsageV1)
	}{
		{"input", func(item *controloverview.UsageV1) { item.InputTokens = &overflow }},
		{"cached input", func(item *controloverview.UsageV1) { item.CachedInputTokens = &overflow }},
		{"uncached input", func(item *controloverview.UsageV1) { item.UncachedInputTokens = &overflow }},
		{"output", func(item *controloverview.UsageV1) { item.OutputTokens = &overflow }},
		{"reasoning", func(item *controloverview.UsageV1) { item.ReasoningTokens = &overflow }},
	} {
		t.Run("unsafe "+usageFact.name+" tokens", func(t *testing.T) {
			changed := cloneOverviewResultV1(result)
			usageFact.set(&changed.Usage[0])
			validate(t, changed, ErrIntegrityFailure)
		})
	}

	duplicates := []struct {
		name   string
		mutate func(*OverviewResultV1)
	}{
		{"Run identity", func(value *OverviewResultV1) {
			copy := value.Runs[0]
			copy.UpdatedAtUnixMicros--
			value.Runs = append(value.Runs, copy)
		}},
		{"UNKNOWN identity", func(value *OverviewResultV1) {
			copy := value.Unknown[0]
			copy.UpdatedAtUnixMicros--
			value.Unknown = append(value.Unknown, copy)
		}},
		{"Learning identity", func(value *OverviewResultV1) {
			copy := value.Learning[0]
			copy.UpdatedAtUnixMicros--
			value.Learning = append(value.Learning, copy)
		}},
		{"Module Review identity", func(value *OverviewResultV1) {
			copy := value.ModuleCandidates[0]
			copy.CreatedAtUnixMicros--
			value.ModuleCandidates = append(value.ModuleCandidates, copy)
		}},
		{"Usage identity", func(value *OverviewResultV1) {
			copy := value.Usage[0]
			copy.UpdatedAtUnixMicros--
			value.Usage = append(value.Usage, copy)
		}},
	}
	for _, test := range duplicates {
		t.Run("duplicate "+test.name, func(t *testing.T) {
			changed := cloneOverviewResultV1(result)
			test.mutate(&changed)
			validate(t, changed, ErrIntegrityFailure)
		})
	}

	changed = cloneOverviewResultV1(result)
	changed.Runs[0].State = corecontract.InitialRunState
	changed.Runs[0].Disposition = ""
	changed.Runs[0].Revision = 0
	validate(t, changed, ErrIntegrityFailure)

	changed = cloneOverviewResultV1(result)
	changed.Runs[0].WorkspaceID = testWorkspaceBV1
	validate(t, changed, ErrIntegrityFailure)

	learning := []struct {
		state    string
		revision uint64
	}{
		{"SUBMITTED", 1},
		{"REVIEW_PENDING", 0},
		{"REVIEW_UNKNOWN", 3},
		{"APPROVED", 1},
		{"REJECTED", 1},
		{"REVIEW_FAILED", 1},
	}
	for _, test := range learning {
		t.Run("Learning "+test.state, func(t *testing.T) {
			changed := cloneOverviewResultV1(result)
			changed.Learning[0].State = test.state
			changed.Learning[0].Revision = test.revision
			validate(t, changed, ErrIntegrityFailure)
		})
	}

	unknown := []struct {
		kind       string
		resourceID string
		revision   uint64
	}{
		{controloverview.UnknownKindModelV1, "model-attempt-a", 2},
		{controloverview.UnknownKindActionV1, "action-attempt-a", 3},
		{controloverview.UnknownKindChannelSendV1, "channel-attempt-a", 0},
		{controloverview.UnknownKindLearningProposalV1, strings.Repeat("e", 64), 1},
		{controloverview.UnknownKindLearningTaskV1, strings.Repeat("f", 64), 3},
	}
	for _, test := range unknown {
		t.Run("UNKNOWN "+test.kind, func(t *testing.T) {
			changed := cloneOverviewResultV1(result)
			changed.Unknown[0].Kind = test.kind
			changed.Unknown[0].ResourceID = test.resourceID
			changed.Unknown[0].Revision = test.revision
			validate(t, changed, ErrIntegrityFailure)
		})
	}

	modules := []struct {
		name   string
		mutate func(*controloverview.ModuleCandidateV1)
	}{
		{"same instance", func(item *controloverview.ModuleCandidateV1) {
			item.TargetInstanceID = item.CurrentInstanceID
		}},
		{"different module", func(item *controloverview.ModuleCandidateV1) {
			item.TargetModuleID = "vendor.other"
		}},
		{"same version", func(item *controloverview.ModuleCandidateV1) {
			item.TargetExactVersion = item.CurrentExactVersion
		}},
		{"same artifact", func(item *controloverview.ModuleCandidateV1) {
			item.TargetArtifactDigest = item.CurrentArtifactDigest
		}},
	}
	for _, test := range modules {
		t.Run("Module "+test.name, func(t *testing.T) {
			changed := cloneOverviewResultV1(result)
			test.mutate(&changed.ModuleCandidates[0])
			validate(t, changed, ErrIntegrityFailure)
		})
	}

	usage := []struct {
		name     string
		status   string
		revision uint64
		clear    bool
	}{
		{"pending advanced", "PENDING", 1, true},
		{"pending with facts", "PENDING", 0, false},
		{"provider report genesis", "PROVIDER_REPORTED", 0, false},
		{"provider report too advanced", "PROVIDER_REPORTED", 3, false},
		{"reconciliation genesis", "PENDING_RECONCILIATION", 0, false},
		{"reconciliation too advanced", "PENDING_RECONCILIATION", 2, true},
		{"no report with facts", "NO_USAGE_REPORTED", 1, false},
		{"no report too advanced", "NO_USAGE_REPORTED", 3, true},
		{"unknown status", "FORGED", 1, false},
	}
	for _, test := range usage {
		t.Run("Usage "+test.name, func(t *testing.T) {
			changed := cloneOverviewResultV1(result)
			changed.Usage[0].ReconciliationStatus = test.status
			changed.Usage[0].Revision = test.revision
			if test.clear {
				changed.Usage[0].OutputTokens = nil
			}
			validate(t, changed, ErrIntegrityFailure)
		})
	}
}

func newCompleteOverviewResultTestV1(t *testing.T) (
	controlapicontract.ControlScopeV1,
	OverviewResultV1,
) {
	t.Helper()
	scope := testTenantScopeV1()
	outputTokens := uint64(3)
	reader := &overviewReaderFixtureV1{snapshot: controloverview.SnapshotV1{
		Basis:                          newTestPublishedBasisReaderV1().basis,
		BasisSourceUpdatedAtUnixMicros: 1_500, SourceUpdatedAtUnixMicros: 1_500,
		Workspaces: []controloverview.WorkspaceRefV1{},
		Runs: []controloverview.RunV1{{
			TenantID: testTenantIDV1, WorkspaceID: testWorkspaceAV1,
			RunID: "overview-run-a", State: corecontract.WaitingReconciliationLoopStep,
			Disposition: corecontract.WaitingReconciliationLoopStep,
			Revision:    1, CreatedAtUnixMicros: 1_000, UpdatedAtUnixMicros: 1_100,
		}},
		Unknown: []controloverview.UnknownV1{{
			Kind: controloverview.UnknownKindModelV1, ResourceID: "model-attempt-a",
			TenantID: testTenantIDV1, WorkspaceID: testWorkspaceAV1,
			RunID: "overview-run-a", Revision: 1, UpdatedAtUnixMicros: 1_300,
		}},
		Learning: []controloverview.LearningV1{{
			ProposalID: strings.Repeat("a", 64), TenantID: testTenantIDV1,
			WorkspaceID: testWorkspaceAV1, Kind: "KNOWLEDGE", State: "SUBMITTED",
			Revision: 0, CreatedAtUnixMicros: 900, UpdatedAtUnixMicros: 1_200,
		}},
		ModuleCandidates: []controloverview.ModuleCandidateV1{{
			ReviewID: strings.Repeat("b", 64), CandidateID: strings.Repeat("c", 64),
			TenantID: testTenantIDV1, BindingTargetKind: "PROFILE",
			CurrentInstanceID: "module-instance-current", TargetInstanceID: "module-instance-target",
			CurrentModuleID: "vendor.tool", CurrentExactVersion: "build-1",
			CurrentArtifactDigest: strings.Repeat("d", 64),
			TargetModuleID:        "vendor.tool", TargetExactVersion: "build-2",
			TargetArtifactDigest: strings.Repeat("e", 64), Conclusion: "WOULD_APPLY",
			CreatedAtUnixMicros: 1_100,
		}},
		Usage: []controloverview.UsageV1{{
			AttemptID: "model-attempt-a", RunID: "overview-run-a",
			TenantID: testTenantIDV1, WorkspaceID: testWorkspaceAV1,
			Revision: 1, OutputTokens: &outputTokens,
			ReconciliationStatus: "PENDING_RECONCILIATION", UpdatedAtUnixMicros: 1_300,
		}},
	}}
	service, err := NewOverviewServiceV1(reader)
	if err != nil {
		t.Fatal(err)
	}
	accessContext := newTestAuthorizationV1("principal-a", scope)
	result, err := service.GetOverviewV1(context.Background(), GetOverviewInputV1{
		Authorization: accessContext,
		Scope:         scope, ObservedAt: testObservedAtV1,
	})
	if err != nil {
		t.Fatalf("complete Overview: %v", err)
	}
	return scope, result
}

func TestOverviewProjectionValidatorsMatchStoreStateMachines(t *testing.T) {
	runs := []struct {
		state       string
		disposition string
		revision    uint64
		valid       bool
	}{
		{corecontract.InitialRunState, "", 0, true},
		{corecontract.InitialRunState, "WAITING_EXTERNAL", 0, true},
		{corecontract.WaitingReconciliationLoopStep, corecontract.WaitingReconciliationLoopStep, 0, false},
		{corecontract.WaitingReconciliationLoopStep, corecontract.WaitingReconciliationLoopStep, 1, true},
		{corecontract.TerminatedLoopStep, corecontract.TerminatedLoopStep, 0, false},
		{corecontract.TerminatedLoopStep, corecontract.TerminatedLoopStep, 1, true},
	}
	for _, test := range runs {
		if got := validOverviewRunProjectionV1(test.state, test.disposition, test.revision); got != test.valid {
			t.Fatalf("Run %s/%s/%d valid=%v want=%v", test.state, test.disposition,
				test.revision, got, test.valid)
		}
	}
	unknown := []struct {
		kind     string
		revision uint64
		valid    bool
	}{
		{controloverview.UnknownKindModelV1, 1, true},
		{controloverview.UnknownKindModelV1, 2, false},
		{controloverview.UnknownKindActionV1, 1, true},
		{controloverview.UnknownKindActionV1, 2, true},
		{controloverview.UnknownKindActionV1, 3, false},
		{controloverview.UnknownKindChannelSendV1, 0, false},
		{controloverview.UnknownKindChannelSendV1, 1, true},
		{controloverview.UnknownKindChannelSendV1, 1 << 53, false},
		{controloverview.UnknownKindLearningProposalV1, 2, true},
		{controloverview.UnknownKindLearningTaskV1, 2, true},
	}
	for _, test := range unknown {
		if got := validOverviewUnknownRevisionV1(test.kind, test.revision); got != test.valid {
			t.Fatalf("UNKNOWN %s/%d valid=%v want=%v", test.kind, test.revision, got, test.valid)
		}
	}
	learning := []struct {
		state    string
		revision uint64
		valid    bool
	}{
		{"SUBMITTED", 0, true},
		{"REVIEW_PENDING", 1, true},
		{"REVIEW_UNKNOWN", 2, true},
		{"APPROVED", 2, true},
		{"APPROVED", 3, true},
		{"REJECTED", 3, true},
		{"REVIEW_FAILED", 3, true},
		{"REVIEW_UNKNOWN", 3, false},
	}
	for _, test := range learning {
		if got := validOverviewLearningProjectionV1(test.state, test.revision); got != test.valid {
			t.Fatalf("Learning %s/%d valid=%v want=%v", test.state, test.revision, got, test.valid)
		}
	}
	known := uint64(1)
	usage := []struct {
		name  string
		item  controloverview.UsageV1
		valid bool
	}{
		{"pending genesis", controloverview.UsageV1{ReconciliationStatus: "PENDING"}, true},
		{"pending facts", controloverview.UsageV1{ReconciliationStatus: "PENDING", OutputTokens: &known}, false},
		{"reported", controloverview.UsageV1{Revision: 1, ReconciliationStatus: "PROVIDER_REPORTED", OutputTokens: &known}, true},
		{"reported reconciled", controloverview.UsageV1{Revision: 2, ReconciliationStatus: "PROVIDER_REPORTED", OutputTokens: &known}, true},
		{"reported impossible advance", controloverview.UsageV1{Revision: 3, ReconciliationStatus: "PROVIDER_REPORTED", OutputTokens: &known}, false},
		{"reconciliation", controloverview.UsageV1{Revision: 1, ReconciliationStatus: "PENDING_RECONCILIATION"}, true},
		{"reconciliation impossible advance", controloverview.UsageV1{Revision: 2, ReconciliationStatus: "PENDING_RECONCILIATION"}, false},
		{"expired no report", controloverview.UsageV1{ReconciliationStatus: "NO_USAGE_REPORTED"}, true},
		{"reconciled no report", controloverview.UsageV1{Revision: 2, ReconciliationStatus: "NO_USAGE_REPORTED"}, true},
		{"no report impossible advance", controloverview.UsageV1{Revision: 3, ReconciliationStatus: "NO_USAGE_REPORTED"}, false},
		{"no report facts", controloverview.UsageV1{Revision: 1, ReconciliationStatus: "NO_USAGE_REPORTED", OutputTokens: &known}, false},
	}
	for _, test := range usage {
		if got := validOverviewUsageProjectionV1(test.item); got != test.valid {
			t.Fatalf("Usage %s valid=%v want=%v", test.name, got, test.valid)
		}
	}
}

func resignOverviewResultTestV1(
	t *testing.T,
	scope controlapicontract.ControlScopeV1,
	observedAt uint64,
	result *OverviewResultV1,
) {
	t.Helper()
	request, err := authorizeModulesRequestV1(
		newTestAuthorizationV1("principal-a", scope), scope, observedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	basis := controlcontract.PublishedBasis{
		TenantID: result.Basis.TenantID, PointerRevision: result.Basis.PointerRevision,
		Control: controlcontract.ControlSnapshotRef{
			SnapshotID: result.Basis.Control.ID, Revision: result.Basis.Control.Revision,
			Digest: result.Basis.Control.Digest,
		},
		Catalog: controlcontract.CatalogGenerationRef{
			GenerationID: result.Basis.Catalog.ID, Generation: result.Basis.Catalog.Revision,
			Digest: result.Basis.Catalog.Digest,
		},
	}
	snapshot := controloverview.SnapshotV1{
		Basis:                          basis,
		BasisSourceUpdatedAtUnixMicros: result.basisSourceUpdatedAtUnixMicros,
		SourceUpdatedAtUnixMicros:      result.View.ObservedAtUnixMicros,
		Workspaces:                     result.Workspaces, WorkspacesTruncated: result.WorkspacesTruncated,
		Runs: result.Runs, RunsTruncated: result.RunsTruncated,
		Unknown: result.Unknown, UnknownTruncated: result.UnknownTruncated,
		Learning: result.Learning, LearningTruncated: result.LearningTruncated,
		ModuleCandidates:          result.ModuleCandidates,
		ModuleCandidatesTruncated: result.ModuleCandidatesTruncated,
		Usage:                     result.Usage, UsageTruncated: result.UsageTruncated,
	}
	sections, err := overviewSectionsV1(request, result.Basis.PointerRevision, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	result.View, _, result.ViewSnapshotDigest, err =
		controlapicontract.NewControlViewSnapshotV1(controlapicontract.ControlViewSnapshotV1{
			SchemaVersion: controlapicontract.ControlViewSnapshotSchemaVersionV1,
			Scope:         request.scope, ScopeDigest: request.scopeDigest,
			ObservedAtUnixMicros: snapshot.SourceUpdatedAtUnixMicros,
			Basis:                result.Basis, Sections: sections,
		})
	if err != nil {
		t.Fatal(err)
	}
	result.ProjectionDigest, err = digestOverviewProjectionV1(*result)
	if err != nil {
		t.Fatal(err)
	}
	result.StrongETag, err = digestStrongETagV1(
		overviewETagDomainV1, request, result.ProjectionDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
}
