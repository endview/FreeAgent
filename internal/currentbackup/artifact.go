package currentbackup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/endview/freeagent/internal/currentstore"
	"github.com/endview/freeagent/internal/mcpstdio"
	"github.com/endview/freeagent/sdk/moduleapi"
)

type verifiedArtifact struct {
	digest            string
	sizeBytes         int64
	manifest          moduleapi.ModuleManifestV1
	canonicalManifest []byte
	tree              safeTree
}

func verifyArtifactDirectory(
	root string,
	expectedDigest string,
) (verifiedArtifact, error) {
	return verifyArtifactDirectoryContext(
		context.Background(),
		root,
		expectedDigest,
	)
}

func verifyArtifactDirectoryContext(
	ctx context.Context,
	root string,
	expectedDigest string,
) (verifiedArtifact, error) {
	if ctx == nil {
		return verifiedArtifact{}, fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	if !moduleapi.ValidSHA256(expectedDigest) {
		return verifiedArtifact{}, fmt.Errorf("%w: invalid artifact digest", ErrIntegrity)
	}
	directory, err := resolveExistingDirectory(root, "artifact directory")
	if err != nil {
		return verifiedArtifact{}, fmt.Errorf("%w: %v", ErrIntegrity, err)
	}
	tree, captured, err := walkSafeTreeWithPlan(
		ctx,
		directory,
		false,
		safeTreeAccessHooks{},
		safeTreeReadPlan{capture: map[string]int64{
			moduleapi.ArtifactManifestPath: int64(moduleapi.MaxTextBytes),
		}},
	)
	if err != nil {
		return verifiedArtifact{}, err
	}
	manifestIdentity, ok := tree.files[moduleapi.ArtifactManifestPath]
	if !ok {
		return verifiedArtifact{}, fmt.Errorf(
			"%w: artifact %s has no module.yaml",
			ErrIntegrity,
			expectedDigest,
		)
	}
	var total int64
	for path, identity := range tree.files {
		if identity.SizeBytes < 0 || identity.SizeBytes > maxArtifactFileBytes ||
			identity.SizeBytes > maxArtifactBytes-total {
			return verifiedArtifact{}, fmt.Errorf(
				"%w: artifact %s size limit at %s",
				ErrIntegrity,
				expectedDigest,
				path,
			)
		}
		total += identity.SizeBytes
	}
	if total == 0 || len(tree.files) > maxArtifactFiles {
		return verifiedArtifact{}, fmt.Errorf("%w: invalid artifact size", ErrIntegrity)
	}
	manifestBytes := captured[moduleapi.ArtifactManifestPath]
	manifestSum := sha256.Sum256(manifestBytes)
	if int64(len(manifestBytes)) != manifestIdentity.SizeBytes ||
		hex.EncodeToString(manifestSum[:]) != manifestIdentity.SHA256 {
		return verifiedArtifact{}, fmt.Errorf(
			"%w: artifact manifest changed after tree verification",
			ErrIntegrity,
		)
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(manifestBytes)
	if err != nil || !bytes.Equal(canonical, manifestBytes) {
		return verifiedArtifact{}, fmt.Errorf("%w: parse artifact manifest: %v", ErrIntegrity, err)
	}
	files, err := moduleapi.ScanArtifactDirectoryContext(
		ctx,
		directory,
		moduleapi.ArtifactMetadataPaths{},
	)
	if err != nil {
		return verifiedArtifact{}, classifyArtifactScanError(err)
	}
	if len(files)+1 != len(tree.files) {
		return verifiedArtifact{}, fmt.Errorf(
			"%w: artifact tree changed during digest scan",
			ErrIntegrity,
		)
	}
	for _, file := range files {
		identity, ok := tree.files[file.Path]
		sum := sha256.Sum256(file.Content)
		if !ok || int64(len(file.Content)) != identity.SizeBytes ||
			hex.EncodeToString(sum[:]) != identity.SHA256 {
			return verifiedArtifact{}, fmt.Errorf(
				"%w: artifact file %s changed during digest scan",
				ErrIntegrity,
				file.Path,
			)
		}
	}
	digest, err := moduleapi.ComputeArtifactDigest(canonical, files)
	if err != nil {
		return verifiedArtifact{}, fmt.Errorf(
			"%w: compute artifact digest: %w",
			ErrIntegrity,
			err,
		)
	}
	if digest != expectedDigest {
		return verifiedArtifact{}, fmt.Errorf(
			"%w: artifact digest is %s, want %s",
			ErrIntegrity,
			digest,
			expectedDigest,
		)
	}
	return verifiedArtifact{
		digest:            digest,
		sizeBytes:         total,
		manifest:          manifest,
		canonicalManifest: bytes.Clone(canonical),
		tree:              tree,
	}, nil
}

func verifyRootedBundleArtifactContext(
	ctx context.Context,
	bundleTree safeTree,
	item Artifact,
	manifestBytes []byte,
) (verifiedArtifact, error) {
	if ctx == nil {
		return verifiedArtifact{}, fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	if bundleTree.rootInfo == nil {
		return verifiedArtifact{}, fmt.Errorf("%w: bundle root identity changed", ErrIntegrity)
	}
	prefix, err := moduleapi.NormalizeArtifactPath(item.Path)
	if err != nil || prefix != item.Path {
		return verifiedArtifact{}, fmt.Errorf("%w: artifact path is invalid", ErrInvalidBundle)
	}
	if _, ok := bundleTree.directories[prefix]; !ok {
		return verifiedArtifact{}, fmt.Errorf("%w: artifact directory is absent", ErrInvalidBundle)
	}
	prefix += "/"
	manifestPath := prefix + moduleapi.ArtifactManifestPath
	manifestIdentity, ok := bundleTree.files[manifestPath]
	if !ok || manifestIdentity.SizeBytes <= 0 ||
		manifestIdentity.SizeBytes > int64(moduleapi.MaxTextBytes) {
		return verifiedArtifact{}, fmt.Errorf("%w: artifact manifest is absent", ErrIntegrity)
	}
	manifestSum := sha256.Sum256(manifestBytes)
	if int64(len(manifestBytes)) != manifestIdentity.SizeBytes ||
		hex.EncodeToString(manifestSum[:]) != manifestIdentity.SHA256 {
		return verifiedArtifact{}, fmt.Errorf(
			"%w: artifact manifest changed after bundle enumeration",
			ErrIntegrity,
		)
	}
	manifest, canonical, err := moduleapi.ParseModuleManifestV1(manifestBytes)
	if err != nil || !bytes.Equal(canonical, manifestBytes) {
		return verifiedArtifact{}, fmt.Errorf("%w: parse artifact manifest: %v", ErrIntegrity, err)
	}

	tree := safeTree{
		files:       make(map[string]fileIdentity),
		directories: make(map[string]struct{}),
	}
	for path := range bundleTree.directories {
		if strings.HasPrefix(path, prefix) {
			tree.directories[strings.TrimPrefix(path, prefix)] = struct{}{}
		}
	}
	totalLimit := maxArtifactBytes
	if moduleapi.DefaultArtifactMaxTotalBytes < totalLimit {
		totalLimit = moduleapi.DefaultArtifactMaxTotalBytes
	}
	var total int64
	digests := make([]moduleapi.ArtifactFileDigest, 0)
	for path, identity := range bundleTree.files {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		relative := strings.TrimPrefix(path, prefix)
		if relative == "" || identity.SizeBytes < 0 ||
			identity.SizeBytes > maxArtifactFileBytes ||
			identity.SizeBytes > totalLimit-total {
			return verifiedArtifact{}, fmt.Errorf(
				"%w: artifact %s size limit at %s",
				ErrIntegrity,
				item.Digest,
				relative,
			)
		}
		total += identity.SizeBytes
		tree.files[relative] = identity
		if relative != moduleapi.ArtifactManifestPath {
			digests = append(digests, moduleapi.ArtifactFileDigest{
				Path:   relative,
				SHA256: identity.SHA256,
			})
		}
	}
	if total == 0 || len(tree.files) > moduleapi.DefaultArtifactMaxFiles ||
		len(tree.files)+len(tree.directories) > moduleapi.DefaultArtifactMaxPaths {
		return verifiedArtifact{}, fmt.Errorf("%w: invalid artifact size", ErrIntegrity)
	}
	digest, err := moduleapi.ComputeArtifactDigestFromFileDigests(canonical, digests)
	if err != nil || digest != item.Digest {
		return verifiedArtifact{}, fmt.Errorf(
			"%w: artifact digest differs: %v",
			ErrIntegrity,
			err,
		)
	}
	if total != item.SizeBytes {
		return verifiedArtifact{}, fmt.Errorf(
			"%w: artifact %s size differs",
			ErrIntegrity,
			item.Digest,
		)
	}
	return verifiedArtifact{
		digest:            digest,
		sizeBytes:         total,
		manifest:          manifest,
		canonicalManifest: bytes.Clone(canonical),
		tree:              tree,
	}, nil
}

func classifyArtifactScanError(err error) error {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf(
		"%w: scan artifact for digest: %w",
		ErrIntegrity,
		err,
	)
}

func classifyArtifactManifestReadError(err error) error {
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: read artifact manifest: %v", ErrIntegrity, err)
}

func copyVerifiedArtifact(
	ctx context.Context,
	sourceRoot string,
	destinationRoot string,
	artifact verifiedArtifact,
) (returnErr error) {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Mkdir(destinationRoot, 0o700); err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			returnErr = errors.Join(
				returnErr,
				removePrivateStagingTree(destinationRoot),
			)
		}
	}()
	if err := prepareVerifiedArtifactDestination(destinationRoot, artifact); err != nil {
		return err
	}
	copyPlan := make(map[string]safeTreeCopyTarget, len(artifact.tree.files))
	for path, identity := range artifact.tree.files {
		copyPlan[path] = safeTreeCopyTarget{
			destination: filepath.Join(destinationRoot, filepath.FromSlash(path)),
			expected:    identity,
		}
	}
	copiedTree, _, err := walkSafeTreeWithPlan(
		ctx,
		sourceRoot,
		false,
		safeTreeAccessHooks{},
		safeTreeReadPlan{copies: copyPlan},
	)
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%w: copy verified artifact source: %w", ErrIntegrity, err)
	}
	if !sameSafeTree(artifact.tree, copiedTree) {
		return fmt.Errorf("%w: verified artifact source changed before copy", ErrIntegrity)
	}
	if err := verifyCopiedArtifactDestination(ctx, destinationRoot, artifact); err != nil {
		return err
	}
	keep = true
	return nil
}

