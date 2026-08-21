//go:build linux

package controlhandoff

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

type platformFileIdentityV1 struct {
	device uint64
	inode  uint64
	size   int64
}

type unixDirectoryIdentityV1 struct {
	device uint64
	inode  uint64
}

func verifyNonElevatedPlatformV1() error {
	if os.Geteuid() == 0 {
		return ErrElevatedProcess
	}
	return nil
}

func validatePlatformPathV1(string, bool) error { return nil }

func ensurePrivateRuntimeDirectoryPlatformV1(path string) (bool, error) {
	if _, err := os.Lstat(path); err == nil {
		return false, verifyPrivateDirectoryPlatformV1(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, ErrUnsafeParent
	}
	parent := filepath.Dir(path)
	if err := stableExistingPathUnixV1(parent); err != nil {
		return false, ErrUnsafeParent
	}
	parentFD, err := unix.Open(
		parent,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return false, ErrUnsafeParent
	}
	defer unix.Close(parentFD)
	if err := unix.Mkdirat(parentFD, filepath.Base(path), 0o700); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return false, ErrTargetExists
		}
		return false, ErrUnsafeParent
	}
	if err := verifyPrivateDirectoryPlatformV1(path); err != nil {
		_ = os.Remove(path)
		return false, err
	}
	return true, nil
}

func verifyPrivateDirectoryPlatformV1(path string) error {
	_, err := inspectPrivateDirectoryUnixV1(path)
	return err
}

func inspectPrivateDirectoryUnixV1(
	path string,
) (unixDirectoryIdentityV1, error) {
	if err := stableExistingPathUnixV1(path); err != nil {
		return unixDirectoryIdentityV1{}, ErrUnsafeParent
	}
	fd, err := unix.Open(
		path,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return unixDirectoryIdentityV1{}, ErrUnsafeParent
	}
	defer unix.Close(fd)
	return inspectPrivateDirectoryFDUnixV1(fd)
}

func inspectPrivateDirectoryFDUnixV1(
	fd int,
) (unixDirectoryIdentityV1, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil ||
		stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o077 != 0 ||
		stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Nlink == 0 {
		return unixDirectoryIdentityV1{}, ErrUnsafeParent
	}
	if err := verifyTrustedLocalFilesystemUnixV1(fd); err != nil {
		return unixDirectoryIdentityV1{}, err
	}
	if err := verifyNoExtendedACLUnixV1(fd, true); err != nil {
		return unixDirectoryIdentityV1{}, err
	}
	return unixDirectoryIdentityV1{
		device: uint64(stat.Dev),
		inode:  uint64(stat.Ino),
	}, nil
}

func writeExclusivePrivateFilePlatformV1(
	path string,
	canonical []byte,
) (identity platformFileIdentityV1, returnErr error) {
	return writeExclusivePrivateFileWithCloseUnixV1(
		path,
		canonical,
		func(file *os.File) error { return file.Close() },
	)
}

