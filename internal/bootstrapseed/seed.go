// Package bootstrapseed implements the explicit, offline S1 bootstrap-seed
// import path. Import is never invoked by Current Store open or normal runtime
// startup; the production composition root must opt in through a dedicated
// operator command.
package bootstrapseed

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
	"unicode"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	SchemaVersionV1 = "freeagent.bootstrap-seed/v1"

	policyIDPrefix = "freeagent.policy."
	policyVersion  = "1"
	jsonMediaType  = "application/json"

	agentDefinitionDigestDomain     = "freeagent.agent-definition/v1"
	workspaceDefinitionDigestDomain = "freeagent.workspace-definition/v1"
	profileDefinitionDigestDomain   = "freeagent.profile-definition/v1"

	maxSeedBytes = 4 * moduleapi.MaxTextBytes
)

var (
	ErrInvalidSeed = errors.New("bootstrapseed: invalid seed")
	ErrArtifact    = errors.New("bootstrapseed: artifact verification failed")
	ErrConflict    = errors.New("bootstrapseed: existing bootstrap state conflicts")
)

var denyAllAuthorityCeiling = []byte(
	`{"effects":[],"filesystem_roots":[],"network_allowlist":[],"schema_version":"authority-ceiling/v1","secret_refs":[]}`,
)

// ActivationResolver is the only caller-supplied authority dependency. A
// seed's expected execution class and adapter identity are assertions passed
// to this resolver; they cannot create a local trust grant or registry entry.
type ActivationResolver interface {
	Resolve(
		context.Context,
		activationresolver.ResolveInput,
	) (currentstore.ActivateModuleInput, error)
}

// ModuleAssertion is the verified package lock and the seed's expected local
// assignment. A composition root first calls PrepareFile, then uses the actual
// package lock together with its own compiled allowlist and exact registry to
// construct an ActivationResolver.
type ModuleAssertion struct {
	ModuleID                string
	ExactVersion            string
	ArtifactDigest          string
	ArtifactSizeBytes       uint64
	ArtifactDirectory       string
	InstanceID              string
	ActivationRevision      uint64
	ExpectedExecutionClass  moduleapi.ExecutionClass
	ExpectedAdapterIdentity string
}

// DefaultAssembly identifies the minimal Agent/Workspace/Profile selected by
// the example seed. It is configuration output, not a hidden runtime default.
type DefaultAssembly struct {
	TenantID    string `json:"tenant_id"`
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	ProfileID   string `json:"profile_id"`
}

// Result contains only stable imported identities and frozen publication
// values. Runtime timestamps remain Store metadata and are intentionally not
// part of seed semantics.
type Result struct {
	SeedID          string
	SeedRevision    uint64
	Module          ModuleAssertion
	Installation    currentstore.ModuleInstallation
	Activation      currentstore.ModuleActivation
	PublishedBasis  controlcontract.PublishedBasis
	DefaultAssembly DefaultAssembly
}

// Prepared is an immutable, fully validated seed plus the exact artifact
// snapshot used to calculate its digest. It performs no Store write.
type Prepared struct {
	seed                   seedV1
	module                 ModuleAssertion
	manifestCanonical      []byte
	contexts               []preparedDeclarativeContext
	knowledgeContexts      []preparedKnowledgeContext
	memoryContexts         []preparedMemoryContext
	actions                []preparedActionProvider
	memoryGenesisCanonical []byte
	policies               []preparedPolicy
	agentRefs              []corecontract.AgentRef
	workspaceRefs          []corecontract.WorkspaceRef
	profileRefs            []corecontract.ProfileRef
	configCanonical        []byte
	configRef              string
	modelProfileRef        *corecontract.ModelProfileRef
	modelProfileCanonical  []byte
	authorityRef           string
	priceSnapshot          corecontract.ModelPriceSnapshotV1
}

type seedV1 struct {
	SchemaVersion               string                                       `json:"schema_version"`
	SeedID                      string                                       `json:"seed_id"`
	SeedRevision                uint64                                       `json:"seed_revision"`
	TenantID                    string                                       `json:"tenant_id"`
	Control                     controlSeed                                  `json:"control"`
	Catalog                     catalogSeed                                  `json:"catalog"`
	Definitions                 definitionsSeed                              `json:"definitions"`
	CompositeAgents             []controlcontract.CompositeAgentDefinitionV1 `json:"composite_agents,omitempty"`
	Policies                    []policySeed                                 `json:"policies"`
	Module                      moduleSeed                                   `json:"module"`
	ModelBinding                modelBindingSeed                             `json:"model_binding"`
	ModelProfile                *corecontract.ModelProfileV1                 `json:"model_profile,omitempty"`
	DeclarativeContextProviders []declarativeContextProviderSeed             `json:"declarative_context_providers,omitempty"`
	KnowledgeContextProviders   []knowledgeContextProviderSeed               `json:"knowledge_context_providers,omitempty"`
	MemoryContextProviders      []memoryContextProviderSeed                  `json:"memory_context_providers,omitempty"`
	ActionProviders             []actionProviderSeed                         `json:"action_providers,omitempty"`
	PriceSnapshot               priceSnapshotSeed                            `json:"price_snapshot"`
	DefaultAssembly             defaultAssemblySeed                          `json:"default_assembly"`
}

type controlSeed struct {
	SnapshotID      string `json:"snapshot_id"`
	Revision        uint64 `json:"revision"`
	PointerRevision uint64 `json:"pointer_revision"`
}

type catalogSeed struct {
	GenerationID string `json:"generation_id"`
	Generation   uint64 `json:"generation"`
}

type definitionsSeed struct {
	Agent                definitionSeed   `json:"agent"`
	Workspace            workspaceSeed    `json:"workspace"`
	Profile              profileSeed      `json:"profile"`
	AdditionalAgents     []definitionSeed `json:"additional_agents,omitempty"`
	AdditionalWorkspaces []workspaceSeed  `json:"additional_workspaces,omitempty"`
	AdditionalProfiles   []profileSeed    `json:"additional_profiles,omitempty"`
}

type definitionSeed struct {
	ID      string          `json:"id"`
	Version string          `json:"version"`
	Body    json.RawMessage `json:"body"`
}

type workspaceSeed struct {
	ID                string          `json:"id"`
	Version           string          `json:"version"`
	Body              json.RawMessage `json:"body"`
	BudgetPolicyAlias string          `json:"budget_policy_alias"`
}

type profileSeed struct {
	ID                    string          `json:"id"`
	Version               string          `json:"version"`
	Body                  json.RawMessage `json:"body"`
	ContextPolicyAlias    string          `json:"context_policy_alias"`
	CostPolicyAlias       string          `json:"cost_policy_alias"`
	SchedulingPolicyAlias string          `json:"scheduling_policy_alias"`
}

type policySeed struct {
	Alias      string                  `json:"alias"`
	PolicyType corecontract.PolicyType `json:"policy_type"`
	Body       json.RawMessage         `json:"body"`
}

type moduleSeed struct {
	InstallationID          string                   `json:"installation_id"`
	ModuleID                string                   `json:"module_id"`
	ExactVersion            string                   `json:"exact_version"`
	ArtifactDigest          string                   `json:"artifact_digest"`
	ArtifactSizeBytes       uint64                   `json:"artifact_size_bytes"`
	ArtifactRelativePath    string                   `json:"artifact_relative_path"`
	InstanceID              string                   `json:"instance_id"`
	ActivationRevision      uint64                   `json:"activation_revision"`
	ExpectedExecutionClass  moduleapi.ExecutionClass `json:"expected_execution_class"`
	ExpectedAdapterIdentity string                   `json:"expected_adapter_identity"`
}

type modelBindingSeed struct {
	Port          moduleapi.PortRef              `json:"port"`
	InstanceID    string                         `json:"instance_id"`
	Config        moduleapi.ModelBindingConfigV1 `json:"config"`
	FailurePolicy moduleapi.FailurePolicy        `json:"failure_policy"`
}

type declarativeContextProviderSeed struct {
	Module        moduleSeed              `json:"module"`
	Port          moduleapi.PortRef       `json:"port"`
	Config        json.RawMessage         `json:"config"`
	FailurePolicy moduleapi.FailurePolicy `json:"failure_policy"`
}

type knowledgeContextProviderSeed struct {
	Module           moduleSeed                            `json:"module"`
	Port             moduleapi.PortRef                     `json:"port"`
	Config           moduleapi.ContextBindingConfigV1      `json:"config"`
	AuthorityCeiling moduleapi.KnowledgeAuthorityCeilingV1 `json:"authority_ceiling"`
	FailurePolicy    moduleapi.FailurePolicy               `json:"failure_policy"`
}

type memoryContextProviderSeed struct {
	Module           moduleSeed                         `json:"module"`
	Port             moduleapi.PortRef                  `json:"port"`
	Config           moduleapi.ContextBindingConfigV1   `json:"config"`
	AuthorityCeiling moduleapi.MemoryAuthorityCeilingV1 `json:"authority_ceiling"`
	FailurePolicy    moduleapi.FailurePolicy            `json:"failure_policy"`
}

type actionProviderSeed struct {
	Module           moduleSeed                         `json:"module"`
	Port             moduleapi.PortRef                  `json:"port"`
	Config           moduleapi.ActionBindingConfigV1    `json:"config"`
	AuthorityCeiling moduleapi.ActionAuthorityCeilingV1 `json:"authority_ceiling"`
	FailurePolicy    moduleapi.FailurePolicy            `json:"failure_policy"`
}

type priceSnapshotSeed struct {
	SchemaVersion   string                     `json:"schema_version"`
	PriceSnapshotID string                     `json:"price_snapshot_id"`
	Provider        string                     `json:"provider"`
	Model           string                     `json:"model"`
	BillingVersion  string                     `json:"billing_version"`
	Currency        string                     `json:"currency"`
	PricingStatus   corecontract.PricingStatus `json:"pricing_status"`
	Pricing         json.RawMessage            `json:"pricing"`
}

type defaultAssemblySeed struct {
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	ProfileID   string `json:"profile_id"`
}

type preparedPolicy struct {
	alias     string
	ref       corecontract.PolicyRef
	canonical []byte
}

type preparedDeclarativeContext struct {
	seed                   declarativeContextProviderSeed
	module                 ModuleAssertion
	manifestCanonical      []byte
	configCanonical        []byte
	configRef              string
	staticContextCanonical []byte
	staticContextRef       string
}

type preparedKnowledgeContext struct {
	seed               knowledgeContextProviderSeed
	module             ModuleAssertion
	manifestCanonical  []byte
	configCanonical    []byte
	configRef          string
	authorityCanonical []byte
	authorityRef       string
}

type preparedMemoryContext struct {
	seed               memoryContextProviderSeed
	module             ModuleAssertion
	manifestCanonical  []byte
	configCanonical    []byte
	configRef          string
	authorityCanonical []byte
	authorityRef       string
}

type preparedActionProvider struct {
	seed               actionProviderSeed
	module             ModuleAssertion
	manifestCanonical  []byte
	configCanonical    []byte
	configRef          string
	authorityCanonical []byte
	authorityRef       string
}

// PrepareFile strictly reads one exact canonical JSON seed and anchors every
// artifact-relative path to the seed file's directory. Symlinked seed files
// are rejected so an operator-selected path cannot change identity silently.
func PrepareFile(seedPath string) (*Prepared, error) {
	if strings.TrimSpace(seedPath) == "" {
		return nil, fmt.Errorf("%w: seed path is required", ErrInvalidSeed)
	}
	absolute, err := filepath.Abs(seedPath)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve seed path: %v", ErrInvalidSeed, err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect seed path: %v", ErrInvalidSeed, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: seed path must be an ordinary non-symlink file", ErrInvalidSeed)
	}
	if info.Size() <= 0 || info.Size() > int64(maxSeedBytes) {
		return nil, fmt.Errorf("%w: seed size must be between 1 and %d bytes", ErrInvalidSeed, maxSeedBytes)
	}
	canonical, err := os.ReadFile(absolute)
	if err != nil {
		return nil, fmt.Errorf("%w: read seed: %v", ErrInvalidSeed, err)
	}
	return Prepare(canonical, filepath.Dir(absolute))
}

// Prepare validates caller-owned seed bytes and safely scans the referenced
// artifact directory. artifactBase is a local operator input and never enters
// a seed digest or persisted Control/Catalog value.
func Prepare(canonical []byte, artifactBase string) (*Prepared, error) {
	if len(canonical) == 0 || len(canonical) > maxSeedBytes {
		return nil, fmt.Errorf("%w: seed size must be between 1 and %d bytes", ErrInvalidSeed, maxSeedBytes)
	}
	checked, err := moduleapi.CanonicalJSONWithLimits(
		canonical,
		moduleapi.CanonicalJSONLimits{MaxBytes: maxSeedBytes, MaxDepth: 128, MaxNodes: maxSeedBytes},
	)
	if err != nil || !bytes.Equal(checked, canonical) || canonical[0] != '{' {
		return nil, fmt.Errorf("%w: seed must be an exact RFC 8785 canonical JSON object", ErrInvalidSeed)
	}
	var seed seedV1
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&seed); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrInvalidSeed, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing JSON", ErrInvalidSeed)
	}
	return prepareSeed(seed, artifactBase)
}

// ModelAssertion returns the verified executable model package lock.
func (prepared *Prepared) ModelAssertion() ModuleAssertion {
	if prepared == nil {
		return ModuleAssertion{}
	}
	return prepared.module
}

