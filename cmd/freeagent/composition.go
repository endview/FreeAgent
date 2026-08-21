package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/actionmaterializer"
	"github.com/endview/freeagent/internal/activationresolver"
	"github.com/endview/freeagent/internal/bootstrapseed"
	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/coreloop"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/deepseekmodel"
	"github.com/endview/freeagent/internal/exactadapter"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/internal/remoteactionhttp"
	"github.com/endview/freeagent/internal/runscheduler"
	"github.com/endview/freeagent/internal/wasmaction"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	localEchoModuleID         = "freeagent.builtin.model.echo"
	localEchoModuleVersion    = "1.0.0"
	localEchoArtifactDigest   = "884c16b339b6f9154a6f64b23e800010f0b29074c1ad9a01c540f9c9ed9182f7"
	localEchoAdapterIdentity  = "freeagent.adapter.model.echo/v1"
	localDeepSeekModuleID     = "freeagent.builtin.model.deepseek"
	localDeepSeekVersion      = "1.0.0"
	localDeepSeekDigest       = "e7864f4478a588dad4de9fff53b0c4dcecc17420e80420018bcc5fe5502887c3"
	localDeepSeekSize         = uint64(2914)
	localDeepSeekEntrypoint   = "freeagent.manifest-request.model.deepseek/v1"
	localDeepSeekSchemaPath   = "schemas/config.schema.json"
	localDeepSeekSchemaDigest = "2113510aca9f5332ba0a7ace2267d9964295c087fadd3ca4be5c47ba7e863d66"
	localDeepSeekFlashBuild   = "deepseek-v4-flash/public-alias-observed-2026-08-04"
	localDeepSeekProBuild     = "deepseek-v4-pro/public-alias-observed-2026-08-04"
	localKnowledgeAdapterID   = "freeagent.adapter.knowledge.lexical/v1"
	localMemoryAdapterID      = "freeagent.adapter.memory.deterministic/v1"
	localTextStatsModuleID    = "freeagent.builtin.action.text_stats"
	localTextStatsVersion     = "1.0.0"
	localTextStatsDigest      = "2331b8b5209f4acffd7fef96f55ce1e0841bc3c4e39d90581efb941ecae9b955"
	localTextStatsAdapterID   = "freeagent.adapter.action.text-stats/v1"
	localTextStatsEntrypoint  = "freeagent.manifest-request.action.text-stats/v1"
	declarativeAdapterID      = "freeagent.adapter.declarative/v1"
)

var (
	productionModelPort = moduleapi.PortRef{
		Name:         moduleapi.PortNameModelGenerate,
		ExactVersion: moduleapi.PortVersionV1,
	}
	productionContextPort = moduleapi.PortRef{
		Name:         moduleapi.PortNameContextProvide,
		ExactVersion: moduleapi.PortVersionV1,
	}
	productionActionPort = moduleapi.PortRef{
		Name:         moduleapi.PortNameActionProvider,
		ExactVersion: moduleapi.PortVersionV1,
	}
)

type initInput struct {
	DatabasePath           string
	SeedPath               string
	ArtifactRoot           string
	LocalMCPArtifactGrants []string
}

type initResult struct {
	DatabasePath  string                        `json:"database_path"`
	ArtifactRoot  string                        `json:"artifact_root"`
	SeedID        string                        `json:"seed_id"`
	SeedRevision  uint64                        `json:"seed_revision"`
	ArtifactLocks []string                      `json:"artifact_locks"`
	Defaults      bootstrapseed.DefaultAssembly `json:"defaults"`
}

type productionComposition struct {
	store     *currentstore.Store
	registry  *exactadapter.Registry
	coreLoop  *coreloop.UniversalLoop
	loop      loopapi.Loop
	scheduler schedulerCloser
	chat      *localchat.ChatService
	composite *localchat.CompositeChatService
	channel   *productionChannelEndpointRuntime
}

type schedulerCloser interface {
	Close(context.Context) error
}

func (composition *productionComposition) Close() error {
	if composition == nil || composition.store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	drainErr := composition.DrainRuntimeV1(ctx)
	cancel()
	if drainErr != nil {
		// The Store remains open while a claimed Loop quantum may still be
		// settling. A later Close call can finish the ordered drain.
		return drainErr
	}
	return composition.CloseStoreV1()
}

// DrainRuntimeV1 is the context-owned runtime phase shared by the optional
// Control shutdown coordinator. It does not manufacture or detach a deadline.
func (composition *productionComposition) DrainRuntimeV1(ctx context.Context) error {
	if composition == nil || composition.store == nil || ctx == nil {
		return errors.New("composition: runtime drain is not initialized")
	}
	if composition.scheduler == nil {
		return nil
	}
	return composition.scheduler.Close(ctx)
}

// CloseStoreV1 closes only the unique Store. Callers must first prove that
// every admitted handler and shared runtime has drained.
func (composition *productionComposition) CloseStoreV1() error {
	if composition == nil || composition.store == nil {
		return nil
	}
	return composition.store.Close()
}

type productionCompositionOptions struct {
	FairScheduler *runscheduler.Config
	DeepSeek      *productionDeepSeekRuntimeConfig
	RemoteAction  *productionRemoteActionRuntimeConfig
	WASMAction    *productionWASMActionRuntimeConfig
}

// productionRemoteActionRuntimeConfig contains the sole process-owned
// capability accepted by the REMOTE Action composition root. HTTP clients,
// transports, endpoints and SecretRefs are deliberately not injectable here.
// The Adapter restores endpoint and SecretRef from each private execution
// closure and owns the hardened HTTP transport itself.
type productionRemoteActionRuntimeConfig struct {
	SecretResolver remoteactionhttp.SecretResolver
}

// productionWASMActionRuntimeConfig contains only process-local, deny-only
// runtime admission policy. It is not persisted and supplies no Secret,
// network, filesystem, WASI, endpoint, or generic ModuleHost capability.
type productionWASMActionRuntimeConfig struct {
	Enabled                bool
	AllowedArtifactDigests []string
}

// productionDeepSeekRuntimeConfig contains process-owned capabilities only.
// Neither value is serialized into a module artifact, Binding, Store row, or
// model request. The exact provider, models, builds, and endpoint remain
// frozen by the compiled composition root and deepseekmodel adapter.
type productionDeepSeekRuntimeConfig struct {
	APIKeyResolver deepseekmodel.APIKeyResolver
	HTTPClient     *http.Client
}

func resolvedArtifactRoot(databasePath, configured string) string {
	if strings.TrimSpace(configured) != "" {
		return configured
	}
	return databasePath + ".artifacts"
}

