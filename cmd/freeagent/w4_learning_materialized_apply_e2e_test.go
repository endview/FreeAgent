package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	w4LearningAppliedModuleID  = "freeagent.learning.skill.applied"
	w4LearningAppliedInstance  = "learning-skill-applied"
	w4LearningAppliedSkillText = "Approved learning is inert until an Operator explicitly binds its exact immutable module version."
	w4LearningReviewerAgentID  = "w4-learning-reviewer-agent"
	w4LearningReviewerProfile  = "w4-learning-reviewer-profile"
)

// TestW4ApprovedLearningMaterializedSkillApplyIsConsumedOnlyByNewRun closes
// the missing W4 product seam. An ordinary successful Run proposes a static
// Skill, an independent model-only Reviewer approves it, Learning
// materialization exports an inert exact artifact, and only an explicit
// Operator module-apply publishes it. The pre-existing Run stays frozen while
// a newly admitted Run consumes the exact approved bytes.
func TestW4ApprovedLearningMaterializedSkillApplyIsConsumedOnlyByNewRun(
	t *testing.T,
) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)

	// This ordinary successful Run is both the immutable pre-Apply baseline
	// and the proven proposer lineage for the Learning candidate.
	oldRunID := assertModuleApplyContextChatV1(
		t,
		databasePath,
		artifactRoot,
		"w4-learning-proposer",
		"propose one bounded static skill",
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyBasicContextText},
			{Role: moduleapi.ModelRoleUser, Content: "propose one bounded static skill"},
		},
	)

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open W4 materialization Store: %v", err)
	}
	oldBefore := loadW4LearningRunV1(t, ctx, store, oldRunID)
	if len(oldBefore.ModelDispatches) != 1 ||
		oldBefore.ModelDispatches[0].Attempt.State != corecontract.ModelAttemptSucceeded {
		_ = store.Close()
		t.Fatalf("proposer is not one successful ordinary Run: %+v", oldBefore)
	}

	_, draftCanonical, err := corecontract.NewStaticContextV1(
		corecontract.StaticContextV1{
			SchemaVersion: corecontract.StaticContextSchemaVersionV1,
			Text:          w4LearningAppliedSkillText,
		},
	)
	if err != nil {
		_ = store.Close()
		t.Fatalf("freeze W4 static Skill Draft: %v", err)
	}
	proposerAttempt := oldBefore.ModelDispatches[0].Attempt
	submitted, err := store.SubmitStaticSkillProposal(
		ctx,
		currentstore.SubmitStaticSkillProposalInput{
			Proposal: learningcontract.ProposalV1{
				SchemaVersion:          learningcontract.ProposalSchemaVersionV1,
				Kind:                   learningcontract.ProposalKindSkillV1,
				TenantID:               defaultTenantID,
				Workspace:              oldBefore.Manifest.Workspace,
				ProposerAgent:          oldBefore.Member.Agent,
				ProposerProfile:        oldBefore.Member.Profile,
				ProposerRunID:          oldBefore.RunID,
				ProposerManifestDigest: oldBefore.Manifest.ManifestDigest,
				ProposerMember:         oldBefore.Manifest.Members[0],
				ProposerResultRef:      proposerAttempt.ResultRef,
				Target: moduleapi.Ref{
					ID:      w4LearningAppliedModuleID,
					Version: moduleApplyTestVersion,
				},
			},
			DraftCanonical: draftCanonical,
			SourceEvidence: learningcontract.SourceEvidenceV1{
				Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
			},
		},
	)
	if err != nil || !submitted.Created ||
		submitted.Record.State != currentstore.LearningProposalSubmitted {
		_ = store.Close()
		t.Fatalf("submit W4 static Skill: result=%+v error=%v", submitted, err)
	}

	reviewer := publishW4LearningReviewerV1(t, ctx, store)
	approved := approveW4LearningProposalV1(
		t,
		ctx,
		store,
		submitted.Record,
		reviewer,
	)
	if approved.State != currentstore.LearningProposalApproved ||
		approved.Revision != 2 || approved.ReviewRunID == oldRunID ||
		approved.ReviewerAttemptID == "" {
		_ = store.Close()
		t.Fatalf("Learning Proposal was not independently approved: %+v", approved)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close approved Learning Store: %v", err)
	}

	// Approval alone remains inert. learning-materialize owns only the exact
	// Version and an Operator handoff; it cannot install, activate, bind, grant
	// Trust/authority, or advance the current Control/Catalog pointer.
	handoff := filepath.Join(root, "approved-skill-handoff")
	var materializeOutput bytes.Buffer
	if err := runLearningMaterialize(
		ctx,
		[]string{
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--tenant", defaultTenantID,
			"--proposal", approved.ProposalID,
			"--proposal-revision", "2",
			"--output-artifact", handoff,
		},
		&materializeOutput,
		&bytes.Buffer{},
	); err != nil {
		t.Fatalf("materialize approved Learning Version: %v", err)
	}
	var materialized learningMaterializeResultV1
	if err := json.Unmarshal(materializeOutput.Bytes(), &materialized); err != nil {
		t.Fatalf("decode Learning materialization result: %v", err)
	}
	if materialized.ProposalID != approved.ProposalID ||
		materialized.ProposalRevision != 2 ||
		materialized.VersionID == "" ||
		materialized.Version.Target.ID != w4LearningAppliedModuleID ||
		materialized.Version.Target.Version != moduleApplyTestVersion ||
		materialized.Module.ID != w4LearningAppliedModuleID ||
		materialized.Module.ExactVersion != moduleApplyTestVersion ||
		materialized.Module.ArtifactDigest != materialized.Version.ArtifactDigest ||
		!materialized.VersionCreated || !materialized.ExportCreated ||
		materialized.OperatorAction !=
			"AUTHOR_EXACT_MODULE_APPLY_PLAN_THEN_RUN_MODULE_DRY_RUN" {
		t.Fatalf("materialized Learning identity or handoff drifted: %+v", materialized)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(handoff, moduleapi.ArtifactManifestPath))
	if err != nil {
		t.Fatalf("read materialized manifest: %v", err)
	}
	manifest, manifestCanonical, err := moduleapi.ParseModuleManifestV1(manifestBytes)
	if err != nil || !bytes.Equal(manifestBytes, manifestCanonical) ||
		manifest.ID != materialized.Module.ID ||
		manifest.Version != materialized.Module.ExactVersion ||
		manifest.Runtime.Mode != moduleapi.RuntimeModeRequestDeclarative ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolStaticV1 ||
		len(manifest.RequestedPermissions) != 0 || len(manifest.Requires) != 0 {
		t.Fatalf("materialized manifest requested authority or drifted: %+v error=%v", manifest, err)
	}

	assertW4LearningStillInertV1(
		t,
		databasePath,
		artifactRoot,
		oldBefore,
		materialized,
		2,
	)

	fixture := moduleApplyDeclarativeFixtureV1{
		ModuleID:          materialized.Module.ID,
		InstanceID:        w4LearningAppliedInstance,
		ArtifactDirectory: handoff,
		ArtifactDigest:    materialized.Module.ArtifactDigest,
		ArtifactSizeBytes: materialized.Module.ArtifactSizeBytes,
	}
	// A stale Operator CAS cannot turn the approved Version into a partial or
	// implicit publication.
	stalePlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "apply-approved-skill-stale.json"),
		newNamedEnabledDeclarativeModuleApplyPlanV1(
			t,
			fixture,
			moduleApplyTestProfileID,
			1,
			0,
		),
	)
	if _, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		stalePlan,
		handoff,
		"",
	); err == nil || !strings.Contains(err.Error(), "POINTER_CONFLICT") {
		t.Fatalf("stale Catalog CAS error=%v", err)
	}
	assertW4LearningStillInertV1(
		t,
		databasePath,
		artifactRoot,
		oldBefore,
		materialized,
		2,
	)

	applyPlan := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "apply-approved-skill.json"),
		newNamedEnabledDeclarativeModuleApplyPlanV1(
			t,
			fixture,
			moduleApplyTestProfileID,
			2,
			0,
		),
	)
	applied, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		applyPlan,
		handoff,
		"",
	)
	if err != nil {
		t.Fatalf("Operator module-apply approved Learning Version: %v", err)
	}
	assertDeclarativeModuleApplyResultV1(
		t,
		applied,
		fixture,
		moduleApplyStatusApplied,
		moduleApplyEnabledV1,
		3,
	)
	if applied.Module == nil ||
		applied.Module.ArtifactDigest != materialized.Module.ArtifactDigest ||
		applied.Module.ID != materialized.Version.Target.ID ||
		applied.Module.ExactVersion != materialized.Version.Target.Version {
		t.Fatalf("module-apply did not publish exact Learning Version: %+v", applied)
	}

	assertW4LearningAppliedStateV1(
		t,
		databasePath,
		artifactRoot,
		oldBefore,
		materialized,
		applied,
		draftCanonical,
	)

	newRunID := assertModuleApplyContextChatV1(
		t,
		databasePath,
		artifactRoot,
		"w4-learning-new-run",
		"consume the approved skill",
		[]moduleapi.ModelMessageV1{
			{Role: moduleapi.ModelRoleSystem, Content: w4LearningAppliedSkillText},
			{Role: moduleapi.ModelRoleSystem, Content: moduleApplyBasicContextText},
			{Role: moduleapi.ModelRoleUser, Content: "consume the approved skill"},
		},
	)
	assertW4LearningNewRunVersionV1(
		t,
		databasePath,
		newRunID,
		materialized,
		applied,
	)
}

