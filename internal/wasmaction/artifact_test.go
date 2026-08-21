package wasmaction

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestLoadArtifactFromDirectoryCapturesExactVerifiedBytes(t *testing.T) {
	descriptor, descriptorCanonical := testDescriptor(t)
	wasm := testWASMModule(testWASMOptions{})
	manifestCanonical := testManifestCanonical(t)
	digest, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		[]moduleapi.ArtifactFile{
			{Path: "content/action-wasm.json", Content: descriptorCanonical},
			{Path: descriptor.ModulePath, Content: wasm},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := testProvider(digest)
	root := writeTestArtifact(t, manifestCanonical, descriptorCanonical, wasm)
	coveredSize := uint64(
		len(manifestCanonical) + len(descriptorCanonical) + len(wasm),
	)
	loadedDescriptor, loadedWASM, err := LoadArtifactFromDirectory(
		context.Background(),
		provider,
		root,
		coveredSize,
	)
	if err != nil {
		t.Fatalf("LoadArtifactFromDirectory: %v", err)
	}
	if loadedDescriptor.ModulePath != descriptor.ModulePath ||
		len(loadedDescriptor.Actions) != 1 ||
		!bytes.Equal(loadedWASM, wasm) {
		t.Fatalf("loaded descriptor=%+v Wasm bytes=%d", loadedDescriptor, len(loadedWASM))
	}
	loadedWASM[0] ^= 0xff
	if bytes.Equal(loadedWASM, wasm) {
		t.Fatal("returned Wasm aliases the test artifact bytes")
	}

	if err := os.WriteFile(
		filepath.Join(root, descriptor.ModulePath),
		append(bytes.Clone(wasm), 0x00),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadArtifactFromDirectory(
		context.Background(),
		provider,
		root,
		coveredSize,
	); err == nil {
		t.Fatal("tampered Wasm passed the captured ArtifactDigest closure")
	}
}

func TestLoadArtifactFromDirectoryRejectsExtraAndOversizedFiles(t *testing.T) {
	_, descriptorCanonical := testDescriptor(t)
	wasm := testWASMModule(testWASMOptions{})
	manifestCanonical := testManifestCanonical(t)

	t.Run("extra covered file", func(t *testing.T) {
		root := writeTestArtifact(t, manifestCanonical, descriptorCanonical, wasm)
		extra := []byte(`{"unused":true}`)
		if err := os.WriteFile(
			filepath.Join(root, "content", "extra.json"),
			extra,
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		digest, err := moduleapi.ComputeArtifactDigest(
			manifestCanonical,
			[]moduleapi.ArtifactFile{
				{Path: "content/action-wasm.json", Content: descriptorCanonical},
				{Path: "content/action.wasm", Content: wasm},
				{Path: "content/extra.json", Content: extra},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadArtifactFromDirectory(
			context.Background(),
			testProvider(digest),
			root,
			uint64(len(manifestCanonical)+len(descriptorCanonical)+len(wasm)+len(extra)),
		); err == nil {
			t.Fatal("artifact with an extra covered file passed the exact shape")
		}
	})

	t.Run("oversized Wasm file", func(t *testing.T) {
		oversized := bytes.Repeat([]byte{0}, MaxModuleBytesV1+1)
		root := writeTestArtifact(
			t,
			manifestCanonical,
			descriptorCanonical,
			oversized,
		)
		if _, _, err := LoadArtifactFromDirectory(
			context.Background(),
			testProvider(string(bytes.Repeat([]byte{'a'}, 64))),
			root,
			uint64(len(manifestCanonical)+len(descriptorCanonical)+len(oversized)),
		); err == nil {
			t.Fatal("oversized Wasm file passed the artifact ceiling")
		}
	})
}

func testManifestCanonical(t *testing.T) []byte {
	t.Helper()
	encoded, err := json.Marshal(moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "example.wasm.action",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestWASM,
			Protocol:   moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
			Entrypoint: "content/action-wasm.json",
		},
		Provides: []moduleapi.PortRef{actionProviderPortV1},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func writeTestArtifact(
	t *testing.T,
	manifest []byte,
	descriptor []byte,
	wasm []byte,
) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "content"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		moduleapi.ArtifactManifestPath: manifest,
		"content/action-wasm.json":     descriptor,
		"content/action.wasm":          wasm,
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
