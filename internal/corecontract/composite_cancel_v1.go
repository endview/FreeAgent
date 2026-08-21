package corecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const RunCancellationRequestSchemaVersionV1 = "cancel-request/v1"

const (
	CancellationReasonUserRequestV1     = "USER_REQUEST"
	CancellationReasonOperatorRequestV1 = "OPERATOR_REQUEST"
	CancellationReasonPolicyEnforcedV1  = "POLICY_ENFORCED"
	CancellationReasonShutdownV1        = "SHUTDOWN"
)

// RunCancellationRequestV1 is the single immutable cancellation request for
// an ordinary Run or a frozen composite family. RootRunID names the ordinary
// Run when Scope is run and the composite root when Scope is family. The
// request deliberately contains neither a caller-generated request identity
// nor wall-clock metadata, so an exact retry has one canonical identity.
type RunCancellationRequestV1 struct {
	SchemaVersion      string `json:"schema_version"`
	RootRunID          string `json:"root_run_id"`
	RootManifestDigest string `json:"root_manifest_digest"`
	Scope              string `json:"scope"`
	ReasonCode         string `json:"reason_code"`
}

func NewRunCancellationRequestV1(
	input RunCancellationRequestV1,
) (RunCancellationRequestV1, []byte, error) {
	if input.SchemaVersion != RunCancellationRequestSchemaVersionV1 {
		return RunCancellationRequestV1{}, nil, fmt.Errorf(
			"corecontract: Run cancellation schema version must be %q",
			RunCancellationRequestSchemaVersionV1,
		)
	}
	if !validOpaque(input.RootRunID, maxOpaqueIDBytes) ||
		!moduleapi.ValidSHA256(input.RootManifestDigest) {
		return RunCancellationRequestV1{}, nil, fmt.Errorf(
			"corecontract: invalid Run cancellation root identity",
		)
	}
	if input.Scope != CancellationScopeRunV1 &&
		input.Scope != CancellationScopeFamilyV1 {
		return RunCancellationRequestV1{}, nil, fmt.Errorf(
			"corecontract: Run cancellation scope must be %q or %q",
			CancellationScopeRunV1,
			CancellationScopeFamilyV1,
		)
	}
	if !validRunCancellationReasonV1(input.ReasonCode) {
		return RunCancellationRequestV1{}, nil, fmt.Errorf(
			"corecontract: unsupported Run cancellation reason %q",
			input.ReasonCode,
		)
	}
	canonical, err := canonicalJSON(input)
	if err != nil {
		return RunCancellationRequestV1{}, nil, err
	}
	return input, canonical, nil
}

func RestoreRunCancellationRequestV1(
	canonical []byte,
) (RunCancellationRequestV1, error) {
	var decoded RunCancellationRequestV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return RunCancellationRequestV1{}, fmt.Errorf(
			"corecontract: decode Run cancellation: %w",
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return RunCancellationRequestV1{}, fmt.Errorf(
			"corecontract: trailing Run cancellation value",
		)
	}
	frozen, rebuilt, err := NewRunCancellationRequestV1(decoded)
	if err != nil {
		return RunCancellationRequestV1{}, err
	}
	if !bytes.Equal(rebuilt, canonical) {
		return RunCancellationRequestV1{}, fmt.Errorf(
			"corecontract: Run cancellation is not canonical",
		)
	}
	return frozen, nil
}

func validRunCancellationReasonV1(reason string) bool {
	switch reason {
	case CancellationReasonUserRequestV1,
		CancellationReasonOperatorRequestV1,
		CancellationReasonPolicyEnforcedV1,
		CancellationReasonShutdownV1:
		return true
	default:
		return false
	}
}
