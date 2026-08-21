package localchat

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/sdk/moduleapi"
)

func TestCompositeDecisionChatCrossWorkspaceUsesTransferEdgesAndExactRetry(
	t *testing.T,
) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeApproveRoundZero,
	)
	targetWorkspaceID := publishDecisionWorkspaceTransferV1(
		t,
		fixture,
		"slot-analysis",
	)
	input := fixture.base.input(
		"decision-cross-workspace-approve",
		"Design one bounded cross-workspace architecture proposal.",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)
	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil || first.TerminalResult == nil || first.Reviewer == nil ||
		first.FailureCode != "" {
		t.Fatalf("cross-Workspace Chat=%+v error=%v", first, err)
	}
	if got := fixture.base.invoker.callCount(); got != 4 {
		t.Fatalf("model calls=%d want 2 Specialists + Reviewer + Root", got)
	}

	var transferredChildID string
	for _, child := range first.Children {
		loaded := loadCompositeChatRun(t, fixture.base.store, child.RunID)
		if loaded.Manifest.Composite != nil &&
			loaded.Manifest.Composite.Assignment != nil &&
			loaded.Manifest.Composite.Assignment.SlotID == "slot-analysis" {
			transferredChildID = child.RunID
			break
		}
	}
	if transferredChildID == "" {
		t.Fatalf("transferred Child is absent: %+v", first.Children)
	}
	child := loadCompositeChatRun(t, fixture.base.store, transferredChildID)
	if child.Member.Workspace.ID != targetWorkspaceID ||
		child.WorkspaceTransfer == nil {
		t.Fatalf("transferred Child Workspace/closure=%+v / %+v", child.Member.Workspace, child.WorkspaceTransfer)
	}
	if _, found := child.FindContent(child.Manifest.TaskInputRef); found {
		t.Fatal("cross-Workspace Child exposes root TASK_INPUT through generic Contents")
	}
	requestTransfer, err := currentstore.PrepareWorkspaceTransferRequestV1(child)
	if err != nil || requestTransfer == nil {
		t.Fatalf("PrepareWorkspaceTransferRequestV1=%+v error=%v", requestTransfer, err)
	}
	taskSummary, err := corecontract.RestoreWorkspaceTaskSummaryV1(
		requestTransfer.Payload.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("RestoreWorkspaceTaskSummaryV1: %v", err)
	}
	assertRunTransferCompilationV1(
		t,
		child,
		corecontract.WorkspaceTransferDirectionRequestV1,
		requestTransfer.EnvelopeRef,
		taskSummary.Summary,
	)

	reviewer := loadCompositeChatRun(
		t,
		fixture.base.store,
		first.Reviewer.RunID,
	)
	assertRunTransferCompilationV1(
		t,
		reviewer,
		corecontract.WorkspaceTransferDirectionResultV1,
		"",
		"",
	)
	root := loadCompositeChatRun(t, fixture.base.store, first.RootRunID)
	assertRunTransferCompilationV1(
		t,
		root,
		corecontract.WorkspaceTransferDirectionResultV1,
		"",
		"",
	)
	transferredResults := 0
	for _, result := range root.CompositeChildren {
		if result.WorkspaceTransfer != nil {
			transferredResults++
			if result.RunID != transferredChildID ||
				result.WorkspaceTransfer.Envelope.Direction !=
					corecontract.WorkspaceTransferDirectionResultV1 ||
				result.WorkspaceTransfer.Payload.Digest != result.ResultRef {
				t.Fatalf("Root RESULT transfer differs from Child result: %+v", result)
			}
		}
	}
	if transferredResults != 1 {
		t.Fatalf("Root transferred result count=%d want 1", transferredResults)
	}

	retry, err := fixture.service.Chat(context.Background(), input)
	if err != nil || retry.AdmissionCreated || retry.Reply != first.Reply {
		t.Fatalf("exact retry=%+v error=%v", retry, err)
	}
	if got := fixture.base.invoker.callCount(); got != 4 {
		t.Fatalf("exact retry added model calls: %d", got)
	}
}

