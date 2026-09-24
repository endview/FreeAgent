package currentstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/internal/corecontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	admissionJSONMediaType          = "application/json"
	learningCycleAdmissionKeyPrefix = "learning-cycle-admission-"
	learningCycleRunIDPrefix        = "learning-cycle-run-"
	learningCycleMemberIDPrefix     = "learning-cycle-member-"
	learningCycleRecoveryRootPrefix = "learning-cycle-recovery-"
)

// CommitRunAdmissionInput is the complete immutable closure for one S1 Run.
// Initial Run, LoopFrame and RunEvent state is Store-owned and cannot be
// supplied by the caller.
type CommitRunAdmissionInput struct {
	PublishedBasis controlcontract.PublishedBasis

	IntentCanonical []byte
	IntentDigest    string

	MemberSnapshotCanonical []byte
	RunManifestCanonical    []byte

	Contents []ContentInput
}

type preparedAdmissionContent struct {
	Digest         string
	Kind           ContentKind
	MediaType      string
	CanonicalBytes []byte
}

type preparedRunAdmission struct {
	input      CommitRunAdmissionInput
	intent     corecontract.AdmissionIntentV1
	member     corecontract.MemberExecutionSnapshot
	manifest   corecontract.RunManifest
	contents   map[string]preparedAdmissionContent
	required   map[string]ContentKind
	policyRefs map[string]corecontract.PolicyRef
	event      preparedAdmissionContent
}

// CommitRunAdmission publishes one complete S1 Run closure in a single
// BEGIN IMMEDIATE transaction. An existing same-intent Run is returned before
// current Control/Catalog is inspected; a new Run must match current exactly.
func (store *Store) CommitRunAdmission(
	ctx context.Context,
	input CommitRunAdmissionInput,
) (RunAdmissionResult, error) {
	if ctx == nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: context is nil",
			ErrInvalidAdmission,
		)
	}
	unlock, err := store.lockOpen()
	if err != nil {
		return RunAdmissionResult{}, err
	}
	defer unlock()

	prepared, err := prepareCompleteRunAdmission(input)
	if err != nil {
		return RunAdmissionResult{}, err
	}
	// Learning-cycle identities are reserved for the task-aware endpoint that
	// publishes the Run and binds PENDING/0 -> RUN_ADMITTED/1 atomically.
	// The compiler accepts all four identities independently, so the generic
	// endpoint must reserve every namespace that could strand a deterministic
	// task behind a different or unbound ordinary Run.
	if usesLearningCycleIdentityNamespace(prepared) {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Learning-cycle identity requires CommitLearningCycleRunAdmission",
			ErrInvalidAdmission,
		)
	}
	if prepared.manifest.Composite != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: composite Runs require atomic CommitCompositeRunFamily",
			ErrInvalidAdmission,
		)
	}
	if prepared.manifest.ConversationTurn != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Conversation Runs require CommitConversationTurnAdmission",
			ErrInvalidAdmission,
		)
	}
	if prepared.intent.ChannelEndpointID != "" {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: Channel-origin Admission must use CommitChannelIngressAndRunAdmission",
			ErrInvalidAdmission,
		)
	}

	connection, err := store.db.Conn(ctx)
	if err != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"currentstore: acquire Run Admission connection: %w",
			err,
		)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"currentstore: begin Run Admission: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	// This check must precede current-pointer validation. A retry of an
	// admitted intent remains valid after Control/Catalog advances.
	existing, found, err := resolveAdmissionWithQueryer(
		ctx,
		connection,
		prepared.intent.TenantID,
		prepared.intent.AdmissionKey,
		prepared.input.IntentDigest,
	)
	if err != nil {
		return RunAdmissionResult{}, err
	}
	if found {
		if err := verifyCurrentRunObservationV1(ctx, connection, existing.RunID); err != nil {
			return RunAdmissionResult{}, err
		}
		if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
			return RunAdmissionResult{}, fmt.Errorf(
				"currentstore: commit idempotent Run Admission: %w",
				err,
			)
		}
		committed = true
		return existing, nil
	}

	createdAt := nowUnixMicro()
	if createdAt <= 0 {
		return RunAdmissionResult{}, fmt.Errorf(
			"%w: invalid Admission time",
			ErrAdmissionIntegrity,
		)
	}
	result, err := publishPreparedRunAdmission(
		ctx,
		connection,
		prepared,
		createdAt,
	)
	if err != nil {
		return RunAdmissionResult{}, err
	}
	if _, err := connection.ExecContext(ctx, `COMMIT`); err != nil {
		return RunAdmissionResult{}, fmt.Errorf(
			"currentstore: commit Run Admission: %w",
			err,
		)
	}
	committed = true
	result.Created = true
	return result, nil
}

