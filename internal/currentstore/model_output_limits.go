package currentstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func validateModelParametersForRun(
	run RunForLoop,
	parametersCanonical []byte,
) (uint64, error) {
	maximum, present, err := modelParametersMaxTokens(parametersCanonical)
	if err != nil {
		return 0, err
	}
	if !present {
		return 0, fmt.Errorf("max_tokens must be explicit")
	}
	policy, err := frozenEffectiveContextPolicyForRun(run)
	if err != nil {
		return 0, err
	}
	if maximum > policy.ReservedOutputTokens {
		return 0, fmt.Errorf(
			"max_tokens %d exceeds frozen output reserve %d",
			maximum,
			policy.ReservedOutputTokens,
		)
	}
	return maximum, nil
}

func modelParametersMaxTokens(canonical []byte) (uint64, bool, error) {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&object); err != nil {
		return 0, false, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return 0, false, fmt.Errorf("parameters JSON has trailing content")
	}
	raw, present := object["max_tokens"]
	if !present {
		return 0, false, nil
	}
	var value uint64
	if err := json.Unmarshal(raw, &value); err != nil || value == 0 {
		return 0, false, fmt.Errorf("max_tokens must be a positive integer")
	}
	return value, true, nil
}
