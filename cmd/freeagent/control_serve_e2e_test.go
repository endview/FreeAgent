package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlconfirmation"
	"github.com/endview/freeagent/internal/controlhandoff"
	"github.com/endview/freeagent/internal/controlhttp"
	"github.com/endview/freeagent/internal/controlsession"
	"github.com/endview/freeagent/internal/controlweb"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type controlServeE2EHandoffV1 struct {
	SchemaVersion       string `json:"schema_version"`
	Origin              string `json:"origin"`
	Capability          string `json:"capability"`
	ExpiresAtUnixMicros uint64 `json:"expires_at_unix_micros"`
}

type controlServeE2EBootstrapResponseV2 struct {
	SchemaVersion    string                              `json:"schema_version"`
	Session          controlapicontract.ControlSessionV1 `json:"session"`
	AuthorizedScopes []controlapicontract.ControlScopeV1 `json:"authorized_scopes"`
	CSRFToken        string                              `json:"csrf_token"`
	ResumeCredential string                              `json:"resume_credential"`
}

type controlServeE2ESessionResumeResponseV1 struct {
	SchemaVersion    string                              `json:"schema_version"`
	Session          controlapicontract.ControlSessionV1 `json:"session"`
	AuthorizedScopes []controlapicontract.ControlScopeV1 `json:"authorized_scopes"`
	CSRFToken        string                              `json:"csrf_token"`
	ResumeCredential string                              `json:"resume_credential"`
}

type controlServeE2EModulesPageV1 struct {
	SchemaVersion      string                                   `json:"schema_version"`
	PublishedPointer   controlapicontract.ExpectedResourceRefV1 `json:"published_pointer"`
	Basis              controlapicontract.PublishedBasisRefV1   `json:"basis"`
	View               controlapicontract.ControlViewSnapshotV1 `json:"view"`
	ViewSnapshotDigest string                                   `json:"view_snapshot_digest"`
	SourceRevision     uint64                                   `json:"source_revision"`
	SourceDigest       string                                   `json:"source_digest"`
	SortVersion        string                                   `json:"sort_version"`
	FilterDigest       string                                   `json:"filter_digest"`
	Items              []controlapp.ModuleSummaryV1             `json:"items"`
	HasMore            bool                                     `json:"has_more"`
	NextCursor         string                                   `json:"next_cursor,omitempty"`
	ProjectionDigest   string                                   `json:"projection_digest"`
}

