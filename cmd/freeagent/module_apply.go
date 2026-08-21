package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/internal/moduleapplyplan"
	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/internal/moduledisabledryrun"
	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	moduleApplyResultSchemaV1     = "freeagent.module-apply-result/v1"
	moduleApplyControlIDPrefixV1  = moduleapplyplan.CandidateControlSnapshotIDPrefixV1
	moduleApplyCatalogIDPrefixV1  = moduleapplyplan.CandidateCatalogGenerationIDPrefixV1
	moduleApplyInstallationPrefix = "module-installation-v1-"
	moduleApplyStagePrefixV1      = ".freeagent-module-apply-stage-"
	moduleApplyJSONMediaType      = "application/json"
	moduleApplyReconcileTimeout   = 5 * time.Second
)

type moduleApplyStageFilesystemV1 struct {
	mkdirTemp                         func(string, string) (string, error)
	removeAll                         func(string) error
	syncDirectory                     func(string) error
	wrapArtifactRootWriteLeaseContext func(context.Context) context.Context
}

func productionModuleApplyStageFilesystemV1() moduleApplyStageFilesystemV1 {
	return moduleApplyStageFilesystemV1{
		mkdirTemp:     os.MkdirTemp,
		removeAll:     os.RemoveAll,
		syncDirectory: syncInitDirectory,
	}
}

type moduleApplyStatusV1 string

const (
	moduleApplyStatusWouldApply     moduleApplyStatusV1 = "WOULD_APPLY"
	moduleApplyStatusApplied        moduleApplyStatusV1 = "APPLIED"
	moduleApplyStatusAlreadyApplied moduleApplyStatusV1 = "ALREADY_APPLIED"
	moduleApplyStatusNoChange       moduleApplyStatusV1 = "NO_CHANGE"
)

type moduleApplyFailureCodeV1 string

const (
	moduleApplyFailureInvalidFlags   moduleApplyFailureCodeV1 = "INVALID_FLAGS"
	moduleApplyFailurePlanInvalid    moduleApplyFailureCodeV1 = "PLAN_INVALID"
	moduleApplyFailureGrantRequired  moduleApplyFailureCodeV1 = "GRANT_REQUIRED"
	moduleApplyFailureArtifact       moduleApplyFailureCodeV1 = "ARTIFACT_INVALID"
	moduleApplyFailureStoreBusy      moduleApplyFailureCodeV1 = "STORE_BUSY"
	moduleApplyFailureStore          moduleApplyFailureCodeV1 = "STORE_INVALID"
	moduleApplyFailureRecovery       moduleApplyFailureCodeV1 = "RECOVERY_FAILED"
	moduleApplyFailurePointer        moduleApplyFailureCodeV1 = "POINTER_CONFLICT"
	moduleApplyFailureTarget         moduleApplyFailureCodeV1 = "TARGET_CONFLICT"
	moduleApplyFailurePublication    moduleApplyFailureCodeV1 = "PUBLICATION_FAILED"
	moduleApplyFailureOutcomeUnknown moduleApplyFailureCodeV1 = "APPLY_OUTCOME_UNKNOWN"
	moduleApplyFailureCancelled      moduleApplyFailureCodeV1 = "CANCELLED"
	moduleApplyFailureInternal       moduleApplyFailureCodeV1 = "INTERNAL_ERROR"
)

type moduleApplyFailureV1 struct {
	code  moduleApplyFailureCodeV1
	cause error
}

func (failure *moduleApplyFailureV1) Error() string {
	if failure == nil {
		return "module apply failed"
	}
	return "module apply failed: " + string(failure.code)
}

func (failure *moduleApplyFailureV1) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func newModuleApplyFailureV1(
	code moduleApplyFailureCodeV1,
	cause error,
) error {
	// A detached post-CAS read can prove either ambiguity or that another
	// plan owns the pointer even when the original caller context was
	// cancelled. Those stronger commit-boundary conclusions must never be
	// collapsed into a harmless-looking CANCELLED result.
	if code != moduleApplyFailureOutcomeUnknown &&
		code != moduleApplyFailurePointer &&
		(errors.Is(cause, context.Canceled) ||
			errors.Is(cause, context.DeadlineExceeded)) {
		code = moduleApplyFailureCancelled
	}
	return &moduleApplyFailureV1{code: code, cause: cause}
}

func moduleApplyFailureCodeOfV1(err error) moduleApplyFailureCodeV1 {
	if err == nil {
		return ""
	}
	var failure *moduleApplyFailureV1
	if errors.As(err, &failure) && failure != nil && failure.code != "" {
		return failure.code
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return moduleApplyFailureCancelled
	}
	return moduleApplyFailureInternal
}

type moduleApplyCommandInputV1 struct {
	DatabasePath                  string
	ArtifactRoot                  string
	ArtifactDirectory             string
	LocalMCPArtifactGrant         string
	TrustedInProcessArtifactGrant string
	RemoteActionArtifactGrant     string
	WASMActionArtifactGrant       string
	RemoteActionEndpointGrant     string
	RemoteActionSecretRefGrant    string
	ModelSecretRefGrant           string
	Plan                          moduleApplyPlanV1
	PlanCanonical                 []byte
	PlanDigest                    string
	// PreStageCheck is an internal, authority-narrowing hook. Manual Apply
	// leaves it nil. Approved-upgrade Apply uses it to revalidate the exact
	// current approval after dry evaluation and before creating a hidden
	// stage or writing any immutable/publication fact.
	PreStageCheck moduleApplyPreStageCheckV1
}

type moduleApplyPreStageCheckV1 func(
	context.Context,
	*currentstore.Store,
	controlcontract.PublishedBasis,
) error

var (
	errModuleApplyArtifactGrantRequiredV1 = errors.New("exact artifact grant is required")
	errModuleApplyArtifactGrantFlagsV1    = errors.New("artifact grant flags are invalid")
	errModuleApplyRemoteGrantRequiredV1   = errors.New("exact REMOTE Action endpoint and SecretRef grants are required")
	errModuleApplyRemoteGrantFlagsV1      = errors.New("REMOTE Action grant flags are invalid")
)

// validateModuleApplyArtifactGrantsV1 is the sole transient command-grant
// validator for enabled module Apply and Dry-run. Grants are exact artifact
// digests used only at this command boundary; they are never published or
// persisted in Control, Catalog, Store content, or command output.
func validateModuleApplyArtifactGrantsV1(
	policy moduleApplyLocalPolicyV1,
	artifactDigest string,
	localMCPGrant string,
	trustedInProcessGrant string,
	remoteActionGrant string,
	wasmActionGrant string,
) error {
	artifactGrantCount := 0
	for _, grant := range []string{
		localMCPGrant,
		trustedInProcessGrant,
		remoteActionGrant,
		wasmActionGrant,
	} {
		if grant != "" {
			artifactGrantCount++
		}
	}
	if artifactGrantCount > 1 {
		return fmt.Errorf(
			"%w: LOCAL_PROCESS, TRUSTED_IN_PROCESS, REMOTE, and WASM grants are mutually exclusive",
			errModuleApplyArtifactGrantFlagsV1,
		)
	}
	switch {
	case policy.RequiresLocalMCPGrant:
		if trustedInProcessGrant != "" || remoteActionGrant != "" ||
			wasmActionGrant != "" {
			return fmt.Errorf(
				"%w: MCP handler accepts only a LOCAL_PROCESS grant",
				errModuleApplyArtifactGrantFlagsV1,
			)
		}
		if localMCPGrant != artifactDigest {
			return fmt.Errorf(
				"%w: exact LOCAL_PROCESS MCP artifact digest is absent",
				errModuleApplyArtifactGrantRequiredV1,
			)
		}
	case policy.RequiresTrustedInProcessArtifactGrant:
		if localMCPGrant != "" || remoteActionGrant != "" ||
			wasmActionGrant != "" {
			return fmt.Errorf(
				"%w: trusted handler accepts only a TRUSTED_IN_PROCESS grant",
				errModuleApplyArtifactGrantFlagsV1,
			)
		}
		if trustedInProcessGrant != artifactDigest {
			return fmt.Errorf(
				"%w: exact TRUSTED_IN_PROCESS artifact digest is absent",
				errModuleApplyArtifactGrantRequiredV1,
			)
		}
	case policy.RequiresRemoteActionArtifactGrant:
		if localMCPGrant != "" || trustedInProcessGrant != "" ||
			wasmActionGrant != "" {
			return fmt.Errorf(
				"%w: REMOTE Action handler accepts only a REMOTE artifact grant",
				errModuleApplyArtifactGrantFlagsV1,
			)
		}
		if remoteActionGrant != artifactDigest {
			return fmt.Errorf(
				"%w: exact REMOTE Action artifact digest is absent",
				errModuleApplyArtifactGrantRequiredV1,
			)
		}
	case policy.RequiresWASMActionArtifactGrant:
		if localMCPGrant != "" || trustedInProcessGrant != "" ||
			remoteActionGrant != "" {
			return fmt.Errorf(
				"%w: WASM Action handler accepts only a WASM artifact grant",
				errModuleApplyArtifactGrantFlagsV1,
			)
		}
		if wasmActionGrant != artifactDigest {
			return fmt.Errorf(
				"%w: exact WASM Action artifact digest is absent",
				errModuleApplyArtifactGrantRequiredV1,
			)
		}
	default:
		if localMCPGrant != "" || trustedInProcessGrant != "" ||
			remoteActionGrant != "" || wasmActionGrant != "" {
			return fmt.Errorf(
				"%w: selected handler accepts no artifact grant",
				errModuleApplyArtifactGrantFlagsV1,
			)
		}
	}
	return nil
}

func validateModuleApplyRemoteActionGrantsV1(
	plan moduleApplyPlanV1,
	policy moduleApplyLocalPolicyV1,
	endpointGrant string,
	secretRefGrant string,
) error {
	if policy.HandlerKind != moduleApplyHandlerRemoteActionHTTPV1 {
		if endpointGrant != "" || secretRefGrant != "" {
			return fmt.Errorf(
				"%w: selected handler accepts no REMOTE endpoint or SecretRef grant",
				errModuleApplyRemoteGrantFlagsV1,
			)
		}
		return nil
	}
	if plan.Binding == nil {
		return fmt.Errorf(
			"%w: REMOTE Action binding is absent",
			errModuleApplyRemoteGrantFlagsV1,
		)
	}
	parameters, err := validateModuleApplyRemoteActionBindingV1(
		plan.TenantID,
		*plan.Binding,
	)
	if err != nil {
		return fmt.Errorf("%w: %v", errModuleApplyRemoteGrantFlagsV1, err)
	}
	if endpointGrant != parameters.EndpointURL ||
		secretRefGrant != parameters.SecretRef {
		return fmt.Errorf(
			"%w: exact endpoint_url or secret_ref is absent",
			errModuleApplyRemoteGrantRequiredV1,
		)
	}
	return nil
}

func moduleApplyRemoteGrantFailureCodeV1(err error) moduleApplyFailureCodeV1 {
	if errors.Is(err, errModuleApplyRemoteGrantRequiredV1) {
		return moduleApplyFailureGrantRequired
	}
	return moduleApplyFailureInvalidFlags
}

func moduleApplyArtifactGrantFailureCodeV1(err error) moduleApplyFailureCodeV1 {
	if errors.Is(err, errModuleApplyArtifactGrantRequiredV1) {
		return moduleApplyFailureGrantRequired
	}
	return moduleApplyFailureInvalidFlags
}

type moduleApplyResultModuleV1 struct {
	ID             string `json:"id"`
	ExactVersion   string `json:"exact_version"`
	ArtifactDigest string `json:"artifact_digest"`
}

type moduleApplyResultV1 struct {
	SchemaVersion       string                     `json:"schema_version"`
	Status              moduleApplyStatusV1        `json:"status"`
	DesiredState        moduleApplyDesiredStateV1  `json:"desired_state"`
	TenantID            string                     `json:"tenant_id"`
	BindingTarget       moduleApplyBindingTargetV1 `json:"binding_target"`
	InstanceID          string                     `json:"instance_id"`
	Port                moduleapi.PortRef          `json:"port"`
	PointerRevision     uint64                     `json:"pointer_revision"`
	ControlSnapshotID   string                     `json:"control_snapshot_id"`
	CatalogGenerationID string                     `json:"catalog_generation_id"`
	PlanDigest          string                     `json:"plan_digest"`
	Module              *moduleApplyResultModuleV1 `json:"module,omitempty"`
}