func initializeProductionData(
	ctx context.Context,
	input initInput,
) (initResult, error) {
	if ctx == nil {
		return initResult{}, errors.New("composition: context is nil")
	}
	prepared, err := bootstrapseed.PrepareFile(input.SeedPath)
	if err != nil {
		return initResult{}, err
	}
	assertions := prepared.ModuleAssertions()
	if len(assertions) == 0 {
		return initResult{}, errors.New("composition: bootstrap seed has no artifacts")
	}
	resolver, err := newBootstrapActivationResolverWithLocalMCP(
		assertions,
		input.LocalMCPArtifactGrants,
	)
	if err != nil {
		return initResult{}, err
	}

	databasePath, err := resolveNewTarget(input.DatabasePath, "database")
	if err != nil {
		return initResult{}, err
	}
	artifactRoot, err := resolveNewTarget(input.ArtifactRoot, "artifact root")
	if err != nil {
		return initResult{}, err
	}
	if samePath(databasePath, artifactRoot) ||
		pathContains(artifactRoot, databasePath) ||
		pathContains(databasePath, artifactRoot) {
		return initResult{}, errors.New("composition: database and artifact root must be separate targets")
	}
	if err := requireAbsent(databasePath, "database"); err != nil {
		return initResult{}, err
	}

	stagedArtifacts, err := moduleartifactstore.ProvisionArtifactStagingRootLeafV1(
		filepath.Dir(artifactRoot),
		".freeagent-artifacts-*",
	)
	if err != nil {
		return initResult{}, fmt.Errorf("composition: create artifact staging root: %w", err)
	}
	stagedDatabase, err := newDatabaseStagingPath(filepath.Dir(databasePath))
	if err != nil {
		_ = os.RemoveAll(stagedArtifacts)
		return initResult{}, err
	}
	defer func() {
		if stagedArtifacts != "" {
			_ = os.RemoveAll(stagedArtifacts)
		}
		if stagedDatabase != "" {
			_ = os.Remove(stagedDatabase)
		}
	}()

	locks := make([]string, 0, len(assertions))
	seen := make(map[string]struct{}, len(assertions))
	for _, assertion := range assertions {
		if _, duplicate := seen[assertion.ArtifactDigest]; duplicate {
			continue
		}
		seen[assertion.ArtifactDigest] = struct{}{}
		if err := stageArtifact(stagedArtifacts, assertion); err != nil {
			return initResult{}, err
		}
		locks = append(locks, assertion.ArtifactDigest)
	}
	sort.Strings(locks)
	if err := syncInitTreeDirectories(stagedArtifacts); err != nil {
		return initResult{}, fmt.Errorf("composition: sync staged artifacts: %w", err)
	}
	stagedArtifactRoot, err := moduleartifactstore.SelectArtifactRootV1(stagedArtifacts)
	if err != nil {
		return initResult{}, fmt.Errorf("composition: staged artifact root is unsafe: %w", err)
	}
	if err := moduleartifactstore.VerifyPhysicalRootClosureV1(ctx, stagedArtifactRoot); err != nil {
		return initResult{}, fmt.Errorf("composition: staged artifact root exceeds physical closure: %w", err)
	}
	if _, err := currentstore.InitFreshCurrentStore(ctx, stagedDatabase); err != nil {
		return initResult{}, err
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, stagedDatabase)
	if err != nil {
		return initResult{}, err
	}
	imported, importErr := prepared.Import(ctx, store, resolver)
	closeErr := store.Close()
	if err := errors.Join(importErr, closeErr); err != nil {
		return initResult{}, err
	}
	if _, err := currentstore.PrepareClosedCurrentStoreForPublication(
		ctx,
		stagedDatabase,
	); err != nil {
		return initResult{}, err
	}
	if err := os.Remove(stagedDatabase + ".freeagent.owner.lock"); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return initResult{}, fmt.Errorf("composition: remove staging owner lock: %w", err)
	}

	if err := publishInitNoReplace(stagedArtifacts, artifactRoot); err != nil {
		if verifyErr := verifyExactInitArtifactRoot(artifactRoot, assertions); verifyErr != nil {
			return initResult{}, errors.Join(
				fmt.Errorf("composition: publish artifact root: %w", err),
				verifyErr,
			)
		}
		if removeErr := os.RemoveAll(stagedArtifacts); removeErr != nil {
			return initResult{}, fmt.Errorf(
				"composition: remove redundant artifact staging root: %w",
				removeErr,
			)
		}
	}
	stagedArtifacts = ""
	publishedArtifactRoot, err := moduleartifactstore.SelectArtifactRootV1(artifactRoot)
	if err != nil {
		return initResult{}, fmt.Errorf("composition: published artifact root is unsafe: %w", err)
	}
	if err := moduleartifactstore.WithArtifactRootWriteLeaseV1(
		ctx,
		publishedArtifactRoot,
		func(lease *moduleartifactstore.ArtifactRootWriteLeaseV1) error {
			if err := verifyExactInitArtifactRoot(artifactRoot, assertions); err != nil {
				return err
			}
			if err := lease.VerifyPhysicalClosureV1(ctx); err != nil {
				return fmt.Errorf("composition: published artifact root exceeds physical closure: %w", err)
			}
			if err := syncInitTreeDirectories(artifactRoot); err != nil {
				return fmt.Errorf("composition: sync published artifacts: %w", err)
			}
			if err := syncInitDirectory(filepath.Dir(artifactRoot)); err != nil {
				return fmt.Errorf("composition: sync artifact parent: %w", err)
			}
			if err := publishInitNoReplace(stagedDatabase, databasePath); err != nil {
				return fmt.Errorf("composition: publish database without replacement: %w", err)
			}
			stagedDatabase = ""
			if err := syncInitDirectory(filepath.Dir(databasePath)); err != nil {
				return fmt.Errorf("composition: sync database parent: %w", err)
			}
			return lease.VerifyPhysicalClosureV1(ctx)
		},
	); err != nil {
		return initResult{}, err
	}

	return initResult{
		DatabasePath:  databasePath,
		ArtifactRoot:  artifactRoot,
		SeedID:        imported.SeedID,
		SeedRevision:  imported.SeedRevision,
		ArtifactLocks: locks,
		Defaults:      imported.DefaultAssembly,
	}, nil
}

func openProductionComposition(
	ctx context.Context,
	databasePath string,
	artifactRoot string,
	tenantID string,
) (*productionComposition, error) {
	return openProductionCompositionWithOptions(
		ctx,
		databasePath,
		artifactRoot,
		tenantID,
		productionCompositionOptions{},
	)
}

func openProductionCompositionWithOptions(
	ctx context.Context,
	databasePath string,
	artifactRoot string,
	tenantID string,
	options productionCompositionOptions,
) (*productionComposition, error) {
	if ctx == nil {
		return nil, errors.New("composition: context is nil")
	}
	if _, err := currentstore.VerifyCurrentStoreReadOnly(ctx, databasePath); err != nil {
		return nil, err
	}
	root, err := resolveExistingArtifactRootV1(artifactRoot)
	if err != nil {
		return nil, err
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, databasePath)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*productionComposition, error) {
		return nil, errors.Join(cause, store.Close())
	}
	// Startup recovery is a Store-only global safety scan. It runs before
	// Catalog-backed registry construction so historical optional modules can
	// never be loaded merely because the process is opening for traffic.
	if err := runProductionStartupRecovery(ctx, store); err != nil {
		return fail(err)
	}
	if options.FairScheduler != nil {
		if err := currentbackup.VerifyCurrentStoreSemanticClosure(
			ctx,
			databasePath,
		); err != nil {
			return fail(fmt.Errorf(
				"composition: fair Scheduler semantic enablement gate: %w",
				err,
			))
		}
	}
	_, _, catalog, err := store.LoadPublishedBasis(ctx, tenantID)
	if err != nil {
		return fail(err)
	}
	registry, err := newProductionAdapterRegistryWithActionRuntimesAndExtras(
		root,
		catalog.Entries,
		options.DeepSeek,
		options.RemoteAction,
		options.WASMAction,
	)
	if err != nil {
		return fail(err)
	}
	coreLoop, err := coreloop.NewUniversalLoop(store, registry)
	if err != nil {
		return fail(err)
	}
	var runExecutor loopapi.Loop = coreLoop
	var scheduler *runscheduler.Scheduler
	if options.FairScheduler != nil {
		config := *options.FairScheduler
		if config.TenantID != tenantID {
			return fail(errors.New(
				"composition: fair Scheduler tenant differs from composed tenant",
			))
		}
		scheduler, err = runscheduler.New(store, coreLoop, config)
		if err != nil {
			return fail(err)
		}
		runExecutor = scheduler
	}
	chat, err := newProductionChatService(store, runExecutor, registry)
	if err != nil {
		if scheduler != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = scheduler.Close(closeCtx)
			cancel()
		}
		return fail(err)
	}
	composite, err := localchat.NewCompositeChatService(store, runExecutor)
	if err != nil {
		if scheduler != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = scheduler.Close(closeCtx)
			cancel()
		}
		return fail(err)
	}
	return &productionComposition{
		store: store, registry: registry, coreLoop: coreLoop,
		loop: runExecutor, scheduler: scheduler, chat: chat,
		composite: composite,
	}, nil
}

func newProductionChatService(
	store *currentstore.Store,
	loop loopapi.Loop,
	registry modulehost.ExactAdapterRegistry,
) (*localchat.ChatService, error) {
	materializer, err := actionmaterializer.New(registry)
	if err != nil {
		return nil, err
	}
	return localchat.NewActionChatService(store, loop, materializer)
}

func newBootstrapActivationResolver(
	assertions []bootstrapseed.ModuleAssertion,
) (*activationresolver.Resolver, error) {
	return newBootstrapActivationResolverWithLocalMCP(assertions, nil)
}

