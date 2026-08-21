package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const moduleVerifyCanonicalFileMaxBytes = moduleapi.MaxModuleSupplyWireBytesV1

type moduleVerifySourceFlagValues struct {
	sourceRoot           *string
	sourcePolicy         *string
	sourcePolicyID       *string
	publisherKey         *string
	publisherKeyID       *string
	signature            *string
	signatureID          *string
	revokedPublisherKeys *repeatedStringFlag
}

func bindModuleVerifySourceFlags(
	flags *flag.FlagSet,
) moduleVerifySourceFlagValues {
	values := moduleVerifySourceFlagValues{
		revokedPublisherKeys: &repeatedStringFlag{},
		sourceRoot: flags.String(
			"source-root",
			"",
			"absolute local source root governed by Source Policy",
		),
		sourcePolicy: flags.String(
			"source-policy",
			"",
			"exact canonical Source Policy file",
		),
		sourcePolicyID: flags.String(
			"source-policy-id",
			"",
			"expected exact Source Policy content ID",
		),
		publisherKey: flags.String(
			"publisher-key",
			"",
			"exact canonical Publisher Key file",
		),
		publisherKeyID: flags.String(
			"publisher-key-id",
			"",
			"expected exact Publisher Key content ID",
		),
		signature: flags.String(
			"signature",
			"",
			"exact canonical detached Signature file",
		),
		signatureID: flags.String(
			"signature-id",
			"",
			"expected exact detached Signature content ID",
		),
	}
	flags.Var(
		values.revokedPublisherKeys,
		"revoked-publisher-key-id",
		"deny one exact Publisher Key ID for this preflight; repeatable",
	)
	return values
}

func moduleVerifySourceMode(flags *flag.FlagSet) bool {
	selected := false
	flags.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "source-root",
			"source-policy",
			"source-policy-id",
			"publisher-key",
			"publisher-key-id",
			"signature",
			"signature-id",
			"revoked-publisher-key-id":
			selected = true
		}
	})
	return selected
}

func prepareModuleVerifySourceInput(
	ctx context.Context,
	artifactRoot string,
	values moduleVerifySourceFlagValues,
	readCanonical moduleVerifyCanonicalReadFunc,
) (moduleconformance.SourceCandidateInput, moduleconformance.FailureCode) {
	if ctx == nil || readCanonical == nil {
		return moduleconformance.SourceCandidateInput{},
			moduleconformance.FailureInternal
	}
	if !exactNonEmptyFlag(*values.sourceRoot) ||
		!exactNonEmptyFlag(*values.sourcePolicy) ||
		!moduleapi.ValidSHA256(*values.sourcePolicyID) {
		return moduleconformance.SourceCandidateInput{},
			moduleconformance.FailureSourceInput
	}

	signatureFields := []string{
		*values.publisherKey,
		*values.publisherKeyID,
		*values.signature,
		*values.signatureID,
	}
	present := 0
	for _, value := range signatureFields {
		if strings.TrimSpace(value) != "" {
			if !exactNonEmptyFlag(value) {
				return moduleconformance.SourceCandidateInput{},
					moduleconformance.FailureSourceInput
			}
			present++
		}
	}
	if present != 0 && present != len(signatureFields) {
		return moduleconformance.SourceCandidateInput{},
			moduleconformance.FailureSourceInput
	}
	if present == len(signatureFields) &&
		(!moduleapi.ValidSHA256(*values.publisherKeyID) ||
			!moduleapi.ValidSHA256(*values.signatureID)) {
		return moduleconformance.SourceCandidateInput{},
			moduleconformance.FailureSourceInput
	}

	revoked, ok := canonicalRevokedPublisherKeys(
		*values.revokedPublisherKeys,
	)
	if !ok {
		return moduleconformance.SourceCandidateInput{},
			moduleconformance.FailureSourceInput
	}

	policyCanonical, err := readCanonical(ctx, *values.sourcePolicy)
	if err != nil {
		return moduleconformance.SourceCandidateInput{},
			moduleVerifyCanonicalReadFailure(
				err,
				moduleconformance.FailureSourceInput,
			)
	}
	policy, err := moduleapi.RestoreModuleSourcePolicyV1(
		policyCanonical,
		*values.sourcePolicyID,
	)
	if err != nil || policy.Kind != moduleapi.ModuleSourceKindLocalDirectoryV1 ||
		policy.Network != moduleapi.ModuleSourceNetworkDenyV1 {
		return moduleconformance.SourceCandidateInput{},
			moduleconformance.FailureSourceInput
	}

	input := moduleconformance.SourceCandidateInput{
		ArtifactRoot:           artifactRoot,
		SourceRoot:             *values.sourceRoot,
		SourcePolicyID:         *values.sourcePolicyID,
		SourcePolicyCanonical:  bytes.Clone(policyCanonical),
		RevokedPublisherKeyIDs: append([]string(nil), revoked...),
	}
	if !policy.SignatureRequired {
		if present != 0 || len(revoked) != 0 {
			return moduleconformance.SourceCandidateInput{},
				moduleconformance.FailureSourceInput
		}
		return input, ""
	}
	if present == 0 {
		return moduleconformance.SourceCandidateInput{},
			moduleconformance.FailureSignatureNeeded
	}
	if *values.publisherKeyID != policy.PublisherKeyID {
		return moduleconformance.SourceCandidateInput{},
			moduleconformance.FailureSignatureInvalid
	}
	for _, keyID := range revoked {
		if keyID == policy.PublisherKeyID {
			return moduleconformance.SourceCandidateInput{},
				moduleconformance.FailurePublisherRevoked
		}
	}

	publisherCanonical, err := readCanonical(ctx, *values.publisherKey)
	if err != nil {
		return moduleconformance.SourceCandidateInput{},
			moduleVerifyCanonicalReadFailure(
				err,
				moduleconformance.FailureSignatureInvalid,
			)
	}
	signatureCanonical, err := readCanonical(ctx, *values.signature)
	if err != nil {
		return moduleconformance.SourceCandidateInput{},
			moduleVerifyCanonicalReadFailure(
				err,
				moduleconformance.FailureSignatureInvalid,
			)
	}
	input.PublisherKeyID = *values.publisherKeyID
	input.PublisherKeyCanonical = bytes.Clone(publisherCanonical)
	input.SignatureID = *values.signatureID
	input.SignatureCanonical = bytes.Clone(signatureCanonical)
	return input, ""
}