func usesLearningCycleIdentityNamespace(prepared preparedRunAdmission) bool {
	return strings.HasPrefix(
		prepared.intent.AdmissionKey,
		learningCycleAdmissionKeyPrefix,
	) || strings.HasPrefix(
		prepared.manifest.RunID,
		learningCycleRunIDPrefix,
	) || strings.HasPrefix(
		prepared.member.MemberID,
		learningCycleMemberIDPrefix,
	) || strings.HasPrefix(
		prepared.manifest.RecoveryRootRef,
		learningCycleRecoveryRootPrefix,
	)
}

func prepareCompleteRunAdmission(
	input CommitRunAdmissionInput,
) (preparedRunAdmission, error) {
	input.IntentCanonical = bytes.Clone(input.IntentCanonical)
	input.MemberSnapshotCanonical = bytes.Clone(
		input.MemberSnapshotCanonical,
	)
	input.RunManifestCanonical = bytes.Clone(input.RunManifestCanonical)
	clonedContents := make([]ContentInput, len(input.Contents))
	for index, content := range input.Contents {
		content.CanonicalBytes = bytes.Clone(content.CanonicalBytes)
		clonedContents[index] = content
	}
	input.Contents = clonedContents

	intent, member, manifest, contents, required, policyRefs, err :=
		prepareRunAdmission(input)
	if err != nil {
		return preparedRunAdmission{}, err
	}
	_, eventCanonical, err := corecontract.NewRunAdmittedEventV1(
		manifest.RunID,
		manifest.ManifestDigest,
		member.MemberSnapshotDigest,
	)
	if err != nil {
		return preparedRunAdmission{}, fmt.Errorf(
			"%w: construct admitted event: %v",
			ErrInvalidAdmission,
			err,
		)
	}
	eventDigest, err := ComputeContentDigest(
		ContentRunEventPayload,
		admissionJSONMediaType,
		eventCanonical,
	)
	if err != nil {
		return preparedRunAdmission{}, fmt.Errorf(
			"%w: construct admitted event content: %v",
			ErrInvalidAdmission,
			err,
		)
	}
	return preparedRunAdmission{
		input:      input,
		intent:     intent,
		member:     member,
		manifest:   manifest,
		contents:   contents,
		required:   required,
		policyRefs: policyRefs,
		event: preparedAdmissionContent{
			Digest:         eventDigest,
			Kind:           ContentRunEventPayload,
			MediaType:      admissionJSONMediaType,
			CanonicalBytes: bytes.Clone(eventCanonical),
		},
	}, nil
}

