package browser

import (
	"errors"
	"golang.org/x/sys/windows"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_SUSPENDED}
}

func systemBrowserLocations() []string {
	var result []string
	for _, folder := range []*windows.KNOWNFOLDERID{windows.FOLDERID_ProgramFiles, windows.FOLDERID_ProgramFilesX86, windows.FOLDERID_LocalAppData} {
		if base, err := windows.KnownFolderPath(folder, 0); err == nil {
			result = append(result, filepath.Join(base, "Google/Chrome/Application/chrome.exe"), filepath.Join(base, "Microsoft/Edge/Application/msedge.exe"))
		}
	}
	return result
}

func bindProcessTree(cmd *exec.Cmd) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	// The initial thread is still suspended: attach before any launcher can
	// create children and exit. Closing the Job cleans all startup failures.
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := false
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID == uint32(cmd.Process.Pid) {
			thread, e := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if e != nil {
				windows.CloseHandle(job)
				return nil, e
			}
			_, e = windows.ResumeThread(thread)
			windows.CloseHandle(thread)
			if e != nil {
				windows.CloseHandle(job)
				return nil, e
			}
			resumed = true
			break
		}
	}
	if !resumed {
		windows.CloseHandle(job)
		return nil, errors.New("browser initial thread is unavailable")
	}
	return func() { _ = windows.TerminateJobObject(job, 1); _ = windows.CloseHandle(job) }, nil
}