func TestCompositeDecisionChatCrossWorkspaceRepairPreservesTransferLineageAndExactRetry(
	t *testing.T,
) {
	fixture := newDecisionCompositeFixture(
		t,
		decisionCompositeRepairOneApprove,
	)
	targetWorkspaceID := publishDecisionWorkspaceTransferV1(
		t,
		fixture,
		"slot-analysis",
	)
	input := fixture.base.input(
		"decision-cross-workspace-repair-one",
		"Repair only the affected cross-workspace specialist contribution.",
		time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
	)
	first, err := fixture.service.Chat(context.Background(), input)
	if err != nil || first.TerminalResult == nil || first.Reviewer == nil ||
		first.RepairReviewer == nil || first.FailureCode != "" ||
		first.Reply != "decision result for "+first.RootRunID {
		t.Fatalf("cross-Workspace repair Chat=%+v error=%v", first, err)
	}
	if got := fixture.base.invoker.callCount(); got != 6 {
		t.Fatalf(
			"model calls=%d want 2 Specialists + Reviewer0 + repair1 + Reviewer1 + Root",
			got,
		)
	}

	initialAnalysisResult, initialAnalysis :=
		findWorkspaceTransferChatChildBySlotV1(
			t,
			fixture.base.store,
			first.Children,
			"slot-analysis",
		)
	initialReviewResult, _ := findWorkspaceTransferChatChildBySlotV1(
		t,
		fixture.base.store,
		first.Children,
		"slot-review",
	)
	runs := fixture.invoker.runIDsSnapshot()
	repairAnalysisResult, repairReviewResult :=
		findActivatedAndSkippedWorkspaceTransferRepairV1(
			t,
			fixture.base.store,
			first.RepairChildren,
			runs,
			"slot-analysis",
		)
	repairAnalysis := loadCompositeChatRun(
		t,
		fixture.base.store,
		repairAnalysisResult.RunID,
	)

	if initialAnalysis.Member.Workspace.ID != targetWorkspaceID ||
		initialAnalysis.WorkspaceTransfer == nil ||
		repairAnalysis.Member.Workspace.ID != targetWorkspaceID ||
		repairAnalysis.WorkspaceTransfer == nil {
		t.Fatalf(
			"transferred initial/repair Workspace closure=%+v / %+v",
			initialAnalysis.WorkspaceTransfer,
			repairAnalysis.WorkspaceTransfer,
		)
	}
	if _, found := initialAnalysis.FindContent(
		initialAnalysis.Manifest.TaskInputRef,
	); found {
		t.Fatal("initial cross-Workspace Child exposes root TASK_INPUT")
	}
	if _, found := repairAnalysis.FindContent(
		repairAnalysis.Manifest.TaskInputRef,
	); found {
		t.Fatal("repair cross-Workspace Child exposes root TASK_INPUT")
	}
	initialRequest, err := currentstore.PrepareWorkspaceTransferRequestV1(
		initialAnalysis,
	)
	if err != nil || initialRequest == nil {
		t.Fatalf("prepare initial REQUEST=%+v error=%v", initialRequest, err)
	}
	initialSummary, err := corecontract.RestoreWorkspaceTaskSummaryV1(
		initialRequest.Payload.CanonicalBytes,
	)
	if err != nil || initialSummary.RepairRound != 0 ||
		initialSummary.PreviousSetDigest != "" || initialSummary.VerdictRef != "" {
		t.Fatalf("initial TaskSummary=%+v error=%v", initialSummary, err)
	}
	assertPersistedWorkspaceTransferRecordV1(
		t,
		fixture.base.store,
		initialRequest,
		corecontract.WorkspaceTransferDirectionRequestV1,
	)
	assertRunTransferCompilationV1(
		t,
		initialAnalysis,
		corecontract.WorkspaceTransferDirectionRequestV1,
		initialRequest.EnvelopeRef,
		initialSummary.Summary,
	)

	material := repairAnalysis.WorkspaceTransfer
	if material.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
		material.PreviousContributionSet == nil ||
		material.PreviousContributionSetDigest == "" ||
		material.RepairVerdict == nil ||
		material.RepairVerdict.Decision !=
			corecontract.CollaborationReviewDecisionRepairRequiredV1 ||
		material.RepairVerdictRef == "" || material.RepairBasis == nil ||
		len(material.RepairBasisCanonical) == 0 ||
		material.RepairBasis.PreviousSetDigest !=
			material.PreviousContributionSetDigest ||
		material.RepairBasis.VerdictRef != material.RepairVerdictRef {
		t.Fatalf("repair transfer lineage=%+v", material)
	}
	repairRequest, err := currentstore.PrepareWorkspaceTransferRequestV1(
		repairAnalysis,
	)
	if err != nil || repairRequest == nil {
		t.Fatalf("prepare repair REQUEST=%+v error=%v", repairRequest, err)
	}
	repairSummary, err := corecontract.RestoreWorkspaceTaskSummaryV1(
		repairRequest.Payload.CanonicalBytes,
	)
	if err != nil ||
		repairSummary.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
		repairSummary.PreviousSetDigest !=
			material.PreviousContributionSetDigest ||
		repairSummary.VerdictRef != material.RepairVerdictRef ||
		repairSummary.SourceTaskInputRef != material.RootTaskInput.Digest ||
		!strings.Contains(repairSummary.Summary, `"repair_basis"`) {
		t.Fatalf("repair TaskSummary=%+v error=%v", repairSummary, err)
	}
	assertPersistedWorkspaceTransferRecordV1(
		t,
		fixture.base.store,
		repairRequest,
		corecontract.WorkspaceTransferDirectionRequestV1,
	)
	assertRunTransferCompilationV1(
		t,
		repairAnalysis,
		corecontract.WorkspaceTransferDirectionRequestV1,
		repairRequest.EnvelopeRef,
		repairSummary.Summary,
	)

	reviewerZero := loadCompositeChatRun(
		t,
		fixture.base.store,
		first.Reviewer.RunID,
	)
	initialResult := findWorkspaceTransferCompositeResultBySlotV1(
		t,
		reviewerZero.CompositeChildren,
		"slot-analysis",
	)
	if initialResult.RunID != initialAnalysisResult.RunID ||
		initialResult.RepairRound != 0 ||
		initialResult.WorkspaceTransfer == nil {
		t.Fatalf("Reviewer0 initial RESULT=%+v", initialResult)
	}
	assertPersistedWorkspaceTransferRecordV1(
		t,
		fixture.base.store,
		initialResult.WorkspaceTransfer,
		corecontract.WorkspaceTransferDirectionResultV1,
	)
	assertRunTransferCompilationV1(
		t,
		reviewerZero,
		corecontract.WorkspaceTransferDirectionResultV1,
		initialResult.WorkspaceTransfer.EnvelopeRef,
		"",
	)

	reviewerOne := loadCompositeChatRun(
		t,
		fixture.base.store,
		first.RepairReviewer.RunID,
	)
	repairResult := findWorkspaceTransferCompositeResultBySlotV1(
		t,
		reviewerOne.CompositeChildren,
		"slot-analysis",
	)
	if repairResult.RunID != repairAnalysisResult.RunID ||
		repairResult.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
		repairResult.WorkspaceTransfer == nil {
		t.Fatalf("Reviewer1 repair RESULT=%+v", repairResult)
	}
	assertPersistedWorkspaceTransferRecordV1(
		t,
		fixture.base.store,
		repairResult.WorkspaceTransfer,
		corecontract.WorkspaceTransferDirectionResultV1,
	)
	assertRunTransferCompilationV1(
		t,
		reviewerOne,
		corecontract.WorkspaceTransferDirectionResultV1,
		repairResult.WorkspaceTransfer.EnvelopeRef,
		"",
	)

	root := loadCompositeChatRun(t, fixture.base.store, first.RootRunID)
	rootAnalysis := findWorkspaceTransferCompositeResultBySlotV1(
		t,
		root.CompositeChildren,
		"slot-analysis",
	)
	rootReview := findWorkspaceTransferCompositeResultBySlotV1(
		t,
		root.CompositeChildren,
		"slot-review",
	)
	if rootAnalysis.RunID != repairAnalysisResult.RunID ||
		rootAnalysis.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
		rootAnalysis.WorkspaceTransfer == nil ||
		rootAnalysis.WorkspaceTransfer.EnvelopeRef !=
			repairResult.WorkspaceTransfer.EnvelopeRef ||
		rootReview.RunID != initialReviewResult.RunID ||
		rootReview.RepairRound != 0 || rootReview.WorkspaceTransfer != nil {
		t.Fatalf(
			"Root effective contribution set analysis=%+v review=%+v",
			rootAnalysis,
			rootReview,
		)
	}
	assertRunTransferCompilationV1(
		t,
		root,
		corecontract.WorkspaceTransferDirectionResultV1,
		repairResult.WorkspaceTransfer.EnvelopeRef,
		"",
	)

	if countWorkspaceTransferRunIDV1(runs, initialAnalysisResult.RunID) != 1 ||
		countWorkspaceTransferRunIDV1(runs, initialReviewResult.RunID) != 1 ||
		countWorkspaceTransferRunIDV1(runs, repairAnalysisResult.RunID) != 1 ||
		countWorkspaceTransferRunIDV1(runs, repairReviewResult.RunID) != 0 ||
		countWorkspaceTransferRunIDV1(runs, first.Reviewer.RunID) != 1 ||
		countWorkspaceTransferRunIDV1(runs, first.RepairReviewer.RunID) != 1 ||
		countWorkspaceTransferRunIDV1(runs, first.RootRunID) != 1 {
		t.Fatalf("bounded repair invocation ledger=%v", runs)
	}

	beforeRetry := fixture.base.invoker.callCount()
	retry, err := fixture.service.Chat(context.Background(), input)
	if err != nil || retry.AdmissionCreated || retry.Reply != first.Reply ||
		retry.TerminalResult == nil || retry.FailureCode != "" {
		t.Fatalf("cross-Workspace repair exact retry=%+v error=%v", retry, err)
	}
	if afterRetry := fixture.base.invoker.callCount(); afterRetry != beforeRetry || afterRetry != 6 {
		t.Fatalf(
			"exact retry provider calls before=%d after=%d want new calls 0",
			beforeRetry,
			afterRetry,
		)
	}
}