func newBootstrapActivationResolverWithLocalMCP(
	assertions []bootstrapseed.ModuleAssertion,
	localMCPArtifactGrants []string,
) (*activationresolver.Resolver, error) {
	if len(assertions) == 0 {
		return nil, errors.New("composition: bootstrap seed has no module assertions")
	}
	if err := validateCompiledModelAssertion(assertions[0]); err != nil {
		return nil, err
	}
	modelProvider := providerFromAssertion(assertions[0])
	modelInvoker, err := newBootstrapModelProbe(assertions[0], modelProvider)
	if err != nil {
		return nil, err
	}
	registrations := []exactadapter.Registration{{
		ArtifactDigest:  modelProvider.ArtifactDigest,
		AdapterIdentity: modelProvider.AdapterIdentity,
		Invoker:         modelInvoker,
	}}
	allowlist := []activationresolver.TrustedInProcessAllowlistEntry{{
		ModuleID:        modelProvider.ModuleID,
		ExactVersion:    modelProvider.Version,
		ArtifactDigest:  modelProvider.ArtifactDigest,
		AdapterIdentity: modelProvider.AdapterIdentity,
	}}
	registeredProviders := map[string]moduleapi.ActivatedModuleRef{
		modelProvider.ArtifactDigest + "\x00" + modelProvider.AdapterIdentity: modelProvider,
	}
	trustedArtifacts := map[string]struct{}{
		modelProvider.ModuleID + "\x00" + modelProvider.Version + "\x00" +
			modelProvider.ArtifactDigest: {},
	}
	localGrantDigests := make(map[string]struct{}, len(localMCPArtifactGrants))
	for index, digest := range localMCPArtifactGrants {
		if !moduleapi.ValidSHA256(digest) {
			return nil, fmt.Errorf(
				"composition: local MCP grant %d must be an exact lowercase SHA-256 digest",
				index,
			)
		}
		if _, duplicate := localGrantDigests[digest]; duplicate {
			return nil, fmt.Errorf(
				"composition: duplicate local MCP artifact grant %s",
				digest,
			)
		}
		localGrantDigests[digest] = struct{}{}
	}
	localProcessGrants := make([]activationresolver.LocalProcessGrant, 0)
	usedLocalGrantDigests := make(map[string]struct{})
	for index, assertion := range assertions[1:] {
		if assertion.ExpectedExecutionClass == moduleapi.ExecutionDeclarative {
			continue
		}
		if assertion.ExpectedExecutionClass != moduleapi.ExecutionTrustedInProcess &&
			assertion.ExpectedExecutionClass != moduleapi.ExecutionLocalProcess {
			return nil, fmt.Errorf(
				"composition: bootstrap assertion %d uses unsupported execution class %q",
				index+1,
				assertion.ExpectedExecutionClass,
			)
		}
		provider := providerFromAssertion(assertion)
		if provider.ExecutionClass == moduleapi.ExecutionLocalProcess {
			if provider.AdapterIdentity != mcpstdio.AdapterIdentityV1 {
				return nil, fmt.Errorf(
					"composition: bootstrap assertion %d local adapter identity must be %q",
					index+1,
					mcpstdio.AdapterIdentityV1,
				)
			}
			if _, granted := localGrantDigests[provider.ArtifactDigest]; !granted {
				return nil, fmt.Errorf(
					"composition: bootstrap assertion %d LOCAL_PROCESS artifact %s lacks an explicit operator grant",
					index+1,
					provider.ArtifactDigest,
				)
			}
			usedLocalGrantDigests[provider.ArtifactDigest] = struct{}{}
		}
		registrationKey := provider.ArtifactDigest + "\x00" + provider.AdapterIdentity
		if registered, found := registeredProviders[registrationKey]; found {
			if !sameProviderArtifactAdapter(registered, provider) {
				return nil, fmt.Errorf(
					"composition: bootstrap assertion %d reuses an adapter key for a different artifact identity",
					index+1,
				)
			}
		} else {
			var registration exactadapter.Registration
			var err error
			switch provider.AdapterIdentity {
			case localKnowledgeAdapterID:
				registration, err = newKnowledgeRegistrationFromArtifact(
					provider,
					assertion.ArtifactDirectory,
					assertion.ArtifactSizeBytes,
				)
			case localMemoryAdapterID:
				registration, err = newMemoryRegistrationFromArtifact(
					provider,
					assertion.ArtifactDirectory,
					assertion.ArtifactSizeBytes,
				)
			case localTextStatsAdapterID:
				registration, err = newTextStatsRegistrationFromArtifact(
					provider,
					assertion.ArtifactDirectory,
					assertion.ArtifactSizeBytes,
				)
			case mcpstdio.AdapterIdentityV1:
				registration, err = newMCPRegistrationFromArtifact(
					provider,
					assertion.ArtifactDirectory,
					assertion.ArtifactSizeBytes,
				)
			default:
				err = fmt.Errorf(
					"adapter %q is not in the compiled bootstrap adapter set",
					provider.AdapterIdentity,
				)
			}
			if err != nil {
				return nil, fmt.Errorf(
					"composition: bootstrap context assertion %d: %w",
					index+1,
					err,
				)
			}
			registrations = append(registrations, registration)
			registeredProviders[registrationKey] = provider
		}
		if provider.ExecutionClass == moduleapi.ExecutionLocalProcess {
			localProcessGrants = append(
				localProcessGrants,
				activationresolver.LocalProcessGrant{
					ModuleID:        assertion.ModuleID,
					ExactVersion:    assertion.ExactVersion,
					ArtifactDigest:  assertion.ArtifactDigest,
					Protocol:        moduleapi.RuntimeProtocolMCPStdio20251125,
					AdapterIdentity: mcpstdio.AdapterIdentityV1,
				},
			)
		} else {
			trustedKey := provider.ModuleID + "\x00" + provider.Version + "\x00" +
				provider.ArtifactDigest
			if _, found := trustedArtifacts[trustedKey]; !found {
				allowlist = append(
					allowlist,
					activationresolver.TrustedInProcessAllowlistEntry{
						ModuleID:        assertion.ModuleID,
						ExactVersion:    assertion.ExactVersion,
						ArtifactDigest:  assertion.ArtifactDigest,
						AdapterIdentity: assertion.ExpectedAdapterIdentity,
					},
				)
				trustedArtifacts[trustedKey] = struct{}{}
			}
		}
	}
	for digest := range localGrantDigests {
		if _, used := usedLocalGrantDigests[digest]; !used {
			return nil, fmt.Errorf(
				"composition: local MCP artifact grant %s does not match a LOCAL_PROCESS seed assertion",
				digest,
			)
		}
	}
	registry, err := exactadapter.NewRegistry(registrations...)
	if err != nil {
		return nil, err
	}
	return activationresolver.New(activationresolver.Config{
		DeclarativeAdapterIdentity: declarativeAdapterID,
		TrustedInProcessAllowlist:  allowlist,
		LocalProcessGrants:         localProcessGrants,
	}, registry)
}

func sameProviderArtifactAdapter(
	left moduleapi.ActivatedModuleRef,
	right moduleapi.ActivatedModuleRef,
) bool {
	return left.ModuleID == right.ModuleID &&
		left.Version == right.Version &&
		left.ArtifactDigest == right.ArtifactDigest &&
		left.ExecutionClass == right.ExecutionClass &&
		left.AdapterIdentity == right.AdapterIdentity
}

func providerFromAssertion(
	assertion bootstrapseed.ModuleAssertion,
) moduleapi.ActivatedModuleRef {
	return moduleapi.ActivatedModuleRef{
		ModuleID:           assertion.ModuleID,
		Version:            assertion.ExactVersion,
		ArtifactDigest:     assertion.ArtifactDigest,
		InstanceID:         assertion.InstanceID,
		ExecutionClass:     assertion.ExpectedExecutionClass,
		AdapterIdentity:    assertion.ExpectedAdapterIdentity,
		ActivationRevision: assertion.ActivationRevision,
	}
}

func validateCompiledModelAssertion(
	assertion bootstrapseed.ModuleAssertion,
) error {
	provider := providerFromAssertion(assertion)
	if !isCompiledModelProvider(provider) {
		return errors.New(
			"composition: bootstrap model is not in the compiled local trust set",
		)
	}
	return nil
}

func newBootstrapModelProbe(
	assertion bootstrapseed.ModuleAssertion,
	provider moduleapi.ActivatedModuleRef,
) (modulehost.ModuleInvoker, error) {
	switch provider.ModuleID {
	case localEchoModuleID:
		// Preserve the original Echo initialization path exactly. In
		// particular, it performs no additional artifact read.
		return exactadapter.NewDeterministicEcho(provider)
	case localDeepSeekModuleID:
		if err := validateDeepSeekArtifact(
			assertion.ArtifactDirectory,
			assertion.ArtifactSizeBytes,
			provider,
		); err != nil {
			return nil, err
		}
		// Seed import needs only an exact registry-presence probe. It must not
		// resolve a credential, create an HTTP client, or make a request.
		return bootstrapModelActivationProbe{}, nil
	default:
		return nil, errors.New(
			"composition: bootstrap model is not in the compiled local trust set",
		)
	}
}

type bootstrapModelActivationProbe struct{}

func (bootstrapModelActivationProbe) Invoke(
	context.Context,
	modulehost.PreparedInvocation,
) (modulehost.InvocationResult, error) {
	return modulehost.InvocationResult{}, errors.New(
		"composition: bootstrap activation probe cannot execute model requests",
	)
}

func isCompiledModelProvider(provider moduleapi.ActivatedModuleRef) bool {
	if err := provider.Validate(); err != nil {
		return false
	}
	if provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess {
		return false
	}
	switch {
	case provider.ModuleID == localEchoModuleID &&
		provider.Version == localEchoModuleVersion &&
		provider.ArtifactDigest == localEchoArtifactDigest &&
		provider.AdapterIdentity == localEchoAdapterIdentity:
		return true
	case provider.ModuleID == localDeepSeekModuleID &&
		provider.Version == localDeepSeekVersion &&
		provider.ArtifactDigest == localDeepSeekDigest &&
		provider.AdapterIdentity == deepseekmodel.AdapterIdentityV1:
		return true
	default:
		return false
	}
}

func exactLocalModelProvider(
	entries []controlcontract.CatalogEntry,
) (moduleapi.ActivatedModuleRef, error) {
	var provider moduleapi.ActivatedModuleRef
	found := false
	for _, entry := range entries {
		for _, port := range entry.Provides {
			if port == productionModelPort {
				candidate := entry.Activation
				if !isCompiledModelProvider(candidate) {
					return moduleapi.ActivatedModuleRef{}, errors.New(
						"composition: current model provider is not in the compiled local trust set",
					)
				}
				if !found {
					provider = candidate
					found = true
				} else if !sameProviderArtifactAdapter(provider, candidate) {
					return moduleapi.ActivatedModuleRef{}, errors.New(
						"composition: model.generate/v1 providers from different exact artifacts or adapters require an explicit multi-model composition",
					)
				}
				break
			}
		}
	}
	if !found {
		return moduleapi.ActivatedModuleRef{}, fmt.Errorf(
			"composition: catalog must contain a model.generate/v1 provider",
		)
	}
	return provider, nil
}

