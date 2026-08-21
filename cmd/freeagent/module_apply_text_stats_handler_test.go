package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestModuleApplyArtifactGrantValidatorV1IsExactAndExclusive(t *testing.T) {
	t.Parallel()

	table := moduleApplyProtocolHandlerTableV1()
	digest := strings.Repeat("a", moduleapi.SHA256HexLength)
	other := strings.Repeat("b", moduleapi.SHA256HexLength)
	tests := []struct {
		name       string
		policy     moduleApplyLocalPolicyV1
		mcp        string
		trusted    string
		remote     string
		wasm       string
		want       error
		wantNoFail bool
	}{
		{name: "MCP exact", policy: table[4], mcp: digest, wantNoFail: true},
		{name: "MCP absent", policy: table[4], want: errModuleApplyArtifactGrantRequiredV1},
		{name: "MCP wrong", policy: table[4], mcp: other, want: errModuleApplyArtifactGrantRequiredV1},
		{name: "MCP cross", policy: table[4], trusted: digest, want: errModuleApplyArtifactGrantFlagsV1},
		{name: "REMOTE exact", policy: table[5], remote: digest, wantNoFail: true},
		{name: "REMOTE absent", policy: table[5], want: errModuleApplyArtifactGrantRequiredV1},
		{name: "REMOTE cross", policy: table[5], trusted: digest, want: errModuleApplyArtifactGrantFlagsV1},
		{name: "WASM exact", policy: table[6], wasm: digest, wantNoFail: true},
		{name: "WASM absent", policy: table[6], want: errModuleApplyArtifactGrantRequiredV1},
		{name: "WASM wrong", policy: table[6], wasm: other, want: errModuleApplyArtifactGrantRequiredV1},
		{name: "WASM cross", policy: table[6], trusted: digest, want: errModuleApplyArtifactGrantFlagsV1},
		{name: "trusted exact", policy: table[7], trusted: digest, wantNoFail: true},
		{name: "trusted absent", policy: table[7], want: errModuleApplyArtifactGrantRequiredV1},
		{name: "trusted wrong", policy: table[7], trusted: other, want: errModuleApplyArtifactGrantRequiredV1},
		{name: "trusted cross", policy: table[7], mcp: digest, want: errModuleApplyArtifactGrantFlagsV1},
		{name: "double grant", policy: table[7], mcp: digest, trusted: digest, want: errModuleApplyArtifactGrantFlagsV1},
		{name: "unrelated grant", policy: table[1], trusted: digest, want: errModuleApplyArtifactGrantFlagsV1},
		{name: "no-grant handler", policy: table[1], wantNoFail: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateModuleApplyArtifactGrantsV1(
				test.policy,
				digest,
				test.mcp,
				test.trusted,
				test.remote,
				test.wasm,
			)
			if test.wantNoFail {
				if err != nil {
					t.Fatalf("validate grant: %v", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("grant error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestModuleApplyTextStatsBindingV1RejectsBroaderActionContracts(t *testing.T) {
	t.Parallel()

	baseConfig := moduleapi.ActionBindingConfigV1{
		SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
		Actions: []moduleapi.ActionBindingMappingV1{{
			PublicActionID:   "text.stats",
			ProviderActionID: "text.stats",
			LocalEffectClass: moduleapi.EffectNone,
			MaxResultBytes:   exactTextStatsMaxResultBytesForTestV1,
		}},
		Parameters: []byte(`{}`),
	}
	baseAuthority := moduleapi.ActionAuthorityCeilingV1{
		SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
		TenantID:                 defaultTenantID,
		AllowedWorkspaceIDs:      []string{"*"},
		AllowedProviderActionIDs: []string{"text.stats"},
		MaxEffectClass:           moduleapi.EffectNone,
		MaxResultBytes:           exactTextStatsMaxResultBytesForTestV1,
	}
	tests := []struct {
		name            string
		mutateConfig    func(*moduleapi.ActionBindingConfigV1)
		mutateAuthority func(*moduleapi.ActionAuthorityCeilingV1)
	}{
		{
			name: "second provider action",
			mutateConfig: func(config *moduleapi.ActionBindingConfigV1) {
				config.Actions = append(config.Actions, moduleapi.ActionBindingMappingV1{
					PublicActionID:   "text.other",
					ProviderActionID: "text.other",
					LocalEffectClass: moduleapi.EffectNone,
					MaxResultBytes:   1,
				})
			},
		},
		{
			name: "effect broadening",
			mutateConfig: func(config *moduleapi.ActionBindingConfigV1) {
				config.Actions[0].LocalEffectClass = moduleapi.EffectReadOnly
			},
		},
		{
			name: "result broadening",
			mutateConfig: func(config *moduleapi.ActionBindingConfigV1) {
				config.Actions[0].MaxResultBytes++
			},
		},
		{
			name: "authority second provider action",
			mutateAuthority: func(authority *moduleapi.ActionAuthorityCeilingV1) {
				authority.AllowedProviderActionIDs = append(
					authority.AllowedProviderActionIDs,
					"text.other",
				)
			},
		},
		{
			name: "authority effect broadening",
			mutateAuthority: func(authority *moduleapi.ActionAuthorityCeilingV1) {
				authority.MaxEffectClass = moduleapi.EffectReadOnly
			},
		},
		{
			name: "authority result broadening",
			mutateAuthority: func(authority *moduleapi.ActionAuthorityCeilingV1) {
				authority.MaxResultBytes++
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configInput := baseConfig
			configInput.Actions = append(
				[]moduleapi.ActionBindingMappingV1(nil),
				baseConfig.Actions...,
			)
			authorityInput := baseAuthority
			authorityInput.AllowedProviderActionIDs = append(
				[]string(nil),
				baseAuthority.AllowedProviderActionIDs...,
			)
			if test.mutateConfig != nil {
				test.mutateConfig(&configInput)
			}
			if test.mutateAuthority != nil {
				test.mutateAuthority(&authorityInput)
			}
			_, config, err := moduleapi.NewActionBindingConfigV1(configInput)
			if err != nil {
				t.Fatalf("freeze broadened config: %v", err)
			}
			_, authority, err := moduleapi.NewActionAuthorityCeilingV1(authorityInput)
			if err != nil {
				t.Fatalf("freeze broadened authority: %v", err)
			}
			if err := validateModuleApplyTextStatsBindingV1(moduleApplyBindingV1{
				Config:           config,
				AuthorityCeiling: authority,
				FailurePolicy:    moduleapi.FailureRequired,
			}); err == nil {
				t.Fatal("broadened text.stats binding was accepted")
			}
		})
	}
}

func TestModuleApplyExactAdapterAvailabilityProbeV1IsInertAndExact(t *testing.T) {
	t.Parallel()

	probe := moduleApplyExactAdapterAvailabilityProbeV1{
		ArtifactDigest:  localTextStatsDigest,
		AdapterIdentity: localTextStatsAdapterID,
	}
	probeType := reflect.TypeOf(probe)
	if probeType.NumField() != 2 || probeType.NumMethod() != 1 {
		t.Fatalf("inert exact probe shape changed: fields=%d methods=%d", probeType.NumField(), probeType.NumMethod())
	}
	registered, err := probe.IsRegistered(
		context.Background(),
		localTextStatsDigest,
		localTextStatsAdapterID,
	)
	if err != nil || !registered {
		t.Fatalf("exact availability = %v, %v", registered, err)
	}
	registered, err = probe.IsRegistered(
		context.Background(),
		strings.Repeat("0", moduleapi.SHA256HexLength),
		localTextStatsAdapterID,
	)
	if err != nil || registered {
		t.Fatalf("wrong digest availability = %v, %v", registered, err)
	}
}

func TestModuleApplyDisabledRejectsTransientArtifactGrantsBeforeStateAccess(t *testing.T) {
	t.Parallel()

	canonicalInput := canonicalModuleApplyPlanTestJSON(
		t,
		disabledModuleApplyPlanTestValue(),
	)
	plan, canonical, digest, err := restoreModuleApplyPlanV1(canonicalInput)
	if err != nil {
		t.Fatalf("restore disabled plan: %v", err)
	}
	base := moduleApplyCommandInputV1{
		DatabasePath:  filepath.Join(t.TempDir(), "missing.sqlite"),
		ArtifactRoot:  filepath.Join(t.TempDir(), "missing-artifacts"),
		Plan:          plan,
		PlanCanonical: canonical,
		PlanDigest:    digest,
	}
	grants := []struct {
		name    string
		mcp     string
		trusted string
	}{
		{name: "MCP", mcp: localTextStatsDigest},
		{name: "trusted", trusted: localTextStatsDigest},
		{name: "both", mcp: localTextStatsDigest, trusted: localTextStatsDigest},
	}
	for _, grant := range grants {
		t.Run(grant.name, func(t *testing.T) {
			input := base
			input.LocalMCPArtifactGrant = grant.mcp
			input.TrustedInProcessArtifactGrant = grant.trusted
			if _, err := applyModulePlanV1(context.Background(), input); moduleApplyFailureCodeOfV1(err) != moduleApplyFailureInvalidFlags {
				t.Fatalf("Apply disabled grant error = %v", err)
			}
			if _, err := dryRunModulePlanV1(context.Background(), input); moduleApplyFailureCodeOfV1(err) != moduleApplyFailureInvalidFlags {
				t.Fatalf("Dry-run disabled grant error = %v", err)
			}
		})
	}
}

func TestModuleApplyUnknownExactSelectorFailsBeforeStateAndArtifactSentinels(t *testing.T) {
	t.Parallel()

	_, config, err := moduleapi.NewActionBindingConfigV1(
		moduleapi.ActionBindingConfigV1{
			SchemaVersion: moduleapi.ActionBindingConfigSchemaV1,
			Actions: []moduleapi.ActionBindingMappingV1{{
				PublicActionID:   "text.stats",
				ProviderActionID: "text.stats",
				LocalEffectClass: moduleapi.EffectNone,
				MaxResultBytes:   exactTextStatsMaxResultBytesForTestV1,
			}},
			Parameters: []byte(`{}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, authority, err := moduleapi.NewActionAuthorityCeilingV1(
		moduleapi.ActionAuthorityCeilingV1{
			SchemaVersion:            moduleapi.ActionAuthorityCeilingSchemaV1,
			TenantID:                 defaultTenantID,
			AllowedWorkspaceIDs:      []string{"*"},
			AllowedProviderActionIDs: []string{"text.stats"},
			MaxEffectClass:           moduleapi.EffectNone,
			MaxResultBytes:           exactTextStatsMaxResultBytesForTestV1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	canonical := canonicalModuleApplyPlanTestJSON(t, map[string]any{
		"schema_version":            moduleApplyPlanSchemaV1,
		"desired_state":             string(moduleApplyEnabledV1),
		"tenant_id":                 defaultTenantID,
		"expected_pointer_revision": uint64(1),
		"binding_target":            moduleApplyProfileBindingTargetTestValue(moduleApplyTestProfileID),
		"instance_id":               "unknown-selector",
		"port": map[string]any{
			"name":          productionActionPort.Name,
			"exact_version": productionActionPort.ExactVersion,
		},
		"module": map[string]any{
			"id":                  localTextStatsModuleID,
			"exact_version":       localTextStatsVersion,
			"artifact_digest":     localTextStatsDigest,
			"artifact_size_bytes": uint64(1936),
			"expected_runtime_request": map[string]any{
				"mode":     string(moduleapi.RuntimeModeRequestDeclarative),
				"protocol": moduleapi.RuntimeProtocolStaticV1,
			},
		},
		"binding": map[string]any{
			"port_binding_index": uint32(0),
			"config":             json.RawMessage(config),
			"authority_ceiling":  json.RawMessage(authority),
			"failure_policy":     string(moduleapi.FailureRequired),
		},
	})
	plan, canonical, digest, err := restoreModuleApplyPlanV1(canonical)
	if err != nil {
		t.Fatalf("restore mismatch-selector plan: %v", err)
	}
	root := t.TempDir()
	sentinel := filepath.Join(root, "must-not-be-read")
	if err := os.WriteFile(sentinel, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := moduleApplyCommandInputV1{
		DatabasePath:      sentinel,
		ArtifactRoot:      sentinel,
		ArtifactDirectory: sentinel,
		Plan:              plan,
		PlanCanonical:     canonical,
		PlanDigest:        digest,
	}
	if _, err := applyModulePlanV1(context.Background(), input); moduleApplyFailureCodeOfV1(err) != moduleApplyFailurePlanInvalid {
		t.Fatalf("Apply selector failure = %v (%s)", err, moduleApplyFailureCodeOfV1(err))
	}
	if _, err := dryRunModulePlanV1(context.Background(), input); moduleApplyFailureCodeOfV1(err) != moduleApplyFailurePlanInvalid {
		t.Fatalf("Dry-run selector failure = %v (%s)", err, moduleApplyFailureCodeOfV1(err))
	}
	contents, err := os.ReadFile(sentinel)
	if err != nil || string(contents) != "sentinel" {
		t.Fatalf("sentinel changed = %q, %v", contents, err)
	}
}

const exactTextStatsMaxResultBytesForTestV1 = uint32(256)
