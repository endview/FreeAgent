package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"

	"github.com/endview/freeagent/internal/contextcompiler"
	"github.com/endview/freeagent/internal/corecontract"
)

// WorkspaceTransferRecordV1 is the one detached Store/Host projection of an
// immutable transfer edge. EnvelopeDigest is the protocol digest, while
// EnvelopeRef is the content_records digest of EnvelopeCanonical. Payload is
// the sole transferred content fact. The two Manifests make the projection
// sufficient for the trusted Context Compiler without a second family source.
type WorkspaceTransferRecordV1 struct {
	Envelope          corecontract.WorkspaceTransferEnvelopeV1
	EnvelopeCanonical []byte
	EnvelopeDigest    string
	EnvelopeRef       string
	Payload           ContentRecord
	RootManifest      corecontract.RunManifest
	ChildManifest     corecontract.RunManifest
}

// ContextMaterialV1 returns a defensive compiler projection. It grants no
// authority: the compiler re-proves the envelope, payload and family, while
// Current Store separately proves both historical grants.
func (record WorkspaceTransferRecordV1) ContextMaterialV1() contextcompiler.WorkspaceTransferMaterialV1 {
	return contextcompiler.WorkspaceTransferMaterialV1{
		EnvelopeRef:       record.EnvelopeRef,
		EnvelopeDigest:    record.EnvelopeDigest,
		EnvelopeCanonical: bytes.Clone(record.EnvelopeCanonical),
		ResolvedPayload: corecontract.WorkspaceTransferResolvedPayloadV1{
			PayloadRef:     record.Payload.Digest,
			ContentKind:    string(record.Payload.Kind),
			MediaType:      record.Payload.MediaType,
			CanonicalBytes: bytes.Clone(record.Payload.CanonicalBytes),
		},
		RootManifest:  cloneRunManifestForLoop(record.RootManifest),
		ChildManifest: cloneRunManifestForLoop(record.ChildManifest),
	}
}

// PrepareWorkspaceTransferRequestV1 is the sole pure preview used by both
// CoreLoop before compilation and BeginModelDispatch inside BEGIN IMMEDIATE.
// It never writes. Nil means that the Run is not a cross-Workspace Child.
func PrepareWorkspaceTransferRequestV1(
	run RunForLoop,
) (*WorkspaceTransferRecordV1, error) {
	if run.WorkspaceTransfer == nil {
		return nil, nil
	}
	material := run.WorkspaceTransfer
	if err := validateWorkspaceTransferRecoveryMaterialV1(run, material); err != nil {
		return nil, fmt.Errorf(
			"currentstore: prepare Workspace transfer request: %w",
			err,
		)
	}
	_, summaryCanonical, err := contextcompiler.CompileWorkspaceTaskSummaryV1(
		material.RootTaskInput.Digest,
		material.RootTaskInput.CanonicalBytes,
		material.RepairBasisCanonical,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"currentstore: compile Workspace transfer request: %w",
			err,
		)
	}
	payload, err := newWorkspaceTransferContentRecordV1(
		ContentWorkspaceTransferPayload,
		summaryCanonical,
	)
	if err != nil {
		return nil, err
	}
	return prepareWorkspaceTransferRecordV1(
		material,
		corecontract.WorkspaceTransferDirectionRequestV1,
		corecontract.WorkspaceTransferPayloadTaskSummaryV1,
		corecontract.WorkspaceTaskSummarySchemaVersionV1,
		payload,
	)
}

// prepareWorkspaceTransferResultV1 creates the reverse edge from an exact
// successful MODEL_RESULT. The strict Specialist contribution is re-proved by
// ValidateResolvedPayloadV1; failed, invalid or UNKNOWN outcomes never call it.
func prepareWorkspaceTransferResultV1(
	run RunForLoop,
	payload ContentRecord,
) (*WorkspaceTransferRecordV1, error) {
	if run.WorkspaceTransfer == nil {
		return nil, nil
	}
	material := run.WorkspaceTransfer
	if err := validateWorkspaceTransferRecoveryMaterialV1(run, material); err != nil {
		return nil, fmt.Errorf(
			"currentstore: prepare Workspace transfer result: %w",
			err,
		)
	}
	return prepareWorkspaceTransferRecordV1(
		material,
		corecontract.WorkspaceTransferDirectionResultV1,
		corecontract.WorkspaceTransferPayloadSpecialistResultV1,
		corecontract.SpecialistContributionSchemaVersionV1,
		payload,
	)
}

