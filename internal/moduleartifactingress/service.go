package moduleartifactingress

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/endview/freeagent/internal/moduleartifactstore"
	"github.com/endview/freeagent/internal/moduleconformance"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type FailureCodeV1 string

const (
	FailureInvalidRequestV1  FailureCodeV1 = "INVALID_REQUEST"
	FailureStoreReadV1       FailureCodeV1 = "STORE_READ_FAILED"
	FailureStoreBasisV1      FailureCodeV1 = "STORE_BASIS_INVALID"
	FailureSourceDeniedV1    FailureCodeV1 = "SOURCE_DENIED"
	FailureArtifactInvalidV1 FailureCodeV1 = "ARTIFACT_INVALID"
	FailurePublishV1         FailureCodeV1 = "PUBLISH_FAILED"
	FailureStoreCommitV1     FailureCodeV1 = "STORE_COMMIT_FAILED"
	FailureFinalVerifyV1     FailureCodeV1 = "FINAL_VERIFY_FAILED"
	FailureCancelledV1       FailureCodeV1 = "CANCELLED"
	FailureInternalV1        FailureCodeV1 = "INTERNAL_ERROR"
)

type FailureV1 struct {
	code  FailureCodeV1
	cause error
}

func (failure *FailureV1) Error() string {
	if failure == nil {
		return "module artifact ingress: INTERNAL_ERROR"
	}
	return "module artifact ingress: " + string(failure.code)
}

func (failure *FailureV1) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func FailureCodeOfV1(err error) FailureCodeV1 {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return FailureCancelledV1
	}
	var failure *FailureV1
	if errors.As(err, &failure) && failure != nil {
		switch failure.code {
		case FailureInvalidRequestV1, FailureStoreReadV1, FailureStoreBasisV1,
			FailureSourceDeniedV1, FailureArtifactInvalidV1, FailurePublishV1,
			FailureStoreCommitV1, FailureFinalVerifyV1, FailureCancelledV1,
			FailureInternalV1:
			return failure.code
		}
	}
	return FailureInternalV1
}

// SelectionV1 contains only Store selectors. PackagePath is deliberately
// absent: the Store-owned discovery Entry supplies it.
type SelectionV1 struct {
	SourceID       string
	SnapshotID     string
	Module         moduleapi.Ref
	ArtifactDigest string
}

// BasisViewV1 is the detached, minimum view of a Store-owned selection needed
// for filesystem ingress. The opaque Store basis remains owned by the caller's
// generic Read/Commit callbacks.
type BasisViewV1 struct {
	SourceID                    string
	SourcePolicyID              string
	SourcePolicyCanonical       []byte
	SourcePolicyRevision        uint64
	SnapshotID                  string
	SnapshotSourcePolicyID      string
	SnapshotObservationRevision uint64
	EntryOrdinal                uint32
	Entry                       moduleapi.ModuleDiscoveryEntryV1
	ArtifactInstalled           bool
}

// ExistingViewV1 is the minimum physical closure needed to return an exact
// already-admitted selector without consulting SourceRoot or the live source
// head.
type ExistingViewV1 struct {
	Module            moduleapi.Ref
	ArtifactDigest    string
	ArtifactSizeBytes uint64
	CoveredFileCount  uint64
	Installed         bool
}

type RequestV1 struct {
	SourceRoot   string
	ArtifactRoot string
	Selection    SelectionV1
}

// StoreCallbacksV1 allows Current Store to inject one already-open Store
// without creating an import cycle. Read returns the opaque exact basis and a
// detached view; Commit receives that same basis after durable publication.
type StoreCallbacksV1[Basis, Artifact, Admission any] struct {
	Existing func(context.Context, SelectionV1) (Artifact, Admission, ExistingViewV1, bool, error)
	Read     func(context.Context, SelectionV1) (Basis, BasisViewV1, error)
	Commit   func(context.Context, Basis, []byte, uint64) (Artifact, Admission, error)
}

type ResultV1[Artifact, Admission any] struct {
	Artifact  Artifact
	Admission Admission
	Reused    bool
}