func findActivatedAndSkippedWorkspaceTransferRepairV1(
	t *testing.T,
	store *currentstore.Store,
	repairs []CompositeChildChatResult,
	runs []string,
	wantActivatedSlot string,
) (CompositeChildChatResult, CompositeChildChatResult) {
	t.Helper()
	var activated CompositeChildChatResult
	var skipped CompositeChildChatResult
	for _, repair := range repairs {
		if !containsDecisionRunID(runs, repair.RunID) {
			if skipped.RunID != "" {
				t.Fatalf("multiple skipped repair Runs: %+v", repairs)
			}
			skipped = repair
			continue
		}
		if activated.RunID != "" {
			t.Fatalf("multiple activated repair Runs: %+v", repairs)
		}
		loaded := loadCompositeChatRun(t, store, repair.RunID)
		if loaded.Manifest.Composite == nil ||
			loaded.Manifest.Composite.Assignment == nil ||
			loaded.Manifest.Composite.Assignment.SlotID != wantActivatedSlot {
			t.Fatalf(
				"activated repair Run %q slot differs: %+v",
				repair.RunID,
				loaded.Manifest.Composite,
			)
		}
		activated = repair
	}
	if activated.RunID == "" || skipped.RunID == "" {
		t.Fatalf(
			"activated/skipped repair closure=%+v / %+v runs=%v",
			activated,
			skipped,
			runs,
		)
	}
	return activated, skipped
}

