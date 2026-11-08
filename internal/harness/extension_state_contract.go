package harness

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
)

const extensionStateVersion = 2

type ExtensionStateOperation struct {
	Scope       string          `json:"scope"`
	Model       bool            `json:"model,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ExtensionStateOrigin struct {
	RunID   string `json:"run_id"`
	InputID string `json:"input_id"`
}

func validateStateDeclaration(manifest ExtensionManifest) error {
	if !manifest.State {
		if manifest.StateVersion != 0 || len(manifest.StateOperations) != 0 {
			return errors.New("state declarations require state capability")
		}
		return nil
	}
	if manifest.StateVersion != extensionStateVersion || len(manifest.StateOperations) == 0 || len(manifest.StateOperations) > 32 {
		return errors.New("unsupported state contract or empty operation declarations")
	}
	for action, operation := range manifest.StateOperations {
		if !validCapabilityID(action) || !slices.Contains([]string{"global", "space", "actor"}, operation.Scope) || len(operation.InputSchema) > 32<<10 {
			return errors.New("invalid state operation declaration")
		}
		var schema struct {
			Type       string `json:"type"`
			Additional *bool  `json:"additionalProperties"`
		}
		if json.Unmarshal(operation.InputSchema, &schema) != nil || schema.Type != "object" || schema.Additional == nil || *schema.Additional {
			return errors.New("state payload schema must be a closed object")
		}
		if _, err := resolveToolSchema(operation.InputSchema); err != nil {
			return err
		}
	}
	return nil
}

func (service *ControlService) validateExtensionChange(ctx context.Context, record *controlRecord, args *ExtensionStateChange, originRun string) error {
	tasks := NewTaskService(service.core)
	definition, err := service.core.extensions.definitionFor(args.ID, tasks.Config())
	if err != nil {
		return err
	}
	manifest := definition.Manifest
	if !manifest.State || manifest.StateVersion != extensionStateVersion || !tasks.Config().Extensions.Enabled[args.ID] {
		return ErrPermissionDenied
	}
	operation, ok := manifest.StateOperations[args.Action]
	if !ok || args.ExpectedRevision < 0 {
		return ErrPermissionDenied
	}
	hash := contentHash(mustJSON([]any{manifest.Version, manifest.StateVersion, manifest.StateOperations, manifest.StorageSchema, manifest.StorageVersion}))
	if args.Contract != 0 && args.Contract != extensionStateVersion || args.DeclarationHash != "" && args.DeclarationHash != hash {
		return ErrControlStaleRevision
	}
	args.Contract, args.DeclarationHash = extensionStateVersion, hash
	if len(args.Payload) == 0 {
		args.Payload = json.RawMessage(`{}`)
	}
	var payload map[string]any
	if json.Unmarshal(args.Payload, &payload) != nil || payload == nil {
		return ErrPermissionDenied
	}
	schema, err := resolveToolSchema(operation.InputSchema)
	if err != nil {
		return err
	}
	if err := schema.Validate(payload); err != nil {
		return err
	}
	scope, err := tasks.extensionScope(ctx, record.Operation.ConversationID, args.Scope)
	if err != nil {
		return err
	}
	args.Scope = scope
	if record.Operation.Authorization != ControlModelProposed {
		return nil
	}
	if !operation.Model {
		return ErrPermissionDenied
	}
	chat, err := tasks.core.Store.GetConversation(ctx, record.Operation.ConversationID)
	if err != nil {
		return err
	}
	if operation.Scope == "global" && chat.SpaceID != LocalSpaceID {
		return ErrPermissionDenied
	}
	if originRun != "" {
		run, err := tasks.core.Store.GetRun(ctx, originRun)
		if err != nil || run.ConversationID != chat.ID {
			return ErrPermissionDenied
		}
		var snapshot runSnapshot
		if json.Unmarshal([]byte(run.ConfigJSON), &snapshot) != nil || snapshot.Capabilities == nil || !slices.Contains(snapshot.Capabilities.Extensions, args.ID) {
			return ErrPermissionDenied
		}
		identity, err := service.core.extensionIdentity(ctx, run)
		if err != nil {
			return err
		}
		input := identity.Inputs[len(identity.Inputs)-1]
		if args.Origin != nil && (args.Origin.RunID != originRun || args.Origin.InputID != input.ID) {
			return ErrPermissionDenied
		}
		args.Origin = &ExtensionStateOrigin{RunID: originRun, InputID: input.ID}
		if chat.SpaceID != LocalSpaceID && args.Scope.ActorID != input.ActorID {
			return ErrPermissionDenied
		}
	} else if args.Origin != nil {
		// This is confirmation of the original frozen source, never a lookup
		// of whichever Run happens to be active in this conversation now.
		run, err := tasks.core.Store.GetRun(ctx, args.Origin.RunID)
		if err != nil || run.ConversationID != chat.ID {
			return ErrPermissionDenied
		}
		var snapshot runSnapshot
		if json.Unmarshal([]byte(run.ConfigJSON), &snapshot) != nil || snapshot.Capabilities == nil || !slices.Contains(snapshot.Capabilities.Extensions, args.ID) {
			return ErrPermissionDenied
		}
		identity, err := service.core.extensionIdentity(ctx, run)
		if err != nil {
			return err
		}
		found := false
		for _, input := range identity.Inputs {
			if input.ID == args.Origin.InputID && (chat.SpaceID == LocalSpaceID || input.ActorID == args.Scope.ActorID) {
				found = true
			}
		}
		if !found {
			return ErrPermissionDenied
		}
		bound := *service.core
		bound.permission = run.Permission
		if err := bound.checkCurrentPermission(ctx, run); err != nil {
			return err
		}
	} else if chat.SpaceID != LocalSpaceID {
		return ErrPermissionDenied
	}
	return nil
}
