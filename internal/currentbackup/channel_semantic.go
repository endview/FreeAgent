package currentbackup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	channelJSONMediaType           = "application/json"
	channelOperationDigestDomainV1 = "freeagent.channel-send-operation/v1"
	channelDispatchEventSchemaV1   = "channel-dispatch-event/v1"
	channelDispatchPendingEvent    = "CHANNEL_DISPATCH_PENDING"
	channelDispatchTerminalEvent   = "CHANNEL_DISPATCH_TERMINAL"
	channelUnknownWaitingReason    = "CHANNEL_DELIVERY_UNKNOWN"
)

type inspectedChannelReceipt struct {
	tenantID              string
	workspaceID           string
	endpointID            string
	cursorScopeKey        string
	cursorRevision        int64
	cursorBeforeRef       string
	cursorAfterRef        string
	endpointBindingDigest string
	disposition           string
	reason                string
	ingressKey            string
	providerEventIDDigest string
	envelopeRef           string
	principalID           string
	aclEpoch              int64
	admissionKey          string
	runID                 string
	createdAt             int64
}

type inspectedChannelContent struct {
	digest    string
	kind      currentstore.ContentKind
	mediaType string
	canonical []byte
}

type acceptedChannelClosure struct {
	receipt          inspectedChannelReceipt
	envelope         moduleapi.ChannelInboundEnvelopeV1
	member           corecontract.MemberExecutionSnapshot
	binding          moduleapi.PortBinding
	bindingCanonical []byte
}

type inspectedChannelSendIdentity struct {
	attemptID     string
	runID         string
	logicalStepID string
	sourceModelID string
}

type inspectedChannelRuntimeProjection struct {
	attemptID           string
	runID               string
	logicalStepID       string
	sourceModelID       string
	logicalOperationKey string
	proposalRef         string
	resultRef           string
	budgetStateRef      string
	state               string
	frameRevision       int64
	updatedAt           int64
}

type inspectedChannelDispatchEventV1 struct {
	SchemaVersion             string                                  `json:"schema_version"`
	RunID                     string                                  `json:"run_id"`
	AttemptID                 string                                  `json:"attempt_id"`
	LogicalStepID             string                                  `json:"logical_step_id"`
	LogicalOperationKey       string                                  `json:"logical_operation_key"`
	ProposalDigest            string                                  `json:"proposal_digest"`
	State                     currentstore.DispatchState              `json:"state"`
	ResultDigest              string                                  `json:"result_digest,omitempty"`
	TransitionOrigin          corecontract.DispatchTransitionOriginV1 `json:"transition_origin"`
	SourceModelAttemptID      string                                  `json:"source_model_attempt_id,omitempty"`
	SourceModelUsage          *corecontract.ModelUsageEventV1         `json:"source_model_usage,omitempty"`
	ResourceSemanticDigest    string                                  `json:"resource_semantic_digest"`
	SourceModelSemanticDigest string                                  `json:"source_model_semantic_digest,omitempty"`
}

// inspectChannelSemanticClosure proves the append-only ingress chain and each
// outbound CHANNEL_SEND closure from persisted bytes only. It deliberately
// has no Adapter, Secret resolver, ModuleHost, or network dependency.
func inspectChannelSemanticClosure(ctx context.Context, database semanticQueryer) error {
	accepted, err := inspectChannelIngressSemanticClosure(ctx, database)
	if err != nil {
		return err
	}
	return inspectChannelSendSemanticClosure(ctx, database, accepted)
}

