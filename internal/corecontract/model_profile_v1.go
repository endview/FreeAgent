package corecontract

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ModelProfileSchemaVersionV1 = "model-profile/v1"

	modelProfileContentKindV1 = "CONFIG"
	modelProfileMediaTypeV1   = "application/json"

	ModelProfileMaximumScoreBasisPointsV1    uint16 = 10000
	ModelProfileMaximumContextWindowTokensV1        = maximumJSONSafeIntegerV1
)

// ModelTendencyV1 is one reproducible evaluation metric. ScoreBasisPoints uses
// the inclusive range 0..10000. MetricID order is canonical UTF-8 byte order.
type ModelTendencyV1 struct {
	MetricID         string `json:"metric_id"`
	ScoreBasisPoints uint16 `json:"score_basis_points"`
}

// ModelProfileV1 describes one exact model build and the reproducible
// evaluation result used by assembly and context policy. It is descriptive:
// it cannot grant authority, raise a budget, or select a different provider.
type ModelProfileV1 struct {
	SchemaVersion          string            `json:"schema_version"`
	ID                     string            `json:"id"`
	Version                string            `json:"version"`
	Provider               string            `json:"provider"`
	Model                  string            `json:"model"`
	ModelBuildID           string            `json:"model_build_id"`
	ModelConfigRef         string            `json:"model_config_ref"`
	AdapterArtifactDigest  string            `json:"adapter_artifact_digest"`
	AdapterIdentity        string            `json:"adapter_identity"`
	ContextWindowTokens    uint64            `json:"context_window_tokens"`
	EvaluationSuite        string            `json:"evaluation_suite"`
	EvaluationVersion      string            `json:"evaluation_version"`
	EvaluationResultDigest string            `json:"evaluation_result_digest"`
	CapabilityTendencies   []ModelTendencyV1 `json:"capability_tendencies"`
	ReliabilityTendencies  []ModelTendencyV1 `json:"reliability_tendencies"`
}

// NewModelProfileV1 validates, canonically orders, defensively copies, and
// content-addresses one model-profile/v1 as a CONFIG content record.
func NewModelProfileV1(
	input ModelProfileV1,
) (ModelProfileV1, ModelProfileRef, []byte, error) {
	if input.SchemaVersion != ModelProfileSchemaVersionV1 {
		return ModelProfileV1{}, ModelProfileRef{}, nil, fmt.Errorf(
			"corecontract: model profile schema version must be %q",
			ModelProfileSchemaVersionV1,
		)
	}
	for _, field := range []struct {
		name    string
		value   string
		maximum int
	}{
		{name: "ID", value: input.ID, maximum: maxOpaqueIDBytes},
		{name: "version", value: input.Version, maximum: maxVersionBytes},
		{name: "provider", value: input.Provider, maximum: maxOpaqueIDBytes},
		{name: "model", value: input.Model, maximum: maxOpaqueIDBytes},
		{name: "model build ID", value: input.ModelBuildID, maximum: maxOpaqueIDBytes},
		{name: "adapter identity", value: input.AdapterIdentity, maximum: maxOpaqueIDBytes},
		{name: "evaluation suite", value: input.EvaluationSuite, maximum: maxOpaqueIDBytes},
		{name: "evaluation version", value: input.EvaluationVersion, maximum: maxVersionBytes},
	} {
		if !validOpaque(field.value, field.maximum) {
			return ModelProfileV1{}, ModelProfileRef{}, nil, fmt.Errorf(
				"corecontract: invalid model profile %s",
				field.name,
			)
		}
	}
	for _, digest := range []struct {
		name  string
		value string
	}{
		{name: "model config ref", value: input.ModelConfigRef},
		{name: "adapter artifact digest", value: input.AdapterArtifactDigest},
		{name: "evaluation result digest", value: input.EvaluationResultDigest},
	} {
		if !moduleapi.ValidSHA256(digest.value) {
			return ModelProfileV1{}, ModelProfileRef{}, nil, fmt.Errorf(
				"corecontract: invalid model profile %s",
				digest.name,
			)
		}
	}
	if input.ContextWindowTokens == 0 ||
		input.ContextWindowTokens > ModelProfileMaximumContextWindowTokensV1 {
		return ModelProfileV1{}, ModelProfileRef{}, nil, fmt.Errorf(
			"corecontract: model profile context window must fit a positive JSON safe integer",
		)
	}

	capabilities, err := canonicalModelTendenciesV1(
		"capability",
		input.CapabilityTendencies,
	)
	if err != nil {
		return ModelProfileV1{}, ModelProfileRef{}, nil, err
	}
	reliability, err := canonicalModelTendenciesV1(
		"reliability",
		input.ReliabilityTendencies,
	)
	if err != nil {
		return ModelProfileV1{}, ModelProfileRef{}, nil, err
	}

	frozen := input
	frozen.CapabilityTendencies = capabilities
	frozen.ReliabilityTendencies = reliability
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return ModelProfileV1{}, ModelProfileRef{}, nil, err
	}
	if len(canonical) > moduleapi.MaxConfigBytes {
		return ModelProfileV1{}, ModelProfileRef{}, nil, fmt.Errorf(
			"corecontract: canonical model profile exceeds %d bytes",
			moduleapi.MaxConfigBytes,
		)
	}
	ref := ModelProfileRef{
		ID:      frozen.ID,
		Version: frozen.Version,
		Digest: contentDigest(
			modelProfileContentKindV1,
			modelProfileMediaTypeV1,
			canonical,
		),
	}
	return cloneModelProfileV1(frozen), ref, bytes.Clone(canonical), nil
}

