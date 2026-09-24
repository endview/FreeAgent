package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/runscheduler"
)

const (
	defaultTenantID    = "default"
	defaultPrincipalID = "local-operator"
	defaultWorkspaceID = "local-chat"
	defaultAgentID     = "assistant"
	defaultProfileID   = "pure-chat"
	defaultListen      = "127.0.0.1:8080"
	backupToolVersion  = "freeagent-cli/s1"
)

func main() {
	args := os.Args[1:]
	var ctx context.Context = context.Background()
	var stop context.CancelFunc = func() {}
	if !isVersionRequest(args) {
		ctx, stop = signal.NotifyContext(
			ctx,
			os.Interrupt,
			syscall.SIGTERM,
		)
	}
	defer stop()
	if err := run(ctx, args, os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if ctx == nil {
		return errors.New("freeagent: context is nil")
	}
	if len(args) == 0 {
		return commandUsageError()
	}
	switch args[0] {
	case "--version":
		return runVersion(args[1:], stdout, currentBuildInfo())
	case "init":
		return runInit(ctx, args[1:], stdout, stderr)
	case "conversation-create":
		return runConversationCreate(ctx, args[1:], stdout, stderr)
	case "conversation-get":
		return runConversationGet(ctx, args[1:], stdout, stderr)
	case "chat":
		return runChat(ctx, args[1:], stdout, stderr)
	case "s3-eval":
		return runS3Eval(ctx, args[1:], stdout, stderr)
	case "s3-store-audit":
		return runS3StoreAudit(ctx, args[1:], stdout, stderr)
	case "s3-cell-audit":
		return runS3CellAudit(ctx, args[1:], stdout, stderr)
	case "module-verify":
		return runModuleVerify(ctx, args[1:], stdout, stderr)
	case "module-list":
		return runModuleList(ctx, args[1:], stdout, stderr)
	case "module-history":
		return runModuleHistory(ctx, args[1:], stdout, stderr)
	case "module-inspect":
		return runModuleInspect(ctx, args[1:], stdout, stderr)
	case "module-apply":
		return runModuleApply(ctx, args[1:], stdout, stderr)
	case "module-dry-run":
		return runModuleDryRun(ctx, args[1:], stdout, stderr)
	case "module-disable":
		return runModuleDisable(ctx, args[1:], stdout, stderr)
	case "module-source-register":
		return runModuleSourceRegister(ctx, args[1:], stdout, stderr)
	case "module-source-refresh":
		return runModuleSourceRefresh(ctx, args[1:], stdout, stderr)
	case "module-artifact-ingress":
		return runModuleArtifactIngress(ctx, args[1:], stdout, stderr)
	case "module-publisher-key-revoke":
		return runModulePublisherKeyRevoke(ctx, args[1:], stdout, stderr)
	case "module-upgrade-review":
		return runModuleUpgradeReview(ctx, args[1:], stdout, stderr)
	case "module-upgrade-review-server-owned":
		return runModuleUpgradeReviewServerOwned(ctx, args[1:], stdout, stderr)
	case "module-upgrade-decide-server-owned":
		return runModuleUpgradeDecisionServerOwned(ctx, args[1:], stdout, stderr)
	case "module-upgrade-decide":
		return runModuleUpgradeDecide(ctx, args[1:], stdout, stderr)
	case "module-upgrade-apply":
		return runModuleUpgradeApply(ctx, args[1:], stdout, stderr)
	case "learning-materialize":
		return runLearningMaterialize(ctx, args[1:], stdout, stderr)
	case "learning-cycle-schedule-create":
		return runLearningCycleScheduleCreate(ctx, args[1:], stdout, stderr)
	case "learning-cycle-schedule-get":
		return runLearningCycleScheduleGet(ctx, args[1:], stdout, stderr)
	case "learning-cycle-schedule-enable":
		return runLearningCycleScheduleEnable(ctx, args[1:], stdout, stderr)
	case "learning-cycle-schedule-disable":
		return runLearningCycleScheduleDisable(ctx, args[1:], stdout, stderr)
	case "learning-cycle-tick":
		return runLearningCycleTick(ctx, args[1:], stdout, stderr)
	case "learning-cycle-reconcile":
		return runLearningCycleReconcile(ctx, args[1:], stdout, stderr)
	case "learning-cycle-report":
		return runLearningCycleReport(ctx, args[1:], stdout, stderr)
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	case "backup":
		return runBackup(ctx, args[1:], stdout, stderr)
	case "backup-verify":
		return runBackupVerify(ctx, args[1:], stdout, stderr)
	case "restore":
		return runRestore(ctx, args[1:], stdout, stderr)
	case "migrate":
		return runMigrate(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("freeagent: unknown command %q; %w", args[0], commandUsageError())
	}
}

func commandUsageError() error {
	return errors.New(
		"usage: freeagent --version | freeagent <init|conversation-create|conversation-get|chat|module-verify|module-list|module-history|module-inspect|module-dry-run|module-apply|module-disable|module-source-register|module-source-refresh|module-artifact-ingress|module-publisher-key-revoke|module-upgrade-review|module-upgrade-review-server-owned|module-upgrade-decide|module-upgrade-decide-server-owned|module-upgrade-apply|learning-materialize|learning-cycle-schedule-create|learning-cycle-schedule-get|learning-cycle-schedule-enable|learning-cycle-schedule-disable|learning-cycle-tick|learning-cycle-reconcile|learning-cycle-report|s3-eval|s3-store-audit|s3-cell-audit|serve|backup|backup-verify|restore|migrate> [flags]",
	)
}

func runConversationCreate(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) (returnErr error) {
	flags := newFlagSet("conversation-create", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	conversationID := flags.String("conversation", "", "new stable Conversation identity")
	tenantID := flags.String("tenant", defaultTenantID, "tenant identity")
	principalID := flags.String("principal", defaultPrincipalID, "principal identity")
	workspaceID := flags.String("workspace", defaultWorkspaceID, "workspace identity")
	agentID := flags.String("agent", defaultAgentID, "agent identity")
	profileID := flags.String("profile", defaultProfileID, "profile identity")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*conversationID) == "" {
		return errors.New(
			"freeagent conversation-create: --db and --conversation are required",
		)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, *databasePath)
	if err != nil {
		return fmt.Errorf("freeagent conversation-create: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	result, err := store.CreateConversation(ctx, currentstore.CreateConversationInput{
		ConversationID: *conversationID,
		TenantID:       *tenantID,
		PrincipalID:    *principalID,
		WorkspaceID:    *workspaceID,
		AgentID:        *agentID,
		ProfileID:      *profileID,
	})
	if err != nil {
		return fmt.Errorf("freeagent conversation-create: %w", err)
	}
	return writeCommandJSON(stdout, newConversationCommandResult(result.Record, result.Created))
}

func runConversationGet(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) (returnErr error) {
	flags := newFlagSet("conversation-get", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	conversationID := flags.String("conversation", "", "stable Conversation identity")
	tenantID := flags.String("tenant", defaultTenantID, "tenant identity")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*conversationID) == "" {
		return errors.New(
			"freeagent conversation-get: --db and --conversation are required",
		)
	}
	store, err := currentstore.OpenExistingCurrentStore(ctx, *databasePath)
	if err != nil {
		return fmt.Errorf("freeagent conversation-get: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	record, err := store.GetConversation(ctx, *tenantID, *conversationID)
	if err != nil {
		return fmt.Errorf("freeagent conversation-get: %w", err)
	}
	return writeCommandJSON(stdout, newConversationCommandResult(record, false))
}

func runInit(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := newFlagSet("init", stderr)
	databasePath := flags.String("db", "", "new Current Store database path")
	seedPath := flags.String("seed", "", "canonical bootstrap seed path")
	artifactRoot := flags.String("artifact-root", "", "new content-addressed artifact root")
	var localMCPArtifactGrants repeatedStringFlag
	flags.Var(
		&localMCPArtifactGrants,
		"allow-local-mcp-artifact",
		"operator-approved LOCAL_PROCESS MCP artifact SHA-256 (repeatable)",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*seedPath) == "" {
		return errors.New("freeagent init: --db and --seed are required")
	}
	result, err := initializeProductionData(ctx, initInput{
		DatabasePath: *databasePath,
		SeedPath:     *seedPath,
		ArtifactRoot: resolvedArtifactRoot(*databasePath, *artifactRoot),
		LocalMCPArtifactGrants: append(
			[]string(nil),
			localMCPArtifactGrants...,
		),
	})
	if err != nil {
		return fmt.Errorf("freeagent init: %w", err)
	}
	return writeCommandJSON(stdout, result)
}

func runChat(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) (returnErr error) {
	flags := newFlagSet("chat", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "content-addressed artifact root")
	message := flags.String("message", "", "chat message")
	requestID := flags.String("request-id", "", "stable request identity")
	deadlineText := flags.String("deadline", "", "stable UTC RFC3339 deadline")
	tenantID := flags.String("tenant", defaultTenantID, "tenant identity")
	principalID := flags.String("principal", defaultPrincipalID, "principal identity")
	workspaceID := flags.String("workspace", defaultWorkspaceID, "workspace identity")
	agentID := flags.String("agent", defaultAgentID, "agent identity")
	profileID := flags.String("profile", defaultProfileID, "profile identity")
	conversationID := flags.String(
		"conversation",
		"",
		"existing Conversation identity; enables one-turn-per-Run admission",
	)
	expectedConversationRevision := flags.Uint64(
		"conversation-revision",
		0,
		"exact Conversation revision observed before this turn",
	)
	expectedHeadRunID := flags.String(
		"conversation-head-run",
		"",
		"exact current Conversation head Run (required after turn one)",
	)
	composite := flags.Bool(
		"composite",
		false,
		"explicitly run the selected frozen Composite Agent family",
	)
	schedulerFlags := bindFairSchedulerFlags(flags)
	deepSeekFlags := bindDeepSeekRuntimeFlags(flags)
	zhipuFlags := bindZhipuRuntimeFlags(flags)
	remoteActionFlags := bindRemoteActionRuntimeFlags(flags)
	wasmActionFlags := bindWASMActionRuntimeFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*message) == "" {
		return errors.New("freeagent chat: --db and --message are required")
	}
	if *composite && strings.TrimSpace(*conversationID) != "" {
		return errors.New(
			"freeagent chat: --composite and --conversation are mutually exclusive in W1",
		)
	}
	deadline, err := parseOptionalDeadline(*deadlineText)
	if err != nil {
		return fmt.Errorf("freeagent chat: %w", err)
	}
	schedulerConfig, err := schedulerFlags.config(flags, *tenantID)
	if err != nil {
		return fmt.Errorf("freeagent chat: %w", err)
	}
	deepSeekConfig, err := deepSeekFlags.config(flags)
	if err != nil {
		return fmt.Errorf("freeagent chat: %w", err)
	}
	zhipuConfig, err := zhipuFlags.config(flags)
	if err != nil {
		return fmt.Errorf("freeagent chat: %w", err)
	}
	remoteActionConfig, err := remoteActionFlags.config(flags)
	if err != nil {
		return fmt.Errorf("freeagent chat: %w", err)
	}
	wasmActionConfig, err := wasmActionFlags.config(flags)
	if err != nil {
		return fmt.Errorf("freeagent chat: %w", err)
	}

	composition, err := openProductionCompositionWithOptions(
		ctx,
		*databasePath,
		resolvedArtifactRoot(*databasePath, *artifactRoot),
		*tenantID,
		productionCompositionOptions{
			FairScheduler: schedulerConfig,
			DeepSeek:      deepSeekConfig,
			Zhipu:         zhipuConfig,
			RemoteAction:  remoteActionConfig,
			WASMAction:    wasmActionConfig,
		},
	)
	if err != nil {
		return fmt.Errorf("freeagent chat: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, composition.Close()) }()
	chatInput := localchat.ChatInput{
		TenantID:                     *tenantID,
		PrincipalID:                  *principalID,
		WorkspaceID:                  *workspaceID,
		AgentID:                      *agentID,
		ProfileID:                    *profileID,
		Message:                      *message,
		RequestID:                    *requestID,
		Deadline:                     deadline,
		ConversationID:               *conversationID,
		ExpectedConversationRevision: *expectedConversationRevision,
		ExpectedHeadRunID:            *expectedHeadRunID,
	}
	if *composite {
		if composition.composite == nil {
			return errors.New("freeagent chat: Composite service is unavailable")
		}
		result, compositeErr := composition.composite.Chat(ctx, chatInput)
		if compositeErr != nil {
			return fmt.Errorf("freeagent chat: %w", compositeErr)
		}
		return writeCommandJSON(stdout, newCompositeChatCommandResult(result))
	}
	result, err := composition.chat.Chat(ctx, chatInput)
	if err != nil {
		return fmt.Errorf("freeagent chat: %w", err)
	}
	commandResult := newChatCommandResult(result)
	commandResult.Usage, err = readChatCommandUsage(
		ctx,
		composition.store,
		result,
	)
	if err != nil {
		return fmt.Errorf("freeagent chat: read authoritative Usage: %w", err)
	}
	return writeCommandJSON(stdout, commandResult)
}

func runServe(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) (returnErr error) {
	flags := newFlagSet("serve", stderr)
	databasePath := flags.String("db", "", "existing Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "content-addressed artifact root")
	tenantID := flags.String("tenant", defaultTenantID, "tenant used to compose exact adapters")
	listenAddress := flags.String("listen", defaultListen, "loopback listen address")
	enableControl := flags.Bool(
		"enable-control",
		false,
		"explicitly enable the local Control API listener",
	)
	controlHandoffPath := flags.String(
		"control-handoff-path",
		"",
		"exclusive owner-only Control bootstrap handoff leaf",
	)
	enableChannel := flags.Bool(
		"enable-channel",
		false,
		"explicitly enable one configured loopback Channel endpoint",
	)
	channelWorkspace := flags.String(
		"channel-workspace",
		"",
		"Workspace that owns the explicitly enabled Channel endpoint",
	)
	channelEndpoint := flags.String(
		"channel-endpoint",
		"",
		"exact enabled Channel endpoint identity",
	)
	channelCredentialFile := flags.String(
		"channel-secret-file",
		"",
		"ordinary file containing the runtime Channel bearer secret",
	)
	schedulerFlags := bindFairSchedulerFlags(flags)
	deepSeekFlags := bindDeepSeekRuntimeFlags(flags)
	zhipuFlags := bindZhipuRuntimeFlags(flags)
	remoteActionFlags := bindRemoteActionRuntimeFlags(flags)
	wasmActionFlags := bindWASMActionRuntimeFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" {
		return errors.New("freeagent serve: --db is required")
	}
	if err := validateControlServeFlagsV1(
		*enableControl,
		*controlHandoffPath,
	); err != nil {
		return fmt.Errorf("freeagent serve: %w", err)
	}
	if err := localchat.ValidateLoopbackAddress(*listenAddress); err != nil {
		return fmt.Errorf("freeagent serve: %w", err)
	}
	schedulerConfig, err := schedulerFlags.config(flags, *tenantID)
	if err != nil {
		return fmt.Errorf("freeagent serve: %w", err)
	}
	deepSeekConfig, err := deepSeekFlags.config(flags)
	if err != nil {
		return fmt.Errorf("freeagent serve: %w", err)
	}
	zhipuConfig, err := zhipuFlags.config(flags)
	if err != nil {
		return fmt.Errorf("freeagent serve: %w", err)
	}
	remoteActionConfig, err := remoteActionFlags.config(flags)
	if err != nil {
		return fmt.Errorf("freeagent serve: %w", err)
	}
	wasmActionConfig, err := wasmActionFlags.config(flags)
	if err != nil {
		return fmt.Errorf("freeagent serve: %w", err)
	}
	channelOptionPresent := strings.TrimSpace(*channelWorkspace) != "" ||
		strings.TrimSpace(*channelEndpoint) != "" ||
		strings.TrimSpace(*channelCredentialFile) != ""
	if !*enableChannel && channelOptionPresent {
		return errors.New(
			"freeagent serve: Channel options require explicit --enable-channel",
		)
	}
	if *enableChannel && (strings.TrimSpace(*channelWorkspace) == "" ||
		strings.TrimSpace(*channelEndpoint) == "" ||
		strings.TrimSpace(*channelCredentialFile) == "") {
		return errors.New(
			"freeagent serve: --enable-channel requires --channel-workspace, --channel-endpoint, and --channel-secret-file",
		)
	}
	if *enableChannel && schedulerConfig != nil {
		return errors.New(
			"freeagent serve: fair Scheduler and Channel cannot be enabled together in S3-A",
		)
	}
	var composition *productionComposition
	err = nil
	if *enableChannel {
		resolver, resolverErr := newFileChannelSecretResolver(*channelCredentialFile)
		if resolverErr != nil {
			return fmt.Errorf("freeagent serve: %w", resolverErr)
		}
		composition, err = openProductionChannelCompositionWithOptions(
			ctx,
			*databasePath,
			resolvedArtifactRoot(*databasePath, *artifactRoot),
			productionChannelEndpointInput{
				TenantID:       *tenantID,
				WorkspaceID:    *channelWorkspace,
				EndpointID:     *channelEndpoint,
				SecretResolver: resolver,
			},
			productionCompositionOptions{
				DeepSeek:     deepSeekConfig,
				Zhipu:        zhipuConfig,
				RemoteAction: remoteActionConfig,
				WASMAction:   wasmActionConfig,
			},
		)
	} else {
		composition, err = openProductionCompositionWithOptions(
			ctx,
			*databasePath,
			resolvedArtifactRoot(*databasePath, *artifactRoot),
			*tenantID,
			productionCompositionOptions{
				FairScheduler: schedulerConfig,
				DeepSeek:      deepSeekConfig,
				Zhipu:         zhipuConfig,
				RemoteAction:  remoteActionConfig,
				WASMAction:    wasmActionConfig,
			},
		)
	}
	if err != nil {
		return fmt.Errorf("freeagent serve: %w", err)
	}
	safeToCloseComposition := true
	defer func() {
		if safeToCloseComposition {
			returnErr = errors.Join(returnErr, composition.Close())
		}
	}()
	handler, err := localchat.New(&tenantChatUseCase{
		tenantID: *tenantID,
		next:     composition.chat,
	})
	if err != nil {
		return fmt.Errorf("freeagent serve: %w", err)
	}
	var rootHandler http.Handler = handler
	ready := map[string]string{
		"deepseek_adapter": "disabled",
		"zhipu_adapter":    "disabled",
		"fair_scheduler":   "disabled",
		"listen":           "",
		"status":           "ready",
	}
	if deepSeekConfig != nil {
		ready["deepseek_adapter"] = "enabled"
	}
	if zhipuConfig != nil {
		ready["zhipu_adapter"] = "enabled"
	}
	if schedulerConfig != nil {
		ready["fair_scheduler"] = "enabled"
		ready["scheduler_limits"] = fmt.Sprintf(
			"global=%d,workspace=%d,family=%d",
			schedulerConfig.Limits.GlobalWorkers,
			schedulerConfig.Limits.MaxActivePerWorkspace,
			schedulerConfig.Limits.MaxActivePerFamily,
		)
	}
	if *enableChannel {
		if composition.channel == nil || composition.channel.handler == nil {
			return errors.New("freeagent serve: explicit Channel composition is incomplete")
		}
		path := composition.channel.handler.Path()
		if isReservedCoreHTTPPath(path) {
			return errors.New("freeagent serve: Channel inbound path conflicts with a Core route")
		}
		rootHandler = &exactChannelHTTPMux{
			path:     path,
			channel:  composition.channel.handler,
			fallback: handler,
		}
		ready["channel_endpoint"] = composition.channel.endpoint.EndpointID
		ready["channel_path"] = path
	}
	if *enableControl {
		// From this point the W6-1 coordinator owns the one Composition and
		// Store lifecycle. The default-off path below remains byte-for-byte on
		// the established single-listener lifecycle.
		safeToCloseComposition = false
		return runControlEnabledServeV1(controlServeInputV1{
			Lifecycle:     ctx,
			Stdout:        stdout,
			Composition:   composition,
			ArtifactRoot:  resolvedArtifactRoot(*databasePath, *artifactRoot),
			TenantID:      *tenantID,
			ListenAddress: *listenAddress,
			HandoffPath:   *controlHandoffPath,
			ChatHandler:   rootHandler,
			Ready:         ready,
		})
	}
	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return fmt.Errorf("freeagent serve: listen: %w", err)
	}
	authority, err := localchat.CanonicalAuthority(listener.Addr())
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("freeagent serve: %w", err)
	}
	securedHandler, err := localchat.NewAuthorityGuard(rootHandler, authority)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("freeagent serve: %w", err)
	}
	requestContext, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRequests()
	trackedHandler := &drainingHTTPHandler{next: securedHandler}
	server := &http.Server{
		Handler:           trackedHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return requestContext
		},
	}
	ready["listen"] = listener.Addr().String()
	if err := writeCommandJSON(stdout, ready); err != nil {
		_ = listener.Close()
		return err
	}
	drained, serveErr := serveHTTPUntilStopped(
		ctx,
		server,
		listener,
		trackedHandler,
		cancelRequests,
	)
	if !drained {
		// Closing the Store while a handler is still committing an Attempt or
		// terminal result is worse than leaking it until process exit.
		safeToCloseComposition = false
	} else if recoveryErr := runProductionShutdownRecovery(
		ctx,
		composition.store,
	); recoveryErr != nil {
		// A failed Store-only closeout may leave a PENDING effect. Do not close
		// the Store as if the durable terminal boundary had been proven.
		safeToCloseComposition = false
		serveErr = errors.Join(serveErr, recoveryErr)
	}
	if serveErr != nil {
		return fmt.Errorf("freeagent serve: %w", serveErr)
	}
	return nil
}

func runBackup(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := newFlagSet("backup", stderr)
	databasePath := flags.String("db", "", "closed Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "content-addressed artifact root")
	destination := flags.String("out", "", "new backup bundle directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*destination) == "" {
		return errors.New("freeagent backup: --db and --out are required")
	}
	manifest, err := currentbackup.CreateBundle(
		ctx,
		*databasePath,
		resolvedArtifactRoot(*databasePath, *artifactRoot),
		*destination,
		backupToolVersion,
	)
	if err != nil {
		return fmt.Errorf("freeagent backup: %w", err)
	}
	bundle, err := filepath.Abs(*destination)
	if err != nil {
		return fmt.Errorf("freeagent backup: resolve output: %w", err)
	}
	return writeCommandJSON(stdout, backupCommandResult{
		Bundle:   bundle,
		Manifest: manifest,
	})
}

func runBackupVerify(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := newFlagSet("backup-verify", stderr)
	bundle := flags.String("bundle", "", "existing backup bundle directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*bundle) == "" {
		return errors.New("freeagent backup-verify: --bundle is required")
	}
	manifest, err := currentbackup.VerifyBundle(ctx, *bundle)
	if err != nil {
		return fmt.Errorf("freeagent backup-verify: %w", err)
	}
	resolved, err := filepath.Abs(*bundle)
	if err != nil {
		return fmt.Errorf("freeagent backup-verify: resolve bundle: %w", err)
	}
	return writeCommandJSON(stdout, backupCommandResult{
		Bundle:   resolved,
		Manifest: manifest,
	})
}

func runRestore(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := newFlagSet("restore", stderr)
	bundle := flags.String("bundle", "", "existing backup bundle directory")
	databasePath := flags.String("db", "", "new Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "new content-addressed artifact root")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*bundle) == "" ||
		strings.TrimSpace(*databasePath) == "" {
		return errors.New("freeagent restore: --bundle and --db are required")
	}
	resolvedArtifacts := resolvedArtifactRoot(*databasePath, *artifactRoot)
	if err := currentbackup.RestoreBundle(
		ctx,
		*bundle,
		*databasePath,
		resolvedArtifacts,
	); err != nil {
		return fmt.Errorf("freeagent restore: %w", err)
	}
	verification, err := currentstore.VerifyCurrentStoreReadOnly(ctx, *databasePath)
	if err != nil {
		return fmt.Errorf("freeagent restore: verify restored Store: %w", err)
	}
	database, err := filepath.Abs(*databasePath)
	if err != nil {
		return fmt.Errorf("freeagent restore: resolve database: %w", err)
	}
	artifacts, err := filepath.Abs(resolvedArtifacts)
	if err != nil {
		return fmt.Errorf("freeagent restore: resolve artifact root: %w", err)
	}
	return writeCommandJSON(stdout, restoreCommandResult{
		DatabasePath:    database,
		ArtifactRoot:    artifacts,
		StoreInstanceID: verification.StoreInstanceID,
	})
}

func runMigrate(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := newFlagSet("migrate", stderr)
	databasePath := flags.String("db", "", "closed Current Store database path")
	artifactRoot := flags.String("artifact-root", "", "content-addressed artifact root")
	backupDestination := flags.String("backup-out", "", "new mandatory pre-migration backup bundle")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*databasePath) == "" ||
		strings.TrimSpace(*backupDestination) == "" {
		return errors.New("freeagent migrate: --db and --backup-out are required")
	}
	resolvedArtifacts := resolvedArtifactRoot(*databasePath, *artifactRoot)
	var manifest currentbackup.Manifest
	var migration currentstore.MigrationResult
	err := currentstore.WithOfflineLease(
		ctx,
		*databasePath,
		func(lease *currentstore.OfflineLease) error {
			var err error
			manifest, err = currentbackup.CreateBundleFromOfflineLease(
				ctx,
				lease,
				resolvedArtifacts,
				*backupDestination,
				backupToolVersion,
			)
			if err != nil {
				return err
			}
			migration, err = currentstore.MigrateOfflineCurrentStore(ctx, lease)
			return err
		},
	)
	if err != nil {
		return fmt.Errorf("freeagent migrate: %w", err)
	}
	backupPath, err := filepath.Abs(*backupDestination)
	if err != nil {
		return fmt.Errorf("freeagent migrate: resolve backup: %w", err)
	}
	database, err := filepath.Abs(*databasePath)
	if err != nil {
		return fmt.Errorf("freeagent migrate: resolve database: %w", err)
	}
	return writeCommandJSON(stdout, migrateCommandResult{
		DatabasePath:      database,
		BackupBundle:      backupPath,
		BackupManifest:    manifest,
		FromVersion:       migration.FromVersion,
		ToVersion:         migration.ToVersion,
		AppliedVersions:   migration.AppliedVersions,
		StoreInstanceID:   migration.Verification.StoreInstanceID,
		SchemaFingerprint: migration.Verification.SchemaFingerprint,
	})
}

func isReservedCoreHTTPPath(path string) bool {
	switch path {
	case "/healthz", "/v1/chat", "/v1/conversations":
		return true
	default:
		return false
	}
}

type tenantChatUseCase struct {
	tenantID string
	next     *localchat.ChatService
}

func (chat *tenantChatUseCase) Chat(
	ctx context.Context,
	input localchat.ChatInput,
) (localchat.ChatResult, error) {
	if chat == nil || chat.next == nil || input.TenantID != chat.tenantID {
		return localchat.ChatResult{}, fmt.Errorf(
			"%w: HTTP tenant is not the composed tenant",
			localchat.ErrInvalidChat,
		)
	}
	return chat.next.Chat(ctx, input)
}

func (chat *tenantChatUseCase) CreateConversation(
	ctx context.Context,
	input localchat.ConversationCreateInput,
) (localchat.ConversationCreateResult, error) {
	if chat == nil || chat.next == nil || input.TenantID != chat.tenantID {
		return localchat.ConversationCreateResult{}, fmt.Errorf(
			"%w: HTTP tenant is not the composed tenant",
			currentstore.ErrInvalidConversation,
		)
	}
	return chat.next.CreateConversation(ctx, input)
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

type fairSchedulerFlagValues struct {
	enabled      *bool
	global       *uint64
	perWorkspace *uint64
	perFamily    *uint64
}

func bindFairSchedulerFlags(flags *flag.FlagSet) fairSchedulerFlagValues {
	return fairSchedulerFlagValues{
		enabled: flags.Bool(
			"enable-fair-scheduler",
			false,
			"explicitly enable the tenant-bound S3-A fair Scheduler",
		),
		global: flags.Uint64(
			"scheduler-global-workers",
			4,
			"Store/process-wide active Run lease ceiling",
		),
		perWorkspace: flags.Uint64(
			"scheduler-workspace-workers",
			2,
			"active Run lease ceiling per Workspace",
		),
		perFamily: flags.Uint64(
			"scheduler-family-workers",
			2,
			"active Run lease ceiling per Composite family",
		),
	}
}

func (values fairSchedulerFlagValues) config(
	flags *flag.FlagSet,
	tenantID string,
) (*runscheduler.Config, error) {
	if values.enabled == nil || values.global == nil ||
		values.perWorkspace == nil || values.perFamily == nil {
		return nil, errors.New("fair Scheduler flags are not initialized")
	}
	limitsPresent := false
	flags.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "scheduler-global-workers",
			"scheduler-workspace-workers",
			"scheduler-family-workers":
			limitsPresent = true
		}
	})
	if !*values.enabled {
		if limitsPresent {
			return nil, errors.New(
				"Scheduler limit options require explicit --enable-fair-scheduler",
			)
		}
		return nil, nil
	}
	maxUint32 := uint64(^uint32(0))
	if *values.global == 0 || *values.global > maxUint32 ||
		*values.perWorkspace == 0 || *values.perWorkspace > maxUint32 ||
		*values.perFamily == 0 || *values.perFamily > maxUint32 {
		return nil, errors.New(
			"Scheduler concurrency limits must be positive uint32 values",
		)
	}
	config := runscheduler.DefaultConfig(tenantID)
	config.Limits = currentstore.FairSchedulerLimits{
		GlobalWorkers:         uint32(*values.global),
		MaxActivePerWorkspace: uint32(*values.perWorkspace),
		MaxActivePerFamily:    uint32(*values.perFamily),
	}
	if config.Limits.MaxActivePerWorkspace > config.Limits.GlobalWorkers ||
		config.Limits.MaxActivePerFamily > config.Limits.GlobalWorkers {
		return nil, errors.New(
			"Scheduler Workspace/family limits cannot exceed global workers",
		)
	}
	return &config, nil
}

