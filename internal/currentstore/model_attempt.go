package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var (
	ErrInvalidModelDispatch = errors.New(
		"currentstore: invalid model dispatch",
	)
	ErrModelDispatchConflict = errors.New(
		"currentstore: model dispatch conflict",
	)
	ErrModelDispatchIntegrity = errors.New(
		"currentstore: model dispatch integrity violation",
	)
	ErrModelRecordNotFound = errors.New(
		"currentstore: model record not found",
	)
)

const (
	modelDeadlineExpiredBeforeDispatchClassification = "DEADLINE_EXPIRED_BEFORE_DISPATCH"
)

// BeginModelDispatchInput contains only Core-selected values that are not
// already frozen in the Run. Member identity, model Binding, provider/model,
// parameters and budget policy are always derived from the recovery closure.
type BeginModelDispatchInput struct {
	Lease                       RunLease
	AttemptID                   string
	LogicalStepID               string
	ContextCompilationCanonical []byte
	RequestCanonical            []byte
	Deadline                    time.Time
}

// ModelDispatchAttemptRecord is the detached authoritative attempt row.
type ModelDispatchAttemptRecord struct {
	AttemptID            string
	LogicalOperationKey  string
	RunID                string
	TenantID             string
	WorkspaceID          string
	MemberID             string
	LogicalStepID        string
	FrameRevision        uint64
	MemberSnapshotDigest string
	Binding              moduleapi.PortBinding
	BindingCanonical     []byte
	// ModelConfigCanonical is the exact model-binding-config/v2 content
	// resolved from Binding.ConfigRef for the one pre-network invocation
	// grant. It is transient gate material, not a second persisted column.
	ModelConfigCanonical []byte
	// ModelAuthorityCanonical is the exact authority ceiling resolved from
	// Binding.AuthorityCeilingRef for the same one-shot invocation grant. It is
	// transient and never creates a second persisted authority source.
	ModelAuthorityCanonical   []byte
	ContextCompilation        *ContentRecord
	Request                   ContentRecord
	Provider                  string
	Model                     string
	ParametersCanonical       []byte
	Deadline                  time.Time
	UsageLedgerRef            string
	SourceDispatchAttemptID   string
	State                     corecontract.ModelAttemptState
	ProviderRequestID         string
	ProviderReceiptRef        string
	ResultRef                 string
	ErrorClassification       string
	ReconciliationEvidenceRef string
	UnknownReason             string
	Revision                  uint64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// BeginModelDispatchResult is the only pre-network grant. InvokeAllowed is
// true exactly for the transaction that created PENDING. An exact retry
// returns the original Attempt with InvokeAllowed=false, preventing semantic
// replay after a lost response.
type BeginModelDispatchResult struct {
	Attempt       ModelDispatchAttemptRecord
	Lease         RunLease
	Created       bool
	InvokeAllowed bool
	permit        *modelInvocationPermit
}

type modelInvocationPermit struct {
	consumed atomic.Bool
	closure  modelInvocationPermitClosure
}

type modelInvocationPermitClosure struct {
	attemptID                   string
	runID                       string
	memberID                    string
	memberDigest                string
	attemptFrameRevision        uint64
	bindingCanonical            []byte
	modelConfigCanonical        []byte
	modelAuthorityCanonical     []byte
	contextCompilationDigest    string
	contextCompilationKind      ContentKind
	contextCompilationMediaType string
	contextCompilationCanonical []byte
	requestDigest               string
	requestKind                 ContentKind
	requestMediaType            string
	requestCanonical            []byte
	deadline                    time.Time
	lease                       RunLease
}

// ConsumeModelInvocationPermit atomically consumes the process-local grant
// created by the one transaction that first persisted PENDING. Copies of a
// result share the same private permit; exact retries and caller-constructed
// values have no permit and can never authorize invocation.
func (result BeginModelDispatchResult) ConsumeModelInvocationPermit() bool {
	if result.permit == nil ||
		!result.Created ||
		!result.InvokeAllowed ||
		!result.permit.matches(result.Attempt, result.Lease) {
		return false
	}
	return result.permit.consumed.CompareAndSwap(false, true)
}

// BeginModelDispatch commits the optional trusted Workspace REQUEST
// payload/envelope, CONTEXT_COMPILATION, MODEL_REQUEST, PENDING Attempt,
// UNKNOWN Usage placeholder, Frame pending identity and RunEvent before any
// adapter may run.
func (store *Store) BeginModelDispatch(
	ctx context.Context,
	input BeginModelDispatchInput,
) (BeginModelDispatchResult, error) {
	if ctx == nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidModelDispatch,
		)
	}
	hasContextCompilation := input.ContextCompilationCanonical != nil
	input.RequestCanonical = bytes.Clone(input.RequestCanonical)
	input.ContextCompilationCanonical = bytes.Clone(
		input.ContextCompilationCanonical,
	)
	if !validLeaseOpaqueID(input.AttemptID) ||
		!validLeaseOpaqueID(input.LogicalStepID) {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: invalid Attempt or logical step identity",
			ErrInvalidModelDispatch,
		)
	}
	if err := validateRunLeaseToken(input.Lease); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	deadline, err := normalizeModelDeadline(input.Deadline)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if deadline.UnixMicro() <= 0 {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: deadline is outside the persisted timestamp domain",
			ErrInvalidModelDispatch,
		)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		input.RequestCanonical,
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: request: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	requestDigest, err := ComputeContentDigest(
		ContentModelRequest,
		admissionJSONMediaType,
		input.RequestCanonical,
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: request content: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	var contextCompilationDigest string
	if hasContextCompilation {
		contextCompilationDigest, err = ComputeContentDigest(
			ContentContextCompilation,
			admissionJSONMediaType,
			input.ContextCompilationCanonical,
		)
		if err != nil {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"%w: context compilation content: %v",
				ErrInvalidModelDispatch,
				err,
			)
		}
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	defer unlock()

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"currentstore: acquire model dispatch connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"currentstore: begin BeginModelDispatch: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	existingID, found, err := findModelAttemptByStep(
		ctx,
		connection,
		input.Lease.RunID,
		input.LogicalStepID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if found {
		existing, err := queryModelDispatchAttempt(
			ctx,
			connection,
			existingID,
		)
		if err != nil {
			return BeginModelDispatchResult{}, err
		}
		if existing.AttemptID != input.AttemptID ||
			existing.Request.Digest != requestDigest ||
			!bytes.Equal(
				existing.Request.CanonicalBytes,
				input.RequestCanonical,
			) ||
			!exactContextCompilationInput(
				existing.ContextCompilation,
				hasContextCompilation,
				contextCompilationDigest,
				input.ContextCompilationCanonical,
			) ||
			!existing.Deadline.Equal(deadline) {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"%w: logical step %q already has another frozen attempt",
				ErrModelDispatchConflict,
				input.LogicalStepID,
			)
		}
		if existing.SourceDispatchAttemptID != "" {
			result, err := reopenSecondModelDispatch(
				ctx,
				connection,
				input,
				existing,
				request,
				requestDigest,
				deadline,
			)
			if err != nil {
				return BeginModelDispatchResult{}, err
			}
			if err := verifyCurrentOverviewResourceObservationV1(
				ctx, connection, overviewResourceModelV1, existing.AttemptID,
			); err != nil {
				return BeginModelDispatchResult{}, err
			}
			if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
				return BeginModelDispatchResult{}, fmt.Errorf(
					"currentstore: commit idempotent model-2 BeginModelDispatch: %w",
					err,
				)
			}
			committed = true
			return result, nil
		}
		currentLease, run, err := loadExactRetryRun(
			ctx,
			connection,
			input.Lease,
			existing,
		)
		if err != nil {
			return BeginModelDispatchResult{}, err
		}
		workspaceTransfer, err := loadWorkspaceTransferRequestV1(
			ctx,
			connection,
			run,
		)
		if err != nil {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"%w: existing Attempt Workspace transfer request: %v",
				ErrModelDispatchIntegrity,
				err,
			)
		}
		run, err = runWithWorkspaceTransferRecordV1(run, workspaceTransfer)
		if err != nil {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"%w: existing Attempt Workspace transfer closure: %v",
				ErrModelDispatchIntegrity,
				err,
			)
		}
		if err := validateFrozenContextCompilationForRun(
			existing.ContextCompilation,
			run,
			request,
			requestDigest,
			existing.FrameRevision,
		); err != nil {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"%w: existing Attempt context compilation: %v",
				ErrModelDispatchIntegrity,
				err,
			)
		}
		frozenBinding, err := exactModelBinding(run.Member)
		if err != nil {
			return BeginModelDispatchResult{}, err
		}
		frozenBindingCanonical, err :=
			canonicalModelBinding(frozenBinding)
		if err != nil {
			return BeginModelDispatchResult{}, err
		}
		if existing.MemberID != run.Member.MemberID ||
			existing.MemberSnapshotDigest !=
				run.Member.MemberSnapshotDigest ||
			!bytes.Equal(
				existing.BindingCanonical,
				frozenBindingCanonical,
			) {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"%w: existing Attempt is not closed by the frozen member",
				ErrModelDispatchIntegrity,
			)
		}
		config, err := frozenModelBindingConfig(run, frozenBinding)
		if err != nil {
			return BeginModelDispatchResult{}, err
		}
		expectedParameters, err := expectedModelParametersForRun(run, config.Parameters)
		if err != nil {
			return BeginModelDispatchResult{}, err
		}
		if existing.Provider != config.Provider ||
			existing.Model != config.Model ||
			!bytes.Equal(
				existing.ParametersCanonical,
				expectedParameters,
			) ||
			!bytes.Equal(request.Parameters, expectedParameters) {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"%w: existing Attempt does not match frozen model config",
				ErrModelDispatchIntegrity,
			)
		}
		if existing.State == corecontract.ModelAttemptPending {
			if run.Frame.PendingAttemptID != existing.AttemptID ||
				run.Frame.Step != corecontract.ModelPendingLoopStep {
				return BeginModelDispatchResult{}, fmt.Errorf(
					"%w: PENDING Attempt is not the current Frame attempt",
					ErrModelDispatchIntegrity,
				)
			}
			if err := verifyBeginPendingUsagePlaceholder(
				ctx,
				connection,
				existing,
			); err != nil {
				return BeginModelDispatchResult{}, err
			}
		}
		if err := verifyCurrentOverviewResourceObservationV1(
			ctx, connection, overviewResourceModelV1, existing.AttemptID,
		); err != nil {
			return BeginModelDispatchResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"currentstore: commit idempotent BeginModelDispatch: %w",
				err,
			)
		}
		committed = true
		return BeginModelDispatchResult{
			Attempt:       cloneModelDispatchAttempt(existing),
			Lease:         currentLease,
			Created:       false,
			InvokeAllowed: false,
		}, nil
	}
	deadlineExpired := deadline.UnixMicro() <= nowUnixMicro()

	run, err := loadRunForLoop(ctx, connection, input.Lease)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if err := checkNewModelDispatchFamilyPermit(
		ctx,
		connection,
		run,
		input.LogicalStepID,
	); err != nil {
		return BeginModelDispatchResult{}, err
	}
	readyStep := corecontract.InitialLoopStep
	compositeRoot := run.Manifest.Composite != nil &&
		run.Manifest.Composite.Role == corecontract.CompositeRunRoleRootV1
	compositeReviewer := run.Manifest.Composite != nil &&
		run.Manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1
	compositeCoordinator := compositeRoot || compositeReviewer
	if compositeCoordinator {
		readyStep = corecontract.WaitingChildrenLoopStep
	}
	if compositeRoot {
		if !allCompositeChildrenSucceeded(run.CompositeChildren) {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"%w: composite root cannot merge before every Specialist succeeds",
				ErrModelDispatchConflict,
			)
		}
	}
	if run.Frame.Step == corecontract.ModelReadyAfterActionLoopStep {
		result, err := commitSecondModelDispatch(
			ctx,
			connection,
			input,
			request,
			input.RequestCanonical,
			requestDigest,
			deadline,
			run,
		)
		if err != nil {
			return BeginModelDispatchResult{}, err
		}
		committed = true
		return result, nil
	}
	if run.Frame.Step != readyStep ||
		run.Frame.PendingAttemptID != "" {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: Run %q is not ready for a new semantic model step",
			ErrModelDispatchConflict,
			run.RunID,
		)
	}
	workspaceTransfer, err := PrepareWorkspaceTransferRequestV1(run)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: Workspace transfer request: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	run, err = runWithWorkspaceTransferRecordV1(run, workspaceTransfer)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: Workspace transfer recovery closure: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	var contextCompilation *ContentRecord
	if hasContextCompilation {
		contextCompilation = &ContentRecord{
			Digest:    contextCompilationDigest,
			Kind:      ContentContextCompilation,
			MediaType: admissionJSONMediaType,
			CanonicalBytes: bytes.Clone(
				input.ContextCompilationCanonical,
			),
			SizeBytes: int64(len(input.ContextCompilationCanonical)),
		}
	}
	if err := validateNewContextCompilationForRunAtCurrentHead(
		ctx,
		connection,
		contextCompilation,
		run,
		request,
		requestDigest,
		run.Frame.Revision,
	); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: context compilation: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	if deadline.After(run.Manifest.Deadline) {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: dispatch deadline exceeds frozen Run deadline",
			ErrInvalidModelDispatch,
		)
	}
	binding, err := exactModelBinding(run.Member)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	bindingCanonical, err := canonicalModelBinding(binding)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	config, err := frozenModelBindingConfig(run, binding)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	modelAuthorityCanonical, err := frozenModelBindingAuthority(run, binding)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	expectedParameters, err := expectedModelParametersForRun(run, config.Parameters)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if !bytes.Equal(request.Parameters, expectedParameters) {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: request parameters differ from frozen model config",
			ErrInvalidModelDispatch,
		)
	}
	logicalKey, err := corecontract.ModelLogicalOperationKey(
		run.RunID,
		run.Member.MemberID,
		input.LogicalStepID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: logical operation: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	if deadlineExpired {
		result, err := commitModelDispatchBeforeNetwork(
			ctx,
			connection,
			input,
			deadline,
			contextCompilationDigest,
			requestDigest,
			run,
			bindingCanonical,
			request.Parameters,
			config.Provider,
			config.Model,
			logicalKey,
			"",
			readyStep,
			workspaceTransfer,
			modelDeadlineExpiredBeforeDispatchClassification,
			corecontract.DispatchTransitionExpiredBeforeNetworkV1,
			overviewTransitionModelExpiredV1,
		)
		if err != nil {
			return BeginModelDispatchResult{}, err
		}
		committed = true
		return result, nil
	}
	if err := checkCurrentKnowledgeReuseActivations(
		ctx,
		connection,
		run,
		contextCompilation,
	); err != nil {
		return BeginModelDispatchResult{}, err
	}
	if err := checkCurrentActivation(
		ctx,
		connection,
		run.RunID,
		moduleapi.PortRef{
			Name:         moduleapi.PortNameModelGenerate,
			ExactVersion: moduleapi.PortVersionV2,
		},
		binding.Provider,
	); err != nil {
		return BeginModelDispatchResult{}, err
	}
	continuation, err := corecontract.NewLoopContinuationV1(
		corecontract.ModelPendingLoopStep,
		input.LogicalStepID,
		input.AttemptID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: continuation: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	createdAt := nowUnixMicro()
	if err := putWorkspaceTransferRecordV1(
		ctx,
		connection,
		workspaceTransfer,
		true,
		createdAt,
	); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: persist Workspace transfer request: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if hasContextCompilation {
		if err := putAdmissionContent(
			ctx,
			connection,
			preparedAdmissionContent{
				Digest:    contextCompilationDigest,
				Kind:      ContentContextCompilation,
				MediaType: admissionJSONMediaType,
				CanonicalBytes: bytes.Clone(
					input.ContextCompilationCanonical,
				),
			},
			createdAt,
		); err != nil {
			return BeginModelDispatchResult{}, err
		}
	}
	if err := putAdmissionContent(
		ctx,
		connection,
		preparedAdmissionContent{
			Digest:         requestDigest,
			Kind:           ContentModelRequest,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: bytes.Clone(input.RequestCanonical),
		},
		createdAt,
	); err != nil {
		return BeginModelDispatchResult{}, err
	}
	deadlineMicros := deadline.UnixMicro()
	_, err = connection.ExecContext(ctx, `
		INSERT INTO model_dispatch_attempts(
			attempt_id,
			logical_operation_key,
			run_id,
			tenant_id,
			workspace_id,
			member_id,
			logical_step_id,
			frame_revision,
			member_snapshot_digest,
			binding_json,
			context_compilation_ref,
			request_ref,
			request_digest,
			provider,
			model,
			parameters_json,
			deadline,
			usage_ledger_ref,
			state,
			provider_request_id,
			provider_receipt_ref,
			result_ref,
			error_classification,
			reconciliation_evidence_ref,
			unknown_reason,
			revision,
			created_at,
			updated_at
		) VALUES(
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			'PENDING', NULL, NULL, NULL, NULL, NULL, NULL, 0, ?, ?
		)
	`,
		input.AttemptID,
		logicalKey,
		run.RunID,
		run.Manifest.TenantID,
		run.Manifest.Workspace.ID,
		run.Member.MemberID,
		input.LogicalStepID,
		int64(input.Lease.FrameRevision),
		run.Member.MemberSnapshotDigest,
		bindingCanonical,
		nullableContextCompilationRef(
			hasContextCompilation,
			contextCompilationDigest,
		),
		requestDigest,
		requestDigest,
		config.Provider,
		config.Model,
		[]byte(request.Parameters),
		deadlineMicros,
		run.Frame.UsageLedgerRef,
		createdAt,
		createdAt,
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: insert PENDING attempt: %v",
			ErrModelDispatchConflict,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO model_usage(
			attempt_id,
			run_id,
			ledger_sequence,
			revision,
			input_tokens,
			cached_input_tokens,
			uncached_input_tokens,
			output_tokens,
			reasoning_tokens,
			usage_status,
			raw_receipt_ref
		) VALUES(
			?, ?, NULL, 0,
			NULL, NULL, NULL, NULL, NULL,
			'PENDING', NULL
		)
	`, input.AttemptID, run.RunID); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: insert Usage placeholder: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	resource, err := loadCurrentOverviewResourceV1(
		ctx, connection, overviewResourceModelV1, input.AttemptID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	_, eventCanonical, err := corecontract.NewModelDispatchEventV1(
		corecontract.ModelDispatchEventV1{
			SchemaVersion: corecontract.ModelDispatchEventSchemaVersionV1,
			RunID:         run.RunID, AttemptID: input.AttemptID,
			LogicalStepID: input.LogicalStepID, LogicalOperationKey: logicalKey,
			RequestDigest: requestDigest, State: corecontract.ModelAttemptPending,
			TransitionOrigin:       corecontract.DispatchTransitionBeginV1,
			ResourceSemanticDigest: resource.Snapshot.SemanticDigest,
		},
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf("%w: pending event: %v", ErrInvalidModelDispatch, err)
	}
	eventDigest, err := ComputeContentDigest(
		ContentRunEventPayload, admissionJSONMediaType, eventCanonical,
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf("%w: pending event content: %v", ErrInvalidModelDispatch, err)
	}
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest: eventDigest, Kind: ContentRunEventPayload, MediaType: admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(eventCanonical),
	}, createdAt); err != nil {
		return BeginModelDispatchResult{}, err
	}

	nextFrameRevision, err := incrementSQLiteUint(
		input.Lease.FrameRevision,
		"Frame revision",
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(
		run.Frame.LastAuthoritativeEvent,
		"RunEvent sequence",
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	leaseNow := nowUnixMicro()
	nextRunRevision := run.RunRevision
	if compositeCoordinator {
		nextRunRevision, err = incrementSQLiteUint(
			run.RunRevision,
			"Run revision",
		)
		if err != nil {
			return BeginModelDispatchResult{}, err
		}
		runUpdate, updateErr := connection.ExecContext(ctx, `
			UPDATE runs
			SET disposition=NULL, revision=?, updated_at=?
			WHERE run_id=?
			  AND state=?
			  AND disposition='WAITING_EXTERNAL'
			  AND revision=?
		`,
			int64(nextRunRevision),
			createdAt,
			run.RunID,
			corecontract.InitialRunState,
			int64(run.RunRevision),
		)
		if updateErr != nil {
			return BeginModelDispatchResult{}, fmt.Errorf(
				"currentstore: activate composite coordination step: %w",
				updateErr,
			)
		}
		if err := requireModelCASRow(
			runUpdate,
			"activate composite coordination step",
		); err != nil {
			return BeginModelDispatchResult{}, err
		}
	}
	update, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET
			frame_revision=?,
			step=?,
			continuation=?,
			pending_attempt_id=?,
			waiting_reason=NULL,
			last_authoritative_event=?
		WHERE run_id=?
		  AND frame_revision=?
		  AND step=?
		  AND pending_attempt_id IS NULL
		  AND last_authoritative_event=?
		  AND lease_owner=?
		  AND lease_epoch=?
		  AND lease_expiry>?
		  AND EXISTS(
		      SELECT 1
		      FROM runs
		      WHERE runs.run_id=loop_frames.run_id
		        AND runs.revision=?
		  )
	`,
		int64(nextFrameRevision),
		corecontract.ModelPendingLoopStep,
		continuation,
		input.AttemptID,
		int64(nextEvent),
		run.RunID,
		int64(input.Lease.FrameRevision),
		readyStep,
		int64(run.Frame.LastAuthoritativeEvent),
		input.Lease.OwnerID,
		int64(input.Lease.LeaseEpoch),
		leaseNow,
		int64(nextRunRevision),
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"currentstore: advance model pending Frame: %w",
			err,
		)
	}
	if err := requireModelCASRow(update, "begin model dispatch"); err != nil {
		return BeginModelDispatchResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO run_events(
			run_id,
			event_sequence,
			event_kind,
			from_revision,
			to_revision,
			payload_ref,
			payload_digest,
			created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.RunID,
		int64(nextEvent),
		corecontract.ModelDispatchPendingEventKind,
		int64(input.Lease.FrameRevision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		createdAt,
	); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: append pending RunEvent: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if err := appendModelBeginRunObservationV1(ctx, connection, run.RunID); err != nil {
		return BeginModelDispatchResult{}, err
	}
	if err := appendModelResourceObservationV1(
		ctx, connection, input.AttemptID, overviewTransitionModelBeginV1,
	); err != nil {
		return BeginModelDispatchResult{}, err
	}

	attempt, err := queryModelDispatchAttempt(
		ctx,
		connection,
		input.AttemptID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	_, modelConfigCanonical, err := moduleapi.NewModelBindingConfigV2(config)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: rebuild frozen model Binding config: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	attempt.ModelConfigCanonical = bytes.Clone(modelConfigCanonical)
	attempt.ModelAuthorityCanonical = bytes.Clone(modelAuthorityCanonical)
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"currentstore: commit BeginModelDispatch: %w",
			err,
		)
	}
	committed = true
	nextLease := input.Lease
	nextLease.RunRevision = nextRunRevision
	nextLease.FrameRevision = nextFrameRevision
	result := BeginModelDispatchResult{
		Attempt:       cloneModelDispatchAttempt(attempt),
		Lease:         nextLease,
		Created:       true,
		InvokeAllowed: true,
	}
	result.permit = newModelInvocationPermit(result.Attempt, result.Lease)
	return result, nil
}

func expectedModelParametersForRun(
	run RunForLoop,
	frozen []byte,
) ([]byte, error) {
	parameters, err := storedModelParametersForRun(run, frozen)
	if err != nil {
		return nil, err
	}
	if _, err := validateModelParametersForRun(run, parameters); err != nil {
		return nil, fmt.Errorf(
			"%w: model parameters: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	return parameters, nil
}

// storedModelParametersForRun reconstructs only the parameters that were
// frozen when an existing Attempt was created. Recovery and backup validation
// use it to preserve earlier history byte-for-byte. Every path that can create
// or redispatch an Attempt must use expectedModelParametersForRun and therefore
// applies the current explicit max_tokens and frozen output-reserve rules.
func storedModelParametersForRun(
	run RunForLoop,
	frozen []byte,
) ([]byte, error) {
	if run.Manifest.Composite == nil ||
		run.Manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1 {
		return bytes.Clone(frozen), nil
	}
	if run.CompositeRoot == nil || run.CompositeRoot.Composite == nil ||
		run.CompositeRoot.Composite.Plan == nil ||
		run.CompositeRoot.Composite.Plan.Reviewer == nil {
		return nil, fmt.Errorf(
			"%w: Reviewer parameter ceiling is absent from the frozen root plan",
			ErrModelDispatchIntegrity,
		)
	}
	plannedReviewer := run.CompositeRoot.Composite.Plan.Reviewer
	if run.Manifest.Composite.RepairRound ==
		corecontract.CompositeRepairRoundOneV1 {
		if run.CompositeRoot.Composite.Plan.Decision == nil {
			return nil, fmt.Errorf(
				"%w: repair Reviewer parameter ceiling is absent from the frozen root plan",
				ErrModelDispatchIntegrity,
			)
		}
		repairReviewer := run.CompositeRoot.Composite.Plan.Decision.RepairReviewer
		plannedReviewer = &repairReviewer
	}
	if plannedReviewer.RunID != run.RunID ||
		plannedReviewer.MemberSnapshotDigest !=
			run.Member.MemberSnapshotDigest {
		return nil, fmt.Errorf(
			"%w: Reviewer parameter ceiling differs from the frozen participant",
			ErrModelDispatchIntegrity,
		)
	}
	tightened, err := corecontract.TightenReviewerModelParametersV1(
		frozen,
		plannedReviewer.MaxOutputTokens,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: tighten Reviewer model parameters: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	return bytes.Clone(tightened), nil
}

// validateNewContextCompilationForRunAtCurrentHead is the only validation
// path allowed to consult mutable Agent Memory. It executes inside the same
// BEGIN IMMEDIATE transaction that may create PENDING, so an evidence ref that
// raced with a newer revision is rejected before any Attempt content is
// written. With no Memory Binding it delegates without a Memory Store read.
func validateNewContextCompilationForRunAtCurrentHead(
	ctx context.Context,
	queryer agentMemoryQueryer,
	record *ContentRecord,
	run RunForLoop,
	request moduleapi.ModelGenerateRequestV1,
	requestDigest string,
	attemptFrameRevision uint64,
) error {
	memoryBindings, err := frozenMemoryBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		return fmt.Errorf("frozen dynamic Memory closure: %w", err)
	}
	if len(memoryBindings) == 0 {
		return validateNewContextCompilationForRun(
			record,
			run,
			request,
			requestDigest,
			attemptFrameRevision,
		)
	}
	if record == nil {
		if run.Manifest.Composite != nil {
			return fmt.Errorf(
				"context compilation is required for a composite Run",
			)
		}
		return fmt.Errorf(
			"context compilation is required for dynamic Memory",
		)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		record.CanonicalBytes,
	)
	if err != nil {
		return fmt.Errorf("restore context-compilation/v1: %w", err)
	}
	if len(compilation.MemoryReads) != len(memoryBindings) {
		return fmt.Errorf(
			"Memory read evidence count does not match frozen dynamic Bindings",
		)
	}
	head, err := queryCurrentAgentMemoryRevision(
		ctx,
		queryer,
		run.Manifest.TenantID,
		run.Member.Agent.ID,
	)
	if err != nil {
		return fmt.Errorf("load current Agent Memory head: %w", err)
	}
	for index, binding := range memoryBindings {
		evidence := compilation.MemoryReads[index]
		if evidence.BindingIndex != binding.BindingIndex ||
			evidence.Snapshot != head.SnapshotRef {
			return fmt.Errorf(
				"Memory read %d does not freeze the current Agent Memory head",
				index,
			)
		}
	}
	content, err := queryContent(ctx, queryer, head.SnapshotRef.Digest)
	if err != nil || content.Kind != ContentMemorySnapshot ||
		content.MediaType != admissionJSONMediaType ||
		!bytes.Equal(content.CanonicalBytes, head.CanonicalBytes) {
		return fmt.Errorf("current Agent Memory head content is unavailable")
	}
	run, err = runWithAdditionalContent(run, content)
	if err != nil {
		return err
	}
	if err := validateNewContextCompilationForRun(
		record,
		run,
		request,
		requestDigest,
		attemptFrameRevision,
	); err != nil {
		return err
	}
	if len(compilation.KnowledgeReuses) == 0 {
		return nil
	}
	knowledgeBindings, err := frozenKnowledgeBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		return fmt.Errorf("frozen Knowledge reuse closure: %w", err)
	}
	nowMillis := nowUnixMicro() / 1000
	if nowMillis <= 0 {
		return fmt.Errorf("current Knowledge reuse evaluation time is invalid")
	}
	return validateKnowledgeReusesForRunAtEvaluationTime(
		compilation,
		run,
		knowledgeBindings,
		memoryBindings,
		uint64(nowMillis),
	)
}

func runWithAdditionalContent(
	run RunForLoop,
	additional ...ContentRecord,
) (RunForLoop, error) {
	byDigest := make(map[string]ContentRecord, len(run.Contents)+len(additional))
	for _, record := range append(
		append([]ContentRecord(nil), run.Contents...),
		additional...,
	) {
		if existing, found := byDigest[record.Digest]; found {
			if existing.Kind != record.Kind ||
				existing.MediaType != record.MediaType ||
				!bytes.Equal(existing.CanonicalBytes, record.CanonicalBytes) {
				return RunForLoop{}, fmt.Errorf(
					"duplicate recovery content %s differs",
					record.Digest,
				)
			}
			continue
		}
		byDigest[record.Digest] = cloneContentRecord(record)
	}
	digests := make([]string, 0, len(byDigest))
	for digest := range byDigest {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	run.Contents = make([]ContentRecord, len(digests))
	for index, digest := range digests {
		run.Contents[index] = byDigest[digest]
	}
	return run, nil
}

func exactContextCompilationInput(
	stored *ContentRecord,
	supplied bool,
	digest string,
	canonical []byte,
) bool {
	if stored == nil {
		return !supplied
	}
	return supplied &&
		stored.Digest == digest &&
		stored.Kind == ContentContextCompilation &&
		stored.MediaType == admissionJSONMediaType &&
		bytes.Equal(stored.CanonicalBytes, canonical)
}

func nullableContextCompilationRef(supplied bool, digest string) any {
	if !supplied {
		return nil
	}
	return digest
}

// validateNewContextCompilationForRun is the admission proof for a new model
// Attempt. It first validates the frozen evidence. When dynamic Knowledge is
// bound, it then runs the one pure Context Compiler over the complete immutable
// Run closure, and requires both the final request and compilation record to be
// byte-for-byte compiler outputs before anything is persisted.
func validateNewContextCompilationForRun(
	record *ContentRecord,
	run RunForLoop,
	request moduleapi.ModelGenerateRequestV1,
	requestDigest string,
	attemptFrameRevision uint64,
) error {
	if err := validateFrozenContextCompilationForRun(
		record,
		run,
		request,
		requestDigest,
		attemptFrameRevision,
	); err != nil {
		return err
	}

	var (
		compilation          corecontract.ContextCompilationV1
		compilationCanonical []byte
	)
	if record != nil {
		var err error
		compilation, err = corecontract.RestoreContextCompilationV1(
			record.CanonicalBytes,
		)
		if err != nil {
			return fmt.Errorf("restore context-compilation/v1: %w", err)
		}
		compilationCanonical = record.CanonicalBytes
	}
	knowledgeBindings, err := frozenKnowledgeBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		return fmt.Errorf("frozen dynamic context closure: %w", err)
	}
	memoryBindings, err := frozenMemoryBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		return fmt.Errorf("frozen dynamic Memory closure: %w", err)
	}
	if err := validateCompilerOutputForNewAttempt(
		compilation,
		compilationCanonical,
		run,
		request,
		attemptFrameRevision,
		knowledgeBindings,
		memoryBindings,
	); err != nil {
		return err
	}
	return nil
}

// validateFrozenContextCompilationForRun is the recovery/read proof for an
// already-persisted Attempt. It validates only frozen bytes, digests, scopes and
// evidence. In particular it never calls contextcompiler.CompileV1 and never
// re-runs a Knowledge provider, so Load/PENDING/UNKNOWN cannot become a second
// semantic execution.
func validateFrozenContextCompilationForRun(
	record *ContentRecord,
	run RunForLoop,
	request moduleapi.ModelGenerateRequestV1,
	requestDigest string,
	attemptFrameRevision uint64,
) error {
	knowledgeBindings, err := frozenKnowledgeBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		return fmt.Errorf("frozen dynamic context closure: %w", err)
	}
	memoryBindings, err := frozenMemoryBindingsForRun(
		run.Manifest,
		run.Member,
		runKnowledgeContentGetter(run),
	)
	if err != nil {
		return fmt.Errorf("frozen dynamic Memory closure: %w", err)
	}
	policy, err := frozenEffectiveContextPolicyForRun(run)
	if err != nil {
		return err
	}
	if record == nil {
		if len(knowledgeBindings) != 0 || len(memoryBindings) != 0 {
			return fmt.Errorf(
				"context compilation is required for dynamic context.provide/v1",
			)
		}
		if len(run.Member.Actions) != 0 {
			return fmt.Errorf(
				"context compilation is required for an Action result reservation",
			)
		}
		estimate, err := contextcompiler.EstimateModelGenerateRequestV1(request)
		if err != nil {
			return fmt.Errorf("estimate final model request: %w", err)
		}
		restoreWatermark, err := policy.RestoreWatermarkTokens()
		if err != nil {
			return fmt.Errorf("context policy restore watermark: %w", err)
		}
		if estimate >= restoreWatermark {
			return fmt.Errorf(
				"context compilation is required: final request estimate %d reaches restore watermark %d",
				estimate,
				restoreWatermark,
			)
		}
		return nil
	}
	if record.Kind != ContentContextCompilation ||
		record.MediaType != admissionJSONMediaType {
		return fmt.Errorf("context compilation content identity mismatch")
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		record.CanonicalBytes,
	)
	if err != nil {
		return fmt.Errorf("restore context-compilation/v1: %w", err)
	}
	if compilation.WorkspaceScope != run.Member.Workspace {
		return fmt.Errorf("context compilation Workspace scope mismatch")
	}
	if compilation.ContextPolicy != run.Member.ContextPolicy {
		return fmt.Errorf("context compilation policy mismatch")
	}
	if compilation.FinalRequestDigest != requestDigest {
		return fmt.Errorf("context compilation request digest mismatch")
	}
	inputBudget, err := policy.InputBudgetTokens()
	if err != nil {
		return fmt.Errorf("context policy input budget: %w", err)
	}
	restoreWatermark, err := policy.RestoreWatermarkTokens()
	if err != nil {
		return fmt.Errorf("context policy restore watermark: %w", err)
	}
	if compilation.InputBudgetTokens != inputBudget ||
		compilation.RestoreWatermarkTokens != restoreWatermark ||
		compilation.EstimatorVersion != policy.EstimatorVersion ||
		compilation.EstimatorVersion !=
			corecontract.ContextEstimatorCanonicalJSONUTF8ByteUpperBoundV1 ||
		compilation.SummaryAlgorithmVersion !=
			corecontract.ContextSummaryHeadTailExtractiveV1 {
		return fmt.Errorf("context compilation policy projection mismatch")
	}
	if err := validateKnowledgeRetrievalsForRun(
		compilation,
		run,
		request,
		knowledgeBindings,
	); err != nil {
		return err
	}
	if err := validateMemoryReadsForRun(
		compilation,
		run,
		request,
		memoryBindings,
	); err != nil {
		return err
	}
	if err := validateDynamicContextMessageOrder(
		compilation,
		request,
	); err != nil {
		return err
	}
	if err := validateContextCompilationEvidence(
		compilation,
		run,
		attemptFrameRevision,
		policy.RecentHistoryTurns,
	); err != nil {
		return err
	}
	if err := validateCompositeCompilationForRun(
		compilation,
		record.CanonicalBytes,
		run,
		request,
		attemptFrameRevision,
		knowledgeBindings,
		memoryBindings,
	); err != nil {
		return err
	}
	expectedActions, err := corecontract.ModelActionDefinitionsV1(
		run.Member.Actions,
	)
	if err != nil || !sameModelActionDefinitions(request.Actions, expectedActions) {
		return fmt.Errorf("model request Action projection differs from frozen member")
	}
	estimate, err := contextcompiler.EstimateModelGenerateRequestV1(request)
	if err != nil {
		return fmt.Errorf("estimate frozen final model request: %w", err)
	}
	if len(run.Member.Actions) == 0 {
		if compilation.ActionResultReservation != nil {
			return fmt.Errorf("unexpected Action result reservation")
		}
		return nil
	}
	expectedReservation, err := corecontract.NewActionResultReservationV1(
		run.Member.Actions,
	)
	if err != nil || compilation.ActionResultReservation == nil ||
		*compilation.ActionResultReservation != expectedReservation ||
		expectedReservation.EstimatedTokens > math.MaxUint64-estimate ||
		compilation.FinalEstimateTokens != estimate+expectedReservation.EstimatedTokens {
		return fmt.Errorf(
			"context compilation Action reservation or final estimate mismatch",
		)
	}
	return nil
}

