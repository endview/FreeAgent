package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentbackup"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/localchat"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/sdk/loopapi"
	"github.com/endview/freeagent/sdk/moduleapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpProductionHelperMarker = "freeagent-mcp-production-helper"

func TestMCPProductionHelperProcess(t *testing.T) {
	mode, eventPath, expectedGORACE, helper := mcpProductionHelperArguments(os.Args)
	if !helper {
		return
	}
	if os.Getenv("GORACE") != expectedGORACE {
		os.Exit(97)
	}
	if runtime.GOOS != "windows" {
		environment := os.Environ()
		if len(environment) != 1 || environment[0] != "GORACE="+expectedGORACE {
			os.Exit(96)
		}
	}
	os.Exit(runMCPProductionHelper(mode, eventPath))
}

func TestProductionMCPCompositionRequiresGrantAndRunsOnceAcrossRestart(
	t *testing.T,
) {
	ctx := context.Background()
	root := t.TempDir()
	eventPath := filepath.Join(root, "mcp-events.log")
	seedPath, artifactDigest := newMCPProductionFixture(
		t,
		filepath.Join(root, "fixture"),
		"success",
		eventPath,
	)

	for name, grants := range map[string][]string{
		"without-grant": nil,
		"wrong-grant":   {strings.Repeat("0", moduleapi.SHA256HexLength)},
	} {
		database := filepath.Join(root, name+".sqlite")
		artifacts := filepath.Join(root, name+"-artifacts")
		if _, err := initializeProductionData(ctx, initInput{
			DatabasePath:           database,
			SeedPath:               seedPath,
			ArtifactRoot:           artifacts,
			LocalMCPArtifactGrants: grants,
		}); err == nil || !strings.Contains(err.Error(), "lacks an explicit operator grant") {
			t.Fatalf("LOCAL_PROCESS initialization %s error=%v", name, err)
		}
		for _, path := range []string{database, artifacts, eventPath} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s rejection left or accessed %s: %v", name, path, err)
			}
		}
	}

	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	initialized, err := initializeProductionData(ctx, initInput{
		DatabasePath:           databasePath,
		SeedPath:               seedPath,
		ArtifactRoot:           artifactRoot,
		LocalMCPArtifactGrants: []string{artifactDigest},
	})
	if err != nil {
		t.Fatalf("initialize MCP production data: %v", err)
	}
	if len(initialized.ArtifactLocks) != 3 || initialized.Defaults.ProfileID != "action-chat" {
		t.Fatalf("MCP initialization=%+v", initialized)
	}
	if _, err := os.Lstat(eventPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initialization started MCP process: %v", err)
	}
	assertNoInitializationResidue(t, root, databasePath)

	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-production-mcp",
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "action-chat",
		Message:     "one MCP tool call only",
		RequestID:   "production-mcp-chain-1",
		Deadline: time.Date(
			2099, time.January, 2, 3, 4, 5, 0, time.UTC,
		),
	}

	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open MCP composition: %v", err)
	}
	registered, err := composition.registry.IsRegistered(
		ctx,
		artifactDigest,
		mcpstdio.AdapterIdentityV1,
	)
	if err != nil || registered {
		_ = composition.Close()
		t.Fatalf("MCP adapter loaded before selected admission=%v, %v", registered, err)
	}
	result, err := composition.chat.Chat(ctx, input)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("MCP Chat: %v", err)
	}
	if !result.AdmissionCreated || result.TerminalResult == nil ||
		result.LoopResult.Disposition != loopapi.DispositionTerminated ||
		result.Reply != "text.stats completed." || result.FailureCode != "" {
		_ = composition.Close()
		t.Fatalf("MCP Chat result=%+v", result)
	}
	terminal := *result.TerminalResult
	if terminal.AttemptKind != corecontract.AttemptKindModel ||
		terminal.State != corecontract.ModelAttemptSucceeded {
		_ = composition.Close()
		t.Fatalf("MCP terminal=%+v", terminal)
	}
	modelTwo, err := composition.store.GetModelDispatchRecord(ctx, terminal.AttemptID)
	if err != nil || modelTwo.Attempt.SourceDispatchAttemptID == "" {
		_ = composition.Close()
		t.Fatalf("MCP second model dispatch=%+v, %v", modelTwo, err)
	}
	modelTwoRequest, err := moduleapi.RestoreModelGenerateRequestV1(
		modelTwo.Attempt.Request.CanonicalBytes,
	)
	if err != nil || len(modelTwoRequest.Messages) == 0 ||
		!strings.HasPrefix(
			modelTwoRequest.Messages[len(modelTwoRequest.Messages)-1].Content,
			corecontract.UntrustedActionResultPrefixV1,
		) {
		_ = composition.Close()
		t.Fatalf("MCP result was not isolated as untrusted model context: %v", err)
	}
	dispatch, err := composition.store.GetActionDispatchRecord(
		ctx,
		modelTwo.Attempt.SourceDispatchAttemptID,
	)
	if err != nil || dispatch.Attempt.State != currentstore.ActionDispatchSucceeded ||
		dispatch.Result == nil || dispatch.ProviderReceipt == nil ||
		dispatch.Attempt.ProviderActionID != "text.stats" ||
		!moduleapi.ValidSHA256(dispatch.Attempt.DefinitionDigest) ||
		dispatch.Attempt.Binding.Provider.ExecutionClass != moduleapi.ExecutionLocalProcess ||
		dispatch.Attempt.Binding.Provider.AdapterIdentity != mcpstdio.AdapterIdentityV1 {
		_ = composition.Close()
		t.Fatalf("MCP dispatch=%+v, %v", dispatch, err)
	}
	var stored struct {
		Status string `json:"status"`
		Result struct {
			SchemaVersion string `json:"schema_version"`
			Content       []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(dispatch.Result.CanonicalBytes, &stored); err != nil ||
		stored.Status != string(corecontract.ActionResultAvailable) ||
		stored.Result.SchemaVersion != "freeagent.mcp-tool-result/v1" ||
		len(stored.Result.Content) != 1 ||
		stored.Result.Content[0].Type != "text" ||
		stored.Result.Content[0].Text != input.Message {
		_ = composition.Close()
		t.Fatalf("stored MCP result=%+v, %v", stored, err)
	}
	var receipt struct {
		SchemaVersion   string `json:"schema_version"`
		ProtocolVersion string `json:"protocol_version"`
		ToolName        string `json:"tool_name"`
		ResultDigest    string `json:"result_digest"`
	}
	if err := json.Unmarshal(dispatch.ProviderReceipt.CanonicalBytes, &receipt); err != nil ||
		receipt.SchemaVersion != "freeagent.mcp-tool-receipt/v1" ||
		receipt.ProtocolVersion != mcpstdio.ProtocolVersionV1 ||
		receipt.ToolName != "text.stats" ||
		!moduleapi.ValidSHA256(receipt.ResultDigest) {
		_ = composition.Close()
		t.Fatalf("stored MCP receipt=%+v, %v", receipt, err)
	}
	registered, err = composition.registry.IsRegistered(
		ctx,
		artifactDigest,
		mcpstdio.AdapterIdentityV1,
	)
	if err != nil || !registered {
		_ = composition.Close()
		t.Fatalf("selected MCP adapter was not loaded=%v, %v", registered, err)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)

	retry, err := composition.chat.Chat(ctx, input)
	if err != nil || retry.AdmissionCreated || retry.RunID != result.RunID ||
		retry.Reply != result.Reply {
		_ = composition.Close()
		t.Fatalf("MCP same-process retry=%+v, %v", retry, err)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)
	if err := composition.Close(); err != nil {
		t.Fatalf("close MCP composition: %v", err)
	}
	assertMCPProductionArtifactClosure(t, ctx, artifactRoot)

	restarted, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("restart MCP composition: %v", err)
	}
	afterRestart, retryErr := restarted.chat.Chat(ctx, input)
	closeErr := restarted.Close()
	if retryErr != nil || closeErr != nil || afterRestart.AdmissionCreated ||
		afterRestart.RunID != result.RunID || afterRestart.Reply != result.Reply {
		t.Fatalf(
			"MCP restart retry=%+v, errors=%v",
			afterRestart,
			errors.Join(retryErr, closeErr),
		)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)
}

