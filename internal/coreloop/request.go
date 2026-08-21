package coreloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/knowledgecore"
	"github.com/endview/freeagent/internal/memorycore"
	"github.com/endview/freeagent/internal/modulehost"
	"github.com/endview/freeagent/sdk/moduleapi"
)

var ErrInvalidPureChatRequest = errors.New(
	"coreloop: invalid Pure Chat request",
)

type preparedPureChatRequestV1 struct {
	Request                     moduleapi.ModelGenerateRequestV1
	RequestCanonical            []byte
	ContextCompilationCanonical []byte
}

// preparedWorkspaceTransferContextV1 is a Host-only preparation result. The
// original TASK_INPUT is retained only so the trusted compiler can re-prove a
// deterministic TASK_SUMMARY; DynamicTaskText is the sole text visible to a
// dynamic Knowledge or Memory query.
type preparedWorkspaceTransferContextV1 struct {
	TaskInput       currentstore.ContentRecord
	DynamicTaskText string
	Materials       []contextcompiler.WorkspaceTransferMaterialV1
}

type dynamicContextMaterialV1 struct {
	AuthorityCanonical  []byte
	StateCanonical      []byte
	RequestCanonical    []byte
	OutputCanonical     []byte
	KnowledgeProvenance *corecontract.KnowledgeRetrievalProvenanceV1
	KnowledgeReuse      *corecontract.KnowledgeReuseEvidenceV1
}

// preparedDynamicContextReadV1 is the immutable result of the first dynamic
// context phase. Every Binding is restored and authorized before the second
// phase may invoke any Provider. NOT_SELECTED Knowledge Bindings carry only
// their authorized ceiling and never acquire a request, Gate or Host.
type preparedDynamicContextReadV1 struct {
	bindingIndex       uint32
	binding            moduleapi.PortBinding
	protocol           string
	authorityCanonical []byte
	stateCanonical     []byte
	requestCanonical   []byte
	requestDigest      string
	validateOutput     func([]byte) error
	notSelected        bool
	knowledgeConfig    *moduleapi.KnowledgeContextBindingV1
	knowledgeRequest   *moduleapi.KnowledgeContextRequestV1
	knowledgeDecision  *knowledgecore.BindingDecision
	decisionSetDigest  string
	knowledgeReuse     *corecontract.KnowledgeReuseEvidenceV1
	memoryConfig       *moduleapi.MemoryContextBindingV1
	memoryAuthority    *moduleapi.MemoryAuthorityCeilingV1
	memorySnapshot     *moduleapi.AgentMemorySnapshotV1
	memorySnapshotRef  *moduleapi.MemorySnapshotRefV1
	memoryScope        *moduleapi.MemoryQueryScopeV1
	memoryEvaluatedAt  uint64
}

// BuildPureChatRequestV1 is the low-watermark compatibility view of the single
// S2.1 request preparation path. It fails closed when a compilation record is
// required so callers cannot discard audit bytes and later invoke
// BeginModelDispatch without them. UniversalLoop calls
// preparePureChatRequestV1 directly and persists both outputs atomically.
func BuildPureChatRequestV1(
	run currentstore.RunForLoop,
) (moduleapi.ModelGenerateRequestV1, []byte, error) {
	prepared, err := preparePureChatRequestV1(run)
	if err != nil {
		return moduleapi.ModelGenerateRequestV1{}, nil, err
	}
	if len(prepared.ContextCompilationCanonical) != 0 {
		return moduleapi.ModelGenerateRequestV1{}, nil, fmt.Errorf(
			"%w: context compilation persistence is required; use UniversalLoop",
			ErrInvalidPureChatRequest,
		)
	}
	return prepared.Request, bytes.Clone(prepared.RequestCanonical), nil
}

// preparePureChatRequestV1 restores only the frozen Current Store recovery
// closure and delegates all context ordering, trust isolation and 85/100
// policy decisions to the one pure Context Compiler. It performs no Store or
// remote reads and never consults current Control/Catalog state.
func preparePureChatRequestV1(
	run currentstore.RunForLoop,
) (preparedPureChatRequestV1, error) {
	transfer, err := prepareWorkspaceTransferContextV1(run)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	return prepareChatRequestWithDynamicContextV1(run, nil, transfer)
}

