package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidAdmission  = errors.New("currentstore: invalid Run Admission")
	ErrAdmissionConflict = errors.New(
		"currentstore: Run Admission identity conflict",
	)
	ErrAdmissionIntegrity = errors.New(
		"currentstore: Run Admission integrity violation",
	)
)

// RunAdmissionResult identifies one already committed Run. Created is true
// only for the call that publishes a new Run closure.
type RunAdmissionResult struct {
	RunID                 string
	AdmissionIntentDigest string
	ManifestDigest        string
	MemberSnapshotDigest  string
	Created               bool
}

// ResolveAdmission performs the pre-compile idempotency lookup. A matching
// key/digest returns the original Run even if current Control has moved.
func (store *Store) ResolveAdmission(
	ctx context.Context,
	tenantID string,
	admissionKey string,
	intentDigest string,
) (RunAdmissionResult, bool, error) {
	if ctx == nil {
		return RunAdmissionResult{}, false, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidAdmission,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return RunAdmissionResult{}, false, err
	}
	defer unlock()
	if err := validateAdmissionIdentity(
		tenantID,
		admissionKey,
		intentDigest,
	); err != nil {
		return RunAdmissionResult{}, false, err
	}

	result, found, err := resolveAdmissionWithQueryer(
		ctx,
		store.db,
		tenantID,
		admissionKey,
		intentDigest,
	)
	if err != nil || !found {
		return result, found, err
	}
	return result, true, nil
}

