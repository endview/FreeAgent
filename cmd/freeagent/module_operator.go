package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleListResultSchemaV1    = "freeagent.module-binding-list/v1"
	moduleHistoryResultSchemaV1 = "freeagent.module-binding-history/v1"
	moduleInspectResultSchemaV1 = "freeagent.module-inspection/v1"
)

// moduleOperatorSourceV1 contains exact package identity only. Manifest
// policy bodies, host filesystem paths and package content deliberately never
// cross the default operator output boundary.
type moduleOperatorSourceV1 struct {
	InstallationID string `json:"installation_id"`
	ID             string `json:"id"`
	ExactVersion   string `json:"exact_version"`
	ArtifactDigest string `json:"artifact_digest"`
	ManifestRef    string `json:"manifest_ref"`
}

type moduleOperatorActivationV1 struct {
	InstanceID         string                   `json:"instance_id"`
	ExecutionClass     moduleapi.ExecutionClass `json:"execution_class"`
	AdapterIdentity    string                   `json:"adapter_identity"`
	ActivationRevision uint64                   `json:"activation_revision"`
}

type moduleOperatorBindingV1 struct {
	BindingTarget       moduleApplyBindingTargetV1 `json:"binding_target"`
	Port                moduleapi.PortRef          `json:"port"`
	PortBindingIndex    uint32                     `json:"port_binding_index"`
	Source              moduleOperatorSourceV1     `json:"source"`
	Activation          moduleOperatorActivationV1 `json:"activation"`
	ConfigRef           string                     `json:"config_ref"`
	AuthorityCeilingRef string                     `json:"authority_ceiling_ref"`
	StaticContextRefs   []string                   `json:"static_context_refs"`
	FailurePolicy       moduleapi.FailurePolicy    `json:"failure_policy"`
}

type moduleListResultV1 struct {
	SchemaVersion string                         `json:"schema_version"`
	Basis         controlcontract.PublishedBasis `json:"basis"`
	BindingTarget *moduleApplyBindingTargetV1    `json:"binding_target,omitempty"`
	Bindings      []moduleOperatorBindingV1      `json:"bindings"`
}

// moduleHistoryResultV1 is an exact historical Control/Catalog projection.
// A historical revision has no historical current-pointer claim, so the
// result deliberately carries the two immutable refs instead of fabricating
// a PublishedBasis or consulting today's current pointer.
type moduleHistoryResultV1 struct {
	SchemaVersion string                               `json:"schema_version"`
	TenantID      string                               `json:"tenant_id"`
	Control       controlcontract.ControlSnapshotRef   `json:"control"`
	Catalog       controlcontract.CatalogGenerationRef `json:"catalog"`
	BindingTarget *moduleApplyBindingTargetV1          `json:"binding_target,omitempty"`
	Bindings      []moduleOperatorBindingV1            `json:"bindings"`
}

type moduleOperatorRuntimeV1 struct {
	Mode       moduleapi.RuntimeModeRequest `json:"mode"`
	Protocol   string                       `json:"protocol"`
	Entrypoint string                       `json:"entrypoint"`
}

type moduleOperatorManifestSummaryV1 struct {
	APIVersion           string                  `json:"api_version"`
	Runtime              moduleOperatorRuntimeV1 `json:"runtime"`
	Provides             []moduleapi.PortRef     `json:"provides"`
	Requires             []moduleapi.PortRef     `json:"requires"`
	RequestedPermissions []moduleapi.Permission  `json:"requested_permissions"`
}

type moduleInspectResultV1 struct {
	SchemaVersion     string                          `json:"schema_version"`
	Basis             controlcontract.PublishedBasis  `json:"basis"`
	Source            moduleOperatorSourceV1          `json:"source"`
	Activation        moduleOperatorActivationV1      `json:"activation"`
	CatalogProvides   []moduleapi.PortRef             `json:"catalog_provides"`
	ArtifactSizeBytes uint64                          `json:"artifact_size_bytes"`
	Manifest          moduleOperatorManifestSummaryV1 `json:"manifest"`
	Bindings          []moduleOperatorBindingV1       `json:"bindings"`
}

type moduleOperatorObservedV1 struct {
	basis   controlcontract.PublishedBasis
	control controlcontract.ControlSnapshot
	catalog controlcontract.CatalogGeneration
}