func prepareWorkspaceTransferRecordV1(
	material *WorkspaceTransferRecoveryMaterialV1,
	direction corecontract.WorkspaceTransferDirectionV1,
	payloadKind corecontract.WorkspaceTransferPayloadKindV1,
	payloadSchemaVersion string,
	payload ContentRecord,
) (*WorkspaceTransferRecordV1, error) {
	var (
		sourceGrant corecontract.WorkspaceTransferGrantV1
		targetGrant corecontract.WorkspaceTransferGrantV1
	)
	switch direction {
	case corecontract.WorkspaceTransferDirectionRequestV1:
		sourceGrant = material.RootGrant
		targetGrant = material.TargetGrant
	case corecontract.WorkspaceTransferDirectionResultV1:
		sourceGrant = material.TargetGrant
		targetGrant = material.RootGrant
	default:
		return nil, fmt.Errorf("currentstore: unsupported Workspace transfer direction")
	}
	if payload.SizeBytes <= 0 || payload.SizeBytes > int64(^uint32(0)) {
		return nil, fmt.Errorf("currentstore: invalid Workspace transfer payload size")
	}
	envelope, envelopeCanonical, envelopeDigest, err :=
		corecontract.NewWorkspaceTransferEnvelopeV1(
			corecontract.WorkspaceTransferEnvelopeV1{
				SchemaVersion:        corecontract.WorkspaceTransferEnvelopeSchemaVersionV1,
				TenantID:             material.RootManifest.TenantID,
				SourceGrantID:        sourceGrant.GrantID,
				TargetGrantID:        targetGrant.GrantID,
				Direction:            direction,
				PayloadKind:          payloadKind,
				PayloadSchemaVersion: payloadSchemaVersion,
				TaskInputRef:         material.RootTaskInput.Digest,
				SourceWorkspace:      sourceGrant.Workspace,
				TargetWorkspace:      targetGrant.Workspace,
				SourceGrantDigest:    workspaceTransferGrantDigestV1(sourceGrant),
				TargetGrantDigest:    workspaceTransferGrantDigestV1(targetGrant),
				RootRunID:            material.RootManifest.RunID,
				ChildRunID:           material.ChildManifest.RunID,
				SlotID:               material.SlotID,
				PayloadRef:           payload.Digest,
				PayloadSizeBytes:     uint32(payload.SizeBytes),
			},
		)
	if err != nil {
		return nil, fmt.Errorf("currentstore: freeze Workspace transfer envelope: %w", err)
	}
	if err := envelope.ValidateAgainstGrantsV1(sourceGrant, targetGrant); err != nil {
		return nil, fmt.Errorf("currentstore: authorize Workspace transfer envelope: %w", err)
	}
	if err := envelope.ValidateAgainstPlanV1(material.Plan); err != nil {
		return nil, fmt.Errorf("currentstore: close Workspace transfer plan: %w", err)
	}
	if err := envelope.ValidateForCompositeFamilyV1(
		material.RootManifest,
		material.ChildManifest,
	); err != nil {
		return nil, fmt.Errorf("currentstore: close Workspace transfer family: %w", err)
	}
	if err := envelope.ValidateResolvedPayloadV1(
		corecontract.WorkspaceTransferResolvedPayloadV1{
			PayloadRef:     payload.Digest,
			ContentKind:    string(payload.Kind),
			MediaType:      payload.MediaType,
			CanonicalBytes: payload.CanonicalBytes,
		},
	); err != nil {
		return nil, fmt.Errorf("currentstore: close Workspace transfer payload: %w", err)
	}
	envelopeRef, err := ComputeContentDigest(
		ContentWorkspaceTransferEnvelope,
		admissionJSONMediaType,
		envelopeCanonical,
	)
	if err != nil {
		return nil, fmt.Errorf("currentstore: hash Workspace transfer envelope: %w", err)
	}
	return &WorkspaceTransferRecordV1{
		Envelope:          envelope,
		EnvelopeCanonical: bytes.Clone(envelopeCanonical),
		EnvelopeDigest:    envelopeDigest,
		EnvelopeRef:       envelopeRef,
		Payload:           cloneContentRecord(payload),
		RootManifest:      cloneRunManifestForLoop(material.RootManifest),
		ChildManifest:     cloneRunManifestForLoop(material.ChildManifest),
	}, nil
}

