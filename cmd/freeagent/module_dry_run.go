package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleDryRunResultSchemaV1 = "freeagent.module-dry-run-result/v1"
	moduleDryRunTempPrefixV1   = ".freeagent-module-dry-run-"
)

type moduleDryRunTempFilesystemV1 struct {
	mkdirTemp func(string, string) (string, error)
	removeAll func(string) error
	lstat     func(string) (os.FileInfo, error)
}

func productionModuleDryRunTempFilesystemV1() moduleDryRunTempFilesystemV1 {
	return moduleDryRunTempFilesystemV1{
		mkdirTemp: os.MkdirTemp,
		removeAll: os.RemoveAll,
		lstat:     os.Lstat,
	}
}

type moduleDryRunProjectionStatusV1 string

const (
	moduleDryRunProjectedNotReservedV1 moduleDryRunProjectionStatusV1 = "PROJECTED_NOT_RESERVED"
	moduleDryRunObservedCurrentV1      moduleDryRunProjectionStatusV1 = "OBSERVED_CURRENT"
)

type moduleDryRunInstallationChangeV1 string

const (
	moduleDryRunInstallationNoneV1   moduleDryRunInstallationChangeV1 = "NONE"
	moduleDryRunInstallationCreateV1 moduleDryRunInstallationChangeV1 = "CREATE"
	moduleDryRunInstallationReuseV1  moduleDryRunInstallationChangeV1 = "REUSE"
)

type moduleDryRunActivationChangeV1 string

const (
	moduleDryRunActivationNoneV1         moduleDryRunActivationChangeV1 = "NONE"
	moduleDryRunActivationCreateV1       moduleDryRunActivationChangeV1 = "CREATE"
	moduleDryRunActivationReuseCurrentV1 moduleDryRunActivationChangeV1 = "REUSE_CURRENT"
)

type moduleDryRunBindingChangeV1 string

const (
	moduleDryRunBindingNoneV1    moduleDryRunBindingChangeV1 = "NONE"
	moduleDryRunBindingInsertV1  moduleDryRunBindingChangeV1 = "INSERT"
	moduleDryRunBindingReplaceV1 moduleDryRunBindingChangeV1 = "REPLACE"
	moduleDryRunBindingRemoveV1  moduleDryRunBindingChangeV1 = "REMOVE"
)

type moduleDryRunCatalogChangeV1 string

const (
	moduleDryRunCatalogNoneV1            moduleDryRunCatalogChangeV1 = "NONE"
	moduleDryRunCatalogAddInstanceV1     moduleDryRunCatalogChangeV1 = "ADD_INSTANCE"
	moduleDryRunCatalogReplaceInstanceV1 moduleDryRunCatalogChangeV1 = "REPLACE_INSTANCE"
	moduleDryRunCatalogRetainInstanceV1  moduleDryRunCatalogChangeV1 = "RETAIN_INSTANCE"
	moduleDryRunCatalogRemoveInstanceV1  moduleDryRunCatalogChangeV1 = "REMOVE_INSTANCE"
)

// moduleDryRunCandidateBasisV1 deliberately contains only publication
// identities. A dry-run never reserves a pointer, revision, activation or
// receipt; these references are a deterministic projection of canonical
// candidate bytes.
type moduleDryRunCandidateBasisV1 struct {
	TenantID         string                               `json:"tenant_id"`
	PointerRevision  uint64                               `json:"pointer_revision"`
	Control          controlcontract.ControlSnapshotRef   `json:"control"`
	Catalog          controlcontract.CatalogGenerationRef `json:"catalog"`
	ProjectionStatus moduleDryRunProjectionStatusV1       `json:"projection_status"`
}

type moduleDryRunChangesV1 struct {
	Installation         moduleDryRunInstallationChangeV1 `json:"installation"`
	Activation           moduleDryRunActivationChangeV1   `json:"activation"`
	Binding              moduleDryRunBindingProjectionV1  `json:"binding"`
	Catalog              moduleDryRunCatalogChangeV1      `json:"catalog"`
	ChannelCursorSeedRef string                           `json:"channel_cursor_seed_ref,omitempty"`
}

