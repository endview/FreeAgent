package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// validateGovernedKnowledgeApplyClosureV1 is the shared pre-publication gate
// for the E5-A Knowledge provider and the Context side of exact Document
// Insight. The proposed Binding does not alter model.generate/v2, so its
// candidate dependency is the exact Model Binding already frozen in the same
// target Profile and Catalog. The function derives no second graph or grant.
func validateGovernedKnowledgeApplyClosureV1(
	ctx context.Context,
	view moduleApplyReadViewV1,
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	if ctx == nil || view == nil {
		return errors.New("governed Knowledge dependency reader is absent")
	}
	if plan.BindingTarget.Kind != moduleApplyBindingTargetProfileV1 {
		return errors.New("governed Knowledge requires an exact PROFILE Binding target")
	}
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil {
		return err
	}
	if policy.HandlerKind != moduleApplyHandlerKnowledgeContextV1 &&
		!(policy.HandlerKind == moduleApplyHandlerDocumentInsightV1 &&
			policy.Port == productionContextPort) {
		return errors.New("governed Knowledge manifest is not bound through the Knowledge handler")
	}
	if err := validateGovernedKnowledgeReadGrantV1(plan, control); err != nil {
		return err
	}

	profile, found := findModuleApplyProfileV1(
		control,
		plan.BindingTarget.ProfileID,
	)
	if !found {
		return errors.New("governed Knowledge target Profile is absent")
	}
	modelBindings := make([]controlcontract.BindingSpec, 0, 1)
	for _, binding := range profile.Bindings {
		if binding.Port == productionModelPort {
			modelBindings = append(modelBindings, binding)
		}
	}
	switch len(modelBindings) {
	case 0:
		for _, other := range control.Profiles {
			if other.Profile.ID == profile.Profile.ID {
				continue
			}
			for _, binding := range other.Bindings {
				if binding.Port == productionModelPort {
					return errors.New(
						"governed Knowledge has a cross-Profile Require for model.generate/v2",
					)
				}
			}
		}
		return errors.New(
			"governed Knowledge is missing exact same-Profile Require model.generate/v2",
		)
	case 1:
	default:
		return fmt.Errorf(
			"governed Knowledge has ambiguous exact same-Profile Require model.generate/v2 (%d Bindings)",
			len(modelBindings),
		)
	}
	if err := validateModuleApplyPortPlanV1(
		profile,
		catalog,
		productionModelPort,
	); err != nil {
		return fmt.Errorf(
			"governed Knowledge model.generate/v2 dependency PortPlan: %w",
			err,
		)
	}
	modelBinding := modelBindings[0]
	entry, found := catalog.FindInstance(modelBinding.InstanceID)
	if !found || entry.Activation.InstanceID != modelBinding.InstanceID ||
		len(entry.Provides) != 1 || entry.Provides[0] != productionModelPort {
		return errors.New(
			"governed Knowledge model.generate/v2 dependency does not close one exact Catalog provider",
		)
	}
	activation, err := view.GetModuleActivationByIdentity(
		ctx,
		plan.TenantID,
		modelBinding.InstanceID,
		entry.Activation.ActivationRevision,
	)
	if err != nil {
		return fmt.Errorf(
			"governed Knowledge model.generate/v2 dependency Activation: %w",
			err,
		)
	}
	if activation.TenantID != plan.TenantID ||
		activation.InstanceID != entry.Activation.InstanceID ||
		activation.ActivationRevision != entry.Activation.ActivationRevision ||
		activation.ExecutionClass != entry.Activation.ExecutionClass ||
		activation.AdapterIdentity != entry.Activation.AdapterIdentity {
		return errors.New(
			"governed Knowledge model.generate/v2 Catalog and exact Activation differ",
		)
	}
	installation, err := view.GetModuleInstallationByIdentity(
		ctx,
		entry.Activation.ModuleID,
		entry.Activation.Version,
	)
	if err != nil {
		return fmt.Errorf(
			"governed Knowledge model.generate/v2 dependency Installation: %w",
			err,
		)
	}
	if activation.InstallationID != installation.InstallationID ||
		installation.ModuleID != entry.Activation.ModuleID ||
		installation.ExactVersion != entry.Activation.Version ||
		installation.ArtifactDigest != entry.Activation.ArtifactDigest {
		return errors.New(
			"governed Knowledge model.generate/v2 Activation and Installation differ",
		)
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(
		installation.ManifestBytes,
	)
	if err != nil || !bytes.Equal(canonical, installation.ManifestBytes) ||
		manifest.ID != installation.ModuleID ||
		manifest.Version != installation.ExactVersion ||
		!moduleOperatorSameExactPortSetV1(manifest.Provides, entry.Provides) ||
		!moduleOperatorPortPresentV1(manifest.Provides, productionModelPort) ||
		!moduleOperatorExecutionMatchesRuntimeV1(
			entry.Activation.ExecutionClass,
			manifest.Runtime.Mode,
		) {
		return fmt.Errorf(
			"governed Knowledge model.generate/v2 installed Manifest closure: %w",
			errors.Join(err, errors.New("identity, runtime, or Provides differ")),
		)
	}
	return nil
}

func validateGovernedKnowledgeReadGrantV1(
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
) error {
	if plan.Binding == nil {
		return errors.New("governed Knowledge Binding grant is absent")
	}
	if plan.TenantID != control.TenantID {
		return errors.New("governed Knowledge Tenant grant is outside Control")
	}
	// This is the existing Apply Binding-policy proof: exact Knowledge Config,
	// authority schema/source, limits, Tenant and referenced scope identities.
	if err := validateModuleApplyAuthorityV1(plan, control); err != nil {
		return fmt.Errorf("governed knowledge.read grant: %w", err)
	}
	contextConfig, err := moduleapi.RestoreContextBindingConfigV1(
		plan.Binding.Config,
	)
	if err != nil {
		return fmt.Errorf("governed knowledge.read Config: %w", err)
	}
	config, err := moduleapi.RestoreKnowledgeContextBindingParametersV1(
		contextConfig,
	)
	if err != nil {
		return fmt.Errorf("governed knowledge.read Config: %w", err)
	}
	authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(
		plan.Binding.AuthorityCeiling,
	)
	if err != nil {
		return fmt.Errorf("governed knowledge.read authority: %w", err)
	}
	if config.Source != authority.Source {
		return errors.New("governed knowledge.read Config and authority lack one exact source")
	}
	if err := config.Source.Validate(); err != nil {
		return fmt.Errorf("governed knowledge.read source is not exact: %w", err)
	}
	for index, scope := range authority.AllowedScopes {
		if scope.TenantID != control.TenantID {
			return fmt.Errorf(
				"governed knowledge.read scope %d Tenant is outside Control",
				index,
			)
		}
		if scope.WorkspaceID == "*" {
			if len(control.Workspaces) == 0 {
				return fmt.Errorf(
					"governed knowledge.read scope %d Workspace is outside Control",
					index,
				)
			}
		} else if _, found := control.FindWorkspace(scope.WorkspaceID); !found {
			return fmt.Errorf(
				"governed knowledge.read scope %d Workspace is outside Control",
				index,
			)
		}
		if scope.AgentID == "*" {
			if len(control.Agents) == 0 {
				return fmt.Errorf(
					"governed knowledge.read scope %d Agent is outside Control",
					index,
				)
			}
		} else if _, found := control.FindAgent(scope.AgentID); !found {
			return fmt.Errorf(
				"governed knowledge.read scope %d Agent is outside Control",
				index,
			)
		}
	}
	return nil
}

// resolveDocumentInsightContextClosureV1 proves the prerequisite that makes a
// second, Action-port Binding safe: the same Profile already owns one exact
// governed Knowledge Context Binding for the same Document Insight instance.
// The proof follows Binding -> Catalog -> Activation -> Installation ->
// Manifest and reuses the ordinary governed Knowledge grant and Model Require
// checks. It is read-only and is called before Apply creates TEMP/stage state.
func resolveDocumentInsightContextClosureV1(
	ctx context.Context,
	view moduleApplyReadViewV1,
	actionPlan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (moduleapi.KnowledgeSourceRefV1, error) {
	if ctx == nil || view == nil {
		return moduleapi.KnowledgeSourceRefV1{}, errors.New(
			"Document Insight Context prerequisite reader is absent",
		)
	}
	if actionPlan.Module == nil || actionPlan.Binding == nil ||
		actionPlan.BindingTarget.Kind != moduleApplyBindingTargetProfileV1 ||
		actionPlan.Port != productionActionPort {
		return moduleapi.KnowledgeSourceRefV1{}, errors.New(
			"Document Insight Action requires an exact PROFILE plan",
		)
	}
	policy, err := resolveEnabledModuleApplyPolicyV1(actionPlan)
	if err != nil || policy.HandlerKind != moduleApplyHandlerDocumentInsightV1 {
		return moduleapi.KnowledgeSourceRefV1{}, errors.Join(
			err,
			errors.New("Document Insight Action is not bound through its exact handler"),
		)
	}
	profile, found := findModuleApplyProfileV1(
		control,
		actionPlan.BindingTarget.ProfileID,
	)
	if !found {
		return moduleapi.KnowledgeSourceRefV1{}, errors.New(
			"Document Insight Action target Profile is absent",
		)
	}

	type contextMatchV1 struct {
		binding controlcontract.BindingSpec
		ordinal uint32
	}
	matches := make([]contextMatchV1, 0, 1)
	var contextOrdinal uint32
	for _, binding := range profile.Bindings {
		if binding.Port != productionContextPort {
			continue
		}
		if binding.InstanceID == actionPlan.InstanceID {
			matches = append(matches, contextMatchV1{
				binding: binding,
				ordinal: contextOrdinal,
			})
		}
		contextOrdinal++
	}
	if len(matches) != 1 {
		return moduleapi.KnowledgeSourceRefV1{}, fmt.Errorf(
			"Document Insight Action requires one exact same-Profile Context Binding; found %d",
			len(matches),
		)
	}
	match := matches[0]
	if len(match.binding.StaticContextRefs) != 0 {
		return moduleapi.KnowledgeSourceRefV1{}, errors.New(
			"Document Insight governed Context Binding has unexpected static Context refs",
		)
	}
	configRecord, err := view.GetContent(ctx, match.binding.ConfigRef)
	if err != nil || configRecord.Kind != currentstore.ContentConfig ||
		configRecord.MediaType != moduleApplyJSONMediaType {
		return moduleapi.KnowledgeSourceRefV1{}, errors.Join(
			err,
			errors.New("Document Insight Context Config content is unavailable"),
		)
	}
	authorityRecord, err := view.GetContent(
		ctx,
		match.binding.AuthorityCeilingRef,
	)
	if err != nil || authorityRecord.Kind != currentstore.ContentAuthorityCeiling ||
		authorityRecord.MediaType != moduleApplyJSONMediaType {
		return moduleapi.KnowledgeSourceRefV1{}, errors.Join(
			err,
			errors.New("Document Insight Context authority content is unavailable"),
		)
	}
	configRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentConfig,
		moduleApplyJSONMediaType,
		configRecord.CanonicalBytes,
	)
	if err != nil || configRef != match.binding.ConfigRef {
		return moduleapi.KnowledgeSourceRefV1{}, errors.Join(
			err,
			errors.New("Document Insight Context Config ref differs from its bytes"),
		)
	}
	authorityRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentAuthorityCeiling,
		moduleApplyJSONMediaType,
		authorityRecord.CanonicalBytes,
	)
	if err != nil || authorityRef != match.binding.AuthorityCeilingRef {
		return moduleapi.KnowledgeSourceRefV1{}, errors.Join(
			err,
			errors.New("Document Insight Context authority ref differs from its bytes"),
		)
	}

	contextPlan := actionPlan
	contextPlan.Port = productionContextPort
	contextPlan.Binding = &moduleApplyBindingV1{
		PortBindingIndex: match.ordinal,
		Config:           bytes.Clone(configRecord.CanonicalBytes),
		AuthorityCeiling: bytes.Clone(authorityRecord.CanonicalBytes),
		FailurePolicy:    match.binding.FailurePolicy,
	}
	entry, found := catalog.FindInstance(actionPlan.InstanceID)
	if !found || !moduleApplyCatalogEntryMatchesV1(entry, contextPlan) {
		return moduleapi.KnowledgeSourceRefV1{}, errors.New(
			"Document Insight Context does not close the exact dual-Port Catalog entry",
		)
	}
	activation, err := view.GetModuleActivationByIdentity(
		ctx,
		actionPlan.TenantID,
		entry.Activation.InstanceID,
		entry.Activation.ActivationRevision,
	)
	if err != nil || activation.TenantID != actionPlan.TenantID ||
		activation.InstanceID != entry.Activation.InstanceID ||
		activation.ActivationRevision != entry.Activation.ActivationRevision ||
		activation.ExecutionClass != entry.Activation.ExecutionClass ||
		activation.AdapterIdentity != entry.Activation.AdapterIdentity {
		return moduleapi.KnowledgeSourceRefV1{}, errors.Join(
			err,
			errors.New("Document Insight Context Catalog and Activation differ"),
		)
	}
	installation, err := view.GetModuleInstallationByIdentity(
		ctx,
		entry.Activation.ModuleID,
		entry.Activation.Version,
	)
	if err != nil || activation.InstallationID != installation.InstallationID ||
		installation.ModuleID != entry.Activation.ModuleID ||
		installation.ExactVersion != entry.Activation.Version ||
		installation.ArtifactDigest != entry.Activation.ArtifactDigest {
		return moduleapi.KnowledgeSourceRefV1{}, errors.Join(
			err,
			errors.New("Document Insight Context Activation and Installation differ"),
		)
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(
		installation.ManifestBytes,
	)
	if err != nil || !bytes.Equal(canonical, installation.ManifestBytes) ||
		manifest.ID != installation.ModuleID ||
		manifest.Version != installation.ExactVersion {
		return moduleapi.KnowledgeSourceRefV1{}, errors.Join(
			err,
			errors.New("Document Insight installed Manifest identity differs"),
		)
	}
	if err := classifyExactDocumentInsightManifestV1(manifest); err != nil {
		return moduleapi.KnowledgeSourceRefV1{}, fmt.Errorf(
			"Document Insight installed Manifest: %w",
			err,
		)
	}
	if err := validateGovernedKnowledgeApplyClosureV1(
		ctx,
		view,
		contextPlan,
		control,
		catalog,
	); err != nil {
		return moduleapi.KnowledgeSourceRefV1{}, fmt.Errorf(
			"Document Insight existing Context grant: %w",
			err,
		)
	}
	contextConfig, err := moduleapi.RestoreContextBindingConfigV1(
		contextPlan.Binding.Config,
	)
	if err != nil {
		return moduleapi.KnowledgeSourceRefV1{}, err
	}
	knowledge, err := moduleapi.RestoreKnowledgeContextBindingParametersV1(
		contextConfig,
	)
	if err != nil {
		return moduleapi.KnowledgeSourceRefV1{}, err
	}
	return knowledge.Source, nil
}
