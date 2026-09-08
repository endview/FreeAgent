package localchat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/assemblycompiler"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	chatJSONMediaType      = "application/json"
	chatCancellationScope  = "run"
	chatDefaultDeadline    = 2 * time.Minute
	chatLoopMaxDuration    = 2 * time.Minute
	chatPureLoopMaxSteps   = 1
	chatActionLoopMaxSteps = 3
	autoRequestIDPrefix    = "request-"
	admissionKeyPrefix     = "chat-admission-"
	runIDPrefix            = "chat-run-"
	memberIDPrefix         = "chat-member-"
	recoveryRootRefPrefix  = "chat-recovery-"
	admissionKeyDomain     = "freeagent.localchat.admission-key/v1"
	runIDDomain            = "freeagent.localchat.run-id/v1"
	memberIDDomain         = "freeagent.localchat.member-id/v1"
	recoveryRootRefDomain  = "freeagent.localchat.recovery-root/v1"
)

var (
	// ErrInvalidChat identifies an invalid service dependency or Chat input.
	ErrInvalidChat = errors.New("localchat: invalid chat request")

	// ErrChatIntegrity identifies a result or reconstructed contract that does
	// not close to the admitted Run.
	ErrChatIntegrity = errors.New("localchat: chat integrity violation")
)

var chatModelGeneratePortV1 = moduleapi.PortRef{
	Name:         moduleapi.PortNameModelGenerate,
	ExactVersion: moduleapi.PortVersionV1,
}

// ChatInput is the complete S1 Pure Chat ingress value. An explicit RequestID
// must be accompanied by an explicit zero-offset deadline so a retry cannot
// silently create a different AdmissionIntent. If RequestID is empty, the
// service generates both a request ID and (when omitted) a deadline and returns
// them in ChatResult for any subsequent retry.
type ChatInput struct {
	TenantID    string
	PrincipalID string
	WorkspaceID string
	AgentID     string
	ProfileID   string
	Message     string
	RequestID   string
	Deadline    time.Time

	// ConversationID opts this request into the W1 one-turn-per-Run
	// Conversation path. The expected revision and head are frozen into the
	// AdmissionIntent so retries cannot silently bind to a later head.
	ConversationID               string
	ExpectedConversationRevision uint64
	ExpectedHeadRunID            string
}

// ConversationCreateInput is the narrow application-level contract shared by
// CLI and loopback HTTP. It fixes stable owner and assembly IDs without
// exposing the HTTP adapter to the Current Store itself.
type ConversationCreateInput struct {
	ConversationID string
	TenantID       string
	PrincipalID    string
	WorkspaceID    string
	AgentID        string
	ProfileID      string
}

