package moduledisabledryrun

import (
	"bytes"
	"context"
	"errors"
	"math"

	"github.com/endview/freeagent/internal/controlcontract"
)

// EvaluateV1 is the sole MODULE_DISABLE observation and projection state
// machine. It performs only immutable reads and calls an injected read-only
// artifact verifier. A final PublishedBasis reread prevents a live Control
// caller from returning a projection assembled across two publications.
func EvaluateV1(
	ctx context.Context,
	view ReadViewV1,
	verifyArtifacts ArtifactVerifierV1,
	input InputV1,
) (ResultV1, error) {
	if ctx == nil || nilInterfaceV1(view) || verifyArtifacts == nil {
		return ResultV1{}, newFailureV1(
			FailureInvalidInputV1,
			errors.New("module disable dry-run context, view, and verifier are required"),
		)
	}
	if err := ctx.Err(); err != nil {
		return ResultV1{}, newFailureV1(FailureCancelledV1, err)
	}
	if err := input.validate(); err != nil {
		return ResultV1{}, newFailureV1(FailureInvalidInputV1, err)
	}

	basis, control, catalog, err := view.LoadPublishedBasis(ctx, input.TenantID)
	if err != nil {
		return ResultV1{}, newFailureV1(FailureStoreInvalidV1, err)
	}
	if basis.TenantID != input.TenantID || control.TenantID != input.TenantID ||
		catalog.TenantID != input.TenantID {
		return ResultV1{}, newFailureV1(
			FailureStoreInvalidV1,
			errors.New("published module disable facts cross tenant boundaries"),
		)
	}
	excluded := lastReferenceInstanceV1(input, control)
	if err := verifyArtifacts(ctx, catalog, excluded); err != nil {
		return ResultV1{}, newFailureV1(FailureArtifactInvalidV1, err)
	}
	if err := view.VerifyPublishedControlCatalogClosureV1(ctx, control, catalog); err != nil {
		return ResultV1{}, newFailureV1(
			FailureStoreInvalidV1,
			errors.Join(err, errors.New("current publication semantic closure is invalid")),
		)
	}

	result, err := evaluateObservedV1(ctx, view, input, basis, control, catalog)
	if err != nil {
		return ResultV1{}, err
	}
	result.ExcludedInstanceID = excluded
	current, _, _, err := view.LoadPublishedBasis(ctx, input.TenantID)
	if err != nil {
		return ResultV1{}, newFailureV1(FailureStoreInvalidV1, err)
	}
	if current != basis {
		return ResultV1{}, newFailureV1(
			FailurePointerConflictV1,
			errors.New("PublishedBasis changed during module disable dry-run"),
		)
	}
	return cloneResultV1(result), nil
}