type w4LearningReviewerPublicationV1 struct {
	basis            controlcontract.PublishedBasis
	controlCanonical []byte
	catalogCanonical []byte
	modelParameters  json.RawMessage
}

func publishW4LearningReviewerV1(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
) w4LearningReviewerPublicationV1 {
	t.Helper()
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil {
		t.Fatalf("load pre-review Control/Catalog: %v", err)
	}
	base, found := control.FindProfile(moduleApplyTestProfileID)
	if !found {
		t.Fatal("Pure Chat Profile is absent before Learning review")
	}
	var modelBinding controlcontract.BindingSpec
	modelCount := 0
	for _, binding := range base.Bindings {
		if binding.Port == productionModelPort {
			modelBinding = binding
			modelCount++
		}
	}
	if modelCount != 1 {
		t.Fatalf("Pure Chat Profile model bindings=%d", modelCount)
	}
	configRecord, err := store.GetContent(ctx, modelBinding.ConfigRef)
	if err != nil {
		t.Fatalf("read Reviewer base Model config: %v", err)
	}
	modelConfig, err := moduleapi.RestoreModelBindingConfigV1(configRecord.CanonicalBytes)
	if err != nil {
		t.Fatalf("restore Reviewer base Model config: %v", err)
	}
	modelConfig.Parameters = json.RawMessage(`{"max_tokens":512}`)
	_, modelConfigCanonical, err := moduleapi.NewModelBindingConfigV1(modelConfig)
	if err != nil {
		t.Fatalf("freeze Reviewer Model config: %v", err)
	}
	modelBinding.ConfigRef = putW4LearningContentV1(
		t,
		ctx,
		store,
		currentstore.ContentConfig,
		modelConfigCanonical,
	)

	reviewerProfile := base
	reviewerProfile.Profile = corecontract.ProfileRef{
		ID:      w4LearningReviewerProfile,
		Version: "v1",
		Digest:  strings.Repeat("7", 64),
	}
	reviewerProfile.Bindings = []controlcontract.BindingSpec{modelBinding}
	control.SnapshotID = "control-w4-learning-reviewer"
	control.Revision++
	control.Digest = ""
	control.Agents = append(control.Agents, corecontract.AgentRef{
		ID:      w4LearningReviewerAgentID,
		Version: "v1",
		Digest:  strings.Repeat("8", 64),
	})
	control.Profiles = append(control.Profiles, reviewerProfile)
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze Learning Reviewer Control: %v", err)
	}
	catalog.GenerationID = "catalog-w4-learning-reviewer"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze Learning Reviewer Catalog: %v", err)
	}
	published, err := store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil || published.PointerRevision != 2 {
		t.Fatalf("publish Learning Reviewer basis=%+v error=%v", published, err)
	}
	return w4LearningReviewerPublicationV1{
		basis:            published,
		controlCanonical: controlCanonical,
		catalogCanonical: catalogCanonical,
		modelParameters:  bytes.Clone(modelConfig.Parameters),
	}
}