// IngressV1 is filesystem-first and Store-second. It never opens a Store,
// network connection, runtime Host, installer, activation path, or tenant
// authority. A publication orphan after a failed Store commit is inert and may
// be reused only after a later current eligible selection fully verifies the
// same digest. A historical selector is recoverable only when its exact
// Admission was already durably committed.
func IngressV1[Basis, Artifact, Admission any](
	ctx context.Context,
	request RequestV1,
	store StoreCallbacksV1[Basis, Artifact, Admission],
) (ResultV1[Artifact, Admission], error) {
	var zero ResultV1[Artifact, Admission]
	if ctx == nil || store.Existing == nil || store.Read == nil || store.Commit == nil {
		return zero, failV1(FailureInvalidRequestV1, errors.New("nil ingress dependency"))
	}
	if err := ctx.Err(); err != nil {
		return zero, failV1(FailureCancelledV1, err)
	}
	if err := validateSelectionV1(request.Selection); err != nil {
		return zero, failV1(FailureInvalidRequestV1, err)
	}
	artifactRoot, err := moduleartifactstore.SelectArtifactRootV1(request.ArtifactRoot)
	if err != nil {
		return zero, failV1(FailureInvalidRequestV1, err)
	}
	existingArtifact, existingAdmission, existingView, found, err := store.Existing(ctx, request.Selection)
	if err != nil {
		return zero, failV1(classifyCancellationV1(FailureStoreReadV1, err), err)
	}
	if found {
		if err := validateExistingViewV1(request.Selection, existingView); err != nil {
			return zero, failV1(FailureStoreBasisV1, errors.New("existing admission closure differs from selection"))
		}
		if err := moduleartifactstore.VerifyExistingRootV1(
			ctx, artifactRoot, existingView.ArtifactDigest,
			existingView.ArtifactSizeBytes, existingView.CoveredFileCount,
			existingModePolicyV1(existingView.Installed),
		); err != nil {
			return zero, failV1(classifyCancellationV1(FailureFinalVerifyV1, err), err)
		}
		return ResultV1[Artifact, Admission]{
			Artifact: existingArtifact, Admission: existingAdmission, Reused: true,
		}, nil
	}
	basis, view, err := store.Read(ctx, request.Selection)
	if err != nil {
		return zero, failV1(classifyCancellationV1(FailureStoreReadV1, err), err)
	}
	policy, err := validateBasisViewV1(request.Selection, view)
	if err != nil {
		return zero, failV1(FailureStoreBasisV1, err)
	}
	if policy.Kind != moduleapi.ModuleSourceKindLocalDirectoryV1 ||
		policy.Network != moduleapi.ModuleSourceNetworkDenyV1 ||
		policy.SignatureRequired || view.Entry.SignatureID != "" {
		return zero, failV1(FailureSourceDeniedV1, errors.New("source policy is outside local unsigned ingress boundary"))
	}

	selected, err := moduleartifactstore.SelectSourcePackageAtRootV1(
		request.SourceRoot, artifactRoot, view.Entry.PackagePath,
	)
	if err != nil {
		return zero, failV1(FailureSourceDeniedV1, err)
	}
	report, err := moduleconformance.VerifySourceCandidateDirectory(ctx, moduleconformance.SourceCandidateInput{
		ArtifactRoot:          selected.PackageRoot(),
		SourceRoot:            selected.SourceRoot(),
		SourcePolicyID:        view.SourcePolicyID,
		SourcePolicyCanonical: bytes.Clone(view.SourcePolicyCanonical),
	})
	if err != nil {
		code := FailureArtifactInvalidV1
		switch moduleconformance.FailureCodeOf(err) {
		case moduleconformance.FailureSourceInput, moduleconformance.FailureSourceDenied,
			moduleconformance.FailureSignatureNeeded, moduleconformance.FailureSignatureInvalid,
			moduleconformance.FailurePublisherRevoked:
			code = FailureSourceDeniedV1
		case moduleconformance.FailureCancelled:
			code = FailureCancelledV1
		}
		return zero, failV1(code, err)
	}
	if report.Module.ID != view.Entry.Module.ID ||
		report.Module.ExactVersion != view.Entry.Module.Version ||
		report.ArtifactDigest != view.Entry.ArtifactDigest ||
		report.ArtifactSizeBytes != view.Entry.ArtifactSizeBytes ||
		report.CoveredFileCount == 0 ||
		report.CoveredFileCount > uint64(moduleapi.DefaultArtifactMaxFiles) {
		return zero, failV1(FailureArtifactInvalidV1, errors.New("verified artifact differs from Store entry"))
	}

	published, err := moduleartifactstore.PublishV1(ctx, moduleartifactstore.PublishRequestV1{
		Source:             selected,
		ArtifactDigest:     view.Entry.ArtifactDigest,
		ArtifactSizeBytes:  view.Entry.ArtifactSizeBytes,
		MaxPackageBytes:    policy.MaxPackageBytes,
		ExistingModePolicy: existingModePolicyV1(view.ArtifactInstalled),
	})
	if err != nil {
		return zero, failV1(classifyCancellationV1(FailurePublishV1, err), err)
	}
	if published.CoveredFileCount != report.CoveredFileCount {
		return zero, failV1(FailureArtifactInvalidV1, errors.New("captured file count differs from verified report"))
	}
	artifact, admission, err := store.Commit(
		ctx, basis, bytes.Clone(published.ManifestCanonical), published.CoveredFileCount,
	)
	if err != nil {
		// Commit errors can be response-ambiguous. Query the exact selector on a
		// bounded recovery context. A found admission wins and is physically
		// reverified. Otherwise the inert publication is deliberately retained:
		// digest equality cannot prove this invocation still owns the path, so
		// synchronous deletion could remove a concurrently referenced object.
		// ArtifactRoot's fail-closed aggregate physical quota bounds retained
		// publications and makes later exact adoption safe.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		recoveredArtifact, recoveredAdmission, recoveredView, recovered, recoveryErr := store.Existing(
			cleanupCtx, request.Selection,
		)
		if recoveryErr != nil {
			return zero, failV1(classifyCancellationV1(FailureStoreCommitV1, err), err)
		}
		if recovered {
			if validateErr := validateExistingViewV1(request.Selection, recoveredView); validateErr != nil {
				return zero, failV1(FailureFinalVerifyV1, validateErr)
			}
			if verifyErr := moduleartifactstore.VerifyExistingRootV1(
				cleanupCtx, artifactRoot, recoveredView.ArtifactDigest,
				recoveredView.ArtifactSizeBytes, recoveredView.CoveredFileCount,
				existingModePolicyV1(recoveredView.Installed),
			); verifyErr != nil {
				return zero, failV1(FailureFinalVerifyV1, verifyErr)
			}
			return ResultV1[Artifact, Admission]{
				Artifact: recoveredArtifact, Admission: recoveredAdmission, Reused: published.Reused,
			}, nil
		}
		return zero, failV1(classifyCancellationV1(FailureStoreCommitV1, err), err)
	}
	if err := moduleartifactstore.VerifyExistingRootV1(
		ctx, artifactRoot, view.Entry.ArtifactDigest, view.Entry.ArtifactSizeBytes,
		published.CoveredFileCount,
		existingModePolicyV1(view.ArtifactInstalled),
	); err != nil {
		return zero, failV1(classifyCancellationV1(FailureFinalVerifyV1, err), err)
	}
	return ResultV1[Artifact, Admission]{
		Artifact: artifact, Admission: admission, Reused: published.Reused,
	}, nil
}