func newProductionAdapterRegistry(
	artifactRoot string,
	entries []controlcontract.CatalogEntry,
) (*exactadapter.Registry, error) {
	return newProductionAdapterRegistryWithRuntimeAndExtras(
		artifactRoot,
		entries,
		nil,
	)
}

// newProductionAdapterRegistryWithExtras retains the exact default Registry
// construction while allowing an explicit composition path to add already
// verified eager adapters. The default path supplies no extras and therefore
// performs exactly the same artifact reads as before.
func newProductionAdapterRegistryWithExtras(
	artifactRoot string,
	entries []controlcontract.CatalogEntry,
	extras ...exactadapter.Registration,
) (*exactadapter.Registry, error) {
	return newProductionAdapterRegistryWithRuntimeAndExtras(
		artifactRoot,
		entries,
		nil,
		extras...,
	)
}

func newProductionAdapterRegistryWithRuntime(
	artifactRoot string,
	entries []controlcontract.CatalogEntry,
	deepSeek *productionDeepSeekRuntimeConfig,
) (*exactadapter.Registry, error) {
	return newProductionAdapterRegistryWithRemoteRuntimeAndExtras(
		artifactRoot,
		entries,
		deepSeek,
		nil,
	)
}

func newProductionAdapterRegistryWithRuntimeAndExtras(
	artifactRoot string,
	entries []controlcontract.CatalogEntry,
	deepSeek *productionDeepSeekRuntimeConfig,
	extras ...exactadapter.Registration,
) (*exactadapter.Registry, error) {
	return newProductionAdapterRegistryWithRemoteRuntimeAndExtras(
		artifactRoot,
		entries,
		deepSeek,
		nil,
		extras...,
	)
}

func newProductionAdapterRegistryWithRemoteRuntimeAndExtras(
	artifactRoot string,
	entries []controlcontract.CatalogEntry,
	deepSeek *productionDeepSeekRuntimeConfig,
	remoteAction *productionRemoteActionRuntimeConfig,
	extras ...exactadapter.Registration,
) (*exactadapter.Registry, error) {
	return newProductionAdapterRegistryWithActionRuntimesAndExtras(
		artifactRoot,
		entries,
		deepSeek,
		remoteAction,
		nil,
		extras...,
	)
}

func newProductionAdapterRegistryWithActionRuntimesAndExtras(
	artifactRoot string,
	entries []controlcontract.CatalogEntry,
	deepSeek *productionDeepSeekRuntimeConfig,
	remoteAction *productionRemoteActionRuntimeConfig,
	wasmAction *productionWASMActionRuntimeConfig,
	extras ...exactadapter.Registration,
) (*exactadapter.Registry, error) {
	remoteProviders, err := exactRemoteActionCatalogProviders(entries)
	if err != nil {
		return nil, err
	}
	wasmProviders, err := exactWASMActionCatalogProviders(entries)
	if err != nil {
		return nil, err
	}
	wasmAction, err = validateProductionWASMActionRuntimeConfigV1(
		wasmAction,
		wasmProviders,
	)
	if err != nil {
		return nil, err
	}
	modelProvider, err := exactLocalModelProvider(entries)
	if err != nil {
		return nil, err
	}
	modelDirectory := filepath.Join(
		artifactRoot,
		modelProvider.ArtifactDigest,
	)
	var modelInvoker modulehost.ModuleInvoker
	switch modelProvider.ModuleID {
	case localEchoModuleID:
		if deepSeek != nil {
			return nil, errors.New(
				"composition: DeepSeek runtime configuration was supplied but the current Catalog selects Echo",
			)
		}
		// Preserve the Echo artifact-read and construction path.
		digest, _, err := inspectArtifact(modelDirectory)
		if err != nil {
			return nil, err
		}
		if digest != modelProvider.ArtifactDigest {
			return nil, errors.New(
				"composition: installed model artifact digest mismatch",
			)
		}
		modelInvoker, err = exactadapter.NewDeterministicEcho(modelProvider)
		if err != nil {
			return nil, err
		}
	case localDeepSeekModuleID:
		if deepSeek == nil {
			return nil, errors.New(
				"composition: current Catalog selects DeepSeek but no explicit runtime configuration was supplied",
			)
		}
		if err := validateDeepSeekArtifact(
			modelDirectory,
			localDeepSeekSize,
			modelProvider,
		); err != nil {
			return nil, err
		}
		modelInvoker, err = deepseekmodel.New(deepseekmodel.Options{
			Provider: modelProvider,
			AllowedModelBuildIDs: map[string]string{
				deepseekmodel.ModelV4Flash: localDeepSeekFlashBuild,
				deepseekmodel.ModelV4Pro:   localDeepSeekProBuild,
			},
			APIKeyResolver: deepSeek.APIKeyResolver,
			HTTPClient:     deepSeek.HTTPClient,
		})
		if err != nil {
			return nil, fmt.Errorf(
				"composition: construct exact DeepSeek adapter: %w",
				err,
			)
		}
	default:
		return nil, errors.New(
			"composition: current model provider is not in the compiled local trust set",
		)
	}
	registrations := []exactadapter.Registration{{
		ArtifactDigest:  modelProvider.ArtifactDigest,
		AdapterIdentity: modelProvider.AdapterIdentity,
		Invoker:         modelInvoker,
	}}
	registrations = append(registrations, extras...)
	return exactadapter.NewRegistryWithLoader(
		newProductionModuleLoaderWithActionRuntimes(
			artifactRoot,
			remoteAction,
			remoteProviders,
			wasmAction,
			wasmProviders,
		),
		registrations...,
	)
}

func validateDeepSeekArtifact(
	artifactDirectory string,
	expectedSize uint64,
	expectedProvider moduleapi.ActivatedModuleRef,
) error {
	if !isCompiledModelProvider(expectedProvider) ||
		expectedProvider.ModuleID != localDeepSeekModuleID {
		return errors.New(
			"composition: DeepSeek provider is not in the compiled local trust set",
		)
	}
	if expectedSize != localDeepSeekSize {
		return errors.New(
			"composition: DeepSeek artifact size is not the compiled package lock",
		)
	}
	digest, size, err := inspectArtifact(artifactDirectory)
	if err != nil {
		return err
	}
	if digest != localDeepSeekDigest ||
		digest != expectedProvider.ArtifactDigest ||
		size != localDeepSeekSize {
		return errors.New(
			"composition: installed DeepSeek artifact digest or size mismatch",
		)
	}
	manifestCanonical, files, err := readArtifact(artifactDirectory)
	if err != nil {
		return err
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		return fmt.Errorf(
			"composition: restore DeepSeek manifest: %w",
			err,
		)
	}
	if manifest.APIVersion != moduleapi.ModuleManifestAPIVersionV1 ||
		manifest.ID != localDeepSeekModuleID ||
		manifest.Version != localDeepSeekVersion ||
		manifest.Runtime.Mode != moduleapi.RuntimeModeRequestTrustedInProcess ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolGoInProcessV1 ||
		manifest.Runtime.Entrypoint != localDeepSeekEntrypoint ||
		len(manifest.Provides) != 1 ||
		manifest.Provides[0] != productionModelPort ||
		len(manifest.Requires) != 0 ||
		len(manifest.RequestedPermissions) != 0 ||
		!bytes.Equal(
			manifest.ConfigSchema,
			[]byte(`{"path":"`+localDeepSeekSchemaPath+`"}`),
		) {
		return errors.New(
			"composition: DeepSeek artifact manifest does not close the exact provider, Port, runtime, entrypoint, and config schema",
		)
	}
	var schemaCanonical []byte
	for _, file := range files {
		if file.Path == localDeepSeekSchemaPath {
			schemaCanonical = file.Content
			break
		}
	}
	if schemaCanonical == nil {
		return errors.New(
			"composition: DeepSeek artifact config schema is missing",
		)
	}
	schemaDigest := fmt.Sprintf("%x", sha256.Sum256(schemaCanonical))
	if schemaDigest != localDeepSeekSchemaDigest {
		return errors.New(
			"composition: DeepSeek artifact config schema does not match the compiled schema",
		)
	}
	return nil
}

func exactRemoteActionCatalogProviders(
	entries []controlcontract.CatalogEntry,
) (map[string]moduleapi.ActivatedModuleRef, error) {
	providers := make(map[string]moduleapi.ActivatedModuleRef)
	for index, entry := range entries {
		provider := entry.Activation
		isRemoteClass := provider.ExecutionClass == moduleapi.ExecutionRemote
		isRemoteAdapter := provider.AdapterIdentity == remoteactionhttp.AdapterIdentityV1
		if !isRemoteClass && !isRemoteAdapter {
			continue
		}
		if err := provider.Validate(); err != nil {
			return nil, fmt.Errorf(
				"composition: REMOTE Action Catalog entry %d: %w",
				index,
				err,
			)
		}
		if !isRemoteClass || !isRemoteAdapter || len(entry.Provides) != 1 ||
			entry.Provides[0] != productionActionPort {
			return nil, fmt.Errorf(
				"composition: REMOTE Catalog entry %d is not the exact freeagent-action-http/v1 action.provider/v1 shape",
				index,
			)
		}
		key := provider.ArtifactDigest + "\x00" + provider.AdapterIdentity
		if previous, found := providers[key]; found {
			if !sameProviderArtifactAdapter(previous, provider) {
				return nil, errors.New(
					"composition: one REMOTE Action adapter key identifies different providers",
				)
			}
			continue
		}
		providers[key] = provider
	}
	return providers, nil
}

