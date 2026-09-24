package moduleapi

import "fmt"

// PortRef identifies one exact, versioned module protocol port.
type PortRef struct {
	Name         string `json:"name"`
	ExactVersion string `json:"exact_version"`
}

func (ref PortRef) Validate() error {
	if !validDottedIdentifier(ref.Name, MaxIdentifierBytes) {
		return fmt.Errorf("port name %q must be a dotted identifier", ref.Name)
	}
	if !validVersion(ref.ExactVersion) {
		return fmt.Errorf("port %q exact_version %q is invalid", ref.Name, ref.ExactVersion)
	}
	return nil
}

func (ref PortRef) CanonicalKey() (string, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	return ref.Name + "\x00" + ref.ExactVersion, nil
}

// ExecutionClass is assigned by Core at module activation time. A module
// manifest cannot grant itself an execution class.
type ExecutionClass string

const (
	ExecutionDeclarative      ExecutionClass = "DECLARATIVE"
	ExecutionTrustedInProcess ExecutionClass = "TRUSTED_IN_PROCESS"
	ExecutionLocalProcess     ExecutionClass = "LOCAL_PROCESS"
	ExecutionRemote           ExecutionClass = "REMOTE"
	ExecutionWASM             ExecutionClass = "WASM"
)

func (class ExecutionClass) Validate() error {
	switch class {
	case ExecutionDeclarative,
		ExecutionTrustedInProcess,
		ExecutionLocalProcess,
		ExecutionRemote,
		ExecutionWASM:
		return nil
	default:
		return fmt.Errorf("unsupported execution class %q", class)
	}
}

func (class ExecutionClass) supportedInS1() bool {
	return class == ExecutionDeclarative || class == ExecutionTrustedInProcess
}

func executionClassSupportedForPortV1(
	port PortRef,
	class ExecutionClass,
) bool {
	if class == ExecutionLocalProcess || class == ExecutionRemote ||
		class == ExecutionWASM {
		return port.Name == PortNameActionProvider &&
			port.ExactVersion == PortVersionV1
	}
	return class.supportedInS1()
}

// ActivatedModuleRef freezes the exact provider identity selected before a
// run is admitted.
type ActivatedModuleRef struct {
	ModuleID           string         `json:"module_id"`
	Version            string         `json:"version"`
	ArtifactDigest     string         `json:"artifact_digest"`
	InstanceID         string         `json:"instance_id"`
	ExecutionClass     ExecutionClass `json:"execution_class"`
	AdapterIdentity    string         `json:"adapter_identity"`
	ActivationRevision uint64         `json:"activation_revision"`
}

func (ref ActivatedModuleRef) Validate() error {
	if !validDottedIdentifier(ref.ModuleID, MaxIdentifierBytes) {
		return fmt.Errorf("activated module id %q must be a dotted identifier", ref.ModuleID)
	}
	if !validVersion(ref.Version) {
		return fmt.Errorf("activated module %q version %q is invalid", ref.ModuleID, ref.Version)
	}
	if !ValidSHA256(ref.ArtifactDigest) {
		return fmt.Errorf("activated module %q artifact_digest must be a lowercase SHA-256 digest", ref.ModuleID)
	}
	if err := validateOpaqueID("activated module instance_id", ref.InstanceID); err != nil {
		return err
	}
	if err := ref.ExecutionClass.Validate(); err != nil {
		return err
	}
	if err := validateOpaqueID("activated module adapter_identity", ref.AdapterIdentity); err != nil {
		return err
	}
	if ref.ActivationRevision == 0 {
		return fmt.Errorf("activated module activation_revision must be positive")
	}
	return nil
}

// PortBinding freezes one provider and the consumer-owned configuration,
// authority ceiling, ordered static context recovery refs, and failure policy
// used at this position.
type PortBinding struct {
	Provider            ActivatedModuleRef `json:"provider"`
	ConfigRef           string             `json:"config_ref"`
	AuthorityCeilingRef string             `json:"authority_ceiling_ref"`
	StaticContextRefs   []string           `json:"static_context_refs"`
	FailurePolicy       FailurePolicy      `json:"failure_policy"`
}

