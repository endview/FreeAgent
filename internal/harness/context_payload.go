package harness

import "encoding/json"

// Account for exactly the body the adapter sends, excluding Core provenance.
// No credentials or response data enter this pure encoder.
func providerRequestPayload(binding ModelBinding, request ModelRequest) ([]byte, error) {
	config := binding.Config
	caps := config.EffectiveCapabilities()
	protocol := caps.Protocol
	if config.Provider == "anthropic" {
		protocol = "anthropic"
	}
	switch protocol {
	case "anthropic":
		source := nativeContinuationSource("anthropic", binding.BaseURL, config.Key, config.Model)
		request.Messages = append([]ModelMessage(nil), request.Messages...)
		for i := range request.Messages {
			if saved := request.Messages[i].ChatContinuation; saved != nil && saved.Source != source {
				request.Messages[i].ChatContinuation = nil
			}
		}
		wire, err := anthropicRequestFrom(request, config.Model, caps)
		if err != nil {
			return nil, err
		}
		return json.Marshal(wire)
	case "responses":
		source := nativeContinuationSource("responses", binding.BaseURL, config.Key, config.Model)
		request.Messages = scopedProviderMessages(request.Messages, source)
		wire, err := responsesRequestForEndpoint(request, config.Model, caps, caps.WebSearch == "supported", binding.BaseURL)
		if err != nil {
			return nil, err
		}
		return json.Marshal(wire)
	default:
		wire, err := openAIRequestFrom(request, config.Model, caps, binding.BaseURL, config.Key)
		if err != nil {
			return nil, err
		}
		return json.Marshal(wire)
	}
}
