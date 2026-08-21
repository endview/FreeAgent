package modulesource

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/sdk/moduleapi"
)

const localIndexFilename = "index.json"

type stableLocalDirectory struct {
	path string
	info os.FileInfo
}

type stableLocalFile struct {
	path string
	info os.FileInfo
}

func (provider *Provider) observeLocalIndex(
	ctx context.Context,
	policy moduleapi.ModuleSourcePolicyV1,
	rootInput string,
) ([]byte, error) {
	root, err := resolveStableLocalDirectory(rootInput)
	if err != nil {
		return nil, observationFailure(FailureSourceDenied, err)
	}
	origin := filepath.ToSlash(root.path)
	digest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte(origin),
	)
	if err != nil || digest != policy.OriginDigest {
		return nil, observationFailure(
			FailureSourceDenied,
			errors.New("local source origin differs from policy"),
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, cancelledObservation(err)
	}

	indexPath := filepath.Join(root.path, localIndexFilename)
	first, firstFile, err := readStableLocalFile(ctx, indexPath, policy.MaxIndexBytes)
	if err != nil {
		return nil, err
	}
	if err := root.verifyCurrent(); err != nil {
		return nil, observationFailure(FailureSourceDrift, err)
	}
	second, secondFile, err := readStableLocalFile(ctx, indexPath, policy.MaxIndexBytes)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(firstFile.info, secondFile.info) ||
		!bytes.Equal(first, second) {
		return nil, observationFailure(
			FailureSourceDrift,
			errors.New("local index changed between reads"),
		)
	}
	if err := firstFile.verifyCurrent(); err != nil {
		return nil, observationFailure(FailureSourceDrift, err)
	}
	if err := root.verifyCurrent(); err != nil {
		return nil, observationFailure(FailureSourceDrift, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, cancelledObservation(err)
	}
	return bytes.Clone(second), nil
}

func resolveStableLocalDirectory(input string) (stableLocalDirectory, error) {
	if input == "" || input != strings.TrimSpace(input) ||
		len(input) > moduleapi.MaxModuleSourceOriginBytesV1 ||
		!utf8.ValidString(input) || input != moduleapi.CanonicalText(input) ||
		strings.ContainsAny(input, "?#") || !filepath.IsAbs(input) {
		return stableLocalDirectory{}, errors.New(
			"local source must be a canonical absolute path",
		)
	}
	if runtime.GOOS == "windows" && unsafeWindowsLocalNamespace(input) {
		return stableLocalDirectory{}, errors.New(
			"local source must not use a UNC or device namespace",
		)
	}
	absolute, err := filepath.Abs(input)
	if err != nil {
		return stableLocalDirectory{}, err
	}
	absolute = filepath.Clean(absolute)
	if absolute != input && !(runtime.GOOS == "windows" && strings.EqualFold(absolute, input)) {
		return stableLocalDirectory{}, errors.New("local source path is not clean")
	}
	if runtime.GOOS == "windows" {
		if unsafeWindowsLocalNamespace(absolute) {
			return stableLocalDirectory{}, errors.New(
				"local source must not use a UNC or device namespace",
			)
		}
		volume := filepath.VolumeName(absolute)
		if strings.Contains(strings.TrimPrefix(absolute, volume), ":") {
			return stableLocalDirectory{}, errors.New(
				"local source must not use an alternate data stream",
			)
		}
	}
	before, err := os.Lstat(absolute)
	if err != nil {
		return stableLocalDirectory{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 || localPathIsReparsePoint(before) ||
		!before.IsDir() {
		return stableLocalDirectory{}, errors.New(
			"local source must be a non-symlink directory",
		)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil || !sameLocalPath(absolute, resolved) {
		return stableLocalDirectory{}, errors.New(
			"local source must not traverse a symlink or reparse point",
		)
	}
	after, err := os.Stat(resolved)
	if err != nil || !after.IsDir() || !os.SameFile(before, after) {
		return stableLocalDirectory{}, errors.New(
			"local source changed during inspection",
		)
	}
	return stableLocalDirectory{path: resolved, info: after}, nil
}

func readStableLocalFile(
	ctx context.Context,
	filename string,
	maximum uint64,
) ([]byte, stableLocalFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, stableLocalFile{}, cancelledObservation(err)
	}
	before, err := os.Lstat(filename)
	if err != nil {
		return nil, stableLocalFile{}, observationFailure(FailureSourceUnavailable, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || localPathIsReparsePoint(before) ||
		!before.Mode().IsRegular() {
		return nil, stableLocalFile{}, observationFailure(
			FailureSourceDenied,
			errors.New("local index must be a non-symlink regular file"),
		)
	}
	if before.Size() < 0 || uint64(before.Size()) > maximum {
		return nil, stableLocalFile{}, observationFailure(
			FailureSourceDenied,
			errors.New("local index exceeds policy byte limit"),
		)
	}
	resolved, err := filepath.EvalSymlinks(filename)
	if err != nil || !sameLocalPath(filename, resolved) {
		return nil, stableLocalFile{}, observationFailure(
			FailureSourceDenied,
			errors.New("local index must not traverse a symlink or reparse point"),
		)
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, stableLocalFile{}, observationFailure(FailureSourceUnavailable, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, stableLocalFile{}, observationFailure(
			FailureSourceDrift,
			errors.New("local index changed while opening"),
		)
	}
	reader := io.LimitReader(file, int64(maximum)+1)
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, stableLocalFile{}, observationFailure(FailureSourceUnavailable, err)
	}
	if uint64(len(content)) > maximum {
		return nil, stableLocalFile{}, observationFailure(
			FailureSourceDenied,
			errors.New("local index exceeds policy byte limit"),
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, stableLocalFile{}, cancelledObservation(err)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) ||
		after.Size() != int64(len(content)) ||
		opened.Size() != after.Size() ||
		!opened.ModTime().Equal(after.ModTime()) {
		return nil, stableLocalFile{}, observationFailure(
			FailureSourceDrift,
			errors.New("local index changed while reading"),
		)
	}
	return bytes.Clone(content), stableLocalFile{path: filename, info: after}, nil
}

func (directory stableLocalDirectory) verifyCurrent() error {
	current, err := os.Lstat(directory.path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 ||
		localPathIsReparsePoint(current) || !current.IsDir() ||
		!os.SameFile(directory.info, current) {
		return errors.New("local source identity changed")
	}
	resolved, err := filepath.EvalSymlinks(directory.path)
	if err != nil || !sameLocalPath(directory.path, resolved) {
		return errors.New("local source path changed")
	}
	return nil
}

func (file stableLocalFile) verifyCurrent() error {
	current, err := os.Lstat(file.path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 ||
		localPathIsReparsePoint(current) || !current.Mode().IsRegular() ||
		!os.SameFile(file.info, current) || current.Size() != file.info.Size() ||
		!current.ModTime().Equal(file.info.ModTime()) {
		return errors.New("local index identity changed")
	}
	resolved, err := filepath.EvalSymlinks(file.path)
	if err != nil || !sameLocalPath(file.path, resolved) {
		return errors.New("local index path changed")
	}
	return nil
}

func unsafeWindowsLocalNamespace(input string) bool {
	normalized := strings.ReplaceAll(strings.TrimSpace(input), "/", `\`)
	lower := strings.ToLower(normalized)
	return strings.HasPrefix(normalized, `\\`) ||
		strings.HasPrefix(lower, `\??\`) ||
		strings.HasPrefix(lower, `\\?\`) ||
		strings.HasPrefix(lower, `\\.\`) ||
		strings.HasPrefix(filepath.VolumeName(normalized), `\\`)
}

func sameLocalPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
