package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/modulehandler"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleApplyPlanSchemaV1       = "module-apply-plan/v1"
	moduleApplyPlanDigestDomainV1 = "freeagent.module-apply-plan/v1"
	maximumModuleApplyPlanBytes   = 1 << 20
	maximumModuleApplyPlanDepth   = 128
)

type moduleApplyDesiredStateV1 string

const (
	moduleApplyEnabledV1  moduleApplyDesiredStateV1 = "ENABLED"
	moduleApplyDisabledV1 moduleApplyDesiredStateV1 = "DISABLED"
)

// moduleApplyPlanV1 is an inert, caller-owned description of one future
// Operator apply. Parsing this value performs no Store, artifact, process,
// network, model, or module operation.
type moduleApplyPlanV1 struct {
	SchemaVersion            string                        `json:"schema_version"`
	DesiredState             moduleApplyDesiredStateV1     `json:"desired_state"`
	TenantID                 string                        `json:"tenant_id"`
	ExpectedPointerRevision  uint64                        `json:"expected_pointer_revision"`
	BindingTarget            moduleApplyBindingTargetV1    `json:"binding_target"`
	InstanceID               string                        `json:"instance_id"`
	ReplaceCurrentInstanceID string                        `json:"replace_current_instance_id,omitempty"`
	Port                     moduleapi.PortRef             `json:"port"`
	Module                   *moduleApplyModuleV1          `json:"module,omitempty"`
	Binding                  *moduleApplyBindingV1         `json:"binding,omitempty"`
	ModelProfile             json.RawMessage               `json:"model_profile,omitempty"`
	ChannelEndpoint          *moduleApplyChannelEndpointV1 `json:"channel_endpoint,omitempty"`
	CursorSeed               json.RawMessage               `json:"cursor_seed,omitempty"`
}

type moduleApplyBindingTargetKindV1 string

const (
	moduleApplyBindingTargetProfileV1                  moduleApplyBindingTargetKindV1 = "PROFILE"
	moduleApplyBindingTargetWorkspaceChannelEndpointV1 moduleApplyBindingTargetKindV1 = "WORKSPACE_CHANNEL_ENDPOINT"
)

// moduleApplyBindingTargetV1 makes the consumer scope explicit. Profile-owned
// ports and Workspace Endpoint-owned Channel ports cannot be inferred from a
// module name, Port, or default configuration.
type moduleApplyBindingTargetV1 struct {
	Kind        moduleApplyBindingTargetKindV1 `json:"kind"`
	ProfileID   string                         `json:"profile_id,omitempty"`
	WorkspaceID string                         `json:"workspace_id,omitempty"`
	EndpointID  string                         `json:"endpoint_id,omitempty"`
}

// moduleApplyChannelEndpointV1 is Operator-owned Workspace routing state. It
// is deliberately separate from provider Config: a module may request one
// channel.transport/v1 Port, but it cannot choose Core Agent/Profile routing
// or Cursor scope.
type moduleApplyChannelEndpointV1 struct {
	Channel         string `json:"channel"`
	AccountID       string `json:"account_id"`
	ConversationID  string `json:"conversation_id"`
	TargetAgentID   string `json:"target_agent_id"`
	TargetProfileID string `json:"target_profile_id"`
	CursorScopeKey  string `json:"cursor_scope_key"`
}

type moduleApplyModuleV1 struct {
	ID                     string                              `json:"id"`
	ExactVersion           string                              `json:"exact_version"`
	ArtifactDigest         string                              `json:"artifact_digest"`
	ArtifactSizeBytes      uint64                              `json:"artifact_size_bytes"`
	ExpectedRuntimeRequest moduleApplyExpectedRuntimeRequestV1 `json:"expected_runtime_request"`
}

// moduleApplyExpectedRuntimeRequestV1 is an Operator-owned expectation used
// by Core when it later chooses one exact protocol handler. It grants no
// execution class, adapter, trust, authority, or handler identity.
type moduleApplyExpectedRuntimeRequestV1 struct {
	Mode     moduleapi.RuntimeModeRequest `json:"mode"`
	Protocol string                       `json:"protocol"`
}