// ModuleAssertions returns model first, followed by declarative, Knowledge,
// Memory and Action providers in their respective seed order. The returned
// slice is detached from Prepared.
func (prepared *Prepared) ModuleAssertions() []ModuleAssertion {
	if prepared == nil {
		return nil
	}
	assertions := make(
		[]ModuleAssertion,
		0,
		1+len(prepared.contexts)+len(prepared.knowledgeContexts)+
			len(prepared.memoryContexts)+len(prepared.actions),
	)
	assertions = append(assertions, prepared.module)
	for _, provider := range prepared.contexts {
		assertions = append(assertions, provider.module)
	}
	for _, provider := range prepared.knowledgeContexts {
		assertions = append(assertions, provider.module)
	}
	for _, provider := range prepared.memoryContexts {
		assertions = append(assertions, provider.module)
	}
	for _, provider := range prepared.actions {
		assertions = append(assertions, provider.module)
	}
	return assertions
}

// ModuleAssertion is the backward-compatible name for ModelAssertion.
func (prepared *Prepared) ModuleAssertion() ModuleAssertion {
	return prepared.ModelAssertion()
}

// DefaultAssembly returns the explicit minimal assembly selected by the seed.
func (prepared *Prepared) DefaultAssembly() DefaultAssembly {
	if prepared == nil {
		return DefaultAssembly{}
	}
	return DefaultAssembly{
		TenantID:    prepared.seed.TenantID,
		WorkspaceID: prepared.seed.DefaultAssembly.WorkspaceID,
		AgentID:     prepared.seed.DefaultAssembly.AgentID,
		ProfileID:   prepared.seed.DefaultAssembly.ProfileID,
	}
}

// Import writes through normal Current Store APIs only. It is idempotent for
// the exact same seed and fails closed if a current publication already means
// something else. No raw database handle, migration, repair, or hidden Store
// is created here.
func (prepared *Prepared) Import(
	ctx context.Context,
	store *currentstore.Store,
	resolver ActivationResolver,
) (Result, error) {
	if prepared == nil {
		return Result{}, fmt.Errorf("%w: prepared seed is nil", ErrInvalidSeed)
	}
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrInvalidSeed)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if store == nil {
		return Result{}, fmt.Errorf("%w: Current Store is nil", ErrInvalidSeed)
	}
	if isNilInterface(resolver) {
		return Result{}, fmt.Errorf("%w: local activation resolver is nil", ErrInvalidSeed)
	}

	for _, policy := range prepared.policies {
		if _, err := putContent(
			ctx,
			store,
			currentstore.ContentPolicy,
			policy.ref.Digest,
			policy.canonical,
		); err != nil {
			return Result{}, fmt.Errorf("bootstrapseed: persist policy %q: %w", policy.alias, err)
		}
	}
	if _, err := putContent(
		ctx,
		store,
		currentstore.ContentConfig,
		prepared.configRef,
		prepared.configCanonical,
	); err != nil {
		return Result{}, fmt.Errorf("bootstrapseed: persist model Binding config: %w", err)
	}
	if prepared.modelProfileRef != nil {
		if _, err := putContent(
			ctx,
			store,
			currentstore.ContentConfig,
			prepared.modelProfileRef.Digest,
			prepared.modelProfileCanonical,
		); err != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: persist ModelProfile: %w",
				err,
			)
		}
	}
	for index, provider := range prepared.contexts {
		if _, err := putContent(
			ctx,
			store,
			currentstore.ContentConfig,
			provider.configRef,
			provider.configCanonical,
		); err != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: persist declarative context provider %d config: %w",
				index,
				err,
			)
		}
		if _, err := putContent(
			ctx,
			store,
			currentstore.ContentStaticContext,
			provider.staticContextRef,
			provider.staticContextCanonical,
		); err != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: persist declarative context provider %d content: %w",
				index,
				err,
			)
		}
	}
	for index, provider := range prepared.knowledgeContexts {
		if _, err := putContent(
			ctx,
			store,
			currentstore.ContentConfig,
			provider.configRef,
			provider.configCanonical,
		); err != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: persist knowledge context provider %d config: %w",
				index,
				err,
			)
		}
		if _, err := putContent(
			ctx,
			store,
			currentstore.ContentAuthorityCeiling,
			provider.authorityRef,
			provider.authorityCanonical,
		); err != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: persist knowledge context provider %d authority ceiling: %w",
				index,
				err,
			)
		}
	}
	for index, provider := range prepared.memoryContexts {
		if _, err := putContent(
			ctx,
			store,
			currentstore.ContentConfig,
			provider.configRef,
			provider.configCanonical,
		); err != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: persist Memory context provider %d config: %w",
				index,
				err,
			)
		}
		if _, err := putContent(
			ctx,
			store,
			currentstore.ContentAuthorityCeiling,
			provider.authorityRef,
			provider.authorityCanonical,
		); err != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: persist Memory context provider %d authority ceiling: %w",
				index,
				err,
			)
		}
	}
	for index, provider := range prepared.actions {
		if _, err := putContent(
			ctx,
			store,
			currentstore.ContentConfig,
			provider.configRef,
			provider.configCanonical,
		); err != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: persist Action provider %d config: %w",
				index,
				err,
			)
		}
		if _, err := putContent(
			ctx,
			store,
			currentstore.ContentAuthorityCeiling,
			provider.authorityRef,
			provider.authorityCanonical,
		); err != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: persist Action provider %d authority ceiling: %w",
				index,
				err,
			)
		}
	}
	if _, err := putContent(
		ctx,
		store,
		currentstore.ContentAuthorityCeiling,
		prepared.authorityRef,
		denyAllAuthorityCeiling,
	); err != nil {
		return Result{}, fmt.Errorf("bootstrapseed: persist deny-all authority ceiling: %w", err)
	}
	if _, err := store.PutModelPriceSnapshot(ctx, prepared.priceSnapshot); err != nil {
		return Result{}, fmt.Errorf("bootstrapseed: persist model price snapshot: %w", err)
	}

	installation, activation, provider, err := prepared.installAndActivate(
		ctx,
		store,
		resolver,
		prepared.seed.Module,
		prepared.module,
		prepared.manifestCanonical,
	)
	if err != nil {
		return Result{}, fmt.Errorf("bootstrapseed: model provider: %w", err)
	}
	contextProviders := make([]moduleapi.ActivatedModuleRef, len(prepared.contexts))
	for index, declarative := range prepared.contexts {
		_, _, contextProvider, activateErr := prepared.installAndActivate(
			ctx,
			store,
			resolver,
			declarative.seed.Module,
			declarative.module,
			declarative.manifestCanonical,
		)
		if activateErr != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: declarative context provider %d: %w",
				index,
				activateErr,
			)
		}
		contextProviders[index] = contextProvider
	}
	knowledgeProviders := make(
		[]moduleapi.ActivatedModuleRef,
		len(prepared.knowledgeContexts),
	)
	for index, knowledge := range prepared.knowledgeContexts {
		_, _, knowledgeProvider, activateErr := prepared.installAndActivate(
			ctx,
			store,
			resolver,
			knowledge.seed.Module,
			knowledge.module,
			knowledge.manifestCanonical,
		)
		if activateErr != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: knowledge context provider %d: %w",
				index,
				activateErr,
			)
		}
		knowledgeProviders[index] = knowledgeProvider
	}
	memoryProviders := make(
		[]moduleapi.ActivatedModuleRef,
		len(prepared.memoryContexts),
	)
	for index, memory := range prepared.memoryContexts {
		_, _, memoryProvider, activateErr := prepared.installAndActivate(
			ctx,
			store,
			resolver,
			memory.seed.Module,
			memory.module,
			memory.manifestCanonical,
		)
		if activateErr != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: Memory context provider %d: %w",
				index,
				activateErr,
			)
		}
		memoryProviders[index] = memoryProvider
	}
	actionProviders := make(
		[]moduleapi.ActivatedModuleRef,
		len(prepared.actions),
	)
	for index, action := range prepared.actions {
		_, _, actionProvider, activateErr := prepared.installAndActivate(
			ctx,
			store,
			resolver,
			action.seed.Module,
			action.module,
			action.manifestCanonical,
		)
		if activateErr != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: Action provider %d: %w",
				index,
				activateErr,
			)
		}
		actionProviders[index] = actionProvider
	}
	if len(prepared.memoryContexts) != 0 {
		if len(prepared.memoryGenesisCanonical) == 0 {
			return Result{}, fmt.Errorf(
				"%w: Memory provider has no prepared genesis snapshot",
				ErrConflict,
			)
		}
		if _, err := store.PutAgentMemoryGenesis(
			ctx,
			prepared.memoryGenesisCanonical,
		); err != nil {
			return Result{}, fmt.Errorf(
				"bootstrapseed: persist Agent Memory genesis: %w",
				err,
			)
		}
	}

	control, controlRef, controlCanonical, catalog, catalogRef, catalogCanonical, err :=
		prepared.buildPublication(
			provider,
			contextProviders,
			knowledgeProviders,
			memoryProviders,
			actionProviders,
		)
	if err != nil {
		return Result{}, err
	}
	basis, err := prepared.publishOrVerify(
		ctx,
		store,
		control,
		controlRef,
		controlCanonical,
		catalog,
		catalogRef,
		catalogCanonical,
	)
	if err != nil {
		return Result{}, err
	}

	return Result{
		SeedID:          prepared.seed.SeedID,
		SeedRevision:    prepared.seed.SeedRevision,
		Module:          prepared.module,
		Installation:    installation,
		Activation:      activation,
		PublishedBasis:  basis,
		DefaultAssembly: prepared.DefaultAssembly(),
	}, nil
}

