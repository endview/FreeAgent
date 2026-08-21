package currentbackup

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestRestoreManifestPreflightRejectsArtifactEntry257(t *testing.T) {
	payload := manifestPreflightArrayPayload("artifacts", maxArtifactCount+1, `{}`)
	_, err := restoreManifest(payload)
	if !errors.Is(err, ErrInvalidBundle) ||
		!strings.Contains(err.Error(), "manifest JSON preflight") ||
		!strings.Contains(err.Error(), "artifacts exceeds 256 entries") {
		t.Fatalf("restoreManifest(257 artifacts) error = %v", err)
	}
}

func TestRestoreManifestPreflightRejectsCurrentEntry100001(t *testing.T) {
	payload := manifestPreflightArrayPayload("current", maxCurrentCount+1, `{}`)
	_, err := restoreManifest(payload)
	if !errors.Is(err, ErrInvalidBundle) ||
		!strings.Contains(err.Error(), "manifest JSON preflight") ||
		!strings.Contains(err.Error(), "current exceeds 100000 entries") {
		t.Fatalf("restoreManifest(100001 current entries) error = %v", err)
	}
}

func TestRestoreManifestPreflightBoundsUnknownObjectFlood(t *testing.T) {
	payload := manifestPreflightArrayPayload(
		"unknown",
		maxManifestPreflightUnknownTokens/2+1,
		`{}`,
	)
	_, err := restoreManifest(payload)
	if !errors.Is(err, ErrInvalidBundle) ||
		!strings.Contains(err.Error(), "manifest JSON preflight") ||
		!strings.Contains(err.Error(), "unknown top-level value exceeds") {
		t.Fatalf("restoreManifest(unknown object flood) error = %v", err)
	}
}

func TestRestoreManifestPreflightBoundsUnknownDepth(t *testing.T) {
	var payload bytes.Buffer
	payload.WriteString(`{"unknown":`)
	for range maxManifestPreflightDepth {
		payload.WriteByte('[')
	}
	payload.WriteString(`[null]`)
	for range maxManifestPreflightDepth {
		payload.WriteByte(']')
	}
	payload.WriteByte('}')
	_, err := restoreManifest(payload.Bytes())
	if !errors.Is(err, ErrInvalidBundle) ||
		!strings.Contains(err.Error(), "manifest JSON preflight") ||
		!strings.Contains(err.Error(), "JSON nesting exceeds 128 levels") {
		t.Fatalf("restoreManifest(deep unknown value) error = %v", err)
	}
}

func TestRestoreManifestPreflightDoesNotReplaceStrictSemantics(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "small unknown field reaches typed decoder",
			payload: `{"unknown":null}`,
			want:    "decode manifest: json: unknown field",
		},
		{
			name:    "noncanonical JSON reaches canonicalizer",
			payload: `{"format_version": "wrong"}`,
			want:    "manifest must be exact RFC 8785 canonical JSON",
		},
		{
			name:    "unknown duplicate reaches canonicalizer",
			payload: `{"unknown":null,"unknown":null}`,
			want:    "manifest must be exact RFC 8785 canonical JSON",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := restoreManifest([]byte(test.payload))
			if !errors.Is(err, ErrInvalidBundle) ||
				!strings.Contains(err.Error(), test.want) ||
				strings.Contains(err.Error(), "manifest JSON preflight") {
				t.Fatalf("restoreManifest() error = %v", err)
			}
		})
	}
}

func TestRestoreManifestPreflightRejectsDuplicateCountedKey(t *testing.T) {
	_, err := restoreManifest([]byte(`{"artifacts":[],"artifacts":[]}`))
	if !errors.Is(err, ErrInvalidBundle) ||
		!strings.Contains(err.Error(), "manifest JSON preflight") ||
		!strings.Contains(err.Error(), `duplicate top-level key "artifacts"`) {
		t.Fatalf("restoreManifest(duplicate artifacts) error = %v", err)
	}
}

func TestRestoreManifestPreflightPreservesLegalRoundTrip(t *testing.T) {
	input := minimalManifestForPreflightTest()
	frozen, canonical, err := freezeManifest(input)
	if err != nil {
		t.Fatalf("freezeManifest() error = %v", err)
	}
	restored, err := restoreManifest(canonical)
	if err != nil {
		t.Fatalf("restoreManifest() error = %v", err)
	}
	if !reflect.DeepEqual(restored, frozen) {
		t.Fatalf("restoreManifest() = %+v, want %+v", restored, frozen)
	}
}

func TestManifestPreflightGlobalBudgetCoversDeclaredEntryBounds(t *testing.T) {
	currentTokens := encodedJSONTokenCount(t, CurrentPublication{})
	if currentTokens != manifestCurrentPublicationJSONTokens {
		t.Fatalf(
			"CurrentPublication token count = %d, frozen count = %d",
			currentTokens,
			manifestCurrentPublicationJSONTokens,
		)
	}
	artifactTokens := encodedJSONTokenCount(t, Artifact{})
	if artifactTokens != manifestArtifactJSONTokens {
		t.Fatalf(
			"Artifact token count = %d, frozen count = %d",
			artifactTokens,
			manifestArtifactJSONTokens,
		)
	}
	required := maxCurrentCount*currentTokens +
		maxArtifactCount*artifactTokens +
		maxManifestPreflightEnvelopeTokens
	if maxManifestPreflightTokens < required {
		t.Fatalf(
			"global preflight token limit = %d, declared entry bounds require %d",
			maxManifestPreflightTokens,
			required,
		)
	}
}