func runModuleApply(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if ctx == nil {
		return errors.New("freeagent module-apply: failed (INVALID_FLAGS)")
	}
	flags := newFlagSet("module-apply", io.Discard)
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
		return errors.New("freeagent module-apply: failed (INVALID_FLAGS)")
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*artifactRoot) == "" || strings.TrimSpace(*planPath) == "" {
		return errors.New("freeagent module-apply: failed (INVALID_FLAGS)")
	}
	remoteAuthorityReferenceGrant := *remoteAuthorityReferenceGrantFlag
	modelAuthorityReferenceGrant := *modelAuthorityReferenceGrantFlag
	plan, canonical, digest, err := readModuleApplyPlanV1(*planPath)
	if err != nil {
		return errors.New("freeagent module-apply: failed (PLAN_INVALID)")
	}
	switch plan.DesiredState {
	case moduleApplyEnabledV1:
		policy, policyErr := resolveEnabledModuleApplyPolicyV1(plan)
		if policyErr != nil {
			return errors.New("freeagent module-apply: failed (PLAN_INVALID)")
		}
		if strings.TrimSpace(*artifactDirectory) == "" || plan.Module == nil {
			return errors.New("freeagent module-apply: failed (GRANT_REQUIRED)")
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
				"freeagent module-apply: failed (%s)",
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
				"freeagent module-apply: failed (%s)",
				moduleApplyRemoteGrantFailureCodeV1(grantErr),
			)
		}
		if grantErr := validateModuleApplyModelSecretGrantV1(plan, modelAuthorityReferenceGrant); grantErr != nil {
			return errors.New("freeagent module-apply: failed (GRANT_REQUIRED)")
		}
	case moduleApplyDisabledV1:
		if strings.TrimSpace(*artifactDirectory) != "" || *grant != "" ||
			*trustedGrant != "" || *remoteArtifactGrant != "" ||
			*wasmArtifactGrant != "" ||
			*remoteEndpointGrant != "" || remoteAuthorityReferenceGrant != "" ||
			modelAuthorityReferenceGrant != "" {
			return errors.New("freeagent module-apply: failed (INVALID_FLAGS)")
		}
	default:
		return errors.New("freeagent module-apply: failed (PLAN_INVALID)")
	}
	result, err := applyModulePlanV1(ctx, moduleApplyCommandInputV1{
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
			"freeagent module-apply: failed (%s)",
			moduleApplyFailureCodeOfV1(err),
		)
	}
	if err := writeCanonicalModuleCommandJSON(stdout, result); err != nil {
		return errors.New("freeagent module-apply: failed (INTERNAL_ERROR)")
	}
	return nil
}

func applyModulePlanV1(
	ctx context.Context,
	input moduleApplyCommandInputV1,
) (result moduleApplyResultV1, returnErr error) {
	if ctx == nil {
		return result, newModuleApplyFailureV1(
			moduleApplyFailureInvalidFlags,
			errors.New("context is nil"),
		)
	}
	if err := ctx.Err(); err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureCancelled, err)
	}
	canonicalPlan, canonical, planDigest, err := restoreModuleApplyPlanV1(
		input.PlanCanonical,
	)
	if err != nil || !bytes.Equal(canonical, input.PlanCanonical) ||
		planDigest != input.PlanDigest {
		return result, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.Join(err, errors.New("plan identity is invalid")),
		)
	}
	// Canonical bytes are the sole authority at the mutating boundary. The
	// parsed copy carried by the CLI is intentionally ignored so an internal
	// caller cannot execute bytes that differ from the plan digest.
	input.Plan = canonicalPlan
	switch input.Plan.DesiredState {
	case moduleApplyEnabledV1:
		policy, policyErr := resolveEnabledModuleApplyPolicyV1(input.Plan)
		if policyErr != nil || input.Plan.Module == nil ||
			strings.TrimSpace(input.ArtifactDirectory) == "" {
			return result, newModuleApplyFailureV1(
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
			return result, newModuleApplyFailureV1(
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
			return result, newModuleApplyFailureV1(
				moduleApplyRemoteGrantFailureCodeV1(grantErr),
				grantErr,
			)
		}
		if grantErr := validateModuleApplyModelSecretGrantV1(
			input.Plan,
			input.ModelSecretRefGrant,
		); grantErr != nil {
			return result, newModuleApplyFailureV1(
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
			return result, newModuleApplyFailureV1(
				moduleApplyFailureInvalidFlags,
				errors.New("DISABLED apply forbids artifact input"),
			)
		}
	default:
		return result, newModuleApplyFailureV1(
			moduleApplyFailurePlanInvalid,
			errors.New("desired state is invalid"),
		)
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

	store, err := currentstore.OpenExistingCurrentStore(ctx, input.DatabasePath)
	if err != nil {
		code := moduleApplyFailureStore
		if errors.Is(err, currentstore.ErrOwnerActive) {
			code = moduleApplyFailureStoreBusy
		}
		return result, newModuleApplyFailureV1(code, err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			returnErr = errors.Join(
				returnErr,
				newModuleApplyFailureV1(moduleApplyFailureStore, closeErr),
			)
		}
	}()
	if samePath(store.Path(), artifactRoot) ||
		pathContains(store.Path(), artifactRoot) ||
		pathContains(artifactRoot, store.Path()) {
		return result, newModuleApplyFailureV1(
			moduleApplyFailureArtifact,
			errors.New("database and artifact root overlap"),
		)
	}
	if err := runProductionStartupRecovery(ctx, store); err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureRecovery, err)
	}
	if err := requireNoPendingAttemptsV1(ctx, store); err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureRecovery, err)
	}

	var basis controlcontract.PublishedBasis
	var control controlcontract.ControlSnapshot
	var catalog controlcontract.CatalogGeneration
	var publication currentstore.PublishControlCatalogInput
	if input.Plan.DesiredState == moduleApplyDisabledV1 {
		disabled, evaluateErr := evaluateDisabledModulePlanOnViewV1(
			ctx,
			store,
			artifactRoot,
			input.Plan,
			input.PlanDigest,
		)
		if evaluateErr != nil {
			return result, evaluateErr
		}
		basis = disabled.ObservedBasis
		status, statusErr := moduleApplyStatusFromDisableDryRunV1(disabled.Status)
		if statusErr != nil {
			return result, statusErr
		}
		if status != moduleApplyStatusWouldApply {
			return newModuleApplyResultV1(
				input.Plan,
				input.PlanDigest,
				basis,
				status,
			), nil
		}
		publication, err = moduleDisablePublicationV1(disabled.Publication)
	} else {
		basis, control, catalog, err = store.LoadPublishedBasis(
			ctx,
			input.Plan.TenantID,
		)
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
		observedEvaluation, evaluateErr := evaluateObservedModuleApplyV1(
			ctx,
			store,
			artifactRoot,
			input.Plan,
			input.PlanDigest,
			basis,
			control,
			catalog,
		)
		if evaluateErr != nil {
			return result, evaluateErr
		}
		if !observedEvaluation.NeedsCandidate {
			if err := ensureModuleApplyMemoryGenesisV1(
				ctx,
				store,
				input.Plan,
			); err != nil {
				return result, newModuleApplyFailureV1(
					moduleApplyFailureOutcomeUnknown,
					errors.Join(
						err,
						errors.New("published Memory binding has no closed genesis"),
					),
				)
			}
			return newModuleApplyResultV1(
				input.Plan,
				input.PlanDigest,
				basis,
				observedEvaluation.Status,
			), nil
		}
		publication, err = prepareAndStageEnabledModuleV1(
			ctx,
			store,
			artifactRoot,
			productionModuleApplyStageFilesystemV1(),
			input,
			basis,
			control,
			catalog,
		)
	}
	if err != nil {
		var failure *moduleApplyFailureV1
		if errors.As(err, &failure) {
			return result, err
		}
		return result, newModuleApplyFailureV1(moduleApplyFailureInternal, err)
	}
	var channelPublication *currentstore.PublishControlCatalogWithChannelCursorSeedInput
	if input.Plan.DesiredState == moduleApplyEnabledV1 &&
		moduleApplyTargetsChannelV1(input.Plan) {
		preparedChannel, prepareErr := buildModuleApplyChannelCursorPublicationV1(
			input.Plan,
			publication,
		)
		if prepareErr != nil {
			return result, newModuleApplyFailureV1(
				moduleApplyFailureTarget,
				prepareErr,
			)
		}
		channelPublication = &preparedChannel
	}
	if err := requireNoPendingAttemptsV1(ctx, store); err != nil {
		return result, newModuleApplyFailureV1(moduleApplyFailureRecovery, err)
	}

	var published controlcontract.PublishedBasis
	if channelPublication != nil {
		published, _, err = store.PublishControlCatalogWithChannelCursorSeed(
			ctx,
			*channelPublication,
		)
	} else {
		published, err = store.PublishControlCatalog(ctx, publication)
	}
	if err != nil {
		return reconcileModulePublicationV1(
			ctx,
			store,
			artifactRoot,
			input.Plan,
			input.PlanDigest,
			publication,
			err,
		)
	}
	verificationCtx, cancelVerification := context.WithTimeout(
		context.WithoutCancel(ctx),
		moduleApplyReconcileTimeout,
	)
	defer cancelVerification()
	verifiedBasis, verifiedControl, verifiedCatalog, err := store.LoadPublishedBasis(
		verificationCtx,
		input.Plan.TenantID,
	)
	publicationVerifyErr := verifyExactModuleApplyPublicationV1(
		verifiedBasis,
		verifiedControl,
		verifiedCatalog,
		publication,
	)
	if err != nil || verifiedBasis != published || publicationVerifyErr != nil {
		return result, newModuleApplyFailureV1(
			moduleApplyFailureOutcomeUnknown,
			errors.Join(
				err,
				publicationVerifyErr,
				errors.New("published basis could not be verified"),
			),
		)
	}
	exact, conflict, err := inspectCurrentModuleApplyStateV1(
		verificationCtx,
		store,
		artifactRoot,
		input.Plan,
		verifiedControl,
		verifiedCatalog,
	)
	if err != nil || !exact || conflict {
		return result, newModuleApplyFailureV1(
			moduleApplyFailureOutcomeUnknown,
			errors.Join(err, errors.New("published desired state did not close")),
		)
	}
	if input.Plan.DesiredState == moduleApplyEnabledV1 {
		if err := verifyEnabledModuleApplySemanticsV1(
			verificationCtx,
			store,
			artifactRoot,
			input.Plan,
			verifiedControl,
			verifiedCatalog,
		); err != nil {
			return result, newModuleApplyFailureV1(
				moduleApplyFailureOutcomeUnknown,
				errors.Join(err, errors.New("published enabled semantics did not close")),
			)
		}
	}
	if err := ensureModuleApplyMemoryGenesisV1(
		verificationCtx,
		store,
		input.Plan,
	); err != nil {
		return result, newModuleApplyFailureV1(
			moduleApplyFailureOutcomeUnknown,
			errors.Join(
				err,
				errors.New(
					"Control/Catalog committed before Agent Memory genesis closed; exact retry is required",
				),
			),
		)
	}
	return newModuleApplyResultV1(
		input.Plan,
		input.PlanDigest,
		verifiedBasis,
		moduleApplyStatusApplied,
	), nil
}

