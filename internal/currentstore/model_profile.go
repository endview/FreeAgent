package currentstore

import (
	"fmt"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type modelProfileContentGetter func(string) (ContentRecord, error)

// restoreMemberModelProfile restores and validates the optional profile only
// from one frozen Member snapshot and its content-addressed recovery closure.
// It never consults current Control, provider metadata, or an evaluation
// service, and it cannot select a different model Binding.
func restoreMemberModelProfile(
	member corecontract.MemberExecutionSnapshot,
	get modelProfileContentGetter,
) (*corecontract.ModelProfileV1, error) {
	if member.ModelProfile == nil {
		return nil, nil
	}
	if get == nil {
		return nil, fmt.Errorf("model profile content getter is nil")
	}
	binding, err := exactModelBinding(member)
	if err != nil {
		return nil, fmt.Errorf("restore model profile Binding: %w", err)
	}
	configRecord, err := get(binding.ConfigRef)
	if err != nil || configRecord.Kind != ContentConfig ||
		configRecord.MediaType != admissionJSONMediaType {
		return nil, fmt.Errorf("model profile Binding CONFIG is unavailable")
	}
	config, err := moduleapi.RestoreModelBindingConfigV2(
		configRecord.CanonicalBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("restore model profile Binding CONFIG: %w", err)
	}

	ref := *member.ModelProfile
	profileRecord, err := get(ref.Digest)
	if err != nil || profileRecord.Kind != ContentConfig ||
		profileRecord.MediaType != admissionJSONMediaType {
		return nil, fmt.Errorf("ModelProfile CONFIG is unavailable")
	}
	profile, err := corecontract.RestoreModelProfileV1(
		profileRecord.CanonicalBytes,
		ref,
	)
	if err != nil {
		return nil, fmt.Errorf("restore model-profile/v1: %w", err)
	}
	if err := corecontract.ValidateModelProfileBindingV1(
		profile,
		binding,
		config,
	); err != nil {
		return nil, fmt.Errorf(
			"ModelProfile does not match the frozen model Binding: %w",
			err,
		)
	}
	return &profile, nil
}

func effectiveContextPolicyForMember(
	member corecontract.MemberExecutionSnapshot,
	base corecontract.ContextPolicyV1,
	get modelProfileContentGetter,
) (corecontract.ContextPolicyV1, error) {
	profile, err := restoreMemberModelProfile(member, get)
	if err != nil {
		return corecontract.ContextPolicyV1{}, err
	}
	if profile == nil {
		return base, nil
	}
	return corecontract.TightenContextPolicyV1ForModelProfile(base, *profile)
}

func validateMemberModelProfileClosure(
	member corecontract.MemberExecutionSnapshot,
	get modelProfileContentGetter,
) error {
	profile, err := restoreMemberModelProfile(member, get)
	if err != nil || profile == nil {
		return err
	}
	policyRecord, err := get(member.ContextPolicy.Digest)
	if err != nil || policyRecord.Kind != ContentPolicy ||
		policyRecord.MediaType != admissionJSONMediaType {
		return fmt.Errorf("ModelProfile ContextPolicy is unavailable")
	}
	document, err := corecontract.RestorePolicyDocument(
		policyRecord.CanonicalBytes,
		member.ContextPolicy,
	)
	if err != nil || document.PolicyType != corecontract.PolicyContext {
		return fmt.Errorf("restore ModelProfile ContextPolicy")
	}
	policy, err := corecontract.RestoreContextPolicyV1(document.Body)
	if err != nil {
		return fmt.Errorf("restore ModelProfile context-policy/v1: %w", err)
	}
	if _, err := corecontract.TightenContextPolicyV1ForModelProfile(
		policy,
		*profile,
	); err != nil {
		return fmt.Errorf("apply ModelProfile context ceiling: %w", err)
	}
	return nil
}

func runContentGetter(run RunForLoop) modelProfileContentGetter {
	return func(digest string) (ContentRecord, error) {
		record, found := run.FindContent(digest)
		if !found {
			return ContentRecord{}, ErrContentNotFound
		}
		return record, nil
	}
}