func approveW4LearningProposalV1(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	proposal currentstore.LearningProposalRecord,
	reviewer w4LearningReviewerPublicationV1,
) currentstore.LearningProposalRecord {
	t.Helper()
	_, reviewCanonical, _, err := learningcontract.NewReviewRequestV1(
		learningcontract.ReviewRequestV1{
			SchemaVersion:       learningcontract.ReviewRequestSchemaVersionV1,
			ProposalID:          proposal.ProposalID,
			SourceFingerprint:   proposal.Proposal.SourceFingerprint,
			ContentFingerprint:  proposal.Proposal.ContentFingerprint,
			DraftDigest:         proposal.Proposal.DraftDigest,
			OutputSchemaVersion: learningcontract.ReviewVerdictSchemaVersionV1,
			MaxOutputTokens:     512,
			ReviewPolicy:        learningcontract.ReviewPolicyProposalGateV1,
			Instructions:        learningcontract.ReviewInstructionsV1,
			ProposalCanonical:   bytes.Clone(proposal.ProposalCanonical),
			DraftCanonical:      bytes.Clone(proposal.DraftCanonical),
		},
	)
	if err != nil {
		t.Fatalf("freeze Learning Review request: %v", err)
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(corecontract.TaskInputV1{
		SchemaVersion: corecontract.TaskInputSchemaVersionV1,
		Text:          string(reviewCanonical),
	})
	if err != nil {
		t.Fatalf("freeze Learning Review task: %v", err)
	}
	task := newW4LearningContentV1(t, currentstore.ContentTaskInput, taskCanonical)
	deadline := time.Date(2099, time.January, 2, 3, 4, 5, 0, time.UTC)
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:          defaultTenantID,
			AdmissionKey:      "w4-learning-review-admission",
			PrincipalID:       "w4-learning-review-principal",
			WorkspaceID:       proposal.Proposal.Workspace.ID,
			AgentID:           w4LearningReviewerAgentID,
			ProfileID:         w4LearningReviewerProfile,
			TaskInputRef:      task.Digest,
			RequestedPorts:    []moduleapi.PortRef{productionModelPort},
			Deadline:          deadline,
			CancellationScope: "run",
			ExplicitLimits:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		t.Fatalf("freeze Learning Reviewer intent: %v", err)
	}
	compiled, err := (assemblycompiler.Compiler{}).Compile(
		ctx,
		assemblycompiler.CompileInput{
			IntentCanonical:  intentCanonical,
			IntentDigest:     intentDigest,
			RunID:            "run-w4-learning-reviewer",
			MemberID:         "member-w4-learning-reviewer",
			RecoveryRootRef:  "recovery/run-w4-learning-reviewer",
			PublishedBasis:   reviewer.basis,
			ControlCanonical: reviewer.controlCanonical,
			CatalogCanonical: reviewer.catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("compile Learning Reviewer Run: %v", err)
	}
	admitted, err := store.CommitLearningReviewAdmission(
		ctx,
		currentstore.CommitLearningReviewAdmissionInput{
			TenantID:                 defaultTenantID,
			ProposalID:               proposal.ProposalID,
			ExpectedProposalRevision: 0,
			Run: currentstore.CommitRunAdmissionInput{
				PublishedBasis:          reviewer.basis,
				IntentCanonical:         intentCanonical,
				IntentDigest:            intentDigest,
				MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
				RunManifestCanonical:    compiled.RunManifestCanonical,
				Contents:                []currentstore.ContentInput{task},
			},
		},
	)
	if err != nil || !admitted.Created ||
		admitted.Proposal.State != currentstore.LearningProposalReviewPending {
		t.Fatalf("admit independent Learning Reviewer: result=%+v error=%v", admitted, err)
	}

	lease, err := store.AcquireRunLease(ctx, currentstore.AcquireRunLeaseInput{
		RunID:                 admitted.Run.RunID,
		OwnerID:               "w4-learning-review-worker",
		ExpectedRunRevision:   0,
		ExpectedFrameRevision: 0,
		TTL:                   time.Hour,
	})
	if err != nil {
		t.Fatalf("acquire Learning Reviewer lease: %v", err)
	}
	_, requestCanonical, err := moduleapi.NewModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages: []moduleapi.ModelMessageV1{{
				Role: moduleapi.ModelRoleUser, Content: string(reviewCanonical),
			}},
			Parameters: bytes.Clone(reviewer.modelParameters),
		},
	)
	if err != nil {
		t.Fatalf("freeze Learning Reviewer model request: %v", err)
	}
	begin, err := store.BeginModelDispatch(ctx, currentstore.BeginModelDispatchInput{
		Lease:            lease,
		AttemptID:        "attempt-w4-learning-reviewer",
		LogicalStepID:    corecontract.PureChatModelLogicalStepIDV1,
		RequestCanonical: requestCanonical,
		Deadline:         deadline.Add(-time.Minute),
	})
	if err != nil || !begin.Created || !begin.InvokeAllowed {
		t.Fatalf("begin Learning Reviewer model attempt: result=%+v error=%v", begin, err)
	}
	_, verdictCanonical, _, err := learningcontract.NewReviewVerdictV1(
		learningcontract.ReviewVerdictV1{
			SchemaVersion:      learningcontract.ReviewVerdictSchemaVersionV1,
			ProposalID:         proposal.ProposalID,
			SourceFingerprint:  proposal.Proposal.SourceFingerprint,
			ContentFingerprint: proposal.Proposal.ContentFingerprint,
			Decision:           learningcontract.ReviewDecisionApproveV1,
			IssueCodes:         []learningcontract.ReviewIssueCodeV1{},
			BoundedReason:      "The exact bounded static Skill is valid and carries no authority grant.",
		},
	)
	if err != nil {
		t.Fatalf("freeze Learning Reviewer verdict: %v", err)
	}
	const providerRequestID = "provider-w4-learning-review"
	_, outputCanonical, err := moduleapi.NewModelGenerateOutputV1(
		moduleapi.ModelGenerateOutputV1{
			SchemaVersion:     moduleapi.ModelGenerateOutputSchemaV1,
			AssistantText:     string(verdictCanonical),
			ProviderRequestID: providerRequestID,
		},
	)
	if err != nil {
		t.Fatalf("freeze Learning Reviewer output: %v", err)
	}
	usageCanonical := newW4LearningUsageV1(t, providerRequestID)
	outcome, err := store.CommitModelDispatchOutcome(
		ctx,
		currentstore.CommitModelDispatchOutcomeInput{
			Lease:                   begin.Lease,
			AttemptID:               begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
			ProviderRequestID:       providerRequestID,
		},
	)
	if err != nil || !outcome.Applied {
		t.Fatalf("commit Learning Reviewer outcome: result=%+v error=%v", outcome, err)
	}
	finalized, err := store.FinalizeLearningReview(
		ctx,
		currentstore.FinalizeLearningReviewInput{
			TenantID:                 defaultTenantID,
			ProposalID:               proposal.ProposalID,
			ExpectedProposalRevision: 1,
		},
	)
	if err != nil || !finalized.Applied {
		t.Fatalf("finalize Learning Review: result=%+v error=%v", finalized, err)
	}
	if err := store.ReleaseRunLease(ctx, outcome.Lease); err != nil {
		t.Fatalf("release Learning Reviewer lease: %v", err)
	}
	return finalized.Proposal
}