func TestProductionMCPUnknownIsNeverReplayedAcrossBackupRestore(
	t *testing.T,
) {
	ctx := context.Background()
	root := t.TempDir()
	eventPath := filepath.Join(root, "mcp-unknown-events.log")
	seedPath, artifactDigest := newMCPProductionFixture(
		t,
		filepath.Join(root, "fixture"),
		"crash-after-call",
		eventPath,
	)
	databasePath := filepath.Join(root, "current.sqlite")
	artifactRoot := filepath.Join(root, "artifacts")
	if _, err := initializeProductionData(ctx, initInput{
		DatabasePath:           databasePath,
		SeedPath:               seedPath,
		ArtifactRoot:           artifactRoot,
		LocalMCPArtifactGrants: []string{artifactDigest},
	}); err != nil {
		t.Fatalf("initialize crashing MCP production data: %v", err)
	}
	composition, err := openProductionComposition(
		ctx,
		databasePath,
		artifactRoot,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open crashing MCP composition: %v", err)
	}
	input := localchat.ChatInput{
		TenantID:    defaultTenantID,
		PrincipalID: "principal-production-mcp-unknown",
		WorkspaceID: defaultWorkspaceID,
		AgentID:     defaultAgentID,
		ProfileID:   "action-chat",
		Message:     "persist this call before crashing",
		RequestID:   "production-mcp-unknown-1",
		Deadline: time.Date(
			2099, time.January, 2, 3, 4, 5, 0, time.UTC,
		),
	}
	first, err := composition.chat.Chat(ctx, input)
	if err != nil {
		_ = composition.Close()
		t.Fatalf("crashing MCP Chat: %v", err)
	}
	if !first.AdmissionCreated || first.TerminalResult != nil || first.Reply != "" ||
		first.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation ||
		first.LoopResult.ReasonCode != "EXECUTOR_ERROR_AFTER_DISPATCH" {
		_ = composition.Close()
		t.Fatalf("crashing MCP result=%+v", first)
	}
	records, err := composition.store.ScanUnsettledActionDispatchRecords(ctx, first.RunID)
	if err != nil || len(records) != 1 ||
		records[0].Attempt.State != currentstore.ActionDispatchUnknown ||
		records[0].Attempt.UnknownReason != "EXECUTOR_ERROR_AFTER_DISPATCH" ||
		records[0].Attempt.Binding.Provider.ExecutionClass != moduleapi.ExecutionLocalProcess ||
		records[0].Attempt.Binding.Provider.AdapterIdentity != mcpstdio.AdapterIdentityV1 {
		_ = composition.Close()
		t.Fatalf("crashing MCP Attempt=%+v, %v", records, err)
	}
	startup, err := composition.store.ScanStartupRecovery(ctx)
	if err != nil || len(startup) != 1 || startup[0].RunID != first.RunID ||
		startup[0].FrameStep != corecontract.WaitingReconciliationLoopStep ||
		startup[0].UnsettledAttemptID != "" ||
		startup[0].UnsettledActionAttemptID != records[0].Attempt.AttemptID ||
		startup[0].UnsettledActionAttemptState != currentstore.ActionDispatchUnknown {
		_ = composition.Close()
		t.Fatalf("crashing MCP recovery projection=%+v, %v", startup, err)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)
	retry, err := composition.chat.Chat(ctx, input)
	if err != nil || retry.AdmissionCreated || retry.RunID != first.RunID ||
		retry.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation {
		_ = composition.Close()
		t.Fatalf("crashing MCP retry=%+v, %v", retry, err)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)
	if err := composition.Close(); err != nil {
		t.Fatalf("close crashing MCP composition: %v", err)
	}
	assertMCPProductionArtifactClosure(t, ctx, artifactRoot)

	bundlePath := filepath.Join(root, "mcp-unknown.bundle")
	manifest, err := currentbackup.CreateBundle(
		ctx,
		databasePath,
		artifactRoot,
		bundlePath,
		"freeagent-mcp-unknown-test/v1",
	)
	if err != nil {
		t.Fatalf("backup MCP UNKNOWN: %v", err)
	}
	if manifest.AttemptCounts.ActionUnknown != 1 ||
		manifest.AttemptCounts.ActionPending != 0 || manifest.ArtifactCount != 3 {
		t.Fatalf("MCP UNKNOWN bundle manifest=%+v", manifest)
	}
	verified, err := currentbackup.VerifyBundle(ctx, bundlePath)
	if err != nil || verified.ManifestDigest != manifest.ManifestDigest {
		t.Fatalf("verify MCP UNKNOWN bundle=%+v, %v", verified, err)
	}
	restoreRoot := filepath.Join(root, "restored")
	if err := os.MkdirAll(restoreRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	restoredDatabase := filepath.Join(restoreRoot, "current.sqlite")
	restoredArtifacts := filepath.Join(restoreRoot, "artifacts")
	if err := currentbackup.RestoreBundle(
		ctx,
		bundlePath,
		restoredDatabase,
		restoredArtifacts,
	); err != nil {
		t.Fatalf("restore MCP UNKNOWN: %v", err)
	}
	restored, err := openProductionComposition(
		ctx,
		restoredDatabase,
		restoredArtifacts,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("open restored MCP UNKNOWN composition: %v", err)
	}
	afterRestore, retryErr := restored.chat.Chat(ctx, input)
	closeErr := restored.Close()
	if retryErr != nil || closeErr != nil || afterRestore.AdmissionCreated ||
		afterRestore.RunID != first.RunID ||
		afterRestore.LoopResult.Disposition != loopapi.DispositionWaitingReconciliation {
		t.Fatalf(
			"restored MCP UNKNOWN retry=%+v, errors=%v",
			afterRestore,
			errors.Join(retryErr, closeErr),
		)
	}
	assertMCPProductionEvents(t, eventPath, 2, 1)
}

func TestHistoricalMCPUnsettledIsZeroAccessDuringPureChatStartup(
	t *testing.T,
) {
	for _, test := range []struct {
		name          string
		helperMode    string
		pendingOnDisk bool
	}{
		{name: "UNKNOWN", helperMode: "crash-after-call"},
		{name: "PENDING", helperMode: "success", pendingOnDisk: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			eventPath := filepath.Join(root, "mcp-events.log")
			seedPath, artifactDigest := newMCPProductionFixture(
				t,
				filepath.Join(root, "fixture"),
				test.helperMode,
				eventPath,
			)
			databasePath := filepath.Join(root, "current.sqlite")
			artifactRoot := filepath.Join(root, "artifacts")
			if _, err := initializeProductionData(ctx, initInput{
				DatabasePath:           databasePath,
				SeedPath:               seedPath,
				ArtifactRoot:           artifactRoot,
				LocalMCPArtifactGrants: []string{artifactDigest},
			}); err != nil {
				t.Fatalf("initialize historical MCP fixture: %v", err)
			}
			composition, err := openProductionComposition(
				ctx,
				databasePath,
				artifactRoot,
				defaultTenantID,
			)
			if err != nil {
				t.Fatalf("open historical MCP fixture: %v", err)
			}

			var faultDB *sql.DB
			if test.pendingOnDisk {
				faultDB, err = sql.Open("sqlite", crashSQLiteURI(databasePath, "rw"))
				if err != nil {
					_ = composition.Close()
					t.Fatal(err)
				}
				if _, err := faultDB.Exec(`
					CREATE TRIGGER inject_mcp_startup_terminal_failure
					BEFORE UPDATE OF state ON dispatch_attempts
					WHEN OLD.state='PENDING' AND NEW.state<>'PENDING'
					BEGIN
						SELECT RAISE(ABORT, 'injected MCP terminal failure');
					END
				`); err != nil {
					_ = faultDB.Close()
					_ = composition.Close()
					t.Fatalf("install MCP terminal failure: %v", err)
				}
			}

			input := localchat.ChatInput{
				TenantID:    defaultTenantID,
				PrincipalID: "principal-mcp-startup-zero-access",
				WorkspaceID: defaultWorkspaceID,
				AgentID:     defaultAgentID,
				ProfileID:   "action-chat",
				Message:     "leave one historical MCP Attempt",
				RequestID:   "mcp-startup-zero-access-" + strings.ToLower(test.name),
				Deadline: time.Date(
					2099, time.January, 2, 3, 4, 5, 0, time.UTC,
				),
			}
			chatResult, chatErr := composition.chat.Chat(ctx, input)
			if test.pendingOnDisk {
				if chatErr == nil || chatResult.RunID == "" {
					_ = faultDB.Close()
					_ = composition.Close()
					t.Fatalf("MCP terminal fault result=%+v error=%v", chatResult, chatErr)
				}
				if _, err := faultDB.Exec(
					`DROP TRIGGER inject_mcp_startup_terminal_failure`,
				); err != nil {
					_ = faultDB.Close()
					_ = composition.Close()
					t.Fatalf("remove MCP terminal failure: %v", err)
				}
				if err := faultDB.Close(); err != nil {
					_ = composition.Close()
					t.Fatalf("close MCP terminal fault DB: %v", err)
				}
			} else if chatErr != nil ||
				chatResult.LoopResult.Disposition !=
					loopapi.DispositionWaitingReconciliation {
				_ = composition.Close()
				t.Fatalf("create historical MCP UNKNOWN=%+v, %v", chatResult, chatErr)
			}

			records, err := composition.store.ScanUnsettledActionDispatchRecords(
				ctx,
				chatResult.RunID,
			)
			if err != nil || len(records) != 1 {
				_ = composition.Close()
				t.Fatalf("historical MCP ledger=%+v, %v", records, err)
			}
			wantBefore := currentstore.ActionDispatchUnknown
			if test.pendingOnDisk {
				wantBefore = currentstore.ActionDispatchPending
			}
			if records[0].Attempt.State != wantBefore ||
				records[0].Attempt.Binding.Provider.ArtifactDigest != artifactDigest ||
				records[0].Attempt.Binding.Provider.AdapterIdentity !=
					mcpstdio.AdapterIdentityV1 {
				_ = composition.Close()
				t.Fatalf("historical MCP Attempt=%+v", records[0].Attempt)
			}
			publishProfileWithoutAction(t, composition.store)
			if err := composition.Close(); err != nil {
				t.Fatalf("close historical MCP fixture: %v", err)
			}
			assertMCPProductionArtifactClosure(t, ctx, artifactRoot)
			assertMCPProductionEvents(t, eventPath, 2, 1)

			offlineArtifact := filepath.Join(root, "offline-mcp-artifact")
			if err := renameHistoricalMCPArtifact(
				filepath.Join(artifactRoot, artifactDigest),
				offlineArtifact,
			); err != nil {
				t.Fatalf("take historical MCP artifact offline: %v", err)
			}

			restarted, err := openProductionComposition(
				ctx,
				databasePath,
				artifactRoot,
				defaultTenantID,
			)
			if err != nil {
				t.Fatalf("Pure Chat startup touched historical MCP closure: %v", err)
			}
			defer restarted.Close()
			registered, err := restarted.registry.IsRegistered(
				ctx,
				artifactDigest,
				mcpstdio.AdapterIdentityV1,
			)
			if err != nil || registered {
				t.Fatalf("historical MCP adapter loaded=%v, %v", registered, err)
			}
			after, err := restarted.store.ScanUnsettledActionDispatchRecords(
				ctx,
				chatResult.RunID,
			)
			if err != nil || len(after) != 1 ||
				after[0].Attempt.AttemptID != records[0].Attempt.AttemptID ||
				after[0].Attempt.State != currentstore.ActionDispatchUnknown {
				t.Fatalf("post-startup MCP ledger=%+v, %v", after, err)
			}
			if test.pendingOnDisk && after[0].Attempt.UnknownReason !=
				"RECOVERED_PENDING_AFTER_CRASH" {
				t.Fatalf("recovered MCP PENDING=%+v", after[0].Attempt)
			}

			pure, err := restarted.chat.Chat(ctx, localchat.ChatInput{
				TenantID:    defaultTenantID,
				PrincipalID: "principal-pure-chat-after-mcp",
				WorkspaceID: defaultWorkspaceID,
				AgentID:     defaultAgentID,
				ProfileID:   "action-chat",
				Message:     "pure chat after historical MCP",
				RequestID:   "pure-chat-after-mcp-" + strings.ToLower(test.name),
				Deadline: time.Date(
					2099, time.January, 2, 3, 4, 6, 0, time.UTC,
				),
			})
			if err != nil || pure.Reply != "pure chat after historical MCP" ||
				pure.LoopResult.Disposition != loopapi.DispositionTerminated {
				t.Fatalf("Pure Chat after historical MCP=%+v, %v", pure, err)
			}
			assertMCPProductionEvents(t, eventPath, 2, 1)
		})
	}
}

