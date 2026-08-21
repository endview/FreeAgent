package moduleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ChannelBindingConfigSchemaV1         = "channel-binding-config/v1"
	ChannelAuthorityCeilingSchemaV1      = "channel-authority-ceiling/v1"
	ChannelInboundEnvelopeSchemaV1       = "channel-inbound-envelope/v1"
	ChannelPrepareSendRequestSchemaV1    = "channel-prepare-send-request/v1"
	ChannelSendProposalSchemaV1          = "channel-send-proposal/v1"
	ChannelExecutionRequestSchemaV1      = "channel-execution-request/v1"
	ChannelExecutionResultSchemaV1       = "channel-execution-result/v1"
	ChannelEndpointBindingDigestDomainV1 = "freeagent.channel-endpoint-binding/v1"

	MaxChannelMessageBytesV1         = 64 << 10
	MaxChannelCursorBytesV1          = 16 << 10
	MaxChannelReplyTargetBytesV1     = 16 << 10
	MaxChannelPreparedPayloadBytesV1 = 64 << 10
	MaxChannelProviderReceiptBytesV1 = 64 << 10
	MaxChannelUnknownReasonBytesV1   = 1024

	channelWireMaxDepthV1              = 64
	channelWireMaxNodesV1              = 64 << 10
	channelProposalDigestDomainV1      = "freeagent.channel-send-proposal/v1"
	channelAssistantTextDigestDomainV1 = "freeagent.channel-assistant-text/v1"
)

// CanonicalChannelEndpointBindingV1 freezes the exact resolved
// channel.transport/v1 binding and returns its provider-neutral canonical
// bytes plus digest. Secret material is not part of PortBinding; only the
// ConfigRef containing a SecretRef is covered.
func CanonicalChannelEndpointBindingV1(
	binding PortBinding,
) ([]byte, string, error) {
	plan, err := NewPortPlan(PortPlan{
		Port: PortRef{
			Name:         PortNameChannelTransport,
			ExactVersion: PortVersionV1,
		},
		Bindings: []PortBinding{binding},
	})
	if err != nil {
		return nil, "", fmt.Errorf(
			"channel endpoint binding: %w",
			err,
		)
	}
	encoded, err := json.Marshal(plan.Bindings[0])
	if err != nil {
		return nil, "", fmt.Errorf(
			"encode channel endpoint binding: %w",
			err,
		)
	}
	canonical, err := CanonicalJSONWithLimits(encoded, CanonicalJSONLimits{
		MaxBytes: MaxConfigBytes,
		MaxDepth: channelWireMaxDepthV1,
		MaxNodes: channelWireMaxNodesV1,
	})
	if err != nil {
		return nil, "", fmt.Errorf(
			"canonicalize channel endpoint binding: %w",
			err,
		)
	}
	return bytes.Clone(canonical), Digest(
		ChannelEndpointBindingDigestDomainV1,
		canonical,
	), nil
}

// ComputeChannelEndpointBindingDigestV1 returns only the exact binding
// digest for callers that already persist canonical binding bytes elsewhere.
func ComputeChannelEndpointBindingDigestV1(
	binding PortBinding,
) (string, error) {
	_, digest, err := CanonicalChannelEndpointBindingV1(binding)
	return digest, err
}

// ChannelBindingConfigV1 is consumer-owned provider-neutral configuration.
// It persists only a SecretRef; secret material is resolved at the invocation
// boundary and must never be embedded in Parameters.
type ChannelBindingConfigV1 struct {
	SchemaVersion   string          `json:"schema_version"`
	AdapterProtocol string          `json:"adapter_protocol"`
	SecretRef       string          `json:"secret_ref"`
	Parameters      json.RawMessage `json:"parameters"`
}

func (config ChannelBindingConfigV1) Validate() error {
	_, _, err := NewChannelBindingConfigV1(config)
	return err
}

