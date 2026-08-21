package loopbackchannel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	AdapterProtocolV1             = "loopback-http/v1"
	parametersSchemaV1            = "loopback-http-parameters/v1"
	inboundWireSchemaV1           = "loopback-channel-inbound/v1"
	preparedPayloadSchemaV1       = "loopback-channel-prepared-send/v1"
	deliveryWireSchemaV1          = "loopback-channel-delivery/v1"
	deliveryResponseWireSchemaV1  = "loopback-channel-delivery-result/v1"
	providerReceiptWireSchemaV1   = "loopback-channel-receipt/v1"
	maximumRequestTimeoutMillisV1 = uint32(maximumRequestTimeout / time.Millisecond)
)

var errInvalidProtocol = errors.New("loopbackchannel: invalid protocol value")

type parametersV1 struct {
	SchemaVersion    string `json:"schema_version"`
	InboundPath      string `json:"inbound_path"`
	OutboundURL      string `json:"outbound_url"`
	RequestTimeoutMS uint32 `json:"request_timeout_ms"`
}

// ValidateBindingConfigV1 is the pure Core/Operator preflight for the exact
// compiled loopback protocol. It resolves no Secret, opens no listener and
// performs no network I/O.
func ValidateBindingConfigV1(config moduleapi.ChannelBindingConfigV1) error {
	frozen, _, err := moduleapi.NewChannelBindingConfigV1(config)
	if err != nil {
		return fmt.Errorf("%w: config: %v", errInvalidProtocol, err)
	}
	if frozen.AdapterProtocol != AdapterProtocolV1 {
		return fmt.Errorf(
			"%w: adapter_protocol must be %q",
			errInvalidProtocol,
			AdapterProtocolV1,
		)
	}
	if _, err := restoreParameters(frozen.Parameters); err != nil {
		return err
	}
	return nil
}

type inboundWireV1 struct {
	SchemaVersion   string          `json:"schema_version"`
	EndpointID      string          `json:"endpoint_id"`
	ProviderEventID string          `json:"provider_event_id"`
	ExternalUserID  string          `json:"external_user_id"`
	Message         string          `json:"message"`
	ReplyTarget     json.RawMessage `json:"reply_target"`
	CursorBefore    json.RawMessage `json:"cursor_before"`
	CursorAfter     json.RawMessage `json:"cursor_after"`
}

// replyTargetV1 is the only reply target understood by the first-party
// loopback adapter. Both identities are mandatory and must match the frozen
// Workspace Endpoint; S2 has no deployed legacy wire to preserve.
type replyTargetV1 struct {
	AccountID      string `json:"account_id"`
	ConversationID string `json:"conversation_id"`
}

type preparedPayloadV1 struct {
	SchemaVersion string `json:"schema_version"`
	Message       string `json:"message"`
}

type deliveryWireV1 struct {
	SchemaVersion string          `json:"schema_version"`
	AttemptID     string          `json:"attempt_id"`
	EndpointID    string          `json:"endpoint_id"`
	IngressKey    string          `json:"ingress_key"`
	ReplyTarget   json.RawMessage `json:"reply_target"`
	Message       string          `json:"message"`
}

type deliveryResponseWireV1 struct {
	SchemaVersion       string `json:"schema_version"`
	AttemptID           string `json:"attempt_id"`
	Outcome             string `json:"outcome"`
	ExternalOperationID string `json:"external_operation_id,omitempty"`
	ErrorClassification string `json:"error_classification,omitempty"`
}

type providerReceiptWireV1 struct {
	SchemaVersion       string `json:"schema_version"`
	ExternalOperationID string `json:"external_operation_id"`
}

func restoreParameters(canonical json.RawMessage) (parametersV1, error) {
	var parameters parametersV1
	if err := decodeStrictCanonical(canonical, moduleapi.MaxConfigBytes, &parameters); err != nil {
		return parametersV1{}, fmt.Errorf("%w: parameters: %v", errInvalidProtocol, err)
	}
	if parameters.SchemaVersion != parametersSchemaV1 {
		return parametersV1{}, fmt.Errorf("%w: parameters schema_version must be %q", errInvalidProtocol, parametersSchemaV1)
	}
	if err := validateInboundPath(parameters.InboundPath); err != nil {
		return parametersV1{}, err
	}
	if _, err := parseLoopbackEndpoint(parameters.OutboundURL); err != nil {
		return parametersV1{}, err
	}
	if parameters.RequestTimeoutMS == 0 || parameters.RequestTimeoutMS > maximumRequestTimeoutMillisV1 {
		return parametersV1{}, fmt.Errorf("%w: request_timeout_ms must be between 1 and %d", errInvalidProtocol, maximumRequestTimeoutMillisV1)
	}
	return parameters, nil
}

func validateInboundPath(value string) error {
	if value == "" || value != strings.TrimSpace(value) || value[0] != '/' ||
		path.Clean(value) != value || strings.Contains(value, "//") ||
		strings.ContainsAny(value, "?#") {
		return fmt.Errorf("%w: inbound_path must be an absolute canonical path", errInvalidProtocol)
	}
	return nil
}

func decodeStrictCanonical(canonical []byte, maximum int, target any) error {
	if len(canonical) == 0 || len(canonical) > maximum {
		return fmt.Errorf("wire must contain between 1 and %d bytes", maximum)
	}
	checked, err := moduleapi.CanonicalJSONWithLimits(
		canonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: 32,
			MaxNodes: maximum,
		},
	)
	if err != nil {
		return err
	}
	if !bytes.Equal(checked, canonical) {
		return errors.New("wire must use RFC 8785 canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("wire contains trailing JSON")
		}
		return err
	}
	return nil
}

func canonicalWire(value any, maximum int) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		raw,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: 32,
			MaxNodes: maximum,
		},
	)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(canonical), nil
}
