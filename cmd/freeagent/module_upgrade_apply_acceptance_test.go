package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const moduleUpgradeApplyTargetContextTextV1 = "Role: prioritize sound architecture, explicit trade-offs, and coherent system structure."

func TestModuleUpgradeApplyAcceptanceFreezesOldRunAndPublishesDistinctTargetContext(
	t *testing.T,
) {
	fixture := newModuleUpgradeCLIIntegrationFixtureV1(t)
	fixture = moduleUpgradeApplyDistinctTargetContextFixtureV1(t, fixture)
	artifactRoot := filepath.Join(fixture.root, "artifacts")

	oldRunID := assertModuleApplyContextChatV1(
		t,
		fixture.databasePath,
		artifactRoot,
		"u4-upgrade-old-run",
		"inspect the old governed context",
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyRoleText},
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyBasicContextText},
			{Role: moduleapi.ModelRoleUser, Content: "inspect the old governed context"},
		},
	)
	oldEvidence := loadModuleUpgradeApplyFrozenContextEvidenceV1(
		t,
		fixture.databasePath,
		oldRunID,
		fixture.current.InstanceID,
	)
	if oldEvidence.Text != moduleApplyRoleText {
		t.Fatalf("old Run static context = %q", oldEvidence.Text)
	}

	reviewResult, decisionID := moduleUpgradeApplyApproveAcceptanceV1(t, fixture)
	applyArgs := moduleUpgradeApplyAcceptanceArgsV1(
		fixture,
		artifactRoot,
		reviewResult.ReviewID,
		decisionID,
	)

	// The publication commits before stdout is written. A lost result must not
	// turn the exact adjacent retry into another mutation or another staging
	// operation.
	writeErr := runModuleUpgradeApply(
		context.Background(),
		applyArgs,
		moduleUpgradeApplyFailWriterV1{},
		ioDiscardModuleUpgradeApplyV1{},
	)
	if writeErr == nil ||
		writeErr.Error() != "freeagent module-upgrade-apply: failed (INTERNAL_ERROR)" {
		t.Fatalf("first committed Apply stdout failure = %v", writeErr)
	}
	assertNoModuleApplyStageResidueV1(t, artifactRoot)
	mutationCounts := moduleApplyMutationRowCountsV1(t, fixture.databasePath)
	artifactEntries := moduleUpgradeApplyArtifactEntriesV1(t, artifactRoot)

	var retryOutput bytes.Buffer
	if err := runModuleUpgradeApply(
		context.Background(),
		applyArgs,
		&retryOutput,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("adjacent retry after stdout failure: %v", err)
	}
	var retried moduleUpgradeApplyCommandResultV1
	decodeModuleOperatorOutputV1(t, retryOutput.Bytes(), &retried)
	if retried.Status != moduleApplyStatusAlreadyApplied ||
		retried.ReviewID != reviewResult.ReviewID ||
		retried.DecisionID != decisionID ||
		retried.PointerRevision != fixture.publishedBasis.PointerRevision+1 {
		t.Fatalf("adjacent retry = %+v", retried)
	}
	if after := moduleApplyMutationRowCountsV1(t, fixture.databasePath); !reflect.DeepEqual(after, mutationCounts) {
		t.Fatalf("adjacent retry mutated Store: before=%v after=%v", mutationCounts, after)
	}
	if after := moduleUpgradeApplyArtifactEntriesV1(t, artifactRoot); !reflect.DeepEqual(after, artifactEntries) {
		t.Fatalf("adjacent retry mutated artifact root: before=%v after=%v", artifactEntries, after)
	}
	assertNoModuleApplyStageResidueV1(t, artifactRoot)

	basis, control, catalog := loadModuleApplyPublishedStateV1(
		t,
		fixture.databasePath,
	)
	if basis.PointerRevision != retried.PointerRevision {
		t.Fatalf("published basis=%+v retry=%+v", basis, retried)
	}
	moduleUpgradeApplyAssertExactReplacementV1(
		t,
		fixture,
		reviewResult.Review,
		control,
		catalog,
	)

	newRunID := assertModuleApplyContextChatV1(
		t,
		fixture.databasePath,
		artifactRoot,
		"u4-upgrade-new-run",
		"inspect the new governed context",
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: moduleUpgradeApplyTargetContextTextV1},
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyBasicContextText},
			{Role: moduleapi.ModelRoleUser, Content: "inspect the new governed context"},
		},
	)
	newEvidence := loadModuleUpgradeApplyFrozenContextEvidenceV1(
		t,
		fixture.databasePath,
		newRunID,
		fixture.upgradeBasis.Selection.TargetInstanceID,
	)
	if newEvidence.Text != moduleUpgradeApplyTargetContextTextV1 ||
		newEvidence.StaticContextRef == oldEvidence.StaticContextRef {
		t.Fatalf("new Run evidence=%+v old=%+v", newEvidence, oldEvidence)
	}

	// The already-admitted Run remains tied to its old provider and exact
	// static-content digest after the current publication changes.
	oldAfter := loadModuleUpgradeApplyFrozenContextEvidenceV1(
		t,
		fixture.databasePath,
		oldRunID,
		fixture.current.InstanceID,
	)
	if oldAfter != oldEvidence {
		t.Fatalf("old Run closure changed: before=%+v after=%+v", oldEvidence, oldAfter)
	}
}

