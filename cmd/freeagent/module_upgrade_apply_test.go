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
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulehandler"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestBuildApprovedModuleApplyPlanV1FreezesNarrowDeclarativeExactCoordinate(
	t *testing.T,
) {
	approval := validModuleUpgradeApplyApprovalV1(t)
	plan, canonical, digest, err := buildApprovedModuleApplyPlanV1(approval)
	if err != nil {
		t.Fatalf("build approved module Apply plan: %v", err)
	}
	impact := approval.Review.BindingImpacts[0]
	if plan.SchemaVersion != moduleApplyPlanSchemaV1 ||
		plan.DesiredState != moduleApplyEnabledV1 ||
		plan.TenantID != approval.TenantID ||
		plan.ExpectedPointerRevision != approval.Review.PublishedBasis.PointerRevision ||
		plan.BindingTarget != (moduleApplyBindingTargetV1{
			Kind:      moduleApplyBindingTargetProfileV1,
			ProfileID: approval.Review.BindingTarget.ProfileID,
		}) || plan.InstanceID != approval.Review.TargetInstanceID ||
		plan.ReplaceCurrentInstanceID != approval.Review.Current.Activation.InstanceID ||
		plan.Port != productionContextPort || plan.Module == nil ||
		plan.Binding == nil {
		t.Fatalf("mapped plan = %+v", plan)
	}
	if *plan.Module != (moduleApplyModuleV1{
		ID:                approval.Review.Target.Module.ID,
		ExactVersion:      approval.Review.Target.Module.Version,
		ArtifactDigest:    approval.Review.Target.ArtifactDigest,
		ArtifactSizeBytes: approval.Review.Target.ArtifactSizeBytes,
		ExpectedRuntimeRequest: moduleApplyExpectedRuntimeRequestV1{
			Mode:     moduleapi.RuntimeModeRequestDeclarative,
			Protocol: moduleapi.RuntimeProtocolStaticV1,
		},
	}) {
		t.Fatalf("mapped module = %+v", *plan.Module)
	}
	if plan.Binding.PortBindingIndex != impact.PortBindingIndex ||
		plan.Binding.FailurePolicy != impact.FailurePolicy ||
		!bytes.Equal(plan.Binding.Config, approval.ConfigCanonical) ||
		!bytes.Equal(plan.Binding.AuthorityCeiling, approval.AuthorityCanonical) {
		t.Fatalf("mapped Binding = %+v", *plan.Binding)
	}
	if bytes.Contains(canonical, []byte("static_context_refs")) {
		t.Fatalf("mapped plan copied current static context refs: %s", canonical)
	}
	restored, rebuilt, rebuiltDigest, err := restoreModuleApplyPlanV1(canonical)
	if err != nil || !reflect.DeepEqual(restored, plan) ||
		!bytes.Equal(rebuilt, canonical) || rebuiltDigest != digest {
		t.Fatalf("mapped plan identity failed: %+v %x %s %v", restored, rebuilt, rebuiltDigest, err)
	}

	approval.ConfigCanonical[0] = ' '
	approval.AuthorityCanonical[0] = ' '
	if plan.Binding.Config[0] == ' ' || plan.Binding.AuthorityCeiling[0] == ' ' {
		t.Fatal("mapped plan aliases approval content")
	}
}

