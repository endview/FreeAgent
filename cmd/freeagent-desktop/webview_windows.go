//go:build windows

package main

import (
	"errors"
	"log"
	"net/url"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/pkg/edge"
	"github.com/jchv/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

type desktopWebView struct {
	view            webview2.WebView
	subclass        *windowSubclass
	origin          string
	workspace       string
	busy            atomic.Bool
	changing        atomic.Bool
	darkTitleFailed atomic.Bool
	workspaceChange func(string) (map[string]any, error)
	attention       func()
	onShutdown      func()
}

func newDesktopWebView(launch engineLaunch, dataPath, workspace string, onClose func()) (*desktopWebView, error) {
	if err := ensureWritableDirectory(dataPath); err != nil {
		return nil, err
	}
	installed, err := webviewloader.GetInstalledVersion()
	if err != nil {
		return nil, err
	}
	if installed == "" {
		return nil, errors.New("WebView2 runtime is not installed")
	}
	view := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  dataPath,
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  "FreeAgent",
			Width:  1200,
			Height: 800,
			Center: true,
		},
	})
	if view == nil {
		return nil, errors.New("WebView2 window creation failed")
	}
	desktop := &desktopWebView{
		view:      view,
		origin:    launch.Origin,
		workspace: workspace,
	}
	hwnd := windows.Handle(uintptr(view.Window()))
	subclass, err := installWindowSubclass(hwnd, func() {
		if onClose != nil {
			onClose()
		}
	})
	if err != nil {
		view.Destroy()
		return nil, err
	}
	desktop.subclass = subclass
	subclass.onSettingChange = func() {
		desktop.updateDarkTitleBar(systemDark())
	}
	desktop.updateDarkTitleBar(systemDark())
	view.SetSize(1200, 800, webview2.HintNone)
	dpi := windowDPI(hwnd)
	view.SetSize(scaleForDPI(minWindowWidth, dpi), scaleForDPI(minWindowHeight, dpi), webview2.HintMin)
	if err := desktop.bind(); err != nil {
		desktop.Close()
		return nil, err
	}
	desktop.initGuard()
	view.Navigate(launch.Address)
	return desktop, nil
}

func (w *desktopWebView) bind() error {
	if err := w.view.Bind("faOpenExternal", func(raw string) error {
		if err := w.checkSource(); err != nil {
			return err
		}
		if !externalURLAllowed(raw) {
			return errors.New("external URL scheme is not allowed")
		}
		return shellOpen(raw)
	}); err != nil {
		return err
	}
	if err := w.view.Bind("faSetBusy", func(busy bool) error {
		if err := w.checkSource(); err != nil {
			return err
		}
		w.busy.Store(busy)
		return nil
	}); err != nil {
		return err
	}
	if err := w.view.Bind("faChangeWorkspace", func() (map[string]any, error) {
		if err := w.checkSource(); err != nil {
			return nil, err
		}
		if w.busy.Load() || w.changing.Load() {
			return nil, errors.New("请先停止运行中的任务")
		}
		next, changed, err := chooseWorkspace(w.hwnd(), w.workspace)
		if err != nil || !changed {
			return map[string]any{"changed": false}, err
		}
		if w.workspaceChange == nil {
			return nil, errors.New("workspace change is unavailable")
		}
		result, err := w.workspaceChange(next)
		if err == nil {
			w.changing.Store(true)
		}
		return result, err
	}); err != nil {
		return err
	}
	if err := w.view.Bind("faSelectFolder", func(current string) (string, error) {
		if err := w.checkSource(); err != nil {
			return "", err
		}
		selected, changed, err := chooseFolder(w.hwnd(), current)
		if err != nil || !changed {
			return "", err
		}
		return selected, nil
	}); err != nil {
		return err
	}
	if err := w.view.Bind("faSelectFile", func(current string) (string, error) {
		if err := w.checkSource(); err != nil {
			return "", err
		}
		selected, changed, err := chooseFile(w.hwnd(), current)
		if err != nil || !changed {
			return "", err
		}
		return selected, nil
	}); err != nil {
		return err
	}
	return w.view.Bind("faAttention", func() error {
		if err := w.checkSource(); err != nil {
			return err
		}
		if w.attention != nil {
			w.attention()
		}
		return nil
	})
}

