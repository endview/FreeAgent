package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleApplyRemoteModuleIDV1           = "example.remote.action"
	moduleApplyRemoteVersionV1            = "1.0.0"
	moduleApplyRemoteInstanceIDV1         = "remote-action-instance"
	moduleApplyRemoteActionIDV1           = "example.echo"
	moduleApplyRemoteEndpointV1           = "https://api.example.com/actions"
	moduleApplyRemoteAuthorityReferenceV1 = "secret.remote-action-test"
)

type moduleApplyRemoteFixtureV1 struct {
	ArtifactDirectory string
	ArtifactDigest    string
	ArtifactSizeBytes uint64
	EndpointURL       string
	SecretRef         string
}

type moduleApplyRemoteGrantsV1 struct {
	Artifact string
	Endpoint string
	Secret   string
}

func TestModuleApplyRemoteActionPlanAndTransientGrantsV1FailClosed(t *testing.T) {
	t.Parallel()

	fixture := newModuleApplyRemoteFixtureV1(t, t.TempDir(), nil)
	canonical := newModuleApplyRemotePlanV1(
		t,
		fixture,
		1,
		moduleApplyRemoteActionIDV1,
		moduleapi.EffectReadOnly,
		256,
	)
	plan, _, _, err := restoreModuleApplyPlanV1(canonical)
	if err != nil {
		t.Fatalf("restore REMOTE Action plan: %v", err)
	}
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil {
		t.Fatalf("resolve REMOTE Action handler: %v", err)
	}
	if policy.HandlerKind != moduleApplyHandlerRemoteActionHTTPV1 ||
		policy.ExecutionClass != moduleapi.ExecutionRemote ||
		policy.AdapterIdentity != remoteactionhttp.AdapterIdentityV1 ||
		!policy.RequiresRemoteActionArtifactGrant {
		t.Fatalf("REMOTE Action policy=%+v", policy)
	}

	if err := validateModuleApplyArtifactGrantsV1(
		policy,
		fixture.ArtifactDigest,
		"",
		"",
		fixture.ArtifactDigest,
		"",
	); err != nil {
		t.Fatalf("exact REMOTE artifact grant: %v", err)
	}
	if err := validateModuleApplyArtifactGrantsV1(
		policy,
		fixture.ArtifactDigest,
		"",
		"",
		"",
		"",
	); !errors.Is(err, errModuleApplyArtifactGrantRequiredV1) {
		t.Fatalf("missing REMOTE artifact grant=%v", err)
	}
	if err := validateModuleApplyArtifactGrantsV1(
		policy,
		fixture.ArtifactDigest,
		"",
		fixture.ArtifactDigest,
		fixture.ArtifactDigest,
		"",
	); !errors.Is(err, errModuleApplyArtifactGrantFlagsV1) {
		t.Fatalf("mixed REMOTE/trusted artifact grants=%v", err)
	}
	if err := validateModuleApplyRemoteActionGrantsV1(
		plan,
		policy,
		fixture.EndpointURL,
		fixture.SecretRef,
	); err != nil {
		t.Fatalf("exact REMOTE endpoint/SecretRef grants: %v", err)
	}
	otherReference := "secret.other"
	for _, test := range []struct {
		name     string
		endpoint string
		secret   string
	}{
		{name: "missing endpoint", secret: fixture.SecretRef},
		{name: "wrong endpoint", endpoint: "https://other.example.com/actions", secret: fixture.SecretRef},
		{name: "missing SecretRef", endpoint: fixture.EndpointURL},
		{name: "wrong SecretRef", endpoint: fixture.EndpointURL, secret: otherReference},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateModuleApplyRemoteActionGrantsV1(
				plan,
				policy,
				test.endpoint,
				test.secret,
			); !errors.Is(err, errModuleApplyRemoteGrantRequiredV1) {
				t.Fatalf("grant error=%v", err)
			}
		})
	}

	ordinary := moduleApplyProtocolHandlerTableV1()[4]
	if err := validateModuleApplyRemoteActionGrantsV1(
		plan,
		ordinary,
		fixture.EndpointURL,
		fixture.SecretRef,
	); !errors.Is(err, errModuleApplyRemoteGrantFlagsV1) {
		t.Fatalf("ordinary handler accepted REMOTE grants: %v", err)
	}
	if _, err := resolveModuleApplyBindingPolicyV1(
		productionActionPort,
		defaultTenantID,
		*plan.Binding,
		moduleApplyExpectedRuntimeRequestV1{
			Mode:     moduleapi.RuntimeModeRequestLocalProcess,
			Protocol: moduleapi.RuntimeProtocolMCPStdio20251125,
		},
		plan.Module,
	); err == nil || !strings.Contains(err.Error(), "non-REMOTE") {
		t.Fatalf("ordinary Action handler accepted REMOTE parameters: %v", err)
	}

	emptyConfig := newModuleApplyRemoteActionConfigV1(
		t,
		moduleApplyRemoteActionIDV1,
		moduleapi.EffectReadOnly,
		256,
		[]byte(`{}`),
	)
	emptyBinding := *plan.Binding
	emptyBinding.Config = emptyConfig
	if _, err := resolveModuleApplyBindingPolicyV1(
		productionActionPort,
		defaultTenantID,
		emptyBinding,
		plan.Module.ExpectedRuntimeRequest,
		plan.Module,
	); err == nil || !strings.Contains(err.Error(), "REMOTE Action parameters") {
		t.Fatalf("REMOTE handler accepted empty parameters: %v", err)
	}
}