func prepareVerifiedArtifactDestination(
	destinationRoot string,
	artifact verifiedArtifact,
) error {
	directories := make([]string, 0, len(artifact.tree.directories))
	for directory := range artifact.tree.directories {
		directories = append(directories, directory)
	}
	sort.Slice(directories, func(left, right int) bool {
		return len(directories[left]) < len(directories[right]) ||
			(len(directories[left]) == len(directories[right]) &&
				directories[left] < directories[right])
	})
	for _, directory := range directories {
		if err := ensurePrivateDirectory(filepath.Join(
			destinationRoot,
			filepath.FromSlash(directory),
		)); err != nil {
			return err
		}
	}
	return nil
}

func verifyCopiedArtifactDestination(
	ctx context.Context,
	destinationRoot string,
	artifact verifiedArtifact,
) error {
	verified, err := verifyArtifactDirectoryContext(
		ctx,
		destinationRoot,
		artifact.digest,
	)
	if err != nil {
		return err
	}
	if verified.sizeBytes != artifact.sizeBytes ||
		verified.manifest.ID != artifact.manifest.ID ||
		verified.manifest.Version != artifact.manifest.Version ||
		!bytes.Equal(verified.canonicalManifest, artifact.canonicalManifest) {
		return fmt.Errorf("%w: copied artifact identity changed", ErrIntegrity)
	}
	if err := syncTreeDirectories(destinationRoot); err != nil {
		return err
	}
	return nil
}