func findWorkspaceTransferChatChildBySlotV1(
	t *testing.T,
	store *currentstore.Store,
	children []CompositeChildChatResult,
	slotID string,
) (CompositeChildChatResult, currentstore.RunForLoop) {
	t.Helper()
	for _, child := range children {
		run := loadCompositeChatRun(t, store, child.RunID)
		if run.Manifest.Composite != nil &&
			run.Manifest.Composite.Assignment != nil &&
			run.Manifest.Composite.Assignment.SlotID == slotID {
			return child, run
		}
	}
	t.Fatalf("Composite Child slot %q is absent", slotID)
	return CompositeChildChatResult{}, currentstore.RunForLoop{}
}

func findWorkspaceTransferCompositeResultBySlotV1(
	t *testing.T,
	children []currentstore.CompositeChildResultRecordV1,
	slotID string,
) currentstore.CompositeChildResultRecordV1 {
	t.Helper()
	for _, child := range children {
		if child.SlotID == slotID {
			return child
		}
	}
	t.Fatalf("Composite result slot %q is absent: %+v", slotID, children)
	return currentstore.CompositeChildResultRecordV1{}
}

func assertPersistedWorkspaceTransferRecordV1(
	t *testing.T,
	store *currentstore.Store,
	record *currentstore.WorkspaceTransferRecordV1,
	direction corecontract.WorkspaceTransferDirectionV1,
) {
	t.Helper()
	if record == nil || record.Envelope.Direction != direction ||
		record.Envelope.PayloadRef != record.Payload.Digest {
		t.Fatalf("Workspace Transfer record differs: %+v", record)
	}
	envelopeContent, err := store.GetContent(
		context.Background(),
		record.EnvelopeRef,
	)
	if err != nil ||
		envelopeContent.Kind != currentstore.ContentWorkspaceTransferEnvelope {
		t.Fatalf("persisted TransferEnvelope=%+v error=%v", envelopeContent, err)
	}
	envelope, err := corecontract.RestoreWorkspaceTransferEnvelopeV1(
		envelopeContent.CanonicalBytes,
		record.EnvelopeDigest,
	)
	if err != nil || envelope != record.Envelope {
		t.Fatalf("restore TransferEnvelope=%+v error=%v", envelope, err)
	}
	payload, err := store.GetContent(
		context.Background(),
		record.Payload.Digest,
	)
	if err != nil || payload.Kind != record.Payload.Kind ||
		payload.MediaType != record.Payload.MediaType ||
		payload.SizeBytes != record.Payload.SizeBytes ||
		!bytes.Equal(payload.CanonicalBytes, record.Payload.CanonicalBytes) ||
		!strings.EqualFold(payload.Digest, record.Payload.Digest) {
		t.Fatalf("persisted Transfer payload=%+v error=%v", payload, err)
	}
}