func TestManifestArtifactByteBudgetsMatchStoreBoundary(t *testing.T) {
	if maxArtifactBytes != moduleapi.DefaultArtifactMaxTotalBytes {
		t.Fatalf(
			"single artifact limit = %d, scanner = %d",
			maxArtifactBytes,
			moduleapi.DefaultArtifactMaxTotalBytes,
		)
	}
	if maxArtifactBytes != int64(moduleapi.MaxModuleSourcePackageBytesV1) {
		t.Fatalf(
			"single artifact limit = %d, Store = %d",
			maxArtifactBytes,
			moduleapi.MaxModuleSourcePackageBytesV1,
		)
	}
	if maxArtifactAggregateBytes != int64(512<<20) {
		t.Fatalf("aggregate artifact limit = %d", maxArtifactAggregateBytes)
	}

	t.Run("single boundary accepted", func(t *testing.T) {
		candidate := minimalManifestForPreflightTest()
		candidate.Artifacts = []Artifact{manifestArtifactForSize('a', maxArtifactBytes)}
		candidate.ArtifactCount = len(candidate.Artifacts)
		if _, _, err := freezeManifest(candidate); err != nil {
			t.Fatalf("freezeManifest(single boundary) error = %v", err)
		}
	})

	t.Run("single boundary plus one rejected", func(t *testing.T) {
		candidate := minimalManifestForPreflightTest()
		candidate.Artifacts = []Artifact{manifestArtifactForSize('a', maxArtifactBytes+1)}
		candidate.ArtifactCount = len(candidate.Artifacts)
		if _, _, err := freezeManifest(candidate); !errors.Is(err, ErrInvalidBundle) {
			t.Fatalf("freezeManifest(single boundary + 1) error = %v", err)
		}
	})

	t.Run("aggregate boundary accepted", func(t *testing.T) {
		candidate := minimalManifestForPreflightTest()
		candidate.Artifacts = []Artifact{
			manifestArtifactForSize('a', maxArtifactBytes),
			manifestArtifactForSize('b', maxArtifactBytes),
		}
		candidate.ArtifactCount = len(candidate.Artifacts)
		if _, _, err := freezeManifest(candidate); err != nil {
			t.Fatalf("freezeManifest(aggregate boundary) error = %v", err)
		}
	})

	t.Run("aggregate boundary plus one rejected", func(t *testing.T) {
		candidate := minimalManifestForPreflightTest()
		candidate.Artifacts = []Artifact{
			manifestArtifactForSize('a', maxArtifactBytes),
			manifestArtifactForSize('b', maxArtifactBytes),
			manifestArtifactForSize('c', 1),
		}
		candidate.ArtifactCount = len(candidate.Artifacts)
		if _, _, err := freezeManifest(candidate); !errors.Is(err, ErrInvalidBundle) {
			t.Fatalf("freezeManifest(aggregate boundary + 1) error = %v", err)
		}
	})
}

func manifestPreflightArrayPayload(key string, count int, element string) []byte {
	var payload bytes.Buffer
	payload.Grow(len(key) + count*(len(element)+1) + 8)
	payload.WriteString(`{"`)
	payload.WriteString(key)
	payload.WriteString(`":[`)
	for index := 0; index < count; index++ {
		if index != 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(element)
	}
	payload.WriteString(`]}`)
	return payload.Bytes()
}

func encodedJSONTokenCount(t *testing.T, value any) int {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T) error = %v", value, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	count := 0
	for {
		if _, err := decoder.Token(); errors.Is(err, io.EOF) {
			return count
		} else if err != nil {
			t.Fatalf("decode JSON tokens for %T: %v", value, err)
		}
		count++
	}
}

func minimalManifestForPreflightTest() Manifest {
	return Manifest{
		FormatVersion: FormatVersionV1,
		CreatedAt:     "2026-08-17T00:00:00Z",
		ToolVersion:   "currentbackup-manifest-preflight-test/v1",
		Database: DatabaseFile{
			Path:      databaseName,
			SHA256:    strings.Repeat("d", 64),
			SizeBytes: 1,
		},
		StoreIdentity: StoreIdentity{
			ApplicationID:     currentstore.ApplicationID,
			UserVersion:       currentstore.UserVersion,
			SchemaIdentity:    currentstore.SchemaIdentity,
			SchemaFingerprint: currentstore.ExpectedSchemaFingerprint,
			GeneratorID:       currentstore.GeneratorID,
			StoreInstanceID:   "manifest-preflight-store",
		},
		Current:   []CurrentPublication{},
		Artifacts: []Artifact{},
	}
}

func manifestArtifactForSize(digit byte, size int64) Artifact {
	digest := strings.Repeat(string(digit), moduleapi.SHA256HexLength)
	return Artifact{
		Path:      artifactsDirectory + "/" + digest,
		Digest:    digest,
		SizeBytes: size,
	}
}
