package loopbackchannel

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// ExecutePrepared issues exactly one non-replayable POST. Every ambiguous
// condition after entering http.Client.Do is returned as an explicit UNKNOWN
// result for the original Attempt, never as a retry instruction.
func (adapter *Adapter) ExecutePrepared(
	ctx context.Context,
	request moduleapi.ChannelExecutionRequestV1,
) (moduleapi.ChannelExecutionResultV1, error) {
	if err := validateAdapterContext(adapter, ctx, ErrExecute); err != nil {
		return moduleapi.ChannelExecutionResultV1{}, err
	}
	frozen, _, err := moduleapi.NewChannelExecutionRequestV1(request)
	if err != nil {
		return moduleapi.ChannelExecutionResultV1{}, fmt.Errorf("%w: request: %v", ErrExecute, err)
	}
	if frozen.EndpointID != adapter.endpointID {
		return failedExecution(frozen.AttemptID, "CHANNEL_ENDPOINT_MISMATCH")
	}
	if err := adapter.validateReplyTarget(frozen.ReplyTarget); err != nil {
		return failedExecution(frozen.AttemptID, "CHANNEL_REPLY_TARGET_MISMATCH")
	}
	var prepared preparedPayloadV1
	if err := decodeStrictCanonical(
		frozen.PreparedPayload,
		moduleapi.MaxChannelPreparedPayloadBytesV1,
		&prepared,
	); err != nil || prepared.SchemaVersion != preparedPayloadSchemaV1 || prepared.Message == "" {
		return failedExecution(frozen.AttemptID, "CHANNEL_PREPARED_PAYLOAD_REJECTED")
	}
	assistantDigest, err := moduleapi.ChannelAssistantTextDigestV1(prepared.Message)
	if err != nil || assistantDigest != frozen.AssistantTextDigest {
		return failedExecution(frozen.AttemptID, "CHANNEL_ASSISTANT_DIGEST_MISMATCH")
	}
	body, err := canonicalWire(deliveryWireV1{
		SchemaVersion: deliveryWireSchemaV1,
		AttemptID:     frozen.AttemptID,
		EndpointID:    frozen.EndpointID,
		IngressKey:    frozen.IngressKey,
		ReplyTarget:   bytes.Clone(frozen.ReplyTarget),
		Message:       prepared.Message,
	}, moduleapi.MaxChannelMessageBytesV1+moduleapi.MaxConfigBytes)
	if err != nil {
		return failedExecution(frozen.AttemptID, "CHANNEL_DELIVERY_WIRE_REJECTED")
	}
	resolved, err := resolveSecret(ctx, adapter.resolver, adapter.secretRef)
	if err != nil {
		return failedExecution(frozen.AttemptID, "CHANNEL_SECRET_UNAVAILABLE")
	}
	secret := resolved
	defer clear(secret)
	headerValue, err := authorizationHeader(secret)
	if err != nil {
		return failedExecution(frozen.AttemptID, "CHANNEL_SECRET_UNAVAILABLE")
	}
	authorization := headerValue
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		adapter.outboundURL.String(),
		&singleUseReader{reader: bytes.NewReader(body)},
	)
	if err != nil {
		return failedExecution(frozen.AttemptID, "CHANNEL_REQUEST_CONSTRUCTION_FAILED")
	}
	httpRequest.ContentLength = int64(len(body))
	httpRequest.GetBody = nil
	httpRequest.Close = true
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Authorization", authorization)
	response, callErr := adapter.client.Do(httpRequest) // exactly one application call
	authorization = ""
	if callErr != nil {
		return unknownExecution(frozen.AttemptID, "CHANNEL_POST_AMBIGUOUS")
	}
	if response == nil {
		return unknownExecution(frozen.AttemptID, "CHANNEL_RESPONSE_MISSING")
	}
	if response.StatusCode >= 300 && response.StatusCode <= 399 {
		if response.Body != nil {
			response.Body.Close()
		}
		return unknownExecution(frozen.AttemptID, "CHANNEL_REDIRECT_FORBIDDEN")
	}
	if response.ContentLength > maximumResponseBytes {
		if response.Body != nil {
			response.Body.Close()
		}
		return unknownExecution(frozen.AttemptID, "CHANNEL_RESPONSE_OVERSIZE")
	}
	responseBody, err := readBoundedAndClose(response.Body, maximumResponseBytes)
	if err != nil {
		return unknownExecution(frozen.AttemptID, "CHANNEL_RESPONSE_AMBIGUOUS")
	}
	if len(response.Header.Values("Content-Type")) != 1 {
		return unknownExecution(frozen.AttemptID, "CHANNEL_RESPONSE_MALFORMED")
	}
	mediaType := response.Header.Get("Content-Type")
	if mediaType != "application/json" {
		return unknownExecution(frozen.AttemptID, "CHANNEL_RESPONSE_MALFORMED")
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		responseBody,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumResponseBytes,
			MaxDepth: 32,
			MaxNodes: maximumResponseBytes,
		},
	)
	if err != nil {
		return unknownExecution(frozen.AttemptID, "CHANNEL_RESPONSE_MALFORMED")
	}
	var wire deliveryResponseWireV1
	if err := decodeStrictCanonical(canonical, maximumResponseBytes, &wire); err != nil ||
		wire.SchemaVersion != deliveryResponseWireSchemaV1 || wire.AttemptID != frozen.AttemptID {
		return unknownExecution(frozen.AttemptID, "CHANNEL_RESPONSE_MALFORMED")
	}
	return adapterResultFromResponse(frozen.AttemptID, response.StatusCode, wire)
}

