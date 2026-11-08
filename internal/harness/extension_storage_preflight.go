package harness

import (
	"context"
	"encoding/json"
	"github.com/endview/freeagent/internal/postgres"
)

// PreflightExtensionStorage is an explicit local maintenance operation. Callers
// hold the Owner lease; ordinary extension startup never repairs bindings.
func (tasks *TaskService) PreflightExtensionStorage(ctx context.Context, id string) error {
	definition, err := tasks.core.extensions.definitionFor(id, tasks.Config())
	if err != nil {
		return err
	}
	if definition.Manifest.Storage != "postgresql" {
		return ErrPermissionDenied
	}
	control, err := tasks.Control()
	if err != nil {
		return err
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	for _, record := range control.operations {
		if record.Operation.Kind != ControlChangeExtensionState || !record.ExtensionDispatched || record.Operation.Status == ControlApplied || record.Operation.Status == ControlFailed {
			continue
		}
		var change struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(record.Arguments, &change) != nil || change.ID == id {
			return ErrUnknownResult
		}
	}
	return postgres.BindExistingExtension(ctx, tasks.DataDir(), extensionStoragePath(tasks.DataDir(), id), id, definition.Manifest.StorageSchema, definition.Manifest.StorageVersion)
}
