package moduleconformance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

// SourceCandidateInput is an offline, authority-free observation request. The
// three canonical envelopes come from an explicit local Operator channel;
// none may be discovered inside the module artifact itself. Revocation is a
// one invocation-scoped immutable deny input and is deliberately not inferred
// from either the package or its Manifest. Success is only an observation; it
// is not a reusable admission proof.
type SourceCandidateInput struct {
	ArtifactRoot           string
	SourceRoot             string
	SourcePolicyID         string
	SourcePolicyCanonical  []byte
	PublisherKeyID         string
	PublisherKeyCanonical  []byte
	SignatureID            string
	SignatureCanonical     []byte
	RevokedPublisherKeyIDs []string
}

type packageVerifyFunc func(context.Context, string, uint64) (Report, error)

type sourceFinalVerifyFunc func(
	context.Context,
	string,
	string,
	uint64,
	uint64,
) error

// VerifySourceCandidateDirectory applies one explicit LOCAL_DIRECTORY Source
// Policy to one unpacked artifact. It constructs no network client or
// transport and performs no Store, Secret, Registry, Host, install,
// activation, Binding, or execution operation. An Operator-provided mapped or
// mounted filesystem remains outside this filesystem-only contract.
func VerifySourceCandidateDirectory(
	ctx context.Context,
	input SourceCandidateInput,
) (Report, error) {
	return verifySourceCandidateDirectory(
		ctx,
		input,
		VerifyDirectoryWithPackageLimit,
		verifySourceArtifactWithPackageLimit,
	)
}

