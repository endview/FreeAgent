package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/modulesource"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleSourceCommandsDefaultOffBeforeDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var reads, stores, providers int
	registerDependencies := moduleSourceRegisterDependenciesV1{
		readCanonical: func(context.Context, string) ([]byte, error) {
			reads++
			return nil, errors.New("must not read")
		},
		openStore: func(context.Context, string) (moduleSourceRegisterStoreV1, error) {
			stores++
			return nil, errors.New("must not open")
		},
	}
	if err := runModuleSourceRegisterWithDependenciesV1(
		ctx,
		[]string{"--db", filepath.Join("private-fixture", "store.db"), "--policy", filepath.Join("private-fixture", "policy.json")},
		ioDiscard{},
		ioDiscard{},
		registerDependencies,
	); err == nil || err.Error() != "freeagent module-source-register: failed (DISCOVERY_DISABLED)" {
		t.Fatalf("register error=%v", err)
	}
	refreshDependencies := moduleSourceRefreshDependenciesV1{
		openStore: func(context.Context, string) (moduleSourceRefreshStoreV1, error) {
			stores++
			return nil, errors.New("must not open")
		},
		newProvider: func(modulesource.Config) (moduleSourceObserverV1, error) {
			providers++
			return nil, errors.New("must not construct")
		},
	}
	if err := runModuleSourceRefreshWithDependenciesV1(
		ctx,
		[]string{"--db", filepath.Join("private-fixture", "store.db"), "--source-id", "vendor.source", "--local-directory", filepath.Join("private-fixture", "source")},
		ioDiscard{},
		ioDiscard{},
		refreshDependencies,
	); err == nil || err.Error() != "freeagent module-source-refresh: failed (DISCOVERY_DISABLED)" {
		t.Fatalf("refresh error=%v", err)
	}
	revokeDependencies := modulePublisherKeyRevokeDependenciesV1{
		openStore: func(context.Context, string) (modulePublisherKeyStoreV1, error) {
			stores++
			return nil, errors.New("must not open")
		},
	}
	if err := runModulePublisherKeyRevokeWithDependenciesV1(
		ctx,
		[]string{"--db", filepath.Join("private-fixture", "store.db"), "--key-id", strings.Repeat("a", 64)},
		ioDiscard{},
		ioDiscard{},
		revokeDependencies,
	); err == nil || err.Error() != "freeagent module-publisher-key-revoke: failed (DISCOVERY_DISABLED)" {
		t.Fatalf("revoke error=%v", err)
	}
	if reads != 0 || stores != 0 || providers != 0 {
		t.Fatalf("disabled dependencies: reads=%d stores=%d providers=%d", reads, stores, providers)
	}
}

func TestModuleSourceCommandsRejectPartialOrMixedFlagsBeforeDependencies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var reads, stores, providers int
	registerDependencies := moduleSourceRegisterDependenciesV1{
		readCanonical: func(context.Context, string) ([]byte, error) {
			reads++
			return nil, nil
		},
		openStore: func(context.Context, string) (moduleSourceRegisterStoreV1, error) {
			stores++
			return nil, nil
		},
	}
	err := runModuleSourceRegisterWithDependenciesV1(
		ctx,
		[]string{
			"--enable-module-discovery",
			"--db", "store.db",
			"--policy", "policy.json",
			"--policy-id", strings.Repeat("a", 64),
			"--publisher-key", "key.json",
		},
		ioDiscard{},
		ioDiscard{},
		registerDependencies,
	)
	if err == nil || err.Error() != "freeagent module-source-register: failed (INVALID_FLAGS)" {
		t.Fatalf("register partial flags error=%v", err)
	}
	refreshDependencies := moduleSourceRefreshDependenciesV1{
		openStore: func(context.Context, string) (moduleSourceRefreshStoreV1, error) {
			stores++
			return nil, nil
		},
		newProvider: func(modulesource.Config) (moduleSourceObserverV1, error) {
			providers++
			return nil, nil
		},
	}
	err = runModuleSourceRefreshWithDependenciesV1(
		ctx,
		[]string{
			"--enable-module-discovery",
			"--db", "store.db",
			"--source-id", "vendor.source",
			"--local-directory", filepath.Join("source-root"),
			"--enable-https-module-discovery=false",
		},
		ioDiscard{},
		ioDiscard{},
		refreshDependencies,
	)
	if err == nil || err.Error() != "freeagent module-source-refresh: failed (INVALID_FLAGS)" {
		t.Fatalf("refresh mixed flags error=%v", err)
	}
	if reads != 0 || stores != 0 || providers != 0 {
		t.Fatalf("invalid dependencies: reads=%d stores=%d providers=%d", reads, stores, providers)
	}
}

