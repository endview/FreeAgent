package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/wasmaction"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type moduleApplyWASMFixtureV1 struct {
	ArtifactDirectory string
	ArtifactDigest    string
	ArtifactSizeBytes uint64
	ModuleID          string
	Version           string
	InstanceID        string
}

func TestModuleApplyWASMActionPlanPolicyAndGrantFailClosedV1(t *testing.T) {
	t.Parallel()

	fixture := newModuleApplyWASMFixtureV1(t, t.TempDir())
	canonical := newModuleApplyWASMPlanV1(
		t,
		fixture,
		1,
		moduleapi.FailureRequired,
		moduleapi.EffectNone,
		moduleapi.EffectNone,
		json.RawMessage(`{}`),
	)
	plan, _, _, err := restoreModuleApplyPlanV1(canonical)
	if err != nil {
		t.Fatalf("restore WASM Action plan: %v", err)
	}
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil {
		t.Fatalf("resolve WASM Action policy: %v", err)
	}
	if policy.HandlerKind != moduleApplyHandlerWASMActionV1 ||
		policy.ExecutionClass != moduleapi.ExecutionWASM ||
		policy.AdapterIdentity != wasmaction.AdapterIdentityV1 ||
		!policy.RequiresWASMActionArtifactGrant {
		t.Fatalf("WASM Action policy=%+v", policy)
	}
	if err := validateModuleApplyArtifactGrantsV1(
		policy,
		fixture.ArtifactDigest,
		"",
		"",
		"",
		fixture.ArtifactDigest,
	); err != nil {
		t.Fatalf("exact WASM artifact grant: %v", err)
	}
	if err := validateModuleApplyArtifactGrantsV1(
		policy,
		fixture.ArtifactDigest,
		"",
		"",
		"",
		"",
	); !errors.Is(err, errModuleApplyArtifactGrantRequiredV1) {
		t.Fatalf("missing WASM artifact grant=%v", err)
	}
	if err := validateModuleApplyArtifactGrantsV1(
		policy,
		fixture.ArtifactDigest,
		fixture.ArtifactDigest,
		"",
		"",
		fixture.ArtifactDigest,
	); !errors.Is(err, errModuleApplyArtifactGrantFlagsV1) {
		t.Fatalf("mixed LOCAL_PROCESS/WASM artifact grants=%v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "wrong protocol",
			mutate: func(value map[string]any) {
				moduleApplyWASMRuntimeRequestV1(value)["protocol"] =
					moduleapi.RuntimeProtocolFreeAgentActionHTTPV1
			},
		},
		{
			name: "wrong port",
			mutate: func(value map[string]any) {
				value["port"] = map[string]any{
					"name":          productionContextPort.Name,
					"exact_version": productionContextPort.ExactVersion,
				}
			},
		},
		{
			name: "optional binding",
			mutate: func(value map[string]any) {
				value["binding"].(map[string]any)["failure_policy"] =
					string(moduleapi.FailureOptional)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := decodeModuleApplyWASMPlanValueV1(t, canonical)
			test.mutate(value)
			mutated := canonicalModuleApplyPlanTestJSON(t, value)
			if _, _, _, err := restoreModuleApplyPlanV1(mutated); err == nil {
				t.Fatal("invalid WASM plan was accepted")
			}
		})
	}

	for _, test := range []struct {
		name            string
		bindingEffect   moduleapi.EffectClass
		authorityEffect moduleapi.EffectClass
		parameters      json.RawMessage
	}{
		{
			name:            "config effect",
			bindingEffect:   moduleapi.EffectReadOnly,
			authorityEffect: moduleapi.EffectReadOnly,
			parameters:      json.RawMessage(`{}`),
		},
		{
			name:            "authority effect",
			bindingEffect:   moduleapi.EffectNone,
			authorityEffect: moduleapi.EffectReadOnly,
			parameters:      json.RawMessage(`{}`),
		},
		{
			name:            "provider parameters",
			bindingEffect:   moduleapi.EffectNone,
			authorityEffect: moduleapi.EffectNone,
			parameters:      json.RawMessage(`{"unexpected":true}`),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := newModuleApplyWASMPlanV1(
				t,
				fixture,
				1,
				moduleapi.FailureRequired,
				test.bindingEffect,
				test.authorityEffect,
				test.parameters,
			)
			if _, _, _, err := restoreModuleApplyPlanV1(mutated); err == nil {
				t.Fatal("authority-bearing WASM plan was accepted")
			}
		})
	}
}

