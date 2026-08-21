package contextcompiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	workspaceTaskExtractSchemaVersionV1 = "workspace-task-extract/v1"
	workspaceRepairExtractSchemaV1      = "workspace-repair-extract/v1"
	workspaceTaskOmissionV1             = "\n[... task omitted ...]\n"
)

// WorkspaceTransferMaterialV1 is trusted Store/Host material for one exact
// cross-Workspace context edge. EnvelopeRef is the ContentRecord digest while
// EnvelopeDigest is the independent protocol digest returned by
// corecontract.NewWorkspaceTransferEnvelopeV1. The compiler re-proves both.
// Grant bodies are deliberately absent: historical Control authorization is
// a Store admission/dispatch responsibility.
type WorkspaceTransferMaterialV1 struct {
	EnvelopeRef       string
	EnvelopeDigest    string
	EnvelopeCanonical []byte
	ResolvedPayload   corecontract.WorkspaceTransferResolvedPayloadV1
	RootManifest      corecontract.RunManifest
	ChildManifest     corecontract.RunManifest
}

type restoredWorkspaceTransferMaterialV1 struct {
	envelope corecontract.WorkspaceTransferEnvelopeV1
	payload  corecontract.WorkspaceTransferResolvedPayloadV1
	root     corecontract.RunManifest
	child    corecontract.RunManifest
	evidence corecontract.WorkspaceTransferEvidenceV1
}

type workspaceTransferCompilationV1 struct {
	taskText *string
	evidence []corecontract.WorkspaceTransferEvidenceV1
}

func restoreWorkspaceTransferCompilationV1(
	input CompileInputV1,
) (workspaceTransferCompilationV1, error) {
	if len(input.WorkspaceTransfers) > corecontract.CompositeMaxChildrenV1 {
		return workspaceTransferCompilationV1{}, invalidInput(
			"Workspace transfers",
			fmt.Errorf("more than %d edges", corecontract.CompositeMaxChildrenV1),
		)
	}
	if input.Composite == nil || input.CompositeCollaboration == nil {
		if len(input.WorkspaceTransfers) != 0 {
			return workspaceTransferCompilationV1{}, invalidInput(
				"Workspace transfers",
				fmt.Errorf("materials require an explicit W5 Composite participant"),
			)
		}
		return workspaceTransferCompilationV1{}, nil
	}

	node := *input.Composite
	participant, err := validateCollaborationParticipantV1(node, input)
	if err != nil {
		return workspaceTransferCompilationV1{}, invalidInput(
			"Workspace transfer participant",
			err,
		)
	}
	restored := make(
		[]restoredWorkspaceTransferMaterialV1,
		len(input.WorkspaceTransfers),
	)
	for index, material := range input.WorkspaceTransfers {
		value, err := restoreWorkspaceTransferMaterialV1(material)
		if err != nil {
			return workspaceTransferCompilationV1{}, invalidInput(
				fmt.Sprintf("Workspace transfer %d", index),
				err,
			)
		}
		if value.root.ManifestDigest != input.CompositeCollaboration.FamilyDigest ||
			value.root.RunID != node.RootRunID ||
			value.envelope.TaskInputRef != input.TaskInputRef ||
			value.root.Composite == nil ||
			value.root.Composite.Plan == nil ||
			!reflect.DeepEqual(
				*value.root.Composite.Plan,
				participant.plan,
			) {
			return workspaceTransferCompilationV1{}, invalidInput(
				fmt.Sprintf("Workspace transfer %d family", index),
				fmt.Errorf("root family or TaskInputRef differs from the frozen compiler participant"),
			)
		}
		restored[index] = value
	}

	switch node.Role {
	case corecontract.CompositeRunRoleChildV1:
		return validateWorkspaceTransferSpecialistV1(
			input,
			node,
			participant,
			restored,
		)
	case corecontract.CompositeRunRoleRootV1,
		corecontract.CompositeRunRoleReviewerV1:
		return validateWorkspaceTransferConsumerV1(
			input,
			participant,
			restored,
		)
	default:
		if len(restored) != 0 {
			return workspaceTransferCompilationV1{}, invalidInput(
				"Workspace transfers",
				fmt.Errorf("unsupported Composite role %q", node.Role),
			)
		}
		return workspaceTransferCompilationV1{}, nil
	}
}