func assertW4LearningStillInertV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	oldBefore currentstore.RunForLoop,
	materialized learningMaterializeResultV1,
	wantPointer uint64,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open inert Learning Store: %v", err)
	}
	defer store.Close()
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil || basis.PointerRevision != wantPointer {
		t.Fatalf("inert Learning pointer=%+v error=%v", basis, err)
	}
	if _, err := store.GetModuleInstallationByIdentity(
		ctx,
		materialized.Module.ID,
		materialized.Module.ExactVersion,
	); !errors.Is(err, currentstore.ErrModuleInstallationNotFound) {
		t.Fatalf("materialization implicitly installed module: %v", err)
	}
	profile, found := control.FindProfile(moduleApplyTestProfileID)
	if !found {
		t.Fatal("Pure Chat Profile disappeared while Learning Version was inert")
	}
	for _, binding := range profile.Bindings {
		if binding.InstanceID == w4LearningAppliedInstance {
			t.Fatalf("materialization implicitly bound module: %+v", binding)
		}
	}
	if _, found := catalog.FindInstance(w4LearningAppliedInstance); found {
		t.Fatal("materialization implicitly activated module in current Catalog")
	}
	version, err := store.GetLearningMaterializedVersion(
		ctx,
		defaultTenantID,
		materialized.ProposalID,
	)
	if err != nil || version.VersionID != materialized.VersionID ||
		version.ArtifactDigest != materialized.Module.ArtifactDigest ||
		version.Version.Target.ID != materialized.Module.ID ||
		version.Version.Target.Version != materialized.Module.ExactVersion {
		t.Fatalf("materialized Version closure=%+v error=%v", version, err)
	}
	oldAfter := loadW4LearningRunV1(t, ctx, store, oldBefore.RunID)
	if !bytes.Equal(oldAfter.MemberCanonical, oldBefore.MemberCanonical) ||
		oldAfter.Member.MemberSnapshotDigest != oldBefore.Member.MemberSnapshotDigest {
		t.Fatal("pre-existing Run changed while Learning Version was inert")
	}
	if _, err := os.Lstat(filepath.Join(
		artifactRoot,
		materialized.Module.ArtifactDigest,
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("materialization published into active artifact root: %v", err)
	}
}