type moduleDryRunBindingProjectionV1 struct {
	Change              moduleDryRunBindingChangeV1 `json:"change"`
	PortBindingIndex    *uint32                     `json:"port_binding_index,omitempty"`
	ConfigRef           string                      `json:"config_ref,omitempty"`
	AuthorityCeilingRef string                      `json:"authority_ceiling_ref,omitempty"`
	StaticContextRefs   []string                    `json:"static_context_refs,omitempty"`
	FailurePolicy       *moduleapi.FailurePolicy    `json:"failure_policy,omitempty"`
}

type moduleDryRunResultV1 struct {
	SchemaVersion           string                         `json:"schema_version"`
	Status                  moduleApplyStatusV1            `json:"status"`
	DesiredState            moduleApplyDesiredStateV1      `json:"desired_state"`
	TenantID                string                         `json:"tenant_id"`
	BindingTarget           moduleApplyBindingTargetV1     `json:"binding_target"`
	InstanceID              string                         `json:"instance_id"`
	Port                    moduleapi.PortRef              `json:"port"`
	PlanDigest              string                         `json:"plan_digest"`
	StartupRecoveryRequired bool                           `json:"startup_recovery_required"`
	ObservedBasis           controlcontract.PublishedBasis `json:"observed_basis"`
	CandidateBasis          moduleDryRunCandidateBasisV1   `json:"candidate_basis"`
	Changes                 moduleDryRunChangesV1          `json:"changes"`
	Module                  *moduleApplyResultModuleV1     `json:"module,omitempty"`
}

