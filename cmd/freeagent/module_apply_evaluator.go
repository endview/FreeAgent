package main

import (
	"bytes"
	"context"
	"errors"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/sdk/moduleapi"
)

// moduleApplyReadViewV1 is the narrow immutable fact surface shared by the
// writer-owned Apply observation and the offline ReadOnlyObserver dry-run.
// It deliberately contains no mutation, recovery, lease, SQL or publication
// capability.
type moduleApplyReadViewV1 interface {
	LoadPublishedBasis(
		context.Context,
		string,
	) (
		controlcontract.PublishedBasis,
		controlcontract.ControlSnapshot,
		controlcontract.CatalogGeneration,
		error,
	)
	VerifyPublishedControlCatalogClosureV1(
		context.Context,
		controlcontract.ControlSnapshot,
		controlcontract.CatalogGeneration,
	) error
	GetContent(context.Context, string) (currentstore.ContentRecord, error)
	GetModelPriceSnapshot(
		context.Context,
		string,
	) (currentstore.ModelPriceSnapshotRecord, error)
	GetChannelCursorSeed(
		context.Context,
		string,
		string,
		string,
	) (currentstore.ChannelIngressReceipt, error)
	GetModuleInstallationByIdentity(
		context.Context,
		string,
		string,
	) (currentstore.ModuleInstallation, error)
	GetModuleActivationByIdentity(
		context.Context,
		string,
		string,
		uint64,
	) (currentstore.ModuleActivation, error)
	GetLatestModuleActivationForInstance(
		context.Context,
		string,
		string,
	) (currentstore.ModuleActivation, error)
	LoadControlCatalogRevision(
		context.Context,
		string,
		uint64,
		uint64,
	) (
		controlcontract.ControlSnapshot,
		controlcontract.CatalogGeneration,
		error,
	)
}

type moduleApplyObservedEvaluationV1 struct {
	Status         moduleApplyStatusV1
	NeedsCandidate bool
}

// evaluateObservedModuleApplyV1 preserves the exact current-state and retry
// decisions used by Apply. It reads verified immutable facts but performs no
// recovery, filesystem mutation, Store mutation or publication.
func evaluateObservedModuleApplyV1(
	ctx context.Context,
	view moduleApplyReadViewV1,
	artifactRoot string,
	plan moduleApplyPlanV1,
	planDigest string,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (moduleApplyObservedEvaluationV1, error) {
	if plan.DesiredState == moduleApplyDisabledV1 {
		return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
			moduleApplyFailureInternal,
			errors.New("DISABLED plans must use the shared MODULE_DISABLE evaluator"),
		)
	}
	candidateIDs, err := moduleapplyplan.DeriveCandidateIDsV1(planDigest)
	if err != nil {
		return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.Join(
				err,
				errors.New("module Apply candidate identity is invalid"),
			),
		)
	}
	if err := view.VerifyPublishedControlCatalogClosureV1(
		ctx,
		control,
		catalog,
	); err != nil {
		return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
			moduleApplyFailureStore,
			errors.Join(
				err,
				errors.New("current publication semantic closure is invalid"),
			),
		)
	}
	wantedSnapshotID := candidateIDs.ControlSnapshotID
	wantedCatalogID := candidateIDs.CatalogGenerationID
	if basis.PointerRevision == plan.ExpectedPointerRevision+1 {
		if basis.Control.SnapshotID != wantedSnapshotID ||
			basis.Catalog.GenerationID != wantedCatalogID {
			return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
				moduleApplyFailurePointer,
				errors.New("next pointer belongs to another plan"),
			)
		}
		if plan.ReplaceCurrentInstanceID != "" {
			state, stateErr := inspectModuleApplyReplacementStateV1(
				plan,
				control,
				catalog,
			)
			if stateErr != nil || state != moduleApplyReplacementTargetV1 {
				return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
					moduleApplyFailureTarget,
					errors.Join(
						stateErr,
						errors.New("published replacement target is not exact"),
					),
				)
			}
		}
		exact, conflict, inspectErr := inspectCurrentModuleApplyStateV1(
			ctx,
			view,
			artifactRoot,
			plan,
			control,
			catalog,
		)
		if inspectErr != nil {
			return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
				moduleApplyFailureStore,
				inspectErr,
			)
		}
		if !exact || conflict {
			return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
				moduleApplyFailureTarget,
				errors.New("published retry does not match desired state"),
			)
		}
		if err := verifyEnabledModuleApplySemanticsV1(
			ctx,
			view,
			artifactRoot,
			plan,
			control,
			catalog,
		); err != nil {
			return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
				moduleApplyFailureTarget,
				err,
			)
		}
		if err := verifyExactModuleApplyRetryV1(
			ctx,
			view,
			artifactRoot,
			plan,
			planDigest,
			basis,
			control,
			catalog,
		); err != nil {
			return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
				moduleApplyFailureTarget,
				errors.Join(
					err,
					errors.New("published retry canonical bytes differ from plan"),
				),
			)
		}
		return moduleApplyObservedEvaluationV1{
			Status: moduleApplyStatusAlreadyApplied,
		}, nil
	}
	if basis.PointerRevision != plan.ExpectedPointerRevision {
		return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
			moduleApplyFailurePointer,
			errors.New("published pointer differs from expected revision"),
		)
	}
	if plan.ReplaceCurrentInstanceID != "" {
		state, stateErr := inspectModuleApplyReplacementStateV1(
			plan,
			control,
			catalog,
		)
		if stateErr != nil || state != moduleApplyReplacementCurrentV1 {
			return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
				moduleApplyFailureTarget,
				errors.Join(
					stateErr,
					errors.New("replacement current coordinate is not exact"),
				),
			)
		}
	}

	exact, conflict, err := inspectCurrentModuleApplyStateV1(
		ctx,
		view,
		artifactRoot,
		plan,
		control,
		catalog,
	)
	if err != nil {
		return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
			moduleApplyFailureStore,
			err,
		)
	}
	if conflict {
		return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
			moduleApplyFailureTarget,
			errors.New("current target conflicts with plan"),
		)
	}
	if exact {
		if err := verifyEnabledModuleApplySemanticsV1(
			ctx,
			view,
			artifactRoot,
			plan,
			control,
			catalog,
		); err != nil {
			return moduleApplyObservedEvaluationV1{}, newModuleApplyFailureV1(
				moduleApplyFailureTarget,
				err,
			)
		}
		return moduleApplyObservedEvaluationV1{
			Status: moduleApplyStatusNoChange,
		}, nil
	}
	return moduleApplyObservedEvaluationV1{
		Status:         moduleApplyStatusWouldApply,
		NeedsCandidate: true,
	}, nil
}