func NewChannelBindingConfigV1(
	input ChannelBindingConfigV1,
) (ChannelBindingConfigV1, []byte, error) {
	if input.SchemaVersion != ChannelBindingConfigSchemaV1 {
		return ChannelBindingConfigV1{}, nil, fmt.Errorf(
			"channel binding config schema_version must be %q",
			ChannelBindingConfigSchemaV1,
		)
	}
	if err := validateOpaqueID(
		"channel binding config adapter_protocol",
		input.AdapterProtocol,
	); err != nil {
		return ChannelBindingConfigV1{}, nil, err
	}
	if err := validateOpaqueID(
		"channel binding config secret_ref",
		input.SecretRef,
	); err != nil {
		return ChannelBindingConfigV1{}, nil, err
	}
	parametersInput := input.Parameters
	if len(bytes.TrimSpace(parametersInput)) == 0 {
		parametersInput = json.RawMessage(`{}`)
	}
	parameters, err := CanonicalJSONWithLimits(
		parametersInput,
		CanonicalJSONLimits{
			MaxBytes: MaxConfigBytes,
			MaxDepth: channelWireMaxDepthV1,
			MaxNodes: channelWireMaxNodesV1,
		},
	)
	if err != nil || len(parameters) == 0 || parameters[0] != '{' {
		return ChannelBindingConfigV1{}, nil, fmt.Errorf(
			"channel binding config parameters must be a bounded JSON object",
		)
	}
	var decoded any
	if err := json.Unmarshal(parameters, &decoded); err != nil {
		return ChannelBindingConfigV1{}, nil, fmt.Errorf(
			"decode channel binding config parameters: %w",
			err,
		)
	}
	if err := rejectChannelSecretMaterialV1(decoded); err != nil {
		return ChannelBindingConfigV1{}, nil, err
	}
	frozen := input
	frozen.Parameters = bytes.Clone(parameters)
	canonical, err := marshalCanonicalChannelWireV1(frozen, MaxConfigBytes)
	if err != nil {
		return ChannelBindingConfigV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreChannelBindingConfigV1(
	canonical []byte,
) (ChannelBindingConfigV1, error) {
	var decoded ChannelBindingConfigV1
	if err := decodeExactChannelWireV1(
		canonical,
		MaxConfigBytes,
		&decoded,
	); err != nil {
		return ChannelBindingConfigV1{}, err
	}
	restored, rebuilt, err := NewChannelBindingConfigV1(decoded)
	if err != nil {
		return ChannelBindingConfigV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ChannelBindingConfigV1{}, fmt.Errorf(
			"channel binding config is not frozen canonically",
		)
	}
	return restored, nil
}

// ChannelAuthorityCeilingV1 is Core-owned deny-only authority. Endpoint and
// Workspace lists are exact sets; the v1 contract has no wildcard grant.
type ChannelAuthorityCeilingV1 struct {
	SchemaVersion       string   `json:"schema_version"`
	TenantID            string   `json:"tenant_id"`
	AllowedWorkspaceIDs []string `json:"allowed_workspace_ids"`
	AllowedEndpointIDs  []string `json:"allowed_endpoint_ids"`
	AllowReceive        bool     `json:"allow_receive"`
	AllowSend           bool     `json:"allow_send"`
	MaxMessageBytes     uint32   `json:"max_message_bytes"`
}

func (ceiling ChannelAuthorityCeilingV1) Validate() error {
	_, _, err := NewChannelAuthorityCeilingV1(ceiling)
	return err
}

func NewChannelAuthorityCeilingV1(
	input ChannelAuthorityCeilingV1,
) (ChannelAuthorityCeilingV1, []byte, error) {
	if input.SchemaVersion != ChannelAuthorityCeilingSchemaV1 {
		return ChannelAuthorityCeilingV1{}, nil, fmt.Errorf(
			"channel authority ceiling schema_version must be %q",
			ChannelAuthorityCeilingSchemaV1,
		)
	}
	if err := validateOpaqueID(
		"channel authority ceiling tenant_id",
		input.TenantID,
	); err != nil {
		return ChannelAuthorityCeilingV1{}, nil, err
	}
	workspaces, err := canonicalChannelOpaqueSetV1(
		"allowed_workspace_ids",
		input.AllowedWorkspaceIDs,
	)
	if err != nil {
		return ChannelAuthorityCeilingV1{}, nil, err
	}
	endpoints, err := canonicalChannelOpaqueSetV1(
		"allowed_endpoint_ids",
		input.AllowedEndpointIDs,
	)
	if err != nil {
		return ChannelAuthorityCeilingV1{}, nil, err
	}
	if !input.AllowReceive && !input.AllowSend {
		return ChannelAuthorityCeilingV1{}, nil, fmt.Errorf(
			"channel authority ceiling must allow receive, send, or both",
		)
	}
	if input.MaxMessageBytes == 0 ||
		input.MaxMessageBytes > MaxChannelMessageBytesV1 {
		return ChannelAuthorityCeilingV1{}, nil, fmt.Errorf(
			"channel authority ceiling max_message_bytes must be between 1 and %d",
			MaxChannelMessageBytesV1,
		)
	}
	frozen := input
	frozen.AllowedWorkspaceIDs = workspaces
	frozen.AllowedEndpointIDs = endpoints
	canonical, err := marshalCanonicalChannelWireV1(frozen, MaxConfigBytes)
	if err != nil {
		return ChannelAuthorityCeilingV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreChannelAuthorityCeilingV1(
	canonical []byte,
) (ChannelAuthorityCeilingV1, error) {
	var decoded ChannelAuthorityCeilingV1
	if err := decodeExactChannelWireV1(
		canonical,
		MaxConfigBytes,
		&decoded,
	); err != nil {
		return ChannelAuthorityCeilingV1{}, err
	}
	restored, rebuilt, err := NewChannelAuthorityCeilingV1(decoded)
	if err != nil {
		return ChannelAuthorityCeilingV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ChannelAuthorityCeilingV1{}, fmt.Errorf(
			"channel authority ceiling is not frozen canonically",
		)
	}
	return restored, nil
}

// ChannelInboundEnvelopeV1 is the only normalized ingress wire emitted by a
// v1 Adapter after provider authentication. Routing and Core identities are
// deliberately absent: Core resolves them from EndpointID.
type ChannelInboundEnvelopeV1 struct {
	SchemaVersion   string          `json:"schema_version"`
	EndpointID      string          `json:"endpoint_id"`
	ProviderEventID string          `json:"provider_event_id"`
	ExternalUserID  string          `json:"external_user_id"`
	Message         string          `json:"message"`
	ReplyTarget     json.RawMessage `json:"reply_target"`
	CursorBefore    json.RawMessage `json:"cursor_before"`
	CursorAfter     json.RawMessage `json:"cursor_after"`
}

func (envelope ChannelInboundEnvelopeV1) Validate() error {
	_, _, err := NewChannelInboundEnvelopeV1(envelope)
	return err
}

func NewChannelInboundEnvelopeV1(
	input ChannelInboundEnvelopeV1,
) (ChannelInboundEnvelopeV1, []byte, error) {
	if input.SchemaVersion != ChannelInboundEnvelopeSchemaV1 {
		return ChannelInboundEnvelopeV1{}, nil, fmt.Errorf(
			"channel inbound envelope schema_version must be %q",
			ChannelInboundEnvelopeSchemaV1,
		)
	}
	for name, value := range map[string]string{
		"endpoint_id":       input.EndpointID,
		"provider_event_id": input.ProviderEventID,
		"external_user_id":  input.ExternalUserID,
	} {
		if err := validateOpaqueID(
			"channel inbound envelope "+name,
			value,
		); err != nil {
			return ChannelInboundEnvelopeV1{}, nil, err
		}
	}
	if err := validateChannelTextV1(
		"channel inbound envelope message",
		input.Message,
		MaxChannelMessageBytesV1,
		false,
	); err != nil {
		return ChannelInboundEnvelopeV1{}, nil, err
	}
	replyTarget, err := canonicalChannelObjectV1(
		"channel inbound envelope reply_target",
		input.ReplyTarget,
		MaxChannelReplyTargetBytesV1,
	)
	if err != nil {
		return ChannelInboundEnvelopeV1{}, nil, err
	}
	cursorBefore, err := canonicalChannelJSONV1(
		"channel inbound envelope cursor_before",
		input.CursorBefore,
		MaxChannelCursorBytesV1,
	)
	if err != nil {
		return ChannelInboundEnvelopeV1{}, nil, err
	}
	cursorAfter, err := canonicalChannelJSONV1(
		"channel inbound envelope cursor_after",
		input.CursorAfter,
		MaxChannelCursorBytesV1,
	)
	if err != nil {
		return ChannelInboundEnvelopeV1{}, nil, err
	}
	if bytes.Equal(cursorBefore, cursorAfter) {
		return ChannelInboundEnvelopeV1{}, nil, fmt.Errorf(
			"channel inbound envelope cursor_after must differ from cursor_before",
		)
	}
	frozen := input
	frozen.ReplyTarget = replyTarget
	frozen.CursorBefore = cursorBefore
	frozen.CursorAfter = cursorAfter
	canonical, err := marshalCanonicalChannelWireV1(
		frozen,
		MaxChannelMessageBytesV1+MaxConfigBytes,
	)
	if err != nil {
		return ChannelInboundEnvelopeV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreChannelInboundEnvelopeV1(
	canonical []byte,
) (ChannelInboundEnvelopeV1, error) {
	var decoded ChannelInboundEnvelopeV1
	if err := decodeExactChannelWireV1(
		canonical,
		MaxChannelMessageBytesV1+MaxConfigBytes,
		&decoded,
	); err != nil {
		return ChannelInboundEnvelopeV1{}, err
	}
	restored, rebuilt, err := NewChannelInboundEnvelopeV1(decoded)
	if err != nil {
		return ChannelInboundEnvelopeV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ChannelInboundEnvelopeV1{}, fmt.Errorf(
			"channel inbound envelope is not frozen canonically",
		)
	}
	return restored, nil
}

// ChannelPrepareSendRequestV1 is the read-only Adapter input used before
// Core creates a content-addressed proposal. It contains no Attempt, Effect,
// provider identity, permission, or mutable Control/Catalog metadata.
type ChannelPrepareSendRequestV1 struct {
	SchemaVersion string          `json:"schema_version"`
	EndpointID    string          `json:"endpoint_id"`
	ReplyTarget   json.RawMessage `json:"reply_target"`
	AssistantText string          `json:"assistant_text"`
}

func (request ChannelPrepareSendRequestV1) Validate() error {
	_, _, err := NewChannelPrepareSendRequestV1(request)
	return err
}

func NewChannelPrepareSendRequestV1(
	input ChannelPrepareSendRequestV1,
) (ChannelPrepareSendRequestV1, []byte, error) {
	if input.SchemaVersion != ChannelPrepareSendRequestSchemaV1 {
		return ChannelPrepareSendRequestV1{}, nil, fmt.Errorf(
			"channel prepare-send request schema_version must be %q",
			ChannelPrepareSendRequestSchemaV1,
		)
	}
	if err := validateOpaqueID(
		"channel prepare-send request endpoint_id",
		input.EndpointID,
	); err != nil {
		return ChannelPrepareSendRequestV1{}, nil, err
	}
	if err := validateChannelTextV1(
		"channel prepare-send request assistant_text",
		input.AssistantText,
		MaxChannelMessageBytesV1,
		false,
	); err != nil {
		return ChannelPrepareSendRequestV1{}, nil, err
	}
	replyTarget, err := canonicalChannelObjectV1(
		"channel prepare-send request reply_target",
		input.ReplyTarget,
		MaxChannelReplyTargetBytesV1,
	)
	if err != nil {
		return ChannelPrepareSendRequestV1{}, nil, err
	}
	frozen := input
	frozen.ReplyTarget = replyTarget
	canonical, err := marshalCanonicalChannelWireV1(
		frozen,
		MaxChannelMessageBytesV1+MaxChannelReplyTargetBytesV1,
	)
	if err != nil {
		return ChannelPrepareSendRequestV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreChannelPrepareSendRequestV1(
	canonical []byte,
) (ChannelPrepareSendRequestV1, error) {
	var decoded ChannelPrepareSendRequestV1
	if err := decodeExactChannelWireV1(
		canonical,
		MaxChannelMessageBytesV1+MaxChannelReplyTargetBytesV1,
		&decoded,
	); err != nil {
		return ChannelPrepareSendRequestV1{}, err
	}
	restored, rebuilt, err := NewChannelPrepareSendRequestV1(decoded)
	if err != nil {
		return ChannelPrepareSendRequestV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ChannelPrepareSendRequestV1{}, fmt.Errorf(
			"channel prepare-send request is not frozen canonically",
		)
	}
	return restored, nil
}

// ChannelTransportV1 is the public, read-only outbound preparation surface.
// Execute is private to ModuleHost/Gateway. The first HTTP Adapter authenticates
// and decodes inbound requests at its concrete listener boundary, then emits a
// strict ChannelInboundEnvelopeV1 into Core.
type ChannelTransportV1 interface {
	PrepareSend(
		context.Context,
		ChannelPrepareSendRequestV1,
	) (json.RawMessage, error)
}

func CanonicalizeChannelPreparedPayloadV1(
	payload json.RawMessage,
) (json.RawMessage, error) {
	return canonicalChannelObjectV1(
		"channel prepared payload",
		payload,
		MaxChannelPreparedPayloadBytesV1,
	)
}

// ChannelAssistantTextDigestV1 returns the exact digest used by proposals and
// execution requests after applying the public v1 text bounds. Host Adapters
// use it to prove that a prepared payload still carries the authorized text.
func ChannelAssistantTextDigestV1(text string) (string, error) {
	if err := validateChannelTextV1(
		"channel assistant text",
		text,
		MaxChannelMessageBytesV1,
		false,
	); err != nil {
		return "", err
	}
	return Digest(channelAssistantTextDigestDomainV1, []byte(text)), nil
}

// ChannelSendProposalV1 is the Core-owned immutable closure committed before
// Gateway can consume a one-shot permit. The actual text is present only in
// PreparedPayload; AssistantTextDigest closes it without duplicating content.
type ChannelSendProposalV1 struct {
	SchemaVersion        string          `json:"schema_version"`
	MemberSnapshotDigest string          `json:"member_snapshot_digest"`
	EndpointID           string          `json:"endpoint_id"`
	IngressKey           string          `json:"ingress_key"`
	ReplyTarget          json.RawMessage `json:"reply_target"`
	AssistantTextDigest  string          `json:"assistant_text_digest"`
	PreparedPayload      json.RawMessage `json:"prepared_payload"`
}

func NewChannelSendProposalV1(
	memberSnapshotDigest string,
	endpointID string,
	ingressKey string,
	replyTarget json.RawMessage,
	assistantText string,
	preparedPayload json.RawMessage,
) (ChannelSendProposalV1, []byte, string, error) {
	if !ValidSHA256(memberSnapshotDigest) {
		return ChannelSendProposalV1{}, nil, "", fmt.Errorf(
			"channel send proposal requires a valid member_snapshot_digest",
		)
	}
	if err := validateOpaqueID(
		"channel send proposal endpoint_id",
		endpointID,
	); err != nil {
		return ChannelSendProposalV1{}, nil, "", err
	}
	if !ValidSHA256(ingressKey) {
		return ChannelSendProposalV1{}, nil, "", fmt.Errorf(
			"channel send proposal ingress_key must be a lowercase SHA-256 digest",
		)
	}
	if err := validateChannelTextV1(
		"channel send proposal assistant text",
		assistantText,
		MaxChannelMessageBytesV1,
		false,
	); err != nil {
		return ChannelSendProposalV1{}, nil, "", err
	}
	canonicalReplyTarget, err := canonicalChannelObjectV1(
		"channel send proposal reply_target",
		replyTarget,
		MaxChannelReplyTargetBytesV1,
	)
	if err != nil {
		return ChannelSendProposalV1{}, nil, "", err
	}
	canonicalPrepared, err := CanonicalizeChannelPreparedPayloadV1(
		preparedPayload,
	)
	if err != nil {
		return ChannelSendProposalV1{}, nil, "", err
	}
	assistantTextDigest, err := ChannelAssistantTextDigestV1(assistantText)
	if err != nil {
		return ChannelSendProposalV1{}, nil, "", err
	}
	proposal := ChannelSendProposalV1{
		SchemaVersion:        ChannelSendProposalSchemaV1,
		MemberSnapshotDigest: memberSnapshotDigest,
		EndpointID:           endpointID,
		IngressKey:           ingressKey,
		ReplyTarget:          canonicalReplyTarget,
		AssistantTextDigest:  assistantTextDigest,
		PreparedPayload:      canonicalPrepared,
	}
	canonical, err := marshalCanonicalChannelWireV1(
		proposal,
		2*MaxConfigBytes,
	)
	if err != nil {
		return ChannelSendProposalV1{}, nil, "", err
	}
	digest := Digest(channelProposalDigestDomainV1, canonical)
	return cloneChannelSendProposalV1(proposal), canonical, digest, nil
}

func RestoreChannelSendProposalV1(
	canonical []byte,
	expectedDigest string,
) (ChannelSendProposalV1, error) {
	if !ValidSHA256(expectedDigest) {
		return ChannelSendProposalV1{}, fmt.Errorf(
			"channel send proposal expected digest is invalid",
		)
	}
	var decoded ChannelSendProposalV1
	if err := decodeExactChannelWireV1(
		canonical,
		2*MaxConfigBytes,
		&decoded,
	); err != nil {
		return ChannelSendProposalV1{}, err
	}
	if decoded.SchemaVersion != ChannelSendProposalSchemaV1 ||
		!ValidSHA256(decoded.MemberSnapshotDigest) ||
		!ValidSHA256(decoded.IngressKey) ||
		!ValidSHA256(decoded.AssistantTextDigest) {
		return ChannelSendProposalV1{}, fmt.Errorf(
			"channel send proposal identity fields are invalid",
		)
	}
	if err := validateOpaqueID(
		"channel send proposal endpoint_id",
		decoded.EndpointID,
	); err != nil {
		return ChannelSendProposalV1{}, err
	}
	replyTarget, err := canonicalChannelObjectV1(
		"channel send proposal reply_target",
		decoded.ReplyTarget,
		MaxChannelReplyTargetBytesV1,
	)
	if err != nil || !bytes.Equal(replyTarget, decoded.ReplyTarget) {
		return ChannelSendProposalV1{}, fmt.Errorf(
			"channel send proposal reply_target is not canonical",
		)
	}
	prepared, err := CanonicalizeChannelPreparedPayloadV1(
		decoded.PreparedPayload,
	)
	if err != nil || !bytes.Equal(prepared, decoded.PreparedPayload) {
		return ChannelSendProposalV1{}, fmt.Errorf(
			"channel send proposal prepared_payload is not canonical",
		)
	}
	actualDigest, err := ChannelSendProposalDigestV1(canonical)
	if err != nil {
		return ChannelSendProposalV1{}, err
	}
	if actualDigest != expectedDigest {
		return ChannelSendProposalV1{}, fmt.Errorf(
			"channel send proposal does not match its digest",
		)
	}
	return cloneChannelSendProposalV1(decoded), nil
}

// ChannelSendProposalDigestV1 validates an exact canonical proposal wire and
// returns the public content identity used by Store and restart verification.
func ChannelSendProposalDigestV1(canonical []byte) (string, error) {
	var decoded ChannelSendProposalV1
	if err := decodeExactChannelWireV1(
		canonical,
		2*MaxConfigBytes,
		&decoded,
	); err != nil {
		return "", err
	}
	if decoded.SchemaVersion != ChannelSendProposalSchemaV1 ||
		!ValidSHA256(decoded.MemberSnapshotDigest) ||
		!ValidSHA256(decoded.IngressKey) ||
		!ValidSHA256(decoded.AssistantTextDigest) {
		return "", fmt.Errorf(
			"channel send proposal identity fields are invalid",
		)
	}
	if err := validateOpaqueID(
		"channel send proposal endpoint_id",
		decoded.EndpointID,
	); err != nil {
		return "", err
	}
	replyTarget, err := canonicalChannelObjectV1(
		"channel send proposal reply_target",
		decoded.ReplyTarget,
		MaxChannelReplyTargetBytesV1,
	)
	if err != nil || !bytes.Equal(replyTarget, decoded.ReplyTarget) {
		return "", fmt.Errorf(
			"channel send proposal reply_target is not canonical",
		)
	}
	prepared, err := CanonicalizeChannelPreparedPayloadV1(
		decoded.PreparedPayload,
	)
	if err != nil || !bytes.Equal(prepared, decoded.PreparedPayload) {
		return "", fmt.Errorf(
			"channel send proposal prepared_payload is not canonical",
		)
	}
	return Digest(channelProposalDigestDomainV1, canonical), nil
}

// ChannelExecutionRequestV1 is constructed by Gateway only. SDK visibility
// enables an exact Host Adapter wire but grants no right to execute it.
type ChannelExecutionRequestV1 struct {
	SchemaVersion       string          `json:"schema_version"`
	AttemptID           string          `json:"attempt_id"`
	EndpointID          string          `json:"endpoint_id"`
	IngressKey          string          `json:"ingress_key"`
	ProposalDigest      string          `json:"proposal_digest"`
	AssistantTextDigest string          `json:"assistant_text_digest"`
	ReplyTarget         json.RawMessage `json:"reply_target"`
	PreparedPayload     json.RawMessage `json:"prepared_payload"`
}

func (request ChannelExecutionRequestV1) Validate() error {
	_, _, err := NewChannelExecutionRequestV1(request)
	return err
}

func NewChannelExecutionRequestV1(
	input ChannelExecutionRequestV1,
) (ChannelExecutionRequestV1, []byte, error) {
	if input.SchemaVersion != ChannelExecutionRequestSchemaV1 {
		return ChannelExecutionRequestV1{}, nil, fmt.Errorf(
			"channel execution request schema_version must be %q",
			ChannelExecutionRequestSchemaV1,
		)
	}
	for name, value := range map[string]string{
		"attempt_id":  input.AttemptID,
		"endpoint_id": input.EndpointID,
	} {
		if err := validateOpaqueID(
			"channel execution request "+name,
			value,
		); err != nil {
			return ChannelExecutionRequestV1{}, nil, err
		}
	}
	for name, digest := range map[string]string{
		"ingress_key":           input.IngressKey,
		"proposal_digest":       input.ProposalDigest,
		"assistant_text_digest": input.AssistantTextDigest,
	} {
		if !ValidSHA256(digest) {
			return ChannelExecutionRequestV1{}, nil, fmt.Errorf(
				"channel execution request %s must be a lowercase SHA-256 digest",
				name,
			)
		}
	}
	replyTarget, err := canonicalChannelObjectV1(
		"channel execution request reply_target",
		input.ReplyTarget,
		MaxChannelReplyTargetBytesV1,
	)
	if err != nil {
		return ChannelExecutionRequestV1{}, nil, err
	}
	prepared, err := CanonicalizeChannelPreparedPayloadV1(input.PreparedPayload)
	if err != nil {
		return ChannelExecutionRequestV1{}, nil, err
	}
	frozen := input
	frozen.ReplyTarget = replyTarget
	frozen.PreparedPayload = prepared
	canonical, err := marshalCanonicalChannelWireV1(frozen, 2*MaxConfigBytes)
	if err != nil {
		return ChannelExecutionRequestV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreChannelExecutionRequestV1(
	canonical []byte,
) (ChannelExecutionRequestV1, error) {
	var decoded ChannelExecutionRequestV1
	if err := decodeExactChannelWireV1(
		canonical,
		2*MaxConfigBytes,
		&decoded,
	); err != nil {
		return ChannelExecutionRequestV1{}, err
	}
	restored, rebuilt, err := NewChannelExecutionRequestV1(decoded)
	if err != nil {
		return ChannelExecutionRequestV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ChannelExecutionRequestV1{}, fmt.Errorf(
			"channel execution request is not frozen canonically",
		)
	}
	return restored, nil
}

type ChannelExecutionOutcomeV1 string

const (
	ChannelExecutionSucceeded ChannelExecutionOutcomeV1 = "SUCCEEDED"
	ChannelExecutionFailed    ChannelExecutionOutcomeV1 = "FAILED"
	ChannelExecutionUnknown   ChannelExecutionOutcomeV1 = "UNKNOWN"
)

func (outcome ChannelExecutionOutcomeV1) Validate() error {
	switch outcome {
	case ChannelExecutionSucceeded,
		ChannelExecutionFailed,
		ChannelExecutionUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported channel execution outcome %q", outcome)
	}
}

type ChannelExecutionResultV1 struct {
	SchemaVersion       string                    `json:"schema_version"`
	AttemptID           string                    `json:"attempt_id"`
	Outcome             ChannelExecutionOutcomeV1 `json:"outcome"`
	ProviderReceipt     json.RawMessage           `json:"provider_receipt,omitempty"`
	ExternalOperationID string                    `json:"external_operation_id,omitempty"`
	ErrorClassification string                    `json:"error_classification,omitempty"`
	UnknownReason       string                    `json:"unknown_reason,omitempty"`
}

func (result ChannelExecutionResultV1) Validate() error {
	_, _, err := NewChannelExecutionResultV1(result)
	return err
}

func NewChannelExecutionResultV1(
	input ChannelExecutionResultV1,
) (ChannelExecutionResultV1, []byte, error) {
	if input.SchemaVersion != ChannelExecutionResultSchemaV1 {
		return ChannelExecutionResultV1{}, nil, fmt.Errorf(
			"channel execution result schema_version must be %q",
			ChannelExecutionResultSchemaV1,
		)
	}
	if err := validateOpaqueID(
		"channel execution result attempt_id",
		input.AttemptID,
	); err != nil {
		return ChannelExecutionResultV1{}, nil, err
	}
	if err := input.Outcome.Validate(); err != nil {
		return ChannelExecutionResultV1{}, nil, err
	}
	if input.ExternalOperationID != "" {
		if err := validateOpaqueID(
			"channel execution result external_operation_id",
			input.ExternalOperationID,
		); err != nil {
			return ChannelExecutionResultV1{}, nil, err
		}
	}
	if input.ErrorClassification != "" {
		if err := validateOpaqueID(
			"channel execution result error_classification",
			input.ErrorClassification,
		); err != nil {
			return ChannelExecutionResultV1{}, nil, err
		}
	}
	if input.UnknownReason != "" {
		if err := validateChannelTextV1(
			"channel execution result unknown_reason",
			input.UnknownReason,
			MaxChannelUnknownReasonBytesV1,
			false,
		); err != nil {
			return ChannelExecutionResultV1{}, nil, err
		}
	}
	frozen := input
	if len(input.ProviderReceipt) != 0 {
		receipt, err := canonicalChannelObjectV1(
			"channel execution result provider_receipt",
			input.ProviderReceipt,
			MaxChannelProviderReceiptBytesV1,
		)
		if err != nil {
			return ChannelExecutionResultV1{}, nil, err
		}
		frozen.ProviderReceipt = receipt
	} else {
		frozen.ProviderReceipt = nil
	}
	switch input.Outcome {
	case ChannelExecutionSucceeded:
		if input.ErrorClassification != "" || input.UnknownReason != "" {
			return ChannelExecutionResultV1{}, nil, fmt.Errorf(
				"SUCCEEDED channel execution result cannot contain error or unknown fields",
			)
		}
		if input.ExternalOperationID == "" && len(frozen.ProviderReceipt) == 0 {
			return ChannelExecutionResultV1{}, nil, fmt.Errorf(
				"SUCCEEDED channel execution result requires an external operation or provider receipt",
			)
		}
	case ChannelExecutionFailed:
		if input.ErrorClassification == "" || input.UnknownReason != "" ||
			input.ExternalOperationID != "" {
			return ChannelExecutionResultV1{}, nil, fmt.Errorf(
				"FAILED channel execution result requires error_classification and cannot claim an external operation or unknown_reason",
			)
		}
	case ChannelExecutionUnknown:
		if input.ErrorClassification != "" {
			return ChannelExecutionResultV1{}, nil, fmt.Errorf(
				"UNKNOWN channel execution result cannot contain error_classification",
			)
		}
		if input.ExternalOperationID == "" && len(frozen.ProviderReceipt) == 0 &&
			input.UnknownReason == "" {
			return ChannelExecutionResultV1{}, nil, fmt.Errorf(
				"UNKNOWN channel execution result requires external operation, receipt, or unknown_reason",
			)
		}
	}
	canonical, err := marshalCanonicalChannelWireV1(frozen, 2*MaxConfigBytes)
	if err != nil {
		return ChannelExecutionResultV1{}, nil, err
	}
	return frozen, canonical, nil
}

func RestoreChannelExecutionResultV1(
	canonical []byte,
) (ChannelExecutionResultV1, error) {
	var decoded ChannelExecutionResultV1
	if err := decodeExactChannelWireV1(
		canonical,
		2*MaxConfigBytes,
		&decoded,
	); err != nil {
		return ChannelExecutionResultV1{}, err
	}
	restored, rebuilt, err := NewChannelExecutionResultV1(decoded)
	if err != nil {
		return ChannelExecutionResultV1{}, err
	}
	if !bytes.Equal(canonical, rebuilt) {
		return ChannelExecutionResultV1{}, fmt.Errorf(
			"channel execution result is not frozen canonically",
		)
	}
	return restored, nil
}

func ValidateChannelExecutionResultForRequestV1(
	request ChannelExecutionRequestV1,
	result ChannelExecutionResultV1,
) error {
	frozenRequest, _, err := NewChannelExecutionRequestV1(request)
	if err != nil {
		return err
	}
	frozenResult, _, err := NewChannelExecutionResultV1(result)
	if err != nil {
		return err
	}
	if frozenRequest.AttemptID != frozenResult.AttemptID {
		return fmt.Errorf(
			"channel execution result attempt_id does not match request",
		)
	}
	return nil
}

func canonicalChannelOpaqueSetV1(
	name string,
	input []string,
) ([]string, error) {
	if len(input) == 0 || len(input) > MaxManifestEntries {
		return nil, fmt.Errorf(
			"channel authority ceiling %s must contain between 1 and %d values",
			name,
			MaxManifestEntries,
		)
	}
	values := append([]string(nil), input...)
	for index, value := range values {
		if value == "*" {
			return nil, fmt.Errorf(
				"channel authority ceiling %s[%d] cannot use a wildcard",
				name,
				index,
			)
		}
		if err := validateOpaqueID(
			"channel authority ceiling "+name,
			value,
		); err != nil {
			return nil, err
		}
	}
	sort.Strings(values)
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return nil, fmt.Errorf(
				"channel authority ceiling %s must be unique",
				name,
			)
		}
	}
	return values, nil
}

func rejectChannelSecretMaterialV1(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.Map(func(character rune) rune {
				if unicode.IsLetter(character) || unicode.IsDigit(character) {
					return unicode.ToLower(character)
				}
				return -1
			}, key)
			for _, forbidden := range []string{
				"authorization",
				"credential",
				"password",
				"apikey",
				"accesstoken",
				"bearertoken",
				"refreshtoken",
				"clientsecret",
				"secretvalue",
				"secret",
				"token",
			} {
				if strings.Contains(normalized, forbidden) {
					return fmt.Errorf(
						"channel binding config parameters must not contain secret material key %q",
						key,
					)
				}
			}
			if err := rejectChannelSecretMaterialV1(nested); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := rejectChannelSecretMaterialV1(nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateChannelTextV1(
	name string,
	value string,
	maximum int,
	allowEmpty bool,
) error {
	if !utf8.ValidString(value) || value != CanonicalText(value) {
		return fmt.Errorf("%s must be canonical UTF-8 using Unicode NFC", name)
	}
	if !allowEmpty && value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if len(value) > maximum {
		return fmt.Errorf("%s exceeds %d bytes", name, maximum)
	}
	for _, character := range value {
		if (character < 0x20 && character != '\n' && character != '\r' &&
			character != '\t') || character == 0x7f {
			return fmt.Errorf("%s contains an unsupported control character", name)
		}
	}
	return nil
}

func canonicalChannelJSONV1(
	name string,
	input json.RawMessage,
	maximum int,
) (json.RawMessage, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("%s must not be empty", name)
	}
	canonical, err := CanonicalJSONWithLimits(
		input,
		CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: channelWireMaxDepthV1,
			MaxNodes: channelWireMaxNodesV1,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return bytes.Clone(canonical), nil
}

func canonicalChannelObjectV1(
	name string,
	input json.RawMessage,
	maximum int,
) (json.RawMessage, error) {
	canonical, err := canonicalChannelJSONV1(name, input, maximum)
	if err != nil {
		return nil, err
	}
	if len(canonical) == 0 || canonical[0] != '{' {
		return nil, fmt.Errorf("%s must be a JSON object", name)
	}
	return canonical, nil
}

func marshalCanonicalChannelWireV1(
	value any,
	maximum int,
) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal channel.transport/v1 wire value: %w", err)
	}
	canonical, err := CanonicalJSONWithLimits(
		encoded,
		CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: channelWireMaxDepthV1,
			MaxNodes: channelWireMaxNodesV1,
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"canonicalize channel.transport/v1 wire value: %w",
			err,
		)
	}
	return bytes.Clone(canonical), nil
}

func decodeExactChannelWireV1(
	canonical []byte,
	maximum int,
	target any,
) error {
	if len(canonical) == 0 || len(canonical) > maximum {
		return fmt.Errorf(
			"channel.transport/v1 wire value must contain between 1 and %d bytes",
			maximum,
		)
	}
	checked, err := CanonicalJSONWithLimits(
		canonical,
		CanonicalJSONLimits{
			MaxBytes: maximum,
			MaxDepth: channelWireMaxDepthV1,
			MaxNodes: channelWireMaxNodesV1,
		},
	)
	if err != nil || !bytes.Equal(checked, canonical) {
		return fmt.Errorf(
			"channel.transport/v1 wire value is not canonical JSON",
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf(
			"decode channel.transport/v1 wire value: %w",
			err,
		)
	}
	return nil
}

func cloneChannelSendProposalV1(
	proposal ChannelSendProposalV1,
) ChannelSendProposalV1 {
	proposal.ReplyTarget = bytes.Clone(proposal.ReplyTarget)
	proposal.PreparedPayload = bytes.Clone(proposal.PreparedPayload)
	return proposal
}
