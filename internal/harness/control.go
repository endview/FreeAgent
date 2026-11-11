package harness

// control.go contains the small owner-side control boundary used by GUI, CLI
// and, later, the model control tools. It deliberately stores operation
// records beside the existing Harness records; it is not a second task or
// execution system.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	controlJournalName       = "control-journal.jsonl"
	controlJournalMaxBytes   = 4 << 20
	controlJournalMaxRecords = 512
	controlConfirmationTTL   = 10 * time.Minute
	controlMaxCodeAttempts   = 5
	controlCodeLength        = 10
)

var (
	ErrControlOperationNotFound       = errors.New("harness: control operation not found")
	ErrControlStaleRevision           = errors.New("harness: control operation has a stale resource revision")
	ErrControlExpired                 = errors.New("harness: control operation confirmation expired")
	ErrControlWrongCode               = errors.New("harness: control confirmation code was not accepted")
	ErrControlConfirmationRequired    = errors.New("harness: control confirmation is required")
	ErrControlAlreadyUsed             = errors.New("harness: control operation was already decided")
	ErrControlUserInteractionRequired = errors.New("harness: interactive control capability is required")
	ErrControlIrreversible            = errors.New("harness: control operation is not reversible")
	ErrControlBusy                    = errors.New("harness: control resource is busy")
	ErrControlJournal                 = errors.New("harness: control journal is invalid")
	ErrExternalControlChange          = errors.New("harness: external control change requires review")
	ErrControlMCPReentryRequired      = errors.New("harness: MCP profile payload requires user re-entry")
	// ErrControlRunConfirmation keeps a Run-bound model proposal from being
	// applied outside its Run, which would leave the Run waiting forever.
	ErrControlRunConfirmation = errors.New("harness: control proposal belongs to a waiting Run; confirm it through that Run")
)

var controlServiceInitMu sync.Mutex

type ControlRisk string

const (
	ControlRiskOrdinary     ControlRisk = "ordinary"
	ControlRiskAuthority    ControlRisk = "authority"
	ControlRiskDestructive  ControlRisk = "destructive"
	ControlRiskIrreversible ControlRisk = "irreversible"
)

type ControlAuthorizationSource string

const (
	ControlUserDirect     ControlAuthorizationSource = "user_direct"
	ControlModelProposed  ControlAuthorizationSource = "model_proposed_confirmed"
	ControlSystemRecovery ControlAuthorizationSource = "system_recovery"
)

type ControlOperationStatus string

const (
	ControlPending          ControlOperationStatus = "pending"
	ControlApplied          ControlOperationStatus = "applied"
	ControlRejected         ControlOperationStatus = "rejected"
	ControlStale            ControlOperationStatus = "stale"
	ControlPartiallyApplied ControlOperationStatus = "partially_applied"
	ControlFailed           ControlOperationStatus = "failed"
)

// Operation names are intentionally typed strings. Adding a new name without
// adding validation and an apply handler is caught by ControlOperationCatalog.
const (
	ControlSetPermission               = "execution.set_permission"
	ControlSetExecutionProfile         = "execution.set_profile"
	ControlSetSandbox                  = "execution.set_sandbox"
	ControlSetNetwork                  = "execution.set_network"
	ControlSetFileScope                = "execution.set_file_scope"
	ControlSetTool                     = "tools.set_enabled"
	ControlSetCommandPolicy            = "commands.set_policy"
	ControlSetWorkspace                = "config.set_workspace"
	ControlSetDefaultModel             = "model.set_default"
	ControlSetModelProfile             = "model.set_profile"
	ControlSetProviderEnabled          = "providers.set_enabled"
	ControlSetServerPolicy             = "server.set_policy"
	ControlRemoveModel                 = "model.remove"
	ControlSetAutoPolicy               = "auto.set_policy"
	ControlSetHTTPPolicy               = "http.set_policy"
	ControlSetChannelPolicy            = "channels.set_policy"
	ControlSetMemoryPolicy             = "memory.set_policy"
	ControlSetPromptPolicy             = "prompt.set_policy"
	ControlSetCollaboration            = "collaboration.set_policy"
	ControlSetRuntimeLimits            = "runtime.set_limits"
	ControlSetSkillPolicy              = "skills.set_policy"
	ControlSetExtensionPolicy          = "extensions.set_policy"
	ControlSetMCPPolicy                = "mcp.set_policy"
	ControlSetMCPProfile               = "mcp.set_profile"
	ControlRemoveMCP                   = "mcp.remove"
	ControlRestartMCP                  = "mcp.restart"
	ControlSetKeyProfile               = "keys.set_profile"
	ControlRemoveKey                   = "keys.remove"
	ControlProbeKey                    = "keys.probe"
	ControlImportSkill                 = "skills.import"
	ControlPublishSkill                = "skills.publish"
	ControlRemoveSkill                 = "skills.remove"
	ControlImportExtension             = "extensions.import"
	ControlRemoveExtension             = "extensions.remove"
	ControlRestartExtension            = "extensions.restart"
	ControlClearExtensionData          = "extensions.clear_data"
	ControlInitExtensionStorage        = "extensions.init_storage"
	ControlChangeExtensionState        = "extensions.change_state"
	ControlSetConversationModel        = "conversation.set_model"
	ControlSetSpacePermission          = "space.set_permission"
	ControlRenameConversation          = "conversation.rename"
	ControlPinConversation             = "conversation.set_pinned"
	ControlSetConversationCapabilities = "conversation.set_capabilities"
	ControlSetMemorySelection          = "memory.set_selection"
	ControlRevert                      = "control.revert"
)

type ControlCatalogEntry struct {
	Kind            string          `json:"kind"`
	ResourceFamily  string          `json:"resource_family"`
	Risk            ControlRisk     `json:"risk"`
	Description     string          `json:"description"`
	ModelProposable bool            `json:"model_proposable"`
	Reversible      bool            `json:"reversible"`
	InputSchema     json.RawMessage `json:"input_schema,omitempty"`
}

type ControlOperation struct {
	RemotePrincipal   string                     `json:"remote_principal,omitempty"`
	ID                string                     `json:"operation_id"`
	AttemptID         string                     `json:"attempt_id,omitempty"`
	Kind              string                     `json:"kind"`
	Authorization     ControlAuthorizationSource `json:"authorization_source"`
	ConversationID    string                     `json:"conversation_id,omitempty"`
	ResourceIDs       []string                   `json:"resource_ids"`
	ExpectedRevisions map[string]uint64          `json:"expected_revisions"`
	NewRevisions      map[string]uint64          `json:"new_revisions,omitempty"`
	OldValues         map[string]string          `json:"old_values,omitempty"`
	ProposedValues    map[string]string          `json:"proposed_values,omitempty"`
	Risk              ControlRisk                `json:"risk"`
	Preview           string                     `json:"preview"`
	Digest            string                     `json:"digest,omitempty"`
	CreatedAt         string                     `json:"created_at"`
	ExpiresAt         string                     `json:"expires_at"`
	AppliedAt         string                     `json:"applied_at,omitempty"`
	Status            ControlOperationStatus     `json:"status"`
	RevertClass       string                     `json:"revert_class"`
	RevertOf          string                     `json:"revert_of,omitempty"`
	ErrorCode         string                     `json:"error_code,omitempty"`
	Recovery          string                     `json:"recovery,omitempty"`
}

type ControlPlanRequest struct {
	originRunID       string
	Kind              string                     `json:"kind"`
	Arguments         json.RawMessage            `json:"arguments"`
	Authorization     ControlAuthorizationSource `json:"authorization_source"`
	ConversationID    string                     `json:"conversation_id,omitempty"`
	ExpectedRevisions map[string]uint64          `json:"expected_revisions,omitempty"`
}

// KeyControlRequest is a user-only key operation. Secret is accepted only at
// this boundary, held in owner memory until apply, and never copied into a
// controlRecord or journal line.
type KeyControlRequest struct {
	ClearNote     bool
	Namespace     string
	Ref           string
	Input         KeyInput
	Remove        bool
	Force         bool
	DisableModels bool
	NewDefault    string
}

// MCPControlRequest carries a full MCP profile only in owner memory. The
// corresponding journal record contains the profile ID and operation kind,
// never command environment values or other MCP payloads.
type MCPControlRequest struct {
	ID     string
	Config MCPServerConfig
	Remove bool
}

// autoControlArguments keeps the Auto policy and its selection binding in one
// revision-checked control operation. The embedded fields preserve the flat
// JSON shape used by existing callers, while DefaultSelection makes
// `config auto --default` atomic with the policy change.
type autoControlArguments struct {
	AutoConfig
	DefaultSelection *string `json:"default_selection,omitempty"`
}

// InteractiveControlCapability is deliberately opaque to callers. It is
// issued in memory by an owner after a GUI gesture or interactive CLI step.
// It is never serialized, put in a URL or passed to a model.
type InteractiveControlCapability struct {
	token          string
	generation     string
	principal      string
	conversationID string
	expiresAt      time.Time
}

type ControlAuthorization struct {
	Source         ControlAuthorizationSource
	ConversationID string
	Principal      string
	Code           string
	Capability     InteractiveControlCapability
}

type ControlConfirmation struct {
	OperationID    string `json:"operation_id"`
	Code           string `json:"code"`
	ExpiresAt      string `json:"expires_at"`
	AttemptsLeft   int    `json:"attempts_left"`
	ConversationID string `json:"conversation_id,omitempty"`
	Preview        string `json:"preview"`
}

type ControlResult struct {
	Operation       ControlOperation `json:"operation"`
	Applied         bool             `json:"applied"`
	NextRunRequired bool             `json:"next_run_required"`
	Result          string           `json:"result"`
	ErrorCode       string           `json:"error_code,omitempty"`
	Recovery        string           `json:"recovery,omitempty"`
	Data            json.RawMessage  `json:"data,omitempty"`
}

type ControlConfigState struct {
	Channels       ChannelsConfig        `json:"channels"`
	PermissionMode string                `json:"permission_mode"`
	SandboxMode    string                `json:"sandbox_mode"`
	FileScope      string                `json:"file_scope"`
	NetworkMode    string                `json:"network_mode"`
	EnabledTools   BuiltinToolSettings   `json:"enabled_tools"`
	CommandReady   bool                  `json:"command_ready"`
	Commands       CommandReadiness      `json:"commands"`
	DefaultModel   string                `json:"default_model,omitempty"`
	Models         []ControlModelState   `json:"models,omitempty"`
	Workspace      string                `json:"workspace"`
	Auto           AutoConfig            `json:"auto"`
	HTTPEnabled    bool                  `json:"http_enabled"`
	HTTP           HTTPConfig            `json:"http"`
	Memory         MemoryConfig          `json:"memory"`
	Collaboration  CollaborationSettings `json:"collaboration"`
	Limits         map[string]int        `json:"limits"`
	EnabledSkills  []string              `json:"enabled_skills,omitempty"`
	EnabledExts    []string              `json:"enabled_extensions,omitempty"`
	EnabledMCP     []string              `json:"enabled_mcp_servers,omitempty"`
}

type ControlModelState struct {
	Key           string                `json:"key,omitempty"`
	Capabilities  ModelCapabilities     `json:"capabilities"`
	ReasoningInfo *ReasoningModelInfo   `json:"reasoning_info,omitempty"`
	Reasoning     ReasoningCapabilities `json:"reasoning,omitempty,omitzero"`
	OutputTokens  int                   `json:"output_tokens,omitempty"`
	ID            string                `json:"id"`
	Provider      string                `json:"provider"`
	Model         string                `json:"model"`
	Protocol      string                `json:"protocol"`
	KeyBound      bool                  `json:"key_bound"`
}

type modelProfileControlArguments struct {
	ID                 string                 `json:"id"`
	Provider           string                 `json:"provider,omitempty"`
	Key                string                 `json:"key,omitempty"`
	Model              string                 `json:"model,omitempty"`
	Capabilities       *ModelCapabilities     `json:"capabilities,omitempty"`
	MakeDefault        bool                   `json:"default,omitempty"`
	AuxiliaryReasoning *ReasoningSelection    `json:"auxiliary_reasoning,omitempty"`
	Reasoning          *ReasoningCapabilities `json:"reasoning,omitempty"`
}

type ControlState struct {
	ExtensionStateOperations map[string]map[string]ExtensionStateOperation `json:"extension_state_operations,omitempty"`
	ExtensionStates          map[string]json.RawMessage                    `json:"extension_states,omitempty"`
	TelegramRequests         []TelegramAccessRequest                       `json:"telegram_requests,omitempty"`
	CurrentRun               *RuntimeFacts                                 `json:"current_run,omitempty"`
	Spaces                   []ControlSpaceState                           `json:"spaces,omitempty"`
	Generation               string                                        `json:"generation"`
	ResourceRevisions        map[string]uint64                             `json:"resource_revisions"`
	Config                   ControlConfigState                            `json:"config"`
	Pending                  []ControlOperation                            `json:"pending,omitempty"`
	ExternalChangePending    bool                                          `json:"external_change_pending,omitempty"`
	ExternalChangeResources  []string                                      `json:"external_change_resources,omitempty"`
}

type ControlSpaceState struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Source     string         `json:"source"`
	Permission PermissionMode `json:"permission"`
}

// ReadControlStateForModel removes fields that are useful to a local UI but
// are unnecessary or too authority-like for a Provider response.
func (service *ControlService) ReadControlStateForModel(ctx context.Context) (ControlState, error) {
	state, err := service.ReadControlState(ctx)
	if err != nil {
		return ControlState{}, err
	}
	for index := range state.Pending {
		state.Pending[index].RemotePrincipal = ""
		state.Pending[index].AttemptID = ""
		state.Pending[index].ExpectedRevisions = nil
		state.Pending[index].NewRevisions = nil
		state.Pending[index].OldValues = nil
		state.Pending[index].ProposedValues = nil
		state.Pending[index].Digest = ""
		state.Pending[index].ExpiresAt = ""
	}
	return state, nil
}

type controlConfirmationState struct {
	operationID    string
	codeMAC        []byte
	code           string
	conversationID string
	expiresAt      time.Time
	attempts       int
}

type controlRecord struct {
	pendingMCP          *MCPServerConfig
	Operation           ControlOperation           `json:"operation"`
	Arguments           json.RawMessage            `json:"arguments"`
	Before              map[string]json.RawMessage `json:"before,omitempty"`
	After               map[string]json.RawMessage `json:"after,omitempty"`
	ResultData          json.RawMessage            `json:"result_data,omitempty"`
	ExtensionDispatched bool                       `json:"extension_dispatched,omitempty"`
}

type controlJournalLine struct {
	Action string        `json:"action"`
	Record controlRecord `json:"record"`
	Hash   string        `json:"hash"`
}

type controlJournalPayload struct {
	Action string        `json:"action"`
	Record controlRecord `json:"record"`
}

type ControlService struct {
	core           *Service
	mu             sync.Mutex
	ownerKey       []byte
	generation     string
	path           string
	operations     map[string]controlRecord
	revisions      map[string]uint64
	entries        []controlJournalLine
	confirmations  map[string]controlConfirmationState
	keySecrets     map[string][]byte
	mcpProfiles    map[string]MCPServerConfig
	gate           controlDecisionGate
	baselineMu     sync.Mutex
	baselineConfig string
	baselineKeys   string
	baselineDB     string
	externalChange bool
}

// controlDecisionGate serializes only the short control commit. Host command
// execution is wired to this gate by the command runner in a later package.
type controlDecisionGate struct {
	mu          sync.Mutex
	controlBusy bool
	hostBusy    int
	processBusy int
}

func (gate *controlDecisionGate) beginControl() error {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.hostBusy > 0 || gate.processBusy > 0 {
		return ErrControlBusy
	}
	if gate.controlBusy {
		return ErrControlBusy
	}
	gate.controlBusy = true
	return nil
}

func (gate *controlDecisionGate) endControl() {
	gate.mu.Lock()
	gate.controlBusy = false
	gate.mu.Unlock()
}
func (gate *controlDecisionGate) beginProcess() error {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.controlBusy {
		return ErrControlBusy
	}
	gate.processBusy++
	return nil
}
func (gate *controlDecisionGate) endProcess() {
	gate.mu.Lock()
	if gate.processBusy > 0 {
		gate.processBusy--
	}
	gate.mu.Unlock()
}

func (gate *controlDecisionGate) beginHost() error {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.controlBusy {
		return ErrControlBusy
	}
	gate.hostBusy++
	return nil
}

func (gate *controlDecisionGate) endHost() {
	gate.mu.Lock()
	if gate.hostBusy > 0 {
		gate.hostBusy--
	}
	gate.mu.Unlock()
}

func NewControlService(tasks *TaskService) (*ControlService, error) {
	if tasks == nil || tasks.core == nil || tasks.core.Store == nil {
		return nil, errors.New("harness: control service requires an open task service")
	}
	return controlServiceForCore(tasks.core)
}

func controlServiceForCore(core *Service) (*ControlService, error) {
	if core == nil || core.Store == nil {
		return nil, errors.New("harness: control service requires an open store")
	}
	controlServiceInitMu.Lock()
	if core.controlMu == nil {
		core.controlMu = &sync.Mutex{}
	}
	controlMu := core.controlMu
	controlServiceInitMu.Unlock()
	controlMu.Lock()
	defer controlMu.Unlock()
	if core.control != nil {
		return core.control, core.controlErr
	}
	service := &ControlService{core: core, path: filepath.Join(core.Store.DataDir(), controlJournalName),
		operations: map[string]controlRecord{}, revisions: map[string]uint64{}, confirmations: map[string]controlConfirmationState{},
		keySecrets: map[string][]byte{}, mcpProfiles: map[string]MCPServerConfig{}}
	service.ownerKey = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, service.ownerKey); err != nil {
		core.controlErr = err
		return nil, err
	}
	service.generation = newID()
	if err := service.loadJournal(); err != nil {
		core.controlErr = err
		return nil, err
	}
	if err := service.recoverAppliedProposals(); err != nil {
		core.controlErr = err
		return nil, err
	}
	if err := service.refreshBaseline(); err != nil {
		core.controlErr = err
		return nil, err
	}
	core.control = service
	return service, nil
}

func (service *ControlService) recoverAppliedProposals() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, record := range service.operations {
		if record.Operation.Status == ControlRejected || record.Operation.Status == ControlStale || record.Operation.Status == ControlFailed {
			if err := service.settleInvalidProposal(ctx, record); err != nil {
				return err
			}
		}
		if record.Operation.Status != ControlApplied || record.Operation.AttemptID == "" {
			continue
		}
		if record.Operation.Kind == ControlChangeExtensionState {
			if err := service.settleReconciledExtension(ctx, record); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			continue
		}
		attempt, err := service.core.Store.GetAttempt(ctx, record.Operation.AttemptID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if attempt.Status != "pending" {
			continue
		}
		run, err := service.core.Store.GetRun(ctx, attempt.RunID)
		if err != nil {
			return err
		}
		result := ToolResult{Content: "control change applied; it takes effect in the next Run"}
		if err := service.core.Store.settleControlProposal(ctx, run, attempt, result, true, "control_change_applied_next_run"); err != nil && !errors.Is(err, ErrControlAlreadyUsed) {
			return err
		}
	}
	return nil
}