func runModuleDryRun(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if ctx == nil {
		return errors.New("freeagent module-dry-run: failed (INVALID_FLAGS)")
	}
	flags := newFlagSet("module-dry-run", io.Discard)
	databasePath := flags.String("db", "", "existing Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "existing content-addressed artifact root")
	planPath := flags.String("plan", "", "exact canonical module apply plan")
	artifactDirectory := flags.String("artifact", "", "unpacked module package directory for ENABLED")
	grant := flags.String(
		"allow-local-mcp-artifact",
		"",
		"operator-approved LOCAL_PROCESS MCP artifact SHA-256",
	)
	trustedGrant := flags.String(
		"allow-trusted-in-process-artifact",
		"",
		"operator-approved TRUSTED_IN_PROCESS artifact SHA-256",
	)
	remoteArtifactGrant := flags.String(
		"allow-remote-action-artifact",
		"",
		"operator-approved REMOTE Action artifact SHA-256",
	)
	wasmArtifactGrant := flags.String(
		"allow-wasm-action-artifact",
		"",
		"operator-approved WASM Action artifact SHA-256",
	)
	remoteEndpointGrant := flags.String(
		"allow-remote-action-endpoint",
		"",
		"operator-approved exact REMOTE Action endpoint URL",
	)
	remoteAuthorityReferenceGrantFlag := flags.String(
		"allow-remote-action-secret-ref",
		"",
		"operator-approved REMOTE Action SecretRef identity (never the Secret value)",
	)
	modelAuthorityReferenceGrantFlag := flags.String(
		"allow-model-secret-ref",
		"",
		"operator-approved model SecretRef identity (never the Secret value)",
	)
	if err := flags.Parse(args); err != nil {
		return errors.New("freeagent module-dry-run: failed (INVALID_FLAGS)")
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*artifactRoot) == "" || strings.TrimSpace(*planPath) == "" {
		return errors.New("freeagent module-dry-run: failed (INVALID_FLAGS)")
	}
	remoteAuthorityReferenceGrant := *remoteAuthorityReferenceGrantFlag
	modelAuthorityReferenceGrant := *modelAuthorityReferenceGrantFlag
	plan, canonical, digest, err := readModuleApplyPlanV1(*planPath)
	if err != nil {
		return errors.New("freeagent module-dry-run: failed (PLAN_INVALID)")
	}
	switch plan.DesiredState {
	case moduleApplyEnabledV1:
		policy, policyErr := resolveEnabledModuleApplyPolicyV1(plan)
		if policyErr != nil {
			return errors.New("freeagent module-dry-run: failed (PLAN_INVALID)")
		}
		if strings.TrimSpace(*artifactDirectory) == "" || plan.Module == nil {
			return errors.New("freeagent module-dry-run: failed (GRANT_REQUIRED)")
		}
		if grantErr := validateModuleApplyArtifactGrantsV1(
			policy,
			plan.Module.ArtifactDigest,
			*grant,
			*trustedGrant,
			*remoteArtifactGrant,
			*wasmArtifactGrant,
		); grantErr != nil {
			return fmt.Errorf(
				"freeagent module-dry-run: failed (%s)",
				moduleApplyArtifactGrantFailureCodeV1(grantErr),
			)
		}
		if grantErr := validateModuleApplyRemoteActionGrantsV1(
			plan,
			policy,
			*remoteEndpointGrant,
			remoteAuthorityReferenceGrant,
		); grantErr != nil {
			return fmt.Errorf(
				"freeagent module-dry-run: failed (%s)",
				moduleApplyRemoteGrantFailureCodeV1(grantErr),
			)
		}
		if grantErr := validateModuleApplyModelSecretGrantV1(plan, modelAuthorityReferenceGrant); grantErr != nil {
			return errors.New("freeagent module-dry-run: failed (GRANT_REQUIRED)")
		}
	case moduleApplyDisabledV1:
		if strings.TrimSpace(*artifactDirectory) != "" || *grant != "" ||
			*trustedGrant != "" || *remoteArtifactGrant != "" ||
			*wasmArtifactGrant != "" ||
			*remoteEndpointGrant != "" || remoteAuthorityReferenceGrant != "" ||
			modelAuthorityReferenceGrant != "" {
			return errors.New("freeagent module-dry-run: failed (INVALID_FLAGS)")
		}
	default:
		return errors.New("freeagent module-dry-run: failed (PLAN_INVALID)")
	}
	result, err := dryRunModulePlanV1(ctx, moduleApplyCommandInputV1{
		DatabasePath:                  *databasePath,
		ArtifactRoot:                  *artifactRoot,
		ArtifactDirectory:             *artifactDirectory,
		LocalMCPArtifactGrant:         *grant,
		TrustedInProcessArtifactGrant: *trustedGrant,
		RemoteActionArtifactGrant:     *remoteArtifactGrant,
		WASMActionArtifactGrant:       *wasmArtifactGrant,
		RemoteActionEndpointGrant:     *remoteEndpointGrant,
		RemoteActionSecretRefGrant:    remoteAuthorityReferenceGrant,
		ModelSecretRefGrant:           modelAuthorityReferenceGrant,
		Plan:                          plan,
		PlanCanonical:                 canonical,
		PlanDigest:                    digest,
	})
	if err != nil {
		return fmt.Errorf(
			"freeagent module-dry-run: failed (%s)",
			moduleApplyFailureCodeOfV1(err),
		)
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return errors.New("freeagent module-dry-run: failed (INTERNAL_ERROR)")
	}
	return nil
}

func dryRunModulePlanV1(
	ctx context.Context,
	input moduleApplyCommandInputV1,
) (result moduleDryRunResultV1, returnErr error) {
	if ctx == nil {
		return result, newModuleApplyFailureV1(
			moduleApplyFailureInvalidFlags,
			errors.New("context is nil"),
		)
	}
	if err := ctx.Err(); err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureCancelled, err)
	}
	canonicalPlan, canonical, planDigest, err := restoreModuleApplyPlanV1(input.PlanCanonical)
	if err != nil || !bytes.Equal(canonical, input.PlanCanonical) ||
		planDigest != input.PlanDigest {
		return result, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.Join(err, errors.New("plan identity is invalid")),
		)
	}
	input.Plan = canonicalPlan
	if err := validateModuleDryRunInputV1(input); err != nil {
		return result, err
	}
	if input.Plan.ExpectedPointerRevision >= math.MaxInt64 {
		return result, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.New("expected pointer revision is exhausted"),
		)
	}
	artifactRoot, err := resolveExistingArtifactRootV1(input.ArtifactRoot)
	if err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureArtifact, err)
	}
	databasePath, err := filepath.Abs(input.DatabasePath)
	if err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureStore, err)
	}
	databasePath, err = filepath.EvalSymlinks(databasePath)
	if err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureStore, err)
	}
	if samePath(databasePath, artifactRoot) || pathContains(databasePath, artifactRoot) ||
		pathContains(artifactRoot, databasePath) {
		return result, newModuleApplyFailureV1(
			moduleApplyFailureArtifact,
			errors.New("database and artifact root overlap"),
		)
	}

	observer, err := currentstore.OpenReadOnlyObserver(ctx, input.DatabasePath)
	if err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureStore, err)
	}
	defer func() {
		if closeErr := observer.Close(); closeErr != nil {
			returnErr = errors.Join(
				returnErr,
				newModuleApplyFailureV1(moduleApplyFailureStore, closeErr),
			)
		}
	}()
	recoveryRequired, err := observer.StartupRecoveryRequired(ctx)
	if err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureRecovery, err)
	}
	if input.Plan.DesiredState == moduleApplyDisabledV1 {
		return dryRunDisabledModulePlanOnViewV1(
			ctx,
			observer,
			artifactRoot,
			input,
			recoveryRequired,
		)
	}
	basis, control, catalog, err := observer.LoadPublishedBasis(ctx, input.Plan.TenantID)
	if err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureStore, err)
	}
	if err := verifyCurrentCatalogArtifactRootV1(
		ctx,
		artifactRoot,
		catalog,
		"",
	); err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureArtifact, err)
	}
	observed, err := evaluateObservedModuleApplyV1(
		ctx,
		observer,
		artifactRoot,
		input.Plan,
		input.PlanDigest,
		basis,
		control,
		catalog,
	)
	if err != nil {
		return result, err
	}
	if !observed.NeedsCandidate {
		return newModuleDryRunObservedResultV1(
			input.Plan,
			input.PlanDigest,
			basis,
			observed.Status,
			recoveryRequired,
		), nil
	}

	var publication currentstore.PublishControlCatalogInput
	changes := moduleDryRunChangesV1{
		Installation: moduleDryRunInstallationNoneV1,
		Activation:   moduleDryRunActivationNoneV1,
		Binding: moduleDryRunBindingProjectionV1{
			Change: moduleDryRunBindingNoneV1,
		},
		Catalog: moduleDryRunCatalogNoneV1,
	}
	var prepared moduleApplyPreparedEnabledV1
	prepared, err = prepareEnabledModuleDryRunV1(
		ctx,
		observer,
		artifactRoot,
		input,
		basis,
		control,
		catalog,
	)
	if err == nil {
		publication = prepared.Publication
		changes.Installation = moduleDryRunInstallationReuseV1
		if prepared.InstallMissing {
			changes.Installation = moduleDryRunInstallationCreateV1
		}
		changes.Activation = moduleDryRunActivationReuseCurrentV1
		if prepared.ActivateMissing {
			changes.Activation = moduleDryRunActivationCreateV1
		}
		index := input.Plan.Binding.PortBindingIndex
		policy := input.Plan.Binding.FailurePolicy
		bindingChange := moduleDryRunBindingInsertV1
		if input.Plan.Port == productionModelPort ||
			input.Plan.ReplaceCurrentInstanceID != "" {
			bindingChange = moduleDryRunBindingReplaceV1
		}
		changes.Binding = moduleDryRunBindingProjectionV1{
			Change:              bindingChange,
			PortBindingIndex:    &index,
			ConfigRef:           prepared.ConfigRef,
			AuthorityCeilingRef: prepared.AuthorityRef,
			StaticContextRefs:   append([]string{}, prepared.StaticContextRefs...),
			FailurePolicy:       &policy,
		}
		if input.Plan.ReplaceCurrentInstanceID != "" {
			changes.Catalog = moduleDryRunCatalogReplaceInstanceV1
		} else if _, present := catalog.FindInstance(input.Plan.InstanceID); present {
			changes.Catalog = moduleDryRunCatalogRetainInstanceV1
		} else {
			changes.Catalog = moduleDryRunCatalogAddInstanceV1
		}
		if moduleApplyTargetsChannelV1(input.Plan) {
			changes.ChannelCursorSeedRef, err = currentstore.ComputeContentDigest(
				currentstore.ContentChannelCursor,
				moduleApplyJSONMediaType,
				input.Plan.CursorSeed,
			)
		}
	}
	if err != nil {
		var failure *moduleApplyFailureV1
		if errors.As(err, &failure) {
			return result, err
		}
		return result, newModuleApplyFailureV1(moduleApplyFailureInternal, err)
	}
	candidate, err := newModuleApplyCandidateBasisV1(input.Plan.TenantID, publication)
	if err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureInternal, err)
	}
	return newModuleDryRunProjectedResultV1(
		input.Plan,
		input.PlanDigest,
		basis,
		candidate,
		recoveryRequired,
		changes,
	), nil
}

