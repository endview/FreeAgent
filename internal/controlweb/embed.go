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
		return assetMetadata{"text/css; charset=utf-8", "848373990f012036cc54eefe263f7e9380b584675d4db76e9969f9dffd6af73a", 17300}, true
	case "assets/app.js":
		return assetMetadata{"text/javascript; charset=utf-8", "eeb19cc9991fad02ea0dfc36cb3a95af5d5fe9790abe5a2bd2f1838aca2f1084", 228790}, true
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