type moduleApplyBindingV1 struct {
	PortBindingIndex uint32                  `json:"port_binding_index"`
	Config           json.RawMessage         `json:"config"`
	AuthorityCeiling json.RawMessage         `json:"authority_ceiling"`
	FailurePolicy    moduleapi.FailurePolicy `json:"failure_policy"`
}

// The wire-only pointer preserves the distinction between an explicit zero
// port_binding_index and an omitted required field.
type moduleApplyPlanWireV1 struct {
	SchemaVersion            string                          `json:"schema_version"`
	DesiredState             moduleApplyDesiredStateV1       `json:"desired_state"`
	TenantID                 string                          `json:"tenant_id"`
	ExpectedPointerRevision  uint64                          `json:"expected_pointer_revision"`
	BindingTarget            *moduleApplyBindingTargetWireV1 `json:"binding_target"`
	InstanceID               string                          `json:"instance_id"`
	ReplaceCurrentInstanceID *string                         `json:"replace_current_instance_id,omitempty"`
	Port                     moduleapi.PortRef               `json:"port"`
	Module                   *moduleApplyModuleV1            `json:"module,omitempty"`
	Binding                  *moduleApplyBindingWireV1       `json:"binding,omitempty"`
	ModelProfile             json.RawMessage                 `json:"model_profile,omitempty"`
	ChannelEndpoint          *moduleApplyChannelEndpointV1   `json:"channel_endpoint,omitempty"`
	CursorSeed               json.RawMessage                 `json:"cursor_seed,omitempty"`
}

// Pointer fields preserve omitted versus explicitly empty target members.
type moduleApplyBindingTargetWireV1 struct {
	Kind        moduleApplyBindingTargetKindV1 `json:"kind"`
	ProfileID   *string                        `json:"profile_id,omitempty"`
	WorkspaceID *string                        `json:"workspace_id,omitempty"`
	EndpointID  *string                        `json:"endpoint_id,omitempty"`
}

type moduleApplyBindingWireV1 struct {
	PortBindingIndex *uint32                 `json:"port_binding_index"`
	Config           json.RawMessage         `json:"config"`
	AuthorityCeiling json.RawMessage         `json:"authority_ceiling"`
	FailurePolicy    moduleapi.FailurePolicy `json:"failure_policy"`
}

// readModuleApplyPlanV1 reads one small, ordinary, non-symlink file and then
// restores its exact canonical plan. The three returned values do not alias
// one another or the read buffer.
func readModuleApplyPlanV1(
	path string,
) (moduleApplyPlanV1, []byte, string, error) {
	if strings.TrimSpace(path) == "" {
		return moduleApplyPlanV1{}, nil, "", errors.New(
			"module apply plan path is required",
		)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
			"resolve module apply plan: %w",
			err,
		)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
			"resolve module apply plan: %w",
			err,
		)
	}
	if !sameModuleApplyPlanPath(absolute, resolved) {
		return moduleApplyPlanV1{}, nil, "", errors.New(
			"module apply plan path must not traverse symbolic links",
		)
	}

	before, err := os.Lstat(resolved)
	if err != nil {
		return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
			"inspect module apply plan: %w",
			err,
		)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return moduleApplyPlanV1{}, nil, "", errors.New(
			"module apply plan must be an ordinary non-symlink file",
		)
	}
	if before.Size() <= 0 || before.Size() > maximumModuleApplyPlanBytes {
		return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
			"module apply plan must contain 1-%d bytes",
			maximumModuleApplyPlanBytes,
		)
	}

	file, err := os.Open(resolved)
	if err != nil {
		return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
			"open module apply plan: %w",
			err,
		)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() ||
		!os.SameFile(before, opened) || opened.Size() != before.Size() {
		_ = file.Close()
		return moduleApplyPlanV1{}, nil, "", errors.New(
			"module apply plan changed or is not an ordinary file",
		)
	}
	payload, readErr := io.ReadAll(io.LimitReader(
		file,
		maximumModuleApplyPlanBytes+1,
	))
	after, afterErr := file.Stat()
	closeErr := file.Close()
	if err := errors.Join(readErr, afterErr, closeErr); err != nil {
		return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
			"read module apply plan: %w",
			err,
		)
	}
	if !os.SameFile(opened, after) || opened.Size() != after.Size() ||
		opened.ModTime() != after.ModTime() || len(payload) == 0 ||
		len(payload) > maximumModuleApplyPlanBytes ||
		int64(len(payload)) != opened.Size() {
		return moduleApplyPlanV1{}, nil, "", errors.New(
			"module apply plan changed or exceeds its size limit",
		)
	}
	return restoreModuleApplyPlanV1(payload)
}