func runModuleList(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if ctx == nil {
		return errors.New("freeagent module-list: failed (INVALID_FLAGS)")
	}
	flags := newFlagSet("module-list", io.Discard)
	databasePath := flags.String("db", "", "existing Current Store database path")
	tenantID := flags.String("tenant", "", "required exact tenant identity")
	profileID := flags.String("profile", "", "optional exact profile identity")
	workspaceID := flags.String("workspace", "", "optional exact Channel Workspace identity")
	endpointID := flags.String("endpoint", "", "optional exact Channel Endpoint identity")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 ||
		strings.TrimSpace(*databasePath) == "" ||
		validateModuleApplyOpaqueIDV1("tenant_id", *tenantID) != nil {
		return errors.New("freeagent module-list: failed (INVALID_FLAGS)")
	}
	filter, err := moduleOperatorBindingFilterV1(*profileID, *workspaceID, *endpointID)
	if err != nil {
		return errors.New("freeagent module-list: failed (INVALID_FLAGS)")
	}

	observer, observed, err := openModuleOperatorObservationV1(
		ctx,
		*databasePath,
		*tenantID,
	)
	if err != nil {
		return errors.New("freeagent module-list: failed (STORE_INVALID)")
	}
	result, err := buildModuleListResultV1(
		ctx,
		observer,
		observed,
		filter,
	)
	closeErr := observer.Close()
	if err != nil || closeErr != nil {
		if errors.Is(err, errModuleOperatorTargetNotFoundV1) {
			return errors.New("freeagent module-list: failed (TARGET_NOT_FOUND)")
		}
		return errors.New("freeagent module-list: failed (STORE_INVALID)")
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return errors.New("freeagent module-list: failed (INTERNAL_ERROR)")
	}
	return nil
}

func runModuleHistory(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if ctx == nil {
		return errors.New("freeagent module-history: failed (INVALID_FLAGS)")
	}
	flags := newFlagSet("module-history", io.Discard)
	databasePath := flags.String("db", "", "existing Current Store database path")
	tenantID := flags.String("tenant", "", "required exact tenant identity")
	controlRevision := flags.Uint64(
		"control-revision",
		0,
		"exact historical Control revision",
	)
	catalogGeneration := flags.Uint64(
		"catalog-generation",
		0,
		"exact historical Catalog generation",
	)
	profileID := flags.String("profile", "", "optional exact historical profile identity")
	workspaceID := flags.String("workspace", "", "optional exact historical Channel Workspace identity")
	endpointID := flags.String("endpoint", "", "optional exact historical Channel Endpoint identity")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 ||
		strings.TrimSpace(*databasePath) == "" ||
		validateModuleApplyOpaqueIDV1("tenant_id", *tenantID) != nil ||
		*controlRevision == 0 || *controlRevision > math.MaxInt64 ||
		*catalogGeneration == 0 || *catalogGeneration > math.MaxInt64 {
		return errors.New("freeagent module-history: failed (INVALID_FLAGS)")
	}
	filter, err := moduleOperatorBindingFilterV1(*profileID, *workspaceID, *endpointID)
	if err != nil {
		return errors.New("freeagent module-history: failed (INVALID_FLAGS)")
	}

	observer, err := currentstore.OpenReadOnlyObserver(ctx, *databasePath)
	if err != nil {
		return errors.New("freeagent module-history: failed (STORE_INVALID)")
	}
	result, err := buildModuleHistoryResultV1(
		ctx,
		observer,
		*tenantID,
		*controlRevision,
		*catalogGeneration,
		filter,
	)
	closeErr := observer.Close()
	if err != nil || closeErr != nil {
		return mapModuleHistoryReadErrorV1(err, closeErr)
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return errors.New("freeagent module-history: failed (INTERNAL_ERROR)")
	}
	return nil
}