func verifySourceCandidateDirectory(
	ctx context.Context,
	input SourceCandidateInput,
	verifyPackage packageVerifyFunc,
	finalVerify sourceFinalVerifyFunc,
) (Report, error) {
	if ctx == nil {
		return Report{}, errors.New("module conformance: context is nil")
	}
	if verifyPackage == nil || finalVerify == nil {
		return Report{}, errors.New(
			"module conformance: source verification dependency is nil",
		)
	}
	if err := ctx.Err(); err != nil {
		return Report{}, sourceCancelled(err)
	}

	policy, err := moduleapi.RestoreModuleSourcePolicyV1(
		input.SourcePolicyCanonical,
		input.SourcePolicyID,
	)
	if err != nil || policy.Kind != moduleapi.ModuleSourceKindLocalDirectoryV1 ||
		policy.Network != moduleapi.ModuleSourceNetworkDenyV1 {
		return Report{}, verificationFailure(
			FailureSourceInput,
			"local source policy validation failed",
			err,
		)
	}
	revoked, err := validateRevokedPublisherKeys(input.RevokedPublisherKeyIDs)
	if err != nil {
		return Report{}, verificationFailure(
			FailureSourceInput,
			"publisher revocation input is invalid",
			err,
		)
	}

	var publisher moduleapi.ModulePublisherKeyV1
	var signature moduleapi.ModuleSignatureV1
	if policy.SignatureRequired {
		if len(input.PublisherKeyCanonical) == 0 ||
			len(input.SignatureCanonical) == 0 ||
			!moduleapi.ValidSHA256(input.PublisherKeyID) ||
			!moduleapi.ValidSHA256(input.SignatureID) {
			return Report{}, verificationFailure(
				FailureSignatureNeeded,
				"source policy requires a detached signature",
				nil,
			)
		}
		if input.PublisherKeyID != policy.PublisherKeyID {
			return Report{}, verificationFailure(
				FailureSignatureInvalid,
				"publisher key does not satisfy source policy",
				nil,
			)
		}
		if _, isRevoked := revoked[input.PublisherKeyID]; isRevoked {
			return Report{}, verificationFailure(
				FailurePublisherRevoked,
				"publisher key is denied for this candidate observation",
				nil,
			)
		}
		publisher, err = moduleapi.RestoreModulePublisherKeyV1(
			input.PublisherKeyCanonical,
			input.PublisherKeyID,
		)
		if err != nil {
			return Report{}, verificationFailure(
				FailureSignatureInvalid,
				"publisher key does not satisfy source policy",
				err,
			)
		}
		signature, err = moduleapi.RestoreModuleSignatureV1(
			input.SignatureCanonical,
			input.SignatureID,
		)
		if err != nil || signature.PublisherKeyID != input.PublisherKeyID {
			return Report{}, verificationFailure(
				FailureSignatureInvalid,
				"detached signature does not satisfy source policy",
				err,
			)
		}
	} else if input.PublisherKeyID != "" || input.SignatureID != "" ||
		len(input.PublisherKeyCanonical) != 0 ||
		len(input.SignatureCanonical) != 0 || len(revoked) != 0 {
		return Report{}, verificationFailure(
			FailureSourceInput,
			"unsigned source policy forbids signature-only inputs",
			nil,
		)
	}

	source, err := resolveStableSourceDirectory(input.SourceRoot, "source root")
	if err != nil {
		return Report{}, verificationFailure(
			FailureSourceInput,
			"local source root validation failed",
			err,
		)
	}
	artifact, err := resolveStableSourceDirectory(input.ArtifactRoot, "artifact root")
	if err != nil || !sourcePathContains(source.path, artifact.path) {
		return Report{}, verificationFailure(
			FailureSourceInput,
			"artifact is not inside the local source root",
			err,
		)
	}
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte(filepath.ToSlash(source.path)),
	)
	if err != nil {
		return Report{}, verificationFailure(
			FailureSourceInput,
			"local source origin normalization failed",
			err,
		)
	}
	if originDigest != policy.OriginDigest {
		return Report{}, verificationFailure(
			FailureSourceDenied,
			"local source origin does not match source policy",
			nil,
		)
	}

	packageReport, err := verifyPackage(
		ctx,
		artifact.path,
		policy.MaxPackageBytes,
	)
	if err != nil {
		return Report{}, err
	}
	if err := moduleapi.ValidateModuleSourceCandidateV1(
		policy,
		moduleapi.Ref{
			ID:      packageReport.Module.ID,
			Version: packageReport.Module.ExactVersion,
		},
		packageReport.ArtifactSizeBytes,
		policy.SignatureRequired,
	); err != nil {
		return Report{}, verificationFailure(
			FailureSourceDenied,
			"candidate is outside source policy",
			err,
		)
	}
	if policy.SignatureRequired {
		if signature.ArtifactDigest != packageReport.ArtifactDigest {
			return Report{}, verificationFailure(
				FailureSignatureInvalid,
				"detached signature artifact identity differs",
				nil,
			)
		}
		if err := moduleapi.VerifyModuleSignatureV1(signature, publisher); err != nil {
			return Report{}, verificationFailure(
				FailureSignatureInvalid,
				"detached signature verification failed",
				err,
			)
		}
	}

	if err := finalVerify(
		ctx,
		artifact.path,
		packageReport.ArtifactDigest,
		packageReport.ArtifactSizeBytes,
		policy.MaxPackageBytes,
	); err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return Report{}, sourceCancelled(err)
		}
		return Report{}, verificationFailure(
			FailureSourceDrift,
			"source candidate changed during preflight",
			err,
		)
	}
	if err := source.verifyCurrent(); err != nil {
		return Report{}, verificationFailure(
			FailureSourceDrift,
			"local source root changed during preflight",
			err,
		)
	}
	if err := artifact.verifyCurrent(); err != nil {
		return Report{}, verificationFailure(
			FailureSourceDrift,
			"artifact root changed during preflight",
			err,
		)
	}
	if err := ctx.Err(); err != nil {
		return Report{}, sourceCancelled(err)
	}

	return cloneReport(packageReport), nil
}

func verifySourceArtifactWithPackageLimit(
	ctx context.Context,
	root string,
	digest string,
	size uint64,
	maxPackageBytes uint64,
) error {
	limits, err := artifactLimitsForPackage(maxPackageBytes)
	if err != nil {
		return err
	}
	return moduleapi.VerifyArtifactDirectoryDigestAndSizeWithLimitsContext(
		ctx,
		root,
		moduleapi.ArtifactMetadataPaths{},
		digest,
		size,
		limits,
	)
}

