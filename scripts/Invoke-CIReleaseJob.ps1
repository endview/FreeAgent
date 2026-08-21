[CmdletBinding(DefaultParameterSetName = 'Prepare')]
param(
    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [switch]$Prepare,

    [Parameter(Mandatory = $true, ParameterSetName = 'Verify')]
    [switch]$Verify,

    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [switch]$GenerateSupplyChain,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [string]$RepositoryRoot,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [string]$Revision,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [string]$WorkRoot,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [string]$ExecutionTempRoot,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [string]$BootstrapPath,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [string]$ExpectedBootstrapSha256,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [string]$ExpectedGeneratorSha256,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [Parameter(Mandatory = $true, ParameterSetName = 'Verify')]
    [string]$ExpectedVerifierSha256,

    [Parameter(Mandatory = $true, ParameterSetName = 'Prepare')]
    [Parameter(Mandatory = $true, ParameterSetName = 'Verify')]
    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [string]$TrustedTemp,

    [Parameter(Mandatory = $true, ParameterSetName = 'Verify')]
    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [string]$SourceRoot,

    [Parameter(Mandatory = $true, ParameterSetName = 'Verify')]
    [string]$StageRoot,

    [Parameter(Mandatory = $true, ParameterSetName = 'Verify')]
    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [string]$ArtifactRoot,

    [Parameter(Mandatory = $true, ParameterSetName = 'Verify')]
    [string]$ManifestPath,

    [Parameter(Mandatory = $true, ParameterSetName = 'Verify')]
    [string]$ManifestSha256,

    [Parameter(Mandatory = $true, ParameterSetName = 'Verify')]
    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [string[]]$RequiredArtifactRelativePath,

    [Parameter(ParameterSetName = 'Verify')]
    [Parameter(ParameterSetName = 'GenerateSupplyChain')]
    [AllowEmptyCollection()]
    [string[]]$OptionalArtifactRelativePath = @(),

    [Parameter(ParameterSetName = 'Verify')]
    [string]$ExpectedArtifactSetSha256 = '',

    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [string]$ModuleCacheRoot,

    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [string]$CreatedUtc,

    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [ValidateSet(
        'permanent',
        'linux-quality',
        'windows',
        'linux-race',
        'cross-build'
    )]
    [string]$JobKind,

    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [bool]$RunSucceeded,

    [Parameter(Mandatory = $true, ParameterSetName = 'GenerateSupplyChain')]
    [string]$MetadataGeneratorSha256,

    [Parameter(ParameterSetName = 'GenerateSupplyChain')]
    [string]$TargetGoos = '',

    [Parameter(ParameterSetName = 'GenerateSupplyChain')]
    [string]$TargetGoarch = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:IsWindowsPlatform = [Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT
$script:PathComparison = if ($script:IsWindowsPlatform) {
    [StringComparison]::OrdinalIgnoreCase
} else {
    [StringComparison]::Ordinal
}
$script:PathComparer = if ($script:IsWindowsPlatform) {
    [StringComparer]::OrdinalIgnoreCase
} else {
    [StringComparer]::Ordinal
}
$script:Utf8Strict = New-Object Text.UTF8Encoding($false, $true)
$script:Utf8NoBom = New-Object Text.UTF8Encoding($false)
$script:MaximumChildInputBytes = 8388608
$script:MaximumChildStdoutBytes = 8388608
$script:MaximumChildStderrBytes = 1048576
$script:MaximumChildMilliseconds = 900000
$script:MaximumAuthenticatedScriptBytes = 4194304
$script:MaximumManifestBytes = 67108864
$script:MaximumArtifactBytes = 1073741824
$script:MaximumArtifactFiles = 10000
$script:MaximumArtifactDirectories = 1000
$script:MaximumArtifactDepth = 32
$script:MaximumSupplyChainInputBytes = 67108864
$script:SupplyChainDirectoryName = 'supply-chain'
$script:SupplyChainSbomPath = 'supply-chain/sbom.spdx.json'
$script:SupplyChainProvenancePath =
    'supply-chain/provenance.unsigned.v1.json'
$script:SupplyChainChecksumsPath = 'supply-chain/checksums.sha256'

function Fail-CIReleaseJob {
    param([Parameter(Mandatory = $true)][string]$Code)
    throw "CI_RELEASE_JOB_FAIL code=$Code"
}

if ($null -eq ('CIReleaseJobBoundedIO' -as [type])) {
    try {
        Add-Type -TypeDefinition @'
using System;
using System.IO;
using System.Threading.Tasks;

public sealed class CIReleaseJobCaptureResult
{
    public byte[] Bytes { get; set; }
    public bool Exceeded { get; set; }
}

public static class CIReleaseJobBoundedIO
{
    public static async Task<CIReleaseJobCaptureResult> CaptureAsync(
        Stream source,
        long maximumBytes)
    {
        if (source == null) throw new ArgumentNullException("source");
        if (maximumBytes < 0) throw new ArgumentOutOfRangeException("maximumBytes");
        var memory = new MemoryStream();
        var buffer = new byte[65536];
        bool exceeded = false;
        try
        {
            int read;
            while ((read = await source.ReadAsync(buffer, 0, buffer.Length)
                .ConfigureAwait(false)) > 0)
            {
                if (!exceeded && memory.Length + read <= maximumBytes)
                {
                    memory.Write(buffer, 0, read);
                }
                else
                {
                    exceeded = true;
                }
            }
            return new CIReleaseJobCaptureResult
            {
                Bytes = memory.ToArray(),
                Exceeded = exceeded
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
'@ -ErrorAction Stop
    } catch {
        Fail-CIReleaseJob -Code 'CIJ_CAPTURE_RUNTIME_UNAVAILABLE'
    }
}

if ($script:IsWindowsPlatform -and
    $null -eq ('CIReleaseJobWindowsStreams' -as [type])) {
    try {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

public static class CIReleaseJobWindowsStreams
{
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    public struct FindStreamData
    {
        public long StreamSize;

        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 296)]
        public string StreamName;
    }

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern IntPtr FindFirstStreamW(
        string fileName,
        int informationLevel,
        out FindStreamData data,
        int flags);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern bool FindNextStreamW(
        IntPtr handle,
        out FindStreamData data);

    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool FindClose(IntPtr handle);
}
'@ -ErrorAction Stop
    } catch {
        Fail-CIReleaseJob -Code 'CIJ_STREAM_RUNTIME_UNAVAILABLE'
    }
}

function ConvertTo-CIJLowerHex {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    return ([BitConverter]::ToString($Bytes)).Replace('-', '').ToLowerInvariant()
}

function Get-CIJSha256Bytes {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        return ConvertTo-CIJLowerHex -Bytes ([byte[]]$sha.ComputeHash($Bytes))
    } finally {
        $sha.Dispose()
    }
}

function New-CIJArtifactRecordFromBytes {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        [byte[]]$digest = $sha.ComputeHash($Bytes)
        return [pscustomobject]@{
            Length = [int64]$Bytes.LongLength
            Sha256Bytes = $digest
            Sha256 = ConvertTo-CIJLowerHex -Bytes $digest
        }
    } finally {
        $sha.Dispose()
    }
}

function Assert-CIJHash {
    param([Parameter(Mandatory = $true)][string]$Value)
    if ($Value -cnotmatch '^[0-9a-f]{64}$') {
        Fail-CIReleaseJob -Code 'CIJ_HASH_INVALID'
    }
}

function Get-CIJAbsolutePath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if ([string]::IsNullOrWhiteSpace($Value)) {
        Fail-CIReleaseJob -Code $Code
    }
    foreach ($character in $Value.ToCharArray()) {
        if ([char]::IsControl($character)) {
            Fail-CIReleaseJob -Code $Code
        }
    }
    $isAbsolute = $false
    if ($script:IsWindowsPlatform) {
        $deviceProbe = $Value.Replace([char]47, [char]92)
        $isDevicePath = $deviceProbe.StartsWith('\\?\', [StringComparison]::Ordinal) -or
            $deviceProbe.StartsWith('\\.\', [StringComparison]::Ordinal) -or
            $deviceProbe.StartsWith('\??\', [StringComparison]::Ordinal)
        $isDriveAbsolute = $Value -match '^[A-Za-z]:[\\/]'
        $isAbsolute = -not $isDevicePath -and $isDriveAbsolute
    } else {
        $isAbsolute = $Value.StartsWith('/', [StringComparison]::Ordinal)
    }
    if (-not $isAbsolute) {
        Fail-CIReleaseJob -Code $Code
    }
    try {
        $full = [IO.Path]::GetFullPath($Value)
        if ($script:IsWindowsPlatform -and (
            $full.StartsWith('\\?\', [StringComparison]::Ordinal) -or
            $full.StartsWith('\\.\', [StringComparison]::Ordinal) -or
            $full.StartsWith('\??\', [StringComparison]::Ordinal)
        )) {
            Fail-CIReleaseJob -Code $Code
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
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_RELEASE_JOB_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIReleaseJob -Code $Code
    }
}

function Assert-CIJNotFileSystemRoot {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $root = [IO.Path]::GetPathRoot($Path)
    if ([string]::IsNullOrWhiteSpace($root)) {
        Fail-CIReleaseJob -Code $Code
    }
    $trimmedPath = $Path.TrimEnd([char[]]@([char]92, [char]47))
    $trimmedRoot = $root.TrimEnd([char[]]@([char]92, [char]47))
    if ([string]::Equals($trimmedPath, $trimmedRoot, $script:PathComparison)) {
        Fail-CIReleaseJob -Code $Code
    }
}

function Test-CIJPathContains {
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

function Assert-CIJDisjointPaths {
    param(
        [Parameter(Mandatory = $true)][string[]]$Paths,
        [Parameter(Mandatory = $true)][string]$Code
    )
    for ($left = 0; $left -lt $Paths.Count; $left++) {
        for ($right = $left + 1; $right -lt $Paths.Count; $right++) {
            if ((Test-CIJPathContains -Parent $Paths[$left] -Candidate $Paths[$right] -AllowEqual) -or
                (Test-CIJPathContains -Parent $Paths[$right] -Candidate $Paths[$left] -AllowEqual)) {
                Fail-CIReleaseJob -Code $Code
            }
        }
    }
}

function Assert-CIJStrictDescendant {
    param(
        [Parameter(Mandatory = $true)][string]$Parent,
        [Parameter(Mandatory = $true)][string]$Candidate,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not (Test-CIJPathContains -Parent $Parent -Candidate $Candidate)) {
        Fail-CIReleaseJob -Code $Code
    }
}

function Assert-CIJNoReparseAncestry {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $probe = $Path
    while (-not [string]::IsNullOrWhiteSpace($probe)) {
        if (Test-Path -LiteralPath $probe) {
            try {
                $item = Get-Item -LiteralPath $probe -Force
            } catch {
                Fail-CIReleaseJob -Code $Code
            }
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                Fail-CIReleaseJob -Code $Code
            }
        }
        $parent = [IO.Path]::GetDirectoryName($probe)
        if ([string]::IsNullOrWhiteSpace($parent) -or
            $parent -ceq $probe) {
            break
        }
        $probe = $parent
    }
}

function Test-CIJLinkLikeItem {
    param([Parameter(Mandatory = $true)]$Item)
    if (($Item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        return $true
    }
    $linkType = $Item.PSObject.Properties['LinkType']
    if ($null -ne $linkType -and
        -not [string]::IsNullOrWhiteSpace([string]$linkType.Value)) {
        return $true
    }
    $target = $Item.PSObject.Properties['Target']
    if ($null -ne $target -and $null -ne $target.Value) {
        if ($target.Value -is [Array]) {
            if (@($target.Value).Count -gt 0) { return $true }
        } elseif (-not [string]::IsNullOrWhiteSpace([string]$target.Value)) {
            return $true
        }
    }
    return $false
}

function Assert-CIJNoAlternateDataStreams {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][bool]$Directory,
        [Parameter(Mandatory = $true)][string]$Code
    )
    if (-not $script:IsWindowsPlatform) { return }
    $data = New-Object CIReleaseJobWindowsStreams+FindStreamData
    $handle = [CIReleaseJobWindowsStreams]::FindFirstStreamW(
        $Path,
        0,
        [ref]$data,
        0
    )
    $invalid = [IntPtr](-1)
    if ($handle -eq $invalid) {
        $errorCode = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
        if ($Directory -and $errorCode -eq 38) {
            return
        }
        Fail-CIReleaseJob -Code $Code
    }
    $names = New-Object 'Collections.Generic.List[string]'
    try {
        while ($true) {
            [void]$names.Add([string]$data.StreamName)
            $next = New-Object CIReleaseJobWindowsStreams+FindStreamData
            if (-not [CIReleaseJobWindowsStreams]::FindNextStreamW(
                $handle,
                [ref]$next
            )) {
                $errorCode = [Runtime.InteropServices.Marshal]::GetLastWin32Error()
                if ($errorCode -ne 38) {
                    Fail-CIReleaseJob -Code $Code
                }
                break
            }
            $data = $next
        }
    } finally {
        if (-not [CIReleaseJobWindowsStreams]::FindClose($handle)) {
            Fail-CIReleaseJob -Code $Code
        }
    }
    if ($Directory) {
        if ($names.Count -ne 0) {
            Fail-CIReleaseJob -Code $Code
        }
    } elseif ($names.Count -ne 1 -or $names[0] -cne '::$DATA') {
        Fail-CIReleaseJob -Code $Code
    }
}

function Read-CIJBoundedRegularFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][int64]$MaximumBytes,
        [Parameter(Mandatory = $true)][string]$Code
    )
    $stream = $null
    try {
        $item = Get-Item -LiteralPath $Path -Force
        if (-not ($item -is [IO.FileInfo]) -or
            (Test-CIJLinkLikeItem -Item $item) -or
            ($item.Attributes -band [IO.FileAttributes]::SparseFile) -ne 0 -or
            $item.Length -gt $MaximumBytes) {
            Fail-CIReleaseJob -Code $Code
        }
        $stream = New-Object IO.FileStream(
            $Path,
            [IO.FileMode]::Open,
            [IO.FileAccess]::Read,
            [IO.FileShare]::Read,
            1048576,
            [IO.FileOptions]::SequentialScan
        )
        if ($stream.Length -gt $MaximumBytes -or
            $stream.Length -gt [int]::MaxValue) {
            Fail-CIReleaseJob -Code $Code
        }
        [byte[]]$bytes = New-Object byte[] ([int]$stream.Length)
        $offset = 0
        while ($offset -lt $bytes.Length) {
            $read = $stream.Read($bytes, $offset, $bytes.Length - $offset)
            if ($read -le 0) {
                Fail-CIReleaseJob -Code $Code
            }
            $offset += $read
        }
        if ($stream.ReadByte() -ne -1) {
            Fail-CIReleaseJob -Code $Code
        }
        return ,$bytes
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_RELEASE_JOB_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIReleaseJob -Code $Code
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Get-CIJAuthenticatedScript {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$ExpectedSha256,
        [Parameter(Mandatory = $true)][string]$Code
    )
    Assert-CIJHash -Value $ExpectedSha256
    $full = Get-CIJAbsolutePath -Value $Path -Code $Code
    Assert-CIJNoReparseAncestry -Path $full -Code $Code
    try {
        [byte[]]$bytes = Read-CIJBoundedRegularFile `
            -Path $full `
            -MaximumBytes $script:MaximumAuthenticatedScriptBytes `
            -Code $Code
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_RELEASE_JOB_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIReleaseJob -Code $Code
    }
    if ((Get-CIJSha256Bytes -Bytes $bytes) -cne $ExpectedSha256) {
        Fail-CIReleaseJob -Code $Code
    }
    try {
        $source = $script:Utf8Strict.GetString($bytes)
        $tokens = $null
        $parseErrors = $null
        [void][Management.Automation.Language.Parser]::ParseInput(
            $source,
            [ref]$tokens,
            [ref]$parseErrors
        )
        if (@($parseErrors).Count -ne 0) {
            Fail-CIReleaseJob -Code $Code
        }
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_RELEASE_JOB_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIReleaseJob -Code $Code
    }
    return [pscustomobject]@{
        Bytes = $bytes
        Source = $source
        Path = $full
    }
}

function Get-CIJRemainingMilliseconds {
    param([Parameter(Mandatory = $true)][Diagnostics.Stopwatch]$Stopwatch)
    $remaining = [int64]$script:MaximumChildMilliseconds - $Stopwatch.ElapsedMilliseconds
    if ($remaining -le 0) { return 0 }
    if ($remaining -gt [int]::MaxValue) { return [int]::MaxValue }
    return [int]$remaining
}

function Stop-CIJChildNoThrow {
    param([Parameter(Mandatory = $true)][Diagnostics.Process]$Process)
    try {
        if (-not $Process.HasExited) { $Process.Kill() }
    } catch {}
    foreach ($stream in @(
        { $Process.StandardInput.BaseStream },
        { $Process.StandardOutput.BaseStream },
        { $Process.StandardError.BaseStream }
    )) {
        try { (& $stream).Dispose() } catch {}
    }
}

function Wait-CIJTask {
    param(
        [Parameter(Mandatory = $true)][Threading.Tasks.Task]$Task,
        [Parameter(Mandatory = $true)][Diagnostics.Process]$Process,
        [Parameter(Mandatory = $true)][Diagnostics.Stopwatch]$Stopwatch,
        [Parameter(Mandatory = $true)][string]$FailureCode
    )
    $remaining = Get-CIJRemainingMilliseconds -Stopwatch $Stopwatch
    if ($remaining -le 0) {
        Stop-CIJChildNoThrow -Process $Process
        Fail-CIReleaseJob -Code 'CIJ_CHILD_TIMEOUT'
    }
    try {
        $completed = $Task.Wait($remaining)
    } catch {
        Stop-CIJChildNoThrow -Process $Process
        if ((Get-CIJRemainingMilliseconds -Stopwatch $Stopwatch) -le 0) {
            Fail-CIReleaseJob -Code 'CIJ_CHILD_TIMEOUT'
        }
        Fail-CIReleaseJob -Code $FailureCode
    }
    if (-not $completed) {
        Stop-CIJChildNoThrow -Process $Process
        Fail-CIReleaseJob -Code 'CIJ_CHILD_TIMEOUT'
    }
    if ($Task.IsCanceled -or $Task.IsFaulted) {
        Stop-CIJChildNoThrow -Process $Process
        Fail-CIReleaseJob -Code $FailureCode
    }
}

function Write-CIJCapturedText {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [byte[]]$Bytes,
        [Parameter(Mandatory = $true)][string]$Prefix
    )
    if ($Bytes.Length -eq 0) { return }
    try {
        $text = $script:Utf8Strict.GetString($Bytes)
    } catch {
        Fail-CIReleaseJob -Code 'CIJ_CHILD_OUTPUT_ENCODING'
    }
    foreach ($line in ($text -split '\r?\n')) {
        if (-not [string]::IsNullOrEmpty($line)) {
            Write-Host "$Prefix $line"
        }
    }
}

function Remove-CIJSensitiveChildEnvironment {
    param([Parameter(Mandatory = $true)][Diagnostics.ProcessStartInfo]$StartInfo)
    foreach ($key in @($StartInfo.EnvironmentVariables.Keys)) {
        $name = [string]$key
        if ($name.StartsWith('GITHUB_', [StringComparison]::OrdinalIgnoreCase) -or
            $name.StartsWith('ACTIONS_', [StringComparison]::OrdinalIgnoreCase) -or
            $name.StartsWith('RUNNER_', [StringComparison]::OrdinalIgnoreCase) -or
            $name -ieq 'CI' -or
            $name -match '(?i)(?:TOKEN|SECRET|PASSWORD|PASSWD|API[_-]?KEY|ACCESS[_-]?KEY|PRIVATE[_-]?KEY|CREDENTIAL)') {
            [void]$StartInfo.EnvironmentVariables.Remove($name)
        }
    }
}

function Invoke-CIJScriptBytes {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Bytes,
        [Parameter(Mandatory = $true)][Collections.IDictionary]$NamedArguments,
        [Parameter(Mandatory = $true)][string]$TrustedTempPath,
        [Parameter(Mandatory = $true)][string]$FailureCode
    )
    $trusted = Get-CIJAbsolutePath -Value $TrustedTempPath -Code 'CIJ_TEMP_INVALID'
    if (-not (Test-Path -LiteralPath $trusted -PathType Container)) {
        Fail-CIReleaseJob -Code 'CIJ_TEMP_INVALID'
    }
    Assert-CIJNoReparseAncestry -Path $trusted -Code 'CIJ_TEMP_INVALID'

    $scriptBase64 = [Convert]::ToBase64String($Bytes)
    $argumentAssignments = @($NamedArguments.GetEnumerator() | ForEach-Object {
        $name = [string]$_.Key
        if ($name -cnotmatch '^[A-Za-z][A-Za-z0-9]*$') {
            Fail-CIReleaseJob -Code 'CIJ_CHILD_ARGUMENT_INVALID'
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
        ('$source = $utf8.GetString([Convert]::FromBase64String(''{0}''))' -f $scriptBase64),
        '$parameters = @{}',
        ($argumentAssignments -join "`n"),
        '$entry = [ScriptBlock]::Create($source)',
        '& $entry @parameters',
        'if ($?) { exit 0 }',
        'exit 1'
    ) -join "`n"
    [byte[]]$bootstrapBytes = $script:Utf8NoBom.GetBytes($bootstrap + "`n")
    if ($bootstrapBytes.LongLength -gt $script:MaximumChildInputBytes) {
        Fail-CIReleaseJob -Code 'CIJ_CHILD_INPUT_TOO_LARGE'
    }

    $process = New-Object Diagnostics.Process
    try {
        $start = New-Object Diagnostics.ProcessStartInfo
        $start.FileName = (Get-Process -Id $PID).Path
        $start.Arguments =
            '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command -'
        $start.WorkingDirectory = $trusted
        $start.UseShellExecute = $false
        $start.CreateNoWindow = $true
        $start.RedirectStandardInput = $true
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        Remove-CIJSensitiveChildEnvironment -StartInfo $start
        foreach ($name in @('TEMP', 'TMP', 'TMPDIR')) {
            $start.EnvironmentVariables[$name] = $trusted
        }
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
            if (-not $processStarted) {
                Fail-CIReleaseJob -Code $FailureCode
            }
        } catch {
            if ($processStarted) {
                Stop-CIJChildNoThrow -Process $process
            }
            if ($_.Exception.Message.StartsWith(
                'CI_RELEASE_JOB_FAIL code=',
                [StringComparison]::Ordinal
            )) {
                throw
            }
            Fail-CIReleaseJob -Code $FailureCode
        }
        $stdoutTask = [CIReleaseJobBoundedIO]::CaptureAsync(
            $process.StandardOutput.BaseStream,
            $script:MaximumChildStdoutBytes
        )
        $stderrTask = [CIReleaseJobBoundedIO]::CaptureAsync(
            $process.StandardError.BaseStream,
            $script:MaximumChildStderrBytes
        )
        $stdinTask = [CIReleaseJobBoundedIO]::WriteAndCloseAsync(
            $process.StandardInput.BaseStream,
            $bootstrapBytes
        )
        Wait-CIJTask `
            -Task $stdinTask `
            -Process $process `
            -Stopwatch $stopwatch `
            -FailureCode 'CIJ_CHILD_INPUT_FAILED'
        $remaining = Get-CIJRemainingMilliseconds -Stopwatch $stopwatch
        if ($remaining -le 0) {
            Stop-CIJChildNoThrow -Process $process
            Fail-CIReleaseJob -Code 'CIJ_CHILD_TIMEOUT'
        }
        try {
            $exited = $process.WaitForExit($remaining)
        } catch {
            Stop-CIJChildNoThrow -Process $process
            Fail-CIReleaseJob -Code 'CIJ_CHILD_WAIT_FAILED'
        }
        if (-not $exited) {
            Stop-CIJChildNoThrow -Process $process
            Fail-CIReleaseJob -Code 'CIJ_CHILD_TIMEOUT'
        }
        Wait-CIJTask `
            -Task $stdoutTask `
            -Process $process `
            -Stopwatch $stopwatch `
            -FailureCode 'CIJ_CHILD_CAPTURE_FAILED'
        Wait-CIJTask `
            -Task $stderrTask `
            -Process $process `
            -Stopwatch $stopwatch `
            -FailureCode 'CIJ_CHILD_CAPTURE_FAILED'
        $stdout = $stdoutTask.Result
        $stderr = $stderrTask.Result
        if ($stdout.Exceeded -or $stderr.Exceeded) {
            Fail-CIReleaseJob -Code 'CIJ_CHILD_OUTPUT_TOO_LARGE'
        }
        Write-CIJCapturedText -Bytes ([byte[]]$stdout.Bytes) -Prefix 'CI_CHILD_OUT:'
        Write-CIJCapturedText -Bytes ([byte[]]$stderr.Bytes) -Prefix 'CI_CHILD_ERR:'
        if ($process.ExitCode -ne 0) {
            Fail-CIReleaseJob -Code $FailureCode
        }
    } finally {
        try { $process.Dispose() } catch {}
    }
}

function Assert-CIJPortableRelativePath {
    param([Parameter(Mandatory = $true)][string]$RelativePath)
    try {
        $isPathRooted = [IO.Path]::IsPathRooted($RelativePath)
    } catch {
        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_ALLOWLIST_INVALID'
    }
    if ([string]::IsNullOrEmpty($RelativePath) -or
        $RelativePath.StartsWith('/', [StringComparison]::Ordinal) -or
        $RelativePath.Contains([char]92) -or
        $isPathRooted -or
        $script:Utf8NoBom.GetByteCount($RelativePath) -gt 4096 -or
        -not [string]::Equals(
            $RelativePath,
            $RelativePath.Normalize([Text.NormalizationForm]::FormC),
            [StringComparison]::Ordinal
        )) {
        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_ALLOWLIST_INVALID'
    }
    foreach ($segment in $RelativePath.Split([char]47)) {
        if ([string]::IsNullOrEmpty($segment) -or
            $segment -ceq '.' -or
            $segment -ceq '..' -or
            $segment.EndsWith(' ', [StringComparison]::Ordinal) -or
            $segment.EndsWith('.', [StringComparison]::Ordinal) -or
            $script:Utf8NoBom.GetByteCount($segment) -gt 255 -or
            $segment.IndexOfAny([char[]]@(
                '<', '>', ':', '"', '/', [char]92, '|', '?', '*'
            )) -ge 0) {
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_ALLOWLIST_INVALID'
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
                Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_ALLOWLIST_INVALID'
            }
        }
        $baseName = ($segment -split '\.', 2)[0]
        if ($baseName -match '^(?i:CON|PRN|AUX|NUL|COM(?:[1-9]|\u00B9|\u00B2|\u00B3)|LPT(?:[1-9]|\u00B9|\u00B2|\u00B3))$' -or
            $segment -ieq '.git') {
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_ALLOWLIST_INVALID'
        }
    }
}

function Get-CIJRelativePath {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path
    )
    if (-not $Path.StartsWith(
        $Root + [IO.Path]::DirectorySeparatorChar,
        $script:PathComparison
    )) {
        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
    }
    $relative = $Path.Substring($Root.Length + 1)
    if ($script:IsWindowsPlatform) {
        $relative = $relative.Replace([char]92, [char]47)
    }
    return $relative
}

function Get-CIJArtifactFileSha256 {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][int64]$ExpectedLength
    )
    Assert-CIJNoAlternateDataStreams `
        -Path $Path `
        -Directory $false `
        -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
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
        if ($stream.Length -ne $ExpectedLength) {
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
        }
        $sha = [Security.Cryptography.SHA256]::Create()
        [byte[]]$hash = $sha.ComputeHash($stream)
        if ($stream.Length -ne $ExpectedLength -or
            $stream.Position -ne $ExpectedLength) {
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
        }
        return ,$hash
    } catch {
        if ($_.Exception.Message.StartsWith(
            'CI_RELEASE_JOB_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw
        }
        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        if ($null -ne $stream) { $stream.Dispose() }
    }
}

function Get-CIJArtifactSetSha256 {
    param(
        [Parameter(Mandatory = $true)]$Records,
        [Parameter(Mandatory = $true)][int64]$TotalBytes
    )
    [string[]]$paths = @($Records.Keys)
    [Array]::Sort($paths, [StringComparer]::Ordinal)
    $canonical = New-Object IO.MemoryStream
    $sha = $null
    try {
        [byte[]]$prefix = $script:Utf8NoBom.GetBytes(
            "freeagent-artifact-set-v1`0"
        )
        $canonical.Write($prefix, 0, $prefix.Length)
        foreach ($path in $paths) {
            $record = $Records[$path]
            [byte[]]$pathBytes = $script:Utf8NoBom.GetBytes($path)
            [byte[]]$pathLength = [BitConverter]::GetBytes([uint32]$pathBytes.Length)
            [byte[]]$fileLength = [BitConverter]::GetBytes([int64]$record.Length)
            if ([BitConverter]::IsLittleEndian) {
                [Array]::Reverse($pathLength)
                [Array]::Reverse($fileLength)
            }
            $canonical.Write($pathLength, 0, $pathLength.Length)
            $canonical.Write($pathBytes, 0, $pathBytes.Length)
            $canonical.Write($fileLength, 0, $fileLength.Length)
            $canonical.Write(
                [byte[]]$record.Sha256Bytes,
                0,
                ([byte[]]$record.Sha256Bytes).Length
            )
        }
        [byte[]]$total = [BitConverter]::GetBytes([int64]$TotalBytes)
        if ([BitConverter]::IsLittleEndian) {
            [Array]::Reverse($total)
        }
        $canonical.Write($total, 0, $total.Length)
        $canonical.Position = 0
        $sha = [Security.Cryptography.SHA256]::Create()
        return ConvertTo-CIJLowerHex -Bytes ([byte[]]$sha.ComputeHash($canonical))
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        $canonical.Dispose()
    }
}