func restoreWorkspaceTransferMaterialV1(
	material WorkspaceTransferMaterialV1,
) (restoredWorkspaceTransferMaterialV1, error) {
	envelopeCanonical := bytes.Clone(material.EnvelopeCanonical)
	if contentDigest(
		"WORKSPACE_TRANSFER_ENVELOPE",
		jsonMediaType,
		envelopeCanonical,
	) != material.EnvelopeRef {
		return restoredWorkspaceTransferMaterialV1{}, fmt.Errorf(
			"envelope ContentRecord ref does not close",
		)
	}
	envelope, err := corecontract.RestoreWorkspaceTransferEnvelopeV1(
		envelopeCanonical,
		material.EnvelopeDigest,
	)
	if err != nil {
		return restoredWorkspaceTransferMaterialV1{}, fmt.Errorf(
			"restore envelope: %w",
			err,
		)
	}
	payload := corecontract.WorkspaceTransferResolvedPayloadV1{
		PayloadRef:     material.ResolvedPayload.PayloadRef,
		ContentKind:    material.ResolvedPayload.ContentKind,
		MediaType:      material.ResolvedPayload.MediaType,
		CanonicalBytes: bytes.Clone(material.ResolvedPayload.CanonicalBytes),
	}
	if err := envelope.ValidateResolvedPayloadV1(payload); err != nil {
		return restoredWorkspaceTransferMaterialV1{}, err
	}
	root, _, err := corecontract.NewRunManifest(material.RootManifest)
	if err != nil || root.ManifestDigest != material.RootManifest.ManifestDigest {
		return restoredWorkspaceTransferMaterialV1{}, fmt.Errorf(
			"root Manifest is not frozen: %v",
			err,
		)
	}
	child, _, err := corecontract.NewRunManifest(material.ChildManifest)
	if err != nil || child.ManifestDigest != material.ChildManifest.ManifestDigest {
		return restoredWorkspaceTransferMaterialV1{}, fmt.Errorf(
			"Child Manifest is not frozen: %v",
			err,
		)
	}
	if err := envelope.ValidateForCompositeFamilyV1(root, child); err != nil {
		return restoredWorkspaceTransferMaterialV1{}, err
	}
	return restoredWorkspaceTransferMaterialV1{
		envelope: envelope,
		payload:  payload,
		root:     root,
		child:    child,
		evidence: corecontract.WorkspaceTransferEvidenceV1{
			Direction:      envelope.Direction,
			PayloadKind:    envelope.PayloadKind,
			EnvelopeRef:    material.EnvelopeRef,
			EnvelopeDigest: material.EnvelopeDigest,
			PayloadRef:     envelope.PayloadRef,
			RootRunID:      envelope.RootRunID,
			ChildRunID:     envelope.ChildRunID,
			SlotID:         envelope.SlotID,
		},
	}, nil
}