func mapModuleHistoryReadErrorV1(readErr error, closeErr error) error {
	// A close failure means the observer lifecycle did not complete cleanly.
	// Never let a more specific read result hide that Store-level failure.
	if closeErr != nil {
		return errors.New("freeagent module-history: failed (STORE_INVALID)")
	}
	switch {
	case errors.Is(readErr, currentstore.ErrPublishedBasisNotFound):
		return errors.New("freeagent module-history: failed (HISTORY_NOT_FOUND)")
	case errors.Is(readErr, errModuleOperatorTargetNotFoundV1):
		return errors.New("freeagent module-history: failed (TARGET_NOT_FOUND)")
	default:
		return errors.New("freeagent module-history: failed (STORE_INVALID)")
	}
}

func runModuleInspect(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if ctx == nil {
		return errors.New("freeagent module-inspect: failed (INVALID_FLAGS)")
	}
	flags := newFlagSet("module-inspect", io.Discard)
	databasePath := flags.String("db", "", "existing Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "existing content-addressed artifact root")
	tenantID := flags.String("tenant", "", "required exact tenant identity")
	instanceID := flags.String("instance", "", "exact current Catalog instance identity")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 ||
		strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*artifactRoot) == "" ||
		validateModuleApplyOpaqueIDV1("tenant_id", *tenantID) != nil ||
		validateModuleApplyOpaqueIDV1("instance_id", *instanceID) != nil {
		return errors.New("freeagent module-inspect: failed (INVALID_FLAGS)")
	}

	root, err := resolveExistingArtifactRootV1(*artifactRoot)
	if err != nil {
		return errors.New("freeagent module-inspect: failed (ARTIFACT_INVALID)")
	}
	observer, observed, err := openModuleOperatorObservationV1(
		ctx,
		*databasePath,
		*tenantID,
	)
	if err != nil {
		return errors.New("freeagent module-inspect: failed (STORE_INVALID)")
	}
	result, err := buildModuleInspectResultV1(
		ctx,
		observer,
		observed,
		root,
		*instanceID,
	)
	closeErr := observer.Close()
	if err != nil || closeErr != nil {
		if errors.Is(err, errModuleOperatorInstanceNotFoundV1) {
			return errors.New("freeagent module-inspect: failed (INSTANCE_NOT_FOUND)")
		}
		if errors.Is(err, errModuleOperatorArtifactInvalidV1) {
			return errors.New("freeagent module-inspect: failed (ARTIFACT_INVALID)")
		}
		return errors.New("freeagent module-inspect: failed (STORE_INVALID)")
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return errors.New("freeagent module-inspect: failed (INTERNAL_ERROR)")
	}
	return nil
}

func runModuleDisable(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if ctx == nil {
		return errors.New("freeagent module-disable: failed (INVALID_FLAGS)")
	}
	flags := newFlagSet("module-disable", io.Discard)
	databasePath := flags.String("db", "", "existing Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "existing content-addressed artifact root")
	planPath := flags.String("plan", "", "exact canonical DISABLED module apply plan")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 ||
		strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*artifactRoot) == "" || strings.TrimSpace(*planPath) == "" {
		return errors.New("freeagent module-disable: failed (INVALID_FLAGS)")
	}
	plan, canonical, digest, err := readModuleApplyPlanV1(*planPath)
	if err != nil || plan.DesiredState != moduleApplyDisabledV1 {
		return errors.New("freeagent module-disable: failed (PLAN_INVALID)")
	}
	result, err := applyModulePlanV1(ctx, moduleApplyCommandInputV1{
		DatabasePath:  *databasePath,
		ArtifactRoot:  *artifactRoot,
		Plan:          plan,
		PlanCanonical: canonical,
		PlanDigest:    digest,
	})
	if err != nil {
		return fmt.Errorf(
			"freeagent module-disable: failed (%s)",
			moduleApplyFailureCodeOfV1(err),
		)
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return errors.New("freeagent module-disable: failed (INTERNAL_ERROR)")
	}
	return nil
}

var (
	errModuleOperatorTargetNotFoundV1   = errors.New("module operator: binding target not found")
	errModuleOperatorInstanceNotFoundV1 = errors.New("module operator: instance not found")
	errModuleOperatorArtifactInvalidV1  = errors.New("module operator: artifact invalid")
)