func (binding PortBinding) Validate() error {
	if err := binding.Provider.Validate(); err != nil {
		return err
	}
	if !ValidSHA256(binding.ConfigRef) {
		return fmt.Errorf("port binding config_ref must be a lowercase SHA-256 content digest")
	}
	if !ValidSHA256(binding.AuthorityCeilingRef) {
		return fmt.Errorf("port binding authority_ceiling_ref must be a lowercase SHA-256 content digest")
	}
	if len(binding.StaticContextRefs) > MaxManifestEntries {
		return fmt.Errorf(
			"port binding may reference at most %d static contexts",
			MaxManifestEntries,
		)
	}
	seenStaticContext := make(
		map[string]struct{},
		len(binding.StaticContextRefs),
	)
	for index, ref := range binding.StaticContextRefs {
		if !ValidSHA256(ref) {
			return fmt.Errorf(
				"port binding static_context_refs[%d] must be a lowercase SHA-256 content digest",
				index,
			)
		}
		if _, duplicate := seenStaticContext[ref]; duplicate {
			return fmt.Errorf(
				"port binding has duplicate static context ref %s",
				ref,
			)
		}
		seenStaticContext[ref] = struct{}{}
	}
	if err := binding.FailurePolicy.Validate(); err != nil {
		return err
	}
	return nil
}

// PortPlan contains the sole, ordered binding plan for one exact PortRef.
// Bindings order is semantic and must never be rearranged by Core or a Host.
type PortPlan struct {
	Port     PortRef       `json:"port"`
	Bindings []PortBinding `json:"bindings"`
}

const (
	PortNameModelGenerate    = "model.generate"
	PortNameContextProvide   = "context.provide"
	PortNameActionProvider   = "action.provider"
	PortNameChannelTransport = "channel.transport"
	PortVersionV1            = "v1"
	PortVersionV2            = "v2"
)

var s1PortRefs = [...]PortRef{
	{Name: PortNameModelGenerate, ExactVersion: PortVersionV2},
	{Name: PortNameContextProvide, ExactVersion: PortVersionV1},
	{Name: PortNameActionProvider, ExactVersion: PortVersionV1},
	{Name: PortNameChannelTransport, ExactVersion: PortVersionV1},
}

// S1PortRefs returns a copy of the exact PortRefs implemented by S1. Port
// behavior remains private to the exact version and is not a configurable
// cardinality or merge policy.
func S1PortRefs() []PortRef {
	refs := make([]PortRef, len(s1PortRefs))
	copy(refs, s1PortRefs[:])
	return refs
}

func isS1PortRef(ref PortRef) bool {
	for _, supported := range s1PortRefs {
		if supported == ref {
			return true
		}
	}
	return false
}

// NewPortPlan constructs an S1 plan. It validates the exact S1 registry and
// phase-supported execution classes, and defensively deep-copies Bindings
// without sorting, deduplicating, or otherwise changing semantic order.
func NewPortPlan(plan PortPlan) (PortPlan, error) {
	if err := validateS1PortPlan(plan); err != nil {
		return PortPlan{}, err
	}
	frozen := plan
	frozen.Bindings = make([]PortBinding, len(plan.Bindings))
	for index, binding := range plan.Bindings {
		binding.StaticContextRefs = append(
			[]string{},
			binding.StaticContextRefs...,
		)
		frozen.Bindings[index] = binding
	}
	return frozen, nil
}

func (plan PortPlan) Validate() error {
	return validateS1PortPlan(plan)
}

