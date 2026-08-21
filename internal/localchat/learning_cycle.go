package localchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/learningcontract"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	learningCycleLoopMaxSteps        = uint32(1)
	learningCycleLoopMaxDuration     = 2 * time.Minute
	learningCycleFinalizeMaxDuration = 5 * time.Second
)

var (
	// ErrInvalidLearningCycle identifies an invalid explicit Tick request or
	// an uninitialized optional Learning-cycle composition.
	ErrInvalidLearningCycle = errors.New(
		"localchat: invalid Learning cycle request",
	)

	// ErrLearningCycleIntegrity identifies a Store, compiler, or Loop result
	// that does not close to the exact logical task selected by Tick.
	ErrLearningCycleIntegrity = errors.New(
		"localchat: Learning cycle integrity violation",
	)
)

// LearningCycleTickInput selects exactly one Schedule. ObservedAt is an
// explicit clock observation, not a timer grant; calling Tick is the only way
// this optional application composition can create or advance a cycle task.
type LearningCycleTickInput struct {
	TenantID   string
	ScheduleID string
	ObservedAt time.Time
}

// LearningCycleTickResult reports the exact Store receipts reached by one
// bounded Tick. LoopResult is nil when no Run was advanced. Terminal state is
// always copied from FinalizeLearningCycleTask; this service never invents it.
type LearningCycleTickResult struct {
	Schedule         currentstore.LearningCycleScheduleRecord
	Task             currentstore.LearningCycleTaskRecord
	Due              bool
	TaskCreated      bool
	AdmissionCreated bool
	LoopResult       *loopapi.RunResult
	FinalizeApplied  bool
}

// LearningCycleService is an explicitly constructed application coordinator
// around the same ChatService Current Store and Universal Loop. Constructing a
// normal ChatService does not construct this value or read Learning tables.
type LearningCycleService struct {
	chat *ChatService
}

// NewLearningCycleService opts one existing Chat composition into explicit
// Learning-cycle Tick. The caller retains ownership of ChatService, Store, and
// Loop; no timer, worker, queue, Provider, or second runtime is created here.
func NewLearningCycleService(chat *ChatService) (*LearningCycleService, error) {
	if chat == nil || chat.store == nil || isNilChatDependency(chat.loop) {
		return nil, fmt.Errorf(
			"%w: initialized ChatService is required",
			ErrInvalidLearningCycle,
		)
	}
	return &LearningCycleService{chat: chat}, nil
}

// Tick freezes at most one due logical window, atomically admits its ordinary
// model-only Run, advances the existing Universal Loop by one step, and asks
// the Store to derive the authoritative task projection. A Loop error does not
// suppress the bounded Store finalization attempt: the external model fact may
// already be durable, while UNKNOWN remains protected by its original Attempt.
func (service *LearningCycleService) Tick(
	ctx context.Context,
	input LearningCycleTickInput,
) (LearningCycleTickResult, error) {
	if service == nil || service.chat == nil || service.chat.store == nil ||
		isNilChatDependency(service.chat.loop) {
		return LearningCycleTickResult{}, fmt.Errorf(
			"%w: service is not initialized",
			ErrInvalidLearningCycle,
		)
	}
	if ctx == nil {
		return LearningCycleTickResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidLearningCycle,
		)
	}
	if err := ctx.Err(); err != nil {
		return LearningCycleTickResult{}, err
	}

	due, err := service.chat.store.EnsureDueTask(
		ctx,
		input.TenantID,
		input.ScheduleID,
		input.ObservedAt,
	)
	result := LearningCycleTickResult{
		Schedule:    due.Schedule,
		Task:        due.Task,
		Due:         due.Due,
		TaskCreated: due.Created,
	}
	if err != nil || !due.Due {
		return result, err
	}

	task := due.Task
	executionIdentity, identityErr := validateLearningCycleExecutionBinding(
		due.Schedule,
		task,
	)
	if identityErr != nil {
		return result, identityErr
	}
	switch task.State {
	case learningcontract.LearningCycleTaskPendingV1:
		admissionInput, compileErr := service.compileLearningCycleAdmission(
			ctx,
			due.Schedule,
			task,
			executionIdentity,
		)
		if compileErr != nil {
			return result, compileErr
		}
		admitted, commitErr := service.chat.store.CommitLearningCycleRunAdmission(
			ctx,
			currentstore.CommitLearningCycleRunAdmissionInput{
				TenantID:             task.TenantID,
				TaskID:               task.TaskID,
				ExpectedTaskRevision: task.Revision,
				Run:                  admissionInput,
			},
		)
		if commitErr != nil {
			return result, commitErr
		}
		if admitted.Task.TaskID != task.TaskID ||
			admitted.Run.RunID != executionIdentity.RunID ||
			admitted.Task.RunID != admitted.Run.RunID ||
			admitted.Task.RunManifestDigest != admitted.Run.ManifestDigest ||
			admitted.Task.State != learningcontract.LearningCycleTaskRunAdmittedV1 {
			return result, fmt.Errorf(
				"%w: Run admission did not bind the exact task",
				ErrLearningCycleIntegrity,
			)
		}
		task = admitted.Task
		result.Task = task
		result.AdmissionCreated = admitted.Created
	case learningcontract.LearningCycleTaskRunAdmittedV1:
		if task.RunID != executionIdentity.RunID || task.Revision != 1 {
			return result, fmt.Errorf(
				"%w: open admitted task does not bind its derived Run",
				ErrLearningCycleIntegrity,
			)
		}
	default:
		return result, fmt.Errorf(
			"%w: EnsureDueTask returned non-open state %q",
			ErrLearningCycleIntegrity,
			task.State,
		)
	}

	advanced, loopErr := service.chat.loop.Run(ctx, loopapi.RunInput{
		RunID:       task.RunID,
		MaxSteps:    learningCycleLoopMaxSteps,
		MaxDuration: learningCycleLoopMaxDuration,
	})
	if advanced.RunID == "" && loopErr == nil {
		loopErr = fmt.Errorf(
			"%w: Universal Loop returned no receipt",
			ErrLearningCycleIntegrity,
		)
	} else if advanced.RunID != "" {
		if validateErr := advanced.Validate(); validateErr != nil ||
			advanced.RunID != task.RunID {
			loopErr = errors.Join(loopErr, fmt.Errorf(
				"%w: Universal Loop returned another or invalid Run: %v",
				ErrLearningCycleIntegrity,
				validateErr,
			))
		} else {
			stable := advanced
			result.LoopResult = &stable
		}
	}

	finalizeCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		learningCycleFinalizeMaxDuration,
	)
	finalized, finalizeErr := service.chat.store.FinalizeLearningCycleTask(
		finalizeCtx,
		currentstore.FinalizeLearningCycleTaskInput{
			TenantID:             task.TenantID,
			TaskID:               task.TaskID,
			ExpectedTaskRevision: task.Revision,
		},
	)
	cancel()
	if errors.Is(finalizeErr, currentstore.ErrLearningCycleNotReady) {
		finalizeErr = nil
	} else if finalizeErr == nil {
		finalIdentity, bindingErr := validateLearningCycleExecutionBinding(
			due.Schedule,
			finalized.Task,
		)
		if finalized.Task.TaskID != task.TaskID || bindingErr != nil ||
			finalized.Task.RunID != finalIdentity.RunID {
			finalizeErr = fmt.Errorf(
				"%w: finalization returned another or invalid task: %v",
				ErrLearningCycleIntegrity,
				bindingErr,
			)
		} else {
			result.Task = finalized.Task
			result.FinalizeApplied = finalized.Applied
		}
	}
	return result, errors.Join(loopErr, finalizeErr)
}