// ConversationCreateResult distinguishes a new Conversation from an exact
// idempotent retry while returning the verified current head.
type ConversationCreateResult struct {
	ConversationID string
	TenantID       string
	PrincipalID    string
	WorkspaceID    string
	AgentID        string
	ProfileID      string
	HeadRunID      string
	Revision       uint64
	Created        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ChatResult keeps the exact public Loop result. Non-TERMINATED dispositions,
// including WAITING_RECONCILIATION, are returned without synthesizing a reply
// or reading a terminal result.
type ChatResult struct {
	RequestID            string
	Deadline             time.Time
	RunID                string
	AdmissionCreated     bool
	ConversationID       string
	ConversationRevision uint64
	LoopResult           loopapi.RunResult
	TerminalResult       *currentstore.TerminalRunResult
	Reply                string
	FailureCode          string
}

// ChatService is the local application service shared by later CLI and
// loopback HTTP composition roots. The default constructor remains Pure Chat;
// only NewActionChatService supplies the optional admission-time Action gate.
type ChatService struct {
	store              *currentstore.Store
	loop               loopapi.Loop
	actionMaterializer assemblycompiler.ActionMaterializerV1
	now                func() time.Time
	newRequestID       func() (string, error)
}

// NewActionChatService explicitly enables admission-time Action Describe for
// profiles that bind action.provider/v1. Pure Chat profiles still take the
// exact NewChatService path and never call the supplied materializer.
func NewActionChatService(
	store *currentstore.Store,
	loop loopapi.Loop,
	materializer assemblycompiler.ActionMaterializerV1,
) (*ChatService, error) {
	if isNilChatDependency(materializer) {
		return nil, fmt.Errorf(
			"%w: admission Action materializer is required",
			ErrInvalidChat,
		)
	}
	service, err := NewChatService(store, loop)
	if err != nil {
		return nil, err
	}
	service.actionMaterializer = materializer
	return service, nil
}

// NewChatService composes the existing Current Store and Universal Loop. The
// caller retains ownership of both dependencies.
func NewChatService(
	store *currentstore.Store,
	loop loopapi.Loop,
) (*ChatService, error) {
	if store == nil || isNilChatDependency(loop) {
		return nil, fmt.Errorf(
			"%w: Current Store and Universal Loop are required",
			ErrInvalidChat,
		)
	}
	return &ChatService{
		store:        store,
		loop:         loop,
		now:          time.Now,
		newRequestID: newLocalChatRequestID,
	}, nil
}

// CreateConversation creates one revision-zero Conversation or returns the
// exact existing record. It delegates identity validation and atomic
// idempotency to the unique Current Store and does not admit or execute a Run.
func (service *ChatService) CreateConversation(
	ctx context.Context,
	input ConversationCreateInput,
) (ConversationCreateResult, error) {
	if service == nil || service.store == nil {
		return ConversationCreateResult{}, fmt.Errorf(
			"%w: ChatService is not initialized",
			ErrInvalidChat,
		)
	}
	if ctx == nil {
		return ConversationCreateResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidChat,
		)
	}
	if err := ctx.Err(); err != nil {
		return ConversationCreateResult{}, err
	}
	created, err := service.store.CreateConversation(
		ctx,
		currentstore.CreateConversationInput{
			ConversationID: input.ConversationID,
			TenantID:       input.TenantID,
			PrincipalID:    input.PrincipalID,
			WorkspaceID:    input.WorkspaceID,
			AgentID:        input.AgentID,
			ProfileID:      input.ProfileID,
		},
	)
	if err != nil {
		return ConversationCreateResult{}, err
	}
	record := created.Record
	return ConversationCreateResult{
		ConversationID: record.ConversationID,
		TenantID:       record.TenantID,
		PrincipalID:    record.PrincipalID,
		WorkspaceID:    record.WorkspaceID,
		AgentID:        record.AgentID,
		ProfileID:      record.ProfileID,
		HeadRunID:      record.HeadRunID,
		Revision:       record.Revision,
		Created:        created.Created,
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
	}, nil
}

// Chat admits and advances exactly one local Chat Run. Generated execution IDs
// are deterministic functions of the stable AdmissionIntent digest and never
// become model prompt content.
func (service *ChatService) Chat(
	ctx context.Context,
	input ChatInput,
) (ChatResult, error) {
	if service == nil ||
		service.store == nil ||
		isNilChatDependency(service.loop) ||
		service.now == nil ||
		service.newRequestID == nil {
		return ChatResult{}, fmt.Errorf(
			"%w: ChatService is not initialized",
			ErrInvalidChat,
		)
	}
	if ctx == nil {
		return ChatResult{}, fmt.Errorf("%w: context is nil", ErrInvalidChat)
	}
	if err := ctx.Err(); err != nil {
		return ChatResult{}, err
	}

	requestID, deadline, err := service.freezeRequestIdentity(input)
	result := ChatResult{
		RequestID:      requestID,
		Deadline:       deadline,
		ConversationID: input.ConversationID,
	}
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

	conversationTurn, admissionIdentity, err := chatConversationIntent(input)
	if err != nil {
		return result, err
	}
	admissionKey := deriveChatID(
		admissionKeyPrefix,
		admissionKeyDomain,
		input.TenantID+"\x00"+admissionIdentity+requestID,
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
			CancellationScope: chatCancellationScope,
			ExplicitLimits:    json.RawMessage(`{}`),
			ConversationTurn:  conversationTurn,
		})
	if err != nil {
		return result, fmt.Errorf("%w: admission intent: %v", ErrInvalidChat, err)
	}

	admission, found, err := service.store.ResolveAdmission(
		ctx,
		input.TenantID,
		admissionKey,
		intentDigest,
	)
	if err != nil {
		return result, err
	}
	if !found {
		admission, err = service.compileAndCommit(
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
			conversationTurn != nil,
		)
		if err != nil {
			return result, err
		}
	}

	result.RunID = admission.RunID
	result.AdmissionCreated = admission.Created
	if conversationTurn != nil {
		result.ConversationRevision =
			conversationTurn.ExpectedConversationRevision + 1
	}
	if found {
		terminal, terminalErr := service.store.GetTerminalRunResult(
			ctx,
			admission.RunID,
		)
		if terminalErr == nil {
			if err := service.applyTerminalResult(
				&result,
				terminal,
			); err != nil {
				return result, err
			}
			return result, nil
		}
		if !errors.Is(terminalErr, currentstore.ErrTerminalRunUnavailable) {
			return result, terminalErr
		}
	}
	maxSteps := uint32(chatPureLoopMaxSteps)
	if !isNilChatDependency(service.actionMaterializer) {
		// An Action-enabled service must reserve model-1 + action-1 before
		// starting and may then complete model-2 in the same bounded call.
		// Profiles without an Action Port still terminate after the original
		// single model step; this only widens the caller-side ceiling.
		maxSteps = chatActionLoopMaxSteps
	}
	advanced, err := service.loop.Run(ctx, loopapi.RunInput{
		RunID:       admission.RunID,
		MaxSteps:    maxSteps,
		MaxDuration: chatLoopMaxDuration,
	})
	if err != nil {
		return result, err
	}
	if err := advanced.Validate(); err != nil || advanced.RunID != admission.RunID {
		return result, fmt.Errorf(
			"%w: Universal Loop returned a result for another or invalid Run: %v",
			ErrChatIntegrity,
			err,
		)
	}
	result.LoopResult = advanced
	if advanced.Disposition != loopapi.DispositionTerminated {
		return result, nil
	}

	terminal, err := service.store.GetTerminalRunResult(ctx, admission.RunID)
	if err != nil {
		return result, err
	}
	return result, service.applyTerminalResult(&result, terminal)
}

