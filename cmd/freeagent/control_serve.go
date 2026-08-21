package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlconfirmation"
	"github.com/endview/freeagent/internal/controlcursor"
	"github.com/endview/freeagent/internal/controlhandoff"
	"github.com/endview/freeagent/internal/controlhttp"
	"github.com/endview/freeagent/internal/controlmutation"
	"github.com/endview/freeagent/internal/controlruntime"
	"github.com/endview/freeagent/internal/controlsession"
)

const controlAuthorizationRevisionV1 uint64 = 1

var verifyControlNonElevatedV1 = controlhandoff.VerifyNonElevatedV1

// This narrow constructor seam lets the default-off production E2E prove that
// no confirmation authority is initialized unless Control is explicitly on.
var newControlConfirmationRegistryV1 = controlconfirmation.NewRegistryV1

type controlHandoffCleanupV1 interface {
	Cleanup() error
}

// publishControlHandoffV1 is a narrow composition seam. Production always
// delegates to the independently fail-closed controlhandoff package; package
// tests may replace only delivery so an otherwise privileged test runner can
// exercise the complete post-verification composition chain.
var publishControlHandoffV1 = func(
	ctx context.Context,
	config controlhandoff.ConfigV1,
) (controlHandoffCleanupV1, error) {
	return controlhandoff.PublishV1(ctx, config)
}

type controlServeInputV1 struct {
	Lifecycle     context.Context
	Stdout        io.Writer
	Composition   *productionComposition
	ArtifactRoot  string
	TenantID      string
	ListenAddress string
	HandoffPath   string
	ChatHandler   http.Handler
	Ready         map[string]string
}

type controlServeOutcomeV1 struct {
	endpoint string
	err      error
}

// validateControlServeFlagsV1 is deliberately called before any Composition,
// Store, artifact-root, credential-file, or listener work. Merely passing a
// Control-only option never enables the optional surface.
func validateControlServeFlagsV1(enabled bool, handoffPath string) error {
	trimmedPath := strings.TrimSpace(handoffPath)
	present := trimmedPath != ""
	if !enabled {
		if present {
			return errors.New(
				"--control-handoff-path requires explicit --enable-control",
			)
		}
		return nil
	}
	if !present {
		return errors.New(
			"--enable-control requires --control-handoff-path",
		)
	}
	if handoffPath != trimmedPath {
		return errors.New(
			"--control-handoff-path must be an exact path without surrounding whitespace",
		)
	}
	if err := verifyControlNonElevatedV1(); err != nil {
		return fmt.Errorf("Control process privilege verification: %w", err)
	}
	return nil
}

