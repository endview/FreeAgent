package harness

import (
	"context"
	"fmt"
)

// Explicitly enabled passive character data does not grant tools to None.
// The manifest cannot declare tools/workflows or call model/tool Host APIs.
func (service *Service) preparePassiveContext(ctx context.Context, run Run, request ModelRequest) (ModelRequest, error) {
	if service.extensions == nil || service.capabilities == nil {
		return request, nil
	}
	if !service.extensionPrepared {
		service.extensionPrepared = true
		service.extensions.configure(service.Config)
		for _, id := range service.capabilities.Extensions {
			definition, err := service.extensions.definition(id)
			if err != nil || !definition.Manifest.PassiveContext {
				continue
			}
			blocks, err := service.extensions.prepare(ctx, id, run, "chat", "turn", nil, nil, service)
			if err != nil {
				if service.extensionRequired(id) {
					return ModelRequest{}, fmt.Errorf("required passive context %q failed: %w", id, err)
				}
				service.event(Event{Kind: EventExtensionError, RunID: run.ID, Message: id + ": " + safeError(err)})
				continue
			}
			service.extensionBlocks = append(service.extensionBlocks, blocks...)
		}
		limit := service.Config.Extensions.MaxContextChars
		if limit == 0 {
			limit = 16384
		}
		service.extensionBlocks = boundExtensionBlocks(service.extensionBlocks, limit)
		service.extensionUses = extensionBlockUses(service.extensionBlocks)
	}
	request.Messages = insertExtensionBlocks(request.Messages, service.extensionBlocks)
	request.Extensions = mergeExtensionUses(request.Extensions, service.extensionUses)
	return request, nil
}