func TestControlEnabledServeProductionDisableDryRunIsEffectFreeV1(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	enablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0),
	)
	if result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePath,
		fixture.ArtifactDirectory,
		"",
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare Control MODULE_DISABLE: result=%+v err=%v", result, err)
	}

	basisBefore := loadControlServeE2EPublishedBasisV1(t, databasePath)
	countsBefore := snapshotControlServeE2ETableCountsV1(t, databasePath)
	artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
	handoffPath := filepath.Join(root, "control-bootstrap.json")

	originalVerifier := verifyControlNonElevatedV1
	verifyControlNonElevatedV1 = func() error { return nil }
	t.Cleanup(func() { verifyControlNonElevatedV1 = originalVerifier })
	installControlServeE2EHandoffPublisherV1(t, handoffPath)

	serveContext, stopServe := context.WithCancel(context.Background())
	stdoutReader, stdoutWriter := io.Pipe()
	serveDone := make(chan error, 1)
	go func() {
		err := run(
			serveContext,
			[]string{
				"serve",
				"--db", databasePath,
				"--artifact-root", artifactRoot,
				"--tenant", defaultTenantID,
				"--listen", "127.0.0.1:0",
				"--enable-control",
				"--control-handoff-path", handoffPath,
			},
			stdoutWriter,
			io.Discard,
		)
		_ = stdoutWriter.CloseWithError(err)
		serveDone <- err
	}()
	serveRunning := true
	t.Cleanup(func() {
		if !serveRunning {
			return
		}
		stopServe()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("Control serve cleanup: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("Control serve cleanup did not finish")
		}
	})

	var ready map[string]string
	if err := json.NewDecoder(stdoutReader).Decode(&ready); err != nil {
		t.Fatalf("decode Control readiness: %v", err)
	}
	if ready["status"] != "ready" || ready["control"] != "enabled" ||
		ready["listen"] == "" {
		t.Fatalf("Control readiness=%v", ready)
	}

	handoffInfo, err := os.Stat(handoffPath)
	if err != nil {
		t.Fatalf("stat owner-only Control handoff: %v", err)
	}
	if runtime.GOOS != "windows" && handoffInfo.Mode().Perm() != 0o600 {
		t.Fatalf("Control handoff mode=%#o, want 0600", handoffInfo.Mode().Perm())
	}
	handoffCanonical, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("read Control handoff: %v", err)
	}
	var handoff controlServeE2EHandoffV1
	decodeControlServeE2ESecretJSONV1(t, handoffCanonical, &handoff)
	if handoff.SchemaVersion != controlhandoff.SchemaVersionV1 ||
		!strings.HasPrefix(handoff.Origin, "http://127.0.0.1:") ||
		handoff.Capability == "" ||
		handoff.ExpiresAtUnixMicros <= uint64(time.Now().UTC().UnixMicro()) ||
		strings.TrimPrefix(handoff.Origin, "http://") == ready["listen"] {
		t.Fatalf("unsafe or non-distinct Control handoff=%+v ready=%v", handoff, ready)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	assertControlServeE2EStaticWebV1(t, client, handoff.Origin)
	chatHealth, err := client.Get("http://" + ready["listen"] + "/healthz")
	if err != nil {
		t.Fatalf("GET Control-enabled Chat health: %v", err)
	}
	chatHealthBody := readControlServeE2EResponseV1(t, chatHealth)
	if chatHealth.StatusCode != http.StatusOK {
		t.Fatalf(
			"Control-enabled Chat health status=%d body=%s",
			chatHealth.StatusCode,
			chatHealthBody,
		)
	}
	bootstrapRaw, err := json.Marshal(map[string]string{
		"schema_version": controlhttp.BootstrapRequestSchemaV1,
		"capability":     handoff.Capability,
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrapCanonical, err := moduleapi.CanonicalJSON(bootstrapRaw)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapRequest, err := http.NewRequest(
		http.MethodPost,
		handoff.Origin+controlhttp.BootstrapPathV1,
		bytes.NewReader(bootstrapCanonical),
	)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapRequest.Header.Set("Content-Type", "application/json")
	bootstrapRequest.Header.Set("Origin", handoff.Origin)
	bootstrapResponse, err := client.Do(bootstrapRequest)
	if err != nil {
		t.Fatalf("exchange production Control bootstrap: %v", err)
	}
	bootstrapBody := readControlServeE2EResponseV1(t, bootstrapResponse)
	if bootstrapResponse.StatusCode != http.StatusOK {
		t.Fatalf("Control bootstrap status=%d", bootstrapResponse.StatusCode)
	}
	var bootstrap controlServeE2EBootstrapResponseV2
	decodeControlServeE2ESecretJSONV1(t, bootstrapBody, &bootstrap)
	_, sessionCanonical, _, err := controlapicontract.NewControlSessionV1(bootstrap.Session)
	clear(sessionCanonical)
	scopeSet, scopeErr := controlsession.NewAuthorizedScopeSetV1(bootstrap.AuthorizedScopes)
	if err != nil || scopeErr != nil ||
		bootstrap.SchemaVersion != controlhttp.BootstrapResponseSchemaV2 ||
		bootstrap.CSRFToken == "" || bootstrap.ResumeCredential == "" ||
		scopeSet.Digest() != bootstrap.Session.ScopeSetDigest {
		t.Fatalf("invalid Control bootstrap response: session=%v scopes=%v", err, scopeErr)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range bootstrapResponse.Cookies() {
		if cookie.Name == controlhttp.SessionCookieV1 {
			copy := *cookie
			sessionCookie = &copy
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" ||
		sessionCookie.Path != "/control/" || !sessionCookie.HttpOnly {
		t.Fatalf("invalid Control session cookie=%+v", sessionCookie)
	}
	waitForControlServeE2EAbsentV1(t, handoffPath, time.Second)

	overviewRequest, err := http.NewRequest(
		http.MethodGet,
		handoff.Origin+controlhttp.OverviewPathV1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	setControlServeE2ETenantScopeV1(overviewRequest)
	overviewRequest.Header.Set(controlhttp.CSRFHeaderV1, bootstrap.CSRFToken)
	overviewRequest.AddCookie(sessionCookie)
	overviewResponse, err := client.Do(overviewRequest)
	if err != nil {
		t.Fatalf("GET production Control Overview: %v", err)
	}
	overviewBody := readControlServeE2EResponseV1(t, overviewResponse)
	if overviewResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"Control Overview status=%d body=%s",
			overviewResponse.StatusCode,
			overviewBody,
		)
	}
	overviewETag := overviewResponse.Header.Get("ETag")
	var overview controlapp.OverviewResultV1
	decodeControlServeE2EJSONV1(t, overviewBody, &overview)
	if overview.SchemaVersion != controlhttp.OverviewHTTPResponseSchemaV1 ||
		overview.PublishedPointer.Validate() != nil || overview.Basis.Validate() != nil ||
		overview.PublishedPointer.ResourceID != defaultTenantID ||
		overview.PublishedPointer.Revision != basisBefore.PointerRevision ||
		overview.View.Scope.Kind != controlapicontract.ScopeTenantV1 ||
		overview.View.Scope.TenantID != defaultTenantID ||
		len(overview.View.Sections) != 6 ||
		overview.Workspaces == nil || overview.Runs == nil || overview.Unknown == nil ||
		overview.Learning == nil || overview.ModuleCandidates == nil || overview.Usage == nil ||
		!moduleapi.ValidSHA256(overview.ViewSnapshotDigest) ||
		!moduleapi.ValidSHA256(overview.ProjectionDigest) ||
		len(overviewETag) != 66 || overviewETag[0] != '"' || overviewETag[65] != '"' ||
		!moduleapi.ValidSHA256(overviewETag[1:65]) ||
		bytes.Contains(overviewBody, []byte(artifactRoot)) ||
		bytes.Contains(overviewBody, []byte(fixture.ArtifactDirectory)) {
		t.Fatalf("invalid or unsafe production Control Overview=%+v ETag=%q", overview, overviewETag)
	}
	overviewConditionalRequest, err := http.NewRequest(
		http.MethodGet,
		handoff.Origin+controlhttp.OverviewPathV1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	setControlServeE2ETenantScopeV1(overviewConditionalRequest)
	overviewConditionalRequest.Header.Set(controlhttp.CSRFHeaderV1, bootstrap.CSRFToken)
	overviewConditionalRequest.Header.Set("If-None-Match", overviewETag)
	overviewConditionalRequest.AddCookie(sessionCookie)
	overviewConditionalResponse, err := client.Do(overviewConditionalRequest)
	if err != nil {
		t.Fatalf("GET conditional production Control Overview: %v", err)
	}
	overviewConditionalBody := readControlServeE2EResponseV1(t, overviewConditionalResponse)
	if overviewConditionalResponse.StatusCode != http.StatusNotModified ||
		len(overviewConditionalBody) != 0 ||
		overviewConditionalResponse.Header.Get("ETag") != overviewETag {
		t.Fatalf(
			"conditional Control Overview status=%d ETag=%q body=%q",
			overviewConditionalResponse.StatusCode,
			overviewConditionalResponse.Header.Get("ETag"),
			overviewConditionalBody,
		)
	}
	assertControlServeE2ESecurityHeadersV1(t, overviewConditionalResponse.Header)

	modulesRequest, err := http.NewRequest(
		http.MethodGet,
		handoff.Origin+controlhttp.ModulesPathV1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	setControlServeE2ETenantScopeV1(modulesRequest)
	modulesRequest.Header.Set(controlhttp.CSRFHeaderV1, bootstrap.CSRFToken)
	modulesRequest.AddCookie(sessionCookie)
	modulesResponse, err := client.Do(modulesRequest)
	if err != nil {
		t.Fatalf("GET production Control modules: %v", err)
	}
	modulesBody := readControlServeE2EResponseV1(t, modulesResponse)
	if modulesResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"Control modules status=%d body=%s",
			modulesResponse.StatusCode,
			modulesBody,
		)
	}
	var modules controlServeE2EModulesPageV1
	decodeControlServeE2EJSONV1(t, modulesBody, &modules)
	if modules.SchemaVersion != controlhttp.ModulesHTTPPageSchemaV1 ||
		modules.PublishedPointer.Validate() != nil ||
		modules.PublishedPointer.ResourceID != defaultTenantID ||
		modules.PublishedPointer.Revision != basisBefore.PointerRevision {
		t.Fatalf("invalid Control modules page=%+v", modules)
	}
	foundInstance := false
	for _, item := range modules.Items {
		if item.InstanceID == fixture.InstanceID {
			foundInstance = true
			break
		}
	}
	if !foundInstance {
		t.Fatalf("Control modules page omitted target instance %q: %+v", fixture.InstanceID, modules.Items)
	}
	detailURL := handoff.Origin + controlhttp.ModulesPathV1 + "/" + fixture.InstanceID
	detailRequest, err := http.NewRequest(http.MethodGet, detailURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	setControlServeE2ETenantScopeV1(detailRequest)
	detailRequest.Header.Set(controlhttp.CSRFHeaderV1, bootstrap.CSRFToken)
	detailRequest.AddCookie(sessionCookie)
	detailResponse, err := client.Do(detailRequest)
	if err != nil {
		t.Fatalf("GET production Control module detail: %v", err)
	}
	detailBody := readControlServeE2EResponseV1(t, detailResponse)
	if detailResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"Control module detail status=%d body=%s",
			detailResponse.StatusCode,
			detailBody,
		)
	}
	detailETag := detailResponse.Header.Get("ETag")
	var detail controlapp.ModuleDetailResultV1
	decodeControlServeE2EJSONV1(t, detailBody, &detail)
	if detail.SchemaVersion != controlapp.ModuleDetailSchemaVersionV1 ||
		detail.PublishedPointer != modules.PublishedPointer ||
		detail.Module.Summary.InstanceID != fixture.InstanceID ||
		detail.Module.Summary.ModuleID != fixture.ModuleID ||
		len(detail.Module.Bindings) != 1 || detailETag == "" ||
		!strings.HasPrefix(detailETag, `"`) ||
		strings.Contains(string(detailBody), artifactRoot) ||
		strings.Contains(string(detailBody), fixture.ArtifactDirectory) {
		t.Fatalf("unsafe production Control module detail=%+v ETag=%q", detail, detailETag)
	}
	notModifiedRequest, err := http.NewRequest(http.MethodGet, detailURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	setControlServeE2ETenantScopeV1(notModifiedRequest)
	notModifiedRequest.Header.Set(controlhttp.CSRFHeaderV1, bootstrap.CSRFToken)
	notModifiedRequest.Header.Set("If-None-Match", detailETag)
	notModifiedRequest.AddCookie(sessionCookie)
	notModifiedResponse, err := client.Do(notModifiedRequest)
	if err != nil {
		t.Fatalf("GET conditional production Control module detail: %v", err)
	}
	notModifiedBody := readControlServeE2EResponseV1(t, notModifiedResponse)
	conditionalETag := notModifiedResponse.Header.Get("ETag")
	// Production observations carry a fresh observed_at value, so a second
	// representation is byte-distinct even when the published source is
	// unchanged. A strong If-None-Match must therefore not manufacture a 304.
	// The fixed-clock transport test covers the exact 304 branch.
	if notModifiedResponse.StatusCode != http.StatusOK ||
		len(notModifiedBody) == 0 ||
		len(conditionalETag) != 66 || conditionalETag[0] != '"' || conditionalETag[65] != '"' ||
		!moduleapi.ValidSHA256(conditionalETag[1:65]) || conditionalETag == detailETag {
		t.Fatalf(
			"conditional module detail status=%d body=%q",
			notModifiedResponse.StatusCode,
			notModifiedBody,
		)
	}

	_, disableCanonical, _, err := controlapp.NewModuleDisableDryRunBodyV1(
		controlapp.ModuleDisableDryRunBodyV1{
			SchemaVersion:           controlapp.ModuleDisableDryRunBodySchemaVersionV1,
			ExpectedPointerRevision: modules.PublishedPointer.Revision,
			BindingTarget: controlapp.ModuleBindingTargetV1{
				Kind:      controlapp.ModuleBindingTargetProfileV1,
				ProfileID: moduleApplyTestProfileID,
			},
			InstanceID: fixture.InstanceID,
			Port:       productionContextPort,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	disableRequest, err := http.NewRequest(
		http.MethodPost,
		handoff.Origin+controlhttp.ModuleDisableDryRunPathV1,
		bytes.NewReader(disableCanonical),
	)
	if err != nil {
		t.Fatal(err)
	}
	disableRequest.Header.Set("Content-Type", "application/json")
	disableRequest.Header.Set("Origin", handoff.Origin)
	disableRequest.Header.Set(controlhttp.CSRFHeaderV1, bootstrap.CSRFToken)
	disableRequest.Header.Set("If-Match", `"`+modules.PublishedPointer.Digest+`"`)
	setControlServeE2ETenantScopeV1(disableRequest)
	disableRequest.AddCookie(sessionCookie)
	disableResponse, err := client.Do(disableRequest)
	if err != nil {
		t.Fatalf("POST production Control MODULE_DISABLE Dry-run: %v", err)
	}
	disableBody := readControlServeE2EResponseV1(t, disableResponse)
	if disableResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"Control MODULE_DISABLE Dry-run status=%d body=%s",
			disableResponse.StatusCode,
			disableBody,
		)
	}
	var dryRun controlapp.ModuleDisableDryRunResultV1
	decodeControlServeE2EJSONV1(t, disableBody, &dryRun)
	if dryRun.SchemaVersion != controlapp.ModuleDisableDryRunResultSchemaVersionV1 ||
		dryRun.Projection.Disposition != controlapp.ModuleDisableWouldApplyV1 ||
		dryRun.Projection.CandidateState != controlapp.ModuleDisableProjectedNotReservedV1 ||
		dryRun.Projection.PreconditionBasis.PointerRevision != basisBefore.PointerRevision ||
		dryRun.Receipt.Status != controlapicontract.OperationStatusDryRunV1 ||
		dryRun.Receipt.PostRef != nil || dryRun.Receipt.DomainReceipt != nil {
		t.Fatalf("unexpected production Control Dry-run result=%+v", dryRun)
	}

	stopServe()
	select {
	case err := <-serveDone:
		serveRunning = false
		if err != nil {
			t.Fatalf("Control serve shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Control serve did not shut down")
	}

	basisAfter := loadControlServeE2EPublishedBasisV1(t, databasePath)
	countsAfter := snapshotControlServeE2ETableCountsV1(t, databasePath)
	artifactsAfter := snapshotModuleDryRunTreeV1(t, artifactRoot)
	if basisAfter != basisBefore {
		t.Fatalf("Control Dry-run changed PublishedBasis: before=%+v after=%+v", basisBefore, basisAfter)
	}
	if !reflect.DeepEqual(countsAfter, countsBefore) {
		t.Fatalf("Control Dry-run changed table counts: before=%v after=%v", countsBefore, countsAfter)
	}
	if !reflect.DeepEqual(artifactsAfter, artifactsBefore) {
		t.Fatalf("Control Dry-run changed artifact tree: before=%+v after=%+v", artifactsBefore, artifactsAfter)
	}
	if _, err := os.Lstat(handoffPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Control handoff survived bootstrap and shutdown: %v", err)
	}
}

func TestDefaultOffServeHasNoControlHandoffOrRouteV1(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	handoffPath := filepath.Join(root, controlhandoff.DefaultFilenameV1)

	originalVerifier := verifyControlNonElevatedV1
	verifierCalls := 0
	verifyControlNonElevatedV1 = func() error {
		verifierCalls++
		return errors.New("default-off must not inspect Control privilege")
	}
	defer func() { verifyControlNonElevatedV1 = originalVerifier }()
	originalConfirmationConstructor := newControlConfirmationRegistryV1
	confirmationConstructorCalls := 0
	newControlConfirmationRegistryV1 = func(
		controlconfirmation.RegistryConfigV1,
	) (*controlconfirmation.RegistryV1, error) {
		confirmationConstructorCalls++
		return nil, errors.New("default-off must not initialize confirmation")
	}
	defer func() {
		newControlConfirmationRegistryV1 = originalConfirmationConstructor
	}()
	originalOverviewConstructor := newControlOverviewServiceV1
	overviewConstructorCalls := 0
	newControlOverviewServiceV1 = func(
		*currentstore.Store,
	) (controlhttp.OverviewServiceV1, error) {
		overviewConstructorCalls++
		return nil, errors.New("default-off must not initialize Overview")
	}
	defer func() { newControlOverviewServiceV1 = originalOverviewConstructor }()
	originalStaticConstructor := newControlStaticAssetResolverV1
	staticConstructorCalls := 0
	newControlStaticAssetResolverV1 = func() (
		controlhttp.StaticAssetResolverV1,
		error,
	) {
		staticConstructorCalls++
		return nil, errors.New("default-off must not initialize static assets")
	}
	defer func() { newControlStaticAssetResolverV1 = originalStaticConstructor }()

	serveContext, stopServe := context.WithCancel(context.Background())
	stdoutReader, stdoutWriter := io.Pipe()
	serveDone := make(chan error, 1)
	go func() {
		err := run(
			serveContext,
			[]string{
				"serve",
				"--db", databasePath,
				"--artifact-root", artifactRoot,
				"--listen", "127.0.0.1:0",
			},
			stdoutWriter,
			io.Discard,
		)
		_ = stdoutWriter.CloseWithError(err)
		serveDone <- err
	}()
	defer func() {
		stopServe()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Errorf("default-off serve shutdown: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("default-off serve did not shut down")
		}
	}()

	var ready map[string]string
	if err := json.NewDecoder(stdoutReader).Decode(&ready); err != nil {
		t.Fatalf("decode default-off readiness: %v", err)
	}
	if ready["status"] != "ready" || ready["listen"] == "" {
		t.Fatalf("default-off readiness=%v", ready)
	}
	if _, present := ready["control"]; present {
		t.Fatalf("default-off readiness exposed Control: %v", ready)
	}
	if verifierCalls != 0 {
		t.Fatalf("default-off serve performed %d Control privilege checks", verifierCalls)
	}
	if confirmationConstructorCalls != 0 {
		t.Fatalf(
			"default-off serve initialized confirmation %d times",
			confirmationConstructorCalls,
		)
	}
	if overviewConstructorCalls != 0 || staticConstructorCalls != 0 {
		t.Fatalf(
			"default-off initialized optional Web dependencies: Overview=%d static=%d",
			overviewConstructorCalls,
			staticConstructorCalls,
		)
	}
	if _, err := os.Lstat(handoffPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default-off serve touched Control handoff: %v", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	health, err := client.Get("http://" + ready["listen"] + "/healthz")
	if err != nil {
		t.Fatalf("default-off Chat listener: %v", err)
	}
	healthBody := readControlServeE2EResponseV1(t, health)
	if health.StatusCode != http.StatusOK {
		t.Fatalf("default-off health status=%d body=%s", health.StatusCode, healthBody)
	}
	controlRequest, err := http.NewRequest(
		http.MethodPost,
		"http://"+ready["listen"]+controlhttp.BootstrapPathV1,
		strings.NewReader(`{}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	controlResponse, err := client.Do(controlRequest)
	if err != nil {
		t.Fatalf("probe default-off Control route: %v", err)
	}
	controlBody := readControlServeE2EResponseV1(t, controlResponse)
	if controlResponse.StatusCode != http.StatusNotFound ||
		len(controlResponse.Cookies()) != 0 {
		t.Fatalf(
			"default-off Control probe status=%d cookies=%v body=%s",
			controlResponse.StatusCode,
			controlResponse.Cookies(),
			controlBody,
		)
	}
	resumeRequest, err := http.NewRequest(
		http.MethodPost,
		"http://"+ready["listen"]+controlhttp.SessionResumePathV1,
		strings.NewReader(`{"resume_credential":"","schema_version":"control-session-resume/v1"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	resumeRequest.Header.Set("Content-Type", "application/json")
	resumeRequest.Header.Set("Origin", "http://"+ready["listen"])
	resumeResponse, err := client.Do(resumeRequest)
	if err != nil {
		t.Fatalf("probe default-off Control session route: %v", err)
	}
	resumeBody := readControlServeE2EResponseV1(t, resumeResponse)
	if resumeResponse.StatusCode != http.StatusNotFound ||
		len(resumeResponse.Cookies()) != 0 || resumeResponse.Header.Get("Set-Cookie") != "" {
		t.Fatalf(
			"default-off Control session probe status=%d cookies=%v body=%s",
			resumeResponse.StatusCode,
			resumeResponse.Cookies(),
			resumeBody,
		)
	}
	for _, path := range []string{
		controlhttp.StaticUIRootPathV1,
		controlhttp.OverviewPathV1,
	} {
		response, err := client.Get("http://" + ready["listen"] + path)
		if err != nil {
			t.Fatalf("probe default-off route %s: %v", path, err)
		}
		body := readControlServeE2EResponseV1(t, response)
		if response.StatusCode != http.StatusNotFound ||
			response.Header.Get("Set-Cookie") != "" {
			t.Fatalf(
				"default-off route %s status=%d cookies=%v body=%s",
				path,
				response.StatusCode,
				response.Cookies(),
				body,
			)
		}
	}
	if overviewConstructorCalls != 0 || staticConstructorCalls != 0 {
		t.Fatalf(
			"default-off route probes accessed optional Web dependencies: Overview=%d static=%d",
			overviewConstructorCalls,
			staticConstructorCalls,
		)
	}
}

func TestControlHandoffCollisionFailsClosedAndClosesStoreV1(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	basisBefore := loadControlServeE2EPublishedBasisV1(t, databasePath)
	handoffPath := filepath.Join(root, "occupied-control-bootstrap.json")
	collision := []byte("owner sentinel: do not replace")
	if err := os.WriteFile(handoffPath, collision, 0o600); err != nil {
		t.Fatal(err)
	}

	originalVerifier := verifyControlNonElevatedV1
	verifyControlNonElevatedV1 = func() error { return nil }
	defer func() { verifyControlNonElevatedV1 = originalVerifier }()
	installControlServeE2EHandoffPublisherV1(t, handoffPath)

	var stdout bytes.Buffer
	err := run(
		context.Background(),
		[]string{
			"serve",
			"--db", databasePath,
			"--artifact-root", artifactRoot,
			"--listen", "127.0.0.1:0",
			"--enable-control",
			"--control-handoff-path", handoffPath,
		},
		&stdout,
		io.Discard,
	)
	if !errors.Is(err, controlhandoff.ErrTargetExists) {
		t.Fatalf("handoff collision error=%v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("handoff collision opened admission/readiness: %q", stdout.String())
	}
	gotCollision, readErr := os.ReadFile(handoffPath)
	if readErr != nil || !bytes.Equal(gotCollision, collision) {
		t.Fatalf("handoff collision replaced owner file: content=%q err=%v", gotCollision, readErr)
	}
	basisAfter := loadControlServeE2EPublishedBasisV1(t, databasePath)
	if basisAfter != basisBefore {
		t.Fatalf("handoff collision changed PublishedBasis: before=%+v after=%+v", basisBefore, basisAfter)
	}
}

func assertControlServeE2EStaticWebV1(
	t *testing.T,
	client *http.Client,
	origin string,
) {
	t.Helper()
	if client == nil || origin == "" {
		t.Fatal("static Web E2E input is incomplete")
	}
	routes := []struct {
		urlPath   string
		assetPath string
	}{
		{controlhttp.StaticUIRootPathV1, "index.html"},
		{controlhttp.StaticUIIndexPathV1, "index.html"},
		{controlhttp.StaticUIAppCSSPathV1, "assets/app.css"},
		{controlhttp.StaticUIAppJSPathV1, "assets/app.js"},
		{controlhttp.StaticUIReactJSPathV1, "assets/react.js"},
		{controlhttp.StaticUITanStackQueryJSPathV1, "assets/tanstack-query.js"},
	}
	for _, route := range routes {
		expected, found, err := controlweb.ResolveAsset(route.assetPath)
		if err != nil || !found {
			t.Fatalf("resolve expected production asset %q: found=%t err=%v", route.assetPath, found, err)
		}
		response, err := client.Get(origin + route.urlPath)
		if err != nil {
			t.Fatalf("GET production Web asset %s: %v", route.urlPath, err)
		}
		body := readControlServeE2EResponseV1(t, response)
		if response.StatusCode != http.StatusOK || !bytes.Equal(body, expected.Bytes) {
			t.Fatalf("Web asset %s status=%d body bytes=%d want=%d", route.urlPath, response.StatusCode, len(body), len(expected.Bytes))
		}
		assertControlServeE2EStaticHeadersV1(t, response.Header, expected)

		headRequest, err := http.NewRequest(http.MethodHead, origin+route.urlPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		headResponse, err := client.Do(headRequest)
		if err != nil {
			t.Fatalf("HEAD production Web asset %s: %v", route.urlPath, err)
		}
		headBody := readControlServeE2EResponseV1(t, headResponse)
		if headResponse.StatusCode != http.StatusOK || len(headBody) != 0 {
			t.Fatalf("HEAD Web asset %s status=%d body=%q", route.urlPath, headResponse.StatusCode, headBody)
		}
		assertControlServeE2EStaticHeadersV1(t, headResponse.Header, expected)
		for _, name := range []string{
			"Cache-Control", "Content-Security-Policy", "Cross-Origin-Opener-Policy",
			"Cross-Origin-Resource-Policy", "Permissions-Policy", "Referrer-Policy",
			"X-Content-Type-Options", "X-Frame-Options", "Content-Type",
			"Content-Length", "ETag",
		} {
			if response.Header.Get(name) != headResponse.Header.Get(name) {
				t.Fatalf("Web asset %s GET/HEAD %s mismatch", route.urlPath, name)
			}
		}

		conditionalRequest, err := http.NewRequest(http.MethodGet, origin+route.urlPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		conditionalRequest.Header.Set("If-None-Match", response.Header.Get("ETag"))
		conditionalResponse, err := client.Do(conditionalRequest)
		if err != nil {
			t.Fatalf("conditional GET production Web asset %s: %v", route.urlPath, err)
		}
		conditionalBody := readControlServeE2EResponseV1(t, conditionalResponse)
		if conditionalResponse.StatusCode != http.StatusNotModified ||
			len(conditionalBody) != 0 ||
			conditionalResponse.Header.Get("ETag") != response.Header.Get("ETag") ||
			conditionalResponse.Header.Get("Content-Encoding") != "" ||
			conditionalResponse.Header.Get("Accept-Ranges") != "" {
			t.Fatalf("conditional Web asset %s status=%d ETag=%q body=%q", route.urlPath, conditionalResponse.StatusCode, conditionalResponse.Header.Get("ETag"), conditionalBody)
		}
		assertControlServeE2ESecurityHeadersV1(t, conditionalResponse.Header)
	}

	plainResponse, err := client.Get(origin + controlhttp.StaticUIIndexPathV1)
	if err != nil {
		t.Fatal(err)
	}
	plainBody := readControlServeE2EResponseV1(t, plainResponse)
	cookieRequest, err := http.NewRequest(
		http.MethodGet,
		origin+controlhttp.StaticUIIndexPathV1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	cookieRequest.Header.Set("Cookie", "ambient=value; theme=dark")
	cookieResponse, err := client.Do(cookieRequest)
	if err != nil {
		t.Fatal(err)
	}
	cookieBody := readControlServeE2EResponseV1(t, cookieResponse)
	if plainResponse.StatusCode != http.StatusOK || cookieResponse.StatusCode != http.StatusOK ||
		!bytes.Equal(plainBody, cookieBody) ||
		plainResponse.Header.Get("ETag") != cookieResponse.Header.Get("ETag") ||
		cookieResponse.Header.Get("Set-Cookie") != "" {
		t.Fatalf("ambient Cookie changed public shell representation")
	}

	for _, path := range []string{
		"/control/ui",
		"/control/ui/assets/../index.html",
		"/control/ui/assets/app.js.map",
		"/control/ui/missing",
	} {
		request, err := http.NewRequest(http.MethodGet, origin+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("probe Web sibling %s: %v", path, err)
		}
		body := readControlServeE2EResponseV1(t, response)
		if response.StatusCode != http.StatusNotFound || response.Header.Get("Set-Cookie") != "" {
			t.Fatalf("Web sibling %s status=%d body=%s", path, response.StatusCode, body)
		}
		assertControlServeE2ESecurityHeadersV1(t, response.Header)
	}
}

func assertControlServeE2EStaticHeadersV1(
	t *testing.T,
	header http.Header,
	asset controlweb.Asset,
) {
	t.Helper()
	assertControlServeE2ESecurityHeadersV1(t, header)
	wants := map[string]string{
		"Content-Type":   asset.MediaType,
		"Content-Length": strconv.Itoa(len(asset.Bytes)),
		"ETag":           `"` + asset.SHA256 + `"`,
	}
	for name, want := range wants {
		if got := header.Get(name); got != want {
			t.Errorf("production Web %s=%q want=%q", name, got, want)
		}
	}
	for _, name := range []string{
		"Access-Control-Allow-Origin", "Accept-Ranges", "Content-Encoding",
		"Transfer-Encoding",
	} {
		if got := header.Get(name); got != "" {
			t.Errorf("production Web unexpected %s=%q", name, got)
		}
	}
}

func assertControlServeE2ESecurityHeadersV1(t *testing.T, header http.Header) {
	t.Helper()
	const csp = "default-src 'self'; script-src 'self'; style-src 'self'; " +
		"connect-src 'self'; img-src 'self' data:; object-src 'none'; " +
		"base-uri 'none'; frame-ancestors 'none'; form-action 'none'; font-src 'none'; " +
		"worker-src 'none'; child-src 'none'; manifest-src 'none'; media-src 'none'"
	wants := map[string]string{
		"Cache-Control":                "no-store",
		"Content-Security-Policy":      csp,
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Permissions-Policy":           "camera=(), geolocation=(), microphone=(), payment=(), usb=()",
		"Referrer-Policy":              "no-referrer",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
	}
	for name, want := range wants {
		if got := header.Get(name); got != want {
			t.Errorf("production Control %s=%q want=%q", name, got, want)
		}
	}
	if got := header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("production Control emitted CORS header %q", got)
	}
}

func setControlServeE2ETenantScopeV1(request *http.Request) {
	request.Header.Set(controlhttp.ScopeKindHeaderV1, string(controlapicontract.ScopeTenantV1))
	request.Header.Set(controlhttp.TenantIDHeaderV1, defaultTenantID)
}

func readControlServeE2EResponseV1(t *testing.T, response *http.Response) []byte {
	t.Helper()
	if response == nil || response.Body == nil {
		t.Fatal("HTTP response is incomplete")
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	return body
}

func decodeControlServeE2EJSONV1(t *testing.T, input []byte, output any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		t.Fatalf("decode exact JSON: %v\n%s", err, input)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON has trailing value: %v\n%s", err, input)
	}
}

func decodeControlServeE2ESecretJSONV1(t *testing.T, input []byte, output any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		t.Fatalf("decode secret-bearing exact JSON: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("secret-bearing JSON has trailing value: %v", err)
	}
}

func waitForControlServeE2EAbsentV1(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			t.Fatalf("inspect Control handoff cleanup: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("Control handoff was not removed after bootstrap: %q", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type controlServeE2EFakeHandoffV1 struct {
	path string
	once sync.Once
	err  error
}

func (handoff *controlServeE2EFakeHandoffV1) Cleanup() error {
	if handoff == nil {
		return nil
	}
	handoff.once.Do(func() {
		err := os.Remove(handoff.path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			handoff.err = err
		}
	})
	return handoff.err
}

func installControlServeE2EHandoffPublisherV1(t *testing.T, path string) {
	t.Helper()
	original := publishControlHandoffV1
	publishControlHandoffV1 = func(
		ctx context.Context,
		config controlhandoff.ConfigV1,
	) (controlHandoffCleanupV1, error) {
		if ctx == nil || ctx.Err() != nil || config.HandoffPath != path ||
			config.RuntimeDirectory != "" || config.Origin == "" ||
			len(config.Material.Capability) != controlsession.CredentialBytesV1 ||
			config.Material.BootID == "" ||
			config.Material.ExpiresAtUnixMicros <= uint64(time.Now().UTC().UnixMicro()) {
			return nil, controlhandoff.ErrInvalidInput
		}
		raw, err := json.Marshal(controlServeE2EHandoffV1{
			SchemaVersion: controlhandoff.SchemaVersionV1,
			Origin:        config.Origin,
			Capability: base64.RawURLEncoding.EncodeToString(
				config.Material.Capability,
			),
			ExpiresAtUnixMicros: config.Material.ExpiresAtUnixMicros,
		})
		if err != nil {
			return nil, err
		}
		canonical, err := moduleapi.CanonicalJSON(raw)
		clear(raw)
		if err != nil {
			return nil, err
		}
		defer clear(canonical)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			return nil, controlhandoff.ErrTargetExists
		}
		if err != nil {
			return nil, err
		}
		remove := true
		defer func() {
			if remove {
				_ = os.Remove(path)
			}
		}()
		written, writeErr := file.Write(canonical)
		if writeErr == nil && written != len(canonical) {
			writeErr = io.ErrShortWrite
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return nil, err
		}
		remove = false
		return &controlServeE2EFakeHandoffV1{path: path}, nil
	}
	t.Cleanup(func() { publishControlHandoffV1 = original })
}

func loadControlServeE2EPublishedBasisV1(
	t *testing.T,
	databasePath string,
) controlapicontract.PublishedBasisRefV1 {
	t.Helper()
	store, err := currentstore.OpenExistingCurrentStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open Store for PublishedBasis: %v", err)
	}
	basis, _, _, loadErr := store.LoadPublishedBasis(context.Background(), defaultTenantID)
	closeErr := store.Close()
	if err := errors.Join(loadErr, closeErr); err != nil {
		t.Fatalf("load PublishedBasis: %v", err)
	}
	return controlapp.PublishedBasisRefV1(basis)
}

func snapshotControlServeE2ETableCountsV1(
	t *testing.T,
	databasePath string,
) map[string]int64 {
	t.Helper()
	database, err := sql.Open("sqlite", crashSQLiteURI(databasePath, "ro"))
	if err != nil {
		t.Fatalf("open Store for table counts: %v", err)
	}
	defer database.Close()
	rows, err := database.Query(`
		SELECT name
		FROM sqlite_schema
		WHERE type='table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name
	`)
	if err != nil {
		t.Fatalf("list ordinary Store tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	rowsErr := rows.Err()
	closeErr := rows.Close()
	if err := errors.Join(rowsErr, closeErr); err != nil {
		t.Fatalf("read ordinary Store tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("Store has no ordinary tables")
	}
	counts := make(map[string]int64, len(tables))
	for _, table := range tables {
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		var count int64
		if err := database.QueryRow("SELECT COUNT(*) FROM " + quoted).Scan(&count); err != nil {
			t.Fatalf("count Store table %q: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}