func adapterResultFromResponse(
	attemptID string,
	status int,
	wire deliveryResponseWireV1,
) (moduleapi.ChannelExecutionResultV1, error) {
	switch wire.Outcome {
	case string(moduleapi.ChannelExecutionSucceeded):
		if status < 200 || status > 299 || wire.ExternalOperationID == "" ||
			wire.ErrorClassification != "" {
			return unknownExecution(attemptID, "CHANNEL_RESPONSE_MALFORMED")
		}
		receipt, err := canonicalWire(providerReceiptWireV1{
			SchemaVersion:       providerReceiptWireSchemaV1,
			ExternalOperationID: wire.ExternalOperationID,
		}, moduleapi.MaxChannelProviderReceiptBytesV1)
		if err != nil {
			return unknownExecution(attemptID, "CHANNEL_RESPONSE_MALFORMED")
		}
		result, err := constructExecutionResult(moduleapi.ChannelExecutionResultV1{
			SchemaVersion:       moduleapi.ChannelExecutionResultSchemaV1,
			AttemptID:           attemptID,
			Outcome:             moduleapi.ChannelExecutionSucceeded,
			ProviderReceipt:     receipt,
			ExternalOperationID: wire.ExternalOperationID,
		})
		if err != nil {
			return unknownExecution(attemptID, "CHANNEL_RESPONSE_MALFORMED")
		}
		return result, nil
	case string(moduleapi.ChannelExecutionFailed):
		if status < 400 || status > 599 || wire.ExternalOperationID != "" ||
			wire.ErrorClassification == "" {
			return unknownExecution(attemptID, "CHANNEL_RESPONSE_MALFORMED")
		}
		result, err := failedExecution(attemptID, wire.ErrorClassification)
		if err != nil {
			return unknownExecution(attemptID, "CHANNEL_RESPONSE_MALFORMED")
		}
		return result, nil
	case string(moduleapi.ChannelExecutionUnknown):
		return unknownExecution(attemptID, "CHANNEL_PROVIDER_UNKNOWN")
	default:
		return unknownExecution(attemptID, "CHANNEL_RESPONSE_MALFORMED")
	}
}

func failedExecution(
	attemptID string,
	classification string,
) (moduleapi.ChannelExecutionResultV1, error) {
	return constructExecutionResult(moduleapi.ChannelExecutionResultV1{
		SchemaVersion:       moduleapi.ChannelExecutionResultSchemaV1,
		AttemptID:           attemptID,
		Outcome:             moduleapi.ChannelExecutionFailed,
		ErrorClassification: classification,
	})
}

func unknownExecution(
	attemptID string,
	reason string,
) (moduleapi.ChannelExecutionResultV1, error) {
	return constructExecutionResult(moduleapi.ChannelExecutionResultV1{
		SchemaVersion: moduleapi.ChannelExecutionResultSchemaV1,
		AttemptID:     attemptID,
		Outcome:       moduleapi.ChannelExecutionUnknown,
		UnknownReason: reason,
	})
}

func constructExecutionResult(
	result moduleapi.ChannelExecutionResultV1,
) (moduleapi.ChannelExecutionResultV1, error) {
	frozen, _, err := moduleapi.NewChannelExecutionResultV1(result)
	return frozen, err
}
