//go:build windows

package main

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	wmClose                 = 0x0010
	wmQueryEndSession       = 0x0011
	wmEndSession            = 0x0016
	wmSettingChange         = 0x001A
	gwlpWndProc             = ^uintptr(3)
	swRestore               = 9
	swHide                  = 0
	swShowMaximized         = 3
	monitorDefaultToNearest = 2
	flashwAll               = 3
	flashwTimerNoFG         = 0xC
	mbOKCancel              = 1
	mbIconWarning           = 0x30
	mbDefaultButton2        = 0x100
	sigdnFileSysPath        = 0x80058000
	dwmImmersiveDark        = uint32(windows.DWMWA_USE_IMMERSIVE_DARK_MODE)
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procCallWindowProc      = user32.NewProc("CallWindowProcW")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procGetProp             = user32.NewProc("GetPropW")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procPostMessage         = user32.NewProc("PostMessageW")
	procRemoveProp          = user32.NewProc("RemovePropW")
	procSetForeground       = user32.NewProc("SetForegroundWindow")
	procSetProp             = user32.NewProc("SetPropW")
	procSetWindowLongPtr    = user32.NewProc("SetWindowLongPtrW")
	procGetWindowPlacement  = user32.NewProc("GetWindowPlacement")
	procSetWindowPlacement  = user32.NewProc("SetWindowPlacement")
	procShowWindow          = user32.NewProc("ShowWindow")
	procMonitorFromRect     = user32.NewProc("MonitorFromRect")
	procMonitorFromWindow   = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfo      = user32.NewProc("GetMonitorInfoW")
	procGetDpiForWindow     = user32.NewProc("GetDpiForWindow")
	procFlashWindowEx       = user32.NewProc("FlashWindowEx")
)

func systemDark() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("AppsUseLightTheme")
	return darkFromRegistry(value, err)
}

func setDarkTitleBar(hwnd windows.Handle, dark bool) error {
	value := uint32(0)
	if dark {
		value = 1
	}
	return windows.DwmSetWindowAttribute(windows.HWND(hwnd), dwmImmersiveDark,
		unsafe.Pointer(&value), uint32(unsafe.Sizeof(value)))
}

func showMessage(text, title string) {
	textPtr, _ := windows.UTF16PtrFromString(text)
	titlePtr, _ := windows.UTF16PtrFromString(title)
	_, _ = windows.MessageBox(0, textPtr, titlePtr, uint32(windows.MB_OK)|uint32(windows.MB_ICONERROR))
}

func confirmClose(hwnd windows.Handle, text, title string) bool {
	textPtr, _ := windows.UTF16PtrFromString(text)
	titlePtr, _ := windows.UTF16PtrFromString(title)
	result, _ := windows.MessageBox(windows.HWND(hwnd), textPtr, titlePtr,
		uint32(mbOKCancel)|uint32(mbIconWarning)|uint32(mbDefaultButton2))
	return result == 1
}

func setWindowProperty(hwnd windows.Handle, name string) error {
	property, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	ok, _, callErr := procSetProp.Call(uintptr(hwnd), uintptr(unsafe.Pointer(property)), 1)
	if ok == 0 {
		return fmt.Errorf("SetPropW: %w", callErr)
	}
	return nil
}

func removeWindowProperty(hwnd windows.Handle, name string) {
	property, err := windows.UTF16PtrFromString(name)
	if err == nil {
		_, _, _ = procRemoveProp.Call(uintptr(hwnd), uintptr(unsafe.Pointer(property)))
	}
}

func findWindowByProperty(name string) (windows.Handle, bool) {
	property, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, false
	}
	var found windows.Handle
	callback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		value, _, _ := procGetProp.Call(hwnd, uintptr(unsafe.Pointer(property)))
		if value != 0 {
			found = windows.Handle(hwnd)
			return 0
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(callback, 0)
	return found, found != 0
}

func activateWindow(hwnd windows.Handle) {
	_, _, _ = procShowWindow.Call(uintptr(hwnd), swRestore)
	_, _, _ = procSetForeground.Call(uintptr(hwnd))
}

func hideWindow(hwnd windows.Handle) {
	_, _, _ = procShowWindow.Call(uintptr(hwnd), swHide)
}

type desktopRect struct {
	Left, Top, Right, Bottom int32
}

type desktopPoint struct {
	X, Y int32
}

type windowPlacement struct {
	Length         uint32
	Flags          uint32
	ShowCmd        uint32
	MinPosition    desktopPoint
	MaxPosition    desktopPoint
	NormalPosition desktopRect
}

func getWindowPlacement(hwnd windows.Handle) (desktopState, error) {
	var placement windowPlacement
	placement.Length = uint32(unsafe.Sizeof(placement))
	ok, _, err := procGetWindowPlacement.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&placement)))
	if ok == 0 {
		return desktopState{}, fmt.Errorf("GetWindowPlacement: %w", err)
	}
	return desktopState{
		Version:   desktopStateVersion,
		X:         int(placement.NormalPosition.Left),
		Y:         int(placement.NormalPosition.Top),
		Width:     int(placement.NormalPosition.Right - placement.NormalPosition.Left),
		Height:    int(placement.NormalPosition.Bottom - placement.NormalPosition.Top),
		Maximized: placement.ShowCmd == swShowMaximized,
	}, nil
}

