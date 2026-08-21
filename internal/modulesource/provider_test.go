package modulesource

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const testSourceID = "vendor.discovery"

func TestObserveLocalIndexFreezesExactSnapshot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	indexCanonical := testDiscoveryIndex(t)
	if err := os.WriteFile(
		filepath.Join(root, localIndexFilename),
		indexCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	policyCanonical, policyID := testSourcePolicy(
		t,
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		moduleapi.ModuleSourceNetworkDenyV1,
		filepath.ToSlash(root),
		uint64(len(indexCanonical)),
	)
	provider, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := provider.Observe(context.Background(), ObserveRequest{
		SourcePolicyCanonical: policyCanonical,
		SourcePolicyID:        policyID,
		LocalDirectory:        root,
	})
	if err != nil {
		t.Fatalf("Observe(local) error = %v cause=%v code=%s", err, errors.Unwrap(err), FailureCodeOf(err))
	}
	if observation.SourcePolicyID != policyID ||
		!bytes.Equal(observation.IndexCanonical, indexCanonical) ||
		observation.IndexID == "" || observation.SnapshotID == "" ||
		observation.Snapshot.SourcePolicyID != policyID ||
		observation.Snapshot.IndexID != observation.IndexID {
		t.Fatalf("unexpected observation: %+v", observation)
	}
	if _, err := moduleapi.RestoreModuleDiscoverySnapshotV1(
		observation.SnapshotCanonical,
		observation.SnapshotID,
		policyCanonical,
		policyID,
		observation.IndexCanonical,
		observation.IndexID,
	); err != nil {
		t.Fatalf("snapshot parent closure failed: %v", err)
	}

	observation.IndexCanonical[0] = '['
	observation.SnapshotCanonical[0] = '['
	observation.Index.Entries[0].PackagePath = "changed"
	second, err := provider.Observe(context.Background(), ObserveRequest{
		SourcePolicyCanonical: policyCanonical,
		SourcePolicyID:        policyID,
		LocalDirectory:        root,
	})
	if err != nil || !bytes.Equal(second.IndexCanonical, indexCanonical) ||
		second.Index.Entries[0].PackagePath == "changed" {
		t.Fatalf("observation aliases returned data: observation=%+v err=%v", second, err)
	}
}