func prepareSeed(seed seedV1, artifactBase string) (*Prepared, error) {
	if seed.SchemaVersion != SchemaVersionV1 {
		return nil, fmt.Errorf("%w: schema_version must be %q", ErrInvalidSeed, SchemaVersionV1)
	}
	for name, value := range map[string]string{
		"seed ID":               seed.SeedID,
		"tenant ID":             seed.TenantID,
		"Control snapshot ID":   seed.Control.SnapshotID,
		"Catalog generation ID": seed.Catalog.GenerationID,
	} {
		if err := validateOpaque(name, value); err != nil {
			return nil, err
		}
	}
	if seed.SeedRevision == 0 || seed.Control.Revision == 0 ||
		seed.Catalog.Generation == 0 {
		return nil, fmt.Errorf("%w: seed, Control and Catalog revisions must be positive", ErrInvalidSeed)
	}
	if seed.Control.PointerRevision != 1 {
		return nil, fmt.Errorf("%w: fresh S1 bootstrap pointer_revision must be 1", ErrInvalidSeed)
	}

	policies, policyRefs, err := preparePolicies(seed.Policies)
	if err != nil {
		return nil, err
	}
	agentRefs, workspaceRefs, profileRefs, err := prepareDefinitionRefs(
		seed.Definitions,
		policyRefs,
	)
	if err != nil {
		return nil, err
	}
	agentRef := agentRefs[0]
	workspaceRef := workspaceRefs[0]
	profileRef := profileRefs[0]
	if seed.DefaultAssembly.AgentID != agentRef.ID ||
		seed.DefaultAssembly.WorkspaceID != workspaceRef.ID ||
		seed.DefaultAssembly.ProfileID != profileRef.ID {
		return nil, fmt.Errorf("%w: default assembly must match the primary seeded definitions", ErrInvalidSeed)
	}
	if len(seed.MemoryContextProviders) != 0 &&
		(len(seed.Definitions.AdditionalAgents) != 0 ||
			len(seed.Definitions.AdditionalWorkspaces) != 0 ||
			len(seed.Definitions.AdditionalProfiles) != 0) {
		return nil, fmt.Errorf(
			"%w: Memory bootstrap supports only the primary Agent/Workspace/Profile definition",
			ErrInvalidSeed,
		)
	}
	compositeAgents, err := freezeSeedCompositeAgents(
		seed,
		agentRefs,
		workspaceRefs,
		profileRefs,
		policyRefs,
	)
	if err != nil {
		return nil, err
	}
	seed.CompositeAgents = compositeAgents

	if seed.ModelBinding.Port != (moduleapi.PortRef{
		Name:         moduleapi.PortNameModelGenerate,
		ExactVersion: moduleapi.PortVersionV1,
	}) {
		return nil, fmt.Errorf("%w: S1 bootstrap binds only model.generate/v1", ErrInvalidSeed)
	}
	if seed.ModelBinding.InstanceID != seed.Module.InstanceID {
		return nil, fmt.Errorf("%w: model Binding instance does not match module instance", ErrInvalidSeed)
	}
	if seed.ModelBinding.FailurePolicy != moduleapi.FailureRequired {
		return nil, fmt.Errorf("%w: model.generate/v1 bootstrap Binding must be REQUIRED", ErrInvalidSeed)
	}
	modelConfig, configCanonical, err := moduleapi.NewModelBindingConfigV1(
		seed.ModelBinding.Config,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: model Binding config: %v", ErrInvalidSeed, err)
	}
	configRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentConfig,
		jsonMediaType,
		configCanonical,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: model Binding config digest: %v", ErrInvalidSeed, err)
	}
	authorityRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentAuthorityCeiling,
		jsonMediaType,
		denyAllAuthorityCeiling,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: deny-all authority ceiling: %v", ErrInvalidSeed, err)
	}

	priceInput := corecontract.ModelPriceSnapshotV1{
		SchemaVersion:   seed.PriceSnapshot.SchemaVersion,
		PriceSnapshotID: seed.PriceSnapshot.PriceSnapshotID,
		Provider:        seed.PriceSnapshot.Provider,
		Model:           seed.PriceSnapshot.Model,
		BillingVersion:  seed.PriceSnapshot.BillingVersion,
		Currency:        seed.PriceSnapshot.Currency,
		PricingStatus:   seed.PriceSnapshot.PricingStatus,
		Pricing:         bytes.Clone(seed.PriceSnapshot.Pricing),
	}
	priceSnapshot, _, err := corecontract.NewModelPriceSnapshotV1(priceInput)
	if err != nil {
		return nil, fmt.Errorf("%w: model price snapshot: %v", ErrInvalidSeed, err)
	}
	if seed.ModelBinding.Config.Provider != priceSnapshot.Provider ||
		seed.ModelBinding.Config.Model != priceSnapshot.Model ||
		seed.ModelBinding.Config.BillingVersion != priceSnapshot.BillingVersion ||
		seed.ModelBinding.Config.PriceSnapshotID != priceSnapshot.PriceSnapshotID {
		return nil, fmt.Errorf("%w: model Binding config and price snapshot do not close", ErrInvalidSeed)
	}

	module, manifestCanonical, err := prepareModelArtifact(
		seed.Module,
		seed.ModelBinding.Port,
		artifactBase,
	)
	if err != nil {
		return nil, err
	}
	modelProfileRef, modelProfileCanonical, err := prepareModelProfile(
		seed.ModelProfile,
		modelConfig,
		configRef,
		module,
		authorityRef,
	)
	if err != nil {
		return nil, err
	}
	contexts, err := prepareDeclarativeContexts(
		seed.Module,
		seed.DeclarativeContextProviders,
		artifactBase,
	)
	if err != nil {
		return nil, err
	}
	knowledgeContexts, err := prepareKnowledgeContexts(
		seed,
		contexts,
		artifactBase,
	)
	if err != nil {
		return nil, err
	}
	memoryContexts, memoryGenesisCanonical, err := prepareMemoryContexts(
		seed,
		agentRef,
		workspaceRef,
		contexts,
		knowledgeContexts,
		artifactBase,
	)
	if err != nil {
		return nil, err
	}
	actions, err := prepareActionProviders(
		seed,
		contexts,
		knowledgeContexts,
		memoryContexts,
		artifactBase,
	)
	if err != nil {
		return nil, err
	}
	return &Prepared{
		seed:                   cloneSeed(seed),
		module:                 module,
		manifestCanonical:      bytes.Clone(manifestCanonical),
		contexts:               clonePreparedDeclarativeContexts(contexts),
		knowledgeContexts:      clonePreparedKnowledgeContexts(knowledgeContexts),
		memoryContexts:         clonePreparedMemoryContexts(memoryContexts),
		actions:                clonePreparedActionProviders(actions),
		memoryGenesisCanonical: bytes.Clone(memoryGenesisCanonical),
		policies:               clonePreparedPolicies(policies),
		agentRefs:              append([]corecontract.AgentRef(nil), agentRefs...),
		workspaceRefs:          append([]corecontract.WorkspaceRef(nil), workspaceRefs...),
		profileRefs:            append([]corecontract.ProfileRef(nil), profileRefs...),
		configCanonical:        bytes.Clone(configCanonical),
		configRef:              configRef,
		modelProfileRef:        cloneModelProfileRef(modelProfileRef),
		modelProfileCanonical:  bytes.Clone(modelProfileCanonical),
		authorityRef:           authorityRef,
		priceSnapshot:          priceSnapshot,
	}, nil
}

func prepareModelProfile(
	input *corecontract.ModelProfileV1,
	modelConfig moduleapi.ModelBindingConfigV1,
	configRef string,
	module ModuleAssertion,
	authorityRef string,
) (*corecontract.ModelProfileRef, []byte, error) {
	if input == nil {
		return nil, nil, nil
	}
	profile, ref, canonical, err := corecontract.NewModelProfileV1(*input)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"%w: ModelProfile: %v",
			ErrInvalidSeed,
			err,
		)
	}
	binding := moduleapi.PortBinding{
		Provider: moduleapi.ActivatedModuleRef{
			ModuleID:           module.ModuleID,
			Version:            module.ExactVersion,
			ArtifactDigest:     module.ArtifactDigest,
			InstanceID:         module.InstanceID,
			ExecutionClass:     module.ExpectedExecutionClass,
			AdapterIdentity:    module.ExpectedAdapterIdentity,
			ActivationRevision: module.ActivationRevision,
		},
		ConfigRef:           configRef,
		AuthorityCeilingRef: authorityRef,
		StaticContextRefs:   []string{},
		FailurePolicy:       moduleapi.FailureRequired,
	}
	if err := corecontract.ValidateModelProfileBindingV1(
		profile,
		binding,
		modelConfig,
	); err != nil {
		return nil, nil, fmt.Errorf(
			"%w: ModelProfile does not match the exact model Binding: %v",
			ErrInvalidSeed,
			err,
		)
	}
	return &ref, bytes.Clone(canonical), nil
}

type scannedArtifact struct {
	assertion         ModuleAssertion
	manifest          moduleapi.ModuleManifestV1
	manifestCanonical []byte
	files             []moduleapi.ArtifactFile
}

func prepareModelArtifact(
	seed moduleSeed,
	port moduleapi.PortRef,
	artifactBase string,
) (ModuleAssertion, []byte, error) {
	artifact, err := scanArtifact(seed, artifactBase)
	if err != nil {
		return ModuleAssertion{}, nil, err
	}
	if seed.ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess {
		return ModuleAssertion{}, nil, fmt.Errorf(
			"%w: the S1 executable model seed expects TRUSTED_IN_PROCESS",
			ErrInvalidSeed,
		)
	}
	if artifact.manifest.Runtime.Mode != moduleapi.RuntimeModeRequestTrustedInProcess ||
		artifact.manifest.Runtime.Protocol != moduleapi.RuntimeProtocolGoInProcessV1 {
		return ModuleAssertion{}, nil, fmt.Errorf(
			"%w: S1 Echo artifact must request TRUSTED_IN_PROCESS/go-in-process/v1",
			ErrArtifact,
		)
	}
	if len(artifact.manifest.Provides) != 1 || artifact.manifest.Provides[0] != port ||
		len(artifact.manifest.Requires) != 0 ||
		len(artifact.manifest.RequestedPermissions) != 0 {
		return ModuleAssertion{}, nil, fmt.Errorf(
			"%w: S1 Echo artifact must provide only model.generate/v1 and request no dependency or permission",
			ErrArtifact,
		)
	}
	return artifact.assertion, bytes.Clone(artifact.manifestCanonical), nil
}

func prepareDeclarativeContexts(
	model moduleSeed,
	input []declarativeContextProviderSeed,
	artifactBase string,
) ([]preparedDeclarativeContext, error) {
	if len(input) > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"%w: declarative_context_providers may contain at most %d entries",
			ErrInvalidSeed,
			moduleapi.MaxManifestEntries,
		)
	}
	prepared := make([]preparedDeclarativeContext, len(input))
	installationIDs := map[string]struct{}{model.InstallationID: {}}
	instanceIDs := map[string]struct{}{model.InstanceID: {}}
	moduleVersions := map[string]struct{}{model.ModuleID + "\x00" + model.ExactVersion: {}}
	contextPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	for index, provider := range input {
		if provider.Port != contextPort {
			return nil, fmt.Errorf(
				"%w: declarative context provider %d must bind only context.provide/v1",
				ErrInvalidSeed,
				index,
			)
		}
		if err := provider.FailurePolicy.Validate(); err != nil {
			return nil, fmt.Errorf(
				"%w: declarative context provider %d failure policy: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		configCanonical, err := canonicalContextBindingConfig(provider.Config)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: declarative context provider %d config: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		configRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentConfig,
			jsonMediaType,
			configCanonical,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: declarative context provider %d config digest: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		for _, coordinate := range []struct {
			name  string
			value string
			seen  map[string]struct{}
		}{
			{name: "installation ID", value: provider.Module.InstallationID, seen: installationIDs},
			{name: "instance ID", value: provider.Module.InstanceID, seen: instanceIDs},
			{
				name:  "module/version",
				value: provider.Module.ModuleID + "\x00" + provider.Module.ExactVersion,
				seen:  moduleVersions,
			},
		} {
			if _, duplicate := coordinate.seen[coordinate.value]; duplicate {
				return nil, fmt.Errorf(
					"%w: declarative context provider %d duplicates %s",
					ErrInvalidSeed,
					index,
					coordinate.name,
				)
			}
			coordinate.seen[coordinate.value] = struct{}{}
		}
		artifact, staticCanonical, err := prepareDeclarativeContextArtifact(
			provider.Module,
			contextPort,
			artifactBase,
		)
		if err != nil {
			return nil, fmt.Errorf("declarative context provider %d: %w", index, err)
		}
		staticRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentStaticContext,
			jsonMediaType,
			staticCanonical,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: declarative context provider %d content digest: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		provider.Config = bytes.Clone(configCanonical)
		prepared[index] = preparedDeclarativeContext{
			seed:                   provider,
			module:                 artifact.assertion,
			manifestCanonical:      bytes.Clone(artifact.manifestCanonical),
			configCanonical:        bytes.Clone(configCanonical),
			configRef:              configRef,
			staticContextCanonical: bytes.Clone(staticCanonical),
			staticContextRef:       staticRef,
		}
	}
	return prepared, nil
}

func prepareKnowledgeContexts(
	seed seedV1,
	declarative []preparedDeclarativeContext,
	artifactBase string,
) ([]preparedKnowledgeContext, error) {
	input := seed.KnowledgeContextProviders
	if len(input) == 0 {
		return []preparedKnowledgeContext{}, nil
	}
	if len(input) > moduleapi.MaxManifestEntries ||
		len(declarative)+len(input)+1 > moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"%w: all context providers plus the model may contain at most %d bindings",
			ErrInvalidSeed,
			moduleapi.MaxManifestEntries,
		)
	}
	installationIDs := map[string]struct{}{seed.Module.InstallationID: {}}
	instanceIDs := map[string]struct{}{seed.Module.InstanceID: {}}
	moduleVersions := map[string]struct{}{
		seed.Module.ModuleID + "\x00" + seed.Module.ExactVersion: {},
	}
	for _, provider := range declarative {
		installationIDs[provider.seed.Module.InstallationID] = struct{}{}
		instanceIDs[provider.seed.Module.InstanceID] = struct{}{}
		moduleVersions[provider.seed.Module.ModuleID+"\x00"+
			provider.seed.Module.ExactVersion] = struct{}{}
	}
	contextPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	seededAgentIDs, seededWorkspaceIDs := seededDefinitionIDSets(seed.Definitions)
	prepared := make([]preparedKnowledgeContext, len(input))
	for index, provider := range input {
		if provider.Port != contextPort {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d must bind only context.provide/v1",
				ErrInvalidSeed,
				index,
			)
		}
		if provider.FailurePolicy != moduleapi.FailureRequired {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d must be REQUIRED",
				ErrInvalidSeed,
				index,
			)
		}
		for _, coordinate := range []struct {
			name  string
			value string
			seen  map[string]struct{}
		}{
			{name: "installation ID", value: provider.Module.InstallationID, seen: installationIDs},
			{name: "instance ID", value: provider.Module.InstanceID, seen: instanceIDs},
			{
				name:  "module/version",
				value: provider.Module.ModuleID + "\x00" + provider.Module.ExactVersion,
				seen:  moduleVersions,
			},
		} {
			if _, duplicate := coordinate.seen[coordinate.value]; duplicate {
				return nil, fmt.Errorf(
					"%w: knowledge context provider %d duplicates %s",
					ErrInvalidSeed,
					index,
					coordinate.name,
				)
			}
			coordinate.seen[coordinate.value] = struct{}{}
		}

		config, configCanonical, err := moduleapi.NewContextBindingConfigV1(
			provider.Config,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d config: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		binding, err := moduleapi.RestoreKnowledgeContextBindingParametersV1(
			config,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d dynamic config: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		if err := rejectSecretValues(configCanonical); err != nil {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d config: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		configRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentConfig,
			jsonMediaType,
			configCanonical,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d config digest: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}

		if err := provider.AuthorityCeiling.Validate(); err != nil {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d authority ceiling: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		authority, authorityCanonical, err :=
			moduleapi.NewKnowledgeAuthorityCeilingV1(provider.AuthorityCeiling)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d authority ceiling: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		if binding.Source != authority.Source {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d config and authority source differ",
				ErrInvalidSeed,
				index,
			)
		}
		for ruleIndex, rule := range authority.AllowedScopes {
			_, workspacePresent := seededWorkspaceIDs[rule.WorkspaceID]
			_, agentPresent := seededAgentIDs[rule.AgentID]
			if rule.TenantID != seed.TenantID ||
				(rule.WorkspaceID != "*" && !workspacePresent) ||
				(rule.AgentID != "*" && !agentPresent) {
				return nil, fmt.Errorf(
					"%w: knowledge context provider %d authority scope %d escapes the exact seeded Tenant/Workspace/Agent ID sets",
					ErrInvalidSeed,
					index,
					ruleIndex,
				)
			}
		}
		if err := rejectSecretValues(authorityCanonical); err != nil {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d authority ceiling: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		authorityRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentAuthorityCeiling,
			jsonMediaType,
			authorityCanonical,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d authority digest: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}

		artifact, sourceRef, err :=
			prepareKnowledgeContextArtifact(
				provider.Module,
				contextPort,
				artifactBase,
			)
		if err != nil {
			return nil, fmt.Errorf(
				"knowledge context provider %d: %w",
				index,
				err,
			)
		}
		if sourceRef != binding.Source || sourceRef != authority.Source {
			return nil, fmt.Errorf(
				"%w: knowledge context provider %d artifact source does not match config and authority",
				ErrArtifact,
				index,
			)
		}
		provider.Config = config
		provider.AuthorityCeiling = authority
		prepared[index] = preparedKnowledgeContext{
			seed:               provider,
			module:             artifact.assertion,
			manifestCanonical:  bytes.Clone(artifact.manifestCanonical),
			configCanonical:    bytes.Clone(configCanonical),
			configRef:          configRef,
			authorityCanonical: bytes.Clone(authorityCanonical),
			authorityRef:       authorityRef,
		}
	}
	return prepared, nil
}