func setWindowPlacement(hwnd windows.Handle, state desktopState) error {
	if !validDesktopState(state) {
		return errors.New("invalid window placement")
	}
	rect := desktopRect{Left: int32(state.X), Top: int32(state.Y),
		Right: int32(state.X + state.Width), Bottom: int32(state.Y + state.Height)}
	var placement windowPlacement
	placement.Length = uint32(unsafe.Sizeof(placement))
	placement.ShowCmd = swRestore
	placement.NormalPosition = rect
	if state.Maximized {
		placement.ShowCmd = swShowMaximized
	}
	ok, _, err := procSetWindowPlacement.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&placement)))
	if ok == 0 {
		return fmt.Errorf("SetWindowPlacement: %w", err)
	}
	return nil
}

func flashWindow(hwnd windows.Handle) {
	type flashInfo struct {
		CbSize                     uint32
		Hwnd                       windows.Handle
		DwFlags, UCount, DwTimeout uint32
	}
	info := flashInfo{CbSize: uint32(unsafe.Sizeof(flashInfo{})), Hwnd: hwnd,
		DwFlags: flashwAll | flashwTimerNoFG, UCount: ^uint32(0)}
	_, _, _ = procFlashWindowEx.Call(uintptr(unsafe.Pointer(&info)))
}

func isWindowVisibleOnMonitor(state desktopState) bool {
	rect := desktopRect{Left: int32(state.X), Top: int32(state.Y),
		Right: int32(state.X + state.Width), Bottom: int32(state.Y + state.Height)}
	monitor, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(&rect)), 0)
	return monitor != 0
}

func windowDPI(hwnd windows.Handle) int {
	if procGetDpiForWindow.Find() == nil {
		if value, _, _ := procGetDpiForWindow.Call(uintptr(hwnd)); value != 0 {
			return int(value)
		}
	}
	return baseDPI
}

// defaultWindowPlacement sizes the first-launch window for the DPI and work
// area of the monitor that currently holds hwnd. Placement coordinates are
// physical workspace coordinates, whose origin is the work area of the primary
// monitor where the new window was created.
func defaultWindowPlacement(hwnd windows.Handle) desktopState {
	state := defaultDesktopState()
	dpi := windowDPI(hwnd)
	type monitorInfo struct {
		Size    uint32
		Monitor desktopRect
		Work    desktopRect
		Flags   uint32
	}
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	monitor, _, _ := procMonitorFromWindow.Call(uintptr(hwnd), monitorDefaultToNearest)
	if monitor == 0 {
		state.Width, state.Height = scaleForDPI(state.Width, dpi), scaleForDPI(state.Height, dpi)
		return state
	}
	if ok, _, _ := procGetMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); ok == 0 {
		state.Width, state.Height = scaleForDPI(state.Width, dpi), scaleForDPI(state.Height, dpi)
		return state
	}
	work := info.Work
	state.X, state.Y, state.Width, state.Height = defaultWindowRect(dpi,
		int(work.Right-work.Left), int(work.Bottom-work.Top))
	if !validDesktopState(state) {
		return defaultDesktopState()
	}
	return state
}

func currentWindowState(hwnd windows.Handle, lastPort int) desktopState {
	state, err := getWindowPlacement(hwnd)
	if err != nil || !validDesktopState(state) {
		state = defaultDesktopState()
	}
	state.LastPort = lastPort
	return state
}

func postClose(hwnd windows.Handle) {
	_, _, _ = procPostMessage.Call(uintptr(hwnd), wmClose, 0, 0)
}

type windowSubclass struct {
	hwnd            windows.Handle
	previous        uintptr
	callback        uintptr
	allowClose      bool
	onClose         func()
	onShutdown      func()
	onSettingChange func()
}

func installWindowSubclass(hwnd windows.Handle, onClose func()) (*windowSubclass, error) {
	subclass := &windowSubclass{hwnd: hwnd, onClose: onClose}
	subclass.callback = windows.NewCallback(subclass.windowProc)
	previous, _, callErr := procSetWindowLongPtr.Call(uintptr(hwnd), gwlpWndProc, subclass.callback)
	if previous == 0 && callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
		return nil, fmt.Errorf("SetWindowLongPtrW: %w", callErr)
	}
	subclass.previous = previous
	return subclass, nil
}

func (s *windowSubclass) windowProc(hwnd, message, wParam, lParam uintptr) uintptr {
	if message == wmQueryEndSession {
		return 1
	}
	if message == wmEndSession {
		if wParam != 0 && s.onShutdown != nil {
			s.onShutdown()
		}
		return 1
	}
	if message == wmSettingChange && s.onSettingChange != nil {
		s.onSettingChange()
	}
	if message == wmClose && !s.allowClose {
		if s.onClose != nil {
			s.onClose()
		}
		return 0
	}
	result, _, _ := procCallWindowProc.Call(s.previous, hwnd, message, wParam, lParam)
	return result
}

func (s *windowSubclass) allowWindowClose() {
	s.allowClose = true
	postClose(s.hwnd)
}

func (s *windowSubclass) restore() {
	if s == nil || s.previous == 0 {
		return
	}
	_, _, _ = procSetWindowLongPtr.Call(uintptr(s.hwnd), gwlpWndProc, s.previous)
	s.previous = 0
}