func countWorkspaceTransferRunIDV1(values []string, wanted string) int {
	count := 0
	for _, value := range values {
		if value == wanted {
			count++
		}
	}
	return count
}

func publishDecisionWorkspaceTransferV1(
	t *testing.T,
	fixture *decisionCompositeFixture,
	slotID string,
) string {
	t.Helper()
	ctx := context.Background()
	basis, control, catalog, err := fixture.base.store.LoadPublishedBasis(
		ctx,
		fixture.base.tenantID,
	)
	if err != nil || len(control.Workspaces) != 1 ||
		len(control.CompositeAgents) != 1 {
		t.Fatalf("LoadPublishedBasis for transfer: %v / %+v", err, control)
	}
	root := control.Workspaces[0]
	target := root
	target.Workspace = corecontract.WorkspaceRef{
		ID:      "workspace-chat-specialist",
		Version: "v1",
		Digest:  strings.Repeat("7", moduleapi.SHA256HexLength),
	}
	target.ChannelEndpoints = nil
	target.ChannelIdentities = nil
	root.TransferGrants = []corecontract.WorkspaceTransferGrantV1{
		workspaceTransferGrantForChatV1(
			"grant-chat-root-target",
			fixture.base.tenantID,
			root.Workspace,
			target.Workspace,
			true,
		),
	}
	target.TransferGrants = []corecontract.WorkspaceTransferGrantV1{
		workspaceTransferGrantForChatV1(
			"grant-chat-target-root",
			fixture.base.tenantID,
			target.Workspace,
			root.Workspace,
			false,
		),
	}
	control.Workspaces[0] = root
	control.Workspaces = append(control.Workspaces, target)
	found := false
	for index := range control.CompositeAgents[0].Members {
		if control.CompositeAgents[0].Members[index].SlotID == slotID {
			control.CompositeAgents[0].Members[index].TargetWorkspaceID =
				target.Workspace.ID
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Composite slot %q is absent", slotID)
	}
	control.SnapshotID = "control-chat-decision-workspace-transfer"
	control.Revision++
	_, controlRef, controlCanonical, err :=
		controlcontract.NewControlSnapshot(control)
	if err != nil {
		t.Fatalf("NewControlSnapshot(transfer): %v", err)
	}
	catalog.GenerationID = "catalog-chat-decision-workspace-transfer"
	catalog.Generation++
	catalog.ControlSnapshotID = controlRef.SnapshotID
	catalog.ControlSnapshotDigest = controlRef.Digest
	_, catalogRef, catalogCanonical, err :=
		controlcontract.NewCatalogGeneration(catalog)
	if err != nil {
		t.Fatalf("NewCatalogGeneration(transfer): %v", err)
	}
	if _, err := fixture.base.store.PublishControlCatalog(
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
		t.Fatalf("PublishControlCatalog(transfer): %v", err)
	}
	return target.Workspace.ID
}

func workspaceTransferGrantForChatV1(
	grantID string,
	tenantID string,
	workspace corecontract.WorkspaceRef,
	peer corecontract.WorkspaceRef,
	root bool,
) corecontract.WorkspaceTransferGrantV1 {
	grant := corecontract.WorkspaceTransferGrantV1{
		SchemaVersion: corecontract.WorkspaceTransferGrantSchemaVersionV1,
		GrantID:       grantID,
		TenantID:      tenantID,
		Workspace:     workspace,
		PeerWorkspace: peer,
		Revision:      1,
		Enabled:       true,
	}
	if root {
		grant.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadTaskSummaryV1,
		}
		grant.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
			corecontract.WorkspaceTransferPayloadSpecialistResultV1,
		}
		grant.MaxSendPayloadBytes =
			corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1
		grant.MaxReceivePayloadBytes =
			corecontract.WorkspaceTransferMaximumPayloadBytesV1
		return grant
	}
	grant.SendPayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
		corecontract.WorkspaceTransferPayloadSpecialistResultV1,
	}
	grant.ReceivePayloadKinds = []corecontract.WorkspaceTransferPayloadKindV1{
		corecontract.WorkspaceTransferPayloadTaskSummaryV1,
	}
	grant.MaxSendPayloadBytes =
		corecontract.WorkspaceTransferMaximumPayloadBytesV1
	grant.MaxReceivePayloadBytes =
		corecontract.WorkspaceTaskSummaryMaximumPayloadBytesV1
	return grant
}