// normalizeRestoredArtifactModes rebuilds the only portable permission
// contract carried by a Current Backup: private directories and ordinary
// files, plus—only for an artifact rooted by an Installation—the one exact
// executable named by an already digest-verified LOCAL_PROCESS MCP descriptor.
// An ingress-only Artifact remains inert and all of its files stay 0600 even
// when its Manifest describes LOCAL_PROCESS. Artifact digests intentionally
// exclude file modes, so copying bundle bytes as private 0600 files cannot
// preserve an executable bit.
//
// Source modes are never propagated. In particular, setuid, setgid, sticky,
// group and world bits cannot cross a restore. This function only mutates the
// unpublished private staging tree and performs no process or protocol I/O.
func normalizeRestoredArtifactModes(
	ctx context.Context,
	artifactDirectory string,
	artifact verifiedArtifact,
	installed bool,
) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrInvalidInput)
	}
	var executable string
	var descriptorCanonical []byte
	if installed {
		var err error
		executable, descriptorCanonical, err = restoredLocalMCPExecutable(
			ctx,
			artifactDirectory,
			artifact,
		)
		if err != nil {
			return err
		}
	}

	directories := make([]string, 0, len(artifact.tree.directories)+1)
	directories = append(directories, artifactDirectory)
	for directory := range artifact.tree.directories {
		directories = append(directories, filepath.Join(
			artifactDirectory,
			filepath.FromSlash(directory),
		))
	}
	sort.Slice(directories, func(left, right int) bool {
		return len(directories[left]) < len(directories[right]) ||
			(len(directories[left]) == len(directories[right]) &&
				directories[left] < directories[right])
	})
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(directory)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf(
				"%w: restored artifact directory changed before mode normalization",
				ErrIntegrity,
			)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("currentbackup: make restored artifact directory private: %w", err)
		}
	}

	files := make([]string, 0, len(artifact.tree.files))
	for path := range artifact.tree.files {
		files = append(files, path)
	}
	sort.Strings(files)
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if path == executable {
			mode = 0o700
		}
		if err := chmodAndSyncRestoredArtifactFile(
			ctx,
			filepath.Join(artifactDirectory, filepath.FromSlash(path)),
			artifact.tree.files[path],
			mode,
		); err != nil {
			return err
		}
	}
	if executable != "" {
		provider := moduleapi.ActivatedModuleRef{
			ModuleID:           artifact.manifest.ID,
			Version:            artifact.manifest.Version,
			ArtifactDigest:     artifact.digest,
			InstanceID:         "currentbackup-local-mcp-mode-check",
			ExecutionClass:     moduleapi.ExecutionLocalProcess,
			AdapterIdentity:    mcpstdio.AdapterIdentityV1,
			ActivationRevision: 1,
		}
		if _, err := mcpstdio.NewContext(
			ctx,
			provider,
			artifactDirectory,
			artifact.manifest.Runtime.Entrypoint,
			descriptorCanonical,
		); err != nil {
			return fmt.Errorf(
				"%w: restored LOCAL_PROCESS MCP host does not close: %v",
				ErrIntegrity,
				err,
			)
		}
	}
	return nil
}