func existingModePolicyV1(installed bool) moduleartifactstore.ExistingModePolicyV1 {
	if installed {
		return moduleartifactstore.ExistingModeStoreProvenInstalledV1
	}
	// The physical CAS can be shared by independently fenced Current Stores.
	// A non-installed Store grants no authority, but it may safely reuse either
	// an all-0600 tree or the one exact descriptor-owned executable enabled by
	// another Store's installation.
	return moduleartifactstore.ExistingModeRootCompatibleV1
}

func validateExistingViewV1(selection SelectionV1, view ExistingViewV1) error {
	if view.Module != selection.Module ||
		view.ArtifactDigest != selection.ArtifactDigest ||
		view.ArtifactSizeBytes == 0 ||
		view.ArtifactSizeBytes > moduleapi.MaxModuleSourcePackageBytesV1 ||
		view.CoveredFileCount == 0 ||
		view.CoveredFileCount > uint64(moduleapi.DefaultArtifactMaxFiles) {
		return errors.New("existing admission closure differs from selection")
	}
	return nil
}

func validateSelectionV1(selection SelectionV1) error {
	if !validDottedIdentifierV1(selection.SourceID) ||
		!moduleapi.ValidSHA256(selection.SnapshotID) ||
		!moduleapi.ValidSHA256(selection.ArtifactDigest) {
		return errors.New("selection identity is invalid")
	}
	return selection.Module.Validate()
}

func validateBasisViewV1(selection SelectionV1, view BasisViewV1) (moduleapi.ModuleSourcePolicyV1, error) {
	if view.SourceID != selection.SourceID || view.SnapshotID != selection.SnapshotID ||
		view.Entry.Module != selection.Module || view.Entry.ArtifactDigest != selection.ArtifactDigest ||
		view.SourcePolicyRevision == 0 || view.SourcePolicyRevision > maxJSONSafeIntegerV1 ||
		view.SnapshotObservationRevision == 0 || view.SnapshotObservationRevision > maxJSONSafeIntegerV1 ||
		view.SnapshotSourcePolicyID != view.SourcePolicyID {
		return moduleapi.ModuleSourcePolicyV1{}, errors.New("basis selection closure differs")
	}
	policy, canonical, policyID, err := moduleapi.ParseModuleSourcePolicyV1(bytes.Clone(view.SourcePolicyCanonical))
	if err != nil || policyID != view.SourcePolicyID || !bytes.Equal(canonical, view.SourcePolicyCanonical) ||
		policy.SourceID != view.SourceID {
		return moduleapi.ModuleSourcePolicyV1{}, errors.New("basis source policy closure is invalid")
	}
	path, err := moduleapi.NormalizeArtifactPath(view.Entry.PackagePath)
	if err != nil || path != view.Entry.PackagePath ||
		view.Entry.ArtifactSizeBytes == 0 ||
		view.Entry.ArtifactSizeBytes > policy.MaxPackageBytes {
		return moduleapi.ModuleSourcePolicyV1{}, errors.New("basis discovery entry is invalid")
	}
	if err := moduleapi.ValidateModuleSourceCandidateV1(
		policy, view.Entry.Module, view.Entry.ArtifactSizeBytes, view.Entry.SignatureID != "",
	); err != nil {
		return moduleapi.ModuleSourcePolicyV1{}, errors.New("basis discovery entry is denied by policy")
	}
	return policy, nil
}

func failV1(code FailureCodeV1, cause error) error {
	return &FailureV1{code: code, cause: cause}
}

func classifyCancellationV1(fallback FailureCodeV1, err error) FailureCodeV1 {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return FailureCancelledV1
	}
	return fallback
}