func writeExclusivePrivateFileWithCloseUnixV1(
	path string,
	canonical []byte,
	closeFile func(*os.File) error,
) (identity platformFileIdentityV1, returnErr error) {
	if closeFile == nil {
		return identity, ErrSecurityVerification
	}
	if _, err := os.Lstat(path); err == nil {
		return identity, ErrTargetExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return identity, ErrSecurityVerification
	}
	parent := filepath.Dir(path)
	expectedParentIdentity, err := inspectPrivateDirectoryUnixV1(parent)
	if err != nil {
		return identity, err
	}
	parentFD, err := unix.Open(
		parent,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return identity, ErrUnsafeParent
	}
	defer unix.Close(parentFD)
	openedParentIdentity, err := inspectPrivateDirectoryFDUnixV1(parentFD)
	if err != nil || !sameUnixDirectoryObjectV1(
		expectedParentIdentity,
		openedParentIdentity,
	) {
		return identity, ErrUnsafeParent
	}
	fd, err := unix.Openat(
		parentFD,
		filepath.Base(path),
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		if errors.Is(err, unix.EEXIST) {
			return identity, ErrTargetExists
		}
		return identity, ErrSecurityVerification
	}
	file := os.NewFile(uintptr(fd), "")
	if file == nil {
		_ = unix.Close(fd)
		_ = unix.Unlinkat(parentFD, filepath.Base(path), 0)
		return identity, ErrSecurityVerification
	}
	emptyIdentity, err := verifyPrivateFileFDUnixV1(fd, 0)
	if err != nil {
		_ = file.Close()
		_ = unix.Unlinkat(parentFD, filepath.Base(path), 0)
		return identity, err
	}
	committed := false
	fileOpen := true
	cleanupIdentity := emptyIdentity
	defer func() {
		// Close must complete before a capability leaf can be committed. If
		// close reports failure, unlink only the exact CREATE_NEW object.
		if fileOpen {
			if current, err := inspectPrivateFileFDUnixV1(fd); err == nil &&
				sameUnixFileObjectV1(current, cleanupIdentity) {
				cleanupIdentity = current
			}
			if err := closeFile(file); err != nil && returnErr == nil {
				returnErr = ErrSecurityVerification
			}
			fileOpen = false
		}
		if !committed {
			if err := removeExactCreatedFileUnixV1(
				parentFD,
				filepath.Base(path),
				cleanupIdentity,
			); err != nil && returnErr == nil {
				returnErr = ErrSecurityVerification
			}
		}
	}()
	written, err := file.Write(canonical)
	if err != nil || written != len(canonical) || file.Sync() != nil {
		return identity, ErrSecurityVerification
	}
	identity, err = verifyPrivateFileFDUnixV1(fd, int64(len(canonical)))
	if err != nil {
		return platformFileIdentityV1{}, err
	}
	if !sameUnixFileObjectV1(identity, emptyIdentity) {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	cleanupIdentity = identity
	if err := unix.Fsync(parentFD); err != nil {
		return platformFileIdentityV1{}, ErrSecurityVerification
	}
	pathIdentity, err := inspectPrivateFileUnixV1(path, int64(len(canonical)))
	if err != nil || pathIdentity != identity {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	if err := closeFile(file); err != nil {
		fileOpen = false
		return identity, ErrSecurityVerification
	}
	fileOpen = false
	pathIdentity, err = inspectPrivateFileUnixV1(path, int64(len(canonical)))
	if err != nil || pathIdentity != identity {
		return identity, ErrIdentityChanged
	}
	committed = true
	return identity, nil
}

func inspectPrivateFileFDUnixV1(fd int) (platformFileIdentityV1, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil ||
		stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o077 != 0 ||
		stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
		return platformFileIdentityV1{}, ErrSecurityVerification
	}
	if err := verifyTrustedLocalFilesystemUnixV1(fd); err != nil {
		return platformFileIdentityV1{}, err
	}
	if err := verifyNoExtendedACLUnixV1(fd, false); err != nil {
		return platformFileIdentityV1{}, err
	}
	return platformFileIdentityV1{
		device: uint64(stat.Dev),
		inode:  uint64(stat.Ino),
		size:   stat.Size,
	}, nil
}

func sameUnixDirectoryObjectV1(
	left unixDirectoryIdentityV1,
	right unixDirectoryIdentityV1,
) bool {
	return left.device == right.device && left.inode == right.inode
}

func verifyTrustedLocalFilesystemUnixV1(fd int) error {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(fd, &stat); err != nil ||
		!isTrustedLocalFilesystemTypeUnixV1(stat.Type) {
		return ErrUnsafeParent
	}
	return nil
}

func isTrustedLocalFilesystemTypeUnixV1(filesystemType int64) bool {
	switch filesystemType {
	case unix.EXT4_SUPER_MAGIC,
		unix.XFS_SUPER_MAGIC,
		unix.BTRFS_SUPER_MAGIC,
		unix.TMPFS_MAGIC,
		unix.RAMFS_MAGIC,
		unix.OVERLAYFS_SUPER_MAGIC:
		return true
	default:
		return false
	}
}

func verifyNoExtendedACLUnixV1(fd int, directory bool) error {
	attributes := []string{"system.posix_acl_access"}
	if directory {
		attributes = append(attributes, "system.posix_acl_default")
	}
	for _, attribute := range attributes {
		if _, err := unix.Fgetxattr(fd, attribute, nil); err == nil {
			return ErrUnsafeParent
		} else if !errors.Is(err, unix.ENODATA) {
			// ENOTSUP and all other unknown results fail closed: without a
			// successful absence proof mode bits are not sufficient authority.
			return ErrUnsafeParent
		}
	}
	return nil
}

func sameUnixFileObjectV1(
	left platformFileIdentityV1,
	right platformFileIdentityV1,
) bool {
	return left.device == right.device && left.inode == right.inode
}

func removeExactCreatedFileUnixV1(
	parentFD int,
	leaf string,
	identity platformFileIdentityV1,
) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(
		parentFD,
		leaf,
		&stat,
		unix.AT_SYMLINK_NOFOLLOW,
	); errors.Is(err, unix.ENOENT) {
		return nil
	} else if err != nil {
		return ErrIdentityChanged
	}
	current := platformFileIdentityV1{
		device: uint64(stat.Dev),
		inode:  uint64(stat.Ino),
		size:   stat.Size,
	}
	if stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o077 != 0 ||
		stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 ||
		current.size != identity.size ||
		!sameUnixFileObjectV1(current, identity) {
		return ErrIdentityChanged
	}
	if err := unix.Unlinkat(parentFD, leaf, 0); err != nil &&
		!errors.Is(err, unix.ENOENT) {
		return ErrSecurityVerification
	}
	if err := unix.Fsync(parentFD); err != nil {
		return ErrSecurityVerification
	}
	return nil
}

func inspectPrivateFileUnixV1(
	path string,
	size int64,
) (platformFileIdentityV1, error) {
	if err := stableExistingPathUnixV1(path); err != nil {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return platformFileIdentityV1{}, ErrIdentityChanged
	}
	defer unix.Close(fd)
	return verifyPrivateFileFDUnixV1(fd, size)
}

func verifyPrivateFileFDUnixV1(
	fd int,
	size int64,
) (platformFileIdentityV1, error) {
	identity, err := inspectPrivateFileFDUnixV1(fd)
	if err != nil || identity.size != size {
		return platformFileIdentityV1{}, ErrSecurityVerification
	}
	return identity, nil
}

func removeExactPrivateFilePlatformV1(
	path string,
	identity platformFileIdentityV1,
) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return ErrIdentityChanged
	}
	current, err := inspectPrivateFileUnixV1(path, identity.size)
	if err != nil || current != identity {
		return ErrIdentityChanged
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrSecurityVerification
	}
	return nil
}

func removeEmptyDirectoryPlatformV1(path string) {
	if verifyPrivateDirectoryPlatformV1(path) == nil {
		_ = os.Remove(path)
	}
}

func stableExistingPathUnixV1(path string) error {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafeParent
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ErrUnsafeParent
	}
	absoluteResolved, err := filepath.Abs(resolved)
	if err != nil || filepath.Clean(absoluteResolved) != filepath.Clean(path) {
		return ErrUnsafeParent
	}
	if raw, ok := info.Sys().(*syscall.Stat_t); ok && raw.Nlink == 0 {
		return ErrUnsafeParent
	}
	return nil
}
