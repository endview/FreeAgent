// Package moduleconformance implements offline module package verification for
// the developer CLI. The compatibility path is format-only; its explicit
// Source Policy path adds authority-free policy and cryptographic checks. The
// package deliberately has no Store, Registry, Module Host, network client or
// transport, staging, installation, or execution dependency.
package moduleconformance

import (
	"bytes"
	"context"
	"errors"
	"math"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ReportSchemaVersionV1 = "freeagent.module-package-verification/v1"

	FailureManifestInvalid  FailureCode = "MANIFEST_INVALID"
	FailureArtifactInvalid  FailureCode = "ARTIFACT_INVALID"
	FailureEntrypointAbsent FailureCode = "ENTRYPOINT_ABSENT"
	FailureArtifactDrift    FailureCode = "ARTIFACT_DRIFT"
	FailureSourceInput      FailureCode = "SOURCE_INPUT_INVALID"
	FailureSourceDenied     FailureCode = "SOURCE_DENIED"
	FailureSignatureNeeded  FailureCode = "SIGNATURE_REQUIRED"
	FailureSignatureInvalid FailureCode = "SIGNATURE_INVALID"
	FailurePublisherRevoked FailureCode = "PUBLISHER_KEY_REVOKED"
	FailureSourceDrift      FailureCode = "SOURCE_PREFLIGHT_DRIFT"
	FailureCancelled        FailureCode = "VERIFY_CANCELLED"
	FailureInternal         FailureCode = "INTERNAL_ERROR"
)

// FailureCode is the fixed, non-sensitive diagnostic allowed to cross the CLI
// boundary. The underlying validation error remains available only to an
// in-process caller through errors.Unwrap.
type FailureCode string

type VerificationError struct {
	code  FailureCode
	label string
	cause error
}

func (failure *VerificationError) Error() string {
	if failure == nil {
		return "module conformance: verification failed"
	}
	return "module conformance: " + failure.label
}