func moduleOperatorBindingFilterV1(
	profileID string,
	workspaceID string,
	endpointID string,
) (*moduleApplyBindingTargetV1, error) {
	switch {
	case profileID == "" && workspaceID == "" && endpointID == "":
		return nil, nil
	case profileID != "" && workspaceID == "" && endpointID == "":
		if err := validateModuleApplyOpaqueIDV1("profile_id", profileID); err != nil {
			return nil, err
		}
		return &moduleApplyBindingTargetV1{
			Kind:      moduleApplyBindingTargetProfileV1,
			ProfileID: profileID,
		}, nil
	case profileID == "" && workspaceID != "" && endpointID != "":
		if err := validateModuleApplyOpaqueIDV1("workspace_id", workspaceID); err != nil {
			return nil, err
		}
		if err := validateModuleApplyOpaqueIDV1("endpoint_id", endpointID); err != nil {
			return nil, err
		}
		return &moduleApplyBindingTargetV1{
			Kind:        moduleApplyBindingTargetWorkspaceChannelEndpointV1,
			WorkspaceID: workspaceID,
			EndpointID:  endpointID,
		}, nil
	default:
		return nil, errors.New("profile and Workspace/Endpoint filters are mutually exclusive")
	}
}

func openModuleOperatorObservationV1(
	ctx context.Context,
	databasePath string,
	tenantID string,
) (*currentstore.ReadOnlyObserver, moduleOperatorObservedV1, error) {
	observer, err := currentstore.OpenReadOnlyObserver(ctx, databasePath)
	if err != nil {
		return nil, moduleOperatorObservedV1{}, err
	}
	basis, control, catalog, err := observer.LoadPublishedBasis(ctx, tenantID)
	if err != nil {
		_ = observer.Close()
		return nil, moduleOperatorObservedV1{}, err
	}
	return observer, moduleOperatorObservedV1{
		basis:   basis,
		control: control,
		catalog: catalog,
	}, nil
}

func buildModuleListResultV1(
	ctx context.Context,
	observer *currentstore.ReadOnlyObserver,
	observed moduleOperatorObservedV1,
	filter *moduleApplyBindingTargetV1,
) (moduleListResultV1, error) {
	if !moduleOperatorTargetExistsV1(observed.control, filter) {
		return moduleListResultV1{}, errModuleOperatorTargetNotFoundV1
	}
	bindings, err := collectModuleOperatorBindingsV1(
		ctx,
		observer,
		observed.catalog,
		observed.control,
		filter,
		"",
	)
	if err != nil {
		return moduleListResultV1{}, err
	}
	return moduleListResultV1{
		SchemaVersion: moduleListResultSchemaV1,
		Basis:         observed.basis,
		BindingTarget: cloneModuleOperatorTargetV1(filter),
		Bindings:      bindings,
	}, nil
}

func buildModuleHistoryResultV1(
	ctx context.Context,
	observer *currentstore.ReadOnlyObserver,
	tenantID string,
	controlRevision uint64,
	catalogGeneration uint64,
	filter *moduleApplyBindingTargetV1,
) (moduleHistoryResultV1, error) {
	control, catalog, err := observer.LoadControlCatalogRevision(
		ctx,
		tenantID,
		controlRevision,
		catalogGeneration,
	)
	if err != nil {
		return moduleHistoryResultV1{}, err
	}
	if !moduleOperatorTargetExistsV1(control, filter) {
		return moduleHistoryResultV1{}, errModuleOperatorTargetNotFoundV1
	}
	bindings, err := collectModuleOperatorBindingsV1(
		ctx,
		observer,
		catalog,
		control,
		filter,
		"",
	)
	if err != nil {
		return moduleHistoryResultV1{}, err
	}
	return moduleHistoryResultV1{
		SchemaVersion: moduleHistoryResultSchemaV1,
		TenantID:      tenantID,
		Control: controlcontract.ControlSnapshotRef{
			SnapshotID: control.SnapshotID,
			Revision:   control.Revision,
			Digest:     control.Digest,
		},
		Catalog: controlcontract.CatalogGenerationRef{
			GenerationID: catalog.GenerationID,
			Generation:   catalog.Generation,
			Digest:       catalog.Digest,
		},
		BindingTarget: cloneModuleOperatorTargetV1(filter),
		Bindings:      bindings,
	}, nil
}

