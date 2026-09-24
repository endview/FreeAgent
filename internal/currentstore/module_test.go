package currentstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestInstallModuleStrictAtomicAndIdempotent(t *testing.T) {
	store := openModuleTestStore(t)
	firstManifest := canonicalModuleManifest(
		t,
		"demo.provider",
		"v1",
		moduleapi.RuntimeModeRequestTrustedInProcess,
		nil,
	)
	firstInput := installInput(
		t,
		"install-demo-v1",
		firstManifest,
		strings.Repeat("a", 64),
	)
	first, err := store.InstallModule(context.Background(), firstInput)
	if err != nil {
		t.Fatalf("InstallModule: %v", err)
	}
	firstInput.ManifestBytes[0] = '['
	first.ManifestBytes[0] = '['

	retryInput := installInput(
		t,
		"install-demo-v1",
		firstManifest,
		strings.Repeat("a", 64),
	)
	retry, err := store.InstallModule(context.Background(), retryInput)
	if err != nil {
		t.Fatalf("idempotent InstallModule: %v", err)
	}
	if !retry.InstalledAt.Equal(first.InstalledAt) {
		t.Fatal("idempotent install changed installed_at")
	}
	differentID := retryInput
	differentID.InstallationID = "different-installation-id"
	if _, err := store.InstallModule(
		context.Background(),
		differentID,
	); !errors.Is(err, ErrModuleConflict) {
		t.Fatalf("different installation ID error=%v", err)
	}

	got, err := store.GetModuleInstallation(
		context.Background(),
		first.InstallationID,
	)
	if err != nil {
		t.Fatalf("GetModuleInstallation: %v", err)
	}
	if got.ModuleID != "demo.provider" ||
		got.ExactVersion != "v1" ||
		got.ArtifactDigest != strings.Repeat("a", 64) ||
		!bytes.Equal(got.ManifestBytes, firstManifest) {
		t.Fatalf("GetModuleInstallation=%+v", got)
	}
	got.ManifestBytes[0] = '['
	again, err := store.GetModuleInstallation(
		context.Background(),
		first.InstallationID,
	)
	if err != nil {
		t.Fatalf("second GetModuleInstallation: %v", err)
	}
	if !bytes.Equal(again.ManifestBytes, firstManifest) {
		t.Fatal("GetModuleInstallation exposed aliased manifest bytes")
	}
}

func TestGetModuleInstallationByIdentityStrictDetachedAndMissing(t *testing.T) {
	store := openModuleTestStore(t)
	manifest := canonicalModuleManifest(
		t,
		"demo.identity.lookup",
		"v1",
		moduleapi.RuntimeModeRequestTrustedInProcess,
		nil,
	)
	installed, err := store.InstallModule(
		context.Background(),
		installInput(
			t,
			"install-identity-lookup",
			manifest,
			strings.Repeat("2", 64),
		),
	)
	if err != nil {
		t.Fatalf("InstallModule: %v", err)
	}

	got, err := store.GetModuleInstallationByIdentity(
		context.Background(),
		"demo.identity.lookup",
		"v1",
	)
	if err != nil {
		t.Fatalf("GetModuleInstallationByIdentity: %v", err)
	}
	if got.InstallationID != installed.InstallationID ||
		got.ModuleID != installed.ModuleID ||
		got.ExactVersion != installed.ExactVersion ||
		got.ManifestRef != installed.ManifestRef ||
		got.ArtifactDigest != installed.ArtifactDigest ||
		!got.InstalledAt.Equal(installed.InstalledAt) ||
		!bytes.Equal(got.ManifestBytes, manifest) {
		t.Fatalf("GetModuleInstallationByIdentity=%+v want %+v", got, installed)
	}
	got.ManifestBytes[0] = '['
	again, err := store.GetModuleInstallationByIdentity(
		context.Background(),
		"demo.identity.lookup",
		"v1",
	)
	if err != nil {
		t.Fatalf("second GetModuleInstallationByIdentity: %v", err)
	}
	if !bytes.Equal(again.ManifestBytes, manifest) {
		t.Fatal("GetModuleInstallationByIdentity exposed aliased manifest bytes")
	}

	if _, err := store.GetModuleInstallationByIdentity(
		context.Background(),
		"demo.identity.missing",
		"v1",
	); !errors.Is(err, ErrModuleInstallationNotFound) {
		t.Fatalf("missing installation error=%v", err)
	}
	for _, test := range []struct {
		name         string
		ctx          context.Context
		moduleID     string
		exactVersion string
	}{
		{
			name: "nil context", ctx: nil,
			moduleID: "demo.identity.lookup", exactVersion: "v1",
		},
		{
			name: "invalid module ID", ctx: context.Background(),
			moduleID: "Demo Identity", exactVersion: "v1",
		},
		{
			name: "invalid exact version", ctx: context.Background(),
			moduleID: "demo.identity.lookup", exactVersion: "bad version",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := store.GetModuleInstallationByIdentity(
				test.ctx,
				test.moduleID,
				test.exactVersion,
			); !errors.Is(err, ErrInvalidModule) {
				t.Fatalf("error=%v want ErrInvalidModule", err)
			}
		})
	}
}

