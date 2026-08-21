package currentstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/moduleupgrade"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// ModuleUpgradeApplyApprovalInput names one exact, already-persisted
// Tenant -> Review -> APPROVE Decision closure. None of these identifiers is
// inferred from a Candidate, Source head, or current Control pointer.
type ModuleUpgradeApplyApprovalInput struct {
	TenantID   string
	ReviewID   string
	DecisionID string
}

// ModuleUpgradeApplyApproval is a detached, read-only projection of the exact
// evidence required to construct an Operator-owned Apply plan. Source is the
// current row for the immutable Source identity; Snapshot retains the exact
// historical SourcePolicy/Index/Snapshot parents admitted by the Review.
//
// Loading this value grants no package, artifact, execution, authority,
// binding, or effect permission. In particular, the selected Binding content
// is returned as immutable evidence rather than as an executable adapter.
type ModuleUpgradeApplyApproval struct {
	Input               ModuleUpgradeApplyApprovalInput
	Review              ModuleUpgradeReviewRecord
	Decision            ModuleCandidateDecisionRecord
	Source              ModuleSource
	PublisherKey        *ModulePublisherKey
	Snapshot            ModuleDiscoverySnapshot
	TargetEntry         moduleapi.ModuleDiscoveryEntryV1
	TargetManifest      ContentRecord
	HistoricalControl   controlcontract.ControlSnapshot
	HistoricalCatalog   controlcontract.CatalogGeneration
	SelectedImpact      moduleupgrade.BindingImpactV1
	BindingConfig       ContentRecord
	AuthorityCeiling    ContentRecord
	CurrentActivation   ModuleActivation
	CurrentInstallation ModuleInstallation
}

// DetachModuleUpgradeApplyApproval returns a deep copy suitable for retaining
// across a bounded artifact verification step. It is the exported analogue of
// the other Store query methods' defensive-copy return contract; it grants no
// additional authority.
func DetachModuleUpgradeApplyApproval(
	value ModuleUpgradeApplyApproval,
) ModuleUpgradeApplyApproval {
	return detachModuleUpgradeApplyApproval(value)
}

// LoadModuleUpgradeApplyApproval restores the complete immutable approval
// closure in one read transaction. It deliberately does not consult mutable
// Source heads, key revocation state, the current Control pointer, or tenant
// suppression when deciding whether historical evidence remains readable.
// Those current facts belong to RevalidateCurrentModuleUpgradeApplyApproval.
func (store *Store) LoadModuleUpgradeApplyApproval(
	ctx context.Context,
	input ModuleUpgradeApplyApprovalInput,
) (ModuleUpgradeApplyApproval, error) {
	if err := validateModuleUpgradeApplyApprovalInput(ctx, input); err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	approval, err := loadModuleUpgradeApplyApproval(ctx, connection, input)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	committed = true
	return detachModuleUpgradeApplyApproval(approval), nil
}