function Assert-CIJArtifactAllowlist {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string[]]$Required,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$Optional
    )
    $artifact = Get-CIJAbsolutePath -Value $Root -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
    if (-not (Test-Path -LiteralPath $artifact -PathType Container)) {
        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
    }
    Assert-CIJNotFileSystemRoot `
        -Path $artifact `
        -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
    Assert-CIJNoReparseAncestry -Path $artifact -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'

    $requiredSet = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    $allowedSet = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    $caseSet = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::OrdinalIgnoreCase)
    $nfdSet = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    foreach ($entry in @($Required) + @($Optional)) {
        Assert-CIJPortableRelativePath -RelativePath $entry
        if (-not $allowedSet.Add($entry) -or
            -not $caseSet.Add($entry) -or
            -not $nfdSet.Add($entry.Normalize([Text.NormalizationForm]::FormD))) {
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_ALLOWLIST_INVALID'
        }
    }
    foreach ($entry in $Required) {
        [void]$requiredSet.Add($entry)
    }
    if (-not $requiredSet.Contains('public-tree-manifest.v1.json')) {
        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_ALLOWLIST_INVALID'
    }

    $actualSet = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    $actualDirectories = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    $records = New-Object 'Collections.Generic.Dictionary[string,object]' ([StringComparer]::Ordinal)
    $totalBytes = [int64]0
    $fileCount = 0
    $directoryCount = 1
    $pending = New-Object Collections.Stack
    $pending.Push([pscustomobject]@{ Path = $artifact; Depth = 0 })
    while ($pending.Count -gt 0) {
        $node = $pending.Pop()
        if ([int]$node.Depth -gt $script:MaximumArtifactDepth) {
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
        }
        try {
            $directoryItem = Get-Item -LiteralPath ([string]$node.Path) -Force
        } catch {
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
        }
        if (-not ($directoryItem -is [IO.DirectoryInfo]) -or
            (Test-CIJLinkLikeItem -Item $directoryItem)) {
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
        }
        Assert-CIJNoAlternateDataStreams `
            -Path $directoryItem.FullName `
            -Directory $true `
            -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
        if ([int]$node.Depth -gt 0) {
            $relativeDirectory = Get-CIJRelativePath `
                -Root $artifact `
                -Path $directoryItem.FullName
            Assert-CIJPortableRelativePath -RelativePath $relativeDirectory
            if (-not $actualDirectories.Add($relativeDirectory)) {
                Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
            }
        }
        try {
            Get-ChildItem -LiteralPath $directoryItem.FullName -Force -ErrorAction Stop |
                ForEach-Object {
                    $item = $_
                    if (Test-CIJLinkLikeItem -Item $item) {
                        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
                    }
                    if ([int]$node.Depth + 1 -gt $script:MaximumArtifactDepth) {
                        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
                    }
                    if ($item -is [IO.DirectoryInfo]) {
                        $directoryCount++
                        if ($directoryCount -gt $script:MaximumArtifactDirectories) {
                            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_TOO_LARGE'
                        }
                        $pending.Push([pscustomobject]@{
                            Path = $item.FullName
                            Depth = [int]$node.Depth + 1
                        })
                        return
                    }
                    if (-not ($item -is [IO.FileInfo]) -or
                        ($item.Attributes -band [IO.FileAttributes]::SparseFile) -ne 0) {
                        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
                    }
                    $fileCount++
                    if ($fileCount -gt $script:MaximumArtifactFiles) {
                        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_TOO_LARGE'
                    }
                    $relative = Get-CIJRelativePath `
                        -Root $artifact `
                        -Path $item.FullName
                    Assert-CIJPortableRelativePath -RelativePath $relative
                    if (-not $actualSet.Add($relative) -or
                        -not $allowedSet.Contains($relative)) {
                        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
                    }
                    $totalBytes += [int64]$item.Length
                    if ($totalBytes -gt $script:MaximumArtifactBytes) {
                        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_TOO_LARGE'
                    }
                    [byte[]]$fileHash = Get-CIJArtifactFileSha256 `
                        -Path $item.FullName `
                        -ExpectedLength ([int64]$item.Length)
                    $records.Add($relative, [pscustomobject]@{
                        Length = [int64]$item.Length
                        Sha256Bytes = $fileHash
                        Sha256 = ConvertTo-CIJLowerHex -Bytes $fileHash
                    })
                }
        } catch {
            if ($_.Exception.Message.StartsWith(
                'CI_RELEASE_JOB_FAIL code=',
                [StringComparison]::Ordinal
            )) {
                throw
            }
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
        }
    }
    foreach ($requiredPath in $requiredSet) {
        if (-not $actualSet.Contains($requiredPath)) {
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
        }
    }

    $expectedDirectories = New-Object 'Collections.Generic.HashSet[string]' ([StringComparer]::Ordinal)
    foreach ($filePath in $actualSet) {
        $directory = [IO.Path]::GetDirectoryName(
            $filePath.Replace([char]47, [IO.Path]::DirectorySeparatorChar)
        )
        while (-not [string]::IsNullOrWhiteSpace($directory)) {
            [void]$expectedDirectories.Add($directory.Replace([char]92, [char]47))
            $directory = [IO.Path]::GetDirectoryName($directory)
        }
    }
    if ($actualDirectories.Count -ne $expectedDirectories.Count) {
        Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
    }
    foreach ($relativeDirectory in $actualDirectories) {
        if (-not $expectedDirectories.Contains($relativeDirectory)) {
            Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
        }
    }
    $setHash = Get-CIJArtifactSetSha256 `
        -Records $records `
        -TotalBytes $totalBytes
    return [pscustomobject]@{
        ArtifactSetSha256 = $setHash
        FileCount = $fileCount
        TotalBytes = $totalBytes
        Records = $records
    }
}

function Get-CIJSha1Bytes {
    param([Parameter(Mandatory = $true)][byte[]]$Bytes)
    $sha = [Security.Cryptography.SHA1]::Create()
    try {
        return ConvertTo-CIJLowerHex -Bytes ([byte[]]$sha.ComputeHash($Bytes))
    } finally {
        $sha.Dispose()
    }
}

function Skip-CIJJsonWhitespace {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][ref]$Position
    )
    while ($Position.Value -lt $Text.Length -and
        $Text[$Position.Value] -in @([char]9, [char]10, [char]13, [char]32)) {
        $Position.Value++
    }
}

function Read-CIJJsonStringToken {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][ref]$Position
    )
    if ($Position.Value -ge $Text.Length -or
        $Text[$Position.Value] -cne [char]34) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    $start = $Position.Value
    $Position.Value++
    $escaped = $false
    while ($Position.Value -lt $Text.Length) {
        $character = $Text[$Position.Value]
        if (-not $escaped) {
            if ([int]$character -lt 32) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            if ($character -ceq [char]34) {
                $Position.Value++
                $literal = $Text.Substring(
                    $start,
                    $Position.Value - $start
                )
                try {
                    return [string](ConvertFrom-Json -InputObject $literal)
                } catch {
                    Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
                }
            }
            if ($character -ceq [char]92) {
                $escaped = $true
            }
        } else {
            if ($character -ceq 'u') {
                if ($Position.Value + 4 -ge $Text.Length) {
                    Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
                }
                for ($offset = 1; $offset -le 4; $offset++) {
                    if ($Text[$Position.Value + $offset] -cnotmatch
                        '^[0-9A-Fa-f]$') {
                        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
                    }
                }
                $Position.Value += 4
            } elseif ($character -cnotmatch '^["\\/bfnrt]$') {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            $escaped = $false
        }
        $Position.Value++
    }
    Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
}

function Read-CIJJsonValue {
    param(
        [Parameter(Mandatory = $true)][string]$Text,
        [Parameter(Mandatory = $true)][ref]$Position,
        [Parameter(Mandatory = $true)][int]$Depth
    )
    if ($Depth -gt 64) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    Skip-CIJJsonWhitespace -Text $Text -Position $Position
    if ($Position.Value -ge $Text.Length) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    $character = $Text[$Position.Value]
    if ($character -ceq [char]123) {
        $Position.Value++
        $names = New-Object 'Collections.Generic.HashSet[string]' (
            [StringComparer]::Ordinal
        )
        Skip-CIJJsonWhitespace -Text $Text -Position $Position
        if ($Position.Value -lt $Text.Length -and
            $Text[$Position.Value] -ceq [char]125) {
            $Position.Value++
            return
        }
        while ($true) {
            $name = Read-CIJJsonStringToken -Text $Text -Position $Position
            if (-not $names.Add($name)) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            Skip-CIJJsonWhitespace -Text $Text -Position $Position
            if ($Position.Value -ge $Text.Length -or
                $Text[$Position.Value] -cne [char]58) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            $Position.Value++
            Read-CIJJsonValue `
                -Text $Text `
                -Position $Position `
                -Depth ($Depth + 1)
            Skip-CIJJsonWhitespace -Text $Text -Position $Position
            if ($Position.Value -ge $Text.Length) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            if ($Text[$Position.Value] -ceq [char]125) {
                $Position.Value++
                return
            }
            if ($Text[$Position.Value] -cne [char]44) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            $Position.Value++
            Skip-CIJJsonWhitespace -Text $Text -Position $Position
        }
    }
    if ($character -ceq [char]91) {
        $Position.Value++
        Skip-CIJJsonWhitespace -Text $Text -Position $Position
        if ($Position.Value -lt $Text.Length -and
            $Text[$Position.Value] -ceq [char]93) {
            $Position.Value++
            return
        }
        while ($true) {
            Read-CIJJsonValue `
                -Text $Text `
                -Position $Position `
                -Depth ($Depth + 1)
            Skip-CIJJsonWhitespace -Text $Text -Position $Position
            if ($Position.Value -ge $Text.Length) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            if ($Text[$Position.Value] -ceq [char]93) {
                $Position.Value++
                return
            }
            if ($Text[$Position.Value] -cne [char]44) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            $Position.Value++
        }
    }
    if ($character -ceq [char]34) {
        [void](Read-CIJJsonStringToken -Text $Text -Position $Position)
        return
    }
    $start = $Position.Value
    while ($Position.Value -lt $Text.Length -and
        $Text[$Position.Value] -notin @(
            [char]9, [char]10, [char]13, [char]32,
            [char]44, [char]93, [char]125
        )) {
        $Position.Value++
    }
    $lexeme = $Text.Substring($start, $Position.Value - $start)
    if ($lexeme -cnotmatch
        '^(?:true|false|null|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)$') {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
}

function ConvertFrom-CIJStrictJson {
    param(
        [Parameter(Mandatory = $true)][byte[]]$Bytes,
        [string]$EmptyPropertyNameReplacement = ''
    )
    if ($Bytes.Length -ge 3 -and
        $Bytes[0] -eq 0xEF -and
        $Bytes[1] -eq 0xBB -and
        $Bytes[2] -eq 0xBF) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    try {
        $text = $script:Utf8Strict.GetString($Bytes)
    } catch {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    $position = 0
    Read-CIJJsonValue -Text $text -Position ([ref]$position) -Depth 0
    Skip-CIJJsonWhitespace -Text $text -Position ([ref]$position)
    if ($position -ne $text.Length) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    if (-not [string]::IsNullOrEmpty($EmptyPropertyNameReplacement)) {
        $emptyPropertyMatches = [regex]::Matches(
            $text,
            '""(?=\s*:)',
            [Text.RegularExpressions.RegexOptions]::CultureInvariant
        )
        if ($EmptyPropertyNameReplacement -cnotmatch '^[a-z_]+$' -or
            $emptyPropertyMatches.Count -ne 1 -or
            [regex]::IsMatch(
                $text,
                '"' + [regex]::Escape($EmptyPropertyNameReplacement) +
                    '"(?=\s*:)',
                [Text.RegularExpressions.RegexOptions]::CultureInvariant
            )) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        # Windows PowerShell 5.1 rejects valid JSON object members whose name is
        # empty. npm lockfile v3 uses exactly one such member for packages[""].
        # The strict parser above validates the original bytes (including
        # duplicate names) before this unambiguous, schema-local projection.
        $text = [regex]::Replace(
            $text,
            '""(?=\s*:)',
            '"' + $EmptyPropertyNameReplacement + '"',
            [Text.RegularExpressions.RegexOptions]::CultureInvariant
        )
    }
    try {
        return ConvertFrom-Json -InputObject $text
    } catch {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
}

function ConvertTo-CIJJsonString {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)
    $builder = New-Object Text.StringBuilder
    [void]$builder.Append([char]34)
    for ($index = 0; $index -lt $Value.Length; $index++) {
        $character = $Value[$index]
        $codePoint = [int]$character
        if ($codePoint -eq 8) {
            [void]$builder.Append('\b')
            continue
        } elseif ($codePoint -eq 9) {
            [void]$builder.Append('\t')
            continue
        } elseif ($codePoint -eq 10) {
            [void]$builder.Append('\n')
            continue
        } elseif ($codePoint -eq 12) {
            [void]$builder.Append('\f')
            continue
        } elseif ($codePoint -eq 13) {
            [void]$builder.Append('\r')
            continue
        } elseif ($codePoint -eq 34) {
            [void]$builder.Append('\"')
            continue
        } elseif ($codePoint -eq 92) {
            [void]$builder.Append('\\')
            continue
        }
        if ($codePoint -lt 32 -or
            $codePoint -eq 0x2028 -or
            $codePoint -eq 0x2029) {
            [void]$builder.Append(('\u{0:x4}' -f $codePoint))
            continue
        }
        if ([char]::IsHighSurrogate($character)) {
            if ($index + 1 -ge $Value.Length -or
                -not [char]::IsLowSurrogate($Value[$index + 1])) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            [void]$builder.Append($character)
            $index++
            [void]$builder.Append($Value[$index])
            continue
        }
        if ([char]::IsLowSurrogate($character)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        [void]$builder.Append($character)
    }
    [void]$builder.Append([char]34)
    return $builder.ToString()
}

function Add-CIJCanonicalJson {
    param(
        [Parameter(Mandatory = $true)][Text.StringBuilder]$Builder,
        [AllowNull()]$Value,
        [Parameter(Mandatory = $true)][int]$Depth
    )
    if ($Depth -gt 64) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
    }
    if ($null -eq $Value) {
        [void]$Builder.Append('null')
        return
    }
    if ($Value -is [string]) {
        [void]$Builder.Append((ConvertTo-CIJJsonString -Value $Value))
        return
    }
    if ($Value -is [bool]) {
        [void]$Builder.Append($(if ($Value) { 'true' } else { 'false' }))
        return
    }
    if ($Value -is [byte] -or
        $Value -is [int16] -or
        $Value -is [int32] -or
        $Value -is [int64] -or
        $Value -is [uint16] -or
        $Value -is [uint32] -or
        $Value -is [uint64]) {
        [void]$Builder.Append(
            ([Convert]::ToString($Value, [Globalization.CultureInfo]::InvariantCulture))
        )
        return
    }
    if ($Value -is [Collections.IDictionary]) {
        [string[]]$keys = @($Value.Keys | ForEach-Object { [string]$_ })
        [Array]::Sort($keys, [StringComparer]::Ordinal)
        [void]$Builder.Append('{')
        for ($index = 0; $index -lt $keys.Count; $index++) {
            if ($index -gt 0) { [void]$Builder.Append(',') }
            [void]$Builder.Append((ConvertTo-CIJJsonString -Value $keys[$index]))
            [void]$Builder.Append(':')
            Add-CIJCanonicalJson `
                -Builder $Builder `
                -Value $Value[$keys[$index]] `
                -Depth ($Depth + 1)
        }
        [void]$Builder.Append('}')
        return
    }
    if ($Value -is [Collections.IEnumerable]) {
        [void]$Builder.Append('[')
        $index = 0
        foreach ($entry in $Value) {
            if ($index -gt 0) { [void]$Builder.Append(',') }
            Add-CIJCanonicalJson `
                -Builder $Builder `
                -Value $entry `
                -Depth ($Depth + 1)
            $index++
        }
        [void]$Builder.Append(']')
        return
    }
    $properties = @($Value.PSObject.Properties)
    if ($properties.Count -gt 0) {
        $dictionary = [ordered]@{}
        foreach ($property in $properties) {
            $dictionary[$property.Name] = $property.Value
        }
        Add-CIJCanonicalJson `
            -Builder $Builder `
            -Value $dictionary `
            -Depth ($Depth + 1)
        return
    }
    Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
}