func TestInstallModuleConflictRollsBackNewManifest(t *testing.T) {
	store := openModuleTestStore(t)
	originalManifest := canonicalModuleManifest(
		t,
		"demo.conflict",
		"v1",
		moduleapi.RuntimeModeRequestDeclarative,
		nil,
	)
	if _, err := store.InstallModule(
		context.Background(),
		installInput(
			t,
			"install-original",
			originalManifest,
			strings.Repeat("b", 64),
		),
	); err != nil {
		t.Fatalf("install original: %v", err)
	}
	artifactOnlyConflict := installInput(
		t,
		"install-artifact-conflict",
		originalManifest,
		strings.Repeat("c", 64),
	)
	if _, err := store.InstallModule(
		context.Background(),
		artifactOnlyConflict,
	); !errors.Is(err, ErrModuleConflict) {
		t.Fatalf("artifact-only conflict error=%v", err)
	}
	changedManifest := canonicalModuleManifest(
		t,
		"demo.conflict",
		"v1",
		moduleapi.RuntimeModeRequestDeclarative,
		map[string]any{
			"config_schema": map[string]any{"type": "object"},
		},
	)
	changed := installInput(
		t,
		"install-changed",
		changedManifest,
		strings.Repeat("b", 64),
	)
	if _, err := store.InstallModule(
		context.Background(),
		changed,
	); !errors.Is(err, ErrModuleConflict) {
		t.Fatalf("conflicting InstallModule error=%v", err)
	}
	if _, err := store.GetContent(
		context.Background(),
		changed.ExpectedManifestRef,
	); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf(
			"rolled-back manifest GetContent error=%v want not found",
			err,
		)
	}
	if _, err := store.GetModuleInstallation(
		context.Background(),
		"install-changed",
	); !errors.Is(err, ErrModuleInstallationNotFound) {
		t.Fatalf(
			"rolled-back installation error=%v want not found",
			err,
		)
	}
}

func TestInstallModuleRejectsIdentityAndSelfAuthorization(t *testing.T) {
	store := openModuleTestStore(t)
	manifest := canonicalModuleManifest(
		t,
		"demo.identity",
		"v1",
		moduleapi.RuntimeModeRequestDeclarative,
		nil,
	)
	wrongIdentity := installInput(
		t,
		"install-wrong-identity",
		manifest,
		strings.Repeat("d", 64),
	)
	wrongIdentity.ModuleID = "demo.other"
	if _, err := store.InstallModule(
		context.Background(),
		wrongIdentity,
	); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("identity mismatch error=%v", err)
	}

	selfAuthorizing := canonicalModuleManifest(
		t,
		"demo.selfauth",
		"v1",
		moduleapi.RuntimeModeRequestTrustedInProcess,
		map[string]any{
			"execution_class": "TRUSTED_IN_PROCESS",
		},
	)
	selfAuthorizingDigest, err := ComputeContentDigest(
		ContentModuleManifest,
		moduleManifestMediaType,
		selfAuthorizing,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.InstallModule(context.Background(), InstallModuleInput{
		InstallationID:      "install-selfauth",
		ModuleID:            "demo.selfauth",
		ExactVersion:        "v1",
		ExpectedManifestRef: selfAuthorizingDigest,
		ManifestBytes:       selfAuthorizing,
		ArtifactDigest:      strings.Repeat("e", 64),
	})
	if !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("self-authorizing manifest error=%v", err)
	}
	if _, err := store.GetContent(
		context.Background(),
		selfAuthorizingDigest,
	); !errors.Is(err, ErrContentNotFound) {
		t.Fatalf("rejected manifest left content: %v", err)
	}
}