// RestoreModelProfileV1 accepts only the exact canonical CONFIG bytes and
// verifies their typed content-addressed reference.
func RestoreModelProfileV1(
	canonical []byte,
	ref ModelProfileRef,
) (ModelProfileV1, error) {
	if len(canonical) == 0 || len(canonical) > moduleapi.MaxConfigBytes {
		return ModelProfileV1{}, fmt.Errorf(
			"corecontract: model profile must contain between 1 and %d canonical bytes",
			moduleapi.MaxConfigBytes,
		)
	}
	if err := ref.Validate(); err != nil {
		return ModelProfileV1{}, err
	}
	if err := requireExactCanonical(canonical); err != nil {
		return ModelProfileV1{}, err
	}
	var decoded ModelProfileV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return ModelProfileV1{}, err
	}
	restored, rebuiltRef, rebuiltCanonical, err := NewModelProfileV1(decoded)
	if err != nil {
		return ModelProfileV1{}, err
	}
	if rebuiltRef != ref || !bytes.Equal(rebuiltCanonical, canonical) {
		return ModelProfileV1{}, fmt.Errorf(
			"corecontract: model profile does not match its reference",
		)
	}
	return cloneModelProfileV1(restored), nil
}

// ValidateModelProfileBindingV1 proves that a profile describes exactly the
// supplied model.generate/v1 Binding, its canonical CONFIG, and its activated
// adapter. It performs no I/O and never searches for another provider.
func ValidateModelProfileBindingV1(
	profile ModelProfileV1,
	binding moduleapi.PortBinding,
	config moduleapi.ModelBindingConfigV1,
) error {
	frozenProfile, _, _, err := NewModelProfileV1(profile)
	if err != nil {
		return fmt.Errorf("corecontract: model profile binding profile: %w", err)
	}
	plan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameModelGenerate,
			ExactVersion: moduleapi.PortVersionV1,
		},
		Bindings: []moduleapi.PortBinding{binding},
	})
	if err != nil {
		return fmt.Errorf("corecontract: model profile binding: %w", err)
	}
	frozenConfig, configCanonical, err :=
		moduleapi.NewModelBindingConfigV1(config)
	if err != nil {
		return fmt.Errorf("corecontract: model profile binding config: %w", err)
	}
	configDigest := contentDigest(
		modelProfileContentKindV1,
		modelProfileMediaTypeV1,
		configCanonical,
	)
	frozenBinding := plan.Bindings[0]
	if frozenProfile.ModelConfigRef != configDigest ||
		frozenBinding.ConfigRef != configDigest {
		return fmt.Errorf(
			"corecontract: model profile binding config reference mismatch",
		)
	}
	if frozenProfile.Provider != frozenConfig.Provider ||
		frozenProfile.Model != frozenConfig.Model ||
		frozenProfile.ModelBuildID != frozenConfig.ModelBuildID {
		return fmt.Errorf(
			"corecontract: model profile binding model build mismatch",
		)
	}
	if frozenProfile.AdapterArtifactDigest !=
		frozenBinding.Provider.ArtifactDigest ||
		frozenProfile.AdapterIdentity !=
			frozenBinding.Provider.AdapterIdentity {
		return fmt.Errorf(
			"corecontract: model profile binding adapter identity mismatch",
		)
	}
	return nil
}

