package moduleapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	// ModuleManifestAPIVersionV1 is the sole S1 module manifest API version.
	ModuleManifestAPIVersionV1 = "freeagent.module/v1"

	RuntimeProtocolStaticV1              = "static/v1"
	RuntimeProtocolGoInProcessV1         = "go-in-process/v1"
	RuntimeProtocolMCPStdio20251125      = "mcp-stdio/2025-11-25"
	RuntimeProtocolFreeAgentActionHTTPV1 = "freeagent-action-http/v1"
	RuntimeProtocolFreeAgentActionWASMV1 = "freeagent-action-wasm/v1"

	maxModuleManifestDepth = 128
)

// RuntimeModeRequest is an untrusted runtime request from a module package.
// It is intentionally a distinct type from ExecutionClass: only Core
// activation policy may assign the latter.
type RuntimeModeRequest string

const (
	RuntimeModeRequestDeclarative      RuntimeModeRequest = "DECLARATIVE"
	RuntimeModeRequestTrustedInProcess RuntimeModeRequest = "TRUSTED_IN_PROCESS"
	RuntimeModeRequestLocalProcess     RuntimeModeRequest = "LOCAL_PROCESS"
	RuntimeModeRequestRemote           RuntimeModeRequest = "REMOTE"
	RuntimeModeRequestWASM             RuntimeModeRequest = "WASM"
)

// RuntimeRequestV1 describes how a package asks to be interpreted. Its values
// are requests only and confer no trust, authority or execution class.
type RuntimeRequestV1 struct {
	Mode       RuntimeModeRequest `json:"mode"`
	Protocol   string             `json:"protocol"`
	Entrypoint string             `json:"entrypoint"`
}

// ModuleManifestV1 is the complete S1 manifest declaration. ConfigSchema,
// Lifecycle and Health are inert canonical JSON objects; accepting them in the
// package declaration does not grant authority or enable lifecycle commands.
type ModuleManifestV1 struct {
	APIVersion           string           `json:"api_version"`
	ID                   string           `json:"id"`
	Version              string           `json:"version"`
	Runtime              RuntimeRequestV1 `json:"runtime"`
	Provides             []PortRef        `json:"provides"`
	Requires             []PortRef        `json:"requires,omitempty"`
	RequestedPermissions []Permission     `json:"requested_permissions,omitempty"`
	ConfigSchema         json.RawMessage  `json:"config_schema,omitempty"`
	Lifecycle            json.RawMessage  `json:"lifecycle,omitempty"`
	Health               json.RawMessage  `json:"health,omitempty"`
}

// ParseModuleManifestV1 accepts only an exact RFC 8785 canonical JSON object.
// In S1, module.yaml is this canonical JSON byte sequence, which is also a
// YAML 1.2-compatible JSON subset; no general YAML parser is involved.
//
// Both the returned manifest collections and canonical bytes are defensive
// copies owned by the caller.
func ParseModuleManifestV1(
	input []byte,
) (ModuleManifestV1, []byte, error) {
	candidate := bytes.Clone(input)
	canonical, err := CanonicalJSONWithLimits(
		candidate,
		CanonicalJSONLimits{
			MaxBytes: MaxTextBytes,
			MaxDepth: maxModuleManifestDepth,
			MaxNodes: MaxTextBytes,
		},
	)
	if err != nil {
		return ModuleManifestV1{}, nil,
			fmt.Errorf("moduleapi: parse module manifest v1: %w", err)
	}
	if len(canonical) == 0 || canonical[0] != '{' {
		return ModuleManifestV1{}, nil,
			fmt.Errorf("moduleapi: module manifest v1 must be a JSON object")
	}
	if !bytes.Equal(candidate, canonical) {
		return ModuleManifestV1{}, nil,
			fmt.Errorf("moduleapi: module manifest v1 must use exact RFC 8785 canonical JSON")
	}

	decoder := json.NewDecoder(bytes.NewReader(candidate))
	decoder.DisallowUnknownFields()
	var manifest ModuleManifestV1
	if err := decoder.Decode(&manifest); err != nil {
		return ModuleManifestV1{}, nil,
			fmt.Errorf("moduleapi: decode module manifest v1: %w", err)
	}
	if err := requireManifestEOF(decoder); err != nil {
		return ModuleManifestV1{}, nil, err
	}
	if err := manifest.Validate(); err != nil {
		return ModuleManifestV1{}, nil, err
	}

	return cloneModuleManifestV1(manifest), bytes.Clone(canonical), nil
}