func TestInstallAndActivateExternalRuntimeRequests(t *testing.T) {
	tests := []struct {
		mode    moduleapi.RuntimeModeRequest
		class   moduleapi.ExecutionClass
		adapter string
	}{
		{
			mode:    moduleapi.RuntimeModeRequestLocalProcess,
			class:   moduleapi.ExecutionLocalProcess,
			adapter: "host.mcp-stdio",
		},
		{
			mode:    moduleapi.RuntimeModeRequestRemote,
			class:   moduleapi.ExecutionRemote,
			adapter: "host.remote-action-http",
		},
		{
			mode:    moduleapi.RuntimeModeRequestWASM,
			class:   moduleapi.ExecutionWASM,
			adapter: "host.wasm-action",
		},
	}
	for _, test := range tests {
		t.Run(string(test.mode), func(t *testing.T) {
			store := openModuleTestStore(t)
			manifest := canonicalModuleManifest(
				t,
				"demo.external",
				"v1",
				test.mode,
				nil,
			)
			installation, err := store.InstallModule(
				context.Background(),
				installInput(
					t,
					"install-external",
					manifest,
					strings.Repeat("9", 64),
				),
			)
			if err != nil {
				t.Fatalf("InstallModule: %v", err)
			}
			if installation.InstallationID != "install-external" {
				t.Fatalf("installation = %+v", installation)
			}

			activationID := "activation-" + string(test.mode)
			activation, err := store.ActivateModule(
				context.Background(),
				ActivateModuleInput{
					ActivationID:       activationID,
					TenantID:           "tenant",
					InstanceID:         "external-instance-" + string(test.mode),
					InstallationID:     installation.InstallationID,
					ActivationRevision: 1,
					ExecutionClass:     test.class,
					AdapterIdentity:    test.adapter,
				},
			)
			if err != nil {
				t.Fatalf("ActivateModule(%s): %v", test.mode, err)
			}
			if activation.ExecutionClass != test.class ||
				activation.AdapterIdentity != test.adapter {
				t.Fatalf("activation = %+v", activation)
			}
			stored, getErr := store.GetModuleActivation(
				context.Background(),
				activationID,
			)
			if getErr != nil || stored != activation {
				t.Fatalf("stored activation = %+v, %v", stored, getErr)
			}
		})
	}
}

func TestActivateModulePreservesCoreAdapterAssignment(t *testing.T) {
	store := openModuleTestStore(t)
	manifest := canonicalModuleManifest(
		t,
		"demo.request-declarative",
		"v1",
		moduleapi.RuntimeModeRequestDeclarative,
		nil,
	)
	installation, err := store.InstallModule(
		context.Background(),
		installInput(
			t,
			"install-request-declarative",
			manifest,
			strings.Repeat("f", 64),
		),
	)
	if err != nil {
		t.Fatalf("InstallModule: %v", err)
	}
	input := ActivateModuleInput{
		ActivationID:       "activation-one",
		TenantID:           "tenant-one",
		InstanceID:         "instance-one",
		InstallationID:     installation.InstallationID,
		ActivationRevision: 1,
		ExecutionClass:     moduleapi.ExecutionDeclarative,
		AdapterIdentity:    "core.assigned.adapter",
	}
	first, err := store.ActivateModule(context.Background(), input)
	if err != nil {
		t.Fatalf("ActivateModule: %v", err)
	}
	if first.ExecutionClass != moduleapi.ExecutionDeclarative {
		t.Fatalf(
			"execution class=%q want Core assignment DECLARATIVE",
			first.ExecutionClass,
		)
	}
	retry := input
	second, err := store.ActivateModule(context.Background(), retry)
	if err != nil {
		t.Fatalf("idempotent ActivateModule: %v", err)
	}
	if second.ActivationID != first.ActivationID ||
		!second.ActivatedAt.Equal(first.ActivatedAt) {
		t.Fatalf("idempotent activation=%+v want existing %+v", second, first)
	}
	differentID := input
	differentID.ActivationID = "different-activation-id"
	if _, err := store.ActivateModule(
		context.Background(),
		differentID,
	); !errors.Is(err, ErrModuleConflict) {
		t.Fatalf("different activation ID error=%v", err)
	}
	got, err := store.GetModuleActivation(
		context.Background(),
		first.ActivationID,
	)
	if err != nil {
		t.Fatalf("GetModuleActivation: %v", err)
	}
	if got != first {
		t.Fatalf("GetModuleActivation=%+v want %+v", got, first)
	}
}

