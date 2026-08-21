// Package moduleupgrade owns the inert, content-addressed projection used to
// review one exact module upgrade. It performs no Store, filesystem, network,
// package, Host, Apply, or module operation.
package moduleupgrade

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/endview/freeagent/internal/controlcontract"
	"github.com/endview/freeagent/sdk/moduleapi"
)

const (
	ReviewSchemaVersionV1  = "module-upgrade-review/v1"
	ReviewIDDigestDomainV1 = "freeagent.module-upgrade-review/v1"

	MaxReviewCanonicalBytesV1 = 1 << 20
	MaxReviewCollectionV1     = moduleapi.MaxManifestEntries
)

type BindingTargetKindV1 string

const (
	BindingTargetProfileV1                  BindingTargetKindV1 = "PROFILE"
	BindingTargetWorkspaceChannelEndpointV1 BindingTargetKindV1 = "WORKSPACE_CHANNEL_ENDPOINT"
)

// BindingTargetV1 is the exact consumer-owned Apply target. PROFILE does not
// imply Workspace ownership; the Workspace fields are legal only for a
// channel endpoint target.
type BindingTargetV1 struct {
	Kind        BindingTargetKindV1 `json:"kind"`
	ProfileID   string              `json:"profile_id,omitempty"`
	WorkspaceID string              `json:"workspace_id,omitempty"`
	EndpointID  string              `json:"endpoint_id,omitempty"`
}

type SignatureStatusV1 string

const (
	SignatureNotRequiredV1 SignatureStatusV1 = "NOT_REQUIRED"
	SignatureVerifiedV1    SignatureStatusV1 = "VERIFIED"
)

// SupplyBasisV1 contains only immutable IDs and Store revisions. It never
// carries an origin, path, URL, public key, signature, or package body.
type SupplyBasisV1 struct {
	SourceID             string            `json:"source_id"`
	SourcePolicyID       string            `json:"source_policy_id"`
	SourcePolicyRevision uint64            `json:"source_policy_revision"`
	SnapshotID           string            `json:"snapshot_id"`
	IndexID              string            `json:"index_id"`
	ObservationRevision  uint64            `json:"observation_revision"`
	PublisherKeyID       string            `json:"publisher_key_id,omitempty"`
	PublisherKeyRevision uint64            `json:"publisher_key_revision,omitempty"`
	SignatureStatus      SignatureStatusV1 `json:"signature_status"`
}

type RuntimeSummaryV1 struct {
	Mode       moduleapi.RuntimeModeRequest `json:"mode"`
	Protocol   string                       `json:"protocol"`
	Entrypoint string                       `json:"entrypoint"`
}

// ManifestSummaryV1 includes only a validated artifact-relative runtime
// entrypoint and omits host paths, URLs, and all optional Manifest object
// bodies. ManifestRef is the authority-free link to the exact recoverable
// Manifest.
type ManifestSummaryV1 struct {
	Module               moduleapi.Ref          `json:"module"`
	Runtime              RuntimeSummaryV1       `json:"runtime"`
	Provides             []moduleapi.PortRef    `json:"provides"`
	Requires             []moduleapi.PortRef    `json:"requires"`
	RequestedPermissions []moduleapi.Permission `json:"requested_permissions"`
}

type CurrentExactV1 struct {
	Activation     moduleapi.ActivatedModuleRef `json:"activation"`
	InstallationID string                       `json:"installation_id"`
	ManifestRef    string                       `json:"manifest_ref"`
	Manifest       ManifestSummaryV1            `json:"manifest"`
}

// TargetEvidenceV1 is the safe projection of one exact Discovery entry and
// its locally verified Manifest. PackagePath and detached signature bytes are
// intentionally absent.
type TargetEvidenceV1 struct {
	Module            moduleapi.Ref     `json:"module"`
	ArtifactDigest    string            `json:"artifact_digest"`
	ArtifactSizeBytes uint64            `json:"artifact_size_bytes"`
	SignatureID       string            `json:"signature_id,omitempty"`
	ManifestRef       string            `json:"manifest_ref"`
	Manifest          ManifestSummaryV1 `json:"manifest"`
}

type ManifestDiffV1 struct {
	RuntimeChanged     bool                   `json:"runtime_changed"`
	ProvidesAdded      []moduleapi.PortRef    `json:"provides_added"`
	ProvidesRemoved    []moduleapi.PortRef    `json:"provides_removed"`
	RequiresAdded      []moduleapi.PortRef    `json:"requires_added"`
	RequiresRemoved    []moduleapi.PortRef    `json:"requires_removed"`
	PermissionsAdded   []moduleapi.Permission `json:"permissions_added"`
	PermissionsRemoved []moduleapi.Permission `json:"permissions_removed"`
}

type HandlerStatusV1 string

const (
	HandlerSupportedV1   HandlerStatusV1 = "SUPPORTED"
	HandlerUnsupportedV1 HandlerStatusV1 = "UNSUPPORTED"
	HandlerConflictV1    HandlerStatusV1 = "CONFLICT"
)

type HandlerAssessmentV1 struct {
	Status          HandlerStatusV1          `json:"status"`
	Kind            string                   `json:"kind,omitempty"`
	ExecutionClass  moduleapi.ExecutionClass `json:"execution_class,omitempty"`
	AdapterIdentity string                   `json:"adapter_identity,omitempty"`
}

type BindingImpactV1 struct {
	BindingTarget       BindingTargetV1         `json:"binding_target"`
	Port                moduleapi.PortRef       `json:"port"`
	PortBindingIndex    uint32                  `json:"port_binding_index"`
	CurrentInstanceID   string                  `json:"current_instance_id"`
	TargetInstanceID    string                  `json:"target_instance_id"`
	ConfigRef           string                  `json:"config_ref"`
	AuthorityCeilingRef string                  `json:"authority_ceiling_ref"`
	StaticContextRefs   []string                `json:"static_context_refs"`
	FailurePolicy       moduleapi.FailurePolicy `json:"failure_policy"`
}

type RequiredGrantKindV1 string

