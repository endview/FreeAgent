package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/endview/freeagent/internal/apppaths"
	"golang.org/x/sys/windows"
)

// launchedFromExplorer reports a console that exists only for this process,
// which is what a double-click in Explorer creates. It closes when we exit.
func launchedFromExplorer() bool {
	var mode uint32
	if windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) != nil {
		return false
	}
	var processes [2]uint32
	count, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleProcessList").
		Call(uintptr(unsafe.Pointer(&processes[0])), uintptr(len(processes)))
	return count == 1 && processes[0] == uint32(os.Getpid())
}

// doubleClickHint tells someone who double-clicked the command-line tool in a
// portable package where the app is.
func doubleClickHint(root string) string {
	return "This is the FreeAgent command-line tool.\n" +
		"To open the app, double-click " + filepath.Join(root, "FreeAgent.exe") + "\n" +
		"To use the command line, run freeagent --help in a terminal.\n" +
		"Press Enter to close this window.\n"
}

// showDoubleClickHint handles a double-click on freeagent.exe inside a
// portable package: it shows the hint instead of a terminal chat. Run from an
// existing terminal, freeagent starts the chat as usual.
func showDoubleClickHint(args []string) (bool, error) {
	if len(args) != 0 || !launchedFromExplorer() {
		return false, nil
	}
	root, portable, err := apppaths.PackageRoot()
	if err != nil || !portable {
		return false, err
	}
	_, _ = fmt.Fprint(os.Stderr, doubleClickHint(root))
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	return true, nil
}

func keepLaunchErrorVisible() {
	if !launchedFromExplorer() {
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, "\nFreeAgent could not start. Extract the complete portable ZIP and open FreeAgent.exe in the package folder.\nPress Enter to close this window.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func openBrowser(address string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(address)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL)
}