func (service *ChatService) applyTerminalResult(
	result *ChatResult,
	terminal currentstore.TerminalRunResult,
) error {
	if service == nil || service.store == nil {
		return fmt.Errorf(
			"%w: ChatService is not initialized",
			ErrInvalidChat,
		)
	}
	if result == nil || terminal.RunID != result.RunID {
		return fmt.Errorf(
			"%w: terminal result belongs to another Run",
			ErrChatIntegrity,
		)
	}
	result.LoopResult = loopapi.RunResult{
		RunID:         terminal.RunID,
		Disposition:   loopapi.DispositionTerminated,
		FrameRevision: terminal.FrameRevision,
		ReasonCode:    terminal.ReasonCode,
	}
	if err := result.LoopResult.Validate(); err != nil {
		return fmt.Errorf(
			"%w: terminal Loop projection: %v",
			ErrChatIntegrity,
			err,
		)
	}
	result.TerminalResult = &terminal
	if terminal.ErrorClassification != "" {
		// Legal Action rejection, Action failure and RESULT_REJECTED all
		// preserve the successful source-model fact while terminating the
		// Run with a durable failure classification.
		result.FailureCode = terminal.ErrorClassification
		return nil
	}
	switch terminal.State {
	case corecontract.ModelAttemptSucceeded:
		result.Reply = terminal.Output.AssistantText
	case corecontract.ModelAttemptFailed:
		result.FailureCode = terminal.ErrorClassification
	default:
		return fmt.Errorf(
			"%w: terminal result has model state %q",
			ErrChatIntegrity,
			terminal.State,
		)
	}
	return nil
}

func (service *ChatService) freezeRequestIdentity(
	input ChatInput,
) (string, time.Time, error) {
	requestID := input.RequestID
	deadline := input.Deadline
	generated := requestID == ""
	if generated {
		var err error
		requestID, err = service.newRequestID()
		if err != nil {
			return "", time.Time{}, fmt.Errorf(
				"%w: generate request ID: %v",
				ErrInvalidChat,
				err,
			)
		}
	}
	if !validChatOpaque(requestID) {
		return requestID, time.Time{}, fmt.Errorf(
			"%w: request ID must be canonical, non-empty, and at most %d bytes",
			ErrInvalidChat,
			moduleapi.MaxOpaqueIDBytes,
		)
	}
	if deadline.IsZero() {
		if !generated {
			return requestID, time.Time{}, fmt.Errorf(
				"%w: an explicit request ID requires an explicit UTC deadline",
				ErrInvalidChat,
			)
		}
		deadline = service.now().UTC().Round(0).Add(chatDefaultDeadline)
	} else {
		_, offset := deadline.Zone()
		if offset != 0 {
			return requestID, time.Time{}, fmt.Errorf(
				"%w: deadline must use UTC (zero offset)",
				ErrInvalidChat,
			)
		}
		deadline = deadline.Round(0).UTC()
	}
	if deadline.IsZero() {
		return requestID, time.Time{}, fmt.Errorf(
			"%w: deadline is required",
			ErrInvalidChat,
		)
	}
	return requestID, deadline, nil
}