type repeatedStringFlag []string

func (values *repeatedStringFlag) String() string {
	if values == nil {
		return ""
	}
	return strings.Join(*values, ",")
}

func (values *repeatedStringFlag) Set(value string) error {
	if values == nil {
		return errors.New("freeagent: repeated string flag target is nil")
	}
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return errors.New("value must be non-empty and trimmed")
	}
	*values = append(*values, value)
	return nil
}

func parseOptionalDeadline(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	deadline, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.New("--deadline must be RFC3339")
	}
	return deadline, nil
}

type chatCommandResult struct {
	RequestID            string            `json:"request_id"`
	Deadline             string            `json:"deadline"`
	RunID                string            `json:"run_id"`
	ConversationID       string            `json:"conversation_id,omitempty"`
	ConversationRevision uint64            `json:"conversation_revision,omitempty"`
	Disposition          string            `json:"disposition"`
	Reason               string            `json:"reason"`
	Reply                string            `json:"reply"`
	Failure              string            `json:"failure"`
	Usage                *chatCommandUsage `json:"usage,omitempty"`
}

// chatCommandUsage is a read-only CLI projection of one terminal model
// Attempt's authoritative Usage. Nil token fields encode as JSON null:
// UNKNOWN is never rewritten to 0.
type chatCommandUsage struct {
	InputTokens         *uint64 `json:"input_tokens"`
	CachedInputTokens   *uint64 `json:"cached_input_tokens"`
	UncachedInputTokens *uint64 `json:"uncached_input_tokens"`
	OutputTokens        *uint64 `json:"output_tokens"`
	ReasoningTokens     *uint64 `json:"reasoning_tokens"`
	Status              string  `json:"status"`
}