// dryRunDisabledModulePlanOnViewV1 is the single effect-free DISABLED
// evaluator shared by the stopped-process CLI observer and the live Control
// application-service facade. The caller supplies an already-owned read view;
// this function never opens a Store, creates a stage, publishes a generation,
// performs recovery, or calls a provider.
func dryRunDisabledModulePlanOnViewV1(
	ctx context.Context,
	view moduleApplyReadViewV1,
	artifactRoot string,
	input moduleApplyCommandInputV1,
	recoveryRequired bool,
) (moduleDryRunResultV1, error) {
	if ctx == nil || view == nil {
		return moduleDryRunResultV1{}, newModuleApplyFailureV1(
			moduleApplyFailureInvalidFlags,
			errors.New("DISABLED dry-run view or context is nil"),
		)
	}
	if err := ctx.Err(); err != nil {
		return moduleDryRunResultV1{}, newModuleApplyFailureV1(
			moduleApplyFailureCancelled,
			err,
		)
	}
	plan, canonical, digest, err := restoreModuleApplyPlanV1(input.PlanCanonical)
	if err != nil || !bytes.Equal(canonical, input.PlanCanonical) ||
		digest != input.PlanDigest || plan.DesiredState != moduleApplyDisabledV1 {
		return moduleDryRunResultV1{}, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.Join(err, errors.New("DISABLED plan identity is invalid")),
		)
	}
	input.Plan = plan
	if err := validateModuleDryRunInputV1(input); err != nil {
		return moduleDryRunResultV1{}, err
	}
	if plan.ExpectedPointerRevision >= math.MaxInt64 {
		return moduleDryRunResultV1{}, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.New("expected pointer revision is exhausted"),
		)
	}
	resolvedArtifactRoot, err := resolveExistingArtifactRootV1(artifactRoot)
	if err != nil {
		return moduleDryRunResultV1{}, newModuleApplyFailureV1(
			moduleApplyFailureArtifact,
			err,
		)
	}
	disabled, err := evaluateDisabledModulePlanOnViewV1(
		ctx,
		view,
		resolvedArtifactRoot,
		plan,
		digest,
	)
	if err != nil {
		return moduleDryRunResultV1{}, err
	}
	status, err := moduleApplyStatusFromDisableDryRunV1(disabled.Status)
	if err != nil {
		return moduleDryRunResultV1{}, err
	}
	if status != moduleApplyStatusWouldApply {
		return newModuleDryRunObservedResultV1(
			plan,
			digest,
			disabled.ObservedBasis,
			status,
			recoveryRequired,
		), nil
	}
	bindingChange, err := moduleDryRunBindingFromDisableV1(disabled.BindingRemoval)
	if err != nil {
		return moduleDryRunResultV1{}, err
	}
	catalogChange, err := moduleDryRunCatalogFromDisableV1(disabled.CatalogChange)
	if err != nil {
		return moduleDryRunResultV1{}, err
	}
	return newModuleDryRunProjectedResultV1(
		plan,
		digest,
		disabled.ObservedBasis,
		disabled.CandidateBasis,
		recoveryRequired,
		moduleDryRunChangesV1{
			Installation: moduleDryRunInstallationNoneV1,
			Activation:   moduleDryRunActivationNoneV1,
			Binding:      bindingChange,
			Catalog:      catalogChange,
		},
	), nil
}

