//go:build windows

package safefiletree

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type platformDevice uint32

type platformIdentity struct {
	volume uint32
	index  uint64
	kind   Kind
	size   int64
	mtime  int64
	change int64
}

type platformNode struct {
	file     *os.File
	info     fs.FileInfo
	kind     Kind
	device   platformDevice
	identity platformIdentity
}

func openPlatformRoot(root string, openForMetadataControl bool) (*platformNode, error) {
	absolute, err := filepath.Abs(root)
	if err != nil || !filepath.IsAbs(absolute) {
		return nil, errors.New("root path is not absolute")
	}
	absolute = filepath.Clean(absolute)
	volume := filepath.VolumeName(absolute)
	if volume == "" || strings.HasPrefix(volume, `\\.\`) {
		return nil, errors.New("root path has an unsupported volume")
	}
	volumeRoot := volume + string(filepath.Separator)
	remainder := strings.TrimLeft(absolute[len(volume):], `\/`)
	current, err := openWindowsVolumeRoot(volumeRoot, openForMetadataControl && remainder == "")
	if err != nil {
		return nil, err
	}
	if remainder == "" {
		return current, nil
	}
	components := strings.FieldsFunc(remainder, func(character rune) bool {
		return character == '\\' || character == '/'
	})
	for index, component := range components {
		if !validWindowsChildName(component) {
			_ = current.file.Close()
			return nil, errors.New("root path contains an invalid component")
		}
		next, openErr := openWindowsChild(
			current, component, KindDirectory, false,
			openForMetadataControl && index == len(components)-1, true,
		)
		_ = current.file.Close()
		if openErr != nil {
			return nil, openErr
		}
		current = next
	}
	return current, nil
}

func openWindowsVolumeRoot(path string, openForMetadataControl bool) (*platformNode, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(
		windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES |
			windows.READ_CONTROL | windows.SYNCHRONIZE,
	)
	if openForMetadataControl {
		access |= windows.WRITE_DAC
	}
	handle, err := windows.CreateFile(
		pointer,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("create volume root file handle")
	}
	node, err := inspectWindowsNode(file, KindDirectory)
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
	openForMetadataControl bool,
) (*platformNode, error) {
	if !validWindowsChildName(name) {
		return nil, errors.New("invalid Windows child name")
	}
	directory, directoryErr := openWindowsChild(
		parent, name, KindDirectory, false, openForMetadataControl, false,
	)
	if directoryErr == nil {
		if directory.device != rootDevice {
			_ = directory.file.Close()
			return nil, errors.New("directory crosses volume boundary")
		}
		return directory, nil
	}
	regular, regularErr := openWindowsChild(
		parent, name, KindRegularFile, openForSync, openForMetadataControl, false,
	)
	if regularErr == nil {
		if regular.device != rootDevice {
			_ = regular.file.Close()
			return nil, errors.New("regular file crosses volume boundary")
		}
		return regular, nil
	}
	return nil, fmt.Errorf(
		"child is unsafe (possible symlink or reparse point): %w",
		errors.Join(directoryErr, regularErr),
	)
}

func openWindowsChild(
	parent *platformNode,
	name string,
	kind Kind,
	openForSync bool,
	openForMetadataControl bool,
	caseInsensitive bool,
) (*platformNode, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: windows.Handle(parent.file.Fd()),
		ObjectName:    objectName,
		Attributes:    windows.OBJ_DONT_REPARSE,
	}
	if caseInsensitive {
		attributes.Attributes |= windows.OBJ_CASE_INSENSITIVE
	}
	options := uint32(
		windows.FILE_SYNCHRONOUS_IO_NONALERT |
			windows.FILE_OPEN_REPARSE_POINT |
			windows.FILE_OPEN_FOR_BACKUP_INTENT,
	)
	if kind == KindDirectory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	var handle windows.Handle
	access := uint32(
		windows.FILE_READ_DATA | windows.FILE_READ_ATTRIBUTES |
			windows.READ_CONTROL | windows.SYNCHRONIZE,
	)
	if openForSync && kind == KindRegularFile {
		access |= windows.GENERIC_WRITE
	}
	if openForMetadataControl {
		access |= windows.WRITE_DAC
	}
	err = windows.NtCreateFile(
		&handle,
		access,
		attributes,
		&windows.IO_STATUS_BLOCK{},
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		options,
		0,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), name)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("create child file handle")
	}
	node, err := inspectWindowsNode(file, kind)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return node, nil
}

func inspectWindowsNode(file *os.File, expected Kind) (*platformNode, error) {
	handle := windows.Handle(file.Fd())
	fileType, err := windows.GetFileType(handle)
	if err != nil || fileType != windows.FILE_TYPE_DISK {
		return nil, errors.New("entry is not a disk filesystem object")
	}
	var byHandle windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &byHandle); err != nil {
		return nil, err
	}
	if byHandle.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, errors.New("entry is a reparse point")
	}
	kind := KindRegularFile
	if byHandle.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		kind = KindDirectory
	}
	if kind != expected {
		return nil, fmt.Errorf("entry kind changed: got %d, want %d", kind, expected)
	}
	if byHandle.NumberOfLinks == 0 || kind == KindRegularFile && byHandle.NumberOfLinks != 1 {
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
	size := int64(uint64(byHandle.FileSizeHigh)<<32 | uint64(byHandle.FileSizeLow))
	var basic windowsFileBasicInfo
	if err := windows.GetFileInformationByHandleEx(
		handle,
		windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&basic)),
		uint32(unsafe.Sizeof(basic)),
	); err != nil {
		return nil, err
	}
	return &platformNode{
		file:   file,
		info:   info,
		kind:   kind,
		device: platformDevice(byHandle.VolumeSerialNumber),
		identity: platformIdentity{
			volume: byHandle.VolumeSerialNumber,
			index: uint64(byHandle.FileIndexHigh)<<32 |
				uint64(byHandle.FileIndexLow),
			kind:   kind,
			size:   size,
			mtime:  basic.LastWriteTime,
			change: basic.ChangeTime,
		},
	}, nil
}

type windowsFileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              uint32
}

func syncPlatformNode(node *platformNode) error {
	if node.kind == KindDirectory {
		return nil
	}
	return node.file.Sync()
}

func verifyPlatformNode(node *platformNode, rootDevice platformDevice) error {
	current, err := inspectWindowsNode(node.file, node.kind)
	if err != nil {
		return err
	}
	if current.device != rootDevice || current.identity != node.identity {
		return errors.New("opened entry identity changed")
	}
	return nil
}

func refreshPlatformNodeMetadata(node *platformNode) error {
	current, err := inspectWindowsNode(node.file, node.kind)
	if err != nil {
		return err
	}
	before, after := node.identity, current.identity
	if before.volume != after.volume || before.index != after.index ||
		before.kind != after.kind || before.size != after.size ||
		before.mtime != after.mtime {
		return errors.New("opened entry changed beyond permission metadata")
	}
	node.info = current.info
	node.identity = current.identity
	return nil
}

func validWindowsChildName(name string) bool {
	return validChildName(name) && !strings.ContainsRune(name, ':')
}
