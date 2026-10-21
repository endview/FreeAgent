//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/endview/freeagent/internal/apppaths"
	"github.com/endview/freeagent/internal/handoff"
	"golang.org/x/sys/windows"
)

const (
	instanceWindowProperty = "FreeAgentDesktop"
	instanceWait           = 3 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runDesktop(ctx); err != nil {
		showMessage(err.Error(), "FreeAgent")
	}
}

func runDesktop(ctx context.Context) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate desktop executable: %w", err)
	}
	dataDir, err := apppaths.DataDir()
	if err != nil {
		return err
	}
	instance, owner, err := acquireInstance(dataDir)
	if err != nil {
		return err
	}
	if !owner {
		if window, ok := waitForWindow(instanceWait); ok {
			activateWindow(window)
		} else {
			showMessage("FreeAgent 已经在运行，但没有找到可激活的窗口。请检查任务栏中的 FreeAgent。", "FreeAgent")
		}
		return nil
	}
	defer func() {
		_ = windows.ReleaseMutex(instance)
		_ = windows.CloseHandle(instance)
	}()

	logDir, err := apppaths.LogDir()
	if err != nil {
		return err
	}
	if err := ensureWritableDirectory(logDir); err != nil {
		return fmt.Errorf("desktop data directory: %w", err)
	}
	logPath := filepath.Join(logDir, "desktop.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("desktop log: %w", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	enginePath, err := engineExecutable(executable)
	if err != nil {
		return err
	}
	workspace, err := apppaths.DefaultWorkspace()
	if err != nil {
		return err
	}
	needs, err := needsInit(dataDir)
	if err != nil {
		return fmt.Errorf("check Harness data: %w", err)
	}
	if needs {
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return fmt.Errorf("create default workspace: %w", err)
		}
		_, stderr, code, runErr := runCLI(ctx, enginePath, initCLIArgs(workspace, dataDir))
		if runErr != nil || code != 0 {
			detail := lastLines(stderr, 20)
			if detail == "" {
				detail = "初始化没有返回错误详情。"
			}
			showMessage(fmt.Sprintf("FreeAgent 初始化失败。\n\n%s\n\n日志：%s", detail, logPath), "FreeAgent")
			return nil
		}
	}

	var config configSnapshot
	attached := false
	var attachedLaunch engineLaunch
	if info, _, chatSecret, discoveryErr := handoff.Read(dataDir); discoveryErr == nil {
		attachedLaunch, discoveryErr = discoveryLaunch(info, chatSecret)
		if discoveryErr == nil && !portAvailable(attachedLaunch.Port) {
			attached = true
			config.Workspace = info.Workspace
		}
	}
	if !attached {
		config, err = readConfigShow(ctx, enginePath, dataDir)
	}
	if !attached && err != nil {
		return fmt.Errorf("read Harness configuration: %w", err)
	}
	desktopStatePath, err := apppaths.DesktopState()
	if err != nil {
		return err
	}
	state, stateErr := loadDesktopState(desktopStatePath)
	if stateErr != nil {
		log.Printf("desktop state reset: %v", stateErr)
	}
	webviewData, err := apppaths.WebView2Dir()
	if err != nil {
		return err
	}
	if err := ensureWritableDirectory(webviewData); err != nil {
		return fmt.Errorf("WebView2 data directory: %w", err)
	}

	forceBrowser := os.Getenv("FREEAGENT_DESKTOP_FORCE_BROWSER") == "1"
	engineOptions := engineOptions{
		Executable:  executable,
		EnginePath:  enginePath,
		DataDir:     dataDir,
		Port:        pickPort(state.LastPort, portAvailable),
		OpenBrowser: forceBrowser,
		LogDir:      logDir,
	}
	var engine *engineProcess
	var launch engineLaunch
	if attached {
		launch = attachedLaunch
	} else {
		engine, launch, err = startEngineWithRetry(ctx, engineOptions)
		if err != nil {
			return err
		}
	}

	var engineMu sync.Mutex
	currentEngine := engine
	currentLaunch := launch
	changeCtx, cancelChange := context.WithCancel(ctx)
	defer cancelChange()
	var closing atomic.Bool
	var view *desktopWebView

	closeEngine := func() {
		engineMu.Lock()
		process := currentEngine
		engineMu.Unlock()
		if process != nil {
			if err := process.closeWithTimeout(engineShutdownTimeout); err != nil {
				log.Printf("engine shutdown: %v", err)
			}
		}
	}
	beginClose := func(system bool) {
		if !closing.CompareAndSwap(false, true) {
			return
		}
		if !system && view != nil && view.busy.Load() {
			if !confirmClose(view.hwnd(),
				"有任务正在运行。\n\n确定：停止任务并退出。停止前已经执行的操作不会撤销，可能已经产生作用。\n取消：返回 FreeAgent。",
				"退出 FreeAgent") {
				closing.Store(false)
				return
			}
		}
		cancelChange()
		if view != nil {
			saved := currentWindowState(view.hwnd(), currentLaunch.Port)
			if err := saveDesktopState(desktopStatePath, saved); err != nil {
				log.Printf("save desktop state: %v", err)
			}
			hideWindow(view.hwnd())
		}
		closeEngine()
		if view != nil && !system && view.subclass != nil {
			view.subclass.allowWindowClose()
		}
	}

	if forceBrowser {
		_ = shellOpen(launch.Address)
		if engine != nil {
			return waitForEngine(ctx, engine)
		}
		<-ctx.Done()
		return nil
	}

	view, err = newDesktopWebView(launch, webviewData, config.Workspace, func() {
		beginClose(false)
	})
	if err != nil {
		log.Printf("WebView2 unavailable: %v", err)
		_ = shellOpen(launch.Address)
		showMessage(fmt.Sprintf("桌面界面不可用，已在默认浏览器打开 FreeAgent。\n\n%s\n\n关闭此提示不会停止任务；请通过任务管理器结束桌面进程。", err), "FreeAgent")
		return waitForEngine(ctx, engine)
	}
	defer view.Close()
	if err := setWindowProperty(view.hwnd(), instanceWindowProperty); err != nil {
		return fmt.Errorf("register desktop window: %w", err)
	}
	defer removeWindowProperty(view.hwnd(), instanceWindowProperty)
	view.applyState(state)
	view.attention = func() {
		foreground, _, _ := procGetForegroundWindow.Call()
		if windows.Handle(foreground) != view.hwnd() {
			flashWindow(view.hwnd())
		}
	}
	view.workspaceChange = func(next string) (map[string]any, error) {
		if closing.Load() {
			return nil, errors.New("FreeAgent 正在退出")
		}
		if attached {
			if !confirmClose(view.hwnd(), "确认让当前 owner 切换工作区？\n\n切换只影响后续运行；当前运行快照不会被改写。", "切换工作区") {
				return map[string]any{"changed": false}, nil
			}
			go changeAttachedWorkspace(changeCtx, launch, view, next, &closing)
			return map[string]any{"changed": true}, nil
		}
		previousWorkspace := view.workspace
		go changeWorkspace(changeCtx, enginePath, dataDir, logDir, view, next,
			&engineMu, &currentEngine, &currentLaunch, &closing, previousWorkspace)
		return map[string]any{"changed": true}, nil
	}
	view.onShutdown = func() {
		beginClose(true)
	}
	view.Run()
	return nil
}