func TestModuleSourceLocalRegisterAndRefreshEndToEnd(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "current.db")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join(directory, "source")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	index, indexCanonical, indexID, err := moduleapi.NewModuleDiscoveryIndexV1(
		moduleapi.ModuleDiscoveryIndexV1{
			SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
			SourceID:      "vendor.local",
			Entries: []moduleapi.ModuleDiscoveryEntryV1{{
				Module:            moduleapi.Ref{ID: "vendor.tool", Version: "build-1"},
				ArtifactDigest:    strings.Repeat("a", 64),
				ArtifactSizeBytes: 128,
				PackagePath:       "vendor.tool/build-1.modpkg",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRoot, "index.json"), indexCanonical, 0o600); err != nil {
		t.Fatal(err)
	}
	origin := filepath.ToSlash(sourceRoot)
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte(origin),
	)
	if err != nil {
		t.Fatal(err)
	}
	policy, policyCanonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(
		moduleapi.ModuleSourcePolicyV1{
			SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
			SourceID:                "vendor.local",
			Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 moduleapi.ModuleSourceNetworkDenyV1,
			AllowedModuleIDPrefixes: []string{"vendor"},
			MaxIndexBytes:           uint64(len(indexCanonical)),
			MaxPackageBytes:         4096,
			MaxCandidates:           8,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(directory, "policy.json")
	if err := os.WriteFile(policyPath, policyCanonical, 0o600); err != nil {
		t.Fatal(err)
	}
	expectedSnapshot, expectedSnapshotCanonical, expectedSnapshotID, err :=
		moduleapi.NewModuleDiscoverySnapshotV1(
			policyCanonical,
			policyID,
			indexCanonical,
			indexID,
		)
	if err != nil {
		t.Fatal(err)
	}

	var registerOutput bytes.Buffer
	err = runModuleSourceRegister(
		ctx,
		[]string{
			"--enable-module-discovery",
			"--db", databasePath,
			"--policy", policyPath,
			"--policy-id", policyID,
			"--expected-policy-revision", "0",
		},
		&registerOutput,
		ioDiscard{},
	)
	if err != nil {
		t.Fatal(err)
	}
	requireCanonicalSafeModuleSourceOutput(t, registerOutput.Bytes(), databasePath, policyPath, sourceRoot)
	if !bytes.Contains(registerOutput.Bytes(), []byte(`"source_policy_id":"`+policyID+`"`)) ||
		!bytes.Contains(registerOutput.Bytes(), []byte(`"source_id":"`+policy.SourceID+`"`)) {
		t.Fatalf("register output=%s", registerOutput.Bytes())
	}

	var refreshOutput bytes.Buffer
	err = runModuleSourceRefresh(
		ctx,
		[]string{
			"--enable-module-discovery",
			"--db", databasePath,
			"--source-id", policy.SourceID,
			"--local-directory", sourceRoot,
		},
		&refreshOutput,
		ioDiscard{},
	)
	if err != nil {
		t.Fatal(err)
	}
	requireCanonicalSafeModuleSourceOutput(t, refreshOutput.Bytes(), databasePath, policyPath, sourceRoot)
	if !bytes.Contains(refreshOutput.Bytes(), []byte(`"index_id":"`+indexID+`"`)) ||
		!bytes.Contains(refreshOutput.Bytes(), []byte(`"entry_count":`+"1")) ||
		len(index.Entries) != 1 {
		t.Fatalf("refresh output=%s", refreshOutput.Bytes())
	}
	var exactRetryOutput bytes.Buffer
	err = runModuleSourceRefresh(
		ctx,
		[]string{
			"--enable-module-discovery",
			"--db", databasePath,
			"--source-id", policy.SourceID,
			"--local-directory", sourceRoot,
		},
		&exactRetryOutput,
		ioDiscard{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(exactRetryOutput.Bytes(), refreshOutput.Bytes()) {
		t.Fatalf(
			"exact refresh retry changed output:\nfirst=%s\nretry=%s",
			refreshOutput.Bytes(),
			exactRetryOutput.Bytes(),
		)
	}

	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	record, err := store.GetModuleSource(ctx, policy.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	if record.PolicyID != policyID || record.CurrentSnapshotID == "" ||
		record.ObservationRevision != 1 {
		t.Fatalf("source record=%+v", record)
	}
	if record.CurrentSnapshotID != expectedSnapshotID {
		t.Fatalf("snapshot id=%q want=%q", record.CurrentSnapshotID, expectedSnapshotID)
	}
	storedSnapshot, err := store.GetModuleDiscoverySnapshot(ctx, record.CurrentSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if storedSnapshot.SnapshotID != expectedSnapshotID ||
		storedSnapshot.IndexID != indexID ||
		storedSnapshot.SourcePolicyID != policyID ||
		storedSnapshot.ObservationRevision != 1 ||
		!bytes.Equal(storedSnapshot.SourcePolicyCanonical, policyCanonical) ||
		!bytes.Equal(storedSnapshot.IndexCanonical, indexCanonical) ||
		!bytes.Equal(storedSnapshot.SnapshotCanonical, expectedSnapshotCanonical) ||
		len(storedSnapshot.Snapshot.Entries) != len(expectedSnapshot.Entries) {
		t.Fatalf("stored snapshot=%+v", storedSnapshot)
	}
}

func TestModuleSourceHTTPSRefreshWiresExactConfigRequestAndStore(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "current.db")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
	const (
		sourceID = "vendor.https"
		origin   = "https://source.example.test"
		rawURL   = origin + "/modules/index.json"
	)
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindHTTPSIndexV1,
		[]byte(rawURL),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(moduleapi.ModuleSourcePolicyV1{
		SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
		SourceID:                sourceID,
		Kind:                    moduleapi.ModuleSourceKindHTTPSIndexV1,
		OriginDigest:            originDigest,
		Network:                 moduleapi.ModuleSourceNetworkExactHTTPSV1,
		AllowedModuleIDPrefixes: []string{"vendor"},
		MaxIndexBytes:           uint64(moduleapi.MaxModuleDiscoveryIndexBytesV1),
		MaxPackageBytes:         4096,
		MaxCandidates:           8,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterModuleSource(ctx, currentstore.RegisterModuleSourceInput{
		PolicyCanonical: policyCanonical,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, indexCanonical, indexID, err := moduleapi.NewModuleDiscoveryIndexV1(moduleapi.ModuleDiscoveryIndexV1{
		SchemaVersion: moduleapi.ModuleDiscoveryIndexSchemaVersionV1,
		SourceID:      sourceID,
		Entries: []moduleapi.ModuleDiscoveryEntryV1{{
			Module:            moduleapi.Ref{ID: "vendor.https.tool", Version: "build-1"},
			ArtifactDigest:    strings.Repeat("d", 64),
			ArtifactSizeBytes: 128,
			PackagePath:       "vendor.https.tool/build-1.modpkg",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, snapshotCanonical, snapshotID, err := moduleapi.NewModuleDiscoverySnapshotV1(
		policyCanonical,
		policyID,
		indexCanonical,
		indexID,
	)
	if err != nil {
		t.Fatal(err)
	}
	providerCalls := 0
	dependencies := moduleSourceRefreshDependenciesV1{
		openStore: func(ctx context.Context, path string) (moduleSourceRefreshStoreV1, error) {
			if path != databasePath {
				t.Fatalf("Store path = %q", path)
			}
			return currentstore.OpenExistingCurrentStore(ctx, path)
		},
		newProvider: func(config modulesource.Config) (moduleSourceObserverV1, error) {
			if !config.HTTPSIndexEnabled || len(config.HTTPSOriginAllowlist) != 1 ||
				config.HTTPSOriginAllowlist[0] != origin {
				t.Fatalf("HTTPS Provider config = %+v", config)
			}
			return moduleSourceObserverFuncV1(func(_ context.Context, request modulesource.ObserveRequest) (modulesource.Observation, error) {
				providerCalls++
				if request.SourcePolicyID != policyID ||
					!bytes.Equal(request.SourcePolicyCanonical, policyCanonical) ||
					request.LocalDirectory != "" || request.HTTPSIndexURL != rawURL {
					t.Fatalf("HTTPS Observe request = %+v", request)
				}
				return modulesource.Observation{
					SourcePolicyID:    policyID,
					Index:             snapshotIndexForModuleSourceTestV1(t, indexCanonical, indexID),
					IndexCanonical:    bytes.Clone(indexCanonical),
					IndexID:           indexID,
					Snapshot:          snapshot,
					SnapshotCanonical: bytes.Clone(snapshotCanonical),
					SnapshotID:        snapshotID,
				}, nil
			}), nil
		},
	}
	var output bytes.Buffer
	if err := runModuleSourceRefreshWithDependenciesV1(ctx, []string{
		"--enable-module-discovery",
		"--enable-https-module-discovery",
		"--allow-https-source-origin", origin,
		"--db", databasePath,
		"--source-id", sourceID,
		"--https-index-url", rawURL,
	}, &output, ioDiscard{}, dependencies); err != nil {
		t.Fatal(err)
	}
	if providerCalls != 1 ||
		!bytes.Contains(output.Bytes(), []byte(`"snapshot_id":"`+snapshotID+`"`)) ||
		!bytes.Contains(output.Bytes(), []byte(`"entry_count":1`)) {
		t.Fatalf("HTTPS refresh calls=%d output=%s", providerCalls, output.Bytes())
	}
	store, err = currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stored, err := store.GetModuleDiscoverySnapshot(ctx, snapshotID)
	if err != nil || stored.IndexID != indexID || stored.SourcePolicyID != policyID ||
		stored.ObservationRevision != 1 {
		t.Fatalf("stored HTTPS Snapshot = %+v, %v", stored, err)
	}
}

func TestModulePublisherKeyRevokeEndToEnd(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "current.db")
	if _, err := currentstore.InitFreshCurrentStore(ctx, databasePath); err != nil {
		t.Fatal(err)
	}
	publicKey := make([]byte, ed25519.PublicKeySize)
	for index := range publicKey {
		publicKey[index] = byte(index + 1)
	}
	_, keyCanonical, keyID, err := moduleapi.NewModulePublisherKeyV1(
		moduleapi.ModulePublisherKeyV1{
			SchemaVersion:   moduleapi.ModulePublisherKeySchemaVersionV1,
			Algorithm:       moduleapi.ModuleSignatureAlgorithmEd25519V1,
			PublicKeyBase64: base64.StdEncoding.EncodeToString(publicKey),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	originDigest, err := moduleapi.ModuleSourceOriginDigestV1(
		moduleapi.ModuleSourceKindLocalDirectoryV1,
		[]byte("bounded/test/source"),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, policyCanonical, policyID, err := moduleapi.NewModuleSourcePolicyV1(
		moduleapi.ModuleSourcePolicyV1{
			SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
			SourceID:                "vendor.signed",
			Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            originDigest,
			Network:                 moduleapi.ModuleSourceNetworkDenyV1,
			SignatureRequired:       true,
			PublisherKeyID:          keyID,
			AllowedModuleIDPrefixes: []string{"vendor"},
			MaxIndexBytes:           4096,
			MaxPackageBytes:         4096,
			MaxCandidates:           8,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(directory, "policy.json")
	keyPath := filepath.Join(directory, "key.json")
	if err := os.WriteFile(policyPath, policyCanonical, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyCanonical, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runModuleSourceRegister(
		ctx,
		[]string{
			"--enable-module-discovery", "--db", databasePath,
			"--policy", policyPath, "--policy-id", policyID,
			"--publisher-key", keyPath, "--publisher-key-id", keyID,
			"--expected-policy-revision", "0",
		},
		ioDiscard{},
		ioDiscard{},
	); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runModulePublisherKeyRevoke(
		ctx,
		[]string{
			"--enable-module-discovery", "--db", databasePath,
			"--key-id", keyID, "--expected-key-revision", "1",
		},
		&output,
		ioDiscard{},
	); err != nil {
		t.Fatal(err)
	}
	requireCanonicalSafeModuleSourceOutput(t, output.Bytes(), databasePath, policyPath, keyPath)
	if !bytes.Contains(output.Bytes(), []byte(`"publisher_key_id":"`+keyID+`"`)) ||
		!bytes.Contains(output.Bytes(), []byte(`"key_revision":2`)) {
		t.Fatalf("revoke output=%s", output.Bytes())
	}
}

func TestModuleSourceFailureMappingDoesNotLeakCause(t *testing.T) {
	t.Parallel()
	sensitiveValue := filepath.Join("private-fixture", "source", "index.json")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"store missing", fmt.Errorf("%w: %s", currentstore.ErrModuleSourceNotFound, sensitiveValue), "freeagent module-source-refresh: failed (SOURCE_NOT_FOUND)"},
		{"store conflict", fmt.Errorf("%w: %s", currentstore.ErrModuleSourceConflict, sensitiveValue), "freeagent module-source-refresh: failed (SOURCE_CONFLICT)"},
		{"provider denied", testModuleSourceProviderErrorV1(modulesource.FailureSourceDenied), "freeagent module-source-refresh: failed (SOURCE_DENIED)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got error
			if strings.HasPrefix(test.name, "provider") {
				got = moduleSourceCommandFailureFromProviderErrorV1("module-source-refresh", test.err)
			} else {
				got = moduleSourceCommandFailureFromStoreErrorV1("module-source-refresh", test.err)
			}
			if got == nil || got.Error() != test.want || strings.Contains(got.Error(), sensitiveValue) {
				t.Fatalf("error=%v want=%q", got, test.want)
			}
		})
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(value []byte) (int, error) { return len(value), nil }

type moduleSourceObserverFuncV1 func(context.Context, modulesource.ObserveRequest) (modulesource.Observation, error)

func (observe moduleSourceObserverFuncV1) Observe(
	ctx context.Context,
	request modulesource.ObserveRequest,
) (modulesource.Observation, error) {
	return observe(ctx, request)
}

func snapshotIndexForModuleSourceTestV1(
	t *testing.T,
	canonical []byte,
	expectedID string,
) moduleapi.ModuleDiscoveryIndexV1 {
	t.Helper()
	index, restored, restoredID, err := moduleapi.ParseModuleDiscoveryIndexV1(canonical)
	if err != nil || restoredID != expectedID || !bytes.Equal(restored, canonical) {
		t.Fatalf("restore test Index id=%s err=%v", restoredID, err)
	}
	return index
}

func requireCanonicalSafeModuleSourceOutput(t *testing.T, payload []byte, sensitiveValues ...string) {
	t.Helper()
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		t.Fatalf("output is not newline-terminated: %q", payload)
	}
	canonical, err := moduleapi.CanonicalJSON(bytes.TrimSuffix(payload, []byte{'\n'}))
	if err != nil || !bytes.Equal(canonical, bytes.TrimSuffix(payload, []byte{'\n'})) {
		t.Fatalf("output is not canonical JSON: %q err=%v", payload, err)
	}
	for _, sensitiveValue := range sensitiveValues {
		if sensitiveValue != "" && strings.Contains(string(payload), sensitiveValue) {
			t.Fatalf("output leaked %q: %s", sensitiveValue, payload)
		}
	}
}

func testModuleSourceProviderErrorV1(code modulesource.FailureCode) error {
	// Provider errors deliberately hide their constructor. Generate one through
	// a fail-closed, zero-I/O Observe call so CLI mapping is tested against the
	// actual public error type rather than a forged value.
	provider, err := modulesource.New(modulesource.Config{})
	if err != nil {
		return err
	}
	_, err = provider.Observe(context.Background(), modulesource.ObserveRequest{})
	if code == modulesource.FailureSourceInputInvalid {
		return err
	}
	// SOURCE_DENIED is obtained from a valid local policy whose origin cannot
	// match the deliberately absent path without touching a package artifact.
	_, policyCanonical, policyID, policyErr := moduleapi.NewModuleSourcePolicyV1(
		moduleapi.ModuleSourcePolicyV1{
			SchemaVersion:           moduleapi.ModuleSourcePolicySchemaVersionV1,
			SourceID:                "vendor.failure",
			Kind:                    moduleapi.ModuleSourceKindLocalDirectoryV1,
			OriginDigest:            strings.Repeat("a", 64),
			Network:                 moduleapi.ModuleSourceNetworkDenyV1,
			AllowedModuleIDPrefixes: []string{"vendor"},
			MaxIndexBytes:           1,
			MaxPackageBytes:         1,
			MaxCandidates:           1,
		},
	)
	if policyErr != nil {
		return policyErr
	}
	_, err = provider.Observe(context.Background(), modulesource.ObserveRequest{
		SourcePolicyCanonical: policyCanonical,
		SourcePolicyID:        policyID,
		LocalDirectory:        filepath.Join(os.TempDir(), "definitely-absent-freeagent-source"),
	})
	return err
}