func requireUnchangedModuleDryRunBasisV1(
	ctx context.Context,
	view moduleApplyReadViewV1,
	observed controlcontract.PublishedBasis,
) error {
	current, _, _, err := view.LoadPublishedBasis(ctx, observed.TenantID)
	if err != nil {
		return newModuleApplyFailureV1(moduleApplyFailureStore, err)
	}
	if current != observed {
		return newModuleApplyFailureV1(
			moduleApplyFailurePointer,
			errors.New("PublishedBasis changed during DISABLED dry-run"),
		)
	}
	return nil
}

func validateModuleDryRunInputV1(input moduleApplyCommandInputV1) error {
	switch input.Plan.DesiredState {
	case moduleApplyEnabledV1:
		policy, policyErr := resolveEnabledModuleApplyPolicyV1(input.Plan)
		if policyErr != nil || input.Plan.Module == nil || input.Plan.Binding == nil ||
			strings.TrimSpace(input.ArtifactDirectory) == "" {
			return newModuleApplyFailureV1(
				moduleApplyFailurePlanInvalid,
				errors.Join(policyErr, errors.New("enabled input is incomplete")),
			)
		}
		if grantErr := validateModuleApplyArtifactGrantsV1(
			policy,
			input.Plan.Module.ArtifactDigest,
			input.LocalMCPArtifactGrant,
			input.TrustedInProcessArtifactGrant,
			input.RemoteActionArtifactGrant,
			input.WASMActionArtifactGrant,
		); grantErr != nil {
			return newModuleApplyFailureV1(
				moduleApplyArtifactGrantFailureCodeV1(grantErr),
				grantErr,
			)
		}
		if grantErr := validateModuleApplyRemoteActionGrantsV1(
			input.Plan,
			policy,
			input.RemoteActionEndpointGrant,
			input.RemoteActionSecretRefGrant,
		); grantErr != nil {
			return newModuleApplyFailureV1(
				moduleApplyRemoteGrantFailureCodeV1(grantErr),
				grantErr,
			)
		}
		if grantErr := validateModuleApplyModelSecretGrantV1(
			input.Plan,
			input.ModelSecretRefGrant,
		); grantErr != nil {
			return newModuleApplyFailureV1(
				moduleApplyFailureGrantRequired,
				grantErr,
			)
		}
	case moduleApplyDisabledV1:
		if strings.TrimSpace(input.ArtifactDirectory) != "" ||
			input.LocalMCPArtifactGrant != "" ||
			input.TrustedInProcessArtifactGrant != "" ||
			input.RemoteActionArtifactGrant != "" ||
			input.WASMActionArtifactGrant != "" ||
			input.RemoteActionEndpointGrant != "" ||
			input.RemoteActionSecretRefGrant != "" ||
			input.ModelSecretRefGrant != "" {
			return newModuleApplyFailureV1(
				moduleApplyFailureInvalidFlags,
				errors.New("DISABLED dry-run forbids artifact input"),
			)
		}
	default:
		return newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.New("desired state is invalid"),
		)
	}
	return nil
}