func exactWASMActionCatalogProviders(
	entries []controlcontract.CatalogEntry,
) (map[string]moduleapi.ActivatedModuleRef, error) {
	providers := make(map[string]moduleapi.ActivatedModuleRef)
	for index, entry := range entries {
		provider := entry.Activation
		isWASMClass := provider.ExecutionClass == moduleapi.ExecutionWASM
		isWASMAdapter := provider.AdapterIdentity == wasmaction.AdapterIdentityV1
		if !isWASMClass && !isWASMAdapter {
			continue
		}
		if err := provider.Validate(); err != nil {
			return nil, fmt.Errorf(
				"composition: WASM Action Catalog entry %d: %w",
				index,
				err,
			)
		}
		if !isWASMClass || !isWASMAdapter || len(entry.Provides) != 1 ||
			entry.Provides[0] != productionActionPort {
			return nil, fmt.Errorf(
				"composition: WASM Catalog entry %d is not the exact freeagent-action-wasm/v1 action.provider/v1 shape",
				index,
			)
		}
		key := provider.ArtifactDigest + "\x00" + provider.AdapterIdentity
		if previous, found := providers[key]; found {
			if !sameProviderArtifactAdapter(previous, provider) {
				return nil, errors.New(
					"composition: one WASM Action adapter key identifies different providers",
				)
			}
			continue
		}
		providers[key] = provider
	}
	return providers, nil
}

func validateProductionWASMActionRuntimeConfigV1(
	config *productionWASMActionRuntimeConfig,
	providers map[string]moduleapi.ActivatedModuleRef,
) (*productionWASMActionRuntimeConfig, error) {
	if config == nil {
		return nil, nil
	}
	if !config.Enabled {
		if len(config.AllowedArtifactDigests) != 0 {
			return nil, errors.New(
				"composition: disabled WASM Action runtime cannot carry an artifact allowlist",
			)
		}
		return nil, nil
	}
	if len(config.AllowedArtifactDigests) == 0 {
		return nil, errors.New(
			"composition: enabled WASM Action runtime requires an exact artifact allowlist",
		)
	}

	digests := make([]string, 0, len(config.AllowedArtifactDigests))
	seen := make(map[string]struct{}, len(config.AllowedArtifactDigests))
	for _, digest := range config.AllowedArtifactDigests {
		if !moduleapi.ValidSHA256(digest) {
			return nil, errors.New(
				"composition: WASM Action runtime allowlist contains a non-canonical artifact digest",
			)
		}
		if _, duplicate := seen[digest]; duplicate {
			return nil, errors.New(
				"composition: WASM Action runtime allowlist contains a duplicate artifact digest",
			)
		}
		if _, selected := providers[digest+"\x00"+wasmaction.AdapterIdentityV1]; !selected {
			return nil, errors.New(
				"composition: WASM Action runtime allowlist contains a non-Catalog artifact digest",
			)
		}
		seen[digest] = struct{}{}
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	return &productionWASMActionRuntimeConfig{
		Enabled:                true,
		AllowedArtifactDigests: digests,
	}, nil
}

func newProductionModuleLoader(artifactRoot string) exactadapter.Loader {
	return newProductionModuleLoaderWithRemote(artifactRoot, nil, nil)
}

func newProductionModuleLoaderWithRemote(
	artifactRoot string,
	remoteAction *productionRemoteActionRuntimeConfig,
	remoteProviders map[string]moduleapi.ActivatedModuleRef,
) exactadapter.Loader {
	return newProductionModuleLoaderWithActionRuntimes(
		artifactRoot,
		remoteAction,
		remoteProviders,
		nil,
		nil,
	)
}

func newProductionModuleLoaderWithActionRuntimes(
	artifactRoot string,
	remoteAction *productionRemoteActionRuntimeConfig,
	remoteProviders map[string]moduleapi.ActivatedModuleRef,
	wasmAction *productionWASMActionRuntimeConfig,
	wasmProviders map[string]moduleapi.ActivatedModuleRef,
) exactadapter.Loader {
	var secretResolver remoteactionhttp.SecretResolver
	if remoteAction != nil {
		// Capture only the narrow late-bound SecretResolver capability. In
		// particular, no endpoint, SecretRef, client or transport enters this
		// artifact-keyed Loader or the Registry cache.
		secretResolver = remoteAction.SecretResolver
	}
	remoteProviderCopy := make(
		map[string]moduleapi.ActivatedModuleRef,
		len(remoteProviders),
	)
	for key, provider := range remoteProviders {
		remoteProviderCopy[key] = provider
	}
	wasmEnabled := wasmAction != nil && wasmAction.Enabled
	wasmAllowedArtifacts := make(map[string]struct{})
	if wasmEnabled {
		wasmAllowedArtifacts = make(
			map[string]struct{},
			len(wasmAction.AllowedArtifactDigests),
		)
		for _, digest := range wasmAction.AllowedArtifactDigests {
			wasmAllowedArtifacts[digest] = struct{}{}
		}
	}
	wasmProviderCopy := make(
		map[string]moduleapi.ActivatedModuleRef,
		len(wasmProviders),
	)
	for key, provider := range wasmProviders {
		wasmProviderCopy[key] = provider
	}
	return func(
		ctx context.Context,
		artifactDigest string,
		adapterIdentity string,
	) (modulehost.ModuleInvoker, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch adapterIdentity {
		case exactadapter.DocumentInsightAdapterIdentityV1:
			return loadDocumentInsightInvokerFromArtifact(
				ctx,
				artifactDigest,
				adapterIdentity,
				filepath.Join(artifactRoot, artifactDigest),
				0,
				nil,
			)
		case localKnowledgeAdapterID:
			return loadKnowledgeInvokerFromArtifact(
				ctx,
				artifactDigest,
				adapterIdentity,
				filepath.Join(artifactRoot, artifactDigest),
				0,
				nil,
			)
		case localMemoryAdapterID:
			return loadMemoryInvokerFromArtifact(
				ctx,
				artifactDigest,
				adapterIdentity,
				filepath.Join(artifactRoot, artifactDigest),
				0,
				nil,
			)
		case localTextStatsAdapterID:
			return loadTextStatsInvokerFromArtifact(
				ctx,
				artifactDigest,
				adapterIdentity,
				filepath.Join(artifactRoot, artifactDigest),
				0,
				nil,
			)
		case mcpstdio.AdapterIdentityV1:
			return loadMCPInvokerFromArtifact(
				ctx,
				artifactDigest,
				adapterIdentity,
				filepath.Join(artifactRoot, artifactDigest),
				0,
				nil,
			)
		case remoteactionhttp.AdapterIdentityV1:
			provider, selected := remoteProviderCopy[artifactDigest+"\x00"+adapterIdentity]
			if !selected || !remoteActionSecretResolverAvailable(secretResolver) {
				return nil, fmt.Errorf(
					"%w: REMOTE Action adapter is not explicitly enabled for the exact Catalog artifact",
					exactadapter.ErrAdapterNotFound,
				)
			}
			artifactDirectory := filepath.Join(artifactRoot, artifactDigest)
			observedDigest, observedSize, err := inspectArtifactContext(
				ctx,
				artifactDirectory,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"composition: inspect REMOTE Action artifact: %w",
					err,
				)
			}
			if observedDigest != artifactDigest || observedSize == 0 {
				return nil, errors.New(
					"composition: REMOTE Action artifact digest or size mismatch",
				)
			}
			return remoteactionhttp.NewFromArtifact(
				ctx,
				provider,
				artifactDirectory,
				observedSize,
				secretResolver,
			)
		case wasmaction.AdapterIdentityV1:
			provider, selected := wasmProviderCopy[artifactDigest+"\x00"+adapterIdentity]
			_, allowed := wasmAllowedArtifacts[artifactDigest]
			if !selected || !wasmEnabled || !allowed {
				return nil, fmt.Errorf(
					"%w: WASM Action adapter is not explicitly enabled for the exact Catalog artifact",
					exactadapter.ErrAdapterNotFound,
				)
			}
			artifactDirectory := filepath.Join(artifactRoot, artifactDigest)
			observedDigest, observedSize, err := inspectArtifactContext(
				ctx,
				artifactDirectory,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"composition: inspect WASM Action artifact: %w",
					err,
				)
			}
			if observedDigest != artifactDigest || observedSize == 0 {
				return nil, errors.New(
					"composition: WASM Action artifact digest or size mismatch",
				)
			}
			return wasmaction.NewFromArtifact(
				ctx,
				provider,
				artifactDirectory,
				observedSize,
			)
		default:
			return nil, fmt.Errorf(
				"%w: adapter %q is not in the compiled lazy adapter set",
				exactadapter.ErrAdapterNotFound,
				adapterIdentity,
			)
		}
	}
}

