package harness

import (
	"context"
	"encoding/json"
)

// A bounded, disposable cache of matched Attempt usage. A restart simply
// returns to conservative estimates; the durable evidence remains the Attempt.
type contextCalibration struct {
	Fingerprint string
	Hashes      []string
	Input       int
	AttemptID   string
}

func (service *Service) calibrationIdentity(run Run, request ModelRequest) (string, []string) {
	binding, err := service.binding()
	if err != nil {
		return "", nil
	}
	fingerprint := contentHash(mustJSON([]any{contextProjectionVersion, service.contextScope(), binding, request.Reasoning, request.MaxOutputTokens, request.Tools}))
	hashes := make([]string, 0, len(request.Messages))
	for _, m := range request.Messages {
		hashes = append(hashes, contentHash(mustJSON([]any{semanticMessage(m), m.Images})))
	}
	return run.ConversationID + "/" + fingerprint, hashes
}

func (service *Service) calibrateContextUsage(run Run, request ModelRequest, usage ContextUsage) ContextUsage {
	if request.Purpose != "" || service.Store == nil {
		return usage
	}
	key, hashes := service.calibrationIdentity(run, request)
	service.Store.mu.Lock()
	saved, ok := service.Store.contextCalibrations[key]
	service.Store.mu.Unlock()
	if !ok || len(saved.Hashes) > len(hashes) {
		return usage
	}
	for i, hash := range saved.Hashes {
		if hashes[i] != hash {
			return usage
		}
	}
	added := 0
	for _, m := range request.Messages[len(saved.Hashes):] {
		added += estimateContextTokens(string(mustJSON(semanticMessage(m)))) + 8
		for _, image := range m.Images {
			added += max(1024, ((image.Width+511)/512)*((image.Height+511)/512)*256)
		}
	}
	usage.EstimatedTokens = saved.Input + added + max(128, saved.Input/10)
	usage.EstimateMethod = "matched_attempt_prefix_plus_new_input"
	usage.CalibrationAttempt = saved.AttemptID
	usage.updateOccupancy()
	return usage
}

func (service *Service) observeContextUsage(ctx context.Context, run Run, attempt Attempt, request ModelRequest, response ModelResponse) error {
	if request.Purpose != "" || response.Usage == nil || response.Usage.InputTokens <= 0 || response.Usage.InputTokens > 1<<30 {
		return nil
	}
	key, hashes := service.calibrationIdentity(run, request)
	if key == "" || len(hashes) > 65536 {
		return nil
	}
	service.Store.mu.Lock()
	if len(service.Store.contextCalibrations) >= 64 {
		service.Store.contextCalibrations = nil
	}
	if service.Store.contextCalibrations == nil {
		service.Store.contextCalibrations = map[string]contextCalibration{}
	}
	service.Store.contextCalibrations[key] = contextCalibration{key, hashes, int(response.Usage.InputTokens), attempt.ID}
	service.Store.mu.Unlock()
	if request.ContextUsage != nil {
		usage := *request.ContextUsage
		usage.AttemptID = attempt.ID
		usage.Reported = response.Usage
		return service.recordContextUsage(ctx, run, usage)
	}
	return nil
}

// Captured range metadata is bounded and contains no duplicated source text.
func contextRequestSources(request ModelRequest) []ContextSource {
	var sources []ContextSource
	for _, m := range request.Messages {
		if m.ContextSource != nil {
			source := *m.ContextSource
			source.Through, source.Count = source.Ref, 1
			if len(sources) > 0 {
				last := &sources[len(sources)-1]
				if last.Part == source.Part && last.RunID == source.RunID && last.Folded == source.Folded {
					last.Through, last.Count, last.Bytes = source.Ref, last.Count+1, last.Bytes+source.Bytes
					last.Revision = contentHash(mustJSON([]string{last.Revision, source.Revision}))
					continue
				}
			}
			sources = append(sources, source)
		}
	}
	return sources
}

func checkCacheUsage(usage *ModelUsage) error {
	if usage == nil {
		return nil
	}
	for _, value := range []*int64{usage.CacheReadTokens, usage.CacheCreatedTokens} {
		if value != nil && (*value < 0 || *value > usage.InputTokens) {
			return &CallError{Certain: true, Category: "protocol", Message: "invalid provider cache usage"}
		}
	}
	if usage.CacheReadTokens != nil && usage.CacheCreatedTokens != nil && *usage.CacheReadTokens+*usage.CacheCreatedTokens > usage.InputTokens {
		return &CallError{Certain: true, Category: "protocol", Message: "invalid combined cache usage"}
	}
	return nil
}

// Used by tests to compare metadata-free wire payloads with recorded Attempts.
func contextUsageFromAttempt(attempt Attempt) *ContextUsage {
	var request ModelRequest
	if json.Unmarshal([]byte(attempt.RequestJSON), &request) != nil {
		return nil
	}
	return request.ContextUsage
}