func (service *Service) controlService() (*ControlService, error) {
	if service == nil {
		return nil, errors.New("harness: service is not configured")
	}
	return controlServiceForCore(service)
}

func (service *Service) Control() (*ControlService, error) {
	return service.controlService()
}

func (service *ControlService) refreshBaseline() error {
	if service == nil || service.core == nil || service.core.Store == nil {
		return errors.New("harness: control baseline is not configured")
	}
	configRaw, err := os.ReadFile(filepath.Join(service.core.Store.DataDir(), "config.json"))
	if err != nil {
		return err
	}
	keysHash, err := service.credentialBaseline()
	if err != nil {
		return err
	}
	var dataVersion int
	if err := service.core.Store.db.QueryRowContext(context.Background(), `SELECT (document->>'revision')::bigint FROM fa.meta WHERE key='core/external'`).Scan(&dataVersion); err != nil {
		return err
	}
	service.baselineMu.Lock()
	service.baselineConfig = contentHash(configRaw)
	service.baselineKeys = keysHash
	service.baselineDB = fmt.Sprint(dataVersion)
	service.externalChange = false
	service.baselineMu.Unlock()
	return nil
}

func (service *ControlService) detectExternalChange() error {
	if service == nil || service.core == nil || service.core.Store == nil {
		return nil
	}
	configRaw, err := os.ReadFile(filepath.Join(service.core.Store.DataDir(), "config.json"))
	if err != nil {
		return err
	}
	configHash := contentHash(configRaw)
	keysHash, err := service.credentialBaseline()
	if err != nil {
		return err
	}
	var dataVersion int
	if err := service.core.Store.db.QueryRowContext(context.Background(), `SELECT (document->>'revision')::bigint FROM fa.meta WHERE key='core/external'`).Scan(&dataVersion); err != nil {
		return err
	}
	service.baselineMu.Lock()
	defer service.baselineMu.Unlock()
	if service.externalChange || service.baselineConfig != configHash || service.baselineKeys != keysHash || service.baselineDB != fmt.Sprint(dataVersion) {
		service.externalChange = true
		return ErrExternalControlChange
	}
	return nil
}