func prepareEnabledModuleDryRunV1(
	ctx context.Context,
	observer *currentstore.ReadOnlyObserver,
	artifactRoot string,
	input moduleApplyCommandInputV1,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (preparedResult moduleApplyPreparedEnabledV1, returnErr error) {
	return prepareEnabledModuleDryRunWithFilesystemV1(
		ctx,
		observer,
		artifactRoot,
		input,
		basis,
		control,
		catalog,
		productionModuleDryRunTempFilesystemV1(),
	)
}

func prepareEnabledModuleDryRunWithFilesystemV1(
	ctx context.Context,
	observer *currentstore.ReadOnlyObserver,
	artifactRoot string,
	input moduleApplyCommandInputV1,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	filesystem moduleDryRunTempFilesystemV1,
) (preparedResult moduleApplyPreparedEnabledV1, returnErr error) {
	if filesystem.mkdirTemp == nil || filesystem.removeAll == nil ||
		filesystem.lstat == nil {
		return preparedResult, newModuleApplyFailureV1(
			moduleApplyFailureArtifact,
			errors.New("module dry-run TEMP filesystem is incomplete"),
		)
	}
	if err := validateEnabledModuleApplyInputV1(
		ctx,
		observer,
		input,
		control,
		catalog,
	); err != nil {
		return preparedResult, err
	}
	tempRoot, err := filesystem.mkdirTemp("", moduleDryRunTempPrefixV1+"*")
	if err != nil {
		return preparedResult, newModuleApplyFailureV1(moduleApplyFailureArtifact, err)
	}
	if err := validateModuleDryRunTempRootV1(tempRoot); err != nil {
		// An invalid seam result is not trusted as a deletion target. Production
		// os.MkdirTemp cannot take this branch.
		return preparedResult, newModuleApplyFailureV1(moduleApplyFailureArtifact, err)
	}
	defer func() {
		cleanupErr := cleanupModuleDryRunTempV1(tempRoot, filesystem)
		if cleanupErr != nil {
			returnErr = joinModuleApplyStageCleanupFailureV1(returnErr, cleanupErr)
		}
	}()
	stagedArtifact := filepath.Join(tempRoot, input.Plan.Module.ArtifactDigest)
	if err := stageVerifiedArtifact(
		ctx,
		input.ArtifactDirectory,
		stagedArtifact,
		input.Plan.Module.ArtifactDigest,
		input.Plan.Module.ArtifactSizeBytes,
	); err != nil {
		return preparedResult, newModuleApplyFailureV1(moduleApplyFailureArtifact, err)
	}
	candidate, err := buildModuleApplyCandidateV1(
		ctx,
		input.Plan,
		stagedArtifact,
		moduleApplyCandidateForDryRunV1,
	)
	if err != nil {
		return preparedResult, newModuleApplyFailureV1(moduleApplyFailureArtifact, err)
	}
	prepared, err := evaluateEnabledModuleCandidateV1(
		ctx,
		observer,
		input,
		basis,
		control,
		catalog,
		candidate,
	)
	if err != nil {
		return preparedResult, err
	}
	if err := verifyProjectedFinalModuleArtifactV1(
		ctx,
		artifactRoot,
		input.Plan,
		candidate,
	); err != nil {
		return preparedResult, newModuleApplyFailureV1(moduleApplyFailureArtifact, err)
	}
	return prepared, nil
}

func cleanupModuleDryRunTempV1(
	tempRoot string,
	filesystem moduleDryRunTempFilesystemV1,
) error {
	if filesystem.removeAll == nil || filesystem.lstat == nil {
		return errors.New("module dry-run TEMP filesystem is incomplete")
	}
	if err := validateModuleDryRunTempRootV1(tempRoot); err != nil {
		return err
	}
	removeErr := filesystem.removeAll(tempRoot)
	var residueErr error
	if _, err := filesystem.lstat(tempRoot); err == nil {
		residueErr = errors.New("module dry-run TEMP remains after cleanup")
	} else if !errors.Is(err, os.ErrNotExist) {
		residueErr = err
	}
	return errors.Join(removeErr, residueErr)
}

func validateModuleDryRunTempRootV1(tempRoot string) error {
	rootAbsolute, err := filepath.Abs(tempRoot)
	if err != nil {
		return err
	}
	systemTemp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return err
	}
	rootAbsolute = filepath.Clean(rootAbsolute)
	systemTemp = filepath.Clean(systemTemp)
	if !samePath(filepath.Dir(rootAbsolute), systemTemp) ||
		!strings.HasPrefix(filepath.Base(rootAbsolute), moduleDryRunTempPrefixV1) {
		return errors.New("module dry-run TEMP identity is invalid")
	}
	return nil
}