func assertRunTransferCompilationV1(
	t *testing.T,
	run currentstore.RunForLoop,
	direction corecontract.WorkspaceTransferDirectionV1,
	wantEnvelopeRef string,
	wantFinalUser string,
) {
	t.Helper()
	if len(run.ModelDispatches) != 1 ||
		run.ModelDispatches[0].Attempt.ContextCompilation == nil {
		t.Fatalf("Run %q model dispatch closure=%+v", run.RunID, run.ModelDispatches)
	}
	compilation, err := corecontract.RestoreContextCompilationV1(
		run.ModelDispatches[0].Attempt.ContextCompilation.CanonicalBytes,
	)
	if err != nil || len(compilation.WorkspaceTransfers) != 1 ||
		compilation.WorkspaceTransfers[0].Direction != direction {
		t.Fatalf("Run %q transfer compilation=%+v error=%v", run.RunID, compilation, err)
	}
	if wantEnvelopeRef != "" &&
		compilation.WorkspaceTransfers[0].EnvelopeRef != wantEnvelopeRef {
		t.Fatalf(
			"Run %q request EnvelopeRef=%s want %s",
			run.RunID,
			compilation.WorkspaceTransfers[0].EnvelopeRef,
			wantEnvelopeRef,
		)
	}
	if wantFinalUser == "" {
		return
	}
	request, err := moduleapi.RestoreModelGenerateRequestV1(
		run.ModelDispatches[0].Attempt.Request.CanonicalBytes,
	)
	if err != nil {
		t.Fatalf("Run %q restore request: %v", run.RunID, err)
	}
	lastUser := ""
	for _, message := range request.Messages {
		if message.Role == moduleapi.ModelRoleUser {
			lastUser = message.Content
		}
	}
	if lastUser != wantFinalUser {
		t.Fatalf("Run %q final USER=%q want trusted summary %q", run.RunID, lastUser, wantFinalUser)
	}
}