func (service *ControlService) CheckExternalChanges(ctx context.Context) error {
	if ctx == nil {
		return errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return service.detectExternalChange()
}

func (service *ControlService) ReviewExternalChange(ctx context.Context) error {
	if ctx == nil {
		return errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if service.core != nil && service.core.Store != nil {
		config, err := LoadConfig(service.core.Store.DataDir())
		if err != nil {
			return err
		}
		service.core.setConfig(config)
		if service.core.toolManager != nil {
			service.core.toolManager.Replace(service.core.toolManager.Build(config))
		}
	}
	return service.refreshBaseline()
}

func (tasks *TaskService) Control() (*ControlService, error) {
	if tasks == nil || tasks.core == nil {
		return nil, errors.New("harness: task service is not configured")
	}
	return tasks.core.controlService()
}

func ControlOperationCatalog() []ControlCatalogEntry {
	entries := []ControlCatalogEntry{
		{Kind: ControlSetStickerSets, ResourceFamily: "telegram.sticker_sets", Risk: ControlRiskAuthority, Description: "Replace the exact space-scoped available Telegram sticker-set list (at most 32 names). Arguments: conversation_id, expected_revision, sets. Bound to the current Bot/chat and access grant; persistent confirmation is required. Removed collection references become invalid; received/explicit lookup references expire independently after 24h.", ModelProposable: true, Reversible: false, InputSchema: json.RawMessage(`{"type":"object","properties":{"conversation_id":{"type":"string"},"expected_revision":{"type":"integer","minimum":0},"sets":{"type":"array","maxItems":32,"items":{"type":"string"}}},"required":["conversation_id","expected_revision","sets"],"additionalProperties":false}`)},
		{Kind: ControlTelegramAccess, ResourceFamily: "telegram.access", Risk: ControlRiskAuthority, Description: "Set Telegram chat admission for a verified numeric Bot/chat and people. Grants chat only. Arguments: request_code or request_id (optional pending application; server resolves its bound identity), target {source:telegram,application_id,chat_id}, kind (private/group), enabled, whole_group, members (telegram:user:ID). Administrator confirmation is always required.", ModelProposable: true},
		{Kind: ControlSetAttachmentPolicy, ResourceFamily: "config.attachments", Risk: ControlRiskAuthority, Description: "Change bounded attachment input/export/storage limits for future uploads.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetPublicWeb, ResourceFamily: "config.public_web", Risk: ControlRiskAuthority, Description: "Configure optional Linux public HTTPS entrance, permission ceiling and bounded login security. Address/socket changes require restart. Raising the remote ceiling requires local confirmation; passwords use direct secure input.", ModelProposable: true, Reversible: true},
		{Kind: ControlDeleteAttachment, ResourceFamily: "attachment", Risk: ControlRiskDestructive, Description: "Permanently remove one exact attachment with its visible-reference impact. Requires its conversation, id and sha256.", ModelProposable: true, Reversible: false},
		{Kind: ControlSetServerPolicy, ResourceFamily: "config.server", Risk: ControlRiskAuthority, Description: "Set the numeric-loopback listener port (0 chooses an available port). Takes effect after an explicit Owner restart; does not change the OS service user or install a service.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetPermission, ResourceFamily: "config.execution", Risk: ControlRiskAuthority, Description: "Change the default permission mode for future Runs.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetExecutionProfile, ResourceFamily: "config.execution", Risk: ControlRiskAuthority, Description: "Change the complete bounded execution profile for future Runs.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetSandbox, ResourceFamily: "config.execution", Risk: ControlRiskAuthority, Description: "Change the default Windows sandbox mode for future Runs.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetNetwork, ResourceFamily: "config.execution", Risk: ControlRiskAuthority, Description: "Change the default network policy for future Runs.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetFileScope, ResourceFamily: "config.execution", Risk: ControlRiskAuthority, Description: "Change the bounded file scope and roots for future Runs.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetTool, ResourceFamily: "config.tools", Risk: ControlRiskAuthority, Description: "Enable or disable one built-in tool for future Runs.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetCommandPolicy, ResourceFamily: "config.commands", Risk: ControlRiskAuthority, Description: "Change command execution policy for future Runs.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetWorkspace, ResourceFamily: "config.workspace", Risk: ControlRiskAuthority, Description: "Change the selected workspace for future Runs.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetDefaultModel, ResourceFamily: "config.models", Risk: ControlRiskOrdinary, Description: "Select the default configured model for future Runs.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetModelProfile, ResourceFamily: "config.models", Risk: ControlRiskAuthority, Description: "Add or replace a full model profile using a saved Key reference. For an existing model, pass ONLY {id,auxiliary_reasoning:{mode:disabled}} (or a declared effort) to change compression/extension-analysis thinking, or ONLY {id,reasoning:{control,efforts,default,auxiliary}} to replace its reasoning declaration. Read current state first; preserve the other selections. For an existing model, ONLY {id,capabilities:{context_window_tokens,output_tokens,...}} patches the supplied top-level capability fields and preserves omitted settings; a supplied vision or reasoning object replaces that declaration ({} clears it). No Key is needed for these narrow changes. Unknown declared efforts are unverified; official exclusions still apply. Confirmation is required.", ModelProposable: true, Reversible: true,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"provider":{"type":"string"},"key":{"type":"string"},"model":{"type":"string"},"capabilities":{"type":"object"},"default":{"type":"boolean"},"reasoning":{"type":"object","properties":{"control":{"type":"string","enum":["none","openai","deepseek","anthropic"]},"efforts":{"type":"string","maxLength":256},"default":{"type":"object"},"auxiliary":{"type":"object"}},"additionalProperties":false},"auxiliary_reasoning":{"type":"object","properties":{"mode":{"type":"string","enum":["default","enabled","disabled"]},"effort":{"type":"string","maxLength":32}},"additionalProperties":false}},"required":["id"],"additionalProperties":false}`)},
		{Kind: ControlSetProviderEnabled, ResourceFamily: "config.providers", Risk: ControlRiskAuthority, Description: "Enable or disable a model Provider by saved Key profile ID. No network request is made.", ModelProposable: true, Reversible: true},
		{Kind: ControlRemoveModel, ResourceFamily: "config.models", Risk: ControlRiskDestructive, Description: "Remove a configured model profile and its future references.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetAutoPolicy, ResourceFamily: "config.auto", Risk: ControlRiskAuthority, Description: "Change bounded Auto model selection settings.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetHTTPPolicy, ResourceFamily: "config.http", Risk: ControlRiskAuthority, Description: "Change the bounded HTTP tool policy and origins.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetChannelPolicy, ResourceFamily: "config.channels", Risk: ControlRiskAuthority, Description: "Configure external connections using saved credentials. Telegram plain_text and disable_typing control formatting and private processing indicators; false enables each feature.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetDeliveryPolicy, ResourceFamily: "config.delivery", Risk: ControlRiskAuthority, Description: "Configure transport bounds and explicitly authorized, bounded extension follow-ups per conversation. Expression strategies belong to optional extensions. Active sending defaults off. Fields: sticker_limit 1..3; interval_ms 0..2000; enabled; rules with extension, conversation, timezone, quiet times, daily/cooldown limits, cancellation and expiry bounds.", ModelProposable: true, Reversible: true},
		{Kind: ControlScheduleDelivery, ResourceFamily: "delivery.schedule", Risk: ControlRiskAuthority, Description: "Schedule one explicitly confirmed fixed text or known sticker to the original Telegram conversation. Arguments: run_id and candidate {key,text or sticker_ref,after,expires,timezone,reason}. Absolute times include timezone; expires at most 24h after due time. No command/model runs at delivery time.", ModelProposable: true, Reversible: false},
		{Kind: ControlSetMemoryPolicy, ResourceFamily: "config.memory", Risk: ControlRiskAuthority, Description: "Change local memory and knowledge bounds.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetPromptPolicy, ResourceFamily: "config.prompt", Risk: ControlRiskAuthority, Description: "Change the bounded system prompt and instruction file selection for future Runs.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetCollaboration, ResourceFamily: "config.collaboration", Risk: ControlRiskAuthority, Description: "Change bounded collaboration authorization and budgets.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetRuntimeLimits, ResourceFamily: "config.limits", Risk: ControlRiskOrdinary, Description: "Change context and output bounds for future requests.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetSkillPolicy, ResourceFamily: "config.skills", Risk: ControlRiskAuthority, Description: "Enable or trust a named Skill without changing its content.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetExtensionPolicy, ResourceFamily: "config.extensions", Risk: ControlRiskAuthority, Description: "Enable, require or trust a named Extension without accepting secrets.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetMCPPolicy, ResourceFamily: "config.mcp", Risk: ControlRiskAuthority, Description: "Enable or change safe metadata for an existing MCP server.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetMCPProfile, ResourceFamily: "config.mcp", Risk: ControlRiskAuthority, Description: "Add or update an MCP profile without literal env secrets; use envFrom references. Exact user confirmation is required; a lost in-memory payload requires local re-entry.", ModelProposable: true, Reversible: false},
		{Kind: ControlRemoveMCP, ResourceFamily: "config.mcp", Risk: ControlRiskDestructive, Description: "Remove an MCP server profile and its future runtime binding.", ModelProposable: true, Reversible: false},
		{Kind: ControlRestartMCP, ResourceFamily: "config.mcp", Risk: ControlRiskAuthority, Description: "Restart the MCP runtime after an explicit user action.", ModelProposable: true, Reversible: false},
		{Kind: ControlSetKeyProfile, ResourceFamily: "keys.profile", Risk: ControlRiskAuthority, Description: "Create or update a Key profile without exposing its secret to the model or journal.", ModelProposable: true, Reversible: false},
		{Kind: ControlRemoveKey, ResourceFamily: "keys.profile", Risk: ControlRiskDestructive, Description: "Remove a Key profile when no configured model still references it.", ModelProposable: true, Reversible: false},
		{Kind: ControlProbeKey, ResourceFamily: "keys.profile", Risk: ControlRiskOrdinary, Description: "Probe one Key profile's models endpoint and record the bounded result.", ModelProposable: true, Reversible: false},
		{Kind: ControlImportSkill, ResourceFamily: "skills.catalog", Risk: ControlRiskAuthority, Description: "Import a validated Skill directory without enabling it.", ModelProposable: true, Reversible: false},
		{Kind: ControlPublishSkill, ResourceFamily: "skills.catalog", Risk: ControlRiskAuthority, Description: "Publish a bounded SKILL.md document only after exact confirmation. Arguments: id, document, expected_sha256 (empty only for a new Skill). No scripts are created or executed; existing enabled Skills must be disabled before replacement.", ModelProposable: true, Reversible: false},
		{Kind: ControlRemoveSkill, ResourceFamily: "skills.catalog", Risk: ControlRiskDestructive, Description: "Remove one user Skill and clear its enabled setting.", ModelProposable: true, Reversible: false},
		{Kind: ControlImportExtension, ResourceFamily: "extensions.catalog", Risk: ControlRiskAuthority, Description: "Import a validated Extension directory without enabling it.", ModelProposable: true, Reversible: false},
		{Kind: ControlRemoveExtension, ResourceFamily: "extensions.catalog", Risk: ControlRiskDestructive, Description: "Remove one user Extension and its local settings.", ModelProposable: true, Reversible: false},
		{Kind: ControlRestartExtension, ResourceFamily: "extensions.runtime", Risk: ControlRiskAuthority, Description: "Restart one Extension process through the owner runtime.", ModelProposable: true, Reversible: false},
		{Kind: ControlClearExtensionData, ResourceFamily: "extensions.data", Risk: ControlRiskDestructive, Description: "Clear one Extension data directory.", ModelProposable: true, Reversible: false},
		{Kind: ControlInitExtensionStorage, ResourceFamily: "extensions.storage", Risk: ControlRiskAuthority, Description: "Explicitly initialize one extension's separate PostgreSQL database and restricted role; no credentials accepted.", ModelProposable: true},
		{Kind: ControlChangeExtensionState, ResourceFamily: "extensions.state", Risk: ControlRiskOrdinary, Description: "Propose one extension state change. Requires extension ID, scope (space_id, resource, actor_id), declared action, expected_revision and bounded payload. The extension returns exact before/after; persistent changes require confirmation.", ModelProposable: true},
		{Kind: ControlSetConversationModel, ResourceFamily: "conversation.model", Risk: ControlRiskOrdinary, Description: "Select a configured model for one conversation.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetSpacePermission, ResourceFamily: "spaces", Risk: ControlRiskAuthority, Description: "Set a space's operation permission; affects all of its conversations. Full requires explicit local confirmation.", ModelProposable: true, Reversible: true},
		{Kind: ControlCreateSpace, ResourceFamily: "spaces", Risk: ControlRiskOrdinary, Description: "Create a local space with None permission.", ModelProposable: true},
		{Kind: ControlRenameSpace, ResourceFamily: "spaces", Risk: ControlRiskOrdinary, Description: "Rename a space.", ModelProposable: true},
		{Kind: ControlDeleteSpace, ResourceFamily: "spaces", Risk: ControlRiskDestructive, Description: "Delete a space, move its conversations to Local, and unpair its external chat.", ModelProposable: true},
		{Kind: ControlMoveConversation, ResourceFamily: "spaces", Risk: ControlRiskAuthority, Description: "Move a conversation; explicitly confirm any publication to an external space.", ModelProposable: true},
		{Kind: ControlSpaceDefault, ResourceFamily: "spaces", Risk: ControlRiskOrdinary, Description: "Choose a space's default conversation.", ModelProposable: true},
		{Kind: ControlSpaceActive, ResourceFamily: "spaces", Risk: ControlRiskOrdinary, Description: "Choose the external channel's active conversation.", ModelProposable: true},
		{Kind: ControlSpaceMulti, ResourceFamily: "spaces", Risk: ControlRiskOrdinary, Description: "Explicitly enable or disable multiple channel conversations.", ModelProposable: true},
		{Kind: ControlPersonPermission, ResourceFamily: "people", Risk: ControlRiskAuthority, Description: "Change a platform person's permission across connected spaces; local confirmation only.", ModelProposable: true},
		{Kind: ControlBranchConversation, ResourceFamily: "conversation.branch", Risk: ControlRiskOrdinary, Description: "Create an independent conversation branch at a verified user or completed assistant message. No filesystem rollback and no automatic branch execution.", ModelProposable: true},
		{Kind: ControlRenameConversation, ResourceFamily: "conversation.metadata", Risk: ControlRiskOrdinary, Description: "Rename one conversation.", ModelProposable: true, Reversible: true},
		{Kind: ControlPinConversation, ResourceFamily: "conversation.metadata", Risk: ControlRiskOrdinary, Description: "Pin or unpin one conversation.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetConversationCapabilities, ResourceFamily: "conversation.capabilities", Risk: ControlRiskAuthority, Description: "Select the Skills, MCP servers and Extensions available to one conversation.", ModelProposable: true, Reversible: true},
		{Kind: ControlSetMemoryPin, ResourceFamily: "memory.pin", Risk: ControlRiskAuthority, Description: "Pin or unpin an exact retained Memory revision and explicit scope; confirmation is always required.", ModelProposable: true, Reversible: false},
		{Kind: ControlSetMemorySelection, ResourceFamily: "conversation.memory", Risk: ControlRiskAuthority, Description: "Select bounded memory and knowledge inputs for one conversation's future Runs.", ModelProposable: true, Reversible: true},
	}
	for i := range entries {
		if len(entries[i].InputSchema) == 0 || entries[i].Kind == ControlSetModelProfile {
			entries[i].InputSchema = controlInputSchemas[entries[i].Kind]
		}
		if entries[i].Kind == ControlSetKeyProfile {
			entries[i].Description = "Update only name/note of an existing saved Key by id. Creating a Key, changing its endpoint or entering secrets requires the dedicated Provider form."
		}
		if entries[i].Kind == ControlSetRuntimeLimits {
			entries[i].Description += " task_summary_bytes: 0 disables automatic compaction; 1..16384 enables it and bounds the summary bytes. Existing summaries are retained."
			entries[i].InputSchema = json.RawMessage(`{"type":"object","properties":{"extension_max_context_chars":{"type":"integer","minimum":0,"maximum":1048576},"extension_max_model_calls_per_turn":{"type":"integer","minimum":0,"maximum":128},"model_retries":{"type":"integer","minimum":0,"maximum":6},"auto_compact_percent":{"type":"integer","minimum":50,"maximum":95},"task_summary_bytes":{"type":"integer","minimum":0,"maximum":16384,"description":"0 disables automatic compaction; positive values enable it with this UTF-8 byte cap"},"history_page_size":{"type":"integer","minimum":1,"maximum":1000},"max_context_bytes":{"type":"integer","minimum":4096,"maximum":16777216},"max_output_tokens":{"type":"integer","minimum":1,"maximum":32768},"max_tool_result_bytes":{"type":"integer","minimum":1,"maximum":16777216}},"additionalProperties":false}`)
		}
	}
	return entries
}

func (service *ControlService) ListControlOperations(ctx context.Context) ([]ControlOperation, error) {
	if service == nil {
		return nil, errors.New("harness: control service is not configured")
	}
	if ctx == nil {
		return nil, errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	result := make([]ControlOperation, 0, len(service.operations))
	if err := service.expireDirectLocked(time.Now()); err != nil {
		return nil, err
	}
	for _, record := range service.operations {
		result = append(result, cloneControlOperation(record.Operation))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt < result[j].CreatedAt })
	return result, nil
}

func (service *ControlService) operationForAttempt(attemptID string) (controlRecord, bool) {
	if service == nil || attemptID == "" {
		return controlRecord{}, false
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	for _, record := range service.operations {
		if record.Operation.AttemptID == attemptID && record.Operation.Status == ControlPending {
			return record, true
		}
	}
	return controlRecord{}, false
}

func (service *ControlService) bindAttempt(operationID, attemptID string) error {
	if service == nil || operationID == "" || attemptID == "" {
		return ErrControlOperationNotFound
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.operations[operationID]
	if !ok {
		return ErrControlOperationNotFound
	}
	if record.Operation.AttemptID != "" && record.Operation.AttemptID != attemptID {
		return ErrControlAlreadyUsed
	}
	record.Operation.AttemptID = attemptID
	service.operations[operationID] = record
	return service.appendLocked("bound", record)
}

// SettingsSnapshot binds editable values and their journal revisions under the
// same lock. A UI must submit these revisions, not revisions read at save time.
func (service *ControlService) SettingsSnapshot() (Config, map[string]uint64) {
	service.mu.Lock()
	defer service.mu.Unlock()
	revisions := cloneRevisions(service.revisions)
	return cloneConfig(service.core.currentConfig()), revisions
}

func (service *ControlService) ReadControlState(ctx context.Context) (ControlState, error) {
	if service == nil || service.core == nil {
		return ControlState{}, errors.New("harness: control service is not configured")
	}
	if ctx == nil {
		return ControlState{}, errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return ControlState{}, err
	}
	_ = service.detectExternalChange()
	spaces, err := service.core.Store.ListSpaces(ctx)
	if err != nil {
		return ControlState{}, err
	}
	spaceStates := make([]ControlSpaceState, 0, min(len(spaces), 100))
	for _, space := range spaces {
		if len(spaceStates) >= 100 {
			break
		}
		p, err := service.core.Store.spacePermission(ctx, space.ID)
		if err != nil {
			return ControlState{}, err
		}
		spaceStates = append(spaceStates, ControlSpaceState{space.ID, space.Title, space.Source, p.Mode})
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	config := service.core.currentConfig()
	if err := service.expireDirectLocked(time.Now()); err != nil {
		return ControlState{}, err
	}
	settings := effectiveExecutionSettings(config)
	profile, profileErr := executionProfileForConfig(config)
	if profileErr != nil {
		return ControlState{}, profileErr
	}
	commands := commandReadiness(profile, effectiveBuiltinToolSettings(config))
	models := make([]ControlModelState, 0, len(config.Models))
	for id, model := range config.Models {
		caps := model.EffectiveCapabilities()
		models = append(models, ControlModelState{Key: model.Key, Capabilities: caps, ID: id, Provider: model.Provider, Model: model.Model, Reasoning: model.Capabilities.Reasoning, ReasoningInfo: OfficialReasoning(model), OutputTokens: model.Capabilities.OutputTokens,
			Protocol: caps.Protocol, KeyBound: model.Key != ""})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	workspace := filepath.Base(filepath.Clean(config.Workspace))
	if workspace == "." || workspace == string(filepath.Separator) || workspace == "" {
		workspace = "workspace"
	}
	workspace = workspace + "#" + shortHash(config.Workspace)
	state := ControlState{Spaces: spaceStates, Generation: service.generation, ResourceRevisions: cloneRevisions(service.revisions),
		Config: ControlConfigState{Channels: config.Channels, PermissionMode: string(settings.PermissionMode), SandboxMode: string(settings.SandboxMode),
			FileScope: string(settings.FileScope), NetworkMode: string(settings.NetworkPolicy.Mode),
			EnabledTools: effectiveBuiltinToolSettings(config), CommandReady: commands.Status == "ready", Commands: commands,
			DefaultModel: config.DefaultModel, Models: models, Workspace: workspace, Auto: config.Auto, HTTPEnabled: config.HTTP.Enabled, HTTP: config.HTTP,
			Memory: config.Memory, Collaboration: redactCollaborationSettings(config.Collaboration), Limits: map[string]int{
				"extension_max_context_chars": config.Extensions.MaxContextChars, "extension_max_model_calls_per_turn": config.Extensions.MaxModelCallsPerTurn,
				"history_page_size": config.HistoryPageSize, "max_context_bytes": config.MaxContextBytes,
				"model_retries":     config.ModelRetries,
				"max_output_tokens": config.MaxOutputTokens, "max_tool_result_bytes": config.MaxToolResult,
				"auto_compact_percent": autoCompactPercent(config), "task_summary_bytes": config.TaskSummaryBytes}, EnabledSkills: enabledMapKeys(config.Skills.Enabled), EnabledExts: enabledMapKeys(config.Extensions.Enabled), EnabledMCP: enabledMCPNames(config.MCPServers)}}
	service.baselineMu.Lock()
	state.ExternalChangePending = service.externalChange
	if state.ExternalChangePending {
		state.ExternalChangeResources = []string{"config.json", "keys.json", "PostgreSQL"}
	}
	service.baselineMu.Unlock()
	for _, record := range service.operations {
		if record.Operation.Status == ControlPending {
			state.Pending = append(state.Pending, cloneControlOperation(record.Operation))
		}
	}
	sort.Slice(state.Pending, func(i, j int) bool { return state.Pending[i].CreatedAt < state.Pending[j].CreatedAt })
	state.TelegramRequests = NewTaskService(service.core).TelegramAccessRequests()
	return state, nil
}

func enabledMapKeys(values map[string]bool) []string {
	result := make([]string, 0)
	for key, enabled := range values {
		if enabled {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func enabledMCPNames(values map[string]MCPServerConfig) []string {
	result := make([]string, 0)
	for key, server := range values {
		if server.Enabled {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func redactCollaborationSettings(settings CollaborationSettings) CollaborationSettings {
	redacted := settings
	redacted.Paths = make([]string, len(settings.Paths))
	for index, path := range settings.Paths {
		redacted.Paths[index] = filepath.Base(filepath.Clean(path)) + "#" + shortHash(path)
	}
	return redacted
}

func (service *ControlService) IssueInteractiveCapability(ctx context.Context, conversationID, principal string) (InteractiveControlCapability, error) {
	if service == nil {
		return InteractiveControlCapability{}, errors.New("harness: control service is not configured")
	}
	if strings.TrimSpace(principal) == "" || len(principal) > 256 || strings.ContainsAny(principal, "\x00\r\n") {
		return InteractiveControlCapability{}, ErrControlUserInteractionRequired
	}
	if ctx == nil {
		return InteractiveControlCapability{}, errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return InteractiveControlCapability{}, err
	}
	return InteractiveControlCapability{token: newID(), generation: service.generation, principal: principal,
		conversationID: conversationID, expiresAt: time.Now().UTC().Add(controlConfirmationTTL)}, nil
}

func (service *ControlService) PlanControlChange(ctx context.Context, request ControlPlanRequest) (ControlOperation, error) {
	if service == nil || service.core == nil {
		return ControlOperation{}, errors.New("harness: control service is not configured")
	}
	if ctx == nil {
		return ControlOperation{}, errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return ControlOperation{}, err
	}
	if err := service.detectExternalChange(); err != nil {
		return ControlOperation{}, err
	}
	if request.Authorization != ControlUserDirect && request.Authorization != ControlModelProposed && request.Authorization != ControlSystemRecovery {
		return ControlOperation{}, errors.New("harness: invalid control authorization source")
	}
	if request.Authorization == ControlModelProposed && strings.TrimSpace(request.ConversationID) == "" {
		return ControlOperation{}, errors.New("harness: model control proposals require a conversation")
	}
	// Platform normalization can wait for a pure channel preparation result.
	// Never hold the control journal lock while its outbox worker is running.
	if request.Kind == ControlSetStickerSets {
		var args stickerSetsArguments
		if err := decodeControlJSON(request.Arguments, &args); err != nil {
			return ControlOperation{}, err
		}
		if args.ConversationID == "" {
			args.ConversationID = request.ConversationID
		}
		_, target, err := NewTaskService(service.core).stickerSpace(ctx, args.ConversationID)
		if err != nil {
			return ControlOperation{}, err
		}
		if args.Sets == nil || len(args.Sets) > maxSpaceStickerSets {
			return ControlOperation{}, ErrRequestKey
		}
		for i, raw := range args.Sets {
			name, err := service.core.Store.channelBridge.Load().prepareResource(ctx, target, raw)
			if err != nil {
				return ControlOperation{}, err
			}
			args.Sets[i] = name
		}
		request.Arguments = mustJSON(args)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	record, err := service.buildRecordLocked(ctx, request)
	if err != nil {
		return ControlOperation{}, err
	}
	for resource, expected := range record.Operation.ExpectedRevisions {
		if current := service.revisionLocked(resource); current != expected {
			return ControlOperation{}, ErrControlStaleRevision
		}
	}
	record.Operation.ID = newID()
	if entry, remote := PublicEntryFrom(ctx); remote {
		record.Operation.RemotePrincipal = entry.Principal
	}
	record.Operation.CreatedAt = nowText()
	record.Operation.ExpiresAt = time.Now().UTC().Add(controlConfirmationTTL).Format(time.RFC3339Nano)
	record.Operation.Status = ControlPending
	record.Operation.Digest = controlDigest(record)
	if record.Operation.Risk == ControlRiskIrreversible {
		record.Operation.RevertClass = "none"
	} else if record.Operation.RevertClass == "" {
		record.Operation.RevertClass = "exact_snapshot"
	}
	if record.pendingMCP != nil {
		service.mcpProfiles[record.Operation.ID] = cloneMCPServerConfig(*record.pendingMCP)
		record.pendingMCP = nil
	}
	service.operations[record.Operation.ID] = record
	if err := service.appendLocked("planned", record); err != nil {
		delete(service.operations, record.Operation.ID)
		service.clearMCPProfileLocked(record.Operation.ID)
		return ControlOperation{}, err
	}
	return cloneControlOperation(record.Operation), nil
}

// PlanKeyProfileChange creates a user-only Key profile operation. The public
// operation record contains only redacted profile metadata; a supplied secret
// is bound to the operation in memory and is cleared after apply or failure.
func (service *ControlService) PlanKeyProfileChange(ctx context.Context, request KeyControlRequest) (ControlOperation, error) {
	defer clear(request.Input.Secret)
	if service == nil || service.core == nil || service.core.Keys == nil {
		return ControlOperation{}, errors.New("harness: key control service is not configured")
	}
	if ctx == nil {
		return ControlOperation{}, errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return ControlOperation{}, err
	}
	if err := service.detectExternalChange(); err != nil {
		return ControlOperation{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	keys := service.core.Keys
	resource := "keys.profiles"
	if request.Namespace == "channels" {
		keys = NewTaskService(service.core).ChannelKeys()
		resource = "channel.keys"
	} else if request.Namespace != "" {
		return ControlOperation{}, ErrPermissionDenied
	}
	views, err := keys.List()
	if err != nil {
		return ControlOperation{}, err
	}
	var current KeyProfileView
	if request.Ref != "" {
		current, err = keys.Find(request.Ref)
		if err != nil {
			return ControlOperation{}, err
		}
	}
	kind := ControlSetKeyProfile
	if request.Remove {
		kind = ControlRemoveKey
		if current.ID == "" {
			return ControlOperation{}, ErrKeyNotFound
		}
		users := keyModelUsers(service.core.currentConfig(), current.ID)
		if request.Namespace == "channels" {
			users = nil
			for _, connection := range service.core.currentConfig().Channels.Connections {
				if connection.Credential == current.ID {
					return ControlOperation{}, ErrControlBusy
				}
			}
		}
		if len(users) != 0 && !request.Force && !request.DisableModels {
			return ControlOperation{}, ErrControlBusy
		}
		args := mustJSON(map[string]any{"namespace": request.Namespace, "id": current.ID, "remove": true, "force": request.Force,
			"disable_models": request.DisableModels, "new_default": request.NewDefault})
		operation := ControlOperation{Kind: kind, Authorization: ControlUserDirect, ResourceIDs: []string{resource},
			ExpectedRevisions: map[string]uint64{resource: service.revisionLocked(resource)}, Risk: ControlRiskDestructive,
			Preview: fmt.Sprintf("Remove Key profile %s%s.", redactID(current.ID), map[bool]string{true: " and its referencing models", false: ""}[request.DisableModels]), RevertClass: "none"}
		record := controlRecord{Operation: operation, Arguments: args,
			Before: map[string]json.RawMessage{resource: mustJSON(views)}, After: map[string]json.RawMessage{resource: mustJSON(removeKeyView(views, current.ID))}}
		if request.Namespace == "" {
			if err := service.bindKeyRemovalModels(&record, current.ID, request.DisableModels, request.NewDefault); err != nil {
				return ControlOperation{}, err
			}
			record.Operation.Preview += fmt.Sprintf(" Models referencing this credential: %v.", users)
		}
		return service.persistKeyPlanLocked(record, nil)
	}

	input := request.Input
	if current.ID != "" {
		if input.Name == "" {
			input.Name = current.Name
		}
		if input.BaseURL == "" {
			input.BaseURL = current.BaseURL
		}
		if input.Note == "" && !request.ClearNote {
			input.Note = current.Note
		}
		if input.Protocol == "" {
			input.Protocol = current.Protocol
		}
		if input.Source == "" {
			input.Source = current.Source
			input.EnvName = current.EnvName
		}
	}
	name, base, note, err := keyFields(input)
	if err != nil {
		return ControlOperation{}, err
	}
	protocol := normalizeKeyProtocol(input.Protocol)
	if !keys.accepts(protocol) {
		return ControlOperation{}, ErrPermissionDenied
	}
	if input.Source == KeySourceEnv && !validEnvName(input.EnvName) {
		return ControlOperation{}, errors.New("harness: invalid environment variable name")
	}
	if input.Source == KeySourceNone && !loopbackBaseURL(base) {
		return ControlOperation{}, errors.New("harness: a profile without a key must use a loopback address")
	}
	if current.ID == "" && input.Source == "" {
		return ControlOperation{}, errors.New("harness: key source must be saved, env, or none")
	}
	if current.ID == "" && input.Source == KeySourceSaved && len(input.Secret) == 0 {
		return ControlOperation{}, errors.New("harness: enter the key to save")
	}
	if input.Source == KeySourceSaved && len(input.Secret) != 0 && !validSecret(input.Secret) {
		return ControlOperation{}, errInvalidSecret
	}
	if nameTakenViews(views, name, current.ID) {
		return ControlOperation{}, ErrKeyNameExists
	}
	args := mustJSON(map[string]any{"namespace": request.Namespace, "id": current.ID, "new": current.ID == "", "name": name, "base_url": base,
		"protocol": protocol, "note": note, "source": input.Source, "env_name": input.EnvName})
	after := replaceKeyView(views, current.ID, KeyProfileView{ID: current.ID, Name: name, BaseURL: base, Protocol: protocol,
		Note: note, Source: input.Source, EnvName: input.EnvName})
	if current.ID == "" {
		after = append(after, KeyProfileView{Name: name, BaseURL: base, Protocol: protocol, Note: note, Source: input.Source, EnvName: input.EnvName})
	}
	operation := ControlOperation{Kind: kind, Authorization: ControlUserDirect, ResourceIDs: []string{resource},
		ExpectedRevisions: map[string]uint64{resource: service.revisionLocked(resource)}, Risk: ControlRiskAuthority,
		Preview: fmt.Sprintf("Save Key profile %s at %s using protocol %s. The secret is not shown.", name, base, protocol), RevertClass: "none"}
	record := controlRecord{Operation: operation, Arguments: args,
		Before: map[string]json.RawMessage{resource: mustJSON(views)}, After: map[string]json.RawMessage{resource: mustJSON(after)}}
	secret := append([]byte(nil), input.Secret...)
	clear(input.Secret)
	return service.persistKeyPlanLocked(record, secret)
}

// PlanKeyProbe creates the typed, owner-serialized operation used by both the
// GUI and CLI for a saved profile test/model discovery. The network request is
// performed only during apply, after the same user confirmation boundary as
// every other control operation.
func (service *ControlService) PlanKeyProbe(ctx context.Context, ref string) (ControlOperation, error) {
	if service == nil || service.core == nil || service.core.Keys == nil {
		return ControlOperation{}, errors.New("harness: key control service is not configured")
	}
	if ctx == nil {
		return ControlOperation{}, errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return ControlOperation{}, err
	}
	if err := service.detectExternalChange(); err != nil {
		return ControlOperation{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	profile, err := service.core.Keys.Find(ref)
	if err != nil {
		return ControlOperation{}, err
	}
	resource := "keys.profiles"
	args := mustJSON(map[string]string{"id": profile.ID})
	record := controlRecord{Operation: ControlOperation{Kind: ControlProbeKey,
		Authorization: ControlUserDirect, ResourceIDs: []string{resource},
		ExpectedRevisions: map[string]uint64{resource: service.revisionLocked(resource)},
		Risk:              ControlRiskOrdinary, Preview: fmt.Sprintf("Test Key profile %s and record the provider response category. No settings will be changed.", redactID(profile.ID)), RevertClass: "none"},
		Arguments: args}
	return service.persistKeyPlanLocked(record, nil)
}

// ProbeKey applies a typed Key probe for a trusted interactive caller and
// returns only the bounded provider result. The secret is resolved inside the
// owner and never leaves this process.
func (service *ControlService) ProbeKey(ctx context.Context, ref, principal string) (ProbeResult, error) {
	operation, err := service.PlanKeyProbe(ctx, ref)
	if err != nil {
		return ProbeResult{}, err
	}
	capability, err := service.IssueInteractiveCapability(ctx, "", principal)
	if err != nil {
		return ProbeResult{}, err
	}
	confirmation, err := service.RenderConfirmation(ctx, operation.ID, "", principal)
	if err != nil {
		return ProbeResult{}, err
	}
	result, err := service.ConfirmControlChange(ctx, operation.ID, ControlAuthorization{
		Source: ControlUserDirect, Principal: principal, Code: confirmation.Code, Capability: capability,
	})
	if err != nil {
		return ProbeResult{}, err
	}
	var probe ProbeResult
	if err := json.Unmarshal(result.Data, &probe); err != nil {
		return ProbeResult{}, errors.New("harness: key probe result is invalid")
	}
	return probe, nil
}

// ApplyMemorySelection is the direct user path for changing a conversation's
// future memory inputs. It uses the same journal, revision and confirmation
// checks as a model proposal while keeping the selection body local.
func (service *ControlService) ApplyMemorySelection(ctx context.Context, conversationID string, selection MemorySelection, principal string) error {
	arguments := mustJSON(map[string]any{"conversation_id": conversationID, "selection": selection})
	operation, err := service.PlanControlChange(ctx, ControlPlanRequest{Kind: ControlSetMemorySelection,
		Arguments: arguments, Authorization: ControlUserDirect, ConversationID: conversationID})
	if err != nil {
		return err
	}
	capability, err := service.IssueInteractiveCapability(ctx, conversationID, principal)
	if err != nil {
		return err
	}
	confirmation, err := service.RenderConfirmation(ctx, operation.ID, conversationID, principal)
	if err != nil {
		return err
	}
	_, err = service.ConfirmControlChange(ctx, operation.ID, ControlAuthorization{Source: ControlUserDirect,
		ConversationID: conversationID, Principal: principal, Code: confirmation.Code, Capability: capability})
	return err
}

// ApplyDirectControlChange is the shared user-direct adapter for local CLI
// and GUI actions that already have a typed operation kind. It deliberately
// still creates a confirmation and consumes the owner capability; callers do
// not get a second persistence path by using this convenience method.
func (service *ControlService) ApplyDirectControlChange(ctx context.Context, kind string, arguments any, conversationID, principal string) (ControlResult, error) {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return ControlResult{}, err
	}
	operation, err := service.PlanControlChange(ctx, ControlPlanRequest{Kind: kind, Arguments: raw,
		Authorization: ControlUserDirect, ConversationID: conversationID})
	if err != nil {
		return ControlResult{}, err
	}
	capability, err := service.IssueInteractiveCapability(ctx, conversationID, principal)
	if err != nil {
		return ControlResult{}, err
	}
	confirmation, err := service.RenderConfirmation(ctx, operation.ID, conversationID, principal)
	if err != nil {
		return ControlResult{}, err
	}
	return service.ConfirmControlChange(ctx, operation.ID, ControlAuthorization{Source: ControlUserDirect,
		ConversationID: conversationID, Principal: principal, Code: confirmation.Code, Capability: capability})
}

// PlanMCPProfileChange keeps the complete MCP payload in owner memory. The
// journal stores only the profile ID and safe metadata, never command
// environment values or other MCP payloads.
func (service *ControlService) PlanMCPProfileChange(ctx context.Context, request MCPControlRequest) (ControlOperation, error) {
	if service == nil || service.core == nil {
		return ControlOperation{}, errors.New("harness: MCP control service is not configured")
	}
	if ctx == nil {
		return ControlOperation{}, errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return ControlOperation{}, err
	}
	if err := service.detectExternalChange(); err != nil {
		return ControlOperation{}, err
	}
	id := strings.TrimSpace(request.ID)
	if !validMCPControlID(id) {
		return ControlOperation{}, errors.New("harness: invalid MCP server identifier")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	config := cloneConfig(service.core.currentConfig())
	if config.MCPServers == nil {
		config.MCPServers = map[string]MCPServerConfig{}
	}
	current, exists := config.MCPServers[id]
	if request.Remove {
		if !exists {
			return ControlOperation{}, errors.New("harness: MCP server not found")
		}
		delete(config.MCPServers, id)
	} else {
		config.MCPServers[id] = cloneMCPServerConfig(request.Config)
	}
	if err := config.Validate(); err != nil {
		return ControlOperation{}, err
	}
	resource := "config.mcp:" + id
	before := mcpControlValue(id, current, exists)
	after := mcpControlValue(id, config.MCPServers[id], !request.Remove)
	kind := ControlSetMCPProfile
	risk := ControlRiskAuthority
	preview := fmt.Sprintf("Save MCP server profile %s without exposing its command environment.", redactID(id))
	if request.Remove {
		kind, risk, preview = ControlRemoveMCP, ControlRiskDestructive, fmt.Sprintf("Remove MCP server profile %s.", redactID(id))
	}
	record := controlRecord{Operation: ControlOperation{Kind: kind, Authorization: ControlUserDirect,
		ResourceIDs: []string{resource}, ExpectedRevisions: map[string]uint64{resource: service.revisionLocked(resource)},
		Risk: risk, Preview: preview, RevertClass: "none"}, Arguments: mustJSON(map[string]any{"id": id, "remove": request.Remove}),
		Before: map[string]json.RawMessage{resource: mustJSON(before)}, After: map[string]json.RawMessage{resource: mustJSON(after)}}
	record.Operation.OldValues = redactedValues(record.Before)
	record.Operation.ProposedValues = redactedValues(record.After)
	record.Operation.ID = newID()
	record.Operation.CreatedAt = nowText()
	record.Operation.ExpiresAt = time.Now().UTC().Add(controlConfirmationTTL).Format(time.RFC3339Nano)
	record.Operation.Status = ControlPending
	record.Operation.Digest = controlDigest(record)
	if !request.Remove {
		service.mcpProfiles[record.Operation.ID] = cloneMCPServerConfig(request.Config)
	}
	service.operations[record.Operation.ID] = record
	if err := service.appendLocked("planned", record); err != nil {
		delete(service.operations, record.Operation.ID)
		service.clearMCPProfileLocked(record.Operation.ID)
		return ControlOperation{}, err
	}
	return cloneControlOperation(record.Operation), nil
}

// ReenterMCPProfile restores the non-journaled payload for a pending MCP
// profile operation after an owner restart. It does not create a new
// operation, change its target or apply anything; the caller still performs
// the ordinary confirmation step on the unchanged operation.
func (service *ControlService) ReenterMCPProfile(ctx context.Context, operationID string, request MCPControlRequest) (ControlOperation, error) {
	if service == nil || service.core == nil {
		return ControlOperation{}, errors.New("harness: MCP control service is not configured")
	}
	if ctx == nil {
		return ControlOperation{}, errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return ControlOperation{}, err
	}
	if err := service.detectExternalChange(); err != nil {
		return ControlOperation{}, err
	}
	id := strings.TrimSpace(request.ID)
	if !validMCPControlID(id) || request.Remove {
		return ControlOperation{}, errors.New("harness: invalid MCP re-entry request")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.operations[operationID]
	if !ok {
		return ControlOperation{}, ErrControlOperationNotFound
	}
	if record.Operation.Status != ControlPending || record.Operation.Kind != ControlSetMCPProfile {
		return ControlOperation{}, ErrControlAlreadyUsed
	}
	var args struct {
		ID     string `json:"id"`
		Remove bool   `json:"remove"`
	}
	if err := decodeControlJSON(record.Arguments, &args); err != nil || args.Remove || args.ID != id {
		return ControlOperation{}, errors.New("harness: MCP re-entry target changed")
	}
	candidate := cloneConfig(service.core.currentConfig())
	if candidate.MCPServers == nil {
		candidate.MCPServers = map[string]MCPServerConfig{}
	}
	candidate.MCPServers[id] = cloneMCPServerConfig(request.Config)
	if err := candidate.Validate(); err != nil {
		return ControlOperation{}, err
	}
	service.mcpProfiles[operationID] = cloneMCPServerConfig(request.Config)
	if err := service.appendLocked("reentered", record); err != nil {
		service.clearMCPProfileLocked(operationID)
		return ControlOperation{}, err
	}
	return cloneControlOperation(record.Operation), nil
}

func validMCPControlID(id string) bool {
	return id != "" && len(id) <= 64 && !hasControl(id) && !strings.ContainsAny(id, `/\\`)
}

func cloneMCPServerConfig(config MCPServerConfig) MCPServerConfig {
	clone := config
	clone.Args = append([]string(nil), config.Args...)
	clone.HiddenTools = append([]string(nil), config.HiddenTools...)
	clone.Env = cloneStringMap(config.Env)
	clone.EnvFrom = cloneStringMap(config.EnvFrom)
	return clone
}

func keyModelUsers(config Config, keyID string) []string {
	users := make([]string, 0)
	for id, model := range config.Models {
		if model.Key == keyID {
			users = append(users, id)
		}
	}
	sort.Strings(users)
	return users
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func mcpControlValue(id string, config MCPServerConfig, exists bool) any {
	if !exists {
		return nil
	}
	return map[string]any{"id": id, "enabled": config.Enabled, "command": config.Command,
		"profile_hash": contentHash(mustJSON(config)),
		"cwd":          config.Cwd, "arg_count": len(config.Args), "env_names": sortedStringKeys(config.Env),
		"env_from": sortedStringKeys(config.EnvFrom), "hidden_tools": config.HiddenTools,
		"trust_read_only_hint": config.TrustReadOnlyHint}
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (service *ControlService) clearMCPProfileLocked(operationID string) {
	if service == nil || service.mcpProfiles == nil {
		return
	}
	delete(service.mcpProfiles, operationID)
}

func (service *ControlService) persistKeyPlanLocked(record controlRecord, secret []byte) (ControlOperation, error) {
	record.Operation.ID = newID()
	record.Operation.CreatedAt = nowText()
	record.Operation.ExpiresAt = time.Now().UTC().Add(controlConfirmationTTL).Format(time.RFC3339Nano)
	record.Operation.Status = ControlPending
	record.Operation.Digest = controlDigest(record)
	service.operations[record.Operation.ID] = record
	if len(secret) != 0 {
		service.keySecrets[record.Operation.ID] = secret
	}
	if err := service.appendLocked("planned", record); err != nil {
		delete(service.operations, record.Operation.ID)
		clear(service.keySecrets[record.Operation.ID])
		delete(service.keySecrets, record.Operation.ID)
		return ControlOperation{}, err
	}
	return cloneControlOperation(record.Operation), nil
}

func nameTakenViews(views []KeyProfileView, name, exceptID string) bool {
	for _, view := range views {
		if view.ID != exceptID && strings.EqualFold(view.Name, name) {
			return true
		}
	}
	return false
}

func removeKeyView(views []KeyProfileView, id string) []KeyProfileView {
	result := make([]KeyProfileView, 0, len(views))
	for _, view := range views {
		if view.ID != id {
			result = append(result, view)
		}
	}
	return result
}

func replaceKeyView(views []KeyProfileView, id string, replacement KeyProfileView) []KeyProfileView {
	result := append([]KeyProfileView(nil), views...)
	for index := range result {
		if result[index].ID == id {
			replacement.ID = id
			result[index] = replacement
			return result
		}
	}
	return result
}

func (service *ControlService) RenderConfirmation(ctx context.Context, operationID, conversationID, principal string) (ControlConfirmation, error) {
	if service == nil {
		return ControlConfirmation{}, errors.New("harness: control service is not configured")
	}
	if ctx == nil {
		return ControlConfirmation{}, errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return ControlConfirmation{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.operations[operationID]
	if !ok {
		return ControlConfirmation{}, ErrControlOperationNotFound
	}
	if err := service.expireDirectLocked(time.Now()); err != nil {
		return ControlConfirmation{}, err
	}
	record = service.operations[operationID]
	if record.Operation.ErrorCode == "expired" {
		return ControlConfirmation{}, ErrControlExpired
	}
	if entry, remote := PublicEntryFrom(ctx); remote && record.Operation.RemotePrincipal != "" && entry.Principal != record.Operation.RemotePrincipal {
		previous, exists := service.confirmations[operationID]
		if record.Operation.AttemptID == "" || record.Operation.Authorization != ControlModelProposed || exists && time.Now().Before(previous.expiresAt) {
			return ControlConfirmation{}, ErrControlUserInteractionRequired
		}
		attempt, err := service.core.Store.GetAttempt(ctx, record.Operation.AttemptID)
		if err != nil || attempt.Status != "pending" {
			return ControlConfirmation{}, ErrControlAlreadyUsed
		}
		run, err := service.core.Store.GetRun(ctx, attempt.RunID)
		if err != nil || run.Status != "waiting_confirmation" || run.ConversationID != record.Operation.ConversationID {
			return ControlConfirmation{}, ErrControlAlreadyUsed
		}
		record.Operation.RemotePrincipal = entry.Principal
		service.operations[operationID] = record
		delete(service.confirmations, operationID)
		if err := service.appendLocked("surface_rebound", record); err != nil {
			return ControlConfirmation{}, err
		}
	}
	if entry, remote := PublicEntryFrom(ctx); remote && record.Operation.RemotePrincipal == "" {
		record.Operation.RemotePrincipal = entry.Principal
		service.operations[operationID] = record
		if err := service.appendLocked("surface_bound", record); err != nil {
			return ControlConfirmation{}, err
		}
	}
	if record.Operation.Status != ControlPending {
		return ControlConfirmation{}, ErrControlAlreadyUsed
	}
	for resource, expected := range record.Operation.ExpectedRevisions {
		if service.revisionLocked(resource) != expected {
			if err := service.invalidateProposalLocked(ctx, &record, "stale_revision"); err != nil {
				return ControlConfirmation{}, err
			}
			return ControlConfirmation{}, ErrControlStaleRevision
		}
	}
	if conversationID == "" {
		conversationID = record.Operation.ConversationID
	}
	if conversationID != record.Operation.ConversationID || strings.TrimSpace(principal) == "" {
		return ControlConfirmation{}, ErrControlUserInteractionRequired
	}
	state, exists := service.confirmations[operationID]
	now := time.Now().UTC()
	// One code epoch serves every authenticated surface of this owner, so a
	// render on another surface shows the same code instead of replacing it.
	if !exists || state.conversationID != conversationID || now.After(state.expiresAt) || state.attempts >= controlMaxCodeAttempts {
		if err := service.newControlCodeLocked(operationID, conversationID, now); err != nil {
			return ControlConfirmation{}, err
		}
		state = service.confirmations[operationID]
		record.Operation.ExpiresAt = state.expiresAt.Format(time.RFC3339Nano)
		service.operations[operationID] = record
		if err := service.appendLocked("reissued", record); err != nil {
			return ControlConfirmation{}, err
		}
	}
	return ControlConfirmation{OperationID: operationID, Code: state.code,
		ExpiresAt: state.expiresAt.Format(time.RFC3339Nano), AttemptsLeft: max(0, controlMaxCodeAttempts-state.attempts),
		ConversationID: conversationID, Preview: record.Operation.Preview}, nil
}

func (service *ControlService) withdrawProposal(operationID, code string) error {
	if service == nil {
		return nil
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.operations[operationID]
	if !ok || record.Operation.Status != ControlPending {
		return nil
	}
	record.Operation.Status = ControlRejected
	record.Operation.ErrorCode = code
	service.operations[operationID] = record
	delete(service.confirmations, operationID)
	service.clearKeySecretLocked(operationID)
	return service.appendLocked("withdrawn", record)
}

func (service *ControlService) ConfirmControlChange(ctx context.Context, operationID string, auth ControlAuthorization) (ControlResult, error) {
	return service.decideControlChange(ctx, operationID, auth, true, false)
}

// ApplyControlChange is the named core entry point for direct UI/CLI changes.
// ConfirmControlChange is an alias with the same checks for model proposals.
func (service *ControlService) ApplyControlChange(ctx context.Context, operationID string, auth ControlAuthorization) (ControlResult, error) {
	return service.decideControlChange(ctx, operationID, auth, true, false)
}

// confirmRunProposal applies a model proposal for TaskService.ConfirmControl,
// which settles the proposing Run's Attempt in the same decision.
func (service *ControlService) confirmRunProposal(ctx context.Context, operationID string, auth ControlAuthorization) (ControlResult, error) {
	return service.decideControlChange(ctx, operationID, auth, true, true)
}

func (service *ControlService) RejectControlChange(ctx context.Context, operationID string, auth ControlAuthorization) (ControlResult, error) {
	return service.rejectControlChange(ctx, operationID, auth, false)
}

func (service *ControlService) rejectControlChange(ctx context.Context, operationID string, auth ControlAuthorization, fromRun bool) (ControlResult, error) {
	if service == nil {
		return ControlResult{}, errors.New("harness: control service is not configured")
	}
	if ctx == nil {
		return ControlResult{}, errors.New("harness: control context is required")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	record, ok := service.operations[operationID]
	if !ok {
		return ControlResult{}, ErrControlOperationNotFound
	}
	if record.Operation.Status != ControlPending {
		return ControlResult{Operation: cloneControlOperation(record.Operation)}, ErrControlAlreadyUsed
	}
	if entry, remote := PublicEntryFrom(ctx); remote && record.Operation.RemotePrincipal != "" && entry.Principal != record.Operation.RemotePrincipal {
		return ControlResult{}, ErrControlUserInteractionRequired
	}
	if err := service.validateCapabilityLocked(auth); err != nil {
		return ControlResult{Operation: cloneControlOperation(record.Operation)}, err
	}
	if auth.ConversationID != "" && record.Operation.ConversationID != "" && auth.ConversationID != record.Operation.ConversationID {
		return ControlResult{Operation: cloneControlOperation(record.Operation)}, ErrControlUserInteractionRequired
	}
	record.Operation.Status = ControlRejected
	record.Operation.ErrorCode = "rejected"
	service.operations[operationID] = record
	if err := service.appendLocked("rejected", record); err != nil {
		return ControlResult{Operation: cloneControlOperation(record.Operation)}, err
	}
	delete(service.confirmations, operationID)
	service.clearKeySecretLocked(operationID)
	service.clearMCPProfileLocked(operationID)
	if !fromRun {
		if err := service.settleInvalidProposal(ctx, record); err != nil {
			return ControlResult{Operation: cloneControlOperation(record.Operation)}, err
		}
	}
	return ControlResult{Operation: cloneControlOperation(record.Operation), Result: "rejected"}, nil
}

func (service *ControlService) PlanRevertControlChange(ctx context.Context, operationID string, source ControlAuthorizationSource, conversationID string) (ControlOperation, error) {
	if service == nil {
		return ControlOperation{}, errors.New("harness: control service is not configured")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	original, ok := service.operations[operationID]
	if !ok {
		return ControlOperation{}, ErrControlOperationNotFound
	}
	if original.Operation.Status != ControlApplied || original.Operation.RevertClass == "none" {
		return ControlOperation{}, ErrControlIrreversible
	}
	if len(original.Before) == 0 {
		return ControlOperation{}, ErrControlIrreversible
	}
	record := controlRecord{Operation: ControlOperation{Kind: ControlRevert, Authorization: source,
		ConversationID: conversationID, ResourceIDs: append([]string(nil), original.Operation.ResourceIDs...),
		ExpectedRevisions: cloneRevisions(original.Operation.NewRevisions), Risk: original.Operation.Risk,
		Preview: "Revert " + original.Operation.Kind + " to its exact protected previous value.", RevertClass: "none", RevertOf: operationID},
		Arguments: mustJSON(map[string]string{"operation_id": operationID}), Before: cloneRawMap(original.After), After: cloneRawMap(original.Before)}
	record.Operation.ID, record.Operation.CreatedAt = newID(), nowText()
	record.Operation.ExpiresAt = time.Now().UTC().Add(controlConfirmationTTL).Format(time.RFC3339Nano)
	record.Operation.Status = ControlPending
	record.Operation.Digest = controlDigest(record)
	service.operations[record.Operation.ID] = record
	if err := service.appendLocked("planned", record); err != nil {
		delete(service.operations, record.Operation.ID)
		return ControlOperation{}, err
	}
	return cloneControlOperation(record.Operation), nil
}

func (service *ControlService) RevertControlChange(ctx context.Context, operationID string, auth ControlAuthorization) (ControlResult, error) {
	return service.decideControlChange(ctx, operationID, auth, true, false)
}

func (service *ControlService) decideControlChange(ctx context.Context, operationID string, auth ControlAuthorization, apply, fromRun bool) (ControlResult, error) {
	if service == nil {
		return ControlResult{}, errors.New("harness: control service is not configured")
	}
	if ctx == nil {
		return ControlResult{}, errors.New("harness: control context is required")
	}
	if err := ctx.Err(); err != nil {
		return ControlResult{}, err
	}
	if err := service.detectExternalChange(); err != nil {
		return ControlResult{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.expireDirectLocked(time.Now()); err != nil {
		return ControlResult{}, err
	}
	record, ok := service.operations[operationID]
	if !ok {
		return ControlResult{}, ErrControlOperationNotFound
	}
	result := ControlResult{Operation: cloneControlOperation(record.Operation)}
	if entry, remote := PublicEntryFrom(ctx); remote && record.Operation.RemotePrincipal != "" && entry.Principal != record.Operation.RemotePrincipal {
		return result, ErrControlUserInteractionRequired
	}
	if record.Operation.ErrorCode == "expired" {
		return result, ErrControlExpired
	}
	if record.Operation.Status != ControlPending {
		return result, ErrControlAlreadyUsed
	}
	if apply && !fromRun && record.Operation.AttemptID != "" {
		return result, ErrControlRunConfirmation
	}
	if err := service.validateCapabilityLocked(auth); err != nil {
		return result, err
	}
	if record.Operation.ConversationID != "" && auth.ConversationID != record.Operation.ConversationID {
		return result, ErrControlUserInteractionRequired
	}
	if record.Operation.Authorization == ControlModelProposed || record.Operation.Risk != ControlRiskOrdinary || strings.TrimSpace(auth.Code) != "" {
		if err := service.verifyCodeLocked(record, auth); err != nil {
			return ControlResult{Operation: cloneControlOperation(service.operations[operationID].Operation)}, err
		}
	}
	if !apply {
		return result, nil
	}
	for resource, expected := range record.Operation.ExpectedRevisions {
		if current := service.revisionLocked(resource); current != expected {
			if err := service.invalidateProposalLocked(ctx, &record, "stale_revision"); err != nil {
				return result, err
			}
			return ControlResult{Operation: cloneControlOperation(record.Operation), ErrorCode: "stale_revision"}, ErrControlStaleRevision
		}
	}
	if record.Operation.Kind == ControlSetWorkspace {
		var active int
		if err := service.core.Store.db.QueryRowContext(ctx, `SELECT count(*) FROM fa.runs WHERE status='running' OR active_since!=''`).Scan(&active); err != nil {
			return result, err
		}
		if active != 0 {
			return ControlResult{Operation: cloneControlOperation(record.Operation), ErrorCode: "busy_active_run"}, ErrControlBusy
		}
	}
	permissionReduction := spacePermissionReduction(record) || record.Operation.Kind == ControlTelegramAccess
	// Delivery grants are checked at dispatch and can be revoked while a
	// scheduled Run is evaluating. Requiring that Run to finish first would
	// make its own master switch unusable during generation.
	livePolicyChange := record.Operation.Kind == ControlSetProviderEnabled || record.Operation.Kind == ControlSetPublicWeb || record.Operation.Kind == ControlSetDeliveryPolicy || len(record.After["config.public_web"]) != 0
	if !permissionReduction {
		global := false
		for _, resource := range record.Operation.ResourceIDs {
			if strings.HasPrefix(resource, "config.") {
				global = true
			}
		}
		if global && !livePolicyChange {
			allowed := ""
			if fromRun {
				allowed = record.Operation.ConversationID
			}
			release, err := service.core.Store.runMu.commit(allowed)
			if err != nil {
				return ControlResult{Operation: cloneControlOperation(record.Operation), ErrorCode: "busy_active_run"}, err
			}
			defer release()
		}
	}
	if commands := service.core.commands; commands != nil && !permissionReduction {
		switch record.Operation.Kind {
		case ControlSetWorkspace, ControlSetPermission, ControlSetExecutionProfile, ControlSetSandbox, ControlSetNetwork, ControlSetFileScope, ControlSetTool, ControlSetCommandPolicy, ControlSetSpacePermission:
			commands.mu.Lock()
			ids := commands.activeIDsLocked()
			commands.mu.Unlock()
			if ids != "" {
				return ControlResult{Operation: cloneControlOperation(record.Operation), ErrorCode: "execution_profile_busy"}, fmt.Errorf("%w; affected commands: %s", ErrCommandBusy, ids)
			}
		}
	}
	if record.Operation.Risk != ControlRiskOrdinary && !permissionReduction && !livePolicyChange {
		if err := service.gate.beginControl(); err != nil {
			return ControlResult{Operation: cloneControlOperation(record.Operation), ErrorCode: "host_execution_busy"}, err
		}
		defer service.gate.endControl()
	}
	if err := service.applyRecordLocked(ctx, &record); err != nil {
		if errors.Is(err, ErrControlMCPReentryRequired) {
			return ControlResult{Operation: cloneControlOperation(record.Operation), ErrorCode: "mcp_reentry_required"}, err
		}
		if errors.Is(err, ErrUnknownResult) {
			record.Operation.Status, record.Operation.ErrorCode = ControlPartiallyApplied, "apply_unknown"
			record.Operation.Recovery = "Inspect saved resources and runtime evidence; do not retry an uncertain control commit."
			service.operations[operationID] = record
			_ = service.appendLocked("unknown", record)
			if record.Operation.AttemptID != "" {
				if attempt, loadErr := service.core.Store.GetAttempt(ctx, record.Operation.AttemptID); loadErr == nil {
					_ = service.core.Store.markUnknown(ctx, attempt, "control commit outcome unknown")
				}
			}
			return ControlResult{Operation: cloneControlOperation(record.Operation), ErrorCode: "apply_unknown", Recovery: record.Operation.Recovery}, err
		}
		record.Operation.Status = ControlFailed
		record.Operation.ErrorCode = "apply_failed"
		record.Operation.Recovery = "Inspect the current control state; no automatic retry was performed."
		service.operations[operationID] = record
		service.clearKeySecretLocked(operationID)
		service.clearMCPProfileLocked(operationID)
		_ = service.appendLocked("failed", record)
		_ = service.settleInvalidProposal(ctx, record)
		return ControlResult{Operation: cloneControlOperation(record.Operation), ErrorCode: "apply_failed", Recovery: record.Operation.Recovery}, err
	}
	record.Operation.Status = ControlApplied
	record.Operation.AppliedAt = nowText()
	if err := service.refreshBaseline(); err != nil {
		service.clearKeySecretLocked(operationID)
		service.clearMCPProfileLocked(operationID)
		return ControlResult{Operation: cloneControlOperation(record.Operation), Applied: true, NextRunRequired: true, Result: "applied_to_future_runs"}, err
	}
	record.Operation.NewRevisions = map[string]uint64{}
	for _, resource := range record.Operation.ResourceIDs {
		service.revisions[resource] = service.revisionLocked(resource) + 1
		record.Operation.NewRevisions[resource] = service.revisions[resource]
	}
	service.operations[operationID] = record
	if err := service.appendLocked("applied", record); err != nil {
		return ControlResult{Operation: cloneControlOperation(record.Operation), Applied: true, NextRunRequired: true, Result: "applied_to_future_runs"}, err
	}
	delete(service.confirmations, operationID)
	service.clearKeySecretLocked(operationID)
	service.clearMCPProfileLocked(operationID)
	return ControlResult{Operation: cloneControlOperation(record.Operation), Applied: true,
		NextRunRequired: true, Result: "applied_to_future_runs", Data: append(json.RawMessage(nil), record.ResultData...)}, nil
}

func (service *ControlService) validateCapabilityLocked(auth ControlAuthorization) error {
	if auth.Capability.token == "" || auth.Capability.generation != service.generation ||
		auth.Capability.principal == "" || auth.Principal == "" || auth.Principal != auth.Capability.principal ||
		(!auth.Capability.expiresAt.IsZero() && time.Now().UTC().After(auth.Capability.expiresAt)) {
		return ErrControlUserInteractionRequired
	}
	if auth.ConversationID != "" && auth.Capability.conversationID != "" && auth.ConversationID != auth.Capability.conversationID {
		return ErrControlUserInteractionRequired
	}
	return nil
}

func (service *ControlService) verifyCodeLocked(record controlRecord, auth ControlAuthorization) error {
	state, ok := service.confirmations[record.Operation.ID]
	if !ok {
		return ErrControlConfirmationRequired
	}
	if time.Now().UTC().After(state.expiresAt) {
		return ErrControlExpired
	}
	if state.attempts >= controlMaxCodeAttempts {
		return ErrControlWrongCode
	}
	if auth.ConversationID != state.conversationID {
		return ErrControlWrongCode
	}
	typed := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(auth.Code, "-", ""), " ", ""))
	if !hmac.Equal(service.controlCodeMAC(record.Operation.ID, state.conversationID, typed), state.codeMAC) {
		state.attempts++
		service.confirmations[record.Operation.ID] = state
		return ErrControlWrongCode
	}
	return nil
}

// controlCodeMAC binds a code to the owner generation, conversation and
// operation. The owner serves one local user, and every caller holding its
// interactive capability acts as that user, so the GUI, CLI and chat surface
// labels are deliberately left out: a code shown on one surface can be typed
// on another, while another conversation or owner generation cannot use it.
func (service *ControlService) controlCodeMAC(operationID, conversationID, code string) []byte {
	mac := hmac.New(sha256.New, service.ownerKey)
	for _, part := range []string{service.generation, operationID, conversationID} {
		mac.Write([]byte(part))
		mac.Write([]byte("\x00"))
	}
	mac.Write([]byte(code))
	return mac.Sum(nil)
}

func (service *ControlService) newControlCodeLocked(operationID, conversationID string, now time.Time) error {
	raw := make([]byte, controlCodeLength)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return err
	}
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	var builder strings.Builder
	for _, value := range raw {
		builder.WriteByte(alphabet[int(value)&31])
	}
	code := builder.String()
	service.confirmations[operationID] = controlConfirmationState{operationID: operationID,
		codeMAC: service.controlCodeMAC(operationID, conversationID, code), code: code,
		conversationID: conversationID, expiresAt: now.Add(controlConfirmationTTL)}
	return nil
}

func (service *ControlService) applyRecordLocked(ctx context.Context, record *controlRecord) error {
	if err := service.validatePublicOperation(ctx, *record); err != nil {
		return err
	}
	if record.Operation.Kind == ControlScheduleDelivery {
		return service.applyScheduledDelivery(ctx, record)
	}
	if record.Operation.Kind == ControlPublishSkill {
		return service.applySkillPublication(ctx, record)
	}
	if record.Operation.Kind == ControlChangeExtensionState {
		return service.applyExtensionState(ctx, record)
	}
	if record.Operation.Kind == ControlInitExtensionStorage {
		return service.initExtensionStorage(ctx, record)
	}
	if record.Operation.Kind == ControlRevert {
		original, ok := service.operations[record.Operation.RevertOf]
		if !ok {
			return ErrControlOperationNotFound
		}
		return service.applyBeforeLocked(ctx, original)
	}
	if record.Operation.Kind == ControlSetKeyProfile || record.Operation.Kind == ControlRemoveKey || record.Operation.Kind == ControlProbeKey {
		return service.applyKeyRecordLocked(ctx, record)
	}
	if record.Operation.Kind == ControlSetMCPProfile || record.Operation.Kind == ControlRemoveMCP {
		return service.applyMCPRecordLocked(ctx, record)
	}
	if record.Operation.Kind == ControlSetMemoryPin {
		return service.applyMemoryPin(ctx, record)
	}
	if record.Operation.Kind == ControlSetStickerSets {
		return service.applyStickerSets(ctx, record)
	}
	if record.Operation.Kind == ControlSetMemorySelection {
		var args struct {
			ConversationID string          `json:"conversation_id"`
			Selection      MemorySelection `json:"selection"`
		}
		if err := decodeControlJSON(record.Arguments, &args); err != nil {
			return err
		}
		return NewTaskService(service.core).SelectMemory(ctx, args.ConversationID, args.Selection)
	}
	if record.Operation.Kind == ControlRestartMCP {
		return NewTaskService(service.core).RestartMCP(ctx)
	}
	if record.Operation.Kind == ControlImportSkill || record.Operation.Kind == ControlRemoveSkill ||
		record.Operation.Kind == ControlImportExtension || record.Operation.Kind == ControlRemoveExtension ||
		record.Operation.Kind == ControlRestartExtension || record.Operation.Kind == ControlClearExtensionData {
		return service.applyCapabilityLifecycleLocked(ctx, record)
	}
	if record.Operation.Kind == ControlBranchConversation {
		return service.applyBranchControl(ctx, record)
	}
	if record.Operation.Kind == ControlDeleteAttachment {
		return service.applyAttachmentDelete(ctx, record)
	}
	if record.Operation.Kind == ControlTelegramAccess {
		return service.applyTelegramAccess(ctx, record)
	}
	if isSpaceControl(record.Operation.Kind) {
		return service.applySpaceControl(ctx, record)
	}
	if isConversationControl(record.Operation.Kind) {
		return service.applyConversationLocked(ctx, *record)
	}
	candidate := cloneConfig(service.core.currentConfig())
	if record.Operation.Kind == ControlSetProviderEnabled || record.Operation.Kind == ControlSetPublicWeb || record.Operation.Kind == ControlSetDeliveryPolicy {
		if record.Operation.Kind == ControlSetDeliveryPolicy {
			service.core.Store.telegramMu.Lock()
			defer service.core.Store.telegramMu.Unlock()
		}

		if record.Operation.Kind == ControlSetProviderEnabled {
			if err := service.validateProvider(record.Arguments); err != nil {
				return err
			}
		}
		if err := applyConfigControl(&candidate, record.Operation.Kind, record.Arguments, ctx, service.core.Store.DataDir()); err != nil {
			return err
		}
		if err := WriteConfig(service.core.Store.DataDir(), candidate); err != nil {
			return err
		}
		service.core.setConfig(candidate)
		if record.Operation.Kind == ControlSetPublicWeb {
			service.core.recordPublicPolicyChange(ctx)
			return NewTaskService(service.core).stopPublicCommands(ctx)
		}
		if record.Operation.Kind == ControlSetDeliveryPolicy {
			return errors.Join(NewTaskService(service.core).cancelRevokedCandidates(ctx), NewTaskService(service.core).cancelRevokedWakes(ctx))
		}
		return nil
	}
	if err := applyConfigControl(&candidate, record.Operation.Kind, record.Arguments, ctx, service.core.Store.DataDir()); err != nil {
		return err
	}
	_, err := NewTaskService(service.core).configureRuntimePolicy(ctx, "", func(config *Config) error { *config = candidate; return nil }, true)
	return err
}

func (service *ControlService) applyMCPRecordLocked(ctx context.Context, record *controlRecord) error {
	var args struct {
		ID     string `json:"id"`
		Remove bool   `json:"remove"`
	}
	if err := decodeControlJSON(record.Arguments, &args); err != nil || !validMCPControlID(args.ID) {
		return errors.New("harness: invalid MCP profile operation")
	}
	candidate := cloneConfig(service.core.currentConfig())
	if args.Remove || record.Operation.Kind == ControlRemoveMCP {
		if _, ok := candidate.MCPServers[args.ID]; !ok {
			return errors.New("harness: MCP server not found")
		}
		delete(candidate.MCPServers, args.ID)
	} else {
		profile, ok := service.mcpProfiles[record.Operation.ID]
		if !ok {
			return ErrControlMCPReentryRequired
		}
		if candidate.MCPServers == nil {
			candidate.MCPServers = map[string]MCPServerConfig{}
		}
		candidate.MCPServers[args.ID] = cloneMCPServerConfig(profile)
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	_, err := NewTaskService(service.core).configureRuntimePolicy(ctx, "", func(config *Config) error {
		*config = candidate
		return nil
	}, true)
	service.clearMCPProfileLocked(record.Operation.ID)
	return err
}

func (service *ControlService) applyCapabilityLifecycleLocked(ctx context.Context, record *controlRecord) error {
	var args struct {
		ID     string `json:"id"`
		Source string `json:"source"`
	}
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	tasks := NewTaskService(service.core)
	switch record.Operation.Kind {
	case ControlImportSkill:
		item, err := tasks.ImportSkill(ctx, args.Source)
		if err == nil {
			record.ResultData, _ = json.Marshal(item)
		}
		return err
	case ControlRemoveSkill:
		return tasks.DeleteSkill(ctx, args.ID)
	case ControlImportExtension:
		item, err := tasks.ImportExtension(ctx, args.Source)
		if err == nil {
			record.ResultData, _ = json.Marshal(item)
		}
		return err
	case ControlRemoveExtension:
		return tasks.DeleteExtension(ctx, args.ID)
	case ControlRestartExtension:
		return tasks.RestartExtension(ctx, args.ID)
	case ControlClearExtensionData:
		return tasks.ClearExtensionData(ctx, args.ID)
	default:
		return errors.New("harness: unsupported capability lifecycle operation")
	}
}

func (service *ControlService) applyKeyRecordLocked(ctx context.Context, record *controlRecord) error {
	var args struct {
		Namespace     string `json:"namespace"`
		ID            string `json:"id"`
		New           bool   `json:"new"`
		Remove        bool   `json:"remove"`
		Force         bool   `json:"force"`
		DisableModels bool   `json:"disable_models"`
		NewDefault    string `json:"new_default"`
		Name          string `json:"name"`
		BaseURL       string `json:"base_url"`
		Protocol      string `json:"protocol"`
		Note          string `json:"note"`
		Source        string `json:"source"`
		EnvName       string `json:"env_name"`
	}
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	if service.core.Keys == nil {
		return errors.New("harness: key store is not configured")
	}
	keys := service.core.Keys
	if args.Namespace == "channels" {
		keys = NewTaskService(service.core).ChannelKeys()
	} else if args.Namespace != "" {
		return ErrPermissionDenied
	}
	if record.Operation.Kind == ControlProbeKey {
		if args.Namespace != "" {
			return ErrPermissionDenied
		}
		resolved, err := keys.Resolve(args.ID)
		if err != nil {
			return err
		}
		probe, err := ProbeModels(ctx, resolved, nil)
		if err != nil {
			return err
		}
		if err := keys.RecordCheck(args.ID, probe.Category); err != nil {
			return err
		}
		raw, err := json.Marshal(probe)
		if err != nil {
			return err
		}
		record.ResultData = raw
		return nil
	}
	if args.Remove || record.Operation.Kind == ControlRemoveKey {
		config := service.core.currentConfig()
		if args.Namespace == "" && !bytes.Equal(record.Before["config.models"], mustJSON(configResourceValue(config, "config.models"))) {
			// Older pending removals without an impact snapshot must be previewed
			// again. Never extend an approved deletion to newly referencing models.
			return ErrControlStaleRevision
		}
		users := keyModelUsers(config, args.ID)
		if args.Namespace == "channels" {
			users = nil
			for _, connection := range config.Channels.Connections {
				if connection.Credential == args.ID {
					return ErrControlBusy
				}
			}
		}
		if len(users) != 0 && !args.Force && !args.DisableModels {
			return ErrControlBusy
		}
		if args.DisableModels && len(users) != 0 {
			candidate := cloneConfig(config)
			if _, err := RemoveModels(&candidate, users, args.NewDefault); err != nil {
				return err
			}
			if _, err := NewTaskService(service.core).configureRuntimePolicy(ctx, "", func(next *Config) error {
				*next = candidate
				return nil
			}, true); err != nil {
				return err
			}
		}
		return keys.Remove(args.ID)
	}
	secret := service.keySecrets[record.Operation.ID]
	delete(service.keySecrets, record.Operation.ID)
	defer clear(secret)
	input := KeyInput{Name: args.Name, BaseURL: args.BaseURL, Protocol: args.Protocol, Note: args.Note,
		Source: args.Source, EnvName: args.EnvName, Secret: secret}
	if args.New {
		_, err := keys.Add(input)
		return err
	}
	if args.ID == "" {
		return ErrKeyNotFound
	}
	_, err := keys.Update(args.ID, input)
	return err
}

func (service *ControlService) clearKeySecretLocked(operationID string) {
	if service == nil || service.keySecrets == nil {
		return
	}
	clear(service.keySecrets[operationID])
	delete(service.keySecrets, operationID)
}

func (service *ControlService) applyBeforeLocked(ctx context.Context, original controlRecord) error {
	if original.Operation.Kind == ControlSetSpacePermission {
		var args SpaceControlArguments
		var before struct {
			Permissions map[string]PermissionSelection `json:"permissions"`
		}
		if json.Unmarshal(original.Arguments, &args) != nil || json.Unmarshal(original.Before["spaces"], &before) != nil {
			return ErrStoreFormat
		}
		selection, ok := before.Permissions[args.SpaceID]
		if !ok {
			return ErrStoreFormat
		}
		return NewTaskService(service.core).persistSpacePermission(ctx, args.SpaceID, mustJSON(selection))
	}
	if isConversationControl(original.Operation.Kind) {
		return service.applyConversationBeforeLocked(ctx, original)
	}
	for resource, raw := range original.Before {
		if strings.HasPrefix(resource, "conversation.memory:") {
			id := strings.TrimPrefix(resource, "conversation.memory:")
			var selection MemorySelection
			if err := json.Unmarshal(raw, &selection); err != nil {
				return err
			}
			return NewTaskService(service.core).SelectMemory(ctx, id, selection)
		}
	}
	candidate := cloneConfig(service.core.currentConfig())
	for resource, raw := range original.Before {
		if err := setConfigResource(&candidate, resource, raw); err != nil {
			return err
		}
	}
	if _, public := original.Before["config.public_web"]; public {
		if err := validatePublicControl(ctx, service.core.currentConfig().PublicWeb, candidate.PublicWeb); err != nil {
			return err
		}
		if err := WriteConfig(service.core.Store.DataDir(), candidate); err != nil {
			return err
		}
		service.core.setConfig(candidate)
		service.core.recordPublicPolicyChange(ctx)
		return NewTaskService(service.core).stopPublicCommands(ctx)
	}
	_, err := NewTaskService(service.core).configureRuntimePolicy(ctx, "", func(config *Config) error { *config = candidate; return nil }, true)
	return err
}

func (service *ControlService) applyConversationLocked(ctx context.Context, record controlRecord) error {
	var args struct {
		ConversationID string               `json:"conversation_id"`
		ModelID        string               `json:"model_id"`
		Mode           PermissionMode       `json:"mode"`
		Selection      *PermissionSelection `json:"selection,omitempty"`
		Title          string               `json:"title"`
		Pinned         bool                 `json:"pinned"`
		Capabilities   CapabilitySelection  `json:"capabilities"`
	}
	if err := decodeControlJSON(record.Arguments, &args); err != nil {
		return err
	}
	if args.ConversationID == "" {
		args.ConversationID = record.Operation.ConversationID
	}
	switch record.Operation.Kind {
	case ControlSetConversationModel:
		config := service.core.currentConfig()
		if _, ok := config.Models[args.ModelID]; !ok {
			return fmt.Errorf("harness: model %q is not configured", args.ModelID)
		}
		_, err := service.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "chat/model/"+args.ConversationID, args.ModelID)
		return err
	case ControlRenameConversation:
		return service.core.Store.RenameConversation(ctx, args.ConversationID, args.Title)
	case ControlPinConversation:
		return service.core.Store.PinConversation(ctx, args.ConversationID, args.Pinned)
	case ControlSetConversationCapabilities:
		return NewTaskService(service.core).SelectCapabilities(ctx, args.ConversationID, args.Capabilities)
	default:
		return errors.New("harness: unsupported conversation control operation")
	}
}

func (service *ControlService) applyConversationBeforeLocked(ctx context.Context, original controlRecord) error {
	for resource, raw := range original.Before {
		if strings.HasPrefix(resource, "conversation.model:") {
			id := strings.TrimPrefix(resource, "conversation.model:")
			var model string
			if err := json.Unmarshal(raw, &model); err != nil {
				return err
			}
			if model == "" {
				_, err := service.core.Store.db.ExecContext(ctx, `DELETE FROM fa.meta WHERE key=$1`, "chat/model/"+id)
				return err
			}
			_, err := service.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, "chat/model/"+id, model)
			return err
		}
		if strings.HasPrefix(resource, "conversation.title:") {
			id := strings.TrimPrefix(resource, "conversation.title:")
			var title string
			if err := json.Unmarshal(raw, &title); err != nil {
				return err
			}
			return service.core.Store.RenameConversation(ctx, id, title)
		}
		if strings.HasPrefix(resource, "conversation.pinned:") {
			id := strings.TrimPrefix(resource, "conversation.pinned:")
			var pinned bool
			if err := json.Unmarshal(raw, &pinned); err != nil {
				return err
			}
			return service.core.Store.PinConversation(ctx, id, pinned)
		}
		if strings.HasPrefix(resource, "conversation.capabilities:") {
			id := strings.TrimPrefix(resource, "conversation.capabilities:")
			var selection CapabilitySelection
			if err := json.Unmarshal(raw, &selection); err != nil || selection.validate(service.core.currentConfig()) != nil {
				return ErrStoreFormat
			}
			encoded, err := json.Marshal(selection.normalize())
			if err != nil {
				return err
			}
			_, err = service.core.Store.db.ExecContext(ctx, `INSERT INTO fa.meta(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, capabilityMetaPrefix+id, string(encoded))
			return err
		}
	}
	return errors.New("harness: conversation revert snapshot is empty")
}

func (service *ControlService) buildRecordLocked(ctx context.Context, request ControlPlanRequest) (controlRecord, error) {
	entry, ok := catalogEntry(request.Kind)
	if !ok {
		return controlRecord{}, fmt.Errorf("harness: unsupported control operation %q", request.Kind)
	}
	if request.Authorization == ControlModelProposed && !entry.ModelProposable {
		return controlRecord{}, errors.New("harness: operation is user-only")
	}
	if request.Kind == ControlSetExtensionPolicy {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(request.Arguments, &fields); err != nil {
			return controlRecord{}, errors.New("harness: invalid extension policy")
		}
		if request.Authorization == ControlModelProposed && fields["env_from"] != nil {
			return controlRecord{}, errors.New("harness: extension secret mappings are user-only")
		}
		if fields["settings"] != nil {
			if err := service.validateExtensionSettings(request.Arguments, request.Authorization == ControlModelProposed); err != nil {
				return controlRecord{}, err
			}
		}
	}
	record := controlRecord{Operation: ControlOperation{Kind: request.Kind, Authorization: request.Authorization, ConversationID: request.ConversationID,
		Risk: entry.Risk, ResourceIDs: []string{}, ExpectedRevisions: map[string]uint64{}, OldValues: map[string]string{}, ProposedValues: map[string]string{}}, Arguments: append(json.RawMessage(nil), request.Arguments...)}
	if len(record.Arguments) == 0 {
		record.Arguments = json.RawMessage(`{}`)
	}
	config := cloneConfig(service.core.currentConfig())
	if isSafeProfileControl(request.Kind) {
		if err := service.buildSafeProfileControl(ctx, &record); err != nil {
			return controlRecord{}, err
		}
	} else if request.Kind == ControlScheduleDelivery {
		if err := service.buildScheduledDelivery(ctx, &record); err != nil {
			return controlRecord{}, err
		}
	} else if request.Kind == ControlPublishSkill {
		if err := service.buildSkillPublication(ctx, &record); err != nil {
			return controlRecord{}, err
		}
	} else if request.Kind == ControlChangeExtensionState || request.Kind == ControlInitExtensionStorage {
		if err := service.buildExtensionState(ctx, &record, request.originRunID); err != nil {
			return controlRecord{}, err
		}
	} else if request.Kind == ControlBranchConversation {
		if err := service.buildBranchControl(ctx, &record); err != nil {
			return controlRecord{}, err
		}
	} else if request.Kind == ControlTelegramAccess {
		if err := service.buildTelegramAccess(ctx, &record); err != nil {
			return controlRecord{}, err
		}
	} else if request.Kind == ControlDeleteAttachment {
		if err := service.buildAttachmentDelete(ctx, &record); err != nil {
			return controlRecord{}, err
		}
	} else if isSpaceControl(request.Kind) {
		if err := service.buildSpaceControl(ctx, &record); err != nil {
			return controlRecord{}, err
		}
	} else if request.Kind == ControlSetMemoryPin {
		if err := service.buildMemoryPin(ctx, &record); err != nil {
			return controlRecord{}, err
		}
	} else if request.Kind == ControlSetStickerSets {
		if err := service.buildStickerSets(ctx, &record); err != nil {
			return controlRecord{}, err
		}
	} else if request.Kind == ControlSetMemorySelection {
		if err := service.buildMemorySelectionRecord(&record, ctx); err != nil {
			return controlRecord{}, err
		}
	} else if isConversationControl(request.Kind) {
		var args struct {
			ConversationID string               `json:"conversation_id"`
			ModelID        string               `json:"model_id"`
			Mode           PermissionMode       `json:"mode"`
			Selection      *PermissionSelection `json:"selection,omitempty"`
			Title          string               `json:"title"`
			Pinned         *bool                `json:"pinned"`
			Capabilities   CapabilitySelection  `json:"capabilities"`
		}
		if err := decodeControlJSON(record.Arguments, &args); err != nil {
			return controlRecord{}, err
		}
		if args.ConversationID == "" {
			args.ConversationID = request.ConversationID
		}
		if args.ConversationID == "" {
			return controlRecord{}, errors.New("harness: conversation control requires a conversation")
		}
		conversation, err := service.core.Store.GetConversation(ctx, args.ConversationID)
		if err != nil {
			return controlRecord{}, err
		}
		record.Operation.ConversationID = args.ConversationID
		switch request.Kind {
		case ControlSetConversationModel:
			if _, ok := config.Models[args.ModelID]; !ok {
				return controlRecord{}, fmt.Errorf("harness: model %q is not configured", args.ModelID)
			}
			record.Operation.ResourceIDs = []string{"conversation.model:" + args.ConversationID}
			record.Before = map[string]json.RawMessage{record.Operation.ResourceIDs[0]: mustJSON(conversation.ModelID)}
			record.After = map[string]json.RawMessage{record.Operation.ResourceIDs[0]: mustJSON(args.ModelID)}
			record.Operation.Preview = fmt.Sprintf("Set conversation %s model to %s for future Runs.", redactID(args.ConversationID), args.ModelID)
		case ControlRenameConversation:
			if strings.TrimSpace(args.Title) == "" {
				return controlRecord{}, ErrInvalidConversationTitle
			}
			record.Operation.ResourceIDs = []string{"conversation.title:" + args.ConversationID}
			record.Before = map[string]json.RawMessage{record.Operation.ResourceIDs[0]: mustJSON(conversation.Title)}
			record.After = map[string]json.RawMessage{record.Operation.ResourceIDs[0]: mustJSON(strings.TrimSpace(args.Title))}
			record.Operation.Preview = fmt.Sprintf("Rename conversation %s.", redactID(args.ConversationID))
		case ControlPinConversation:
			if args.Pinned == nil {
				return controlRecord{}, errors.New("harness: pinned is required")
			}
			record.Operation.ResourceIDs = []string{"conversation.pinned:" + args.ConversationID}
			record.Before = map[string]json.RawMessage{record.Operation.ResourceIDs[0]: mustJSON(conversation.Pinned)}
			record.After = map[string]json.RawMessage{record.Operation.ResourceIDs[0]: mustJSON(*args.Pinned)}
			record.Operation.Preview = fmt.Sprintf("%s conversation %s.", map[bool]string{true: "Pin", false: "Unpin"}[*args.Pinned], redactID(args.ConversationID))
		case ControlSetConversationCapabilities:
			if err := args.Capabilities.validate(config); err != nil {
				return controlRecord{}, err
			}
			resource := "conversation.capabilities:" + args.ConversationID
			previous, previousErr := service.core.Store.selectedCapabilities(ctx, args.ConversationID, config)
			if previousErr != nil {
				return controlRecord{}, previousErr
			}
			record.Operation.ResourceIDs = []string{resource}
			record.Before = map[string]json.RawMessage{resource: mustJSON(previous)}
			record.After = map[string]json.RawMessage{resource: mustJSON(args.Capabilities.normalize())}
			record.Operation.Preview = fmt.Sprintf("Change capability selection for conversation %s for future Runs.", redactID(args.ConversationID))
		}
	} else {
		if record.Operation.Kind == ControlSetProviderEnabled {
			if err := service.validateProvider(record.Arguments); err != nil {
				return controlRecord{}, err
			}
		}
		if err := buildConfigRecord(&record, &config, ctx, service.core.Store.DataDir()); err != nil {
			return controlRecord{}, err
		}
		if record.Operation.Kind == ControlSetChannelPolicy {
			keys := NewTaskService(service.core).ChannelKeys()
			if err := validateChannelKeys(keys, config.Channels); err != nil {
				return controlRecord{}, err
			}
		}
	}
	for _, resource := range record.Operation.ResourceIDs {
		if expected, ok := request.ExpectedRevisions[resource]; ok {
			record.Operation.ExpectedRevisions[resource] = expected
		} else {
			record.Operation.ExpectedRevisions[resource] = service.revisionLocked(resource)
		}
	}
	for resource := range request.ExpectedRevisions {
		if !contains(record.Operation.ResourceIDs, resource) {
			return controlRecord{}, ErrControlStaleRevision
		}
	}
	if len(record.Operation.OldValues) == 0 {
		record.Operation.OldValues = redactedValues(record.Before)
	}
	if len(record.Operation.ProposedValues) == 0 {
		record.Operation.ProposedValues = redactedValues(record.After)
	}
	return record, nil
}

func (service *ControlService) buildMemorySelectionRecord(record *controlRecord, ctx context.Context) error {
	var args struct {
		ConversationID string          `json:"conversation_id"`
		Selection      MemorySelection `json:"selection"`
	}
	if err := decodeControlJSON(record.Arguments, &args); err != nil || strings.TrimSpace(args.ConversationID) == "" {
		return errors.New("harness: memory selection requires a conversation")
	}
	tasks := NewTaskService(service.core)
	if _, err := service.core.Store.GetConversation(ctx, args.ConversationID); err != nil {
		return err
	}
	agent, err := tasks.AgentIdentity(ctx, args.ConversationID)
	if err != nil {
		return err
	}
	if err := service.core.validateMemorySelection(ctx, memoryRunPolicy{AgentID: agent, Selection: args.Selection}); err != nil {
		return err
	}
	selectionResource := "conversation.memory:" + args.ConversationID
	previous, err := tasks.SelectedMemory(ctx, args.ConversationID)
	if err != nil {
		return err
	}
	record.Operation.ConversationID = args.ConversationID
	record.Operation.ResourceIDs = []string{selectionResource}
	record.Before = map[string]json.RawMessage{selectionResource: mustJSON(previous)}
	record.After = map[string]json.RawMessage{selectionResource: mustJSON(args.Selection)}
	record.Operation.Preview = fmt.Sprintf("Change memory selection for conversation %s for future Runs.", redactID(args.ConversationID))
	return nil
}

func buildConfigRecord(record *controlRecord, config *Config, ctx context.Context, dataDir string) error {
	before := cloneConfig(*config)
	if err := applyConfigControl(config, record.Operation.Kind, record.Arguments, ctx, dataDir); err != nil {
		return err
	}
	resource, err := configControlResource(record.Operation.Kind, record.Arguments)
	if err != nil {
		return err
	}
	record.Operation.ResourceIDs = []string{resource}
	record.Before = map[string]json.RawMessage{resource: mustJSON(configResourceValue(before, resource))}
	record.After = map[string]json.RawMessage{resource: mustJSON(configResourceValue(*config, resource))}
	if record.Operation.Kind == ControlSetModelProfile || record.Operation.Kind == ControlRemoveModel {
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(record.Arguments, &args); err != nil {
			return err
		}
		profileResource := "config.model:" + args.ID
		if profileResource != resource {
			record.Operation.ResourceIDs = append(record.Operation.ResourceIDs, profileResource)
			record.Before[profileResource] = mustJSON(configResourceValue(before, profileResource))
			record.After[profileResource] = mustJSON(configResourceValue(*config, profileResource))
		}
	}
	record.Operation.Preview = fmt.Sprintf("Change %s for future Runs from %s to %s.", record.Operation.Kind, redactJSON(record.Before[resource]), redactJSON(record.After[resource]))
	if record.Operation.Kind == ControlSetPublicWeb {
		record.Operation.Preview = publicPolicyPreview(before.PublicWeb, config.PublicWeb)
	}
	return nil
}

func configControlResource(kind string, raw json.RawMessage) (string, error) {
	switch kind {
	case ControlSetDeliveryPolicy:
		return "config.delivery", nil
	case ControlSetPublicWeb:
		return "config.public_web", nil
	case ControlSetAttachmentPolicy:
		return "config.attachments", nil
	case ControlSetServerPolicy:
		return "config.server", nil
	case ControlSetProviderEnabled:
		return "config.providers", nil
	case ControlSetDefaultModel, ControlRemoveModel:
		return "config.models", nil
	case ControlSetWorkspace:
		return "config.workspace", nil
	case ControlSetPermission, ControlSetExecutionProfile, ControlSetSandbox, ControlSetNetwork, ControlSetFileScope, ControlSetTool, ControlSetCommandPolicy:
		return "config." + kind, nil
	case ControlSetAutoPolicy:
		return "config.auto", nil
	case ControlSetHTTPPolicy:
		return "config.http", nil
	case ControlSetChannelPolicy:
		return "config.channels", nil
	case ControlSetMemoryPolicy:
		return "config.memory", nil
	case ControlSetPromptPolicy:
		return "config.prompt", nil
	case ControlSetCollaboration:
		return "config.collaboration", nil
	case ControlSetRuntimeLimits:
		return "config.limits", nil
	case ControlSetSkillPolicy:
		var args struct {
			ID               string `json:"id"`
			Enabled          *bool  `json:"enabled"`
			TrustedWorkspace string `json:"trusted_workspace,omitempty"`
			Trusted          *bool  `json:"trusted"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || args.ID == "" && (args.TrustedWorkspace == "" || args.Trusted == nil) || args.ID != "" && !validCapabilityID(args.ID) {
			return "", errors.New("harness: skill id is required")
		}
		if args.ID == "" {
			return "config.skills", nil
		}
		return "config.skill:" + args.ID, nil
	case ControlSetExtensionPolicy:
		var args struct {
			ID               string            `json:"id"`
			Enabled          *bool             `json:"enabled"`
			Required         *bool             `json:"required"`
			TrustedWorkspace string            `json:"trusted_workspace,omitempty"`
			Trusted          *bool             `json:"trusted"`
			Settings         map[string]any    `json:"settings,omitempty"`
			EnvFrom          map[string]string `json:"env_from,omitempty"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || args.ID == "" && (args.TrustedWorkspace == "" || args.Trusted == nil) || args.ID != "" && !validCapabilityID(args.ID) {
			return "", errors.New("harness: extension id is required")
		}
		if args.ID == "" {
			return "config.extensions", nil
		}
		return "config.extension:" + args.ID, nil
	case ControlSetMCPPolicy:
		var args struct {
			ID                string   `json:"id"`
			Enabled           *bool    `json:"enabled"`
			TrustReadOnlyHint *bool    `json:"trust_read_only_hint"`
			HiddenTools       []string `json:"hidden_tools"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || strings.TrimSpace(args.ID) == "" {
			return "", errors.New("harness: MCP server id is required")
		}
		return "config.mcp:" + args.ID, nil
	case ControlRestartMCP:
		return "runtime.mcp", nil
	case ControlImportSkill:
		var args struct {
			Source string `json:"source"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || !filepath.IsAbs(args.Source) {
			return "", errors.New("harness: Skill import source must be absolute")
		}
		item, err := readSkill(args.Source, "user")
		if err != nil {
			return "", fmt.Errorf("harness: invalid skill folder: %w", err)
		}
		return "skills.catalog:" + item.Name, nil
	case ControlRemoveSkill:
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || !validCapabilityID(args.ID) {
			return "", errors.New("harness: Skill id is required")
		}
		return "skills.catalog:" + args.ID, nil
	case ControlImportExtension:
		var args struct {
			Source string `json:"source"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || !filepath.IsAbs(args.Source) {
			return "", errors.New("harness: Extension import source must be absolute")
		}
		item, err := CheckExtension(args.Source)
		if err != nil {
			return "", err
		}
		return "extensions.catalog:" + item.ID, nil
	case ControlRemoveExtension, ControlRestartExtension, ControlClearExtensionData:
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || !validExtensionID(args.ID) {
			return "", errors.New("harness: Extension id is required")
		}
		family := "extensions.catalog"
		if kind == ControlRestartExtension {
			family = "extensions.runtime"
		} else if kind == ControlClearExtensionData {
			family = "extensions.data"
		}
		return family + ":" + args.ID, nil
	case ControlSetModelProfile:
		var args modelProfileControlArguments
		if err := decodeControlJSON(raw, &args); err != nil || !validModelID(args.ID) {
			return "", errors.New("harness: model id is required")
		}
		if args.MakeDefault {
			return "config.models", nil
		}
		return "config.model:" + args.ID, nil
	default:
		return "", fmt.Errorf("harness: unsupported config control operation %q", kind)
	}
}

func applyConfigControl(config *Config, kind string, raw json.RawMessage, ctx context.Context, dataDir string) error {
	if config == nil {
		return errors.New("harness: configuration is required")
	}
	switch kind {
	case ControlSetDeliveryPolicy:
		if err := decodeControlJSON(raw, &config.Delivery); err != nil {
			return err
		}
		if err := config.Delivery.validate(); err != nil {
			return err
		}
	case ControlSetPublicWeb:
		wasEnabled := config.PublicWeb.Effective().Enabled
		if err := decodeControlJSON(raw, &config.PublicWeb); err != nil {
			return err
		}
		if !wasEnabled && config.PublicWeb.Enabled && runtime.GOOS != "linux" {
			return errors.New("harness: public Web can only be enabled on Linux; local GUI and CLI remain available")
		}
	case ControlSetAttachmentPolicy:
		if err := decodeControlJSON(raw, &config.Attachments); err != nil {
			return err
		}
	case ControlSetServerPolicy:
		if err := decodeControlJSON(raw, &config.Server); err != nil {
			return err
		}
	case ControlSetProviderEnabled:
		var args struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		if !validKeyID(args.ID) {
			return errors.New("harness: provider requires a saved Key profile ID")
		}
		if config.Providers == nil {
			config.Providers = map[string]bool{}
		}
		config.Providers[args.ID] = args.Enabled
	case ControlSetPermission:
		var args struct {
			Mode PermissionMode `json:"mode"`
		}
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		if err := args.Mode.Validate(); err != nil {
			return err
		}
		config.Execution.PermissionMode = args.Mode
	case ControlSetExecutionProfile:
		var args struct {
			Mode          PermissionMode `json:"mode"`
			Scope         FileScope      `json:"scope"`
			AllowedRoots  []string       `json:"allowed_roots,omitempty"`
			BlockedPaths  []string       `json:"blocked_paths,omitempty"`
			NetworkPolicy NetworkPolicy  `json:"network_policy"`
		}
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		if err := args.Mode.Validate(); err != nil {
			return err
		}
		if err := ValidateFileAccessSettings(ctx, dataDir, args.Scope, args.AllowedRoots, args.BlockedPaths); err != nil {
			return err
		}
		if err := args.NetworkPolicy.Validate(); err != nil {
			return err
		}
		roots, err := normalizeComputerRoots(ctx, dataDir, args.AllowedRoots)
		if err != nil {
			return err
		}
		blocked, err := normalizeBlockedPaths(ctx, args.BlockedPaths)
		if err != nil {
			return err
		}
		config.Execution.PermissionMode = args.Mode
		config.Execution.FileScope, config.Execution.AdditionalRoots = args.Scope, roots
		config.Execution.BlockedPaths, config.ComputerRoots = blocked, append([]string(nil), roots...)
		config.ComputerFiles = args.Scope != FileScopeWorkspace
		config.Execution.NetworkPolicy = args.NetworkPolicy
	case ControlSetSandbox:
		var args struct {
			Mode SandboxMode `json:"mode"`
		}
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		if err := args.Mode.Validate(); err != nil {
			return err
		}
		config.Execution.SandboxMode = args.Mode
	case ControlSetNetwork:
		var args NetworkPolicy
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		if err := args.Validate(); err != nil {
			return err
		}
		config.Execution.NetworkPolicy = args
	case ControlSetFileScope:
		var args struct {
			Scope        FileScope `json:"scope"`
			AllowedRoots []string  `json:"allowed_roots,omitempty"`
			BlockedPaths []string  `json:"blocked_paths,omitempty"`
		}
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		if err := ValidateFileAccessSettings(ctx, dataDir, args.Scope, args.AllowedRoots, args.BlockedPaths); err != nil {
			return err
		}
		roots, err := normalizeComputerRoots(ctx, dataDir, args.AllowedRoots)
		if err != nil {
			return err
		}
		blocked, err := normalizeBlockedPaths(ctx, args.BlockedPaths)
		if err != nil {
			return err
		}
		config.Execution.FileScope, config.Execution.AdditionalRoots, config.ComputerRoots, config.Execution.BlockedPaths = args.Scope, roots, append([]string(nil), roots...), blocked
		config.ComputerFiles = args.Scope != FileScopeWorkspace
	case ControlSetTool:
		var args struct {
			Tool    string `json:"tool"`
			Enabled bool   `json:"enabled"`
		}
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		tools := effectiveBuiltinToolSettings(*config)
		switch args.Tool {
		case "file":
			tools.File = args.Enabled
		case "command_run":
			tools.CommandRun = args.Enabled
			config.Commands.Enabled = args.Enabled
		case "network":
			tools.Network = args.Enabled
			config.HTTP.Enabled = args.Enabled
			if !args.Enabled {
				config.HTTP.AllowedOrigins = nil
			}
		case "memory":
			tools.Memory = args.Enabled
		default:
			return errors.New("harness: unsupported built-in tool")
		}
		config.Tools = tools
	case ControlSetCommandPolicy:
		var args CommandConfig
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		config.Commands = args
		tools := effectiveBuiltinToolSettings(*config)
		tools.CommandRun = args.Enabled
		config.Tools = tools
		if err := validateCommandPolicy(config.Commands); err != nil {
			return err
		}
	case ControlSetWorkspace:
		var args struct {
			Workspace string `json:"workspace"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || strings.TrimSpace(args.Workspace) == "" {
			return errors.New("harness: workspace is required")
		}
		workspace, err := canonicalDir(args.Workspace)
		if err != nil {
			return err
		}
		config.Workspace = workspace
	case ControlSetDefaultModel:
		var args struct {
			ModelID string `json:"model_id"`
		}
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		return SetDefaultModel(config, args.ModelID)
	case ControlSetModelProfile:
		var args modelProfileControlArguments
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) == nil && len(fields) == 2 && args.Capabilities != nil {
			model, ok := config.Models[args.ID]
			if !ok {
				return errors.New("harness: a capabilities patch requires an existing model")
			}
			// Merge capability fields, but replace explicitly supplied compound
			// declarations. In particular {} must really disable vision/thinking,
			// not retain the previous enabled flag or effort through Go's merge.
			var supplied map[string]json.RawMessage
			if err := json.Unmarshal(fields["capabilities"], &supplied); err != nil {
				return err
			}
			if _, ok := supplied["vision"]; ok {
				model.Capabilities.Vision = VisionCapabilities{}
			}
			if _, ok := supplied["reasoning"]; ok {
				model.Capabilities.Reasoning = ReasoningCapabilities{}
			}
			if err := decodeControlJSON(fields["capabilities"], &model.Capabilities); err != nil {
				return err
			}
			return ApplyModel(config, args.ID, model, false)
		}
		if args.AuxiliaryReasoning != nil || args.Reasoning != nil {
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 {
				return errors.New("harness: reasoning or auxiliary_reasoning must be the only profile change")
			}
			model, ok := config.Models[args.ID]
			if !ok {
				return errors.New("harness: auxiliary reasoning requires an existing model")
			}
			if args.AuxiliaryReasoning != nil {
				model.Capabilities.Reasoning.Auxiliary = *args.AuxiliaryReasoning
			} else {
				model.Capabilities.Reasoning = *args.Reasoning
			}
			return ApplyModel(config, args.ID, model, false)
		}
		caps := ModelCapabilities{}
		if args.Capabilities != nil {
			caps = *args.Capabilities
		}
		return ApplyModel(config, args.ID, ModelConfig{Provider: args.Provider, Key: args.Key, Model: args.Model, Capabilities: caps}, args.MakeDefault)
	case ControlRemoveModel:
		var args struct {
			ID         string `json:"id"`
			NewDefault string `json:"new_default,omitempty"`
		}
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		_, err := RemoveModels(config, []string{args.ID}, args.NewDefault)
		return err
	case ControlSetAutoPolicy:
		var args autoControlArguments
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		config.Auto = args.AutoConfig
		if args.DefaultSelection != nil {
			selection := strings.TrimSpace(*args.DefaultSelection)
			if selection != "" && selection != "auto" {
				if _, ok := config.Models[selection]; !ok {
					return errors.New("harness: default selection must name a configured model or auto")
				}
			}
			if selection == "auto" && !config.Auto.Enabled {
				return errors.New("harness: auto default selection requires auto to be enabled")
			}
			config.DefaultSelection = selection
		}
	case ControlSetHTTPPolicy:
		var args HTTPConfig
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		config.HTTP = args
	case ControlSetChannelPolicy:
		var args ChannelsConfig
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		if err := args.validate(); err != nil {
			return err
		}
		config.Channels = args
	case ControlSetMemoryPolicy:
		var args MemoryConfig
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		config.Memory = args
	case ControlSetPromptPolicy:
		var args struct {
			SystemPrompt string   `json:"system_prompt"`
			Instructions []string `json:"instructions"`
		}
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		if len(args.SystemPrompt) > 64<<10 || len(args.Instructions) > 16 {
			return errors.New("harness: prompt policy exceeds its bound")
		}
		config.SystemPrompt = args.SystemPrompt
		config.Instructions = append([]string(nil), args.Instructions...)
	case ControlSetCollaboration:
		var args CollaborationSettings
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		config.Collaboration = args
	case ControlSetRuntimeLimits:
		var args struct {
			ModelRetries          *int `json:"model_retries"`
			AutoCompactAt         *int `json:"auto_compact_percent"`
			HistoryPageSize       *int `json:"history_page_size"`
			MaxContextBytes       *int `json:"max_context_bytes"`
			MaxOutputTokens       *int `json:"max_output_tokens"`
			MaxToolResult         *int `json:"max_tool_result_bytes"`
			TaskSummaryBytes      *int `json:"task_summary_bytes"`
			ExtensionContextChars *int `json:"extension_max_context_chars"`
			ExtensionModelCalls   *int `json:"extension_max_model_calls_per_turn"`
		}
		if err := decodeControlJSON(raw, &args); err != nil {
			return err
		}
		if args.AutoCompactAt != nil {
			config.AutoCompactAt = *args.AutoCompactAt
		}
		if args.ModelRetries != nil {
			config.ModelRetries = *args.ModelRetries
		}
		if args.HistoryPageSize != nil {
			config.HistoryPageSize = *args.HistoryPageSize
		}
		if args.MaxContextBytes != nil {
			config.MaxContextBytes = *args.MaxContextBytes
		}
		if args.MaxOutputTokens != nil {
			config.MaxOutputTokens = *args.MaxOutputTokens
		}
		if args.MaxToolResult != nil {
			config.MaxToolResult = *args.MaxToolResult
		}
		if args.TaskSummaryBytes != nil {
			config.TaskSummaryBytes = *args.TaskSummaryBytes
		}
		if args.ExtensionContextChars != nil {
			config.Extensions.MaxContextChars = *args.ExtensionContextChars
		}
		if args.ExtensionModelCalls != nil {
			config.Extensions.MaxModelCallsPerTurn = *args.ExtensionModelCalls
		}
	case ControlSetSkillPolicy:
		var args struct {
			ID               string `json:"id"`
			Enabled          *bool  `json:"enabled"`
			TrustedWorkspace string `json:"trusted_workspace,omitempty"`
			Trusted          *bool  `json:"trusted"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || args.ID == "" && (args.TrustedWorkspace == "" || args.Trusted == nil) || args.ID != "" && !validCapabilityID(args.ID) {
			return errors.New("harness: invalid skill policy")
		}
		if args.ID == "" {
			if config.Skills.TrustedWorkspaces == nil {
				config.Skills.TrustedWorkspaces = map[string]bool{}
			}
			config.Skills.TrustedWorkspaces[args.TrustedWorkspace] = *args.Trusted
			return config.Validate()
		}
		if args.Enabled != nil {
			if config.Skills.Enabled == nil {
				config.Skills.Enabled = map[string]bool{}
			}
			config.Skills.Enabled[args.ID] = *args.Enabled
		}
		if args.TrustedWorkspace != "" && args.Trusted != nil {
			if config.Skills.TrustedWorkspaces == nil {
				config.Skills.TrustedWorkspaces = map[string]bool{}
			}
			config.Skills.TrustedWorkspaces[args.TrustedWorkspace] = *args.Trusted
		}
	case ControlSetExtensionPolicy:
		var args struct {
			ID               string            `json:"id"`
			Enabled          *bool             `json:"enabled"`
			Required         *bool             `json:"required"`
			TrustedWorkspace string            `json:"trusted_workspace,omitempty"`
			Trusted          *bool             `json:"trusted"`
			Settings         map[string]any    `json:"settings,omitempty"`
			EnvFrom          map[string]string `json:"env_from,omitempty"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || args.ID == "" && (args.TrustedWorkspace == "" || args.Trusted == nil) || args.ID != "" && !validCapabilityID(args.ID) {
			return errors.New("harness: invalid extension policy")
		}
		if args.ID == "" {
			if config.Extensions.TrustedWorkspaces == nil {
				config.Extensions.TrustedWorkspaces = map[string]bool{}
			}
			if *args.Trusted {
				config.Extensions.TrustedWorkspaces[args.TrustedWorkspace] = true
			} else {
				delete(config.Extensions.TrustedWorkspaces, args.TrustedWorkspace)
			}
			return config.Validate()
		}
		if args.Enabled != nil {
			if config.Extensions.Enabled == nil {
				config.Extensions.Enabled = map[string]bool{}
			}
			config.Extensions.Enabled[args.ID] = *args.Enabled
		}
		if args.Required != nil {
			if config.Extensions.Required == nil {
				config.Extensions.Required = map[string]bool{}
			}
			if *args.Required {
				config.Extensions.Required[args.ID] = true
			} else {
				delete(config.Extensions.Required, args.ID)
			}
		}
		if args.TrustedWorkspace != "" && args.Trusted != nil {
			if config.Extensions.TrustedWorkspaces == nil {
				config.Extensions.TrustedWorkspaces = map[string]bool{}
			}
			if *args.Trusted {
				config.Extensions.TrustedWorkspaces[args.TrustedWorkspace] = true
			} else {
				delete(config.Extensions.TrustedWorkspaces, args.TrustedWorkspace)
			}
		}
		if args.Settings != nil {
			if config.Extensions.Settings == nil {
				config.Extensions.Settings = map[string]map[string]any{}
			}
			config.Extensions.Settings[args.ID] = args.Settings
		}
		if args.EnvFrom != nil {
			if config.Extensions.EnvFrom == nil {
				config.Extensions.EnvFrom = map[string]map[string]string{}
			}
			config.Extensions.EnvFrom[args.ID] = args.EnvFrom
		}
	case ControlSetMCPPolicy:
		var args struct {
			ID                string   `json:"id"`
			Enabled           *bool    `json:"enabled"`
			TrustReadOnlyHint *bool    `json:"trust_read_only_hint"`
			HiddenTools       []string `json:"hidden_tools"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || strings.TrimSpace(args.ID) == "" {
			return errors.New("harness: invalid MCP policy")
		}
		server, ok := config.MCPServers[args.ID]
		if !ok {
			return errors.New("harness: MCP server is not configured")
		}
		if args.Enabled != nil {
			server.Enabled = *args.Enabled
		}
		if args.TrustReadOnlyHint != nil {
			server.TrustReadOnlyHint = *args.TrustReadOnlyHint
		}
		if args.HiddenTools != nil {
			server.HiddenTools = append([]string(nil), args.HiddenTools...)
		}
		config.MCPServers[args.ID] = server
	case ControlRestartMCP:
		// The runtime restart is performed by applyRecordLocked. This branch
		// keeps planning strict without mutating configuration during preview.
	case ControlImportSkill:
		var args struct {
			Source string `json:"source"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || !filepath.IsAbs(args.Source) {
			return errors.New("harness: Skill import source must be absolute")
		}
		if _, err := readSkill(args.Source, "user"); err != nil {
			return fmt.Errorf("harness: invalid skill folder: %w", err)
		}
	case ControlRemoveSkill:
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || !validCapabilityID(args.ID) {
			return errors.New("harness: Skill id is required")
		}
	case ControlImportExtension:
		var args struct {
			Source string `json:"source"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || !filepath.IsAbs(args.Source) {
			return errors.New("harness: Extension import source must be absolute")
		}
		if _, err := CheckExtension(args.Source); err != nil {
			return err
		}
	case ControlRemoveExtension, ControlRestartExtension, ControlClearExtensionData:
		var args struct {
			ID string `json:"id"`
		}
		if err := decodeControlJSON(raw, &args); err != nil || !validExtensionID(args.ID) {
			return errors.New("harness: Extension id is required")
		}
	default:
		return fmt.Errorf("harness: unsupported config control operation %q", kind)
	}
	if err := validatePlatformNetwork(*config, kind); err != nil {
		return err
	}
	return config.Validate()
}

func configResourceValue(config Config, resource string) any {
	switch resource {
	case "config.delivery":
		return config.Delivery
	case "config.public_web":
		return config.PublicWeb.Effective()
	case "config.attachments":
		return config.Attachments.effective()
	case "config.server":
		return config.Server
	case "config.providers":
		return config.Providers
	case "config.models":
		return map[string]any{"models": config.Models, "default_model": config.DefaultModel, "default_selection": config.DefaultSelection, "auto": config.Auto, "collaboration": config.Collaboration}
	case "config.auto":
		selection := config.DefaultSelection
		return autoControlArguments{AutoConfig: config.Auto, DefaultSelection: &selection}
	case "config.http":
		return config.HTTP
	case "config.channels":
		return config.Channels
	case "config.memory":
		return config.Memory
	case "config.prompt":
		return map[string]any{"system_prompt": config.SystemPrompt, "instructions": config.Instructions}
	case "config.collaboration":
		return config.Collaboration
	case "config.limits":
		return map[string]any{"history_page_size": config.HistoryPageSize, "max_context_bytes": config.MaxContextBytes,
			"extension_max_context_chars": config.Extensions.MaxContextChars, "extension_max_model_calls_per_turn": config.Extensions.MaxModelCallsPerTurn,
			"model_retries":     config.ModelRetries,
			"max_output_tokens": config.MaxOutputTokens, "max_tool_result_bytes": config.MaxToolResult, "auto_compact_percent": autoCompactPercent(config), "task_summary_bytes": config.TaskSummaryBytes}
	case "config.workspace":
		return config.Workspace
	case "config." + ControlSetPermission:
		return effectiveExecutionSettings(config).PermissionMode
	case "config." + ControlSetExecutionProfile:
		settings := effectiveExecutionSettings(config)
		return map[string]any{"mode": settings.PermissionMode, "scope": EffectiveFileScope(config), "allowed_roots": config.ComputerRoots, "blocked_paths": config.Execution.BlockedPaths, "network_policy": settings.NetworkPolicy}
	case "config." + ControlSetSandbox:
		return effectiveExecutionSettings(config).SandboxMode
	case "config." + ControlSetNetwork:
		return effectiveExecutionSettings(config).NetworkPolicy
	case "config." + ControlSetFileScope:
		return map[string]any{"scope": EffectiveFileScope(config), "roots": config.ComputerRoots, "blocked": config.Execution.BlockedPaths}
	case "config." + ControlSetTool:
		return effectiveBuiltinToolSettings(config)
	case "config." + ControlSetCommandPolicy:
		return config.Commands
	default:
		if strings.HasPrefix(resource, "config.model:") {
			model, ok := config.Models[strings.TrimPrefix(resource, "config.model:")]
			if !ok {
				return nil
			}
			return model
		}
		if strings.HasPrefix(resource, "config.skill:") {
			id := strings.TrimPrefix(resource, "config.skill:")
			return map[string]any{"id": id, "enabled": config.Skills.Enabled[id], "trusted_workspaces": config.Skills.TrustedWorkspaces}
		}
		if strings.HasPrefix(resource, "config.extension:") {
			id := strings.TrimPrefix(resource, "config.extension:")
			return map[string]any{"id": id, "enabled": config.Extensions.Enabled[id], "required": config.Extensions.Required[id], "trusted_workspaces": config.Extensions.TrustedWorkspaces}
		}
		if strings.HasPrefix(resource, "config.mcp:") {
			id := strings.TrimPrefix(resource, "config.mcp:")
			server := config.MCPServers[id]
			return map[string]any{"id": id, "enabled": server.Enabled, "trust_read_only_hint": server.TrustReadOnlyHint, "hidden_tools": server.HiddenTools}
		}
		return nil
	}
}

func setConfigResource(config *Config, resource string, raw json.RawMessage) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	switch resource {
	case "config.providers":
		if err := json.Unmarshal(raw, &config.Providers); err != nil {
			return err
		}
	case "config.delivery":
		if err := json.Unmarshal(raw, &config.Delivery); err != nil {
			return err
		}
	case "config.attachments":
		if err := json.Unmarshal(raw, &config.Attachments); err != nil {
			return err
		}
	case "config.public_web":
		var next PublicWebConfig
		if err := json.Unmarshal(raw, &next); err != nil {
			return err
		}
		config.PublicWeb = next
	case "config.server":
		if err := json.Unmarshal(raw, &config.Server); err != nil {
			return err
		}
	case "config." + ControlSetPermission:
		var mode PermissionMode
		if err := json.Unmarshal(raw, &mode); err != nil {
			return err
		}
		config.Execution.PermissionMode = mode
	case "config." + ControlSetExecutionProfile:
		var profile struct {
			Mode          PermissionMode `json:"mode"`
			Scope         FileScope      `json:"scope"`
			AllowedRoots  []string       `json:"allowed_roots"`
			BlockedPaths  []string       `json:"blocked_paths"`
			NetworkPolicy NetworkPolicy  `json:"network_policy"`
		}
		if err := json.Unmarshal(raw, &profile); err != nil {
			return err
		}
		config.Execution.PermissionMode = profile.Mode
		config.Execution.FileScope, config.Execution.AdditionalRoots = profile.Scope, profile.AllowedRoots
		config.ComputerFiles = profile.Scope != FileScopeWorkspace
		config.ComputerRoots, config.Execution.BlockedPaths = append([]string(nil), profile.AllowedRoots...), profile.BlockedPaths
		config.Execution.NetworkPolicy = profile.NetworkPolicy
	case "config." + ControlSetSandbox:
		var mode SandboxMode
		if err := json.Unmarshal(raw, &mode); err != nil {
			return err
		}
		config.Execution.SandboxMode = mode
	case "config." + ControlSetNetwork:
		var policy NetworkPolicy
		if err := json.Unmarshal(raw, &policy); err != nil {
			return err
		}
		config.Execution.NetworkPolicy = policy
	case "config." + ControlSetFileScope:
		var data struct {
			Scope   FileScope `json:"scope"`
			Roots   []string  `json:"roots"`
			Blocked []string  `json:"blocked"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return err
		}
		config.Execution.FileScope, config.Execution.AdditionalRoots, config.ComputerRoots, config.Execution.BlockedPaths = data.Scope, data.Roots, append([]string(nil), data.Roots...), data.Blocked
		config.ComputerFiles = data.Scope != FileScopeWorkspace
	case "config." + ControlSetTool:
		var tools BuiltinToolSettings
		if err := json.Unmarshal(raw, &tools); err != nil {
			return err
		}
		config.Tools = tools
		config.Commands.Enabled, config.HTTP.Enabled = tools.CommandRun, tools.Network
	case "config." + ControlSetCommandPolicy:
		var policy CommandConfig
		if err := json.Unmarshal(raw, &policy); err != nil {
			return err
		}
		config.Commands = policy
	case "config.workspace":
		var workspace string
		if err := json.Unmarshal(raw, &workspace); err != nil {
			return err
		}
		resolved, err := canonicalDir(workspace)
		if err != nil {
			return err
		}
		config.Workspace = resolved
	case "config.models":
		var data struct {
			Models           map[string]ModelConfig `json:"models"`
			DefaultModel     string                 `json:"default_model"`
			DefaultSelection string                 `json:"default_selection"`
			Auto             AutoConfig             `json:"auto"`
			Collaboration    CollaborationSettings  `json:"collaboration"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return err
		}
		config.Models, config.DefaultModel, config.DefaultSelection, config.Auto, config.Collaboration = data.Models, data.DefaultModel, data.DefaultSelection, data.Auto, data.Collaboration
	case "config.auto":
		var args autoControlArguments
		if err := json.Unmarshal(raw, &args); err != nil {
			return err
		}
		config.Auto = args.AutoConfig
		if args.DefaultSelection != nil {
			config.DefaultSelection = *args.DefaultSelection
		}
	case "config.http":
		return decodeAndAssign(raw, &config.HTTP)
	case "config.channels":
		return decodeAndAssign(raw, &config.Channels)
	case "config.memory":
		return decodeAndAssign(raw, &config.Memory)
	case "config.prompt":
		var data struct {
			SystemPrompt string   `json:"system_prompt"`
			Instructions []string `json:"instructions"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return err
		}
		config.SystemPrompt, config.Instructions = data.SystemPrompt, append([]string(nil), data.Instructions...)
	case "config.collaboration":
		return decodeAndAssign(raw, &config.Collaboration)
	case "config.limits":
		var data struct {
			ModelRetries          int `json:"model_retries"`
			AutoCompactAt         int `json:"auto_compact_percent"`
			HistoryPageSize       int `json:"history_page_size"`
			MaxContextBytes       int `json:"max_context_bytes"`
			MaxOutputTokens       int `json:"max_output_tokens"`
			MaxToolResult         int `json:"max_tool_result_bytes"`
			TaskSummaryBytes      int `json:"task_summary_bytes"`
			ExtensionContextChars int `json:"extension_max_context_chars"`
			ExtensionModelCalls   int `json:"extension_max_model_calls_per_turn"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return err
		}
		config.HistoryPageSize, config.MaxContextBytes, config.MaxOutputTokens = data.HistoryPageSize, data.MaxContextBytes, data.MaxOutputTokens
		config.MaxToolResult, config.TaskSummaryBytes = data.MaxToolResult, data.TaskSummaryBytes
		config.AutoCompactAt = data.AutoCompactAt
		config.ModelRetries = data.ModelRetries
		config.Extensions.MaxContextChars = data.ExtensionContextChars
		config.Extensions.MaxModelCallsPerTurn = data.ExtensionModelCalls
	default:
		if strings.HasPrefix(resource, "config.model:") {
			id := strings.TrimPrefix(resource, "config.model:")
			if string(bytes.TrimSpace(raw)) == "null" {
				delete(config.Models, id)
				return config.Validate()
			}
			var model ModelConfig
			if err := json.Unmarshal(raw, &model); err != nil {
				return err
			}
			if config.Models == nil {
				config.Models = map[string]ModelConfig{}
			}
			config.Models[id] = model
			return config.Validate()
		}
		if strings.HasPrefix(resource, "config.skill:") || strings.HasPrefix(resource, "config.extension:") || strings.HasPrefix(resource, "config.mcp:") {
			var data struct {
				ID                string          `json:"id"`
				Enabled           bool            `json:"enabled"`
				Required          bool            `json:"required"`
				TrustedWorkspaces map[string]bool `json:"trusted_workspaces"`
				TrustReadOnlyHint bool            `json:"trust_read_only_hint"`
				HiddenTools       []string        `json:"hidden_tools"`
			}
			if string(bytes.TrimSpace(raw)) == "null" {
				return errors.New("harness: protected capability snapshot is missing")
			}
			if err := json.Unmarshal(raw, &data); err != nil {
				return err
			}
			if strings.HasPrefix(resource, "config.skill:") {
				if config.Skills.Enabled == nil {
					config.Skills.Enabled = map[string]bool{}
				}
				config.Skills.Enabled[data.ID] = data.Enabled
				config.Skills.TrustedWorkspaces = data.TrustedWorkspaces
			} else if strings.HasPrefix(resource, "config.extension:") {
				if config.Extensions.Enabled == nil {
					config.Extensions.Enabled = map[string]bool{}
				}
				config.Extensions.Enabled[data.ID] = data.Enabled
				if config.Extensions.Required == nil {
					config.Extensions.Required = map[string]bool{}
				}
				if data.Required {
					config.Extensions.Required[data.ID] = true
				} else {
					delete(config.Extensions.Required, data.ID)
				}
				config.Extensions.TrustedWorkspaces = data.TrustedWorkspaces
			} else {
				server, ok := config.MCPServers[data.ID]
				if !ok {
					return errors.New("harness: MCP server is not configured")
				}
				server.Enabled, server.TrustReadOnlyHint, server.HiddenTools = data.Enabled, data.TrustReadOnlyHint, data.HiddenTools
				config.MCPServers[data.ID] = server
			}
			return config.Validate()
		}
		return fmt.Errorf("harness: unsupported protected resource %q", resource)
	}
	return config.Validate()
}

func decodeAndAssign(raw []byte, target any) error {
	if err := json.Unmarshal(raw, target); err != nil {
		return err
	}
	return nil
}

func catalogEntry(kind string) (ControlCatalogEntry, bool) {
	for _, entry := range ControlOperationCatalog() {
		if entry.Kind == kind {
			return entry, true
		}
	}
	return ControlCatalogEntry{}, false
}

func isConversationControl(kind string) bool {
	return kind == ControlSetConversationModel || kind == ControlRenameConversation || kind == ControlPinConversation || kind == ControlSetConversationCapabilities
}

func decodeControlJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("harness: invalid control arguments: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("harness: control arguments must contain one JSON object")
	}
	return nil
}

func controlDigest(record controlRecord) string {
	payload := struct {
		Kind          string
		Arguments     json.RawMessage
		Resources     []string
		Revisions     map[string]uint64
		Before, After map[string]json.RawMessage
	}{record.Operation.Kind, record.Arguments, record.Operation.ResourceIDs, record.Operation.ExpectedRevisions, record.Before, record.After}
	raw, _ := json.Marshal(payload)
	return contentHash(raw)
}

func cloneControlOperation(operation ControlOperation) ControlOperation {
	clone := operation
	clone.ResourceIDs = append([]string(nil), operation.ResourceIDs...)
	clone.ExpectedRevisions = cloneRevisions(operation.ExpectedRevisions)
	clone.NewRevisions = cloneRevisions(operation.NewRevisions)
	clone.OldValues = cloneStrings(operation.OldValues)
	clone.ProposedValues = cloneStrings(operation.ProposedValues)
	return clone
}

func cloneRevisions(values map[string]uint64) map[string]uint64 {
	if values == nil {
		return nil
	}
	result := make(map[string]uint64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
func cloneRawMap(values map[string]json.RawMessage) map[string]json.RawMessage {
	if values == nil {
		return nil
	}
	result := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}
func redactedValues(values map[string]json.RawMessage) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		result[key] = redactJSON(value)
	}
	return result
}
func redactJSON(raw []byte) string {
	if len(raw) > 512 {
		return "[redacted bounded value]"
	}
	return string(raw)
}
func redactID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:6] + "#" + shortHash(id)
}
func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:8]
}
func (service *ControlService) revisionLocked(resource string) uint64 {
	if value := service.revisions[resource]; value > 0 {
		return value
	}
	return 1
}

func (service *ControlService) loadJournal() error {
	file, err := os.Open(service.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, controlJournalMaxBytes+1))
	scanner.Buffer(make([]byte, 4096), 512<<10)
	for scanner.Scan() {
		var line controlJournalLine
		if json.Unmarshal(scanner.Bytes(), &line) != nil || line.Action == "" || line.Record.Operation.ID == "" || !validControlHash(line) {
			return ErrControlJournal
		}
		service.entries = append(service.entries, line)
		service.operations[line.Record.Operation.ID] = line.Record
		if line.Record.Operation.Status == ControlApplied {
			for resource, revision := range line.Record.Operation.NewRevisions {
				if revision > service.revisions[resource] {
					service.revisions[resource] = revision
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ErrControlJournal
	}
	return service.expireDirectLocked(time.Now())
}

// Direct settings gestures have an absolute lifetime. Re-rendering a card or
// restarting the Owner cannot extend it. Run-bound proposals keep their Run's
// lifecycle and may obtain a new confirmation credential separately.
func (service *ControlService) expireDirectLocked(now time.Time) error {
	for id, record := range service.operations {
		op := record.Operation
		if op.Status != ControlPending || op.Authorization != ControlUserDirect || op.AttemptID != "" {
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, op.CreatedAt)
		if err != nil || now.Before(created.Add(controlConfirmationTTL)) {
			continue
		}
		record.Operation.Status, record.Operation.ErrorCode = ControlStale, "expired"
		record.Operation.Recovery = "The direct setting request expired; start a new setting operation."
		if err := service.appendLocked("expired", record); err != nil {
			return err
		}
		service.operations[id] = record
		delete(service.confirmations, id)
		service.clearKeySecretLocked(id)
		service.clearMCPProfileLocked(id)
	}
	return nil
}

func validControlHash(line controlJournalLine) bool {
	payload, _ := json.Marshal(controlJournalPayload{Action: line.Action, Record: line.Record})
	sum := sha256.Sum256(payload)
	return hmac.Equal([]byte(line.Hash), []byte(hex.EncodeToString(sum[:])))
}

func (service *ControlService) appendLocked(action string, record controlRecord) error {
	payload := controlJournalPayload{Action: action, Record: record}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	line := controlJournalLine{Action: action, Record: record, Hash: hex.EncodeToString(sum[:])}
	encoded, err := json.Marshal(line)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if info, statErr := os.Stat(service.path); statErr == nil && info.Size()+int64(len(encoded)) > controlJournalMaxBytes {
		if err := service.compactLocked(); err != nil {
			return err
		}
	}
	file, err := os.OpenFile(service.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	service.entries = append(service.entries, line)
	if len(service.entries) > controlJournalMaxRecords {
		service.entries = service.entries[len(service.entries)-controlJournalMaxRecords:]
	}
	return nil
}

func (service *ControlService) compactLocked() error {
	keep := service.entries
	if len(keep) > controlJournalMaxRecords {
		keep = keep[len(keep)-controlJournalMaxRecords:]
	}
	temp, err := os.CreateTemp(filepath.Dir(service.path), ".control-journal-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	for _, line := range keep {
		raw, _ := json.Marshal(line)
		if _, err := temp.Write(append(raw, '\n')); err != nil {
			temp.Close()
			return err
		}
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, service.path)
}

func (service *ControlService) gateState() (bool, bool) {
	service.gate.mu.Lock()
	defer service.gate.mu.Unlock()
	return service.gate.controlBusy, service.gate.hostBusy > 0
}

// ControlCodeForTests is intentionally only useful to deterministic tests
// that rendered a confirmation. Production surfaces receive the code from
// RenderConfirmation and never from an operation or model tool result.
func (service *ControlService) ControlCodeForTests(ctx context.Context, operationID, conversationID, principal string) (string, error) {
	confirmation, err := service.RenderConfirmation(ctx, operationID, conversationID, principal)
	return confirmation.Code, err
}

func (service *ControlService) credentialBaseline() (string, error) {
	paths := []string{filepath.Join(service.core.Store.DataDir(), "channel-keys.json")}
	if service.core.Keys != nil && service.core.Keys.Path() != "" {
		paths = append(paths, service.core.Keys.Path())
	}
	var hashes []string
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			hashes = append(hashes, "")
			continue
		}
		if err != nil {
			return "", err
		}
		hashes = append(hashes, contentHash(raw))
	}
	return contentHash(mustJSON(hashes)), nil
}
