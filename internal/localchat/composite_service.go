package localchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	compositeAdmissionKeyPrefix  = "composite-chat-admission-"
	compositeRunIDPrefix         = "composite-chat-run-"
	compositeMemberIDPrefix      = "composite-chat-member-"
	compositeRecoveryRootPrefix  = "composite-chat-recovery-"
	compositeAdmissionKeyDomain  = "freeagent.localchat.composite-admission-key/v1"
	compositeRunIDDomain         = "freeagent.localchat.composite-run-id/v1"
	compositeMemberIDDomain      = "freeagent.localchat.composite-member-id/v1"
	compositeRecoveryRootDomain  = "freeagent.localchat.composite-recovery-root/v1"
	compositeChildLoopMaxSteps   = 1
	compositeParentLoopMaxSteps  = 1
	compositeMaximumParallelRuns = corecontract.CompositeMaxChildrenV1
)

// CompositeChatService is a thin application orchestrator. Every participant
// still executes through the caller-supplied Universal Loop and Current Store;
// this type owns no Scheduler, Runtime, Store, model selection, or retry fact.
type CompositeChatService struct {
	store        *currentstore.Store
	loop         loopapi.Loop
	now          func() time.Time
	newRequestID func() (string, error)
}

type CompositeChildChatResult struct {
	RunID      string
	LoopResult loopapi.RunResult
}

// CompositeReviewerChatResult is present only when the frozen Composite
// family contains the optional Reviewer Run. It is a read-only application
// view; the ReviewVerdict authority remains in the Reviewer's persisted
// MODEL_RESULT and is enforced by Current Store before Root merge admission.
type CompositeReviewerChatResult struct {
	RunID      string
	LoopResult loopapi.RunResult
}

type CompositeChatResult struct {
	RequestID        string
	Deadline         time.Time
	RootRunID        string
	AdmissionCreated bool
	Children         []CompositeChildChatResult
	Reviewer         *CompositeReviewerChatResult
	RepairChildren   []CompositeChildChatResult
	RepairReviewer   *CompositeReviewerChatResult
	LoopResult       loopapi.RunResult
	TerminalResult   *currentstore.TerminalRunResult
	Reply            string
	FailureCode      string
}

func NewCompositeChatService(
	store *currentstore.Store,
	loop loopapi.Loop,
) (*CompositeChatService, error) {
	if store == nil || isNilChatDependency(loop) {
		return nil, fmt.Errorf(
			"%w: Current Store and Universal Loop are required",
			ErrInvalidChat,
		)
	}
	return &CompositeChatService{
		store:        store,
		loop:         loop,
		now:          time.Now,
		newRequestID: newLocalChatRequestID,
	}, nil
}