// publishPreparedRunAdmission is the one transaction body shared by direct
// Run Admission and Channel ingress. The caller owns BEGIN IMMEDIATE and must
// decide idempotency before invoking it.
func publishPreparedRunAdmission(
	ctx context.Context,
	connection *sql.Conn,
	prepared preparedRunAdmission,
	createdAt int64,
) (RunAdmissionResult, error) {
	if err := requireAdmissionRunIDAvailable(
		ctx,
		connection,
		prepared.manifest.RunID,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	control, catalog, err := verifyCurrentAdmissionBasis(
		ctx,
		connection,
		prepared.input.PublishedBasis,
	)
	if err != nil {
		return RunAdmissionResult{}, err
	}
	if err := verifyAdmissionControlMember(
		control,
		catalog,
		prepared.member,
		prepared.manifest,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	if err := verifyProspectiveAdmissionClosure(
		ctx,
		connection,
		prepared.manifest,
		prepared.member,
		prepared.contents,
		prepared.required,
		prepared.policyRefs,
	); err != nil {
		return RunAdmissionResult{}, err
	}

	allContents := make(
		[]preparedAdmissionContent,
		0,
		len(prepared.contents)+1,
	)
	for _, content := range prepared.contents {
		allContents = append(allContents, content)
	}
	allContents = append(allContents, prepared.event)
	sort.Slice(allContents, func(left, right int) bool {
		return allContents[left].Digest < allContents[right].Digest
	})
	for _, content := range allContents {
		if err := putAdmissionContent(
			ctx,
			connection,
			content,
			createdAt,
		); err != nil {
			return RunAdmissionResult{}, err
		}
	}

	if err := insertAdmissionRun(
		ctx,
		connection,
		prepared.manifest,
		createdAt,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	if err := insertAdmissionMember(
		ctx,
		connection,
		prepared.input.PublishedBasis,
		prepared.manifest.RunID,
		prepared.member,
		prepared.input.MemberSnapshotCanonical,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	if err := insertAdmissionManifest(
		ctx,
		connection,
		prepared.manifest,
		prepared.input.RunManifestCanonical,
		createdAt,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	if err := insertInitialAdmissionFrame(
		ctx,
		connection,
		prepared.manifest,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	if err := insertInitialAdmissionEvent(
		ctx,
		connection,
		prepared.manifest.RunID,
		prepared.event.Digest,
		createdAt,
	); err != nil {
		return RunAdmissionResult{}, err
	}
	if err := appendAdmissionRunObservationV1(
		ctx, connection, prepared.manifest.RunID,
	); err != nil {
		return RunAdmissionResult{}, err
	}

	return loadAdmissionClosure(
		ctx,
		connection,
		prepared.manifest.RunID,
		prepared.manifest.TenantID,
		prepared.manifest.AdmissionKey,
		prepared.manifest.AdmissionIntentDigest,
		prepared.manifest.Workspace.ID,
	)
}

func prepareRunAdmission(
	input CommitRunAdmissionInput,
) (
	corecontract.AdmissionIntentV1,
	corecontract.MemberExecutionSnapshot,
	corecontract.RunManifest,
	map[string]preparedAdmissionContent,
	map[string]ContentKind,
	map[string]corecontract.PolicyRef,
	error,
) {
	if err := input.PublishedBasis.Validate(); err != nil {
		return corecontract.AdmissionIntentV1{},
			corecontract.MemberExecutionSnapshot{},
			corecontract.RunManifest{}, nil, nil, nil,
			fmt.Errorf("%w: published basis: %v", ErrInvalidAdmission, err)
	}
	if input.PublishedBasis.PointerRevision > math.MaxInt64 ||
		input.PublishedBasis.Control.Revision > math.MaxInt64 ||
		input.PublishedBasis.Catalog.Generation > math.MaxInt64 {
		return corecontract.AdmissionIntentV1{},
			corecontract.MemberExecutionSnapshot{},
			corecontract.RunManifest{}, nil, nil, nil,
			fmt.Errorf(
				"%w: published revisions exceed SQLite INTEGER",
				ErrInvalidAdmission,
			)
	}
	intentCanonical := bytes.Clone(input.IntentCanonical)
	intent, err := corecontract.RestoreAdmissionIntentV1(
		intentCanonical,
		input.IntentDigest,
	)
	if err != nil {
		return corecontract.AdmissionIntentV1{},
			corecontract.MemberExecutionSnapshot{},
			corecontract.RunManifest{}, nil, nil, nil,
			fmt.Errorf("%w: restore intent: %v", ErrInvalidAdmission, err)
	}
	memberCanonical := bytes.Clone(input.MemberSnapshotCanonical)
	member, err := corecontract.RestoreMemberExecutionSnapshot(memberCanonical)
	if err != nil {
		return corecontract.AdmissionIntentV1{},
			corecontract.MemberExecutionSnapshot{},
			corecontract.RunManifest{}, nil, nil, nil,
			fmt.Errorf(
				"%w: restore member snapshot: %v",
				ErrInvalidAdmission,
				err,
			)
	}
	manifestCanonical := bytes.Clone(input.RunManifestCanonical)
	manifest, err := corecontract.RestoreRunManifest(manifestCanonical)
	if err != nil {
		return corecontract.AdmissionIntentV1{},
			corecontract.MemberExecutionSnapshot{},
			corecontract.RunManifest{}, nil, nil, nil,
			fmt.Errorf(
				"%w: restore RunManifest: %v",
				ErrInvalidAdmission,
				err,
			)
	}
	if err := manifest.ValidateAgainstMember(member); err != nil {
		return corecontract.AdmissionIntentV1{},
			corecontract.MemberExecutionSnapshot{},
			corecontract.RunManifest{}, nil, nil, nil,
			fmt.Errorf(
				"%w: Manifest/Member closure: %v",
				ErrInvalidAdmission,
				err,
			)
	}
	if string(intent.ExplicitLimits) != "{}" ||
		intent.TenantID != input.PublishedBasis.TenantID ||
		manifest.TenantID != intent.TenantID ||
		manifest.AdmissionKey != intent.AdmissionKey ||
		manifest.AdmissionIntentDigest != input.IntentDigest ||
		manifest.Workspace.ID != intent.WorkspaceID ||
		manifest.PrimaryAgent.ID != intent.AgentID ||
		member.Profile.ID != intent.ProfileID ||
		manifest.TaskInputRef != intent.TaskInputRef ||
		manifest.TaskInputDigest != intent.TaskInputRef ||
		manifest.CancellationScope != intent.CancellationScope ||
		!manifest.Deadline.Equal(intent.Deadline) ||
		member.Catalog.ID != input.PublishedBasis.Catalog.GenerationID ||
		member.Catalog.Version != strconv.FormatUint(
			input.PublishedBasis.Catalog.Generation,
			10,
		) ||
		member.Catalog.Digest != input.PublishedBasis.Catalog.Digest ||
		!memberContainsRequestedPorts(member, intent.RequestedPorts) {
		return corecontract.AdmissionIntentV1{},
			corecontract.MemberExecutionSnapshot{},
			corecontract.RunManifest{}, nil, nil, nil,
			fmt.Errorf(
				"%w: intent, basis and compiled Run do not form one closure",
				ErrInvalidAdmission,
			)
	}
	if !conversationTurnIntentMatchesManifest(intent, manifest) {
		return corecontract.AdmissionIntentV1{},
			corecontract.MemberExecutionSnapshot{},
			corecontract.RunManifest{}, nil, nil, nil,
			fmt.Errorf(
				"%w: Conversation intent and RunManifest do not form one closure",
				ErrInvalidAdmission,
			)
	}

	if len(input.Contents) > moduleapi.MaxManifestEntries {
		return corecontract.AdmissionIntentV1{},
			corecontract.MemberExecutionSnapshot{},
			corecontract.RunManifest{}, nil, nil, nil,
			fmt.Errorf("%w: too many Admission contents", ErrInvalidAdmission)
	}
	required := make(map[string]ContentKind)
	policyRefs := make(map[string]corecontract.PolicyRef)
	addRequiredAdmissionContent := func(
		digest string,
		kind ContentKind,
	) error {
		if err := validateDigest(digest); err != nil {
			return fmt.Errorf(
				"%w: invalid required %s digest",
				ErrInvalidAdmission,
				kind,
			)
		}
		if existing, found := required[digest]; found && existing != kind {
			return fmt.Errorf(
				"%w: one digest is required as both %s and %s",
				ErrInvalidAdmission,
				existing,
				kind,
			)
		}
		required[digest] = kind
		return nil
	}
	if err := addRequiredAdmissionContent(
		manifest.TaskInputRef,
		ContentTaskInput,
	); err != nil {
		return corecontract.AdmissionIntentV1{},
			corecontract.MemberExecutionSnapshot{},
			corecontract.RunManifest{}, nil, nil, nil, err
	}
	for _, policy := range []corecontract.PolicyRef{
		member.ContextPolicy,
		member.SchedulingPolicy,
	} {
		if err := addRequiredAdmissionContent(
			policy.Digest,
			ContentPolicy,
		); err != nil {
			return corecontract.AdmissionIntentV1{},
				corecontract.MemberExecutionSnapshot{},
				corecontract.RunManifest{}, nil, nil, nil, err
		}
		if existing, found := policyRefs[policy.Digest]; found &&
			existing != policy {
			return corecontract.AdmissionIntentV1{},
				corecontract.MemberExecutionSnapshot{},
				corecontract.RunManifest{}, nil, nil, nil,
				fmt.Errorf(
					"%w: one POLICY digest has different typed refs",
					ErrInvalidAdmission,
				)
		}
		policyRefs[policy.Digest] = policy
	}
	if member.ModelProfile != nil {
		if err := addRequiredAdmissionContent(
			member.ModelProfile.Digest,
			ContentConfig,
		); err != nil {
			return corecontract.AdmissionIntentV1{},
				corecontract.MemberExecutionSnapshot{},
				corecontract.RunManifest{}, nil, nil, nil, err
		}
	}
	for _, plan := range member.PortPlans {
		for _, binding := range plan.Bindings {
			if err := addRequiredAdmissionContent(
				binding.ConfigRef,
				ContentConfig,
			); err != nil {
				return corecontract.AdmissionIntentV1{},
					corecontract.MemberExecutionSnapshot{},
					corecontract.RunManifest{}, nil, nil, nil, err
			}
			if err := addRequiredAdmissionContent(
				binding.AuthorityCeilingRef,
				ContentAuthorityCeiling,
			); err != nil {
				return corecontract.AdmissionIntentV1{},
					corecontract.MemberExecutionSnapshot{},
					corecontract.RunManifest{}, nil, nil, nil, err
			}
			for _, digest := range binding.StaticContextRefs {
				if err := addRequiredAdmissionContent(
					digest,
					ContentStaticContext,
				); err != nil {
					return corecontract.AdmissionIntentV1{},
						corecontract.MemberExecutionSnapshot{},
						corecontract.RunManifest{}, nil, nil, nil, err
				}
			}
		}
	}
	contents := make(map[string]preparedAdmissionContent, len(input.Contents))
	for index, content := range input.Contents {
		body := bytes.Clone(content.CanonicalBytes)
		computed, err := ComputeContentDigest(
			content.Kind,
			content.MediaType,
			body,
		)
		if err != nil || computed != content.Digest {
			return corecontract.AdmissionIntentV1{},
				corecontract.MemberExecutionSnapshot{},
				corecontract.RunManifest{}, nil, nil, nil,
				fmt.Errorf(
					"%w: content %d does not match its digest: %v",
					ErrInvalidAdmission,
					index,
					err,
				)
		}
		expected, requiredByRun := required[content.Digest]
		if !requiredByRun || expected != content.Kind {
			return corecontract.AdmissionIntentV1{},
				corecontract.MemberExecutionSnapshot{},
				corecontract.RunManifest{}, nil, nil, nil,
				fmt.Errorf(
					"%w: content %d is not an exact required Run content",
					ErrInvalidAdmission,
					index,
				)
		}
		prepared := preparedAdmissionContent{
			Digest:         content.Digest,
			Kind:           content.Kind,
			MediaType:      content.MediaType,
			CanonicalBytes: body,
		}
		if existing, duplicate := contents[content.Digest]; duplicate {
			if existing.Kind != prepared.Kind ||
				existing.MediaType != prepared.MediaType ||
				!bytes.Equal(
					existing.CanonicalBytes,
					prepared.CanonicalBytes,
				) {
				return corecontract.AdmissionIntentV1{},
					corecontract.MemberExecutionSnapshot{},
					corecontract.RunManifest{}, nil, nil, nil,
					fmt.Errorf(
						"%w: duplicate content %s differs",
						ErrInvalidAdmission,
						content.Digest,
					)
			}
			continue
		}
		contents[content.Digest] = prepared
	}
	return intent, member, manifest, contents, required, policyRefs, nil
}

func memberContainsRequestedPorts(
	member corecontract.MemberExecutionSnapshot,
	requested []moduleapi.PortRef,
) bool {
	available := make(map[string]struct{}, len(member.PortPlans))
	for _, plan := range member.PortPlans {
		key, _ := plan.Port.CanonicalKey()
		available[key] = struct{}{}
	}
	for _, port := range requested {
		key, _ := port.CanonicalKey()
		if _, found := available[key]; !found {
			return false
		}
	}
	return true
}

func requireAdmissionRunIDAvailable(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	runID string,
) error {
	var existing string
	err := queryer.QueryRowContext(
		ctx,
		`SELECT run_id FROM runs WHERE run_id=?`,
		runID,
	).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect Run ID before Admission: %w",
			err,
		)
	}
	return fmt.Errorf(
		"%w: Run ID %q is already allocated",
		ErrAdmissionConflict,
		runID,
	)
}

func verifyCurrentAdmissionBasis(
	ctx context.Context,
	connection *sql.Conn,
	basis controlcontract.PublishedBasis,
) (
	controlcontract.ControlSnapshot,
	controlcontract.CatalogGeneration,
	error,
) {
	current, found, err := queryCurrentControlPointer(
		ctx,
		connection,
		basis.TenantID,
	)
	if err != nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, err
	}
	if !found ||
		current.PointerRevision != basis.PointerRevision ||
		current.SnapshotID != basis.Control.SnapshotID ||
		current.CatalogGenerationID != basis.Catalog.GenerationID {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf(
				"%w: current Control/Catalog pointer no longer matches compiled basis",
				ErrAdmissionConflict,
			)
	}
	control, catalog, err := loadAdmissionControlCatalog(
		ctx,
		connection,
		basis.TenantID,
		basis.Control.SnapshotID,
		basis.Catalog.GenerationID,
	)
	if err != nil {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{}, err
	}
	if control.SnapshotID != basis.Control.SnapshotID ||
		control.Revision != basis.Control.Revision ||
		control.Digest != basis.Control.Digest ||
		catalog.GenerationID != basis.Catalog.GenerationID ||
		catalog.Generation != basis.Catalog.Generation ||
		catalog.Digest != basis.Catalog.Digest {
		return controlcontract.ControlSnapshot{},
			controlcontract.CatalogGeneration{},
			fmt.Errorf(
				"%w: frozen Control/Catalog rows differ from compiled basis",
				ErrAdmissionConflict,
			)
	}
	return control, catalog, nil
}

func verifyProspectiveAdmissionClosure(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	manifest corecontract.RunManifest,
	member corecontract.MemberExecutionSnapshot,
	contents map[string]preparedAdmissionContent,
	required map[string]ContentKind,
	policyRefs map[string]corecontract.PolicyRef,
) error {
	for digest, kind := range required {
		record, err := prospectiveAdmissionContent(
			ctx,
			queryer,
			contents,
			digest,
		)
		if err != nil {
			return err
		}
		if record.Kind != kind {
			return fmt.Errorf(
				"%w: content %s has kind %s, want %s",
				ErrAdmissionIntegrity,
				digest,
				record.Kind,
				kind,
			)
		}
		if kind == ContentTaskInput || kind == ContentStaticContext {
			if err := verifyS1ChatContent(record); err != nil {
				return err
			}
		}
	}
	for digest, ref := range policyRefs {
		record, err := prospectiveAdmissionContent(
			ctx,
			queryer,
			contents,
			digest,
		)
		if err != nil {
			return err
		}
		if record.MediaType != admissionJSONMediaType {
			return fmt.Errorf(
				"%w: POLICY %s has media type %q",
				ErrAdmissionIntegrity,
				digest,
				record.MediaType,
			)
		}
		if _, err := corecontract.RestorePolicyDocument(
			record.CanonicalBytes,
			ref,
		); err != nil {
			return fmt.Errorf(
				"%w: PolicyRef %s does not match POLICY content",
				ErrAdmissionIntegrity,
				digest,
			)
		}
	}
	if err := validateMemberModelProfileClosure(
		member,
		func(digest string) (ContentRecord, error) {
			return prospectiveAdmissionContent(
				ctx,
				queryer,
				contents,
				digest,
			)
		},
	); err != nil {
		return fmt.Errorf(
			"%w: frozen ModelProfile closure: %v",
			ErrAdmissionIntegrity,
			err,
		)
	}
	if _, err := frozenKnowledgeBindingsForRun(
		manifest,
		member,
		func(digest string) (ContentRecord, error) {
			return prospectiveAdmissionContent(
				ctx,
				queryer,
				contents,
				digest,
			)
		},
	); err != nil {
		return fmt.Errorf(
			"%w: frozen dynamic context closure: %v",
			ErrAdmissionIntegrity,
			err,
		)
	}
	if _, err := frozenMemoryBindingsForRun(
		manifest,
		member,
		func(digest string) (ContentRecord, error) {
			return prospectiveAdmissionContent(
				ctx,
				queryer,
				contents,
				digest,
			)
		},
	); err != nil {
		return fmt.Errorf(
			"%w: frozen dynamic Memory closure: %v",
			ErrAdmissionIntegrity,
			err,
		)
	}
	for _, plan := range member.PortPlans {
		for _, binding := range plan.Bindings {
			if err := verifyAdmissionActivation(
				ctx,
				queryer,
				manifest.TenantID,
				binding.Provider,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func prospectiveAdmissionContent(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	contents map[string]preparedAdmissionContent,
	digest string,
) (ContentRecord, error) {
	if content, found := contents[digest]; found {
		return ContentRecord{
			Digest:         content.Digest,
			Kind:           content.Kind,
			MediaType:      content.MediaType,
			CanonicalBytes: bytes.Clone(content.CanonicalBytes),
			SizeBytes:      int64(len(content.CanonicalBytes)),
		}, nil
	}
	record, err := queryContent(ctx, queryer, digest)
	if err != nil {
		return ContentRecord{}, fmt.Errorf(
			"%w: required content %s is unavailable",
			ErrAdmissionIntegrity,
			digest,
		)
	}
	return record, nil
}

func putAdmissionContent(
	ctx context.Context,
	connection *sql.Conn,
	content preparedAdmissionContent,
	createdAt int64,
) error {
	result, err := connection.ExecContext(ctx, `
		INSERT INTO content_records(
			content_digest,
			kind,
			media_type,
			canonical_bytes,
			size_bytes,
			created_at
		) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(content_digest) DO NOTHING
	`,
		content.Digest,
		string(content.Kind),
		content.MediaType,
		content.CanonicalBytes,
		len(content.CanonicalBytes),
		createdAt,
	)
	if err != nil {
		return fmt.Errorf(
			"currentstore: insert Admission content %s: %w",
			content.Digest,
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect Admission content insert: %w",
			err,
		)
	}
	switch affected {
	case 1:
		return nil
	case 0:
		record, err := queryContent(ctx, connection, content.Digest)
		if err != nil {
			return err
		}
		if record.Kind != content.Kind ||
			record.MediaType != content.MediaType ||
			record.SizeBytes != int64(len(content.CanonicalBytes)) ||
			!bytes.Equal(record.CanonicalBytes, content.CanonicalBytes) {
			return fmt.Errorf(
				"%w: stored content %s differs",
				ErrAdmissionIntegrity,
				content.Digest,
			)
		}
		return nil
	default:
		return fmt.Errorf(
			"%w: content insert affected %d rows",
			ErrAdmissionIntegrity,
			affected,
		)
	}
}

func insertAdmissionRun(
	ctx context.Context,
	connection *sql.Conn,
	manifest corecontract.RunManifest,
	createdAt int64,
) error {
	var conversationID any
	var conversationTurnIndex any
	var conversationPredecessorRunID any
	if manifest.ConversationTurn != nil {
		conversationID = manifest.ConversationTurn.ConversationID
		conversationTurnIndex = manifest.ConversationTurn.TurnIndex
		if manifest.ConversationTurn.PredecessorRunID != "" {
			conversationPredecessorRunID = manifest.ConversationTurn.PredecessorRunID
		}
	}
	var parentRunID any
	var parentManifestDigest any
	var parentSlotID any
	var disposition any
	if manifest.Composite != nil &&
		(manifest.Composite.Role == corecontract.CompositeRunRoleChildV1 ||
			manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1) {
		parentRunID = manifest.ParentRunID
		parentManifestDigest = manifest.Composite.ParentManifestDigest
		parentSlotID = manifest.Composite.ParentSlotID
	}
	if manifest.Composite != nil &&
		(manifest.Composite.RepairRound == corecontract.CompositeRepairRoundOneV1 ||
			manifest.Composite.Role == corecontract.CompositeRunRoleRootV1 ||
			manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1) {
		disposition = string("WAITING_EXTERNAL")
	}
	result, err := connection.ExecContext(ctx, `
		INSERT INTO runs(
			run_id, tenant_id, workspace_id, admission_key,
			conversation_id, conversation_turn_index,
			conversation_predecessor_run_id,
			admission_intent_digest,
			parent_run_id, parent_manifest_digest, parent_slot_id,
			cancel_request_ref, state, disposition,
			revision, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, 0, ?, ?)
	`,
		manifest.RunID,
		manifest.TenantID,
		manifest.Workspace.ID,
		manifest.AdmissionKey,
		conversationID,
		conversationTurnIndex,
		conversationPredecessorRunID,
		manifest.AdmissionIntentDigest,
		parentRunID,
		parentManifestDigest,
		parentSlotID,
		corecontract.InitialRunState,
		disposition,
		createdAt,
		createdAt,
	)
	if err != nil {
		return fmt.Errorf("currentstore: insert admitted Run: %w", err)
	}
	return requireOneAdmissionRow(result, "insert admitted Run")
}

func insertAdmissionMember(
	ctx context.Context,
	connection *sql.Conn,
	basis controlcontract.PublishedBasis,
	runID string,
	member corecontract.MemberExecutionSnapshot,
	canonical []byte,
) error {
	result, err := connection.ExecContext(ctx, `
		INSERT INTO member_execution_snapshots(
			run_id, member_id,
			agent_id, agent_version, agent_digest,
			profile_id, profile_version, profile_digest,
			workspace_id, workspace_version, workspace_digest,
			control_snapshot_id, catalog_generation_id,
			canonical_json, digest
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		runID,
		member.MemberID,
		member.Agent.ID,
		member.Agent.Version,
		member.Agent.Digest,
		member.Profile.ID,
		member.Profile.Version,
		member.Profile.Digest,
		member.Workspace.ID,
		member.Workspace.Version,
		member.Workspace.Digest,
		basis.Control.SnapshotID,
		basis.Catalog.GenerationID,
		canonical,
		member.MemberSnapshotDigest,
	)
	if err != nil {
		return fmt.Errorf(
			"currentstore: insert admitted member snapshot: %w",
			err,
		)
	}
	return requireOneAdmissionRow(result, "insert admitted member snapshot")
}

func insertAdmissionManifest(
	ctx context.Context,
	connection *sql.Conn,
	manifest corecontract.RunManifest,
	canonical []byte,
	publishedAt int64,
) error {
	result, err := connection.ExecContext(ctx, `
		INSERT INTO run_manifests(
			run_id, canonical_json, digest, published_at
		) VALUES(?, ?, ?, ?)
	`,
		manifest.RunID,
		canonical,
		manifest.ManifestDigest,
		publishedAt,
	)
	if err != nil {
		return fmt.Errorf("currentstore: insert RunManifest: %w", err)
	}
	return requireOneAdmissionRow(result, "insert RunManifest")
}

func insertInitialAdmissionFrame(
	ctx context.Context,
	connection *sql.Conn,
	manifest corecontract.RunManifest,
) error {
	runID := manifest.RunID
	step := corecontract.InitialLoopStep
	waitingReason := any(nil)
	budgetRef, continuation, err := corecontract.NewInitialLoopState(runID)
	if manifest.Composite != nil &&
		manifest.Composite.RepairRound == corecontract.CompositeRepairRoundOneV1 {
		step = corecontract.WaitingRepairActivationLoopStep
		waitingReason = compositeRepairDormantWaitingReason
		budgetRef, continuation, err =
			corecontract.NewWaitingRepairActivationLoopState(runID)
	} else if manifest.Composite != nil &&
		(manifest.Composite.Role == corecontract.CompositeRunRoleRootV1 ||
			manifest.Composite.Role == corecontract.CompositeRunRoleReviewerV1) {
		step = corecontract.WaitingChildrenLoopStep
		waitingReason = compositeChildrenPendingWaitingReason
		budgetRef, continuation, err =
			corecontract.NewWaitingChildrenLoopState(runID)
	}
	if err != nil {
		return fmt.Errorf(
			"%w: construct initial LoopFrame: %v",
			ErrAdmissionIntegrity,
			err,
		)
	}
	result, err := connection.ExecContext(ctx, `
		INSERT INTO loop_frames(
			run_id, frame_revision, step, usage_ledger_ref,
			continuation, pending_attempt_id, waiting_reason,
			last_authoritative_event, lease_owner, lease_epoch,
			lease_expiry
		) VALUES(?, 0, ?, ?, ?, NULL, ?, 0, NULL, 0, NULL)
	`,
		runID,
		step,
		budgetRef,
		continuation,
		waitingReason,
	)
	if err != nil {
		return fmt.Errorf("currentstore: insert initial LoopFrame: %w", err)
	}
	return requireOneAdmissionRow(result, "insert initial LoopFrame")
}

func insertInitialAdmissionEvent(
	ctx context.Context,
	connection *sql.Conn,
	runID string,
	payloadDigest string,
	createdAt int64,
) error {
	result, err := connection.ExecContext(ctx, `
		INSERT INTO run_events(
			run_id, event_sequence, event_kind,
			from_revision, to_revision,
			payload_ref, payload_digest, created_at
		) VALUES(?, 0, ?, 0, 0, ?, ?, ?)
	`,
		runID,
		corecontract.RunAdmittedEventKind,
		payloadDigest,
		payloadDigest,
		createdAt,
	)
	if err != nil {
		return fmt.Errorf("currentstore: insert RunEvent zero: %w", err)
	}
	return requireOneAdmissionRow(result, "insert RunEvent zero")
}

func requireOneAdmissionRow(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(
			"currentstore: inspect %s: %w",
			operation,
			err,
		)
	}
	if affected != 1 {
		return fmt.Errorf(
			"%w: %s affected %d rows",
			ErrAdmissionIntegrity,
			operation,
			affected,
		)
	}
	return nil
}