const (
	GrantLocalProcessArtifactV1     RequiredGrantKindV1 = "LOCAL_PROCESS_ARTIFACT"
	GrantTrustedInProcessArtifactV1 RequiredGrantKindV1 = "TRUSTED_IN_PROCESS_ARTIFACT"
	GrantRemoteActionArtifactV1     RequiredGrantKindV1 = "REMOTE_ACTION_ARTIFACT"
	GrantWASMActionArtifactV1       RequiredGrantKindV1 = "WASM_ACTION_ARTIFACT"
	GrantRemoteEndpointDigestV1     RequiredGrantKindV1 = "REMOTE_ENDPOINT_DIGEST"
	GrantRemoteCredentialDigestV1   RequiredGrantKindV1 = "REMOTE_SECRET_REF_DIGEST"
	GrantModelCredentialDigestV1    RequiredGrantKindV1 = "MODEL_SECRET_REF_DIGEST"
)

// RequiredGrantV1 uses a digest-only reference. In particular, endpoint URLs
// and SecretRef strings cannot enter the review wire.
type RequiredGrantV1 struct {
	Kind            RequiredGrantKindV1 `json:"kind"`
	ReferenceDigest string              `json:"reference_digest"`
}

type ConclusionV1 string

const (
	ConclusionWouldApplyV1  ConclusionV1 = "WOULD_APPLY"
	ConclusionConflictV1    ConclusionV1 = "CONFLICT"
	ConclusionUnsupportedV1 ConclusionV1 = "UNSUPPORTED"
)

type ReasonCodeV1 string

const (
	ReasonHandlerUnsupportedV1           ReasonCodeV1 = "HANDLER_UNSUPPORTED"
	ReasonHandlerConflictV1              ReasonCodeV1 = "HANDLER_CONFLICT"
	ReasonTargetPortRemovedV1            ReasonCodeV1 = "TARGET_PORT_REMOVED"
	ReasonTargetRequirementsChangedV1    ReasonCodeV1 = "TARGET_REQUIREMENTS_CHANGED"
	ReasonRequestedPermissionsExpandedV1 ReasonCodeV1 = "REQUESTED_PERMISSIONS_EXPANDED"
	ReasonPublishedBindingConflictV1     ReasonCodeV1 = "PUBLISHED_BINDING_CONFLICT"
	ReasonTargetInstanceConflictV1       ReasonCodeV1 = "TARGET_INSTANCE_CONFLICT"
	ReasonOperatorGrantRequiredV1        ReasonCodeV1 = "OPERATOR_GRANT_REQUIRED"
	ReasonOperatorRequestedRollbackV1    ReasonCodeV1 = "OPERATOR_REQUESTED_ROLLBACK"
)

// ReviewV1 is an inert Tenant-scoped review projection. CandidateID and
// ReviewKey keep their independently frozen U0 meanings; ReviewID identifies
// this exact target/basis/diff projection only.
type ReviewV1 struct {
	SchemaVersion    string                         `json:"schema_version"`
	CandidateID      string                         `json:"candidate_id"`
	ReviewKey        string                         `json:"review_key"`
	TenantID         string                         `json:"tenant_id"`
	BindingTarget    BindingTargetV1                `json:"binding_target"`
	Port             moduleapi.PortRef              `json:"port"`
	PortBindingIndex uint32                         `json:"port_binding_index"`
	TargetInstanceID string                         `json:"target_instance_id"`
	SupplyBasis      SupplyBasisV1                  `json:"supply_basis"`
	PublishedBasis   controlcontract.PublishedBasis `json:"published_basis"`
	Current          CurrentExactV1                 `json:"current"`
	Target           TargetEvidenceV1               `json:"target"`
	Diff             ManifestDiffV1                 `json:"diff"`
	Handler          HandlerAssessmentV1            `json:"handler"`
	BindingImpacts   []BindingImpactV1              `json:"binding_impacts"`
	RequiredGrants   []RequiredGrantV1              `json:"required_grants"`
	Conclusion       ConclusionV1                   `json:"conclusion"`
	ReasonCodes      []ReasonCodeV1                 `json:"reason_codes"`
}