// RevalidateCurrentModuleUpgradeApplyApproval first restores the same exact
// historical approval and then closes every mutable admission edge in the
// same coherent read transaction. Any Source policy/head, PublisherKey,
// current pointer, selected Binding, fan-out impact, Activation, Installation,
// target-instance, or tenant rejection drift fails before Apply is admitted.
// It performs no package, filesystem, network, Host, Gateway, or Store write.
func (store *Store) RevalidateCurrentModuleUpgradeApplyApproval(
	ctx context.Context,
	input ModuleUpgradeApplyApprovalInput,
) (ModuleUpgradeApplyApproval, error) {
	if err := validateModuleUpgradeApplyApprovalInput(ctx, input); err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	defer unlock()
	connection, err := store.db.Conn(ctx)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN`); err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	approval, err := loadModuleUpgradeApplyApproval(ctx, connection, input)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	live, err := rebuildModuleUpgradeReviewBasis(
		ctx,
		connection,
		selectionFromReview(approval.Review.Review),
	)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, mapUpgradeBasisCommitError(err)
	}
	if !reviewMatchesLiveBasis(approval.Review.Review, live) {
		return ModuleUpgradeApplyApproval{}, ErrModuleUpgradeReviewStale
	}
	if err := moduleapi.ValidateModuleSourceCandidateV1(
		live.Source.Policy,
		live.TargetEntry.Module,
		live.TargetEntry.ArtifactSizeBytes,
		live.TargetEntry.SignatureID != "",
	); err != nil {
		return ModuleUpgradeApplyApproval{}, errors.Join(
			ErrModuleUpgradeReviewStale,
			err,
		)
	}
	if err := VerifyPublishedControlCatalogClosureV1(
		ctx,
		connection,
		live.Control,
		live.Catalog,
	); err != nil {
		return ModuleUpgradeApplyApproval{}, errors.Join(
			ErrModuleUpgradeReviewStale,
			err,
		)
	}
	// Return the exact current Source/key/head and target tuple used for the
	// successful second gate. Review/Snapshot/Decision and historical
	// publication facts remain immutable evidence. The caller can therefore
	// rerun the pure candidate and detached-signature checks without querying
	// a second Store authority source.
	approval.Source = live.Source
	approval.PublisherKey = live.PublisherKey
	approval.Snapshot = live.Snapshot
	approval.TargetEntry = live.TargetEntry
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	committed = true
	return detachModuleUpgradeApplyApproval(approval), nil
}

func validateModuleUpgradeApplyApprovalInput(
	ctx context.Context,
	input ModuleUpgradeApplyApprovalInput,
) error {
	if ctx == nil {
		return fmt.Errorf(
			"%w: apply approval context is nil",
			ErrInvalidModuleUpgradeReview,
		)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePublishedBasisTenantID(input.TenantID); err != nil {
		return fmt.Errorf(
			"%w: invalid apply approval Tenant: %v",
			ErrInvalidModuleUpgradeReview,
			err,
		)
	}
	if !moduleapi.ValidSHA256(input.ReviewID) ||
		!moduleapi.ValidSHA256(input.DecisionID) {
		return fmt.Errorf(
			"%w: ReviewID and DecisionID must be SHA-256",
			ErrInvalidModuleUpgradeReview,
		)
	}
	return nil
}

func loadModuleUpgradeApplyApproval(
	ctx context.Context,
	q moduleDiscoveryQueryer,
	input ModuleUpgradeApplyApprovalInput,
) (ModuleUpgradeApplyApproval, error) {
	// Match the existing exact Review/Decision retry contract: unrelated
	// persisted corruption also fails the immutable approval read closed.
	if err := VerifyModuleUpgradeSemanticClosureV1(ctx, q); err != nil {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: apply approval historical closure: %v",
			ErrModuleUpgradeIntegrity,
			err,
		)
	}
	review, found, err := queryModuleUpgradeReview(ctx, q, input.ReviewID)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	if !found {
		return ModuleUpgradeApplyApproval{}, ErrModuleUpgradeReviewNotFound
	}
	decision, found, err := queryModuleCandidateDecisionByReview(
		ctx,
		q,
		input.ReviewID,
	)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	if !found {
		return ModuleUpgradeApplyApproval{}, ErrModuleUpgradeReviewNotFound
	}
	if review.Review.TenantID != input.TenantID ||
		decision.TenantID != input.TenantID ||
		decision.ReviewID != input.ReviewID ||
		decision.DecisionID != input.DecisionID ||
		decision.ReviewKey != review.Review.ReviewKey ||
		decision.Decision.CandidateID != review.Review.CandidateID {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: exact Review/Decision parent differs",
			ErrModuleUpgradeReviewConflict,
		)
	}
	if decision.Decision.Decision != moduleapi.ModuleCandidateDecisionApproveV1 ||
		review.Review.Conclusion != moduleupgrade.ConclusionWouldApplyV1 {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: Apply requires APPROVE over WOULD_APPLY",
			ErrModuleUpgradeReviewConflict,
		)
	}

	source, found, err := queryModuleSource(
		ctx,
		q,
		review.Review.SupplyBasis.SourceID,
	)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	if !found {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: historical Source is absent",
			ErrModuleUpgradeIntegrity,
		)
	}
	snapshot, found, err := queryModuleDiscoverySnapshot(
		ctx,
		q,
		review.Review.SupplyBasis.SnapshotID,
	)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, err
	}
	if !found {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: historical Snapshot is absent",
			ErrModuleUpgradeIntegrity,
		)
	}
	targetEntry, found := findUpgradeTargetEntry(
		snapshot.Snapshot.Entries,
		review.Review.Target.Module,
		review.Review.Target.ArtifactDigest,
	)
	if !found {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: exact target entry is absent from Snapshot",
			ErrModuleUpgradeIntegrity,
		)
	}
	historicalPolicy, _, historicalPolicyID, err :=
		moduleapi.ParseModuleSourcePolicyV1(snapshot.SourcePolicyCanonical)
	if err != nil || historicalPolicyID != snapshot.SourcePolicyID {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: historical SourcePolicy differs: %v",
			ErrModuleUpgradeIntegrity,
			err,
		)
	}
	if err := moduleapi.ValidateModuleSourceCandidateV1(
		historicalPolicy,
		targetEntry.Module,
		targetEntry.ArtifactSizeBytes,
		targetEntry.SignatureID != "",
	); err != nil {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: historical target violates admitted SourcePolicy: %v",
			ErrModuleUpgradeIntegrity,
			err,
		)
	}

	var publisher *ModulePublisherKey
	if snapshot.PublisherKeyID != "" {
		key, found, err := queryModulePublisherKey(
			ctx,
			q,
			snapshot.PublisherKeyID,
		)
		if err != nil {
			return ModuleUpgradeApplyApproval{}, err
		}
		if !found {
			return ModuleUpgradeApplyApproval{}, fmt.Errorf(
				"%w: historical PublisherKey is absent",
				ErrModuleUpgradeIntegrity,
			)
		}
		value := detachModulePublisherKey(key)
		publisher = &value
	}
	targetManifest, err := queryContent(
		ctx,
		q,
		review.Review.Target.ManifestRef,
	)
	if err != nil || targetManifest.Kind != ContentModuleManifest ||
		targetManifest.MediaType != moduleManifestMediaType {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: target Manifest evidence differs: %v",
			ErrModuleUpgradeIntegrity,
			err,
		)
	}
	historicalControl, historicalCatalog, err := loadAdmissionControlCatalog(
		ctx,
		q,
		input.TenantID,
		review.Review.PublishedBasis.Control.SnapshotID,
		review.Review.PublishedBasis.Catalog.GenerationID,
	)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: historical publication: %v",
			ErrModuleUpgradeIntegrity,
			err,
		)
	}
	selectedImpact, found := selectedModuleUpgradeImpact(review.Review)
	if !found {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: selected Binding impact is absent",
			ErrModuleUpgradeIntegrity,
		)
	}
	bindingConfig, err := queryContent(ctx, q, selectedImpact.ConfigRef)
	if err != nil || bindingConfig.Kind != ContentConfig {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: selected Binding Config differs: %v",
			ErrModuleUpgradeIntegrity,
			err,
		)
	}
	authority, err := queryContent(ctx, q, selectedImpact.AuthorityCeilingRef)
	if err != nil || authority.Kind != ContentAuthorityCeiling {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: selected Binding authority differs: %v",
			ErrModuleUpgradeIntegrity,
			err,
		)
	}
	activation, err := queryModuleActivationByIdentity(
		ctx,
		q,
		input.TenantID,
		review.Review.Current.Activation.InstanceID,
		review.Review.Current.Activation.ActivationRevision,
	)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: historical Activation differs: %v",
			ErrModuleUpgradeIntegrity,
			err,
		)
	}
	installation, err := queryModuleInstallationByID(
		ctx,
		q,
		activation.InstallationID,
	)
	if err != nil {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: historical Installation differs: %v",
			ErrModuleUpgradeIntegrity,
			err,
		)
	}
	if activation.InstallationID != review.Review.Current.InstallationID ||
		installation.ManifestRef != review.Review.Current.ManifestRef ||
		activation.ExecutionClass != review.Review.Current.Activation.ExecutionClass ||
		activation.AdapterIdentity != review.Review.Current.Activation.AdapterIdentity {
		return ModuleUpgradeApplyApproval{}, fmt.Errorf(
			"%w: historical Activation/Installation projection differs",
			ErrModuleUpgradeIntegrity,
		)
	}

	return ModuleUpgradeApplyApproval{
		Input:               input,
		Review:              review,
		Decision:            decision,
		Source:              source,
		PublisherKey:        publisher,
		Snapshot:            snapshot,
		TargetEntry:         targetEntry,
		TargetManifest:      targetManifest,
		HistoricalControl:   historicalControl,
		HistoricalCatalog:   historicalCatalog,
		SelectedImpact:      selectedImpact,
		BindingConfig:       bindingConfig,
		AuthorityCeiling:    authority,
		CurrentActivation:   activation,
		CurrentInstallation: installation,
	}, nil
}

func selectedModuleUpgradeImpact(
	review moduleupgrade.ReviewV1,
) (moduleupgrade.BindingImpactV1, bool) {
	for _, impact := range review.BindingImpacts {
		if impact.BindingTarget == review.BindingTarget &&
			impact.Port == review.Port &&
			impact.PortBindingIndex == review.PortBindingIndex {
			return cloneUpgradeImpacts([]moduleupgrade.BindingImpactV1{impact})[0], true
		}
	}
	return moduleupgrade.BindingImpactV1{}, false
}

func detachModuleUpgradeApplyApproval(
	value ModuleUpgradeApplyApproval,
) ModuleUpgradeApplyApproval {
	value.Review = detachModuleUpgradeReviewRecord(value.Review)
	value.Decision = detachModuleCandidateDecisionRecord(value.Decision)
	value.Source = detachModuleSource(value.Source)
	if value.PublisherKey != nil {
		key := detachModulePublisherKey(*value.PublisherKey)
		value.PublisherKey = &key
	}
	value.Snapshot = detachModuleDiscoverySnapshot(value.Snapshot)
	value.TargetManifest = cloneContentRecord(value.TargetManifest)
	value.HistoricalControl = cloneModuleUpgradeApplyControl(value.HistoricalControl)
	value.HistoricalCatalog = cloneModuleUpgradeApplyCatalog(value.HistoricalCatalog)
	value.SelectedImpact = cloneUpgradeImpacts(
		[]moduleupgrade.BindingImpactV1{value.SelectedImpact},
	)[0]
	value.BindingConfig = cloneContentRecord(value.BindingConfig)
	value.AuthorityCeiling = cloneContentRecord(value.AuthorityCeiling)
	value.CurrentInstallation = cloneModuleInstallation(value.CurrentInstallation)
	return value
}

// These projection-local clones deliberately mirror the complete mutable
// shape of controlcontract's frozen values without exporting a general Core
// clone API. Historical Control/Catalog values have already passed restore and
// semantic verification, so copying cannot fail or recanonicalize identity.
func cloneModuleUpgradeApplyControl(
	value controlcontract.ControlSnapshot,
) controlcontract.ControlSnapshot {
	value.Agents = cloneModuleUpgradeApplySlice(value.Agents)
	if value.CompositeAgents != nil {
		definitions := make(
			[]controlcontract.CompositeAgentDefinitionV1,
			len(value.CompositeAgents),
		)
		for index, definition := range value.CompositeAgents {
			definition.Members = cloneModuleUpgradeApplySlice(definition.Members)
			if definition.Reviewer != nil {
				reviewer := *definition.Reviewer
				definition.Reviewer = &reviewer
			}
			if definition.Decision != nil {
				decision := *definition.Decision
				definition.Decision = &decision
			}
			definitions[index] = definition
		}
		value.CompositeAgents = definitions
	}
	value.Profiles = cloneModuleUpgradeApplySlice(value.Profiles)
	for profileIndex := range value.Profiles {
		profile := &value.Profiles[profileIndex]
		if profile.ModelProfile != nil {
			modelProfile := *profile.ModelProfile
			profile.ModelProfile = &modelProfile
		}
		profile.Bindings = cloneModuleUpgradeApplySlice(profile.Bindings)
		for bindingIndex := range profile.Bindings {
			profile.Bindings[bindingIndex].StaticContextRefs =
				cloneModuleUpgradeApplySlice(
					profile.Bindings[bindingIndex].StaticContextRefs,
				)
		}
	}
	value.Workspaces = cloneModuleUpgradeApplySlice(value.Workspaces)
	for workspaceIndex := range value.Workspaces {
		workspace := &value.Workspaces[workspaceIndex]
		workspace.ChannelEndpoints = cloneModuleUpgradeApplySlice(
			workspace.ChannelEndpoints,
		)
		for endpointIndex := range workspace.ChannelEndpoints {
			binding := &workspace.ChannelEndpoints[endpointIndex].Binding
			binding.StaticContextRefs = cloneModuleUpgradeApplySlice(
				binding.StaticContextRefs,
			)
		}
		workspace.ChannelIdentities = cloneModuleUpgradeApplySlice(
			workspace.ChannelIdentities,
		)
		workspace.TransferGrants = cloneModuleUpgradeApplySlice(
			workspace.TransferGrants,
		)
		for grantIndex := range workspace.TransferGrants {
			grant := &workspace.TransferGrants[grantIndex]
			grant.SendPayloadKinds = cloneModuleUpgradeApplySlice(
				grant.SendPayloadKinds,
			)
			grant.ReceivePayloadKinds = cloneModuleUpgradeApplySlice(
				grant.ReceivePayloadKinds,
			)
		}
	}
	return value
}

func cloneModuleUpgradeApplyCatalog(
	value controlcontract.CatalogGeneration,
) controlcontract.CatalogGeneration {
	value.Entries = cloneModuleUpgradeApplySlice(value.Entries)
	for index := range value.Entries {
		value.Entries[index].Provides = cloneModuleUpgradeApplySlice(
			value.Entries[index].Provides,
		)
	}
	return value
}

func cloneModuleUpgradeApplySlice[T any](value []T) []T {
	if value == nil {
		return nil
	}
	cloned := make([]T, len(value))
	copy(cloned, value)
	return cloned
}

// equalModuleUpgradeApplyApproval is intentionally unexported. It gives the
// Operator Apply service an exact in-package assertion for tests without
// creating another persisted receipt, authority source, or public equality
// contract.
func equalModuleUpgradeApplyApproval(
	left ModuleUpgradeApplyApproval,
	right ModuleUpgradeApplyApproval,
) bool {
	return reflect.DeepEqual(
		detachModuleUpgradeApplyApproval(left),
		detachModuleUpgradeApplyApproval(right),
	)
}