func verifyExactModuleApplyRetryV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	artifactRoot string,
	plan moduleApplyPlanV1,
	planDigest string,
	currentBasis controlcontract.PublishedBasis,
	currentControl controlcontract.ControlSnapshot,
	currentCatalog controlcontract.CatalogGeneration,
) error {
	if currentControl.Revision <= 1 || currentCatalog.Generation <= 1 {
		return errors.New("published retry has no predecessor revision")
	}
	previousControl, previousCatalog, err := store.LoadControlCatalogRevision(
		ctx,
		plan.TenantID,
		currentControl.Revision-1,
		currentCatalog.Generation-1,
	)
	if err != nil {
		return err
	}
	_, previousControlRef, _, err := controlcontract.NewControlSnapshot(
		previousControl,
	)
	if err != nil {
		return err
	}
	_, previousCatalogRef, _, err := controlcontract.NewCatalogGeneration(
		previousCatalog,
	)
	if err != nil {
		return err
	}
	previousBasis := controlcontract.PublishedBasis{
		TenantID:        plan.TenantID,
		PointerRevision: plan.ExpectedPointerRevision,
		Control:         previousControlRef,
		Catalog:         previousCatalogRef,
	}
	if err := previousBasis.Validate(); err != nil {
		return err
	}

	var publication currentstore.PublishControlCatalogInput
	switch plan.DesiredState {
	case moduleApplyEnabledV1:
		if plan.Module == nil || plan.Binding == nil {
			return errors.New("enabled retry payload is incomplete")
		}
		entry, found := currentCatalog.FindInstance(plan.InstanceID)
		if !found || !moduleApplyCatalogEntryMatchesV1(entry, plan) {
			return errors.New("enabled retry provider is absent")
		}
		configRef, authorityRef, err := moduleApplyContentRefsV1(*plan.Binding)
		if err != nil {
			return err
		}
		staticContextRefs, _, err := moduleApplyStaticContextFromArtifactV1(
			ctx,
			artifactRoot,
			plan,
		)
		if err != nil {
			return err
		}
		previousControl, previousCatalog, err = addEnabledModuleBindingV1(
			ctx,
			store,
			plan,
			previousControl,
			previousCatalog,
			entry.Activation,
			configRef,
			authorityRef,
			staticContextRefs,
		)
		if err != nil {
			return err
		}
		publication, err = buildModuleApplyPublicationV1(
			planDigest,
			previousBasis,
			previousControl,
			previousCatalog,
		)
		if err != nil {
			return err
		}
	case moduleApplyDisabledV1:
		return errors.New("DISABLED retries must use the shared MODULE_DISABLE evaluator")
	default:
		return errors.New("retry desired state is invalid")
	}
	return verifyExactModuleApplyPublicationV1(
		currentBasis,
		currentControl,
		currentCatalog,
		publication,
	)
}

func newModuleApplyResultV1(
	plan moduleApplyPlanV1,
	planDigest string,
	basis controlcontract.PublishedBasis,
	status moduleApplyStatusV1,
) moduleApplyResultV1 {
	result := moduleApplyResultV1{
		SchemaVersion:       moduleApplyResultSchemaV1,
		Status:              status,
		DesiredState:        plan.DesiredState,
		TenantID:            plan.TenantID,
		BindingTarget:       plan.BindingTarget,
		InstanceID:          plan.InstanceID,
		Port:                plan.Port,
		PointerRevision:     basis.PointerRevision,
		ControlSnapshotID:   basis.Control.SnapshotID,
		CatalogGenerationID: basis.Catalog.GenerationID,
		PlanDigest:          planDigest,
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

func requireNoPendingAttemptsV1(
	ctx context.Context,
	store *currentstore.Store,
) error {
	runs, err := store.ScanStartupRecovery(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.UnsettledAttemptState == "PENDING" ||
			run.UnsettledActionAttemptState == "PENDING" ||
			run.UnsettledChannelAttemptState == "PENDING" {
			return errors.New("startup recovery left a PENDING attempt")
		}
	}
	return nil
}

func verifyCurrentCatalogArtifactRootV1(
	ctx context.Context,
	artifactRoot string,
	catalog controlcontract.CatalogGeneration,
	excludedInstanceID string,
) error {
	seen := make(map[string]struct{}, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		if entry.Activation.InstanceID == excludedInstanceID {
			continue
		}
		digest := entry.Activation.ArtifactDigest
		if _, duplicate := seen[digest]; !duplicate {
			seen[digest] = struct{}{}
			if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
				ctx,
				filepath.Join(artifactRoot, digest),
				moduleapi.ArtifactMetadataPaths{},
				digest,
				0,
			); err != nil {
				return fmt.Errorf("verify current catalog artifact: %w", err)
			}
		}
		if entry.Activation.ExecutionClass == moduleapi.ExecutionLocalProcess {
			if _, err := loadMCPInvokerFromArtifact(
				ctx,
				digest,
				entry.Activation.AdapterIdentity,
				filepath.Join(artifactRoot, digest),
				0,
				&entry.Activation,
			); err != nil {
				return fmt.Errorf(
					"verify current local-process artifact: %w",
					err,
				)
			}
		}
	}
	return nil
}

type moduleApplyReplacementStateV1 string

const (
	moduleApplyReplacementCurrentV1 moduleApplyReplacementStateV1 = "CURRENT"
	moduleApplyReplacementTargetV1  moduleApplyReplacementStateV1 = "TARGET"
)

var errModuleApplyReplacementConflictV1 = errors.New(
	"module apply replacement coordinate conflicts with current publication",
)

// inspectModuleApplyReplacementStateV1 validates the complete narrow
// replacement coordinate without mutating either snapshot. CURRENT means the
// reviewed old Instance occupies the exact PROFILE/Port/ordinal and has no
// other Control reference. TARGET means the exact desired Instance occupies
// that coordinate and the old Instance has disappeared from Control/Catalog.
func inspectModuleApplyReplacementStateV1(
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (moduleApplyReplacementStateV1, error) {
	if plan.ReplaceCurrentInstanceID == "" ||
		plan.DesiredState != moduleApplyEnabledV1 ||
		plan.BindingTarget.Kind != moduleApplyBindingTargetProfileV1 ||
		plan.Port != productionContextPort || plan.Module == nil ||
		plan.Binding == nil || plan.InstanceID == plan.ReplaceCurrentInstanceID {
		return "", errModuleApplyReplacementConflictV1
	}
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil || policy.ExecutionClass != moduleapi.ExecutionDeclarative ||
		policy.AdapterIdentity != declarativeAdapterID {
		return "", errors.Join(errModuleApplyReplacementConflictV1, err)
	}
	profile, found := findModuleApplyProfileV1(
		control,
		plan.BindingTarget.ProfileID,
	)
	if !found {
		return "", errModuleApplyReplacementConflictV1
	}
	var selected *controlcontract.BindingSpec
	ordinal := uint32(0)
	for index := range profile.Bindings {
		binding := &profile.Bindings[index]
		if binding.Port != plan.Port {
			continue
		}
		if ordinal == plan.Binding.PortBindingIndex {
			selected = binding
			break
		}
		ordinal++
	}
	if selected == nil {
		return "", errModuleApplyReplacementConflictV1
	}
	configRef, authorityRef, err := moduleApplyContentRefsV1(*plan.Binding)
	if err != nil {
		return "", err
	}
	if selected.ConfigRef != configRef ||
		selected.AuthorityCeilingRef != authorityRef ||
		selected.FailurePolicy != plan.Binding.FailurePolicy {
		return "", errModuleApplyReplacementConflictV1
	}
	currentReferences := moduleApplyControlReferenceCountV1(
		control,
		plan.ReplaceCurrentInstanceID,
	)
	targetReferences := moduleApplyControlReferenceCountV1(
		control,
		plan.InstanceID,
	)
	currentEntry, currentPresent := catalog.FindInstance(
		plan.ReplaceCurrentInstanceID,
	)
	targetEntry, targetPresent := catalog.FindInstance(plan.InstanceID)
	switch selected.InstanceID {
	case plan.ReplaceCurrentInstanceID:
		if currentReferences != 1 || targetReferences != 0 ||
			!currentPresent || targetPresent ||
			currentEntry.Activation.InstanceID != plan.ReplaceCurrentInstanceID ||
			currentEntry.Activation.ModuleID != plan.Module.ID ||
			currentEntry.Activation.Version == plan.Module.ExactVersion ||
			currentEntry.Activation.ArtifactDigest == plan.Module.ArtifactDigest ||
			currentEntry.Activation.ExecutionClass != moduleapi.ExecutionDeclarative ||
			currentEntry.Activation.AdapterIdentity != declarativeAdapterID ||
			!moduleApplyCatalogProvidesMatchPolicyV1(currentEntry.Provides, policy) {
			return "", errModuleApplyReplacementConflictV1
		}
		return moduleApplyReplacementCurrentV1, nil
	case plan.InstanceID:
		if currentReferences != 0 || targetReferences != 1 ||
			currentPresent || !targetPresent ||
			!moduleApplyCatalogEntryMatchesV1(targetEntry, plan) {
			return "", errModuleApplyReplacementConflictV1
		}
		return moduleApplyReplacementTargetV1, nil
	default:
		return "", errModuleApplyReplacementConflictV1
	}
}

func moduleApplyControlReferenceCountV1(
	control controlcontract.ControlSnapshot,
	instanceID string,
) int {
	count := 0
	for _, profile := range control.Profiles {
		for _, binding := range profile.Bindings {
			if binding.InstanceID == instanceID {
				count++
			}
		}
	}
	for _, workspace := range control.Workspaces {
		for _, endpoint := range workspace.ChannelEndpoints {
			if endpoint.Binding.InstanceID == instanceID {
				count++
			}
		}
	}
	return count
}

func inspectCurrentModuleApplyStateV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	artifactRoot string,
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (exact bool, conflict bool, returnErr error) {
	if plan.ReplaceCurrentInstanceID != "" {
		if _, err := inspectModuleApplyReplacementStateV1(
			plan,
			control,
			catalog,
		); err != nil {
			if errors.Is(err, errModuleApplyReplacementConflictV1) {
				return false, true, nil
			}
			return false, false, err
		}
	}
	if moduleApplyTargetsChannelV1(plan) {
		return inspectCurrentChannelModuleApplyStateV1(
			ctx,
			store,
			artifactRoot,
			plan,
			control,
			catalog,
		)
	}
	profile, found := findModuleApplyProfileV1(control, plan.BindingTarget.ProfileID)
	if !found {
		return false, true, nil
	}
	portOrdinal := 0
	matches := make([]struct {
		ordinal int
		binding controlcontract.BindingSpec
	}, 0, 1)
	for _, binding := range profile.Bindings {
		if binding.Port != plan.Port {
			continue
		}
		if binding.InstanceID == plan.InstanceID {
			matches = append(matches, struct {
				ordinal int
				binding controlcontract.BindingSpec
			}{ordinal: portOrdinal, binding: binding})
		}
		portOrdinal++
	}
	if len(matches) > 1 {
		return false, true, nil
	}
	if plan.DesiredState == moduleApplyDisabledV1 {
		if len(matches) != 0 {
			return false, false, nil
		}
		_, catalogHasInstance := catalog.FindInstance(plan.InstanceID)
		controlStillReferences := controlReferencesInstanceV1(control, plan.InstanceID)
		if controlStillReferences != catalogHasInstance {
			return false, true, nil
		}
		return true, false, nil
	}
	if plan.Module == nil || plan.Binding == nil {
		return false, true, nil
	}
	if len(matches) == 0 {
		if _, exists := catalog.FindInstance(plan.InstanceID); exists {
			entry, _ := catalog.FindInstance(plan.InstanceID)
			if !moduleApplyCatalogEntryMatchesV1(entry, plan) {
				return false, true, nil
			}
		}
		return false, false, nil
	}
	configRef, authorityRef, err := moduleApplyContentRefsV1(*plan.Binding)
	if err != nil {
		return false, false, err
	}
	staticContextRefs, staticContextCanonical, err :=
		moduleApplyStaticContextFromArtifactV1(ctx, artifactRoot, plan)
	if err != nil {
		return false, false, err
	}
	match := matches[0]
	if match.ordinal != int(plan.Binding.PortBindingIndex) ||
		match.binding.ConfigRef != configRef ||
		match.binding.AuthorityCeilingRef != authorityRef ||
		!reflect.DeepEqual(match.binding.StaticContextRefs, staticContextRefs) ||
		match.binding.FailurePolicy != plan.Binding.FailurePolicy {
		if plan.Port == productionModelPort && match.ordinal == 0 {
			entry, found := catalog.FindInstance(plan.InstanceID)
			if !found || !moduleApplyCatalogEntryMatchesV1(entry, plan) {
				return false, true, nil
			}
			// A Model plan replaces the sole Binding in place. A different
			// Config/Authority is the desired candidate, not a target conflict.
			return false, false, nil
		}
		return false, true, nil
	}
	entry, found := catalog.FindInstance(plan.InstanceID)
	if !found || !moduleApplyCatalogEntryMatchesV1(entry, plan) {
		return false, true, nil
	}
	var modelProfileRef *corecontract.ModelProfileRef
	var modelProfileCanonical []byte
	modelProfileChanged := false
	if plan.Port == productionModelPort {
		modelProfileRef, modelProfileCanonical, err = moduleApplyPlannedModelProfileV1(
			plan,
			entry.Activation,
		)
		if err != nil {
			return false, false, err
		}
		modelProfileChanged = !reflect.DeepEqual(profile.ModelProfile, modelProfileRef)
	}
	contents := []struct {
		digest string
		kind   currentstore.ContentKind
		bytes  []byte
	}{
		{digest: configRef, kind: currentstore.ContentConfig, bytes: plan.Binding.Config},
		{digest: authorityRef, kind: currentstore.ContentAuthorityCeiling, bytes: plan.Binding.AuthorityCeiling},
	}
	// A changed ModelProfile is candidate input and may not exist in the Store
	// yet. The unchanged Config/Authority closure must still be verified before
	// accepting that candidate; an exact current ModelProfile remains part of
	// the ordinary desired-state check below.
	if !modelProfileChanged && modelProfileRef != nil {
		contents = append(contents, struct {
			digest string
			kind   currentstore.ContentKind
			bytes  []byte
		}{
			digest: modelProfileRef.Digest,
			kind:   currentstore.ContentConfig,
			bytes:  modelProfileCanonical,
		})
	}
	if len(staticContextRefs) == 1 {
		contents = append(contents, struct {
			digest string
			kind   currentstore.ContentKind
			bytes  []byte
		}{
			digest: staticContextRefs[0],
			kind:   currentstore.ContentStaticContext,
			bytes:  staticContextCanonical,
		})
	}
	for _, ref := range contents {
		record, err := store.GetContent(ctx, ref.digest)
		if err != nil {
			return false, false, err
		}
		if record.Kind != ref.kind || record.MediaType != moduleApplyJSONMediaType ||
			!bytes.Equal(record.CanonicalBytes, ref.bytes) {
			return false, true, nil
		}
	}
	if modelProfileChanged && profile.ModelProfile != nil {
		currentProfileRecord, err := store.GetContent(
			ctx,
			profile.ModelProfile.Digest,
		)
		if err != nil {
			return false, false, err
		}
		if currentProfileRecord.Kind != currentstore.ContentConfig ||
			currentProfileRecord.MediaType != moduleApplyJSONMediaType {
			return false, true, nil
		}
		currentModelProfile, err := corecontract.RestoreModelProfileV1(
			currentProfileRecord.CanonicalBytes,
			*profile.ModelProfile,
		)
		if err != nil {
			return false, true, nil
		}
		currentModelConfig, err := moduleapi.RestoreModelBindingConfigV1(
			plan.Binding.Config,
		)
		if err != nil {
			return false, false, err
		}
		if err := corecontract.ValidateModelProfileBindingV1(
			currentModelProfile,
			moduleapi.PortBinding{
				Provider:            entry.Activation,
				ConfigRef:           configRef,
				AuthorityCeilingRef: authorityRef,
				FailurePolicy:       moduleapi.FailureRequired,
			},
			currentModelConfig,
		); err != nil {
			return false, true, nil
		}
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		filepath.Join(artifactRoot, plan.Module.ArtifactDigest),
		moduleapi.ArtifactMetadataPaths{},
		plan.Module.ArtifactDigest,
		plan.Module.ArtifactSizeBytes,
	); err != nil {
		return false, true, nil
	}
	if modelProfileChanged {
		// Model Apply is a complete desired state. Only after the unchanged
		// target closure and any replaced Profile content have proved healthy
		// may a valid optional ModelProfile delta become a candidate.
		return false, false, nil
	}
	return true, false, nil
}

func moduleApplyCatalogEntryMatchesV1(
	entry controlcontract.CatalogEntry,
	plan moduleApplyPlanV1,
) bool {
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil || plan.Module == nil {
		return false
	}
	module := *plan.Module
	provider := entry.Activation
	return provider.ModuleID == module.ID &&
		provider.Version == module.ExactVersion &&
		provider.ArtifactDigest == module.ArtifactDigest &&
		provider.ExecutionClass == policy.ExecutionClass &&
		provider.AdapterIdentity == policy.AdapterIdentity &&
		moduleApplyCatalogProvidesMatchPolicyV1(entry.Provides, policy)
}

func findModuleApplyProfileV1(
	control controlcontract.ControlSnapshot,
	profileID string,
) (controlcontract.ProfileDefinition, bool) {
	for _, profile := range control.Profiles {
		if profile.Profile.ID == profileID {
			return profile, true
		}
	}
	return controlcontract.ProfileDefinition{}, false
}

func moduleApplyContentRefsV1(
	binding moduleApplyBindingV1,
) (string, string, error) {
	configRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentConfig,
		moduleApplyJSONMediaType,
		binding.Config,
	)
	if err != nil {
		return "", "", err
	}
	authorityRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentAuthorityCeiling,
		moduleApplyJSONMediaType,
		binding.AuthorityCeiling,
	)
	if err != nil {
		return "", "", err
	}
	return configRef, authorityRef, nil
}