func changeAttachedWorkspace(ctx context.Context, launch engineLaunch, view *desktopWebView, workspace string, closing *atomic.Bool) {
	request := func(method, path string, body any, output any) error {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, launch.Origin+path, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("X-FreeAgent-Key", launch.Token)
		req.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return fmt.Errorf("owner returned HTTP %d", response.StatusCode)
		}
		return json.NewDecoder(response.Body).Decode(output)
	}
	var operation struct {
		ID string `json:"operation_id"`
	}
	err := request(http.MethodPost, "/api/control/plan", map[string]any{"kind": "config.set_workspace", "arguments": map[string]string{"workspace": workspace}}, &operation)
	if err == nil {
		var confirmation struct {
			Code string `json:"code"`
		}
		err = request(http.MethodGet, "/api/control/confirmation?operation_id="+url.QueryEscape(operation.ID), nil, &confirmation)
		if err == nil {
			var result map[string]any
			err = request(http.MethodPost, "/api/control/confirm", map[string]string{"operation_id": operation.ID, "code": confirmation.Code}, &result)
		}
	}
	view.view.Dispatch(func() {
		if closing.Load() {
			return
		}
		view.changing.Store(false)
		if err != nil {
			showMessage(fmt.Sprintf("工作区切换失败。\n\n%s", err), "FreeAgent")
			return
		}
		view.workspace = workspace
		view.view.Navigate(launch.Address)
	})
}

type configSnapshot struct {
	Workspace string `json:"workspace"`
}