// prepareChatRequestWithDynamicContextV1 is the one pure request compiler
// entry. Dynamic context bytes can only be supplied by UniversalLoop after an
// exact, one-shot context read; callers without a Host continue through the
// unchanged Pure Chat path and fail closed if a dynamic Binding is present.
func prepareChatRequestWithDynamicContextV1(
	run currentstore.RunForLoop,
	dynamic map[uint32]dynamicContextMaterialV1,
	transfer preparedWorkspaceTransferContextV1,
) (preparedPureChatRequestV1, error) {
	modelBinding, err := exactModelGenerateBinding(run)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	modelConfigContent, err := exactContent(
		run,
		modelBinding.ConfigRef,
		currentstore.ContentConfig,
		"model Binding config",
	)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	modelConfig, err := moduleapi.RestoreModelBindingConfigV1(
		modelConfigContent.CanonicalBytes,
	)
	if err != nil {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: restore model Binding config: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	modelProfileRef, modelProfileCanonical, err := restoreFrozenModelProfile(
		run,
		modelBinding,
		modelConfig,
	)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	modelParameters, err := reviewerTightenedModelParametersV1(
		run,
		modelConfig.Parameters,
	)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}

	contextPolicyContent, err := exactContent(
		run,
		run.Member.ContextPolicy.Digest,
		currentstore.ContentPolicy,
		"ContextPolicy",
	)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	contextPlan, contextBindings, err := restoreContextCompilerBindings(run)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	if err := applyDynamicContextMaterials(
		contextPlan,
		contextBindings,
		dynamic,
	); err != nil {
		return preparedPureChatRequestV1{}, err
	}
	var history []contextcompiler.HistoryTurnV1
	var conversationHistory []contextcompiler.ConversationHistoryTurnV1
	var conversationSummaryCandidate *corecontract.ContextCompilationSummaryV1
	if run.Manifest.ConversationTurn != nil {
		conversationHistory, err = restoreContextCompilerConversationHistory(run)
		if err == nil {
			conversationSummaryCandidate, err =
				currentstore.ConversationSummaryCandidateForCompilerV1(run)
		}
	} else {
		history, err = restoreContextCompilerHistory(run)
	}
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	taskContent := transfer.TaskInput
	if taskContent.Digest != run.Manifest.TaskInputRef ||
		taskContent.Kind != currentstore.ContentTaskInput ||
		taskContent.MediaType != "application/json" ||
		len(taskContent.CanonicalBytes) == 0 {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: frozen task input is absent from the Host preparation",
			ErrInvalidPureChatRequest,
		)
	}
	var (
		composite             *corecontract.CompositeRunNodeV1
		compositeResults      []contextcompiler.CompositeChildResultV1
		specialistSet         *corecontract.CompositeSpecialistResultSetV1
		specialistDigest      string
		reviewVerdict         *contextcompiler.CompositeReviewVerdictMaterialV1
		collaborationMaterial *contextcompiler.CompositeCollaborationMaterialV1
	)
	_, collaborationEnabled, err := collaborationRootForRunV1(run)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	if collaborationEnabled {
		composite, compositeResults, collaborationMaterial, err =
			collaborationCompilerInputV1(run)
	} else {
		composite, compositeResults, specialistSet, specialistDigest,
			reviewVerdict, err = compositeContextCompilerInputV1(run)
	}
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}

	compiled, err := contextcompiler.CompileV1(
		contextcompiler.CompileInputV1{
			TenantID:                        run.Manifest.TenantID,
			WorkspaceScope:                  run.Member.Workspace,
			AgentScope:                      run.Member.Agent,
			ContextPolicyRef:                run.Member.ContextPolicy,
			ContextPolicyDocumentCanonical:  contextPolicyContent.CanonicalBytes,
			ModelProfileRef:                 modelProfileRef,
			ModelProfileCanonical:           modelProfileCanonical,
			ModelParameters:                 modelParameters,
			ContextPlan:                     contextPlan,
			ContextBindings:                 contextBindings,
			Actions:                         run.Member.Actions,
			HistoryTurns:                    history,
			ConversationHistoryTurns:        conversationHistory,
			ConversationSummaryCandidate:    conversationSummaryCandidate,
			TaskInputRef:                    run.Manifest.TaskInputRef,
			TaskInputCanonical:              taskContent.CanonicalBytes,
			Composite:                       composite,
			CompositeChildResults:           compositeResults,
			CompositeSpecialistResultSet:    specialistSet,
			CompositeSpecialistResultDigest: specialistDigest,
			CompositeReviewVerdict:          reviewVerdict,
			CompositeCollaboration:          collaborationMaterial,
			WorkspaceTransfers:              transfer.Materials,
		},
	)
	if err != nil {
		return preparedPureChatRequestV1{}, fmt.Errorf(
			"%w: compile frozen context: %w",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	return preparedPureChatRequestV1{
		Request:                     compiled.Request,
		RequestCanonical:            bytes.Clone(compiled.RequestCanonical),
		ContextCompilationCanonical: bytes.Clone(compiled.CompilationCanonical),
	}, nil
}

func reviewerTightenedModelParametersV1(
	run currentstore.RunForLoop,
	parameters json.RawMessage,
) (json.RawMessage, error) {
	if run.Manifest.Composite == nil ||
		run.Manifest.Composite.Role != corecontract.CompositeRunRoleReviewerV1 {
		if run.CompositeRoot != nil {
			_, collaboration, err := collaborationRootForRunV1(run)
			if err != nil || !collaboration ||
				run.Manifest.Composite == nil ||
				run.Manifest.Composite.Role !=
					corecontract.CompositeRunRoleChildV1 {
				return nil, fmt.Errorf(
					"%w: non-Reviewer Run exposes an invalid composite Root projection: %v",
					ErrInvalidPureChatRequest,
					err,
				)
			}
		}
		return bytes.Clone(parameters), nil
	}
	root := run.CompositeRoot
	if root == nil || root.Composite == nil ||
		root.Composite.Role != corecontract.CompositeRunRoleRootV1 ||
		root.Composite.Plan == nil || root.Composite.Plan.Reviewer == nil {
		return nil, fmt.Errorf(
			"%w: Reviewer Run lacks its frozen Root Reviewer ref",
			ErrInvalidPureChatRequest,
		)
	}
	var reviewer *corecontract.CompositeReviewerRunRefV1
	switch run.Manifest.Composite.RepairRound {
	case 0:
		reviewer = root.Composite.Plan.Reviewer
	case corecontract.CompositeRepairRoundOneV1:
		if root.Composite.Plan.Decision == nil {
			return nil, fmt.Errorf(
				"%w: repair Reviewer lacks its frozen decision plan",
				ErrInvalidPureChatRequest,
			)
		}
		repair := root.Composite.Plan.Decision.RepairReviewer
		reviewer = &repair
	default:
		return nil, fmt.Errorf(
			"%w: Reviewer repair round is unsupported",
			ErrInvalidPureChatRequest,
		)
	}
	if reviewer.RunID != run.RunID ||
		reviewer.MemberSnapshotDigest != run.Member.MemberSnapshotDigest ||
		run.Manifest.Composite.RootRunID != root.RunID ||
		run.Manifest.Composite.ParentManifestDigest != root.ManifestDigest ||
		reviewer.MaxOutputTokens == 0 ||
		reviewer.MaxOutputTokens > corecontract.CompositeReviewerMaxOutputTokensV1 {
		return nil, fmt.Errorf(
			"%w: Reviewer Run differs from its frozen Root Reviewer ref",
			ErrInvalidPureChatRequest,
		)
	}
	tightened, err := corecontract.TightenReviewerModelParametersV1(
		parameters,
		reviewer.MaxOutputTokens,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: tighten Reviewer model parameters: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	return tightened, nil
}

func compositeContextCompilerInputV1(
	run currentstore.RunForLoop,
) (
	*corecontract.CompositeRunNodeV1,
	[]contextcompiler.CompositeChildResultV1,
	*corecontract.CompositeSpecialistResultSetV1,
	string,
	*contextcompiler.CompositeReviewVerdictMaterialV1,
	error,
) {
	if run.Manifest.Composite == nil {
		if len(run.CompositeChildren) != 0 || run.CompositeReviewer != nil {
			return nil, nil, nil, "", nil, fmt.Errorf(
				"%w: non-composite Run exposes Child results",
				ErrInvalidPureChatRequest,
			)
		}
		return nil, nil, nil, "", nil, nil
	}
	nodeValue := *run.Manifest.Composite
	if run.Manifest.Composite.Assignment != nil {
		assignment := *run.Manifest.Composite.Assignment
		nodeValue.Assignment = &assignment
	}
	if run.Manifest.Composite.Plan != nil {
		plan := *run.Manifest.Composite.Plan
		plan.Children = append(
			[]corecontract.CompositeChildRunRefV1(nil),
			run.Manifest.Composite.Plan.Children...,
		)
		if run.Manifest.Composite.Plan.Reviewer != nil {
			reviewer := *run.Manifest.Composite.Plan.Reviewer
			plan.Reviewer = &reviewer
		}
		nodeValue.Plan = &plan
	}
	node := &nodeValue
	if node.Role == corecontract.CompositeRunRoleChildV1 {
		if len(run.CompositeChildren) != 0 || run.CompositeReviewer != nil {
			return nil, nil, nil, "", nil, fmt.Errorf(
				"%w: composite Child cannot receive sibling results",
				ErrInvalidPureChatRequest,
			)
		}
		return node, nil, nil, "", nil, nil
	}
	if node.Role != corecontract.CompositeRunRoleRootV1 &&
		node.Role != corecontract.CompositeRunRoleReviewerV1 {
		return nil, nil, nil, "", nil, fmt.Errorf(
			"%w: unsupported composite context role %q",
			ErrInvalidPureChatRequest,
			node.Role,
		)
	}
	if node.Role == corecontract.CompositeRunRoleRootV1 && node.Plan == nil {
		return nil, nil, nil, "", nil, fmt.Errorf(
			"%w: composite root result set does not match its plan",
			ErrInvalidPureChatRequest,
		)
	}
	if node.Role == corecontract.CompositeRunRoleReviewerV1 && node.Plan != nil {
		return nil, nil, nil, "", nil, fmt.Errorf(
			"%w: composite Reviewer carries a Root plan",
			ErrInvalidPureChatRequest,
		)
	}
	if len(run.CompositeChildren) < corecontract.CompositeMinChildrenV1 ||
		len(run.CompositeChildren) > corecontract.CompositeMaxChildrenV1 ||
		(node.Plan != nil && len(run.CompositeChildren) != len(node.Plan.Children)) {
		return nil, nil, nil, "", nil, fmt.Errorf(
			"%w: composite Specialist result count does not close",
			ErrInvalidPureChatRequest,
		)
	}
	results := make(
		[]contextcompiler.CompositeChildResultV1,
		len(run.CompositeChildren),
	)
	specialistResults := make(
		[]corecontract.CompositeSpecialistResultV1,
		len(run.CompositeChildren),
	)
	for index := range run.CompositeChildren {
		child := run.CompositeChildren[index]
		assignment := child.Assignment
		var planned *corecontract.CompositeChildRunRefV1
		if node.Plan != nil {
			planned = &node.Plan.Children[index]
			assignment = planned.Assignment
		}
		if child.State != currentstore.CompositeChildSucceededV1 ||
			(planned != nil && (child.SlotID != planned.SlotID ||
				child.RunID != planned.RunID ||
				child.MemberSnapshotDigest != planned.MemberSnapshotDigest ||
				child.Assignment != planned.Assignment)) ||
			child.ResultRef == "" || len(child.OutputCanonical) == 0 {
			return nil, nil, nil, "", nil, fmt.Errorf(
				"%w: composite Child %d is not an exact successful merge input",
				ErrInvalidPureChatRequest,
				index,
			)
		}
		results[index] = contextcompiler.CompositeChildResultV1{
			SlotID:                child.SlotID,
			RunID:                 child.RunID,
			ChildManifestDigest:   child.ManifestDigest,
			MemberSnapshotDigest:  child.MemberSnapshotDigest,
			Assignment:            assignment,
			ResultRef:             child.ResultRef,
			TerminalRevision:      child.RunRevision,
			TerminalFrameRevision: child.FrameRevision,
			ResultCanonical:       bytes.Clone(child.OutputCanonical),
		}
		if planned != nil {
			results[index].AdmissionKey = planned.AdmissionKey
		}
		specialistResults[index] = corecontract.CompositeSpecialistResultV1{
			SlotID:                child.SlotID,
			FocusID:               assignment.FocusID,
			WeightBasisPoints:     assignment.WeightBasisPoints,
			RunID:                 child.RunID,
			ManifestDigest:        child.ManifestDigest,
			MemberSnapshotDigest:  child.MemberSnapshotDigest,
			ResultRef:             child.ResultRef,
			TerminalRunRevision:   child.RunRevision,
			TerminalFrameRevision: child.FrameRevision,
		}
	}
	if node.Role == corecontract.CompositeRunRoleRootV1 &&
		node.Plan.Reviewer == nil {
		if run.CompositeReviewer != nil {
			return nil, nil, nil, "", nil, fmt.Errorf(
				"%w: Reviewer-disabled Root exposes Reviewer state",
				ErrInvalidPureChatRequest,
			)
		}
		return node, results, nil, "", nil, nil
	}
	familyDigest := run.Manifest.ManifestDigest
	if node.Role == corecontract.CompositeRunRoleReviewerV1 {
		familyDigest = node.ParentManifestDigest
		if run.CompositeReviewer != nil {
			return nil, nil, nil, "", nil, fmt.Errorf(
				"%w: Reviewer Run exposes a sibling Reviewer projection",
				ErrInvalidPureChatRequest,
			)
		}
	}
	set, _, specialistDigest, err :=
		corecontract.NewCompositeSpecialistResultSetV1(
			corecontract.CompositeSpecialistResultSetV1{
				SchemaVersion: corecontract.CompositeSpecialistResultSetSchemaVersionV1,
				FamilyDigest:  familyDigest,
				TaskInputRef:  run.Manifest.TaskInputRef,
				Results:       specialistResults,
			},
		)
	if err != nil {
		return nil, nil, nil, "", nil, fmt.Errorf(
			"%w: build composite Specialist result set: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	if node.Role == corecontract.CompositeRunRoleReviewerV1 {
		return node, results, &set, specialistDigest, nil, nil
	}
	reviewer := run.CompositeReviewer
	if reviewer == nil || reviewer.State != currentstore.CompositeReviewerSucceededV1 ||
		reviewer.Verdict == nil ||
		reviewer.Verdict.Decision != corecontract.ReviewDecisionApproveV1 ||
		len(reviewer.OutputCanonical) == 0 ||
		len(reviewer.VerdictCanonical) == 0 {
		return nil, nil, nil, "", nil, fmt.Errorf(
			"%w: Reviewer-enabled Root lacks an exact APPROVE result",
			ErrInvalidPureChatRequest,
		)
	}
	reviewMaterial := &contextcompiler.CompositeReviewVerdictMaterialV1{
		ReviewerRunID:          reviewer.RunID,
		ReviewerManifestDigest: reviewer.ManifestDigest,
		MemberSnapshotDigest:   reviewer.MemberSnapshotDigest,
		AttemptID:              reviewer.AttemptID,
		LogicalStepID:          corecontract.CompositeReviewLogicalStepIDV1,
		ResultRef:              reviewer.ResultRef,
		TerminalRunRevision:    reviewer.RunRevision,
		TerminalFrameRevision:  reviewer.FrameRevision,
		ResultCanonical:        bytes.Clone(reviewer.OutputCanonical),
		VerdictCanonical:       bytes.Clone(reviewer.VerdictCanonical),
	}
	return node, results, &set, specialistDigest, reviewMaterial, nil
}

// prepareChatRequestV1 performs the optional local dynamic context reads and
// then enters
// the same pure Context Compiler used by Pure Chat. Reads are sequential in
// frozen Binding order, do not persist an Attempt, and cannot reach a remote
// execution class in the current local-only slices.
func (loop *UniversalLoop) prepareChatRequestV1(
	ctx context.Context,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
) (preparedPureChatRequestV1, error) {
	transfer, err := prepareWorkspaceTransferContextV1(run)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	dynamic, err := loop.invokeDynamicContextsV1(
		ctx,
		run,
		lease,
		transfer.DynamicTaskText,
	)
	if err != nil {
		return preparedPureChatRequestV1{}, err
	}
	return prepareChatRequestWithDynamicContextV1(run, dynamic, transfer)
}

func (loop *UniversalLoop) invokeDynamicContextsV1(
	ctx context.Context,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
	taskText string,
) (map[uint32]dynamicContextMaterialV1, error) {
	if !hasDynamicContextBindingV1(run) {
		return nil, nil
	}
	plan, materials, err := restoreContextCompilerBindings(run)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, nil
	}
	if taskText == "" {
		return nil, fmt.Errorf(
			"%w: dynamic context task text is empty",
			ErrInvalidPureChatRequest,
		)
	}
	scope := moduleapi.KnowledgeQueryScopeV1{
		TenantID: run.Manifest.TenantID,
		Workspace: moduleapi.KnowledgeObjectRefV1{
			ID:      run.Member.Workspace.ID,
			Version: run.Member.Workspace.Version,
			Digest:  run.Member.Workspace.Digest,
		},
		Agent: moduleapi.KnowledgeObjectRefV1{
			ID:      run.Member.Agent.ID,
			Version: run.Member.Agent.Version,
			Digest:  run.Member.Agent.Digest,
		},
		TaskInputRef: run.Manifest.TaskInputRef,
	}
	knowledgeDecisions, knowledgeDecisionSetDigest, err :=
		decideDynamicKnowledgeBindingsV1(
			*plan,
			materials,
			taskText,
		)
	if err != nil {
		return nil, err
	}

	preparedReads := make(
		[]preparedDynamicContextReadV1,
		0,
		len(plan.Bindings),
	)
	for bindingIndex, binding := range plan.Bindings {
		if binding.Provider.ExecutionClass == moduleapi.ExecutionDeclarative {
			continue
		}
		if binding.Provider.ExecutionClass !=
			moduleapi.ExecutionTrustedInProcess {
			return nil, fmt.Errorf(
				"%w: context Binding %d execution class %q is not a local knowledge read",
				ErrInvalidPureChatRequest,
				bindingIndex,
				binding.Provider.ExecutionClass,
			)
		}
		config, err := moduleapi.RestoreContextBindingConfigV1(
			materials[bindingIndex].ConfigCanonical,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: restore dynamic context Binding %d config: %v",
				ErrInvalidPureChatRequest,
				bindingIndex,
				err,
			)
		}
		authorityContent, err := exactContent(
			run,
			binding.AuthorityCeilingRef,
			currentstore.ContentAuthorityCeiling,
			fmt.Sprintf(
				"dynamic context Binding %d authority ceiling",
				bindingIndex,
			),
		)
		if err != nil {
			return nil, err
		}
		protocol, err := moduleapi.ContextBindingParametersSchemaVersionV1(
			config,
		)
		if err != nil {
			return nil, dynamicContextError(
				uint32(bindingIndex),
				"protocol",
				err,
			)
		}
		var knowledgeDecision *knowledgecore.BindingDecision
		if protocol == moduleapi.KnowledgeContextBindingSchemaV1 {
			decision, found := knowledgeDecisions[uint32(bindingIndex)]
			if !found {
				return nil, dynamicContextError(
					uint32(bindingIndex),
					"routing decision",
					fmt.Errorf("decision is absent"),
				)
			}
			decisionCopy := decision
			knowledgeDecision = &decisionCopy
		}
		prepared, err := loop.prepareOneDynamicContextV1(
			ctx,
			run,
			uint32(bindingIndex),
			binding,
			config,
			authorityContent.CanonicalBytes,
			taskText,
			scope,
			knowledgeDecision,
			knowledgeDecisionSetDigest,
		)
		if err != nil {
			return nil, err
		}
		preparedReads = append(preparedReads, prepared)
	}
	preparedReads, reuseBindings, err := prepareKnowledgeReuseV1(
		run,
		preparedReads,
		knowledgeDecisions,
		knowledgeDecisionSetDigest,
	)
	if err != nil {
		return nil, err
	}
	if err := loop.preflightKnowledgeReuseActivationsV1(
		ctx,
		run,
		preparedReads,
		reuseBindings,
	); err != nil {
		return nil, err
	}

	// Phase two starts only after every dynamic Binding's exact config,
	// protocol, authority, scope and limits have closed. This prevents Binding
	// order from allowing an earlier Provider call before a later invalid
	// NOT_SELECTED ceiling is discovered.
	dynamic := make(map[uint32]dynamicContextMaterialV1, len(preparedReads))
	deadline := narrowedModelDeadline(run.Manifest.Deadline, lease.ExpiresAt)
	for _, prepared := range preparedReads {
		material, err := loop.invokePreparedDynamicContextV1(
			ctx,
			run,
			lease,
			deadline,
			prepared,
		)
		if err != nil {
			return nil, err
		}
		dynamic[prepared.bindingIndex] = material
	}
	return dynamic, nil
}

// decideDynamicKnowledgeBindingsV1 runs the same pure K1 classifier used by
// the Context Compiler before any Provider can be armed or invoked. Binding
// indices remain the exact context.provide/v1 PortPlan indices; Memory and
// declarative Bindings are deliberately absent from the decision input.
func decideDynamicKnowledgeBindingsV1(
	plan moduleapi.PortPlan,
	materials []contextcompiler.BindingMaterialV1,
	taskText string,
) (map[uint32]knowledgecore.BindingDecision, string, error) {
	if len(plan.Bindings) != len(materials) {
		return nil, "", fmt.Errorf(
			"%w: dynamic routing material count does not match the frozen PortPlan",
			ErrInvalidPureChatRequest,
		)
	}
	inputs := make([]knowledgecore.BindingInput, 0)
	for bindingIndex, binding := range plan.Bindings {
		switch binding.Provider.ExecutionClass {
		case moduleapi.ExecutionDeclarative:
			continue
		case moduleapi.ExecutionTrustedInProcess:
			// Continue below.
		default:
			return nil, "", fmt.Errorf(
				"%w: context Binding %d execution class %q is not a local context read",
				ErrInvalidPureChatRequest,
				bindingIndex,
				binding.Provider.ExecutionClass,
			)
		}
		config, err := moduleapi.RestoreContextBindingConfigV1(
			materials[bindingIndex].ConfigCanonical,
		)
		if err != nil {
			return nil, "", dynamicContextError(
				uint32(bindingIndex),
				"routing config",
				err,
			)
		}
		protocol, err := moduleapi.ContextBindingParametersSchemaVersionV1(
			config,
		)
		if err != nil {
			return nil, "", dynamicContextError(
				uint32(bindingIndex),
				"routing protocol",
				err,
			)
		}
		if protocol != moduleapi.KnowledgeContextBindingSchemaV1 {
			continue
		}
		knowledge, err := moduleapi.RestoreKnowledgeContextBindingParametersV1(
			config,
		)
		if err != nil {
			return nil, "", dynamicContextError(
				uint32(bindingIndex),
				"routing parameters",
				err,
			)
		}
		inputs = append(inputs, knowledgecore.BindingInput{
			BindingIndex: uint32(bindingIndex),
			Config:       knowledge,
		})
	}
	set, _, decisionSetDigest, err := knowledgecore.Decide(taskText, inputs)
	if err != nil {
		return nil, "", fmt.Errorf(
			"%w: decide dynamic Knowledge routing: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	decisions := make(
		map[uint32]knowledgecore.BindingDecision,
		len(set.Decisions),
	)
	for _, decision := range set.Decisions {
		if _, duplicate := decisions[decision.BindingIndex]; duplicate {
			return nil, "", fmt.Errorf(
				"%w: duplicate dynamic Knowledge routing decision for Binding %d",
				ErrInvalidPureChatRequest,
				decision.BindingIndex,
			)
		}
		decisions[decision.BindingIndex] = decision
	}
	if len(decisions) != len(inputs) {
		return nil, "", fmt.Errorf(
			"%w: dynamic Knowledge routing decision count mismatch",
			ErrInvalidPureChatRequest,
		)
	}
	return decisions, decisionSetDigest, nil
}

func (loop *UniversalLoop) prepareOneDynamicContextV1(
	ctx context.Context,
	run currentstore.RunForLoop,
	bindingIndex uint32,
	binding moduleapi.PortBinding,
	config moduleapi.ContextBindingConfigV1,
	authorityCanonical []byte,
	taskText string,
	knowledgeScope moduleapi.KnowledgeQueryScopeV1,
	knowledgeDecision *knowledgecore.BindingDecision,
	knowledgeDecisionSetDigest string,
) (preparedDynamicContextReadV1, error) {
	protocol, err := moduleapi.ContextBindingParametersSchemaVersionV1(config)
	if err != nil {
		return preparedDynamicContextReadV1{}, fmt.Errorf(
			"%w: restore dynamic context Binding %d protocol: %v",
			ErrInvalidPureChatRequest,
			bindingIndex,
			err,
		)
	}
	var (
		stateCanonical            []byte
		requestCanonical          []byte
		requestDigest             string
		validateOutput            func([]byte) error
		preparedKnowledgeConfig   *moduleapi.KnowledgeContextBindingV1
		preparedKnowledgeRequest  *moduleapi.KnowledgeContextRequestV1
		preparedKnowledgeDecision *knowledgecore.BindingDecision
		preparedMemoryConfig      *moduleapi.MemoryContextBindingV1
		preparedMemoryAuthority   *moduleapi.MemoryAuthorityCeilingV1
		preparedMemorySnapshot    *moduleapi.AgentMemorySnapshotV1
		preparedMemorySnapshotRef *moduleapi.MemorySnapshotRefV1
		preparedMemoryScope       *moduleapi.MemoryQueryScopeV1
		memoryEvaluatedAt         uint64
	)
	switch protocol {
	case moduleapi.KnowledgeContextBindingSchemaV1:
		knowledgeConfig, err :=
			moduleapi.RestoreKnowledgeContextBindingParametersV1(config)
		if err != nil {
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"parameters",
				err,
			)
		}
		authority, err := moduleapi.RestoreKnowledgeAuthorityCeilingV1(
			authorityCanonical,
		)
		if err != nil {
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"authority",
				err,
			)
		}
		maxHits, maxTextBytes, err := moduleapi.ResolveKnowledgeLimitsV1(
			knowledgeConfig,
			authority,
			knowledgeScope,
		)
		if err != nil {
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"authority",
				err,
			)
		}
		if knowledgeDecision == nil ||
			knowledgeDecision.BindingIndex != bindingIndex {
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"routing decision",
				fmt.Errorf("exact decision is absent"),
			)
		}
		knowledgeConfigCopy := knowledgeConfig
		preparedKnowledgeConfig = &knowledgeConfigCopy
		knowledgeDecisionCopy := *knowledgeDecision
		knowledgeDecisionCopy.MatchedTerms = append(
			[]string(nil),
			knowledgeDecision.MatchedTerms...,
		)
		knowledgeDecisionCopy.CollectionTags = append(
			[]string(nil),
			knowledgeDecision.CollectionTags...,
		)
		preparedKnowledgeDecision = &knowledgeDecisionCopy
		switch knowledgeDecision.Decision {
		case knowledgecore.DecisionNotSelected:
			return preparedDynamicContextReadV1{
				bindingIndex:       bindingIndex,
				binding:            binding,
				protocol:           protocol,
				authorityCanonical: bytes.Clone(authorityCanonical),
				notSelected:        true,
				knowledgeConfig:    preparedKnowledgeConfig,
				knowledgeDecision:  preparedKnowledgeDecision,
				decisionSetDigest:  knowledgeDecisionSetDigest,
			}, nil
		case knowledgecore.DecisionFreshRAG:
			// Continue through the unchanged request/Gate/Host path.
		default:
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"routing decision",
				fmt.Errorf(
					"unsupported outcome %q",
					knowledgeDecision.Decision,
				),
			)
		}
		request, canonical, digest, err := moduleapi.NewKnowledgeContextRequestV1(
			moduleapi.KnowledgeContextRequestV1{
				SchemaVersion:     moduleapi.KnowledgeContextRequestSchemaV1,
				Source:            knowledgeConfig.Source,
				Scope:             knowledgeScope,
				QueryText:         taskText,
				MaxHits:           maxHits,
				MaxTotalTextBytes: maxTextBytes,
			},
		)
		if err != nil {
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"request",
				err,
			)
		}
		requestCanonical = canonical
		requestDigest = digest
		requestCopy := request
		preparedKnowledgeRequest = &requestCopy
		validateOutput = func(canonical []byte) error {
			output, _, err := moduleapi.RestoreKnowledgeContextOutputV1(canonical)
			if err != nil {
				return err
			}
			return moduleapi.ValidateKnowledgeContextOutputForRequestV1(
				request,
				output,
			)
		}
	case moduleapi.MemoryContextBindingSchemaV1:
		if knowledgeDecision != nil {
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"routing decision",
				fmt.Errorf("Memory Binding carries a Knowledge decision"),
			)
		}
		memoryConfig, _, err :=
			moduleapi.RestoreMemoryContextBindingParametersV1(config)
		if err != nil {
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"parameters",
				err,
			)
		}
		authority, err := moduleapi.RestoreMemoryAuthorityCeilingV1(
			authorityCanonical,
		)
		if err != nil {
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"authority",
				err,
			)
		}
		head, err := loop.store.GetCurrentAgentMemory(
			ctx,
			run.Manifest.TenantID,
			run.Member.Agent.ID,
		)
		if err != nil {
			return preparedDynamicContextReadV1{}, fmt.Errorf(
				"%w: load dynamic context Binding %d Memory head: %w",
				ErrInvalidPureChatRequest,
				bindingIndex,
				err,
			)
		}
		memoryScope := moduleapi.MemoryQueryScopeV1{
			TenantID: knowledgeScope.TenantID,
			Workspace: moduleapi.MemoryObjectRefV1{
				ID: knowledgeScope.Workspace.ID, Version: knowledgeScope.Workspace.Version,
				Digest: knowledgeScope.Workspace.Digest,
			},
			Agent: moduleapi.MemoryObjectRefV1{
				ID: knowledgeScope.Agent.ID, Version: knowledgeScope.Agent.Version,
				Digest: knowledgeScope.Agent.Digest,
			},
			TaskInputRef: knowledgeScope.TaskInputRef,
		}
		evaluatedAt := uint64(time.Now().UTC().UnixMilli())
		candidates, resolved, err := memorycore.FilterCandidates(
			head.Snapshot,
			head.SnapshotRef,
			memoryScope,
			memoryConfig,
			authority,
			evaluatedAt,
		)
		if err != nil {
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"candidates",
				err,
			)
		}
		request, canonical, digest, err := moduleapi.NewMemoryContextRequestV1(
			moduleapi.MemoryContextRequestV1{
				SchemaVersion:     moduleapi.MemoryContextRequestSchemaV1,
				Snapshot:          head.SnapshotRef,
				Scope:             memoryScope,
				QueryText:         taskText,
				EvaluatedAtUnixMS: evaluatedAt,
				Candidates:        candidates,
				MaxItems:          resolved.MaxItems,
				MaxTotalTextBytes: resolved.MaxTotalTextBytes,
			},
		)
		if err != nil {
			return preparedDynamicContextReadV1{}, dynamicContextError(
				bindingIndex,
				"request",
				err,
			)
		}
		stateCanonical = bytes.Clone(head.CanonicalBytes)
		requestCanonical = canonical
		requestDigest = digest
		memoryConfigCopy := memoryConfig
		memoryAuthorityCopy := authority
		memorySnapshotCopy := head.Snapshot
		memorySnapshotCopy.Entries = append(
			[]moduleapi.MemoryEntryV1(nil),
			head.Snapshot.Entries...,
		)
		memorySnapshotRefCopy := head.SnapshotRef
		memoryScopeCopy := memoryScope
		preparedMemoryConfig = &memoryConfigCopy
		preparedMemoryAuthority = &memoryAuthorityCopy
		preparedMemorySnapshot = &memorySnapshotCopy
		preparedMemorySnapshotRef = &memorySnapshotRefCopy
		preparedMemoryScope = &memoryScopeCopy
		memoryEvaluatedAt = evaluatedAt
		validateOutput = func(canonical []byte) error {
			output, _, err := moduleapi.RestoreMemoryContextOutputV1(canonical)
			if err != nil {
				return err
			}
			return moduleapi.ValidateMemoryContextOutputForRequestV1(
				request,
				output,
			)
		}
	default:
		return preparedDynamicContextReadV1{}, fmt.Errorf(
			"%w: dynamic context Binding %d has unsupported protocol %q",
			ErrInvalidPureChatRequest,
			bindingIndex,
			protocol,
		)
	}
	return preparedDynamicContextReadV1{
		bindingIndex:       bindingIndex,
		binding:            binding,
		protocol:           protocol,
		authorityCanonical: bytes.Clone(authorityCanonical),
		stateCanonical:     bytes.Clone(stateCanonical),
		requestCanonical:   bytes.Clone(requestCanonical),
		requestDigest:      requestDigest,
		validateOutput:     validateOutput,
		knowledgeConfig:    preparedKnowledgeConfig,
		knowledgeRequest:   preparedKnowledgeRequest,
		knowledgeDecision:  preparedKnowledgeDecision,
		decisionSetDigest:  knowledgeDecisionSetDigest,
		memoryConfig:       preparedMemoryConfig,
		memoryAuthority:    preparedMemoryAuthority,
		memorySnapshot:     preparedMemorySnapshot,
		memorySnapshotRef:  preparedMemorySnapshotRef,
		memoryScope:        preparedMemoryScope,
		memoryEvaluatedAt:  memoryEvaluatedAt,
	}, nil
}