func publishProfileWithoutAction(
	t *testing.T,
	store *currentstore.Store,
) {
	t.Helper()
	ctx := context.Background()
	basis, control, catalog, err := store.LoadPublishedBasis(
		ctx,
		defaultTenantID,
	)
	if err != nil {
		t.Fatalf("load MCP publication: %v", err)
	}
	removed := 0
	for profileIndex := range control.Profiles {
		bindings := control.Profiles[profileIndex].Bindings[:0]
		for _, binding := range control.Profiles[profileIndex].Bindings {
			if binding.Port.Name == moduleapi.PortNameActionProvider {
				removed++
				continue
			}
			bindings = append(bindings, binding)
		}
		control.Profiles[profileIndex].Bindings = bindings
	}
	if removed != 1 {
		t.Fatalf("removed MCP Action bindings=%d, want 1", removed)
	}
	control.SnapshotID = "control-mcp-unbound-pure-chat"
	control.Revision++
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("freeze no-Action Control: %v", err)
	}
	catalog.GenerationID = "catalog-mcp-unbound-pure-chat"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("freeze no-Action Catalog: %v", err)
	}
	if _, err := store.PublishControlCatalog(
		ctx,
		currentstore.PublishControlCatalogInput{
			ExpectedPointerRevision: basis.PointerRevision,
			NewPointerRevision:      basis.PointerRevision + 1,
			ControlRef:              controlRef,
			ControlCanonical:        controlCanonical,
			CatalogRef:              catalogRef,
			CatalogCanonical:        catalogCanonical,
		},
	); err != nil {
		t.Fatalf("publish no-Action current basis: %v", err)
	}
}

