package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/internal/modulehandler"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func moduleApplyDeepSeekBuildsV1() map[string]string {
	return modulehandler.DeepSeekBuildsV1()
}

func validateModuleApplyDeepSeekBindingV1(
	tenantID string,
	binding moduleApplyBindingV1,
) error {
	return validateModuleApplyDeepSeekBindingCanonicalV1(
		tenantID,
		binding.Config,
		binding.AuthorityCeiling,
	)
}

func validateModuleApplyDeepSeekBindingCanonicalV1(
	tenantID string,
	configCanonical []byte,
	authorityCanonical []byte,
) error {
	return modulehandler.ValidateDeepSeekBindingCanonicalV1(
		tenantID, configCanonical, authorityCanonical,
	)
}

// validateModuleApplyModelSecretGrantV1 binds the Operator's transient local
// grant to the immutable SecretRef identity carried by a Model authority
// ceiling. The grant is never persisted or returned by the command.
func validateModuleApplyModelSecretGrantV1(
	plan moduleApplyPlanV1,
	grant string,
) error {
	if plan.Port != productionModelPort {
		if grant != "" {
			return errors.New("non-Model module apply forbids a Model SecretRef grant")
		}
		return nil
	}
	if plan.DesiredState != moduleApplyEnabledV1 || plan.Binding == nil {
		return errors.New("Model SecretRef grant requires an enabled Model binding")
	}
	authority, err := moduleapi.RestoreModelAuthorityCeilingV1(
		plan.Binding.AuthorityCeiling,
	)
	if err != nil {
		return err
	}
	if grant == "" || grant != authority.SecretRef {
		return errors.New("exact local Model SecretRef grant is absent")
	}
	return nil
}

func validateModuleApplyModelStoreClosureV1(
	_ context.Context,
	_ moduleApplyReadViewV1,
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
) error {
	if plan.Port != productionModelPort {
		return nil
	}
	if plan.Module == nil || plan.Binding == nil {
		return errors.New("enabled Model replacement payload is incomplete")
	}
	profile, found := findModuleApplyProfileV1(
		control,
		plan.BindingTarget.ProfileID,
	)
	if !found {
		return errors.New("target Profile is absent")
	}
	var current *controlcontract.BindingSpec
	for index := range profile.Bindings {
		binding := &profile.Bindings[index]
		if binding.Port != productionModelPort {
			continue
		}
		if current != nil {
			return errors.New("target Profile has more than one Model binding")
		}
		current = binding
	}
	if current == nil {
		return errors.New("target Profile has no Model binding to replace")
	}
	if current.InstanceID != plan.InstanceID {
		return errors.New(
			"first Model replacement slice must retain the exact active provider instance",
		)
	}

	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           plan.Module.ID,
		Version:            plan.Module.ExactVersion,
		ArtifactDigest:     plan.Module.ArtifactDigest,
		InstanceID:         plan.InstanceID,
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    deepseekmodel.AdapterIdentityV1,
		ActivationRevision: 1,
	}
	modelProfileRef, _, err := moduleApplyPlannedModelProfileV1(plan, provider)
	if err != nil {
		return err
	}
	// module-apply-plan/v1 is a complete desired state for this exact Model
	// binding. An omitted optional ModelProfile therefore clears the current
	// profile instead of silently retaining stale model evaluation metadata.
	// A supplied profile is still required to match the new binding exactly.
	_ = modelProfileRef
	return nil
}

func moduleApplyPlannedModelProfileV1(
	plan moduleApplyPlanV1,
	provider moduleapi.ActivatedModuleRef,
) (*corecontract.ModelProfileRef, []byte, error) {
	if len(plan.ModelProfile) == 0 {
		return nil, nil, nil
	}
	if plan.Binding == nil {
		return nil, nil, errors.New("ModelProfile requires a Model binding")
	}
	var decoded corecontract.ModelProfileV1
	decoder := json.NewDecoder(bytes.NewReader(plan.ModelProfile))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, nil, err
	}
	profile, ref, canonical, err := corecontract.NewModelProfileV1(decoded)
	if err != nil || !bytes.Equal(canonical, plan.ModelProfile) {
		return nil, nil, errors.Join(err, errors.New("planned ModelProfile is not canonical"))
	}
	config, err := moduleapi.RestoreModelBindingConfigV2(plan.Binding.Config)
	if err != nil {
		return nil, nil, err
	}
	configRef, authorityRef, err := moduleApplyContentRefsV1(*plan.Binding)
	if err != nil {
		return nil, nil, err
	}
	if err := corecontract.ValidateModelProfileBindingV1(
		profile,
		moduleapi.PortBinding{
			Provider:            provider,
			ConfigRef:           configRef,
			AuthorityCeilingRef: authorityRef,
			FailurePolicy:       moduleapi.FailureRequired,
		},
		config,
	); err != nil {
		return nil, nil, err
	}
	copy := ref
	return &copy, bytes.Clone(canonical), nil
}