func moduleVerifyCanonicalReadFailure(
	err error,
	fallback moduleconformance.FailureCode,
) moduleconformance.FailureCode {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return moduleconformance.FailureCancelled
	}
	return fallback
}

func exactNonEmptyFlag(value string) bool {
	return value != "" && value == strings.TrimSpace(value)
}

func canonicalRevokedPublisherKeys(
	input []string,
) ([]string, bool) {
	if len(input) > int(moduleapi.MaxModuleDiscoveryCandidatesV1) {
		return nil, false
	}
	result := append([]string(nil), input...)
	seen := make(map[string]struct{}, len(result))
	for _, value := range result {
		if !moduleapi.ValidSHA256(value) {
			return nil, false
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, false
		}
		seen[value] = struct{}{}
	}
	return result, true
}

func readModuleVerifyCanonicalFile(
	ctx context.Context,
	input string,
) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("module-verify canonical read context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !exactNonEmptyFlag(input) || strings.ContainsAny(input, "?#") {
		return nil, errors.New("canonical file path is invalid")
	}
	if runtime.GOOS == "windows" && unsafeModuleVerifyWindowsPath(input) {
		return nil, errors.New("canonical file path uses a forbidden namespace")
	}
	absolute, err := filepath.Abs(input)
	if err != nil {
		return nil, errors.New("canonical file path cannot be resolved")
	}
	absolute = filepath.Clean(absolute)
	if runtime.GOOS == "windows" && unsafeModuleVerifyWindowsPath(absolute) {
		return nil, errors.New("canonical file path uses a forbidden namespace")
	}
	before, err := os.Lstat(absolute)
	if err != nil || before.Mode()&os.ModeSymlink != 0 ||
		moduleVerifyPathIsReparsePoint(before) ||
		!before.Mode().IsRegular() || before.Size() <= 0 ||
		before.Size() > int64(moduleVerifyCanonicalFileMaxBytes) {
		return nil, errors.New("canonical file must be a bounded ordinary file")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !sameModuleVerifyPath(absolute, resolved) {
		return nil, errors.New("canonical file path traverses a symlink")
	}

	file, err := os.Open(absolute)
	if err != nil {
		return nil, errors.New("canonical file cannot be opened")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() ||
		!os.SameFile(before, opened) {
		return nil, errors.New("canonical file changed before it was opened")
	}
	first, err := readBoundedModuleVerifyCanonical(ctx, file)
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, errors.New("canonical file cannot be rechecked")
	}
	second, err := readBoundedModuleVerifyCanonical(ctx, file)
	if err != nil || !bytes.Equal(first, second) {
		return nil, errors.New("canonical file changed while it was read")
	}
	afterHandle, err := file.Stat()
	if err != nil || !os.SameFile(opened, afterHandle) ||
		afterHandle.Size() != opened.Size() ||
		!afterHandle.ModTime().Equal(opened.ModTime()) {
		return nil, errors.New("canonical file changed while it was read")
	}
	afterPath, err := os.Lstat(absolute)
	if err != nil || afterPath.Mode()&os.ModeSymlink != 0 ||
		moduleVerifyPathIsReparsePoint(afterPath) ||
		!afterPath.Mode().IsRegular() || !os.SameFile(opened, afterPath) ||
		afterPath.Size() != opened.Size() ||
		!afterPath.ModTime().Equal(opened.ModTime()) {
		return nil, errors.New("canonical file path changed while it was read")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return bytes.Clone(first), nil
}

func readBoundedModuleVerifyCanonical(
	ctx context.Context,
	reader io.Reader,
) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(
		reader,
		int64(moduleVerifyCanonicalFileMaxBytes)+1,
	))
	if err != nil {
		return nil, errors.New("canonical file read failed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(content) == 0 || len(content) > moduleVerifyCanonicalFileMaxBytes {
		return nil, errors.New("canonical file size is outside the v1 ceiling")
	}
	return content, nil
}

func sameModuleVerifyPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func unsafeModuleVerifyWindowsPath(input string) bool {
	normalized := strings.ReplaceAll(strings.TrimSpace(input), "/", `\`)
	lower := strings.ToLower(normalized)
	volume := filepath.VolumeName(normalized)
	return strings.HasPrefix(normalized, `\\`) ||
		strings.HasPrefix(lower, `\??\`) ||
		strings.HasPrefix(lower, `\\?\`) ||
		strings.HasPrefix(lower, `\\.\`) ||
		strings.HasPrefix(volume, `\\`) ||
		strings.Contains(strings.TrimPrefix(normalized, volume), ":")
}
