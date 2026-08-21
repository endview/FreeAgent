// Package remoteactionhttp implements the Core-owned
// freeagent-action-http/v1 Host Adapter. Describe and Prepare are derived from
// an offline, artifact-covered descriptor; only the private Action Gateway
// executor may perform network I/O.
package remoteactionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	AdapterIdentityV1    = "freeagent.adapter.action.remote-http/v1"
	DescriptorSchemaV1   = "freeagent-action-http-descriptor/v1"
	MaxDescriptorBytesV1 = moduleapi.MaxActionDefinitionAggregateBytesV1 +
		moduleapi.MaxConfigBytes
)

var ErrInvalidDescriptor = errors.New(
	"remoteactionhttp: invalid offline descriptor",
)

// DescriptorV1 is the complete offline description of one REMOTE Action
// Provider. It contains no endpoint, credential identity, dynamic discovery
// URL or executable content.
type DescriptorV1 struct {
	SchemaVersion string                         `json:"schema_version"`
	Actions       []moduleapi.ActionDefinitionV1 `json:"actions"`
}

func NewDescriptorV1(input DescriptorV1) (DescriptorV1, []byte, error) {
	if input.SchemaVersion != DescriptorSchemaV1 {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: schema_version must be %q",
			ErrInvalidDescriptor,
			DescriptorSchemaV1,
		)
	}
	actions, err := moduleapi.FreezeActionDefinitionsV1(input.Actions)
	if err != nil {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: actions: %v",
			ErrInvalidDescriptor,
			err,
		)
	}
	aggregate := 0
	for _, action := range actions {
		aggregate += len(action.Description) + len(action.InputSchema)
		if aggregate > moduleapi.MaxActionDefinitionAggregateBytesV1 {
			return DescriptorV1{}, nil, fmt.Errorf(
				"%w: Action definition aggregate exceeds %d bytes",
				ErrInvalidDescriptor,
				moduleapi.MaxActionDefinitionAggregateBytesV1,
			)
		}
	}
	frozen := DescriptorV1{
		SchemaVersion: input.SchemaVersion,
		Actions:       cloneDefinitions(actions),
	}
	encoded, err := json.Marshal(frozen)
	if err != nil {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: encode: %v",
			ErrInvalidDescriptor,
			err,
		)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		encoded,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaxDescriptorBytesV1,
			MaxDepth: 128,
			MaxNodes: MaxDescriptorBytesV1,
		},
	)
	if err != nil {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: canonicalize: %v",
			ErrInvalidDescriptor,
			err,
		)
	}
	return frozen, bytes.Clone(canonical), nil
}