func inspectChannelIngressSemanticClosure(
	ctx context.Context,
	database semanticQueryer,
) (map[string]acceptedChannelClosure, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT
			tenant_id, workspace_id, endpoint_id, cursor_scope_key,
			cursor_revision, cursor_before_ref, cursor_after_ref,
			endpoint_binding_digest, disposition, reason,
			ingress_key, provider_event_id_digest, envelope_ref,
			principal_id, acl_epoch, admission_key, run_id, created_at
		FROM channel_ingress_receipts
		ORDER BY tenant_id, endpoint_id, cursor_scope_key, cursor_revision
	`)
	if err != nil {
		return nil, fmt.Errorf("currentbackup: read Channel ingress receipts: %w", err)
	}
	receipts := make([]inspectedChannelReceipt, 0)
	for rows.Next() {
		var receipt inspectedChannelReceipt
		var before, ingress, event, envelope sql.NullString
		var principal, admission, run sql.NullString
		var acl sql.NullInt64
		if err := rows.Scan(
			&receipt.tenantID,
			&receipt.workspaceID,
			&receipt.endpointID,
			&receipt.cursorScopeKey,
			&receipt.cursorRevision,
			&before,
			&receipt.cursorAfterRef,
			&receipt.endpointBindingDigest,
			&receipt.disposition,
			&receipt.reason,
			&ingress,
			&event,
			&envelope,
			&principal,
			&acl,
			&admission,
			&run,
			&receipt.createdAt,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("currentbackup: scan Channel ingress receipt: %w", err)
		}
		receipt.cursorBeforeRef = before.String
		receipt.ingressKey = ingress.String
		receipt.providerEventIDDigest = event.String
		receipt.envelopeRef = envelope.String
		receipt.principalID = principal.String
		receipt.aclEpoch = acl.Int64
		receipt.admissionKey = admission.String
		receipt.runID = run.String
		receipts = append(receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("currentbackup: iterate Channel ingress receipts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("currentbackup: close Channel ingress receipts: %w", err)
	}

	accepted := make(map[string]acceptedChannelClosure)
	var previous *inspectedChannelReceipt
	for index := range receipts {
		receipt := receipts[index]
		newScope := previous == nil ||
			previous.tenantID != receipt.tenantID ||
			previous.endpointID != receipt.endpointID ||
			previous.cursorScopeKey != receipt.cursorScopeKey
		if newScope {
			if receipt.cursorRevision != 0 || receipt.cursorBeforeRef != "" ||
				receipt.disposition != string(currentstore.ChannelCursorSeed) {
				return nil, channelIntegrity("cursor scope does not start at revision-zero seed")
			}
		} else if receipt.cursorRevision != previous.cursorRevision+1 ||
			receipt.cursorBeforeRef != previous.cursorAfterRef ||
			receipt.workspaceID != previous.workspaceID {
			return nil, channelIntegrity("cursor revisions are not continuous or before/after is open")
		}
		if receipt.createdAt <= 0 || !moduleapi.ValidSHA256(receipt.cursorAfterRef) ||
			!moduleapi.ValidSHA256(receipt.endpointBindingDigest) {
			return nil, channelIntegrity("ingress receipt has invalid scalar projection")
		}
		after, err := inspectExactChannelContent(
			ctx,
			database,
			receipt.cursorAfterRef,
			currentstore.ContentChannelCursor,
			moduleapi.MaxChannelCursorBytesV1,
			true,
		)
		if err != nil {
			return nil, err
		}

		switch receipt.disposition {
		case string(currentstore.ChannelCursorSeed):
			if !newScope || receipt.ingressKey != "" ||
				receipt.providerEventIDDigest != "" || receipt.envelopeRef != "" ||
				receipt.principalID != "" || receipt.aclEpoch != 0 ||
				receipt.admissionKey != "" || receipt.runID != "" {
				return nil, channelIntegrity("CURSOR_SEED carries event or Run identity")
			}
		case string(currentstore.ChannelIngressRejected),
			string(currentstore.ChannelIngressAccepted):
			if newScope || !moduleapi.ValidSHA256(receipt.cursorBeforeRef) ||
				!moduleapi.ValidSHA256(receipt.ingressKey) ||
				!moduleapi.ValidSHA256(receipt.providerEventIDDigest) ||
				!moduleapi.ValidSHA256(receipt.envelopeRef) {
				return nil, channelIntegrity("event receipt has invalid identity")
			}
			before, err := inspectExactChannelContent(
				ctx,
				database,
				receipt.cursorBeforeRef,
				currentstore.ContentChannelCursor,
				moduleapi.MaxChannelCursorBytesV1,
				true,
			)
			if err != nil {
				return nil, err
			}
			envelopeContent, err := inspectExactChannelContent(
				ctx,
				database,
				receipt.envelopeRef,
				currentstore.ContentChannelIngressEnvelope,
				moduleapi.MaxChannelMessageBytesV1+moduleapi.MaxConfigBytes,
				false,
			)
			if err != nil {
				return nil, err
			}
			envelope, err := moduleapi.RestoreChannelInboundEnvelopeV1(
				envelopeContent.canonical,
			)
			if err != nil || envelope.EndpointID != receipt.endpointID ||
				!bytes.Equal(envelope.CursorBefore, before.canonical) ||
				!bytes.Equal(envelope.CursorAfter, after.canonical) {
				return nil, channelIntegrity("ingress Envelope does not close cursor or Endpoint")
			}
			wantIngress, wantEvent, err := currentstore.ComputeChannelIngressIdentity(
				receipt.tenantID,
				receipt.endpointID,
				envelope.ProviderEventID,
			)
			if err != nil || wantIngress != receipt.ingressKey ||
				wantEvent != receipt.providerEventIDDigest {
				return nil, channelIntegrity("ingress stable identity differs")
			}
			if receipt.disposition == string(currentstore.ChannelIngressRejected) {
				if receipt.principalID != "" || receipt.aclEpoch != 0 ||
					receipt.admissionKey != "" || receipt.runID != "" {
					return nil, channelIntegrity("REJECTED ingress carries a Run")
				}
				break
			}
			closure, err := inspectAcceptedChannelClosure(
				ctx,
				database,
				receipt,
				envelope,
			)
			if err != nil {
				return nil, err
			}
			if _, duplicate := accepted[receipt.runID]; duplicate {
				return nil, channelIntegrity("more than one ACCEPTED ingress names one Run")
			}
			accepted[receipt.runID] = closure
		default:
			return nil, channelIntegrity("unknown ingress disposition")
		}
		previous = &receipts[index]
	}
	return accepted, nil
}

func inspectAcceptedChannelClosure(
	ctx context.Context,
	database semanticQueryer,
	receipt inspectedChannelReceipt,
	envelope moduleapi.ChannelInboundEnvelopeV1,
) (acceptedChannelClosure, error) {
	if receipt.principalID == "" || receipt.aclEpoch <= 0 || receipt.runID == "" ||
		receipt.admissionKey != "channel/v1/"+receipt.ingressKey {
		return acceptedChannelClosure{}, channelIntegrity(
			"ACCEPTED ingress lacks Principal, ACL, Admission, or Run",
		)
	}
	var (
		runTenant, runWorkspace, runAdmission, runIntent string
		manifestCanonical                                []byte
		memberID, controlID, catalogID, memberDigest     string
		memberCanonical                                  []byte
		memberCount                                      int64
	)
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM member_execution_snapshots WHERE run_id=?
	`, receipt.runID).Scan(&memberCount); err != nil || memberCount != 1 {
		return acceptedChannelClosure{}, channelIntegrity("ACCEPTED Run does not have one member")
	}
	var storedManifestDigest string
	if err := database.QueryRowContext(ctx, `
		SELECT
			r.tenant_id, r.workspace_id, r.admission_key,
			r.admission_intent_digest,
			rm.canonical_json, rm.digest,
			m.member_id, m.control_snapshot_id, m.catalog_generation_id,
			m.canonical_json, m.digest
		FROM runs AS r
		JOIN run_manifests AS rm ON rm.run_id=r.run_id
		JOIN member_execution_snapshots AS m ON m.run_id=r.run_id
		WHERE r.run_id=?
	`, receipt.runID).Scan(
		&runTenant,
		&runWorkspace,
		&runAdmission,
		&runIntent,
		&manifestCanonical,
		&storedManifestDigest,
		&memberID,
		&controlID,
		&catalogID,
		&memberCanonical,
		&memberDigest,
	); err != nil {
		return acceptedChannelClosure{}, channelIntegrity("ACCEPTED ingress Run closure is unavailable")
	}
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil || manifest.ManifestDigest != storedManifestDigest ||
		manifest.RunID != receipt.runID || manifest.TenantID != receipt.tenantID ||
		manifest.AdmissionKey != receipt.admissionKey ||
		manifest.AdmissionIntentDigest != runIntent ||
		manifest.Workspace.ID != receipt.workspaceID ||
		runTenant != receipt.tenantID || runWorkspace != receipt.workspaceID ||
		runAdmission != receipt.admissionKey || len(manifest.Members) != 1 ||
		manifest.Members[0].MemberID != memberID ||
		manifest.Members[0].Digest != memberDigest {
		return acceptedChannelClosure{}, channelIntegrity("ACCEPTED ingress does not close its Run")
	}
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil || member.MemberSnapshotDigest != memberDigest ||
		member.MemberID != memberID || member.Workspace.ID != receipt.workspaceID ||
		member.Agent.ID != manifest.PrimaryAgent.ID {
		return acceptedChannelClosure{}, channelIntegrity("ACCEPTED ingress member closure differs")
	}

	control, catalog, err := inspectChannelControlCatalog(
		ctx,
		database,
		receipt.tenantID,
		controlID,
		catalogID,
	)
	if err != nil {
		return acceptedChannelClosure{}, err
	}
	workspace, found := control.FindWorkspace(receipt.workspaceID)
	if !found {
		return acceptedChannelClosure{}, channelIntegrity("ACCEPTED Workspace is absent from frozen Control")
	}
	endpoint, found := workspace.FindChannelEndpoint(receipt.endpointID)
	if !found || !endpoint.Enabled || endpoint.CursorScopeKey != receipt.cursorScopeKey ||
		endpoint.TargetAgentID != member.Agent.ID ||
		endpoint.TargetProfileID != member.Profile.ID ||
		endpoint.Binding.Port.Name != moduleapi.PortNameChannelTransport ||
		endpoint.Binding.Port.ExactVersion != moduleapi.PortVersionV1 {
		return acceptedChannelClosure{}, channelIntegrity("ACCEPTED Endpoint/Workspace target differs")
	}
	binding, err := inspectResolvedChannelBinding(endpoint, catalog)
	if err != nil {
		return acceptedChannelClosure{}, err
	}
	bindingCanonical, bindingDigest, err :=
		moduleapi.CanonicalChannelEndpointBindingV1(binding)
	if err != nil || bindingDigest != receipt.endpointBindingDigest {
		return acceptedChannelClosure{}, channelIntegrity("ACCEPTED Endpoint Binding digest differs")
	}
	if err := inspectChannelBindingContent(ctx, database, receipt, envelope, binding); err != nil {
		return acceptedChannelClosure{}, err
	}
	if !memberContainsExactChannelBinding(member, endpoint.Binding.Port, bindingCanonical) {
		return acceptedChannelClosure{}, channelIntegrity("ACCEPTED Binding is absent from member snapshot")
	}
	identityFound := false
	for _, identity := range workspace.ChannelIdentities {
		if identity.Channel == endpoint.Channel && identity.AccountID == endpoint.AccountID &&
			identity.ExternalUserID == envelope.ExternalUserID {
			if !identity.Active || identity.PrincipalID != receipt.principalID ||
				identity.ACLEpoch != uint64(receipt.aclEpoch) {
				return acceptedChannelClosure{}, channelIntegrity("ACCEPTED Principal/ACL differs")
			}
			identityFound = true
			break
		}
	}
	if !identityFound {
		return acceptedChannelClosure{}, channelIntegrity("ACCEPTED Principal is not authorized")
	}
	return acceptedChannelClosure{
		receipt:          receipt,
		envelope:         envelope,
		member:           member,
		binding:          binding,
		bindingCanonical: bindingCanonical,
	}, nil
}

