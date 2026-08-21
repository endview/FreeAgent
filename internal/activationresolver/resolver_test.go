package activationresolver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	testTrustedAdapter      = "local.trusted.adapter"
	testDeclarativeAdapter  = "local.declarative.adapter"
	testLocalProcessAdapter = "local.mcp-stdio.adapter"
	testRemoteActionAdapter = "remote.action-http.adapter"
	testWASMActionAdapter   = "wasm.action.adapter"
)

var (
	testArtifactDigest     = strings.Repeat("a", moduleapi.SHA256HexLength)
	testDriftedArtifact    = strings.Repeat("b", moduleapi.SHA256HexLength)
	testManifestEntrypoint = "manifest.requested.adapter"
	testDeclarativeContent = "content/provider.json"
)

func TestTrustedInProcessRejectsUnallowlistedModule(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		testManifestEntrypoint,
		testArtifactDigest,
	)
	resolver := mustResolver(t, nil, nil)

	_, err := resolver.Resolve(
		context.Background(),
		testResolveInput(installation),
	)
	if !errors.Is(err, ErrTrustedModuleNotAllowlisted) {
		t.Fatalf("Resolve() error = %v, want not-allowlisted", err)
	}
}

func TestTrustedInProcessRejectsArtifactDriftBeforeRegistryProbe(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		testManifestEntrypoint,
		testDriftedArtifact,
	)
	probe := &recordingRegistryProbe{registered: true}
	resolver := mustResolver(
		t,
		[]TrustedInProcessAllowlistEntry{testAllowlistEntry(
			testArtifactDigest,
			testTrustedAdapter,
		)},
		probe,
	)

	_, err := resolver.Resolve(
		context.Background(),
		testResolveInput(installation),
	)
	if !errors.Is(err, ErrTrustedModuleNotAllowlisted) {
		t.Fatalf("Resolve() error = %v, want not-allowlisted", err)
	}
	if probe.calls != 0 {
		t.Fatalf("registry probe calls = %d, want 0", probe.calls)
	}
}

func TestTrustedInProcessRejectsUnknownExactAdapter(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		testManifestEntrypoint,
		testArtifactDigest,
	)
	probe := &recordingRegistryProbe{}
	resolver := mustResolver(
		t,
		[]TrustedInProcessAllowlistEntry{testAllowlistEntry(
			testArtifactDigest,
			testTrustedAdapter,
		)},
		probe,
	)

	_, err := resolver.Resolve(
		context.Background(),
		testResolveInput(installation),
	)
	if !errors.Is(err, ErrAdapterNotRegistered) {
		t.Fatalf("Resolve() error = %v, want adapter-not-registered", err)
	}
	if probe.calls != 1 ||
		probe.artifactDigest != testArtifactDigest ||
		probe.adapterIdentity != testTrustedAdapter {
		t.Fatalf(
			"probe calls/digest/adapter = %d/%q/%q",
			probe.calls,
			probe.artifactDigest,
			probe.adapterIdentity,
		)
	}
}

func TestTrustedManifestRequestCannotChooseGrantedAdapter(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		testManifestEntrypoint,
		testArtifactDigest,
	)
	probe := &recordingRegistryProbe{registered: true}
	resolver := mustResolver(
		t,
		[]TrustedInProcessAllowlistEntry{testAllowlistEntry(
			testArtifactDigest,
			testTrustedAdapter,
		)},
		probe,
	)

	result, err := resolver.Resolve(
		context.Background(),
		testResolveInput(installation),
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		result.AdapterIdentity != testTrustedAdapter {
		t.Fatalf("Resolve() result = %+v", result)
	}
	if result.AdapterIdentity == testManifestEntrypoint {
		t.Fatal("manifest entrypoint became the granted adapter")
	}
}

