package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxChannelCredentialFileBytes = 4098 // 4096 visible bytes plus optional CRLF

// fileChannelSecretResolver retains only an absolute operator-selected path.
// Secret bytes are read for one adapter operation, returned as an owned slice,
// and cleared by the adapter after use.
type fileChannelSecretResolver struct {
	path string
}

func newFileChannelSecretResolver(path string) (*fileChannelSecretResolver, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("channel secret file path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve Channel secret file: %w", err)
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect Channel secret file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxChannelCredentialFileBytes {
		return nil, errors.New("Channel secret file must be a small ordinary file")
	}
	return &fileChannelSecretResolver{path: absolute}, nil
}

func (resolver *fileChannelSecretResolver) ResolveSecret(
	ctx context.Context,
	_ string,
) ([]byte, error) {
	if resolver == nil || resolver.path == "" || ctx == nil {
		return nil, errors.New("Channel secret resolver is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	before, err := os.Lstat(resolver.path)
	if err != nil || !before.Mode().IsRegular() ||
		before.Mode()&os.ModeSymlink != 0 || before.Size() <= 0 ||
		before.Size() > maxChannelCredentialFileBytes {
		return nil, errors.New("Channel secret file is unavailable")
	}
	file, err := os.Open(resolver.path)
	if err != nil {
		return nil, errors.New("Channel secret file is unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, errors.New("Channel secret file changed during resolution")
	}
	body, err := io.ReadAll(io.LimitReader(file, maxChannelCredentialFileBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxChannelCredentialFileBytes {
		clear(body)
		return nil, errors.New("Channel secret file cannot be read safely")
	}
	if err := ctx.Err(); err != nil {
		clear(body)
		return nil, err
	}
	body = bytes.TrimSuffix(body, []byte{'\n'})
	body = bytes.TrimSuffix(body, []byte{'\r'})
	if len(body) == 0 || len(body) > 4096 {
		clear(body)
		return nil, errors.New("Channel secret file contains an invalid credential")
	}
	return body, nil
}