// NewReviewV1 validates and freezes a review. The exact U0 Candidate and its
// exact Snapshot are mandatory parents; caller-provided IDs alone are never
// treated as an authorized observation.
func NewReviewV1(input ReviewV1, candidateCanonical, snapshotCanonical []byte) (ReviewV1, []byte, string, error) {
	if input.SchemaVersion != ReviewSchemaVersionV1 {
		return ReviewV1{}, nil, "", fmt.Errorf("moduleupgrade: schema_version must be %q", ReviewSchemaVersionV1)
	}
	if !moduleapi.ValidSHA256(input.CandidateID) || !moduleapi.ValidSHA256(input.ReviewKey) {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: candidate_id and review_key must be SHA-256")
	}
	if err := validateOpaque("tenant_id", input.TenantID); err != nil {
		return ReviewV1{}, nil, "", err
	}
	if err := validateBindingTarget(input.BindingTarget); err != nil {
		return ReviewV1{}, nil, "", err
	}
	if err := input.Port.Validate(); err != nil {
		return ReviewV1{}, nil, "", fmt.Errorf("moduleupgrade: review port: %w", err)
	}
	if !isCurrentPort(input.Port) {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: review port is not registered in the current exact Port set")
	}
	if err := validateTargetPort(input.BindingTarget, input.Port); err != nil {
		return ReviewV1{}, nil, "", err
	}
	if err := validateOpaque("target_instance_id", input.TargetInstanceID); err != nil {
		return ReviewV1{}, nil, "", err
	}
	if err := validateSupplyBasis(input.SupplyBasis); err != nil {
		return ReviewV1{}, nil, "", err
	}
	if err := input.PublishedBasis.Validate(); err != nil || input.PublishedBasis.TenantID != input.TenantID {
		return ReviewV1{}, nil, "", errors.Join(err, errors.New("moduleupgrade: PublishedBasis must be valid and match tenant_id"))
	}

	var snapshot moduleapi.ModuleDiscoverySnapshotV1
	if err := decodeStrictCanonical(snapshotCanonical, moduleapi.MaxModuleDiscoverySnapshotBytesV1, &snapshot); err != nil {
		return ReviewV1{}, nil, "", fmt.Errorf("moduleupgrade: Snapshot parent: %w", err)
	}
	if snapshot.SchemaVersion != moduleapi.ModuleDiscoverySnapshotSchemaVersionV1 ||
		input.SupplyBasis.SourceID != snapshot.SourceID ||
		input.SupplyBasis.SourcePolicyID != snapshot.SourcePolicyID ||
		input.SupplyBasis.IndexID != snapshot.IndexID {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: SupplyBasis differs from exact Snapshot parent")
	}
	candidate, err := moduleapi.RestoreModuleUpgradeCandidateV1(candidateCanonical, input.CandidateID, snapshotCanonical, input.SupplyBasis.SnapshotID)
	if err != nil {
		return ReviewV1{}, nil, "", fmt.Errorf("moduleupgrade: Candidate parent: %w", err)
	}
	if candidate.Change != moduleapi.ModuleCandidateChangeExactVersionV1 || candidate.Current == nil {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: v1 review accepts only EXACT_VERSION_CHANGE Candidate")
	}
	if input.ReviewKey != candidate.ReviewKey || input.SupplyBasis.SourceID != candidate.SourceID ||
		input.SupplyBasis.SourcePolicyID != candidate.SourcePolicyID || input.SupplyBasis.SnapshotID != candidate.SnapshotID {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: Candidate identities differ from review basis")
	}

	current, err := normalizeCurrent(input.Current)
	if err != nil {
		return ReviewV1{}, nil, "", err
	}
	target, err := normalizeTarget(input.Target)
	if err != nil {
		return ReviewV1{}, nil, "", err
	}
	if current.Activation.InstanceID == input.TargetInstanceID {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: target instance must differ from current instance")
	}
	if current.Activation.ModuleID != candidate.Current.Module.ID || current.Activation.Version != candidate.Current.Module.Version ||
		current.Activation.ArtifactDigest != candidate.Current.ArtifactDigest ||
		target.Module != candidate.Target.Module || target.ArtifactDigest != candidate.Target.ArtifactDigest ||
		target.ArtifactSizeBytes != candidate.Target.ArtifactSizeBytes || target.SignatureID != candidate.Target.SignatureID {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: current or target differs from exact Candidate")
	}
	if err := validateSignatureClosure(input.SupplyBasis, target.SignatureID); err != nil {
		return ReviewV1{}, nil, "", err
	}
	if !containsPort(current.Manifest.Provides, input.Port) {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: current Manifest does not provide selected port")
	}
	if runtimeExecutionClass(current.Manifest.Runtime.Mode) != current.Activation.ExecutionClass {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: current runtime request and Activation ExecutionClass differ")
	}

	diff := computeManifestDiff(current.Manifest, target.Manifest)
	if diffSupplied(input.Diff) {
		normalized, diffErr := normalizeDiff(input.Diff)
		if diffErr != nil || !reflect.DeepEqual(normalized, diff) {
			return ReviewV1{}, nil, "", errors.Join(diffErr, errors.New("moduleupgrade: supplied Manifest diff is not deterministic"))
		}
	}
	handler, err := normalizeHandler(input.Handler)
	if err != nil {
		return ReviewV1{}, nil, "", err
	}
	if handler.Status != HandlerUnsupportedV1 &&
		runtimeExecutionClass(target.Manifest.Runtime.Mode) != handler.ExecutionClass {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: target runtime request and handler ExecutionClass differ")
	}
	impacts, err := normalizeImpacts(input.BindingImpacts, current.Activation.InstanceID, input.TargetInstanceID)
	if err != nil {
		return ReviewV1{}, nil, "", err
	}
	for _, impact := range impacts {
		if !containsPort(current.Manifest.Provides, impact.Port) {
			return ReviewV1{}, nil, "", errors.New("moduleupgrade: Binding impact Port is absent from current Manifest")
		}
	}
	if !containsSelectedImpact(impacts, input.BindingTarget, input.Port, input.PortBindingIndex) {
		return ReviewV1{}, nil, "", errors.New("moduleupgrade: binding impacts omit the selected exact Binding")
	}
	grants, err := normalizeGrants(input.RequiredGrants, target.ArtifactDigest)
	if err != nil {
		return ReviewV1{}, nil, "", err
	}
	if err := validateGrantHandlerClosure(grants, handler); err != nil {
		return ReviewV1{}, nil, "", err
	}
	reasons, err := normalizeReasons(input.ReasonCodes)
	if err != nil {
		return ReviewV1{}, nil, "", err
	}
	if err := validateConclusion(input.Conclusion, handler, diff, target.Manifest, impacts, grants, reasons); err != nil {
		return ReviewV1{}, nil, "", err
	}

	frozen := input
	frozen.Current = current
	frozen.Target = target
	frozen.Diff = diff
	frozen.Handler = handler
	frozen.BindingImpacts = impacts
	frozen.RequiredGrants = grants
	frozen.ReasonCodes = reasons
	canonical, err := marshalCanonical(frozen)
	if err != nil {
		return ReviewV1{}, nil, "", err
	}
	return cloneReview(frozen), bytes.Clone(canonical), moduleapi.Digest(ReviewIDDigestDomainV1, canonical), nil
}

func RestoreReviewV1(canonical []byte, expectedReviewID string, candidateCanonical, snapshotCanonical []byte) (ReviewV1, error) {
	if !moduleapi.ValidSHA256(expectedReviewID) {
		return ReviewV1{}, errors.New("moduleupgrade: expected ReviewID must be SHA-256")
	}
	var decoded ReviewV1
	if err := decodeStrictCanonical(canonical, MaxReviewCanonicalBytesV1, &decoded); err != nil {
		return ReviewV1{}, err
	}
	restored, rebuilt, reviewID, err := NewReviewV1(decoded, candidateCanonical, snapshotCanonical)
	if err != nil {
		return ReviewV1{}, err
	}
	if reviewID != expectedReviewID || !bytes.Equal(rebuilt, canonical) {
		return ReviewV1{}, errors.New("moduleupgrade: review is not the expected frozen canonical value")
	}
	return restored, nil
}

