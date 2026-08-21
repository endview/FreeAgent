package localchat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	learningReviewDefaultDeadline = 2 * time.Minute
	learningReviewLoopMaxDuration = 2 * time.Minute
	learningReviewLoopMaxSteps    = uint32(1)

	autoLearningReviewIDPrefix    = "learning-review-"
	learningReviewAdmissionPrefix = "learning-review-admission-"
	learningReviewRunPrefix       = "learning-review-run-"
	learningReviewMemberPrefix    = "learning-review-member-"
	learningReviewRecoveryPrefix  = "learning-review-recovery-"

	learningReviewAdmissionDomain = "freeagent.localchat.learning-review-admission/v1"
	learningReviewRunDomain       = "freeagent.localchat.learning-review-run/v1"
	learningReviewMemberDomain    = "freeagent.localchat.learning-review-member/v1"
	learningReviewRecoveryDomain  = "freeagent.localchat.learning-review-recovery/v1"
)

var (
	// ErrInvalidLearningReview identifies invalid application input or an
	// uninitialized optional Learning Review service.
	ErrInvalidLearningReview = errors.New(
		"localchat: invalid Learning Review request",
	)

	// ErrLearningReviewIntegrity identifies a Store, Loop, or finalization
	// result that does not close to the exact Proposal and Reviewer Run.
	ErrLearningReviewIntegrity = errors.New(
		"localchat: Learning Review integrity violation",
	)
)

// LearningReviewInput selects one Operator-authorized, independent Reviewer
// Agent/Profile for an existing Proposal. Workspace and draft material are
// recovered from the Proposal itself and cannot be supplied by this caller.
// An explicit ReviewID, like an explicit Chat RequestID, requires an explicit
// UTC deadline so exact retries cannot silently create a different intent.
type LearningReviewInput struct {
	TenantID          string
	PrincipalID       string
	ProposalID        string
	ReviewerAgentID   string
	ReviewerProfileID string
	ReviewID          string
	Deadline          time.Time
	MaxOutputTokens   uint32
}

// LearningReviewResult exposes the exact ordinary Run result and the
// authoritative post-finalization Proposal. It does not synthesize a verdict
// from provider output; APPROVED/REJECTED are Store-owned states.
type LearningReviewResult struct {
	ReviewID         string
	Deadline         time.Time
	ProposalID       string
	RunID            string
	AdmissionCreated bool
	LoopResult       loopapi.RunResult
	Proposal         currentstore.LearningProposalRecord
	FinalizeApplied  bool
}

// LearningReviewService is optional application composition around the same
// ChatService dependencies. Constructing a normal ChatService does not enable
// Learning review or add a second Store, Loop, Registry, or model path.
type LearningReviewService struct {
	chat        *ChatService
	now         func() time.Time
	newReviewID func() (string, error)
}

// NewLearningReviewService explicitly enables the application workflow while
// retaining ownership of the supplied ChatService with its unique Store and
// Loop.
func NewLearningReviewService(
	chat *ChatService,
) (*LearningReviewService, error) {
	if chat == nil || chat.store == nil || isNilChatDependency(chat.loop) {
		return nil, fmt.Errorf(
			"%w: initialized ChatService is required",
			ErrInvalidLearningReview,
		)
	}
	return &LearningReviewService{
		chat:        chat,
		now:         time.Now,
		newReviewID: newLearningReviewID,
	}, nil
}