func (service *ChatService) compileAndCommit(
	ctx context.Context,
	tenantID string,
	intentCanonical []byte,
	intentDigest string,
	task currentstore.ContentInput,
	conversation bool,
) (currentstore.RunAdmissionResult, error) {
	runID := deriveChatID(runIDPrefix, runIDDomain, intentDigest)
	memberID := deriveChatID(memberIDPrefix, memberIDDomain, intentDigest)
	recoveryRootRef := deriveChatID(
		recoveryRootRefPrefix,
		recoveryRootRefDomain,
		intentDigest,
	)
	commitInput, err := service.compileRunAdmissionInput(
		ctx,
		tenantID,
		intentCanonical,
		intentDigest,
		task,
		runCompileIdentity{
			RunID:           runID,
			MemberID:        memberID,
			RecoveryRootRef: recoveryRootRef,
		},
		true,
	)
	if err != nil {
		return currentstore.RunAdmissionResult{}, err
	}
	if conversation {
		return service.store.CommitConversationTurnAdmission(ctx, commitInput)
	}
	return service.store.CommitRunAdmission(ctx, commitInput)
}

// runCompileIdentity contains the three opaque identities that distinguish
// application-level admission domains while preserving one Assembly Compiler
// and one immutable Run closure. Chat and optional workflows derive these
// values independently before entering this shared helper.
type runCompileIdentity struct {
	RunID           string
	MemberID        string
	RecoveryRootRef string
}

// compileRunAdmissionInput is the shared, non-persisting Assembly Compiler
// boundary. allowActions is true only for the existing Chat path; optional
// single-model workflows pass false so an Action-enabled Profile fails before
// any Run is admitted or external model permit can exist.
func (service *ChatService) compileRunAdmissionInput(
	ctx context.Context,
	tenantID string,
	intentCanonical []byte,
	intentDigest string,
	task currentstore.ContentInput,
	identity runCompileIdentity,
	allowActions bool,
) (currentstore.CommitRunAdmissionInput, error) {
	basis, control, catalog, err := service.store.LoadPublishedBasis(ctx, tenantID)
	if err != nil {
		return currentstore.CommitRunAdmissionInput{}, err
	}
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil || controlRef != basis.Control {
		return currentstore.CommitRunAdmissionInput{}, fmt.Errorf(
			"%w: loaded Control cannot reconstruct the PublishedBasis: %v",
			ErrChatIntegrity,
			err,
		)
	}
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil || catalogRef != basis.Catalog {
		return currentstore.CommitRunAdmissionInput{}, fmt.Errorf(
			"%w: loaded Catalog cannot reconstruct the PublishedBasis: %v",
			ErrChatIntegrity,
			err,
		)
	}
	var (
		actionMaterializer assemblycompiler.ActionMaterializerV1
		actionMaterials    []actionmaterializer.BindingMaterialV1
	)
	if allowActions {
		actionMaterializer = service.actionMaterializer
		actionMaterials, err = service.loadActionBindingMaterials(
			ctx,
			control,
			intentCanonical,
			intentDigest,
		)
		if err != nil {
			return currentstore.CommitRunAdmissionInput{}, err
		}
	}

	compiled, err := (assemblycompiler.Compiler{}).Compile(
		ctx,
		assemblycompiler.CompileInput{
			IntentCanonical:        intentCanonical,
			IntentDigest:           intentDigest,
			RunID:                  identity.RunID,
			MemberID:               identity.MemberID,
			RecoveryRootRef:        identity.RecoveryRootRef,
			PublishedBasis:         basis,
			ControlCanonical:       controlCanonical,
			CatalogCanonical:       catalogCanonical,
			ActionMaterializer:     actionMaterializer,
			ActionBindingMaterials: actionMaterials,
		},
	)
	if err != nil {
		return currentstore.CommitRunAdmissionInput{}, err
	}
	return currentstore.CommitRunAdmissionInput{
		PublishedBasis:          basis,
		IntentCanonical:         intentCanonical,
		IntentDigest:            intentDigest,
		MemberSnapshotCanonical: compiled.MemberSnapshotCanonical,
		RunManifestCanonical:    compiled.RunManifestCanonical,
		Contents:                []currentstore.ContentInput{task},
	}, nil
}