func normalizeCurrent(input CurrentExactV1) (CurrentExactV1, error) {
	if err := input.Activation.Validate(); err != nil {
		return CurrentExactV1{}, fmt.Errorf("moduleupgrade: current Activation: %w", err)
	}
	if err := validateOpaque("current installation_id", input.InstallationID); err != nil {
		return CurrentExactV1{}, err
	}
	if !moduleapi.ValidSHA256(input.ManifestRef) {
		return CurrentExactV1{}, errors.New("moduleupgrade: current manifest_ref must be SHA-256")
	}
	manifest, err := normalizeManifest(input.Manifest)
	if err != nil {
		return CurrentExactV1{}, fmt.Errorf("moduleupgrade: current Manifest summary: %w", err)
	}
	if manifest.Module.ID != input.Activation.ModuleID || manifest.Module.Version != input.Activation.Version {
		return CurrentExactV1{}, errors.New("moduleupgrade: current Manifest and Activation identities differ")
	}
	input.Manifest = manifest
	return input, nil
}

func normalizeTarget(input TargetEvidenceV1) (TargetEvidenceV1, error) {
	if err := input.Module.Validate(); err != nil || !moduleapi.ValidSHA256(input.ArtifactDigest) || input.ArtifactSizeBytes == 0 {
		return TargetEvidenceV1{}, errors.Join(err, errors.New("moduleupgrade: target entry identity, digest, or size is invalid"))
	}
	if input.SignatureID != "" && !moduleapi.ValidSHA256(input.SignatureID) {
		return TargetEvidenceV1{}, errors.New("moduleupgrade: target signature_id must be empty or SHA-256")
	}
	if !moduleapi.ValidSHA256(input.ManifestRef) {
		return TargetEvidenceV1{}, errors.New("moduleupgrade: target manifest_ref must be SHA-256")
	}
	manifest, err := normalizeManifest(input.Manifest)
	if err != nil {
		return TargetEvidenceV1{}, fmt.Errorf("moduleupgrade: target Manifest summary: %w", err)
	}
	if manifest.Module != input.Module {
		return TargetEvidenceV1{}, errors.New("moduleupgrade: target entry and Manifest identities differ")
	}
	input.Manifest = manifest
	return input, nil
}

func normalizeManifest(input ManifestSummaryV1) (ManifestSummaryV1, error) {
	// Rebuild an ordinary Manifest and reuse the public SDK validator so this
	// safe projection follows the exact runtime entrypoint/protocol rules. For
	// external runtimes the entrypoint is an artifact-covered relative path or
	// adapter identity, never a host path or endpoint URL.
	if unsafeRuntimeEntrypointProjection(input.Runtime.Entrypoint) {
		return ManifestSummaryV1{}, errors.New("moduleupgrade: runtime entrypoint is unsafe for the persisted review projection")
	}
	manifest := moduleapi.ModuleManifestV1{
		APIVersion: moduleapi.ModuleManifestAPIVersionV1,
		ID:         input.Module.ID,
		Version:    input.Module.Version,
		Runtime: moduleapi.RuntimeRequestV1{
			Mode:       input.Runtime.Mode,
			Protocol:   input.Runtime.Protocol,
			Entrypoint: input.Runtime.Entrypoint,
		},
		Provides:             input.Provides,
		Requires:             input.Requires,
		RequestedPermissions: input.RequestedPermissions,
	}
	if err := manifest.Validate(); err != nil {
		return ManifestSummaryV1{}, err
	}
	provides, err := normalizePorts("provides", input.Provides, true)
	if err != nil {
		return ManifestSummaryV1{}, err
	}
	requires, err := normalizePorts("requires", input.Requires, false)
	if err != nil {
		return ManifestSummaryV1{}, err
	}
	provided := make(map[string]struct{}, len(provides))
	for _, port := range provides {
		key, _ := port.CanonicalKey()
		provided[key] = struct{}{}
	}
	for _, port := range requires {
		key, _ := port.CanonicalKey()
		if _, exists := provided[key]; exists {
			return ManifestSummaryV1{}, errors.New("Manifest summary cannot both provide and require one exact port")
		}
	}
	permissions := append([]moduleapi.Permission(nil), input.RequestedPermissions...)
	if len(permissions) > MaxReviewCollectionV1 {
		return ManifestSummaryV1{}, errors.New("moduleupgrade: too many requested permissions")
	}
	for _, permission := range permissions {
		if err := permission.Validate(); err != nil {
			return ManifestSummaryV1{}, err
		}
	}
	sort.Slice(permissions, func(i, j int) bool { return permissions[i] < permissions[j] })
	for i := 1; i < len(permissions); i++ {
		if permissions[i] == permissions[i-1] {
			return ManifestSummaryV1{}, errors.New("moduleupgrade: duplicate requested permission")
		}
	}
	input.Provides = nonNilPorts(provides)
	input.Requires = nonNilPorts(requires)
	input.RequestedPermissions = nonNilPermissions(permissions)
	return input, nil
}