func validateCompositeCompilationForRun(
	compilation corecontract.ContextCompilationV1,
	compilationCanonical []byte,
	run RunForLoop,
	request moduleapi.ModelGenerateRequestV1,
	attemptFrameRevision uint64,
	knowledgeBindings []frozenKnowledgeBinding,
	memoryBindings []frozenMemoryBinding,
) error {
	if run.Manifest.Composite == nil {
		if compilation.Composite != nil {
			return fmt.Errorf("unexpected Composite context evidence")
		}
		return nil
	}
	if compilation.Composite == nil ||
		compilation.Composite.Role != run.Manifest.Composite.Role {
		return fmt.Errorf("Composite context evidence role mismatch")
	}
	compiled, err := recompileContextForNewAttempt(
		compilation,
		run,
		attemptFrameRevision,
		knowledgeBindings,
		memoryBindings,
	)
	if err != nil {
		return fmt.Errorf("rebuild frozen Composite context: %w", err)
	}
	_, requestCanonical, err := moduleapi.NewModelGenerateRequestV1(request)
	if err != nil {
		return fmt.Errorf("rebuild Composite model request: %w", err)
	}
	if !bytes.Equal(compiled.RequestCanonical, requestCanonical) ||
		compiled.Compilation == nil ||
		!bytes.Equal(compiled.CompilationCanonical, compilationCanonical) {
		return fmt.Errorf(
			"Composite request or evidence is not the exact frozen Context Compiler output for Run %q role %q (request_equal=%t evidence_present=%t evidence_equal=%t)",
			run.RunID,
			run.Manifest.Composite.Role,
			bytes.Equal(compiled.RequestCanonical, requestCanonical),
			compiled.Compilation != nil,
			bytes.Equal(compiled.CompilationCanonical, compilationCanonical),
		)
	}
	return nil
}