func newMCPRegistrationFromArtifact(
	provider moduleapi.ActivatedModuleRef,
	artifactDirectory string,
	expectedSize uint64,
) (exactadapter.Registration, error) {
	if err := provider.Validate(); err != nil {
		return exactadapter.Registration{}, fmt.Errorf(
			"invalid MCP provider: %w",
			err,
		)
	}
	if provider.ExecutionClass != moduleapi.ExecutionLocalProcess ||
		provider.AdapterIdentity != mcpstdio.AdapterIdentityV1 {
		return exactadapter.Registration{}, errors.New(
			"MCP provider is not in the compiled local adapter set",
		)
	}
	adapter, err := loadMCPInvokerFromArtifact(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
		artifactDirectory,
		expectedSize,
		&provider,
	)
	if err != nil {
		return exactadapter.Registration{}, err
	}
	return exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         adapter,
	}, nil
}

func loadMCPInvokerFromArtifact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
	artifactDirectory string,
	expectedSize uint64,
	expectedProvider *moduleapi.ActivatedModuleRef,
) (modulehost.ModuleInvoker, error) {
	if !moduleapi.ValidSHA256(artifactDigest) ||
		adapterIdentity != mcpstdio.AdapterIdentityV1 {
		return nil, errors.New(
			"MCP artifact identity is not in the compiled local adapter set",
		)
	}
	if err := moduleapi.VerifyArtifactDirectoryDigestAndSizeContext(
		ctx,
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
		artifactDigest,
		expectedSize,
	); err != nil {
		return nil, fmt.Errorf("composition: verify MCP artifact: %w", err)
	}
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		artifactDirectory,
	)
	if err != nil {
		return nil, fmt.Errorf("composition: restore MCP manifest: %w", err)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		return nil, fmt.Errorf("composition: restore MCP manifest: %w", err)
	}
	if manifest.Runtime.Mode != moduleapi.RuntimeModeRequestLocalProcess ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolMCPStdio20251125 ||
		len(manifest.Provides) != 1 ||
		manifest.Provides[0] != productionActionPort ||
		len(manifest.Requires) != 0 ||
		len(manifest.RequestedPermissions) != 0 {
		return nil, errors.New(
			"composition: MCP artifact manifest does not close LOCAL_PROCESS and action.provider/v1",
		)
	}
	descriptorCanonical, err := moduleapi.ReadArtifactOrdinaryFileFromDirectoryContext(
		ctx,
		artifactDirectory,
		manifest.Runtime.Entrypoint,
		mcpstdio.MaxHostDescriptorBytesV1,
	)
	if err != nil {
		return nil, fmt.Errorf("composition: read MCP artifact descriptor: %w", err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           manifest.ID,
		Version:            manifest.Version,
		ArtifactDigest:     artifactDigest,
		InstanceID:         "artifact-adapter-template",
		ExecutionClass:     moduleapi.ExecutionLocalProcess,
		AdapterIdentity:    adapterIdentity,
		ActivationRevision: 1,
	}
	if expectedProvider != nil &&
		(expectedProvider.ModuleID != provider.ModuleID ||
			expectedProvider.Version != provider.Version ||
			expectedProvider.ArtifactDigest != provider.ArtifactDigest ||
			expectedProvider.ExecutionClass != provider.ExecutionClass ||
			expectedProvider.AdapterIdentity != provider.AdapterIdentity) {
		return nil, errors.New(
			"composition: MCP artifact manifest does not match the expected provider",
		)
	}
	return mcpstdio.NewContext(
		ctx,
		provider,
		artifactDirectory,
		manifest.Runtime.Entrypoint,
		descriptorCanonical,
	)
}

func newKnowledgeRegistrationFromArtifact(
	provider moduleapi.ActivatedModuleRef,
	artifactDirectory string,
	expectedSize uint64,
) (exactadapter.Registration, error) {
	if err := provider.Validate(); err != nil {
		return exactadapter.Registration{}, fmt.Errorf(
			"invalid knowledge provider: %w",
			err,
		)
	}
	if provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		provider.AdapterIdentity != localKnowledgeAdapterID {
		return exactadapter.Registration{}, errors.New(
			"knowledge provider is not in the compiled local adapter set",
		)
	}
	knowledge, err := loadKnowledgeInvokerFromArtifact(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
		artifactDirectory,
		expectedSize,
		&provider,
	)
	if err != nil {
		return exactadapter.Registration{}, err
	}
	return exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         knowledge,
	}, nil
}

func loadKnowledgeInvokerFromArtifact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
	artifactDirectory string,
	expectedSize uint64,
	expectedProvider *moduleapi.ActivatedModuleRef,
) (modulehost.ModuleInvoker, error) {
	if !moduleapi.ValidSHA256(artifactDigest) ||
		adapterIdentity != localKnowledgeAdapterID {
		return nil, errors.New(
			"knowledge artifact identity is not in the compiled local adapter set",
		)
	}
	manifestCanonical, files, err := readArtifactContext(ctx, artifactDirectory)
	if err != nil {
		return nil, err
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		return nil, fmt.Errorf(
			"compute knowledge artifact digest: %w",
			err,
		)
	}
	size := uint64(len(manifestCanonical))
	for _, file := range files {
		if uint64(len(file.Content)) > math.MaxUint64-size {
			return nil, errors.New(
				"composition: knowledge artifact size overflow",
			)
		}
		size += uint64(len(file.Content))
	}
	if digest != artifactDigest ||
		(expectedSize != 0 && size != expectedSize) {
		return nil, errors.New(
			"composition: knowledge artifact digest or size mismatch",
		)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		return nil, fmt.Errorf(
			"composition: restore knowledge manifest: %w",
			err,
		)
	}
	if _, err := classifyExactKnowledgeManifestV1(manifest); err != nil {
		return nil, fmt.Errorf(
			"composition: knowledge artifact manifest does not close an exact supported shape: %w",
			err,
		)
	}
	entrypoint := manifest.Runtime.Entrypoint
	var sourceCanonical []byte
	for _, file := range files {
		if file.Path == entrypoint {
			sourceCanonical = bytes.Clone(file.Content)
			break
		}
	}
	if sourceCanonical == nil {
		return nil, errors.New(
			"composition: knowledge artifact entrypoint is missing",
		)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           manifest.ID,
		Version:            manifest.Version,
		ArtifactDigest:     artifactDigest,
		InstanceID:         "artifact-adapter-template",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    adapterIdentity,
		ActivationRevision: 1,
	}
	if expectedProvider != nil &&
		(expectedProvider.ModuleID != provider.ModuleID ||
			expectedProvider.Version != provider.Version ||
			expectedProvider.ArtifactDigest != provider.ArtifactDigest ||
			expectedProvider.ExecutionClass != provider.ExecutionClass ||
			expectedProvider.AdapterIdentity != provider.AdapterIdentity) {
		return nil, errors.New(
			"composition: knowledge artifact manifest does not match the expected provider",
		)
	}
	knowledge, err := exactadapter.NewDeterministicKnowledge(
		provider,
		sourceCanonical,
	)
	if err != nil {
		return nil, err
	}
	return knowledge, nil
}

func newMemoryRegistrationFromArtifact(
	provider moduleapi.ActivatedModuleRef,
	artifactDirectory string,
	expectedSize uint64,
) (exactadapter.Registration, error) {
	if err := provider.Validate(); err != nil {
		return exactadapter.Registration{}, fmt.Errorf(
			"invalid Memory provider: %w",
			err,
		)
	}
	if provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		provider.AdapterIdentity != localMemoryAdapterID {
		return exactadapter.Registration{}, errors.New(
			"Memory provider is not in the compiled local adapter set",
		)
	}
	memory, err := loadMemoryInvokerFromArtifact(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
		artifactDirectory,
		expectedSize,
		&provider,
	)
	if err != nil {
		return exactadapter.Registration{}, err
	}
	return exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         memory,
	}, nil
}

func loadMemoryInvokerFromArtifact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
	artifactDirectory string,
	expectedSize uint64,
	expectedProvider *moduleapi.ActivatedModuleRef,
) (modulehost.ModuleInvoker, error) {
	if !moduleapi.ValidSHA256(artifactDigest) ||
		adapterIdentity != localMemoryAdapterID {
		return nil, errors.New(
			"Memory artifact identity is not in the compiled local adapter set",
		)
	}
	manifestCanonical, files, err := readArtifactContext(ctx, artifactDirectory)
	if err != nil {
		return nil, err
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		return nil, fmt.Errorf("compute Memory artifact digest: %w", err)
	}
	size := uint64(len(manifestCanonical))
	for _, file := range files {
		if uint64(len(file.Content)) > math.MaxUint64-size {
			return nil, errors.New("composition: Memory artifact size overflow")
		}
		size += uint64(len(file.Content))
	}
	if digest != artifactDigest ||
		(expectedSize != 0 && size != expectedSize) {
		return nil, errors.New(
			"composition: Memory artifact digest or size mismatch",
		)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		return nil, fmt.Errorf("composition: restore Memory manifest: %w", err)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           manifest.ID,
		Version:            manifest.Version,
		ArtifactDigest:     artifactDigest,
		InstanceID:         "artifact-adapter-template",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    adapterIdentity,
		ActivationRevision: 1,
	}
	if expectedProvider != nil &&
		(expectedProvider.ModuleID != provider.ModuleID ||
			expectedProvider.Version != provider.Version ||
			expectedProvider.ArtifactDigest != provider.ArtifactDigest ||
			expectedProvider.ExecutionClass != provider.ExecutionClass ||
			expectedProvider.AdapterIdentity != provider.AdapterIdentity) {
		return nil, errors.New(
			"composition: Memory artifact manifest does not match the expected provider",
		)
	}
	memory, err := exactadapter.NewDeterministicMemoryFromArtifact(
		provider,
		manifestCanonical,
		files,
	)
	if err != nil {
		return nil, err
	}
	return memory, nil
}

