package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlapicontract"
	"github.com/endview/freeagent/internal/controlapp"
	"github.com/endview/freeagent/internal/controlhttp"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type controlMutationE2EConfirmationV1 struct {
	SchemaVersion string `json:"schema_version"`

	Request       controlapicontract.ControlOperationRequestV1 `json:"request"`
	RequestDigest string                                       `json:"request_digest"`

	Evaluation       controlapp.ModuleDisableEvaluationV1 `json:"evaluation"`
	EvaluationDigest string                               `json:"evaluation_digest"`

	Statement       controlapicontract.ControlConfirmationStatementV1 `json:"statement"`
	StatementDigest string                                            `json:"statement_digest"`

	ConfirmationProof   string `json:"confirmation_proof"`
	ExpiresAtUnixMicros uint64 `json:"expires_at_unix_micros"`
}

type runningControlMutationE2EV1 struct {
	origin  string
	client  *http.Client
	cookie  *http.Cookie
	csrf    string
	cancel  context.CancelFunc
	done    <-chan error
	stopped bool
}

func TestControlEnabledServeProductionModuleDisableMutationV1(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initializePureChatForModuleApplyV1(t, databasePath, artifactRoot)
	fixture := newModuleApplyDeclarativeFixtureV1(
		t,
		moduleApplyRoleID,
		moduleApplyRoleInstance,
	)
	enablePlan := newEnabledDeclarativeModuleApplyPlanV1(t, fixture, 1, 0)
	eligibleEnablePlan := bytes.Replace(
		enablePlan,
		[]byte(`"failure_policy":"REQUIRED"`),
		[]byte(`"failure_policy":"OPTIONAL"`),
		1,
	)
	if bytes.Equal(eligibleEnablePlan, enablePlan) {
		t.Fatal("failed to construct narrow OPTIONAL Control mutation fixture")
	}
	enablePath := writeModuleApplyPlanFixtureV1(
		t,
		filepath.Join(root, "enable-role.json"),
		eligibleEnablePlan,
	)
	if result, err := runModuleApplyFixtureV1(
		databasePath,
		artifactRoot,
		enablePath,
		fixture.ArtifactDirectory,
		"",
	); err != nil || result.Status != moduleApplyStatusApplied {
		t.Fatalf("prepare Control mutation: result=%+v err=%v", result, err)
	}

	originalVerifier := verifyControlNonElevatedV1
	verifyControlNonElevatedV1 = func() error { return nil }
	t.Cleanup(func() { verifyControlNonElevatedV1 = originalVerifier })

	basisBefore := loadControlServeE2EPublishedBasisV1(t, databasePath)
	pointerBefore := controlMutationE2EPointerV1(t, basisBefore)
	countsBefore := snapshotControlServeE2ETableCountsV1(t, databasePath)
	artifactsBefore := snapshotModuleDryRunTreeV1(t, artifactRoot)
	bodyBefore := controlMutationE2EBodyV1(
		t,
		pointerBefore.Revision,
		fixture.InstanceID,
	)

	first := startControlMutationE2EServeV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "control-bootstrap-first.json"),
	)
	assertControlMutationSiblingNotFoundV1(t, first)

	const appliedKey = "freeagent-control-e2e-applied-key-0001"
	confirmationBody := postControlMutationE2EV1(
		t,
		first,
		controlhttp.ModuleDisableConfirmationPathV1,
		bodyBefore,
		pointerBefore,
		appliedKey,
		"",
		"",
	)
	var confirmation controlMutationE2EConfirmationV1
	decodeControlServeE2EJSONV1(t, confirmationBody, &confirmation)
	if confirmation.SchemaVersion !=
		controlhttp.ModuleDisableConfirmationResultSchemaVersionV1 ||
		confirmation.Evaluation.Projection.Disposition !=
			controlapp.ModuleDisableWouldApplyV1 ||
		confirmation.Request.ExpectedRef != pointerBefore ||
		confirmation.Request.Operation != controlapicontract.OperationModuleDisableV1 ||
		confirmation.Request.Intent != controlapicontract.OperationIntentMutateV1 ||
		confirmation.Request.OperationEvaluationDigest != confirmation.EvaluationDigest ||
		confirmation.Request.ConfirmationDigest != confirmation.StatementDigest ||
		confirmation.ConfirmationProof == "" {
		t.Fatalf("unexpected production confirmation=%+v", confirmation)
	}
	if got := getControlMutationE2EPointerV1(t, first); got != pointerBefore {
		t.Fatalf("confirmation changed Published Pointer: got=%+v want=%+v", got, pointerBefore)
	}
	countsAfterConfirmation := snapshotControlServeE2ETableCountsV1(t, databasePath)
	if !reflect.DeepEqual(countsAfterConfirmation, countsBefore) {
		t.Fatalf(
			"confirmation changed table counts: before=%v after=%v",
			countsBefore,
			countsAfterConfirmation,
		)
	}
	if got := snapshotModuleDryRunTreeV1(t, artifactRoot); !reflect.DeepEqual(got, artifactsBefore) {
		t.Fatalf("confirmation changed artifact tree: before=%+v after=%+v", artifactsBefore, got)
	}

	appliedBody := postControlMutationE2EV1(
		t,
		first,
		controlhttp.ModuleDisableMutatePathV1,
		bodyBefore,
		pointerBefore,
		appliedKey,
		confirmation.EvaluationDigest,
		confirmation.ConfirmationProof,
	)
	confirmation.ConfirmationProof = ""
	var applied controlhttp.MutateModuleDisableResultV1
	decodeControlServeE2EJSONV1(t, appliedBody, &applied)
	if applied.SchemaVersion != controlhttp.ModuleDisableMutationResultSchemaVersionV1 ||
		applied.Receipt.Status != controlapicontract.OperationStatusAppliedV1 ||
		applied.Receipt.PreRef == nil || *applied.Receipt.PreRef != pointerBefore ||
		applied.Receipt.PostRef == nil ||
		applied.Receipt.DomainReceipt == nil ||
		applied.Receipt.ReplayDisposition != controlapicontract.ReplayReturnExactReceiptV1 {
		t.Fatalf("unexpected APPLIED response=%+v", applied)
	}
	pointerAfterApplied := *applied.Receipt.PostRef
	if got := getControlMutationE2EPointerV1(t, first); got != pointerAfterApplied {
		t.Fatalf("APPLIED pointer=%+v want=%+v", got, pointerAfterApplied)
	}
	countsAfterApplied := snapshotControlServeE2ETableCountsV1(t, databasePath)

	sameProcessRetry := postControlMutationE2EV1(
		t,
		first,
		controlhttp.ModuleDisableMutatePathV1,
		bodyBefore,
		pointerBefore,
		appliedKey,
		confirmation.EvaluationDigest,
		"",
	)
	if !bytes.Equal(sameProcessRetry, appliedBody) {
		t.Fatalf(
			"same-process exact retry changed response:\nfirst=%s\nretry=%s",
			appliedBody,
			sameProcessRetry,
		)
	}
	if got := snapshotControlServeE2ETableCountsV1(t, databasePath); !reflect.DeepEqual(got, countsAfterApplied) {
		t.Fatalf("same-process exact retry changed table counts: before=%v after=%v", countsAfterApplied, got)
	}
	first.stopV1(t)

	second := startControlMutationE2EServeV1(
		t,
		databasePath,
		artifactRoot,
		filepath.Join(root, "control-bootstrap-second.json"),
	)
	restartRetry := postControlMutationE2EV1(
		t,
		second,
		controlhttp.ModuleDisableMutatePathV1,
		bodyBefore,
		pointerBefore,
		appliedKey,
		confirmation.EvaluationDigest,
		"",
	)
	if !bytes.Equal(restartRetry, appliedBody) {
		t.Fatalf(
			"new-session restart retry changed exact receipt:\nfirst=%s\nretry=%s",
			appliedBody,
			restartRetry,
		)
	}
	if got := snapshotControlServeE2ETableCountsV1(t, databasePath); !reflect.DeepEqual(got, countsAfterApplied) {
		t.Fatalf("restart exact retry changed table counts: before=%v after=%v", countsAfterApplied, got)
	}

	bodyAfterApplied := controlMutationE2EBodyV1(
		t,
		pointerAfterApplied.Revision,
		fixture.InstanceID,
	)
	const noChangeKey = "freeagent-control-e2e-no-change-key-0001"
	noChangeConfirmationBody := postControlMutationE2EV1(
		t,
		second,
		controlhttp.ModuleDisableConfirmationPathV1,
		bodyAfterApplied,
		pointerAfterApplied,
		noChangeKey,
		"",
		"",
	)
	var noChangeConfirmation controlMutationE2EConfirmationV1
	decodeControlServeE2EJSONV1(t, noChangeConfirmationBody, &noChangeConfirmation)
	if noChangeConfirmation.Evaluation.Projection.Disposition !=
		controlapp.ModuleDisableNoChangeV1 ||
		noChangeConfirmation.ConfirmationProof == "" {
		t.Fatalf("unexpected NO_CHANGE confirmation=%+v", noChangeConfirmation)
	}
	if got := snapshotControlServeE2ETableCountsV1(t, databasePath); !reflect.DeepEqual(got, countsAfterApplied) {
		t.Fatalf("NO_CHANGE confirmation wrote Store: before=%v after=%v", countsAfterApplied, got)
	}

	noChangeBody := postControlMutationE2EV1(
		t,
		second,
		controlhttp.ModuleDisableMutatePathV1,
		bodyAfterApplied,
		pointerAfterApplied,
		noChangeKey,
		noChangeConfirmation.EvaluationDigest,
		noChangeConfirmation.ConfirmationProof,
	)
	noChangeConfirmation.ConfirmationProof = ""
	var noChange controlhttp.MutateModuleDisableResultV1
	decodeControlServeE2EJSONV1(t, noChangeBody, &noChange)
	if noChange.Receipt.Status != controlapicontract.OperationStatusNoChangeV1 ||
		noChange.Receipt.PreRef == nil || noChange.Receipt.PostRef == nil ||
		*noChange.Receipt.PreRef != pointerAfterApplied ||
		*noChange.Receipt.PostRef != pointerAfterApplied ||
		noChange.Receipt.DomainReceipt != nil {
		t.Fatalf("unexpected NO_CHANGE response=%+v", noChange)
	}
	if got := getControlMutationE2EPointerV1(t, second); got != pointerAfterApplied {
		t.Fatalf("NO_CHANGE changed Published Pointer: got=%+v want=%+v", got, pointerAfterApplied)
	}
	second.stopV1(t)

	basisAfter := loadControlServeE2EPublishedBasisV1(t, databasePath)
	if got := controlMutationE2EPointerV1(t, basisAfter); got != pointerAfterApplied {
		t.Fatalf("final pointer=%+v want=%+v", got, pointerAfterApplied)
	}
	countsAfter := snapshotControlServeE2ETableCountsV1(t, databasePath)
	assertControlMutationE2ETableDeltasV1(t, countsBefore, countsAfter)
	artifactsAfter := snapshotModuleDryRunTreeV1(t, artifactRoot)
	if !reflect.DeepEqual(artifactsAfter, artifactsBefore) {
		t.Fatalf("Control mutation changed artifact tree: before=%+v after=%+v", artifactsBefore, artifactsAfter)
	}
}