func frozenEffectiveContextPolicyForRun(
	run RunForLoop,
) (corecontract.ContextPolicyV1, error) {
	policyContent, found := run.FindContent(run.Member.ContextPolicy.Digest)
	if !found ||
		policyContent.Kind != ContentPolicy ||
		policyContent.MediaType != admissionJSONMediaType {
		return corecontract.ContextPolicyV1{}, fmt.Errorf(
			"frozen context policy content is unavailable",
		)
	}
	policyDocument, err := corecontract.RestorePolicyDocument(
		policyContent.CanonicalBytes,
		run.Member.ContextPolicy,
	)
	if err != nil || policyDocument.PolicyType != corecontract.PolicyContext {
		return corecontract.ContextPolicyV1{}, fmt.Errorf(
			"restore frozen context policy document",
		)
	}
	policy, err := corecontract.RestoreContextPolicyV1(policyDocument.Body)
	if err != nil {
		return corecontract.ContextPolicyV1{}, fmt.Errorf(
			"restore context-policy/v1: %w",
			err,
		)
	}
	policy, err = effectiveContextPolicyForMember(
		run.Member,
		policy,
		runContentGetter(run),
	)
	if err != nil {
		return corecontract.ContextPolicyV1{}, fmt.Errorf(
			"apply frozen ModelProfile context ceiling: %w",
			err,
		)
	}
	return policy, nil
}

