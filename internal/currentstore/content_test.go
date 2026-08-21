package currentstore

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestContentKindClosedSet(t *testing.T) {
	kinds := []ContentKind{
		ContentModuleManifest,
		ContentConfig,
		ContentAuthorityCeiling,
		ContentTaskInput,
		ContentPolicy,
		ContentModelRequest,
		ContentContextCompilation,
		ContentModelResult,
		ContentActionProposal,
		ContentActionResult,
		ContentProviderReceipt,
		ContentStaticContext,
		ContentMemorySnapshot,
		ContentReconciliationEvidence,
		ContentRunEventPayload,
		ContentRunCancellation,
		ContentChannelCursor,
		ContentChannelIngressEnvelope,
		ContentChannelSendProposal,
		ContentChannelSendResult,
		ContentWorkspaceTransferPayload,
		ContentWorkspaceTransferEnvelope,
	}
	if len(kinds) != 22 {
		t.Fatalf("content kinds=%d want 22", len(kinds))
	}
	seen := make(map[ContentKind]struct{}, len(kinds))
	for _, kind := range kinds {
		if !kind.Valid() {
			t.Fatalf("ContentKind(%q).Valid()=false", kind)
		}
		if _, duplicate := seen[kind]; duplicate {
			t.Fatalf("duplicate ContentKind %q", kind)
		}
		seen[kind] = struct{}{}
	}
	if ContentKind("").Valid() || ContentKind("MEMORY").Valid() {
		t.Fatal("ContentKind.Valid accepted a kind outside the frozen set")
	}
}

func TestComputeContentDigestGolden(t *testing.T) {
	const want = "4df06f6efc54c72be24394520728c5be80672467cb93d091390570a418726e87"
	got, err := ComputeContentDigest(
		ContentTaskInput,
		"application/json",
		[]byte(`{"a":1,"b":"x"}`),
	)
	if err != nil {
		t.Fatalf("ComputeContentDigest: %v", err)
	}
	if got != want {
		t.Fatalf("ComputeContentDigest=%s want %s", got, want)
	}
}

func TestComputeContentDigestCanonicalJSONAndOpaqueBytes(t *testing.T) {
	for _, test := range []struct {
		name      string
		kind      ContentKind
		mediaType string
		body      []byte
		wantError bool
	}{
		{
			name:      "canonical application json",
			kind:      ContentPolicy,
			mediaType: "application/json",
			body:      []byte(`{"a":1,"b":2}`),
		},
		{
			name:      "canonical structured suffix",
			kind:      ContentPolicy,
			mediaType: "application/problem+json",
			body:      []byte(`{"detail":"x"}`),
		},
		{
			name:      "noncanonical member order",
			kind:      ContentPolicy,
			mediaType: "application/json",
			body:      []byte(`{"b":2,"a":1}`),
			wantError: true,
		},
		{
			name:      "noncanonical whitespace",
			kind:      ContentPolicy,
			mediaType: "application/json",
			body:      []byte(`{"a": 1}`),
			wantError: true,
		},
		{
			name:      "opaque invalid utf8",
			kind:      ContentStaticContext,
			mediaType: "application/octet-stream",
			body:      []byte{0xff, 0x00, 0x80},
		},
		{
			name:      "empty opaque body",
			kind:      ContentStaticContext,
			mediaType: "application/octet-stream",
			body:      nil,
		},
		{
			name:      "invalid kind",
			kind:      ContentKind("RAG_CHUNK"),
			mediaType: "application/octet-stream",
			body:      []byte("x"),
			wantError: true,
		},
		{
			name:      "untrimmed media",
			kind:      ContentTaskInput,
			mediaType: " application/json",
			body:      []byte(`{}`),
			wantError: true,
		},
		{
			name:      "invalid media",
			kind:      ContentTaskInput,
			mediaType: "not a media type",
			body:      []byte("x"),
			wantError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ComputeContentDigest(
				test.kind,
				test.mediaType,
				test.body,
			)
			if test.wantError && !errors.Is(err, ErrInvalidContent) {
				t.Fatalf("error=%v want ErrInvalidContent", err)
			}
			if !test.wantError && err != nil {
				t.Fatalf("ComputeContentDigest: %v", err)
			}
		})
	}
}