func createModuleApplyStageRootV1(
	artifactRoot string,
	filesystem moduleApplyStageFilesystemV1,
) (string, error) {
	if err := validateModuleApplyStageFilesystemV1(filesystem); err != nil {
		return "", err
	}
	stageRoot, err := filesystem.mkdirTemp(
		artifactRoot,
		moduleApplyStagePrefixV1+"*",
	)
	if err != nil {
		return "", fmt.Errorf("create module apply stage: %w", err)
	}
	if err := validateModuleApplyStageRootV1(artifactRoot, stageRoot); err != nil {
		// An invalid path returned by the filesystem seam is not trusted as a
		// removal target. Production os.MkdirTemp cannot take this branch.
		return "", err
	}
	return stageRoot, nil
}

func cleanupModuleApplyStageRootV1(
	stageRoot string,
	artifactRoot string,
	filesystem moduleApplyStageFilesystemV1,
) error {
	if err := validateModuleApplyStageFilesystemV1(filesystem); err != nil {
		return err
	}
	if err := validateModuleApplyStageRootV1(artifactRoot, stageRoot); err != nil {
		return err
	}
	removeErr := filesystem.removeAll(stageRoot)
	var residueErr error
	if _, err := os.Lstat(stageRoot); err == nil {
		residueErr = errors.New("module apply stage remains after cleanup")
	} else if !errors.Is(err, os.ErrNotExist) {
		residueErr = fmt.Errorf("inspect module apply stage after cleanup: %w", err)
	}
	// Root sync is attempted even when removal fails. This is both the normal
	// durability barrier and the best-effort failure/cancellation cleanup path.
	syncErr := filesystem.syncDirectory(artifactRoot)
	if removeErr == nil && residueErr == nil && syncErr == nil {
		return nil
	}
	return fmt.Errorf(
		"remove and sync module apply stage: %w",
		errors.Join(removeErr, residueErr, syncErr),
	)
}

func validateModuleApplyStageFilesystemV1(
	filesystem moduleApplyStageFilesystemV1,
) error {
	if filesystem.mkdirTemp == nil || filesystem.removeAll == nil ||
		filesystem.syncDirectory == nil {
		return errors.New("module apply stage filesystem is incomplete")
	}
	return nil
}

func validateModuleApplyStageRootV1(
	artifactRoot string,
	stageRoot string,
) error {
	rootAbsolute, err := filepath.Abs(artifactRoot)
	if err != nil {
		return fmt.Errorf("resolve module apply artifact root: %w", err)
	}
	stageAbsolute, err := filepath.Abs(stageRoot)
	if err != nil {
		return fmt.Errorf("resolve module apply stage root: %w", err)
	}
	rootAbsolute = filepath.Clean(rootAbsolute)
	stageAbsolute = filepath.Clean(stageAbsolute)
	stageName := filepath.Base(stageAbsolute)
	if !samePath(filepath.Dir(stageAbsolute), rootAbsolute) ||
		!strings.HasPrefix(stageName, moduleApplyStagePrefixV1) ||
		moduleapi.ValidSHA256(stageName) {
		return errors.New(
			"module apply stage must be one hidden non-artifact child of artifact root",
		)
	}
	return nil
}

func validateModuleApplyStagedArtifactPathV1(
	stagedArtifact string,
	artifactRoot string,
	digest string,
) error {
	if !moduleapi.ValidSHA256(digest) || filepath.Base(stagedArtifact) != digest {
		return errors.New("module apply staged artifact identity is invalid")
	}
	return validateModuleApplyStageRootV1(
		artifactRoot,
		filepath.Dir(stagedArtifact),
	)
}

func joinModuleApplyStageCleanupFailureV1(
	primary error,
	cleanup error,
) error {
	if cleanup == nil {
		return primary
	}
	if primary == nil {
		return newModuleApplyFailureV1(moduleApplyFailureArtifact, cleanup)
	}
	var failure *moduleApplyFailureV1
	if errors.As(primary, &failure) && failure != nil {
		return &moduleApplyFailureV1{
			code:  failure.code,
			cause: errors.Join(failure.cause, cleanup),
		}
	}
	return newModuleApplyFailureV1(
		moduleApplyFailureArtifact,
		errors.Join(primary, cleanup),
	)
}

func prepareAndStageEnabledModuleV1(
	ctx context.Context,
	store *currentstore.Store,
	artifactRoot string,
	stageFilesystem moduleApplyStageFilesystemV1,
	input moduleApplyCommandInputV1,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (
	publicationResult currentstore.PublishControlCatalogInput,
	returnErr error,
) {
	if err := validateEnabledModuleApplyInputV1(
		ctx,
		store,
		input,
		control,
		catalog,
	); err != nil {
		return currentstore.PublishControlCatalogInput{}, err
	}
	if input.PreStageCheck != nil {
		if err := input.PreStageCheck(ctx, store, basis); err != nil {
			var failure *moduleApplyFailureV1
			if errors.As(err, &failure) && failure != nil {
				return currentstore.PublishControlCatalogInput{}, err
			}
			return currentstore.PublishControlCatalogInput{}, newModuleApplyFailureV1(
				moduleApplyFailureStore,
				err,
			)
		}
	}

	prepared, err := prepareAndPublishEnabledModuleArtifactV1(
		ctx,
		store,
		artifactRoot,
		stageFilesystem,
		input,
		basis,
		control,
		catalog,
	)
	if err != nil {
		return currentstore.PublishControlCatalogInput{}, err
	}
	publication := prepared.Publication
	if prepared.InstallMissing {
		installed, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
			InstallationID:      prepared.Installation.InstallationID,
			ModuleID:            prepared.Installation.ModuleID,
			ExactVersion:        prepared.Installation.ExactVersion,
			ExpectedManifestRef: prepared.Installation.ManifestRef,
			ManifestBytes:       prepared.Installation.ManifestBytes,
			ArtifactDigest:      prepared.Installation.ArtifactDigest,
		})
		if err != nil || installed.InstallationID != prepared.Installation.InstallationID {
			return currentstore.PublishControlCatalogInput{}, newModuleApplyFailureV1(
				moduleApplyFailurePublication,
				errors.Join(err, errors.New("module installation did not close")),
			)
		}
	}
	if prepared.ActivateMissing {
		activated, err := store.ActivateModule(ctx, prepared.ActivationInput)
		if err != nil || activated.ActivationID != prepared.ActivationInput.ActivationID {
			return currentstore.PublishControlCatalogInput{}, newModuleApplyFailureV1(
				moduleApplyFailurePublication,
				errors.Join(err, errors.New("module activation did not close")),
			)
		}
	}
	for _, content := range prepared.Contents {
		if _, err := store.PutContent(ctx, content); err != nil {
			return currentstore.PublishControlCatalogInput{}, newModuleApplyFailureV1(
				moduleApplyFailurePublication,
				err,
			)
		}
	}
	return publication, nil
}