func TestDeclarativeUsesConfiguredLocalAdapter(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestDeclarative,
		testDeclarativeContent,
		testArtifactDigest,
	)
	probe := &recordingRegistryProbe{
		err: errors.New("declarative path must not probe trusted registry"),
	}
	resolver := mustResolver(t, nil, probe)
	input := testResolveInput(installation)
	input.ExpectedExecutionClass = moduleapi.ExecutionDeclarative
	input.ExpectedAdapterIdentity = testDeclarativeAdapter

	result, err := resolver.Resolve(context.Background(), input)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.ExecutionClass != moduleapi.ExecutionDeclarative ||
		result.AdapterIdentity != testDeclarativeAdapter ||
		result.AdapterIdentity == testDeclarativeContent {
		t.Fatalf("Resolve() result = %+v", result)
	}
	if probe.calls != 0 {
		t.Fatalf("registry probe calls = %d, want 0", probe.calls)
	}
}

func TestLocalProcessRequiresExactCoreGrantAndRegisteredAdapter(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestLocalProcess,
		"content/mcp-server",
		testArtifactDigest,
	)
	probe := &recordingRegistryProbe{registered: true}
	ungranted := mustResolver(t, nil, probe)
	asserted := testResolveInput(installation)
	asserted.ExpectedExecutionClass = moduleapi.ExecutionLocalProcess
	asserted.ExpectedAdapterIdentity = testLocalProcessAdapter
	if _, err := ungranted.Resolve(
		context.Background(),
		asserted,
	); !errors.Is(err, ErrLocalProcessModuleNotGranted) {
		t.Fatalf("ungranted Resolve() error = %v, want local-process-not-granted", err)
	}
	if probe.calls != 0 {
		t.Fatalf("ungranted registry probe calls = %d, want 0", probe.calls)
	}

	resolver := mustResolverWithLocalProcess(
		t,
		[]LocalProcessGrant{testLocalProcessGrant(
			testArtifactDigest,
			testLocalProcessAdapter,
		)},
		probe,
	)
	input := testResolveInput(installation)
	input.ExpectedExecutionClass = moduleapi.ExecutionLocalProcess
	input.ExpectedAdapterIdentity = testLocalProcessAdapter
	result, err := resolver.Resolve(context.Background(), input)
	if err != nil {
		t.Fatalf("granted Resolve() error = %v", err)
	}
	if result.ExecutionClass != moduleapi.ExecutionLocalProcess ||
		result.AdapterIdentity != testLocalProcessAdapter {
		t.Fatalf("granted Resolve() result = %+v", result)
	}
	if probe.calls != 1 || probe.artifactDigest != testArtifactDigest ||
		probe.adapterIdentity != testLocalProcessAdapter {
		t.Fatalf(
			"probe calls/digest/adapter = %d/%q/%q",
			probe.calls,
			probe.artifactDigest,
			probe.adapterIdentity,
		)
	}
}

func TestLocalProcessGrantRejectsUnknownExactAdapter(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestLocalProcess,
		"content/mcp-server",
		testArtifactDigest,
	)
	probe := &recordingRegistryProbe{}
	resolver := mustResolverWithLocalProcess(
		t,
		[]LocalProcessGrant{testLocalProcessGrant(
			testArtifactDigest,
			testLocalProcessAdapter,
		)},
		probe,
	)
	if _, err := resolver.Resolve(
		context.Background(),
		testResolveInput(installation),
	); !errors.Is(err, ErrAdapterNotRegistered) {
		t.Fatalf("Resolve() error = %v, want adapter-not-registered", err)
	}
}

func TestRemoteActionRequiresExactCoreGrantAndRegisteredAdapter(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestRemote,
		"content/remote-action.json",
		testArtifactDigest,
	)
	probe := &recordingRegistryProbe{registered: true}
	ungranted := mustResolver(t, nil, probe)
	asserted := testResolveInput(installation)
	asserted.ExpectedExecutionClass = moduleapi.ExecutionRemote
	asserted.ExpectedAdapterIdentity = testRemoteActionAdapter
	_, err := ungranted.Resolve(
		context.Background(),
		asserted,
	)
	if !errors.Is(err, ErrRemoteActionModuleNotGranted) {
		t.Fatalf("ungranted Resolve() error = %v, want remote-action-not-granted", err)
	}
	if probe.calls != 0 {
		t.Fatalf("ungranted registry probe calls = %d, want 0", probe.calls)
	}

	resolver := mustResolverWithRemoteAction(
		t,
		[]RemoteActionGrant{testRemoteActionGrant(
			testArtifactDigest,
			testRemoteActionAdapter,
		)},
		probe,
	)
	result, err := resolver.Resolve(context.Background(), asserted)
	if err != nil {
		t.Fatalf("granted Resolve() error = %v", err)
	}
	if result.ExecutionClass != moduleapi.ExecutionRemote ||
		result.AdapterIdentity != testRemoteActionAdapter {
		t.Fatalf("granted Resolve() result = %+v", result)
	}
	if probe.calls != 1 || probe.artifactDigest != testArtifactDigest ||
		probe.adapterIdentity != testRemoteActionAdapter {
		t.Fatalf(
			"probe calls/digest/adapter = %d/%q/%q",
			probe.calls,
			probe.artifactDigest,
			probe.adapterIdentity,
		)
	}
}

