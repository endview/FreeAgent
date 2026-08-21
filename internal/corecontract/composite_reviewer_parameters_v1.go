package corecontract

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// TightenReviewerModelParametersV1 returns the one canonical parameter object
// used by both the live Reviewer dispatch and Current Store's exact rebuild.
// The frozen Reviewer ceiling may only reduce an existing positive integer
// max_tokens value; when max_tokens is absent, the ceiling is inserted.
func TightenReviewerModelParametersV1(
	parameters json.RawMessage,
	ceiling uint32,
) (json.RawMessage, error) {
	if ceiling == 0 || ceiling > CompositeReviewerMaxOutputTokensV1 {
		return nil, fmt.Errorf(
			"corecontract: Reviewer max-output-token ceiling must be in [1,%d]",
			CompositeReviewerMaxOutputTokensV1,
		)
	}
	canonical, err := moduleapi.CanonicalJSON(parameters)
	if err != nil {
		return nil, fmt.Errorf(
			"corecontract: canonical Reviewer model parameters: %w",
			err,
		)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &object); err != nil || object == nil {
		return nil, fmt.Errorf(
			"corecontract: Reviewer model parameters must be a JSON object",
		)
	}
	maxTokens := uint64(ceiling)
	if raw, present := object["max_tokens"]; present {
		if len(raw) == 0 {
			return nil, fmt.Errorf(
				"corecontract: Reviewer max_tokens is empty",
			)
		}
		for _, character := range raw {
			if character < '0' || character > '9' {
				return nil, fmt.Errorf(
					"corecontract: Reviewer max_tokens must be a positive integer",
				)
			}
		}
		existing, parseErr := strconv.ParseUint(string(raw), 10, 64)
		if parseErr != nil || existing == 0 {
			return nil, fmt.Errorf(
				"corecontract: Reviewer max_tokens must be a positive bounded integer",
			)
		}
		if existing < maxTokens {
			maxTokens = existing
		}
	}
	object["max_tokens"] = json.RawMessage(strconv.FormatUint(maxTokens, 10))
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf(
			"corecontract: encode tightened Reviewer model parameters: %w",
			err,
		)
	}
	tightened, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return nil, fmt.Errorf(
			"corecontract: canonicalize tightened Reviewer model parameters: %w",
			err,
		)
	}
	return json.RawMessage(tightened), nil
}