func discoveryLaunch(info handoff.EngineInfo, secret string) (engineLaunch, error) {
	return parseEngineFirstLine(strings.TrimRight(info.Origin, "/") + "/#key=" + secret)
}

func readConfigShow(ctx context.Context, enginePath, dataDir string) (configSnapshot, error) {
	stdout, stderr, code, err := runCLI(ctx, enginePath, []string{"config", "show", "--data-dir", dataDir})
	if err != nil {
		return configSnapshot{}, err
	}
	if code != 0 {
		detail := lastLines(stderr, 20)
		if detail == "" {
			detail = "config show 失败。"
		}
		return configSnapshot{}, errors.New(detail)
	}
	var config configSnapshot
	if err := json.Unmarshal([]byte(stdout), &config); err != nil {
		return configSnapshot{}, fmt.Errorf("parse config show: %w", err)
	}
	if strings.TrimSpace(config.Workspace) == "" {
		return configSnapshot{}, errors.New("config show returned an empty workspace")
	}
	return config, nil
}

func changeWorkspace(ctx context.Context, enginePath, dataDir, logDir string, view *desktopWebView,
	next string, engineMu *sync.Mutex, currentEngine **engineProcess, currentLaunch *engineLaunch,
	closing *atomic.Bool, previousWorkspace string) {
	engineMu.Lock()
	old := *currentEngine
	oldLaunch := *currentLaunch
	engineMu.Unlock()
	if old != nil {
		_ = old.closeWithTimeout(engineShutdownTimeout)
	}
	stdout, stderr, code, cliErr := runCLI(ctx, enginePath, workspaceCLIArgs(next, dataDir))
	_ = stdout
	if ctx.Err() != nil || closing.Load() {
		return
	}
	options := engineOptions{
		EnginePath: enginePath,
		DataDir:    dataDir,
		Port:       pickPort(oldLaunch.Port, portAvailable),
		LogDir:     logDir,
	}
	restarted, launch, startErr := startEngineWithRetry(ctx, options)
	if startErr != nil {
		view.view.Dispatch(func() {
			if !closing.Load() {
				showMessage(fmt.Sprintf("工作区切换后无法重新启动引擎。\n\n%s", startErr), "FreeAgent")
				view.onCloseForError()
			}
		})
		return
	}
	engineMu.Lock()
	if closing.Load() {
		engineMu.Unlock()
		_ = restarted.closeWithTimeout(engineShutdownTimeout)
		return
	}
	*currentEngine = restarted
	*currentLaunch = launch
	engineMu.Unlock()

	view.view.Dispatch(func() {
		if closing.Load() {
			return
		}
		if cliErr != nil || code != 0 {
			detail := lastLines(stderr, 20)
			if detail == "" {
				detail = "工作区配置没有改变。"
			}
			view.changing.Store(false)
			view.view.Navigate(launch.Address)
			showMessage(fmt.Sprintf("工作区没有更换。\n\n%s\n\n当前工作区仍为：%s", detail, previousWorkspace), "FreeAgent")
			return
		}
		view.workspace = next
		view.origin = launch.Origin
		view.changing.Store(false)
		if err := view.bind(); err != nil {
			showMessage(fmt.Sprintf("工作区已切换，但桌面桥接重新建立失败：%s", err), "FreeAgent")
			view.onCloseForError()
			return
		}
		view.initGuard()
		view.view.Navigate(launch.Address)
	})
}

func startEngineWithRetry(ctx context.Context, options engineOptions) (*engineProcess, engineLaunch, error) {
	engine, launch, err := startEngine(ctx, options)
	if err == nil || options.Port == 0 {
		return engine, launch, err
	}
	options.Port = 0
	return startEngine(ctx, options)
}

func waitForEngine(ctx context.Context, engine *engineProcess) error {
	select {
	case err := <-engine.wait():
		engine.finish()
		return normalizeProcessExit(err)
	case <-ctx.Done():
		return engine.closeWithTimeout(engineShutdownTimeout)
	}
}

func portAvailable(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

func acquireInstance(dataDir string) (windows.Handle, bool, error) {
	name, err := instanceName(dataDir)
	if err != nil {
		return 0, false, err
	}
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, false, err
	}
	handle, err := windows.CreateMutex(nil, true, ptr)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(handle)
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return handle, true, nil
}

func waitForWindow(timeout time.Duration) (windows.Handle, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if hwnd, ok := findWindowByProperty(instanceWindowProperty); ok {
			return hwnd, true
		}
		if time.Now().After(deadline) {
			return 0, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func lastLines(value string, count int) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