func verifyProjectedFinalModuleArtifactV1(
	ctx context.Context,
	artifactRoot string,
	plan moduleApplyPlanV1,
	candidate moduleApplyCandidateV1,
) error {
	finalArtifact := filepath.Join(artifactRoot, plan.Module.ArtifactDigest)
	if _, err := os.Lstat(finalArtifact); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		finalArtifact,
		moduleapi.ArtifactMetadataPaths{},
		plan.Module.ArtifactDigest,
		plan.Module.ArtifactSizeBytes,
	); err != nil {
		return fmt.Errorf("existing final artifact conflicts with candidate: %w", err)
	}
	return verifyModuleApplyCandidateArtifactV1(
		ctx,
		plan,
		candidate,
		finalArtifact,
	)
}

func newModuleDryRunObservedResultV1(
	plan moduleApplyPlanV1,
	planDigest string,
	basis controlcontract.PublishedBasis,
	status moduleApplyStatusV1,
	recoveryRequired bool,
) moduleDryRunResultV1 {
	return newModuleDryRunResultV1(
		plan,
		planDigest,
		basis,
		moduleDryRunCandidateBasisV1{
			TenantID:         basis.TenantID,
			PointerRevision:  basis.PointerRevision,
			Control:          basis.Control,
			Catalog:          basis.Catalog,
			ProjectionStatus: moduleDryRunObservedCurrentV1,
		},
		status,
		recoveryRequired,
		moduleDryRunChangesV1{
			Installation: moduleDryRunInstallationNoneV1,
			Activation:   moduleDryRunActivationNoneV1,
			Binding: moduleDryRunBindingProjectionV1{
				Change: moduleDryRunBindingNoneV1,
			},
			Catalog: moduleDryRunCatalogNoneV1,
		},
	)
}

