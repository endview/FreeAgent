package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controloverview"
	"github.com/endview/freeagent/internal/controlweb"
	"github.com/endview/freeagent/internal/currentstore"
)

func TestProductionControlStaticAssetResolverIsExactAndDefensiveV1(t *testing.T) {
	t.Parallel()
	resolver := productionControlStaticAssetResolverV1{}
	for _, path := range controlweb.AssetPaths() {
		want, found, err := controlweb.ResolveAsset(path)
		if err != nil || !found {
			t.Fatalf("controlweb.ResolveAsset(%q): found=%t err=%v", path, found, err)
		}
		got, found, err := resolver.ResolveStaticAssetV1(path)
		if err != nil || !found || got.Path != want.Path ||
			got.MediaType != want.MediaType || got.SHA256 != want.SHA256 ||
			got.Size != uint64(len(want.Bytes)) || !bytes.Equal(got.Bytes, want.Bytes) {
			t.Fatalf("production asset %q=(%+v,%t,%v)", path, got, found, err)
		}
		got.Bytes[0] ^= 0xff
		again, found, err := resolver.ResolveStaticAssetV1(path)
		if err != nil || !found || !bytes.Equal(again.Bytes, want.Bytes) {
			t.Fatalf("production asset %q aliases returned bytes", path)
		}
	}
	for _, path := range []string{"", "/index.html", "assets/../index.html", "missing"} {
		got, found, err := resolver.ResolveStaticAssetV1(path)
		if err != nil || found || got.Path != "" || got.Bytes != nil {
			t.Fatalf("noncanonical asset %q=(%+v,%t,%v)", path, got, found, err)
		}
	}
}

func TestProductionControlOverviewReaderMapsPrivateErrorsV1(t *testing.T) {
	t.Parallel()
	reader := productionControlOverviewReaderV1{}
	if _, err := reader.LoadControlOverviewV1(
		context.Background(),
		"tenant-a",
		"",
		1,
	); !errors.Is(err, controlapp.ErrStoreUnavailable) {
		t.Fatalf("nil Overview Store error=%v", err)
	}
	tests := []struct {
		name string
		in   error
		want error
	}{
		{"cancelled", context.Canceled, controlapp.ErrCancelled},
		{"deadline", context.DeadlineExceeded, controlapp.ErrCancelled},
		{"invalid", fmt.Errorf("wrapped: %w", controloverview.ErrInvalidRequest), controloverview.ErrInvalidRequest},
		{"not found", fmt.Errorf("wrapped: %w", controloverview.ErrNotFound), controloverview.ErrNotFound},
		{"integrity", fmt.Errorf("wrapped: %w", controloverview.ErrIntegrity), controloverview.ErrIntegrity},
		{"busy", fmt.Errorf("wrapped: %w", currentstore.ErrOwnerActive), controlapp.ErrStoreBusy},
		{"closed", fmt.Errorf("wrapped: %w", currentstore.ErrStoreClosed), controlapp.ErrStoreUnavailable},
		{"admission integrity", fmt.Errorf("wrapped: %w", currentstore.ErrAdmissionIntegrity), controlapp.ErrIntegrityFailure},
		{"basis integrity", fmt.Errorf("wrapped: %w", currentstore.ErrPublishedBasisIntegrity), controlapp.ErrIntegrityFailure},
		{"private", errors.New("private sqlite detail"), controlapp.ErrStoreUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mapControlOverviewStoreErrorV1(test.in)
			if !errors.Is(got, test.want) {
				t.Fatalf("map(%v)=%v want classification %v", test.in, got, test.want)
			}
			if test.name == "private" && got.Error() == test.in.Error() {
				t.Fatal("private Store detail escaped adapter")
			}
		})
	}
	if got := mapControlOverviewStoreErrorV1(nil); got != nil {
		t.Fatalf("map(nil)=%v", got)
	}
}

func TestProductionControlManagementReaderAcceptsPageLookaheadV1(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(ctx, path); err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	reader := productionControlManagementReaderV1{store: store}
	if records, more, err := reader.ListUnknownAttempts(
		ctx, defaultTenantID, "", 101,
	); err != nil || len(records) != 0 || more {
		t.Fatalf("UNKNOWN lookahead: records=%d more=%t err=%v", len(records), more, err)
	}
	if records, more, err := reader.ListArtifactAdmissions(
		ctx, defaultTenantID, "", 101,
	); err != nil || len(records) != 0 || more {
		t.Fatalf("Artifact lookahead: records=%d more=%t err=%v", len(records), more, err)
	}
}