// runControlEnabledServeV1 is the sole W6-1 production composition root. It
// shares one Store, application-service set, Admission gate, request context,
// shutdown budget, and shutdown coordinator across the Chat and Control
// listeners. Both sockets serve behind startup-closed admission until the
// exclusive handoff and readiness publication have completed.
func runControlEnabledServeV1(input controlServeInputV1) (returnErr error) {
	if err := validateControlServeInputV1(input); err != nil {
		return errors.Join(err, closeUnusedControlCompositionV1(input.Composition))
	}

	// Until a coordinator is constructed, this guard owns the unused
	// Composition. Once transferred, only that coordinator may recover and
	// close the Store.
	coordinatorOwnsComposition := false
	defer func() {
		if !coordinatorOwnsComposition {
			returnErr = errors.Join(
				returnErr,
				closeUnusedControlCompositionV1(input.Composition),
			)
		}
	}()

	modules, err := controlapp.NewModulesServiceV1(
		productionControlPublishedBasisReaderV1{store: input.Composition.store},
	)
	if err != nil {
		return fmt.Errorf("Control Modules application service: %w", err)
	}
	overview, err := newControlOverviewServiceV1(input.Composition.store)
	if err != nil {
		return fmt.Errorf("Control Overview application service: %w", err)
	}
	staticAssets, err := newControlStaticAssetResolverV1()
	if err != nil {
		return fmt.Errorf("Control Web shell assets: %w", err)
	}
	disableDryRun := &productionModuleDisableDryRunServiceV1{
		store:        input.Composition.store,
		artifactRoot: input.ArtifactRoot,
	}

	gate := controlruntime.NewAdmissionGate()
	chatListener, err := net.Listen("tcp", input.ListenAddress)
	if err != nil {
		return fmt.Errorf("Control-enabled Chat listen: %w", err)
	}
	chatListenerOwned := true
	defer func() {
		if chatListenerOwned {
			returnErr = errors.Join(
				returnErr,
				normalizeListenerCloseV1(chatListener.Close()),
			)
		}
	}()

	controlListener, err := net.ListenTCP(
		"tcp4",
		&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0},
	)
	if err != nil {
		return fmt.Errorf("Control listen: %w", err)
	}
	controlListenerOwned := true
	defer func() {
		if controlListenerOwned {
			returnErr = errors.Join(
				returnErr,
				normalizeListenerCloseV1(controlListener.Close()),
			)
		}
	}()
	authority, err := canonicalControlAuthorityV1(controlListener.Addr())
	if err != nil {
		return err
	}

	scopeSet, err := controlsession.NewAuthorizedScopeSetV1(
		[]controlapicontract.ControlScopeV1{{
			SchemaVersion: controlapicontract.ControlScopeSchemaVersionV1,
			Kind:          controlapicontract.ScopeTenantV1,
			TenantID:      input.TenantID,
		}},
	)
	if err != nil {
		return fmt.Errorf("Control scope set: %w", err)
	}
	registry, bootstrap, err := controlsession.NewRegistryV1(
		controlsession.RegistryConfigV1{
			PrincipalID:           defaultPrincipalID,
			AuthorizationRevision: controlAuthorizationRevisionV1,
			Capabilities: []controlapicontract.ControlCapabilityV1{
				controlapicontract.CapabilityObserveV1,
				controlapicontract.CapabilityOperateModulesV1,
			},
			ScopeSet: scopeSet,
		},
	)
	if err != nil {
		return fmt.Errorf("Control session registry: %w", err)
	}
	defer registry.Close()
	// bootstrap is caller-owned. PublishV1 copies it into its bounded
	// canonical file; erase our copy on every path after publication or error.
	defer clear(bootstrap.Capability)

	cursors, err := controlcursor.NewRegistryV1(registry.BootID())
	if err != nil {
		return fmt.Errorf("Control cursor registry: %w", err)
	}
	defer cursors.Close()

	confirmations, err := newControlConfirmationRegistryV1(
		controlconfirmation.RegistryConfigV1{},
	)
	if err != nil {
		return fmt.Errorf("Control confirmation registry: %w", err)
	}
	// Normal coordinated shutdown closes this authority before runtime drain
	// and Store recovery. The defer is the early-construction-failure fallback.
	defer confirmations.Close()
	mutationCoordinator, err := controlmutation.NewCoordinatorV1(
		input.Composition.store,
		confirmations,
	)
	if err != nil {
		return fmt.Errorf("Control mutation coordinator: %w", err)
	}
	moduleDisableMutation := &productionModuleDisableControlServiceV1{
		coordinator: mutationCoordinator,
	}

	controlHandler, err := controlhttp.NewHandlerV1(controlhttp.ConfigV1{
		Authority:                 authority,
		Registry:                  registry,
		StaticAssets:              staticAssets,
		Overview:                  overview,
		Modules:                   modules,
		ModuleDisableDryRun:       disableDryRun,
		ModuleDisableConfirmation: moduleDisableMutation,
		ModuleDisableMutation:     moduleDisableMutation,
		Cursor:                    cursors,
	})
	if err != nil {
		return fmt.Errorf("Control HTTP handler: %w", err)
	}
	chatAdmission, err := gate.Wrap(input.ChatHandler)
	if err != nil {
		return fmt.Errorf("Control-enabled Chat admission: %w", err)
	}
	controlAdmission, err := gate.Wrap(controlHandler)
	if err != nil {
		return fmt.Errorf("Control admission: %w", err)
	}
	securedControl, err := controlhttp.WrapSecurityHeadersV1(controlAdmission)
	if err != nil {
		return fmt.Errorf("Control security wrapper: %w", err)
	}

	requestContext, cancelRequests := context.WithCancel(
		context.WithoutCancel(input.Lifecycle),
	)
	defer cancelRequests()
	chatServer := &http.Server{
		Handler:           chatAdmission,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return requestContext
		},
	}
	controlServer := &http.Server{
		Handler: securedControl,
		BaseContext: func(net.Listener) context.Context {
			return requestContext
		},
	}
	if err := controlhttp.RecommendedServerPolicyV1().ApplyTo(controlServer); err != nil {
		return fmt.Errorf("Control HTTP server policy: %w", err)
	}

	budget, err := controlruntime.NewShutdownBudget(
		context.WithoutCancel(input.Lifecycle),
	)
	if err != nil {
		return fmt.Errorf("Control shutdown budget: %w", err)
	}
	coordinator, err := controlruntime.NewShutdownCoordinator(
		gate,
		budget,
		controlruntime.ShutdownHooks{
			Endpoints: [2]controlruntime.ShutdownStep{
				shutdownHTTPServerV1(chatServer, chatListener),
				shutdownHTTPServerV1(controlServer, controlListener),
			},
			CancelRequests: cancelRequests,
			DrainRuntime: func(ctx context.Context) error {
				// Admission and both endpoints have drained before this hook.
				// Invalidate every outstanding proof before any shared runtime
				// drain, recovery, or Store close can begin.
				confirmations.Close()
				return input.Composition.DrainRuntimeV1(ctx)
			},
			RecoverStore: func(ctx context.Context) error {
				return runProductionShutdownRecoveryWithinV1(
					ctx,
					input.Composition.store,
				)
			},
			CloseStore: func(context.Context) error {
				return input.Composition.CloseStoreV1()
			},
		},
	)
	if err != nil {
		return fmt.Errorf("Control shutdown coordinator: %w", err)
	}
	coordinatorOwnsComposition = true
	chatListenerOwned = false
	controlListenerOwned = false

	serveResults := make(chan controlServeOutcomeV1, 2)
	go serveControlEndpointV1("chat", chatServer, chatListener, serveResults)
	go serveControlEndpointV1("control", controlServer, controlListener, serveResults)
	serveRemaining := 2

	var handoff controlHandoffCleanupV1
	startupErr := func() error {
		if outcome, stopped := pollControlServeOutcomeV1(serveResults); stopped {
			serveRemaining--
			return unexpectedControlServeStopV1(outcome)
		}
		published, publishErr := publishControlHandoffV1(
			input.Lifecycle,
			controlhandoff.ConfigV1{
				Origin:      "http://" + authority,
				Material:    bootstrap,
				HandoffPath: input.HandoffPath,
			},
		)
		if publishErr != nil {
			return fmt.Errorf("publish Control handoff: %w", publishErr)
		}
		if published == nil {
			return errors.New("publish Control handoff returned no cleanup authority")
		}
		handoff = published
		go func() {
			<-registry.BootstrapDone()
			_ = published.Cleanup()
		}()
		if outcome, stopped := pollControlServeOutcomeV1(serveResults); stopped {
			serveRemaining--
			return unexpectedControlServeStopV1(outcome)
		}

		ready := make(map[string]string, len(input.Ready)+1)
		for key, value := range input.Ready {
			ready[key] = value
		}
		ready["listen"] = chatListener.Addr().String()
		ready["control"] = "enabled"
		if err := writeCommandJSON(input.Stdout, ready); err != nil {
			return fmt.Errorf("publish Control-enabled readiness: %w", err)
		}
		if outcome, stopped := pollControlServeOutcomeV1(serveResults); stopped {
			serveRemaining--
			return unexpectedControlServeStopV1(outcome)
		}
		if err := gate.Open(); err != nil {
			return fmt.Errorf("open shared Control admission: %w", err)
		}
		return nil
	}()
	clear(bootstrap.Capability)

	if startupErr != nil {
		if handoff != nil {
			startupErr = errors.Join(startupErr, handoff.Cleanup())
		}
		return finishControlServeV1(
			coordinator,
			serveResults,
			serveRemaining,
			startupErr,
		)
	}

	var triggerErr error
	select {
	case outcome := <-serveResults:
		serveRemaining--
		triggerErr = unexpectedControlServeStopV1(outcome)
	case <-input.Lifecycle.Done():
	}
	if handoff != nil {
		defer func() {
			returnErr = errors.Join(returnErr, handoff.Cleanup())
		}()
	}
	return finishControlServeV1(
		coordinator,
		serveResults,
		serveRemaining,
		triggerErr,
	)
}