func (w *desktopWebView) initGuard() {
	script := `(function(){` +
		`var expected=` + jsString(w.origin) + `;` +
		`if(window.location.origin!==expected){` +
		`try{delete window.faOpenExternal;delete window.faChangeWorkspace;delete window.faSelectFolder;delete window.faSelectFile;delete window.faSetBusy;delete window.faAttention;}catch(e){}` +
		`try{if(window.external){window.external.invoke=function(){return Promise.reject("external page");};}}catch(e){}` +
		`}})();`
	w.view.Init(script)
}

func (w *desktopWebView) checkSource() error {
	source, err := webViewSource(w.view)
	if err != nil {
		return err
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme+"://"+parsed.Host != w.origin {
		return errors.New("desktop binding called from an unexpected page")
	}
	return nil
}

func (w *desktopWebView) Run() {
	w.view.Run()
}

func (w *desktopWebView) hwnd() windows.Handle {
	return windows.Handle(uintptr(w.view.Window()))
}

func (w *desktopWebView) applyState(state desktopState) {
	// A saved state always records the engine port, so equality with the
	// defaults means there is nothing saved yet: size for the actual monitor.
	if state == defaultDesktopState() || !validDesktopState(state) || !isWindowVisibleOnMonitor(state) {
		state = defaultWindowPlacement(w.hwnd())
	}
	if err := setWindowPlacement(w.hwnd(), state); err != nil {
		log.Printf("restore desktop window: %v", err)
	}
}

func (w *desktopWebView) updateDarkTitleBar(dark bool) {
	if w == nil || w.darkTitleFailed.Load() {
		return
	}
	if err := setDarkTitleBar(w.hwnd(), dark); err != nil &&
		w.darkTitleFailed.CompareAndSwap(false, true) {
		log.Printf("dark title bar unavailable: %v", err)
	}
}

func (w *desktopWebView) navigate(address string) {
	if w == nil || w.view == nil {
		return
	}
	w.view.Dispatch(func() {
		if w.view != nil {
			w.view.Navigate(address)
		}
	})
}

func (w *desktopWebView) Close() {
	if w == nil || w.view == nil {
		return
	}
	if w.subclass != nil {
		w.subclass.allowWindowClose()
		w.subclass.restore()
	}
	w.view.Destroy()
	w.view = nil
}

func (w *desktopWebView) onCloseForError() {
	if w != nil && w.subclass != nil {
		w.subclass.allowWindowClose()
	}
}

func jsString(value string) string {
	return strconv.Quote(value)
}

type coreWebViewLayout struct {
	Vtbl *coreWebViewVtbl
}

type coreWebViewVtbl struct {
	Unknown     [3]edge.ComProc
	GetSettings edge.ComProc
	GetSource   edge.ComProc
}

func webViewSource(view webview2.WebView) (string, error) {
	raw := reflect.ValueOf(view)
	if raw.Kind() != reflect.Pointer || raw.IsNil() {
		return "", errors.New("WebView2 source is unavailable")
	}
	raw = raw.Elem()
	browserField := raw.FieldByName("browser")
	if !browserField.IsValid() {
		return "", errors.New("WebView2 browser is unavailable")
	}
	browserField = reflect.NewAt(browserField.Type(), unsafe.Pointer(browserField.UnsafeAddr())).Elem()
	browser := reflect.ValueOf(browserField.Interface())
	if browser.Kind() != reflect.Pointer || browser.IsNil() {
		return "", errors.New("WebView2 browser is unavailable")
	}
	browser = browser.Elem()
	webviewField := browser.FieldByName("webview")
	if !webviewField.IsValid() {
		return "", errors.New("WebView2 source is unavailable")
	}
	webviewField = reflect.NewAt(webviewField.Type(), unsafe.Pointer(webviewField.UnsafeAddr())).Elem()
	core, ok := webviewField.Interface().(*edge.ICoreWebView2)
	if !ok || core == nil {
		return "", errors.New("WebView2 source is unavailable")
	}
	layout := (*coreWebViewLayout)(unsafe.Pointer(core))
	var source *uint16
	_, _, callErr := layout.Vtbl.GetSource.Call(uintptr(unsafe.Pointer(core)), uintptr(unsafe.Pointer(&source)))
	if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
		return "", callErr
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(source))
	return windows.UTF16PtrToString(source), nil
}

func shellOpen(raw string) error {
	target, err := windows.UTF16PtrFromString(raw)
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}

func init() {
	runtime.LockOSThread()
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}