func TestPutGetContentIdempotentAndDefensive(t *testing.T) {
	store := openContentTestStore(t)
	body := []byte(`{"message":"hello"}`)
	digest, err := ComputeContentDigest(
		ContentTaskInput,
		"application/json",
		body,
	)
	if err != nil {
		t.Fatal(err)
	}
	input := ContentInput{
		Digest:         digest,
		Kind:           ContentTaskInput,
		MediaType:      "application/json",
		CanonicalBytes: body,
	}
	first, err := store.PutContent(context.Background(), input)
	if err != nil {
		t.Fatalf("PutContent: %v", err)
	}
	body[0] = '!'
	if got := string(first.CanonicalBytes); got != `{"message":"hello"}` {
		t.Fatalf("PutContent returned bytes=%q", got)
	}
	first.CanonicalBytes[0] = '!'

	second, err := store.PutContent(context.Background(), ContentInput{
		Digest:         digest,
		Kind:           ContentTaskInput,
		MediaType:      "application/json",
		CanonicalBytes: []byte(`{"message":"hello"}`),
	})
	if err != nil {
		t.Fatalf("idempotent PutContent: %v", err)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf(
			"idempotent PutContent changed created_at: %s != %s",
			second.CreatedAt,
			first.CreatedAt,
		)
	}

	got, err := store.GetContent(context.Background(), digest)
	if err != nil {
		t.Fatalf("GetContent: %v", err)
	}
	if got.Digest != digest ||
		got.Kind != ContentTaskInput ||
		got.MediaType != "application/json" ||
		got.SizeBytes != int64(len(`{"message":"hello"}`)) ||
		string(got.CanonicalBytes) != `{"message":"hello"}` {
		t.Fatalf("GetContent=%+v", got)
	}
	got.CanonicalBytes[0] = '!'
	again, err := store.GetContent(context.Background(), digest)
	if err != nil {
		t.Fatalf("second GetContent: %v", err)
	}
	if string(again.CanonicalBytes) != `{"message":"hello"}` {
		t.Fatalf("GetContent exposed aliased bytes: %q", again.CanonicalBytes)
	}
}

func TestPutContentRejectsDigestMismatchWithoutWriting(t *testing.T) {
	store := openContentTestStore(t)
	const otherDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err := store.PutContent(context.Background(), ContentInput{
		Digest:         otherDigest,
		Kind:           ContentTaskInput,
		MediaType:      "application/json",
		CanonicalBytes: []byte(`{"a":1}`),
	})
	if !errors.Is(err, ErrContentIntegrity) {
		t.Fatalf("PutContent error=%v want ErrContentIntegrity", err)
	}
	if _, err := store.GetContent(
		context.Background(),
		otherDigest,
	); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("GetContent error=%v want ErrContentNotFound", err)
	}
}

func TestPutContentFailsClosedOnConflictingStoredRow(t *testing.T) {
	store := openContentTestStore(t)
	body := []byte(`{"right":true}`)
	digest, err := ComputeContentDigest(
		ContentModelResult,
		"application/json",
		body,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO content_records(
			content_digest, kind, media_type, canonical_bytes, size_bytes, created_at
		) VALUES(?, ?, ?, ?, ?, ?)
	`,
		digest,
		string(ContentModelResult),
		"application/json",
		[]byte(`{"wrong":true}`),
		len(`{"wrong":true}`),
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro(),
	); err != nil {
		t.Fatalf("inject conflicting row: %v", err)
	}
	_, err = store.PutContent(context.Background(), ContentInput{
		Digest:         digest,
		Kind:           ContentModelResult,
		MediaType:      "application/json",
		CanonicalBytes: body,
	})
	if !errors.Is(err, ErrContentIntegrity) {
		t.Fatalf("PutContent error=%v want ErrContentIntegrity", err)
	}
}

func TestGetContentNotFoundAndStoreLifecycle(t *testing.T) {
	const missing = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	var nilStore *Store
	if _, err := nilStore.GetContent(
		context.Background(),
		missing,
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("nil GetContent error=%v want ErrStoreClosed", err)
	}
	if _, err := nilStore.PutContent(
		context.Background(),
		ContentInput{},
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("nil PutContent error=%v want ErrStoreClosed", err)
	}

	store := openContentTestStore(t)
	if _, err := store.GetContent(
		context.Background(),
		missing,
	); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("GetContent error=%v want ErrContentNotFound", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := store.GetContent(
		context.Background(),
		missing,
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed GetContent error=%v want ErrStoreClosed", err)
	}
	if _, err := store.PutContent(
		context.Background(),
		ContentInput{},
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed PutContent error=%v want ErrStoreClosed", err)
	}
}

func openContentTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "content.sqlite")
	if _, err := InitFreshCurrentStore(context.Background(), path); err != nil {
		t.Fatalf("InitFreshCurrentStore: %v", err)
	}
	store, err := OpenExistingCurrentStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenExistingCurrentStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func TestComputeContentDigestUsesRawNonJSONBytes(t *testing.T) {
	first, err := ComputeContentDigest(
		ContentStaticContext,
		"text/plain",
		[]byte("e\u0301"),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ComputeContentDigest(
		ContentStaticContext,
		"text/plain",
		[]byte("\u00e9"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("non-JSON bytes were normalized before hashing")
	}
	if bytes.Equal([]byte("e\u0301"), []byte("\u00e9")) {
		t.Fatal("test fixture unexpectedly has equal bytes")
	}
}
