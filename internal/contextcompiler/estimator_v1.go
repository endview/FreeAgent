package contextcompiler

import (
	"encoding/json"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// EstimateModelGenerateRequestV1 applies the one frozen V1 estimator to an
// already assembled provider-neutral request. Store admission gates call this
// same function when proving that a missing context-compilation/v1 record is
// valid; there is deliberately no second estimate implementation at that
// boundary.
func EstimateModelGenerateRequestV1(
	request moduleapi.ModelGenerateRequestV1,
) (uint64, error) {
	frozen, _, err := moduleapi.NewModelGenerateRequestV1(request)
	if err != nil {
		return 0, invalidInput("final model request", err)
	}
	// encoding/json may escape more bytes than RFC 8785, but uses the exact
	// same omitempty rules as the frozen wire. This keeps the historical
	// no-Action estimate unchanged while covering the optional Action schema
	// projection without a second hand-maintained request layout.
	encoded, err := json.Marshal(frozen)
	if err != nil {
		return 0, invalidInput("final model request", err)
	}
	return uint64(len(encoded)), nil
}

func estimateUnits(
	units []contextUnit,
	parameters json.RawMessage,
) (uint64, error) {
	return estimateUnitsWithActions(units, parameters, nil, 0)
}

// estimateUnitsWithActions is the sole Action-aware extension of the V1
// estimator. reservationTokens is the conservative incremental estimate for
// appending the largest permitted Action result message to model two.
func estimateUnitsWithActions(
	units []contextUnit,
	parameters json.RawMessage,
	actions []moduleapi.ModelActionDefinitionV1,
	reservationTokens uint64,
) (uint64, error) {
	messages := flattenMessages(units)
	if len(actions) != 0 && len(messages) >= moduleapi.MaxManifestEntries {
		return 0, fmt.Errorf(
			"%w: Action-enabled model one must reserve one complete message slot for its result envelope",
			ErrContextBudgetExceeded,
		)
	}
	estimate, err := EstimateModelGenerateRequestV1(
		moduleapi.ModelGenerateRequestV1{
			SchemaVersion: moduleapi.ModelGenerateRequestSchemaV1,
			Messages:      messages,
			Parameters:    parameters,
			Actions:       actions,
		},
	)
	if err != nil {
		return 0, err
	}
	return checkedAdd(estimate, reservationTokens)
}

func validateMessage(message moduleapi.ModelMessageV1) error {
	if err := message.Role.Validate(); err != nil {
		return err
	}
	if message.Content == "" ||
		len(message.Content) > moduleapi.MaxTextBytes ||
		!utf8.ValidString(message.Content) ||
		message.Content != moduleapi.CanonicalText(message.Content) {
		return fmt.Errorf(
			"message content must be non-empty NFC UTF-8 and at most %d bytes",
			moduleapi.MaxTextBytes,
		)
	}
	return nil
}

func checkedAdd(left uint64, right uint64) (uint64, error) {
	if right > math.MaxUint64-left {
		return 0, fmt.Errorf("contextcompiler: token estimate overflow")
	}
	return left + right, nil
}