// prepareAndPublishEnabledModuleArtifactV1 owns the complete physical writer
// interval for manual Apply and approved-upgrade Apply. The opaque reservation
// is prepared before waiting for the root lease; once held, the current root
// is quota-checked, the existing candidate flow stages/evaluates/publishes and
// removes its hidden stage, and the complete physical closure is re-derived
// before any immutable Current Store write occurs.
func prepareAndPublishEnabledModuleArtifactV1(
	ctx context.Context,
	store *currentstore.Store,
	artifactRoot string,
	stageFilesystem moduleApplyStageFilesystemV1,
	input moduleApplyCommandInputV1,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (moduleApplyPreparedEnabledV1, error) {
	plan := input.Plan
	reservation, err := moduleartifactstore.PreparePhysicalArtifactReservationV1(
		ctx,
		input.ArtifactDirectory,
		plan.Module.ArtifactDigest,
		plan.Module.ArtifactSizeBytes,
	)
	if err != nil {
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailureArtifact,
			err,
		)
	}
	selectedRoot, err := moduleartifactstore.SelectArtifactRootV1(artifactRoot)
	if err != nil {
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailureArtifact,
			err,
		)
	}

	leaseContext := ctx
	if stageFilesystem.wrapArtifactRootWriteLeaseContext != nil {
		leaseContext = stageFilesystem.wrapArtifactRootWriteLeaseContext(ctx)
		if leaseContext == nil {
			return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
				moduleApplyFailureInternal,
				errors.New("module Apply write-lease context wrapper returned nil"),
			)
		}
	}

	var prepared moduleApplyPreparedEnabledV1
	err = moduleartifactstore.WithArtifactRootWriteLeaseV1(
		leaseContext,
		selectedRoot,
		func(lease *moduleartifactstore.ArtifactRootWriteLeaseV1) (
			callbackErr error,
		) {
			if err := lease.VerifyPhysicalReservationV1(
				ctx,
				reservation,
				false,
			); err != nil {
				return err
			}

			stageRoot, err := createModuleApplyStageRootV1(
				artifactRoot,
				stageFilesystem,
			)
			if err != nil {
				return err
			}
			defer func() {
				if stageRoot == "" {
					return
				}
				cleanupErr := cleanupModuleApplyStageRootV1(
					stageRoot,
					artifactRoot,
					stageFilesystem,
				)
				if cleanupErr != nil {
					callbackErr = joinModuleApplyStageCleanupFailureV1(
						callbackErr,
						cleanupErr,
					)
				}
			}()

			stagedArtifact := filepath.Join(stageRoot, plan.Module.ArtifactDigest)
			if err := stageVerifiedArtifact(
				ctx,
				input.ArtifactDirectory,
				stagedArtifact,
				plan.Module.ArtifactDigest,
				plan.Module.ArtifactSizeBytes,
			); err != nil {
				return err
			}
			candidate, err := buildModuleApplyCandidateV1(
				ctx,
				plan,
				stagedArtifact,
				moduleApplyCandidateForApplyV1,
			)
			if err != nil {
				return err
			}
			evaluated, err := evaluateEnabledModuleCandidateV1(
				ctx,
				store,
				input,
				basis,
				control,
				catalog,
				candidate,
			)
			if err != nil {
				return err
			}
			prepared = evaluated
			reused, err := publishStagedModuleArtifactV1(
				ctx,
				stagedArtifact,
				artifactRoot,
				plan.Module.ArtifactDigest,
				plan.Module.ArtifactSizeBytes,
			)
			if err != nil {
				return err
			}
			// Publication has detached the final digest tree from the stage. Remove
			// and sync the hidden stage before any full physical-root proof.
			if err := cleanupModuleApplyStageRootV1(
				stageRoot,
				artifactRoot,
				stageFilesystem,
			); err != nil {
				return err
			}
			stageRoot = ""
			// Ingress deliberately publishes every new file as inert 0600. If
			// this digest already existed, enable only the descriptor-bound
			// LOCAL_PROCESS executable through held handles before the final Core
			// handler proof. This remains filesystem state, not Store authority.
			if reused {
				if err := lease.PromoteStoreProvenInstalledModesV1(
					ctx,
					reservation,
				); err != nil {
					return err
				}
			}
			// The final content-addressed directory may have pre-existed. Re-prove
			// it through the exact Core handler selected before staging.
			if err := verifyModuleApplyCandidateArtifactV1(
				ctx,
				plan,
				prepared.Candidate,
				filepath.Join(
					artifactRoot,
					prepared.Candidate.ProviderTemplate.ArtifactDigest,
				),
			); err != nil {
				return err
			}
			return lease.VerifyPhysicalClosureV1(ctx)
		},
	)
	if err != nil {
		var failure *moduleApplyFailureV1
		if errors.As(err, &failure) && failure != nil {
			return moduleApplyPreparedEnabledV1{}, err
		}
		return moduleApplyPreparedEnabledV1{}, newModuleApplyFailureV1(
			moduleApplyFailureArtifact,
			err,
		)
	}
	return prepared, nil
}

func validateModuleApplyAuthorityV1(
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
) error {
	if plan.Binding == nil {
		return errors.New("enabled plan binding is absent")
	}
	if err := validateModuleApplyBindingPolicyV1(
		plan.Port,
		plan.TenantID,
		plan.Binding.Config,
		plan.Binding.AuthorityCeiling,
		plan.Binding.FailurePolicy,
	); err != nil {
		return err
	}
	consumerSchema, err := moduleApplyBindingConsumerSchemaV1(
		plan.Port,
		*plan.Binding,
	)
	if err != nil {
		return err
	}
	if plan.Module != nil {
		policy, err := resolveEnabledModuleApplyPolicyV1(plan)
		if err != nil {
			return err
		}
		if policy.ConsumerSchema != consumerSchema {
			return errors.New("resolved handler consumer schema differs from binding")
		}
	}
	if moduleApplyTargetsChannelV1(plan) {
		return validateModuleApplyChannelAuthorityTargetV1(plan, control)
	}
	if plan.Port == productionModelPort {
		return validateModuleApplyDeepSeekBindingV1(
			control.TenantID,
			*plan.Binding,
		)
	}
	if consumerSchema == moduleapi.KnowledgeContextBindingSchemaV1 {
		authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(
			plan.Binding.AuthorityCeiling,
		)
		if err != nil {
			return err
		}
		workspaces := make(map[string]struct{}, len(control.Workspaces))
		for _, workspace := range control.Workspaces {
			workspaces[workspace.Workspace.ID] = struct{}{}
		}
		agents := make(map[string]struct{}, len(control.Agents))
		for _, agent := range control.Agents {
			agents[agent.ID] = struct{}{}
		}
		for _, scope := range authority.AllowedScopes {
			if scope.WorkspaceID != "*" {
				if _, found := workspaces[scope.WorkspaceID]; !found {
					return fmt.Errorf(
						"Knowledge authority references unknown Workspace %q",
						scope.WorkspaceID,
					)
				}
			}
			if scope.AgentID != "*" {
				if _, found := agents[scope.AgentID]; !found {
					return fmt.Errorf(
						"Knowledge authority references unknown Agent %q",
						scope.AgentID,
					)
				}
			}
		}
		return nil
	}
	if consumerSchema == moduleapi.MemoryContextBindingSchemaV1 {
		authority, err := moduleapi.RestoreMemoryAuthorityCeilingV1(
			plan.Binding.AuthorityCeiling,
		)
		if err != nil {
			return err
		}
		if authority.TenantID != control.TenantID {
			return errors.New("Memory authority tenant is outside Control")
		}
		if _, found := control.FindAgent(authority.AgentID); !found {
			return fmt.Errorf(
				"Memory authority references unknown Agent %q",
				authority.AgentID,
			)
		}
		if len(authority.AllowedWorkspaceIDs) == 1 &&
			authority.AllowedWorkspaceIDs[0] == "*" {
			if len(control.Workspaces) == 0 {
				return errors.New("Memory wildcard authority has no Control Workspace")
			}
			return nil
		}
		for _, workspaceID := range authority.AllowedWorkspaceIDs {
			if _, found := control.FindWorkspace(workspaceID); !found {
				return fmt.Errorf(
					"Memory authority references unknown Workspace %q",
					workspaceID,
				)
			}
		}
		return nil
	}
	if plan.Port != productionActionPort {
		return nil
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(plan.Binding.Config)
	if err != nil {
		return err
	}
	authority, err := moduleapi.RestoreActionAuthorityCeilingV1(
		plan.Binding.AuthorityCeiling,
	)
	if err != nil {
		return err
	}
	workspaces := make(map[string]struct{}, len(control.Workspaces))
	for _, workspace := range control.Workspaces {
		workspaces[workspace.Workspace.ID] = struct{}{}
	}
	for _, workspaceID := range authority.AllowedWorkspaceIDs {
		if workspaceID == "*" {
			continue
		}
		if _, found := workspaces[workspaceID]; !found {
			return fmt.Errorf("authority references unknown Workspace %q", workspaceID)
		}
	}
	allowedActions := make(map[string]struct{}, len(authority.AllowedProviderActionIDs))
	for _, actionID := range authority.AllowedProviderActionIDs {
		allowedActions[actionID] = struct{}{}
	}
	for _, mapping := range config.Actions {
		if _, found := allowedActions[mapping.ProviderActionID]; !found {
			return fmt.Errorf("authority does not allow provider action %q", mapping.ProviderActionID)
		}
		allowed, err := moduleapi.ActionEffectAtMostV1(
			mapping.LocalEffectClass,
			authority.MaxEffectClass,
		)
		if err != nil || !allowed || mapping.MaxResultBytes > authority.MaxResultBytes {
			return fmt.Errorf("authority is below action mapping %q", mapping.ProviderActionID)
		}
	}
	return nil
}

func verifyEnabledModuleApplySemanticsV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	artifactRoot string,
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) error {
	if plan.Module == nil || plan.Binding == nil {
		return errors.New("enabled plan payload is incomplete")
	}
	if err := validateModuleApplyAuthorityV1(plan, control); err != nil {
		return err
	}
	if moduleApplyTargetsChannelV1(plan) {
		return verifyEnabledChannelModuleApplySemanticsV1(
			ctx,
			store,
			artifactRoot,
			plan,
			control,
			catalog,
		)
	}
	profile, found := findModuleApplyProfileV1(control, plan.BindingTarget.ProfileID)
	if !found {
		return errors.New("target Profile is absent")
	}
	if plan.Port == productionActionPort {
		if _, err := collectModuleApplyPublicActionsV1(
			ctx,
			store,
			profile.Bindings,
		); err != nil {
			return err
		}
	}
	entry, found := catalog.FindInstance(plan.InstanceID)
	if !found || !moduleApplyCatalogEntryMatchesV1(entry, plan) {
		return errors.New("enabled Catalog entry differs from plan")
	}
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil {
		return err
	}
	artifactDirectory := filepath.Join(
		artifactRoot,
		plan.Module.ArtifactDigest,
	)
	switch policy.HandlerKind {
	case moduleApplyHandlerDeepSeekModelV1:
		if err := validateDeepSeekArtifact(
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			entry.Activation,
		); err != nil {
			return err
		}
		return validateModuleApplyModelStoreClosureV1(
			ctx,
			store,
			plan,
			control,
		)
	case moduleApplyHandlerMCPActionV1:
		_, descriptor, _, err := readModuleApplyMCPMetadataV1(
			ctx,
			artifactDirectory,
		)
		if err != nil {
			return err
		}
		config, err := moduleapi.RestoreActionBindingConfigV1(plan.Binding.Config)
		if err != nil {
			return err
		}
		return validateModuleApplyDescriptorMappingsV1(config, descriptor)

	case moduleApplyHandlerRemoteActionHTTPV1:
		descriptor, err := remoteactionhttp.LoadDescriptorFromArtifact(
			ctx,
			entry.Activation,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
		)
		if err != nil {
			return err
		}
		config, err := moduleapi.RestoreActionBindingConfigV1(plan.Binding.Config)
		if err != nil {
			return err
		}
		return validateModuleApplyRemoteDescriptorMappingsV1(config, descriptor)

	case moduleApplyHandlerWASMActionV1:
		_, descriptor, err := validateModuleApplyWASMActionArtifactV1(
			ctx,
			plan,
			moduleApplyCandidateV1{
				Policy:           policy,
				ProviderTemplate: entry.Activation,
			},
			artifactDirectory,
		)
		if err != nil {
			return err
		}
		config, err := moduleapi.RestoreActionBindingConfigV1(plan.Binding.Config)
		if err != nil {
			return err
		}
		return validateModuleApplyWASMDescriptorMappingsV1(config, descriptor)

	case moduleApplyHandlerTextStatsActionV1:
		_, err := validateTextStatsArtifactFromArtifact(
			ctx,
			entry.Activation.ArtifactDigest,
			entry.Activation.AdapterIdentity,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			&entry.Activation,
		)
		return err

	case moduleApplyHandlerDocumentInsightV1:
		verified, err := validateDocumentInsightArtifactFromArtifact(
			ctx,
			entry.Activation.ArtifactDigest,
			entry.Activation.AdapterIdentity,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			&entry.Activation,
		)
		if err != nil {
			return err
		}
		switch policy.Port {
		case productionContextPort:
			if err := validateModuleApplyKnowledgeSourceV1(
				plan,
				verified.SourceRef,
			); err != nil {
				return err
			}
			if err := validateGovernedKnowledgeApplyClosureV1(
				ctx,
				store,
				plan,
				control,
				catalog,
			); err != nil {
				return err
			}
		case productionActionPort:
			expectedSource, err := resolveDocumentInsightContextClosureV1(
				ctx,
				store,
				plan,
				control,
				catalog,
			)
			if err != nil {
				return err
			}
			if expectedSource != verified.SourceRef {
				return errors.New(
					"Document Insight Action artifact source differs from the existing Context grant",
				)
			}
		default:
			return errors.New("Document Insight handler Port is unsupported")
		}
		_, err = loadDocumentInsightInvokerFromArtifact(
			ctx,
			entry.Activation.ArtifactDigest,
			entry.Activation.AdapterIdentity,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			&entry.Activation,
		)
		return err

	case moduleApplyHandlerKnowledgeContextV1:
		_, sourceRef, err := readModuleApplyKnowledgeMetadataV1(
			ctx,
			artifactDirectory,
		)
		if err != nil {
			return err
		}
		if err := validateModuleApplyKnowledgeSourceV1(plan, sourceRef); err != nil {
			return err
		}
		_, err = loadKnowledgeInvokerFromArtifact(
			ctx,
			entry.Activation.ArtifactDigest,
			entry.Activation.AdapterIdentity,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			&entry.Activation,
		)
		return err

	case moduleApplyHandlerMemoryContextV1:
		_, err := readModuleApplyMemoryMetadataV1(
			ctx,
			artifactDirectory,
		)
		if err != nil {
			return err
		}
		_, err = loadMemoryInvokerFromArtifact(
			ctx,
			entry.Activation.ArtifactDigest,
			entry.Activation.AdapterIdentity,
			artifactDirectory,
			plan.Module.ArtifactSizeBytes,
			&entry.Activation,
		)
		return err

	case moduleApplyHandlerDeclarativeContextV1:
		_, staticCanonical, err :=
			readModuleApplyDeclarativeMetadataV1(ctx, artifactDirectory)
		if err != nil {
			return err
		}
		staticRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentStaticContext,
			moduleApplyJSONMediaType,
			staticCanonical,
		)
		if err != nil {
			return err
		}
		record, err := store.GetContent(ctx, staticRef)
		if err != nil || record.Kind != currentstore.ContentStaticContext ||
			record.MediaType != moduleApplyJSONMediaType ||
			!bytes.Equal(record.CanonicalBytes, staticCanonical) {
			return errors.Join(
				err,
				errors.New("declarative static Context content is unavailable"),
			)
		}
		return nil

	default:
		return fmt.Errorf(
			"Core protocol handler kind %q has no local semantic verifier",
			policy.HandlerKind,
		)
	}
}