type moduleApplyPreparedEnabledV1 struct {
	Publication       currentstore.PublishControlCatalogInput
	Candidate         moduleApplyCandidateV1
	Installation      currentstore.ModuleInstallation
	InstallMissing    bool
	ActivationInput   currentstore.ActivateModuleInput
	ActivateMissing   bool
	Provider          moduleapi.ActivatedModuleRef
	ConfigRef         string
	AuthorityRef      string
	StaticContextRefs []string
	Contents          []currentstore.ContentInput
}

// evaluateEnabledModuleCandidateV1 freezes the exact candidate publication
// and immutable intents from already-observed Store facts and an already
// prepared TEMP/stage artifact. It never writes the artifact root or Store.
func evaluateEnabledModuleCandidateV1(
	ctx context.Context,
	view moduleApplyReadViewV1,
	input moduleApplyCommandInputV1,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	candidate moduleApplyCandidateV1,
) (moduleApplyPreparedEnabledV1, error) {
	plan := input.Plan
	if plan.Module == nil || plan.Binding == nil {
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.New("enabled plan payload is incomplete"),
		)
	}
	manifest := candidate.ManifestCanonical
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		moduleApplyJSONMediaType,
		manifest,
	)
	if err != nil {
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailureArtifact,
			err,
		)
	}
	installation, installMissing, err := resolveModuleApplyInstallationV1(
		ctx,
		view,
		*plan.Module,
		manifest,
		manifestRef,
	)
	if err != nil {
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailureTarget,
			err,
		)
	}
	provider, activationInput, activateMissing, err := resolveModuleApplyActivationV1(
		ctx,
		view,
		candidate.Resolver,
		plan,
		installation,
		catalog,
	)
	if err != nil {
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailureTarget,
			err,
		)
	}
	configRef, authorityRef, err := moduleApplyContentRefsV1(*plan.Binding)
	if err != nil {
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			err,
		)
	}
	staticContextRefs := moduleApplyCandidateStaticRefsV1(candidate)
	control, catalog, err = addEnabledModuleBindingV1(
		ctx,
		view,
		plan,
		control,
		catalog,
		provider,
		configRef,
		authorityRef,
		staticContextRefs,
	)
	if err != nil {
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailureTarget,
			err,
		)
	}
	publication, err := buildModuleApplyPublicationV1(
		input.PlanDigest,
		basis,
		control,
		catalog,
	)
	if err != nil {
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailureTarget,
			err,
		)
	}
	contents := []currentstore.ContentInput{
		{
			Digest:         configRef,
			Kind:           currentstore.ContentConfig,
			MediaType:      moduleApplyJSONMediaType,
			CanonicalBytes: bytes.Clone(plan.Binding.Config),
		},
		{
			Digest:         authorityRef,
			Kind:           currentstore.ContentAuthorityCeiling,
			MediaType:      moduleApplyJSONMediaType,
			CanonicalBytes: bytes.Clone(plan.Binding.AuthorityCeiling),
		},
	}
	modelProfileRef, modelProfileCanonical, err := moduleApplyPlannedModelProfileV1(
		plan,
		provider,
	)
	if err != nil {
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			err,
		)
	}
	if modelProfileRef != nil {
		contents = append(contents, currentstore.ContentInput{
			Digest:         modelProfileRef.Digest,
			Kind:           currentstore.ContentConfig,
			MediaType:      moduleApplyJSONMediaType,
			CanonicalBytes: modelProfileCanonical,
		})
	}
	if candidate.StaticContextRef != "" {
		contents = append(contents, currentstore.ContentInput{
			Digest:         candidate.StaticContextRef,
			Kind:           currentstore.ContentStaticContext,
			MediaType:      moduleApplyJSONMediaType,
			CanonicalBytes: bytes.Clone(candidate.StaticContextCanonical),
		})
	}
	return moduleApplyPreparedEnabledV1{
		Publication:       publication,
		Candidate:         candidate,
		Installation:      installation,
		InstallMissing:    installMissing,
		ActivationInput:   activationInput,
		ActivateMissing:   activateMissing,
		Provider:          provider,
		ConfigRef:         configRef,
		AuthorityRef:      authorityRef,
		StaticContextRefs: append([]string{}, staticContextRefs...),
		Contents:          contents,
	}, nil
}

