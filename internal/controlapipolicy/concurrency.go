package controlapipolicy

import (
	"encoding/json"
	"fmt"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	MinimumIdempotencyKeyBytesV1 = 16
	MaximumIdempotencyKeyBytesV1 = 128

	IdempotencyKeyDigestDomainV1 = "freeagent.control-idempotency-key/v1"
	operationBodyDomainPrefixV1  = "freeagent.control-operation-body/"
	operationBodyDomainSuffixV1  = "/v1"

	MaximumOperationBodyBytesV1 = 1 << 20
	MaximumOperationBodyDepthV1 = 64
	MaximumOperationBodyNodesV1 = 4096
)

// DigestIdempotencyKeyV1 validates the raw transport value and returns only a
// domain-separated digest. It intentionally does not trim, normalize case, or
// expose the raw key through another type.
func DigestIdempotencyKeyV1(raw string) (string, error) {
	if len(raw) < MinimumIdempotencyKeyBytesV1 ||
		len(raw) > MaximumIdempotencyKeyBytesV1 {
		return "", fmt.Errorf(
			"controlapipolicy: idempotency key length must be between %d and %d bytes",
			MinimumIdempotencyKeyBytesV1,
			MaximumIdempotencyKeyBytesV1,
		)
	}
	for index := range len(raw) {
		if raw[index] < 0x20 || raw[index] > 0x7e {
			return "", fmt.Errorf(
				"controlapipolicy: idempotency key must contain printable ASCII only",
			)
		}
	}
	return moduleapi.Digest(IdempotencyKeyDigestDomainV1, []byte(raw)), nil
}

// DigestOperationBodyV1 canonicalizes one bounded JSON object and hashes it in
// an operation-specific domain. The caller must first validate operation
// membership against the closed operation enum in the API contract.
func DigestOperationBodyV1(
	operation controlapicontract.ControlOperationV1,
	body json.RawMessage,
) (json.RawMessage, string, error) {
	if !validControlOperationV1(operation) {
		return nil, "", fmt.Errorf("controlapipolicy: invalid operation token")
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		body,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaximumOperationBodyBytesV1,
			MaxDepth: MaximumOperationBodyDepthV1,
			MaxNodes: MaximumOperationBodyNodesV1,
		},
	)
	if err != nil || len(canonical) == 0 || canonical[0] != '{' {
		return nil, "", fmt.Errorf(
			"controlapipolicy: operation body must be a bounded JSON object",
		)
	}
	domain := operationBodyDomainPrefixV1 + string(operation) + operationBodyDomainSuffixV1
	return json.RawMessage(append([]byte(nil), canonical...)),
		moduleapi.Digest(domain, canonical), nil
}

func validControlOperationV1(value controlapicontract.ControlOperationV1) bool {
	switch value {
	case controlapicontract.OperationModuleApplyV1,
		controlapicontract.OperationModuleDisableV1,
		controlapicontract.OperationModuleUpgradeReviewV1,
		controlapicontract.OperationModuleUpgradeApplyV1,
		controlapicontract.OperationLearningProposalReviewV1,
		controlapicontract.OperationLearningCycleRunV1:
		return true
	default:
		return false
	}
}

// StrongETagV1 quotes the exact lowercase digest from the authoritative body
// precondition contract. Revision, kind, and ID remain mandatory Store compare
// inputs; an ETag never replaces the complete expected reference.
func StrongETagV1(ref controlapicontract.ExpectedResourceRefV1) (string, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	return `"` + ref.Digest + `"`, nil
}

// ValidateIfMatchV1 requires one exact strong validator matching the expected
// reference in the canonical operation body. Wildcards, weak validators,
// lists, trimming, and case folding are forbidden.
func ValidateIfMatchV1(
	ifMatch string,
	bodyExpected controlapicontract.ExpectedResourceRefV1,
) error {
	if ifMatch == "" {
		return fmt.Errorf("controlapipolicy: If-Match is required")
	}
	want, err := StrongETagV1(bodyExpected)
	if err != nil {
		return err
	}
	if ifMatch != want {
		return fmt.Errorf("controlapipolicy: If-Match does not match body expected reference")
	}
	return nil
}