func (failure *VerificationError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

// FailureCodeOf returns only a closed, non-sensitive classification. Unknown
// errors from an injected or future dependency collapse to INTERNAL_ERROR.
func FailureCodeOf(err error) FailureCode {
	if err == nil {
		return ""
	}
	var failure *VerificationError
	if errors.As(err, &failure) && failure != nil {
		switch failure.code {
		case FailureManifestInvalid,
			FailureArtifactInvalid,
			FailureEntrypointAbsent,
			FailureArtifactDrift,
			FailureSourceInput,
			FailureSourceDenied,
			FailureSignatureNeeded,
			FailureSignatureInvalid,
			FailurePublisherRevoked,
			FailureSourceDrift,
			FailureCancelled,
			FailureInternal:
			return failure.code
		}
	}
	return FailureInternal
}

// PortRef is a report-local copy of one exact declared Port. Keeping the
// report DTO local prevents future SDK fields from silently changing the CLI
// wire.
type PortRef struct {
	Name         string `json:"name"`
	ExactVersion string `json:"exact_version"`
}

// RuntimeRequest reports the package's untrusted runtime request. It is not an
// ExecutionClass grant and says nothing about Host availability.
type RuntimeRequest struct {
	Mode       string `json:"mode"`
	Protocol   string `json:"protocol"`
	Entrypoint string `json:"entrypoint"`
}

// ModuleIdentity is the exact package identity declared by module.yaml.
type ModuleIdentity struct {
	ID           string `json:"id"`
	ExactVersion string `json:"exact_version"`
}

// Report is the stable, format-only result of verifying one unpacked module
// package. A Report is returned only after the complete package passes; an
// invalid package returns an error and no partial success report.
type Report struct {
	SchemaVersion        string         `json:"schema_version"`
	PackageAPIVersion    string         `json:"package_api_version"`
	Module               ModuleIdentity `json:"module"`
	ArtifactDigest       string         `json:"artifact_digest"`
	ArtifactSizeBytes    uint64         `json:"artifact_size_bytes"`
	CoveredFileCount     uint64         `json:"covered_file_count"`
	RuntimeRequest       RuntimeRequest `json:"runtime_request"`
	Provides             []PortRef      `json:"provides"`
	Requires             []PortRef      `json:"requires"`
	RequestedPermissions []string       `json:"requested_permissions"`
}

// VerifyDirectory validates one unpacked module package without installing,
// activating, binding, loading, or executing it. Envelope metadata is not
// excluded by this source-package verifier: detached signatures and install
// receipts belong outside the artifact directory until an installer owns the
// corresponding envelope policy.
func VerifyDirectory(ctx context.Context, root string) (Report, error) {
	return verifyDirectory(
		ctx,
		root,
		moduleapi.VerifyArtifactDirectoryDigestAndSizeContext,
	)
}

// VerifyDirectoryWithPackageLimit is the Source-Policy-tight variant. It
// preserves the legacy report wire while ensuring the first complete package
// scan, including module.yaml, cannot read beyond maxPackageBytes. The limit
// may only tighten the SDK hard ceiling.
func VerifyDirectoryWithPackageLimit(
	ctx context.Context,
	root string,
	maxPackageBytes uint64,
) (Report, error) {
	report, _, err := VerifyDirectoryWithPackageLimitEvidence(
		ctx,
		root,
		maxPackageBytes,
	)
	return report, err
}

// VerifyDirectoryWithPackageLimitEvidence returns the exact canonical
// Manifest bytes that participated in the verified ArtifactDigest. The bytes
// are detached and are not added to the stable Report wire. Callers that
// compare repeated observations must compare both Report and these bytes.
func VerifyDirectoryWithPackageLimitEvidence(
	ctx context.Context,
	root string,
	maxPackageBytes uint64,
) (Report, []byte, error) {
	limits, err := artifactLimitsForPackage(maxPackageBytes)
	if err != nil {
		return Report{}, nil, verificationFailure(
			FailureSourceInput,
			"source package limit is invalid",
			err,
		)
	}
	var manifestCanonical []byte
	report, err := verifyDirectoryWithLimits(
		ctx,
		root,
		limits,
		true,
		func(
			ctx context.Context,
			artifactRoot string,
			metadata moduleapi.ArtifactMetadataPaths,
			digest string,
			size uint64,
		) error {
			return moduleapi.VerifyArtifactDirectoryDigestAndSizeWithLimitsContext(
				ctx,
				artifactRoot,
				metadata,
				digest,
				size,
				limits,
			)
		},
		func(canonical []byte) {
			manifestCanonical = bytes.Clone(canonical)
		},
	)
	if err != nil {
		return Report{}, nil, err
	}
	return report, bytes.Clone(manifestCanonical), nil
}

type finalVerifyFunc func(
	context.Context,
	string,
	moduleapi.ArtifactMetadataPaths,
	string,
	uint64,
) error

func verifyDirectory(
	ctx context.Context,
	root string,
	finalVerify finalVerifyFunc,
) (Report, error) {
	return verifyDirectoryWithLimits(
		ctx,
		root,
		moduleapi.DefaultArtifactScanLimits(),
		false,
		finalVerify,
		nil,
	)
}

func verifyDirectoryWithLimits(
	ctx context.Context,
	root string,
	limits moduleapi.ArtifactScanLimits,
	scanBeforeManifest bool,
	finalVerify finalVerifyFunc,
	captureManifest func([]byte),
) (Report, error) {
	if ctx == nil {
		return Report{}, errors.New("module conformance: context is nil")
	}
	if finalVerify == nil {
		return Report{}, errors.New("module conformance: final verifier is nil")
	}
	if err := ctx.Err(); err != nil {
		return Report{}, verificationFailure(
			FailureCancelled,
			"verification was cancelled",
			err,
		)
	}
	var (
		files []moduleapi.ArtifactFile
		err   error
	)
	if scanBeforeManifest {
		files, err = moduleapi.ScanArtifactDirectoryWithLimitsContext(
			ctx,
			root,
			moduleapi.ArtifactMetadataPaths{},
			limits,
		)
		if err != nil {
			if errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded) {
				return Report{}, verificationFailure(
					FailureCancelled,
					"verification was cancelled",
					err,
				)
			}
			return Report{}, verificationFailure(
				FailureArtifactInvalid,
				"artifact validation failed",
				err,
			)
		}
	}

	manifestCanonical, err :=
		moduleapi.ReadArtifactManifestV1FromDirectoryContext(ctx, root)
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return Report{}, verificationFailure(
				FailureCancelled,
				"verification was cancelled",
				err,
			)
		}
		return Report{}, verificationFailure(
			FailureManifestInvalid,
			"manifest validation failed",
			err,
		)
	}
	manifest, canonical, err :=
		moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil || !bytes.Equal(manifestCanonical, canonical) {
		return Report{}, verificationFailure(
			FailureManifestInvalid,
			"manifest validation failed",
			err,
		)
	}

	if !scanBeforeManifest {
		files, err = moduleapi.ScanArtifactDirectoryWithLimitsContext(
			ctx,
			root,
			moduleapi.ArtifactMetadataPaths{},
			limits,
		)
		if err != nil {
			if errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded) {
				return Report{}, verificationFailure(
					FailureCancelled,
					"verification was cancelled",
					err,
				)
			}
			return Report{}, verificationFailure(
				FailureArtifactInvalid,
				"artifact validation failed",
				err,
			)
		}
	}
	if err := ctx.Err(); err != nil {
		return Report{}, verificationFailure(
			FailureCancelled,
			"verification was cancelled",
			err,
		)
	}
	if requiresFileEntrypoint(manifest.Runtime) &&
		!containsExactFile(files, manifest.Runtime.Entrypoint) {
		return Report{}, verificationFailure(
			FailureEntrypointAbsent,
			"runtime file entrypoint is absent from the artifact",
			nil,
		)
	}

	digest, err := moduleapi.ComputeArtifactDigest(canonical, files)
	if err != nil {
		return Report{}, verificationFailure(
			FailureArtifactInvalid,
			"artifact digest validation failed",
			err,
		)
	}
	size, err := coveredSize(canonical, files)
	if err != nil {
		return Report{}, verificationFailure(
			FailureArtifactInvalid,
			"artifact size validation failed",
			err,
		)
	}
	// Re-read the whole package through the streaming verifier. Besides
	// checking the computed values, this detects drift between the captured
	// pass and the final verification pass without retaining another copy.
	if err := finalVerify(
		ctx,
		root,
		moduleapi.ArtifactMetadataPaths{},
		digest,
		size,
	); err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return Report{}, verificationFailure(
				FailureCancelled,
				"verification was cancelled",
				err,
			)
		}
		return Report{}, verificationFailure(
			FailureArtifactDrift,
			"artifact changed or failed final verification",
			err,
		)
	}

	report := Report{
		SchemaVersion:     ReportSchemaVersionV1,
		PackageAPIVersion: manifest.APIVersion,
		Module: ModuleIdentity{
			ID:           manifest.ID,
			ExactVersion: manifest.Version,
		},
		ArtifactDigest:    digest,
		ArtifactSizeBytes: size,
		CoveredFileCount:  uint64(len(files)) + 1,
		RuntimeRequest: RuntimeRequest{
			Mode:       string(manifest.Runtime.Mode),
			Protocol:   manifest.Runtime.Protocol,
			Entrypoint: manifest.Runtime.Entrypoint,
		},
		Provides:             reportPorts(manifest.Provides),
		Requires:             reportPorts(manifest.Requires),
		RequestedPermissions: reportPermissions(manifest.RequestedPermissions),
	}
	if captureManifest != nil {
		captureManifest(canonical)
	}
	return report, nil
}