type contextCompilationUnit struct {
	digest   string
	messages []moduleapi.ModelMessageV1
}

func validateContextCompilationEvidence(
	compilation corecontract.ContextCompilationV1,
	run RunForLoop,
	attemptFrameRevision uint64,
	recentHistoryTurns uint64,
) error {
	attemptFrames := make(map[string]uint64, len(run.ModelDispatches))
	for _, dispatch := range run.ModelDispatches {
		attemptFrames[dispatch.Attempt.AttemptID] =
			dispatch.Attempt.FrameRevision
	}
	historyUnits, err := contextCompilationHistoryUnits(
		run,
		attemptFrames,
		attemptFrameRevision,
	)
	if err != nil {
		return err
	}
	protected := int(recentHistoryTurns)
	if protected > len(historyUnits) {
		protected = len(historyUnits)
	}
	deletableHistory := historyUnits[:len(historyUnits)-protected]

	if compilation.Summary != nil {
		sources := compilation.Summary.SourceTurnDigests
		if !contextCompilationHistoryPrefix(deletableHistory, sources) {
			return fmt.Errorf(
				"context summary is not the oldest deletable History prefix",
			)
		}
		messages := make([]moduleapi.ModelMessageV1, 0, len(sources)*2)
		for index := range sources {
			messages = append(messages, deletableHistory[index].messages...)
		}
		expectedText, err := corecontract.ContextHeadTailSummaryV1(messages)
		if err != nil {
			return fmt.Errorf("rebuild deterministic context summary: %w", err)
		}
		if compilation.Summary.Text != expectedText {
			return fmt.Errorf("context summary text does not match frozen History")
		}
	}

	if compilation.Summary != nil && len(compilation.Drops) != 0 {
		return fmt.Errorf("context compilation mixes Summary and Drop evidence")
	}
	if len(compilation.Drops) > len(deletableHistory) {
		return fmt.Errorf("context Drop exceeds deletable History")
	}
	for index, drop := range compilation.Drops {
		if drop.UnitKind != corecontract.ContextCompilationUnitHistoryTurn {
			return fmt.Errorf("only complete History turns may be dropped")
		}
		if drop.UnitDigest != deletableHistory[index].digest {
			return fmt.Errorf(
				"context Drop is not the oldest deletable History prefix",
			)
		}
	}
	return nil
}

