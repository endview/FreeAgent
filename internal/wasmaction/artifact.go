package wasmaction

import (
	"bytes"
	"context"
	"fmt"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const maxArtifactTotalBytesV1 = int64(moduleapi.MaxTextBytes) +
	int64(MaxDescriptorBytesV1) + int64(MaxModuleBytesV1)

// LoadArtifactFromDirectory captures, verifies and returns the exact
// descriptor and Wasm bytes selected by one activated provider. The captured
// bytes are consumed directly; the executable path is never reopened after
// the ArtifactDigest check.
func LoadArtifactFromDirectory(
	ctx context.Context,
	provider moduleapi.ActivatedModuleRef,
	artifactDirectory string,
	expectedCoveredSize uint64,
) (DescriptorV1, []byte, error) {
	if ctx == nil {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidDescriptor,
		)
	}
	if err := ctx.Err(); err != nil {
		return DescriptorV1{}, nil, err
	}
	if err := validateProvider(provider); err != nil {
		return DescriptorV1{}, nil, err
	}
	if expectedCoveredSize == 0 || expectedCoveredSize > uint64(maxArtifactTotalBytesV1) {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: covered artifact size is outside the protocol ceiling",
			ErrInvalidDescriptor,
		)
	}

	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: read manifest",
			ErrInvalidDescriptor,
		)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil || manifest.ID != provider.ModuleID ||
		manifest.Version != provider.Version ||
		manifest.Runtime.Mode != moduleapi.RuntimeModeRequestWASM ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolFreeAgentActionWASMV1 ||
		len(manifest.Provides) != 1 ||
		manifest.Provides[0] != actionProviderPortV1 ||
		len(manifest.Requires) != 0 ||
		len(manifest.RequestedPermissions) != 0 ||
		len(manifest.ConfigSchema) != 0 ||
		len(manifest.Lifecycle) != 0 ||
		len(manifest.Health) != 0 {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: manifest is not the exact WASM action.provider/v1 shape",
			ErrInvalidDescriptor,
		)
	}

	files, err := moduleapi.ScanArtifactDirectoryWithLimits(
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
		moduleapi.ArtifactScanLimits{
			MaxPaths:      64,
			MaxFiles:      3, // module.yaml, descriptor and Wasm binary
			MaxFileBytes:  MaxModuleBytesV1,
			MaxTotalBytes: maxArtifactTotalBytesV1,
		},
	)
	if err != nil || len(files) != 2 {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: artifact must contain only descriptor and Wasm module",
			ErrInvalidDescriptor,
		)
	}
	if err := ctx.Err(); err != nil {
		return DescriptorV1{}, nil, err
	}

	observedDigest, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		files,
	)
	if err != nil || observedDigest != provider.ArtifactDigest {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: verify captured artifact digest",
			ErrInvalidDescriptor,
		)
	}
	observedCoveredSize := uint64(len(manifestCanonical))
	for _, file := range files {
		observedCoveredSize += uint64(len(file.Content))
	}
	if observedCoveredSize != expectedCoveredSize {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: captured artifact covered size does not match expected size",
			ErrInvalidDescriptor,
		)
	}

	var descriptorBytes []byte
	for _, file := range files {
		if file.Path == manifest.Runtime.Entrypoint {
			descriptorBytes = bytes.Clone(file.Content)
			break
		}
	}
	if len(descriptorBytes) == 0 || len(descriptorBytes) > MaxDescriptorBytesV1 {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: manifest-selected descriptor is absent or oversized",
			ErrInvalidDescriptor,
		)
	}
	descriptor, err := RestoreDescriptorV1(descriptorBytes)
	if err != nil || descriptor.ModulePath == manifest.Runtime.Entrypoint {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: descriptor module_path is invalid",
			ErrInvalidDescriptor,
		)
	}

	var wasm []byte
	for _, file := range files {
		if file.Path == descriptor.ModulePath {
			wasm = bytes.Clone(file.Content)
			break
		}
	}
	if len(wasm) == 0 || len(wasm) > MaxModuleBytesV1 {
		return DescriptorV1{}, nil, fmt.Errorf(
			"%w: descriptor-selected Wasm module is absent or oversized",
			ErrInvalidDescriptor,
		)
	}
	return cloneDescriptor(descriptor), wasm, nil
}

func NewFromArtifact(
	ctx context.Context,
	provider moduleapi.ActivatedModuleRef,
	artifactDirectory string,
	expectedCoveredSize uint64,
) (*Adapter, error) {
	descriptor, wasm, err := LoadArtifactFromDirectory(
		ctx,
		provider,
		artifactDirectory,
		expectedCoveredSize,
	)
	if err != nil {
		return nil, err
	}
	return New(ctx, provider, descriptor, wasm)
}