// restoreModuleApplyPlanV1 accepts only the exact RFC 8785 representation.
// It returns detached canonical bytes and a domain-separated digest.
func restoreModuleApplyPlanV1(
	payload []byte,
) (moduleApplyPlanV1, []byte, string, error) {
	if len(payload) == 0 || len(payload) > maximumModuleApplyPlanBytes {
		return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
			"module apply plan must contain 1-%d bytes",
			maximumModuleApplyPlanBytes,
		)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		bytes.Clone(payload),
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumModuleApplyPlanBytes,
			MaxDepth: maximumModuleApplyPlanDepth,
			MaxNodes: maximumModuleApplyPlanBytes,
		},
	)
	if err != nil {
		return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
			"canonicalize module apply plan: %w",
			err,
		)
	}
	if !bytes.Equal(payload, canonical) {
		return moduleApplyPlanV1{}, nil, "", errors.New(
			"module apply plan must be exact RFC 8785 canonical JSON",
		)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil || fields == nil {
		return moduleApplyPlanV1{}, nil, "", errors.New(
			"module apply plan must be a JSON object",
		)
	}
	var wire moduleApplyPlanWireV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
			"decode module apply plan: %w",
			err,
		)
	}
	if jsonValue, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
				"module apply plan has trailing token %v",
				jsonValue,
			)
		}
		return moduleApplyPlanV1{}, nil, "", fmt.Errorf(
			"decode module apply plan trailer: %w",
			err,
		)
	}

	plan, err := validateModuleApplyPlanWireV1(wire, fields)
	if err != nil {
		return moduleApplyPlanV1{}, nil, "", err
	}
	detachedCanonical := bytes.Clone(canonical)
	return cloneModuleApplyPlanV1(plan), detachedCanonical,
		moduleapi.Digest(moduleApplyPlanDigestDomainV1, detachedCanonical), nil
}

// freezeModuleApplyPlanValueV1 is the sole in-memory constructor used by
// trusted application facades. It still round-trips through the exact frozen
// wire validator; callers cannot bypass omitted-field or semantic checks.
func freezeModuleApplyPlanValueV1(
	plan moduleApplyPlanV1,
) (moduleApplyPlanV1, []byte, string, error) {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return moduleApplyPlanV1{}, nil, "", err
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: maximumModuleApplyPlanBytes,
			MaxDepth: maximumModuleApplyPlanDepth,
			MaxNodes: maximumModuleApplyPlanBytes,
		},
	)
	if err != nil {
		return moduleApplyPlanV1{}, nil, "", err
	}
	return restoreModuleApplyPlanV1(canonical)
}