func validateModuleApplyDescriptorMappingsV1(
	config moduleapi.ActionBindingConfigV1,
	descriptor mcpstdio.HostDescriptorV1,
) error {
	tools := make(map[string]mcpstdio.ToolBindingV1, len(descriptor.Tools))
	for _, tool := range descriptor.Tools {
		tools[tool.ProviderActionID] = tool
	}
	for _, mapping := range config.Actions {
		tool, found := tools[mapping.ProviderActionID]
		if !found {
			return fmt.Errorf("Action config references absent descriptor tool %q", mapping.ProviderActionID)
		}
		effective, err := moduleapi.HigherActionEffectV1(
			mapping.LocalEffectClass,
			tool.RequestedEffectClass,
		)
		if err != nil || effective != mapping.LocalEffectClass ||
			mapping.MaxResultBytes > tool.RequestedMaxResultBytes {
			return fmt.Errorf("Action config exceeds descriptor mapping %q", mapping.ProviderActionID)
		}
	}
	return nil
}

func validateModuleApplyRemoteDescriptorMappingsV1(
	config moduleapi.ActionBindingConfigV1,
	descriptor remoteactionhttp.DescriptorV1,
) error {
	actions := make(
		map[string]moduleapi.ActionDefinitionV1,
		len(descriptor.Actions),
	)
	for _, action := range descriptor.Actions {
		actions[action.ProviderActionID] = action
	}
	for _, mapping := range config.Actions {
		action, found := actions[mapping.ProviderActionID]
		if !found {
			return fmt.Errorf(
				"Action config references absent REMOTE descriptor action %q",
				mapping.ProviderActionID,
			)
		}
		effective, err := moduleapi.HigherActionEffectV1(
			mapping.LocalEffectClass,
			action.RequestedEffectClass,
		)
		if err != nil || effective != mapping.LocalEffectClass ||
			mapping.MaxResultBytes > action.RequestedMaxResultBytes {
			return fmt.Errorf(
				"Action config exceeds REMOTE descriptor mapping %q",
				mapping.ProviderActionID,
			)
		}
	}
	return nil
}

func resolveModuleApplyInstallationV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	module moduleApplyModuleV1,
	manifest []byte,
	manifestRef string,
) (currentstore.ModuleInstallation, bool, error) {
	existing, err := store.GetModuleInstallationByIdentity(
		ctx,
		module.ID,
		module.ExactVersion,
	)
	if err == nil {
		if existing.ManifestRef != manifestRef ||
			existing.ArtifactDigest != module.ArtifactDigest ||
			!bytes.Equal(existing.ManifestBytes, manifest) {
			return currentstore.ModuleInstallation{}, false,
				errors.New("module identity is installed with different bytes")
		}
		return existing, false, nil
	}
	if !errors.Is(err, currentstore.ErrModuleInstallationNotFound) {
		return currentstore.ModuleInstallation{}, false, err
	}
	return currentstore.ModuleInstallation{
		InstallationID: moduleApplyInstallationPrefix + module.ArtifactDigest,
		ModuleID:       module.ID,
		ExactVersion:   module.ExactVersion,
		ManifestRef:    manifestRef,
		ManifestBytes:  bytes.Clone(manifest),
		ArtifactDigest: module.ArtifactDigest,
	}, true, nil
}

func resolveModuleApplyActivationV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	resolver *activationresolver.Resolver,
	plan moduleApplyPlanV1,
	installation currentstore.ModuleInstallation,
	catalog controlcontract.CatalogGeneration,
) (
	moduleapi.ActivatedModuleRef,
	currentstore.ActivateModuleInput,
	bool,
	error,
) {
	policy, err := resolveEnabledModuleApplyPolicyV1(plan)
	if err != nil {
		return moduleapi.ActivatedModuleRef{}, currentstore.ActivateModuleInput{}, false, err
	}
	if entry, found := catalog.FindInstance(plan.InstanceID); found {
		if plan.Module == nil || !moduleApplyCatalogEntryMatchesV1(entry, plan) {
			return moduleapi.ActivatedModuleRef{}, currentstore.ActivateModuleInput{}, false,
				errors.New("instance is already active with a different provider")
		}
		return entry.Activation, currentstore.ActivateModuleInput{}, false, nil
	}
	latest, latestErr := store.GetLatestModuleActivationForInstance(
		ctx,
		plan.TenantID,
		plan.InstanceID,
	)
	if latestErr != nil && !errors.Is(latestErr, currentstore.ErrModuleActivationNotFound) {
		return moduleapi.ActivatedModuleRef{}, currentstore.ActivateModuleInput{}, false, latestErr
	}
	revision := uint64(1)
	if latestErr == nil {
		if latest.ActivationRevision >= math.MaxInt64 {
			return moduleapi.ActivatedModuleRef{}, currentstore.ActivateModuleInput{}, false,
				errors.New("module activation revision is exhausted")
		}
		revision = latest.ActivationRevision + 1
	}
	activationInput, err := resolver.Resolve(ctx, activationresolver.ResolveInput{
		Installation:            installation,
		TenantID:                plan.TenantID,
		InstanceID:              plan.InstanceID,
		ActivationRevision:      revision,
		ExpectedExecutionClass:  policy.ExecutionClass,
		ExpectedAdapterIdentity: policy.AdapterIdentity,
	})
	if err != nil {
		return moduleapi.ActivatedModuleRef{}, currentstore.ActivateModuleInput{}, false, err
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activationInput.InstanceID,
		ExecutionClass:     activationInput.ExecutionClass,
		AdapterIdentity:    activationInput.AdapterIdentity,
		ActivationRevision: activationInput.ActivationRevision,
	}
	if err := provider.Validate(); err != nil {
		return moduleapi.ActivatedModuleRef{}, currentstore.ActivateModuleInput{}, false, err
	}
	return provider, activationInput, true, nil
}

