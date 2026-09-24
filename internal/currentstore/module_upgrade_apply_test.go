package currentstore

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleUpgradeApplyApprovalLoadRevalidateExactRetryAndDefensiveCopy(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	input := commitApprovedModuleUpgradeForApplyTest(t, fixture, fixture.selection)
	beforeSupply := moduleUpgradeSupplyCounts(t, store)
	beforeEffects := moduleUpgradeEffectCounts(t, store)

	loaded, err := store.LoadModuleUpgradeApplyApproval(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	revalidated, err := store.RevalidateCurrentModuleUpgradeApplyApproval(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.LoadModuleUpgradeApplyApproval(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !equalModuleUpgradeApplyApproval(loaded, revalidated) ||
		!equalModuleUpgradeApplyApproval(loaded, retry) {
		t.Fatal("unchanged exact Load/Revalidate retry projections differ")
	}
	if loaded.Review.Review.Conclusion != moduleupgrade.ConclusionWouldApplyV1 ||
		loaded.Decision.Decision.Decision != moduleapi.ModuleCandidateDecisionApproveV1 ||
		loaded.TargetEntry.ArtifactDigest != loaded.Review.Review.Target.ArtifactDigest ||
		loaded.TargetManifest.Digest != loaded.Review.Review.Target.ManifestRef ||
		loaded.BindingConfig.Digest != loaded.SelectedImpact.ConfigRef ||
		loaded.AuthorityCeiling.Digest != loaded.SelectedImpact.AuthorityCeilingRef ||
		loaded.CurrentActivation.InstanceID != loaded.Review.Review.Current.Activation.InstanceID ||
		loaded.CurrentInstallation.InstallationID != loaded.Review.Review.Current.InstallationID {
		t.Fatalf("approval projection differs: %+v", loaded)
	}
	if !reflect.DeepEqual(moduleUpgradeSupplyCounts(t, store), beforeSupply) ||
		!reflect.DeepEqual(moduleUpgradeEffectCounts(t, store), beforeEffects) {
		t.Fatal("approval reads changed Store state")
	}

	// Mutate every returned byte/slice family that the Apply service consumes;
	// a fresh read must still reproduce the Store-owned exact value.
	loaded.Review.Canonical[0] ^= 0xff
	loaded.Review.Candidate.Canonical[0] ^= 0xff
	loaded.Decision.Canonical[0] ^= 0xff
	loaded.Source.PolicyCanonical[0] ^= 0xff
	loaded.Snapshot.SnapshotCanonical[0] ^= 0xff
	loaded.TargetManifest.CanonicalBytes[0] ^= 0xff
	loaded.BindingConfig.CanonicalBytes[0] ^= 0xff
	loaded.AuthorityCeiling.CanonicalBytes[0] ^= 0xff
	loaded.CurrentInstallation.ManifestBytes[0] ^= 0xff
	loaded.SelectedImpact.StaticContextRefs = append(
		loaded.SelectedImpact.StaticContextRefs,
		"mutated",
	)
	loaded.HistoricalControl.Profiles[0].Profile.ID = "mutated-profile"
	loaded.HistoricalControl.Profiles[0].Bindings[0].InstanceID =
		"mutated-instance"
	loaded.HistoricalControl.Profiles[0].Bindings[0].StaticContextRefs =
		append(
			loaded.HistoricalControl.Profiles[0].Bindings[0].StaticContextRefs,
			"mutated-static-context",
		)
	loaded.HistoricalCatalog.Entries[0].Activation.InstanceID =
		"mutated-catalog-instance"
	loaded.HistoricalCatalog.Entries[0].Provides[0].Name = "mutated.port"
	fresh, err := store.LoadModuleUpgradeApplyApproval(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !equalModuleUpgradeApplyApproval(retry, fresh) {
		t.Fatal("caller mutation escaped detached approval projection")
	}
}

func TestDetachModuleUpgradeApplyApprovalDeepCopiesControlCatalogBothDirections(t *testing.T) {
	modelProfile := corecontract.ModelProfileRef{
		ID:      "model-profile",
		Version: "v1",
		Digest:  strings.Repeat("1", 64),
	}
	base := ModuleUpgradeApplyApproval{
		HistoricalControl: controlcontract.ControlSnapshot{
			Agents: []corecontract.AgentRef{{
				ID: "agent-one", Version: "v1", Digest: strings.Repeat("2", 64),
			}},
			CompositeAgents: []controlcontract.CompositeAgentDefinitionV1{{
				Members: []controlcontract.CompositeAgentMemberV1{{SlotID: "slot-one"}},
				Reviewer: &controlcontract.CompositeReviewerDefinitionV1{
					AgentID: "reviewer-one",
				},
				Decision: &controlcontract.CompositeDecisionDefinitionV1{
					SchemaVersion: controlcontract.CompositeDecisionSchemaVersionV1,
				},
			}},
			Profiles: []controlcontract.ProfileDefinition{{
				Profile:      corecontract.ProfileRef{ID: "profile-one"},
				ModelProfile: &modelProfile,
				Bindings: []controlcontract.BindingSpec{{
					InstanceID:        "profile-instance-one",
					StaticContextRefs: []string{"profile-static-one"},
				}},
			}},
			Workspaces: []controlcontract.WorkspaceDefinition{{
				Workspace: corecontract.WorkspaceRef{ID: "workspace-one"},
				ChannelEndpoints: []controlcontract.ChannelEndpointDefinition{{
					EndpointID: "endpoint-one",
					Binding: controlcontract.BindingSpec{
						InstanceID:        "endpoint-instance-one",
						StaticContextRefs: []string{"endpoint-static-one"},
					},
				}},
				ChannelIdentities: []controlcontract.ChannelIdentityDefinition{{
					ExternalUserID: "external-one",
				}},
				TransferGrants: []corecontract.WorkspaceTransferGrantV1{{
					GrantID: "grant-one",
					SendPayloadKinds: []corecontract.WorkspaceTransferPayloadKindV1{
						corecontract.WorkspaceTransferPayloadTaskSummaryV1,
					},
					ReceivePayloadKinds: []corecontract.WorkspaceTransferPayloadKindV1{
						corecontract.WorkspaceTransferPayloadSpecialistResultV1,
					},
				}},
			}},
		},
		HistoricalCatalog: controlcontract.CatalogGeneration{
			Entries: []controlcontract.CatalogEntry{{
				Activation: moduleapi.ActivatedModuleRef{InstanceID: "catalog-instance-one"},
				Provides: []moduleapi.PortRef{{
					Name:         moduleapi.PortNameModelGenerate,
					ExactVersion: moduleapi.PortVersionV2,
				}},
			}},
		},
	}
	detached := DetachModuleUpgradeApplyApproval(base)

	// Caller-owned source mutations must not alter the detached result.
	base.HistoricalControl.Agents[0].ID = "source-agent-mutated"
	base.HistoricalControl.CompositeAgents[0].Members[0].SlotID = "source-slot-mutated"
	base.HistoricalControl.CompositeAgents[0].Reviewer.AgentID = "source-reviewer-mutated"
	base.HistoricalControl.CompositeAgents[0].Decision.SchemaVersion = "source-decision-mutated"
	base.HistoricalControl.Profiles[0].Profile.ID = "source-profile-mutated"
	base.HistoricalControl.Profiles[0].ModelProfile.ID = "source-model-profile-mutated"
	base.HistoricalControl.Profiles[0].Bindings[0].InstanceID = "source-binding-mutated"
	base.HistoricalControl.Profiles[0].Bindings[0].StaticContextRefs[0] = "source-profile-static-mutated"
	base.HistoricalControl.Workspaces[0].Workspace.ID = "source-workspace-mutated"
	base.HistoricalControl.Workspaces[0].ChannelEndpoints[0].EndpointID = "source-endpoint-mutated"
	base.HistoricalControl.Workspaces[0].ChannelEndpoints[0].Binding.StaticContextRefs[0] = "source-endpoint-static-mutated"
	base.HistoricalControl.Workspaces[0].ChannelIdentities[0].ExternalUserID = "source-identity-mutated"
	base.HistoricalControl.Workspaces[0].TransferGrants[0].GrantID = "source-grant-mutated"
	base.HistoricalControl.Workspaces[0].TransferGrants[0].SendPayloadKinds[0] = corecontract.WorkspaceTransferPayloadSpecialistResultV1
	base.HistoricalControl.Workspaces[0].TransferGrants[0].ReceivePayloadKinds[0] = corecontract.WorkspaceTransferPayloadTaskSummaryV1
	base.HistoricalCatalog.Entries[0].Activation.InstanceID = "source-catalog-mutated"
	base.HistoricalCatalog.Entries[0].Provides[0].Name = "source.port"

	if detached.HistoricalControl.Agents[0].ID != "agent-one" ||
		detached.HistoricalControl.CompositeAgents[0].Members[0].SlotID != "slot-one" ||
		detached.HistoricalControl.CompositeAgents[0].Reviewer.AgentID != "reviewer-one" ||
		detached.HistoricalControl.CompositeAgents[0].Decision.SchemaVersion != controlcontract.CompositeDecisionSchemaVersionV1 ||
		detached.HistoricalControl.Profiles[0].Profile.ID != "profile-one" ||
		detached.HistoricalControl.Profiles[0].ModelProfile.ID != "model-profile" ||
		detached.HistoricalControl.Profiles[0].Bindings[0].InstanceID != "profile-instance-one" ||
		detached.HistoricalControl.Profiles[0].Bindings[0].StaticContextRefs[0] != "profile-static-one" ||
		detached.HistoricalControl.Workspaces[0].Workspace.ID != "workspace-one" ||
		detached.HistoricalControl.Workspaces[0].ChannelEndpoints[0].EndpointID != "endpoint-one" ||
		detached.HistoricalControl.Workspaces[0].ChannelEndpoints[0].Binding.StaticContextRefs[0] != "endpoint-static-one" ||
		detached.HistoricalControl.Workspaces[0].ChannelIdentities[0].ExternalUserID != "external-one" ||
		detached.HistoricalControl.Workspaces[0].TransferGrants[0].GrantID != "grant-one" ||
		detached.HistoricalControl.Workspaces[0].TransferGrants[0].SendPayloadKinds[0] != corecontract.WorkspaceTransferPayloadTaskSummaryV1 ||
		detached.HistoricalControl.Workspaces[0].TransferGrants[0].ReceivePayloadKinds[0] != corecontract.WorkspaceTransferPayloadSpecialistResultV1 ||
		detached.HistoricalCatalog.Entries[0].Activation.InstanceID != "catalog-instance-one" ||
		detached.HistoricalCatalog.Entries[0].Provides[0].Name != moduleapi.PortNameModelGenerate {
		t.Fatal("source mutation crossed into detached Control/Catalog")
	}

	// Mutations in the detached result must likewise not alter the source.
	detached.HistoricalControl.Agents[0].ID = "detached-agent-mutated"
	detached.HistoricalControl.CompositeAgents[0].Members[0].SlotID = "detached-slot-mutated"
	detached.HistoricalControl.CompositeAgents[0].Reviewer.AgentID = "detached-reviewer-mutated"
	detached.HistoricalControl.Profiles[0].ModelProfile.ID = "detached-model-profile-mutated"
	detached.HistoricalControl.Profiles[0].Bindings[0].StaticContextRefs[0] = "detached-profile-static-mutated"
	detached.HistoricalControl.Workspaces[0].ChannelEndpoints[0].Binding.StaticContextRefs[0] = "detached-endpoint-static-mutated"
	detached.HistoricalControl.Workspaces[0].ChannelIdentities[0].ExternalUserID = "detached-identity-mutated"
	detached.HistoricalControl.Workspaces[0].TransferGrants[0].SendPayloadKinds[0] = corecontract.WorkspaceTransferPayloadSpecialistResultV1
	detached.HistoricalCatalog.Entries[0].Activation.InstanceID = "detached-catalog-mutated"
	detached.HistoricalCatalog.Entries[0].Provides[0].Name = "detached.port"
	if base.HistoricalControl.Agents[0].ID != "source-agent-mutated" ||
		base.HistoricalControl.CompositeAgents[0].Members[0].SlotID != "source-slot-mutated" ||
		base.HistoricalControl.CompositeAgents[0].Reviewer.AgentID != "source-reviewer-mutated" ||
		base.HistoricalControl.Profiles[0].ModelProfile.ID != "source-model-profile-mutated" ||
		base.HistoricalControl.Profiles[0].Bindings[0].StaticContextRefs[0] != "source-profile-static-mutated" ||
		base.HistoricalControl.Workspaces[0].ChannelEndpoints[0].Binding.StaticContextRefs[0] != "source-endpoint-static-mutated" ||
		base.HistoricalControl.Workspaces[0].ChannelIdentities[0].ExternalUserID != "source-identity-mutated" ||
		base.HistoricalControl.Workspaces[0].TransferGrants[0].SendPayloadKinds[0] != corecontract.WorkspaceTransferPayloadSpecialistResultV1 ||
		base.HistoricalCatalog.Entries[0].Activation.InstanceID != "source-catalog-mutated" ||
		base.HistoricalCatalog.Entries[0].Provides[0].Name != "source.port" {
		t.Fatal("detached mutation crossed back into source Control/Catalog")
	}
}

func TestModuleUpgradeApplyApprovalInputAndTerminalDecisionClosedSet(t *testing.T) {
	ctx := context.Background()
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	approved := commitApprovedModuleUpgradeForApplyTest(t, fixture, fixture.selection)
	validOtherDigest := strings.Repeat("f", 64)
	for _, test := range []struct {
		name  string
		ctx   context.Context
		input ModuleUpgradeApplyApprovalInput
		want  error
	}{
		{name: "nil context", input: approved, want: ErrInvalidModuleUpgradeReview},
		{name: "invalid review", ctx: ctx, input: ModuleUpgradeApplyApprovalInput{TenantID: approved.TenantID, ReviewID: "bad", DecisionID: approved.DecisionID}, want: ErrInvalidModuleUpgradeReview},
		{name: "invalid tenant", ctx: ctx, input: ModuleUpgradeApplyApprovalInput{TenantID: " bad ", ReviewID: approved.ReviewID, DecisionID: approved.DecisionID}, want: ErrInvalidModuleUpgradeReview},
		{name: "missing review", ctx: ctx, input: ModuleUpgradeApplyApprovalInput{TenantID: approved.TenantID, ReviewID: validOtherDigest, DecisionID: approved.DecisionID}, want: ErrModuleUpgradeReviewNotFound},
		{name: "wrong decision id", ctx: ctx, input: ModuleUpgradeApplyApprovalInput{TenantID: approved.TenantID, ReviewID: approved.ReviewID, DecisionID: validOtherDigest}, want: ErrModuleUpgradeReviewConflict},
		{name: "wrong tenant", ctx: ctx, input: ModuleUpgradeApplyApprovalInput{TenantID: approved.TenantID + "-other", ReviewID: approved.ReviewID, DecisionID: approved.DecisionID}, want: ErrModuleUpgradeReviewConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := store.LoadModuleUpgradeApplyApproval(test.ctx, test.input); !errors.Is(err, test.want) {
				t.Fatalf("Load error=%v want %v", err, test.want)
			}
			if _, err := store.RevalidateCurrentModuleUpgradeApplyApproval(test.ctx, test.input); !errors.Is(err, test.want) {
				t.Fatalf("Revalidate error=%v want %v", err, test.want)
			}
		})
	}

	rejectedFixture := newModuleUpgradeStoreFixture(t)
	rejectedBasis, err := rejectedFixture.publication.store.ReadModuleUpgradeReviewBasis(
		ctx,
		rejectedFixture.selection,
	)
	if err != nil {
		t.Fatal(err)
	}
	rejectedReview, err := rejectedFixture.publication.store.CommitModuleUpgradeReview(
		ctx,
		CommitModuleUpgradeReviewInput{
			Basis: rejectedBasis,
			ReviewCanonical: moduleUpgradeReviewCanonical(
				t,
				rejectedBasis,
				rejectedFixture.targetManifest,
				moduleupgrade.ConclusionWouldApplyV1,
			),
			TargetManifestCanonical: rejectedFixture.targetManifest,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rejectCanonical := moduleUpgradeDecisionCanonical(
		t,
		rejectedReview.Review.CandidateID,
		moduleapi.ModuleCandidateDecisionRejectV1,
		"operator rejects exact candidate",
	)
	reject, err := rejectedFixture.publication.store.DecideModuleCandidate(
		ctx,
		DecideModuleCandidateInput{
			TenantID:                rejectedFixture.publication.tenantID,
			ReviewID:                rejectedReview.ReviewID,
			DecisionCanonical:       rejectCanonical,
			ConfirmTenantWideReject: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	rejectedInput := ModuleUpgradeApplyApprovalInput{
		TenantID:   rejectedFixture.publication.tenantID,
		ReviewID:   rejectedReview.ReviewID,
		DecisionID: reject.DecisionID,
	}
	if _, err := rejectedFixture.publication.store.LoadModuleUpgradeApplyApproval(ctx, rejectedInput); !errors.Is(err, ErrModuleUpgradeReviewConflict) {
		t.Fatalf("REJECT Load error=%v", err)
	}
}

func TestModuleUpgradeApplyApprovalHistoricalReadSurvivesCurrentPointerAndSourceDrift(t *testing.T) {
	ctx := context.Background()
	t.Run("current pointer", func(t *testing.T) {
		fixture := newModuleUpgradeStoreFixture(t)
		store := fixture.publication.store
		input := commitApprovedModuleUpgradeForApplyTest(t, fixture, fixture.selection)
		next := fixture.publication.input(
			t,
			"apply-current-control-2",
			2,
			"apply-current-catalog-2",
			2,
			1,
			2,
			nil,
		)
		if _, err := store.PublishControlCatalog(ctx, next); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LoadModuleUpgradeApplyApproval(ctx, input); err != nil {
			t.Fatalf("historical Load after pointer drift=%v", err)
		}
		if _, err := store.RevalidateCurrentModuleUpgradeApplyApproval(ctx, input); !errors.Is(err, ErrModuleUpgradeReviewStale) {
			t.Fatalf("pointer drift Revalidate error=%v", err)
		}
	})

	t.Run("source snapshot head", func(t *testing.T) {
		fixture := newModuleUpgradeStoreFixture(t)
		store := fixture.publication.store
		input := commitApprovedModuleUpgradeForApplyTest(t, fixture, fixture.selection)
		basis, err := store.ReadModuleSourceRefreshBasis(ctx, fixture.selection.SourceID)
		if err != nil {
			t.Fatal(err)
		}
		extra := moduleDiscoveryTestEntry(
			"test.other",
			"v1",
			strings.Repeat("e", 64),
		)
		if _, err := store.CommitModuleSourceRefresh(
			ctx,
			basis,
			moduleDiscoveryTestIndex(
				t,
				fixture.selection.SourceID,
				fixture.snapshot.Snapshot.Entries[0],
				extra,
			),
		); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LoadModuleUpgradeApplyApproval(ctx, input); err != nil {
			t.Fatalf("historical Load after Source drift=%v", err)
		}
		if _, err := store.RevalidateCurrentModuleUpgradeApplyApproval(ctx, input); !errors.Is(err, ErrModuleUpgradeReviewStale) {
			t.Fatalf("Source drift Revalidate error=%v", err)
		}
	})

	t.Run("compatible policy revision", func(t *testing.T) {
		fixture := newModuleUpgradeStoreFixture(t)
		store := fixture.publication.store
		input := commitApprovedModuleUpgradeForApplyTest(t, fixture, fixture.selection)
		historical, err := store.LoadModuleUpgradeApplyApproval(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		policy := historical.Source.Policy
		policy.AllowedModuleIDPrefixes = []string{"test", "future"}
		_, canonical, _, err := moduleapi.NewModuleSourcePolicyV1(policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterModuleSource(
			ctx,
			RegisterModuleSourceInput{
				PolicyCanonical:        canonical,
				ExpectedPolicyRevision: historical.Source.PolicyRevision,
			},
		); err != nil {
			t.Fatal(err)
		}
		basis, err := store.ReadModuleSourceRefreshBasis(ctx, fixture.selection.SourceID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitModuleSourceRefresh(
			ctx,
			basis,
			moduleDiscoveryTestIndex(
				t,
				fixture.selection.SourceID,
				fixture.snapshot.Snapshot.Entries[0],
			),
		); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LoadModuleUpgradeApplyApproval(ctx, input); err != nil {
			t.Fatalf("historical Load after policy revision=%v", err)
		}
		if _, err := store.RevalidateCurrentModuleUpgradeApplyApproval(ctx, input); !errors.Is(err, ErrModuleUpgradeReviewStale) {
			t.Fatalf("policy revision Revalidate error=%v", err)
		}
	})
}

func TestModuleUpgradeApplyApprovalCurrentRevocationAndTenantSuppression(t *testing.T) {
	ctx := context.Background()
	t.Run("PublisherKey revocation", func(t *testing.T) {
		fixture := newModuleUpgradeStoreFixture(t)
		store := fixture.publication.store
		keyCanonical, keyID := moduleDiscoveryTestPublisherKey(t)
		policyCanonical, policy, _ := moduleDiscoveryTestPolicy(
			t,
			"source.upgrade.apply.signed",
			"upgrade-apply-signed-origin",
			true,
			keyID,
		)
		policy.AllowedModuleIDPrefixes = []string{"test"}
		_, policyCanonical, _, err := moduleapi.NewModuleSourcePolicyV1(policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.RegisterModuleSource(
			ctx,
			RegisterModuleSourceInput{
				PolicyCanonical:       policyCanonical,
				PublisherKeyCanonical: keyCanonical,
			},
		); err != nil {
			t.Fatal(err)
		}
		refresh, err := store.ReadModuleSourceRefreshBasis(
			ctx,
			"source.upgrade.apply.signed",
		)
		if err != nil {
			t.Fatal(err)
		}
		target := moduleDiscoveryTestEntry(
			"test.model",
			"v2",
			strings.Repeat("b", 64),
		)
		target.SignatureID = strings.Repeat("c", 64)
		snapshot, err := store.CommitModuleSourceRefresh(
			ctx,
			refresh,
			moduleDiscoveryTestIndex(
				t,
				"source.upgrade.apply.signed",
				target,
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		selection := fixture.selection
		selection.SourceID = "source.upgrade.apply.signed"
		selection.SnapshotID = snapshot.SnapshotID
		input := commitApprovedModuleUpgradeForApplyTest(t, fixture, selection)
		if _, err := store.RevokeModulePublisherKey(ctx, keyID, 1); err != nil {
			t.Fatal(err)
		}
		loaded, err := store.LoadModuleUpgradeApplyApproval(ctx, input)
		if err != nil || loaded.PublisherKey == nil || loaded.PublisherKey.RevokedAt == nil {
			t.Fatalf("historical signed Load=%+v err=%v", loaded.PublisherKey, err)
		}
		if _, err := store.RevalidateCurrentModuleUpgradeApplyApproval(ctx, input); !errors.Is(err, ErrModuleUpgradeReviewStale) {
			t.Fatalf("revocation Revalidate error=%v", err)
		}
	})

	t.Run("tenant reject", func(t *testing.T) {
		fixture := newModuleUpgradeStoreFixture(t)
		addSecondModuleUpgradeProfile(t, fixture.publication)
		store := fixture.publication.store
		firstSelection := fixture.selection
		secondSelection := fixture.selection
		secondSelection.BindingTarget = moduleupgrade.BindingTargetV1{
			Kind:      moduleupgrade.BindingTargetProfileV1,
			ProfileID: "profile-secondary",
		}
		secondSelection.TargetInstanceID = "instance-model-target-secondary"
		firstInput := commitApprovedModuleUpgradeForApplyTest(
			t,
			fixture,
			firstSelection,
		)
		secondBasis, err := store.ReadModuleUpgradeReviewBasis(ctx, secondSelection)
		if err != nil {
			t.Fatal(err)
		}
		secondReview, err := store.CommitModuleUpgradeReview(
			ctx,
			CommitModuleUpgradeReviewInput{
				Basis: secondBasis,
				ReviewCanonical: moduleUpgradeReviewCanonical(
					t,
					secondBasis,
					fixture.targetManifest,
					moduleupgrade.ConclusionWouldApplyV1,
				),
				TargetManifestCanonical: fixture.targetManifest,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		rejectCanonical := moduleUpgradeDecisionCanonical(
			t,
			secondReview.Review.CandidateID,
			moduleapi.ModuleCandidateDecisionRejectV1,
			"tenant rejects after another scope approval",
		)
		if _, err := store.DecideModuleCandidate(
			ctx,
			DecideModuleCandidateInput{
				TenantID:                fixture.publication.tenantID,
				ReviewID:                secondReview.ReviewID,
				DecisionCanonical:       rejectCanonical,
				ConfirmTenantWideReject: true,
			},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LoadModuleUpgradeApplyApproval(ctx, firstInput); err != nil {
			t.Fatalf("historical Load after tenant REJECT=%v", err)
		}
		if _, err := store.RevalidateCurrentModuleUpgradeApplyApproval(ctx, firstInput); !errors.Is(err, ErrModuleUpgradeSuppressed) {
			t.Fatalf("tenant suppression Revalidate error=%v", err)
		}
	})
}

func TestModuleUpgradeApplyApprovalFailsClosedOnHistoricalTamper(t *testing.T) {
	fixture := newModuleUpgradeStoreFixture(t)
	store := fixture.publication.store
	ctx := context.Background()
	input := commitApprovedModuleUpgradeForApplyTest(t, fixture, fixture.selection)
	execClosedFileTamperV1(t, store,
		[]string{"module_upgrade_reviews_reject_update"},
		`UPDATE module_upgrade_reviews SET pointer_revision=pointer_revision+1 WHERE review_id=?`,
		input.ReviewID,
	)
	if _, err := store.LoadModuleUpgradeApplyApproval(ctx, input); !errors.Is(err, ErrModuleUpgradeIntegrity) {
		t.Fatalf("historical tamper Load error=%v", err)
	}
	if _, err := store.RevalidateCurrentModuleUpgradeApplyApproval(ctx, input); !errors.Is(err, ErrModuleUpgradeIntegrity) {
		t.Fatalf("historical tamper Revalidate error=%v", err)
	}
}

func commitApprovedModuleUpgradeForApplyTest(
	t *testing.T,
	fixture *moduleUpgradeStoreFixture,
	selection ReadModuleUpgradeReviewBasisInput,
) ModuleUpgradeApplyApprovalInput {
	t.Helper()
	ctx := context.Background()
	store := fixture.publication.store
	basis, err := store.ReadModuleUpgradeReviewBasis(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	review, err := store.CommitModuleUpgradeReview(
		ctx,
		CommitModuleUpgradeReviewInput{
			Basis: basis,
			ReviewCanonical: moduleUpgradeReviewCanonical(
				t,
				basis,
				fixture.targetManifest,
				moduleupgrade.ConclusionWouldApplyV1,
			),
			TargetManifestCanonical: bytes.Clone(fixture.targetManifest),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	decisionCanonical := moduleUpgradeDecisionCanonical(
		t,
		review.Review.CandidateID,
		moduleapi.ModuleCandidateDecisionApproveV1,
		"approved for exact Operator Apply",
	)
	decision, err := store.DecideModuleCandidate(
		ctx,
		DecideModuleCandidateInput{
			TenantID:          selection.TenantID,
			ReviewID:          review.ReviewID,
			DecisionCanonical: decisionCanonical,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return ModuleUpgradeApplyApprovalInput{
		TenantID:   selection.TenantID,
		ReviewID:   review.ReviewID,
		DecisionID: decision.DecisionID,
	}
}
