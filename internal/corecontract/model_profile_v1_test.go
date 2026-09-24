package corecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModelProfileV1CanonicalRoundTripSortsAndDefensivelyCopies(
	t *testing.T,
) {
	profile, _, _ := validModelProfileBindingV1(t)
	source := append([]ModelTendencyV1(nil), profile.CapabilityTendencies...)
	profile.CapabilityTendencies = source
	profile.ReliabilityTendencies = nil
	rebuildInput := cloneModelProfileV1(profile)

	frozen, ref, canonical, err := NewModelProfileV1(profile)
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != profile.ID || ref.Version != profile.Version ||
		!moduleapi.ValidSHA256(ref.Digest) {
		t.Fatalf("ref = %+v", ref)
	}
	if len(frozen.CapabilityTendencies) != 2 ||
		frozen.CapabilityTendencies[0].MetricID != "coding" ||
		frozen.CapabilityTendencies[1].MetricID != "reasoning" {
		t.Fatalf("capability tendencies = %+v", frozen.CapabilityTendencies)
	}
	if frozen.ReliabilityTendencies == nil ||
		len(frozen.ReliabilityTendencies) != 0 ||
		!bytes.Contains(canonical, []byte(`"reliability_tendencies":[]`)) {
		t.Fatalf("nil reliability was not normalized to []: %s", canonical)
	}
	if contentDigest("CONFIG", "application/json", canonical) != ref.Digest {
		t.Fatal("ModelProfileRef is not the CONFIG content digest")
	}

	source[0].MetricID = "mutated-source"
	frozen.CapabilityTendencies[0].MetricID = "mutated-return"
	restored, err := RestoreModelProfileV1(canonical, ref)
	if err != nil {
		t.Fatal(err)
	}
	if restored.CapabilityTendencies[0].MetricID != "coding" ||
		restored.ReliabilityTendencies == nil {
		t.Fatalf("restored = %+v", restored)
	}
	wantCanonical := bytes.Clone(canonical)
	canonical[0] ^= 0xff
	restored.CapabilityTendencies[0].MetricID = "mutated-restored"
	_, rebuiltRef, rebuilt, err := NewModelProfileV1(rebuildInput)
	if err != nil {
		t.Fatal(err)
	}
	if rebuiltRef != ref || !bytes.Equal(rebuilt, wantCanonical) {
		t.Fatalf("rebuilt ref=%+v bytes=%s", rebuiltRef, rebuilt)
	}
}

func TestModelProfileV1RejectsInvalidFieldsAndTendencies(t *testing.T) {
	valid, _, _ := validModelProfileBindingV1(t)
	tests := []struct {
		name   string
		mutate func(*ModelProfileV1)
	}{
		{"schema", func(value *ModelProfileV1) { value.SchemaVersion = "model-profile/v2" }},
		{"ID", func(value *ModelProfileV1) { value.ID = "" }},
		{"version", func(value *ModelProfileV1) { value.Version = " version" }},
		{"provider", func(value *ModelProfileV1) { value.Provider = "" }},
		{"model", func(value *ModelProfileV1) { value.Model = "model\n" }},
		{"build", func(value *ModelProfileV1) { value.ModelBuildID = "" }},
		{"config ref", func(value *ModelProfileV1) { value.ModelConfigRef = strings.Repeat("A", 64) }},
		{"artifact", func(value *ModelProfileV1) { value.AdapterArtifactDigest = "not-a-digest" }},
		{"adapter", func(value *ModelProfileV1) { value.AdapterIdentity = "" }},
		{"zero window", func(value *ModelProfileV1) { value.ContextWindowTokens = 0 }},
		{"unsafe JSON window", func(value *ModelProfileV1) {
			value.ContextWindowTokens = ModelProfileMaximumContextWindowTokensV1 + 1
		}},
		{"suite", func(value *ModelProfileV1) { value.EvaluationSuite = "" }},
		{"evaluation version", func(value *ModelProfileV1) { value.EvaluationVersion = " v1" }},
		{"evaluation digest", func(value *ModelProfileV1) { value.EvaluationResultDigest = strings.Repeat("g", 64) }},
		{"metric", func(value *ModelProfileV1) {
			value.CapabilityTendencies[0].MetricID = ""
		}},
		{"score", func(value *ModelProfileV1) {
			value.CapabilityTendencies[0].ScoreBasisPoints = 10001
		}},
		{"duplicate", func(value *ModelProfileV1) {
			value.CapabilityTendencies[1].MetricID =
				value.CapabilityTendencies[0].MetricID
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := cloneModelProfileV1(valid)
			test.mutate(&input)
			if _, _, _, err := NewModelProfileV1(input); err == nil {
				t.Fatal("invalid model profile accepted")
			}
		})
	}
}

