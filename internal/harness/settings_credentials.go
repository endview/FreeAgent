package harness

import "context"

// CredentialSettingsSnapshot reads the safe metadata and its edit revision
// together. A form rendered in one browser cannot overwrite a later rotation.
func (service *ControlService) CredentialSettingsSnapshot(ctx context.Context, namespace string) ([]KeyProfileView, uint64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	keys, resource := service.core.Keys, "keys.profiles"
	switch namespace {
	case "channels":
		keys, resource = NewTaskService(service.core).ChannelKeys(), "channel.keys"
	case "":
	default:
		return nil, 0, ErrPermissionDenied
	}
	if keys == nil {
		return nil, 0, ErrKeyNotFound
	}
	profiles, err := keys.List()
	return profiles, service.revisionLocked(resource), err
}