func TestRemoteActionGrantRejectsArtifactDriftBeforeRegistryProbe(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestRemote,
		"content/remote-action.json",
		testDriftedArtifact,
	)
	probe := &recordingRegistryProbe{registered: true}
	resolver := mustResolverWithRemoteAction(
		t,
		[]RemoteActionGrant{testRemoteActionGrant(
			testArtifactDigest,
			testRemoteActionAdapter,
		)},
		probe,
	)
	if _, err := resolver.Resolve(
		context.Background(),
		testResolveInput(installation),
	); !errors.Is(err, ErrRemoteActionModuleNotGranted) {
		t.Fatalf("Resolve() error = %v, want remote-action-not-granted", err)
	}
	if probe.calls != 0 {
		t.Fatalf("registry probe calls = %d, want 0", probe.calls)
	}
}

func TestWASMActionRequiresExactCoreGrantAndRegisteredAdapter(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestWASM,
		"content/wasm-action.json",
		testArtifactDigest,
	)
	probe := &recordingRegistryProbe{registered: true}
	ungranted := mustResolver(t, nil, probe)
	asserted := testResolveInput(installation)
	asserted.ExpectedExecutionClass = moduleapi.ExecutionWASM
	asserted.ExpectedAdapterIdentity = testWASMActionAdapter
	_, err := ungranted.Resolve(context.Background(), asserted)
	if !errors.Is(err, ErrWASMActionModuleNotGranted) {
		t.Fatalf("ungranted Resolve() error = %v, want WASM-action-not-granted", err)
	}
	if probe.calls != 0 {
		t.Fatalf("ungranted registry probe calls = %d, want 0", probe.calls)
	}

	resolver := mustResolverWithWASMAction(
		t,
		[]WASMActionGrant{testWASMActionGrant(
			testArtifactDigest,
			testWASMActionAdapter,
		)},
		probe,
	)
	result, err := resolver.Resolve(context.Background(), asserted)
	if err != nil {
		t.Fatalf("granted Resolve() error = %v", err)
	}
	if result.ExecutionClass != moduleapi.ExecutionWASM ||
		result.AdapterIdentity != testWASMActionAdapter {
		t.Fatalf("granted Resolve() result = %+v", result)
	}
	if probe.calls != 1 || probe.artifactDigest != testArtifactDigest ||
		probe.adapterIdentity != testWASMActionAdapter {
		t.Fatalf(
			"probe calls/digest/adapter = %d/%q/%q",
			probe.calls,
			probe.artifactDigest,
			probe.adapterIdentity,
		)
	}
}