func validateModuleApplyPlanWireV1(
	wire moduleApplyPlanWireV1,
	fields map[string]json.RawMessage,
) (moduleApplyPlanV1, error) {
	if wire.SchemaVersion != moduleApplyPlanSchemaV1 {
		return moduleApplyPlanV1{}, fmt.Errorf(
			"module apply plan schema_version must be %q",
			moduleApplyPlanSchemaV1,
		)
	}
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "tenant_id", value: wire.TenantID},
		{label: "instance_id", value: wire.InstanceID},
	} {
		if err := validateModuleApplyOpaqueIDV1(field.label, field.value); err != nil {
			return moduleApplyPlanV1{}, err
		}
	}
	if wire.ExpectedPointerRevision == 0 ||
		wire.ExpectedPointerRevision >= math.MaxInt64 {
		return moduleApplyPlanV1{}, errors.New(
			"module apply plan expected_pointer_revision must be between 1 and MaxInt64-1",
		)
	}
	if wire.BindingTarget == nil {
		return moduleApplyPlanV1{}, errors.New(
			"module apply plan requires one explicit binding_target",
		)
	}
	bindingTarget, err := validateModuleApplyBindingTargetWireV1(
		*wire.BindingTarget,
	)
	if err != nil {
		return moduleApplyPlanV1{}, err
	}
	if _, present := fields["port"]; !present {
		return moduleApplyPlanV1{}, errors.New(
			"module apply plan requires an exact port",
		)
	}
	if err := validateModuleApplyPortV1(wire.Port); err != nil {
		return moduleApplyPlanV1{}, fmt.Errorf(
			"module apply plan port: %w",
			err,
		)
	}
	channelPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameChannelTransport,
		ExactVersion: moduleapi.PortVersionV1,
	}
	switch bindingTarget.Kind {
	case moduleApplyBindingTargetProfileV1:
		if wire.Port == channelPort {
			return moduleApplyPlanV1{}, errors.New(
				"PROFILE binding_target cannot bind channel.transport/v1",
			)
		}
	case moduleApplyBindingTargetWorkspaceChannelEndpointV1:
		if wire.Port != channelPort {
			return moduleApplyPlanV1{}, errors.New(
				"WORKSPACE_CHANNEL_ENDPOINT binding_target requires channel.transport/v1",
			)
		}
	default:
		return moduleApplyPlanV1{}, errors.New("unsupported binding_target kind")
	}
	if wire.DesiredState == moduleApplyDisabledV1 && wire.Port == productionModelPort {
		return moduleApplyPlanV1{}, errors.New(
			"model.generate/v2 cannot be disabled; use ENABLED replacement",
		)
	}

	plan := moduleApplyPlanV1{
		SchemaVersion:           wire.SchemaVersion,
		DesiredState:            wire.DesiredState,
		TenantID:                wire.TenantID,
		ExpectedPointerRevision: wire.ExpectedPointerRevision,
		BindingTarget:           bindingTarget,
		InstanceID:              wire.InstanceID,
		Port:                    wire.Port,
	}
	_, hasReplacement := fields["replace_current_instance_id"]
	if hasReplacement != (wire.ReplaceCurrentInstanceID != nil) {
		return moduleApplyPlanV1{}, errors.New(
			"module apply plan replacement field has an invalid wire shape",
		)
	}
	if wire.ReplaceCurrentInstanceID != nil {
		if err := validateModuleApplyOpaqueIDV1(
			"replace_current_instance_id",
			*wire.ReplaceCurrentInstanceID,
		); err != nil {
			return moduleApplyPlanV1{}, err
		}
		if *wire.ReplaceCurrentInstanceID == wire.InstanceID {
			return moduleApplyPlanV1{}, errors.New(
				"replacement current and target instance IDs must differ",
			)
		}
		plan.ReplaceCurrentInstanceID = *wire.ReplaceCurrentInstanceID
	}
	_, hasModule := fields["module"]
	_, hasBinding := fields["binding"]
	_, hasChannelEndpoint := fields["channel_endpoint"]
	_, hasCursorSeed := fields["cursor_seed"]
	_, hasModelProfile := fields["model_profile"]
	switch wire.DesiredState {
	case moduleApplyDisabledV1:
		if hasReplacement || wire.ReplaceCurrentInstanceID != nil {
			return moduleApplyPlanV1{}, errors.New(
				"DISABLED module apply plan forbids replacement intent",
			)
		}
		if hasModule || hasBinding || hasModelProfile || hasChannelEndpoint || hasCursorSeed ||
			wire.Module != nil || wire.Binding != nil || wire.ChannelEndpoint != nil ||
			wire.ModelProfile != nil || wire.CursorSeed != nil {
			return moduleApplyPlanV1{}, errors.New(
				"DISABLED module apply plan must omit module, binding, model_profile, channel_endpoint, and cursor_seed",
			)
		}
	case moduleApplyEnabledV1:
		if !hasModule || !hasBinding || wire.Module == nil || wire.Binding == nil {
			return moduleApplyPlanV1{}, errors.New(
				"ENABLED module apply plan requires module and binding objects",
			)
		}
		if wire.Binding.PortBindingIndex == nil {
			return moduleApplyPlanV1{}, errors.New(
				"ENABLED module apply plan requires port_binding_index",
			)
		}
		if err := validateModuleApplyModuleV1(*wire.Module); err != nil {
			return moduleApplyPlanV1{}, err
		}
		if wire.Module.ExpectedRuntimeRequest.Mode == moduleapi.RuntimeModeRequestWASM &&
			wire.Port != productionActionPort {
			return moduleApplyPlanV1{}, errors.New(
				"WASM runtime is supported only for exact action.provider/v1",
			)
		}
		if err := validateModuleApplyBindingPolicyV1(
			wire.Port,
			wire.TenantID,
			wire.Binding.Config,
			wire.Binding.AuthorityCeiling,
			wire.Binding.FailurePolicy,
		); err != nil {
			return moduleApplyPlanV1{}, err
		}
		moduleCopy := *wire.Module
		bindingCopy := moduleApplyBindingV1{
			PortBindingIndex: *wire.Binding.PortBindingIndex,
			Config:           bytes.Clone(wire.Binding.Config),
			AuthorityCeiling: bytes.Clone(wire.Binding.AuthorityCeiling),
			FailurePolicy:    wire.Binding.FailurePolicy,
		}
		if wire.Module.ExpectedRuntimeRequest.Mode == moduleapi.RuntimeModeRequestWASM {
			if err := validateModuleApplyWASMActionBindingV1(
				wire.TenantID,
				bindingCopy,
			); err != nil {
				return moduleApplyPlanV1{}, err
			}
		}
		plan.Module = &moduleCopy
		plan.Binding = &bindingCopy
		if plan.ReplaceCurrentInstanceID != "" &&
			(bindingTarget.Kind != moduleApplyBindingTargetProfileV1 ||
				wire.Port != productionContextPort ||
				wire.Module.ExpectedRuntimeRequest.Mode != moduleapi.RuntimeModeRequestDeclarative ||
				wire.Module.ExpectedRuntimeRequest.Protocol != moduleapi.RuntimeProtocolStaticV1) {
			return moduleApplyPlanV1{}, errors.New(
				"replacement is supported only for exact PROFILE DECLARATIVE context.provide/v1",
			)
		}
		if plan.ReplaceCurrentInstanceID != "" {
			contextConfig, contextErr := moduleapi.RestoreContextBindingConfigV1(
				bindingCopy.Config,
			)
			if contextErr != nil ||
				contextConfig.Placement != moduleapi.ContextPlacementTrustedInstruction ||
				!bytes.Equal(
					bindingCopy.AuthorityCeiling,
					modulehandler.DenyAllAuthorityCanonicalV1(),
				) {
				return moduleApplyPlanV1{}, errors.Join(
					contextErr,
					errors.New(
						"replacement requires TRUSTED_INSTRUCTION Context and deny-all authority",
					),
				)
			}
		}
		if wire.Port == productionModelPort && bindingCopy.PortBindingIndex != 0 {
			return moduleApplyPlanV1{}, errors.New(
				"model.generate/v2 replacement requires port_binding_index zero",
			)
		}
		if wire.Port == productionModelPort {
			if hasModelProfile {
				modelProfile, err := validateModuleApplyPlanModelProfileV1(
					wire.ModelProfile,
				)
				if err != nil {
					return moduleApplyPlanV1{}, err
				}
				plan.ModelProfile = modelProfile
			}
		} else if hasModelProfile || wire.ModelProfile != nil {
			return moduleApplyPlanV1{}, errors.New(
				"model_profile is allowed only for model.generate/v2",
			)
		}
		switch bindingTarget.Kind {
		case moduleApplyBindingTargetProfileV1:
			if hasChannelEndpoint || hasCursorSeed || wire.ChannelEndpoint != nil ||
				wire.CursorSeed != nil {
				return moduleApplyPlanV1{}, errors.New(
					"PROFILE module apply plan must omit channel_endpoint and cursor_seed",
				)
			}
		case moduleApplyBindingTargetWorkspaceChannelEndpointV1:
			if !hasChannelEndpoint || !hasCursorSeed || wire.ChannelEndpoint == nil ||
				wire.CursorSeed == nil {
				return moduleApplyPlanV1{}, errors.New(
					"enabled WORKSPACE_CHANNEL_ENDPOINT plan requires channel_endpoint and cursor_seed",
				)
			}
			if bindingCopy.PortBindingIndex != 0 ||
				bindingCopy.FailurePolicy != moduleapi.FailureRequired {
				return moduleApplyPlanV1{}, errors.New(
					"channel endpoint Binding must be REQUIRED at index zero",
				)
			}
			if err := validateModuleApplyChannelEndpointV1(*wire.ChannelEndpoint); err != nil {
				return moduleApplyPlanV1{}, err
			}
			cursorSeed, err := validateModuleApplyCursorSeedV1(wire.CursorSeed)
			if err != nil {
				return moduleApplyPlanV1{}, err
			}
			endpointCopy := *wire.ChannelEndpoint
			plan.ChannelEndpoint = &endpointCopy
			plan.CursorSeed = cursorSeed
		default:
			return moduleApplyPlanV1{}, errors.New("unsupported binding_target kind")
		}
	default:
		return moduleApplyPlanV1{}, errors.New(
			"module apply plan desired_state must be ENABLED or DISABLED",
		)
	}
	return plan, nil
}

