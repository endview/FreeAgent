package corecontract

import (
	"bytes"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ContextPolicySchemaVersionV1 = "context-policy/v1"

	ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1 = "canonical-json-utf8-byte-upper-bound/v1"
	ContextSummaryHeadTailExtractiveV1                = "head-tail-extractive/v1"

	ContextRestoreBasisPointsV1 uint64 = 8500
	ContextFullBasisPointsV1    uint64 = 10000

	maximumJSONSafeIntegerV1 uint64 = 1<<53 - 1
)

// ContextPolicyV1 is the frozen base context budget. An optional ModelProfile
// may only tighten its window. Thresholds and algorithms are exact Core V1
// semantics rather than caller-tunable values.
type ContextPolicyV1 struct {
	SchemaVersion        string `json:"schema_version"`
	ContextWindowTokens  uint64 `json:"context_window_tokens"`
	ReservedOutputTokens uint64 `json:"reserved_output_tokens"`
	RecentHistoryTurns   uint64 `json:"recent_history_turns"`
	EstimatorVersion     string `json:"estimator_version"`
}

func NewContextPolicyV1(
	input ContextPolicyV1,
) (ContextPolicyV1, []byte, error) {
	if input.SchemaVersion != ContextPolicySchemaVersionV1 {
		return ContextPolicyV1{}, nil, fmt.Errorf(
			"corecontract: context policy schema version must be %q",
			ContextPolicySchemaVersionV1,
		)
	}
	if input.ContextWindowTokens == 0 ||
		input.ContextWindowTokens > maximumJSONSafeIntegerV1 {
		return ContextPolicyV1{}, nil, fmt.Errorf(
			"corecontract: context window tokens must fit a positive JSON safe integer",
		)
	}
	if input.ReservedOutputTokens >= input.ContextWindowTokens {
		return ContextPolicyV1{}, nil, fmt.Errorf(
			"corecontract: reserved output tokens must be below the context window",
		)
	}
	if input.RecentHistoryTurns > moduleapi.MaxManifestEntries {
		return ContextPolicyV1{}, nil, fmt.Errorf(
			"corecontract: recent History turns exceed %d",
			moduleapi.MaxManifestEntries,
		)
	}
	if input.EstimatorVersion !=
		ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1 {
		return ContextPolicyV1{}, nil, fmt.Errorf(
			"corecontract: unsupported context estimator %q",
			input.EstimatorVersion,
		)
	}
	canonical, err := canonicalJSON(input)
	if err != nil {
		return ContextPolicyV1{}, nil, err
	}
	return input, canonical, nil
}

func RestoreContextPolicyV1(canonical []byte) (ContextPolicyV1, error) {
	if err := requireExactCanonical(canonical); err != nil {
		return ContextPolicyV1{}, err
	}
	var decoded ContextPolicyV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return ContextPolicyV1{}, err
	}
	restored, rebuilt, err := NewContextPolicyV1(decoded)
	if err != nil {
		return ContextPolicyV1{}, err
	}
	if !bytes.Equal(rebuilt, canonical) {
		return ContextPolicyV1{}, fmt.Errorf(
			"corecontract: context policy is not frozen canonically",
		)
	}
	return restored, nil
}

// InputBudgetTokens returns context_window_tokens-reserved_output_tokens.
func (policy ContextPolicyV1) InputBudgetTokens() (uint64, error) {
	if _, _, err := NewContextPolicyV1(policy); err != nil {
		return 0, err
	}
	return policy.ContextWindowTokens - policy.ReservedOutputTokens, nil
}

// RestoreWatermarkTokens returns floor(input_budget_tokens*0.85) without an
// overflowing multiplication.
func (policy ContextPolicyV1) RestoreWatermarkTokens() (uint64, error) {
	budget, err := policy.InputBudgetTokens()
	if err != nil {
		return 0, err
	}
	return ContextRestoreWatermarkTokensV1(budget)
}

// ContextRestoreWatermarkTokensV1 returns floor(input_budget_tokens*0.85)
// without an overflowing multiplication.
func ContextRestoreWatermarkTokensV1(inputBudgetTokens uint64) (uint64, error) {
	if inputBudgetTokens == 0 ||
		inputBudgetTokens > maximumJSONSafeIntegerV1 {
		return 0, fmt.Errorf(
			"corecontract: context input budget must fit a positive JSON safe integer",
		)
	}
	quotient := inputBudgetTokens / ContextFullBasisPointsV1
	remainder := inputBudgetTokens % ContextFullBasisPointsV1
	return quotient*ContextRestoreBasisPointsV1 +
		(remainder*ContextRestoreBasisPointsV1)/ContextFullBasisPointsV1, nil
}