func startControlMutationE2EServeV1(
	t *testing.T,
	databasePath string,
	artifactRoot string,
	handoffPath string,
) *runningControlMutationE2EV1 {
	t.Helper()
	installControlServeE2EHandoffPublisherV1(t, handoffPath)
	lifecycle, cancel := context.WithCancel(context.Background())
	stdoutReader, stdoutWriter := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := run(
			lifecycle,
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
		done <- err
	}()
	serve := &runningControlMutationE2EV1{
		client: &http.Client{Timeout: 10 * time.Second},
		cancel: cancel,
		done:   done,
	}
	t.Cleanup(func() { serve.stopV1(t) })

	var ready map[string]string
	if err := json.NewDecoder(stdoutReader).Decode(&ready); err != nil {
		t.Fatalf("decode Control mutation readiness: %v", err)
	}
	if ready["status"] != "ready" || ready["control"] != "enabled" ||
		ready["listen"] == "" {
		t.Fatalf("Control mutation readiness=%v", ready)
	}
	handoffCanonical, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("read Control mutation handoff: %v", err)
	}
	var handoff controlServeE2EHandoffV1
	decodeControlServeE2ESecretJSONV1(t, handoffCanonical, &handoff)
	serve.origin = handoff.Origin

	bootstrapRaw, err := json.Marshal(map[string]string{
		"schema_version": controlhttp.BootstrapRequestSchemaV1,
		"capability":     handoff.Capability,
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrapCanonical, err := moduleapi.CanonicalJSON(bootstrapRaw)
	clear(bootstrapRaw)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(bootstrapCanonical)
	bootstrapRequest, err := http.NewRequest(
		http.MethodPost,
		serve.origin+controlhttp.BootstrapPathV1,
		bytes.NewReader(bootstrapCanonical),
	)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapRequest.Header.Set("Content-Type", "application/json")
	bootstrapRequest.Header.Set("Origin", serve.origin)
	bootstrapResponse, err := serve.client.Do(bootstrapRequest)
	if err != nil {
		t.Fatalf("bootstrap Control mutation serve: %v", err)
	}
	bootstrapBody := readControlServeE2EResponseV1(t, bootstrapResponse)
	if bootstrapResponse.StatusCode != http.StatusOK {
		t.Fatalf("Control mutation bootstrap status=%d", bootstrapResponse.StatusCode)
	}
	var bootstrap controlServeE2EBootstrapResponseV2
	decodeControlServeE2ESecretJSONV1(t, bootstrapBody, &bootstrap)
	serve.csrf = bootstrap.CSRFToken
	for _, cookie := range bootstrapResponse.Cookies() {
		if cookie.Name == controlhttp.SessionCookieV1 {
			copy := *cookie
			serve.cookie = &copy
		}
	}
	if serve.origin == "" || serve.csrf == "" || serve.cookie == nil {
		t.Fatal("incomplete Control mutation bootstrap response")
	}
	return serve
}

func (serve *runningControlMutationE2EV1) stopV1(t *testing.T) {
	t.Helper()
	if serve == nil || serve.stopped {
		return
	}
	serve.stopped = true
	serve.cancel()
	select {
	case err := <-serve.done:
		if err != nil {
			t.Fatalf("Control mutation serve shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Control mutation serve did not shut down")
	}
}

func controlMutationE2EPointerV1(
	t *testing.T,
	basis controlapicontract.PublishedBasisRefV1,
) controlapicontract.ExpectedResourceRefV1 {
	t.Helper()
	pointer, err := controlapp.PublishedPointerRefV1(basis)
	if err != nil {
		t.Fatal(err)
	}
	return pointer
}

func controlMutationE2EBodyV1(
	t *testing.T,
	revision uint64,
	instanceID string,
) []byte {
	t.Helper()
	_, canonical, _, err := controlapp.NewModuleDisableDryRunBodyV1(
		controlapp.ModuleDisableDryRunBodyV1{
			SchemaVersion:           controlapp.ModuleDisableDryRunBodySchemaVersionV1,
			ExpectedPointerRevision: revision,
			BindingTarget: controlapp.ModuleBindingTargetV1{
				Kind:      controlapp.ModuleBindingTargetProfileV1,
				ProfileID: moduleApplyTestProfileID,
			},
			InstanceID: instanceID,
			Port:       productionContextPort,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func postControlMutationE2EV1(
	t *testing.T,
	serve *runningControlMutationE2EV1,
	path string,
	body []byte,
	expected controlapicontract.ExpectedResourceRefV1,
	idempotencyKey string,
	evaluationDigest string,
	confirmationProof string,
) []byte {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		serve.origin+path,
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", serve.origin)
	request.Header.Set(controlhttp.CSRFHeaderV1, serve.csrf)
	request.Header.Set("If-Match", `"`+expected.Digest+`"`)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	if evaluationDigest != "" {
		request.Header.Set(
			controlhttp.OperationEvaluationDigestHeaderV1,
			evaluationDigest,
		)
	}
	if confirmationProof != "" {
		request.Header.Set(controlhttp.ConfirmationHeaderV1, confirmationProof)
	}
	setControlServeE2ETenantScopeV1(request)
	request.AddCookie(serve.cookie)
	response, err := serve.client.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	responseBody := readControlServeE2EResponseV1(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST %s status=%d body=%s", path, response.StatusCode, responseBody)
	}
	return responseBody
}

func getControlMutationE2EPointerV1(
	t *testing.T,
	serve *runningControlMutationE2EV1,
) controlapicontract.ExpectedResourceRefV1 {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodGet,
		serve.origin+controlhttp.ModulesPathV1,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(controlhttp.CSRFHeaderV1, serve.csrf)
	setControlServeE2ETenantScopeV1(request)
	request.AddCookie(serve.cookie)
	response, err := serve.client.Do(request)
	if err != nil {
		t.Fatalf("GET modules after Control mutation: %v", err)
	}
	body := readControlServeE2EResponseV1(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET modules status=%d body=%s", response.StatusCode, body)
	}
	var page controlServeE2EModulesPageV1
	decodeControlServeE2EJSONV1(t, body, &page)
	return page.PublishedPointer
}

func assertControlMutationSiblingNotFoundV1(
	t *testing.T,
	serve *runningControlMutationE2EV1,
) {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		serve.origin+controlhttp.ModuleDisableMutatePathV1+"/sibling",
		bytes.NewReader([]byte(`{}`)),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", serve.origin)
	response, err := serve.client.Do(request)
	if err != nil {
		t.Fatalf("probe mutation sibling: %v", err)
	}
	body := readControlServeE2EResponseV1(t, response)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("mutation sibling status=%d body=%s", response.StatusCode, body)
	}
}

func assertControlMutationE2ETableDeltasV1(
	t *testing.T,
	before map[string]int64,
	after map[string]int64,
) {
	t.Helper()
	if len(after) != len(before) {
		t.Fatalf("ordinary table set changed: before=%v after=%v", before, after)
	}
	allowed := map[string]int64{
		"control_snapshots":           1,
		"runtime_catalog_generations": 1,
		"control_operation_receipts":  2,
		"overview_basis_snapshots":    1,
		"overview_basis_workspaces":   1,
	}
	for table, beforeCount := range before {
		want := beforeCount + allowed[table]
		if after[table] != want {
			t.Fatalf(
				"table %s count=%d, want %d (before=%d)",
				table,
				after[table],
				want,
				beforeCount,
			)
		}
	}
	for table := range after {
		if _, ok := before[table]; !ok {
			t.Fatalf("unexpected ordinary table %q", table)
		}
	}
}
