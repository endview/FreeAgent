param(
    [Parameter(Mandatory = $true)]
    [string]$Package
)

$ErrorActionPreference = 'Stop'
$Package = (Resolve-Path -LiteralPath $Package).Path
$desktop = Join-Path $Package 'FreeAgent.exe'
$cli = Join-Path $Package 'bin/freeagent.exe'
$data = Join-Path $Package 'data/Harness-v5'
$configPath = Join-Path $data 'config.json'
if ((Test-Path -LiteralPath (Join-Path $Package 'data')) -or
    (Test-Path -LiteralPath (Join-Path $Package 'workspace'))) {
    throw 'Desktop startup regression requires a freshly extracted package without data or workspace.'
}

if (-not ('FreeAgentStartupWindow' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class FreeAgentStartupWindow {
    private delegate bool EnumProc(IntPtr window, IntPtr parameter);
    [DllImport("user32.dll")] private static extern bool EnumWindows(EnumProc callback, IntPtr parameter);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] private static extern IntPtr GetProp(IntPtr window, string name);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window, uint message, IntPtr w, IntPtr l);
    public static IntPtr Find(uint pid) {
        IntPtr found = IntPtr.Zero;
        EnumWindows((window, parameter) => {
            uint owner;
            GetWindowThreadProcessId(window, out owner);
            if (owner == pid && IsWindowVisible(window) && GetProp(window, "FreeAgentDesktop") != IntPtr.Zero) {
                found = window;
                return false;
            }
            return true;
        }, IntPtr.Zero);
        return found;
    }
}
'@
}

function Get-PackageProcesses {
    @(Get-CimInstance Win32_Process -Filter "Name = 'FreeAgent.exe'" | Where-Object {
        $_.ExecutablePath -eq $desktop -or $_.ExecutablePath -eq $cli
    })
}

$previousBrowser = $env:FREEAGENT_DESKTOP_FORCE_BROWSER
$previousKeys = $env:FREEAGENT_KEYS_FILE
$desktopProcess = $null
$configDigest = $null
try {
    Remove-Item Env:FREEAGENT_DESKTOP_FORCE_BROWSER -ErrorAction SilentlyContinue
    Remove-Item Env:FREEAGENT_KEYS_FILE -ErrorAction SilentlyContinue
    foreach ($iteration in 1..2) {
        # No arguments and no CLI pre-initialization: the actual desktop entry point.
        $start = [Diagnostics.ProcessStartInfo]::new()
        $start.FileName = $desktop
        $start.WorkingDirectory = [IO.Path]::GetTempPath()
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        # Explorer-launched processes inherit hidden per-drive entries such as
        # "=C:=C:\..."; PowerShell children do not, so add them explicitly.
        $start.Environment['=C:'] = [IO.Path]::GetTempPath().TrimEnd('\')
        $start.Environment['=::'] = '::\'
        $desktopProcess = [Diagnostics.Process]::Start($start)
        $deadline = [DateTime]::UtcNow.AddSeconds(45)
        $window = [IntPtr]::Zero
        while ([DateTime]::UtcNow -lt $deadline) {
            if ($desktopProcess.HasExited) { throw "Desktop exited before its window was ready (launch $iteration)." }
            $window = [FreeAgentStartupWindow]::Find($desktopProcess.Id)
            if ($window -ne [IntPtr]::Zero -and (Test-Path -LiteralPath $configPath)) { break }
            Start-Sleep -Milliseconds 100
        }
        if ($window -eq [IntPtr]::Zero -or -not (Test-Path -LiteralPath $configPath)) {
            throw "Desktop did not initialize configuration and create its main window (launch $iteration)."
        }
        $config = Get-Content -LiteralPath $configPath -Raw | ConvertFrom-Json
        if ($config.workspace -ne (Join-Path $Package 'workspace') -or
            @($config.models.PSObject.Properties).Count -ne 0) {
            throw 'Desktop first launch did not retain the portable workspace and zero-model defaults.'
        }
        $digest = (Get-FileHash -LiteralPath $configPath -Algorithm SHA256).Hash
        if ($iteration -eq 1) { $configDigest = $digest }
        elseif ($digest -ne $configDigest) { throw 'Reopening the desktop changed its configuration.' }

        $processes = @(Get-PackageProcesses)
        $engines = @($processes | Where-Object {
            $_.ExecutablePath -eq $cli -and $_.ParentProcessId -eq $desktopProcess.Id
        })
        if ($engines.Count -ne 1 -or @($processes | Where-Object { $_.ExecutablePath -eq $desktop }).Count -ne 1) {
            throw 'Expected one desktop process with exactly one bin/freeagent.exe engine.'
        }
        $engineId = $engines[0].ProcessId
        $listeners = @(Get-NetTCPConnection -State Listen -OwningProcess $engineId -ErrorAction SilentlyContinue |
            Where-Object { $_.LocalAddress -eq '127.0.0.1' })
        if ($listeners.Count -ne 1) { throw 'Desktop engine has no unique loopback HTTP listener.' }
        $page = Invoke-WebRequest -Uri "http://127.0.0.1:$($listeners[0].LocalPort)/" -UseBasicParsing -TimeoutSec 5
        if ($page.StatusCode -ne 200 -or $page.Content -notmatch '<script' -or
            -not (Test-Path -LiteralPath (Join-Path $Package 'data/engine.log'))) {
            throw 'Desktop engine did not serve the embedded frontend.'
        }

        $null = [FreeAgentStartupWindow]::PostMessage($window, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)
        if (-not $desktopProcess.WaitForExit(15000) -or $desktopProcess.ExitCode -ne 0) {
            throw 'Desktop did not exit cleanly after closing its idle main window.'
        }
        $desktopProcess.Dispose()
        $desktopProcess = $null
        $deadline = [DateTime]::UtcNow.AddSeconds(10)
        while (@(Get-PackageProcesses).Count -ne 0 -and [DateTime]::UtcNow -lt $deadline) {
            Start-Sleep -Milliseconds 100
        }
        if (@(Get-PackageProcesses).Count -ne 0) { throw 'Desktop left a package process running after close.' }
    }
    [pscustomobject]@{
        ok = $true
        fresh_desktop_launch = $true
        desktop_reopen = $true
        desktop_engine_path = $true
        desktop_http = $true
        desktop_close = $true
    }
} finally {
    # Only processes whose executable belongs to this fresh test package.
    foreach ($process in @(Get-PackageProcesses)) {
        Stop-Process -Id $process.ProcessId -Force -ErrorAction SilentlyContinue
    }
    if ($null -ne $desktopProcess) { $desktopProcess.Dispose() }
    if ($null -eq $previousBrowser) {
        Remove-Item Env:FREEAGENT_DESKTOP_FORCE_BROWSER -ErrorAction SilentlyContinue
    } else { $env:FREEAGENT_DESKTOP_FORCE_BROWSER = $previousBrowser }
    if ($null -eq $previousKeys) {
        Remove-Item Env:FREEAGENT_KEYS_FILE -ErrorAction SilentlyContinue
    } else { $env:FREEAGENT_KEYS_FILE = $previousKeys }
}