func validateWorkspaceTransferRecoveryMaterialV1(
	run RunForLoop,
	material *WorkspaceTransferRecoveryMaterialV1,
) error {
	if material == nil || material.ChildManifest.Composite == nil ||
		run.RunID != material.ChildManifest.RunID ||
		run.Manifest.ManifestDigest != material.ChildManifest.ManifestDigest ||
		run.Manifest.Workspace != material.ChildManifest.Workspace ||
		material.RootManifest.RunID == material.ChildManifest.RunID ||
		material.RootManifest.TaskInputRef != material.RootTaskInput.Digest ||
		material.ChildManifest.TaskInputRef != material.RootTaskInput.Digest ||
		material.SlotID == "" || material.RepairRound != material.ChildManifest.Composite.RepairRound {
		return fmt.Errorf("Workspace transfer recovery family identity differs")
	}
	if material.ChildManifest.Composite.Assignment == nil ||
		material.ChildManifest.Composite.Assignment.SlotID != material.SlotID {
		return fmt.Errorf("Workspace transfer recovery Child assignment differs")
	}
	root, _, err := corecontract.NewRunManifest(material.RootManifest)
	if err != nil || root.ManifestDigest != material.RootManifest.ManifestDigest {
		return fmt.Errorf("Workspace transfer root Manifest is not frozen: %v", err)
	}
	child, _, err := corecontract.NewRunManifest(material.ChildManifest)
	if err != nil || child.ManifestDigest != material.ChildManifest.ManifestDigest {
		return fmt.Errorf("Workspace transfer Child Manifest is not frozen: %v", err)
	}
	rootGrant, rootCanonical, rootDigest, err :=
		corecontract.NewWorkspaceTransferGrantV1(material.RootGrant)
	if err != nil || !bytes.Equal(rootCanonical, material.RootGrantCanonical) {
		return fmt.Errorf("Workspace transfer root grant is not frozen: %v", err)
	}
	targetGrant, targetCanonical, targetDigest, err :=
		corecontract.NewWorkspaceTransferGrantV1(material.TargetGrant)
	if err != nil || !bytes.Equal(targetCanonical, material.TargetGrantCanonical) {
		return fmt.Errorf("Workspace transfer target grant is not frozen: %v", err)
	}
	if material.Plan.RootGrantDigest != rootDigest ||
		material.Plan.TargetGrantDigest != targetDigest ||
		material.Plan.RootWorkspace != root.Workspace ||
		material.Plan.TargetWorkspace != child.Workspace {
		return fmt.Errorf("Workspace transfer plan differs from frozen grants or Workspaces")
	}
	if err := material.Plan.ValidateAgainstGrantsV1(rootGrant, targetGrant); err != nil {
		return err
	}
	if material.RootTaskInput.Kind != ContentTaskInput ||
		material.RootTaskInput.MediaType != admissionJSONMediaType ||
		material.RootTaskInput.SizeBytes != int64(len(material.RootTaskInput.CanonicalBytes)) {
		return fmt.Errorf("Workspace transfer root TASK_INPUT metadata differs")
	}
	taskDigest, err := ComputeContentDigest(
		ContentTaskInput,
		admissionJSONMediaType,
		material.RootTaskInput.CanonicalBytes,
	)
	if err != nil || taskDigest != material.RootTaskInput.Digest {
		return fmt.Errorf("Workspace transfer root TASK_INPUT does not close: %v", err)
	}
	switch material.RepairRound {
	case 0:
		if material.PreviousContributionSet != nil ||
			material.PreviousContributionSetDigest != "" ||
			material.RepairVerdict != nil || material.RepairVerdictRef != "" ||
			material.RepairBasis != nil || len(material.RepairBasisCanonical) != 0 {
			return fmt.Errorf("initial Workspace transfer carries repair lineage")
		}
	case corecontract.CompositeRepairRoundOneV1:
		if material.PreviousContributionSet == nil ||
			material.RepairVerdict == nil || material.RepairBasis == nil {
			return fmt.Errorf("repair Workspace transfer lacks lineage")
		}
		set, _, setDigest, err := corecontract.NewCollaborationContributionSetV1(
			*material.PreviousContributionSet,
		)
		if err != nil || set.RepairRound != 0 ||
			setDigest != material.PreviousContributionSetDigest {
			return fmt.Errorf("repair Workspace transfer previous set does not close: %v", err)
		}
		verdict, _, err := corecontract.NewCollaborationReviewVerdictV1(
			*material.RepairVerdict,
		)
		if err != nil ||
			verdict.Decision != corecontract.CollaborationReviewDecisionRepairRequiredV1 ||
			verdict.RepairRound != 0 ||
			verdict.ContributionSetDigest != setDigest ||
			!workspaceTransferContainsSlotV1(verdict.AffectedSlotIDs, material.SlotID) {
			return fmt.Errorf("repair Workspace transfer verdict does not close: %v", err)
		}
		basis, err := corecontract.RestoreWorkspaceTaskSummaryV1(
			material.RepairBasisCanonical,
		)
		if err != nil || basis != *material.RepairBasis ||
			basis.SourceTaskInputRef != material.RootTaskInput.Digest ||
			basis.RepairRound != corecontract.CompositeRepairRoundOneV1 ||
			basis.PreviousSetDigest != setDigest ||
			basis.VerdictRef != material.RepairVerdictRef {
			return fmt.Errorf("repair Workspace transfer task basis does not close: %v", err)
		}
	default:
		return fmt.Errorf("unsupported Workspace transfer repair round")
	}
	return nil
}