func TestModuleApplyWASMActionDryRunApplyRetryAndCurrentStateStayOfflineV1(
	t *testing.T,
) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyWASMFixtureV1(t, filepath.Join(root, "fixture"))
	planPath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "wasm-enable.json"),
		newModuleApplyWASMPlanV1(
			t,
			fixture,
			1,
			moduleapi.FailureRequired,
			moduleapi.EffectNone,
			moduleapi.EffectNone,
			json.RawMessage(`{}`),
		),
	)

	if _, err := runModuleApplyWASMFixtureV1(
		true,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		"",
		false,
	); err == nil || !strings.Contains(err.Error(), "GRANT_REQUIRED") {
		t.Fatalf("dry-run without WASM grant error=%v", err)
	}
	if _, err := runModuleApplyWASMFixtureV1(
		true,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
		true,
	); err == nil || !strings.Contains(err.Error(), "INVALID_FLAGS") {
		t.Fatalf("dry-run with mixed grants error=%v", err)
	}

	dryRun, err := runModuleApplyWASMFixtureV1(
		true,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
		false,
	)
	if err != nil {
		t.Fatalf("dry-run WASM Action: %v", err)
	}
	if dryRun.Status != moduleApplyStatusWouldApply {
		t.Fatalf("dry-run status=%q", dryRun.Status)
	}
	if _, err := os.Stat(filepath.Join(artifactRoot, fixture.ArtifactDigest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run published WASM artifact: %v", err)
	}

	applied, err := runModuleApplyWASMFixtureV1(
		false,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
		false,
	)
	if err != nil {
		t.Fatalf("apply WASM Action: %v", err)
	}
	if applied.Status != moduleApplyStatusApplied || applied.PointerRevision != 2 {
		t.Fatalf("apply result=%+v", applied)
	}
	observer, err := currentstore.OpenReadOnlyObserver(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	_, _, catalog, err := observer.LoadPublishedBasis(
		context.Background(),
		defaultTenantID,
	)
	if err != nil {
		t.Fatal(err)
	}
	entry, found := catalog.FindInstance(fixture.InstanceID)
	if !found || entry.Activation.ExecutionClass != moduleapi.ExecutionWASM ||
		entry.Activation.AdapterIdentity != wasmaction.AdapterIdentityV1 {
		t.Fatalf("published WASM Catalog entry=%+v found=%v", entry, found)
	}

	retried, err := runModuleApplyWASMFixtureV1(
		false,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
		false,
	)
	if err != nil || retried.Status != moduleApplyStatusAlreadyApplied {
		t.Fatalf("retry WASM Action result=%+v error=%v", retried, err)
	}

	// The guest's exported execute function traps. Reaching APPLIED and
	// ALREADY_APPLIED therefore also proves Apply/Dry-run only compiled the ABI
	// and never instantiated or executed the guest.
	finalWASM := filepath.Join(
		artifactRoot,
		fixture.ArtifactDigest,
		"content",
		"action.wasm",
	)
	if err := os.WriteFile(finalWASM, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runModuleApplyWASMFixtureV1(
		true,
		databasePath,
		artifactRoot,
		planPath,
		fixture.ArtifactDirectory,
		fixture.ArtifactDigest,
		false,
	); err == nil || !strings.Contains(err.Error(), "ARTIFACT_INVALID") {
		t.Fatalf("current-state static revalidation error=%v", err)
	}
}

func TestModuleOperatorExecutionMatchesRemoteAndWASMRuntimeV1(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		class moduleapi.ExecutionClass
		mode  moduleapi.RuntimeModeRequest
	}{
		{class: moduleapi.ExecutionRemote, mode: moduleapi.RuntimeModeRequestRemote},
		{class: moduleapi.ExecutionWASM, mode: moduleapi.RuntimeModeRequestWASM},
	} {
		if !moduleOperatorExecutionMatchesRuntimeV1(test.class, test.mode) {
			t.Fatalf("execution %q did not match runtime %q", test.class, test.mode)
		}
		if moduleOperatorExecutionMatchesRuntimeV1(
			moduleapi.ExecutionLocalProcess,
			test.mode,
		) {
			t.Fatalf("LOCAL_PROCESS matched runtime %q", test.mode)
		}
	}
}

func newModuleApplyWASMFixtureV1(
	t *testing.T,
	root string,
) moduleApplyWASMFixtureV1 {
	t.Helper()
	provider, manifest, descriptor, _ := productionWASMActionTestArtifact(t)
	wasm := moduleApplyWASMTrapModuleV1(t)
	digest, err := moduleapi.ComputeArtifactDigest(
		manifest,
		[]moduleapi.ArtifactFile{
			{Path: "content/action.wasm", Content: wasm},
			{Path: "content/actions.json", Content: descriptor},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	provider.ArtifactDigest = digest
	productionWASMActionWriteArtifact(
		t,
		root,
		provider,
		manifest,
		descriptor,
		wasm,
	)
	return moduleApplyWASMFixtureV1{
		ArtifactDirectory: filepath.Join(root, digest),
		ArtifactDigest:    digest,
		ArtifactSizeBytes: uint64(len(manifest) + len(descriptor) + len(wasm)),
		ModuleID:          provider.ModuleID,
		Version:           provider.Version,
		InstanceID:        provider.InstanceID,
	}
}

func moduleApplyWASMTrapModuleV1(t *testing.T) []byte {
	t.Helper()
	base := productionWASMActionMinimalModuleV1()
	section := bytes.LastIndex(base, []byte{0x0a, 0x0c, 0x02})
	if section < 0 {
		t.Fatal("minimal WASM code section is absent")
	}
	wasm := append([]byte{}, base[:section]...)
	wasm = append(wasm,
		0x0a, 0x0b, 0x02,
		0x05, 0x00, 0x41, 0x80, 0x08, 0x0b,
		0x03, 0x00, 0x00, 0x0b,
	)
	if err := wasmaction.ValidateModuleV1(context.Background(), wasm); err != nil {
		t.Fatalf("trapping test WASM does not satisfy the static ABI: %v", err)
	}
	return wasm
}

func newModuleApplyWASMPlanV1(
	t *testing.T,
	fixture moduleApplyWASMFixtureV1,
	expectedPointer uint64,
	failurePolicy moduleapi.FailurePolicy,
	bindingEffect moduleapi.EffectClass,
	authorityEffect moduleapi.EffectClass,
	parameters json.RawMessage,
) []byte {
	t.Helper()
	_, config, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "wasm.echo",
				ProviderActionID: "example.wasm.echo",
				LocalEffectClass: bindingEffect,
				MaxResultBytes:   256,
			}},
			Parameters: bytes.Clone(parameters),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 defaultTenantID,
			AllowedWorkspaceIDs:      []string{defaultWorkspaceID},
			AllowedProviderActionIDs: []string{"example.wasm.echo"},
			MaxEffectClass:           authorityEffect,
			MaxResultBytes:           256,
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
		"binding_target": moduleApplyProfileBindingTargetTestValue(
			moduleApplyTestProfileID,
		),
		"instance_id": fixture.InstanceID,
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  fixture.ModuleID,
			"exact_version":       fixture.Version,
			"artifact_digest":     fixture.ArtifactDigest,
			"artifact_size_bytes": fixture.ArtifactSizeBytes,
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestWASM),
				"protocol": moduleapi.RuntimeProtocolFreeAgentActionWASMV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": 0,
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(failurePolicy),
		},
	})
}

