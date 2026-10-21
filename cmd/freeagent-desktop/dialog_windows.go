//go:build windows

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	ole32                           = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	procSHCreateItemFromParsingName = windows.NewLazySystemDLL("shell32.dll").NewProc("SHCreateItemFromParsingName")
)

const (
	clsctxInprocServer = 0x1
	fosPickFolders     = 0x00000020
	fosForceFileSystem = 0x00000040
	fosPathMustExist   = 0x00000800
)

type comObject struct {
	vtable *[32]uintptr
}

func comPointer(object uintptr) unsafe.Pointer {
	// COM interface pointers arrive from syscall as uintptr; copy the bits
	// through a local word before dereferencing the unmanaged vtable.
	return *(*unsafe.Pointer)(unsafe.Pointer(&object))
}

func comCall(object uintptr, index int, args ...uintptr) uintptr {
	entry := (*comObject)(comPointer(object)).vtable[index]
	values := make([]uintptr, 0, len(args)+1)
	values = append(values, object)
	values = append(values, args...)
	result, _, _ := syscall.SyscallN(entry, values...)
	return result
}

func comRelease(object uintptr) {
	if object != 0 {
		_ = comCall(object, 2)
	}
}

func chooseWorkspace(hwnd windows.Handle, current string) (string, bool, error) {
	name, changed, err := chooseDialog(hwnd, current, true)
	if err != nil || !changed {
		return name, changed, err
	}
	if !confirmClose(hwnd, "更换到 "+name+"？\n\n更换后，当前工作区的会话不会显示，换回原工作区后会重新出现。", "更换工作区") {
		return "", false, nil
	}
	return name, true, nil
}

func chooseFolder(hwnd windows.Handle, current string) (string, bool, error) {
	return chooseDialog(hwnd, current, true)
}

func chooseFile(hwnd windows.Handle, current string) (string, bool, error) {
	return chooseDialog(hwnd, current, false)
}

func chooseDialog(hwnd windows.Handle, current string, folders bool) (string, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	initialized := true
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil {
		initialized = errors.Is(err, syscall.Errno(windows.S_FALSE))
		if !initialized {
			return "", false, err
		}
	}
	if initialized {
		defer windows.CoUninitialize()
	}

	clsid, err := windows.GUIDFromString("{DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7}")
	if err != nil {
		return "", false, err
	}
	iid, err := windows.GUIDFromString("{D57C7288-D4AD-4768-BE02-9D969532D960}")
	if err != nil {
		return "", false, err
	}
	var dialog uintptr
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsid)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&dialog)))
	if int32(uint32(hr)) < 0 {
		return "", false, fmt.Errorf("CoCreateInstance failed: 0x%x", hr)
	}
	defer comRelease(dialog)

	options := uintptr(0)
	if result := comCall(dialog, 10, uintptr(unsafe.Pointer(&options))); result != 0 {
		return "", false, fmt.Errorf("IFileDialog.GetOptions failed: 0x%x", result)
	}
	if folders {
		options |= fosPickFolders
	}
	options |= fosForceFileSystem | fosPathMustExist
	if result := comCall(dialog, 9, options); result != 0 {
		return "", false, fmt.Errorf("IFileDialog.SetOptions failed: 0x%x", result)
	}
	if current != "" {
		if item, itemErr := shellItem(current); itemErr == nil {
			_ = comCall(dialog, 12, item)
			comRelease(item)
		}
	}
	if result := comCall(dialog, 3, uintptr(hwnd)); result != 0 {
		if result == 0x800704C7 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("IFileDialog.Show failed: 0x%x", result)
	}
	var resultItem uintptr
	if result := comCall(dialog, 20, uintptr(unsafe.Pointer(&resultItem))); result != 0 {
		return "", false, fmt.Errorf("IFileDialog.GetResult failed: 0x%x", result)
	}
	defer comRelease(resultItem)
	name, err := shellItemPath(resultItem)
	if err != nil {
		return "", false, err
	}
	if filepathEqual(name, current) {
		return "", false, nil
	}
	return name, true, nil
}

func shellItem(path string) (uintptr, error) {
	raw, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	iid, err := windows.GUIDFromString("{43826D1E-E718-42EE-BC55-A1E261C37BFE}")
	if err != nil {
		return 0, err
	}
	var item uintptr
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(raw)), 0, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&item)))
	if int32(uint32(hr)) < 0 {
		return 0, fmt.Errorf("SHCreateItemFromParsingName failed: 0x%x", hr)
	}
	return item, nil
}

func shellItemPath(item uintptr) (string, error) {
	var raw *uint16
	if result := comCall(item, 5, uintptr(sigdnFileSysPath), uintptr(unsafe.Pointer(&raw))); result != 0 {
		return "", fmt.Errorf("IShellItem.GetDisplayName failed: 0x%x", result)
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(raw))
	return windows.UTF16PtrToString(raw), nil
}

func filepathEqual(left, right string) bool {
	left = strings.TrimRight(filepath.Clean(left), `\/`)
	right = strings.TrimRight(filepath.Clean(right), `\/`)
	return strings.EqualFold(left, right)
}
