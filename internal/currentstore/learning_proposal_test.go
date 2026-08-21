package currentstore

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestSubmitKnowledgeProposalCreatesExactImmutableRecordAndRetries(
	t *testing.T,
) {
	fixture := newLearningProposalStoreFixture(t)
	input := fixture.input(
		t,
		"1.0.0",
		"Governed Knowledge is submitted without activation authority.",
		learningcontract.SourceEvidenceV1{
			Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
		},
		fixture.manifest.TenantID,
		fixture.member.Workspace.ID,
	)
	pristineDraft := bytes.Clone(input.DraftCanonical)
	before := snapshotLearningOutsideScope(t, fixture.store)
	first, err := fixture.store.SubmitKnowledgeProposal(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("SubmitKnowledgeProposal: %v", err)
	}
	if !first.Created ||
		first.Record.State != LearningProposalSubmitted ||
		first.Record.Revision != 0 ||
		first.Record.ProposerAttemptID != fixture.attemptID ||
		!first.Record.CreatedAt.Equal(first.Record.UpdatedAt) {
		t.Fatalf("first result=%+v", first)
	}
	if _, err := learningcontract.RestoreProposalV1(
		first.Record.ProposalCanonical,
		first.Record.DraftCanonical,
		first.Record.ProposalID,
	); err != nil {
		t.Fatalf("restore stored Proposal: %v", err)
	}
	after := snapshotLearningOutsideScope(t, fixture.store)
	if after.LearningProposals != before.LearningProposals+1 {
		t.Fatalf("Learning rows before=%+v after=%+v", before, after)
	}
	after.LearningProposals = before.LearningProposals
	if after != before {
		t.Fatalf("Proposal submission changed Runtime state\nbefore=%+v\nafter=%+v", before, after)
	}

	retryInput := input
	retryInput.DraftCanonical = bytes.Clone(pristineDraft)
	retry, err := fixture.store.SubmitKnowledgeProposal(
		context.Background(),
		retryInput,
	)
	if err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	if retry.Created ||
		retry.Record.ProposalID != first.Record.ProposalID ||
		!retry.Record.CreatedAt.Equal(first.Record.CreatedAt) ||
		!bytes.Equal(retry.Record.ProposalCanonical, first.Record.ProposalCanonical) ||
		!bytes.Equal(retry.Record.DraftCanonical, first.Record.DraftCanonical) {
		t.Fatalf("retry=%+v first=%+v", retry, first)
	}

	input.DraftCanonical[0] = 'x'
	first.Record.ProposalCanonical[0] = 'x'
	first.Record.DraftCanonical[0] = 'x'
	stored, err := fixture.store.GetKnowledgeProposal(
		context.Background(),
		fixture.manifest.TenantID,
		retry.Record.ProposalID,
	)
	if err != nil {
		t.Fatalf("GetKnowledgeProposal: %v", err)
	}
	if stored.ProposalCanonical[0] != '{' || stored.DraftCanonical[0] != '{' {
		t.Fatal("caller mutation aliased persisted Learning bytes")
	}
	if _, err := fixture.store.GetKnowledgeProposal(
		context.Background(),
		"another-tenant",
		stored.ProposalID,
	); !errors.Is(err, ErrLearningProposalNotFound) {
		t.Fatalf("cross-tenant Get error=%v", err)
	}
}