// prepareKnowledgeReuseV1 applies K3 only after every dynamic Binding has
// completed the same config, authority, scope, limit and Memory-head preflight
// used by a fresh read. It selects at most one explicit routed hit and at most
// one latest exact-question predecessor; a miss never falls through to an
// older turn.
func prepareKnowledgeReuseV1(
	run currentstore.RunForLoop,
	prepared []preparedDynamicContextReadV1,
	decisions map[uint32]knowledgecore.BindingDecision,
	decisionSetDigest string,
) ([]preparedDynamicContextReadV1, map[uint32]struct{}, error) {
	targetIndex := -1
	qualifiedRouted := 0
	for index := range prepared {
		item := &prepared[index]
		if item.protocol != moduleapi.KnowledgeContextBindingSchemaV1 ||
			item.knowledgeConfig == nil || item.knowledgeConfig.Routing == nil {
			continue
		}
		decision, found := decisions[item.bindingIndex]
		if !found || item.knowledgeDecision == nil ||
			decision.BindingIndex != item.bindingIndex ||
			decision.Decision != knowledgecore.DecisionFreshRAG ||
			decision.MinMatchTerms == 0 ||
			uint32(len(decision.MatchedTerms)) < decision.MinMatchTerms {
			continue
		}
		qualifiedRouted++
		targetIndex = index
	}
	if qualifiedRouted != 1 || targetIndex < 0 {
		return prepared, nil, nil
	}
	target := &prepared[targetIndex]
	if target.knowledgeConfig.Routing.Reuse == nil ||
		target.knowledgeRequest == nil || target.knowledgeDecision == nil ||
		target.notSelected {
		return prepared, nil, nil
	}

	memoryIndex := -1
	for index := range prepared {
		if prepared[index].protocol != moduleapi.MemoryContextBindingSchemaV1 {
			continue
		}
		if memoryIndex >= 0 {
			return nil, nil, fmt.Errorf(
				"%w: exact-question reuse found more than one Memory Binding",
				ErrInvalidPureChatRequest,
			)
		}
		memoryIndex = index
	}
	if memoryIndex < 0 {
		return prepared, nil, nil
	}
	memory := &prepared[memoryIndex]
	if memory.memoryConfig == nil || memory.memoryAuthority == nil ||
		memory.memorySnapshot == nil || memory.memorySnapshotRef == nil ||
		memory.memoryScope == nil || memory.memoryEvaluatedAt == 0 {
		return nil, nil, fmt.Errorf(
			"%w: exact-question reuse Memory preflight is incomplete",
			ErrInvalidPureChatRequest,
		)
	}

	turn := run.Manifest.ConversationTurn
	if turn == nil || turn.TurnIndex <= 1 {
		return prepared, nil, nil
	}
	var source *currentstore.ConversationHistoryTurnRecord
	for index := len(run.ConversationHistory) - 1; index >= 0; index-- {
		entry := &run.ConversationHistory[index]
		if entry.UserContent.Digest == run.Manifest.TaskInputRef {
			source = entry
			break
		}
	}
	if source == nil || source.SourceContextCompilation == nil {
		return prepared, nil, nil
	}
	if source.SourceContextCompilationAttemptID == "" {
		return nil, nil, fmt.Errorf(
			"%w: exact-question reuse source Compilation Attempt is absent",
			ErrInvalidPureChatRequest,
		)
	}
	// K3 intentionally permits only a Compilation owned by the successful
	// terminal assistant Attempt. An Action predecessor's summary may come
	// from model-1 for M2, but its Knowledge retrieval is not a K3 source.
	if source.SourceContextCompilationAttemptID != source.SourceAttemptID {
		return prepared, nil, nil
	}
	if source.TurnIndex == 0 || source.TurnIndex >= turn.TurnIndex {
		return nil, nil, fmt.Errorf(
			"%w: exact-question reuse source turn is invalid",
			ErrInvalidPureChatRequest,
		)
	}
	lookback := turn.TurnIndex - source.TurnIndex
	if lookback > uint64(moduleapi.MaxKnowledgeReuseLookbackTurnsV1) {
		return prepared, nil, nil
	}

	sourceRecord := source.SourceContextCompilation
	if sourceRecord.Kind != currentstore.ContentContextCompilation ||
		sourceRecord.MediaType != "application/json" {
		return nil, nil, fmt.Errorf(
			"%w: exact-question reuse source Compilation content is invalid",
			ErrInvalidPureChatRequest,
		)
	}
	computedSourceDigest, err := currentstore.ComputeContentDigest(
		currentstore.ContentContextCompilation,
		sourceRecord.MediaType,
		sourceRecord.CanonicalBytes,
	)
	if err != nil || computedSourceDigest != sourceRecord.Digest {
		return nil, nil, fmt.Errorf(
			"%w: exact-question reuse source Compilation digest does not close",
			ErrInvalidPureChatRequest,
		)
	}
	sourceCompilation, err := corecontract.RestoreContextCompilationV1(
		sourceRecord.CanonicalBytes,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"%w: restore exact-question reuse source Compilation: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	var sourceRetrieval *corecontract.KnowledgeRetrievalEvidenceV1
	for index := range sourceCompilation.KnowledgeRetrievals {
		retrieval := &sourceCompilation.KnowledgeRetrievals[index]
		if retrieval.BindingIndex != target.bindingIndex {
			continue
		}
		if sourceRetrieval != nil {
			return nil, nil, fmt.Errorf(
				"%w: exact-question source repeats a Knowledge Binding",
				ErrInvalidPureChatRequest,
			)
		}
		sourceRetrieval = retrieval
	}
	if sourceRetrieval == nil || sourceRetrieval.Provenance == nil ||
		len(sourceRetrieval.Hits) == 0 {
		return prepared, nil, nil
	}

	proof, found, err := memorycore.SelectKnowledgeReuseCounterProofV1(
		*memory.memorySnapshot,
		*memory.memorySnapshotRef,
		*memory.memoryScope,
		*memory.memoryConfig,
		*memory.memoryAuthority,
		memory.memoryEvaluatedAt,
		target.knowledgeDecision.CollectionTags,
		target.knowledgeDecision.MatchedTerms,
		target.knowledgeConfig.Routing.Reuse.MinCategoryCount,
		target.knowledgeConfig.Routing.Reuse.MinRepeatedTermCount,
	)
	if err != nil {
		return nil, nil, dynamicContextError(
			memory.bindingIndex,
			"reuse counter proof",
			err,
		)
	}
	if !found {
		return prepared, nil, nil
	}
	category, err := moduleapi.NewMemoryCandidateV1(proof.CategoryCounter)
	if err != nil {
		return nil, nil, dynamicContextError(
			memory.bindingIndex,
			"reuse category counter",
			err,
		)
	}
	repeated, err := moduleapi.NewMemoryCandidateV1(proof.RepeatedTermCounter)
	if err != nil {
		return nil, nil, dynamicContextError(
			memory.bindingIndex,
			"reuse repeated-term counter",
			err,
		)
	}

	currentRequest := *target.knowledgeRequest
	sourceRequest := currentRequest
	sourceRequest.Source = sourceRetrieval.Source
	sourceRequest.Scope = sourceRetrieval.Scope
	_, _, sourceRequestDigest, err := moduleapi.NewKnowledgeContextRequestV1(
		sourceRequest,
	)
	if err != nil || sourceRequestDigest != sourceRetrieval.RequestDigest {
		return prepared, nil, nil
	}
	sourceOutput, _, sourceOutputDigest, err :=
		moduleapi.NewKnowledgeContextOutputV1(
			moduleapi.KnowledgeContextOutputV1{
				SchemaVersion: moduleapi.KnowledgeContextOutputSchemaV1,
				RequestDigest: sourceRetrieval.RequestDigest,
				Source:        sourceRetrieval.Source,
				Hits:          sourceRetrieval.Hits,
			},
		)
	if err != nil || sourceOutputDigest != sourceRetrieval.OutputDigest {
		return prepared, nil, nil
	}
	evaluation, err := knowledgecore.EvaluateReuseV1(
		knowledgecore.ReuseEvaluationInputV1{
			Policy:            *target.knowledgeConfig.Routing.Reuse,
			EvaluatedAtUnixMS: memory.memoryEvaluatedAt,
			Current: knowledgecore.ReuseCurrentFactsV1{
				ConversationID:          turn.ConversationID,
				Request:                 currentRequest,
				Provider:                target.binding.Provider,
				ConfigDigest:            target.binding.ConfigRef,
				AuthorityDigest:         target.binding.AuthorityCeilingRef,
				RoutingAlgorithmVersion: knowledgecore.RoutingAlgorithmVersionV1,
				DecisionSetDigest:       decisionSetDigest,
				Decision:                *target.knowledgeDecision,
			},
			Candidate: knowledgecore.ReuseCandidateV1{
				ConversationID:                 turn.ConversationID,
				SourceAttemptID:                source.SourceAttemptID,
				SourceCompilationDigest:        sourceRecord.Digest,
				Request:                        sourceRequest,
				Output:                         sourceOutput,
				Provider:                       sourceRetrieval.Provenance.Provider,
				ConfigDigest:                   sourceRetrieval.ConfigRef,
				AuthorityDigest:                sourceRetrieval.AuthorityCeilingRef,
				BindingIndex:                   sourceRetrieval.BindingIndex,
				RoutingAlgorithmVersion:        sourceRetrieval.Provenance.RoutingAlgorithmVersion,
				DecisionSetDigest:              sourceRetrieval.Provenance.DecisionSetDigest,
				RetrievedAtUnixMS:              sourceRetrieval.Provenance.RetrievedAtUnixMS,
				LookbackTurns:                  uint32(lookback),
				IsLatestExactQuestionCandidate: true,
				FreshRetrieval:                 true,
				SourceAttemptSucceeded:         true,
			},
			CategoryCounter:     category,
			RepeatedTermCounter: repeated,
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"%w: evaluate exact-question Knowledge reuse: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	if evaluation.Decision != knowledgecore.DecisionReuse ||
		evaluation.Candidate == nil || evaluation.CategoryCounter == nil ||
		evaluation.RepeatedTermCounter == nil {
		return prepared, nil, nil
	}
	fresh := cloneKnowledgeRetrievalEvidenceV1(*sourceRetrieval)
	target.knowledgeReuse = &corecontract.KnowledgeReuseEvidenceV1{
		SourceConversationID: turn.ConversationID,
		SourceTurnIndex:      source.TurnIndex,
		SourceRunID:          source.SourceRunID,
		SourceAttemptID:      source.SourceAttemptID,
		SourceCompilationRef: sourceRecord.Digest,
		FreshRetrieval:       fresh,
		MemoryBindingIndex:   memory.bindingIndex,
		CategoryCounter:      *evaluation.CategoryCounter,
		RepeatedTermCounter:  *evaluation.RepeatedTermCounter,
	}
	return prepared, map[uint32]struct{}{
		target.bindingIndex: {},
		memory.bindingIndex: {},
	}, nil
}

func cloneKnowledgeRetrievalEvidenceV1(
	input corecontract.KnowledgeRetrievalEvidenceV1,
) corecontract.KnowledgeRetrievalEvidenceV1 {
	cloned := input
	cloned.Hits = make([]moduleapi.KnowledgeHitV1, len(input.Hits))
	for index, hit := range input.Hits {
		cloned.Hits[index] = hit
		cloned.Hits[index].VisibleTo = append(
			[]moduleapi.KnowledgeScopeRuleV1(nil),
			hit.VisibleTo...,
		)
	}
	if input.Provenance != nil {
		provenance := *input.Provenance
		cloned.Provenance = &provenance
	}
	return cloned
}

// preflightKnowledgeReuseActivationsV1 checks every Provider on which an
// actual REUSE depends before the phase-two loop can invoke even the Memory
// Provider. ModuleHost repeats the check at invocation time for the remaining
// read, closing the revocation race without arming a Knowledge Gate.
func (loop *UniversalLoop) preflightKnowledgeReuseActivationsV1(
	ctx context.Context,
	run currentstore.RunForLoop,
	prepared []preparedDynamicContextReadV1,
	required map[uint32]struct{},
) error {
	if len(required) == 0 {
		return nil
	}
	checked := 0
	for _, item := range prepared {
		if _, needed := required[item.bindingIndex]; !needed {
			continue
		}
		if item.notSelected || item.binding.Provider.ExecutionClass !=
			moduleapi.ExecutionTrustedInProcess {
			return dynamicContextError(
				item.bindingIndex,
				"reuse activation",
				fmt.Errorf("invalid reuse dependency"),
			)
		}
		if err := loop.currentActivation.CheckCurrentActivation(
			ctx,
			run.RunID,
			contextProvidePortV1,
			item.binding.Provider,
		); err != nil {
			return fmt.Errorf(
				"%w: dynamic context Binding %d reuse activation: %w",
				ErrInvalidPureChatRequest,
				item.bindingIndex,
				fmt.Errorf("%w: %w", modulehost.ErrCurrentActivationDenied, err),
			)
		}
		checked++
	}
	if checked != len(required) {
		return fmt.Errorf(
			"%w: exact-question reuse activation closure is incomplete",
			ErrInvalidPureChatRequest,
		)
	}
	return nil
}

func (loop *UniversalLoop) invokePreparedDynamicContextV1(
	ctx context.Context,
	run currentstore.RunForLoop,
	lease currentstore.RunLease,
	deadline time.Time,
	prepared preparedDynamicContextReadV1,
) (dynamicContextMaterialV1, error) {
	if prepared.notSelected {
		if len(prepared.stateCanonical) != 0 ||
			len(prepared.requestCanonical) != 0 ||
			prepared.requestDigest != "" || prepared.validateOutput != nil {
			return dynamicContextMaterialV1{}, dynamicContextError(
				prepared.bindingIndex,
				"routing decision",
				fmt.Errorf("NOT_SELECTED acquired dynamic request material"),
			)
		}
		return dynamicContextMaterialV1{
			AuthorityCanonical: bytes.Clone(prepared.authorityCanonical),
		}, nil
	}
	if prepared.knowledgeReuse != nil {
		if prepared.protocol != moduleapi.KnowledgeContextBindingSchemaV1 ||
			prepared.knowledgeConfig == nil || prepared.knowledgeRequest == nil ||
			prepared.knowledgeDecision == nil ||
			prepared.knowledgeConfig.Routing == nil ||
			prepared.knowledgeConfig.Routing.Reuse == nil ||
			len(prepared.stateCanonical) != 0 {
			return dynamicContextMaterialV1{}, dynamicContextError(
				prepared.bindingIndex,
				"reuse",
				fmt.Errorf("prepared reuse closure is invalid"),
			)
		}
		reuse := *prepared.knowledgeReuse
		reuse.FreshRetrieval = cloneKnowledgeRetrievalEvidenceV1(
			prepared.knowledgeReuse.FreshRetrieval,
		)
		return dynamicContextMaterialV1{
			AuthorityCanonical: bytes.Clone(prepared.authorityCanonical),
			KnowledgeReuse:     &reuse,
		}, nil
	}
	if len(prepared.requestCanonical) == 0 ||
		!moduleapi.ValidSHA256(prepared.requestDigest) ||
		prepared.validateOutput == nil {
		return dynamicContextMaterialV1{}, dynamicContextError(
			prepared.bindingIndex,
			"prepared request",
			fmt.Errorf("exact request closure is absent"),
		)
	}

	invocationID := contextReadInvocationIDV1(
		run,
		prepared.bindingIndex,
		prepared.requestDigest,
	)
	gate, invocation, err := ArmContextReadGate(
		run,
		lease,
		prepared.bindingIndex,
		invocationID,
		prepared.requestCanonical,
		deadline,
	)
	if err != nil {
		return dynamicContextMaterialV1{}, err
	}
	host, err := modulehost.NewInvocationHostWithCurrentActivation(
		gate,
		loop.registry,
		loop.currentActivation,
	)
	if err != nil {
		return dynamicContextMaterialV1{}, dynamicContextError(
			prepared.bindingIndex,
			"Host",
			err,
		)
	}
	result, err := host.Invoke(ctx, invocation)
	if err != nil {
		return dynamicContextMaterialV1{}, fmt.Errorf(
			"%w: invoke dynamic context Binding %d: %w",
			ErrInvalidPureChatRequest,
			prepared.bindingIndex,
			err,
		)
	}
	if result.InvocationID != invocationID ||
		result.Provider != prepared.binding.Provider ||
		result.Outcome != modulehost.InvocationSucceeded ||
		len(result.UsageReceipt) != 0 {
		return dynamicContextMaterialV1{}, fmt.Errorf(
			"%w: dynamic context Binding %d returned a non-local or non-success result",
			ErrInvalidPureChatRequest,
			prepared.bindingIndex,
		)
	}
	if err := prepared.validateOutput(result.Output); err != nil {
		return dynamicContextMaterialV1{}, dynamicContextError(
			prepared.bindingIndex,
			"output",
			err,
		)
	}
	var provenance *corecontract.KnowledgeRetrievalProvenanceV1
	if prepared.protocol == moduleapi.KnowledgeContextBindingSchemaV1 &&
		prepared.knowledgeConfig != nil &&
		prepared.knowledgeConfig.Routing != nil &&
		prepared.knowledgeConfig.Routing.Reuse != nil {
		retrievedAt := uint64(time.Now().UTC().UnixMilli())
		if retrievedAt == 0 || retrievedAt > moduleapi.MaxMemorySafeIntegerV1 ||
			!moduleapi.ValidSHA256(prepared.decisionSetDigest) {
			return dynamicContextMaterialV1{}, dynamicContextError(
				prepared.bindingIndex,
				"fresh provenance",
				fmt.Errorf("retrieval time or decision-set digest is invalid"),
			)
		}
		provenance = &corecontract.KnowledgeRetrievalProvenanceV1{
			Provider:                prepared.binding.Provider,
			RoutingAlgorithmVersion: knowledgecore.RoutingAlgorithmVersionV1,
			DecisionSetDigest:       prepared.decisionSetDigest,
			RetrievedAtUnixMS:       retrievedAt,
		}
	}
	return dynamicContextMaterialV1{
		AuthorityCanonical:  bytes.Clone(prepared.authorityCanonical),
		StateCanonical:      bytes.Clone(prepared.stateCanonical),
		RequestCanonical:    bytes.Clone(prepared.requestCanonical),
		OutputCanonical:     bytes.Clone(result.Output),
		KnowledgeProvenance: provenance,
	}, nil
}

func dynamicContextError(bindingIndex uint32, part string, err error) error {
	return fmt.Errorf(
		"%w: dynamic context Binding %d %s: %v",
		ErrInvalidPureChatRequest,
		bindingIndex,
		part,
		err,
	)
}

// hasDynamicContextBindingV1 is a cheap snapshot-only guard. In particular,
// the default Pure Chat path does not restore context bodies twice, resolve a
// Registry entry, or construct a Module Host merely to discover that dynamic
// Context is absent.
func hasDynamicContextBindingV1(run currentstore.RunForLoop) bool {
	for _, plan := range run.Member.PortPlans {
		if plan.Port.Name != moduleapi.PortNameContextProvide ||
			plan.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		for _, binding := range plan.Bindings {
			if binding.Provider.ExecutionClass !=
				moduleapi.ExecutionDeclarative {
				return true
			}
		}
	}
	return false
}

func applyDynamicContextMaterials(
	plan *moduleapi.PortPlan,
	materials []contextcompiler.BindingMaterialV1,
	dynamic map[uint32]dynamicContextMaterialV1,
) error {
	if plan == nil {
		if len(dynamic) != 0 {
			return fmt.Errorf(
				"%w: dynamic context material has no exact frozen PortPlan",
				ErrInvalidPureChatRequest,
			)
		}
		return nil
	}
	if len(plan.Bindings) != len(materials) {
		return fmt.Errorf(
			"%w: dynamic context material has no exact frozen PortPlan",
			ErrInvalidPureChatRequest,
		)
	}
	expected := 0
	for bindingIndex, binding := range plan.Bindings {
		index := uint32(bindingIndex)
		material, found := dynamic[index]
		switch binding.Provider.ExecutionClass {
		case moduleapi.ExecutionDeclarative:
			if found {
				return fmt.Errorf(
					"%w: declarative context Binding %d carries dynamic material",
					ErrInvalidPureChatRequest,
					bindingIndex,
				)
			}
			continue
		case moduleapi.ExecutionTrustedInProcess:
			expected++
			if !found {
				return fmt.Errorf(
					"%w: dynamic context Binding %d has no exact material",
					ErrInvalidPureChatRequest,
					bindingIndex,
				)
			}
		default:
			return fmt.Errorf(
				"%w: context Binding %d has unsupported execution class %q",
				ErrInvalidPureChatRequest,
				bindingIndex,
				binding.Provider.ExecutionClass,
			)
		}
		materials[bindingIndex].AuthorityCanonical =
			bytes.Clone(material.AuthorityCanonical)
		materials[bindingIndex].DynamicStateCanonical =
			bytes.Clone(material.StateCanonical)
		materials[bindingIndex].DynamicRequestCanonical =
			bytes.Clone(material.RequestCanonical)
		materials[bindingIndex].DynamicOutputCanonical =
			bytes.Clone(material.OutputCanonical)
		if material.KnowledgeProvenance != nil &&
			material.KnowledgeReuse != nil {
			return fmt.Errorf(
				"%w: dynamic context Binding %d carries both fresh and reuse evidence",
				ErrInvalidPureChatRequest,
				bindingIndex,
			)
		}
		if material.KnowledgeProvenance != nil {
			provenance := *material.KnowledgeProvenance
			materials[bindingIndex].KnowledgeProvenance = &provenance
		}
		if material.KnowledgeReuse != nil {
			reuse := *material.KnowledgeReuse
			reuse.FreshRetrieval = cloneKnowledgeRetrievalEvidenceV1(
				material.KnowledgeReuse.FreshRetrieval,
			)
			materials[bindingIndex].KnowledgeReuse = &reuse
		}
	}
	if len(dynamic) != expected {
		return fmt.Errorf(
			"%w: dynamic context material count %d does not match %d TRUSTED_IN_PROCESS Bindings",
			ErrInvalidPureChatRequest,
			len(dynamic),
			expected,
		)
	}
	return nil
}

func contextReadInvocationIDV1(
	run currentstore.RunForLoop,
	bindingIndex uint32,
	requestDigest string,
) string {
	payload := []byte(
		run.RunID + "\x00" + run.Member.MemberSnapshotDigest + "\x00" +
			strconv.FormatUint(uint64(bindingIndex), 10) + "\x00" +
			requestDigest,
	)
	return "context-read-" + moduleapi.Digest(
		"freeagent.context-read-invocation/v1",
		payload,
	)
}

func restoreFrozenModelProfile(
	run currentstore.RunForLoop,
	binding moduleapi.PortBinding,
	config moduleapi.ModelBindingConfigV1,
) (*corecontract.ModelProfileRef, []byte, error) {
	if run.Member.ModelProfile == nil {
		return nil, nil, nil
	}
	ref := *run.Member.ModelProfile
	content, err := exactContent(
		run,
		ref.Digest,
		currentstore.ContentConfig,
		"ModelProfile",
	)
	if err != nil {
		return nil, nil, err
	}
	if content.MediaType != "application/json" {
		return nil, nil, fmt.Errorf(
			"%w: ModelProfile media type is %q, want application/json",
			ErrInvalidPureChatRequest,
			content.MediaType,
		)
	}
	profile, err := corecontract.RestoreModelProfileV1(
		content.CanonicalBytes,
		ref,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"%w: restore frozen ModelProfile: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	if err := corecontract.ValidateModelProfileBindingV1(
		profile,
		binding,
		config,
	); err != nil {
		return nil, nil, fmt.Errorf(
			"%w: ModelProfile does not match frozen model Binding: %v",
			ErrInvalidPureChatRequest,
			err,
		)
	}
	return &ref, bytes.Clone(content.CanonicalBytes), nil
}

func restoreContextCompilerBindings(
	run currentstore.RunForLoop,
) (*moduleapi.PortPlan, []contextcompiler.BindingMaterialV1, error) {
	var selected *moduleapi.PortPlan
	for index := range run.Member.PortPlans {
		plan := run.Member.PortPlans[index]
		if plan.Port.Name != moduleapi.PortNameContextProvide ||
			plan.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		if selected != nil {
			return nil, nil, fmt.Errorf(
				"%w: context.provide/v1 has more than one PortPlan",
				ErrInvalidPureChatRequest,
			)
		}
		frozen, err := moduleapi.NewPortPlan(plan)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"%w: restore context PortPlan: %v",
				ErrInvalidPureChatRequest,
				err,
			)
		}
		selected = &frozen
	}
	if selected == nil {
		return nil, nil, nil
	}

	materials := make(
		[]contextcompiler.BindingMaterialV1,
		len(selected.Bindings),
	)
	for bindingIndex, binding := range selected.Bindings {
		config, err := exactContent(
			run,
			binding.ConfigRef,
			currentstore.ContentConfig,
			fmt.Sprintf("context Binding %d config", bindingIndex),
		)
		if err != nil {
			return nil, nil, err
		}
		material := contextcompiler.BindingMaterialV1{
			ConfigCanonical: bytes.Clone(config.CanonicalBytes),
			StaticContextCanonicals: make(
				[][]byte,
				len(binding.StaticContextRefs),
			),
		}
		for refIndex, ref := range binding.StaticContextRefs {
			content, err := exactContent(
				run,
				ref,
				currentstore.ContentStaticContext,
				fmt.Sprintf(
					"static context binding %d ref %d",
					bindingIndex,
					refIndex,
				),
			)
			if err != nil {
				return nil, nil, err
			}
			material.StaticContextCanonicals[refIndex] =
				bytes.Clone(content.CanonicalBytes)
		}
		materials[bindingIndex] = material
	}
	return selected, materials, nil
}

func restoreContextCompilerHistory(
	run currentstore.RunForLoop,
) ([]contextcompiler.HistoryTurnV1, error) {
	history := make([]contextcompiler.HistoryTurnV1, len(run.History))
	for index, entry := range run.History {
		expectedSequence := uint64(index + 1)
		if entry.Sequence != expectedSequence ||
			entry.Role != string(moduleapi.ModelRoleAssistant) {
			return nil, fmt.Errorf(
				"%w: History entry %d must be contiguous ASSISTANT sequence %d",
				ErrInvalidPureChatRequest,
				index,
				expectedSequence,
			)
		}
		if entry.Content.Kind != currentstore.ContentModelResult {
			return nil, fmt.Errorf(
				"%w: History entry %d has content kind %q, want %q",
				ErrInvalidPureChatRequest,
				index,
				entry.Content.Kind,
				currentstore.ContentModelResult,
			)
		}
		output, err := moduleapi.RestoreModelGenerateOutputV1(
			entry.Content.CanonicalBytes,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: restore History entry %d: %v",
				ErrInvalidPureChatRequest,
				index,
				err,
			)
		}
		history[index] = contextcompiler.HistoryTurnV1{
			Sequence:            entry.Sequence,
			SourceContentDigest: entry.Content.Digest,
			Message: moduleapi.ModelMessageV1{
				Role:    moduleapi.ModelRoleAssistant,
				Content: output.AssistantText,
			},
		}
	}
	return history, nil
}

