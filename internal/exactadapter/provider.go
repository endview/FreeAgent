package exactadapter

import "github.com/endview/freeagent/sdk/moduleapi"

// reusableProviderIdentity is the implementation identity shared by every
// activated Instance of one exact artifact and adapter. InstanceID and
// ActivationRevision remain invocation-specific frozen Binding facts.
type reusableProviderIdentity struct {
	moduleID        string
	version         string
	artifactDigest  string
	executionClass  moduleapi.ExecutionClass
	adapterIdentity string
}

func newReusableProviderIdentity(
	provider moduleapi.ActivatedModuleRef,
) (reusableProviderIdentity, error) {
	if err := provider.Validate(); err != nil {
		return reusableProviderIdentity{}, err
	}
	return reusableProviderIdentity{
		moduleID:        provider.ModuleID,
		version:         provider.Version,
		artifactDigest:  provider.ArtifactDigest,
		executionClass:  provider.ExecutionClass,
		adapterIdentity: provider.AdapterIdentity,
	}, nil
}

func (identity reusableProviderIdentity) matches(
	provider moduleapi.ActivatedModuleRef,
) bool {
	if err := provider.Validate(); err != nil {
		return false
	}
	return provider.ModuleID == identity.moduleID &&
		provider.Version == identity.version &&
		provider.ArtifactDigest == identity.artifactDigest &&
		provider.ExecutionClass == identity.executionClass &&
		provider.AdapterIdentity == identity.adapterIdentity
}
