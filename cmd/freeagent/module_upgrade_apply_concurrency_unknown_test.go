package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleUpgradeApplySameApprovalThirtyTwoConcurrentPublishesOnce(
	t *testing.T,
) {
	fixture := newModuleUpgradeApplyAcceptedFixtureV1(t)
	before := moduleApplyMutationRowCountsV1(t, fixture.databasePath)

	const callers = 32
	type outcome struct {
		result moduleUpgradeApplyCommandResultV1
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	for range callers {
		go func() {
			ready.Done()
			<-start
			var output bytes.Buffer
			err := runModuleUpgradeApply(
				context.Background(),
				fixture.applyArgs(),
				&output,
				ioDiscardModuleUpgradeApplyV1{},
			)
			var result moduleUpgradeApplyCommandResultV1
			if err == nil {
				if decodeErr := json.Unmarshal(output.Bytes(), &result); decodeErr != nil {
					err = decodeErr
				}
			} else if output.Len() != 0 {
				err = errors.Join(err, errors.New("failed Apply emitted output"))
			}
			outcomes <- outcome{result: result, err: err}
		}()
	}
	ready.Wait()
	close(start)

	applied := 0
	already := 0
	busy := 0
	var winning moduleUpgradeApplyCommandResultV1
	for range callers {
		got := <-outcomes
		if got.err != nil {
			if got.err.Error() != "freeagent module-upgrade-apply: failed (STORE_BUSY)" {
				t.Fatalf("concurrent approved Apply error=%v", got.err)
			}
			busy++
			continue
		}
		switch got.result.Status {
		case moduleApplyStatusApplied:
			applied++
			winning = got.result
		case moduleApplyStatusAlreadyApplied:
			already++
		default:
			t.Fatalf("concurrent approved Apply result = %+v", got.result)
		}
	}
	if applied != 1 || applied+already+busy != callers {
		t.Fatalf(
			"concurrent outcomes APPLIED=%d ALREADY_APPLIED=%d STORE_BUSY=%d",
			applied,
			already,
			busy,
		)
	}

	after := moduleApplyMutationRowCountsV1(t, fixture.databasePath)
	for _, table := range []string{
		"control_snapshots",
		"runtime_catalog_generations",
		"module_installations",
		"module_activations",
	} {
		if after[table] != before[table]+1 {
			t.Fatalf(
				"concurrent Apply %s rows before=%d after=%d, want exactly one new row",
				table,
				before[table],
				after[table],
			)
		}
	}
	if after["control_current"] != before["control_current"] {
		t.Fatalf(
			"concurrent Apply changed control_current row cardinality before=%d after=%d",
			before["control_current"],
			after["control_current"],
		)
	}

	store, err := currentstore.OpenExistingCurrentStore(
		context.Background(),
		fixture.databasePath,
	)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, readErr := store.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	closeErr := store.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	profile, found := control.FindProfile(moduleApplyTestProfileID)
	if !found || basis.PointerRevision != fixture.publishedBasis.PointerRevision+1 ||
		basis.PointerRevision != winning.PointerRevision ||
		basis.Control.SnapshotID != winning.ControlSnapshotID ||
		basis.Catalog.GenerationID != winning.CatalogGenerationID {
		t.Fatalf("concurrent final publication basis=%+v winner=%+v", basis, winning)
	}
	oldCount, targetCount := 0, 0
	for _, binding := range profile.Bindings {
		if binding.InstanceID == fixture.current.InstanceID {
			oldCount++
		}
		if binding.InstanceID == fixture.targetInstanceID {
			targetCount++
		}
	}
	if oldCount != 0 || targetCount != 1 {
		t.Fatalf("concurrent final Binding old=%d target=%d", oldCount, targetCount)
	}
	if _, found := catalog.FindInstance(fixture.current.InstanceID); found {
		t.Fatal("concurrent final Catalog retained replaced Instance")
	}
	if _, found := catalog.FindInstance(fixture.targetInstanceID); !found {
		t.Fatal("concurrent final Catalog lacks target Instance")
	}

	var retryOutput bytes.Buffer
	if err := runModuleUpgradeApply(
		context.Background(),
		fixture.applyArgs(),
		&retryOutput,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("post-concurrency exact retry: %v", err)
	}
	var retry moduleUpgradeApplyCommandResultV1
	decodeModuleOperatorOutputV1(t, retryOutput.Bytes(), &retry)
	if retry.Status != moduleApplyStatusAlreadyApplied ||
		retry.PlanDigest != winning.PlanDigest ||
		retry.PointerRevision != winning.PointerRevision ||
		retry.ControlSnapshotID != winning.ControlSnapshotID ||
		retry.CatalogGenerationID != winning.CatalogGenerationID {
		t.Fatalf("post-concurrency retry=%+v winner=%+v", retry, winning)
	}
	if final := moduleApplyMutationRowCountsV1(t, fixture.databasePath); !reflect.DeepEqual(final, after) {
		t.Fatalf("exact retry wrote rows before=%v after=%v", after, final)
	}
	assertNoModuleApplyStageResidueV1(t, fixture.artifactRoot)
}

func TestModuleUpgradeApplyExactReplacementUnknownDoesNotRebaseOrReplay(
	t *testing.T,
) {
	fixture := newModuleUpgradeApplyAcceptedFixtureV1(t)
	input := fixture.approvalInput()
	approval, err := loadModuleUpgradeApplyApprovalV1(
		context.Background(),
		fixture.databasePath,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, canonical, planDigest, err := buildApprovedModuleApplyPlanV1(
		moduleUpgradeApplyApprovalFromStoreV1(approval),
	)
	if err != nil || plan.ReplaceCurrentInstanceID != fixture.current.InstanceID ||
		plan.InstanceID != fixture.targetInstanceID ||
		plan.ExpectedPointerRevision != fixture.publishedBasis.PointerRevision ||
		!moduleapi.ValidSHA256(planDigest) || len(canonical) == 0 {
		t.Fatalf("approved exact plan=%+v digest=%q error=%v", plan, planDigest, err)
	}

	var output bytes.Buffer
	if err := runModuleUpgradeApply(
		context.Background(),
		fixture.applyArgs(),
		&output,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("approved Apply: %v", err)
	}
	var applied moduleUpgradeApplyCommandResultV1
	decodeModuleOperatorOutputV1(t, output.Bytes(), &applied)
	if applied.Status != moduleApplyStatusApplied || applied.PlanDigest != planDigest {
		t.Fatalf("approved Apply=%+v plan_digest=%s", applied, planDigest)
	}

	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	publication := currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: basis.PointerRevision - 1,
		NewPointerRevision:      basis.PointerRevision,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	}
	before := moduleApplyMutationRowCountsV1(t, fixture.databasePath)

	finalArtifact := filepath.Join(fixture.artifactRoot, fixture.targetReport.ArtifactDigest)
	manifestCanonical, err := os.ReadFile(filepath.Join(finalArtifact, moduleapi.ArtifactManifestPath))
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(finalArtifact, filepath.FromSlash(manifest.Runtime.Entrypoint)),
		[]byte(`{"schema_version":"static-context/v1","text":"tampered after exact U4 publication"}`),
		0o600,
	); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}

	_, reconcileErr := reconcileModulePublicationV1(
		ctx,
		store,
		fixture.artifactRoot,
		plan,
		planDigest,
		publication,
		errors.New("simulated ambiguous exact U4 PublishControlCatalog return"),
	)
	closeErr := store.Close()
	if code := moduleApplyFailureCodeOfV1(reconcileErr); code != moduleApplyFailureOutcomeUnknown {
		t.Fatalf("exact U4 reconciliation code=%q error=%v", code, reconcileErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if afterUnknown := moduleApplyMutationRowCountsV1(t, fixture.databasePath); !reflect.DeepEqual(afterUnknown, before) {
		t.Fatalf("UNKNOWN reconciliation wrote rows before=%v after=%v", before, afterUnknown)
	}

	var retryOutput bytes.Buffer
	retryErr := runModuleUpgradeApply(
		context.Background(),
		fixture.applyArgs(),
		&retryOutput,
		ioDiscardModuleUpgradeApplyV1{},
	)
	if retryErr == nil ||
		retryErr.Error() != "freeagent module-upgrade-apply: failed (APPROVAL_INVALID)" ||
		retryOutput.Len() != 0 {
		t.Fatalf("UNKNOWN exact retry error=%v output=%q", retryErr, retryOutput.String())
	}
	if afterRetry := moduleApplyMutationRowCountsV1(t, fixture.databasePath); !reflect.DeepEqual(afterRetry, before) {
		t.Fatalf("UNKNOWN retry rebased or replayed before=%v after=%v", before, afterRetry)
	}

	historical, err := loadModuleUpgradeApplyApprovalV1(
		context.Background(),
		fixture.databasePath,
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, rebuiltCanonical, rebuiltDigest, err := buildApprovedModuleApplyPlanV1(
		moduleUpgradeApplyApprovalFromStoreV1(historical),
	)
	if err != nil || rebuiltDigest != planDigest ||
		!bytes.Equal(rebuiltCanonical, canonical) ||
		rebuilt.ReplaceCurrentInstanceID != plan.ReplaceCurrentInstanceID ||
		rebuilt.InstanceID != plan.InstanceID ||
		rebuilt.ExpectedPointerRevision != plan.ExpectedPointerRevision {
		t.Fatalf(
			"UNKNOWN changed frozen plan rebuilt=%+v digest=%q error=%v",
			rebuilt,
			rebuiltDigest,
			err,
		)
	}
	assertNoModuleApplyStageResidueV1(t, fixture.artifactRoot)
}

func TestModuleUpgradeApplyFacadePropagatesLowerUnknownExactlyOnce(t *testing.T) {
	fixture := newModuleUpgradeApplyAcceptedFixtureV1(t)
	called := 0
	var captured moduleUpgradeApplyCommandRequestV1
	dependencies := moduleUpgradeApplyDependenciesV1{apply: func(
		_ context.Context,
		request moduleUpgradeApplyCommandRequestV1,
	) (moduleUpgradeApplyCommandResultV1, error) {
		called++
		captured = request
		return moduleUpgradeApplyCommandResultV1{}, newModuleApplyFailureV1(
			moduleApplyFailureOutcomeUnknown,
			errors.Join(context.Canceled, errors.New("ambiguous post-publication result")),
		)
	}}
	var output bytes.Buffer
	err := runModuleUpgradeApplyWithDependenciesV1(
		context.Background(),
		fixture.applyArgs(),
		&output,
		ioDiscardModuleUpgradeApplyV1{},
		dependencies,
	)
	if err == nil ||
		err.Error() != "freeagent module-upgrade-apply: failed (APPLY_OUTCOME_UNKNOWN)" ||
		called != 1 || output.Len() != 0 {
		t.Fatalf("lower UNKNOWN error=%v calls=%d output=%q", err, called, output.String())
	}
	if captured.ReviewID != fixture.reviewID ||
		captured.DecisionID != fixture.decisionID ||
		captured.TenantID != defaultTenantID ||
		captured.DatabasePath != fixture.databasePath ||
		captured.ArtifactRoot != fixture.artifactRoot ||
		captured.SourceRoot != fixture.sourceRoot ||
		captured.ArtifactDirectory != fixture.targetDirectory {
		t.Fatalf("lower UNKNOWN changed exact request: %+v", captured)
	}
	for _, grant := range []string{
		captured.LocalMCPArtifactGrant,
		captured.TrustedInProcessArtifactGrant,
		captured.RemoteActionArtifactGrant,
		captured.WASMActionArtifactGrant,
		captured.RemoteActionEndpointGrant,
		captured.RemoteActionSecretRefGrant,
		captured.ModelSecretRefGrant,
	} {
		if strings.TrimSpace(grant) != "" {
			t.Fatalf("lower UNKNOWN widened request with grant %q", grant)
		}
	}
}