func workspaceTransferGrantDigestV1(
	grant corecontract.WorkspaceTransferGrantV1,
) string {
	_, _, digest, err := corecontract.NewWorkspaceTransferGrantV1(grant)
	if err != nil {
		return ""
	}
	return digest
}

func workspaceTransferContainsSlotV1(slots []string, wanted string) bool {
	for _, slot := range slots {
		if slot == wanted {
			return true
		}
	}
	return false
}

func newWorkspaceTransferContentRecordV1(
	kind ContentKind,
	canonical []byte,
) (ContentRecord, error) {
	digest, err := ComputeContentDigest(
		kind,
		admissionJSONMediaType,
		canonical,
	)
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"currentstore: hash Workspace transfer payload: %w",
			err,
		)
	}
	return ContentRecord{
		Digest:         digest,
		Kind:           kind,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(canonical),
		SizeBytes:      int64(len(canonical)),
	}, nil
}

func putWorkspaceTransferRecordV1(
	ctx context.Context,
	connection *sql.Conn,
	record *WorkspaceTransferRecordV1,
	includePayload bool,
	createdAt int64,
) error {
	if record == nil {
		return nil
	}
	if includePayload {
		if err := putAdmissionContent(
			ctx,
			connection,
			preparedAdmissionContent{
				Digest:         record.Payload.Digest,
				Kind:           record.Payload.Kind,
				MediaType:      record.Payload.MediaType,
				CanonicalBytes: bytes.Clone(record.Payload.CanonicalBytes),
			},
			createdAt,
		); err != nil {
			return fmt.Errorf("currentstore: persist Workspace transfer payload: %w", err)
		}
	}
	if err := putAdmissionContent(
		ctx,
		connection,
		preparedAdmissionContent{
			Digest:         record.EnvelopeRef,
			Kind:           ContentWorkspaceTransferEnvelope,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: bytes.Clone(record.EnvelopeCanonical),
		},
		createdAt,
	); err != nil {
		return fmt.Errorf("currentstore: persist Workspace transfer envelope: %w", err)
	}
	return nil
}