func TestModelProfileV1ContextWindowJSONSafeIntegerBoundary(t *testing.T) {
	profile, _, _ := validModelProfileBindingV1(t)
	profile.ContextWindowTokens = ModelProfileMaximumContextWindowTokensV1

	frozen, ref, canonical, err := NewModelProfileV1(profile)
	if err != nil {
		t.Fatalf("NewModelProfileV1(max safe integer): %v", err)
	}
	restored, err := RestoreModelProfileV1(canonical, ref)
	if err != nil {
		t.Fatalf("RestoreModelProfileV1(max safe integer): %v", err)
	}
	if frozen.ContextWindowTokens != ModelProfileMaximumContextWindowTokensV1 ||
		restored.ContextWindowTokens != ModelProfileMaximumContextWindowTokensV1 {
		t.Fatalf(
			"context window changed across canonical round trip: frozen=%d restored=%d",
			frozen.ContextWindowTokens,
			restored.ContextWindowTokens,
		)
	}

	profile.ContextWindowTokens = ModelProfileMaximumContextWindowTokensV1 + 1
	if _, _, _, err := NewModelProfileV1(profile); err == nil {
		t.Fatal("context window above the JSON safe integer boundary was accepted")
	}
}

func TestModelProfileV1RequiresBoundedConfigBytes(t *testing.T) {
	profile, _, _ := validModelProfileBindingV1(t)
	profile.CapabilityTendencies = make(
		[]ModelTendencyV1,
		moduleapi.MaxManifestEntries,
	)
	for index := range profile.CapabilityTendencies {
		profile.CapabilityTendencies[index] = ModelTendencyV1{
			MetricID: fmt.Sprintf(
				"%03d-%s",
				index,
				strings.Repeat("x", moduleapi.MaxOpaqueIDBytes-4),
			),
			ScoreBasisPoints: 1,
		}
	}
	if _, _, _, err := NewModelProfileV1(profile); err == nil {
		t.Fatal("oversize canonical model profile was accepted")
	}

	valid, _, _ := validModelProfileBindingV1(t)
	_, ref, _, err := NewModelProfileV1(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreModelProfileV1(
		bytes.Repeat([]byte{' '}, moduleapi.MaxConfigBytes+1),
		ref,
	); err == nil {
		t.Fatal("oversize model profile wire was accepted")
	}
}

func TestRestoreModelProfileV1RejectsWrongRefAndNonExactWire(t *testing.T) {
	profile, _, _ := validModelProfileBindingV1(t)
	_, ref, canonical, err := NewModelProfileV1(profile)
	if err != nil {
		t.Fatal(err)
	}
	wrong := ref
	wrong.Digest = strings.Repeat("f", 64)
	if _, err := RestoreModelProfileV1(canonical, wrong); err == nil {
		t.Fatal("wrong typed ref accepted")
	}
	if _, err := RestoreModelProfileV1(
		append(bytes.Clone(canonical), ' '),
		ref,
	); err == nil {
		t.Fatal("non-canonical wire accepted")
	}
	var object map[string]any
	if err := json.Unmarshal(canonical, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	withUnknown, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	withUnknown, err = moduleapi.CanonicalJSON(withUnknown)
	if err != nil {
		t.Fatal(err)
	}
	unknownRef := ref
	unknownRef.Digest = contentDigest("CONFIG", "application/json", withUnknown)
	if _, err := RestoreModelProfileV1(withUnknown, unknownRef); err == nil {
		t.Fatal("unknown model profile field accepted")
	}
}

func TestValidateModelProfileBindingV1RequiresOneExactClosure(t *testing.T) {
	profile, binding, config := validModelProfileBindingV1(t)
	if err := ValidateModelProfileBindingV1(profile, binding, config); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name          string
		mutateProfile func(*ModelProfileV1)
		mutateBinding func(*moduleapi.PortBinding)
		mutateConfig  func(*moduleapi.ModelBindingConfigV2)
	}{
		{
			name: "config ref",
			mutateBinding: func(value *moduleapi.PortBinding) {
				value.ConfigRef = strings.Repeat("d", 64)
			},
		},
		{
			name: "provider",
			mutateProfile: func(value *ModelProfileV1) {
				value.Provider = "other-provider"
			},
		},
		{
			name: "model",
			mutateProfile: func(value *ModelProfileV1) {
				value.Model = "other-model"
			},
		},
		{
			name: "model build",
			mutateProfile: func(value *ModelProfileV1) {
				value.ModelBuildID = "other-build"
			},
		},
		{
			name: "artifact",
			mutateProfile: func(value *ModelProfileV1) {
				value.AdapterArtifactDigest = strings.Repeat("d", 64)
			},
		},
		{
			name: "adapter",
			mutateProfile: func(value *ModelProfileV1) {
				value.AdapterIdentity = "other-adapter/v1"
			},
		},
		{
			name: "non model binding",
			mutateBinding: func(value *moduleapi.PortBinding) {
				value.FailurePolicy = moduleapi.FailureOptional
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateProfile := cloneModelProfileV1(profile)
			candidateBinding := binding
			candidateBinding.StaticContextRefs = append(
				[]string{},
				binding.StaticContextRefs...,
			)
			candidateConfig := config
			candidateConfig.Parameters = bytes.Clone(config.Parameters)
			if test.mutateProfile != nil {
				test.mutateProfile(&candidateProfile)
			}
			if test.mutateBinding != nil {
				test.mutateBinding(&candidateBinding)
			}
			if test.mutateConfig != nil {
				test.mutateConfig(&candidateConfig)
			}
			if err := ValidateModelProfileBindingV1(
				candidateProfile,
				candidateBinding,
				candidateConfig,
			); err == nil {
				t.Fatal("mismatched model profile Binding accepted")
			}
		})
	}
}

func TestTightenContextPolicyV1ForModelProfileNeverExpands(t *testing.T) {
	profile, _, _ := validModelProfileBindingV1(t)
	base := validContextPolicyV1()
	profile.ContextWindowTokens = 64000
	tightened, err := TightenContextPolicyV1ForModelProfile(base, profile)
	if err != nil {
		t.Fatal(err)
	}
	want := base
	want.ContextWindowTokens = 64000
	if !reflect.DeepEqual(tightened, want) {
		t.Fatalf("tightened=%+v want=%+v", tightened, want)
	}

	profile.ContextWindowTokens = base.ContextWindowTokens + 1
	unchanged, err := TightenContextPolicyV1ForModelProfile(base, profile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unchanged, base) {
		t.Fatalf("larger model window expanded policy: %+v", unchanged)
	}

	profile.ContextWindowTokens = base.ReservedOutputTokens
	if _, err := TightenContextPolicyV1ForModelProfile(
		base,
		profile,
	); err == nil {
		t.Fatal("model ceiling that cannot fit reserved output accepted")
	}
}

func validModelProfileBindingV1(
	t *testing.T,
) (ModelProfileV1, moduleapi.PortBinding, moduleapi.ModelBindingConfigV2) {
	t.Helper()
	config := moduleapi.ModelBindingConfigV2{
		SchemaVersion: moduleapi.ModelBindingConfigSchemaV2,
		Provider:      "provider-local",
		Model:         "model-v1",
		ModelBuildID:  "model-v1-build-2026-08-03",
		Parameters:    json.RawMessage(`{}`),
	}
	_, configCanonical, err := moduleapi.NewModelBindingConfigV2(config)
	if err != nil {
		t.Fatal(err)
	}
	configRef := contentDigest("CONFIG", "application/json", configCanonical)
	binding := moduleapi.PortBinding{
		Provider: moduleapi.ActivatedModuleRef{
			ModuleID:           "freeagent.model.provider",
			Version:            "1.0.0",
			ArtifactDigest:     strings.Repeat("a", 64),
			InstanceID:         "model-provider-1",
			ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
			AdapterIdentity:    "freeagent.adapter.model/v1",
			ActivationRevision: 1,
		},
		ConfigRef:           configRef,
		AuthorityCeilingRef: strings.Repeat("b", 64),
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	profile := ModelProfileV1{
		SchemaVersion:          ModelProfileSchemaVersionV1,
		ID:                     "model-profile-local",
		Version:                "1",
		Provider:               config.Provider,
		Model:                  config.Model,
		ModelBuildID:           config.ModelBuildID,
		ModelConfigRef:         configRef,
		AdapterArtifactDigest:  binding.Provider.ArtifactDigest,
		AdapterIdentity:        binding.Provider.AdapterIdentity,
		ContextWindowTokens:    128000,
		EvaluationSuite:        "freeagent-model-eval",
		EvaluationVersion:      "1",
		EvaluationResultDigest: strings.Repeat("c", 64),
		CapabilityTendencies: []ModelTendencyV1{
			{MetricID: "reasoning", ScoreBasisPoints: 9100},
			{MetricID: "coding", ScoreBasisPoints: 9300},
		},
		ReliabilityTendencies: []ModelTendencyV1{
			{MetricID: "instruction-following", ScoreBasisPoints: 8800},
		},
	}
	return profile, binding, config
}