func validateModuleApplyBindingTargetWireV1(
	wire moduleApplyBindingTargetWireV1,
) (moduleApplyBindingTargetV1, error) {
	target := moduleApplyBindingTargetV1{Kind: wire.Kind}
	switch wire.Kind {
	case moduleApplyBindingTargetProfileV1:
		if wire.ProfileID == nil || wire.WorkspaceID != nil || wire.EndpointID != nil {
			return moduleApplyBindingTargetV1{}, errors.New(
				"PROFILE binding_target requires only profile_id",
			)
		}
		if err := validateModuleApplyOpaqueIDV1("binding_target.profile_id", *wire.ProfileID); err != nil {
			return moduleApplyBindingTargetV1{}, err
		}
		target.ProfileID = *wire.ProfileID
	case moduleApplyBindingTargetWorkspaceChannelEndpointV1:
		if wire.ProfileID != nil || wire.WorkspaceID == nil || wire.EndpointID == nil {
			return moduleApplyBindingTargetV1{}, errors.New(
				"WORKSPACE_CHANNEL_ENDPOINT binding_target requires only workspace_id and endpoint_id",
			)
		}
		if err := validateModuleApplyOpaqueIDV1("binding_target.workspace_id", *wire.WorkspaceID); err != nil {
			return moduleApplyBindingTargetV1{}, err
		}
		if err := validateModuleApplyOpaqueIDV1("binding_target.endpoint_id", *wire.EndpointID); err != nil {
			return moduleApplyBindingTargetV1{}, err
		}
		target.WorkspaceID = *wire.WorkspaceID
		target.EndpointID = *wire.EndpointID
	default:
		return moduleApplyBindingTargetV1{}, errors.New(
			"module apply plan binding_target.kind must be PROFILE or WORKSPACE_CHANNEL_ENDPOINT",
		)
	}
	return target, nil
}

