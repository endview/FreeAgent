package remoteactionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	maximumResolvedMaterialBytesV1 = 4096
	maximumHTTPResponseBytesV1     = 2 * moduleapi.MaxConfigBytes

	failedClosureRejected       = "REMOTE_ACTION_CLOSURE_REJECTED"
	failedProviderMismatch      = "REMOTE_ACTION_PROVIDER_MISMATCH"
	failedPreparedRejected      = "REMOTE_ACTION_PREPARED_PAYLOAD_REJECTED"
	failedEndpointDenied        = "REMOTE_ACTION_ENDPOINT_DENIED"
	failedResolutionUnavailable = "REMOTE_ACTION_SECRET_UNAVAILABLE"
	failedRequestRejected       = "REMOTE_ACTION_REQUEST_REJECTED"
	unknownTransportAmbiguous   = "REMOTE_ACTION_TRANSPORT_AMBIGUOUS"
	unknownResponseMissing      = "REMOTE_ACTION_RESPONSE_MISSING"
	unknownRedirectForbidden    = "REMOTE_ACTION_REDIRECT_FORBIDDEN"
	unknownResponseOversize     = "REMOTE_ACTION_RESPONSE_OVERSIZE"
	unknownResponseIncomplete   = "REMOTE_ACTION_RESPONSE_INCOMPLETE"
	unknownResponseMalformed    = "REMOTE_ACTION_RESPONSE_MALFORMED"
	unknownProviderUncertain    = "REMOTE_ACTION_PROVIDER_UNKNOWN"
	unknownMaterialEchoed       = "REMOTE_ACTION_RESPONSE_CONTAINS_SECRET"
)