// Validate checks declaration syntax only. It does not install, activate,
// authorize or bind a module.
func (manifest ModuleManifestV1) Validate() error {
	if manifest.APIVersion != ModuleManifestAPIVersionV1 {
		return fmt.Errorf(
			"moduleapi: module manifest api_version must be %q",
			ModuleManifestAPIVersionV1,
		)
	}
	ref := Ref{ID: manifest.ID, Version: manifest.Version}
	if err := ref.Validate(); err != nil {
		return fmt.Errorf("moduleapi: module manifest identity: %w", err)
	}
	if err := manifest.Runtime.validate(); err != nil {
		return err
	}
	if len(manifest.Provides) == 0 {
		return fmt.Errorf("moduleapi: module manifest must provide at least one exact port")
	}
	if len(manifest.Provides) > MaxManifestEntries ||
		len(manifest.Requires) > MaxManifestEntries ||
		len(manifest.RequestedPermissions) > MaxManifestEntries {
		return fmt.Errorf(
			"moduleapi: module manifest lists may contain at most %d entries each",
			MaxManifestEntries,
		)
	}

	provided := make(map[string]struct{}, len(manifest.Provides))
	for index, port := range manifest.Provides {
		key, err := port.CanonicalKey()
		if err != nil {
			return fmt.Errorf(
				"moduleapi: module manifest provides[%d]: %w",
				index,
				err,
			)
		}
		if _, duplicate := provided[key]; duplicate {
			return fmt.Errorf(
				"moduleapi: module manifest provides exact port %s/%s more than once",
				port.Name,
				port.ExactVersion,
			)
		}
		provided[key] = struct{}{}
	}

	required := make(map[string]struct{}, len(manifest.Requires))
	for index, port := range manifest.Requires {
		key, err := port.CanonicalKey()
		if err != nil {
			return fmt.Errorf(
				"moduleapi: module manifest requires[%d]: %w",
				index,
				err,
			)
		}
		if _, duplicate := required[key]; duplicate {
			return fmt.Errorf(
				"moduleapi: module manifest requires exact port %s/%s more than once",
				port.Name,
				port.ExactVersion,
			)
		}
		if _, selfProvided := provided[key]; selfProvided {
			return fmt.Errorf(
				"moduleapi: module manifest cannot both provide and require exact port %s/%s",
				port.Name,
				port.ExactVersion,
			)
		}
		required[key] = struct{}{}
	}

	permissions := make(map[Permission]struct{}, len(manifest.RequestedPermissions))
	for index, permission := range manifest.RequestedPermissions {
		if err := permission.Validate(); err != nil {
			return fmt.Errorf(
				"moduleapi: module manifest requested_permissions[%d]: %w",
				index,
				err,
			)
		}
		if _, duplicate := permissions[permission]; duplicate {
			return fmt.Errorf(
				"moduleapi: module manifest requests permission %q more than once",
				permission,
			)
		}
		permissions[permission] = struct{}{}
	}

	for _, optional := range []struct {
		name  string
		value json.RawMessage
	}{
		{name: "config_schema", value: manifest.ConfigSchema},
		{name: "lifecycle", value: manifest.Lifecycle},
		{name: "health", value: manifest.Health},
	} {
		if err := validateOptionalManifestObject(optional.name, optional.value); err != nil {
			return err
		}
	}
	return nil
}

