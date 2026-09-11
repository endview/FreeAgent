[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ExecutablePath,
    [Parameter(Mandatory = $true)]
    [AllowEmptyCollection()]
    [string[]]$ArgumentList,
    [Parameter(Mandatory = $true)][string]$WorkingDirectory,
    [Parameter(Mandatory = $true)][string]$OutputRoot,
    [Parameter(Mandatory = $true)][string]$StandardOutputPath,
    [Parameter(Mandatory = $true)][string]$StandardErrorPath,
    [Parameter(Mandatory = $true)][string]$PrivateTempRoot,
    [ValidateRange(1, 7800)]
    [int]$TimeoutSeconds = 1800,
    [ValidateRange(1, 268435456)]
    [int64]$MaximumStandardOutputBytes = 67108864,
    [ValidateRange(1, 67108864)]
    [int64]$MaximumStandardErrorBytes = 8388608,
    [hashtable]$EnvironmentOverrides = @{}
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:IsWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:PathComparison = if ($script:IsWindowsPlatform) {
    [StringComparison]::OrdinalIgnoreCase
} else {
    [StringComparison]::Ordinal
}
$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:MaximumBootstrapBytes = 8388608

function Fail-CIReleaseCommand {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw "CI_RELEASE_COMMAND_FAIL code=$Code"
}

if ($null -eq ('CIReleaseCommandBoundedIO' -as [type])) {
    try {
        Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Threading.Tasks;

public sealed class CIReleaseCommandCaptureResult
{
    public byte[] Bytes { get; set; }
    public bool Exceeded { get; set; }
    public long TotalBytes { get; set; }
}

public static class CIReleaseCommandBoundedIO
{
    public static async Task<CIReleaseCommandCaptureResult> CaptureAsync(
        Stream source,
        long maximumBytes)
    {
        if (source == null) throw new ArgumentNullException("source");
        if (maximumBytes < 0) throw new ArgumentOutOfRangeException("maximumBytes");
        var memory = new MemoryStream();
        var buffer = new byte[65536];
        long total = 0;
        bool exceeded = false;
        try
        {
            int read;
            while ((read = await source.ReadAsync(buffer, 0, buffer.Length)
                .ConfigureAwait(false)) > 0)
            {
                total += read;
                if (!exceeded && memory.Length + read <= maximumBytes)
                {
                    memory.Write(buffer, 0, read);
                }
                else
                {
                    exceeded = true;
                }
            }
            return new CIReleaseCommandCaptureResult
            {
                Bytes = memory.ToArray(),
                Exceeded = exceeded,
                TotalBytes = total
            };
        }
        finally
        {
            memory.Dispose();
        }
    }

    public static async Task WriteAndCloseAsync(
        Stream destination,
        byte[] bytes)
    {
        if (destination == null) throw new ArgumentNullException("destination");
        if (bytes == null) throw new ArgumentNullException("bytes");
        Exception primary = null;
        try
        {
            await destination.WriteAsync(bytes, 0, bytes.Length)
                .ConfigureAwait(false);
            await destination.FlushAsync().ConfigureAwait(false);
        }
        catch (Exception ex)
        {
            primary = ex;
        }
        try
        {
            destination.Close();
        }
        catch (Exception ex)
        {
            if (primary == null) primary = ex;
        }
        if (primary != null) throw primary;
    }
}

public static class CIReleaseCommandProcessTree
{
    private const UInt32 JobObjectExtendedLimitInformation = 9;
    private const UInt32 JobObjectLimitKillOnJobClose = 0x00002000;

    [StructLayout(LayoutKind.Sequential)]
    private struct JobObjectBasicLimitInformation
    {
        public Int64 PerProcessUserTimeLimit;
        public Int64 PerJobUserTimeLimit;
        public UInt32 LimitFlags;
        public UIntPtr MinimumWorkingSetSize;
        public UIntPtr MaximumWorkingSetSize;
        public UInt32 ActiveProcessLimit;
        public Int64 Affinity;
        public UInt32 PriorityClass;
        public UInt32 SchedulingClass;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct IoCounters
    {
        public UInt64 ReadOperationCount;
        public UInt64 WriteOperationCount;
        public UInt64 OtherOperationCount;
        public UInt64 ReadTransferCount;
        public UInt64 WriteTransferCount;
        public UInt64 OtherTransferCount;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct JobObjectExtendedLimitInformationValue
    {
        public JobObjectBasicLimitInformation BasicLimitInformation;
        public IoCounters IoInfo;
        public UIntPtr ProcessMemoryLimit;
        public UIntPtr JobMemoryLimit;
        public UIntPtr PeakProcessMemoryUsed;
        public UIntPtr PeakJobMemoryUsed;
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CreateJobObject(
        IntPtr securityAttributes,
        string name);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool SetInformationJobObject(
        IntPtr job,
        UInt32 informationClass,
        IntPtr information,
        UInt32 informationLength);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool AssignProcessToJobObject(
        IntPtr job,
        IntPtr process);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool TerminateJobObject(
        IntPtr job,
        UInt32 exitCode);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern bool CloseHandle(IntPtr handle);

    public static IntPtr CreateKillOnCloseJob(Process process)
    {
        if (process == null) throw new ArgumentNullException("process");
        IntPtr job = CreateJobObject(IntPtr.Zero, null);
        if (job == IntPtr.Zero)
            throw new Win32Exception(Marshal.GetLastWin32Error());

        IntPtr information = IntPtr.Zero;
        bool assigned = false;
        try
        {
            var value = new JobObjectExtendedLimitInformationValue();
            value.BasicLimitInformation.LimitFlags =
                JobObjectLimitKillOnJobClose;
            int size = Marshal.SizeOf(value);
            information = Marshal.AllocHGlobal(size);
            Marshal.StructureToPtr(value, information, false);
            if (!SetInformationJobObject(
                job,
                JobObjectExtendedLimitInformation,
                information,
                (UInt32)size))
            {
                throw new Win32Exception(Marshal.GetLastWin32Error());
            }
            if (!AssignProcessToJobObject(job, process.Handle))
            {
                throw new Win32Exception(Marshal.GetLastWin32Error());
            }
            assigned = true;
            return job;
        }
        finally
        {
            if (information != IntPtr.Zero)
                Marshal.FreeHGlobal(information);
            if (!assigned)
                CloseHandle(job);
        }
    }

    public static void Terminate(IntPtr job)
    {
        if (job == IntPtr.Zero) return;
        if (!TerminateJobObject(job, 1))
            throw new Win32Exception(Marshal.GetLastWin32Error());
    }

    public static void Close(IntPtr job)
    {
        if (job == IntPtr.Zero) return;
        if (!CloseHandle(job))
            throw new Win32Exception(Marshal.GetLastWin32Error());
    }
}
'@ -ErrorAction Stop
    } catch {
        Fail-CIReleaseCommand -Code 'CIC_CAPTURE_RUNTIME_UNAVAILABLE'
    }
}

function Get-CICAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value)) {
        Fail-CIReleaseCommand -Code $Code
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) {
            Fail-CIReleaseCommand -Code $Code
        }
    }
    $isAbsolute = $false
    if ($script:IsWindowsPlatform) {
        $probe = $Value.Replace([char]47, [char]92)
        $device = $probe.StartsWith('\\?\', [StringComparison]::Ordinal) -or
            $probe.StartsWith('\\.\', [StringComparison]::Ordinal) -or
            $probe.StartsWith('\??\', [StringComparison]::Ordinal)
        $isAbsolute = -not $device -and $Value -match '^[A-Za-z]:[\\/]'
    } else {
        $isAbsolute = $Value.StartsWith('/', [StringComparison]::Ordinal)
    }
    if (-not $isAbsolute) {
        Fail-CIReleaseCommand -Code $Code
    }
    try {
        $full = [IO.Path]::GetFullPath($Value)
    } catch {
        Fail-CIReleaseCommand -Code $Code
    }
    $root = [IO.Path]::GetPathRoot($full)
    while ($full.Length -gt $root.Length -and
        ($full.EndsWith(
            [IO.Path]::DirectorySeparatorChar.ToString(),
            [StringComparison]::Ordinal
        ) -or
        $full.EndsWith(
            [IO.Path]::AltDirectorySeparatorChar.ToString(),
            [StringComparison]::Ordinal
        ))) {
        $full = $full.Substring(0, $full.Length - 1)
    }
    return $full
}

function Test-CICPathContains {
    param(
        [Parameter(Mandatory = $true)][string]$Parent,
        [Parameter(Mandatory = $true)][string]$Candidate,
        [switch]$AllowEqual
    )
    if ([string]::Equals($Parent, $Candidate, $script:PathComparison)) {
        return [bool]$AllowEqual
    }
    $prefix = $Parent
    if (-not $prefix.EndsWith(
        [IO.Path]::DirectorySeparatorChar.ToString(),
        [StringComparison]::Ordinal
    )) {
        $prefix += [IO.Path]::DirectorySeparatorChar
    }
    return $Candidate.StartsWith($prefix, $script:PathComparison)
}

function Assert-CICNoReparseAncestry {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $cursor = $Path
    while (-not [string]::IsNullOrWhiteSpace($cursor)) {
        if (Test-Path -LiteralPath $cursor) {
            try {
                $item = Get-Item -LiteralPath $cursor -Force
            } catch {
                Fail-CIReleaseCommand -Code $Code
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-CIReleaseCommand -Code $Code
            }
            $linkType = $item.PSObject.Properties['LinkType']
            if ($null -ne $linkType -and
                -not [string]::IsNullOrWhiteSpace([string]$linkType.Value) -and
                [string]$linkType.Value -cne 'HardLink') {
                Fail-CIReleaseCommand -Code $Code
            }
        }
        $parent = [IO.Path]::GetDirectoryName($cursor)
        if ([string]::IsNullOrWhiteSpace($parent) -or
            [string]::Equals($parent, $cursor, $script:PathComparison)) {
            break
        }
        $cursor = $parent
    }
}

function Stop-CICProcessNoThrow {
    param(
        [Parameter(Mandatory = $true)][Diagnostics.Process]$Process,
        [IntPtr]$JobHandle = [IntPtr]::Zero
    )
    if ($script:IsWindowsPlatform -and $JobHandle -ne [IntPtr]::Zero) {
        try {
            [CIReleaseCommandProcessTree]::Terminate($JobHandle)
        } catch {}
    }
    try {
        if (-not $Process.HasExited) {
            try {
                $Process.Kill($true)
            } catch {
                $Process.Kill()
            }
        }
    } catch {}
    foreach ($stream in @(
        { $Process.StandardInput.BaseStream },
        { $Process.StandardOutput.BaseStream },
        { $Process.StandardError.BaseStream }
    )) {
        try { (& $stream).Dispose() } catch {}
    }
}

function Get-CICRemainingMilliseconds {
    param(
        [Parameter(Mandatory = $true)][Diagnostics.Stopwatch]$Stopwatch,
        [Parameter(Mandatory = $true)][int64]$DeadlineMilliseconds
    )
    $remaining = $DeadlineMilliseconds - $Stopwatch.ElapsedMilliseconds
    if ($remaining -le 0) { return 0 }
    if ($remaining -gt [int]::MaxValue) { return [int]::MaxValue }
    return [int]$remaining
}

function Wait-CICTask {
    param(
        [Parameter(Mandatory = $true)][Threading.Tasks.Task]$Task,
        [Parameter(Mandatory = $true)][Diagnostics.Process]$Process,
        [Parameter(Mandatory = $true)][Diagnostics.Stopwatch]$Stopwatch,
        [Parameter(Mandatory = $true)][int64]$DeadlineMilliseconds,
        [Parameter(Mandatory = $true)][string]$Code,
        [IntPtr]$JobHandle = [IntPtr]::Zero
    )
    $remaining = Get-CICRemainingMilliseconds `
        -Stopwatch $Stopwatch `
        -DeadlineMilliseconds $DeadlineMilliseconds
    if ($remaining -le 0) {
        Stop-CICProcessNoThrow -Process $Process -JobHandle $JobHandle
        Fail-CIReleaseCommand -Code 'CIC_TIMEOUT'
    }
    try {
        $completed = $Task.Wait($remaining)
    } catch {
        Stop-CICProcessNoThrow -Process $Process -JobHandle $JobHandle
        Fail-CIReleaseCommand -Code $Code
    }
    if (-not $completed -or $Task.IsCanceled -or $Task.IsFaulted) {
        Stop-CICProcessNoThrow -Process $Process -JobHandle $JobHandle
        Fail-CIReleaseCommand -Code $Code
    }
}

function Remove-CICSensitiveEnvironment {
    param([Parameter(Mandatory = $true)][Diagnostics.ProcessStartInfo]$StartInfo)
    foreach ($key in @($StartInfo.EnvironmentVariables.Keys)) {
        $name = [string]$key
        if ($name.StartsWith('GITHUB_', [StringComparison]::OrdinalIgnoreCase) -or
            $name.StartsWith('ACTIONS_', [StringComparison]::OrdinalIgnoreCase) -or
            $name.StartsWith('RUNNER_', [StringComparison]::OrdinalIgnoreCase) -or
            $name.StartsWith('GIT_', [StringComparison]::OrdinalIgnoreCase) -or
            $name.StartsWith('DYLD_', [StringComparison]::OrdinalIgnoreCase) -or
            $name.StartsWith('COR_', [StringComparison]::OrdinalIgnoreCase) -or
            $name.StartsWith('CORECLR_', [StringComparison]::OrdinalIgnoreCase) -or
            $name -ieq 'CI' -or
            $name -ieq 'LD_PRELOAD' -or
            $name -ieq 'LD_LIBRARY_PATH' -or
            $name -ieq 'DOTNET_STARTUP_HOOKS' -or
            $name -ieq 'PSModulePath' -or
            $name -ieq 'SSH_AUTH_SOCK' -or
            $name -match '(?i)(?:TOKEN|SECRET|PASSWORD|PASSWD|API[_-]?KEY|ACCESS[_-]?KEY|PRIVATE[_-]?KEY|CREDENTIAL)') {
            [void]$StartInfo.EnvironmentVariables.Remove($name)
        }
    }
}

function Write-CICFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Bytes
    )
    $stream = $null
    try {
        $stream = New-Object IO.FileStream(
            $Path,
            [IO.FileMode]::Create,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        $stream.Write($Bytes, 0, $Bytes.Length)
        $stream.Flush($true)
    } catch {
        Fail-CIReleaseCommand -Code 'CIC_OUTPUT_WRITE_FAILED'
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

$executable = Get-CICAbsolutePath `
    -Value $ExecutablePath `
    -Code 'CIC_EXECUTABLE_INVALID'
$working = Get-CICAbsolutePath `
    -Value $WorkingDirectory `
    -Code 'CIC_WORKING_DIRECTORY_INVALID'
$output = Get-CICAbsolutePath -Value $OutputRoot -Code 'CIC_OUTPUT_ROOT_INVALID'
$stdoutPath = Get-CICAbsolutePath `
    -Value $StandardOutputPath `
    -Code 'CIC_OUTPUT_PATH_INVALID'
$stderrPath = Get-CICAbsolutePath `
    -Value $StandardErrorPath `
    -Code 'CIC_OUTPUT_PATH_INVALID'
$privateTemp = Get-CICAbsolutePath `
    -Value $PrivateTempRoot `
    -Code 'CIC_PRIVATE_TEMP_INVALID'

foreach ($directory in @($working, $output, $privateTemp)) {
    if (-not (Test-Path -LiteralPath $directory -PathType Container)) {
        Fail-CIReleaseCommand -Code 'CIC_LAYOUT_MISSING'
    }
    Assert-CICNoReparseAncestry -Path $directory -Code 'CIC_LAYOUT_INVALID'
}
if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
    Fail-CIReleaseCommand -Code 'CIC_EXECUTABLE_INVALID'
}
Assert-CICNoReparseAncestry -Path $executable -Code 'CIC_EXECUTABLE_INVALID'
if ((Test-CICPathContains -Parent $working -Candidate $executable -AllowEqual) -or
    -not (Test-CICPathContains -Parent $output -Candidate $stdoutPath) -or
    -not (Test-CICPathContains -Parent $output -Candidate $stderrPath) -or
    [string]::Equals($stdoutPath, $stderrPath, $script:PathComparison)) {
    Fail-CIReleaseCommand -Code 'CIC_LAYOUT_INVALID'
}
foreach ($path in @($stdoutPath, $stderrPath)) {
    $parent = [IO.Path]::GetDirectoryName($path)
    try {
        [void][IO.Directory]::CreateDirectory($parent)
    } catch {
        Fail-CIReleaseCommand -Code 'CIC_OUTPUT_PATH_INVALID'
    }
    Assert-CICNoReparseAncestry -Path $path -Code 'CIC_OUTPUT_PATH_INVALID'
    if (Test-Path -LiteralPath $path) {
        $item = Get-Item -LiteralPath $path -Force
        if (-not ($item -is [IO.FileInfo]) -or
            ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            Fail-CIReleaseCommand -Code 'CIC_OUTPUT_PATH_INVALID'
        }
    }
}

$isolatedHome = Join-Path $privateTemp 'home'
try {
    [void][IO.Directory]::CreateDirectory($isolatedHome)
} catch {
    Fail-CIReleaseCommand -Code 'CIC_PRIVATE_TEMP_INVALID'
}
Assert-CICNoReparseAncestry `
    -Path $isolatedHome `
    -Code 'CIC_PRIVATE_TEMP_INVALID'

$executableEncoded = [Convert]::ToBase64String($script:Utf8NoBom.GetBytes($executable))
$argumentLines = @($ArgumentList | ForEach-Object {
    $encoded = [Convert]::ToBase64String($script:Utf8NoBom.GetBytes([string]$_))
    ('$argumentsList.Add($utf8.GetString([Convert]::FromBase64String(''{0}'')))' -f $encoded)
})
$bootstrap = @(
    '$ErrorActionPreference = ''Stop''',
    '$utf8 = New-Object Text.UTF8Encoding($false)',
    '[Console]::OutputEncoding = $utf8',
    ('$executable = $utf8.GetString([Convert]::FromBase64String(''{0}''))' -f $executableEncoded),
    '$argumentsList = New-Object ''Collections.Generic.List[string]''',
    ($argumentLines -join "`n"),
    '$global:LASTEXITCODE = 0',
    '& $executable @argumentsList',
    '$commandExit = $LASTEXITCODE',
    'if ($null -eq $commandExit) { $commandExit = if ($?) { 0 } else { 1 } }',
    'exit ([int]$commandExit)'
) -join "`n"
[byte[]]$bootstrapBytes = $script:Utf8NoBom.GetBytes($bootstrap + "`n")
if ($bootstrapBytes.LongLength -gt $script:MaximumBootstrapBytes) {
    Fail-CIReleaseCommand -Code 'CIC_BOOTSTRAP_TOO_LARGE'
}

$process = New-Object Diagnostics.Process
$jobHandle = [IntPtr]::Zero
$clock = [Diagnostics.Stopwatch]::StartNew()
$deadlineMilliseconds = [int64]$TimeoutSeconds * 1000
$startedAt = [DateTimeOffset]::UtcNow
try {
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = (Get-Process -Id $PID).Path
    $start.Arguments =
        '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command -'
    $start.WorkingDirectory = $working
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardInput = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    Remove-CICSensitiveEnvironment -StartInfo $start
    foreach ($name in @('TEMP', 'TMP', 'TMPDIR', 'HOME', 'USERPROFILE')) {
        $start.EnvironmentVariables[$name] = if ($name -in @('HOME', 'USERPROFILE')) {
            $isolatedHome
        } else {
            $privateTemp
        }
    }
    $start.EnvironmentVariables['PSModulePath'] = Join-Path $PSHOME 'Modules'
    $start.EnvironmentVariables['PSModuleAnalysisCachePath'] =
        Join-Path $privateTemp 'PowerShell-ModuleAnalysisCache'
    $start.EnvironmentVariables['GIT_TERMINAL_PROMPT'] = '0'
    $start.EnvironmentVariables['GIT_NO_REPLACE_OBJECTS'] = '1'
    $start.EnvironmentVariables['NO_COLOR'] = '1'
    foreach ($name in $EnvironmentOverrides.Keys) {
        $environmentName = [string]$name
        if ($environmentName -cnotmatch '^[A-Z][A-Z0-9_]{0,127}$' -or
            $environmentName.StartsWith('GITHUB_', [StringComparison]::Ordinal) -or
            $environmentName.StartsWith('ACTIONS_', [StringComparison]::Ordinal) -or
            $environmentName.StartsWith('RUNNER_', [StringComparison]::Ordinal) -or
            $environmentName -match '(?:TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|ACCESS_KEY|PRIVATE_KEY|CREDENTIAL)') {
            Fail-CIReleaseCommand -Code 'CIC_ENVIRONMENT_OVERRIDE_INVALID'
        }
        $start.EnvironmentVariables[$environmentName] = [string]$EnvironmentOverrides[$name]
    }
    $process.StartInfo = $start
    $originalConsoleInputEncoding = [Console]::InputEncoding
    $processStarted = $false
    try {
        # Windows PowerShell 5.1 constructs Process.StandardInput with
        # Console.InputEncoding. Its default UTF-8 writer emits a BOM before
        # the raw bootstrap bytes, and `-Command -` then treats that BOM as
        # part of the first command. Select the existing no-BOM UTF-8 encoder
        # only for process creation, when the redirected writer is frozen.
        [Console]::InputEncoding = $script:Utf8NoBom
        $processStarted = $process.Start()
    } catch {
        if ($processStarted) {
            Stop-CICProcessNoThrow -Process $process
        }
        Fail-CIReleaseCommand -Code 'CIC_PROCESS_START_FAILED'
    } finally {
        try {
            [Console]::InputEncoding = $originalConsoleInputEncoding
        } catch {
            if ($processStarted) {
                Stop-CICProcessNoThrow -Process $process
            }
            Fail-CIReleaseCommand -Code 'CIC_PROCESS_START_FAILED'
        }
    }
    if (-not $processStarted) {
        Fail-CIReleaseCommand -Code 'CIC_PROCESS_START_FAILED'
    }
    if ($script:IsWindowsPlatform) {
        try {
            $jobHandle = [CIReleaseCommandProcessTree]::CreateKillOnCloseJob(
                $process
            )
        } catch {
            Stop-CICProcessNoThrow -Process $process
            Fail-CIReleaseCommand -Code 'CIC_PROCESS_ISOLATION_FAILED'
        }
    }
    $stdoutTask = [CIReleaseCommandBoundedIO]::CaptureAsync(
        $process.StandardOutput.BaseStream,
        $MaximumStandardOutputBytes
    )
    $stderrTask = [CIReleaseCommandBoundedIO]::CaptureAsync(
        $process.StandardError.BaseStream,
        $MaximumStandardErrorBytes
    )
    $stdinTask = [CIReleaseCommandBoundedIO]::WriteAndCloseAsync(
        $process.StandardInput.BaseStream,
        $bootstrapBytes
    )
    Wait-CICTask `
        -Task $stdinTask `
        -Process $process `
        -Stopwatch $clock `
        -DeadlineMilliseconds $deadlineMilliseconds `
        -Code 'CIC_INPUT_FAILED' `
        -JobHandle $jobHandle
    $remaining = Get-CICRemainingMilliseconds `
        -Stopwatch $clock `
        -DeadlineMilliseconds $deadlineMilliseconds
    if ($remaining -le 0 -or -not $process.WaitForExit($remaining)) {
        Stop-CICProcessNoThrow -Process $process -JobHandle $jobHandle
        Fail-CIReleaseCommand -Code 'CIC_TIMEOUT'
    }
    if ($script:IsWindowsPlatform -and $jobHandle -ne [IntPtr]::Zero) {
        try {
            [CIReleaseCommandProcessTree]::Close($jobHandle)
            $jobHandle = [IntPtr]::Zero
        } catch {
            Fail-CIReleaseCommand -Code 'CIC_PROCESS_ISOLATION_FAILED'
        }
    }
    Wait-CICTask `
        -Task $stdoutTask `
        -Process $process `
        -Stopwatch $clock `
        -DeadlineMilliseconds $deadlineMilliseconds `
        -Code 'CIC_CAPTURE_FAILED' `
        -JobHandle $jobHandle
    Wait-CICTask `
        -Task $stderrTask `
        -Process $process `
        -Stopwatch $clock `
        -DeadlineMilliseconds $deadlineMilliseconds `
        -Code 'CIC_CAPTURE_FAILED' `
        -JobHandle $jobHandle
    $stdout = $stdoutTask.Result
    $stderr = $stderrTask.Result
    Write-CICFile -Path $stdoutPath -Bytes ([byte[]]$stdout.Bytes)
    Write-CICFile -Path $stderrPath -Bytes ([byte[]]$stderr.Bytes)
    if ($stdout.Exceeded -or $stderr.Exceeded) {
        Fail-CIReleaseCommand -Code 'CIC_OUTPUT_TOO_LARGE'
    }
    $clock.Stop()
    $finishedAt = [DateTimeOffset]::UtcNow
    [pscustomobject]@{
        ExitCode = $process.ExitCode
        StartedAtUtc = $startedAt.ToString('O')
        FinishedAtUtc = $finishedAt.ToString('O')
        ElapsedMilliseconds = $clock.ElapsedMilliseconds
        StandardOutputBytes = [int64]$stdout.TotalBytes
        StandardErrorBytes = [int64]$stderr.TotalBytes
    }
} finally {
    if ($script:IsWindowsPlatform -and $jobHandle -ne [IntPtr]::Zero) {
        try {
            [CIReleaseCommandProcessTree]::Terminate($jobHandle)
        } catch {}
        try {
            [CIReleaseCommandProcessTree]::Close($jobHandle)
        } catch {}
    }
    try { $process.Dispose() } catch {}
}