// Review admits at most one ordinary Reviewer Run, advances exactly one model
// step, and asks the Store to derive the review state from that Run's exact
// Attempt/Result closure. Exact retries reuse the admitted Run and never
// create a second model permission.
func (service *LearningReviewService) Review(
	ctx context.Context,
	input LearningReviewInput,
) (LearningReviewResult, error) {
	if service == nil || service.chat == nil || service.chat.store == nil ||
		isNilChatDependency(service.chat.loop) || service.now == nil ||
		service.newReviewID == nil {
		return LearningReviewResult{}, fmt.Errorf(
			"%w: service is not initialized",
			ErrInvalidLearningReview,
		)
	}
	if ctx == nil {
		return LearningReviewResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidLearningReview,
		)
	}
	if err := ctx.Err(); err != nil {
		return LearningReviewResult{}, err
	}

	reviewID, deadline, err := service.freezeReviewIdentity(input)
	result := LearningReviewResult{
		ReviewID:   reviewID,
		Deadline:   deadline,
		ProposalID: input.ProposalID,
	}
	if err != nil {
		return result, err
	}

	proposal, err := service.chat.store.GetLearningProposal(
		ctx,
		input.TenantID,
		input.ProposalID,
	)
	if err != nil {
		return result, err
	}
	maxOutputTokens := input.MaxOutputTokens
	if maxOutputTokens == 0 {
		maxOutputTokens = learningcontract.MaxReviewOutputTokensV1
	}
	_, reviewCanonical, _, err := learningcontract.NewReviewRequestV1(
		learningcontract.ReviewRequestV1{
			SchemaVersion:       learningcontract.ReviewRequestSchemaVersionV1,
			Instructions:        learningcontract.ReviewInstructionsV1,
			ProposalID:          proposal.ProposalID,
			SourceFingerprint:   proposal.Proposal.SourceFingerprint,
			ContentFingerprint:  proposal.Proposal.ContentFingerprint,
			DraftDigest:         proposal.Proposal.DraftDigest,
			OutputSchemaVersion: learningcontract.ReviewVerdictSchemaVersionV1,
			MaxOutputTokens:     maxOutputTokens,
			ReviewPolicy:        learningcontract.ReviewPolicyProposalGateV1,
			ProposalCanonical:   json.RawMessage(proposal.ProposalCanonical),
			DraftCanonical:      json.RawMessage(proposal.DraftCanonical),
		},
	)
	if err != nil {
		return result, fmt.Errorf(
			"%w: construct exact review request: %v",
			ErrInvalidLearningReview,
			err,
		)
	}
	_, taskCanonical, err := corecontract.NewTaskInputV1(
		corecontract.TaskInputV1{
			SchemaVersion: corecontract.TaskInputSchemaVersionV1,
			Text:          string(reviewCanonical),
		},
	)
	if err != nil {
		return result, fmt.Errorf(
			"%w: construct review TaskInput: %v",
			ErrInvalidLearningReview,
			err,
		)
	}
	taskDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentTaskInput,
		chatJSONMediaType,
		taskCanonical,
	)
	if err != nil {
		return result, fmt.Errorf(
			"%w: construct review TaskInput content: %v",
			ErrInvalidLearningReview,
			err,
		)
	}

	stableIdentity := input.TenantID + "\x00" + input.ProposalID + "\x00" + reviewID
	admissionKey := deriveChatID(
		learningReviewAdmissionPrefix,
		learningReviewAdmissionDomain,
		stableIdentity,
	)
	_, intentCanonical, intentDigest, err := corecontract.NewAdmissionIntentV1(
		corecontract.AdmissionIntentV1{
			SchemaVersion:     corecontract.AdmissionIntentSchemaVersionV1,
			TenantID:          input.TenantID,
			AdmissionKey:      admissionKey,
			PrincipalID:       input.PrincipalID,
			WorkspaceID:       proposal.Proposal.Workspace.ID,
			AgentID:           input.ReviewerAgentID,
			ProfileID:         input.ReviewerProfileID,
			TaskInputRef:      taskDigest,
			RequestedPorts:    []moduleapi.PortRef{chatModelGeneratePortV1},
			Deadline:          deadline,
			CancellationScope: chatCancellationScope,
			ExplicitLimits:    json.RawMessage(`{}`),
		},
	)
	if err != nil {
		return result, fmt.Errorf(
			"%w: construct review AdmissionIntent: %v",
			ErrInvalidLearningReview,
			err,
		)
	}

	admission, found, err := service.chat.store.ResolveAdmission(
		ctx,
		input.TenantID,
		admissionKey,
		intentDigest,
	)
	if err != nil {
		return result, err
	}
	if found {
		proposal, err = service.chat.store.GetLearningProposal(
			ctx,
			input.TenantID,
			input.ProposalID,
		)
		if err != nil {
			return result, err
		}
		if proposal.ReviewRunID != admission.RunID ||
			proposal.State == currentstore.LearningProposalSubmitted {
			return result, fmt.Errorf(
				"%w: admitted review Run is not bound to the Proposal",
				ErrLearningReviewIntegrity,
			)
		}
	} else {
		identity := runCompileIdentity{
			RunID: deriveChatID(
				learningReviewRunPrefix,
				learningReviewRunDomain,
				intentDigest,
			),
			MemberID: deriveChatID(
				learningReviewMemberPrefix,
				learningReviewMemberDomain,
				intentDigest,
			),
			RecoveryRootRef: deriveChatID(
				learningReviewRecoveryPrefix,
				learningReviewRecoveryDomain,
				intentDigest,
			),
		}
		runInput, compileErr := service.chat.compileRunAdmissionInput(
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
			identity,
			false,
		)
		if compileErr != nil {
			return result, compileErr
		}
		committed, commitErr := service.chat.store.CommitLearningReviewAdmission(
			ctx,
			currentstore.CommitLearningReviewAdmissionInput{
				TenantID:                 input.TenantID,
				ProposalID:               input.ProposalID,
				ExpectedProposalRevision: proposal.Revision,
				Run:                      runInput,
			},
		)
		if commitErr != nil {
			return result, commitErr
		}
		admission = committed.Run
		proposal = committed.Proposal
		result.AdmissionCreated = committed.Created
	}
	if admission.RunID == "" || proposal.ProposalID != input.ProposalID ||
		proposal.ReviewRunID != admission.RunID ||
		proposal.State == currentstore.LearningProposalSubmitted {
		return result, fmt.Errorf(
			"%w: review admission did not close to its Proposal",
			ErrLearningReviewIntegrity,
		)
	}
	result.RunID = admission.RunID

	advanced, err := service.chat.loop.Run(ctx, loopapi.RunInput{
		RunID:       admission.RunID,
		MaxSteps:    learningReviewLoopMaxSteps,
		MaxDuration: learningReviewLoopMaxDuration,
	})
	if err != nil {
		return result, err
	}
	if err := advanced.Validate(); err != nil || advanced.RunID != admission.RunID {
		return result, fmt.Errorf(
			"%w: Universal Loop returned another or invalid Run: %v",
			ErrLearningReviewIntegrity,
			err,
		)
	}
	result.LoopResult = advanced

	finalized, err := service.chat.store.FinalizeLearningReview(
		ctx,
		currentstore.FinalizeLearningReviewInput{
			TenantID:                 input.TenantID,
			ProposalID:               input.ProposalID,
			ExpectedProposalRevision: proposal.Revision,
		},
	)
	if err != nil {
		return result, err
	}
	if finalized.Proposal.ProposalID != input.ProposalID ||
		finalized.Proposal.ReviewRunID != admission.RunID ||
		finalized.Proposal.State == currentstore.LearningProposalSubmitted ||
		finalized.Proposal.State == currentstore.LearningProposalReviewPending {
		return result, fmt.Errorf(
			"%w: finalization did not produce a review terminal state",
			ErrLearningReviewIntegrity,
		)
	}
	result.Proposal = finalized.Proposal
	result.FinalizeApplied = finalized.Applied
	return result, nil
}