func TestActivateModuleRejectsExecutionClassThatDiffersFromRuntimeRequest(
	t *testing.T,
) {
	tests := []struct {
		name          string
		mode          moduleapi.RuntimeModeRequest
		assignedClass moduleapi.ExecutionClass
	}{
		{
			name:          "declarative cannot become trusted in process",
			mode:          moduleapi.RuntimeModeRequestDeclarative,
			assignedClass: moduleapi.ExecutionTrustedInProcess,
		},
		{
			name:          "trusted in process cannot become declarative",
			mode:          moduleapi.RuntimeModeRequestTrustedInProcess,
			assignedClass: moduleapi.ExecutionDeclarative,
		},
		{
			name:          "local process cannot become trusted in process",
			mode:          moduleapi.RuntimeModeRequestLocalProcess,
			assignedClass: moduleapi.ExecutionTrustedInProcess,
		},
		{
			name:          "remote cannot become local process",
			mode:          moduleapi.RuntimeModeRequestRemote,
			assignedClass: moduleapi.ExecutionLocalProcess,
		},
		{
			name:          "WASM cannot become local process",
			mode:          moduleapi.RuntimeModeRequestWASM,
			assignedClass: moduleapi.ExecutionLocalProcess,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openModuleTestStore(t)
			manifest := canonicalModuleManifest(
				t,
				"demo.activation.mismatch",
				"v1",
				test.mode,
				nil,
			)
			installation, err := store.InstallModule(
				context.Background(),
				installInput(
					t,
					fmt.Sprintf("install-mismatch-%d", index),
					manifest,
					strings.Repeat(string(rune('4'+index)), 64),
				),
			)
			if err != nil {
				t.Fatalf("InstallModule: %v", err)
			}
			activationID := fmt.Sprintf("activation-mismatch-%d", index)
			_, err = store.ActivateModule(
				context.Background(),
				ActivateModuleInput{
					ActivationID:       activationID,
					TenantID:           "tenant",
					InstanceID:         fmt.Sprintf("instance-%d", index),
					InstallationID:     installation.InstallationID,
					ActivationRevision: 1,
					ExecutionClass:     test.assignedClass,
					AdapterIdentity:    "core.assigned.adapter",
				},
			)
			if !errors.Is(err, ErrInvalidModule) {
				t.Fatalf("ActivateModule error=%v", err)
			}
			if _, err := store.GetModuleActivation(
				context.Background(),
				activationID,
			); !errors.Is(err, ErrModuleActivationNotFound) {
				t.Fatalf("rejected activation was persisted: %v", err)
			}
		})
	}
}