func (service *CompositeChatService) Chat(
	ctx context.Context,
	input ChatInput,
) (CompositeChatResult, error) {
	if service == nil || service.store == nil ||
		isNilChatDependency(service.loop) || service.now == nil ||
		service.newRequestID == nil {
		return CompositeChatResult{}, fmt.Errorf(
			"%w: CompositeChatService is not initialized",
			ErrInvalidChat,
		)
	}
	if ctx == nil {
		return CompositeChatResult{}, fmt.Errorf(
			"%w: context is nil", ErrInvalidChat,
		)
	}
	if err := ctx.Err(); err != nil {
		return CompositeChatResult{}, err
	}
	if input.ConversationID != "" ||
		input.ExpectedConversationRevision != 0 ||
		input.ExpectedHeadRunID != "" {
		return CompositeChatResult{}, fmt.Errorf(
			"%w: Conversation and Composite are mutually exclusive in W1",
			ErrInvalidChat,
		)
	}
	requestID, deadline, err := service.freezeRequestIdentity(input)
	result := CompositeChatResult{RequestID: requestID, Deadline: deadline}
	if err != nil {
		return result, err
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          input.Message,
		},
	)
	if err != nil {
		return result, fmt.Errorf("%w: task input: %v", ErrInvalidChat, err)
	}
	taskDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentTaskInput,
		chatJSONMediaType,
		taskCanonical,
	)
	if err != nil {
		return result, fmt.Errorf("%w: task content: %v", ErrInvalidChat, err)
	}
	admissionKey := deriveChatID(
		compositeAdmissionKeyPrefix,
		compositeAdmissionKeyDomain,
		input.TenantID+"\x00"+requestID,
	)
	_, intentCanonical, intentDigest, err :=
		corecontract.NewAdmissionIntentV1(corecontract.AdmissionIntentV1{
			SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:          input.TenantID,
			AdmissionKey:      admissionKey,
			PrincipalID:       input.PrincipalID,
			WorkspaceID:       input.WorkspaceID,
			AgentID:           input.AgentID,
			ProfileID:         input.ProfileID,
			TaskInputRef:      taskDigest,
			RequestedPorts:    []moduleapi.PortRef{chatModelGeneratePortV1},
			Deadline:          deadline,
			CancellationScope: corecontract.CancellationScopeFamilyV1,
			ExplicitLimits:    json.RawMessage(`{}`),
		})
	if err != nil {
		return result, fmt.Errorf("%w: admission intent: %v", ErrInvalidChat, err)
	}

	family, found, err := service.store.ResolveCompositeRunFamily(
		ctx,
		input.TenantID,
		admissionKey,
		intentDigest,
	)
	if err != nil {
		return result, err
	}
	if !found {
		family, err = service.compileAndCommit(
			ctx,
			input.TenantID,
			intentCanonical,
			intentDigest,
			currentstore.ContentInput{
				Digest:         taskDigest,
				Kind:           currentstore.ContentTaskInput,
				MediaType:      chatJSONMediaType,
				CanonicalBytes: taskCanonical,
			},
		)
		if err != nil {
			return result, err
		}
	}
	result.RootRunID = family.Parent.RunID
	result.AdmissionCreated = family.Created
	result.Children = make([]CompositeChildChatResult, len(family.Children))
	if len(family.RepairChildren) != 0 || family.RepairReviewer != nil {
		return service.chatDecisionFamily(ctx, family, result)
	}

	var wait sync.WaitGroup
	var errorLock sync.Mutex
	childErrors := make([]error, 0)
	semaphore := make(chan struct{}, compositeMaximumParallelRuns)
	for index := range family.Children {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			child := family.Children[index]
			advanced, runErr := service.loop.Run(ctx, loopapi.RunInput{
				RunID:       child.RunID,
				MaxSteps:    compositeChildLoopMaxSteps,
				MaxDuration: chatLoopMaxDuration,
			})
			result.Children[index] = CompositeChildChatResult{
				RunID:      child.RunID,
				LoopResult: advanced,
			}
			if runErr != nil {
				errorLock.Lock()
				childErrors = append(childErrors, fmt.Errorf(
					"composite Child %d (%s): %w",
					index,
					child.RunID,
					runErr,
				))
				errorLock.Unlock()
				return
			}
			if validateErr := advanced.Validate(); validateErr != nil ||
				advanced.RunID != child.RunID {
				errorLock.Lock()
				childErrors = append(childErrors, fmt.Errorf(
					"%w: composite Child %d returned another or invalid Run: %v",
					ErrChatIntegrity,
					index,
					validateErr,
				))
				errorLock.Unlock()
			}
		}()
	}
	wait.Wait()
	if len(childErrors) != 0 {
		return result, errors.Join(childErrors...)
	}

	if family.Reviewer != nil {
		result.Reviewer = &CompositeReviewerChatResult{
			RunID: family.Reviewer.RunID,
		}
		reviewerAdvanced, runErr := service.loop.Run(ctx, loopapi.RunInput{
			RunID:       family.Reviewer.RunID,
			MaxSteps:    compositeChildLoopMaxSteps,
			MaxDuration: chatLoopMaxDuration,
		})
		result.Reviewer.LoopResult = reviewerAdvanced
		if runErr != nil {
			return result, fmt.Errorf(
				"composite Reviewer (%s): %w",
				family.Reviewer.RunID,
				runErr,
			)
		}
		if validateErr := reviewerAdvanced.Validate(); validateErr != nil ||
			reviewerAdvanced.RunID != family.Reviewer.RunID {
			return result, fmt.Errorf(
				"%w: composite Reviewer returned another or invalid Run: %v",
				ErrChatIntegrity,
				validateErr,
			)
		}
	}

	advanced, err := service.loop.Run(ctx, loopapi.RunInput{
		RunID:       family.Parent.RunID,
		MaxSteps:    compositeParentLoopMaxSteps,
		MaxDuration: chatLoopMaxDuration,
	})
	if err != nil {
		return result, err
	}
	if err := advanced.Validate(); err != nil ||
		advanced.RunID != family.Parent.RunID {
		return result, fmt.Errorf(
			"%w: composite root returned another or invalid Run: %v",
			ErrChatIntegrity,
			err,
		)
	}
	result.LoopResult = advanced
	if advanced.Disposition != loopapi.DispositionTerminated {
		return result, nil
	}
	terminal, err := service.store.GetTerminalRunResult(ctx, family.Parent.RunID)
	if err != nil {
		return result, err
	}
	result.TerminalResult = &terminal
	if terminal.ErrorClassification != "" {
		result.FailureCode = terminal.ErrorClassification
		return result, nil
	}
	switch terminal.State {
	case corecontract.ModelAttemptSucceeded:
		result.Reply = terminal.Output.AssistantText
	case corecontract.ModelAttemptFailed:
		result.FailureCode = terminal.ErrorClassification
	default:
		return result, fmt.Errorf(
			"%w: composite terminal result has model state %q",
			ErrChatIntegrity,
			terminal.State,
		)
	}
	return result, nil
}