func artifactLimitsForPackage(
	maxPackageBytes uint64,
) (moduleapi.ArtifactScanLimits, error) {
	defaults := moduleapi.DefaultArtifactScanLimits()
	if maxPackageBytes == 0 ||
		maxPackageBytes > uint64(defaults.MaxTotalBytes) {
		return moduleapi.ArtifactScanLimits{}, errors.New(
			"package limit must be positive and within the artifact hard ceiling",
		)
	}
	limits := defaults
	limits.MaxTotalBytes = int64(maxPackageBytes)
	if limits.MaxFileBytes > limits.MaxTotalBytes {
		limits.MaxFileBytes = limits.MaxTotalBytes
	}
	return limits, nil
}

func verificationFailure(
	code FailureCode,
	label string,
	cause error,
) error {
	return &VerificationError{code: code, label: label, cause: cause}
}

func requiresFileEntrypoint(request moduleapi.RuntimeRequestV1) bool {
	return request.Mode == moduleapi.RuntimeModeRequestDeclarative ||
		request.Mode == moduleapi.RuntimeModeRequestLocalProcess ||
		(request.Mode == moduleapi.RuntimeModeRequestRemote &&
			request.Protocol == moduleapi.RuntimeProtocolFreeAgentActionHTTPV1) ||
		(request.Mode == moduleapi.RuntimeModeRequestWASM &&
			request.Protocol == moduleapi.RuntimeProtocolFreeAgentActionWASMV1)
}

func containsExactFile(files []moduleapi.ArtifactFile, path string) bool {
	for _, file := range files {
		if file.Path == path {
			return true
		}
	}
	return false
}

func coveredSize(
	manifest []byte,
	files []moduleapi.ArtifactFile,
) (uint64, error) {
	size := uint64(len(manifest))
	for _, file := range files {
		fileSize := uint64(len(file.Content))
		if fileSize > math.MaxUint64-size {
			return 0, errors.New("module conformance: artifact size overflow")
		}
		size += fileSize
	}
	if size == 0 {
		return 0, errors.New("module conformance: artifact is empty")
	}
	return size, nil
}

func reportPorts(input []moduleapi.PortRef) []PortRef {
	output := make([]PortRef, len(input))
	for index, port := range input {
		output[index] = PortRef{
			Name:         port.Name,
			ExactVersion: port.ExactVersion,
		}
	}
	return output
}

func reportPermissions(input []moduleapi.Permission) []string {
	output := make([]string, len(input))
	for index, permission := range input {
		output[index] = string(permission)
	}
	return output
}