func newTextStatsRegistrationFromArtifact(
	provider moduleapi.ActivatedModuleRef,
	artifactDirectory string,
	expectedSize uint64,
) (exactadapter.Registration, error) {
	if err := provider.Validate(); err != nil {
		return exactadapter.Registration{}, fmt.Errorf(
			"invalid text.stats provider: %w",
			err,
		)
	}
	if provider.ModuleID != localTextStatsModuleID ||
		provider.Version != localTextStatsVersion ||
		provider.ArtifactDigest != localTextStatsDigest ||
		provider.ExecutionClass != moduleapi.ExecutionTrustedInProcess ||
		provider.AdapterIdentity != localTextStatsAdapterID {
		return exactadapter.Registration{}, errors.New(
			"text.stats provider is not in the compiled local adapter set",
		)
	}
	action, err := loadTextStatsInvokerFromArtifact(
		context.Background(),
		provider.ArtifactDigest,
		provider.AdapterIdentity,
		artifactDirectory,
		expectedSize,
		&provider,
	)
	if err != nil {
		return exactadapter.Registration{}, err
	}
	return exactadapter.Registration{
		ArtifactDigest:  provider.ArtifactDigest,
		AdapterIdentity: provider.AdapterIdentity,
		Invoker:         action,
	}, nil
}

func loadTextStatsInvokerFromArtifact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
	artifactDirectory string,
	expectedSize uint64,
	expectedProvider *moduleapi.ActivatedModuleRef,
) (modulehost.ModuleInvoker, error) {
	verified, err := validateTextStatsArtifactFromArtifact(
		ctx,
		artifactDigest,
		adapterIdentity,
		artifactDirectory,
		expectedSize,
		expectedProvider,
	)
	if err != nil {
		return nil, err
	}
	action, err := exactadapter.NewTextStatsAction(verified.Provider)
	if err != nil {
		return nil, err
	}
	return action, nil
}

type validatedTextStatsArtifactV1 struct {
	ManifestCanonical []byte
	Provider          moduleapi.ActivatedModuleRef
}

// validateTextStatsArtifactFromArtifact proves the compiled text.stats
// artifact and provider identity without constructing or invoking the
// Provider. Module Dry-run uses this inert boundary; Apply may subsequently
// call loadTextStatsInvokerFromArtifact to construct the compiled adapter.
func validateTextStatsArtifactFromArtifact(
	ctx context.Context,
	artifactDigest string,
	adapterIdentity string,
	artifactDirectory string,
	expectedSize uint64,
	expectedProvider *moduleapi.ActivatedModuleRef,
) (validatedTextStatsArtifactV1, error) {
	if !moduleapi.ValidSHA256(artifactDigest) ||
		artifactDigest != localTextStatsDigest ||
		adapterIdentity != localTextStatsAdapterID {
		return validatedTextStatsArtifactV1{}, errors.New(
			"text.stats artifact identity is not in the compiled local adapter set",
		)
	}
	manifestCanonical, files, err := readArtifactContext(ctx, artifactDirectory)
	if err != nil {
		return validatedTextStatsArtifactV1{}, err
	}
	digest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		return validatedTextStatsArtifactV1{}, fmt.Errorf("compute text.stats artifact digest: %w", err)
	}
	size := uint64(len(manifestCanonical))
	for _, file := range files {
		if uint64(len(file.Content)) > math.MaxUint64-size {
			return validatedTextStatsArtifactV1{}, errors.New("composition: text.stats artifact size overflow")
		}
		size += uint64(len(file.Content))
	}
	if digest != artifactDigest ||
		(expectedSize != 0 && size != expectedSize) {
		return validatedTextStatsArtifactV1{}, errors.New(
			"composition: text.stats artifact digest or size mismatch",
		)
	}
	manifest, _, err := moduleapi.ParseModuleManifestV1(manifestCanonical)
	if err != nil {
		return validatedTextStatsArtifactV1{}, fmt.Errorf("composition: restore text.stats manifest: %w", err)
	}
	if manifest.ID != localTextStatsModuleID ||
		manifest.Version != localTextStatsVersion ||
		manifest.Runtime.Mode != moduleapi.RuntimeModeRequestTrustedInProcess ||
		manifest.Runtime.Protocol != moduleapi.RuntimeProtocolGoInProcessV1 ||
		manifest.Runtime.Entrypoint != localTextStatsEntrypoint ||
		len(manifest.Provides) != 1 ||
		manifest.Provides[0] != productionActionPort ||
		len(manifest.Requires) != 0 ||
		len(manifest.RequestedPermissions) != 0 {
		return validatedTextStatsArtifactV1{}, errors.New(
			"composition: text.stats artifact manifest does not close the exact provider and Port",
		)
	}
	provider := moduleapi.ActivatedModuleRef{
		ModuleID:           manifest.ID,
		Version:            manifest.Version,
		ArtifactDigest:     artifactDigest,
		InstanceID:         "artifact-adapter-template",
		ExecutionClass:     moduleapi.ExecutionTrustedInProcess,
		AdapterIdentity:    adapterIdentity,
		ActivationRevision: 1,
	}
	if expectedProvider != nil &&
		(expectedProvider.ModuleID != provider.ModuleID ||
			expectedProvider.Version != provider.Version ||
			expectedProvider.ArtifactDigest != provider.ArtifactDigest ||
			expectedProvider.ExecutionClass != provider.ExecutionClass ||
			expectedProvider.AdapterIdentity != provider.AdapterIdentity) {
		return validatedTextStatsArtifactV1{}, errors.New(
			"composition: text.stats artifact manifest does not match the expected provider",
		)
	}
	return validatedTextStatsArtifactV1{
		ManifestCanonical: bytes.Clone(manifestCanonical),
		Provider:          provider,
	}, nil
}

func stageArtifact(
	stagingRoot string,
	assertion bootstrapseed.ModuleAssertion,
) error {
	return stageVerifiedArtifact(
		context.Background(),
		assertion.ArtifactDirectory,
		filepath.Join(stagingRoot, assertion.ArtifactDigest),
		assertion.ArtifactDigest,
		assertion.ArtifactSizeBytes,
	)
}