func (service *CompositeChatService) freezeRequestIdentity(
	input ChatInput,
) (string, time.Time, error) {
	helper := &ChatService{
		now:          service.now,
		newRequestID: service.newRequestID,
	}
	return helper.freezeRequestIdentity(input)
}

func (service *CompositeChatService) compileAndCommit(
	ctx context.Context,
	tenantID string,
	intentCanonical []byte,
	intentDigest string,
	task currentstore.ContentInput,
) (currentstore.CompositeRunFamilyAdmissionResult, error) {
	basis, control, catalog, err := service.store.LoadPublishedBasis(ctx, tenantID)
	if err != nil {
		return currentstore.CompositeRunFamilyAdmissionResult{}, err
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil || controlRef != basis.Control {
		return currentstore.CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
			"%w: loaded Control cannot reconstruct PublishedBasis: %v",
			ErrChatIntegrity,
			err,
		)
	}
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil || catalogRef != basis.Catalog {
		return currentstore.CompositeRunFamilyAdmissionResult{}, fmt.Errorf(
			"%w: loaded Catalog cannot reconstruct PublishedBasis: %v",
			ErrChatIntegrity,
			err,
		)
	}
	compiled, err := (assemblycompiler.Compiler{}).CompileCompositeFamily(
		ctx,
		assemblycompiler.CompositeCompileInput{
			Parent: assemblycompiler.CompileInput{
				IntentCanonical: intentCanonical,
				IntentDigest:    intentDigest,
				RunID: deriveChatID(
					compositeRunIDPrefix,
					compositeRunIDDomain,
					intentDigest,
				),
				MemberID: deriveChatID(
					compositeMemberIDPrefix,
					compositeMemberIDDomain,
					intentDigest,
				),
				RecoveryRootRef: deriveChatID(
					compositeRecoveryRootPrefix,
					compositeRecoveryRootDomain,
					intentDigest,
				),
				PublishedBasis:   basis,
				ControlCanonical: controlCanonical,
				CatalogCanonical: catalogCanonical,
			},
		},
	)
	if err != nil {
		return currentstore.CompositeRunFamilyAdmissionResult{}, err
	}
	contents := []currentstore.ContentInput{task}
	input := currentstore.CommitCompositeRunFamilyInput{
		Parent: currentstore.CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         intentCanonical,
			IntentDigest:            intentDigest,
			MemberSnapshotCanonical: compiled.Parent.MemberSnapshotCanonical,
			RunManifestCanonical:    compiled.Parent.RunManifestCanonical,
			Contents:                contents,
		},
		Children: make(
			[]currentstore.CommitRunAdmissionInput,
			len(compiled.Children),
		),
	}
	for index, child := range compiled.Children {
		input.Children[index] = currentstore.CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         child.IntentCanonical,
			IntentDigest:            child.IntentDigest,
			MemberSnapshotCanonical: child.MemberSnapshotCanonical,
			RunManifestCanonical:    child.RunManifestCanonical,
			Contents:                contents,
		}
	}
	if compiled.Reviewer != nil {
		input.Reviewer = &currentstore.CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         compiled.Reviewer.IntentCanonical,
			IntentDigest:            compiled.Reviewer.IntentDigest,
			MemberSnapshotCanonical: compiled.Reviewer.MemberSnapshotCanonical,
			RunManifestCanonical:    compiled.Reviewer.RunManifestCanonical,
			Contents:                contents,
		}
	}
	if compiled.Decision != nil {
		input.RepairChildren = make(
			[]currentstore.CommitRunAdmissionInput,
			len(compiled.Decision.RepairChildren),
		)
		for index, child := range compiled.Decision.RepairChildren {
			input.RepairChildren[index] = currentstore.CommitRunAdmissionInput{
				PublishedBasis:          basis,
				IntentCanonical:         child.IntentCanonical,
				IntentDigest:            child.IntentDigest,
				MemberSnapshotCanonical: child.MemberSnapshotCanonical,
				RunManifestCanonical:    child.RunManifestCanonical,
				Contents:                contents,
			}
		}
		reviewer := compiled.Decision.RepairReviewer
		input.RepairReviewer = &currentstore.CommitRunAdmissionInput{
			PublishedBasis:          basis,
			IntentCanonical:         reviewer.IntentCanonical,
			IntentDigest:            reviewer.IntentDigest,
			MemberSnapshotCanonical: reviewer.MemberSnapshotCanonical,
			RunManifestCanonical:    reviewer.RunManifestCanonical,
			Contents:                contents,
		}
	}
	return service.store.CommitCompositeRunFamily(ctx, input)
}