type conversationCommandResult struct {
	ConversationID string `json:"conversation_id"`
	TenantID       string `json:"tenant_id"`
	PrincipalID    string `json:"principal_id"`
	WorkspaceID    string `json:"workspace_id"`
	AgentID        string `json:"agent_id"`
	ProfileID      string `json:"profile_id"`
	HeadRunID      string `json:"head_run_id,omitempty"`
	Revision       uint64 `json:"revision"`
	Created        bool   `json:"created"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type compositeChildCommandResult struct {
	RunID       string `json:"run_id"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
}

type compositeReviewerCommandResult struct {
	RunID       string `json:"run_id"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
}

type compositeChatCommandResult struct {
	RequestID   string                          `json:"request_id"`
	Deadline    string                          `json:"deadline"`
	RootRunID   string                          `json:"root_run_id"`
	Children    []compositeChildCommandResult   `json:"children"`
	Reviewer    *compositeReviewerCommandResult `json:"reviewer,omitempty"`
	Disposition string                          `json:"disposition"`
	Reason      string                          `json:"reason"`
	Reply       string                          `json:"reply"`
	Failure     string                          `json:"failure"`
}

type backupCommandResult struct {
	Bundle   string                 `json:"bundle"`
	Manifest currentbackup.Manifest `json:"manifest"`
}

type restoreCommandResult struct {
	DatabasePath    string `json:"database_path"`
	ArtifactRoot    string `json:"artifact_root"`
	StoreInstanceID string `json:"store_instance_id"`
}

type migrateCommandResult struct {
	DatabasePath      string                 `json:"database_path"`
	BackupBundle      string                 `json:"backup_bundle"`
	BackupManifest    currentbackup.Manifest `json:"backup_manifest"`
	FromVersion       int                    `json:"from_version"`
	ToVersion         int                    `json:"to_version"`
	AppliedVersions   []int                  `json:"applied_versions"`
	StoreInstanceID   string                 `json:"store_instance_id"`
	SchemaFingerprint string                 `json:"schema_fingerprint"`
}

func newChatCommandResult(result localchat.ChatResult) chatCommandResult {
	return chatCommandResult{
		RequestID:            result.RequestID,
		Deadline:             result.Deadline.UTC().Format(time.RFC3339Nano),
		RunID:                result.RunID,
		ConversationID:       result.ConversationID,
		ConversationRevision: result.ConversationRevision,
		Disposition:          string(result.LoopResult.Disposition),
		Reason:               result.LoopResult.ReasonCode,
		Reply:                result.Reply,
		Failure:              result.FailureCode,
	}
}

func readChatCommandUsage(
	ctx context.Context,
	store *currentstore.Store,
	result localchat.ChatResult,
) (*chatCommandUsage, error) {
	if result.TerminalResult == nil ||
		result.TerminalResult.AttemptKind != corecontract.AttemptKindModel {
		return nil, nil
	}
	if store == nil {
		return nil, errors.New("Current Store is unavailable")
	}
	record, err := store.GetModelDispatchRecord(
		ctx,
		result.TerminalResult.AttemptID,
	)
	if err != nil {
		return nil, err
	}
	if record.Attempt.RunID != result.RunID ||
		record.Attempt.AttemptID != result.TerminalResult.AttemptID {
		return nil, errors.New("terminal model Attempt identity drift")
	}
	return &chatCommandUsage{
		InputTokens:         record.Usage.Tokens.Input,
		CachedInputTokens:   record.Usage.Tokens.CachedInput,
		UncachedInputTokens: record.Usage.Tokens.UncachedInput,
		OutputTokens:        record.Usage.Tokens.Output,
		ReasoningTokens:     record.Usage.Tokens.Reasoning,
		Status:              record.Usage.UsageStatus,
	}, nil
}

func newConversationCommandResult(
	record currentstore.ConversationRecord,
	created bool,
) conversationCommandResult {
	return conversationCommandResult{
		ConversationID: record.ConversationID,
		TenantID:       record.TenantID,
		PrincipalID:    record.PrincipalID,
		WorkspaceID:    record.WorkspaceID,
		AgentID:        record.AgentID,
		ProfileID:      record.ProfileID,
		HeadRunID:      record.HeadRunID,
		Revision:       record.Revision,
		Created:        created,
		CreatedAt:      record.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:      record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func newCompositeChatCommandResult(
	result localchat.CompositeChatResult,
) compositeChatCommandResult {
	children := make(
		[]compositeChildCommandResult,
		len(result.Children),
	)
	for index, child := range result.Children {
		children[index] = compositeChildCommandResult{
			RunID:       child.RunID,
			Disposition: string(child.LoopResult.Disposition),
			Reason:      child.LoopResult.ReasonCode,
		}
	}
	var reviewer *compositeReviewerCommandResult
	if result.Reviewer != nil {
		reviewer = &compositeReviewerCommandResult{
			RunID:       result.Reviewer.RunID,
			Disposition: string(result.Reviewer.LoopResult.Disposition),
			Reason:      result.Reviewer.LoopResult.ReasonCode,
		}
	}
	return compositeChatCommandResult{
		RequestID:   result.RequestID,
		Deadline:    result.Deadline.UTC().Format(time.RFC3339Nano),
		RootRunID:   result.RootRunID,
		Children:    children,
		Reviewer:    reviewer,
		Disposition: string(result.LoopResult.Disposition),
		Reason:      result.LoopResult.ReasonCode,
		Reply:       result.Reply,
		Failure:     result.FailureCode,
	}
}

func writeCommandJSON(destination io.Writer, value any) error {
	encoder := json.NewEncoder(destination)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("freeagent: write JSON response: %w", err)
	}
	return nil
}