function ConvertTo-CIJCanonicalJsonBytes {
    param([Parameter(Mandatory = $true)]$Value)
    $builder = New-Object Text.StringBuilder
    Add-CIJCanonicalJson -Builder $builder -Value $Value -Depth 0
    [void]$builder.Append("`n")
    return ,([byte[]]$script:Utf8NoBom.GetBytes($builder.ToString()))
}

function Assert-CIJExactProperties {
    param(
        [Parameter(Mandatory = $true)]$Value,
        [Parameter(Mandatory = $true)][string[]]$Expected
    )
    if ($null -eq $Value) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    [string[]]$actual = @($Value.PSObject.Properties.Name)
    [string[]]$expectedCopy = @($Expected)
    [Array]::Sort($actual, [StringComparer]::Ordinal)
    [Array]::Sort($expectedCopy, [StringComparer]::Ordinal)
    if (($actual -join "`n") -cne ($expectedCopy -join "`n")) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
}

function Get-CIJInputFileRecord {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$RelativePath
    )
    Assert-CIJPortableRelativePath -RelativePath $RelativePath
    $path = Get-CIJAbsolutePath `
        -Value (Join-Path $Root $RelativePath.Replace(
            [char]47,
            [IO.Path]::DirectorySeparatorChar
        )) `
        -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    Assert-CIJStrictDescendant `
        -Parent $Root `
        -Candidate $path `
        -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    Assert-CIJNoReparseAncestry `
        -Path $path `
        -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    [byte[]]$bytes = Read-CIJBoundedRegularFile `
        -Path $path `
        -MaximumBytes $script:MaximumSupplyChainInputBytes `
        -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    return [pscustomobject]@{
        Path = $path
        RelativePath = $RelativePath
        Bytes = $bytes
        Length = [int64]$bytes.Length
        Sha256 = Get-CIJSha256Bytes -Bytes $bytes
    }
}