func restoreContextCompilerConversationHistory(
	run currentstore.RunForLoop,
) ([]contextcompiler.ConversationHistoryTurnV1, error) {
	turn := run.Manifest.ConversationTurn
	if turn == nil {
		if len(run.ConversationHistory) != 0 {
			return nil, fmt.Errorf(
				"%w: non-Conversation Run contains Conversation History",
				ErrInvalidPureChatRequest,
			)
		}
		return nil, nil
	}
	if len(run.History) != 0 {
		return nil, fmt.Errorf(
			"%w: current Conversation Run cannot reuse its own History as predecessor turns",
			ErrInvalidPureChatRequest,
		)
	}
	wantTurns := turn.TurnIndex - 1
	if uint64(len(run.ConversationHistory)) != wantTurns {
		return nil, fmt.Errorf(
			"%w: Conversation History contains %d turns, want %d",
			ErrInvalidPureChatRequest,
			len(run.ConversationHistory),
			wantTurns,
		)
	}
	history := make(
		[]contextcompiler.ConversationHistoryTurnV1,
		len(run.ConversationHistory),
	)
	for index, entry := range run.ConversationHistory {
		expectedIndex := uint64(index + 1)
		if entry.TurnIndex != expectedIndex || entry.SourceRunID == "" ||
			entry.SourceAttemptID == "" {
			return nil, fmt.Errorf(
				"%w: Conversation History turn %d has invalid identity",
				ErrInvalidPureChatRequest,
				index,
			)
		}
		if entry.UserContent.Kind != currentstore.ContentTaskInput {
			return nil, fmt.Errorf(
				"%w: Conversation History turn %d USER kind is %q",
				ErrInvalidPureChatRequest,
				index,
				entry.UserContent.Kind,
			)
		}
		user, err := corecontract.RestoreTaskInputV1(
			entry.UserContent.CanonicalBytes,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: restore Conversation USER turn %d: %v",
				ErrInvalidPureChatRequest,
				index,
				err,
			)
		}
		if entry.AssistantContent.Kind != currentstore.ContentModelResult {
			return nil, fmt.Errorf(
				"%w: Conversation History turn %d ASSISTANT kind is %q",
				ErrInvalidPureChatRequest,
				index,
				entry.AssistantContent.Kind,
			)
		}
		assistant, err := moduleapi.RestoreModelGenerateOutputV1(
			entry.AssistantContent.CanonicalBytes,
		)
		if err != nil || assistant.ActionRequest != nil ||
			assistant.AssistantText == "" {
			return nil, fmt.Errorf(
				"%w: restore Conversation ASSISTANT turn %d: %v",
				ErrInvalidPureChatRequest,
				index,
				err,
			)
		}
		history[index] = contextcompiler.ConversationHistoryTurnV1{
			TurnIndex:                    entry.TurnIndex,
			UserSourceContentDigest:      entry.UserContent.Digest,
			AssistantSourceContentDigest: entry.AssistantContent.Digest,
			UserMessage: moduleapi.ModelMessageV1{
				Role:    moduleapi.ModelRoleUser,
				Content: user.Text,
			},
			AssistantMessage: moduleapi.ModelMessageV1{
				Role:    moduleapi.ModelRoleAssistant,
				Content: assistant.AssistantText,
			},
		}
	}
	return history, nil
}