func moduleUpgradeApplyDistinctTargetContextFixtureV1(
	t *testing.T,
	fixture moduleUpgradeCLIIntegrationFixtureV1,
) moduleUpgradeCLIIntegrationFixtureV1 {
	t.Helper()
	previousDigest := fixture.targetReport.ArtifactDigest
	previousSnapshotID := fixture.snapshotID
	manifestPath := filepath.Join(
		fixture.targetDirectory,
		moduleapi.ArtifactManifestPath,
	)
	manifestCanonical, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	// The first fixture Snapshot has already observed 2.0.0. Discovery
	// observations are immutable by exact module/version, so the distinct
	// candidate uses the next exact version instead of rewriting that history.
	manifest.Version = "2.0.1"
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestCanonical, err = moduleapi.CanonicalJSON(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestCanonical, 0o600); err != nil {
		t.Fatal(err)
	}
	_, staticCanonical, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text:          moduleUpgradeApplyTargetContextTextV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(fixture.targetDirectory, "content", "context.json")
	if err := os.WriteFile(contextPath, staticCanonical, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := moduleconformance.VerifyDirectory(
		context.Background(),
		fixture.targetDirectory,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.ArtifactDigest == previousDigest ||
		report.ArtifactSizeBytes != fixture.targetReport.ArtifactSizeBytes {
		t.Fatalf(
			"distinct target report=%+v previous=%+v",
			report,
			fixture.targetReport,
		)
	}
	entry := moduleapi.ModuleDiscoveryEntryV1{
		Module: moduleapi.Ref{
			ID: report.Module.ID, Version: report.Module.ExactVersion,
		},
		ArtifactDigest:    report.ArtifactDigest,
		ArtifactSizeBytes: report.ArtifactSizeBytes,
		PackagePath:       "packages/ignored-by-u3",
	}
	_, indexCanonical, _, err := moduleapi.NewModuleDiscoveryIndexV1(
		moduleapi.ModuleDiscoveryIndexV1{
			SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      fixture.sourceID,
			Entries:       []moduleapi.ModuleDiscoveryEntryV1{entry},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	refreshBasis, err := store.ReadModuleSourceRefreshBasis(ctx, fixture.sourceID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	snapshot, err := store.CommitModuleSourceRefresh(
		ctx,
		refreshBasis,
		indexCanonical,
	)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	upgradeBasis, err := store.ReadModuleUpgradeReviewBasis(
		ctx,
		currentstore.ReadModuleUpgradeReviewBasisInput{
			SourceID:             fixture.sourceID,
			SnapshotID:           snapshot.SnapshotID,
			TenantID:             defaultTenantID,
			BindingTarget:        moduleupgrade.BindingTargetV1{Kind: moduleupgrade.BindingTargetProfileV1, ProfileID: moduleApplyTestProfileID},
			Port:                 productionContextPort,
			PortBindingIndex:     0,
			TargetModule:         entry.Module,
			TargetArtifactDigest: entry.ArtifactDigest,
			TargetInstanceID:     fixture.upgradeBasis.Selection.TargetInstanceID,
		},
	)
	closeErr := store.Close()
	if err := errors.Join(err, closeErr); err != nil {
		t.Fatal(err)
	}
	if snapshot.SnapshotID == previousSnapshotID {
		t.Fatal("distinct target context did not create a new Discovery snapshot")
	}
	fixture.targetReport = report
	fixture.snapshotID = snapshot.SnapshotID
	fixture.upgradeBasis = upgradeBasis
	fixture.currentActivation = upgradeBasis.CurrentActivation
	return fixture
}

func moduleUpgradeApplyApproveAcceptanceV1(
	t *testing.T,
	fixture moduleUpgradeCLIIntegrationFixtureV1,
) (moduleUpgradeReviewCommandResultV1, string) {
	t.Helper()
	var reviewOutput bytes.Buffer
	if err := runModuleUpgradeReview(
		context.Background(),
		fixture.reviewArgs(),
		&reviewOutput,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("record Review: %v", err)
	}
	var reviewResult moduleUpgradeReviewCommandResultV1
	decodeModuleOperatorOutputV1(t, reviewOutput.Bytes(), &reviewResult)
	reasonPath := filepath.Join(fixture.root, "u4-acceptance-approval.txt")
	if err := os.WriteFile(
		reasonPath,
		[]byte("approve exact distinct-context replacement"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var decisionOutput bytes.Buffer
	if err := runModuleUpgradeDecide(
		context.Background(),
		[]string{
			"--enable-module-upgrade-review",
			"--db", fixture.databasePath,
			"--tenant", defaultTenantID,
			"--review-id", reviewResult.ReviewID,
			"--candidate-id", reviewResult.CandidateID,
			"--decision", "APPROVE",
			"--operator-principal", "operator.u4.acceptance",
			"--reason-file", reasonPath,
		},
		&decisionOutput,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("record APPROVE Decision: %v", err)
	}
	var decision struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		TenantID      string `json:"tenant_id"`
		CandidateID   string `json:"candidate_id"`
		ReviewID      string `json:"review_id"`
		DecisionID    string `json:"decision_id"`
	}
	decodeModuleOperatorOutputV1(t, decisionOutput.Bytes(), &decision)
	if !moduleapi.ValidSHA256(decision.DecisionID) {
		t.Fatalf("Decision ID = %q", decision.DecisionID)
	}
	return reviewResult, decision.DecisionID
}

func moduleUpgradeApplyAcceptanceArgsV1(
	fixture moduleUpgradeCLIIntegrationFixtureV1,
	artifactRoot string,
	reviewID string,
	decisionID string,
) []string {
	return []string{
		"--enable-module-upgrade-apply",
		"--db", fixture.databasePath,
		"--artifact-root", artifactRoot,
		"--source-root", fixture.sourceRoot,
		"--tenant", defaultTenantID,
		"--review-id", reviewID,
		"--decision-id", decisionID,
		"--artifact", fixture.targetDirectory,
		"--signature=",
		"--allow-local-mcp-artifact=",
		"--allow-trusted-in-process-artifact=",
		"--allow-remote-action-artifact=",
		"--allow-wasm-action-artifact=",
		"--allow-remote-action-endpoint=",
		"--allow-remote-action-secret-ref=",
		"--allow-model-secret-ref=",
	}
}

type moduleUpgradeApplyFailWriterV1 struct{}

func (moduleUpgradeApplyFailWriterV1) Write([]byte) (int, error) {
	return 0, errors.New("injected stdout failure after committed upgrade Apply")
}

func moduleUpgradeApplyArtifactEntriesV1(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

type moduleUpgradeApplyFrozenContextEvidenceV1 struct {
	ProviderInstanceID string
	StaticContextRef   string
	Text               string
}

func loadModuleUpgradeApplyFrozenContextEvidenceV1(
	t *testing.T,
	databasePath string,
	runID string,
	instanceID string,
) moduleUpgradeApplyFrozenContextEvidenceV1 {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID: runID, OwnerID: "u4-upgrade-run-inspector", TTL: moduleApplyReconcileTimeout,
		},
	)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	run, loadErr := store.LoadRunForLoop(ctx, lease)
	releaseErr := store.ReleaseRunLease(ctx, lease)
	closeErr := store.Close()
	if err := errors.Join(loadErr, releaseErr, closeErr); err != nil {
		t.Fatal(err)
	}
	for _, plan := range run.Member.PortPlans {
		if plan.Port != productionContextPort {
			continue
		}
		for _, binding := range plan.Bindings {
			if binding.Provider.InstanceID != instanceID {
				continue
			}
			if len(binding.StaticContextRefs) != 1 {
				t.Fatalf("frozen Context Binding = %+v", binding)
			}
			record, found := run.FindContent(binding.StaticContextRefs[0])
			if !found || record.Kind != currentstore.ContentStaticContext {
				t.Fatalf("frozen static context %q = %+v found=%v", binding.StaticContextRefs[0], record, found)
			}
			staticContext, err := corecontract.RestoreStaticContextV1(record.CanonicalBytes)
			if err != nil {
				t.Fatal(err)
			}
			return moduleUpgradeApplyFrozenContextEvidenceV1{
				ProviderInstanceID: binding.Provider.InstanceID,
				StaticContextRef:   binding.StaticContextRefs[0],
				Text:               staticContext.Text,
			}
		}
	}
	t.Fatalf("Run %q has no frozen Context Binding for %q", runID, instanceID)
	return moduleUpgradeApplyFrozenContextEvidenceV1{}
}

func moduleUpgradeApplyAssertExactReplacementV1(
	t *testing.T,
	fixture moduleUpgradeCLIIntegrationFixtureV1,
	review moduleupgrade.ReviewV1,
	afterControl controlcontract.ControlSnapshot,
	afterCatalog controlcontract.CatalogGeneration,
) {
	t.Helper()
	before, found := fixture.upgradeBasis.Control.FindProfile(moduleApplyTestProfileID)
	if !found {
		t.Fatal("review basis Profile is absent")
	}
	after, found := afterControl.FindProfile(moduleApplyTestProfileID)
	if !found || len(after.Bindings) != len(before.Bindings) {
		t.Fatalf("replacement Profile before=%+v after=%+v", before, after)
	}
	_, targetCanonical, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text:          moduleUpgradeApplyTargetContextTextV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	targetRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentStaticContext,
		moduleApplyJSONMediaType,
		targetCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	expected := append([]controlcontract.BindingSpec(nil), before.Bindings...)
	ordinal := uint32(0)
	replacedAt := -1
	for index := range expected {
		if expected[index].Port != productionContextPort {
			continue
		}
		if ordinal == review.PortBindingIndex {
			if expected[index].InstanceID != fixture.current.InstanceID {
				t.Fatalf("reviewed current Binding = %+v", expected[index])
			}
			expected[index].InstanceID = fixture.upgradeBasis.Selection.TargetInstanceID
			expected[index].StaticContextRefs = []string{targetRef}
			replacedAt = index
			break
		}
		ordinal++
	}
	if replacedAt < 0 || !reflect.DeepEqual(after.Bindings, expected) {
		t.Fatalf(
			"exact ordinal replacement index=%d\n got=%+v\nwant=%+v",
			replacedAt,
			after.Bindings,
			expected,
		)
	}
	if _, found := afterCatalog.FindInstance(fixture.current.InstanceID); found {
		t.Fatal("last-reference replacement retained old current Catalog entry")
	}
	target, found := afterCatalog.FindInstance(
		fixture.upgradeBasis.Selection.TargetInstanceID,
	)
	if !found || target.Activation.ModuleID != fixture.targetReport.Module.ID ||
		target.Activation.Version != fixture.targetReport.Module.ExactVersion ||
		target.Activation.ArtifactDigest != fixture.targetReport.ArtifactDigest {
		t.Fatalf("replacement target Catalog entry=%+v found=%v", target, found)
	}
}