func (service *LearningReviewService) freezeReviewIdentity(
	input LearningReviewInput,
) (string, time.Time, error) {
	reviewID := input.ReviewID
	deadline := input.Deadline
	generated := reviewID == ""
	if generated {
		var err error
		reviewID, err = service.newReviewID()
		if err != nil {
			return "", time.Time{}, fmt.Errorf(
				"%w: generate ReviewID: %v",
				ErrInvalidLearningReview,
				err,
			)
		}
	}
	if !validChatOpaque(reviewID) {
		return reviewID, time.Time{}, fmt.Errorf(
			"%w: ReviewID must be canonical, non-empty, and bounded",
			ErrInvalidLearningReview,
		)
	}
	if deadline.IsZero() {
		if !generated {
			return reviewID, time.Time{}, fmt.Errorf(
				"%w: explicit ReviewID requires an explicit UTC deadline",
				ErrInvalidLearningReview,
			)
		}
		deadline = service.now().UTC().Round(0).Add(
			learningReviewDefaultDeadline,
		)
	} else {
		_, offset := deadline.Zone()
		if offset != 0 {
			return reviewID, time.Time{}, fmt.Errorf(
				"%w: deadline must use UTC (zero offset)",
				ErrInvalidLearningReview,
			)
		}
		deadline = deadline.Round(0).UTC()
	}
	return reviewID, deadline, nil
}

func newLearningReviewID() (string, error) {
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return "", err
	}
	return autoLearningReviewIDPrefix + hex.EncodeToString(identity[:]), nil
}