func TestGetLatestModuleActivationForInstanceStrictScopedAndMissing(t *testing.T) {
	store := openModuleTestStore(t)
	manifest := canonicalModuleManifest(
		t,
		"demo.latest.activation",
		"v1",
		moduleapi.RuntimeModeRequestDeclarative,
		nil,
	)
	installation, err := store.InstallModule(
		context.Background(),
		installInput(
			t,
			"install-latest-activation",
			manifest,
			strings.Repeat("3", 64),
		),
	)
	if err != nil {
		t.Fatalf("InstallModule: %v", err)
	}
	inputs := []ActivateModuleInput{
		{
			ActivationID:       "activation-target-revision-2",
			TenantID:           "tenant-target",
			InstanceID:         "instance-target",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 2,
			ExecutionClass:     moduleapi.ExecutionDeclarative,
			AdapterIdentity:    "adapter.revision-2",
		},
		{
			ActivationID:       "activation-target-revision-1",
			TenantID:           "tenant-target",
			InstanceID:         "instance-target",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionDeclarative,
			AdapterIdentity:    "adapter.revision-1",
		},
		{
			ActivationID:       "activation-target-revision-4",
			TenantID:           "tenant-target",
			InstanceID:         "instance-target",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 4,
			ExecutionClass:     moduleapi.ExecutionDeclarative,
			AdapterIdentity:    "adapter.revision-4",
		},
		{
			ActivationID:       "activation-other-tenant-revision-9",
			TenantID:           "tenant-other",
			InstanceID:         "instance-target",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 9,
			ExecutionClass:     moduleapi.ExecutionDeclarative,
			AdapterIdentity:    "adapter.other-tenant",
		},
		{
			ActivationID:       "activation-other-instance-revision-10",
			TenantID:           "tenant-target",
			InstanceID:         "instance-other",
			InstallationID:     installation.InstallationID,
			ActivationRevision: 10,
			ExecutionClass:     moduleapi.ExecutionDeclarative,
			AdapterIdentity:    "adapter.other-instance",
		},
	}
	var want, wantExact ModuleActivation
	for _, input := range inputs {
		activation, err := store.ActivateModule(context.Background(), input)
		if err != nil {
			t.Fatalf("ActivateModule(%s): %v", input.ActivationID, err)
		}
		if input.ActivationID == "activation-target-revision-4" {
			want = activation
		}
		if input.ActivationID == "activation-target-revision-1" {
			wantExact = activation
		}
	}

	got, err := store.GetLatestModuleActivationForInstance(
		context.Background(),
		"tenant-target",
		"instance-target",
	)
	if err != nil {
		t.Fatalf("GetLatestModuleActivationForInstance: %v", err)
	}
	if got != want {
		t.Fatalf("GetLatestModuleActivationForInstance=%+v want %+v", got, want)
	}
	exact, err := store.GetModuleActivationByIdentity(
		context.Background(),
		"tenant-target",
		"instance-target",
		1,
	)
	if err != nil || exact != wantExact {
		t.Fatalf("GetModuleActivationByIdentity=%+v want %+v error=%v", exact, wantExact, err)
	}
	if _, err := store.GetModuleActivationByIdentity(
		context.Background(),
		"tenant-target",
		"instance-target",
		3,
	); !errors.Is(err, ErrModuleActivationNotFound) {
		t.Fatalf("missing exact activation error=%v", err)
	}
	if _, err := store.GetModuleActivationByIdentity(
		context.Background(),
		"tenant-target",
		"instance-target",
		0,
	); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("zero exact activation revision error=%v", err)
	}

	if _, err := store.GetLatestModuleActivationForInstance(
		context.Background(),
		"tenant-target",
		"instance-missing",
	); !errors.Is(err, ErrModuleActivationNotFound) {
		t.Fatalf("missing activation error=%v", err)
	}
	for _, test := range []struct {
		name       string
		ctx        context.Context
		tenantID   string
		instanceID string
	}{
		{
			name: "nil context", ctx: nil,
			tenantID: "tenant-target", instanceID: "instance-target",
		},
		{
			name: "invalid tenant ID", ctx: context.Background(),
			tenantID: " tenant-target", instanceID: "instance-target",
		},
		{
			name: "invalid instance ID", ctx: context.Background(),
			tenantID: "tenant-target", instanceID: "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := store.GetLatestModuleActivationForInstance(
				test.ctx,
				test.tenantID,
				test.instanceID,
			); !errors.Is(err, ErrInvalidModule) {
				t.Fatalf("error=%v want ErrInvalidModule", err)
			}
		})
	}
}

func TestActivateModuleRejectsConflictMissingInstallAndUnsupportedClass(t *testing.T) {
	store := openModuleTestStore(t)
	if _, err := store.ActivateModule(
		context.Background(),
		ActivateModuleInput{
			ActivationID:       "activation-missing",
			TenantID:           "tenant",
			InstanceID:         "instance",
			InstallationID:     "missing-installation",
			ActivationRevision: 1,
			ExecutionClass:     moduleapi.ExecutionDeclarative,
			AdapterIdentity:    "adapter",
		},
	); !errors.Is(err, ErrModuleInstallationNotFound) {
		t.Fatalf("missing installation error=%v", err)
	}

	manifest := canonicalModuleManifest(
		t,
		"demo.activate",
		"v1",
		moduleapi.RuntimeModeRequestDeclarative,
		nil,
	)
	installation, err := store.InstallModule(
		context.Background(),
		installInput(
			t,
			"install-activate",
			manifest,
			strings.Repeat("1", 64),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	base := ActivateModuleInput{
		ActivationID:       "activation-base",
		TenantID:           "tenant",
		InstanceID:         "instance",
		InstallationID:     installation.InstallationID,
		ActivationRevision: 1,
		ExecutionClass:     moduleapi.ExecutionDeclarative,
		AdapterIdentity:    "adapter.one",
	}
	if _, err := store.ActivateModule(
		context.Background(),
		base,
	); err != nil {
		t.Fatal(err)
	}
	conflict := base
	conflict.ActivationID = "activation-conflict"
	conflict.AdapterIdentity = "adapter.two"
	if _, err := store.ActivateModule(
		context.Background(),
		conflict,
	); !errors.Is(err, ErrModuleConflict) {
		t.Fatalf("activation conflict error=%v", err)
	}

	unsupported := base
	unsupported.ActivationID = "activation-unsupported"
	unsupported.InstanceID = "instance-two"
	unsupported.ExecutionClass = moduleapi.ExecutionClass("SANDBOX")
	if _, err := store.ActivateModule(
		context.Background(),
		unsupported,
	); !errors.Is(err, ErrInvalidModule) {
		t.Fatalf("unsupported class error=%v", err)
	}
	if _, err := store.GetModuleActivation(
		context.Background(),
		unsupported.ActivationID,
	); !errors.Is(err, ErrModuleActivationNotFound) {
		t.Fatalf("unsupported class left activation: %v", err)
	}
}

func TestModuleMethodsRejectClosedStore(t *testing.T) {
	store := openModuleTestStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InstallModule(
		context.Background(),
		InstallModuleInput{},
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed InstallModule error=%v", err)
	}
	if _, err := store.GetModuleInstallation(
		context.Background(),
		"installation",
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed GetModuleInstallation error=%v", err)
	}
	if _, err := store.GetModuleInstallationByIdentity(
		context.Background(),
		"demo.installation",
		"v1",
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed GetModuleInstallationByIdentity error=%v", err)
	}
	if _, err := store.ActivateModule(
		context.Background(),
		ActivateModuleInput{},
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed ActivateModule error=%v", err)
	}
	if _, err := store.GetModuleActivation(
		context.Background(),
		"activation",
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed GetModuleActivation error=%v", err)
	}
	if _, err := store.GetLatestModuleActivationForInstance(
		context.Background(),
		"tenant",
		"instance",
	); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed GetLatestModuleActivationForInstance error=%v", err)
	}
}

func openModuleTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "module.sqlite")
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

