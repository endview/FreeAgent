package exactadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const MemoryAdapterDescriptorSchemaV1 = "memory-adapter-descriptor/v1"

type memoryAdapterDescriptorV1 struct {
	SchemaVersion   string `json:"schema_version"`
	AdapterIdentity string `json:"adapter_identity"`
}

// NewDeterministicMemoryFromArtifact closes the compiled selector against one
// exact immutable package. The descriptor binds the package digest to the
// adapter identity; ModuleID is only artifact identity and is never used as a
// protocol or implementation switch.
func NewDeterministicMemoryFromArtifact(
	provider moduleapi.ActivatedModuleRef,
	manifestCanonical []byte,
	files []moduleapi.ArtifactFile,
) (*DeterministicMemory, error) {
	if err := provider.Validate(); err != nil {
		return nil, fmt.Errorf("%w: provider: %v", ErrInvalidMemoryProvider, err)
	}
	if provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess {
		return nil, fmt.Errorf(
			"%w: execution class must be %q",
			ErrInvalidMemoryProvider,
			moduleapi.ExecutionTrustedInProcess,
		)
	}
	manifest, rebuiltManifest, err := moduleapi.ParseModuleManifestV1(
		append([]byte(nil), manifestCanonical...),
	)
	if err != nil || !bytes.Equal(manifestCanonical, rebuiltManifest) {
		return nil, fmt.Errorf(
			"%w: manifest is not exact canonical module/v1: %v",
			ErrInvalidMemoryProvider,
			err,
		)
	}
	digest, err := moduleapi.ComputeArtifactDigest(rebuiltManifest, files)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: compute artifact digest: %v",
			ErrInvalidMemoryProvider,
			err,
		)
	}
	if digest != provider.ArtifactDigest ||
		manifest.ID != provider.ModuleID || manifest.Version != provider.Version {
		return nil, fmt.Errorf(
			"%w: artifact digest or manifest identity differs from exact provider",
			ErrInvalidMemoryProvider,
		)
	}
	if manifest.Runtime.Mode != moduleapi.RuntimeModeRequestTrustedInProcess ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolGoInProcessV1 ||
		len(manifest.Provides) != 1 || manifest.Provides[0] != contextProvidePortV1 ||
		len(manifest.Requires) != 0 || len(manifest.RequestedPermissions) != 0 {
		return nil, fmt.Errorf(
			"%w: artifact must provide only context.provide/v1 as permissionless TRUSTED_IN_PROCESS/go-in-process/v1",
			ErrInvalidMemoryProvider,
		)
	}
	entrypoint, err := moduleapi.NormalizeArtifactPath(manifest.Runtime.Entrypoint)
	if err != nil || entrypoint != manifest.Runtime.Entrypoint ||
		!strings.HasPrefix(entrypoint, "content/") {
		return nil, fmt.Errorf(
			"%w: artifact entrypoint must be a canonical content/ path",
			ErrInvalidMemoryProvider,
		)
	}
	var descriptorCanonical []byte
	for _, file := range files {
		if file.Path == entrypoint {
			descriptorCanonical = append([]byte(nil), file.Content...)
			break
		}
	}
	if descriptorCanonical == nil {
		return nil, fmt.Errorf(
			"%w: artifact descriptor entrypoint is missing",
			ErrInvalidMemoryProvider,
		)
	}
	checked, err := moduleapi.CanonicalJSONWithLimits(
		descriptorCanonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: moduleapi.MaxConfigBytes,
			MaxDepth: 16,
			MaxNodes: 32,
		},
	)
	if err != nil || !bytes.Equal(checked, descriptorCanonical) {
		return nil, fmt.Errorf(
			"%w: artifact descriptor is not exact canonical JSON",
			ErrInvalidMemoryProvider,
		)
	}
	var descriptor memoryAdapterDescriptorV1
	decoder := json.NewDecoder(bytes.NewReader(descriptorCanonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return nil, fmt.Errorf(
			"%w: restore artifact descriptor: %v",
			ErrInvalidMemoryProvider,
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		// Decode already consumed canonical JSON. A second successful value is
		// impossible after CanonicalJSON, but retain an explicit exactness gate.
		if err == nil {
			return nil, fmt.Errorf(
				"%w: artifact descriptor contains trailing JSON",
				ErrInvalidMemoryProvider,
			)
		}
	}
	if descriptor.SchemaVersion != MemoryAdapterDescriptorSchemaV1 ||
		descriptor.AdapterIdentity != provider.AdapterIdentity {
		return nil, fmt.Errorf(
			"%w: artifact descriptor does not bind the exact adapter identity",
			ErrInvalidMemoryProvider,
		)
	}
	return NewDeterministicMemory(provider)
}