func buildModuleInspectResultV1(
	ctx context.Context,
	observer *currentstore.ReadOnlyObserver,
	observed moduleOperatorObservedV1,
	artifactRoot string,
	instanceID string,
) (moduleInspectResultV1, error) {
	entry, found := observed.catalog.FindInstance(instanceID)
	if !found {
		return moduleInspectResultV1{}, errModuleOperatorInstanceNotFoundV1
	}
	source, manifest, err := loadModuleOperatorSourceV1(ctx, observer, entry)
	if err != nil {
		return moduleInspectResultV1{}, err
	}
	artifactDirectory := filepath.Join(artifactRoot, source.ArtifactDigest)
	report, err := moduleconformance.VerifyDirectory(ctx, artifactDirectory)
	if err != nil {
		return moduleInspectResultV1{}, errors.Join(
			errModuleOperatorArtifactInvalidV1,
			err,
		)
	}
	artifactManifest, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return moduleInspectResultV1{}, errors.Join(
			errModuleOperatorArtifactInvalidV1,
			err,
		)
	}
	installation, err := observer.GetModuleInstallationByIdentity(
		ctx,
		source.ID,
		source.ExactVersion,
	)
	if err != nil || !bytes.Equal(artifactManifest, installation.ManifestBytes) ||
		report.Module.ID != source.ID ||
		report.Module.ExactVersion != source.ExactVersion ||
		report.ArtifactDigest != source.ArtifactDigest {
		return moduleInspectResultV1{}, errors.Join(
			errModuleOperatorArtifactInvalidV1,
			err,
			errors.New("artifact does not close to current Catalog installation"),
		)
	}
	bindings, err := collectModuleOperatorBindingsV1(
		ctx,
		observer,
		observed.catalog,
		observed.control,
		nil,
		instanceID,
	)
	if err != nil {
		return moduleInspectResultV1{}, err
	}

	provides := cloneAndSortModuleOperatorPortsV1(manifest.Provides)
	requires := cloneAndSortModuleOperatorPortsV1(manifest.Requires)
	permissions := append([]moduleapi.Permission{}, manifest.RequestedPermissions...)
	sort.Slice(permissions, func(left, right int) bool {
		return string(permissions[left]) < string(permissions[right])
	})
	return moduleInspectResultV1{
		SchemaVersion:     moduleInspectResultSchemaV1,
		Basis:             observed.basis,
		Source:            source,
		Activation:        moduleOperatorActivationFromEntryV1(entry),
		CatalogProvides:   cloneAndSortModuleOperatorPortsV1(entry.Provides),
		ArtifactSizeBytes: report.ArtifactSizeBytes,
		Manifest: moduleOperatorManifestSummaryV1{
			APIVersion: manifest.APIVersion,
			Runtime: moduleOperatorRuntimeV1{
				Mode:       manifest.Runtime.Mode,
				Protocol:   manifest.Runtime.Protocol,
				Entrypoint: manifest.Runtime.Entrypoint,
			},
			Provides:             provides,
			Requires:             requires,
			RequestedPermissions: permissions,
		},
		Bindings: bindings,
	}, nil
}