func assertW4LearningAppliedStateV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	oldBefore currentstore.RunForLoop,
	materialized learningMaterializeResultV1,
	applied moduleApplyResultV1,
	draftCanonical []byte,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open applied Learning Store: %v", err)
	}
	defer store.Close()
	basis, control, catalog, err := store.LoadPublishedBasis(ctx, defaultTenantID)
	if err != nil || basis.PointerRevision != 3 ||
		basis.Control.SnapshotID != applied.ControlSnapshotID ||
		basis.Catalog.GenerationID != applied.CatalogGenerationID {
		t.Fatalf("applied Learning basis=%+v error=%v", basis, err)
	}
	installation, err := store.GetModuleInstallationByIdentity(
		ctx,
		materialized.Module.ID,
		materialized.Module.ExactVersion,
	)
	if err != nil || installation.ArtifactDigest != materialized.Module.ArtifactDigest {
		t.Fatalf("exact Learning Installation=%+v error=%v", installation, err)
	}
	entry, found := catalog.FindInstance(w4LearningAppliedInstance)
	if !found || entry.Activation.ModuleID != materialized.Module.ID ||
		entry.Activation.Version != materialized.Module.ExactVersion ||
		entry.Activation.ArtifactDigest != materialized.Module.ArtifactDigest ||
		entry.Activation.ExecutionClass != moduleapi.ExecutionDeclarative ||
		entry.Activation.AdapterIdentity != declarativeAdapterID {
		t.Fatalf("local Trust/Catalog assignment=%+v found=%v", entry, found)
	}
	profile, found := control.FindProfile(moduleApplyTestProfileID)
	if !found {
		t.Fatal("Pure Chat Profile absent after Operator Apply")
	}
	var appliedBinding *controlcontract.BindingSpec
	for index := range profile.Bindings {
		if profile.Bindings[index].InstanceID == w4LearningAppliedInstance {
			copy := profile.Bindings[index]
			appliedBinding = &copy
			break
		}
	}
	if appliedBinding == nil || appliedBinding.Port != productionContextPort ||
		appliedBinding.FailurePolicy != moduleapi.FailureRequired ||
		len(appliedBinding.StaticContextRefs) != 1 {
		t.Fatalf("Operator-owned Learning Binding=%+v", appliedBinding)
	}
	config, err := store.GetContent(ctx, appliedBinding.ConfigRef)
	if err != nil {
		t.Fatalf("read Learning Binding config: %v", err)
	}
	contextConfig, err := moduleapi.RestoreContextBindingConfigV1(config.CanonicalBytes)
	if err != nil ||
		contextConfig.Placement != moduleapi.ContextPlacementTrustedInstruction ||
		contextConfig.AllowSummary || contextConfig.AllowDrop ||
		string(contextConfig.Parameters) != "{}" {
		t.Fatalf("Learning Binding config=%+v error=%v", contextConfig, err)
	}
	authority, err := store.GetContent(ctx, appliedBinding.AuthorityCeilingRef)
	if err != nil || !bytes.Equal(
		authority.CanonicalBytes,
		moduleApplyDenyAllAuthorityCanonicalV1,
	) {
		t.Fatalf("Learning Binding authority is not exact deny-all: %v", err)
	}
	wantStaticRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentStaticContext,
		"application/json",
		draftCanonical,
	)
	if err != nil || appliedBinding.StaticContextRefs[0] != wantStaticRef {
		t.Fatalf("Learning static ref=%v want=%s error=%v", appliedBinding.StaticContextRefs, wantStaticRef, err)
	}
	oldAfter := loadW4LearningRunV1(t, ctx, store, oldBefore.RunID)
	if !bytes.Equal(oldAfter.MemberCanonical, oldBefore.MemberCanonical) ||
		oldAfter.Member.Catalog.ID == applied.CatalogGenerationID {
		t.Fatal("Operator Apply rewrote or rebound the pre-existing Run")
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		filepath.Join(artifactRoot, materialized.Module.ArtifactDigest),
		moduleapi.ArtifactMetadataPaths{},
		materialized.Module.ArtifactDigest,
		materialized.Module.ArtifactSizeBytes,
	); err != nil {
		t.Fatalf("verify active exact Learning artifact: %v", err)
	}
}