func chatConversationIntent(
	input ChatInput,
) (*corecontract.ConversationTurnIntentV1, string, error) {
	if input.ConversationID == "" {
		if input.ExpectedConversationRevision != 0 || input.ExpectedHeadRunID != "" {
			return nil, "", fmt.Errorf(
				"%w: Conversation revision/head require conversation ID",
				ErrInvalidChat,
			)
		}
		// Keep the pre-W1 AdmissionKey bytes unchanged for non-Conversation
		// callers by contributing no additional identity segment.
		return nil, "", nil
	}
	turn := &corecontract.ConversationTurnIntentV1{
		SchemaVersion:                corecontract.ConversationTurnIntentSchemaVersionV1,
		ConversationID:               input.ConversationID,
		ExpectedConversationRevision: input.ExpectedConversationRevision,
		ExpectedHeadRunID:            input.ExpectedHeadRunID,
	}
	if err := turn.Validate(); err != nil {
		return nil, "", fmt.Errorf("%w: Conversation turn: %v", ErrInvalidChat, err)
	}
	return turn, input.ConversationID + "\x00", nil
}

func (service *ChatService) loadActionBindingMaterials(
	ctx context.Context,
	control controlcontract.ControlSnapshot,
	intentCanonical []byte,
	intentDigest string,
) ([]actionmaterializer.BindingMaterialV1, error) {
	intent, err := corecontract.RestoreAdmissionIntentV1(
		bytes.Clone(intentCanonical),
		intentDigest,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: restore Action admission intent: %v",
			ErrChatIntegrity,
			err,
		)
	}
	profile, found := control.FindProfile(intent.ProfileID)
	if !found {
		return nil, fmt.Errorf(
			"%w: selected profile is absent from loaded Control",
			ErrChatIntegrity,
		)
	}
	actionBindingCount := 0
	for _, binding := range profile.Bindings {
		if binding.Port.Name == moduleapi.PortNameActionProvider &&
			binding.Port.ExactVersion == moduleapi.PortVersionV1 {
			actionBindingCount++
		}
	}
	if actionBindingCount == 0 {
		return nil, nil
	}
	if isNilChatDependency(service.actionMaterializer) {
		return nil, fmt.Errorf(
			"%w: selected profile binds action.provider/v1 but ChatService has no Action materializer",
			assemblycompiler.ErrCapabilityNotAvailable,
		)
	}
	materials := make(
		[]actionmaterializer.BindingMaterialV1,
		0,
		actionBindingCount,
	)
	for index, binding := range profile.Bindings {
		if binding.Port.Name != moduleapi.PortNameActionProvider ||
			binding.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		config, err := service.store.GetContent(ctx, binding.ConfigRef)
		if err != nil {
			return nil, fmt.Errorf(
				"localchat: load Action Binding %d CONFIG: %w",
				index,
				err,
			)
		}
		if config.Digest != binding.ConfigRef ||
			config.Kind != currentstore.ContentConfig ||
			config.MediaType != chatJSONMediaType {
			return nil, fmt.Errorf(
				"%w: Action Binding %d ConfigRef is not canonical CONFIG",
				ErrChatIntegrity,
				index,
			)
		}
		authority, err := service.store.GetContent(
			ctx,
			binding.AuthorityCeilingRef,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"localchat: load Action Binding %d AUTHORITY_CEILING: %w",
				index,
				err,
			)
		}
		if authority.Digest != binding.AuthorityCeilingRef ||
			authority.Kind != currentstore.ContentAuthorityCeiling ||
			authority.MediaType != chatJSONMediaType {
			return nil, fmt.Errorf(
				"%w: Action Binding %d AuthorityCeilingRef is not canonical AUTHORITY_CEILING",
				ErrChatIntegrity,
				index,
			)
		}
		materials = append(materials, actionmaterializer.BindingMaterialV1{
			ConfigCanonical:    bytes.Clone(config.CanonicalBytes),
			AuthorityCanonical: bytes.Clone(authority.CanonicalBytes),
		})
	}
	return materials, nil
}

func newLocalChatRequestID() (string, error) {
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return "", err
	}
	return autoRequestIDPrefix + hex.EncodeToString(identity[:]), nil
}

func deriveChatID(prefix string, domain string, stable string) string {
	return prefix + moduleapi.Digest(domain, []byte(stable))
}

func validChatOpaque(value string) bool {
	if value == "" ||
		len(value) > moduleapi.MaxOpaqueIDBytes ||
		!utf8.ValidString(value) ||
		value != strings.TrimSpace(value) ||
		value != moduleapi.CanonicalText(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func isNilChatDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