func TestSubmitKnowledgeProposalRejectsIndependentDuplicateAxes(t *testing.T) {
	t.Run("source", func(t *testing.T) {
		fixture := newLearningProposalStoreFixture(t)
		evidence := learningcontract.SourceEvidenceV1{
			Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
		}
		first := fixture.input(
			t, "1.0.0", "First semantic body.", evidence,
			fixture.manifest.TenantID, fixture.member.Workspace.ID,
		)
		second := fixture.input(
			t, "2.0.0", "Changed semantic body.", evidence,
			fixture.manifest.TenantID, fixture.member.Workspace.ID,
		)
		if _, err := fixture.store.SubmitKnowledgeProposal(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.SubmitKnowledgeProposal(
			context.Background(),
			second,
		); !errors.Is(err, ErrLearningSourceDuplicate) {
			t.Fatalf("source duplicate error=%v", err)
		}
		assertLearningProposalCount(t, fixture.store, 1)
	})

	t.Run("content", func(t *testing.T) {
		fixture := newLearningProposalStoreFixture(t)
		first := fixture.input(
			t,
			"1.0.0",
			"Semantically identical imported content.",
			localLearningEvidence("origin-a", "revision-a"),
			fixture.manifest.TenantID,
			fixture.member.Workspace.ID,
		)
		second := fixture.input(
			t,
			"2.0.0",
			"Semantically identical imported content.",
			localLearningEvidence("origin-b", "revision-b"),
			fixture.manifest.TenantID,
			fixture.member.Workspace.ID,
		)
		if _, err := fixture.store.SubmitKnowledgeProposal(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.SubmitKnowledgeProposal(
			context.Background(),
			second,
		); !errors.Is(err, ErrLearningContentDuplicate) {
			t.Fatalf("content duplicate error=%v", err)
		}
		assertLearningProposalCount(t, fixture.store, 1)
	})

	t.Run("target", func(t *testing.T) {
		fixture := newLearningProposalStoreFixture(t)
		first := fixture.input(
			t,
			"1.0.0",
			"First body for an immutable target.",
			localLearningEvidence("origin-a", "revision-a"),
			fixture.manifest.TenantID,
			fixture.member.Workspace.ID,
		)
		second := fixture.input(
			t,
			"1.0.0",
			"Conflicting body for the same immutable target.",
			localLearningEvidence("origin-b", "revision-b"),
			fixture.manifest.TenantID,
			fixture.member.Workspace.ID,
		)
		if _, err := fixture.store.SubmitKnowledgeProposal(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.SubmitKnowledgeProposal(
			context.Background(),
			second,
		); !errors.Is(err, ErrLearningTargetDuplicate) {
			t.Fatalf("target duplicate error=%v", err)
		}
		assertLearningProposalCount(t, fixture.store, 1)
	})
}

func TestSubmitKnowledgeProposalConcurrentRetryAndContentCollision(
	t *testing.T,
) {
	t.Run("exact retry", func(t *testing.T) {
		fixture := newLearningProposalStoreFixture(t)
		input := fixture.input(
			t,
			"1.0.0",
			"One exact concurrent Proposal.",
			localLearningEvidence("origin", "revision"),
			fixture.manifest.TenantID,
			fixture.member.Workspace.ID,
		)
		results := runConcurrentLearningSubmissions(t, fixture.store, []SubmitKnowledgeProposalInput{
			input, input, input, input, input, input, input, input,
		})
		created := 0
		var proposalID string
		for _, result := range results {
			if result.err != nil {
				t.Fatalf("concurrent exact retry: %v", result.err)
			}
			if result.result.Created {
				created++
			}
			if proposalID == "" {
				proposalID = result.result.Record.ProposalID
			} else if proposalID != result.result.Record.ProposalID {
				t.Fatal("exact retries returned different ProposalIDs")
			}
		}
		if created != 1 {
			t.Fatalf("created=%d want 1", created)
		}
		assertLearningProposalCount(t, fixture.store, 1)
	})

	t.Run("content collision", func(t *testing.T) {
		fixture := newLearningProposalStoreFixture(t)
		first := fixture.input(
			t, "1.0.0", "Concurrent semantic body.",
			localLearningEvidence("origin-a", "revision-a"),
			fixture.manifest.TenantID, fixture.member.Workspace.ID,
		)
		second := fixture.input(
			t, "2.0.0", "Concurrent semantic body.",
			localLearningEvidence("origin-b", "revision-b"),
			fixture.manifest.TenantID, fixture.member.Workspace.ID,
		)
		results := runConcurrentLearningSubmissions(
			t,
			fixture.store,
			[]SubmitKnowledgeProposalInput{first, second},
		)
		created, duplicate := 0, 0
		for _, result := range results {
			switch {
			case result.err == nil && result.result.Created:
				created++
			case errors.Is(result.err, ErrLearningContentDuplicate):
				duplicate++
			default:
				t.Fatalf("unexpected concurrent result=%+v", result)
			}
		}
		if created != 1 || duplicate != 1 {
			t.Fatalf("created=%d duplicate=%d", created, duplicate)
		}
		assertLearningProposalCount(t, fixture.store, 1)
	})
}

func TestSubmitKnowledgeProposalRejectsUnprovenLineageAndUnsupportedKind(
	t *testing.T,
) {
	fixture := newLearningProposalStoreFixture(t)
	base := fixture.input(
		t,
		"1.0.0",
		"Lineage must be proven before a Proposal is stored.",
		learningcontract.SourceEvidenceV1{
			Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
		},
		fixture.manifest.TenantID,
		fixture.member.Workspace.ID,
	)
	cases := []struct {
		name   string
		mutate func(*SubmitKnowledgeProposalInput)
	}{
		{"tenant", func(input *SubmitKnowledgeProposalInput) {
			input.Proposal.TenantID = "another-tenant"
			input.DraftCanonical = learningKnowledgeDraft(
				t, input.Proposal.Target.ID, input.Proposal.Target.Version,
				"Lineage must be proven before a Proposal is stored.",
				"another-tenant", fixture.member.Workspace.ID,
			)
		}},
		{"workspace", func(input *SubmitKnowledgeProposalInput) {
			input.Proposal.Workspace.ID = "another-workspace"
		}},
		{"agent", func(input *SubmitKnowledgeProposalInput) {
			input.Proposal.ProposerAgent.Digest = learningTestDigest("wrong-agent")
		}},
		{"profile", func(input *SubmitKnowledgeProposalInput) {
			input.Proposal.ProposerProfile.Digest = learningTestDigest("wrong-profile")
		}},
		{"manifest", func(input *SubmitKnowledgeProposalInput) {
			input.Proposal.ProposerManifestDigest = learningTestDigest("wrong-manifest")
		}},
		{"member", func(input *SubmitKnowledgeProposalInput) {
			input.Proposal.ProposerMember.Digest = learningTestDigest("wrong-member")
		}},
		{"result", func(input *SubmitKnowledgeProposalInput) {
			input.Proposal.ProposerResultRef = learningTestDigest("wrong-result")
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			input := cloneLearningSubmitInput(base)
			test.mutate(&input)
			if _, err := fixture.store.SubmitKnowledgeProposal(
				context.Background(),
				input,
			); !errors.Is(err, ErrLearningProposalLineage) {
				t.Fatalf("lineage error=%v", err)
			}
			assertLearningProposalCount(t, fixture.store, 0)
		})
	}

	_, skillDraft, err := corecontract.NewStaticContextV1(corecontract.StaticContextV1{
		SchemaVersion: corecontract.StaticContextSchemaVersionV1,
		Text:          "A static Skill remains a Draft outside W4-L1A.",
	})
	if err != nil {
		t.Fatal(err)
	}
	skill := base
	skill.Proposal.Kind = learningcontract.ProposalKindSkillV1
	skill.Proposal.Target = moduleapi.Ref{
		ID: "freeagent.test.skill.learning", Version: "1.0.0",
	}
	skill.DraftCanonical = skillDraft
	if _, err := fixture.store.SubmitKnowledgeProposal(
		context.Background(),
		skill,
	); !errors.Is(err, ErrInvalidLearningProposal) {
		t.Fatalf("Skill error=%v", err)
	}
	assertLearningProposalCount(t, fixture.store, 0)

	if _, err := fixture.store.SubmitKnowledgeProposal(nil, base); !errors.Is(
		err,
		ErrInvalidLearningProposal,
	) {
		t.Fatalf("nil context error=%v", err)
	}
}

func TestSubmitKnowledgeProposalRejectsNonTerminalFailedAndUnknownProposerRuns(
	t *testing.T,
) {
	for _, state := range []corecontract.ModelAttemptState{
		corecontract.ModelAttemptPending,
		corecontract.ModelAttemptFailed,
		corecontract.ModelAttemptUnknown,
	} {
		state := state
		t.Run(string(state), func(t *testing.T) {
			fixture := newLearningProposalStoreFixtureWithAttemptState(t, state)
			input := fixture.input(
				t,
				"1.0.0",
				"Only a terminal successful Model result may propose Knowledge.",
				learningcontract.SourceEvidenceV1{
					Mechanism: learningcontract.SourceMechanismAgentGeneratedV1,
				},
				fixture.manifest.TenantID,
				fixture.member.Workspace.ID,
			)
			before := snapshotLearningOutsideScope(t, fixture.store)
			if _, err := fixture.store.SubmitKnowledgeProposal(
				context.Background(),
				input,
			); !errors.Is(err, ErrLearningProposalLineage) {
				t.Fatalf("%s proposer error=%v", state, err)
			}
			after := snapshotLearningOutsideScope(t, fixture.store)
			if after != before {
				t.Fatalf(
					"%s lineage rejection changed Store state\nbefore=%+v\nafter=%+v",
					state,
					before,
					after,
				)
			}
		})
	}
}

func TestSubmitKnowledgeProposalRejectsClosedStore(t *testing.T) {
	fixture := newLearningProposalStoreFixture(t)
	input := fixture.input(
		t,
		"1.0.0",
		"A closed Store cannot accept a Learning Proposal.",
		localLearningEvidence("closed-origin", "closed-revision"),
		fixture.manifest.TenantID,
		fixture.member.Workspace.ID,
	)
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := fixture.store.SubmitKnowledgeProposal(
		context.Background(),
		input,
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed Store error=%v", err)
	}
}

func TestSubmitKnowledgeProposalWildcardScopeRemainsAuthorityRequestOnly(
	t *testing.T,
) {
	fixture := newLearningProposalStoreFixture(t)
	input := fixture.input(
		t,
		"1.0.0",
		"Tenant-local wildcard visibility remains an unapproved request.",
		localLearningEvidence("wildcard-origin", "wildcard-revision"),
		fixture.manifest.TenantID,
		"*",
	)
	before := snapshotLearningOutsideScope(t, fixture.store)
	result, err := fixture.store.SubmitKnowledgeProposal(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("Submit wildcard Proposal: %v", err)
	}
	if !result.Created || result.Record.State != LearningProposalSubmitted {
		t.Fatalf("wildcard Proposal result=%+v", result)
	}
	source, _, err := moduleapi.RestoreKnowledgeSourceV1(
		result.Record.DraftCanonical,
	)
	if err != nil {
		t.Fatalf("restore wildcard draft: %v", err)
	}
	if len(source.Chunks) != 1 || len(source.Chunks[0].VisibleTo) != 1 {
		t.Fatalf("wildcard draft scope=%+v", source.Chunks)
	}
	rule := source.Chunks[0].VisibleTo[0]
	if rule.TenantID != fixture.manifest.TenantID ||
		rule.WorkspaceID != "*" || rule.AgentID != "*" ||
		rule.TaskInputRef != "*" {
		t.Fatalf("wildcard request rule=%+v", rule)
	}
	after := snapshotLearningOutsideScope(t, fixture.store)
	if after.LearningProposals != before.LearningProposals+1 {
		t.Fatalf("Learning rows before=%+v after=%+v", before, after)
	}
	after.LearningProposals = before.LearningProposals
	if after != before {
		t.Fatalf(
			"wildcard request granted or changed Runtime Authority\nbefore=%+v\nafter=%+v",
			before,
			after,
		)
	}
}

func TestSubmitKnowledgeProposalDeduplicationIsTenantScoped(t *testing.T) {
	firstTenant := newLearningProposalStoreFixture(t)
	secondTenant := newLearningProposalStoreFixtureForTenant(
		t,
		firstTenant,
		"tenant-learning-second",
	)
	evidence := localLearningEvidence("shared-origin", "shared-revision")
	firstInput := firstTenant.input(
		t,
		"1.0.0",
		"The same source may be governed independently by another Tenant.",
		evidence,
		firstTenant.manifest.TenantID,
		firstTenant.member.Workspace.ID,
	)
	secondInput := secondTenant.input(
		t,
		"1.0.0",
		"The same source may be governed independently by another Tenant.",
		evidence,
		secondTenant.manifest.TenantID,
		secondTenant.member.Workspace.ID,
	)
	first, err := firstTenant.store.SubmitKnowledgeProposal(
		context.Background(),
		firstInput,
	)
	if err != nil {
		t.Fatalf("first Tenant submission: %v", err)
	}
	second, err := secondTenant.store.SubmitKnowledgeProposal(
		context.Background(),
		secondInput,
	)
	if err != nil {
		t.Fatalf("second Tenant submission: %v", err)
	}
	if !first.Created || !second.Created ||
		first.Record.ProposalID == second.Record.ProposalID {
		t.Fatalf("tenant-scoped results first=%+v second=%+v", first, second)
	}
	if first.Record.Proposal.SourceFingerprint !=
		second.Record.Proposal.SourceFingerprint {
		t.Fatal("identical imported source did not retain one source fingerprint")
	}
	if first.Record.Proposal.ContentFingerprint ==
		second.Record.Proposal.ContentFingerprint {
		t.Fatal("tenant visibility unexpectedly disappeared from content fingerprint")
	}
	assertLearningProposalCount(t, firstTenant.store, 2)
}

type learningProposalStoreFixture struct {
	store     *Store
	admission *admissionCommitFixture
	manifest  corecontract.RunManifest
	member    corecontract.MemberExecutionSnapshot
	attemptID string
	resultRef string
}

func newLearningProposalStoreFixture(t *testing.T) learningProposalStoreFixture {
	return newLearningProposalStoreFixtureWithAttemptState(
		t,
		corecontract.ModelAttemptSucceeded,
	)
}

func newLearningProposalStoreFixtureWithAttemptState(
	t *testing.T,
	state corecontract.ModelAttemptState,
) learningProposalStoreFixture {
	t.Helper()
	fixture, _, beginInput := newModelDispatchFixture(t)
	begin, err := fixture.store.BeginModelDispatch(context.Background(), beginInput)
	if err != nil {
		t.Fatal(err)
	}
	resultRef := learningTestDigest("missing-result-" + string(state))
	switch state {
	case corecontract.ModelAttemptPending:
		// The persisted PENDING Attempt deliberately leaves the Run non-terminal.
	case corecontract.ModelAttemptSucceeded:
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
		resultRef = outcome.Record.Attempt.ResultRef
	case corecontract.ModelAttemptFailed:
		if _, err := fixture.store.CommitModelDispatchOutcome(
			context.Background(),
			CommitModelDispatchOutcomeInput{
				Lease:                   begin.Lease,
				AttemptID:               begin.Attempt.AttemptID,
				InvocationID:            begin.Attempt.AttemptID,
				Provider:                begin.Attempt.Binding.Provider,
				ExpectedAttemptRevision: begin.Attempt.Revision,
				State:                   corecontract.ModelAttemptFailed,
				ErrorClassification:     "LEARNING_TEST_PROVIDER_FAILURE",
			},
		); err != nil {
			t.Fatal(err)
		}
	case corecontract.ModelAttemptUnknown:
		if _, err := fixture.store.CommitModelDispatchOutcome(
			context.Background(),
			CommitModelDispatchOutcomeInput{
				Lease:                   begin.Lease,
				AttemptID:               begin.Attempt.AttemptID,
				InvocationID:            begin.Attempt.AttemptID,
				Provider:                begin.Attempt.Binding.Provider,
				ExpectedAttemptRevision: begin.Attempt.Revision,
				State:                   corecontract.ModelAttemptUnknown,
				ProviderRequestID:       "learning-test-provider-request",
				UnknownReason:           "provider result was not observed",
			},
		); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported Learning fixture state %q", state)
	}
	manifest, err := corecontract.RestoreRunManifest(fixture.input.RunManifestCanonical)
	if err != nil {
		t.Fatal(err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(
		fixture.input.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	return learningProposalStoreFixture{
		store:     fixture.store,
		admission: fixture,
		manifest:  manifest,
		member:    member,
		attemptID: begin.Attempt.AttemptID,
		resultRef: resultRef,
	}
}

func newLearningProposalStoreFixtureForTenant(
	t *testing.T,
	base learningProposalStoreFixture,
	tenantID string,
) learningProposalStoreFixture {
	t.Helper()
	ctx := context.Background()
	control, err := controlcontract.RestoreControlSnapshot(
		base.admission.controlCanonical,
		base.admission.basis.Control,
	)
	if err != nil {
		t.Fatalf("restore base Control: %v", err)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		base.admission.catalogCanonical,
		base.admission.basis.Catalog,
	)
	if err != nil {
		t.Fatalf("restore base Catalog: %v", err)
	}
	for _, entry := range catalog.Entries {
		activation, err := base.store.GetLatestModuleActivationForInstance(
			ctx,
			base.manifest.TenantID,
			entry.Activation.InstanceID,
		)
		if err != nil {
			t.Fatalf("load base activation %s: %v", entry.Activation.InstanceID, err)
		}
		if _, err := base.store.ActivateModule(ctx, ActivateModuleInput{
			ActivationID: "activation-" + tenantID + "-" + activation.InstanceID,
			TenantID:     tenantID, InstanceID: activation.InstanceID,
			InstallationID:     activation.InstallationID,
			ActivationRevision: activation.ActivationRevision,
			ExecutionClass:     activation.ExecutionClass,
			AdapterIdentity:    activation.AdapterIdentity,
		}); err != nil {
			t.Fatalf("activate %s for second Tenant: %v", activation.InstanceID, err)
		}
	}

	control.SnapshotID = "control-" + tenantID
	control.TenantID = tenantID
	control.Revision = 1
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze second-Tenant Control: %v", err)
	}
	catalog.GenerationID = "catalog-" + tenantID
	catalog.Generation = 1
	catalog.TenantID = tenantID
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze second-Tenant Catalog: %v", err)
	}
	basis, err := base.store.PublishControlCatalog(
		ctx,
		PublishControlCatalogInput{
			ExpectedPointerRevision: 0,
			NewPointerRevision:      1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	)
	if err != nil {
		t.Fatalf("publish second-Tenant Control/Catalog: %v", err)
	}

	intent := base.admission.intent
	intent.TenantID = tenantID
	intent.AdmissionKey = "admission-" + tenantID
	_, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(intent)
	if err != nil {
		t.Fatalf("freeze second-Tenant intent: %v", err)
	}
	admission := &admissionCommitFixture{
		store:            base.store,
		basis:            basis,
		controlCanonical: controlCanonical,
		catalogCanonical: catalogCanonical,
		modelProfile:     base.admission.modelProfile,
		intent:           intent,
		task:             base.admission.task,
		staticContext:    base.admission.staticContext,
	}
	runID := "run-" + tenantID
	admission.input = admission.compileInput(
		t,
		intentCanonical,
		intentDigest,
		runID,
	)
	if _, err := base.store.CommitRunAdmission(ctx, admission.input); err != nil {
		t.Fatalf("admit second-Tenant Run: %v", err)
	}
	lease, err := base.store.AcquireRunLease(ctx, AcquireRunLeaseInput{
		RunID: runID, OwnerID: "worker-" + tenantID,
		ExpectedRunRevision: 0, ExpectedFrameRevision: 0,
		TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("lease second-Tenant Run: %v", err)
	}
	baseAttempt, err := base.store.GetModelDispatchRecord(ctx, base.attemptID)
	if err != nil {
		t.Fatalf("load base Model request: %v", err)
	}
	manifest, err := corecontract.RestoreRunManifest(
		admission.input.RunManifestCanonical,
	)
	if err != nil {
		t.Fatalf("restore second-Tenant Manifest: %v", err)
	}
	begin, err := base.store.BeginModelDispatch(ctx, BeginModelDispatchInput{
		Lease: lease, AttemptID: "attempt-" + tenantID,
		LogicalStepID:    "reply-1",
		RequestCanonical: bytes.Clone(baseAttempt.Attempt.Request.CanonicalBytes),
		Deadline:         manifest.Deadline.Add(-time.Hour).Truncate(time.Microsecond),
	})
	if err != nil {
		t.Fatalf("begin second-Tenant Model Attempt: %v", err)
	}
	outputCanonical, usageCanonical := modelSuccessOutcomeCanonical(t)
	outcome, err := base.store.CommitModelDispatchOutcome(
		ctx,
		CommitModelDispatchOutcomeInput{
			Lease: begin.Lease, AttemptID: begin.Attempt.AttemptID,
			InvocationID:            begin.Attempt.AttemptID,
			Provider:                begin.Attempt.Binding.Provider,
			ExpectedAttemptRevision: begin.Attempt.Revision,
			State:                   corecontract.ModelAttemptSucceeded,
			OutputCanonical:         outputCanonical,
			UsageReceiptCanonical:   usageCanonical,
		},
	)
	if err != nil {
		t.Fatalf("complete second-Tenant Model Attempt: %v", err)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(
		admission.input.MemberSnapshotCanonical,
	)
	if err != nil {
		t.Fatalf("restore second-Tenant Member: %v", err)
	}
	return learningProposalStoreFixture{
		store: base.store, admission: admission, manifest: manifest, member: member,
		attemptID: outcome.Record.Attempt.AttemptID,
		resultRef: outcome.Record.Attempt.ResultRef,
	}
}

func (fixture learningProposalStoreFixture) input(
	t *testing.T,
	version string,
	text string,
	evidence learningcontract.SourceEvidenceV1,
	tenantID string,
	visibleWorkspaceID string,
) SubmitKnowledgeProposalInput {
	t.Helper()
	target := moduleapi.Ref{
		ID:      "freeagent.test.knowledge.learning",
		Version: version,
	}
	draft := learningKnowledgeDraft(
		t,
		target.ID,
		target.Version,
		text,
		tenantID,
		visibleWorkspaceID,
	)
	return SubmitKnowledgeProposalInput{
		Proposal: learningcontract.ProposalV1{
			SchemaVersion:          learningcontract.ProposalSchemaVersionV1,
			Kind:                   learningcontract.ProposalKindKnowledgeV1,
			TenantID:               tenantID,
			Workspace:              fixture.member.Workspace,
			ProposerAgent:          fixture.member.Agent,
			ProposerProfile:        fixture.member.Profile,
			ProposerRunID:          fixture.manifest.RunID,
			ProposerManifestDigest: fixture.manifest.ManifestDigest,
			ProposerMember: corecontract.MemberSnapshotRef{
				MemberID: fixture.member.MemberID,
				Digest:   fixture.member.MemberSnapshotDigest,
			},
			ProposerResultRef: fixture.resultRef,
			Target:            target,
		},
		DraftCanonical: draft,
		SourceEvidence: evidence,
	}
}

func learningKnowledgeDraft(
	t *testing.T,
	targetID string,
	version string,
	text string,
	tenantID string,
	workspaceID string,
) []byte {
	t.Helper()
	_, canonical, _, err := moduleapi.NewKnowledgeSourceV1(moduleapi.KnowledgeSourceV1{
		SchemaVersion: moduleapi.KnowledgeSourceSchemaV1,
		ID:            targetID,
		Version:       version,
		Chunks: []moduleapi.KnowledgeChunkV1{{
			Document: moduleapi.KnowledgeDocumentRefV1{
				ID:      "learning-document",
				Version: version,
				Digest:  learningTestDigest(version + "\x00" + text),
			},
			ChunkID: "learning-chunk",
			Text:    text,
			VisibleTo: []moduleapi.KnowledgeScopeRuleV1{{
				TenantID:     tenantID,
				WorkspaceID:  workspaceID,
				AgentID:      "*",
				TaskInputRef: "*",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func learningTestDigest(value string) string {
	return moduleapi.Digest("freeagent.learning-store-test/v1", []byte(value))
}

func localLearningEvidence(origin, revision string) learningcontract.SourceEvidenceV1 {
	return learningcontract.SourceEvidenceV1{
		Mechanism:        learningcontract.SourceMechanismLocalImportV1,
		OriginMaterial:   []byte(origin),
		RevisionMaterial: []byte(revision),
	}
}

func cloneLearningSubmitInput(input SubmitKnowledgeProposalInput) SubmitKnowledgeProposalInput {
	input.DraftCanonical = bytes.Clone(input.DraftCanonical)
	input.SourceEvidence.OriginMaterial = bytes.Clone(input.SourceEvidence.OriginMaterial)
	input.SourceEvidence.RevisionMaterial = bytes.Clone(input.SourceEvidence.RevisionMaterial)
	return input
}

type concurrentLearningSubmission struct {
	result SubmitKnowledgeProposalResult
	err    error
}

func runConcurrentLearningSubmissions(
	t *testing.T,
	store *Store,
	inputs []SubmitKnowledgeProposalInput,
) []concurrentLearningSubmission {
	t.Helper()
	start := make(chan struct{})
	results := make([]concurrentLearningSubmission, len(inputs))
	var wait sync.WaitGroup
	for index := range inputs {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index].result, results[index].err =
				store.SubmitKnowledgeProposal(
					context.Background(),
					cloneLearningSubmitInput(inputs[index]),
				)
		}(index)
	}
	close(start)
	wait.Wait()
	return results
}

type learningOutsideScopeSnapshot struct {
	LearningProposals           int64
	ControlCurrent              int64
	ControlPointerRevisionTotal int64
	ControlSnapshots            int64
	AuthorityCeilings           int64
	Installations               int64
	Activations                 int64
	CatalogGenerations          int64
	Runs                        int64
	ModelAttempts               int64
	ModelUsage                  int64
	DispatchAttempts            int64
	RunEvents                   int64
	HistoryEntries              int64
	MemoryRevisions             int64
	ChannelIngress              int64
	SchedulerState              int64
}

func snapshotLearningOutsideScope(
	t *testing.T,
	store *Store,
) learningOutsideScopeSnapshot {
	t.Helper()
	var snapshot learningOutsideScopeSnapshot
	if err := store.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM learning_proposals),
			(SELECT COUNT(*) FROM control_current),
			(SELECT COALESCE(SUM(pointer_revision), 0) FROM control_current),
			(SELECT COUNT(*) FROM control_snapshots),
			(SELECT COUNT(*) FROM content_records WHERE kind='AUTHORITY_CEILING'),
			(SELECT COUNT(*) FROM module_installations),
			(SELECT COUNT(*) FROM module_activations),
			(SELECT COUNT(*) FROM runtime_catalog_generations),
			(SELECT COUNT(*) FROM runs),
			(SELECT COUNT(*) FROM model_dispatch_attempts),
			(SELECT COUNT(*) FROM model_usage),
			(SELECT COUNT(*) FROM dispatch_attempts),
			(SELECT COUNT(*) FROM run_events),
			(SELECT COUNT(*) FROM history_entries),
			(SELECT COUNT(*) FROM agent_memory_revisions),
			(SELECT COUNT(*) FROM channel_ingress_receipts),
			(SELECT COUNT(*) FROM workspace_scheduler_state)
	`).Scan(
		&snapshot.LearningProposals,
		&snapshot.ControlCurrent,
		&snapshot.ControlPointerRevisionTotal,
		&snapshot.ControlSnapshots,
		&snapshot.AuthorityCeilings,
		&snapshot.Installations,
		&snapshot.Activations,
		&snapshot.CatalogGenerations,
		&snapshot.Runs,
		&snapshot.ModelAttempts,
		&snapshot.ModelUsage,
		&snapshot.DispatchAttempts,
		&snapshot.RunEvents,
		&snapshot.HistoryEntries,
		&snapshot.MemoryRevisions,
		&snapshot.ChannelIngress,
		&snapshot.SchedulerState,
	); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertLearningProposalCount(t *testing.T, store *Store, want int64) {
	t.Helper()
	var got int64
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM learning_proposals`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Learning Proposal count=%d want=%d", got, want)
	}
}