func validateRevokedPublisherKeys(input []string) (map[string]struct{}, error) {
	if len(input) > int(moduleapi.MaxModuleDiscoveryCandidatesV1) {
		return nil, errors.New("too many revoked publisher key IDs")
	}
	revoked := make(map[string]struct{}, len(input))
	for _, keyID := range append([]string(nil), input...) {
		if !moduleapi.ValidSHA256(keyID) {
			return nil, errors.New("revoked publisher key ID must be lowercase SHA-256")
		}
		if _, duplicate := revoked[keyID]; duplicate {
			return nil, errors.New("revoked publisher key IDs must be unique")
		}
		revoked[keyID] = struct{}{}
	}
	return revoked, nil
}

type stableSourceDirectory struct {
	path string
	info os.FileInfo
}

func resolveStableSourceDirectory(input, label string) (stableSourceDirectory, error) {
	if strings.TrimSpace(input) == "" || input != strings.TrimSpace(input) ||
		len([]byte(input)) > moduleapi.MaxModuleSourceOriginBytesV1 ||
		!utf8.ValidString(input) || input != moduleapi.CanonicalText(input) ||
		strings.ContainsAny(input, "?#") || !filepath.IsAbs(input) {
		return stableSourceDirectory{}, errors.New(
			label + " is required as a canonical absolute filesystem path",
		)
	}
	if runtime.GOOS == "windows" && unsafeWindowsSourceNamespace(input) {
		return stableSourceDirectory{}, errors.New(
			label + " must not use a UNC or device namespace",
		)
	}
	absolute, err := filepath.Abs(input)
	if err != nil {
		return stableSourceDirectory{}, err
	}
	absolute = filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		if unsafeWindowsSourceNamespace(absolute) {
			return stableSourceDirectory{}, errors.New(
				label + " must not use a UNC or device namespace",
			)
		}
		volume := filepath.VolumeName(absolute)
		if strings.Contains(strings.TrimPrefix(absolute, volume), ":") {
			return stableSourceDirectory{}, errors.New(label + " must not use an alternate data stream")
		}
	}
	before, err := os.Lstat(absolute)
	if err != nil {
		return stableSourceDirectory{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 || sourcePathIsReparsePoint(before) ||
		!before.IsDir() {
		return stableSourceDirectory{}, errors.New(label + " must be a non-symlink directory")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !sameSourcePath(absolute, resolved) {
		return stableSourceDirectory{}, errors.New(label + " must not traverse a symlink or reparse point")
	}
	opened, err := os.Stat(resolved)
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		return stableSourceDirectory{}, errors.New(label + " changed while it was inspected")
	}
	return stableSourceDirectory{path: resolved, info: opened}, nil
}

func unsafeWindowsSourceNamespace(input string) bool {
	normalized := strings.ReplaceAll(strings.TrimSpace(input), "/", `\`)
	lower := strings.ToLower(normalized)
	return strings.HasPrefix(normalized, `\\`) ||
		strings.HasPrefix(lower, `\??\`) ||
		strings.HasPrefix(lower, `\\?\`) ||
		strings.HasPrefix(lower, `\\.\`) ||
		strings.HasPrefix(filepath.VolumeName(normalized), `\\`)
}

func (directory stableSourceDirectory) verifyCurrent() error {
	current, err := os.Lstat(directory.path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 ||
		sourcePathIsReparsePoint(current) || !current.IsDir() ||
		!os.SameFile(directory.info, current) {
		return errors.New("directory identity changed")
	}
	resolved, err := filepath.EvalSymlinks(directory.path)
	if err != nil || !sameSourcePath(directory.path, resolved) {
		return errors.New("directory path changed")
	}
	return nil
}

func sameSourcePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func sourcePathContains(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		return false
	}
	return relative == "." ||
		(relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func sourceCancelled(err error) error {
	return verificationFailure(
		FailureCancelled,
		"source verification was cancelled",
		err,
	)
}

func cloneReport(report Report) Report {
	if report.Provides != nil {
		cloned := make([]PortRef, len(report.Provides))
		copy(cloned, report.Provides)
		report.Provides = cloned
	}
	if report.Requires != nil {
		cloned := make([]PortRef, len(report.Requires))
		copy(cloned, report.Requires)
		report.Requires = cloned
	}
	if report.RequestedPermissions != nil {
		cloned := make([]string, len(report.RequestedPermissions))
		copy(cloned, report.RequestedPermissions)
		report.RequestedPermissions = cloned
	}
	return report
}