func contextCompilationHistoryUnits(
	run RunForLoop,
	attemptFrames map[string]uint64,
	attemptFrameRevision uint64,
) ([]contextCompilationUnit, error) {
	if run.Manifest.ConversationTurn != nil {
		// A persisted successful current-turn Attempt legitimately gains one
		// local History result after its ContextCompilation was frozen. That
		// output was not predecessor evidence and is therefore ignored here;
		// the only admissible evidence units remain the separately restored,
		// exact prior Conversation turns below. The pre-dispatch compiler path
		// still rejects local History in knowledgeCompilerHistoryForRun.
		units := make(
			[]contextCompilationUnit,
			0,
			len(run.ConversationHistory),
		)
		for index, entry := range run.ConversationHistory {
			if entry.TurnIndex != uint64(index+1) {
				return nil, fmt.Errorf(
					"Conversation History turn sequence is not contiguous",
				)
			}
			digest, err := corecontract.ContextConversationTurnDigestV1(
				entry.TurnIndex,
				entry.UserContent.Digest,
				entry.AssistantContent.Digest,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"derive frozen Conversation turn identity: %w",
					err,
				)
			}
			user, err := corecontract.RestoreTaskInputV1(
				entry.UserContent.CanonicalBytes,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"restore frozen Conversation USER: %w",
					err,
				)
			}
			assistant, err := moduleapi.RestoreModelGenerateOutputV1(
				entry.AssistantContent.CanonicalBytes,
			)
			if err != nil || assistant.ActionRequest != nil ||
				assistant.AssistantText == "" {
				return nil, fmt.Errorf(
					"restore frozen Conversation ASSISTANT: %w",
					err,
				)
			}
			units = append(units, contextCompilationUnit{
				digest: digest,
				messages: []moduleapi.ModelMessageV1{
					{Role: moduleapi.ModelRoleUser, Content: user.Text},
					{Role: moduleapi.ModelRoleAssistant, Content: assistant.AssistantText},
				},
			})
		}
		return units, nil
	}
	if len(run.ConversationHistory) != 0 {
		return nil, fmt.Errorf(
			"non-Conversation Run contains Conversation History evidence",
		)
	}
	units := make([]contextCompilationUnit, 0, len(run.History))
	for _, entry := range run.History {
		frameRevision, found := attemptFrames[entry.SourceAttemptID]
		if !found {
			return nil, fmt.Errorf("frozen History source Attempt is unavailable")
		}
		if frameRevision >= attemptFrameRevision {
			continue
		}
		turnDigest, err := corecontract.ContextHistoryTurnDigestV1(
			entry.Sequence,
			entry.Content.Digest,
		)
		if err != nil {
			return nil, fmt.Errorf("derive frozen History turn identity: %w", err)
		}
		output, err := moduleapi.RestoreModelGenerateOutputV1(
			entry.Content.CanonicalBytes,
		)
		if err != nil {
			return nil, fmt.Errorf("restore frozen History result: %w", err)
		}
		units = append(units, contextCompilationUnit{
			digest: turnDigest,
			messages: []moduleapi.ModelMessageV1{{
				Role:    moduleapi.ModelRoleAssistant,
				Content: output.AssistantText,
			}},
		})
	}
	return units, nil
}

func contextCompilationHistoryPrefix(
	history []contextCompilationUnit,
	sources []string,
) bool {
	if len(sources) == 0 || len(sources) > len(history) {
		return false
	}
	for index, digest := range sources {
		if history[index].digest != digest {
			return false
		}
	}
	return true
}