func renameHistoricalMCPArtifact(source, destination string) error {
	err := os.Rename(source, destination)
	if err == nil || runtime.GOOS != "windows" ||
		!isTransientWindowsRenameLock(err) {
		return err
	}

	// A just-reaped Windows test executable can remain under a short image or
	// antivirus sharing lock even after cmd.Wait and the Job handle have closed.
	// Keep the workaround at this test-only proof boundary: only the two exact
	// transient lock errors are retried, and a persistent handle still fails.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	lastErr := err
	for {
		select {
		case <-deadline.C:
			return lastErr
		case <-retry.C:
			lastErr = os.Rename(source, destination)
			if lastErr == nil || !isTransientWindowsRenameLock(lastErr) {
				return lastErr
			}
		}
	}
}

func isTransientWindowsRenameLock(err error) bool {
	const (
		windowsErrorAccessDenied     syscall.Errno = 5
		windowsErrorSharingViolation syscall.Errno = 32
	)
	var errno syscall.Errno
	return errors.As(err, &errno) &&
		(errno == windowsErrorAccessDenied ||
			errno == windowsErrorSharingViolation)
}

func newMCPProductionFixture(
	t *testing.T,
	root string,
	mode string,
	eventPath string,
) (string, string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyMCPTestTree(
		exampleBootstrapArtifactsPath(t),
		filepath.Join(root, "bootstrap-artifacts"),
	); err != nil {
		t.Fatalf("copy bootstrap artifacts: %v", err)
	}

	const moduleID = "freeagent.test.mcp.text_stats"
	const version = "1.0.0"
	relative := filepath.ToSlash(filepath.Join("bootstrap-artifacts", moduleID, version))
	artifactDirectory := filepath.Join(root, filepath.FromSlash(relative))
	contentDirectory := filepath.Join(artifactDirectory, "content")
	if err := os.MkdirAll(contentDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	currentExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executableName := "mcp-production-helper"
	currentExecutableBytes, err := os.ReadFile(currentExecutable)
	if err != nil {
		t.Fatal(err)
	}
	expectedGORACE := ""
	arguments := []string{
		"-test.run=^TestMCPProductionHelperProcess$",
		"--",
		mcpProductionHelperMarker,
		mode,
		eventPath,
		expectedGORACE,
	}
	executableBytes := currentExecutableBytes
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	} else {
		expectedGORACE = strings.TrimPrefix(mcpProductionGORACEEnvironment(), "GORACE=")
		arguments[len(arguments)-1] = expectedGORACE
		runtimeHelper := eventPath + ".helper"
		if err := os.WriteFile(
			runtimeHelper,
			currentExecutableBytes,
			0o700,
		); err != nil {
			t.Fatal(err)
		}
		arguments = append([]string{expectedGORACE, runtimeHelper}, arguments...)
		executableBytes = []byte("#!/bin/sh\nGORACE=\"$1\"\nHELPER=\"$2\"\nshift 2\nexec /usr/bin/env -i \"GORACE=$GORACE\" \"$HELPER\" \"$@\"\n")
	}
	if err := os.WriteFile(
		filepath.Join(contentDirectory, executableName),
		executableBytes,
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	executableDigest := sha256.Sum256(executableBytes)
	descriptorRaw, err := json.Marshal(mcpstdio.HostDescriptorV1{
		SchemaVersion:    mcpstdio.HostDescriptorSchemaV1,
		ProtocolVersion:  mcpstdio.ProtocolVersionV1,
		Executable:       "content/" + executableName,
		ExecutableSHA256: hex.EncodeToString(executableDigest[:]),
		Arguments:        arguments,
		WorkingDirectory: "content",
		Tools: []mcpstdio.ToolBindingV1{{
			ProviderActionID:        "text.stats",
			RequestedEffectClass:    moduleapi.EffectNone,
			RequestedMaxResultBytes: 256,
		}},
		StartupTimeoutMillis: 5000,
		CallTimeoutMillis:    2000,
		CloseTimeoutMillis:   5000,
		MaxFrameBytes:        256 << 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptorCanonical, err := moduleapi.CanonicalJSON(descriptorRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(contentDirectory, "mcp.json"),
		descriptorCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	manifestInput, err := moduleapi.CanonicalJSON([]byte(
		`{"api_version":"freeagent.module/v1","id":"` + moduleID +
			`","provides":[{"exact_version":"v1","name":"action.provider"}],` +
			`"runtime":{"entrypoint":"content/mcp.json","mode":"LOCAL_PROCESS",` +
			`"protocol":"mcp-stdio/2025-11-25"},"version":"` + version + `"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	_, manifestCanonical, err := moduleapi.ParseModuleManifestV1(manifestInput)
	if err != nil {
		t.Fatalf("parse MCP manifest: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(artifactDirectory, moduleapi.ArtifactManifestPath),
		manifestCanonical,
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	files, err := moduleapi.ScanArtifactDirectory(
		artifactDirectory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		t.Fatalf("scan MCP artifact: %v", err)
	}
	artifactDigest, err := moduleapi.ComputeArtifactDigest(manifestCanonical, files)
	if err != nil {
		t.Fatalf("digest MCP artifact: %v", err)
	}
	artifactSize := uint64(len(manifestCanonical))
	for _, file := range files {
		artifactSize += uint64(len(file.Content))
	}

	baseSeed, err := os.ReadFile(actionExampleSeedPath(t))
	if err != nil {
		t.Fatal(err)
	}
	var seed map[string]any
	if err := json.Unmarshal(baseSeed, &seed); err != nil {
		t.Fatal(err)
	}
	providers, ok := seed["action_providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("base Action seed providers=%T %#v", seed["action_providers"], seed["action_providers"])
	}
	provider, ok := providers[0].(map[string]any)
	if !ok {
		t.Fatalf("base Action provider=%T", providers[0])
	}
	provider["module"] = map[string]any{
		"activation_revision":       1,
		"artifact_digest":           artifactDigest,
		"artifact_relative_path":    relative,
		"artifact_size_bytes":       artifactSize,
		"exact_version":             version,
		"expected_adapter_identity": mcpstdio.AdapterIdentityV1,
		"expected_execution_class":  string(moduleapi.ExecutionLocalProcess),
		"installation_id":           "installation-freeagent-test-mcp-text-stats-1",
		"instance_id":               "mcp-text-stats",
		"module_id":                 moduleID,
	}
	seed["seed_id"] = "freeagent.test.mcp-action-chat"
	encodedSeed, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	canonicalSeed, err := moduleapi.CanonicalJSON(encodedSeed)
	if err != nil {
		t.Fatal(err)
	}
	seedPath := filepath.Join(root, "current-v1.mcp.bootstrap.seed.json")
	if err := os.WriteFile(seedPath, canonicalSeed, 0o600); err != nil {
		t.Fatal(err)
	}
	return seedPath, artifactDigest
}

func exampleBootstrapArtifactsPath(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve MCP composition test source")
	}
	return filepath.Join(
		filepath.Dir(currentFile),
		"..",
		"..",
		"examples",
		"bootstrap-artifacts",
	)
}

func copyMCPTestTree(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("test fixture source must not be a symlink")
	}
	if info.IsDir() {
		if err := os.MkdirAll(destination, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyMCPTestTree(
				filepath.Join(source, entry.Name()),
				filepath.Join(destination, entry.Name()),
			); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return errors.New("test fixture source must be an ordinary file")
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, content, info.Mode().Perm())
}

func mcpProductionGORACEEnvironment() string {
	const boundedExit = "atexit_sleep_ms=0"
	options := make([]string, 0, len(strings.Fields(os.Getenv("GORACE")))+1)
	for _, option := range strings.Fields(os.Getenv("GORACE")) {
		if strings.HasPrefix(option, "atexit_sleep_ms=") {
			continue
		}
		options = append(options, option)
	}
	options = append(options, boundedExit)
	return "GORACE=" + strings.Join(options, " ")
}

func mcpProductionHelperArguments(arguments []string) (string, string, string, bool) {
	for index, argument := range arguments {
		if argument != mcpProductionHelperMarker || index+3 >= len(arguments) {
			continue
		}
		return arguments[index+1], arguments[index+2], arguments[index+3], true
	}
	return "", "", "", false
}

func runMCPProductionHelper(mode, eventPath string) int {
	if err := appendMCPProductionEvent(eventPath, "process_start\n"); err != nil {
		return 3
	}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "freeagent-production-test-mcp", Version: "1.0.0"},
		&mcp.ServerOptions{PageSize: 1},
	)
	server.AddTool(
		&mcp.Tool{
			Name:        "text.stats",
			Description: "Return the exact supplied text through MCP.",
			InputSchema: json.RawMessage(
				`{"additionalProperties":false,"properties":{"text":{"maxLength":8192,"type":"string"}},"required":["text"],"type":"object"}`,
			),
		},
		func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if err := appendMCPProductionEvent(eventPath, "tool_call\n"); err != nil {
				return nil, err
			}
			if mode == "crash-after-call" {
				os.Exit(91)
			}
			var arguments struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
				return nil, err
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: arguments.Text}},
			}, nil
		},
	)
	server.AddTool(
		&mcp.Tool{
			Name:        "unconfigured_tool",
			Description: "Force tools/list pagination without granting an Action.",
			InputSchema: json.RawMessage(`{"additionalProperties":false,"properties":{},"type":"object"}`),
		},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{}}, nil
		},
	)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil &&
		!errors.Is(err, io.EOF) {
		return 2
	}
	return 0
}

func appendMCPProductionEvent(path, event string) (returnErr error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, file.Close())
	}()
	if _, err := file.WriteString(event); err != nil {
		return err
	}
	return file.Sync()
}

func assertMCPProductionEvents(
	t *testing.T,
	path string,
	wantStarts int,
	wantCalls int,
) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read MCP production events: %v", err)
	}
	if starts := strings.Count(string(content), "process_start\n"); starts != wantStarts {
		t.Fatalf("MCP process starts=%d want=%d; events=%q", starts, wantStarts, content)
	}
	if calls := strings.Count(string(content), "tool_call\n"); calls != wantCalls {
		t.Fatalf("MCP tool calls=%d want=%d; events=%q", calls, wantCalls, content)
	}
}

func assertMCPProductionArtifactClosure(
	t *testing.T,
	ctx context.Context,
	artifactRoot string,
) {
	t.Helper()
	root, err := moduleartifactstore.SelectArtifactRootV1(artifactRoot)
	if err != nil {
		t.Fatalf("select MCP artifact root after execution: %v", err)
	}
	if err := moduleartifactstore.VerifyPhysicalRootClosureV1(ctx, root); err != nil {
		t.Fatalf("verify MCP artifact root after execution: %v", err)
	}
}