func prepareKnowledgeContextArtifact(
	seed moduleSeed,
	port moduleapi.PortRef,
	artifactBase string,
) (
	scannedArtifact,
	moduleapi.KnowledgeSourceRefV1,
	error,
) {
	artifact, err := scanArtifact(seed, artifactBase)
	if err != nil {
		return scannedArtifact{}, moduleapi.KnowledgeSourceRefV1{}, err
	}
	if seed.ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess {
		return scannedArtifact{}, moduleapi.KnowledgeSourceRefV1{}, fmt.Errorf(
			"%w: knowledge context seed expects TRUSTED_IN_PROCESS",
			ErrInvalidSeed,
		)
	}
	manifest := artifact.manifest
	if manifest.Runtime.Mode != moduleapi.RuntimeModeRequestTrustedInProcess ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolGoInProcessV1 {
		return scannedArtifact{}, moduleapi.KnowledgeSourceRefV1{}, fmt.Errorf(
			"%w: knowledge context artifact must request TRUSTED_IN_PROCESS/go-in-process/v1",
			ErrArtifact,
		)
	}
	if len(manifest.Provides) != 1 || manifest.Provides[0] != port ||
		len(manifest.Requires) != 0 || len(manifest.RequestedPermissions) != 0 {
		return scannedArtifact{}, moduleapi.KnowledgeSourceRefV1{}, fmt.Errorf(
			"%w: knowledge context artifact must provide only context.provide/v1 and request no dependency or permission",
			ErrArtifact,
		)
	}
	entrypointPath, err := moduleapi.NormalizeArtifactPath(
		manifest.Runtime.Entrypoint,
	)
	if err != nil || entrypointPath != manifest.Runtime.Entrypoint ||
		!strings.HasPrefix(entrypointPath, "content/") {
		return scannedArtifact{}, moduleapi.KnowledgeSourceRefV1{}, fmt.Errorf(
			"%w: knowledge context entrypoint must be a canonical content/ artifact path",
			ErrArtifact,
		)
	}
	var entrypoint []byte
	for _, file := range artifact.files {
		if file.Path == entrypointPath {
			entrypoint = bytes.Clone(file.Content)
			break
		}
	}
	if entrypoint == nil {
		return scannedArtifact{}, moduleapi.KnowledgeSourceRefV1{}, fmt.Errorf(
			"%w: knowledge context entrypoint %q is absent from the scanned artifact",
			ErrArtifact,
			entrypointPath,
		)
	}
	_, sourceRef, err := moduleapi.RestoreKnowledgeSourceV1(entrypoint)
	if err != nil {
		return scannedArtifact{}, moduleapi.KnowledgeSourceRefV1{}, fmt.Errorf(
			"%w: knowledge context entrypoint: %v",
			ErrArtifact,
			err,
		)
	}
	return artifact, sourceRef, nil
}

func prepareMemoryContexts(
	seed seedV1,
	agentRef corecontract.AgentRef,
	workspaceRef corecontract.WorkspaceRef,
	declarative []preparedDeclarativeContext,
	knowledge []preparedKnowledgeContext,
	artifactBase string,
) ([]preparedMemoryContext, []byte, error) {
	input := seed.MemoryContextProviders
	if len(input) == 0 {
		return []preparedMemoryContext{}, nil, nil
	}
	// Context Compiler v1 admits one Memory evidence chain. Freeze that limit
	// at seed preparation rather than publishing an unusable assembly.
	if len(input) != 1 {
		return nil, nil, fmt.Errorf(
			"%w: the first Memory slice requires exactly one memory_context_provider when enabled",
			ErrInvalidSeed,
		)
	}
	if len(declarative)+len(knowledge)+len(input)+1 > moduleapi.MaxManifestEntries {
		return nil, nil, fmt.Errorf(
			"%w: all context providers plus the model may contain at most %d bindings",
			ErrInvalidSeed,
			moduleapi.MaxManifestEntries,
		)
	}
	installationIDs := map[string]struct{}{seed.Module.InstallationID: {}}
	instanceIDs := map[string]struct{}{seed.Module.InstanceID: {}}
	moduleVersions := map[string]struct{}{
		seed.Module.ModuleID + "\x00" + seed.Module.ExactVersion: {},
	}
	for _, provider := range declarative {
		installationIDs[provider.seed.Module.InstallationID] = struct{}{}
		instanceIDs[provider.seed.Module.InstanceID] = struct{}{}
		moduleVersions[provider.seed.Module.ModuleID+"\x00"+
			provider.seed.Module.ExactVersion] = struct{}{}
	}
	for _, provider := range knowledge {
		installationIDs[provider.seed.Module.InstallationID] = struct{}{}
		instanceIDs[provider.seed.Module.InstanceID] = struct{}{}
		moduleVersions[provider.seed.Module.ModuleID+"\x00"+
			provider.seed.Module.ExactVersion] = struct{}{}
	}
	contextPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	prepared := make([]preparedMemoryContext, len(input))
	for index, provider := range input {
		if provider.Port != contextPort {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d must bind only context.provide/v1",
				ErrInvalidSeed,
				index,
			)
		}
		if provider.FailurePolicy != moduleapi.FailureRequired {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d must be REQUIRED",
				ErrInvalidSeed,
				index,
			)
		}
		for _, coordinate := range []struct {
			name  string
			value string
			seen  map[string]struct{}
		}{
			{name: "installation ID", value: provider.Module.InstallationID, seen: installationIDs},
			{name: "instance ID", value: provider.Module.InstanceID, seen: instanceIDs},
			{
				name:  "module/version",
				value: provider.Module.ModuleID + "\x00" + provider.Module.ExactVersion,
				seen:  moduleVersions,
			},
		} {
			if _, duplicate := coordinate.seen[coordinate.value]; duplicate {
				return nil, nil, fmt.Errorf(
					"%w: Memory context provider %d duplicates %s",
					ErrInvalidSeed,
					index,
					coordinate.name,
				)
			}
			coordinate.seen[coordinate.value] = struct{}{}
		}

		config, configCanonical, err := moduleapi.NewContextBindingConfigV1(
			provider.Config,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d config: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		binding, _, err := moduleapi.RestoreMemoryContextBindingParametersV1(
			config,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d dynamic config: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		if err := rejectSecretValues(configCanonical); err != nil {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d config: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		configRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentConfig,
			jsonMediaType,
			configCanonical,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d config digest: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}

		authority, authorityCanonical, err :=
			moduleapi.NewMemoryAuthorityCeilingV1(provider.AuthorityCeiling)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d authority ceiling: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		if authority.TenantID != seed.TenantID ||
			authority.AgentID != seed.Definitions.Agent.ID ||
			len(authority.AllowedWorkspaceIDs) != 1 ||
			authority.AllowedWorkspaceIDs[0] != seed.Definitions.Workspace.ID {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d authority must grant only the exact seeded Tenant, Agent and Workspace",
				ErrInvalidSeed,
				index,
			)
		}
		if _, err := moduleapi.ResolveMemoryAuthorityV1(
			binding,
			authority,
			moduleapi.MemorySnapshotRefV1{
				TenantID: seed.TenantID,
				AgentID:  agentRef.ID,
				Revision: 1,
				Digest:   strings.Repeat("0", moduleapi.SHA256HexLength),
			},
			moduleapi.MemoryQueryScopeV1{
				TenantID: seed.TenantID,
				Workspace: moduleapi.MemoryObjectRefV1{
					ID: workspaceRef.ID, Version: workspaceRef.Version,
					Digest: workspaceRef.Digest,
				},
				Agent: moduleapi.MemoryObjectRefV1{
					ID: agentRef.ID, Version: agentRef.Version,
					Digest: agentRef.Digest,
				},
				TaskInputRef: strings.Repeat("0", moduleapi.SHA256HexLength),
			},
		); err != nil {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d exact authority closure: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		if err := rejectSecretValues(authorityCanonical); err != nil {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d authority ceiling: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		authorityRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentAuthorityCeiling,
			jsonMediaType,
			authorityCanonical,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"%w: Memory context provider %d authority digest: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}

		artifact, err := prepareMemoryContextArtifact(
			provider.Module,
			artifactBase,
		)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"Memory context provider %d: %w",
				index,
				err,
			)
		}
		provider.Config = config
		provider.AuthorityCeiling = authority
		prepared[index] = preparedMemoryContext{
			seed:               provider,
			module:             artifact.assertion,
			manifestCanonical:  bytes.Clone(artifact.manifestCanonical),
			configCanonical:    bytes.Clone(configCanonical),
			configRef:          configRef,
			authorityCanonical: bytes.Clone(authorityCanonical),
			authorityRef:       authorityRef,
		}
	}
	_, genesisCanonical, err := moduleapi.NewAgentMemorySnapshotV1(
		moduleapi.AgentMemorySnapshotV1{
			SchemaVersion: moduleapi.MemorySnapshotSchemaV1,
			TenantID:      seed.TenantID,
			AgentID:       agentRef.ID,
			Revision:      1,
			Entries:       []moduleapi.MemoryEntryV1{},
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"%w: build Agent Memory genesis: %v",
			ErrInvalidSeed,
			err,
		)
	}
	return prepared, genesisCanonical, nil
}

func prepareMemoryContextArtifact(
	seed moduleSeed,
	artifactBase string,
) (scannedArtifact, error) {
	artifact, err := scanArtifact(seed, artifactBase)
	if err != nil {
		return scannedArtifact{}, err
	}
	if seed.ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess {
		return scannedArtifact{}, fmt.Errorf(
			"%w: Memory context seed expects TRUSTED_IN_PROCESS",
			ErrInvalidSeed,
		)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           artifact.assertion.ModuleID,
		Version:            artifact.assertion.ExactVersion,
		ArtifactDigest:     artifact.assertion.ArtifactDigest,
		InstanceID:         artifact.assertion.InstanceID,
		ExecutionClass:     artifact.assertion.ExpectedExecutionClass,
		AdapterIdentity:    artifact.assertion.ExpectedAdapterIdentity,
		ActivationRevision: artifact.assertion.ActivationRevision,
	}
	if _, err := exactadapter.NewDeterministicMemoryFromArtifact(
		provider,
		artifact.manifestCanonical,
		artifact.files,
	); err != nil {
		return scannedArtifact{}, fmt.Errorf(
			"%w: Memory context artifact: %v",
			ErrArtifact,
			err,
		)
	}
	return artifact, nil
}

