[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$RepositoryRoot,
    [Parameter(Mandatory = $true)][string]$Revision,
    [Parameter(Mandatory = $true)][string]$WorkRoot,
    [Parameter(Mandatory = $true)][string]$ExpectedGeneratorSha256,
    [Parameter(Mandatory = $true)][string]$ExpectedVerifierSha256
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:IsWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:Utf8Strict = New-Object System.Text.UTF8Encoding($false, $true)
$script:Utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$script:Ascii = [Text.Encoding]::ASCII
$script:ManifestName = 'public-tree-manifest.v1.json'
$script:MaximumGitMetadataBytes = 67108864
$script:MaximumTreeFiles = 100000
$script:MaximumTreeBytes = 8589934592
$script:MaximumArchiveBytes = 8589934592
$script:MaximumGitAttributesBytes = 1048576
$script:MaximumStagingToolBytes = 4194304
$script:MaximumChildInputBytes = 8388608
$script:MaximumChildStdoutBytes = 8388608
$script:MaximumChildStderrBytes = 1048576
$script:MaximumChildMilliseconds = 600000

function Fail-CIReleaseWorkspace {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw "CI_RELEASE_WORKSPACE_FAIL code=$Code"
}

if ($null -eq ('CIReleaseWorkspaceBoundedIO' -as [type])) {
    try {
        Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Threading.Tasks;

public sealed class CIReleaseWorkspaceCaptureResult
{
    public byte[] Bytes { get; set; }
    public bool Exceeded { get; set; }
    public long TotalBytes { get; set; }
}

public static class CIReleaseWorkspaceBoundedIO
{
    public static async Task<CIReleaseWorkspaceCaptureResult> CaptureAsync(
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
            while ((read = await source.ReadAsync(buffer, 0, buffer.Length).ConfigureAwait(false)) > 0)
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
            return new CIReleaseWorkspaceCaptureResult
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

    public static async Task<CIReleaseWorkspaceCaptureResult> CopyAsync(
        Stream source,
        Stream destination,
        long maximumBytes)
    {
        if (source == null) throw new ArgumentNullException("source");
        if (destination == null) throw new ArgumentNullException("destination");
        if (maximumBytes < 0) throw new ArgumentOutOfRangeException("maximumBytes");
        var buffer = new byte[1048576];
        long total = 0;
        bool exceeded = false;
        int read;
        while ((read = await source.ReadAsync(buffer, 0, buffer.Length).ConfigureAwait(false)) > 0)
        {
            total += read;
            if (!exceeded && total <= maximumBytes)
            {
                await destination.WriteAsync(buffer, 0, read).ConfigureAwait(false);
            }
            else
            {
                exceeded = true;
            }
        }
        return new CIReleaseWorkspaceCaptureResult
        {
            Bytes = new byte[0],
            Exceeded = exceeded,
            TotalBytes = total
        };
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
            await destination.WriteAsync(bytes, 0, bytes.Length).ConfigureAwait(false);
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
        if (primary != null)
        {
            throw primary;
        }
    }
}
'@ -ErrorAction Stop
    } catch {
        Write-Error 'CI_RELEASE_WORKSPACE_FAIL code=CRW_CAPTURE_RUNTIME_UNAVAILABLE'
        exit 1
    }
}

function ConvertTo-LowerHex {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    return ([BitConverter]::ToString($Bytes)).Replace('-', '').ToLowerInvariant()
}

function Get-FileSha256 {
    param([Parameter(Mandatory = $true)][string]$Path)
    $stream = $null
    $sha = $null
    try {
        $stream = New-Object IO.FileStream(
            $Path,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read,
            1048576,
            [IO.FileOptions]::SequentialScan
        )
        $sha = [Security.Cryptography.SHA256]::Create()
        return ConvertTo-LowerHex -Bytes ([byte[]]$sha.ComputeHash($stream))
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_FILE_HASH_FAILED'
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Get-BytesSha256 {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return ConvertTo-LowerHex -Bytes ([byte[]]$sha.ComputeHash($Bytes))
    } finally {
        $sha.Dispose()
    }
}

function Get-NormalizedAbsolutePath {
    param([Parameter(Mandatory = $true)][string]$Value)
    if ([string]::IsNullOrWhiteSpace($Value)) {
        Fail-CIReleaseWorkspace -Code 'CRW_ROOT_NOT_ABSOLUTE'
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) {
            Fail-CIReleaseWorkspace -Code 'CRW_ROOT_NOT_ABSOLUTE'
        }
    }
    $isAbsolute = $false
    if ($script:IsWindowsPlatform) {
        $deviceProbe = $Value.Replace([char]47, [char]92)
        $isDevicePath = $deviceProbe.StartsWith('\\?\', [StringComparison]::Ordinal) -or
            $deviceProbe.StartsWith('\\.\', [StringComparison]::Ordinal) -or
            $deviceProbe.StartsWith('\??\', [StringComparison]::Ordinal)
        $isDriveAbsolute = $Value -match '^[A-Za-z]:[\\/]'
        $isUncAbsolute = $Value -match '^[\\/]{2}[^\\/]+[\\/][^\\/]+(?:[\\/].*)?$'
        $isAbsolute = -not $isDevicePath -and ($isDriveAbsolute -or $isUncAbsolute)
    } else {
        $isAbsolute = $Value.StartsWith('/', [StringComparison]::Ordinal)
    }
    if (-not $isAbsolute) {
        Fail-CIReleaseWorkspace -Code 'CRW_ROOT_NOT_ABSOLUTE'
    }
    try {
        $full = [IO.Path]::GetFullPath($Value)
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_ROOT_NOT_ABSOLUTE'
    }
    if ($script:IsWindowsPlatform -and (
        $full.StartsWith('\\?\', [StringComparison]::Ordinal) -or
        $full.StartsWith('\\.\', [StringComparison]::Ordinal) -or
        $full.StartsWith('\??\', [StringComparison]::Ordinal)
    )) {
        Fail-CIReleaseWorkspace -Code 'CRW_ROOT_NOT_ABSOLUTE'
    }
    return $full.TrimEnd([char[]]@(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    ))
}

function Assert-NotFileSystemRoot {
    param([Parameter(Mandatory = $true)][string]$Path)
    $pathRoot = [IO.Path]::GetPathRoot($Path)
    if ([string]::IsNullOrWhiteSpace($pathRoot)) {
        Fail-CIReleaseWorkspace -Code 'CRW_ROOT_NOT_ABSOLUTE'
    }
    $trimmedPath = $Path.TrimEnd([char[]]@([char]92, [char]47))
    $trimmedRoot = $pathRoot.TrimEnd([char[]]@([char]92, [char]47))
    $comparison = if ($script:IsWindowsPlatform) {
        [StringComparison]::OrdinalIgnoreCase
    } else {
        [StringComparison]::Ordinal
    }
    if ([string]::Equals($trimmedPath, $trimmedRoot, $comparison)) {
        Fail-CIReleaseWorkspace -Code 'CRW_ROOT_FILESYSTEM'
    }
}

function Assert-NoReparseAncestry {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $cursor = if (Test-Path -LiteralPath $Path) {
        $Path
    } else {
        [IO.Path]::GetDirectoryName($Path)
    }
    $comparison = if ($script:IsWindowsPlatform) {
        [StringComparison]::OrdinalIgnoreCase
    } else {
        [StringComparison]::Ordinal
    }
    while (-not [string]::IsNullOrWhiteSpace($cursor)) {
        if (Test-Path -LiteralPath $cursor) {
            try {
                $item = Get-Item -LiteralPath $cursor -Force
            } catch {
                Fail-CIReleaseWorkspace -Code $Code
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-CIReleaseWorkspace -Code $Code
            }
        }
        $parent = [IO.Directory]::GetParent($cursor)
        if ($null -eq $parent) { break }
        $next = $parent.FullName
        if ([string]::Equals($next, $cursor, $comparison)) { break }
        $cursor = $next
    }
}

function Test-PathInside {
    param(
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$Container
    )
    $comparison = if ($script:IsWindowsPlatform) {
        [StringComparison]::OrdinalIgnoreCase
    } else {
        [StringComparison]::Ordinal
    }
    if ([string]::Equals($Candidate, $Container, $comparison)) {
        return $true
    }
    return $Candidate.StartsWith(
        $Container + [IO.Path]::DirectorySeparatorChar,
        $comparison
    )
}

function Assert-RootsDisjoint {
    param([Parameter(Mandatory = $true)][string[]]$Roots)
    for ($left = 0; $left -lt $Roots.Count; $left++) {
        for ($right = $left + 1; $right -lt $Roots.Count; $right++) {
            if ((Test-PathInside -Candidate $Roots[$left] -Container $Roots[$right]) -or
                (Test-PathInside -Candidate $Roots[$right] -Container $Roots[$left])) {
                Fail-CIReleaseWorkspace -Code 'CRW_ROOTS_NOT_DISJOINT'
            }
        }
    }
}

function Assert-DirectoryEmpty {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    try {
        $entries = @(Get-ChildItem -LiteralPath $Path -Force)
    } catch {
        Fail-CIReleaseWorkspace -Code $Code
    }
    if ($entries.Count -ne 0) {
        Fail-CIReleaseWorkspace -Code $Code
    }
}

function Resolve-ArchiveOperationFailure {
    param(
        [Parameter(Mandatory = $false)][AllowNull()]$PrimaryFailure,
        [Parameter(Mandatory = $true)][bool]$CleanupFailed
    )
    if ($null -ne $PrimaryFailure) {
        throw $PrimaryFailure
    }
    if ($CleanupFailed) {
        Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_CLEANUP_FAILED'
    }
}

function Assert-PortableRelativePath {
    param([Parameter(Mandatory = $true)][string]$RelativePath)
    try {
        $isPathRooted = [IO.Path]::IsPathRooted($RelativePath)
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_PATH_INVALID'
    }
    if ([string]::IsNullOrEmpty($RelativePath) -or
        $RelativePath.StartsWith('/', [StringComparison]::Ordinal) -or
        $RelativePath.Contains([char]92) -or
        $isPathRooted -or
        $script:Utf8NoBom.GetByteCount($RelativePath) -gt 4096) {
        Fail-CIReleaseWorkspace -Code 'CRW_PATH_INVALID'
    }
    foreach ($segment in $RelativePath.Split([char]47)) {
        if ([string]::IsNullOrEmpty($segment) -or
            $segment -ceq '.' -or
            $segment -ceq '..' -or
            $segment.EndsWith('.', [StringComparison]::Ordinal) -or
            $segment.EndsWith(' ', [StringComparison]::Ordinal) -or
            $script:Utf8NoBom.GetByteCount($segment) -gt 255 -or
            -not [string]::Equals(
                $segment.Normalize([Text.NormalizationForm]::FormC),
                $segment,
                [StringComparison]::Ordinal
            ) -or
            $segment.IndexOfAny([char[]]@(
                '<', '>', ':', '"', '/', [char]92, '|', '?', '*'
            )) -ge 0) {
            Fail-CIReleaseWorkspace -Code 'CRW_PATH_INVALID'
        }
        foreach ($character in $segment.ToCharArray()) {
            $category = [Globalization.CharUnicodeInfo]::GetUnicodeCategory($character)
            if ($category -in @(
                [Globalization.UnicodeCategory]::Control,
                [Globalization.UnicodeCategory]::Format,
                [Globalization.UnicodeCategory]::LineSeparator,
                [Globalization.UnicodeCategory]::ParagraphSeparator,
                [Globalization.UnicodeCategory]::Surrogate
            )) {
                Fail-CIReleaseWorkspace -Code 'CRW_PATH_INVALID'
            }
        }
        $baseName = ($segment -split '\.', 2)[0]
        if ($baseName -match '^(?i:CON|PRN|AUX|NUL|COM(?:[1-9]|\u00B9|\u00B2|\u00B3)|LPT(?:[1-9]|\u00B9|\u00B2|\u00B3))$' -or
            $segment -ieq '.git') {
            Fail-CIReleaseWorkspace -Code 'CRW_PATH_INVALID'
        }
    }
}

function Add-PortablePathKey {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)]$Exact,
        [Parameter(Mandatory = $true)]$CaseInsensitive,
        [Parameter(Mandatory = $true)]$Nfd
    )
    if (-not $Exact.Add($Path) -or
        -not $CaseInsensitive.Add($Path) -or
        -not $Nfd.Add($Path.Normalize([Text.NormalizationForm]::FormD))) {
        Fail-CIReleaseWorkspace -Code 'CRW_PATH_COLLISION'
    }
}

function Get-ChildRemainingMilliseconds {
    param([Parameter(Mandatory = $true)][Diagnostics.Stopwatch]$Stopwatch)
    $remaining = [int64]$script:MaximumChildMilliseconds - $Stopwatch.ElapsedMilliseconds
    if ($remaining -le 0) { return 0 }
    if ($remaining -gt [int]::MaxValue) { return [int]::MaxValue }
    return [int]$remaining
}

function Stop-BoundedChildProcess {
    param([Parameter(Mandatory = $true)][Diagnostics.Process]$Process)
    try {
        if (-not $Process.HasExited) {
            $Process.Kill()
        }
    } catch {}
    foreach ($stream in @(
        { $Process.StandardInput.BaseStream },
        { $Process.StandardOutput.BaseStream },
        { $Process.StandardError.BaseStream }
    )) {
        try {
            (& $stream).Dispose()
        } catch {}
    }
}

function Wait-ChildTaskWithinDeadline {
    param(
        [Parameter(Mandatory = $true)][Threading.Tasks.Task]$Task,
        [Parameter(Mandatory = $true)][Diagnostics.Process]$Process,
        [Parameter(Mandatory = $true)][Diagnostics.Stopwatch]$Stopwatch,
        [Parameter(Mandatory = $true)][string]$TimeoutCode,
        [Parameter(Mandatory = $true)][string]$CaptureFailureCode
    )
    $remaining = Get-ChildRemainingMilliseconds -Stopwatch $Stopwatch
    if ($remaining -le 0) {
        Stop-BoundedChildProcess -Process $Process
        Fail-CIReleaseWorkspace -Code $TimeoutCode
    }
    try {
        $completed = $Task.Wait($remaining)
    } catch {
        Stop-BoundedChildProcess -Process $Process
        if ((Get-ChildRemainingMilliseconds -Stopwatch $Stopwatch) -le 0) {
            Fail-CIReleaseWorkspace -Code $TimeoutCode
        }
        Fail-CIReleaseWorkspace -Code $CaptureFailureCode
    }
    if (-not $completed) {
        Stop-BoundedChildProcess -Process $Process
        Fail-CIReleaseWorkspace -Code $TimeoutCode
    }
    if ($Task.IsCanceled -or $Task.IsFaulted) {
        Stop-BoundedChildProcess -Process $Process
        Fail-CIReleaseWorkspace -Code $CaptureFailureCode
    }
}

function Wait-BoundedChildProcess {
    param(
        [Parameter(Mandatory = $true)][Diagnostics.Process]$Process,
        [Parameter(Mandatory = $true)][Diagnostics.Stopwatch]$Stopwatch,
        [AllowNull()][Threading.Tasks.Task]$StandardInputTask,
        [Parameter(Mandatory = $true)]$StandardOutputTask,
        [Parameter(Mandatory = $true)]$StandardErrorTask,
        [Parameter(Mandatory = $true)][string]$TimeoutCode,
        [Parameter(Mandatory = $true)][string]$CaptureFailureCode
    )
    if ($null -ne $StandardInputTask) {
        Wait-ChildTaskWithinDeadline `
            -Task $StandardInputTask `
            -Process $Process `
            -Stopwatch $Stopwatch `
            -TimeoutCode $TimeoutCode `
            -CaptureFailureCode $CaptureFailureCode
    }
    $remaining = Get-ChildRemainingMilliseconds -Stopwatch $Stopwatch
    if ($remaining -le 0) {
        Stop-BoundedChildProcess -Process $Process
        Fail-CIReleaseWorkspace -Code $TimeoutCode
    }
    try {
        $exited = $Process.WaitForExit($remaining)
    } catch {
        Stop-BoundedChildProcess -Process $Process
        if ((Get-ChildRemainingMilliseconds -Stopwatch $Stopwatch) -le 0) {
            Fail-CIReleaseWorkspace -Code $TimeoutCode
        }
        Fail-CIReleaseWorkspace -Code $CaptureFailureCode
    }
    if (-not $exited) {
        Stop-BoundedChildProcess -Process $Process
        Fail-CIReleaseWorkspace -Code $TimeoutCode
    }
    Wait-ChildTaskWithinDeadline `
        -Task $StandardOutputTask `
        -Process $Process `
        -Stopwatch $Stopwatch `
        -TimeoutCode $TimeoutCode `
        -CaptureFailureCode $CaptureFailureCode
    Wait-ChildTaskWithinDeadline `
        -Task $StandardErrorTask `
        -Process $Process `
        -Stopwatch $Stopwatch `
        -TimeoutCode $TimeoutCode `
        -CaptureFailureCode $CaptureFailureCode
    try {
        $standardOutput = $StandardOutputTask.Result
        $standardError = $StandardErrorTask.Result
    } catch {
        Fail-CIReleaseWorkspace -Code $CaptureFailureCode
    }
    return [pscustomobject]@{
        StandardOutput = $standardOutput
        StandardError = $standardError
        ExitCode = $Process.ExitCode
    }
}

function Write-BoundedCapturedText {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][byte[]]$Bytes,
        [Parameter(Mandatory = $true)][string]$FailureCode
    )
    if ($Bytes.Length -eq 0) { return }
    try {
        $text = $script:Utf8Strict.GetString($Bytes)
    } catch {
        Fail-CIReleaseWorkspace -Code $FailureCode
    }
    foreach ($line in ($text -split '\r?\n')) {
        if (-not [string]::IsNullOrEmpty($line)) {
            Write-Host $line
        }
    }
}

function New-GitStartInfo {
    param(
        [Parameter(Mandatory = $true)][string]$Arguments,
        [Parameter(Mandatory = $true)][bool]$RedirectStandardOutput
    )
    $start = New-Object Diagnostics.ProcessStartInfo
    $start.FileName = $script:GitCommand
    $start.Arguments = '--no-replace-objects ' + $Arguments
    $start.WorkingDirectory = $script:RepositoryFull
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $RedirectStandardOutput
    $start.RedirectStandardError = $true
    $start.StandardErrorEncoding = $script:Utf8Strict
    if ($RedirectStandardOutput) {
        $start.StandardOutputEncoding = $script:Utf8Strict
    }

    foreach ($name in @(
        'GIT_DIR',
        'GIT_WORK_TREE',
        'GIT_INDEX_FILE',
        'GIT_OBJECT_DIRECTORY',
        'GIT_ALTERNATE_OBJECT_DIRECTORIES',
        'GIT_COMMON_DIR',
        'GIT_CONFIG',
        'GIT_CONFIG_GLOBAL',
        'GIT_CONFIG_SYSTEM',
        'GIT_CONFIG_PARAMETERS',
        'GIT_CONFIG_COUNT',
        'GIT_CEILING_DIRECTORIES',
        'GIT_DISCOVERY_ACROSS_FILESYSTEM'
    )) {
        [void]$start.EnvironmentVariables.Remove($name)
    }
    foreach ($key in @($start.EnvironmentVariables.Keys)) {
        $name = [string]$key
        if ($name.StartsWith('GIT_CONFIG_KEY_', [StringComparison]::OrdinalIgnoreCase) -or
            $name.StartsWith('GIT_CONFIG_VALUE_', [StringComparison]::OrdinalIgnoreCase)) {
            [void]$start.EnvironmentVariables.Remove($name)
        }
    }
    $start.EnvironmentVariables['GIT_NO_REPLACE_OBJECTS'] = '1'
    $start.EnvironmentVariables['GIT_CONFIG_NOSYSTEM'] = '1'
    $start.EnvironmentVariables['GIT_ATTR_NOSYSTEM'] = '1'
    $start.EnvironmentVariables['GIT_CONFIG_GLOBAL'] = if ($script:IsWindowsPlatform) {
        'NUL'
    } else {
        '/dev/null'
    }
    $start.EnvironmentVariables['GIT_OPTIONAL_LOCKS'] = '0'
    $start.EnvironmentVariables['LC_ALL'] = 'C'
    return $start
}

function Invoke-GitBytes {
    param(
        [Parameter(Mandatory = $true)][string]$Arguments,
        [Parameter(Mandatory = $true)][string]$FailureCode,
        [int64]$MaximumBytes = $script:MaximumGitMetadataBytes
    )
    $process = New-Object Diagnostics.Process
    try {
        $process.StartInfo = New-GitStartInfo -Arguments $Arguments -RedirectStandardOutput $true
        $stopwatch = [Diagnostics.Stopwatch]::StartNew()
        try {
            if (-not $process.Start()) {
                Fail-CIReleaseWorkspace -Code $FailureCode
            }
        } catch {
            Fail-CIReleaseWorkspace -Code $FailureCode
        }
        $outputTask = [CIReleaseWorkspaceBoundedIO]::CaptureAsync(
            $process.StandardOutput.BaseStream,
            $MaximumBytes
        )
        $errorTask = [CIReleaseWorkspaceBoundedIO]::CaptureAsync(
            $process.StandardError.BaseStream,
            $script:MaximumChildStderrBytes
        )
        $result = Wait-BoundedChildProcess `
            -Process $process `
            -Stopwatch $stopwatch `
            -StandardOutputTask $outputTask `
            -StandardErrorTask $errorTask `
            -TimeoutCode 'CRW_GIT_TIMEOUT' `
            -CaptureFailureCode 'CRW_GIT_CAPTURE_FAILED'
        if ($result.StandardOutput.Exceeded -or $result.StandardError.Exceeded) {
            Fail-CIReleaseWorkspace -Code 'CRW_GIT_OUTPUT_TOO_LARGE'
        }
        if ($result.ExitCode -ne 0) {
            Fail-CIReleaseWorkspace -Code $FailureCode
        }
        return [byte[]]$result.StandardOutput.Bytes
    } finally {
        try { $process.Dispose() } catch {}
    }
}

function Invoke-GitText {
    param(
        [Parameter(Mandatory = $true)][string]$Arguments,
        [Parameter(Mandatory = $true)][string]$FailureCode
    )
    [byte[]]$bytes = Invoke-GitBytes -Arguments $Arguments -FailureCode $FailureCode
    try {
        $text = $script:Utf8Strict.GetString($bytes)
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_GIT_OUTPUT_ENCODING'
    }
    if ($text.IndexOf([char]0) -ge 0) {
        Fail-CIReleaseWorkspace -Code 'CRW_GIT_OUTPUT_FORMAT'
    }
    $normalized = $text.Replace("`r`n", "`n")
    if ($normalized.IndexOf([char]13) -ge 0) {
        Fail-CIReleaseWorkspace -Code 'CRW_GIT_OUTPUT_FORMAT'
    }
    [string[]]$lines = @($normalized.Split([char]10))
    if ($lines.Count -gt 0 -and $lines[$lines.Count - 1] -ceq '') {
        $lines = @($lines[0..($lines.Count - 2)])
    }
    if ($lines.Count -ne 1 -or [string]::IsNullOrWhiteSpace($lines[0])) {
        Fail-CIReleaseWorkspace -Code 'CRW_GIT_OUTPUT_FORMAT'
    }
    return $lines[0]
}

function Write-GitArchive {
    param(
        [Parameter(Mandatory = $true)][string]$RevisionValue,
        [Parameter(Mandatory = $true)][string]$Destination
    )
    $process = New-Object Diagnostics.Process
    $output = $null
    try {
        try {
            $output = New-Object IO.FileStream(
                $Destination,
                [IO.FileMode]::CreateNew,
                [IO.FileAccess]::Write,
                [IO.FileShare]::None,
                1048576,
                [IO.FileOptions]::SequentialScan
            )
        } catch {
            Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_CREATE_FAILED'
        }
        $process.StartInfo = New-GitStartInfo `
            -Arguments ('archive --format=zip ' + $RevisionValue) `
            -RedirectStandardOutput $true
        $stopwatch = [Diagnostics.Stopwatch]::StartNew()
        try {
            if (-not $process.Start()) {
                Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_CREATE_FAILED'
            }
        } catch {
            Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_CREATE_FAILED'
        }
        $outputTask = [CIReleaseWorkspaceBoundedIO]::CopyAsync(
            $process.StandardOutput.BaseStream,
            $output,
            $script:MaximumArchiveBytes
        )
        $errorTask = [CIReleaseWorkspaceBoundedIO]::CaptureAsync(
            $process.StandardError.BaseStream,
            $script:MaximumChildStderrBytes
        )
        $result = Wait-BoundedChildProcess `
            -Process $process `
            -Stopwatch $stopwatch `
            -StandardOutputTask $outputTask `
            -StandardErrorTask $errorTask `
            -TimeoutCode 'CRW_GIT_TIMEOUT' `
            -CaptureFailureCode 'CRW_GIT_CAPTURE_FAILED'
        if ($result.StandardOutput.Exceeded) {
            Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_TOO_LARGE'
        }
        if ($result.StandardError.Exceeded) {
            Fail-CIReleaseWorkspace -Code 'CRW_GIT_OUTPUT_TOO_LARGE'
        }
        $output.Flush($true)
        if ($result.ExitCode -ne 0) {
            Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_CREATE_FAILED'
        }
    } finally {
        if ($null -ne $output) {
            try { $output.Dispose() } catch {}
        }
        try { $process.Dispose() } catch {}
    }
}

function Get-GitTreeRecords {
    param(
        [Parameter(Mandatory = $true)][byte[]]$TreeBytes,
        [Parameter(Mandatory = $true)][int]$ObjectIdLength
    )
    if ($TreeBytes.Length -eq 0 -or $TreeBytes[$TreeBytes.Length - 1] -ne 0) {
        Fail-CIReleaseWorkspace -Code 'CRW_TREE_FORMAT'
    }
    try {
        $text = $script:Utf8Strict.GetString($TreeBytes)
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_TREE_ENCODING'
    }
    [string[]]$entries = @($text.Split([char]0))
    if ($entries[$entries.Count - 1] -cne '') {
        Fail-CIReleaseWorkspace -Code 'CRW_TREE_FORMAT'
    }
    $records = New-Object 'System.Collections.Generic.List[object]'
    $recordByPath = New-Object 'System.Collections.Generic.Dictionary[string,object]' (
        [StringComparer]::Ordinal
    )
    $allExact = New-Object 'System.Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $allCase = New-Object 'System.Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    $allNfd = New-Object 'System.Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $directories = New-Object 'System.Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $directoryCase = New-Object 'System.Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    $directoryNfd = New-Object 'System.Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    [int64]$totalTreeBytes = 0

    $oidPattern = if ($ObjectIdLength -eq 40) {
        '[0-9a-f]{40}'
    } elseif ($ObjectIdLength -eq 64) {
        '[0-9a-f]{64}'
    } else {
        Fail-CIReleaseWorkspace -Code 'CRW_OBJECT_FORMAT'
    }
    $pattern = '^(?<mode>[0-9]{6}) (?<type>[a-z]+) (?<oid>' +
        $oidPattern + ') +(?<size>[0-9]+|-)' + "`t" + '(?<path>.+)$'

    for ($index = 0; $index -lt $entries.Count - 1; $index++) {
        $entry = $entries[$index]
        $match = [regex]::Match($entry, $pattern, [Text.RegularExpressions.RegexOptions]::CultureInvariant)
        if (-not $match.Success) {
            Fail-CIReleaseWorkspace -Code 'CRW_TREE_FORMAT'
        }
        $mode = $match.Groups['mode'].Value
        $type = $match.Groups['type'].Value
        if ($type -cne 'blob' -or $mode -notin @('100644', '100755')) {
            Fail-CIReleaseWorkspace -Code 'CRW_TREE_ENTRY_FORBIDDEN'
        }
        $path = $match.Groups['path'].Value
        Assert-PortableRelativePath -RelativePath $path
        Add-PortablePathKey `
            -Path $path `
            -Exact $allExact `
            -CaseInsensitive $allCase `
            -Nfd $allNfd
        $leaf = [IO.Path]::GetFileName($path.Replace([char]47, [IO.Path]::DirectorySeparatorChar))
        if ($leaf -ieq '.gitmodules' -or $leaf -ieq '.lfsconfig') {
            Fail-CIReleaseWorkspace -Code 'CRW_GIT_METADATA_FORBIDDEN'
        }
        [int64]$size = 0
        if ($match.Groups['size'].Value -ceq '-' -or
            -not [int64]::TryParse(
                $match.Groups['size'].Value,
                [Globalization.NumberStyles]::None,
                [Globalization.CultureInfo]::InvariantCulture,
                [ref]$size
            ) -or
            $size -lt 0) {
            Fail-CIReleaseWorkspace -Code 'CRW_TREE_FORMAT'
        }
        $record = [pscustomobject]@{
            Mode = $mode
            ObjectId = $match.Groups['oid'].Value
            Size = $size
            Path = $path
        }
        $records.Add($record)
        $recordByPath.Add($path, $record)
        if ($records.Count -gt $script:MaximumTreeFiles -or
            $size -gt $script:MaximumTreeBytes - $totalTreeBytes) {
            Fail-CIReleaseWorkspace -Code 'CRW_TREE_TOO_LARGE'
        }
        $totalTreeBytes += $size
    }
    if ($records.Count -eq 0) {
        Fail-CIReleaseWorkspace -Code 'CRW_TREE_EMPTY'
    }

    foreach ($record in $records) {
        [string[]]$segments = @(([string]$record.Path).Split([char]47))
        if ($segments.Count -lt 2) { continue }
        for ($depth = 1; $depth -lt $segments.Count; $depth++) {
            $directory = ($segments[0..($depth - 1)] -join '/')
            if ($recordByPath.ContainsKey($directory)) {
                Fail-CIReleaseWorkspace -Code 'CRW_PATH_COLLISION'
            }
            if ($directories.Add($directory)) {
                if (-not $directoryCase.Add($directory) -or
                    -not $directoryNfd.Add($directory.Normalize([Text.NormalizationForm]::FormD)) -or
                    -not $allExact.Add($directory) -or
                    -not $allCase.Add($directory) -or
                    -not $allNfd.Add($directory.Normalize([Text.NormalizationForm]::FormD))) {
                    Fail-CIReleaseWorkspace -Code 'CRW_PATH_COLLISION'
                }
            }
        }
    }
    return [pscustomobject]@{
        Records = $records.ToArray()
        RecordByPath = $recordByPath
        Directories = $directories
    }
}

function Get-ArchiveUnixType {
    param([Parameter(Mandatory = $true)][int]$ExternalAttributes)
    $unsigned = [BitConverter]::ToUInt32([BitConverter]::GetBytes($ExternalAttributes), 0)
    return (($unsigned -shr 16) -band 0xF000)
}

function Get-GitBlobHash {
    param(
        [Parameter(Mandatory = $true)][IO.Stream]$InputStream,
        [Parameter(Mandatory = $true)][IO.Stream]$OutputStream,
        [Parameter(Mandatory = $true)][int64]$ExpectedSize,
        [Parameter(Mandatory = $true)][int]$ObjectIdLength,
        [string]$MismatchCode = 'CRW_ARCHIVE_BLOB_MISMATCH'
    )
    $hash = if ($ObjectIdLength -eq 40) {
        [Security.Cryptography.SHA1]::Create()
    } elseif ($ObjectIdLength -eq 64) {
        [Security.Cryptography.SHA256]::Create()
    } else {
        Fail-CIReleaseWorkspace -Code 'CRW_OBJECT_FORMAT'
    }
    try {
        [byte[]]$header = $script:Ascii.GetBytes('blob ' + [string]$ExpectedSize + [char]0)
        [void]$hash.TransformBlock($header, 0, $header.Length, $header, 0)
        [byte[]]$buffer = New-Object byte[] 1048576
        [int64]$written = 0
        while (($read = $InputStream.Read($buffer, 0, $buffer.Length)) -gt 0) {
            if ($written + $read -gt $ExpectedSize) {
                Fail-CIReleaseWorkspace -Code $MismatchCode
            }
            $OutputStream.Write($buffer, 0, $read)
            [void]$hash.TransformBlock($buffer, 0, $read, $buffer, 0)
            $written += $read
        }
        [byte[]]$empty = New-Object byte[] 0
        [void]$hash.TransformFinalBlock($empty, 0, 0)
        if ($written -ne $ExpectedSize) {
            Fail-CIReleaseWorkspace -Code $MismatchCode
        }
        return ConvertTo-LowerHex -Bytes ([byte[]]$hash.Hash)
    } finally {
        $hash.Dispose()
    }
}

function Assert-DirectoryMatchesGitTree {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)]$Tree,
        [Parameter(Mandatory = $true)][int]$ObjectIdLength
    )
    if (-not (Test-Path -LiteralPath $Root -PathType Container)) {
        Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
    }
    Assert-NoReparseAncestry -Path $Root -Code 'CRW_FINAL_TREE_MISMATCH'
    $actualFiles = New-Object 'System.Collections.Generic.Dictionary[string,object]' (
        [StringComparer]::Ordinal
    )
    $actualDirectories = New-Object 'System.Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )

    function Visit-TreeDirectory {
        param(
            [Parameter(Mandatory = $true)][string]$FullPath,
            [Parameter(Mandatory = $true)][AllowEmptyString()][string]$RelativePath
        )
        try {
            $directoryItem = Get-Item -LiteralPath $FullPath -Force
            $children = @(Get-ChildItem -LiteralPath $FullPath -Force)
        } catch {
            Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
        }
        $directoryLinkType = $directoryItem.PSObject.Properties['LinkType']
        if (($directoryItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
            ($null -ne $directoryLinkType -and
                -not [string]::IsNullOrWhiteSpace([string]$directoryLinkType.Value))) {
            Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
        }
        foreach ($child in $children) {
            $childRelative = if ([string]::IsNullOrEmpty($RelativePath)) {
                [string]$child.Name
            } else {
                $RelativePath + '/' + [string]$child.Name
            }
            Assert-PortableRelativePath -RelativePath $childRelative
            $linkType = $child.PSObject.Properties['LinkType']
            if (($child.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
                ($null -ne $linkType -and
                    -not [string]::IsNullOrWhiteSpace([string]$linkType.Value))) {
                Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
            }
            if ($child.PSIsContainer) {
                if (-not $actualDirectories.Add($childRelative)) {
                    Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
                }
                Visit-TreeDirectory -FullPath $child.FullName -RelativePath $childRelative
                continue
            }
            if (-not ($child -is [IO.FileInfo]) -or
                ($child.Attributes -band [IO.FileAttributes]::SparseFile) -ne 0 -or
                $actualFiles.ContainsKey($childRelative)) {
                Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
            }
            $actualFiles.Add($childRelative, $child)
        }
    }

    Visit-TreeDirectory -FullPath $Root -RelativePath ''
    if ($actualFiles.Count -ne $Tree.Records.Count -or
        $actualDirectories.Count -ne $Tree.Directories.Count) {
        Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
    }
    foreach ($directory in $Tree.Directories) {
        if (-not $actualDirectories.Contains([string]$directory)) {
            Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
        }
    }
    foreach ($record in $Tree.Records) {
        if (-not $actualFiles.ContainsKey([string]$record.Path)) {
            Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
        }
        $item = $actualFiles[[string]$record.Path]
        if ([int64]$item.Length -ne [int64]$record.Size) {
            Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
        }
        $stream = $null
        try {
            $stream = New-Object IO.FileStream(
                $item.FullName,
                [IO.FileMode]::Open,
                [IO.FileAccess]::Read,
                [IO.FileShare]::Read,
                1048576,
                [IO.FileOptions]::SequentialScan
            )
            $actualObjectId = Get-GitBlobHash `
                -InputStream $stream `
                -OutputStream ([IO.Stream]::Null) `
                -ExpectedSize ([int64]$record.Size) `
                -ObjectIdLength $ObjectIdLength `
                -MismatchCode 'CRW_FINAL_TREE_MISMATCH'
        } catch {
            if ($_.Exception.Message.StartsWith(
                'CI_RELEASE_WORKSPACE_FAIL code=',
                [StringComparison]::Ordinal
            )) {
                throw
            }
            Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
        } finally {
            if ($null -ne $stream) { $stream.Dispose() }
        }
        if ($actualObjectId -cne [string]$record.ObjectId) {
            Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
        }
        try {
            $after = Get-Item -LiteralPath $item.FullName -Force
        } catch {
            Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
        }
        if ([int64]$after.Length -ne [int64]$record.Size) {
            Fail-CIReleaseWorkspace -Code 'CRW_FINAL_TREE_MISMATCH'
        }
    }
}

function Get-TreeBoundScriptBytes {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)]$Tree,
        [Parameter(Mandatory = $true)][string]$RelativePath,
        [Parameter(Mandatory = $true)][int]$ObjectIdLength
    )
    if (-not $Tree.RecordByPath.ContainsKey($RelativePath)) {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_MISSING'
    }
    $record = $Tree.RecordByPath[$RelativePath]
    if ([int64]$record.Size -gt $script:MaximumStagingToolBytes) {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_TOO_LARGE'
    }
    $native = $RelativePath.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
    $path = Join-Path $Root $native
    try {
        [byte[]]$bytes = [IO.File]::ReadAllBytes($path)
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_AUTH_FAILED'
    }
    if ($bytes.LongLength -ne [int64]$record.Size) {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_AUTH_FAILED'
    }
    $memory = New-Object IO.MemoryStream(, $bytes)
    try {
        $objectId = Get-GitBlobHash `
            -InputStream $memory `
            -OutputStream ([IO.Stream]::Null) `
            -ExpectedSize ([int64]$record.Size) `
            -ObjectIdLength $ObjectIdLength `
            -MismatchCode 'CRW_STAGING_TOOL_AUTH_FAILED'
    } finally {
        $memory.Dispose()
    }
    if ($objectId -cne [string]$record.ObjectId) {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_AUTH_FAILED'
    }
    try {
        [void]$script:Utf8Strict.GetString($bytes)
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_AUTH_FAILED'
    }
    return [byte[]]$bytes
}

function Write-AuthenticatedScriptSnapshot {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][byte[]]$Bytes
    )
    $stream = $null
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $expectedHash = ConvertTo-LowerHex -Bytes ([byte[]]$sha.ComputeHash($Bytes))
    } finally {
        $sha.Dispose()
    }
    try {
        $stream = New-Object IO.FileStream(
            $Path,
            [IO.FileMode]::CreateNew,
            [IO.FileAccess]::Write,
            [IO.FileShare]::None
        )
        $stream.Write($Bytes, 0, $Bytes.Length)
        $stream.Flush($true)
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_SNAPSHOT_FAILED'
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
    }
    if ((Get-FileSha256 -Path $Path) -cne $expectedHash) {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_SNAPSHOT_FAILED'
    }
}

function Assert-NoLfsContent {
    param(
        [Parameter(Mandatory = $true)][string]$SourceRoot,
        [Parameter(Mandatory = $true)][object[]]$Records
    )
    $pointerPrefix = 'version https://git-lfs.github.com/spec/v1'
    foreach ($record in $Records) {
        $native = ([string]$record.Path).Replace([char]47, [IO.Path]::DirectorySeparatorChar)
        $path = Join-Path $SourceRoot $native
        $stream = $null
        try {
            $stream = New-Object IO.FileStream(
                $path,
                [IO.FileMode]::Open,
                [IO.FileAccess]::Read,
                [IO.FileShare]::Read
            )
            $length = [Math]::Min([int64]256, $stream.Length)
            [byte[]]$prefix = New-Object byte[] ([int]$length)
            $read = $stream.Read($prefix, 0, $prefix.Length)
            $text = $script:Ascii.GetString($prefix, 0, $read)
            if ($text.StartsWith($pointerPrefix, [StringComparison]::Ordinal) -and
                ($text.Length -eq $pointerPrefix.Length -or
                    $text[$pointerPrefix.Length] -eq [char]10 -or
                    $text[$pointerPrefix.Length] -eq [char]13)) {
                Fail-CIReleaseWorkspace -Code 'CRW_LFS_FORBIDDEN'
            }
        } finally {
            if ($null -ne $stream) { $stream.Dispose() }
        }

        $leaf = [IO.Path]::GetFileName($native)
        if ($leaf -ieq '.gitattributes') {
            if ([int64]$record.Size -gt $script:MaximumGitAttributesBytes) {
                Fail-CIReleaseWorkspace -Code 'CRW_GIT_ATTRIBUTES_TOO_LARGE'
            }
            [byte[]]$bytes = [IO.File]::ReadAllBytes($path)
            try {
                $attributes = $script:Utf8Strict.GetString($bytes)
            } catch {
                Fail-CIReleaseWorkspace -Code 'CRW_GIT_ATTRIBUTES_INVALID'
            }
            if ([regex]::IsMatch(
                $attributes,
                '(?im)(?:^|\s)filter\s*=\s*lfs(?:\s|$)',
                [Text.RegularExpressions.RegexOptions]::CultureInvariant
            )) {
                Fail-CIReleaseWorkspace -Code 'CRW_LFS_FORBIDDEN'
            }
        }
    }
}

function Expand-VerifiedGitArchive {
    param(
        [Parameter(Mandatory = $true)][string]$ArchivePath,
        [Parameter(Mandatory = $true)][string]$SourceRoot,
        [Parameter(Mandatory = $true)]$Tree,
        [Parameter(Mandatory = $true)][int]$ObjectIdLength
    )
    try {
        Add-Type -AssemblyName System.IO.Compression -ErrorAction Stop
        Add-Type -AssemblyName System.IO.Compression.FileSystem -ErrorAction Stop
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_ZIP_RUNTIME_UNAVAILABLE'
    }

    $archiveStream = $null
    $archive = $null
    try {
        $archiveStream = New-Object IO.FileStream(
            $ArchivePath,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read,
            1048576,
            [IO.FileOptions]::SequentialScan
        )
        $archive = New-Object IO.Compression.ZipArchive(
            $archiveStream,
            [IO.Compression.ZipArchiveMode]::Read,
            $false
        )

        $fileEntries = New-Object 'System.Collections.Generic.Dictionary[string,object]' (
            [StringComparer]::Ordinal
        )
        $archiveExact = New-Object 'System.Collections.Generic.HashSet[string]' (
            [StringComparer]::Ordinal
        )
        $archiveCase = New-Object 'System.Collections.Generic.HashSet[string]' (
            [StringComparer]::OrdinalIgnoreCase
        )
        $archiveNfd = New-Object 'System.Collections.Generic.HashSet[string]' (
            [StringComparer]::Ordinal
        )
        foreach ($entry in $archive.Entries) {
            $entryPath = [string]$entry.FullName
            $isDirectory = $entryPath.EndsWith('/', [StringComparison]::Ordinal)
            $portablePath = if ($isDirectory) {
                $entryPath.Substring(0, $entryPath.Length - 1)
            } else {
                $entryPath
            }
            Assert-PortableRelativePath -RelativePath $portablePath
            Add-PortablePathKey `
                -Path $portablePath `
                -Exact $archiveExact `
                -CaseInsensitive $archiveCase `
                -Nfd $archiveNfd
            $unixType = Get-ArchiveUnixType -ExternalAttributes $entry.ExternalAttributes
            if ($isDirectory) {
                if (-not $Tree.Directories.Contains($portablePath) -or
                    $entry.Length -ne 0 -or
                    ($unixType -ne 0 -and $unixType -ne 0x4000)) {
                    Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_TREE_MISMATCH'
                }
                continue
            }
            if (-not $Tree.RecordByPath.ContainsKey($portablePath) -or
                ($unixType -ne 0 -and $unixType -ne 0x8000)) {
                Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_TREE_MISMATCH'
            }
            $record = $Tree.RecordByPath[$portablePath]
            if ([int64]$entry.Length -ne [int64]$record.Size) {
                Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_BLOB_MISMATCH'
            }
            $fileEntries.Add($portablePath, $entry)
        }
        if ($fileEntries.Count -ne $Tree.Records.Count) {
            Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_TREE_MISMATCH'
        }

        $sortedDirectories = @($Tree.Directories | Sort-Object {
            ([string]$_).Split([char]47).Count
        }, { [string]$_ })
        foreach ($directory in $sortedDirectories) {
            $native = ([string]$directory).Replace([char]47, [IO.Path]::DirectorySeparatorChar)
            $destination = Join-Path $SourceRoot $native
            if (Test-Path -LiteralPath $destination) {
                Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_EXTRACT_RACE'
            }
            try {
                [void][IO.Directory]::CreateDirectory($destination)
            } catch {
                Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_EXTRACT_FAILED'
            }
            Assert-NoReparseAncestry -Path $destination -Code 'CRW_ARCHIVE_EXTRACT_RACE'
        }

        foreach ($record in $Tree.Records) {
            $native = ([string]$record.Path).Replace([char]47, [IO.Path]::DirectorySeparatorChar)
            $destination = Join-Path $SourceRoot $native
            if (-not (Test-PathInside `
                    -Candidate ([IO.Path]::GetFullPath($destination)) `
                    -Container $SourceRoot) -or
                (Test-Path -LiteralPath $destination)) {
                Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_EXTRACT_RACE'
            }
            $entryStream = $null
            $destinationStream = $null
            try {
                $entryStream = $fileEntries[[string]$record.Path].Open()
                $destinationStream = New-Object IO.FileStream(
                    $destination,
                    [IO.FileMode]::CreateNew,
                    [IO.FileAccess]::Write,
                    [IO.FileShare]::None,
                    1048576,
                    [IO.FileOptions]::SequentialScan
                )
                $actualObjectId = Get-GitBlobHash `
                    -InputStream $entryStream `
                    -OutputStream $destinationStream `
                    -ExpectedSize ([int64]$record.Size) `
                    -ObjectIdLength $ObjectIdLength
                $destinationStream.Flush($true)
            } catch {
                if ($_.Exception.Message.StartsWith(
                    'CI_RELEASE_WORKSPACE_FAIL code=',
                    [StringComparison]::Ordinal
                )) {
                    throw
                }
                Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_EXTRACT_FAILED'
            } finally {
                if ($null -ne $destinationStream) { $destinationStream.Dispose() }
                if ($null -ne $entryStream) { $entryStream.Dispose() }
            }
            if ($actualObjectId -cne [string]$record.ObjectId) {
                Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_BLOB_MISMATCH'
            }
        }
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_RELEASE_WORKSPACE_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIReleaseWorkspace -Code 'CRW_ARCHIVE_INVALID'
    } finally {
        if ($null -ne $archive) { $archive.Dispose() }
        if ($null -ne $archiveStream) { $archiveStream.Dispose() }
    }
    Assert-NoLfsContent -SourceRoot $SourceRoot -Records $Tree.Records
}

function Invoke-BoundedPowerShellBootstrap {
    param(
        [Parameter(Mandatory = $true)][string]$Bootstrap,
        [Parameter(Mandatory = $true)][string]$TrustedTemp,
        [Parameter(Mandatory = $true)][string]$FailureCode
    )
    [byte[]]$bootstrapBytes = $script:Utf8NoBom.GetBytes($Bootstrap + "`n")
    if ($bootstrapBytes.LongLength -gt $script:MaximumChildInputBytes) {
        Fail-CIReleaseWorkspace -Code 'CRW_CHILD_INPUT_TOO_LARGE'
    }
    $process = $null
    try {
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = $script:PowerShellCommand
        $start.Arguments =
            '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command -'
        $start.WorkingDirectory = $TrustedTemp
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardInput = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        foreach ($name in @('TEMP', 'TMP', 'TMPDIR')) {
            $start.EnvironmentVariables[$name] = $TrustedTemp
        }
        $process = New-Object Diagnostics.Process
        $process.StartInfo = $start
        $stopwatch = [Diagnostics.Stopwatch]::StartNew()
        $originalConsoleInputEncoding = [Console]::InputEncoding
        $processStarted = $false
        try {
            try {
                [Console]::InputEncoding = $script:Utf8NoBom
                $processStarted = $process.Start()
            } finally {
                [Console]::InputEncoding = $originalConsoleInputEncoding
            }
        } catch {
            if ($processStarted) {
                Stop-BoundedChildProcess -Process $process
            }
            Fail-CIReleaseWorkspace -Code $FailureCode
        }
        if (-not $processStarted) {
            Fail-CIReleaseWorkspace -Code $FailureCode
        }
        $stdoutTask = [CIReleaseWorkspaceBoundedIO]::CaptureAsync(
            $process.StandardOutput.BaseStream,
            $script:MaximumChildStdoutBytes
        )
        $stderrTask = [CIReleaseWorkspaceBoundedIO]::CaptureAsync(
            $process.StandardError.BaseStream,
            $script:MaximumChildStderrBytes
        )
        $stdinTask = [CIReleaseWorkspaceBoundedIO]::WriteAndCloseAsync(
            $process.StandardInput.BaseStream,
            $bootstrapBytes
        )
        $result = Wait-BoundedChildProcess `
            -Process $process `
            -Stopwatch $stopwatch `
            -StandardInputTask $stdinTask `
            -StandardOutputTask $stdoutTask `
            -StandardErrorTask $stderrTask `
            -TimeoutCode 'CRW_CHILD_TIMEOUT' `
            -CaptureFailureCode 'CRW_CHILD_CAPTURE_FAILED'
        if ($result.StandardOutput.Exceeded -or $result.StandardError.Exceeded) {
            Fail-CIReleaseWorkspace -Code 'CRW_CHILD_OUTPUT_TOO_LARGE'
        }
        Write-BoundedCapturedText `
            -Bytes ([byte[]]$result.StandardOutput.Bytes) `
            -FailureCode 'CRW_CHILD_OUTPUT_ENCODING'
        Write-BoundedCapturedText `
            -Bytes ([byte[]]$result.StandardError.Bytes) `
            -FailureCode 'CRW_CHILD_OUTPUT_ENCODING'
        if ($result.ExitCode -ne 0) {
            Fail-CIReleaseWorkspace -Code $FailureCode
        }
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_RELEASE_WORKSPACE_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIReleaseWorkspace -Code $FailureCode
    } finally {
        if ($null -ne $process) {
            try { $process.Dispose() } catch {}
        }
    }
}

function Invoke-ChildPowerShell {
    param(
        [Parameter(Mandatory = $true)][string]$ScriptPath,
        [Parameter(Mandatory = $true)][Collections.IDictionary]$Arguments,
        [Parameter(Mandatory = $true)][string]$TrustedTemp,
        [Parameter(Mandatory = $true)][string]$FailureCode
    )
    $scriptBase64 = [Convert]::ToBase64String($script:Utf8NoBom.GetBytes($ScriptPath))
    $argumentAssignments = @($Arguments.GetEnumerator() | ForEach-Object {
        $name = [string]$_.Key
        if ($name -cnotmatch '^[A-Za-z][A-Za-z0-9]*$') {
            Fail-CIReleaseWorkspace -Code 'CRW_CHILD_ARGUMENT_INVALID'
        }
        $nameEncoded = [Convert]::ToBase64String($script:Utf8NoBom.GetBytes($name))
        $valueEncoded = [Convert]::ToBase64String(
            $script:Utf8NoBom.GetBytes([string]$_.Value)
        )
        (
            '$parameters[$utf8.GetString([Convert]::FromBase64String(''{0}''))] = ' +
            '$utf8.GetString([Convert]::FromBase64String(''{1}''))'
        ) -f $nameEncoded, $valueEncoded
    })
    $bootstrap = @(
        '$utf8 = New-Object Text.UTF8Encoding($false)',
        '[Console]::OutputEncoding = $utf8',
        ('$scriptPath = $utf8.GetString([Convert]::FromBase64String(''{0}''))' -f $scriptBase64),
        '$parameters = @{}',
        ($argumentAssignments -join "`n"),
        '& $scriptPath @parameters',
        'if ($?) { exit 0 }',
        'exit 1'
    ) -join "`n"
    Invoke-BoundedPowerShellBootstrap `
        -Bootstrap $bootstrap `
        -TrustedTemp $TrustedTemp `
        -FailureCode $FailureCode
}

function Invoke-AuthenticatedVerifierBytes {
    param(
        [Parameter(Mandatory = $true)][byte[]]$VerifierBytes,
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$ManifestPath,
        [Parameter(Mandatory = $true)][string]$ManifestSha256,
        [Parameter(Mandatory = $true)][string]$TrustedTemp,
        [Parameter(Mandatory = $true)][string]$FailureCode
    )
    try {
        [void]$script:Utf8Strict.GetString($VerifierBytes)
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_AUTH_FAILED'
    }
    $verifierBase64 = [Convert]::ToBase64String($VerifierBytes)
    $rootBase64 = [Convert]::ToBase64String($script:Utf8NoBom.GetBytes($Root))
    $manifestBase64 = [Convert]::ToBase64String($script:Utf8NoBom.GetBytes($ManifestPath))
    $pinBase64 = [Convert]::ToBase64String($script:Utf8NoBom.GetBytes($ManifestSha256))
    $bootstrap = @(
        '$utf8 = New-Object Text.UTF8Encoding($false)',
        '[Console]::OutputEncoding = $utf8',
        ('$source = $utf8.GetString([Convert]::FromBase64String(''{0}''))' -f $verifierBase64),
        ('$root = $utf8.GetString([Convert]::FromBase64String(''{0}''))' -f $rootBase64),
        ('$manifest = $utf8.GetString([Convert]::FromBase64String(''{0}''))' -f $manifestBase64),
        ('$pin = $utf8.GetString([Convert]::FromBase64String(''{0}''))' -f $pinBase64),
        '$verifier = [ScriptBlock]::Create($source)',
        '& $verifier -Root $root -ManifestPath $manifest -ManifestSha256 $pin',
        'if ($?) { exit 0 }',
        'exit 1'
    ) -join "`n"
    Invoke-BoundedPowerShellBootstrap `
        -Bootstrap $bootstrap `
        -TrustedTemp $TrustedTemp `
        -FailureCode $FailureCode
}

try {
    if ($Revision -cnotmatch '^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$') {
        Fail-CIReleaseWorkspace -Code 'CRW_REVISION_INVALID'
    }
    if ($ExpectedGeneratorSha256 -cnotmatch '^[0-9a-f]{64}$' -or
        $ExpectedVerifierSha256 -cnotmatch '^[0-9a-f]{64}$') {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_PIN_INVALID'
    }
    $revisionLower = $Revision.ToLowerInvariant()
    $script:RepositoryFull = Get-NormalizedAbsolutePath -Value $RepositoryRoot
    $workFull = Get-NormalizedAbsolutePath -Value $WorkRoot
    Assert-NotFileSystemRoot -Path $script:RepositoryFull
    Assert-NotFileSystemRoot -Path $workFull
    Assert-RootsDisjoint -Roots @($script:RepositoryFull, $workFull)

    if (-not (Test-Path -LiteralPath $script:RepositoryFull -PathType Container)) {
        Fail-CIReleaseWorkspace -Code 'CRW_REPOSITORY_MISSING'
    }
    $workParent = [IO.Path]::GetDirectoryName($workFull)
    if ([string]::IsNullOrWhiteSpace($workParent) -or
        -not (Test-Path -LiteralPath $workParent -PathType Container)) {
        Fail-CIReleaseWorkspace -Code 'CRW_WORK_PARENT_MISSING'
    }
    Assert-NoReparseAncestry -Path $script:RepositoryFull -Code 'CRW_REPOSITORY_REPARSE'
    Assert-NoReparseAncestry -Path $workFull -Code 'CRW_WORK_ROOT_REPARSE'

    $gitMetadata = Join-Path $script:RepositoryFull '.git'
    if (-not (Test-Path -LiteralPath $gitMetadata)) {
        Fail-CIReleaseWorkspace -Code 'CRW_GIT_METADATA_MISSING'
    }
    if (-not (Test-Path -LiteralPath $gitMetadata -PathType Container)) {
        Fail-CIReleaseWorkspace -Code 'CRW_GIT_DIRECTORY_INVALID'
    }
    try {
        $gitMetadataItem = Get-Item -LiteralPath $gitMetadata -Force
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_GIT_METADATA_MISSING'
    }
    if (-not ($gitMetadataItem -is [IO.DirectoryInfo]) -or
        ($gitMetadataItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        Fail-CIReleaseWorkspace -Code 'CRW_REPOSITORY_REPARSE'
    }
    Assert-NoReparseAncestry -Path $gitMetadata -Code 'CRW_REPOSITORY_REPARSE'

    if (Test-Path -LiteralPath $workFull) {
        if (-not (Test-Path -LiteralPath $workFull -PathType Container)) {
            Fail-CIReleaseWorkspace -Code 'CRW_WORK_ROOT_NOT_DIRECTORY'
        }
        Assert-DirectoryEmpty -Path $workFull -Code 'CRW_WORK_ROOT_NOT_EMPTY'
    } else {
        try {
            [void][IO.Directory]::CreateDirectory($workFull)
        } catch {
            Fail-CIReleaseWorkspace -Code 'CRW_WORK_ROOT_CREATE_FAILED'
        }
    }
    Assert-NoReparseAncestry -Path $workFull -Code 'CRW_WORK_ROOT_REPARSE'
    Assert-DirectoryEmpty -Path $workFull -Code 'CRW_WORK_ROOT_NOT_EMPTY'

    try {
        $script:GitCommand = (@(
            Get-Command git -CommandType Application -ErrorAction Stop
        ))[0].Source
        $script:PowerShellCommand = (Get-Process -Id $PID).Path
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_TOOL_MISSING'
    }
    if (-not [IO.Path]::IsPathRooted($script:GitCommand) -or
        -not [IO.Path]::IsPathRooted($script:PowerShellCommand)) {
        Fail-CIReleaseWorkspace -Code 'CRW_TOOL_INVALID'
    }

    $topLevel = Get-NormalizedAbsolutePath -Value (
        Invoke-GitText -Arguments 'rev-parse --show-toplevel' -FailureCode 'CRW_REPOSITORY_INVALID'
    )
    $pathComparison = if ($script:IsWindowsPlatform) {
        [StringComparison]::OrdinalIgnoreCase
    } else {
        [StringComparison]::Ordinal
    }
    if (-not [string]::Equals($topLevel, $script:RepositoryFull, $pathComparison)) {
        Fail-CIReleaseWorkspace -Code 'CRW_REPOSITORY_TOPLEVEL_MISMATCH'
    }
    $expectedGitDirectory = Get-NormalizedAbsolutePath -Value $gitMetadata
    $absoluteGitDirectory = Get-NormalizedAbsolutePath -Value (
        Invoke-GitText `
            -Arguments 'rev-parse --absolute-git-dir' `
            -FailureCode 'CRW_GIT_DIRECTORY_INVALID'
    )
    $commonGitDirectory = Get-NormalizedAbsolutePath -Value (
        Invoke-GitText `
            -Arguments 'rev-parse --path-format=absolute --git-common-dir' `
            -FailureCode 'CRW_GIT_DIRECTORY_INVALID'
    )
    if (-not [string]::Equals($absoluteGitDirectory, $expectedGitDirectory, $pathComparison) -or
        -not [string]::Equals($commonGitDirectory, $expectedGitDirectory, $pathComparison)) {
        Fail-CIReleaseWorkspace -Code 'CRW_GIT_DIRECTORY_INVALID'
    }
    if (Test-Path -LiteralPath (Join-Path $gitMetadata 'objects/info/alternates')) {
        Fail-CIReleaseWorkspace -Code 'CRW_GIT_ALTERNATES_FORBIDDEN'
    }

    $resolvedRevision = Invoke-GitText `
        -Arguments ('rev-parse --verify ' + $revisionLower + '^{commit}') `
        -FailureCode 'CRW_REVISION_NOT_COMMIT'
    if ($resolvedRevision -cnotmatch '^(?:[0-9a-f]{40}|[0-9a-f]{64})$' -or
        $resolvedRevision -cne $revisionLower) {
        Fail-CIReleaseWorkspace -Code 'CRW_REVISION_NOT_COMMIT'
    }
    $headRevision = Invoke-GitText `
        -Arguments 'rev-parse --verify HEAD^{commit}' `
        -FailureCode 'CRW_HEAD_INVALID'
    if ($headRevision -cne $revisionLower) {
        Fail-CIReleaseWorkspace -Code 'CRW_HEAD_MISMATCH'
    }
    $objectIdLength = $revisionLower.Length

    [byte[]]$treeBytes = Invoke-GitBytes `
        -Arguments ('ls-tree -r -z -l --full-tree ' + $revisionLower) `
        -FailureCode 'CRW_TREE_READ_FAILED'
    $tree = Get-GitTreeRecords -TreeBytes $treeBytes -ObjectIdLength $objectIdLength

    $sourceFull = Join-Path $workFull 'source'
    $stageFull = Join-Path $workFull 'stage'
    $artifactFull = Join-Path $workFull 'artifact'
    $cacheFull = Join-Path $workFull 'cache'
    $tempFull = Join-Path $workFull 'temp'
    Assert-RootsDisjoint -Roots @(
        $sourceFull,
        $stageFull,
        $artifactFull,
        $cacheFull,
        $tempFull
    )
    foreach ($directory in @($sourceFull, $artifactFull, $cacheFull, $tempFull)) {
        try {
            [void][IO.Directory]::CreateDirectory($directory)
        } catch {
            Fail-CIReleaseWorkspace -Code 'CRW_LAYOUT_CREATE_FAILED'
        }
        Assert-NoReparseAncestry -Path $directory -Code 'CRW_LAYOUT_REPARSE'
        Assert-DirectoryEmpty -Path $directory -Code 'CRW_LAYOUT_RACE'
    }
    if (Test-Path -LiteralPath $stageFull) {
        Fail-CIReleaseWorkspace -Code 'CRW_LAYOUT_RACE'
    }

    $archivePath = Join-Path $workFull ('.source-' + [Guid]::NewGuid().ToString('N') + '.zip')
    $archiveFailure = $null
    try {
        Write-GitArchive -RevisionValue $revisionLower -Destination $archivePath
        Expand-VerifiedGitArchive `
            -ArchivePath $archivePath `
            -SourceRoot $sourceFull `
            -Tree $tree `
            -ObjectIdLength $objectIdLength
    } catch {
        $archiveFailure = $_
    }
    $archiveCleanupFailed = $false
    try {
        if (Test-Path -LiteralPath $archivePath) {
            $archiveItem = Get-Item -LiteralPath $archivePath -Force
            if (-not ($archiveItem -is [IO.FileInfo]) -or
                ($archiveItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                $archiveCleanupFailed = $true
            } else {
                [IO.File]::Delete($archivePath)
            }
        }
    } catch {
        $archiveCleanupFailed = $true
    }
    Resolve-ArchiveOperationFailure `
        -PrimaryFailure $archiveFailure `
        -CleanupFailed $archiveCleanupFailed
    Assert-DirectoryMatchesGitTree `
        -Root $sourceFull `
        -Tree $tree `
        -ObjectIdLength $objectIdLength

    foreach ($cacheDirectory in @('build', 'module', 'go-tmp', 'gopath')) {
        $path = Join-Path $cacheFull $cacheDirectory
        try {
            [void][IO.Directory]::CreateDirectory($path)
        } catch {
            Fail-CIReleaseWorkspace -Code 'CRW_LAYOUT_CREATE_FAILED'
        }
    }
    $processTemp = Join-Path $tempFull 'process'
    try {
        [void][IO.Directory]::CreateDirectory($processTemp)
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_LAYOUT_CREATE_FAILED'
    }

    [byte[]]$generatorBytes = Get-TreeBoundScriptBytes `
        -Root $sourceFull `
        -Tree $tree `
        -RelativePath 'scripts/New-PublicStaging.ps1' `
        -ObjectIdLength $objectIdLength
    [byte[]]$verifierBytes = Get-TreeBoundScriptBytes `
        -Root $sourceFull `
        -Tree $tree `
        -RelativePath 'scripts/Test-PublicStaging.ps1' `
        -ObjectIdLength $objectIdLength
    if ((Get-BytesSha256 -Bytes $generatorBytes) -cne $ExpectedGeneratorSha256 -or
        (Get-BytesSha256 -Bytes $verifierBytes) -cne $ExpectedVerifierSha256) {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_PIN_MISMATCH'
    }
    $toolSnapshotRoot = Join-Path $tempFull ('staging-tools-' + [Guid]::NewGuid().ToString('N'))
    try {
        [void][IO.Directory]::CreateDirectory($toolSnapshotRoot)
    } catch {
        Fail-CIReleaseWorkspace -Code 'CRW_STAGING_TOOL_SNAPSHOT_FAILED'
    }
    Assert-NoReparseAncestry `
        -Path $toolSnapshotRoot `
        -Code 'CRW_STAGING_TOOL_SNAPSHOT_FAILED'
    $generatorSnapshot = Join-Path $toolSnapshotRoot 'New-PublicStaging.ps1'
    $verifierSnapshot = Join-Path $toolSnapshotRoot 'Test-PublicStaging.ps1'
    Write-AuthenticatedScriptSnapshot -Path $generatorSnapshot -Bytes $generatorBytes
    Write-AuthenticatedScriptSnapshot -Path $verifierSnapshot -Bytes $verifierBytes

    $savedTemp = [ordered]@{}
    foreach ($name in @('TEMP', 'TMP', 'TMPDIR')) {
        $savedTemp[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
        [Environment]::SetEnvironmentVariable($name, $processTemp, 'Process')
    }
    try {
        Invoke-ChildPowerShell `
            -ScriptPath $generatorSnapshot `
            -Arguments ([ordered]@{
                SourceRoot = $sourceFull
                StageRoot = $stageFull
                ArtifactRoot = $artifactFull
            }) `
            -TrustedTemp $processTemp `
            -FailureCode 'CRW_STAGING_FAILED'
    } finally {
        foreach ($name in $savedTemp.Keys) {
            [Environment]::SetEnvironmentVariable($name, $savedTemp[$name], 'Process')
        }
    }
    Assert-DirectoryMatchesGitTree `
        -Root $sourceFull `
        -Tree $tree `
        -ObjectIdLength $objectIdLength
    Assert-DirectoryMatchesGitTree `
        -Root $stageFull `
        -Tree $tree `
        -ObjectIdLength $objectIdLength

    $manifestPath = Join-Path $artifactFull $script:ManifestName
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        Fail-CIReleaseWorkspace -Code 'CRW_MANIFEST_MISSING'
    }
    $manifestSha256 = Get-FileSha256 -Path $manifestPath
    if ($manifestSha256 -cnotmatch '^[0-9a-f]{64}$') {
        Fail-CIReleaseWorkspace -Code 'CRW_MANIFEST_HASH_INVALID'
    }
    Invoke-AuthenticatedVerifierBytes `
        -VerifierBytes $verifierBytes `
        -Root $sourceFull `
        -ManifestPath $manifestPath `
        -ManifestSha256 $manifestSha256 `
        -TrustedTemp $processTemp `
        -FailureCode 'CRW_SOURCE_VERIFY_FAILED'
    Invoke-AuthenticatedVerifierBytes `
        -VerifierBytes $verifierBytes `
        -Root $stageFull `
        -ManifestPath $manifestPath `
        -ManifestSha256 $manifestSha256 `
        -TrustedTemp $processTemp `
        -FailureCode 'CRW_POST_VERIFY_FAILED'

    $workEntries = @(Get-ChildItem -LiteralPath $workFull -Force)
    $expectedWorkEntries = @('artifact', 'cache', 'source', 'stage', 'temp')
    $actualWorkEntries = @($workEntries | ForEach-Object { $_.Name } | Sort-Object)
    if (($actualWorkEntries -join "`n") -cne ($expectedWorkEntries -join "`n")) {
        Fail-CIReleaseWorkspace -Code 'CRW_LAYOUT_UNEXPECTED_ENTRY'
    }
    Write-Host (
        'CI_RELEASE_WORKSPACE_PASS ' +
        "revision=$revisionLower " +
        "manifest_sha256=$manifestSha256 " +
        "files=$($tree.Records.Count)"
    )
} catch {
    if ($_.Exception.Message.StartsWith(
        'CI_RELEASE_WORKSPACE_FAIL code=',
        [StringComparison]::Ordinal
    )) {
        Write-Error $_.Exception.Message
    } else {
        Write-Error 'CI_RELEASE_WORKSPACE_FAIL code=CRW_INTERNAL'
    }
    exit 1
}