func TestModuleApplyRemoteActionDryRunApplyRetryAndDisableStayOffline(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.db")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyRemoteFixtureV1(t, root, nil)
	planCanonical := newModuleApplyRemotePlanV1(
		t,
		fixture,
		1,
		moduleApplyRemoteActionIDV1,
		moduleapi.EffectReadOnly,
		256,
	)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "remote-enable.json"),
		planCanonical,
	)
	exactGrants := moduleApplyRemoteGrantsV1{
		Artifact: fixture.ArtifactDigest,
		Endpoint: fixture.EndpointURL,
		Secret:   fixture.SecretRef,
	}
	beforeRows := moduleApplyMutationRowCountsV1(t, databasePath)
	beforeAttempts := moduleApplyRemoteDispatchAttemptCountV1(t, databasePath)

	_, _, err := runModuleApplyRemoteFixtureV1(
		false,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		moduleApplyRemoteGrantsV1{
			Endpoint: fixture.EndpointURL,
			Secret:   fixture.SecretRef,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "GRANT_REQUIRED") {
		t.Fatalf("missing artifact grant error=%v", err)
	}
	if after := moduleApplyMutationRowCountsV1(t, databasePath); !reflect.DeepEqual(after, beforeRows) {
		t.Fatalf("missing grant mutated Store: before=%v after=%v", beforeRows, after)
	}
	if got := moduleApplyRemoteDispatchAttemptCountV1(t, databasePath); got != beforeAttempts {
		t.Fatalf("missing grant created dispatch attempts: got=%d want=%d", got, beforeAttempts)
	}
	if _, err := os.Lstat(filepath.Join(artifactRoot, fixture.ArtifactDigest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing grant published artifact: %v", err)
	}

	_, _, err = runModuleApplyRemoteFixtureV1(
		false,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		moduleApplyRemoteGrantsV1{
			Artifact: fixture.ArtifactDigest,
			Secret:   fixture.SecretRef,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "GRANT_REQUIRED") {
		t.Fatalf("missing endpoint grant error=%v", err)
	}
	if after := moduleApplyMutationRowCountsV1(t, databasePath); !reflect.DeepEqual(after, beforeRows) {
		t.Fatalf("missing endpoint grant mutated Store: before=%v after=%v", beforeRows, after)
	}
	if got := moduleApplyRemoteDispatchAttemptCountV1(t, databasePath); got != beforeAttempts {
		t.Fatalf("missing endpoint grant created dispatch attempts: got=%d want=%d", got, beforeAttempts)
	}

	dryPayload, dryResult, err := runModuleApplyRemoteFixtureV1(
		true,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		exactGrants,
	)
	var dryProjection moduleDryRunResultV1
	decodeErr := decodeModuleApplyRemoteResultV1(dryPayload, &dryProjection)
	if err != nil || decodeErr != nil ||
		dryResult.Status != moduleApplyStatusWouldApply ||
		dryProjection.Changes.Activation != moduleDryRunActivationCreateV1 {
		t.Fatalf("REMOTE dry-run result=%+v err=%v", dryResult, err)
	}
	assertModuleApplyRemoteGrantsNotOutputV1(t, dryPayload, fixture)
	if after := moduleApplyMutationRowCountsV1(t, databasePath); !reflect.DeepEqual(after, beforeRows) {
		t.Fatalf("REMOTE dry-run mutated Store: before=%v after=%v", beforeRows, after)
	}
	if got := moduleApplyRemoteDispatchAttemptCountV1(t, databasePath); got != beforeAttempts {
		t.Fatalf("REMOTE dry-run created dispatch attempts: got=%d want=%d", got, beforeAttempts)
	}
	if _, err := os.Lstat(filepath.Join(artifactRoot, fixture.ArtifactDigest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("REMOTE dry-run published artifact: %v", err)
	}

	applyPayload, applyResult, err := runModuleApplyRemoteFixtureV1(
		false,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		exactGrants,
	)
	if err != nil || applyResult.Status != moduleApplyStatusApplied ||
		applyResult.PointerRevision != 2 {
		t.Fatalf("REMOTE apply result=%+v err=%v", applyResult, err)
	}
	assertModuleApplyRemoteGrantsNotOutputV1(t, applyPayload, fixture)
	activation := readModuleApplyRemoteActivationV1(t, databasePath)
	if activation.ExecutionClass != moduleapi.ExecutionRemote ||
		activation.AdapterIdentity != remoteactionhttp.AdapterIdentityV1 ||
		activation.InstallationID != moduleApplyInstallationPrefix+fixture.ArtifactDigest {
		t.Fatalf("REMOTE activation=%+v", activation)
	}
	if got := moduleApplyRemoteDispatchAttemptCountV1(t, databasePath); got != beforeAttempts {
		t.Fatalf("REMOTE Apply created dispatch attempts: got=%d want=%d", got, beforeAttempts)
	}
	rowsBeforeRetry := moduleApplyMutationRowCountsV1(t, databasePath)

	_, retryResult, err := runModuleApplyRemoteFixtureV1(
		false,
		databasePath,
		artifactRoot,
		planPath,
		filepath.Join(root, "source-does-not-exist"),
		exactGrants,
	)
	if err != nil || retryResult.Status != moduleApplyStatusAlreadyApplied ||
		retryResult.PointerRevision != 2 {
		t.Fatalf("REMOTE exact retry result=%+v err=%v", retryResult, err)
	}
	if rowsAfterRetry := moduleApplyMutationRowCountsV1(t, databasePath); !reflect.DeepEqual(rowsAfterRetry, rowsBeforeRetry) {
		t.Fatalf("REMOTE exact retry changed Store rows: before=%v after=%v", rowsBeforeRetry, rowsAfterRetry)
	}
	if got := moduleApplyRemoteDispatchAttemptCountV1(t, databasePath); got != beforeAttempts {
		t.Fatalf("REMOTE exact retry created dispatch attempts: got=%d want=%d", got, beforeAttempts)
	}

	disablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "remote-disable.json"),
		newModuleApplyRemoteDisablePlanV1(t, 2),
	)
	_, _, err = runModuleApplyRemoteFixtureV1(
		false,
		databasePath,
		artifactRoot,
		disablePath,
		"",
		moduleApplyRemoteGrantsV1{Artifact: fixture.ArtifactDigest},
	)
	if err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
		t.Fatalf("DISABLED accepted transient REMOTE grant: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(artifactRoot, fixture.ArtifactDigest)); err != nil {
		t.Fatalf("remove final REMOTE artifact before emergency Disable: %v", err)
	}
	_, disabled, err := runModuleApplyRemoteFixtureV1(
		false,
		databasePath,
		artifactRoot,
		disablePath,
		"",
		moduleApplyRemoteGrantsV1{},
	)
	if err != nil || disabled.Status != moduleApplyStatusApplied ||
		disabled.PointerRevision != 3 {
		t.Fatalf("REMOTE disable result=%+v err=%v", disabled, err)
	}
	assertModuleApplyRemoteInstanceAbsentV1(t, databasePath)
}

func TestModuleApplyRemoteActionExactRetryRejectsTamperedFinalArtifact(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.db")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyRemoteFixtureV1(t, root, nil)
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "remote-enable.json"),
		newModuleApplyRemotePlanV1(
			t,
			fixture,
			1,
			moduleApplyRemoteActionIDV1,
			moduleapi.EffectReadOnly,
			256,
		),
	)
	grants := moduleApplyRemoteGrantsV1{
		Artifact: fixture.ArtifactDigest,
		Endpoint: fixture.EndpointURL,
		Secret:   fixture.SecretRef,
	}
	if _, result, err := runModuleApplyRemoteFixtureV1(
		false,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		grants,
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("initial REMOTE apply result=%+v err=%v", result, err)
	}
	rowsBeforeRetry := moduleApplyMutationRowCountsV1(t, databasePath)
	attemptsBeforeRetry := moduleApplyRemoteDispatchAttemptCountV1(t, databasePath)
	descriptorPath := filepath.Join(
		artifactRoot,
		fixture.ArtifactDigest,
		"content",
		"actions.json",
	)
	descriptor, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(descriptorPath, append(descriptor, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = runModuleApplyRemoteFixtureV1(
		false,
		databasePath,
		artifactRoot,
		planPath,
		filepath.Join(root, "source-does-not-exist"),
		grants,
	)
	if err == nil || !strings.Contains(err.Error(), "ARTIFACT_INVALID") {
		t.Fatalf("tampered final artifact exact retry error=%v", err)
	}
	if rowsAfterRetry := moduleApplyMutationRowCountsV1(t, databasePath); !reflect.DeepEqual(rowsAfterRetry, rowsBeforeRetry) {
		t.Fatalf("tampered exact retry changed Store rows: before=%v after=%v", rowsBeforeRetry, rowsAfterRetry)
	}
	if attemptsAfterRetry := moduleApplyRemoteDispatchAttemptCountV1(t, databasePath); attemptsAfterRetry != attemptsBeforeRetry {
		t.Fatalf("tampered exact retry created dispatch attempts: before=%d after=%d", attemptsBeforeRetry, attemptsAfterRetry)
	}
}

func TestModuleApplyRemoteActionArtifactVerificationFailsBeforePublication(t *testing.T) {
	tests := []struct {
		name          string
		permissions   []moduleapi.Permission
		providerID    string
		wantSubstring string
	}{
		{
			name:          "manifest requested permission",
			permissions:   []moduleapi.Permission{"network.http"},
			providerID:    moduleApplyRemoteActionIDV1,
			wantSubstring: "ARTIFACT_INVALID",
		},
		{
			name:          "mapping absent from descriptor",
			providerID:    "example.missing",
			wantSubstring: "ARTIFACT_INVALID",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "current.db")
			artifactRoot := filepath.Join(root, "artifacts")
			initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
			fixture := newModuleApplyRemoteFixtureV1(t, root, test.permissions)
			planPath := writeModuleApplyPlanFixtureV1(
				t,
				filepath.Join(root, "remote-invalid.json"),
				newModuleApplyRemotePlanV1(
					t,
					fixture,
					1,
					test.providerID,
					moduleapi.EffectReadOnly,
					256,
				),
			)
			before := moduleApplyMutationRowCountsV1(t, databasePath)
			for _, dryRun := range []bool{true, false} {
				mode := "apply"
				if dryRun {
					mode = "dry-run"
				}
				t.Run(mode, func(t *testing.T) {
					_, _, err := runModuleApplyRemoteFixtureV1(
						dryRun,
						databasePath,
						artifactRoot,
						planPath,
						fixture.ArtifactDirectory,
						moduleApplyRemoteGrantsV1{
							Artifact: fixture.ArtifactDigest,
							Endpoint: fixture.EndpointURL,
							Secret:   fixture.SecretRef,
						},
					)
					if err == nil || !strings.Contains(err.Error(), test.wantSubstring) {
						t.Fatalf("invalid REMOTE artifact error=%v", err)
					}
					if after := moduleApplyMutationRowCountsV1(t, databasePath); !reflect.DeepEqual(after, before) {
						t.Fatalf("invalid artifact mutated Store: before=%v after=%v", before, after)
					}
					if _, err := os.Lstat(filepath.Join(artifactRoot, fixture.ArtifactDigest)); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("invalid REMOTE artifact was published: %v", err)
					}
				})
			}
		})
	}
}