func prepareActionProviders(
	seed seedV1,
	declarative []preparedDeclarativeContext,
	knowledge []preparedKnowledgeContext,
	memory []preparedMemoryContext,
	artifactBase string,
) ([]preparedActionProvider, error) {
	input := seed.ActionProviders
	if len(input) == 0 {
		return []preparedActionProvider{}, nil
	}
	if len(input) > moduleapi.MaxManifestEntries ||
		len(declarative)+len(knowledge)+len(memory)+len(input)+1 >
			moduleapi.MaxManifestEntries {
		return nil, fmt.Errorf(
			"%w: all providers plus the model may contain at most %d bindings",
			ErrInvalidSeed,
			moduleapi.MaxManifestEntries,
		)
	}

	installationIDs := map[string]struct{}{seed.Module.InstallationID: {}}
	instanceIDs := map[string]struct{}{seed.Module.InstanceID: {}}
	moduleVersions := map[string]struct{}{
		seed.Module.ModuleID + "\x00" + seed.Module.ExactVersion: {},
	}
	addCoordinates := func(module moduleSeed) {
		installationIDs[module.InstallationID] = struct{}{}
		instanceIDs[module.InstanceID] = struct{}{}
		moduleVersions[module.ModuleID+"\x00"+module.ExactVersion] = struct{}{}
	}
	for _, provider := range declarative {
		addCoordinates(provider.seed.Module)
	}
	for _, provider := range knowledge {
		addCoordinates(provider.seed.Module)
	}
	for _, provider := range memory {
		addCoordinates(provider.seed.Module)
	}

	actionPort := moduleapi.PortRef{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}
	_, seededWorkspaceIDs := seededDefinitionIDSets(seed.Definitions)
	prepared := make([]preparedActionProvider, len(input))
	seenPublicActionIDs := make(map[string]struct{})
	totalActions := 0
	for index, provider := range input {
		if provider.Port != actionPort {
			return nil, fmt.Errorf(
				"%w: Action provider %d must bind only action.provider/v1",
				ErrInvalidSeed,
				index,
			)
		}
		if provider.FailurePolicy != moduleapi.FailureRequired {
			return nil, fmt.Errorf(
				"%w: Action provider %d must be REQUIRED",
				ErrInvalidSeed,
				index,
			)
		}
		for _, coordinate := range []struct {
			name  string
			value string
			seen  map[string]struct{}
		}{
			{name: "installation ID", value: provider.Module.InstallationID, seen: installationIDs},
			{name: "instance ID", value: provider.Module.InstanceID, seen: instanceIDs},
			{
				name:  "module/version",
				value: provider.Module.ModuleID + "\x00" + provider.Module.ExactVersion,
				seen:  moduleVersions,
			},
		} {
			if _, duplicate := coordinate.seen[coordinate.value]; duplicate {
				return nil, fmt.Errorf(
					"%w: Action provider %d duplicates %s",
					ErrInvalidSeed,
					index,
					coordinate.name,
				)
			}
			coordinate.seen[coordinate.value] = struct{}{}
		}

		config, configCanonical, err := moduleapi.NewActionBindingConfigV1(
			provider.Config,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: Action provider %d config: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		if err := rejectSecretValues(configCanonical); err != nil {
			return nil, fmt.Errorf(
				"%w: Action provider %d config: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		configRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentConfig,
			jsonMediaType,
			configCanonical,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: Action provider %d config digest: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}

		authority, authorityCanonical, err :=
			moduleapi.NewActionAuthorityCeilingV1(provider.AuthorityCeiling)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: Action provider %d authority ceiling: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		if authority.TenantID != seed.TenantID {
			return nil, fmt.Errorf(
				"%w: Action provider %d authority tenant differs from the seeded Tenant",
				ErrInvalidSeed,
				index,
			)
		}
		for _, workspaceID := range authority.AllowedWorkspaceIDs {
			if workspaceID == "*" {
				continue
			}
			if _, present := seededWorkspaceIDs[workspaceID]; !present {
				return nil, fmt.Errorf(
					"%w: Action provider %d authority Workspace %q escapes the exact seeded Workspace ID set",
					ErrInvalidSeed,
					index,
					workspaceID,
				)
			}
		}
		allowedProviderIDs := make(
			map[string]struct{},
			len(authority.AllowedProviderActionIDs),
		)
		for _, providerActionID := range authority.AllowedProviderActionIDs {
			allowedProviderIDs[providerActionID] = struct{}{}
		}
		for mappingIndex, mapping := range config.Actions {
			if _, allowed := allowedProviderIDs[mapping.ProviderActionID]; !allowed {
				return nil, fmt.Errorf(
					"%w: Action provider %d mapping %d is outside its authority action IDs",
					ErrInvalidSeed,
					index,
					mappingIndex,
				)
			}
			allowed, effectErr := moduleapi.ActionEffectAtMostV1(
				mapping.LocalEffectClass,
				authority.MaxEffectClass,
			)
			if effectErr != nil || !allowed {
				return nil, fmt.Errorf(
					"%w: Action provider %d mapping %d effect exceeds its authority ceiling",
					ErrInvalidSeed,
					index,
					mappingIndex,
				)
			}
			if mapping.MaxResultBytes > authority.MaxResultBytes {
				return nil, fmt.Errorf(
					"%w: Action provider %d mapping %d result ceiling exceeds its authority ceiling",
					ErrInvalidSeed,
					index,
					mappingIndex,
				)
			}
			if _, duplicate := seenPublicActionIDs[mapping.PublicActionID]; duplicate {
				return nil, fmt.Errorf(
					"%w: Action provider %d duplicates public action ID %q across Bindings",
					ErrInvalidSeed,
					index,
					mapping.PublicActionID,
				)
			}
			seenPublicActionIDs[mapping.PublicActionID] = struct{}{}
			totalActions++
			if totalActions > moduleapi.MaxActionsPerMemberV1 {
				return nil, fmt.Errorf(
					"%w: Action providers expose more than %d public actions",
					ErrInvalidSeed,
					moduleapi.MaxActionsPerMemberV1,
				)
			}
		}
		if err := rejectSecretValues(authorityCanonical); err != nil {
			return nil, fmt.Errorf(
				"%w: Action provider %d authority ceiling: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}
		authorityRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentAuthorityCeiling,
			jsonMediaType,
			authorityCanonical,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: Action provider %d authority digest: %v",
				ErrInvalidSeed,
				index,
				err,
			)
		}

		artifact, err := prepareActionArtifact(
			provider.Module,
			actionPort,
			artifactBase,
		)
		if err != nil {
			return nil, fmt.Errorf("Action provider %d: %w", index, err)
		}
		provider.Config = config
		provider.AuthorityCeiling = authority
		prepared[index] = preparedActionProvider{
			seed:               provider,
			module:             artifact.assertion,
			manifestCanonical:  bytes.Clone(artifact.manifestCanonical),
			configCanonical:    bytes.Clone(configCanonical),
			configRef:          configRef,
			authorityCanonical: bytes.Clone(authorityCanonical),
			authorityRef:       authorityRef,
		}
	}
	return prepared, nil
}

func prepareActionArtifact(
	seed moduleSeed,
	port moduleapi.PortRef,
	artifactBase string,
) (scannedArtifact, error) {
	artifact, err := scanArtifact(seed, artifactBase)
	if err != nil {
		return scannedArtifact{}, err
	}
	manifest := artifact.manifest
	switch seed.ExpectedExecutionClass {
	case moduleapi.ExecutionTrustedInProcess:
		if manifest.Runtime.Mode != moduleapi.RuntimeModeRequestTrustedInProcess ||
			manifest.Runtime.Protocol != moduleapi.RuntimeProtocolGoInProcessV1 {
			return scannedArtifact{}, fmt.Errorf(
				"%w: trusted Action artifact must request TRUSTED_IN_PROCESS/go-in-process/v1",
				ErrArtifact,
			)
		}
	case moduleapi.ExecutionLocalProcess:
		if manifest.Runtime.Mode != moduleapi.RuntimeModeRequestLocalProcess ||
			manifest.Runtime.Protocol != moduleapi.RuntimeProtocolMCPStdio20251125 {
			return scannedArtifact{}, fmt.Errorf(
				"%w: local Action artifact must request LOCAL_PROCESS/mcp-stdio/2025-11-25",
				ErrArtifact,
			)
		}
		entrypointFound := false
		for _, file := range artifact.files {
			if file.Path == manifest.Runtime.Entrypoint {
				entrypointFound = true
				break
			}
		}
		if !entrypointFound {
			return scannedArtifact{}, fmt.Errorf(
				"%w: local Action descriptor %q is absent",
				ErrArtifact,
				manifest.Runtime.Entrypoint,
			)
		}
	default:
		return scannedArtifact{}, fmt.Errorf(
			"%w: Action provider seed execution class %q is unavailable",
			ErrInvalidSeed,
			seed.ExpectedExecutionClass,
		)
	}
	if len(manifest.Provides) != 1 || manifest.Provides[0] != port ||
		len(manifest.Requires) != 0 || len(manifest.RequestedPermissions) != 0 {
		return scannedArtifact{}, fmt.Errorf(
			"%w: Action artifact must provide only action.provider/v1 and request no dependency or permission",
			ErrArtifact,
		)
	}
	return artifact, nil
}

func canonicalContextBindingConfig(input json.RawMessage) ([]byte, error) {
	config, err := moduleapi.RestoreContextBindingConfigV1(input)
	if err != nil {
		return nil, err
	}
	// The public v1 schema reserves these retention grants for a later runtime
	// revision. Reject them at the bootstrap publication boundary so an
	// operator cannot successfully initialize a Store that fails on first use.
	if config.AllowSummary || config.AllowDrop {
		return nil, fmt.Errorf(
			"bootstrap context Binding allow_summary and allow_drop must both be false",
		)
	}
	if err := rejectSecretValues(input); err != nil {
		return nil, err
	}
	return bytes.Clone(input), nil
}

func prepareDeclarativeContextArtifact(
	seed moduleSeed,
	port moduleapi.PortRef,
	artifactBase string,
) (scannedArtifact, []byte, error) {
	artifact, err := scanArtifact(seed, artifactBase)
	if err != nil {
		return scannedArtifact{}, nil, err
	}
	if seed.ExpectedExecutionClass != moduleapi.ExecutionDeclarative {
		return scannedArtifact{}, nil, fmt.Errorf(
			"%w: static context seed expects DECLARATIVE",
			ErrInvalidSeed,
		)
	}
	manifest := artifact.manifest
	if manifest.Runtime.Mode != moduleapi.RuntimeModeRequestDeclarative ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolStaticV1 {
		return scannedArtifact{}, nil, fmt.Errorf(
			"%w: static context artifact must request DECLARATIVE/static/v1",
			ErrArtifact,
		)
	}
	if len(manifest.Provides) != 1 || manifest.Provides[0] != port ||
		len(manifest.Requires) != 0 || len(manifest.RequestedPermissions) != 0 {
		return scannedArtifact{}, nil, fmt.Errorf(
			"%w: static context artifact must provide only context.provide/v1 and request no dependency or permission",
			ErrArtifact,
		)
	}
	var entrypoint []byte
	for _, file := range artifact.files {
		if file.Path == manifest.Runtime.Entrypoint {
			entrypoint = bytes.Clone(file.Content)
			break
		}
	}
	if entrypoint == nil {
		return scannedArtifact{}, nil, fmt.Errorf(
			"%w: static context entrypoint %q is absent from the scanned artifact",
			ErrArtifact,
			manifest.Runtime.Entrypoint,
		)
	}
	if _, err := corecontract.RestoreStaticContextV1(entrypoint); err != nil {
		return scannedArtifact{}, nil, fmt.Errorf(
			"%w: static context entrypoint: %v",
			ErrArtifact,
			err,
		)
	}
	return artifact, entrypoint, nil
}

func scanArtifact(seed moduleSeed, artifactBase string) (scannedArtifact, error) {
	if err := (moduleapi.Ref{ID: seed.ModuleID, Version: seed.ExactVersion}).Validate(); err != nil {
		return scannedArtifact{}, fmt.Errorf("%w: module identity: %v", ErrInvalidSeed, err)
	}
	for name, value := range map[string]string{
		"installation ID":           seed.InstallationID,
		"instance ID":               seed.InstanceID,
		"expected adapter identity": seed.ExpectedAdapterIdentity,
	} {
		if err := validateOpaque(name, value); err != nil {
			return scannedArtifact{}, err
		}
	}
	if seed.ActivationRevision == 0 || seed.ActivationRevision > math.MaxInt64 {
		return scannedArtifact{}, fmt.Errorf("%w: activation revision is outside the Current Store range", ErrInvalidSeed)
	}
	if err := seed.ExpectedExecutionClass.Validate(); err != nil {
		return scannedArtifact{}, fmt.Errorf("%w: expected execution class: %v", ErrInvalidSeed, err)
	}
	if !moduleapi.ValidSHA256(seed.ArtifactDigest) || seed.ArtifactSizeBytes == 0 {
		return scannedArtifact{}, fmt.Errorf("%w: artifact digest and positive size assertions are required", ErrInvalidSeed)
	}
	normalized, err := moduleapi.NormalizeArtifactPath(seed.ArtifactRelativePath)
	if err != nil || normalized != seed.ArtifactRelativePath {
		return scannedArtifact{}, fmt.Errorf("%w: artifact_relative_path must be canonical: %v", ErrInvalidSeed, err)
	}
	if strings.TrimSpace(artifactBase) == "" {
		return scannedArtifact{}, fmt.Errorf("%w: artifact base is required", ErrInvalidSeed)
	}
	base, err := filepath.Abs(artifactBase)
	if err != nil {
		return scannedArtifact{}, fmt.Errorf("%w: resolve artifact base: %v", ErrArtifact, err)
	}
	artifactDirectory := filepath.Join(base, filepath.FromSlash(normalized))
	manifestPath := filepath.Join(artifactDirectory, moduleapi.ArtifactManifestPath)
	manifestBefore, err := os.ReadFile(manifestPath)
	if err != nil {
		return scannedArtifact{}, fmt.Errorf("%w: read module.yaml: %v", ErrArtifact, err)
	}
	manifest, manifestCanonical, err := moduleapi.ParseModuleManifestV1(manifestBefore)
	if err != nil {
		return scannedArtifact{}, fmt.Errorf("%w: parse module.yaml: %v", ErrArtifact, err)
	}
	if manifest.ID != seed.ModuleID || manifest.Version != seed.ExactVersion {
		return scannedArtifact{}, fmt.Errorf("%w: manifest identity does not match seed lock", ErrArtifact)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		return scannedArtifact{}, fmt.Errorf("%w: %v", ErrArtifact, err)
	}
	manifestAfter, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(manifestBefore, manifestAfter) {
		return scannedArtifact{}, fmt.Errorf("%w: module.yaml changed during artifact scan", ErrArtifact)
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		return scannedArtifact{}, fmt.Errorf("%w: compute digest: %v", ErrArtifact, err)
	}
	var size uint64 = uint64(len(manifestCanonical))
	for _, file := range files {
		if uint64(len(file.Content)) > math.MaxUint64-size {
			return scannedArtifact{}, fmt.Errorf("%w: artifact size overflow", ErrArtifact)
		}
		size += uint64(len(file.Content))
	}
	if digest != seed.ArtifactDigest || size != seed.ArtifactSizeBytes {
		return scannedArtifact{}, fmt.Errorf(
			"%w: computed digest/size %s/%d, seed asserts %s/%d",
			ErrArtifact,
			digest,
			size,
			seed.ArtifactDigest,
			seed.ArtifactSizeBytes,
		)
	}
	return scannedArtifact{
		assertion: ModuleAssertion{
			ModuleID:                seed.ModuleID,
			ExactVersion:            seed.ExactVersion,
			ArtifactDigest:          digest,
			ArtifactSizeBytes:       size,
			ArtifactDirectory:       artifactDirectory,
			InstanceID:              seed.InstanceID,
			ActivationRevision:      seed.ActivationRevision,
			ExpectedExecutionClass:  seed.ExpectedExecutionClass,
			ExpectedAdapterIdentity: seed.ExpectedAdapterIdentity,
		},
		manifest:          manifest,
		manifestCanonical: bytes.Clone(manifestCanonical),
		files:             files,
	}, nil
}

func preparePolicies(input []policySeed) ([]preparedPolicy, map[string]corecontract.PolicyRef, error) {
	if len(input) == 0 || len(input) > moduleapi.MaxManifestEntries {
		return nil, nil, fmt.Errorf("%w: policies must contain between 1 and %d entries", ErrInvalidSeed, moduleapi.MaxManifestEntries)
	}
	prepared := make([]preparedPolicy, len(input))
	refs := make(map[string]corecontract.PolicyRef, len(input))
	for index, policy := range input {
		if err := validatePolicyAlias(policy.Alias); err != nil {
			return nil, nil, fmt.Errorf("policy %d: %w", index, err)
		}
		if _, duplicate := refs[policy.Alias]; duplicate {
			return nil, nil, fmt.Errorf("%w: duplicate policy alias %q", ErrInvalidSeed, policy.Alias)
		}
		if err := rejectSecretValues(policy.Body); err != nil {
			return nil, nil, fmt.Errorf("%w: policy %q: %v", ErrInvalidSeed, policy.Alias, err)
		}
		if policy.PolicyType == corecontract.PolicyContext {
			if _, err := corecontract.RestoreContextPolicyV1(policy.Body); err != nil {
				return nil, nil, fmt.Errorf(
					"%w: policy %q context body: %v",
					ErrInvalidSeed,
					policy.Alias,
					err,
				)
			}
		}
		_, ref, canonical, err := corecontract.NewPolicyDocument(
			policyIDPrefix+policy.Alias,
			policyVersion,
			policy.PolicyType,
			policy.Body,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: policy %q: %v", ErrInvalidSeed, policy.Alias, err)
		}
		prepared[index] = preparedPolicy{alias: policy.Alias, ref: ref, canonical: canonical}
		refs[policy.Alias] = ref
	}
	return prepared, refs, nil
}

func prepareDefinitionRefs(
	definitions definitionsSeed,
	policyRefs map[string]corecontract.PolicyRef,
) (
	[]corecontract.AgentRef,
	[]corecontract.WorkspaceRef,
	[]corecontract.ProfileRef,
	error,
) {
	agents := append(
		[]definitionSeed{definitions.Agent},
		definitions.AdditionalAgents...,
	)
	workspaces := append(
		[]workspaceSeed{definitions.Workspace},
		definitions.AdditionalWorkspaces...,
	)
	profiles := append(
		[]profileSeed{definitions.Profile},
		definitions.AdditionalProfiles...,
	)
	for _, list := range []struct {
		name  string
		count int
	}{
		{name: "Agent definitions", count: len(agents)},
		{name: "Workspace definitions", count: len(workspaces)},
		{name: "Profile definitions", count: len(profiles)},
	} {
		if list.count > moduleapi.MaxManifestEntries {
			return nil, nil, nil, fmt.Errorf(
				"%w: %s may contain at most %d entries",
				ErrInvalidSeed,
				list.name,
				moduleapi.MaxManifestEntries,
			)
		}
	}

	agentRefs := make([]corecontract.AgentRef, len(agents))
	seenAgentIDs := make(map[string]struct{}, len(agents))
	for index, definition := range agents {
		ref, err := prepareAgentRef(definition)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("Agent definition %d: %w", index, err)
		}
		if _, duplicate := seenAgentIDs[ref.ID]; duplicate {
			return nil, nil, nil, fmt.Errorf(
				"%w: duplicate Agent definition ID %q",
				ErrInvalidSeed,
				ref.ID,
			)
		}
		seenAgentIDs[ref.ID] = struct{}{}
		agentRefs[index] = ref
	}

	workspaceRefs := make([]corecontract.WorkspaceRef, len(workspaces))
	seenWorkspaceIDs := make(map[string]struct{}, len(workspaces))
	for index, definition := range workspaces {
		ref, err := prepareWorkspaceRef(definition)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("Workspace definition %d: %w", index, err)
		}
		if _, duplicate := seenWorkspaceIDs[ref.ID]; duplicate {
			return nil, nil, nil, fmt.Errorf(
				"%w: duplicate Workspace definition ID %q",
				ErrInvalidSeed,
				ref.ID,
			)
		}
		seenWorkspaceIDs[ref.ID] = struct{}{}
		if err := requirePolicyAlias(
			policyRefs,
			definition.BudgetPolicyAlias,
			fmt.Sprintf("Workspace definition %q budget", ref.ID),
		); err != nil {
			return nil, nil, nil, err
		}
		workspaceRefs[index] = ref
	}

	profileRefs := make([]corecontract.ProfileRef, len(profiles))
	seenProfileIDs := make(map[string]struct{}, len(profiles))
	for index, definition := range profiles {
		ref, err := prepareProfileRef(definition)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("Profile definition %d: %w", index, err)
		}
		if _, duplicate := seenProfileIDs[ref.ID]; duplicate {
			return nil, nil, nil, fmt.Errorf(
				"%w: duplicate Profile definition ID %q",
				ErrInvalidSeed,
				ref.ID,
			)
		}
		seenProfileIDs[ref.ID] = struct{}{}
		for _, policy := range []struct {
			name  string
			alias string
		}{
			{name: "context", alias: definition.ContextPolicyAlias},
			{name: "cost", alias: definition.CostPolicyAlias},
			{name: "scheduling", alias: definition.SchedulingPolicyAlias},
		} {
			if err := requirePolicyAlias(
				policyRefs,
				policy.alias,
				fmt.Sprintf("Profile definition %q %s", ref.ID, policy.name),
			); err != nil {
				return nil, nil, nil, err
			}
		}
		profileRefs[index] = ref
	}
	return agentRefs, workspaceRefs, profileRefs, nil
}