func TestRemoteRuntimeRejectsNonNarrowManifestBeforeRegistryProbe(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*moduleapi.ModuleManifestV1)
	}{
		{
			name: "non action provider",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Provides = []moduleapi.PortRef{{
					Name:         moduleapi.PortNameModelGenerate,
					ExactVersion: moduleapi.PortVersionV1,
				}}
			},
		},
		{
			name: "dependency request",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.Requires = []moduleapi.PortRef{{
					Name:         moduleapi.PortNameModelGenerate,
					ExactVersion: moduleapi.PortVersionV1,
				}}
			},
		},
		{
			name: "permission request",
			mutate: func(manifest *moduleapi.ModuleManifestV1) {
				manifest.RequestedPermissions = []moduleapi.Permission{"network.http"}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			installation := testInstallation(
				t,
				moduleapi.RuntimeModeRequestRemote,
				"content/remote-action.json",
				testArtifactDigest,
			)
			var manifest moduleapi.ModuleManifestV1
			if err := json.Unmarshal(installation.ManifestBytes, &manifest); err != nil {
				t.Fatal(err)
			}
			test.mutate(&manifest)
			payload, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			installation.ManifestBytes, err = moduleapi.CanonicalJSON(payload)
			if err != nil {
				t.Fatal(err)
			}
			installation.ManifestRef, err = currentstore.ComputeContentDigest(
				currentstore.ContentModuleManifest,
				moduleManifestMediaType,
				installation.ManifestBytes,
			)
			if err != nil {
				t.Fatal(err)
			}

			probe := &recordingRegistryProbe{registered: true}
			resolver := mustResolverWithRemoteAction(
				t,
				[]RemoteActionGrant{testRemoteActionGrant(
					testArtifactDigest,
					testRemoteActionAdapter,
				)},
				probe,
			)
			if _, err := resolver.Resolve(
				context.Background(),
				testResolveInput(installation),
			); !errors.Is(err, ErrRuntimeModeUnavailable) {
				t.Fatalf("Resolve() error = %v, want unavailable runtime mode", err)
			}
			if probe.calls != 0 {
				t.Fatalf("registry probe calls = %d, want 0", probe.calls)
			}
		})
	}
}

func TestRemoteActionGrantRejectsUnknownExactAdapter(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestRemote,
		"content/remote-action.json",
		testArtifactDigest,
	)
	probe := &recordingRegistryProbe{}
	resolver := mustResolverWithRemoteAction(
		t,
		[]RemoteActionGrant{testRemoteActionGrant(
			testArtifactDigest,
			testRemoteActionAdapter,
		)},
		probe,
	)
	if _, err := resolver.Resolve(
		context.Background(),
		testResolveInput(installation),
	); !errors.Is(err, ErrAdapterNotRegistered) {
		t.Fatalf("Resolve() error = %v, want adapter-not-registered", err)
	}
}

func TestNewRejectsInvalidRemoteActionGrant(t *testing.T) {
	grant := testRemoteActionGrant(testArtifactDigest, testRemoteActionAdapter)
	grant.Protocol = moduleapi.RuntimeProtocolMCPStdio20251125
	if _, err := New(Config{
		DeclarativeAdapterIdentity: testDeclarativeAdapter,
		RemoteActionGrants:         []RemoteActionGrant{grant},
	}, &recordingRegistryProbe{registered: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("New() error = %v, want invalid input", err)
	}
}

func TestActivationIDIsCanonicalDeterministicAndCoreOwned(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		testManifestEntrypoint,
		testArtifactDigest,
	)
	probe := &recordingRegistryProbe{registered: true}
	resolver := mustResolver(
		t,
		[]TrustedInProcessAllowlistEntry{testAllowlistEntry(
			testArtifactDigest,
			testTrustedAdapter,
		)},
		probe,
	)
	input := testResolveInput(installation)

	first, err := resolver.Resolve(context.Background(), input)
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}
	secondInput := input
	secondInput.ExpectedExecutionClass =
		moduleapi.ExecutionTrustedInProcess
	secondInput.ExpectedAdapterIdentity = testTrustedAdapter
	second, err := resolver.Resolve(context.Background(), secondInput)
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if first != second {
		t.Fatalf("deterministic results differ:\nfirst  %+v\nsecond %+v", first, second)
	}
	if !moduleapi.ValidSHA256(first.ActivationID) {
		t.Fatalf("ActivationID = %q, want lowercase SHA-256", first.ActivationID)
	}

	otherInput := input
	otherInput.InstanceID = "provider-secondary"
	other, err := resolver.Resolve(context.Background(), otherInput)
	if err != nil {
		t.Fatalf("changed-coordinate Resolve() error = %v", err)
	}
	if other.ActivationID == first.ActivationID {
		t.Fatal("different instance identity produced the same ActivationID")
	}
}