func runModuleApplyWASMFixtureV1(
	dryRun bool,
	databasePath string,
	artifactRoot string,
	planPath string,
	artifactDirectory string,
	wasmGrant string,
	includeLocalGrant bool,
) (moduleApplyResultV1, error) {
	args := []string{
		"--db", databasePath,
		"--artifact-root", artifactRoot,
		"--plan", planPath,
		"--artifact", artifactDirectory,
	}
	if wasmGrant != "" {
		args = append(args, "--allow-wasm-action-artifact", wasmGrant)
	}
	if includeLocalGrant {
		args = append(args, "--allow-local-mcp-artifact", wasmGrant)
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
		return moduleApplyResultV1{}, errors.Join(
			err,
			errors.New("module command unexpectedly wrote stderr: "+stderr.String()),
		)
	}
	if err != nil {
		if stdout.Len() != 0 {
			return moduleApplyResultV1{}, errors.Join(
				err,
				errors.New("failed module command unexpectedly wrote stdout"),
			)
		}
		return moduleApplyResultV1{}, err
	}
	if dryRun {
		var result moduleDryRunResultV1
		if err := decodeModuleApplyWASMResultV1(stdout.Bytes(), &result); err != nil {
			return moduleApplyResultV1{}, err
		}
		return moduleApplyResultV1{
			Status:          result.Status,
			PointerRevision: result.CandidateBasis.PointerRevision,
		}, nil
	}
	var result moduleApplyResultV1
	if err := decodeModuleApplyWASMResultV1(stdout.Bytes(), &result); err != nil {
		return moduleApplyResultV1{}, err
	}
	return result, nil
}

func decodeModuleApplyWASMResultV1(payload []byte, target any) error {
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

func decodeModuleApplyWASMPlanValueV1(
	t *testing.T,
	canonical []byte,
) map[string]any {
	t.Helper()
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func moduleApplyWASMRuntimeRequestV1(value map[string]any) map[string]any {
	return value["module"].(map[string]any)["expected_runtime_request"].(map[string]any)
}