// restoredLocalMCPExecutable extracts only the permission-relevant field
// from the exact descriptor selected by the canonical manifest. Installation,
// activation and adapter admission remain the authority for all other MCP
// semantics. The descriptor and executable identities are nevertheless
// checked against the already verified artifact tree before chmod is allowed.
func restoredLocalMCPExecutable(
	ctx context.Context,
	artifactDirectory string,
	artifact verifiedArtifact,
) (string, []byte, error) {
	if artifact.manifest.Runtime.Mode != moduleapi.RuntimeModeRequestLocalProcess {
		return "", nil, nil
	}
	if artifact.manifest.Runtime.Protocol != moduleapi.RuntimeProtocolMCPStdio20251125 {
		return "", nil, fmt.Errorf(
			"%w: LOCAL_PROCESS artifact has an unsupported protocol",
			ErrIntegrity,
		)
	}
	descriptorCanonical, err := moduleapi.ReadArtifactOrdinaryFileFromDirectoryContext(
		ctx,
		artifactDirectory,
		artifact.manifest.Runtime.Entrypoint,
		mcpstdio.MaxHostDescriptorBytesV1,
	)
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return "", nil, err
		}
		return "", nil, fmt.Errorf("%w: read LOCAL_PROCESS MCP descriptor: %v", ErrIntegrity, err)
	}
	canonical, err := moduleapi.CanonicalJSONWithLimits(
		descriptorCanonical,
		moduleapi.CanonicalJSONLimits{
			MaxBytes: mcpstdio.MaxHostDescriptorBytesV1,
			MaxDepth: 32,
			MaxNodes: 4096,
		},
	)
	if err != nil || !bytes.Equal(canonical, descriptorCanonical) ||
		len(canonical) == 0 || canonical[0] != '{' {
		return "", nil, fmt.Errorf(
			"%w: LOCAL_PROCESS MCP descriptor is not exact canonical JSON",
			ErrIntegrity,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(descriptorCanonical))
	decoder.DisallowUnknownFields()
	var descriptor mcpstdio.HostDescriptorV1
	if err := decoder.Decode(&descriptor); err != nil {
		return "", nil, fmt.Errorf("%w: decode LOCAL_PROCESS MCP descriptor: %v", ErrIntegrity, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", nil, fmt.Errorf(
			"%w: LOCAL_PROCESS MCP descriptor has trailing JSON",
			ErrIntegrity,
		)
	}
	if descriptor.SchemaVersion != mcpstdio.HostDescriptorSchemaV1 ||
		descriptor.ProtocolVersion != mcpstdio.ProtocolVersionV1 {
		return "", nil, fmt.Errorf(
			"%w: LOCAL_PROCESS MCP descriptor has an unsupported schema or protocol",
			ErrIntegrity,
		)
	}
	executable, err := moduleapi.NormalizeArtifactPath(descriptor.Executable)
	if err != nil || executable != descriptor.Executable ||
		!strings.HasPrefix(executable, "content/") {
		return "", nil, fmt.Errorf(
			"%w: LOCAL_PROCESS MCP executable path is invalid",
			ErrIntegrity,
		)
	}
	identity, found := artifact.tree.files[executable]
	if !found || !moduleapi.ValidSHA256(descriptor.ExecutableSHA256) ||
		identity.SHA256 != descriptor.ExecutableSHA256 {
		return "", nil, fmt.Errorf(
			"%w: LOCAL_PROCESS MCP executable identity differs from descriptor",
			ErrIntegrity,
		)
	}
	return executable, bytes.Clone(descriptorCanonical), nil
}