func requirePolicyAlias(
	refs map[string]corecontract.PolicyRef,
	alias string,
	owner string,
) error {
	_, present := refs[alias]
	if !present {
		return fmt.Errorf(
			"%w: %s policy alias %q is absent",
			ErrInvalidSeed,
			owner,
			alias,
		)
	}
	return nil
}

func seededDefinitionIDSets(
	definitions definitionsSeed,
) (map[string]struct{}, map[string]struct{}) {
	agentIDs := make(
		map[string]struct{},
		1+len(definitions.AdditionalAgents),
	)
	agentIDs[definitions.Agent.ID] = struct{}{}
	for _, definition := range definitions.AdditionalAgents {
		agentIDs[definition.ID] = struct{}{}
	}
	workspaceIDs := make(
		map[string]struct{},
		1+len(definitions.AdditionalWorkspaces),
	)
	workspaceIDs[definitions.Workspace.ID] = struct{}{}
	for _, definition := range definitions.AdditionalWorkspaces {
		workspaceIDs[definition.ID] = struct{}{}
	}
	return agentIDs, workspaceIDs
}

func freezeSeedCompositeAgents(
	seed seedV1,
	agentRefs []corecontract.AgentRef,
	workspaceRefs []corecontract.WorkspaceRef,
	profileRefs []corecontract.ProfileRef,
	policyRefs map[string]corecontract.PolicyRef,
) ([]controlcontract.CompositeAgentDefinitionV1, error) {
	if len(seed.CompositeAgents) == 0 {
		return nil, nil
	}
	workspaceSeeds := append(
		[]workspaceSeed{seed.Definitions.Workspace},
		seed.Definitions.AdditionalWorkspaces...,
	)
	profileSeeds := append(
		[]profileSeed{seed.Definitions.Profile},
		seed.Definitions.AdditionalProfiles...,
	)
	workspaces := make([]controlcontract.WorkspaceDefinition, len(workspaceRefs))
	for index, ref := range workspaceRefs {
		workspaces[index] = controlcontract.WorkspaceDefinition{
			Workspace:    ref,
			BudgetPolicy: policyRefs[workspaceSeeds[index].BudgetPolicyAlias],
		}
	}
	profiles := make([]controlcontract.ProfileDefinition, len(profileRefs))
	for index, ref := range profileRefs {
		definition := profileSeeds[index]
		profiles[index] = controlcontract.ProfileDefinition{
			Profile:          ref,
			ContextPolicy:    policyRefs[definition.ContextPolicyAlias],
			CostPolicy:       policyRefs[definition.CostPolicyAlias],
			SchedulingPolicy: policyRefs[definition.SchedulingPolicyAlias],
			Bindings:         []controlcontract.BindingSpec{},
		}
	}
	frozen, _, _, err := controlcontract.NewControlSnapshot(
		controlcontract.ControlSnapshot{
			SchemaVersion:   controlcontract.ControlSnapshotSchemaVersionV1,
			SnapshotID:      seed.Control.SnapshotID,
			TenantID:        seed.TenantID,
			Revision:        seed.Control.Revision,
			Agents:          append([]corecontract.AgentRef(nil), agentRefs...),
			CompositeAgents: cloneCompositeAgentDefinitions(seed.CompositeAgents),
			Workspaces:      workspaces,
			Profiles:        profiles,
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: Composite Agent definitions do not close over the seeded definitions: %v",
			ErrInvalidSeed,
			err,
		)
	}
	return cloneCompositeAgentDefinitions(frozen.CompositeAgents), nil
}

func prepareAgentRef(seed definitionSeed) (corecontract.AgentRef, error) {
	digest, err := definitionDigest(agentDefinitionDigestDomain, seed.ID, seed.Version, seed.Body)
	if err != nil {
		return corecontract.AgentRef{}, fmt.Errorf("%w: Agent definition: %v", ErrInvalidSeed, err)
	}
	ref := corecontract.AgentRef{ID: seed.ID, Version: seed.Version, Digest: digest}
	if err := ref.Validate(); err != nil {
		return corecontract.AgentRef{}, fmt.Errorf("%w: %v", ErrInvalidSeed, err)
	}
	return ref, nil
}

func prepareWorkspaceRef(seed workspaceSeed) (corecontract.WorkspaceRef, error) {
	digest, err := definitionDigest(workspaceDefinitionDigestDomain, seed.ID, seed.Version, seed.Body)
	if err != nil {
		return corecontract.WorkspaceRef{}, fmt.Errorf("%w: Workspace definition: %v", ErrInvalidSeed, err)
	}
	ref := corecontract.WorkspaceRef{ID: seed.ID, Version: seed.Version, Digest: digest}
	if err := ref.Validate(); err != nil {
		return corecontract.WorkspaceRef{}, fmt.Errorf("%w: %v", ErrInvalidSeed, err)
	}
	return ref, nil
}

func prepareProfileRef(seed profileSeed) (corecontract.ProfileRef, error) {
	digest, err := definitionDigest(profileDefinitionDigestDomain, seed.ID, seed.Version, seed.Body)
	if err != nil {
		return corecontract.ProfileRef{}, fmt.Errorf("%w: Profile definition: %v", ErrInvalidSeed, err)
	}
	ref := corecontract.ProfileRef{ID: seed.ID, Version: seed.Version, Digest: digest}
	if err := ref.Validate(); err != nil {
		return corecontract.ProfileRef{}, fmt.Errorf("%w: %v", ErrInvalidSeed, err)
	}
	return ref, nil
}

func definitionDigest(domain, id, version string, body json.RawMessage) (string, error) {
	if err := validateOpaque("definition ID", id); err != nil {
		return "", err
	}
	if err := validateOpaque("definition version", version); err != nil {
		return "", err
	}
	canonicalBody, err := moduleapi.CanonicalJSON(body)
	if err != nil || len(canonicalBody) == 0 || canonicalBody[0] != '{' {
		return "", fmt.Errorf("definition body must be a canonical JSON object")
	}
	if err := rejectSecretValues(canonicalBody); err != nil {
		return "", err
	}
	wire := struct {
		ID      string          `json:"id"`
		Version string          `json:"version"`
		Body    json.RawMessage `json:"body"`
	}{ID: id, Version: version, Body: canonicalBody}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return "", err
	}
	canonical, err := moduleapi.CanonicalJSON(encoded)
	if err != nil {
		return "", err
	}
	return moduleapi.Digest(domain, canonical), nil
}