func collectModuleOperatorBindingsV1(
	ctx context.Context,
	observer *currentstore.ReadOnlyObserver,
	catalog controlcontract.CatalogGeneration,
	control controlcontract.ControlSnapshot,
	filter *moduleApplyBindingTargetV1,
	instanceFilter string,
) ([]moduleOperatorBindingV1, error) {
	result := make([]moduleOperatorBindingV1, 0)
	sources := make(map[string]moduleOperatorSourceV1)
	manifests := make(map[string]moduleapi.ModuleManifestV1)
	appendBinding := func(
		target moduleApplyBindingTargetV1,
		binding controlcontract.BindingSpec,
		index uint32,
	) error {
		if filter != nil && target != *filter {
			return nil
		}
		if instanceFilter != "" && binding.InstanceID != instanceFilter {
			return nil
		}
		entry, found := catalog.FindInstance(binding.InstanceID)
		if !found || !moduleOperatorPortPresentV1(entry.Provides, binding.Port) {
			return errors.New("module operator: Binding is not closed by current Catalog")
		}
		key := entry.Activation.ModuleID + "\x00" + entry.Activation.Version
		source, loaded := sources[key]
		manifest := manifests[key]
		var err error
		if !loaded {
			source, manifest, err = loadModuleOperatorSourceV1(ctx, observer, entry)
			if err != nil {
				return err
			}
			sources[key] = source
			manifests[key] = manifest
		}
		if source.ID != entry.Activation.ModuleID ||
			source.ExactVersion != entry.Activation.Version ||
			source.ArtifactDigest != entry.Activation.ArtifactDigest ||
			!moduleOperatorExecutionMatchesRuntimeV1(
				entry.Activation.ExecutionClass,
				manifest.Runtime.Mode,
			) || !moduleOperatorSameExactPortSetV1(manifest.Provides, entry.Provides) ||
			!moduleOperatorPortPresentV1(manifest.Provides, binding.Port) {
			return errors.New("module operator: Binding does not close to installed manifest")
		}
		result = append(result, moduleOperatorBindingV1{
			BindingTarget:       target,
			Port:                binding.Port,
			PortBindingIndex:    index,
			Source:              source,
			Activation:          moduleOperatorActivationFromEntryV1(entry),
			ConfigRef:           binding.ConfigRef,
			AuthorityCeilingRef: binding.AuthorityCeilingRef,
			StaticContextRefs:   append([]string{}, binding.StaticContextRefs...),
			FailurePolicy:       binding.FailurePolicy,
		})
		return nil
	}
	for _, profile := range control.Profiles {
		portIndices := make(map[string]uint32)
		for _, binding := range profile.Bindings {
			portKey, err := binding.Port.CanonicalKey()
			if err != nil {
				return nil, err
			}
			index := portIndices[portKey]
			portIndices[portKey] = index + 1
			if err := appendBinding(moduleApplyBindingTargetV1{
				Kind:      moduleApplyBindingTargetProfileV1,
				ProfileID: profile.Profile.ID,
			}, binding, index); err != nil {
				return nil, err
			}
		}
	}
	for _, workspace := range control.Workspaces {
		for _, endpoint := range workspace.ChannelEndpoints {
			if err := appendBinding(moduleApplyBindingTargetV1{
				Kind:        moduleApplyBindingTargetWorkspaceChannelEndpointV1,
				WorkspaceID: workspace.Workspace.ID,
				EndpointID:  endpoint.EndpointID,
			}, endpoint.Binding, 0); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		leftKey := moduleOperatorBindingSortKeyV1(result[left])
		rightKey := moduleOperatorBindingSortKeyV1(result[right])
		return leftKey < rightKey
	})
	return result, nil
}

func moduleOperatorTargetExistsV1(
	control controlcontract.ControlSnapshot,
	target *moduleApplyBindingTargetV1,
) bool {
	if target == nil {
		return true
	}
	switch target.Kind {
	case moduleApplyBindingTargetProfileV1:
		_, found := control.FindProfile(target.ProfileID)
		return found
	case moduleApplyBindingTargetWorkspaceChannelEndpointV1:
		workspace, found := control.FindWorkspace(target.WorkspaceID)
		if !found {
			return false
		}
		_, found = workspace.FindChannelEndpoint(target.EndpointID)
		return found
	default:
		return false
	}
}

func cloneModuleOperatorTargetV1(
	target *moduleApplyBindingTargetV1,
) *moduleApplyBindingTargetV1 {
	if target == nil {
		return nil
	}
	cloned := *target
	return &cloned
}

func moduleOperatorBindingSortKeyV1(binding moduleOperatorBindingV1) string {
	target := binding.BindingTarget
	return string(target.Kind) + "\x00" + target.ProfileID + "\x00" +
		target.WorkspaceID + "\x00" + target.EndpointID + "\x00" +
		binding.Port.Name + "\x00" + binding.Port.ExactVersion + "\x00" +
		fmt.Sprintf("%010d", binding.PortBindingIndex) + "\x00" +
		binding.Activation.InstanceID
}