// ExecutePrepared performs one native HTTPS POST only after validating the
// complete private Gateway closure. Every local denial returns a deterministic
// FAILED result without calling RoundTrip. Once client.Do is entered, an
// unclassified failure is UNKNOWN and is never retried.
func (adapter *Adapter) ExecutePrepared(
	ctx context.Context,
	execution modulehost.PreparedActionExecutionV1,
) (moduleapi.ActionExecutionResultV1, error) {
	if err := validateContext(adapter, ctx, ErrExecute); err != nil {
		return moduleapi.ActionExecutionResultV1{}, err
	}
	requestAttemptID := execution.Request.AttemptID
	if _, _, err := moduleapi.NewActionExecutionRequestV1(execution.Request); err != nil {
		return moduleapi.ActionExecutionResultV1{}, fmt.Errorf(
			"%w: request identity is invalid",
			ErrExecute,
		)
	}
	frozenExecution, err := modulehost.NewPreparedActionExecutionV1(execution)
	if err != nil {
		return failedExecution(requestAttemptID, failedClosureRejected)
	}
	request := frozenExecution.Request
	provider := frozenExecution.Binding.Provider
	if !adapter.provider.matches(provider) {
		return failedExecution(request.AttemptID, failedProviderMismatch)
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(
		frozenExecution.ConfigCanonical,
	)
	if err != nil {
		return failedExecution(request.AttemptID, failedClosureRejected)
	}
	parameters, err := moduleapi.RestoreRemoteActionHTTPBindingParametersV1(
		config.Parameters,
	)
	if err != nil {
		return failedExecution(request.AttemptID, failedClosureRejected)
	}
	if err := rejectNonPublicLiteralEndpoint(parameters.EndpointURL); err != nil {
		return failedExecution(request.AttemptID, failedEndpointDenied)
	}
	definition, found := adapter.definitionByProviderID[request.ProviderActionID]
	if !found || moduleapi.ValidateActionInputV1(
		definition.InputSchema,
		request.PreparedPayload,
	) != nil {
		return failedExecution(request.AttemptID, failedPreparedRejected)
	}
	bodyRequest, err := canonicalExecutionRequest(request)
	if err != nil {
		return failedExecution(request.AttemptID, failedRequestRejected)
	}
	resolved, err := adapter.resolver.ResolveSecret(ctx, SecretIdentityV1{
		Provider:    provider,
		EndpointURL: parameters.EndpointURL,
		SecretRef:   parameters.SecretRef,
	})
	if err != nil {
		return failedExecution(request.AttemptID, failedResolutionUnavailable)
	}
	material := bytes.Clone(resolved)
	clear(resolved)
	if !validResolvedSecret(material) {
		clear(material)
		return failedExecution(request.AttemptID, failedResolutionUnavailable)
	}
	defer clear(material)
	if err := ctx.Err(); err != nil {
		return failedExecution(request.AttemptID, failedRequestRejected)
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		parameters.EndpointURL,
		&singleUseReader{reader: bytes.NewReader(bodyRequest)},
	)
	if err != nil {
		return failedExecution(request.AttemptID, failedRequestRejected)
	}
	httpRequest.ContentLength = int64(len(bodyRequest))
	httpRequest.GetBody = nil
	httpRequest.Close = true
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+string(material))

	response, callErr := adapter.client.Do(httpRequest) // exactly one POST
	httpRequest.Header.Del("Authorization")
	if callErr != nil {
		closeHTTPResponse(response)
		classified := sanitizeTransportError(callErr)
		if errors.Is(classified, ErrEndpointNotPublic) ||
			errors.Is(classified, ErrEndpointResolution) {
			return failedExecution(request.AttemptID, failedEndpointDenied)
		}
		return unknownExecution(request.AttemptID, unknownTransportAmbiguous)
	}
	if response == nil {
		return unknownExecution(request.AttemptID, unknownResponseMissing)
	}
	if response.StatusCode >= 300 && response.StatusCode <= 399 {
		closeHTTPResponse(response)
		return unknownExecution(request.AttemptID, unknownRedirectForbidden)
	}
	if response.ContentLength > maximumHTTPResponseBytesV1 {
		closeHTTPResponse(response)
		return unknownExecution(request.AttemptID, unknownResponseOversize)
	}
	responseBody, err := readBoundedResponse(response.Body)
	if err != nil {
		return unknownExecution(request.AttemptID, unknownResponseIncomplete)
	}
	defer clear(responseBody)
	if bytes.Contains(responseBody, material) {
		return unknownExecution(request.AttemptID, unknownMaterialEchoed)
	}
	if !exactJSONContentType(response.Header) {
		return unknownExecution(request.AttemptID, unknownResponseMalformed)
	}
	result, resultOnlyRejected, err := restoreHTTPExecutionResult(
		request,
		responseBody,
	)
	if err != nil || !statusClosesOutcome(response.StatusCode, result.Outcome) {
		return unknownExecution(request.AttemptID, unknownResponseMalformed)
	}
	if result.Outcome == moduleapi.ActionExecutionUnknown {
		result.UnknownReason = unknownProviderUncertain
		frozen, _, freezeErr := moduleapi.NewActionExecutionResultV1(result)
		if freezeErr != nil {
			return unknownExecution(request.AttemptID, unknownResponseMalformed)
		}
		return frozen, nil
	}
	if resultOnlyRejected {
		// Gateway owns the existing SUCCEEDED + RESULT_REJECTED transition.
		// Returning the otherwise-complete proof avoids corrupting certainty.
		return result, nil
	}
	return result, nil
}

func canonicalExecutionRequest(
	request moduleapi.ActionExecutionRequestV1,
) ([]byte, error) {
	_, canonical, err := moduleapi.NewActionExecutionRequestV1(request)
	if err != nil || len(canonical) == 0 ||
		len(canonical) > 2*moduleapi.MaxConfigBytes {
		return nil, errors.New("remote Action HTTP request is invalid")
	}
	return bytes.Clone(canonical), nil
}

func validResolvedSecret(material []byte) bool {
	if len(material) == 0 || len(material) > maximumResolvedMaterialBytesV1 {
		return false
	}
	paddingStarted := false
	payloadBytes := 0
	for _, character := range material {
		if character == '=' {
			paddingStarted = true
			continue
		}
		if paddingStarted || !validBearerTokenCharacter(character) {
			return false
		}
		payloadBytes++
	}
	return payloadBytes > 0
}

func validBearerTokenCharacter(character byte) bool {
	return character >= 'A' && character <= 'Z' ||
		character >= 'a' && character <= 'z' ||
		character >= '0' && character <= '9' ||
		character == '-' || character == '.' || character == '_' ||
		character == '~' || character == '+' || character == '/'
}

