package remoteactionhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const testRemoteMaterial = "remote-action-test-secret"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type recordingResolver struct {
	mu       sync.Mutex
	values   map[string]string
	requests []SecretIdentityV1
	err      error
}

func (resolver *recordingResolver) ResolveSecret(
	_ context.Context,
	identity SecretIdentityV1,
) ([]byte, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	resolver.requests = append(resolver.requests, identity)
	if resolver.err != nil {
		return nil, resolver.err
	}
	value, found := resolver.values[identity.SecretRef]
	if !found {
		return nil, errors.New("secret absent")
	}
	return []byte(value), nil
}

func TestDescriptorArtifactValidationAndOfflineActionProvider(t *testing.T) {
	descriptor, descriptorCanonical := testDescriptor(t)
	manifestValue := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         "example.remote.action",
		Version:    "1.0.0",
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestRemote,
			Protocol:   moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
			Entrypoint: "content/actions.json",
		},
		Provides: []moduleapi.PortRef{actionProviderPortV1},
	}
	encodedManifest, err := json.Marshal(manifestValue)
	if err != nil {
		t.Fatal(err)
	}
	manifestCanonical, err := moduleapi.CanonicalJSON(encodedManifest)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		[]moduleapi.ArtifactFile{{
			Path:    "content/actions.json",
			Content: descriptorCanonical,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	extraCanonical := []byte(`{"unused":true}`)
	if err := os.WriteFile(
		filepath.Join(root, "content", "unused.json"),
		extraCanonical,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	digestWithExtra, err := moduleapi.ComputeArtifactDigest(
		manifestCanonical,
		[]moduleapi.ArtifactFile{
			{Path: "content/actions.json", Content: descriptorCanonical},
			{Path: "content/unused.json", Content: extraCanonical},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDescriptorFromArtifact(
		context.Background(),
		testProvider(digestWithExtra, "instance-a", 1),
		root,
		uint64(len(manifestCanonical)+len(descriptorCanonical)+len(extraCanonical)),
	); err == nil {
		t.Fatal("artifact with an extra covered file passed the exact two-file contract")
	}
	if err := os.Remove(filepath.Join(root, "content", "unused.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "content", "actions.json"),
		descriptorCanonical,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	provider := testProvider(digest, "instance-a", 1)
	loaded, err := LoadDescriptorFromArtifact(
		context.Background(),
		provider,
		root,
		uint64(len(manifestCanonical)+len(descriptorCanonical)),
	)
	if err != nil {
		t.Fatalf("LoadDescriptorFromArtifact: %v", err)
	}
	if len(loaded.Actions) != 1 ||
		loaded.Actions[0].ProviderActionID != descriptor.Actions[0].ProviderActionID {
		t.Fatalf("loaded descriptor = %+v", loaded)
	}
	manifestWithPermissions := manifestValue
	manifestWithPermissions.RequestedPermissions = []moduleapi.Permission{"network.http"}
	encodedManifestWithPermissions, err := json.Marshal(manifestWithPermissions)
	if err != nil {
		t.Fatal(err)
	}
	canonicalManifestWithPermissions, err := moduleapi.CanonicalJSON(
		encodedManifestWithPermissions,
	)
	if err != nil {
		t.Fatal(err)
	}
	digestWithPermissions, err := moduleapi.ComputeArtifactDigest(
		canonicalManifestWithPermissions,
		[]moduleapi.ArtifactFile{{
			Path:    "content/actions.json",
			Content: descriptorCanonical,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, moduleapi.ArtifactManifestPath),
		canonicalManifestWithPermissions,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDescriptorFromArtifact(
		context.Background(),
		testProvider(digestWithPermissions, "instance-a", 1),
		root,
		uint64(len(canonicalManifestWithPermissions)+len(descriptorCanonical)),
	); err == nil {
		t.Fatal("REMOTE manifest with requested permissions was accepted")
	}
	if err := os.WriteFile(
		filepath.Join(root, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "content", "actions.json"),
		append(bytes.Clone(descriptorCanonical), ' '),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDescriptorFromArtifact(
		context.Background(),
		provider,
		root,
		uint64(len(manifestCanonical)+len(descriptorCanonical)),
	); err == nil {
		t.Fatal("tampered descriptor passed full artifact verification")
	}
}

func TestDescribeAndPrepareAreOfflineAndGenericInvokeIsForbidden(t *testing.T) {
	descriptor, _ := testDescriptor(t)
	resolver := &recordingResolver{
		values: map[string]string{"secret.one": testRemoteMaterial},
	}
	var calls atomic.Int64
	adapter, err := newAdapterWithRoundTripper(
		testProvider(strings.Repeat("a", 64), "instance-a", 1),
		descriptor,
		resolver,
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("must not be called")
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	parameters := testParameters(t, "https://one.example.com/action", "secret.one")
	definitions, err := adapter.Describe(
		context.Background(),
		moduleapi.ActionDescribeRequestV1{
			SchemaVersion: moduleapi.ActionDescribeRequestSchemaV1,
			Parameters:    parameters,
		},
	)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("Describe definitions=%+v err=%v", definitions, err)
	}
	prepared, err := adapter.Prepare(
		context.Background(),
		moduleapi.ActionRequestV1{
			SchemaVersion:    moduleapi.ActionRequestSchemaV1,
			PublicActionID:   "example.echo",
			ProviderActionID: "example.echo",
			DefinitionDigest: strings.Repeat("d", 64),
			CanonicalInput:   json.RawMessage(`{"value":"hello"}`),
		},
	)
	if err != nil || !bytes.Equal(prepared, []byte(`{"value":"hello"}`)) {
		t.Fatalf("Prepare payload=%s err=%v", prepared, err)
	}
	if _, err := adapter.Invoke(
		context.Background(),
		modulehost.PreparedInvocation{},
	); !errors.Is(err, ErrGenericInvocation) {
		t.Fatalf("generic Invoke error = %v", err)
	}
	resolver.mu.Lock()
	resolved := len(resolver.requests)
	resolver.mu.Unlock()
	if calls.Load() != 0 || resolved != 0 {
		t.Fatalf("offline calls: HTTP=%d secret=%d", calls.Load(), resolved)
	}
}

func TestExecutePreparedUsesPerClosureEndpointAndSecretWithoutCrossTalk(t *testing.T) {
	descriptor, _ := testDescriptor(t)
	resolver := &recordingResolver{values: map[string]string{
		"secret.one": "credential-one",
		"secret.two": "credential-two",
	}}
	var mu sync.Mutex
	seen := map[string]string{}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		frozen, err := moduleapi.RestoreActionExecutionRequestV1(body)
		if err != nil {
			return nil, err
		}
		if request.Method != http.MethodPost || request.GetBody != nil ||
			!request.Close || request.Header.Get("Content-Type") != "application/json" ||
			request.Header.Get("Accept") != "application/json" {
			return nil, errors.New("request transport contract drifted")
		}
		mu.Lock()
		seen[request.URL.String()] = request.Header.Get("Authorization")
		mu.Unlock()
		_, result, err := moduleapi.NewActionExecutionResultV1(
			moduleapi.ActionExecutionResultV1{
				SchemaVersion:       moduleapi.ActionExecutionResultSchemaV1,
				AttemptID:           frozen.AttemptID,
				Outcome:             moduleapi.ActionExecutionSucceeded,
				CanonicalResult:     json.RawMessage(`{"ok":true}`),
				ExternalOperationID: "operation-" + frozen.AttemptID,
			},
		)
		if err != nil {
			return nil, err
		}
		return jsonResponse(http.StatusOK, result), nil
	})
	adapter, err := newAdapterWithRoundTripper(
		testProvider(strings.Repeat("a", 64), "template", 1),
		descriptor,
		resolver,
		transport,
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		attempt, instance, endpoint, ref string
		revision                         uint64
	}{
		{"attempt-one", "instance-one", "https://one.example.com/action", "secret.one", 2},
		{"attempt-two", "instance-two", "https://two.example.com/action", "secret.two", 7},
	}
	var wait sync.WaitGroup
	errorsFound := make(chan error, len(tests))
	for _, test := range tests {
		test := test
		execution := testExecution(
			t,
			testProvider(strings.Repeat("a", 64), test.instance, test.revision),
			test.attempt,
			test.endpoint,
			test.ref,
		)
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := adapter.ExecutePrepared(
				context.Background(),
				execution,
			)
			if err != nil || result.Outcome != moduleapi.ActionExecutionSucceeded {
				errorsFound <- errors.New("execution did not succeed")
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	scheme := "Bearer "
	if seen[tests[0].endpoint] != scheme+"credential-one" ||
		seen[tests[1].endpoint] != scheme+"credential-two" || len(seen) != 2 {
		t.Fatalf("per-closure targets crossed: %+v", seen)
	}
}

func TestExecutePreparedFailureAndUnknownBoundaryNeverReplays(t *testing.T) {
	descriptor, _ := testDescriptor(t)
	tests := []struct {
		name        string
		endpoint    string
		resolverErr error
		response    func(string) (*http.Response, error)
		want        moduleapi.ActionExecutionOutcomeV1
	}{
		{
			name:     "private literal denied before POST",
			endpoint: "https://127.0.0.1/action",
			want:     moduleapi.ActionExecutionFailed,
		},
		{
			name:        "secret unavailable before POST",
			endpoint:    "https://api.example.com/action",
			resolverErr: errors.New("secret text must be sanitized"),
			want:        moduleapi.ActionExecutionFailed,
		},
		{
			name:     "transport ambiguity",
			endpoint: "https://api.example.com/action",
			response: func(string) (*http.Response, error) {
				return nil, errors.New("write may have completed")
			},
			want: moduleapi.ActionExecutionUnknown,
		},
		{
			name:     "redirect forbidden",
			endpoint: "https://api.example.com/action",
			response: func(string) (*http.Response, error) {
				response := jsonResponse(http.StatusFound, []byte(`{}`))
				response.Header.Set("Location", "https://other.example.com/action")
				return response, nil
			},
			want: moduleapi.ActionExecutionUnknown,
		},
		{
			name:     "malformed response",
			endpoint: "https://api.example.com/action",
			response: func(string) (*http.Response, error) {
				return jsonResponse(http.StatusOK, []byte(`{"outcome":"SUCCEEDED"}`)), nil
			},
			want: moduleapi.ActionExecutionUnknown,
		},
		{
			name:     "secret echo discarded",
			endpoint: "https://api.example.com/action",
			response: func(attempt string) (*http.Response, error) {
				return jsonResponse(http.StatusOK, canonicalResult(t, moduleapi.ActionExecutionResultV1{
					SchemaVersion:   moduleapi.ActionExecutionResultSchemaV1,
					AttemptID:       attempt,
					Outcome:         moduleapi.ActionExecutionSucceeded,
					CanonicalResult: json.RawMessage(`{"value":"` + testRemoteMaterial + `"}`),
				})), nil
			},
			want: moduleapi.ActionExecutionUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &recordingResolver{
				values: map[string]string{"secret.one": testRemoteMaterial},
				err:    test.resolverErr,
			}
			var calls atomic.Int64
			adapter, err := newAdapterWithRoundTripper(
				testProvider(strings.Repeat("a", 64), "instance", 1),
				descriptor,
				resolver,
				roundTripFunc(func(request *http.Request) (*http.Response, error) {
					calls.Add(1)
					body, _ := io.ReadAll(request.Body)
					frozen, _ := moduleapi.RestoreActionExecutionRequestV1(body)
					return test.response(frozen.AttemptID)
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.ExecutePrepared(
				context.Background(),
				testExecution(
					t,
					testProvider(strings.Repeat("a", 64), "instance", 1),
					"attempt-boundary",
					test.endpoint,
					"secret.one",
				),
			)
			if err != nil || result.Outcome != test.want {
				t.Fatalf("result=%+v err=%v want=%s", result, err, test.want)
			}
			wantCalls := int64(1)
			if test.response == nil {
				wantCalls = 0
			}
			if calls.Load() != wantCalls {
				t.Fatalf("RoundTrip calls=%d want=%d", calls.Load(), wantCalls)
			}
		})
	}
}

func TestPublicNetworkDialerRejectsPrivateOrMixedDNSBeforeDial(t *testing.T) {
	tests := []struct {
		name      string
		addresses []net.IPAddr
		wantDial  bool
	}{
		{name: "private", addresses: []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}},
		{name: "documentation", addresses: []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}}},
		{name: "mixed", addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}},
		{name: "public", addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, wantDial: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var dialed atomic.Int64
			dialer := &publicNetworkDialer{
				lookupIPAddr: func(context.Context, string) ([]net.IPAddr, error) {
					return test.addresses, nil
				},
				dialContext: func(context.Context, string, string) (net.Conn, error) {
					dialed.Add(1)
					return nil, errors.New("test dial stop")
				},
			}
			_, err := dialer.DialContext(
				context.Background(),
				"tcp",
				"api.example.com:443",
			)
			if err == nil {
				t.Fatal("DialContext unexpectedly succeeded")
			}
			want := int64(0)
			if test.wantDial {
				want = 1
			}
			if dialed.Load() != want {
				t.Fatalf("dial calls=%d want=%d", dialed.Load(), want)
			}
		})
	}
}

func testDescriptor(t *testing.T) (DescriptorV1, []byte) {
	t.Helper()
	schema, err := moduleapi.CanonicalJSON([]byte(
		`{"additionalProperties":false,"properties":{"value":{"maxLength":4096,"type":"string"}},"required":["value"],"type":"object"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	descriptor, canonical, err := NewDescriptorV1(DescriptorV1{
		SchemaVersion: DescriptorSchemaV1,
		Actions: []moduleapi.ActionDefinitionV1{{
			ProviderActionID:        "example.echo",
			Description:             "Return one value through the remote Action endpoint.",
			InputSchema:             schema,
			RequestedEffectClass:    moduleapi.EffectReadOnly,
			RequestedMaxResultBytes: 256,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return descriptor, canonical
}

func testProvider(
	digest string,
	instance string,
	revision uint64,
) moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           "example.remote.action",
		Version:            "1.0.0",
		ArtifactDigest:     digest,
		InstanceID:         instance,
		ExecutionClass:     moduleapi.ExecutionRemote,
		AdapterIdentity:    AdapterIdentityV1,
		ActivationRevision: revision,
	}
}

func testParameters(t *testing.T, endpoint string, secretRef string) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewRemoteActionHTTPBindingParametersV1(
		moduleapi.RemoteActionHTTPBindingParametersV1{
			SchemaVersion: moduleapi.RemoteActionHTTPBindingParametersSchemaV1,
			EndpointURL:   endpoint,
			SecretRef:     secretRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func testExecution(
	t *testing.T,
	provider moduleapi.ActivatedModuleRef,
	attempt string,
	endpoint string,
	secretRef string,
) modulehost.PreparedActionExecutionV1 {
	t.Helper()
	parameters := testParameters(t, endpoint, secretRef)
	_, config, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "example.echo",
				ProviderActionID: "example.echo",
				LocalEffectClass: moduleapi.EffectReadOnly,
				MaxResultBytes:   256,
			}},
			Parameters: parameters,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 "tenant-one",
			AllowedWorkspaceIDs:      []string{"workspace-one"},
			AllowedProviderActionIDs: []string{"example.echo"},
			MaxEffectClass:           moduleapi.EffectReadOnly,
			MaxResultBytes:           256,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := moduleapi.PortBinding{
		Provider:            provider,
		ConfigRef:           testContentRef("CONFIG", config),
		AuthorityCeilingRef: testContentRef("AUTHORITY_CEILING", authority),
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	request, _, err := moduleapi.NewActionExecutionRequestV1(
		moduleapi.ActionExecutionRequestV1{
			SchemaVersion:    moduleapi.ActionExecutionRequestSchemaV1,
			AttemptID:        attempt,
			PublicActionID:   "example.echo",
			ProviderActionID: "example.echo",
			DefinitionDigest: strings.Repeat("d", 64),
			MaxResultBytes:   256,
			PreparedPayload:  json.RawMessage(`{"value":"hello"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return modulehost.PreparedActionExecutionV1{
		Request:            request,
		Binding:            binding,
		ConfigCanonical:    config,
		AuthorityCanonical: authority,
	}
}

func testContentRef(kind string, canonical []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("freeagent.content-record/v1\x00"))
	_, _ = digest.Write([]byte(kind))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte("application/json"))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(canonical)
	return hex.EncodeToString(digest.Sum(nil))
}

func canonicalResult(
	t *testing.T,
	result moduleapi.ActionExecutionResultV1,
) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewActionExecutionResultV1(result)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func jsonResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}