func newModuleApplyRemoteFixtureV1(
	t *testing.T,
	root string,
	permissions []moduleapi.Permission,
) moduleApplyRemoteFixtureV1 {
	t.Helper()
	inputSchema, err := moduleapi.CanonicalJSON([]byte(
		`{"additionalProperties":false,"properties":{"value":{"maxLength":4096,"type":"string"}},"required":["value"],"type":"object"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	_, descriptorCanonical, err := remoteactionhttp.NewDescriptorV1(
		remoteactionhttp.DescriptorV1{
			SchemaVersion: remoteactionhttp.DescriptorSchemaV1,
			Actions: []moduleapi.ActionDefinitionV1{{
				ProviderActionID:        moduleApplyRemoteActionIDV1,
				Description:             "Return one value through a remote Action endpoint.",
				InputSchema:             inputSchema,
				RequestedEffectClass:    moduleapi.EffectReadOnly,
				RequestedMaxResultBytes: 256,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	manifestValue := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         moduleApplyRemoteModuleIDV1,
		Version:    moduleApplyRemoteVersionV1,
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       moduleapi.RuntimeModeRequestRemote,
			Protocol:   moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
			Entrypoint: "content/actions.json",
		},
		Provides: []moduleapi.PortRef{productionActionPort},
		RequestedPermissions: append(
			[]moduleapi.Permission{},
			permissions...,
		),
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
	artifactDirectory := filepath.Join(root, "remote-source-"+digest[:12])
	if err := os.MkdirAll(filepath.Join(artifactDirectory, "content"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(artifactDirectory, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(artifactDirectory, "content", "actions.json"),
		descriptorCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return moduleApplyRemoteFixtureV1{
		ArtifactDirectory: artifactDirectory,
		ArtifactDigest:    digest,
		ArtifactSizeBytes: uint64(len(manifestCanonical) + len(descriptorCanonical)),
		EndpointURL:       moduleApplyRemoteEndpointV1,
		SecretRef:         moduleApplyRemoteAuthorityReferenceV1,
	}
}

func newModuleApplyRemotePlanV1(
	t *testing.T,
	fixture moduleApplyRemoteFixtureV1,
	expectedPointer uint64,
	providerActionID string,
	effect moduleapi.EffectClass,
	maxResultBytes uint32,
) []byte {
	t.Helper()
	_, parameters, err := moduleapi.NewRemoteActionHTTPBindingParametersV1(
		moduleapi.RemoteActionHTTPBindingParametersV1{
			SchemaVersion: moduleapi.RemoteActionHTTPBindingParametersSchemaV1,
			EndpointURL:   fixture.EndpointURL,
			SecretRef:     fixture.SecretRef,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	config := newModuleApplyRemoteActionConfigV1(
		t,
		providerActionID,
		effect,
		maxResultBytes,
		parameters,
	)
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 defaultTenantID,
			AllowedWorkspaceIDs:      []string{defaultWorkspaceID},
			AllowedProviderActionIDs: []string{providerActionID},
			MaxEffectClass:           effect,
			MaxResultBytes:           maxResultBytes,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(moduleApplyTestProfileID),
		"instance_id":               moduleApplyRemoteInstanceIDV1,
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  moduleApplyRemoteModuleIDV1,
			"exact_version":       moduleApplyRemoteVersionV1,
			"artifact_digest":     fixture.ArtifactDigest,
			"artifact_size_bytes": fixture.ArtifactSizeBytes,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestRemote),
				"protocol": moduleapi.RuntimeProtocolFreeAgentActionHTTPV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": 0,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
	})
}

func newModuleApplyRemoteActionConfigV1(
	t *testing.T,
	providerActionID string,
	effect moduleapi.EffectClass,
	maxResultBytes uint32,
	parameters []byte,
) []byte {
	t.Helper()
	_, canonical, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "remote.echo",
				ProviderActionID: providerActionID,
				LocalEffectClass: effect,
				MaxResultBytes:   maxResultBytes,
			}},
			Parameters: bytes.Clone(parameters),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func newModuleApplyRemoteDisablePlanV1(
	t *testing.T,
	expectedPointer uint64,
) []byte {
	t.Helper()
	return canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyDisabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": expectedPointer,
		"binding_target":            moduleApplyProfileBindingTargetTestValue(moduleApplyTestProfileID),
		"instance_id":               moduleApplyRemoteInstanceIDV1,
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
	})
}

func runModuleApplyRemoteFixtureV1(
	dryRun bool,
	databasePath string,
	artifactRoot string,
	planPath string,
	artifactDirectory string,
	grants moduleApplyRemoteGrantsV1,
) ([]byte, moduleApplyResultV1, error) {
	args := []string{
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--plan", planPath,
	}
	if artifactDirectory != "" {
		args = append(args, "--artifact", artifactDirectory)
	}
	if grants.Artifact != "" {
		args = append(args, "--allow-remote-action-artifact", grants.Artifact)
	}
	if grants.Endpoint != "" {
		args = append(args, "--allow-remote-action-endpoint", grants.Endpoint)
	}
	if grants.Secret != "" {
		args = append(args, "--allow-remote-action-secret-ref", grants.Secret)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var err error
	if dryRun {
		err = runModuleDryRun(context.Background(), args, &stdout, &stderr)
	} else {
		err = runModuleApply(context.Background(), args, &stdout, &stderr)
	}
	if stderr.Len() != 0 {
		return nil, moduleApplyResultV1{}, errors.Join(
			err,
			errors.New("module command unexpectedly wrote stderr: "+stderr.String()),
		)
	}
	if err != nil {
		if stdout.Len() != 0 {
			return nil, moduleApplyResultV1{}, errors.Join(
				err,
				errors.New("failed module command unexpectedly wrote stdout"),
			)
		}
		return nil, moduleApplyResultV1{}, err
	}
	payload := bytes.Clone(stdout.Bytes())
	if dryRun {
		var result moduleDryRunResultV1
		if err := decodeModuleApplyRemoteResultV1(payload, &result); err != nil {
			return nil, moduleApplyResultV1{}, err
		}
		return payload, moduleApplyResultV1{
			Status:          result.Status,
			PointerRevision: result.CandidateBasis.PointerRevision,
		}, nil
	}
	var result moduleApplyResultV1
	if err := decodeModuleApplyRemoteResultV1(payload, &result); err != nil {
		return nil, moduleApplyResultV1{}, err
	}
	return payload, result, nil
}

func decodeModuleApplyRemoteResultV1(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("module command result has trailing JSON")
	}
	return nil
}

func assertModuleApplyRemoteGrantsNotOutputV1(
	t *testing.T,
	payload []byte,
	fixture moduleApplyRemoteFixtureV1,
) {
	t.Helper()
	if bytes.Contains(payload, []byte(fixture.EndpointURL)) ||
		bytes.Contains(payload, []byte(fixture.SecretRef)) {
		t.Fatalf("transient REMOTE grants leaked to command output: %s", payload)
	}
}

func readModuleApplyRemoteActivationV1(
	t *testing.T,
	databasePath string,
) currentstore.ModuleActivation {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	activation, readErr := store.GetLatestModuleActivationForInstance(
		ctx,
		defaultTenantID,
		moduleApplyRemoteInstanceIDV1,
	)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read REMOTE activation: %v", errors.Join(readErr, closeErr))
	}
	return activation
}

func assertModuleApplyRemoteInstanceAbsentV1(
	t *testing.T,
	databasePath string,
) {
	t.Helper()
	ctx := context.Background()
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, _, catalog, readErr := store.LoadPublishedBasis(ctx, defaultTenantID)
	_, found := catalog.FindInstance(moduleApplyRemoteInstanceIDV1)
	closeErr := store.Close()
	if readErr != nil || closeErr != nil || found {
		t.Fatalf(
			"REMOTE instance remains after Disable: found=%v err=%v",
			found,
			errors.Join(readErr, closeErr),
		)
	}
}

func moduleApplyRemoteDispatchAttemptCountV1(
	t *testing.T,
	databasePath string,
) int64 {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	readErr := database.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM dispatch_attempts",
	).Scan(&count)
	closeErr := database.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("count REMOTE dispatch attempts: %v", errors.Join(readErr, closeErr))
	}
	return count
}