func newModuleDryRunProjectedResultV1(
	plan moduleApplyPlanV1,
	planDigest string,
	observed controlcontract.PublishedBasis,
	candidate controlcontract.PublishedBasis,
	recoveryRequired bool,
	changes moduleDryRunChangesV1,
) moduleDryRunResultV1 {
	return newModuleDryRunResultV1(
		plan,
		planDigest,
		observed,
		moduleDryRunCandidateBasisV1{
			TenantID:         candidate.TenantID,
			PointerRevision:  candidate.PointerRevision,
			Control:          candidate.Control,
			Catalog:          candidate.Catalog,
			ProjectionStatus: moduleDryRunProjectedNotReservedV1,
		},
		moduleApplyStatusWouldApply,
		recoveryRequired,
		changes,
	)
}

func newModuleDryRunResultV1(
	plan moduleApplyPlanV1,
	planDigest string,
	observed controlcontract.PublishedBasis,
	candidate moduleDryRunCandidateBasisV1,
	status moduleApplyStatusV1,
	recoveryRequired bool,
	changes moduleDryRunChangesV1,
) moduleDryRunResultV1 {
	result := moduleDryRunResultV1{
		SchemaVersion:           moduleDryRunResultSchemaV1,
		Status:                  status,
		DesiredState:            plan.DesiredState,
		TenantID:                plan.TenantID,
		BindingTarget:           plan.BindingTarget,
		InstanceID:              plan.InstanceID,
		Port:                    plan.Port,
		PlanDigest:              planDigest,
		StartupRecoveryRequired: recoveryRequired,
		ObservedBasis:           observed,
		CandidateBasis:          candidate,
		Changes:                 changes,
	}
	if plan.Module != nil {
		result.Module = &moduleApplyResultModuleV1{
			ID:             plan.Module.ID,
			ExactVersion:   plan.Module.ExactVersion,
			ArtifactDigest: plan.Module.ArtifactDigest,
		}
	}
	return result
}
