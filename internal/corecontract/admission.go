package corecontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	AdmissionIntentSchemaVersionV1 = "admission-intent/v1"
	admissionIntentDigestDomain    = "freeagent.admission-intent/v1"
)

// AdmissionIntentV1 is the stable, explicit ingress intent used for
// idempotency. It deliberately excludes generated Run/Attempt IDs, current
// Control/Catalog resolution and compiled Snapshot/Manifest output.
type AdmissionIntentV1 struct {
	SchemaVersion     string                    `json:"schema_version"`
	TenantID          string                    `json:"tenant_id"`
	AdmissionKey      string                    `json:"admission_key"`
	PrincipalID       string                    `json:"principal_id"`
	WorkspaceID       string                    `json:"workspace_id"`
	AgentID           string                    `json:"agent_id"`
	ProfileID         string                    `json:"profile_id"`
	ChannelEndpointID string                    `json:"channel_endpoint_id,omitempty"`
	ConversationTurn  *ConversationTurnIntentV1 `json:"conversation_turn,omitempty"`
	TaskInputRef      string                    `json:"task_input_ref"`
	RequestedPorts    []moduleapi.PortRef       `json:"requested_ports"`
	Deadline          time.Time                 `json:"deadline"`
	CancellationScope string                    `json:"cancellation_scope"`
	ExplicitLimits    json.RawMessage           `json:"explicit_limits"`
}

// NewAdmissionIntentV1 freezes a stable ingress intent and returns its exact
// canonical bytes and digest. RequestedPorts are a capability set here; their
// order does not choose providers or establish Binding order.
func NewAdmissionIntentV1(
	input AdmissionIntentV1,
) (AdmissionIntentV1, []byte, string, error) {
	if input.SchemaVersion != AdmissionIntentSchemaVersionV1 {
		return AdmissionIntentV1{}, nil, "", fmt.Errorf(
			"corecontract: admission intent schema version must be %q",
			AdmissionIntentSchemaVersionV1,
		)
	}
	for name, value := range map[string]string{
		"tenant ID":          input.TenantID,
		"admission key":      input.AdmissionKey,
		"principal ID":       input.PrincipalID,
		"workspace ID":       input.WorkspaceID,
		"agent ID":           input.AgentID,
		"profile ID":         input.ProfileID,
		"cancellation scope": input.CancellationScope,
	} {
		if !validOpaque(value, maxOpaqueIDBytes) {
			return AdmissionIntentV1{}, nil, "", fmt.Errorf(
				"corecontract: invalid admission intent %s",
				name,
			)
		}
	}
	if err := ValidateCancellationScopeV1(input.CancellationScope); err != nil {
		return AdmissionIntentV1{}, nil, "", err
	}
	if input.ChannelEndpointID != "" &&
		!validOpaque(input.ChannelEndpointID, maxOpaqueIDBytes) {
		return AdmissionIntentV1{}, nil, "", fmt.Errorf(
			"corecontract: invalid admission intent channel endpoint ID",
		)
	}
	if input.ConversationTurn != nil {
		if input.ChannelEndpointID != "" {
			return AdmissionIntentV1{}, nil, "", fmt.Errorf(
				"corecontract: Channel and Conversation ingress cannot share one admission intent",
			)
		}
		if err := input.ConversationTurn.Validate(); err != nil {
			return AdmissionIntentV1{}, nil, "", err
		}
	}
	if !moduleapi.ValidSHA256(input.TaskInputRef) {
		return AdmissionIntentV1{}, nil, "", fmt.Errorf(
			"corecontract: invalid admission intent task input ref",
		)
	}
	if input.Deadline.IsZero() {
		return AdmissionIntentV1{}, nil, "", fmt.Errorf(
			"corecontract: admission intent deadline is required",
		)
	}
	if input.ExplicitLimits == nil {
		input.ExplicitLimits = json.RawMessage(`{}`)
	}
	canonicalLimits, err := moduleapi.CanonicalJSON(input.ExplicitLimits)
	if err != nil ||
		len(canonicalLimits) == 0 ||
		canonicalLimits[0] != '{' ||
		!bytes.Equal(canonicalLimits, input.ExplicitLimits) {
		return AdmissionIntentV1{}, nil, "", fmt.Errorf(
			"corecontract: admission intent explicit limits must be a canonical JSON object",
		)
	}

	ports := append([]moduleapi.PortRef(nil), input.RequestedPorts...)
	for index, port := range ports {
		if err := port.Validate(); err != nil {
			return AdmissionIntentV1{}, nil, "", fmt.Errorf(
				"corecontract: admission requested port %d: %w",
				index,
				err,
			)
		}
	}
	sort.Slice(ports, func(left, right int) bool {
		if ports[left].Name != ports[right].Name {
			return ports[left].Name < ports[right].Name
		}
		return ports[left].ExactVersion < ports[right].ExactVersion
	})
	for index := 1; index < len(ports); index++ {
		if ports[index] == ports[index-1] {
			return AdmissionIntentV1{}, nil, "", fmt.Errorf(
				"corecontract: duplicate admission requested port %s/%s",
				ports[index].Name,
				ports[index].ExactVersion,
			)
		}
	}

	frozen := input
	frozen.ConversationTurn = cloneConversationTurnIntentV1(input.ConversationTurn)
	frozen.RequestedPorts = ports
	if frozen.RequestedPorts == nil {
		frozen.RequestedPorts = []moduleapi.PortRef{}
	}
	frozen.Deadline = input.Deadline.Round(0).UTC()
	frozen.ExplicitLimits = bytes.Clone(canonicalLimits)
	canonical, err := canonicalJSON(frozen)
	if err != nil {
		return AdmissionIntentV1{}, nil, "", err
	}
	digest := moduleapi.Digest(admissionIntentDigestDomain, canonical)
	return cloneAdmissionIntent(frozen), canonical, digest, nil
}

// RestoreAdmissionIntentV1 restores exact canonical bytes and verifies the
// externally stored AdmissionIntentDigest.
func RestoreAdmissionIntentV1(
	canonical []byte,
	expectedDigest string,
) (AdmissionIntentV1, error) {
	if !moduleapi.ValidSHA256(expectedDigest) {
		return AdmissionIntentV1{}, fmt.Errorf(
			"corecontract: invalid expected admission intent digest",
		)
	}
	if err := requireExactCanonical(canonical); err != nil {
		return AdmissionIntentV1{}, err
	}
	var decoded AdmissionIntentV1
	if err := decodeStrict(canonical, &decoded); err != nil {
		return AdmissionIntentV1{}, err
	}
	frozen, rebuiltCanonical, rebuiltDigest, err :=
		NewAdmissionIntentV1(decoded)
	if err != nil {
		return AdmissionIntentV1{}, err
	}
	if !bytes.Equal(rebuiltCanonical, canonical) ||
		rebuiltDigest != expectedDigest {
		return AdmissionIntentV1{}, fmt.Errorf(
			"corecontract: admission intent does not match its digest",
		)
	}
	return frozen, nil
}

func cloneAdmissionIntent(intent AdmissionIntentV1) AdmissionIntentV1 {
	intent.ConversationTurn = cloneConversationTurnIntentV1(intent.ConversationTurn)
	intent.RequestedPorts = append(
		[]moduleapi.PortRef(nil),
		intent.RequestedPorts...,
	)
	intent.ExplicitLimits = bytes.Clone(intent.ExplicitLimits)
	return intent
}