func TestExpectedAssignmentIsAssertionOnly(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestTrustedInProcess,
		testManifestEntrypoint,
		testArtifactDigest,
	)
	probe := &recordingRegistryProbe{registered: true}
	resolver := mustResolver(
		t,
		[]TrustedInProcessAllowlistEntry{testAllowlistEntry(
			testArtifactDigest,
			testTrustedAdapter,
		)},
		probe,
	)

	wrongClass := testResolveInput(installation)
	wrongClass.ExpectedExecutionClass = moduleapi.ExecutionDeclarative
	if _, err := resolver.Resolve(
		context.Background(),
		wrongClass,
	); !errors.Is(err, ErrAssertionMismatch) {
		t.Fatalf("wrong-class Resolve() error = %v, want assertion mismatch", err)
	}

	wrongAdapter := testResolveInput(installation)
	wrongAdapter.ExpectedAdapterIdentity = testManifestEntrypoint
	if _, err := resolver.Resolve(
		context.Background(),
		wrongAdapter,
	); !errors.Is(err, ErrAssertionMismatch) {
		t.Fatalf("wrong-adapter Resolve() error = %v, want assertion mismatch", err)
	}

	unallowlisted := mustResolver(t, nil, nil)
	asserted := testResolveInput(installation)
	asserted.ExpectedExecutionClass = moduleapi.ExecutionTrustedInProcess
	asserted.ExpectedAdapterIdentity = testManifestEntrypoint
	if _, err := unallowlisted.Resolve(
		context.Background(),
		asserted,
	); !errors.Is(err, ErrTrustedModuleNotAllowlisted) {
		t.Fatalf(
			"asserted unallowlisted Resolve() error = %v, want not-allowlisted",
			err,
		)
	}
}

func TestResolveStrictlyParsesInstallationManifest(t *testing.T) {
	installation := testInstallation(
		t,
		moduleapi.RuntimeModeRequestDeclarative,
		testDeclarativeContent,
		testArtifactDigest,
	)
	installation.ManifestBytes = append(
		append([]byte(nil), installation.ManifestBytes...),
		' ',
	)
	resolver := mustResolver(t, nil, nil)

	_, err := resolver.Resolve(
		context.Background(),
		testResolveInput(installation),
	)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Resolve() error = %v, want invalid input", err)
	}
}

type recordingRegistryProbe struct {
	registered      bool
	err             error
	calls           int
	artifactDigest  string
	adapterIdentity string
}

func (probe *recordingRegistryProbe) IsRegistered(
	_ context.Context,
	artifactDigest string,
	adapterIdentity string,
) (bool, error) {
	probe.calls++
	probe.artifactDigest = artifactDigest
	probe.adapterIdentity = adapterIdentity
	return probe.registered, probe.err
}