func unsafeRuntimeEntrypointProjection(input string) bool {
	canonicalRelative, err := moduleapi.NormalizeArtifactPath(input)
	if err != nil || canonicalRelative != input {
		return true
	}
	normalized := strings.ReplaceAll(strings.TrimSpace(input), "/", `\`)
	lower := strings.ToLower(normalized)
	return normalized == "" ||
		strings.Contains(lower, "://") ||
		strings.Contains(normalized, ":") ||
		strings.HasPrefix(normalized, `\`) ||
		strings.HasPrefix(lower, `\??\`) ||
		strings.HasPrefix(lower, `\\?\`) ||
		strings.HasPrefix(lower, `\\.\`)
}

func normalizePorts(name string, input []moduleapi.PortRef, requireNonEmpty bool) ([]moduleapi.PortRef, error) {
	if requireNonEmpty && len(input) == 0 || len(input) > MaxReviewCollectionV1 {
		return nil, fmt.Errorf("moduleupgrade: Manifest %s has invalid count", name)
	}
	output := append([]moduleapi.PortRef(nil), input...)
	for _, port := range output {
		if err := port.Validate(); err != nil {
			return nil, err
		}
	}
	sort.Slice(output, func(i, j int) bool { return portKey(output[i]) < portKey(output[j]) })
	for i := 1; i < len(output); i++ {
		if output[i] == output[i-1] {
			return nil, fmt.Errorf("moduleupgrade: duplicate exact port in %s", name)
		}
	}
	return output, nil
}

func computeManifestDiff(current, target ManifestSummaryV1) ManifestDiffV1 {
	return ManifestDiffV1{
		RuntimeChanged:     current.Runtime != target.Runtime,
		ProvidesAdded:      nonNilPorts(portDifference(target.Provides, current.Provides)),
		ProvidesRemoved:    nonNilPorts(portDifference(current.Provides, target.Provides)),
		RequiresAdded:      nonNilPorts(portDifference(target.Requires, current.Requires)),
		RequiresRemoved:    nonNilPorts(portDifference(current.Requires, target.Requires)),
		PermissionsAdded:   nonNilPermissions(permissionDifference(target.RequestedPermissions, current.RequestedPermissions)),
		PermissionsRemoved: nonNilPermissions(permissionDifference(current.RequestedPermissions, target.RequestedPermissions)),
	}
}

func normalizeDiff(input ManifestDiffV1) (ManifestDiffV1, error) {
	var err error
	input.ProvidesAdded, err = normalizePorts("diff provides_added", input.ProvidesAdded, false)
	if err != nil {
		return ManifestDiffV1{}, err
	}
	input.ProvidesRemoved, err = normalizePorts("diff provides_removed", input.ProvidesRemoved, false)
	if err != nil {
		return ManifestDiffV1{}, err
	}
	input.RequiresAdded, err = normalizePorts("diff requires_added", input.RequiresAdded, false)
	if err != nil {
		return ManifestDiffV1{}, err
	}
	input.RequiresRemoved, err = normalizePorts("diff requires_removed", input.RequiresRemoved, false)
	if err != nil {
		return ManifestDiffV1{}, err
	}
	for _, list := range []*[]moduleapi.Permission{&input.PermissionsAdded, &input.PermissionsRemoved} {
		for _, permission := range *list {
			if err := permission.Validate(); err != nil {
				return ManifestDiffV1{}, err
			}
		}
		sort.Slice(*list, func(i, j int) bool { return (*list)[i] < (*list)[j] })
		for i := 1; i < len(*list); i++ {
			if (*list)[i] == (*list)[i-1] {
				return ManifestDiffV1{}, errors.New("moduleupgrade: duplicate diff permission")
			}
		}
		*list = nonNilPermissions(*list)
	}
	input.ProvidesAdded = nonNilPorts(input.ProvidesAdded)
	input.ProvidesRemoved = nonNilPorts(input.ProvidesRemoved)
	input.RequiresAdded = nonNilPorts(input.RequiresAdded)
	input.RequiresRemoved = nonNilPorts(input.RequiresRemoved)
	return input, nil
}

func normalizeHandler(input HandlerAssessmentV1) (HandlerAssessmentV1, error) {
	switch input.Status {
	case HandlerSupportedV1, HandlerConflictV1:
		if err := validateOpaque("handler kind", input.Kind); err != nil {
			return HandlerAssessmentV1{}, err
		}
		if err := input.ExecutionClass.Validate(); err != nil {
			return HandlerAssessmentV1{}, err
		}
		if err := validateOpaque("handler adapter_identity", input.AdapterIdentity); err != nil {
			return HandlerAssessmentV1{}, err
		}
	case HandlerUnsupportedV1:
		if input.Kind != "" || input.ExecutionClass != "" || input.AdapterIdentity != "" {
			return HandlerAssessmentV1{}, errors.New("moduleupgrade: unsupported handler must not invent handler identity")
		}
	default:
		return HandlerAssessmentV1{}, fmt.Errorf("moduleupgrade: unsupported handler status %q", input.Status)
	}
	return input, nil
}

func normalizeImpacts(input []BindingImpactV1, currentInstance, targetInstance string) ([]BindingImpactV1, error) {
	if len(input) == 0 || len(input) > MaxReviewCollectionV1 {
		return nil, errors.New("moduleupgrade: binding impacts must be non-empty and bounded")
	}
	output := make([]BindingImpactV1, len(input))
	for i, impact := range input {
		if err := validateBindingTarget(impact.BindingTarget); err != nil {
			return nil, fmt.Errorf("moduleupgrade: impact %d: %w", i, err)
		}
		if err := impact.Port.Validate(); err != nil {
			return nil, fmt.Errorf("moduleupgrade: impact %d: %w", i, err)
		}
		if !isCurrentPort(impact.Port) {
			return nil, fmt.Errorf("moduleupgrade: impact %d port is not registered", i)
		}
		if err := validateTargetPort(impact.BindingTarget, impact.Port); err != nil {
			return nil, fmt.Errorf("moduleupgrade: impact %d: %w", i, err)
		}
		if impact.CurrentInstanceID != currentInstance || impact.TargetInstanceID != targetInstance {
			return nil, fmt.Errorf("moduleupgrade: impact %d instance closure differs", i)
		}
		if !moduleapi.ValidSHA256(impact.ConfigRef) || !moduleapi.ValidSHA256(impact.AuthorityCeilingRef) {
			return nil, fmt.Errorf("moduleupgrade: impact %d config/authority refs must be SHA-256", i)
		}
		if err := impact.FailurePolicy.Validate(); err != nil {
			return nil, fmt.Errorf("moduleupgrade: impact %d: %w", i, err)
		}
		if len(impact.StaticContextRefs) > MaxReviewCollectionV1 {
			return nil, errors.New("moduleupgrade: too many static context refs")
		}
		seen := map[string]struct{}{}
		impact.StaticContextRefs = append([]string(nil), impact.StaticContextRefs...)
		for _, ref := range impact.StaticContextRefs {
			if !moduleapi.ValidSHA256(ref) {
				return nil, errors.New("moduleupgrade: static context ref must be SHA-256")
			}
			if _, ok := seen[ref]; ok {
				return nil, errors.New("moduleupgrade: duplicate static context ref")
			}
			seen[ref] = struct{}{}
		}
		impact.StaticContextRefs = nonNilStrings(impact.StaticContextRefs)
		output[i] = impact
	}
	sort.Slice(output, func(i, j int) bool { return impactKey(output[i]) < impactKey(output[j]) })
	for i := 1; i < len(output); i++ {
		if impactKey(output[i]) == impactKey(output[i-1]) {
			return nil, errors.New("moduleupgrade: duplicate Binding impact")
		}
	}
	return output, nil
}

func normalizeGrants(input []RequiredGrantV1, targetDigest string) ([]RequiredGrantV1, error) {
	if len(input) > MaxReviewCollectionV1 {
		return nil, errors.New("moduleupgrade: too many required grants")
	}
	output := append([]RequiredGrantV1(nil), input...)
	for _, grant := range output {
		switch grant.Kind {
		case GrantLocalProcessArtifactV1, GrantTrustedInProcessArtifactV1, GrantRemoteActionArtifactV1, GrantWASMActionArtifactV1,
			GrantRemoteEndpointDigestV1, GrantRemoteCredentialDigestV1, GrantModelCredentialDigestV1:
		default:
			return nil, fmt.Errorf("moduleupgrade: unknown required grant kind %q", grant.Kind)
		}
		if !moduleapi.ValidSHA256(grant.ReferenceDigest) {
			return nil, errors.New("moduleupgrade: required grant reference must be SHA-256")
		}
		switch grant.Kind {
		case GrantLocalProcessArtifactV1, GrantTrustedInProcessArtifactV1, GrantRemoteActionArtifactV1, GrantWASMActionArtifactV1:
			if grant.ReferenceDigest != targetDigest {
				return nil, errors.New("moduleupgrade: artifact grant must reference target ArtifactDigest")
			}
		}
	}
	sort.Slice(output, func(i, j int) bool { return grantKey(output[i]) < grantKey(output[j]) })
	for i := 1; i < len(output); i++ {
		if output[i] == output[i-1] {
			return nil, errors.New("moduleupgrade: duplicate required grant")
		}
	}
	if output == nil {
		output = []RequiredGrantV1{}
	}
	return output, nil
}

func normalizeReasons(input []ReasonCodeV1) ([]ReasonCodeV1, error) {
	if len(input) > MaxReviewCollectionV1 {
		return nil, errors.New("moduleupgrade: too many reason codes")
	}
	output := append([]ReasonCodeV1(nil), input...)
	for _, reason := range output {
		switch reason {
		case ReasonHandlerUnsupportedV1, ReasonHandlerConflictV1, ReasonTargetPortRemovedV1,
			ReasonTargetRequirementsChangedV1, ReasonRequestedPermissionsExpandedV1,
			ReasonPublishedBindingConflictV1, ReasonTargetInstanceConflictV1,
			ReasonOperatorGrantRequiredV1, ReasonOperatorRequestedRollbackV1:
		default:
			return nil, fmt.Errorf("moduleupgrade: unknown reason code %q", reason)
		}
	}
	sort.Slice(output, func(i, j int) bool { return output[i] < output[j] })
	for i := 1; i < len(output); i++ {
		if output[i] == output[i-1] {
			return nil, errors.New("moduleupgrade: duplicate reason code")
		}
	}
	if output == nil {
		output = []ReasonCodeV1{}
	}
	return output, nil
}

func validateConclusion(conclusion ConclusionV1, handler HandlerAssessmentV1, diff ManifestDiffV1, target ManifestSummaryV1, impacts []BindingImpactV1, grants []RequiredGrantV1, reasons []ReasonCodeV1) error {
	has := func(want ReasonCodeV1) bool {
		for _, value := range reasons {
			if value == want {
				return true
			}
		}
		return false
	}
	if (len(grants) != 0) != has(ReasonOperatorGrantRequiredV1) {
		return errors.New("moduleupgrade: required grants and OPERATOR_GRANT_REQUIRED reason must agree")
	}
	portRemoved := false
	for _, impact := range impacts {
		if !containsPort(target.Provides, impact.Port) {
			portRemoved = true
			break
		}
	}
	if portRemoved != has(ReasonTargetPortRemovedV1) {
		return errors.New("moduleupgrade: removed impacted port and TARGET_PORT_REMOVED reason must agree")
	}
	requiresChanged := len(diff.RequiresAdded) != 0 || len(diff.RequiresRemoved) != 0
	if requiresChanged != has(ReasonTargetRequirementsChangedV1) {
		return errors.New("moduleupgrade: requirements diff and TARGET_REQUIREMENTS_CHANGED reason must agree")
	}
	permissionsExpanded := len(diff.PermissionsAdded) != 0
	if permissionsExpanded != has(ReasonRequestedPermissionsExpandedV1) {
		return errors.New("moduleupgrade: permission expansion and REQUESTED_PERMISSIONS_EXPANDED reason must agree")
	}
	if handler.Status == HandlerConflictV1 && !has(ReasonHandlerConflictV1) ||
		handler.Status != HandlerConflictV1 && has(ReasonHandlerConflictV1) {
		return errors.New("moduleupgrade: handler conflict status and reason must agree")
	}
	if handler.Status == HandlerUnsupportedV1 && !has(ReasonHandlerUnsupportedV1) ||
		handler.Status != HandlerUnsupportedV1 && has(ReasonHandlerUnsupportedV1) {
		return errors.New("moduleupgrade: handler unsupported status and reason must agree")
	}
	switch conclusion {
	case ConclusionWouldApplyV1:
		if handler.Status != HandlerSupportedV1 || portRemoved || requiresChanged || permissionsExpanded {
			return errors.New("moduleupgrade: WOULD_APPLY requires supported handler, retained impacted ports, stable requirements, and no permission expansion")
		}
		for _, blocking := range []ReasonCodeV1{
			ReasonPublishedBindingConflictV1,
			ReasonTargetInstanceConflictV1,
		} {
			if has(blocking) {
				return errors.New("moduleupgrade: WOULD_APPLY contains a blocking conflict reason")
			}
		}
	case ConclusionConflictV1:
		if len(reasons) == 0 || handler.Status == HandlerUnsupportedV1 {
			return errors.New("moduleupgrade: CONFLICT requires reasons and a non-unsupported handler")
		}
	case ConclusionUnsupportedV1:
		if handler.Status != HandlerUnsupportedV1 || !has(ReasonHandlerUnsupportedV1) {
			return errors.New("moduleupgrade: UNSUPPORTED requires unsupported handler reason")
		}
	default:
		return fmt.Errorf("moduleupgrade: unsupported conclusion %q", conclusion)
	}
	return nil
}

func validateSupplyBasis(input SupplyBasisV1) error {
	if err := validateOpaque("supply source_id", input.SourceID); err != nil {
		return err
	}
	if !moduleapi.ValidSHA256(input.SourcePolicyID) || !moduleapi.ValidSHA256(input.SnapshotID) || !moduleapi.ValidSHA256(input.IndexID) || input.SourcePolicyRevision == 0 || input.ObservationRevision == 0 {
		return errors.New("moduleupgrade: invalid SupplyBasis IDs or revisions")
	}
	switch input.SignatureStatus {
	case SignatureNotRequiredV1:
		if input.PublisherKeyID != "" || input.PublisherKeyRevision != 0 {
			return errors.New("moduleupgrade: unsigned basis cannot contain publisher key")
		}
	case SignatureVerifiedV1:
		if !moduleapi.ValidSHA256(input.PublisherKeyID) || input.PublisherKeyRevision == 0 {
			return errors.New("moduleupgrade: verified signature requires exact publisher key basis")
		}
	default:
		return fmt.Errorf("moduleupgrade: invalid signature status %q", input.SignatureStatus)
	}
	return nil
}

func validateSignatureClosure(supply SupplyBasisV1, signatureID string) error {
	if signatureID == "" && supply.SignatureStatus != SignatureNotRequiredV1 || signatureID != "" && supply.SignatureStatus != SignatureVerifiedV1 {
		return errors.New("moduleupgrade: target signature and SupplyBasis status differ")
	}
	return nil
}

func validateBindingTarget(input BindingTargetV1) error {
	switch input.Kind {
	case BindingTargetProfileV1:
		if input.WorkspaceID != "" || input.EndpointID != "" {
			return errors.New("moduleupgrade: PROFILE target must omit Workspace and Endpoint")
		}
		return validateOpaque("binding target profile_id", input.ProfileID)
	case BindingTargetWorkspaceChannelEndpointV1:
		if input.ProfileID != "" {
			return errors.New("moduleupgrade: WORKSPACE_CHANNEL_ENDPOINT target must omit Profile")
		}
		if err := validateOpaque("binding target workspace_id", input.WorkspaceID); err != nil {
			return err
		}
		return validateOpaque("binding target endpoint_id", input.EndpointID)
	default:
		return fmt.Errorf("moduleupgrade: unsupported binding target %q", input.Kind)
	}
}

func isCurrentPort(port moduleapi.PortRef) bool {
	for _, current := range moduleapi.S1PortRefs() {
		if port == current {
			return true
		}
	}
	return false
}

func validateTargetPort(target BindingTargetV1, port moduleapi.PortRef) error {
	channel := port.Name == moduleapi.PortNameChannelTransport &&
		port.ExactVersion == moduleapi.PortVersionV1
	switch target.Kind {
	case BindingTargetProfileV1:
		if channel {
			return errors.New("PROFILE target cannot own channel.transport/v1")
		}
	case BindingTargetWorkspaceChannelEndpointV1:
		if !channel {
			return errors.New("WORKSPACE_CHANNEL_ENDPOINT target requires channel.transport/v1")
		}
	}
	return nil
}

func runtimeExecutionClass(mode moduleapi.RuntimeModeRequest) moduleapi.ExecutionClass {
	switch mode {
	case moduleapi.RuntimeModeRequestDeclarative:
		return moduleapi.ExecutionDeclarative
	case moduleapi.RuntimeModeRequestTrustedInProcess:
		return moduleapi.ExecutionTrustedInProcess
	case moduleapi.RuntimeModeRequestLocalProcess:
		return moduleapi.ExecutionLocalProcess
	case moduleapi.RuntimeModeRequestRemote:
		return moduleapi.ExecutionRemote
	case moduleapi.RuntimeModeRequestWASM:
		return moduleapi.ExecutionWASM
	default:
		return ""
	}
}

func validateGrantHandlerClosure(grants []RequiredGrantV1, handler HandlerAssessmentV1) error {
	artifactKinds := map[RequiredGrantKindV1]moduleapi.ExecutionClass{
		GrantLocalProcessArtifactV1:     moduleapi.ExecutionLocalProcess,
		GrantTrustedInProcessArtifactV1: moduleapi.ExecutionTrustedInProcess,
		GrantRemoteActionArtifactV1:     moduleapi.ExecutionRemote,
		GrantWASMActionArtifactV1:       moduleapi.ExecutionWASM,
	}
	artifactCount := 0
	for _, grant := range grants {
		if class, artifact := artifactKinds[grant.Kind]; artifact {
			artifactCount++
			if handler.Status == HandlerUnsupportedV1 || handler.ExecutionClass != class {
				return errors.New("moduleupgrade: artifact grant and handler ExecutionClass differ")
			}
		}
		if grant.Kind == GrantRemoteEndpointDigestV1 || grant.Kind == GrantRemoteCredentialDigestV1 {
			if handler.Status == HandlerUnsupportedV1 || handler.ExecutionClass != moduleapi.ExecutionRemote {
				return errors.New("moduleupgrade: REMOTE parameter grant requires REMOTE handler")
			}
		}
		if grant.Kind == GrantModelCredentialDigestV1 &&
			(handler.Status == HandlerUnsupportedV1 || handler.ExecutionClass != moduleapi.ExecutionTrustedInProcess) {
			return errors.New("moduleupgrade: model SecretRef grant requires TRUSTED_IN_PROCESS handler")
		}
	}
	if artifactCount > 1 {
		return errors.New("moduleupgrade: artifact grant kinds are mutually exclusive")
	}
	return nil
}

func validateOpaque(name, value string) error {
	if value == "" || len(value) > moduleapi.MaxOpaqueIDBytes || !utf8.ValidString(value) || value != strings.TrimSpace(value) || value != moduleapi.CanonicalText(value) {
		return fmt.Errorf("moduleupgrade: %s must be nonblank canonical UTF-8 of at most %d bytes", name, moduleapi.MaxOpaqueIDBytes)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("moduleupgrade: %s contains a control character", name)
		}
	}
	return nil
}

func marshalCanonical(input any) ([]byte, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("moduleupgrade: encode review: %w", err)
	}
	if len(encoded) > MaxReviewCanonicalBytesV1 {
		return nil, fmt.Errorf("moduleupgrade: review exceeds %d bytes", MaxReviewCanonicalBytesV1)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(encoded, moduleapi.CanonicalJSONLimits{MaxBytes: MaxReviewCanonicalBytesV1, MaxDepth: 128, MaxNodes: MaxReviewCanonicalBytesV1})
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func decodeStrictCanonical(canonical []byte, maximum int, target any) error {
	if len(canonical) == 0 || len(canonical) > maximum {
		return fmt.Errorf("canonical value must contain 1-%d bytes", maximum)
	}
	checked, err := moduleapi.CanonicalJSONWithLimits(canonical, moduleapi.CanonicalJSONLimits{MaxBytes: maximum, MaxDepth: 128, MaxNodes: maximum})
	if err != nil || !bytes.Equal(checked, canonical) || canonical[0] != '{' {
		return errors.Join(err, errors.New("value is not an exact canonical JSON object"))
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("strict decode: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("strict decode has trailing data")
	}
	return nil
}

func cloneReview(input ReviewV1) ReviewV1 {
	input.Current.Manifest = cloneManifest(input.Current.Manifest)
	input.Target.Manifest = cloneManifest(input.Target.Manifest)
	input.Diff = cloneDiff(input.Diff)
	input.BindingImpacts = append([]BindingImpactV1(nil), input.BindingImpacts...)
	for i := range input.BindingImpacts {
		input.BindingImpacts[i].StaticContextRefs = append([]string(nil), input.BindingImpacts[i].StaticContextRefs...)
	}
	input.RequiredGrants = append([]RequiredGrantV1(nil), input.RequiredGrants...)
	input.ReasonCodes = append([]ReasonCodeV1(nil), input.ReasonCodes...)
	return input
}

func cloneManifest(input ManifestSummaryV1) ManifestSummaryV1 {
	input.Provides = append([]moduleapi.PortRef(nil), input.Provides...)
	input.Requires = append([]moduleapi.PortRef(nil), input.Requires...)
	input.RequestedPermissions = append([]moduleapi.Permission(nil), input.RequestedPermissions...)
	return input
}

func cloneDiff(input ManifestDiffV1) ManifestDiffV1 {
	input.ProvidesAdded = append([]moduleapi.PortRef(nil), input.ProvidesAdded...)
	input.ProvidesRemoved = append([]moduleapi.PortRef(nil), input.ProvidesRemoved...)
	input.RequiresAdded = append([]moduleapi.PortRef(nil), input.RequiresAdded...)
	input.RequiresRemoved = append([]moduleapi.PortRef(nil), input.RequiresRemoved...)
	input.PermissionsAdded = append([]moduleapi.Permission(nil), input.PermissionsAdded...)
	input.PermissionsRemoved = append([]moduleapi.Permission(nil), input.PermissionsRemoved...)
	return input
}

func portDifference(left, right []moduleapi.PortRef) []moduleapi.PortRef {
	set := map[moduleapi.PortRef]struct{}{}
	for _, v := range right {
		set[v] = struct{}{}
	}
	var out []moduleapi.PortRef
	for _, v := range left {
		if _, ok := set[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}
func permissionDifference(left, right []moduleapi.Permission) []moduleapi.Permission {
	set := map[moduleapi.Permission]struct{}{}
	for _, v := range right {
		set[v] = struct{}{}
	}
	var out []moduleapi.Permission
	for _, v := range left {
		if _, ok := set[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}
func containsPort(ports []moduleapi.PortRef, wanted moduleapi.PortRef) bool {
	for _, port := range ports {
		if port == wanted {
			return true
		}
	}
	return false
}
func containsSelectedImpact(values []BindingImpactV1, target BindingTargetV1, port moduleapi.PortRef, index uint32) bool {
	for _, value := range values {
		if value.BindingTarget == target && value.Port == port && value.PortBindingIndex == index {
			return true
		}
	}
	return false
}
func diffSupplied(input ManifestDiffV1) bool {
	return input.RuntimeChanged || len(input.ProvidesAdded)+len(input.ProvidesRemoved)+len(input.RequiresAdded)+len(input.RequiresRemoved)+len(input.PermissionsAdded)+len(input.PermissionsRemoved) != 0
}
func nonNilPorts(input []moduleapi.PortRef) []moduleapi.PortRef {
	if input == nil {
		return []moduleapi.PortRef{}
	}
	return input
}
func nonNilPermissions(input []moduleapi.Permission) []moduleapi.Permission {
	if input == nil {
		return []moduleapi.Permission{}
	}
	return input
}
func nonNilStrings(input []string) []string {
	if input == nil {
		return []string{}
	}
	return input
}
func portKey(input moduleapi.PortRef) string { return input.Name + "\x00" + input.ExactVersion }
func targetKey(input BindingTargetV1) string {
	return string(input.Kind) + "\x00" + input.ProfileID + "\x00" + input.WorkspaceID + "\x00" + input.EndpointID
}
func impactKey(input BindingImpactV1) string {
	return targetKey(input.BindingTarget) + "\x00" + portKey(input.Port) + fmt.Sprintf("\x00%010d", input.PortBindingIndex)
}
func grantKey(input RequiredGrantV1) string {
	return string(input.Kind) + "\x00" + input.ReferenceDigest
}
