package controlweb

import (
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
)

// ErrEmbeddedAssetIntegrity reports that a committed asset does not match the
// immutable metadata compiled into the binary.
var ErrEmbeddedAssetIntegrity = errors.New("controlweb: embedded asset integrity mismatch")

// Asset is an immutable snapshot of one exact embedded control-web file.
// Bytes is a fresh copy owned by the caller.
type Asset struct {
	Path      string
	MediaType string
	SHA256    string
	Bytes     []byte
}

//go:embed dist/index.html dist/assets/app.css dist/assets/app.js dist/assets/react.js dist/assets/tanstack-query.js
var embeddedAssets embed.FS

type assetMetadata struct {
	mediaType string
	sha256    string
	size      int
}

// AssetPaths returns the complete, stable, Ordinal-sorted embedded asset set.
func AssetPaths() []string {
	return []string{
		"assets/app.css",
		"assets/app.js",
		"assets/react.js",
		"assets/tanstack-query.js",
		"index.html",
	}
}

// ResolveAsset resolves only a canonical path returned by AssetPaths. It does
// not clean, redirect, or interpret URL paths, query strings, or traversal.
func ResolveAsset(path string) (Asset, bool, error) {
	metadata, ok := metadataFor(path)
	if !ok {
		return Asset{}, false, nil
	}

	payload, err := embeddedAssets.ReadFile("dist/" + path)
	if err != nil {
		return Asset{}, true, fmt.Errorf("%w: %s: %v", ErrEmbeddedAssetIntegrity, path, err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(payload))
	if len(payload) != metadata.size || digest != metadata.sha256 {
		return Asset{}, true, fmt.Errorf("%w: %s", ErrEmbeddedAssetIntegrity, path)
	}

	return Asset{
		Path:      path,
		MediaType: metadata.mediaType,
		SHA256:    metadata.sha256,
		Bytes:     append([]byte(nil), payload...),
	}, true, nil
}

func metadataFor(path string) (assetMetadata, bool) {
	switch path {
	case "assets/app.css":
		return assetMetadata{"text/css; charset=utf-8", "d7b367e63bb3f80f493ce912dc3cf2f4383c7dcb639b1f929f78f60c7ef039e5", 18401}, true
	case "assets/app.js":
		return assetMetadata{"text/javascript; charset=utf-8", "84a84a8fc33410287a1f56738e4037d77105e99c86c43762fc162866c24ea811", 352905}, true
	case "assets/react.js":
		return assetMetadata{"text/javascript; charset=utf-8", "87b823d95ddc471c8ea19c7891961eadf87cbc422e600dae77680da28dc393f9", 554455}, true
	case "assets/tanstack-query.js":
		return assetMetadata{"text/javascript; charset=utf-8", "ec1bbca1b6df03fa9ba364ac3f70097def64f3e049c30e550a81d6c1e0cf2e9c", 79776}, true
	case "index.html":
		return assetMetadata{"text/html; charset=utf-8", "c153aca88b96eb5f13ddeab983ce2e7043230d1258473f745489664acc56b6d3", 497}, true
	default:
		return assetMetadata{}, false
	}
}