func loadExactWorkspaceTransferRecordV1(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	expected *WorkspaceTransferRecordV1,
) (*WorkspaceTransferRecordV1, error) {
	if expected == nil {
		return nil, nil
	}
	payload, err := queryContent(ctx, queryer, expected.Payload.Digest)
	if err != nil || !sameWorkspaceTransferContentV1(payload, expected.Payload) {
		return nil, fmt.Errorf(
			"currentstore: persisted Workspace transfer payload differs: %v",
			err,
		)
	}
	envelopeContent, err := queryContent(ctx, queryer, expected.EnvelopeRef)
	if err != nil || envelopeContent.Kind != ContentWorkspaceTransferEnvelope ||
		envelopeContent.MediaType != admissionJSONMediaType ||
		!bytes.Equal(envelopeContent.CanonicalBytes, expected.EnvelopeCanonical) {
		return nil, fmt.Errorf(
			"currentstore: persisted Workspace transfer envelope differs: %v",
			err,
		)
	}
	envelope, err := corecontract.RestoreWorkspaceTransferEnvelopeV1(
		envelopeContent.CanonicalBytes,
		expected.EnvelopeDigest,
	)
	if err != nil || envelope != expected.Envelope {
		return nil, fmt.Errorf(
			"currentstore: persisted Workspace transfer envelope protocol differs: %v",
			err,
		)
	}
	loaded := cloneWorkspaceTransferRecordV1(*expected)
	loaded.Payload = payload
	return &loaded, nil
}

func loadWorkspaceTransferRequestV1(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	run RunForLoop,
) (*WorkspaceTransferRecordV1, error) {
	expected, err := PrepareWorkspaceTransferRequestV1(run)
	if err != nil {
		return nil, err
	}
	return loadExactWorkspaceTransferRecordV1(ctx, queryer, expected)
}

// loadWorkspaceTransferResultV1 is the sole persisted RESULT projection for
// composite result loading. A terminal successful cross-Workspace Child
// without this exact record is an integrity error, never an ordinary Child
// failure: the terminal Attempt and RESULT envelope are one transaction.
func loadWorkspaceTransferResultV1(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	run RunForLoop,
	payload ContentRecord,
) (*WorkspaceTransferRecordV1, error) {
	expected, err := prepareWorkspaceTransferResultV1(run, payload)
	if err != nil {
		return nil, err
	}
	return loadExactWorkspaceTransferRecordV1(ctx, queryer, expected)
}

func runWithWorkspaceTransferRecordV1(
	run RunForLoop,
	record *WorkspaceTransferRecordV1,
) (RunForLoop, error) {
	if record == nil {
		return run, nil
	}
	envelope := ContentRecord{
		Digest:         record.EnvelopeRef,
		Kind:           ContentWorkspaceTransferEnvelope,
		MediaType:      admissionJSONMediaType,
		CanonicalBytes: bytes.Clone(record.EnvelopeCanonical),
		SizeBytes:      int64(len(record.EnvelopeCanonical)),
	}
	return runWithAdditionalContent(run, record.Payload, envelope)
}

func sameWorkspaceTransferContentV1(left ContentRecord, right ContentRecord) bool {
	return left.Digest == right.Digest && left.Kind == right.Kind &&
		left.MediaType == right.MediaType && left.SizeBytes == right.SizeBytes &&
		bytes.Equal(left.CanonicalBytes, right.CanonicalBytes)
}

func cloneWorkspaceTransferRecordV1(
	record WorkspaceTransferRecordV1,
) WorkspaceTransferRecordV1 {
	record.EnvelopeCanonical = bytes.Clone(record.EnvelopeCanonical)
	record.Payload = cloneContentRecord(record.Payload)
	record.RootManifest = cloneRunManifestForLoop(record.RootManifest)
	record.ChildManifest = cloneRunManifestForLoop(record.ChildManifest)
	return record
}