func validateWorkspaceTransferSpecialistV1(
	input CompileInputV1,
	node corecontract.CompositeRunNodeV1,
	participant collaborationParticipantV1,
	materials []restoredWorkspaceTransferMaterialV1,
) (workspaceTransferCompilationV1, error) {
	planned, ok := workspaceTransferPlannedChildV1(
		participant.plan,
		node.RepairRound,
		input.CompositeCollaboration.ParticipantRunID,
	)
	if !ok {
		return workspaceTransferCompilationV1{}, invalidInput(
			"Workspace transfer Specialist",
			fmt.Errorf("planned Child is absent"),
		)
	}
	if planned.Transfer == nil {
		if len(materials) != 0 {
			return workspaceTransferCompilationV1{}, invalidInput(
				"Workspace transfer Specialist",
				fmt.Errorf("same-Workspace Child carries a transfer edge"),
			)
		}
		return workspaceTransferCompilationV1{}, nil
	}
	if len(materials) != 1 {
		return workspaceTransferCompilationV1{}, invalidInput(
			"Workspace transfer Specialist",
			fmt.Errorf("cross-Workspace Child requires exactly one REQUEST edge"),
		)
	}
	material := materials[0]
	envelope := material.envelope
	if envelope.Direction != corecontract.WorkspaceTransferDirectionRequestV1 ||
		envelope.PayloadKind != corecontract.WorkspaceTransferPayloadTaskSummaryV1 ||
		envelope.ChildRunID != planned.RunID ||
		envelope.SlotID != planned.SlotID ||
		material.child.RunID != planned.RunID ||
		material.child.Workspace != input.WorkspaceScope ||
		material.child.Composite == nil ||
		!reflect.DeepEqual(*material.child.Composite, node) {
		return workspaceTransferCompilationV1{}, invalidInput(
			"Workspace transfer Specialist",
			fmt.Errorf("REQUEST edge differs from the current frozen Child"),
		)
	}
	summary, err := corecontract.RestoreWorkspaceTaskSummaryV1(
		material.payload.CanonicalBytes,
	)
	if err != nil || summary.SourceTaskInputRef != input.TaskInputRef ||
		summary.RepairRound != node.RepairRound {
		return workspaceTransferCompilationV1{}, invalidInput(
			"Workspace transfer Specialist task summary",
			fmt.Errorf("request payload does not bind the Child round: %v", err),
		)
	}
	repairBasis := input.CompositeCollaboration.RepairBasisCanonical
	if node.RepairRound == 0 {
		if len(repairBasis) != 0 {
			return workspaceTransferCompilationV1{}, invalidInput(
				"Workspace transfer Specialist task summary",
				fmt.Errorf("round zero carries a repair basis"),
			)
		}
	}
	_, expectedCanonical, err := CompileWorkspaceTaskSummaryV1(
		input.TaskInputRef,
		input.TaskInputCanonical,
		repairBasis,
	)
	if err != nil || !bytes.Equal(
		material.payload.CanonicalBytes,
		expectedCanonical,
	) {
		return workspaceTransferCompilationV1{}, invalidInput(
			"Workspace transfer Specialist task summary",
			fmt.Errorf("REQUEST payload differs from the trusted task-summary compiler: %v", err),
		)
	}
	text := summary.Summary
	return workspaceTransferCompilationV1{
		taskText: &text,
		evidence: []corecontract.WorkspaceTransferEvidenceV1{
			material.evidence,
		},
	}, nil
}

func validateWorkspaceTransferConsumerV1(
	input CompileInputV1,
	participant collaborationParticipantV1,
	materials []restoredWorkspaceTransferMaterialV1,
) (workspaceTransferCompilationV1, error) {
	evidence := make(
		[]corecontract.WorkspaceTransferEvidenceV1,
		0,
		len(materials),
	)
	materialIndex := 0
	for index, result := range input.CompositeChildResults {
		planned, ok := workspaceTransferPlannedResultV1(participant.plan, result)
		if !ok {
			return workspaceTransferCompilationV1{}, invalidInput(
				fmt.Sprintf("Workspace transfer contribution %d", index),
				fmt.Errorf("Child result is absent from the frozen plan"),
			)
		}
		if planned.Transfer == nil {
			continue
		}
		if materialIndex >= len(materials) {
			return workspaceTransferCompilationV1{}, invalidInput(
				fmt.Sprintf("Workspace transfer contribution %d", index),
				fmt.Errorf("cross-Workspace result lacks a RESULT edge"),
			)
		}
		material := materials[materialIndex]
		materialIndex++
		envelope := material.envelope
		if envelope.Direction != corecontract.WorkspaceTransferDirectionResultV1 ||
			envelope.PayloadKind != corecontract.WorkspaceTransferPayloadSpecialistResultV1 ||
			envelope.ChildRunID != result.RunID ||
			envelope.SlotID != result.SlotID ||
			envelope.PayloadRef != result.ResultRef ||
			material.child.RunID != result.RunID ||
			material.child.ManifestDigest != result.ChildManifestDigest ||
			material.payload.PayloadRef != result.ResultRef ||
			!bytes.Equal(material.payload.CanonicalBytes, result.ResultCanonical) ||
			material.root.Workspace != input.WorkspaceScope {
			return workspaceTransferCompilationV1{}, invalidInput(
				fmt.Sprintf("Workspace transfer contribution %d", index),
				fmt.Errorf("RESULT edge differs from the frozen Specialist result"),
			)
		}
		evidence = append(evidence, material.evidence)
	}
	if materialIndex != len(materials) {
		return workspaceTransferCompilationV1{}, invalidInput(
			"Workspace transfers",
			fmt.Errorf("unconsumed or out-of-order RESULT edge"),
		)
	}
	return workspaceTransferCompilationV1{evidence: evidence}, nil
}