// commitModelDispatchBeforeNetwork closes a semantic model step before an
// adapter may run because its normalized deadline expired. The failed
// Attempt, empty Usage, terminal Run/Frame, and terminal event are one
// transaction, so no PENDING recovery window or invocation permit exists.
func commitModelDispatchBeforeNetwork(
	ctx context.Context,
	connection *sql.Conn,
	input BeginModelDispatchInput,
	deadline time.Time,
	contextCompilationDigest string,
	requestDigest string,
	run RunForLoop,
	bindingCanonical []byte,
	requestParameters []byte,
	provider string,
	model string,
	logicalKey string,
	sourceDispatchAttemptID string,
	expectedFrameStep string,
	workspaceTransfer *WorkspaceTransferRecordV1,
	errorClassification string,
	transitionOrigin corecontract.DispatchTransitionOriginV1,
	resourceTransition string,
) (BeginModelDispatchResult, error) {
	continuation, err := corecontract.NewLoopContinuationV1(
		corecontract.TerminatedLoopStep,
		input.LogicalStepID,
		input.AttemptID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: expired deadline continuation: %v",
			ErrInvalidModelDispatch,
			err,
		)
	}
	nextRunRevision, err := incrementSQLiteUint(
		run.RunRevision,
		"Run revision",
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	nextFrameRevision, err := incrementSQLiteUint(
		run.Frame.Revision,
		"Frame revision",
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	nextEvent, err := incrementSQLiteUint(
		run.Frame.LastAuthoritativeEvent,
		"RunEvent sequence",
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}

	createdAt := nowUnixMicro()
	if err := putWorkspaceTransferRecordV1(
		ctx,
		connection,
		workspaceTransfer,
		true,
		createdAt,
	); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: persist expired Workspace transfer request: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	expectedDisposition := ""
	expectedWaitingReason := ""
	if expectedFrameStep == corecontract.WaitingChildrenLoopStep {
		expectedDisposition = "WAITING_EXTERNAL"
		expectedWaitingReason = compositeChildrenPendingWaitingReason
	}
	contents := make([]preparedAdmissionContent, 0, 2)
	if input.ContextCompilationCanonical != nil {
		contents = append(contents, preparedAdmissionContent{
			Digest:    contextCompilationDigest,
			Kind:      ContentContextCompilation,
			MediaType: admissionJSONMediaType,
			CanonicalBytes: bytes.Clone(
				input.ContextCompilationCanonical,
			),
		})
	}
	contents = append(contents, preparedAdmissionContent{
		Digest:         requestDigest,
		Kind:           ContentModelRequest,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(input.RequestCanonical),
	})
	for _, content := range contents {
		if err := putAdmissionContent(
			ctx,
			connection,
			content,
			createdAt,
		); err != nil {
			return BeginModelDispatchResult{}, err
		}
	}

	if _, err := connection.ExecContext(ctx, `
		INSERT INTO model_dispatch_attempts(
			attempt_id,
			logical_operation_key,
			run_id,
			tenant_id,
			workspace_id,
			member_id,
			logical_step_id,
			frame_revision,
			member_snapshot_digest,
			binding_json,
			context_compilation_ref,
			request_ref,
			request_digest,
			provider,
			model,
			parameters_json,
			deadline,
			usage_ledger_ref,
			source_dispatch_attempt_id,
			state,
			provider_request_id,
			provider_receipt_ref,
			result_ref,
			error_classification,
			reconciliation_evidence_ref,
			unknown_reason,
			revision,
			created_at,
			updated_at
		) VALUES(
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			'FAILED', NULL, NULL, NULL, ?, NULL, NULL, 0, ?, ?
		)
	`,
		input.AttemptID,
		logicalKey,
		run.RunID,
		run.Manifest.TenantID,
		run.Manifest.Workspace.ID,
		run.Member.MemberID,
		input.LogicalStepID,
		int64(run.Frame.Revision),
		run.Member.MemberSnapshotDigest,
		bytes.Clone(bindingCanonical),
		nullableContextCompilationRef(
			input.ContextCompilationCanonical != nil,
			contextCompilationDigest,
		),
		requestDigest,
		requestDigest,
		provider,
		model,
		bytes.Clone(requestParameters),
		deadline.UnixMicro(),
		run.Frame.UsageLedgerRef,
		nullableModelString(sourceDispatchAttemptID),
		errorClassification,
		createdAt,
		createdAt,
	); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: insert expired deadline Attempt: %v",
			ErrModelDispatchConflict,
			err,
		)
	}
	if _, err := connection.ExecContext(ctx, `
		INSERT INTO model_usage(
			attempt_id,
			run_id,
			ledger_sequence,
			revision,
			input_tokens,
			cached_input_tokens,
			uncached_input_tokens,
			output_tokens,
			reasoning_tokens,
			usage_status,
			raw_receipt_ref
		) VALUES(
			?, ?, NULL, 0,
			NULL, NULL, NULL, NULL, NULL,
			?, NULL
		)
	`,
		input.AttemptID,
		run.RunID,
		modelUsageStatusNoReport,
	); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: insert expired deadline Usage: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	resource, err := loadCurrentOverviewResourceV1(
		ctx, connection, overviewResourceModelV1, input.AttemptID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	usageEvent, err := modelUsageEventV1(ModelUsageRecord{
		AttemptID: input.AttemptID, RunID: run.RunID,
		UsageStatus: modelUsageStatusNoReport,
	})
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	_, eventCanonical, err := corecontract.NewModelDispatchEventV1(corecontract.ModelDispatchEventV1{
		SchemaVersion: corecontract.ModelDispatchEventSchemaVersionV1,
		RunID:         run.RunID, AttemptID: input.AttemptID, LogicalStepID: input.LogicalStepID,
		LogicalOperationKey: logicalKey, RequestDigest: requestDigest,
		State:            corecontract.ModelAttemptFailed,
		TransitionOrigin: transitionOrigin,
		Usage:            usageEvent, ResourceSemanticDigest: resource.Snapshot.SemanticDigest,
	})
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf("%w: expired deadline event: %v", ErrInvalidModelDispatch, err)
	}
	eventDigest, err := ComputeContentDigest(ContentRunEventPayload, admissionJSONMediaType, eventCanonical)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf("%w: expired deadline event content: %v", ErrInvalidModelDispatch, err)
	}
	if err := putAdmissionContent(ctx, connection, preparedAdmissionContent{
		Digest: eventDigest, Kind: ContentRunEventPayload, MediaType: admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(eventCanonical),
	}, createdAt); err != nil {
		return BeginModelDispatchResult{}, err
	}

	runUpdate, err := connection.ExecContext(ctx, `
		UPDATE runs
		SET
			state=?,
			disposition=?,
			revision=?,
			updated_at=?
		WHERE run_id=?
		  AND state=?
		  AND COALESCE(disposition, '')=?
		  AND revision=?
	`,
		corecontract.TerminatedLoopStep,
		corecontract.TerminatedLoopStep,
		int64(nextRunRevision),
		createdAt,
		run.RunID,
		corecontract.InitialRunState,
		expectedDisposition,
		int64(run.RunRevision),
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"currentstore: terminate expired deadline Run: %w",
			err,
		)
	}
	if err := requireModelCASRow(
		runUpdate,
		"terminate expired deadline Run",
	); err != nil {
		return BeginModelDispatchResult{}, err
	}

	frameUpdate, err := connection.ExecContext(ctx, `
		UPDATE loop_frames
		SET
			frame_revision=?,
			step=?,
			continuation=?,
			pending_attempt_id=NULL,
			pending_dispatch_attempt_id=NULL,
			waiting_reason=NULL,
			last_authoritative_event=?
		WHERE run_id=?
		  AND frame_revision=?
		  AND step=?
		  AND pending_attempt_id IS NULL
		  AND pending_dispatch_attempt_id IS NULL
		  AND COALESCE(waiting_reason, '')=?
		  AND last_authoritative_event=?
		  AND lease_owner=?
		  AND lease_epoch=?
		  AND lease_expiry>?
		  AND EXISTS(
		      SELECT 1
		      FROM runs
		      WHERE runs.run_id=loop_frames.run_id
		        AND runs.revision=?
		  )
	`,
		int64(nextFrameRevision),
		corecontract.TerminatedLoopStep,
		continuation,
		int64(nextEvent),
		run.RunID,
		int64(run.Frame.Revision),
		expectedFrameStep,
		expectedWaitingReason,
		int64(run.Frame.LastAuthoritativeEvent),
		input.Lease.OwnerID,
		int64(input.Lease.LeaseEpoch),
		createdAt,
		int64(nextRunRevision),
	)
	if err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"currentstore: terminate expired deadline Frame: %w",
			err,
		)
	}
	if err := requireModelCASRow(
		frameUpdate,
		"terminate expired deadline Frame",
	); err != nil {
		return BeginModelDispatchResult{}, err
	}

	if _, err := connection.ExecContext(ctx, `
		INSERT INTO run_events(
			run_id,
			event_sequence,
			event_kind,
			from_revision,
			to_revision,
			payload_ref,
			payload_digest,
			created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.RunID,
		int64(nextEvent),
		corecontract.ModelDispatchTerminalEventKind,
		int64(run.Frame.Revision),
		int64(nextFrameRevision),
		eventDigest,
		eventDigest,
		createdAt,
	); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"%w: append expired deadline terminal RunEvent: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	if err := appendModelOutcomeRunObservationV1(ctx, connection, run.RunID); err != nil {
		return BeginModelDispatchResult{}, err
	}
	if err := appendModelResourceObservationV1(
		ctx, connection, input.AttemptID, resourceTransition,
	); err != nil {
		return BeginModelDispatchResult{}, err
	}

	stored, err := queryModelDispatchRecord(
		ctx,
		connection,
		input.AttemptID,
	)
	if err != nil {
		return BeginModelDispatchResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return BeginModelDispatchResult{}, fmt.Errorf(
			"currentstore: commit expired deadline BeginModelDispatch: %w",
			err,
		)
	}
	nextLease := input.Lease
	nextLease.RunRevision = nextRunRevision
	nextLease.FrameRevision = nextFrameRevision
	return BeginModelDispatchResult{
		Attempt:       cloneModelDispatchAttempt(stored.Attempt),
		Lease:         nextLease,
		Created:       true,
		InvokeAllowed: false,
	}, nil
}

func newModelInvocationPermit(
	attempt ModelDispatchAttemptRecord,
	lease RunLease,
) *modelInvocationPermit {
	return &modelInvocationPermit{closure: modelInvocationPermitClosure{
		attemptID:               attempt.AttemptID,
		runID:                   attempt.RunID,
		memberID:                attempt.MemberID,
		memberDigest:            attempt.MemberSnapshotDigest,
		attemptFrameRevision:    attempt.FrameRevision,
		bindingCanonical:        bytes.Clone(attempt.BindingCanonical),
		modelConfigCanonical:    bytes.Clone(attempt.ModelConfigCanonical),
		modelAuthorityCanonical: bytes.Clone(attempt.ModelAuthorityCanonical),
		contextCompilationDigest: contextCompilationDigest(
			attempt.ContextCompilation,
		),
		contextCompilationKind: contextCompilationKind(
			attempt.ContextCompilation,
		),
		contextCompilationMediaType: contextCompilationMediaType(
			attempt.ContextCompilation,
		),
		contextCompilationCanonical: contextCompilationCanonical(
			attempt.ContextCompilation,
		),
		requestDigest:    attempt.Request.Digest,
		requestKind:      attempt.Request.Kind,
		requestMediaType: attempt.Request.MediaType,
		requestCanonical: bytes.Clone(attempt.Request.CanonicalBytes),
		deadline:         attempt.Deadline,
		lease:            lease,
	}}
}

func (permit *modelInvocationPermit) matches(
	attempt ModelDispatchAttemptRecord,
	lease RunLease,
) bool {
	if permit == nil {
		return false
	}
	closure := permit.closure
	return attempt.AttemptID == closure.attemptID &&
		attempt.RunID == closure.runID &&
		attempt.MemberID == closure.memberID &&
		attempt.MemberSnapshotDigest == closure.memberDigest &&
		attempt.FrameRevision == closure.attemptFrameRevision &&
		bytes.Equal(attempt.BindingCanonical, closure.bindingCanonical) &&
		bytes.Equal(
			attempt.ModelConfigCanonical,
			closure.modelConfigCanonical,
		) &&
		bytes.Equal(
			attempt.ModelAuthorityCanonical,
			closure.modelAuthorityCanonical,
		) &&
		contextCompilationDigest(attempt.ContextCompilation) ==
			closure.contextCompilationDigest &&
		contextCompilationKind(attempt.ContextCompilation) ==
			closure.contextCompilationKind &&
		contextCompilationMediaType(attempt.ContextCompilation) ==
			closure.contextCompilationMediaType &&
		bytes.Equal(
			contextCompilationCanonical(attempt.ContextCompilation),
			closure.contextCompilationCanonical,
		) &&
		attempt.Request.Digest == closure.requestDigest &&
		attempt.Request.Kind == closure.requestKind &&
		attempt.Request.MediaType == closure.requestMediaType &&
		bytes.Equal(
			attempt.Request.CanonicalBytes,
			closure.requestCanonical,
		) &&
		attempt.Deadline.Equal(closure.deadline) &&
		lease == closure.lease
}

func contextCompilationDigest(record *ContentRecord) string {
	if record == nil {
		return ""
	}
	return record.Digest
}

func contextCompilationKind(record *ContentRecord) ContentKind {
	if record == nil {
		return ""
	}
	return record.Kind
}

func contextCompilationMediaType(record *ContentRecord) string {
	if record == nil {
		return ""
	}
	return record.MediaType
}

func contextCompilationCanonical(record *ContentRecord) []byte {
	if record == nil {
		return nil
	}
	return record.CanonicalBytes
}

func loadExactRetryRun(
	ctx context.Context,
	connection *sql.Conn,
	original RunLease,
	existing ModelDispatchAttemptRecord,
) (RunLease, RunForLoop, error) {
	current, err := loadStoredRunLease(
		ctx,
		connection,
		original.RunID,
	)
	if err != nil {
		return RunLease{}, RunForLoop{}, err
	}
	observedAt := nowUnixMicro()
	sameRunRevision := uint64(current.runRevision) == original.RunRevision
	preNetworkTerminalOneBehind :=
		existing.State == corecontract.ModelAttemptFailed &&
			existing.ErrorClassification ==
				modelDeadlineExpiredBeforeDispatchClassification &&
			existing.FrameRevision == original.FrameRevision &&
			original.RunRevision < math.MaxInt64 &&
			original.FrameRevision < math.MaxInt64 &&
			uint64(current.runRevision) == original.RunRevision+1 &&
			uint64(current.frameRevision) == original.FrameRevision+1
	if !current.owner.Valid ||
		current.owner.String != original.OwnerID ||
		current.epoch <= 0 ||
		uint64(current.epoch) != original.LeaseEpoch ||
		(!sameRunRevision && !preNetworkTerminalOneBehind) ||
		!current.expiry.Valid ||
		current.expiry.Int64 <= observedAt {
		return RunLease{}, RunForLoop{}, fmt.Errorf(
			"%w: exact retry no longer owns the current lease",
			ErrRunLeaseConflict,
		)
	}
	lease, err := newRunLease(
		original.RunID,
		original.OwnerID,
		current.epoch,
		current.runRevision,
		current.frameRevision,
		current.expiry.Int64,
	)
	if err != nil {
		return RunLease{}, RunForLoop{}, err
	}
	run, err := loadRunForLoop(ctx, connection, lease)
	if err != nil {
		return RunLease{}, RunForLoop{}, err
	}
	return lease, run, nil
}

func verifyBeginPendingUsagePlaceholder(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	attempt ModelDispatchAttemptRecord,
) error {
	var (
		runID               string
		ledgerSequence      sql.NullInt64
		revision            int64
		inputTokens         sql.NullInt64
		cachedInputTokens   sql.NullInt64
		uncachedInputTokens sql.NullInt64
		outputTokens        sql.NullInt64
		reasoningTokens     sql.NullInt64
		status              string
		rawReceipt          sql.NullString
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			run_id,
			ledger_sequence,
			revision,
			input_tokens,
			cached_input_tokens,
			uncached_input_tokens,
			output_tokens,
			reasoning_tokens,
			usage_status,
			raw_receipt_ref
		FROM model_usage
		WHERE attempt_id=?
	`, attempt.AttemptID).Scan(
		&runID,
		&ledgerSequence,
		&revision,
		&inputTokens,
		&cachedInputTokens,
		&uncachedInputTokens,
		&outputTokens,
		&reasoningTokens,
		&status,
		&rawReceipt,
	)
	if err != nil ||
		runID != attempt.RunID ||
		revision != 0 ||
		ledgerSequence.Valid ||
		inputTokens.Valid ||
		cachedInputTokens.Valid ||
		uncachedInputTokens.Valid ||
		outputTokens.Valid ||
		reasoningTokens.Valid ||
		status != "PENDING" ||
		rawReceipt.Valid {
		return fmt.Errorf(
			"%w: PENDING Attempt %q lacks its exact UNKNOWN Usage placeholder",
			ErrModelDispatchIntegrity,
			attempt.AttemptID,
		)
	}
	return nil
}

