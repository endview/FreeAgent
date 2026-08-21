package controlweb

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"testing"
)

func TestEmbeddedAssetsAreExactAndImmutable(t *testing.T) {
	paths := AssetPaths()
	if !slices.IsSorted(paths) {
		t.Fatalf("AssetPaths is not sorted: %v", paths)
	}
	if len(paths) != 5 {
		t.Fatalf("AssetPaths length = %d, want 5", len(paths))
	}

	for _, path := range paths {
		asset, found, err := ResolveAsset(path)
		if err != nil {
			t.Fatalf("ResolveAsset(%q): %v", path, err)
		}
		if !found {
			t.Fatalf("ResolveAsset(%q) was not found", path)
		}
		if asset.Path != path || asset.MediaType == "" || len(asset.Bytes) == 0 {
			t.Fatalf("ResolveAsset(%q) returned incomplete metadata", path)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(asset.Bytes)); got != asset.SHA256 {
			t.Fatalf("ResolveAsset(%q) digest = %s, want %s", path, got, asset.SHA256)
		}

		asset.Bytes[0] ^= 0xff
		again, found, err := ResolveAsset(path)
		if err != nil || !found {
			t.Fatalf("second ResolveAsset(%q) = found %t, err %v", path, found, err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(again.Bytes)); got != again.SHA256 {
			t.Fatalf("ResolveAsset(%q) exposed mutable embedded bytes", path)
		}
	}
}

func TestResolveAssetRejectsNonCanonicalPaths(t *testing.T) {
	for _, path := range []string{
		"",
		"/index.html",
		"dist/index.html",
		"assets/../index.html",
		"assets\\app.js",
		"assets/app.js?query=1",
		"assets/missing.js",
	} {
		asset, found, err := ResolveAsset(path)
		if err != nil || found || asset.Path != "" || asset.MediaType != "" || asset.SHA256 != "" || asset.Bytes != nil {
			t.Fatalf("ResolveAsset(%q) = (%+v, %t, %v), want zero, false, nil", path, asset, found, err)
		}
	}
}