func workspaceTransferPlannedChildV1(
	plan corecontract.CompositeRunPlanV1,
	repairRound uint32,
	runID string,
) (corecontract.CompositeChildRunRefV1, bool) {
	candidates := plan.Children
	if repairRound == corecontract.CompositeRepairRoundOneV1 &&
		plan.Decision != nil {
		candidates = plan.Decision.RepairChildren
	}
	for _, candidate := range candidates {
		if candidate.RunID == runID {
			return candidate, true
		}
	}
	return corecontract.CompositeChildRunRefV1{}, false
}

func workspaceTransferPlannedResultV1(
	plan corecontract.CompositeRunPlanV1,
	result CompositeChildResultV1,
) (corecontract.CompositeChildRunRefV1, bool) {
	for _, candidate := range plan.Children {
		if candidate.RunID == result.RunID && candidate.SlotID == result.SlotID {
			return candidate, true
		}
	}
	if plan.Decision != nil {
		for _, candidate := range plan.Decision.RepairChildren {
			if candidate.RunID == result.RunID && candidate.SlotID == result.SlotID {
				return candidate, true
			}
		}
	}
	return corecontract.CompositeChildRunRefV1{}, false
}

// CompileWorkspaceTaskSummaryV1 is the pure, deterministic compiler used by
// Current Store after it has loaded the exact TASK_INPUT and optional repair
// basis. It performs no model call and never accepts caller-authored summary
// text. A repair basis is retained whole; only the source task excerpt may be
// shortened to fit the frozen transfer payload ceiling.
func CompileWorkspaceTaskSummaryV1(
	taskInputRef string,
	taskInputCanonical []byte,
	repairBasisCanonical []byte,
) (corecontract.WorkspaceTaskSummaryV1, []byte, error) {
	task, err := corecontract.RestoreTaskInputV1(
		bytes.Clone(taskInputCanonical),
	)
	if err != nil || contentDigest(
		"TASK_INPUT",
		jsonMediaType,
		taskInputCanonical,
	) != taskInputRef {
		return corecontract.WorkspaceTaskSummaryV1{}, nil, fmt.Errorf(
			"contextcompiler: Workspace transfer TASK_INPUT does not close: %v",
			err,
		)
	}

	var repair *corecontract.WorkspaceTaskSummaryV1
	var repairSummary json.RawMessage
	if len(repairBasisCanonical) != 0 {
		basis, restoreErr := corecontract.RestoreWorkspaceTaskSummaryV1(
			bytes.Clone(repairBasisCanonical),
		)
		if restoreErr != nil ||
			basis.SourceTaskInputRef != taskInputRef ||
			basis.RepairRound != corecontract.CompositeRepairRoundOneV1 {
			return corecontract.WorkspaceTaskSummaryV1{}, nil, fmt.Errorf(
				"contextcompiler: Workspace transfer repair basis does not close: %v",
				restoreErr,
			)
		}
		canonical, canonicalErr := moduleapi.CanonicalJSON(
			[]byte(basis.Summary),
		)
		if canonicalErr != nil || !bytes.Equal(canonical, []byte(basis.Summary)) ||
			len(canonical) == 0 || canonical[0] != '{' {
			return corecontract.WorkspaceTaskSummaryV1{}, nil, fmt.Errorf(
				"contextcompiler: Workspace transfer repair summary is not one canonical object: %v",
				canonicalErr,
			)
		}
		repair = &basis
		repairSummary = bytes.Clone(canonical)
	}

	for _, maximum := range workspaceTaskExcerptCandidateLimitsV1(
		len(task.Text),
	) {
		excerpt := workspaceTaskHeadTailExtractV1(task.Text, maximum)
		if excerpt == "" {
			continue
		}
		summaryText, buildErr := workspaceTaskSummaryTextV1(
			excerpt,
			repairSummary,
		)
		if buildErr != nil {
			return corecontract.WorkspaceTaskSummaryV1{}, nil, buildErr
		}
		candidate := corecontract.WorkspaceTaskSummaryV1{
			SchemaVersion:      corecontract.WorkspaceTaskSummarySchemaVersionV1,
			SourceTaskInputRef: taskInputRef,
			Summary:            summaryText,
		}
		if repair != nil {
			candidate.RepairRound = corecontract.CompositeRepairRoundOneV1
			candidate.PreviousSetDigest = repair.PreviousSetDigest
			candidate.VerdictRef = repair.VerdictRef
		}
		frozen, canonical, freezeErr :=
			corecontract.NewWorkspaceTaskSummaryV1(candidate)
		if freezeErr == nil {
			return frozen, canonical, nil
		}
	}
	return corecontract.WorkspaceTaskSummaryV1{}, nil, fmt.Errorf(
		"contextcompiler: Workspace transfer task and mandatory repair basis exceed the V1 payload ceiling",
	)
}