func addEnabledModuleBindingV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	plan moduleApplyPlanV1,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	provider moduleapi.ActivatedModuleRef,
	configRef string,
	authorityRef string,
	staticContextRefs []string,
) (controlcontract.ControlSnapshot, controlcontract.CatalogGeneration, error) {
	if plan.Binding == nil {
		return control, catalog, errors.New("enabled binding is absent")
	}
	if moduleApplyTargetsChannelV1(plan) {
		return addEnabledChannelModuleBindingV1(
			plan,
			control,
			catalog,
			provider,
			configRef,
			authorityRef,
			staticContextRefs,
		)
	}
	profileIndex := -1
	for index := range control.Profiles {
		if control.Profiles[index].Profile.ID == plan.BindingTarget.ProfileID {
			profileIndex = index
			break
		}
	}
	if profileIndex < 0 {
		return control, catalog, errors.New("target Profile is absent")
	}
	bindings := append([]controlcontract.BindingSpec(nil), control.Profiles[profileIndex].Bindings...)
	if plan.Port == productionModelPort {
		if plan.Binding.PortBindingIndex != 0 ||
			plan.Binding.FailurePolicy != moduleapi.FailureRequired ||
			len(staticContextRefs) != 0 {
			return control, catalog, errors.New("Model replacement must be the sole REQUIRED binding at index zero")
		}
		modelIndex := -1
		for index, binding := range bindings {
			if binding.Port != productionModelPort {
				continue
			}
			if modelIndex >= 0 {
				return control, catalog, errors.New("target Profile has more than one Model binding")
			}
			modelIndex = index
		}
		if modelIndex < 0 {
			return control, catalog, errors.New("target Profile has no Model binding to replace")
		}
		if bindings[modelIndex].InstanceID != plan.InstanceID {
			return control, catalog, errors.New("Model replacement cannot change provider instance in the first slice")
		}
		entry, found := catalog.FindInstance(plan.InstanceID)
		if !found || entry.Activation != provider ||
			!moduleApplyCatalogEntryMatchesV1(entry, plan) {
			return control, catalog, errors.New("Model Catalog instance conflicts with replacement provider")
		}
		bindings[modelIndex] = controlcontract.BindingSpec{
			Port:                productionModelPort,
			InstanceID:          plan.InstanceID,
			ConfigRef:           configRef,
			AuthorityCeilingRef: authorityRef,
			StaticContextRefs:   []string{},
			FailurePolicy:       moduleapi.FailureRequired,
		}
		control.Profiles[profileIndex].Bindings = bindings
		modelProfileRef, _, err := moduleApplyPlannedModelProfileV1(plan, provider)
		if err != nil {
			return control, catalog, err
		}
		control.Profiles[profileIndex].ModelProfile = modelProfileRef
		if err := validateModuleApplyPortPlanV1(
			control.Profiles[profileIndex],
			catalog,
			productionModelPort,
		); err != nil {
			return control, catalog, err
		}
		return control, catalog, nil
	}
	if plan.ReplaceCurrentInstanceID != "" {
		state, stateErr := inspectModuleApplyReplacementStateV1(
			plan,
			control,
			catalog,
		)
		if stateErr != nil || state != moduleApplyReplacementCurrentV1 {
			return control, catalog, errors.Join(
				stateErr,
				errors.New("replacement current coordinate is not exact"),
			)
		}
		policy, policyErr := resolveEnabledModuleApplyPolicyV1(plan)
		if policyErr != nil || provider.InstanceID != plan.InstanceID ||
			provider.ModuleID != plan.Module.ID ||
			provider.Version != plan.Module.ExactVersion ||
			provider.ArtifactDigest != plan.Module.ArtifactDigest ||
			provider.ExecutionClass != policy.ExecutionClass ||
			provider.AdapterIdentity != policy.AdapterIdentity {
			return control, catalog, errors.Join(
				policyErr,
				errors.New("replacement target provider differs from exact plan"),
			)
		}
		replaceAt := -1
		portOrdinal := uint32(0)
		for index := range bindings {
			if bindings[index].Port != plan.Port {
				continue
			}
			if portOrdinal == plan.Binding.PortBindingIndex {
				replaceAt = index
				break
			}
			portOrdinal++
		}
		if replaceAt < 0 ||
			bindings[replaceAt].InstanceID != plan.ReplaceCurrentInstanceID {
			return control, catalog, errors.New(
				"replacement ordinal no longer contains current Instance",
			)
		}
		bindings[replaceAt] = controlcontract.BindingSpec{
			Port:                plan.Port,
			InstanceID:          plan.InstanceID,
			ConfigRef:           configRef,
			AuthorityCeilingRef: authorityRef,
			StaticContextRefs:   append([]string{}, staticContextRefs...),
			FailurePolicy:       plan.Binding.FailurePolicy,
		}
		control.Profiles[profileIndex].Bindings = bindings
		entries := make([]controlcontract.CatalogEntry, 0, len(catalog.Entries))
		removed := 0
		for _, entry := range catalog.Entries {
			if entry.Activation.InstanceID == plan.ReplaceCurrentInstanceID {
				removed++
				continue
			}
			if entry.Activation.InstanceID == plan.InstanceID {
				return control, catalog, errors.New(
					"replacement target Instance already exists in Catalog",
				)
			}
			entries = append(entries, entry)
		}
		if removed != 1 {
			return control, catalog, errors.New(
				"replacement current Catalog entry is not exact",
			)
		}
		entries = append(entries, controlcontract.CatalogEntry{
			Activation: provider,
			Provides:   moduleApplyCatalogProvidesForPolicyV1(policy),
		})
		catalog.Entries = entries
		if err := validateModuleApplyContextPlacementOrderV1(
			ctx,
			store,
			control.Profiles[profileIndex],
			plan.InstanceID,
			configRef,
			plan.Binding.Config,
		); err != nil {
			return control, catalog, err
		}
		if err := validateModuleApplyPortPlanV1(
			control.Profiles[profileIndex],
			catalog,
			plan.Port,
		); err != nil {
			return control, catalog, err
		}
		return control, catalog, nil
	}
	portPositions := make([]int, 0)
	for index, binding := range bindings {
		if binding.Port != plan.Port {
			continue
		}
		if binding.InstanceID == plan.InstanceID {
			return control, catalog, errors.New("target Profile already contains the instance")
		}
		portPositions = append(portPositions, index)
	}
	if uint64(plan.Binding.PortBindingIndex) > uint64(len(portPositions)) {
		return control, catalog, errors.New("port_binding_index is outside the exact Port Binding sequence")
	}
	if plan.Port == productionActionPort {
		if err := rejectDuplicateModuleApplyPublicActionsV1(
			ctx,
			store,
			bindings,
			plan.Binding.Config,
		); err != nil {
			return control, catalog, err
		}
	}
	insertAt := len(bindings)
	if len(portPositions) > 0 {
		if int(plan.Binding.PortBindingIndex) < len(portPositions) {
			insertAt = portPositions[plan.Binding.PortBindingIndex]
		} else {
			insertAt = portPositions[len(portPositions)-1] + 1
		}
	}
	request := controlcontract.BindingSpec{
		Port:                plan.Port,
		InstanceID:          plan.InstanceID,
		ConfigRef:           configRef,
		AuthorityCeilingRef: authorityRef,
		StaticContextRefs:   append([]string{}, staticContextRefs...),
		FailurePolicy:       plan.Binding.FailurePolicy,
	}
	bindings = append(bindings, controlcontract.BindingSpec{})
	copy(bindings[insertAt+1:], bindings[insertAt:])
	bindings[insertAt] = request
	control.Profiles[profileIndex].Bindings = bindings
	if plan.Port == productionContextPort {
		if err := validateModuleApplyContextPlacementOrderV1(
			ctx,
			store,
			control.Profiles[profileIndex],
			plan.InstanceID,
			configRef,
			plan.Binding.Config,
		); err != nil {
			return control, catalog, err
		}
	}
	policy, policyErr := resolveEnabledModuleApplyPolicyV1(plan)
	if policyErr != nil {
		return control, catalog, policyErr
	}
	catalogProvides := moduleApplyCatalogProvidesForPolicyV1(policy)
	if existing, found := catalog.FindInstance(plan.InstanceID); found {
		if existing.Activation != provider ||
			!moduleApplyCatalogProvidesMatchPolicyV1(existing.Provides, policy) {
			return control, catalog, errors.New("Catalog instance conflicts with enabled provider")
		}
	} else {
		catalog.Entries = append(catalog.Entries, controlcontract.CatalogEntry{
			Activation: provider,
			Provides:   catalogProvides,
		})
	}
	if err := validateModuleApplyPortPlanV1(
		control.Profiles[profileIndex],
		catalog,
		plan.Port,
	); err != nil {
		return control, catalog, err
	}
	return control, catalog, nil
}

func rejectDuplicateModuleApplyPublicActionsV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	bindings []controlcontract.BindingSpec,
	newConfig []byte,
) error {
	publicIDs, err := collectModuleApplyPublicActionsV1(ctx, store, bindings)
	if err != nil {
		return err
	}
	config, err := moduleapi.RestoreActionBindingConfigV1(newConfig)
	if err != nil {
		return err
	}
	for _, action := range config.Actions {
		if _, duplicate := publicIDs[action.PublicActionID]; duplicate {
			return fmt.Errorf("public Action ID %q is already bound", action.PublicActionID)
		}
	}
	return nil
}

func collectModuleApplyPublicActionsV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	bindings []controlcontract.BindingSpec,
) (map[string]struct{}, error) {
	publicIDs := make(map[string]struct{})
	for _, binding := range bindings {
		if binding.Port != productionActionPort {
			continue
		}
		record, err := store.GetContent(ctx, binding.ConfigRef)
		if err != nil || record.Kind != currentstore.ContentConfig ||
			record.MediaType != moduleApplyJSONMediaType {
			return nil, errors.Join(
				err,
				errors.New("existing Action config is unavailable"),
			)
		}
		config, err := moduleapi.RestoreActionBindingConfigV1(record.CanonicalBytes)
		if err != nil {
			return nil, err
		}
		for _, action := range config.Actions {
			if _, duplicate := publicIDs[action.PublicActionID]; duplicate {
				return nil, fmt.Errorf(
					"existing public Action ID %q is duplicated",
					action.PublicActionID,
				)
			}
			publicIDs[action.PublicActionID] = struct{}{}
		}
	}
	return publicIDs, nil
}

func validateModuleApplyContextPlacementOrderV1(
	ctx context.Context,
	store moduleApplyReadViewV1,
	profile controlcontract.ProfileDefinition,
	incomingInstanceID string,
	incomingConfigRef string,
	incomingConfigCanonical []byte,
) error {
	seenUntrustedData := false
	memoryBindingCount := 0
	for _, binding := range profile.Bindings {
		if binding.Port != productionContextPort {
			continue
		}
		var canonical []byte
		if binding.InstanceID == incomingInstanceID &&
			binding.ConfigRef == incomingConfigRef {
			canonical = incomingConfigCanonical
		} else {
			record, err := store.GetContent(ctx, binding.ConfigRef)
			if err != nil || record.Kind != currentstore.ContentConfig ||
				record.MediaType != moduleApplyJSONMediaType {
				return errors.Join(
					err,
					errors.New("existing Context Config is unavailable"),
				)
			}
			canonical = record.CanonicalBytes
		}
		config, err := moduleapi.RestoreContextBindingConfigV1(canonical)
		if err != nil {
			return fmt.Errorf("restore Context placement: %w", err)
		}
		switch config.Placement {
		case moduleapi.ContextPlacementUntrustedData:
			seenUntrustedData = true
			protocol, err := moduleapi.ContextBindingParametersSchemaVersionV1(
				config,
			)
			if err != nil {
				return fmt.Errorf("restore dynamic Context protocol: %w", err)
			}
			if protocol == moduleapi.MemoryContextBindingSchemaV1 {
				memoryBindingCount++
				if memoryBindingCount > 1 {
					return errors.New(
						"target Profile permits at most one Memory Binding",
					)
				}
			}
		case moduleapi.ContextPlacementTrustedInstruction:
			if seenUntrustedData {
				return errors.New(
					"Context placement order cannot put TRUSTED_INSTRUCTION after UNTRUSTED_DATA",
				)
			}
		default:
			return errors.New("Context placement is unsupported")
		}
	}
	return nil
}

func validateModuleApplyPortPlanV1(
	profile controlcontract.ProfileDefinition,
	catalog controlcontract.CatalogGeneration,
	port moduleapi.PortRef,
) error {
	bindings := make([]moduleapi.PortBinding, 0)
	for _, request := range profile.Bindings {
		if request.Port != port {
			continue
		}
		entry, found := catalog.FindInstance(request.InstanceID)
		if !found {
			return errors.New("Port Binding instance is absent from candidate Catalog")
		}
		bindings = append(bindings, moduleapi.PortBinding{
			Provider:            entry.Activation,
			ConfigRef:           request.ConfigRef,
			AuthorityCeilingRef: request.AuthorityCeilingRef,
			StaticContextRefs:   append([]string(nil), request.StaticContextRefs...),
			FailurePolicy:       request.FailurePolicy,
		})
	}
	_, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port:     port,
		Bindings: bindings,
	})
	return err
}

func controlReferencesInstanceV1(
	control controlcontract.ControlSnapshot,
	instanceID string,
) bool {
	for _, profile := range control.Profiles {
		for _, binding := range profile.Bindings {
			if binding.InstanceID == instanceID {
				return true
			}
		}
	}
	for _, workspace := range control.Workspaces {
		for _, endpoint := range workspace.ChannelEndpoints {
			if endpoint.Binding.InstanceID == instanceID {
				return true
			}
		}
	}
	return false
}