func newModuleApplyCandidateBasisV1(
	tenantID string,
	publication currentstore.PublishControlCatalogInput,
) (controlcontract.PublishedBasis, error) {
	basis := controlcontract.PublishedBasis{
		TenantID:        tenantID,
		PointerRevision: publication.NewPointerRevision,
		Control:         publication.ControlRef,
		Catalog:         publication.CatalogRef,
	}
	if err := basis.Validate(); err != nil {
		return controlcontract.PublishedBasis{}, err
	}
	return basis, nil
}

// Keep the dependency explicit at compile time; both concrete readers must
// satisfy the same immutable observation surface.
var _ moduleApplyReadViewV1 = (*currentstore.Store)(nil)
var _ moduleApplyReadViewV1 = (*currentstore.ReadOnlyObserver)(nil)

// validateEnabledModuleApplyInputV1 is the shared, read-only source artifact
// preflight. Callers invoke it only after observed-state evaluation says a
// candidate is required, preserving Apply's NO_CHANGE/ALREADY source-path
// behavior.
func validateEnabledModuleApplyInputV1(
	ctx context.Context,
	view moduleApplyReadViewV1,
	input moduleApplyCommandInputV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	plan := input.Plan
	if plan.Module == nil || plan.Binding == nil {
		return newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.New("enabled plan payload is incomplete"),
		)
	}
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil {
		return newModuleApplyFailureV1(moduleApplyFailurePlanInvalid, err)
	}
	if grantErr := validateModuleApplyArtifactGrantsV1(
		policy,
		plan.Module.ArtifactDigest,
		input.LocalMCPArtifactGrant,
		input.TrustedInProcessArtifactGrant,
		input.RemoteActionArtifactGrant,
		input.WASMActionArtifactGrant,
	); grantErr != nil {
		return newModuleApplyFailureV1(
			moduleApplyArtifactGrantFailureCodeV1(grantErr),
			grantErr,
		)
	}
	if grantErr := validateModuleApplyRemoteActionGrantsV1(
		plan,
		policy,
		input.RemoteActionEndpointGrant,
		input.RemoteActionSecretRefGrant,
	); grantErr != nil {
		return newModuleApplyFailureV1(
			moduleApplyRemoteGrantFailureCodeV1(grantErr),
			grantErr,
		)
	}
	if grantErr := validateModuleApplyModelSecretGrantV1(
		plan,
		input.ModelSecretRefGrant,
	); grantErr != nil {
		return newModuleApplyFailureV1(
			moduleApplyFailureGrantRequired,
			grantErr,
		)
	}
	if err := validateModuleApplyAuthorityV1(plan, control); err != nil {
		return newModuleApplyFailureV1(moduleApplyFailureTarget, err)
	}
	if err := validateModuleApplyModelStoreClosureV1(
		ctx,
		view,
		plan,
		control,
	); err != nil {
		return newModuleApplyFailureV1(moduleApplyFailureTarget, err)
	}
	if policy.HandlerKind == moduleApplyHandlerDocumentInsightV1 &&
		policy.Port == productionActionPort {
		// Action is deliberately the second Binding. Prove the same-Profile
		// governed Context prerequisite before Dry-run creates TEMP or Apply
		// creates a stage/final artifact or writes immutable Store rows.
		if _, err := resolveDocumentInsightContextClosureV1(
			ctx,
			view,
			plan,
			control,
			catalog,
		); err != nil {
			return newModuleApplyFailureV1(moduleApplyFailureTarget, err)
		}
	}
	report, err := moduleconformance.VerifyDirectory(ctx, input.ArtifactDirectory)
	if err != nil {
		return newModuleApplyFailureV1(moduleApplyFailureArtifact, err)
	}
	provides := make([]moduleapi.PortRef, len(report.Provides))
	for index, port := range report.Provides {
		provides[index] = moduleapi.PortRef{
			Name:         port.Name,
			ExactVersion: port.ExactVersion,
		}
	}
	requires := make([]moduleapi.PortRef, len(report.Requires))
	for index, port := range report.Requires {
		requires[index] = moduleapi.PortRef{
			Name:         port.Name,
			ExactVersion: port.ExactVersion,
		}
	}
	permissions := make(
		[]moduleapi.Permission,
		len(report.RequestedPermissions),
	)
	for index, permission := range report.RequestedPermissions {
		permissions[index] = moduleapi.Permission(permission)
	}
	knowledgeShape, err := validateModuleApplyVerificationReportV1(
		plan,
		policy,
		report.Module.ID,
		report.Module.ExactVersion,
		report.ArtifactDigest,
		report.ArtifactSizeBytes,
		report.RuntimeRequest.Mode,
		report.RuntimeRequest.Protocol,
		report.RuntimeRequest.Entrypoint,
		provides,
		requires,
		permissions,
	)
	if err != nil {
		return newModuleApplyFailureV1(moduleApplyFailureArtifact, err)
	}
	if knowledgeShape == knowledgeManifestGovernedV1 {
		if err := validateGovernedKnowledgeApplyClosureV1(
			ctx,
			view,
			plan,
			control,
			catalog,
		); err != nil {
			return newModuleApplyFailureV1(moduleApplyFailureTarget, err)
		}
	}
	if policy.HandlerKind == moduleApplyHandlerDocumentInsightV1 {
		_, _, sourceRef, err := readModuleApplyDocumentInsightMetadataV1(
			ctx,
			input.ArtifactDirectory,
		)
		if err != nil {
			return newModuleApplyFailureV1(moduleApplyFailureArtifact, err)
		}
		switch policy.Port {
		case productionContextPort:
			if err := validateModuleApplyKnowledgeSourceV1(plan, sourceRef); err != nil {
				return newModuleApplyFailureV1(moduleApplyFailureTarget, err)
			}
			if err := validateGovernedKnowledgeApplyClosureV1(
				ctx,
				view,
				plan,
				control,
				catalog,
			); err != nil {
				return newModuleApplyFailureV1(moduleApplyFailureTarget, err)
			}
		case productionActionPort:
			expectedSource, err := resolveDocumentInsightContextClosureV1(
				ctx,
				view,
				plan,
				control,
				catalog,
			)
			if err != nil {
				return newModuleApplyFailureV1(moduleApplyFailureTarget, err)
			}
			if sourceRef != expectedSource {
				return newModuleApplyFailureV1(
					moduleApplyFailureTarget,
					errors.New(
						"Document Insight Action artifact source differs from the existing Context grant",
					),
				)
			}
		default:
			return newModuleApplyFailureV1(
				moduleApplyFailurePlanInvalid,
				errors.New("Document Insight handler Port is unsupported"),
			)
		}
	}
	return nil
}