// stageVerifiedArtifact copies one exact unpacked artifact into a new target
// directory. The source is completely verified before copying, the captured
// copy is checked against the same exact identity, and the target is completely
// verified again before success. Any failure after target creation removes the
// partial target; callers therefore never observe a successful partial stage.
func stageVerifiedArtifact(
	ctx context.Context,
	sourceDirectory string,
	targetDirectory string,
	expectedDigest string,
	expectedSize uint64,
) (returnErr error) {
	if ctx == nil {
		return errors.New("composition: artifact staging context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("composition: stage artifact: %w", err)
	}
	if strings.TrimSpace(sourceDirectory) == "" {
		return errors.New("composition: prepared artifact directory is absent")
	}
	if strings.TrimSpace(targetDirectory) == "" {
		return errors.New("composition: artifact staging target is absent")
	}
	if !moduleapi.ValidSHA256(expectedDigest) || expectedSize == 0 {
		return errors.New("composition: expected artifact identity is invalid")
	}

	// This first complete pass rejects a stale assertion before any target path
	// is created. A second complete read below supplies the bytes to copy and is
	// independently compared so source drift cannot become staged authority.
	sourceDigest, sourceSize, err := inspectArtifactContext(ctx, sourceDirectory)
	if err != nil {
		return err
	}
	if sourceDigest != expectedDigest || sourceSize != expectedSize {
		return errors.New("composition: prepared artifact changed before staging")
	}
	manifest, files, err := readArtifactContext(ctx, sourceDirectory)
	if err != nil {
		return err
	}
	capturedDigest, capturedSize, err := artifactIdentity(manifest, files)
	if err != nil {
		return err
	}
	if capturedDigest != expectedDigest || capturedSize != expectedSize {
		return errors.New("composition: prepared artifact drifted while staging")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("composition: stage artifact: %w", err)
	}

	if err := os.Mkdir(targetDirectory, 0o700); err != nil {
		return fmt.Errorf("composition: create artifact directory: %w", err)
	}
	targetCreated := true
	defer func() {
		if returnErr == nil || !targetCreated {
			return
		}
		cleanupErr := os.RemoveAll(targetDirectory)
		cleanupSyncErr := syncInitDirectory(filepath.Dir(targetDirectory))
		if cleanupErr != nil || cleanupSyncErr != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf(
					"composition: remove and sync partial artifact stage: %w",
					errors.Join(cleanupErr, cleanupSyncErr),
				),
			)
		}
	}()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("composition: stage artifact: %w", err)
	}
	if err := writeExclusiveFileContext(
		ctx,
		filepath.Join(targetDirectory, moduleapi.ArtifactManifestPath),
		manifest,
		0o600,
	); err != nil {
		return err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("composition: stage artifact: %w", err)
		}
		path := filepath.Join(
			targetDirectory,
			filepath.FromSlash(file.Path),
		)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("composition: create artifact subdirectory: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("composition: stage artifact: %w", err)
		}
		mode, err := stagedArtifactFileMode(sourceDirectory, file.Path)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("composition: stage artifact: %w", err)
		}
		if err := writeExclusiveFileContext(ctx, path, file.Content, mode); err != nil {
			return err
		}
	}
	if err := syncInitTreeDirectoriesContext(ctx, targetDirectory); err != nil {
		return fmt.Errorf("composition: sync staged artifact directories: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("composition: stage artifact: %w", err)
	}
	if err := syncInitDirectory(filepath.Dir(targetDirectory)); err != nil {
		return fmt.Errorf("composition: sync artifact staging parent: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("composition: stage artifact: %w", err)
	}

	stagedDigest, stagedSize, err := inspectArtifactContext(ctx, targetDirectory)
	if err != nil {
		return err
	}
	if stagedDigest != expectedDigest || stagedSize != expectedSize {
		return errors.New("composition: staged artifact verification failed")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("composition: stage artifact: %w", err)
	}
	targetCreated = false
	return nil
}

func readArtifact(root string) ([]byte, []moduleapi.ArtifactFile, error) {
	return readArtifactContext(context.Background(), root)
}

func readArtifactContext(
	ctx context.Context,
	root string,
) ([]byte, []moduleapi.ArtifactFile, error) {
	if ctx == nil {
		return nil, nil, errors.New("composition: artifact context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("composition: read artifact: %w", err)
	}
	directory, err := resolveExistingDirectory(root, "artifact directory")
	if err != nil {
		return nil, nil, err
	}
	manifestCanonical, err := moduleapi.ReadArtifactManifestV1FromDirectoryContext(
		ctx,
		directory,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("composition: read artifact manifest: %w", err)
	}
	files, err := moduleapi.ScanArtifactDirectoryContext(
		ctx,
		directory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("composition: scan artifact: %w", err)
	}
	return bytes.Clone(manifestCanonical), files, nil
}

func inspectArtifact(root string) (string, uint64, error) {
	return inspectArtifactContext(context.Background(), root)
}

func inspectArtifactContext(
	ctx context.Context,
	root string,
) (string, uint64, error) {
	manifest, files, err := readArtifactContext(ctx, root)
	if err != nil {
		return "", 0, err
	}
	return artifactIdentity(manifest, files)
}

func artifactIdentity(
	manifest []byte,
	files []moduleapi.ArtifactFile,
) (string, uint64, error) {
	digest, err := moduleapi.ComputeArtifactDigest(manifest, files)
	if err != nil {
		return "", 0, err
	}
	size := uint64(len(manifest))
	for _, file := range files {
		if uint64(len(file.Content)) > math.MaxUint64-size {
			return "", 0, errors.New("composition: artifact size overflow")
		}
		size += uint64(len(file.Content))
	}
	return digest, size, nil
}

func stagedArtifactFileMode(root, normalizedPath string) (os.FileMode, error) {
	source := filepath.Join(root, filepath.FromSlash(normalizedPath))
	info, err := os.Lstat(source)
	if err != nil {
		return 0, fmt.Errorf(
			"composition: inspect staged artifact source %q: %w",
			normalizedPath,
			err,
		)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return 0, fmt.Errorf(
			"composition: staged artifact source %q is not an ordinary file",
			normalizedPath,
		)
	}
	if info.Mode().Perm()&0o111 != 0 {
		return 0o700, nil
	}
	return 0o600, nil
}

func writeExclusiveFile(
	path string,
	content []byte,
	mode os.FileMode,
) error {
	return writeExclusiveFileContext(
		context.Background(),
		path,
		content,
		mode,
	)
}

func writeExclusiveFileContext(
	ctx context.Context,
	path string,
	content []byte,
	mode os.FileMode,
) (returnErr error) {
	if ctx == nil {
		return errors.New("composition: file write context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("composition: create artifact file: %w", err)
	}
	if mode != 0o600 && mode != 0o700 {
		return errors.New("composition: staged artifact mode is outside install policy")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("composition: create artifact file: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	const writeChunkBytes = 32 << 10
	for offset := 0; offset < len(content); {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("composition: write artifact file: %w", err)
		}
		end := offset + writeChunkBytes
		if end > len(content) {
			end = len(content)
		}
		written, err := file.Write(content[offset:end])
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		offset += written
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("composition: write artifact file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("composition: sync artifact file: %w", err)
	}
	return nil
}

func newDatabaseStagingPath(parent string) (string, error) {
	file, err := os.CreateTemp(parent, ".freeagent-current-*.sqlite")
	if err != nil {
		return "", fmt.Errorf("composition: create database staging path: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func verifyExactInitArtifactRoot(
	root string,
	assertions []bootstrapseed.ModuleAssertion,
) error {
	directory, err := resolveExistingArtifactRootV1(root)
	if err != nil {
		return err
	}
	expected := make(map[string]uint64, len(assertions))
	if len(assertions) > int(moduleartifactstore.MaxPhysicalArtifactsV1) {
		return errors.New("composition: bootstrap artifact count exceeds physical closure")
	}
	for _, assertion := range assertions {
		if previous, duplicate := expected[assertion.ArtifactDigest]; duplicate &&
			previous != assertion.ArtifactSizeBytes {
			return errors.New("composition: duplicate artifact assertion differs")
		}
		expected[assertion.ArtifactDigest] = assertion.ArtifactSizeBytes
	}
	entries, err := readBoundedInitArtifactRootEntriesV1(
		directory,
		len(expected)+1,
	)
	if err != nil {
		return fmt.Errorf("composition: read artifact root: %w", err)
	}
	artifactEntries := entries[:0]
	for _, entry := range entries {
		if moduleartifactstore.IsArtifactRootControlEntryV1(entry.Name()) {
			continue
		}
		artifactEntries = append(artifactEntries, entry)
	}
	if len(artifactEntries) != len(expected) {
		return fmt.Errorf(
			"composition: artifact root has %d entries, want %d",
			len(artifactEntries),
			len(expected),
		)
	}
	for _, entry := range artifactEntries {
		expectedSize, found := expected[entry.Name()]
		if !found || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return fmt.Errorf(
				"composition: artifact root contains unexpected entry %q",
				entry.Name(),
			)
		}
		digest, size, err := inspectArtifact(filepath.Join(directory, entry.Name()))
		if err != nil {
			return err
		}
		if digest != entry.Name() || size != expectedSize {
			return fmt.Errorf(
				"composition: artifact %s does not match the prepared seed",
				entry.Name(),
			)
		}
	}
	return nil
}

func readBoundedInitArtifactRootEntriesV1(
	directory string,
	maximum int,
) (entries []os.DirEntry, returnErr error) {
	if maximum < 0 || maximum > int(moduleartifactstore.MaxPhysicalArtifactsV1)+1 {
		return nil, errors.New("composition: artifact root entry limit is invalid")
	}
	root, err := os.Open(directory)
	if err != nil {
		return nil, fmt.Errorf("composition: open artifact root: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, root.Close()) }()
	for {
		remaining := maximum + 1 - len(entries)
		if remaining <= 0 {
			return nil, errors.New("composition: artifact root entry limit exceeded")
		}
		chunk := remaining
		if chunk > 64 {
			chunk = 64
		}
		batch, readErr := root.ReadDir(chunk)
		entries = append(entries, batch...)
		if len(entries) > maximum {
			return nil, errors.New("composition: artifact root entry limit exceeded")
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("composition: read artifact root: %w", readErr)
		}
	}
	return entries, nil
}

func syncInitTreeDirectories(root string) error {
	return syncInitTreeDirectoriesContext(context.Background(), root)
}

func syncInitTreeDirectoriesContext(ctx context.Context, root string) error {
	if ctx == nil {
		return errors.New("composition: directory sync context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("composition: sync artifact directories: %w", err)
	}
	var directories []string
	if err := filepath.WalkDir(root, func(
		path string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, path)
		}
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(directories, func(left, right int) bool {
		return len(directories[left]) > len(directories[right])
	})
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("composition: sync artifact directories: %w", err)
		}
		if err := syncInitDirectory(directory); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("composition: sync artifact directories: %w", err)
		}
	}
	return nil
}

func resolveNewTarget(path, label string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("composition: %s path is required", label)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("composition: resolve %s: %w", label, err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", fmt.Errorf("composition: resolve %s parent: %w", label, err)
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("composition: %s parent must be an existing directory", label)
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func resolveExistingDirectory(path, label string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("composition: resolve %s: %w", label, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("composition: resolve %s: %w", label, err)
	}
	if !samePath(absolute, resolved) {
		return "", fmt.Errorf("composition: %s must not traverse symlinks", label)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("composition: %s must be an existing directory", label)
	}
	return resolved, nil
}

func resolveExistingArtifactRootV1(path string) (string, error) {
	root, err := resolveExistingDirectory(path, "artifact root")
	if err != nil {
		return "", err
	}
	if _, err := moduleartifactstore.SelectArtifactRootV1(root); err != nil {
		return "", fmt.Errorf("composition: artifact root is unsafe: %w", err)
	}
	return root, nil
}

func requireAbsent(path, label string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("composition: %s target already exists", label)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("composition: inspect %s target: %w", label, err)
	}
	return nil
}

func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func pathContains(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