// EvaluateExactBasisV1 deterministically evaluates one already-restored,
// semantically verified historical basis. It performs no Store, artifact,
// filesystem, provider, clock, or network I/O. Callers remain responsible for
// restoring the supplied Control/Catalog canonical bytes and verifying their
// content/activation closure before calling this function.
//
// This entry point deliberately accepts only the exact precondition revision;
// adjacent-current retry recognition remains owned by EvaluateV1. Durable
// receipt and Backup verification use it to replay the original candidate
// from immutable predecessor facts without inventing a second evaluator.
func EvaluateExactBasisV1(
	input InputV1,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (ResultV1, error) {
	if err := input.validate(); err != nil {
		return ResultV1{}, newFailureV1(FailureInvalidInputV1, err)
	}
	if err := basis.Validate(); err != nil ||
		basis.TenantID != input.TenantID ||
		control.TenantID != input.TenantID ||
		catalog.TenantID != input.TenantID ||
		basis.Control.SnapshotID != control.SnapshotID ||
		basis.Control.Revision != control.Revision ||
		basis.Control.Digest != control.Digest ||
		basis.Catalog.GenerationID != catalog.GenerationID ||
		basis.Catalog.Generation != catalog.Generation ||
		basis.Catalog.Digest != catalog.Digest ||
		catalog.ControlSnapshotID != control.SnapshotID ||
		catalog.ControlSnapshotDigest != control.Digest {
		return ResultV1{}, newFailureV1(
			FailureStoreInvalidV1,
			errors.Join(err, errors.New("module disable exact basis closure differs")),
		)
	}
	if basis.PointerRevision != input.ExpectedPointerRevision {
		return ResultV1{}, newFailureV1(
			FailurePointerConflictV1,
			errors.New("PublishedBasis differs from the exact disable precondition"),
		)
	}

	exact, conflict := inspectCurrentV1(input, control, catalog)
	if conflict {
		return ResultV1{}, newFailureV1(
			FailureTargetConflictV1,
			errors.New("current module disable target conflicts with plan"),
		)
	}
	if exact {
		return cloneResultV1(ResultV1{
			Status:             StatusNoChangeV1,
			PreconditionBasis:  basis,
			ObservedBasis:      basis,
			CandidateBasis:     basis,
			BindingRemoval:     emptyBindingRemovalV1(input),
			CatalogChange:      CatalogChangeNoneV1,
			ExcludedInstanceID: lastReferenceInstanceV1(input, control),
		}), nil
	}

	binding := currentBindingRemovalV1(input, control)
	publication, err := preparePublicationV1(input, basis, control, catalog)
	if err != nil {
		var failure *FailureV1
		if errors.As(err, &failure) {
			return ResultV1{}, err
		}
		return ResultV1{}, newFailureV1(FailureInternalV1, err)
	}
	candidate := controlcontract.PublishedBasis{
		TenantID:        input.TenantID,
		PointerRevision: publication.NewPointerRevision,
		Control:         publication.ControlRef,
		Catalog:         publication.CatalogRef,
	}
	if err := candidate.Validate(); err != nil {
		return ResultV1{}, newFailureV1(FailureInternalV1, err)
	}
	_, observedHas := catalog.FindInstance(input.InstanceID)
	candidateCatalog, err := controlcontract.RestoreCatalogGeneration(
		publication.CatalogCanonical,
		publication.CatalogRef,
	)
	if err != nil {
		return ResultV1{}, newFailureV1(FailureInternalV1, err)
	}
	_, candidateHas := candidateCatalog.FindInstance(input.InstanceID)
	catalogChange := CatalogChangeNoneV1
	if observedHas && candidateHas {
		catalogChange = CatalogChangeRetainInstanceV1
	} else if observedHas && !candidateHas {
		catalogChange = CatalogChangeRemoveInstanceV1
	}
	return cloneResultV1(ResultV1{
		Status:             StatusWouldApplyV1,
		PreconditionBasis:  basis,
		ObservedBasis:      basis,
		CandidateBasis:     candidate,
		BindingRemoval:     binding,
		CatalogChange:      catalogChange,
		Publication:        &publication,
		ExcludedInstanceID: lastReferenceInstanceV1(input, control),
	}), nil
}

func evaluateObservedV1(
	ctx context.Context,
	view ReadViewV1,
	input InputV1,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (ResultV1, error) {
	if basis.PointerRevision == input.ExpectedPointerRevision+1 {
		if basis.Control.SnapshotID != input.CandidateControlSnapshotID ||
			basis.Catalog.GenerationID != input.CandidateCatalogGenerationID {
			return ResultV1{}, newFailureV1(
				FailurePointerConflictV1,
				errors.New("next pointer belongs to another plan"),
			)
		}
		exact, conflict := inspectCurrentV1(input, control, catalog)
		if !exact || conflict {
			return ResultV1{}, newFailureV1(
				FailureTargetConflictV1,
				errors.New("published module disable retry does not match desired state"),
			)
		}
		precondition, err := verifyExactRetryV1(
			ctx,
			view,
			input,
			basis,
			control,
			catalog,
		)
		if err != nil {
			var failure *FailureV1
			if errors.As(err, &failure) {
				return ResultV1{}, err
			}
			return ResultV1{}, newFailureV1(
				FailureTargetConflictV1,
				errors.Join(err, errors.New("published module disable retry differs from plan")),
			)
		}
		return ResultV1{
			Status:            StatusAlreadyAppliedV1,
			PreconditionBasis: precondition,
			ObservedBasis:     basis,
			CandidateBasis:    basis,
			BindingRemoval:    emptyBindingRemovalV1(input),
			CatalogChange:     CatalogChangeNoneV1,
		}, nil
	}
	if basis.PointerRevision != input.ExpectedPointerRevision {
		return ResultV1{}, newFailureV1(
			FailurePointerConflictV1,
			errors.New("published pointer differs from expected revision"),
		)
	}
	return EvaluateExactBasisV1(input, basis, control, catalog)
}

func inspectCurrentV1(
	input InputV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (exact bool, conflict bool) {
	switch input.TargetKind {
	case TargetWorkspaceChannelEndpointV1:
		workspace, found := control.FindWorkspace(input.WorkspaceID)
		if !found {
			return false, true
		}
		endpoint, found := workspace.FindChannelEndpoint(input.EndpointID)
		if found {
			if endpoint.Binding.InstanceID != input.InstanceID ||
				endpoint.Binding.Port != input.Port {
				return false, true
			}
			return false, false
		}
	case TargetProfileV1:
		profile, found := control.FindProfile(input.ProfileID)
		if !found {
			return false, true
		}
		matches := 0
		for _, binding := range profile.Bindings {
			if binding.Port == input.Port && binding.InstanceID == input.InstanceID {
				matches++
			}
		}
		if matches > 1 {
			return false, true
		}
		if matches == 1 {
			return false, false
		}
	default:
		return false, true
	}
	_, catalogHasInstance := catalog.FindInstance(input.InstanceID)
	controlStillReferences := controlReferencesInstanceV1(control, input.InstanceID)
	if controlStillReferences != catalogHasInstance {
		return false, true
	}
	return true, false
}

func preparePublicationV1(
	input InputV1,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (PublicationV1, error) {
	clonedControl, _, _, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		return PublicationV1{}, err
	}
	clonedCatalog, _, _, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		return PublicationV1{}, err
	}
	control = clonedControl
	catalog = clonedCatalog

	switch input.TargetKind {
	case TargetWorkspaceChannelEndpointV1:
		workspaceIndex := -1
		endpointIndex := -1
		for index := range control.Workspaces {
			if control.Workspaces[index].Workspace.ID != input.WorkspaceID {
				continue
			}
			workspaceIndex = index
			for candidateIndex, endpoint := range control.Workspaces[index].ChannelEndpoints {
				if endpoint.EndpointID != input.EndpointID {
					continue
				}
				if endpointIndex >= 0 || endpoint.Binding.InstanceID != input.InstanceID ||
					endpoint.Binding.Port != input.Port {
					return PublicationV1{}, newFailureV1(
						FailureTargetConflictV1,
						errors.New("Channel disable target is ambiguous or differs"),
					)
				}
				endpointIndex = candidateIndex
			}
			break
		}
		if workspaceIndex < 0 || endpointIndex < 0 {
			return PublicationV1{}, newFailureV1(
				FailureTargetConflictV1,
				errors.New("Channel disable target is absent"),
			)
		}
		endpoints := append(
			[]controlcontract.ChannelEndpointDefinition(nil),
			control.Workspaces[workspaceIndex].ChannelEndpoints...,
		)
		endpoints = append(endpoints[:endpointIndex], endpoints[endpointIndex+1:]...)
		control.Workspaces[workspaceIndex].ChannelEndpoints = endpoints
	case TargetProfileV1:
		profileIndex := -1
		matchIndex := -1
		for index := range control.Profiles {
			if control.Profiles[index].Profile.ID != input.ProfileID {
				continue
			}
			profileIndex = index
			for bindingIndex, binding := range control.Profiles[index].Bindings {
				if binding.Port == input.Port && binding.InstanceID == input.InstanceID {
					if matchIndex >= 0 {
						return PublicationV1{}, newFailureV1(
							FailureTargetConflictV1,
							errors.New("disable target is ambiguous"),
						)
					}
					matchIndex = bindingIndex
				}
			}
			break
		}
		if profileIndex < 0 || matchIndex < 0 {
			return PublicationV1{}, newFailureV1(
				FailureTargetConflictV1,
				errors.New("disable target is absent"),
			)
		}
		bindings := append(
			[]controlcontract.BindingSpec(nil),
			control.Profiles[profileIndex].Bindings...,
		)
		bindings = append(bindings[:matchIndex], bindings[matchIndex+1:]...)
		control.Profiles[profileIndex].Bindings = bindings
	default:
		return PublicationV1{}, newFailureV1(
			FailureInvalidInputV1,
			errors.New("module disable target kind is invalid"),
		)
	}

	if !controlReferencesInstanceV1(control, input.InstanceID) {
		entries := make([]controlcontract.CatalogEntry, 0, len(catalog.Entries))
		for _, entry := range catalog.Entries {
			if entry.Activation.InstanceID != input.InstanceID {
				entries = append(entries, entry)
			}
		}
		catalog.Entries = entries
	}
	return freezePublicationV1(input, basis, control, catalog)
}

func freezePublicationV1(
	input InputV1,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (PublicationV1, error) {
	if basis.PointerRevision >= math.MaxInt64 ||
		control.Revision >= math.MaxInt64 || catalog.Generation >= math.MaxInt64 {
		return PublicationV1{}, errors.New("Control/Catalog revision is exhausted")
	}
	control.SnapshotID = input.CandidateControlSnapshotID
	control.Revision++
	control.Digest = ""
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		return PublicationV1{}, err
	}
	catalog.GenerationID = input.CandidateCatalogGenerationID
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		return PublicationV1{}, err
	}
	return PublicationV1{
		ExpectedPointerRevision: basis.PointerRevision,
		NewPointerRevision:      basis.PointerRevision + 1,
		ControlRef:              controlRef,
		ControlCanonical:        append([]byte(nil), controlCanonical...),
		CatalogRef:              catalogRef,
		CatalogCanonical:        append([]byte(nil), catalogCanonical...),
	}, nil
}

func verifyExactRetryV1(
	ctx context.Context,
	view ReadViewV1,
	input InputV1,
	currentBasis controlcontract.PublishedBasis,
	currentControl controlcontract.ControlSnapshot,
	currentCatalog controlcontract.CatalogGeneration,
) (controlcontract.PublishedBasis, error) {
	if currentControl.Revision <= 1 || currentCatalog.Generation <= 1 {
		return controlcontract.PublishedBasis{}, errors.New("published retry has no predecessor revision")
	}
	previousControl, previousCatalog, err := view.LoadControlCatalogRevision(
		ctx,
		input.TenantID,
		currentControl.Revision-1,
		currentCatalog.Generation-1,
	)
	if err != nil {
		return controlcontract.PublishedBasis{}, newFailureV1(
			FailureStoreInvalidV1,
			errors.Join(err, errors.New("load exact predecessor Control/Catalog")),
		)
	}
	_, previousControlRef, _, err := controlcontract.NewControlSnapshot(previousControl)
	if err != nil {
		return controlcontract.PublishedBasis{}, newFailureV1(
			FailureStoreInvalidV1,
			errors.Join(err, errors.New("restore predecessor Control")),
		)
	}
	_, previousCatalogRef, _, err := controlcontract.NewCatalogGeneration(previousCatalog)
	if err != nil {
		return controlcontract.PublishedBasis{}, newFailureV1(
			FailureStoreInvalidV1,
			errors.Join(err, errors.New("restore predecessor Catalog")),
		)
	}
	previousBasis := controlcontract.PublishedBasis{
		TenantID:        input.TenantID,
		PointerRevision: input.ExpectedPointerRevision,
		Control:         previousControlRef,
		Catalog:         previousCatalogRef,
	}
	if err := previousBasis.Validate(); err != nil {
		return controlcontract.PublishedBasis{}, newFailureV1(
			FailureStoreInvalidV1,
			errors.Join(err, errors.New("predecessor PublishedBasis is invalid")),
		)
	}
	publication, err := preparePublicationV1(
		input,
		previousBasis,
		previousControl,
		previousCatalog,
	)
	if err != nil {
		return controlcontract.PublishedBasis{}, err
	}
	if currentBasis.PointerRevision != publication.NewPointerRevision ||
		currentBasis.Control != publication.ControlRef ||
		currentBasis.Catalog != publication.CatalogRef {
		return controlcontract.PublishedBasis{}, errors.New("published Control/Catalog refs differ from module disable")
	}
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(currentControl)
	if err != nil {
		return controlcontract.PublishedBasis{}, err
	}
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(currentCatalog)
	if err != nil {
		return controlcontract.PublishedBasis{}, err
	}
	if controlRef != publication.ControlRef || catalogRef != publication.CatalogRef ||
		!bytes.Equal(controlCanonical, publication.ControlCanonical) ||
		!bytes.Equal(catalogCanonical, publication.CatalogCanonical) {
		return controlcontract.PublishedBasis{}, errors.New("published Control/Catalog canonical bytes differ from module disable")
	}
	return previousBasis, nil
}

func currentBindingRemovalV1(
	input InputV1,
	control controlcontract.ControlSnapshot,
) BindingRemovalV1 {
	projection := emptyBindingRemovalV1(input)
	if input.TargetKind == TargetWorkspaceChannelEndpointV1 {
		workspace, found := control.FindWorkspace(input.WorkspaceID)
		if !found {
			return projection
		}
		endpoint, found := workspace.FindChannelEndpoint(input.EndpointID)
		if !found || endpoint.Binding.InstanceID != input.InstanceID ||
			endpoint.Binding.Port != input.Port {
			return projection
		}
		index := uint32(0)
		policy := endpoint.Binding.FailurePolicy
		projection.PortBindingIndex = &index
		projection.ConfigRef = endpoint.Binding.ConfigRef
		projection.AuthorityCeilingRef = endpoint.Binding.AuthorityCeilingRef
		projection.StaticContextRefs = append([]string(nil), endpoint.Binding.StaticContextRefs...)
		projection.FailurePolicy = &policy
		return projection
	}
	profile, found := control.FindProfile(input.ProfileID)
	if !found {
		return projection
	}
	ordinal := uint32(0)
	for _, binding := range profile.Bindings {
		if binding.Port != input.Port {
			continue
		}
		if binding.InstanceID == input.InstanceID {
			index := ordinal
			policy := binding.FailurePolicy
			projection.PortBindingIndex = &index
			projection.ConfigRef = binding.ConfigRef
			projection.AuthorityCeilingRef = binding.AuthorityCeilingRef
			projection.StaticContextRefs = append([]string(nil), binding.StaticContextRefs...)
			projection.FailurePolicy = &policy
			return projection
		}
		ordinal++
	}
	return projection
}

func emptyBindingRemovalV1(input InputV1) BindingRemovalV1 {
	return BindingRemovalV1{
		TargetKind:  input.TargetKind,
		ProfileID:   input.ProfileID,
		WorkspaceID: input.WorkspaceID,
		EndpointID:  input.EndpointID,
		Port:        input.Port,
	}
}

func lastReferenceInstanceV1(
	input InputV1,
	control controlcontract.ControlSnapshot,
) string {
	targetMatches := 0
	otherReferences := 0
	for _, profile := range control.Profiles {
		for _, binding := range profile.Bindings {
			if binding.InstanceID != input.InstanceID {
				continue
			}
			if input.TargetKind == TargetProfileV1 &&
				profile.Profile.ID == input.ProfileID && binding.Port == input.Port {
				targetMatches++
			} else {
				otherReferences++
			}
		}
	}
	for _, workspace := range control.Workspaces {
		for _, endpoint := range workspace.ChannelEndpoints {
			if endpoint.Binding.InstanceID != input.InstanceID {
				continue
			}
			if input.TargetKind == TargetWorkspaceChannelEndpointV1 &&
				workspace.Workspace.ID == input.WorkspaceID &&
				endpoint.EndpointID == input.EndpointID && endpoint.Binding.Port == input.Port {
				targetMatches++
			} else {
				otherReferences++
			}
		}
	}
	if targetMatches == 1 && otherReferences == 0 {
		return input.InstanceID
	}
	return ""
}

func controlReferencesInstanceV1(
	control controlcontract.ControlSnapshot,
	instanceID string,
) bool {
	for _, profile := range control.Profiles {
		for _, binding := range profile.Bindings {
			if binding.InstanceID == instanceID {
				return true
			}
		}
	}
	for _, workspace := range control.Workspaces {
		for _, endpoint := range workspace.ChannelEndpoints {
			if endpoint.Binding.InstanceID == instanceID {
				return true
			}
		}
	}
	return false
}