func TestObserveLocalIndexRejectsOriginLimitAndUnsafePaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	indexCanonical := testDiscoveryIndex(t)
	indexPath := filepath.Join(root, localIndexFilename)
	if err := os.WriteFile(indexPath, indexCanonical, 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}

	wrongCanonical, wrongID := testSourcePolicy(
		t,
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		moduleapi.ModuleSourceNetworkDenyV1,
		filepath.ToSlash(t.TempDir()),
		uint64(len(indexCanonical)),
	)
	_, err = provider.Observe(context.Background(), ObserveRequest{
		SourcePolicyCanonical: wrongCanonical,
		SourcePolicyID:        wrongID,
		LocalDirectory:        root,
	})
	requireFailureCode(t, err, FailureSourceDenied)

	limitedCanonical, limitedID := testSourcePolicy(
		t,
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		moduleapi.ModuleSourceNetworkDenyV1,
		filepath.ToSlash(root),
		uint64(len(indexCanonical)-1),
	)
	_, err = provider.Observe(context.Background(), ObserveRequest{
		SourcePolicyCanonical: limitedCanonical,
		SourcePolicyID:        limitedID,
		LocalDirectory:        root,
	})
	requireFailureCode(t, err, FailureSourceDenied)

	symlinkRoot := t.TempDir()
	if err := os.Symlink(indexPath, filepath.Join(symlinkRoot, localIndexFilename)); err == nil {
		symlinkCanonical, symlinkID := testSourcePolicy(
			t,
			moduleapi.ModuleSourceKindLocalDirectoryV1,
			moduleapi.ModuleSourceNetworkDenyV1,
			filepath.ToSlash(symlinkRoot),
			uint64(len(indexCanonical)),
		)
		_, err = provider.Observe(context.Background(), ObserveRequest{
			SourcePolicyCanonical: symlinkCanonical,
			SourcePolicyID:        symlinkID,
			LocalDirectory:        symlinkRoot,
		})
		requireFailureCode(t, err, FailureSourceDenied)
	}

	if runtime.GOOS == "windows" {
		drivePath := "C" + ":" + `\missing`
		for _, unsafe := range []string{
			`\\server.invalid\share\missing`,
			`\\?\` + drivePath,
			`\\.\` + drivePath,
			`\??\` + drivePath,
			root + `:stream`,
		} {
			if _, resolveErr := resolveStableLocalDirectory(unsafe); resolveErr == nil {
				t.Fatalf("unsafe Windows path accepted: %q", unsafe)
			}
		}
	}
}

func TestObserveHTTPSIndexUsesOneBoundedHTTP11GET(t *testing.T) {
	var requests atomic.Int32
	indexCanonical := testDiscoveryIndex(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			requests.Add(1)
			if request.Method != http.MethodGet || request.URL.Path != "/modules/index.json" ||
				request.Header.Get("Accept") != moduleIndexMediaType ||
				request.ProtoMajor != 1 || !request.Close || request.Body != http.NoBody ||
				request.GetBody != nil || request.Header.Get("Authorization") != "" ||
				request.Header.Get("Cookie") != "" ||
				request.Header.Get("Proxy-Authorization") != "" ||
				request.Header.Get("If-None-Match") != "" ||
				request.Header.Get("If-Modified-Since") != "" {
				t.Errorf("unexpected request: method=%s path=%s accept=%q proto=%s",
					request.Method, request.URL.Path, request.Header.Get("Accept"), request.Proto)
			}
			writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = writer.Write(indexCanonical)
		},
	))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	provider, rawURL, policyCanonical, policyID, counters :=
		testHTTPSProvider(t, server, "/modules/index.json", indexCanonical)
	observation, err := provider.Observe(context.Background(), ObserveRequest{
		SourcePolicyCanonical: policyCanonical,
		SourcePolicyID:        policyID,
		HTTPSIndexURL:         rawURL,
	})
	if err != nil {
		t.Fatalf("Observe(HTTPS) error=%v code=%s", err, FailureCodeOf(err))
	}
	if requests.Load() != 1 || counters.lookups.Load() != 1 ||
		counters.dials.Load() != 1 ||
		!bytes.Equal(observation.IndexCanonical, indexCanonical) {
		t.Fatalf("counts requests=%d lookup=%d dial=%d observation=%+v",
			requests.Load(), counters.lookups.Load(), counters.dials.Load(), observation)
	}
}

func TestObserveHTTPSIndexCommitsExactStoreSnapshot(t *testing.T) {
	var requests atomic.Int32
	indexCanonical := testDiscoveryIndex(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			requests.Add(1)
			writer.Header().Set("Content-Type", moduleIndexMediaType)
			_, _ = writer.Write(indexCanonical)
		},
	))
	server.StartTLS()
	defer server.Close()

	provider, rawURL, policyCanonical, policyID, counters :=
		testHTTPSProvider(t, server, "/modules/index.json", indexCanonical)
	databasePath := filepath.Join(t.TempDir(), "current.sqlite")
	if _, err := currentstore.InitFreshCurrentStore(context.Background(), databasePath); err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	registered, err := store.RegisterModuleSource(context.Background(), currentstore.RegisterModuleSourceInput{
		PolicyCanonical: policyCanonical,
	})
	if err != nil {
		t.Fatal(err)
	}
	if registered.PolicyID != policyID {
		t.Fatalf("registered PolicyID = %s, want %s", registered.PolicyID, policyID)
	}
	basis, err := store.ReadModuleSourceRefreshBasis(context.Background(), testSourceID)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := provider.Observe(context.Background(), ObserveRequest{
		SourcePolicyCanonical: basis.Source.PolicyCanonical,
		SourcePolicyID:        basis.Source.PolicyID,
		HTTPSIndexURL:         rawURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	committed, err := store.CommitModuleSourceRefresh(
		context.Background(),
		basis,
		observation.IndexCanonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if committed.SourcePolicyID != policyID ||
		committed.IndexID != observation.IndexID ||
		committed.SnapshotID != observation.SnapshotID ||
		!bytes.Equal(committed.IndexCanonical, observation.IndexCanonical) ||
		!bytes.Equal(committed.SnapshotCanonical, observation.SnapshotCanonical) ||
		committed.ObservationRevision != 1 || len(committed.Snapshot.Entries) != 1 {
		t.Fatalf("committed Snapshot = %+v", committed)
	}
	if requests.Load() != 1 || counters.lookups.Load() != 1 || counters.dials.Load() != 1 {
		t.Fatalf("HTTPS work: requests=%d lookups=%d dials=%d",
			requests.Load(), counters.lookups.Load(), counters.dials.Load())
	}
}

func TestObserveHTTPSIndexRejectsRedirectWithoutFollowing(t *testing.T) {
	var first, redirected atomic.Int32
	index := testDiscoveryIndex(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/redirected" {
				redirected.Add(1)
				writer.Header().Set("Content-Type", moduleIndexMediaType)
				_, _ = writer.Write(index)
				return
			}
			first.Add(1)
			http.Redirect(writer, request, "/redirected", http.StatusFound)
		},
	))
	server.StartTLS()
	defer server.Close()
	provider, rawURL, policyCanonical, policyID, counters :=
		testHTTPSProvider(t, server, "/index.json", index)
	_, err := provider.Observe(context.Background(), ObserveRequest{
		SourcePolicyCanonical: policyCanonical,
		SourcePolicyID:        policyID,
		HTTPSIndexURL:         rawURL,
	})
	requireFailureCode(t, err, FailureSourceDenied)
	if first.Load() != 1 || redirected.Load() != 0 {
		t.Fatalf("redirect counts first=%d followed=%d", first.Load(), redirected.Load())
	}
	if counters.lookups.Load() != 1 || counters.dials.Load() != 1 {
		t.Fatalf("redirect caused extra network work: lookups=%d dials=%d",
			counters.lookups.Load(), counters.dials.Load())
	}
}

type httpsTestCounters struct {
	lookups atomic.Int32
	dials   atomic.Int32
}

func testHTTPSProvider(
	t *testing.T,
	server *httptest.Server,
	requestPath string,
	indexCanonical []byte,
) (*Provider, string, []byte, string, *httpsTestCounters) {
	t.Helper()
	_, serverPort, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	rawURL := "https://source.example.test:" + serverPort + requestPath
	origin := "https://source.example.test:" + serverPort
	counters := &httpsTestCounters{}
	provider, err := newProvider(Config{
		HTTPSIndexEnabled:    true,
		HTTPSOriginAllowlist: []string{origin},
		Timeout:              5 * time.Second,
	}, httpsDependencies{
		lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
			counters.lookups.Add(1)
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		},
		dialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			counters.dials.Add(1)
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
		tlsConfig: &tls.Config{InsecureSkipVerify: true}, // test-only hermetic TLS
	})
	if err != nil {
		t.Fatal(err)
	}
	policyCanonical, policyID := testSourcePolicy(
		t,
		moduleapi.ModuleSourceKindHTTPSIndexV1,
		moduleapi.ModuleSourceNetworkExactHTTPSV1,
		rawURL,
		uint64(len(indexCanonical)),
	)
	return provider, rawURL, policyCanonical, policyID, counters
}

func testDiscoveryIndex(t *testing.T) []byte {
	t.Helper()
	_, canonical, _, err := moduleapi.NewModuleDiscoveryIndexV1(
		moduleapi.ModuleDiscoveryIndexV1{
			SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      testSourceID,
			Entries: []moduleapi.ModuleDiscoveryEntryV1{{
				Module:            moduleapi.Ref{ID: "vendor.tool", Version: "build-1"},
				ArtifactDigest:    strings.Repeat("a", 64),
				ArtifactSizeBytes: 1,
				PackagePath:       "vendor-tool-build-1.modpkg",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func testSourcePolicy(
	t *testing.T,
	kind moduleapi.ModuleSourceKindV1,
	network moduleapi.ModuleSourceNetworkV1,
	origin string,
	maxIndexBytes uint64,
) ([]byte, string) {
	t.Helper()
	digest, err := moduleapi.ModuleSourceOriginDigestV1(kind, []byte(origin))
	if err != nil {
		t.Fatal(err)
	}
	_, canonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(
		moduleapi.ModuleSourcePolicyV1{
			SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
			SourceID:                testSourceID,
			Kind:                    kind,
			OriginDigest:            digest,
			Network:                 network,
			AllowedModuleIDPrefixes: []string{"vendor"},
			MaxIndexBytes:           maxIndexBytes,
			MaxPackageBytes:         1024,
			MaxCandidates:           8,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical, policyID
}

func requireFailureCode(t *testing.T, err error, code FailureCode) {
	t.Helper()
	if err == nil || FailureCodeOf(err) != code {
		t.Fatalf("error=%v code=%s, want code=%s", err, FailureCodeOf(err), code)
	}
}

func TestProviderInputAndCancellationAreFailClosed(t *testing.T) {
	index := testDiscoveryIndex(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, localIndexFilename), index, 0o600); err != nil {
		t.Fatal(err)
	}
	policy, policyID := testSourcePolicy(
		t,
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		moduleapi.ModuleSourceNetworkDenyV1,
		filepath.ToSlash(root),
		uint64(len(index)),
	)
	provider, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Observe(nil, ObserveRequest{}); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = provider.Observe(ctx, ObserveRequest{
		SourcePolicyCanonical: policy,
		SourcePolicyID:        policyID,
		LocalDirectory:        root,
	})
	if !errors.Is(err, context.Canceled) || FailureCodeOf(err) != FailureSourceCancelled {
		t.Fatalf("cancel error=%v code=%s", err, FailureCodeOf(err))
	}
	_, err = provider.Observe(context.Background(), ObserveRequest{
		SourcePolicyCanonical: policy,
		SourcePolicyID:        policyID,
		LocalDirectory:        root,
		HTTPSIndexURL:         "https://source.example.test/index.json",
	})
	requireFailureCode(t, err, FailureSourceInputInvalid)
}