func installInput(
	t *testing.T,
	installationID string,
	manifest []byte,
	artifactDigest string,
) InstallModuleInput {
	t.Helper()
	parsed, _, err := moduleapi.ParseModuleManifestV1(manifest)
	if err != nil {
		t.Fatalf("ParseModuleManifestV1: %v", err)
	}
	manifestDigest, err := ComputeContentDigest(
		ContentModuleManifest,
		moduleManifestMediaType,
		manifest,
	)
	if err != nil {
		t.Fatalf("ComputeContentDigest: %v", err)
	}
	return InstallModuleInput{
		InstallationID:      installationID,
		ModuleID:            parsed.ID,
		ExactVersion:        parsed.Version,
		ExpectedManifestRef: manifestDigest,
		ManifestBytes:       bytes.Clone(manifest),
		ArtifactDigest:      artifactDigest,
	}
}

func canonicalModuleManifest(
	t *testing.T,
	moduleID string,
	version string,
	mode moduleapi.RuntimeModeRequest,
	extra map[string]any,
) []byte {
	t.Helper()
	protocol := moduleapi.RuntimeProtocolStaticV1
	entrypoint := "content/value.json"
	if mode == moduleapi.RuntimeModeRequestTrustedInProcess {
		protocol = moduleapi.RuntimeProtocolGoInProcessV1
		entrypoint = "builtin.demo"
	} else if mode == moduleapi.RuntimeModeRequestLocalProcess {
		protocol = moduleapi.RuntimeProtocolMCPStdio20251125
		entrypoint = "content/mcp-server"
	} else if mode == moduleapi.RuntimeModeRequestRemote {
		protocol = moduleapi.RuntimeProtocolFreeAgentActionHTTPV1
		entrypoint = "content/remote-action.json"
	} else if mode == moduleapi.RuntimeModeRequestWASM {
		protocol = moduleapi.RuntimeProtocolFreeAgentActionWASMV1
		entrypoint = "content/wasm-action.json"
	}
	providedPort := moduleapi.PortNameModelGenerate
	providedVersion := moduleapi.PortVersionV2
	if mode == moduleapi.RuntimeModeRequestLocalProcess ||
		mode == moduleapi.RuntimeModeRequestRemote ||
		mode == moduleapi.RuntimeModeRequestWASM {
		providedPort = moduleapi.PortNameActionProvider
		providedVersion = moduleapi.PortVersionV1
	}
	value := map[string]any{
		"api_version": moduleapi.ModuleManifestAPIVersionV1,
		"id":          moduleID,
		"version":     version,
		"runtime": map[string]any{
			"mode":       string(mode),
			"protocol":   protocol,
			"entrypoint": entrypoint,
		},
		"provides": []any{
			map[string]any{
				"name":          providedPort,
				"exact_version": providedVersion,
			},
		},
	}
	for key, item := range extra {
		value[key] = item
	}
	encoded, err := moduleapi.CanonicalJSON(mustJSON(t, value))
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	return encoded
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return encoded
}