function Assert-CIJInputRecordsUnchanged {
    param([Parameter(Mandatory = $true)]$Records)
    foreach ($record in $Records) {
        [byte[]]$current = Read-CIJBoundedRegularFile `
            -Path $record.Path `
            -MaximumBytes $script:MaximumSupplyChainInputBytes `
            -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        if ($current.Length -ne $record.Length -or
            (Get-CIJSha256Bytes -Bytes $current) -cne $record.Sha256) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
    }
}

function Get-CIJCanonicalRecordSetSha256 {
    param([Parameter(Mandatory = $true)]$Records)
    [string[]]$names = @($Records.Keys)
    [Array]::Sort($names, [StringComparer]::Ordinal)
    $memory = New-Object IO.MemoryStream
    $sha = $null
    try {
        [byte[]]$prefix = $script:Utf8NoBom.GetBytes(
            "freeagent-supply-chain-input-v1`0"
        )
        $memory.Write($prefix, 0, $prefix.Length)
        foreach ($name in $names) {
            [byte[]]$nameBytes = $script:Utf8NoBom.GetBytes($name)
            [byte[]]$valueBytes = [byte[]]$Records[$name]
            [byte[]]$nameLength = [BitConverter]::GetBytes(
                [uint32]$nameBytes.Length
            )
            [byte[]]$valueLength = [BitConverter]::GetBytes(
                [int64]$valueBytes.Length
            )
            if ([BitConverter]::IsLittleEndian) {
                [Array]::Reverse($nameLength)
                [Array]::Reverse($valueLength)
            }
            $memory.Write($nameLength, 0, $nameLength.Length)
            $memory.Write($nameBytes, 0, $nameBytes.Length)
            $memory.Write($valueLength, 0, $valueLength.Length)
            $memory.Write($valueBytes, 0, $valueBytes.Length)
        }
        $memory.Position = 0
        $sha = [Security.Cryptography.SHA256]::Create()
        return ConvertTo-CIJLowerHex -Bytes ([byte[]]$sha.ComputeHash($memory))
    } finally {
        if ($null -ne $sha) { $sha.Dispose() }
        $memory.Dispose()
    }
}

function ConvertTo-CIJGoCachePath {
    param([Parameter(Mandatory = $true)][string]$Value)
    $builder = New-Object Text.StringBuilder
    foreach ($character in $Value.ToCharArray()) {
        if ($character -cmatch '^[A-Z]$') {
            [void]$builder.Append('!')
            [void]$builder.Append(
                $character.ToString().ToLowerInvariant()
            )
        } elseif ($character -ceq '!') {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        } else {
            [void]$builder.Append($character)
        }
    }
    return $builder.ToString()
}