func TestBuildApprovedModuleApplyPlanV1RejectsBroaderOrStaleShapes(t *testing.T) {
	tests := []struct {
		name string
		want error
		edit func(*moduleUpgradeApplyApprovalV1)
	}{
		{
			name: "rejected decision",
			want: errModuleUpgradeApplyApprovalV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Decision.Decision = moduleapi.ModuleCandidateDecisionRejectV1
			},
		},
		{
			name: "non-WOULD_APPLY review",
			want: errModuleUpgradeApplyApprovalV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.Conclusion = moduleupgrade.ConclusionConflictV1
			},
		},
		{
			name: "Model single-provider Port",
			want: errModuleUpgradeApplyUnsupportedV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.Port = productionModelPort
				value.Review.BindingImpacts[0].Port = productionModelPort
			},
		},
		{
			name: "Channel single-provider target",
			want: errModuleUpgradeApplyUnsupportedV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.BindingTarget = moduleupgrade.BindingTargetV1{
					Kind:        moduleupgrade.BindingTargetWorkspaceChannelEndpointV1,
					WorkspaceID: "workspace-1", EndpointID: "endpoint-1",
				}
			},
		},
		{
			name: "Action Port",
			want: errModuleUpgradeApplyUnsupportedV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.Port = productionActionPort
				value.Review.BindingImpacts[0].Port = productionActionPort
			},
		},
		{
			name: "multi-Binding fanout",
			want: errModuleUpgradeApplyUnsupportedV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				second := value.Review.BindingImpacts[0]
				second.BindingTarget.ProfileID = "profile-2"
				value.Review.BindingImpacts = append(value.Review.BindingImpacts, second)
			},
		},
		{
			name: "impact selection drift",
			want: errModuleUpgradeApplyApprovalV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.BindingImpacts[0].PortBindingIndex++
			},
		},
		{
			name: "same current and target instance",
			want: errModuleUpgradeApplyApprovalV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.TargetInstanceID = value.Review.Current.Activation.InstanceID
				value.Review.BindingImpacts[0].TargetInstanceID = value.Review.TargetInstanceID
			},
		},
		{
			name: "cross-module replacement",
			want: errModuleUpgradeApplyApprovalV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.Current.Activation.ModuleID = "vendor.other"
			},
		},
		{
			name: "same exact version replacement",
			want: errModuleUpgradeApplyApprovalV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.Current.Activation.Version = value.Review.Target.Module.Version
			},
		},
		{
			name: "same artifact replacement",
			want: errModuleUpgradeApplyApprovalV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.Current.Activation.ArtifactDigest = value.Review.Target.ArtifactDigest
			},
		},
		{
			name: "grant-bearing review",
			want: errModuleUpgradeApplyUnsupportedV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.RequiredGrants = []moduleupgrade.RequiredGrantV1{{
					Kind:            moduleupgrade.GrantTrustedInProcessArtifactV1,
					ReferenceDigest: value.Review.Target.ArtifactDigest,
				}}
			},
		},
		{
			name: "handler drift",
			want: errModuleUpgradeApplyUnsupportedV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.Handler.AdapterIdentity = "freeagent.adapter.other/v1"
			},
		},
		{
			name: "target Manifest permissions",
			want: errModuleUpgradeApplyUnsupportedV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.Target.Manifest.RequestedPermissions = []moduleapi.Permission{
					moduleapi.PermissionKnowledgeReadV1,
				}
			},
		},
		{
			name: "untrusted placement",
			want: errModuleUpgradeApplyUnsupportedV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				_, canonical, err := moduleapi.NewContextBindingConfigV1(
					moduleapi.ContextBindingConfigV1{
						SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
						Placement:     moduleapi.ContextPlacementUntrustedData,
						Parameters:    json.RawMessage(`{}`),
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				value.ConfigCanonical = canonical
				value.Review.BindingImpacts[0].ConfigRef = moduleUpgradeApplyContentRefV1(
					t, currentstore.ContentConfig, canonical,
				)
			},
		},
		{
			name: "non-deny authority",
			want: errModuleUpgradeApplyUnsupportedV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				canonical := []byte(`{"effects":["read_only"],"filesystem_roots":[],"network_allowlist":[],"schema_version":"authority-ceiling/v1","secret_refs":[]}`)
				value.AuthorityCanonical = canonical
				value.Review.BindingImpacts[0].AuthorityCeilingRef = moduleUpgradeApplyContentRefV1(
					t, currentstore.ContentAuthorityCeiling, canonical,
				)
			},
		},
		{
			name: "config content drift",
			want: errModuleUpgradeApplyApprovalV1,
			edit: func(value *moduleUpgradeApplyApprovalV1) {
				value.Review.BindingImpacts[0].ConfigRef = strings.Repeat("f", 64)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			approval := validModuleUpgradeApplyApprovalV1(t)
			test.edit(&approval)
			if _, _, _, err := buildApprovedModuleApplyPlanV1(approval); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestRunModuleUpgradeApplyFacadeIsDefaultOffAndExplicit(t *testing.T) {
	windowsFixturePath := func(leaf string) string {
		return string([]byte{'D', ':', '\\'}) + leaf
	}
	storePath := windowsFixturePath("store.db")
	artifactRoot := windowsFixturePath("artifacts")
	sourceRoot := windowsFixturePath("source")
	artifactDirectory := windowsFixturePath("artifact")
	called := 0
	dependencies := moduleUpgradeApplyDependenciesV1{apply: func(
		_ context.Context,
		request moduleUpgradeApplyCommandRequestV1,
	) (moduleUpgradeApplyCommandResultV1, error) {
		called++
		if request.TenantID != "tenant-1" ||
			request.ReviewID != strings.Repeat("a", 64) ||
			request.DecisionID != strings.Repeat("b", 64) ||
			request.SourceRoot != sourceRoot ||
			request.ArtifactDirectory != artifactDirectory ||
			request.SignaturePath != "" {
			t.Fatalf("request = %+v", request)
		}
		return moduleUpgradeApplyCommandResultV1{
			SchemaVersion:       moduleUpgradeApplyResultSchemaV1,
			Status:              moduleApplyStatusApplied,
			TenantID:            request.TenantID,
			ReviewID:            request.ReviewID,
			DecisionID:          request.DecisionID,
			PlanDigest:          strings.Repeat("c", 64),
			PointerRevision:     2,
			ControlSnapshotID:   strings.Repeat("d", 64),
			CatalogGenerationID: strings.Repeat("e", 64),
		}, nil
	}}
	var stdout bytes.Buffer
	err := runModuleUpgradeApplyWithDependenciesV1(
		context.Background(),
		[]string{"--db", "ignored"},
		&stdout,
		ioDiscardModuleUpgradeApplyV1{},
		dependencies,
	)
	if err == nil || err.Error() != "freeagent module-upgrade-apply: failed (UPGRADE_APPLY_DISABLED)" ||
		called != 0 || stdout.Len() != 0 {
		t.Fatalf("disabled result: err=%v called=%d stdout=%q", err, called, stdout.String())
	}

	base := []string{
		"--enable-module-upgrade-apply",
		"--db", storePath,
		"--artifact-root", artifactRoot,
		"--source-root", sourceRoot,
		"--tenant", "tenant-1",
		"--review-id", strings.Repeat("a", 64),
		"--decision-id", strings.Repeat("b", 64),
		"--artifact", artifactDirectory,
		"--signature=",
		"--allow-local-mcp-artifact=",
		"--allow-trusted-in-process-artifact=",
		"--allow-remote-action-artifact=",
		"--allow-wasm-action-artifact=",
		"--allow-remote-action-endpoint=",
		"--allow-remote-action-secret-ref=",
	}
	stdout.Reset()
	err = runModuleUpgradeApplyWithDependenciesV1(
		context.Background(),
		base,
		&stdout,
		ioDiscardModuleUpgradeApplyV1{},
		dependencies,
	)
	if err == nil || err.Error() != "freeagent module-upgrade-apply: failed (INVALID_FLAGS)" ||
		called != 0 || stdout.Len() != 0 {
		t.Fatalf("implicit grant flag result: err=%v called=%d stdout=%q", err, called, stdout.String())
	}

	args := append(append([]string{}, base...), "--allow-model-secret-ref=")
	stdout.Reset()
	err = runModuleUpgradeApplyWithDependenciesV1(
		context.Background(),
		args,
		&stdout,
		ioDiscardModuleUpgradeApplyV1{},
		dependencies,
	)
	if err != nil || called != 1 ||
		stdout.String() != `{"catalog_generation_id":"`+strings.Repeat("e", 64)+`","control_snapshot_id":"`+strings.Repeat("d", 64)+`","decision_id":"`+strings.Repeat("b", 64)+`","plan_digest":"`+strings.Repeat("c", 64)+`","pointer_revision":2,"review_id":"`+strings.Repeat("a", 64)+`","schema_version":"freeagent.module-upgrade-apply-command-result/v1","status":"APPLIED","tenant_id":"tenant-1"}`+"\n" {
		t.Fatalf("enabled result: err=%v called=%d stdout=%q", err, called, stdout.String())
	}

	nonemptyGrant := append([]string{}, args...)
	nonemptyGrant[len(nonemptyGrant)-1] = "--allow-model-secret-ref=secret-ref"
	stdout.Reset()
	err = runModuleUpgradeApplyWithDependenciesV1(
		context.Background(),
		nonemptyGrant,
		&stdout,
		ioDiscardModuleUpgradeApplyV1{},
		dependencies,
	)
	if err == nil || err.Error() != "freeagent module-upgrade-apply: failed (APPLY_UNSUPPORTED)" ||
		called != 1 || stdout.Len() != 0 {
		t.Fatalf("grant-bearing result: err=%v called=%d stdout=%q", err, called, stdout.String())
	}
}

func TestModuleUpgradeApplyFailureCodePreservesCommitBoundaryMeaning(
	t *testing.T,
) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want moduleUpgradeApplyFailureCodeV1
	}{
		{
			name: "outcome unknown overrides wrapped cancellation",
			err: newModuleApplyFailureV1(
				moduleApplyFailureOutcomeUnknown,
				errors.Join(errModuleUpgradeApplyApprovalV1, context.Canceled),
			),
			want: moduleUpgradeApplyFailureOutcomeUnknownV1,
		},
		{
			name: "typed cancellation overrides approval cause",
			err: newModuleApplyFailureV1(
				moduleApplyFailureCancelled,
				errors.Join(errModuleUpgradeApplyApprovalV1, context.Canceled),
			),
			want: moduleUpgradeApplyFailureCancelledV1,
		},
		{
			name: "pointer conflict",
			err: newModuleApplyFailureV1(
				moduleApplyFailurePointer,
				errors.New("another plan owns the pointer"),
			),
			want: moduleUpgradeApplyFailurePointerV1,
		},
		{
			name: "typed store busy",
			err: newModuleApplyFailureV1(
				moduleApplyFailureStoreBusy,
				currentstore.ErrOwnerActive,
			),
			want: moduleUpgradeApplyFailureStoreBusyV1,
		},
		{
			name: "artifact invalid belongs to approved replacement",
			err: newModuleApplyFailureV1(
				moduleApplyFailureArtifact,
				errors.New("artifact differs from approved evidence"),
			),
			want: moduleUpgradeApplyFailureApprovalV1,
		},
		{
			name: "target conflict belongs to approved replacement",
			err: newModuleApplyFailureV1(
				moduleApplyFailureTarget,
				errors.New("target differs from approved replacement"),
			),
			want: moduleUpgradeApplyFailureApprovalV1,
		},
		{
			name: "generated plan invalid belongs to approved replacement",
			err: newModuleApplyFailureV1(
				moduleApplyFailurePlanInvalid,
				errors.New("approved replacement plan is invalid"),
			),
			want: moduleUpgradeApplyFailureApprovalV1,
		},
		{
			name: "owner active",
			err:  currentstore.ErrOwnerActive,
			want: moduleUpgradeApplyFailureStoreBusyV1,
		},
		{
			name: "cancelled",
			err:  context.Canceled,
			want: moduleUpgradeApplyFailureCancelledV1,
		},
		{
			name: "approval",
			err:  errors.Join(errModuleUpgradeApplyApprovalV1, errors.New("stale")),
			want: moduleUpgradeApplyFailureApprovalV1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := moduleUpgradeApplyFailureCodeOfV1(test.err); got != test.want {
				t.Fatalf("failure code = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLoadModuleUpgradeApplyApprovalV1ClassifiesSemanticFailuresOnly(
	t *testing.T,
) {
	fixture := newModuleUpgradeCLIIntegrationFixtureV1(t)
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

	reasonPath := filepath.Join(fixture.root, "rejected-apply-reason.txt")
	if err := os.WriteFile(
		reasonPath,
		[]byte("reject exact governed replacement tenant-wide"),
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
			"--decision", "REJECT",
			"--operator-principal", "operator.u4.reject",
			"--reason-file", reasonPath,
			"--confirm-tenant-wide-reject",
		},
		&decisionOutput,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("record REJECT Decision: %v", err)
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

	wrongReviewID := strings.Repeat("f", moduleapi.SHA256HexLength)
	if wrongReviewID == reviewResult.ReviewID {
		wrongReviewID = strings.Repeat("e", moduleapi.SHA256HexLength)
	}
	wrongDecisionID := strings.Repeat("f", moduleapi.SHA256HexLength)
	if wrongDecisionID == decisionResult.DecisionID {
		wrongDecisionID = strings.Repeat("e", moduleapi.SHA256HexLength)
	}
	tests := []struct {
		name  string
		input currentstore.ModuleUpgradeApplyApprovalInput
		want  error
	}{
		{
			name: "wrong review",
			input: currentstore.ModuleUpgradeApplyApprovalInput{
				TenantID: defaultTenantID, ReviewID: wrongReviewID,
				DecisionID: decisionResult.DecisionID,
			},
			want: currentstore.ErrModuleUpgradeReviewNotFound,
		},
		{
			name: "wrong decision",
			input: currentstore.ModuleUpgradeApplyApprovalInput{
				TenantID: defaultTenantID, ReviewID: reviewResult.ReviewID,
				DecisionID: wrongDecisionID,
			},
			want: currentstore.ErrModuleUpgradeReviewConflict,
		},
		{
			name: "rejected approval",
			input: currentstore.ModuleUpgradeApplyApprovalInput{
				TenantID: defaultTenantID, ReviewID: reviewResult.ReviewID,
				DecisionID: decisionResult.DecisionID,
			},
			want: currentstore.ErrModuleUpgradeReviewConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := loadModuleUpgradeApplyApprovalV1(
				context.Background(),
				fixture.databasePath,
				test.input,
			)
			if !errors.Is(err, errModuleUpgradeApplyApprovalV1) ||
				!errors.Is(err, test.want) {
				t.Fatalf("approval load error = %v, want approval + %v", err, test.want)
			}
		})
	}

	_, openErr := loadModuleUpgradeApplyApprovalV1(
		context.Background(),
		filepath.Join(fixture.root, "absent-current.sqlite"),
		currentstore.ModuleUpgradeApplyApprovalInput{
			TenantID: defaultTenantID, ReviewID: reviewResult.ReviewID,
			DecisionID: decisionResult.DecisionID,
		},
	)
	if openErr == nil || errors.Is(openErr, errModuleUpgradeApplyApprovalV1) {
		t.Fatalf("Open error = %v, want raw non-approval failure", openErr)
	}
}

func TestVerifyApprovedModuleUpgradeArtifactV1UsesExplicitGovernedSourceRoot(
	t *testing.T,
) {
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		"u4-source-verification",
	)
	sourceRoot := filepath.Join(t.TempDir(), "source")
	artifactDirectory := filepath.Join(sourceRoot, "packages", "role")
	copyModuleApplyTestTreeV1(t, fixture.ArtifactDirectory, artifactDirectory)
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		context.Background(),
		artifactDirectory,
	)
	if err != nil {
		t.Fatal(err)
	}
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte(filepath.ToSlash(sourceRoot)),
	)
	if err != nil {
		t.Fatal(err)
	}
	policy, policyCanonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(
		moduleapi.ModuleSourcePolicyV1{
			SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
			SourceID:                "local.u4-approved-apply",
			Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 moduleapi.ModuleSourceNetworkDenyV1,
			AllowedModuleIDPrefixes: []string{"freeagent.example"},
			MaxIndexBytes:           4096,
			MaxPackageBytes:         fixture.ArtifactSizeBytes,
			MaxCandidates:           1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	entry := moduleapi.ModuleDiscoveryEntryV1{
		Module: moduleapi.Ref{
			ID: fixture.ModuleID, Version: moduleApplyTestVersion,
		},
		ArtifactDigest:    fixture.ArtifactDigest,
		ArtifactSizeBytes: fixture.ArtifactSizeBytes,
		PackagePath:       "ignored/by/approved/apply",
	}
	approval := currentstore.ModuleUpgradeApplyApproval{
		Input: currentstore.ModuleUpgradeApplyApprovalInput{
			TenantID: "tenant-1", ReviewID: strings.Repeat("a", 64),
			DecisionID: strings.Repeat("b", 64),
		},
		Review: currentstore.ModuleUpgradeReviewRecord{
			Review: moduleupgrade.ReviewV1{
				Target: moduleupgrade.TargetEvidenceV1{
					Module: entry.Module, ArtifactDigest: entry.ArtifactDigest,
					ArtifactSizeBytes: entry.ArtifactSizeBytes,
				},
			},
		},
		Source: currentstore.ModuleSource{
			SourceID: policy.SourceID, Policy: policy,
			PolicyID: policyID, PolicyCanonical: bytes.Clone(policyCanonical),
		},
		Snapshot: currentstore.ModuleDiscoverySnapshot{
			SourcePolicyID:        policyID,
			SourcePolicyCanonical: bytes.Clone(policyCanonical),
		},
		TargetEntry: entry,
		TargetManifest: currentstore.ContentRecord{
			Kind: currentstore.ContentModuleManifest, MediaType: moduleApplyJSONMediaType,
			CanonicalBytes: bytes.Clone(manifestCanonical),
		},
	}
	request := moduleUpgradeApplyCommandRequestV1{
		SourceRoot: sourceRoot, ArtifactDirectory: artifactDirectory,
	}
	result, err := verifyApprovedModuleUpgradeArtifactV1(
		context.Background(),
		request,
		approval,
		historicalModuleUpgradeApplySourcePolicyV1(approval),
	)
	if err != nil || result.ArtifactDigest != fixture.ArtifactDigest ||
		result.ArtifactSizeBytes != fixture.ArtifactSizeBytes {
		t.Fatalf("historical source verification: %+v, %v", result, err)
	}
	if _, err := verifyApprovedModuleUpgradeArtifactV1(
		context.Background(),
		request,
		approval,
		currentModuleUpgradeApplySourcePolicyV1(approval),
	); err != nil {
		t.Fatalf("current source verification: %v", err)
	}

	outside := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		"u4-outside-source",
	)
	request.ArtifactDirectory = outside.ArtifactDirectory
	if _, err := verifyApprovedModuleUpgradeArtifactV1(
		context.Background(),
		request,
		approval,
		historicalModuleUpgradeApplySourcePolicyV1(approval),
	); !errors.Is(err, errModuleUpgradeApplyApprovalV1) {
		t.Fatalf("artifact outside Source root error = %v", err)
	}
}

func TestModuleUpgradeApplyProductionExactReplacementAndAdjacentRetry(
	t *testing.T,
) {
	fixture := newModuleUpgradeCLIIntegrationFixtureV1(t)
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
	reasonPath := filepath.Join(fixture.root, "approved-apply-reason.txt")
	if err := os.WriteFile(
		reasonPath,
		[]byte("approve exact governed replacement"),
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
			"--operator-principal", "operator.u4",
			"--reason-file", reasonPath,
		},
		&decisionOutput,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("record Decision: %v", err)
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
	artifactRoot := filepath.Join(fixture.root, "artifacts")
	applyArgs := []string{
		"--enable-module-upgrade-apply",
		"--db", fixture.databasePath,
		"--artifact-root", artifactRoot,
		"--source-root", fixture.sourceRoot,
		"--tenant", defaultTenantID,
		"--review-id", reviewResult.ReviewID,
		"--decision-id", decisionResult.DecisionID,
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
	var applyOutput bytes.Buffer
	if err := runModuleUpgradeApply(
		context.Background(),
		applyArgs,
		&applyOutput,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("approved Apply: %v", err)
	}
	var applied moduleUpgradeApplyCommandResultV1
	decodeModuleOperatorOutputV1(t, applyOutput.Bytes(), &applied)
	if applied.SchemaVersion != moduleUpgradeApplyResultSchemaV1 ||
		applied.Status != moduleApplyStatusApplied ||
		applied.TenantID != defaultTenantID ||
		applied.ReviewID != reviewResult.ReviewID ||
		applied.DecisionID != decisionResult.DecisionID ||
		applied.PointerRevision != fixture.publishedBasis.PointerRevision+1 ||
		!moduleapi.ValidSHA256(applied.PlanDigest) ||
		applied.ControlSnapshotID == "" || applied.CatalogGenerationID == "" {
		t.Fatalf("approved Apply result = %+v", applied)
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
	if !found {
		t.Fatal("approved replacement Profile is absent")
	}
	oldCount, targetCount := 0, 0
	for _, binding := range profile.Bindings {
		if binding.InstanceID == fixture.current.InstanceID {
			oldCount++
		}
		if binding.InstanceID == fixture.upgradeBasis.Selection.TargetInstanceID {
			targetCount++
		}
	}
	if oldCount != 0 || targetCount != 1 ||
		basis.PointerRevision != applied.PointerRevision {
		t.Fatalf("replacement Binding closure old=%d target=%d basis=%+v", oldCount, targetCount, basis)
	}
	if _, found := catalog.FindInstance(fixture.current.InstanceID); found {
		t.Fatal("approved replacement retained old Catalog Instance")
	}
	targetEntry, found := catalog.FindInstance(
		fixture.upgradeBasis.Selection.TargetInstanceID,
	)
	if !found || targetEntry.Activation.Version != fixture.targetReport.Module.ExactVersion ||
		targetEntry.Activation.ArtifactDigest != fixture.targetReport.ArtifactDigest {
		t.Fatalf("approved replacement target = %+v, found=%v", targetEntry, found)
	}
	beforeProfile, found := fixture.upgradeBasis.Control.FindProfile(
		moduleApplyTestProfileID,
	)
	if !found || len(beforeProfile.Bindings) != len(profile.Bindings) {
		t.Fatalf(
			"replacement Profile shape before=%+v after=%+v",
			beforeProfile,
			profile,
		)
	}
	expectedBindings := append(
		[]controlcontract.BindingSpec(nil),
		beforeProfile.Bindings...,
	)
	portOrdinal := uint32(0)
	replacedAt := -1
	for index := range expectedBindings {
		if expectedBindings[index].Port != productionContextPort {
			continue
		}
		if portOrdinal == reviewResult.Review.PortBindingIndex {
			if expectedBindings[index].InstanceID != fixture.current.InstanceID {
				t.Fatalf("reviewed old Binding = %+v", expectedBindings[index])
			}
			expectedBindings[index].InstanceID = fixture.upgradeBasis.Selection.TargetInstanceID
			replacedAt = index
			break
		}
		portOrdinal++
	}
	if replacedAt < 0 || !reflect.DeepEqual(profile.Bindings, expectedBindings) {
		t.Fatalf(
			"replacement did not preserve exact Binding order at index %d\n got=%+v\nwant=%+v",
			replacedAt,
			profile.Bindings,
			expectedBindings,
		)
	}

	// Adjacent-current idempotency must use the exact canonical plan and final
	// artifact. It must not re-enter Source verification after the commit.
	if err := os.RemoveAll(fixture.sourceRoot); err != nil {
		t.Fatal(err)
	}
	var retryOutput bytes.Buffer
	if err := runModuleUpgradeApply(
		context.Background(),
		applyArgs,
		&retryOutput,
		ioDiscardModuleUpgradeApplyV1{},
	); err != nil {
		t.Fatalf("adjacent retry after Source removal: %v", err)
	}
	var retried moduleUpgradeApplyCommandResultV1
	decodeModuleOperatorOutputV1(t, retryOutput.Bytes(), &retried)
	if retried.Status != moduleApplyStatusAlreadyApplied ||
		retried.PlanDigest != applied.PlanDigest ||
		retried.PointerRevision != applied.PointerRevision ||
		retried.ControlSnapshotID != applied.ControlSnapshotID ||
		retried.CatalogGenerationID != applied.CatalogGenerationID {
		t.Fatalf("adjacent retry = %+v, applied=%+v", retried, applied)
	}

	// Once another exact publication advances the pointer, this approval is no
	// longer adjacent. It must fail closed instead of rebasing or replaying.
	post := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplySkillID,
		"u4-post-upgrade-publication",
	)
	postPlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(fixture.root, "post-upgrade-publication.json"),
		newEnabledDeclarativeModuleApplyPlanV1(
			t,
			post,
			applied.PointerRevision,
			1,
		),
	)
	postResult, err := runModuleApplyFixtureV1(
		fixture.databasePath,
		artifactRoot,
		postPlan,
		post.ArtifactDirectory,
		"",
	)
	if err != nil || postResult.Status != moduleApplyStatusApplied ||
		postResult.PointerRevision != applied.PointerRevision+1 {
		t.Fatalf("advance publication = %+v, %v", postResult, err)
	}
	var staleOutput bytes.Buffer
	err = runModuleUpgradeApply(
		context.Background(),
		applyArgs,
		&staleOutput,
		ioDiscardModuleUpgradeApplyV1{},
	)
	if err == nil ||
		err.Error() != "freeagent module-upgrade-apply: failed (POINTER_CONFLICT)" ||
		staleOutput.Len() != 0 {
		t.Fatalf("non-adjacent retry error=%v output=%q", err, staleOutput.String())
	}
}

type ioDiscardModuleUpgradeApplyV1 struct{}

func (ioDiscardModuleUpgradeApplyV1) Write(payload []byte) (int, error) {
	return len(payload), nil
}

func validModuleUpgradeApplyApprovalV1(t *testing.T) moduleUpgradeApplyApprovalV1 {
	t.Helper()
	_, config, err := moduleapi.NewContextBindingConfigV1(
		moduleapi.ContextBindingConfigV1{
			SchemaVersion: moduleapi.ContextBindingConfigSchemaV1,
			Placement:     moduleapi.ContextPlacementTrustedInstruction,
			AllowSummary:  false,
			AllowDrop:     false,
			Parameters:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	authority := modulehandler.DenyAllAuthorityCanonicalV1()
	candidateID := strings.Repeat("c", 64)
	decision, _, decisionID, err := moduleapi.NewModuleCandidateDecisionV1(
		moduleapi.ModuleCandidateDecisionV1{
			SchemaVersion:       moduleapi.ModuleCandidateDecisionSchemaVersionV1,
			CandidateID:         candidateID,
			Decision:            moduleapi.ModuleCandidateDecisionApproveV1,
			OperatorPrincipalID: "operator-1",
			Reason:              "approved for exact local publication",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	targetModule := moduleapi.Ref{ID: "vendor.context", Version: "build-2"}
	currentInstance := "context-current"
	targetInstance := "context-target"
	target := moduleupgrade.BindingTargetV1{
		Kind: moduleupgrade.BindingTargetProfileV1, ProfileID: "profile-1",
	}
	impact := moduleupgrade.BindingImpactV1{
		BindingTarget:       target,
		Port:                productionContextPort,
		PortBindingIndex:    1,
		CurrentInstanceID:   currentInstance,
		TargetInstanceID:    targetInstance,
		ConfigRef:           moduleUpgradeApplyContentRefV1(t, currentstore.ContentConfig, config),
		AuthorityCeilingRef: moduleUpgradeApplyContentRefV1(t, currentstore.ContentAuthorityCeiling, authority),
		StaticContextRefs:   []string{strings.Repeat("d", 64)},
		FailurePolicy:       moduleapi.FailureOptional,
	}
	return moduleUpgradeApplyApprovalV1{
		TenantID:           "tenant-1",
		ReviewID:           strings.Repeat("a", 64),
		DecisionID:         decisionID,
		Decision:           decision,
		ConfigCanonical:    bytes.Clone(config),
		AuthorityCanonical: bytes.Clone(authority),
		Review: moduleupgrade.ReviewV1{
			SchemaVersion:    moduleupgrade.ReviewSchemaVersionV1,
			CandidateID:      candidateID,
			ReviewKey:        strings.Repeat("e", 64),
			TenantID:         "tenant-1",
			BindingTarget:    target,
			Port:             productionContextPort,
			PortBindingIndex: 1,
			TargetInstanceID: targetInstance,
			PublishedBasis: controlcontract.PublishedBasis{
				TenantID: "tenant-1", PointerRevision: 7,
			},
			Current: moduleupgrade.CurrentExactV1{
				Activation: moduleapi.ActivatedModuleRef{
					ModuleID: "vendor.context", Version: "build-1",
					ArtifactDigest: strings.Repeat("f", 64),
					InstanceID:     currentInstance,
				},
			},
			Target: moduleupgrade.TargetEvidenceV1{
				Module:            targetModule,
				ArtifactDigest:    strings.Repeat("b", 64),
				ArtifactSizeBytes: 4096,
				Manifest: moduleupgrade.ManifestSummaryV1{
					Module: targetModule,
					Runtime: moduleupgrade.RuntimeSummaryV1{
						Mode:       moduleapi.RuntimeModeRequestDeclarative,
						Protocol:   moduleapi.RuntimeProtocolStaticV1,
						Entrypoint: "content/context.json",
					},
					Provides:             []moduleapi.PortRef{productionContextPort},
					Requires:             []moduleapi.PortRef{},
					RequestedPermissions: []moduleapi.Permission{},
				},
			},
			Handler: moduleupgrade.HandlerAssessmentV1{
				Status:          moduleupgrade.HandlerSupportedV1,
				Kind:            string(modulehandler.HandlerDeclarativeContextV1),
				ExecutionClass:  moduleapi.ExecutionDeclarative,
				AdapterIdentity: modulehandler.DeclarativeAdapterIdentityV1,
			},
			BindingImpacts: []moduleupgrade.BindingImpactV1{impact},
			RequiredGrants: []moduleupgrade.RequiredGrantV1{},
			Conclusion:     moduleupgrade.ConclusionWouldApplyV1,
		},
	}
}

func moduleUpgradeApplyContentRefV1(
	t *testing.T,
	kind currentstore.ContentKind,
	canonical []byte,
) string {
	t.Helper()
	digest, err := currentstore.ComputeContentDigest(kind, moduleApplyJSONMediaType, canonical)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