func resolveAdmissionWithQueryer(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	admissionKey string,
	intentDigest string,
) (RunAdmissionResult, bool, error) {
	var (
		runID        string
		storedIntent string
		workspaceID  string
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT run_id, admission_intent_digest, workspace_id
		FROM runs
		WHERE tenant_id=? AND admission_key=?
	`, tenantID, admissionKey).Scan(
		&runID,
		&storedIntent,
		&workspaceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RunAdmissionResult{}, false, nil
	}
	if err != nil {
		return RunAdmissionResult{}, false, fmt.Errorf(
			"currentstore: resolve Run Admission: %w",
			err,
		)
	}
	if storedIntent != intentDigest {
		return RunAdmissionResult{}, true, fmt.Errorf(
			"%w: tenant %q admission key %q has another intent",
			ErrAdmissionConflict,
			tenantID,
			admissionKey,
		)
	}
	result, err := loadAdmissionClosure(
		ctx,
		queryer,
		runID,
		tenantID,
		admissionKey,
		intentDigest,
		workspaceID,
	)
	if err != nil {
		return RunAdmissionResult{}, true, err
	}
	return result, true, nil
}

func loadAdmissionClosure(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	tenantID string,
	admissionKey string,
	intentDigest string,
	workspaceID string,
) (RunAdmissionResult, error) {
	var manifestBytes []byte
	var manifestDigest string
	if err := queryer.QueryRowContext(ctx, `
		SELECT canonical_json, digest
		FROM run_manifests
		WHERE run_id=?
	`, runID).Scan(&manifestBytes, &manifestDigest); err != nil {
		return RunAdmissionResult{}, admissionClosureReadError(
			"manifest",
			err,
		)
	}
	manifest, err := corecontract.RestoreRunManifest(manifestBytes)
	if err != nil ||
		manifest.ManifestDigest != manifestDigest ||
		manifest.RunID != runID ||
		manifest.TenantID != tenantID ||
		manifest.AdmissionKey != admissionKey ||
		manifest.AdmissionIntentDigest != intentDigest ||
		manifest.Workspace.ID != workspaceID {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Run %q manifest does not match its persisted identity",
			ErrAdmissionIntegrity,
			runID,
		)
	}
	var (
		conversationID          sql.NullString
		conversationTurnIndex   sql.NullInt64
		conversationPredecessor sql.NullString
		parentRunID             sql.NullString
		parentManifestDigest    sql.NullString
		parentSlotID            sql.NullString
	)
	if err := queryer.QueryRowContext(ctx, `
		SELECT
			conversation_id,
			conversation_turn_index,
			conversation_predecessor_run_id,
			parent_run_id,
			parent_manifest_digest,
			parent_slot_id
		FROM runs
		WHERE run_id=?
	`, runID).Scan(
		&conversationID,
		&conversationTurnIndex,
		&conversationPredecessor,
		&parentRunID,
		&parentManifestDigest,
		&parentSlotID,
	); err != nil {
		return RunAdmissionResult{}, admissionClosureReadError(
			"Run family projection",
			err,
		)
	}
	if !runConversationProjectionMatchesManifest(
		manifest.ConversationTurn,
		conversationID,
		conversationTurnIndex,
		conversationPredecessor,
	) {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Run %q Conversation projection does not match its Manifest",
			ErrAdmissionIntegrity,
			runID,
		)
	}
	if !runFamilyProjectionMatchesManifest(
		manifest,
		parentRunID,
		parentManifestDigest,
		parentSlotID,
	) {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Run %q family projection does not match its Manifest",
			ErrAdmissionIntegrity,
			runID,
		)
	}

	memberRef := manifest.Members[0]
	var (
		memberBytes       []byte
		memberDigest      string
		agentID           string
		agentVersion      string
		agentDigest       string
		profileID         string
		profileVersion    string
		profileDigest     string
		memberWorkspaceID string
		workspaceVersion  string
		workspaceDigest   string
		controlID         string
		catalogID         string
		memberCount       int
	)
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM member_execution_snapshots
		WHERE run_id=?
	`, runID).Scan(&memberCount); err != nil || memberCount != 1 {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Run %q must have exactly one member snapshot",
			ErrAdmissionIntegrity,
			runID,
		)
	}
	if err := queryer.QueryRowContext(ctx, `
		SELECT
			agent_id, agent_version, agent_digest,
			profile_id, profile_version, profile_digest,
			workspace_id, workspace_version, workspace_digest,
			control_snapshot_id, catalog_generation_id,
			canonical_json, digest
		FROM member_execution_snapshots
		WHERE run_id=? AND member_id=?
	`, runID, memberRef.MemberID).Scan(
		&agentID,
		&agentVersion,
		&agentDigest,
		&profileID,
		&profileVersion,
		&profileDigest,
		&memberWorkspaceID,
		&workspaceVersion,
		&workspaceDigest,
		&controlID,
		&catalogID,
		&memberBytes,
		&memberDigest,
	); err != nil {
		return RunAdmissionResult{}, admissionClosureReadError(
			"member snapshot",
			err,
		)
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberBytes)
	if err != nil ||
		member.MemberSnapshotDigest != memberDigest ||
		memberDigest != memberRef.Digest ||
		member.Agent.ID != agentID ||
		member.Agent.Version != agentVersion ||
		member.Agent.Digest != agentDigest ||
		member.Profile.ID != profileID ||
		member.Profile.Version != profileVersion ||
		member.Profile.Digest != profileDigest ||
		member.Workspace.ID != memberWorkspaceID ||
		member.Workspace.Version != workspaceVersion ||
		member.Workspace.Digest != workspaceDigest ||
		member.Workspace.ID != workspaceID ||
		member.Catalog.ID != catalogID {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Run %q member snapshot does not match persisted projections",
			ErrAdmissionIntegrity,
			runID,
		)
	}
	if err := manifest.ValidateAgainstMember(member); err != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Run %q manifest/member mismatch",
			ErrAdmissionIntegrity,
			runID,
		)
	}
	if manifest.ConversationTurn != nil {
		conversation, err := queryConversation(
			ctx,
			queryer,
			manifest.ConversationTurn.ConversationID,
		)
		if err != nil {
			return RunAdmissionResult{}, err
		}
		if conversation.TenantID != manifest.TenantID ||
			conversation.PrincipalID != manifest.ConversationTurn.PrincipalID ||
			conversation.WorkspaceID != manifest.Workspace.ID ||
			conversation.AgentID != manifest.PrimaryAgent.ID ||
			conversation.ProfileID != member.Profile.ID {
			return RunAdmissionResult{}, fmt.Errorf(
				"%w: Run %q Conversation fixed scope does not match its Manifest and Member",
				ErrAdmissionIntegrity,
				runID,
			)
		}
	}

	control, catalog, err := loadAdmissionControlCatalog(
		ctx,
		queryer,
		tenantID,
		controlID,
		catalogID,
	)
	if err != nil {
		return RunAdmissionResult{}, err
	}
	if err := verifyAdmissionControlMember(
		control,
		catalog,
		member,
		manifest,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	if err := verifyAdmissionContentClosure(
		ctx,
		queryer,
		manifest,
		member,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	if err := verifyAdmissionFrameAndEvent(
		ctx,
		queryer,
		runID,
		manifest,
		manifestDigest,
		memberDigest,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	return RunAdmissionResult{
		RunID:                 runID,
		AdmissionIntentDigest: intentDigest,
		ManifestDigest:        manifestDigest,
		MemberSnapshotDigest:  memberDigest,
		Created:               false,
	}, nil
}

func runFamilyProjectionMatchesManifest(
	manifest corecontract.RunManifest,
	parentRunID sql.NullString,
	parentManifestDigest sql.NullString,
	parentSlotID sql.NullString,
) bool {
	if manifest.Composite == nil ||
		manifest.Composite.Role == corecontract.CompositeRunRoleRootV1 {
		return !parentRunID.Valid &&
			!parentManifestDigest.Valid &&
			!parentSlotID.Valid
	}
	return (manifest.Composite.Role == corecontract.CompositeRunRoleChildV1 ||
		manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1) &&
		parentRunID.Valid &&
		parentRunID.String == manifest.ParentRunID &&
		parentManifestDigest.Valid &&
		parentManifestDigest.String == manifest.Composite.ParentManifestDigest &&
		parentSlotID.Valid &&
		parentSlotID.String == manifest.Composite.ParentSlotID
}

func loadAdmissionControlCatalog(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	controlID string,
	catalogID string,
) (
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
	error,
) {
	var (
		controlTenant   string
		controlRevision int64
		controlBytes    []byte
		controlDigest   string
		catalogTenant   string
		generation      int64
		catalogControl  string
		catalogBytes    []byte
		catalogDigest   string
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			control.tenant_id,
			control.revision,
			control.canonical_json,
			control.digest,
			catalog.tenant_id,
			catalog.generation,
			catalog.control_snapshot_id,
			catalog.canonical_json,
			catalog.digest
		FROM control_snapshots AS control
		JOIN runtime_catalog_generations AS catalog
		  ON catalog.generation_id=?
		WHERE control.snapshot_id=?
	`, catalogID, controlID).Scan(
		&controlTenant,
		&controlRevision,
		&controlBytes,
		&controlDigest,
		&catalogTenant,
		&generation,
		&catalogControl,
		&catalogBytes,
		&catalogDigest,
	)
	if err != nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			admissionClosureReadError("Control/Catalog", err)
	}
	if controlTenant != tenantID ||
		catalogTenant != tenantID ||
		catalogControl != controlID ||
		controlRevision <= 0 ||
		generation <= 0 {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf(
				"%w: Run references an invalid Control/Catalog edge",
				ErrAdmissionIntegrity,
			)
	}
	control, err := controlcontract.RestoreControlSnapshot(
		controlBytes,
		controlcontract.ControlSnapshotRef{
			SnapshotID: controlID,
			Revision:   uint64(controlRevision),
			Digest:     controlDigest,
		},
	)
	if err != nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf(
				"%w: restore frozen Control: %v",
				ErrAdmissionIntegrity,
				err,
			)
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		catalogBytes,
		controlcontract.CatalogGenerationRef{
			GenerationID: catalogID,
			Generation:   uint64(generation),
			Digest:       catalogDigest,
		},
	)
	if err != nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf(
				"%w: restore frozen Catalog: %v",
				ErrAdmissionIntegrity,
				err,
			)
	}
	if catalog.ControlSnapshotID != control.SnapshotID ||
		catalog.ControlSnapshotDigest != control.Digest {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf(
				"%w: frozen Catalog does not close to frozen Control",
				ErrAdmissionIntegrity,
			)
	}
	return control, catalog, nil
}

func verifyAdmissionControlMember(
	control controlcontract.ControlSnapshot,
	catalog controlcontract.CatalogGeneration,
	member corecontract.MemberExecutionSnapshot,
	manifest corecontract.RunManifest,
) error {
	agent, found := control.FindAgent(member.Agent.ID)
	if !found || agent != member.Agent {
		return fmt.Errorf(
			"%w: member Agent is absent from frozen Control",
			ErrAdmissionIntegrity,
		)
	}
	workspace, found := control.FindWorkspace(member.Workspace.ID)
	if !found ||
		workspace.Workspace != member.Workspace {
		return fmt.Errorf(
			"%w: member Workspace is absent from frozen Control",
			ErrAdmissionIntegrity,
		)
	}
	profile, found := control.FindProfile(member.Profile.ID)
	if !found ||
		profile.Profile != member.Profile ||
		profile.ContextPolicy != member.ContextPolicy ||
		profile.SchedulingPolicy != member.SchedulingPolicy ||
		!sameOptionalModelProfileRef(
			profile.ModelProfile,
			member.ModelProfile,
		) {
		return fmt.Errorf(
			"%w: member Profile is absent from frozen Control",
			ErrAdmissionIntegrity,
		)
	}
	if member.Catalog.ID != catalog.GenerationID ||
		member.Catalog.Version != strconv.FormatUint(catalog.Generation, 10) ||
		member.Catalog.Digest != catalog.Digest {
		return fmt.Errorf(
			"%w: member Catalog reference does not match frozen Catalog",
			ErrAdmissionIntegrity,
		)
	}
	expectedBindings := make(map[string][]controlcontract.BindingSpec)
	for _, binding := range profile.Bindings {
		key, _ := binding.Port.CanonicalKey()
		expectedBindings[key] = append(expectedBindings[key], binding)
	}
	for _, plan := range member.PortPlans {
		if plan.Port != (moduleapi.PortRef{
			Name:         moduleapi.PortNameChannelTransport,
			ExactVersion: moduleapi.PortVersionV1,
		}) {
			continue
		}
		matched := false
		for _, endpoint := range workspace.ChannelEndpoints {
			if !endpoint.Enabled ||
				endpoint.TargetAgentID != member.Agent.ID ||
				endpoint.TargetProfileID != member.Profile.ID {
				continue
			}
			resolved, err := resolveChannelEndpointBinding(endpoint, catalog)
			if err != nil || len(plan.Bindings) != 1 {
				continue
			}
			resolvedCanonical, _, resolvedErr :=
				moduleapi.CanonicalChannelEndpointBindingV1(resolved)
			planCanonical, _, planErr :=
				moduleapi.CanonicalChannelEndpointBindingV1(plan.Bindings[0])
			if resolvedErr == nil && planErr == nil &&
				bytes.Equal(resolvedCanonical, planCanonical) {
				key, _ := endpoint.Binding.Port.CanonicalKey()
				expectedBindings[key] = []controlcontract.BindingSpec{
					endpoint.Binding,
				}
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf(
				"%w: Channel PortPlan is absent from frozen Workspace/Catalog",
				ErrAdmissionIntegrity,
			)
		}
	}
	if len(member.PortPlans) != len(expectedBindings) {
		return fmt.Errorf(
			"%w: member PortPlans do not match frozen Profile",
			ErrAdmissionIntegrity,
		)
	}
	for _, plan := range member.PortPlans {
		key, _ := plan.Port.CanonicalKey()
		expected := expectedBindings[key]
		if len(expected) != len(plan.Bindings) {
			return fmt.Errorf(
				"%w: member Binding count does not match frozen Profile",
				ErrAdmissionIntegrity,
			)
		}
		for index, binding := range plan.Bindings {
			request := expected[index]
			entry, found := catalog.FindInstance(request.InstanceID)
			if !found ||
				binding.Provider != entry.Activation ||
				binding.ConfigRef != request.ConfigRef ||
				binding.AuthorityCeilingRef != request.AuthorityCeilingRef ||
				!sameAdmissionStringOrder(
					binding.StaticContextRefs,
					request.StaticContextRefs,
				) ||
				binding.FailurePolicy != request.FailurePolicy ||
				!admissionEntryProvides(entry, plan.Port) {
				return fmt.Errorf(
					"%w: member Binding is not proven by frozen Control/Catalog",
					ErrAdmissionIntegrity,
				)
			}
		}
	}
	return nil
}

func verifyAdmissionContentClosure(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
) error {
	task, err := requireAdmissionContentKind(
		ctx,
		queryer,
		manifest.TaskInputRef,
		ContentTaskInput,
	)
	if err != nil {
		return err
	}
	if err := verifyS1ChatContent(task); err != nil {
		return err
	}
	for _, policy := range []corecontract.PolicyRef{
		member.ContextPolicy,
		member.SchedulingPolicy,
	} {
		record, err := requireAdmissionContentKind(
			ctx,
			queryer,
			policy.Digest,
			ContentPolicy,
		)
		if err != nil {
			return err
		}
		if _, err := corecontract.RestorePolicyDocument(
			record.CanonicalBytes,
			policy,
		); err != nil {
			return fmt.Errorf(
				"%w: PolicyRef does not match POLICY content",
				ErrAdmissionIntegrity,
			)
		}
	}
	if err := validateMemberModelProfileClosure(
		member,
		func(digest string) (ContentRecord, error) {
			return queryContent(ctx, queryer, digest)
		},
	); err != nil {
		return fmt.Errorf(
			"%w: frozen ModelProfile closure: %v",
			ErrAdmissionIntegrity,
			err,
		)
	}
	if _, err := frozenKnowledgeBindingsForRun(
		manifest,
		member,
		func(digest string) (ContentRecord, error) {
			return queryContent(ctx, queryer, digest)
		},
	); err != nil {
		return fmt.Errorf(
			"%w: frozen dynamic context closure: %v",
			ErrAdmissionIntegrity,
			err,
		)
	}
	if _, err := frozenMemoryBindingsForRun(
		manifest,
		member,
		func(digest string) (ContentRecord, error) {
			return queryContent(ctx, queryer, digest)
		},
	); err != nil {
		return fmt.Errorf(
			"%w: frozen dynamic Memory closure: %v",
			ErrAdmissionIntegrity,
			err,
		)
	}
	for _, plan := range member.PortPlans {
		for _, binding := range plan.Bindings {
			if _, err := requireAdmissionContentKind(
				ctx,
				queryer,
				binding.ConfigRef,
				ContentConfig,
			); err != nil {
				return err
			}
			if _, err := requireAdmissionContentKind(
				ctx,
				queryer,
				binding.AuthorityCeilingRef,
				ContentAuthorityCeiling,
			); err != nil {
				return err
			}
			for _, digest := range binding.StaticContextRefs {
				record, err := requireAdmissionContentKind(
					ctx,
					queryer,
					digest,
					ContentStaticContext,
				)
				if err != nil {
					return err
				}
				if err := verifyS1ChatContent(record); err != nil {
					return err
				}
			}
			if err := verifyAdmissionActivation(
				ctx,
				queryer,
				manifest.TenantID,
				binding.Provider,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func sameOptionalModelProfileRef(
	left *corecontract.ModelProfileRef,
	right *corecontract.ModelProfileRef,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func verifyS1ChatContent(record ContentRecord) error {
	if record.MediaType != admissionJSONMediaType {
		return fmt.Errorf(
			"%w: %s content %s must use application/json",
			ErrAdmissionIntegrity,
			record.Kind,
			record.Digest,
		)
	}
	switch record.Kind {
	case ContentTaskInput:
		if _, err := corecontract.RestoreTaskInputV1(
			record.CanonicalBytes,
		); err != nil {
			return fmt.Errorf(
				"%w: TASK_INPUT %s is not task-input/v1",
				ErrAdmissionIntegrity,
				record.Digest,
			)
		}
	case ContentStaticContext:
		if _, err := corecontract.RestoreStaticContextV1(
			record.CanonicalBytes,
		); err != nil {
			return fmt.Errorf(
				"%w: STATIC_CONTEXT %s is not static-context/v1",
				ErrAdmissionIntegrity,
				record.Digest,
			)
		}
	default:
		return fmt.Errorf(
			"%w: %s is not S1 chat content",
			ErrAdmissionIntegrity,
			record.Kind,
		)
	}
	return nil
}

func verifyAdmissionActivation(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	tenantID string,
	provider moduleapi.ActivatedModuleRef,
) error {
	var (
		installationID string
		class          string
		adapter        string
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT installation_id, execution_class, adapter_identity
		FROM module_activations
		WHERE tenant_id=? AND instance_id=? AND activation_revision=?
	`, tenantID, provider.InstanceID, int64(provider.ActivationRevision)).Scan(
		&installationID,
		&class,
		&adapter,
	)
	if err != nil {
		return admissionClosureReadError("module activation", err)
	}
	installation, err := queryModuleInstallationByID(
		ctx,
		queryer,
		installationID,
	)
	if err != nil ||
		installation.ModuleID != provider.ModuleID ||
		installation.ExactVersion != provider.Version ||
		installation.ArtifactDigest != provider.ArtifactDigest ||
		class != string(provider.ExecutionClass) ||
		adapter != provider.AdapterIdentity {
		return fmt.Errorf(
			"%w: frozen Provider does not match Activation/Installation",
			ErrAdmissionIntegrity,
		)
	}
	return nil
}

func verifyAdmissionFrameAndEvent(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	manifest corecontract.RunManifest,
	manifestDigest string,
	memberDigest string,
) error {
	var (
		runState      string
		disposition   sql.NullString
		frameRevision int64
		step          string
		budgetRef     string
		continuation  []byte
		pending       sql.NullString
		pendingAction sql.NullString
		waitingReason sql.NullString
		lastEvent     int64
		leaseOwner    sql.NullString
		leaseEpoch    int64
		leaseExpiry   sql.NullString
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			r.state, r.disposition,
			f.frame_revision, f.step, f.usage_ledger_ref, f.continuation,
			f.pending_attempt_id, f.pending_dispatch_attempt_id,
			f.waiting_reason, f.last_authoritative_event,
			f.lease_owner, f.lease_epoch, f.lease_expiry
		FROM loop_frames AS f
		JOIN runs AS r ON r.run_id=f.run_id
		WHERE f.run_id=?
	`, runID).Scan(
		&runState,
		&disposition,
		&frameRevision,
		&step,
		&budgetRef,
		&continuation,
		&pending,
		&pendingAction,
		&waitingReason,
		&lastEvent,
		&leaseOwner,
		&leaseEpoch,
		&leaseExpiry,
	)
	if err != nil {
		return admissionClosureReadError("LoopFrame", err)
	}
	canonical, canonicalErr := moduleapi.CanonicalJSON(continuation)
	if canonicalErr != nil || !bytes.Equal(canonical, continuation) {
		return fmt.Errorf(
			"%w: LoopFrame continuation is not canonical",
			ErrAdmissionIntegrity,
		)
	}
	if frameRevision == 0 {
		expectedStep := corecontract.InitialLoopStep
		expectedDisposition := ""
		expectedWaitingReason := ""
		expectedBudget, expectedContinuation, err :=
			corecontract.NewInitialLoopState(runID)
		dormantRepair := manifest.Composite != nil &&
			manifest.Composite.RepairRound ==
				corecontract.CompositeRepairRoundOneV1
		if dormantRepair {
			expectedStep = corecontract.WaitingRepairActivationLoopStep
			expectedDisposition = "WAITING_EXTERNAL"
			expectedWaitingReason = compositeRepairDormantWaitingReason
			expectedBudget, expectedContinuation, err =
				corecontract.NewWaitingRepairActivationLoopState(runID)
		} else if manifest.Composite != nil &&
			(manifest.Composite.Role == corecontract.CompositeRunRoleRootV1 ||
				manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1) {
			expectedStep = corecontract.WaitingChildrenLoopStep
			expectedDisposition = "WAITING_EXTERNAL"
			expectedWaitingReason = compositeChildrenPendingWaitingReason
			expectedBudget, expectedContinuation, err =
				corecontract.NewWaitingChildrenLoopState(runID)
		}
		if err != nil ||
			runState != corecontract.InitialRunState ||
			disposition.String != expectedDisposition ||
			disposition.Valid != (expectedDisposition != "") ||
			step != expectedStep ||
			budgetRef != expectedBudget ||
			!bytes.Equal(continuation, expectedContinuation) ||
			pending.Valid || pendingAction.Valid ||
			waitingReason.String != expectedWaitingReason ||
			waitingReason.Valid != (expectedWaitingReason != "") ||
			lastEvent != 0 ||
			leaseOwner.Valid ||
			leaseEpoch != 0 ||
			leaseExpiry.Valid {
			return fmt.Errorf(
				"%w: initial LoopFrame does not match fixed state",
				ErrAdmissionIntegrity,
			)
		}
		if dormantRepair {
			var modelAttempts, dispatchAttempts, usageRows, historyRows int64
			if err := queryer.QueryRowContext(ctx, `
				SELECT
				 (SELECT COUNT(*) FROM model_dispatch_attempts WHERE run_id=?),
				 (SELECT COUNT(*) FROM dispatch_attempts WHERE run_id=?),
				 (SELECT COUNT(*) FROM model_usage WHERE run_id=?),
				 (SELECT COUNT(*) FROM history_entries WHERE run_id=?)
			`, runID, runID, runID, runID).Scan(
				&modelAttempts,
				&dispatchAttempts,
				&usageRows,
				&historyRows,
			); err != nil || modelAttempts != 0 || dispatchAttempts != 0 ||
				usageRows != 0 || historyRows != 0 {
				return fmt.Errorf(
					"%w: dormant repair admission contains execution or usage",
					ErrAdmissionIntegrity,
				)
			}
		}
	}

	var (
		eventKind     string
		fromRevision  int64
		toRevision    int64
		payloadRef    string
		payloadDigest string
	)
	if err := queryer.QueryRowContext(ctx, `
		SELECT
			event_kind, from_revision, to_revision,
			payload_ref, payload_digest
		FROM run_events
		WHERE run_id=? AND event_sequence=0
	`, runID).Scan(
		&eventKind,
		&fromRevision,
		&toRevision,
		&payloadRef,
		&payloadDigest,
	); err != nil {
		return admissionClosureReadError("RunEvent zero", err)
	}
	record, err := requireAdmissionContentKind(
		ctx,
		queryer,
		payloadRef,
		ContentRunEventPayload,
	)
	if err != nil {
		return err
	}
	event, err := corecontract.RestoreRunAdmittedEventV1(
		record.CanonicalBytes,
	)
	if err != nil ||
		eventKind != corecontract.RunAdmittedEventKind ||
		fromRevision != 0 ||
		toRevision != 0 ||
		payloadRef != payloadDigest ||
		event.RunID != runID ||
		event.ManifestDigest != manifestDigest ||
		event.MemberSnapshotDigest != memberDigest {
		return fmt.Errorf(
			"%w: RunEvent zero does not match admitted Run",
			ErrAdmissionIntegrity,
		)
	}
	return nil
}

func requireAdmissionContentKind(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	digest string,
	kind ContentKind,
) (ContentRecord, error) {
	record, err := queryContent(ctx, queryer, digest)
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"%w: required %s content %s is unavailable",
			ErrAdmissionIntegrity,
			kind,
			digest,
		)
	}
	if record.Kind != kind {
		return ContentRecord{}, fmt.Errorf(
			"%w: content %s has kind %s, want %s",
			ErrAdmissionIntegrity,
			digest,
			record.Kind,
			kind,
		)
	}
	return record, nil
}

func admissionEntryProvides(
	entry controlcontract.CatalogEntry,
	port moduleapi.PortRef,
) bool {
	for _, provided := range entry.Provides {
		if provided == port {
			return true
		}
	}
	return false
}

func sameAdmissionStringOrder(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func admissionClosureReadError(component string, err error) error {
	if errors.Is(err, sql.ErrNoRows) ||
		errors.Is(err, ErrContentNotFound) ||
		errors.Is(err, ErrModuleInstallationNotFound) {
		return fmt.Errorf(
			"%w: Run closure is missing %s",
			ErrAdmissionIntegrity,
			component,
		)
	}
	return fmt.Errorf(
		"currentstore: read Run Admission %s: %w",
		component,
		err,
	)
}

func validateAdmissionIdentity(
	tenantID string,
	admissionKey string,
	intentDigest string,
) error {
	for name, value := range map[string]string{
		"tenant ID":     tenantID,
		"admission key": admissionKey,
	} {
		if value == "" ||
			len(value) > moduleapi.MaxOpaqueIDBytes ||
			!utf8.ValidString(value) ||
			value != strings.TrimSpace(value) ||
			value != moduleapi.CanonicalText(value) {
			return fmt.Errorf(
				"%w: invalid %s",
				ErrInvalidAdmission,
				name,
			)
		}
		for _, character := range value {
			if unicode.IsControl(character) {
				return fmt.Errorf(
					"%w: invalid %s",
					ErrInvalidAdmission,
					name,
				)
			}
		}
	}
	if !moduleapi.ValidSHA256(intentDigest) {
		return fmt.Errorf(
			"%w: invalid admission intent digest",
			ErrInvalidAdmission,
		)
	}
	return nil
}