func mustResolver(
	t *testing.T,
	allowlist []TrustedInProcessAllowlistEntry,
	probe ExactAdapterRegistryProbe,
) *Resolver {
	t.Helper()
	resolver, err := New(
		Config{
			DeclarativeAdapterIdentity: testDeclarativeAdapter,
			TrustedInProcessAllowlist:  allowlist,
		},
		probe,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return resolver
}

func mustResolverWithLocalProcess(
	t *testing.T,
	grants []LocalProcessGrant,
	probe ExactAdapterRegistryProbe,
) *Resolver {
	t.Helper()
	resolver, err := New(
		Config{
			DeclarativeAdapterIdentity: testDeclarativeAdapter,
			LocalProcessGrants:         grants,
		},
		probe,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return resolver
}

func mustResolverWithRemoteAction(
	t *testing.T,
	grants []RemoteActionGrant,
	probe ExactAdapterRegistryProbe,
) *Resolver {
	t.Helper()
	resolver, err := New(
		Config{
			DeclarativeAdapterIdentity: testDeclarativeAdapter,
			RemoteActionGrants:         grants,
		},
		probe,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return resolver
}

func mustResolverWithWASMAction(
	t *testing.T,
	grants []WASMActionGrant,
	probe ExactAdapterRegistryProbe,
) *Resolver {
	t.Helper()
	resolver, err := New(
		Config{
			DeclarativeAdapterIdentity: testDeclarativeAdapter,
			WASMActionGrants:           grants,
		},
		probe,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return resolver
}

func testAllowlistEntry(
	artifactDigest string,
	adapterIdentity string,
) TrustedInProcessAllowlistEntry {
	return TrustedInProcessAllowlistEntry{
		ModuleID:        "example.provider",
		ExactVersion:    "1.2.3",
		ArtifactDigest:  artifactDigest,
		AdapterIdentity: adapterIdentity,
	}
}

func testLocalProcessGrant(
	artifactDigest string,
	adapterIdentity string,
) LocalProcessGrant {
	return LocalProcessGrant{
		ModuleID:        "example.provider",
		ExactVersion:    "1.2.3",
		ArtifactDigest:  artifactDigest,
		Protocol:        moduleapi.RuntimeProtocolMCPStdio20251125,
		AdapterIdentity: adapterIdentity,
	}
}

func testRemoteActionGrant(
	artifactDigest string,
	adapterIdentity string,
) RemoteActionGrant {
	return RemoteActionGrant{
		ModuleID:        "example.provider",
		ExactVersion:    "1.2.3",
		ArtifactDigest:  artifactDigest,
		Protocol:        moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
		AdapterIdentity: adapterIdentity,
	}
}

func testWASMActionGrant(
	artifactDigest string,
	adapterIdentity string,
) WASMActionGrant {
	return WASMActionGrant{
		ModuleID:        "example.provider",
		ExactVersion:    "1.2.3",
		ArtifactDigest:  artifactDigest,
		Protocol:        moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
		AdapterIdentity: adapterIdentity,
	}
}

func testResolveInput(
	installation currentstore.ModuleInstallation,
) ResolveInput {
	return ResolveInput{
		Installation:       installation,
		TenantID:           "tenant-one",
		InstanceID:         "provider-primary",
		ActivationRevision: 7,
	}
}

func testInstallation(
	t *testing.T,
	mode moduleapi.RuntimeModeRequest,
	entrypoint string,
	artifactDigest string,
) currentstore.ModuleInstallation {
	t.Helper()
	protocol := moduleapi.RuntimeProtocolStaticV1
	if mode == moduleapi.RuntimeModeRequestTrustedInProcess {
		protocol = moduleapi.RuntimeProtocolGoInProcessV1
	} else if mode == moduleapi.RuntimeModeRequestLocalProcess {
		protocol = moduleapi.RuntimeProtocolMCPStdio20251125
	} else if mode == moduleapi.RuntimeModeRequestRemote {
		protocol = moduleapi.RuntimeProtocolFreeAgentActionHTTPV1
	} else if mode == moduleapi.RuntimeModeRequestWASM {
		protocol = moduleapi.RuntimeProtocolFreeAgentActionWASMV1
	}
	providedPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameModelGenerate,
		ExactVersion: "1",
	}
	if mode == moduleapi.RuntimeModeRequestLocalProcess ||
		mode == moduleapi.RuntimeModeRequestRemote ||
		mode == moduleapi.RuntimeModeRequestWASM {
		providedPort = moduleapi.PortRef{
			Name:         moduleapi.PortNameActionProvider,
			ExactVersion: moduleapi.PortVersionV1,
		}
	}
	manifest := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "example.provider",
		Version:    "1.2.3",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       mode,
			Protocol:   protocol,
			Entrypoint: entrypoint,
		},
		Provides: []moduleapi.PortRef{providedPort},
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal(manifest) error = %v", err)
	}
	canonical, err := moduleapi.CanonicalJSON(payload)
	if err != nil {
		t.Fatalf("CanonicalJSON(manifest) error = %v", err)
	}
	if _, _, err := moduleapi.ParseModuleManifestV1(canonical); err != nil {
		t.Fatalf("ParseModuleManifestV1(fixture) error = %v", err)
	}
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		moduleManifestMediaType,
		canonical,
	)
	if err != nil {
		t.Fatalf("ComputeContentDigest(manifest) error = %v", err)
	}
	return currentstore.ModuleInstallation{
		InstallationID: "installation-one",
		ModuleID:       manifest.ID,
		ExactVersion:   manifest.Version,
		ManifestRef:    manifestRef,
		ManifestBytes:  canonical,
		ArtifactDigest: artifactDigest,
	}
}