func exactModelGenerateBinding(
	run currentstore.RunForLoop,
) (moduleapi.PortBinding, error) {
	planCount := 0
	var binding moduleapi.PortBinding
	for _, plan := range run.Member.PortPlans {
		if plan.Port.Name != moduleapi.PortNameModelGenerate ||
			plan.Port.ExactVersion != moduleapi.PortVersionV1 {
			continue
		}
		planCount++
		if len(plan.Bindings) != 1 {
			return moduleapi.PortBinding{}, fmt.Errorf(
				"%w: model.generate/v1 must have exactly one Binding",
				ErrInvalidPureChatRequest,
			)
		}
		binding = plan.Bindings[0]
	}
	if planCount != 1 {
		return moduleapi.PortBinding{}, fmt.Errorf(
			"%w: model.generate/v1 must have exactly one PortPlan",
			ErrInvalidPureChatRequest,
		)
	}
	return binding, nil
}

func exactContent(
	run currentstore.RunForLoop,
	digest string,
	kind currentstore.ContentKind,
	subject string,
) (currentstore.ContentRecord, error) {
	content, found := run.FindContent(digest)
	if !found {
		return currentstore.ContentRecord{}, fmt.Errorf(
			"%w: %s content %q is absent",
			ErrInvalidPureChatRequest,
			subject,
			digest,
		)
	}
	if content.Kind != kind {
		return currentstore.ContentRecord{}, fmt.Errorf(
			"%w: %s content kind is %q, want %q",
			ErrInvalidPureChatRequest,
			subject,
			content.Kind,
			kind,
		)
	}
	return content, nil
}