func findModelAttemptByStep(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
	logicalStepID string,
) (string, bool, error) {
	var attemptID string
	err := queryer.QueryRowContext(ctx, `
		SELECT attempt_id
		FROM model_dispatch_attempts
		WHERE run_id=? AND logical_step_id=?
	`, runID, logicalStepID).Scan(&attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf(
			"currentstore: find model attempt by step: %w",
			err,
		)
	}
	return attemptID, true, nil
}

func queryModelDispatchAttempt(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	attemptID string,
) (ModelDispatchAttemptRecord, error) {
	var (
		record                 ModelDispatchAttemptRecord
		frameRevision          int64
		bindingCanonical       []byte
		contextCompilationRef  sql.NullString
		requestRef             string
		requestDigest          string
		parametersCanonical    []byte
		deadlineMicros         int64
		usageLedgerRef         string
		state                  string
		providerRequestID      sql.NullString
		providerReceiptRef     sql.NullString
		resultRef              sql.NullString
		errorClassification    sql.NullString
		reconciliationEvidence sql.NullString
		unknownReason          sql.NullString
		sourceDispatchAttempt  sql.NullString
		revision               int64
		createdAtMicros        int64
		updatedAtMicros        int64
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT
			attempt_id,
			logical_operation_key,
			run_id,
			tenant_id,
			workspace_id,
			member_id,
			logical_step_id,
			frame_revision,
			member_snapshot_digest,
			binding_json,
			context_compilation_ref,
			request_ref,
			request_digest,
			provider,
			model,
			parameters_json,
			deadline,
			usage_ledger_ref,
			source_dispatch_attempt_id,
			state,
			provider_request_id,
			provider_receipt_ref,
			result_ref,
			error_classification,
			reconciliation_evidence_ref,
			unknown_reason,
			revision,
			created_at,
			updated_at
		FROM model_dispatch_attempts
		WHERE attempt_id=?
	`, attemptID).Scan(
		&record.AttemptID,
		&record.LogicalOperationKey,
		&record.RunID,
		&record.TenantID,
		&record.WorkspaceID,
		&record.MemberID,
		&record.LogicalStepID,
		&frameRevision,
		&record.MemberSnapshotDigest,
		&bindingCanonical,
		&contextCompilationRef,
		&requestRef,
		&requestDigest,
		&record.Provider,
		&record.Model,
		&parametersCanonical,
		&deadlineMicros,
		&usageLedgerRef,
		&sourceDispatchAttempt,
		&state,
		&providerRequestID,
		&providerReceiptRef,
		&resultRef,
		&errorClassification,
		&reconciliationEvidence,
		&unknownReason,
		&revision,
		&createdAtMicros,
		&updatedAtMicros,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ModelDispatchAttemptRecord{}, fmt.Errorf(
			"%w: Attempt %q",
			ErrModelRecordNotFound,
			attemptID,
		)
	}
	if err != nil {
		return ModelDispatchAttemptRecord{}, fmt.Errorf(
			"currentstore: query model attempt: %w",
			err,
		)
	}
	if frameRevision < 0 || revision < 0 || !validLeaseOpaqueID(record.TenantID) ||
		!validLeaseOpaqueID(record.WorkspaceID) {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"negative revision",
		)
	}
	expectedKey, err := corecontract.ModelLogicalOperationKey(
		record.RunID,
		record.MemberID,
		record.LogicalStepID,
	)
	if err != nil || expectedKey != record.LogicalOperationKey {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"logical operation key",
		)
	}
	binding, err := restoreModelBinding(bindingCanonical)
	if err != nil {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"binding",
		)
	}
	if contextCompilationRef.Valid {
		contextCompilation, err := queryContent(
			ctx,
			queryer,
			contextCompilationRef.String,
		)
		if err != nil ||
			contextCompilation.Kind != ContentContextCompilation ||
			contextCompilation.MediaType != admissionJSONMediaType {
			return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
				attemptID,
				"context compilation content",
			)
		}
		record.ContextCompilation = &contextCompilation
	}
	requestContent, err := queryContent(ctx, queryer, requestRef)
	if err != nil ||
		requestRef != requestDigest ||
		requestContent.Kind != ContentModelRequest ||
		requestContent.Digest != requestDigest {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"request content",
		)
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		requestContent.CanonicalBytes,
	)
	if err != nil ||
		!bytes.Equal(request.Parameters, parametersCanonical) {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"request parameters",
		)
	}
	if record.ContextCompilation != nil {
		compilation, err := corecontract.RestoreContextCompilationV1(
			record.ContextCompilation.CanonicalBytes,
		)
		if err != nil || compilation.FinalRequestDigest != requestDigest {
			return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
				attemptID,
				"context compilation closure",
			)
		}
	}
	if err := requireCanonicalJSONObject(parametersCanonical); err != nil {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"parameters JSON",
		)
	}
	if _, err := corecontract.ParseUsageLedgerRefV1(
		usageLedgerRef,
		record.RunID,
	); err != nil {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"usage ledger reference",
		)
	}
	deadline, err := timeFromUnixMicro(deadlineMicros)
	if err != nil {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"deadline",
		)
	}
	createdAt, err := timeFromUnixMicro(createdAtMicros)
	if err != nil {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"created_at",
		)
	}
	updatedAt, err := timeFromUnixMicro(updatedAtMicros)
	if err != nil || updatedAt.Before(createdAt) {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"updated_at",
		)
	}
	attemptState := corecontract.ModelAttemptState(state)
	if err := attemptState.Validate(); err != nil {
		return ModelDispatchAttemptRecord{}, modelAttemptIntegrity(
			attemptID,
			"state",
		)
	}

	record.FrameRevision = uint64(frameRevision)
	record.Binding = binding
	record.BindingCanonical = bytes.Clone(bindingCanonical)
	if record.ContextCompilation != nil {
		contextCompilation := cloneContentRecord(
			*record.ContextCompilation,
		)
		record.ContextCompilation = &contextCompilation
	}
	record.Request = cloneContentRecord(requestContent)
	record.ParametersCanonical = bytes.Clone(parametersCanonical)
	record.Deadline = deadline
	record.UsageLedgerRef = usageLedgerRef
	record.SourceDispatchAttemptID = sourceDispatchAttempt.String
	record.State = attemptState
	record.ProviderRequestID = providerRequestID.String
	record.ProviderReceiptRef = providerReceiptRef.String
	record.ResultRef = resultRef.String
	record.ErrorClassification = errorClassification.String
	record.ReconciliationEvidenceRef =
		reconciliationEvidence.String
	record.UnknownReason = unknownReason.String
	record.Revision = uint64(revision)
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return record, nil
}

func exactModelBinding(
	member corecontract.MemberExecutionSnapshot,
) (moduleapi.PortBinding, error) {
	for _, plan := range member.PortPlans {
		if plan.Port.Name == moduleapi.PortNameModelGenerate &&
			plan.Port.ExactVersion == moduleapi.PortVersionV2 {
			if len(plan.Bindings) != 1 {
				return moduleapi.PortBinding{}, fmt.Errorf(
					"%w: model.generate/v2 does not have one binding",
					ErrModelDispatchIntegrity,
				)
			}
			return plan.Bindings[0], nil
		}
	}
	return moduleapi.PortBinding{}, fmt.Errorf(
		"%w: frozen member lacks model.generate/v2",
		ErrModelDispatchIntegrity,
	)
}

func frozenModelBindingConfig(
	run RunForLoop,
	binding moduleapi.PortBinding,
) (moduleapi.ModelBindingConfigV2, error) {
	record, found := run.FindContent(binding.ConfigRef)
	if !found ||
		record.Kind != ContentConfig ||
		record.MediaType != admissionJSONMediaType {
		return moduleapi.ModelBindingConfigV2{}, fmt.Errorf(
			"%w: model Binding ConfigRef is unavailable",
			ErrModelDispatchIntegrity,
		)
	}
	config, err := moduleapi.RestoreModelBindingConfigV2(
		record.CanonicalBytes,
	)
	if err != nil {
		return moduleapi.ModelBindingConfigV2{}, fmt.Errorf(
			"%w: model Binding ConfigRef is not model-binding-config/v2",
			ErrModelDispatchIntegrity,
		)
	}
	return config, nil
}

func frozenModelBindingAuthority(
	run RunForLoop,
	binding moduleapi.PortBinding,
) ([]byte, error) {
	record, found := run.FindContent(binding.AuthorityCeilingRef)
	if !found || record.Kind != ContentAuthorityCeiling ||
		record.MediaType != admissionJSONMediaType {
		return nil, fmt.Errorf(
			"%w: model Binding AuthorityCeilingRef is unavailable",
			ErrModelDispatchIntegrity,
		)
	}
	digest, err := ComputeContentDigest(
		ContentAuthorityCeiling,
		record.MediaType,
		record.CanonicalBytes,
	)
	if err != nil || digest != binding.AuthorityCeilingRef {
		return nil, fmt.Errorf(
			"%w: model Binding AuthorityCeilingRef does not close its bytes",
			ErrModelDispatchIntegrity,
		)
	}
	return bytes.Clone(record.CanonicalBytes), nil
}

func canonicalModelBinding(
	binding moduleapi.PortBinding,
) ([]byte, error) {
	if err := binding.Validate(); err != nil {
		return nil, fmt.Errorf(
			"%w: model binding: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	encoded, err := json.Marshal(binding)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: marshal model binding: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: canonicalize model binding: %v",
			ErrModelDispatchIntegrity,
			err,
		)
	}
	return canonical, nil
}

func restoreModelBinding(
	canonical []byte,
) (moduleapi.PortBinding, error) {
	checked, err := moduleapi.CanonicalJSON(canonical)
	if err != nil || !bytes.Equal(checked, canonical) {
		return moduleapi.PortBinding{}, fmt.Errorf(
			"binding is not canonical",
		)
	}
	var binding moduleapi.PortBinding
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&binding); err != nil {
		return moduleapi.PortBinding{}, err
	}
	rebuilt, err := canonicalModelBinding(binding)
	if err != nil || !bytes.Equal(rebuilt, canonical) {
		return moduleapi.PortBinding{}, fmt.Errorf(
			"binding is not frozen",
		)
	}
	return binding, nil
}

func requireCanonicalJSONObject(canonical []byte) error {
	checked, err := moduleapi.CanonicalJSON(canonical)
	if err != nil ||
		!bytes.Equal(checked, canonical) ||
		len(canonical) == 0 ||
		canonical[0] != '{' {
		return fmt.Errorf("not a canonical JSON object")
	}
	return nil
}

func normalizeModelDeadline(deadline time.Time) (time.Time, error) {
	if deadline.IsZero() ||
		deadline.Location() != time.UTC ||
		deadline.Nanosecond()%int(time.Microsecond) != 0 {
		return time.Time{}, fmt.Errorf(
			"%w: deadline must be UTC at microsecond precision",
			ErrInvalidModelDispatch,
		)
	}
	deadline = deadline.Round(0)
	return deadline, nil
}

func incrementSQLiteUint(value uint64, field string) (uint64, error) {
	if value >= math.MaxInt64 {
		return 0, fmt.Errorf(
			"%w: %s cannot advance",
			ErrModelDispatchIntegrity,
			field,
		)
	}
	return value + 1, nil
}

func requireModelCASRow(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect %s CAS: %w",
			operation,
			err,
		)
	}
	if affected == 0 {
		return fmt.Errorf(
			"%w: %s lost lease or revision CAS",
			ErrModelDispatchConflict,
			operation,
		)
	}
	if affected != 1 {
		return fmt.Errorf(
			"%w: %s affected %d rows",
			ErrModelDispatchIntegrity,
			operation,
			affected,
		)
	}
	return nil
}

func modelAttemptIntegrity(attemptID string, subject string) error {
	return fmt.Errorf(
		"%w: Attempt %q has invalid %s",
		ErrModelDispatchIntegrity,
		attemptID,
		subject,
	)
}

func cloneModelDispatchAttempt(
	record ModelDispatchAttemptRecord,
) ModelDispatchAttemptRecord {
	record.Binding.StaticContextRefs = append(
		[]string{},
		record.Binding.StaticContextRefs...,
	)
	record.BindingCanonical = bytes.Clone(record.BindingCanonical)
	record.ModelConfigCanonical = bytes.Clone(record.ModelConfigCanonical)
	record.ModelAuthorityCanonical = bytes.Clone(record.ModelAuthorityCanonical)
	if record.ContextCompilation != nil {
		contextCompilation := cloneContentRecord(
			*record.ContextCompilation,
		)
		record.ContextCompilation = &contextCompilation
	}
	record.Request = cloneContentRecord(record.Request)
	record.ParametersCanonical = bytes.Clone(record.ParametersCanonical)
	return record
}