func (service *LearningCycleService) compileLearningCycleAdmission(
	ctx context.Context,
	schedule currentstore.LearningCycleScheduleRecord,
	task currentstore.LearningCycleTaskRecord,
	executionIdentity learningcontract.LearningCycleExecutionIdentityV1,
) (currentstore.CommitRunAdmissionInput, error) {
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          string(task.RequestCanonical),
		},
	)
	if err != nil {
		return currentstore.CommitRunAdmissionInput{}, fmt.Errorf(
			"%w: construct cycle TaskInput: %v",
			ErrInvalidLearningCycle,
			err,
		)
	}
	taskDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentTaskInput,
		chatJSONMediaType,
		taskCanonical,
	)
	if err != nil {
		return currentstore.CommitRunAdmissionInput{}, fmt.Errorf(
			"%w: construct cycle TASK_INPUT content: %v",
			ErrInvalidLearningCycle,
			err,
		)
	}
	deadline := time.UnixMicro(task.Request.WindowEndMicros).UTC()
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:          task.TenantID,
			AdmissionKey:      executionIdentity.AdmissionKey,
			PrincipalID:       schedule.Schedule.ServicePrincipalID,
			WorkspaceID:       schedule.Schedule.WorkspaceID,
			AgentID:           schedule.Schedule.AgentID,
			ProfileID:         schedule.Schedule.ProfileID,
			TaskInputRef:      taskDigest,
			RequestedPorts:    []moduleapi.PortRef{chatModelGeneratePortV1},
			Deadline:          deadline,
			CancellationScope: chatCancellationScope,
			ExplicitLimits:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		return currentstore.CommitRunAdmissionInput{}, fmt.Errorf(
			"%w: construct cycle AdmissionIntent: %v",
			ErrInvalidLearningCycle,
			err,
		)
	}
	return service.chat.compileRunAdmissionInput(
		ctx,
		task.TenantID,
		intentCanonical,
		intentDigest,
		currentstore.ContentInput{
			Digest:         taskDigest,
			Kind:           currentstore.ContentTaskInput,
			MediaType:      chatJSONMediaType,
			CanonicalBytes: taskCanonical,
		},
		runCompileIdentity{
			RunID:           executionIdentity.RunID,
			MemberID:        executionIdentity.MemberID,
			RecoveryRootRef: executionIdentity.RecoveryRootRef,
		},
		false,
	)
}

func validateLearningCycleExecutionBinding(
	schedule currentstore.LearningCycleScheduleRecord,
	task currentstore.LearningCycleTaskRecord,
) (learningcontract.LearningCycleExecutionIdentityV1, error) {
	if schedule.Schedule.TenantID != task.TenantID ||
		schedule.Schedule.ScheduleID != task.ScheduleID ||
		schedule.ScheduleDigest != task.ScheduleDigest ||
		task.Request.ScheduleDigest != task.ScheduleDigest ||
		task.Request.RequestDigest != task.RequestDigest {
		return learningcontract.LearningCycleExecutionIdentityV1{}, fmt.Errorf(
			"%w: Schedule and task identity differ",
			ErrLearningCycleIntegrity,
		)
	}
	identity, err := learningcontract.DeriveLearningCycleExecutionIdentityV1(
		task.TaskID,
		task.RequestDigest,
	)
	if err != nil {
		return learningcontract.LearningCycleExecutionIdentityV1{}, fmt.Errorf(
			"%w: derive cycle execution identity: %v",
			ErrLearningCycleIntegrity,
			err,
		)
	}
	return identity, nil
}