func inspectChannelControlCatalog(
	ctx context.Context,
	database semanticQueryer,
	tenantID, controlID, catalogID string,
) (controlcontract.ControlSnapshot, controlcontract.CatalogGeneration, error) {
	var controlTenant, controlDigest string
	var controlRevision int64
	var controlCanonical []byte
	if err := database.QueryRowContext(ctx, `
		SELECT tenant_id, revision, canonical_json, digest
		FROM control_snapshots WHERE snapshot_id=?
	`, controlID).Scan(
		&controlTenant,
		&controlRevision,
		&controlCanonical,
		&controlDigest,
	); err != nil || controlRevision <= 0 {
		return controlcontract.ControlSnapshot{}, controlcontract.CatalogGeneration{},
			channelIntegrity("Channel frozen Control is unavailable")
	}
	control, err := controlcontract.RestoreControlSnapshot(
		controlCanonical,
		controlcontract.ControlSnapshotRef{
			SnapshotID: controlID,
			Revision:   uint64(controlRevision),
			Digest:     controlDigest,
		},
	)
	if err != nil || controlTenant != tenantID || control.TenantID != tenantID {
		return controlcontract.ControlSnapshot{}, controlcontract.CatalogGeneration{},
			channelIntegrity("Channel frozen Control identity differs")
	}
	var catalogTenant, catalogControlID, catalogDigest string
	var catalogGeneration int64
	var catalogCanonical []byte
	if err := database.QueryRowContext(ctx, `
		SELECT tenant_id, generation, control_snapshot_id, canonical_json, digest
		FROM runtime_catalog_generations WHERE generation_id=?
	`, catalogID).Scan(
		&catalogTenant,
		&catalogGeneration,
		&catalogControlID,
		&catalogCanonical,
		&catalogDigest,
	); err != nil || catalogGeneration <= 0 {
		return controlcontract.ControlSnapshot{}, controlcontract.CatalogGeneration{},
			channelIntegrity("Channel frozen Catalog is unavailable")
	}
	catalog, err := controlcontract.RestoreCatalogGeneration(
		catalogCanonical,
		controlcontract.CatalogGenerationRef{
			GenerationID: catalogID,
			Generation:   uint64(catalogGeneration),
			Digest:       catalogDigest,
		},
	)
	if err != nil || catalogTenant != tenantID || catalog.TenantID != tenantID ||
		catalogControlID != controlID || catalog.ControlSnapshotID != controlID ||
		catalog.ControlSnapshotDigest != controlDigest {
		return controlcontract.ControlSnapshot{}, controlcontract.CatalogGeneration{},
			channelIntegrity("Channel frozen Catalog/Control identity differs")
	}
	return control, catalog, nil
}

func inspectResolvedChannelBinding(
	endpoint controlcontract.ChannelEndpointDefinition,
	catalog controlcontract.CatalogGeneration,
) (moduleapi.PortBinding, error) {
	entry, found := catalog.FindInstance(endpoint.Binding.InstanceID)
	if !found {
		return moduleapi.PortBinding{}, channelIntegrity("Channel Endpoint provider is absent from Catalog")
	}
	provided := false
	for _, port := range entry.Provides {
		if port == endpoint.Binding.Port {
			provided = true
			break
		}
	}
	if !provided {
		return moduleapi.PortBinding{}, channelIntegrity("Channel Endpoint provider does not provide its Port")
	}
	binding := moduleapi.PortBinding{
		Provider:            entry.Activation,
		ConfigRef:           endpoint.Binding.ConfigRef,
		AuthorityCeilingRef: endpoint.Binding.AuthorityCeilingRef,
		StaticContextRefs: append(
			[]string(nil),
			endpoint.Binding.StaticContextRefs...,
		),
		FailurePolicy: endpoint.Binding.FailurePolicy,
	}
	if _, _, err := moduleapi.CanonicalChannelEndpointBindingV1(binding); err != nil {
		return moduleapi.PortBinding{}, channelIntegrity("Channel Endpoint resolved Binding is invalid")
	}
	return binding, nil
}

func inspectChannelBindingContent(
	ctx context.Context,
	database semanticQueryer,
	receipt inspectedChannelReceipt,
	envelope moduleapi.ChannelInboundEnvelopeV1,
	binding moduleapi.PortBinding,
) error {
	config, err := inspectExactChannelContent(
		ctx,
		database,
		binding.ConfigRef,
		currentstore.ContentConfig,
		moduleapi.MaxConfigBytes,
		false,
	)
	if err != nil {
		return err
	}
	if _, err := moduleapi.RestoreChannelBindingConfigV1(config.canonical); err != nil {
		return channelIntegrity("Channel Binding Config is invalid")
	}
	authorityContent, err := inspectExactChannelContent(
		ctx,
		database,
		binding.AuthorityCeilingRef,
		currentstore.ContentAuthorityCeiling,
		moduleapi.MaxConfigBytes,
		false,
	)
	if err != nil {
		return err
	}
	authority, err := moduleapi.RestoreChannelAuthorityCeilingV1(
		authorityContent.canonical,
	)
	if err != nil || authority.TenantID != receipt.tenantID || !authority.AllowReceive ||
		!channelStringSetContains(authority.AllowedWorkspaceIDs, receipt.workspaceID) ||
		!channelStringSetContains(authority.AllowedEndpointIDs, receipt.endpointID) ||
		len([]byte(envelope.Message)) > int(authority.MaxMessageBytes) {
		return channelIntegrity("Channel receive Authority does not close ACCEPTED ingress")
	}
	return nil
}

func channelStringSetContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func memberContainsExactChannelBinding(
	member corecontract.MemberExecutionSnapshot,
	port moduleapi.PortRef,
	wantCanonical []byte,
) bool {
	for _, plan := range member.PortPlans {
		if plan.Port != port || len(plan.Bindings) != 1 {
			continue
		}
		canonical, _, err := moduleapi.CanonicalChannelEndpointBindingV1(plan.Bindings[0])
		if err == nil && bytes.Equal(canonical, wantCanonical) {
			return true
		}
	}
	return false
}

func inspectChannelSendSemanticClosure(
	ctx context.Context,
	database semanticQueryer,
	accepted map[string]acceptedChannelClosure,
) error {
	rows, err := database.QueryContext(ctx, `
		SELECT attempt_id, run_id, logical_step_id, source_model_attempt_id
		FROM dispatch_attempts
		WHERE dispatch_kind='CHANNEL_SEND'
		ORDER BY run_id, created_at, attempt_id
	`)
	if err != nil {
		return fmt.Errorf("currentbackup: read Channel send IDs: %w", err)
	}
	identities := make([]inspectedChannelSendIdentity, 0)
	byRun := make(map[string][]inspectedChannelSendIdentity)
	for rows.Next() {
		var identity inspectedChannelSendIdentity
		if err := rows.Scan(
			&identity.attemptID,
			&identity.runID,
			&identity.logicalStepID,
			&identity.sourceModelID,
		); err != nil {
			_ = rows.Close()
			return fmt.Errorf("currentbackup: scan Channel send ID: %w", err)
		}
		if identity.logicalStepID != corecontract.ChannelSendLogicalStepIDV1 {
			_ = rows.Close()
			return channelIntegrity("CHANNEL_SEND does not use the frozen logical step")
		}
		identities = append(identities, identity)
		byRun[identity.runID] = append(byRun[identity.runID], identity)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("currentbackup: iterate Channel send IDs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("currentbackup: close Channel send IDs: %w", err)
	}
	for runID, attempts := range byRun {
		if len(attempts) > 1 {
			return channelIntegrity("ACCEPTED Run has more than one CHANNEL_SEND")
		}
		if _, found := accepted[runID]; !found {
			return channelIntegrity("CHANNEL_SEND is not owned by an ACCEPTED Run")
		}
	}
	for runID := range accepted {
		if err := inspectAcceptedChannelReverseClosure(
			ctx,
			database,
			runID,
			byRun[runID],
		); err != nil {
			return err
		}
	}
	for _, identity := range identities {
		if err := inspectOneChannelSend(
			ctx,
			database,
			identity.attemptID,
			accepted,
		); err != nil {
			return err
		}
	}
	return nil
}

func inspectAcceptedChannelReverseClosure(
	ctx context.Context,
	database semanticQueryer,
	runID string,
	attempts []inspectedChannelSendIdentity,
) error {
	rows, err := database.QueryContext(ctx, `
		SELECT attempt_id, result_ref
		FROM model_dispatch_attempts
		WHERE run_id=? AND state='SUCCEEDED'
		ORDER BY created_at, attempt_id
	`, runID)
	if err != nil {
		return fmt.Errorf("currentbackup: read accepted Channel Model results: %w", err)
	}
	type modelResultRef struct {
		attemptID string
		resultRef string
	}
	modelResults := make([]modelResultRef, 0, 2)
	for rows.Next() {
		var result modelResultRef
		if err := rows.Scan(&result.attemptID, &result.resultRef); err != nil {
			_ = rows.Close()
			return fmt.Errorf("currentbackup: scan accepted Channel Model result: %w", err)
		}
		modelResults = append(modelResults, result)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("currentbackup: iterate accepted Channel Model results: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("currentbackup: close accepted Channel Model results: %w", err)
	}
	finalModelIDs := make([]string, 0, 1)
	for _, result := range modelResults {
		content, err := inspectExactChannelContent(
			ctx,
			database,
			result.resultRef,
			currentstore.ContentModelResult,
			moduleapi.MaxTextBytes,
			false,
		)
		if err != nil {
			return err
		}
		output, err := moduleapi.RestoreModelGenerateOutputV1(content.canonical)
		if err != nil {
			return channelIntegrity("ACCEPTED Run has an invalid successful Model result")
		}
		if output.ActionRequest == nil && output.AssistantText != "" {
			finalModelIDs = append(finalModelIDs, result.attemptID)
		}
	}
	if len(finalModelIDs) > 1 {
		return channelIntegrity("ACCEPTED Run has more than one final Assistant Model result")
	}
	if len(finalModelIDs) == 0 {
		if len(attempts) != 0 {
			return channelIntegrity("CHANNEL_SEND lacks a final Assistant Model result")
		}
		return nil
	}
	if len(attempts) != 1 || attempts[0].sourceModelID != finalModelIDs[0] {
		return channelIntegrity("final Assistant Model result does not close to one CHANNEL_SEND")
	}
	return nil
}

func inspectOneChannelSend(
	ctx context.Context,
	database semanticQueryer,
	attemptID string,
	accepted map[string]acceptedChannelClosure,
) error {
	var (
		logicalOperationKey, runID, memberID, logicalStepID string
		sourceModelID, memberDigest, endpointID, ingressKey string
		proposalRef, effectClass, budgetStateRef, state     string
		bindingCanonical                                    []byte
		bindingIndex, frameRevision, maxResultBytes         int64
		deadline, revision, createdAt, updatedAt            int64
		externalOperation, receiptRef, resultRef            sql.NullString
		errorClassification, evidenceRef, unknownReason     sql.NullString
		actionID, providerActionID, definitionDigest        sql.NullString
		actionProposalRef                                   sql.NullString
	)
	if err := database.QueryRowContext(ctx, `
		SELECT
			attempt_id, logical_operation_key, run_id, member_id,
			logical_step_id, source_model_attempt_id, frame_revision,
			member_snapshot_digest, binding_index, binding_json,
			public_action_id, provider_action_id, definition_digest,
			proposal_ref, channel_endpoint_id, channel_ingress_key,
			channel_proposal_ref, effect_class, max_result_bytes,
			deadline, budget_state_ref, state, external_operation_id,
			provider_receipt_ref, result_ref, error_classification,
			reconciliation_evidence_ref, unknown_reason,
			revision, created_at, updated_at
		FROM dispatch_attempts
		WHERE attempt_id=? AND dispatch_kind='CHANNEL_SEND'
	`, attemptID).Scan(
		&attemptID,
		&logicalOperationKey,
		&runID,
		&memberID,
		&logicalStepID,
		&sourceModelID,
		&frameRevision,
		&memberDigest,
		&bindingIndex,
		&bindingCanonical,
		&actionID,
		&providerActionID,
		&definitionDigest,
		&actionProposalRef,
		&endpointID,
		&ingressKey,
		&proposalRef,
		&effectClass,
		&maxResultBytes,
		&deadline,
		&budgetStateRef,
		&state,
		&externalOperation,
		&receiptRef,
		&resultRef,
		&errorClassification,
		&evidenceRef,
		&unknownReason,
		&revision,
		&createdAt,
		&updatedAt,
	); err != nil {
		return channelIntegrity(fmt.Sprintf("read CHANNEL_SEND %q: %v", attemptID, err))
	}
	if actionID.Valid || providerActionID.Valid || definitionDigest.Valid ||
		actionProposalRef.Valid || !moduleapi.ValidSHA256(logicalOperationKey) ||
		!moduleapi.ValidSHA256(memberDigest) || !moduleapi.ValidSHA256(ingressKey) ||
		!moduleapi.ValidSHA256(proposalRef) || bindingIndex < 0 || frameRevision < 0 ||
		maxResultBytes != moduleapi.MaxChannelProviderReceiptBytesV1 || deadline <= 0 ||
		revision < 0 || createdAt <= 0 || updatedAt < createdAt ||
		effectClass != string(moduleapi.EffectIrreversibleWrite) {
		return channelIntegrity("CHANNEL_SEND scalar projection differs")
	}
	wantOperation, err := channelSendLogicalOperationKey(runID, memberID, logicalStepID)
	if err != nil || wantOperation != logicalOperationKey {
		return channelIntegrity("CHANNEL_SEND logical operation key differs")
	}
	if _, err := corecontract.ParseBudgetStateRefV1(budgetStateRef, runID); err != nil {
		return channelIntegrity("CHANNEL_SEND BudgetStateRef differs")
	}
	closure, found := accepted[runID]
	if !found || closure.receipt.endpointID != endpointID ||
		closure.receipt.ingressKey != ingressKey || closure.member.MemberID != memberID ||
		closure.member.MemberSnapshotDigest != memberDigest {
		return channelIntegrity("CHANNEL_SEND does not close to ACCEPTED ingress/member")
	}
	if int64(len(closure.member.PortPlans)) == 0 {
		return channelIntegrity("CHANNEL_SEND member has no PortPlan")
	}
	plan, found := exactChannelPortPlan(closure.member)
	if !found || bindingIndex >= int64(len(plan.Bindings)) {
		return channelIntegrity("CHANNEL_SEND BindingIndex is outside channel.transport/v1")
	}
	storedBinding, storedCanonical, err := restoreExactChannelBinding(bindingCanonical)
	if err != nil || !bytes.Equal(storedCanonical, closure.bindingCanonical) ||
		storedBinding.Provider != closure.binding.Provider ||
		!equalChannelBinding(storedBinding, plan.Bindings[bindingIndex]) {
		return channelIntegrity("CHANNEL_SEND exact Binding differs")
	}

	proposalContent, err := inspectExactChannelContent(
		ctx,
		database,
		proposalRef,
		currentstore.ContentChannelSendProposal,
		2*moduleapi.MaxConfigBytes,
		false,
	)
	if err != nil {
		return err
	}
	proposalWireDigest, err := moduleapi.ChannelSendProposalDigestV1(
		proposalContent.canonical,
	)
	if err != nil {
		return channelIntegrity("CHANNEL_SEND Proposal is invalid")
	}
	proposal, err := moduleapi.RestoreChannelSendProposalV1(
		proposalContent.canonical,
		proposalWireDigest,
	)
	if err != nil || proposal.MemberSnapshotDigest != memberDigest ||
		proposal.EndpointID != endpointID || proposal.IngressKey != ingressKey ||
		!bytes.Equal(proposal.ReplyTarget, closure.envelope.ReplyTarget) {
		return channelIntegrity("CHANNEL_SEND Proposal identity differs")
	}

	assistantText, err := inspectFinalChannelModel(
		ctx,
		database,
		sourceModelID,
		runID,
		memberID,
		attemptID,
	)
	if err != nil {
		return err
	}
	if err := inspectChannelSendAuthority(
		ctx,
		database,
		closure,
		endpointID,
		assistantText,
	); err != nil {
		return err
	}
	_, rebuiltCanonical, _, err := moduleapi.NewChannelSendProposalV1(
		memberDigest,
		endpointID,
		ingressKey,
		proposal.ReplyTarget,
		assistantText,
		proposal.PreparedPayload,
	)
	if err != nil || !bytes.Equal(rebuiltCanonical, proposalContent.canonical) {
		return channelIntegrity("CHANNEL_SEND Proposal does not close final Model text")
	}

	var result *moduleapi.ChannelExecutionResultV1
	if resultRef.Valid {
		content, err := inspectExactChannelContent(
			ctx,
			database,
			resultRef.String,
			currentstore.ContentChannelSendResult,
			2*moduleapi.MaxConfigBytes,
			false,
		)
		if err != nil {
			return err
		}
		executed, err := moduleapi.RestoreChannelExecutionResultV1(content.canonical)
		if err != nil || executed.AttemptID != attemptID {
			return channelIntegrity("CHANNEL_SEND Result closure differs")
		}
		result = &executed
	}
	var receipt *inspectedChannelContent
	if receiptRef.Valid {
		content, err := inspectExactChannelContent(
			ctx,
			database,
			receiptRef.String,
			currentstore.ContentProviderReceipt,
			moduleapi.MaxChannelProviderReceiptBytesV1,
			false,
		)
		if err != nil {
			return err
		}
		if err := inspectCanonicalChannelJSONObject(content.canonical); err != nil {
			return channelIntegrity("CHANNEL_SEND Provider receipt is not a canonical object")
		}
		receipt = &content
	}
	if evidenceRef.Valid {
		evidence, err := inspectExactChannelContent(
			ctx,
			database,
			evidenceRef.String,
			currentstore.ContentReconciliationEvidence,
			moduleapi.MaxChannelProviderReceiptBytesV1,
			false,
		)
		if err != nil {
			return err
		}
		if err := inspectCanonicalChannelJSONObject(evidence.canonical); err != nil {
			return channelIntegrity("CHANNEL_SEND reconciliation evidence is not a canonical object")
		}
	}
	if err := inspectChannelSendState(
		state,
		externalOperation.String,
		receipt,
		result,
		errorClassification.String,
		evidenceRef.String,
		unknownReason.String,
	); err != nil {
		return err
	}
	return inspectChannelRuntimeProjection(
		ctx,
		database,
		inspectedChannelRuntimeProjection{
			attemptID:           attemptID,
			runID:               runID,
			logicalStepID:       logicalStepID,
			sourceModelID:       sourceModelID,
			logicalOperationKey: logicalOperationKey,
			proposalRef:         proposalRef,
			resultRef:           resultRef.String,
			budgetStateRef:      budgetStateRef,
			state:               state,
			frameRevision:       frameRevision,
			updatedAt:           updatedAt,
		},
	)
}

func inspectCanonicalChannelJSONObject(canonical []byte) error {
	checked, err := moduleapi.CanonicalJSONWithLimits(
		canonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: moduleapi.MaxChannelProviderReceiptBytesV1,
			MaxDepth: 32,
			MaxNodes: 64 << 10,
		},
	)
	if err != nil || !bytes.Equal(checked, canonical) {
		return fmt.Errorf("JSON object is not bounded exact canonical JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return fmt.Errorf("not a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("JSON object has trailing data")
	}
	return nil
}

func inspectChannelSendAuthority(
	ctx context.Context,
	database semanticQueryer,
	closure acceptedChannelClosure,
	endpointID, assistantText string,
) error {
	content, err := inspectExactChannelContent(
		ctx,
		database,
		closure.binding.AuthorityCeilingRef,
		currentstore.ContentAuthorityCeiling,
		moduleapi.MaxConfigBytes,
		false,
	)
	if err != nil {
		return err
	}
	authority, err := moduleapi.RestoreChannelAuthorityCeilingV1(content.canonical)
	if err != nil || authority.TenantID != closure.receipt.tenantID ||
		!authority.AllowSend ||
		!channelStringSetContains(
			authority.AllowedWorkspaceIDs,
			closure.receipt.workspaceID,
		) ||
		!channelStringSetContains(authority.AllowedEndpointIDs, endpointID) ||
		len([]byte(assistantText)) > int(authority.MaxMessageBytes) {
		return channelIntegrity("CHANNEL_SEND Authority does not close final Model text")
	}
	return nil
}

func inspectFinalChannelModel(
	ctx context.Context,
	database semanticQueryer,
	attemptID, runID, memberID, channelAttemptID string,
) (string, error) {
	var modelRun, modelMember, state, resultRef string
	var modelUpdatedAt int64
	if err := database.QueryRowContext(ctx, `
		SELECT run_id, member_id, state, result_ref, updated_at
		FROM model_dispatch_attempts WHERE attempt_id=?
	`, attemptID).Scan(
		&modelRun,
		&modelMember,
		&state,
		&resultRef,
		&modelUpdatedAt,
	); err != nil ||
		modelRun != runID || modelMember != memberID ||
		state != string(corecontract.ModelAttemptSucceeded) ||
		!moduleapi.ValidSHA256(resultRef) || modelUpdatedAt <= 0 {
		return "", channelIntegrity("CHANNEL_SEND final Model Attempt differs")
	}
	var continuationCount int64
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM model_dispatch_attempts
		WHERE source_dispatch_attempt_id=?
	`, channelAttemptID).Scan(&continuationCount); err != nil || continuationCount != 0 {
		return "", channelIntegrity("CHANNEL_SEND has a downstream Model continuation")
	}
	content, err := inspectExactChannelContent(
		ctx,
		database,
		resultRef,
		currentstore.ContentModelResult,
		moduleapi.MaxTextBytes,
		false,
	)
	if err != nil {
		return "", err
	}
	output, err := moduleapi.RestoreModelGenerateOutputV1(content.canonical)
	if err != nil || output.ActionRequest != nil || output.AssistantText == "" {
		return "", channelIntegrity("CHANNEL_SEND final Model output is not assistant text")
	}
	if err := inspectFinalChannelAssistantHistory(
		ctx,
		database,
		attemptID,
		runID,
		memberID,
		resultRef,
		modelUpdatedAt,
	); err != nil {
		return "", err
	}
	return output.AssistantText, nil
}

func inspectFinalChannelAssistantHistory(
	ctx context.Context,
	database semanticQueryer,
	attemptID, runID, memberID, resultRef string,
	modelUpdatedAt int64,
) error {
	var count int64
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM history_entries WHERE source_attempt_id=?
	`, attemptID).Scan(&count); err != nil || count != 1 {
		return channelIntegrity("CHANNEL_SEND source Model does not own one History entry")
	}
	var (
		historyRun, historyMember, role, contentRef, contentDigest string
		sourceAttempt                                              string
		createdAt                                                  int64
	)
	if err := database.QueryRowContext(ctx, `
		SELECT run_id, member_id, role, content_ref, content_digest,
		       source_attempt_id, created_at
		FROM history_entries WHERE source_attempt_id=?
	`, attemptID).Scan(
		&historyRun,
		&historyMember,
		&role,
		&contentRef,
		&contentDigest,
		&sourceAttempt,
		&createdAt,
	); err != nil || historyRun != runID || historyMember != memberID ||
		role != string(moduleapi.ModelRoleAssistant) || contentRef != resultRef ||
		contentDigest != resultRef || sourceAttempt != attemptID ||
		createdAt != modelUpdatedAt {
		return channelIntegrity("CHANNEL_SEND source Model History closure differs")
	}
	return nil
}

func inspectChannelRuntimeProjection(
	ctx context.Context,
	database semanticQueryer,
	projection inspectedChannelRuntimeProjection,
) error {
	var (
		runState, frameStep, frameBudget             string
		continuation                                 []byte
		runDisposition                               sql.NullString
		pendingModel, pendingDispatch, waitingReason sql.NullString
		runRevision, runUpdatedAt                    int64
		frameRevision, lastEvent                     int64
	)
	if err := database.QueryRowContext(ctx, `
		SELECT r.state, r.disposition, r.revision, r.updated_at,
		       f.frame_revision, f.step, f.budget_state_ref, f.continuation,
		       f.pending_attempt_id, f.pending_dispatch_attempt_id,
		       f.waiting_reason, f.last_authoritative_event
		FROM runs AS r
		JOIN loop_frames AS f ON f.run_id=r.run_id
		WHERE r.run_id=?
	`, projection.runID).Scan(
		&runState,
		&runDisposition,
		&runRevision,
		&runUpdatedAt,
		&frameRevision,
		&frameStep,
		&frameBudget,
		&continuation,
		&pendingModel,
		&pendingDispatch,
		&waitingReason,
		&lastEvent,
	); err != nil {
		return channelIntegrity("CHANNEL_SEND Run/Frame projection is unavailable")
	}
	continued, err := corecontract.RestoreLoopContinuationV1(continuation)
	if err != nil || continued.State != frameStep ||
		continued.AttemptKind != corecontract.AttemptKindChannel ||
		continued.AttemptID != projection.attemptID ||
		continued.LogicalStepID != projection.logicalStepID ||
		frameBudget != projection.budgetStateRef ||
		frameRevision <= projection.frameRevision ||
		runRevision <= 0 || lastEvent <= 0 ||
		runUpdatedAt != projection.updatedAt {
		return channelIntegrity(fmt.Sprintf(
			"CHANNEL_SEND Run/Frame continuation projection differs: run=%q disposition=%q run_revision=%d frame_revision=%d attempt_frame_revision=%d last_event=%d frame_step=%q continuation=%+v frame_budget_match=%t run_updated_at=%d attempt_updated_at=%d",
			runState,
			runDisposition.String,
			runRevision,
			frameRevision,
			projection.frameRevision,
			lastEvent,
			frameStep,
			continued,
			frameBudget == projection.budgetStateRef,
			runUpdatedAt,
			projection.updatedAt,
		))
	}

	expectedEventKind := channelDispatchTerminalEvent
	switch projection.state {
	case string(currentstore.DispatchPending):
		expectedEventKind = channelDispatchPendingEvent
		if runState != corecontract.InitialRunState || runDisposition.Valid ||
			frameStep != corecontract.ChannelPendingLoopStep || pendingModel.Valid ||
			!pendingDispatch.Valid || pendingDispatch.String != projection.attemptID ||
			waitingReason.Valid {
			return channelIntegrity("PENDING CHANNEL_SEND Run/Frame projection differs")
		}
	case string(currentstore.DispatchUnknown):
		if runState != corecontract.WaitingReconciliationLoopStep ||
			!runDisposition.Valid ||
			runDisposition.String != corecontract.WaitingReconciliationLoopStep ||
			frameStep != corecontract.WaitingReconciliationLoopStep ||
			pendingModel.Valid || pendingDispatch.Valid || !waitingReason.Valid ||
			waitingReason.String != channelUnknownWaitingReason {
			return channelIntegrity("UNKNOWN CHANNEL_SEND Run/Frame projection differs")
		}
	case string(currentstore.DispatchSucceeded), string(currentstore.DispatchFailed):
		if runState != corecontract.TerminatedLoopStep || !runDisposition.Valid ||
			runDisposition.String != corecontract.TerminatedLoopStep ||
			frameStep != corecontract.TerminatedLoopStep || pendingModel.Valid ||
			pendingDispatch.Valid || waitingReason.Valid {
			return channelIntegrity("terminal CHANNEL_SEND Run/Frame projection differs")
		}
	default:
		return channelIntegrity("CHANNEL_SEND runtime projection has unknown state")
	}

	var (
		eventKind, payloadRef, payloadDigest     string
		fromRevision, toRevision, eventCreatedAt int64
	)
	if err := database.QueryRowContext(ctx, `
		SELECT event_kind, from_revision, to_revision,
		       payload_ref, payload_digest, created_at
		FROM run_events
		WHERE run_id=? AND event_sequence=?
	`, projection.runID, lastEvent).Scan(
		&eventKind,
		&fromRevision,
		&toRevision,
		&payloadRef,
		&payloadDigest,
		&eventCreatedAt,
	); err != nil || eventKind != expectedEventKind ||
		fromRevision < 0 || toRevision > frameRevision ||
		toRevision <= projection.frameRevision ||
		fromRevision+1 != toRevision || payloadRef != payloadDigest ||
		eventCreatedAt != projection.updatedAt {
		return channelIntegrity("CHANNEL_SEND latest Run event projection differs")
	}
	eventContent, err := inspectExactChannelContent(
		ctx,
		database,
		payloadRef,
		currentstore.ContentRunEventPayload,
		moduleapi.MaxConfigBytes,
		false,
	)
	if err != nil {
		return err
	}
	event, err := restoreInspectedChannelDispatchEvent(eventContent.canonical)
	if err != nil {
		return channelIntegrity("CHANNEL_SEND latest Run event is not canonical")
	}
	if event.SchemaVersion != channelDispatchEventSchemaV1 ||
		event.RunID != projection.runID || event.AttemptID != projection.attemptID ||
		event.LogicalStepID != projection.logicalStepID ||
		event.LogicalOperationKey != projection.logicalOperationKey ||
		event.ProposalDigest != projection.proposalRef ||
		string(event.State) != projection.state ||
		event.ResultDigest != projection.resultRef {
		return channelIntegrity("CHANNEL_SEND latest Run event identity differs")
	}
	if projection.state == string(currentstore.DispatchPending) {
		if event.TransitionOrigin != corecontract.DispatchTransitionBeginV1 ||
			event.SourceModelAttemptID != projection.sourceModelID ||
			event.SourceModelUsage == nil || event.SourceModelUsage.Validate() != nil ||
			!moduleapi.ValidSHA256(event.SourceModelSemanticDigest) {
			return channelIntegrity("PENDING CHANNEL_SEND event provenance differs")
		}
	} else if event.TransitionOrigin == corecontract.DispatchTransitionBeginV1 ||
		event.SourceModelAttemptID != "" || event.SourceModelUsage != nil ||
		event.SourceModelSemanticDigest != "" {
		return channelIntegrity("terminal CHANNEL_SEND event provenance differs")
	}
	return nil
}

func restoreInspectedChannelDispatchEvent(
	canonical []byte,
) (inspectedChannelDispatchEventV1, error) {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var event inspectedChannelDispatchEventV1
	if err := decoder.Decode(&event); err != nil {
		return inspectedChannelDispatchEventV1{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return inspectedChannelDispatchEventV1{}, fmt.Errorf("event has trailing JSON")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return inspectedChannelDispatchEventV1{}, err
	}
	rebuilt, err := moduleapi.CanonicalJSON(raw)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		return inspectedChannelDispatchEventV1{}, fmt.Errorf("event is not exact canonical JSON")
	}
	if event.SchemaVersion != channelDispatchEventSchemaV1 ||
		validateOpaque("Channel event Run ID", event.RunID) != nil ||
		validateOpaque("Channel event Attempt ID", event.AttemptID) != nil ||
		validateOpaque("Channel event logical step ID", event.LogicalStepID) != nil ||
		!moduleapi.ValidSHA256(event.LogicalOperationKey) ||
		!moduleapi.ValidSHA256(event.ProposalDigest) ||
		!moduleapi.ValidSHA256(event.ResourceSemanticDigest) ||
		event.TransitionOrigin.Validate() != nil ||
		(event.ResultDigest != "" && !moduleapi.ValidSHA256(event.ResultDigest)) {
		return inspectedChannelDispatchEventV1{}, fmt.Errorf("invalid Channel event projection")
	}
	switch event.State {
	case currentstore.DispatchPending:
		if event.ResultDigest != "" ||
			event.TransitionOrigin != corecontract.DispatchTransitionBeginV1 ||
			validateOpaque("Channel event source Model Attempt ID", event.SourceModelAttemptID) != nil ||
			event.SourceModelUsage == nil || event.SourceModelUsage.Validate() != nil ||
			!moduleapi.ValidSHA256(event.SourceModelSemanticDigest) {
			return inspectedChannelDispatchEventV1{}, fmt.Errorf("invalid Channel begin event")
		}
	case currentstore.DispatchSucceeded:
		if !moduleapi.ValidSHA256(event.ResultDigest) ||
			event.TransitionOrigin == corecontract.DispatchTransitionBeginV1 ||
			event.SourceModelAttemptID != "" || event.SourceModelUsage != nil ||
			event.SourceModelSemanticDigest != "" {
			return inspectedChannelDispatchEventV1{}, fmt.Errorf("invalid successful Channel event")
		}
	case currentstore.DispatchFailed, currentstore.DispatchUnknown:
		if event.ResultDigest != "" ||
			event.TransitionOrigin == corecontract.DispatchTransitionBeginV1 ||
			event.SourceModelAttemptID != "" || event.SourceModelUsage != nil ||
			event.SourceModelSemanticDigest != "" {
			return inspectedChannelDispatchEventV1{}, fmt.Errorf("invalid terminal Channel event")
		}
	default:
		return inspectedChannelDispatchEventV1{}, fmt.Errorf("invalid Channel event state")
	}
	return event, nil
}

func inspectChannelSendState(
	state, externalOperation string,
	receipt *inspectedChannelContent,
	result *moduleapi.ChannelExecutionResultV1,
	errorClassification, evidenceRef, unknownReason string,
) error {
	switch state {
	case string(currentstore.DispatchPending):
		if externalOperation != "" || receipt != nil || result != nil ||
			errorClassification != "" || evidenceRef != "" || unknownReason != "" {
			return channelIntegrity("PENDING CHANNEL_SEND carries terminal facts")
		}
	case string(currentstore.DispatchSucceeded):
		if result == nil || result.Outcome != moduleapi.ChannelExecutionSucceeded ||
			errorClassification != "" || unknownReason != "" ||
			(externalOperation == "" && receipt == nil) ||
			result.ExternalOperationID != externalOperation ||
			((len(result.ProviderReceipt) == 0) != (receipt == nil)) ||
			(receipt != nil && !bytes.Equal(result.ProviderReceipt, receipt.canonical)) {
			return channelIntegrity("SUCCEEDED CHANNEL_SEND result/receipt facts differ")
		}
	case string(currentstore.DispatchFailed):
		if result != nil || errorClassification == "" || unknownReason != "" {
			return channelIntegrity("FAILED CHANNEL_SEND facts differ")
		}
	case string(currentstore.DispatchUnknown):
		if result != nil || errorClassification != "" ||
			(externalOperation == "" && receipt == nil && evidenceRef == "" && unknownReason == "") {
			return channelIntegrity("UNKNOWN CHANNEL_SEND facts differ")
		}
	default:
		return channelIntegrity("CHANNEL_SEND state is unknown")
	}
	return nil
}

func inspectExactChannelContent(
	ctx context.Context,
	database semanticQueryer,
	ref string,
	wantKind currentstore.ContentKind,
	maximum int,
	allowEmpty bool,
) (inspectedChannelContent, error) {
	var content inspectedChannelContent
	var kind string
	var size int64
	if err := database.QueryRowContext(ctx, `
		SELECT content_digest, kind, media_type, canonical_bytes, size_bytes
		FROM content_records WHERE content_digest=?
	`, ref).Scan(
		&content.digest,
		&kind,
		&content.mediaType,
		&content.canonical,
		&size,
	); err != nil {
		return inspectedChannelContent{}, channelIntegrity("Channel content reference is unavailable")
	}
	content.kind = currentstore.ContentKind(kind)
	computed, err := currentstore.ComputeContentDigest(
		content.kind,
		content.mediaType,
		content.canonical,
	)
	if err != nil || content.digest != ref || computed != ref ||
		content.kind != wantKind || content.mediaType != channelJSONMediaType ||
		size != int64(len(content.canonical)) || len(content.canonical) > maximum ||
		(!allowEmpty && len(content.canonical) == 0) {
		return inspectedChannelContent{}, channelIntegrity("Channel content identity or digest differs")
	}
	return content, nil
}

func exactChannelPortPlan(
	member corecontract.MemberExecutionSnapshot,
) (moduleapi.PortPlan, bool) {
	var result moduleapi.PortPlan
	found := false
	for _, plan := range member.PortPlans {
		if plan.Port.Name != moduleapi.PortNameChannelTransport ||
			plan.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		if found {
			return moduleapi.PortPlan{}, false
		}
		result = plan
		found = true
	}
	return result, found
}

func restoreExactChannelBinding(
	canonical []byte,
) (moduleapi.PortBinding, []byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var binding moduleapi.PortBinding
	if err := decoder.Decode(&binding); err != nil {
		return moduleapi.PortBinding{}, nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return moduleapi.PortBinding{}, nil, fmt.Errorf("binding has trailing JSON")
	}
	plan, err := moduleapi.NewPortPlan(moduleapi.PortPlan{
		Port: moduleapi.PortRef{
			Name:         moduleapi.PortNameChannelTransport,
			ExactVersion: moduleapi.PortVersionV1,
		},
		Bindings: []moduleapi.PortBinding{binding},
	})
	if err != nil {
		return moduleapi.PortBinding{}, nil, err
	}
	frozen := plan.Bindings[0]
	encoded, err := json.Marshal(frozen)
	if err != nil {
		return moduleapi.PortBinding{}, nil, err
	}
	rebuilt, err := moduleapi.CanonicalJSON(encoded)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		return moduleapi.PortBinding{}, nil, fmt.Errorf("binding is not exact canonical JSON")
	}
	return frozen, rebuilt, nil
}

func equalChannelBinding(left, right moduleapi.PortBinding) bool {
	leftCanonical, _, leftErr := moduleapi.CanonicalChannelEndpointBindingV1(left)
	rightCanonical, _, rightErr := moduleapi.CanonicalChannelEndpointBindingV1(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftCanonical, rightCanonical)
}

func channelSendLogicalOperationKey(
	runID, memberID, logicalStepID string,
) (string, error) {
	raw, err := json.Marshal(struct {
		RunID         string `json:"run_id"`
		MemberID      string `json:"member_id"`
		LogicalStepID string `json:"logical_step_id"`
	}{runID, memberID, logicalStepID})
	if err != nil {
		return "", err
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		return "", err
	}
	return moduleapi.Digest(channelOperationDigestDomainV1, canonical), nil
}

func channelIntegrity(subject string) error {
	return fmt.Errorf("%w: %s", ErrIntegrity, subject)
}