func readBoundedResponse(body io.ReadCloser) ([]byte, error) {
	if body == nil {
		return nil, errors.New("response body is nil")
	}
	defer body.Close()
	value, err := io.ReadAll(io.LimitReader(
		body,
		maximumHTTPResponseBytesV1+1,
	))
	if err != nil || len(value) > maximumHTTPResponseBytesV1 {
		clear(value)
		return nil, errors.New("response body is incomplete or oversized")
	}
	return value, nil
}

func exactJSONContentType(header http.Header) bool {
	values := header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(values[0])
	return err == nil && mediaType == "application/json" &&
		len(parameters) == 0 && values[0] == "application/json"
}

func restoreHTTPExecutionResult(
	request moduleapi.ActionExecutionRequestV1,
	canonical []byte,
) (moduleapi.ActionExecutionResultV1, bool, error) {
	normalized, err := moduleapi.CanonicalJSONWithLimits(
		canonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumHTTPResponseBytesV1,
			MaxDepth: 64,
			MaxNodes: maximumHTTPResponseBytesV1,
		},
	)
	if err != nil || len(normalized) == 0 || normalized[0] != '{' ||
		!bytes.Equal(canonical, normalized) {
		return moduleapi.ActionExecutionResultV1{}, false,
			errors.New("response is not exact canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var decoded moduleapi.ActionExecutionResultV1
	if err := decoder.Decode(&decoded); err != nil {
		return moduleapi.ActionExecutionResultV1{}, false, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return moduleapi.ActionExecutionResultV1{}, false,
			errors.New("response has trailing JSON")
	}
	frozen, _, err := moduleapi.NewActionExecutionResultV1(decoded)
	if err == nil {
		err = moduleapi.ValidateActionExecutionResultForRequestV1(request, frozen)
	}
	if err == nil {
		return frozen, false, nil
	}
	if resultOnlyInvalidSuccess(request, decoded) {
		return decoded, true, nil
	}
	return moduleapi.ActionExecutionResultV1{}, false, err
}

func resultOnlyInvalidSuccess(
	request moduleapi.ActionExecutionRequestV1,
	result moduleapi.ActionExecutionResultV1,
) bool {
	if result.Outcome != moduleapi.ActionExecutionSucceeded ||
		result.AttemptID != request.AttemptID ||
		len(result.CanonicalResult) == 0 {
		return false
	}
	probe := result
	probe.CanonicalResult = json.RawMessage(`0`)
	frozen, _, err := moduleapi.NewActionExecutionResultV1(probe)
	return err == nil &&
		moduleapi.ValidateActionExecutionResultForRequestV1(request, frozen) == nil
}

func statusClosesOutcome(status int, outcome moduleapi.ActionExecutionOutcomeV1) bool {
	switch outcome {
	case moduleapi.ActionExecutionSucceeded:
		return status >= 200 && status <= 299
	case moduleapi.ActionExecutionFailed:
		return status >= 400 && status <= 599
	case moduleapi.ActionExecutionUnknown:
		return status >= 200 && status <= 299 || status >= 400 && status <= 599
	default:
		return false
	}
}

func failedExecution(
	attemptID string,
	classification string,
) (moduleapi.ActionExecutionResultV1, error) {
	result, _, err := moduleapi.NewActionExecutionResultV1(
		moduleapi.ActionExecutionResultV1{
			SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
			AttemptID:           attemptID,
			Outcome:             moduleapi.ActionExecutionFailed,
			ErrorClassification: classification,
		},
	)
	return result, err
}

func unknownExecution(
	attemptID string,
	reason string,
) (moduleapi.ActionExecutionResultV1, error) {
	result, _, err := moduleapi.NewActionExecutionResultV1(
		moduleapi.ActionExecutionResultV1{
			SchemaVersion: moduleapi.ActionExecutionResultSchemaV1,
			AttemptID:     attemptID,
			Outcome:       moduleapi.ActionExecutionUnknown,
			UnknownReason: reason,
		},
	)
	return result, err
}

func closeHTTPResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
}

type singleUseReader struct {
	reader io.Reader
}

func (reader *singleUseReader) Read(target []byte) (int, error) {
	return reader.reader.Read(target)
}