func chmodAndSyncRestoredArtifactFile(
	ctx context.Context,
	path string,
	expected fileIdentity,
	mode os.FileMode,
) (returnErr error) {
	before, err := os.Lstat(path)
	if err != nil || before.Mode()&os.ModeSymlink != 0 ||
		!before.Mode().IsRegular() || before.Size() != expected.SizeBytes {
		return fmt.Errorf(
			"%w: restored artifact file changed before mode normalization",
			ErrIntegrity,
		)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return fmt.Errorf(
			"%w: restored artifact file changed while opening for mode normalization",
			ErrIntegrity,
		)
	}
	links, err := openedFileLinkCount(file)
	if err != nil {
		return err
	}
	if links != 1 {
		return fmt.Errorf(
			"%w: hard-linked restored artifact file is forbidden",
			ErrIntegrity,
		)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("currentbackup: normalize restored artifact file mode: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("currentbackup: sync restored artifact file mode: %w", err)
	}
	if err := verifyStableOpenedFile(file, before); err != nil {
		return err
	}
	return ctx.Err()
}

func verifyArtifactClosure(
	installations []installationRef,
	moduleArtifacts []moduleArtifactRef,
	artifacts map[string]verifiedArtifact,
) error {
	for _, installation := range installations {
		artifact, ok := artifacts[installation.ArtifactDigest]
		if !ok {
			return fmt.Errorf(
				"%w: installation %s artifact %s is absent",
				ErrIntegrity,
				installation.InstallationID,
				installation.ArtifactDigest,
			)
		}
		if artifact.manifest.ID != installation.ModuleID ||
			artifact.manifest.Version != installation.ExactVersion ||
			!bytes.Equal(artifact.canonicalManifest, installation.ManifestBytes) {
			return fmt.Errorf(
				"%w: installation %s does not match artifact manifest",
				ErrIntegrity,
				installation.InstallationID,
			)
		}
		manifestRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentModuleManifest,
			"application/json",
			artifact.canonicalManifest,
		)
		if err != nil || manifestRef != installation.ManifestRef {
			return fmt.Errorf(
				"%w: installation %s manifest_ref differs",
				ErrIntegrity,
				installation.InstallationID,
			)
		}
	}
	for _, stored := range moduleArtifacts {
		artifact, ok := artifacts[stored.ArtifactDigest]
		if !ok {
			return fmt.Errorf(
				"%w: ingressed artifact %s is absent",
				ErrIntegrity,
				stored.ArtifactDigest,
			)
		}
		if artifact.manifest.ID != stored.ModuleID ||
			artifact.manifest.Version != stored.ExactVersion ||
			artifact.sizeBytes < 0 ||
			uint64(artifact.sizeBytes) != stored.ArtifactSizeBytes ||
			uint64(len(artifact.tree.files)) != stored.CoveredFileCount ||
			!bytes.Equal(artifact.canonicalManifest, stored.ManifestBytes) {
			return fmt.Errorf(
				"%w: ingressed artifact %s does not match its Store fact",
				ErrIntegrity,
				stored.ArtifactDigest,
			)
		}
		manifestRef, err := currentstore.ComputeContentDigest(
			currentstore.ContentModuleManifest,
			"application/json",
			artifact.canonicalManifest,
		)
		if err != nil || manifestRef != stored.ManifestRef {
			return fmt.Errorf(
				"%w: ingressed artifact %s manifest_ref differs",
				ErrIntegrity,
				stored.ArtifactDigest,
			)
		}
	}
	if len(artifacts) != len(distinctArtifactDigests(
		installations,
		moduleArtifacts,
	)) {
		return fmt.Errorf("%w: bundle contains unreferenced artifacts", ErrIntegrity)
	}
	return nil
}