func validateModuleApplyChannelEndpointV1(endpoint moduleApplyChannelEndpointV1) error {
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "channel_endpoint.channel", value: endpoint.Channel},
		{label: "channel_endpoint.account_id", value: endpoint.AccountID},
		{label: "channel_endpoint.conversation_id", value: endpoint.ConversationID},
		{label: "channel_endpoint.target_agent_id", value: endpoint.TargetAgentID},
		{label: "channel_endpoint.target_profile_id", value: endpoint.TargetProfileID},
		{label: "channel_endpoint.cursor_scope_key", value: endpoint.CursorScopeKey},
	} {
		if err := validateModuleApplyOpaqueIDV1(field.label, field.value); err != nil {
			return err
		}
	}
	return nil
}

func validateModuleApplyCursorSeedV1(seed json.RawMessage) (json.RawMessage, error) {
	if len(seed) == 0 || len(seed) > moduleapi.MaxChannelCursorBytesV1 {
		return nil, fmt.Errorf(
			"module apply plan cursor_seed must contain 1-%d bytes",
			moduleapi.MaxChannelCursorBytesV1,
		)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(seed, moduleapi.CanonicalJSONLimits{
		MaxBytes: moduleapi.MaxChannelCursorBytesV1,
		MaxDepth: 64,
		MaxNodes: moduleapi.MaxChannelCursorBytesV1,
	})
	if err != nil || !bytes.Equal(seed, canonical) {
		return nil, errors.Join(
			err,
			errors.New("module apply plan cursor_seed must be bounded canonical JSON"),
		)
	}
	return bytes.Clone(canonical), nil
}

func validateModuleApplyModuleV1(module moduleApplyModuleV1) error {
	if err := (moduleapi.Ref{
		ID:      module.ID,
		Version: module.ExactVersion,
	}).Validate(); err != nil {
		return fmt.Errorf("module apply plan exact module reference: %w", err)
	}
	if !moduleapi.ValidSHA256(module.ArtifactDigest) {
		return errors.New(
			"module apply plan artifact_digest must be a lowercase SHA-256 digest",
		)
	}
	if module.ArtifactSizeBytes == 0 {
		return errors.New(
			"module apply plan artifact_size_bytes must be greater than zero",
		)
	}
	if err := validateModuleApplyExpectedRuntimeRequestV1(
		module.ExpectedRuntimeRequest,
	); err != nil {
		return err
	}
	return nil
}

func validateModuleApplyPlanModelProfileV1(
	canonical json.RawMessage,
) (json.RawMessage, error) {
	if len(canonical) == 0 || len(canonical) > moduleapi.MaxConfigBytes {
		return nil, errors.New("module apply plan model_profile is empty or too large")
	}
	checked, err := moduleapi.CanonicalJSON(canonical)
	if err != nil || !bytes.Equal(checked, canonical) {
		return nil, errors.Join(
			err,
			errors.New("module apply plan model_profile must be canonical JSON"),
		)
	}
	var profile corecontract.ModelProfileV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&profile); err != nil {
		return nil, fmt.Errorf("module apply plan model_profile: %w", err)
	}
	_, _, rebuilt, err := corecontract.NewModelProfileV1(profile)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		return nil, errors.Join(
			err,
			errors.New("module apply plan model_profile is not exact model-profile/v1"),
		)
	}
	return bytes.Clone(canonical), nil
}