func validateControlServeInputV1(input controlServeInputV1) error {
	if input.Lifecycle == nil || input.Stdout == nil || input.Composition == nil ||
		input.Composition.store == nil || input.ChatHandler == nil ||
		input.Ready == nil || strings.TrimSpace(input.ArtifactRoot) == "" ||
		strings.TrimSpace(input.TenantID) == "" ||
		strings.TrimSpace(input.ListenAddress) == "" ||
		strings.TrimSpace(input.HandoffPath) == "" {
		return errors.New("Control serve composition is incomplete")
	}
	return nil
}

func canonicalControlAuthorityV1(address net.Addr) (string, error) {
	tcpAddress, ok := address.(*net.TCPAddr)
	if !ok || tcpAddress == nil || tcpAddress.Port < 1 || tcpAddress.Port > 65535 ||
		tcpAddress.IP == nil || tcpAddress.IP.To4() == nil ||
		!tcpAddress.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		return "", errors.New("Control listener did not bind exact IPv4 loopback")
	}
	return net.JoinHostPort(
		"127.0.0.1",
		strconv.Itoa(tcpAddress.Port),
	), nil
}

func serveControlEndpointV1(
	name string,
	server *http.Server,
	listener net.Listener,
	results chan<- controlServeOutcomeV1,
) {
	results <- controlServeOutcomeV1{
		endpoint: name,
		err:      server.Serve(listener),
	}
}

