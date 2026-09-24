package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleartifactingress"
	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleUpgradeCommandsDefaultOffAndInvalidFlagsUseZeroDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var reviewCalls, decisionCalls int
	reviewDependencies := moduleUpgradeReviewDependenciesV1{review: func(context.Context, moduleUpgradeReviewCommandRequestV1) (any, error) {
		reviewCalls++
		return nil, errors.New("must not run")
	}}
	decisionDependencies := moduleUpgradeDecideDependenciesV1{decide: func(context.Context, moduleUpgradeDecideCommandRequestV1) (any, error) {
		decisionCalls++
		return nil, errors.New("must not run")
	}}
	if err := runModuleUpgradeReviewWithDependenciesV1(ctx, []string{"--db", "private.db"}, ioDiscard{}, ioDiscard{}, reviewDependencies); err == nil || !strings.Contains(err.Error(), "UPGRADE_REVIEW_DISABLED") {
		t.Fatalf("disabled review error=%v", err)
	}
	if err := runModuleUpgradeDecideWithDependenciesV1(ctx, []string{"--db", "private.db"}, ioDiscard{}, ioDiscard{}, decisionDependencies); err == nil || !strings.Contains(err.Error(), "UPGRADE_REVIEW_DISABLED") {
		t.Fatalf("disabled decision error=%v", err)
	}
	if err := runModuleUpgradeReviewWithDependenciesV1(ctx, []string{"--enable-module-upgrade-review", "--db", "private.db"}, ioDiscard{}, ioDiscard{}, reviewDependencies); err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
		t.Fatalf("invalid review error=%v", err)
	}
	if err := runModuleUpgradeDecideWithDependenciesV1(ctx, []string{"--enable-module-upgrade-review", "--db", "private.db"}, ioDiscard{}, ioDiscard{}, decisionDependencies); err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
		t.Fatalf("invalid decision error=%v", err)
	}
	if reviewCalls != 0 || decisionCalls != 0 {
		t.Fatalf("disabled/invalid commands invoked dependencies: review=%d decision=%d", reviewCalls, decisionCalls)
	}
}

func TestModuleUpgradeProductionSourceHasNoNetworkStageHostOrApplyPath(t *testing.T) {
	t.Parallel()
	source, err := os.ReadFile("module_upgrade.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"http.", "url.Parse", "os.MkdirTemp(", "os.Chmod(",
		"dryRunModulePlanV1(", ".PackagePath", "modulehost.",
		"moduleApplyStage", "Registry.Register", "ExecutePrepared(",
	} {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Fatalf("U3 production CLI contains forbidden capability %q", forbidden)
		}
	}
}

func TestModuleUpgradeReviewScopeFlagsAreExactAndSafeOutputOmitsLocalMaterial(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	args := moduleUpgradeReviewTestArgsV1(digest)
	var calls int
	dependencies := moduleUpgradeReviewDependenciesV1{review: func(_ context.Context, request moduleUpgradeReviewCommandRequestV1) (any, error) {
		calls++
		if request.BindingTarget.Kind != "PROFILE" || request.BindingTarget.ProfileID != "profile.a" || request.BindingTarget.WorkspaceID != "" {
			t.Fatalf("request target=%+v", request.BindingTarget)
		}
		return safeModuleUpgradeCommandResultV1(moduleUpgradeReviewResultSchemaV1, "RECORDED", request.TenantID, digest, digest, ""), nil
	}}
	var output bytes.Buffer
	if err := runModuleUpgradeReviewWithDependenciesV1(context.Background(), args, &output, ioDiscard{}, dependencies); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("review calls=%d", calls)
	}
	for _, forbidden := range []string{"private.db", "artifact-private", "signature", "package_path", "config", "authority", "public_key", "http://", "https://"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("safe output leaks %q: %s", forbidden, output.String())
		}
	}
	mixed := append(append([]string(nil), args...), "--workspace", "workspace.a")
	if err := runModuleUpgradeReviewWithDependenciesV1(context.Background(), mixed, ioDiscard{}, ioDiscard{}, dependencies); err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
		t.Fatalf("mixed PROFILE scope error=%v", err)
	}
	workspace := moduleUpgradeReviewTestArgsV1(digest)
	workspace = replaceModuleUpgradeTestFlagV1(workspace, "--target-kind", "WORKSPACE_CHANNEL_ENDPOINT")
	workspace = removeModuleUpgradeTestFlagV1(workspace, "--profile")
	workspace = append(workspace, "--workspace", "workspace.a", "--endpoint", "endpoint.a")
	workspace = replaceModuleUpgradeTestFlagV1(workspace, "--port", productionChannelPort.Name)
	workspace = replaceModuleUpgradeTestFlagV1(workspace, "--port-version", productionChannelPort.ExactVersion)
	workspace = append(workspace, "--profile", "profile.forbidden")
	if err := runModuleUpgradeReviewWithDependenciesV1(context.Background(), workspace, ioDiscard{}, ioDiscard{}, dependencies); err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
		t.Fatalf("mixed Workspace scope error=%v", err)
	}
	if calls != 1 {
		t.Fatalf("invalid scopes invoked dependency: calls=%d", calls)
	}
	if runtime.GOOS == "windows" {
		uncArtifactPath := string([]byte{92, 92}) + "server" + string([]byte{92}) + "share" + string([]byte{92}) + "artifact"
		unsafe := replaceModuleUpgradeTestFlagV1(args, "--artifact-directory", uncArtifactPath)
		if err := runModuleUpgradeReviewWithDependenciesV1(context.Background(), unsafe, ioDiscard{}, ioDiscard{}, dependencies); err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
			t.Fatalf("unsafe Windows artifact path error=%v", err)
		}
		if calls != 1 {
			t.Fatalf("unsafe Windows path invoked dependency: calls=%d", calls)
		}
	}
}