func (prepared *Prepared) installAndActivate(
	ctx context.Context,
	store *currentstore.Store,
	resolver ActivationResolver,
	seed moduleSeed,
	assertion ModuleAssertion,
	manifestCanonical []byte,
) (
	currentstore.ModuleInstallation,
	currentstore.ModuleActivation,
	moduleapi.ActivatedModuleRef,
	error,
) {
	manifestRef, err := currentstore.ComputeContentDigest(
		currentstore.ContentModuleManifest,
		jsonMediaType,
		manifestCanonical,
	)
	if err != nil {
		return currentstore.ModuleInstallation{}, currentstore.ModuleActivation{},
			moduleapi.ActivatedModuleRef{}, fmt.Errorf("compute manifest ref: %w", err)
	}
	installation, err := store.InstallModule(ctx, currentstore.InstallModuleInput{
		InstallationID:      seed.InstallationID,
		ModuleID:            assertion.ModuleID,
		ExactVersion:        assertion.ExactVersion,
		ExpectedManifestRef: manifestRef,
		ManifestBytes:       bytes.Clone(manifestCanonical),
		ArtifactDigest:      assertion.ArtifactDigest,
	})
	if err != nil {
		return currentstore.ModuleInstallation{}, currentstore.ModuleActivation{},
			moduleapi.ActivatedModuleRef{}, fmt.Errorf("install verified module: %w", err)
	}
	activationInput, err := resolver.Resolve(ctx, activationresolver.ResolveInput{
		Installation:            installation,
		TenantID:                prepared.seed.TenantID,
		InstanceID:              assertion.InstanceID,
		ActivationRevision:      assertion.ActivationRevision,
		ExpectedExecutionClass:  assertion.ExpectedExecutionClass,
		ExpectedAdapterIdentity: assertion.ExpectedAdapterIdentity,
	})
	if err != nil {
		return currentstore.ModuleInstallation{}, currentstore.ModuleActivation{},
			moduleapi.ActivatedModuleRef{}, fmt.Errorf("resolve local activation: %w", err)
	}
	if err := prepared.verifyResolvedActivation(
		activationInput,
		installation,
		assertion,
	); err != nil {
		return currentstore.ModuleInstallation{}, currentstore.ModuleActivation{},
			moduleapi.ActivatedModuleRef{}, err
	}
	activation, err := store.ActivateModule(ctx, activationInput)
	if err != nil {
		return currentstore.ModuleInstallation{}, currentstore.ModuleActivation{},
			moduleapi.ActivatedModuleRef{}, fmt.Errorf("activate verified module: %w", err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           installation.ModuleID,
		Version:            installation.ExactVersion,
		ArtifactDigest:     installation.ArtifactDigest,
		InstanceID:         activation.InstanceID,
		ExecutionClass:     activation.ExecutionClass,
		AdapterIdentity:    activation.AdapterIdentity,
		ActivationRevision: activation.ActivationRevision,
	}
	if err := provider.Validate(); err != nil {
		return currentstore.ModuleInstallation{}, currentstore.ModuleActivation{},
			moduleapi.ActivatedModuleRef{}, fmt.Errorf(
				"%w: resolved provider: %v",
				ErrInvalidSeed,
				err,
			)
	}
	return installation, activation, provider, nil
}

func (prepared *Prepared) verifyResolvedActivation(
	input currentstore.ActivateModuleInput,
	installation currentstore.ModuleInstallation,
	assertion ModuleAssertion,
) error {
	if input.TenantID != prepared.seed.TenantID ||
		input.InstanceID != assertion.InstanceID ||
		input.InstallationID != installation.InstallationID ||
		input.ActivationRevision != assertion.ActivationRevision ||
		input.ExecutionClass != assertion.ExpectedExecutionClass ||
		input.AdapterIdentity != assertion.ExpectedAdapterIdentity {
		return fmt.Errorf("%w: local activation result does not match verified coordinates and seed assertions", ErrConflict)
	}
	return nil
}

func (prepared *Prepared) buildPublication(
	modelProvider moduleapi.ActivatedModuleRef,
	contextProviders []moduleapi.ActivatedModuleRef,
	knowledgeProviders []moduleapi.ActivatedModuleRef,
	memoryProviders []moduleapi.ActivatedModuleRef,
	actionProviders []moduleapi.ActivatedModuleRef,
) (
	controlcontract.ControlSnapshot,
	controlcontract.ControlSnapshotRef,
	[]byte,
	controlcontract.CatalogGeneration,
	controlcontract.CatalogGenerationRef,
	[]byte,
	error,
) {
	if len(contextProviders) != len(prepared.contexts) {
		return controlcontract.ControlSnapshot{}, controlcontract.ControlSnapshotRef{}, nil,
			controlcontract.CatalogGeneration{}, controlcontract.CatalogGenerationRef{}, nil,
			fmt.Errorf("%w: declarative context activation count changed", ErrConflict)
	}
	if len(knowledgeProviders) != len(prepared.knowledgeContexts) {
		return controlcontract.ControlSnapshot{}, controlcontract.ControlSnapshotRef{}, nil,
			controlcontract.CatalogGeneration{}, controlcontract.CatalogGenerationRef{}, nil,
			fmt.Errorf("%w: knowledge context activation count changed", ErrConflict)
	}
	if len(memoryProviders) != len(prepared.memoryContexts) {
		return controlcontract.ControlSnapshot{}, controlcontract.ControlSnapshotRef{}, nil,
			controlcontract.CatalogGeneration{}, controlcontract.CatalogGenerationRef{}, nil,
			fmt.Errorf("%w: Memory context activation count changed", ErrConflict)
	}
	if len(actionProviders) != len(prepared.actions) {
		return controlcontract.ControlSnapshot{}, controlcontract.ControlSnapshotRef{}, nil,
			controlcontract.CatalogGeneration{}, controlcontract.CatalogGenerationRef{}, nil,
			fmt.Errorf("%w: Action activation count changed", ErrConflict)
	}
	refs := make(map[string]corecontract.PolicyRef, len(prepared.policies))
	for _, policy := range prepared.policies {
		refs[policy.alias] = policy.ref
	}
	modelBinding := prepared.seed.ModelBinding
	bindings := make(
		[]controlcontract.BindingSpec,
		0,
		len(prepared.contexts)+len(prepared.knowledgeContexts)+
			len(prepared.memoryContexts)+len(prepared.actions)+1,
	)
	entries := make(
		[]controlcontract.CatalogEntry,
		0,
		len(prepared.contexts)+len(prepared.knowledgeContexts)+
			len(prepared.memoryContexts)+len(prepared.actions)+1,
	)
	for index, declarative := range prepared.contexts {
		bindings = append(bindings, controlcontract.BindingSpec{
			Port:                declarative.seed.Port,
			InstanceID:          declarative.seed.Module.InstanceID,
			ConfigRef:           declarative.configRef,
			AuthorityCeilingRef: prepared.authorityRef,
			StaticContextRefs:   []string{declarative.staticContextRef},
			FailurePolicy:       declarative.seed.FailurePolicy,
		})
		entries = append(entries, controlcontract.CatalogEntry{
			Activation: contextProviders[index],
			Provides:   []moduleapi.PortRef{declarative.seed.Port},
		})
	}
	for index, knowledge := range prepared.knowledgeContexts {
		bindings = append(bindings, controlcontract.BindingSpec{
			Port:                knowledge.seed.Port,
			InstanceID:          knowledge.seed.Module.InstanceID,
			ConfigRef:           knowledge.configRef,
			AuthorityCeilingRef: knowledge.authorityRef,
			StaticContextRefs:   []string{},
			FailurePolicy:       knowledge.seed.FailurePolicy,
		})
		entries = append(entries, controlcontract.CatalogEntry{
			Activation: knowledgeProviders[index],
			Provides:   []moduleapi.PortRef{knowledge.seed.Port},
		})
	}
	for index, memory := range prepared.memoryContexts {
		bindings = append(bindings, controlcontract.BindingSpec{
			Port:                memory.seed.Port,
			InstanceID:          memory.seed.Module.InstanceID,
			ConfigRef:           memory.configRef,
			AuthorityCeilingRef: memory.authorityRef,
			StaticContextRefs:   []string{},
			FailurePolicy:       memory.seed.FailurePolicy,
		})
		entries = append(entries, controlcontract.CatalogEntry{
			Activation: memoryProviders[index],
			Provides:   []moduleapi.PortRef{memory.seed.Port},
		})
	}
	for index, action := range prepared.actions {
		bindings = append(bindings, controlcontract.BindingSpec{
			Port:                action.seed.Port,
			InstanceID:          action.seed.Module.InstanceID,
			ConfigRef:           action.configRef,
			AuthorityCeilingRef: action.authorityRef,
			StaticContextRefs:   []string{},
			FailurePolicy:       action.seed.FailurePolicy,
		})
		entries = append(entries, controlcontract.CatalogEntry{
			Activation: actionProviders[index],
			Provides:   []moduleapi.PortRef{action.seed.Port},
		})
	}
	bindings = append(bindings, controlcontract.BindingSpec{
		Port:                modelBinding.Port,
		InstanceID:          modelBinding.InstanceID,
		ConfigRef:           prepared.configRef,
		AuthorityCeilingRef: prepared.authorityRef,
		StaticContextRefs:   []string{},
		FailurePolicy:       modelBinding.FailurePolicy,
	})
	entries = append(entries, controlcontract.CatalogEntry{
		Activation: modelProvider,
		Provides:   []moduleapi.PortRef{modelBinding.Port},
	})
	modelProfileRef := cloneModelProfileRef(prepared.modelProfileRef)
	workspaceSeeds := append(
		[]workspaceSeed{prepared.seed.Definitions.Workspace},
		prepared.seed.Definitions.AdditionalWorkspaces...,
	)
	workspaces := make(
		[]controlcontract.WorkspaceDefinition,
		len(prepared.workspaceRefs),
	)
	for index, workspaceRef := range prepared.workspaceRefs {
		workspaces[index] = controlcontract.WorkspaceDefinition{
			Workspace:    workspaceRef,
			BudgetPolicy: refs[workspaceSeeds[index].BudgetPolicyAlias],
		}
	}
	profileSeeds := append(
		[]profileSeed{prepared.seed.Definitions.Profile},
		prepared.seed.Definitions.AdditionalProfiles...,
	)
	profiles := make(
		[]controlcontract.ProfileDefinition,
		len(prepared.profileRefs),
	)
	for index, profileRef := range prepared.profileRefs {
		profileDefinition := profileSeeds[index]
		profiles[index] = controlcontract.ProfileDefinition{
			Profile:          profileRef,
			ModelProfile:     cloneModelProfileRef(modelProfileRef),
			ContextPolicy:    refs[profileDefinition.ContextPolicyAlias],
			CostPolicy:       refs[profileDefinition.CostPolicyAlias],
			SchedulingPolicy: refs[profileDefinition.SchedulingPolicyAlias],
			Bindings:         cloneBindingSpecs(bindings),
		}
	}
	control, controlRef, controlCanonical, err := controlcontract.NewControlSnapshot(
		controlcontract.ControlSnapshot{
			SchemaVersion: controlcontract.ControlSnapshotSchemaVersionV1,
			SnapshotID:    prepared.seed.Control.SnapshotID,
			TenantID:      prepared.seed.TenantID,
			Revision:      prepared.seed.Control.Revision,
			Agents: append(
				[]corecontract.AgentRef(nil),
				prepared.agentRefs...,
			),
			CompositeAgents: cloneCompositeAgentDefinitions(
				prepared.seed.CompositeAgents,
			),
			Workspaces: workspaces,
			Profiles:   profiles,
		},
	)
	if err != nil {
		return controlcontract.ControlSnapshot{}, controlcontract.ControlSnapshotRef{}, nil,
			controlcontract.CatalogGeneration{}, controlcontract.CatalogGenerationRef{}, nil,
			fmt.Errorf("bootstrapseed: build ControlSnapshot: %w", err)
	}
	catalog, catalogRef, catalogCanonical, err := controlcontract.NewCatalogGeneration(
		controlcontract.CatalogGeneration{
			SchemaVersion:         controlcontract.CatalogGenerationSchemaVersionV1,
			GenerationID:          prepared.seed.Catalog.GenerationID,
			Generation:            prepared.seed.Catalog.Generation,
			TenantID:              prepared.seed.TenantID,
			ControlSnapshotID:     controlRef.SnapshotID,
			ControlSnapshotDigest: controlRef.Digest,
			Entries:               entries,
		},
	)
	if err != nil {
		return controlcontract.ControlSnapshot{}, controlcontract.ControlSnapshotRef{}, nil,
			controlcontract.CatalogGeneration{}, controlcontract.CatalogGenerationRef{}, nil,
			fmt.Errorf("bootstrapseed: build CatalogGeneration: %w", err)
	}
	return control, controlRef, controlCanonical, catalog, catalogRef, catalogCanonical, nil
}

func (prepared *Prepared) publishOrVerify(
	ctx context.Context,
	store *currentstore.Store,
	control controlcontract.ControlSnapshot,
	controlRef controlcontract.ControlSnapshotRef,
	controlCanonical []byte,
	catalog controlcontract.CatalogGeneration,
	catalogRef controlcontract.CatalogGenerationRef,
	catalogCanonical []byte,
) (controlcontract.PublishedBasis, error) {
	existingBasis, existingControl, existingCatalog, err := store.LoadPublishedBasis(
		ctx,
		prepared.seed.TenantID,
	)
	if err == nil {
		_, loadedControlRef, loadedControlCanonical, rebuildControlErr :=
			controlcontract.NewControlSnapshot(existingControl)
		_, loadedCatalogRef, loadedCatalogCanonical, rebuildCatalogErr :=
			controlcontract.NewCatalogGeneration(existingCatalog)
		if rebuildControlErr != nil || rebuildCatalogErr != nil ||
			existingBasis.PointerRevision != prepared.seed.Control.PointerRevision ||
			loadedControlRef != controlRef || loadedCatalogRef != catalogRef ||
			!bytes.Equal(loadedControlCanonical, controlCanonical) ||
			!bytes.Equal(loadedCatalogCanonical, catalogCanonical) {
			return controlcontract.PublishedBasis{}, fmt.Errorf("%w: tenant already has a different current Control/Catalog", ErrConflict)
		}
		return existingBasis, nil
	}
	if !errors.Is(err, currentstore.ErrPublishedBasisNotFound) {
		return controlcontract.PublishedBasis{}, fmt.Errorf("bootstrapseed: inspect current publication: %w", err)
	}
	basis, err := store.PublishControlCatalog(ctx, currentstore.PublishControlCatalogInput{
		ExpectedPointerRevision: 0,
		NewPointerRevision:      prepared.seed.Control.PointerRevision,
		ControlRef:              controlRef,
		ControlCanonical:        controlCanonical,
		CatalogRef:              catalogRef,
		CatalogCanonical:        catalogCanonical,
	})
	if err != nil {
		return controlcontract.PublishedBasis{}, fmt.Errorf("bootstrapseed: publish Control/Catalog: %w", err)
	}
	if basis.Control != controlRef || basis.Catalog != catalogRef ||
		basis.PointerRevision != prepared.seed.Control.PointerRevision ||
		control.TenantID != basis.TenantID || catalog.TenantID != basis.TenantID {
		return controlcontract.PublishedBasis{}, fmt.Errorf("%w: publication result differs from prepared seed", ErrConflict)
	}
	return basis, nil
}

func putContent(
	ctx context.Context,
	store *currentstore.Store,
	kind currentstore.ContentKind,
	digest string,
	canonical []byte,
) (currentstore.ContentRecord, error) {
	return store.PutContent(ctx, currentstore.ContentInput{
		Digest:         digest,
		Kind:           kind,
		MediaType:      jsonMediaType,
		CanonicalBytes: bytes.Clone(canonical),
	})
}

func validatePolicyAlias(alias string) error {
	if alias == "" || len(alias) > moduleapi.MaxOpaqueIDBytes ||
		strings.HasPrefix(alias, ".") || strings.HasSuffix(alias, ".") ||
		strings.Contains(alias, "..") || strings.Contains(alias, "secret") {
		return fmt.Errorf("%w: policy alias %q is not canonical", ErrInvalidSeed, alias)
	}
	for _, character := range []byte(alias) {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '-' {
			continue
		}
		return fmt.Errorf(
			"%w: policy alias %q may contain only lowercase ASCII letters, digits, dot and hyphen",
			ErrInvalidSeed,
			alias,
		)
	}
	return nil
}

func validateOpaque(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) ||
		len(value) > moduleapi.MaxOpaqueIDBytes || !utf8.ValidString(value) ||
		value != moduleapi.CanonicalText(value) {
		return fmt.Errorf("%w: %s must be canonical, non-empty and at most %d bytes", ErrInvalidSeed, name, moduleapi.MaxOpaqueIDBytes)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: %s contains a control character", ErrInvalidSeed, name)
		}
	}
	return nil
}