// TightenContextPolicyV1ForModelProfile applies the model's frozen context
// ceiling without changing any other policy field. A larger model ceiling is
// ignored, so this function can never expand the caller's policy.
func TightenContextPolicyV1ForModelProfile(
	base ContextPolicyV1,
	profile ModelProfileV1,
) (ContextPolicyV1, error) {
	frozenBase, _, err := NewContextPolicyV1(base)
	if err != nil {
		return ContextPolicyV1{}, fmt.Errorf(
			"corecontract: tighten context policy base: %w",
			err,
		)
	}
	frozenProfile, _, _, err := NewModelProfileV1(profile)
	if err != nil {
		return ContextPolicyV1{}, fmt.Errorf(
			"corecontract: tighten context policy profile: %w",
			err,
		)
	}
	if frozenProfile.ContextWindowTokens >= frozenBase.ContextWindowTokens {
		return frozenBase, nil
	}
	frozenBase.ContextWindowTokens = frozenProfile.ContextWindowTokens
	tightened, _, err := NewContextPolicyV1(frozenBase)
	if err != nil {
		return ContextPolicyV1{}, fmt.Errorf(
			"corecontract: model profile context ceiling is incompatible with the frozen policy: %w",
			err,
		)
	}
	return tightened, nil
}

func canonicalModelTendenciesV1(
	kind string,
	input []ModelTendencyV1,
) ([]ModelTendencyV1, error) {
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"corecontract: model profile %s tendencies exceed %d",
			kind,
			moduleapi.MaxManifestEntries,
		)
	}
	frozen := append([]ModelTendencyV1{}, input...)
	for index, tendency := range frozen {
		if !validOpaque(tendency.MetricID, maxOpaqueIDBytes) {
			return nil, fmt.Errorf(
				"corecontract: model profile %s tendency %d has invalid metric ID",
				kind,
				index,
			)
		}
		if tendency.ScoreBasisPoints >
			ModelProfileMaximumScoreBasisPointsV1 {
			return nil, fmt.Errorf(
				"corecontract: model profile %s tendency %q score exceeds %d basis points",
				kind,
				tendency.MetricID,
				ModelProfileMaximumScoreBasisPointsV1,
			)
		}
	}
	sort.Slice(frozen, func(left, right int) bool {
		return frozen[left].MetricID < frozen[right].MetricID
	})
	for index := 1; index < len(frozen); index++ {
		if frozen[index-1].MetricID == frozen[index].MetricID {
			return nil, fmt.Errorf(
				"corecontract: duplicate model profile %s tendency %q",
				kind,
				frozen[index].MetricID,
			)
		}
	}
	return frozen, nil
}

func cloneModelProfileV1(profile ModelProfileV1) ModelProfileV1 {
	profile.CapabilityTendencies = append(
		[]ModelTendencyV1{},
		profile.CapabilityTendencies...,
	)
	profile.ReliabilityTendencies = append(
		[]ModelTendencyV1{},
		profile.ReliabilityTendencies...,
	)
	return profile
}