func TestModuleUpgradeTargetVerifierRejectsSecondScanDrift(t *testing.T) {
	t.Parallel()
	base := moduleUpgradeTestManifestCanonicalV1(t, "vendor.module", "2.0.0", moduleapi.RuntimeModeRequestDeclarative, moduleapi.RuntimeProtocolStaticV1, "content/context.json", nil)
	runtimeDrift := mutateModuleUpgradeTestManifestCanonicalV1(t, base, func(manifest *moduleapi.ModuleManifestV1) {
		manifest.Runtime.Entrypoint = "content/context-v2.json"
	})
	optionalBodyA := mutateModuleUpgradeTestManifestCanonicalV1(t, base, func(manifest *moduleapi.ModuleManifestV1) {
		manifest.ConfigSchema = json.RawMessage(`{"type":"object"}`)
	})
	optionalBodyB := mutateModuleUpgradeTestManifestCanonicalV1(t, base, func(manifest *moduleapi.ModuleManifestV1) {
		manifest.ConfigSchema = json.RawMessage(`{"additionalProperties":false,"type":"object"}`)
	})
	baseReport := moduleconformance.Report{
		SchemaVersion:     moduleconformance.ReportSchemaVersionV1,
		PackageAPIVersion: moduleapi.ModuleManifestAPIVersionV1,
		Module:            moduleconformance.ModuleIdentity{ID: "vendor.module", ExactVersion: "2.0.0"},
		ArtifactDigest:    strings.Repeat("a", 64),
		ArtifactSizeBytes: 128,
	}
	tests := []struct {
		name             string
		firstCanonical   []byte
		secondCanonical  []byte
		secondReportEdit func(*moduleconformance.Report)
	}{
		{
			name:            "report digest",
			firstCanonical:  base,
			secondCanonical: base,
			secondReportEdit: func(report *moduleconformance.Report) {
				report.ArtifactDigest = strings.Repeat("b", 64)
			},
		},
		{name: "same report different runtime", firstCanonical: base, secondCanonical: runtimeDrift},
		{name: "same report different optional body", firstCanonical: optionalBodyA, secondCanonical: optionalBodyB},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			verify := func(context.Context, string, uint64) (moduleconformance.Report, []byte, error) {
				calls++
				report := baseReport
				canonical := test.firstCanonical
				if calls == 2 {
					canonical = test.secondCanonical
					if test.secondReportEdit != nil {
						test.secondReportEdit(&report)
					}
				}
				return report, bytes.Clone(canonical), nil
			}
			_, _, _, err := verifyModuleUpgradeTargetArtifactV1(
				context.Background(), "artifact-private", 4096, verify,
			)
			if err == nil || !strings.Contains(err.Error(), "changed between independent scans") || calls != 2 {
				t.Fatalf("drift calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestModuleUpgradeDetachedSignatureUsesExactStoreKeyAndEnvelope(t *testing.T) {
	t.Parallel()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publisher, publisherCanonical, publisherID, err := moduleapi.NewModulePublisherKeyV1(moduleapi.ModulePublisherKeyV1{
		SchemaVersion:   moduleapi.ModulePublisherKeySchemaVersionV1,
		Algorithm:       moduleapi.ModuleSignatureAlgorithmEd25519V1,
		PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey),
	})
	if err != nil || publisher.PublisherKeyID != publisherID {
		t.Fatalf("publisher=%+v id=%q error=%v", publisher, publisherID, err)
	}
	artifactDigest := strings.Repeat("c", 64)
	message, err := moduleapi.ModuleSignatureInputV1(artifactDigest)
	if err != nil {
		t.Fatal(err)
	}
	_, signatureCanonical, signatureID, err := moduleapi.NewModuleSignatureV1(moduleapi.ModuleSignatureV1{
		SchemaVersion:   moduleapi.ModuleSignatureSchemaVersionV1,
		Algorithm:       moduleapi.ModuleSignatureAlgorithmEd25519V1,
		PublisherKeyID:  publisherID,
		ArtifactDigest:  artifactDigest,
		SignatureBase64: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, message)),
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	signaturePath := filepath.Join(root, "target.sig")
	if err := os.WriteFile(signaturePath, signatureCanonical, 0o600); err != nil {
		t.Fatal(err)
	}
	basis := currentstore.ModuleUpgradeReviewBasis{
		Source: currentstore.ModuleSource{Policy: moduleapi.ModuleSourcePolicyV1{
			SignatureRequired: true,
			PublisherKeyID:    publisherID,
		}},
		PublisherKey: &currentstore.ModulePublisherKey{
			PublisherKeyID: publisherID,
			Canonical:      publisherCanonical,
			Revision:       1,
		},
		TargetEntry: moduleapi.ModuleDiscoveryEntryV1{SignatureID: signatureID},
	}
	report := moduleconformance.Report{ArtifactDigest: artifactDigest}
	if err := verifyModuleUpgradeDetachedSignatureV1(context.Background(), signaturePath, basis, report); err != nil {
		t.Fatal(err)
	}
	basis.TargetEntry.SignatureID = strings.Repeat("d", 64)
	if err := verifyModuleUpgradeDetachedSignatureV1(context.Background(), signaturePath, basis, report); err == nil {
		t.Fatal("wrong Store signature identity was accepted")
	}
	unsigned := currentstore.ModuleUpgradeReviewBasis{Source: currentstore.ModuleSource{Policy: moduleapi.ModuleSourcePolicyV1{SignatureRequired: false}}}
	if err := verifyModuleUpgradeDetachedSignatureV1(context.Background(), "", unsigned, report); err != nil {
		t.Fatalf("unsigned Source without envelope: %v", err)
	}
	if err := verifyModuleUpgradeDetachedSignatureV1(context.Background(), signaturePath, unsigned, report); err == nil {
		t.Fatal("unsigned Source accepted signature-only input")
	}
}

func TestModuleUpgradeCLIUnsignedReviewDecisionOfflineChain(t *testing.T) {
	fixture := newModuleUpgradeCLIIntegrationFixtureV1(t)
	runtimeBefore := readW2E2RuntimeCountsV1(t, fixture.databasePath)
	wrong := replaceModuleUpgradeTestFlagV1(
		fixture.reviewArgs(),
		"--target-artifact-digest",
		strings.Repeat("f", 64),
	)
	if err := runModuleUpgradeReview(context.Background(), wrong, ioDiscard{}, ioDiscard{}); err == nil {
		t.Fatal("wrong target artifact identity was accepted")
	}
	var reviewOutput bytes.Buffer
	if err := runModuleUpgradeReview(context.Background(), append(fixture.reviewArgs(), "--operator-requested-rollback"), &reviewOutput, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	var reviewResult struct {
		SchemaVersion string                 `json:"schema_version"`
		Status        string                 `json:"status"`
		CandidateID   string                 `json:"candidate_id"`
		ReviewID      string                 `json:"review_id"`
		Review        moduleupgrade.ReviewV1 `json:"review"`
	}
	decodeModuleOperatorOutputV1(t, reviewOutput.Bytes(), &reviewResult)
	if reviewResult.SchemaVersion != moduleUpgradeReviewResultSchemaV1 || reviewResult.Status != "RECORDED" || reviewResult.Review.TenantID != defaultTenantID || reviewResult.Review.CandidateID != reviewResult.CandidateID || reviewResult.Review.Conclusion != moduleupgrade.ConclusionWouldApplyV1 || !containsModuleUpgradeReasonV1(reviewResult.Review.ReasonCodes, moduleupgrade.ReasonOperatorRequestedRollbackV1) || !moduleapi.ValidSHA256(reviewResult.CandidateID) || !moduleapi.ValidSHA256(reviewResult.ReviewID) {
		t.Fatalf("review output=%s", reviewOutput.Bytes())
	}
	for _, forbidden := range []string{fixture.root, fixture.databasePath, fixture.targetDirectory, "package_path", "signature_base64", "public_key_base64", `"config":`, `"authority_ceiling":`, "http://", "https://"} {
		if strings.Contains(reviewOutput.String(), forbidden) {
			t.Fatalf("review output leaked %q: %s", forbidden, reviewOutput.String())
		}
	}
	reasonPath := filepath.Join(fixture.root, "reason.txt")
	if err := os.WriteFile(reasonPath, []byte("approved after exact offline review"), 0o600); err != nil {
		t.Fatal(err)
	}
	var decisionOutput bytes.Buffer
	if err := runModuleUpgradeDecide(context.Background(), []string{
		"--enable-module-upgrade-review",
		"--db", fixture.databasePath,
		"--tenant", defaultTenantID,
		"--review-id", reviewResult.ReviewID,
		"--candidate-id", reviewResult.CandidateID,
		"--decision", "APPROVE",
		"--operator-principal", "operator.test",
		"--reason-file", reasonPath,
	}, &decisionOutput, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(decisionOutput.String(), reasonPath) || strings.Contains(decisionOutput.String(), "approved after") {
		t.Fatalf("decision output leaked local reason material: %s", decisionOutput.String())
	}
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	review, reviewErr := store.GetModuleUpgradeReview(context.Background(), reviewResult.ReviewID)
	targetManifestContent, targetManifestErr := store.GetContent(context.Background(), reviewResult.Review.Target.ManifestRef)
	decision, decisionErr := store.GetModuleCandidateDecision(context.Background(), reviewResult.ReviewID)
	afterBasis, _, _, basisErr := store.LoadPublishedBasis(context.Background(), defaultTenantID)
	closeErr := store.Close()
	if err := errors.Join(reviewErr, targetManifestErr, decisionErr, basisErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if targetManifestContent.Kind != currentstore.ContentModuleManifest {
		t.Fatalf("target Manifest parent kind=%q", targetManifestContent.Kind)
	}
	wantTargetManifest, err := moduleapi.ReadArtifactManifestV1FromDirectory(fixture.targetDirectory)
	if err != nil || !bytes.Equal(targetManifestContent.CanonicalBytes, wantTargetManifest) {
		t.Fatalf("target Manifest parent mismatch: error=%v", err)
	}
	if review.Review.Conclusion != moduleupgrade.ConclusionWouldApplyV1 || decision.Decision.Decision != moduleapi.ModuleCandidateDecisionApproveV1 || decision.Decision.OperatorPrincipalID != "operator.test" || afterBasis != fixture.publishedBasis {
		t.Fatalf("review=%+v decision=%+v before=%+v after=%+v", review.Review, decision.Decision, fixture.publishedBasis, afterBasis)
	}
	if runtimeAfter := readW2E2RuntimeCountsV1(t, fixture.databasePath); runtimeAfter != runtimeBefore {
		t.Fatalf("U3 review/decision changed runtime counts: before=%+v after=%+v", runtimeBefore, runtimeAfter)
	}
	before := decisionOutput.String()
	var retry bytes.Buffer
	if err := runModuleUpgradeDecide(context.Background(), []string{
		"--enable-module-upgrade-review", "--db", fixture.databasePath, "--tenant", defaultTenantID,
		"--review-id", reviewResult.ReviewID, "--candidate-id", reviewResult.CandidateID,
		"--decision", "APPROVE", "--operator-principal", "operator.test", "--reason-file", reasonPath,
	}, &retry, ioDiscard{}); err != nil || retry.String() != before {
		t.Fatalf("exact decision retry output=%q want=%q error=%v", retry.String(), before, err)
	}
	if err := os.WriteFile(reasonPath, []byte("a different exact decision"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runModuleUpgradeDecide(context.Background(), []string{
		"--enable-module-upgrade-review", "--db", fixture.databasePath, "--tenant", defaultTenantID,
		"--review-id", reviewResult.ReviewID, "--candidate-id", reviewResult.CandidateID,
		"--decision", "REJECT", "--operator-principal", "operator.test", "--reason-file", reasonPath,
		"--confirm-tenant-wide-reject",
	}, ioDiscard{}, ioDiscard{}); err == nil {
		t.Fatal("conflicting decision was accepted")
	}
}

func TestModuleUpgradeServerOwnedReviewConsumesAdmissionAndIsInert(t *testing.T) {
	ctx := context.Background()
	fixture := newModuleUpgradeCLIIntegrationFixtureV1(t)
	admittedPackage := filepath.Join(fixture.sourceRoot, "packages", "ignored-by-u3")
	copyModuleApplyTestTreeV1(t, fixture.targetDirectory, admittedPackage)

	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	ingress, err := runModuleArtifactIngressOperationV1(ctx, moduleartifactingress.RequestV1{
		SourceRoot:   fixture.sourceRoot,
		ArtifactRoot: filepath.Join(fixture.root, "artifacts"),
		Selection: moduleartifactingress.SelectionV1{
			SourceID: fixture.sourceID, SnapshotID: fixture.snapshotID,
			Module: moduleapi.Ref{
				ID:      fixture.targetReport.Module.ID,
				Version: fixture.targetReport.Module.ExactVersion,
			},
			ArtifactDigest: fixture.targetReport.ArtifactDigest,
		},
	}, store)
	if closeErr := store.Close(); err != nil || closeErr != nil {
		t.Fatalf("admit server-owned artifact: operation=%v close=%v", err, closeErr)
	}
	if ingress.Admission.AdmissionID == "" {
		t.Fatal("artifact ingress did not return an Admission")
	}

	deps := productionModuleUpgradeServerOwnedReviewDependenciesV1()
	deps.artifactRoot = func(string) string { return filepath.Join(fixture.root, "artifacts") }
	args := []string{
		"--enable-module-upgrade-review", "--db", fixture.databasePath,
		"--tenant", defaultTenantID, "--admission-id", ingress.Admission.AdmissionID,
		"--target-instance", fixture.upgradeBasis.Selection.TargetInstanceID,
		"--operator-principal", "operator.server-owned",
		"--review-request-digest", strings.Repeat("c", 64),
		"--port", productionContextPort.Name, "--port-version", productionContextPort.ExactVersion,
		"--port-binding-index", "0", "--target-kind", "PROFILE", "--profile", moduleApplyTestProfileID,
	}
	var first bytes.Buffer
	if err := runModuleUpgradeReviewServerOwnedWithDependenciesV1(ctx, args, &first, ioDiscard{}, deps); err != nil {
		t.Fatal(err)
	}
	runtimeBefore := readW2E2RuntimeCountsV1(t, fixture.databasePath)
	var firstResult moduleUpgradeReviewCommandResultV1
	decodeModuleOperatorOutputV1(t, first.Bytes(), &firstResult)
	if firstResult.SchemaVersion != moduleUpgradeServerOwnedReviewResultSchemaV1 ||
		firstResult.Review.ArtifactAdmissionID != ingress.Admission.AdmissionID ||
		firstResult.Review.OperatorPrincipalID != "operator.server-owned" {
		t.Fatalf("server-owned review=%s", first.Bytes())
	}
	if strings.Contains(first.String(), fixture.targetDirectory) || strings.Contains(first.String(), fixture.root) {
		t.Fatalf("server-owned review leaked local material: %s", first.Bytes())
	}
	var retry bytes.Buffer
	if err := runModuleUpgradeReviewServerOwnedWithDependenciesV1(ctx, args, &retry, ioDiscard{}, deps); err != nil || retry.String() != first.String() {
		t.Fatalf("server-owned exact retry output=%q want=%q error=%v", retry.String(), first.String(), err)
	}
	reasonPath := filepath.Join(fixture.root, "server-owned-decision-reason.txt")
	if err := os.WriteFile(reasonPath, []byte("approved after exact server-owned review"), 0o600); err != nil {
		t.Fatal(err)
	}
	decisionArgs := []string{
		"--enable-module-upgrade-review", "--db", fixture.databasePath,
		"--tenant", defaultTenantID, "--review-id", firstResult.ReviewID,
		"--decision", "APPROVE", "--operator-principal", "operator.decision",
		"--reason-file", reasonPath,
	}
	var decisionOutput bytes.Buffer
	if err := runModuleUpgradeDecisionServerOwned(ctx, decisionArgs, &decisionOutput, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	var decisionResult struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		TenantID      string `json:"tenant_id"`
		CandidateID   string `json:"candidate_id"`
		ReviewID      string `json:"review_id"`
		DecisionID    string `json:"decision_id"`
	}
	decodeModuleOperatorOutputV1(t, decisionOutput.Bytes(), &decisionResult)
	if decisionResult.SchemaVersion != moduleUpgradeDecisionResultSchemaV1 ||
		decisionResult.Status != "RECORDED" || decisionResult.TenantID != defaultTenantID ||
		decisionResult.ReviewID != firstResult.ReviewID || !moduleapi.ValidSHA256(decisionResult.DecisionID) {
		t.Fatalf("server-owned decision=%s", decisionOutput.Bytes())
	}
	var decisionRetry bytes.Buffer
	if err := runModuleUpgradeDecisionServerOwned(ctx, decisionArgs, &decisionRetry, ioDiscard{}); err != nil || decisionRetry.String() != decisionOutput.String() {
		t.Fatalf("server-owned exact decision retry output=%q want=%q error=%v", decisionRetry.String(), decisionOutput.String(), err)
	}
	store, err = currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := store.GetModuleCandidateDecision(ctx, firstResult.ReviewID)
	closeErr := store.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read server-owned decision: %v; close=%v", err, closeErr)
	}
	if decision.Decision.Decision != moduleapi.ModuleCandidateDecisionApproveV1 ||
		decision.Decision.OperatorPrincipalID != "operator.decision" {
		t.Fatalf("durable server-owned decision=%+v", decision.Decision)
	}
	if runtimeAfter := readW2E2RuntimeCountsV1(t, fixture.databasePath); runtimeAfter != runtimeBefore {
		t.Fatalf("server-owned Review/Decision changed runtime counts: before=%+v after=%+v", runtimeBefore, runtimeAfter)
	}
	bundlePath := filepath.Join(fixture.root, "server-owned-review.bundle")
	if _, err := currentbackup.CreateBundle(ctx, fixture.databasePath, filepath.Join(fixture.root, "artifacts"), bundlePath, "server-owned-review-test/v1"); err != nil {
		t.Fatalf("server-owned Review backup: %v", err)
	}
	if _, err := currentbackup.VerifyBundle(ctx, bundlePath); err != nil {
		t.Fatalf("server-owned Review backup verify: %v", err)
	}
	restoredRoot := filepath.Join(fixture.root, "restored-server-owned")
	if err := os.Mkdir(restoredRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	restoredDB := filepath.Join(restoredRoot, "current.sqlite")
	restoredArtifacts := filepath.Join(restoredRoot, "artifacts")
	if err := currentbackup.RestoreBundle(ctx, bundlePath, restoredDB, restoredArtifacts); err != nil {
		t.Fatalf("server-owned Review restore: %v", err)
	}
	if err := currentbackup.VerifyCurrentStoreSemanticClosure(ctx, restoredDB); err != nil {
		t.Fatalf("server-owned restored semantic closure: %v", err)
	}
	restoredStore, err := currentstore.OpenExistingCurrentStore(ctx, restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	restoredReview, reviewErr := restoredStore.GetModuleUpgradeReview(ctx, firstResult.ReviewID)
	restoredDecision, decisionErr := restoredStore.GetModuleCandidateDecision(ctx, firstResult.ReviewID)
	closeErr = restoredStore.Close()
	if err := errors.Join(reviewErr, decisionErr, closeErr); err != nil {
		t.Fatalf("read restored server-owned closure: %v", err)
	}
	if restoredReview.Review.ArtifactAdmissionID != ingress.Admission.AdmissionID || restoredDecision.DecisionID != decisionResult.DecisionID {
		t.Fatalf("restored server-owned closure review=%+v decision=%+v", restoredReview.Review, restoredDecision)
	}
	wrongTenant := append([]string(nil), decisionArgs...)
	wrongTenant = replaceModuleUpgradeTestFlagV1(wrongTenant, "--tenant", "tenant.other")
	if err := runModuleUpgradeDecisionServerOwned(ctx, wrongTenant, ioDiscard{}, ioDiscard{}); err == nil {
		t.Fatal("server-owned decision accepted a different tenant")
	}

	var artifactFile string
	if err := filepath.WalkDir(filepath.Join(fixture.root, "artifacts", ingress.Admission.Artifact.ArtifactDigest), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if artifactFile == "" && entry.Type().IsRegular() {
			artifactFile = path
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if artifactFile == "" {
		t.Fatal("server-owned artifact has no file to tamper")
	}
	content, err := os.ReadFile(artifactFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactFile, append(content, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runModuleUpgradeReviewServerOwnedWithDependenciesV1(ctx, args, ioDiscard{}, ioDiscard{}, deps); err == nil {
		t.Fatal("server-owned Review accepted a tampered Artifact")
	}
}

func TestModuleUpgradeCLIRejectRequiresExplicitTenantWideConfirmation(t *testing.T) {
	fixture := newModuleUpgradeCLIIntegrationFixtureV1(t)
	var reviewOutput bytes.Buffer
	if err := runModuleUpgradeReview(context.Background(), fixture.reviewArgs(), &reviewOutput, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	var reviewResult struct {
		SchemaVersion string                 `json:"schema_version"`
		Status        string                 `json:"status"`
		CandidateID   string                 `json:"candidate_id"`
		ReviewID      string                 `json:"review_id"`
		Review        moduleupgrade.ReviewV1 `json:"review"`
	}
	decodeModuleOperatorOutputV1(t, reviewOutput.Bytes(), &reviewResult)
	reasonPath := filepath.Join(fixture.root, "reject-reason.txt")
	if err := os.WriteFile(reasonPath, []byte("reject exact supply observation tenant-wide"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{
		"--enable-module-upgrade-review", "--db", fixture.databasePath, "--tenant", defaultTenantID,
		"--review-id", reviewResult.ReviewID, "--candidate-id", reviewResult.CandidateID,
		"--decision", "REJECT", "--operator-principal", "operator.test", "--reason-file", reasonPath,
	}
	if err := runModuleUpgradeDecide(context.Background(), base, ioDiscard{}, ioDiscard{}); err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
		t.Fatalf("unconfirmed tenant-wide reject error=%v", err)
	}
	var output bytes.Buffer
	if err := runModuleUpgradeDecide(context.Background(), append(base, "--confirm-tenant-wide-reject"), &output, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion string `json:"schema_version"`
		Status        string `json:"status"`
		TenantID      string `json:"tenant_id"`
		CandidateID   string `json:"candidate_id"`
		ReviewID      string `json:"review_id"`
		DecisionID    string `json:"decision_id"`
	}
	decodeModuleOperatorOutputV1(t, output.Bytes(), &result)
	if result.Status != "TENANT_WIDE_REJECT_RECORDED" || !moduleapi.ValidSHA256(result.DecisionID) {
		t.Fatalf("reject output=%s", output.Bytes())
	}
	approve := append([]string(nil), base...)
	approve = replaceModuleUpgradeTestFlagV1(approve, "--decision", "APPROVE")
	approve = append(approve, "--confirm-tenant-wide-reject")
	if err := runModuleUpgradeDecide(context.Background(), approve, ioDiscard{}, ioDiscard{}); err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
		t.Fatalf("APPROVE accepted tenant-wide reject confirmation: %v", err)
	}
}

func TestModuleUpgradeHandlerAssessmentUnknownPermissionExpansionAndOccupiedTarget(t *testing.T) {
	fixture := newModuleUpgradeCLIIntegrationFixtureV1(t)
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectory(fixture.targetDirectory)
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		t.Fatal(err)
	}

	unknown := target
	unknown.Runtime = moduleapi.RuntimeRequestV1{
		Mode:       moduleapi.RuntimeModeRequestRemote,
		Protocol:   "vendor-unknown/v1",
		Entrypoint: "vendor.adapter.unknown",
	}
	handler, grants, conclusion, reasons, err := assessModuleUpgradeHandlerV1(ctx, store, fixture.upgradeBasis, unknown, productionContextPort)
	if err != nil || handler.Status != moduleupgrade.HandlerUnsupportedV1 || conclusion != moduleupgrade.ConclusionUnsupportedV1 || len(grants) != 0 || !containsModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonHandlerUnsupportedV1) {
		t.Fatalf("unknown handler=%+v grants=%+v conclusion=%q reasons=%v error=%v", handler, grants, conclusion, reasons, err)
	}

	expanded := target
	expanded.RequestedPermissions = []moduleapi.Permission{"vendor.read"}
	handler, _, conclusion, reasons, err = assessModuleUpgradeHandlerV1(ctx, store, fixture.upgradeBasis, expanded, productionContextPort)
	if err != nil || handler.Status != moduleupgrade.HandlerSupportedV1 || conclusion != moduleupgrade.ConclusionConflictV1 || !containsModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonRequestedPermissionsExpandedV1) {
		t.Fatalf("permission expansion handler=%+v conclusion=%q reasons=%v error=%v", handler, conclusion, reasons, err)
	}

	occupied := fixture.upgradeBasis
	for _, entry := range occupied.Catalog.Entries {
		if entry.Activation.InstanceID != occupied.CurrentActivation.InstanceID {
			occupied.Selection.TargetInstanceID = entry.Activation.InstanceID
			break
		}
	}
	if occupied.Selection.TargetInstanceID == fixture.upgradeBasis.Selection.TargetInstanceID {
		t.Fatal("fixture has no second occupied Catalog instance")
	}
	handler, _, conclusion, reasons, err = assessModuleUpgradeHandlerV1(ctx, store, occupied, target, productionContextPort)
	if err != nil || handler.Status != moduleupgrade.HandlerSupportedV1 || conclusion != moduleupgrade.ConclusionConflictV1 || !containsModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonTargetInstanceConflictV1) {
		t.Fatalf("occupied target handler=%+v conclusion=%q reasons=%v error=%v", handler, conclusion, reasons, err)
	}

	fanout := fixture.upgradeBasis
	fanout.BindingImpacts = append([]moduleupgrade.BindingImpactV1(nil), fixture.upgradeBasis.BindingImpacts...)
	secondImpact := fanout.BindingImpacts[0]
	secondImpact.Port = productionActionPort
	secondImpact.PortBindingIndex = 0
	fanout.BindingImpacts = append(fanout.BindingImpacts, secondImpact)
	handler, _, conclusion, reasons, err = assessModuleUpgradeHandlerV1(ctx, store, fanout, target, productionContextPort)
	if err != nil || handler.Status != moduleupgrade.HandlerSupportedV1 || conclusion != moduleupgrade.ConclusionConflictV1 || !containsModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonTargetPortRemovedV1) {
		t.Fatalf("multi-Port fan-out handler=%+v conclusion=%q reasons=%v error=%v", handler, conclusion, reasons, err)
	}

	configConflictTarget := target
	configConflictTarget.Provides = append(configConflictTarget.Provides, productionActionPort)
	handler, _, conclusion, reasons, err = assessModuleUpgradeHandlerV1(ctx, store, fanout, configConflictTarget, productionContextPort)
	if err != nil || handler.Status != moduleupgrade.HandlerSupportedV1 || conclusion != moduleupgrade.ConclusionConflictV1 || !containsModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonPublishedBindingConflictV1) || containsModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonTargetPortRemovedV1) {
		t.Fatalf("fan-out config conflict handler=%+v conclusion=%q reasons=%v error=%v", handler, conclusion, reasons, err)
	}

	unknownFanout := fixture.upgradeBasis
	unknownFanout.BindingImpacts = append([]moduleupgrade.BindingImpactV1(nil), fixture.upgradeBasis.BindingImpacts...)
	var modelBindingFound bool
	for _, profile := range unknownFanout.Control.Profiles {
		for _, binding := range profile.Bindings {
			if binding.Port == productionModelPort {
				impact := unknownFanout.BindingImpacts[0]
				impact.Port = productionModelPort
				impact.ConfigRef = binding.ConfigRef
				impact.AuthorityCeilingRef = binding.AuthorityCeilingRef
				impact.FailurePolicy = binding.FailurePolicy
				unknownFanout.BindingImpacts = append(unknownFanout.BindingImpacts, impact)
				modelBindingFound = true
				break
			}
		}
		if modelBindingFound {
			break
		}
	}
	if !modelBindingFound {
		t.Fatal("fixture has no Model binding for fan-out projection")
	}
	unknownFanoutTarget := target
	unknownFanoutTarget.Provides = append(unknownFanoutTarget.Provides, productionModelPort)
	handler, grants, conclusion, reasons, err = assessModuleUpgradeHandlerV1(ctx, store, unknownFanout, unknownFanoutTarget, productionContextPort)
	if err != nil || handler.Status != moduleupgrade.HandlerSupportedV1 || conclusion != moduleupgrade.ConclusionConflictV1 || !containsModuleUpgradeReasonV1(reasons, moduleupgrade.ReasonPublishedBindingConflictV1) {
		t.Fatalf("fan-out incompatible handler=%+v grants=%+v conclusion=%q reasons=%v error=%v", handler, grants, conclusion, reasons, err)
	}

	wrongConfigKind := fixture.upgradeBasis
	wrongConfigKind.BindingImpacts = append([]moduleupgrade.BindingImpactV1(nil), fixture.upgradeBasis.BindingImpacts...)
	wrongConfigKind.BindingImpacts[0].ConfigRef = fixture.upgradeBasis.CurrentInstallation.ManifestRef
	if _, _, _, _, err := assessModuleUpgradeHandlerV1(ctx, store, wrongConfigKind, target, productionContextPort); !errors.Is(err, currentstore.ErrModuleUpgradeIntegrity) {
		t.Fatalf("wrong config Content kind error=%v", err)
	}
	wrongAuthorityKind := fixture.upgradeBasis
	wrongAuthorityKind.BindingImpacts = append([]moduleupgrade.BindingImpactV1(nil), fixture.upgradeBasis.BindingImpacts...)
	wrongAuthorityKind.BindingImpacts[0].AuthorityCeilingRef = fixture.upgradeBasis.CurrentInstallation.ManifestRef
	if _, _, _, _, err := assessModuleUpgradeHandlerV1(ctx, store, wrongAuthorityKind, target, productionContextPort); !errors.Is(err, currentstore.ErrModuleUpgradeIntegrity) {
		t.Fatalf("wrong authority Content kind error=%v", err)
	}
}

func TestModuleUpgradeHandlerProjectionCoversFrozenExactHandlerTables(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("a", 64)
	policies := make([]moduleApplyProtocolHandlerV1, 0, 11)
	for _, policy := range moduleApplyProtocolHandlerTableV1() {
		policies = append(policies, policy)
	}
	for _, policy := range moduleApplyExactSelectorProtocolHandlerTableV1() {
		policies = append(policies, policy)
	}
	if len(policies) != 11 {
		t.Fatalf("frozen handler projection rows=%d", len(policies))
	}
	for index, policy := range policies {
		policy := policy
		t.Run(strconv.Itoa(index)+"-"+string(policy.HandlerKind), func(t *testing.T) {
			var (
				resolved moduleApplyProtocolHandlerV1
				err      error
			)
			if index < len(moduleApplyProtocolHandlerTableV1()) {
				resolved, err = resolveModuleApplyProtocolHandlerV1(policy.protocolHandlerKeyV1())
			} else {
				resolved, err = resolveModuleApplyExactSelectorProtocolHandlerV1(policy.protocolHandlerKeyV1())
			}
			if err != nil || resolved != policy {
				t.Fatalf("exact resolver projection=%+v want=%+v error=%v", resolved, policy, err)
			}
			handler := moduleUpgradeHandlerAssessmentV1(resolved, moduleupgrade.HandlerSupportedV1)
			if handler.Status != moduleupgrade.HandlerSupportedV1 || handler.Kind != string(policy.HandlerKind) || handler.ExecutionClass != policy.ExecutionClass || handler.AdapterIdentity != policy.AdapterIdentity {
				t.Fatalf("handler projection=%+v policy=%+v", handler, policy)
			}
			artifactDigest := digest
			if policy.ArtifactDigest != "" {
				artifactDigest = policy.ArtifactDigest
			}
			grants := requiredModuleUpgradeGrantsV1(policy, moduleApplyBindingV1{}, defaultTenantID, artifactDigest)
			var wantKind moduleupgrade.RequiredGrantKindV1
			switch {
			case policy.RequiresLocalMCPGrant:
				wantKind = moduleupgrade.GrantLocalProcessArtifactV1
			case policy.RequiresTrustedInProcessArtifactGrant:
				wantKind = moduleupgrade.GrantTrustedInProcessArtifactV1
			case policy.RequiresRemoteActionArtifactGrant:
				wantKind = moduleupgrade.GrantRemoteActionArtifactV1
			case policy.RequiresWASMActionArtifactGrant:
				wantKind = moduleupgrade.GrantWASMActionArtifactV1
			}
			if wantKind == "" {
				if len(grants) != 0 {
					t.Fatalf("grant-free handler projected grants=%+v", grants)
				}
			} else if len(grants) == 0 || grants[0] != (moduleupgrade.RequiredGrantV1{Kind: wantKind, ReferenceDigest: artifactDigest}) {
				t.Fatalf("artifact grant projection=%+v want kind=%q digest=%q", grants, wantKind, artifactDigest)
			}
			conclusion := moduleupgrade.ConclusionWouldApplyV1
			if handler.Status != moduleupgrade.HandlerSupportedV1 {
				conclusion = moduleupgrade.ConclusionUnsupportedV1
			}
			if conclusion != moduleupgrade.ConclusionWouldApplyV1 {
				t.Fatalf("exact supported handler conclusion=%q", conclusion)
			}
		})
	}
}

func containsModuleUpgradeReasonV1(values []moduleupgrade.ReasonCodeV1, wanted moduleupgrade.ReasonCodeV1) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type moduleUpgradeCLIIntegrationFixtureV1 struct {
	root              string
	databasePath      string
	targetDirectory   string
	sourceRoot        string
	targetReport      moduleconformance.Report
	sourceID          string
	snapshotID        string
	current           moduleApplyDeclarativeFixtureV1
	currentActivation currentstore.ModuleActivation
	publishedBasis    controlcontract.PublishedBasis
	upgradeBasis      currentstore.ModuleUpgradeReviewBasis
}

func newModuleUpgradeCLIIntegrationFixtureV1(t *testing.T) moduleUpgradeCLIIntegrationFixtureV1 {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	current := newModuleApplyDeclarativeFixtureV1(t, moduleApplyRoleID, moduleApplyRoleInstance)
	planPath := writeModuleApplyPlanFixtureV1(t, filepath.Join(root, "enable-current.json"), newEnabledDeclarativeModuleApplyPlanV1(t, current, 1, 0))
	if _, err := runModuleApplyFixtureV1(databasePath, artifactRoot, planPath, current.ArtifactDirectory, ""); err != nil {
		t.Fatalf("apply current module: %v", err)
	}
	sourceRoot := filepath.Join(root, "source-root-unused-by-review")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	targetDirectory := filepath.Join(
		sourceRoot,
		"packages",
		"target-artifact-private",
	)
	copyModuleApplyTestTreeV1(t, current.ArtifactDirectory, targetDirectory)
	targetManifestPath := filepath.Join(targetDirectory, moduleapi.ArtifactManifestPath)
	manifestCanonical, err := os.ReadFile(targetManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = "2.0.0"
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(canonical); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetManifestPath, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	targetReport, err := moduleconformance.VerifyDirectory(context.Background(), targetDirectory)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := "vendor.upgrade.local"
	entry := moduleapi.ModuleDiscoveryEntryV1{
		Module:            moduleapi.Ref{ID: targetReport.Module.ID, Version: targetReport.Module.ExactVersion},
		ArtifactDigest:    targetReport.ArtifactDigest,
		ArtifactSizeBytes: targetReport.ArtifactSizeBytes,
		PackagePath:       "packages/ignored-by-u3",
	}
	_, indexCanonical, _, err := moduleapi.NewModuleDiscoveryIndexV1(moduleapi.ModuleDiscoveryIndexV1{
		SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
		SourceID:      sourceID,
		Entries:       []moduleapi.ModuleDiscoveryEntryV1{entry},
	})
	if err != nil {
		t.Fatal(err)
	}
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(moduleapi.ModuleSourceKindLocalDirectoryV1, []byte(filepath.ToSlash(sourceRoot)))
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, _, err := moduleapi.NewModuleSourcePolicyV1(moduleapi.ModuleSourcePolicyV1{
		SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
		SourceID:                sourceID,
		Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
		OriginDigest:            originDigest,
		Network:                 moduleapi.ModuleSourceNetworkDenyV1,
		AllowedModuleIDPrefixes: []string{"freeagent.example"},
		MaxIndexBytes:           uint64(len(indexCanonical)),
		MaxPackageBytes:         targetReport.ArtifactSizeBytes + 1024,
		MaxCandidates:           4,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.RegisterModuleSource(ctx, currentstore.RegisterModuleSourceInput{PolicyCanonical: policyCanonical, ExpectedPolicyRevision: 0}); err != nil {
		t.Fatal(err)
	}
	refreshBasis, err := store.ReadModuleSourceRefreshBasis(ctx, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.CommitModuleSourceRefresh(ctx, refreshBasis, indexCanonical)
	if err != nil {
		t.Fatal(err)
	}
	published, _, _, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	upgradeBasis, err := store.ReadModuleUpgradeReviewBasis(ctx, currentstore.ReadModuleUpgradeReviewBasisInput{
		SourceID:             sourceID,
		SnapshotID:           snapshot.SnapshotID,
		TenantID:             defaultTenantID,
		BindingTarget:        moduleupgrade.BindingTargetV1{Kind: moduleupgrade.BindingTargetProfileV1, ProfileID: moduleApplyTestProfileID},
		Port:                 productionContextPort,
		PortBindingIndex:     0,
		TargetModule:         entry.Module,
		TargetArtifactDigest: entry.ArtifactDigest,
		TargetInstanceID:     "role-architect-v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	return moduleUpgradeCLIIntegrationFixtureV1{
		root:              root,
		databasePath:      databasePath,
		targetDirectory:   targetDirectory,
		sourceRoot:        sourceRoot,
		targetReport:      targetReport,
		sourceID:          sourceID,
		snapshotID:        snapshot.SnapshotID,
		current:           current,
		currentActivation: upgradeBasis.CurrentActivation,
		publishedBasis:    published,
		upgradeBasis:      upgradeBasis,
	}
}

func (fixture moduleUpgradeCLIIntegrationFixtureV1) reviewArgs() []string {
	return []string{
		"--enable-module-upgrade-review",
		"--db", fixture.databasePath,
		"--tenant", defaultTenantID,
		"--expected-pointer-revision", strconv.FormatUint(fixture.publishedBasis.PointerRevision, 10),
		"--source-id", fixture.sourceID,
		"--snapshot-id", fixture.snapshotID,
		"--target-module", fixture.targetReport.Module.ID,
		"--target-version", fixture.targetReport.Module.ExactVersion,
		"--target-artifact-digest", fixture.targetReport.ArtifactDigest,
		"--target-instance", fixture.upgradeBasis.Selection.TargetInstanceID,
		"--current-instance", fixture.current.InstanceID,
		"--current-activation", fixture.currentActivation.ActivationID,
		"--current-module", fixture.current.ModuleID,
		"--current-version", moduleApplyTestVersion,
		"--current-artifact-digest", fixture.current.ArtifactDigest,
		"--port", productionContextPort.Name,
		"--port-version", productionContextPort.ExactVersion,
		"--port-binding-index", "0",
		"--target-kind", "PROFILE",
		"--profile", moduleApplyTestProfileID,
		"--artifact-directory", fixture.targetDirectory,
	}
}

func moduleUpgradeReviewTestArgsV1(digest string) []string {
	return []string{
		"--enable-module-upgrade-review", "--db", "private.db", "--tenant", "tenant.a",
		"--expected-pointer-revision", "1", "--source-id", "vendor.source", "--snapshot-id", digest,
		"--target-module", "vendor.module", "--target-version", "2.0.0", "--target-artifact-digest", digest,
		"--target-instance", "instance.target", "--current-instance", "instance.current",
		"--current-activation", "activation.current", "--current-module", "vendor.module",
		"--current-version", "1.0.0", "--current-artifact-digest", strings.Repeat("b", 64),
		"--port", productionContextPort.Name, "--port-version", productionContextPort.ExactVersion,
		"--port-binding-index", "0", "--target-kind", "PROFILE", "--profile", "profile.a",
		"--artifact-directory", "artifact-private",
	}
}

func replaceModuleUpgradeTestFlagV1(input []string, name, value string) []string {
	result := append([]string(nil), input...)
	for index := 0; index+1 < len(result); index++ {
		if result[index] == name {
			result[index+1] = value
			return result
		}
	}
	return result
}

func removeModuleUpgradeTestFlagV1(input []string, name string) []string {
	result := make([]string, 0, len(input))
	for index := 0; index < len(input); index++ {
		if input[index] == name && index+1 < len(input) {
			index++
			continue
		}
		result = append(result, input[index])
	}
	return result
}

func moduleUpgradeTestManifestCanonicalV1(
	t *testing.T,
	moduleID, version string,
	mode moduleapi.RuntimeModeRequest,
	protocol, entrypoint string,
	permissions []moduleapi.Permission,
) []byte {
	t.Helper()
	encoded, err := json.Marshal(moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         moduleID,
		Version:    version,
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       mode,
			Protocol:   protocol,
			Entrypoint: entrypoint,
		},
		Provides:             []moduleapi.PortRef{productionContextPort},
		RequestedPermissions: permissions,
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(canonical); err != nil {
		t.Fatal(err)
	}
	return canonical
}

func mutateModuleUpgradeTestManifestCanonicalV1(
	t *testing.T,
	input []byte,
	mutate func(*moduleapi.ModuleManifestV1),
) []byte {
	t.Helper()
	manifest, _, err := moduleapi.ParseModuleManifestV1(input)
	if err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, restored, err := moduleapi.ParseModuleManifestV1(canonical); err != nil || !bytes.Equal(restored, canonical) {
		t.Fatalf("mutated Manifest is not exact canonical: %v", err)
	}
	return canonical
}