func rejectSecretValues(canonical json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return walkSecretValues(value)
}

func walkSecretValues(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(key))
			switch normalized {
			case "apikey", "authorization", "credential", "credentials", "password", "secret", "secretvalue", "token", "accesstoken", "bearertoken":
				return fmt.Errorf("secret value key %q is forbidden in a seed", key)
			}
			if err := walkSecretValues(nested); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := walkSecretValues(nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func clonePreparedPolicies(input []preparedPolicy) []preparedPolicy {
	result := make([]preparedPolicy, len(input))
	for index, policy := range input {
		policy.canonical = bytes.Clone(policy.canonical)
		result[index] = policy
	}
	return result
}

func clonePreparedDeclarativeContexts(
	input []preparedDeclarativeContext,
) []preparedDeclarativeContext {
	result := make([]preparedDeclarativeContext, len(input))
	for index, provider := range input {
		provider.seed.Config = bytes.Clone(provider.seed.Config)
		provider.manifestCanonical = bytes.Clone(provider.manifestCanonical)
		provider.configCanonical = bytes.Clone(provider.configCanonical)
		provider.staticContextCanonical = bytes.Clone(provider.staticContextCanonical)
		result[index] = provider
	}
	return result
}

func clonePreparedKnowledgeContexts(
	input []preparedKnowledgeContext,
) []preparedKnowledgeContext {
	result := make([]preparedKnowledgeContext, len(input))
	for index, provider := range input {
		provider.seed.Config.Parameters = bytes.Clone(
			provider.seed.Config.Parameters,
		)
		provider.seed.AuthorityCeiling.AllowedScopes = append(
			[]moduleapi.KnowledgeScopeRuleV1(nil),
			provider.seed.AuthorityCeiling.AllowedScopes...,
		)
		provider.manifestCanonical = bytes.Clone(provider.manifestCanonical)
		provider.configCanonical = bytes.Clone(provider.configCanonical)
		provider.authorityCanonical = bytes.Clone(provider.authorityCanonical)
		result[index] = provider
	}
	return result
}

func clonePreparedMemoryContexts(
	input []preparedMemoryContext,
) []preparedMemoryContext {
	result := make([]preparedMemoryContext, len(input))
	for index, provider := range input {
		provider.seed.Config.Parameters = bytes.Clone(
			provider.seed.Config.Parameters,
		)
		provider.seed.AuthorityCeiling.AllowedWorkspaceIDs = append(
			[]string(nil),
			provider.seed.AuthorityCeiling.AllowedWorkspaceIDs...,
		)
		provider.seed.AuthorityCeiling.AllowedKinds = append(
			[]moduleapi.MemoryEntryKindV1(nil),
			provider.seed.AuthorityCeiling.AllowedKinds...,
		)
		provider.manifestCanonical = bytes.Clone(provider.manifestCanonical)
		provider.configCanonical = bytes.Clone(provider.configCanonical)
		provider.authorityCanonical = bytes.Clone(provider.authorityCanonical)
		result[index] = provider
	}
	return result
}

func clonePreparedActionProviders(
	input []preparedActionProvider,
) []preparedActionProvider {
	result := make([]preparedActionProvider, len(input))
	for index, provider := range input {
		provider.seed.Config.Actions = append(
			[]moduleapi.ActionBindingMappingV1(nil),
			provider.seed.Config.Actions...,
		)
		provider.seed.Config.Parameters = bytes.Clone(
			provider.seed.Config.Parameters,
		)
		provider.seed.AuthorityCeiling.AllowedWorkspaceIDs = append(
			[]string(nil),
			provider.seed.AuthorityCeiling.AllowedWorkspaceIDs...,
		)
		provider.seed.AuthorityCeiling.AllowedProviderActionIDs = append(
			[]string(nil),
			provider.seed.AuthorityCeiling.AllowedProviderActionIDs...,
		)
		provider.manifestCanonical = bytes.Clone(provider.manifestCanonical)
		provider.configCanonical = bytes.Clone(provider.configCanonical)
		provider.authorityCanonical = bytes.Clone(provider.authorityCanonical)
		result[index] = provider
	}
	return result
}

func cloneModelProfileRef(
	input *corecontract.ModelProfileRef,
) *corecontract.ModelProfileRef {
	if input == nil {
		return nil
	}
	cloned := *input
	return &cloned
}

func cloneBindingSpecs(
	input []controlcontract.BindingSpec,
) []controlcontract.BindingSpec {
	result := append([]controlcontract.BindingSpec(nil), input...)
	for index := range result {
		result[index].StaticContextRefs = append(
			[]string(nil),
			result[index].StaticContextRefs...,
		)
	}
	return result
}

func cloneSeed(seed seedV1) seedV1 {
	seed.Definitions.Agent.Body = bytes.Clone(seed.Definitions.Agent.Body)
	seed.Definitions.Workspace.Body = bytes.Clone(seed.Definitions.Workspace.Body)
	seed.Definitions.Profile.Body = bytes.Clone(seed.Definitions.Profile.Body)
	seed.Definitions.AdditionalAgents = append(
		[]definitionSeed(nil),
		seed.Definitions.AdditionalAgents...,
	)
	for index := range seed.Definitions.AdditionalAgents {
		seed.Definitions.AdditionalAgents[index].Body = bytes.Clone(
			seed.Definitions.AdditionalAgents[index].Body,
		)
	}
	seed.Definitions.AdditionalWorkspaces = append(
		[]workspaceSeed(nil),
		seed.Definitions.AdditionalWorkspaces...,
	)
	for index := range seed.Definitions.AdditionalWorkspaces {
		seed.Definitions.AdditionalWorkspaces[index].Body = bytes.Clone(
			seed.Definitions.AdditionalWorkspaces[index].Body,
		)
	}
	seed.Definitions.AdditionalProfiles = append(
		[]profileSeed(nil),
		seed.Definitions.AdditionalProfiles...,
	)
	for index := range seed.Definitions.AdditionalProfiles {
		seed.Definitions.AdditionalProfiles[index].Body = bytes.Clone(
			seed.Definitions.AdditionalProfiles[index].Body,
		)
	}
	seed.CompositeAgents = cloneCompositeAgentDefinitions(seed.CompositeAgents)
	seed.Policies = append([]policySeed(nil), seed.Policies...)
	for index := range seed.Policies {
		seed.Policies[index].Body = bytes.Clone(seed.Policies[index].Body)
	}
	seed.ModelBinding.Config.Parameters = bytes.Clone(seed.ModelBinding.Config.Parameters)
	if seed.ModelProfile != nil {
		profile := *seed.ModelProfile
		profile.CapabilityTendencies = append(
			[]corecontract.ModelTendencyV1{},
			profile.CapabilityTendencies...,
		)
		profile.ReliabilityTendencies = append(
			[]corecontract.ModelTendencyV1{},
			profile.ReliabilityTendencies...,
		)
		seed.ModelProfile = &profile
	}
	seed.DeclarativeContextProviders = append(
		[]declarativeContextProviderSeed(nil),
		seed.DeclarativeContextProviders...,
	)
	for index := range seed.DeclarativeContextProviders {
		seed.DeclarativeContextProviders[index].Config = bytes.Clone(
			seed.DeclarativeContextProviders[index].Config,
		)
	}
	seed.KnowledgeContextProviders = append(
		[]knowledgeContextProviderSeed(nil),
		seed.KnowledgeContextProviders...,
	)
	for index := range seed.KnowledgeContextProviders {
		provider := &seed.KnowledgeContextProviders[index]
		provider.Config.Parameters = bytes.Clone(provider.Config.Parameters)
		provider.AuthorityCeiling.AllowedScopes = append(
			[]moduleapi.KnowledgeScopeRuleV1(nil),
			provider.AuthorityCeiling.AllowedScopes...,
		)
	}
	seed.MemoryContextProviders = append(
		[]memoryContextProviderSeed(nil),
		seed.MemoryContextProviders...,
	)
	for index := range seed.MemoryContextProviders {
		provider := &seed.MemoryContextProviders[index]
		provider.Config.Parameters = bytes.Clone(provider.Config.Parameters)
		provider.AuthorityCeiling.AllowedWorkspaceIDs = append(
			[]string(nil),
			provider.AuthorityCeiling.AllowedWorkspaceIDs...,
		)
		provider.AuthorityCeiling.AllowedKinds = append(
			[]moduleapi.MemoryEntryKindV1(nil),
			provider.AuthorityCeiling.AllowedKinds...,
		)
	}
	seed.ActionProviders = append(
		[]actionProviderSeed(nil),
		seed.ActionProviders...,
	)
	for index := range seed.ActionProviders {
		provider := &seed.ActionProviders[index]
		provider.Config.Actions = append(
			[]moduleapi.ActionBindingMappingV1(nil),
			provider.Config.Actions...,
		)
		provider.Config.Parameters = bytes.Clone(provider.Config.Parameters)
		provider.AuthorityCeiling.AllowedWorkspaceIDs = append(
			[]string(nil),
			provider.AuthorityCeiling.AllowedWorkspaceIDs...,
		)
		provider.AuthorityCeiling.AllowedProviderActionIDs = append(
			[]string(nil),
			provider.AuthorityCeiling.AllowedProviderActionIDs...,
		)
	}
	seed.PriceSnapshot.Pricing = bytes.Clone(seed.PriceSnapshot.Pricing)
	return seed
}

func cloneCompositeAgentDefinitions(
	input []controlcontract.CompositeAgentDefinitionV1,
) []controlcontract.CompositeAgentDefinitionV1 {
	if len(input) == 0 {
		return nil
	}
	result := append(
		[]controlcontract.CompositeAgentDefinitionV1(nil),
		input...,
	)
	for index := range result {
		result[index].Members = append(
			[]controlcontract.CompositeAgentMemberV1(nil),
			result[index].Members...,
		)
		if result[index].Reviewer != nil {
			reviewer := *result[index].Reviewer
			result[index].Reviewer = &reviewer
		}
	}
	return result
}