func workspaceTaskSummaryTextV1(
	excerpt string,
	repairSummary json.RawMessage,
) (string, error) {
	var value any
	if len(repairSummary) == 0 {
		value = struct {
			SchemaVersion string `json:"schema_version"`
			TaskExcerpt   string `json:"task_excerpt"`
		}{
			SchemaVersion: workspaceTaskExtractSchemaVersionV1,
			TaskExcerpt:   excerpt,
		}
	} else {
		value = struct {
			SchemaVersion string          `json:"schema_version"`
			TaskExcerpt   string          `json:"task_excerpt"`
			RepairBasis   json.RawMessage `json:"repair_basis"`
		}{
			SchemaVersion: workspaceRepairExtractSchemaV1,
			TaskExcerpt:   excerpt,
			RepairBasis:   bytes.Clone(repairSummary),
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf(
			"contextcompiler: marshal Workspace transfer task summary: %w",
			err,
		)
	}
	canonical, err := moduleapi.CanonicalJSON(raw)
	if err != nil {
		return "", fmt.Errorf(
			"contextcompiler: canonicalize Workspace transfer task summary: %w",
			err,
		)
	}
	return string(canonical), nil
}

func workspaceTaskExcerptCandidateLimitsV1(length int) []int {
	limits := []int{
		length,
		24 << 10,
		16 << 10,
		8 << 10,
		4 << 10,
		2 << 10,
		1 << 10,
		512,
		256,
		128,
		64,
		32,
		16,
		8,
		4,
		1,
	}
	result := make([]int, 0, len(limits))
	seen := make(map[int]struct{}, len(limits))
	for _, limit := range limits {
		if limit <= 0 || limit > length {
			continue
		}
		if _, duplicate := seen[limit]; duplicate {
			continue
		}
		seen[limit] = struct{}{}
		result = append(result, limit)
	}
	return result
}

func workspaceTaskHeadTailExtractV1(value string, maximum int) string {
	if maximum <= 0 || value == "" {
		return ""
	}
	if len(value) <= maximum {
		return value
	}
	if maximum <= len(workspaceTaskOmissionV1)+2 {
		return moduleapi.CanonicalText(
			workspaceValidUTF8PrefixV1(value, maximum),
		)
	}
	available := maximum - len(workspaceTaskOmissionV1)
	headLimit := available / 2
	tailLimit := available - headLimit
	head := workspaceValidUTF8PrefixV1(value, headLimit)
	tail := workspaceValidUTF8SuffixV1(value, tailLimit)
	return moduleapi.CanonicalText(head + workspaceTaskOmissionV1 + tail)
}

func workspaceValidUTF8PrefixV1(value string, maximum int) string {
	if maximum >= len(value) {
		return value
	}
	if maximum <= 0 {
		return ""
	}
	end := maximum
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

func workspaceValidUTF8SuffixV1(value string, maximum int) string {
	if maximum >= len(value) {
		return value
	}
	if maximum <= 0 {
		return ""
	}
	start := len(value) - maximum
	for start < len(value) && !utf8.ValidString(value[start:]) {
		start++
	}
	return value[start:]
}