func buildModuleApplyPublicationV1(
	planDigest string,
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
) (currentstore.PublishControlCatalogInput, error) {
	candidateIDs, err := moduleapplyplan.DeriveCandidateIDsV1(planDigest)
	if err != nil {
		return currentstore.PublishControlCatalogInput{}, fmt.Errorf(
			"derive module Apply candidate identity: %w",
			err,
		)
	}
	if basis.PointerRevision >= math.MaxInt64 ||
		control.Revision >= math.MaxInt64 || catalog.Generation >= math.MaxInt64 {
		return currentstore.PublishControlCatalogInput{}, errors.New("Control/Catalog revision is exhausted")
	}
	control.SnapshotID = candidateIDs.ControlSnapshotID
	control.Revision++
	control.Digest = ""
	frozenControl, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		return currentstore.PublishControlCatalogInput{}, err
	}
	catalog.GenerationID = candidateIDs.CatalogGenerationID
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	catalog.Digest = ""
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		return currentstore.PublishControlCatalogInput{}, err
	}
	if frozenControl.SnapshotID != controlRef.SnapshotID {
		return currentstore.PublishControlCatalogInput{}, errors.New("Control freeze changed identity")
	}
	return currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: basis.PointerRevision,
		NewPointerRevision:      basis.PointerRevision + 1,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	}, nil
}

func normalizeStagedMCPArtifactModesV1(
	ctx context.Context,
	artifactDirectory string,
	expectedDigest string,
	expectedSize uint64,
) ([]byte, mcpstdio.HostDescriptorV1, error) {
	manifestCanonical, descriptor, executable, err :=
		readModuleApplyMCPMetadataV1(ctx, artifactDirectory)
	if err != nil {
		return nil, mcpstdio.HostDescriptorV1{}, err
	}
	files, err := moduleapi.ScanArtifactDirectoryContext(
		ctx,
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		return nil, mcpstdio.HostDescriptorV1{}, err
	}
	executableFound := false
	for _, file := range files {
		mode := os.FileMode(0o600)
		if file.Path == executable {
			mode = 0o700
			executableFound = true
		}
		path := filepath.Join(
			artifactDirectory,
			filepath.FromSlash(file.Path),
		)
		if err := os.Chmod(path, mode); err != nil {
			return nil, mcpstdio.HostDescriptorV1{}, err
		}
		if err := syncModuleApplyArtifactFileV1(ctx, path); err != nil {
			return nil, mcpstdio.HostDescriptorV1{}, err
		}
	}
	if !executableFound {
		return nil, mcpstdio.HostDescriptorV1{}, errors.New("MCP executable is absent")
	}
	manifestPath := filepath.Join(
		artifactDirectory,
		moduleapi.ArtifactManifestPath,
	)
	if err := os.Chmod(manifestPath, 0o600); err != nil {
		return nil, mcpstdio.HostDescriptorV1{}, err
	}
	if err := syncModuleApplyArtifactFileV1(ctx, manifestPath); err != nil {
		return nil, mcpstdio.HostDescriptorV1{}, err
	}
	if err := syncInitTreeDirectoriesContext(ctx, artifactDirectory); err != nil {
		return nil, mcpstdio.HostDescriptorV1{}, err
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
		expectedDigest,
		expectedSize,
	); err != nil {
		return nil, mcpstdio.HostDescriptorV1{}, err
	}
	return bytes.Clone(manifestCanonical), descriptor, nil
}

func readModuleApplyMCPMetadataV1(
	ctx context.Context,
	artifactDirectory string,
) ([]byte, mcpstdio.HostDescriptorV1, string, error) {
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return nil, mcpstdio.HostDescriptorV1{}, "", err
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil || manifest.Runtime.Mode != moduleapi.RuntimeModeRequestLocalProcess ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolMCPStdio20251125 {
		return nil, mcpstdio.HostDescriptorV1{}, "", errors.Join(
			err,
			errors.New("artifact is not exact LOCAL_PROCESS MCP stdio"),
		)
	}
	descriptorCanonical, err := moduleapi.ReadArtifactOrdinaryFileFromDirectoryContext(
		ctx,
		artifactDirectory,
		manifest.Runtime.Entrypoint,
		mcpstdio.MaxHostDescriptorBytesV1,
	)
	if err != nil {
		return nil, mcpstdio.HostDescriptorV1{}, "", err
	}
	var descriptor mcpstdio.HostDescriptorV1
	decoder := json.NewDecoder(bytes.NewReader(descriptorCanonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&descriptor); err != nil {
		return nil, mcpstdio.HostDescriptorV1{}, "", err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, mcpstdio.HostDescriptorV1{}, "",
			errors.New("MCP descriptor has trailing JSON")
	}
	executable, err := moduleapi.NormalizeArtifactPath(descriptor.Executable)
	if err != nil || executable != descriptor.Executable ||
		!strings.HasPrefix(executable, "content/") {
		return nil, mcpstdio.HostDescriptorV1{}, "",
			errors.New("MCP executable path is invalid")
	}
	return bytes.Clone(manifestCanonical), descriptor, executable, nil
}

func syncModuleApplyArtifactFileV1(
	ctx context.Context,
	path string,
) (returnErr error) {
	if ctx == nil {
		return errors.New("module apply artifact sync context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	if err := file.Sync(); err != nil {
		return err
	}
	return ctx.Err()
}

func publishStagedModuleArtifactV1(
	ctx context.Context,
	stagedArtifact string,
	artifactRoot string,
	digest string,
	size uint64,
) (bool, error) {
	if ctx == nil {
		return false, errors.New("module apply artifact publish context is nil")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateModuleApplyStagedArtifactPathV1(
		stagedArtifact,
		artifactRoot,
		digest,
	); err != nil {
		return false, err
	}
	destination := filepath.Join(artifactRoot, digest)
	if _, err := os.Lstat(destination); err == nil {
		if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
			ctx,
			destination,
			moduleapi.ArtifactMetadataPaths{},
			digest,
			size,
		); err != nil {
			return false, errors.New("published artifact target differs from plan")
		}
		// A pre-existing exact artifact is reusable only after every ordinary
		// file has crossed a bounded durability barrier. Directory fsync alone
		// cannot prove file data was flushed by the process that created it.
		if err := syncModuleApplyArtifactFilesV1(ctx, destination); err != nil {
			return false, err
		}
		if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
			ctx,
			destination,
			moduleapi.ArtifactMetadataPaths{},
			digest,
			size,
		); err != nil {
			return false, errors.New("published artifact target drifted during sync")
		}
		if err := syncInitTreeDirectoriesContext(ctx, destination); err != nil {
			return false, err
		}
		return true, syncInitDirectory(artifactRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	reused := false
	if err := publishInitNoReplace(stagedArtifact, destination); err != nil {
		if verifyErr := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
			ctx,
			destination,
			moduleapi.ArtifactMetadataPaths{},
			digest,
			size,
		); verifyErr != nil {
			return false, errors.Join(err, verifyErr)
		}
		reused = true
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		destination,
		moduleapi.ArtifactMetadataPaths{},
		digest,
		size,
	); err != nil {
		return false, err
	}
	if err := syncInitTreeDirectoriesContext(ctx, destination); err != nil {
		return false, err
	}
	return reused, syncInitDirectory(artifactRoot)
}

func syncModuleApplyArtifactFilesV1(
	ctx context.Context,
	artifactDirectory string,
) error {
	return syncModuleApplyArtifactFilesWithV1(
		ctx,
		artifactDirectory,
		syncModuleApplyArtifactFileV1,
	)
}

func syncModuleApplyArtifactFilesWithV1(
	ctx context.Context,
	artifactDirectory string,
	syncFile func(context.Context, string) error,
) error {
	if ctx == nil {
		return errors.New("module apply artifact sync context is nil")
	}
	if syncFile == nil {
		return errors.New("module apply artifact file sync is absent")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// ScanArtifactDirectoryContext applies the SDK's immutable limits for
	// paths, ordinary files, per-file bytes, and total bytes. Its result omits
	// only module.yaml when metadata paths are empty, so sync that ordinary
	// manifest explicitly and then every returned ordinary file.
	files, err := moduleapi.ScanArtifactDirectoryContext(
		ctx,
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(files)+1)
	paths = append(paths, filepath.Join(
		artifactDirectory,
		moduleapi.ArtifactManifestPath,
	))
	for _, file := range files {
		paths = append(paths, filepath.Join(
			artifactDirectory,
			filepath.FromSlash(file.Path),
		))
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := syncFile(ctx, path); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func reconcileModulePublicationV1(
	ctx context.Context,
	store *currentstore.Store,
	artifactRoot string,
	plan moduleApplyPlanV1,
	planDigest string,
	publication currentstore.PublishControlCatalogInput,
	publishErr error,
) (moduleApplyResultV1, error) {
	reconcileCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		moduleApplyReconcileTimeout,
	)
	defer cancel()
	basis, control, catalog, err := store.LoadPublishedBasis(
		reconcileCtx,
		plan.TenantID,
	)
	if err != nil {
		return moduleApplyResultV1{}, newModuleApplyFailureV1(
			moduleApplyFailureOutcomeUnknown,
			errors.Join(publishErr, err),
		)
	}
	if basis.PointerRevision == publication.NewPointerRevision &&
		verifyExactModuleApplyPublicationV1(
			basis,
			control,
			catalog,
			publication,
		) == nil {
		if plan.DesiredState == moduleApplyDisabledV1 {
			disabled, evaluateErr := evaluateDisabledModulePlanOnViewV1(
				reconcileCtx,
				store,
				artifactRoot,
				plan,
				planDigest,
			)
			if evaluateErr != nil || disabled.Status !=
				moduledisabledryrun.StatusAlreadyAppliedV1 {
				return moduleApplyResultV1{}, newModuleApplyFailureV1(
					moduleApplyFailureOutcomeUnknown,
					errors.Join(
						publishErr,
						evaluateErr,
						errors.New("committed Disable publication could not be verified"),
					),
				)
			}
		} else {
			exact, conflict, inspectErr := inspectCurrentModuleApplyStateV1(
				reconcileCtx,
				store,
				artifactRoot,
				plan,
				control,
				catalog,
			)
			if inspectErr != nil || !exact || conflict {
				return moduleApplyResultV1{}, newModuleApplyFailureV1(
					moduleApplyFailureOutcomeUnknown,
					errors.Join(
						publishErr,
						inspectErr,
						errors.New("committed publication target state could not be verified"),
					),
				)
			}
			if semanticErr := verifyEnabledModuleApplySemanticsV1(
				reconcileCtx,
				store,
				artifactRoot,
				plan,
				control,
				catalog,
			); semanticErr != nil {
				return moduleApplyResultV1{}, newModuleApplyFailureV1(
					moduleApplyFailureOutcomeUnknown,
					errors.Join(
						publishErr,
						semanticErr,
						errors.New("committed enabled publication semantics could not be verified"),
					),
				)
			}
		}
		if genesisErr := ensureModuleApplyMemoryGenesisV1(
			reconcileCtx,
			store,
			plan,
		); genesisErr != nil {
			return moduleApplyResultV1{}, newModuleApplyFailureV1(
				moduleApplyFailureOutcomeUnknown,
				errors.Join(
					publishErr,
					genesisErr,
					errors.New(
						"committed Memory publication has no closed genesis; exact retry is required",
					),
				),
			)
		}
		return newModuleApplyResultV1(
			plan,
			planDigest,
			basis,
			moduleApplyStatusApplied,
		), nil
	}
	if basis.PointerRevision == publication.ExpectedPointerRevision {
		return moduleApplyResultV1{}, newModuleApplyFailureV1(
			moduleApplyFailurePublication,
			publishErr,
		)
	}
	return moduleApplyResultV1{}, newModuleApplyFailureV1(
		moduleApplyFailurePointer,
		errors.Join(publishErr, errors.New("publication pointer moved to another plan")),
	)
}

func verifyExactModuleApplyPublicationV1(
	basis controlcontract.PublishedBasis,
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	publication currentstore.PublishControlCatalogInput,
) error {
	if basis.PointerRevision != publication.NewPointerRevision ||
		basis.Control != publication.ControlRef ||
		basis.Catalog != publication.CatalogRef {
		return errors.New("published Control/Catalog refs differ from apply")
	}
	_, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(control)
	if err != nil {
		return err
	}
	_, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		return err
	}
	if controlRef != publication.ControlRef ||
		catalogRef != publication.CatalogRef ||
		!bytes.Equal(controlCanonical, publication.ControlCanonical) ||
		!bytes.Equal(catalogCanonical, publication.CatalogCanonical) {
		return errors.New("published Control/Catalog canonical bytes differ from apply")
	}
	return nil
}