function Write-CIJNewFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][byte[]]$Bytes
    )
    $stream = $null
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
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
    } finally {
        if ($null -ne $stream) { $stream.Dispose() }
    }
    [byte[]]$current = Read-CIJBoundedRegularFile `
        -Path $Path `
        -MaximumBytes $script:MaximumSupplyChainInputBytes `
        -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
    if ($current.Length -ne $Bytes.Length -or
        (Get-CIJSha256Bytes -Bytes $current) -cne
            (Get-CIJSha256Bytes -Bytes $Bytes)) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
    }
}

function New-CIJSupplyChainMetadata {
    param(
        [Parameter(Mandatory = $true)][string]$Source,
        [Parameter(Mandatory = $true)][string]$Artifact,
        [Parameter(Mandatory = $true)][string]$ModuleCache,
        [Parameter(Mandatory = $true)][string]$TemporaryRoot,
        [Parameter(Mandatory = $true)][string]$RevisionValue,
        [Parameter(Mandatory = $true)][string]$CreatedUtcValue,
        [Parameter(Mandatory = $true)][string]$Kind,
        [Parameter(Mandatory = $true)][bool]$Succeeded,
        [Parameter(Mandatory = $true)][string]$GeneratorSha256,
        [Parameter(Mandatory = $true)][string[]]$Required,
        [Parameter(Mandatory = $true)][AllowEmptyCollection()][string[]]$Optional,
        [string]$Goos = '',
        [string]$Goarch = ''
    )
    if ($RevisionValue -cnotmatch '^(?:[0-9a-f]{40}|[0-9a-f]{64})$') {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    Assert-CIJHash -Value $GeneratorSha256
    $created = [DateTime]::MinValue
    if (-not [DateTime]::TryParseExact(
        $CreatedUtcValue,
        "yyyy-MM-dd'T'HH:mm:ss'Z'",
        [Globalization.CultureInfo]::InvariantCulture,
        [Globalization.DateTimeStyles]::AssumeUniversal -bor
            [Globalization.DateTimeStyles]::AdjustToUniversal,
        [ref]$created
    ) -or $created.ToString(
        "yyyy-MM-dd'T'HH:mm:ss'Z'",
        [Globalization.CultureInfo]::InvariantCulture
    ) -cne $CreatedUtcValue) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    if ($Kind -ceq 'cross-build') {
        if ($Goos -cnotmatch '^(?:windows|linux|darwin)$' -or
            $Goarch -cnotmatch '^(?:amd64|arm64)$') {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
    } elseif (-not [string]::IsNullOrEmpty($Goos) -or
        -not [string]::IsNullOrEmpty($Goarch)) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    foreach ($metadataPath in @(
        $script:SupplyChainSbomPath,
        $script:SupplyChainProvenancePath,
        $script:SupplyChainChecksumsPath
    )) {
        if ($metadataPath -in @($Required) + @($Optional)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
    }
    $destination = Join-Path $Artifact $script:SupplyChainDirectoryName
    if (Test-Path -LiteralPath $destination) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_OUTPUT_EXISTS'
    }
    Assert-CIJNoReparseAncestry `
        -Path $destination `
        -Code 'CIJ_SUPPLY_CHAIN_OUTPUT_EXISTS'

    $baseState = Assert-CIJArtifactAllowlist `
        -Root $Artifact `
        -Required $Required `
        -Optional $Optional

    $dependencyRecord = Get-CIJInputFileRecord `
        -Root $Source `
        -RelativePath 'testdata/release/dependency-licenses.v1.json'
    $assetManifestRecord = Get-CIJInputFileRecord `
        -Root $Source `
        -RelativePath 'testdata/release/distributed-assets.v1.json'
    $frontendManifestRecord = Get-CIJInputFileRecord `
        -Root $Source `
        -RelativePath 'testdata/release/frontend-dependency-licenses.v1.json'
    $goModRecord = Get-CIJInputFileRecord `
        -Root $Source `
        -RelativePath 'go.mod'
    $goSumRecord = Get-CIJInputFileRecord `
        -Root $Source `
        -RelativePath 'go.sum'
    $inputFiles = New-Object 'Collections.Generic.List[object]'
    $inputFilePaths = New-Object 'Collections.Generic.HashSet[string]' (
        $script:PathComparer
    )
    foreach ($record in @(
        $dependencyRecord,
        $assetManifestRecord,
        $frontendManifestRecord,
        $goModRecord,
        $goSumRecord
    )) {
        [void]$inputFiles.Add($record)
        [void]$inputFilePaths.Add($record.Path)
    }
    $dependencyManifest = ConvertFrom-CIJStrictJson -Bytes $dependencyRecord.Bytes
    $assetManifest = ConvertFrom-CIJStrictJson -Bytes $assetManifestRecord.Bytes
    $frontendManifest = ConvertFrom-CIJStrictJson -Bytes $frontendManifestRecord.Bytes
    Assert-CIJExactProperties `
        -Value $dependencyManifest `
        -Expected @(
            'schema_version',
            'kind',
            'main_module',
            'compatibility_policy',
            'license_refs',
            'modules'
        )
    Assert-CIJExactProperties `
        -Value $assetManifest `
        -Expected @('schema_version', 'kind', 'assets')
    Assert-CIJExactProperties `
        -Value $frontendManifest `
        -Expected @(
            'schema_version',
            'kind',
            'package_lock',
            'build_environment',
            'packages',
            'distributed_chunks'
        )
    if ([int]$dependencyManifest.schema_version -ne 1 -or
        [string]$dependencyManifest.kind -cne
            'freeagent-go-dependency-licenses' -or
        [int]$assetManifest.schema_version -ne 1 -or
        [string]$assetManifest.kind -cne 'freeagent-distributed-assets' -or
        [int]$frontendManifest.schema_version -ne 1 -or
        [string]$frontendManifest.kind -cne
            'freeagent-npm-dependency-licenses') {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    Assert-CIJExactProperties `
        -Value $frontendManifest.package_lock `
        -Expected @('path', 'sha256', 'lockfile_version')
    $packageLockRelative = [string]$frontendManifest.package_lock.path
    if ($packageLockRelative -cne 'internal/controlweb/package-lock.json' -or
        [int]$frontendManifest.package_lock.lockfile_version -ne 3 -or
        [string]$frontendManifest.package_lock.sha256 -cnotmatch '^[0-9a-f]{64}$') {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    $packageLockRecord = Get-CIJInputFileRecord `
        -Root $Source `
        -RelativePath $packageLockRelative
    if ($packageLockRecord.Sha256 -cne
        [string]$frontendManifest.package_lock.sha256) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    if ($inputFilePaths.Add($packageLockRecord.Path)) {
        [void]$inputFiles.Add($packageLockRecord)
    }
    $packageLockRootPropertyName = '__freeagent_package_lock_root__'
    $packageLock = ConvertFrom-CIJStrictJson `
        -Bytes $packageLockRecord.Bytes `
        -EmptyPropertyNameReplacement $packageLockRootPropertyName
    Assert-CIJExactProperties `
        -Value $frontendManifest.build_environment `
        -Expected @('node_version', 'npm_version', 'registry', 'install_scripts')
    if ([string]$frontendManifest.build_environment.node_version -cne '24.19.0' -or
        [string]$frontendManifest.build_environment.npm_version -cne '12.0.2' -or
        [string]$frontendManifest.build_environment.registry -cne
            'https://registry.npmjs.org/' -or
        [string]$frontendManifest.build_environment.install_scripts -cne 'disabled' -or
        [int]$packageLock.lockfileVersion -ne 3) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    Assert-CIJExactProperties `
        -Value $dependencyManifest.compatibility_policy `
        -Expected @(
            'id',
            'version',
            'project_license',
            'source_dependency_mode',
            'binary_targets',
            'cgo_enabled',
            'go_version'
        )
    $mainModule = [string]$dependencyManifest.main_module
    $projectLicense = [string](
        $dependencyManifest.compatibility_policy.project_license
    )
    if ($mainModule -cnotmatch '^[A-Za-z0-9._~/-]+$' -or
        $projectLicense -cne 'AGPL-3.0-only') {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    try {
        $goModText = $script:Utf8Strict.GetString($goModRecord.Bytes)
        $goSumText = $script:Utf8Strict.GetString($goSumRecord.Bytes)
    } catch {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    $moduleMatch = [regex]::Match(
        $goModText,
        '(?m)^module ([^\r\n ]+)\r?$'
    )
    $goVersionMatch = [regex]::Match(
        $goModText,
        '(?m)^go ([0-9]+\.[0-9]+(?:\.[0-9]+)?)\r?$'
    )
    if (-not $moduleMatch.Success -or
        $moduleMatch.Groups[1].Value -cne $mainModule -or
        -not $goVersionMatch.Success -or
        ('go' + $goVersionMatch.Groups[1].Value) -cne
            [string]$dependencyManifest.compatibility_policy.go_version) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }

    $goSumEntries = New-Object 'Collections.Generic.Dictionary[string,string]' (
        [StringComparer]::Ordinal
    )
    foreach ($line in @($goSumText -split '\r?\n')) {
        if ([string]::IsNullOrEmpty($line)) { continue }
        $match = [regex]::Match(
            $line,
            '^([^ ]+) ([^ ]+?)(/go\.mod)? (h1:[A-Za-z0-9+/]+={0,2})$'
        )
        if (-not $match.Success) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $kindSuffix = if ($match.Groups[3].Success) { '/go.mod' } else { '' }
        $key = $match.Groups[1].Value + '@' +
            $match.Groups[2].Value + $kindSuffix
        if ($goSumEntries.ContainsKey($key)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        try {
            [byte[]]$decoded = [Convert]::FromBase64String(
                $match.Groups[4].Value.Substring(3)
            )
        } catch {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        if ($decoded.Length -ne 32) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $goSumEntries.Add($key, $match.Groups[4].Value)
    }

    $modules = @($dependencyManifest.modules)
    $assets = @($assetManifest.assets)
    $licenseRefs = @($dependencyManifest.license_refs)
    if ($modules.Count -lt 1 -or $assets.Count -lt 1) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    $moduleIdentities = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $moduleCaseIdentities = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    $noticeIds = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $usedLicenseRefs = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $previousModuleIdentity = ''
    foreach ($module in $modules) {
        Assert-CIJExactProperties `
            -Value $module `
            -Expected @(
                'path',
                'version',
                'module_sum',
                'go_mod_sum',
                'declared_license_expression',
                'source_url',
                'notice_id',
                'compatibility_conclusion',
                'source_spdx_scan',
                'required_files'
            )
        $path = [string]$module.path
        $version = [string]$module.version
        $identity = "$path@$version"
        if ($path -cnotmatch '^[A-Za-z0-9._~/-]+$' -or
            $version -cnotmatch '^v[0-9A-Za-z.+-]+$' -or
            [string]$module.notice_id -cnotmatch '^go-[0-9a-f]{24}$' -or
            -not $moduleIdentities.Add($identity) -or
            -not $moduleCaseIdentities.Add($identity) -or
            -not $noticeIds.Add([string]$module.notice_id) -or
            (-not [string]::IsNullOrEmpty($previousModuleIdentity) -and
                [StringComparer]::Ordinal.Compare(
                    $previousModuleIdentity,
                    $identity
                ) -ge 0)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $previousModuleIdentity = $identity
        foreach ($sumName in @('module_sum', 'go_mod_sum')) {
            $sum = [string]$module.$sumName
            if ($sum -cnotmatch '^h1:[A-Za-z0-9+/]+={0,2}$') {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            try {
                [byte[]]$decoded = [Convert]::FromBase64String(
                    $sum.Substring(3)
                )
            } catch {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            if ($decoded.Length -ne 32) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
        }
        $moduleKey = $identity
        $goModKey = $identity + '/go.mod'
        if (-not $goSumEntries.ContainsKey($moduleKey) -or
            -not $goSumEntries.ContainsKey($goModKey) -or
            $goSumEntries[$moduleKey] -cne [string]$module.module_sum -or
            $goSumEntries[$goModKey] -cne [string]$module.go_mod_sum) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $expression = [string]$module.declared_license_expression
        if ([string]::IsNullOrWhiteSpace($expression) -or
            $expression.IndexOfAny([char[]]@([char]10, [char]13, [char]0)) -ge 0) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        foreach ($licenseMatch in [regex]::Matches(
            $expression,
            'LicenseRef-[A-Za-z0-9.-]+'
        )) {
            [void]$usedLicenseRefs.Add($licenseMatch.Value)
        }
        $requiredLegalFiles = @($module.required_files)
        if ($requiredLegalFiles.Count -lt 1) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        foreach ($requiredLegalFile in $requiredLegalFiles) {
            Assert-CIJExactProperties `
                -Value $requiredLegalFile `
                -Expected @('path', 'role', 'size', 'sha256')
            $requiredFilePath = [string]$requiredLegalFile.path
            Assert-CIJPortableRelativePath -RelativePath $requiredFilePath
            $cacheRelative = (
                (ConvertTo-CIJGoCachePath -Value $path) + '@' +
                (ConvertTo-CIJGoCachePath -Value $version) + '/' +
                $requiredFilePath
            )
            $legalRecord = Get-CIJInputFileRecord `
                -Root $ModuleCache `
                -RelativePath $cacheRelative
            if ($legalRecord.Length -ne [int64]$requiredLegalFile.size -or
                $legalRecord.Sha256 -cne [string]$requiredLegalFile.sha256 -or
                [string]$requiredLegalFile.role -cnotmatch
                    '^(?:license|notice|attribution|copyright|patent)$') {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            if ($inputFilePaths.Add($legalRecord.Path)) {
                [void]$inputFiles.Add($legalRecord)
            }
        }
    }

    $licenseRefIds = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $extractedLicenses = New-Object 'Collections.Generic.List[object]'
    $previousLicenseRef = ''
    foreach ($licenseRef in $licenseRefs) {
        Assert-CIJExactProperties `
            -Value $licenseRef `
            -Expected @(
                'id',
                'name',
                'source_url',
                'text_sha256',
                'applies_to',
                'required_file'
            )
        $licenseId = [string]$licenseRef.id
        if ($licenseId -cnotmatch '^LicenseRef-[A-Za-z0-9.-]+$' -or
            -not $licenseRefIds.Add($licenseId) -or
            (-not [string]::IsNullOrEmpty($previousLicenseRef) -and
                [StringComparer]::Ordinal.Compare(
                    $previousLicenseRef,
                    $licenseId
                ) -ge 0) -or
            -not $usedLicenseRefs.Contains($licenseId) -or
            [string]$licenseRef.applies_to -cnotmatch '^([^@]+)@(.+)$') {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $previousLicenseRef = $licenseId
        $appliesTo = [string]$licenseRef.applies_to
        if (-not $moduleIdentities.Contains($appliesTo)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $separator = $appliesTo.LastIndexOf('@')
        $modulePath = $appliesTo.Substring(0, $separator)
        $moduleVersion = $appliesTo.Substring($separator + 1)
        $cacheRelative = (
            (ConvertTo-CIJGoCachePath -Value $modulePath) + '@' +
            (ConvertTo-CIJGoCachePath -Value $moduleVersion) + '/' +
            [string]$licenseRef.required_file
        )
        $textRecord = Get-CIJInputFileRecord `
            -Root $ModuleCache `
            -RelativePath $cacheRelative
        if ($textRecord.Sha256 -cne [string]$licenseRef.text_sha256) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        try {
            $licenseText = $script:Utf8Strict.GetString($textRecord.Bytes)
        } catch {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        if ([string]::IsNullOrWhiteSpace($licenseText)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        if ($inputFilePaths.Add($textRecord.Path)) {
            [void]$inputFiles.Add($textRecord)
        }
        [void]$extractedLicenses.Add([ordered]@{
            extractedText = $licenseText
            licenseId = $licenseId
            name = [string]$licenseRef.name
            seeAlsos = @([string]$licenseRef.source_url)
        })
    }
    if ($licenseRefIds.Count -ne $usedLicenseRefs.Count) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    foreach ($licenseId in $usedLicenseRefs) {
        if (-not $licenseRefIds.Contains($licenseId)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
    }

    $lockPackagesProperty = $packageLock.PSObject.Properties['packages']
    if ($null -eq $lockPackagesProperty -or
        $null -eq $lockPackagesProperty.Value) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    $lockRootProperty =
        $lockPackagesProperty.Value.PSObject.Properties[
            $packageLockRootPropertyName
        ]
    if ($null -eq $lockRootProperty -or $null -eq $lockRootProperty.Value) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    $lockByIdentity = New-Object 'Collections.Generic.Dictionary[string,object]' (
        [StringComparer]::Ordinal
    )
    $lockByName = New-Object 'Collections.Generic.Dictionary[string,object]' (
        [StringComparer]::Ordinal
    )
    foreach ($property in @($lockPackagesProperty.Value.PSObject.Properties)) {
        $lockPath = [string]$property.Name
        if ($lockPath -ceq $packageLockRootPropertyName) { continue }
        $match = [regex]::Match(
            $lockPath,
            '(?:^|/)node_modules/((?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*)$'
        )
        if (-not $match.Success) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $lockName = $match.Groups[1].Value
        $lockEntry = $property.Value
        $lockVersion = [string]$lockEntry.version
        $lockIdentity = "$lockName@$lockVersion"
        if ($lockVersion -cnotmatch '^[0-9]+\.[0-9]+\.[0-9]+$' -or
            $lockByIdentity.ContainsKey($lockIdentity) -or
            $lockByName.ContainsKey($lockName)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $lockBinding = [pscustomobject]@{
            Name = $lockName
            Identity = $lockIdentity
            Entry = $lockEntry
        }
        $lockByIdentity.Add($lockIdentity, $lockBinding)
        $lockByName.Add($lockName, $lockBinding)
    }
    if ($lockByIdentity.Count -lt 1) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    $runtimeIdentities = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $runtimeQueue = New-Object 'Collections.Generic.Queue[string]'
    $rootDependenciesProperty =
        $lockRootProperty.Value.PSObject.Properties['dependencies']
    if ($null -eq $rootDependenciesProperty -or
        $null -eq $rootDependenciesProperty.Value) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    foreach ($dependency in @($rootDependenciesProperty.Value.PSObject.Properties)) {
        $runtimeQueue.Enqueue([string]$dependency.Name)
    }
    while ($runtimeQueue.Count -gt 0) {
        $dependencyName = $runtimeQueue.Dequeue()
        if (-not $lockByName.ContainsKey($dependencyName)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $binding = $lockByName[$dependencyName]
        if (-not $runtimeIdentities.Add([string]$binding.Identity)) { continue }
        foreach ($dependencyPropertyName in @('dependencies', 'optionalDependencies')) {
            $dependenciesProperty =
                $binding.Entry.PSObject.Properties[$dependencyPropertyName]
            if ($null -eq $dependenciesProperty -or
                $null -eq $dependenciesProperty.Value) { continue }
            foreach ($dependency in @($dependenciesProperty.Value.PSObject.Properties)) {
                $runtimeQueue.Enqueue([string]$dependency.Name)
            }
        }
    }

    $frontendPackages = @($frontendManifest.packages)
    if ($frontendPackages.Count -ne $lockByIdentity.Count) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    $frontendPackageMap = New-Object 'Collections.Generic.Dictionary[string,object]' (
        [StringComparer]::Ordinal
    )
    $previousFrontendIdentity = ''
    foreach ($frontendPackage in $frontendPackages) {
        Assert-CIJExactProperties `
            -Value $frontendPackage `
            -Expected @(
                'name', 'version', 'dependency_kind', 'optional', 'resolved',
                'integrity', 'declared_license_expression', 'source_url',
                'notice_id', 'required_files'
            )
        $name = [string]$frontendPackage.name
        $version = [string]$frontendPackage.version
        $identity = "$name@$version"
        if ($name -cnotmatch '^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$' -or
            $version -cnotmatch '^[0-9]+\.[0-9]+\.[0-9]+$' -or
            -not $lockByIdentity.ContainsKey($identity) -or
            $frontendPackageMap.ContainsKey($identity) -or
            (-not [string]::IsNullOrEmpty($previousFrontendIdentity) -and
                [StringComparer]::Ordinal.Compare(
                    $previousFrontendIdentity,
                    $identity
                ) -ge 0) -or
            [string]$frontendPackage.notice_id -cnotmatch '^npm-[0-9a-f]{24}$' -or
            -not $noticeIds.Add([string]$frontendPackage.notice_id) -or
            -not ($frontendPackage.optional -is [bool])) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $previousFrontendIdentity = $identity
        $expectedDependencyKind = if ($runtimeIdentities.Contains($identity)) {
            'runtime'
        } else {
            'build'
        }
        if ([string]$frontendPackage.dependency_kind -cne
            $expectedDependencyKind) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $lockEntry = $lockByIdentity[$identity].Entry
        $lockOptionalProperty = $lockEntry.PSObject.Properties['optional']
        $lockOptional = $null -ne $lockOptionalProperty -and
            [bool]$lockOptionalProperty.Value
        if ([bool]$frontendPackage.optional -ne $lockOptional -or
            [string]$frontendPackage.resolved -cne [string]$lockEntry.resolved -or
            [string]$frontendPackage.source_url -cne [string]$lockEntry.resolved -or
            [string]$frontendPackage.integrity -cne [string]$lockEntry.integrity -or
            [string]$frontendPackage.declared_license_expression -cne
                [string]$lockEntry.license -or
            [string]$frontendPackage.resolved -cnotmatch '^https://[^\s]+$' -or
            [string]$frontendPackage.integrity -cnotmatch
                '^sha512-[A-Za-z0-9+/]+={0,2}$' -or
            [string]$frontendPackage.declared_license_expression -cmatch
                '[\x00\r\n]' -or
            [string]::IsNullOrWhiteSpace(
                [string]$frontendPackage.declared_license_expression
            )) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        try {
            [byte[]]$integrityBytes = [Convert]::FromBase64String(
                ([string]$frontendPackage.integrity).Substring(7)
            )
        } catch {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        if ($integrityBytes.Length -ne 64 -or
            @($frontendPackage.required_files).Count -ne 1) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $requiredLegalFile = @($frontendPackage.required_files)[0]
        Assert-CIJExactProperties `
            -Value $requiredLegalFile `
            -Expected @('path', 'role', 'size', 'sha256')
        $legalPath = [string]$requiredLegalFile.path
        if (-not $legalPath.StartsWith(
            'third_party/npm/',
            [StringComparison]::Ordinal
        ) -or [string]$requiredLegalFile.role -cne 'license' -or
            [string]$requiredLegalFile.sha256 -cnotmatch '^[0-9a-f]{64}$') {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $legalRecord = Get-CIJInputFileRecord `
            -Root $Source `
            -RelativePath $legalPath
        if ($legalRecord.Length -ne [int64]$requiredLegalFile.size -or
            $legalRecord.Sha256 -cne [string]$requiredLegalFile.sha256) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        try {
            $legalText = $script:Utf8Strict.GetString($legalRecord.Bytes)
        } catch {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        if ([string]::IsNullOrWhiteSpace($legalText)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        if ($inputFilePaths.Add($legalRecord.Path)) {
            [void]$inputFiles.Add($legalRecord)
        }
        $frontendPackageMap.Add($identity, $frontendPackage)
    }
    foreach ($identity in $lockByIdentity.Keys) {
        if (-not $frontendPackageMap.ContainsKey($identity)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
    }
    $frontendChunkMap = New-Object 'Collections.Generic.Dictionary[string,object]' (
        [StringComparer]::Ordinal
    )
    $chunkedRuntimeIdentities = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $previousChunkPath = ''
    foreach ($chunk in @($frontendManifest.distributed_chunks)) {
        Assert-CIJExactProperties `
            -Value $chunk `
            -Expected @('path', 'sha256', 'packages')
        $chunkPath = [string]$chunk.path
        Assert-CIJPortableRelativePath -RelativePath $chunkPath
        if ([string]$chunk.sha256 -cnotmatch '^[0-9a-f]{64}$' -or
            $frontendChunkMap.ContainsKey($chunkPath) -or
            (-not [string]::IsNullOrEmpty($previousChunkPath) -and
                [StringComparer]::Ordinal.Compare(
                    $previousChunkPath,
                    $chunkPath
                ) -ge 0) -or @($chunk.packages).Count -lt 1) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $previousChunkPath = $chunkPath
        $chunkPackageSet = New-Object 'Collections.Generic.HashSet[string]' (
            [StringComparer]::Ordinal
        )
        foreach ($memberValue in @($chunk.packages)) {
            $member = [string]$memberValue
            if (-not $frontendPackageMap.ContainsKey($member) -or
                [string]$frontendPackageMap[$member].dependency_kind -cne 'runtime' -or
                -not $chunkPackageSet.Add($member)) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            [void]$chunkedRuntimeIdentities.Add($member)
        }
        $frontendChunkMap.Add($chunkPath, [pscustomobject]@{
            Manifest = $chunk
            PackageSet = $chunkPackageSet
        })
    }
    if ($frontendChunkMap.Count -lt 1) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    foreach ($runtimeIdentity in $runtimeIdentities) {
        if (-not $chunkedRuntimeIdentities.Contains($runtimeIdentity)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
    }

    $files = New-Object 'Collections.Generic.List[object]'
    $assetRelationships = New-Object 'Collections.Generic.List[object]'
    $assetSha1Values = New-Object 'Collections.Generic.List[string]'
    $assetPaths = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $assetCasePaths = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::OrdinalIgnoreCase
    )
    $assetFileIds = New-Object 'Collections.Generic.Dictionary[string,string]' (
        [StringComparer]::Ordinal
    )
    $assetSpdxExpressions = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    $allSpdxIds = New-Object 'Collections.Generic.HashSet[string]' (
        [StringComparer]::Ordinal
    )
    foreach ($fixedId in @(
        'SPDXRef-DOCUMENT',
        'SPDXRef-Package-FreeAgent',
        'SPDXRef-Package-DistributedAssets'
    )) {
        [void]$allSpdxIds.Add($fixedId)
    }
    $previousAssetPath = ''
    foreach ($asset in $assets) {
        Assert-CIJExactProperties `
            -Value $asset `
            -Expected @(
                'path',
                'size',
                'sha256',
                'origin',
                'source_url',
                'spdx_expression',
                'notice_id',
                'required_files'
            )
        $assetPath = [string]$asset.path
        Assert-CIJPortableRelativePath -RelativePath $assetPath
        if (-not $assetPaths.Add($assetPath) -or
            -not $assetCasePaths.Add($assetPath) -or
            (-not [string]::IsNullOrEmpty($previousAssetPath) -and
                [StringComparer]::Ordinal.Compare(
                    $previousAssetPath,
                    $assetPath
                ) -ge 0)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $assetOrigin = [string]$asset.origin
        $assetExpression = [string]$asset.spdx_expression
        if ($assetOrigin -ceq 'project-owned') {
            if ($assetExpression -cne $projectLicense -or
                [string]$asset.source_url -cne
                    'project://github.com/endview/freeagent' -or
                -not [string]::IsNullOrEmpty([string]$asset.notice_id) -or
                @($asset.required_files).Count -ne 0) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
        } elseif ($assetOrigin -ceq 'third-party') {
            if ([string]$asset.source_url -cnotmatch '^https://[^\s]+$' -or
                [string]$asset.notice_id -cnotmatch '^asset-[0-9a-f]{24}$' -or
                -not $noticeIds.Add([string]$asset.notice_id) -or
                [string]::IsNullOrWhiteSpace($assetExpression) -or
                $assetExpression -cmatch '[\x00\r\n]' -or
                @($asset.required_files).Count -lt 1) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
            $requiredAssetPaths = New-Object 'Collections.Generic.HashSet[string]' (
                [StringComparer]::Ordinal
            )
            $previousRequiredPath = ''
            foreach ($requiredFile in @($asset.required_files)) {
                Assert-CIJExactProperties `
                    -Value $requiredFile `
                    -Expected @('path', 'role', 'size', 'sha256')
                $requiredPath = [string]$requiredFile.path
                Assert-CIJPortableRelativePath -RelativePath $requiredPath
                if (-not $requiredPath.StartsWith(
                    'third_party/',
                    [StringComparison]::Ordinal
                ) -or [string]$requiredFile.role -cnotmatch
                    '^(?:license|notice|attribution|copyright|patent)$' -or
                    [string]$requiredFile.sha256 -cnotmatch '^[0-9a-f]{64}$' -or
                    -not $requiredAssetPaths.Add($requiredPath) -or
                    (-not [string]::IsNullOrEmpty($previousRequiredPath) -and
                        [StringComparer]::Ordinal.Compare(
                            $previousRequiredPath,
                            $requiredPath
                        ) -ge 0)) {
                    Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
                }
                $previousRequiredPath = $requiredPath
                $requiredRecord = Get-CIJInputFileRecord `
                    -Root $Source `
                    -RelativePath $requiredPath
                if ($requiredRecord.Length -ne [int64]$requiredFile.size -or
                    $requiredRecord.Sha256 -cne [string]$requiredFile.sha256) {
                    Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
                }
                if ($inputFilePaths.Add($requiredRecord.Path)) {
                    [void]$inputFiles.Add($requiredRecord)
                }
            }
        } else {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $previousAssetPath = $assetPath
        $assetRecord = Get-CIJInputFileRecord `
            -Root $Source `
            -RelativePath $assetPath
        if ($assetRecord.Length -ne [int64]$asset.size -or
            $assetRecord.Sha256 -cne [string]$asset.sha256) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        if ($inputFilePaths.Add($assetRecord.Path)) {
            [void]$inputFiles.Add($assetRecord)
        }
        $assetSha1 = Get-CIJSha1Bytes -Bytes $assetRecord.Bytes
        [void]$assetSha1Values.Add($assetSha1)
        $fileId = 'SPDXRef-File-' + (
            Get-CIJSha256Bytes -Bytes (
                $script:Utf8NoBom.GetBytes("asset`0$assetPath")
            )
        )
        if (-not $allSpdxIds.Add($fileId)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $assetFileIds.Add($assetPath, $fileId)
        [void]$assetSpdxExpressions.Add($assetExpression)
        [void]$files.Add([ordered]@{
            SPDXID = $fileId
            checksums = @(
                [ordered]@{
                    algorithm = 'SHA1'
                    checksumValue = $assetSha1
                },
                [ordered]@{
                    algorithm = 'SHA256'
                    checksumValue = $assetRecord.Sha256
                }
            )
            copyrightText = 'NOASSERTION'
            fileName = './' + $assetPath
            licenseConcluded = $assetExpression
            licenseInfoInFiles = @($assetExpression)
        })
        [void]$assetRelationships.Add([ordered]@{
            relatedSpdxElement = $fileId
            relationshipType = 'CONTAINS'
            spdxElementId = 'SPDXRef-Package-DistributedAssets'
        })
    }
    foreach ($chunkPath in $frontendChunkMap.Keys) {
        if (-not $assetPaths.Contains($chunkPath) -or
            -not $assetFileIds.ContainsKey($chunkPath)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $asset = @($assets | Where-Object {
            [string]$_.path -ceq $chunkPath
        })
        if ($asset.Count -ne 1 -or
            [string]$asset[0].origin -cne 'third-party' -or
            [string]$asset[0].sha256 -cne
                [string]$frontendChunkMap[$chunkPath].Manifest.sha256) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $chunkLicenses = New-Object 'Collections.Generic.HashSet[string]' (
            [StringComparer]::Ordinal
        )
        foreach ($member in $frontendChunkMap[$chunkPath].PackageSet) {
            [void]$chunkLicenses.Add(
                [string]$frontendPackageMap[$member].declared_license_expression
            )
        }
        if ($chunkLicenses.Count -ne 1 -or
            -not $chunkLicenses.Contains([string]$asset[0].spdx_expression)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
    }
    foreach ($asset in $assets) {
        if ([string]$asset.origin -cne 'third-party') { continue }
        foreach ($requiredFile in @($asset.required_files)) {
            $requiredPath = [string]$requiredFile.path
            $declaredRequiredAsset = @($assets | Where-Object {
                [string]$_.path -ceq $requiredPath -and
                [string]$_.origin -ceq 'third-party'
            })
            if ($declaredRequiredAsset.Count -ne 1) {
                Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
            }
        }
    }
    [string[]]$sortedAssetSha1 = $assetSha1Values.ToArray()
    [Array]::Sort($sortedAssetSha1, [StringComparer]::Ordinal)
    $assetVerificationCode = Get-CIJSha1Bytes -Bytes (
        $script:Utf8NoBom.GetBytes(($sortedAssetSha1 -join ''))
    )
    [string[]]$sortedAssetLicenses = @($assetSpdxExpressions)
    [Array]::Sort($sortedAssetLicenses, [StringComparer]::Ordinal)
    $distributedLicenseConclusion = if ($sortedAssetLicenses.Count -eq 1) {
        $sortedAssetLicenses[0]
    } else {
        'NOASSERTION'
    }

    $packages = New-Object 'Collections.Generic.List[object]'
    [void]$packages.Add([ordered]@{
        SPDXID = 'SPDXRef-Package-FreeAgent'
        copyrightText = 'NOASSERTION'
        downloadLocation = 'NOASSERTION'
        filesAnalyzed = $false
        licenseConcluded = $projectLicense
        licenseDeclared = $projectLicense
        name = 'FreeAgent'
        versionInfo = $RevisionValue
    })
    [void]$packages.Add([ordered]@{
        SPDXID = 'SPDXRef-Package-DistributedAssets'
        copyrightText = 'NOASSERTION'
        downloadLocation = 'NOASSERTION'
        filesAnalyzed = $true
        licenseConcluded = $distributedLicenseConclusion
        licenseDeclared = $distributedLicenseConclusion
        licenseInfoFromFiles = $sortedAssetLicenses
        name = 'FreeAgent distributed assets'
        packageVerificationCode = [ordered]@{
            packageVerificationCodeValue = $assetVerificationCode
        }
        versionInfo = $RevisionValue
    })
    $moduleRelationships = New-Object 'Collections.Generic.List[object]'
    foreach ($module in $modules) {
        $packageId = 'SPDXRef-Package-' + [string]$module.notice_id
        if (-not $allSpdxIds.Add($packageId)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        [void]$packages.Add([ordered]@{
            SPDXID = $packageId
            copyrightText = 'NOASSERTION'
            downloadLocation = 'NOASSERTION'
            filesAnalyzed = $false
            homepage = [string]$module.source_url
            licenseConcluded = 'NOASSERTION'
            licenseDeclared = [string]$module.declared_license_expression
            name = [string]$module.path
            versionInfo = [string]$module.version
        })
        [void]$moduleRelationships.Add([ordered]@{
            relatedSpdxElement = $packageId
            relationshipType = 'DEPENDS_ON'
            spdxElementId = 'SPDXRef-Package-FreeAgent'
        })
    }
    $frontendRelationships = New-Object 'Collections.Generic.List[object]'
    $frontendPackageIds = New-Object 'Collections.Generic.Dictionary[string,string]' (
        [StringComparer]::Ordinal
    )
    foreach ($frontendPackage in $frontendPackages) {
        $identity = [string]$frontendPackage.name + '@' +
            [string]$frontendPackage.version
        $packageId = 'SPDXRef-Package-' +
            [string]$frontendPackage.notice_id
        if (-not $allSpdxIds.Add($packageId)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $frontendPackageIds.Add($identity, $packageId)
        [void]$packages.Add([ordered]@{
            SPDXID = $packageId
            copyrightText = 'NOASSERTION'
            downloadLocation = [string]$frontendPackage.resolved
            filesAnalyzed = $false
            homepage = [string]$frontendPackage.source_url
            licenseConcluded = 'NOASSERTION'
            licenseDeclared =
                [string]$frontendPackage.declared_license_expression
            name = [string]$frontendPackage.name
            versionInfo = [string]$frontendPackage.version
        })
        if ([string]$frontendPackage.dependency_kind -ceq 'runtime') {
            [void]$frontendRelationships.Add([ordered]@{
                relatedSpdxElement = $packageId
                relationshipType = 'DEPENDS_ON'
                spdxElementId = 'SPDXRef-Package-FreeAgent'
            })
        } else {
            [void]$frontendRelationships.Add([ordered]@{
                relatedSpdxElement = 'SPDXRef-Package-FreeAgent'
                relationshipType = 'BUILD_DEPENDENCY_OF'
                spdxElementId = $packageId
            })
        }
    }
    foreach ($chunkPath in $frontendChunkMap.Keys) {
        $fileId = $assetFileIds[$chunkPath]
        foreach ($member in $frontendChunkMap[$chunkPath].PackageSet) {
            [void]$frontendRelationships.Add([ordered]@{
                relatedSpdxElement = $frontendPackageIds[$member]
                relationshipType = 'GENERATED_FROM'
                spdxElementId = $fileId
            })
        }
    }
    $relationships = New-Object 'Collections.Generic.List[object]'
    [void]$relationships.Add([ordered]@{
        relatedSpdxElement = 'SPDXRef-Package-FreeAgent'
        relationshipType = 'DESCRIBES'
        spdxElementId = 'SPDXRef-DOCUMENT'
    })
    [void]$relationships.Add([ordered]@{
        relatedSpdxElement = 'SPDXRef-Package-DistributedAssets'
        relationshipType = 'CONTAINS'
        spdxElementId = 'SPDXRef-Package-FreeAgent'
    })
    foreach ($relationship in $moduleRelationships) {
        [void]$relationships.Add($relationship)
    }
    foreach ($relationship in $frontendRelationships) {
        [void]$relationships.Add($relationship)
    }
    foreach ($relationship in $assetRelationships) {
        [void]$relationships.Add($relationship)
    }

    $inputSetRecords = New-Object 'Collections.Generic.Dictionary[string,byte[]]' (
        [StringComparer]::Ordinal
    )
    foreach ($pair in @(
        @('profile', 'freeagent-spdx-generator-1.0.0'),
        @('revision', $RevisionValue),
        @('created_utc', $CreatedUtcValue),
        @('job_kind', $Kind),
        @('target_goos', $Goos),
        @('target_goarch', $Goarch),
        @('generator_sha256', $GeneratorSha256)
    )) {
        $inputSetRecords.Add(
            [string]$pair[0],
            [byte[]]$script:Utf8NoBom.GetBytes([string]$pair[1])
        )
    }
    foreach ($record in $inputFiles) {
        $name = 'file/' + $record.RelativePath
        if ($inputSetRecords.ContainsKey($name)) {
            $name += '/' + $record.Sha256
        }
        if ($inputSetRecords.ContainsKey($name)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        $inputSetRecords.Add($name, [byte[]]$record.Bytes)
    }
    $inputSetSha256 = Get-CIJCanonicalRecordSetSha256 `
        -Records $inputSetRecords
    $sbom = [ordered]@{
        SPDXID = 'SPDXRef-DOCUMENT'
        creationInfo = [ordered]@{
            created = $CreatedUtcValue
            creators = @('Tool: freeagent-spdx-generator-1.0.0')
        }
        dataLicense = 'CC0-1.0'
        documentNamespace = (
            'https://github.com/endview/freeagent/spdx/2.3/' +
            $RevisionValue + '/' + $inputSetSha256
        )
        files = $files.ToArray()
        hasExtractedLicensingInfos = $extractedLicenses.ToArray()
        name = "freeagent-$RevisionValue.spdx.json"
        packages = $packages.ToArray()
        relationships = $relationships.ToArray()
        spdxVersion = 'SPDX-2.3'
    }
    [byte[]]$sbomBytes = ConvertTo-CIJCanonicalJsonBytes -Value $sbom
    $sbomSha256 = Get-CIJSha256Bytes -Bytes $sbomBytes

    $subjects = New-Object 'Collections.Generic.List[object]'
    [string[]]$basePaths = @($baseState.Records.Keys)
    [Array]::Sort($basePaths, [StringComparer]::Ordinal)
    foreach ($path in $basePaths) {
        [void]$subjects.Add([ordered]@{
            digest = [ordered]@{
                sha256 = [string]$baseState.Records[$path].Sha256
            }
            name = $path
        })
    }
    [void]$subjects.Add([ordered]@{
        digest = [ordered]@{ sha256 = $sbomSha256 }
        name = $script:SupplyChainSbomPath
    })
    $resolvedDependencies = @(
        [ordered]@{
            digest = [ordered]@{ gitCommit = $RevisionValue }
            uri = "git+https://github.com/endview/freeagent@$RevisionValue"
        },
        [ordered]@{
            digest = [ordered]@{ sha256 = $dependencyRecord.Sha256 }
            uri = 'file:testdata/release/dependency-licenses.v1.json'
        },
        [ordered]@{
            digest = [ordered]@{ sha256 = $assetManifestRecord.Sha256 }
            uri = 'file:testdata/release/distributed-assets.v1.json'
        },
        [ordered]@{
            digest = [ordered]@{ sha256 = $frontendManifestRecord.Sha256 }
            uri = 'file:testdata/release/frontend-dependency-licenses.v1.json'
        },
        [ordered]@{
            digest = [ordered]@{ sha256 = $packageLockRecord.Sha256 }
            uri = 'file:internal/controlweb/package-lock.json'
        },
        [ordered]@{
            digest = [ordered]@{ sha256 = $goModRecord.Sha256 }
            uri = 'file:go.mod'
        },
        [ordered]@{
            digest = [ordered]@{ sha256 = $goSumRecord.Sha256 }
            uri = 'file:go.sum'
        }
    )
    $predicate = [ordered]@{
        buildDefinition = [ordered]@{
            buildType = (
                'https://github.com/endview/freeagent/' +
                'buildtypes/ci-release-job/v1'
            )
            externalParameters = [ordered]@{
                jobKind = $Kind
                revision = $RevisionValue
                targetGoarch = $Goarch
                targetGoos = $Goos
            }
            internalParameters = [ordered]@{
                createdUtc = $CreatedUtcValue
                metadataGeneratorSha256 = $GeneratorSha256
            }
            resolvedDependencies = $resolvedDependencies
        }
        runDetails = [ordered]@{
            builder = [ordered]@{
                id = (
                    'https://github.com/endview/freeagent/' +
                    'builders/ci-release-control/v1'
                )
            }
            byproducts = @(
                [ordered]@{
                    digest = [ordered]@{ sha256 = $sbomSha256 }
                    name = $script:SupplyChainSbomPath
                }
            )
        }
    }
    $predicate[
        'https://github.com/endview/freeagent/provenance/metadata/v1'
    ] = [ordered]@{
        authentication = 'none'
        envelope = 'absent'
        runSucceeded = $Succeeded
        securityClaim = 'informational-only'
    }
    $provenance = [ordered]@{
        _type = 'https://in-toto.io/Statement/v1'
        predicate = $predicate
        predicateType = 'https://slsa.dev/provenance/v1'
        subject = $subjects.ToArray()
    }
    [byte[]]$provenanceBytes = ConvertTo-CIJCanonicalJsonBytes `
        -Value $provenance
    $provenanceSha256 = Get-CIJSha256Bytes -Bytes $provenanceBytes

    $checksumRecords = New-Object 'Collections.Generic.Dictionary[string,string]' (
        [StringComparer]::Ordinal
    )
    foreach ($path in $basePaths) {
        $checksumRecords.Add(
            $path,
            [string]$baseState.Records[$path].Sha256
        )
    }
    $checksumRecords.Add($script:SupplyChainSbomPath, $sbomSha256)
    $checksumRecords.Add(
        $script:SupplyChainProvenancePath,
        $provenanceSha256
    )
    [string[]]$checksumPaths = @($checksumRecords.Keys)
    [Array]::Sort($checksumPaths, [StringComparer]::Ordinal)
    $checksumBuilder = New-Object Text.StringBuilder
    foreach ($path in $checksumPaths) {
        [void]$checksumBuilder.Append($checksumRecords[$path])
        [void]$checksumBuilder.Append('  ')
        [void]$checksumBuilder.Append($path)
        [void]$checksumBuilder.Append("`n")
    }
    [byte[]]$checksumBytes = $script:Utf8NoBom.GetBytes(
        $checksumBuilder.ToString()
    )
    $checksumsSha256 = Get-CIJSha256Bytes -Bytes $checksumBytes

    $expectedArtifactRecords =
        New-Object 'Collections.Generic.Dictionary[string,object]' (
            [StringComparer]::Ordinal
        )
    foreach ($path in $basePaths) {
        $expectedArtifactRecords.Add($path, $baseState.Records[$path])
    }
    foreach ($metadataEntry in @(
        [pscustomobject]@{
            Path = $script:SupplyChainSbomPath
            Bytes = $sbomBytes
        },
        [pscustomobject]@{
            Path = $script:SupplyChainProvenancePath
            Bytes = $provenanceBytes
        },
        [pscustomobject]@{
            Path = $script:SupplyChainChecksumsPath
            Bytes = $checksumBytes
        }
    )) {
        $expectedArtifactRecords.Add(
            [string]$metadataEntry.Path,
            (New-CIJArtifactRecordFromBytes `
                -Bytes ([byte[]]$metadataEntry.Bytes))
        )
    }
    $expectedArtifactFileCount = [int]$baseState.FileCount + 3
    $expectedArtifactBytes =
        [int64]$baseState.TotalBytes +
        [int64]$sbomBytes.LongLength +
        [int64]$provenanceBytes.LongLength +
        [int64]$checksumBytes.LongLength
    if ($expectedArtifactFileCount -gt $script:MaximumArtifactFiles -or
        $expectedArtifactBytes -gt $script:MaximumArtifactBytes) {
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
    }
    $expectedArtifactSetSha256 = Get-CIJArtifactSetSha256 `
        -Records $expectedArtifactRecords `
        -TotalBytes $expectedArtifactBytes

    $pending = Join-Path $TemporaryRoot (
        'supply-chain-' + [Guid]::NewGuid().ToString('N')
    )
    $cleanupFailure = $false
    try {
        [void][IO.Directory]::CreateDirectory($pending)
        Assert-CIJStrictDescendant `
            -Parent $TemporaryRoot `
            -Candidate $pending `
            -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
        Assert-CIJNoReparseAncestry `
            -Path $pending `
            -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
        Write-CIJNewFile `
            -Path (Join-Path $pending 'sbom.spdx.json') `
            -Bytes $sbomBytes
        Write-CIJNewFile `
            -Path (Join-Path $pending 'provenance.unsigned.v1.json') `
            -Bytes $provenanceBytes
        Write-CIJNewFile `
            -Path (Join-Path $pending 'checksums.sha256') `
            -Bytes $checksumBytes
        [string[]]$pendingNames = @(
            Get-ChildItem -LiteralPath $pending -Force |
                ForEach-Object { $_.Name }
        )
        [Array]::Sort($pendingNames, [StringComparer]::Ordinal)
        if (($pendingNames -join "`n") -cne
            "checksums.sha256`nprovenance.unsigned.v1.json`nsbom.spdx.json") {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
        }
        Assert-CIJInputRecordsUnchanged -Records $inputFiles
        $baseStateBeforePublish = Assert-CIJArtifactAllowlist `
            -Root $Artifact `
            -Required $Required `
            -Optional $Optional
        if ($baseStateBeforePublish.ArtifactSetSha256 -cne
                $baseState.ArtifactSetSha256 -or
            [int]$baseStateBeforePublish.FileCount -ne
                [int]$baseState.FileCount -or
            [int64]$baseStateBeforePublish.TotalBytes -ne
                [int64]$baseState.TotalBytes) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        if (Test-Path -LiteralPath $destination) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_OUTPUT_EXISTS'
        }
        [IO.Directory]::Move($pending, $destination)
    } catch {
        $primary = $_
        if (Test-Path -LiteralPath $pending -PathType Container) {
            try {
                Assert-CIJStrictDescendant `
                    -Parent $TemporaryRoot `
                    -Candidate $pending `
                    -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
                [IO.Directory]::Delete($pending, $true)
            } catch {
                $cleanupFailure = $true
            }
        }
        if ($cleanupFailure) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
        }
        if ($primary.Exception.Message.StartsWith(
            'CI_RELEASE_JOB_FAIL code=',
            [StringComparison]::Ordinal
        )) {
            throw $primary
        }
        Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_GENERATION_FAILED'
    }
    Write-Host (
        'CI_RELEASE_JOB_SUPPLY_CHAIN_PASS ' +
        "revision=$RevisionValue input_set_sha256=$inputSetSha256"
    )
    return [pscustomobject]@{
        InputSetSha256 = $inputSetSha256
        SbomSha256 = $sbomSha256
        ProvenanceSha256 = $provenanceSha256
        ChecksumsSha256 = $checksumsSha256
        ExpectedArtifactSetSha256 = $expectedArtifactSetSha256
        ExpectedArtifactFileCount = $expectedArtifactFileCount
        ExpectedArtifactBytes = $expectedArtifactBytes
    }
}

function Assert-CIJInitialCacheLayout {
    param([Parameter(Mandatory = $true)][string]$CacheRoot)
    $cache = Get-CIJAbsolutePath -Value $CacheRoot -Code 'CIJ_CACHE_DIRTY'
    if (-not (Test-Path -LiteralPath $cache -PathType Container)) {
        Fail-CIReleaseJob -Code 'CIJ_CACHE_DIRTY'
    }
    Assert-CIJNoReparseAncestry -Path $cache -Code 'CIJ_CACHE_DIRTY'
    $expected = @('build', 'go-tmp', 'gopath', 'module')
    $items = @(Get-ChildItem -LiteralPath $cache -Force)
    [string[]]$actual = @($items | ForEach-Object { $_.Name })
    [Array]::Sort($actual, [StringComparer]::Ordinal)
    if (($expected -join "`n") -cne ($actual -join "`n")) {
        Fail-CIReleaseJob -Code 'CIJ_CACHE_DIRTY'
    }
    foreach ($item in $items) {
        if (-not ($item -is [IO.DirectoryInfo]) -or
            ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
            @(Get-ChildItem -LiteralPath $item.FullName -Force).Count -ne 0) {
            Fail-CIReleaseJob -Code 'CIJ_CACHE_DIRTY'
        }
    }
}

if ($PSCmdlet.ParameterSetName -ceq 'GenerateSupplyChain') {
    $sourceForMetadata = Get-CIJAbsolutePath `
        -Value $SourceRoot `
        -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    $artifactForMetadata = Get-CIJAbsolutePath `
        -Value $ArtifactRoot `
        -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    $moduleCacheForMetadata = Get-CIJAbsolutePath `
        -Value $ModuleCacheRoot `
        -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    $temporaryForMetadata = Get-CIJAbsolutePath `
        -Value $TrustedTemp `
        -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    Assert-CIJDisjointPaths `
        -Paths @(
            $sourceForMetadata,
            $artifactForMetadata,
            $moduleCacheForMetadata,
            $temporaryForMetadata
        ) `
        -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    foreach ($root in @(
        $sourceForMetadata,
        $artifactForMetadata,
        $moduleCacheForMetadata,
        $temporaryForMetadata
    )) {
        Assert-CIJNotFileSystemRoot `
            -Path $root `
            -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        if (-not (Test-Path -LiteralPath $root -PathType Container)) {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        Assert-CIJNoReparseAncestry `
            -Path $root `
            -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
    }
    try {
        New-CIJSupplyChainMetadata `
            -Source $sourceForMetadata `
            -Artifact $artifactForMetadata `
            -ModuleCache $moduleCacheForMetadata `
            -TemporaryRoot $temporaryForMetadata `
            -RevisionValue $Revision `
            -CreatedUtcValue $CreatedUtc `
            -Kind $JobKind `
            -Succeeded $RunSucceeded `
            -GeneratorSha256 $MetadataGeneratorSha256 `
            -Required $RequiredArtifactRelativePath `
            -Optional $OptionalArtifactRelativePath `
            -Goos $TargetGoos `
            -Goarch $TargetGoarch
    } catch {
        if ($_.Exception.Message -cmatch
            '^CI_RELEASE_JOB_FAIL code=CIJ_ARTIFACT_(?:ALLOWLIST_INVALID|LAYOUT_INVALID|TOO_LARGE)') {
            Fail-CIReleaseJob -Code 'CIJ_SUPPLY_CHAIN_INPUT_INVALID'
        }
        throw
    }
    return
}

if ($PSCmdlet.ParameterSetName -ceq 'Prepare') {
    if ($Revision -cnotmatch '^(?:[0-9a-f]{40}|[0-9a-f]{64})$') {
        Fail-CIReleaseJob -Code 'CIJ_REVISION_INVALID'
    }
    $repository = Get-CIJAbsolutePath -Value $RepositoryRoot -Code 'CIJ_REPOSITORY_INVALID'
    $work = Get-CIJAbsolutePath -Value $WorkRoot -Code 'CIJ_WORK_ROOT_INVALID'
    $executionTemp = Get-CIJAbsolutePath `
        -Value $ExecutionTempRoot `
        -Code 'CIJ_EXECUTION_TEMP_INVALID'
    $trusted = Get-CIJAbsolutePath -Value $TrustedTemp -Code 'CIJ_TEMP_INVALID'
    if (-not (Test-Path -LiteralPath $repository -PathType Container)) {
        Fail-CIReleaseJob -Code 'CIJ_REPOSITORY_INVALID'
    }
    if (-not (Test-Path -LiteralPath $trusted -PathType Container)) {
        Fail-CIReleaseJob -Code 'CIJ_TEMP_INVALID'
    }
    Assert-CIJNotFileSystemRoot -Path $repository -Code 'CIJ_REPOSITORY_INVALID'
    Assert-CIJNotFileSystemRoot -Path $trusted -Code 'CIJ_TEMP_INVALID'
    Assert-CIJNotFileSystemRoot -Path $work -Code 'CIJ_WORK_ROOT_INVALID'
    Assert-CIJNotFileSystemRoot -Path $executionTemp -Code 'CIJ_EXECUTION_TEMP_INVALID'
    Assert-CIJNoReparseAncestry -Path $repository -Code 'CIJ_REPOSITORY_INVALID'
    Assert-CIJNoReparseAncestry -Path $trusted -Code 'CIJ_TEMP_INVALID'
    Assert-CIJDisjointPaths `
        -Paths @($repository, $trusted) `
        -Code 'CIJ_ROOTS_OVERLAP'
    Assert-CIJStrictDescendant `
        -Parent $trusted `
        -Candidate $work `
        -Code 'CIJ_WORK_ROOT_INVALID'
    Assert-CIJStrictDescendant `
        -Parent $trusted `
        -Candidate $executionTemp `
        -Code 'CIJ_EXECUTION_TEMP_INVALID'
    Assert-CIJDisjointPaths `
        -Paths @($repository, $work, $executionTemp) `
        -Code 'CIJ_ROOTS_OVERLAP'
    if (Test-Path -LiteralPath $executionTemp) {
        Fail-CIReleaseJob -Code 'CIJ_EXECUTION_TEMP_NOT_EMPTY'
    }

    $bootstrap = Get-CIJAuthenticatedScript `
        -Path $BootstrapPath `
        -ExpectedSha256 $ExpectedBootstrapSha256 `
        -Code 'CIJ_BOOTSTRAP_AUTH_FAILED'
    $generatorPath = Join-Path $repository 'scripts/New-PublicStaging.ps1'
    [void](Get-CIJAuthenticatedScript `
        -Path $generatorPath `
        -ExpectedSha256 $ExpectedGeneratorSha256 `
        -Code 'CIJ_GENERATOR_AUTH_FAILED')
    $verifierPath = Join-Path $repository 'scripts/Test-PublicStaging.ps1'
    [void](Get-CIJAuthenticatedScript `
        -Path $verifierPath `
        -ExpectedSha256 $ExpectedVerifierSha256 `
        -Code 'CIJ_VERIFIER_AUTH_FAILED')

    Invoke-CIJScriptBytes `
        -Bytes ([byte[]]$bootstrap.Bytes) `
        -NamedArguments ([ordered]@{
            RepositoryRoot = $repository
            Revision = $Revision
            WorkRoot = $work
            ExpectedGeneratorSha256 = $ExpectedGeneratorSha256
            ExpectedVerifierSha256 = $ExpectedVerifierSha256
        }) `
        -TrustedTempPath $trusted `
        -FailureCode 'CIJ_BOOTSTRAP_FAILED'

    $source = Join-Path $work 'source'
    $stage = Join-Path $work 'stage'
    $artifact = Join-Path $work 'artifact'
    $cache = Join-Path $work 'cache'
    $bootstrapTemp = Join-Path $work 'temp'
    $manifest = Join-Path $artifact 'public-tree-manifest.v1.json'
    Assert-CIJDisjointPaths `
        -Paths @($source, $stage, $artifact, $cache, $bootstrapTemp, $executionTemp) `
        -Code 'CIJ_ROOTS_OVERLAP'
    if (-not (Test-Path -LiteralPath $manifest -PathType Leaf)) {
        Fail-CIReleaseJob -Code 'CIJ_MANIFEST_MISSING'
    }
    [void](Get-CIJAuthenticatedScript `
        -Path (Join-Path $source 'scripts/New-PublicStaging.ps1') `
        -ExpectedSha256 $ExpectedGeneratorSha256 `
        -Code 'CIJ_GENERATOR_AUTH_FAILED')
    [void](Get-CIJAuthenticatedScript `
        -Path (Join-Path $source 'scripts/Test-PublicStaging.ps1') `
        -ExpectedSha256 $ExpectedVerifierSha256 `
        -Code 'CIJ_VERIFIER_AUTH_FAILED')
    [byte[]]$manifestBytes = Read-CIJBoundedRegularFile `
        -Path $manifest `
        -MaximumBytes $script:MaximumManifestBytes `
        -Code 'CIJ_MANIFEST_INVALID'
    $manifestHash = Get-CIJSha256Bytes -Bytes $manifestBytes
    Assert-CIJHash -Value $manifestHash
    Assert-CIJInitialCacheLayout -CacheRoot $cache
    $initialArtifactState = Assert-CIJArtifactAllowlist `
        -Root $artifact `
        -Required @('public-tree-manifest.v1.json') `
        -Optional @()
    try {
        [void][IO.Directory]::CreateDirectory($executionTemp)
    } catch {
        Fail-CIReleaseJob -Code 'CIJ_EXECUTION_TEMP_CREATE_FAILED'
    }
    Assert-CIJNoReparseAncestry `
        -Path $executionTemp `
        -Code 'CIJ_EXECUTION_TEMP_CREATE_FAILED'
    if (@(Get-ChildItem -LiteralPath $executionTemp -Force).Count -ne 0) {
        Fail-CIReleaseJob -Code 'CIJ_EXECUTION_TEMP_NOT_EMPTY'
    }
    Write-Host (
        'CI_RELEASE_JOB_PREPARE_PASS ' +
        "revision=$Revision manifest_sha256=$manifestHash"
    )
    [pscustomobject]@{
        Revision = $Revision
        ManifestSha256 = $manifestHash
        ManifestPath = $manifest
        InitialArtifactSetSha256 = $initialArtifactState.ArtifactSetSha256
        SourceRoot = $source
        StageRoot = $stage
        ArtifactRoot = $artifact
        CacheRoot = $cache
        BootstrapTempRoot = $bootstrapTemp
        ExecutionTempRoot = $executionTemp
    }
    return
}

Assert-CIJHash -Value $ManifestSha256
if (-not [string]::IsNullOrWhiteSpace($ExpectedArtifactSetSha256)) {
    Assert-CIJHash -Value $ExpectedArtifactSetSha256
}
$sourceFull = Get-CIJAbsolutePath -Value $SourceRoot -Code 'CIJ_SOURCE_INVALID'
$stageFull = Get-CIJAbsolutePath -Value $StageRoot -Code 'CIJ_STAGE_INVALID'
$artifactFull = Get-CIJAbsolutePath -Value $ArtifactRoot -Code 'CIJ_ARTIFACT_LAYOUT_INVALID'
$manifestFull = Get-CIJAbsolutePath -Value $ManifestPath -Code 'CIJ_MANIFEST_INVALID'
$trustedFull = Get-CIJAbsolutePath -Value $TrustedTemp -Code 'CIJ_TEMP_INVALID'
Assert-CIJDisjointPaths `
    -Paths @($sourceFull, $stageFull, $artifactFull, $trustedFull) `
    -Code 'CIJ_ROOTS_OVERLAP'
foreach ($rootPath in @($sourceFull, $stageFull, $artifactFull, $trustedFull)) {
    Assert-CIJNotFileSystemRoot -Path $rootPath -Code 'CIJ_VERIFY_LAYOUT_INVALID'
}
$sourceParent = [IO.Path]::GetDirectoryName($sourceFull)
$stageParent = [IO.Path]::GetDirectoryName($stageFull)
$artifactParent = [IO.Path]::GetDirectoryName($artifactFull)
if (-not [string]::Equals($sourceParent, $stageParent, $script:PathComparison) -or
    -not [string]::Equals($sourceParent, $artifactParent, $script:PathComparison) -or
    [IO.Path]::GetFileName($sourceFull) -cne 'source' -or
    [IO.Path]::GetFileName($stageFull) -cne 'stage' -or
    [IO.Path]::GetFileName($artifactFull) -cne 'artifact') {
    Fail-CIReleaseJob -Code 'CIJ_VERIFY_LAYOUT_INVALID'
}
$expectedManifestFull = Get-CIJAbsolutePath `
    -Value (Join-Path $artifactFull 'public-tree-manifest.v1.json') `
    -Code 'CIJ_MANIFEST_INVALID'
if (-not [string]::Equals(
    $manifestFull,
    $expectedManifestFull,
    $script:PathComparison
)) {
    Fail-CIReleaseJob -Code 'CIJ_MANIFEST_INVALID'
}
foreach ($directory in @($sourceFull, $stageFull, $artifactFull, $trustedFull)) {
    if (-not (Test-Path -LiteralPath $directory -PathType Container)) {
        Fail-CIReleaseJob -Code 'CIJ_VERIFY_LAYOUT_MISSING'
    }
    Assert-CIJNoReparseAncestry -Path $directory -Code 'CIJ_VERIFY_LAYOUT_INVALID'
}
if (-not (Test-Path -LiteralPath $manifestFull -PathType Leaf)) {
    Fail-CIReleaseJob -Code 'CIJ_MANIFEST_INVALID'
}
[byte[]]$currentManifestBytes = Read-CIJBoundedRegularFile `
    -Path $manifestFull `
    -MaximumBytes $script:MaximumManifestBytes `
    -Code 'CIJ_MANIFEST_INVALID'
if ((Get-CIJSha256Bytes -Bytes $currentManifestBytes) -cne $ManifestSha256) {
    Fail-CIReleaseJob -Code 'CIJ_MANIFEST_PIN_MISMATCH'
}
$verifier = Get-CIJAuthenticatedScript `
    -Path (Join-Path $sourceFull 'scripts/Test-PublicStaging.ps1') `
    -ExpectedSha256 $ExpectedVerifierSha256 `
    -Code 'CIJ_VERIFIER_AUTH_FAILED'
foreach ($rootToVerify in @($sourceFull, $stageFull)) {
    Invoke-CIJScriptBytes `
        -Bytes ([byte[]]$verifier.Bytes) `
        -NamedArguments ([ordered]@{
            Root = $rootToVerify
            ManifestPath = $manifestFull
            ManifestSha256 = $ManifestSha256
        }) `
        -TrustedTempPath $trustedFull `
        -FailureCode 'CIJ_TREE_VERIFY_FAILED'
}
$artifactState = Assert-CIJArtifactAllowlist `
    -Root $artifactFull `
    -Required $RequiredArtifactRelativePath `
    -Optional $OptionalArtifactRelativePath
if (-not [string]::IsNullOrWhiteSpace($ExpectedArtifactSetSha256) -and
    $artifactState.ArtifactSetSha256 -cne $ExpectedArtifactSetSha256) {
    Fail-CIReleaseJob -Code 'CIJ_ARTIFACT_SET_PIN_MISMATCH'
}
Write-Host (
    'CI_RELEASE_JOB_VERIFY_PASS ' +
    "manifest_sha256=$ManifestSha256 " +
    "artifact_set_sha256=$($artifactState.ArtifactSetSha256) " +
    "files=$($artifactState.FileCount) bytes=$($artifactState.TotalBytes)"
)
[pscustomobject]@{
    ManifestSha256 = $ManifestSha256
    ArtifactSetSha256 = $artifactState.ArtifactSetSha256
    ArtifactFileCount = $artifactState.FileCount
    ArtifactBytes = $artifactState.TotalBytes
}