func assertW4LearningNewRunVersionV1(
	t *testing.T,
	databasePath string,
	runID string,
	materialized learningMaterializeResultV1,
	applied moduleApplyResultV1,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("open new Learning Run Store: %v", err)
	}
	defer store.Close()
	run := loadW4LearningRunV1(t, ctx, store, runID)
	if run.Member.Catalog.ID != applied.CatalogGenerationID {
		t.Fatalf("new Run froze Catalog %q want %q", run.Member.Catalog.ID, applied.CatalogGenerationID)
	}
	for _, plan := range run.Member.PortPlans {
		if plan.Port != productionContextPort {
			continue
		}
		if len(plan.Bindings) < 1 {
			break
		}
		provider := plan.Bindings[0].Provider
		if provider.InstanceID != w4LearningAppliedInstance ||
			provider.ModuleID != materialized.Module.ID ||
			provider.Version != materialized.Module.ExactVersion ||
			provider.ArtifactDigest != materialized.Module.ArtifactDigest ||
			provider.ExecutionClass != moduleapi.ExecutionDeclarative ||
			provider.AdapterIdentity != declarativeAdapterID {
			t.Fatalf("new Run did not freeze exact approved Learning provider: %+v", provider)
		}
		return
	}
	t.Fatal("new Run has no applied Learning context Binding")
}