func loadModuleOperatorSourceV1(
	ctx context.Context,
	observer *currentstore.ReadOnlyObserver,
	entry controlcontract.CatalogEntry,
) (moduleOperatorSourceV1, moduleapi.ModuleManifestV1, error) {
	installation, err := observer.GetModuleInstallationByIdentity(
		ctx,
		entry.Activation.ModuleID,
		entry.Activation.Version,
	)
	if err != nil {
		return moduleOperatorSourceV1{}, moduleapi.ModuleManifestV1{}, err
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(
		installation.ManifestBytes,
	)
	if err != nil || !bytes.Equal(canonical, installation.ManifestBytes) ||
		installation.ModuleID != entry.Activation.ModuleID ||
		installation.ExactVersion != entry.Activation.Version ||
		installation.ArtifactDigest != entry.Activation.ArtifactDigest ||
		manifest.ID != installation.ModuleID ||
		manifest.Version != installation.ExactVersion ||
		!moduleOperatorExecutionMatchesRuntimeV1(
			entry.Activation.ExecutionClass,
			manifest.Runtime.Mode,
		) {
		return moduleOperatorSourceV1{}, moduleapi.ModuleManifestV1{}, errors.Join(
			err,
			errors.New("module operator: Catalog installation closure is invalid"),
		)
	}
	if !moduleOperatorSameExactPortSetV1(manifest.Provides, entry.Provides) {
		return moduleOperatorSourceV1{}, moduleapi.ModuleManifestV1{},
			errors.New("module operator: Catalog Provides differ from installed manifest")
	}
	return moduleOperatorSourceV1{
		InstallationID: installation.InstallationID,
		ID:             installation.ModuleID,
		ExactVersion:   installation.ExactVersion,
		ArtifactDigest: installation.ArtifactDigest,
		ManifestRef:    installation.ManifestRef,
	}, manifest, nil
}

func moduleOperatorExecutionMatchesRuntimeV1(
	class moduleapi.ExecutionClass,
	mode moduleapi.RuntimeModeRequest,
) bool {
	switch mode {
	case moduleapi.RuntimeModeRequestDeclarative:
		return class == moduleapi.ExecutionDeclarative
	case moduleapi.RuntimeModeRequestTrustedInProcess:
		return class == moduleapi.ExecutionTrustedInProcess
	case moduleapi.RuntimeModeRequestLocalProcess:
		return class == moduleapi.ExecutionLocalProcess
	case moduleapi.RuntimeModeRequestRemote:
		return class == moduleapi.ExecutionRemote
	case moduleapi.RuntimeModeRequestWASM:
		return class == moduleapi.ExecutionWASM
	default:
		return false
	}
}

func moduleOperatorPortPresentV1(
	ports []moduleapi.PortRef,
	wanted moduleapi.PortRef,
) bool {
	for _, port := range ports {
		if port == wanted {
			return true
		}
	}
	return false
}

func moduleOperatorSameExactPortSetV1(
	left []moduleapi.PortRef,
	right []moduleapi.PortRef,
) bool {
	if len(left) != len(right) {
		return false
	}
	remaining := make(map[moduleapi.PortRef]struct{}, len(left))
	for _, port := range left {
		if _, duplicate := remaining[port]; duplicate {
			return false
		}
		remaining[port] = struct{}{}
	}
	for _, port := range right {
		if _, found := remaining[port]; !found {
			return false
		}
		delete(remaining, port)
	}
	return len(remaining) == 0
}

func moduleOperatorActivationFromEntryV1(
	entry controlcontract.CatalogEntry,
) moduleOperatorActivationV1 {
	return moduleOperatorActivationV1{
		InstanceID:         entry.Activation.InstanceID,
		ExecutionClass:     entry.Activation.ExecutionClass,
		AdapterIdentity:    entry.Activation.AdapterIdentity,
		ActivationRevision: entry.Activation.ActivationRevision,
	}
}

func cloneAndSortModuleOperatorPortsV1(
	input []moduleapi.PortRef,
) []moduleapi.PortRef {
	ports := append([]moduleapi.PortRef{}, input...)
	sort.Slice(ports, func(left, right int) bool {
		if ports[left].Name != ports[right].Name {
			return ports[left].Name < ports[right].Name
		}
		return ports[left].ExactVersion < ports[right].ExactVersion
	})
	return ports
}

func writeCanonicalModuleCommandJSON(destination io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("freeagent: encode module command response: %w", err)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return fmt.Errorf("freeagent: canonicalize module command response: %w", err)
	}
	if _, err := destination.Write(append(canonical, '\n')); err != nil {
		return fmt.Errorf("freeagent: write module command response: %w", err)
	}
	return nil
}
