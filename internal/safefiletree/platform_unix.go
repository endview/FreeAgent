//go:build linux || darwin

package safefiletree

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type platformDevice uint64

type platformIdentity struct {
	device uint64
	inode  uint64
	kind   Kind
	size   int64
	mtime  int64
	ctime  int64
}

type platformNode struct {
	file     *os.File
	info     fs.FileInfo
	kind     Kind
	device   platformDevice
	identity platformIdentity
}

func openPlatformRoot(root string, _ bool) (*platformNode, error) {
	absolute, err := filepath.Abs(root)
	if err != nil || !filepath.IsAbs(absolute) {
		return nil, errors.New("root path is not absolute")
	}
	absolute = filepath.Clean(absolute)
	currentFD, err := unix.Open(
		string(filepath.Separator),
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, err
	}
	currentName := string(filepath.Separator)
	components := strings.Split(strings.TrimPrefix(absolute, currentName), currentName)
	for _, component := range components {
		if component == "" {
			continue
		}
		if !validChildName(component) {
			_ = unix.Close(currentFD)
			return nil, errors.New("root path contains an invalid component")
		}
		nextFD, openErr := unix.Openat(
			currentFD,
			component,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
			0,
		)
		_ = unix.Close(currentFD)
		if openErr != nil {
			return nil, openErr
		}
		currentFD = nextFD
		currentName = filepath.Join(currentName, component)
	}
	file := os.NewFile(uintptr(currentFD), currentName)
	if file == nil {
		_ = unix.Close(currentFD)
		return nil, errors.New("create root file handle")
	}
	node, err := inspectUnixNode(file, KindDirectory)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return node, nil
}

func openPlatformChild(
	parent *platformNode,
	name string,
	rootDevice platformDevice,
	openForSync bool,
	_ bool,
) (*platformNode, error) {
	directory, directoryErr := openUnixChild(parent, name, KindDirectory, false)
	if directoryErr == nil {
		if directory.device != rootDevice {
			_ = directory.file.Close()
			return nil, errors.New("directory crosses filesystem boundary")
		}
		return directory, nil
	}
	regular, regularErr := openUnixChild(parent, name, KindRegularFile, openForSync)
	if regularErr == nil {
		if regular.device != rootDevice {
			_ = regular.file.Close()
			return nil, errors.New("regular file crosses filesystem boundary")
		}
		return regular, nil
	}
	return nil, fmt.Errorf(
		"child is unsafe (possible symlink or reparse point): %w",
		errors.Join(directoryErr, regularErr),
	)
}

func openUnixChild(
	parent *platformNode,
	name string,
	kind Kind,
	openForSync bool,
) (*platformNode, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC |
		unix.O_NONBLOCK | unix.O_NOCTTY
	if kind == KindDirectory {
		flags |= unix.O_DIRECTORY
	} else if openForSync {
		flags &^= unix.O_RDONLY
		flags |= unix.O_RDWR
	}
	fd, err := unix.Openat(int(parent.file.Fd()), name, flags, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("create child file handle")
	}
	node, err := inspectUnixNode(file, kind)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return node, nil
}

func inspectUnixNode(file *os.File, expected Kind) (*platformNode, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return nil, err
	}
	var kind Kind
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		kind = KindDirectory
	case unix.S_IFREG:
		kind = KindRegularFile
	default:
		return nil, errors.New("entry is neither a directory nor a regular file")
	}
	if kind != expected {
		return nil, fmt.Errorf("entry kind changed: got %d, want %d", kind, expected)
	}
	links := uint64(stat.Nlink)
	if links == 0 || kind == KindRegularFile && links != 1 {
		return nil, errors.New("regular file is a hardlink or entry has an unsafe link count")
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if kind == KindDirectory && !info.IsDir() ||
		kind == KindRegularFile && !info.Mode().IsRegular() {
		return nil, errors.New("entry kind differs between handle inspections")
	}
	return &platformNode{
		file:   file,
		info:   info,
		kind:   kind,
		device: platformDevice(stat.Dev),
		identity: platformIdentity{
			device: uint64(stat.Dev),
			inode:  uint64(stat.Ino),
			kind:   kind,
			size:   stat.Size,
			mtime:  info.ModTime().UnixNano(),
			ctime:  unixChangeTime(stat),
		},
	}, nil
}

func syncPlatformNode(node *platformNode) error {
	return node.file.Sync()
}

func verifyPlatformNode(node *platformNode, rootDevice platformDevice) error {
	current, err := inspectUnixNode(node.file, node.kind)
	if err != nil {
		return err
	}
	if current.device != rootDevice || current.identity != node.identity {
		return errors.New("opened entry identity changed")
	}
	return nil
}

func refreshPlatformNodeMetadata(node *platformNode) error {
	current, err := inspectUnixNode(node.file, node.kind)
	if err != nil {
		return err
	}
	before, after := node.identity, current.identity
	if before.device != after.device || before.inode != after.inode ||
		before.kind != after.kind || before.size != after.size ||
		before.mtime != after.mtime {
		return errors.New("opened entry changed beyond permission metadata")
	}
	node.info = current.info
	node.identity = current.identity
	return nil
}