func validateModuleApplyExpectedRuntimeRequestV1(
	request moduleApplyExpectedRuntimeRequestV1,
) error {
	return modulehandler.ValidateRuntimeRequestV1(modulehandler.RuntimeRequestV1{
		Mode: request.Mode, Protocol: request.Protocol,
	})
}

func validateModuleApplyOpaqueIDV1(label string, value string) error {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > moduleapi.MaxOpaqueIDBytes || !utf8.ValidString(value) ||
		value != moduleapi.CanonicalText(value) {
		return fmt.Errorf(
			"module apply plan %s must be canonical nonblank UTF-8 of at most %d bytes",
			label,
			moduleapi.MaxOpaqueIDBytes,
		)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf(
				"module apply plan %s contains a control character",
				label,
			)
		}
	}
	return nil
}

func cloneModuleApplyPlanV1(plan moduleApplyPlanV1) moduleApplyPlanV1 {
	if plan.Module != nil {
		moduleCopy := *plan.Module
		plan.Module = &moduleCopy
	}
	if plan.Binding != nil {
		bindingCopy := *plan.Binding
		bindingCopy.Config = bytes.Clone(plan.Binding.Config)
		bindingCopy.AuthorityCeiling = bytes.Clone(plan.Binding.AuthorityCeiling)
		plan.Binding = &bindingCopy
	}
	plan.ModelProfile = bytes.Clone(plan.ModelProfile)
	if plan.ChannelEndpoint != nil {
		endpointCopy := *plan.ChannelEndpoint
		plan.ChannelEndpoint = &endpointCopy
	}
	plan.CursorSeed = bytes.Clone(plan.CursorSeed)
	return plan
}

func sameModuleApplyPlanPath(left string, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