func loadW4LearningRunV1(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	runID string,
) currentstore.RunForLoop {
	t.Helper()
	lease, err := store.AcquireCurrentRunLease(
		ctx,
		currentstore.AcquireCurrentRunLeaseInput{
			RunID:   runID,
			OwnerID: "w4-learning-run-inspector",
			TTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("acquire W4 Learning Run read lease: %v", err)
	}
	run, loadErr := store.LoadRunForLoop(ctx, lease)
	releaseErr := store.ReleaseRunLease(ctx, lease)
	if loadErr != nil || releaseErr != nil {
		t.Fatalf("load/release W4 Learning Run: load=%v release=%v", loadErr, releaseErr)
	}
	return run
}

func putW4LearningContentV1(
	t *testing.T,
	ctx context.Context,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	canonical []byte,
) string {
	t.Helper()
	content := newW4LearningContentV1(t, kind, canonical)
	if _, err := store.PutContent(ctx, content); err != nil {
		t.Fatalf("put W4 Learning %s: %v", kind, err)
	}
	return content.Digest
}

func newW4LearningContentV1(
	t *testing.T,
	kind currentstore.ContentKind,
	canonical []byte,
) currentstore.ContentInput {
	t.Helper()
	const mediaType = "application/json"
	digest, err := currentstore.ComputeContentDigest(kind, mediaType, canonical)
	if err != nil {
		t.Fatalf("digest W4 Learning %s: %v", kind, err)
	}
	return currentstore.ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      mediaType,
		CanonicalBytes: bytes.Clone(canonical),
	}
}

func newW4LearningUsageV1(t *testing.T, providerRequestID string) []byte {
	t.Helper()
	input := uint64(24)
	cached := uint64(0)
	uncached := uint64(24)
	output := uint64(12)
	reasoning := uint64(0)
	_, canonical, err := moduleapi.NewModelUsageReceiptV1(
		moduleapi.ModelUsageReceiptV1{
			SchemaVersion:       moduleapi.ModelUsageReceiptSchemaV1,
			InputTokens:         &input,
			CachedInputTokens:   &cached,
			UncachedInputTokens: &uncached,
			OutputTokens:        &output,
			ReasoningTokens:     &reasoning,
			RawReceipt: []byte(
				`{"id":"` + providerRequestID + `","status":"completed"}`,
			),
		},
	)
	if err != nil {
		t.Fatalf("freeze W4 Learning Usage receipt: %v", err)
	}
	return canonical
}