func RestoreDescriptorV1(canonical []byte) (DescriptorV1, error) {
	owned := bytes.Clone(canonical)
	if len(owned) == 0 || len(owned) > MaxDescriptorBytesV1 {
		return DescriptorV1{}, fmt.Errorf(
			"%w: wire must contain between 1 and %d bytes",
			ErrInvalidDescriptor,
			MaxDescriptorBytesV1,
		)
	}
	normalized, err := moduleapi.CanonicalJSONWithLimits(
		owned,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: MaxDescriptorBytesV1,
			MaxDepth: 128,
			MaxNodes: MaxDescriptorBytesV1,
		},
	)
	if err != nil || len(normalized) == 0 || normalized[0] != '{' ||
		!bytes.Equal(owned, normalized) {
		return DescriptorV1{}, fmt.Errorf(
			"%w: wire must be an exact canonical JSON object",
			ErrInvalidDescriptor,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(owned))
	decoder.DisallowUnknownFields()
	var decoded DescriptorV1
	if err := decoder.Decode(&decoded); err != nil {
		return DescriptorV1{}, fmt.Errorf(
			"%w: decode: %v",
			ErrInvalidDescriptor,
			err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return DescriptorV1{}, fmt.Errorf(
			"%w: trailing JSON",
			ErrInvalidDescriptor,
		)
	}
	frozen, rebuilt, err := NewDescriptorV1(decoded)
	if err != nil {
		return DescriptorV1{}, err
	}
	if !bytes.Equal(owned, rebuilt) {
		return DescriptorV1{}, fmt.Errorf(
			"%w: wire is not frozen canonically",
			ErrInvalidDescriptor,
		)
	}
	return frozen, nil
}

// LoadDescriptorFromArtifact validates the narrow package shape, verifies the
// complete selected artifact, and only then accepts its manifest-selected
// descriptor. It performs no network, secret, Store, activation, discovery or
// registration operation.
func LoadDescriptorFromArtifact(
	ctx context.Context,
	provider moduleapi.ActivatedModuleRef,
	artifactDirectory string,
	expectedCoveredSize uint64,
) (DescriptorV1, error) {
	if ctx == nil {
		return DescriptorV1{}, fmt.Errorf("%w: context is nil", ErrInvalidDescriptor)
	}
	if err := ctx.Err(); err != nil {
		return DescriptorV1{}, err
	}
	if err := validateProvider(provider); err != nil {
		return DescriptorV1{}, err
	}
	if expectedCoveredSize == 0 {
		return DescriptorV1{}, fmt.Errorf(
			"%w: expected covered artifact size must be positive",
			ErrInvalidDescriptor,
		)
	}
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return DescriptorV1{}, fmt.Errorf(
			"%w: read manifest: %v",
			ErrInvalidDescriptor,
			err,
		)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil || manifest.ID != provider.ModuleID ||
		manifest.Version != provider.Version ||
		manifest.Runtime.Mode != moduleapi.RuntimeModeRequestRemote ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolFreeAgentActionHTTPV1 ||
		len(manifest.Provides) != 1 ||
		manifest.Provides[0] != actionProviderPortV1 ||
		len(manifest.Requires) != 0 ||
		len(manifest.RequestedPermissions) != 0 ||
		len(manifest.ConfigSchema) != 0 ||
		len(manifest.Lifecycle) != 0 ||
		len(manifest.Health) != 0 {
		return DescriptorV1{}, fmt.Errorf(
			"%w: manifest is not the exact REMOTE action.provider/v1 shape",
			ErrInvalidDescriptor,
		)
	}
	maximumFileBytes := int64(MaxDescriptorBytesV1)
	if manifestMaximum := int64(moduleapi.MaxTextBytes); manifestMaximum > maximumFileBytes {
		maximumFileBytes = manifestMaximum
	}
	maximumTotalBytes := int64(MaxDescriptorBytesV1) + int64(moduleapi.MaxTextBytes)
	if expectedCoveredSize > uint64(maximumTotalBytes) {
		return DescriptorV1{}, fmt.Errorf(
			"%w: covered artifact exceeds the native protocol ceiling",
			ErrInvalidDescriptor,
		)
	}
	// Preflight with the native protocol's much tighter shape before the
	// generic digest verifier walks the package. This prevents an exact but
	// oversized operator-supplied artifact from consuming the generic 256 MiB
	// scan allowance merely to prove that it contains an extra file.
	files, err := moduleapi.ScanArtifactDirectoryWithLimits(
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
		moduleapi.ArtifactScanLimits{
			MaxPaths:      64,
			MaxFiles:      2, // module.yaml plus exactly one descriptor
			MaxFileBytes:  maximumFileBytes,
			MaxTotalBytes: maximumTotalBytes,
		},
	)
	if err != nil || len(files) != 1 ||
		files[0].Path != manifest.Runtime.Entrypoint {
		return DescriptorV1{}, fmt.Errorf(
			"%w: artifact must contain only the manifest-selected descriptor",
			ErrInvalidDescriptor,
		)
	}
	if err := ctx.Err(); err != nil {
		return DescriptorV1{}, err
	}
	// Verify and consume the exact bytes captured by the bounded scan. A
	// second directory verification followed by a third descriptor read would
	// reopen a verify/read race in which the descriptor could change after its
	// digest was accepted.
	observedDigest, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		files,
	)
	if err != nil || observedDigest != provider.ArtifactDigest {
		return DescriptorV1{}, fmt.Errorf(
			"%w: verify captured artifact digest",
			ErrInvalidDescriptor,
		)
	}
	observedCoveredSize := uint64(len(manifestCanonical))
	for _, file := range files {
		observedCoveredSize += uint64(len(file.Content))
	}
	if observedCoveredSize != expectedCoveredSize {
		return DescriptorV1{}, fmt.Errorf(
			"%w: captured artifact covered size %d does not match expected %d",
			ErrInvalidDescriptor,
			observedCoveredSize,
			expectedCoveredSize,
		)
	}
	descriptorCanonical := bytes.Clone(files[0].Content)
	descriptor, err := RestoreDescriptorV1(descriptorCanonical)
	if err != nil {
		return DescriptorV1{}, err
	}
	return cloneDescriptor(descriptor), nil
}

func cloneDescriptor(input DescriptorV1) DescriptorV1 {
	return DescriptorV1{
		SchemaVersion: input.SchemaVersion,
		Actions:       cloneDefinitions(input.Actions),
	}
}

func cloneDefinitions(input []moduleapi.ActionDefinitionV1) []moduleapi.ActionDefinitionV1 {
	output := make([]moduleapi.ActionDefinitionV1, len(input))
	for index, definition := range input {
		definition.InputSchema = bytes.Clone(definition.InputSchema)
		output[index] = definition
	}
	return output
}

func validateProvider(provider moduleapi.ActivatedModuleRef) error {
	if err := provider.Validate(); err != nil {
		return fmt.Errorf("remoteactionhttp: invalid Provider: %v", err)
	}
	if provider.ExecutionClass != moduleapi.ExecutionRemote ||
		provider.AdapterIdentity != AdapterIdentityV1 {
		return fmt.Errorf(
			"remoteactionhttp: Provider must be the exact REMOTE freeagent-action-http/v1 adapter",
		)
	}
	return nil
}

var actionProviderPortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameActionProvider,
	ExactVersion: moduleapi.PortVersionV1,
}