func (request RuntimeRequestV1) validate() error {
	if err := validateOpaqueID("runtime protocol", request.Protocol); err != nil {
		return fmt.Errorf("moduleapi: module manifest %w", err)
	}
	if err := validateOpaqueID("runtime entrypoint", request.Entrypoint); err != nil {
		return fmt.Errorf("moduleapi: module manifest %w", err)
	}

	switch request.Mode {
	case RuntimeModeRequestDeclarative:
		if request.Protocol != RuntimeProtocolStaticV1 {
			return fmt.Errorf(
				"moduleapi: DECLARATIVE runtime protocol must be %q",
				RuntimeProtocolStaticV1,
			)
		}
		normalized, err := NormalizeArtifactPath(request.Entrypoint)
		if err != nil {
			return fmt.Errorf(
				"moduleapi: DECLARATIVE runtime entrypoint: %w",
				err,
			)
		}
		if normalized != request.Entrypoint ||
			!strings.HasPrefix(normalized, "content/") {
			return fmt.Errorf(
				"moduleapi: DECLARATIVE runtime entrypoint must be a canonical content/ relative path",
			)
		}
	case RuntimeModeRequestTrustedInProcess:
		if request.Protocol != RuntimeProtocolGoInProcessV1 {
			return fmt.Errorf(
				"moduleapi: TRUSTED_IN_PROCESS runtime protocol must be %q",
				RuntimeProtocolGoInProcessV1,
			)
		}
		// Entrypoint has already been validated as an opaque adapter identity.
	case RuntimeModeRequestLocalProcess:
		if request.Protocol != RuntimeProtocolMCPStdio20251125 {
			return fmt.Errorf(
				"moduleapi: LOCAL_PROCESS runtime protocol must be %q",
				RuntimeProtocolMCPStdio20251125,
			)
		}
		normalized, err := NormalizeArtifactPath(request.Entrypoint)
		if err != nil {
			return fmt.Errorf(
				"moduleapi: LOCAL_PROCESS runtime entrypoint: %w",
				err,
			)
		}
		if normalized != request.Entrypoint ||
			!strings.HasPrefix(normalized, "content/") {
			return fmt.Errorf(
				"moduleapi: LOCAL_PROCESS runtime entrypoint must be a canonical content/ relative path",
			)
		}
	case RuntimeModeRequestRemote:
		// REMOTE protocol and entrypoint remain opaque install-time requests.
		// Recognizing a request here never grants a Host or network authority;
		// an exact Core handler and local Operator policy own that decision.
		// The one protocol understood by v1 additionally freezes a covered,
		// local descriptor path instead of accepting an endpoint in Manifest.
		if request.Protocol == RuntimeProtocolFreeAgentActionHTTPV1 {
			normalized, err := NormalizeArtifactPath(request.Entrypoint)
			if err != nil {
				return fmt.Errorf(
					"moduleapi: REMOTE freeagent-action-http/v1 entrypoint: %w",
					err,
				)
			}
			if normalized != request.Entrypoint ||
				!strings.HasPrefix(normalized, "content/") {
				return fmt.Errorf(
					"moduleapi: REMOTE freeagent-action-http/v1 entrypoint must be a canonical content/ relative path",
				)
			}
		}
		// The entrypoint is an offline descriptor covered by ArtifactDigest,
		// never a URL. Endpoint and SecretRef authority remain consumer-owned
		// Binding facts and are unavailable to the current protocol's package
		// manifest.
	case RuntimeModeRequestWASM:
		// WASM requests remain inert until an exact Core handler and local
		// artifact grant assign an ExecutionClass. The one protocol understood
		// by v1 selects an artifact-covered offline descriptor; unknown future
		// protocols remain opaque install-time requests with no execution right.
		if request.Protocol == RuntimeProtocolFreeAgentActionWASMV1 {
			normalized, err := NormalizeArtifactPath(request.Entrypoint)
			if err != nil {
				return fmt.Errorf(
					"moduleapi: WASM freeagent-action-wasm/v1 entrypoint: %w",
					err,
				)
			}
			if normalized != request.Entrypoint ||
				!strings.HasPrefix(normalized, "content/") {
				return fmt.Errorf(
					"moduleapi: WASM freeagent-action-wasm/v1 entrypoint must be a canonical content/ relative path",
				)
			}
		}
	default:
		return fmt.Errorf(
			"moduleapi: unsupported runtime mode request %q",
			request.Mode,
		)
	}
	return nil
}

func validateOptionalManifestObject(
	name string,
	value json.RawMessage,
) error {
	if value == nil {
		return nil
	}
	if len(value) == 0 {
		return fmt.Errorf(
			"moduleapi: module manifest %s must be a canonical JSON object",
			name,
		)
	}
	canonical, err := CanonicalJSONWithLimits(
		value,
		CanonicalJSONLimits{
			MaxBytes: MaxConfigBytes,
			MaxDepth: maxModuleManifestDepth,
			MaxNodes: MaxConfigBytes,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"moduleapi: module manifest %s: %w",
			name,
			err,
		)
	}
	if canonical[0] != '{' || !bytes.Equal(canonical, value) {
		return fmt.Errorf(
			"moduleapi: module manifest %s must be a canonical JSON object",
			name,
		)
	}
	return nil
}

func requireManifestEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("moduleapi: module manifest v1 has trailing JSON")
		}
		return fmt.Errorf(
			"moduleapi: decode module manifest v1 trailing data: %w",
			err,
		)
	}
	return nil
}

func cloneModuleManifestV1(manifest ModuleManifestV1) ModuleManifestV1 {
	manifest.Provides = append([]PortRef(nil), manifest.Provides...)
	manifest.Requires = append([]PortRef(nil), manifest.Requires...)
	manifest.RequestedPermissions = append(
		[]Permission(nil),
		manifest.RequestedPermissions...,
	)
	manifest.ConfigSchema = bytes.Clone(manifest.ConfigSchema)
	manifest.Lifecycle = bytes.Clone(manifest.Lifecycle)
	manifest.Health = bytes.Clone(manifest.Health)
	return manifest
}