func validateS1PortPlan(plan PortPlan) error {
	if err := plan.Port.Validate(); err != nil {
		return err
	}
	if !isS1PortRef(plan.Port) {
		return fmt.Errorf(
			"port %s/%s is not registered in S1",
			plan.Port.Name,
			plan.Port.ExactVersion,
		)
	}
	if len(plan.Bindings) == 0 {
		return fmt.Errorf("port %s/%s plan must not be empty", plan.Port.Name, plan.Port.ExactVersion)
	}
	if len(plan.Bindings) > MaxManifestEntries {
		return fmt.Errorf(
			"port %s/%s plan may contain at most %d bindings",
			plan.Port.Name,
			plan.Port.ExactVersion,
			MaxManifestEntries,
		)
	}
	// model.generate/v2 and channel.transport/v1 are exact single-provider
	// Ports. This is a protocol rule, not caller-configurable cardinality.
	if ((plan.Port.Name == PortNameModelGenerate &&
		plan.Port.ExactVersion == PortVersionV2) ||
		(plan.Port.Name == PortNameChannelTransport &&
			plan.Port.ExactVersion == PortVersionV1)) &&
		len(plan.Bindings) != 1 {
		return fmt.Errorf(
			"port %s/%s requires exactly one binding",
			plan.Port.Name,
			plan.Port.ExactVersion,
		)
	}
	staticContextCount := 0
	for index, binding := range plan.Bindings {
		if err := binding.Validate(); err != nil {
			return fmt.Errorf("port binding %d: %w", index, err)
		}
		staticContextCount += len(binding.StaticContextRefs)
		if staticContextCount > MaxManifestEntries {
			return fmt.Errorf(
				"port %s/%s may reference at most %d static contexts",
				plan.Port.Name,
				plan.Port.ExactVersion,
				MaxManifestEntries,
			)
		}
		if plan.Port.Name != PortNameContextProvide &&
			len(binding.StaticContextRefs) != 0 {
			return fmt.Errorf(
				"port %s/%s cannot reference static context",
				plan.Port.Name,
				plan.Port.ExactVersion,
			)
		}
		if !executionClassSupportedForPortV1(
			plan.Port,
			binding.Provider.ExecutionClass,
		) {
			return fmt.Errorf(
				"port binding %d: execution class %q is not supported for port %s/%s",
				index,
				binding.Provider.ExecutionClass,
				plan.Port.Name,
				plan.Port.ExactVersion,
			)
		}
		if plan.Port.Name == PortNameContextProvide {
			switch binding.Provider.ExecutionClass {
			case ExecutionDeclarative:
				if len(binding.StaticContextRefs) == 0 {
					return fmt.Errorf(
						"port binding %d: DECLARATIVE context.provide/v1 requires at least one static context ref",
						index,
					)
				}
			case ExecutionTrustedInProcess:
				if len(binding.StaticContextRefs) != 0 {
					return fmt.Errorf(
						"port binding %d: TRUSTED_IN_PROCESS context.provide/v1 cannot reference static context",
						index,
					)
				}
				if binding.FailurePolicy != FailureRequired {
					return fmt.Errorf(
						"port binding %d: TRUSTED_IN_PROCESS context.provide/v1 must be REQUIRED",
						index,
					)
				}
			}
		}
		if plan.Port.Name == PortNameActionProvider {
			if binding.Provider.ExecutionClass != ExecutionTrustedInProcess &&
				binding.Provider.ExecutionClass != ExecutionLocalProcess &&
				binding.Provider.ExecutionClass != ExecutionRemote &&
				binding.Provider.ExecutionClass != ExecutionWASM {
				return fmt.Errorf(
					"port binding %d: action.provider/v1 requires TRUSTED_IN_PROCESS, LOCAL_PROCESS, REMOTE, or WASM",
					index,
				)
			}
			if binding.FailurePolicy != FailureRequired {
				return fmt.Errorf(
					"port binding %d: action.provider/v1 must be REQUIRED",
					index,
				)
			}
		}
		if plan.Port.Name == PortNameChannelTransport {
			if binding.Provider.ExecutionClass != ExecutionTrustedInProcess {
				return fmt.Errorf(
					"port binding %d: channel.transport/v1 requires TRUSTED_IN_PROCESS",
					index,
				)
			}
			if binding.FailurePolicy != FailureRequired {
				return fmt.Errorf(
					"port binding %d: channel.transport/v1 must be REQUIRED",
					index,
				)
			}
		}
	}
	if plan.Port.Name == PortNameModelGenerate &&
		plan.Bindings[0].FailurePolicy != FailureRequired {
		return fmt.Errorf("port model.generate/v2 requires a REQUIRED binding")
	}
	return nil
}