func pollControlServeOutcomeV1(
	results <-chan controlServeOutcomeV1,
) (controlServeOutcomeV1, bool) {
	select {
	case outcome := <-results:
		return outcome, true
	default:
		return controlServeOutcomeV1{}, false
	}
}

func unexpectedControlServeStopV1(outcome controlServeOutcomeV1) error {
	err := normalizeControlServeErrorV1(outcome.err)
	if err == nil {
		return fmt.Errorf("%s HTTP endpoint stopped unexpectedly", outcome.endpoint)
	}
	return fmt.Errorf("%s HTTP endpoint: %w", outcome.endpoint, err)
}

func normalizeControlServeErrorV1(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) ||
		errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func normalizeListenerCloseV1(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func shutdownHTTPServerV1(
	server *http.Server,
	listener net.Listener,
) controlruntime.ShutdownStep {
	return func(ctx context.Context) (returnErr error) {
		if server == nil || listener == nil || ctx == nil {
			return errors.New("HTTP endpoint shutdown is not initialized")
		}
		// Shutdown normally owns listener closure. The explicit close also
		// covers the startup race in which Serve has not registered its
		// listener before an early handoff failure begins shutdown.
		defer func() {
			returnErr = errors.Join(
				returnErr,
				normalizeListenerCloseV1(listener.Close()),
			)
		}()
		return normalizeControlServeErrorV1(server.Shutdown(ctx))
	}
}

func finishControlServeV1(
	coordinator *controlruntime.ShutdownCoordinator,
	serveResults <-chan controlServeOutcomeV1,
	remaining int,
	triggerErr error,
) error {
	if coordinator == nil || serveResults == nil || remaining < 0 || remaining > 2 {
		return errors.Join(triggerErr, errors.New("Control shutdown is not initialized"))
	}
	_, shutdownErr := coordinator.Shutdown(context.Background())
	serveErr := triggerErr
	for index := 0; index < remaining; index++ {
		outcome := <-serveResults
		if err := normalizeControlServeErrorV1(outcome.err); err != nil {
			serveErr = errors.Join(
				serveErr,
				fmt.Errorf("%s HTTP endpoint: %w", outcome.endpoint, err),
			)
		}
	}
	return errors.Join(serveErr, shutdownErr)
}

func closeUnusedControlCompositionV1(
	composition *productionComposition,
) error {
	if composition == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := composition.DrainRuntimeV1(ctx); err != nil {
		// Preserve Store ownership while a Scheduler quantum may still settle.
		return err
	}
	return composition.CloseStoreV1()
}
