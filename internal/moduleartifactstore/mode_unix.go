//go:build !windows

package moduleartifactstore

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/endview/freeagent/internal/safefiletree"
)

func artifactRootPrivateV1(path string, current os.FileInfo) bool {
	latest, err := os.Lstat(path)
	if err != nil || current == nil || !current.IsDir() || !latest.IsDir() ||
		!os.SameFile(current, latest) || !artifactPathPrivateV1(path, latest) {
		return false
	}
	return latest.Mode().Perm() == 0o700 &&
		latest.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}

func artifactPathPrivateV1(path string, current os.FileInfo) bool {
	latest, err := os.Lstat(path)
	if err != nil || current == nil || !os.SameFile(current, latest) ||
		latest.Mode()&os.ModeSymlink != 0 {
		return false
	}
	stat, ok := latest.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func artifactOpenedPathPrivateV1(file *os.File, expected os.FileInfo) bool {
	if file == nil || expected == nil {
		return false
	}
	current, err := file.Stat()
	if err != nil || !os.SameFile(expected, current) {
		return false
	}
	stat, ok := current.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func artifactSameFilesystemV1(root, current os.FileInfo) bool {
	rootStat, rootOK := root.Sys().(*syscall.Stat_t)
	currentStat, currentOK := current.Sys().(*syscall.Stat_t)
	return rootOK && currentOK && rootStat.Dev == currentStat.Dev
}

func artifactOpenedSingleLinkV1(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil || info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

func artifactOpenedLeaseFilePrivateV1(file *os.File, expected os.FileInfo) bool {
	if file == nil || expected == nil || !expected.Mode().IsRegular() ||
		expected.Mode().Perm() != 0o600 ||
		expected.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	return artifactOpenedPathPrivateV1(file, expected) && artifactOpenedSingleLinkV1(file)
}

func artifactWalkedEntryPrivateV1(entry safefiletree.Entry) bool {
	if entry.Info == nil {
		return false
	}
	private := false
	err := entry.Control(func(descriptor uintptr) error {
		var stat syscall.Stat_t
		if err := syscall.Fstat(int(descriptor), &stat); err != nil {
			return err
		}
		private = stat.Uid == uint32(os.Geteuid())
		return nil
	})
	return err == nil && private
}

func artifactWalkedLeaseFilePrivateV1(entry safefiletree.Entry) bool {
	return entry.Kind == safefiletree.KindRegularFile && entry.Info != nil &&
		entry.Info.Size() == 0 && entry.Info.Mode().Perm() == 0o600 &&
		entry.Info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0 &&
		artifactWalkedEntryPrivateV1(entry)
}

func artifactRootAncestryPrivateV1(path string) bool {
	childPath := filepath.Clean(path)
	child, err := os.Lstat(childPath)
	if err != nil || !child.IsDir() || child.Mode()&os.ModeSymlink != 0 {
		return false
	}
	for {
		parentPath := filepath.Dir(childPath)
		if parentPath == childPath {
			return true
		}
		parent, err := os.Lstat(parentPath)
		if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
			return false
		}
		parentStat, parentOK := parent.Sys().(*syscall.Stat_t)
		if !parentOK || (parentStat.Uid != 0 && parentStat.Uid != uint32(os.Geteuid())) {
			return false
		}
		if parent.Mode().Perm()&0o022 != 0 {
			childStat, childOK := child.Sys().(*syscall.Stat_t)
			if parent.Mode()&os.ModeSticky == 0 || !childOK ||
				childStat.Uid != uint32(os.Geteuid()) {
				return false
			}
		}
		childPath, child = parentPath, parent
	}
}

func artifactProvisionParentPrivateV1(path string, current os.FileInfo) bool {
	if current == nil || !current.IsDir() || !artifactPathPrivateV1(path, current) {
		return false
	}
	return current.Mode().Perm()&0o022 == 0 &&
		current.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}

func provisionArtifactPathPrivateV1(path string) error {
	return os.Chmod(path, 0o700)
}

func provisionArtifactLeaseFilePrivateV1(path string) error {
	return os.Chmod(path, 0o600)
}

func artifactLeasePathKeyV1(path string) string { return path }

func artifactExecutableModeObservableV1() bool { return true }

func artifactModeCandidateV1(info os.FileInfo) (bool, bool) {
	if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false, false
	}
	if info.IsDir() {
		return false, info.Mode().Perm() == 0o700
	}
	if !info.Mode().IsRegular() {
		return false, false
	}
	switch info.Mode().Perm() {
	case 0o600:
		return false, true
	case 0o700:
		return true, true
	default:
		return false, false
	}
}

type artifactInstalledModeNormalizerV1 struct{}

func newArtifactInstalledModeNormalizerV1() (artifactInstalledModeNormalizerV1, error) {
	return artifactInstalledModeNormalizerV1{}, nil
}

func (artifactInstalledModeNormalizerV1) normalizeV1(
	entry safefiletree.Entry,
	executable bool,
) error {
	if entry.Info == nil || entry.Info.Mode()&
		(os.ModeSetuid|os.ModeSetgid|os.ModeSticky|os.ModeSymlink) != 0 {
		return syscall.EINVAL
	}
	target := os.FileMode(0o600)
	if entry.Kind == safefiletree.KindDirectory || executable {
		target = 0o700
	}
	if entry.Kind != safefiletree.KindDirectory &&
		entry.Kind != safefiletree.KindRegularFile {
		return syscall.EINVAL
	}
	if entry.Info.Mode().Perm() == target {
		return nil
	}
	return entry.ControlMetadata(func(descriptor uintptr) error {
		return syscall.Fchmod(int(descriptor), uint32(target.Perm()))
	})
}
